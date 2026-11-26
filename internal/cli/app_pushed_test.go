package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// appTestRemote gives team payments a remote, a bare repository standing
// in for its GitLab project, with dev and production pushed, and returns
// a clone of it: a developer's own machine.
func appTestRemote(t *testing.T, b *appTestBroker) (remote, laptop string) {
	t.Helper()
	remote = filepath.Join(t.TempDir(), "payments.git")
	gitIn(t, filepath.Dir(remote), "init", "-q", "--bare", remote)
	gitIn(t, b.repo, "remote", "add", "origin", remote)
	boxGit(b.repo, "branch", "-f", appProductionBranch, "main")
	gitIn(t, b.repo, "push", "-q", "origin", "main", appProductionBranch)
	config, _ := loadTeam("payments")
	config.Remote = "https://gitlab.com/acme/payments.git"
	if err := saveTeam(config); err != nil {
		t.Fatal(err)
	}
	// The forge is the folder: git reads it plainly, as it would GitLab.
	saved := appForgeGit
	appForgeGit = func(repo, _ string, args ...string) (string, error) {
		args = append([]string{"-C", repo}, args...)
		for i, arg := range args {
			if arg == "origin" {
				args[i] = remote
			}
		}
		out, err := exec.Command("git", args...).CombinedOutput()
		return strings.TrimSpace(string(out)), err
	}
	t.Cleanup(func() { appForgeGit = saved })
	laptop = filepath.Join(t.TempDir(), "laptop")
	if out, err := exec.Command("git", "clone", "-q", remote, laptop).CombinedOutput(); err != nil {
		t.Fatal(string(out))
	}
	gitIn(t, laptop, "config", "user.name", "Ada Dev")
	gitIn(t, laptop, "config", "user.email", "ada@example.com")
	return remote, laptop
}

func appTestPush(t *testing.T, laptop, branch, file, text, subject string) {
	t.Helper()
	gitIn(t, laptop, "fetch", "-q", "origin")
	gitIn(t, laptop, "checkout", "-q", "-B", branch, "origin/main")
	os.MkdirAll(filepath.Dir(filepath.Join(laptop, file)), 0o700)
	os.WriteFile(filepath.Join(laptop, file), []byte(text), 0o600)
	gitIn(t, laptop, "add", "-A")
	gitIn(t, laptop, "commit", "-q", "--author", "Ada Dev <ada@example.com>", "-m", subject)
	gitIn(t, laptop, "push", "-q", "-f", "origin", branch)
}

// A branch someone pushes from their own machine shows up, goes on the
// preview, and into dev as their work, with the dev box's checks.
func TestTeamPushedBranches(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	appTestRunnable(b)
	_, laptop := appTestRemote(t, b)
	config, _ := loadTeam("payments")

	appTestPush(t, laptop, "local-dev", "src/chart.ts", "export const days = 31\n", "Show the last day on the chart")
	if _, tell, err := fetchTeamPushed(config, false); err != nil || tell != "" {
		t.Fatal(err, tell)
	}
	view := appPushedBranches(config)
	if view.Forge != "GitLab" || len(view.Branches) != 1 {
		t.Fatalf("%+v", view)
	}
	branch := view.Branches[0]
	if branch.Name != "local-dev" || branch.Author != "Ada Dev" || branch.Ahead != 1 || len(branch.Files) != 1 || branch.Files[0] != "src/chart.ts" || branch.Dev {
		t.Fatalf("%+v", branch)
	}
	// The dev box's clone reads it from the team's repository.
	if boxGit(b.repo, "rev-parse", "refs/heads/pushed/local-dev") != branch.Commit {
		t.Fatal("not kept as pushed/local-dev")
	}
	var out bytes.Buffer
	if err := appBranchesCommand([]string{"payments"}, &out); err != nil || !strings.Contains(out.String(), "local-dev") || !strings.Contains(out.String(), "pushed by Ada Dev") {
		t.Fatal(out.String(), err)
	}

	state, err := b.previewPushedBranch("local-dev")
	if err != nil || state.Source != "pushed/local-dev" || !strings.Contains(state.Label, "pushed by Ada Dev") {
		t.Fatalf("%+v %v", state, err)
	}
	saveTeamSlot("payments", state)
	if status, _ := b.devStatus(); status.PreviewBranch != "pushed/local-dev" {
		t.Fatalf("%+v", status)
	}

	result, err := b.shipPushedBranch(appDevShipRequest{Branch: "local-dev", By: "Bo"})
	if err != nil {
		t.Fatal(err)
	}
	task := result.Task
	if task.Branch != "pushed/local-dev" || task.Agent != "pushed to GitLab by Ada Dev" || task.Error != "" {
		t.Fatalf("%+v", task)
	}
	if got := appTaskAskedBy(task); got != "pushed to GitLab by Ada Dev" {
		t.Fatal(got)
	}
	if msg := boxGit(b.repo, "log", "-1", "--format=%B", "main"); !strings.Contains(msg, "From local-dev, pushed to GitLab by Ada Dev.") {
		t.Fatal(msg)
	}
	// Pushed to the remote: a merge request there shows as merged.
	if boxGit(b.repo, "ls-remote", "origin", "main") == "" || !strings.HasPrefix(boxGit(b.repo, "ls-remote", "origin", "refs/heads/main"), boxGit(b.repo, "rev-parse", "main")) {
		t.Fatal("dev wasn't pushed")
	}
	if _, err := b.shipPushedBranch(appDevShipRequest{Branch: "local-dev"}); err == nil || !strings.Contains(err.Error(), "already") {
		t.Fatal("added twice:", err)
	}
	if view := appPushedBranches(config); view.Branches[0].Ahead != 0 || !view.Branches[0].Shipped {
		t.Fatalf("%+v", view.Branches[0])
	}

	// A clash says how to fix it where it was pushed from.
	gitIn(t, laptop, "checkout", "-q", "-B", "week", "local-dev~1")
	os.MkdirAll(filepath.Join(laptop, "src"), 0o700)
	os.WriteFile(filepath.Join(laptop, "src/chart.ts"), []byte("export const days = 7\n"), 0o600)
	gitIn(t, laptop, "add", "-A")
	gitIn(t, laptop, "commit", "-q", "-m", "Show a week")
	gitIn(t, laptop, "push", "-q", "origin", "week")
	fetchTeamPushed(config, false)
	if _, err := b.shipPushedBranch(appDevShipRequest{Branch: "week"}); err == nil || !strings.Contains(err.Error(), "on the machine it was pushed from") {
		t.Fatal(err)
	} else if answer, ok := b.devConflictAnswer(err); !ok || answer["conflict"] == nil {
		t.Fatalf("%+v", answer)
	}
	if status := boxGit(b.repo, "status", "--porcelain"); status != "" {
		t.Fatal("a merge was left half done:", status)
	}

	// Deleted on the remote: gone here too.
	gitIn(t, laptop, "push", "-q", "origin", "--delete", "week")
	fetchTeamPushed(config, false)
	if boxGit(b.repo, "rev-parse", "--verify", "-q", "refs/heads/pushed/week") != "" {
		t.Fatal("pushed/week stayed")
	}
	if _, err := appPushedFind(config, "nope"); err == nil {
		t.Fatal("found a branch nobody pushed")
	}
}

// Dev's own branch pushed straight to: one more branch to add, and hi's
// next push is a fast-forward of it. Production pushed: told, not taken.
func TestTeamPushedMainAndProduction(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	appTestRunnable(b)
	_, laptop := appTestRemote(t, b)
	config, _ := loadTeam("payments")

	gitIn(t, laptop, "checkout", "-q", "main")
	os.WriteFile(filepath.Join(laptop, "hotfix.txt"), []byte("fix\n"), 0o600)
	gitIn(t, laptop, "add", "-A")
	gitIn(t, laptop, "commit", "-q", "-m", "A hot fix")
	gitIn(t, laptop, "push", "-q", "origin", "main")
	gitIn(t, laptop, "push", "-q", "-f", "origin", "main:"+appProductionBranch)
	_, tell, err := fetchTeamPushed(config, false)
	if err != nil || !strings.Contains(tell, "Someone pushed to *production* on GitLab") {
		t.Fatal(err, tell)
	}
	if _, again, _ := fetchTeamPushed(config, false); again != "" {
		t.Fatal("told twice:", again)
	}
	if boxGit(b.repo, "rev-parse", "--verify", "-q", "refs/heads/pushed/production") != "" {
		t.Fatal("production taken")
	}
	view := appPushedBranches(config)
	if len(view.Branches) != 1 || view.Branches[0].Name != "main" || !view.Branches[0].Dev {
		t.Fatalf("%+v", view)
	}
	result, err := b.shipPushedBranch(appDevShipRequest{Branch: "main"})
	if err != nil || result.Task.Error != "" {
		t.Fatalf("%+v %v", result.Task, err)
	}
	// After hi's push, the remote's main is dev's again: nothing waits.
	fetchTeamPushed(config, false)
	if view := appPushedBranches(config); len(view.Branches) != 0 {
		t.Fatalf("%+v", view)
	}
}

func TestAppForgeGitArgs(t *testing.T) {
	join := func(remote string) string { return strings.Join(appForgeGitArgs(remote), " ") }
	if got := join("https://gitlab.com/acme/x.git"); !strings.Contains(got, "credential.helper= ") || !strings.Contains(got, "credential.https://gitlab.com.helper=!glab auth git-credential") {
		t.Fatal(got)
	}
	if got := join("https://github.com/acme/x"); !strings.Contains(got, "credential.https://github.com.helper=!gh auth git-credential") {
		t.Fatal(got)
	}
	if got := join("gitlab.example.com/team/x"); !strings.Contains(got, "credential.https://gitlab.example.com.helper=!glab") {
		t.Fatal(got)
	}
	if got := join("/srv/x"); strings.Contains(got, ".helper=!") {
		t.Fatal(got)
	}
	if appForgeName("https://github.com/a/b") != "GitHub" || appForgeName("https://gitlab.example.com/a/b") != "GitLab" || appForgeName("") != "the remote" {
		t.Fatal("forge names")
	}
}
