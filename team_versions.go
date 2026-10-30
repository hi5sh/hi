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

// Dev and production. Everything the lead merges goes to dev, the team's
// branch (main): the people try it in previews, and what doesn't work out
// can be thrown away. Production is a branch of its own that moves only
// when an owner or a developer says yes: a release puts dev's latest commit there as the
// next version (v1, v2, ...), tagged release-<n>, and releasing an earlier
// version puts production back on it. Every move of production, and every
// time dev is set back to it, is posted in the channel itself, not in a
// thread, so the people see it without following every conversation.

const teamProductionBranch = "production"

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
	// From and To are the versions production moved between, and Snapshot
	// the one taken just before: production's data as From left it, which
	// a rollback to From puts back. Events before v0.56.0 have none.
	From     int    `json:"from,omitempty"`
	To       int    `json:"to,omitempty"`
	Snapshot string `json:"snapshot,omitempty"`
}

// leftSnapshot is the snapshot taken the last time production left
// version: its data as that version left it.
func (v teamVersions) leftSnapshot(version int) (teamVersionEvent, bool) {
	for i := len(v.History) - 1; i >= 0; i-- {
		if event := v.History[i]; event.From == version && event.Snapshot != "" {
			return event, true
		}
	}
	return teamVersionEvent{}, false
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

// teamWork is one piece of work that dev has and production hasn't: the
// task it was asked for with (the root of its line of tasks), and every
// merged task of that line, such as the fixes after a review.
type teamWork struct {
	Root teamTask
	IDs  []string
}

// teamDevWork groups dev's changes by the task each line started with, so
// a fix after a review shows as the work it fixed, asked for by whoever
// asked for that.
func teamDevWork(all, changes []teamTask) []teamWork {
	byID := map[string]teamTask{}
	for _, task := range all {
		byID[task.ID] = task
	}
	var work []teamWork
	at := map[string]int{}
	for _, task := range changes {
		root, seen := task, map[string]bool{}
		for root.On != "" && !seen[root.ID] {
			seen[root.ID] = true
			up, ok := byID[root.On]
			if !ok {
				break
			}
			root = up
		}
		i, ok := at[root.ID]
		if !ok {
			i, at[root.ID] = len(work), len(work)
			work = append(work, teamWork{Root: root})
		}
		if root.ID != task.ID && !containsString(work[i].IDs, root.ID) {
			work[i].IDs = append(work[i].IDs, root.ID)
		}
		work[i].IDs = append(work[i].IDs, task.ID)
	}
	return work
}

var teamSlackTSPattern = regexp.MustCompile(`^[0-9]{9,11}\.[0-9]{6}$`)

// teamVersionsView is what hi team versions shows.
type teamVersionsView struct {
	Production int           `json:"production"`
	Release    *teamRelease  `json:"release,omitempty"` // production's
	Dev        string        `json:"dev"`               // dev's label, such as "v3 + 2 changes"
	Changes    []teamTask    `json:"changes,omitempty"`
	Releases   []teamRelease `json:"releases,omitempty"`
}

func teamVersionsReport(team string) (teamVersionsView, error) {
	versions, err := loadTeamVersions(team)
	if err != nil {
		return teamVersionsView{}, err
	}
	tasks := loadTeamTasks(team)
	view := teamVersionsView{Production: versions.Production, Releases: versions.Releases}
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
	Version int    `json:"version,omitempty"` // an earlier release to put production back on; 0 releases dev
	Notes   string `json:"notes,omitempty"`
	// Data puts production's data back as Version left it too: a rollback.
	Data bool `json:"data,omitempty"`
	// DataFrom is "preview" for a release that gives production a copy of
	// the preview's data, under the same yes.
	DataFrom string `json:"data_from,omitempty"`
	// host is an owner on the machine, through the host's socket, who
	// needs no yes in the channel. The lead's socket never sets it.
	host string
}

// teamReleaseResult is what a release did.
type teamReleaseResult struct {
	Release  teamRelease `json:"release"`
	Previous int         `json:"previous"`
	Back     bool        `json:"back,omitempty"`  // production moved to an earlier release
	Again    bool        `json:"again,omitempty"` // ... or to a later one it had left
	Warning  string      `json:"warning,omitempty"`
	// Snapshot is the one taken just before, of the data and memory.
	Snapshot string `json:"snapshot,omitempty"`
	// DataFrom is the snapshot a rollback put production's data back from,
	// taken when DataAt.
	DataFrom string    `json:"data_from,omitempty"`
	DataAt   time.Time `json:"data_at,omitzero"`
	// PreviewData is how much of the preview's data a release copied to
	// production (--data preview).
	PreviewData int64 `json:"preview_data,omitempty"`
	// Preview is the preview's new fork of production's data after the
	// release, or its empty folder (previewAfterRelease).
	Preview *teamDataCopy `json:"preview,omitempty"`
	// Approval is the yes that moved production this time.
	Approval *teamApproval `json:"approval,omitempty"`
	// AskedBy says who asked for the work a release holds.
	AskedBy string `json:"asked_by,omitempty"`
}

// release moves production: to dev's latest commit as the next version, or
// to an earlier release. A yes is required either way: an owner's or a
// developer's for a release, an owner's for a rollback.
func (b *teamBroker) release(request teamReleaseRequest) (teamReleaseResult, error) {
	defer teamMarkBusy(b.team, teamBusyReleasing)()
	result, announcement, err := b.moveProduction(request)
	if err != nil {
		return result, err
	}
	config, configErr := loadTeam(b.team)
	if configErr == nil {
		announcement += teamReleaseHelperNote(config)
	}
	// Production's address shows the new version before the channel hears.
	app, docs := "", ""
	if b.channel != nil {
		if shown, err := b.production(0); err != nil {
			result.Warning = strings.TrimSpace(result.Warning + "\nProduction's address still shows the version before: " + err.Error())
		} else if shown.URL != "" {
			app = shown.URL
			// An address on a relay from before /docs/ shows the app there.
			if shown.Relay == teamRelayStamp(b.team, teamSlotProduction) {
				docs = teamDocsLink(shown.URL)
			}
		}
	}
	if configErr == nil {
		b.announceInThread(config, teamReleaseHeadline(result, app, docs), announcement)
	}
	// The preview runs production's code now, and gets its data too.
	if preview, done, err := b.previewAfterRelease(); err != nil {
		result.Warning = strings.TrimSpace(result.Warning + "\nThe preview didn't get a new fork of production's data: " + err.Error())
	} else if done {
		result.Preview = &preview
		if preview.Warning != "" {
			result.Warning = strings.TrimSpace(result.Warning + "\n" + preview.Warning)
		}
	}
	return result, nil
}

// teamReleaseHeadline is the release's one line in the channel; what it
// brings goes in the line's thread.
func teamReleaseHeadline(result teamReleaseResult, app, docs string) string {
	version := result.Release.Version
	var text string
	switch {
	case result.Back:
		text = fmt.Sprintf("PROD back on v%d :leftwards_arrow_with_hook:", version)
	case result.Again:
		text = fmt.Sprintf("PROD on v%d again :rocket:", version)
	default:
		text = fmt.Sprintf("New PROD version (v%d) :rocket:", version)
	}
	return text + teamSlotLinks(app, docs)
}

// teamSlotLinks are an address's links in a headline: " -> App: link -
// Docs: link".
func teamSlotLinks(app, docs string) string {
	if app == "" {
		return ""
	}
	text := fmt.Sprintf(" -> App: <%s|link>", app)
	if docs != "" {
		text += fmt.Sprintf(" - Docs: <%s|link>", docs)
	}
	return text
}

// teamReleaseHelperNote tells the channel that production has no helper,
// so people trying a release don't look for the chat. On is the default
// and goes unsaid.
func teamReleaseHelperNote(config teamConfig) string {
	if teamHelperOn(config, teamSlotProduction) {
		return ""
	}
	return "\n\nThe helper, help and feedback in the app, is off in production."
}

// moveProduction moves the production branch and records the release; it
// returns what to post in the channel.
func (b *teamBroker) moveProduction(request teamReleaseRequest) (teamReleaseResult, string, error) {
	result, err := b.moveProductionLocked(request)
	if err != nil {
		return result, "", err
	}
	return result, teamReleaseAnnouncement(result, *result.Approval, strings.TrimSpace(request.Notes)), nil
}

func (b *teamBroker) moveProductionLocked(request teamReleaseRequest) (teamReleaseResult, error) {
	config, err := loadTeam(b.team)
	if err != nil {
		return teamReleaseResult{}, err
	}
	notes := strings.TrimSpace(request.Notes)
	if request.Version == 0 && notes == "" {
		if request.Data {
			return teamReleaseResult{}, errors.New("name the version to roll back to: hi team rollback <team> v2")
		}
		return teamReleaseResult{}, errors.New("say what changes for the people: --notes <file|->, a few lines in their words; hi posts them in the channel")
	}
	if len(notes) > 3000 {
		return teamReleaseResult{}, errors.New("keep the notes short: a few lines, under 3000 characters")
	}
	if request.DataFrom != "" && (request.DataFrom != teamSlotPreview || request.Version != 0) {
		return teamReleaseResult{}, errors.New("--data preview goes with a release of dev: production gets a copy of the preview's data")
	}
	var approval teamApproval
	if request.host != "" {
		approval = teamApproval{Name: request.host}
	} else {
		return teamReleaseResult{}, errors.New("production moves on a click on the dev page; on the machine, hi team rollback puts it back on an earlier release")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	versions, err := loadTeamVersions(b.team)
	if err != nil {
		return teamReleaseResult{}, err
	}
	result := teamReleaseResult{Previous: versions.Production, Approval: &approval}
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
		var left teamVersionEvent
		if request.Data {
			var ok bool
			if left, ok = versions.leftSnapshot(release.Version); !ok {
				return result, fmt.Errorf("hi has no snapshot of production's data as v%d left it (hi snapshots it when production moves, since v0.55.0); Roll back to v%d without its data puts back its code alone", release.Version, release.Version)
			}
		}
		b.snapshotBefore(release.Version, &result)
		if request.Data {
			if result.Snapshot == "" {
				return result, fmt.Errorf("nothing was rolled back: production's data now couldn't be snapshotted, so what was entered since v%d would be lost. %s", release.Version, strings.TrimSpace(result.Warning))
			}
			if err := b.putBackData(left.Snapshot); err != nil {
				return result, fmt.Errorf("nothing was rolled back: %w", err)
			}
			result.DataFrom, result.DataAt = left.Snapshot, left.At
		}
		if out, err := teamGit(b.repo, "branch", "-f", teamProductionBranch, release.Commit); err != nil {
			return result, fmt.Errorf("git branch: %s", qFirstLine(out, err.Error()))
		}
		result.Release = release
		result.Back, result.Again = release.Version < versions.Production, release.Version > versions.Production
		event.What = fmt.Sprintf("production from v%d to v%d", versions.Production, release.Version)
		if request.Data {
			event.What += fmt.Sprintf(", with its data from snapshot %s", left.Snapshot)
		}
		event.From, event.To = versions.Production, release.Version
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
		all := loadTeamTasks(b.team)
		tasks := teamDevChanges(b.repo, all)
		for _, task := range tasks {
			changes = append(changes, task.ID+" "+task.Title)
		}
		result.AskedBy = teamAskedBy(teamDevWork(all, tasks))
		version := 1
		for _, release := range versions.Releases {
			version = max(version, release.Version+1)
		}
		release := teamRelease{Version: version, Commit: head, Notes: notes, Changes: changes, Approval: &approval, Released: event.At}
		b.snapshotBefore(version, &result)
		if request.DataFrom == teamSlotPreview {
			if _, had, _ := teamDataUsage(teamSlotDataDir(b.team, teamSlotProduction)); had > 0 && result.Snapshot == "" {
				return result, fmt.Errorf("nothing was released: production's data couldn't be snapshotted first, so it would be lost. %s", strings.TrimSpace(result.Warning))
			}
			copied, err := b.copyDataLocked(teamSlotPreview, teamSlotProduction)
			if err != nil {
				return result, fmt.Errorf("nothing was released: %w", err)
			}
			result.PreviewData = copied.Bytes
		}
		if out, err := teamGit(b.repo, "branch", "-f", teamProductionBranch, head); err != nil {
			return result, fmt.Errorf("git branch: %s", qFirstLine(out, err.Error()))
		}
		if out, err := teamGit(b.repo, "tag", "-f", "-a", "-m", "Release v"+fmt.Sprint(version)+"\n\n"+notes, teamReleaseTag(version), head); err != nil {
			return result, fmt.Errorf("git tag: %s", qFirstLine(out, err.Error()))
		}
		versions.Releases = append(versions.Releases, release)
		event.What = fmt.Sprintf("release v%d", version)
		if result.PreviewData > 0 {
			event.What += ", with a copy of the preview's data"
		}
		event.From, event.To = versions.Production, version
		versions.Production, result.Release = version, release
		push = []string{teamProductionBranch, "refs/tags/" + teamReleaseTag(version)}
	}
	event.Snapshot = result.Snapshot
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
	return result, nil
}

func teamReleaseAnnouncement(result teamReleaseResult, approval teamApproval, notes string) string {
	var text strings.Builder
	if strings.HasPrefix(approval.TS, "dm:") {
		approval.Name += " in a DM"
	}
	version := result.Release.Version
	data := ""
	if result.DataFrom != "" {
		data = fmt.Sprintf(", with its data as of %s", result.DataAt.Local().Format("Mon Jan 2 at 15:04"))
	}
	switch {
	case result.DataFrom != "" && result.Back:
		fmt.Fprintf(&text, ":leftwards_arrow_with_hook: *Production is back on v%d%s* (it was on v%d), approved by %s. What was entered in production since is kept in snapshot %s: going forward to v%d the same way puts it back.",
			version, data, result.Previous, approval.Name, result.Snapshot, result.Previous)
	case result.DataFrom != "":
		fmt.Fprintf(&text, ":rocket: *Production is on v%d again%s* (it was on v%d), approved by %s. Production's data from v%d is kept in snapshot %s.",
			version, data, result.Previous, approval.Name, result.Previous, result.Snapshot)
	case result.Back:
		fmt.Fprintf(&text, ":leftwards_arrow_with_hook: *Production is back on v%d* (it was on v%d), approved by %s.", version, result.Previous, approval.Name)
	case result.Again:
		fmt.Fprintf(&text, ":rocket: *Production is on v%d again* (it was on v%d), approved by %s.", version, result.Previous, approval.Name)
	case result.Previous == 0:
		fmt.Fprintf(&text, ":rocket: *Production v%d is out*, the first release, approved by %s.", version, approval.Name)
	default:
		fmt.Fprintf(&text, ":rocket: *Production v%d is out* (it was v%d), approved by %s.", version, result.Previous, approval.Name)
	}
	if result.PreviewData > 0 {
		fmt.Fprintf(&text, " Production starts with a copy of the preview's data (%s).", teamDataSize(result.PreviewData))
	}
	if notes != "" {
		text.WriteString("\n\n" + notes)
	}
	if result.AskedBy != "" {
		text.WriteString("\n\nAsked for by " + teamSlackEscape(result.AskedBy) + ".")
	}
	return text.String()
}

// teamTaskAskedBy says who asked for one piece of work, and where.
func teamTaskAskedBy(task teamTask) string {
	switch {
	case task.Role == teamDevRole:
		return "built in the dev box"
	case task.Approval == nil || task.Approval.Name == "":
		if task.Small {
			return "a small fix by the lead"
		}
		return "continuing earlier work"
	case strings.HasPrefix(task.Approval.TS, "dm:"):
		return "asked for by " + teamSlackEscape(task.Approval.Name) + " in a DM"
	case teamWebMessageID.MatchString(task.Approval.TS):
		return "asked for by " + teamSlackEscape(task.Approval.Name) + " in the app"
	}
	return "asked for by " + teamSlackEscape(task.Approval.Name)
}

// teamAskedBy says who asked for a release's work, from its tasks' yeses
// wherever they were given: Anna (t-12, t-14), Bo (t-13).
func teamAskedBy(work []teamWork) string {
	var names []string
	ids := map[string][]string{}
	for _, piece := range work {
		task := piece.Root
		name := "the lead, as small fixes"
		if task.Approval != nil && task.Approval.Name != "" {
			name = task.Approval.Name
		} else if !task.Small {
			continue
		}
		if ids[name] == nil {
			names = append(names, name)
		}
		ids[name] = append(ids[name], firstNonEmpty(strings.Join(piece.IDs, ", "), task.ID))
	}
	var parts []string
	for _, name := range names {
		parts = append(parts, fmt.Sprintf("%s (%s)", name, strings.Join(ids[name], ", ")))
	}
	return strings.Join(parts, ", ")
}

// announce posts in the channel itself, outside any thread.
func (b *teamBroker) announce(config teamConfig, text string) {
	if b.channel != nil {
		b.channel.post(config.Channel, text)
	}
}

// announceOps posts in the ops channel, or the main one without it.
func (b *teamBroker) announceOps(config teamConfig, text string) {
	if b.channel != nil {
		b.channel.post(config.opsChannel(), text)
	}
}

// announceInThread posts one line in the channel, and the rest in its
// thread, where people who want it read it and talk to the lead about it.
func (b *teamBroker) announceInThread(config teamConfig, headline, details string) string {
	if b.channel == nil {
		return ""
	}
	ts, err := b.channel.post(config.Channel, headline)
	if err != nil {
		return ""
	}
	if details = strings.TrimSpace(details); details != "" {
		b.channel.postIn(config.Channel, ts, details)
	}
	return ts
}

// resetDev sets dev's code back to production's, as a new commit, so what
// was merged since stays in the history. The lead's specs stay as they are.
// owner is who did it, an owner on the dev page.
func (b *teamBroker) resetDev(owner string) ([]teamTask, error) {
	config, err := loadTeam(b.team)
	if err != nil {
		return nil, err
	}
	approval := teamApproval{Name: owner}
	b.mu.Lock()
	defer b.mu.Unlock()
	versions, err := loadTeamVersions(b.team)
	if err != nil {
		return nil, err
	}
	// Before the first release, an adopted project goes back to v0, the
	// project as it came.
	target, label := teamProductionBranch, fmt.Sprintf("production v%d", versions.Production)
	if versions.Production == 0 {
		if _, adopted := versions.release(0); !adopted || boxGit(b.repo, "rev-parse", "--verify", "-q", teamReleaseTag(0)) == "" {
			return nil, errors.New("nothing is released yet, so there's no production to go back to")
		}
		target, label = teamReleaseTag(0), "v0, the project as adopted"
	}
	if err := b.commitSpecsLocked(config); err != nil {
		return nil, fmt.Errorf("committing the specs: %w", err)
	}
	if err := b.onTeamBranch(config); err != nil {
		return nil, err
	}
	if teamSameCode(b.repo, target, "HEAD") {
		return nil, fmt.Errorf("dev is the same as %s already", label)
	}
	changes := teamDevChanges(b.repo, loadTeamTasks(b.team))
	var lines []string
	for _, task := range changes {
		lines = append(lines, "- "+task.ID+" "+task.Title)
	}
	if out, err := teamGit(b.repo, "restore", "--source="+target, "--staged", "--worktree", "--", ".", ":(exclude)"+teamSpecsPath); err != nil {
		return nil, fmt.Errorf("git restore: %s", qFirstLine(out, err.Error()))
	}
	message := fmt.Sprintf("Set dev back to %s\n\nApproved by %s.", label, approval.Name)
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
	versions.History = append(versions.History, teamVersionEvent{What: "dev back to " + strings.TrimPrefix(label, "production "), Approval: &approval, At: time.Now().UTC()})
	if err := saveTeamVersions(b.team, versions); err != nil {
		return changes, err
	}
	if config.Remote != "" {
		if out, err := teamGit(b.repo, "push", "-q", "origin", config.Branch); err != nil {
			return changes, fmt.Errorf("dev is set back, but git push failed: %s", qFirstLine(out, err.Error()))
		}
	}
	text := fmt.Sprintf(":broom: *Dev is set back to %s*, approved by %s. Production didn't change.", label, approval.Name)
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
