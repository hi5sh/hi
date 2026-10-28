package main

import (
	"bytes"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// adoptTestTeam adopts the test upstream as team payments, fetching it
// later from the folder it came from.
func adoptTestTeam(t *testing.T) (upstream string) {
	t.Helper()
	upstream = makeTestUpstream(t)
	fakeAdoptGitHub(t, upstream, teamRepoInfo{Description: "A kanban board", DefaultBranch: "main"}, false)
	if err := teamAdoptCommand([]string{"payments", "ada/pocket-kanban", "--from", writeAdoptAnswers(t, "")}, strings.NewReader(""), io.Discard); err != nil {
		t.Fatal(err)
	}
	saved := teamUpstreamFetch
	teamUpstreamFetch = func(config teamConfig, repo string) error {
		if out, err := exec.Command("git", "-C", repo, "fetch", "-q", "--prune", teamUpstream).CombinedOutput(); err != nil {
			t.Fatalf("git fetch: %s", out)
		}
		return nil
	}
	t.Cleanup(func() { teamUpstreamFetch = saved })
	return upstream
}

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
		t.Fatalf("git %v: %s", args, out)
	}
}

func TestTeamUpstream(t *testing.T) {
	setupTeamTest(t)
	upstream := adoptTestTeam(t)
	var out bytes.Buffer
	if err := teamUpstreamCommand([]string{"payments"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Dev: pocket-kanban's ") || !strings.Contains(out.String(), "+ 1 commit of the team's") || strings.Contains(out.String(), "v1.0") ||
		!strings.Contains(out.String(), "Upstream: nothing new on main.") {
		t.Fatal(out.String())
	}
	// Upstream moves on: two commits and a release. Its tags land under
	// upstream/, never beside the team's.
	gitIn(t, upstream, "commit", "-q", "--allow-empty", "-m", "Calendar view")
	gitIn(t, upstream, "tag", "v1.1")
	gitIn(t, upstream, "commit", "-q", "--allow-empty", "-m", "Faster boards")
	out.Reset()
	if err := teamUpstreamCommand([]string{"payments"}, &out); err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(teamDir("payments"), "repo")
	if !strings.Contains(out.String(), "Upstream: 2 commits on main since, and 1 newer tag: v1.1.") || boxGit(repo, "tag", "--list", "v1.1") != "" {
		t.Fatal(out.String(), boxGit(repo, "tag", "--list"))
	}
	// Dev at an annotated tag is named by it.
	config, _ := loadTeam("payments")
	gitIn(t, upstream, "tag", "-a", "v1.1.1", "-m", "Fix", "main")
	gitIn(t, repo, "fetch", "-q", teamUpstream)
	gitIn(t, repo, "merge", "-q", "--no-edit", "upstream/main")
	if view, _ := teamUpstreamReport(config); view.BaseTag != "v1.1.1" || view.Behind != 0 {
		t.Fatalf("%+v", view)
	}
	if teamUpstreamCommand([]string{"nope"}, &out) == nil {
		t.Fatal("a team that doesn't exist")
	}
}

func TestTeamUpstreamWakesTheLead(t *testing.T) {
	setupTeamTest(t)
	upstream := adoptTestTeam(t)
	b := newTeamBroker("payments", "hi")
	channel := &fakeTeamChannel{}
	b.channel = channel
	var mu sync.Mutex
	var woken []string
	b.wakeLead = func(name, prompt, deliver string) error {
		mu.Lock()
		defer mu.Unlock()
		woken = append(woken, name+" "+deliver+" "+prompt)
		return nil
	}
	now := time.Now()
	// The first check takes what's there as what the team started with.
	if err := b.checkUpstream(now); err != nil || len(woken) != 0 {
		t.Fatal(err, woken)
	}
	gitIn(t, upstream, "commit", "-q", "--allow-empty", "-m", "Calendar view")
	gitIn(t, upstream, "tag", "v1.1")
	gitIn(t, upstream, "tag", "nightly-2026-10-18")
	// Within the day, no fetch.
	if err := b.checkUpstream(now.Add(time.Hour)); err != nil || len(woken) != 0 {
		t.Fatal(err, woken)
	}
	if err := b.checkUpstream(now.Add(25 * time.Hour)); err != nil {
		t.Fatal(err)
	}
	if len(woken) != 1 || !strings.HasPrefix(woken[0], "hi-upstream-v1-1 slack:C07PAYMENTS:1700000000.") || !strings.Contains(woken[0], "--upstream=v1.1") ||
		!strings.Contains(strings.Join(channel.calls, "\n"), "has a new version: v1.1.*") {
		t.Fatal(woken, channel.calls)
	}
	// Another release within the week waits; after it, the lead hears.
	gitIn(t, upstream, "tag", "v1.2")
	if err := b.checkUpstream(now.Add(50 * time.Hour)); err != nil || len(woken) != 1 {
		t.Fatal(err, woken)
	}
	if err := b.checkUpstream(now.Add(9 * 24 * time.Hour)); err != nil || len(woken) != 2 || !strings.HasPrefix(woken[1], "hi-upstream-v1-2 ") {
		t.Fatal(err, woken)
	}
	if err := b.checkUpstream(now.Add(20 * 24 * time.Hour)); err != nil || len(woken) != 2 {
		t.Fatal("told twice of the same version", woken)
	}
}
