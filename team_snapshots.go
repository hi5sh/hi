package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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
// the boxes that write, about a second. See
// docs/specs/approved/hi_team_snapshots.md.

const (
	teamSnapshotDaily   = "daily"
	teamSnapshotRelease = "release" // before a release; its label is the version
	teamSnapshotManual  = "manual"
	teamSnapshotRestore = "restore" // before a restore; its label is the snapshot restored

	teamSnapshotTag = "hi-team"
	// teamSnapshotKeepWithin keeps everything this recent, whatever the
	// daily, weekly, and monthly counts say.
	teamSnapshotKeepWithin = "7d"
)

// teamSnapshotKeep is the retention when team.json sets none.
var teamSnapshotKeep = teamBackups{Daily: 7, Weekly: 4, Monthly: 6}

// teamBackups is team.json's "backups": how long snapshots are kept, and
// where they are copied. The bucket's keys are in secrets.json.
type teamBackups struct {
	Offsite string `json:"offsite,omitempty"` // a restic repository, s3:https://<host>/<bucket>[/<path>]
	// Region is the bucket's; restic hangs looking it up from most
	// S3-compatible services, which take us-east-1.
	Region  string `json:"region,omitempty"`
	Daily   int    `json:"keep_daily,omitempty"`
	Weekly  int    `json:"keep_weekly,omitempty"`
	Monthly int    `json:"keep_monthly,omitempty"`
}

func (config teamConfig) backups() teamBackups {
	backups := teamSnapshotKeep
	if config.Backups != nil {
		backups.Offsite, backups.Region = config.Backups.Offsite, config.Backups.Region
		if config.Backups.Daily+config.Backups.Weekly+config.Backups.Monthly > 0 {
			backups.Daily, backups.Weekly, backups.Monthly = config.Backups.Daily, config.Backups.Weekly, config.Backups.Monthly
		}
	}
	return backups
}

// teamSnapshot is one snapshot, as hi shows it.
type teamSnapshot struct {
	ID      string    `json:"id"` // restic's short ID
	Time    time.Time `json:"time"`
	Kind    string    `json:"kind"`
	Label   string    `json:"label,omitempty"`
	Added   int64     `json:"added,omitempty"` // bytes this snapshot added
	Size    int64     `json:"size,omitempty"`  // bytes it holds
	Warning string    `json:"warning,omitempty"`
}

// why says why the snapshot was taken.
func (s teamSnapshot) why() string {
	switch s.Kind {
	case teamSnapshotDaily:
		return "daily"
	case teamSnapshotRelease:
		return "before " + s.Label
	case teamSnapshotRestore:
		return "before restoring " + s.Label
	case teamSnapshotManual:
		return "by hand"
	}
	return s.Kind
}

// teamSnapshotStatus is status.json beside the repository: what hi team
// status shows about snapshots.
type teamSnapshotStatus struct {
	Last     *teamSnapshot `json:"last,omitempty"`
	Failed   string        `json:"failed,omitempty"` // the last attempt's error, until one succeeds
	FailedAt time.Time     `json:"failed_at,omitzero"`
	// Copied is when the last copy offsite finished: every snapshot taken
	// before it is in the bucket.
	Copied        time.Time `json:"copied,omitzero"`
	CopyFailed    string    `json:"copy_failed,omitempty"`
	CopyFailedAt  time.Time `json:"copy_failed_at,omitzero"`
	CopiedOffsite string    `json:"copied_offsite,omitempty"` // the bucket Copied is about
}

var teamSnapshotStatusMu sync.Mutex

// teamSnapshotsDir holds a team's repository, its key, and status.json:
// beside the teams' links, outside the team's folder.
func teamSnapshotsDir(team string) string {
	return filepath.Join(filepath.Dir(teamsDir()), "team-snapshots", team)
}

func teamSnapshotKey(team string) string { return filepath.Join(teamSnapshotsDir(team), "key") }

func loadTeamSnapshotStatus(team string) teamSnapshotStatus {
	var status teamSnapshotStatus
	if data, err := os.ReadFile(filepath.Join(teamSnapshotsDir(team), "status.json")); err == nil {
		json.Unmarshal(data, &status)
	}
	return status
}

func updateTeamSnapshotStatus(team string, change func(*teamSnapshotStatus)) {
	teamSnapshotStatusMu.Lock()
	defer teamSnapshotStatusMu.Unlock()
	status := loadTeamSnapshotStatus(team)
	change(&status)
	data, _ := json.MarshalIndent(status, "", "  ")
	writeFileAtomic(filepath.Join(teamSnapshotsDir(team), "status.json"), append(data, '\n'), 0o600)
}

// ---------------------------------------------------------------------------
// restic

// teamRestic runs restic with env on top of hi's own environment, without
// any RESTIC_ or AWS_ settings of the user's, in dir; it returns stdout.
// Tests replace it.
var teamRestic = func(env []string, dir string, args ...string) (string, error) {
	if _, err := exec.LookPath("restic"); err != nil {
		return "", errors.New("restic isn't installed on this machine; hi install restic adds it")
	}
	// restic keeps retrying a bucket it can't reach; checking for one is
	// given a couple of minutes.
	limit := 12 * time.Hour
	if args[0] == "cat" {
		limit = teamResticCheckLimit
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
			err = teamResticError{code: exit.ExitCode(), text: firstNonEmpty(lastLines(strings.TrimSpace(stderr.String()), 1), err.Error())}
		}
		return string(out), err
	}
	return string(out), nil
}

var teamResticCheckLimit = 2 * time.Minute

type teamResticError struct {
	code int
	text string
}

func (e teamResticError) Error() string { return "restic: " + e.text }

// teamResticMissingRepo is restic's exit code for a repository that isn't
// there.
const teamResticMissingRepo = 10

func teamResticEnv(team string) []string {
	return []string{"RESTIC_REPOSITORY=" + filepath.Join(teamSnapshotsDir(team), "repo"), "RESTIC_PASSWORD_FILE=" + teamSnapshotKey(team)}
}

// teamOffsiteEnv reaches the bucket, with the local repository as the one
// restic copy reads from.
func teamOffsiteEnv(team string, backups teamBackups, secrets teamSecrets) []string {
	return []string{"RESTIC_REPOSITORY=" + backups.Offsite, "RESTIC_PASSWORD_FILE=" + teamSnapshotKey(team),
		"RESTIC_FROM_REPOSITORY=" + filepath.Join(teamSnapshotsDir(team), "repo"), "RESTIC_FROM_PASSWORD_FILE=" + teamSnapshotKey(team),
		"AWS_ACCESS_KEY_ID=" + secrets.BackupKeyID, "AWS_SECRET_ACCESS_KEY=" + secrets.BackupSecret,
		"AWS_DEFAULT_REGION=" + firstNonEmpty(backups.Region, teamDefaultRegion)}
}

const teamDefaultRegion = "us-east-1"

// teamSnapshotRepo makes the team's repository and its key the first time.
func teamSnapshotRepo(team string) error {
	dir := teamSnapshotsDir(team)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	key, repo := teamSnapshotKey(team), filepath.Join(dir, "repo")
	if fileExists(filepath.Join(repo, "config")) {
		if !fileExists(key) {
			return fmt.Errorf("the key to the team's snapshots, %s, is gone; without it they can't be read", key)
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
	if _, err := teamRestic(teamResticEnv(team), dir, "init", "--quiet"); err != nil {
		return fmt.Errorf("making the team's snapshot repository: %w", err)
	}
	return nil
}

// teamSnapshotPaths are what a snapshot holds, relative to the team's
// folder: everything but secrets.json and run/, and the network log.
func teamSnapshotPaths(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, entry := range entries {
		name := entry.Name()
		if name == "secrets.json" || name == "run" || strings.HasPrefix(name, ".restore-") {
			continue
		}
		paths = append(paths, name)
	}
	if fileExists(filepath.Join(dir, "run", "proxy", "network.log")) {
		paths = append(paths, filepath.Join("run", "proxy", "network.log"))
	}
	return paths, nil
}

var teamLabelPattern = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// teamTakeSnapshot backs up the team's folder. pause freezes what writes
// to it, and returns what thaws it and a warning about what didn't pause.
// Callers that need the broker's locks hold them.
func teamTakeSnapshot(team, kind, label string, pause func(team string) (func(), string)) (snapshot teamSnapshot, err error) {
	defer func() {
		if err != nil {
			updateTeamSnapshotStatus(team, func(s *teamSnapshotStatus) { s.Failed, s.FailedAt = err.Error(), time.Now().UTC() })
		}
	}()
	if err := teamSnapshotRepo(team); err != nil {
		return snapshot, err
	}
	dir := teamDir(team)
	paths, err := teamSnapshotPaths(dir)
	if err != nil {
		return snapshot, err
	}
	label = teamLabelPattern.ReplaceAllString(label, "-")
	tags := teamSnapshotTag + ",kind:" + kind
	if label != "" {
		tags += ",label:" + label
	}
	// Sockets and PostgreSQL's pid file mean nothing in a restored copy, and
	// a stale pid file can stop the memory server from starting.
	args := append([]string{"backup", "--json", "--quiet", "--host", teamSnapshotHost(team), "--tag", tags, "--exclude", "*.sock", "--exclude", "postmaster.pid"}, paths...)
	thaw, warning := pause(team)
	out, err := teamRestic(teamResticEnv(team), dir, args...)
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
	snapshot = teamSnapshot{ID: summary.SnapshotID[:min(8, len(summary.SnapshotID))], Time: time.Now().UTC(), Kind: kind, Label: label,
		Added: summary.DataAdded, Size: summary.Total, Warning: warning}
	updateTeamSnapshotStatus(team, func(s *teamSnapshotStatus) { s.Last, s.Failed, s.FailedAt = &snapshot, "", time.Time{} })
	return snapshot, nil
}

// teamAfterSnapshot forgets old snapshots after a daily one, then copies
// what is new offsite. It takes as long as it takes, so it runs without the
// broker's locks.
func teamAfterSnapshot(team, kind string) error {
	if kind == teamSnapshotDaily {
		config, _ := loadTeam(team)
		if _, err := teamRestic(teamResticEnv(team), teamDir(team), teamForgetArgs(config.backups())...); err != nil {
			return fmt.Errorf("forgetting old snapshots: %w", err)
		}
	}
	return teamCopyOffsite(team, kind == teamSnapshotDaily)
}

// teamSnapshotHost is what a team's snapshots are filed under: the name
// it was made with, so a renamed team keeps the ones it took before.
func teamSnapshotHost(team string) string {
	if config, err := loadTeam(team); err == nil {
		team = config.madeAs()
	}
	return "hi-team-" + team
}

// teamForgetArgs keeps the policy's daily, weekly, and monthly snapshots,
// everything from the last week, and every snapshot before a release.
func teamForgetArgs(keep teamBackups) []string {
	return []string{"forget", "--quiet", "--prune", "--group-by", "host",
		"--keep-daily", strconv.Itoa(keep.Daily), "--keep-weekly", strconv.Itoa(keep.Weekly), "--keep-monthly", strconv.Itoa(keep.Monthly),
		"--keep-within", teamSnapshotKeepWithin, "--keep-tag", "kind:" + teamSnapshotRelease}
}

// teamPause freezes the boxes that write to the team's folder while it is
// copied: the lead, its memory server, and production. A frozen copy
// restores like a power cut. Tests replace it.
var teamPause = func(team string) (func(), string) {
	engine, _, err := detectBoxEngine()
	if err != nil {
		return func() {}, ""
	}
	containers := []string{"hi-box-team-" + team, teamMemoryContainer(team)}
	if slot, ok := loadTeamSlot(team, teamSlotProduction); ok && slot.Container != "" {
		containers = append(containers, slot.Container)
	}
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
	}, warning
}

func teamNoPause(string) (func(), string) { return func() {}, "" }

// teamCopyOffsite copies the snapshots not in the bucket yet; after a daily
// snapshot it forgets old ones there too.
var teamCopyMu sync.Mutex

func teamCopyOffsite(team string, forget bool) (err error) {
	config, err := loadTeam(team)
	if err != nil {
		return err
	}
	backups := config.backups()
	if backups.Offsite == "" {
		return nil
	}
	teamCopyMu.Lock()
	defer teamCopyMu.Unlock()
	defer func() {
		now := time.Now().UTC()
		updateTeamSnapshotStatus(team, func(s *teamSnapshotStatus) {
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
	env, dir := teamOffsiteEnv(team, backups, secrets), teamSnapshotsDir(team)
	if _, err := teamRestic(env, dir, "cat", "config"); err != nil {
		var failure teamResticError
		if !errors.As(err, &failure) || failure.code != teamResticMissingRepo {
			return fmt.Errorf("reaching %s: %w", backups.Offsite, err)
		}
		if _, err := teamRestic(env, dir, "init", "--quiet", "--copy-chunker-params"); err != nil {
			return fmt.Errorf("making the repository in %s: %w", backups.Offsite, err)
		}
	}
	if _, err := teamRestic(env, dir, "copy", "--quiet", "--host", teamSnapshotHost(team)); err != nil {
		return fmt.Errorf("copying to %s: %w", backups.Offsite, err)
	}
	if forget {
		if _, err := teamRestic(env, dir, teamForgetArgs(backups)...); err != nil {
			return fmt.Errorf("forgetting old snapshots in %s: %w", backups.Offsite, err)
		}
	}
	return nil
}

// teamListSnapshots lists a team's snapshots, oldest first.
func teamListSnapshots(team string) ([]teamSnapshot, error) {
	if !fileExists(filepath.Join(teamSnapshotsDir(team), "repo", "config")) {
		return nil, nil
	}
	out, err := teamRestic(teamResticEnv(team), teamSnapshotsDir(team), "snapshots", "--json", "--host", teamSnapshotHost(team))
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
	var snapshots []teamSnapshot
	for _, item := range found {
		snapshot := teamSnapshot{ID: item.ShortID, Time: item.Time, Added: item.Summary.DataAdded, Size: item.Summary.Total}
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

// teamFindSnapshot picks one: an ID (or its start), latest, a version (the
// snapshot before it went out), or a day (its last).
func teamFindSnapshot(snapshots []teamSnapshot, which string) (teamSnapshot, error) {
	if len(snapshots) == 0 {
		return teamSnapshot{}, errors.New("the team has no snapshots yet; hi team snapshot takes one")
	}
	var picked []teamSnapshot
	switch {
	case which == "latest":
		picked = snapshots
	case regexp.MustCompile(`^v[0-9]+$`).MatchString(which):
		for _, snapshot := range snapshots {
			if snapshot.Kind == teamSnapshotRelease && snapshot.Label == which {
				picked = append(picked, snapshot)
			}
		}
		if len(picked) == 0 {
			return teamSnapshot{}, fmt.Errorf("no snapshot was taken before %s; hi team snapshots lists them", which)
		}
	default:
		if day, err := time.ParseInLocation("2006-01-02", which, time.Local); err == nil {
			for _, snapshot := range snapshots {
				if !snapshot.Time.Before(day) && snapshot.Time.Before(day.AddDate(0, 0, 1)) {
					picked = append(picked, snapshot)
				}
			}
			if len(picked) == 0 {
				return teamSnapshot{}, fmt.Errorf("no snapshot was taken on %s; hi team snapshots lists them", which)
			}
			break
		}
		for _, snapshot := range snapshots {
			if len(which) >= 4 && strings.HasPrefix(snapshot.ID, which) {
				picked = append(picked, snapshot)
			}
		}
		if len(picked) == 0 {
			return teamSnapshot{}, fmt.Errorf("no snapshot %s; hi team snapshots lists them", which)
		}
		if len(picked) > 1 {
			return teamSnapshot{}, fmt.Errorf("%s starts more than one snapshot's ID; give more of it", which)
		}
	}
	return picked[len(picked)-1], nil
}

// ---------------------------------------------------------------------------
// the broker's side

type teamSnapshotRequest struct {
	Kind string `json:"kind"`
}

// snapshot takes one with both the broker's locks held, so no merge, task
// start, release, or preview is half done, then copies it offsite in the
// background. A failure is posted for the owners.
func (b *teamBroker) snapshot(kind, label string) (teamSnapshot, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.snapshotLocked(kind, label)
}

// snapshotLocked is snapshot for a caller that holds b.mu.
func (b *teamBroker) snapshotLocked(kind, label string) (teamSnapshot, error) {
	b.previewMu.Lock()
	snapshot, err := b.takeSnapshot(kind, label)
	b.previewMu.Unlock()
	config, _ := loadTeam(b.team)
	if err != nil {
		b.announce(config, fmt.Sprintf(":warning: %s hi couldn't take the team's snapshot (%s): %s", teamPeople(config.Owners), teamSnapshot{Kind: kind, Label: label}.why(), err))
		return snapshot, err
	}
	if snapshot.Warning != "" {
		b.announce(config, fmt.Sprintf(":warning: %s The team's snapshot %s (%s) has a problem: %s", teamPeople(config.Owners), snapshot.ID, snapshot.why(), snapshot.Warning))
	}
	go func() {
		if err := teamAfterSnapshot(b.team, kind); err != nil {
			b.announce(config, fmt.Sprintf(":warning: %s The team's snapshot %s is taken, but: %s", teamPeople(config.Owners), snapshot.ID, err))
		}
	}()
	return snapshot, nil
}

// snapshotBefore takes the snapshot a move of production starts with, with
// b.mu held; a failure is a warning, not a reason to stop the release.
func (b *teamBroker) snapshotBefore(version int, result *teamReleaseResult) {
	snapshot, err := b.snapshotLocked(teamSnapshotRelease, fmt.Sprintf("v%d", version))
	if err != nil {
		result.Warning = strings.TrimSpace(result.Warning + "\nNo snapshot was taken first: " + err.Error())
		return
	}
	result.Snapshot = snapshot.ID
}

// putBackData puts production's data (app/) back as it was in a snapshot,
// with production's box stopped; the caller holds b.mu and starts
// production again. Nothing changes if it fails.
func (b *teamBroker) putBackData(id string) error {
	if state, ok := loadTeamSlot(b.team, teamSlotProduction); ok && state.Container != "" {
		b.engineOutput("rm", "-f", state.Container)
	}
	return b.restoreData(id)
}

// teamRestoreData restores app/ from a snapshot beside it, then swaps it
// in; a snapshot without app/ leaves it empty.
func teamRestoreData(team, id string) error {
	dir := teamDir(team)
	restored := filepath.Join(dir, ".restore-"+id)
	os.RemoveAll(restored)
	defer os.RemoveAll(restored)
	if _, err := teamRestic(teamResticEnv(team), dir, "restore", id, "--quiet", "--target", restored, "--include", "/app"); err != nil {
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

func teamHostSocket(team string) string {
	return filepath.Join(teamBrokerDir(team)+"-host", teamSocketName)
}

func (b *teamBroker) hostHandler() http.Handler {
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
		var request teamSnapshotRequest
		if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&request); err != nil {
			reply(w, nil, err)
			return
		}
		if request.Kind != teamSnapshotDaily {
			request.Kind = teamSnapshotManual
		}
		snapshot, err := b.snapshot(request.Kind, "")
		reply(w, snapshot, err)
	})
	mux.HandleFunc("POST /rollback", func(w http.ResponseWriter, r *http.Request) {
		var request teamHostRollback
		if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&request); err != nil {
			reply(w, nil, err)
			return
		}
		result, err := b.release(teamReleaseRequest{Version: request.Version, Data: !request.CodeOnly, host: firstNonEmpty(request.Who, "someone") + " on the machine"})
		reply(w, result, err)
	})
	mux.HandleFunc("POST /say", func(w http.ResponseWriter, r *http.Request) {
		var request teamSayRequest
		if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&request); err != nil {
			reply(w, nil, err)
			return
		}
		said, err := b.say(request)
		reply(w, said, err)
	})
	return mux
}

type teamHostRollback struct {
	Version  int    `json:"version"`
	CodeOnly bool   `json:"code_only,omitempty"`
	Who      string `json:"who,omitempty"`
}

// teamHostCall calls the team's service on the host's socket. ok is false
// when the service isn't there to ask, or runs a hi from before it had one.
func teamHostCall(team, path string, body, out any) (ok bool, err error) {
	socket := teamHostSocket(team)
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

// teamSnapshotCommand is hi team snapshot <name> [--daily].
func teamSnapshotCommand(args []string, stdout io.Writer) error {
	kind := teamSnapshotManual
	var words []string
	for _, arg := range args {
		switch {
		case arg == "--daily":
			kind = teamSnapshotDaily
		case strings.HasPrefix(arg, "-"):
			return usageError{"unknown option " + arg}
		default:
			words = append(words, arg)
		}
	}
	if len(words) != 1 {
		return usageError{"usage: hi team snapshot <name>"}
	}
	team := words[0]
	if _, err := loadTeam(team); err != nil {
		return err
	}
	var snapshot teamSnapshot
	asked, err := teamHostCall(team, "/snapshot", teamSnapshotRequest{Kind: kind}, &snapshot)
	if !asked {
		// The team is down: nothing holds the locks, but production's box
		// may still run.
		if snapshot, err = teamTakeSnapshot(team, kind, "", teamPause); err == nil {
			if afterErr := teamAfterSnapshot(team, kind); afterErr != nil {
				snapshot.Warning = strings.TrimSpace(snapshot.Warning + " " + teamCapitalize(afterErr.Error()) + ".")
			}
		}
	}
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Snapshot %s of team %s: %s, %s new.\n", snapshot.ID, team, qSize(snapshot.Size), qSize(snapshot.Added))
	if snapshot.Warning != "" {
		fmt.Fprintln(stdout, "Note: "+snapshot.Warning)
	}
	if asked {
		if config, err := loadTeam(team); err == nil && config.backups().Offsite != "" {
			fmt.Fprintln(stdout, "The team copies it offsite now; hi team snapshots shows when it's there.")
		}
	}
	return nil
}

// teamSnapshotsCommand is hi team snapshots <name>.
func teamSnapshotsCommand(name string, stdout io.Writer) error {
	config, err := loadTeam(name)
	if err != nil {
		return err
	}
	snapshots, err := teamListSnapshots(name)
	if err != nil {
		return err
	}
	status := loadTeamSnapshotStatus(name)
	backups := config.backups()
	fmt.Fprintf(stdout, "Snapshots of team %s, in %s.\n", name, teamSnapshotsDir(name))
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
		fmt.Fprintf(stdout, "\nNone yet. hi team snapshot %s takes one; the team takes one daily while it is up.\n", name)
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
	fmt.Fprintf(stdout, "\nhi team restore %s <ID> puts the team back as it was then.\n", name)
	return nil
}

// teamSnapshotLine is hi team status's line about snapshots.
func teamSnapshotLine(name string) string {
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
		line += " Not copied offsite (hi team backups)."
	} else if status.CopyFailed != "" {
		line += fmt.Sprintf(" The last copy to %s failed, %s: %s", offsite, status.CopyFailedAt.Local().Format("Jan 2 15:04"), status.CopyFailed)
	} else if status.CopiedOffsite == offsite {
		line += fmt.Sprintf(" Copied offsite %s.", status.Copied.Local().Format("Jan 2 15:04"))
	}
	return line
}

// ---------------------------------------------------------------------------
// restore

// teamRestoreHooks are what a restore does outside the folder; tests
// replace them.
var (
	// teamStopProduction removes production's box, which runs outside the
	// team's service; the team starts it again on its link.
	teamStopProduction = func(team string) {
		state, ok := loadTeamSlot(team, teamSlotProduction)
		if !ok || state.Container == "" {
			return
		}
		if engine, _, err := detectBoxEngine(); err == nil {
			engine.output("rm", "-f", state.Container)
		}
	}
	teamRestorePost = func(config teamConfig, text string) error {
		secrets, err := loadTeamSecrets(config.Name)
		if err != nil {
			return err
		}
		_, err = newLiveTeamChannel(secrets.SlackBotToken).post(config.Channel, text)
		return err
	}
)

// teamRestoreCommand is hi team restore <name> <snapshot> [--code] [--yes].
func teamRestoreCommand(args []string, stdin io.Reader, stdout io.Writer) error {
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
		return usageError{"usage: hi team restore <name> <snapshot ID | latest | v3 | 2026-10-12> [--code] [--yes]"}
	}
	team := words[0]
	config, err := loadTeam(team)
	if err != nil {
		return err
	}
	snapshots, err := teamListSnapshots(team)
	if err != nil {
		return err
	}
	snapshot, err := teamFindSnapshot(snapshots, words[1])
	if err != nil {
		return err
	}
	dir := teamDir(team)
	code = code || !fileExists(filepath.Join(dir, "repo"))
	fmt.Fprintf(stdout, "This puts team %s back as it was %s, in snapshot %s (%s):\n", team, snapshot.Time.Local().Format("Mon Jan 2 at 15:04"), snapshot.ID, snapshot.why())
	fmt.Fprintln(stdout, "  the lead's memory     lead/, memory/")
	fmt.Fprintln(stdout, "  the team's record     versions.json, tasks/, channel/, logs/")
	fmt.Fprintln(stdout, "  the app's data        app/")
	if code {
		fmt.Fprintln(stdout, "  the code              repo/")
	} else {
		fmt.Fprintln(stdout, "The code (repo/) stays as it is; --code puts it back too.")
	}
	fmt.Fprintln(stdout, "The team's settings (team.json, secrets.json) stay. The team stops while it's done, and hi takes a snapshot of how it is now first, so this can be undone.")
	if err := confirm(stdin, stdout, yes, "Restore team "+team+"?"); err != nil {
		return err
	}

	wasUp := teamState(team) == "up"
	if wasUp {
		fmt.Fprintf(stdout, "Stopping team %s…\n", team)
		if out, err := teamSystemctl("stop", teamUnitName(team)); err != nil {
			return fmt.Errorf("systemctl --user stop %s: %s", teamUnitName(team), qFirstLine(out, err.Error()))
		}
	}
	start := func() {
		if wasUp {
			if out, err := teamSystemctl("start", teamUnitName(team)); err != nil {
				fmt.Fprintf(stdout, "The team didn't start again: %s. hi team up %s starts it.\n", qFirstLine(out, err.Error()), team)
			}
		}
	}
	teamStopProduction(team)
	before, err := teamTakeSnapshot(team, teamSnapshotRestore, snapshot.ID, teamNoPause)
	if err != nil {
		start()
		return fmt.Errorf("nothing was restored: the snapshot of how things are now failed: %w", err)
	}
	fmt.Fprintf(stdout, "Snapshot %s has how things were.\n", before.ID)

	restored := filepath.Join(dir, ".restore-"+snapshot.ID)
	os.RemoveAll(restored)
	restoreArgs := []string{"restore", snapshot.ID, "--quiet", "--target", restored, "--exclude", "/run", "--exclude", "/secrets.json", "--exclude", "/team.json"}
	if !code {
		restoreArgs = append(restoreArgs, "--exclude", "/repo")
	}
	if _, err := teamRestic(teamResticEnv(team), dir, restoreArgs...); err != nil {
		os.RemoveAll(restored)
		start()
		return fmt.Errorf("nothing was restored: %w", err)
	}
	if err := teamSwapRestored(dir, restored, code); err != nil {
		start()
		return err
	}
	if wasUp {
		fmt.Fprintf(stdout, "Starting team %s…\n", team)
	}
	start()

	what := "the lead's memory, the tasks, the channel's log, and the app's data"
	if code {
		what += ", and the code"
	}
	text := fmt.Sprintf(":rewind: *The team is back as it was %s* (snapshot %s, %s): %s. What changed since is in snapshot %s; hi team restore %s %s on the machine puts it back.",
		snapshot.Time.Local().Format("Mon Jan 2 at 15:04"), snapshot.ID, snapshot.why(), what, before.ID, team, before.ID)
	if err := teamRestorePost(config, text); err != nil {
		fmt.Fprintf(stdout, "The channel wasn't told: %v\n", err)
	}
	fmt.Fprintf(stdout, "Team %s is back as it was in snapshot %s. Snapshot %s undoes it: hi team restore %s %s\n", team, snapshot.ID, before.ID, team, before.ID)
	return nil
}

// teamSwapRestored moves what a snapshot holds out of the team's folder and
// the restored copy in, then deletes the old. The settings (team.json,
// secrets.json) and run/ stay, and repo/ unless code. On failure it moves
// the old back.
func teamSwapRestored(dir, restored string, code bool) error {
	stays := func(name string) bool {
		return name == "secrets.json" || name == "team.json" || name == "run" || strings.HasPrefix(name, ".restore-") || name == "repo" && !code
	}
	old := restored + "-old"
	os.RemoveAll(old)
	if err := os.Mkdir(old, 0o700); err != nil {
		return err
	}
	current, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	incoming, err := os.ReadDir(restored)
	if err != nil {
		return err
	}
	var movedOut, movedIn []string
	undo := func(cause error) error {
		for _, name := range movedIn {
			os.RemoveAll(filepath.Join(dir, name))
		}
		for _, name := range movedOut {
			os.Rename(filepath.Join(old, name), filepath.Join(dir, name))
		}
		os.RemoveAll(old)
		os.RemoveAll(restored)
		return fmt.Errorf("nothing was restored: %w", cause)
	}
	for _, entry := range current {
		if stays(entry.Name()) {
			continue
		}
		if err := os.Rename(filepath.Join(dir, entry.Name()), filepath.Join(old, entry.Name())); err != nil {
			return undo(err)
		}
		movedOut = append(movedOut, entry.Name())
	}
	for _, entry := range incoming {
		if stays(entry.Name()) {
			continue
		}
		if err := os.Rename(filepath.Join(restored, entry.Name()), filepath.Join(dir, entry.Name())); err != nil {
			return undo(err)
		}
		movedIn = append(movedIn, entry.Name())
	}
	os.RemoveAll(old)
	os.RemoveAll(restored)
	return nil
}

// ---------------------------------------------------------------------------
// backups: where and how long

// teamBackupsCommand is hi team backups <name> [--offsite <url>|off]
// [--keep d,w,m] [--show-key].
func teamBackupsCommand(args []string, stdin io.Reader, stdout io.Writer) error {
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
			return usageError{"usage: hi team backups <name> [--offsite s3:https://<host>/<bucket>[/<path>] [--region <region>] | --offsite off] [--keep <daily>,<weekly>,<monthly>] [--show-key]"}
		case name == "":
			name = arg
		default:
			return usageError{"usage: hi team backups <name> [--offsite ...] [--keep ...] [--show-key]"}
		}
	}
	if name == "" {
		return usageError{"name the team: hi team backups <name>"}
	}
	config, err := loadTeam(name)
	if err != nil {
		return err
	}
	if showKey {
		key, err := os.ReadFile(teamSnapshotKey(name))
		if err != nil {
			return errors.New("the team has no snapshots yet, so no key; hi team snapshot takes the first")
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
		id, secret := os.Getenv("HI_TEAM_S3_ACCESS_KEY_ID"), os.Getenv("HI_TEAM_S3_SECRET_ACCESS_KEY")
		if id == "" {
			if id, err = teamAskSecret(stdin, stdout, "The bucket's access key ID"); err != nil {
				return err
			}
		}
		if secret == "" {
			if secret, err = teamAskSecret(stdin, stdout, "Its secret access key"); err != nil {
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
		if err := teamSnapshotRepo(name); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Copying the team's snapshots to %s…\n", backups.Offsite)
		if err := teamCopyOffsite(name, false); err != nil {
			return fmt.Errorf("the bucket is saved, but the copy failed: %w", err)
		}
	}
	fmt.Fprintf(stdout, "Team %s's snapshots are in %s.\n", name, teamSnapshotsDir(name))
	fmt.Fprintf(stdout, "hi keeps the last %d days, %d weeks, and %d months, everything from the last week, and every snapshot taken before a release.\n", backups.Daily, backups.Weekly, backups.Monthly)
	if backups.Offsite != "" {
		fmt.Fprintf(stdout, "Each one is copied to %s.\n", backups.Offsite)
		fmt.Fprintf(stdout, "They are encrypted with the key in %s; keep a copy somewhere other than this machine (hi team backups %s --show-key), or the bucket can't be read if the machine is lost.\n", teamSnapshotKey(name), name)
	} else {
		fmt.Fprintf(stdout, "They are only on this machine: hi team backups %s --offsite s3:https://<host>/<bucket> copies them to a bucket too.\n", name)
	}
	return nil
}

// ---------------------------------------------------------------------------
// the daily timer

func teamSnapshotUnitName(name string) string { return "hi-team-" + name + "-snapshot" }

func teamSnapshotUnits(config teamConfig, executable string) (service, timer string) {
	env := ""
	if dir := os.Getenv("HI_TEAMS_DIR"); dir != "" {
		env += "Environment=HI_TEAMS_DIR=" + dir + "\n"
	}
	env += "Environment=PATH=" + os.Getenv("PATH") + "\n"
	service = fmt.Sprintf(`[Unit]
Description=hi team %[1]s: the daily snapshot

[Service]
Type=oneshot
%[2]sExecStart=%[3]s team snapshot %[1]s --daily
`, config.Name, env, executable)
	timer = fmt.Sprintf(`[Unit]
Description=hi team %[1]s: a snapshot every day

[Timer]
OnCalendar=*-*-* 03:00:00
RandomizedDelaySec=1h
Persistent=true

[Install]
WantedBy=timers.target
`, config.Name)
	return service, timer
}

// teamInstallSnapshotTimer writes the timer and its service; the caller
// reloads systemd and enables the timer.
func teamInstallSnapshotTimer(config teamConfig, executable string) error {
	service, timer := teamSnapshotUnits(config, executable)
	dir := filepath.Dir(teamUnitPath(config.Name))
	if err := os.WriteFile(filepath.Join(dir, teamSnapshotUnitName(config.Name)+".service"), []byte(service), 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, teamSnapshotUnitName(config.Name)+".timer"), []byte(timer), 0o644)
}

func teamRemoveSnapshotTimer(name string) {
	teamSystemctl("disable", "--now", teamSnapshotUnitName(name)+".timer")
	dir := filepath.Dir(teamUnitPath(name))
	os.Remove(filepath.Join(dir, teamSnapshotUnitName(name)+".service"))
	os.Remove(filepath.Join(dir, teamSnapshotUnitName(name)+".timer"))
}

// teamResticInstalled says whether snapshots can be taken here.
var teamResticInstalled = func() bool {
	_, err := exec.LookPath("restic")
	return err == nil
}

// ---------------------------------------------------------------------------
// rollback, on the host

// teamRollbackCommand is hi team rollback <name> v2 [--code-only] [--yes]:
// production back on an earlier release, with its data as that release
// left it. An owner on the machine needs no yes in the channel; the team's
// service does it, so the team must be up.
func teamRollbackCommand(args []string, stdin io.Reader, stdout io.Writer) error {
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
	if len(words) != 2 || !teamVersionArg.MatchString(words[1]) {
		return usageError{"usage: hi team rollback <name> v2 [--code-only] [--yes]"}
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
		return fmt.Errorf("team %s has no v%d; hi team status %s shows its versions", team, version, team)
	}
	if version == versions.Production {
		return fmt.Errorf("production is on v%d already", version)
	}
	if codeOnly {
		fmt.Fprintf(stdout, "This puts team %s's production back on v%d's code (it is on v%d). Production's data stays as it is.\n", team, version, versions.Production)
	} else {
		left, ok := versions.leftSnapshot(version)
		if !ok {
			return fmt.Errorf("hi has no snapshot of production's data as v%d left it (hi snapshots it when production moves, since v0.55.0); --code-only puts back its code alone", version)
		}
		fmt.Fprintf(stdout, "This puts team %s's production back on v%d (it is on v%d), with production's data as v%d left it, %s (snapshot %s).\n",
			team, version, versions.Production, version, left.At.Local().Format("Mon Jan 2 at 15:04"), left.Snapshot)
		fmt.Fprintf(stdout, "What was entered in production since is set aside in a snapshot hi takes first; hi team rollback %s v%d puts it back.\n", team, versions.Production)
	}
	fmt.Fprintln(stdout, "The lead's memory, the tasks, and the channel's log stay as they are. hi posts it in the channel.")
	if err := confirm(stdin, stdout, yes, fmt.Sprintf("Roll production back to v%d?", version)); err != nil {
		return err
	}
	var result teamReleaseResult
	asked, err := teamHostCall(team, "/rollback", teamHostRollback{Version: version, CodeOnly: codeOnly, Who: currentUserName()}, &result)
	if !asked {
		return fmt.Errorf("team %s isn't up, or runs a hi from before rollback; hi team up %s, then try again", team, team)
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
