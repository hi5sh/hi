package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// Until v2.6.67 an app was a team: its link by name was in
// ~/.local/share/hi/teams, its snapshots in team-snapshots, its settings in
// team.json, its folder ./team-<name>, its services hi-team-<name>*, its
// boxes team-<name> and team-<name>-dev, and its addresses team-<name>,
// team-<name>-live, and team-<name>-dev. The machine's links and snapshots
// move the first time hi looks for them; an app's own names move when
// hi app up starts it (appMigrate).

var appMigrateMachine = &sync.Once{}

// migrateAppsMachine moves the old links by name and the snapshots to their
// new folders, once, when the new ones don't exist yet, and gives the
// folder the links are in. While an app still runs under its old names its
// old hi reads the old folders, so they stay until hi app up has moved the
// last such app.
func migrateAppsMachine(apps string) string {
	old := filepath.Join(filepath.Dir(apps), "teams")
	if fileExists(apps) || !fileExists(old) {
		return apps
	}
	appMigrateMachine.Do(func() {
		if legacyAppsInstalled() {
			return
		}
		base := filepath.Dir(apps)
		for from, to := range map[string]string{"teams": "apps", "team-snapshots": "app-snapshots"} {
			if from, to := filepath.Join(base, from), filepath.Join(base, to); fileExists(from) && !fileExists(to) {
				os.Rename(from, to)
			}
		}
	})
	if fileExists(apps) {
		return apps
	}
	return old
}

// legacyAppsInstalled says whether any app still has its old service.
func legacyAppsInstalled() bool {
	matches, _ := filepath.Glob(filepath.Join(filepath.Dir(appUnitPath("x")), "hi-team-*.service"))
	for _, unit := range matches {
		if !strings.HasSuffix(unit, "-snapshot.service") {
			return true
		}
	}
	return false
}

// migrateAppConfig renames an old folder's team.json to app.json.
func migrateAppConfig(dir string) {
	from, to := filepath.Join(dir, legacyAppConfigFile), filepath.Join(dir, appConfigFile)
	if fileExists(from) && !fileExists(to) {
		os.Rename(from, to)
	}
}

func legacyAppUnit(name string) string     { return "hi-team-" + name + ".service" }
func legacyAppSnapshot(name string) string { return "hi-team-" + name + "-snapshot" }
func legacyAppResolver(name string) string { return "hi-team-" + name + "-resolver" }

// appLegacy says whether an app still runs under the names from before
// v2.6.67: its old service is installed.
func appLegacy(name string) bool {
	return fileExists(filepath.Join(filepath.Dir(appUnitPath(name)), legacyAppUnit(name)))
}

// appMigrate moves an app made before v2.6.67 to the new names: it stops
// the old services, boxes, and addresses (appStopLegacy) and moves
// ./team-<name> to ./app-<name>. hi app up then starts it under the new
// names; the addresses change, the links' keys stay, and the lead posts the
// new ones.
var appMigrate = func(name string, stdout io.Writer) error {
	if appLegacy(name) {
		fmt.Fprintf(stdout, "App %s still has the names from before v2.6.67; moving it to the new ones.\n", name)
		appStopLegacy(name)
	}
	folder := appDir(name)
	migrateAppWatchState(name, folder)
	if filepath.Base(folder) != "team-"+name {
		return nil
	}
	next := filepath.Join(filepath.Dir(folder), "app-"+name)
	if fileExists(next) {
		fmt.Fprintf(stdout, "Its folder stays %s: %s is taken.\n", folder, next)
		return nil
	}
	// Nothing may run from the folder while it moves.
	appSystemctl("stop", appUnitName(name))
	appStopDevBox(name)
	if err := os.Rename(folder, next); err != nil {
		return fmt.Errorf("moving %s to %s: %w", folder, next, err)
	}
	if err := appRegister(name, next); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Its folder is now %s.\n", next)
	return nil
}

// appStopLegacy stops an app's services, boxes, and addresses under the
// names from before v2.6.67, and removes the old services.
func appStopLegacy(name string) {
	appSystemctl("disable", "--now", legacyAppUnit(name))
	appSystemctl("disable", "--now", legacyAppSnapshot(name)+".timer")
	appSystemctl("stop", legacyAppResolver(name))
	appRemoveLegacyBoxes(name)
	units := filepath.Dir(appUnitPath(name))
	for _, unit := range []string{legacyAppUnit(name), legacyAppSnapshot(name) + ".service", legacyAppSnapshot(name) + ".timer"} {
		os.Remove(filepath.Join(units, unit))
	}
	appSystemctl("daemon-reload")
	appMigrateMachine = &sync.Once{} // the last one may let the machine's folders move
}

// appRemoveLegacyBoxes stops an app's boxes and addresses under their old
// names; tests replace it.
var appRemoveLegacyBoxes = func(name string) {
	others := map[string]bool{}
	if entries, err := os.ReadDir(appsDir()); err == nil {
		for _, entry := range entries {
			if entry.Name() != name {
				others[entry.Name()] = true
			}
		}
	}
	for expose := range netExposeStates() {
		if legacyAppExpose(expose, name, others) {
			netExposeStop([]string{expose}, io.Discard)
		}
	}
	engine, _, err := detectBoxEngine()
	if err != nil {
		return
	}
	if out, err := engine.output("ps", "-a", "--format", "{{.Names}}"); err == nil {
		for _, container := range strings.Fields(out) {
			if legacyAppContainer(container, name, others) {
				engine.output("rm", "-f", container)
			}
		}
	}
	for _, box := range []string{"team-" + name, "team-" + name + "-dev"} {
		teardownBox(engine, boxMeta{Name: box})
		os.RemoveAll(boxStateFile(box))
	}
}

// legacyAppContainer says whether a container is one of app name's under
// the old names: hi-box-team-<name> and what starts with it, unless it is
// another app's whose name starts with this one's (pay and pay-ops).
func legacyAppContainer(container, name string, others map[string]bool) bool {
	prefix := "hi-box-team-" + name
	if container != prefix && !strings.HasPrefix(container, prefix+"-") {
		return false
	}
	for other := range others {
		if len(other) > len(name) && strings.HasPrefix(other, name+"-") {
			if theirs := "hi-box-team-" + other; container == theirs || strings.HasPrefix(container, theirs+"-") {
				return false
			}
		}
	}
	return true
}

// legacyAppExpose says whether an exposure is one of app name's under the
// old names: team-<name>, team-<name>-live, or team-<name>-dev, each with
// the six characters hi net expose adds, unless another app's name
// accounts for it.
func legacyAppExpose(expose, name string, others map[string]bool) bool {
	if !legacyAppExposeName(expose, name) {
		return false
	}
	for other := range others {
		if len(other) > len(name) && strings.HasPrefix(other, name+"-") && legacyAppExposeName(expose, other) {
			return false
		}
	}
	return true
}

func legacyAppExposeName(expose, name string) bool {
	return regexp.MustCompile(`^team-` + regexp.QuoteMeta(name) + `(-live|-dev)?-[A-Za-z0-9_-]{6}$`).MatchString(expose)
}

// migrateAppWatchState gives the watcher's record of each box under its
// old name to the box's new name: how far it read the box's network log,
// and what it has seen there. Without it the watcher would read the dev
// box's whole log again and take every site an owner ever allowed for new.
func migrateAppWatchState(name, folder string) {
	renamed := func(key string) (string, bool) {
		old := "box:team-" + name
		i := strings.Index(key, old)
		if i < 0 {
			return "", false
		}
		rest := key[i+len(old):]
		if rest != "" && rest[0] != '-' && rest[0] != ':' {
			return "", false
		}
		return key[:i] + "box:app-" + name + rest, true
	}
	carry := func(values map[string]any) {
		for key, value := range values {
			if next, ok := renamed(key); ok {
				if _, there := values[next]; !there {
					values[next] = value
				}
				delete(values, key)
			}
		}
	}
	rewrite := func(path string, fields func(map[string]any) []map[string]any) {
		data, err := os.ReadFile(path)
		if err != nil {
			return
		}
		var state map[string]any
		if json.Unmarshal(data, &state) != nil {
			return
		}
		for _, values := range fields(state) {
			carry(values)
		}
		if data, err := json.MarshalIndent(state, "", "  "); err == nil {
			writeFileAtomic(path, append(data, '\n'), 0o600)
		}
	}
	field := func(state map[string]any, keys ...string) map[string]any {
		var current any = state
		for _, key := range keys {
			next, ok := current.(map[string]any)
			if !ok {
				return nil
			}
			current = next[key]
		}
		values, _ := current.(map[string]any)
		return values
	}
	rewrite(filepath.Join(folder, "watch", "state.json"), func(state map[string]any) []map[string]any {
		return []map[string]any{field(state, "teams", name, "logs_kept"), field(state, "teams", name, "net_seen")}
	})
	rewrite(filepath.Join(folder, "run", "watch.json"), func(state map[string]any) []map[string]any {
		return []map[string]any{field(state, "logs_kept")}
	})
}
