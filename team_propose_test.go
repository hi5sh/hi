package main

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestTeamPropose(t *testing.T) {
	setupTeamTest(t)
	upstream := adoptTestTeam(t)
	adopted, _ := loadTeam("payments")
	b := newTeamTestBroker(t)
	config, _ := loadTeam("payments")
	config.Adopted = adopted.Adopted
	saveTeam(config)
	// Upstream moves on before the team proposes: the change goes on its latest.
	gitIn(t, upstream, "commit", "-q", "--allow-empty", "-m", "Upstream's latest")

	fork := filepath.Join(t.TempDir(), "fork.git")
	gitIn(t, filepath.Dir(fork), "init", "-q", "--bare", fork)
	var gh []string
	savedGH, savedLook, savedFork := teamGH, teamGHLookPath, teamForkURL
	teamGH = func(dir string, args ...string) (string, error) {
		gh = append(gh, strings.Join(args, " "))
		switch {
		case args[0] == "api" && args[1] == "user":
			return "octocat", nil
		case args[0] == "pr":
			return "https://github.com/ada/pocket-kanban/pull/42", nil
		}
		return "", nil
	}
	teamGHLookPath = func() (string, error) { return "/usr/bin/gh", nil }
	teamForkURL = func(name string) string {
		if name != "octocat/pocket-kanban" {
			t.Errorf("pushed to %s", name)
		}
		return fork
	}
	t.Cleanup(func() { teamGH, teamGHLookPath, teamForkURL = savedGH, savedLook, savedFork })

	merged := teamTestShip(t, b, "Add dark mode", map[string]string{"src/dark.css": "body { background: #000 }\n", "docs/map.md": "The team's map\n"})
	notes := "Adds a dark theme, chosen by the system setting. Tested in Firefox and Safari."

	if _, err := b.propose(teamProposeRequest{Task: merged.ID, Notes: notes}); err == nil || !strings.Contains(err.Error(), "owner") {
		t.Fatal("proposed without an owner", err)
	}
	if _, err := b.propose(teamProposeRequest{Task: merged.ID, owner: "Olga"}); err == nil || !strings.Contains(err.Error(), "--notes") {
		t.Fatal(err)
	}
	task, err := b.propose(teamProposeRequest{Task: merged.ID, owner: "Olga", Notes: notes})
	if err != nil {
		t.Fatal(err)
	}
	if task.Proposed != "https://github.com/ada/pocket-kanban/pull/42" || task.ProposeApproval == nil {
		t.Fatalf("%+v", task)
	}
	calls := strings.Join(gh, "\n")
	if !strings.Contains(calls, "repo fork ada/pocket-kanban --clone=false") ||
		!strings.Contains(calls, "pr create --repo ada/pocket-kanban --draft --head octocat:hi-team-payments-t-1 --base main --title Add dark mode --body "+notes) ||
		!strings.Contains(calls, "made by a team of AI agents") {
		t.Fatal(calls)
	}
	// The branch is upstream's latest and the change alone: no specs, map,
	// or rules of the team's.
	branch := "refs/heads/hi-team-payments-t-1"
	if boxGit(fork, "rev-parse", branch+"^") != boxGit(upstream, "rev-parse", "main") {
		t.Fatal("not on upstream's latest")
	}
	files := boxGit(fork, "diff", "--name-only", branch+"^", branch)
	if files != "src/dark.css" || boxGit(fork, "show", branch+":AGENTS.md") != "Run npm test before you finish." {
		t.Fatal(files)
	}
	if subject := boxGit(fork, "log", "-1", "--format=%s%n%b", branch); !strings.Contains(subject, "Add dark mode") || !strings.Contains(subject, "From the payments team's work (t-1)") {
		t.Fatal(subject)
	}
	if boxGit(b.repo, "worktree", "list", "--porcelain") != "worktree "+b.repo+"\nHEAD "+boxGit(b.repo, "rev-parse", "HEAD")+"\nbranch refs/heads/main" {
		t.Fatal("a worktree is left:", boxGit(b.repo, "worktree", "list"))
	}
	if _, err := b.propose(teamProposeRequest{Task: merged.ID, owner: "Olga", Notes: notes}); err == nil || !strings.Contains(err.Error(), "proposed already") {
		t.Fatal(err)
	}

	// Work that doesn't apply to upstream's latest waits for it to be brought in.
	conflicting := teamTestShip(t, b, "Rename the app", map[string]string{"README.md": "# The team's kanban\n"})
	writeSkillTestFile(t, filepath.Join(upstream, "README.md"), "# pocket-kanban, renamed upstream\n", 0o644)
	gitIn(t, upstream, "commit", "-q", "-am", "Rename")
	if _, err := b.propose(teamProposeRequest{Task: conflicting.ID, owner: "Olga", Notes: notes}); err == nil || !strings.Contains(err.Error(), "Bring upstream in first") {
		t.Fatal(err)
	}
	if out, _ := exec.Command("git", "-C", b.repo, "worktree", "list").CombinedOutput(); strings.Count(string(out), "\n") != 1 {
		t.Fatal("a worktree is left:", string(out))
	}

	// Only merged work, and only for a project from GitHub.
	saveTeamTask("payments", teamTask{ID: "t-5", Kind: "task", Status: "done", Title: "Not merged"})
	if _, err := b.propose(teamProposeRequest{Task: "t-5", owner: "Olga", Notes: notes}); err == nil || !strings.Contains(err.Error(), "isn't merged") {
		t.Fatal(err)
	}
	config.Adopted.GitHub = ""
	saveTeam(config)
	if _, err := b.propose(teamProposeRequest{Task: merged.ID, owner: "Olga", Notes: notes}); err == nil || !strings.Contains(err.Error(), "isn't on GitHub") {
		t.Fatal(err)
	}
}

func TestTeamMergedWork(t *testing.T) {
	tasks := []teamTask{
		{ID: "t-1", Kind: "task", Status: "merged"},
		{ID: "t-2", Kind: "review", Review: "t-1", Status: "done"},
		{ID: "t-3", Kind: "task", On: "t-1", Status: "merged", Merged: "abc"},
		{ID: "t-4", Kind: "task", Status: "done"},
	}
	for id, want := range map[string]string{"t-1": "t-3", "t-3": "t-3"} {
		if work, err := teamMergedWork(tasks, id); err != nil || work.ID != want {
			t.Errorf("%s: %s %v", id, work.ID, err)
		}
	}
	for id, want := range map[string]string{"t-2": "is a review", "t-4": "isn't merged", "t-9": "no task"} {
		if _, err := teamMergedWork(tasks, id); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v", id, err)
		}
	}
}
