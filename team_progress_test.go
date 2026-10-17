package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTeamProgressCard(t *testing.T) {
	setupTeamTest(t)
	dir := t.TempDir()
	if err := teamRegister("payments", dir); err != nil {
		t.Fatal(err)
	}
	config := testTeamConfig()
	saveTeam(config)
	os.MkdirAll(teamTaskDir("payments"), 0o700)
	start := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	save := func(task teamTask) {
		if err := saveTeamTask("payments", task); err != nil {
			t.Fatal(err)
		}
	}
	yes := &teamApproval{TS: "1792254800.000100", Name: "Anna", Thread: "1792254766.636789"}
	save(teamTask{ID: "t-1", Role: "coder", Agent: "claude", Kind: "task", Title: "Sentiment", Status: "running", Created: start, Approval: yes})
	// Work agreed in a DM has no thread for a card.
	save(teamTask{ID: "t-2", Role: "coder", Agent: "claude", Kind: "task", Title: "Other", Status: "running", Created: start, Approval: &teamApproval{TS: "dm:D1:1792254800.000200"}})

	source := &fakeTeamChannel{}
	b := newTeamBroker("payments", "hi")
	b.channel = source
	readers := map[string]*agentProgressReader{}
	step := func(now time.Time) string {
		t.Helper()
		before := len(source.calls)
		if err := b.updateProgress(readers, now); err != nil {
			t.Fatal(err)
		}
		return strings.Join(source.calls[before:], "\n")
	}

	// Off by default.
	if got := step(start.Add(time.Minute)); got != "" {
		t.Fatal("a card while progress is off:", got)
	}
	var out strings.Builder
	if err := teamProgressCommand([]string{"payments", "on"}, &out); err != nil || !strings.Contains(out.String(), "keeps a progress card") {
		t.Fatal(err, out.String())
	}

	got := step(start.Add(2 * time.Minute))
	if !strings.HasPrefix(got, "startStream C07PAYMENTS 1792254766.636789 U07OWNER1 ") || strings.Count(got, "\n") != 0 {
		t.Fatal(got)
	}
	for _, want := range []string{`"title":"Sentiment · the coder is working on it"`, `"id":"t-1"`, `"status":"in_progress"`, `running 2m`} {
		if !strings.Contains(got, want) {
			t.Errorf("no %s in %s", want, got)
		}
	}

	// The coder is done and the reviewer asks for changes: each line's
	// details go once, since Slack adds them to what it has.
	save(teamTask{ID: "t-1", Role: "coder", Agent: "claude", Kind: "task", Title: "Sentiment", Status: "done", Created: start, Finished: start.Add(9 * time.Minute), Approval: yes})
	save(teamTask{ID: "t-3", Role: "reviewer", Agent: "codex", Kind: "review", Review: "t-1", Title: "Review t-1", Status: "running", Created: start.Add(9 * time.Minute)})
	got = step(start.Add(10 * time.Minute))
	if !strings.HasPrefix(got, "appendStream C07PAYMENTS ") || !strings.Contains(got, `"details":"done in 9m"`) || !strings.Contains(got, "the reviewer is checking") {
		t.Fatal(got)
	}
	got = step(start.Add(11 * time.Minute))
	if strings.Contains(got, "details") {
		t.Fatal("details sent twice:", got)
	}

	// Approved, nothing runs: the card waits for a next round, then ends.
	save(teamTask{ID: "t-3", Role: "reviewer", Agent: "codex", Kind: "review", Review: "t-1", Title: "Review t-1", Status: "done", Verdict: "approve", Created: start.Add(9 * time.Minute), Finished: start.Add(12 * time.Minute)})
	got = step(start.Add(12 * time.Minute))
	if strings.Contains(got, "stopStream") || !strings.Contains(got, `"details":"approved in 3m"`) || !strings.Contains(got, "the lead merges it") {
		t.Fatal(got)
	}
	got = step(start.Add(16 * time.Minute))
	if !strings.HasSuffix(got, "stopStream C07PAYMENTS 1700000000.000001") {
		t.Fatal(got)
	}
	if got = step(start.Add(17 * time.Minute)); got != "" {
		t.Fatal("an ended card for settled work:", got)
	}

	// More work on it later gets a card of its own, with only the new tasks.
	save(teamTask{ID: "t-4", Role: "coder", Agent: "claude", Kind: "task", On: "t-1", Title: "More", Status: "running", Created: start.Add(30 * time.Minute)})
	got = step(start.Add(31 * time.Minute))
	if !strings.HasPrefix(got, "startStream") || strings.Contains(got, `"id":"t-1"`) || !strings.Contains(got, `"id":"t-4"`) {
		t.Fatal(got)
	}

	// Someone stops the card in Slack: no new one for this work.
	source.streamErr = "stopped_by_user"
	step(start.Add(32 * time.Minute))
	source.streamErr = ""
	if got = step(start.Add(33 * time.Minute)); got != "" {
		t.Fatal("a card after someone stopped it:", got)
	}
}

func TestTeamProgressSmallFix(t *testing.T) {
	setupTeamTest(t)
	dir := t.TempDir()
	if err := teamRegister("payments", dir); err != nil {
		t.Fatal(err)
	}
	config := testTeamConfig()
	config.Progress = true
	saveTeam(config)
	os.MkdirAll(teamTaskDir("payments"), 0o700)
	os.MkdirAll(teamChannelDir("payments"), 0o700)
	// The person's message, in a thread, that asked for the fix.
	os.WriteFile(filepath.Join(teamChannelDir("payments"), "2026-10-17.jsonl"),
		[]byte(`{"ts":"1792254900.000100","thread":"1792254766.636789","user":"U07ANNA01","name":"Anna","text":"wrong model"}`+"\n"), 0o600)
	b := newTeamBroker("payments", "hi")
	if _, err := b.teamFindThread(config, "nope"); err == nil {
		t.Fatal("--in took something that isn't an id")
	}
	if _, err := b.teamFindThread(config, "1792254999.000100"); err == nil || !strings.Contains(err.Error(), "--ids") {
		t.Fatal(err)
	}
	thread, err := b.teamFindThread(config, "[1792254900.000100]")
	if err != nil || thread != "1792254766.636789" {
		t.Fatal(thread, err)
	}

	start := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	saveTeamTask("payments", teamTask{ID: "t-1", Role: "coder", Agent: "claude", Kind: "task", Title: "Fix the model", Status: "running", Created: start, Small: true, Thread: thread})
	// A small fix without a thread has no card.
	saveTeamTask("payments", teamTask{ID: "t-2", Role: "coder", Agent: "claude", Kind: "task", Title: "Typo", Status: "running", Created: start, Small: true})
	source := &fakeTeamChannel{}
	b.channel = source
	if err := b.updateProgress(map[string]*agentProgressReader{}, start.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if len(source.calls) != 1 || !strings.HasPrefix(source.calls[0], "startStream C07PAYMENTS 1792254766.636789 U07OWNER1 ") || !strings.Contains(source.calls[0], "Fix the model") {
		t.Fatal(source.calls)
	}
}

func TestTeamProgressClip(t *testing.T) {
	if got := teamProgressClip(strings.Repeat("å", 300)); len([]rune(got)) != 256 || !strings.HasSuffix(got, "…") {
		t.Fatal(len([]rune(got)))
	}
	if teamProgressMinutes(30*time.Second) != "<1m" || teamProgressMinutes(75*time.Minute) != "1h15m" {
		t.Fatal(teamProgressMinutes(75 * time.Minute))
	}
}
