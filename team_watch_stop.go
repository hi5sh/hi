package main

// hi watch, release 3, on the team's machine: what the hi server asks in
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
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// teamLeadFrozenHosts are what the lead may still reach while the team is
// frozen: Slack, to say what happened. Its model calls go through the
// proxy's injector, which no allowlist governs.
var teamLeadFrozenHosts = []string{"slack.com"}

func teamWatchStopPath(team string) string {
	return filepath.Join(teamDir(team), "run", "watch-stop.json")
}

// teamWatchStopState is run/watch-stop.json: how the team is stopped, and
// what to put back.
type teamWatchStopState struct {
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

func loadTeamWatchStop(team string) teamWatchStopState {
	var state teamWatchStopState
	if data, err := os.ReadFile(teamWatchStopPath(team)); err == nil {
		json.Unmarshal(data, &state)
	}
	return state
}

func saveTeamWatchStop(team string, state teamWatchStopState) error {
	os.MkdirAll(filepath.Dir(teamWatchStopPath(team)), 0o700)
	data, _ := json.MarshalIndent(state, "", "  ")
	return writeFileAtomic(teamWatchStopPath(team), append(data, '\n'), 0o600)
}

// errStopped says why the broker won't start work or a new version.
func (b *teamBroker) errStopped() error {
	state := loadTeamWatchStop(b.team)
	if !state.Frozen && !state.Paused {
		return nil
	}
	what := "frozen"
	if state.Paused {
		what = "paused"
	}
	return fmt.Errorf("the team is %s (%s: %s), so nothing new starts; someone at the machine undoes it (hi team un%s %s). Tell the people in a line", what, state.By, firstNonEmpty(state.Because, "no reason given"), strings.TrimSuffix(what, "d"), b.team)
}

// stopped says the team is frozen or paused: hi's own checks then don't
// take the app's paused box for one that broke.
func (b *teamBroker) stopped() bool {
	state := loadTeamWatchStop(b.team)
	return state.Frozen || state.Paused
}

// teamWatchBoxes are the team's member boxes on this machine.
func teamWatchBoxes(team string) []string {
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

// teamWatchEngine runs the engine; tests replace it.
var teamWatchEngine = func(args ...string) (string, error) { return (&teamBroker{}).engineOutput(args...) }

// teamRunningContainers are the running containers whose names start with
// one of the prefixes.
func teamRunningContainers(prefixes ...string) []string {
	out, err := teamWatchEngine("ps", "--format", "{{.Names}}")
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
func (b *teamBroker) carryOut(do apiWatchDo) error {
	state := loadTeamWatchStop(b.team)
	if do.ID == "" || do.ID == state.Done {
		return nil
	}
	var text string
	switch do.Action {
	case "freeze":
		if !state.Frozen {
			state.Allow = map[string]string{}
			lead := filepath.Join(teamDir(b.team), "run", "proxy", "allow")
			files := map[string]string{lead: strings.Join(teamLeadFrozenHosts, "\n") + "\n"}
			for _, box := range teamWatchBoxes(b.team) {
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
			state.Apps = teamRunningContainers(teamPreviewContainer(b.team) + "-")
			for _, app := range state.Apps {
				teamWatchEngine("pause", app)
			}
			state.Frozen = true
		}
		text = ":ice_cube: *The team's hi server froze the team*"
	case "unfreeze":
		if state.Frozen {
			for path, data := range state.Allow {
				if fileExists(path) {
					writeFileAtomic(path, []byte(data), 0o600)
				}
			}
			if !state.Paused {
				for _, app := range state.Apps {
					teamWatchEngine("unpause", app)
				}
			}
			state.Frozen, state.Allow, state.Apps = false, nil, nil
		}
		text = ":sunny: *The team's hi server unfroze the team*"
	case "pause":
		if !state.Paused {
			running := teamRunningContainers("hi-box-team-"+b.team, "hi-box-"+b.team+"-t-")
			for _, name := range running {
				teamWatchEngine("pause", name)
			}
			state.Boxes, state.Paused = running, true
		}
		text = ":pause_button: *The team's hi server paused every box of the team*"
	case "unpause":
		if state.Paused {
			for _, name := range state.Boxes {
				teamWatchEngine("unpause", name)
			}
			if state.Frozen {
				for _, app := range state.Apps {
					teamWatchEngine("pause", app)
				}
			}
			state.Paused, state.Boxes = false, nil
		}
		text = ":arrow_forward: *The team's hi server unpaused the team*"
	default:
		return fmt.Errorf("the hi server asked for %q, which this hi doesn't know", do.Action)
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
		text += " Nothing runs: not the lead, not the app. Someone at the machine undoes it: hi team unpause " + b.team + "."
	case state.Frozen:
		text += " The boxes reach nothing but this channel, the app's pages are paused, and no work or version starts. Someone at the machine undoes it, after a look: hi team unfreeze " + b.team + "."
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

// teamUndeclared compares production's box with its bill: the packages
// in the app's folder (node_modules, a Python venv) and the OS packages in
// the box.
func teamUndeclared(team string) (*apiUndeclared, error) {
	state, ok := loadTeamSlot(team, teamSlotProduction)
	if !ok || state.App == "" {
		return nil, errors.New("production isn't running")
	}
	path, _, err := teamProductionBom(team, teamBomTools)
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
			declared["pypi "+teamPyName(component.Name)+"@"+component.Version] = true
		case component.property(bomPart) == "image:base" && strings.HasPrefix(component.Purl, "pkg:deb/"):
			declared["deb "+component.Name+"@"+component.Version] = true
		}
	}
	found := map[string]bool{}
	for _, pkg := range teamNodeModules(state.App) {
		found["npm "+pkg] = true
	}
	for _, pkg := range teamSitePackages(state.App) {
		found["pypi "+pkg] = true
	}
	if out, err := teamWatchEngine("exec", state.Container, "dpkg-query", "-W", "-f", "${Package}\t${Version}\n"); err == nil {
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

// teamNodeModules are the name@version of every package installed in a
// folder's node_modules, at any depth.
func teamNodeModules(dir string) []string {
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

// teamSitePackages are the name@version of a folder's Python venv.
func teamSitePackages(dir string) []string {
	var packages []string
	matches, _ := filepath.Glob(filepath.Join(dir, ".venv", "lib", "python*", "site-packages", "*.dist-info"))
	for _, match := range matches {
		base := strings.TrimSuffix(filepath.Base(match), ".dist-info")
		if at := strings.LastIndex(base, "-"); at > 0 {
			packages = append(packages, teamPyName(base[:at])+"@"+base[at+1:])
		}
	}
	return packages
}

// teamPyName is a Python package's name as PyPI compares them.
func teamPyName(name string) string {
	return strings.ToLower(strings.NewReplacer("_", "-", ".", "-").Replace(name))
}

// teamKeyFromServer is hi team key <name> openrouter --from-server: the
// team's hi server makes it a key of its own, which an admin can revoke
// there, even when this machine can't be trusted to.
// teamKeysFromServer is off from release 3 of hi_team_ops.md; its code goes
// in release 4.
var teamKeysFromServer = false

func teamKeyFromServer(name string, stdout io.Writer) error {
	if _, err := loadTeam(name); err != nil {
		return err
	}
	// From hi 2.6.32 a team watches itself, and the hi server makes no
	// team's key (hi_team_ops.md).
	if !teamKeysFromServer {
		return fmt.Errorf("the hi server no longer makes teams' keys; give the team its own: hi team key %s openrouter", name)
	}
	client, server := teamWatchClient()
	if client == nil {
		return errTeamNotWatched
	}
	var answer apiWatchKey
	if err := client.call(http.MethodPost, "/v1/watch/key", map[string]string{"team": name}, &answer); err != nil {
		return err
	}
	if err := teamUseServerKey(name, answer); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Team %s's model calls use a key %s made for it: an admin there can revoke it (hi server watch revoke %s).\n", name, server, name)
	return nil
}

// teamUseServerKey saves a key the server made as the team's own; the
// proxies read it again for each call, so nothing restarts.
func teamUseServerKey(name string, key apiWatchKey) error {
	if key.Key == "" {
		return errors.New("the hi server sent no key")
	}
	secrets, err := loadTeamSecrets(name)
	if err != nil {
		return err
	}
	secrets.OpenRouterAPIKey = key.Key
	if err := saveTeamSecrets(name, secrets); err != nil {
		return err
	}
	if err := writeTeamKeys(name, secrets); err != nil {
		return err
	}
	teamWatchStateMu.Lock()
	defer teamWatchStateMu.Unlock()
	state := loadTeamWatchState(name)
	state.KeyHash = key.Hash
	return saveTeamWatchState(name, state)
}
