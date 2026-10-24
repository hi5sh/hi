package main

// Review rounds that end, and briefs that say what's new
// (docs/specs/approved/hi_team_rounds.md): a person's thinking keyword
// reaches one task, the review loop runs two rounds and then the people
// decide, and a small fix doesn't start beside other work.

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// teamThinkingKeyword is Claude Code's words for thinking harder.
var teamThinkingKeyword = regexp.MustCompile(`(?i)\b(ultrathink|megathink|think (?:very |really |super )?hard(?:er)?)\b`)

// teamThinkingDropped is what a keyword becomes where it doesn't belong:
// the brief still reads as written, and the member sees a word was there.
const teamThinkingDropped = "(thinking keyword: first task only)"

// teamLongBrief is how many words of the lead's own text make a brief
// long enough for a hint.
const teamLongBrief = 800

// teamDropThinking replaces the thinking keywords in text, and returns
// the ones it found, lowercase, each once.
func teamDropThinking(text string) (string, []string) {
	var found []string
	text = teamThinkingKeyword.ReplaceAllStringFunc(text, func(word string) string {
		if word = strings.ToLower(word); !containsString(found, word) {
			found = append(found, word)
		}
		return teamThinkingDropped
	})
	return text, found
}

// teamThinkingHint tells the lead hi took keywords out of a brief.
func teamThinkingHint(found []string) string {
	return fmt.Sprintf("hi took %q out of this brief: a person's thinking keyword reaches the first task after they write it, once. How hard each member thinks is its effort (hi team member).",
		strings.Join(found, `", "`))
}

// keepsThinking says whether a new task may keep thinking keywords: the
// first task started on a yes whose thread, up to that yes, has a person's
// message with one that no earlier task in the thread was given.
func (b *teamBroker) keepsThinking(task teamTask) bool {
	if task.Approval == nil || task.On != "" || task.Small || task.Kind != "task" {
		return false
	}
	approval := *task.Approval
	tasks := loadTeamTasks(b.team)
	thread := teamApprovalSlackThread(approval)
	given := "" // the latest message an earlier task in the thread got
	for _, other := range tasks {
		if other.ID == task.ID || other.Kind != "task" || other.Approval == nil {
			continue
		}
		if other.Approval.TS == approval.TS {
			return false // this yes started a task already
		}
		if thread != "" && teamApprovalSlackThread(*other.Approval) == thread && teamTSBefore(given, other.Approval.TS) {
			given = other.Approval.TS
		}
	}
	var messages []teamChannelMessage
	if thread != "" {
		if config, err := loadTeam(b.team); err == nil {
			messages = teamApprovalThread(config, approval)
		}
	}
	if len(messages) == 0 {
		return teamThinkingKeyword.MatchString(approval.Text)
	}
	for _, message := range messages {
		if !message.Bot && (given == "" || teamTSBefore(given, message.TS)) && teamThinkingKeyword.MatchString(message.Text) {
			return true
		}
	}
	return false
}

// teamChangesSinceYes counts the reviews that asked for changes in the
// work id belongs to, back to the last task started on a yes.
func teamChangesSinceYes(tasks []teamTask, id string) int {
	byID := map[string]teamTask{}
	for _, task := range tasks {
		byID[task.ID] = task
	}
	count := 0
	for seen := 0; seen < len(tasks); seen++ {
		task, ok := byID[id]
		if !ok {
			break
		}
		for _, review := range tasks {
			if review.Kind == "review" && review.Review == task.ID && review.Status == "done" && review.Verdict == "changes" {
				count++
			}
		}
		if task.Approval != nil || task.On == "" {
			break
		}
		id = task.On
	}
	return count
}

// teamBusyWork is a member's task or review still working, if one is.
func teamBusyWork(tasks []teamTask) *teamTask {
	for i, task := range tasks {
		if teamTaskActive(task) && !task.Survey {
			return &tasks[i]
		}
	}
	return nil
}

// teamPreviewChecks are the lines of an approving review that only the
// running app can settle: "Verify on the preview: ...".
func teamPreviewChecks(review teamTask) []string {
	if review.Report == nil {
		return nil
	}
	var block struct {
		FollowUps []string `json:"follow_ups"`
	}
	json.Unmarshal(review.Report.Report, &block)
	var checks []string
	for _, line := range block.FollowUps {
		line = strings.TrimSpace(line)
		if len(line) > len(teamPreviewCheck) && strings.EqualFold(line[:len(teamPreviewCheck)], teamPreviewCheck) {
			checks = append(checks, strings.TrimSpace(line[len(teamPreviewCheck):]))
		}
	}
	return checks
}

const teamPreviewCheck = "Verify on the preview:"

// teamReviewRules are the reviewer's rules on what a review may ask for,
// and, from the second round, what it checks.
func teamReviewRules(round int) string {
	var text strings.Builder
	text.WriteString("What a review may ask for:\n\n")
	fmt.Fprintf(&text, "- What only the running app can show isn't yours to check: the real service behind a key only the app has, the app's own data, the deployed app. It isn't a change needed. List each as a line starting %q in follow_ups, and approve if the rest is right: the lead checks those on the preview before merging.\n", teamPreviewCheck)
	text.WriteString("- An edge case the acceptance list doesn't ask for is for later: a line starting \"Later:\" in follow_ups, not a change needed, unless it loses data, leaks a secret, breaks what worked, or fails the acceptance list.\n")
	if round > 1 {
		fmt.Fprintf(&text, "- This is review %d. The brief below is the fix of what the earlier review asked for: check that those changes were made and broke nothing. Something new is a change needed only if it loses data, leaks a secret, breaks what worked, or fails the acceptance list; anything else is \"Later:\".\n", round)
	}
	text.WriteString("\n")
	return text.String()
}
