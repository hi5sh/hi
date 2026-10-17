package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// A progress card is a reply hi keeps current in the Slack thread where
// work was agreed: a plan of the work's tasks (the coder's, each review,
// each fix), each running, done, or failed, under a title that says what
// is happening now, so the people see the members work while the lead
// waits for them.
//
// It is an ordinary message with a plan block, which hi edits. A live
// message (chat.startStream) shows a spinner, but Slack ends it after
// about ten minutes and marks a task still running then as failed, and a
// task can run for an hour, so hi doesn't stream.

const (
	teamProgressEvery = 20 * time.Second
	// teamProgressSettle is how long a card waits, with nothing running,
	// before it ends: the broker starts the next review or fix in that time.
	teamProgressSettle = 3 * time.Minute
)

// teamProgressCard is a card's record, in run/progress.json.
type teamProgressCard struct {
	Root    string    `json:"root"` // the task the work was agreed for
	Thread  string    `json:"thread"`
	TS      string    `json:"ts"`
	Tasks   []string  `json:"tasks"`             // the tasks it shows, in order
	Shown   string    `json:"shown,omitempty"`   // what it shows, to edit it only when that changes
	Active  time.Time `json:"active"`            // when a task last ran
	Closed  bool      `json:"closed,omitempty"`  // done with: the work settled, or the card is gone
	Removed bool      `json:"removed,omitempty"` // someone deleted it: no new card for this work
}

type teamProgressState struct {
	Cards []teamProgressCard `json:"cards"`
}

func teamProgressPath(team string) string {
	return filepath.Join(teamDir(team), "run", "progress.json")
}

func loadTeamProgress(team string) teamProgressState {
	var state teamProgressState
	if data, err := os.ReadFile(teamProgressPath(team)); err == nil {
		json.Unmarshal(data, &state)
	}
	return state
}

func saveTeamProgress(team string, state teamProgressState) error {
	// A card that ended a week ago is done with.
	var keep []teamProgressCard
	for _, card := range state.Cards {
		if !card.Closed || time.Since(card.Active) < 7*24*time.Hour {
			keep = append(keep, card)
		}
	}
	state.Cards = keep
	if err := os.MkdirAll(filepath.Dir(teamProgressPath(team)), 0o700); err != nil {
		return err
	}
	data, _ := json.MarshalIndent(state, "", "  ")
	return writeFileAtomic(teamProgressPath(team), append(data, '\n'), 0o600)
}

// teamProgressRoot is the task a task's work was agreed for: the first
// one up its line (what it continues, or reviews) with a yes in a Slack
// thread; "" for work agreed in a DM, or not agreed at all.
func teamProgressRoot(byID map[string]teamTask, task teamTask) (teamTask, bool) {
	seen := map[string]bool{}
	for !seen[task.ID] {
		seen[task.ID] = true
		if task.Approval != nil || task.Small {
			return task, teamProgressThread(task) != ""
		}
		up := firstNonEmpty(task.Review, task.On)
		next, ok := byID[up]
		if !ok {
			return teamTask{}, false
		}
		task = next
	}
	return teamTask{}, false
}

// teamProgressThread is the thread a root's card goes in: the one its
// yes was given in, or a small fix's.
func teamProgressThread(root teamTask) string {
	if root.Approval != nil {
		return teamApprovalSlackThread(*root.Approval)
	}
	return root.Thread
}

// followProgress keeps the progress cards current, and wakes the lead
// for work no wait told it the end of (team_handoff.go), until done.
func (b *teamBroker) followProgress(done <-chan struct{}, stderr io.Writer) {
	readers := map[string]*agentProgressReader{}
	for {
		if err := b.updateProgress(readers, time.Now()); err != nil {
			fmt.Fprintf(stderr, "hi: the progress cards in Slack: %v\n", err)
		}
		if err := b.checkHandoffs(time.Now()); err != nil {
			fmt.Fprintf(stderr, "hi: %v\n", err)
		}
		select {
		case <-done:
			return
		case <-time.After(teamProgressEvery):
		}
	}
}

// updateProgress starts a card for work that runs and has none, brings
// each open card up to date, and ends those whose work has settled.
func (b *teamBroker) updateProgress(readers map[string]*agentProgressReader, now time.Time) error {
	config, err := loadTeam(b.team)
	if err != nil {
		return err
	}
	if !config.Progress {
		return nil // a card it was keeping stays as it was, until it is on again
	}
	tasks := loadTeamTasks(b.team)
	byID := map[string]teamTask{}
	for _, task := range tasks {
		byID[task.ID] = task
	}
	// Each task belongs to the work its root was agreed for.
	groups := map[string][]teamTask{}
	for _, task := range tasks {
		if root, ok := teamProgressRoot(byID, task); ok {
			groups[root.ID] = append(groups[root.ID], task)
		}
	}
	state := loadTeamProgress(b.team)
	shown := map[string]bool{} // tasks an earlier card of the same work showed
	open := map[string]int{}   // root -> its open card
	for i, card := range state.Cards {
		if card.Closed {
			for _, id := range card.Tasks {
				shown[id] = true
			}
		} else {
			open[card.Root] = i
		}
	}
	var failed []string
	for root, group := range groups {
		var fresh []teamTask
		active := false
		for _, task := range group {
			if !shown[task.ID] {
				fresh = append(fresh, task)
				active = active || teamTaskActive(task)
			}
		}
		i, has := open[root]
		if !has {
			removed := false
			for _, card := range state.Cards {
				removed = removed || card.Root == root && card.Removed
			}
			if !active || removed {
				continue
			}
			card := teamProgressCard{Root: root, Thread: teamProgressThread(byID[root]), Active: now}
			for _, task := range fresh {
				card.Tasks = append(card.Tasks, task.ID)
			}
			text, blocks := teamProgressBlocks(card, byID, readers, now)
			ts, err := b.channel.postBlocks(config.Channel, card.Thread, text, blocks)
			if err != nil {
				failed = append(failed, fmt.Sprintf("posting the card for %s: %v", root, err))
				continue
			}
			card.TS, card.Shown = ts, teamProgressShown(blocks)
			state.Cards = append(state.Cards, card)
			continue
		}
		card := &state.Cards[i]
		for _, task := range fresh {
			if !containsString(card.Tasks, task.ID) {
				card.Tasks = append(card.Tasks, task.ID)
			}
		}
		if active {
			card.Active = now
		}
		text, blocks := teamProgressBlocks(*card, byID, readers, now)
		if active && b.progressBuried(*card) {
			// Someone wrote below it: it moves to the end of the thread,
			// posted again there, the old one deleted.
			ts, err := b.channel.postBlocks(config.Channel, card.Thread, text, blocks)
			if err != nil {
				failed = append(failed, fmt.Sprintf("moving the card for %s down: %v", root, err))
				continue
			}
			if err := b.channel.deleteMessage(config.Channel, card.TS); err != nil && !teamSlackCode(err, "message_not_found") {
				failed = append(failed, fmt.Sprintf("deleting the card for %s that moved down: %v", root, err))
			}
			card.TS, card.Shown = ts, teamProgressShown(blocks)
			continue
		}
		if shown := teamProgressShown(blocks); shown != card.Shown {
			if err := b.channel.updateBlocks(config.Channel, card.TS, text, blocks); err != nil {
				if teamSlackCode(err, "message_not_found") {
					card.Closed, card.Removed = true, true
					continue
				}
				failed = append(failed, fmt.Sprintf("updating the card for %s: %v", root, err))
				continue
			}
			card.Shown = shown
		}
		// It ends once nothing has run for a while: the broker starts the
		// next review or fix in that time.
		card.Closed = !active && now.Sub(card.Active) >= teamProgressSettle
	}
	if err := saveTeamProgress(b.team, state); err != nil {
		return err
	}
	if len(failed) > 0 {
		return errors.New(strings.Join(failed, "; "))
	}
	return nil
}

// teamProgressBlocks is a card as it is now: a plan block, its title
// what happens now and a task per task, and the message's text.
func teamProgressBlocks(card teamProgressCard, byID map[string]teamTask, readers map[string]*agentProgressReader, now time.Time) (string, []map[string]any) {
	var running *teamTask
	var last teamTask
	tasks := []map[string]any{}
	for _, id := range card.Tasks {
		task, ok := byID[id]
		if !ok {
			continue
		}
		last = task
		line := map[string]any{"task_id": task.ID, "title": teamProgressClip(teamProgressTaskTitle(task))}
		var details string
		switch {
		case teamTaskActive(task):
			running = &task
			line["status"], details = "in_progress", teamProgressRunning(task, readers, now)
		case task.Status == "done" || task.Status == "merged":
			line["status"], details = "complete", teamProgressEnded(task)
			delete(readers, task.ID)
		default: // failed, stopped, discarded
			line["status"], details = "error", teamProgressEnded(task)
			delete(readers, task.ID)
		}
		line["details"] = map[string]any{"type": "rich_text", "elements": []map[string]any{{"type": "rich_text_section",
			"elements": []map[string]any{{"type": "text", "text": teamProgressClip(details)}}}}}
		tasks = append(tasks, line)
	}
	var headline string
	switch {
	case running != nil && running.Kind == "review":
		headline = "the reviewer is checking the work"
	case running != nil:
		headline = "the " + running.Role + " is working on it"
	case last.Status == "merged":
		headline = "merged into dev"
	case last.Kind == "review" && last.Verdict == "approve":
		headline = "approved by the reviewer; the lead merges it"
	case last.Kind == "review" && last.Status == "done":
		headline = "the reviewer asked for changes"
	case last.Status == "failed" || last.Status == "stopped":
		headline = last.ID + " " + last.Status + "; the lead decides what's next"
	default:
		headline = "done; the lead looks at it"
	}
	title := teamProgressClip(byID[card.Root].Title + " · " + headline)
	return title, []map[string]any{{"type": "plan", "title": title, "tasks": tasks}}
}

// progressBuried says whether a message came after a card in its thread,
// by the channel's log.
func (b *teamBroker) progressBuried(card teamProgressCard) bool {
	messages, _ := readTeamChannel(teamChannelDir(b.team), card.TS)
	for _, message := range messages {
		if message.Thread == card.Thread && teamTSBefore(card.TS, message.TS) {
			return true
		}
	}
	return false
}

// teamProgressShown is what a card shows, to compare.
func teamProgressShown(blocks []map[string]any) string {
	data, _ := json.Marshal(blocks)
	return string(data)
}

func teamProgressTaskTitle(task teamTask) string {
	return fmt.Sprintf("%s (%s): %s", task.Role, teamAgentLabel(task.Agent), task.Title)
}

// teamProgressRunning is how long a task has run, and its steps so far.
func teamProgressRunning(task teamTask, readers map[string]*agentProgressReader, now time.Time) string {
	text := "running for " + teamProgressMinutes(now.Sub(task.Created))
	reader := readers[task.ID]
	if reader == nil && task.Box != "" {
		reader = newAgentProgressReader(task.Agent, boxStateFile(task.Box, "home"))
		readers[task.ID] = reader
	}
	if reader != nil {
		if progress := reader.poll(); progress.steps > 0 {
			text += fmt.Sprintf(" · %d steps", progress.steps)
			if step := strings.Join(strings.Fields(progress.step), " "); step != "" {
				// What it does now, such as "Bash: bun test".
				if runes := []rune(step); len(runes) > 100 {
					step = string(runes[:99]) + "…"
				}
				text += " · now: " + step
			}
		}
	}
	return text
}

// teamProgressEnded is what a task's line says once it ended.
func teamProgressEnded(task teamTask) string {
	took := ""
	if !task.Finished.IsZero() && !task.Created.IsZero() {
		took = " in " + teamProgressMinutes(task.Finished.Sub(task.Created))
	}
	switch {
	case task.Kind == "review" && task.Verdict == "approve":
		return "approved" + took
	case task.Kind == "review" && task.Verdict == "changes":
		return "asked for changes" + took
	case task.Status == "done":
		return "done" + took
	case task.Status == "merged":
		return "merged"
	case task.Error != "":
		return task.Status + ": " + task.Error
	}
	return task.Status
}

func teamProgressMinutes(d time.Duration) string {
	if d < time.Minute {
		return "<1m"
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
}

// teamProgressClip keeps a title or details within Slack's 256 characters.
func teamProgressClip(text string) string {
	if runes := []rune(text); len(runes) > 256 {
		return string(runes[:255]) + "…"
	}
	return text
}

func teamAgentLabel(agent string) string {
	switch agent {
	case "claude":
		return "Claude Code"
	case "codex":
		return "Codex"
	case "hermes":
		return "Hermes"
	}
	return agent
}

// teamProgressCommand is hi team progress <team> [on|off].
func teamProgressCommand(args []string, stdout io.Writer) error {
	if len(args) != 1 && (len(args) != 2 || args[1] != "on" && args[1] != "off") {
		return usageError{"usage: hi team progress <name> [on|off]"}
	}
	config, err := loadTeam(args[0])
	if err != nil {
		return err
	}
	if len(args) == 2 {
		config.Progress = args[1] == "on"
		if err := saveTeam(config); err != nil {
			return err
		}
	}
	if config.Progress {
		fmt.Fprintf(stdout, "Team %s keeps a progress card in the thread its work was agreed in, while the work runs.\n", config.Name)
	} else {
		fmt.Fprintf(stdout, "Team %s keeps no progress card; hi team progress %s on adds one.\n", config.Name, config.Name)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Slack's Web API with blocks the Slack library hi uses doesn't know.

// teamSlackError is an error Slack answered with, by its code.
type teamSlackError struct{ code string }

func (e teamSlackError) Error() string { return e.code }

// teamSlackCode says whether Slack answered err with code.
func teamSlackCode(err error, code string) bool {
	var slackErr teamSlackError
	return errors.As(err, &slackErr) && slackErr.code == code
}

// call posts a Web API method as JSON with the bot's token.
func (l *liveTeamChannel) call(method string, body map[string]any) (map[string]any, error) {
	data, _ := json.Marshal(body)
	request, err := http.NewRequest("POST", "https://slack.com/api/"+method, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+l.token)
	request.Header.Set("Content-Type", "application/json; charset=utf-8")
	client := &http.Client{Timeout: 30 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(response.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("%s: %w", method, err)
	}
	if ok, _ := out["ok"].(bool); !ok {
		code, _ := out["error"].(string)
		return out, teamSlackError{firstNonEmpty(code, "not ok")}
	}
	return out, nil
}

func (l *liveTeamChannel) postBlocks(channel, thread, text string, blocks []map[string]any) (string, error) {
	out, err := l.call("chat.postMessage", map[string]any{"channel": channel, "thread_ts": thread, "text": text, "blocks": blocks, "unfurl_links": false})
	if err != nil {
		return "", err
	}
	ts, _ := out["ts"].(string)
	return ts, nil
}

func (l *liveTeamChannel) deleteMessage(channel, ts string) error {
	_, err := l.call("chat.delete", map[string]any{"channel": channel, "ts": ts})
	return err
}

func (l *liveTeamChannel) updateBlocks(channel, ts, text string, blocks []map[string]any) error {
	_, err := l.call("chat.update", map[string]any{"channel": channel, "ts": ts, "text": text, "blocks": blocks})
	return err
}
