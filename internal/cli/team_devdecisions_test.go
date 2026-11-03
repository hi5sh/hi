package cli

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// The Decisions tab reads docs/decisions/ from the team's branch, and an
// edit, a new one, or a delete is a commit there.
func TestTeamDevPageDecisions(t *testing.T) {
	b, _ := teamTestDevPage(t)
	handler := b.devPageHandler()
	list := func() []teamDecision {
		t.Helper()
		code, body := teamTestDevPageCall(t, handler, teamTestOwnerKey, "GET", "api/decisions", nil)
		var decisions []teamDecision
		if code != 200 || json.Unmarshal([]byte(body), &decisions) != nil {
			t.Fatal(code, body)
		}
		return decisions
	}
	save := func(body map[string]any) (int, string) {
		return teamTestDevPageCall(t, handler, teamTestOwnerKey, "POST", "api/decision", body)
	}
	writeSkillTestFile(t, b.repo+"/docs/decisions/README.md", "# Decisions\n", 0o644)
	writeSkillTestFile(t, b.repo+"/docs/decisions/0001-sqlite.md", "# Use SQLite\n\nOne file, no **server** to run.\n\nWhy: the app is small.\n", 0o644)
	teamGit(b.repo, "add", "-A")
	teamGit(b.repo, "commit", "-q", "-m", "a decision")

	decisions := list()
	if len(decisions) != 1 || decisions[0].File != "0001-sqlite.md" || decisions[0].Number != "0001" || decisions[0].Title != "Use SQLite" ||
		decisions[0].Summary != "One file, no server to run." || decisions[0].Added.IsZero() || decisions[0].Blob == "" {
		t.Fatalf("%+v", decisions)
	}

	// A new one is numbered after the last, and named from its title.
	if code, body := save(map[string]any{"text": "# Prices in cents\n\nFloats round."}); code != 200 || !strings.Contains(body, "0002-prices-in-cents.md") {
		t.Fatal(code, body)
	}
	if code, body := save(map[string]any{"text": "No title"}); code != http.StatusBadRequest {
		t.Fatal("a decision with no title:", code, body)
	}
	decisions = list()
	if len(decisions) != 2 || decisions[0].File != "0002-prices-in-cents.md" {
		t.Fatalf("%+v", decisions)
	}
	if log := boxGit(b.repo, "log", "-1", "--format=%B"); !strings.Contains(log, "Add the decision 0002-prices-in-cents.md") || !strings.Contains(log, "Olga on the dev page") {
		t.Fatal(log)
	}

	// An edit over the file as the page read it; one over a newer change is refused.
	sqlite := decisions[1]
	if code, body := save(map[string]any{"file": sqlite.File, "blob": sqlite.Blob, "text": "# Use SQLite\n\nOne file."}); code != 200 {
		t.Fatal(code, body)
	}
	if code, body := save(map[string]any{"file": sqlite.File, "blob": sqlite.Blob, "text": "# Use Postgres"}); code != http.StatusBadRequest || !strings.Contains(body, "changed") {
		t.Fatal("a stale edit:", code, body)
	}
	if text := list()[1].Text; text != "# Use SQLite\n\nOne file." {
		t.Fatal(text)
	}

	// Delete; the README isn't a decision to delete.
	if code, body := save(map[string]any{"file": "README.md", "delete": true}); code != http.StatusBadRequest {
		t.Fatal("deleted the README:", code, body)
	}
	if code, body := save(map[string]any{"file": sqlite.File, "blob": list()[1].Blob, "delete": true}); code != 200 {
		t.Fatal(code, body)
	}
	if decisions := list(); len(decisions) != 1 || boxGit(b.repo, "ls-files", "docs/decisions/0001-sqlite.md") != "" || boxGit(b.repo, "status", "--porcelain") != "" {
		t.Fatalf("%+v", decisions)
	}

	// Someone with no level can't read them.
	if code, _ := teamTestDevPageCall(t, handler, teamTestAskerKey, "GET", "api/decisions", nil); code != http.StatusUnauthorized {
		t.Fatal("an asker read the decisions")
	}
}
