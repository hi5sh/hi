package main

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// teamTestRelay runs a box's relay until the test ends or stop is called,
// and plays an app that follows the rule: once its workers file goes away
// it says its workers stopped.
func teamTestRelay(t *testing.T, dir, box string, grace time.Duration, follows bool) (file string, stop func()) {
	t.Helper()
	file = filepath.Join(t.TempDir(), "hi", "workers")
	done := make(chan struct{})
	var once sync.Once
	stop = func() { once.Do(func() { close(done) }) }
	t.Cleanup(stop)
	go teamWorkersRelay(dir, box, file, grace, done, io.Discard)
	if follows {
		go func() {
			had := false
			for {
				select {
				case <-done:
					return
				case <-time.After(5 * time.Millisecond):
				}
				switch exists := fileExists(file); {
				case exists:
					had = true
				case had:
					os.WriteFile(file+".stopped", nil, 0o644)
					had = false
				}
			}
		}()
	}
	return file, stop
}

func teamTestEventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal(what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func teamTestYield(t *testing.T, dir, box string, until time.Time) {
	t.Helper()
	data, _ := json.Marshal(teamWorkersYield{Box: box, At: time.Now(), Until: until})
	if err := os.WriteFile(filepath.Join(dir, "yield"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestTeamWorkersRelayHandsOver(t *testing.T) {
	saved := teamWorkersPoll
	teamWorkersPoll = 10 * time.Millisecond
	t.Cleanup(func() { teamWorkersPoll = saved })
	dir := t.TempDir()

	// One box holds the workers; a second waits.
	old, _ := teamTestRelay(t, dir, "old", time.Hour, false)
	teamTestEventually(t, "the first box never got the workers", func() bool { return fileExists(old) })
	next, _ := teamTestRelay(t, dir, "next", time.Hour, false)
	time.Sleep(100 * time.Millisecond)
	if fileExists(next) || teamWorkersFree(dir) || readTeamWorkersHolder(dir).Box != "old" {
		t.Fatal("two boxes hold the workers")
	}

	// The old box yields: its file goes at once, the lock once its app
	// says its workers stopped.
	teamTestYield(t, dir, "old", time.Now().Add(time.Hour))
	teamTestEventually(t, "the old box's file stayed", func() bool { return !fileExists(old) })
	time.Sleep(50 * time.Millisecond)
	if fileExists(next) {
		t.Fatal("the next box took the workers before the old one's stopped")
	}
	os.WriteFile(old+".stopped", nil, 0o644)
	teamTestEventually(t, "the next box never got the workers", func() bool { return fileExists(next) })
	if holder := readTeamWorkersHolder(dir); holder.Box != "next" || holder.Since.IsZero() {
		t.Fatalf("%+v", holder)
	}
	if fileExists(old + ".stopped") {
		t.Fatal("the stopped file stays for next time")
	}

	// The yield gone, the old box doesn't take them back from the next.
	os.Remove(filepath.Join(dir, "yield"))
	time.Sleep(50 * time.Millisecond)
	if fileExists(old) || !fileExists(next) {
		t.Fatal("both boxes hold the workers")
	}
}

func TestTeamWorkersRelayGraceAndWithdrawal(t *testing.T) {
	saved := teamWorkersPoll
	teamWorkersPoll = 10 * time.Millisecond
	t.Cleanup(func() { teamWorkersPoll = saved })

	// An app that ignores the rule lets go after the grace; a deploy that
	// failed gives the box its workers back.
	dir := t.TempDir()
	file, _ := teamTestRelay(t, dir, "old", 50*time.Millisecond, false)
	teamTestEventually(t, "no workers", func() bool { return fileExists(file) })
	teamTestYield(t, dir, "old", time.Now().Add(time.Hour))
	teamTestEventually(t, "the lock was kept past the grace", func() bool { return teamWorkersFree(dir) })
	if fileExists(file) {
		t.Fatal("a box that let go keeps its file")
	}
	os.Remove(filepath.Join(dir, "yield"))
	teamTestEventually(t, "the box never took its workers back", func() bool { return fileExists(file) && !teamWorkersFree(dir) })

	// A yield withdrawn before the box let go: its workers carry on.
	dir = t.TempDir()
	file, _ = teamTestRelay(t, dir, "old", time.Hour, false)
	teamTestEventually(t, "no workers", func() bool { return fileExists(file) })
	teamTestYield(t, dir, "old", time.Now().Add(time.Hour))
	teamTestEventually(t, "the file stayed", func() bool { return !fileExists(file) })
	os.Remove(filepath.Join(dir, "yield"))
	teamTestEventually(t, "the workers didn't carry on", func() bool { return fileExists(file) })

	// A yield past its time, left by a broker that stopped, is ignored.
	teamTestYield(t, dir, "old", time.Now().Add(-time.Minute))
	time.Sleep(50 * time.Millisecond)
	if !fileExists(file) {
		t.Fatal("an old yield stopped the workers")
	}

	// The box ending lets go.
	dir = t.TempDir()
	file, stop := teamTestRelay(t, dir, "old", time.Hour, false)
	teamTestEventually(t, "no workers", func() bool { return fileExists(file) })
	stop()
	teamTestEventually(t, "an ended box kept the lock", func() bool { return teamWorkersFree(dir) })
}

func TestTeamWorkersGrace(t *testing.T) {
	for value, want := range map[string]time.Duration{
		"": 30 * time.Second, "90": 90 * time.Second, "2m": 2 * time.Minute, "1h": 300 * time.Second,
		"-5": 0, "soon": 30 * time.Second,
	} {
		if got := teamWorkersGrace(value); got != want {
			t.Errorf("%q: %s, not %s", value, got, want)
		}
	}
}

func TestTeamAppGetsTheWorkersFile(t *testing.T) {
	setupTeamTest(t)
	newTeamTestBroker(t)
	env := teamAppEnv("payments", teamSlotProduction, 8080)
	if env["HI_WORKERS_FILE"] != teamAppWorkersFile {
		t.Fatal(env)
	}
	if !teamAppEnvReserved["HI_WORKERS_FILE"] {
		t.Fatal("the app's settings can set hi's own variable")
	}
	for _, text := range []string{teamJobsRule, teamAgentsFile} {
		if !strings.Contains(text, "$HI_WORKERS_FILE") {
			t.Errorf("no workers rule in %.60q", text)
		}
	}
}

// A release: the old box's workers stop before the new box starts, and
// the new box takes them. When the new box fails, the old one keeps them.
func TestTeamDeployHandsOverWorkers(t *testing.T) {
	saved := teamWorkersPoll
	teamWorkersPoll = 10 * time.Millisecond
	t.Cleanup(func() { teamWorkersPoll = saved })
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	os.WriteFile(filepath.Join(b.repo, "Makefile"), []byte("run:\n\t./serve\n"), 0o644)
	commit := func(message string) {
		os.WriteFile(filepath.Join(b.repo, "version.txt"), []byte(message), 0o644)
		exec.Command("git", "-C", b.repo, "add", "-A").Run()
		exec.Command("git", "-C", b.repo, "commit", "-q", "-m", message).Run()
	}
	dir := teamSlotWorkersDir("payments", teamSlotProduction)
	files := map[string]string{}
	stops := map[string]func(){}
	var mu sync.Mutex
	fakeRemove := b.removeApp
	b.removeApp = func(container string) {
		mu.Lock()
		if stop := stops[container]; stop != nil {
			stop()
		}
		mu.Unlock()
		fakeRemove(container)
	}
	fakeApp := b.app
	var failNext bool
	var order []string
	b.app = func(slot, name, src, data, address, command string, port, hostPort int) error {
		mu.Lock()
		defer mu.Unlock()
		os.MkdirAll(dir, 0o700) // as startApp does
		for box, file := range files {
			if fileExists(file) {
				order = append(order, box+" still runs its workers")
			}
		}
		if !teamWorkersFree(dir) {
			order = append(order, "the lock is held")
		}
		if failNext {
			return errors.New("it didn't start")
		}
		file, stop := teamTestRelay(t, dir, name, time.Hour, true)
		files[name], stops[name] = file, stop
		order = append(order, "started "+name)
		return fakeApp(slot, name, src, data, address, command, port, hostPort)
	}
	show := func() (teamPreview, error) {
		return b.show(teamPreview{Slot: teamSlotProduction, Source: "main", Label: "production"}, "HEAD", time.Hour)
	}

	commit("v1")
	first, err := show()
	if err != nil || !first.Workers {
		t.Fatal(err, first)
	}
	teamTestEventually(t, "the first box never got the workers", func() bool { return fileExists(files[first.Container]) })

	commit("v2")
	second, err := show()
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	if strings.Join(order, "; ") != "started "+first.Container+"; started "+second.Container {
		t.Fatal(order)
	}
	mu.Unlock()
	teamTestEventually(t, "the new box never got the workers", func() bool { return fileExists(files[second.Container]) })
	if fileExists(filepath.Join(dir, "yield")) {
		t.Fatal("the yield stays after the deploy")
	}
	view := teamWorkersReport("payments", teamSlotProduction, b.running, time.Now())
	if view == nil || view.State != "runs" || view.Box != second.Container {
		t.Fatalf("%+v", view)
	}

	// The next version doesn't start: the box there keeps its workers.
	commit("v3")
	failNext = true
	if _, err := show(); err == nil {
		t.Fatal("a box that didn't start")
	}
	teamTestEventually(t, "the box never took its workers back", func() bool { return fileExists(files[second.Container]) })
	if current, _ := loadTeamSlot("payments", teamSlotProduction); current.Container != second.Container {
		t.Fatal(current.Container)
	}
}

// A box from a hi before the lock has nothing to hand over: the deploy
// doesn't wait.
func TestTeamHandOverFromAnOlderBox(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	b.running = func(string) bool { return true }
	start := time.Now()
	b.handOverWorkers(teamSlotProduction, teamPreview{Container: "old"}, true)()
	if time.Since(start) > time.Second || fileExists(filepath.Join(teamSlotWorkersDir("payments", teamSlotProduction), "yield")) {
		t.Fatal("handed over from a box with no lock")
	}
}

func TestTeamWorkersWaitIsAnIncident(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	channel := &fakeTeamChannel{}
	b.channel = channel
	teamTestWakes(b.teamBroker)
	b.running = func(string) bool { return true }
	os.MkdirAll(filepath.Join(teamDir("payments"), "run"), 0o700)
	saveTeamSlot("payments", teamPreview{Slot: teamSlotProduction, Label: "production v17", Container: "box-new", Workers: true, Started: time.Now()})
	dir := teamSlotWorkersDir("payments", teamSlotProduction)
	os.MkdirAll(dir, 0o700)
	hold := func(box string) {
		data, _ := json.Marshal(teamWorkersHolder{Box: box, Since: time.Now()})
		os.WriteFile(filepath.Join(dir, "holder"), data, 0o644)
	}
	hold("box-old")
	now := time.Now().Add(time.Minute)
	for _, at := range []time.Duration{0, 4 * time.Minute, 6 * time.Minute, 7 * time.Minute} {
		if err := b.checkWorkers(now.Add(at)); err != nil {
			t.Fatal(err)
		}
	}
	incidents := loadTeamIncidents("payments")
	if len(incidents) != 1 || incidents[0].Kind != "workers" || !strings.Contains(incidents[0].Title, "waited 6 minutes") ||
		!strings.Contains(incidents[0].Detail, "podman rm -f box-old") {
		t.Fatalf("%+v", incidents)
	}
	var text strings.Builder
	printTeamWorkers(teamWorkersReport("payments", teamSlotProduction, b.running, now), now, &text)
	if !strings.Contains(text.String(), "production v17's box waits for them") || !strings.Contains(text.String(), "box-old holds them") {
		t.Fatal(text.String())
	}

	hold("box-new")
	if err := b.checkWorkers(now.Add(8 * time.Minute)); err != nil {
		t.Fatal(err)
	}
	if incident, _ := loadTeamIncident("payments", incidents[0].ID); incident.Status != "closed" {
		t.Fatalf("%+v", incident)
	}
	text.Reset()
	printTeamWorkers(teamWorkersReport("payments", teamSlotProduction, b.running, now), now, &text)
	if !strings.HasPrefix(text.String(), "Workers: production v17's box runs them") {
		t.Fatal(text.String())
	}
}

func TestTeamWorkersCheckOnce(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	channel := &fakeTeamChannel{}
	b.channel = channel
	woken := teamTestWakes(b.teamBroker)
	check := func() {
		t.Helper()
		if err := b.checkWorkersRule(); err != nil {
			t.Fatal(err)
		}
	}
	// No jobs reported: no workers hi knows of.
	check()
	if len(*woken) != 0 || !fileExists(teamWorkersCheckedPath("payments")) {
		t.Fatal(*woken)
	}
	// Jobs, and dev doesn't follow the rule: the lead is asked once.
	os.Remove(teamWorkersCheckedPath("payments"))
	writeTeamTestJob(t, teamSlotProduction, teamJob{Job: "backfill", State: "running", Done: 1, At: time.Now()})
	check()
	check()
	if len(channel.calls) != 1 || len(*woken) != 1 || !strings.HasPrefix((*woken)[0], "hi-workers-check ") || !strings.Contains((*woken)[0], "$HI_WORKERS_FILE.stopped") {
		t.Fatal(channel.calls, *woken)
	}
	// Dev that follows it already isn't asked about.
	os.Remove(teamWorkersCheckedPath("payments"))
	os.WriteFile(filepath.Join(b.repo, "workers.ts"), []byte("const file = process.env.HI_WORKERS_FILE\n"), 0o644)
	exec.Command("git", "-C", b.repo, "add", "-A").Run()
	exec.Command("git", "-C", b.repo, "commit", "-q", "-m", "workers").Run()
	check()
	if len(*woken) != 1 {
		t.Fatal(*woken)
	}
}

func TestTeamAppRelayArgs(t *testing.T) {
	var stderr strings.Builder
	if code := runTeamApp([]string{"--workers", "/hi-workers"}, &stderr); code != 2 || !strings.Contains(stderr.String(), "--box") {
		t.Fatal(code, stderr.String())
	}
}
