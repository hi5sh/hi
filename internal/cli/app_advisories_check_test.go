package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// An admin's Check now matches production's bill at the app's next beat,
// not a day after the last match; the page sees it wait, run, and end.
func TestAdvisoriesCheckNow(t *testing.T) {
	fake := &fakeAdvisories{vulns: map[string]osvRecord{
		"npm react 19.3.0": testOSVRecord("GHSA-r3ac-t000-0001", "npm", "react", "19.3.1", "CVE-2026-2222", "LOW", "2026-10-01T00:00:00Z"),
	}, kev: []string{"CVE-2026-0001"}, epss: map[string]string{}}
	fake.start(t)
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	appTestWakes(b.appBroker)
	b.channel = &fakeTeamChannel{}
	savedMachine, savedTools, savedSystemctl := appWatchMachine, appBomTools, appSystemctl
	t.Cleanup(func() { appWatchMachine, appBomTools, appSystemctl = savedMachine, savedTools, savedSystemctl })
	appWatchMachine = (&appTestProbe{states: map[string]string{}, disk: 0.3}).probe()
	appBomTools = appTestBomTools()
	running := "active"
	appSystemctl = func(args ...string) (string, error) { return running, nil }
	os.WriteFile(filepath.Join(b.repo, "bun.lock"), []byte(`{"packages": {"react": ["react@19.3.0", "", {}, "sha512-a"],},}`), 0o644)
	exec.Command("git", "-C", b.repo, "add", "-A").Run()
	exec.Command("git", "-C", b.repo, "commit", "-q", "-m", "lockfile").Run()

	// Nothing released: nothing to check.
	admin := adminPerson{Name: "Olga"}
	if _, err := adminCheckAdvisories(admin, "payments"); err == nil || !strings.Contains(err.Error(), "nothing is released") {
		t.Fatal(err)
	}
	saveTeamVersions("payments", appVersions{Production: 2, Releases: []appRelease{{Version: 2, Commit: boxGit(b.repo, "rev-parse", "HEAD")}}})
	b.sendHeartbeat("")
	w := appLocalWatcher("payments")
	matched := func() time.Time {
		w.mu.Lock()
		defer w.mu.Unlock()
		if team := w.state.Teams["payments"]; team != nil && !w.matching.Load() {
			return team.Matched
		}
		return time.Time{}
	}
	appTestEventually(t, "never matched", func() bool { return !matched().IsZero() })
	first := matched()
	b.sendHeartbeat("") // saves the watcher's state, which the page reads
	if check := adminAdvisoryCheckOf("payments", "v2"); !check.Up || check.Matched.IsZero() || !check.Asked.IsZero() {
		t.Fatalf("%+v", check)
	}

	// A new advisory: the day's match is hours away, so only an ask finds
	// it now.
	fake.mu.Lock()
	fake.vulns["npm react 19.3.0"] = testOSVRecord("GHSA-r3ac-t000-0002", "npm", "react", "19.3.1", "CVE-2026-2223", "LOW", "2026-10-02T00:00:00Z")
	fake.mu.Unlock()
	b.sendHeartbeat("")
	time.Sleep(50 * time.Millisecond)
	if !matched().Equal(first) {
		t.Fatal("matched again without an ask")
	}
	answer, err := adminCheckAdvisories(admin, "")
	if err != nil || strings.Join(answer["asked"].([]string), ",") != "payments" {
		t.Fatal(answer, err)
	}
	if check := adminAdvisoryCheckOf("payments", "v2"); check.Asked.IsZero() || !check.Started.IsZero() {
		t.Fatalf("%+v", check)
	}
	b.sendHeartbeat("")
	appTestEventually(t, "the ask wasn't taken", func() bool { return matched().After(first) })
	appTestEventually(t, "the ask stayed", func() bool { return !fileExists(appAdvisoriesCheckPath("payments")) })
	state := loadTeamWatchState("payments")
	if len(state.Advisories) != 1 || state.Advisories[0].ID != "GHSA-r3ac-t000-0002" {
		t.Fatalf("the new match isn't shown at once: %+v", state.Advisories)
	}
	b.sendHeartbeat("")
	if check := adminAdvisoryCheckOf("payments", "v2"); !check.Asked.IsZero() || check.Error != "" || !check.Matched.After(first) {
		t.Fatalf("%+v", check)
	}

	// A check that started and never ended is said to have failed.
	saveAppAdvisoriesCheck("payments", appAdvisoriesCheck{By: "Olga", Asked: time.Now().Add(-time.Hour), Started: time.Now().Add(-time.Hour)})
	os.Chtimes(appAdvisoriesCheckPath("payments"), time.Now(), time.Now())
	w.mu.Lock()
	w.state.Teams["payments"].Matched = time.Now().Add(-2 * time.Hour)
	w.saveLocked()
	w.mu.Unlock()
	if check := adminAdvisoryCheckOf("payments", "v2"); !strings.Contains(check.Error, "didn't finish") {
		t.Fatalf("%+v", check)
	}
	// A stopped app isn't asked.
	running = "inactive"
	if _, err := adminCheckAdvisories(admin, "payments"); err == nil || !strings.Contains(err.Error(), "isn't running") {
		t.Fatal(err)
	}
}
