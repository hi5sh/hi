package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func newTestWatch(t *testing.T) (*hiServer, *[]string) {
	t.Helper()
	server, err := openServer(t.TempDir(), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	pushes := []string{}
	server.watch.push = func(config watchConfig, topic, title, text string) error {
		mu.Lock()
		defer mu.Unlock()
		pushes = append(pushes, topic+" "+text)
		return nil
	}
	server.watch.ping = func(string) error { return nil }
	return server, &pushes
}

func testBeat(at time.Time) apiHeartbeat {
	return apiHeartbeat{Team: "payments", At: at, Hi: "v1.4.0", Production: "v3",
		Parts:    map[string]apiWatchPart{"broker": {State: "up"}, "lead": {State: "up"}, "gateway": {State: "up"}, "production": {State: "up"}, "memory": {State: "off"}},
		Disk:     apiWatchDisk{Team: 0.4, Containers: 0.5, Inodes: 0.1},
		Snapshot: apiWatchSnapshot{Last: at.Add(-time.Hour)}}
}

var testWatchDevice = serverDevice{Fingerprint: "SHA256:payments-machine", User: "anna", Hostname: "aiw9"}

func openWatchRules(server *hiServer) []string {
	var rules []string
	for _, incident := range server.watch.view().Incidents {
		rules = append(rules, incident.Rule)
	}
	return rules
}

func TestWatchHeartbeatKeepsTheNetworkLogOnce(t *testing.T) {
	server, _ := newTestWatch(t)
	now := time.Now()
	beat := testBeat(now)
	beat.Network = &apiWatchNetwork{From: 0, To: 44, Lines: []string{"2026-10-22T09:13:02Z allowed api.github.com"}}
	reply, err := server.watch.heartbeat(server, testWatchDevice, beat, now)
	if err != nil || reply.NetworkKept != 44 {
		t.Fatal(reply, err)
	}
	// The answer was lost: the same lines again are kept once.
	if reply, _ = server.watch.heartbeat(server, testWatchDevice, beat, now); reply.NetworkKept != 44 {
		t.Fatal(reply)
	}
	beat.Network = &apiWatchNetwork{From: 44, To: 70, Lines: []string{"2026-10-22T09:14:00Z spend 0.02"}}
	server.watch.heartbeat(server, testWatchDevice, beat, now)
	// Rotated: noted, and kept from 0.
	beat.Network = &apiWatchNetwork{From: 0, To: 20, Rotated: true, Lines: []string{"2026-10-22T10:00:00Z x"}}
	if reply, _ = server.watch.heartbeat(server, testWatchDevice, beat, now); reply.NetworkKept != 20 {
		t.Fatal(reply)
	}
	data, _ := os.ReadFile(filepath.Join(server.watch.dir, "teams", "payments", "network.log"))
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 4 || !strings.Contains(lines[2], "started again from 0") || lines[3] != "2026-10-22T10:00:00Z x" {
		t.Fatalf("%q", lines)
	}

	// Another machine can't send the team's heartbeats until it is
	// forgotten.
	other := serverDevice{Fingerprint: "SHA256:other", User: "eve", Hostname: "laptop"}
	if _, err := server.watch.heartbeat(server, other, testBeat(now), now); err == nil || !strings.Contains(err.Error(), "watched from another machine (aiw9)") {
		t.Fatal(err)
	}
	admin := httptest.NewServer(server.adminHandler())
	defer admin.Close()
	if response, err := http.Post(admin.URL+"/admin/watch/forget/payments", "", nil); err != nil || response.StatusCode != 200 {
		t.Fatal(err, response.StatusCode)
	}
	if _, err := server.watch.heartbeat(server, other, testBeat(now), now); err != nil {
		t.Fatal(err)
	}
}

func TestWatchSilenceIsAnIncidentAndAPush(t *testing.T) {
	server, pushes := newTestWatch(t)
	server.watch.config.Ntfy = "https://ntfy.example"
	now := time.Now()
	server.watch.heartbeat(server, testWatchDevice, testBeat(now), now)
	server.checkWatch(now.Add(2 * time.Minute))
	if rules := openWatchRules(server); len(rules) != 0 {
		t.Fatal(rules)
	}
	server.checkWatch(now.Add(4 * time.Minute))
	server.checkWatch(now.Add(5 * time.Minute))
	if rules := openWatchRules(server); len(rules) != 1 || rules[0] != "heartbeat" {
		t.Fatal(rules)
	}
	teamTestEventually(t, "no push", func() bool {
		server.watch.mu.Lock()
		defer server.watch.mu.Unlock()
		return len(*pushes) == 1 && strings.HasPrefix((*pushes)[0], "hi-watch-payments payments has sent no heartbeat for 4 minutes")
	})
	// It comes back: closed.
	server.watch.heartbeat(server, testWatchDevice, testBeat(now.Add(6*time.Minute)), now.Add(6*time.Minute))
	if rules := openWatchRules(server); len(rules) != 0 {
		t.Fatal(rules)
	}

	// A restart it said was one has ten minutes; a down, until it is up.
	later := now.Add(time.Hour)
	stopping := testBeat(later)
	stopping.Stopping = "restart"
	server.watch.heartbeat(server, testWatchDevice, stopping, later)
	server.checkWatch(later.Add(5 * time.Minute))
	if rules := openWatchRules(server); len(rules) != 0 {
		t.Fatal(rules)
	}
	server.checkWatch(later.Add(11 * time.Minute))
	if rules := openWatchRules(server); len(rules) != 1 {
		t.Fatal(rules)
	}
	server.watch.heartbeat(server, testWatchDevice, testBeat(later.Add(12*time.Minute)), later.Add(12*time.Minute))
	stopping.Stopping = "down"
	server.watch.heartbeat(server, testWatchDevice, stopping, later.Add(13*time.Minute))
	server.checkWatch(later.Add(48 * time.Hour))
	if rules := openWatchRules(server); len(rules) != 0 {
		t.Fatal(rules)
	}
}

func TestWatchHealthRules(t *testing.T) {
	server, pushes := newTestWatch(t)
	now := time.Now()
	send := func(at time.Time, change func(*apiHeartbeat)) apiHeartbeatReply {
		t.Helper()
		beat := testBeat(at)
		if change != nil {
			change(&beat)
		}
		reply, err := server.watch.heartbeat(server, testWatchDevice, beat, at)
		if err != nil {
			t.Fatal(err)
		}
		return reply
	}
	// A part down for three heartbeats in a row; the gateway for ten.
	for i := range 3 {
		send(now.Add(time.Duration(i)*time.Minute), func(beat *apiHeartbeat) {
			beat.Parts["production"] = apiWatchPart{State: "down", Detail: "its box isn't running"}
			beat.Parts["gateway"] = apiWatchPart{State: "down"}
		})
	}
	reply := send(now.Add(3*time.Minute), func(beat *apiHeartbeat) {
		beat.Parts["production"] = apiWatchPart{State: "down"}
		beat.Parts["gateway"] = apiWatchPart{State: "down"}
	})
	if rules := openWatchRules(server); strings.Join(rules, ",") != "part:production" {
		t.Fatal(rules)
	}
	// The team's channel hears of production, not of its own gateway.
	if len(reply.Tell) != 1 || reply.Tell[0].Rule != "part:production" {
		t.Fatalf("%+v", reply.Tell)
	}
	reply = send(now.Add(4*time.Minute), nil)
	if rules := openWatchRules(server); len(rules) != 0 || len(reply.Tell) != 0 {
		t.Fatal(rules, reply.Tell)
	}

	// Disk, OOM, snapshots, restarts.
	reply = send(now.Add(5*time.Minute), func(beat *apiHeartbeat) {
		beat.Disk.Containers = 0.9
		beat.OOMKills = 1
		beat.Snapshot.Last = now.Add(-30 * time.Hour)
		beat.Restarts = 4
		beat.Parts["production"] = apiWatchPart{State: "up", Restarts1h: 4}
	})
	if rules := strings.Join(openWatchRules(server), ","); rules != "production-restarts,serve-restarts,oom,disk,snapshot" {
		t.Fatal(rules)
	}
	if len(reply.Tell) != 4 {
		t.Fatalf("%+v", reply.Tell)
	}
	send(now.Add(6*time.Minute), nil)

	// The key spends more than hi counted: the watch channel only.
	reply = send(now.Add(7*time.Minute), func(beat *apiHeartbeat) {
		usage := 3.5
		beat.KeyUsage, beat.Spend, beat.Window = &usage, 1.2, 60
	})
	if rules := openWatchRules(server); strings.Join(rules, ",") != "key" || len(reply.Tell) != 0 {
		t.Fatal(rules, reply.Tell)
	}
	if incident := server.watch.view().Incidents[0]; !strings.Contains(incident.Title, "$2.30 outside hi") || !strings.Contains(incident.Detail, "hi team key payments openrouter") {
		t.Fatalf("%+v", incident)
	}
	if len(*pushes) != 0 {
		t.Fatal(*pushes)
	}
	send(now.Add(8*time.Minute), nil)

	// Spend: only with a day of hours to average, and over $5.
	for hour := 1; hour <= 25; hour++ {
		send(now.Add(time.Duration(hour)*time.Hour), func(beat *apiHeartbeat) { beat.Spend = 1 })
	}
	if rules := openWatchRules(server); len(rules) != 0 {
		t.Fatal(rules)
	}
	send(now.Add(26*time.Hour), func(beat *apiHeartbeat) { beat.Spend = 9 })
	if rules := openWatchRules(server); strings.Join(rules, ",") != "spend" {
		t.Fatal(rules)
	}
}

func TestWatchDeadmanAndPushes(t *testing.T) {
	server, _ := newTestWatch(t)
	var mu sync.Mutex
	pinged := 0
	server.watch.ping = func(url string) error {
		mu.Lock()
		defer mu.Unlock()
		if url == "https://hc-ping.com/abc" {
			pinged++
		}
		return nil
	}
	server.watch.config.Deadman = "https://hc-ping.com/abc"
	now := time.Now()
	server.checkWatch(now)
	server.checkWatch(now.Add(30 * time.Second))
	server.checkWatch(now.Add(61 * time.Second))
	teamTestEventually(t, "not pinged each minute", func() bool { mu.Lock(); defer mu.Unlock(); return pinged == 2 })

	// A push goes to the team's topic with the token, and says little.
	var got *http.Request
	var body string
	ntfy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		got, body = r, string(data)
	}))
	defer ntfy.Close()
	config := watchConfig{Ntfy: ntfy.URL, NtfyToken: "tk_secret"}
	if err := watchPushNtfy(config, config.topic("payments"), "hi watch: payments", "payments has sent no heartbeat for 4 minutes (w-1)"); err != nil {
		t.Fatal(err)
	}
	if got.URL.Path != "/hi-watch-payments" || got.Header.Get("Authorization") != "Bearer tk_secret" || got.Header.Get("Title") != "hi watch: payments" || body != "payments has sent no heartbeat for 4 minutes (w-1)" {
		t.Fatal(got.URL.Path, got.Header, body)
	}

	// The settings, through the admin socket's routes.
	admin := httptest.NewServer(server.adminHandler())
	defer admin.Close()
	http.Post(admin.URL+"/admin/watch/config", "application/json", strings.NewReader(`{"ntfy":"https://ntfy.example","ntfy_token":"t","deadman":"off"}`))
	if server.watch.config.Deadman != "" || server.watch.config.Ntfy != "https://ntfy.example" {
		t.Fatalf("%+v", server.watch.config)
	}
	response, _ := http.Get(admin.URL + "/admin/watch/subscribe/payments")
	data, _ := io.ReadAll(response.Body)
	if !strings.Contains(string(data), "https://ntfy.example/hi-watch-payments") {
		t.Fatal(string(data))
	}
	var text strings.Builder
	printWatchView(server.watch.view(), now, &text)
	if !strings.Contains(text.String(), "nobody would know if this server stopped") {
		t.Fatal(text.String())
	}
}
