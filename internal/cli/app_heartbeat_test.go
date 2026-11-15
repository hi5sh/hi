package cli

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// heartbeatTestService is an outside service that counts heartbeats by
// path, answering 200, or 404 for a check it doesn't know.
func heartbeatTestService(t *testing.T) (*httptest.Server, func(string) int) {
	t.Helper()
	var mu sync.Mutex
	hits := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits[r.URL.Path]++
		mu.Unlock()
		if strings.Contains(r.URL.Path, "unknown") {
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server, func(path string) int { mu.Lock(); defer mu.Unlock(); return hits[path] }
}

// A team's heartbeat: set with a first ping, sent by the broker at its
// interval, shown by host only, and off again.
func TestTeamHeartbeat(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	saveTeamSecrets("payments", appSecrets{SlackBotToken: "xoxb-1"})
	service, hits := heartbeatTestService(t)
	secret := service.URL + "/ping/secret-uuid"
	var out bytes.Buffer

	if err := appHeartbeatCommand([]string{"payments"}, &out); err != nil || !strings.Contains(out.String(), "Heartbeat: none") {
		t.Fatal(err, out.String())
	}
	for _, bad := range [][]string{{"ftp://x"}, {"not a url"}, {service.URL + "/unknown"}, {secret, "--every", "5s"}, {secret, "--every", "2h"}} {
		if err := appHeartbeatCommand(append([]string{"payments"}, bad...), &out); err == nil {
			t.Errorf("%q was taken", bad)
		}
	}
	out.Reset()
	if err := appHeartbeatCommand([]string{"payments", secret, "--every", "5m"}, &out); err != nil {
		t.Fatal(err)
	}
	if hits("/ping/secret-uuid") != 1 || strings.Contains(out.String(), "secret-uuid") || !strings.Contains(out.String(), "every 5m") {
		t.Fatal(hits("/ping/secret-uuid"), out.String())
	}
	config, _ := loadTeam("payments")
	secrets, _ := loadTeamSecrets("payments")
	if config.HeartbeatEvery != "5m" || config.HeartbeatURL != secret || secrets.SlackBotToken != "xoxb-1" {
		t.Fatal(config, secrets)
	}
	// In app.json, out of the vault, so it goes while the vault is locked.
	if data, _ := os.ReadFile(filepath.Join(appDir("payments"), "secrets.json")); strings.Contains(string(data), "secret-uuid") {
		t.Fatal("the URL is in secrets.json")
	}

	// The broker sends it, records it, and waits its interval.
	if every := b.beat(); every.String() != "5m0s" || hits("/ping/secret-uuid") != 2 {
		t.Fatal(every, hits("/ping/secret-uuid"))
	}
	line := appHeartbeatLine(config)
	if !strings.Contains(line, "every 5m; the last was sent") || strings.Contains(line, "secret-uuid") {
		t.Fatal(line)
	}

	// An error names the host, not the URL.
	service.Close()
	b.beat()
	if last := loadTeamHeartbeatLast("payments"); last.Error == "" || strings.Contains(last.Error, "secret-uuid") || !strings.Contains(last.Error, heartbeatHost(secret)) {
		t.Fatalf("%+v", last)
	}

	out.Reset()
	if err := appHeartbeatCommand([]string{"payments", "off"}, &out); err != nil || !strings.Contains(out.String(), "will alert") {
		t.Fatal(err, out.String())
	}
	if config, _ := loadTeam("payments"); appHeartbeatURL(config) != "" {
		t.Fatal("still set")
	}
}

// The machine's heartbeat: a systemd user timer that runs hi heartbeat
// __ping, and the answers file of hi app new sets it once.
func TestMachineHeartbeat(t *testing.T) {
	setupTeamTest(t)
	var calls []string
	saved := appSystemctl
	appSystemctl = func(args ...string) (string, error) { calls = append(calls, strings.Join(args, " ")); return "", nil }
	t.Cleanup(func() { appSystemctl = saved })
	service, hits := heartbeatTestService(t)
	var out bytes.Buffer

	if err := heartbeatCommand(nil, &out); err != nil || !strings.Contains(out.String(), "sends no heartbeat") {
		t.Fatal(err, out.String())
	}
	if err := heartbeatCommand([]string{service.URL + "/m", "--every", "30s"}, &out); err == nil {
		t.Fatal("an interval under a minute")
	}
	out.Reset()
	if err := heartbeatCommand([]string{service.URL + "/m", "--every", "10m"}, &out); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Dir(appUnitPath("x"))
	timer, _ := os.ReadFile(filepath.Join(dir, "hi-heartbeat.timer"))
	unit, _ := os.ReadFile(filepath.Join(dir, "hi-heartbeat.service"))
	if !strings.Contains(string(timer), "OnUnitActiveSec=10m") || !strings.Contains(string(unit), " heartbeat __ping") || strings.Contains(string(unit), "/m") && strings.Contains(string(unit), service.URL) {
		t.Fatal(string(timer), string(unit))
	}
	if !strings.Contains(strings.Join(calls, "\n"), "enable --now hi-heartbeat.timer") || hits("/m") != 1 {
		t.Fatal(calls, hits("/m"))
	}
	if info, _ := os.Stat(machineHeartbeatPath()); info.Mode().Perm() != 0o600 {
		t.Fatal(info.Mode())
	}
	// The timer's ping, recorded.
	if err := heartbeatCommand([]string{"__ping"}, &out); err != nil || hits("/m") != 2 {
		t.Fatal(err, hits("/m"))
	}
	out.Reset()
	heartbeatCommand(nil, &out)
	if !strings.Contains(out.String(), "every 10m; the last was sent") {
		t.Fatal(out.String())
	}

	// The wizard's answers don't replace a machine's heartbeat.
	applyTeamHeartbeats("payments", appAnswers{MachineHeartbeatURL: service.URL + "/other"}, &out)
	if hits("/other") != 0 {
		t.Fatal("replaced the machine's heartbeat")
	}
	out.Reset()
	if err := heartbeatCommand([]string{"off"}, &out); err != nil || fileExists(filepath.Join(dir, "hi-heartbeat.timer")) || fileExists(machineHeartbeatPath()) {
		t.Fatal(err, out.String())
	}
	applyTeamHeartbeats("payments", appAnswers{MachineHeartbeatURL: service.URL + "/other"}, &out)
	if hits("/other") != 1 {
		t.Fatal("the answers' machine heartbeat wasn't set")
	}
}

// hi before v2.6.154 kept the URL in secrets.json: it is read there, and
// moves to app.json once no process runs that hi.
func TestAppHeartbeatMovesToAppJSON(t *testing.T) {
	setupTeamTest(t)
	newTeamTestBroker(t)
	if err := saveTeamSecrets("payments", appSecrets{SlackBotToken: "xoxb-1", LegacyHeartbeatURL: "https://hc-ping.example/abc"}); err != nil {
		t.Fatal(err)
	}
	saved := vaultKeepFiles
	t.Cleanup(func() { vaultKeepFiles = saved })
	vaultKeepFiles = func() bool { return true }
	if err := moveAppHeartbeat("payments"); err != nil {
		t.Fatal(err)
	}
	config, _ := loadTeam("payments")
	if config.HeartbeatURL != "" || appHeartbeatURL(config) != "https://hc-ping.example/abc" {
		t.Fatalf("while an older hi runs: %+v", config)
	}
	vaultKeepFiles = func() bool { return false }
	if err := moveAppHeartbeat("payments"); err != nil {
		t.Fatal(err)
	}
	config, _ = loadTeam("payments")
	if config.HeartbeatURL != "https://hc-ping.example/abc" || appLegacyHeartbeat("payments") != "" {
		t.Fatalf("moved: %+v %q", config, appLegacyHeartbeat("payments"))
	}
	if secrets, _ := loadTeamSecrets("payments"); secrets.SlackBotToken != "xoxb-1" {
		t.Fatalf("the other secrets: %+v", secrets)
	}
}
