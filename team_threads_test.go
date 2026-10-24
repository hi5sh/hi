package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// After its first turn, the coder's fix round and the reviewer's second
// review are each the next turn of their conversation: what's new, not
// everything again.
func TestTeamSessionsHandover(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	b.reviewRounds, b.sessions = teamReviewRounds, true
	b.reports["payments-t-2"] = `{"status": "complete", "summary": "Changes needed: no tests.", "follow_ups": ["Test the empty list.", "Verify on the preview: the real list"]}`
	b.reports["payments-t-4"] = `{"status": "complete", "summary": "Approve: tested."}`
	if _, err := b.startTask(teamRequest{Role: "coder", Brief: "Add a supplier list", Small: true}); err != nil {
		t.Fatal(err)
	}
	steps := waitTeamSteps(t, "t-1")
	if len(steps) != 4 {
		t.Fatalf("%+v", steps)
	}
	first, review, fix, second := steps[0], steps[1], steps[2], steps[3]
	if first.Session != teamSessionDir("payments", "t-1", "coder") || first.Resumes {
		t.Fatalf("%+v", first)
	}
	// The first steps get everything, as before.
	if brief := b.brief(first); !strings.Contains(brief, "## Your role on this team") && !strings.Contains(brief, "docs/map.md is the project's map") {
		t.Fatal(brief)
	}
	if review.Resumes || review.Session != teamSessionDir("payments", "t-1", "reviewer") {
		t.Fatalf("%+v", review)
	}
	// The fix goes on with the coder's conversation.
	brief := b.brief(fix)
	if !fix.Resumes || fix.Session != first.Session {
		t.Fatalf("%+v", fix)
	}
	for _, unwanted := range []string{"docs/map.md is the project's map", "## How this work started", "Verify on the preview"} {
		if strings.Contains(brief, unwanted) {
			t.Fatalf("the fix's turn repeats %q:\n%s", unwanted, brief)
		}
	}
	for _, want := range []string{"Fix what review t-2 found in t-1", "- Test the empty list.", "You continue task t-1"} {
		if !strings.Contains(brief, want) {
			t.Fatalf("the fix's turn lacks %q:\n%s", want, brief)
		}
	}
	if hint := strings.Join(fix.Hints, "\n"); !strings.Contains(hint, "goes on with its conversation from t-1") {
		t.Fatal(fix.Hints)
	}
	// The second review is the reviewer's next turn: the changes since.
	brief = b.brief(second)
	if !second.Resumes {
		t.Fatalf("%+v", second)
	}
	for _, want := range []string{"answered your review t-2 with task t-3", "git diff " + review.Commit + "..HEAD", "This is review 2"} {
		if !strings.Contains(brief, want) {
			t.Fatalf("the review's turn lacks %q:\n%s", want, brief)
		}
	}
	if strings.Contains(brief, "Check that:") {
		t.Fatal(brief)
	}
	if !strings.Contains(strings.Join(b.calls, "\n"), "--session "+teamSessionDir("payments", "t-1", "reviewer")) {
		t.Fatal(b.calls)
	}

	// A resumed run that fails sets its session aside: the next step
	// starts a new conversation, with the whole brief and where the work
	// stood.
	fix.Status = "failed"
	b.sessionFailed(fix)
	if fileExists(filepath.Join(first.Session, agentSessionStateFile)) {
		t.Fatal("the failed session is still there")
	}
	saveTeamTask("payments", teamTask{ID: "t-3", Role: "coder", Agent: "claude", Kind: "task", On: "t-1", Status: "done", Branch: fix.Branch, Box: fix.Box, Round: 1,
		Report: &agentReport{Status: "done", Text: "Tested the empty list.", Report: []byte(`{"status":"complete","summary":"Tested."}`)}, Created: fix.Created})
	os.Remove(filepath.Join(first.Session, agentSessionStateFile))
	more := teamTestSmall(t, b, teamRequest{Brief: "Sort the list", On: "t-1"})
	brief = b.brief(more)
	if more.Resumes || !strings.Contains(brief, "## Where the work stands") || !strings.Contains(brief, "Tested the empty list.") {
		t.Fatalf("%+v\n%s", more.Hints, brief)
	}
}

func TestTeamPruneSessions(t *testing.T) {
	setupTeamTest(t)
	newTeamTestBroker(t)
	now := time.Now()
	saveTeamTask("payments", teamTask{ID: "t-1", Kind: "task", Status: "done", Finished: now.Add(-10 * 24 * time.Hour)})
	saveTeamTask("payments", teamTask{ID: "t-2", Kind: "review", Review: "t-1", Status: "done", Finished: now.Add(-10 * 24 * time.Hour)})
	saveTeamTask("payments", teamTask{ID: "t-3", Kind: "task", On: "t-1", Status: "merged", Finished: now.Add(-9 * 24 * time.Hour)})
	saveTeamTask("payments", teamTask{ID: "t-4", Kind: "task", Status: "merged", Finished: now.Add(-2 * 24 * time.Hour)})
	saveTeamTask("payments", teamTask{ID: "t-5", Kind: "task", Status: "done", Finished: now.Add(-20 * 24 * time.Hour)})
	for _, root := range []string{"t-1", "t-4", "t-5"} {
		os.MkdirAll(teamSessionDir("payments", root, "coder"), 0o700)
	}
	if err := teamPruneSessions("payments", now); err != nil {
		t.Fatal(err)
	}
	for root, kept := range map[string]bool{"t-1": false, "t-4": true, "t-5": true} {
		if fileExists(teamSessionDir("payments", root, "coder")) != kept {
			t.Errorf("%s's sessions kept: %v, want %v", root, !kept, kept)
		}
	}
}
