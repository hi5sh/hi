package main

// The team's watcher: its rules, run every minute in the team's broker on
// the heartbeat it makes (team_watch.go, team_watch_local.go): parts that
// are down, a team that restarts on its own, OOM kills, a disk filling up,
// snapshots that stopped, model spend that runs away or happens outside
// hi. Rules are plain code, no model; each opens one incident until it
// clears. Until hi 2.6.32 the hi server ran them on heartbeats teams sent
// it.
//
// Spec: docs/specs/approved/hi_watch.md, and hi_team_ops.md (release 3).

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
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
	LogsKept   map[string]int64   `json:"logs_kept,omitempty"`
	Do         *apiWatchDo        `json:"do,omitempty"`
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
	// AutoFreeze is "off" to have every freeze wait for an owner.
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

type teamWatcher struct {
	mu     sync.Mutex
	dir    string
	config watchConfig
	state  watchState
	// matchMu keeps one advisory match at a time; matching says one runs
	// for the teams that are due.
	net      map[string]*watchNet
	hosts    []string
	hostsAt  time.Time
	matchMu  sync.Mutex
	matching atomic.Bool
	feeds    *advisoryFeeds
}

// openTeamWatcher opens a team's watcher, in its folder's watch/.
func openTeamWatcher(teamFolder string) *teamWatcher {
	w := &teamWatcher{dir: filepath.Join(teamFolder, "watch")}
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

func (w *teamWatcher) saveLocked() error {
	if err := os.MkdirAll(w.dir, 0o700); err != nil {
		return err
	}
	data, _ := json.MarshalIndent(w.state, "", "  ")
	return writeFileAtomic(filepath.Join(w.dir, "state.json"), append(data, '\n'), 0o600)
}

// beatLocked keeps a heartbeat's logs, runs the rules on it, and answers:
// the server's for every team, and a team's own watcher for itself
// (team_watch_local.go).
func (w *teamWatcher) beatLocked(team *watchTeam, beat apiHeartbeat, now time.Time) (apiHeartbeatReply, []watchEvent, string, string) {
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
		// Production's bill, once the broker handed it over.
		if beat.Bom != "" && beat.Bom != team.Bom && fileExists(filepath.Join(w.dir, "teams", team.Team, "boms", strings.TrimPrefix(beat.Bom, "sha256:")+".cdx.json")) {
			team.Bom, team.BomVersion, team.Matched = beat.Bom, beat.BomVersion, time.Time{}
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
func (w *teamWatcher) applyIgnoresLocked(team *watchTeam, now time.Time) []watchEvent {
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
func (w *teamWatcher) ruleLocked(team *watchTeam, rule string, on bool, now time.Time, open func() watchIncident, cleared string) []watchEvent {
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
func (w *teamWatcher) rulesLocked(team *watchTeam, now time.Time) []watchEvent {
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

// ---------------------------------------------------------------------------
// what the admins see
