package main

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// teamTestProduction releases a first version to production.
func teamTestProduction(t *testing.T, b *teamTestBroker) teamPreview {
	t.Helper()
	os.WriteFile(filepath.Join(b.repo, "Makefile"), []byte("run:\n\t./serve\n"), 0o644)
	exec.Command("git", "-C", b.repo, "add", "-A").Run()
	exec.Command("git", "-C", b.repo, "commit", "-q", "-m", "make run").Run()
	teamTestMerge(t, b, "Suppliers")
	if _, err := b.release(teamReleaseRequest{host: "Olga", Notes: "Suppliers"}); err != nil {
		t.Fatal(err)
	}
	state, ok := loadTeamSlot("payments", teamSlotProduction)
	if !ok || state.Container == "" || state.Expose == "" {
		t.Fatalf("%+v", state)
	}
	return state
}

func TestTeamProductionAddressGoneKeepsTheBox(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	b.channel = &fakeTeamChannel{}
	first := teamTestProduction(t, b)
	b.mu.Lock()
	boxes := len(b.apps)
	b.mu.Unlock()

	// Its address ends early, as when its NetBird service was deleted and
	// its relay stopped: the box that runs and answers gets a new one, and
	// no second box starts on its data.
	b.mu.Lock()
	delete(b.exposures, first.Expose)
	b.mu.Unlock()
	if !b.productionNeedsWork() {
		t.Fatal("production without an address needs no work")
	}
	done := make(chan struct{})
	close(done)
	b.keepLinks(done, io.Discard)
	second, _ := loadTeamSlot("payments", teamSlotProduction)
	b.mu.Lock()
	after := len(b.apps)
	b.mu.Unlock()
	if second.Container != first.Container || second.Expose == first.Expose || second.Expose == "" || after != boxes {
		t.Fatalf("a new box for a new address: %+v (%d boxes, was %d)", second, after, boxes)
	}
	if b.productionNeedsWork() {
		t.Fatal("production still needs work")
	}

	// A box that doesn't answer gets a new box, as before.
	b.answers = func(int) bool { return false }
	b.mu.Lock()
	delete(b.exposures, second.Expose)
	b.mu.Unlock()
	b.keepLinks(done, io.Discard)
	third, _ := loadTeamSlot("payments", teamSlotProduction)
	if third.Container == second.Container || third.Expose == second.Expose {
		t.Fatalf("a box that doesn't answer kept its place: %+v", third)
	}
}

func TestTeamAppSilentIncident(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	channel := &fakeTeamChannel{}
	b.channel = channel
	woken := teamTestWakes(b.teamBroker)
	teamTestProduction(t, b)
	now := time.Now()

	// An app that answers is no news.
	if err := b.checkAnswers(now); err != nil || len(loadTeamIncidents("payments")) != 0 {
		t.Fatal(err, loadTeamIncidents("payments"))
	}
	// One that stops answering is an incident after five minutes, once.
	silent := true
	b.answers = func(int) bool { return !silent }
	for _, after := range []time.Duration{0, 4 * time.Minute} {
		b.checkAnswers(now.Add(after))
	}
	if incidents := loadTeamIncidents("payments"); len(incidents) != 0 {
		t.Fatalf("an incident before five minutes: %+v", incidents)
	}
	b.checkAnswers(now.Add(6 * time.Minute))
	b.checkAnswers(now.Add(7 * time.Minute))
	incidents := loadTeamIncidents("payments")
	if len(incidents) != 1 || incidents[0].Kind != "app" || incidents[0].Status != "open" ||
		!strings.Contains(incidents[0].Title, "hasn't answered for 6 minutes") || !strings.Contains(incidents[0].Detail, "a restart starts the same work again") {
		t.Fatalf("%+v", incidents)
	}
	if len(*woken) != 1 || !strings.Contains((*woken)[0], "hi-incident-i-1") {
		t.Fatalf("the lead was woken %d times: %q", len(*woken), *woken)
	}
	// Answering again closes it, and says so in its thread.
	silent = false
	b.checkAnswers(now.Add(8 * time.Minute))
	if closed, _ := loadTeamIncident("payments", "i-1"); closed.Status != "closed" {
		t.Fatalf("%+v", closed)
	}
	if !strings.Contains(strings.Join(channel.calls, "\n"), "Production's app answers again. Incident i-1 is closed.") {
		t.Fatal(channel.calls)
	}

	// Silence from before the team started counts from its start.
	b.jobsMu.Lock()
	jobs := loadTeamJobsState("payments")
	jobs.Silent = b.started.Add(-time.Hour)
	saveTeamJobsState("payments", jobs)
	b.jobsMu.Unlock()
	silent = true
	b.checkAnswers(now.Add(9 * time.Minute))
	if len(loadTeamIncidents("payments")) != 1 {
		t.Fatal("old silence opened an incident at once")
	}
}
