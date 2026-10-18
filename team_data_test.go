package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func writeTeamTestData(t *testing.T, slot string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		path := filepath.Join(teamSlotDataDir("payments", slot), name)
		os.MkdirAll(filepath.Dir(path), 0o700)
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// teamTestRunnable gives the project a Makefile, so production starts.
func teamTestRunnable(b *teamTestBroker) {
	os.WriteFile(filepath.Join(b.repo, "Makefile"), []byte("run:\n\t./serve\n"), 0o644)
	exec.Command("git", "-C", b.repo, "add", "-A").Run()
	exec.Command("git", "-C", b.repo, "commit", "-q", "-m", "app").Run()
}

func readTeamTestData(slot, name string) string {
	data, _ := os.ReadFile(filepath.Join(teamSlotDataDir("payments", slot), name))
	return string(data)
}

func TestTeamDataCopy(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	channel := &fakeTeamChannel{}
	b.channel = channel
	taken := 0
	b.takeSnapshot = func(kind, label string) (teamSnapshot, error) {
		taken++
		return teamSnapshot{ID: fmt.Sprintf("snap%04d", taken), Kind: kind, Label: label}, nil
	}
	posted := func() string { return channel.calls[len(channel.calls)-1] }

	// Nothing to copy to before the first release.
	writeTeamTestData(t, teamSlotPreview, map[string]string{"app.db": "533k releases", "attachments/a.pdf": "pdf"})
	if _, err := b.copyData(teamDataCopyRequest{From: "preview", To: "production", Approval: teamTestYes(t, "U07OWNER1", 40)}); err == nil || !strings.Contains(err.Error(), "--data preview") {
		t.Fatal(err)
	}
	teamTestRunnable(b)
	teamTestMerge(t, b, "Add a news feed")
	if _, err := b.release(teamReleaseRequest{Approval: teamTestYes(t, "U07OWNER1", 39), Notes: "News."}); err != nil {
		t.Fatal(err)
	}
	writeTeamTestData(t, teamSlotProduction, map[string]string{"app.db": "started over"})

	// Only an owner's yes copies to production; a slot copies to the other.
	if _, err := b.copyData(teamDataCopyRequest{From: "preview", To: "production", Approval: teamTestYes(t, "U07ASKER1", 38)}); err == nil || !strings.Contains(err.Error(), "owners") {
		t.Fatal(err)
	}
	if _, err := b.copyData(teamDataCopyRequest{From: "preview", To: "preview"}); err == nil {
		t.Fatal("copied a slot to itself")
	}
	yes := teamTestYes(t, "U07OWNER1", 37)
	result, err := b.copyData(teamDataCopyRequest{From: "preview", To: "production", Approval: yes})
	if err != nil {
		t.Fatal(err)
	}
	if readTeamTestData(teamSlotProduction, "app.db") != "533k releases" || readTeamTestData(teamSlotProduction, "attachments/a.pdf") != "pdf" ||
		readTeamTestData(teamSlotPreview, "app.db") != "533k releases" {
		t.Fatal("production doesn't have the preview's data")
	}
	if result.Files != 2 || result.Snapshot == "" || !strings.Contains(posted(), "Production now has a copy of the preview's data") ||
		!strings.Contains(posted(), "kept in snapshot "+result.Snapshot) {
		t.Fatal(result, posted())
	}
	// Production runs on it again, and the copy left nothing beside it.
	if state, ok := loadTeamSlot("payments", teamSlotProduction); !ok || !b.running(state.Container) {
		t.Fatal("production didn't start again", result.Warning, ok)
	}
	if matches, _ := filepath.Glob(filepath.Join(teamDir("payments"), ".restore-*")); len(matches) != 0 {
		t.Fatal(matches)
	}
	versions, _ := loadTeamVersions("payments")
	if last := versions.History[len(versions.History)-1]; last.What != "production's data from the preview" || last.Snapshot != result.Snapshot || last.From != 0 {
		t.Fatal(last)
	}
	// A yes copies once.
	if _, err := b.copyData(teamDataCopyRequest{From: "preview", To: "production", Approval: yes}); err == nil || !strings.Contains(err.Error(), "already approved") {
		t.Fatal(err)
	}

	// The preview gets production's data without a yes, and keeps its own once.
	if _, err := b.preview(teamPreviewRequest{}); err != nil {
		t.Fatal(err)
	}
	before, _ := loadTeamSlot("payments", teamSlotPreview)
	writeTeamTestData(t, teamSlotProduction, map[string]string{"app.db": "entered in production"})
	writeTeamTestData(t, teamSlotPreview, map[string]string{"test.txt": "tried in the preview"})
	result, err = b.copyData(teamDataCopyRequest{From: "production", To: "preview"})
	if err != nil {
		t.Fatal(err)
	}
	if readTeamTestData(teamSlotPreview, "app.db") != "entered in production" || readTeamTestData(teamSlotPreview, "test.txt") != "" {
		t.Fatal("the preview doesn't have production's data")
	}
	if kept, _ := os.ReadFile(filepath.Join(result.Kept, "test.txt")); string(kept) != "tried in the preview" {
		t.Fatal(result.Kept, string(kept))
	}
	after, _ := loadTeamSlot("payments", teamSlotPreview)
	if after.Container == before.Container || !b.running(after.Container) || after.URL != before.URL || after.Commit != before.Commit {
		t.Fatal("the preview didn't start again on the same address", before, after)
	}
	if strings.Contains(posted(), "the preview") && !strings.Contains(posted(), "Production now has") {
		t.Fatal("a copy to the preview was posted:", posted())
	}

	// An empty source copies nothing.
	os.RemoveAll(teamSlotDataDir("payments", teamSlotPreview))
	if _, err := b.copyData(teamDataCopyRequest{From: "preview", To: "production", Approval: teamTestYes(t, "U07OWNER1", 36)}); err == nil || !strings.Contains(err.Error(), "no data") {
		t.Fatal(err)
	}
	if readTeamTestData(teamSlotProduction, "app.db") != "entered in production" {
		t.Fatal("production's data changed")
	}
}

func TestTeamDataCopyNeedsTheSnapshot(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	teamTestMerge(t, b, "Add a news feed")
	b.release(teamReleaseRequest{Approval: teamTestYes(t, "U07OWNER1", 30), Notes: "News."})
	b.takeSnapshot = func(kind, label string) (teamSnapshot, error) {
		return teamSnapshot{}, errors.New("restic isn't installed")
	}
	writeTeamTestData(t, teamSlotPreview, map[string]string{"app.db": "preview"})
	writeTeamTestData(t, teamSlotProduction, map[string]string{"app.db": "production"})
	if _, err := b.copyData(teamDataCopyRequest{From: "preview", To: "production", Approval: teamTestYes(t, "U07OWNER1", 29)}); err == nil || !strings.Contains(err.Error(), "nothing was copied") {
		t.Fatal(err)
	}
	if readTeamTestData(teamSlotProduction, "app.db") != "production" {
		t.Fatal("production's data was replaced without a snapshot")
	}
	// The disk must have room for the copy.
	defer func(free func(string) int64) { teamFreeBytes = free }(teamFreeBytes)
	teamFreeBytes = func(string) int64 { return 1 << 20 }
	if _, err := b.copyData(teamDataCopyRequest{From: "production", To: "preview"}); err == nil || !strings.Contains(err.Error(), "free") {
		t.Fatal(err)
	}
}

func TestTeamReleaseWithPreviewData(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	channel := &fakeTeamChannel{}
	b.channel = channel
	b.takeSnapshot = func(kind, label string) (teamSnapshot, error) {
		return teamSnapshot{ID: "snap0001", Kind: kind, Label: label}, nil
	}
	teamTestMerge(t, b, "Add a news feed")
	// The ask says when production would start empty and the preview has data.
	if note := teamReleaseDataNote("payments"); note != "" {
		t.Fatal(note)
	}
	writeTeamTestData(t, teamSlotPreview, map[string]string{"app.db": strings.Repeat("x", 3<<20)})
	if note := teamReleaseDataNote("payments"); !strings.Contains(note, "the preview has 3 MB") {
		t.Fatal(note)
	}
	if _, err := b.release(teamReleaseRequest{Approval: teamTestYes(t, "U07OWNER1", 30), Notes: "News.", DataFrom: "production"}); err == nil {
		t.Fatal("released with --data production")
	}
	result, err := b.release(teamReleaseRequest{Approval: teamTestYes(t, "U07OWNER1", 29), Notes: "News.", DataFrom: "preview"})
	if err != nil {
		t.Fatal(err)
	}
	if result.PreviewData != 3<<20 || len(readTeamTestData(teamSlotProduction, "app.db")) != 3<<20 {
		t.Fatal(result)
	}
	if !strings.Contains(strings.Join(channel.calls, "\n"), "Production starts with a copy of the preview's data (3 MB)") {
		t.Fatal(channel.calls)
	}
	if teamReleaseDataNote("payments") != "" {
		t.Fatal("asked again once production has data")
	}
	versions, _ := loadTeamVersions("payments")
	if last := versions.History[len(versions.History)-1]; !strings.Contains(last.What, "with a copy of the preview's data") {
		t.Fatal(last)
	}
}

func TestTeamDataCommands(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	b.takeSnapshot = func(kind, label string) (teamSnapshot, error) {
		return teamSnapshot{ID: "snap0001", Kind: kind, Label: label}, nil
	}
	teamTestRunnable(b)
	teamTestMerge(t, b, "Add a news feed")
	b.release(teamReleaseRequest{Approval: teamTestYes(t, "U07OWNER1", 30), Notes: "News."})
	writeTeamTestData(t, teamSlotPreview, map[string]string{"app.db": "preview"})

	var out bytes.Buffer
	if err := teamDataCommand([]string{"payments"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "production") || !strings.Contains(out.String(), "empty") || !strings.Contains(out.String(), "7 bytes in 1 file(s)") {
		t.Fatal(out.String())
	}
	if err := teamDataCommand([]string{"payments", "copy", "preview"}, strings.NewReader(""), &out); err == nil {
		t.Fatal("copied without a target")
	}

	// The host's socket needs no yes, but asks first.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serveHost, err := b.listenHost(ctx, teamHostSocket("payments"))
	if err != nil {
		t.Fatal(err)
	}
	go serveHost()
	if err := teamDataCommand([]string{"payments", "copy", "preview", "production"}, strings.NewReader(""), &out); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatal("copied without asking:", err)
	}
	out.Reset()
	if err := teamDataCommand([]string{"payments", "copy", "preview", "production", "--yes"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err, out.String())
	}
	if readTeamTestData(teamSlotProduction, "app.db") != "preview" || !strings.Contains(out.String(), "Production has a copy of preview's data now") {
		t.Fatal(out.String())
	}
	versions, _ := loadTeamVersions("payments")
	if last := versions.History[len(versions.History)-1]; !strings.HasSuffix(last.Approval.Name, "on the machine") {
		t.Fatal(last.Approval)
	}

	// The lead's socket can't speak for someone on the machine.
	leadSocket := filepath.Join(t.TempDir(), "lead.sock")
	serveLead, _ := b.listen(ctx, leadSocket)
	go serveLead()
	t.Setenv(teamSocketEnv, leadSocket)
	out.Reset()
	if code := runTeam([]string{"data"}, strings.NewReader(""), &out, &out); code != 0 || !strings.Contains(out.String(), "$HI_SLOT") {
		t.Fatal(code, out.String())
	}
	out.Reset()
	if code := runTeam([]string{"data", "copy", "preview", "production"}, strings.NewReader(""), &out, &out); code == 0 || !strings.Contains(out.String(), "approved this work") {
		t.Fatal(code, out.String())
	}
}
