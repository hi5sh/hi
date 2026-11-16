package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func appTestHooksReset(t *testing.T) {
	t.Helper()
	appDevHooksMu.Lock()
	delete(appDevHooks, "payments")
	appDevHooksMu.Unlock()
	t.Cleanup(func() {
		appDevHooksMu.Lock()
		delete(appDevHooks, "payments")
		appDevHooksMu.Unlock()
	})
}

// Claude Code's events, one after another: what the page then says.
func TestAppDevHookStates(t *testing.T) {
	appTestHooksReset(t)
	start := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	at := start
	send := func(event appDevHookEvent) {
		event.Workspace, event.Agent = "w2", firstNonEmpty(event.Agent, "claude")
		if event.Session == "" {
			event.Session = "s-1"
		}
		at = at.Add(time.Second)
		appDevHookRecord("payments", event, at)
	}
	status := func(herdr string, now time.Time) (string, *appDevHookState) {
		return appDevHookStatus("payments", "w2", "claude", herdr, "/box/home/.claude/projects/x/s-1.jsonl", now)
	}
	send(appDevHookEvent{Event: "SessionStart", Kind: "startup"})
	if got, hook := status("idle", at); got != "idle" || hook == nil {
		t.Fatal(got, hook)
	}
	send(appDevHookEvent{Event: "UserPromptSubmit"})
	turn := at
	send(appDevHookEvent{Event: "PreToolUse", Tool: "Bash"})
	// herdr hasn't seen it yet: the hooks have.
	if got, hook := status("idle", at); got != "working" || hook.Tool != "Bash" || !hook.Since.Equal(turn) {
		t.Fatal(got, hook)
	}
	// A subagent's tool: still the main agent's turn, and its tool.
	send(appDevHookEvent{Event: "PreToolUse", Tool: "Read", Subagent: true})
	if _, hook := status("working", at); hook.Tool != "Bash" {
		t.Fatal(hook)
	}
	send(appDevHookEvent{Event: "PostToolUse", Tool: "Bash"})
	send(appDevHookEvent{Event: "PreToolUse", Tool: "AskUserQuestion"})
	// herdr misses the question: the page doesn't.
	if got, hook := status("working", at.Add(time.Hour)); got != "blocked" || hook.Waiting != "a question" {
		t.Fatal(got, hook)
	}
	send(appDevHookEvent{Event: "PostToolUse", Tool: "AskUserQuestion"})
	send(appDevHookEvent{Event: "PermissionRequest", Tool: "Edit"})
	if got, hook := status("working", at); got != "blocked" || hook.Waiting != "permission for Edit" {
		t.Fatal(got, hook)
	}
	send(appDevHookEvent{Event: "PostToolUse", Tool: "Edit"})
	// Stopped with Esc: no event. herdr's idle wins once the hooks are quiet.
	if got, _ := status("idle", at.Add(5*time.Second)); got != "working" {
		t.Fatal("too soon:", got)
	}
	if got, hook := status("idle", at.Add(appDevHookTrust+time.Second)); got != "idle" || hook != nil {
		t.Fatal(got, hook)
	}
	send(appDevHookEvent{Event: "Stop"})
	// herdr's done stays: it's finished, and nobody looked yet.
	if got, _ := status("done", at); got != "done" {
		t.Fatal(got)
	}
	if got, hook := status("working", at.Add(time.Second)); got != "idle" || hook == nil {
		t.Fatal("herdr lags behind the Stop:", got, hook)
	}
	// Another session in the workspace than the hooks spoke for: herdr's.
	if got, hook := appDevHookStatus("payments", "w2", "claude", "working", "s-2", at); got != "working" || hook != nil {
		t.Fatal(got, hook)
	}
	// Another agent there: herdr's too.
	if got, hook := appDevHookStatus("payments", "w2", "codex", "working", "", at); got != "working" || hook != nil {
		t.Fatal(got, hook)
	}
	send(appDevHookEvent{Event: "SessionEnd"})
	if got, hook := status("working", at); got != "working" || hook != nil {
		t.Fatal(got, hook)
	}
	// Codex: a turn done.
	send(appDevHookEvent{Agent: "codex", Event: "Stop", Session: "thread-1"})
	if got, hook := appDevHookStatus("payments", "w2", "codex", "working", "", at); got != "idle" || hook == nil {
		t.Fatal(got, hook)
	}
	// What isn't a workspace or an agent hi knows is dropped.
	appDevHookRecord("payments", appDevHookEvent{Workspace: "../x", Agent: "claude", Event: "Stop"}, at)
	appDevHookRecord("payments", appDevHookEvent{Workspace: "w9", Agent: "sh", Event: "Stop"}, at)
	appDevHooksMu.Lock()
	n := len(appDevHooks["payments"])
	appDevHooksMu.Unlock()
	if n != 1 {
		t.Fatal(n)
	}
}

// hi's hooks go in beside herdr's and the owners', once.
func TestAppDevClaudeHooks(t *testing.T) {
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, ".claude"), 0o700)
	herdr := `{"hooks":{"SessionStart":[{"matcher":"^(startup|resume)$","hooks":[{"type":"command","command":"bash herdr-agent-state.sh session","timeout":10}]}]},"model":"sonnet"}`
	os.WriteFile(filepath.Join(home, ".claude", "settings.json"), []byte(herdr), 0o600)
	for i := 0; i < 2; i++ {
		if err := appDevClaudeSettings(home); err != nil {
			t.Fatal(err)
		}
	}
	data, _ := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	var settings struct {
		Hooks map[string][]struct {
			Hooks []struct{ Command string }
		}
		Model string
	}
	if err := json.Unmarshal(data, &settings); err != nil || settings.Model != "sonnet" {
		t.Fatal(err, string(data))
	}
	for _, event := range appDevClaudeHookEvents {
		ours := 0
		for _, group := range settings.Hooks[event] {
			for _, hook := range group.Hooks {
				if hook.Command == "/box/bin/hi dev __hook claude" {
					ours++
				}
			}
		}
		if ours != 1 {
			t.Errorf("%s: %d of hi's", event, ours)
		}
	}
	if len(settings.Hooks["SessionStart"]) != 2 || !strings.Contains(settings.Hooks["SessionStart"][0].Hooks[0].Command, "herdr") {
		t.Fatal("herdr's hook:", string(data))
	}
}

// hi dev __hook hands the event to the broker's socket, and says nothing
// whatever happens; outside herdr it does nothing.
func TestAppDevHookClient(t *testing.T) {
	appTestHooksReset(t)
	b, _ := appTestDevPage(t)
	server := httptest.NewUnstartedServer(b.devHandler())
	dir, _ := os.MkdirTemp("", "hk")
	defer os.RemoveAll(dir)
	socket := filepath.Join(dir, "dev.sock") // a short path: a socket's has a limit
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	server.Listener = listener
	server.Start()
	defer server.Close()
	t.Setenv(appDevSocketEnv, socket)
	t.Setenv("HERDR_WORKSPACE_ID", "")
	appDevHookClient([]string{"claude"}, strings.NewReader(`{"hook_event_name":"UserPromptSubmit","session_id":"s-1"}`), io.Discard)
	if got, hook := appDevHookStatus("payments", "w2", "claude", "idle", "", time.Now()); got != "idle" || hook != nil {
		t.Fatal("outside herdr:", got, hook)
	}
	t.Setenv("HERDR_WORKSPACE_ID", "w2")
	t.Setenv("HERDR_PANE_ID", "w2:p1")
	appDevHookClient([]string{"claude"}, strings.NewReader(`{"hook_event_name":"PreToolUse","session_id":"s-1","tool_name":"Bash","tool_input":{"command":"go test"}}`), io.Discard)
	if got, hook := appDevHookStatus("payments", "w2", "claude", "idle", "s-1", time.Now()); got != "working" || hook == nil || hook.Tool != "Bash" || hook.Pane != "w2:p1" {
		t.Fatal(got, hook)
	}
	appDevHookClient([]string{"codex", `{"type":"agent-turn-complete","thread-id":"t-1","last-assistant-message":"Done."}`}, nil, io.Discard)
	if got, hook := appDevHookStatus("payments", "w2", "codex", "working", "", time.Now()); got != "idle" || hook == nil {
		t.Fatal(got, hook)
	}
	// agy: its own hooks, answered {} always, so it goes on as it would.
	conv := "7213ebda-c25a-4a04-b509-e588d68b558c"
	agy := func(event, tool string) string {
		var out bytes.Buffer
		appDevHookClient([]string{"agy"}, strings.NewReader(`{"hookEventName":"`+event+`","conversationId":"`+conv+`","toolCall":{"id":"t","name":"`+tool+`","args":{}},"cwd":"/box/repo"}`), &out)
		return out.String()
	}
	if out := agy("PreInvocation", ""); out != "{}\n" {
		t.Fatalf("%q", out)
	}
	if got, hook := appDevHookStatus("payments", "w2", "agy", "idle", "", time.Now()); got != "working" || hook == nil || hook.Session != conv {
		t.Fatal(got, hook)
	}
	if out := agy("PreToolUse", "run_command"); out != `{"decision":"ask"}`+"\n" {
		t.Fatalf("an empty answer denies the tool: %q", out)
	}
	if got, hook := appDevHookStatus("payments", "w2", "agy", "idle", "", time.Now()); got != "working" || hook == nil || hook.Tool != "run_command" {
		t.Fatal(got, hook)
	}
	agy("PreToolUse", "ask_question")
	if got, hook := appDevHookStatus("payments", "w2", "agy", "working", "", time.Now()); got != "blocked" || hook == nil || hook.Waiting != "a question" {
		t.Fatal(got, hook)
	}
	if appDevHookSession("payments", "w2", "agy") != conv || appDevHookSession("payments", "w2", "claude") != "" {
		t.Fatal(appDevHookSession("payments", "w2", "agy"))
	}
	agy("PostToolUse", "ask_question")
	agy("Stop", "")
	if got, _ := appDevHookStatus("payments", "w2", "agy", "working", "", time.Now()); got != "idle" {
		t.Fatal(got)
	}
	t.Setenv("HERDR_WORKSPACE_ID", "")
	if out := agy("PreToolUse", "run_command"); out != `{"decision":"ask"}`+"\n" {
		t.Fatalf("outside herdr: %q", out)
	}
	t.Setenv("HERDR_WORKSPACE_ID", "w2")
	// The page's workspaces, with what the hooks add.
	appDevHookClient([]string{"claude"}, strings.NewReader(`{"hook_event_name":"PermissionRequest","session_id":"s-1","tool_name":"Edit"}`), io.Discard)
	spaces, err := appDevSpaces("payments")
	if err != nil {
		t.Fatal(err)
	}
	if spaces[1].Status != "blocked" || spaces[1].Waiting != "permission for Edit" || spaces[1].Since.IsZero() {
		t.Fatalf("%+v", spaces[1])
	}
	// No broker: still nothing said, nothing failed.
	t.Setenv(appDevSocketEnv, filepath.Join(t.TempDir(), "none.sock"))
	appDevHookClient([]string{"claude"}, strings.NewReader(`{"hook_event_name":"Stop"}`), io.Discard)
	var out bytes.Buffer
	if err := appDevClient([]string{"__hook", "claude"}, strings.NewReader("not json"), &out); err != nil || out.Len() != 0 {
		t.Fatal(err, out.String())
	}
}

// hi dev note: the agent's line on its workspace, by herdr's workspace or
// the worktree it runs in, kept by the work's branch, one line, cleared
// with --clear; a folder that's no workspace's gets a way out.
func TestAppDevNote(t *testing.T) {
	b, _ := appTestDevPage(t)
	work, err := b.devNote("w2", "", "tests pass;\n  waiting for the API key")
	if err != nil || work != "1-chart" {
		t.Fatal(work, err)
	}
	spaces, _ := appDevSpaces("payments")
	if spaces[1].Note != "tests pass; waiting for the API key" || spaces[1].NoteAt.IsZero() || spaces[0].Note != "" {
		t.Fatalf("%+v", spaces)
	}
	// From its worktree, without herdr's workspace: the same work.
	if work, err := b.devNote("", "/box/home/.herdr/worktrees/repo/1-chart/web", strings.Repeat("x", 300)); err != nil || work != "1-chart" {
		t.Fatal(work, err)
	}
	if note := appDevNotes("payments")["1-chart"].Text; len([]rune(note)) != appDevNoteMax || !strings.HasSuffix(note, "…") {
		t.Fatal(note)
	}
	if _, err := b.devNote("", "/tmp", "lost"); err == nil || !strings.Contains(err.Error(), "workspaces") {
		t.Fatal(err)
	}
	if _, err := b.devNote("w2", "", ""); err != nil {
		t.Fatal(err)
	}
	if _, ok := appDevNotes("payments")["1-chart"]; ok {
		t.Fatal("not cleared")
	}
	// The agents are told when.
	if !strings.Contains(appDevInstructions, `hi dev note "<a line>"`) || !strings.Contains(appDevUsage, "hi dev note") {
		t.Fatal("the agents aren't told")
	}
}

// hi's hook in agy's hooks.json: beside the others, written once.
func TestAppDevAgyHooks(t *testing.T) {
	hooks := map[string]any{"orca-status": map[string]any{"Stop": []any{}}}
	if !appDevAgyHooks(hooks) || appDevAgyHooks(hooks) {
		t.Fatal("not once")
	}
	if hooks["orca-status"] == nil {
		t.Fatal("the others' gone")
	}
	data, _ := json.Marshal(hooks[appDevAgyHookName])
	for _, want := range []string{`"PreToolUse":[{"hooks":[{"command":"/box/bin/hi dev __hook agy"`, `"matcher":"*"`, `"Stop":[{"command":"/box/bin/hi dev __hook agy"`, `"PreInvocation"`, `"PostInvocation"`, `"SessionStart"`, `"PostToolUse"`} {
		if !strings.Contains(string(data), want) {
			t.Fatal(want, string(data))
		}
	}
}
