package main

// hi watch, on the server: each team's __serve sends a signed heartbeat
// every minute (team_watch.go), and the server watches what the team's
// broker can't see from inside: a machine that stopped, parts that are
// down, a disk filling up, snapshots that stopped, model spend that runs
// away or happens outside hi. Rules are plain code, no model. Incidents go
// to the server's watch channel, a stopped heartbeat also to the owners by
// push, and what the team's lead can act on back to the team in the
// heartbeat's answer. The network log each heartbeat carries is kept here,
// off the team's machine.
//
// Spec: docs/specs/approved/hi_watch.md (release 1).

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/slack-go/slack"
)

const (
	// watchSilentAfter is how long a team may send nothing before it is an
	// incident, and watchSilentAfterRestart after a stop it said was a
	// restart.
	watchSilentAfter        = 3 * time.Minute
	watchSilentAfterRestart = 10 * time.Minute
	watchNetworkLinesMax    = 2000
	watchLineMax            = 1024
	watchDeadmanEvery       = time.Minute
	watchKeep               = 7 * 24 * time.Hour
)

// apiHeartbeat is what a team sends every minute.
type apiHeartbeat struct {
	Team       string                  `json:"team"`
	At         time.Time               `json:"at"`
	Hi         string                  `json:"hi"`
	Production string                  `json:"production,omitempty"`
	Parts      map[string]apiWatchPart `json:"parts"`
	Restarts   int                     `json:"serve_restarts_24h"`
	Disk       apiWatchDisk            `json:"disk"`
	OOMKills   int                     `json:"oom_kills_24h"`
	Snapshot   apiWatchSnapshot        `json:"snapshot"`
	Incidents  int                     `json:"incidents_open"`
	Spend      float64                 `json:"spend_1h_usd"`
	KeyUsage   *float64                `json:"key_usage_1h_usd,omitempty"`
	Window     int                     `json:"spend_window_minutes,omitempty"`
	Network    *apiWatchNetwork        `json:"network,omitempty"`
	Stopping   string                  `json:"stopping,omitempty"` // "restart" or "down"
	// Stop is how the team is stopped (release 3), and Undeclared what
	// production's box has that its bill doesn't.
	Stop       apiWatchStop   `json:"stop"`
	Undeclared *apiUndeclared `json:"undeclared,omitempty"`
	// KeyHash is the server-made key the team uses (release 4).
	KeyHash string `json:"key_hash,omitempty"`
	// Bom is the digest of production's bill of materials, and BomVersion
	// the version it is of (team_bom.go); Ignored the advisories an owner
	// said don't apply.
	// Logs are the network logs besides the lead's: the app boxes'
	// lookups ("apps") and each member box's ("box:<name>"); Exempt the
	// hosts big uploads may go to, such as the snapshots' bucket.
	Logs       []apiWatchNetwork    `json:"logs,omitempty"`
	Exempt     []string             `json:"exempt,omitempty"`
	Bom        string               `json:"bom,omitempty"`
	BomVersion string               `json:"bom_version,omitempty"`
	Ignored    []teamAdvisoryIgnore `json:"ignored,omitempty"`
}

type apiWatchPart struct {
	State       string    `json:"state"` // "up", "down", "off"
	Detail      string    `json:"detail,omitempty"`
	LastEvent   time.Time `json:"last_event,omitzero"`
	Restarts1h  int       `json:"restarts_1h,omitempty"`
	Restarts24h int       `json:"restarts_24h,omitempty"`
}

type apiWatchDisk struct {
	Team       float64 `json:"team"`
	Containers float64 `json:"containers"`
	Inodes     float64 `json:"inodes"`
}

type apiWatchSnapshot struct {
	Last       time.Time `json:"last,omitzero"`
	Offsite    time.Time `json:"offsite,omitzero"`
	OffsiteSet bool      `json:"offsite_set,omitempty"`
}

// apiWatchNetwork is the team's network log from byte From to To.
type apiWatchNetwork struct {
	Log     string   `json:"log,omitempty"` // "" for the lead's
	From    int64    `json:"from"`
	To      int64    `json:"to"`
	Lines   []string `json:"lines"`
	Rotated bool     `json:"rotated,omitempty"`
}

// apiHeartbeatReply says how far the server has kept the network log, and
// the server's open incidents the team's channel should hear of.
type apiHeartbeatReply struct {
	NetworkKept int64              `json:"network_kept"`
	Tell        []apiWatchIncident `json:"tell,omitempty"`
	// NeedBom asks for the bill the heartbeat named; Advisories are what
	// matches production's; Report is a weekly report for the team's
	// channel, posted once by its ID (server_advisories.go).
	LogsKept map[string]int64 `json:"logs_kept,omitempty"`
	Do       *apiWatchDo      `json:"do,omitempty"`
	// NewKey is a key an admin had the server make the team (rekey),
	// until the team's heartbeat says it uses it.
	NewKey     *apiWatchKey       `json:"new_key,omitempty"`
	NeedBom    bool               `json:"need_bom,omitempty"`
	Advisories []apiAdvisory      `json:"advisories,omitempty"`
	Counts     []apiAdvisoryCount `json:"advisory_counts,omitempty"`
	Report     *apiWatchReport    `json:"report,omitempty"`
}

type apiWatchIncident struct {
	ID     string `json:"id"`
	Rule   string `json:"rule"`
	Title  string `json:"title"`
	Detail string `json:"detail,omitempty"`
}

// ---------------------------------------------------------------------------
// state, in watch/ beside state.json

type watchConfig struct {
	Deadman    string `json:"deadman,omitempty"`
	Ntfy       string `json:"ntfy,omitempty"`
	NtfyToken  string `json:"ntfy_token,omitempty"`
	TopicStart string `json:"topic_start,omitempty"`
	Channel    string `json:"channel,omitempty"`
	// AutoFreeze is "off" to have every freeze wait for an admin.
	AutoFreeze string `json:"auto_freeze,omitempty"`
}

type watchState struct {
	Teams     map[string]*watchTeam     `json:"teams"`
	Incidents map[string]*watchIncident `json:"incidents"`
	Next      int                       `json:"next"`
	StopNext  int                       `json:"stop_next,omitempty"`
}

type watchTeam struct {
	Team        string            `json:"team"`
	Device      string            `json:"device"`
	User        string            `json:"user"`
	Hostname    string            `json:"hostname"`
	First       time.Time         `json:"first"`
	Last        time.Time         `json:"last"`
	Beat        apiHeartbeat      `json:"beat"`
	Stopping    string            `json:"stopping,omitempty"`
	StoppingAt  time.Time         `json:"stopping_at,omitzero"`
	NetworkKept int64             `json:"network_kept"`
	Down        map[string]int    `json:"down,omitempty"`  // heartbeats in a row each part wasn't up
	Spend       []watchSample     `json:"spend,omitempty"` // one an hour, for the week's average
	Disk        []watchSample     `json:"disk,omitempty"`  // one a day, for the week's rate
	Open        map[string]string `json:"open,omitempty"`  // rule → incident
	// Production's bill, what matched it and when, and the weekly report.
	Bom        string          `json:"bom,omitempty"`
	BomVersion string          `json:"bom_version,omitempty"`
	Matched    time.Time       `json:"matched,omitzero"`
	MatchTried time.Time       `json:"match_tried,omitzero"`
	Advisories []apiAdvisory   `json:"-"` // in teams/<team>/advisories.json
	Reported   time.Time       `json:"reported,omitzero"`
	NoFixTold  map[string]bool `json:"no_fix_told,omitempty"`
	// ImageTotals are each image's advisories at the last report.
	ImageTotals map[string]int  `json:"image_totals,omitempty"`
	Report      *apiWatchReport `json:"report,omitempty"`
	// The network rules' state: how far each log besides the lead's is
	// kept, when each rule last fired, the hosts production looked up and
	// those new this week, and what the team was asked to do.
	LogsKept      map[string]int64     `json:"logs_kept,omitempty"`
	NetSeen       map[string]time.Time `json:"net_seen,omitempty"`
	AppHosts      map[string]time.Time `json:"app_hosts,omitempty"`
	AppHostsSince time.Time            `json:"app_hosts_since,omitzero"`
	NewHosts      []string             `json:"new_hosts,omitempty"`
	NetReported   time.Time            `json:"net_reported,omitzero"`
	Want          *apiWatchDo          `json:"want,omitempty"`
	WantDone      bool                 `json:"want_done,omitempty"`
	// Release 4: the key the server made the team (its hash at
	// OpenRouter), whether an admin revoked it, the NetBird address its
	// heartbeats come from, and how it was cut off.
	KeyHash    string       `json:"key_hash,omitempty"`
	KeyRevoked bool         `json:"key_revoked,omitempty"`
	PeerIP     string       `json:"peer_ip,omitempty"`
	CutOff     *watchCutOff `json:"cut_off,omitempty"`
}

type watchSample struct {
	At    time.Time `json:"at"`
	Value float64   `json:"value"`
}

type watchIncident struct {
	ID      string    `json:"id"`
	Team    string    `json:"team"`
	Rule    string    `json:"rule"`
	Title   string    `json:"title"`
	Detail  string    `json:"detail,omitempty"`
	Opened  time.Time `json:"opened"`
	Closed  time.Time `json:"closed,omitzero"`
	Cleared string    `json:"cleared,omitempty"`
	Tell    bool      `json:"tell,omitempty"`
	Push    bool      `json:"push,omitempty"`
	SlackTS string    `json:"slack_ts,omitempty"`
}

type serverWatch struct {
	mu     sync.Mutex
	dir    string
	config watchConfig
	state  watchState
	pinged time.Time
	// matchMu keeps one advisory match at a time; matching says one runs
	// for the teams that are due.
	net      map[string]*watchNet
	hosts    []string
	hostsAt  time.Time
	matchMu  sync.Mutex
	matching atomic.Bool
	feeds    *advisoryFeeds

	// local is a team's own watcher, on its machine: it keeps no copy of
	// the logs it reads (team_watch_local.go).
	local bool

	// push and ping are replaced in tests.
	push func(config watchConfig, topic, title, text string) error
	ping func(url string) error
}

func watchDir(serverDir string) string { return filepath.Join(serverDir, "watch") }

func openServerWatch(serverDir string) *serverWatch {
	w := &serverWatch{dir: watchDir(serverDir), push: watchPushNtfy, ping: watchPing}
	w.feeds = &advisoryFeeds{dir: filepath.Join(w.dir, "feeds")}
	if data, err := os.ReadFile(filepath.Join(w.dir, "config.json")); err == nil {
		json.Unmarshal(data, &w.config)
	}
	if data, err := os.ReadFile(filepath.Join(w.dir, "state.json")); err == nil {
		json.Unmarshal(data, &w.state)
	}
	if w.state.Teams == nil {
		w.state.Teams = map[string]*watchTeam{}
	}
	if w.state.Incidents == nil {
		w.state.Incidents = map[string]*watchIncident{}
	}
	for name, team := range w.state.Teams {
		if data, err := os.ReadFile(w.advisoriesPath(name)); err == nil {
			json.Unmarshal(data, &team.Advisories)
		}
		// A match that didn't end, such as one the server was stopped in,
		// is tried again now rather than in an hour.
		if team.Bom != "" && team.MatchTried.After(team.Matched) {
			team.MatchTried = time.Time{}
		}
	}
	return w
}

func (w *serverWatch) saveLocked() error {
	if err := os.MkdirAll(w.dir, 0o700); err != nil {
		return err
	}
	data, _ := json.MarshalIndent(w.state, "", "  ")
	return writeFileAtomic(filepath.Join(w.dir, "state.json"), append(data, '\n'), 0o600)
}

func (w *serverWatch) saveConfigLocked() error {
	if err := os.MkdirAll(w.dir, 0o700); err != nil {
		return err
	}
	data, _ := json.MarshalIndent(w.config, "", "  ")
	return writeFileAtomic(filepath.Join(w.dir, "config.json"), append(data, '\n'), 0o600)
}

// topic is a team's ntfy topic.
func (c watchConfig) topic(team string) string {
	return firstNonEmpty(c.TopicStart, "hi-watch") + "-" + team
}

// ---------------------------------------------------------------------------
// the heartbeat

func (s *hiServer) handleHeartbeat(w http.ResponseWriter, r *http.Request, device serverDevice, body []byte) {
	var beat apiHeartbeat
	if err := json.Unmarshal(body, &beat); err != nil || !teamNamePattern.MatchString(beat.Team) {
		writeAPIError(w, http.StatusBadRequest, "malformed heartbeat")
		return
	}
	// From hi 2.6.32 a team watches itself on its own machine
	// (hi_team_ops.md, release 3): a heartbeat from an older one closes
	// what the server had open for it, and is turned away.
	s.watchEvents(s.watch.retire(beat.Team, computeNow()))
	writeAPIError(w, http.StatusGone, "teams watch themselves on their own machine from hi v2.6.32 (hi team watch); update hi on the team's machine")
}

// retire closes a team's open watch incidents, once: the team watches
// itself now.
func (w *serverWatch) retire(name string, now time.Time) []watchEvent {
	w.mu.Lock()
	defer w.mu.Unlock()
	team := w.state.Teams[name]
	if team == nil || len(team.Open) == 0 {
		return nil
	}
	var events []watchEvent
	for rule := range team.Open {
		events = append(events, w.ruleLocked(team, rule, false, now, nil, "The team watches itself now, on its own machine (hi team watch).")...)
	}
	w.saveLocked()
	return events
}

var _ = (*serverWatch).heartbeat

func (w *serverWatch) heartbeat(s *hiServer, device serverDevice, beat apiHeartbeat, now time.Time) (apiHeartbeatReply, error) {
	w.mu.Lock()
	team := w.state.Teams[beat.Team]
	if team != nil && team.Device != device.Fingerprint {
		w.mu.Unlock()
		return apiHeartbeatReply{}, fmt.Errorf("team %s is watched from another machine (%s); on the server, hi server watch forget %s lets this one take it over", beat.Team, team.Hostname, beat.Team)
	}
	if team == nil {
		team = &watchTeam{Team: beat.Team, Device: device.Fingerprint, User: device.User, Hostname: device.Hostname, First: now}
		w.state.Teams[beat.Team] = team
		s.audit(device.User, "watching", beat.Team, "from "+device.Hostname)
	}
	team.User, team.Hostname = device.User, device.Hostname
	reply, events, confirmed, weekly := w.beatLocked(team, beat, now)
	if beat.Stopping == "" {
		secrets := w.loadSecretsLocked()
		if key := secrets.Pending[team.Team]; key != "" {
			if beat.KeyHash == team.KeyHash {
				delete(secrets.Pending, team.Team)
				w.saveSecretsLocked(secrets)
			} else {
				reply.NewKey = &apiWatchKey{Key: key, Hash: team.KeyHash}
			}
		}
	}
	w.saveLocked()
	w.mu.Unlock()
	s.watchEvents(events)
	if confirmed != "" {
		s.audit("watch", "carried out", beat.Team, confirmed)
		s.watchPost("", ":white_check_mark: "+confirmed, false)
	}
	if weekly != "" {
		s.watchPost("", weekly, false)
	}
	return reply, nil
}

// beatLocked keeps a heartbeat's logs, runs the rules on it, and answers:
// the server's for every team, and a team's own watcher for itself
// (team_watch_local.go).
func (w *serverWatch) beatLocked(team *watchTeam, beat apiHeartbeat, now time.Time) (apiHeartbeatReply, []watchEvent, string, string) {
	network, logs := beat.Network, beat.Logs
	beat.Network, beat.Logs = nil, nil
	if beat.Stopping != "" {
		// A last word before a stop: kept, but the parts it describes are
		// the ones that are stopping.
		team.Stopping, team.StoppingAt, team.Last = beat.Stopping, now, now
	} else {
		team.Stopping, team.StoppingAt = "", time.Time{}
		team.Beat, team.Last = beat, now
	}
	var events []watchEvent
	if network != nil {
		events = append(events, w.keepLogLocked(team, *network, beat.Exempt, now)...)
	}
	for _, log := range logs {
		if log.Log != "" {
			events = append(events, w.keepLogLocked(team, log, beat.Exempt, now)...)
		}
	}
	reply := apiHeartbeatReply{NetworkKept: team.NetworkKept, LogsKept: team.LogsKept}
	confirmed, weekly := "", ""
	if beat.Stopping == "" {
		events = append(events, w.rulesLocked(team, now)...)
		quiet, text := w.networkQuietLocked(team, now)
		events, weekly = append(events, quiet...), text
		confirmed = w.stopConfirmedLocked(team, beat.Stop)
		// Production's bill: the server asks for one it doesn't have.
		if beat.Bom != "" && beat.Bom != team.Bom {
			if fileExists(filepath.Join(w.dir, "teams", team.Team, "boms", strings.TrimPrefix(beat.Bom, "sha256:")+".cdx.json")) {
				team.Bom, team.BomVersion, team.Matched = beat.Bom, beat.BomVersion, time.Time{}
			} else {
				reply.NeedBom = true
			}
		}
		// What owners said doesn't apply takes effect at once.
		events = append(events, w.applyIgnoresLocked(team, now)...)
		reply.Advisories, reply.Counts = teamFacingAdvisories(team.Advisories)
		reply.Report = team.Report
	}
	for _, id := range team.Open {
		if incident := w.state.Incidents[id]; incident != nil && incident.Tell {
			reply.Tell = append(reply.Tell, apiWatchIncident{ID: id, Rule: incident.Rule, Title: incident.Title, Detail: incident.Detail})
		}
	}
	sort.Slice(reply.Tell, func(i, j int) bool { return reply.Tell[i].ID < reply.Tell[j].ID })
	if team.Want != nil && !team.WantDone {
		do := *team.Want
		reply.Do = &do
	}
	return reply, events, confirmed, weekly
}

// applyIgnoresLocked marks the advisories an owner said don't apply, as
// long as the advisory hasn't changed since, and opens or closes their
// incidents.
func (w *serverWatch) applyIgnoresLocked(team *watchTeam, now time.Time) []watchEvent {
	if len(team.Advisories) == 0 {
		return nil
	}
	for i := range team.Advisories {
		team.Advisories[i].Ignored = ""
		for _, ignore := range team.Beat.Ignored {
			if ignore.ID == team.Advisories[i].ID && ignore.Package == team.Advisories[i].Package && ignore.Modified == team.Advisories[i].Modified {
				team.Advisories[i].Ignored = firstNonEmpty(ignore.Because, "ignored")
			}
		}
	}
	return w.advisoryRulesLocked(team, now)
}

// ---------------------------------------------------------------------------
// rules

// watchEvent is an incident opened or closed, told outside the lock.
type watchEvent struct {
	incident watchIncident
	closed   bool
}

// ruleLocked opens the rule's incident when on and none is open, and
// closes it when off.
func (w *serverWatch) ruleLocked(team *watchTeam, rule string, on bool, now time.Time, open func() watchIncident, cleared string) []watchEvent {
	if team.Open == nil {
		team.Open = map[string]string{}
	}
	id := team.Open[rule]
	switch {
	case on && id == "":
		incident := open()
		w.state.Next++
		incident.ID, incident.Team, incident.Rule, incident.Opened = fmt.Sprintf("w-%d", w.state.Next), team.Team, rule, now.UTC()
		w.state.Incidents[incident.ID] = &incident
		team.Open[rule] = incident.ID
		return []watchEvent{{incident: incident}}
	case !on && id != "":
		delete(team.Open, rule)
		incident := w.state.Incidents[id]
		if incident == nil {
			return nil
		}
		incident.Closed, incident.Cleared = now.UTC(), cleared
		return []watchEvent{{incident: *incident, closed: true}}
	}
	return nil
}

// rulesLocked runs the health rules on a team's last heartbeat.
func (w *serverWatch) rulesLocked(team *watchTeam, now time.Time) []watchEvent {
	beat := team.Beat
	var events []watchEvent
	add := func(rule string, on bool, cleared string, open func() watchIncident) {
		events = append(events, w.ruleLocked(team, rule, on, now, open, cleared)...)
	}
	// Heartbeats came again.
	add("heartbeat", false, fmt.Sprintf("Heartbeats came again (serve_restarts_24h: %d).", beat.Restarts), nil)

	// Parts: down for 3 heartbeats in a row; the gateway for 10, since
	// Slack's own reconnects take minutes.
	if team.Down == nil {
		team.Down = map[string]int{}
	}
	names := make([]string, 0, len(beat.Parts))
	for name := range beat.Parts {
		names = append(names, name)
	}
	sort.Strings(names)
	for name := range team.Down {
		if _, ok := beat.Parts[name]; !ok {
			delete(team.Down, name)
		}
	}
	for _, name := range names {
		part := beat.Parts[name]
		// A frozen or paused team's parts are down on purpose.
		if part.State == "up" || part.State == "off" || beat.Stop.Frozen || beat.Stop.Paused {
			team.Down[name] = 0
		} else {
			team.Down[name]++
		}
		limit := 3
		if name == "gateway" {
			limit = 10
		}
		add("part:"+name, team.Down[name] >= limit, fmt.Sprintf("%s is up again.", name), func() watchIncident {
			return watchIncident{Title: fmt.Sprintf("%s's %s has been %s for %d minutes", team.Team, name, firstNonEmpty(part.State, "down"), team.Down[name]),
				Detail: part.Detail, Tell: name == "production" || name == "memory"}
		})
	}
	if production, ok := beat.Parts["production"]; ok {
		add("production-restarts", production.Restarts1h > 3, "Production has stopped restarting.", func() watchIncident {
			return watchIncident{Title: fmt.Sprintf("%s's production stopped %d times in the last hour", team.Team, production.Restarts1h), Tell: true}
		})
	}
	add("serve-restarts", beat.Restarts > 3, "The team has stopped restarting on its own.", func() watchIncident {
		return watchIncident{Title: fmt.Sprintf("%s restarted on its own %d times in a day", team.Team, beat.Restarts),
			Detail: "systemd starts hi team __serve again when it stops without being asked (a crash, a kill); updates and hi team up don't count. This many means it keeps stopping: journalctl --user -u hi-team-" + team.Team + " on the team's machine says why."}
	})
	add("oom", beat.OOMKills > 0, "No box was killed for memory in the last day.", func() watchIncident {
		return watchIncident{Title: fmt.Sprintf("A box of %s was killed for memory (%d in a day)", team.Team, beat.OOMKills), Tell: true}
	})

	// Disk: over 85%, or full within 3 days at the week's rate.
	if n := len(team.Disk); n == 0 || now.Sub(team.Disk[n-1].At) >= 24*time.Hour {
		team.Disk = append(team.Disk, watchSample{At: now, Value: beat.Disk.Team})
	}
	team.Disk = watchTrim(team.Disk, now)
	fullest := max(beat.Disk.Team, beat.Disk.Containers, beat.Disk.Inodes)
	days := watchDaysLeft(team.Disk, beat.Disk.Team, now)
	add("disk", fullest > 0.85 || days < 3, "The disk has room again.", func() watchIncident {
		title := fmt.Sprintf("%s's disk is %.0f%% full", team.Team, fullest*100)
		if days < 3 {
			title = fmt.Sprintf("%s's disk fills up in about %.1f days at this week's rate (%.0f%% now)", team.Team, days, beat.Disk.Team*100)
		}
		return watchIncident{Title: title, Detail: fmt.Sprintf("Team folder %.0f%%, containers %.0f%%, inodes %.0f%%.", beat.Disk.Team*100, beat.Disk.Containers*100, beat.Disk.Inodes*100), Tell: true}
	})

	// Snapshots: one a day, and its copy offsite when set. A new team has a
	// day to take its first.
	old := func(at time.Time) bool { return now.Sub(firstNonZeroTime(at, team.First)) > 26*time.Hour }
	add("snapshot", old(beat.Snapshot.Last), "A snapshot was taken.", func() watchIncident {
		return watchIncident{Title: fmt.Sprintf("%s has taken no snapshot in over 26 hours", team.Team), Tell: true}
	})
	add("offsite", beat.Snapshot.OffsiteSet && old(beat.Snapshot.Offsite), "The snapshots were copied offsite.", func() watchIncident {
		return watchIncident{Title: fmt.Sprintf("%s's snapshots haven't been copied offsite in over 26 hours", team.Team), Tell: true}
	})

	// Spend: a loop shows as an hour over three times the week's hourly
	// average, once there is a day of hours to average.
	if n := len(team.Spend); n == 0 || now.Sub(team.Spend[n-1].At) >= time.Hour {
		team.Spend = append(team.Spend, watchSample{At: now, Value: beat.Spend})
	}
	team.Spend = watchTrim(team.Spend, now)
	average, hours := watchAverage(team.Spend)
	add("spend", hours >= 24 && beat.Spend > 5 && beat.Spend > 3*average, "Model spend is back to normal.", func() watchIncident {
		return watchIncident{Title: fmt.Sprintf("%s's model calls cost $%.2f in the last hour, %.0f times the week's hourly average", team.Team, beat.Spend, beat.Spend/max(average, 0.01)), Tell: true}
	})
	// The key spending more than hi's proxies counted: someone uses it
	// outside hi. Not told to the team: its lead may be what uses it.
	outside := 0.0
	if beat.KeyUsage != nil {
		outside = *beat.KeyUsage - beat.Spend
	}
	add("key", outside > 1, "The key spends no more than hi counts again.", func() watchIncident {
		return watchIncident{Title: fmt.Sprintf("%s's OpenRouter key spent $%.2f outside hi in the last %d minutes", team.Team, outside, beat.Window),
			Detail: fmt.Sprintf("OpenRouter counts $%.2f on the key; hi's proxies counted $%.2f. The key never enters a box, so spend hi didn't see was made elsewhere with it: rotate it at openrouter.ai and set the new one with hi team key %s openrouter.", *beat.KeyUsage, beat.Spend, team.Team)}
	})
	// What production's box has that its bill doesn't: the bill missed
	// it, or something installed it (the team looks once a week).
	undeclared := beat.Undeclared
	add("undeclared", undeclared != nil && undeclared.Count > 0, "Production's box has only what its bill says again.", func() watchIncident {
		return watchIncident{Title: fmt.Sprintf("%s's production box has %d packages its bill of materials doesn't list", team.Team, undeclared.Count),
			Detail: "Either the bill missed them or something installed them at run time: " + strings.Join(undeclared.Items, ", "), Tell: true}
	})
	return events
}

func firstNonZeroTime(times ...time.Time) time.Time {
	for _, t := range times {
		if !t.IsZero() {
			return t
		}
	}
	return time.Time{}
}

func watchTrim(samples []watchSample, now time.Time) []watchSample {
	for len(samples) > 0 && now.Sub(samples[0].At) > watchKeep {
		samples = samples[1:]
	}
	return samples
}

// watchAverage is the samples' average, but the last, and how many.
func watchAverage(samples []watchSample) (float64, int) {
	if len(samples) < 2 {
		return 0, 0
	}
	sum := 0.0
	for _, sample := range samples[:len(samples)-1] {
		sum += sample.Value
	}
	return sum / float64(len(samples)-1), len(samples) - 1
}

// watchDaysLeft is how many days until the disk is full at the rate since
// the oldest daily sample, or a large number when it isn't filling.
func watchDaysLeft(samples []watchSample, now float64, at time.Time) float64 {
	if len(samples) < 2 {
		return 1e9
	}
	days := at.Sub(samples[0].At).Hours() / 24
	rate := (now - samples[0].Value) / days
	if days < 1 || rate <= 0 {
		return 1e9
	}
	return (1 - now) / rate
}

// check is the watcher's tick: teams that stopped sending, and the
// dead-man's switch.
func (s *hiServer) checkWatch(now time.Time) {
	w := s.watch
	w.mu.Lock()
	var events []watchEvent
	for _, team := range w.state.Teams {
		limit := watchSilentAfter
		switch team.Stopping {
		case "down":
			continue
		case "restart":
			limit = watchSilentAfterRestart
		}
		silent := now.Sub(team.Last)
		if silent < limit {
			continue
		}
		minutes := int(silent.Round(time.Minute) / time.Minute)
		events = append(events, w.ruleLocked(team, "heartbeat", true, now, func() watchIncident {
			detail := fmt.Sprintf("The last heartbeat came from %s at %s UTC.", team.Hostname, team.Last.UTC().Format("Jan 2 15:04"))
			if team.Stopping == "restart" {
				detail += " It said it was restarting."
			}
			return watchIncident{Title: fmt.Sprintf("%s has sent no heartbeat for %d minutes", team.Team, minutes),
				Detail: detail + " The machine may be off or off the network, or the team may be failing to start: on the machine, systemctl --user status hi-team-" + team.Team + ".",
				Push:   true}
		}, "")...)
	}
	if len(events) > 0 {
		w.saveLocked()
	}
	if !w.matching.Load() {
		w.matching.Store(true)
		go func() {
			defer w.matching.Store(false)
			s.matchAdvisoriesDue(now)
		}()
	}
	deadman := w.config.Deadman
	ping := deadman != "" && now.Sub(w.pinged) >= watchDeadmanEvery
	if ping {
		w.pinged = now
	}
	w.mu.Unlock()
	s.watchEvents(events)
	if ping {
		go func() {
			if err := w.ping(deadman); err != nil {
				fmt.Fprintf(s.log, "watch: the dead-man's switch: %v\n", err)
			}
		}()
	}
}

// watchEvents tells the watch channel, the audit log, and for a push, the
// team's owners.
func (s *hiServer) watchEvents(events []watchEvent) {
	w := s.watch
	for _, event := range events {
		incident := event.incident
		if event.closed {
			s.audit("watch", "closed", incident.ID, incident.Team+": "+incident.Cleared)
			text := fmt.Sprintf("%s closed after %s: %s", incident.ID, teamJobDuration(incident.Closed.Sub(incident.Opened).Round(time.Minute)), incident.Cleared)
			s.watchPost(incident.ID, text, true)
			continue
		}
		s.audit("watch", "opened", incident.ID, incident.Team+": "+incident.Title)
		text := fmt.Sprintf(":rotating_light: *%s* %s", incident.ID, incident.Title)
		if incident.Detail != "" {
			text += "\n" + incident.Detail
		}
		if incident.Tell {
			text += "\nThe team's channel hears of it too."
		}
		s.watchPost(incident.ID, text, false)
		if incident.Push {
			w.mu.Lock()
			config := w.config
			w.mu.Unlock()
			if config.Ntfy != "" {
				go func() {
					if err := w.push(config, config.topic(incident.Team), "hi watch: "+incident.Team, incident.Title+" ("+incident.ID+")"); err != nil {
						fmt.Fprintf(s.log, "watch: pushing %s: %v\n", incident.ID, err)
					}
				}()
			}
		}
	}
}

// watchPost posts in the watch channel, or the approvals channel when none
// is set; a closing goes in the incident's thread.
func (s *hiServer) watchPost(id, text string, thread bool) {
	if s.slack == nil {
		return
	}
	s.slack.enqueue(func() {
		s.watch.mu.Lock()
		channel := firstNonEmpty(s.watch.config.Channel, s.slack.channel())
		incident := s.watch.state.Incidents[id]
		ts := ""
		if incident != nil {
			ts = incident.SlackTS
		}
		s.watch.mu.Unlock()
		options := []slack.MsgOption{slack.MsgOptionText(text, false)}
		if thread && ts != "" {
			options = append(options, slack.MsgOptionTS(ts))
		}
		_, posted, err := s.slack.api.PostMessage(channel, options...)
		if err != nil {
			fmt.Fprintf(s.log, "slack: %v\n", err)
			return
		}
		if !thread {
			s.watch.mu.Lock()
			if incident := s.watch.state.Incidents[id]; incident != nil {
				incident.SlackTS = posted
				s.watch.saveLocked()
			}
			s.watch.mu.Unlock()
		}
	})
}

// watchPushNtfy publishes a push to the team's topic.
func watchPushNtfy(config watchConfig, topic, title, text string) error {
	request, err := http.NewRequest(http.MethodPost, strings.TrimRight(config.Ntfy, "/")+"/"+topic, strings.NewReader(text))
	if err != nil {
		return err
	}
	request.Header.Set("Title", title)
	request.Header.Set("Priority", "high")
	if config.NtfyToken != "" {
		request.Header.Set("Authorization", "Bearer "+config.NtfyToken)
	}
	response, err := (&http.Client{Timeout: 20 * time.Second}).Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 512))
		return fmt.Errorf("ntfy said %s: %s", response.Status, strings.TrimSpace(string(body)))
	}
	return nil
}

func watchPing(url string) error {
	response, err := (&http.Client{Timeout: 20 * time.Second}).Get(url)
	if err != nil {
		return err
	}
	response.Body.Close()
	if response.StatusCode >= 300 {
		return errors.New(response.Status)
	}
	return nil
}

// ---------------------------------------------------------------------------
// what the admins see

type watchTeamView struct {
	Team       string                  `json:"team"`
	Hostname   string                  `json:"hostname"`
	User       string                  `json:"user"`
	Last       time.Time               `json:"last"`
	Stopping   string                  `json:"stopping,omitempty"`
	Hi         string                  `json:"hi,omitempty"`
	Production string                  `json:"production,omitempty"`
	Parts      map[string]apiWatchPart `json:"parts,omitempty"`
	Network    int64                   `json:"network_kept"`
	Bom        string                  `json:"bom_version,omitempty"`
	Advisories int                     `json:"advisories"`
	Loud       int                     `json:"advisories_loud"`
	Matched    time.Time               `json:"matched,omitzero"`
	Stop       apiWatchStop            `json:"stop"`
	Want       *apiWatchDo             `json:"want,omitempty"` // asked, not done yet
	KeyMade    bool                    `json:"key_made,omitempty"`
	KeyRevoked bool                    `json:"key_revoked,omitempty"`
	CutOff     *watchCutOff            `json:"cut_off,omitempty"`
}

type watchView struct {
	Teams      []watchTeamView `json:"teams"`
	Incidents  []watchIncident `json:"incidents"`
	Deadman    bool            `json:"deadman"`
	Ntfy       string          `json:"ntfy,omitempty"`
	Channel    string          `json:"channel,omitempty"`
	AutoFreeze bool            `json:"auto_freeze"`
}

func (w *serverWatch) view() watchView {
	w.mu.Lock()
	defer w.mu.Unlock()
	view := watchView{Teams: []watchTeamView{}, Incidents: []watchIncident{}, Deadman: w.config.Deadman != "", Ntfy: w.config.Ntfy, Channel: w.config.Channel, AutoFreeze: w.config.AutoFreeze != "off"}
	for _, team := range w.state.Teams {
		row := watchTeamView{Team: team.Team, Hostname: team.Hostname, User: team.User, Last: team.Last, Stopping: team.Stopping,
			Hi: team.Beat.Hi, Production: team.Beat.Production, Parts: team.Beat.Parts, Network: team.NetworkKept, Bom: team.BomVersion, Matched: team.Matched,
			Stop: team.Beat.Stop, KeyMade: team.KeyHash != "", KeyRevoked: team.KeyRevoked, CutOff: team.CutOff}
		if team.Want != nil && !team.WantDone {
			want := *team.Want
			row.Want = &want
		}
		for _, advisory := range team.Advisories {
			if advisory.Ignored == "" {
				row.Advisories++
				if tier := advisory.tier(); tier == "kev" || tier == "incident" {
					row.Loud++
				}
			}
		}
		view.Teams = append(view.Teams, row)
	}
	sort.Slice(view.Teams, func(i, j int) bool { return view.Teams[i].Team < view.Teams[j].Team })
	for _, incident := range w.state.Incidents {
		if incident.Closed.IsZero() {
			view.Incidents = append(view.Incidents, *incident)
		}
	}
	sort.Slice(view.Incidents, func(i, j int) bool {
		a, b := view.Incidents[i], view.Incidents[j]
		if !a.Opened.Equal(b.Opened) {
			return a.Opened.Before(b.Opened)
		}
		return watchNumber(a.ID) < watchNumber(b.ID)
	})
	return view
}

// watchNumber is w-12's 12.
func watchNumber(id string) int {
	n, _ := strconv.Atoi(strings.TrimPrefix(id, "w-"))
	return n
}

func printWatchView(view watchView, now time.Time, out io.Writer) {
	if len(view.Teams) == 0 {
		fmt.Fprintln(out, "No team sends heartbeats yet. A team on a connected machine (hi connect) starts within a minute of hi team up.")
	}
	for _, team := range view.Teams {
		state := "heartbeat " + teamAgo(now.Sub(team.Last)) + " ago"
		switch team.Stopping {
		case "down":
			state = "stopped with hi team down " + teamAgo(now.Sub(team.Last)) + " ago"
		case "restart":
			state = "restarting since " + teamAgo(now.Sub(team.Last)) + " ago"
		}
		fmt.Fprintf(out, "%s on %s (%s): %s; hi %s, production %s\n", team.Team, team.Hostname, team.User, state, firstNonEmpty(team.Hi, "?"), firstNonEmpty(team.Production, "not released"))
		names := make([]string, 0, len(team.Parts))
		for name := range team.Parts {
			names = append(names, name)
		}
		sort.Strings(names)
		var parts []string
		for _, name := range names {
			parts = append(parts, name+" "+team.Parts[name].State)
		}
		if len(parts) > 0 {
			fmt.Fprintf(out, "  %s\n", strings.Join(parts, " · "))
		}
		switch {
		case team.Stop.Paused:
			fmt.Fprintln(out, "  PAUSED: every box of the team is paused (hi server watch unpause "+team.Team+")")
		case team.Stop.Frozen:
			fmt.Fprintln(out, "  FROZEN: its boxes reach nothing and its app is paused (hi server watch unfreeze "+team.Team+")")
		}
		if team.Want != nil {
			fmt.Fprintf(out, "  asked to %s by %s %s ago, not done yet\n", team.Want.Action, team.Want.By, teamAgo(now.Sub(team.Want.At)))
		}
		switch {
		case team.KeyRevoked:
			fmt.Fprintln(out, "  KEY REVOKED: its OpenRouter key is disabled (hi server watch rekey "+team.Team+")")
		case !team.KeyMade:
			fmt.Fprintln(out, "  its OpenRouter key isn't the server's, so it can't be revoked from here (hi team key "+team.Team+" openrouter --from-server on its machine)")
		}
		if team.CutOff != nil {
			fmt.Fprintf(out, "  CUT OFF the NetBird network %s ago (hi server watch reconnect %s)\n", teamAgo(now.Sub(team.CutOff.At)), team.Team)
		}
		switch {
		case team.Bom == "":
			fmt.Fprintln(out, "  no bill of materials yet")
		case team.Matched.IsZero():
			fmt.Fprintf(out, "  bill for %s, not matched against advisories yet\n", team.Bom)
		default:
			fmt.Fprintf(out, "  bill for %s: %d advisories, %d of them incidents; matched %s ago\n", team.Bom, team.Advisories, team.Loud, teamAgo(now.Sub(team.Matched)))
		}
	}
	if len(view.Incidents) > 0 {
		fmt.Fprintln(out, "\nOpen:")
		for _, incident := range view.Incidents {
			fmt.Fprintf(out, "  %s  %s  %s (%s ago)\n", incident.ID, incident.Team, incident.Title, teamAgo(now.Sub(incident.Opened)))
		}
	}
	if !view.Deadman {
		fmt.Fprintln(out, "\nWarning: nobody would know if this server stopped. hi server watch --deadman <url> pings an outside check (healthchecks.io) every minute.")
	}
	if view.AutoFreeze {
		fmt.Fprintln(out, "\nA burst of sends to paste or tunnel hosts, or an exploited advisory in an app's own packages, freezes the team on its own (--auto-freeze off to wait for an admin).")
	}
	if view.Ntfy == "" {
		fmt.Fprintln(out, "Warning: no pushes. hi server watch --ntfy <url> --ntfy-token <token> pushes a stopped heartbeat to the team's owners.")
	}
}

func (s *hiServer) watchRoutes(mux *http.ServeMux) {
	s.watchStopRoutes(mux)
	s.watchCutoffRoutes(mux)
	mux.HandleFunc("GET /admin/watch", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, s.watch.view())
	})
	mux.HandleFunc("POST /admin/watch/config", func(w http.ResponseWriter, r *http.Request) {
		var change watchConfig
		if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&change); err != nil {
			writeAPIError(w, http.StatusBadRequest, "malformed settings")
			return
		}
		s.watch.mu.Lock()
		config := s.watch.config
		for _, set := range []struct{ to, from *string }{{&config.Deadman, &change.Deadman}, {&config.Ntfy, &change.Ntfy}, {&config.NtfyToken, &change.NtfyToken}, {&config.Channel, &change.Channel}, {&config.TopicStart, &change.TopicStart}} {
			switch *set.from {
			case "":
			case "off":
				*set.to = ""
			default:
				*set.to = *set.from
			}
		}
		// Auto-freeze is on unless set off.
		switch change.AutoFreeze {
		case "on":
			config.AutoFreeze = ""
		case "off":
			config.AutoFreeze = "off"
		}
		s.watch.config = config
		err := s.watch.saveConfigLocked()
		s.watch.mu.Unlock()
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.audit(currentUserName(), "changed watch settings", "watch", "")
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})
	mux.HandleFunc("POST /admin/watch/forget/{team}", func(w http.ResponseWriter, r *http.Request) {
		team := r.PathValue("team")
		s.watch.mu.Lock()
		_, ok := s.watch.state.Teams[team]
		delete(s.watch.state.Teams, team)
		s.watch.saveLocked()
		s.watch.mu.Unlock()
		if !ok {
			writeAPIError(w, http.StatusNotFound, "no team "+team+" sends heartbeats")
			return
		}
		s.audit(currentUserName(), "forgot", team, "its next heartbeat, from any machine, takes it over")
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})
	mux.HandleFunc("GET /admin/watch/subscribe/{team}", func(w http.ResponseWriter, r *http.Request) {
		s.watch.mu.Lock()
		config := s.watch.config
		s.watch.mu.Unlock()
		if config.Ntfy == "" {
			writeAPIError(w, http.StatusBadRequest, "no pushes yet: hi server watch --ntfy <url> --ntfy-token <token>")
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"url": strings.TrimRight(config.Ntfy, "/") + "/" + config.topic(r.PathValue("team")), "topic": config.topic(r.PathValue("team"))})
	})
}

// serverWatchCommand is hi server watch.
func serverWatchCommand(args []string, stdout, stderr io.Writer) error {
	flags, dirFlag := serverFlags("watch", stderr)
	deadman := flags.String("deadman", "", "a URL the server pings every minute (off to stop)")
	ntfy := flags.String("ntfy", "", "the ntfy server owners' pushes go through (off to stop)")
	token := flags.String("ntfy-token", "", "a token for the ntfy server")
	channel := flags.String("channel", "", "the Slack channel watch incidents go to (off: the approvals channel)")
	autoFreeze := flags.String("auto-freeze", "", "on, or off to have every freeze wait for an admin")
	because := flags.String("because", "", "why, for freeze, unfreeze, pause, unpause, revoke, rekey, cutoff, and reconnect")
	openrouter := flags.Bool("openrouter", false, "set the OpenRouter provisioning key the server makes and revokes teams' keys with")
	netbird := flags.Bool("netbird", false, "set the NetBird API token the server cuts teams' machines off with")
	netbirdURL := flags.String("netbird-url", "", "the NetBird management API, when it isn't this machine's")
	asJSON := flags.Bool("json", false, "print JSON")
	rest, err := flags.parse(args)
	if err != nil {
		return err
	}
	switch {
	case len(rest) == 2 && rest[0] == "forget":
		if err := adminCall(*dirFlag, http.MethodPost, "/admin/watch/forget/"+rest[1], nil, nil); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Forgot team %s; its next heartbeat, from whichever machine, takes it over.\n", rest[1])
		return nil
	case len(rest) == 2 && rest[0] == "subscribe":
		var link map[string]string
		if err := adminCall(*dirFlag, http.MethodGet, "/admin/watch/subscribe/"+rest[1], nil, &link); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Team %s's pushes: %s\nIn the ntfy app, subscribe to topic %s on that server, signed in with an account that may read it.\n", rest[1], link["url"], link["topic"])
		return nil
	case len(rest) == 2 && (rest[0] == "freeze" || rest[0] == "unfreeze" || rest[0] == "pause" || rest[0] == "unpause"):
		return watchStopCommand(*dirFlag, rest[0], rest[1], *because, stdout)
	case len(rest) == 2 && (rest[0] == "revoke" || rest[0] == "rekey" || rest[0] == "cutoff" || rest[0] == "reconnect"):
		if err := adminCall(*dirFlag, http.MethodPost, "/admin/watch/act/"+rest[1]+"/"+rest[0], map[string]string{"because": *because, "as": currentUserName()}, nil); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Done: %s %s. hi server watch shows it, and the watch channel says so.\n", rest[0], rest[1])
		return nil
	case len(rest) > 0:
		return usageError{"usage: hi server watch [--deadman <url>] [--ntfy <url> --ntfy-token <token>] [--channel <id>] [--auto-freeze on|off] [--openrouter] [--netbird]\n       hi server watch forget|subscribe <team>\n       hi server watch freeze|unfreeze|pause|unpause|revoke|rekey|cutoff|reconnect <team> [--because \"<why>\"]"}
	}
	for which, set := range map[string]bool{"openrouter": *openrouter, "netbird": *netbird} {
		if !set {
			continue
		}
		label := map[string]string{"openrouter": "OpenRouter provisioning", "netbird": "NetBird API"}[which]
		value, err := readProviderKey(label, os.Stdin, stdout)
		if err != nil {
			return err
		}
		if err := adminCall(*dirFlag, http.MethodPost, "/admin/watch/secret/"+which, map[string]string{"value": value, "url": *netbirdURL}, nil); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Saved the %s key; it works.\n", label)
	}
	if *autoFreeze != "" && *autoFreeze != "on" && *autoFreeze != "off" {
		return usageError{"--auto-freeze is on or off"}
	}

	if *deadman != "" || *ntfy != "" || *token != "" || *channel != "" || *autoFreeze != "" {
		change := watchConfig{Deadman: *deadman, Ntfy: *ntfy, NtfyToken: *token, Channel: *channel, AutoFreeze: *autoFreeze}
		if err := adminCall(*dirFlag, http.MethodPost, "/admin/watch/config", change, nil); err != nil {
			return err
		}
		fmt.Fprintln(stdout, "Saved.")
	}
	var view watchView
	if err := adminCall(*dirFlag, http.MethodGet, "/admin/watch", nil, &view); err != nil {
		return err
	}
	if *asJSON {
		data, _ := json.MarshalIndent(view, "", "  ")
		fmt.Fprintln(stdout, string(data))
		return nil
	}
	printWatchView(view, time.Now(), stdout)
	return nil
}

// watchSummary is /hi teams in Slack.
func (s *hiServer) watchSummary(now time.Time) string {
	var text strings.Builder
	printWatchView(s.watch.view(), now, &text)
	return text.String()
}
