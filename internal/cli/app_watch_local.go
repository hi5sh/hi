package cli

// The team's own watcher (docs/specs/approved/hi_app_ops.md, release 3):
// the rules the org server ran on a team's heartbeat run in the team's
// broker, on the same heartbeat, with the server's own code
// (server_watch.go, server_watch_network.go, server_advisories.go) and
// its state in the team's folder, watch/. What they open and close are
// the team's incidents, in the ops channel; a stopped team or machine is
// the outside heartbeat's to notice (app_heartbeat.go).

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
	appLocalWatchersMu sync.Mutex
	appLocalWatchers   = map[string]*appWatcher{}
)

// appLocalWatcher is the team's watcher, opened once per process.
func appLocalWatcher(team string) *appWatcher {
	appLocalWatchersMu.Lock()
	defer appLocalWatchersMu.Unlock()
	dir := appDir(team)
	if w := appLocalWatchers[dir]; w != nil {
		return w
	}
	w := openTeamWatcher(dir)
	appLocalWatchers[dir] = w
	return w
}

// appWatchWant is run/watch-want.json: a freeze, unfreeze, pause, or
// unpause asked on the machine, which the broker takes at its next beat.
type appWatchWant struct {
	Action  string    `json:"action"`
	By      string    `json:"by"`
	Because string    `json:"because,omitempty"`
	At      time.Time `json:"at"`
}

func appWatchWantPath(team string) string {
	return filepath.Join(appDir(team), "run", "watch-want.json")
}

// appAdvisoriesCheck is run/advisories-check.json: an admin's ask to
// match production's bill against the advisories now, not at the day's
// match. The broker takes it at its next beat, marks it started, and
// removes it once the match is done, or keeps why it failed.
type appAdvisoriesCheck struct {
	By      string    `json:"by"`
	Asked   time.Time `json:"asked"`
	Started time.Time `json:"started,omitzero"`
	Error   string    `json:"error,omitempty"`
}

func appAdvisoriesCheckPath(team string) string {
	return filepath.Join(appDir(team), "run", "advisories-check.json")
}

func loadAppAdvisoriesCheck(team string) (appAdvisoriesCheck, bool) {
	var check appAdvisoriesCheck
	data, err := os.ReadFile(appAdvisoriesCheckPath(team))
	return check, err == nil && json.Unmarshal(data, &check) == nil && !check.Asked.IsZero()
}

func saveAppAdvisoriesCheck(team string, check appAdvisoriesCheck) error {
	data, _ := json.Marshal(check)
	os.MkdirAll(filepath.Dir(appAdvisoriesCheckPath(team)), 0o700)
	return writeFileAtomic(appAdvisoriesCheckPath(team), data, 0o600)
}

// localBeat runs the team's watcher on a heartbeat: what to do, and the
// incidents that opened or closed. A heartbeat's network lines are read
// from where the watcher left off; production's bill is handed over.
func (b *appBroker) localBeat(beat apiHeartbeat, bomPath string, now time.Time) (apiHeartbeatReply, []watchEvent, []string) {
	w := appLocalWatcher(b.team)
	var posts []string
	w.mu.Lock()
	// The owners' choice of automatic freezes, from the file, which
	// hi app watch writes.
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
	if data, err := os.ReadFile(appWatchWantPath(b.team)); err == nil {
		var want appWatchWant
		if json.Unmarshal(data, &want) == nil && want.Action != "" {
			do := w.wantLocked(team, want.Action, want.By, want.Because, now)
			posts = append(posts, fmt.Sprintf(":octagonal_sign: %s asked the app to %s%s (%s). It does within a minute.",
				appSlackEscape(want.By), want.Action, map[bool]string{true: ": " + appSlackEscape(want.Because), false: ""}[want.Because != ""], do.ID))
		}
		os.Remove(appWatchWantPath(b.team))
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
	check, asked := loadAppAdvisoriesCheck(b.team)
	asked = asked && check.Started.IsZero() && check.Error == ""
	if asked && team.Bom == "" {
		check.Error, asked = "nothing is released, so production runs no packages to match", false
		saveAppAdvisoriesCheck(b.team, check)
	}
	due := team.Bom != "" && (asked || now.Sub(team.Matched) >= advisoryMatchEvery && now.Sub(team.MatchTried) >= time.Hour)
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
	// An admin's ask waits for a match that already runs.
	if due && w.matching.CompareAndSwap(false, true) {
		if asked {
			check.Started = now
			saveAppAdvisoriesCheck(b.team, check)
		}
		go func() {
			defer w.matching.Store(false)
			events, _, err := w.matchTeam(b.team, time.Now())
			if asked {
				if err != nil {
					check.Error = err.Error()
					saveAppAdvisoriesCheck(b.team, check)
				} else {
					os.Remove(appAdvisoriesCheckPath(b.team))
				}
			}
			if err != nil {
				fmt.Fprintf(os.Stderr, "hi: matching production's bill against advisories: %v\n", err)
				return
			}
			w.mu.Lock()
			var advisories []apiAdvisory
			if team := w.state.Teams[b.team]; team != nil {
				advisories = team.Advisories
			}
			w.mu.Unlock()
			appWatchStateMu.Lock()
			state := loadTeamWatchState(b.team)
			// What matched shows at once, not at the next beat.
			state.Advisories, state.AdvisoryCounts = appFacingAdvisories(advisories)
			b.tellLocal(&state, events)
			saveTeamWatchState(b.team, state)
			appWatchStateMu.Unlock()
		}()
	}
	// The team tells itself by events, not by the list the server sent.
	reply.Tell = nil
	return reply, events, posts
}

// tellLocal opens a team incident, in the ops channel, for each of the
// watcher's that opened, and closes it when the watcher's closed. The
// lead isn't woken for a key used outside hi: it may be what uses it.
func (b *appBroker) tellLocal(state *appWatchState, events []watchEvent) {
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
		prompt := appIncidentPrompt
		if incident.Rule == "key" {
			prompt = nil
		}
		local, err := b.openIncident(appIncident{Kind: "watch", Slot: appSlotProduction, Title: incident.Title, Detail: incident.Detail, Opened: time.Now().UTC()}, text, "", prompt)
		if err != nil {
			continue
		}
		state.Told[incident.ID] = local.ID
		// The worst, such as an exploited advisory, call the owners by name.
		if incident.Push && len(config.Owners) > 0 && b.channel != nil && local.Thread != "" {
			b.channel.postIn(local.channelIn(config), local.Thread, appPeople(config.Owners)+", this one needs you.")
		}
	}
}

// appWatchCommand is hi app watch <name> [--auto-freeze on|off]: the
// watcher's open incidents, and whether it freezes on its own.
func appWatchCommand(args []string, stdout io.Writer) error {
	usage := usageError{"usage: hi app watch <name> [--auto-freeze on|off]"}
	if len(args) != 1 && !(len(args) == 3 && args[1] == "--auto-freeze" && (args[2] == "on" || args[2] == "off")) {
		return usage
	}
	name := args[0]
	if _, err := loadTeam(name); err != nil {
		return err
	}
	dir := filepath.Join(appDir(name), "watch")
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
		fmt.Fprintf(stdout, "App %s's watcher doesn't freeze it on its own; an exploited advisory in the app or a burst to a watched host is an incident that waits for an owner.\n", name)
	} else {
		fmt.Fprintf(stdout, "App %s's watcher freezes it on its own for an exploited advisory in the app's own packages, or a burst to a watched host (hi app watch %s --auto-freeze off to wait for an owner).\n", name, name)
	}
	fmt.Fprintln(stdout, appWatchLine(name))
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

// appStopCommand is hi app freeze|unfreeze|pause|unpause <name>
// --why "…": asked here, done by the team's broker within a minute.
func appStopCommand(action string, args []string, stdout io.Writer) error {
	usage := usageError{fmt.Sprintf("usage: hi app %s <name> --why \"<why>\"", action)}
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
		return fmt.Errorf("say why, for the ops channel: hi app %s %s --why \"<why>\"", action, name)
	}
	want := appWatchWant{Action: action, By: appActor(), Because: because, At: time.Now().UTC()}
	data, _ := json.Marshal(want)
	if err := os.MkdirAll(filepath.Dir(appWatchWantPath(name)), 0o700); err != nil {
		return err
	}
	if err := writeFileAtomic(appWatchWantPath(name), data, 0o600); err != nil {
		return err
	}
	if appState(name) != "up" {
		fmt.Fprintf(stdout, "Asked app %s to %s; it does when it's up again (hi app up %s).\n", name, action, name)
		return nil
	}
	fmt.Fprintf(stdout, "Asked app %s to %s. Its broker does within a minute, and says so in the ops channel.\n", name, action)
	return nil
}
