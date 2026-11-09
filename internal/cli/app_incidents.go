package cli

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
// app's job that failed or stopped moving (app_jobs.go), production's box
// that stopped, a member's task that failed, or an alert. Each is one
// Slack thread and one record, incidents/i-<n>.json in the team's folder.
// The lead is woken once, handles what can be undone on its own, and asks
// the people about the rest. hi writes the record from the commands the
// lead runs (--incident <id> --because "<why>"), and posts each action in
// the thread, so the people see what the lead decided without asking.
//
// Spec: docs/specs/approved/hi_app_jobs.md.

const (
	// appIncidentRemindAfter is how long a question to the people waits
	// before hi reminds the owners, once.
	appIncidentRemindAfter = 24 * time.Hour
	// appIncidentStandsAfter is how long a production incident hi closes
	// on its own (a job, the app, the workers) may stay open, with nobody
	// asked, before hi tells the owners, once: the lead may have chosen to
	// wait, and waiting with production's work stopped is theirs to choose.
	appIncidentStandsAfter = 4 * time.Hour
	// appAlertsPerDay and appAlertGap limit hi app alert.
	appAlertsPerDay = 10
	appAlertGap     = time.Minute
	// appIncidentLogLines is how much of the app's log an incident keeps.
	appIncidentLogLines = 30
)

type appIncident struct {
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
	// incident was before the ops channel (app_ops.go).
	Channel string    `json:"channel,omitempty"`
	Opened  time.Time `json:"opened"`
	// Status is open, waiting (the lead asked the people), or closed.
	Status   string             `json:"status"`
	Asked    time.Time          `json:"asked,omitzero"`
	Reminded time.Time          `json:"reminded,omitzero"`
	Closed   time.Time          `json:"closed,omitzero"`
	Events   []appIncidentEvent `json:"events"`
}

// channelIn is the channel the incident's thread is in.
func (i appIncident) channelIn(config appConfig) string {
	return firstNonEmpty(i.Channel, config.Channel)
}

// appIncidentEvent is one line of an incident's timeline.
type appIncidentEvent struct {
	At time.Time `json:"at"`
	// Who is hi, lead, or a person's name.
	Who string `json:"who"`
	// Action is opened, changed, restart, task, note, ask, rule, close,
	// recovered, reminder, or forget.
	Action   string       `json:"action"`
	Command  string       `json:"command,omitempty"`
	Because  string       `json:"because,omitempty"`
	Result   string       `json:"result,omitempty"`
	Approval *appApproval `json:"approval,omitempty"`
}

// appJobRule is a decision the people made about a job, kept so the lead
// handles the case alone next time: incidents/rules.json.
type appJobRule struct {
	Job      string      `json:"job"`
	Rule     string      `json:"rule"`
	Approval appApproval `json:"approval"`
	Incident string      `json:"incident,omitempty"`
	At       time.Time   `json:"at"`
}

func appIncidentsDir(team string) string { return filepath.Join(appDir(team), "incidents") }

func appIncidentPath(team, id string) string {
	return filepath.Join(appIncidentsDir(team), id+".json")
}

func appIncidentNumber(id string) int {
	n, _ := strconv.Atoi(strings.TrimPrefix(id, "i-"))
	return n
}

func loadTeamIncident(team, id string) (appIncident, error) {
	id = strings.TrimSpace(id)
	if !strings.HasPrefix(id, "i-") || appIncidentNumber(id) <= 0 {
		return appIncident{}, fmt.Errorf("%q isn't an incident; they are named like i-3", id)
	}
	data, err := os.ReadFile(appIncidentPath(team, id))
	if errors.Is(err, os.ErrNotExist) {
		return appIncident{}, fmt.Errorf("no incident %s; hi app incidents lists them", id)
	}
	if err != nil {
		return appIncident{}, err
	}
	var incident appIncident
	if err := json.Unmarshal(data, &incident); err != nil {
		return appIncident{}, fmt.Errorf("incident %s: %w", id, err)
	}
	return incident, nil
}

func saveTeamIncident(team string, incident appIncident) error {
	if err := os.MkdirAll(appIncidentsDir(team), 0o700); err != nil {
		return err
	}
	data, _ := json.MarshalIndent(incident, "", "  ")
	return writeFileAtomic(appIncidentPath(team, incident.ID), append(data, '\n'), 0o600)
}

// loadTeamIncidents is every incident, oldest first.
func loadTeamIncidents(team string) []appIncident {
	entries, _ := os.ReadDir(appIncidentsDir(team))
	var incidents []appIncident
	for _, entry := range entries {
		id, ok := strings.CutSuffix(entry.Name(), ".json")
		if !ok || !strings.HasPrefix(id, "i-") {
			continue
		}
		if incident, err := loadTeamIncident(team, id); err == nil {
			incidents = append(incidents, incident)
		}
	}
	sort.Slice(incidents, func(i, j int) bool { return appIncidentNumber(incidents[i].ID) < appIncidentNumber(incidents[j].ID) })
	return incidents
}

func appRulesPath(team string) string { return filepath.Join(appIncidentsDir(team), "rules.json") }

func loadTeamRules(team string) []appJobRule {
	var rules []appJobRule
	if data, err := os.ReadFile(appRulesPath(team)); err == nil {
		json.Unmarshal(data, &rules)
	}
	return rules
}

func appRulesFor(rules []appJobRule, job string) []appJobRule {
	var out []appJobRule
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
func (b *appBroker) openIncident(incident appIncident, alert, thread string, prompt func(appConfig, appIncident) string) (appIncident, error) {
	config, err := loadTeam(b.team)
	if err != nil {
		return appIncident{}, err
	}
	b.incidentMu.Lock()
	last := 0
	for _, old := range loadTeamIncidents(b.team) {
		last = max(last, appIncidentNumber(old.ID))
	}
	incident.ID = fmt.Sprintf("i-%d", last+1)
	incident.Status = "open"
	if incident.Opened.IsZero() {
		incident.Opened = time.Now().UTC()
	}
	incident.Events = append(incident.Events, appIncidentEvent{At: incident.Opened, Who: "hi", Action: "opened", Result: incident.Title})
	err = saveTeamIncident(b.team, incident)
	b.incidentMu.Unlock()
	if err != nil {
		return appIncident{}, err
	}
	text := fmt.Sprintf(":rotating_light: *Incident %s · %s*\n%s", incident.ID, appIncidentWhere(incident), appSlackEscape(alert))
	// A task's incident is told only in its work's thread; without one, the
	// lead's wait says it.
	// In a work's thread, in the main channel where that thread is; on its
	// own, in the ops channel.
	if b.channel != nil && (thread != "" || incident.Kind != "task") {
		channel := config.Channel
		// An incident nobody hears of is none: a refusal or a blip, likely
		// when the incident is the network's, is tried again.
		for attempt := range appIncidentPostTries {
			if attempt > 0 {
				time.Sleep(appIncidentPostWait * time.Duration(attempt))
			}
			if thread != "" {
				_, err = b.channel.postIn(channel, thread, text)
			} else {
				channel = config.opsChannel()
				var ts string
				ts, err = b.channel.post(channel, text)
				if err == nil {
					thread = ts
				}
			}
			if err == nil {
				break
			}
		}
		if err != nil {
			b.addIncidentEvent(incident.ID, appIncidentEvent{Who: "hi", Action: "note", Result: "couldn't say it in Slack: " + err.Error()})
		}
		if err == nil {
			if channel == config.Channel {
				channel = ""
			}
			b.updateIncident(incident.ID, func(i *appIncident) { i.Thread, i.Channel = thread, channel })
			incident.Thread, incident.Channel = thread, channel
		}
	}
	if prompt != nil && b.wakeLead != nil && incident.Thread != "" {
		if err := b.wakeLead("hi-incident-"+incident.ID, prompt(config, incident), "slack:"+incident.channelIn(config)+":"+incident.Thread); err != nil {
			b.addIncidentEvent(incident.ID, appIncidentEvent{Who: "hi", Action: "note", Result: "couldn't wake the lead: " + err.Error()})
		}
	}
	return incident, nil
}

// appIncidentPostTries and appIncidentPostWait are how an incident's post
// is tried again; tests shorten the wait.
var (
	appIncidentPostTries = 3
	appIncidentPostWait  = 3 * time.Second
)

func appIncidentWhere(incident appIncident) string {
	switch incident.Kind {
	case "job":
		return appSlotName(incident.Slot) + "'s job " + incident.Job
	case "app":
		return appSlotName(incident.Slot) + "'s app"
	case "task":
		return "task " + incident.Task
	case "workers":
		return appSlotName(incident.Slot) + "'s workers"
	}
	return "alert"
}

// updateIncident changes a saved incident.
func (b *appBroker) updateIncident(id string, change func(*appIncident)) (appIncident, error) {
	b.incidentMu.Lock()
	defer b.incidentMu.Unlock()
	incident, err := loadTeamIncident(b.team, id)
	if err != nil {
		return incident, err
	}
	change(&incident)
	return incident, saveTeamIncident(b.team, incident)
}

func (b *appBroker) addIncidentEvent(id string, event appIncidentEvent) {
	if event.At.IsZero() {
		event.At = time.Now().UTC()
	}
	b.updateIncident(id, func(incident *appIncident) { incident.Events = append(incident.Events, event) })
}

func (b *appBroker) incidentOpen(id string) bool {
	incident, err := loadTeamIncident(b.team, id)
	return err == nil && incident.Status != "closed"
}

// logIncidentAction records something the lead did and says it in the
// incident's thread: what, and why.
func (b *appBroker) logIncidentAction(incident appIncident, event appIncidentEvent) {
	b.addIncidentEvent(incident.ID, event)
	if b.channel == nil || incident.Thread == "" {
		return
	}
	config, err := loadTeam(b.team)
	if err != nil {
		return
	}
	text := fmt.Sprintf(":gear: *%s, %s:* %s", appCapitalize(appIncidentWho(event.Who)), incident.ID, appSlackEscape(appIncidentActionText(event)))
	b.channel.postIn(incident.channelIn(config), incident.Thread, text)
}

func appIncidentWho(who string) string {
	if who == "lead" {
		return "the lead"
	}
	return who
}

func appIncidentActionText(event appIncidentEvent) string {
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
func (b *appBroker) recovered(id, text string) {
	incident, err := b.updateIncident(id, func(incident *appIncident) {
		now := time.Now().UTC()
		incident.Events = append(incident.Events, appIncidentEvent{At: now, Who: "hi", Action: "recovered", Result: text})
		incident.Status, incident.Closed = "closed", now
	})
	if err != nil || b.channel == nil || incident.Thread == "" {
		return
	}
	if config, err := loadTeam(b.team); err == nil {
		b.channel.postIn(incident.channelIn(config), incident.Thread, fmt.Sprintf(":white_check_mark: %s Incident %s is closed.", appSlackEscape(text), incident.ID))
	}
}

// leadIncident is an open incident the lead names with what it does.
func (b *appBroker) leadIncident(id, because string) (appIncident, error) {
	if strings.TrimSpace(id) == "" {
		return appIncident{}, errors.New("name the incident this is for: --incident i-3")
	}
	if strings.TrimSpace(because) == "" {
		return appIncident{}, errors.New("say why, in a sentence the people can read: --because \"<why>\"")
	}
	incident, err := loadTeamIncident(b.team, id)
	if err != nil {
		return incident, err
	}
	if incident.Status == "closed" {
		return incident, fmt.Errorf("incident %s is closed; hi app incidents lists the open ones", incident.ID)
	}
	return incident, nil
}

// ---------------------------------------------------------------------------
// what opens one

func (b *appBroker) openJobIncident(slot string, job appJob, condition string, now time.Time) (appIncident, error) {
	title := appJobConditionText(job, condition)
	data, _ := json.MarshalIndent(job, "", "  ")
	detail := "The job's file:\n" + string(data)
	if state, ok := loadTeamSlot(b.team, slot); ok && b.slotLogs != nil {
		if logs := strings.TrimSpace(b.slotLogs(state.Container)); logs != "" {
			detail += "\n\nThe app's last lines:\n" + logs
		}
	}
	alert := title + "."
	if !b.started.IsZero() && now.Sub(b.started) < 2*appJobSettle {
		restarted := fmt.Sprintf("hi started the app at %s UTC, which cuts the app's model calls and restarts its boxes; that may be the cause, though the job hasn't moved again since.", b.started.UTC().Format("15:04"))
		alert += "\n" + restarted
		detail += "\n\n" + restarted
	}
	incident := appIncident{Kind: "job", Slot: slot, Job: job.Job, Title: title, Detail: detail, Opened: now.UTC()}
	if slot == appSlotPreview {
		// A preview is an owner's work in progress: hi app jobs --preview,
		// and the dev page, show it.
		return incident, nil
	}
	return b.openIncident(incident, alert+"\nThe lead looks into it here.", "", appIncidentPrompt)
}

// appStopped opens an incident for production's box, which stopped on its
// own while the team ran, before hi starts it again. A stop from before
// the team started (a reboot, hi app down) isn't one.
func (b *appBroker) appStopped(state appPreview) {
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
	for _, job := range readTeamJobs(b.team, appSlotProduction) {
		if job.State == "running" {
			running = append(running, job.Job)
		}
	}
	if len(running) > 0 {
		title += "; it was running " + strings.Join(running, ", ")
	}
	detail := ""
	if b.slotLogs != nil {
		if logs := strings.TrimSpace(b.slotLogs(state.Container)); logs != "" {
			detail = "The app's last lines:\n" + logs
		}
	}
	incident := appIncident{Kind: "app", Slot: appSlotProduction, Title: title, Detail: detail}
	b.openIncident(incident, title+".\nhi starts it again in a new box; the lead looks into why.", "", appIncidentPrompt)
}

// alert is hi app alert: something the lead or a member noticed.
func (b *appBroker) alert(text, who string) (appIncident, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return appIncident{}, errors.New(`say what's wrong: hi app alert "<what you noticed>"`)
	}
	if len(text) > 2000 {
		text = appClipText(text, 2000)
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
	if now.Sub(last) < appAlertGap {
		return appIncident{}, errors.New("an alert went out less than a minute ago; add to its thread instead")
	}
	if today >= appAlertsPerDay {
		return appIncident{}, fmt.Errorf("%d alerts went out in the last day, the most hi sends; say it in the channel instead", appAlertsPerDay)
	}
	incident := appIncident{Kind: "alert", Title: appClipText(text, 300), Detail: who + ": " + text}
	return b.openIncident(incident, text+"\n(from "+who+") The lead looks into it here.", "", appIncidentPrompt)
}

// remindIncidents reminds the owners, once, of a question the lead asked
// that nobody answered in a day, and of a production incident that has
// stood for hours with nobody asked.
func (b *appBroker) remindIncidents(now time.Time) error {
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
		if appIncidentStands(incident, now) {
			b.updateIncident(incident.ID, func(i *appIncident) {
				i.Reminded = now.UTC()
				i.Events = append(i.Events, appIncidentEvent{At: now.UTC(), Who: "hi", Action: "reminder", Result: "told the owners it still stands"})
			})
			b.channel.postIn(incident.channelIn(config), incident.Thread, fmt.Sprintf(":hourglass: %s, %s (%s) has stood for %s, and nobody was asked. The lead's account is above: if what it waits for isn't what you want, say so here.",
				strings.Join(owners, " "), incident.ID, appIncidentWhere(incident), appJobDuration(now.Sub(incident.Opened).Truncate(time.Hour))))
			continue
		}
		if incident.Status != "waiting" || !incident.Reminded.IsZero() || incident.Thread == "" || now.Sub(incident.Asked) < appIncidentRemindAfter {
			continue
		}
		messages, _ := readTeamChannel(appChannelLogDir(b.team, config, incident.channelIn(config)), appSlackTS(incident.Asked))
		answered := false
		for _, message := range messages {
			if message.Thread == incident.Thread && !appChannelFromBot(config, message) {
				answered = true
			}
		}
		b.updateIncident(incident.ID, func(i *appIncident) {
			i.Reminded = now.UTC()
			if !answered {
				i.Events = append(i.Events, appIncidentEvent{At: now.UTC(), Who: "hi", Action: "reminder", Result: "reminded the owners"})
			}
		})
		if answered {
			continue
		}
		b.channel.postIn(incident.channelIn(config), incident.Thread, fmt.Sprintf(":hourglass: %s, the lead's question above has waited a day. Nothing happens to %s until one of you answers.",
			strings.Join(owners, " "), appIncidentWhere(incident)))
	}
	return nil
}

// appIncidentStands is whether an incident is one of production's that hi
// closes on its own, open for appIncidentStandsAfter with nobody asked
// and the owners not yet told.
func appIncidentStands(incident appIncident, now time.Time) bool {
	switch incident.Kind {
	case "job", "app", "workers":
	default:
		return false
	}
	return incident.Status == "open" && incident.Slot == appSlotProduction && incident.Thread != "" &&
		incident.Reminded.IsZero() && now.Sub(incident.Opened) >= appIncidentStandsAfter
}

// ---------------------------------------------------------------------------
// what the lead does with one

type appIncidentRequest struct {
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
func (b *appBroker) incidentAction(action string, request appIncidentRequest) (appIncident, error) {
	switch action {
	case "note":
		incident, err := b.leadIncident(request.ID, request.Text)
		if err != nil {
			return incident, err
		}
		// A note is the lead's own account, kept as such; not posted.
		b.addIncidentEvent(incident.ID, appIncidentEvent{Who: "lead", Action: "note", Result: "(the lead's own note) " + appClipText(request.Text, 2000)})
	case "ask":
		incident, err := b.leadIncident(request.ID, request.Text)
		if err != nil {
			return incident, err
		}
		now := time.Now().UTC()
		b.updateIncident(incident.ID, func(i *appIncident) {
			i.Status, i.Asked, i.Reminded = "waiting", now, time.Time{}
			i.Events = append(i.Events, appIncidentEvent{At: now, Who: "lead", Action: "ask", Result: appClipText(request.Text, 2000)})
		})
	case "close":
		incident, err := b.leadIncident(request.ID, request.Because)
		if err != nil {
			return incident, err
		}
		now := time.Now().UTC()
		b.updateIncident(incident.ID, func(i *appIncident) { i.Status, i.Closed = "closed", now })
		b.logIncidentAction(incident, appIncidentEvent{At: now, Who: firstNonEmpty(request.owner, "lead"), Action: "close", Result: "closed " + incident.ID, Because: request.Because})
	default:
		return appIncident{}, fmt.Errorf("no incident action %q", action)
	}
	return loadTeamIncident(b.team, request.ID)
}

// addRule keeps a decision the people made about a job, with the message
// it was made in, which must be one of theirs.
func (b *appBroker) addRule(request appIncidentRequest) (appIncident, error) {
	if !appJobName.MatchString(request.Job) {
		return appIncident{}, fmt.Errorf("%q isn't a job's name; hi app jobs lists them", request.Job)
	}
	text := strings.TrimSpace(request.Text)
	if text == "" {
		return appIncident{}, errors.New("say the rule, in the people's words")
	}
	var approval appApproval
	if request.owner != "" {
		approval = appApproval{Name: request.owner}
	} else {
		return appIncident{}, errors.New("a rule for a job is the owners' decision: they add it on the dev page")
	}
	var err error
	var incident appIncident
	if request.ID != "" {
		if incident, err = loadTeamIncident(b.team, request.ID); err != nil {
			return incident, err
		}
	}
	b.incidentMu.Lock()
	rules := append(loadTeamRules(b.team), appJobRule{Job: request.Job, Rule: appClipText(text, 1000), Approval: approval, Incident: incident.ID, At: time.Now().UTC()})
	if err := os.MkdirAll(appIncidentsDir(b.team), 0o700); err != nil {
		b.incidentMu.Unlock()
		return incident, err
	}
	data, _ := json.MarshalIndent(rules, "", "  ")
	err = writeFileAtomic(appRulesPath(b.team), append(data, '\n'), 0o600)
	b.incidentMu.Unlock()
	if err != nil {
		return incident, err
	}
	if incident.ID != "" {
		b.logIncidentAction(incident, appIncidentEvent{Who: firstNonEmpty(request.owner, "lead"), Action: "rule", Result: fmt.Sprintf("a rule for %s: %s", request.Job, text), Approval: &approval})
		return loadTeamIncident(b.team, incident.ID)
	}
	return incident, nil
}

// removeRule takes a job's rule out: an owner on the dev page changed
// their mind.
func (b *appBroker) removeRule(job, rule string) error {
	b.incidentMu.Lock()
	defer b.incidentMu.Unlock()
	var kept []appJobRule
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
	data, _ := json.MarshalIndent(append([]appJobRule{}, kept...), "", "  ")
	return writeFileAtomic(appRulesPath(b.team), append(data, '\n'), 0o600)
}

// ---------------------------------------------------------------------------
// what wakes the lead

// appIncidentAutonomy is how the lead handles an incident: the same in
// every prompt and in its skill.
const appIncidentAutonomy = `Handle what you can on your own, so the people don't have to babysit the work; ask the owners about the rest. They build and release in the dev box and on the dev page; you don't change the code.

On your own, when it can be undone and changes no result nobody has ruled on:
- Wait or retry when the cause passes (a network error, a service that was down, a rate limit), and check back once it should have moved.
- Restart the app's box once per incident: hi app jobs restart [--preview] --incident <id> --because "<why>". After that, the owners restart it on the dev page.
- Apply a rule the owners gave (listed below, and in hi app incidents --rules).

Ask the owners when no rule covers a choice that changes results, when a step can't be undone (deleting or rewriting data, a rollback), when the fix is a change to the code, when your own attempts ran out, or when you aren't sure what happened. Never skip, drop, or invent an item a job couldn't process unless a rule says so: in some jobs every later value depends on the one before, and a silent gap makes them all wrong. Say what you found, what depends on it, and the choices, each with what it would do to the results, and which you'd take. Then run hi app incidents ask <id> "<your question in a line>", so hi reminds the owners if nobody answers in a day. They keep their decision as the job's rule on the dev page. When it needs a change to the code, file a task with what you found (hi app task add), and say its number here.

Waiting is a choice too. When what you'd do leaves production's work stopped beyond the incident's own cause, say how much is left and what waiting costs at the work's usual rate, give the choices (resuming it through the app's own controls now is one, and isn't a restart), and ask.

Log what you do: pass --incident <id> --because "<why>" to hi app jobs restart, and keep the rest as notes (hi app incidents note <id> "..."); hi posts each in this thread and keeps it in the incident's record. When it is resolved and hi hasn't closed it, run hi app incidents close <id> --because "<what resolved it>".`

// appIncidentPrompt wakes the lead for an incident, with what it would
// otherwise spend its turn finding.
func appIncidentPrompt(config appConfig, incident appIncident) string {
	var text strings.Builder
	fmt.Fprintf(&text, "This is hi, not a person. Incident %s is open: %s.\n\n", incident.ID, incident.Title)
	if incident.Detail != "" {
		fmt.Fprintf(&text, "%s\n\n", appClipText(incident.Detail, 6000))
	}
	if incident.Job != "" {
		rules := appRulesFor(loadTeamRules(config.Name), incident.Job)
		if len(rules) == 0 {
			fmt.Fprintf(&text, "The people have given no rules for %s yet.\n\n", incident.Job)
		} else {
			fmt.Fprintf(&text, "Rules the people gave for %s:\n", incident.Job)
			for _, rule := range rules {
				fmt.Fprintf(&text, "- %s (%s, %s)\n", rule.Rule, rule.Approval.Name, rule.At.Format("2006-01-02"))
			}
			text.WriteString("\n")
		}
		text.WriteString("hi app jobs shows every job's state, count, and rate. ")
	}
	fmt.Fprintf(&text, "First find out why, reading only: the logs, the code, the item, hi app incidents %s. Nothing in production changes while you look.\n\n", incident.ID)
	text.WriteString(strings.ReplaceAll(appIncidentAutonomy, "<id>", incident.ID))
	text.WriteString("\n\nWhat you write at the end is posted in the incident's thread. Keep it short: what happened, what you did or propose, and what happens next.")
	return text.String()
}

// ---------------------------------------------------------------------------
// hi app incidents

// appIncidentsReport is the open incidents, then the last month's.
func appIncidentsReport(team string, now time.Time) []appIncident {
	var open, recent []appIncident
	for _, incident := range loadTeamIncidents(team) {
		if incident.Status != "closed" {
			open = append(open, incident)
		} else if now.Sub(incident.Opened) < appJobKeep {
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

func printTeamIncidents(incidents []appIncident, w io.Writer) {
	if len(incidents) == 0 {
		fmt.Fprintln(w, "No incidents in the last month.")
		return
	}
	for _, incident := range incidents {
		fmt.Fprintf(w, "%s · %s · %s · %s\n  %s\n", incident.ID, incident.Status, incident.Opened.Local().Format("Jan 2 15:04"), appIncidentWhere(incident), incident.Title)
	}
}

func printTeamIncident(incident appIncident, w io.Writer) {
	fmt.Fprintf(w, "%s · %s · %s\n%s\n", incident.ID, incident.Status, appIncidentWhere(incident), incident.Title)
	if incident.Detail != "" {
		fmt.Fprintf(w, "\n%s\n", incident.Detail)
	}
	fmt.Fprintln(w, "\nTimeline:")
	for _, event := range incident.Events {
		line := appIncidentActionText(event)
		if event.Action == "opened" || event.Action == "note" || event.Action == "ask" || event.Action == "reminder" || event.Action == "recovered" || event.Action == "changed" {
			line = event.Action + ": " + firstNonEmpty(event.Result, event.Because)
		}
		fmt.Fprintf(w, "  %s  %s  %s\n", event.At.Local().Format("Jan 2 15:04"), appIncidentWho(event.Who), line)
	}
}

func printTeamRules(rules []appJobRule, w io.Writer) {
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

// appIncidentsCommand is hi app incidents <name> [<id>|--rules], on the
// machine; it reads the team's folder, so it works while the team is down.
func appIncidentsCommand(args []string, stdout io.Writer) error {
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
		return usageError{"usage: hi app incidents <name> [<id>] [--rules] [--json]"}
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
		value = appIncidentsReport(team, time.Now())
	}
	if asJSON {
		data, _ := json.MarshalIndent(value, "", "  ")
		fmt.Fprintln(stdout, string(data))
		return nil
	}
	switch v := value.(type) {
	case []appJobRule:
		printTeamRules(v, stdout)
	case appIncident:
		printTeamIncident(v, stdout)
	case []appIncident:
		printTeamIncidents(v, stdout)
	}
	return nil
}

// appAlertPrefix starts a member's follow-up that raises an alert.
const appAlertPrefix = "Alert:"

// ---------------------------------------------------------------------------
// the lead's commands, through its socket

func (b *appBroker) incidentRoutes(mux *http.ServeMux, reply func(http.ResponseWriter, any, error)) {
	readJSON := func(r *http.Request, into any) error {
		return json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(into)
	}
	mux.HandleFunc("GET /jobs", func(w http.ResponseWriter, r *http.Request) {
		slot := appSlotProduction
		if r.URL.Query().Get("preview") != "" {
			slot = appSlotPreview
		}
		reply(w, appJobsReport(b.team, slot, time.Now()), nil)
	})
	mux.HandleFunc("GET /advisories", func(w http.ResponseWriter, r *http.Request) {
		reply(w, appAdvisoriesReport(b.team), nil)
	})
	mux.HandleFunc("GET /workers", func(w http.ResponseWriter, r *http.Request) {
		slot := appSlotProduction
		if r.URL.Query().Get("preview") != "" {
			slot = appSlotPreview
		}
		reply(w, appWorkersReport(b.team, slot, b.running, time.Now()), nil)
	})
	mux.HandleFunc("POST /jobs/{action}", func(w http.ResponseWriter, r *http.Request) {
		var request appJobsRequest
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
			reply(w, appIncidentsReport(b.team, time.Now()), nil)
		}
	})
	mux.HandleFunc("POST /incidents/{action}", func(w http.ResponseWriter, r *http.Request) {
		var request appIncidentRequest
		if err := readJSON(r, &request); err != nil {
			reply(w, nil, err)
			return
		}
		incident, err := b.incidentAction(r.PathValue("action"), request)
		reply(w, incident, err)
	})
	mux.HandleFunc("POST /alert", func(w http.ResponseWriter, r *http.Request) {
		var request appIncidentRequest
		if err := readJSON(r, &request); err != nil {
			reply(w, nil, err)
			return
		}
		incident, err := b.alert(request.Text, "the lead")
		reply(w, incident, err)
	})
}

// runTeamIncidentLeadCommand is hi app jobs, incidents, and alert in the
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
			return usageError{`usage: hi app alert "<what you noticed>"`}
		}
		var incident appIncident
		if err := call("POST", "/alert", appIncidentRequest{Text: text}, &incident); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Opened %s; hi posted it in the channel and wakes you there.\n", incident.ID)
	case "jobs":
		if len(words) == 0 {
			path := "/jobs"
			slot := appSlotProduction
			if bools["--preview"] {
				path, slot = "/jobs?preview=1", appSlotPreview
			}
			var views []appJobView
			if err := call("GET", path, nil, &views); err != nil {
				return err
			}
			if bools["--json"] {
				printJSON(views)
				return nil
			}
			var workers *appWorkersView
			call("GET", strings.Replace(path, "/jobs", "/workers", 1), nil, &workers)
			printTeamWorkers(workers, time.Now(), stdout)
			printTeamJobs(views, slot, time.Now(), stdout)
			return nil
		}
		request := appJobsRequest{Preview: bools["--preview"], Incident: flags["--incident"], Because: flags["--because"]}
		switch {
		case words[0] == "restart" && len(words) == 1:
			var state appPreview
			if err := call("POST", "/jobs/restart", request, &state); err != nil {
				return err
			}
			fmt.Fprintf(stdout, "%s runs in a new box. hi logged it in %s and said so in its thread.\n", appCapitalize(appSlotName(state.Slot)), request.Incident)
		case words[0] == "forget" && len(words) == 2:
			request.Job = words[1]
			var out map[string]string
			if err := call("POST", "/jobs/forget", request, &out); err != nil {
				return err
			}
			fmt.Fprintf(stdout, "Forgot %s: it isn't listed or watched until it writes again.\n", request.Job)
		default:
			return usageError{"usage: hi app jobs [--preview] [--json]\n       hi app jobs restart [--preview] --incident <id> --because <why>\n       hi app jobs forget <job> [--preview] --because <why>"}
		}
	case "incidents":
		usage := usageError{`usage: hi app incidents [<id>] [--rules] [--json]
       hi app incidents ask <id> "<your question, in a line>"
       hi app incidents note <id> "<what you found or did>"
       hi app incidents close <id> --because "<what resolved it>"`}
		if len(words) > 0 {
			switch action := words[0]; action {
			case "ask", "note", "close":
				if len(words) < 2 {
					return usage
				}
				request := appIncidentRequest{ID: words[1], Text: strings.Join(words[2:], " "), Because: flags["--because"]}
				var incident appIncident
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
			var rules []appJobRule
			if err := call("GET", "/incidents?rules=1", nil, &rules); err != nil {
				return err
			}
			value = rules
		case len(words) == 1:
			var incident appIncident
			if err := call("GET", "/incidents?id="+url.QueryEscape(words[0]), nil, &incident); err != nil {
				return err
			}
			value = incident
		case len(words) == 0:
			var incidents []appIncident
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
		case []appJobRule:
			printTeamRules(v, stdout)
		case appIncident:
			printTeamIncident(v, stdout)
		case []appIncident:
			printTeamIncidents(v, stdout)
		}
	}
	return nil
}
