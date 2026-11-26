package cli

// The server's backup queue (hi_server_backups.md, release 2): one timer,
// hi-backups.timer, takes each app's daily snapshot in turn and waits for
// its copy, checks each repository in the bucket once a week, and then
// backs up the server's own state to <bucket>/<name>/server/. Each step is
// written to the run log the page's Recent runs shows.

import (
	"bufio"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	serverBackupsUnit     = "hi-backups"
	serverBackupsRunsFile = "backups-runs.jsonl"
	serverStateStatusFile = "backups-status.json"
	// A repository in the bucket is read through once a week, and a part
	// of its data once a month.
	serverCheckEvery     = 7 * 24 * time.Hour
	serverDataCheckEvery = 30 * 24 * time.Hour
)

// serverBackupsLimitArgs is restic's upload limit, when the plan has one.
func serverBackupsLimitArgs() []string {
	if limit := loadServerBackups().LimitUpload; limit > 0 {
		return []string{"--limit-upload", strconv.Itoa(limit)}
	}
	return nil
}

// ---------------------------------------------------------------------------
// the run log

// serverBackupsRun is one step of a backup: an app's snapshot and copy, a
// check, or the server's state.
type serverBackupsRun struct {
	At    time.Time `json:"at"`
	What  string    `json:"what"`          // daily, now, check, or server
	App   string    `json:"app,omitempty"` // none: the server's state
	Took  float64   `json:"took"`          // seconds
	Size  int64     `json:"size,omitempty"`
	Added int64     `json:"added,omitempty"`
	Note  string    `json:"note,omitempty"`
	Error string    `json:"error,omitempty"`
}

var serverBackupsRunsMu sync.Mutex

func serverBackupsRunsPath() string { return filepath.Join(serverBackupsDir(), serverBackupsRunsFile) }

// serverBackupsRecord adds a step to the log, which keeps its last 200.
func serverBackupsRecord(run serverBackupsRun) {
	serverBackupsRunsMu.Lock()
	defer serverBackupsRunsMu.Unlock()
	line, _ := json.Marshal(run)
	path := serverBackupsRunsPath()
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	file.Write(append(line, '\n'))
	file.Close()
	if info, err := os.Stat(path); err == nil && info.Size() > 256<<10 {
		runs := serverBackupsRuns(200)
		var out []byte
		for i := len(runs) - 1; i >= 0; i-- {
			data, _ := json.Marshal(runs[i])
			out = append(append(out, data...), '\n')
		}
		writeFileAtomic(path, out, 0o600)
	}
}

// serverBackupsRuns are the log's last n steps, newest first.
func serverBackupsRuns(n int) []serverBackupsRun {
	file, err := os.Open(serverBackupsRunsPath())
	if err != nil {
		return nil
	}
	defer file.Close()
	var runs []serverBackupsRun
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64<<10), 1<<20)
	for scanner.Scan() {
		var run serverBackupsRun
		if json.Unmarshal(scanner.Bytes(), &run) == nil {
			runs = append(runs, run)
		}
	}
	slices.Reverse(runs)
	if len(runs) > n {
		runs = runs[:n]
	}
	return runs
}

// ---------------------------------------------------------------------------
// the queue

// serverBackupsLock keeps one backup run at a time: the daily queue and
// a Back up now wait for each other.
func serverBackupsLock(stdout io.Writer) (func(), error) {
	file, err := os.OpenFile(filepath.Join(serverBackupsDir(), "backups.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		fmt.Fprintln(stdout, "Another backup runs; this one waits for it to end.")
		if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
			file.Close()
			return nil, err
		}
	}
	return func() { syscall.Flock(int(file.Fd()), syscall.LOCK_UN); file.Close() }, nil
}

// serverAppUp says an app's service is enabled: one an owner took down
// has no daily snapshot, as with its own timer before. Tests replace it.
var serverAppUp = func(name string) bool {
	out, _ := appSystemctl("is-enabled", appUnitName(name))
	return strings.TrimSpace(out) == "enabled"
}

// serverBackupsRunAll is the queue: the apps one at a time, then the
// server's state. daily takes daily snapshots of every app that is up
// (and checks what is due); otherwise apps names those to copy now, or
// none for every app that is copied offsite, and withApps false backs up
// only the server's state.
func serverBackupsRunAll(daily bool, apps []string, withApps, withServer bool, stdout io.Writer) error {
	unlock, err := serverBackupsLock(stdout)
	if err != nil {
		return err
	}
	defer unlock()
	server := loadServerBackups()
	if withApps && len(apps) == 0 {
		names := machineAppNames()
		sort.Strings(names)
		for _, name := range names {
			config, err := loadTeam(name)
			if err != nil {
				continue
			}
			if daily && serverAppUp(name) || !daily && config.backupsWith(server).Offsite != "" {
				apps = append(apps, name)
			}
		}
		if !daily && len(apps) == 0 && !withServer {
			return errors.New("no app is copied offsite: set the server's bucket (hi server backups --bucket), or turn apps on")
		}
	}
	what := map[bool]string{true: "daily", false: "now"}[daily]
	var failed []string
	for _, name := range apps {
		if _, err := loadTeam(name); err != nil {
			return err
		}
	}
	for _, name := range apps {
		config, _ := loadTeam(name)
		offsite := config.backupsWith(server).Offsite
		if !daily && offsite == "" {
			fmt.Fprintf(stdout, "App %s isn't copied offsite; skipped.\n", name)
			continue
		}
		started := time.Now()
		err := serverBackupsOneApp(name, daily, offsite != "", stdout)
		run := serverBackupsRun{At: started.UTC(), What: what, App: name, Took: time.Since(started).Seconds()}
		if last := loadTeamSnapshotStatus(name).Last; last != nil && !last.Time.Before(started.Add(-time.Second)) {
			run.Size, run.Added = last.Size, last.Added
		}
		if offsite == "" {
			run.Note = "on this machine only"
		}
		if err != nil {
			run.Error = err.Error()
			fmt.Fprintf(stdout, "App %s: %v\n", name, err)
			failed = append(failed, name)
		}
		serverBackupsRecord(run)
		if err == nil && daily && offsite != "" {
			if err := serverBackupsCheckApp(name, false, stdout); err != nil {
				failed = append(failed, name+" (check)")
			}
		}
	}
	if withServer {
		if err := serverBackupsState(daily, stdout); err != nil {
			fmt.Fprintf(stdout, "The server's state: %v\n", err)
			failed = append(failed, "the server's state")
		} else if daily {
			if err := serverBackupsCheckState(stdout); err != nil {
				failed = append(failed, "the server's state (check)")
			}
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("not backed up: %s", strings.Join(failed, ", "))
	}
	return nil
}

// serverBackupsWait is how long the queue waits for an app's service to
// copy its snapshot.
var serverBackupsWait = 3 * time.Hour

// serverBackupsOneApp takes an app's snapshot and, when it is copied
// offsite, waits until the copy is in the bucket.
func serverBackupsOneApp(name string, daily, offsite bool, stdout io.Writer) error {
	args := []string{name}
	if daily {
		args = append(args, "--daily")
	}
	if err := appSnapshotCommand(args, stdout); err != nil {
		return err
	}
	status := loadTeamSnapshotStatus(name)
	if status.Last == nil {
		return errors.New("the snapshot isn't in its status")
	}
	if !offsite {
		return nil
	}
	taken := status.Last.Time
	deadline := time.Now().Add(serverBackupsWait)
	for {
		status = loadTeamSnapshotStatus(name)
		if !status.Copied.Before(taken) {
			fmt.Fprintf(stdout, "App %s is in the bucket, %s.\n", name, status.CopiedOffsite)
			return nil
		}
		if status.CopyFailed != "" && !status.CopyFailedAt.Before(taken) {
			return errors.New(status.CopyFailed)
		}
		if time.Now().After(deadline) {
			return errors.New("the copy hasn't ended yet; hi server backups shows when it does")
		}
		time.Sleep(2 * time.Second)
	}
}

// ---------------------------------------------------------------------------
// checks

// serverCheckArgs is restic check, with a part of the data read when that
// is due.
func serverCheckArgs(dataChecked time.Time) ([]string, bool) {
	if time.Since(dataChecked) >= serverDataCheckEvery {
		return []string{"check", "--read-data-subset=1%"}, true
	}
	return []string{"check"}, false
}

// serverBackupsCheckApp reads the app's repository in the bucket through,
// when a week has passed (or always, with force).
func serverBackupsCheckApp(name string, force bool, stdout io.Writer) error {
	config, err := loadTeam(name)
	if err != nil {
		return err
	}
	backups := config.backups()
	status := loadTeamSnapshotStatus(name)
	if backups.Offsite == "" || !force && time.Since(status.Checked) < serverCheckEvery {
		return nil
	}
	bucket, err := appOffsiteBucket(name, backups)
	if err != nil {
		return err
	}
	args, data := serverCheckArgs(status.DataChecked)
	started := time.Now()
	_, err = appResticExclusive(appOffsiteEnv(name, backups, bucket), appSnapshotsDir(name), appResticLockWait, args...)
	run := serverBackupsRun{At: started.UTC(), What: "check", App: name, Took: time.Since(started).Seconds()}
	if data {
		run.Note = "with 1% of the data read"
	}
	now := time.Now().UTC()
	updateTeamSnapshotStatus(name, func(s *appSnapshotStatus) {
		if err != nil {
			s.CheckFailed, s.CheckFailedAt = err.Error(), now
			return
		}
		s.Checked, s.CheckFailed, s.CheckFailedAt = now, "", time.Time{}
		if data {
			s.DataChecked = now
		}
	})
	if err != nil {
		run.Error = err.Error()
		fmt.Fprintf(stdout, "App %s: checking %s: %v\n", name, backups.Offsite, err)
	} else {
		fmt.Fprintf(stdout, "App %s: the bucket's copy reads back whole.\n", name)
	}
	serverBackupsRecord(run)
	return err
}

// ---------------------------------------------------------------------------
// the server's own state

// serverStateStatus is backups-status.json: the server's state's last
// copy and check.
type serverStateStatus struct {
	Last          time.Time `json:"last,omitzero"`
	Size          int64     `json:"size,omitempty"`
	Added         int64     `json:"added,omitempty"`
	Failed        string    `json:"failed,omitempty"`
	FailedAt      time.Time `json:"failed_at,omitzero"`
	Checked       time.Time `json:"checked,omitzero"`
	DataChecked   time.Time `json:"data_checked,omitzero"`
	CheckFailed   string    `json:"check_failed,omitempty"`
	CheckFailedAt time.Time `json:"check_failed_at,omitzero"`
	KeySealed     string    `json:"key_sealed,omitempty"`
	Repository    string    `json:"repository,omitempty"` // where Last is
}

func loadServerStateStatus() serverStateStatus {
	var status serverStateStatus
	if data, err := os.ReadFile(filepath.Join(serverBackupsDir(), serverStateStatusFile)); err == nil {
		json.Unmarshal(data, &status)
	}
	return status
}

func saveServerStateStatus(status serverStateStatus) {
	data, _ := json.MarshalIndent(status, "", "  ")
	writeFileAtomic(filepath.Join(serverBackupsDir(), serverStateStatusFile), append(data, '\n'), 0o600)
}

// serverStateRepository is where the server's state goes in the bucket.
func (b serverBackups) stateRepository() string { return b.folder() + "/server" }

// serverStateHost is restic's host for the server's snapshots.
func (b serverBackups) stateHost() string { return "hi-server-" + b.Name }

// serverStateKeySecret is the key the server's snapshots are encrypted
// with: in the vault at hi/server/snapshots.
func serverStateKeySecret(dir string) secretFile {
	return secretFile{disk: filepath.Join(dir, "backups-snapshot-key"), path: machineServerPath(dir, "hi/server/snapshots"), kind: secretRaw, what: "the server's snapshot key"}
}

// serverStateKey is that key, made the first time.
func serverStateKey() ([]byte, error) {
	secret := serverStateKeySecret(serverBackupsDir())
	key, err := readSecretFile(secret)
	if err == nil && len(strings.TrimSpace(string(key))) > 0 {
		return []byte(strings.TrimSpace(string(key))), nil
	}
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	random := make([]byte, 32)
	rand.Read(random)
	key = []byte(hex.EncodeToString(random))
	if err := writeSecretFile(secret, append(key, '\n'), 0o600); err != nil {
		return nil, err
	}
	return key, nil
}

// serverStateKeyCommand prints that key, for restic only (hi server
// backups __key).
func serverStateKeyCommand(stdout io.Writer) error {
	key, err := readSecretFile(serverStateKeySecret(serverBackupsDir()))
	if err != nil {
		return err
	}
	fmt.Fprintln(stdout, strings.TrimSpace(string(key)))
	return nil
}

// serverStateEnv reaches the server's repository in the bucket. The key
// comes through a command, so it is in no file, except for a test binary,
// which would run its tests: a file in the staging folder's parent.
func serverStateEnv(b serverBackups, bucket appBucket, key []byte) ([]string, func(), error) {
	env := []string{"RESTIC_REPOSITORY=" + b.stateRepository(), "AWS_ACCESS_KEY_ID=" + bucket.KeyID,
		"AWS_SECRET_ACCESS_KEY=" + bucket.Secret, "AWS_DEFAULT_REGION=" + firstNonEmpty(bucket.Region, appDefaultRegion)}
	exe, err := os.Executable()
	if err == nil && !strings.HasSuffix(exe, ".test") && !strings.ContainsAny(exe, " '\"\\") {
		return append(env, "RESTIC_PASSWORD_COMMAND="+exe+" server backups __key"), func() {}, nil
	}
	file, err := os.CreateTemp(serverBackupsDir(), ".restic-key-*")
	if err != nil {
		return nil, nil, err
	}
	file.Write(key)
	file.Close()
	return append(env, "RESTIC_PASSWORD_FILE="+file.Name()), func() { os.Remove(file.Name()) }, nil
}

// serverStateSources are what the server's state is: its folder, the
// vault (encrypted as it is), and the admin page's folder, each with what
// isn't copied. The vault's key file, when it has one, is in a folder of
// its own (~/.config/hi/vault-key) and in none of these.
func serverStateSources() []struct {
	name, dir string
	skip      func(rel string) bool
} {
	return []struct {
		name, dir string
		skip      func(rel string) bool
	}{
		{"org", serverBackupsDir(), func(rel string) bool {
			// The bucket's keys without a vault, a test's key file, and the lock.
			return rel == "backups-keys.json" || rel == "backups.lock" || strings.HasPrefix(rel, ".restic-key-")
		}},
		{"vault", vaultDir(), func(rel string) bool { return rel == ".lock" }},
		{"admin", adminDir(), func(rel string) bool {
			first, _, _ := strings.Cut(rel, string(filepath.Separator))
			return first == "sockets" || first == "jobs" || first == "drafts"
		}},
	}
}

// serverStateStaging is where the state is copied before it goes: a fixed
// folder, so restic finds the last snapshot to compare with.
func serverStateStaging() (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(cache, "hi", "server-backup"), nil
}

// copyStateTree copies a folder's files, folders, and links; sockets and
// pipes are left out, and so is what skip names (by the path inside).
func copyStateTree(src, dst string, skip func(rel string) bool) error {
	if resolved, err := filepath.EvalSymlinks(src); err == nil {
		src = resolved
	}
	return filepath.WalkDir(src, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		rel, _ := filepath.Rel(src, path)
		if rel != "." && skip(rel) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		target := filepath.Join(dst, rel)
		info, err := entry.Info()
		if err != nil {
			return nil // gone meanwhile
		}
		switch mode := info.Mode(); {
		case mode.IsDir():
			return os.MkdirAll(target, 0o700)
		case mode&fs.ModeSymlink != 0:
			link, err := os.Readlink(path)
			if err != nil {
				return nil
			}
			return os.Symlink(link, target)
		case mode.IsRegular():
			data, err := os.ReadFile(path)
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					return nil
				}
				return err
			}
			return os.WriteFile(target, data, 0o600)
		}
		return nil // sockets, pipes, devices
	})
}

// serverStageState copies the server's state into the staging folder;
// the vault's with its lock held, so no write is half in it.
func serverStageState(staging string) error {
	if err := os.RemoveAll(staging); err != nil {
		return err
	}
	if err := os.MkdirAll(staging, 0o700); err != nil {
		return err
	}
	for _, source := range serverStateSources() {
		if source.dir == "" || !fileExists(source.dir) {
			continue
		}
		copyOne := func() error { return copyStateTree(source.dir, filepath.Join(staging, source.name), source.skip) }
		if source.name == "vault" {
			unlock, err := vaultLock()
			if err != nil {
				return err
			}
			err = copyOne()
			unlock()
			if err != nil {
				return fmt.Errorf("copying the vault: %w", err)
			}
			continue
		}
		if err := copyOne(); err != nil {
			return fmt.Errorf("copying %s: %w", source.dir, err)
		}
	}
	return nil
}

// serverBackupsState backs the server's state up to the bucket, forgets
// old snapshots of it by the plan, checks it weekly, and keeps its key
// beside it sealed to the recovery key.
func serverBackupsState(daily bool, stdout io.Writer) (err error) {
	b := loadServerBackups()
	if !b.on() {
		return errors.New("the server has no bucket")
	}
	if !vaultExists() {
		return errors.New("the server has no vault: its state holds secrets, and only goes encrypted, with a key the vault keeps")
	}
	started := time.Now()
	run := serverBackupsRun{At: started.UTC(), What: map[bool]string{true: "daily", false: "now"}[daily]}
	status := loadServerStateStatus()
	defer func() {
		run.Took = time.Since(started).Seconds()
		if err != nil {
			run.Error = err.Error()
			status.Failed, status.FailedAt = err.Error(), time.Now().UTC()
		}
		saveServerStateStatus(status)
		serverBackupsRecord(run)
	}()
	bucket, err := loadServerBucket(b)
	if err != nil {
		return err
	}
	key, err := serverStateKey()
	if err != nil {
		return err
	}
	env, cleanup, err := serverStateEnv(b, bucket, key)
	if err != nil {
		return err
	}
	defer cleanup()
	staging, err := serverStateStaging()
	if err != nil {
		return err
	}
	if err := serverStageState(staging); err != nil {
		return err
	}
	defer os.RemoveAll(staging)
	if _, err := appRestic(env, staging, "cat", "config"); err != nil {
		var failure appResticError
		if !errors.As(err, &failure) || failure.code != appResticMissingRepo {
			return fmt.Errorf("reaching %s: %w", b.stateRepository(), err)
		}
		if _, err := appRestic(env, staging, "init", "--quiet"); err != nil {
			return fmt.Errorf("making the repository in %s: %w", b.stateRepository(), err)
		}
	}
	kind := map[bool]string{true: appSnapshotDaily, false: appSnapshotManual}[daily]
	out, err := appRestic(env, staging, slices.Concat([]string{"backup", "--json", "--host", b.stateHost(), "--tag", "hi-server", "--tag", "kind:" + kind}, serverBackupsLimitArgs(), []string{"."})...)
	if err != nil {
		return fmt.Errorf("backing up to %s: %w", b.stateRepository(), err)
	}
	for _, line := range strings.Split(out, "\n") {
		var summary struct {
			MessageType string `json:"message_type"`
			DataAdded   int64  `json:"data_added"`
			Total       int64  `json:"total_bytes_processed"`
		}
		if json.Unmarshal([]byte(line), &summary) == nil && summary.MessageType == "summary" {
			run.Size, run.Added = summary.Total, summary.DataAdded
		}
	}
	status.Last, status.Size, status.Added, status.Failed, status.FailedAt, status.Repository = time.Now().UTC(), run.Size, run.Added, "", time.Time{}, b.stateRepository()
	fmt.Fprintf(stdout, "The server's state is in the bucket, %s: %s, %s new.\n", b.stateRepository(), qSize(run.Size), qSize(run.Added))
	if daily {
		keep := b.keep()
		if _, err := appResticExclusive(env, staging, appResticLockWait, "forget", "--quiet", "--prune", "--host", b.stateHost(),
			"--keep-daily", strconv.Itoa(keep.Daily), "--keep-weekly", strconv.Itoa(keep.Weekly), "--keep-monthly", strconv.Itoa(keep.Monthly),
			"--keep-within", appSnapshotKeepWithin); err != nil {
			return fmt.Errorf("forgetting old snapshots of the server's state: %w", err)
		}
	}
	// Its key beside it, sealed to the recovery key, as each app's is.
	if keys, err := loadVaultKeys(); err == nil && len(keys.RecoveryPublic) == 32 {
		mark := sha256.Sum256(slicesConcat(keys.RecoveryPublic, []byte(b.stateRepository()), key))
		if marker := hex.EncodeToString(mark[:]); status.KeySealed != marker {
			sealed, err := appSealKey("hi-server:"+b.Name, key, keys)
			if err == nil {
				err = appBucketPutObject(b.stateRepository(), appSealedKeyName, bucket, sealed)
			}
			if err != nil {
				return fmt.Errorf("%s beside the server's state: %w", appSealedKeyName, err)
			}
			status.KeySealed = marker
		}
	}
	return nil
}

// serverBackupsCheckState reads the server's repository in the bucket
// through, once a week.
func serverBackupsCheckState(stdout io.Writer) error {
	b := loadServerBackups()
	status := loadServerStateStatus()
	if !b.on() || time.Since(status.Checked) < serverCheckEvery {
		return nil
	}
	bucket, err := loadServerBucket(b)
	if err != nil {
		return err
	}
	key, err := serverStateKey()
	if err != nil {
		return err
	}
	env, cleanup, err := serverStateEnv(b, bucket, key)
	if err != nil {
		return err
	}
	defer cleanup()
	args, data := serverCheckArgs(status.DataChecked)
	started := time.Now()
	_, err = appResticExclusive(env, serverBackupsDir(), appResticLockWait, args...)
	run := serverBackupsRun{At: started.UTC(), What: "check", Took: time.Since(started).Seconds()}
	if data {
		run.Note = "with 1% of the data read"
	}
	now := time.Now().UTC()
	if err != nil {
		run.Error = err.Error()
		status.CheckFailed, status.CheckFailedAt = err.Error(), now
		fmt.Fprintf(stdout, "The server's state: checking %s: %v\n", b.stateRepository(), err)
	} else {
		status.Checked, status.CheckFailed, status.CheckFailedAt = now, "", time.Time{}
		if data {
			status.DataChecked = now
		}
		fmt.Fprintln(stdout, "The server's state: the bucket's copy reads back whole.")
	}
	saveServerStateStatus(status)
	serverBackupsRecord(run)
	return err
}

// ---------------------------------------------------------------------------
// the timer

func serverBackupsTimerPath() string   { return userUnitPath(serverBackupsUnit + ".timer") }
func serverBackupsServicePath() string { return userUnitPath(serverBackupsUnit + ".service") }

// serverBackupsQueued says the server's queue takes the apps' daily
// snapshots, so an app has no timer of its own.
func serverBackupsQueued() bool { return fileExists(serverBackupsTimerPath()) }

func serverBackupsUnits(executable string, b serverBackups) (service, timer string) {
	env := ""
	if dir := os.Getenv("HI_APPS_DIR"); dir != "" {
		env += "Environment=HI_APPS_DIR=" + dir + "\n"
	}
	path := os.Getenv("PATH")
	if path == "" {
		path = "/usr/local/bin:/usr/bin:/bin"
	}
	env += "Environment=PATH=" + path + "\n"
	service = fmt.Sprintf(`[Unit]
Description=hi server: back up each app in turn, then the server

[Service]
Type=oneshot
%sExecStart=%s server backups run --daily
TimeoutStartSec=infinity
Nice=10
IOSchedulingClass=idle
`, env, executable)
	timer = fmt.Sprintf(`[Unit]
Description=hi server: back up every day

[Timer]
OnCalendar=*-*-* %s:00
RandomizedDelaySec=10m
Persistent=true

[Install]
WantedBy=timers.target
`, b.at())
	return service, timer
}

// serverBackupsEnsureTimer installs the queue's timer, as the server
// starts, and then takes the apps' own timers away: an app is never left
// without its daily snapshot.
func serverBackupsEnsureTimer(stdout io.Writer) error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	if resolved, err := filepath.EvalSymlinks(executable); err == nil {
		executable = resolved
	}
	service, timer := serverBackupsUnits(executable, loadServerBackups())
	changed := false
	for path, text := range map[string]string{serverBackupsServicePath(): service, serverBackupsTimerPath(): timer} {
		if old, err := os.ReadFile(path); err == nil && string(old) == text {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			return err
		}
		changed = true
	}
	if changed {
		appSystemctl("daemon-reload")
	}
	unit := serverBackupsUnit + ".timer"
	if out, _ := appSystemctl("is-enabled", unit); strings.TrimSpace(out) != "enabled" {
		if out, err := appSystemctl("enable", "--now", unit); err != nil {
			return fmt.Errorf("systemctl --user enable --now %s: %s", unit, qFirstLine(out, err.Error()))
		}
		fmt.Fprintf(stdout, "hi: %s takes each app's daily snapshot now, one after another, and backs up the server.\n", unit)
	} else if changed {
		appSystemctl("restart", unit)
	}
	for _, name := range machineAppNames() {
		if fileExists(appSnapshotTimerPath(name)) {
			appRemoveSnapshotTimer(name)
			fmt.Fprintf(stdout, "hi: app %s's own daily timer is removed; %s takes its snapshot.\n", name, unit)
		}
	}
	return nil
}
