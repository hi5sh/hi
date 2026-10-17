package main

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestTeamHandoff(t *testing.T) {
	setupTeamTest(t)
	dir := t.TempDir()
	if err := teamRegister("payments", dir); err != nil {
		t.Fatal(err)
	}
	saveTeam(testTeamConfig())
	os.MkdirAll(teamTaskDir("payments"), 0o700)
	start := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	yes := &teamApproval{TS: "1792254800.000100", Thread: "1792254766.636789"}
	// Work that settled before hi kept track: taken as heard.
	saveTeamTask("payments", teamTask{ID: "t-1", Role: "coder", Agent: "claude", Kind: "task", Title: "Old", Status: "done", Created: start, Finished: start.Add(time.Minute), Approval: yes})

	b := newTeamBroker("payments", "hi")
	var woken []string
	b.wakeLead = func(name, prompt, deliver string) error {
		woken = append(woken, name+" "+deliver+" "+prompt)
		return nil
	}
	check := func(now time.Time) {
		t.Helper()
		if err := b.checkHandoffs(now); err != nil {
			t.Fatal(err)
		}
	}
	check(start.Add(10 * time.Minute))
	if len(woken) != 0 {
		t.Fatal("woke the lead for work from before:", woken)
	}

	// New work: the coder, then a review that asks for changes after the
	// rounds, while a restart ended the lead's wait.
	other := &teamApproval{TS: "1792255000.000100", Thread: "1792255000.000100"}
	saveTeamTask("payments", teamTask{ID: "t-2", Role: "coder", Agent: "claude", Kind: "task", Title: "Momentum", Status: "running", Created: start, Approval: other})
	check(start.Add(20 * time.Minute))
	saveTeamTask("payments", teamTask{ID: "t-2", Role: "coder", Agent: "claude", Kind: "task", Title: "Momentum", Status: "done", Created: start, Finished: start.Add(25 * time.Minute), Approval: other})
	saveTeamTask("payments", teamTask{ID: "t-3", Role: "reviewer", Agent: "codex", Kind: "review", Review: "t-2", Round: 3, Title: "Review t-2", Status: "done", Verdict: "changes",
		Created: start.Add(25 * time.Minute), Finished: start.Add(30 * time.Minute)})
	// A wait that is there hands the lead the result itself.
	done := b.waitingOn("t-2")
	check(start.Add(32 * time.Minute))
	if len(woken) != 0 {
		t.Fatal("woke the lead while it waited:", woken)
	}
	done()
	// Within a minute of the end, a wait may still read it.
	check(start.Add(30*time.Minute + 30*time.Second))
	if len(woken) != 0 {
		t.Fatal("woke the lead too soon:", woken)
	}
	check(start.Add(33 * time.Minute))
	if len(woken) != 1 || !strings.HasPrefix(woken[0], "hi-handoff-t-3 slack:C07PAYMENTS:1792255000.000100 ") ||
		!strings.Contains(woken[0], "hi team wait t-2") || !strings.Contains(woken[0], "asked for changes in 5m (review 3)") {
		t.Fatal(woken)
	}
	check(start.Add(34 * time.Minute))
	if len(woken) != 1 {
		t.Fatal("woke the lead twice:", woken)
	}

	// A wait that read the end leaves nothing to wake the lead for.
	saveTeamTask("payments", teamTask{ID: "t-4", Role: "coder", Agent: "claude", Kind: "task", On: "t-2", Title: "Fix", Status: "done", Created: start.Add(40 * time.Minute), Finished: start.Add(45 * time.Minute)})
	b.heard("t-4")
	check(start.Add(50 * time.Minute))
	// Merged work is dealt with.
	saveTeamTask("payments", teamTask{ID: "t-5", Role: "coder", Agent: "claude", Kind: "task", Title: "Merged", Status: "merged", Created: start, Finished: start.Add(time.Minute), Approval: &teamApproval{TS: "1792256000.000100", Thread: "1792256000.000100"}})
	check(start.Add(55 * time.Minute))
	if len(woken) != 1 {
		t.Fatal(woken)
	}
}
