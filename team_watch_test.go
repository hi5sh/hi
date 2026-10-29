package main

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTeamReadNetworkFromAnOffset(t *testing.T) {
	path := filepath.Join(t.TempDir(), "network.log")
	os.WriteFile(path, []byte("a 1\nb 22\nc 333\nunfinished"), 0o600)
	network, more := teamReadNetwork(path, 0, 2)
	if strings.Join(network.Lines, "|") != "a 1|b 22" || network.To != 9 || !more {
		t.Fatalf("%+v %v", network, more)
	}
	network, more = teamReadNetwork(path, 9, 10)
	if strings.Join(network.Lines, "|") != "c 333" || network.From != 9 || network.To != 15 || !more {
		t.Fatalf("%+v %v", network, more)
	}
	// Shorter than where the server is: rotated.
	os.WriteFile(path, []byte("d 4\n"), 0o600)
	network, _ = teamReadNetwork(path, 15, 10)
	if !network.Rotated || network.From != 0 || network.To != 4 || network.Lines[0] != "d 4" {
		t.Fatalf("%+v", network)
	}
}

type teamTestProbe struct {
	states map[string]string
	disk   float64
	oom    map[string]int
	usage  float64
	key    bool
}

func (p *teamTestProbe) probe() teamWatchProbe {
	return teamWatchProbe{
		state:    func(container string) string { return p.states[container] },
		statfs:   func(string) (float64, float64, error) { return p.disk, 0.1, nil },
		graph:    func() string { return "/graph" },
		oom:      func(string) map[string]int { return p.oom },
		keyUsage: func(string) (float64, bool) { return p.usage, p.key },
	}
}

func TestTeamHeartbeatSays(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	now := time.Now()
	run := filepath.Join(teamDir("payments"), "run")
	os.MkdirAll(filepath.Join(run, "proxy"), 0o700)
	saveTeamSlot("payments", teamPreview{Slot: teamSlotProduction, Container: "box-prod", Label: "production v3"})
	saveTeamVersions("payments", teamVersions{Production: 3})
	os.WriteFile(filepath.Join(teamDir("payments"), "lead", "gateway_state.json"),
		[]byte(`{"gateway_state":"running","updated_at":"`+now.UTC().Format(time.RFC3339)+`","platforms":{"slack":{"state":"connected"}}}`), 0o600)
	os.WriteFile(teamServeStartsPath("payments"), []byte("unplanned "+now.Add(-2*time.Hour).UTC().Format(time.RFC3339)+"\nplanned "+now.Add(-time.Minute).UTC().Format(time.RFC3339)+"\n"), 0o600)
	// Spend through hi: the app's calls, and the lead's in the network log.
	os.WriteFile(teamSpendLogPath("payments"), []byte(now.Add(-10*time.Minute).UTC().Format(time.RFC3339)+" production 0.5\n"+now.Add(-3*time.Hour).UTC().Format(time.RFC3339)+" production 9\n"), 0o600)
	os.WriteFile(teamNetworkLogPath("payments"), []byte(now.Add(-5*time.Minute).UTC().Format(time.RFC3339)+" spend 0.25\n"+now.UTC().Format(time.RFC3339)+" openrouter /api/v1/chat\n"), 0o600)

	probe := &teamTestProbe{states: map[string]string{"hi-box-team-payments": "running", "hi-box-team-payments-proxy": "running", "box-prod": "running"},
		disk: 0.42, oom: map[string]int{"box-prod": 0}, usage: 10, key: true}
	state := teamWatchState{}
	beat := b.heartbeat(&state, probe.probe(), now.Add(-30*time.Minute))
	if beat.KeyUsage != nil {
		t.Fatal("a key usage from one sample")
	}
	probe.oom["box-prod"] = 2
	probe.usage = 11
	beat = b.heartbeat(&state, probe.probe(), now)
	for name, want := range map[string]string{"broker": "up", "lead": "up", "proxy": "up", "gateway": "up", "memory": "off", "production": "up"} {
		if beat.Parts[name].State != want {
			t.Errorf("%s: %+v", name, beat.Parts[name])
		}
	}
	if beat.Production != "v3" || beat.Restarts != 1 || beat.Disk.Team != 0.42 || beat.Disk.Containers != 0.42 || beat.OOMKills != 2 {
		t.Fatalf("%+v", beat)
	}
	if beat.KeyUsage == nil || *beat.KeyUsage != 1 || beat.Window != 30 || beat.Spend != 0.75 {
		t.Fatalf("key %v, spend %v over %d minutes", beat.KeyUsage, beat.Spend, beat.Window)
	}

	// A gateway whose Slack is gone, and production that doesn't answer.
	os.WriteFile(filepath.Join(teamDir("payments"), "lead", "gateway_state.json"),
		[]byte(`{"gateway_state":"running","updated_at":"`+now.UTC().Format(time.RFC3339)+`","platforms":{"slack":{"state":"retrying","error_message":"socket closed"}}}`), 0o600)
	saveTeamJobsState("payments", teamJobsState{Silent: now.Add(-10 * time.Minute)})
	beat = b.heartbeat(&state, probe.probe(), now)
	if beat.Parts["gateway"].Detail != "Slack is retrying: socket closed" || beat.Parts["production"].State != "down" {
		t.Fatalf("%+v", beat.Parts)
	}
}

// A team watches itself: the rules run on its heartbeat in its own
// broker, and what they open is an incident in the team, closed with it.
func TestTeamWatchesItself(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	teamTestWakes(b.teamBroker)
	channel := &fakeTeamChannel{}
	b.channel = channel
	savedMachine := teamWatchMachine
	t.Cleanup(func() { teamWatchMachine = savedMachine })
	probe := &teamTestProbe{states: map[string]string{"hi-box-team-payments": "running", "hi-box-team-payments-proxy": "running"}, disk: 0.5}
	teamWatchMachine = probe.probe()
	os.MkdirAll(filepath.Dir(teamNetworkLogPath("payments")), 0o700)
	os.WriteFile(teamNetworkLogPath("payments"), []byte("2026-10-22T09:13:02Z allowed api.github.com\n"), 0o600)

	if _, err := b.sendHeartbeat(""); err != nil {
		t.Fatal(err)
	}
	state := loadTeamWatchState("payments")
	if state.Kept != 44 || state.Sent.IsZero() {
		t.Fatalf("%+v", state)
	}
	if line := teamWatchLine("payments"); !strings.HasPrefix(line, "Watcher: on this machine, last checked") {
		t.Fatal(line)
	}
	if fileExists(filepath.Join(teamDir("payments"), "watch", "teams", "payments", "network.log")) {
		t.Fatal("the watcher copied the log it reads")
	}

	// A disk too full is an incident here, once, and closes when it has
	// room again.
	probe.disk = 0.95
	b.sendHeartbeat("")
	incidents := loadTeamIncidents("payments")
	if len(incidents) != 1 || incidents[0].Kind != "watch" || !strings.Contains(incidents[0].Title, "disk is 95% full") {
		t.Fatalf("%+v", incidents)
	}
	b.sendHeartbeat("")
	if len(loadTeamIncidents("payments")) != 1 {
		t.Fatalf("told twice: %+v", loadTeamIncidents("payments"))
	}
	probe.disk = 0.5
	b.sendHeartbeat("")
	if incident, _ := loadTeamIncident("payments", incidents[0].ID); incident.Status != "closed" {
		t.Fatalf("%+v", incident)
	}
	// hi team down sends nothing anywhere.
	if _, err := b.sendHeartbeat("down"); err != nil {
		t.Fatal(err)
	}
}

// What the hi server told a team before it watched itself is closed at
// the team's first check, and opens again here if it still holds.
func TestTeamWatchTakesOverFromTheServer(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	teamTestWakes(b.teamBroker)
	b.channel = &fakeTeamChannel{}
	savedMachine := teamWatchMachine
	t.Cleanup(func() { teamWatchMachine = savedMachine })
	teamWatchMachine = (&teamTestProbe{states: map[string]string{"hi-box-team-payments": "running"}, disk: 0.95}).probe()
	told, _ := b.openIncident(teamIncident{Kind: "watch", Slot: teamSlotProduction, Title: "payments's disk is 95% full"}, "x", "", nil)
	saveTeamWatchState("payments", teamWatchState{Server: "https://hi.example", Told: map[string]string{"w-1": told.ID}})
	b.sendHeartbeat("")
	if incident, _ := loadTeamIncident("payments", told.ID); incident.Status != "closed" {
		t.Fatalf("%+v", incident)
	}
	open := 0
	for _, incident := range loadTeamIncidents("payments") {
		if incident.Status != "closed" && strings.Contains(incident.Title, "disk is 95% full") {
			open++
		}
	}
	if state := loadTeamWatchState("payments"); open != 1 || state.Server != "" || len(state.Told) != 1 {
		t.Fatal(open, state)
	}
}

// A hi server turns away what a team from before hi 2.6.32 sends.
func TestServerTurnsHeartbeatsAway(t *testing.T) {
	ts := newTestServer(t)
	ts.connectAs(t, "anna", "staff")
	key, _ := loadDeviceKey(false)
	for _, path := range []string{"/v1/watch/heartbeat", "/v1/watch/bom", "/v1/watch/key"} {
		err := newServerClient(ts.url, key).call(http.MethodPost, path, apiHeartbeat{Team: "payments"}, nil)
		if err == nil || !strings.Contains(err.Error(), "watch themselves") {
			t.Fatal(path, err)
		}
	}
}

// Only the starts nobody asked for count: a stop systemd asked for (an
// update, hi team up, hi team down) leaves a mark the next start reads.
func TestTeamUnplannedRestarts(t *testing.T) {
	setupTeamTest(t)
	newTeamTestBroker(t)
	os.MkdirAll(filepath.Join(teamDir("payments"), "run"), 0o700)
	// The days before: lines that say neither don't count.
	os.WriteFile(teamServeStartsPath("payments"), []byte(time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)+"\n"), 0o600)
	teamNoteServeStart("payments") // no mark: it crashed
	teamNoteServeStop("payments")
	teamNoteServeStart("payments") // an update
	teamNoteServeStop("payments")
	teamNoteServeStart("payments") // hi team up
	teamNoteServeStart("payments") // killed, no mark
	if n := teamUnplannedStarts("payments", time.Now().Add(-24*time.Hour)); n != 2 {
		data, _ := os.ReadFile(teamServeStartsPath("payments"))
		t.Fatalf("%d:\n%s", n, data)
	}
	if fileExists(teamServeStoppedPath("payments")) {
		t.Fatal("the mark stays after the start that read it")
	}
	// A team's very first start isn't a restart.
	os.Remove(teamServeStartsPath("payments"))
	teamNoteServeStart("payments")
	if n := teamUnplannedStarts("payments", time.Now().Add(-time.Hour)); n != 0 {
		t.Fatal(n)
	}
}
