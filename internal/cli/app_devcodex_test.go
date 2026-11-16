package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A Codex session as the dev box has it: its first line, then what was
// said and done, as item_completed records among the rest.
func appTestCodexSession(t *testing.T, box, id, cwd, source string, at time.Time) {
	t.Helper()
	host, _ := appDevHostPath("payments", box)
	os.MkdirAll(filepath.Dir(host), 0o700)
	lines := []string{
		`{"timestamp":"2026-11-16T10:00:00Z","type":"session_meta","payload":{"id":"` + id + `","cwd":"` + cwd + `","source":"` + source + `","originator":"codex_cli_rs","timestamp":"2026-11-16T10:00:00Z","base_instructions":{"text":"` + strings.Repeat("x", 9000) + `"}}}`,
		`{"timestamp":"2026-11-16T10:00:01Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"UserMessage","id":"u0","content":[{"type":"text","text":"# AGENTS.md instructions for ` + cwd + `\n\n<INSTRUCTIONS>…"}]}}}`,
		`{"timestamp":"2026-11-16T10:00:02Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"UserMessage","id":"u1","content":[{"type":"text","text":"Make the chart green when it's up","text_elements":[]}]}}}`,
		`{"timestamp":"2026-11-16T10:00:03Z","type":"event_msg","payload":{"type":"token_count","info":{}}}`,
		`{"timestamp":"2026-11-16T10:00:04Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"Reasoning","id":"r1","summary_text":["Looking at the chart's colors"],"raw_content":[]}}}`,
		`{"timestamp":"2026-11-16T10:00:05Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"CommandExecution","id":"exec-1","command":["/bin/bash","-lc","rg -n strokeStyle web"],"status":"completed","aggregated_output":"web/chart.js:11:  ctx.strokeStyle = \"red\";\n","exit_code":0}}}`,
		`{"timestamp":"2026-11-16T10:00:06Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"CommandExecution","id":"exec-2","command":["/bin/bash","-lc","npm test"],"status":"failed","aggregated_output":"1 failing","exit_code":1}}}`,
		`{"timestamp":"2026-11-16T10:00:07Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"FileChange","id":"fc-1","changes":[{"path":"web/chart.js","kind":{"type":"update"}}],"status":"completed"}}}`,
		`{"timestamp":"2026-11-16T10:00:08Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"AgentMessage","id":"m1","content":[{"type":"Text","text":"The chart is green when it's up."}],"phase":"final_answer"}}}`,
	}
	os.WriteFile(host, []byte(strings.Join(lines, "\n")+"\n"), 0o600)
	os.Chtimes(host, at, at)
}

// Codex's sessions in Chat: what was said and done, without what Codex
// adds to the first message; a workspace's sessions, both agents', newest
// first, without hi's own runs; Resume for each.
func TestAppDevCodexChat(t *testing.T) {
	appTestHooksReset(t)
	b, ran := appTestDevPage(t)
	handler := b.devPageHandler()
	cwd := "/box/home/.herdr/worktrees/repo/1-chart"
	codexID := "01a1c0ab-3199-7e21-9880-67693b4b0911"
	codexBox := "/box/home/.codex/sessions/2026/11/16/rollout-2026-11-16T10-00-00-" + codexID + ".jsonl"
	now := time.Now()
	appTestCodexSession(t, codexBox, codexID, cwd, "cli", now.Add(-time.Minute))
	appTestCodexSession(t, "/box/home/.codex/sessions/2026/11/16/rollout-2026-11-16T09-00-00-01a1c0ab-0000-7000-8000-000000000001.jsonl", "01a1c0ab-0000-7000-8000-000000000001", "/box/home/.cache/hi-review-1", "exec", now)
	appTestCodexSession(t, "/box/home/.codex/sessions/2026/11/15/rollout-2026-11-15T09-00-00-01a1c0ab-0000-7000-8000-000000000002.jsonl", "01a1c0ab-0000-7000-8000-000000000002", "/box/repo", "cli", now)
	claude := appDevClaudeDir(cwd) + "/0b6f7f4e-1c2d-4e5f-8a9b-0c1d2e3f4a5b.jsonl"
	host, _ := appDevHostPath("payments", claude)
	os.MkdirAll(filepath.Dir(host), 0o700)
	os.WriteFile(host, []byte(`{"type":"user","timestamp":"2026-11-15T08:00:00Z","message":{"role":"user","content":"Fix the chart's axis"}}`+"\n"), 0o600)
	os.Chtimes(host, now.Add(-time.Hour), now.Add(-time.Hour))

	running := "codex"
	previous := appDevBoxExec
	appDevBoxExec = func(team string, command ...string) (string, error) {
		switch strings.Join(command[:min(3, len(command))], " ") {
		case "herdr pane list":
			if running == "" {
				return `{"result":{"panes":[{"pane_id":"w2:p1","cwd":"` + cwd + `"}]}}`, nil
			}
			return `{"result":{"panes":[{"pane_id":"w2:p1","agent":"codex","agent_status":"idle","cwd":"` + cwd + `"}]}}`, nil
		case "herdr pane read":
			return "> ", nil
		}
		return previous(team, command...)
	}
	t.Cleanup(func() { appDevBoxExec = previous })

	var view appDevChatView
	code, body := appTestDevPageCall(t, handler, appTestOwnerKey, "GET", "api/chat?workspace=w2", nil)
	if code != 200 || json.Unmarshal([]byte(body), &view) != nil {
		t.Fatal(code, body)
	}
	if view.Session != codexBox || view.ID != codexID || view.SessionAgent != "codex" || view.Note != "" || view.Choose {
		t.Fatalf("%+v", view)
	}
	var got []string
	for _, entry := range view.Entries {
		got = append(got, entry.T+"|"+entry.Name+"|"+firstNonEmpty(entry.Summary, entry.Text)+map[bool]string{true: "|failed"}[entry.Error])
	}
	want := []string{
		"user||Make the chart green when it's up",
		"thinking||Looking at the chart's colors",
		"tool|Bash|rg -n strokeStyle web",
		"result||web/chart.js:11:  ctx.strokeStyle = \"red\";",
		"tool|Bash|npm test",
		"result||1 failing|failed",
		"tool|Edit|web/chart.js",
		"result||completed",
		"text||The chart is green when it's up.",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got\n%s", strings.Join(got, "\n"))
	}

	// The workspace's sessions: both agents', newest first, not the review's
	// nor the clone's.
	code, body = appTestDevPageCall(t, handler, appTestOwnerKey, "GET", "api/chat/sessions?workspace=w2", nil)
	var list struct{ Sessions []appDevSession }
	if code != 200 || json.Unmarshal([]byte(body), &list) != nil || len(list.Sessions) != 2 {
		t.Fatal(code, body)
	}
	if s := list.Sessions[0]; s.Agent != "codex" || s.ID != codexID || s.Title != "Make the chart green when it's up" {
		t.Fatalf("%+v", s)
	}
	if s := list.Sessions[1]; s.Agent != "claude" || s.ID != "0b6f7f4e-1c2d-4e5f-8a9b-0c1d2e3f4a5b" || s.Title != "Fix the chart's axis" {
		t.Fatalf("%+v", s)
	}

	// Resume: not over a running agent; Codex's with codex resume.
	if code, body := appTestDevPageCall(t, handler, appTestOwnerKey, "POST", "api/chat/resume", map[string]any{"workspace": "w2", "session": codexID, "agent": "codex"}); code == 200 || !strings.Contains(body, "running") {
		t.Fatal(code, body)
	}
	running = ""
	if code, body := appTestDevPageCall(t, handler, appTestOwnerKey, "POST", "api/chat/resume", map[string]any{"workspace": "w2", "session": codexID, "agent": "codex"}); code != 200 {
		t.Fatal(code, body)
	}
	if code, body := appTestDevPageCall(t, handler, appTestOwnerKey, "POST", "api/chat/resume", map[string]any{"workspace": "w2", "session": codexID, "agent": "sh"}); code == 200 {
		t.Fatal(code, body)
	}
	if all := strings.Join(*ran, "\n"); !strings.Contains(all, "herdr pane run w2:p1 codex resume "+codexID) {
		t.Fatal(all)
	}
	// No agent: the folder's newest session, whichever agent's.
	code, body = appTestDevPageCall(t, handler, appTestOwnerKey, "GET", "api/chat?workspace=w2", nil)
	if json.Unmarshal([]byte(body), &view) != nil || view.Session != codexBox {
		t.Fatal(code, body)
	}
}
