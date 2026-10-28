package main

import (
	"encoding/json"
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

// A deep review from the box: no Slack yes, a budget within the cap, and
// the report into the box's home instead of a DM and the lead.
func TestTeamDevDeepReview(t *testing.T) {
	root := setupTeamTest(t)
	t.Setenv("XDG_STATE_HOME", filepath.Join(root, "state"))
	b := newTeamTestBroker(t)
	channel := &fakeTeamChannel{}
	b.channel = channel
	teamDeepReviewPoll = 10 * time.Millisecond
	t.Cleanup(func() { teamDeepReviewPoll = 30 * time.Second })
	var mu sync.Mutex
	var started, woken []string
	b.wakeLead = func(name, prompt, deliver string) error {
		mu.Lock()
		defer mu.Unlock()
		woken = append(woken, name)
		return nil
	}
	fake := b.run
	b.run = func(args ...string) ([]byte, error) {
		if len(args) > 1 && args[0] == "agent" && args[1] == "review" && args[2] != "stop" {
			mu.Lock()
			started = append(started, strings.Join(args, " "))
			mu.Unlock()
			rv := review{ID: "r-1", Root: b.repo, State: "running", PID: os.Getpid(), Created: time.Now().UTC(), Areas: []string{"security"}}
			teamTestReviewState(t, rv, "")
			return json.Marshal(rv)
		}
		return fake(args...)
	}

	for _, budget := range []float64{0, teamDevDeepReviewMaxUSD + 1} {
		if _, err := b.devDeepReview(teamDeepReviewRequest{Budget: budget}); err == nil {
			t.Fatalf("started with $%v", budget)
		}
	}
	record, err := b.devDeepReview(teamDeepReviewRequest{Budget: 20, Context: "Only owners use it."})
	if err != nil {
		t.Fatal(err)
	}
	if record.ID != "r-1" || !record.Dev || record.Approval.Name != teamDevBy || len(started) != 1 || !strings.Contains(started[0], "--budget 20.00") {
		t.Fatalf("%+v %v", record, started)
	}
	if len(channel.calls) != 0 {
		t.Errorf("Slack heard of it: %v", channel.calls)
	}
	if _, err := b.devDeepReview(teamDeepReviewRequest{Budget: 20}); err == nil || !strings.Contains(err.Error(), "hi dev deep-review stop r-1") {
		t.Fatalf("a second one: %v", err)
	}
	if text, err := b.devShow("reviews", "r-1"); err != nil || !strings.Contains(text, "~/hi/reviews/r-1/") || strings.Contains(text, "/team/") {
		t.Fatalf("%v\n%s", err, text)
	}

	teamTestReviewState(t, review{ID: "r-1", Root: b.repo, State: "done", Created: time.Now().UTC(), Areas: []string{"security"}}, "# Review r-1\n\nNo findings.\n")
	for range 300 {
		if reviews := loadTeamDeepReviews("payments"); len(reviews) == 1 && reviews[0].Told {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	report, _ := os.ReadFile(filepath.Join(teamDevResultsDir("payments", "reviews", "r-1"), "report.md"))
	if !strings.Contains(string(report), "No findings") {
		t.Fatalf("the report isn't in the box's home: %q", report)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(woken) != 0 || len(channel.calls) != 0 {
		t.Errorf("the lead or Slack heard of it: %v %v", woken, channel.calls)
	}
}

// A brainstorm from the box: its ideas go into the box's home, where the
// agent that asked writes the proposal.
func TestTeamDevBrainstorm(t *testing.T) {
	root := setupTeamTest(t)
	t.Setenv("XDG_STATE_HOME", filepath.Join(root, "state"))
	b := newTeamTestBroker(t)
	channel := &fakeTeamChannel{}
	b.channel = channel
	teamBrainstormPoll = 10 * time.Millisecond
	t.Cleanup(func() { teamBrainstormPoll = 30 * time.Second })
	woken := 0
	b.wakeLead = func(string, string, string) error { woken++; return nil }
	fake := b.run
	b.run = func(args ...string) ([]byte, error) {
		if len(args) > 1 && args[0] == "agent" && args[1] == "brainstorm" {
			bs := brainstorm{ID: "b-1", Root: b.repo, State: "running", PID: os.Getpid(), Created: time.Now().UTC(), Question: "Charts?",
				Runs: []brainstormRun{{ID: "codex", Kind: "idea", Name: "codex", Agent: "codex", State: "running"}}}
			teamTestBrainstormState(t, bs)
			return json.Marshal(bs)
		}
		return fake(args...)
	}
	if _, err := b.devBrainstorm(teamBrainstormRequest{}); err == nil || !strings.Contains(err.Error(), "hi dev brainstorm") {
		t.Fatalf("no question: %v", err)
	}
	record, err := b.devBrainstorm(teamBrainstormRequest{Question: "Charts?"})
	if err != nil || !record.Dev || record.Deliver != "" {
		t.Fatalf("%+v %v", record, err)
	}
	context, _ := os.ReadFile(filepath.Join(teamBrainstormsDir("payments"), "context.md"))
	if !strings.Contains(string(context), "its owners build with coding agents") {
		t.Errorf("context:\n%s", context)
	}
	teamTestBrainstormState(t, brainstorm{ID: "b-1", Root: b.repo, State: "done", Created: time.Now().UTC(), Ended: time.Now().UTC(), Question: "Charts?",
		Runs: []brainstormRun{{ID: "codex", Kind: "idea", Name: "codex", Agent: "codex", State: "done"}}})
	for range 300 {
		if all := loadTeamBrainstorms("payments"); len(all) == 1 && all[0].Told {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	idea, _ := os.ReadFile(filepath.Join(teamDevResultsDir("payments", "brainstorms", "b-1"), "ideas", "codex", "response.md"))
	if string(idea) != "An idea from codex\n" || woken != 0 || len(channel.calls) != 0 {
		t.Fatalf("idea %q, woken %d, Slack %v", idea, woken, channel.calls)
	}
}

// The box sets the preview's own values and sees only names.
func TestTeamDevSettings(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	saveTeamSecrets("payments", teamSecrets{Env: map[string]string{"QUOTES_KEY": "real"}})
	for _, bad := range []teamDevSettingRequest{{Key: "1X", Value: "v"}, {Key: "PORT", Value: "1"}, {Key: "X"}, {Key: "X", Value: "a\nb"}, {Key: "NOPE", Unset: true}} {
		if _, err := b.devSetting(bad); err == nil {
			t.Errorf("%+v was set", bad)
		}
	}
	if _, err := b.devSetting(teamDevSettingRequest{Key: "QUOTES_KEY", Value: "test"}); err != nil {
		t.Fatal(err)
	}
	secrets, _ := loadTeamSecrets("payments")
	if secrets.EnvProduction["QUOTES_KEY"] != "real" || secrets.EnvPreview["QUOTES_KEY"] != "test" {
		t.Fatalf("%+v", secrets)
	}
	handler := b.devHandler()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/env", nil))
	if body := recorder.Body.String(); strings.Contains(body, "real") || strings.Contains(body, "test") || !strings.Contains(body, "QUOTES_KEY") {
		t.Fatal(body)
	}
	if _, err := b.devSetting(teamDevSettingRequest{Key: "QUOTES_KEY", Unset: true}); err != nil {
		t.Fatal(err)
	}
	if secrets, _ = loadTeamSecrets("payments"); secrets.EnvProduction["QUOTES_KEY"] != "real" || secrets.EnvPreview["QUOTES_KEY"] != "" {
		t.Fatalf("%+v", secrets)
	}
}

func TestTeamDevIncidentsAndData(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	os.MkdirAll(teamIncidentsDir("payments"), 0o700)
	saveTeamIncident("payments", teamIncident{ID: "i-1", Kind: "job", Title: "The nightly import failed", Detail: "exit 1", Status: "open", Opened: time.Now()})
	handler := b.devHandler()
	get := func(path string) string {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", path, recorder.Code, recorder.Body.String())
		}
		return recorder.Body.String()
	}
	if body := get("/incidents?id=i-1"); !strings.Contains(body, "exit 1") {
		t.Fatal(body)
	}
	if body := get("/data"); strings.Contains(body, teamDir("payments")) || !strings.Contains(body, `"preview"`) {
		t.Fatal(body)
	}
}

// hi dev review: the other agent, in a copy of the branch, with the
// review's rules; the report is printed and kept.
func TestTeamDevReview(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("CLAUDECODE", "1")
	origin := filepath.Join(root, "origin")
	git := func(dir string, args ...string) {
		t.Helper()
		if out, err := teamGitCommand(dir, args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
	}
	os.MkdirAll(origin, 0o700)
	git(origin, "init", "-q", "-b", "main")
	os.WriteFile(filepath.Join(origin, "a.txt"), []byte("a\n"), 0o600)
	git(origin, "add", ".")
	git(origin, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "a")
	clone := filepath.Join(root, "clone")
	git(root, "clone", "-q", origin, clone)
	git(clone, "checkout", "-q", "-b", "chart")
	t.Chdir(clone)

	var asked, in string
	previous := teamDevReviewer
	teamDevReviewer = func(agent, dir, prompt string, _ io.Writer) (string, error) {
		asked, in = agent, dir
		if !fileExists(filepath.Join(dir, "b.txt")) {
			t.Error("the copy isn't the branch")
		}
		return "Approve: fine.\n\nVerify on the preview: the chart", nil
	}
	t.Cleanup(func() { teamDevReviewer = previous })
	t.Setenv("PATH", filepath.Join(root, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
	os.MkdirAll(filepath.Join(root, "bin"), 0o700)
	os.WriteFile(filepath.Join(root, "bin", "codex"), []byte("#!/bin/sh\n"), 0o755)

	var out strings.Builder
	if err := teamDevReview(nil, nil, &out); err == nil || !strings.Contains(err.Error(), "nothing to review") {
		t.Fatalf("no commits: %v", err)
	}
	os.WriteFile(filepath.Join(clone, "b.txt"), []byte("b\n"), 0o600)
	git(clone, "add", ".")
	git(clone, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "b")
	os.WriteFile(filepath.Join(clone, "a.txt"), []byte("changed\n"), 0o600)
	if err := teamDevReview(nil, nil, &out); err == nil || !strings.Contains(err.Error(), "commit first") {
		t.Fatalf("uncommitted: %v", err)
	}
	git(clone, "checkout", "-q", "a.txt")
	if err := teamDevReview([]string{"the", "chart's", "dates"}, nil, &out); err != nil {
		t.Fatal(err)
	}
	if asked != "codex" || fileExists(in) || !strings.Contains(out.String(), "Approve: fine.") {
		t.Fatalf("agent %s, copy %s left: %v\n%s", asked, in, fileExists(in), out.String())
	}
	kept, _ := filepath.Glob(filepath.Join(root, "home", "hi", "reviews", "second-chart-*.md"))
	if len(kept) != 1 {
		t.Fatal("not kept:", kept)
	}
	prompt := teamDevReviewPrompt("chart", "main", "abc123", "the chart's dates")
	for _, want := range []string{"git diff abc123..HEAD", teamPreviewCheck, "Later:", "Alert:", "\"Approve:\"", "Don't fix anything", "the chart's dates"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the prompt lacks %q", want)
		}
	}
}
