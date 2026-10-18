package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Contributing back: hi team propose offers the project a team adopted a
// piece of the team's merged work, as a draft pull request from a fork
// under the host's GitHub account, once an owner says yes. Only the
// work's own changes go: the team's specs, rules, and docs stay out, and
// the change is put on upstream's latest, where its maintainers would
// review it. A draft, so a person reads it before it's marked ready.

// teamProposeRequest is what the lead sends.
type teamProposeRequest struct {
	Task     string `json:"task"`
	Approval string `json:"approval"`
	Notes    string `json:"notes"`
}

// teamTeamOnlyPaths are the team's own files in an adopted project: they
// stay out of a proposal unless upstream has them too.
var teamTeamOnlyPaths = []string{teamSpecsPath, ".hi5", "AGENTS.md", "docs/map.md", "docs/user", "docs/tech", "docs/decisions"}

// teamProposePushTries and teamProposePushWait give a new fork time to
// be ready for the push.
var (
	teamProposePushTries = 4
	teamProposePushWait  = 5 * time.Second
)

// teamForkURL is where hi pushes the proposal's branch; tests replace it.
var teamForkURL = func(fork string) string { return "https://github.com/" + fork + ".git" }

// propose opens a draft pull request upstream with a merged task's work.
func (b *teamBroker) propose(request teamProposeRequest) (teamTask, error) {
	config, err := loadTeam(b.team)
	if err != nil {
		return teamTask{}, err
	}
	adopted := config.Adopted
	switch {
	case adopted == nil:
		return teamTask{}, errors.New("hi team propose is for a project the team adopted; this one has no upstream")
	case adopted.GitHub == "":
		return teamTask{}, fmt.Errorf("%s isn't on GitHub, so hi can't open a pull request there", adopted.Name)
	case strings.TrimSpace(request.Notes) == "":
		return teamTask{}, errors.New("--notes <file>: a description of the change for the project's maintainers")
	case len(request.Notes) > 20000:
		return teamTask{}, errors.New("keep the description under 20,000 characters")
	}
	work, err := teamMergedWork(loadTeamTasks(b.team), request.Task)
	if err != nil {
		return teamTask{}, err
	}
	if work.Proposed != "" {
		return work, fmt.Errorf("%s is proposed already: %s", work.ID, work.Proposed)
	}
	if _, brought := teamUpstreamWorkOf(loadTeamTasks(b.team), work); brought {
		return teamTask{}, fmt.Errorf("%s brought %s's own changes in; there's nothing of the team's in it to offer back", work.ID, adopted.Name)
	}
	approval, err := b.teamFindApproval(config, request.Approval, "propose")
	if err != nil {
		return teamTask{}, err
	}
	if _, err := teamGHLookPath(); err != nil {
		return teamTask{}, errors.New("contributing back needs gh on the machine, signed in: whoever runs hi there runs hi install gh, then gh auth login")
	}
	login, err := teamGH("", "api", "user", "--jq", ".login")
	if err != nil || login == "" {
		return teamTask{}, fmt.Errorf("gh isn't signed in on the machine: %s", qFirstLine(login, fmt.Sprint(err)))
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	if _, err := fetchTeamUpstream(config); err != nil {
		return teamTask{}, fmt.Errorf("couldn't fetch %s's latest: %w", adopted.Name, err)
	}
	follow := "refs/remotes/" + teamUpstream + "/" + adopted.Follow
	excluded := []string{"."}
	for _, path := range teamTeamOnlyPaths {
		if exec.Command("git", "-C", b.repo, "cat-file", "-e", follow+":"+path).Run() != nil {
			excluded = append(excluded, ":(exclude)"+path)
		}
	}
	patch, err := teamGit(b.repo, append([]string{"diff", "--binary", work.Merged + "^1", work.Merged, "--"}, excluded...)...)
	if err != nil {
		return teamTask{}, fmt.Errorf("git diff: %s", qFirstLine(patch, err.Error()))
	}
	if strings.TrimSpace(patch) == "" {
		return teamTask{}, fmt.Errorf("%s changed only the team's own files (its specs, rules, and docs): nothing to propose", work.ID)
	}

	// On upstream's latest, in a worktree of its own.
	tree, err := os.MkdirTemp("", "hi-propose-")
	if err != nil {
		return teamTask{}, err
	}
	os.Remove(tree)
	if out, err := teamGit(b.repo, "worktree", "add", "-q", "--detach", tree, follow); err != nil {
		return teamTask{}, fmt.Errorf("git worktree add: %s", out)
	}
	defer func() {
		teamGit(b.repo, "worktree", "remove", "--force", tree)
		os.RemoveAll(tree)
	}()
	apply := teamGitCommand(tree, "apply", "--3way", "--index", "--whitespace=nowarn")
	apply.Stdin = strings.NewReader(patch + "\n")
	if out, err := apply.CombinedOutput(); err != nil {
		return teamTask{}, fmt.Errorf("%s's work doesn't apply to %s's latest %s (%s). Bring upstream in first (hi team task ... --upstream), then propose the work again",
			work.ID, adopted.Name, adopted.Follow, qFirstLine(lastLines(strings.TrimSpace(string(out)), 2), err.Error()))
	}
	name := firstNonEmpty(boxGit(".", "config", "user.name"), login)
	email := firstNonEmpty(boxGit(".", "config", "user.email"), login+"@users.noreply.github.com")
	message := fmt.Sprintf("%s\n\nFrom the %s team's work (%s).", work.Title, config.Name, work.ID)
	if out, err := exec.Command("git", "-C", tree, "-c", "user.name="+name, "-c", "user.email="+email, "commit", "-q", "-m", message).CombinedOutput(); err != nil {
		return teamTask{}, fmt.Errorf("git commit: %s", qFirstLine(strings.TrimSpace(string(out)), err.Error()))
	}
	commit := boxGit(tree, "rev-parse", "HEAD")

	// A fork under the host's account; gh answers with the existing one.
	repoName := filepath.Base(adopted.GitHub)
	if out, err := teamGH("", "repo", "fork", adopted.GitHub, "--clone=false", "--remote=false"); err != nil {
		return teamTask{}, fmt.Errorf("gh repo fork: %s", qFirstLine(out, err.Error()))
	}
	fork := login + "/" + repoName
	branch := fmt.Sprintf("hi-team-%s-%s", config.Name, work.ID)
	// A new fork takes GitHub a few seconds to make.
	for attempt := 1; ; attempt++ {
		push := exec.Command("git", "-C", b.repo, "-c", "credential.helper=", "-c", "credential.https://github.com.helper=!gh auth git-credential",
			"push", "--quiet", "--force", teamForkURL(fork), commit+":refs/heads/"+branch)
		push.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
		out, err := push.CombinedOutput()
		if err == nil {
			break
		}
		if attempt == teamProposePushTries {
			return teamTask{}, fmt.Errorf("pushing to %s: %s", fork, qFirstLine(lastLines(strings.TrimSpace(string(out)), 2), err.Error()))
		}
		time.Sleep(teamProposePushWait)
	}
	body := strings.TrimSpace(request.Notes) + "\n\n---\n\n" + fmt.Sprintf("This change was made by a team of AI agents (a [hi team](https://hi5.sh/guide/team/adopt/)) working on a copy of %s, and checked by its own reviewer agent. "+
		"It's a draft, opened by @%s, who reads it before marking it ready for review.", adopted.Name, login)
	out, err := teamGH("", "pr", "create", "--repo", adopted.GitHub, "--draft", "--head", login+":"+branch, "--base", adopted.Follow, "--title", work.Title, "--body", body)
	if err != nil {
		return teamTask{}, fmt.Errorf("gh pr create: %s", qFirstLine(out, err.Error()))
	}
	lines := strings.Fields(out)
	if len(lines) == 0 {
		return teamTask{}, errors.New("gh pr create gave no link")
	}
	work.Proposed, work.ProposedAt, work.ProposeApproval = lines[len(lines)-1], time.Now().UTC(), &approval
	if err := saveTeamTask(b.team, work); err != nil {
		return work, err
	}
	return work, nil
}

// teamMergedWork is the merged task that holds a piece of work: the task
// named, or the one its line of tasks was merged with.
func teamMergedWork(tasks []teamTask, id string) (teamTask, error) {
	var named *teamTask
	for i := range tasks {
		if tasks[i].ID == id {
			named = &tasks[i]
		}
	}
	switch {
	case named == nil:
		return teamTask{}, fmt.Errorf("no task %s", id)
	case named.Kind == "review":
		return teamTask{}, fmt.Errorf("%s is a review; propose the work it reviewed, %s", id, named.Review)
	case named.Status != "merged":
		return teamTask{}, fmt.Errorf("%s isn't merged (%s): propose work that's in dev, once the people agree it works", id, named.Status)
	case named.Merged != "":
		return *named, nil
	}
	root := teamChainRoot(tasks, id)
	for _, task := range tasks {
		if task.Merged != "" && teamChainRoot(tasks, task.ID) == root {
			return task, nil
		}
	}
	return teamTask{}, fmt.Errorf("hi can't find the merge that brought %s into dev", id)
}
