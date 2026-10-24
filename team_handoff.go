package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// The lead waits for a task with hi team wait, in the background. When the
// team restarts (hi update --restart, a reboot), that wait ends without
// its answer, and the lead no longer knows the work it started has
// finished: it tells the people the coder is still at it while nobody is.
// So the broker notes whether a wait (or hi team show) handed the lead how
// each piece of work ended; when work settles and no wait did, hi wakes
// the lead once, with a job in its Hermes whose answer goes to the thread
// the work was agreed in.

const (
	// teamHandoffGrace is how long a wait has to hand the lead a result
	// before hi wakes the lead.
	teamHandoffGrace = time.Minute
	// teamHandoffMaxAge is the oldest result hi wakes the lead for.
	teamHandoffMaxAge = 12 * time.Hour
)

// teamHandoff is run/handoff.json: the tasks whose end the lead has
// heard of, from a wait or from hi.
type teamHandoff struct {
	Heard []string `json:"heard"`
}

func teamHandoffPath(team string) string { return filepath.Join(teamDir(team), "run", "handoff.json") }

func loadTeamHandoff(team string) (teamHandoff, bool) {
	var handoff teamHandoff
	data, err := os.ReadFile(teamHandoffPath(team))
	if err != nil {
		return handoff, false
	}
	json.Unmarshal(data, &handoff)
	return handoff, true
}

func saveTeamHandoff(team string, handoff teamHandoff) error {
	if len(handoff.Heard) > 500 {
		handoff.Heard = handoff.Heard[len(handoff.Heard)-500:]
	}
	if err := os.MkdirAll(filepath.Dir(teamHandoffPath(team)), 0o700); err != nil {
		return err
	}
	data, _ := json.MarshalIndent(handoff, "", "  ")
	return writeFileAtomic(teamHandoffPath(team), append(data, '\n'), 0o600)
}

// heard notes that the lead read how a task ended.
func (b *teamBroker) heard(id string) {
	b.handoffMu.Lock()
	defer b.handoffMu.Unlock()
	handoff, _ := loadTeamHandoff(b.team)
	if !containsString(handoff.Heard, id) {
		handoff.Heard = append(handoff.Heard, id)
		saveTeamHandoff(b.team, handoff)
	}
}

// waitingOn counts a wait for the steps from a task, while it waits.
func (b *teamBroker) waitingOn(id string) func() {
	b.handoffMu.Lock()
	b.waits[id]++
	b.handoffMu.Unlock()
	return func() {
		b.handoffMu.Lock()
		b.waits[id]--
		b.handoffMu.Unlock()
	}
}

// teamWorkGroups is the team's tasks by the work they belong to: the task
// it was agreed for, with a Slack thread (teamProgressRoot), in order.
func teamWorkGroups(tasks []teamTask) (map[string][]teamTask, map[string]teamTask) {
	byID := map[string]teamTask{}
	for _, task := range tasks {
		byID[task.ID] = task
	}
	groups := map[string][]teamTask{}
	for _, task := range tasks {
		if root, ok := teamProgressRoot(byID, task); ok {
			groups[root.ID] = append(groups[root.ID], task)
		}
	}
	return groups, byID
}

// checkHandoffs wakes the lead for work that settled while no wait was
// there to tell it. The first time, it takes what has settled as heard.
func (b *teamBroker) checkHandoffs(now time.Time) error {
	if b.wakeLead == nil {
		return nil
	}
	config, err := loadTeam(b.team)
	if err != nil {
		return err
	}
	groups, byID := teamWorkGroups(loadTeamTasks(b.team))
	b.handoffMu.Lock()
	defer b.handoffMu.Unlock()
	handoff, seen := loadTeamHandoff(b.team)
	var wake []teamTask
	for _, group := range groups {
		last, work := group[len(group)-1], teamTask{}
		for _, task := range group {
			if task.Kind == "task" {
				work = task
			}
		}
		waiting := false
		for _, task := range group {
			waiting = waiting || b.waits[task.ID] > 0
		}
		switch {
		case teamTaskActive(last), work.Status == "merged", work.Status == "discarded", last.Status == "discarded":
			continue // still running, or the lead dealt with it
		case containsString(handoff.Heard, last.ID):
			continue
		case !seen:
			handoff.Heard = append(handoff.Heard, last.ID) // from before hi kept track
			continue
		case waiting, last.Finished.IsZero(), now.Sub(last.Finished) < teamHandoffGrace, now.Sub(last.Finished) > teamHandoffMaxAge:
			continue
		}
		wake = append(wake, last)
	}
	var failed []string
	for _, last := range wake {
		root, _ := teamProgressRoot(byID, last)
		deliver := "slack:" + config.Channel + ":" + teamProgressThread(root)
		if err := b.wakeLead("hi-handoff-"+last.ID, teamHandoffPrompt(root, last), deliver); err != nil {
			failed = append(failed, fmt.Sprintf("telling the lead that %s ended: %v", last.ID, err))
			continue
		}
		handoff.Heard = append(handoff.Heard, last.ID)
	}
	if err := saveTeamHandoff(b.team, handoff); err != nil {
		return err
	}
	if len(failed) > 0 {
		return fmt.Errorf("%s", strings.Join(failed, "; "))
	}
	return nil
}

// teamHandoffPrompt is what wakes the lead: what ended, and what to do.
func teamHandoffPrompt(root, last teamTask) string {
	ended := teamProgressEnded(last)
	if last.Kind == "review" && last.Round > 0 {
		ended = fmt.Sprintf("%s (review %d)", ended, last.Round)
	}
	note := ""
	if last.Incident != "" {
		note = "\n\n" + teamTaskIncidentNote(last)
	}
	return fmt.Sprintf(`This is hi, not a person. You started %s (%s) and were waiting for it, but your wait ended before the work did, as when the team restarts, so you haven't heard how it went. It has ended: %s, %s.

Run hi team wait %s to read how it ended, then carry on as after any wait: if the reviewer approved, check what it lists to verify on the preview, merge it, and run hi team preview; if changes are still needed, read the reviewer's notes and decide (continue with hi team task coder <brief.md> --on %s, or ask the people: after two reviews asking for changes, going on needs their yes, --approval); if it failed, find out why.

What you write at the end is posted in the thread where the work was agreed. End with a short note to the people there on where the work stands and what happens next. If you said earlier that the work was still running, say that it had in fact finished.%s`,
		root.ID, root.Title, last.ID, ended, root.ID, root.ID, note)
}
