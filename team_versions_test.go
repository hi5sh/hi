package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// teamTestYes logs an owner's (or someone's) yes to the lead's question in
// a thread of its own, and returns its id.
func teamTestYes(t *testing.T, user string, minutesAgo int) string {
	t.Helper()
	now := time.Now()
	ts := func(seconds int) string {
		return teamSlackTS(now.Add(-time.Duration(minutesAgo)*time.Minute + time.Duration(seconds)*time.Second))
	}
	root, question, yes := ts(0), ts(1), ts(2)
	os.MkdirAll(teamChannelDir("payments"), 0o700)
	teamAppendChannel(teamChannelDir("payments"), []teamChannelMessage{
		{TS: root, User: user, Name: user, Text: "Can we ship it?"},
		{TS: question, Thread: root, Name: "team-payments", Bot: true, Text: "Shall I?"},
		{TS: yes, Thread: root, User: user, Name: user, Text: "Yes"},
	})
	return yes
}

// teamTestMerge runs a small task, has it approved, and merges it.
func teamTestMerge(t *testing.T, b *teamTestBroker, brief string) teamTask {
	t.Helper()
	task, err := b.startTask(teamRequest{Role: "coder", Brief: brief, Small: true})
	if err != nil {
		t.Fatal(err)
	}
	waitTeamTask(t, task.ID)
	b.reports[fmt.Sprintf("payments-t-%d", teamTaskNumber(task.ID)+1)] = `{"status": "complete", "summary": "Approve."}`
	review, err := b.startReview(teamRequest{Task: task.ID})
	if err != nil {
		t.Fatal(err)
	}
	waitTeamTask(t, review.ID)
	merged, err := b.merge(task.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	return merged
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
	// One yes may start two tasks, but it doesn't also release them.
	both := teamTestYes(t, "U07OWNER1", 40)
	for range 2 {
		if _, err := b.startTask(teamRequest{Role: "coder", Brief: "Both", Approval: both}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := b.release(teamReleaseRequest{Approval: both, Notes: "x"}); err == nil || !strings.Contains(err.Error(), "already approved") {
		t.Fatal(err)
	}
	for _, id := range []string{"t-1", "t-2"} {
		waitTeamTask(t, id)
		if _, err := b.discard(id); err != nil {
			t.Fatal(err)
		}
	}
	first := teamTestMerge(t, b, "Add a supplier list")
	teamTestMerge(t, b, "Add a search box")
	if view, _ = teamVersionsReport("payments"); len(view.Changes) != 2 || view.Dev != "dev, 2 changes, nothing released yet" {
		t.Fatalf("%+v", view)
	}

	// The owners are asked in the channel, with what dev has, who asked,
	// and the preview with its docs.
	b.exposed = func(name string) (netExposeState, bool) { return netExposeState{}, name == "team-payments" }
	os.MkdirAll(filepath.Join(teamDir("payments"), "run"), 0o700)
	saveTeamSlot("payments", teamPreview{Slot: teamSlotPreview, URL: "https://p.example.com/?hi_key=k", Expose: "team-payments", Relay: teamRelayStamp("payments", teamSlotPreview)})
	ask, askErr := b.askRelease("Suppliers, and a search.", "", nil)
	if askErr != nil || ask.Version != 1 || ask.Changes != 2 || ask.TS == "" {
		t.Fatalf("%+v %v", ask, askErr)
	}
	// One line in the channel, the rest in its thread.
	if headline := channel.calls[len(channel.calls)-2]; headline != "post C07PAYMENTS :package: *Ready for the first release, production v1?* (2 changes, nothing released yet) An owner's yes in the thread releases it." {
		t.Fatal(headline)
	}
	if !strings.HasPrefix(posted(), "postIn C07PAYMENTS "+ask.TS+" Dev has:\n• Add a supplier list ("+first.ID+"), ") ||
		!strings.Contains(posted(), "\nSuppliers, and a search.\n") || !strings.HasSuffix(posted(), "Try it on <https://p.example.com/?hi_key=k|the preview>, and read what changed in <https://p.example.com/docs/?hi_key=k|its docs>.\nAn owner's yes in this thread releases it as v1.") {
		t.Fatal(posted())
	}
	// Asked from an owner's DM, it goes there, and nowhere else; from
	// someone else's, in the channel.
	calls := len(channel.calls)
	if ask, err := b.askRelease("", "", &teamReleaseDM{User: "U07OWNER1", Channel: "D07OWNER", Thread: "1700000000.000900"}); err != nil || !ask.InDM ||
		len(channel.calls) != calls+1 || !strings.HasPrefix(posted(), "postIn D07OWNER 1700000000.000900 :package: *Ready for the first release, production v1?* Dev has:") ||
		!strings.HasSuffix(posted(), "Say yes here and I release it as v1; the channel gets one line about it.") {
		t.Fatal(ask, err, posted())
	}
	if ask, err := b.askRelease("", "", &teamReleaseDM{User: "U07OWNER1"}); err != nil || !ask.InDM || !strings.HasPrefix(posted(), "dm U07OWNER1 :package:") {
		t.Fatal(ask, err, posted())
	}
	if ask, err := b.askRelease("", "", &teamReleaseDM{User: "U07ASKER"}); err != nil || ask.InDM || !strings.HasPrefix(channel.calls[len(channel.calls)-2], "post C07PAYMENTS :package:") {
		t.Fatal(ask, err, channel.calls)
	}
	// Asked for in a message, the summary goes in its thread.
	request := teamTestYes(t, "U07OWNER1", 35)
	config, _ := loadTeam("payments")
	thread, _ := b.teamFindThread(config, request)
	if _, err := b.askRelease("", request, nil); err != nil || thread == "" || !strings.HasPrefix(posted(), "postIn C07PAYMENTS "+thread+" :package: *Ready for the first release") {
		t.Fatal(err, posted())
	}

	// Only an owner's yes releases, with notes for the people.
	asker := teamTestYes(t, "U07ASKER", 30)
	owner := teamTestYes(t, "U07OWNER1", 20)
	if _, err := b.release(teamReleaseRequest{Approval: owner}); err == nil || !strings.Contains(err.Error(), "--notes") {
		t.Fatal(err)
	}
	if _, err := b.release(teamReleaseRequest{Approval: asker, Notes: "x"}); err == nil || !strings.Contains(err.Error(), "isn't one of the team's owners") {
		t.Fatal(err)
	}
	result, err := b.release(teamReleaseRequest{Approval: owner, Notes: "- A list of suppliers\n- Search"})
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
		!strings.HasPrefix(posted(), "postIn C07PAYMENTS ") || !strings.Contains(posted(), " :rocket: *Production v1 is out*, the first release, approved by U07OWNER1.\n\n- A list of suppliers") {
		t.Fatal(headline, posted())
	}
	if len(result.Release.Changes) != 2 || !strings.HasPrefix(result.Release.Changes[0], first.ID+" ") {
		t.Fatal(result.Release.Changes)
	}
	if _, err := b.release(teamReleaseRequest{Approval: owner, Notes: "x"}); err == nil || !strings.Contains(err.Error(), "already approved") {
		t.Fatal(err)
	}
	if _, err := b.release(teamReleaseRequest{Approval: teamTestYes(t, "U07OWNER1", 19), Notes: "x"}); err == nil || !strings.Contains(err.Error(), "nothing new since production v1") {
		t.Fatal(err)
	}
	if _, err := b.askRelease("", "", nil); err == nil || !strings.Contains(err.Error(), "nothing new since production v1") {
		t.Fatal(err)
	}
	if view, _ = teamVersionsReport("payments"); view.Dev != "dev, the same as production v1" {
		t.Fatal(view.Dev)
	}

	export := teamTestMerge(t, b, "Export to a spreadsheet")
	if view, _ = teamVersionsReport("payments"); view.Dev != "dev, v1 + 1 change" || view.Changes[0].ID != export.ID {
		t.Fatalf("%+v", view)
	}
	if _, err := b.release(teamReleaseRequest{Approval: teamTestYes(t, "U07OWNER1", 18), Notes: "Export"}); err != nil {
		t.Fatal(err)
	}
	// Back to v1, then on to v2 again.
	if result, err = b.release(teamReleaseRequest{Version: 1, Approval: teamTestYes(t, "U07OWNER1", 17)}); err != nil || !result.Back {
		t.Fatal(result, err)
	}
	if boxGit(b.repo, "rev-parse", "production") != head || !strings.Contains(posted(), "*Production is back on v1* (it was on v2)") {
		t.Fatal(posted())
	}
	if view, _ = teamVersionsReport("payments"); view.Production != 1 || view.Dev != "dev, v1 + 1 change" {
		t.Fatalf("%+v", view)
	}
	if _, err := b.release(teamReleaseRequest{Version: 1, Approval: teamTestYes(t, "U07OWNER1", 16)}); err == nil || !strings.Contains(err.Error(), "on v1 already") {
		t.Fatal(err)
	}

	// Dev back to production: the export is set aside, the specs stay.
	os.WriteFile(filepath.Join(b.repo, "docs", "specs", "export.md"), []byte("Status: dropped\n"), 0o644)
	exportFile := filepath.Join(b.repo, export.Box+".txt")
	if !fileExists(exportFile) {
		t.Fatal("the export isn't in dev")
	}
	set, err := b.resetDev(teamTestYes(t, "U07ASKER", 15))
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

	// At most five ideas wait unmerged; continuing one adds none.
	var ideas []teamTask
	for i := range teamMaxIdeas {
		task, err := b.startTask(teamRequest{Role: "coder", Brief: fmt.Sprintf("Idea %d", i), Small: true})
		if err != nil {
			t.Fatal(err)
		}
		ideas = append(ideas, waitTeamTask(t, task.ID))
	}
	if _, err := b.startTask(teamRequest{Role: "coder", Brief: "One more", Small: true}); err == nil || !strings.Contains(err.Error(), "5 ideas aren't merged") {
		t.Fatal(err)
	}
	more, err := b.startTask(teamRequest{Role: "coder", Brief: "Keep going", On: ideas[0].ID})
	if err != nil {
		t.Fatal(err)
	}
	more = waitTeamTask(t, more.ID)
	if _, err := b.merge(ideas[1].ID, ""); err == nil {
		t.Fatal("merged without a review")
	}
	discarded, err := b.discard(more.ID)
	if err != nil || len(discarded) != 2 {
		t.Fatal(discarded, err)
	}
	if boxGit(b.repo, "branch", "--list", ideas[0].Branch, more.Branch) != "" {
		t.Fatal("a discarded idea's branches are kept")
	}
	if _, err := b.startTask(teamRequest{Role: "coder", Brief: "x", On: ideas[0].ID}); err == nil {
		t.Fatal("continued a discarded idea")
	}
	last, err := b.startTask(teamRequest{Role: "coder", Brief: "One more", Small: true})
	if err != nil {
		t.Fatal(err)
	}
	waitTeamTask(t, last.ID)
	if _, err := b.discard(first.ID); err == nil || !strings.Contains(err.Error(), "in dev already") {
		t.Fatal(err)
	}
}
