package cli

// One bucket for every app on the machine, set on the server
// (docs/specs/approved/hi_server_backups.md, release 1). The server keeps the
// bucket's address, its name in the bucket, and the plan in backups.json
// beside its config, and the bucket's keys in the vault at
// hi/server/backups. An app that follows the server copies its snapshots
// to <bucket>/<name>/apps/<app>, a restic repository of its own as before.

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
)

const (
	serverBackupsFile = "backups.json"
	serverClaimName   = "hi-server.json"
)

// serverBackups is the server's backups.json.
type serverBackups struct {
	Bucket string `json:"bucket,omitempty"` // s3:https://<host>/<bucket>[/<path>]
	Region string `json:"region,omitempty"` // the bucket's; from its host when unset
	Name   string `json:"name,omitempty"`   // this server's folder in the bucket
	Claim  string `json:"claim,omitempty"`  // the ID in <name>/hi-server.json
	// At is the daily snapshot's time, HH:MM (03:00), with up to an hour
	// at random after it.
	At      string `json:"at,omitempty"`
	Daily   int    `json:"keep_daily,omitempty"`
	Weekly  int    `json:"keep_weekly,omitempty"`
	Monthly int    `json:"keep_monthly,omitempty"`
	// AppsOff: apps that haven't chosen aren't copied (on by default).
	AppsOff bool `json:"apps_off,omitempty"`
	// LimitUpload is restic's upload limit, KiB/s; 0 is none.
	LimitUpload int `json:"limit_upload,omitempty"`
}

func (b serverBackups) on() bool       { return b.Bucket != "" && b.Name != "" }
func (b serverBackups) region() string { return firstNonEmpty(b.Region, appBucketRegion(b.Bucket)) }
func (b serverBackups) at() string     { return firstNonEmpty(b.At, "03:00") }

// folder is this server's folder in the bucket.
func (b serverBackups) folder() string { return strings.TrimRight(b.Bucket, "/") + "/" + b.Name }

// appAddress is an app's restic repository in the bucket.
func (b serverBackups) appAddress(app string) string { return b.folder() + "/apps/" + app }

func (b serverBackups) keep() appBackups {
	keep := appSnapshotKeep
	if b.Daily+b.Weekly+b.Monthly > 0 {
		keep.Daily, keep.Weekly, keep.Monthly = b.Daily, b.Weekly, b.Monthly
	}
	return keep
}

// serverBackupsDir is the machine's server folder, where backups.json is.
func serverBackupsDir() string {
	dir, err := orgDirectory("")
	if err != nil {
		return ""
	}
	return dir
}

func loadServerBackups() serverBackups {
	var b serverBackups
	if dir := serverBackupsDir(); dir != "" {
		if data, err := os.ReadFile(filepath.Join(dir, serverBackupsFile)); err == nil {
			json.Unmarshal(data, &b)
		}
	}
	return b
}

func saveServerBackups(b serverBackups) error {
	dir := serverBackupsDir()
	if _, err := loadServerConfig(dir); err != nil {
		return err
	}
	data, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(dir, serverBackupsFile), append(data, '\n'), 0o600)
}

// serverBucketSecret is the bucket's keys: in the vault at
// hi/server/backups, or backups-keys.json without one.
func serverBucketSecret(dir string) secretFile {
	return secretFile{disk: filepath.Join(dir, "backups-keys.json"), path: machineServerPath(dir, "hi/server/backups"), kind: secretJSON, what: "the server's bucket keys"}
}

type serverBucketKeys struct {
	KeyID  string `json:"key_id"`
	Secret string `json:"secret"`
}

// loadServerBucket is the server's bucket keys, with its region.
func loadServerBucket(b serverBackups) (appBucket, error) {
	data, err := readSecretFile(serverBucketSecret(serverBackupsDir()))
	if err != nil {
		if os.IsNotExist(err) {
			return appBucket{}, errors.New("the server's bucket has no keys: hi server backups --bucket sets them")
		}
		return appBucket{}, err
	}
	var keys serverBucketKeys
	if err := json.Unmarshal(data, &keys); err != nil {
		return appBucket{}, err
	}
	return appBucket{KeyID: keys.KeyID, Secret: keys.Secret, Region: b.region()}, nil
}

// ---------------------------------------------------------------------------
// the folder's claim

// serverClaim is <name>/hi-server.json: which server writes there, so two
// machines with one name don't write over each other.
type serverClaim struct {
	Kind     string    `json:"kind"` // "hi server backups"
	Claim    string    `json:"claim"`
	Hostname string    `json:"hostname"`
	Since    time.Time `json:"since"`
}

// serverClaimFolder makes sure the folder is this server's, and says the
// claim's ID. Reading the claim also checks the bucket and its keys.
func serverClaimFolder(b serverBackups, bucket appBucket, takeOver bool) (string, error) {
	data, found, err := appBucketGetObject(b.folder(), serverClaimName, bucket)
	if err != nil {
		return "", fmt.Errorf("reaching %s: %w", b.folder(), err)
	}
	if found {
		var there serverClaim
		json.Unmarshal(data, &there)
		if b.Claim != "" && there.Claim == b.Claim {
			return b.Claim, nil
		}
		if !takeOver {
			return "", fmt.Errorf("%s/ in this bucket belongs to another server (%s, since %s): give this one another name with --name, or --take-over if this machine takes that one's place",
				b.Name, firstNonEmpty(there.Hostname, "unknown"), there.Since.Local().Format("Jan 2, 2006"))
		}
	}
	id := make([]byte, 8)
	rand.Read(id)
	host, _ := os.Hostname()
	claim := serverClaim{Kind: "hi server backups", Claim: hex.EncodeToString(id), Hostname: host, Since: time.Now().UTC()}
	encoded, _ := json.MarshalIndent(claim, "", "  ")
	if err := appBucketPutObject(b.folder(), serverClaimName, bucket, append(encoded, '\n')); err != nil {
		return "", fmt.Errorf("writing %s/%s: %w", b.folder(), serverClaimName, err)
	}
	return claim.Claim, nil
}

var serverBackupsNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// serverDefaultName is the machine's short hostname, as a folder name.
func serverDefaultName() string {
	host, _ := os.Hostname()
	host, _, _ = strings.Cut(strings.ToLower(host), ".")
	name := strings.Trim(regexp.MustCompile(`[^a-z0-9-]+`).ReplaceAllString(host, "-"), "-")
	if !serverBackupsNamePattern.MatchString(name) {
		return "server"
	}
	return name
}

// ---------------------------------------------------------------------------
// each app's state

// serverBackupsApp is one app's line: where it copies, and how it went.
type serverBackupsApp struct {
	Name         string    `json:"name"`
	Use          string    `json:"use"` // server, own, off, or none
	Offsite      string    `json:"offsite,omitempty"`
	Keep         string    `json:"keep"` // daily,weekly,monthly
	OwnKeep      bool      `json:"own_keep,omitempty"`
	Last         time.Time `json:"last,omitzero"`
	Failed       string    `json:"failed,omitempty"`
	FailedAt     time.Time `json:"failed_at,omitzero"`
	Copied       time.Time `json:"copied,omitzero"` // the last copy to Offsite
	CopyFailed   string    `json:"copy_failed,omitempty"`
	CopyFailedAt time.Time `json:"copy_failed_at,omitzero"`
	Checked      time.Time `json:"checked,omitzero"`
	CheckFailed  string    `json:"check_failed,omitempty"`
	// RestoreTested: a restore test brought its newest copy back whole.
	RestoreTested     time.Time `json:"restore_tested,omitzero"`
	RestoreTestFailed string    `json:"restore_test_failed,omitempty"`
}

func serverBackupsApps(server serverBackups) []serverBackupsApp {
	names := machineAppNames()
	sort.Strings(names)
	apps := []serverBackupsApp{}
	for _, name := range names {
		config, err := loadTeam(name)
		if err != nil {
			continue
		}
		backups := config.backupsWith(server)
		status := loadTeamSnapshotStatus(name)
		line := serverBackupsApp{Name: name, Use: config.offsiteUse(server), Offsite: backups.Offsite,
			Keep:    fmt.Sprintf("%d,%d,%d", backups.Daily, backups.Weekly, backups.Monthly),
			OwnKeep: config.Backups != nil && config.Backups.Daily+config.Backups.Weekly+config.Backups.Monthly > 0,
			Failed:  status.Failed, FailedAt: status.FailedAt}
		if status.Last != nil {
			line.Last = status.Last.Time
		}
		if backups.Offsite != "" {
			if status.CopiedOffsite == backups.Offsite {
				line.Copied = status.Copied
			}
			if status.CopyFailed != "" && status.CopyFailedAt.After(line.Copied) {
				line.CopyFailed, line.CopyFailedAt = status.CopyFailed, status.CopyFailedAt
			}
			line.Checked, line.CheckFailed = status.Checked, status.CheckFailed
			line.RestoreTested, line.RestoreTestFailed = status.RestoreTested, status.RestoreTestFailed
		}
		apps = append(apps, line)
	}
	return apps
}

// ---------------------------------------------------------------------------
// hi server backups

const serverBackupsUsage = `usage: hi server backups                       the bucket, the plan, and each app
       hi server backups --bucket s3:https://<host>/<bucket> [--region <r>] [--name <server>] [--take-over]
       hi server backups --bucket off
       hi server backups [--at HH:MM] [--keep <daily>,<weekly>,<monthly>] [--apps-default on|off] [--limit-upload <KiB/s>|off]
       hi server backups on|off <app>... | --all
       hi server backups now [<app>... | --server]
       hi server backups test-restore [<app>... | --server]   restore the newest copies to a folder of their own, check them, remove them
       hi server backups run --daily                 the daily queue (hi-backups.timer runs it)`

func serverBackupsCommand(args []string, stdin io.Reader, stdout io.Writer) error {
	var bucket, region, name, at, keep, appsDefault, limit, verb string
	var takeOver, all, daily, stateOnly bool
	var apps []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		value := func() (string, error) {
			if i+1 >= len(args) {
				return "", usageError{arg + " takes a value\n" + serverBackupsUsage}
			}
			i++
			return args[i], nil
		}
		var err error
		switch {
		case arg == "--bucket":
			bucket, err = value()
		case arg == "--region":
			region, err = value()
		case arg == "--name":
			name, err = value()
		case arg == "--at":
			at, err = value()
		case arg == "--keep":
			keep, err = value()
		case arg == "--apps-default":
			appsDefault, err = value()
		case arg == "--limit-upload":
			limit, err = value()
		case arg == "--daily" && verb == "run":
			daily = true
		case arg == "--server" && (verb == "now" || verb == "test-restore"):
			stateOnly = true
		case arg == "--take-over":
			takeOver = true
		case arg == "--all":
			all = true
		case strings.HasPrefix(arg, "-"):
			return usageError{"unknown option " + arg + "\n" + serverBackupsUsage}
		case verb == "" && (arg == "on" || arg == "off" || arg == "now" || arg == "run" || arg == "test-restore" || arg == "__key"):
			verb = arg
		case verb != "":
			apps = append(apps, arg)
		default:
			return usageError{serverBackupsUsage}
		}
		if err != nil {
			return err
		}
	}
	if verb == "__key" {
		return serverStateKeyCommand(stdout)
	}
	if _, err := loadServerConfig(serverBackupsDir()); err != nil {
		return errors.New("this machine runs no server: hi server init makes one, and its backups are set there")
	}
	settings := bucket != "" || region != "" || name != "" || at != "" || keep != "" || appsDefault != "" || limit != "" || takeOver
	if verb != "" && settings || verb == "" && (all || len(apps) > 0) || stateOnly && len(apps) > 0 {
		return usageError{serverBackupsUsage}
	}
	switch verb {
	case "on", "off":
		return serverBackupsTurn(verb == "on", apps, all, stdout)
	case "now":
		// Every app that is copied, and the server's state; or the apps
		// named; or, with --server, the server's state alone.
		b := loadServerBackups()
		return serverBackupsRunAll(false, apps, !stateOnly, stateOnly || len(apps) == 0 && b.on() && vaultExists(), stdout)
	case "test-restore":
		// The apps named, or every app that is copied and the server's
		// state; with --server, the server's state alone.
		b := loadServerBackups()
		return serverBackupsTestRestore(apps, !stateOnly, stateOnly || len(apps) == 0 && b.on() && vaultExists(), stdout)
	case "run":
		if !daily || len(apps) > 0 {
			return usageError{"usage: hi server backups run --daily"}
		}
		return serverBackupsRunAll(true, nil, true, loadServerBackups().on() && vaultExists(), stdout)
	}
	if bucket != "" {
		if err := serverBackupsSetBucket(bucket, region, name, takeOver, stdin, stdout); err != nil {
			return err
		}
	} else if region != "" || name != "" || takeOver {
		return usageError{"--region, --name, and --take-over go with --bucket"}
	}
	if at != "" || keep != "" || appsDefault != "" || limit != "" {
		if err := serverBackupsSetPlan(at, keep, appsDefault, limit, stdout); err != nil {
			return err
		}
	}
	return serverBackupsShow(stdout)
}

func serverBackupsSetBucket(address, region, name string, takeOver bool, stdin io.Reader, stdout io.Writer) error {
	b := loadServerBackups()
	if address == "off" {
		if region != "" || name != "" || takeOver {
			return usageError{"--bucket off takes nothing else"}
		}
		if err := removeSecretFile(serverBucketSecret(serverBackupsDir())); err != nil && !os.IsNotExist(err) {
			return err
		}
		b.Bucket, b.Region, b.Claim = "", "", ""
		if err := saveServerBackups(b); err != nil {
			return err
		}
		fmt.Fprintln(stdout, "The server's bucket is off: apps that followed it are no longer copied offsite. What is in the bucket stays there.")
		return nil
	}
	if !strings.HasPrefix(address, "s3:https://") && !strings.HasPrefix(address, "s3:http://") {
		return usageError{"--bucket takes an S3-compatible bucket as restic names it: s3:https://<host>/<bucket>[/<path>]"}
	}
	if err := appCheckBucket(address); err != nil {
		return usageError{err.Error()}
	}
	name = firstNonEmpty(name, b.Name, serverDefaultName())
	if !serverBackupsNamePattern.MatchString(name) {
		return usageError{"--name is a folder name: small letters, digits, and -"}
	}
	next := b
	if strings.TrimRight(address, "/") != strings.TrimRight(b.Bucket, "/") || name != b.Name {
		next.Claim = ""
	}
	next.Bucket, next.Name = strings.TrimRight(address, "/"), name
	next.Region = firstNonEmpty(region, appBucketRegion(address))
	id, secret, err := appAskBucketKeys(stdin, stdout, "HI_S3_ACCESS_KEY_ID", "HI_S3_SECRET_ACCESS_KEY")
	if err != nil {
		return err
	}
	keys := appBucket{KeyID: id, Secret: secret, Region: next.region()}
	claim, err := serverClaimFolder(next, keys, takeOver)
	if err != nil {
		return err
	}
	next.Claim = claim
	encoded, _ := json.Marshal(serverBucketKeys{KeyID: id, Secret: secret})
	if err := writeSecretFile(serverBucketSecret(serverBackupsDir()), append(encoded, '\n'), 0o600); err != nil {
		return err
	}
	if err := saveServerBackups(next); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "The server's bucket is %s/: hi reached it and wrote %s there.\n", next.folder(), serverClaimName)
	fmt.Fprintf(stdout, "Each app's snapshots are copied after its daily snapshot (around %s) and before each release; hi server backups now copies them at once.\n", next.at())
	return nil
}

func serverBackupsSetPlan(at, keep, appsDefault, limit string, stdout io.Writer) error {
	b := loadServerBackups()
	timers := false
	if at != "" {
		parsed, err := time.Parse("15:04", at)
		if err != nil {
			return usageError{"--at takes a time of day, HH:MM: --at 03:00"}
		}
		if formatted := parsed.Format("15:04"); formatted != b.at() {
			b.At, timers = formatted, true
		}
	}
	if keep != "" {
		counts, err := appParseKeep(keep)
		if err != nil {
			return err
		}
		b.Daily, b.Weekly, b.Monthly = counts[0], counts[1], counts[2]
	}
	switch limit {
	case "":
	case "off", "0":
		b.LimitUpload = 0
	default:
		n, err := strconv.Atoi(limit)
		if err != nil || n < 1 || n > 10_000_000 {
			return usageError{"--limit-upload takes KiB/s, such as 5000 (about 40 Mbit/s), or off"}
		}
		b.LimitUpload = n
	}
	switch appsDefault {
	case "":
	case "on":
		b.AppsOff = false
	case "off":
		b.AppsOff = true
	default:
		return usageError{"--apps-default is on or off"}
	}
	if err := saveServerBackups(b); err != nil {
		return err
	}
	if timers {
		serverRewriteSnapshotTimers(stdout)
	}
	return nil
}

// serverRewriteSnapshotTimers writes each app's daily timer again, for a
// new time, and restarts those that run.
func serverRewriteSnapshotTimers(stdout io.Writer) {
	changed := []string{}
	if serverBackupsQueued() {
		_, timer := serverBackupsUnits("", loadServerBackups())
		if err := os.WriteFile(serverBackupsTimerPath(), []byte(timer), 0o644); err != nil {
			fmt.Fprintf(stdout, "hi: %s: %v\n", serverBackupsUnit+".timer", err)
		} else {
			appSystemctl("daemon-reload")
			appSystemctl("restart", serverBackupsUnit+".timer")
		}
	}
	for _, name := range machineAppNames() {
		config, err := loadTeam(name)
		if err != nil {
			continue
		}
		path := appSnapshotTimerPath(name)
		if !fileExists(path) {
			continue
		}
		_, timer := appSnapshotUnits(config, "")
		if err := os.WriteFile(path, []byte(timer), 0o644); err != nil {
			fmt.Fprintf(stdout, "hi: app %s's timer: %v\n", name, err)
			continue
		}
		changed = append(changed, name)
	}
	if len(changed) == 0 {
		return
	}
	appSystemctl("daemon-reload")
	for _, name := range changed {
		timer := appSnapshotUnitName(name) + ".timer"
		if out, _ := appSystemctl("is-enabled", timer); strings.TrimSpace(out) == "enabled" {
			appSystemctl("restart", timer)
		}
	}
}

func serverBackupsTurn(on bool, apps []string, all bool, stdout io.Writer) error {
	server := loadServerBackups()
	if on && !server.on() {
		return errors.New("the server has no bucket yet: hi server backups --bucket s3:https://<host>/<bucket> sets it")
	}
	if all {
		if len(apps) > 0 {
			return usageError{"name apps, or --all"}
		}
		apps = machineAppNames()
		sort.Strings(apps)
	}
	if len(apps) == 0 {
		return usageError{serverBackupsUsage}
	}
	for _, name := range apps {
		if _, err := loadTeam(name); err != nil {
			return err
		}
	}
	for _, name := range apps {
		config, _ := loadTeam(name)
		own := appBackups{}
		if config.Backups != nil {
			own = *config.Backups
		}
		before := config.offsiteUse(server)
		if on {
			own.Use = appOffsiteServer
		} else {
			own.Use = appOffsiteOff
		}
		if on && own.Offsite != "" {
			fmt.Fprintf(stdout, "App %s copied to a bucket of its own, %s; what is there stays.\n", name, own.Offsite)
		}
		if on || before != appOffsiteOwn {
			own.Offsite, own.Region = "", ""
			if secrets, err := loadTeamSecrets(name); err == nil && (secrets.BackupKeyID != "" || secrets.BackupSecret != "") {
				secrets.BackupKeyID, secrets.BackupSecret = "", ""
				if err := saveTeamSecrets(name, secrets); err != nil {
					return err
				}
			}
		} else {
			// Off for an app with its own bucket: its address stays, for
			// hi app backups --offsite to turn on again.
			own.Use = appOffsiteOff
		}
		config.Backups = &own
		if err := saveTeam(config); err != nil {
			return err
		}
		if on {
			fmt.Fprintf(stdout, "App %s: copied to %s after each snapshot.\n", name, server.appAddress(name))
		} else {
			fmt.Fprintf(stdout, "App %s: not copied offsite. Its snapshots on this machine go on.\n", name)
		}
	}
	return nil
}

func serverBackupsShow(stdout io.Writer) error {
	b := loadServerBackups()
	if b.on() {
		fmt.Fprintf(stdout, "Bucket: %s/ (region %s)\n", b.folder(), firstNonEmpty(b.region(), appDefaultRegion))
	} else {
		fmt.Fprintln(stdout, "Bucket: none. hi server backups --bucket s3:https://<host>/<bucket> sets one for every app.")
	}
	keep := b.keep()
	fmt.Fprintf(stdout, "Plan: a snapshot of each app daily around %s, copied offsite after it; keep %d days, %d weeks, %d months.\n", b.at(), keep.Daily, keep.Weekly, keep.Monthly)
	if b.LimitUpload > 0 {
		fmt.Fprintf(stdout, "Uploads are limited to %d KiB/s.\n", b.LimitUpload)
	}
	if serverBackupsQueued() {
		fmt.Fprintf(stdout, "%s takes the snapshots one app after another, then backs up the server's state.\n", serverBackupsUnit+".timer")
	}
	if b.on() {
		state := loadServerStateStatus()
		switch {
		case state.Failed != "" && state.FailedAt.After(state.Last):
			fmt.Fprintf(stdout, "The server's state: failed %s: %s\n", state.FailedAt.Local().Format("Jan 2 15:04"), state.Failed)
		case !state.Last.IsZero() && state.Repository == b.stateRepository():
			fmt.Fprintf(stdout, "The server's state (vault, admins, settings): copied %s, %s.\n", state.Last.Local().Format("Jan 2 15:04"), qSize(state.Size))
		default:
			fmt.Fprintln(stdout, "The server's state (vault, admins, settings): not copied yet (hi server backups now --server).")
		}
	}
	if b.AppsOff {
		fmt.Fprintln(stdout, "Apps are copied only when turned on (hi server backups on <app>).")
	} else {
		fmt.Fprintln(stdout, "Every app is copied unless turned off (hi server backups off <app>).")
	}
	apps := serverBackupsApps(b)
	if len(apps) == 0 {
		return nil
	}
	fmt.Fprintln(stdout)
	table := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(table, "APP\tOFFSITE\tLAST SNAPSHOT\tLAST COPY\tCHECKED\tKEEP")
	when := func(t time.Time) string {
		if t.IsZero() {
			return "-"
		}
		return t.Local().Format("Jan 2 15:04")
	}
	for _, app := range apps {
		use := map[string]string{appOffsiteServer: "server's bucket", appOffsiteOwn: "own bucket", appOffsiteOff: "off", appOffsiteNone: "-"}[app.Use]
		copied := when(app.Copied)
		if app.CopyFailed != "" {
			copied = "failed " + when(app.CopyFailedAt)
		}
		if app.Offsite == "" {
			copied = "-"
		}
		snapshot := when(app.Last)
		if app.Failed != "" && app.FailedAt.After(app.Last) {
			snapshot = "failed " + when(app.FailedAt)
		}
		checked := when(app.Checked)
		if app.CheckFailed != "" {
			checked = "failed"
		}
		if app.Offsite == "" {
			checked = "-"
		}
		fmt.Fprintf(table, "%s\t%s\t%s\t%s\t%s\t%s\n", app.Name, use, snapshot, copied, checked, app.Keep)
	}
	table.Flush()
	for _, app := range apps {
		if app.CopyFailed != "" && app.Offsite != "" {
			fmt.Fprintf(stdout, "%s: %s\n", app.Name, app.CopyFailed)
		}
		if app.CheckFailed != "" && app.Offsite != "" {
			fmt.Fprintf(stdout, "%s: the bucket's copy failed its check: %s\n", app.Name, app.CheckFailed)
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// the admin page's Backups

// serverBackupsView is what the page's Backups shows.
type serverBackupsView struct {
	On      bool               `json:"on"`
	Bucket  string             `json:"bucket,omitempty"`
	Region  string             `json:"region,omitempty"`
	Name    string             `json:"name"` // the folder's name, or the one it would get
	Folder  string             `json:"folder,omitempty"`
	At      string             `json:"at"`
	Keep    string             `json:"keep"`
	AppsOff bool               `json:"apps_off,omitempty"`
	Apps    []serverBackupsApp `json:"apps"`
	// Running are the backup jobs (Back up now) that run: the page marks
	// their apps as copying until each is in the bucket.
	Running []serverBackupsJob `json:"running,omitempty"`
	// LimitUpload is the plan's upload limit, KiB/s.
	LimitUpload int `json:"limit_upload,omitempty"`
	// Queued: hi-backups.timer takes the snapshots, one app at a time.
	Queued bool `json:"queued"`
	// Server is the server's own state's copy; Vault says it can be copied.
	Server serverStateStatus  `json:"server"`
	Vault  bool               `json:"vault"`
	Runs   []serverBackupsRun `json:"runs"`
}

// serverBackupsJob is a running hi server backups now, from the page.
type serverBackupsJob struct {
	ID      string    `json:"id"`
	Title   string    `json:"title"`
	Apps    []string  `json:"apps,omitempty"`   // none: every app that is copied
	Server  bool      `json:"server,omitempty"` // the server's state alone
	Test    bool      `json:"test,omitempty"`   // a restore test, not a backup
	Started time.Time `json:"started"`
}

// serverBackupsRunning are the page's backup jobs that still run.
func serverBackupsRunning() []serverBackupsJob {
	var running []serverBackupsJob
	for _, job := range adminJobs() {
		if job.State != "running" || len(job.Args) < 3 || job.Args[0] != "server" || job.Args[1] != "backups" || job.Args[2] != "now" && job.Args[2] != "test-restore" {
			continue
		}
		line := serverBackupsJob{ID: job.ID, Title: job.Title, Apps: job.Args[3:], Started: job.Started, Test: job.Args[2] == "test-restore"}
		if slices.Contains(line.Apps, "--server") {
			line.Apps, line.Server = nil, true
		}
		running = append(running, line)
	}
	return running
}

func serverBackupsViewOf() serverBackupsView {
	b := loadServerBackups()
	keep := b.keep()
	view := serverBackupsView{On: b.on(), Name: firstNonEmpty(b.Name, serverDefaultName()), At: b.at(),
		Keep: fmt.Sprintf("%d,%d,%d", keep.Daily, keep.Weekly, keep.Monthly), AppsOff: b.AppsOff, Apps: serverBackupsApps(b),
		Running: serverBackupsRunning(), LimitUpload: b.LimitUpload, Queued: serverBackupsQueued(), Vault: vaultExists(), Runs: serverBackupsRuns(30)}
	if b.on() {
		view.Server = loadServerStateStatus()
		if view.Server.Repository != b.stateRepository() {
			view.Server.Last, view.Server.Size, view.Server.Added = time.Time{}, 0, 0
		}
	}
	if view.Runs == nil {
		view.Runs = []serverBackupsRun{}
	}
	if b.on() {
		view.Bucket, view.Region, view.Folder = b.Bucket, b.region(), b.folder()+"/"
	}
	return view
}

// serverBackupsChange is a change from the page.
type serverBackupsChange struct {
	What        string   `json:"what"` // bucket, off, plan, apps, now, or test
	Bucket      string   `json:"bucket"`
	Region      string   `json:"region"`
	Name        string   `json:"name"`
	KeyID       string   `json:"key_id"`
	Secret      string   `json:"secret"`
	TakeOver    bool     `json:"take_over"`
	At          string   `json:"at"`
	Keep        string   `json:"keep"`
	AppsDefault string   `json:"apps_default"`
	Apps        []string `json:"apps"`
	On          bool     `json:"on"`
	Server      bool     `json:"server"` // now: the server's state alone
	LimitUpload string   `json:"limit_upload"`
}

// serverBackupsArgs is the hi server backups command a change runs, what
// goes on its stdin, and what the activity log says.
func serverBackupsArgs(c serverBackupsChange) (args []string, stdin, said string, err error) {
	for _, app := range c.Apps {
		if !appNamePattern.MatchString(app) {
			return nil, "", "", fmt.Errorf("%q isn't an app's name", app)
		}
	}
	clean := func(s string) string { return strings.TrimSpace(s) }
	switch c.What {
	case "bucket":
		if clean(c.Bucket) == "" || clean(c.KeyID) == "" || clean(c.Secret) == "" {
			return nil, "", "", errors.New("give the bucket's address and both keys")
		}
		args = []string{"server", "backups", "--bucket", clean(c.Bucket)}
		if clean(c.Region) != "" {
			args = append(args, "--region", clean(c.Region))
		}
		if clean(c.Name) != "" {
			args = append(args, "--name", clean(c.Name))
		}
		if c.TakeOver {
			args = append(args, "--take-over")
		}
		return args, clean(c.KeyID) + "\n" + clean(c.Secret) + "\n", "set the server's bucket: " + clean(c.Bucket), nil
	case "off":
		return []string{"server", "backups", "--bucket", "off"}, "", "turned the server's bucket off", nil
	case "plan":
		args = []string{"server", "backups"}
		if clean(c.At) != "" {
			args = append(args, "--at", clean(c.At))
		}
		if clean(c.Keep) != "" {
			args = append(args, "--keep", clean(c.Keep))
		}
		if c.AppsDefault == "on" || c.AppsDefault == "off" {
			args = append(args, "--apps-default", c.AppsDefault)
		}
		if limit := clean(c.LimitUpload); limit != "" {
			args = append(args, "--limit-upload", limit)
		}
		return args, "", "changed the backup plan: " + strings.Join(args[2:], " "), nil
	case "apps":
		if len(c.Apps) == 0 {
			return nil, "", "", errors.New("pick the apps")
		}
		verb := map[bool]string{true: "on", false: "off"}[c.On]
		return append([]string{"server", "backups", verb}, c.Apps...), "", "turned offsite copies " + verb + " for " + strings.Join(c.Apps, ", "), nil
	case "test":
		if c.Server {
			if len(c.Apps) > 0 {
				return nil, "", "", errors.New("the server's state, or apps")
			}
			return []string{"server", "backups", "test-restore", "--server"}, "", "", nil
		}
		return append([]string{"server", "backups", "test-restore"}, c.Apps...), "", "", nil
	case "now":
		if c.Server {
			if len(c.Apps) > 0 {
				return nil, "", "", errors.New("the server's state, or apps")
			}
			return []string{"server", "backups", "now", "--server"}, "", "", nil
		}
		return append([]string{"server", "backups", "now"}, c.Apps...), "", "", nil
	}
	return nil, "", "", errors.New("bucket, off, plan, apps, now, or test")
}

func (s *adminServer) backupsRoutes(mux *http.ServeMux, reply func(http.ResponseWriter, int, any),
	decode func(http.ResponseWriter, *http.Request, any) bool,
	signedIn func(func(http.ResponseWriter, *http.Request, adminPerson)) http.HandlerFunc) {
	failure := func(w http.ResponseWriter, status int, err error) {
		reply(w, status, map[string]string{"error": err.Error()})
	}
	mux.HandleFunc("GET /api/backups", signedIn(func(w http.ResponseWriter, r *http.Request, admin adminPerson) {
		if adminOrgDir() == "" {
			failure(w, http.StatusNotFound, errors.New("this machine runs no server"))
			return
		}
		reply(w, http.StatusOK, serverBackupsViewOf())
	}))
	mux.HandleFunc("POST /api/backups", signedIn(func(w http.ResponseWriter, r *http.Request, admin adminPerson) {
		if adminOrgDir() == "" {
			failure(w, http.StatusNotFound, errors.New("this machine runs no server"))
			return
		}
		var change serverBackupsChange
		if !decode(w, r, &change) {
			return
		}
		args, stdin, said, err := serverBackupsArgs(change)
		if err != nil {
			failure(w, http.StatusBadRequest, err)
			return
		}
		if change.What == "now" || change.What == "test" {
			title := "Back up every app and the server now"
			if change.Server {
				title = "Back up the server's state now"
			} else if len(change.Apps) > 0 {
				title = "Back up " + strings.Join(change.Apps, ", ") + " now"
			}
			if change.What == "test" {
				title = "Test restoring every app and the server"
				if change.Server {
					title = "Test restoring the server's state"
				} else if len(change.Apps) > 0 {
					title = "Test restoring " + strings.Join(change.Apps, ", ")
				}
			}
			job, err := s.startJob(adminJob{Title: title, By: admin.Name, Args: args})
			if err != nil {
				failure(w, http.StatusInternalServerError, err)
				return
			}
			reply(w, http.StatusOK, map[string]any{"job": job})
			return
		}
		text, err := adminRunHi(s.hi, admin.Name, stdin, args...)
		if err != nil {
			failure(w, http.StatusBadRequest, err)
			return
		}
		adminRecord(admin.Name, "", said)
		reply(w, http.StatusOK, map[string]any{"text": text, "view": serverBackupsViewOf()})
	}))
}
