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
		case "herdr pane send-keys":
			if command[len(command)-1] == "ctrl+c" {
				running = ""
			}
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
	if s := list.Sessions[0]; s.Agent != "codex" || s.ID != codexID || s.Title != "Make the chart green when it's up" || !s.Live || list.Sessions[1].Live {
		t.Fatalf("%+v", s)
	}
	if s := list.Sessions[1]; s.Agent != "claude" || s.ID != "0b6f7f4e-1c2d-4e5f-8a9b-0c1d2e3f4a5b" || s.Title != "Fix the chart's axis" {
		t.Fatalf("%+v", s)
	}

	// Resume: not over a running agent; Codex's with codex resume.
	if code, body := appTestDevPageCall(t, handler, appTestOwnerKey, "POST", "api/chat/resume", map[string]any{"workspace": "w2", "session": codexID, "agent": "codex"}); code == 200 || !strings.Contains(body, "running") {
		t.Fatal(code, body)
	}
	// With stop, from the page's session menu: Ctrl+C ends it first.
	if code, body := appTestDevPageCall(t, handler, appTestOwnerKey, "POST", "api/chat/resume", map[string]any{"workspace": "w2", "session": codexID, "agent": "codex", "stop": true}); code != 200 {
		t.Fatal(code, body)
	}
	if all := strings.Join(*ran, "\n"); !strings.Contains(all, "herdr pane send-keys w2:p1 ctrl+c") {
		t.Fatal(all)
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

// A task finished and started again gets its branch's folder back, and
// Claude Code keeps sessions by folder: the earlier work's sessions aren't
// this workspace's, in its list or its chat, until one is resumed here.
// Each says when it began and when something was last said or done in it,
// not its file's time, which Claude Code's bookkeeping moves.
func TestAppDevSessionsOfThisWorktree(t *testing.T) {
	b, _ := appTestDevPage(t)
	handler := b.devPageHandler()
	cwd := "/box/home/.herdr/worktrees/repo/1-chart"
	previous := appDevBoxExec
	appDevBoxExec = func(team string, command ...string) (string, error) {
		if strings.Join(command[:min(3, len(command))], " ") == "herdr pane list" {
			return `{"result":{"panes":[{"pane_id":"w2:p1","cwd":"` + cwd + `"}]}}`, nil
		}
		return previous(team, command...)
	}
	t.Cleanup(func() { appDevBoxExec = previous })
	stamp := func(at time.Time) string { return at.UTC().Format(time.RFC3339Nano) }
	write := func(id string, lines []string, changed time.Time) string {
		host, _ := appDevHostPath("payments", appDevClaudeDir(cwd)+"/"+id+".jsonl")
		os.MkdirAll(filepath.Dir(host), 0o700)
		os.WriteFile(host, []byte(strings.Join(lines, "\n")+"\n"), 0o600)
		os.Chtimes(host, changed, changed)
		return host
	}
	born := time.Now().Add(-20 * time.Minute).Truncate(time.Second)
	gitFile, _ := appDevHostPath("payments", cwd+"/.git")
	os.MkdirAll(filepath.Dir(gitFile), 0o700)
	os.WriteFile(gitFile, []byte("gitdir: /box/repo/.git/worktrees/1-chart\n"), 0o600)
	os.Chtimes(gitFile, born, born)
	session := func(started, last time.Time) []string {
		return []string{
			`{"type":"mode","mode":"normal"}`,
			`{"type":"user","timestamp":"` + stamp(started) + `","message":{"role":"user","content":"Work on task #1: Competitor news"}}`,
			`{"type":"ai-title","aiTitle":"Competitor news Slack report"}`,
			`{"type":"assistant","timestamp":"` + stamp(last) + `","message":{"role":"assistant","content":[{"type":"text","text":"Which bot should it use?"}]}}`,
			`{"type":"cost-state","totalCostUSD":0.43}`,
		}
	}
	earlier := "873ea1d4-fcb8-4e6e-9903-fa4fce796ee7"
	earlierHost := write(earlier, session(born.Add(-40*time.Minute), born.Add(-15*time.Minute)), born.Add(-time.Minute))
	now := "2b25a39c-e6f9-4fc2-994f-00df4597ac3f"
	nowHost := write(now, session(born.Add(time.Second), born.Add(time.Minute)), time.Now())

	list := func() []appDevSession {
		t.Helper()
		code, body := appTestDevPageCall(t, handler, appTestOwnerKey, "GET", "api/chat/sessions?workspace=w2", nil)
		var got struct{ Sessions []appDevSession }
		if code != 200 || json.Unmarshal([]byte(body), &got) != nil {
			t.Fatal(code, body)
		}
		return got.Sessions
	}
	got := list()
	if len(got) != 1 || got[0].ID != now || got[0].Title != "Competitor news Slack report" ||
		!got[0].Started.Equal(born.Add(time.Second)) || !got[0].Last.Equal(born.Add(time.Minute)) {
		t.Fatalf("%+v", got)
	}
	var view appDevChatView
	_, body := appTestDevPageCall(t, handler, appTestOwnerKey, "GET", "api/chat?workspace=w2", nil)
	if json.Unmarshal([]byte(body), &view) != nil || view.ID != now {
		t.Fatal(body)
	}
	// Its own sessions gone, the chat doesn't show the earlier work's.
	os.Remove(nowHost)
	view = appDevChatView{}
	_, body = appTestDevPageCall(t, handler, appTestOwnerKey, "GET", "api/chat?workspace=w2", nil)
	if json.Unmarshal([]byte(body), &view) != nil || view.Session != "" || view.Note != "No session here yet." {
		t.Fatal(body)
	}
	// Resumed here, the earlier session is this workspace's too; newest
	// first by what was last said, not by the files' times.
	write(now, session(born.Add(time.Second), born.Add(time.Minute)), born.Add(2*time.Minute))
	os.Chtimes(earlierHost, time.Now(), time.Now())
	if got := list(); len(got) != 2 || got[0].ID != now || got[1].ID != earlier || !got[1].Last.Equal(born.Add(-15*time.Minute)) {
		t.Fatalf("%+v", got)
	}
	// The project's own clone has a .git folder: all of its sessions.
	if !appDevWorktreeBorn("payments", "/box/repo").IsZero() {
		t.Fatal("the clone has a time it was made")
	}
}

// Work is shipped while its last change is in dev; a branch with none, or
// one taken out since, isn't.
func TestAppDevShipped(t *testing.T) {
	ref := appDevBranchRef("1-chart")
	tasks := []appTask{{Kind: "task", Branch: appDevBranchRef("2-other"), Status: "merged"}}
	if appDevShipped(tasks, "1-chart") {
		t.Fatal("no change of its own")
	}
	tasks = append(tasks, appTask{Kind: "task", Branch: ref, Status: "merged", Merged: "abc"})
	if !appDevShipped(tasks, "1-chart") {
		t.Fatal("merged")
	}
	tasks = append(tasks, appTask{Kind: "task", Branch: ref, Status: "discarded", Merged: "abc"})
	if appDevShipped(tasks, "1-chart") {
		t.Fatal("taken out since")
	}
}
