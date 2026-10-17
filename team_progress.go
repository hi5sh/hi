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
// is happening now. Slack shows it as a live message with a spinner
// (chat.startStream in plan mode), so the people see the members work
// while the lead waits for them.
//
// What Slack does with a card, found by trying it: a task_update with an
// id it has seen replaces that task's title and status but adds its
// details to what it had, so details go once, when the task ends; and
// stopping a card marks tasks still in progress as failed, so a card stops
// only when none is running.

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
	Ended   []string  `json:"ended,omitempty"`   // tasks whose details it sent
	Active  time.Time `json:"active"`            // when a task last ran
	Closed  bool      `json:"closed,omitempty"`  // stopped, by hi or by someone in Slack
	Stopped bool      `json:"stopped,omitempty"` // someone stopped it in Slack: no new card for this work
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

// followProgress keeps the progress cards current until done.
func (b *teamBroker) followProgress(done <-chan struct{}, stderr io.Writer) {
	readers := map[string]*agentProgressReader{}
	for {
		if err := b.updateProgress(readers, time.Now()); err != nil {
			fmt.Fprintf(stderr, "hi: the progress cards in Slack: %v\n", err)
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
			stopped := false
			for _, card := range state.Cards {
				stopped = stopped || card.Root == root && card.Stopped
			}
			if !active || stopped {
				continue
			}
			card := teamProgressCard{Root: root, Thread: teamProgressThread(byID[root]), Active: now}
			for _, task := range fresh {
				card.Tasks = append(card.Tasks, task.ID)
			}
			chunks := teamProgressChunks(&card, byID, fresh, readers, now)
			ts, err := b.channel.startStream(config.Channel, card.Thread, b.progressRecipient(config, byID[root]), chunks)
			if err != nil {
				failed = append(failed, fmt.Sprintf("starting the card for %s: %v", root, err))
				continue
			}
			card.TS = ts
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
		chunks := teamProgressChunks(card, byID, fresh, readers, now)
		settled := !active && now.Sub(card.Active) >= teamProgressSettle
		if err := b.channel.appendStream(config.Channel, card.TS, chunks); err != nil {
			var slackErr teamSlackError
			if errors.As(err, &slackErr) && (slackErr.code == "stopped_by_user" || slackErr.code == "message_not_in_streaming_state") {
				// Someone stopped it, or Slack ended it: a card that
				// Slack ended gets a successor while the work runs.
				card.Closed, card.Stopped = true, slackErr.code == "stopped_by_user"
				continue
			}
			failed = append(failed, fmt.Sprintf("updating the card for %s: %v", root, err))
			continue
		}
		if settled {
			if err := b.channel.stopStream(config.Channel, card.TS); err != nil {
				failed = append(failed, fmt.Sprintf("ending the card for %s: %v", root, err))
			}
			card.Closed = true
		}
	}
	if err := saveTeamProgress(b.team, state); err != nil {
		return err
	}
	if len(failed) > 0 {
		return errors.New(strings.Join(failed, "; "))
	}
	return nil
}

// progressRecipient is who agreed the work, from the channel's log;
// Slack wants a person for a card in a channel. The first owner otherwise.
func (b *teamBroker) progressRecipient(config teamConfig, root teamTask) string {
	if root.Approval != nil && teamSlackTSPattern.MatchString(root.Approval.TS) {
		ts := root.Approval.TS
		messages, _ := readTeamChannel(teamChannelDir(b.team), teamSlackTS(teamTSTime(ts).Add(-time.Second)))
		for _, message := range messages {
			if message.TS == ts && message.User != "" {
				return message.User
			}
		}
	}
	if len(config.Owners) > 0 {
		return config.Owners[0]
	}
	return ""
}

// teamProgressChunks is a card's plan as it is now: its title, then a
// task_update per task, with the details of a task that ended the first
// time the card shows it ended.
func teamProgressChunks(card *teamProgressCard, byID map[string]teamTask, fresh []teamTask, readers map[string]*agentProgressReader, now time.Time) []map[string]any {
	root := byID[card.Root]
	var running *teamTask
	var last teamTask
	chunks := []map[string]any{nil} // the title goes first, once it's known
	for _, id := range card.Tasks {
		task, ok := byID[id]
		if !ok {
			continue
		}
		last = task
		title := teamProgressTaskTitle(task)
		chunk := map[string]any{"type": "task_update", "id": task.ID}
		switch {
		case teamTaskActive(task):
			running = &task
			chunk["status"] = "in_progress"
			title += " · " + teamProgressRunning(task, readers, now)
		case task.Status == "done" || task.Status == "merged":
			chunk["status"] = "complete"
			delete(readers, task.ID)
		default: // failed, stopped, discarded
			chunk["status"] = "error"
			delete(readers, task.ID)
		}
		chunk["title"] = teamProgressClip(title)
		if !teamTaskActive(task) && !containsString(card.Ended, task.ID) {
			chunk["details"] = teamProgressClip(teamProgressEnded(task))
			card.Ended = append(card.Ended, task.ID)
		}
		chunks = append(chunks, chunk)
	}
	headline := ""
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
	chunks[0] = map[string]any{"type": "plan_update", "title": teamProgressClip(root.Title + " · " + headline)}
	return chunks
}

func teamProgressTaskTitle(task teamTask) string {
	return fmt.Sprintf("%s (%s): %s", task.Role, teamAgentLabel(task.Agent), task.Title)
}

// teamProgressRunning is how long a task has run, and its steps so far.
func teamProgressRunning(task teamTask, readers map[string]*agentProgressReader, now time.Time) string {
	text := "running " + teamProgressMinutes(now.Sub(task.Created))
	reader := readers[task.ID]
	if reader == nil && task.Box != "" {
		reader = newAgentProgressReader(task.Agent, boxStateFile(task.Box, "home"))
		readers[task.ID] = reader
	}
	if reader != nil {
		if progress := reader.poll(); progress.steps > 0 {
			text += fmt.Sprintf(" · %d steps", progress.steps)
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
// Slack's streaming methods, which the Slack library hi uses doesn't have.

// teamSlackError is an error Slack answered with, by its code.
type teamSlackError struct{ code string }

func (e teamSlackError) Error() string { return e.code }

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

func (l *liveTeamChannel) startStream(channel, thread, user string, chunks []map[string]any) (string, error) {
	if l.teamID == "" {
		out, err := l.call("auth.test", map[string]any{})
		if err != nil {
			return "", err
		}
		l.teamID, _ = out["team_id"].(string)
	}
	out, err := l.call("chat.startStream", map[string]any{"channel": channel, "thread_ts": thread, "recipient_user_id": user,
		"recipient_team_id": l.teamID, "task_display_mode": "plan", "chunks": chunks})
	if err != nil {
		return "", err
	}
	ts, _ := out["ts"].(string)
	return ts, nil
}

func (l *liveTeamChannel) appendStream(channel, ts string, chunks []map[string]any) error {
	_, err := l.call("chat.appendStream", map[string]any{"channel": channel, "ts": ts, "chunks": chunks})
	return err
}

func (l *liveTeamChannel) stopStream(channel, ts string) error {
	_, err := l.call("chat.stopStream", map[string]any{"channel": channel, "ts": ts})
	return err
}
