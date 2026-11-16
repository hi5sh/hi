package cli

import (
	"bytes"
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
