package main

import (
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
	os.WriteFile(teamServeStartsPath("payments"), []byte(now.Add(-2*time.Hour).UTC().Format(time.RFC3339)+"\n"+now.Add(-time.Minute).UTC().Format(time.RFC3339)+"\n"), 0o600)
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

// A team sends signed heartbeats to its hi server; what the server says the
// lead can act on opens an incident in the team, and closes with it.
func TestTeamHeartbeatReachesTheServer(t *testing.T) {
	ts := newTestServer(t)
	ts.connectAs(t, "anna", "staff")
	key, err := loadDeviceKey(false)
	if err != nil {
		t.Fatal(err)
	}
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	teamTestWakes(b.teamBroker)
	b.channel = &fakeTeamChannel{}
	saved, savedMachine := teamWatchClient, teamWatchMachine
	t.Cleanup(func() { teamWatchClient, teamWatchMachine = saved, savedMachine })
	teamWatchClient = func() (*serverClient, string) { return newServerClient(ts.url, key), ts.url }
	probe := &teamTestProbe{states: map[string]string{"hi-box-team-payments": "running"}, disk: 0.5}
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
	if line := teamWatchLine("payments"); !strings.HasPrefix(line, "Watched by "+ts.url+": last heartbeat") {
		t.Fatal(line)
	}

	// A disk the server finds too full is the lead's to deal with.
	probe.disk = 0.95
	b.sendHeartbeat("")
	incidents := loadTeamIncidents("payments")
	if len(incidents) != 1 || incidents[0].Kind != "watch" || !strings.Contains(incidents[0].Title, "disk is 95% full") {
		t.Fatalf("%+v", incidents)
	}
	b.sendHeartbeat("")
	if len(loadTeamIncidents("payments")) != 1 {
		t.Fatal("told twice")
	}
	probe.disk = 0.5
	b.sendHeartbeat("")
	if incident, _ := loadTeamIncident("payments", incidents[0].ID); incident.Status != "closed" {
		t.Fatalf("%+v", incident)
	}

	// hi team down: the server stops waiting for it.
	b.sendHeartbeat("down")
	view := ts.server.watch.view()
	if len(view.Teams) != 1 || view.Teams[0].Stopping != "down" || view.Teams[0].Hostname == "" {
		t.Fatalf("%+v", view.Teams)
	}
}
