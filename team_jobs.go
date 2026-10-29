package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// An app often does long work of its own: a backfill, an import, a nightly
// recalculation. To hi a worker that stopped and one that is idle look the
// same, so the app says what each job is doing, in a file per job, and the
// broker watches the files without a model: a job that fails, stops
// moving, or stops writing opens an incident (team_incidents.go), once.
//
// Spec: docs/specs/approved/hi_team_jobs.md.

const (
	// teamAppJobsDir is where an app's box writes its jobs ($HI_JOBS_DIR).
	teamAppJobsDir = "/jobs"
	// teamJobStallDefault is how long a running job's count may stay the
	// same, or its file go unwritten, before it counts as stuck.
	teamJobStallDefault = 30 * time.Minute
	// teamJobKeep is how long a job's file and samples are kept after it
	// last wrote.
	teamJobKeep = 30 * 24 * time.Hour
	// teamJobSampleEvery is how often the broker notes a job's count, for
	// its rate.
	teamJobSampleEvery = time.Hour
	// teamJobSettle is how long after the team starts hi holds a job's
	// incident: starting the team (a new hi) cuts the app's model calls
	// through hi and restarts its boxes, and a job that trips on that
	// moves again within minutes.
	teamJobSettle  = 10 * time.Minute
	teamJobsMax    = 64      // job files read per slot
	teamJobFileMax = 8 << 10 // bytes read from a job's file
)

// teamJobsRule is in the dev box's instructions: how the app's long work
// reports, and what it does with an item it can't process.
const teamJobsRule = "Long work in the app: work that can outlive a request (a loop over many records, a queue, a backfill, an import, a scheduled recalculation) reports as a job, so hi notices when it stops. " +
	"While it runs, at least every few minutes, it writes $HI_JOBS_DIR/<job>.json (to a temporary name, then renamed) with " +
	`{"state": "running|blocked|failed|done", "done": <count>, "total": <count>, "unit": "<word>", "stall_after": "30m", "at": "<RFC 3339 time>", "item": "<what it is on>", "error": "<why, for people>"}` +
	", or runs \"$HI\" team job <job> --done <count> (also --total, --unit, --item, --error, --stall-after, and --running, --blocked, --failed, --finished). " +
	"stall_after is how long its count may stay the same before hi alerts: set it to what is normal for the job. Without $HI_JOBS_DIR, as in the dev box and the tests, it writes nothing. " +
	"When the job meets an item it can't process, it stops with state blocked, the item, and why, unless the job's spec gives a rule for that case: never skip, drop, or make up an item on its own, since later values may depend on it. " +
	"A job that stops on its own for any other reason (too many failures in a row, a limit it reached) writes failed or blocked with why before it stops: a job that only stops looks to hi like one that is idle. " +
	"Long work never runs in the thread that answers requests: give it a worker thread or a process of its own, and keep each write to the database short (commit in batches), so a request never waits on it. " +
	"Production must answer and its work must move: never meet one by switching the other off. Work the app holds back on purpose (off by a setting, or paused until a person says) writes blocked, with why and how to resume it, so hi asks the people instead of taking it for dead. " +
	"Long work your change touches that doesn't report yet is made to report too. Put the job's name and its rules in its spec.\n" +
	teamWorkersRule

var teamJobName = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,62}$`)

var teamJobStates = map[string]bool{"running": true, "blocked": true, "failed": true, "done": true}

// teamJob is what an app writes in $HI_JOBS_DIR/<job>.json.
type teamJob struct {
	Job        string    `json:"job"`
	State      string    `json:"state"`
	Done       int64     `json:"done"`
	Total      int64     `json:"total,omitempty"`
	Unit       string    `json:"unit,omitempty"`
	StallAfter string    `json:"stall_after,omitempty"`
	At         time.Time `json:"at"`
	Item       string    `json:"item,omitempty"`
	Error      string    `json:"error,omitempty"`
	Note       string    `json:"note,omitempty"`
}

// stall is how long the job may sit still: its stall_after, or the default.
func (job teamJob) stall() time.Duration {
	if d, err := time.ParseDuration(job.StallAfter); err == nil && d >= time.Minute {
		return d
	}
	return teamJobStallDefault
}

func teamSlotJobsDir(team, slot string) string {
	return filepath.Join(teamDir(team), "run", slot+"-jobs")
}

// readTeamJobs reads a slot's job files, skipping what isn't one.
func readTeamJobs(team, slot string) []teamJob {
	entries, _ := os.ReadDir(teamSlotJobsDir(team, slot))
	var jobs []teamJob
	for _, entry := range entries {
		name, ok := strings.CutSuffix(entry.Name(), ".json")
		if !ok || !entry.Type().IsRegular() || !teamJobName.MatchString(name) || len(jobs) >= teamJobsMax {
			continue
		}
		file, err := os.Open(filepath.Join(teamSlotJobsDir(team, slot), entry.Name()))
		if err != nil {
			continue
		}
		data, _ := io.ReadAll(io.LimitReader(file, teamJobFileMax))
		file.Close()
		var job teamJob
		if json.Unmarshal(data, &job) != nil || !teamJobStates[job.State] || job.At.IsZero() {
			continue
		}
		job.Job = name
		job.Error, job.Note, job.Item, job.Unit = teamClipText(job.Error, 500), teamClipText(job.Note, 300), teamClipText(job.Item, 200), teamClipText(job.Unit, 40)
		jobs = append(jobs, job)
	}
	sort.Slice(jobs, func(i, j int) bool { return jobs[i].Job < jobs[j].Job })
	return jobs
}

// teamJobsState is run/jobs.json: what the broker saw of each job.
type teamJobsState struct {
	Jobs map[string]*teamJobWatch `json:"jobs,omitempty"` // by slot/job
	// AppStopped is the last stop of production's box that was alerted.
	AppStopped string `json:"app_stopped,omitempty"`
	// Silent is since when production's app hasn't answered, and
	// SilentIncident the incident hi opened for it (team_health.go).
	Silent         time.Time `json:"app_silent,omitzero"`
	SilentIncident string    `json:"app_silent_incident,omitempty"`
	// WorkersWait is since when production's box has waited for its
	// workers' lock, and WorkersIncident the incident for it
	// (team_workers.go).
	WorkersWait     time.Time `json:"workers_wait,omitzero"`
	WorkersIncident string    `json:"workers_incident,omitempty"`
}

type teamJobWatch struct {
	Slot string `json:"slot"`
	Job  string `json:"job"`
	Done int64  `json:"done"`
	// Moved is when Done last changed, or when hi first saw the job.
	Moved   time.Time       `json:"moved"`
	Samples []teamJobSample `json:"samples,omitempty"`
	Alerted string          `json:"alerted,omitempty"` // the condition last alerted
	Open    string          `json:"incident,omitempty"`
	Last    teamJob         `json:"last"`
}

type teamJobSample struct {
	At   time.Time `json:"at"`
	Done int64     `json:"done"`
}

func teamJobsStatePath(team string) string { return filepath.Join(teamDir(team), "run", "jobs.json") }

func loadTeamJobsState(team string) teamJobsState {
	var state teamJobsState
	if data, err := os.ReadFile(teamJobsStatePath(team)); err == nil {
		json.Unmarshal(data, &state)
	}
	if state.Jobs == nil {
		state.Jobs = map[string]*teamJobWatch{}
	}
	return state
}

func saveTeamJobsState(team string, state teamJobsState) error {
	if err := os.MkdirAll(filepath.Dir(teamJobsStatePath(team)), 0o700); err != nil {
		return err
	}
	data, _ := json.MarshalIndent(state, "", "  ")
	return writeFileAtomic(teamJobsStatePath(team), append(data, '\n'), 0o600)
}

// teamJobCondition is what is wrong with a job, if anything, and a key that
// changes when it is wrong anew.
func teamJobCondition(job teamJob, watch *teamJobWatch, now time.Time) (condition, key string) {
	switch {
	case job.State == "blocked" || job.State == "failed":
		return job.State, job.State + "@" + job.At.UTC().Format(time.RFC3339Nano)
	case job.State != "running":
		return "", ""
	case now.Sub(job.At) > job.stall():
		return "silent", "silent@" + job.At.UTC().Format(time.RFC3339Nano)
	case now.Sub(watch.Moved) > job.stall():
		return "stalled", "stalled@" + strconv.FormatInt(job.Done, 10)
	}
	return "", ""
}

// teamJobRate is a job's count per day, from the samples of the last week,
// and whether there are enough to say. A count that went down started
// over (a new run, or a count the app reset), so the rate counts from
// there: across it, it would come out below zero.
func teamJobRate(watch *teamJobWatch, now time.Time) (float64, bool) {
	if watch == nil || len(watch.Samples) == 0 {
		return 0, false
	}
	var first, last *teamJobSample
	for i := range watch.Samples {
		sample := &watch.Samples[i]
		if now.Sub(sample.At) > 7*24*time.Hour {
			continue
		}
		if first == nil || sample.Done < last.Done {
			first = sample
		}
		last = sample
	}
	if first == nil || watch.Done < last.Done {
		return 0, false
	}
	span := now.Sub(first.At)
	if span < teamJobSampleEvery {
		return 0, false
	}
	return float64(watch.Done-first.Done) / span.Hours() * 24, true
}

// checkJobs reads the jobs of production and the preview, notes their
// counts, and opens an incident for a job that went wrong, or closes one
// for a job that moves again. Nothing here calls a model.
func (b *teamBroker) checkJobs(now time.Time) error {
	if b.stopped() {
		return nil // the app is paused on purpose: its jobs stand still
	}
	b.jobsMu.Lock()
	defer b.jobsMu.Unlock()
	state := loadTeamJobsState(b.team)
	var failed []string
	for _, slot := range []string{teamSlotProduction, teamSlotPreview} {
		for _, job := range readTeamJobs(b.team, slot) {
			id := slot + "/" + job.Job
			if now.Sub(job.At) > teamJobKeep {
				os.Remove(filepath.Join(teamSlotJobsDir(b.team, slot), job.Job+".json"))
				delete(state.Jobs, id)
				continue
			}
			watch := state.Jobs[id]
			if watch == nil {
				watch = &teamJobWatch{Slot: slot, Job: job.Job, Done: job.Done, Moved: now}
				state.Jobs[id] = watch
			}
			if job.Done != watch.Done {
				watch.Done, watch.Moved = job.Done, now
			}
			watch.Last = job
			if n := len(watch.Samples); n == 0 || now.Sub(watch.Samples[n-1].At) >= teamJobSampleEvery {
				watch.Samples = append(watch.Samples, teamJobSample{At: now, Done: job.Done})
			}
			for len(watch.Samples) > 0 && now.Sub(watch.Samples[0].At) > teamJobKeep {
				watch.Samples = watch.Samples[1:]
			}
			condition, key := teamJobCondition(job, watch, now)
			open := watch.Open != "" && b.incidentOpen(watch.Open)
			switch {
			case condition == "":
				watch.Alerted = ""
				if open {
					b.recovered(watch.Open, teamJobRecoveredText(job))
				}
				watch.Open = ""
			case key == watch.Alerted:
			case open:
				// Worse, or stuck anew, within the same incident.
				watch.Alerted = key
				b.addIncidentEvent(watch.Open, teamIncidentEvent{Who: "hi", Action: "changed", Result: teamJobConditionText(job, condition)})
			case !b.started.IsZero() && now.Sub(b.started) < teamJobSettle:
				// Held: asked again next minute, once the team has settled.
			default:
				watch.Alerted = key
				incident, err := b.openJobIncident(slot, job, condition, now)
				if err != nil {
					failed = append(failed, fmt.Sprintf("%s's job %s: %v", slot, job.Job, err))
					continue
				}
				watch.Open = incident.ID
			}
		}
	}
	for id, watch := range state.Jobs {
		if now.Sub(watch.Last.At) > teamJobKeep {
			delete(state.Jobs, id)
		}
	}
	if err := saveTeamJobsState(b.team, state); err != nil {
		return err
	}
	if len(failed) > 0 {
		return errors.New(strings.Join(failed, "; "))
	}
	return nil
}

// watchJobs checks the jobs every minute, and the incidents waiting for a
// person (team_incidents.go), until done.
func (b *teamBroker) watchJobs(done <-chan struct{}, stderr io.Writer) {
	for {
		if err := b.checkJobs(time.Now()); err != nil {
			fmt.Fprintf(stderr, "hi: watching the app's jobs: %v\n", err)
		}
		if err := b.remindIncidents(time.Now()); err != nil {
			fmt.Fprintf(stderr, "hi: reminding about incidents: %v\n", err)
		}
		if err := b.checkUnreported(); err != nil {
			fmt.Fprintf(stderr, "hi: asking the lead about the app's long work: %v\n", err)
		}
		if err := b.checkAnswers(time.Now()); err != nil {
			fmt.Fprintf(stderr, "hi: asking whether production's app answers: %v\n", err)
		}
		if err := b.checkWorkers(time.Now()); err != nil {
			fmt.Fprintf(stderr, "hi: looking at who runs the app's workers: %v\n", err)
		}
		if err := b.checkWorkersRule(); err != nil {
			fmt.Fprintf(stderr, "hi: asking the lead about the app's workers: %v\n", err)
		}
		select {
		case <-done:
			return
		case <-time.After(time.Minute):
		}
	}
}

// An app written before hi watched jobs (v0.93.0) may have long work that
// doesn't report, and hi can't see it stop. Once per team, when its dev
// has merged work and no job has ever reported, hi asks the lead to look
// through the app for such work and brief the fix. A team with no merged
// work yet needs no check: every brief and review since carries the rule.

func teamJobsCheckedPath(team string) string {
	return filepath.Join(teamDir(team), "run", "jobs-checked")
}

// checkUnreported asks the lead, once, to find the app's long work that
// hi can't see.
func (b *teamBroker) checkUnreported() error {
	path := teamJobsCheckedPath(b.team)
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	merged := false
	for _, task := range loadTeamTasks(b.team) {
		merged = merged || task.Status == "merged"
	}
	reported := len(readTeamJobs(b.team, "production")) > 0 || len(readTeamJobs(b.team, "preview")) > 0
	if merged && !reported {
		if b.wakeLead == nil {
			return nil // asked once the lead is there
		}
		config, err := loadTeam(b.team)
		if err != nil {
			return err
		}
		deliver := "slack:" + config.opsChannel()
		if b.channel != nil {
			ts, err := b.channel.post(config.opsChannel(), ":mag: *hi now watches the app's long work*, such as a backfill or an import, and says when it stops. Work written before can't be seen yet: the lead checks this app's and says here what needs changing.")
			if err != nil {
				return err
			}
			deliver += ":" + ts
		}
		if err := b.wakeLead("hi-jobs-check", teamUnreportedPrompt(), deliver); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0o600)
}

func teamUnreportedPrompt() string {
	return `This is hi, not a person. hi now watches the app's long work: work that can outlive a request (a loop over many records, a queue consumer, a backfill, an import, a scheduled recalculation, a worker started with the app) reports as a job, and hi opens an incident and wakes you when one fails, is blocked, stops moving, or goes silent. This app's work was written before that, and no job has reported yet, so hi can't see it stop.

Look through dev (/team/repo) and the specs, reading only, for such work. For each piece, note whether it reports as a job ($HI_JOBS_DIR or "$HI" team job), and whether it says so when it stops on its own: after too many failures in a row, at a limit, or on an item it can't process, it must write blocked or failed with why, not just stop. hi team jobs shows what reports today.

If any doesn't, file a task for the owners (hi team task add), titled "Report the app's long work as jobs": each piece reports as a named job, with a stall_after that is normal for it, and writes blocked or failed with the item and why wherever it stops on its own today, keeping its behavior otherwise. List the pieces you found, with where they are. It reaches production with the owners' next release.

What you write is posted under hi's message: in a few plain lines, which work hi couldn't see stopping, in the people's words, and the task's number. If all of it already reports, say so in one line.`
}

func teamJobConditionText(job teamJob, condition string) string {
	count := teamJobCount(job)
	switch condition {
	case "blocked":
		return fmt.Sprintf("%s stopped and waits for a decision at %s: %s", job.Job, firstNonEmpty(job.Item, "an item it didn't name"), firstNonEmpty(job.Error, "it gave no reason"))
	case "failed":
		return fmt.Sprintf("%s failed at %s: %s", job.Job, firstNonEmpty(job.Item, "an item it didn't name"), firstNonEmpty(job.Error, "it gave no reason"))
	case "silent":
		return fmt.Sprintf("%s has written nothing since %s (%s); whatever ran it may have died", job.Job, job.At.UTC().Format("Jan 2 15:04 UTC"), count)
	case "stalled":
		return fmt.Sprintf("%s says it runs, but its count hasn't moved in over %s (%s)", job.Job, teamJobDuration(job.stall()), count)
	}
	return job.Job + " is " + condition
}

func teamJobRecoveredText(job teamJob) string {
	if job.State == "done" {
		return fmt.Sprintf("%s finished: %s.", job.Job, teamJobCount(job))
	}
	return fmt.Sprintf("%s is running again: %s.", job.Job, teamJobCount(job))
}

// teamJobCount says how far a job is, as 8,272 of 533,386 releases.
func teamJobCount(job teamJob) string {
	text := teamThousands(job.Done)
	if job.Total > 0 {
		text += " of " + teamThousands(job.Total)
	}
	if job.Unit != "" {
		text += " " + job.Unit
	}
	return text
}

func teamThousands(n int64) string {
	digits := strconv.FormatInt(n, 10)
	sign := ""
	if strings.HasPrefix(digits, "-") {
		sign, digits = "-", digits[1:]
	}
	for i := len(digits) - 3; i > 0; i -= 3 {
		digits = digits[:i] + "," + digits[i:]
	}
	return sign + digits
}

func teamJobDuration(d time.Duration) string {
	if d >= time.Hour && d%time.Hour == 0 {
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%d minutes", int(d.Minutes()))
}

// ---------------------------------------------------------------------------
// what a job looks like to the people and the lead

type teamJobView struct {
	Slot      string    `json:"slot"`
	Job       string    `json:"job"`
	State     string    `json:"state"`
	Condition string    `json:"condition,omitempty"` // stalled or silent, for a job that says it runs
	Done      int64     `json:"done"`
	Total     int64     `json:"total,omitempty"`
	Unit      string    `json:"unit,omitempty"`
	PerDay    *float64  `json:"per_day,omitempty"`
	At        time.Time `json:"at"`
	Item      string    `json:"item,omitempty"`
	Error     string    `json:"error,omitempty"`
	Note      string    `json:"note,omitempty"`
	Incident  string    `json:"incident,omitempty"`
}

// teamJobsReport is a slot's jobs as they stand.
func teamJobsReport(team, slot string, now time.Time) []teamJobView {
	state := loadTeamJobsState(team)
	views := []teamJobView{}
	for _, job := range readTeamJobs(team, slot) {
		if now.Sub(job.At) > teamJobKeep {
			continue
		}
		watch := state.Jobs[slot+"/"+job.Job]
		view := teamJobView{Slot: slot, Job: job.Job, State: job.State, Done: job.Done, Total: job.Total, Unit: job.Unit, At: job.At, Item: job.Item, Error: job.Error, Note: job.Note}
		if watch != nil {
			if rate, ok := teamJobRate(watch, now); ok {
				view.PerDay = &rate
			}
			if condition, _ := teamJobCondition(job, watch, now); condition == "stalled" || condition == "silent" {
				view.Condition = condition
			}
			if watch.Open != "" {
				if incident, err := loadTeamIncident(team, watch.Open); err == nil && incident.Status != "closed" {
					view.Incident = incident.ID
				}
			}
		}
		views = append(views, view)
	}
	return views
}

// teamJobLines is production's and the preview's jobs, a line each, for
// the dev page's system panel and hi dev status: "prices in the preview:
// done, 5 of 5 tickers, 4 min ago".
func teamJobLines(team string, now time.Time) []string {
	var lines []string
	for _, slot := range []string{teamSlotProduction, teamSlotPreview} {
		for _, view := range teamJobsReport(team, slot, now) {
			state := view.State
			if view.Condition != "" {
				state = view.Condition
			}
			line := fmt.Sprintf("%s in %s: %s, %s, %s ago", view.Job, teamSlotName(slot), state, teamJobCount(teamJob{Done: view.Done, Total: view.Total, Unit: view.Unit}), teamAgo(now.Sub(view.At)))
			if view.Item != "" && state != "done" {
				line += ", at " + view.Item
			}
			if view.Error != "" {
				line += ": " + view.Error
			}
			if view.Incident != "" {
				line += " (incident " + view.Incident + ")"
			}
			lines = append(lines, line)
		}
	}
	return lines
}

func printTeamJobs(views []teamJobView, slot string, now time.Time, w io.Writer) {
	if len(views) == 0 {
		fmt.Fprintf(w, "No jobs in %s. An app's long work (a backfill, an import, a recalculation) shows here once it writes $HI_JOBS_DIR/<job>.json, or runs \"$HI\" team job.\n", teamSlotName(slot))
		return
	}
	fmt.Fprintf(w, "Jobs in %s:\n", teamSlotName(slot))
	for _, view := range views {
		state := view.State
		if view.Condition != "" {
			state = view.Condition
		}
		fmt.Fprintf(w, "\n%s · %s · %s\n", view.Job, state, teamJobCount(teamJob{Done: view.Done, Total: view.Total, Unit: view.Unit}))
		if view.PerDay != nil {
			fmt.Fprintf(w, "  Rate: %s a day (from hi's hourly counts)\n", teamThousands(int64(*view.PerDay+0.5)))
		} else {
			fmt.Fprintln(w, "  Rate: not known yet (hi counts once an hour)")
		}
		fmt.Fprintf(w, "  Last written: %s ago\n", teamAgo(now.Sub(view.At)))
		if view.Item != "" {
			fmt.Fprintf(w, "  At: %s\n", view.Item)
		}
		if view.Error != "" {
			fmt.Fprintf(w, "  Error: %s\n", view.Error)
		}
		if view.Note != "" {
			fmt.Fprintf(w, "  Note: %s\n", view.Note)
		}
		if view.Incident != "" {
			fmt.Fprintf(w, "  Incident: %s (hi team incidents %s)\n", view.Incident, view.Incident)
		}
	}
}

func teamAgo(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "less than a minute"
	case d < time.Hour:
		return fmt.Sprintf("%d min", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%d days", int(d.Hours()/24))
}

// restartApp gives a slot's app a new box, for a job that went silent
// while its box still runs. The lead may do it once per incident; after
// that, an owner does, on the dev page.
func (b *teamBroker) restartApp(request teamJobsRequest) (teamPreview, error) {
	incident, err := b.leadIncident(request.Incident, request.Because)
	if err != nil {
		return teamPreview{}, err
	}
	var approval *teamApproval
	restarts := 0
	for _, event := range incident.Events {
		if event.Action == "restart" && event.Who == "lead" {
			restarts++
		}
	}
	who := "lead"
	if request.owner != "" {
		approval, who = &teamApproval{Name: request.owner}, request.owner
	} else if restarts > 0 {
		return teamPreview{}, fmt.Errorf("you restarted the app once in %s already; a second restart is the owners': ask them, and they restart it on the dev page", incident.ID)
	}
	slot := teamSlotProduction
	if request.Preview {
		slot = teamSlotPreview
	}
	current, ok := loadTeamSlot(b.team, slot)
	if !ok {
		return teamPreview{}, fmt.Errorf("%s isn't running", teamSlotName(slot))
	}
	// Removed first, so its stop isn't taken for a crash.
	b.removeApp(current.Container)
	var state teamPreview
	if slot == teamSlotProduction {
		state, err = b.production(0)
	} else {
		state, err = b.refreshPreview(current)
	}
	event := teamIncidentEvent{Who: who, Action: "restart", Command: "hi team jobs restart" + map[bool]string{true: " --preview"}[request.Preview], Because: request.Because, Approval: approval}
	if err != nil {
		event.Result = "failed: " + err.Error()
	} else {
		event.Result = fmt.Sprintf("%s runs in a new box", teamSlotName(slot))
	}
	b.logIncidentAction(incident, event)
	return state, err
}

// forgetJob removes a job that no longer exists, such as one whose code a
// release took out, so it isn't listed or alerted on.
func (b *teamBroker) forgetJob(request teamJobsRequest) error {
	if !teamJobName.MatchString(request.Job) {
		return fmt.Errorf("%q isn't a job's name", request.Job)
	}
	if strings.TrimSpace(request.Because) == "" {
		return errors.New("say why: --because \"<why>\"")
	}
	slot := teamSlotProduction
	if request.Preview {
		slot = teamSlotPreview
	}
	path := filepath.Join(teamSlotJobsDir(b.team, slot), request.Job+".json")
	if !fileExists(path) {
		return fmt.Errorf("%s has no job %s; hi team jobs lists them", teamSlotName(slot), request.Job)
	}
	b.jobsMu.Lock()
	defer b.jobsMu.Unlock()
	if err := os.Remove(path); err != nil {
		return err
	}
	state := loadTeamJobsState(b.team)
	if watch := state.Jobs[slot+"/"+request.Job]; watch != nil && watch.Open != "" {
		if incident, err := loadTeamIncident(b.team, watch.Open); err == nil {
			b.logIncidentAction(incident, teamIncidentEvent{Who: "lead", Action: "forget", Command: "hi team jobs forget " + request.Job, Because: request.Because})
		}
	}
	delete(state.Jobs, slot+"/"+request.Job)
	return saveTeamJobsState(b.team, state)
}

type teamJobsRequest struct {
	Preview  bool   `json:"preview,omitempty"`
	Job      string `json:"job,omitempty"`
	Incident string `json:"incident,omitempty"`
	Because  string `json:"because,omitempty"`
	// owner is an owner on the dev page, whose click is the yes; no
	// socket sets it.
	owner string
}

// ---------------------------------------------------------------------------
// hi team job, in the app's box

// teamJobCommand writes a job's file for the app: hi team job <job>
// [--running|--blocked|--failed|--finished] [--done <n>] [--total <n>] ...
// What it isn't told stays as the file had it.
func teamJobCommand(args []string, stdout io.Writer) error {
	usage := usageError{`usage: "$HI" team job <job> [--running|--blocked|--failed|--finished] [--done <n>] [--total <n>] [--unit <word>]
                     [--item <id>] [--error <text>] [--note <text>] [--stall-after <duration>]`}
	dir := os.Getenv("HI_JOBS_DIR")
	if dir == "" {
		return errors.New("hi team job is for a team's app, in its box, where $HI_JOBS_DIR says where jobs go")
	}
	if len(args) == 0 || !teamJobName.MatchString(args[0]) {
		return usage
	}
	name := args[0]
	path := filepath.Join(dir, name+".json")
	var job teamJob
	if data, err := os.ReadFile(path); err == nil {
		json.Unmarshal(data, &job)
	}
	job.Job = name
	state := ""
	number := func(value string) (int64, error) {
		n, err := strconv.ParseInt(value, 10, 64)
		if err != nil || n < 0 {
			return 0, fmt.Errorf("%q isn't a count", value)
		}
		return n, nil
	}
	for i := 1; i < len(args); i++ {
		arg, value := args[i], ""
		if i+1 < len(args) {
			value = args[i+1]
		}
		var err error
		switch arg {
		case "--running", "--blocked", "--failed":
			state = strings.TrimPrefix(arg, "--")
			continue
		case "--finished":
			state = "done"
			continue
		case "--done":
			job.Done, err = number(value)
		case "--total":
			job.Total, err = number(value)
		case "--unit":
			job.Unit = value
		case "--item":
			job.Item = value
		case "--error":
			job.Error = value
		case "--note":
			job.Note = value
		case "--stall-after":
			if d, parseErr := time.ParseDuration(value); parseErr != nil || d < time.Minute {
				err = fmt.Errorf("--stall-after takes a duration of a minute or more, such as 30m or 2h, not %q", value)
			}
			job.StallAfter = value
		default:
			return usage
		}
		if err != nil {
			return err
		}
		if i+1 >= len(args) {
			return usage
		}
		i++
	}
	if state != "" {
		job.State = state
	}
	if job.State == "" {
		job.State = "running"
	}
	if job.State == "running" || job.State == "done" {
		job.Error = "" // a job that moves again isn't stuck on its last error
	}
	job.At = time.Now().UTC()
	data, _ := json.MarshalIndent(job, "", "  ")
	if err := writeFileAtomic(path, append(data, '\n'), 0o644); err != nil {
		return err
	}
	return nil
}

// ---------------------------------------------------------------------------
// hi team jobs, on the machine

// teamJobsCommand is hi team jobs <name> [--preview] [--json].
func teamJobsCommand(args []string, stdout io.Writer) error {
	var words []string
	slot, asJSON := teamSlotProduction, false
	for _, arg := range args {
		switch arg {
		case "--preview":
			slot = teamSlotPreview
		case "--json":
			asJSON = true
		default:
			if strings.HasPrefix(arg, "-") {
				return usageError{"unknown option " + arg}
			}
			words = append(words, arg)
		}
	}
	if len(words) != 1 {
		return usageError{"usage: hi team jobs <name> [--preview] [--json]"}
	}
	if _, err := loadTeam(words[0]); err != nil {
		return err
	}
	views := teamJobsReport(words[0], slot, time.Now())
	workers := teamWorkersReport(words[0], slot, (&teamBroker{}).appRunning, time.Now())
	if asJSON {
		data, _ := json.MarshalIndent(views, "", "  ")
		fmt.Fprintln(stdout, string(data))
		return nil
	}
	printTeamWorkers(workers, time.Now(), stdout)
	printTeamJobs(views, slot, time.Now(), stdout)
	return nil
}
