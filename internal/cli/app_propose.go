package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Contributing back: hi app propose offers the project a team adopted a
// piece of the team's merged work, as a draft pull request from a fork
// under the host's GitHub account, once an owner says yes. Only the
// work's own changes go: the team's specs, rules, and docs stay out, and
// the change is put on upstream's latest, where its maintainers would
// review it. A draft, so a person reads it before it's marked ready.

// appProposeRequest is what the lead sends.
type appProposeRequest struct {
	Task  string `json:"task"`
	Notes string `json:"notes"`
	// owner is an owner in the dev box, who asked their agent; no other
	// socket sets it.
	owner string
}

// appTeamOnlyPaths are the team's own files in an adopted project: they
// stay out of a proposal unless upstream has them too.
var appTeamOnlyPaths = []string{appSpecsPath, ".hi5", "AGENTS.md", "docs/map.md", "docs/user", "docs/tech", "docs/decisions"}

// appProposePushTries and appProposePushWait give a new fork time to
// be ready for the push.
var (
	appProposePushTries = 4
	appProposePushWait  = 5 * time.Second
)

// appForkURL is where hi pushes the proposal's branch; tests replace it.
var appForkURL = func(fork string) string { return "https://github.com/" + fork + ".git" }

// propose opens a draft pull request upstream with a merged task's work.
func (b *appBroker) propose(request appProposeRequest) (appTask, error) {
	config, err := loadTeam(b.team)
	if err != nil {
		return appTask{}, err
	}
	adopted := config.Adopted
	switch {
	case adopted == nil:
		return appTask{}, errors.New("hi app propose is for a project the app adopted; this one has no upstream")
	case adopted.Local:
		return appTask{}, fmt.Errorf("the project is a copy of the folder %s: there's no upstream to propose to", adopted.From)
	case adopted.GitHub == "":
		return appTask{}, fmt.Errorf("%s isn't on GitHub, so hi can't open a pull request there", adopted.Name)
	case strings.TrimSpace(request.Notes) == "":
		return appTask{}, errors.New("--notes <file>: a description of the change for the project's maintainers")
	case len(request.Notes) > 20000:
		return appTask{}, errors.New("keep the description under 20,000 characters")
	}
	work, err := appMergedWork(loadTeamTasks(b.team), request.Task)
	if err != nil {
		return appTask{}, err
	}
	if work.Proposed != "" {
		return work, fmt.Errorf("%s is proposed already: %s", work.ID, work.Proposed)
	}
	if _, brought := appUpstreamWorkOf(loadTeamTasks(b.team), work); brought {
		return appTask{}, fmt.Errorf("%s brought %s's own changes in; there's nothing of the app's in it to offer back", work.ID, adopted.Name)
	}
	if request.owner == "" {
		return appTask{}, errors.New("an owner proposes work, from the dev box: hi dev propose")
	}
	approval := appApproval{Name: request.owner}
	if _, err := appGHLookPath(); err != nil {
		return appTask{}, errors.New("contributing back needs gh on the machine, signed in: whoever runs hi there runs hi install gh, then gh auth login")
	}
	login, err := appGH("", "api", "user", "--jq", ".login")
	if err != nil || login == "" {
		return appTask{}, fmt.Errorf("gh isn't signed in on the machine: %s", qFirstLine(login, fmt.Sprint(err)))
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	if _, err := fetchTeamUpstream(config); err != nil {
		return appTask{}, fmt.Errorf("couldn't fetch %s's latest: %w", adopted.Name, err)
	}
	follow := "refs/remotes/" + appUpstream + "/" + adopted.Follow
	excluded := []string{"."}
	for _, path := range appTeamOnlyPaths {
		if exec.Command("git", "-C", b.repo, "cat-file", "-e", follow+":"+path).Run() != nil {
			excluded = append(excluded, ":(exclude)"+path)
		}
	}
	patch, err := appGit(b.repo, append([]string{"diff", "--binary", work.Merged + "^1", work.Merged, "--"}, excluded...)...)
	if err != nil {
		return appTask{}, fmt.Errorf("git diff: %s", qFirstLine(patch, err.Error()))
	}
	if strings.TrimSpace(patch) == "" {
		return appTask{}, fmt.Errorf("%s changed only the app's own files (its specs, rules, and docs): nothing to propose", work.ID)
	}

	// On upstream's latest, in a worktree of its own.
	tree, err := os.MkdirTemp("", "hi-propose-")
	if err != nil {
		return appTask{}, err
	}
	os.Remove(tree)
	if out, err := appGit(b.repo, "worktree", "add", "-q", "--detach", tree, follow); err != nil {
		return appTask{}, fmt.Errorf("git worktree add: %s", out)
	}
	defer func() {
		appGit(b.repo, "worktree", "remove", "--force", tree)
		os.RemoveAll(tree)
	}()
	apply := appGitCommand(tree, "apply", "--3way", "--index", "--whitespace=nowarn")
	apply.Stdin = strings.NewReader(patch + "\n")
	if out, err := apply.CombinedOutput(); err != nil {
		return appTask{}, fmt.Errorf("%s's work doesn't apply to %s's latest %s (%s). Bring upstream in first (hi dev upstream, in the dev box), then propose the work again",
			work.ID, adopted.Name, adopted.Follow, qFirstLine(lastLines(strings.TrimSpace(string(out)), 2), err.Error()))
	}
	name := firstNonEmpty(boxGit(".", "config", "user.name"), login)
	email := firstNonEmpty(boxGit(".", "config", "user.email"), login+"@users.noreply.github.com")
	message := fmt.Sprintf("%s\n\nFrom the %s app's work (%s).", work.Title, config.Name, work.ID)
	if out, err := exec.Command("git", "-C", tree, "-c", "user.name="+name, "-c", "user.email="+email, "commit", "-q", "-m", message).CombinedOutput(); err != nil {
		return appTask{}, fmt.Errorf("git commit: %s", qFirstLine(strings.TrimSpace(string(out)), err.Error()))
	}
	commit := boxGit(tree, "rev-parse", "HEAD")

	// A fork under the host's account; gh answers with the existing one.
	repoName := filepath.Base(adopted.GitHub)
	if out, err := appGH("", "repo", "fork", adopted.GitHub, "--clone=false", "--remote=false"); err != nil {
		return appTask{}, fmt.Errorf("gh repo fork: %s", qFirstLine(out, err.Error()))
	}
	fork := login + "/" + repoName
	branch := fmt.Sprintf("hi-team-%s-%s", config.Name, work.ID)
	// A new fork takes GitHub a few seconds to make.
	for attempt := 1; ; attempt++ {
		push := exec.Command("git", "-C", b.repo, "-c", "credential.helper=", "-c", "credential.https://github.com.helper=!gh auth git-credential",
			"push", "--quiet", "--force", appForkURL(fork), commit+":refs/heads/"+branch)
		push.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
		out, err := push.CombinedOutput()
		if err == nil {
			break
		}
		if attempt == appProposePushTries {
			return appTask{}, fmt.Errorf("pushing to %s: %s", fork, qFirstLine(lastLines(strings.TrimSpace(string(out)), 2), err.Error()))
		}
		time.Sleep(appProposePushWait)
	}
	body := strings.TrimSpace(request.Notes) + "\n\n---\n\n" + fmt.Sprintf("This change was made by a team of AI agents (a [hi app](https://hi5.sh/guide/app/adopt/)) working on a copy of %s, and checked by its own reviewer agent. "+
		"It's a draft, opened by @%s, who reads it before marking it ready for review.", adopted.Name, login)
	out, err := appGH("", "pr", "create", "--repo", adopted.GitHub, "--draft", "--head", login+":"+branch, "--base", adopted.Follow, "--title", work.Title, "--body", body)
	if err != nil {
		return appTask{}, fmt.Errorf("gh pr create: %s", qFirstLine(out, err.Error()))
	}
	lines := strings.Fields(out)
	if len(lines) == 0 {
		return appTask{}, errors.New("gh pr create gave no link")
	}
	work.Proposed, work.ProposedAt, work.ProposeApproval = lines[len(lines)-1], time.Now().UTC(), &approval
	if err := saveTeamTask(b.team, work); err != nil {
		return work, err
	}
	return work, nil
}

// appMergedWork is the merged task that holds a piece of work: the task
// named, or the one its line of tasks was merged with.
func appMergedWork(tasks []appTask, id string) (appTask, error) {
	var named *appTask
	for i := range tasks {
		if tasks[i].ID == id {
			named = &tasks[i]
		}
	}
	switch {
	case named == nil:
		return appTask{}, fmt.Errorf("no task %s", id)
	case named.Kind == "review":
		return appTask{}, fmt.Errorf("%s is a review; propose the work it reviewed, %s", id, named.Review)
	case named.Status != "merged":
		return appTask{}, fmt.Errorf("%s isn't merged (%s): propose work that's in dev, once the people agree it works", id, named.Status)
	case named.Merged != "":
		return *named, nil
	}
	root := appChainRoot(tasks, id)
	for _, task := range tasks {
		if task.Merged != "" && appChainRoot(tasks, task.ID) == root {
			return task, nil
		}
	}
	return appTask{}, fmt.Errorf("hi can't find the merge that brought %s into dev", id)
}
