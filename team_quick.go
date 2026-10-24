package main

// Small work doesn't wait on its review, and checks don't get one
// (docs/specs/approved/hi_team_rounds.md, v1.14.3). On cartman a chart
// was done and approved 15 minutes after it was asked for, and the next
// 22 went on three more coder tasks, each reviewed, that only checked it.
//
//   - Work that changes only docs/ since a review approved it, such as a
//     verification record, gets no review, and merges on that approval.
//   - Quick work, a small fix or a change of few lines, merges while its
//     review runs: the lead's wait ends when the coder is done, and when
//     the review afterwards asks for changes, hi wakes the lead to fix
//     them in dev.

import (
	"fmt"
	"strconv"
	"strings"
)

// teamQuickLines is the most lines a change outside docs/ may add and
// remove together and still merge before its review.
const teamQuickLines = 100

// teamWorkDiff is what changed between two commits: the files, and the
// lines added and removed outside docs/.
func teamWorkDiff(repo, from, to string) (files []string, codeLines int) {
	for _, name := range strings.Split(boxGit(repo, "diff", "--name-only", from, to), "\n") {
		if name != "" {
			files = append(files, name)
		}
	}
	for _, line := range strings.Split(boxGit(repo, "diff", "--numstat", from, to), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 || teamDocsPath(fields[2]) {
			continue
		}
		added, _ := strconv.Atoi(fields[0]) // "-" for a binary file
		removed, _ := strconv.Atoi(fields[1])
		codeLines += added + removed
	}
	return files, codeLines
}

func teamDocsPath(name string) bool { return strings.HasPrefix(name, "docs/") }

// approvedDocsOnly is the review that approved an earlier commit of the
// work, when everything since changed only docs/: then the work needs no
// review again.
func (b *teamBroker) approvedDocsOnly(work teamTask) (teamTask, bool) {
	head := boxGit(b.repo, "rev-parse", work.Branch)
	if head == "" {
		return teamTask{}, false
	}
	tasks := loadTeamTasks(b.team)
	root := firstNonEmpty(teamChainRoot(tasks, work.ID), work.ID)
	var best teamTask
	for _, review := range teamChainTasks(tasks, root) {
		if review.Kind != "review" || review.Status != "done" || review.Verdict != "approve" || review.Commit == "" || review.Commit == head {
			continue
		}
		if !teamIsAncestor(b.repo, review.Commit, head) || best.ID != "" && !review.Created.After(best.Created) {
			continue
		}
		files, _ := teamWorkDiff(b.repo, review.Commit, head)
		docsOnly := len(files) > 0
		for _, name := range files {
			docsOnly = docsOnly && teamDocsPath(name)
		}
		if docsOnly {
			best = review
		}
	}
	return best, best.ID != ""
}

// quickWork says whether work may merge before its review: a small fix,
// or few changed lines outside docs/ since the team's branch.
func (b *teamBroker) quickWork(work teamTask) bool {
	if work.Kind != "task" || work.Branch == "" {
		return false
	}
	tasks := loadTeamTasks(b.team)
	if root, err := loadTeamTask(b.team, firstNonEmpty(teamChainRoot(tasks, work.ID), work.ID)); err == nil && root.Small {
		return true
	}
	config, err := loadTeam(b.team)
	if err != nil {
		return false
	}
	base := boxGit(b.repo, "merge-base", config.Branch, work.Branch)
	if base == "" {
		return false
	}
	files, lines := teamWorkDiff(b.repo, base, work.Branch)
	return len(files) > 0 && lines <= teamQuickLines
}

// reviewedAfterMerge wakes the lead when a review that ran after its work
// was merged asks for changes: they are fixed in dev, with a small fix.
func (b *teamBroker) reviewedAfterMerge(review teamTask) {
	if review.Kind != "review" || review.Verdict != "changes" || b.wakeLead == nil {
		return
	}
	reviewed, err := loadTeamTask(b.team, review.Review)
	if err != nil || reviewed.Status != "merged" {
		return
	}
	config, err := loadTeam(b.team)
	if err != nil {
		return
	}
	tasks := loadTeamTasks(b.team)
	byID := map[string]teamTask{}
	for _, task := range tasks {
		byID[task.ID] = task
	}
	root, _ := teamProgressRoot(byID, reviewed)
	block := teamReviewBlock(review)
	var changes []string
	for _, change := range teamMustChange(block.FollowUps) {
		changes = append(changes, "- "+change)
	}
	prompt := fmt.Sprintf(`This is hi, not a person. You merged %s (%s) before its review, as quick work may be. The review has ended, and asks for changes: %s

%s

They are in dev, not production. Fix them with a small fix (hi team task coder <brief.md> --small --in <id>), or, if a change isn't worth it, say why. Say in a line to the people in the thread what the review found and what you're doing about it.`,
		reviewed.ID, reviewed.Title, strings.TrimSpace(block.Summary), strings.Join(changes, "\n"))
	b.wakeLead("hi-after-review-"+review.ID, prompt, "slack:"+config.Channel+":"+teamProgressThread(root))
}
