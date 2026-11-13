package cli

// hi watch, release 3, on the team's machine: what the org server asks in
// the answer to a heartbeat (freeze, unfreeze, pause, unpause), carried
// out once and confirmed in the next heartbeat; and the weekly look at
// what production's box has that its bill of materials doesn't.
//
// Freeze: every box's allowlist is emptied (the lead keeps Slack, to say
// what happened), the app's boxes are paused, and the broker starts no
// task and puts up no new version. Pause: every box of the team is
// paused. Both are undone exactly; the lead can't do either.
//
// Spec: docs/specs/approved/hi_watch.md (release 3).

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
)

// appLeadFrozenHosts are what the lead may still reach while the team is
// frozen: Slack, to say what happened. Its model calls go through the
// proxy's injector, which no allowlist governs.
var appLeadFrozenHosts = []string{"slack.com"}

func appWatchStopPath(team string) string {
	return filepath.Join(appDir(team), "run", "watch-stop.json")
}

// appWatchStopState is run/watch-stop.json: how the team is stopped, and
// what to put back.
type appWatchStopState struct {
	Frozen  bool              `json:"frozen,omitempty"`
	Paused  bool              `json:"paused,omitempty"`
	Done    string            `json:"done,omitempty"`
	By      string            `json:"by,omitempty"`
	Because string            `json:"because,omitempty"`
	At      time.Time         `json:"at,omitzero"`
	Allow   map[string]string `json:"allow,omitempty"` // allowlist file → what it said
	Apps    []string          `json:"apps,omitempty"`  // app boxes the freeze paused
	Boxes   []string          `json:"boxes,omitempty"` // boxes the pause paused
}

func loadTeamWatchStop(team string) appWatchStopState {
	var state appWatchStopState
	if data, err := os.ReadFile(appWatchStopPath(team)); err == nil {
		json.Unmarshal(data, &state)
	}
	return state
}

func saveTeamWatchStop(team string, state appWatchStopState) error {
	os.MkdirAll(filepath.Dir(appWatchStopPath(team)), 0o700)
	data, _ := json.MarshalIndent(state, "", "  ")
	return writeFileAtomic(appWatchStopPath(team), append(data, '\n'), 0o600)
}

// errStopped says why the broker won't start work or a new version.
func (b *appBroker) errStopped() error {
	state := loadTeamWatchStop(b.team)
	if !state.Frozen && !state.Paused {
		return nil
	}
	what := "frozen"
	if state.Paused {
		what = "paused"
	}
	return fmt.Errorf("the app is %s (%s: %s), so nothing new starts; someone at the machine undoes it (hi app un%s %s). Tell the people in a line", what, state.By, firstNonEmpty(state.Because, "no reason given"), strings.TrimSuffix(what, "d"), b.team)
}

// stopped says the team is frozen or paused: hi's own checks then don't
// take the app's paused box for one that broke.
func (b *appBroker) stopped() bool {
	state := loadTeamWatchStop(b.team)
	return state.Frozen || state.Paused
}

// appWatchBoxes are the team's member boxes on this machine.
func appWatchBoxes(team string) []string {
	seen := map[string]bool{}
	var boxes []string
	for _, task := range loadTeamTasks(team) {
		if task.Box != "" && !seen[task.Box] && fileExists(boxStateFile(task.Box)) {
			seen[task.Box] = true
			boxes = append(boxes, task.Box)
		}
	}
	return boxes
}

// appWatchEngine runs the engine; tests replace it.
var appWatchEngine = func(args ...string) (string, error) { return (&appBroker{}).engineOutput(args...) }

// appRunningContainers are the running containers whose names start with
// one of the prefixes.
func appRunningContainers(prefixes ...string) []string {
	out, err := appWatchEngine("ps", "--format", "{{.Names}}")
	if err != nil {
		return nil
	}
	var names []string
	for _, name := range strings.Fields(out) {
		for _, prefix := range prefixes {
			if strings.HasPrefix(name, prefix) {
				names = append(names, name)
				break
			}
		}
	}
	sort.Strings(names)
	return names
}

// carryOut does what the server asked, once.
func (b *appBroker) carryOut(do apiWatchDo) error {
	state := loadTeamWatchStop(b.team)
	if do.ID == "" || do.ID == state.Done {
		return nil
	}
	var text string
	switch do.Action {
	case "freeze":
		if !state.Frozen {
			state.Allow = map[string]string{}
			lead := filepath.Join(appDir(b.team), "run", "proxy", "allow")
			files := map[string]string{lead: strings.Join(appLeadFrozenHosts, "\n") + "\n"}
			for _, box := range appWatchBoxes(b.team) {
				files[boxStateFile(box, "allow")] = ""
			}
			for path, frozen := range files {
				data, err := os.ReadFile(path)
				if err != nil {
					continue
				}
				state.Allow[path] = string(data)
				if err := writeFileAtomic(path, []byte(frozen), 0o600); err != nil {
					return err
				}
			}
			state.Apps = appTeamContainers(b.team, appRunningContainers(appPreviewContainer(b.team)+"-"), true)
			for _, app := range state.Apps {
				appWatchEngine("pause", app)
			}
			state.Frozen = true
		}
		text = ":ice_cube: *The server froze the app*"
	case "unfreeze":
		if state.Frozen {
			for path, data := range state.Allow {
				if fileExists(path) {
					writeFileAtomic(path, []byte(data), 0o600)
				}
			}
			if !state.Paused {
				for _, app := range state.Apps {
					appWatchEngine("unpause", app)
				}
			}
			state.Frozen, state.Allow, state.Apps = false, nil, nil
		}
		text = ":sunny: *The server unfroze the app*"
	case "pause":
		if !state.Paused {
			running := appTeamContainers(b.team, appRunningContainers("hi-box-app-"+b.team, "hi-box-"+b.team+"-t-"), false)
			for _, name := range running {
				appWatchEngine("pause", name)
			}
			state.Boxes, state.Paused = running, true
		}
		text = ":pause_button: *The server paused every box of the app*"
	case "unpause":
		if state.Paused {
			for _, name := range state.Boxes {
				appWatchEngine("unpause", name)
			}
			if state.Frozen {
				for _, app := range state.Apps {
					appWatchEngine("pause", app)
				}
			}
			state.Paused, state.Boxes = false, nil
		}
		text = ":arrow_forward: *The server unpaused the app*"
	default:
		return fmt.Errorf("the server asked for %q, which this hi doesn't know", do.Action)
	}
	state.Done, state.By, state.Because, state.At = do.ID, do.By, do.Because, time.Now().UTC()
	if err := saveTeamWatchStop(b.team, state); err != nil {
		return err
	}
	text += fmt.Sprintf(" (%s", do.By)
	if do.Because != "" {
		text += ": " + do.Because
	}
	text += ")."
	switch {
	case state.Paused:
		text += " Nothing runs: not the lead, not the app. Someone at the machine undoes it: hi app unpause " + b.team + "."
	case state.Frozen:
		text += " The boxes reach nothing but this channel, the app's pages are paused, and no work or version starts. Someone at the machine undoes it, after a look: hi app unfreeze " + b.team + "."
	default:
		text += " Everything runs as before."
	}
	if b.channel != nil {
		if config, err := loadTeam(b.team); err == nil {
			b.channel.post(config.opsChannel(), text)
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// what production's box has that its bill doesn't

// appUndeclared compares production's box with its bill: the packages
// in the app's folder (node_modules, a Python venv) and the OS packages in
// the box.
func appUndeclared(team string) (*apiUndeclared, error) {
	state, ok := loadTeamSlot(team, appSlotProduction)
	if !ok || state.App == "" {
		return nil, errors.New("production isn't running")
	}
	path, _, err := appProductionBom(team, appBomTools)
	if err != nil {
		return nil, err
	}
	var bom cdxBom
	data, err := os.ReadFile(path)
	if err == nil {
		err = json.Unmarshal(data, &bom)
	}
	if err != nil {
		return nil, err
	}
	declared := map[string]bool{}
	for _, component := range bom.Components {
		switch {
		case component.property(bomOSVSystem) == "npm":
			declared["npm "+component.Name+"@"+component.Version] = true
		case component.property(bomOSVSystem) == "PyPI":
			declared["pypi "+appPyName(component.Name)+"@"+component.Version] = true
		case component.property(bomPart) == "image:base" && strings.HasPrefix(component.Purl, "pkg:deb/"):
			declared["deb "+component.Name+"@"+component.Version] = true
		}
	}
	found := map[string]bool{}
	for _, pkg := range appNodeModules(state.App) {
		found["npm "+pkg] = true
	}
	for _, pkg := range appSitePackages(state.App) {
		found["pypi "+pkg] = true
	}
	if out, err := appWatchEngine("exec", state.Container, "dpkg-query", "-W", "-f", "${Package}\t${Version}\n"); err == nil {
		for _, line := range strings.Split(out, "\n") {
			if name, version, ok := strings.Cut(strings.TrimSpace(line), "\t"); ok && name != "" {
				found["deb "+name+"@"+version] = true
			}
		}
	}
	undeclared := &apiUndeclared{Checked: time.Now().UTC()}
	for _, pkg := range sortedKeys(found) {
		if !declared[pkg] {
			undeclared.Count++
			if len(undeclared.Items) < 30 {
				undeclared.Items = append(undeclared.Items, pkg)
			}
		}
	}
	return undeclared, nil
}

// appNodeModules are the name@version of every package installed in a
// folder's node_modules, at any depth.
func appNodeModules(dir string) []string {
	var packages []string
	root := filepath.Join(dir, "node_modules")
	filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || entry.Name() != "package.json" {
			return nil
		}
		parent := filepath.Dir(path)
		up := filepath.Dir(parent)
		if filepath.Base(up) != "node_modules" && !(strings.HasPrefix(filepath.Base(up), "@") && filepath.Base(filepath.Dir(up)) == "node_modules") {
			return nil
		}
		var manifest struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		}
		if data, err := os.ReadFile(path); err == nil && json.Unmarshal(data, &manifest) == nil && manifest.Name != "" && manifest.Version != "" {
			packages = append(packages, manifest.Name+"@"+manifest.Version)
		}
		return nil
	})
	return packages
}

// appSitePackages are the name@version of a folder's Python venv.
func appSitePackages(dir string) []string {
	var packages []string
	matches, _ := filepath.Glob(filepath.Join(dir, ".venv", "lib", "python*", "site-packages", "*.dist-info"))
	for _, match := range matches {
		base := strings.TrimSuffix(filepath.Base(match), ".dist-info")
		if at := strings.LastIndex(base, "-"); at > 0 {
			packages = append(packages, appPyName(base[:at])+"@"+base[at+1:])
		}
	}
	return packages
}

// appPyName is a Python package's name as PyPI compares them.
func appPyName(name string) string {
	return strings.ToLower(strings.NewReplacer("_", "-", ".", "-").Replace(name))
}

// appSlotContainerPattern is an app box by its whole name: an app's
// containers are told by their whole names. A prefix isn't one: pausing pay paused
// pay-ops, whose names start with pay's, and pay-app's start like pay's
// production box.
var appSlotContainerPattern = regexp.MustCompile(`^hi-box-app-(.+)-app-(preview|production)-[0-9]+$`)

// appSlotContainer says a container is one of team's app boxes, the
// preview's or production's.
func appSlotContainer(team, name string) bool {
	match := appSlotContainerPattern.FindStringSubmatch(name)
	return match != nil && match[1] == team
}

// appTeamContainer says a container is team's: its lead, the lead's proxy
// and memory, its dev box with its proxy and peer, an app box, or a
// member's box (hi-box-<team>-t-…).
func appTeamContainer(team, name string) bool {
	if appSlotContainer(team, name) || strings.HasPrefix(name, "hi-box-"+team+"-t-") {
		return true
	}
	for _, suffix := range []string{"", "-proxy", "-memory", "-dev", "-dev-proxy", "-dev-net"} {
		if name == "hi-box-app-"+team+suffix {
			return true
		}
	}
	return false
}

// appTeamContainers keeps those of names that are team's.
func appTeamContainers(team string, names []string, slotsOnly bool) []string {
	var kept []string
	for _, name := range names {
		if slotsOnly && appSlotContainer(team, name) || !slotsOnly && appTeamContainer(team, name) {
			kept = append(kept, name)
		}
	}
	return kept
}

// holdWatchStop puts the org server's freeze or pause back on what the
// service's start made anew: the lead's box is a new one, and member
// boxes started since have their own allowlists.
func (b *appBroker) holdWatchStop(stderr io.Writer) {
	state := loadTeamWatchStop(b.team)
	if !state.Frozen && !state.Paused {
		return
	}
	if state.Frozen {
		if state.Allow == nil {
			state.Allow = map[string]string{}
		}
		for _, box := range appWatchBoxes(b.team) {
			path := boxStateFile(box, "allow")
			data, err := os.ReadFile(path)
			if err != nil || len(data) == 0 {
				continue
			}
			if _, kept := state.Allow[path]; !kept {
				state.Allow[path] = string(data)
			}
			writeFileAtomic(path, nil, 0o600)
		}
		for _, app := range appTeamContainers(b.team, appRunningContainers(appPreviewContainer(b.team)+"-"), true) {
			appWatchEngine("pause", app)
			if !slices.Contains(state.Apps, app) {
				state.Apps = append(state.Apps, app)
			}
		}
	}
	if state.Paused {
		for _, name := range appTeamContainers(b.team, appRunningContainers("hi-box-app-"+b.team, "hi-box-"+b.team+"-t-"), false) {
			appWatchEngine("pause", name)
			if !slices.Contains(state.Boxes, name) {
				state.Boxes = append(state.Boxes, name)
			}
		}
	}
	if err := saveTeamWatchStop(b.team, state); err != nil {
		fmt.Fprintf(stderr, "hi: the server's freeze: %v\n", err)
	}
}
