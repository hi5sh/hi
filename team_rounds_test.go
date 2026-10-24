package main

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestTeamDropThinking(t *testing.T) {
	text, found := teamDropThinking("Send “Ultrathink” to coder. Think harder, think very hard; we think hardly ever.")
	if text != "Send “(thinking keyword: first task only)” to coder. (thinking keyword: first task only), (thinking keyword: first task only); we think hardly ever." {
		t.Fatal(text)
	}
	if strings.Join(found, ",") != "ultrathink,think harder,think very hard" {
		t.Fatal(found)
	}
	if _, found := teamDropThinking("nothing to see"); found != nil {
		t.Fatal(found)
	}
}

// A person's thinking keyword reaches the first task started on it, and
// no other: not the next yes in the thread, not the lead's own text, not a
// fix round, not a review.
func TestTeamThinkingKeywordOnce(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	b.channel = &fakeTeamChannel{}
	now := time.Now()
	ts := func(minutes int) string { return teamSlackTS(now.Add(-time.Duration(minutes) * time.Minute)) }
	root := ts(30)
	os.MkdirAll(teamChannelDir("payments"), 0o700)
	teamAppendChannel(teamChannelDir("payments"), []teamChannelMessage{
		{TS: root, User: "U07OWNER1", Name: "Owner", Text: "A backtest view, please. ultrathink"},
		{TS: ts(29), Thread: root, Name: "team-payments", Bot: true, Text: "The coder will get “ultrathink”. Shall I start?"},
		{TS: ts(28), Thread: root, User: "U07OWNER1", Name: "Owner", Text: "Yes"},
		{TS: ts(10), Thread: root, User: "U07OWNER1", Name: "Owner", Text: "Now download the index's prices"},
		{TS: ts(9), Thread: root, Name: "team-payments", Bot: true, Text: "Shall I?"},
		{TS: ts(8), Thread: root, User: "U07OWNER1", Name: "Owner", Text: "Yes please"},
	})
	first := teamTestSmall(t, b, teamRequest{Brief: "Build the backtest\n\nultrathink", Approval: ts(28)})
	if brief := b.brief(first); strings.Count(strings.ToLower(brief), "ultrathink") < 2 || strings.Contains(brief, teamThinkingDropped) || len(first.Hints) != 0 {
		t.Fatalf("%v\n%s", first.Hints, brief)
	}

	// A fix of it: none, and the lead hears why.
	more := teamTestSmall(t, b, teamRequest{Brief: "Fix the chart\n\nultrathink", On: first.ID})
	if brief := b.brief(more); strings.Contains(strings.ToLower(brief), "ultrathink") || !strings.Contains(brief, "Fix the chart\n\n"+teamThinkingDropped) {
		t.Fatal(brief)
	}
	if hint := strings.Join(more.Hints, "\n"); !strings.Contains(hint, `hi took "ultrathink" out of this brief`) {
		t.Fatal(more.Hints)
	}
	// Its review: none.
	review, err := b.startReview(teamRequest{Task: more.ID})
	if err != nil {
		t.Fatal(err)
	}
	review = waitTeamTask(t, review.ID)
	if brief := b.brief(review); strings.Contains(strings.ToLower(brief), "ultrathink") {
		t.Fatal(brief)
	}
	// The next yes in the thread, separate work: the keyword was given
	// already, so neither the thread nor the lead's copy carries it.
	next := teamTestSmall(t, b, teamRequest{Brief: "Download the prices\n\nultrathink", Approval: ts(8), New: true})
	if brief := b.brief(next); strings.Contains(strings.ToLower(brief), "ultrathink") {
		t.Fatal(brief)
	}
}

// A small fix waits for work under way, or for a yes.
func TestTeamSmallWaitsForWorkUnderWay(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	saveTeamTask("payments", teamTask{ID: "t-9", Role: "coder", Kind: "task", Status: "running", Title: "Download the prices"})
	_, err := b.startTask(teamRequest{Role: "coder", Brief: "Fix a flaky test", Small: true})
	if err == nil || !strings.Contains(err.Error(), "t-9 (Download the prices) is under way") || !strings.Contains(err.Error(), "--approval") {
		t.Fatal(err)
	}
	saveTeamTask("payments", teamTask{ID: "t-9", Role: "coder", Kind: "task", Status: "done", Title: "Download the prices"})
	teamTestSmall(t, b, teamRequest{Brief: "Fix a flaky test"})
}

func TestTeamChangesSinceYes(t *testing.T) {
	yes := &teamApproval{TS: "1.000001"}
	tasks := []teamTask{
		{ID: "t-1", Kind: "task", Approval: yes},
		{ID: "t-2", Kind: "review", Review: "t-1", Status: "done", Verdict: "changes"},
		{ID: "t-3", Kind: "task", On: "t-1"},
		{ID: "t-4", Kind: "review", Review: "t-3", Status: "done", Verdict: "changes"},
		{ID: "t-5", Kind: "task", On: "t-3", Approval: &teamApproval{TS: "2.000001"}},
		{ID: "t-6", Kind: "review", Review: "t-5", Status: "done", Verdict: "changes"},
		{ID: "t-7", Kind: "task", On: "t-5"},
		{ID: "t-8", Kind: "review", Review: "t-7", Status: "done", Verdict: "approve"},
	}
	for id, want := range map[string]int{"t-1": 1, "t-3": 2, "t-5": 1, "t-7": 1} {
		if got := teamChangesSinceYes(tasks, id); got != want {
			t.Errorf("%s: %d, want %d", id, got, want)
		}
	}
}

func TestTeamPreviewChecks(t *testing.T) {
	review := teamTask{ID: "t-4", Kind: "review", Review: "t-3", Status: "done", Verdict: "approve", Round: 1,
		Report: &agentReport{Status: "done", Report: []byte(`{"status":"complete","summary":"Approve: does what was agreed.","follow_ups":["Verify on the preview: the real download fills 20 years","Later: retry a missing day"]}`)}}
	if checks := teamPreviewChecks(review); len(checks) != 1 || checks[0] != "the real download fills 20 years" {
		t.Fatal(checks)
	}
	steps := []teamTask{{ID: "t-3", Kind: "task", Role: "coder", Status: "done", Next: "t-4"}, review}
	var out strings.Builder
	printTeamTaskView(teamTaskView{teamTask: steps[0], Steps: steps}, &out)
	for _, want := range []string{"Merge it with hi team merge t-3 and put it on the preview", "don't start a task to check it", "  - the real download fills 20 years"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("no %q in:\n%s", want, out.String())
		}
	}
	if rules := teamReviewRules(2); !strings.Contains(rules, "This is review 2") || strings.Contains(teamReviewRules(1), "This is review") {
		t.Fatal(rules)
	}
}

// Notes for the lead don't become the coder's changes, and a review with
// only those starts no fix round.
func TestTeamMustChange(t *testing.T) {
	must := teamMustChange([]string{"Test the empty list.", "Verify on the preview: the tab title", "Later: a retry", "Alert: happy-dom's advisory fails make check on main too", "Learned: bun audit runs in make check", " "})
	if len(must) != 1 || must[0] != "Test the empty list." {
		t.Fatal(must)
	}
	review := teamTask{ID: "t-2", Kind: "review", Review: "t-1", Round: 1, Status: "done", Verdict: "changes",
		Report: &agentReport{Status: "done", Report: []byte(`{"status":"complete","summary":"Changes needed: make check fails.","follow_ups":["Alert: happy-dom's advisory fails make check on main too","Verify on the preview: the tab title"]}`)}}
	if brief := teamFixBrief(review, teamTask{ID: "t-1"}, 2); strings.Contains(brief, "Verify on the preview") || strings.Contains(brief, "## What must change") {
		t.Fatal(brief)
	}
	steps := []teamTask{{ID: "t-1", Kind: "task", Role: "coder", Status: "done", Next: "t-2"}, review}
	var out strings.Builder
	printTeamTaskView(teamTaskView{teamTask: steps[0], Steps: steps}, &out)
	if !strings.Contains(out.String(), "The reviewer asks nothing of the coder") {
		t.Fatal(out.String())
	}
	if rules := teamReviewRules(1); !strings.Contains(rules, `"Alert:"`) {
		t.Fatal(rules)
	}
}
