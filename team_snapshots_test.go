package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// fakeTeamRestic records restic's calls and answers backup with a summary.
type fakeTeamRestic struct {
	calls [][]string
	envs  [][]string
	fail  string // a subcommand that fails
	// restore, when set, makes restore's target from it.
	restore func(target string)
	list    string
}

func (f *fakeTeamRestic) install(t *testing.T) {
	saved := teamRestic
	teamRestic = func(env []string, dir string, args ...string) (string, error) {
		f.calls = append(f.calls, args)
		f.envs = append(f.envs, env)
		if args[0] == f.fail {
			return "", teamResticError{code: 1, text: args[0] + " broke"}
		}
		switch args[0] {
		case "init":
			os.MkdirAll(filepath.Join(teamSnapshotsDir("payments"), "repo"), 0o700)
			os.WriteFile(filepath.Join(teamSnapshotsDir("payments"), "repo", "config"), []byte("x"), 0o600)
		case "backup":
			return fmt.Sprintf("{\"message_type\":\"summary\",\"data_added\":2048,\"total_bytes_processed\":4096,\"snapshot_id\":\"%08dabcdef\"}\n", len(f.calls)), nil
		case "snapshots":
			return f.list, nil
		case "restore":
			if f.restore != nil {
				f.restore(args[slices.Index(args, "--target")+1])
			}
		}
		return "", nil
	}
	t.Cleanup(func() { teamRestic = saved })
}

func (f *fakeTeamRestic) find(command string) []string {
	for _, call := range f.calls {
		if call[0] == command {
			return call
		}
	}
	return nil
}

func writeTeamFolderForSnapshots(t *testing.T) string {
	t.Helper()
	dir := teamDir("payments")
	for path, content := range map[string]string{
		"lead/SOUL.md": "soul", "memory/pg": "rows", "tasks/t-1.json": "{}", "app/entries.json": "[1]",
		"secrets.json": "{}", "run/home/x": "x", "run/proxy/network.log": "log", ".restore-old/x": "x",
		"versions.json": "{}",
	} {
		writeSkillTestFile(t, filepath.Join(dir, path), content, 0o600)
	}
	saveTeam(testTeamConfig())
	return dir
}

func TestTeamSnapshotPaths(t *testing.T) {
	setupTeamTest(t)
	dir := writeTeamFolderForSnapshots(t)
	paths, err := teamSnapshotPaths(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"app", "lead", "memory", "tasks", "team.json", "versions.json", "run/proxy/network.log"}
	if strings.Join(paths, " ") != strings.Join(want, " ") {
		t.Fatal(paths)
	}
}

func TestTeamTakeSnapshot(t *testing.T) {
	setupTeamTest(t)
	writeTeamFolderForSnapshots(t)
	restic := &fakeTeamRestic{}
	restic.install(t)
	var paused, thawed int
	pause := func(team string) (func(), string) {
		paused++
		return func() { thawed++ }, "Taken while x ran: they didn't pause."
	}

	snapshot, err := teamTakeSnapshot("payments", teamSnapshotRelease, "v3", pause)
	if err != nil {
		t.Fatal(err)
	}
	// The first snapshot makes the repository and its key, outside the folder.
	key, _ := os.ReadFile(teamSnapshotKey("payments"))
	if restic.calls[0][0] != "init" || len(strings.TrimSpace(string(key))) < 40 || strings.HasPrefix(teamSnapshotsDir("payments"), teamDir("payments")) {
		t.Fatal(restic.calls, string(key))
	}
	backup := strings.Join(restic.find("backup"), " ")
	if !strings.Contains(backup, "--tag hi-team,kind:release,label:v3") || !strings.Contains(backup, "--host hi-team-payments") ||
		!strings.Contains(backup, "--exclude *.sock") || !strings.Contains(backup, "--exclude postmaster.pid") || strings.Contains(backup, "secrets.json") || !strings.HasSuffix(backup, " run/proxy/network.log") {
		t.Fatal(backup)
	}
	if paused != 1 || thawed != 1 || snapshot.ID != "00000002" || snapshot.Added != 2048 || snapshot.why() != "before v3" || !strings.Contains(snapshot.Warning, "didn't pause") {
		t.Fatal(paused, thawed, snapshot)
	}
	if restic.find("forget") != nil {
		t.Fatal("only a daily snapshot forgets old ones")
	}
	if status := loadTeamSnapshotStatus("payments"); status.Last == nil || status.Last.ID != snapshot.ID {
		t.Fatal(status)
	}

	// A daily one forgets by the team's policy, and keeps every release's,
	// after the snapshot rather than while the boxes wait.
	config := testTeamConfig()
	config.Backups = &teamBackups{Daily: 3, Weekly: 2, Monthly: 1}
	saveTeam(config)
	if _, err := teamTakeSnapshot("payments", teamSnapshotDaily, "", teamNoPause); err != nil || restic.find("forget") != nil {
		t.Fatal(err, restic.calls)
	}
	if err := teamAfterSnapshot("payments", teamSnapshotDaily); err != nil {
		t.Fatal(err)
	}
	forget := strings.Join(restic.find("forget"), " ")
	if !strings.Contains(forget, "--keep-daily 3 --keep-weekly 2 --keep-monthly 1") || !strings.Contains(forget, "--keep-tag kind:release") || !strings.Contains(forget, "--prune") {
		t.Fatal(forget)
	}

	// A failure thaws the boxes and is shown until a snapshot works.
	restic.fail = "backup"
	if _, err := teamTakeSnapshot("payments", teamSnapshotManual, "", pause); err == nil || thawed != 2 {
		t.Fatal(err, thawed)
	}
	if line := teamSnapshotLine("payments"); !strings.Contains(line, "failed") || !strings.Contains(line, "backup broke") {
		t.Fatal(line)
	}
	restic.fail = ""
	if _, err := teamTakeSnapshot("payments", teamSnapshotManual, "", teamNoPause); err != nil {
		t.Fatal(err)
	}
	if line := teamSnapshotLine("payments"); strings.Contains(line, "failed") || !strings.Contains(line, "by hand") || !strings.Contains(line, "Not copied offsite") {
		t.Fatal(line)
	}
}

func TestTeamCopyOffsite(t *testing.T) {
	setupTeamTest(t)
	writeTeamFolderForSnapshots(t)
	restic := &fakeTeamRestic{}
	restic.install(t)
	if err := teamCopyOffsite("payments", true); err != nil || len(restic.calls) != 0 {
		t.Fatal("no bucket, no copy:", err, restic.calls)
	}
	config := testTeamConfig()
	config.Backups = &teamBackups{Offsite: "s3:https://s3.example.com/backups/payments"}
	saveTeam(config)
	saveTeamSecrets("payments", teamSecrets{BackupKeyID: "AKID", BackupSecret: "SECRET"})

	// A bucket without a repository gets one, with the local chunker.
	saved := teamRestic
	teamRestic = func(env []string, dir string, args ...string) (string, error) {
		if args[0] == "cat" {
			restic.calls = append(restic.calls, args)
			return "", teamResticError{code: teamResticMissingRepo, text: "no repository"}
		}
		return saved(env, dir, args...)
	}
	if err := teamCopyOffsite("payments", true); err != nil {
		t.Fatal(err)
	}
	var commands []string
	for _, call := range restic.calls {
		commands = append(commands, call[0])
	}
	if strings.Join(commands, " ") != "cat init copy forget" || !slices.Contains(restic.find("init"), "--copy-chunker-params") {
		t.Fatal(restic.calls)
	}
	env := strings.Join(restic.envs[len(restic.envs)-1], " ")
	if !strings.Contains(env, "RESTIC_REPOSITORY=s3:https://s3.example.com/backups/payments") || !strings.Contains(env, "AWS_SECRET_ACCESS_KEY=SECRET") || !strings.Contains(env, "AWS_DEFAULT_REGION=us-east-1") ||
		!strings.Contains(env, "RESTIC_FROM_REPOSITORY="+filepath.Join(teamSnapshotsDir("payments"), "repo")) {
		t.Fatal(env)
	}
	if line := teamSnapshotLine("payments"); !strings.Contains(line, "Copied offsite") {
		t.Fatal(line)
	}

	// A bucket that can't be reached isn't initialized over.
	teamRestic = func(env []string, dir string, args ...string) (string, error) {
		return "", teamResticError{code: 1, text: "connection refused"}
	}
	if err := teamCopyOffsite("payments", false); err == nil || !strings.Contains(err.Error(), "reaching") {
		t.Fatal(err)
	}
	if line := teamSnapshotLine("payments"); !strings.Contains(line, "failed") {
		t.Fatal(line)
	}
}

func TestTeamBrokerSnapshot(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	channel := &fakeTeamChannel{}
	b.channel = channel
	var taken []string
	b.takeSnapshot = func(kind, label string) (teamSnapshot, error) {
		// Nothing else may change the team while it is copied.
		if b.teamBroker.mu.TryLock() || b.previewMu.TryLock() {
			t.Error("a snapshot was taken without the broker's locks")
		}
		taken = append(taken, kind+" "+label)
		if label == "v2" {
			return teamSnapshot{}, errors.New("restic isn't installed on this machine")
		}
		return teamSnapshot{ID: fmt.Sprintf("snap%04d", len(taken)), Kind: kind, Label: label}, nil
	}

	// A release takes one first, labelled with its version.
	teamTestMerge(t, b, "Add a supplier list")
	result, err := b.release(teamReleaseRequest{Approval: teamTestYes(t, "U07OWNER1", 5), Notes: "Suppliers."})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(taken, "; ") != "release v1" || result.Snapshot != "snap0001" || strings.Contains(result.Warning, "snapshot") {
		t.Fatal(taken, result)
	}
	// A failed one doesn't stop the release; the owners hear of it.
	teamTestMerge(t, b, "Add a search box")
	result, err = b.release(teamReleaseRequest{Approval: teamTestYes(t, "U07OWNER1", 4), Notes: "Search."})
	if err != nil || result.Release.Version != 2 || !strings.Contains(result.Warning, "No snapshot was taken first") {
		t.Fatal(err, result)
	}
	if !slices.ContainsFunc(channel.calls, func(call string) bool {
		return strings.Contains(call, "<@U07OWNER1>") && strings.Contains(call, "couldn't take the team's snapshot (before v2)")
	}) {
		t.Fatal(channel.calls)
	}
	// Going back to an earlier release takes one too; a refused move doesn't.
	if _, err := b.release(teamReleaseRequest{Version: 2, Approval: teamTestYes(t, "U07OWNER1", 3)}); err == nil {
		t.Fatal("production is on v2 already")
	}
	if _, err := b.release(teamReleaseRequest{Version: 1, Approval: teamTestYes(t, "U07OWNER1", 2)}); err != nil {
		t.Fatal(err)
	}
	if strings.Join(taken, "; ") != "release v1; release v2; release v1" {
		t.Fatal(taken)
	}

	// The host asks for one on its own socket.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serve, err := b.listenHost(ctx, teamHostSocket("payments"))
	if err != nil {
		t.Fatal(err)
	}
	go serve()
	var out bytes.Buffer
	if err := teamSnapshotCommand([]string{"payments", "--daily"}, &out); err != nil {
		t.Fatal(err)
	}
	if taken[len(taken)-1] != "daily " || !strings.Contains(out.String(), "Snapshot snap0004 of team payments") {
		t.Fatal(taken, out.String())
	}
}

func TestTeamSnapshotWithTeamDown(t *testing.T) {
	setupTeamTest(t)
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	writeTeamFolderForSnapshots(t)
	restic := &fakeTeamRestic{}
	restic.install(t)
	saved := teamPause
	paused := 0
	teamPause = func(string) (func(), string) { paused++; return func() {}, "" }
	t.Cleanup(func() { teamPause = saved })
	var out bytes.Buffer
	if err := teamSnapshotCommand([]string{"payments"}, &out); err != nil {
		t.Fatal(err)
	}
	// Production's box may still run while the team is down.
	if paused != 1 || !strings.Contains(strings.Join(restic.find("backup"), " "), "kind:manual") || !strings.Contains(out.String(), "4.0 KB, 2.0 KB new") {
		t.Fatal(paused, restic.calls, out.String())
	}
}

func TestTeamFindSnapshot(t *testing.T) {
	day := func(d, h int) time.Time { return time.Date(2026, 10, d, h, 0, 0, 0, time.Local) }
	snapshots := []teamSnapshot{
		{ID: "aaaa1111", Time: day(11, 3), Kind: teamSnapshotDaily},
		{ID: "bbbb2222", Time: day(12, 3), Kind: teamSnapshotDaily},
		{ID: "cccc3333", Time: day(12, 15), Kind: teamSnapshotRelease, Label: "v3"},
		{ID: "abcd4444", Time: day(13, 3), Kind: teamSnapshotManual},
	}
	for which, want := range map[string]string{"latest": "abcd4444", "v3": "cccc3333", "2026-10-12": "cccc3333", "2026-10-11": "aaaa1111", "bbbb": "bbbb2222", "cccc3333": "cccc3333"} {
		if got, err := teamFindSnapshot(snapshots, which); err != nil || got.ID != want {
			t.Errorf("%s: %s %v", which, got.ID, err)
		}
	}
	for which, want := range map[string]string{"v4": "before v4", "2026-10-01": "on 2026-10-01", "zzzz": "no snapshot zzzz", "abc": "no snapshot abc"} {
		if _, err := teamFindSnapshot(snapshots, which); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v", which, err)
		}
	}
	snapshots = append(snapshots, teamSnapshot{ID: "aaaa5555", Time: day(14, 3)})
	if _, err := teamFindSnapshot(snapshots, "aaaa"); err == nil || !strings.Contains(err.Error(), "more than one") {
		t.Fatal(err)
	}
	if _, err := teamFindSnapshot(nil, "latest"); err == nil || !strings.Contains(err.Error(), "no snapshots yet") {
		t.Fatal(err)
	}
}

func TestTeamRestore(t *testing.T) {
	setupTeamTest(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dir := writeTeamFolderForSnapshots(t)
	writeSkillTestFile(t, filepath.Join(dir, "repo", "README.md"), "today's code", 0o644)
	writeSkillTestFile(t, filepath.Join(dir, "channel", "log.jsonl"), "newer than the snapshot", 0o600)
	os.MkdirAll(filepath.Join(teamSnapshotsDir("payments"), "repo"), 0o700)
	os.WriteFile(filepath.Join(teamSnapshotsDir("payments"), "repo", "config"), []byte("x"), 0o600)
	os.WriteFile(teamSnapshotKey("payments"), []byte("key\n"), 0o600)
	restic := &fakeTeamRestic{list: `[{"short_id":"1a2b3c4d","time":"2026-10-12T03:00:00Z","tags":["hi-team","kind:daily"],"summary":{"data_added":10,"total_bytes_processed":100}}]`}
	restic.install(t)
	restic.restore = func(target string) {
		for path, content := range map[string]string{"lead/SOUL.md": "the old soul", "app/entries.json": "[]", "team.json": `{"name":"payments","channel":"C07OLD"}`, "repo/README.md": "old code"} {
			writeSkillTestFile(t, filepath.Join(target, path), content, 0o600)
		}
	}
	var calls []string
	var posted string
	var stopped bool
	savedSystemctl, savedStop, savedPost := teamSystemctl, teamStopProduction, teamRestorePost
	teamSystemctl = func(args ...string) (string, error) {
		calls = append(calls, strings.Join(args, " "))
		if args[0] == "is-active" {
			return "active", nil
		}
		return "", nil
	}
	teamStopProduction = func(string) { stopped = true }
	teamRestorePost = func(config teamConfig, text string) error { posted = text; return nil }
	t.Cleanup(func() { teamSystemctl, teamStopProduction, teamRestorePost = savedSystemctl, savedStop, savedPost })

	var out bytes.Buffer
	if err := teamRestoreCommand([]string{"payments", "1a2b"}, strings.NewReader(""), &out); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatal("restored without asking:", err)
	}
	if err := teamRestoreCommand([]string{"payments", "1a2b", "--yes"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err, out.String())
	}
	read := func(path string) string { data, _ := os.ReadFile(filepath.Join(dir, path)); return string(data) }
	// What the snapshot holds is back, and what is newer is gone; the
	// settings, run/, and the code stay.
	if config, _ := loadTeam("payments"); config.Channel != "C07PAYMENTS" {
		t.Fatal("team.json is the team's settings, and stays:", config)
	}
	if read("lead/SOUL.md") != "the old soul" || read("app/entries.json") != "[]" || fileExists(filepath.Join(dir, "channel")) ||
		fileExists(filepath.Join(dir, "memory")) || read("secrets.json") != "{}" || read("run/proxy/network.log") != "log" || read("repo/README.md") != "today's code" {
		t.Fatal("the folder isn't as the snapshot had it")
	}
	if matches, _ := filepath.Glob(filepath.Join(dir, ".restore-1a2b3c4d*")); len(matches) != 0 {
		t.Fatal(matches)
	}
	// A snapshot of how things were comes first, with the team stopped.
	backup := strings.Join(restic.find("backup"), " ")
	restore := strings.Join(restic.find("restore"), " ")
	if !strings.Contains(backup, "kind:restore,label:1a2b3c4d") || !strings.Contains(restore, "--exclude /repo") || !strings.Contains(restore, "--exclude /run") || !strings.Contains(restore, "--exclude /team.json") {
		t.Fatal(backup, restore)
	}
	if strings.Join(calls, "; ") != "is-active hi-team-payments.service; stop hi-team-payments.service; start hi-team-payments.service" || !stopped {
		t.Fatal(calls, stopped)
	}
	if !strings.Contains(posted, "back as it was") || !strings.Contains(posted, "hi team restore payments 0000000") || !strings.Contains(out.String(), "undoes it: hi team restore payments 0000000") {
		t.Fatal(posted, out.String())
	}

	// --code puts the code back too.
	writeSkillTestFile(t, filepath.Join(dir, "lead", "SOUL.md"), "x", 0o600)
	if err := teamRestoreCommand([]string{"payments", "latest", "--code", "--yes"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	if read("repo/README.md") != "old code" || slices.Contains(restic.calls[len(restic.calls)-1], "/repo") {
		t.Fatal(read("repo/README.md"), restic.calls[len(restic.calls)-1])
	}

	// When the snapshot of how things are fails, nothing is restored and
	// the team starts again.
	calls = nil
	writeSkillTestFile(t, filepath.Join(dir, "lead", "SOUL.md"), "today", 0o600)
	restic.fail = "backup"
	if err := teamRestoreCommand([]string{"payments", "latest", "--yes"}, strings.NewReader(""), &out); err == nil || !strings.Contains(err.Error(), "nothing was restored") {
		t.Fatal(err)
	}
	if read("lead/SOUL.md") != "today" || calls[len(calls)-1] != "start hi-team-payments.service" {
		t.Fatal(read("lead/SOUL.md"), calls)
	}
}

func TestTeamBackupsCommand(t *testing.T) {
	setupTeamTest(t)
	writeTeamFolderForSnapshots(t)
	restic := &fakeTeamRestic{}
	restic.install(t)
	var out bytes.Buffer
	if err := teamBackupsCommand([]string{"payments", "--keep", "7,4"}, strings.NewReader(""), &out); err == nil {
		t.Fatal("--keep takes three counts")
	}
	if err := teamBackupsCommand([]string{"payments", "--offsite", "s3.example.com/bucket"}, strings.NewReader(""), &out); err == nil {
		t.Fatal("an offsite URL is restic's s3:https://...")
	}
	t.Setenv("HI_TEAM_S3_ACCESS_KEY_ID", "AKID")
	t.Setenv("HI_TEAM_S3_SECRET_ACCESS_KEY", "SECRET")
	if err := teamBackupsCommand([]string{"payments", "--offsite", "s3:https://s3.example.com/bucket", "--keep", "14,8,12"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	config, _ := loadTeam("payments")
	secrets, _ := loadTeamSecrets("payments")
	if config.backups() != (teamBackups{Offsite: "s3:https://s3.example.com/bucket", Daily: 14, Weekly: 8, Monthly: 12}) || secrets.BackupSecret != "SECRET" {
		t.Fatal(config.backups(), secrets)
	}
	// The keys stay out of team.json, which snapshots hold.
	if data, _ := os.ReadFile(filepath.Join(teamDir("payments"), "team.json")); strings.Contains(string(data), "SECRET") {
		t.Fatal(string(data))
	}
	if restic.find("copy") == nil || !strings.Contains(out.String(), "--show-key") {
		t.Fatal(restic.calls, out.String())
	}
	out.Reset()
	if err := teamBackupsCommand([]string{"payments", "--show-key"}, strings.NewReader(""), &out); err != nil || len(strings.TrimSpace(out.String())) < 40 {
		t.Fatal(err, out.String())
	}
	if err := teamBackupsCommand([]string{"payments", "--offsite", "off"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	config, _ = loadTeam("payments")
	secrets, _ = loadTeamSecrets("payments")
	if config.backups().Offsite != "" || secrets.BackupSecret != "" || config.backups().Daily != 14 {
		t.Fatal(config.backups(), secrets)
	}
}

func TestTeamAppDataFolders(t *testing.T) {
	setupTeamTest(t)
	if teamSlotDataDir("payments", teamSlotProduction) != filepath.Join(teamDir("payments"), "app") ||
		teamSlotDataDir("payments", teamSlotPreview) != filepath.Join(teamDir("payments"), "run", "preview-data") {
		t.Fatal(teamSlotDataDir("payments", teamSlotProduction), teamSlotDataDir("payments", teamSlotPreview))
	}
	if env := teamAppEnv("payments", teamSlotProduction, 8080); env["HI_DATA_DIR"] != "/data" || env["HI_SLOT"] != "production" {
		t.Fatal(env)
	}
	if env := teamAppEnv("payments", teamSlotPreview, 8080); env["HI_SLOT"] != "preview" {
		t.Fatal(env)
	}
	if !teamAppEnvReserved["HI_DATA_DIR"] || !teamAppEnvReserved["HI_SLOT"] {
		t.Fatal("HI_DATA_DIR and HI_SLOT are hi's")
	}
}

// TestTeamSnapshotRestic takes a real snapshot and restores it, with
// restic from PATH.
func TestTeamSnapshotRestic(t *testing.T) {
	if _, err := exec.LookPath("restic"); err != nil {
		t.Skip("restic isn't installed")
	}
	setupTeamTest(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dir := writeTeamFolderForSnapshots(t)
	saved := teamSystemctl
	teamSystemctl = func(args ...string) (string, error) { return "inactive", nil }
	t.Cleanup(func() { teamSystemctl = saved })
	savedPost, savedStop := teamRestorePost, teamStopProduction
	teamRestorePost = func(teamConfig, string) error { return nil }
	teamStopProduction = func(string) {}
	t.Cleanup(func() { teamRestorePost, teamStopProduction = savedPost, savedStop })

	first, err := teamTakeSnapshot("payments", teamSnapshotDaily, "", teamNoPause)
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "app", "entries.json"), []byte("[1,2]"), 0o600)
	os.WriteFile(filepath.Join(dir, "app", "new.json"), []byte("new"), 0o600)
	if _, err := teamTakeSnapshot("payments", teamSnapshotRelease, "v1", teamNoPause); err != nil {
		t.Fatal(err)
	}
	snapshots, err := teamListSnapshots("payments")
	if err != nil || len(snapshots) != 2 || snapshots[0].ID != first.ID || snapshots[1].why() != "before v1" {
		t.Fatal(err, snapshots)
	}
	var out bytes.Buffer
	if err := teamRestoreCommand([]string{"payments", first.ID, "--yes"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err, out.String())
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "app", "entries.json")); string(data) != "[1]" || fileExists(filepath.Join(dir, "app", "new.json")) {
		t.Fatal(string(data))
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "secrets.json")); string(data) != "{}" {
		t.Fatal(string(data))
	}
	out.Reset()
	if err := teamSnapshotsCommand("payments", &out); err != nil || !strings.Contains(out.String(), "before restoring "+first.ID) {
		t.Fatal(err, out.String())
	}

	// A rollback puts back app/ alone.
	os.WriteFile(filepath.Join(dir, "app", "entries.json"), []byte("[9]"), 0o600)
	os.WriteFile(filepath.Join(dir, "lead", "SOUL.md"), []byte("today"), 0o600)
	snapshots, _ = teamListSnapshots("payments")
	if err := teamRestoreData("payments", snapshots[1].ID); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "app", "entries.json")); string(data) != "[1,2]" || !fileExists(filepath.Join(dir, "app", "new.json")) {
		t.Fatal(string(data))
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "lead", "SOUL.md")); string(data) != "today" {
		t.Fatal(string(data))
	}
	if matches, _ := filepath.Glob(filepath.Join(dir, ".restore-"+snapshots[1].ID+"*")); len(matches) != 0 {
		t.Fatal(matches)
	}
}

func TestTeamRollback(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	channel := &fakeTeamChannel{}
	b.channel = channel
	taken, failSnapshot := 0, false
	b.takeSnapshot = func(kind, label string) (teamSnapshot, error) {
		if failSnapshot {
			return teamSnapshot{}, errors.New("the disk is full")
		}
		taken++
		return teamSnapshot{ID: fmt.Sprintf("snap%04d", taken), Kind: kind, Label: label}, nil
	}
	var putBack []string
	b.restoreData = func(id string) error { putBack = append(putBack, id); return nil }
	posted := func() string { return channel.calls[len(channel.calls)-1] }

	teamTestMerge(t, b, "Add a supplier list")
	if _, err := b.release(teamReleaseRequest{Approval: teamTestYes(t, "U07OWNER1", 30), Notes: "Suppliers."}); err != nil {
		t.Fatal(err)
	}
	teamTestMerge(t, b, "Add a search box")
	if _, err := b.release(teamReleaseRequest{Approval: teamTestYes(t, "U07OWNER1", 29), Notes: "Search."}); err != nil {
		t.Fatal(err)
	}
	// Each move records the snapshot taken as production left a version.
	versions, _ := loadTeamVersions("payments")
	if left, ok := versions.leftSnapshot(1); !ok || left.Snapshot != "snap0002" || left.To != 2 {
		t.Fatal(versions.History)
	}

	// Only an owner's yes rolls back.
	if _, err := b.release(teamReleaseRequest{Version: 1, Data: true, Approval: teamTestYes(t, "U07ASKER1", 28)}); err == nil || !strings.Contains(err.Error(), "owners") {
		t.Fatal(err)
	}
	// Without a snapshot of now, nothing moves.
	failSnapshot = true
	if _, err := b.release(teamReleaseRequest{Version: 1, Data: true, Approval: teamTestYes(t, "U07OWNER1", 27)}); err == nil || !strings.Contains(err.Error(), "nothing was rolled back") {
		t.Fatal(err)
	}
	if versions, _ = loadTeamVersions("payments"); versions.Production != 2 || len(putBack) != 0 {
		t.Fatal(versions.Production, putBack)
	}
	failSnapshot = false

	// Back to v1, with production's data as v1 left it.
	result, err := b.release(teamReleaseRequest{Version: 1, Data: true, Approval: teamTestYes(t, "U07OWNER1", 26)})
	if err != nil {
		t.Fatal(err)
	}
	if result.DataFrom != "snap0002" || result.Snapshot != "snap0003" || strings.Join(putBack, " ") != "snap0002" || boxGit(b.repo, "rev-parse", teamProductionBranch) != result.Release.Commit {
		t.Fatal(result, putBack)
	}
	if !strings.Contains(posted(), "Production is back on v1, with its data as of") || !strings.Contains(posted(), "kept in snapshot snap0003") {
		t.Fatal(posted())
	}
	// Forward to v2 again puts back what was entered under v2.
	if _, err := b.release(teamReleaseRequest{Version: 2, Data: true, Approval: teamTestYes(t, "U07OWNER1", 25)}); err != nil {
		t.Fatal(err)
	}
	if strings.Join(putBack, " ") != "snap0002 snap0003" || !strings.Contains(posted(), "Production is on v2 again, with its data") {
		t.Fatal(putBack, posted())
	}
	// A plain move back keeps the data, and is still snapshotted.
	if _, err := b.release(teamReleaseRequest{Version: 1, Approval: teamTestYes(t, "U07OWNER1", 24)}); err != nil {
		t.Fatal(err)
	}
	if len(putBack) != 2 || taken != 5 {
		t.Fatal(putBack, taken)
	}

	// The lead's socket can't skip the yes; the host's needs none.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	leadSocket := filepath.Join(t.TempDir(), "lead.sock")
	serveLead, _ := b.listen(ctx, leadSocket)
	go serveLead()
	t.Setenv(teamSocketEnv, leadSocket)
	var out bytes.Buffer
	if code := runTeam([]string{"rollback", "v2"}, strings.NewReader(""), &out, &out); code == 0 || !strings.Contains(out.String(), "approved this work") {
		t.Fatal(code, out.String())
	}
	t.Setenv(teamSocketEnv, "")
	serveHost, err := b.listenHost(ctx, teamHostSocket("payments"))
	if err != nil {
		t.Fatal(err)
	}
	go serveHost()
	out.Reset()
	if err := teamRollbackCommand([]string{"payments", "v2"}, strings.NewReader(""), &out); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatal("rolled back without asking:", err)
	}
	if err := teamRollbackCommand([]string{"payments", "v2", "--yes"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err, out.String())
	}
	// v2 was last left by the plain move back, snapshot snap0005.
	if putBack[len(putBack)-1] != "snap0005" || !strings.Contains(out.String(), "Production is on v2 now (it was on v1). Its data is from snapshot snap0005") ||
		!strings.Contains(posted(), "on the machine") {
		t.Fatal(putBack, out.String(), posted())
	}
	if err := teamRollbackCommand([]string{"payments", "v1", "--code-only", "--yes"}, strings.NewReader(""), &out); err != nil || len(putBack) != 3 {
		t.Fatal(err, putBack)
	}
	if err := teamRollbackCommand([]string{"payments", "v9", "--yes"}, strings.NewReader(""), &out); err == nil || !strings.Contains(err.Error(), "no v9") {
		t.Fatal(err)
	}
}

func TestTeamRollbackNeedsTheDataSnapshot(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	b.takeSnapshot = func(kind, label string) (teamSnapshot, error) {
		return teamSnapshot{}, errors.New("restic isn't installed")
	}
	teamTestMerge(t, b, "Add a supplier list")
	b.release(teamReleaseRequest{Approval: teamTestYes(t, "U07OWNER1", 30), Notes: "Suppliers."})
	teamTestMerge(t, b, "Add a search box")
	b.release(teamReleaseRequest{Approval: teamTestYes(t, "U07OWNER1", 29), Notes: "Search."})
	// Releases from before v0.55.0, or whose snapshot failed, roll back
	// their code only.
	if _, err := b.release(teamReleaseRequest{Version: 1, Data: true, Approval: teamTestYes(t, "U07OWNER1", 28)}); err == nil || !strings.Contains(err.Error(), "hi team release v1 puts back its code alone") {
		t.Fatal(err)
	}
}
