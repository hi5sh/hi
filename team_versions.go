package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Dev and production. Everything the lead merges goes to dev, the team's
// branch (main): the people try it in previews, and what doesn't work out
// can be thrown away. Production is a branch of its own that moves only
// when an owner says yes: a release puts dev's latest commit there as the
// next version (v1, v2, ...), tagged release-<n>, and releasing an earlier
// version puts production back on it. Every move of production, and every
// time dev is set back to it, is posted in the channel itself, not in a
// thread, so the people see it without following every conversation.
//
// Ideas not merged yet are capped: past teamMaxIdeas, one is merged or
// discarded before another starts, so they can't pile up beyond what the
// people can keep track of in Slack.

const (
	teamProductionBranch = "production"
	teamMaxIdeas         = 5
)

// teamRelease is one version of production.
type teamRelease struct {
	Version  int           `json:"version"`
	Commit   string        `json:"commit"`
	Notes    string        `json:"notes,omitempty"`
	Changes  []string      `json:"changes,omitempty"` // the merged tasks it brought, "t-4 Title"
	Approval *teamApproval `json:"approval,omitempty"`
	Released time.Time     `json:"released"`
}

// teamVersionEvent is one move of production, or dev set back to it.
type teamVersionEvent struct {
	What     string        `json:"what"`
	Approval *teamApproval `json:"approval,omitempty"`
	At       time.Time     `json:"at"`
}

// teamVersions is versions.json in the team's folder.
type teamVersions struct {
	Production int                `json:"production"` // the version production is on; 0 before the first release
	Releases   []teamRelease      `json:"releases"`
	History    []teamVersionEvent `json:"history"`
}

func teamVersionsPath(team string) string { return filepath.Join(teamDir(team), "versions.json") }

func loadTeamVersions(team string) (teamVersions, error) {
	var versions teamVersions
	data, err := os.ReadFile(teamVersionsPath(team))
	if errors.Is(err, os.ErrNotExist) {
		return versions, nil
	}
	if err != nil {
		return versions, err
	}
	return versions, json.Unmarshal(data, &versions)
}

func saveTeamVersions(team string, versions teamVersions) error {
	data, _ := json.MarshalIndent(versions, "", "  ")
	path := teamVersionsPath(team)
	if err := os.WriteFile(path+".tmp", append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(path+".tmp", path)
}

func (v teamVersions) release(version int) (teamRelease, bool) {
	for _, release := range v.Releases {
		if release.Version == version {
			return release, true
		}
	}
	return teamRelease{}, false
}

func teamReleaseTag(version int) string { return fmt.Sprintf("release-%d", version) }

// teamSameCode says whether two commits differ only in the lead's specs.
func teamSameCode(repo, a, b string) bool {
	return exec.Command("git", "-C", repo, "diff", "--quiet", a, b, "--", ".", ":(exclude)"+teamSpecsPath).Run() == nil
}

// teamDevChanges are the merged tasks that dev has and production hasn't.
func teamDevChanges(repo string, tasks []teamTask) []teamTask {
	production := boxGit(repo, "rev-parse", "--verify", "-q", teamProductionBranch)
	var changes []teamTask
	for _, task := range tasks {
		if task.Kind == "task" && task.Status == "merged" && task.Merged != "" && (production == "" || !teamIsAncestor(repo, task.Merged, production)) {
			changes = append(changes, task)
		}
	}
	return changes
}

// teamOpenIdeas are the pieces of work not merged or discarded: each line
// of tasks once, by its latest task.
func teamOpenIdeas(tasks []teamTask) []teamTask {
	continued := map[string]bool{}
	for _, task := range tasks {
		if task.Kind == "task" && task.On != "" && task.Status != "discarded" {
			continued[task.On] = true
		}
	}
	var ideas []teamTask
	for _, task := range tasks {
		if task.Kind != "task" || task.Status == "merged" || task.Status == "discarded" || continued[task.ID] {
			continue
		}
		if task.Branch == "" && !teamTaskActive(task) {
			continue // failed before it had any work
		}
		ideas = append(ideas, task)
	}
	return ideas
}

// teamChain is a task and every task that continues it, or that it
// continues: one idea.
func teamChain(tasks []teamTask, id string) []teamTask {
	in := map[string]bool{id: true}
	for grew := true; grew; {
		grew = false
		for _, task := range tasks {
			if task.Kind != "task" || task.On == "" || in[task.ID] == in[task.On] {
				continue
			}
			in[task.ID], in[task.On], grew = true, true, true
		}
	}
	var chain []teamTask
	for _, task := range tasks {
		if in[task.ID] && task.Kind == "task" {
			chain = append(chain, task)
		}
	}
	return chain
}

// teamVersionsView is what hi team versions shows.
type teamVersionsView struct {
	Production int           `json:"production"`
	Release    *teamRelease  `json:"release,omitempty"` // production's
	Dev        string        `json:"dev"`               // dev's label, such as "v3 + 2 changes"
	Changes    []teamTask    `json:"changes,omitempty"`
	Ideas      []teamTask    `json:"ideas,omitempty"`
	Releases   []teamRelease `json:"releases,omitempty"`
}

func teamVersionsReport(team string) (teamVersionsView, error) {
	versions, err := loadTeamVersions(team)
	if err != nil {
		return teamVersionsView{}, err
	}
	tasks := loadTeamTasks(team)
	view := teamVersionsView{Production: versions.Production, Releases: versions.Releases, Ideas: teamOpenIdeas(tasks)}
	view.Changes = teamDevChanges(filepath.Join(teamDir(team), "repo"), tasks)
	if release, ok := versions.release(versions.Production); ok {
		view.Release = &release
	}
	view.Dev = teamDevLabel(view.Production, len(view.Changes))
	return view, nil
}

// teamDevLabel names what dev has, the way the lead says it.
func teamDevLabel(production, changes int) string {
	switch {
	case production == 0:
		return fmt.Sprintf("dev, %s, nothing released yet", teamPlural(changes, "change"))
	case changes == 0:
		return fmt.Sprintf("dev, the same as production v%d", production)
	}
	return fmt.Sprintf("dev, v%d + %s", production, teamPlural(changes, "change"))
}

func teamPlural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

func printTeamVersions(view teamVersionsView, w io.Writer) {
	if view.Release == nil {
		fmt.Fprintln(w, "Production: nothing released yet.")
	} else {
		approved := ""
		if view.Release.Approval != nil {
			approved = ", approved by " + view.Release.Approval.Name
		}
		fmt.Fprintf(w, "Production: v%d, released %s UTC%s.\n", view.Production, view.Release.Released.UTC().Format("Jan 2 15:04"), approved)
	}
	fmt.Fprintf(w, "Dev: %s.\n", strings.TrimPrefix(view.Dev, "dev, "))
	const shown = 10
	for i, task := range view.Changes {
		if i == shown {
			fmt.Fprintf(w, "  and %d more\n", len(view.Changes)-shown)
			break
		}
		fmt.Fprintf(w, "  %-6s %s\n", task.ID, task.Title)
	}
	if len(view.Ideas) == 0 {
		fmt.Fprintf(w, "Ideas not merged: none (%d at most).\n", teamMaxIdeas)
	} else {
		fmt.Fprintf(w, "Ideas not merged: %d of %d at most.\n", len(view.Ideas), teamMaxIdeas)
		for _, task := range view.Ideas {
			fmt.Fprintf(w, "  %-6s %-9s %s\n", task.ID, task.Status, task.Title)
		}
	}
	if len(view.Releases) > 0 {
		fmt.Fprintln(w, "Releases:")
		for i := len(view.Releases) - 1; i >= 0 && i >= len(view.Releases)-5; i-- {
			release := view.Releases[i]
			current := ""
			if release.Version == view.Production {
				current = "  (production)"
			}
			fmt.Fprintf(w, "  v%-4d %s  %s%s\n", release.Version, release.Released.UTC().Format("Jan 2"), qClip(teamFirstLine(release.Notes)), current)
		}
	}
}

func teamFirstLine(text string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(text), "\n")
	return strings.TrimLeft(line, "-•* ")
}

// ---------------------------------------------------------------------------
// the broker's side

type teamReleaseRequest struct {
	Version  int    `json:"version,omitempty"` // an earlier release to put production back on; 0 releases dev
	Notes    string `json:"notes,omitempty"`
	Approval string `json:"approval,omitempty"`
}

// teamReleaseResult is what a release did.
type teamReleaseResult struct {
	Release  teamRelease `json:"release"`
	Previous int         `json:"previous"`
	Back     bool        `json:"back,omitempty"`  // production moved to an earlier release
	Again    bool        `json:"again,omitempty"` // ... or to a later one it had left
	Warning  string      `json:"warning,omitempty"`
}

// release moves production: to dev's latest commit as the next version, or
// to an earlier release. An owner's yes is required either way.
func (b *teamBroker) release(request teamReleaseRequest) (teamReleaseResult, error) {
	config, err := loadTeam(b.team)
	if err != nil {
		return teamReleaseResult{}, err
	}
	notes := strings.TrimSpace(request.Notes)
	if request.Version == 0 && notes == "" {
		return teamReleaseResult{}, errors.New("say what changes for the people: --notes <file|->, a few lines in their words; hi posts them in the channel")
	}
	if len(notes) > 3000 {
		return teamReleaseResult{}, errors.New("keep the notes short: a few lines, under 3000 characters")
	}
	approval, err := b.teamFindApproval(config, request.Approval, "release")
	if err != nil {
		return teamReleaseResult{}, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	versions, err := loadTeamVersions(b.team)
	if err != nil {
		return teamReleaseResult{}, err
	}
	result := teamReleaseResult{Previous: versions.Production}
	event := teamVersionEvent{Approval: &approval, At: time.Now().UTC()}
	var push []string
	if request.Version != 0 {
		release, ok := versions.release(request.Version)
		if !ok {
			return result, fmt.Errorf("there is no v%d; hi team versions lists the releases", request.Version)
		}
		if release.Version == versions.Production {
			return result, fmt.Errorf("production is on v%d already", release.Version)
		}
		if out, err := teamGit(b.repo, "branch", "-f", teamProductionBranch, release.Commit); err != nil {
			return result, fmt.Errorf("git branch: %s", qFirstLine(out, err.Error()))
		}
		result.Release = release
		result.Back, result.Again = release.Version < versions.Production, release.Version > versions.Production
		event.What = fmt.Sprintf("production from v%d to v%d", versions.Production, release.Version)
		versions.Production = release.Version
		push = []string{teamProductionBranch}
	} else {
		if err := b.commitSpecsLocked(config); err != nil {
			return result, fmt.Errorf("committing the specs: %w", err)
		}
		head := boxGit(b.repo, "rev-parse", config.Branch)
		if head == "" {
			return result, fmt.Errorf("the project has no %s branch", config.Branch)
		}
		production := boxGit(b.repo, "rev-parse", "--verify", "-q", teamProductionBranch)
		if production != "" && teamSameCode(b.repo, production, head) {
			return result, fmt.Errorf("dev has nothing new since production v%d", versions.Production)
		}
		var changes []string
		for _, task := range teamDevChanges(b.repo, loadTeamTasks(b.team)) {
			changes = append(changes, task.ID+" "+task.Title)
		}
		version := 1
		for _, release := range versions.Releases {
			version = max(version, release.Version+1)
		}
		release := teamRelease{Version: version, Commit: head, Notes: notes, Changes: changes, Approval: &approval, Released: event.At}
		if out, err := teamGit(b.repo, "branch", "-f", teamProductionBranch, head); err != nil {
			return result, fmt.Errorf("git branch: %s", qFirstLine(out, err.Error()))
		}
		if out, err := teamGit(b.repo, "tag", "-f", "-a", "-m", "Release v"+fmt.Sprint(version)+"\n\n"+notes, teamReleaseTag(version), head); err != nil {
			return result, fmt.Errorf("git tag: %s", qFirstLine(out, err.Error()))
		}
		versions.Releases = append(versions.Releases, release)
		event.What = fmt.Sprintf("release v%d", version)
		versions.Production, result.Release = version, release
		push = []string{teamProductionBranch, "refs/tags/" + teamReleaseTag(version)}
	}
	versions.History = append(versions.History, event)
	if err := saveTeamVersions(b.team, versions); err != nil {
		return result, err
	}
	if config.Remote != "" {
		// Production is the broker's alone, so moving it back may rewrite it.
		if out, err := teamGit(b.repo, append([]string{"push", "-q", "-f", "origin"}, push...)...); err != nil {
			result.Warning = "released, but git push failed: " + qFirstLine(out, err.Error())
		}
	}
	b.announce(config, teamReleaseAnnouncement(result, approval, notes))
	return result, nil
}

func teamReleaseAnnouncement(result teamReleaseResult, approval teamApproval, notes string) string {
	var text strings.Builder
	version := result.Release.Version
	switch {
	case result.Back:
		fmt.Fprintf(&text, ":leftwards_arrow_with_hook: *Production is back on v%d* (it was on v%d), approved by %s.", version, result.Previous, approval.Name)
	case result.Again:
		fmt.Fprintf(&text, ":rocket: *Production is on v%d again* (it was on v%d), approved by %s.", version, result.Previous, approval.Name)
	case result.Previous == 0:
		fmt.Fprintf(&text, ":rocket: *Production v%d is out*, the first release, approved by %s.", version, approval.Name)
	default:
		fmt.Fprintf(&text, ":rocket: *Production v%d is out* (it was v%d), approved by %s.", version, result.Previous, approval.Name)
	}
	if notes != "" {
		text.WriteString("\n\n" + notes)
	}
	return text.String()
}

// announce posts in the channel itself, outside any thread.
func (b *teamBroker) announce(config teamConfig, text string) {
	if b.channel != nil {
		b.channel.post(config.Channel, text)
	}
}

// discard drops an idea that isn't merged: the task, the tasks that
// continue it or that it continues, their reviews, boxes, and branches.
func (b *teamBroker) discard(id string) ([]teamTask, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	task, err := loadTeamTask(b.team, id)
	if err != nil {
		return nil, err
	}
	switch {
	case task.Kind != "task":
		return nil, fmt.Errorf("%s is a review; discard %s, the task it reviews", id, task.Review)
	case task.Status == "merged":
		return nil, fmt.Errorf("%s is in dev already; to drop it, set dev back to production (hi team discard dev --approval <id>), or give the coder a task to take it out", id)
	case task.Status == "discarded":
		return nil, fmt.Errorf("%s is discarded already", id)
	}
	tasks := loadTeamTasks(b.team)
	chain := teamChain(tasks, id)
	inIdea := map[string]bool{}
	for _, other := range chain {
		inIdea[other.ID] = true
	}
	// A review of the idea's work may be running, too.
	for _, other := range tasks {
		if (inIdea[other.ID] || inIdea[other.Review]) && teamTaskActive(other) {
			return nil, fmt.Errorf("%s is still working; hi team stop %s first", other.ID, other.ID)
		}
	}
	inChain := map[string]bool{}
	var discarded []teamTask
	for _, other := range chain {
		if other.Status == "merged" || other.Status == "discarded" {
			continue
		}
		inChain[other.ID] = true
		other.Status = "discarded"
		if err := saveTeamTask(b.team, other); err != nil {
			return discarded, err
		}
		discarded = append(discarded, other)
	}
	for _, other := range tasks {
		if (inChain[other.ID] || inChain[other.Review]) && !teamTaskActive(other) {
			b.run("box", "rm", "--force", other.Box)
			if other.Kind == "task" {
				os.RemoveAll(teamTaskFilesDir(b.team, other.ID))
			}
			if other.Branch != "" {
				teamGit(b.repo, "branch", "-D", other.Branch)
			}
		}
	}
	return discarded, nil
}

// resetDev sets dev's code back to production's, as a new commit, so what
// was merged since stays in the history. The lead's specs stay as they are.
func (b *teamBroker) resetDev(ref string) ([]teamTask, error) {
	config, err := loadTeam(b.team)
	if err != nil {
		return nil, err
	}
	approval, err := b.teamFindApproval(config, ref, "dev")
	if err != nil {
		return nil, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	versions, err := loadTeamVersions(b.team)
	if err != nil {
		return nil, err
	}
	if versions.Production == 0 {
		return nil, errors.New("nothing is released yet, so there's no production to go back to")
	}
	if err := b.commitSpecsLocked(config); err != nil {
		return nil, fmt.Errorf("committing the specs: %w", err)
	}
	if err := b.onTeamBranch(config); err != nil {
		return nil, err
	}
	if teamSameCode(b.repo, teamProductionBranch, "HEAD") {
		return nil, fmt.Errorf("dev is the same as production v%d already", versions.Production)
	}
	changes := teamDevChanges(b.repo, loadTeamTasks(b.team))
	var lines []string
	for _, task := range changes {
		lines = append(lines, "- "+task.ID+" "+task.Title)
	}
	if out, err := teamGit(b.repo, "restore", "--source="+teamProductionBranch, "--staged", "--worktree", "--", ".", ":(exclude)"+teamSpecsPath); err != nil {
		return nil, fmt.Errorf("git restore: %s", qFirstLine(out, err.Error()))
	}
	message := fmt.Sprintf("Set dev back to production v%d\n\nApproved by %s.", versions.Production, approval.Name)
	if len(lines) > 0 {
		message += " Set aside:\n\n" + strings.Join(lines, "\n")
	}
	if out, err := teamGit(b.repo, "commit", "-q", "-m", message); err != nil {
		teamGit(b.repo, "reset", "-q", "--hard", "HEAD")
		return nil, fmt.Errorf("git commit: %s", qFirstLine(out, err.Error()))
	}
	for i := range changes {
		changes[i].Status = "discarded"
		saveTeamTask(b.team, changes[i])
	}
	versions.History = append(versions.History, teamVersionEvent{What: fmt.Sprintf("dev back to v%d", versions.Production), Approval: &approval, At: time.Now().UTC()})
	if err := saveTeamVersions(b.team, versions); err != nil {
		return changes, err
	}
	if config.Remote != "" {
		if out, err := teamGit(b.repo, "push", "-q", "origin", config.Branch); err != nil {
			return changes, fmt.Errorf("dev is set back, but git push failed: %s", qFirstLine(out, err.Error()))
		}
	}
	text := fmt.Sprintf(":broom: *Dev is set back to production v%d*, approved by %s. Production didn't change.", versions.Production, approval.Name)
	if len(changes) > 0 {
		var titles []string
		for _, task := range changes {
			titles = append(titles, "• "+task.Title)
		}
		text += " Set aside:\n" + strings.Join(titles, "\n")
	}
	b.announce(config, text)
	return changes, nil
}

// onTeamBranch checks repo/ is clean and on the team's branch.
func (b *teamBroker) onTeamBranch(config teamConfig) error {
	if dirty := boxGit(b.repo, "status", "--porcelain", "--untracked-files=no"); dirty != "" {
		return errors.New("repo/ has changes that aren't committed; the lead's branch must be clean")
	}
	if current := boxGit(b.repo, "branch", "--show-current"); current != config.Branch {
		if out, err := teamGit(b.repo, "checkout", "-q", config.Branch); err != nil {
			return fmt.Errorf("git checkout %s: %s", config.Branch, out)
		}
	}
	return nil
}
