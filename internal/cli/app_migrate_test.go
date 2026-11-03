package cli

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestAppMigrateMovesAnAppFromBefore267(t *testing.T) {
	dir := setupTeamTest(t)
	parent := filepath.Join(dir, "projects")
	folder := filepath.Join(parent, "team-payments")
	data, _ := json.Marshal(testTeamConfig())
	writeSkillTestFile(t, filepath.Join(folder, legacyAppConfigFile), string(data), 0o600)
	if err := appRegister("payments", folder); err != nil {
		t.Fatal(err)
	}
	units := filepath.Dir(appUnitPath("payments"))
	for _, unit := range []string{"hi-team-payments.service", "hi-team-payments-snapshot.service", "hi-team-payments-snapshot.timer"} {
		writeSkillTestFile(t, filepath.Join(units, unit), "[Unit]\n", 0o644)
	}
	var calls []string
	savedSystemctl, savedBoxes := appSystemctl, appRemoveLegacyBoxes
	appSystemctl = func(args ...string) (string, error) { calls = append(calls, strings.Join(args, " ")); return "", nil }
	var removed []string
	appRemoveLegacyBoxes = func(name string) { removed = append(removed, name) }
	t.Cleanup(func() { appSystemctl, appRemoveLegacyBoxes = savedSystemctl, savedBoxes })

	// Any command that loads the app renames team.json first.
	if _, err := loadTeam("payments"); err != nil || !fileExists(filepath.Join(folder, appConfigFile)) || fileExists(filepath.Join(folder, legacyAppConfigFile)) {
		t.Fatal(err)
	}
	if !appLegacy("payments") {
		t.Fatal("the old service isn't seen")
	}
	if err := appMigrate("payments", io.Discard); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(calls, "; ")
	for _, want := range []string{"disable --now hi-team-payments.service", "disable --now hi-team-payments-snapshot.timer", "stop hi-team-payments-resolver", "daemon-reload", "stop hi-app-payments.service"} {
		if !strings.Contains(got, want) {
			t.Fatalf("no %q in %s", want, got)
		}
	}
	if strings.Join(removed, " ") != "payments" {
		t.Fatal(removed)
	}
	for _, unit := range []string{"hi-team-payments.service", "hi-team-payments-snapshot.service", "hi-team-payments-snapshot.timer"} {
		if fileExists(filepath.Join(units, unit)) {
			t.Fatalf("%s is still there", unit)
		}
	}
	next := filepath.Join(parent, "app-payments")
	if appDir("payments") != next || fileExists(folder) || !fileExists(filepath.Join(next, appConfigFile)) {
		t.Fatal(appDir("payments"))
	}
	if appLegacy("payments") {
		t.Fatal("still legacy")
	}
	// A second run has nothing to do.
	calls = nil
	if err := appMigrate("payments", io.Discard); err != nil || len(calls) != 0 {
		t.Fatal(err, calls)
	}
}

func TestAppMigrateKeepsATakenFolder(t *testing.T) {
	dir := setupTeamTest(t)
	folder := filepath.Join(dir, "projects", "team-payments")
	data, _ := json.Marshal(testTeamConfig())
	writeSkillTestFile(t, filepath.Join(folder, appConfigFile), string(data), 0o600)
	os.MkdirAll(filepath.Join(dir, "projects", "app-payments"), 0o700)
	appRegister("payments", folder)
	savedSystemctl := appSystemctl
	appSystemctl = func(args ...string) (string, error) { return "", nil }
	t.Cleanup(func() { appSystemctl = savedSystemctl })
	if err := appMigrate("payments", io.Discard); err != nil || appDir("payments") != folder {
		t.Fatal(err, appDir("payments"))
	}
}

func TestLegacyAppContainerLeavesOtherApps(t *testing.T) {
	others := map[string]bool{"pay-ops": true, "billing": true}
	for container, want := range map[string]bool{
		"hi-box-team-pay":                         true,
		"hi-box-team-pay-proxy":                   true,
		"hi-box-team-pay-dev-net":                 true,
		"hi-box-team-pay-app-production-4100":     true,
		"hi-box-team-pay-ops":                     false,
		"hi-box-team-pay-ops-proxy":               false,
		"hi-box-team-payroll":                     false,
		"hi-box-app-pay":                          false,
		"hi-box-pay-t-1":                          false,
		"hi-box-team-billing":                     false,
		"hi-box-team-pay-ops-app-production-4100": false,
	} {
		if got := legacyAppContainer(container, "pay", others); got != want {
			t.Errorf("%s: %v, want %v", container, got, want)
		}
	}
}

func TestAppsDirMovesTheOldFolders(t *testing.T) {
	data, config := t.TempDir(), t.TempDir()
	t.Setenv("XDG_DATA_HOME", data)
	t.Setenv("XDG_CONFIG_HOME", config)
	t.Setenv("HI_APPS_DIR", "")
	os.Unsetenv("HI_APPS_DIR")
	writeSkillTestFile(t, filepath.Join(data, "hi", "teams", "settings.json"), "{}", 0o600)
	writeSkillTestFile(t, filepath.Join(data, "hi", "team-snapshots", "payments", "key"), "k", 0o600)
	saved := appMigrateMachine
	t.Cleanup(func() { appMigrateMachine = saved })

	// While an app still runs under its old names, its old hi reads the
	// old folders, so they stay.
	legacy := filepath.Join(config, "systemd", "user", "hi-team-billing.service")
	writeSkillTestFile(t, legacy, "[Unit]\n", 0o644)
	appMigrateMachine = &sync.Once{}
	if got := appsDir(); got != filepath.Join(data, "hi", "teams") {
		t.Fatal(got)
	}
	if got := appSnapshotsDir("payments"); got != filepath.Join(data, "hi", "team-snapshots", "payments") {
		t.Fatal(got)
	}

	os.Remove(legacy)
	appMigrateMachine = &sync.Once{}
	if got := appsDir(); got != filepath.Join(data, "hi", "apps") {
		t.Fatal(got)
	}
	if !fileExists(filepath.Join(data, "hi", "apps", "settings.json")) || fileExists(filepath.Join(data, "hi", "teams")) {
		t.Fatal("the links by name didn't move")
	}
	if !fileExists(filepath.Join(data, "hi", "app-snapshots", "payments", "key")) || fileExists(filepath.Join(data, "hi", "team-snapshots")) {
		t.Fatal("the snapshots didn't move")
	}
}

func TestLegacyAppExposeLeavesOtherApps(t *testing.T) {
	others := map[string]bool{"cartman-dev": true}
	for expose, want := range map[string]bool{
		"team-cartman-UbUwtw":          true,
		"team-cartman-live-pxT1SU":     true,
		"team-cartman-dev-6BWWIk":      false, // cartman-dev's Stage
		"team-cartman-dev-live-pxT1SU": false,
		"app-cartman-UbUwtw":           false,
		"team-cartmanx-UbUwtw":         false,
		"team-cartman":                 false,
	} {
		if got := legacyAppExpose(expose, "cartman", others); got != want {
			t.Errorf("%s: %v, want %v", expose, got, want)
		}
	}
	if !legacyAppExpose("team-cartman-dev-6BWWIk", "cartman", nil) {
		t.Error("cartman's dev page, with no cartman-dev app")
	}
}
