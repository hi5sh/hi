package cli

import (
	"bytes"
	"errors"
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
	if err := moveOrgDirectory(old, &out); err != nil || !strings.Contains(out.String(), "Moved the server's state") {
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
	for _, line := range []string{"Description=hi server", "Environment=HI_ORG_DIR=" + filepath.Join(account.HomeDir, ".local/state/hi/org"),
		"ExecStart=/usr/local/bin/hi server run", "LimitNOFILE=4096", "User=" + account.Username} {
		if !strings.Contains(unit, line+"\n") {
			t.Errorf("unit lacks %q:\n%s", line, unit)
		}
	}
	custom := orgUnitFrom("[Service]\nEnvironment=\"HI_SERVER_DIR=/srv/hi\" OTHER=1\nExecStart=/usr/local/bin/hi org run --dir /srv/hi\n")
	if !strings.Contains(custom, "Environment=\"HI_ORG_DIR=/srv/hi\" OTHER=1\n") || !strings.Contains(custom, "ExecStart=/usr/local/bin/hi server run --dir /srv/hi\n") {
		t.Errorf("custom unit:\n%s", custom)
	}
}

// hi update makes the org server's system unit a user service, with
// sudo this once; without a terminal it only says so.
func TestUpdateMovesTheOrgUnitToAUserService(t *testing.T) {
	requireLinux(t)
	if os.Geteuid() == 0 {
		t.Skip("as root, systemctl runs without sudo")
	}
	installedBinary(t, "v0.9.0")
	fakeReleases(t, "v0.9.0", false)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	path, _ := updateExecutable()
	fakeProc(t, map[string][3]string{
		"101": {path + " (deleted)", path + "\x00server\x00run\x00", "0::/system.slice/hi-server.service\n"},
	})
	os.WriteFile(filepath.Join(systemdUnitDir, "hi-server.service"), []byte("[Unit]\nDescription=hi compute server\n\n[Service]\nUser="+currentUserName()+
		"\nExecStart=/usr/local/bin/hi server run\nRestart=always\n\n[Install]\nWantedBy=multi-user.target\n"), 0o644)
	var runs []string
	previous, terminal, lingers := updateRun, updateIsTerminal, appLingers
	updateRun = func(_ io.Reader, _, _ io.Writer, name string, args ...string) error {
		runs = append(runs, strings.Join(append([]string{name}, args...), " "))
		return nil
	}
	appLingers = func() bool { return false }
	t.Cleanup(func() { updateRun, updateIsTerminal, appLingers = previous, terminal, lingers })

	code, stdout, _ := runUpdateTest("--restart")
	if code != 0 || !strings.Contains(stdout, "Run hi update on a terminal on this machine to move hi-server.service") || fileExists(orgUserUnitPath()) {
		t.Fatalf("without a terminal: %d %q\n%s", code, runs, stdout)
	}
	runs = nil
	updateIsTerminal = func(io.Reader) bool { return true }
	code, stdout, stderr := runUpdateTest("--restart")
	want := []string{
		"sudo loginctl enable-linger " + currentUserName(),
		"sudo systemctl disable --now hi-server.service",
		"systemctl --user daemon-reload",
		"systemctl --user enable --now hi-server.service",
		"systemctl --user is-active --quiet hi-server.service",
		"sudo rm " + filepath.Join(systemdUnitDir, "hi-server.service"),
		"sudo systemctl daemon-reload",
	}
	if code != 0 || strings.Join(runs, "|") != strings.Join(want, "|") || !strings.Contains(stdout, "hi-server.service runs v0.9.0.") {
		t.Fatalf("move: %d %q\n%s%s", code, runs, stdout, stderr)
	}
	unit, _ := os.ReadFile(orgUserUnitPath())
	for _, line := range []string{"ExecStart=/usr/local/bin/hi server run\n", "WantedBy=default.target\n", "Description=hi server\n", "RestartSec=5\n"} {
		if !strings.Contains(string(unit), line) {
			t.Errorf("the user unit lacks %q:\n%s", line, unit)
		}
	}
	if strings.Contains(string(unit), "User=") {
		t.Errorf("the user unit names a user:\n%s", unit)
	}

	// Once it's a user service, nothing more is offered.
	os.Remove(filepath.Join(systemdUnitDir, "hi-server.service"))
	if _, stdout, _ := runUpdateTest("--no-restart"); strings.Contains(stdout, "user service") {
		t.Fatalf("offered again:\n%s", stdout)
	}
}

// A system unit that runs the server as another account isn't moved.
func TestOrgUnitMoveRefusesAnotherAccount(t *testing.T) {
	systemdUnitDir = t.TempDir()
	t.Cleanup(func() { systemdUnitDir = "/etc/systemd/system" })
	os.WriteFile(filepath.Join(systemdUnitDir, oldOrgUnit), []byte("[Service]\nUser=someone-else\nExecStart=/usr/local/bin/hi org run\n"), 0o644)
	if err := moveOrgUnit(nil, io.Discard, io.Discard); err == nil || !strings.Contains(err.Error(), "as someone-else") {
		t.Fatalf("moved: %v", err)
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
		"disconnect":     "hi disconnect is now hi server leave",
		"connect status": "hi connect status is now hi server status",
	} {
		if _, _, stderr := runHi(strings.Fields(args)...); !strings.Contains(stderr, want) {
			t.Errorf("hi %s: %q", args, stderr)
		}
	}
	if got := orgFormOf([]string{"connect", "100.64.0.1:7373", "--user", "ana"}); got != "hi server join 100.64.0.1:7373 --user ana" {
		t.Errorf("connect: %s", got)
	}
	for _, name := range []string{"server", "org"} {
		if _, _, stderr := runHi(name, "status"); strings.Contains(stderr, "is now") {
			t.Errorf("hint on hi %s: %s", name, stderr)
		}
	}
}

// After an update on the org server's machine, hi update says what it
// still needs: an owner, and the old wall sessions removed.
func TestUpdateNotesOnTheOrgMachine(t *testing.T) {
	orgMigrateTest(t)
	dir := t.TempDir()
	t.Setenv("HI_ORG_DIR", dir)
	os.WriteFile(filepath.Join(dir, "config.json"), []byte("{}"), 0o600)
	systemdUnitDir = t.TempDir()
	t.Cleanup(func() { systemdUnitDir = "/etc/systemd/system" })
	os.WriteFile(filepath.Join(systemdUnitDir, "hi-wall-live.service"), []byte("[Service]\n"), 0o644)
	var out bytes.Buffer
	orgUpdateNotes(&out)
	for _, want := range []string{"The server has no owner yet", "sudo systemctl disable --now hi-wall-live.service", "sudo userdel -r hi-wall"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("no %q in:\n%s", want, out.String())
		}
	}
	applyOrgPeople(dir, orgPeopleInput{As: "t", Action: "owner", Slack: "U07OWNER1", Email: "olga@example.com"})
	out.Reset()
	orgUpdateNotes(&out)
	if strings.Contains(out.String(), "no owner") {
		t.Errorf("an owner, and still: %s", out.String())
	}
	if _, stdout, _ := runHi("org", "status"); !strings.Contains(stdout, "owned by olga") {
		t.Errorf("status: %s", stdout)
	}
}

// A slip between hi serve and hi server says which was meant and runs
// nothing (hi_server_apps.md).
func TestServeAndServerSayWhichWasMeant(t *testing.T) {
	orgMigrateTest(t)
	for args, want := range map[string]string{
		"serve join 100.64.0.1:7373": "did you mean hi server join?",
		"serve approvers":            "did you mean hi server approvers?",
		"server llm qwen":            "did you mean hi serve llm?",
		"org recipes":                "did you mean hi serve recipes?",
	} {
		code, stdout, stderr := runHi(strings.Fields(args)...)
		if code != 2 || stdout != "" || !strings.Contains(stderr, want) {
			t.Errorf("hi %s: %d %q %q", args, code, stdout, stderr)
		}
	}
}

// hi update renames the server's user service from hi-org.service to
// hi-server.service as it restarts it.
func TestOrgUserUnitRenamed(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("as root, systemctl runs without sudo")
	}
	installedBinary(t, "v0.9.0")
	fakeReleases(t, "v0.9.0", false)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	systemdUnitDir = t.TempDir()
	t.Cleanup(func() { systemdUnitDir = "/etc/systemd/system" })
	path, _ := updateExecutable()
	fakeProc(t, map[string][3]string{
		"101": {path + " (deleted)", path + "\x00org\x00run\x00", "0::/user.slice/user-1000.slice/user@1000.service/app.slice/hi-org.service\n"},
	})
	os.MkdirAll(filepath.Dir(oldOrgUserUnitPath()), 0o755)
	os.WriteFile(oldOrgUserUnitPath(), []byte("[Unit]\nDescription=hi org server\n\n[Service]\nEnvironment=HI_ORG_DIR=/srv/org\nExecStart=/usr/local/bin/hi org run\nRestart=always\n\n[Install]\nWantedBy=default.target\n"), 0o644)
	var runs []string
	previous := appSystemctl
	appSystemctl = func(args ...string) (string, error) {
		runs = append(runs, strings.Join(args, " "))
		return "", nil
	}
	t.Cleanup(func() { appSystemctl = previous })

	code, stdout, stderr := runUpdateTest("--restart")
	want := []string{"daemon-reload", "disable --now hi-org.service", "enable --now hi-server.service", "is-active --quiet hi-server.service", "daemon-reload"}
	if code != 0 || strings.Join(runs, "|") != strings.Join(want, "|") || !strings.Contains(stdout, "hi-server.service runs v0.9.0.") {
		t.Fatalf("rename: %d %q\n%s%s", code, runs, stdout, stderr)
	}
	if fileExists(oldOrgUserUnitPath()) {
		t.Error("hi-org.service is still there")
	}
	unit, _ := os.ReadFile(orgUserUnitPath())
	for _, line := range []string{"Description=hi server\n", "Environment=HI_ORG_DIR=/srv/org\n", "ExecStart=/usr/local/bin/hi server run\n"} {
		if !strings.Contains(string(unit), line) {
			t.Errorf("hi-server.service lacks %q:\n%s", line, unit)
		}
	}

	// When the new one doesn't come up, the old one runs again.
	os.Remove(orgUserUnitPath())
	os.WriteFile(oldOrgUserUnitPath(), []byte("[Service]\nExecStart=/usr/local/bin/hi org run\n"), 0o644)
	runs = nil
	appSystemctl = func(args ...string) (string, error) {
		runs = append(runs, strings.Join(args, " "))
		if args[0] == "is-active" {
			return "", errors.New("inactive")
		}
		return "", nil
	}
	if err := moveOrgUserUnit(io.Discard); err == nil || !strings.Contains(err.Error(), "hi-org.service runs again") || !fileExists(oldOrgUserUnitPath()) || fileExists(orgUserUnitPath()) {
		t.Fatalf("no way back: %v %q", err, runs)
	}
	if last := runs[len(runs)-1]; last != "enable --now hi-org.service" {
		t.Errorf("last: %q", last)
	}
}
