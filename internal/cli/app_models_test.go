package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The chat's model and effort: read from the transcript's last answer,
// picked with Claude Code's /model and /effort, and not at all where the
// app's admin keeps them.
func TestDevChatModels(t *testing.T) {
	b, ran := appTestDevPage(t)
	handler := b.devPageHandler()
	session := "/box/home/.claude/projects/-box-home--herdr-worktrees-repo-1-chart/0b6f7f4e-1c2d-4e5f-8a9b-0c1d2e3f4a5b.jsonl"
	status := "idle"
	previous := appDevBoxExec
	appDevBoxExec = func(team string, command ...string) (string, error) {
		switch strings.Join(command[:min(3, len(command))], " ") {
		case "herdr pane list":
			return `{"result":{"panes":[{"pane_id":"w2:p1","agent":"claude","agent_status":"` + status + `","cwd":"/box/home/.herdr/worktrees/repo/1-chart","agent_session":{"agent":"claude","kind":"path","value":"` + session + `"}}]}}`, nil
		case "herdr pane read":
			return "> ", nil
		}
		return previous(team, command...)
	}
	t.Cleanup(func() { appDevBoxExec = previous })
	host, _ := appDevHostPath("payments", session)
	os.MkdirAll(filepath.Dir(host), 0o700)
	os.WriteFile(host, []byte(strings.Join([]string{
		`{"type":"user","timestamp":"2026-11-07T08:00:00Z","message":{"role":"user","content":"Fix the chart"}}`,
		`{"type":"assistant","timestamp":"2026-11-07T08:00:05Z","effort":"medium","message":{"role":"assistant","model":"claude-opus-5-5","content":[{"type":"text","text":"Done."}]}}`,
		`{"type":"assistant","timestamp":"2026-11-07T08:00:06Z","message":{"role":"assistant","model":"<synthetic>","content":[{"type":"text","text":"x"}]}}`,
	}, "\n")+"\n"), 0o600)
	read := func() appDevChatView {
		t.Helper()
		var view appDevChatView
		code, body := appTestDevPageCall(t, handler, appTestOwnerKey, "GET", "api/chat?workspace=w2", nil)
		if code != 200 {
			t.Fatalf("%d %s", code, body)
		}
		json.Unmarshal([]byte(body), &view)
		return view
	}
	if view := read(); view.Model != "claude-opus-5-5" || view.Effort != "medium" || !view.Choose {
		t.Fatalf("view: %s %s %v", view.Model, view.Effort, view.Choose)
	}
	pick := func(fields map[string]any) (int, string) {
		fields["workspace"] = "w2"
		return appTestDevPageCall(t, handler, appTestOwnerKey, "POST", "api/chat/model", fields)
	}
	if code, body := pick(map[string]any{"model": "sonnet", "effort": "high"}); code != 200 {
		t.Fatalf("pick: %d %s", code, body)
	}
	all := strings.Join(*ran, "\n")
	if !strings.Contains(all, "herdr agent prompt w2:p1 /model sonnet") || !strings.Contains(all, "herdr agent prompt w2:p1 /effort high") {
		t.Fatal(all)
	}
	// What was picked shows until the transcript has a newer answer.
	if view := read(); view.Model != "claude-sonnet" || view.Effort != "high" {
		t.Fatalf("after picking: %s %s", view.Model, view.Effort)
	}
	for _, bad := range []map[string]any{{"model": "gpt-5"}, {"effort": "turbo"}, {"model": "opus; rm -rf /"}, {}} {
		if code, _ := pick(bad); code == 200 {
			t.Fatalf("picked %v", bad)
		}
	}
	status = "working"
	if code, body := pick(map[string]any{"model": "opus"}); code == 200 || !strings.Contains(body, "busy") {
		t.Fatalf("picked while it works: %d %s", code, body)
	}
	status = "idle"

	// The app's admin keeps them: no picker, and no picking.
	var out strings.Builder
	if err := appModelsCommand([]string{"payments", "off"}, &out); err != nil || !strings.Contains(out.String(), "don't pick") {
		t.Fatalf("%v %s", err, out.String())
	}
	if view := read(); view.Choose {
		t.Fatal("the picker shows where the admin keeps them")
	}
	if code, body := pick(map[string]any{"effort": "low"}); code == 200 || !strings.Contains(body, "admin") {
		t.Fatalf("picked where the admin keeps them: %d %s", code, body)
	}
	if err := appModelsCommand([]string{"payments", "on"}, &out); err != nil {
		t.Fatal(err)
	}
	if config, _ := loadTeam("payments"); config.LockModels {
		t.Fatal("still kept")
	}
}
