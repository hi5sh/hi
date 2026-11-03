package cli

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
		path := filepath.Join(appSlotDataDir("payments", slot), name)
		os.MkdirAll(filepath.Dir(path), 0o700)
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// appTestRunnable gives the project a Makefile, so production starts.
func appTestRunnable(b *appTestBroker) {
	os.WriteFile(filepath.Join(b.repo, "Makefile"), []byte("run:\n\t./serve\n"), 0o644)
	exec.Command("git", "-C", b.repo, "add", "-A").Run()
	exec.Command("git", "-C", b.repo, "commit", "-q", "-m", "app").Run()
}

func readTeamTestData(slot, name string) string {
	data, _ := os.ReadFile(filepath.Join(appSlotDataDir("payments", slot), name))
	return string(data)
}

func TestTeamDataCopy(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	channel := &fakeTeamChannel{}
	b.channel = channel
	taken := 0
	b.takeSnapshot = func(kind, label string) (appSnapshot, error) {
		taken++
		return appSnapshot{ID: fmt.Sprintf("snap%04d", taken), Kind: kind, Label: label}, nil
	}
	posted := func() string { return channel.calls[len(channel.calls)-1] }

	// Nothing to copy to before the first release.
	writeTeamTestData(t, appSlotPreview, map[string]string{"app.db": "533k releases", "attachments/a.pdf": "pdf"})
	if _, err := b.copyData(appDataCopyRequest{From: "preview", To: "production", owner: "Olga"}); err == nil || !strings.Contains(err.Error(), "--data preview") {
		t.Fatal(err)
	}
	appTestRunnable(b)
	appTestMerge(t, b, "Add a news feed")
	if _, err := b.release(appReleaseRequest{host: "Olga", Notes: "News."}); err != nil {
		t.Fatal(err)
	}
	writeTeamTestData(t, appSlotProduction, map[string]string{"app.db": "started over"})

	// Only an owner copies to production; a slot copies to the other.
	if _, err := b.copyData(appDataCopyRequest{From: "preview", To: "production"}); err == nil || !strings.Contains(err.Error(), "owners") {
		t.Fatal(err)
	}
	if _, err := b.copyData(appDataCopyRequest{From: "preview", To: "preview"}); err == nil {
		t.Fatal("copied a slot to itself")
	}
	result, err := b.copyData(appDataCopyRequest{From: "preview", To: "production", owner: "Olga on the dev page"})
	if err != nil {
		t.Fatal(err)
	}
	if readTeamTestData(appSlotProduction, "app.db") != "533k releases" || readTeamTestData(appSlotProduction, "attachments/a.pdf") != "pdf" ||
		readTeamTestData(appSlotPreview, "app.db") != "533k releases" {
		t.Fatal("production doesn't have the preview's data")
	}
	if result.Files != 2 || result.Snapshot == "" || !strings.Contains(posted(), "Production now has a copy of the preview's data") ||
		!strings.Contains(posted(), "kept in snapshot "+result.Snapshot) {
		t.Fatal(result, posted())
	}
	// Production runs on it again, and the copy left nothing beside it.
	if state, ok := loadTeamSlot("payments", appSlotProduction); !ok || !b.running(state.Container) {
		t.Fatal("production didn't start again", result.Warning, ok)
	}
	if matches, _ := filepath.Glob(filepath.Join(appDir("payments"), ".restore-*")); len(matches) != 0 {
		t.Fatal(matches)
	}
	versions, _ := loadTeamVersions("payments")
	if last := versions.History[len(versions.History)-1]; last.What != "production's data from the preview" || last.Snapshot != result.Snapshot || last.From != 0 {
		t.Fatal(last)
	}

	// The preview gets production's data without a yes, and keeps its own once.
	if _, err := b.preview(appPreviewRequest{}); err != nil {
		t.Fatal(err)
	}
	before, _ := loadTeamSlot("payments", appSlotPreview)
	writeTeamTestData(t, appSlotProduction, map[string]string{"app.db": "entered in production"})
	writeTeamTestData(t, appSlotPreview, map[string]string{"test.txt": "tried in the preview"})
	result, err = b.copyData(appDataCopyRequest{From: "production", To: "preview"})
	if err != nil {
		t.Fatal(err)
	}
	if readTeamTestData(appSlotPreview, "app.db") != "entered in production" || readTeamTestData(appSlotPreview, "test.txt") != "" {
		t.Fatal("the preview doesn't have production's data")
	}
	if kept, _ := os.ReadFile(filepath.Join(result.Kept, "test.txt")); string(kept) != "tried in the preview" {
		t.Fatal(result.Kept, string(kept))
	}
	after, _ := loadTeamSlot("payments", appSlotPreview)
	if after.Container == before.Container || !b.running(after.Container) || after.URL != before.URL || after.Commit != before.Commit {
		t.Fatal("the preview didn't start again on the same address", before, after)
	}
	if strings.Contains(posted(), "the preview") && !strings.Contains(posted(), "Production now has") {
		t.Fatal("a copy to the preview was posted:", posted())
	}

	// An empty source copies nothing.
	os.RemoveAll(appSlotDataDir("payments", appSlotPreview))
	if _, err := b.copyData(appDataCopyRequest{From: "preview", To: "production", owner: "Olga"}); err == nil || !strings.Contains(err.Error(), "no data") {
		t.Fatal(err)
	}
	if readTeamTestData(appSlotProduction, "app.db") != "entered in production" {
		t.Fatal("production's data changed")
	}
}

func TestTeamDataCopyNeedsTheSnapshot(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	appTestMerge(t, b, "Add a news feed")
	b.release(appReleaseRequest{host: "Olga", Notes: "News."})
	b.takeSnapshot = func(kind, label string) (appSnapshot, error) {
		return appSnapshot{}, errors.New("restic isn't installed")
	}
	writeTeamTestData(t, appSlotPreview, map[string]string{"app.db": "preview"})
	writeTeamTestData(t, appSlotProduction, map[string]string{"app.db": "production"})
	if _, err := b.copyData(appDataCopyRequest{From: "preview", To: "production", owner: "Olga"}); err == nil || !strings.Contains(err.Error(), "nothing was copied") {
		t.Fatal(err)
	}
	if readTeamTestData(appSlotProduction, "app.db") != "production" {
		t.Fatal("production's data was replaced without a snapshot")
	}
	// The disk must have room for the copy.
	defer func(free func(string) int64) { appFreeBytes = free }(appFreeBytes)
	appFreeBytes = func(string) int64 { return 1 << 20 }
	if _, err := b.copyData(appDataCopyRequest{From: "production", To: "preview"}); err == nil || !strings.Contains(err.Error(), "free") {
		t.Fatal(err)
	}
}

func TestTeamReleaseWithPreviewData(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	channel := &fakeTeamChannel{}
	b.channel = channel
	b.takeSnapshot = func(kind, label string) (appSnapshot, error) {
		return appSnapshot{ID: "snap0001", Kind: kind, Label: label}, nil
	}
	appTestMerge(t, b, "Add a news feed")
	writeTeamTestData(t, appSlotPreview, map[string]string{"app.db": strings.Repeat("x", 3<<20)})
	if _, err := b.release(appReleaseRequest{host: "Olga", Notes: "News.", DataFrom: "production"}); err == nil {
		t.Fatal("released with --data production")
	}
	result, err := b.release(appReleaseRequest{host: "Olga", Notes: "News.", DataFrom: "preview"})
	if err != nil {
		t.Fatal(err)
	}
	if result.PreviewData != 3<<20 || len(readTeamTestData(appSlotProduction, "app.db")) != 3<<20 {
		t.Fatal(result)
	}
	if !strings.Contains(strings.Join(channel.calls, "\n"), "Production starts with a copy of the preview's data (3 MB)") {
		t.Fatal(channel.calls)
	}
	versions, _ := loadTeamVersions("payments")
	if last := versions.History[len(versions.History)-1]; !strings.Contains(last.What, "with a copy of the preview's data") {
		t.Fatal(last)
	}
}

func TestTeamDataCommands(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	b.takeSnapshot = func(kind, label string) (appSnapshot, error) {
		return appSnapshot{ID: "snap0001", Kind: kind, Label: label}, nil
	}
	appTestRunnable(b)
	appTestMerge(t, b, "Add a news feed")
	b.release(appReleaseRequest{host: "Olga", Notes: "News."})
	writeTeamTestData(t, appSlotPreview, map[string]string{"app.db": "preview"})

	var out bytes.Buffer
	if err := appDataCommand([]string{"payments"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "production") || !strings.Contains(out.String(), "empty") || !strings.Contains(out.String(), "7 bytes in 1 file(s)") {
		t.Fatal(out.String())
	}
	if err := appDataCommand([]string{"payments", "copy", "preview"}, strings.NewReader(""), &out); err == nil {
		t.Fatal("copied without a target")
	}

	// The host's socket needs no yes, but asks first.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serveHost, err := b.listenHost(ctx, appHostSocket("payments"))
	if err != nil {
		t.Fatal(err)
	}
	go serveHost()
	if err := appDataCommand([]string{"payments", "copy", "preview", "production"}, strings.NewReader(""), &out); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatal("copied without asking:", err)
	}
	out.Reset()
	if err := appDataCommand([]string{"payments", "copy", "preview", "production", "--yes"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err, out.String())
	}
	if readTeamTestData(appSlotProduction, "app.db") != "preview" || !strings.Contains(out.String(), "Production has a copy of preview's data now") {
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
	t.Setenv(appSocketEnv, leadSocket)
	out.Reset()
	if code := runTeam([]string{"data"}, strings.NewReader(""), &out, &out); code != 0 || !strings.Contains(out.String(), "$HI_SLOT") {
		t.Fatal(code, out.String())
	}
	out.Reset()
	if code := runTeam([]string{"data", "copy", "preview", "production"}, strings.NewReader(""), &out, &out); code == 0 || !strings.Contains(out.String(), "dev page") {
		t.Fatal(code, out.String())
	}
}
