package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// teamTestAskReview logs the lead's offer of a deep review in a thread,
// naming a figure, and someone's yes; it returns the yes's id and the
// thread.
func teamTestAskReview(t *testing.T, user, question string, minutesAgo int) (string, string) {
	t.Helper()
	now := time.Now()
	ts := func(seconds int) string {
		return teamSlackTS(now.Add(-time.Duration(minutesAgo)*time.Minute + time.Duration(seconds)*time.Second))
	}
	root, asked, yes := ts(0), ts(1), ts(2)
	os.MkdirAll(teamChannelDir("payments"), 0o700)
	teamAppendChannel(teamChannelDir("payments"), []teamChannelMessage{
		{TS: root, User: user, Name: user, Text: "We changed how people sign in. Is it safe?"},
		{TS: asked, Thread: root, Name: "team-payments", Bot: true, Text: question},
		{TS: yes, Thread: root, User: user, Name: user, Text: "Yes, go ahead"},
	})
	return yes, root
}

// teamTestReviewState writes hi agent review's record of a review, as
// the review's own process would.
func teamTestReviewState(t *testing.T, rv review, report string) {
	t.Helper()
	os.MkdirAll(reviewStateFile(rv.ID), 0o700)
	if err := saveReview(&rv); err != nil {
		t.Fatal(err)
	}
	if report != "" {
		os.WriteFile(reviewStateFile(rv.ID, "report.md"), []byte(report), 0o600)
		data, _ := json.Marshal(rv)
		os.WriteFile(reviewStateFile(rv.ID, "report.json"), data, 0o600)
	}
}

func TestTeamDeepReview(t *testing.T) {
	root := setupTeamTest(t)
	t.Setenv("XDG_STATE_HOME", filepath.Join(root, "state"))
	b := newTeamTestBroker(t)
	channel := &fakeTeamChannel{}
	b.channel = channel
	teamDeepReviewPoll = 10 * time.Millisecond
	t.Cleanup(func() { teamDeepReviewPoll = 30 * time.Second })

	var mu sync.Mutex
	var reviews []string
	var woken []string
	b.wakeLead = func(name, prompt, deliver string) error {
		mu.Lock()
		defer mu.Unlock()
		woken = append(woken, name+"\n"+deliver+"\n"+prompt)
		return nil
	}
	fake := b.run
	b.run = func(args ...string) ([]byte, error) {
		if len(args) > 1 && args[0] == "agent" && args[1] == "review" {
			mu.Lock()
			reviews = append(reviews, strings.Join(args, " "))
			id := "r-" + string(rune('0'+len(reviews)))
			mu.Unlock()
			rv := review{ID: id, Root: b.repo, State: "running", PID: os.Getpid(), Created: time.Now().UTC(), Areas: []string{"security", "correctness"}}
			teamTestReviewState(t, rv, "")
			return json.Marshal(rv)
		}
		return fake(args...)
	}
	ask := func(request teamDeepReviewRequest) (teamDeepReview, error) { return b.startDeepReview(request) }

	// The lead's question has to name the budget it passes.
	yes, _ := teamTestAskReview(t, "U07OWNER1", "Want a deep review before we release? It changes nothing.", 30)
	if _, err := ask(teamDeepReviewRequest{Approval: yes, Budget: 30}); err == nil || !strings.Contains(err.Error(), "doesn't name $30") {
		t.Fatalf("a yes to a question without a figure: %v", err)
	}
	yes, _ = teamTestAskReview(t, "U07OWNER1", "Want a deep review? It can cost up to $20.", 25)
	if _, err := ask(teamDeepReviewRequest{Approval: yes, Budget: 30}); err == nil || !strings.Contains(err.Error(), "doesn't name $30") {
		t.Fatalf("a yes to another figure: %v", err)
	}
	// Only an owner's yes.
	config, _ := loadTeam("payments")
	config.Askers = []string{"U07ASKER1"}
	saveTeam(config)
	yes, _ = teamTestAskReview(t, "U07ASKER1", "Want a deep review? Up to $30.", 20)
	if _, err := ask(teamDeepReviewRequest{Approval: yes, Budget: 30}); err == nil || !strings.Contains(err.Error(), "owners") {
		t.Fatalf("an asker's yes: %v", err)
	}
	if len(reviews) != 0 {
		t.Fatalf("a review started without an owner's yes to its budget: %v", reviews)
	}

	yes, thread := teamTestAskReview(t, "U07OWNER1", "We changed how people sign in. Want a deep review before we release? It can cost up to $30 of the month, and changes nothing.", 10)
	record, err := ask(teamDeepReviewRequest{Approval: yes, Budget: 30, Context: "Only the three of us use it, and it holds suppliers' bank details."})
	if err != nil {
		t.Fatal(err)
	}
	main := boxGit(b.repo, "rev-parse", "main")
	if record.ID != "r-1" || record.Owner != "U07OWNER1" || record.Commit != main || record.Status != "running" {
		t.Fatalf("record: %+v", record)
	}
	args := reviews[0]
	for _, want := range []string{"agent review " + b.repo, "--ref " + main, "--budget 30.00", "--agents claude:opus,codex", "--yes --detach --json", "--context "} {
		if !strings.Contains(args, want) {
			t.Errorf("hi agent review lacks %q: %s", want, args)
		}
	}
	context, _ := os.ReadFile(filepath.Join(teamReviewsDir("payments"), "context.md"))
	for _, want := range []string{"Track supplier payments", "Production isn't running", "suppliers' bank details", "1 owner, and 1 other person may ask for changes"} {
		if !strings.Contains(string(context), want) {
			t.Errorf("the context lacks %q:\n%s", want, context)
		}
	}
	if !strings.Contains(strings.Join(channel.calls, "\n"), "postIn C07PAYMENTS "+thread+" Deep review r-1 started on dev") {
		t.Errorf("no word in the thread: %v", channel.calls)
	}
	// Production's key stays out of the context; that a key guards it
	// doesn't.
	data, _ := json.Marshal(teamPreview{Slot: teamSlotProduction, URL: "https://payments.app.example.net/?hi_key=s3cret-key"})
	os.MkdirAll(filepath.Dir(teamSlotPath("payments", teamSlotProduction)), 0o700)
	os.WriteFile(teamSlotPath("payments", teamSlotProduction), data, 0o600)
	versions, _ := loadTeamVersions("payments")
	context = []byte(b.deepReviewContext(config, versions, ""))
	if strings.Contains(string(context), "s3cret-key") || strings.Contains(string(context), "hi_key") ||
		!strings.Contains(string(context), "https://payments.app.example.net/,") && !strings.Contains(string(context), "https://payments.app.example.net/ ") ||
		!strings.Contains(string(context), "carries a secret key") {
		t.Errorf("the context with production's key:\n%s", context)
	}
	os.Remove(teamSlotPath("payments", teamSlotProduction))

	// One at a time, and a yes counts once.
	if _, err := ask(teamDeepReviewRequest{Approval: yes, Budget: 30}); err == nil {
		t.Fatal("a second review started while one runs")
	}

	// It ends: the report goes to the team's folder and the owner, and
	// the lead is woken in the thread.
	finding := reviewFinding{ID: "F-1", Area: "security", Title: "any user can move any card", Verdict: "confirmed", Fingerprint: "fp1", File: "api/cards.ts"}
	report := "# Review r-1: payments\n\n> These are recommendations, not requirements.\n\n## F-1 · any user can move any card\n\n- Where: `api/cards.ts` line 5\n\n**Assumes.** Boards are private.\n\n## Rejected and duplicates\n"
	teamTestReviewState(t, review{ID: "r-1", Root: b.repo, State: "done", Created: time.Now().UTC(), Ended: time.Now().UTC(), Areas: []string{"security"}, Findings: []reviewFinding{finding}}, report)
	var ended teamDeepReview
	for range 300 {
		for _, r := range loadTeamDeepReviews("payments") {
			if r.ID == "r-1" {
				ended = r
			}
		}
		if ended.Told {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !ended.Told || ended.Status != "done" {
		t.Fatalf("the end wasn't handled: %+v", ended)
	}
	if data, _ := os.ReadFile(filepath.Join(teamReviewsDir("payments"), "r-1", "report.md")); !strings.Contains(string(data), "## F-1") {
		t.Error("the report isn't in the team's folder")
	}
	calls := strings.Join(channel.calls, "\n")
	for _, want := range []string{"dm U07OWNER1 The deep review you agreed to (r-1) has ended: 1 thing raised: 1 confirmed", "upload DU07OWNER1  report.md Review r-1", "upload DU07OWNER1  report.html"} {
		if !strings.Contains(calls, want) {
			t.Errorf("Slack lacks %q:\n%s", want, calls)
		}
	}
	mu.Lock()
	wake := strings.Join(woken, "\n---\n")
	mu.Unlock()
	for _, want := range []string{"hi-review-r-1\nslack:C07PAYMENTS:" + thread, "/team/reviews/r-1/report.md", "recommendations to weigh, not requirements",
		"Don't offer to fix everything", "--finding r-1:F-1", "nothing about how to exploit them", "hi sent U07OWNER1 the full report"} {
		if !strings.Contains(wake, want) {
			t.Errorf("the lead's prompt lacks %q:\n%s", want, wake)
		}
	}

	// A fix for a finding the people chose carries its section, and the
	// next review knows the task set out to fix it.
	if _, err := b.startTask(teamRequest{Role: "coder", Brief: "Fix the card check", Small: true, Findings: []string{"r-1:F-9"}}); err == nil {
		t.Error("a finding the review doesn't have was accepted")
	}
	task, err := b.startTask(teamRequest{Role: "coder", Brief: "Fix the card check", Small: true, Findings: []string{"r-1:F-1"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(task.Findings) != 1 || task.Findings[0].Fingerprint != "fp1" {
		t.Fatalf("task findings: %+v", task.Findings)
	}
	waitTeamTask(t, task.ID)
	brief, _ := os.ReadFile(filepath.Join(teamTaskDir("payments"), task.ID+".md"))
	if !strings.Contains(string(brief), "# From the deep review") || !strings.Contains(string(brief), "**Assumes.** Boards are private.") ||
		strings.Contains(string(brief), "Rejected and duplicates") {
		t.Errorf("brief:\n%s", brief)
	}
	task, _ = loadTeamTask("payments", task.ID)
	task.Status = "merged"
	saveTeamTask("payments", task)

	yes, _ = teamTestAskReview(t, "U07OWNER1", "The fix is in. Another deep review, up to $25?", 5)
	if _, err := ask(teamDeepReviewRequest{Approval: yes, Budget: 25, Since: "v1"}); err == nil || !strings.Contains(err.Error(), "there's no v1") {
		t.Fatalf("--since a version that doesn't exist: %v", err)
	}
	if _, err := ask(teamDeepReviewRequest{Approval: yes, Budget: 25}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(reviews[1], "--compare r-1") || !strings.Contains(reviews[1], "--fixes ") {
		t.Errorf("the second review isn't compared: %s", reviews[1])
	}
	fixes, _ := os.ReadFile(filepath.Join(teamReviewsDir("payments"), "fixes.json"))
	if string(fixes) != `{"fp1":"`+task.ID+`"}` {
		t.Errorf("fixes: %s", fixes)
	}
	text, err := b.showDeepReviews("")
	if err != nil || !strings.Contains(text, "r-1  done") || !strings.Contains(text, "r-2  running") {
		t.Errorf("list: %v\n%s", err, text)
	}
}

func TestTeamDeepReviewCommand(t *testing.T) {
	var path string
	var body teamDeepReviewRequest
	call := func(method, p string, in any, out any) error {
		path = method + " " + p
		if request, ok := in.(teamDeepReviewRequest); ok {
			body = request
			data, _ := json.Marshal(teamDeepReview{ID: "r-3", Budget: request.Budget, Commit: "abc1234def", Areas: []string{"security"}, Approval: &teamApproval{Name: "Ana"}})
			return json.Unmarshal(data, out)
		}
		return json.Unmarshal([]byte(`{"text": "ok"}`), out)
	}
	notes := filepath.Join(t.TempDir(), "notes.md")
	os.WriteFile(notes, []byte("Three people use it.\n"), 0o600)
	var out bytes.Buffer
	args := []string{"--project", "--approval", "1700000000.000002", "--budget", "$30", "--area", "security", "--since", "v2", "--context", notes}
	if err := runTeamDeepReviewCommand(args, strings.NewReader(""), &out, call); err != nil {
		t.Fatal(err)
	}
	if path != "POST /deep-review" || body.Budget != 30 || body.Areas != "security" || body.Since != "v2" || body.Context != "Three people use it." {
		t.Fatalf("%s %+v", path, body)
	}
	if !strings.Contains(out.String(), "Deep review r-3 started") || !strings.Contains(out.String(), "sends Ana the report") {
		t.Errorf("output: %s", out.String())
	}
	for _, bad := range [][]string{{"--project", "--budget", "lots"}, {"--project", "--frob"}, {"show"}} {
		if err := runTeamDeepReviewCommand(bad, strings.NewReader(""), &out, call); err == nil {
			t.Errorf("%v was accepted", bad)
		}
	}
	runTeamDeepReviewCommand([]string{"stop", "r-3"}, strings.NewReader(""), &out, call)
	if path != "POST /deep-review/stop" {
		t.Errorf("stop: %s", path)
	}
}

func TestTeamSoulDeepReview(t *testing.T) {
	soul := teamSoul(testTeamConfig())
	if strings.Count(soul, "## Deep reviews") != 1 || !strings.Contains(soul, "A no is a no") {
		t.Fatalf("SOUL.md:\n%s", soul)
	}
	// A SOUL.md from before gets the section once.
	path := filepath.Join(t.TempDir(), "SOUL.md")
	os.WriteFile(path, []byte(strings.Replace(soul, "\n"+teamSoulDeepReview, "", 1)), 0o600)
	for range 2 {
		if err := updateTeamSoul(path); err != nil {
			t.Fatal(err)
		}
	}
	data, _ := os.ReadFile(path)
	if strings.Count(string(data), "## Deep reviews") != 1 {
		t.Fatalf("updated SOUL.md:\n%s", data)
	}
}
