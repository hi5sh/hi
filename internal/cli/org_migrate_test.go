package cli

import (
	"bytes"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func orgMigrateTest(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, ".local", "state"))
	t.Setenv("HI_ORG_DIR", "")
	t.Setenv("HI_SERVER_DIR", "")
	return home
}

// The server's state stays in ~/.local/state/hi/server until hi org run
// moves it to ~/.local/state/hi/org and links the old path there.
func TestOrgRunMovesTheStateDirectory(t *testing.T) {
	home := orgMigrateTest(t)
	old, target := filepath.Join(home, ".local/state/hi/server"), filepath.Join(home, ".local/state/hi/org")
	os.MkdirAll(old, 0o700)
	os.WriteFile(filepath.Join(old, "config.json"), []byte("{}"), 0o600)

	if dir, _ := orgDirectory(""); dir != old {
		t.Fatalf("before the move: %s", dir)
	}
	var out bytes.Buffer
	if err := moveOrgDirectory(old, &out); err != nil || !strings.Contains(out.String(), "Moved the org server's state") {
		t.Fatalf("move: %v %s", err, out.String())
	}
	if dir, _ := orgDirectory(""); dir != target {
		t.Fatalf("after the move: %s", dir)
	}
	if link, err := os.Readlink(old); err != nil || link != "org" {
		t.Fatalf("old path: %q %v", link, err)
	}
	if _, err := os.Stat(filepath.Join(old, "config.json")); err != nil {
		t.Fatalf("the old path doesn't reach the state: %v", err)
	}
	out.Reset()
	if moveOrgDirectory(target, &out); out.Len() != 0 {
		t.Fatalf("moved twice: %s", out.String())
	}

	// A directory of the admin's choosing stays, and HI_ORG_DIR wins.
	custom := filepath.Join(home, "elsewhere")
	os.MkdirAll(custom, 0o700)
	if moveOrgDirectory(custom, &out); out.Len() != 0 {
		t.Fatalf("moved a custom directory: %s", out.String())
	}
	t.Setenv("HI_SERVER_DIR", custom)
	if dir, _ := orgDirectory(""); dir != custom {
		t.Fatalf("HI_SERVER_DIR: %s", dir)
	}
	t.Setenv("HI_ORG_DIR", target)
	if dir, _ := orgDirectory(""); dir != target {
		t.Fatalf("HI_ORG_DIR: %s", dir)
	}
}

// A device's server.json becomes org.json the first time it is read, and
// hi org leave removes both.
func TestDeviceConnectionMovesToOrgJSON(t *testing.T) {
	home := orgMigrateTest(t)
	config := filepath.Join(home, ".config", "hi")
	os.MkdirAll(config, 0o700)
	os.WriteFile(filepath.Join(config, "server.json"), []byte(`{"url": "http://100.64.0.1:7373", "user": "ana"}`), 0o600)

	connection, err := loadServerConnection()
	if err != nil || connection == nil || connection.User != "ana" {
		t.Fatalf("load: %+v %v", connection, err)
	}
	if _, err := os.Stat(filepath.Join(config, "org.json")); err != nil {
		t.Fatalf("org.json: %v", err)
	}
	if link, _ := os.Readlink(filepath.Join(config, "server.json")); link != "org.json" {
		t.Fatalf("server.json links to %q", link)
	}

	// An older hi saves server.json over the link: the newer copy wins.
	os.Remove(filepath.Join(config, "server.json"))
	os.WriteFile(filepath.Join(config, "server.json"), []byte(`{"url": "http://100.64.0.1:7373", "user": "bo"}`), 0o600)
	future := time.Now().Add(time.Minute)
	os.Chtimes(filepath.Join(config, "server.json"), future, future)
	if connection, _ := loadServerConnection(); connection == nil || connection.User != "bo" {
		t.Fatalf("after an older hi saved: %+v", connection)
	}
	if link, _ := os.Readlink(filepath.Join(config, "server.json")); link != "org.json" {
		t.Fatalf("server.json isn't linked again: %q", link)
	}
	// An older copy than org.json is only linked again.
	os.Remove(filepath.Join(config, "server.json"))
	os.WriteFile(filepath.Join(config, "server.json"), []byte(`{"url": "x", "user": "old"}`), 0o600)
	past := time.Now().Add(-time.Hour)
	os.Chtimes(filepath.Join(config, "server.json"), past, past)
	if connection, _ := loadServerConnection(); connection == nil || connection.User != "bo" {
		t.Fatalf("an older copy won: %+v", connection)
	}

	if code, _, stderr := runHi("org", "leave"); code != 0 {
		t.Fatalf("leave: %s", stderr)
	}
	for _, name := range []string{"org.json", "server.json"} {
		if _, err := os.Lstat(filepath.Join(config, name)); !os.IsNotExist(err) {
			t.Errorf("%s is still there after hi org leave", name)
		}
	}
}

func TestOrgUnitFromAnOldUnit(t *testing.T) {
	account, err := user.Current()
	if err != nil {
		t.Skip(err)
	}
	old := "[Unit]\nDescription=hi compute server\n\n[Service]\nUser=" + account.Username +
		"\nEnvironment=HI_SERVER_DIR=" + filepath.Join(account.HomeDir, ".local/state/hi/server") +
		"\nExecStart=/usr/local/bin/hi server run\nLimitNOFILE=4096\n"
	unit := orgUnitFrom(old)
	for _, line := range []string{"Description=hi org server", "Environment=HI_ORG_DIR=" + filepath.Join(account.HomeDir, ".local/state/hi/org"),
		"ExecStart=/usr/local/bin/hi org run", "LimitNOFILE=4096", "User=" + account.Username} {
		if !strings.Contains(unit, line+"\n") {
			t.Errorf("unit lacks %q:\n%s", line, unit)
		}
	}
	custom := orgUnitFrom("[Service]\nEnvironment=\"HI_SERVER_DIR=/srv/hi\" OTHER=1\nExecStart=/usr/local/bin/hi org run --dir /srv/hi\n")
	if !strings.Contains(custom, "Environment=\"HI_ORG_DIR=/srv/hi\" OTHER=1\n") || !strings.Contains(custom, "ExecStart=/usr/local/bin/hi org run --dir /srv/hi\n") {
		t.Errorf("custom unit:\n%s", custom)
	}
}

// hi update replaces hi-server.service with hi-org.service, which restarts
// the server; without a terminal it only says so.
func TestUpdateMovesTheOrgUnit(t *testing.T) {
	requireLinux(t)
	if os.Geteuid() == 0 {
		t.Skip("as root, systemctl runs without sudo")
	}
	installedBinary(t, "v0.9.0")
	fakeReleases(t, "v0.9.0", false)
	path, _ := updateExecutable()
	fakeProc(t, map[string][3]string{
		"101": {path + " (deleted)", path + "\x00server\x00run\x00", "0::/system.slice/hi-server.service\n"},
	})
	os.WriteFile(filepath.Join(systemdUnitDir, oldOrgUnit), []byte("[Service]\nExecStart=/usr/local/bin/hi server run\n"), 0o644)
	var runs []string
	installed := ""
	previous := updateRun
	updateRun = func(_ io.Reader, _, _ io.Writer, name string, args ...string) error {
		runs = append(runs, strings.Join(append([]string{name}, args...), " "))
		if len(args) == 5 && args[0] == "install" {
			data, _ := os.ReadFile(args[3])
			installed = string(data)
			args[3] = "<unit>"
			runs[len(runs)-1] = strings.Join(append([]string{name}, args...), " ")
		}
		return nil
	}
	t.Cleanup(func() { updateRun = previous })

	code, stdout, _ := runUpdateTest()
	if code != 0 || !strings.Contains(stdout, "hi-server.service is now called hi-org.service; run `hi update` on a terminal") || len(runs) != 0 {
		t.Fatalf("without a terminal: %d %q\n%s", code, runs, stdout)
	}
	code, stdout, stderr := runUpdateTest("--restart")
	want := []string{
		"sudo install -m 0644 <unit> " + filepath.Join(systemdUnitDir, orgUnit),
		"sudo systemctl daemon-reload",
		"sudo systemctl disable --now hi-server.service",
		"sudo systemctl enable --now hi-org.service",
		"systemctl is-active --quiet hi-org.service",
	}
	if code != 0 || strings.Join(runs, "|") != strings.Join(want, "|") || !strings.Contains(stdout, "hi-org.service runs v0.9.0.") {
		t.Fatalf("move: %d %q\n%s%s", code, runs, stdout, stderr)
	}
	if !strings.Contains(installed, "ExecStart=/usr/local/bin/hi org run\n") {
		t.Fatalf("installed unit:\n%s", installed)
	}

	// Once hi-org.service is there, nothing more is offered.
	os.WriteFile(filepath.Join(systemdUnitDir, orgUnit), []byte(installed), 0o644)
	runs = nil
	if _, stdout, _ := runUpdateTest("--no-restart"); strings.Contains(stdout, "now called") {
		t.Fatalf("offered again:\n%s", stdout)
	}
}

func TestQSavedServerProviderIsOrg(t *testing.T) {
	home := orgMigrateTest(t)
	os.MkdirAll(filepath.Join(home, ".config", "hi"), 0o700)
	os.WriteFile(filepath.Join(qConfigDirectory(), "q.json"), []byte(`{"provider": "server", "model": "m"}`), 0o600)
	if config, _, err := loadQConfig(); err != nil || config.Provider != "org" {
		t.Fatalf("%+v %v", config, err)
	}
}

// On a terminal, the old names say the new one; scripts see nothing new.
func TestOldNamesHintOnATerminal(t *testing.T) {
	orgMigrateTest(t)
	if _, _, stderr := runHi("disconnect"); strings.Contains(stderr, "is now") {
		t.Fatalf("hint without a terminal: %s", stderr)
	}
	previous := oldNameIsTerminal
	oldNameIsTerminal = func(io.Writer) bool { return true }
	t.Cleanup(func() { oldNameIsTerminal = previous })
	for args, want := range map[string]string{
		"disconnect":       "hi disconnect is now hi org leave",
		"connect status":   "hi connect status is now hi org status",
		"server audit -n1": "hi server audit -n1 is now hi org audit -n1",
	} {
		if _, _, stderr := runHi(strings.Fields(args)...); !strings.Contains(stderr, want) {
			t.Errorf("hi %s: %q", args, stderr)
		}
	}
	if got := orgFormOf([]string{"connect", "100.64.0.1:7373", "--user", "ana"}); got != "hi org join 100.64.0.1:7373 --user ana" {
		t.Errorf("connect: %s", got)
	}
	if _, _, stderr := runHi("org", "status"); strings.Contains(stderr, "is now") {
		t.Errorf("hint on hi org: %s", stderr)
	}
}
