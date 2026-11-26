package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Branches people push to the app's own GitHub or GitLab project, from
// their own machines (hi_app_pushed_branches.md). The broker pushes dev
// and production there; it also fetches every 2 minutes, running nothing
// from the project, and keeps each other branch in the team's repository
// as pushed/<branch>, where the dev page and the dev box see it. Trying
// one on the preview and adding it to dev are as for a branch of the dev
// box, with the same checks. Dev's own branch pushed there with commits
// dev hasn't is one more branch, pushed/<dev>; a pushed production is
// never taken: the ops channel hears once, and the next release
// force-pushes it.

const (
	// appPushedEvery is how often the broker looks at the remote.
	appPushedEvery  = 2 * time.Minute
	appPushedPrefix = "pushed/"
	// appPushedFilesMax is how many of a branch's files the page lists.
	appPushedFilesMax = 40
)

// appPushedRef is where the team's repository keeps a branch pushed to
// its remote.
func appPushedRef(branch string) string { return appPushedPrefix + branch }

// appPushedState is run/pushed.json: the remote's branches as last
// fetched, and the pushed production hi has told the ops channel about.
type appPushedState struct {
	Fetched time.Time         `json:"fetched"`
	Error   string            `json:"error,omitempty"`
	Heads   map[string]string `json:"heads,omitempty"`
	// Told is the remote's production commit hi posted about.
	Told string `json:"told,omitempty"`
}

func appPushedStatePath(team string) string {
	return filepath.Join(appDir(team), "run", "pushed.json")
}

func loadTeamPushedState(team string) appPushedState {
	var state appPushedState
	if data, err := os.ReadFile(appPushedStatePath(team)); err == nil {
		json.Unmarshal(data, &state)
	}
	return state
}

func saveTeamPushedState(team string, state appPushedState) error {
	if err := os.MkdirAll(filepath.Dir(appPushedStatePath(team)), 0o700); err != nil {
		return err
	}
	data, _ := json.MarshalIndent(state, "", "  ")
	return writeFileAtomic(appPushedStatePath(team), append(data, '\n'), 0o600)
}

// appForgeHost is the host of a git address: gitlab.com for
// https://gitlab.com/acme/billing.git, "" for one it can't read.
func appForgeHost(remote string) string {
	if !strings.Contains(remote, "://") {
		if strings.HasPrefix(remote, "/") || strings.HasPrefix(remote, ".") || strings.Contains(remote, "@") {
			return "" // a folder, or ssh: no https sign-in
		}
		remote = "https://" + remote
	}
	if u, err := url.Parse(remote); err == nil && u.Host != "" && (u.Scheme == "https" || u.Scheme == "http") {
		return strings.ToLower(u.Hostname())
	}
	return ""
}

// appForgeName is the forge, in the page's words: GitHub, GitLab, or the
// host.
func appForgeName(remote string) string {
	host := appForgeHost(remote)
	switch {
	case host == "github.com" || strings.Contains(host, "github"):
		return "GitHub"
	case host == "gitlab.com" || strings.Contains(host, "gitlab"):
		return "GitLab"
	case host != "":
		return host
	}
	return "the remote"
}

// appForgeGitArgs are git's options to read a remote without running
// anything from it, through the machine's sign-in for its host: gh's for
// GitHub, glab's for any other (gitlab.com, or a GitLab of one's own).
// The repository's own helpers are cleared first, as for a review.
func appForgeGitArgs(remote string) []string {
	args := append([]string{}, reviewCloneArgs...)
	host := appForgeHost(remote)
	if host == "" {
		return args
	}
	helper := "!glab auth git-credential"
	if appForgeName(remote) == "GitHub" {
		helper = "!gh auth git-credential"
	}
	return append(args, "-c", "credential.https://"+host+".helper="+helper)
}

// appForgeGit runs git on the team's repository against its remote;
// tests replace it.
var appForgeGit = func(repo, remote string, args ...string) (string, error) {
	command := exec.Command("git", append(append(appForgeGitArgs(remote), "-C", repo), args...)...)
	command.Env = reviewCloneEnv()
	out, err := command.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// appPushedLocks keeps two fetches of one app's remote apart: the
// broker's and hi app branches --fetch.
var appPushedLocks sync.Map

// fetchTeamPushed looks at the remote's branches and, when they changed
// since the last time, fetches them and keeps pushed/<branch> in step.
// It returns the state saved, and what the ops channel should hear, if
// anything: a production pushed by someone else.
func fetchTeamPushed(config appConfig, force bool) (appPushedState, string, error) {
	lock, _ := appPushedLocks.LoadOrStore(config.Name, &sync.Mutex{})
	lock.(*sync.Mutex).Lock()
	defer lock.(*sync.Mutex).Unlock()
	state := loadTeamPushedState(config.Name)
	if config.Remote == "" {
		return state, "", errors.New("this app's project has no remote: nobody can push to it")
	}
	repo := filepath.Join(appDir(config.Name), "repo")
	state.Fetched, state.Error = time.Now().UTC(), ""
	out, err := appForgeGit(repo, config.Remote, "ls-remote", "--heads", "origin")
	if err != nil {
		state.Error = "git ls-remote: " + qFirstLine(lastLines(out, 3), err.Error())
		saveTeamPushedState(config.Name, state)
		return state, "", errors.New(state.Error)
	}
	heads := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		commit, ref, ok := strings.Cut(strings.TrimSpace(line), "\t")
		if branch, isHead := strings.CutPrefix(ref, "refs/heads/"); ok && isHead && branch != "" {
			heads[branch] = commit
		}
	}
	if force || !appSameHeads(heads, state.Heads) || !appPushedInStep(repo, config, heads) {
		if out, err := appForgeGit(repo, config.Remote, "fetch", "--quiet", "--prune", "--no-tags", "--no-recurse-submodules",
			"origin", "+refs/heads/*:refs/remotes/origin/*"); err != nil {
			state.Error = "git fetch: " + qFirstLine(lastLines(out, 3), err.Error())
			saveTeamPushedState(config.Name, state)
			return state, "", errors.New(state.Error)
		}
		state.Heads = heads
	}
	appKeepPushedRefs(config, repo, heads)
	// production is the broker's: a commit there that isn't one of its
	// releases is told once, and not taken.
	tell := ""
	if theirs, ok := heads[appProductionBranch]; ok && theirs != state.Told {
		ours := boxGit(repo, "rev-parse", "--verify", "-q", "refs/heads/"+appProductionBranch)
		if ours != "" && theirs != ours && !appIsAncestor(repo, theirs, ours) {
			tell = fmt.Sprintf(":warning: Someone pushed to *%s* on %s (%s). hi doesn't take it: production moves only when someone publishes on the dev page, and the next release overwrites it. Push a branch instead; it shows on the dev page. Protect %s on %s, so only hi's account can push there.",
				appProductionBranch, appForgeName(config.Remote), shortCommit(theirs), appProductionBranch, appForgeName(config.Remote))
			state.Told = theirs
		}
	}
	return state, tell, saveTeamPushedState(config.Name, state)
}

func appSameHeads(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// appPushedInStep says the team's repository has every remote branch it
// should, at the commit the remote has.
func appPushedInStep(repo string, config appConfig, heads map[string]string) bool {
	for branch, commit := range heads {
		if boxGit(repo, "rev-parse", "--verify", "-q", "refs/remotes/origin/"+branch) != commit {
			return false
		}
	}
	return true
}

// appKeepPushedRefs makes pushed/<branch> match the remote's branches:
// every branch but production, and dev's own when it has commits dev
// hasn't. One the remote dropped goes, unless it is on the preview.
func appKeepPushedRefs(config appConfig, repo string, heads map[string]string) {
	want := map[string]string{}
	for branch, commit := range heads {
		switch {
		case branch == appProductionBranch:
		case branch == config.Branch:
			if !appIsAncestor(repo, commit, "refs/heads/"+config.Branch) {
				want[branch] = commit
			}
		case strings.HasPrefix(branch, appPushedPrefix), strings.HasPrefix(branch, appDevBranchRef("")):
			// hi's own names: not a branch someone works on.
		default:
			want[branch] = commit
		}
	}
	previewing := ""
	if slot, ok := loadTeamSlot(config.Name, appSlotPreview); ok {
		previewing = slot.Source
	}
	for _, ref := range strings.Fields(boxGit(repo, "for-each-ref", "--format=%(refname)", "refs/heads/"+appPushedPrefix)) {
		branch := strings.TrimPrefix(ref, "refs/heads/"+appPushedPrefix)
		if _, ok := want[branch]; !ok && previewing != appPushedRef(branch) {
			appGit(repo, "update-ref", "-d", ref)
		}
	}
	for branch, commit := range want {
		if appDevBranchPattern.MatchString(branch) && !strings.Contains(branch, "..") {
			appGit(repo, "update-ref", "refs/heads/"+appPushedRef(branch), commit)
		}
	}
}

// appPushedBranch is a branch pushed to the remote, as the page and hi
// app branches show it.
type appPushedBranch struct {
	Name    string    `json:"name"`
	Commit  string    `json:"commit"`
	Author  string    `json:"author"` // its last commit's
	Email   string    `json:"email,omitempty"`
	At      time.Time `json:"at"`
	Subject string    `json:"subject"`
	Ahead   int       `json:"ahead"`            // commits dev hasn't
	Behind  int       `json:"behind,omitempty"` // dev's commits it hasn't
	Files   []string  `json:"files,omitempty"`
	More    int       `json:"more,omitempty"` // files past those listed
	// Dev says it is dev's own branch, pushed with commits dev hasn't.
	Dev bool `json:"dev,omitempty"`
	// Shipped says its last commit is in dev, through hi.
	Shipped bool `json:"shipped,omitempty"`
	// Out says its change was taken out of dev since.
	Out bool `json:"out,omitempty"`
	// Workspace is the branch of the workspace Change it makes: its own
	// name, or for dev's own branch, which no workspace can have, the
	// name and the commit (main-2b3f2cd).
	Workspace string `json:"workspace"`
	// Inspect is the branch of the workspace Inspect makes: a checkout of
	// this push for an agent to read and explain, never to add from
	// (inspect/local-dev-2b3f2cd).
	Inspect string `json:"inspect"`
}

// appDevInspectPrefix starts the branches of Inspect's workspaces, which
// hi doesn't add to dev: the pushed branch is added, as its author's.
const appDevInspectPrefix = "inspect/"

// appPushedView is what the page and hi app branches show.
type appPushedView struct {
	Forge    string            `json:"forge"`
	Remote   string            `json:"remote,omitempty"`
	Fetched  time.Time         `json:"fetched,omitzero"`
	Error    string            `json:"error,omitempty"`
	Branches []appPushedBranch `json:"branches,omitempty"`
}

// appPushedBranches lists pushed/<branch>, newest first.
func appPushedBranches(config appConfig) appPushedView {
	view := appPushedView{Forge: appForgeName(config.Remote), Remote: config.Remote}
	if config.Remote == "" {
		return view
	}
	state := loadTeamPushedState(config.Name)
	view.Fetched, view.Error = state.Fetched, state.Error
	repo := filepath.Join(appDir(config.Name), "repo")
	tasks := loadTeamTasks(config.Name)
	dev := "refs/heads/" + config.Branch
	out := boxGit(repo, "for-each-ref", "--sort=-committerdate",
		"--format=%(refname)%00%(objectname)%00%(authorname)%00%(authoremail)%00%(committerdate:iso-strict)%00%(contents:subject)",
		"refs/heads/"+appPushedPrefix)
	for _, line := range strings.Split(out, "\n") {
		parts := strings.Split(line, "\x00")
		if len(parts) != 6 {
			continue
		}
		branch := appPushedBranch{Name: strings.TrimPrefix(parts[0], "refs/heads/"+appPushedPrefix), Commit: parts[1],
			Author: parts[2], Email: strings.Trim(parts[3], "<>"), Subject: parts[5], Dev: strings.TrimPrefix(parts[0], "refs/heads/"+appPushedPrefix) == config.Branch}
		branch.At, _ = time.Parse(time.RFC3339, parts[4])
		branch.Workspace = branch.Name
		short := branch.Commit[:min(7, len(branch.Commit))]
		branch.Inspect = appDevInspectPrefix + branch.Name + "-" + short
		if branch.Dev {
			branch.Workspace = branch.Name + "-" + short
		}
		count := func(spec string) int {
			n := 0
			fmt.Sscan(boxGit(repo, "rev-list", "--count", "--no-merges", spec), &n)
			return n
		}
		branch.Ahead = count(dev + ".." + branch.Commit)
		branch.Behind = count(branch.Commit + ".." + dev)
		if branch.Dev && branch.Ahead == 0 && !appIsAncestor(repo, branch.Commit, dev) {
			// A merge request merged on the forge after its branch was
			// added: only a merge commit dev hasn't, which still stops hi's
			// push of dev until it's added.
			fmt.Sscan(boxGit(repo, "rev-list", "--count", dev+".."+branch.Commit), &branch.Ahead)
		}
		if base := boxGit(repo, "merge-base", dev, branch.Commit); base != "" && branch.Ahead > 0 {
			files := strings.Fields(boxGit(repo, "diff", "--name-only", base, branch.Commit))
			if len(files) > appPushedFilesMax {
				branch.More, files = len(files)-appPushedFilesMax, files[:appPushedFilesMax]
			}
			branch.Files = files
		}
		branch.Shipped = appBranchShipped(tasks, appPushedRef(branch.Name))
		branch.Out = appBranchTakenOut(tasks, appPushedRef(branch.Name)) != nil
		view.Branches = append(view.Branches, branch)
	}
	return view
}

// appPushedFind is one pushed branch, by its name.
func appPushedFind(config appConfig, name string) (appPushedBranch, error) {
	name = strings.TrimPrefix(name, appPushedPrefix)
	for _, branch := range appPushedBranches(config).Branches {
		if branch.Name == name {
			return branch, nil
		}
	}
	return appPushedBranch{}, fmt.Errorf("no branch %s pushed to %s (as of the last fetch: hi app branches %s --fetch fetches again)", name, appForgeName(config.Remote), config.Name)
}

// previewPushedBranch puts a pushed branch on the preview, with a fresh
// fork of production's data when it is new work there.
func (b *appBroker) previewPushedBranch(name string) (appPreview, error) {
	config, err := loadTeam(b.team)
	if err != nil {
		return appPreview{}, err
	}
	branch, err := appPushedFind(config, name)
	if err != nil {
		return appPreview{}, err
	}
	source := appPushedRef(branch.Name)
	data := ""
	if config.PreviewData != appPreviewEmpty && appOverlayEngine() {
		if current, ok := loadTeamSlot(b.team, appSlotPreview); !ok || current.Source != source {
			data = b.forkForNewWork()
		}
	}
	b.previewMu.Lock()
	defer b.previewMu.Unlock()
	next := appPreview{Slot: appSlotPreview, Source: source, Label: fmt.Sprintf("%s, pushed by %s, not in dev yet", branch.Name, firstNonEmpty(branch.Author, "someone"))}
	state, err := b.show(next, "refs/heads/"+source, appLinkKeep)
	state.Data = data
	return state, err
}

// shipPushedBranch merges a pushed branch into dev, as shipDevBranch does
// a branch of the dev box, and puts dev on the preview.
func (b *appBroker) shipPushedBranch(request appDevShipRequest) (appDevShipResult, error) {
	config, err := loadTeam(b.team)
	if err != nil {
		return appDevShipResult{}, err
	}
	branch, err := appPushedFind(config, request.Branch)
	if err != nil {
		return appDevShipResult{}, err
	}
	request.Branch, request.Pushed = branch.Name, true
	request.Author = firstNonEmpty(branch.Author, branch.Email, "someone")
	task, err := b.mergeDevBranch(request, branch.Commit)
	if err != nil {
		return appDevShipResult{}, err
	}
	result := appDevShipResult{Task: task}
	if result.Preview, err = b.preview(appPreviewRequest{}); err != nil {
		result.Warning = "Added, but dev isn't on the preview: " + err.Error()
	}
	return result, nil
}

// keepPushed looks at the remote every 2 minutes.
func (b *appBroker) keepPushed(done <-chan struct{}, stderr io.Writer) {
	failing := ""
	for {
		if config, err := loadTeam(b.team); err == nil && config.Remote != "" {
			_, tell, err := fetchTeamPushed(config, false)
			// A failure is said once, not every 2 minutes.
			if err != nil && err.Error() != failing {
				fmt.Fprintf(stderr, "hi: the branches pushed to %s: %v\n", appForgeName(config.Remote), err)
			}
			failing = ""
			if err != nil {
				failing = err.Error()
			}
			if tell != "" && b.channel != nil {
				if _, err := b.channel.post(config.opsChannel(), tell); err != nil {
					fmt.Fprintf(stderr, "hi: telling the ops channel about a pushed %s: %v\n", appProductionBranch, err)
				}
			}
		}
		select {
		case <-done:
			return
		case <-time.After(appPushedEvery):
		}
	}
}

// printPushedBranches is hi app branches and hi dev branches.
func printPushedBranches(view appPushedView, w io.Writer) {
	switch {
	case view.Remote == "":
		fmt.Fprintln(w, "This app's project has no remote on GitHub or GitLab: there is nothing to push branches to.")
		return
	case len(view.Branches) == 0:
		fmt.Fprintf(w, "No branches pushed to %s, beside dev and production.\n", view.Forge)
	}
	sort.SliceStable(view.Branches, func(i, j int) bool { return view.Branches[i].At.After(view.Branches[j].At) })
	for _, branch := range view.Branches {
		state := fmt.Sprintf("%s dev hasn't", appPlural(branch.Ahead, "commit"))
		switch {
		case branch.Out && branch.Ahead == 0:
			state = "taken out of dev"
		case branch.Ahead == 0:
			state = "in dev"
		}
		name := branch.Name
		if branch.Dev {
			name += " (pushed straight to dev's branch)"
		}
		fmt.Fprintf(w, "%s  %s, pushed by %s, %s: %s\n", name, shortCommit(branch.Commit), firstNonEmpty(branch.Author, "?"), branch.At.Local().Format("Jan 2 15:04"), state)
		if branch.Subject != "" {
			fmt.Fprintf(w, "  %s\n", branch.Subject)
		}
		if len(branch.Files) > 0 {
			files := strings.Join(branch.Files, ", ")
			if branch.More > 0 {
				files += fmt.Sprintf(" and %d more", branch.More)
			}
			fmt.Fprintf(w, "  Files: %s\n", files)
		}
	}
	switch {
	case view.Error != "":
		fmt.Fprintf(w, "The last fetch failed (%s): %s\n", view.Fetched.Local().Format("Jan 2 15:04"), view.Error)
	case !view.Fetched.IsZero():
		fmt.Fprintf(w, "Fetched %s.\n", view.Fetched.Local().Format("Jan 2 15:04"))
	}
}

// appBranchesCommand is hi app branches <name> [--fetch] on the machine.
func appBranchesCommand(args []string, stdout io.Writer) error {
	fetch := false
	var names []string
	for _, arg := range args {
		switch arg {
		case "--fetch":
			fetch = true
		default:
			if strings.HasPrefix(arg, "-") {
				return usageError{"usage: hi app branches <name> [--fetch]"}
			}
			names = append(names, arg)
		}
	}
	if len(names) != 1 {
		return usageError{"usage: hi app branches <name> [--fetch]"}
	}
	config, err := loadTeam(names[0])
	if err != nil {
		return err
	}
	if fetch && config.Remote != "" {
		if _, _, err := fetchTeamPushed(config, true); err != nil {
			fmt.Fprintf(stdout, "hi couldn't fetch from %s: %v\nWhat follows is as of the last fetch.\n", appForgeName(config.Remote), err)
		}
	}
	printPushedBranches(appPushedBranches(config), stdout)
	return nil
}
