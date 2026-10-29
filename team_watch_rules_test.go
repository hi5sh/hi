package main

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// testWatchRig runs a team's watcher on heartbeats, as its broker does.
type testWatchRig struct{ watch *teamWatcher }

func newTestWatch(t *testing.T) *testWatchRig {
	t.Helper()
	return &testWatchRig{watch: openTeamWatcher(t.TempDir())}
}

// beat runs the rules on a heartbeat of team payments.
func (r *testWatchRig) beat(beat apiHeartbeat, at time.Time) apiHeartbeatReply {
	w := r.watch
	w.mu.Lock()
	defer w.mu.Unlock()
	team := w.state.Teams[beat.Team]
	if team == nil {
		team = &watchTeam{Team: beat.Team, First: at}
		w.state.Teams[beat.Team] = team
	}
	reply, _, _, _ := w.beatLocked(team, beat, at)
	w.saveLocked()
	return reply
}

// team is the watcher's view of team payments.
func (r *testWatchRig) team() watchTeam {
	r.watch.mu.Lock()
	defer r.watch.mu.Unlock()
	return *r.watch.state.Teams["payments"]
}

func testBeat(at time.Time) apiHeartbeat {
	return apiHeartbeat{Team: "payments", At: at, Hi: "v1.4.0", Production: "v3",
		Parts:    map[string]apiWatchPart{"broker": {State: "up"}, "lead": {State: "up"}, "gateway": {State: "up"}, "production": {State: "up"}, "memory": {State: "off"}},
		Disk:     apiWatchDisk{Team: 0.4, Containers: 0.5, Inodes: 0.1},
		Snapshot: apiWatchSnapshot{Last: at.Add(-time.Hour)}}
}

// openWatchRules are the open incidents' rules, oldest first.
func openWatchRules(r *testWatchRig) []string {
	r.watch.mu.Lock()
	defer r.watch.mu.Unlock()
	var open []*watchIncident
	for _, incident := range r.watch.state.Incidents {
		if incident.Closed.IsZero() {
			open = append(open, incident)
		}
	}
	sort.Slice(open, func(i, j int) bool { return watchIncidentNumber(open[i].ID) < watchIncidentNumber(open[j].ID) })
	rules := []string{}
	for _, incident := range open {
		rules = append(rules, incident.Rule)
	}
	return rules
}

func watchIncidentNumber(id string) int {
	n := 0
	for _, c := range strings.TrimPrefix(id, "w-") {
		n = n*10 + int(c-'0')
	}
	return n
}

func TestWatchKeepsItsPlaceInTheLogs(t *testing.T) {
	rig := newTestWatch(t)
	now := time.Now()
	beat := testBeat(now)
	beat.Network = &apiWatchNetwork{From: 0, To: 44, Lines: []string{"2026-10-22T09:13:02Z allowed api.github.com"}}
	if reply := rig.beat(beat, now); reply.NetworkKept != 44 {
		t.Fatal(reply)
	}
	// The same lines again are read once.
	if reply := rig.beat(beat, now); reply.NetworkKept != 44 {
		t.Fatal(reply)
	}
	beat.Network = &apiWatchNetwork{From: 44, To: 70, Lines: []string{"2026-10-22T09:14:00Z spend 0.02"}}
	rig.beat(beat, now)
	// Rotated: read from 0 again.
	beat.Network = &apiWatchNetwork{From: 0, To: 20, Rotated: true, Lines: []string{"2026-10-22T10:00:00Z x"}}
	if reply := rig.beat(beat, now); reply.NetworkKept != 20 {
		t.Fatal(reply)
	}
	// It reads the logs where they are, and keeps no copy.
	if entries, _ := os.ReadDir(filepath.Join(rig.watch.dir, "teams", "payments")); len(entries) != 0 {
		t.Fatal(entries)
	}
}

func TestWatchHealthRules(t *testing.T) {
	rig := newTestWatch(t)
	now := time.Now()
	send := func(at time.Time, change func(*apiHeartbeat)) {
		t.Helper()
		beat := testBeat(at)
		if change != nil {
			change(&beat)
		}
		rig.beat(beat, at)
	}
	// A part down for three checks in a row; the gateway for ten.
	for i := range 4 {
		send(now.Add(time.Duration(i)*time.Minute), func(beat *apiHeartbeat) {
			beat.Parts["production"] = apiWatchPart{State: "down", Detail: "its box isn't running"}
			beat.Parts["gateway"] = apiWatchPart{State: "down"}
		})
	}
	if rules := openWatchRules(rig); strings.Join(rules, ",") != "part:production" {
		t.Fatal(rules)
	}
	send(now.Add(4*time.Minute), nil)
	if rules := openWatchRules(rig); len(rules) != 0 {
		t.Fatal(rules)
	}

	// Disk, OOM, snapshots, restarts.
	send(now.Add(5*time.Minute), func(beat *apiHeartbeat) {
		beat.Disk.Containers = 0.9
		beat.OOMKills = 1
		beat.Snapshot.Last = now.Add(-30 * time.Hour)
		beat.Restarts = 4
		beat.Parts["production"] = apiWatchPart{State: "up", Restarts1h: 4}
	})
	if rules := strings.Join(openWatchRules(rig), ","); rules != "production-restarts,serve-restarts,oom,disk,snapshot" {
		t.Fatal(rules)
	}
	rig.watch.mu.Lock()
	restarts := rig.watch.state.Incidents[rig.watch.state.Teams["payments"].Open["serve-restarts"]]
	rig.watch.mu.Unlock()
	if !strings.Contains(restarts.Title, "restarted on its own 4 times in a day") {
		t.Fatal(restarts.Title)
	}
	send(now.Add(6*time.Minute), nil)

	// The key spends more than hi counted.
	send(now.Add(7*time.Minute), func(beat *apiHeartbeat) {
		usage := 3.5
		beat.KeyUsage, beat.Spend, beat.Window = &usage, 1.2, 60
	})
	if rules := openWatchRules(rig); strings.Join(rules, ",") != "key" {
		t.Fatal(rules)
	}
	rig.watch.mu.Lock()
	key := rig.watch.state.Incidents[rig.watch.state.Teams["payments"].Open["key"]]
	rig.watch.mu.Unlock()
	if !strings.Contains(key.Title, "$2.30 outside hi") || !strings.Contains(key.Detail, "hi team key payments openrouter") {
		t.Fatalf("%+v", key)
	}
	send(now.Add(8*time.Minute), nil)

	// Spend: only with a day of hours to average, and over $5.
	for hour := 1; hour <= 25; hour++ {
		send(now.Add(time.Duration(hour)*time.Hour), func(beat *apiHeartbeat) { beat.Spend = 1 })
	}
	if rules := openWatchRules(rig); len(rules) != 0 {
		t.Fatal(rules)
	}
	send(now.Add(26*time.Hour), func(beat *apiHeartbeat) { beat.Spend = 9 })
	if rules := openWatchRules(rig); strings.Join(rules, ",") != "spend" {
		t.Fatal(rules)
	}
}
