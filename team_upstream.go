package main

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

// Keeping up with the project a team adopted (team_adopt.go). The host
// fetches upstream once a day, running nothing from it, into
// refs/remotes/upstream/* and refs/tags/upstream/*, which every member's
// worktree can read. hi team upstream says where dev stands against it.
// Bringing it in is work like any other: a task with --upstream, on a yes,
// that starts by merging upstream's branch or a tag, and a review of what
// the merge itself changed.

const (
	// teamUpstreamEvery is how often the broker fetches upstream.
	teamUpstreamEvery = 24 * time.Hour
	// teamUpstreamQuiet is the least time between two wake-ups of the lead
	// for upstream's new versions.
	teamUpstreamQuiet = 7 * 24 * time.Hour
)

// teamUpstreamMerge is one time the team brought upstream in.
type teamUpstreamMerge struct {
	Label  string    `json:"label"`  // what it brought in: v2.4, or main
	From   string    `json:"from"`   // upstream's commit dev had before
	Commit string    `json:"commit"` // upstream's commit it brought in
	Task   string    `json:"task"`
	At     time.Time `json:"at"`
}

// teamUpstreamState is run/upstream.json: when hi last fetched, the tags
// it has seen, and when it last woke the lead for a new version.
type teamUpstreamState struct {
	Fetched time.Time `json:"fetched"`
	Error   string    `json:"error,omitempty"`
	Seen    []string  `json:"seen"`
	Told    time.Time `json:"told,omitempty"`
	Thread  string    `json:"thread,omitempty"`
}

func teamUpstreamStatePath(team string) string {
	return filepath.Join(teamDir(team), "run", "upstream.json")
}

func loadTeamUpstreamState(team string) (teamUpstreamState, bool) {
	var state teamUpstreamState
	data, err := os.ReadFile(teamUpstreamStatePath(team))
	if err != nil {
		return state, false
	}
	json.Unmarshal(data, &state)
	return state, true
}

func saveTeamUpstreamState(team string, state teamUpstreamState) error {
	if err := os.MkdirAll(filepath.Dir(teamUpstreamStatePath(team)), 0o700); err != nil {
		return err
	}
	data, _ := json.MarshalIndent(state, "", "  ")
	return writeFileAtomic(teamUpstreamStatePath(team), append(data, '\n'), 0o600)
}

// teamUpstreamFetch fetches upstream into the team's repository, running
// nothing from it; tests replace it.
var teamUpstreamFetch = func(config teamConfig, repo string) error {
	args := append([]string{}, reviewCloneArgs...)
	if config.Adopted.Private {
		// Through gh's sign-in, which keeps the token out of the address.
		args = append(args, "-c", "credential.https://github.com.helper=!gh auth git-credential")
	}
	args = append(args, "-C", repo, "fetch", "--quiet", "--prune", "--no-recurse-submodules", teamUpstream)
	command := exec.Command("git", args...)
	command.Env = reviewCloneEnv()
	out, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git fetch %s: %s", teamUpstream, qFirstLine(lastLines(strings.TrimSpace(string(out)), 3), err.Error()))
	}
	return nil
}

// fetchUpstream fetches upstream and records when, or why it couldn't.
func fetchTeamUpstream(config teamConfig) (teamUpstreamState, error) {
	state, _ := loadTeamUpstreamState(config.Name)
	err := teamUpstreamFetch(config, filepath.Join(teamDir(config.Name), "repo"))
	state.Fetched, state.Error = time.Now().UTC(), ""
	if err != nil {
		state.Error = err.Error()
	}
	saveTeamUpstreamState(config.Name, state)
	return state, err
}

// teamUpstreamView is where dev stands against upstream.
type teamUpstreamView struct {
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
	Ours    int                 `json:"ours"`
	Behind  int                 `json:"behind"`
	NewTags []string            `json:"new_tags,omitempty"` // upstream's tags dev hasn't, newest first
	Merges  []teamUpstreamMerge `json:"merges,omitempty"`
	Fetched time.Time           `json:"fetched,omitempty"`
	Error   string              `json:"error,omitempty"`
	Shallow bool                `json:"shallow,omitempty"`
}

// teamNoUpstream says why a team's project has no upstream to keep up
// with, or nil when it has one.
func teamNoUpstream(config teamConfig) error {
	switch {
	case config.Adopted == nil:
		return errors.New("this team's project didn't come from elsewhere: it has no upstream")
	case config.Adopted.Local:
		return fmt.Errorf("this team's project is a copy of the folder %s, without its history: it has no upstream to keep up with", config.Adopted.From)
	}
	return nil
}

func teamUpstreamReport(config teamConfig) (teamUpstreamView, error) {
	if err := teamNoUpstream(config); err != nil {
		return teamUpstreamView{}, err
	}
	adopted := config.Adopted
	repo := filepath.Join(teamDir(config.Name), "repo")
	view := teamUpstreamView{Name: adopted.Name, From: adopted.From, Follow: adopted.Follow, Adopted: adopted.Commit, Merges: adopted.Merges, Shallow: adopted.Shallow}
	if state, ok := loadTeamUpstreamState(config.Name); ok {
		view.Fetched, view.Error = state.Fetched, state.Error
	}
	follow := "refs/remotes/" + teamUpstream + "/" + adopted.Follow
	if boxGit(repo, "rev-parse", "--verify", "-q", follow) == "" {
		return view, fmt.Errorf("upstream has no branch %s any more", adopted.Follow)
	}
	view.Base = firstNonEmpty(boxGit(repo, "merge-base", config.Branch, follow), adopted.Commit)
	// Not git describe: an annotated tag moved under upstream/ still names
	// itself by its old name there.
	if tags := strings.Fields(boxGit(repo, "tag", "--points-at", view.Base, "--list", teamUpstream+"/*", "--sort=-v:refname")); len(tags) > 0 {
		view.BaseTag = strings.TrimPrefix(tags[0], teamUpstream+"/")
	}
	count := func(args ...string) int {
		n := 0
		fmt.Sscan(boxGit(repo, append([]string{"rev-list", "--count"}, args...)...), &n)
		return n
	}
	view.Ours = count("--no-merges", view.Base+".."+config.Branch)
	view.Behind = count(view.Base + ".." + follow)
	for _, tag := range strings.Fields(boxGit(repo, "tag", "--list", teamUpstream+"/*", "--no-merged", config.Branch, "--merged", follow, "--sort=-v:refname")) {
		view.NewTags = append(view.NewTags, strings.TrimPrefix(tag, teamUpstream+"/"))
	}
	return view, nil
}

func printTeamUpstream(view teamUpstreamView, w io.Writer) {
	at := shortCommit(view.Base)
	if view.BaseTag != "" {
		at = view.BaseTag + " (" + at + ")"
	}
	fmt.Fprintf(w, "%s, from %s, branch %s.\n", view.Name, view.From, view.Follow)
	fmt.Fprintf(w, "Dev: %s's %s + %s of the team's.\n", view.Name, at, teamPlural(view.Ours, "commit"))
	switch {
	case view.Behind == 0:
		fmt.Fprintf(w, "Upstream: nothing new on %s.\n", view.Follow)
	case len(view.NewTags) > 0:
		fmt.Fprintf(w, "Upstream: %s on %s since, and %s: %s.\n", teamPlural(view.Behind, "commit"), view.Follow, teamPlural(len(view.NewTags), "newer tag"), teamShortList(view.NewTags, 5))
	default:
		fmt.Fprintf(w, "Upstream: %s on %s since.\n", teamPlural(view.Behind, "commit"), view.Follow)
	}
	if view.Behind > 0 {
		fmt.Fprintf(w, "  git log --oneline %s..%s/%s shows them.\n", shortCommit(view.Base), teamUpstream, view.Follow)
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
		fmt.Fprintln(w, "The team's copy has only the history since it was adopted, so counts start there.")
	}
}

// teamUpstreamCommand is hi team upstream <name> on the machine: fetch,
// then say where dev stands.
func teamUpstreamCommand(args []string, stdout io.Writer) error {
	if len(args) != 1 {
		return usageError{"usage: hi team upstream <name>"}
	}
	config, err := loadTeam(args[0])
	if err != nil {
		return err
	}
	if err := teamNoUpstream(config); err != nil {
		return fmt.Errorf("team %s: %w", config.Name, err)
	}
	if _, err := fetchTeamUpstream(config); err != nil {
		fmt.Fprintf(stdout, "hi couldn't fetch %s: %v\nWhat follows is as of the last fetch.\n", config.Adopted.From, err)
	}
	view, err := teamUpstreamReport(config)
	if err != nil {
		return err
	}
	printTeamUpstream(view, stdout)
	return nil
}

// upstream fetches and reports, for the lead.
func (b *teamBroker) upstream() (teamUpstreamView, error) {
	config, err := loadTeam(b.team)
	if err != nil {
		return teamUpstreamView{}, err
	}
	if err := teamNoUpstream(config); err != nil {
		return teamUpstreamView{}, err
	}
	fetchTeamUpstream(config)
	return teamUpstreamReport(config)
}

// teamReleaseTagPattern is a tag that names a release, such as v2.4 or
// 1.10.0, rather than a nightly build or a test.
var teamReleaseTagPattern = regexp.MustCompile(`^v?[0-9]+(\.[0-9]+)+$`)

// keepUpstream fetches upstream once a day and wakes the lead, at most
// once a week, when it has a new release the team hasn't seen.
func (b *teamBroker) keepUpstream(done <-chan struct{}, stderr io.Writer) {
	for {
		if err := b.checkUpstream(time.Now()); err != nil {
			fmt.Fprintf(stderr, "hi: keeping up with the adopted project: %v\n", err)
		}
		select {
		case <-done:
			return
		case <-time.After(teamUpstreamEvery):
		}
	}
}

func (b *teamBroker) checkUpstream(now time.Time) error {
	config, err := loadTeam(b.team)
	if err != nil || teamNoUpstream(config) != nil {
		return err
	}
	state, seen := loadTeamUpstreamState(b.team)
	if seen && now.Sub(state.Fetched) < teamUpstreamEvery-time.Minute {
		return nil // a restart doesn't fetch again
	}
	if state, err = fetchTeamUpstream(config); err != nil {
		return err
	}
	view, err := teamUpstreamReport(config)
	if err != nil {
		return err
	}
	var fresh []string
	for _, tag := range view.NewTags {
		if teamReleaseTagPattern.MatchString(tag) && !containsString(state.Seen, tag) {
			fresh = append(fresh, tag)
		}
	}
	// The first time, what's there is what the team started with. A new
	// version within the quiet week waits for the next check after it.
	markSeen := func() {
		for _, tag := range strings.Fields(boxGit(b.repo, "tag", "--list", teamUpstream+"/*")) {
			if tag = strings.TrimPrefix(tag, teamUpstream+"/"); !containsString(state.Seen, tag) {
				state.Seen = append(state.Seen, tag)
			}
		}
	}
	if !seen || len(fresh) == 0 {
		markSeen()
	} else if now.Sub(state.Told) >= teamUpstreamQuiet && b.wakeLead != nil {
		newest := fresh[0]
		deliver := "slack:" + config.Channel
		if b.channel != nil {
			text := fmt.Sprintf(":seedling: *%s, the project this team started from, has a new version: %s.* The lead says what's new here, and whether it matters for this app.",
				teamSlackEscape(config.Adopted.Name), teamSlackEscape(newest))
			if ts, err := b.channel.post(config.Channel, text); err == nil {
				deliver += ":" + ts
				state.Thread = ts
			}
		}
		if err := b.wakeLead("hi-upstream-"+strings.NewReplacer(".", "-", "/", "-").Replace(newest), teamUpstreamPrompt(config, view, newest), deliver); err != nil {
			fmt.Fprintf(os.Stderr, "hi: telling the lead about %s %s: %v\n", config.Adopted.Name, newest, err)
		} else {
			state.Told = now.UTC()
			markSeen()
		}
	}
	return saveTeamUpstreamState(b.team, state)
}

func teamUpstreamPrompt(config teamConfig, view teamUpstreamView, tag string) string {
	at := firstNonEmpty(view.BaseTag, shortCommit(view.Base))
	return fmt.Sprintf(`This is hi, not a person. %[1]s, the project this team started from, has a new version upstream: %[2]s. Dev has its %[3]s, plus the team's own work.

Read what changed: git -C /team/repo log --oneline %[4]s..%[5]s/%[2]s, and its release notes or changelog there (git -C /team/repo show %[5]s/%[2]s:CHANGELOG.md, if it has one). Then tell the people, in a few lines in their words, what's new that they would notice, and whether you think it matters for this app; if it fixes something they use or adds something they asked for, ask whether they want it. Don't start any work: bringing it in is a task like any other, with --upstream=%[2]s once someone says yes. If nothing in it matters to them, say so in one line.

What you write is posted under hi's message about the new version.`,
		config.Adopted.Name, tag, at, view.Base, teamUpstream)
}

var teamUpstreamRefPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/+-]{0,200}$`)

// teamUpstreamWorkOf is the task that brought upstream in, among the
// work a task continues.
func teamUpstreamWorkOf(tasks []teamTask, task teamTask) (teamTask, bool) {
	byID := map[string]teamTask{}
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
	return teamTask{}, false
}
