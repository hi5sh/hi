package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// An incident is something that went wrong while nobody was looking: an
// app's job that failed or stopped moving (team_jobs.go), production's box
// that stopped, a member's task that failed, or an alert. Each is one
// Slack thread and one record, incidents/i-<n>.json in the team's folder.
// The lead is woken once, handles what can be undone on its own, and asks
// the people about the rest. hi writes the record from the commands the
// lead runs (--incident <id> --because "<why>"), and posts each action in
// the thread, so the people see what the lead decided without asking.
//
// Spec: docs/specs/approved/hi_team_jobs.md.

const (
	// teamIncidentRemindAfter is how long a question to the people waits
	// before hi reminds the owners, once.
	teamIncidentRemindAfter = 24 * time.Hour
	// teamIncidentStandsAfter is how long a production incident hi closes
	// on its own (a job, the app, the workers) may stay open, with nobody
	// asked, before hi tells the owners, once: the lead may have chosen to
	// wait, and waiting with production's work stopped is theirs to choose.
	teamIncidentStandsAfter = 4 * time.Hour
	// teamAlertsPerDay and teamAlertGap limit hi team alert.
	teamAlertsPerDay = 10
	teamAlertGap     = time.Minute
	// teamIncidentLogLines is how much of the app's log an incident keeps.
	teamIncidentLogLines = 30
)

type teamIncident struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"` // job, app, task, or alert
	Slot  string `json:"slot,omitempty"`
	Job   string `json:"job,omitempty"`
	Task  string `json:"task,omitempty"`
	Title string `json:"title"`
	// Detail is what hi knew when it opened: the job's file, the app's
	// last lines, the task's error.
	Detail string `json:"detail,omitempty"`
	// Thread is the Slack thread it is discussed in; empty when hi had no
	// Slack.
	Thread string `json:"thread,omitempty"`
	// Channel is where Thread is; empty is the main channel, where every
	// incident was before the ops channel (team_ops.go).
	Channel string    `json:"channel,omitempty"`
	Opened  time.Time `json:"opened"`
	// Status is open, waiting (the lead asked the people), or closed.
	Status   string              `json:"status"`
	Asked    time.Time           `json:"asked,omitzero"`
	Reminded time.Time           `json:"reminded,omitzero"`
	Closed   time.Time           `json:"closed,omitzero"`
	Events   []teamIncidentEvent `json:"events"`
}

// channelIn is the channel the incident's thread is in.
func (i teamIncident) channelIn(config teamConfig) string {
	return firstNonEmpty(i.Channel, config.Channel)
}

// teamIncidentEvent is one line of an incident's timeline.
type teamIncidentEvent struct {
	At time.Time `json:"at"`
	// Who is hi, lead, or a person's name.
	Who string `json:"who"`
	// Action is opened, changed, restart, task, note, ask, rule, close,
	// recovered, reminder, or forget.
	Action   string        `json:"action"`
	Command  string        `json:"command,omitempty"`
	Because  string        `json:"because,omitempty"`
	Result   string        `json:"result,omitempty"`
	Approval *teamApproval `json:"approval,omitempty"`
}

// teamJobRule is a decision the people made about a job, kept so the lead
// handles the case alone next time: incidents/rules.json.
type teamJobRule struct {
	Job      string       `json:"job"`
	Rule     string       `json:"rule"`
	Approval teamApproval `json:"approval"`
	Incident string       `json:"incident,omitempty"`
	At       time.Time    `json:"at"`
}

func teamIncidentsDir(team string) string { return filepath.Join(teamDir(team), "incidents") }

func teamIncidentPath(team, id string) string {
	return filepath.Join(teamIncidentsDir(team), id+".json")
}

func teamIncidentNumber(id string) int {
	n, _ := strconv.Atoi(strings.TrimPrefix(id, "i-"))
	return n
}

func loadTeamIncident(team, id string) (teamIncident, error) {
	id = strings.TrimSpace(id)
	if !strings.HasPrefix(id, "i-") || teamIncidentNumber(id) <= 0 {
		return teamIncident{}, fmt.Errorf("%q isn't an incident; they are named like i-3", id)
	}
	data, err := os.ReadFile(teamIncidentPath(team, id))
	if errors.Is(err, os.ErrNotExist) {
		return teamIncident{}, fmt.Errorf("no incident %s; hi team incidents lists them", id)
	}
	if err != nil {
		return teamIncident{}, err
	}
	var incident teamIncident
	if err := json.Unmarshal(data, &incident); err != nil {
		return teamIncident{}, fmt.Errorf("incident %s: %w", id, err)
	}
	return incident, nil
}

func saveTeamIncident(team string, incident teamIncident) error {
	if err := os.MkdirAll(teamIncidentsDir(team), 0o700); err != nil {
		return err
	}
	data, _ := json.MarshalIndent(incident, "", "  ")
	return writeFileAtomic(teamIncidentPath(team, incident.ID), append(data, '\n'), 0o600)
}

// loadTeamIncidents is every incident, oldest first.
func loadTeamIncidents(team string) []teamIncident {
	entries, _ := os.ReadDir(teamIncidentsDir(team))
	var incidents []teamIncident
	for _, entry := range entries {
		id, ok := strings.CutSuffix(entry.Name(), ".json")
		if !ok || !strings.HasPrefix(id, "i-") {
			continue
		}
		if incident, err := loadTeamIncident(team, id); err == nil {
			incidents = append(incidents, incident)
		}
	}
	sort.Slice(incidents, func(i, j int) bool { return teamIncidentNumber(incidents[i].ID) < teamIncidentNumber(incidents[j].ID) })
	return incidents
}

func teamRulesPath(team string) string { return filepath.Join(teamIncidentsDir(team), "rules.json") }

func loadTeamRules(team string) []teamJobRule {
	var rules []teamJobRule
	if data, err := os.ReadFile(teamRulesPath(team)); err == nil {
		json.Unmarshal(data, &rules)
	}
	return rules
}

func teamRulesFor(rules []teamJobRule, job string) []teamJobRule {
	var out []teamJobRule
	for _, rule := range rules {
		if rule.Job == job {
			out = append(out, rule)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// opening, logging, closing

// openIncident numbers an incident, posts its alert (in thread, or as a
// new message in the channel when thread is empty), saves it, and wakes
// the lead with prompt when there is one.
func (b *teamBroker) openIncident(incident teamIncident, alert, thread string, prompt func(teamConfig, teamIncident) string) (teamIncident, error) {
	config, err := loadTeam(b.team)
	if err != nil {
		return teamIncident{}, err
	}
	b.incidentMu.Lock()
	last := 0
	for _, old := range loadTeamIncidents(b.team) {
		last = max(last, teamIncidentNumber(old.ID))
	}
	incident.ID = fmt.Sprintf("i-%d", last+1)
	incident.Status = "open"
	if incident.Opened.IsZero() {
		incident.Opened = time.Now().UTC()
	}
	incident.Events = append(incident.Events, teamIncidentEvent{At: incident.Opened, Who: "hi", Action: "opened", Result: incident.Title})
	err = saveTeamIncident(b.team, incident)
	b.incidentMu.Unlock()
	if err != nil {
		return teamIncident{}, err
	}
	text := fmt.Sprintf(":rotating_light: *Incident %s · %s*\n%s", incident.ID, teamIncidentWhere(incident), teamSlackEscape(alert))
	// A task's incident is told only in its work's thread; without one, the
	// lead's wait says it.
	// In a work's thread, in the main channel where that thread is; on its
	// own, in the ops channel.
	if b.channel != nil && (thread != "" || incident.Kind != "task") {
		var ts string
		channel := config.Channel
		if thread != "" {
			ts, err = b.channel.postIn(channel, thread, text)
		} else {
			channel = config.opsChannel()
			ts, err = b.channel.post(channel, text)
			thread = ts
		}
		if err == nil {
			if channel == config.Channel {
				channel = ""
			}
			b.updateIncident(incident.ID, func(i *teamIncident) { i.Thread, i.Channel = thread, channel })
			incident.Thread, incident.Channel = thread, channel
		}
	}
	if prompt != nil && b.wakeLead != nil && incident.Thread != "" {
		if err := b.wakeLead("hi-incident-"+incident.ID, prompt(config, incident), "slack:"+incident.channelIn(config)+":"+incident.Thread); err != nil {
			b.addIncidentEvent(incident.ID, teamIncidentEvent{Who: "hi", Action: "note", Result: "couldn't wake the lead: " + err.Error()})
		}
	}
	return incident, nil
}

func teamIncidentWhere(incident teamIncident) string {
	switch incident.Kind {
	case "job":
		return teamSlotName(incident.Slot) + "'s job " + incident.Job
	case "app":
		return teamSlotName(incident.Slot) + "'s app"
	case "task":
		return "task " + incident.Task
	case "workers":
		return teamSlotName(incident.Slot) + "'s workers"
	}
	return "alert"
}

// updateIncident changes a saved incident.
func (b *teamBroker) updateIncident(id string, change func(*teamIncident)) (teamIncident, error) {
	b.incidentMu.Lock()
	defer b.incidentMu.Unlock()
	incident, err := loadTeamIncident(b.team, id)
	if err != nil {
		return incident, err
	}
	change(&incident)
	return incident, saveTeamIncident(b.team, incident)
}

func (b *teamBroker) addIncidentEvent(id string, event teamIncidentEvent) {
	if event.At.IsZero() {
		event.At = time.Now().UTC()
	}
	b.updateIncident(id, func(incident *teamIncident) { incident.Events = append(incident.Events, event) })
}

func (b *teamBroker) incidentOpen(id string) bool {
	incident, err := loadTeamIncident(b.team, id)
	return err == nil && incident.Status != "closed"
}

// logIncidentAction records something the lead did and says it in the
// incident's thread: what, and why.
func (b *teamBroker) logIncidentAction(incident teamIncident, event teamIncidentEvent) {
	b.addIncidentEvent(incident.ID, event)
	if b.channel == nil || incident.Thread == "" {
		return
	}
	config, err := loadTeam(b.team)
	if err != nil {
		return
	}
	text := fmt.Sprintf(":gear: *%s, %s:* %s", teamCapitalize(teamIncidentWho(event.Who)), incident.ID, teamSlackEscape(teamIncidentActionText(event)))
	b.channel.postIn(incident.channelIn(config), incident.Thread, text)
}

func teamIncidentWho(who string) string {
	if who == "lead" {
		return "the lead"
	}
	return who
}

func teamIncidentActionText(event teamIncidentEvent) string {
	var text strings.Builder
	switch {
	case event.Command != "":
		text.WriteString("`" + event.Command + "`")
	case event.Result != "":
		text.WriteString(event.Result)
	default:
		text.WriteString(event.Action)
	}
	if event.Command != "" && event.Result != "" {
		text.WriteString(": " + event.Result)
	}
	text.WriteString(".")
	if event.Because != "" {
		text.WriteString(" Why: " + strings.TrimSuffix(event.Because, ".") + ".")
	}
	if event.Approval != nil {
		text.WriteString(" " + event.Approval.Name + " said yes.")
	}
	return text.String()
}

// recovered closes an incident whose cause went away, and says so.
func (b *teamBroker) recovered(id, text string) {
	incident, err := b.updateIncident(id, func(incident *teamIncident) {
		now := time.Now().UTC()
		incident.Events = append(incident.Events, teamIncidentEvent{At: now, Who: "hi", Action: "recovered", Result: text})
		incident.Status, incident.Closed = "closed", now
	})
	if err != nil || b.channel == nil || incident.Thread == "" {
		return
	}
	if config, err := loadTeam(b.team); err == nil {
		b.channel.postIn(incident.channelIn(config), incident.Thread, fmt.Sprintf(":white_check_mark: %s Incident %s is closed.", teamSlackEscape(text), incident.ID))
	}
}

// leadIncident is an open incident the lead names with what it does.
func (b *teamBroker) leadIncident(id, because string) (teamIncident, error) {
	if strings.TrimSpace(id) == "" {
		return teamIncident{}, errors.New("name the incident this is for: --incident i-3")
	}
	if strings.TrimSpace(because) == "" {
		return teamIncident{}, errors.New("say why, in a sentence the people can read: --because \"<why>\"")
	}
	incident, err := loadTeamIncident(b.team, id)
	if err != nil {
		return incident, err
	}
	if incident.Status == "closed" {
		return incident, fmt.Errorf("incident %s is closed; hi team incidents lists the open ones", incident.ID)
	}
	return incident, nil
}

// ---------------------------------------------------------------------------
// what opens one

func (b *teamBroker) openJobIncident(slot string, job teamJob, condition string, now time.Time) (teamIncident, error) {
	title := teamJobConditionText(job, condition)
	data, _ := json.MarshalIndent(job, "", "  ")
	detail := "The job's file:\n" + string(data)
	if state, ok := loadTeamSlot(b.team, slot); ok && b.appLogs != nil {
		if logs := strings.TrimSpace(b.appLogs(state.Container)); logs != "" {
			detail += "\n\nThe app's last lines:\n" + logs
		}
	}
	alert := title + "."
	if !b.started.IsZero() && now.Sub(b.started) < 2*teamJobSettle {
		restarted := fmt.Sprintf("hi started the team at %s UTC, which cuts the app's model calls and restarts its boxes; that may be the cause, though the job hasn't moved again since.", b.started.UTC().Format("15:04"))
		alert += "\n" + restarted
		detail += "\n\n" + restarted
	}
	incident := teamIncident{Kind: "job", Slot: slot, Job: job.Job, Title: title, Detail: detail, Opened: now.UTC()}
	if slot == teamSlotPreview {
		// A preview is an owner's work in progress: hi team jobs --preview,
		// and the dev page, show it.
		return incident, nil
	}
	return b.openIncident(incident, alert+"\nThe lead looks into it here.", "", teamIncidentPrompt)
}

// appStopped opens an incident for production's box, which stopped on its
// own while the team ran, before hi starts it again. A stop from before
// the team started (a reboot, hi team down) isn't one.
func (b *teamBroker) appStopped(state teamPreview) {
	if b.appExit == nil || b.stopped() {
		return
	}
	exit, finished, ok := b.appExit(state.Container)
	if !ok || finished.Before(b.started) {
		return
	}
	b.jobsMu.Lock()
	jobs := loadTeamJobsState(b.team)
	key := state.Container + "@" + finished.UTC().Format(time.RFC3339Nano)
	seen := jobs.AppStopped == key
	jobs.AppStopped = key
	saveTeamJobsState(b.team, jobs)
	b.jobsMu.Unlock()
	if seen {
		return
	}
	title := fmt.Sprintf("Production's app stopped at %s with exit code %d", finished.UTC().Format("15:04 UTC"), exit)
	if exit == 137 {
		title += " (killed, often for memory)"
	}
	var running []string
	for _, job := range readTeamJobs(b.team, teamSlotProduction) {
		if job.State == "running" {
			running = append(running, job.Job)
		}
	}
	if len(running) > 0 {
		title += "; it was running " + strings.Join(running, ", ")
	}
	detail := ""
	if b.appLogs != nil {
		if logs := strings.TrimSpace(b.appLogs(state.Container)); logs != "" {
			detail = "The app's last lines:\n" + logs
		}
	}
	incident := teamIncident{Kind: "app", Slot: teamSlotProduction, Title: title, Detail: detail}
	b.openIncident(incident, title+".\nhi starts it again in a new box; the lead looks into why.", "", teamIncidentPrompt)
}

// alert is hi team alert: something the lead or a member noticed.
func (b *teamBroker) alert(text, who string) (teamIncident, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return teamIncident{}, errors.New(`say what's wrong: hi team alert "<what you noticed>"`)
	}
	if len(text) > 2000 {
		text = teamClipText(text, 2000)
	}
	now := time.Now()
	today, last := 0, time.Time{}
	for _, incident := range loadTeamIncidents(b.team) {
		if incident.Kind != "alert" {
			continue
		}
		if now.Sub(incident.Opened) < 24*time.Hour {
			today++
		}
		if incident.Opened.After(last) {
			last = incident.Opened
		}
	}
	if now.Sub(last) < teamAlertGap {
		return teamIncident{}, errors.New("an alert went out less than a minute ago; add to its thread instead")
	}
	if today >= teamAlertsPerDay {
		return teamIncident{}, fmt.Errorf("%d alerts went out in the last day, the most hi sends; say it in the channel instead", teamAlertsPerDay)
	}
	incident := teamIncident{Kind: "alert", Title: teamClipText(text, 300), Detail: who + ": " + text}
	return b.openIncident(incident, text+"\n(from "+who+") The lead looks into it here.", "", teamIncidentPrompt)
}

// remindIncidents reminds the owners, once, of a question the lead asked
// that nobody answered in a day, and of a production incident that has
// stood for hours with nobody asked.
func (b *teamBroker) remindIncidents(now time.Time) error {
	if b.channel == nil {
		return nil
	}
	config, err := loadTeam(b.team)
	if err != nil {
		return err
	}
	var owners []string
	for _, owner := range config.Owners {
		owners = append(owners, "<@"+owner+">")
	}
	for _, incident := range loadTeamIncidents(b.team) {
		if teamIncidentStands(incident, now) {
			b.updateIncident(incident.ID, func(i *teamIncident) {
				i.Reminded = now.UTC()
				i.Events = append(i.Events, teamIncidentEvent{At: now.UTC(), Who: "hi", Action: "reminder", Result: "told the owners it still stands"})
			})
			b.channel.postIn(incident.channelIn(config), incident.Thread, fmt.Sprintf(":hourglass: %s, %s (%s) has stood for %s, and nobody was asked. The lead's account is above: if what it waits for isn't what you want, say so here.",
				strings.Join(owners, " "), incident.ID, teamIncidentWhere(incident), teamJobDuration(now.Sub(incident.Opened).Truncate(time.Hour))))
			continue
		}
		if incident.Status != "waiting" || !incident.Reminded.IsZero() || incident.Thread == "" || now.Sub(incident.Asked) < teamIncidentRemindAfter {
			continue
		}
		messages, _ := readTeamChannel(teamChannelLogDir(b.team, config, incident.channelIn(config)), teamSlackTS(incident.Asked))
		answered := false
		for _, message := range messages {
			if message.Thread == incident.Thread && !teamChannelFromBot(config, message) {
				answered = true
			}
		}
		b.updateIncident(incident.ID, func(i *teamIncident) {
			i.Reminded = now.UTC()
			if !answered {
				i.Events = append(i.Events, teamIncidentEvent{At: now.UTC(), Who: "hi", Action: "reminder", Result: "reminded the owners"})
			}
		})
		if answered {
			continue
		}
		b.channel.postIn(incident.channelIn(config), incident.Thread, fmt.Sprintf(":hourglass: %s, the lead's question above has waited a day. Nothing happens to %s until one of you answers.",
			strings.Join(owners, " "), teamIncidentWhere(incident)))
	}
	return nil
}

// teamIncidentStands is whether an incident is one of production's that hi
// closes on its own, open for teamIncidentStandsAfter with nobody asked
// and the owners not yet told.
func teamIncidentStands(incident teamIncident, now time.Time) bool {
	switch incident.Kind {
	case "job", "app", "workers":
	default:
		return false
	}
	return incident.Status == "open" && incident.Slot == teamSlotProduction && incident.Thread != "" &&
		incident.Reminded.IsZero() && now.Sub(incident.Opened) >= teamIncidentStandsAfter
}

// ---------------------------------------------------------------------------
// what the lead does with one

type teamIncidentRequest struct {
	ID      string `json:"id,omitempty"`
	Text    string `json:"text,omitempty"`
	Because string `json:"because,omitempty"`
	Job     string `json:"job,omitempty"`
	Who     string `json:"who,omitempty"`
	// owner is an owner on the dev page, whose click is the yes; no
	// socket sets it.
	owner string
}

// incidentAction is the lead's note, question, close, or rule.
func (b *teamBroker) incidentAction(action string, request teamIncidentRequest) (teamIncident, error) {
	switch action {
	case "note":
		incident, err := b.leadIncident(request.ID, request.Text)
		if err != nil {
			return incident, err
		}
		// A note is the lead's own account, kept as such; not posted.
		b.addIncidentEvent(incident.ID, teamIncidentEvent{Who: "lead", Action: "note", Result: "(the lead's own note) " + teamClipText(request.Text, 2000)})
	case "ask":
		incident, err := b.leadIncident(request.ID, request.Text)
		if err != nil {
			return incident, err
		}
		now := time.Now().UTC()
		b.updateIncident(incident.ID, func(i *teamIncident) {
			i.Status, i.Asked, i.Reminded = "waiting", now, time.Time{}
			i.Events = append(i.Events, teamIncidentEvent{At: now, Who: "lead", Action: "ask", Result: teamClipText(request.Text, 2000)})
		})
	case "close":
		incident, err := b.leadIncident(request.ID, request.Because)
		if err != nil {
			return incident, err
		}
		now := time.Now().UTC()
		b.updateIncident(incident.ID, func(i *teamIncident) { i.Status, i.Closed = "closed", now })
		b.logIncidentAction(incident, teamIncidentEvent{At: now, Who: firstNonEmpty(request.owner, "lead"), Action: "close", Result: "closed " + incident.ID, Because: request.Because})
	default:
		return teamIncident{}, fmt.Errorf("no incident action %q", action)
	}
	return loadTeamIncident(b.team, request.ID)
}

// addRule keeps a decision the people made about a job, with the message
// it was made in, which must be one of theirs.
func (b *teamBroker) addRule(request teamIncidentRequest) (teamIncident, error) {
	if !teamJobName.MatchString(request.Job) {
		return teamIncident{}, fmt.Errorf("%q isn't a job's name; hi team jobs lists them", request.Job)
	}
	text := strings.TrimSpace(request.Text)
	if text == "" {
		return teamIncident{}, errors.New("say the rule, in the people's words")
	}
	var approval teamApproval
	if request.owner != "" {
		approval = teamApproval{Name: request.owner}
	} else {
		return teamIncident{}, errors.New("a rule for a job is the owners' decision: they add it on the dev page")
	}
	var err error
	var incident teamIncident
	if request.ID != "" {
		if incident, err = loadTeamIncident(b.team, request.ID); err != nil {
			return incident, err
		}
	}
	b.incidentMu.Lock()
	rules := append(loadTeamRules(b.team), teamJobRule{Job: request.Job, Rule: teamClipText(text, 1000), Approval: approval, Incident: incident.ID, At: time.Now().UTC()})
	if err := os.MkdirAll(teamIncidentsDir(b.team), 0o700); err != nil {
		b.incidentMu.Unlock()
		return incident, err
	}
	data, _ := json.MarshalIndent(rules, "", "  ")
	err = writeFileAtomic(teamRulesPath(b.team), append(data, '\n'), 0o600)
	b.incidentMu.Unlock()
	if err != nil {
		return incident, err
	}
	if incident.ID != "" {
		b.logIncidentAction(incident, teamIncidentEvent{Who: firstNonEmpty(request.owner, "lead"), Action: "rule", Result: fmt.Sprintf("a rule for %s: %s", request.Job, text), Approval: &approval})
		return loadTeamIncident(b.team, incident.ID)
	}
	return incident, nil
}

// removeRule takes a job's rule out: an owner on the dev page changed
// their mind.
func (b *teamBroker) removeRule(job, rule string) error {
	b.incidentMu.Lock()
	defer b.incidentMu.Unlock()
	var kept []teamJobRule
	found := false
	for _, r := range loadTeamRules(b.team) {
		if !found && r.Job == job && r.Rule == rule {
			found = true
			continue
		}
		kept = append(kept, r)
	}
	if !found {
		return fmt.Errorf("%s has no such rule", job)
	}
	data, _ := json.MarshalIndent(append([]teamJobRule{}, kept...), "", "  ")
	return writeFileAtomic(teamRulesPath(b.team), append(data, '\n'), 0o600)
}

// ---------------------------------------------------------------------------
// what wakes the lead

// teamIncidentAutonomy is how the lead handles an incident: the same in
// every prompt and in its skill.
const teamIncidentAutonomy = `Handle what you can on your own, so the people don't have to babysit the work; ask the owners about the rest. They build and release in the dev box and on the dev page; you don't change the code.

On your own, when it can be undone and changes no result nobody has ruled on:
- Wait or retry when the cause passes (a network error, a service that was down, a rate limit), and check back once it should have moved.
- Restart the app's box once per incident: hi team jobs restart [--preview] --incident <id> --because "<why>". After that, the owners restart it on the dev page.
- Apply a rule the owners gave (listed below, and in hi team incidents --rules).

Ask the owners when no rule covers a choice that changes results, when a step can't be undone (deleting or rewriting data, a rollback), when the fix is a change to the code, when your own attempts ran out, or when you aren't sure what happened. Never skip, drop, or invent an item a job couldn't process unless a rule says so: in some jobs every later value depends on the one before, and a silent gap makes them all wrong. Say what you found, what depends on it, and the choices, each with what it would do to the results, and which you'd take. Then run hi team incidents ask <id> "<your question in a line>", so hi reminds the owners if nobody answers in a day. They keep their decision as the job's rule on the dev page. When it needs a change to the code, file a task with what you found (hi team task add), and say its number here.

Waiting is a choice too. When what you'd do leaves production's work stopped beyond the incident's own cause, say how much is left and what waiting costs at the work's usual rate, give the choices (resuming it through the app's own controls now is one, and isn't a restart), and ask.

Log what you do: pass --incident <id> --because "<why>" to hi team jobs restart, and keep the rest as notes (hi team incidents note <id> "..."); hi posts each in this thread and keeps it in the incident's record. When it is resolved and hi hasn't closed it, run hi team incidents close <id> --because "<what resolved it>".`

// teamIncidentPrompt wakes the lead for an incident, with what it would
// otherwise spend its turn finding.
func teamIncidentPrompt(config teamConfig, incident teamIncident) string {
	var text strings.Builder
	fmt.Fprintf(&text, "This is hi, not a person. Incident %s is open: %s.\n\n", incident.ID, incident.Title)
	if incident.Detail != "" {
		fmt.Fprintf(&text, "%s\n\n", teamClipText(incident.Detail, 6000))
	}
	if incident.Job != "" {
		rules := teamRulesFor(loadTeamRules(config.Name), incident.Job)
		if len(rules) == 0 {
			fmt.Fprintf(&text, "The people have given no rules for %s yet.\n\n", incident.Job)
		} else {
			fmt.Fprintf(&text, "Rules the people gave for %s:\n", incident.Job)
			for _, rule := range rules {
				fmt.Fprintf(&text, "- %s (%s, %s)\n", rule.Rule, rule.Approval.Name, rule.At.Format("2006-01-02"))
			}
			text.WriteString("\n")
		}
		text.WriteString("hi team jobs shows every job's state, count, and rate. ")
	}
	fmt.Fprintf(&text, "First find out why, reading only: the logs, the code, the item, hi team incidents %s. Nothing in production changes while you look.\n\n", incident.ID)
	text.WriteString(strings.ReplaceAll(teamIncidentAutonomy, "<id>", incident.ID))
	text.WriteString("\n\nWhat you write at the end is posted in the incident's thread. Keep it short: what happened, what you did or propose, and what happens next.")
	return text.String()
}

// ---------------------------------------------------------------------------
// hi team incidents

// teamIncidentsReport is the open incidents, then the last month's.
func teamIncidentsReport(team string, now time.Time) []teamIncident {
	var open, recent []teamIncident
	for _, incident := range loadTeamIncidents(team) {
		if incident.Status != "closed" {
			open = append(open, incident)
		} else if now.Sub(incident.Opened) < teamJobKeep {
			recent = append(recent, incident)
		}
	}
	slicesReverse(recent)
	return append(open, recent...)
}

func slicesReverse[T any](items []T) {
	for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
		items[i], items[j] = items[j], items[i]
	}
}

func printTeamIncidents(incidents []teamIncident, w io.Writer) {
	if len(incidents) == 0 {
		fmt.Fprintln(w, "No incidents in the last month.")
		return
	}
	for _, incident := range incidents {
		fmt.Fprintf(w, "%s · %s · %s · %s\n  %s\n", incident.ID, incident.Status, incident.Opened.Local().Format("Jan 2 15:04"), teamIncidentWhere(incident), incident.Title)
	}
}

func printTeamIncident(incident teamIncident, w io.Writer) {
	fmt.Fprintf(w, "%s · %s · %s\n%s\n", incident.ID, incident.Status, teamIncidentWhere(incident), incident.Title)
	if incident.Detail != "" {
		fmt.Fprintf(w, "\n%s\n", incident.Detail)
	}
	fmt.Fprintln(w, "\nTimeline:")
	for _, event := range incident.Events {
		line := teamIncidentActionText(event)
		if event.Action == "opened" || event.Action == "note" || event.Action == "ask" || event.Action == "reminder" || event.Action == "recovered" || event.Action == "changed" {
			line = event.Action + ": " + firstNonEmpty(event.Result, event.Because)
		}
		fmt.Fprintf(w, "  %s  %s  %s\n", event.At.Local().Format("Jan 2 15:04"), teamIncidentWho(event.Who), line)
	}
}

func printTeamRules(rules []teamJobRule, w io.Writer) {
	if len(rules) == 0 {
		fmt.Fprintln(w, "The people have given no rules for the app's jobs yet.")
		return
	}
	sort.SliceStable(rules, func(i, j int) bool { return rules[i].Job < rules[j].Job })
	job := ""
	for _, rule := range rules {
		if rule.Job != job {
			job = rule.Job
			fmt.Fprintf(w, "%s:\n", job)
		}
		from := ""
		if rule.Incident != "" {
			from = ", in " + rule.Incident
		}
		fmt.Fprintf(w, "  - %s (%s, %s%s)\n", rule.Rule, rule.Approval.Name, rule.At.Local().Format("2006-01-02"), from)
	}
}

// teamIncidentsCommand is hi team incidents <name> [<id>|--rules], on the
// machine; it reads the team's folder, so it works while the team is down.
func teamIncidentsCommand(args []string, stdout io.Writer) error {
	var words []string
	rules, asJSON := false, false
	for _, arg := range args {
		switch arg {
		case "--rules":
			rules = true
		case "--json":
			asJSON = true
		default:
			if strings.HasPrefix(arg, "-") {
				return usageError{"unknown option " + arg}
			}
			words = append(words, arg)
		}
	}
	if len(words) < 1 || len(words) > 2 {
		return usageError{"usage: hi team incidents <name> [<id>] [--rules] [--json]"}
	}
	team := words[0]
	if _, err := loadTeam(team); err != nil {
		return err
	}
	var value any
	switch {
	case rules:
		value = loadTeamRules(team)
	case len(words) == 2:
		incident, err := loadTeamIncident(team, words[1])
		if err != nil {
			return err
		}
		value = incident
	default:
		value = teamIncidentsReport(team, time.Now())
	}
	if asJSON {
		data, _ := json.MarshalIndent(value, "", "  ")
		fmt.Fprintln(stdout, string(data))
		return nil
	}
	switch v := value.(type) {
	case []teamJobRule:
		printTeamRules(v, stdout)
	case teamIncident:
		printTeamIncident(v, stdout)
	case []teamIncident:
		printTeamIncidents(v, stdout)
	}
	return nil
}

// teamAlertPrefix starts a member's follow-up that raises an alert.
const teamAlertPrefix = "Alert:"

// ---------------------------------------------------------------------------
// the lead's commands, through its socket

func (b *teamBroker) incidentRoutes(mux *http.ServeMux, reply func(http.ResponseWriter, any, error)) {
	readJSON := func(r *http.Request, into any) error {
		return json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(into)
	}
	mux.HandleFunc("GET /jobs", func(w http.ResponseWriter, r *http.Request) {
		slot := teamSlotProduction
		if r.URL.Query().Get("preview") != "" {
			slot = teamSlotPreview
		}
		reply(w, teamJobsReport(b.team, slot, time.Now()), nil)
	})
	mux.HandleFunc("GET /advisories", func(w http.ResponseWriter, r *http.Request) {
		reply(w, teamAdvisoriesReport(b.team), nil)
	})
	mux.HandleFunc("GET /workers", func(w http.ResponseWriter, r *http.Request) {
		slot := teamSlotProduction
		if r.URL.Query().Get("preview") != "" {
			slot = teamSlotPreview
		}
		reply(w, teamWorkersReport(b.team, slot, b.running, time.Now()), nil)
	})
	mux.HandleFunc("POST /jobs/{action}", func(w http.ResponseWriter, r *http.Request) {
		var request teamJobsRequest
		if err := readJSON(r, &request); err != nil {
			reply(w, nil, err)
			return
		}
		switch r.PathValue("action") {
		case "restart":
			state, err := b.restartApp(request)
			reply(w, state, err)
		case "forget":
			reply(w, map[string]string{}, b.forgetJob(request))
		default:
			reply(w, nil, errors.New("no such jobs action"))
		}
	})
	mux.HandleFunc("GET /incidents", func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Query().Get("rules") != "":
			reply(w, loadTeamRules(b.team), nil)
		case r.URL.Query().Get("id") != "":
			incident, err := loadTeamIncident(b.team, r.URL.Query().Get("id"))
			reply(w, incident, err)
		default:
			reply(w, teamIncidentsReport(b.team, time.Now()), nil)
		}
	})
	mux.HandleFunc("POST /incidents/{action}", func(w http.ResponseWriter, r *http.Request) {
		var request teamIncidentRequest
		if err := readJSON(r, &request); err != nil {
			reply(w, nil, err)
			return
		}
		incident, err := b.incidentAction(r.PathValue("action"), request)
		reply(w, incident, err)
	})
	mux.HandleFunc("POST /alert", func(w http.ResponseWriter, r *http.Request) {
		var request teamIncidentRequest
		if err := readJSON(r, &request); err != nil {
			reply(w, nil, err)
			return
		}
		incident, err := b.alert(request.Text, "the lead")
		reply(w, incident, err)
	})
}

// runTeamIncidentLeadCommand is hi team jobs, incidents, and alert in the
// lead's box.
func runTeamIncidentLeadCommand(command string, args []string, stdout io.Writer, call func(method, path string, body, out any) error) error {
	flags := map[string]string{}
	var words []string
	bools := map[string]bool{}
	for i := 0; i < len(args); i++ {
		switch arg := args[i]; arg {
		case "--preview", "--json", "--rules":
			bools[arg] = true
		case "--incident", "--because":
			if i+1 >= len(args) {
				return usageError{arg + " needs a value"}
			}
			flags[arg] = args[i+1]
			i++
		default:
			if strings.HasPrefix(arg, "--") {
				return usageError{"unknown option " + arg}
			}
			words = append(words, arg)
		}
	}
	printJSON := func(value any) {
		data, _ := json.MarshalIndent(value, "", "  ")
		fmt.Fprintln(stdout, string(data))
	}
	switch command {
	case "alert":
		text := strings.TrimSpace(strings.Join(words, " "))
		if text == "" {
			return usageError{`usage: hi team alert "<what you noticed>"`}
		}
		var incident teamIncident
		if err := call("POST", "/alert", teamIncidentRequest{Text: text}, &incident); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Opened %s; hi posted it in the channel and wakes you there.\n", incident.ID)
	case "jobs":
		if len(words) == 0 {
			path := "/jobs"
			slot := teamSlotProduction
			if bools["--preview"] {
				path, slot = "/jobs?preview=1", teamSlotPreview
			}
			var views []teamJobView
			if err := call("GET", path, nil, &views); err != nil {
				return err
			}
			if bools["--json"] {
				printJSON(views)
				return nil
			}
			var workers *teamWorkersView
			call("GET", strings.Replace(path, "/jobs", "/workers", 1), nil, &workers)
			printTeamWorkers(workers, time.Now(), stdout)
			printTeamJobs(views, slot, time.Now(), stdout)
			return nil
		}
		request := teamJobsRequest{Preview: bools["--preview"], Incident: flags["--incident"], Because: flags["--because"]}
		switch {
		case words[0] == "restart" && len(words) == 1:
			var state teamPreview
			if err := call("POST", "/jobs/restart", request, &state); err != nil {
				return err
			}
			fmt.Fprintf(stdout, "%s runs in a new box. hi logged it in %s and said so in its thread.\n", teamCapitalize(teamSlotName(state.Slot)), request.Incident)
		case words[0] == "forget" && len(words) == 2:
			request.Job = words[1]
			var out map[string]string
			if err := call("POST", "/jobs/forget", request, &out); err != nil {
				return err
			}
			fmt.Fprintf(stdout, "Forgot %s: it isn't listed or watched until it writes again.\n", request.Job)
		default:
			return usageError{"usage: hi team jobs [--preview] [--json]\n       hi team jobs restart [--preview] --incident <id> --because <why>\n       hi team jobs forget <job> [--preview] --because <why>"}
		}
	case "incidents":
		usage := usageError{`usage: hi team incidents [<id>] [--rules] [--json]
       hi team incidents ask <id> "<your question, in a line>"
       hi team incidents note <id> "<what you found or did>"
       hi team incidents close <id> --because "<what resolved it>"`}
		if len(words) > 0 {
			switch action := words[0]; action {
			case "ask", "note", "close":
				if len(words) < 2 {
					return usage
				}
				request := teamIncidentRequest{ID: words[1], Text: strings.Join(words[2:], " "), Because: flags["--because"]}
				var incident teamIncident
				if err := call("POST", "/incidents/"+action, request, &incident); err != nil {
					return err
				}
				switch action {
				case "ask":
					fmt.Fprintf(stdout, "%s waits for the people; hi reminds the owners if nobody answers in a day.\n", incident.ID)
				case "note":
					fmt.Fprintf(stdout, "Noted in %s, as your own note.\n", incident.ID)
				case "close":
					fmt.Fprintf(stdout, "Closed %s; hi said so in its thread.\n", incident.ID)
				}
				return nil
			}
		}
		var value any
		switch {
		case bools["--rules"]:
			var rules []teamJobRule
			if err := call("GET", "/incidents?rules=1", nil, &rules); err != nil {
				return err
			}
			value = rules
		case len(words) == 1:
			var incident teamIncident
			if err := call("GET", "/incidents?id="+url.QueryEscape(words[0]), nil, &incident); err != nil {
				return err
			}
			value = incident
		case len(words) == 0:
			var incidents []teamIncident
			if err := call("GET", "/incidents", nil, &incidents); err != nil {
				return err
			}
			value = incidents
		default:
			return usage
		}
		if bools["--json"] {
			printJSON(value)
			return nil
		}
		switch v := value.(type) {
		case []teamJobRule:
			printTeamRules(v, stdout)
		case teamIncident:
			printTeamIncident(v, stdout)
		case []teamIncident:
			printTeamIncidents(v, stdout)
		}
	}
	return nil
}
