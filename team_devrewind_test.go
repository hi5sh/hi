package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// teamTestRewindSession writes a transcript as Claude Code does, with a
// rewind in it: "Make a" (a shell command), "Write b" (b.txt, by its Write
// tool), and "Two and two", then a rewind of both to before "Write b", and
// "Say ok" after "Make a". Its checkpoints are in file-history.
func teamTestRewindSession(t *testing.T, team string) (session, host, cwd string) {
	t.Helper()
	cwd = "/box/home/.herdr/worktrees/repo/1-chart"
	session = "/box/home/.claude/projects/-box-home--herdr-worktrees-repo-1-chart/5461d4d9-b597-4501-af42-1fc7079f507f.jsonl"
	host, _ = teamDevHostPath(team, session)
	os.MkdirAll(filepath.Dir(host), 0o700)
	history, _ := teamDevHostPath(team, "/box/home/.claude/file-history/5461d4d9-b597-4501-af42-1fc7079f507f")
	os.MkdirAll(history, 0o700)
	os.WriteFile(filepath.Join(history, "2ec2@v2"), []byte("world\n"), 0o600)
	work, _ := teamDevHostPath(team, cwd)
	os.MkdirAll(work, 0o700)
	user := func(id, parent, text string) string {
		return fmt.Sprintf(`{"type":"user","uuid":%q,"parentUuid":%q,"timestamp":"2026-10-30T11:41:00Z","message":{"role":"user","content":%q}}`, id, parent, text)
	}
	record := func(kind, id, parent, text string) string {
		return fmt.Sprintf(`{"type":%q,"uuid":%q,"parentUuid":%q,"message":{"role":"assistant","content":[{"type":"text","text":%q}]}}`, kind, id, parent, text)
	}
	snapshot := func(id, files string) string {
		return fmt.Sprintf(`{"type":"file-history-snapshot","messageId":%q,"snapshot":{"messageId":%q,"trackedFileBackups":{%s}},"isSnapshotUpdate":false}`, id, id, files)
	}
	dir := `"realParentDir":"` + cwd + `"`
	lines := []string{
		snapshot("p1", ""),
		user("p1", "", "Make a"),
		record("assistant", "a1", "p1", "Done."),
		`{"type":"system","subtype":"turn_duration","uuid":"s1","parentUuid":"a1"}`,
		snapshot("p2", ""),
		user("p2", "s1", "Write b\nwith the Write tool"),
		`{"type":"file-history-delta","messageId":"a2","snapshotMessageId":"p2","trackingPath":"b.txt","backup":{"backupFileName":null,"version":1,` + dir + `}}`,
		record("assistant", "a2", "p2", "Wrote b."),
		`{"type":"system","subtype":"turn_duration","uuid":"s2","parentUuid":"a2"}`,
		user("p3", "s2", "Two and two"),
		snapshot("p3", `"b.txt":{"backupFileName":"2ec2@v2","version":2,`+dir+`}`),
		record("assistant", "a3", "p3", "4"),
		`{"type":"system","subtype":"turn_duration","uuid":"s3","parentUuid":"a3"}`,
		snapshot("p4", `"b.txt":{"backupFileName":null,"version":3,`+dir+`}`),
		user("p4", "s1", "Say ok"),
		record("assistant", "a4", "p4", "ok"),
	}
	os.WriteFile(host, []byte(strings.Join(lines, "\n")+"\n"), 0o600)
	return session, host, cwd
}

// The conversation is the newest record's branch: the chat shows its
// turns, and the timeline its messages, with what the code did after each.
func TestTeamDevRewindBranch(t *testing.T) {
	b, _ := teamTestDevPage(t)
	_, host, cwd := teamTestRewindSession(t, "payments")
	index, err := teamDevRewindIndexFor(host)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, prompt := range index.active() {
		ids = append(ids, prompt.UUID)
	}
	index.mu.Unlock()
	if strings.Join(ids, ",") != "p1,p4" {
		t.Fatal(ids)
	}
	shown, turns, _ := teamDevChatBranch(host, "")
	entries, _, _, _ := readDevChat(host, 0, shown)
	var said []string
	for _, entry := range entries {
		said = append(said, entry.Text)
	}
	if strings.Join(said, " | ") != "Make a | Done. | Say ok | ok" || turns != "2:p4" {
		t.Fatal(said, turns)
	}
	// A page that had "Write b" and "Two and two" starts again.
	if _, _, follows := teamDevChatBranch(host, "3:p3"); follows {
		t.Fatal("the old branch still follows")
	}
	if _, _, follows := teamDevChatBranch(host, "1:p1"); !follows {
		t.Fatal("the start doesn't follow")
	}

	// b.txt was made in the turn rewound, and taken out again: no changes.
	// Made again since (by hand), "Say ok" has it, and restoring takes it out.
	points := func() []teamDevRewindPoint {
		index, _ := teamDevRewindIndexFor(host)
		defer index.mu.Unlock()
		return index.points("payments", "/box/home/.claude/projects/x/5461d4d9-b597-4501-af42-1fc7079f507f.jsonl")
	}
	got := points()
	if len(got) != 2 || got[0].Text != "Make a" || len(got[0].Changes) != 0 || len(got[1].Changes) != 0 || !got[1].Code {
		t.Fatalf("%+v", got)
	}
	work, _ := teamDevHostPath("payments", cwd)
	os.WriteFile(filepath.Join(work, "b.txt"), []byte("world\nagain\n"), 0o600)
	got = points()
	if c := got[1].Changes; len(c) != 1 || c[0] != (teamDevFileChange{Path: "b.txt", Added: 2}) {
		t.Fatalf("%+v", got[1])
	}
	if r := got[0].Restore; len(r) != 1 || r[0] != (teamDevFileChange{Path: "b.txt", Removed: 2}) {
		t.Fatalf("%+v", got[0])
	}

	// Rewound again, to before "Say ok": the transcript says nothing until
	// the next message, so hi keeps the cut.
	index, _ = teamDevRewindIndexFor(host)
	index.cut, index.cutLeaf = "p4", index.leaf
	index.mu.Unlock()
	if _, turns, _ := teamDevChatBranch(host, "2:p4"); turns != "1:p1" {
		t.Fatal(turns)
	}
	f, _ := os.OpenFile(host, os.O_APPEND|os.O_WRONLY, 0o600)
	f.WriteString(`{"type":"user","uuid":"p5","parentUuid":"s1","message":{"role":"user","content":"Say hi"}}` + "\n")
	f.Close()
	if _, turns, _ := teamDevChatBranch(host, ""); turns != "2:p5" {
		t.Fatal(turns)
	}
	_ = b
}

func TestTeamDevLineChanges(t *testing.T) {
	for _, c := range []struct {
		a, b           string
		added, removed int
	}{
		{"", "x\n", 1, 0},
		{"x\n", "", 0, 1},
		{"a\nb\nc\n", "a\nB\nc\nd\n", 2, 1},
		{"same\n", "same\n", 0, 0},
	} {
		if added, removed := teamDevLineChanges(c.a, c.b); added != c.added || removed != c.removed {
			t.Errorf("%q → %q: +%d -%d", c.a, c.b, added, removed)
		}
	}
}

// teamTestClaudeRewind is Claude Code's terminal, as far as /rewind goes:
// Esc Esc opens the list (or clears what's typed), up and down move in
// it, Enter asks to confirm, and a number restores.
type teamTestClaudeRewind struct {
	items    []string // the messages, as the list shows them
	state    string   // prompt, list, confirm
	typed    string
	chosen   int // in items, len(items) for (current)
	escs     int
	restored string
}

func (c *teamTestClaudeRewind) key(key string) {
	if key != "esc" {
		c.escs = 0
	}
	switch {
	case key == "esc" && c.state == "prompt":
		if c.escs++; c.escs == 2 {
			c.escs = 0
			if c.typed != "" {
				c.typed = ""
			} else {
				c.state, c.chosen = "list", len(c.items)
			}
		}
	case key == "esc":
		c.state = "prompt"
	case key == "up" && c.state == "list" && c.chosen > 0:
		c.chosen--
	case key == "down" && c.state == "list" && c.chosen < len(c.items):
		c.chosen++
	case key == "enter" && c.state == "list" && c.chosen < len(c.items):
		c.state = "confirm"
	case c.state == "confirm" && key >= "1" && key <= "3":
		c.restored = map[string]string{"1": "both", "2": "conversation", "3": "code"}[key]
		c.state = "prompt"
		if c.restored != "code" {
			c.typed = c.items[c.chosen]
		}
	}
}

func (c *teamTestClaudeRewind) screen() string {
	switch c.state {
	case "list":
		lines := []string{"   Rewind", "   " + teamDevRewindTitle + "…"}
		for i, item := range append(append([]string(nil), c.items...), "(current)") {
			mark := "     "
			if i == c.chosen {
				mark = "   ❯ "
			}
			lines = append(lines, mark+item, "     No code changes")
		}
		return strings.Join(lines, "\n")
	case "confirm":
		return "   Rewind\n   " + teamDevRewindConfirm + " to the point before you sent this message:\n   │ " + c.items[c.chosen] +
			"\n   ❯ 1. Restore code and conversation\n     2. Restore conversation\n     3. Restore code\n     4. Summarize from here\n   ↓ 5. Summarize up to here"
	}
	return "● ok\n────\n❯ " + c.typed + "\n────"
}

// Rewind presses Claude Code's keys, checks each screen, and leaves the
// message for the chat's box; a list that doesn't match backs out.
func TestTeamDevChatRewind(t *testing.T) {
	b, ran := teamTestDevPage(t)
	handler := b.devPageHandler()
	session, _, _ := teamTestRewindSession(t, "payments")
	previousWait := teamDevRewindWait
	teamDevRewindWait = time.Millisecond
	t.Cleanup(func() { teamDevRewindWait = previousWait })
	claude := &teamTestClaudeRewind{items: []string{"Make a", "Say ok"}, state: "prompt"}
	status := "idle"
	previous := teamDevBoxExec
	teamDevBoxExec = func(team string, command ...string) (string, error) {
		switch strings.Join(command[:min(3, len(command))], " ") {
		case "herdr pane list":
			return `{"result":{"panes":[{"pane_id":"w2:p1","agent":"claude","agent_status":"` + status + `","cwd":"/box/home/.herdr/worktrees/repo/1-chart","agent_session":{"kind":"path","value":"` + session + `"}}]}}`, nil
		case "herdr pane read":
			return claude.screen(), nil
		case "herdr agent send-keys":
			*ran = append(*ran, strings.Join(command, " "))
			for _, key := range command[4:] {
				claude.key(key)
			}
			return "", nil
		}
		return previous(team, command...)
	}
	rewind := func(uuid, mode string) (int, string) {
		return teamTestDevPageCall(t, handler, teamTestOwnerKey, "POST", "api/chat/rewind", map[string]any{"workspace": "w2", "uuid": uuid, "mode": mode})
	}

	// The timeline: the conversation's two messages.
	code, body := teamTestDevPageCall(t, handler, teamTestOwnerKey, "GET", "api/chat/points?workspace=w2", nil)
	var view teamDevRewindView
	json.Unmarshal([]byte(body), &view)
	if code != 200 || len(view.Points) != 2 || view.Points[1].UUID != "p4" || view.Turns != "2:p4" {
		t.Fatalf("%d %s", code, body)
	}

	// At work, it waits.
	status = "working"
	if code, body := rewind("p4", "both"); code == 200 || !strings.Contains(body, "stop it first") {
		t.Fatalf("%d %s", code, body)
	}
	status = "idle"
	// Something typed in the terminal is cleared first; the message comes
	// back for the chat, and the terminal's prompt is cleared again.
	claude.typed = "half a thought"
	code, body = rewind("p4", "conversation")
	if code != 200 || !strings.Contains(body, `"text":"Say ok"`) || claude.restored != "conversation" || claude.state != "prompt" || claude.typed != "" {
		t.Fatalf("%d %s %+v", code, body, claude)
	}
	// The chat leaves "Say ok" out until the next message.
	_, body = teamTestDevPageCall(t, handler, teamTestOwnerKey, "GET", "api/chat?workspace=w2&turns=2:p4", nil)
	var chat teamDevChatView
	json.Unmarshal([]byte(body), &chat)
	if !chat.Reset || chat.Turns != "1:p1" || len(chat.Entries) != 2 {
		t.Fatalf("%s", body)
	}

	// A list that doesn't show the message where the chat has it: Esc,
	// and nothing is restored.
	claude.items, claude.restored = []string{"Make a", "Something else"}, ""
	teamDevRewindIndexes.Lock()
	clear(teamDevRewindIndexes.byPath) // the cut forgotten, as with a restart
	teamDevRewindIndexes.Unlock()
	if code, body := rewind("p4", "both"); code == 200 || !strings.Contains(body, "didn't show that message") || claude.restored != "" || claude.state != "prompt" {
		t.Fatalf("%d %s %+v", code, body, claude)
	}
	if code, _ := rewind("p4", "everything"); code == 200 {
		t.Fatal("an unknown mode")
	}
	if code, _ := rewind("nope", "both"); code == 200 {
		t.Fatal("a message that isn't there")
	}
}
