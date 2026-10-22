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
}

type watchState struct {
	Teams     map[string]*watchTeam     `json:"teams"`
	Incidents map[string]*watchIncident `json:"incidents"`
	Next      int                       `json:"next"`
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
	// push and ping are replaced in tests.
	push func(config watchConfig, topic, title, text string) error
	ping func(url string) error
}

func watchDir(serverDir string) string { return filepath.Join(serverDir, "watch") }

func openServerWatch(serverDir string) *serverWatch {
	w := &serverWatch{dir: watchDir(serverDir), push: watchPushNtfy, ping: watchPing}
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

func (s *hiServer) handleHeartbeat(w http.ResponseWriter, _ *http.Request, device serverDevice, body []byte) {
	var beat apiHeartbeat
	if err := json.Unmarshal(body, &beat); err != nil || !teamNamePattern.MatchString(beat.Team) {
		writeAPIError(w, http.StatusBadRequest, "malformed heartbeat")
		return
	}
	reply, err := s.watch.heartbeat(s, device, beat, computeNow())
	if err != nil {
		writeAPIError(w, http.StatusForbidden, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, reply)
}

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
	network := beat.Network
	beat.Network = nil
	if beat.Stopping != "" {
		// A last word before a stop: kept, but the parts it describes are
		// the ones that are stopping.
		team.Stopping, team.StoppingAt, team.Last = beat.Stopping, now, now
	} else {
		team.Stopping, team.StoppingAt = "", time.Time{}
		team.Beat, team.Last = beat, now
	}
	if network != nil {
		w.keepNetworkLocked(s, team, *network)
	}
	var events []watchEvent
	if beat.Stopping == "" {
		events = w.rulesLocked(team, now)
	}
	reply := apiHeartbeatReply{NetworkKept: team.NetworkKept}
	for _, id := range team.Open {
		if incident := w.state.Incidents[id]; incident != nil && incident.Tell {
			reply.Tell = append(reply.Tell, apiWatchIncident{ID: id, Rule: incident.Rule, Title: incident.Title, Detail: incident.Detail})
		}
	}
	sort.Slice(reply.Tell, func(i, j int) bool { return reply.Tell[i].ID < reply.Tell[j].ID })
	w.saveLocked()
	w.mu.Unlock()
	s.watchEvents(events)
	return reply, nil
}

// keepNetworkLocked appends the lines the team sent that the server
// doesn't have yet. The team always sends from the offset the server last
// said it kept, so a heartbeat whose answer was lost sends lines the
// server has already, which it skips.
func (w *serverWatch) keepNetworkLocked(s *hiServer, team *watchTeam, network apiWatchNetwork) {
	if network.To < network.From || len(network.Lines) > watchNetworkLinesMax {
		return
	}
	note := ""
	switch {
	case network.Rotated:
		note = fmt.Sprintf("# %s the team's network log started again from 0 (rotated or shortened)", computeNow().UTC().Format(time.RFC3339))
		team.NetworkKept = 0
	case network.To <= team.NetworkKept && network.From < team.NetworkKept:
		return // kept already
	case network.From > team.NetworkKept:
		note = fmt.Sprintf("# %s bytes %d to %d of the team's network log never arrived", computeNow().UTC().Format(time.RFC3339), team.NetworkKept, network.From)
	}
	dir := filepath.Join(w.dir, "teams", team.Team)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	file, err := os.OpenFile(filepath.Join(dir, "network.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer file.Close()
	var text strings.Builder
	if note != "" {
		text.WriteString(note + "\n")
	}
	for _, line := range network.Lines {
		line = strings.ReplaceAll(line, "\n", " ")
		if len(line) > watchLineMax {
			line = line[:watchLineMax]
		}
		text.WriteString(line + "\n")
	}
	if _, err := file.WriteString(text.String()); err != nil {
		return
	}
	team.NetworkKept = network.To
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
		if part.State == "up" || part.State == "off" {
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
	add("serve-restarts", beat.Restarts > 3, "The team has stopped restarting.", func() watchIncident {
		return watchIncident{Title: fmt.Sprintf("%s started %d times in a day", team.Team, beat.Restarts+1),
			Detail: "systemd starts hi team __serve again when it stops; this many means it keeps stopping. journalctl --user -u hi-team-" + team.Team + " on the team's machine says why."}
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
}

type watchView struct {
	Teams     []watchTeamView `json:"teams"`
	Incidents []watchIncident `json:"incidents"`
	Deadman   bool            `json:"deadman"`
	Ntfy      string          `json:"ntfy,omitempty"`
	Channel   string          `json:"channel,omitempty"`
}

func (w *serverWatch) view() watchView {
	w.mu.Lock()
	defer w.mu.Unlock()
	view := watchView{Teams: []watchTeamView{}, Incidents: []watchIncident{}, Deadman: w.config.Deadman != "", Ntfy: w.config.Ntfy, Channel: w.config.Channel}
	for _, team := range w.state.Teams {
		view.Teams = append(view.Teams, watchTeamView{Team: team.Team, Hostname: team.Hostname, User: team.User, Last: team.Last, Stopping: team.Stopping,
			Hi: team.Beat.Hi, Production: team.Beat.Production, Parts: team.Beat.Parts, Network: team.NetworkKept})
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
	if view.Ntfy == "" {
		fmt.Fprintln(out, "Warning: no pushes. hi server watch --ntfy <url> --ntfy-token <token> pushes a stopped heartbeat to the team's owners.")
	}
}

func (s *hiServer) watchRoutes(mux *http.ServeMux) {
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
	case len(rest) > 0:
		return usageError{"usage: hi server watch [--deadman <url>] [--ntfy <url> --ntfy-token <token>] [--channel <id>] | forget <team> | subscribe <team>"}
	}
	if *deadman != "" || *ntfy != "" || *token != "" || *channel != "" {
		change := watchConfig{Deadman: *deadman, Ntfy: *ntfy, NtfyToken: *token, Channel: *channel}
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
