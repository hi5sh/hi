package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func writeTeamTestJob(t *testing.T, slot string, job teamJob) {
	t.Helper()
	dir := teamSlotJobsDir("payments", slot)
	os.MkdirAll(dir, 0o700)
	data, _ := json.Marshal(job)
	if err := os.WriteFile(filepath.Join(dir, job.Job+".json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// teamTestWakes records the lead's wake-ups.
func teamTestWakes(b *teamBroker) *[]string {
	var mu sync.Mutex
	var woken []string
	b.wakeLead = func(name, prompt, deliver string) error {
		mu.Lock()
		defer mu.Unlock()
		woken = append(woken, name+" "+deliver+" "+prompt)
		return nil
	}
	return &woken
}

func TestTeamJobCommand(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HI_JOBS_DIR", "")
	if err := teamJobCommand([]string{"backfill"}, nil); err == nil || !strings.Contains(err.Error(), "app, in its box") {
		t.Fatal(err)
	}
	t.Setenv("HI_JOBS_DIR", dir)
	read := func() teamJob {
		t.Helper()
		var job teamJob
		data, err := os.ReadFile(filepath.Join(dir, "backfill.json"))
		if err != nil || json.Unmarshal(data, &job) != nil {
			t.Fatal(err, string(data))
		}
		return job
	}
	if err := teamJobCommand([]string{"backfill", "--done", "10", "--total", "500", "--unit", "releases", "--stall-after", "2h"}, nil); err != nil {
		t.Fatal(err)
	}
	if job := read(); job.State != "running" || job.Done != 10 || job.Total != 500 || job.Unit != "releases" || job.stall() != 2*time.Hour || job.At.IsZero() {
		t.Fatalf("%+v", job)
	}
	// What it isn't told stays.
	teamJobCommand([]string{"backfill", "--blocked", "--item", "r-338", "--error", "no text and no PDF"}, nil)
	if job := read(); job.State != "blocked" || job.Done != 10 || job.Total != 500 || job.Item != "r-338" || job.Error != "no text and no PDF" {
		t.Fatalf("%+v", job)
	}
	// Moving again drops the old error.
	teamJobCommand([]string{"backfill", "--running", "--done", "11"}, nil)
	if job := read(); job.State != "running" || job.Error != "" || job.Done != 11 {
		t.Fatalf("%+v", job)
	}
	teamJobCommand([]string{"backfill", "--finished"}, nil)
	if job := read(); job.State != "done" {
		t.Fatalf("%+v", job)
	}
	for _, bad := range [][]string{{"Backfill"}, {"backfill", "--done", "-1"}, {"backfill", "--stall-after", "5s"}, {"backfill", "--done"}, {"backfill", "--what"}} {
		if err := teamJobCommand(bad, nil); err == nil {
			t.Errorf("%v: no error", bad)
		}
	}
}

func TestTeamJobsAlertOncePerIncident(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	channel := &fakeTeamChannel{}
	b.channel = channel
	woken := teamTestWakes(b.teamBroker)
	b.appLogs = func(string) string { return "fetching r-338\nno text" }
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	b.started = now.Add(-time.Hour)
	check := func(at time.Time) {
		t.Helper()
		if err := b.checkJobs(at); err != nil {
			t.Fatal(err)
		}
	}
	os.MkdirAll(filepath.Join(teamDir("payments"), "run"), 0o700)
	saveTeamSlot("payments", teamPreview{Slot: teamSlotProduction, Container: "app-prod"})

	// A job that moves raises nothing, and its rate comes from hi's counts.
	writeTeamTestJob(t, teamSlotProduction, teamJob{Job: "backfill", State: "running", Done: 100, Total: 533386, Unit: "releases", At: now})
	check(now)
	writeTeamTestJob(t, teamSlotProduction, teamJob{Job: "backfill", State: "running", Done: 600, Total: 533386, Unit: "releases", At: now.Add(2 * time.Hour)})
	check(now.Add(2 * time.Hour))
	if len(channel.calls) != 0 || len(*woken) != 0 {
		t.Fatal(channel.calls, *woken)
	}
	views := teamJobsReport("payments", teamSlotProduction, now.Add(2*time.Hour))
	if len(views) != 1 || views[0].PerDay == nil || int(*views[0].PerDay) != 6000 {
		t.Fatalf("%+v", views)
	}

	// Blocked on an item: one incident, in the channel, and the lead woken
	// there with the job, the app's lines, and the rules so far.
	blocked := teamJob{Job: "backfill", State: "blocked", Done: 600, Total: 533386, Unit: "releases", At: now.Add(3 * time.Hour), Item: "338809a1", Error: "the release has no text and its PDF can't be read"}
	writeTeamTestJob(t, teamSlotProduction, blocked)
	check(now.Add(3 * time.Hour))
	check(now.Add(3*time.Hour + time.Minute))
	if len(channel.calls) != 1 || !strings.Contains(channel.calls[0], "Incident i-1 · production's job backfill") || !strings.Contains(channel.calls[0], "338809a1") {
		t.Fatal(channel.calls)
	}
	if len(*woken) != 1 || !strings.HasPrefix((*woken)[0], "hi-incident-i-1 slack:C07PAYMENTS:1700000000.000001 ") {
		t.Fatal(*woken)
	}
	for _, want := range []string{"Incident i-1 is open", "no rules for backfill", "fetching r-338", "--incident i-1", "Never skip", "hi team incidents ask i-1"} {
		if !strings.Contains((*woken)[0], want) {
			t.Errorf("the lead's prompt has no %q:\n%s", want, (*woken)[0])
		}
	}
	incident, err := loadTeamIncident("payments", "i-1")
	if err != nil || incident.Status != "open" || incident.Thread != "1700000000.000001" || incident.Job != "backfill" {
		t.Fatalf("%+v %v", incident, err)
	}
	if views := teamJobsReport("payments", teamSlotProduction, now.Add(3*time.Hour)); views[0].Incident != "i-1" {
		t.Fatalf("%+v", views)
	}

	// It moves again: hi says so in the thread and closes it.
	writeTeamTestJob(t, teamSlotProduction, teamJob{Job: "backfill", State: "running", Done: 610, Total: 533386, Unit: "releases", At: now.Add(4 * time.Hour)})
	check(now.Add(4 * time.Hour))
	if incident, _ := loadTeamIncident("payments", "i-1"); incident.Status != "closed" || len(channel.calls) != 2 ||
		!strings.Contains(channel.calls[1], "postIn C07PAYMENTS 1700000000.000001 :white_check_mark: backfill is running again: 610 of 533,386 releases.") {
		t.Fatal(incident.Status, channel.calls)
	}

	// Stalled: running, but its count stays put past stall_after.
	for i := 5; i <= 6; i++ {
		writeTeamTestJob(t, teamSlotProduction, teamJob{Job: "backfill", State: "running", Done: 610, At: now.Add(time.Duration(i) * time.Hour)})
		check(now.Add(time.Duration(i) * time.Hour))
	}
	if len(*woken) != 2 || !strings.Contains((*woken)[1], "hasn't moved in over 30 minutes") {
		t.Fatal(*woken)
	}
	// Then silent within the same incident: noted, not posted again.
	check(now.Add(8 * time.Hour))
	if len(*woken) != 2 {
		t.Fatal(*woken)
	}
	incident, _ = loadTeamIncident("payments", "i-2")
	if last := incident.Events[len(incident.Events)-1]; last.Action != "changed" || !strings.Contains(last.Result, "written nothing since") {
		t.Fatalf("%+v", incident.Events)
	}

	// A job silent for a month is dropped.
	check(now.Add(31 * 24 * time.Hour))
	if views := teamJobsReport("payments", teamSlotProduction, now.Add(31*24*time.Hour)); len(views) != 0 || fileExists(filepath.Join(teamSlotJobsDir("payments", teamSlotProduction), "backfill.json")) {
		t.Fatalf("%+v", views)
	}
}

func TestTeamJobsOnThePreview(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	channel := &fakeTeamChannel{}
	b.channel = channel
	woken := teamTestWakes(b.teamBroker)
	now := time.Now()
	b.started = now.Add(-time.Hour)
	// Dev on the preview: hi team jobs --preview shows it, nobody is told.
	os.MkdirAll(filepath.Join(teamDir("payments"), "run"), 0o700)
	saveTeamSlot("payments", teamPreview{Slot: teamSlotPreview, Source: "main", Container: "app-preview"})
	writeTeamTestJob(t, teamSlotPreview, teamJob{Job: "import", State: "failed", At: now, Error: "boom"})
	if err := b.checkJobs(now); err != nil || len(channel.calls) != 0 || len(*woken) != 0 {
		t.Fatal(err, channel.calls, *woken)
	}
	// A task's idea on the preview: its thread hears.
	yes := &teamApproval{TS: "1792254800.000100", Thread: "1792254766.636789"}
	saveTeamTask("payments", teamTask{ID: "t-1", Role: "coder", Kind: "task", Title: "Import", Status: "done", Approval: yes})
	saveTeamSlot("payments", teamPreview{Slot: teamSlotPreview, Source: "t-1", Container: "app-preview"})
	writeTeamTestJob(t, teamSlotPreview, teamJob{Job: "import", State: "failed", At: now.Add(time.Minute), Error: "boom again"})
	if err := b.checkJobs(now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if len(channel.calls) != 1 || !strings.HasPrefix(channel.calls[0], "postIn C07PAYMENTS 1792254766.636789 :rotating_light: *Incident i-1 · the preview's job import*") ||
		len(*woken) != 1 || !strings.Contains((*woken)[0], "slack:C07PAYMENTS:1792254766.636789") {
		t.Fatal(channel.calls, *woken)
	}
}

func TestTeamAppStoppedIsAnIncident(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	channel := &fakeTeamChannel{}
	b.channel = channel
	woken := teamTestWakes(b.teamBroker)
	state := teamPreview{Slot: teamSlotProduction, Container: "app-prod-1"}
	finished := b.started.Add(-time.Hour)
	b.appExit = func(string) (int, time.Time, bool) { return 137, finished, true }
	b.appLogs = func(string) string { return "out of memory" }
	// Stopped before the team started, as at a reboot: not an incident.
	b.appStopped(state)
	if len(channel.calls) != 0 {
		t.Fatal(channel.calls)
	}
	finished = b.started.Add(time.Hour)
	writeTeamTestJob(t, teamSlotProduction, teamJob{Job: "backfill", State: "running", At: time.Now()})
	b.appStopped(state)
	b.appStopped(state) // the same stop, seen again
	if len(channel.calls) != 1 || !strings.Contains(channel.calls[0], "exit code 137 (killed, often for memory); it was running backfill") || len(*woken) != 1 || !strings.Contains((*woken)[0], "out of memory") {
		t.Fatal(channel.calls, *woken)
	}
}

func TestTeamTaskFailureIsAnIncident(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	channel := &fakeTeamChannel{}
	b.channel = channel
	woken := teamTestWakes(b.teamBroker)
	run := b.run
	failing := true
	b.run = func(args ...string) ([]byte, error) {
		if args[0] == "agent" && args[1] == "wait" && failing {
			return json.Marshal(agentReport{Name: args[2], Status: "failed", Report: json.RawMessage(`{"status": "blocked", "summary": "The tests need a database that isn't there.", "follow_ups": ["Alert: production's export returns 500"]}`)})
		}
		return run(args...)
	}
	saveTeamTask("payments", teamTask{ID: "t-0", Kind: "task", Title: "Old", Status: "merged", Thread: "1792254766.636789", Small: true})
	task := teamTestSmall(t, b, teamRequest{Brief: "Add an export", In: ""})
	if task.Status != "failed" || task.Incident == "" {
		t.Fatalf("%+v", task)
	}
	incident, err := loadTeamIncident("payments", task.Incident)
	if err != nil || incident.Kind != "task" || !strings.Contains(incident.Title, "database that isn't there") {
		t.Fatalf("%+v %v", incident, err)
	}
	// A small fix without a thread: no alert in the channel for the task,
	// but the member's Alert: line is one of its own.
	var alerts []teamIncident
	for _, incident := range loadTeamIncidents("payments") {
		if incident.Kind == "alert" {
			alerts = append(alerts, incident)
		}
	}
	if len(alerts) != 1 || !strings.Contains(alerts[0].Detail, "the coder, in "+task.ID) || len(*woken) != 1 {
		t.Fatal(alerts, *woken)
	}
	for _, call := range channel.calls {
		if strings.Contains(call, "Incident "+task.Incident) {
			t.Fatal("a task without a thread was alerted in the channel:", call)
		}
	}
	// The lead's wait and hi's wake-up say what to do with it.
	var out strings.Builder
	printTeamTask(task, &out)
	if !strings.Contains(out.String(), "Incident: hi opened incident "+task.Incident) || !strings.Contains(out.String(), "--incident "+task.Incident) {
		t.Fatal(out.String())
	}
	if prompt := teamHandoffPrompt(task, task); !strings.Contains(prompt, "hi opened incident "+task.Incident) {
		t.Fatal(prompt)
	}

	// The lead re-briefs it for the incident: logged and posted; it fails
	// again within the same incident, then works and closes it.
	if _, err := b.startTask(teamRequest{Role: "coder", Brief: "Add an export, with a fake database", Small: true, Incident: task.Incident}); err == nil || !strings.Contains(err.Error(), "--because") {
		t.Fatal(err)
	}
	retry := teamTestSmall(t, b, teamRequest{Brief: "Add an export, with a fake database", On: task.ID, Incident: task.Incident, Because: "the tests need no real database"})
	if retry.ForIncident != task.Incident || retry.Incident != task.Incident {
		t.Fatalf("%+v", retry)
	}
	failing = false
	again := teamTestSmall(t, b, teamRequest{Brief: "Same, smaller", On: task.ID, Incident: task.Incident, Because: "split it"})
	incident, _ = loadTeamIncident("payments", task.Incident)
	if again.Status != "done" || incident.Status != "closed" {
		t.Fatalf("%+v %+v", again, incident)
	}
	var actions []string
	for _, event := range incident.Events {
		actions = append(actions, event.Who+":"+event.Action)
	}
	if strings.Join(actions, " ") != "hi:opened lead:task hi:changed lead:task hi:recovered" {
		t.Fatal(actions)
	}
	if !strings.Contains(incident.Events[1].Because, "the tests need no real database") || !strings.Contains(incident.Events[1].Command, "hi team task coder → "+retry.ID) {
		t.Fatalf("%+v", incident.Events[1])
	}
	// Records can't name an incident that's closed.
	if _, err := b.startTask(teamRequest{Role: "coder", Brief: "More", Small: true, Incident: task.Incident, Because: "x"}); err == nil || !strings.Contains(err.Error(), "is closed") {
		t.Fatal(err)
	}
}

func TestTeamIncidentLeadActions(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	channel := &fakeTeamChannel{}
	b.channel = channel
	teamTestWakes(b.teamBroker)
	incident, err := b.openIncident(teamIncident{Kind: "job", Slot: teamSlotProduction, Job: "backfill", Title: "backfill stopped"}, "backfill stopped.", "", teamIncidentPrompt)
	if err != nil {
		t.Fatal(err)
	}

	// Restart: once on its own, a second time with a person's yes.
	if _, err := b.restartApp(teamJobsRequest{Incident: incident.ID}); err == nil || !strings.Contains(err.Error(), "--because") {
		t.Fatal(err)
	}
	if _, err := b.production(0); err == nil {
		t.Fatal("production without a release")
	}
	os.WriteFile(filepath.Join(b.repo, "Makefile"), []byte("run:\n\t./serve\n"), 0o644)
	exec.Command("git", "-C", b.repo, "add", "-A").Run()
	exec.Command("git", "-C", b.repo, "commit", "-q", "-m", "make run").Run()
	if _, err := b.preview(teamPreviewRequest{}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.restartApp(teamJobsRequest{Preview: true, Incident: incident.ID, Because: "the job went silent while the box ran"}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.restartApp(teamJobsRequest{Preview: true, Incident: incident.ID, Because: "again"}); err == nil || !strings.Contains(err.Error(), "needs a person's yes") {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(channel.calls, "\n"), ":gear: *The lead, i-1:* `hi team jobs restart --preview`: the preview runs in a new box. Why: the job went silent while the box ran.") {
		t.Fatal(channel.calls)
	}

	// A question waits; a day later with no answer, hi reminds the owners once.
	if _, err := b.incidentAction("ask", teamIncidentRequest{ID: incident.ID, Text: "Tag it invalid and move on?"}); err != nil {
		t.Fatal(err)
	}
	asked, _ := loadTeamIncident("payments", incident.ID)
	b.remindIncidents(asked.Asked.Add(time.Hour))
	b.remindIncidents(asked.Asked.Add(25 * time.Hour))
	b.remindIncidents(asked.Asked.Add(50 * time.Hour))
	reminders := 0
	for _, call := range channel.calls {
		if strings.Contains(call, "<@U07OWNER1>, the lead's question above has waited a day") {
			reminders++
		}
	}
	if reminders != 1 {
		t.Fatal(channel.calls)
	}

	// A rule needs the people's message.
	if _, err := b.incidentAction("rule", teamIncidentRequest{Job: "backfill", Text: "No text and no PDF: tag it invalid and move on."}); err == nil || !strings.Contains(err.Error(), "--approval") {
		t.Fatal(err)
	}
	yes := teamTestYes(t, "U07OWNER1", 1)
	if _, err := b.incidentAction("rule", teamIncidentRequest{Job: "backfill", Text: "No text and no PDF: tag it invalid and move on.", Approval: yes, ID: incident.ID}); err != nil {
		t.Fatal(err)
	}
	rules := loadTeamRules("payments")
	if len(rules) != 1 || rules[0].Approval.Name != "U07OWNER1" || rules[0].Incident != incident.ID {
		t.Fatalf("%+v", rules)
	}
	if prompt := teamIncidentPrompt(testTeamConfig(), teamIncident{ID: "i-9", Job: "backfill", Title: "x"}); !strings.Contains(prompt, "- No text and no PDF: tag it invalid and move on. (U07OWNER1,") {
		t.Fatal(prompt)
	}
	var out strings.Builder
	printTeamRules(rules, &out)
	if !strings.Contains(out.String(), "backfill:\n  - No text and no PDF") {
		t.Fatal(out.String())
	}

	// A note is the lead's own; closing is posted.
	b.incidentAction("note", teamIncidentRequest{ID: incident.ID, Text: "The release page was empty in 2009."})
	if _, err := b.incidentAction("close", teamIncidentRequest{ID: incident.ID}); err == nil {
		t.Fatal("closed without a why")
	}
	if _, err := b.incidentAction("close", teamIncidentRequest{ID: incident.ID, Because: "the rule handles it; the fix is t-3"}); err != nil {
		t.Fatal(err)
	}
	closed, _ := loadTeamIncident("payments", incident.ID)
	out.Reset()
	printTeamIncident(closed, &out)
	for _, want := range []string{"i-1 · closed", "note: (the lead's own note) The release page was empty in 2009.", "reminder: reminded the owners", "U07OWNER1 said yes."} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("no %q in\n%s", want, out.String())
		}
	}
}

func TestTeamAlertLimits(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	b.channel = &fakeTeamChannel{}
	woken := teamTestWakes(b.teamBroker)
	if _, err := b.alert(" ", "the lead"); err == nil {
		t.Fatal("an empty alert")
	}
	if _, err := b.alert("production's export returns 500", "the lead"); err != nil || len(*woken) != 1 {
		t.Fatal(err, *woken)
	}
	if _, err := b.alert("and again", "the lead"); err == nil || !strings.Contains(err.Error(), "less than a minute ago") {
		t.Fatal(err)
	}
	b.updateIncident("i-1", func(i *teamIncident) { i.Opened = i.Opened.Add(-2 * time.Minute) })
	for i := 2; i <= teamAlertsPerDay; i++ {
		saveTeamIncident("payments", teamIncident{ID: fmt.Sprintf("i-%d", i+10), Kind: "alert", Status: "closed", Opened: time.Now().Add(-time.Hour)})
	}
	if _, err := b.alert("more", "the lead"); err == nil || !strings.Contains(err.Error(), "most hi sends") {
		t.Fatal(err)
	}
}

func TestTeamIncidentCommands(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	b.channel = &fakeTeamChannel{}
	teamTestWakes(b.teamBroker)
	writeTeamTestJob(t, teamSlotProduction, teamJob{Job: "backfill", State: "running", Done: 8272, Total: 533386, Unit: "releases", At: time.Now()})
	b.alert("the export is slow", "the lead")

	// On the machine, from the team's folder.
	var out strings.Builder
	if err := teamJobsCommand([]string{"payments"}, &out); err != nil || !strings.Contains(out.String(), "backfill · running · 8,272 of 533,386 releases") {
		t.Fatal(err, out.String())
	}
	out.Reset()
	if err := teamIncidentsCommand([]string{"payments"}, &out); err != nil || !strings.Contains(out.String(), "i-1 · open") {
		t.Fatal(err, out.String())
	}
	out.Reset()
	if err := teamIncidentsCommand([]string{"payments", "i-1"}, &out); err != nil || !strings.Contains(out.String(), "Timeline:") {
		t.Fatal(err, out.String())
	}
	if err := teamIncidentsCommand([]string{"payments", "i-9"}, &out); err == nil {
		t.Fatal("no i-9")
	}

	// In the lead's box, through its socket.
	socket := filepath.Join(t.TempDir(), "s")
	serve, err := b.listen(t.Context(), socket)
	if err != nil {
		t.Fatal(err)
	}
	go serve()
	t.Setenv(teamSocketEnv, socket)
	lead := func(command string, args ...string) (string, error) {
		var out strings.Builder
		err := runTeamLeadCommand(command, args, strings.NewReader(""), &out)
		return out.String(), err
	}
	if text, err := lead("jobs"); err != nil || !strings.Contains(text, "Jobs in production:") {
		t.Fatal(text, err)
	}
	if text, err := lead("incidents", "note", "i-1", "Looked at the logs."); err != nil || !strings.Contains(text, "Noted in i-1") {
		t.Fatal(text, err)
	}
	if text, err := lead("incidents", "close", "i-1", "--because", "it was a slow query, fixed by t-2"); err != nil || !strings.Contains(text, "Closed i-1") {
		t.Fatal(text, err)
	}
	if text, err := lead("incidents", "i-1"); err != nil || !strings.Contains(text, "i-1 · closed") {
		t.Fatal(text, err)
	}
	if _, err := lead("jobs", "forget", "backfill"); err == nil || !strings.Contains(err.Error(), "--because") {
		t.Fatal(err)
	}
	if text, err := lead("jobs", "forget", "backfill", "--because", "the release took it out"); err != nil || !strings.Contains(text, "Forgot backfill") {
		t.Fatal(text, err)
	}
	if text, err := lead("alert", "production is down"); err == nil || !strings.Contains(err.Error(), "less than a minute") {
		t.Fatal(text, err)
	}
}

func TestTeamThousands(t *testing.T) {
	for n, want := range map[int64]string{0: "0", 999: "999", 1000: "1,000", 533386: "533,386", 1234567: "1,234,567", -4200: "-4,200"} {
		if got := teamThousands(n); got != want {
			t.Errorf("%d: %s", n, got)
		}
	}
}

func TestTeamParseFinishedAt(t *testing.T) {
	want := time.Date(2026, 10, 19, 7, 19, 10, 12635831, time.UTC)
	for _, out := range []string{"2026-10-19 07:19:10.012635831 +0000 UTC", "2026-10-19T07:19:10.012635831Z"} {
		if got, ok := teamParseFinishedAt(strings.Fields(out)); !ok || !got.Equal(want) {
			t.Errorf("%s: %v %v", out, got, ok)
		}
	}
	if _, ok := teamParseFinishedAt([]string{"0001-01-01"}); ok {
		t.Error("parsed half a time")
	}
}

func TestTeamJobsCheckUnreported(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	channel := &fakeTeamChannel{}
	b.channel = channel
	woken := teamTestWakes(b.teamBroker)
	check := func() {
		t.Helper()
		if err := b.checkUnreported(); err != nil {
			t.Fatal(err)
		}
	}
	// A team with no merged work has nothing written before the rule.
	check()
	if len(channel.calls) != 0 || len(*woken) != 0 || !fileExists(teamJobsCheckedPath("payments")) {
		t.Fatal(channel.calls, *woken)
	}

	// Merged work and no job reported: the lead is asked once.
	os.Remove(teamJobsCheckedPath("payments"))
	saveTeamTask("payments", teamTask{ID: "t-1", Role: "coder", Kind: "task", Title: "History worker", Status: "merged"})
	check()
	check()
	if len(channel.calls) != 1 || len(*woken) != 1 || !strings.HasPrefix((*woken)[0], "hi-jobs-check ") {
		t.Fatal(channel.calls, *woken)
	}
	if !strings.Contains((*woken)[0], "--small") || !strings.Contains((*woken)[0], "blocked or failed") {
		t.Fatal((*woken)[0])
	}

	// An app whose work already reports isn't asked about.
	os.Remove(teamJobsCheckedPath("payments"))
	writeTeamTestJob(t, teamSlotProduction, teamJob{Job: "backfill", State: "running", Done: 1, At: time.Now()})
	check()
	if len(*woken) != 1 {
		t.Fatal(*woken)
	}
}

// Starting the team cuts the app's model calls: a job that trips on it
// and moves again within minutes raises nothing, and one that doesn't is
// told with the start.
func TestTeamJobsSettleAfterStart(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	channel := &fakeTeamChannel{}
	b.channel = channel
	woken := teamTestWakes(b.teamBroker)
	start := time.Date(2026, 10, 19, 12, 29, 8, 0, time.UTC)
	b.started = start
	os.MkdirAll(filepath.Join(teamDir("payments"), "run"), 0o700)
	saveTeamSlot("payments", teamPreview{Slot: teamSlotProduction, Container: "app-prod"})
	check := func(at time.Time) {
		t.Helper()
		if err := b.checkJobs(at); err != nil {
			t.Fatal(err)
		}
	}
	writeTeamTestJob(t, teamSlotProduction, teamJob{Job: "history", State: "failed", Done: 37, At: start.Add(14 * time.Second), Error: "ECONNRESET"})
	check(start.Add(time.Minute))
	writeTeamTestJob(t, teamSlotProduction, teamJob{Job: "history", State: "running", Done: 38, At: start.Add(time.Minute)})
	check(start.Add(2 * time.Minute))
	if len(channel.calls) != 0 || len(*woken) != 0 {
		t.Fatal(channel.calls, *woken)
	}

	// Still failed once the team has settled: an incident, with the start.
	writeTeamTestJob(t, teamSlotProduction, teamJob{Job: "history", State: "failed", Done: 40, At: start.Add(3 * time.Minute), Error: "ECONNRESET"})
	check(start.Add(5 * time.Minute))
	if len(channel.calls) != 0 {
		t.Fatal(channel.calls)
	}
	check(start.Add(11 * time.Minute))
	if len(channel.calls) != 1 || !strings.Contains(channel.calls[0], "hi started the team at 12:29 UTC") || len(*woken) != 1 {
		t.Fatal(channel.calls, *woken)
	}
}

// A count that went down started over: the rate counts from there, never
// below zero (cartman's volatility-backfill showed -73,826 a day).
func TestTeamJobRateAfterACountStartsOver(t *testing.T) {
	now := time.Now()
	hours := func(n int) time.Time { return now.Add(-time.Duration(n) * time.Hour) }
	watch := &teamJobWatch{Done: 0, Samples: []teamJobSample{{At: hours(36), Done: 112221}, {At: hours(35), Done: 0}, {At: hours(1), Done: 0}}}
	if rate, ok := teamJobRate(watch, now); !ok || rate != 0 {
		t.Fatal(rate, ok)
	}
	watch = &teamJobWatch{Done: 1300, Samples: []teamJobSample{{At: hours(30), Done: 9000}, {At: hours(24), Done: 100}, {At: hours(12), Done: 700}}}
	if rate, ok := teamJobRate(watch, now); !ok || rate != 1200 {
		t.Fatal(rate, ok)
	}
	// Down since the last sample: too soon to say.
	watch = &teamJobWatch{Done: 50, Samples: []teamJobSample{{At: hours(5), Done: 100}, {At: hours(2), Done: 400}}}
	if _, ok := teamJobRate(watch, now); ok {
		t.Fatal("a rate across a count that went down")
	}
	// A count that only grows keeps its week.
	watch = &teamJobWatch{Done: 4800, Samples: []teamJobSample{{At: hours(48), Done: 0}, {At: hours(24), Done: 2400}}}
	if rate, ok := teamJobRate(watch, now); !ok || rate != 2400 {
		t.Fatal(rate, ok)
	}
}

func TestTeamIncidentStandingTellsTheOwners(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	channel := &fakeTeamChannel{}
	b.channel = channel
	teamTestWakes(b.teamBroker)
	stands, err := b.openIncident(teamIncident{Kind: "job", Slot: teamSlotProduction, Job: "analysis", Title: "analysis went silent", Opened: time.Now().UTC()}, "analysis went silent.", "", teamIncidentPrompt)
	if err != nil {
		t.Fatal(err)
	}
	preview, _ := b.openIncident(teamIncident{Kind: "job", Slot: teamSlotPreview, Job: "analysis", Title: "the preview's analysis went silent", Opened: time.Now().UTC()}, "x", "", teamIncidentPrompt)
	asked, _ := b.openIncident(teamIncident{Kind: "app", Slot: teamSlotProduction, Title: "the app doesn't answer", Opened: time.Now().UTC()}, "x", "", teamIncidentPrompt)
	if _, err := b.incidentAction("ask", teamIncidentRequest{ID: asked.ID, Text: "Restart it again?"}); err != nil {
		t.Fatal(err)
	}
	if stands.Thread == "" || preview.Thread == "" {
		t.Fatal("no threads")
	}
	count := func() int {
		n := 0
		for _, call := range channel.calls {
			if strings.Contains(call, "has stood for") {
				n++
			}
		}
		return n
	}
	b.remindIncidents(stands.Opened.Add(3 * time.Hour))
	if count() != 0 {
		t.Fatal(channel.calls)
	}
	b.remindIncidents(stands.Opened.Add(5 * time.Hour))
	b.remindIncidents(stands.Opened.Add(12 * time.Hour))
	if count() != 1 || !strings.Contains(strings.Join(channel.calls, "\n"), "<@U07OWNER1>, "+stands.ID+" (production's job analysis) has stood for 5h, and nobody was asked") {
		t.Fatal(channel.calls)
	}
	if after, _ := loadTeamIncident("payments", stands.ID); after.Events[len(after.Events)-1].Action != "reminder" {
		t.Fatalf("%+v", after.Events)
	}
	if !strings.Contains(teamIncidentPrompt(testTeamConfig(), stands), "Waiting is a choice too") {
		t.Fatal("the lead isn't told")
	}
}
