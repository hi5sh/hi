package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Keeping up with the project a team adopted (app_adopt.go). The host
// fetches upstream once a day, running nothing from it, into
// refs/remotes/upstream/* and refs/tags/upstream/*, which every member's
// worktree can read. hi app upstream says where dev stands against it.
// Bringing it in is work like any other: a task with --upstream, on a yes,
// that starts by merging upstream's branch or a tag, and a review of what
// the merge itself changed.

const (
	// appUpstreamEvery is how often the broker fetches upstream.
	appUpstreamEvery = 24 * time.Hour
	// appUpstreamQuiet is the least time between two wake-ups of the lead
	// for upstream's new versions.
	appUpstreamQuiet = 7 * 24 * time.Hour
)

// appUpstreamMerge is one time the team brought upstream in.
type appUpstreamMerge struct {
	Label  string    `json:"label"`  // what it brought in: v2.4, or main
	From   string    `json:"from"`   // upstream's commit dev had before
	Commit string    `json:"commit"` // upstream's commit it brought in
	Task   string    `json:"task"`
	At     time.Time `json:"at"`
}

// appUpstreamState is run/upstream.json: when hi last fetched, the tags
// it has seen, and when it last woke the lead for a new version.
type appUpstreamState struct {
	Fetched time.Time `json:"fetched"`
	Error   string    `json:"error,omitempty"`
	Seen    []string  `json:"seen"`
	Told    time.Time `json:"told,omitempty"`
	Thread  string    `json:"thread,omitempty"`
}

func appUpstreamStatePath(team string) string {
	return filepath.Join(appDir(team), "run", "upstream.json")
}

func loadTeamUpstreamState(team string) (appUpstreamState, bool) {
	var state appUpstreamState
	data, err := os.ReadFile(appUpstreamStatePath(team))
	if err != nil {
		return state, false
	}
	json.Unmarshal(data, &state)
	return state, true
}

func saveTeamUpstreamState(team string, state appUpstreamState) error {
	if err := os.MkdirAll(filepath.Dir(appUpstreamStatePath(team)), 0o700); err != nil {
		return err
	}
	data, _ := json.MarshalIndent(state, "", "  ")
	return writeFileAtomic(appUpstreamStatePath(team), append(data, '\n'), 0o600)
}

// appUpstreamFetch fetches upstream into the team's repository, running
// nothing from it; tests replace it.
var appUpstreamFetch = func(config appConfig, repo string) error {
	args := append([]string{}, reviewCloneArgs...)
	if config.Adopted.Private {
		// Through gh's sign-in, which keeps the token out of the address.
		args = append(args, "-c", "credential.https://github.com.helper=!gh auth git-credential")
	}
	args = append(args, "-C", repo, "fetch", "--quiet", "--prune", "--no-recurse-submodules", appUpstream)
	command := exec.Command("git", args...)
	command.Env = reviewCloneEnv()
	out, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git fetch %s: %s", appUpstream, qFirstLine(lastLines(strings.TrimSpace(string(out)), 3), err.Error()))
	}
	return nil
}

// fetchUpstream fetches upstream and records when, or why it couldn't.
func fetchTeamUpstream(config appConfig) (appUpstreamState, error) {
	state, _ := loadTeamUpstreamState(config.Name)
	err := appUpstreamFetch(config, filepath.Join(appDir(config.Name), "repo"))
	state.Fetched, state.Error = time.Now().UTC(), ""
	if err != nil {
		state.Error = err.Error()
	}
	saveTeamUpstreamState(config.Name, state)
	return state, err
}

// appUpstreamView is where dev stands against upstream.
type appUpstreamView struct {
	Name    string `json:"name"`
	From    string `json:"from"`
	Follow  string `json:"follow"`
	Adopted string `json:"adopted"` // the commit it was adopted at
	// Base is upstream's latest commit that dev has, and BaseTag the
	// upstream tag that names it, if one does.
	Base    string `json:"base"`
	BaseTag string `json:"base_tag,omitempty"`
	// Ours is how many commits dev has that upstream hasn't, and Behind
	// how many upstream's branch has that dev hasn't.
	Ours    int                `json:"ours"`
	Behind  int                `json:"behind"`
	NewTags []string           `json:"new_tags,omitempty"` // upstream's tags dev hasn't, newest first
	Merges  []appUpstreamMerge `json:"merges,omitempty"`
	Fetched time.Time          `json:"fetched,omitempty"`
	Error   string             `json:"error,omitempty"`
	Shallow bool               `json:"shallow,omitempty"`
}

// appNoUpstream says why a team's project has no upstream to keep up
// with, or nil when it has one.
func appNoUpstream(config appConfig) error {
	switch {
	case config.Adopted == nil:
		return errors.New("this app's project didn't come from elsewhere: it has no upstream")
	case config.Adopted.Local:
		return fmt.Errorf("this app's project is a copy of the folder %s, without its history: it has no upstream to keep up with", config.Adopted.From)
	}
	return nil
}

func appUpstreamReport(config appConfig) (appUpstreamView, error) {
	if err := appNoUpstream(config); err != nil {
		return appUpstreamView{}, err
	}
	adopted := config.Adopted
	repo := filepath.Join(appDir(config.Name), "repo")
	view := appUpstreamView{Name: adopted.Name, From: adopted.From, Follow: adopted.Follow, Adopted: adopted.Commit, Merges: adopted.Merges, Shallow: adopted.Shallow}
	if state, ok := loadTeamUpstreamState(config.Name); ok {
		view.Fetched, view.Error = state.Fetched, state.Error
	}
	follow := "refs/remotes/" + appUpstream + "/" + adopted.Follow
	if boxGit(repo, "rev-parse", "--verify", "-q", follow) == "" {
		return view, fmt.Errorf("upstream has no branch %s any more", adopted.Follow)
	}
	view.Base = firstNonEmpty(boxGit(repo, "merge-base", config.Branch, follow), adopted.Commit)
	// Not git describe: an annotated tag moved under upstream/ still names
	// itself by its old name there.
	if tags := strings.Fields(boxGit(repo, "tag", "--points-at", view.Base, "--list", appUpstream+"/*", "--sort=-v:refname")); len(tags) > 0 {
		view.BaseTag = strings.TrimPrefix(tags[0], appUpstream+"/")
	}
	count := func(args ...string) int {
		n := 0
		fmt.Sscan(boxGit(repo, append([]string{"rev-list", "--count"}, args...)...), &n)
		return n
	}
	view.Ours = count("--no-merges", view.Base+".."+config.Branch)
	view.Behind = count(view.Base + ".." + follow)
	for _, tag := range strings.Fields(boxGit(repo, "tag", "--list", appUpstream+"/*", "--no-merged", config.Branch, "--merged", follow, "--sort=-v:refname")) {
		view.NewTags = append(view.NewTags, strings.TrimPrefix(tag, appUpstream+"/"))
	}
	return view, nil
}

func printTeamUpstream(view appUpstreamView, w io.Writer) {
	at := shortCommit(view.Base)
	if view.BaseTag != "" {
		at = view.BaseTag + " (" + at + ")"
	}
	fmt.Fprintf(w, "%s, from %s, branch %s.\n", view.Name, view.From, view.Follow)
	fmt.Fprintf(w, "Dev: %s's %s + %s of the app's.\n", view.Name, at, appPlural(view.Ours, "commit"))
	switch {
	case view.Behind == 0:
		fmt.Fprintf(w, "Upstream: nothing new on %s.\n", view.Follow)
	case len(view.NewTags) > 0:
		fmt.Fprintf(w, "Upstream: %s on %s since, and %s: %s.\n", appPlural(view.Behind, "commit"), view.Follow, appPlural(len(view.NewTags), "newer tag"), appShortList(view.NewTags, 5))
	default:
		fmt.Fprintf(w, "Upstream: %s on %s since.\n", appPlural(view.Behind, "commit"), view.Follow)
	}
	if view.Behind > 0 {
		fmt.Fprintf(w, "  git log --oneline %s..%s/%s shows them.\n", shortCommit(view.Base), appUpstream, view.Follow)
	}
	for _, merge := range view.Merges {
		fmt.Fprintf(w, "Brought in %s (%s) with %s, %s.\n", merge.Label, shortCommit(merge.Commit), merge.Task, merge.At.Local().Format("Jan 2"))
	}
	switch {
	case view.Error != "":
		fmt.Fprintf(w, "The last fetch failed (%s): %s\n", view.Fetched.Local().Format("Jan 2 15:04"), view.Error)
	case !view.Fetched.IsZero():
		fmt.Fprintf(w, "Fetched %s.\n", view.Fetched.Local().Format("Jan 2 15:04"))
	}
	if view.Shallow {
		fmt.Fprintln(w, "The app's copy has only the history since it was adopted, so counts start there.")
	}
}

// appUpstreamCommand is hi app upstream <name> on the machine: fetch,
// then say where dev stands.
func appUpstreamCommand(args []string, stdout io.Writer) error {
	if len(args) != 1 {
		return usageError{"usage: hi app upstream <name>"}
	}
	config, err := loadTeam(args[0])
	if err != nil {
		return err
	}
	if err := appNoUpstream(config); err != nil {
		return fmt.Errorf("app %s: %w", config.Name, err)
	}
	if _, err := fetchTeamUpstream(config); err != nil {
		fmt.Fprintf(stdout, "hi couldn't fetch %s: %v\nWhat follows is as of the last fetch.\n", config.Adopted.From, err)
	}
	view, err := appUpstreamReport(config)
	if err != nil {
		return err
	}
	printTeamUpstream(view, stdout)
	return nil
}

// upstream fetches and reports, for the lead.
func (b *appBroker) upstream() (appUpstreamView, error) {
	config, err := loadTeam(b.team)
	if err != nil {
		return appUpstreamView{}, err
	}
	if err := appNoUpstream(config); err != nil {
		return appUpstreamView{}, err
	}
	fetchTeamUpstream(config)
	return appUpstreamReport(config)
}

// appReleaseTagPattern is a tag that names a release, such as v2.4 or
// 1.10.0, rather than a nightly build or a test.
var appReleaseTagPattern = regexp.MustCompile(`^v?[0-9]+(\.[0-9]+)+$`)

// keepUpstream fetches upstream once a day and wakes the lead, at most
// once a week, when it has a new release the team hasn't seen.
func (b *appBroker) keepUpstream(done <-chan struct{}, stderr io.Writer) {
	for {
		if err := b.checkUpstream(time.Now()); err != nil {
			fmt.Fprintf(stderr, "hi: keeping up with the adopted project: %v\n", err)
		}
		select {
		case <-done:
			return
		case <-time.After(appUpstreamEvery):
		}
	}
}

func (b *appBroker) checkUpstream(now time.Time) error {
	config, err := loadTeam(b.team)
	if err != nil || appNoUpstream(config) != nil {
		return err
	}
	state, seen := loadTeamUpstreamState(b.team)
	if seen && now.Sub(state.Fetched) < appUpstreamEvery-time.Minute {
		return nil // a restart doesn't fetch again
	}
	if state, err = fetchTeamUpstream(config); err != nil {
		return err
	}
	view, err := appUpstreamReport(config)
	if err != nil {
		return err
	}
	var fresh []string
	for _, tag := range view.NewTags {
		if appReleaseTagPattern.MatchString(tag) && !containsString(state.Seen, tag) {
			fresh = append(fresh, tag)
		}
	}
	// The first time, what's there is what the team started with. A new
	// version within the quiet week waits for the next check after it.
	markSeen := func() {
		for _, tag := range strings.Fields(boxGit(b.repo, "tag", "--list", appUpstream+"/*")) {
			if tag = strings.TrimPrefix(tag, appUpstream+"/"); !containsString(state.Seen, tag) {
				state.Seen = append(state.Seen, tag)
			}
		}
	}
	if !seen || len(fresh) == 0 {
		markSeen()
	} else if now.Sub(state.Told) >= appUpstreamQuiet && b.wakeLead != nil {
		newest := fresh[0]
		deliver := "slack:" + config.Channel
		if b.channel != nil {
			text := fmt.Sprintf(":seedling: *%s, the project this app started from, has a new version: %s.* The lead says what's new here, and whether it matters for this app.",
				appSlackEscape(config.Adopted.Name), appSlackEscape(newest))
			if ts, err := b.channel.post(config.Channel, text); err == nil {
				deliver += ":" + ts
				state.Thread = ts
			}
		}
		if err := b.wakeLead("hi-upstream-"+strings.NewReplacer(".", "-", "/", "-").Replace(newest), appUpstreamPrompt(config, view, newest), deliver); err != nil {
			fmt.Fprintf(os.Stderr, "hi: telling the lead about %s %s: %v\n", config.Adopted.Name, newest, err)
		} else {
			state.Told = now.UTC()
			markSeen()
		}
	}
	return saveTeamUpstreamState(b.team, state)
}

func appUpstreamPrompt(config appConfig, view appUpstreamView, tag string) string {
	at := firstNonEmpty(view.BaseTag, shortCommit(view.Base))
	return fmt.Sprintf(`This is hi, not a person. %[1]s, the project this app started from, has a new version upstream: %[2]s. Dev has its %[3]s, plus the app's own work.

Read what changed: git -C /team/repo log --oneline %[4]s..%[5]s/%[2]s, and its release notes or changelog there (git -C /team/repo show %[5]s/%[2]s:CHANGELOG.md, if it has one). Then tell the people, in a few lines in their words, what's new that they would notice, and whether you think it matters for this app; if it fixes something they use or adds something they asked for, ask whether they want it. Don't start any work: bringing it in is a task like any other, with --upstream=%[2]s once someone says yes. If nothing in it matters to them, say so in one line.

What you write is posted under hi's message about the new version.`,
		config.Adopted.Name, tag, at, view.Base, appUpstream)
}

var appUpstreamRefPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/+-]{0,200}$`)

// appUpstreamWorkOf is the task that brought upstream in, among the
// work a task continues.
func appUpstreamWorkOf(tasks []appTask, task appTask) (appTask, bool) {
	byID := map[string]appTask{}
	for _, other := range tasks {
		byID[other.ID] = other
	}
	for seen := map[string]bool{}; !seen[task.ID]; {
		seen[task.ID] = true
		if task.UpstreamCommit != "" {
			return task, true
		}
		next, ok := byID[task.On]
		if !ok {
			break
		}
		task = next
	}
	return appTask{}, false
}
