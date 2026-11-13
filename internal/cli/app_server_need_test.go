package cli

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Other tests run on a machine with a server; these turn the check back on.
func init() {
	appServerJoined = func() bool { return true }
}

func realServerCheck(t *testing.T) {
	previous := appServerJoined
	t.Cleanup(func() { appServerJoined = previous })
	appServerJoined = func() bool {
		if connection, err := loadServerConnection(); err == nil && connection != nil && connection.URL != "" {
			return true
		}
		dir, err := orgDirectory("")
		return err == nil && fileExists(filepath.Join(dir, "config.json"))
	}
}

// A machine has a server when it joined one or runs one, and not before.
func TestAppNeedsServer(t *testing.T) {
	realServerCheck(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, ".local", "state"))
	t.Setenv("HI_ORG_DIR", "")
	t.Setenv("HI_SERVER_DIR", "")
	err := appNeedsServer("hi app new")
	if err == nil || !strings.Contains(err.Error(), "hi app new needs a hi server") ||
		!strings.Contains(err.Error(), "hi server join <server>") || !strings.Contains(err.Error(), "hi server init") {
		t.Fatalf("no server: %v", err)
	}
	if code, _, stderr := runHi("app", "new", "payments"); code == 0 || !strings.Contains(stderr, "hi app new needs a hi server") {
		t.Errorf("hi app new: %d %q", code, stderr)
	}
	// Running one here is enough.
	dir := filepath.Join(home, "org")
	os.MkdirAll(dir, 0o700)
	os.WriteFile(filepath.Join(dir, "config.json"), []byte("{}"), 0o600)
	t.Setenv("HI_ORG_DIR", dir)
	if err := appNeedsServer("hi app new"); err != nil {
		t.Fatalf("runs one: %v", err)
	}
	// So is having joined one.
	t.Setenv("HI_ORG_DIR", filepath.Join(home, "none"))
	if err := saveServerConnection(serverConnection{URL: "http://hi.corp.example:7373", User: "ana"}); err != nil {
		t.Fatal(err)
	}
	if err := appNeedsServer("hi app new"); err != nil {
		t.Fatalf("joined one: %v", err)
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
	previous := appServerJoined
	appServerJoined = func() bool { return false }
	t.Cleanup(func() { appServerJoined = previous })
	olga := a.signIn(t, "Olga")
	var failed map[string]any
	if code := a.post(t, olga, "/api/new", map[string]any{"action": "start", "name": "orders"}, &failed); code != http.StatusBadRequest ||
		!strings.Contains(failed["error"].(string), "A new app needs a hi server") {
		t.Fatalf("%d %v", code, failed)
	}
}
