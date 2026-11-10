package cli

// hi watches that production's app answers, not only that its box runs:
// an app whose own work blocks it serves nobody while its box runs on.
// Once a minute hi asks the box's port for /. When nothing has answered
// for appBoxSilentAfter, hi opens an incident, and closes it when the app
// answers again. Any answer counts, as when the app starts: a 503 while it
// warms up is an answer.

import (
	"fmt"
	"net/http"
	"strings"
	"time"
)

const (
	// appBoxAnswerTimeout is how long one ask waits: longer than at the
	// app's start, as an app at work may be slow.
	appBoxAnswerTimeout = 10 * time.Second
	// appBoxSilentAfter is how long the app may not answer before it is
	// an incident.
	appBoxSilentAfter = 5 * time.Minute
)

// appBoxAnswers says the app on hostPort answers, with anything but a
// server error: an app that says 503, "starting", for longer than
// appBoxSilentAfter serves nobody, as one that says nothing doesn't.
func appBoxAnswers(hostPort int) bool {
	client := &http.Client{Timeout: appBoxAnswerTimeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/", hostPort))
	if err != nil {
		return false
	}
	response.Body.Close()
	return response.StatusCode < 500
}

// checkAnswers is one look at whether production's app answers.
func (b *appBroker) checkAnswers(now time.Time) error {
	if b.stopped() {
		return nil // its box is paused on purpose (app_watch_stop.go)
	}
	state, ok := loadTeamSlot(b.team, appSlotProduction)
	answered := ok && state.HostPort != 0 && b.answers(state.HostPort)

	b.jobsMu.Lock()
	jobs := loadTeamJobsState(b.team)
	open := jobs.SilentIncident
	if answered || !ok {
		jobs.Silent, jobs.SilentIncident = time.Time{}, ""
		err := saveTeamJobsState(b.team, jobs)
		b.jobsMu.Unlock()
		if open != "" && b.incidentOpen(open) {
			b.recovered(open, "Production's app answers again.")
		}
		return err
	}
	// Silence from before this broker started, while the team was down,
	// counts from now.
	if jobs.Silent.IsZero() || jobs.Silent.Before(b.started) {
		jobs.Silent = now.UTC()
		err := saveTeamJobsState(b.team, jobs)
		b.jobsMu.Unlock()
		return err
	}
	since := jobs.Silent
	b.jobsMu.Unlock()
	if open != "" || now.Sub(since) < appBoxSilentAfter || b.appIncidentOpen() {
		return nil
	}

	runs := b.running(state.Container)
	minutes := int(now.Sub(since).Round(time.Minute) / time.Minute)
	title := fmt.Sprintf("Production's app hasn't answered for %d minutes, or only with an error", minutes)
	var running []string
	for _, job := range readTeamJobs(b.team, appSlotProduction) {
		if job.State == "running" {
			running = append(running, job.Job)
		}
	}
	if len(running) > 0 {
		title += "; it is running " + strings.Join(running, ", ")
	}
	detail := fmt.Sprintf("hi asks / on the app's port (%d) every minute, and since %s UTC nothing has answered within %s but a server error (5xx), such as 503 while the app says it is starting. ",
		state.HostPort, since.Format("15:04"), appBoxAnswerTimeout)
	alert := title + "."
	if runs {
		detail += "Its box still runs, so hi doesn't start it again. The app may be blocked by its own work, such as a job on the thread that serves requests; a restart starts the same work again, so look at what the app does before restarting it."
		alert += "\nIts box still runs, so hi leaves it be; the lead looks into it here."
	} else {
		detail += "Its box doesn't run; hi starts production again."
		alert += "\nIts box doesn't run; hi starts production again, and the lead looks into why here."
	}
	if b.slotLogs != nil {
		if logs := strings.TrimSpace(b.slotLogs(state.Container)); logs != "" {
			detail += "\n\nThe app's last lines:\n" + logs
		}
	}
	incident, err := b.openIncident(appIncident{Kind: "app", Slot: appSlotProduction, Title: title, Detail: detail, Opened: now.UTC()}, alert, "", appIncidentPrompt)
	if err != nil {
		return err
	}
	b.jobsMu.Lock()
	defer b.jobsMu.Unlock()
	jobs = loadTeamJobsState(b.team)
	jobs.SilentIncident = incident.ID
	return saveTeamJobsState(b.team, jobs)
}

// appIncidentOpen says an incident about production's app is open already,
// such as its box that stopped: one is enough.
func (b *appBroker) appIncidentOpen() bool {
	for _, incident := range loadTeamIncidents(b.team) {
		if incident.Kind == "app" && incident.Slot == appSlotProduction && incident.Status != "closed" {
			return true
		}
	}
	return false
}
