package cli

import (
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestAppGitLabNames(t *testing.T) {
	for name, want := range map[string]string{
		"acme/billing":            "gitlab.com acme/billing",
		"gitlab.com/acme/billing": "gitlab.com acme/billing",
		"https://gitlab.example.com/team/sub/billing.git": "gitlab.example.com team/sub/billing",
	} {
		host, path, err := appGitLabParts(name)
		if err != nil || host+" "+path != want {
			t.Errorf("%s: %s %s %v", name, host, path, err)
		}
	}
	for _, bad := range []string{"billing", "gitlab.com/billing", "gitlab.com/acme/bil ling"} {
		if _, _, err := appGitLabParts(bad); err == nil {
			t.Errorf("%s was taken", bad)
		}
	}
	for answer, gitlab := range map[string]bool{"octocat/world-clock": false, "gitlab.com/acme/billing": true, "git.example.org/a/b": true, "github.com/a/b": false} {
		if appIsGitLabAnswer(answer) != gitlab {
			t.Errorf("%s: gitlab %v", answer, !gitlab)
		}
	}
}

// The page's Code step takes GitHub or GitLab, checked with glab's
// sign-in for the project's host.
func TestAdminNewAppOnGitLab(t *testing.T) {
	a, olga := newAppTest(t)
	glab, look := appGlab, appGlabLookPath
	var asked []string
	appGlab = func(host, _ string, args ...string) (string, error) {
		asked = append(asked, host+" "+strings.Join(args, " "))
		if host == "gitlab.example.com" {
			return "", errors.New("not logged in")
		}
		return "Logged in", nil
	}
	appGlabLookPath = func() (string, error) { return "/usr/bin/glab", nil }
	t.Cleanup(func() { appGlab, appGlabLookPath = glab, look })
	var view adminDraftView
	if code := a.post(t, olga, "/api/new", map[string]any{"action": "start", "name": "billing"}, &view); code != http.StatusOK {
		t.Fatal(code)
	}
	var out map[string]any
	step := func(code map[string]any) int {
		return a.post(t, olga, "/api/new", map[string]any{"action": "step", "name": "billing", "step": "code", "code": code}, &out)
	}
	if status := step(map[string]any{"kind": "new", "github": "octocat/billing", "gitlab": "acme/billing"}); status != http.StatusBadRequest {
		t.Fatalf("both: %d", status)
	}
	if status := step(map[string]any{"kind": "new", "gitlab": "gitlab.example.com/team/billing"}); status != http.StatusBadRequest || !strings.Contains(out["error"].(string), "glab auth login --hostname gitlab.example.com") {
		t.Fatalf("not signed in: %d %v", status, out)
	}
	if status := step(map[string]any{"kind": "new", "gitlab": "acme/billing"}); status != http.StatusOK {
		t.Fatalf("gitlab.com: %d %v", status, out)
	}
	draft, _ := loadAdminDraft("billing")
	if draft.Answers.GitLab != "acme/billing" || draft.Answers.GitHub != "" || !strings.Contains(strings.Join(asked, "|"), "gitlab.com auth status --hostname gitlab.com") {
		t.Fatalf("answers: %+v %v", draft.Answers, asked)
	}
}
