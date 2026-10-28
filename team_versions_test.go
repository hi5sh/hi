package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// teamTestMerge puts a piece of work into dev the way the dev box does:
// a commit on a branch of its clone, shipped.
func teamTestMerge(t *testing.T, b *teamTestBroker, title string) teamTask {
	t.Helper()
	n := len(loadTeamTasks("payments")) + 1
	return teamTestShip(t, b, title, map[string]string{fmt.Sprintf("work/%d.txt", n): title + "\n"})
}

// teamTestShip ships files, on a branch of the dev box's clone from dev.
func teamTestShip(t *testing.T, b *teamTestBroker, title string, files map[string]string) teamTask {
	t.Helper()
	branch := fmt.Sprintf("work-%d", len(loadTeamTasks("payments"))+1)
	clone := filepath.Join(teamDevDir("payments"), "repo")
	if !fileExists(filepath.Join(clone, ".git")) {
		if err := prepareTeamDevRepo("payments"); err != nil {
			t.Fatal(err)
		}
	}
	teamGit(clone, "fetch", "-q", "origin")
	if out, err := teamGit(clone, "checkout", "-q", "-B", branch, "origin/main"); err != nil {
		t.Fatal(out, err)
	}
	for name, text := range files {
		os.MkdirAll(filepath.Dir(filepath.Join(clone, name)), 0o700)
		os.WriteFile(filepath.Join(clone, name), []byte(text), 0o600)
	}
	if out, err := teamGit(clone, "add", "-A"); err != nil {
		t.Fatal(out, err)
	}
	if out, err := teamGit(clone, "commit", "-q", "-m", title); err != nil {
		t.Fatal(out, err)
	}
	head, err := b.fetchDevBranch(branch)
	if err != nil {
		t.Fatal(err)
	}
	task, err := b.mergeDevBranch(teamDevShipRequest{Branch: branch, Title: title}, head)
	if err != nil {
		t.Fatal(err)
	}
	return task
}

func TestTeamVersions(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	channel := &fakeTeamChannel{}
	b.channel = channel
	posted := func() string { return channel.calls[len(channel.calls)-1] }

	view, _ := teamVersionsReport("payments")
	if view.Production != 0 || view.Dev != "dev, 0 changes, nothing released yet" {
		t.Fatalf("%+v", view)
	}
	first := teamTestMerge(t, b, "Add a supplier list")
	teamTestMerge(t, b, "Add a search box")
	if view, _ = teamVersionsReport("payments"); len(view.Changes) != 2 || view.Dev != "dev, 2 changes, nothing released yet" {
		t.Fatalf("%+v", view)
	}

	// An owner releases, with notes for the people.
	if _, err := b.release(teamReleaseRequest{host: "Olga"}); err == nil || !strings.Contains(err.Error(), "--notes") {
		t.Fatal(err)
	}
	if _, err := b.release(teamReleaseRequest{Notes: "x"}); err == nil || !strings.Contains(err.Error(), "dev page") {
		t.Fatal("released without an owner:", err)
	}
	result, err := b.release(teamReleaseRequest{host: "Olga", Notes: "- A list of suppliers\n- Search"})
	if err != nil {
		t.Fatal(err)
	}
	head := boxGit(b.repo, "rev-parse", "main")
	if result.Release.Version != 1 || boxGit(b.repo, "rev-parse", "production") != head || boxGit(b.repo, "rev-parse", "release-1^{commit}") != head {
		t.Fatalf("%+v", result)
	}
	// One line in the channel; what it brings in its thread.
	headline := channel.calls[len(channel.calls)-2]
	if !strings.HasPrefix(headline, "post C07PAYMENTS New PROD version (v1) :rocket:") ||
		!strings.HasPrefix(posted(), "postIn C07PAYMENTS ") || !strings.Contains(posted(), " :rocket: *Production v1 is out*, the first release, approved by Olga.\n\n- A list of suppliers") {
		t.Fatal(headline, posted())
	}
	if len(result.Release.Changes) != 2 || !strings.HasPrefix(result.Release.Changes[0], first.ID+" ") {
		t.Fatal(result.Release.Changes)
	}
	if _, err := b.release(teamReleaseRequest{host: "Olga", Notes: "x"}); err == nil || !strings.Contains(err.Error(), "nothing new since production v1") {
		t.Fatal(err)
	}
	if view, _ = teamVersionsReport("payments"); view.Dev != "dev, the same as production v1" {
		t.Fatal(view.Dev)
	}

	export := teamTestMerge(t, b, "Export to a spreadsheet")
	if view, _ = teamVersionsReport("payments"); view.Dev != "dev, v1 + 1 change" || view.Changes[0].ID != export.ID {
		t.Fatalf("%+v", view)
	}
	if _, err := b.release(teamReleaseRequest{host: "Olga", Notes: "Export"}); err != nil {
		t.Fatal(err)
	}
	// Back to v1, then on to v2 again.
	if result, err = b.release(teamReleaseRequest{Version: 1, host: "Olga"}); err != nil || !result.Back {
		t.Fatal(result, err)
	}
	if boxGit(b.repo, "rev-parse", "production") != head || !strings.Contains(posted(), "*Production is back on v1* (it was on v2)") {
		t.Fatal(posted())
	}
	if view, _ = teamVersionsReport("payments"); view.Production != 1 || view.Dev != "dev, v1 + 1 change" {
		t.Fatalf("%+v", view)
	}
	if _, err := b.release(teamReleaseRequest{Version: 1, host: "Olga"}); err == nil || !strings.Contains(err.Error(), "on v1 already") {
		t.Fatal(err)
	}

	// Dev back to production: the export is set aside, the specs stay.
	os.WriteFile(filepath.Join(b.repo, "docs", "specs", "export.md"), []byte("Status: dropped\n"), 0o644)
	exportFile := filepath.Join(b.repo, "work", "3.txt")
	if !fileExists(exportFile) {
		t.Fatal("the export isn't in dev")
	}
	set, err := b.resetDev("Olga")
	if err != nil || len(set) != 1 || set[0].ID != export.ID {
		t.Fatal(set, err)
	}
	if fileExists(exportFile) || !fileExists(filepath.Join(b.repo, "docs", "specs", "export.md")) || boxGit(b.repo, "status", "--porcelain") != "" {
		t.Fatal("dev isn't production's code with the lead's specs")
	}
	if !strings.Contains(posted(), "*Dev is set back to production v1*") || !strings.Contains(posted(), "• Export to a spreadsheet") {
		t.Fatal(posted())
	}
	if view, _ = teamVersionsReport("payments"); view.Dev != "dev, the same as production v1" {
		t.Fatal(view.Dev)
	}

}
