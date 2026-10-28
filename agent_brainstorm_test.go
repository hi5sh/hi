package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBrainstormFlags(t *testing.T) {
	agentTestSetup(t)
	data := t.TempDir()
	for _, args := range [][]string{
		{},
		{"--agents", "gpt", "why?"},
		{"--agents", "hermes", "why?"}, // not installed here
		{"--max", "10s", "why?"},
		{"--budget", "lots", "why?"},
		{"--web", "--data", data, "why?"},
		{"--data", filepath.Join(data, "nothing"), "why?"},
		{"--no-proposal", "--lead", "codex", "why?"},
		{"--question", "-", "why?"},
		{"--ref", "nowhere", "why?"},
	} {
		var stdout, stderr bytes.Buffer
		if status := runAgent(append([]string{"brainstorm"}, args...), strings.NewReader(""), &stdout, &stderr); status == 0 {
			t.Errorf("%v was accepted", args)
		}
	}
	// Without a terminal or --yes, nothing starts.
	var stdout, stderr bytes.Buffer
	if status := runAgent([]string{"brainstorm", "why?"}, strings.NewReader(""), &stdout, &stderr); status == 0 || !strings.Contains(stderr.String(), "--yes") {
		t.Fatalf("started without confirmation: %d %s", status, stderr.String())
	}
	// Gemini isn't ready here (no Hermes), so the default list leaves it
	// out and says so.
	if !strings.Contains(stdout.String(), "codex, claude, each alone") || !strings.Contains(stdout.String(), "left out, not ready here: gemini") {
		t.Fatalf("the confirmation:\n%s", stdout.String())
	}
	if entries, _ := os.ReadDir(brainstormStateFile()); len(entries) != 0 {
		t.Fatal("a brainstorm that didn't start left a folder")
	}
}

func TestBrainstormAgents(t *testing.T) {
	agentTestSetup(t)
	names, kinds, models, _, err := parseBrainstormAgents("claude:sonnet,codex")
	if err != nil || strings.Join(names, ",") != "claude,codex" || strings.Join(kinds, ",") != "claude,codex" || models[0] != "sonnet" {
		t.Fatal(names, kinds, models, err)
	}
	hermesTestSetup(t)
	names, kinds, models, _, err = parseBrainstormAgents("gemini,gemini:google/gemini-3.8-flash")
	if err != nil || strings.Join(names, ",") != "gemini,gemini" || strings.Join(kinds, ",") != "hermes,hermes" ||
		models[0] != brainstormGeminiModel || models[1] != "google/gemini-3.8-flash" {
		t.Fatal(names, kinds, models, err)
	}
}

func TestBrainstormRun(t *testing.T) {
	project, calls, exited := agentTestSetup(t)
	reviewCommit(t, project, map[string]string{"docs/user/index.md": "A board for the team's cards.\n"})
	os.WriteFile(filepath.Join(project, "notes.md"), []byte("not committed yet\n"), 0o644)
	data := filepath.Join(t.TempDir(), "sales")
	os.MkdirAll(data, 0o755)
	*exited = "0"
	brainstormPoll = time.Millisecond
	brainstormSpawn = func(string, string) error { return nil }
	t.Cleanup(func() { brainstormPoll, brainstormSpawn = 3*time.Second, nil })

	outside := filepath.Join(t.TempDir(), "secret.txt")
	os.WriteFile(outside, []byte("host secret\n"), 0o600)
	tasks := map[string]string{}
	reviewFakeAgents(t, func(box string, meta boxMeta, home, task string) {
		tasks[box] = task
		write := func(name, content string) {
			os.MkdirAll(filepath.Dir(filepath.Join(home, name)), 0o700)
			os.WriteFile(filepath.Join(home, name), []byte(content), 0o600)
		}
		if meta.Agent == "claude" {
			write("result.json", `{"result":"Done.","total_cost_usd":0.75}`)
		} else {
			write("last.txt", "Done.")
		}
		write("agent.json", `{"agent_exit":0,"agent_seconds":120}`)
		switch {
		case strings.HasSuffix(box, "-codex"):
			write("response.md", "# Cards by colour\n\nGroup cards by colour.\n\nSee flow.svg.")
			write("artifacts/flow.svg", "<svg/>")
			// A link out of the box is never followed.
			os.Symlink(outside, filepath.Join(home, "artifacts", "secret.txt"))
		case strings.HasSuffix(box, "-claude"):
			write("response.md", "## Swimlanes\n\nOne lane per person.")
		case strings.HasSuffix(box, "-proposal"):
			write("proposal.md", "# Lanes, coloured\n\nSwimlanes from claude, colours from codex.")
		}
	})

	var stdout, stderr bytes.Buffer
	if status := runAgent([]string{"brainstorm", "--agents", "codex,claude", "--data", data, "--yes", "--detach", "How", "should", "people", "sort", "cards?"}, strings.NewReader(""), &stdout, &stderr); status != 0 {
		t.Fatalf("status %d: %s", status, stderr.String())
	}
	for _, want := range []string{"Brainstorm b-1 on", "with your uncommitted changes", "codex, claude, each alone", "codex compiles the ideas", "locked network", "read-only at /data/sales"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("the confirmation lacks %q:\n%s", want, stdout.String())
		}
	}
	if err := driveBrainstorm("b-1", io.Discard); err != nil {
		t.Fatal(err)
	}
	bs, err := loadBrainstorm("b-1")
	if err != nil {
		t.Fatal(err)
	}
	if bs.State != "done" || len(bs.Runs) != 3 {
		t.Fatalf("%s: %s %+v", bs.State, bs.Reason, bs.Runs)
	}
	for _, run := range bs.Runs {
		if run.State != "done" {
			t.Errorf("%s: %s %s", run.ID, run.State, run.Note)
		}
	}
	if got := strings.Join(bs.Runs[0].Artifacts, ","); got != "flow.svg" {
		t.Errorf("codex's artifacts: %q", got)
	}
	if fileExists(brainstormStateFile("b-1", "ideas", "codex", "artifacts", "secret.txt")) {
		t.Error("a link out of the box was copied")
	}
	// The ideas saw the snapshot with the uncommitted file, the question,
	// and the data; the proposal saw the ideas, not the other way round.
	if boxGit(project, "show", bs.Commit+":notes.md") != "not committed yet" {
		t.Error("the snapshot lacks the uncommitted file")
	}
	for box, task := range tasks {
		if !strings.Contains(task, "> How should people sort cards?") || !strings.Contains(task, "/data/sales") {
			t.Errorf("%s's task:\n%s", box, task)
		}
		if strings.HasSuffix(box, "-proposal") != strings.Contains(task, "/brainstorm/<agent>/response.md") {
			t.Errorf("%s's task and the ideas:\n%s", box, task)
		}
	}
	starts := 0
	for _, call := range *calls {
		line := strings.Join(call, " ")
		if call[0] != "run" || strings.Contains(line, "-proxy") {
			continue
		}
		starts++
		if !strings.Contains(line, data+":/data/sales:ro") || strings.Contains(line, "-proposal") != strings.Contains(line, ":/brainstorm:ro") {
			t.Errorf("box mounts: %s", line)
		}
	}
	if starts != 3 {
		t.Fatalf("%d boxes started", starts)
	}
	report, _ := os.ReadFile(brainstormStateFile("b-1", "report.md"))
	for _, want := range []string{"## The proposal, compiled by codex", "### Lanes, coloured", "## codex's idea", "### Cards by colour", "- flow.svg", "## claude's idea", "#### Swimlanes"} {
		if !strings.Contains(string(report), want) {
			t.Errorf("the report lacks %q:\n%s", want, report)
		}
	}
	// Nothing an agent did is left: no boxes, no branches.
	for _, run := range bs.Runs {
		if _, err := loadBoxMeta(run.Box); err == nil {
			t.Errorf("%s's box is left", run.ID)
		}
	}
	if branches := boxGit(project, "branch", "--list", "hi-brainstorm/*"); branches != "" {
		t.Errorf("branches left: %s", branches)
	}
	var shown bytes.Buffer
	if status := runAgent([]string{"brainstorm", "ls"}, strings.NewReader(""), &shown, io.Discard); status != 0 || !strings.Contains(shown.String(), "2 of 2 ideas") || !strings.Contains(shown.String(), "$0.75") {
		t.Fatalf("ls: %s", shown.String())
	}
}

func TestBrainstormWithoutIdeas(t *testing.T) {
	_, _, exited := agentTestSetup(t)
	*exited = "1"
	brainstormPoll = time.Millisecond
	brainstormSpawn = func(string, string) error { return nil }
	t.Cleanup(func() { brainstormPoll, brainstormSpawn = 3*time.Second, nil })
	reviewFakeAgents(t, func(box string, meta boxMeta, home, task string) {})
	var stdout, stderr bytes.Buffer
	if status := runAgent([]string{"brainstorm", "--agents", "codex", "--web", "--yes", "--detach", "why?"}, strings.NewReader(""), &stdout, &stderr); status != 0 {
		t.Fatalf("status %d: %s", status, stderr.String())
	}
	if !strings.Contains(stdout.String(), "open network") {
		t.Errorf("--web: %s", stdout.String())
	}
	driveBrainstorm("b-1", io.Discard)
	bs, _ := loadBrainstorm("b-1")
	if bs.State != "failed" || !strings.HasPrefix(bs.Runs[0].Note, "it wrote no response.md") || bs.Runs[1].State != "skipped" {
		t.Fatalf("%+v", bs)
	}
}

// hermesTestSetup makes Hermes look installed, with an OpenRouter key.
func hermesTestSetup(t *testing.T) {
	home := filepath.Join(t.TempDir(), ".hermes")
	t.Setenv("HERMES_HOME", home)
	t.Setenv("HERMES_INSTALL_DIR", "")
	os.MkdirAll(filepath.Dir(hermesLauncher()), 0o755)
	os.WriteFile(hermesLauncher(), []byte("#!/bin/sh\n"), 0o755)
	os.WriteFile(filepath.Join(home, "config.yaml"), []byte("model:\n  default: google/gemini-3.8-flash\n  provider: openrouter\n"), 0o600)
	os.WriteFile(hermesEnvPath(), []byte("OPENROUTER_API_KEY=sk-or-test\n"), 0o600)
}
