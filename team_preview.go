package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// A team has two addresses: the preview, where the people try dev or an
// idea, and production, the version they use. Each runs the project in an
// app box from a commit, publishes its port to this machine only, and puts
// it on an address with hi net expose --link, which uses this machine's
// NetBird: the link carries its key, so the people open it without a
// password. The lead gets the address; it never joins the network itself.
//
// An address outlives what it shows. Its exposure follows a port file, so
// a new version starts in a box beside the old one and the address moves
// to it once it answers: the link the people have keeps working, with the
// new version behind it. An exposure lasts a day at most, so production's
// is renewed before it ends, and hi keeps one message in the channel with
// production's current link.

const (
	teamPreviewPort  = 8080            // the app's port in its box, as $PORT
	teamPreviewReady = 3 * time.Minute // installing dependencies takes a while
	// teamLinkKeep is how long a preview's address must have left for a
	// new preview to keep it; otherwise it gets a new one, for a day.
	teamLinkKeep = 12 * time.Hour
	// teamLinkRenew is how long before production's address ends that hi
	// gives it a new one.
	teamLinkRenew = 30 * time.Minute
)

const (
	teamSlotPreview    = "preview"
	teamSlotProduction = "production"
)

// teamPreview is run/<slot>.json: what an address shows.
type teamPreview struct {
	Slot      string    `json:"slot"`
	Source    string    `json:"source"`          // the branch, or a task's ID
	Label     string    `json:"label,omitempty"` // which version it is, as the lead says it
	Commit    string    `json:"commit"`
	Command   string    `json:"command"`
	Port      int       `json:"port,omitempty"` // the app's port in its box
	HostPort  int       `json:"host_port"`
	Container string    `json:"container,omitempty"`
	App       string    `json:"app,omitempty"` // the app's copy of the code
	URL       string    `json:"url"`
	Password  string    `json:"password,omitempty"`
	Expose    string    `json:"expose"` // hi net expose's name, to stop it
	Started   time.Time `json:"started"`
	Expires   time.Time `json:"expires"` // when the address ends
	// Settings is teamSettingsStamp when the box started; a change starts
	// the app again.
	Settings string `json:"settings,omitempty"`
	// Relay is teamRelayStamp when the address's relay started: what it
	// routes to hi, such as /docs/.
	Relay string `json:"relay,omitempty"`
	// Message is hi's message in the channel with production's link.
	Message string `json:"message,omitempty"`
	// NewLink says the address isn't the one the people had; only in replies.
	NewLink bool `json:"new_link,omitempty"`
	// Permalink is the channel message's link; only in replies.
	Permalink string `json:"permalink,omitempty"`
	// Domain is the name that stays the address is on (hi team domain),
	// or "" for one NetBird made up. DomainError says why the name it
	// should be on didn't work, at DomainChecked; hi tries again later.
	Domain        string    `json:"domain,omitempty"`
	DomainError   string    `json:"domain_error,omitempty"`
	DomainChecked time.Time `json:"domain_checked,omitzero"`
}

// teamDomainRetry is how long hi waits before trying a name that failed
// again.
const teamDomainRetry = 30 * time.Minute

// teamDomainSettled says the slot's address is on the name it should be,
// or the name failed a short while ago.
func teamDomainSettled(state teamPreview, wanted string) bool {
	return state.Domain == wanted || (state.DomainError != "" && time.Since(state.DomainChecked) < teamDomainRetry)
}

func teamSlotPath(team, slot string) string {
	return filepath.Join(teamDir(team), "run", slot+".json")
}

func teamPreviewPath(team string) string { return teamSlotPath(team, teamSlotPreview) }

// teamSlotPortFile is the port the slot's address relays to.
func teamSlotPortFile(team, slot string) string {
	return filepath.Join(teamDir(team), "run", slot+".port")
}

// teamSlotAddressDir holds the slot's address in a file the app's box sees
// read-only at teamAppAddressFile. hi rewrites it when the address
// changes, which it does each day, so the app reads it when it needs it.
func teamSlotAddressDir(team, slot string) string {
	return filepath.Join(teamDir(team), "run", slot+"-address")
}

const teamAppAddressFile = "/hi/address"

// teamAppDataDir is where an app box keeps what must outlive a deploy
// ($HI_DATA_DIR): production's is app/ in the team's folder, which
// snapshots hold; a preview's is its own, in run/.
const teamAppDataDir = "/data"

func teamSlotDataDir(team, slot string) string {
	if slot == teamSlotProduction {
		return filepath.Join(teamDir(team), "app")
	}
	return filepath.Join(teamDir(team), "run", slot+"-data")
}

func writeTeamSlotAddress(team, slot, url string) error {
	dir := teamSlotAddressDir(team, slot)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(dir, filepath.Base(teamAppAddressFile)), []byte(url+"\n"), 0o600)
}

// teamPreviewContainer is the start of the name of every app box of a team.
func teamPreviewContainer(team string) string { return "hi-box-team-" + team + "-app" }

func loadTeamSlot(team, slot string) (teamPreview, bool) {
	var state teamPreview
	data, err := os.ReadFile(teamSlotPath(team, slot))
	if err != nil || json.Unmarshal(data, &state) != nil {
		return teamPreview{}, false
	}
	state.Slot = slot
	if state.Container == "" {
		state.Container = teamPreviewContainer(team) // from an older hi
	}
	return state, true
}

func saveTeamSlot(team string, state teamPreview) error {
	state.NewLink, state.Permalink = false, ""
	data, _ := json.MarshalIndent(state, "", "  ")
	return writeFileAtomic(teamSlotPath(team, state.Slot), append(data, '\n'), 0o600)
}

// teamRunCommand is how the project starts: make hi-run, for a project
// whose own run target means something else (hi team adopt), else make
// run, else the start script, with Bun when the project has a bun.lock
// and npm otherwise.
func teamRunCommand(dir string) (string, error) {
	if data, err := os.ReadFile(filepath.Join(dir, "Makefile")); err == nil {
		for _, target := range []string{"hi-run", "run"} {
			if regexp.MustCompile(`(?m)^` + target + `\s*:`).Match(data) {
				return "make " + target, nil
			}
		}
	}
	if data, err := os.ReadFile(filepath.Join(dir, "package.json")); err == nil {
		var pkg struct {
			Scripts map[string]string `json:"scripts"`
		}
		if json.Unmarshal(data, &pkg) == nil && pkg.Scripts["start"] != "" {
			if fileExists(filepath.Join(dir, "bun.lock")) {
				return "bun install --frozen-lockfile && bun run start", nil
			}
			install := "npm install --no-audit --no-fund"
			if fileExists(filepath.Join(dir, "package-lock.json")) {
				install = "npm ci --no-audit --no-fund"
			}
			return install + " && npm start", nil
		}
	}
	return "", errors.New("the project has no way to start: ask the coder to add a Makefile with a run target that starts the app on $PORT, listening on 0.0.0.0")
}

type teamPreviewRequest struct {
	Task       string `json:"task,omitempty"`
	Production bool   `json:"production,omitempty"`
	Port       int    `json:"port,omitempty"`
	Stop       bool   `json:"stop,omitempty"`
	// Notes say what is new in dev, for the people: the DEV line's thread.
	Notes string `json:"notes,omitempty"`
}

// preview puts dev, a task's branch, or production on its address, or
// stops the preview.
func (b *teamBroker) preview(request teamPreviewRequest) (teamPreview, error) {
	if request.Production {
		return b.production(request.Port)
	}
	b.previewMu.Lock()
	defer b.previewMu.Unlock()
	if request.Stop {
		b.stopSlot(teamSlotPreview)
		return teamPreview{}, nil
	}
	config, err := loadTeam(b.team)
	if err != nil {
		return teamPreview{}, err
	}
	next := teamPreview{Slot: teamSlotPreview, Source: config.Branch, Label: "dev", Port: request.Port}
	ref := config.Branch
	if view, err := teamVersionsReport(b.team); err == nil {
		next.Label = view.Dev
	}
	if request.Task != "" {
		// A task the broker had fixed shows as its latest fix.
		task, busy, err := teamLatestWork(b.team, request.Task)
		if err != nil {
			return teamPreview{}, err
		}
		if busy != nil && busy.ID != request.Task && busy.Kind == "task" {
			return teamPreview{}, fmt.Errorf("%s's work is being fixed after a review (%s); preview it once hi team wait %s says how it went", request.Task, busy.ID, request.Task)
		}
		if task.Branch == "" || task.Status == "merged" {
			return teamPreview{}, fmt.Errorf("%s has no branch of its own (%s); preview %s instead", task.ID, task.Status, config.Branch)
		}
		next.Source, next.Label, ref = task.ID, fmt.Sprintf("idea %s, not in dev yet", task.ID), task.Branch
		return b.show(next, ref, teamLinkKeep)
	}
	state, err := b.show(next, ref, teamLinkKeep)
	if err == nil {
		b.announceDev(config, state, request.Notes)
	}
	return state, err
}

func teamDevAnnouncedPath(team string) string {
	return filepath.Join(teamDir(team), "run", "dev-announced")
}

// announceDev tells the channel, in one line, that the preview shows a new
// version of dev, once per version; what is new goes in the line's
// thread: the lead's notes, or the work merged since the last one.
func (b *teamBroker) announceDev(config teamConfig, state teamPreview, notes string) {
	if b.channel == nil || teamFreshApp(b.repo, config.Branch) {
		return
	}
	data, _ := os.ReadFile(teamDevAnnouncedPath(b.team))
	last := strings.TrimSpace(string(data))
	if last == state.Commit {
		return
	}
	view, err := teamVersionsReport(b.team)
	if err != nil || len(view.Changes) == 0 && view.Production > 0 {
		return // the same as production: nothing new to try
	}
	label := strings.TrimPrefix(view.Dev, "dev, ")
	docs := ""
	if state.URL != "" && state.Relay == teamRelayStamp(b.team, teamSlotPreview) {
		docs = teamDocsLink(state.URL)
	}
	details := strings.TrimSpace(notes)
	if details == "" {
		var lines []string
		all := loadTeamTasks(b.team)
		var fresh []teamTask
		for _, task := range view.Changes {
			if last == "" || !teamIsAncestor(b.repo, task.Merged, last) {
				fresh = append(fresh, task)
			}
		}
		for _, piece := range teamDevWork(all, fresh) {
			lines = append(lines, fmt.Sprintf("• %s (%s), %s", teamSlackEscape(piece.Root.Title), strings.Join(piece.IDs, ", "), teamTaskAskedBy(piece.Root)))
		}
		if len(lines) > 0 {
			details = "New in dev:\n" + strings.Join(lines, "\n")
		}
	}
	if view.Production > 0 {
		details += fmt.Sprintf("\n\nProduction stays on v%d until an owner releases dev.", view.Production)
	}
	if b.announceInThread(config, fmt.Sprintf("New DEV version (%s) :test_tube:", label)+teamSlotLinks(state.URL, docs), details) != "" {
		writeFileAtomic(teamDevAnnouncedPath(b.team), []byte(state.Commit+"\n"), 0o600)
	}
}

// startPreview puts the new team's blank app on the preview when the team
// starts, so the people's own links (/hi) work before anyone has asked for
// anything. Only while the project is as hi team new wrote it: once there
// is work in it, or for a project cloned or started empty, the lead makes
// the previews.
func (b *teamBroker) startPreview(stderr io.Writer) {
	if state, ok := loadTeamSlot(b.team, teamSlotPreview); ok {
		if _, exposed := b.exposed(state.Expose); exposed {
			return
		}
	}
	config, err := loadTeam(b.team)
	if err != nil || !teamFreshApp(b.repo, config.Branch) {
		return
	}
	if _, err := b.preview(teamPreviewRequest{}); err != nil {
		fmt.Fprintf(stderr, "hi: the blank app's preview: %v\n", err)
	}
}

// teamFreshApp says the project is still the blank app from team-app: its
// only commit outside the lead's specs is hi team new's.
func teamFreshApp(repo, branch string) bool {
	data, ok := teamGitShow(repo, branch, templateMetadataPath)
	var metadata templateMetadataFile
	if !ok || json.Unmarshal([]byte(data), &metadata) != nil || metadata.Template != teamAppTemplate {
		return false
	}
	return len(strings.Fields(boxGit(repo, "rev-list", branch, "--", ".", ":!"+teamSpecsPath))) == 1
}

// production puts production on its address and keeps hi's message in the
// channel up to date with it.
func (b *teamBroker) production(port int) (teamPreview, error) {
	b.previewMu.Lock()
	defer b.previewMu.Unlock()
	versions, err := loadTeamVersions(b.team)
	if err != nil {
		return teamPreview{}, err
	}
	if versions.Production == 0 {
		return teamPreview{}, errors.New("nothing is released to production yet")
	}
	if port == 0 {
		if current, ok := loadTeamSlot(b.team, teamSlotProduction); ok {
			port = current.Port
		}
	}
	next := teamPreview{Slot: teamSlotProduction, Source: teamProductionBranch, Label: fmt.Sprintf("production v%d", versions.Production), Port: port}
	state, err := b.show(next, teamProductionBranch, teamLinkRenew)
	if err != nil {
		return state, err
	}
	state.Permalink = b.postProductionLink(&state)
	return state, nil
}

// show puts a commit on a slot's address. A new version starts in a box
// beside the one there, and the address moves to it once it answers. The
// address stays while it has keep left; otherwise it gets a new one.
func (b *teamBroker) show(next teamPreview, ref string, keep time.Duration) (teamPreview, error) {
	if next.Port == 0 {
		next.Port = teamPreviewPort
	}
	if next.Port < 1 || next.Port > 65535 {
		return teamPreview{}, fmt.Errorf("%d isn't a port", next.Port)
	}
	next.Commit = boxGit(b.repo, "rev-parse", ref)
	if next.Commit == "" {
		return teamPreview{}, fmt.Errorf("%s isn't in the repository", ref)
	}
	current, had := loadTeamSlot(b.team, next.Slot)
	_, exposed := b.exposed(current.Expose)
	exposed = had && exposed
	wanted := teamSlotDomain(b.team, next.Slot)
	// An address that stays, on a relay from before hi routed what it
	// routes now, moves to a new relay with the same key, so the links
	// people have keep working.
	relay := teamRelayStamp(b.team, next.Slot)
	outdated := teamRelayOutdated(current, wanted, relay) && exposed
	lasts := exposed && time.Until(current.Expires) > keep && teamDomainSettled(current, wanted) && !outdated
	next.Settings = teamSettingsStamp(b.team)
	if lasts && current.Commit == next.Commit && current.Port == next.Port && current.Settings == next.Settings && b.running(current.Container) {
		current.Label = next.Label
		saveTeamSlot(b.team, current)
		return current, nil // it shows this already
	}

	// The app gets a copy of the commit of its own; its installs and
	// files stay out of repo/.
	hostPort, err := teamFreePort()
	if err != nil {
		return teamPreview{}, err
	}
	next.HostPort = hostPort
	next.Container = fmt.Sprintf("%s-%s-%d", teamPreviewContainer(b.team), next.Slot, hostPort)
	next.App = filepath.Join(teamDir(b.team), "run", fmt.Sprintf("app-%s-%d", next.Slot, hostPort))
	os.RemoveAll(next.App)
	if err := os.MkdirAll(next.App, 0o700); err != nil {
		return teamPreview{}, err
	}
	discard := func() {
		b.engineOutput("rm", "-f", next.Container)
		os.RemoveAll(next.App)
	}
	still := ""
	if exposed {
		still = fmt.Sprintf(" The address still shows %s.", firstNonEmpty(current.Label, current.Source))
	}
	if out, err := teamShell(b.repo, fmt.Sprintf("git archive %s | tar -x -C %s", shellQuote(next.Commit), shellQuote(next.App))); err != nil {
		discard()
		return teamPreview{}, fmt.Errorf("copying %s: %s", ref, qFirstLine(out, err.Error()))
	}
	next.Command, err = teamRunCommand(next.App)
	if err != nil {
		discard()
		return teamPreview{}, err
	}
	address := ""
	if exposed {
		address = current.URL
	}
	if err := writeTeamSlotAddress(b.team, next.Slot, address); err != nil {
		discard()
		return teamPreview{}, err
	}
	data := teamSlotDataDir(b.team, next.Slot)
	if err := os.MkdirAll(data, 0o700); err != nil {
		discard()
		return teamPreview{}, err
	}
	if err := b.app(next.Slot, next.Container, next.App, data, teamSlotAddressDir(b.team, next.Slot), next.Command, next.Port, hostPort); err != nil {
		discard()
		return teamPreview{}, fmt.Errorf("%w.%s", err, still)
	}
	if err := teamWaitHTTP(fmt.Sprintf("http://127.0.0.1:%d/", hostPort), teamPreviewReady); err != nil {
		logs, _ := b.engineOutput("logs", "--tail", "15", next.Container)
		discard()
		return teamPreview{}, fmt.Errorf("the app didn't answer on port %d within %s; it must listen on 0.0.0.0:$PORT.%s Its last lines:\n%s", next.Port, teamPreviewReady, still, strings.TrimSpace(logs))
	}

	// The address moves to the new box.
	portFile := teamSlotPortFile(b.team, next.Slot)
	if err := writeFileAtomic(portFile, []byte(strconv.Itoa(hostPort)+"\n"), 0o600); err != nil {
		discard()
		return teamPreview{}, err
	}
	next.Started = time.Now().UTC()
	next.URL, next.Password, next.Expose, next.Expires, next.Message = current.URL, current.Password, current.Expose, current.Expires, current.Message
	next.Domain, next.DomainError, next.DomainChecked = current.Domain, current.DomainError, current.DomainChecked
	next.Relay = current.Relay
	if !lasts {
		name := "team-" + b.team
		if next.Slot == teamSlotProduction {
			name += "-live"
		}
		key := ""
		if outdated {
			key = teamLinkKey(current.URL)
		}
		exposure, err := b.expose(hostPort, portFile, name, next.Slot, wanted, key)
		domain := wanted
		if wanted != "" {
			next.DomainError, next.DomainChecked = "", time.Now().UTC()
			if err != nil {
				// The name that stays doesn't work (yet): an address NetBird
				// makes up, unless the one the people have still lasts.
				next.DomainError = err.Error()
				if exposed && time.Until(current.Expires) > keep {
					exposure, err = netExposeState{}, errors.New("kept the address")
				} else {
					exposure, err = b.expose(hostPort, portFile, name, next.Slot, "", "")
					domain = ""
				}
			}
		}
		switch {
		case err != nil && !exposed:
			discard()
			os.Remove(portFile)
			return teamPreview{}, err
		case err == nil:
			if exposed {
				b.run("net", "expose", "stop", current.Expose)
			}
			link := firstNonEmpty(exposure.Link, exposure.URL)
			next.URL, next.Password, next.Expose, next.Expires, next.NewLink = link, exposure.Password, exposure.Name, exposure.Expires, !exposed || link != current.URL
			next.Relay = relay
			next.Domain = domain
			writeTeamSlotAddress(b.team, next.Slot, next.URL)
		}
		// Without a new address, the old one shows the new version for
		// the time it has left.
	}
	if had && current.Container != next.Container {
		b.engineOutput("rm", "-f", current.Container)
		if current.App != "" {
			os.RemoveAll(current.App)
		}
	}
	if err := saveTeamSlot(b.team, next); err != nil {
		return next, err
	}
	if next.NewLink {
		// The DMs with people's own links get the new address.
		go b.refreshLinks()
	}
	return next, nil
}

// expose puts the port in portFile on a new address, for a day, with the
// helper in its pages while the slot's helper is on.
func (b *teamBroker) expose(hostPort int, portFile, name, slot, domain, key string) (netExposeState, error) {
	longest := netExposeMaxMax
	if domain != "" {
		longest = netExposeDomainMax
	}
	args := []string{"net", "expose", strconv.Itoa(hostPort), "--follow", portFile, "--link", "--detach", "--json", "--max", longest.String(), "--name", name}
	if domain != "" {
		args = append(args, "--domain", domain)
	}
	if key != "" {
		args = append(args, "--key", key)
	}
	out, err := b.run(append(args, teamExposeHelperArgs(b.team, slot)...)...)
	if err != nil {
		return netExposeState{}, fmt.Errorf("hi net expose: %w (the app runs, but only on this machine)", err)
	}
	var exposure netExposeState
	if err := json.Unmarshal(out, &exposure); err != nil || exposure.URL == "" {
		return netExposeState{}, fmt.Errorf("hi net expose gave no address: %s", qFirstLine(string(out), "no output"))
	}
	return exposure, nil
}

// teamExposed is the running exposure with this name.
func teamExposed(name string) (netExposeState, bool) {
	if name == "" {
		return netExposeState{}, false
	}
	state, ok := netExposeStates()[name]
	return state, ok
}

func (b *teamBroker) appRunning(container string) bool {
	out, err := b.engineOutput("inspect", "-f", "{{.State.Running}}", container)
	return err == nil && strings.TrimSpace(out) == "true"
}

// appExitState is how an app's box that stopped ended: its exit code and
// when. ok is false for a box that runs, or isn't there.
func (b *teamBroker) appExitState(container string) (int, time.Time, bool) {
	out, err := b.engineOutput("inspect", "-f", "{{.State.Running}} {{.State.ExitCode}} {{.State.FinishedAt}}", container)
	fields := strings.Fields(out)
	if err != nil || len(fields) < 3 || fields[0] != "false" {
		return 0, time.Time{}, false
	}
	exit, err := strconv.Atoi(fields[1])
	if err != nil {
		return 0, time.Time{}, false
	}
	finished, ok := teamParseFinishedAt(fields[2:])
	return exit, finished, ok
}

// teamParseFinishedAt reads when a box stopped, as the engine says it:
// Docker 2026-10-18T14:02:11.123Z, Podman 2026-10-18 14:02:11.123 +0000 UTC.
func teamParseFinishedAt(fields []string) (time.Time, bool) {
	if len(fields) == 1 {
		finished, err := time.Parse(time.RFC3339Nano, fields[0])
		return finished, err == nil
	}
	if len(fields) >= 3 {
		finished, err := time.Parse("2006-01-02 15:04:05.999999999 -0700", strings.Join(fields[:3], " "))
		return finished, err == nil
	}
	return time.Time{}, false
}

// stopSlot ends a slot's address and its app's box.
func (b *teamBroker) stopSlot(slot string) {
	if state, ok := loadTeamSlot(b.team, slot); ok {
		if state.Expose != "" {
			b.run("net", "expose", "stop", state.Expose)
		}
		b.engineOutput("rm", "-f", state.Container)
		if state.App != "" {
			os.RemoveAll(state.App)
		}
	}
	os.Remove(teamSlotPath(b.team, slot))
	os.Remove(teamSlotPortFile(b.team, slot))
	os.RemoveAll(teamSlotAddressDir(b.team, slot))
}

// teamProductionLinkText is hi's message in the channel with production's
// link. Slack shows the time in each reader's own time zone.
func teamProductionLinkText(state teamPreview) string {
	return fmt.Sprintf(":globe_with_meridians: *%s* is at <%s|this link>. hi keeps this message current: when production changes, or its link is renewed, the link here changes with it. Updated <!date^%d^{date_short_pretty} {time}|%s>.",
		teamCapitalize(state.Label), state.URL, time.Now().Unix(), time.Now().UTC().Format("2 Jan 15:04 UTC"))
}

func teamCapitalize(text string) string {
	if text == "" {
		return text
	}
	return strings.ToUpper(text[:1]) + text[1:]
}

// postProductionLink puts production's link in hi's message in the
// channel, editing the one there, and returns the message's own link.
func (b *teamBroker) postProductionLink(state *teamPreview) string {
	if b.channel == nil {
		return ""
	}
	config, err := loadTeam(b.team)
	if err != nil {
		return ""
	}
	text := teamProductionLinkText(*state)
	if state.Message == "" || b.channel.update(config.Channel, state.Message, text) != nil {
		ts, err := b.channel.post(config.Channel, text)
		if err != nil {
			return ""
		}
		state.Message = ts
		saveTeamSlot(b.team, *state)
	}
	link, _ := b.channel.permalink(config.Channel, state.Message)
	go b.refreshLinks()
	return link
}

// keepLinks keeps production on its address, and gives it a new one
// before the old ends; a preview whose address has ended is stopped.
func (b *teamBroker) keepLinks(done <-chan struct{}, stderr io.Writer) {
	var failed, previewFailed time.Time
	for {
		if preview, ok := loadTeamSlot(b.team, teamSlotPreview); ok {
			if _, exposed := b.exposed(preview.Expose); !exposed {
				b.previewMu.Lock()
				b.stopSlot(teamSlotPreview)
				b.previewMu.Unlock()
			} else if (preview.Settings != teamSettingsStamp(b.team) || teamRelayOutdated(preview, teamSlotDomain(b.team, teamSlotPreview), teamRelayStamp(b.team, teamSlotPreview))) &&
				time.Since(previewFailed) > 30*time.Minute {
				// A new hi, or new settings: the same version in a new box,
				// on the same address.
				if _, err := b.refreshPreview(preview); err != nil {
					fmt.Fprintf(stderr, "hi: the preview in a new box: %v\n", err)
					previewFailed = time.Now()
				}
			}
		}
		if time.Since(failed) > 30*time.Minute && b.productionNeedsWork() {
			if state, ok := loadTeamSlot(b.team, teamSlotProduction); ok {
				b.appStopped(state) // an incident, when it stopped on its own
			}
			if _, err := b.production(0); err != nil {
				fmt.Fprintf(stderr, "hi: putting production on its address: %v\n", err)
				failed = time.Now()
			} else {
				failed = time.Time{}
			}
		}
		select {
		case <-done:
			return
		case <-time.After(time.Minute):
		}
	}
}

// refreshPreview shows what the preview shows in a new box, beside the
// old one, keeping its address and what it says it is.
func (b *teamBroker) refreshPreview(current teamPreview) (teamPreview, error) {
	b.previewMu.Lock()
	defer b.previewMu.Unlock()
	if now, ok := loadTeamSlot(b.team, teamSlotPreview); !ok || now.Container != current.Container {
		return now, nil // changed meanwhile
	}
	next := teamPreview{Slot: teamSlotPreview, Source: current.Source, Label: current.Label, Port: current.Port}
	return b.show(next, current.Commit, teamLinkRenew)
}

// productionNeedsWork says production is released but its address is
// missing, about to end, or shows something else.
func (b *teamBroker) productionNeedsWork() bool {
	versions, err := loadTeamVersions(b.team)
	if err != nil || versions.Production == 0 {
		return false
	}
	state, ok := loadTeamSlot(b.team, teamSlotProduction)
	if !ok {
		return true
	}
	_, exposed := b.exposed(state.Expose)
	return !exposed || time.Until(state.Expires) <= teamLinkRenew || state.Settings != teamSettingsStamp(b.team) ||
		!teamDomainSettled(state, teamSlotDomain(b.team, teamSlotProduction)) ||
		state.Commit != boxGit(b.repo, "rev-parse", teamProductionBranch) || !b.running(state.Container) ||
		teamRelayOutdated(state, teamSlotDomain(b.team, teamSlotProduction), teamRelayStamp(b.team, teamSlotProduction))
}

// teamRelayOutdated says a slot's address is on a name that stays but its
// relay routes less than hi does now. An address NetBird made up gets the
// new routes with its next address, within a day, rather than new links
// for everyone now.
func teamRelayOutdated(state teamPreview, wanted, relay string) bool {
	return wanted != "" && state.Domain == wanted && state.Relay != relay
}

// teamLinkKey is the key in an address's link.
func teamLinkKey(link string) string {
	parsed, err := url.Parse(link)
	if err != nil {
		return ""
	}
	return parsed.Query().Get(netExposeKeyParam)
}

// startApp runs the project in its box: the hardening every box has, no
// credentials, its port published to this machine only, its data folder
// at /data, its address folder read-only at /hi, its slot and the team's
// settings, and the models socket, which hi team __app relays to an
// ordinary address in the box.
func (b *teamBroker) startApp(slot, name, src, data, address, command string, port, hostPort int) error {
	engine, _, err := detectBoxEngine()
	if err != nil {
		return err
	}
	image, err := engine.ensureBaseImage(nopWriter{}, nopWriter{})
	if err != nil {
		return err
	}
	models := teamModelsDir(b.team)
	if err := os.MkdirAll(models, 0o700); err != nil {
		return err
	}
	jobs := teamSlotJobsDir(b.team, slot)
	if err := os.MkdirAll(jobs, 0o700); err != nil {
		return err
	}
	envFile, err := writeTeamAppEnvFile(b.team, teamAppEnv(b.team, slot, port))
	if err != nil {
		return err
	}
	defer os.Remove(envFile)
	run := []string{"run", "-d", "--init", "--name", name, "--hostname", b.team + "-app",
		"-p", fmt.Sprintf("127.0.0.1:%d:%d", hostPort, port)}
	run = append(run, engine.userArgs()...)
	run = append(run, boxHardening("4g")...)
	run = append(run, "-v", src+":/app", "-w", "/app", "-v", data+":"+teamAppDataDir,
		"-v", address+":"+filepath.Dir(teamAppAddressFile)+":ro",
		"-v", models+":"+teamAppModelsDir,
		"-v", jobs+":"+teamAppJobsDir,
		"-v", b.hi+":"+teamAppRelay+":ro",
		"--env-file", envFile,
		image, teamAppRelay, "team", "__app", teamAppModelsDir+"/"+teamModelsSocket, strconv.Itoa(teamAppModelsPort), command)
	name, args := teamOutsideService(engine.bin, run)
	if out, err := exec.Command(name, args...).CombinedOutput(); err != nil {
		return fmt.Errorf("%s: %s", engine.name, qFirstLine(strings.TrimSpace(string(out)), err.Error()))
	}
	return nil
}

// teamOutsideService runs a command in a systemd scope of its own when hi
// team runs under systemd, so the containers and processes it leaves
// behind aren't stopped with the team's service.
func teamOutsideService(name string, args []string) (string, []string) {
	if os.Getenv("INVOCATION_ID") == "" {
		return name, args
	}
	if _, err := exec.LookPath("systemd-run"); err != nil {
		return name, args
	}
	return "systemd-run", append([]string{"--user", "--scope", "--quiet", "--collect", "--", name}, args...)
}

func (b *teamBroker) engineOutput(args ...string) (string, error) {
	engine, _, err := detectBoxEngine()
	if err != nil {
		return "", err
	}
	return engine.output(args...)
}

type nopWriter struct{}

func (nopWriter) Write(p []byte) (int, error) { return len(p), nil }

func teamFreePort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port, nil
}

// teamWaitHTTP waits until something answers HTTP at url.
func teamWaitHTTP(url string, limit time.Duration) error {
	client := &http.Client{Timeout: 3 * time.Second}
	deadline := time.Now().Add(limit)
	for {
		if response, err := client.Get(url); err == nil {
			response.Body.Close()
			return nil
		} else if time.Now().After(deadline) {
			return err
		}
		time.Sleep(2 * time.Second)
	}
}

func teamShell(dir, script string) (string, error) {
	var out strings.Builder
	err := boxCommand(nil, &out, &out, "sh", "-c", "cd "+shellQuote(dir)+" && "+script)
	return strings.TrimSpace(out.String()), err
}

// writeFileAtomic writes path whole or not at all, so a reader never sees
// half of it.
func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	if err := os.WriteFile(path+".tmp", data, mode); err != nil {
		return err
	}
	return os.Rename(path+".tmp", path)
}

// printTeamPreview tells the lead what an address shows, and what to say.
func printTeamPreview(w io.Writer, preview teamPreview) {
	label := firstNonEmpty(preview.Label, preview.Source)
	commit := preview.Commit[:min(len(preview.Commit), 12)]
	until := preview.Expires.UTC().Format("Mon 15:04 UTC")
	if preview.Slot == teamSlotProduction {
		fmt.Fprintf(w, "%s (%s) is up at:\n  %s\n", teamCapitalize(label), commit, preview.URL)
		if preview.Permalink != "" {
			fmt.Fprintf(w, "Post this link itself when people ask for production, not a link to an older message. hi also keeps a message in the channel with the current one: %s\n", preview.Permalink)
		}
		return
	}
	if preview.NewLink {
		fmt.Fprintf(w, "Preview of %s (%s) is on a new link, until %s. Say which version it is, and post the link whole, as [a few words](link) or on a line of its own, never in bold or code; its key lets the people in:\n  %s\n", label, commit, until, preview.URL)
	} else {
		fmt.Fprintf(w, "Preview of %s (%s) is on the same link as before, until %s: the link the people have shows it now, so tell them to reload it and say which version it is. It is:\n  %s\n", label, commit, until, preview.URL)
	}
	if preview.Password != "" {
		fmt.Fprintf(w, "  password: %s\n", preview.Password)
	}
	fmt.Fprintln(w, "It works on phones too. A new preview shows on the same link while it has 12 hours left; hi team preview stop ends it.")
}
