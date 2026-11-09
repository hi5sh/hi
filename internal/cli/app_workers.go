package cli

// One box of a slot runs the app's background workers at a time, and the
// box that stops running them stops before the next one starts. Each slot
// has a lock, run/<slot>-workers/lock, which its boxes mount at
// /hi-workers; the relay in each box (hi team __app) holds it for the box,
// and while it does, $HI_WORKERS_FILE exists in the box. The app starts its
// workers once the file exists and stops them when it goes away.
//
// At a deploy hi writes the old box's name to run/<slot>-workers/yield.
// The old relay removes its file, waits for the app to say its workers
// stopped ($HI_WORKERS_FILE.stopped) or for the grace, and lets go; hi
// waits for the lock to be free before it starts the new box, whose relay
// takes it. If the new box doesn't start, hi removes it and the yield, and
// the old relay takes the lock back.
//
// Spec: docs/specs/approved/hi_app_workers_handover.md.

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	// appBoxWorkersDir is where an app's box sees its slot's lock, and
	// appBoxWorkersFile is $HI_WORKERS_FILE, in the box's own /tmp.
	appBoxWorkersDir  = "/hi-workers"
	appBoxWorkersFile = "/tmp/hi/workers"
	// appWorkersGraceDefault is how long an app has to stop its workers
	// when it doesn't say it has; HI_WORKERS_GRACE sets it, up to
	// appWorkersGraceMax.
	appWorkersGraceDefault = 30 * time.Second
	appWorkersGraceMax     = 300 * time.Second
	// appWorkersSpare is how much longer than the grace hi waits for the
	// lock: the relay looks once a second.
	appWorkersSpare = 10 * time.Second
	// appWorkersWaitAlert is how long a slot's box may wait for the lock
	// before it is an incident.
	appWorkersWaitAlert = 5 * time.Minute
)

// appWorkersRule follows the jobs rule in every brief.
const appWorkersRule = "Background workers (loops, queues, and schedules started with the app) start once the file named by $HI_WORKERS_FILE exists and stop when it goes away, checking every few seconds: at a release hi stops the old version's workers before the new version starts, so they never run twice. " +
	"Stopping puts back the item in hand and writes nothing more; when the last worker has stopped, the app creates $HI_WORKERS_FILE.stopped, so hi needn't wait out the grace (30 seconds, or HI_WORKERS_GRACE). Without $HI_WORKERS_FILE, as in your box and the tests, the workers run as before. " +
	"During a release the old version's pages still use the data, so the app's database waits for a lock rather than failing (for SQLite, a busy timeout of 10 seconds or more), and a migration keeps the old version working."

// appWorkersPoll is how often a relay looks at the lock and the yield.
var appWorkersPoll = time.Second

func appSlotWorkersDir(team, slot string) string {
	return filepath.Join(appDir(team), "run", slot+"-workers")
}

// appWorkersYield is run/<slot>-workers/yield: the box that is to let go
// of the workers, and until when. A yield hi left behind when it stopped
// mid-deploy ends by itself.
type appWorkersYield struct {
	Box   string    `json:"box"`
	At    time.Time `json:"at"`
	Until time.Time `json:"until"`
	Grace string    `json:"grace,omitempty"`
}

// appWorkersHolder is run/<slot>-workers/holder, written by the relay
// that took the lock.
type appWorkersHolder struct {
	Box   string    `json:"box"`
	Since time.Time `json:"since"`
}

func readTeamWorkersYield(dir string, now time.Time) (appWorkersYield, bool) {
	var yield appWorkersYield
	data, err := os.ReadFile(filepath.Join(dir, "yield"))
	if err != nil || json.Unmarshal(data, &yield) != nil || yield.Box == "" || now.After(yield.Until) {
		return appWorkersYield{}, false
	}
	return yield, true
}

func readTeamWorkersHolder(dir string) appWorkersHolder {
	var holder appWorkersHolder
	if data, err := os.ReadFile(filepath.Join(dir, "holder")); err == nil {
		json.Unmarshal(data, &holder)
	}
	return holder
}

// appWorkersGrace reads HI_WORKERS_GRACE: seconds, or a duration such as
// 2m.
func appWorkersGrace(value string) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return appWorkersGraceDefault
	}
	grace, err := time.ParseDuration(value)
	if err != nil {
		seconds, err := strconv.Atoi(value)
		if err != nil {
			return appWorkersGraceDefault
		}
		grace = time.Duration(seconds) * time.Second
	}
	return min(max(grace, 0), appWorkersGraceMax)
}

// appTryLock takes an exclusive flock on file without waiting.
func appTryLock(file *os.File) bool {
	return syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) == nil
}

// appWorkersFree says nobody holds the slot's lock: hi takes it and lets
// go at once.
func appWorkersFree(dir string) bool {
	file, err := os.OpenFile(filepath.Join(dir, "lock"), os.O_RDWR|os.O_CREATE, 0o666)
	if err != nil {
		return false
	}
	defer file.Close()
	return appTryLock(file)
}

// ---------------------------------------------------------------------------
// in the app's box

// appWorkersRelay holds the slot's lock for box while the app runs, and
// keeps the workers file to say so. It runs until done.
func appWorkersRelay(dir, box, workersFile string, grace time.Duration, done <-chan struct{}, stderr io.Writer) {
	file, err := os.OpenFile(filepath.Join(dir, "lock"), os.O_RDWR|os.O_CREATE, 0o666)
	if err != nil {
		// Without the lock the workers run as before hi handed them over.
		fmt.Fprintf(stderr, "hi: the app's workers run without hi's lock this time: %v\n", err)
		os.MkdirAll(filepath.Dir(workersFile), 0o755)
		os.WriteFile(workersFile, nil, 0o644)
		return
	}
	defer file.Close()
	stopped := workersFile + ".stopped"
	holding := false
	var yielding time.Time
	for {
		now := time.Now()
		yield, ok := readTeamWorkersYield(dir, now)
		yielded := ok && yield.Box == box
		switch {
		case !holding && !yielded && appTryLock(file):
			holding, yielding = true, time.Time{}
			os.Remove(stopped)
			data, _ := json.Marshal(appWorkersHolder{Box: box, Since: now.UTC()})
			writeFileAtomic(filepath.Join(dir, "holder"), append(data, '\n'), 0o644)
			os.MkdirAll(filepath.Dir(workersFile), 0o755)
			os.WriteFile(workersFile, nil, 0o644)
			fmt.Fprintln(stderr, "hi: this box runs the app's workers ($HI_WORKERS_FILE)")
		case holding && yielded && yielding.IsZero():
			yielding = now
			os.Remove(workersFile)
			fmt.Fprintf(stderr, "hi: a new version starts: the app's workers stop here (within %s)\n", grace)
		case holding && yielded:
			if fileExists(stopped) || now.Sub(yielding) >= grace {
				syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
				holding, yielding = false, time.Time{}
				os.Remove(stopped)
			}
		case holding && !yielding.IsZero():
			// The deploy gave up before this box let go: its workers carry on.
			yielding = time.Time{}
			os.WriteFile(workersFile, nil, 0o644)
		}
		select {
		case <-done:
			os.Remove(workersFile)
			return
		case <-time.After(appWorkersPoll):
		}
	}
}

// ---------------------------------------------------------------------------
// at a deploy

// handOverWorkers asks current's box to let go of its slot's workers
// before a new box starts, and waits for the lock to be free. What it
// returns ends the handover: the new box has started, or it failed and
// the old box takes the workers back. Boxes from a hi before the lock,
// and a box that isn't running, have nothing to hand over.
func (b *appBroker) handOverWorkers(slot string, current appPreview, had bool) func() {
	if !had || !current.Workers || current.Container == "" || !b.running(current.Container) {
		return func() {}
	}
	dir := appSlotWorkersDir(b.team, slot)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return func() {}
	}
	grace := appWorkersGrace(appBoxEnv(b.team, slot, 0)["HI_WORKERS_GRACE"])
	now := time.Now().UTC()
	yield := appWorkersYield{Box: current.Container, At: now, Until: now.Add(grace + appWorkersSpare + appPreviewReady + 10*time.Minute), Grace: grace.String()}
	data, _ := json.Marshal(yield)
	path := filepath.Join(dir, "yield")
	if err := writeFileAtomic(path, append(data, '\n'), 0o644); err != nil {
		return func() {}
	}
	deadline := time.Now().Add(grace + appWorkersSpare)
	for !appWorkersFree(dir) && time.Now().Before(deadline) {
		time.Sleep(appWorkersPoll / 2)
	}
	return func() { os.Remove(path) }
}

// clearWorkersYields removes the yields a broker that stopped mid-deploy
// left behind, so the boxes they named take their workers back.
func clearWorkersYields(team string) {
	for _, slot := range []string{appSlotProduction, appSlotPreview} {
		os.Remove(filepath.Join(appSlotWorkersDir(team, slot), "yield"))
	}
}

// ---------------------------------------------------------------------------
// what hi shows

type appWorkersView struct {
	Slot string `json:"slot"`
	// State is "runs" (the slot's box holds the workers), "handover" (its
	// box lets go of them for a new version), "waits" (its box waits for
	// them), or "unlocked" (a box from a hi before the lock).
	State  string    `json:"state"`
	Box    string    `json:"box"`
	Label  string    `json:"label,omitempty"`
	Since  time.Time `json:"since,omitzero"`
	Holder string    `json:"holder,omitempty"` // the box that holds them, when not the slot's
	Grace  string    `json:"grace,omitempty"`
}

// appWorkersReport says which box runs a slot's workers, or nil when the
// slot's box doesn't run.
func appWorkersReport(team, slot string, running func(string) bool, now time.Time) *appWorkersView {
	state, ok := loadTeamSlot(team, slot)
	if !ok || state.Container == "" || !running(state.Container) {
		return nil
	}
	view := &appWorkersView{Slot: slot, Box: state.Container, Label: state.Label}
	if !state.Workers {
		view.State = "unlocked"
		return view
	}
	dir := appSlotWorkersDir(team, slot)
	holder := readTeamWorkersHolder(dir)
	if yield, ok := readTeamWorkersYield(dir, now); ok && yield.Box == state.Container {
		view.State, view.Since, view.Grace = "handover", yield.At, yield.Grace
		return view
	}
	if holder.Box == state.Container {
		view.State, view.Since = "runs", holder.Since
		return view
	}
	view.State, view.Since, view.Holder = "waits", state.Started, holder.Box
	return view
}

func printTeamWorkers(view *appWorkersView, now time.Time, w io.Writer) {
	if view == nil {
		return
	}
	name := firstNonEmpty(view.Label, appSlotName(view.Slot))
	switch view.State {
	case "runs":
		fmt.Fprintf(w, "Workers: %s's box runs them, since %s ago ($HI_WORKERS_FILE).\n", name, appAgo(now.Sub(view.Since)))
	case "handover":
		fmt.Fprintf(w, "Workers: %s's box is handing them over to a new version: %s of its %s grace.\n", name, now.Sub(view.Since).Round(time.Second), firstNonEmpty(view.Grace, appWorkersGraceDefault.String()))
	case "waits":
		holder := "another box"
		if view.Holder != "" {
			holder = view.Holder
		}
		fmt.Fprintf(w, "Workers: %s's box waits for them, since %s ago; %s holds them.\n", name, appAgo(now.Sub(view.Since)), holder)
	case "unlocked":
		fmt.Fprintf(w, "Workers: %s's box started before hi handed workers over; the next deploy gives it the lock.\n", name)
	}
}

// checkWorkers opens an incident when production's box has waited for
// the workers' lock for appWorkersWaitAlert: something else holds it, so
// the app's workers don't run. It closes it when the box has them.
func (b *appBroker) checkWorkers(now time.Time) error {
	if b.stopped() {
		return nil
	}
	view := appWorkersReport(b.team, appSlotProduction, b.running, now)
	waits := view != nil && view.State == "waits"

	b.jobsMu.Lock()
	jobs := loadTeamJobsState(b.team)
	open := jobs.WorkersIncident
	if !waits {
		// The incident is kept until the workers run again: during a
		// deploy, the fix it asked for, there is no box, or a handover.
		runs := view != nil && view.State == "runs"
		changed := !jobs.WorkersWait.IsZero() || open != "" && runs
		jobs.WorkersWait = time.Time{}
		if runs {
			jobs.WorkersIncident = ""
		}
		var err error
		if changed {
			err = saveTeamJobsState(b.team, jobs)
		}
		b.jobsMu.Unlock()
		if open != "" && runs && b.incidentOpen(open) {
			b.recovered(open, "Production's box has the app's workers again.")
		}
		return err
	}
	if jobs.WorkersWait.IsZero() || jobs.WorkersWait.Before(b.started) {
		jobs.WorkersWait = now.UTC()
		err := saveTeamJobsState(b.team, jobs)
		b.jobsMu.Unlock()
		return err
	}
	since := jobs.WorkersWait
	b.jobsMu.Unlock()
	if open != "" || now.Sub(since) < appWorkersWaitAlert {
		return nil
	}
	minutes := int(now.Sub(since).Round(time.Minute) / time.Minute)
	holder := firstNonEmpty(view.Holder, "another process")
	title := fmt.Sprintf("Production's app has waited %d minutes for its workers", minutes)
	detail := fmt.Sprintf("Production's box (%s) answers, but hasn't had the workers' lock since %s UTC, so the app's background work doesn't run: %s holds it. ", view.Box, since.Format("15:04"), holder)
	if view.Holder != "" && b.running(view.Holder) {
		detail += "That box still runs: a deploy that stopped half way left it. Someone at the machine removes it with podman rm -f " + view.Holder + ", and production's box takes the workers within a second."
	} else {
		detail += "No box by that name runs. hi app jobs restart gives production a new box."
	}
	alert := title + ".\nIts pages answer; its background work waits. The lead looks into it here."
	incident, err := b.openIncident(appIncident{Kind: "workers", Slot: appSlotProduction, Title: title, Detail: detail, Opened: now.UTC()}, alert, "", appIncidentPrompt)
	if err != nil {
		return err
	}
	b.jobsMu.Lock()
	defer b.jobsMu.Unlock()
	jobs = loadTeamJobsState(b.team)
	jobs.WorkersIncident = incident.ID
	return saveTeamJobsState(b.team, jobs)
}

// ---------------------------------------------------------------------------
// work written before

// An app written before hi handed workers over (v1.3.0) starts its
// workers whatever $HI_WORKERS_FILE says, so a deploy still runs them
// twice. Once per team, when the app has reported jobs and dev's code
// doesn't mention $HI_WORKERS_FILE (AGENTS.md does, for every project), hi
// asks the lead to brief the change.

func appWorkersCheckedPath(team string) string {
	return filepath.Join(appDir(team), "run", "workers-checked")
}

func (b *appBroker) checkWorkersRule() error {
	path := appWorkersCheckedPath(b.team)
	if fileExists(path) {
		return nil
	}
	config, err := loadTeam(b.team)
	if err != nil {
		return err
	}
	reported := len(readTeamJobs(b.team, appSlotProduction)) > 0 || len(readTeamJobs(b.team, appSlotPreview)) > 0
	follows := strings.TrimSpace(boxGit(b.repo, "grep", "-l", "HI_WORKERS_FILE", config.Branch, "--", ".", ":!*.md")) != ""
	if reported && !follows {
		if b.wakeLead == nil {
			return nil // asked once the lead is there
		}
		deliver := "slack:" + config.opsChannel()
		if b.channel != nil {
			ts, err := b.channel.post(config.opsChannel(), ":handshake: *hi now hands the app's background work over at every release*: the old version's workers stop before the new one starts, so they never run twice and the new version doesn't find the database busy. This app's workers don't take part yet: the lead checks what needs changing and says here.")
			if err != nil {
				return err
			}
			deliver += ":" + ts
			if err := appMarkDone(path); err != nil { // said once
				return err
			}
		}
		if err := b.wakeLead("hi-workers-check", appWorkersRulePrompt(), deliver); err != nil {
			return err
		}
	}
	return appMarkDone(path)
}

func appWorkersRulePrompt() string {
	return `This is hi, not a person. At a release, a rollback, or a restart, hi starts the new version's box beside the old one, on the same data. Until now both boxes ran the app's background workers for that minute: the same work twice, and a new version that can fail to start because the old one's workers hold the database. hi now hands the workers over: one box of each slot holds them, and $HI_WORKERS_FILE exists in that box while it does. At a deploy hi removes it in the old box, waits for the workers there to stop, and only then starts the new box.

This app reports jobs, so it has such workers, and dev doesn't mention $HI_WORKERS_FILE yet. Look through dev (/team/repo), reading only, for where the workers start: loops, queues, schedules started with the app.

File a task for the owners (hi app task add), titled "Hand the workers over at a deploy": each worker starts once the file named by $HI_WORKERS_FILE exists and stops when it goes away, checking every few seconds; stopping puts back the item in hand and writes nothing more; when the last worker has stopped, the app creates $HI_WORKERS_FILE.stopped, so hi needn't wait out the grace (30 seconds; HI_WORKERS_GRACE in hi app env for longer). Without $HI_WORKERS_FILE, as in the tests, the workers run as before. List where the workers start. The first release after it still overlaps; the ones after don't.

What you write is posted under hi's message: in a few plain lines, which work runs twice at a release today, in the people's words, and the task's number. If the app already does this some other way, say so in one line.`
}
