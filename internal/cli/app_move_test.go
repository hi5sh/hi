package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAppMachinesAddAndRemove(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	var out bytes.Buffer
	if err := appMachinesCommand([]string{"add", "ws1", "ssh-rsa", "AAAA"}, &out); err == nil {
		t.Fatal("took a key that isn't ed25519")
	}
	if err := appMachinesCommand([]string{"add", "ws1;rm", "ssh-ed25519", "AAAAC3NzaC1lZDI1NTE5AAAAIGk="}, &out); err == nil {
		t.Fatal("took a machine name that isn't one")
	}
	os.MkdirAll(filepath.Join(home, ".ssh"), 0o700)
	os.WriteFile(filepath.Join(home, ".ssh", "authorized_keys"), []byte("ssh-ed25519 AAAAothers someone@laptop\n"), 0o600)
	if err := appMachinesCommand([]string{"add", "ws1", "ssh-ed25519", "AAAAC3NzaC1lZDI1NTE5AAAAIGk=", "hi-move@ws1"}, &out); err != nil {
		t.Fatal(err)
	}
	// Adding it again replaces the line.
	if err := appMachinesCommand([]string{"add", "ws1", "ssh-ed25519", "AAAAC3NzaC1lZDI1NTE5AAAAIGk="}, &out); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(home, ".ssh", "authorized_keys"))
	text := string(data)
	if strings.Count(text, appMoveKeyMarker+"ws1") != 1 || !strings.Contains(text, `restrict,command="`) || !strings.Contains(text, ` app __receive ws1" ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIGk= hi-move-from:ws1`) ||
		!strings.Contains(text, "someone@laptop") {
		t.Fatalf("authorized_keys:\n%s", text)
	}
	if from := appMachinesFrom(); len(from) != 1 || from[0] != "ws1" {
		t.Fatal(from)
	}
	if err := appMachinesCommand([]string{"rm", "ws1"}, &out); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(filepath.Join(home, ".ssh", "authorized_keys"))
	if strings.Contains(string(data), "ws1") || !strings.Contains(string(data), "someone@laptop") {
		t.Fatalf("after rm:\n%s", data)
	}
	if err := appMachinesCommand([]string{"rm", "ws1"}, &out); err == nil {
		t.Fatal("removed a machine that wasn't there")
	}
}

func TestAppReceiveTakesOnlyWhatAMoveSends(t *testing.T) {
	dir := setupTeamTest(t)
	folder := filepath.Join(dir, "incoming", "app-payments")
	var out bytes.Buffer
	if err := appReceive("ws1", []string{"prepare", "payments", folder}, nil, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out.String()) != appSnapshotsDir("payments") || !fileExists(filepath.Join(folder, appReceivingFile)) {
		t.Fatalf("prepare said %q", out.String())
	}
	for _, words := range [][]string{
		{"rsync", "--server", "-logDtpre.iLsfxCIvu", "--delete", ".", filepath.Join(dir, "elsewhere")},
		{"rsync", "--server", "--sender", "-logDtpre.iLsfxCIvu", ".", folder},
		{"rsync", "--server", "--log-file=/tmp/x", ".", folder},
		{"rsync", "--server", "-logDtpre.iLsfxCIvu", ".", "relative/path"},
		{"rsync", "--daemon", ".", folder},
		{"sh", "-c", "id"},
	} {
		if err := appReceive("ws1", words, nil, io.Discard, io.Discard); err == nil {
			t.Errorf("ran %v", words)
		}
	}
	// A folder that is someone else's isn't received into.
	other := filepath.Join(dir, "other")
	os.MkdirAll(other, 0o700)
	os.WriteFile(filepath.Join(other, "notes.txt"), []byte("mine"), 0o600)
	if err := appReceive("ws1", []string{"prepare", "billing", other}, nil, io.Discard, io.Discard); err == nil || !strings.Contains(err.Error(), "in the way") {
		t.Fatal(err)
	}
}

// moveTestMachines sets up two machines in one process: ws1, this one, and
// ws2, whose apps, units, and snapshots are elsewhere. The SSH connection
// runs hi app __receive with ws2's settings, and rsync copies locally.
type moveTestMachines struct {
	ws1Apps, ws1Config, ws2Apps, ws2Config string
	posts                                  []string
	running                                bool
}

func (m *moveTestMachines) on(apps, config string, action func()) {
	savedApps, savedConfig := os.Getenv("HI_APPS_DIR"), os.Getenv("XDG_CONFIG_HOME")
	os.Setenv("HI_APPS_DIR", apps)
	os.Setenv("XDG_CONFIG_HOME", config)
	defer func() {
		os.Setenv("HI_APPS_DIR", savedApps)
		os.Setenv("XDG_CONFIG_HOME", savedConfig)
	}()
	action()
}

func setupMoveTest(t *testing.T) (*moveTestMachines, string) {
	dir := setupTeamTest(t)
	if _, err := exec.LookPath("rsync"); err != nil {
		t.Skip("rsync isn't installed")
	}
	t.Setenv("HOME", filepath.Join(dir, "home"))
	m := &moveTestMachines{ws1Apps: os.Getenv("HI_APPS_DIR"), ws1Config: os.Getenv("XDG_CONFIG_HOME"),
		ws2Apps: filepath.Join(dir, "ws2", "apps"), ws2Config: filepath.Join(dir, "ws2", "config"), running: true}
	saved := []any{appSystemctl, appRemoveContainers, appLingers, appMoveCall, appMoveRsync, appMovePost, appMoveReachable, appPeerIP, appMoveContainerRunning, appMoveStartPatience, appMovePoll}
	t.Cleanup(func() {
		appSystemctl = saved[0].(func(...string) (string, error))
		appRemoveContainers = saved[1].(func(string, []string))
		appLingers = saved[2].(func() bool)
		appMoveCall = saved[3].(func(appMoveTarget, io.Reader, io.Writer, io.Writer, ...string) error)
		appMoveRsync = saved[4].(func(appMoveTarget, string, string, io.Writer) error)
		appMovePost = saved[5].(func(appConfig, appSecrets, string))
		appMoveReachable = saved[6].(func(string) bool)
		appPeerIP = saved[7].(func(string) (netip.Addr, error))
		appMoveContainerRunning = saved[8].(func(string) bool)
		appMoveStartPatience, appMovePoll = saved[9].(time.Duration), saved[10].(time.Duration)
	})
	appSystemctl = func(args ...string) (string, error) {
		if args[0] == "is-active" {
			return "active", nil
		}
		return "", nil
	}
	appRemoveContainers = func(string, []string) {}
	appLingers = func() bool { return true }
	appMoveCall = func(target appMoveTarget, stdin io.Reader, stdout, stderr io.Writer, words ...string) error {
		var err error
		m.on(m.ws2Apps, m.ws2Config, func() { err = appReceive("ws1", words, stdin, stdout, stderr) })
		return err
	}
	appMoveRsync = func(target appMoveTarget, from, to string, stdout io.Writer) error {
		out, err := exec.Command("rsync", "-aH", "--delete", "--exclude=*.sock", "--exclude=/"+appReceivingFile, "--exclude=/"+appMovedFile, from+"/", to+"/").CombinedOutput()
		if err != nil {
			t.Fatalf("rsync: %s", out)
		}
		return nil
	}
	appMovePost = func(config appConfig, secrets appSecrets, text string) { m.posts = append(m.posts, text) }
	appMoveReachable = func(string) bool { return true }
	appPeerIP = func(string) (netip.Addr, error) { return netip.MustParseAddr("100.64.0.9"), nil }
	appMoveContainerRunning = func(string) bool { return m.running }
	appMoveStartPatience, appMovePoll = 50*time.Millisecond, 10*time.Millisecond

	folder := filepath.Join(dir, "app-payments")
	os.MkdirAll(filepath.Join(folder, "dev", "netbird"), 0o700)
	if err := appRegister("payments", folder); err != nil {
		t.Fatal(err)
	}
	saveTeam(testTeamConfig())
	saveTeamSecrets("payments", appSecrets{SlackBotToken: "xoxb-1", SlackAppToken: "xapp-1", OpenRouterAPIKey: "sk-or-app"})
	os.WriteFile(filepath.Join(folder, "dev", "netbird", "default.json"), []byte(`{"PrivateKey":"peer"}`), 0o600)
	os.MkdirAll(filepath.Join(folder, "app"), 0o700)
	os.WriteFile(filepath.Join(folder, "app", "data.db"), []byte("the people's work"), 0o600)
	os.MkdirAll(filepath.Dir(appUnitPath("payments")), 0o755)
	os.WriteFile(appUnitPath("payments"), []byte("[Unit]\n"), 0o644)
	os.MkdirAll(appSnapshotsDir("payments"), 0o700)
	os.WriteFile(appSnapshotKey("payments"), []byte("key\n"), 0o600)
	return m, folder
}

func TestAppMove(t *testing.T) {
	m, folder := setupMoveTest(t)
	there := filepath.Join(filepath.Dir(folder), "ws2-home", "app-payments")
	var out bytes.Buffer
	if err := appMoveCommand([]string{"payments", "ws2", "--dir", there, "--check"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err, out.String())
	}
	if !strings.Contains(out.String(), "can move to ws2") || fileExists(there) {
		t.Fatalf("--check moved something:\n%s", out.String())
	}
	out.Reset()
	if err := appMoveCommand([]string{"payments", "ws2", "--dir", there, "--yes"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err, out.String())
	}
	if !strings.Contains(out.String(), "runs on ws2 now, at the same addresses") {
		t.Fatal(out.String())
	}
	// ws2 has the folder, its secrets and the peer's identity, its
	// snapshots and their key, its link, and its service.
	for _, path := range []string{"secrets.json", "dev/netbird/default.json", "app/data.db"} {
		if !fileExists(filepath.Join(there, path)) {
			t.Errorf("%s didn't move", path)
		}
	}
	if fileExists(filepath.Join(there, appReceivingFile)) {
		t.Error("the folder there still says it is being received")
	}
	m.on(m.ws2Apps, m.ws2Config, func() {
		if appDir("payments") != there || !fileExists(appUnitPath("payments")) || !fileExists(appSnapshotKey("payments")) {
			t.Errorf("ws2: folder %s, unit %t, snapshot key %t", appDir("payments"), fileExists(appUnitPath("payments")), fileExists(appSnapshotKey("payments")))
		}
		if _, ok := loadAppIncoming("payments"); ok {
			t.Error("ws2 still receives the app")
		}
	})
	// ws1 keeps the folder, which says where the app went, and nothing else.
	if fileExists(filepath.Join(m.ws1Apps, "payments")) || fileExists(appUnitPath("payments")) {
		t.Error("ws1 still has the app's link or service")
	}
	moved, ok := appMovedAway(folder)
	if !ok || moved.To != "ws2" {
		t.Fatal("no moved.json", moved)
	}
	if _, err := appAdopt(folder, io.Discard); err == nil || !strings.Contains(err.Error(), "moved to ws2") {
		t.Fatal("ws1 would start the app again:", err)
	}
	if len(m.posts) != 2 || !strings.Contains(m.posts[1], "another machine") {
		t.Fatal(m.posts)
	}
}

func TestAppMoveGoesBackWhenItDoesntStartThere(t *testing.T) {
	m, folder := setupMoveTest(t)
	m.running = false // the lead never runs on ws2
	there := filepath.Join(filepath.Dir(folder), "ws2-home", "app-payments")
	var out bytes.Buffer
	err := appMoveCommand([]string{"payments", "ws2", "--dir", there, "--yes"}, strings.NewReader(""), &out)
	if err == nil || !strings.Contains(err.Error(), "didn't come up on ws2") {
		t.Fatal(err, out.String())
	}
	// ws1 runs it again; ws2 keeps a copy for the next try, and no app.
	if appDir("payments") != folder || !fileExists(appUnitPath("payments")) {
		t.Error("the app isn't back on ws1")
	}
	if _, moved := appMovedAway(folder); moved {
		t.Error("ws1's folder says the app moved")
	}
	m.on(m.ws2Apps, m.ws2Config, func() {
		if fileExists(filepath.Join(appsDir(), "payments")) || fileExists(appUnitPath("payments")) {
			t.Error("ws2 still has the app")
		}
		if !fileExists(filepath.Join(there, appReceivingFile)) {
			t.Error("ws2's copy doesn't say it's a copy being received")
		}
	})
	if last := m.posts[len(m.posts)-1]; !strings.Contains(last, "same machine") {
		t.Fatal(m.posts)
	}
}

func TestAppMoveCheckSaysWhatsMissing(t *testing.T) {
	m, _ := setupMoveTest(t)
	needs := appMoveNeedsOf("payments", "/srv/apps/app-payments")
	needs.Size = 1 << 62 // more than any disk
	needs.GitHub = false
	data, _ := json.Marshal(needs)
	var report appMoveReport
	var out bytes.Buffer
	var err error
	m.on(m.ws2Apps, m.ws2Config, func() { err = appReceive("ws1", []string{"check"}, bytes.NewReader(data), &out, io.Discard) })
	if err != nil {
		t.Fatal(err)
	}
	json.Unmarshal(out.Bytes(), &report)
	if !strings.Contains(strings.Join(report.Problems, "\n"), "free for the app's folder") {
		t.Fatal(report)
	}
	needs.Size, needs.Domain = 1, "hi.example"
	data, _ = json.Marshal(needs)
	out.Reset()
	m.on(m.ws2Apps, m.ws2Config, func() { appReceive("ws1", []string{"check"}, bytes.NewReader(data), &out, io.Discard) })
	report = appMoveReport{}
	json.Unmarshal(out.Bytes(), &report)
	if len(report.Problems) != 0 || !strings.Contains(strings.Join(report.Warnings, "\n"), "https names under hi.example stop working there") {
		t.Fatal(report)
	}
}

func TestAppRsyncErrorsSayWhichFiles(t *testing.T) {
	text := `rsync: [generator] chgrp "/srv/app-demo/run/home/.hermes" failed: Operation not permitted (1)
hi on ws2: exit status 23
rsync error: some files/attrs were not transferred (see previous errors) (code 23) at main.c(1338) [sender=3.4.1]
`
	got := appRsyncErrors(text, errors.New("exit status 23"))
	if !strings.Contains(got, `chgrp "/srv/app-demo/run/home/.hermes" failed`) || strings.Contains(got, "hi on ws2") || strings.Contains(got, "code 23") {
		t.Fatal(got)
	}
	if got := appRsyncErrors("", errors.New("exit status 12")); got != "exit status 12" {
		t.Fatal(got)
	}
}
