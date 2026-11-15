package cli

// An app's settings in its boxes (docs/specs/ideas/hi_server_vault.md,
// release 0). They never go to the engine: podman keeps a container's
// environment in its config.json, on disk, and shows it in podman
// inspect. Instead each slot's settings are files in a folder on tmpfs
// (the user's runtime folder, in memory), which its box mounts read-only:
//
//	/run/hi/settings/<NAME>   one file per name, its value
//	/run/hi/settings.env      all of them, NAME='value' a line
//	/hi-files/<NAME>          a file setting, such as a certificate
//
// The app's first process (hi app __app) gives the app its settings as
// environment variables when it starts, and hi-env <command> in the box
// runs a command with them as they are now. A change rewrites the files,
// each in one step, within a minute; a running process's environment
// can't be changed from outside, so it keeps what it started with unless
// the app asks for a signal or a restart (on_settings_change in app.json).

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

const (
	// appBoxSettingsMount is the slot's settings folder in its box.
	appBoxSettingsMount = "/run/hi"
	appBoxSettingsDir   = appBoxSettingsMount + "/settings"
	appBoxSettingsFile  = appBoxSettingsMount + "/settings.env"
	// appBoxHiEnv is hi in a box as hi-env, on its PATH.
	appBoxHiEnv = "/usr/local/bin/hi-env"
	// The box's environment says where they are.
	appSettingsDirEnv  = "HI_SETTINGS_DIR"
	appSettingsFileEnv = "HI_SETTINGS_FILE"
	// appSettingsStampFile, in the folder, is what it holds, hashed.
	appSettingsStampFile = ".stamp"
)

// appSettingsSignals are the signals an app may ask for on a change.
var appSettingsSignals = map[string]syscall.Signal{"SIGHUP": syscall.SIGHUP, "SIGUSR1": syscall.SIGUSR1, "SIGUSR2": syscall.SIGUSR2}

// appSettingsRuntimeRoot is the user's runtime folder, on tmpfs, or "" when
// there is none.
func appSettingsRuntimeRoot() string {
	if dir := os.Getenv("XDG_RUNTIME_DIR"); dir != "" && isDirectory(dir) {
		return dir
	}
	if dir := fmt.Sprintf("/run/user/%d", os.Getuid()); isDirectory(dir) {
		return dir
	}
	return ""
}

func hasKey[V any](m map[string]V, key string) bool {
	_, ok := m[key]
	return ok
}

// appSlotSettingsDir is a slot's settings folder: in memory, or, on a
// machine without a runtime folder, in the app's run folder as before.
func appSlotSettingsDir(team, slot string) string {
	if root := appSettingsRuntimeRoot(); root != "" {
		return filepath.Join(root, "hi", "app-settings", team, slot)
	}
	return filepath.Join(appDir(team), "run", slot+"-settings")
}

// appSlotSettings are what a slot's box gets: the values by name, with
// each file setting's path in its box, and the files' contents.
func appSlotSettings(team, slot string) (map[string]string, map[string][]byte, error) {
	values, files := map[string]string{}, map[string][]byte{}
	secrets, err := loadTeamSecrets(team)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, nil, err
	}
	config, _ := loadTeam(team)
	shared := func(scope string) map[string]string {
		got, _ := appSharedScope(config, scope)
		return got
	}
	// A team store's values come first: the app's own beat them, since a
	// name both give was refused unless it overrides (vault_connect.go).
	for _, scope := range map[bool][]string{true: {appSlotDev}, false: {"", slot}}[slot == appSlotDev] {
		team, _, err := appTeamScope(config, scope)
		if err != nil {
			return nil, nil, err
		}
		for key, value := range team {
			values[key] = value
		}
	}
	if slot == appSlotDev {
		for _, set := range []map[string]string{secrets.EnvDev, shared(appSlotDev)} {
			for key, value := range set {
				values[key] = value
			}
		}
	} else {
		own := secrets.EnvPreview
		if slot == appSlotProduction {
			own = secrets.EnvProduction
		}
		// A slot's own beats the app's; a shared key's beats a value of
		// its scope, which hi app env --from removed anyway.
		for _, set := range []map[string]string{secrets.Env, shared(""), own, shared(slot)} {
			for key, value := range set {
				values[key] = value
			}
		}
	}
	for key, value := range appBoxFiles(secrets, slot) {
		data, err := base64.StdEncoding.DecodeString(value)
		if err != nil {
			return nil, nil, fmt.Errorf("the file %s: %w", key, err)
		}
		files[key], values[key] = data, appBoxFilesDir+"/"+key
	}
	// hi's own variables come first, as they did when the engine set both.
	own := appBoxOwnEnv(team, slot, 0)
	if slot == appSlotDev {
		own = appDevEnv(team, "")
	}
	for key := range values {
		if _, ok := own[key]; ok || appBoxEnvReserved[key] {
			delete(values, key)
			delete(files, key)
		}
	}
	return values, files, nil
}

// appSettingsStampOf hashes what a slot's folder holds.
func appSettingsStampOf(values map[string]string, files map[string][]byte) string {
	hash := sha256.New()
	for _, key := range sortedKeys(values) {
		fmt.Fprintf(hash, "%s=%q\n", key, values[key])
	}
	for _, key := range sortedKeys(files) {
		fmt.Fprintf(hash, "file:%s=%x\n", key, sha256.Sum256(files[key]))
	}
	return hex.EncodeToString(hash.Sum(nil))[:16]
}

// writeAppSlotSettings writes a slot's settings folder, and says whether
// anything in it changed. Each file is replaced in one step, so a box that
// reads one never reads half of it; names it no longer gets go.
func writeAppSlotSettings(team, slot string) (string, bool, error) {
	dir := appSlotSettingsDir(team, slot)
	values, files, err := appSlotSettings(team, slot)
	if err != nil {
		return dir, false, err
	}
	stamp := appSettingsStampOf(values, files)
	if old, err := os.ReadFile(filepath.Join(dir, appSettingsStampFile)); err == nil && string(old) == stamp &&
		isDirectory(filepath.Join(dir, "settings")) && isDirectory(filepath.Join(dir, "files")) {
		return dir, false, nil
	}
	for _, sub := range []string{dir, filepath.Join(dir, "settings"), filepath.Join(dir, "files")} {
		if err := os.MkdirAll(sub, 0o700); err != nil {
			return dir, false, err
		}
	}
	write := func(sub string, wanted map[string][]byte) error {
		for key, data := range wanted {
			if err := writeFileAtomic(filepath.Join(dir, sub, key), data, 0o400); err != nil {
				return err
			}
		}
		entries, _ := os.ReadDir(filepath.Join(dir, sub))
		for _, entry := range entries {
			if _, ok := wanted[entry.Name()]; !ok {
				os.Remove(filepath.Join(dir, sub, entry.Name()))
			}
		}
		return nil
	}
	settings := map[string][]byte{}
	for key, value := range values {
		settings[key] = []byte(value)
	}
	if err := write("settings", settings); err != nil {
		return dir, false, err
	}
	if err := write("files", files); err != nil {
		return dir, false, err
	}
	if err := writeFileAtomic(filepath.Join(dir, "settings.env"), []byte(appSettingsEnvText(values)), 0o400); err != nil {
		return dir, false, err
	}
	if err := writeFileAtomic(filepath.Join(dir, appSettingsStampFile), []byte(stamp), 0o600); err != nil {
		return dir, false, err
	}
	return dir, true, nil
}

// appSettingsEnvText is settings.env: NAME='value' a line, which a shell
// reads with set -a; . /run/hi/settings.env, and most .env readers too.
func appSettingsEnvText(values map[string]string) string {
	var text strings.Builder
	for _, key := range sortedKeys(values) {
		text.WriteString(key + "=" + shellQuote(values[key]) + "\n")
	}
	return text.String()
}

// removeAppSettings removes an app's settings folders.
func removeAppSettings(team string) {
	if root := appSettingsRuntimeRoot(); root != "" {
		os.RemoveAll(filepath.Join(root, "hi", "app-settings", team))
	}
}

// ---------------------------------------------------------------------------
// in the box

// boxSettings reads the settings folder in a box: what the app's
// environment gets. Names that aren't variables, and hi's own, are left
// out, as hi app env never sets them.
func boxSettings(dir string) map[string]string {
	settings := map[string]string{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return settings
	}
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") || !entry.Type().IsRegular() || !appEnvName.MatchString(name) || appBoxEnvReserved[name] {
			continue
		}
		if data, err := os.ReadFile(filepath.Join(dir, name)); err == nil {
			settings[name] = string(data)
		}
	}
	return settings
}

// boxEnvWithSettings is env with the box's settings on top.
func boxEnvWithSettings(env []string, dir string) []string {
	settings := boxSettings(dir)
	out := make([]string, 0, len(env)+len(settings))
	for _, entry := range env {
		if name, _, _ := strings.Cut(entry, "="); !hasKey(settings, name) {
			out = append(out, entry)
		}
	}
	for _, key := range sortedKeys(settings) {
		out = append(out, key+"="+settings[key])
	}
	return out
}

// boxSettingsDirNow is the settings folder this box has.
func boxSettingsDirNow() string {
	return firstNonEmpty(os.Getenv(appSettingsDirEnv), appBoxSettingsDir)
}

// runHiEnv is hi-env <command> [args]: the command with the box's
// settings as they are now, in place of this process.
func runHiEnv(args []string, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprintln(stderr, "usage: hi-env <command> [args]: runs the command with this box's settings as they are now ("+appBoxSettingsDir+")")
		if len(args) == 0 {
			return 2
		}
		return 0
	}
	path, err := exec.LookPath(args[0])
	if err != nil {
		fmt.Fprintf(stderr, "hi-env: %v\n", err)
		return 127
	}
	err = syscall.Exec(path, args, boxEnvWithSettings(os.Environ(), boxSettingsDirNow()))
	fmt.Fprintf(stderr, "hi-env: %v\n", err)
	return 126
}

// boxSignalTargets are the processes a settings signal goes to: the
// app's command, or, when that is a shell running a line, its children,
// since a shell ends on SIGHUP and the app with it.
func boxSignalTargets(pid int) []int {
	comm, err := os.ReadFile(fmt.Sprintf("/proc/%d/comm", pid))
	if err != nil {
		return []int{pid}
	}
	switch strings.TrimSpace(string(comm)) {
	case "sh", "dash", "bash", "ash":
	default:
		return []int{pid}
	}
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/task/%d/children", pid, pid))
	var children []int
	for _, field := range strings.Fields(string(data)) {
		if child, err := strconv.Atoi(field); err == nil {
			children = append(children, child)
		}
	}
	if err != nil || len(children) == 0 {
		return []int{pid}
	}
	return children
}

// ---------------------------------------------------------------------------
// on_settings_change

// appOnSettingsChange reads app.json's on_settings_change: "" (the files
// change, nothing else), "restart", or "signal SIGHUP" (SIGUSR1,
// SIGUSR2), also given as the signal's name alone.
func appOnSettingsChange(text string) (kind, signal string, err error) {
	text = strings.TrimSpace(text)
	switch strings.ToLower(text) {
	case "", "files", "none":
		return "", "", nil
	case "restart":
		return "restart", "", nil
	}
	name := strings.ToUpper(strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(text, "signal"), "SIGNAL")))
	if !strings.HasPrefix(name, "SIG") {
		name = "SIG" + name
	}
	if _, ok := appSettingsSignals[name]; ok {
		return "signal", name, nil
	}
	return "", "", fmt.Errorf("on_settings_change is files, restart, or a signal (SIGHUP, SIGUSR1, SIGUSR2), not %q", text)
}

// appSettingsRestart says a change of settings starts the app's boxes
// again, as it asked.
func appSettingsRestart(config appConfig) bool {
	kind, _, _ := appOnSettingsChange(config.OnSettingsChange)
	return kind == "restart"
}

// syncSettings brings each running box's settings folder up to date, and
// signals the app when it asked to be. An app that asked for a restart
// gets new boxes instead (appSettingsStamp).
func (b *appBroker) syncSettings(stderr io.Writer) {
	config, err := loadTeam(b.team)
	if err != nil {
		return
	}
	kind, signal, _ := appOnSettingsChange(config.OnSettingsChange)
	// A shared OpenRouter key's new value reaches the proxy as settings do.
	if config.OpenRouterFrom != "" {
		if secrets, err := loadTeamSecrets(b.team); err == nil {
			if err := writeTeamKeys(b.team, secrets); err != nil {
				fmt.Fprintf(stderr, "hi: the app's OpenRouter key: %v\n", err)
			}
		}
	}
	for _, slot := range []string{appSlotPreview, appSlotProduction} {
		state, ok := loadTeamSlot(b.team, slot)
		if !ok || state.Container == "" || b.running == nil || !b.running(state.Container) {
			continue
		}
		b.forgetOldFiles(slot, state.Container)
		if kind == "restart" {
			continue // new boxes, with them (appSettingsStamp)
		}
		if _, changed, err := writeAppSlotSettings(b.team, slot); err != nil {
			fmt.Fprintf(stderr, "hi: %s's settings: %v\n", slot, err)
		} else if changed && kind == "signal" && b.signalBox != nil {
			if err := b.signalBox(state.Container, signal); err != nil {
				fmt.Fprintf(stderr, "hi: %s for %s's new settings: %v\n", signal, slot, err)
			}
		}
	}
	if dev := appDevContainer(b.team); b.running != nil && b.running(dev) {
		if _, _, err := writeAppSlotSettings(b.team, appSlotDev); err != nil {
			fmt.Fprintf(stderr, "hi: the dev box's settings: %v\n", err)
		}
		b.forgetOldFiles(appSlotDev, dev)
	}
}

// appBoxMounts are the folders a box mounts, a line each; tests replace it.
var appBoxMounts = func(b *appBroker, container string) (string, error) {
	return b.engineOutput("container", "inspect", "-f", "{{range .Mounts}}{{.Source}}\n{{end}}", container)
}

// forgetOldFiles removes a slot's file settings from the app's folder on
// disk, where hi kept them before they were in memory, once the slot's box
// no longer mounts them.
func (b *appBroker) forgetOldFiles(slot, container string) {
	old := appSlotFilesDir(b.team, slot)
	if !isDirectory(old) || appSlotSettingsDir(b.team, slot) == filepath.Join(appDir(b.team), "run", slot+"-settings") {
		return
	}
	mounts, err := appBoxMounts(b, container)
	if err != nil {
		return
	}
	for _, line := range strings.Split(mounts, "\n") {
		if filepath.Clean(strings.TrimSpace(line)) == old {
			return
		}
	}
	os.RemoveAll(old)
}
