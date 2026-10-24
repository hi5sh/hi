package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A task that only adds to docs/ after an approval gets no review, and
// merges on that approval.
func TestTeamDocsOnlyNeedsNoReview(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	b.reviewRounds = teamReviewRounds
	b.reports["payments-t-2"] = `{"status": "complete", "summary": "Approve: tested."}`
	if _, err := b.startTask(teamRequest{Role: "coder", Brief: "Plot the index", Small: true}); err != nil {
		t.Fatal(err)
	}
	waitTeamSteps(t, "t-1")
	// The check writes only a verification record.
	b.files = map[string]map[string]string{"payments-t-3": {"docs/verification/plot.md": "checked"}}
	saved := b.run
	b.run = func(args ...string) ([]byte, error) {
		out, err := saved(args...)
		if len(args) > 5 && args[0] == "agent" && args[1] != "wait" && args[5] == "payments-t-3" {
			// The fake commits a file named for the box; keep it in docs/.
			work := filepath.Join(t.TempDir(), "w")
			exec.Command("git", "-C", b.repo, "worktree", "add", "-q", work, "hi-box/payments-t-3").Run()
			exec.Command("git", "-C", work, "rm", "-q", "payments-t-3.txt").Run()
			os.MkdirAll(filepath.Join(work, "docs"), 0o755)
			os.WriteFile(filepath.Join(work, "docs", "t-3.txt"), []byte("t-3"), 0o644)
			exec.Command("git", "-C", work, "add", "-A").Run()
			exec.Command("git", "-C", work, "commit", "-q", "-m", "docs only").Run()
			exec.Command("git", "-C", b.repo, "worktree", "remove", "--force", work).Run()
		}
		return out, err
	}
	check := teamTestSmall(t, b, teamRequest{Brief: "Record the preview checks", On: "t-1"})
	check = waitTeamTask(t, check.ID)
	if check.Next != "" {
		t.Fatalf("a docs-only task was reviewed: %+v", check)
	}
	if earlier, ok := b.approvedDocsOnly(check); !ok || earlier.ID != "t-2" {
		t.Fatal(earlier, ok)
	}
	var out strings.Builder
	printTeamTaskView(teamTaskView{teamTask: check, DocsApproved: "t-2"}, &out)
	if !strings.Contains(out.String(), "gets no review again: hi team merge t-3 merges it") {
		t.Fatal(out.String())
	}
	merged, err := b.merge("t-3", "")
	if err != nil || merged.ID != "t-3" || merged.Status != "merged" {
		t.Fatal(merged.ID, merged.Status, err)
	}
	if log := boxGit(b.repo, "log", "-1", "--format=%B", "main"); !strings.Contains(log, "approved in review t-2, with only docs changed since") {
		t.Fatal(log)
	}
}

// Quick work merges while its review runs; a review that then asks for
// changes wakes the lead.
func TestTeamQuickWorkMergesBeforeReview(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	var woke []string
	b.wakeLead = func(name, prompt, deliver string) error {
		woke = append(woke, name+"\n"+prompt)
		return nil
	}
	task := teamTestSmall(t, b, teamRequest{Brief: "Fix a label"})
	if !b.quickWork(task) {
		t.Fatal("a small fix isn't quick")
	}
	// Its review is still running.
	saveTeamTask("payments", teamTask{ID: "t-2", Role: "reviewer", Kind: "review", Review: task.ID, Status: "running", Round: 1, Created: time.Now()})
	task.Next = "t-2"
	saveTeamTask("payments", task)
	merged, err := b.merge(task.ID, "")
	if err != nil || merged.Status != "merged" {
		t.Fatal(merged.Status, err)
	}
	if log := boxGit(b.repo, "log", "-1", "--format=%B", "main"); !strings.Contains(log, "merged as quick work while review t-2 runs") {
		t.Fatal(log)
	}
	steps := []teamTask{merged, {ID: "t-2", Role: "reviewer", Kind: "review", Review: task.ID, Status: "running", Round: 1}}
	var out strings.Builder
	printTeamTaskView(teamTaskView{teamTask: steps[0], Steps: steps, Quick: true}, &out)
	if !strings.Contains(out.String(), "Quick work: the coder is done, and the review (t-2) runs after") {
		t.Fatal(out.String())
	}
	review := teamTask{ID: "t-2", Role: "reviewer", Kind: "review", Review: task.ID, Status: "done", Verdict: "changes", Round: 1,
		Report: &agentReport{Status: "done", Report: []byte(`{"status":"complete","summary":"Changes needed: the label is cut on phones.","follow_ups":["Wrap the label."]}`)}}
	b.reviewedAfterMerge(review)
	if len(woke) != 1 || !strings.Contains(woke[0], "hi-after-review-t-2") || !strings.Contains(woke[0], "- Wrap the label.") {
		t.Fatal(woke)
	}
}

func TestTeamWorkDiffCountsCode(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	base := boxGit(b.repo, "rev-parse", "HEAD")
	os.MkdirAll(filepath.Join(b.repo, "docs"), 0o755)
	os.WriteFile(filepath.Join(b.repo, "docs", "long.md"), []byte(strings.Repeat("doc\n", 500)), 0o644)
	os.WriteFile(filepath.Join(b.repo, "chart.ts"), []byte(strings.Repeat("code\n", 30)), 0o644)
	exec.Command("git", "-C", b.repo, "add", "-A").Run()
	exec.Command("git", "-C", b.repo, "commit", "-q", "-m", "x").Run()
	files, lines := teamWorkDiff(b.repo, base, "HEAD")
	if len(files) != 2 || lines != 30 {
		t.Fatal(files, lines)
	}
}
