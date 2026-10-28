package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeAgentSessionTestFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

func claudeSessionTestLine(id string, context int) string {
	return fmt.Sprintf(`{"type":"assistant","message":{"id":%q,"model":"claude-opus-5-5","usage":{"input_tokens":10,"cache_read_input_tokens":%d,"cache_creation_input_tokens":0,"output_tokens":5},"content":[{"type":"tool_use","name":"Read","input":{"file_path":"/x"}}]}}`+"\n", id, context-10)
}

// A Claude session goes from one box to the folder, and from the folder to
// the next box, under that box's own working folder.
func TestAgentSessionClaude(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	session := t.TempDir()
	id := "7ba05080-98db-4417-96ce-496d8a9f4650"
	first := boxMeta{Name: "payments-t-1", Agent: "claude", Model: "opus", Session: session, Workdir: "/home/hi/.local/state/hi/box/payments-t-1/work"}
	home := boxStateFile(first.Name, "home")
	writeAgentSessionTestFile(t, filepath.Join(home, ".claude", "projects", agentClaudeProject(first.Workdir), id+".jsonl"), claudeSessionTestLine("m1", 40_000))
	writeAgentSessionTestFile(t, filepath.Join(home, ".claude", "projects", agentClaudeProject(first.Workdir), id, "tool-results", "a.txt"), "big")
	writeAgentSessionTestFile(t, filepath.Join(home, agentResultDir, "result.json"), `{"modelUsage":{"claude-opus-5-5":{"contextWindow":1000000}}}`)
	if err := keepAgentSession(first, id); err != nil {
		t.Fatal(err)
	}
	if state, err := agentSessionResumable(session, "claude", "opus"); err != nil || state.ID != id || state.Window != 1_000_000 {
		t.Fatal(state, err)
	}
	// Measured against the window Claude Code said: 215,000 tokens goes on.
	writeAgentSessionTestFile(t, filepath.Join(session, agentSessionFiles, ".claude", "projects", agentClaudeProject(first.Workdir), id+".jsonl"), claudeSessionTestLine("m1", 215_000))
	if _, err := agentSessionResumable(session, "claude", "opus"); err != nil {
		t.Fatal(err)
	}
	writeAgentSessionTestFile(t, filepath.Join(session, agentSessionFiles, ".claude", "projects", agentClaudeProject(first.Workdir), id+".jsonl"), claudeSessionTestLine("m1", 40_000))

	second := boxMeta{Name: "payments-t-3", Agent: "claude", Model: "opus", Session: session, Workdir: "/home/hi/.local/state/hi/box/payments-t-3/work"}
	home2 := boxStateFile(second.Name, "home")
	resume, err := prepareAgentSession("claude", second, home2)
	if err != nil || resume != id {
		t.Fatal(resume, err)
	}
	project := filepath.Join(home2, ".claude", "projects", "-home-hi--local-state-hi-box-payments-t-3-work")
	if !fileExists(filepath.Join(project, id+".jsonl")) || !fileExists(filepath.Join(project, id, "tool-results", "a.txt")) {
		t.Fatal("the session isn't under the new box's working folder")
	}
	resumed, ok := loadAgentResumed(home2)
	if !ok || resumed.ID != id || resumed.Offset == 0 {
		t.Fatalf("%+v", resumed)
	}

	// The run's own progress starts after the earlier runs.
	f, _ := os.OpenFile(filepath.Join(project, id+".jsonl"), os.O_APPEND|os.O_WRONLY, 0o600)
	f.WriteString(claudeSessionTestLine("m2", 50_000))
	f.Close()
	progress := newAgentProgressReader("claude", home2).poll()
	if progress.steps != 1 || progress.in != 50_000 {
		t.Fatalf("%+v", progress)
	}

	// The command resumes it.
	command, err := agentBoxSetup("claude", "Fix it", second, home2, &[]string{}, map[string]string{})
	if err != nil {
		if strings.Contains(err.Error(), "not installed") {
			t.Skip(err)
		}
		t.Fatal(err)
	}
	if script := strings.Join(command, " "); !strings.Contains(script, "--resume "+id) {
		t.Fatal(script)
	}
}

func TestAgentSessionNotResumed(t *testing.T) {
	session := t.TempDir()
	if _, err := agentSessionResumable(session, "claude", ""); err == nil {
		t.Fatal("an empty folder resumed")
	}
	file := ".claude/projects/-w/abcdef12-0000.jsonl"
	writeAgentSessionTestFile(t, filepath.Join(session, agentSessionStateFile), `{"agent":"claude","model":"opus","id":"abcdef12-0000","file":"`+file+`"}`)
	writeAgentSessionTestFile(t, filepath.Join(session, agentSessionFiles, file), claudeSessionTestLine("m1", 160_000))
	for _, c := range []struct{ kind, model, want string }{
		{"codex", "opus", "Claude Code's"},
		{"claude", "sonnet", "ran on opus"},
		{"claude", "opus", "more than 75% of the model's window"},
	} {
		if _, err := agentSessionResumable(session, c.kind, c.model); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s %s: %v", c.kind, c.model, err)
		}
	}
	writeAgentSessionTestFile(t, filepath.Join(session, agentSessionFiles, file), claudeSessionTestLine("m1", 140_000))
	if _, err := agentSessionResumable(session, "claude", "opus"); err != nil {
		t.Fatal(err)
	}
	os.Remove(filepath.Join(session, agentSessionFiles, file))
	if _, err := agentSessionResumable(session, "claude", "opus"); err == nil || !strings.Contains(err.Error(), "gone") {
		t.Fatal(err)
	}
}

// A Codex session keeps its place under ~/.codex/sessions, and its tokens
// count from where the run began.
func TestAgentSessionCodex(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	session := t.TempDir()
	id := "01a16cad-b7a3-76f2-9349-f9503cca4fcd"
	token := func(input, cached, output, last int) string {
		return fmt.Sprintf(`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":%d,"cached_input_tokens":%d,"output_tokens":%d},"last_token_usage":{"input_tokens":%d},"model_context_window":258400}}}`+"\n", input, cached, output, last)
	}
	first := boxMeta{Name: "payments-t-2", Agent: "codex", Model: "gpt-6.1-sol", Session: session}
	file := filepath.Join(".codex", "sessions", "2026", "10", "24", "rollout-2026-10-24T06-51-26-"+id+".jsonl")
	writeAgentSessionTestFile(t, filepath.Join(boxStateFile(first.Name, "home"), file), token(100_000, 90_000, 2_000, 48_000))
	if err := keepAgentSession(first, id); err != nil {
		t.Fatal(err)
	}
	second := boxMeta{Name: "payments-t-4", Agent: "codex", Model: "gpt-6.1-sol", Session: session}
	home2 := boxStateFile(second.Name, "home")
	if resume, err := prepareAgentSession("codex", second, home2); err != nil || resume != id {
		t.Fatal(resume, err)
	}
	f, _ := os.OpenFile(filepath.Join(home2, file), os.O_APPEND|os.O_WRONLY, 0o600)
	f.WriteString(token(160_000, 140_000, 3_000, 60_000))
	f.Close()
	if progress := newAgentProgressReader("codex", home2).poll(); progress.in != 60_000 || progress.cached != 50_000 || progress.out != 1_000 {
		t.Fatalf("%+v", progress)
	}
	// Over three quarters of the window, the next run starts a new session.
	f, _ = os.OpenFile(filepath.Join(session, agentSessionFiles, file), os.O_APPEND|os.O_WRONLY, 0o600)
	f.WriteString(token(400_000, 300_000, 9_000, 200_000))
	f.Close()
	if _, err := agentSessionResumable(session, "codex", "gpt-6.1-sol"); err == nil {
		t.Fatal("a full session resumed")
	}
}
