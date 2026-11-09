package cli

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

// appPreviewReady is how long a new version may take to answer while its
// app still runs: installing dependencies takes a while, and migrations
// on a large database before the app listens longer. An app that stops
// fails at once.
var appPreviewReady = 30 * time.Minute

const (
	appPreviewPort = 8080 // the app's port in its box, as $PORT
	// appLinkKeep is how long a preview's address must have left for a
	// new preview to keep it; otherwise it gets a new one, for a day.
	appLinkKeep = 12 * time.Hour
	// appLinkRenew is how long before production's address ends that hi
	// gives it a new one.
	appLinkRenew = 30 * time.Minute
)

const (
	appSlotPreview    = "preview"
	appSlotProduction = "production"
)

// appPreview is run/<slot>.json: what an address shows.
type appPreview struct {
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
	// Settings is appSettingsStamp when the box started; a change starts
	// the app again.
	Settings string `json:"settings,omitempty"`
	// Data says what hi did with the preview's data first: a fresh copy of
	// production's for new work (app_data_fork.go).
	Data string `json:"data,omitempty"`
	// Relay is appRelayStamp when the address's relay started: what it
	// routes to hi, such as /docs/.
	Relay string `json:"relay,omitempty"`
	// Message is hi's message in the channel with production's link.
	Message string `json:"message,omitempty"`
	// NewLink says the address isn't the one the people had; only in replies.
	NewLink bool `json:"new_link,omitempty"`
	// Permalink is the channel message's link; only in replies.
	Permalink string `json:"permalink,omitempty"`
	// Docs is the docs' link, and Told where hi told of a new dev
	// version ("thread", "dm", "channel"); only in replies.
	Docs string `json:"docs,omitempty"`
	Told string `json:"told,omitempty"`
	// Domain is the name that stays the address is on (hi app domain),
	// or "" for one NetBird made up. DomainError says why the name it
	// should be on didn't work, at DomainChecked; hi tries again later.
	Domain        string    `json:"domain,omitempty"`
	DomainError   string    `json:"domain_error,omitempty"`
	DomainChecked time.Time `json:"domain_checked,omitzero"`
	// AllowIPs is the --allow-ip the address got: a change in the team's
	// setting moves it to a new one.
	AllowIPs string `json:"allow_ips,omitempty"`
	// Workers says the box's relay holds the slot's workers' lock
	// (app_workers.go): a box from a hi before it has nothing to hand
	// over.
	Workers bool `json:"workers,omitempty"`
}

// appDomainRetry is how long hi waits before trying a name that failed
// again.
const appDomainRetry = 30 * time.Minute

// appDomainSettled says the slot's address is on the name it should be,
// or the name failed a short while ago.
func appDomainSettled(state appPreview, wanted string) bool {
	return state.Domain == wanted || (state.DomainError != "" && time.Since(state.DomainChecked) < appDomainRetry)
}

func appSlotPath(team, slot string) string {
	return filepath.Join(appDir(team), "run", slot+".json")
}

// appSlotPortFile is the port the slot's address relays to.
func appSlotPortFile(team, slot string) string {
	return filepath.Join(appDir(team), "run", slot+".port")
}

// appSlotAddressDir holds the slot's address in a file the app's box sees
// read-only at appBoxAddressFile. hi rewrites it when the address
// changes, which it does each day, so the app reads it when it needs it.
func appSlotAddressDir(team, slot string) string {
	return filepath.Join(appDir(team), "run", slot+"-address")
}

const appBoxAddressFile = "/hi/address"

// appBoxDataDir is where an app box keeps what must outlive a deploy
// ($HI_DATA_DIR): production's is app/ in the team's folder, which
// snapshots hold; a preview's is its own, in run/.
const appBoxDataDir = "/data"

func appSlotDataDir(team, slot string) string {
	if slot == appSlotProduction {
		return filepath.Join(appDir(team), "app")
	}
	return filepath.Join(appDir(team), "run", slot+"-data")
}

func writeTeamSlotAddress(team, slot, url string) error {
	dir := appSlotAddressDir(team, slot)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(dir, filepath.Base(appBoxAddressFile)), []byte(url+"\n"), 0o600)
}

// appPreviewContainer is the start of the name of every app box of a team.
func appPreviewContainer(team string) string { return "hi-box-app-" + team + "-app" }

func loadTeamSlot(team, slot string) (appPreview, bool) {
	var state appPreview
	data, err := os.ReadFile(appSlotPath(team, slot))
	if err != nil || json.Unmarshal(data, &state) != nil {
		return appPreview{}, false
	}
	state.Slot = slot
	if state.Container == "" {
		state.Container = appPreviewContainer(team) // from an older hi
	}
	return state, true
}

func saveTeamSlot(team string, state appPreview) error {
	state.NewLink, state.Permalink = false, ""
	data, _ := json.MarshalIndent(state, "", "  ")
	return writeFileAtomic(appSlotPath(team, state.Slot), append(data, '\n'), 0o600)
}

// appRunCommand is how the project starts: make hi-run, for a project
// whose own run target means something else (hi app adopt), else make
// run, else the start script, with Bun when the project has a bun.lock
// and npm otherwise.
func appRunCommand(dir string) (string, error) {
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

type appPreviewRequest struct {
	Production bool `json:"production,omitempty"`
	Port       int  `json:"port,omitempty"`
	Stop       bool `json:"stop,omitempty"`
}

// preview puts dev or production on its address, or stops the preview.
// A branch of the dev box goes on it with previewDevBranch.
func (b *appBroker) preview(request appPreviewRequest) (appPreview, error) {
	if request.Production {
		return b.production(request.Port)
	}
	b.previewMu.Lock()
	defer b.previewMu.Unlock()
	if request.Stop {
		b.stopSlot(appSlotPreview)
		return appPreview{}, nil
	}
	config, err := loadTeam(b.team)
	if err != nil {
		return appPreview{}, err
	}
	next := appPreview{Slot: appSlotPreview, Source: config.Branch, Label: "dev", Port: request.Port}
	ref := config.Branch
	if view, err := appVersionsReport(b.team); err == nil {
		next.Label = view.Dev
	}
	state, err := b.show(next, ref, appLinkKeep)
	if err != nil {
		return state, err
	}
	if state.URL != "" && state.Relay == appRelayStamp(b.team, appSlotPreview) {
		state.Docs = appDocsLink(state.URL)
	}
	return state, nil
}

// startPreview puts the new team's blank app on the preview when the team
// starts, so the people's own links (/hi) work before anyone has asked for
// anything. Only while the project is as hi app new wrote it: once there
// is work in it, or for a project cloned or started empty, the lead makes
// the previews.
func (b *appBroker) startPreview(stderr io.Writer) {
	if b.stopped() {
		return // frozen or paused: nothing new starts
	}
	if state, ok := loadTeamSlot(b.team, appSlotPreview); ok {
		if _, exposed := b.exposed(state.Expose); exposed {
			return
		}
	}
	config, err := loadTeam(b.team)
	if err != nil || !appFreshApp(b.repo, config.Branch) {
		return
	}
	if _, err := b.preview(appPreviewRequest{}); err != nil {
		fmt.Fprintf(stderr, "hi: the blank app's preview: %v\n", err)
	}
}

// appFreshApp says the project is still the blank app from team-app: its
// only commit outside the lead's specs is hi app new's.
func appFreshApp(repo, branch string) bool {
	data, ok := appGitShow(repo, branch, templateMetadataPath)
	var metadata templateMetadataFile
	if !ok || json.Unmarshal([]byte(data), &metadata) != nil {
		return false
	}
	if followRenamedBuiltins(&metadata); metadata.Template != hiAppTemplate {
		return false
	}
	return len(strings.Fields(boxGit(repo, "rev-list", branch, "--", ".", ":!"+appSpecsPath))) == 1
}

// production puts production on its address and keeps hi's message in the
// channel up to date with it.
func (b *appBroker) production(port int) (appPreview, error) {
	b.previewMu.Lock()
	defer b.previewMu.Unlock()
	versions, err := loadTeamVersions(b.team)
	if err != nil {
		return appPreview{}, err
	}
	if versions.Production == 0 {
		return appPreview{}, errors.New("nothing is released to production yet")
	}
	if port == 0 {
		if current, ok := loadTeamSlot(b.team, appSlotProduction); ok {
			port = current.Port
		}
	}
	next := appPreview{Slot: appSlotProduction, Source: appProductionBranch, Label: fmt.Sprintf("production v%d", versions.Production), Port: port}
	state, err := b.show(next, appProductionBranch, appLinkRenew)
	if err != nil {
		return state, err
	}
	state.Permalink = b.postProductionLink(&state)
	return state, nil
}

// show puts a commit on a slot's address. A new version starts in a box
// beside the one there, and the address moves to it once it answers. The
// address stays while it has keep left; otherwise it gets a new one.
func (b *appBroker) show(next appPreview, ref string, keep time.Duration) (appPreview, error) {
	if err := b.errStopped(); err != nil {
		return appPreview{}, err
	}
	if next.Port == 0 {
		next.Port = appPreviewPort
	}
	if next.Port < 1 || next.Port > 65535 {
		return appPreview{}, fmt.Errorf("%d isn't a port", next.Port)
	}
	next.Commit = boxGit(b.repo, "rev-parse", ref)
	if next.Commit == "" {
		return appPreview{}, fmt.Errorf("%s isn't in the repository", ref)
	}
	current, had := loadTeamSlot(b.team, next.Slot)
	_, exposed := b.exposed(current.Expose)
	exposed = had && exposed
	config, _ := loadTeam(b.team)
	wanted := appSlotDomain(b.team, next.Slot)
	if !appPublicOn(config, next.Slot) {
		wanted = "" // an internal address has its NetBird name
	}
	// An address that stays, on a relay from before hi routed what it
	// routes now, moves to a new relay with the same key, so the links
	// people have keep working.
	relay := appRelayStamp(b.team, next.Slot)
	outdated := (appRelayOutdated(current, wanted, relay) || appAccessOutdated(b.team, current) || appExposeKindOutdated(config, next.Slot, current.Expose) ||
		appInternalMoved(b.team, next.Slot, current.Expose, current.URL)) && exposed
	lasts := exposed && time.Until(current.Expires) > keep && appDomainSettled(current, wanted) && !outdated
	next.Settings = appSettingsStamp(b.team)
	// hi app rebuild built the base image again: a new box, on it.
	rebuilt := had && b.oldImage != nil && b.oldImage(current.Container)
	if lasts && current.Commit == next.Commit && current.Port == next.Port && current.Settings == next.Settings && !rebuilt && b.running(current.Container) {
		current.Label = next.Label
		saveTeamSlot(b.team, current)
		return current, nil // it shows this already
	}
	// The box shows this already, and only its address is gone or ending:
	// the box gets a new address and keeps running. A new box beside it
	// would start the app a second time on its data while the address
	// moved, and wait for it to answer.
	if had && current.Commit == next.Commit && current.Port == next.Port && current.Settings == next.Settings && !rebuilt &&
		current.HostPort != 0 && b.running(current.Container) && b.answers(current.HostPort) {
		current.Label, current.NewLink = next.Label, false
		if err := b.newAddress(&current, current, exposed, outdated, wanted, relay, keep); err != nil {
			return appPreview{}, err
		}
		if err := saveTeamSlot(b.team, current); err != nil {
			return current, err
		}
		if current.NewLink {
			go b.refreshLinks()
		}
		return current, nil
	}

	// The app gets a copy of the commit of its own; its installs and
	// files stay out of repo/.
	hostPort, err := appFreePort()
	if err != nil {
		return appPreview{}, err
	}
	next.HostPort = hostPort
	next.Container = fmt.Sprintf("%s-%s-%d", appPreviewContainer(b.team), next.Slot, hostPort)
	next.App = filepath.Join(appDir(b.team), "run", fmt.Sprintf("app-%s-%d", next.Slot, hostPort))
	os.RemoveAll(next.App)
	if err := os.MkdirAll(next.App, 0o700); err != nil {
		return appPreview{}, err
	}
	discard := func() {
		b.removeApp(next.Container)
		os.RemoveAll(next.App)
	}
	still := ""
	if exposed {
		still = fmt.Sprintf(" The address still shows %s.", firstNonEmpty(current.Label, current.Source))
	}
	if out, err := appShell(b.repo, fmt.Sprintf("git archive %s | tar -x -C %s", shellQuote(next.Commit), shellQuote(next.App))); err != nil {
		discard()
		return appPreview{}, fmt.Errorf("copying %s: %s", ref, qFirstLine(out, err.Error()))
	}
	next.Command, err = appRunCommand(next.App)
	if err != nil {
		discard()
		return appPreview{}, err
	}
	address := ""
	if exposed {
		address = current.URL
	}
	if err := writeTeamSlotAddress(b.team, next.Slot, address); err != nil {
		discard()
		return appPreview{}, err
	}
	data := appSlotDataDir(b.team, next.Slot)
	if err := os.MkdirAll(data, 0o700); err != nil {
		discard()
		return appPreview{}, err
	}
	// Two boxes mustn't write to one overlay: a preview on a fork stops
	// before its next version starts, a few seconds without a preview.
	if next.Slot == appSlotPreview && had && current.Container != "" && appPreviewOverlay(b.team) {
		b.removeApp(current.Container)
		still = " The preview is down until a version of it starts."
	}
	// The old box's workers stop before the new box opens the data; if
	// the new box fails, they carry on.
	endHandover := b.handOverWorkers(next.Slot, current, had)
	defer endHandover()
	next.Workers = true
	if err := b.app(next.Slot, next.Container, next.App, data, appSlotAddressDir(b.team, next.Slot), next.Command, next.Port, hostPort); err != nil {
		discard()
		return appPreview{}, fmt.Errorf("%w.%s", err, still)
	}
	if err := appWaitApp(fmt.Sprintf("http://127.0.0.1:%d/", hostPort), func() bool { return b.running(next.Container) }, appPreviewReady); err != nil {
		logs := b.lastLines(next.Container, 15)
		discard()
		if errors.Is(err, errTeamAppStopped) {
			return appPreview{}, fmt.Errorf("the app stopped before it answered on port %d.%s Its last lines:\n%s", next.Port, still, strings.TrimSpace(logs))
		}
		return appPreview{}, fmt.Errorf("the app ran for %s without answering on port %d; it must listen on 0.0.0.0:$PORT.%s Its last lines:\n%s", appPreviewReady, next.Port, still, strings.TrimSpace(logs))
	}

	// The address moves to the new box.
	portFile := appSlotPortFile(b.team, next.Slot)
	if err := writeFileAtomic(portFile, []byte(strconv.Itoa(hostPort)+"\n"), 0o600); err != nil {
		discard()
		return appPreview{}, err
	}
	next.Started = time.Now().UTC()
	next.URL, next.Password, next.Expose, next.Expires, next.Message = current.URL, current.Password, current.Expose, current.Expires, current.Message
	next.Domain, next.DomainError, next.DomainChecked = current.Domain, current.DomainError, current.DomainChecked
	next.Relay, next.AllowIPs = current.Relay, current.AllowIPs
	if !lasts {
		if err := b.newAddress(&next, current, exposed, outdated, wanted, relay, keep); err != nil {
			discard()
			os.Remove(portFile)
			return appPreview{}, err
		}
	}
	if had && current.Container != next.Container {
		b.removeApp(current.Container)
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

// newAddress puts next, the box on next.HostPort, on a new address in
// place of current's, which doesn't last. With the same key on a name that
// stays, the links people have keep working. When no new address can be
// made, current's shows next for the time it has left; the error is for
// when there is no address at all.
func (b *appBroker) newAddress(next *appPreview, current appPreview, exposed, outdated bool, wanted, relay string, keep time.Duration) error {
	// An address that isn't public is the team's internal one: its peer
	// on the NetBird network, and the internal relay behind it. Settings
	// that can't be read make nothing public.
	config, err := loadTeam(b.team)
	if err != nil {
		return fmt.Errorf("the app's settings can't be read, so its address stays as it is: %w", err)
	}
	if !appPublicOn(config, next.Slot) {
		if exposed && current.Expose != "" && !appIsInternal(current.Expose) {
			b.run("net", "expose", "stop", current.Expose)
		}
		link := appInternalURL(b.team, next.Slot)
		next.NewLink = current.URL != link
		next.URL, next.Password, next.Expose, next.Expires = link, "", appInternalExpose+next.Slot, time.Now().AddDate(10, 0, 0).UTC()
		next.Relay, next.Domain, next.DomainError, next.AllowIPs = relay, "", "", ""
		writeTeamSlotAddress(b.team, next.Slot, next.URL)
		return nil
	}
	portFile := appSlotPortFile(b.team, next.Slot)
	name := "app-" + b.team
	if next.Slot == appSlotProduction {
		name += "-live"
	}
	key := ""
	if outdated || !exposed && appAddressLost(current) {
		key = appLinkKey(current.URL)
	}
	exposure, err := b.expose(next.HostPort, portFile, name, next.Slot, wanted, key)
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
				exposure, err = b.expose(next.HostPort, portFile, name, next.Slot, "", "")
				domain = ""
			}
		}
	}
	switch {
	case err != nil && !exposed:
		return err
	case err == nil:
		if exposed {
			b.run("net", "expose", "stop", current.Expose)
		}
		link := firstNonEmpty(exposure.Link, exposure.URL)
		next.URL, next.Password, next.Expose, next.Expires, next.NewLink = link, exposure.Password, exposure.Name, exposure.Expires, !exposed || link != current.URL
		next.Relay = relay
		next.Domain = domain
		next.AllowIPs = appAllowIPs(b.team)
		writeTeamSlotAddress(b.team, next.Slot, next.URL)
	}
	return nil
}

// expose puts the port in portFile on a new address, for a day, with the
// helper in its pages while the slot's helper is on.
func (b *appBroker) expose(hostPort int, portFile, name, slot, domain, key string) (netExposeState, error) {
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
	if allow := appAllowIPs(b.team); allow != "" {
		args = append(args, "--allow-ip", allow)
	}
	out, err := b.run(append(args, appExposeHelperArgs(b.team, slot)...)...)
	if err != nil {
		return netExposeState{}, fmt.Errorf("hi net expose: %w (the app runs, but only on this machine)", err)
	}
	var exposure netExposeState
	if err := json.Unmarshal(out, &exposure); err != nil || exposure.URL == "" {
		return netExposeState{}, fmt.Errorf("hi net expose gave no address: %s", qFirstLine(string(out), "no output"))
	}
	return exposure, nil
}

// appExposed is the running exposure with this name.
func appExposed(name string) (netExposeState, bool) {
	if name == "" {
		return netExposeState{}, false
	}
	state, ok := netExposeStates()[name]
	return state, ok
}

func (b *appBroker) appRunning(container string) bool {
	out, err := b.engineOutput("inspect", "-f", "{{.State.Running}}", container)
	return err == nil && strings.TrimSpace(out) == "true"
}

// appExitState is how an app's box that stopped ended: its exit code and
// when. ok is false for a box that runs, or isn't there.
func (b *appBroker) appExitState(container string) (int, time.Time, bool) {
	out, err := b.engineOutput("inspect", "-f", "{{.State.Running}} {{.State.ExitCode}} {{.State.FinishedAt}}", container)
	fields := strings.Fields(out)
	if err != nil || len(fields) < 3 || fields[0] != "false" {
		return 0, time.Time{}, false
	}
	exit, err := strconv.Atoi(fields[1])
	if err != nil {
		return 0, time.Time{}, false
	}
	finished, ok := appParseFinishedAt(fields[2:])
	return exit, finished, ok
}

// appParseFinishedAt reads when a box stopped, as the engine says it:
// Docker 2026-10-18T14:02:11.123Z, Podman 2026-10-18 14:02:11.123 +0000 UTC.
func appParseFinishedAt(fields []string) (time.Time, bool) {
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
func (b *appBroker) stopSlot(slot string) {
	if state, ok := loadTeamSlot(b.team, slot); ok {
		if state.Expose != "" {
			b.run("net", "expose", "stop", state.Expose)
		}
		b.engineOutput("rm", "-f", state.Container)
		if state.App != "" {
			os.RemoveAll(state.App)
		}
	}
	os.Remove(appSlotPath(b.team, slot))
	os.Remove(appSlotPortFile(b.team, slot))
	os.RemoveAll(appSlotAddressDir(b.team, slot))
}

// appProductionLinkText is hi's message in the channel with production's
// link. Slack shows the time in each reader's own time zone.
func appProductionLinkText(state appPreview) string {
	return fmt.Sprintf(":globe_with_meridians: *%s* is at <%s|this link>. hi keeps this message current: when production changes, or its link is renewed, the link here changes with it. Updated <!date^%d^{date_short_pretty} {time}|%s>.",
		appCapitalize(state.Label), state.URL, time.Now().Unix(), time.Now().UTC().Format("2 Jan 15:04 UTC"))
}

func appCapitalize(text string) string {
	if text == "" {
		return text
	}
	return strings.ToUpper(text[:1]) + text[1:]
}

// postProductionLink puts production's link in hi's message in the
// channel, editing the one there, and returns the message's own link.
func (b *appBroker) postProductionLink(state *appPreview) string {
	if b.channel == nil {
		return ""
	}
	config, err := loadTeam(b.team)
	if err != nil {
		return ""
	}
	text := appProductionLinkText(*state)
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
// before the old ends. A preview whose address has had its day is
// stopped; one whose address ended early, stopped from outside hi app,
// goes back on an address, and the people hear of its new link.
func (b *appBroker) keepLinks(done <-chan struct{}, stderr io.Writer) {
	var failed, previewFailed time.Time
	for {
		// A frozen or paused app stays as the watcher left it: a box in a
		// new one, or one put back on its address, would undo that.
		if b.stopped() {
			select {
			case <-done:
				return
			case <-time.After(time.Minute):
			}
			continue
		}
		if preview, ok := loadTeamSlot(b.team, appSlotPreview); ok {
			if _, exposed := b.exposed(preview.Expose); !exposed {
				switch {
				case !appAddressLost(preview):
					b.previewMu.Lock()
					b.stopSlot(appSlotPreview)
					b.previewMu.Unlock()
				case time.Since(previewFailed) > 30*time.Minute:
					if err := b.restorePreview(preview); err != nil {
						fmt.Fprintf(stderr, "hi: the preview on a new address: %v\n", err)
						previewFailed = time.Now()
					}
				}
			} else if config, _ := loadTeam(b.team); (preview.Settings != appSettingsStamp(b.team) ||
				// Made internal or public since: the same version on its
				// new kind of address, as production and the dev page do.
				appExposeKindOutdated(config, appSlotPreview, preview.Expose) || appRelayOutdated(preview, appSlotDomain(b.team, appSlotPreview), appRelayStamp(b.team, appSlotPreview)) ||
				appAccessOutdated(b.team, preview) || b.oldImage != nil && b.oldImage(preview.Container)) &&
				time.Since(previewFailed) > 30*time.Minute {
				// A new hi, new settings, or a rebuilt image: the same
				// version in a new box, on the same address.
				if _, err := b.refreshPreview(preview); err != nil {
					fmt.Fprintf(stderr, "hi: the preview in a new box: %v\n", err)
					previewFailed = time.Now()
				}
			}
		}
		if time.Since(failed) > 30*time.Minute && b.productionNeedsWork() {
			if state, ok := loadTeamSlot(b.team, appSlotProduction); ok {
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
func (b *appBroker) refreshPreview(current appPreview) (appPreview, error) {
	b.previewMu.Lock()
	defer b.previewMu.Unlock()
	if now, ok := loadTeamSlot(b.team, appSlotPreview); !ok || now.Container != current.Container {
		return now, nil // changed meanwhile
	}
	next := appPreview{Slot: appSlotPreview, Source: current.Source, Label: current.Label, Port: current.Port}
	return b.show(next, current.Commit, appLinkRenew)
}

// appAddressLost says a slot's address ended before its time: its
// exposure was stopped from outside, such as by hi net expose stop --all.
// The exposure's own end is at Expires, give or take a moment.
func appAddressLost(state appPreview) bool {
	return state.Expose != "" && time.Until(state.Expires) > time.Minute
}

// restorePreview puts the preview, whose address ended early, on a new
// one, and gives its new link where hi told of that version.
func (b *appBroker) restorePreview(lost appPreview) error {
	// The owners see its new link on the dev page.
	_, err := b.refreshPreview(lost)
	return err
}

// productionNeedsWork says production is released but its address is
// missing, about to end, or shows something else, or its box runs hi's
// base image from before it was built again.
func (b *appBroker) productionNeedsWork() bool {
	versions, err := loadTeamVersions(b.team)
	if err != nil || versions.Production == 0 {
		return false
	}
	state, ok := loadTeamSlot(b.team, appSlotProduction)
	if !ok {
		return true
	}
	_, exposed := b.exposed(state.Expose)
	config, err := loadTeam(b.team)
	if err != nil {
		return false // settings that can't be read move nothing
	}
	wanted := appSlotDomain(b.team, appSlotProduction)
	if !appPublicOn(config, appSlotProduction) {
		wanted = ""
	}
	return !exposed || time.Until(state.Expires) <= appLinkRenew || state.Settings != appSettingsStamp(b.team) ||
		appExposeKindOutdated(config, appSlotProduction, state.Expose) || appInternalMoved(b.team, appSlotProduction, state.Expose, state.URL) ||
		!appDomainSettled(state, wanted) ||
		state.Commit != boxGit(b.repo, "rev-parse", appProductionBranch) || !b.running(state.Container) ||
		appRelayOutdated(state, appSlotDomain(b.team, appSlotProduction), appRelayStamp(b.team, appSlotProduction)) ||
		appAccessOutdated(b.team, state) || b.oldImage != nil && b.oldImage(state.Container)
}

// appOnOldImage says an app's box runs another image than hi's base image
// is now: hi app rebuild built it again, with Ubuntu's fixes.
func (b *appBroker) appOnOldImage(container string) bool {
	running, err := b.engineOutput("container", "inspect", "-f", "{{.Image}}", container)
	if err != nil {
		return false
	}
	base, err := b.engineOutput("image", "inspect", "-f", "{{.Id}}", boxBaseImage())
	if err != nil {
		return false
	}
	id := func(s string) string { return strings.TrimPrefix(strings.TrimSpace(s), "sha256:") }
	return id(running) != "" && id(base) != "" && id(running) != id(base)
}

// appRelayOutdated says a slot's address is on a name that stays but its
// relay routes less than hi does now. An address NetBird made up gets the
// new routes with its next address, within a day, rather than new links
// for everyone now.
func appRelayOutdated(state appPreview, wanted, relay string) bool {
	return wanted != "" && state.Domain == wanted && state.Relay != relay
}

// appLinkKey is the key in an address's link.
func appLinkKey(link string) string {
	parsed, err := url.Parse(link)
	if err != nil {
		return ""
	}
	return parsed.Query().Get(netExposeKeyParam)
}

// startApp runs the project in its box: the hardening every box has, no
// credentials, its port published to this machine only, its data folder
// at /data, its address folder read-only at /hi, its slot and the team's
// settings, its file settings read-only at /hi-files, the models socket, which hi team __app relays to an ordinary
// address in the box, and the slot's workers' lock, which it holds for the
// box (app_workers.go).
func (b *appBroker) startApp(slot, name, src, data, address, command string, port, hostPort int) error {
	engine, _, err := detectBoxEngine()
	if err != nil {
		return err
	}
	image, err := engine.ensureBaseImage(nopWriter{}, nopWriter{})
	if err != nil {
		return err
	}
	models := appSlotModelsDir(b.team, slot)
	if err := os.MkdirAll(models, 0o700); err != nil {
		return err
	}
	jobs := appSlotJobsDir(b.team, slot)
	if err := os.MkdirAll(jobs, 0o700); err != nil {
		return err
	}
	workers := appSlotWorkersDir(b.team, slot)
	if err := os.MkdirAll(workers, 0o700); err != nil {
		return err
	}
	envFile, err := writeTeamAppEnvFile(b.team, appBoxEnv(b.team, slot, port))
	if err != nil {
		return err
	}
	defer os.Remove(envFile)
	files, err := writeTeamAppFiles(b.team, slot)
	if err != nil {
		return err
	}
	run := []string{"run", "-d", "--init", "--name", name, "--hostname", b.team + "-app",
		"-p", fmt.Sprintf("127.0.0.1:%d:%d", hostPort, port)}
	run = append(run, engine.userArgs()...)
	// Its lookups go through the team's resolver, which logs them
	// (app_resolver.go).
	run = append(run, appBoxNetworkArgs(b.team, slot, engine)...)
	config, _ := loadTeam(b.team)
	run = append(run, boxHardening(appBoxMemory(config, appBoxApp))...)
	run = append(run, "-v", src+":/app", "-w", "/app")
	run = append(run, appBoxDataMount(b.team, slot, data)...)
	run = append(run, "-v", address+":"+filepath.Dir(appBoxAddressFile)+":ro",
		"-v", models+":"+appBoxModelsDir,
		"-v", jobs+":"+appBoxJobsDir,
		"-v", workers+":"+appBoxWorkersDir,
		"-v", files+":"+appBoxFilesDir+":ro",
		"-v", b.hi+":"+appBoxRelay+":ro",
		"--env-file", envFile,
		image, appBoxRelay, "app", "__app", "--workers", appBoxWorkersDir, "--box", name,
		appBoxModelsDir+"/"+appModelsSocket, strconv.Itoa(appBoxModelsPort), command)
	name, args := appOutsideService(engine.bin, run)
	if out, err := exec.Command(name, args...).CombinedOutput(); err != nil {
		return fmt.Errorf("%s: %s", engine.name, qFirstLine(strings.TrimSpace(string(out)), err.Error()))
	}
	return nil
}

// appOutsideService runs a command in a systemd scope of its own when hi
// team runs under systemd, so the containers and processes it leaves
// behind aren't stopped with the team's service.
func appOutsideService(name string, args []string) (string, []string) {
	if os.Getenv("INVOCATION_ID") == "" {
		return name, args
	}
	if _, err := exec.LookPath("systemd-run"); err != nil {
		return name, args
	}
	return "systemd-run", append([]string{"--user", "--scope", "--quiet", "--collect", "--", name}, args...)
}

func (b *appBroker) engineOutput(args ...string) (string, error) {
	engine, _, err := detectBoxEngine()
	if err != nil {
		return "", err
	}
	return engine.output(args...)
}

// lastLines is the last n lines a box printed, on stdout or stderr.
func (b *appBroker) lastLines(container string, n int) string {
	engine, _, err := detectBoxEngine()
	if err != nil {
		return ""
	}
	return engine.lastLines(container, n)
}

type nopWriter struct{}

func (nopWriter) Write(p []byte) (int, error) { return len(p), nil }

func appFreePort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port, nil
}

var errTeamAppStopped = errors.New("the app stopped")

// appWaitApp waits for an app to answer at url, for as long as limit
// while running says it still runs.
func appWaitApp(url string, running func() bool, limit time.Duration) error {
	client := &http.Client{Timeout: 3 * time.Second}
	deadline := time.Now().Add(limit)
	for checks := 0; ; checks++ {
		response, err := client.Get(url)
		if err == nil {
			response.Body.Close()
			return nil
		}
		if time.Now().After(deadline) {
			return err
		}
		// Asking the engine every 10 seconds is enough.
		if checks%5 == 4 && !running() {
			return errTeamAppStopped
		}
		time.Sleep(2 * time.Second)
	}
}

func appShell(dir, script string) (string, error) {
	var out strings.Builder
	err := boxCommand(nil, &out, &out, "sh", "-c", "cd "+shellQuote(dir)+" && "+script)
	return strings.TrimSpace(out.String()), err
}

// writeFileAtomic writes path whole or not at all, so a reader never sees
// half of it, through a temporary file of its own, so two writers of one
// file never write into each other's. It is for hi's own folders; a folder
// a box writes in takes boxedWriteFile.
func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	file, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmp := file.Name()
	_, err = file.Write(data)
	if err == nil {
		err = file.Chmod(mode)
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		os.Remove(tmp)
	}
	return err
}
