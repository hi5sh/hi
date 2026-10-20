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

// teamTestBrainstormState writes hi agent brainstorm's record, as its own
// process would, with each done idea's response.
func teamTestBrainstormState(t *testing.T, bs brainstorm) {
	t.Helper()
	os.MkdirAll(brainstormStateFile(bs.ID), 0o700)
	if err := saveBrainstorm(&bs); err != nil {
		t.Fatal(err)
	}
	for _, run := range bs.Runs {
		if run.Kind == "idea" && run.State == "done" {
			os.MkdirAll(brainstormStateFile(bs.ID, "ideas", run.ID, "artifacts"), 0o700)
			os.WriteFile(brainstormStateFile(bs.ID, "ideas", run.ID, "response.md"), []byte("An idea from "+run.Name+"\n"), 0o600)
			for _, artifact := range run.Artifacts {
				os.WriteFile(brainstormStateFile(bs.ID, "ideas", run.ID, "artifacts", artifact), []byte("<svg/>"), 0o600)
			}
		}
	}
	if bs.State != "running" {
		os.WriteFile(brainstormStateFile(bs.ID, "question.md"), []byte(bs.Question+"\n"), 0o600)
		writeBrainstormReport(bs)
	}
}

func TestTeamBrainstorm(t *testing.T) {
	root := setupTeamTest(t)
	t.Setenv("XDG_STATE_HOME", filepath.Join(root, "state"))
	b := newTeamTestBroker(t)
	channel := &fakeTeamChannel{}
	b.channel = channel
	teamBrainstormPoll = 10 * time.Millisecond
	t.Cleanup(func() { teamBrainstormPoll = 30 * time.Second })

	var mu sync.Mutex
	var started, woken []string
	b.wakeLead = func(name, prompt, deliver string) error {
		mu.Lock()
		defer mu.Unlock()
		woken = append(woken, name+"\n"+deliver+"\n"+prompt)
		return nil
	}
	fake := b.run
	b.run = func(args ...string) ([]byte, error) {
		if len(args) > 1 && args[0] == "agent" && args[1] == "brainstorm" {
			mu.Lock()
			started = append(started, strings.Join(args, " "))
			mu.Unlock()
			bs := brainstorm{ID: "b-1", Root: b.repo, State: "running", PID: os.Getpid(), Created: time.Now().UTC(), Question: "How should people sort cards?",
				Runs: []brainstormRun{{ID: "codex", Kind: "idea", Name: "codex", Agent: "codex", State: "running"},
					{ID: "gemini", Kind: "idea", Name: "gemini", Agent: "hermes", Model: brainstormGeminiModel, State: "running"}}}
			teamTestBrainstormState(t, bs)
			return json.Marshal(bs)
		}
		return fake(args...)
	}
	// The app's data, which the agents read unless they look at the web.
	os.MkdirAll(teamSlotDataDir("payments", teamSlotProduction), 0o700)
	os.WriteFile(filepath.Join(teamSlotDataDir("payments", teamSlotProduction), "cards.db"), []byte("x"), 0o600)

	yes, thread := teamTestAskReview(t, "U07OWNER1", "This one is hard. Want me to start a brainstorm with Codex and Gemini?", 10)
	if _, err := b.startBrainstorm(teamBrainstormRequest{Approval: yes}); err == nil || !strings.Contains(err.Error(), "--question") {
		t.Fatalf("no question: %v", err)
	}
	record, err := b.startBrainstorm(teamBrainstormRequest{Approval: yes, Question: "How should people sort cards?", Agents: "codex,gemini"})
	if err != nil {
		t.Fatal(err)
	}
	main := boxGit(b.repo, "rev-parse", "main")
	if record.ID != "b-1" || record.Commit != main || strings.Join(record.Agents, ",") != "codex,gemini ("+brainstormGeminiModel+")" ||
		record.Deliver != "slack:C07PAYMENTS:"+thread {
		t.Fatalf("record: %+v", record)
	}
	for _, want := range []string{"agent brainstorm --question ", "--name payments", "--ref " + main, "--budget 10", "--max 20m", "--no-proposal", "--yes --detach --json",
		"--agents codex,gemini", "--data " + teamSlotDataDir("payments", teamSlotProduction)} {
		if !strings.Contains(started[0], want) {
			t.Errorf("hi agent brainstorm lacks %q: %s", want, started[0])
		}
	}
	context, _ := os.ReadFile(filepath.Join(teamBrainstormsDir("payments"), "context.md"))
	if !strings.Contains(string(context), "Track supplier payments") || !strings.Contains(string(context), "/data/app, read-only") {
		t.Errorf("context:\n%s", context)
	}
	// One at a time, and a yes counts once.
	if _, err := b.startBrainstorm(teamBrainstormRequest{Approval: yes, Question: "Again?"}); err == nil || !strings.Contains(err.Error(), "still running") {
		t.Fatalf("a second brainstorm: %v", err)
	}

	// It ends: the ideas go to the team's folder, every idea in full to the
	// thread, and the lead is woken there to compile them.
	teamTestBrainstormState(t, brainstorm{ID: "b-1", Root: b.repo, State: "done", Created: time.Now().UTC(), Ended: time.Now().UTC(), Question: "How should people sort cards?",
		Runs: []brainstormRun{{ID: "codex", Kind: "idea", Name: "codex", Agent: "codex", State: "done", Artifacts: []string{"flow.svg"}},
			{ID: "gemini", Kind: "idea", Name: "gemini", Agent: "hermes", Model: brainstormGeminiModel, State: "failed", Note: "it wrote no response.md"}}})
	var ended teamBrainstorm
	for range 300 {
		for _, r := range loadTeamBrainstorms("payments") {
			ended = r
		}
		if ended.Told {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !ended.Told || ended.Status != "done" {
		t.Fatalf("the end wasn't handled: %+v", ended)
	}
	folder := filepath.Join(teamBrainstormsDir("payments"), "b-1")
	if data, _ := os.ReadFile(filepath.Join(folder, "ideas", "codex", "response.md")); string(data) != "An idea from codex\n" || !fileExists(filepath.Join(folder, "ideas", "codex", "artifacts", "flow.svg")) {
		t.Errorf("the ideas aren't in the team's folder: %q", data)
	}
	if calls := strings.Join(channel.calls, "\n"); !strings.Contains(calls, "upload C07PAYMENTS "+thread+" report.md Brainstorm b-1: every idea in full") {
		t.Errorf("Slack: %s", calls)
	}
	mu.Lock()
	wake := strings.Join(woken, "\n---\n")
	mu.Unlock()
	for _, want := range []string{"hi-brainstorm-b-1\nslack:C07PAYMENTS:" + thread, "/team/brainstorms/b-1/ideas/codex/response.md", "artifacts in /team/brainstorms/b-1/ideas/codex/artifacts/ (flow.svg)",
		"No idea came from gemini", "one coherent proposal", "naming the agent it came from", "Where they differ"} {
		if !strings.Contains(wake, want) {
			t.Errorf("the lead's prompt lacks %q:\n%s", want, wake)
		}
	}
	// The yes was used; a new one is needed.
	if _, err := b.startBrainstorm(teamBrainstormRequest{Approval: yes, Question: "Again?"}); err == nil || !strings.Contains(err.Error(), "already approved") {
		t.Fatalf("a used yes: %v", err)
	}
	text, err := b.showBrainstorms("")
	if err != nil || !strings.Contains(text, "b-1  done") {
		t.Errorf("list: %v\n%s", err, text)
	}
}

func TestTeamBrainstormCommand(t *testing.T) {
	var path string
	var body teamBrainstormRequest
	call := func(method, p string, in any, out any) error {
		path = method + " " + p
		if request, ok := in.(teamBrainstormRequest); ok {
			body = request
			data, _ := json.Marshal(teamBrainstorm{ID: "b-3", Commit: "abc1234def", Agents: []string{"codex", "claude"}, Approval: &teamApproval{Name: "Ana"}})
			return json.Unmarshal(data, out)
		}
		return json.Unmarshal([]byte(`{"text": "ok"}`), out)
	}
	question := filepath.Join(t.TempDir(), "question.md")
	os.WriteFile(question, []byte("How should people sort cards?\n"), 0o600)
	t.Setenv("HERMES_SESSION_CHAT_ID", "D07ANA")
	t.Setenv("HERMES_SESSION_THREAD_ID", "1700000100.000001")
	var out bytes.Buffer
	if err := runTeamBrainstormCommand([]string{"--approval", "1700000000.000002", "--question", question, "--agents", "codex,claude", "--web"}, strings.NewReader(""), &out, call); err != nil {
		t.Fatal(err)
	}
	if path != "POST /brainstorm" || body.Agents != "codex,claude" || !body.Web || !strings.Contains(body.Question, "sort cards") || body.Deliver != "slack:D07ANA:1700000100.000001" {
		t.Fatalf("%s %+v", path, body)
	}
	if !strings.Contains(out.String(), "Brainstorm b-3 started with codex, claude") {
		t.Errorf("output: %s", out.String())
	}
	for _, bad := range [][]string{{"--approval", "1"}, {"--frob"}, {"show"}, {"agents", "x"}} {
		if err := runTeamBrainstormCommand(bad, strings.NewReader(""), &out, call); err == nil {
			t.Errorf("%v was accepted", bad)
		}
	}
	runTeamBrainstormCommand([]string{"agents"}, strings.NewReader(""), &out, call)
	if path != "GET /brainstorm/agents" {
		t.Errorf("agents: %s", path)
	}
	runTeamBrainstormCommand([]string{"stop", "b-3"}, strings.NewReader(""), &out, call)
	if path != "POST /brainstorm/stop" {
		t.Errorf("stop: %s", path)
	}
}
