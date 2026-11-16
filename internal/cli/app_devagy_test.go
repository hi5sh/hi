package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const appTestAgyToken = `{"token":{"access_token":"ya29.a","token_type":"Bearer","refresh_token":"1//app-refresh","expiry":"2026-11-16T12:00:00Z","expires_in":3599},"auth_method":"consumer","id_token_email":"payments-agy@corp.example"}`

// agy in the dev box: none without the app's sign-in; with it, agy in
// /box/bin, the sign-in in the box's home (agy's own refreshes of it
// kept), the box's folders trusted, Google's agy sites allowed, and the
// page offering it to start. --unset takes all of it away again.
func TestAppDevAgy(t *testing.T) {
	// The machine's agy sign-in is under HOME: never the real one.
	t.Setenv("HOME", t.TempDir())
	b, ran := appTestDevPage(t)
	bin := t.TempDir()
	os.WriteFile(filepath.Join(bin, "agy"), []byte("#!/bin/sh\n"), 0o755)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	config, _ := loadTeam("payments")
	home := appDevHome("payments")
	state := filepath.Join(home, ".gemini", "antigravity-cli")

	if has, err := prepareTeamDevAgy("payments"); err != nil || has || appDevHasAgy("payments") {
		t.Fatal("agy without a sign-in:", has, err)
	}
	saveTeamSecrets("payments", appSecrets{SlackBotToken: "xoxb-1"})
	if err := appDevAgentOK("payments", "agy", false); err == nil || !strings.Contains(err.Error(), "hi app signin payments agy") {
		t.Fatal(err)
	}
	if slices.Contains(appDevAllowHosts("payments"), "daily-cloudcode-pa.googleapis.com") {
		t.Fatal("agy's sites without agy")
	}

	// The app's own Google account.
	file := filepath.Join(t.TempDir(), "antigravity-oauth-token")
	os.WriteFile(file, []byte(appTestAgyToken), 0o600)
	var out bytes.Buffer
	if err := appSignin([]string{"payments", "agy", "--file", filepath.Join(t.TempDir(), "none")}, nil, &out); err == nil {
		t.Fatal("a file that isn't there")
	}
	os.WriteFile(filepath.Join(t.TempDir(), "x"), []byte("{}"), 0o600)
	bad := filepath.Join(t.TempDir(), "bad")
	os.WriteFile(bad, []byte(`{"token":{}}`), 0o600)
	if err := appSignin([]string{"payments", "agy", "--file", bad}, nil, &out); err == nil || !strings.Contains(err.Error(), "isn't agy's sign-in") {
		t.Fatal(err)
	}
	if err := appSignin([]string{"payments", "agy", "--file", file}, nil, &out); err != nil || !strings.Contains(out.String(), "payments-agy@corp.example") {
		t.Fatal(err, out.String())
	}
	os.MkdirAll(state, 0o700)
	os.WriteFile(filepath.Join(state, "settings.json"), []byte(`{"theme":"dark"}`), 0o600)
	if has, err := prepareTeamDevAgy("payments"); err != nil || !has || !appDevHasAgy("payments") {
		t.Fatal(has, err)
	}
	if data, _ := os.ReadFile(filepath.Join(state, "antigravity-oauth-token")); string(data) != appTestAgyToken {
		t.Fatal(string(data))
	}
	if data, _ := os.ReadFile(filepath.Join(state, "settings.json")); !strings.Contains(string(data), `"theme": "dark"`) || !strings.Contains(string(data), "/box/repo") {
		t.Fatal(string(data))
	}
	// agy refreshed its access token in the box: that stays.
	refreshed := strings.Replace(appTestAgyToken, "ya29.a", "ya29.b", 1)
	os.WriteFile(filepath.Join(state, "antigravity-oauth-token"), []byte(refreshed), 0o600)
	prepareTeamDevAgy("payments")
	if data, _ := os.ReadFile(filepath.Join(state, "antigravity-oauth-token")); string(data) != refreshed {
		t.Fatal("agy's refresh was undone")
	}
	if hosts := appDevAllowHosts("payments"); !slices.Contains(hosts, "daily-cloudcode-pa.googleapis.com") || !slices.Contains(hosts, "oauth2.googleapis.com") {
		t.Fatal(hosts)
	}
	if b.devPageView(appPersonKey{User: "U07OWNER1"}, appLevelOwner).Agy != true {
		t.Fatal("the page doesn't offer agy")
	}
	// Started from a task, it skips its own permission questions: the box
	// is the boundary.
	if err := appDevAgentOK("payments", "agy", false); err != nil {
		t.Fatal(err)
	}
	if got := appDevAgentCommand("agy", "Work on task #1"); got != "agy --dangerously-skip-permissions -i 'Work on task #1'" {
		t.Fatal(got)
	}
	appTicketDo("payments", appTicketRequest{Action: "add", Title: "Chart", Author: "Olga"})
	if _, _, err := appDevStartIn(config, 1, "agy", "Olga"); err != nil {
		t.Fatal(err)
	}
	if all := strings.Join(*ran, "\n"); !strings.Contains(all, "agy --dangerously-skip-permissions -i 'Work on task #1") {
		t.Fatal(all)
	}
	// No first-run questions: the color scheme, and trusting the worktree.
	if data, _ := os.ReadFile(filepath.Join(state, "cache", "onboarding.json")); !strings.Contains(string(data), `"onboardingComplete": true`) {
		t.Fatal("onboarding:", string(data))
	}
	if data, _ := os.ReadFile(filepath.Join(state, "settings.json")); !strings.Contains(string(data), "/box/home/.herdr/worktrees/repo/1-chart") {
		t.Fatal("the worktree isn't trusted:", string(data))
	}
	secrets, _ := loadTeamSecrets("payments")
	if state := appAgyState(secrets); !strings.Contains(state, "The app's own (payments-agy@corp.example)") {
		t.Fatal(state)
	}

	// No sign-in again: hi's goes from the box, and agy with it.
	out.Reset()
	if err := appSignin([]string{"payments", "agy", "--unset"}, nil, &out); err != nil {
		t.Fatal(err)
	}
	if has, _ := prepareTeamDevAgy("payments"); has || appDevHasAgy("payments") || fileExists(filepath.Join(state, "antigravity-oauth-token")) {
		t.Fatal("agy stayed")
	}
	// A sign-in an owner made in the box themselves isn't hi's to take.
	os.WriteFile(filepath.Join(state, "antigravity-oauth-token"), []byte(appTestAgyToken), 0o600)
	prepareTeamDevAgy("payments")
	if !fileExists(filepath.Join(state, "antigravity-oauth-token")) {
		t.Fatal("an owner's own sign-in went")
	}

	// This machine's, with a warning.
	machine := agyTokenPath()
	if !strings.HasPrefix(machine, os.Getenv("HOME")) || os.Getenv("HOME") == "" {
		t.Fatalf("agy's sign-in at %s, outside the test's home", machine)
	}
	os.MkdirAll(filepath.Dir(machine), 0o700)
	os.WriteFile(machine, []byte(strings.Replace(appTestAgyToken, "payments-agy", "owner", 1)), 0o600)
	out.Reset()
	if err := appSignin([]string{"payments", "agy", "--machine"}, nil, &out); err != nil || !strings.Contains(out.String(), "can copy this machine's Google sign-in (owner@corp.example)") {
		t.Fatal(err, out.String())
	}
	if token, _ := appAgyToken("payments"); !strings.Contains(token, "owner@corp.example") {
		t.Fatal(token)
	}
}

// agy's transcript in Chat: the person's request, its commands and reads
// with their results (each the step after its call), its question, and
// waiting while the question has no answer; its sessions with Resume.
func TestAppDevAgyChat(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	appTestHooksReset(t)
	b, ran := appTestDevPage(t)
	handler := b.devPageHandler()
	cwd := "/box/home/.herdr/worktrees/repo/1-chart"
	id := "7213ebda-c25a-4a04-b509-e588d68b558c"
	session := appDevAgyBrain + "/" + id + appDevAgyTranscript
	host, _ := appDevHostPath("payments", session)
	os.MkdirAll(filepath.Dir(host), 0o700)
	lines := []string{
		`{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","status":"DONE","created_at":"2026-11-16T15:58:40Z","content":"<USER_REQUEST>\nWork on task #1: the chart\n</USER_REQUEST>\n<ADDITIONAL_METADATA>\nThe current local time is: now.\n</ADDITIONAL_METADATA>"}`,
		`{"step_index":1,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","created_at":"2026-11-16T15:58:41Z","tool_calls":[{"name":"run_command","args":{"CommandLine":"\"pwd && ls\"","toolSummary":"\"Look around\""}},{"name":"read_file","args":{"Path":"\"` + cwd + `/web/chart.js\""}}]}`,
		`{"step_index":2,"source":"MODEL","type":"GENERIC","status":"DONE","created_at":"2026-11-16T15:58:42Z","content":"Created At: x\nCompleted At: y\nCommand exited with code 0.\n\nCommand output:\n` + cwd + `\r\n"}`,
		`{"step_index":3,"source":"MODEL","type":"GENERIC","status":"DONE","created_at":"2026-11-16T15:58:43Z","content":"Created At: x\nCompleted At: y\nShowing lines 1-2 of 2\n1: draw()"}`,
		`{"step_index":4,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","created_at":"2026-11-16T15:58:44Z","thinking":"Which green?","tool_calls":[{"name":"ask_question","args":{"questions":"[{\"question\":\"Which green?\",\"options\":[\"Theme\",\"Literal\"]}]"}}]}`,
	}
	os.WriteFile(host, []byte(strings.Join(lines, "\n")+"\n"), 0o600)

	running := true
	previous := appDevBoxExec
	appDevBoxExec = func(team string, command ...string) (string, error) {
		switch strings.Join(command[:min(3, len(command))], " ") {
		case "herdr pane list":
			if !running {
				return `{"result":{"panes":[{"pane_id":"w2:p1","cwd":"` + cwd + `"}]}}`, nil
			}
			return `{"result":{"panes":[{"pane_id":"w2:p1","agent":"agy","agent_status":"working","cwd":"` + cwd + `"}]}}`, nil
		case "herdr pane read":
			return "Question 1/1: Which green?\n\n> 1. Theme\n  2. Literal\n  3. Write-in...\n\n↑/↓ Navigate · enter Select", nil
		}
		return previous(team, command...)
	}
	t.Cleanup(func() { appDevBoxExec = previous })

	var view appDevChatView
	code, body := appTestDevPageCall(t, handler, appTestOwnerKey, "GET", "api/chat?workspace=w2", nil)
	if code != 200 || json.Unmarshal([]byte(body), &view) != nil {
		t.Fatal(code, body)
	}
	if view.SessionAgent != "agy" || view.ID != id || view.Note != "" || view.Agent.Status != "blocked" || view.Agent.Waiting != "a question" || view.Screen == "" {
		t.Fatalf("%+v", view)
	}
	// Its question, as options to tap: agy's are picked with the arrows.
	if view.Ask == nil || len(view.Ask.Options) != 3 || !view.Ask.Options[0].Cursor || !view.Ask.Options[2].Typed {
		t.Fatalf("%+v", view.Ask)
	}
	if keys, typed, _ := appDevAskKeys(view.Ask, "agy", 1, ""); strings.Join(keys, " ") != "down enter" || typed {
		t.Fatal(keys, typed)
	}
	if keys, typed, _ := appDevAskKeys(view.Ask, "agy", 2, "Use the theme's"); strings.Join(keys, " ") != "down down enter" || !typed {
		t.Fatal(keys, typed)
	}
	var got []string
	for _, entry := range view.Entries {
		got = append(got, entry.T+"|"+entry.ID+"|"+entry.Name+"|"+firstNonEmpty(entry.Summary, entry.Text))
	}
	want := []string{
		"user|||Work on task #1: the chart",
		"tool|2|Bash|Look around",
		"tool|3|Read|" + cwd + "/web/chart.js",
		"result|2||Command exited with code 0.\n\nCommand output:\n" + cwd,
		"result|3||Showing lines 1-2 of 2\n1: draw()",
		"thinking|||Which green?",
		"tool|5|Question|Which green?",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got\n%s", strings.Join(got, "\n"))
	}
	// Answered: no longer waiting.
	f, _ := os.OpenFile(host, os.O_APPEND|os.O_WRONLY, 0o600)
	f.WriteString(`{"step_index":5,"source":"MODEL","type":"GENERIC","status":"DONE","created_at":"2026-11-16T15:59:00Z","content":"A1: Theme"}` + "\n")
	f.Close()
	if appDevAgyAsking("payments", host) {
		t.Fatal("still asking after the answer")
	}
	// Its sessions, and Resume, trusting the folder first.
	code, body = appTestDevPageCall(t, handler, appTestOwnerKey, "GET", "api/chat/sessions?workspace=w2", nil)
	var list struct{ Sessions []appDevSession }
	if json.Unmarshal([]byte(body), &list) != nil || len(list.Sessions) != 1 || list.Sessions[0].Agent != "agy" || list.Sessions[0].Title != "Work on task #1: the chart" {
		t.Fatal(code, body)
	}
	running = false
	os.MkdirAll(filepath.Join(appDevDir("payments"), "bin"), 0o700)
	os.WriteFile(filepath.Join(appDevDir("payments"), "bin", "agy"), []byte("#!/bin/sh\n"), 0o755)
	if code, body := appTestDevPageCall(t, handler, appTestOwnerKey, "POST", "api/chat/resume", map[string]any{"workspace": "w2", "session": id, "agent": "agy"}); code != 200 {
		t.Fatal(code, body)
	}
	if all := strings.Join(*ran, "\n"); !strings.Contains(all, "agy --dangerously-skip-permissions --conversation "+id) {
		t.Fatal(all)
	}
	settings, _ := os.ReadFile(filepath.Join(appDevHome("payments"), ".gemini", "antigravity-cli", "settings.json"))
	if !strings.Contains(string(settings), cwd) {
		t.Fatal("not trusted:", string(settings))
	}
	// A path that isn't a transcript isn't one.
	if appDevIsAgySession(appDevAgyBrain+"/../x"+appDevAgyTranscript) || appDevIsAgySession(appDevAgyBrain+"/a/b"+appDevAgyTranscript) {
		t.Fatal("not a transcript")
	}
}
