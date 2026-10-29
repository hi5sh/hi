package main

// The team's own watcher (docs/specs/approved/hi_team_ops.md, release 3):
// the rules the hi server ran on a team's heartbeat run in the team's
// broker, on the same heartbeat, with the server's own code
// (server_watch.go, server_watch_network.go, server_advisories.go) and
// its state in the team's folder, watch/. What they open and close are
// the team's incidents, in the ops channel; a stopped team or machine is
// the outside heartbeat's to notice (team_heartbeat.go).

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var (
	teamLocalWatchersMu sync.Mutex
	teamLocalWatchers   = map[string]*teamWatcher{}
)

// teamLocalWatcher is the team's watcher, opened once per process.
func teamLocalWatcher(team string) *teamWatcher {
	teamLocalWatchersMu.Lock()
	defer teamLocalWatchersMu.Unlock()
	dir := teamDir(team)
	if w := teamLocalWatchers[dir]; w != nil {
		return w
	}
	w := openTeamWatcher(dir)
	teamLocalWatchers[dir] = w
	return w
}

// teamWatchWant is run/watch-want.json: a freeze, unfreeze, pause, or
// unpause asked on the machine, which the broker takes at its next beat.
type teamWatchWant struct {
	Action  string    `json:"action"`
	By      string    `json:"by"`
	Because string    `json:"because,omitempty"`
	At      time.Time `json:"at"`
}

func teamWatchWantPath(team string) string {
	return filepath.Join(teamDir(team), "run", "watch-want.json")
}

// localBeat runs the team's watcher on a heartbeat: what to do, and the
// incidents that opened or closed. A heartbeat's network lines are read
// from where the watcher left off; production's bill is handed over.
func (b *teamBroker) localBeat(beat apiHeartbeat, bomPath string, now time.Time) (apiHeartbeatReply, []watchEvent, []string) {
	w := teamLocalWatcher(b.team)
	var posts []string
	w.mu.Lock()
	// The owners' choice of automatic freezes, from the file, which
	// hi team watch writes.
	var config watchConfig
	if data, err := os.ReadFile(filepath.Join(w.dir, "config.json")); err == nil {
		json.Unmarshal(data, &config)
	}
	w.config.AutoFreeze = config.AutoFreeze
	team := w.state.Teams[b.team]
	if team == nil {
		hostname, _ := os.Hostname()
		team = &watchTeam{Team: b.team, Device: "local", Hostname: hostname, First: now}
		w.state.Teams[b.team] = team
	}
	if data, err := os.ReadFile(teamWatchWantPath(b.team)); err == nil {
		var want teamWatchWant
		if json.Unmarshal(data, &want) == nil && want.Action != "" {
			do := w.wantLocked(team, want.Action, want.By, want.Because, now)
			posts = append(posts, fmt.Sprintf(":octagonal_sign: %s asked the team to %s%s (%s). It does within a minute.",
				teamSlackEscape(want.By), want.Action, map[bool]string{true: ": " + teamSlackEscape(want.Because), false: ""}[want.Because != ""], do.ID))
		}
		os.Remove(teamWatchWantPath(b.team))
	}
	if beat.Bom != "" && beat.Bom != team.Bom && bomPath != "" {
		if data, err := os.ReadFile(bomPath); err == nil {
			dir := filepath.Join(w.dir, "teams", b.team, "boms")
			if os.MkdirAll(dir, 0o700) == nil {
				writeFileAtomic(filepath.Join(dir, strings.TrimPrefix(beat.Bom, "sha256:")+".cdx.json"), data, 0o600)
			}
		}
	}
	reply, events, confirmed, weekly := w.beatLocked(team, beat, now)
	due := team.Bom != "" && now.Sub(team.Matched) >= advisoryMatchEvery && now.Sub(team.MatchTried) >= time.Hour
	w.saveLocked()
	w.mu.Unlock()
	if confirmed != "" {
		posts = append(posts, ":white_check_mark: "+confirmed)
	}
	if weekly != "" {
		posts = append(posts, weekly)
	}
	// Production's bill against the advisories, once a day, apart: the
	// first match takes minutes.
	if due && w.matching.CompareAndSwap(false, true) {
		go func() {
			defer w.matching.Store(false)
			events, _, err := w.matchTeam(b.team, time.Now())
			if err != nil {
				fmt.Fprintf(os.Stderr, "hi: matching production's bill against advisories: %v\n", err)
				return
			}
			teamWatchStateMu.Lock()
			state := loadTeamWatchState(b.team)
			b.tellLocal(&state, events)
			saveTeamWatchState(b.team, state)
			teamWatchStateMu.Unlock()
		}()
	}
	// The team tells itself by events, not by the list the server sent.
	reply.Tell = nil
	return reply, events, posts
}

// tellLocal opens a team incident, in the ops channel, for each of the
// watcher's that opened, and closes it when the watcher's closed. The
// lead isn't woken for a key used outside hi: it may be what uses it.
func (b *teamBroker) tellLocal(state *teamWatchState, events []watchEvent) {
	if state.Told == nil {
		state.Told = map[string]string{}
	}
	config, _ := loadTeam(b.team)
	for _, event := range events {
		incident := event.incident
		if event.closed {
			if local := state.Told[incident.ID]; local != "" {
				if b.incidentOpen(local) {
					b.recovered(local, firstNonEmpty(incident.Cleared, "It cleared."))
				}
				delete(state.Told, incident.ID)
			}
			continue
		}
		if _, told := state.Told[incident.ID]; told {
			continue
		}
		text := incident.Title + "."
		if incident.Detail != "" {
			text += "\n" + incident.Detail
		}
		prompt := teamIncidentPrompt
		if incident.Rule == "key" {
			prompt = nil
		}
		local, err := b.openIncident(teamIncident{Kind: "watch", Slot: teamSlotProduction, Title: incident.Title, Detail: incident.Detail, Opened: time.Now().UTC()}, text, "", prompt)
		if err != nil {
			continue
		}
		state.Told[incident.ID] = local.ID
		// The worst, such as an exploited advisory, call the owners by name.
		if incident.Push && len(config.Owners) > 0 && b.channel != nil && local.Thread != "" {
			b.channel.postIn(local.channelIn(config), local.Thread, teamPeople(config.Owners)+", this one needs you.")
		}
	}
}

// teamWatchCommand is hi team watch <name> [--auto-freeze on|off]: the
// watcher's open incidents, and whether it freezes on its own.
func teamWatchCommand(args []string, stdout io.Writer) error {
	usage := usageError{"usage: hi team watch <name> [--auto-freeze on|off]"}
	if len(args) != 1 && !(len(args) == 3 && args[1] == "--auto-freeze" && (args[2] == "on" || args[2] == "off")) {
		return usage
	}
	name := args[0]
	if _, err := loadTeam(name); err != nil {
		return err
	}
	dir := filepath.Join(teamDir(name), "watch")
	var config watchConfig
	if data, err := os.ReadFile(filepath.Join(dir, "config.json")); err == nil {
		json.Unmarshal(data, &config)
	}
	if len(args) == 3 {
		config.AutoFreeze = map[string]string{"on": "", "off": "off"}[args[2]]
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
		data, _ := json.MarshalIndent(config, "", "  ")
		if err := writeFileAtomic(filepath.Join(dir, "config.json"), append(data, '\n'), 0o600); err != nil {
			return err
		}
	}
	if config.AutoFreeze == "off" {
		fmt.Fprintf(stdout, "Team %s's watcher doesn't freeze it on its own; an exploited advisory in the app or a burst to a watched host is an incident that waits for an owner.\n", name)
	} else {
		fmt.Fprintf(stdout, "Team %s's watcher freezes it on its own for an exploited advisory in the app's own packages, or a burst to a watched host (hi team watch %s --auto-freeze off to wait for an owner).\n", name, name)
	}
	fmt.Fprintln(stdout, teamWatchLine(name))
	var open []string
	for _, incident := range loadTeamIncidents(name) {
		if incident.Kind == "watch" && incident.Status != "closed" {
			open = append(open, fmt.Sprintf("  %s  %s", incident.ID, incident.Title))
		}
	}
	if len(open) > 0 {
		fmt.Fprintln(stdout, "Open:")
		fmt.Fprintln(stdout, strings.Join(open, "\n"))
	}
	return nil
}

// teamStopCommand is hi team freeze|unfreeze|pause|unpause <name>
// --why "…": asked here, done by the team's broker within a minute.
func teamStopCommand(action string, args []string, stdout io.Writer) error {
	usage := usageError{fmt.Sprintf("usage: hi team %s <name> --why \"<why>\"", action)}
	if len(args) == 0 {
		return usage
	}
	name, because := args[0], ""
	for i := 1; i < len(args); i++ {
		switch {
		case args[i] == "--why" && i+1 < len(args):
			i++
			because = args[i]
		case strings.HasPrefix(args[i], "--why="):
			because = strings.TrimPrefix(args[i], "--why=")
		default:
			return usage
		}
	}
	if _, err := loadTeam(name); err != nil {
		return err
	}
	if (action == "freeze" || action == "pause") && strings.TrimSpace(because) == "" {
		return fmt.Errorf("say why, for the ops channel: hi team %s %s --why \"<why>\"", action, name)
	}
	want := teamWatchWant{Action: action, By: firstNonEmpty(os.Getenv("USER"), "an admin") + " on the machine", Because: because, At: time.Now().UTC()}
	data, _ := json.Marshal(want)
	if err := os.MkdirAll(filepath.Dir(teamWatchWantPath(name)), 0o700); err != nil {
		return err
	}
	if err := writeFileAtomic(teamWatchWantPath(name), data, 0o600); err != nil {
		return err
	}
	if teamState(name) != "up" {
		fmt.Fprintf(stdout, "Asked team %s to %s; it does when it's up again (hi team up %s).\n", name, action, name)
		return nil
	}
	fmt.Fprintf(stdout, "Asked team %s to %s. Its broker does within a minute, and says so in the ops channel.\n", name, action)
	return nil
}
