package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// teamTestSmall starts a small fix and waits for it.
func teamTestSmall(t *testing.T, b *teamTestBroker, request teamRequest) teamTask {
	t.Helper()
	request.Role = firstNonEmpty(request.Role, "coder")
	if request.On == "" && request.Approval == "" {
		request.Small = true
	}
	task, err := b.startTask(request)
	if err != nil {
		t.Fatal(err)
	}
	return waitTeamTask(t, task.ID)
}

func TestTeamContinueGoesOnFromTheLatestWork(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t)

	first := teamTestSmall(t, b, teamRequest{Brief: "Add a supplier list"})
	more := teamTestSmall(t, b, teamRequest{Brief: "Sort it by name", On: first.ID})
	if more.On != first.ID || len(more.Hints) != 0 {
		t.Fatalf("%+v", more)
	}
	// The lead says t-1 again; t-2 has the work since, so that's where it goes on.
	again := teamTestSmall(t, b, teamRequest{Brief: "And a search box", On: first.ID})
	if again.On != more.ID || len(again.Hints) != 1 || !strings.Contains(again.Hints[0], "this continues "+more.ID) {
		t.Fatalf("%+v", again)
	}
	if !teamIsAncestor(b.repo, more.Branch, again.Branch) {
		t.Fatal("the continuation left t-2's work out")
	}
	// Still one idea.
	if ideas := teamOpenIdeas(loadTeamTasks("payments")); len(ideas) != 1 || ideas[0].ID != again.ID {
		t.Fatalf("%+v", ideas)
	}

	// One piece of work per idea at a time.
	running := again
	running.Status = "running"
	saveTeamTask("payments", running)
	if _, err := b.startTask(teamRequest{Role: "coder", Brief: "x", On: first.ID}); err == nil || !strings.Contains(err.Error(), "still going ("+again.ID+" is working on it)") {
		t.Fatal(err)
	}
	running.Status = "done"
	saveTeamTask("payments", running)

	// An idea that split in two is refused until one side is dropped.
	fork := teamTask{ID: "t-9", Role: "coder", Agent: "claude", Kind: "task", On: more.ID, Title: "Another way", Branch: more.Branch, Status: "done", Created: time.Now()}
	saveTeamTask("payments", fork)
	if _, err := b.startTask(teamRequest{Role: "coder", Brief: "x", On: first.ID}); err == nil || !strings.Contains(err.Error(), "split into 2 lines") {
		t.Fatal(err)
	}
	fork.Status = "discarded"
	saveTeamTask("payments", fork)

	// A review isn't work to continue.
	review, err := b.startReview(teamRequest{Task: again.ID})
	if err != nil {
		t.Fatal(err)
	}
	waitTeamTask(t, review.ID)
	if _, err := b.startTask(teamRequest{Role: "coder", Brief: "x", On: review.ID}); err == nil || !strings.Contains(err.Error(), "--on "+again.ID) {
		t.Fatal(err)
	}

	// Merged and discarded work say what to do instead.
	discarded := teamTestSmall(t, b, teamRequest{Brief: "An export"})
	if _, err := b.discard(discarded.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := b.startTask(teamRequest{Role: "coder", Brief: "x", On: discarded.ID}); err == nil || !strings.Contains(err.Error(), "was discarded") {
		t.Fatal(err)
	}
	merged := teamTestMerge(t, b, "A search box")
	if _, err := b.startTask(teamRequest{Role: "coder", Brief: "x", On: merged.ID}); err == nil || !strings.Contains(err.Error(), "merged already") {
		t.Fatal(err)
	}
}

func TestTeamOpenIdeasKeepWorkAContinuationNeverStarted(t *testing.T) {
	tasks := []teamTask{
		{ID: "t-1", Kind: "task", Status: "done", Branch: "hi-box/payments-t-1"},
		{ID: "t-2", Kind: "task", On: "t-1", Status: "failed"}, // failed before it had a branch
	}
	if ideas := teamOpenIdeas(tasks); len(ideas) != 1 || ideas[0].ID != "t-1" {
		t.Fatalf("%+v", ideas)
	}
}

func TestTeamNewWorkInTheThreadOfOpenWork(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	channel := &fakeTeamChannel{}
	b.channel = channel

	now := time.Now()
	ts := func(minutes int) string { return teamSlackTS(now.Add(-time.Duration(minutes) * time.Minute)) }
	root := ts(30)
	os.MkdirAll(teamChannelDir("payments"), 0o700)
	teamAppendChannel(teamChannelDir("payments"), []teamChannelMessage{
		{TS: root, User: "U07OWNER1", Name: "Owner", Text: "A supplier list, please"},
		{TS: ts(29), Thread: root, Name: "team-payments", Bot: true, Text: "Shall I start?"},
		{TS: ts(28), Thread: root, User: "U07OWNER1", Name: "Owner", Text: "Yes"},
		{TS: ts(10), Thread: root, User: "U07OWNER1", Name: "Owner", Text: "Can it be sorted by name?"},
		{TS: ts(9), Thread: root, Name: "team-payments", Bot: true, Text: "Shall I?"},
		{TS: ts(8), Thread: root, User: "U07OWNER1", Name: "Owner", Text: "Yes please"},
	})
	first, err := b.startTask(teamRequest{Role: "coder", Brief: "Add a supplier list", Approval: ts(28)})
	if err != nil {
		t.Fatal(err)
	}
	first = waitTeamTask(t, first.ID)
	if first.Approval.Thread != root {
		t.Fatalf("%+v", first.Approval)
	}

	// The later yes in the same thread is more of t-1, so a new task is refused.
	if _, err := b.startTask(teamRequest{Role: "coder", Brief: "Sort the list", Approval: ts(8)}); err == nil || !strings.Contains(err.Error(), "--on "+first.ID) || !strings.Contains(err.Error(), "--new") {
		t.Fatal(err)
	}
	// The lead says it really is separate.
	separate, err := b.startTask(teamRequest{Role: "coder", Brief: "Sort the list", Approval: ts(8), New: true})
	if err != nil {
		t.Fatal(err)
	}
	separate = waitTeamTask(t, separate.ID)
	if len(separate.Hints) != 1 || !strings.Contains(separate.Hints[0], "Not merged yet: "+first.ID) {
		t.Fatalf("%+v", separate.Hints)
	}
	if _, err := b.discard(separate.ID); err != nil {
		t.Fatal(err)
	}

	// Continuing is announced in the thread it was agreed in, and the
	// member gets what the people agreed to.
	more := teamTestSmall(t, b, teamRequest{Brief: "Sort the list by name", On: first.ID})
	if !strings.Contains(strings.Join(channel.calls, "\n"), "postIn C07PAYMENTS "+root+" More on this ("+more.ID+"): Sort the list by name") {
		t.Fatal(channel.calls)
	}
	brief, _ := os.ReadFile(filepath.Join(teamTaskDir("payments"), more.ID+".md"))
	if !strings.Contains(string(brief), "### What the people agreed to ("+first.ID+")") || !strings.Contains(string(brief), "A supplier list, please") {
		t.Fatal(string(brief))
	}
}

func TestTeamContinueAfterDevMoved(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	b.files = map[string]map[string]string{
		"payments-t-1": {"src/list.ts": "the list\n"},
		"payments-t-2": {"src/list.ts": "a different list\n"},
	}
	first := teamTestSmall(t, b, teamRequest{Brief: "Add a supplier list"})
	teamTestMerge(t, b, "Another list")

	// Continuing t-1 tells the coder dev changed the same file.
	more := teamTestSmall(t, b, teamRequest{Brief: "Sort it", On: first.ID})
	brief, _ := os.ReadFile(filepath.Join(teamTaskDir("payments"), more.ID+".md"))
	if !strings.Contains(string(brief), "main has changed since this work began, in files it changes too: src/list.ts. Run git merge main") {
		t.Fatal(string(brief))
	}

	// Merging it conflicts, so hi starts the work to bring it up to date.
	b.reports[fmt.Sprintf("payments-t-%d", teamTaskNumber(more.ID)+1)] = `{"status": "complete", "summary": "Approve."}`
	review, err := b.startReview(teamRequest{Task: more.ID})
	if err != nil {
		t.Fatal(err)
	}
	waitTeamTask(t, review.ID)
	_, err = b.merge(more.ID, "")
	if err == nil || !strings.Contains(err.Error(), "doesn't merge cleanly into main: both changed the same lines in src/list.ts. hi started t-") {
		t.Fatal(err)
	}
	catchUp := waitTeamTask(t, fmt.Sprintf("t-%d", teamTaskNumber(review.ID)+1))
	if catchUp.On != more.ID || !strings.HasPrefix(catchUp.Title, "Bring "+more.ID+" up to date with main") {
		t.Fatalf("%+v", catchUp)
	}
	if boxGit(b.repo, "status", "--porcelain") != "" {
		t.Fatal("the failed merge left changes behind")
	}

	// tasks shows the idea as its line of tasks.
	var out bytes.Buffer
	printTeamTasks(loadTeamTasks("payments"), 30, &out)
	want := fmt.Sprintf("  %s (done) Bring %s up to date with main\n      %s > %s > %s\n", catchUp.ID, more.ID, first.ID, more.ID, catchUp.ID)
	if !strings.Contains(out.String(), "Not merged yet (1 of 5 ideas)") || !strings.Contains(out.String(), want) {
		t.Fatal(out.String())
	}
}

// Lines of work that went on from the same merged task are separate ideas,
// as in a team that forked work before v0.75.0.
func TestTeamIdeasEndAtMergedWork(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	for _, task := range []teamTask{
		{ID: "t-1", Kind: "task", Status: "merged", Branch: "hi-box/payments-t-1", Title: "A list"},
		{ID: "t-2", Kind: "task", On: "t-1", Status: "done", Branch: "hi-box/payments-t-2", Title: "Left behind"},
		{ID: "t-3", Kind: "task", On: "t-1", Status: "merged", Branch: "hi-box/payments-t-3", Title: "Kept"},
		{ID: "t-4", Kind: "task", On: "t-3", Status: "failed", Branch: "hi-box/payments-t-4", Title: "Failed on"},
	} {
		task.Role, task.Agent, task.Created = "coder", "claude", time.Now()
		saveTeamTask("payments", task)
	}
	tasks := loadTeamTasks("payments")
	if ideas := teamOpenIdeas(tasks); len(ideas) != 2 {
		t.Fatalf("%+v", ideas)
	}
	on, hints, err := b.continuing("t-4")
	if err != nil || on != "t-4" || len(hints) != 1 || !strings.Contains(hints[0], "t-4 failed") {
		t.Fatal(on, hints, err)
	}
	var out bytes.Buffer
	printTeamIdeas(tasks, &out)
	if !strings.Contains(out.String(), "  t-2 (done) Left behind\n      t-1 (merged) > t-2\n") || !strings.Contains(out.String(), "      t-3 (merged) > t-4\n") {
		t.Fatal(out.String())
	}
	// Discarding one line leaves the other.
	if _, err := b.discard("t-2"); err != nil {
		t.Fatal(err)
	}
	if four, _ := loadTeamTask("payments", "t-4"); four.Status != "failed" {
		t.Fatalf("t-4 is %s", four.Status)
	}
}
