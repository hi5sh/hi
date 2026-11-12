package cli

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"text/tabwriter"
	"time"
)

// Snapshots of a team: the lead's memory, the team's record, and the app's
// data, as they were. restic keeps them, encrypted and deduplicated, in a
// repository per team outside the team's folder, so they outlast it, and
// copies them to an S3-compatible bucket when the team has one. The team's
// service takes them without stopping: the broker's locks, then a pause of
// the boxes that write, for a pass that reads only what changed since a
// first pass taken while they ran. See
// docs/specs/approved/hi_app_snapshots.md.

const (
	appSnapshotDaily   = "daily"
	appSnapshotRelease = "release" // before a release; its label is the version
	appSnapshotManual  = "manual"
	appSnapshotRestore = "restore" // before a restore; its label is the snapshot restored
	// appSnapshotWarmup is the pass before the boxes pause, forgotten
	// once the snapshot is taken.
	appSnapshotWarmup = "warmup"

	appSnapshotTag = "hi-team"
	// appSnapshotKeepWithin keeps everything this recent, whatever the
	// daily, weekly, and monthly counts say.
	appSnapshotKeepWithin = "7d"
)

// appSnapshotKeep is the retention when app.json sets none.
var appSnapshotKeep = appBackups{Daily: 7, Weekly: 4, Monthly: 6}

// appBackups is app.json's "backups": how long snapshots are kept, and
// where they are copied. The bucket's keys are in secrets.json.
type appBackups struct {
	Offsite string `json:"offsite,omitempty"` // a restic repository, s3:https://<host>/<bucket>[/<path>]
	// Region is the bucket's; restic hangs looking it up from most
	// S3-compatible services, which take us-east-1.
	Region  string `json:"region,omitempty"`
	Daily   int    `json:"keep_daily,omitempty"`
	Weekly  int    `json:"keep_weekly,omitempty"`
	Monthly int    `json:"keep_monthly,omitempty"`
}

func (config appConfig) backups() appBackups {
	backups := appSnapshotKeep
	if config.Backups != nil {
		backups.Offsite, backups.Region = config.Backups.Offsite, config.Backups.Region
		if config.Backups.Daily+config.Backups.Weekly+config.Backups.Monthly > 0 {
			backups.Daily, backups.Weekly, backups.Monthly = config.Backups.Daily, config.Backups.Weekly, config.Backups.Monthly
		}
	}
	return backups
}

// appSnapshot is one snapshot, as hi shows it.
type appSnapshot struct {
	ID      string    `json:"id"` // restic's short ID
	Time    time.Time `json:"time"`
	Kind    string    `json:"kind"`
	Label   string    `json:"label,omitempty"`
	Added   int64     `json:"added,omitempty"` // bytes this snapshot added
	Size    int64     `json:"size,omitempty"`  // bytes it holds
	Warning string    `json:"warning,omitempty"`
}

// why says why the snapshot was taken.
func (s appSnapshot) why() string {
	switch s.Kind {
	case appSnapshotDaily:
		return "daily"
	case appSnapshotRelease:
		return "before " + s.Label
	case appSnapshotRestore:
		return "before restoring " + s.Label
	case appSnapshotManual:
		return "by hand"
	}
	return s.Kind
}

// appSnapshotStatus is status.json beside the repository: what hi app
// status shows about snapshots.
type appSnapshotStatus struct {
	Last     *appSnapshot `json:"last,omitempty"`
	Failed   string       `json:"failed,omitempty"` // the last attempt's error, until one succeeds
	FailedAt time.Time    `json:"failed_at,omitzero"`
	// Copied is when the last copy offsite finished: every snapshot taken
	// before it is in the bucket.
	Copied        time.Time `json:"copied,omitzero"`
	CopyFailed    string    `json:"copy_failed,omitempty"`
	CopyFailedAt  time.Time `json:"copy_failed_at,omitzero"`
	CopiedOffsite string    `json:"copied_offsite,omitempty"` // the bucket Copied is about
}

var appSnapshotStatusMu sync.Mutex

// appSnapshotsDir holds a team's repository, its key, and status.json:
// beside the teams' links, outside the team's folder.
func appSnapshotsDir(team string) string {
	root := filepath.Join(filepath.Dir(appsDir()), "app-snapshots")
	if old := filepath.Join(filepath.Dir(root), "team-snapshots"); !fileExists(root) && fileExists(old) {
		root = old // until the machine's last app from before v2.6.67 moves (migrateAppsMachine)
	}
	return filepath.Join(root, team)
}

func appSnapshotKey(team string) string { return filepath.Join(appSnapshotsDir(team), "key") }

func loadTeamSnapshotStatus(team string) appSnapshotStatus {
	var status appSnapshotStatus
	if data, err := os.ReadFile(filepath.Join(appSnapshotsDir(team), "status.json")); err == nil {
		json.Unmarshal(data, &status)
	}
	return status
}

func updateTeamSnapshotStatus(team string, change func(*appSnapshotStatus)) {
	appSnapshotStatusMu.Lock()
	defer appSnapshotStatusMu.Unlock()
	status := loadTeamSnapshotStatus(team)
	change(&status)
	data, _ := json.MarshalIndent(status, "", "  ")
	writeFileAtomic(filepath.Join(appSnapshotsDir(team), "status.json"), append(data, '\n'), 0o600)
}

// ---------------------------------------------------------------------------
// restic

// appRestic runs restic with env on top of hi's own environment, without
// any RESTIC_ or AWS_ settings of the user's, in dir; it returns stdout.
// Tests replace it.
var appRestic = func(env []string, dir string, args ...string) (string, error) {
	if _, err := exec.LookPath("restic"); err != nil {
		return "", errors.New("restic isn't installed on this machine; hi install restic adds it")
	}
	// restic keeps retrying a bucket it can't reach; checking for one is
	// given a couple of minutes.
	limit := 12 * time.Hour
	if args[0] == "cat" {
		limit = appResticCheckLimit
	}
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	command := exec.CommandContext(ctx, "restic", args...)
	command.Dir = dir
	for _, value := range os.Environ() {
		if !strings.HasPrefix(value, "RESTIC_") && !strings.HasPrefix(value, "AWS_") {
			command.Env = append(command.Env, value)
		}
	}
	command.Env = append(command.Env, env...)
	var stderr strings.Builder
	command.Stderr = &stderr
	out, err := command.Output()
	if err != nil {
		var exit *exec.ExitError
		if ctx.Err() != nil {
			err = fmt.Errorf("restic didn't finish within %s: %s", limit, firstNonEmpty(lastLines(strings.TrimSpace(stderr.String()), 1), "no answer"))
		} else if errors.As(err, &exit) {
			err = appResticError{code: exit.ExitCode(), text: firstNonEmpty(lastLines(strings.TrimSpace(stderr.String()), 1), err.Error())}
		}
		return string(out), err
	}
	return string(out), nil
}

var appResticCheckLimit = 2 * time.Minute

// appResticExclusive runs forget, which needs the repository to itself.
// restic never clears the lock of a restic that died, as one does when a
// restart cuts a backup or copy short: every forget after it failed with
// "the unlock command can be used to remove stale locks", and old
// snapshots piled up. unlock removes only such locks (a dead process's on
// this machine, or one not refreshed for half an hour), never a running
// one's; wait is how long to wait for a running one to let go.
func appResticExclusive(env []string, dir, wait string, args ...string) (string, error) {
	appRestic(env, dir, "unlock", "--quiet")
	return appRestic(env, dir, slices.Concat(args[:1], []string{"--retry-lock", wait}, args[1:])...)
}

// appResticLockWait is how long a forget after a snapshot waits for a
// listing or copy to finish. The warmup's forget, with the broker's locks
// held, doesn't wait: the daily forget drops what it leaves.
const (
	appResticLockWait   = "30m"
	appResticLockNoWait = "0s"
)

type appResticError struct {
	code int
	text string
}

func (e appResticError) Error() string { return "restic: " + e.text }

// appResticMissingRepo is restic's exit code for a repository that isn't
// there.
const appResticMissingRepo = 10

func appResticEnv(team string) []string {
	return []string{"RESTIC_REPOSITORY=" + filepath.Join(appSnapshotsDir(team), "repo"), "RESTIC_PASSWORD_FILE=" + appSnapshotKey(team)}
}

// appOffsiteEnv reaches the bucket, with the local repository as the one
// restic copy reads from.
func appOffsiteEnv(team string, backups appBackups, secrets appSecrets) []string {
	return []string{"RESTIC_REPOSITORY=" + backups.Offsite, "RESTIC_PASSWORD_FILE=" + appSnapshotKey(team),
		"RESTIC_FROM_REPOSITORY=" + filepath.Join(appSnapshotsDir(team), "repo"), "RESTIC_FROM_PASSWORD_FILE=" + appSnapshotKey(team),
		"AWS_ACCESS_KEY_ID=" + secrets.BackupKeyID, "AWS_SECRET_ACCESS_KEY=" + secrets.BackupSecret,
		"AWS_DEFAULT_REGION=" + firstNonEmpty(backups.Region, appDefaultRegion)}
}

const appDefaultRegion = "us-east-1"

// appSnapshotRepo makes the team's repository and its key the first time.
func appSnapshotRepo(team string) error {
	dir := appSnapshotsDir(team)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	key, repo := appSnapshotKey(team), filepath.Join(dir, "repo")
	if fileExists(filepath.Join(repo, "config")) {
		if !fileExists(key) {
			return fmt.Errorf("the key to the app's snapshots, %s, is gone; without it they can't be read", key)
		}
		return nil
	}
	if !fileExists(key) {
		secret := make([]byte, 32)
		rand.Read(secret)
		if err := os.WriteFile(key, []byte(base64.RawURLEncoding.EncodeToString(secret)+"\n"), 0o600); err != nil {
			return err
		}
	}
	if _, err := appRestic(appResticEnv(team), dir, "init", "--quiet"); err != nil {
		return fmt.Errorf("making the app's snapshot repository: %w", err)
	}
	return nil
}

// appSnapshotPaths are what a snapshot holds, relative to the team's
// folder: everything but secrets.json and run/, and the network log.
func appSnapshotPaths(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, entry := range entries {
		name := entry.Name()
		// net/ holds the peers' NetBird identities, which stay out as the
		// dev box's does.
		if name == "secrets.json" || name == "run" || name == "net" || strings.HasPrefix(name, ".restore-") {
			continue
		}
		if name == "dev" && entry.IsDir() {
			// The dev box's NetBird identity and setup key stay out, as
			// secrets.json does: a restored box joins with a new key.
			inside, err := os.ReadDir(filepath.Join(dir, name))
			if err != nil {
				return nil, err
			}
			for _, sub := range inside {
				if sub.Name() != "netbird" && sub.Name() != appDevSetupKey {
					paths = append(paths, filepath.Join(name, sub.Name()))
				}
			}
			continue
		}
		paths = append(paths, name)
	}
	if fileExists(filepath.Join(dir, "run", "proxy", "network.log")) {
		paths = append(paths, filepath.Join("run", "proxy", "network.log"))
	}
	return paths, nil
}

var appLabelPattern = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// appTakeSnapshot backs up the team's folder. pause freezes what writes
// to it, and returns what thaws it and a warning about what didn't pause.
// Callers that need the broker's locks hold them.
func appTakeSnapshot(team, kind, label string, pause func(team string) (func(), string)) (snapshot appSnapshot, err error) {
	defer func() {
		if err != nil {
			updateTeamSnapshotStatus(team, func(s *appSnapshotStatus) { s.Failed, s.FailedAt = err.Error(), time.Now().UTC() })
		}
	}()
	if err := appSnapshotRepo(team); err != nil {
		return snapshot, err
	}
	dir := appDir(team)
	paths, err := appSnapshotPaths(dir)
	if err != nil {
		return snapshot, err
	}
	label = appLabelPattern.ReplaceAllString(label, "-")
	tags := appSnapshotTag + ",kind:" + kind
	if label != "" {
		tags += ",label:" + label
	}
	// Sockets and PostgreSQL's pid file mean nothing in a restored copy, and
	// a stale pid file can stop the memory server from starting.
	backup := func(tags string) []string {
		return append([]string{"backup", "--json", "--quiet", "--host", appSnapshotHost(team), "--tag", tags, "--exclude", "*.sock", "--exclude", "postmaster.pid"}, paths...)
	}
	// A first pass while everything runs reads what is new since the last
	// snapshot, however much: on orbit, gigabytes a day of downloads. The
	// pass while the boxes are paused then reads only what changed since,
	// in seconds, not the minutes the whole of it took with production
	// frozen. The first pass's snapshot isn't kept.
	if out, err := appRestic(appResticEnv(team), dir, backup(appSnapshotTag+",kind:"+appSnapshotWarmup)...); err == nil {
		if id := appResticSnapshotID(out); id != "" {
			defer appResticExclusive(appResticEnv(team), dir, appResticLockNoWait, "forget", "--quiet", id)
		}
	}
	thaw, warning := pause(team)
	out, err := appRestic(appResticEnv(team), dir, backup(tags)...)
	thaw()
	if err != nil {
		return snapshot, err
	}
	var summary struct {
		MessageType string `json:"message_type"`
		SnapshotID  string `json:"snapshot_id"`
		DataAdded   int64  `json:"data_added"`
		Total       int64  `json:"total_bytes_processed"`
	}
	for _, line := range strings.Split(out, "\n") {
		var message struct {
			MessageType string `json:"message_type"`
		}
		if json.Unmarshal([]byte(line), &message) == nil && message.MessageType == "summary" {
			json.Unmarshal([]byte(line), &summary)
		}
	}
	if summary.SnapshotID == "" {
		return snapshot, errors.New("restic didn't say which snapshot it took")
	}
	snapshot = appSnapshot{ID: summary.SnapshotID[:min(8, len(summary.SnapshotID))], Time: time.Now().UTC(), Kind: kind, Label: label,
		Added: summary.DataAdded, Size: summary.Total, Warning: warning}
	updateTeamSnapshotStatus(team, func(s *appSnapshotStatus) { s.Last, s.Failed, s.FailedAt = &snapshot, "", time.Time{} })
	return snapshot, nil
}

// appResticSnapshotID is the snapshot a backup's JSON output took.
func appResticSnapshotID(out string) string {
	for _, line := range strings.Split(out, "\n") {
		var summary struct {
			MessageType string `json:"message_type"`
			SnapshotID  string `json:"snapshot_id"`
		}
		if json.Unmarshal([]byte(line), &summary) == nil && summary.MessageType == "summary" {
			return summary.SnapshotID
		}
	}
	return ""
}

// appAfterSnapshot forgets old snapshots after a daily one, then copies
// what is new offsite. It takes as long as it takes, so it runs without the
// broker's locks. The forget waits for a copy still running, which reads
// the repository.
func appAfterSnapshot(team, kind string) error {
	if kind == appSnapshotDaily {
		config, _ := loadTeam(team)
		appCopyMu.Lock()
		_, err := appResticExclusive(appResticEnv(team), appDir(team), appResticLockWait, appForgetArgs(config.backups())...)
		appCopyMu.Unlock()
		if err != nil {
			return fmt.Errorf("forgetting old snapshots: %w", err)
		}
	}
	return appCopyOffsite(team, kind == appSnapshotDaily)
}

// appSnapshotHost is what a team's snapshots are filed under: the name
// it was made with, so a renamed team keeps the ones it took before.
func appSnapshotHost(team string) string {
	if config, err := loadTeam(team); err == nil {
		team = config.madeAs()
	}
	return "hi-team-" + team
}

// appForgetArgs keeps the policy's daily, weekly, and monthly snapshots,
// everything from the last week, and every snapshot before a release.
func appForgetArgs(keep appBackups) []string {
	return []string{"forget", "--quiet", "--prune", "--group-by", "host",
		"--keep-daily", strconv.Itoa(keep.Daily), "--keep-weekly", strconv.Itoa(keep.Weekly), "--keep-monthly", strconv.Itoa(keep.Monthly),
		"--keep-within", appSnapshotKeepWithin, "--keep-tag", "kind:" + appSnapshotRelease}
}

// appPause freezes the boxes that write to the team's folder while it is
// copied: the lead, its memory server, the dev box, and production. A frozen copy
// restores like a power cut. Tests replace it.
var appPause = func(team string) (func(), string) {
	engine, _, err := detectBoxEngine()
	if err != nil {
		return func() {}, ""
	}
	containers := []string{"hi-box-app-" + team, appMemoryContainer(team), appDevContainer(team)}
	if slot, ok := loadTeamSlot(team, appSlotProduction); ok && slot.Container != "" {
		containers = append(containers, slot.Container)
	}
	// A restart now would leave them paused: hi update waits for the note
	// to go, and the next start unpauses what it names.
	done := appMarkBusy(team, appBusyPaused, containers...)
	var paused, failed []string
	for _, container := range containers {
		if state, _ := engine.output("container", "inspect", "--format", "{{.State.Status}}", container); strings.TrimSpace(state) != "running" {
			continue
		}
		if _, err := engine.output("pause", container); err != nil {
			failed = append(failed, container)
			continue
		}
		paused = append(paused, container)
	}
	warning := ""
	if len(failed) > 0 {
		warning = "Taken while " + strings.Join(failed, ", ") + " ran: they didn't pause."
	}
	return func() {
		for _, container := range paused {
			engine.output("unpause", container)
		}
		done()
	}, warning
}

func appNoPause(string) (func(), string) { return func() {}, "" }

// appCopyOffsite copies the snapshots not in the bucket yet; after a daily
// snapshot it forgets old ones there too. appCopyMu keeps one copy, or the
// local forget, at a time.
var appCopyMu sync.Mutex

func appCopyOffsite(team string, forget bool) (err error) {
	config, err := loadTeam(team)
	if err != nil {
		return err
	}
	backups := config.backups()
	if backups.Offsite == "" {
		return nil
	}
	appCopyMu.Lock()
	defer appCopyMu.Unlock()
	defer func() {
		now := time.Now().UTC()
		updateTeamSnapshotStatus(team, func(s *appSnapshotStatus) {
			if err != nil {
				s.CopyFailed, s.CopyFailedAt = err.Error(), now
			} else {
				s.Copied, s.CopiedOffsite, s.CopyFailed, s.CopyFailedAt = now, backups.Offsite, "", time.Time{}
			}
		})
	}()
	secrets, err := loadTeamSecrets(team)
	if err != nil {
		return err
	}
	env, dir := appOffsiteEnv(team, backups, secrets), appSnapshotsDir(team)
	if _, err := appRestic(env, dir, "cat", "config"); err != nil {
		var failure appResticError
		if !errors.As(err, &failure) || failure.code != appResticMissingRepo {
			return fmt.Errorf("reaching %s: %w", backups.Offsite, err)
		}
		if _, err := appRestic(env, dir, "init", "--quiet", "--copy-chunker-params"); err != nil {
			return fmt.Errorf("making the repository in %s: %w", backups.Offsite, err)
		}
	}
	if _, err := appRestic(env, dir, "copy", "--quiet", "--host", appSnapshotHost(team)); err != nil {
		return fmt.Errorf("copying to %s: %w", backups.Offsite, err)
	}
	if forget {
		if _, err := appResticExclusive(env, dir, appResticLockWait, appForgetArgs(backups)...); err != nil {
			return fmt.Errorf("forgetting old snapshots in %s: %w", backups.Offsite, err)
		}
	}
	return nil
}

// appListSnapshots lists a team's snapshots, oldest first.
func appListSnapshots(team string) ([]appSnapshot, error) {
	if !fileExists(filepath.Join(appSnapshotsDir(team), "repo", "config")) {
		return nil, nil
	}
	out, err := appRestic(appResticEnv(team), appSnapshotsDir(team), "snapshots", "--json", "--host", appSnapshotHost(team))
	if err != nil {
		return nil, err
	}
	var found []struct {
		ShortID string    `json:"short_id"`
		Time    time.Time `json:"time"`
		Tags    []string  `json:"tags"`
		Summary struct {
			DataAdded int64 `json:"data_added"`
			Total     int64 `json:"total_bytes_processed"`
		} `json:"summary"`
	}
	if err := json.Unmarshal([]byte(out), &found); err != nil {
		return nil, fmt.Errorf("restic snapshots: %w", err)
	}
	var snapshots []appSnapshot
	for _, item := range found {
		snapshot := appSnapshot{ID: item.ShortID, Time: item.Time, Added: item.Summary.DataAdded, Size: item.Summary.Total}
		for _, tag := range item.Tags {
			if value, ok := strings.CutPrefix(tag, "kind:"); ok {
				snapshot.Kind = value
			} else if value, ok := strings.CutPrefix(tag, "label:"); ok {
				snapshot.Label = value
			}
		}
		snapshots = append(snapshots, snapshot)
	}
	sort.SliceStable(snapshots, func(i, j int) bool { return snapshots[i].Time.Before(snapshots[j].Time) })
	return snapshots, nil
}

// appFindSnapshot picks one: an ID (or its start), latest, a version (the
// snapshot before it went out), or a day (its last).
func appFindSnapshot(snapshots []appSnapshot, which string) (appSnapshot, error) {
	if len(snapshots) == 0 {
		return appSnapshot{}, errors.New("the app has no snapshots yet; hi app snapshot takes one")
	}
	var picked []appSnapshot
	switch {
	case which == "latest":
		picked = snapshots
	case regexp.MustCompile(`^v[0-9]+$`).MatchString(which):
		for _, snapshot := range snapshots {
			if snapshot.Kind == appSnapshotRelease && snapshot.Label == which {
				picked = append(picked, snapshot)
			}
		}
		if len(picked) == 0 {
			return appSnapshot{}, fmt.Errorf("no snapshot was taken before %s; hi app snapshots lists them", which)
		}
	default:
		if day, err := time.ParseInLocation("2006-01-02", which, time.Local); err == nil {
			for _, snapshot := range snapshots {
				if !snapshot.Time.Before(day) && snapshot.Time.Before(day.AddDate(0, 0, 1)) {
					picked = append(picked, snapshot)
				}
			}
			if len(picked) == 0 {
				return appSnapshot{}, fmt.Errorf("no snapshot was taken on %s; hi app snapshots lists them", which)
			}
			break
		}
		for _, snapshot := range snapshots {
			if len(which) >= 4 && strings.HasPrefix(snapshot.ID, which) {
				picked = append(picked, snapshot)
			}
		}
		if len(picked) == 0 {
			return appSnapshot{}, fmt.Errorf("no snapshot %s; hi app snapshots lists them", which)
		}
		if len(picked) > 1 {
			return appSnapshot{}, fmt.Errorf("%s starts more than one snapshot's ID; give more of it", which)
		}
	}
	return picked[len(picked)-1], nil
}

// ---------------------------------------------------------------------------
// the broker's side

type appSnapshotRequest struct {
	Kind string `json:"kind"`
}

// snapshot takes one with both the broker's locks held, so no merge, task
// start, release, or preview is half done, then copies it offsite in the
// background. A failure is posted for the owners.
func (b *appBroker) snapshot(kind, label string) (appSnapshot, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.snapshotLocked(kind, label)
}

// snapshotLocked is snapshot for a caller that holds b.mu.
func (b *appBroker) snapshotLocked(kind, label string) (appSnapshot, error) {
	b.previewMu.Lock()
	snapshot, err := b.takeSnapshot(kind, label)
	b.previewMu.Unlock()
	config, _ := loadTeam(b.team)
	if err != nil {
		b.announceOps(config, fmt.Sprintf(":warning: %s hi couldn't take the app's snapshot (%s): %s", appPeople(config.Owners), appSnapshot{Kind: kind, Label: label}.why(), err))
		return snapshot, err
	}
	if snapshot.Warning != "" {
		b.announceOps(config, fmt.Sprintf(":warning: %s The app's snapshot %s (%s) has a problem: %s", appPeople(config.Owners), snapshot.ID, snapshot.why(), snapshot.Warning))
	}
	go func() {
		if err := appAfterSnapshot(b.team, kind); err != nil {
			b.announceOps(config, fmt.Sprintf(":warning: %s The app's snapshot %s is taken, but: %s", appPeople(config.Owners), snapshot.ID, err))
		}
	}()
	return snapshot, nil
}

// snapshotBefore takes the snapshot a move of production starts with, with
// b.mu held; a failure is a warning, not a reason to stop the release.
func (b *appBroker) snapshotBefore(version int, result *appReleaseResult) {
	snapshot, err := b.snapshotLocked(appSnapshotRelease, fmt.Sprintf("v%d", version))
	if err != nil {
		result.Warning = strings.TrimSpace(result.Warning + "\nNo snapshot was taken first: " + err.Error())
		return
	}
	result.Snapshot = snapshot.ID
}

// putBackData puts production's data (app/) back as it was in a snapshot,
// with production's box stopped; the caller holds b.mu and starts
// production again. Nothing changes if it fails.
func (b *appBroker) putBackData(id string) error {
	if state, ok := loadTeamSlot(b.team, appSlotProduction); ok && state.Container != "" {
		b.engineOutput("rm", "-f", state.Container)
	}
	return b.restoreData(id)
}

// appRestoreData restores app/ from a snapshot beside it, then swaps it
// in; a snapshot without app/ leaves it empty.
func appRestoreData(team, id string) error {
	dir := appDir(team)
	restored := filepath.Join(dir, ".restore-"+id)
	os.RemoveAll(restored)
	defer os.RemoveAll(restored)
	if _, err := appRestic(appResticEnv(team), dir, "restore", id, "--quiet", "--target", restored, "--include", "/app"); err != nil {
		return fmt.Errorf("putting back production's data: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(restored, "app"), 0o700); err != nil {
		return err
	}
	app, old := filepath.Join(dir, "app"), restored+"-old"
	os.RemoveAll(old)
	if err := os.Rename(app, old); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Rename(filepath.Join(restored, "app"), app); err != nil {
		os.Rename(old, app)
		return err
	}
	return os.RemoveAll(old)
}

// ---------------------------------------------------------------------------
// the host's socket

// The host's socket is the broker's door for the people on the machine:
// a snapshot, a rollback. It is never mounted in a box, so what comes
// through it needs no yes in the channel.

func appHostSocket(team string) string {
	return filepath.Join(appBrokerDir(team)+"-host", appSocketName)
}

func (b *appBroker) hostHandler() http.Handler {
	mux := http.NewServeMux()
	reply := func(w http.ResponseWriter, value any, err error) {
		w.Header().Set("Content-Type", "application/json")
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		json.NewEncoder(w).Encode(value)
	}
	mux.HandleFunc("POST /snapshot", func(w http.ResponseWriter, r *http.Request) {
		var request appSnapshotRequest
		if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&request); err != nil {
			reply(w, nil, err)
			return
		}
		if request.Kind != appSnapshotDaily {
			request.Kind = appSnapshotManual
		}
		snapshot, err := b.snapshot(request.Kind, "")
		reply(w, snapshot, err)
	})
	mux.HandleFunc("POST /rollback", func(w http.ResponseWriter, r *http.Request) {
		var request appHostRollback
		if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&request); err != nil {
			reply(w, nil, err)
			return
		}
		result, err := b.release(appReleaseRequest{Version: request.Version, Data: !request.CodeOnly, host: firstNonEmpty(request.Who, "someone") + " on the machine"})
		reply(w, result, err)
	})
	mux.HandleFunc("POST /data/copy", func(w http.ResponseWriter, r *http.Request) {
		var request appDataCopyRequest
		if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&request); err != nil {
			reply(w, nil, err)
			return
		}
		request.Who = firstNonEmpty(request.Who, "someone")
		result, err := b.copyData(request)
		reply(w, result, err)
	})
	// The admin page opens the dev page for an admin as they are in the
	// app (hi_admin.md): a link of their own, not sent in a DM.
	mux.HandleFunc("POST /devlink", func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			User string `json:"user"`
			By   string `json:"by"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&request); err != nil {
			reply(w, nil, err)
			return
		}
		link, err := b.devPageLinkFor(request.User)
		reply(w, map[string]string{"link": link}, err)
	})
	// The admin page's invitations (hi_people.md): a sign-in link for
	// someone with a level, which the admin page puts in its email.
	mux.HandleFunc("POST /emailinvite", func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Email string `json:"email"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&request); err != nil {
			reply(w, nil, err)
			return
		}
		email, err := appCheckEmail(request.Email)
		if err != nil {
			reply(w, nil, err)
			return
		}
		invite, err := b.mailInvite(email)
		reply(w, invite, err)
	})
	mux.HandleFunc("POST /say", func(w http.ResponseWriter, r *http.Request) {
		var request appSayRequest
		if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&request); err != nil {
			reply(w, nil, err)
			return
		}
		said, err := b.say(request)
		reply(w, said, err)
	})
	return mux
}

type appHostRollback struct {
	Version  int    `json:"version"`
	CodeOnly bool   `json:"code_only,omitempty"`
	Who      string `json:"who,omitempty"`
}

// appHostCall calls the team's service on the host's socket. ok is false
// when the service isn't there to ask, or runs a hi from before it had one.
func appHostCall(team, path string, body, out any) (ok bool, err error) {
	socket := appHostSocket(team)
	if !fileExists(socket) {
		return false, nil
	}
	client := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}}
	data, _ := json.Marshal(body)
	response, err := client.Post("http://team"+path, "application/json", strings.NewReader(string(data)))
	if err != nil {
		return false, nil // a socket left by a team that stopped
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return false, nil
	}
	answer, _ := io.ReadAll(response.Body)
	if response.StatusCode != http.StatusOK {
		var failure struct{ Error string }
		json.Unmarshal(answer, &failure)
		return true, errors.New(firstNonEmpty(failure.Error, response.Status))
	}
	return true, json.Unmarshal(answer, out)
}

// ---------------------------------------------------------------------------
// the commands, on the host

// appSnapshotCommand is hi app snapshot <name> [--daily].
func appSnapshotCommand(args []string, stdout io.Writer) error {
	kind := appSnapshotManual
	var words []string
	for _, arg := range args {
		switch {
		case arg == "--daily":
			kind = appSnapshotDaily
		case strings.HasPrefix(arg, "-"):
			return usageError{"unknown option " + arg}
		default:
			words = append(words, arg)
		}
	}
	if len(words) != 1 {
		return usageError{"usage: hi app snapshot <name>"}
	}
	team := words[0]
	if _, err := loadTeam(team); err != nil {
		return err
	}
	var snapshot appSnapshot
	asked, err := appHostCall(team, "/snapshot", appSnapshotRequest{Kind: kind}, &snapshot)
	if !asked {
		// The team is down: nothing holds the locks, but production's box
		// may still run.
		if snapshot, err = appTakeSnapshot(team, kind, "", appPause); err == nil {
			if afterErr := appAfterSnapshot(team, kind); afterErr != nil {
				snapshot.Warning = strings.TrimSpace(snapshot.Warning + " " + appCapitalize(afterErr.Error()) + ".")
			}
		}
	}
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Snapshot %s of app %s: %s, %s new.\n", snapshot.ID, team, qSize(snapshot.Size), qSize(snapshot.Added))
	if snapshot.Warning != "" {
		fmt.Fprintln(stdout, "Note: "+snapshot.Warning)
	}
	if asked {
		if config, err := loadTeam(team); err == nil && config.backups().Offsite != "" {
			fmt.Fprintln(stdout, "The app copies it offsite now; hi app snapshots shows when it's there.")
		}
	}
	return nil
}

// appSnapshotsCommand is hi app snapshots <name>.
func appSnapshotsCommand(name string, stdout io.Writer) error {
	config, err := loadTeam(name)
	if err != nil {
		return err
	}
	snapshots, err := appListSnapshots(name)
	if err != nil {
		return err
	}
	status := loadTeamSnapshotStatus(name)
	backups := config.backups()
	fmt.Fprintf(stdout, "Snapshots of app %s, in %s.\n", name, appSnapshotsDir(name))
	if backups.Offsite != "" {
		fmt.Fprintf(stdout, "Copied to %s.\n", backups.Offsite)
	}
	if status.Failed != "" {
		fmt.Fprintf(stdout, "The last one failed, %s: %s\n", status.FailedAt.Local().Format("Jan 2 15:04"), status.Failed)
	}
	if status.CopyFailed != "" {
		fmt.Fprintf(stdout, "The last copy offsite failed, %s: %s\n", status.CopyFailedAt.Local().Format("Jan 2 15:04"), status.CopyFailed)
	}
	if len(snapshots) == 0 {
		fmt.Fprintf(stdout, "\nNone yet. hi app snapshot %s takes one; the app takes one daily while it is up.\n", name)
		return nil
	}
	fmt.Fprintln(stdout)
	table := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	header := "ID\tTAKEN\tWHY\tSIZE\tNEW"
	if backups.Offsite != "" {
		header += "\tOFFSITE"
	}
	fmt.Fprintln(table, header)
	for _, snapshot := range snapshots {
		line := fmt.Sprintf("%s\t%s\t%s\t%s\t%s", snapshot.ID, snapshot.Time.Local().Format("2006-01-02 15:04"), snapshot.why(), qSize(snapshot.Size), qSize(snapshot.Added))
		if backups.Offsite != "" {
			offsite := "not yet"
			if status.CopiedOffsite == backups.Offsite && !snapshot.Time.After(status.Copied) {
				offsite = "yes"
			}
			line += "\t" + offsite
		}
		fmt.Fprintln(table, line)
	}
	table.Flush()
	fmt.Fprintf(stdout, "\nhi app restore %s <ID> puts the app back as it was then.\n", name)
	return nil
}

// appSnapshotLine is hi app status's line about snapshots.
func appSnapshotLine(name string) string {
	config, err := loadTeam(name)
	if err != nil {
		return ""
	}
	status := loadTeamSnapshotStatus(name)
	var line string
	switch {
	case status.Failed != "":
		line = fmt.Sprintf("Snapshots: the last one failed, %s: %s", status.FailedAt.Local().Format("Jan 2 15:04"), status.Failed)
	case status.Last != nil:
		line = fmt.Sprintf("Snapshots: the last was %s, %s (%s).", status.Last.ID, status.Last.Time.Local().Format("Jan 2 15:04"), status.Last.why())
	default:
		line = "Snapshots: none yet."
	}
	if offsite := config.backups().Offsite; offsite == "" {
		line += " Not copied offsite (hi app backups)."
	} else if status.CopyFailed != "" {
		line += fmt.Sprintf(" The last copy to %s failed, %s: %s", offsite, status.CopyFailedAt.Local().Format("Jan 2 15:04"), status.CopyFailed)
	} else if status.CopiedOffsite == offsite {
		line += fmt.Sprintf(" Copied offsite %s.", status.Copied.Local().Format("Jan 2 15:04"))
	}
	return line
}

// ---------------------------------------------------------------------------
// restore

// appRestoreHooks are what a restore does outside the folder; tests
// replace them.
var (
	// appStopProduction removes production's box, which runs outside the
	// team's service; the team starts it again on its link.
	appStopProduction = func(team string) {
		state, ok := loadTeamSlot(team, appSlotProduction)
		if !ok || state.Container == "" {
			return
		}
		if engine, _, err := detectBoxEngine(); err == nil {
			engine.output("rm", "-f", state.Container)
		}
	}
	appRestorePost = func(config appConfig, text string) error {
		secrets, err := loadTeamSecrets(config.Name)
		if err != nil {
			return err
		}
		_, err = newLiveTeamChannel(secrets.SlackBotToken).post(config.opsChannel(), text)
		return err
	}
)

// appRestoreCommand is hi app restore <name> <snapshot> [--code] [--yes].
func appRestoreCommand(args []string, stdin io.Reader, stdout io.Writer) error {
	var code, yes bool
	var words []string
	for _, arg := range args {
		switch {
		case arg == "--code":
			code = true
		case arg == "--yes" || arg == "-y":
			yes = true
		case strings.HasPrefix(arg, "-"):
			return usageError{"unknown option " + arg}
		default:
			words = append(words, arg)
		}
	}
	if len(words) != 2 {
		return usageError{"usage: hi app restore <name> <snapshot ID | latest | v3 | 2026-10-12> [--code] [--yes]"}
	}
	team := words[0]
	config, err := loadTeam(team)
	if err != nil {
		return err
	}
	snapshots, err := appListSnapshots(team)
	if err != nil {
		return err
	}
	snapshot, err := appFindSnapshot(snapshots, words[1])
	if err != nil {
		return err
	}
	dir := appDir(team)
	code = code || !fileExists(filepath.Join(dir, "repo"))
	fmt.Fprintf(stdout, "This puts app %s back as it was %s, in snapshot %s (%s):\n", team, snapshot.Time.Local().Format("Mon Jan 2 at 15:04"), snapshot.ID, snapshot.why())
	fmt.Fprintln(stdout, "  the lead's memory     lead/, memory/")
	fmt.Fprintln(stdout, "  the app's record     versions.json, tasks/, incidents/, channel/, logs/")
	fmt.Fprintln(stdout, "  the app's data        app/")
	if code {
		fmt.Fprintln(stdout, "  the code              repo/")
	} else {
		fmt.Fprintln(stdout, "The code (repo/) stays as it is; --code puts it back too.")
	}
	fmt.Fprintln(stdout, "The app's settings (app.json, secrets.json) and its NetBird identities stay. The app and its dev box stop while it's done, and hi takes a snapshot of how it is now first, so this can be undone.")
	if err := confirm(stdin, stdout, yes, "Restore app "+team+"?"); err != nil {
		return err
	}

	wasUp := appState(team) == "up"
	if wasUp {
		fmt.Fprintf(stdout, "Stopping app %s…\n", team)
		if out, err := appSystemctl("stop", appUnitName(team)); err != nil {
			return fmt.Errorf("systemctl --user stop %s: %s", appUnitName(team), qFirstLine(out, err.Error()))
		}
	}
	start := func() {
		if wasUp {
			if out, err := appSystemctl("start", appUnitName(team)); err != nil {
				fmt.Fprintf(stdout, "The app didn't start again: %s. hi app up %s starts it.\n", qFirstLine(out, err.Error()), team)
			}
		}
	}
	appStopProduction(team)
	// The dev box runs on dev/'s folders, which the restore swaps: it
	// stops, and the app's start brings it back on the restored ones.
	appStopDevBox(team)
	before, err := appTakeSnapshot(team, appSnapshotRestore, snapshot.ID, appNoPause)
	if err != nil {
		start()
		return fmt.Errorf("nothing was restored: the snapshot of how things are now failed: %w", err)
	}
	fmt.Fprintf(stdout, "Snapshot %s has how things were.\n", before.ID)

	restored := filepath.Join(dir, ".restore-"+snapshot.ID)
	os.RemoveAll(restored)
	restoreArgs := []string{"restore", snapshot.ID, "--quiet", "--target", restored, "--exclude", "/run", "--exclude", "/secrets.json", "--exclude", "/" + appConfigFile, "--exclude", "/" + legacyAppConfigFile}
	if !code {
		restoreArgs = append(restoreArgs, "--exclude", "/repo")
	}
	if _, err := appRestic(appResticEnv(team), dir, restoreArgs...); err != nil {
		os.RemoveAll(restored)
		start()
		return fmt.Errorf("nothing was restored: %w", err)
	}
	if err := appSwapRestored(dir, restored, code); err != nil {
		start()
		return err
	}
	if err := appForgetRestoredIncidents(team); err != nil {
		fmt.Fprintf(stdout, "hi: the app's watch on its jobs: %v\n", err)
	}
	if wasUp {
		fmt.Fprintf(stdout, "Starting app %s…\n", team)
	}
	start()

	what := "the lead's memory, the tasks, the channel's log, and the app's data"
	if code {
		what += ", and the code"
	}
	text := fmt.Sprintf(":rewind: *The app is back as it was %s* (snapshot %s, %s): %s. What changed since is in snapshot %s; hi app restore %s %s on the machine puts it back.",
		snapshot.Time.Local().Format("Mon Jan 2 at 15:04"), snapshot.ID, snapshot.why(), what, before.ID, team, before.ID)
	if err := appRestorePost(config, text); err != nil {
		fmt.Fprintf(stdout, "The channel wasn't told: %v\n", err)
	}
	fmt.Fprintf(stdout, "App %s is back as it was in snapshot %s. Snapshot %s undoes it: hi app restore %s %s\n", team, snapshot.ID, before.ID, team, before.ID)
	return nil
}

// appSwapRestored moves what a snapshot holds out of the team's folder and
// the restored copy in, then deletes the old. The settings (app.json,
// secrets.json) and run/ stay, and repo/ unless code. On failure it moves
// the old back.
func appSwapRestored(dir, restored string, code bool) error {
	// What no snapshot holds stays: the settings, run/, the peers'
	// NetBird identities (net/, and the dev box's in dev/), and the dev
	// box's setup key. dev/ is swapped one level down, around those.
	stays := func(rel string) bool {
		switch rel {
		case "secrets.json", appConfigFile, legacyAppConfigFile, "run", "net", "dev",
			filepath.Join("dev", "netbird"), filepath.Join("dev", appDevSetupKey):
			return true
		}
		return strings.HasPrefix(rel, ".restore-") || rel == "repo" && !code
	}
	old := restored + "-old"
	os.RemoveAll(old)
	if err := os.MkdirAll(filepath.Join(old, "dev"), 0o700); err != nil {
		return err
	}
	var movedOut, movedIn []string
	undo := func(cause error) error {
		for _, rel := range movedIn {
			os.RemoveAll(filepath.Join(dir, rel))
		}
		for _, rel := range movedOut {
			os.Rename(filepath.Join(old, rel), filepath.Join(dir, rel))
		}
		os.RemoveAll(old)
		os.RemoveAll(restored)
		return fmt.Errorf("nothing was restored: %w", cause)
	}
	for _, level := range []string{"", "dev"} {
		current, err := os.ReadDir(filepath.Join(dir, level))
		if err != nil && !(level == "dev" && errors.Is(err, fs.ErrNotExist)) {
			return undo(err)
		}
		for _, entry := range current {
			rel := filepath.Join(level, entry.Name())
			if stays(rel) {
				continue
			}
			if err := os.Rename(filepath.Join(dir, rel), filepath.Join(old, rel)); err != nil {
				return undo(err)
			}
			movedOut = append(movedOut, rel)
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, "dev"), 0o700); err != nil {
		return undo(err)
	}
	for _, level := range []string{"", "dev"} {
		incoming, err := os.ReadDir(filepath.Join(restored, level))
		if err != nil && !(level == "dev" && errors.Is(err, fs.ErrNotExist)) {
			return undo(err)
		}
		for _, entry := range incoming {
			rel := filepath.Join(level, entry.Name())
			if stays(rel) {
				continue
			}
			if err := os.Rename(filepath.Join(restored, rel), filepath.Join(dir, rel)); err != nil {
				return undo(err)
			}
			movedIn = append(movedIn, rel)
		}
	}
	os.RemoveAll(old)
	os.RemoveAll(restored)
	return nil
}

// ---------------------------------------------------------------------------
// backups: where and how long

// appBackupsCommand is hi app backups <name> [--offsite <url>|off]
// [--keep d,w,m] [--show-key].
func appBackupsCommand(args []string, stdin io.Reader, stdout io.Writer) error {
	var name, offsite, keep, region string
	var showKey bool
	for i := 0; i < len(args); i++ {
		switch arg := args[i]; {
		case (arg == "--offsite" || arg == "--keep" || arg == "--region") && i+1 < len(args):
			switch arg {
			case "--offsite":
				offsite = args[i+1]
			case "--keep":
				keep = args[i+1]
			default:
				region = args[i+1]
			}
			i++
		case arg == "--show-key":
			showKey = true
		case strings.HasPrefix(arg, "-"):
			return usageError{"usage: hi app backups <name> [--offsite s3:https://<host>/<bucket>[/<path>] [--region <region>] | --offsite off] [--keep <daily>,<weekly>,<monthly>] [--show-key]"}
		case name == "":
			name = arg
		default:
			return usageError{"usage: hi app backups <name> [--offsite ...] [--keep ...] [--show-key]"}
		}
	}
	if name == "" {
		return usageError{"name the app: hi app backups <name>"}
	}
	config, err := loadTeam(name)
	if err != nil {
		return err
	}
	if showKey {
		key, err := os.ReadFile(appSnapshotKey(name))
		if err != nil {
			return errors.New("the app has no snapshots yet, so no key; hi app snapshot takes the first")
		}
		fmt.Fprintln(stdout, strings.TrimSpace(string(key)))
		return nil
	}
	backups := config.backups()
	changed := false
	if keep != "" {
		parts := strings.Split(keep, ",")
		var counts []int
		for _, part := range parts {
			n, err := strconv.Atoi(strings.TrimSpace(part))
			if err != nil || n < 0 || n > 1000 {
				counts = nil
				break
			}
			counts = append(counts, n)
		}
		if len(counts) != 3 || counts[0]+counts[1]+counts[2] == 0 {
			return usageError{"--keep takes three counts, daily, weekly, and monthly: --keep 7,4,6"}
		}
		backups.Daily, backups.Weekly, backups.Monthly = counts[0], counts[1], counts[2]
		changed = true
	}
	if region != "" {
		if offsite == "" && backups.Offsite == "" {
			return usageError{"--region is the bucket's: give it with --offsite"}
		}
		backups.Region, changed = region, true
	}
	if offsite == "off" {
		backups.Offsite, backups.Region, changed = "", "", true
		if secrets, err := loadTeamSecrets(name); err == nil {
			secrets.BackupKeyID, secrets.BackupSecret = "", ""
			if err := saveTeamSecrets(name, secrets); err != nil {
				return err
			}
		}
	} else if offsite != "" {
		if !strings.HasPrefix(offsite, "s3:https://") && !strings.HasPrefix(offsite, "s3:http://") {
			return usageError{"--offsite takes an S3-compatible bucket as restic names it: s3:https://<host>/<bucket>[/<path>]"}
		}
		secrets, err := loadTeamSecrets(name)
		if err != nil {
			return err
		}
		id, secret := os.Getenv("HI_APP_S3_ACCESS_KEY_ID"), os.Getenv("HI_APP_S3_SECRET_ACCESS_KEY")
		if id == "" {
			if id, err = appAskSecret(stdin, stdout, "The bucket's access key ID"); err != nil {
				return err
			}
		}
		if secret == "" {
			if secret, err = appAskSecret(stdin, stdout, "Its secret access key"); err != nil {
				return err
			}
		}
		secrets.BackupKeyID, secrets.BackupSecret = id, secret
		if err := saveTeamSecrets(name, secrets); err != nil {
			return err
		}
		backups.Offsite, changed = offsite, true
	}
	if changed {
		config.Backups = &backups
		if err := saveTeam(config); err != nil {
			return err
		}
	}
	if offsite != "" && offsite != "off" || region != "" && backups.Offsite != "" {
		if err := appSnapshotRepo(name); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Copying the app's snapshots to %s…\n", backups.Offsite)
		if err := appCopyOffsite(name, false); err != nil {
			return fmt.Errorf("the bucket is saved, but the copy failed: %w", err)
		}
	}
	fmt.Fprintf(stdout, "App %s's snapshots are in %s.\n", name, appSnapshotsDir(name))
	fmt.Fprintf(stdout, "hi keeps the last %d days, %d weeks, and %d months, everything from the last week, and every snapshot taken before a release.\n", backups.Daily, backups.Weekly, backups.Monthly)
	if backups.Offsite != "" {
		fmt.Fprintf(stdout, "Each one is copied to %s.\n", backups.Offsite)
		fmt.Fprintf(stdout, "They are encrypted with the key in %s; keep a copy somewhere other than this machine (hi app backups %s --show-key), or the bucket can't be read if the machine is lost.\n", appSnapshotKey(name), name)
	} else {
		fmt.Fprintf(stdout, "They are only on this machine: hi app backups %s --offsite s3:https://<host>/<bucket> copies them to a bucket too.\n", name)
	}
	return nil
}

// ---------------------------------------------------------------------------
// the daily timer

func appSnapshotUnitName(name string) string { return "hi-app-" + name + "-snapshot" }

func appSnapshotUnits(config appConfig, executable string) (service, timer string) {
	env := ""
	if dir := os.Getenv("HI_APPS_DIR"); dir != "" {
		env += "Environment=HI_APPS_DIR=" + dir + "\n"
	}
	env += "Environment=PATH=" + os.Getenv("PATH") + "\n"
	service = fmt.Sprintf(`[Unit]
Description=hi app %[1]s: the daily snapshot

[Service]
Type=oneshot
%[2]sExecStart=%[3]s app snapshot %[1]s --daily
`, config.Name, env, executable)
	timer = fmt.Sprintf(`[Unit]
Description=hi app %[1]s: a snapshot every day

[Timer]
OnCalendar=*-*-* 03:00:00
RandomizedDelaySec=1h
Persistent=true

[Install]
WantedBy=timers.target
`, config.Name)
	return service, timer
}

// appInstallSnapshotTimer writes the timer and its service; the caller
// reloads systemd and enables the timer.
func appInstallSnapshotTimer(config appConfig, executable string) error {
	service, timer := appSnapshotUnits(config, executable)
	dir := filepath.Dir(appUnitPath(config.Name))
	if err := os.WriteFile(filepath.Join(dir, appSnapshotUnitName(config.Name)+".service"), []byte(service), 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, appSnapshotUnitName(config.Name)+".timer"), []byte(timer), 0o644)
}

func appRemoveSnapshotTimer(name string) {
	appSystemctl("disable", "--now", appSnapshotUnitName(name)+".timer")
	dir := filepath.Dir(appUnitPath(name))
	os.Remove(filepath.Join(dir, appSnapshotUnitName(name)+".service"))
	os.Remove(filepath.Join(dir, appSnapshotUnitName(name)+".timer"))
}

// appResticInstalled says whether snapshots can be taken here.
var appResticInstalled = func() bool {
	_, err := exec.LookPath("restic")
	return err == nil
}

// ---------------------------------------------------------------------------
// rollback, on the host

// appRollbackCommand is hi app rollback <name> v2 [--code-only] [--yes]:
// production back on an earlier release, with its data as that release
// left it. An owner on the machine needs no yes in the channel; the team's
// service does it, so the team must be up.
func appRollbackCommand(args []string, stdin io.Reader, stdout io.Writer) error {
	var codeOnly, yes bool
	var words []string
	for _, arg := range args {
		switch {
		case arg == "--code-only":
			codeOnly = true
		case arg == "--yes" || arg == "-y":
			yes = true
		case strings.HasPrefix(arg, "-"):
			return usageError{"unknown option " + arg}
		default:
			words = append(words, arg)
		}
	}
	if len(words) != 2 || !appVersionArg.MatchString(words[1]) {
		return usageError{"usage: hi app rollback <name> v2 [--code-only] [--yes]"}
	}
	team := words[0]
	if _, err := loadTeam(team); err != nil {
		return err
	}
	version, _ := strconv.Atoi(strings.TrimPrefix(words[1], "v"))
	versions, err := loadTeamVersions(team)
	if err != nil {
		return err
	}
	if _, ok := versions.release(version); !ok {
		return fmt.Errorf("app %s has no v%d; hi app status %s shows its versions", team, version, team)
	}
	if version == versions.Production {
		return fmt.Errorf("production is on v%d already", version)
	}
	if codeOnly {
		fmt.Fprintf(stdout, "This puts app %s's production back on v%d's code (it is on v%d). Production's data stays as it is.\n", team, version, versions.Production)
	} else {
		left, ok := versions.leftSnapshot(version)
		if !ok {
			return fmt.Errorf("hi has no snapshot of production's data as v%d left it (hi snapshots it when production moves, since v0.55.0); --code-only puts back its code alone", version)
		}
		fmt.Fprintf(stdout, "This puts app %s's production back on v%d (it is on v%d), with production's data as v%d left it, %s (snapshot %s).\n",
			team, version, versions.Production, version, left.At.Local().Format("Mon Jan 2 at 15:04"), left.Snapshot)
		fmt.Fprintf(stdout, "What was entered in production since is set aside in a snapshot hi takes first; hi app rollback %s v%d puts it back.\n", team, versions.Production)
	}
	fmt.Fprintln(stdout, "The lead's memory, the tasks, and the channel's log stay as they are. hi posts it in the channel.")
	if err := confirm(stdin, stdout, yes, fmt.Sprintf("Roll production back to v%d?", version)); err != nil {
		return err
	}
	var result appReleaseResult
	asked, err := appHostCall(team, "/rollback", appHostRollback{Version: version, CodeOnly: codeOnly, Who: currentUserName()}, &result)
	if !asked {
		return fmt.Errorf("app %s isn't up, or runs a hi from before rollback; hi app up %s, then try again", team, team)
	}
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Production is on v%d now (it was on v%d).", result.Release.Version, result.Previous)
	if result.DataFrom != "" {
		fmt.Fprintf(stdout, " Its data is from snapshot %s; what was entered since is in snapshot %s.", result.DataFrom, result.Snapshot)
	}
	fmt.Fprintln(stdout)
	if result.Warning != "" {
		fmt.Fprintln(stdout, result.Warning)
	}
	return nil
}

// appForgetRestoredIncidents takes out of run/ the incidents a restore
// took away: run/ stays across a restore, and a job, a silent app, or the
// workers whose incident is gone would never have one opened again.
func appForgetRestoredIncidents(team string) error {
	there := func(id string) bool {
		_, err := loadTeamIncident(team, id)
		return err == nil
	}
	state := loadTeamJobsState(team)
	for _, watch := range state.Jobs {
		if watch.Open != "" && !there(watch.Open) {
			watch.Open, watch.Alerted = "", ""
		}
	}
	if state.SilentIncident != "" && !there(state.SilentIncident) {
		state.SilentIncident = ""
	}
	if state.WorkersIncident != "" && !there(state.WorkersIncident) {
		state.WorkersIncident = ""
	}
	return saveTeamJobsState(team, state)
}
