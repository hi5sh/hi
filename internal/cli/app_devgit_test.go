package cli

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestAppDevGitParse(t *testing.T) {
	out := strings.Join([]string{
		"12-search\n",
		"M  web/app.js\x00 M README.md\x00R  docs/new.md\x00docs/old.md\x00?? notes/todo.txt\x00UU go.mod\x00AM api/x.go\x00",
		"3\t1\tREADME.md\x002\t0\tapi/x.go\x00",
		"5\t2\tweb/app.js\x000\t0\t\x00docs/old.md\x00docs/new.md\x00-\t-\tapi/x.go\x00",
		"aaa\x1fa1\x1fSearch box\x1fdev box\x1f1700000100\x1e\nbbb\x1fb1\x1fStart\x1fdev box\x1f1700000000\x1e\n",
		"aaa\n",
		"4\n",
	}, "\035")
	view := appDevGitParse(out)
	if view.Branch != "12-search" || view.Ahead != 1 || view.Behind != 4 {
		t.Fatalf("branch, ahead, behind: %+v", view)
	}
	wantStaged := []appDevGitFile{
		{Path: "web/app.js", Status: "M", Added: 5, Removed: 2},
		{Path: "docs/new.md", Old: "docs/old.md", Status: "R"},
		{Path: "api/x.go", Status: "A", Binary: true},
	}
	if !reflect.DeepEqual(view.Staged, wantStaged) {
		t.Errorf("staged: %+v", view.Staged)
	}
	wantChanges := []appDevGitFile{
		{Path: "README.md", Status: "M", Added: 3, Removed: 1},
		{Path: "notes/todo.txt", Status: "?"},
		{Path: "api/x.go", Status: "M", Added: 2},
	}
	if !reflect.DeepEqual(view.Changes, wantChanges) {
		t.Errorf("changes: %+v", view.Changes)
	}
	if len(view.Conflicts) != 1 || view.Conflicts[0].Path != "go.mod" {
		t.Errorf("conflicts: %+v", view.Conflicts)
	}
	if len(view.Commits) != 2 || !view.Commits[0].New || view.Commits[1].New || view.Commits[0].Subject != "Search box" || view.Commits[1].At.Unix() != 1700000000 {
		t.Errorf("commits: %+v", view.Commits)
	}
	if empty := appDevGitParse(""); empty.Staged == nil || empty.Changes == nil || empty.Commits == nil {
		t.Errorf("an empty answer gives lists, not nulls: %+v", empty)
	}
}

func TestAppDevGitPath(t *testing.T) {
	for _, bad := range []string{"", ".", "..", "../x", "/etc/passwd", "a/../../x", ".git/config", ".git", "a\x00b"} {
		if _, err := appDevGitPath(bad); err == nil {
			t.Errorf("%q passed", bad)
		}
	}
	for in, want := range map[string]string{"a/b.go": "a/b.go", "a/./b.go": "a/b.go", ":(top)x": ":(top)x", "-rf": "-rf"} {
		if got, err := appDevGitPath(in); err != nil || got != want {
			t.Errorf("%q: %q %v", in, got, err)
		}
	}
}

// The scripts, on a real worktree: what the view lists, a file's diff,
// staging, discarding, and a commit.
func TestAppDevGitInAWorktree(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	config, _ := loadTeam("payments")
	config.Branch = "main"
	saveTeam(config)
	dir := t.TempDir()
	origin, work := filepath.Join(dir, "origin.git"), filepath.Join(dir, "work")
	git := func(where string, args ...string) {
		t.Helper()
		if out, err := exec.Command("git", append([]string{"-C", where, "-c", "user.name=t", "-c", "user.email=t@t", "-c", "init.defaultBranch=main"}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
	}
	git(dir, "init", "-q", "--bare", origin)
	git(dir, "clone", "-q", origin, work)
	os.WriteFile(filepath.Join(work, "a.txt"), []byte("one\ntwo\n"), 0o644)
	os.WriteFile(filepath.Join(work, "gone.txt"), []byte("x\n"), 0o644)
	git(work, "add", "-A")
	git(work, "commit", "-q", "-m", "First")
	git(work, "push", "-q", "origin", "main")
	git(work, "checkout", "-q", "-b", "2-more")
	git(work, "config", "user.name", "dev box")
	git(work, "config", "user.email", "dev-box@payments.invalid")

	appDevSpacesCache.Lock()
	appDevSpacesCache.team, appDevSpacesCache.at, appDevSpacesCache.spaces = "payments", time.Now().Add(time.Hour), []appDevSpace{{ID: "w2", Branch: "2-more", Path: work}}
	appDevSpacesCache.Unlock()
	t.Cleanup(func() { appDevSpacesCache.Lock(); appDevSpacesCache.team = ""; appDevSpacesCache.Unlock() })
	previous := appDevBoxExec
	appDevBoxExec = func(team string, command ...string) (string, error) {
		var stdout, stderr bytes.Buffer
		cmd := exec.Command(command[0], command[1:]...)
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		if err := cmd.Run(); err != nil {
			return stdout.String(), errors.New(strings.TrimSpace(stderr.String()))
		}
		return stdout.String(), nil
	}
	t.Cleanup(func() { appDevBoxExec = previous })

	os.WriteFile(filepath.Join(work, "a.txt"), []byte("one\n2\nthree\n"), 0o644)
	os.WriteFile(filepath.Join(work, "new.txt"), []byte("hello\n"), 0o644)
	os.Remove(filepath.Join(work, "gone.txt"))
	view, err := b.devGitStatus("w2")
	if err != nil {
		t.Fatal(err)
	}
	if view.Branch != "2-more" || len(view.Staged) != 0 || len(view.Changes) != 3 || view.Path != work || view.Base != "main" || len(view.Commits) != 1 || view.Commits[0].New {
		t.Fatalf("status: %+v", view)
	}
	if diff, err := b.devGitDiff("w2", "", []string{"a.txt"}); err != nil || !strings.Contains(diff, "-two\n+2\n+three") {
		t.Errorf("diff: %q %v", diff, err)
	}
	if diff, err := b.devGitDiff("w2", "new", []string{"new.txt"}); err != nil || !strings.Contains(diff, "+hello") {
		t.Errorf("a new file's diff: %q %v", diff, err)
	}
	if _, err := b.devGitDiff("w2", "new", []string{"../origin.git/config"}); err == nil {
		t.Error("a diff outside the worktree")
	}

	if err := b.devGitDo(appDevGitRequest{Workspace: "w2", Action: "stage", Paths: []string{"a.txt"}}); err != nil {
		t.Fatal(err)
	}
	if view, _ = b.devGitStatus("w2"); len(view.Staged) != 1 || view.Staged[0].Added != 2 || view.Staged[0].Removed != 1 || len(view.Changes) != 2 {
		t.Fatalf("after staging: %+v", view)
	}
	// Discarding the new file takes it out; the deleted one comes back.
	if err := b.devGitDo(appDevGitRequest{Workspace: "w2", Action: "discard", Paths: []string{"new.txt", "gone.txt"}}); err != nil {
		t.Fatal(err)
	}
	if fileExists(filepath.Join(work, "new.txt")) || !fileExists(filepath.Join(work, "gone.txt")) {
		t.Error("discard")
	}
	if err := b.devGitDo(appDevGitRequest{Workspace: "w2", Action: "commit", Message: " "}); err == nil {
		t.Error("a commit without a message")
	}
	if err := b.devGitDo(appDevGitRequest{Workspace: "w2", Action: "commit", Message: "Count to three"}); err != nil {
		t.Fatal(err)
	}
	if view, _ = b.devGitStatus("w2"); len(view.Staged)+len(view.Changes) != 0 || view.Ahead != 1 || !view.Commits[0].New || view.Commits[0].Subject != "Count to three" {
		t.Fatalf("after the commit: %+v", view)
	}
	if err := b.devGitDo(appDevGitRequest{Workspace: "w2", Action: "commit", Message: "Again"}); err == nil || !strings.Contains(err.Error(), "nothing to commit") {
		t.Errorf("an empty commit: %v", err)
	}
	// Nothing staged: a commit takes everything.
	os.WriteFile(filepath.Join(work, "b.txt"), []byte("b\n"), 0o644)
	if err := b.devGitDo(appDevGitRequest{Workspace: "w2", Action: "commit", Message: "Add b"}); err != nil {
		t.Fatal(err)
	}
	if view, _ = b.devGitStatus("w2"); len(view.Changes) != 0 || view.Ahead != 2 {
		t.Fatalf("after committing everything: %+v", view)
	}
	if _, err := b.devGitStatus("w9"); err == nil {
		t.Error("a workspace the box hasn't")
	}
}
