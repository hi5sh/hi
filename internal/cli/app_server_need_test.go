package cli

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Other tests run on a machine with a server; these turn the check back on.
// And no test sees the real server of the machine it runs on: its state is
// under XDG_STATE_HOME, here an empty folder unless a test sets its own.
func init() {
	appServerHere = func() bool { return true }
	state, err := os.MkdirTemp("", "hi-test-state-")
	if err != nil {
		panic(err)
	}
	os.Setenv("XDG_STATE_HOME", state)
	os.Unsetenv("HI_ORG_DIR")
	os.Unsetenv("HI_SERVER_DIR")
	// Nor its services: a test that needs systemctl says what it answers.
	appSystemctl = func(args ...string) (string, error) {
		return "", errors.New("tests don't run systemctl")
	}
}

func realServerCheck(t *testing.T) {
	previous := appServerHere
	t.Cleanup(func() { appServerHere = previous })
	appServerHere = func() bool {
		dir, err := orgDirectory("")
		return err == nil && fileExists(filepath.Join(dir, "config.json"))
	}
}

// A machine has a server when it runs one; joined to another as a device
// isn't enough, and says to leave it first.
func TestAppNeedsServer(t *testing.T) {
	realServerCheck(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, ".local", "state"))
	t.Setenv("HI_ORG_DIR", "")
	t.Setenv("HI_SERVER_DIR", "")
	err := appNeedsServer("hi app new")
	if err == nil || !strings.Contains(err.Error(), "hi app new needs a hi server on this machine") ||
		!strings.Contains(err.Error(), "hi server init --owner") || strings.Contains(err.Error(), "leave") {
		t.Fatalf("no server: %v", err)
	}
	if code, _, stderr := runHi("app", "new", "payments"); code == 0 || !strings.Contains(stderr, "hi app new needs a hi server") {
		t.Errorf("hi app new: %d %q", code, stderr)
	}
	// Joined to another as a device: leave it first.
	if err := saveServerConnection(serverConnection{URL: "http://hi.corp.example:7373", User: "ana"}); err != nil {
		t.Fatal(err)
	}
	if err := appNeedsServer("hi app new"); err == nil || !strings.Contains(err.Error(), "joined to http://hi.corp.example:7373 as a device") ||
		!strings.Contains(err.Error(), "hi server leave") {
		t.Fatalf("joined: %v", err)
	}
	// Running one here is enough.
	dir := filepath.Join(home, "org")
	os.MkdirAll(dir, 0o700)
	os.WriteFile(filepath.Join(dir, "config.json"), []byte("{}"), 0o600)
	t.Setenv("HI_ORG_DIR", dir)
	if err := appNeedsServer("hi app new"); err != nil {
		t.Fatalf("runs one: %v", err)
	}
}

// hi update on a machine with apps and no server changes nothing.
func TestUpdateWaitsForAServer(t *testing.T) {
	requireLinux(t)
	realServerCheck(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, ".local", "state"))
	t.Setenv("HI_ORG_DIR", filepath.Join(home, "none"))
	fakeReleases(t, "v0.9.0", false)
	path := installedBinary(t, "v0.7.0")
	os.MkdirAll(filepath.Join(appsDir(), "payments"), 0o700)
	if !machineHasApps() {
		t.Skip("appsDir isn't under HOME here")
	}
	code, stdout, stderr := runUpdateTest()
	if code == 0 || strings.Contains(stdout, "Updating") || !strings.Contains(stderr, "hi update waits until it has one") {
		t.Fatalf("%d %q %q", code, stdout, stderr)
	}
	if got := readTestFile(t, path); got != fakeBinary("v0.7.0") {
		t.Fatalf("the binary changed:\n%s", got)
	}
	if code, stdout, stderr := runUpdateTest("--check"); code != 0 || !strings.Contains(stdout, "v0.9.0 is available") {
		t.Fatalf("--check: %d %q %q", code, stdout, stderr)
	}
}

// New app doesn't start on a machine with no server.
func TestAdminNewNeedsServer(t *testing.T) {
	a := setupAdminTest(t)
	previous := appServerHere
	appServerHere = func() bool { return false }
	t.Cleanup(func() { appServerHere = previous })
	olga := a.signIn(t, "Olga")
	var failed map[string]any
	if code := a.post(t, olga, "/api/new", map[string]any{"action": "start", "name": "orders"}, &failed); code != http.StatusBadRequest ||
		!strings.Contains(failed["error"].(string), "A new app needs a hi server") {
		t.Fatalf("%d %v", code, failed)
	}
}

// hi admin is gone: what a person types runs nothing and says where it
// went; what older units and jobs run still works.
func TestAdminIsGone(t *testing.T) {
	for _, args := range [][]string{{"admin"}, {"admin", "start"}, {"admin", "link"}, {"admin", "admins"}} {
		code, stdout, stderr := runHi(args...)
		if code != 2 || stdout != "" || !strings.Contains(stderr, "hi admin is gone: the admin page is the server's (hi server status") {
			t.Errorf("hi %s: %d %q %q", strings.Join(args, " "), code, stdout, stderr)
		}
	}
	if _, _, stderr := runHi("admin", "__askpass"); !strings.Contains(stderr, "usage: hi server __askpass") {
		t.Errorf("__askpass: %q", stderr)
	}
	if _, stdout, _ := runHi("help"); strings.Contains(stdout, "hi admin") {
		t.Errorf("help names hi admin:\n%s", stdout)
	}
}

// A unit written before release 3 runs hi server __page from its next start.
func TestAdminUpgradeUnit(t *testing.T) {
	setupAdminTest(t)
	os.MkdirAll(filepath.Dir(adminUnitPath()), 0o755)
	writeTestFile(t, adminUnitPath(), "[Service]\nEnvironment=PATH=/usr/bin\nExecStart=/home/ana/.local/bin/hi admin serve\nRestart=always\n", 0o644)
	var log strings.Builder
	adminUpgradeUnit(&log)
	if got := readTestFile(t, adminUnitPath()); !strings.Contains(got, "ExecStart=/home/ana/.local/bin/hi server __page\n") || strings.Contains(got, "admin serve") || !strings.Contains(got, "Environment=PATH=/usr/bin") {
		t.Fatalf("unit:\n%s", got)
	}
	if !strings.Contains(log.String(), "runs hi server __page from its next start") {
		t.Fatalf("log: %q", log.String())
	}
}
