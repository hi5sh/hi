package main

// One session per member for each piece of work
// (docs/specs/approved/hi_team_threads.md, release 2). A piece of work is
// a chain: the first task, its reviews, its fix rounds, and the tasks that
// continue it. The coder and the reviewer each keep one conversation for
// it, in tasks/<first task>-sessions/<role> (hi agent --session): a fix
// round is the next turn in the coder's conversation, with the reviewer's
// notes, and a second review the next turn in the reviewer's, with the
// coder's answer. The broker stays in the middle: it writes each turn,
// counts the rounds, and keeps every turn as the task's brief.

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// teamSessionsKept is how long a chain's sessions stay after it is merged
// or discarded, for looking back.
const teamSessionsKept = 7 * 24 * time.Hour

func teamSessionDir(team, root, role string) string {
	return filepath.Join(teamTaskDir(team), root+"-sessions", role)
}

// teamSessionAgent says whether a member's agent can keep its session.
func teamSessionAgent(agent string) bool { return agent == "claude" || agent == "codex" }

// teamSessionModel is the model hi agent records for a member's session:
// its own, else the agent's default on this machine.
func teamSessionModel(member teamMember) string {
	return firstNonEmpty(member.Model, defaultAgentModel(member.Agent))
}

// teamChainTasks are the tasks of the chain root starts, reviews too.
func teamChainTasks(tasks []teamTask, root string) []teamTask {
	in := map[string]bool{}
	for _, task := range tasks {
		if task.Kind != "review" && teamChainRoot(tasks, task.ID) == root {
			in[task.ID] = true
		}
	}
	var chain []teamTask
	for _, task := range tasks {
		if in[task.ID] || task.Kind == "review" && in[task.Review] {
			chain = append(chain, task)
		}
	}
	return chain
}

// teamLastReview is the chain's latest finished review with its commit:
// what the reviewer last looked at.
func teamLastReview(tasks []teamTask, root string) (teamTask, bool) {
	var last teamTask
	for _, task := range teamChainTasks(tasks, root) {
		if task.Kind == "review" && task.Status == "done" && task.Commit != "" && (last.ID == "" || task.Created.After(last.Created)) {
			last = task
		}
	}
	return last, last.ID != ""
}

// teamSessionHint tells the lead whether a step goes on with its member's
// conversation, and why not.
func teamSessionHint(task teamTask, from string, err error) string {
	if err == nil {
		return fmt.Sprintf("The %s goes on with its conversation from %s, so this brief is only what's new.", task.Role, from)
	}
	return fmt.Sprintf("The %s starts a new conversation (%v), with the whole brief.", task.Role, err)
}

// teamWhereWorkStands is what a member starting a new conversation on
// continued work gets of the one before: the last work's report.
func teamWhereWorkStands(on teamTask) string {
	if on.Report == nil || strings.TrimSpace(on.Report.Text+string(on.Report.Report)) == "" {
		return ""
	}
	text := strings.TrimSpace(on.Report.Text)
	if len(on.Report.Report) > 0 && string(on.Report.Report) != "null" {
		text += "\n\n" + string(on.Report.Report)
	}
	return fmt.Sprintf("\n## Where the work stands\n\nThe %s's conversation on this work had grown too long to go on with, so this is a new one. Its last report, from %s:\n\n%s\n",
		on.Role, on.ID, teamClipText(text, 8<<10))
}

// teamReviewHandover is the reviewer's next turn: what the coder did since
// its last review, and what to check.
func teamReviewHandover(team, branch string, task, reviewed, last teamTask, brief, notes string) string {
	var text strings.Builder
	fmt.Fprintf(&text, "# Review %s for the %s team\n\n", task.ID, team)
	if reviewed.Round > 0 {
		fmt.Fprintf(&text, "The %s answered your review %s with task %s, on this branch.", reviewed.Role, last.ID, reviewed.ID)
	} else {
		fmt.Fprintf(&text, "The lead continued the work you reviewed in %s with task %s, on this branch; its brief is below.", last.ID, reviewed.ID)
	}
	fmt.Fprintf(&text, " Look at what changed since you last looked:\n\n    git log --oneline %[1]s..HEAD\n    git diff %[1]s..HEAD\n\n", last.Commit)
	fmt.Fprintf(&text, "Judge it as before, against the current specs on %s (git show %s:docs/specs/<file>).\n\n", branch, branch)
	text.WriteString(teamReviewRules(task.Round))
	text.WriteString("Don't fix anything yourself and don't commit: say what must change.\n\n")
	text.WriteString("In your report, start the summary with \"Approve:\" if it can be merged as it is, or with \"Changes needed:\" and put each change in follow_ups.\n\n")
	if notes = strings.TrimSpace(notes); notes != "" {
		fmt.Fprintf(&text, "## The lead's notes\n\n%s\n\n", notes)
	}
	fmt.Fprintf(&text, "## The brief for %s\n\n%s\n", reviewed.ID, teamBriefBody(brief))
	if reviewed.Report != nil && len(reviewed.Report.Report) > 0 && string(reviewed.Report.Report) != "null" {
		fmt.Fprintf(&text, "\n## The %s's report\n\n%s\n", reviewed.Role, string(reviewed.Report.Report))
	}
	return text.String()
}

// sessionFailed sets aside a session whose resumed run failed, so the
// next step starts a new one, with the whole brief.
func (b *teamBroker) sessionFailed(task teamTask) {
	if !task.Resumes || task.Session == "" {
		return
	}
	state := filepath.Join(task.Session, agentSessionStateFile)
	if fileExists(state) {
		os.Rename(state, filepath.Join(task.Session, "hi-session.failed.json"))
	}
}

// keepSessions removes the sessions of chains merged or discarded a week
// ago, once an hour.
func (b *teamBroker) keepSessions(done <-chan struct{}, stderr io.Writer) {
	for {
		if err := teamPruneSessions(b.team, time.Now()); err != nil {
			fmt.Fprintf(stderr, "hi: the members' sessions: %v\n", err)
		}
		select {
		case <-done:
			return
		case <-time.After(time.Hour):
		}
	}
}

func teamPruneSessions(team string, now time.Time) error {
	dirs, _ := filepath.Glob(filepath.Join(teamTaskDir(team), "*-sessions"))
	if len(dirs) == 0 {
		return nil
	}
	tasks := loadTeamTasks(team)
	for _, dir := range dirs {
		root := strings.TrimSuffix(filepath.Base(dir), "-sessions")
		chain := teamChainTasks(tasks, root)
		ended, latest := len(chain) > 0, time.Time{}
		for _, task := range chain {
			if teamTaskActive(task) || task.Kind == "task" && task.Status != "merged" && task.Status != "discarded" && !teamTaskSuperseded(chain, task) {
				ended = false
			}
			if task.Finished.After(latest) {
				latest = task.Finished
			}
		}
		if ended && now.Sub(latest) > teamSessionsKept {
			if err := os.RemoveAll(dir); err != nil {
				return err
			}
		}
	}
	return nil
}

// teamTaskSuperseded says whether a later task of the chain continues
// task, so its own status no longer says how the work ended.
func teamTaskSuperseded(chain []teamTask, task teamTask) bool {
	for _, other := range chain {
		if other.On == task.ID {
			return true
		}
	}
	return false
}
