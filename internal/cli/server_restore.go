package cli

// Release 3 of hi_server_backups.md: a restore test that changes nothing
// (hi server backups test-restore), and a whole server brought back on a
// new machine from its folder in the bucket (hi server restore).

import (
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// the restore test

// resticListed is one snapshot as restic snapshots --json lists it.
type resticListed struct {
	ID      string    `json:"short_id"`
	Time    time.Time `json:"time"`
	Tags    []string  `json:"tags"`
	Summary *struct {
		Files int64 `json:"total_files_processed"`
		Bytes int64 `json:"total_bytes_processed"`
	} `json:"summary"`
}

// resticLatest is a host's newest snapshot that isn't a warm-up pass.
func resticLatest(env []string, dir, host string) (resticListed, error) {
	out, err := appRestic(env, dir, "snapshots", "--json", "--host", host)
	if err != nil {
		return resticListed{}, err
	}
	var listed []resticListed
	if err := json.Unmarshal([]byte(out), &listed); err != nil {
		return resticListed{}, fmt.Errorf("restic's list of snapshots: %w", err)
	}
	sort.Slice(listed, func(i, j int) bool { return listed[i].Time.Before(listed[j].Time) })
	for i := len(listed) - 1; i >= 0; i-- {
		warmup := false
		for _, tag := range listed[i].Tags {
			warmup = warmup || tag == "kind:"+appSnapshotWarmup
		}
		if !warmup {
			return listed[i], nil
		}
	}
	return resticListed{}, errors.New("the bucket has no snapshots")
}

// serverRestoreTestDir is where a test restores to, removed after.
func serverRestoreTestDir(what string) (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(cache, "hi", "restore-test", what), nil
}

// treeSize counts a restored folder's files and bytes.
func treeSize(dir string) (files, bytes int64) {
	filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err == nil && entry.Type().IsRegular() {
			if info, err := entry.Info(); err == nil {
				files++
				bytes += info.Size()
			}
		}
		return nil
	})
	return files, bytes
}

// sealedForThisVault says hi-key.sealed opens with this vault's recovery
// key: the words on paper are the ones that bring the copy back.
func sealedForThisVault(data []byte) error {
	var sealed appSealedKey
	if err := json.Unmarshal(data, &sealed); err != nil {
		return fmt.Errorf("%s doesn't read: %w", appSealedKeyName, err)
	}
	keys, err := loadVaultKeys()
	if err != nil {
		return err
	}
	if len(keys.RecoveryPublic) != 32 {
		return errors.New("this vault has no recovery key yet: hi server vault recovery")
	}
	if string(sealed.Public) != string(keys.RecoveryPublic) {
		return fmt.Errorf("%s is sealed to an older recovery key; the next copy seals it again", appSealedKeyName)
	}
	return nil
}

// serverTestRestoreApp restores the app's newest copy from the bucket to
// a folder of its own, has restic read every file back (--verify), checks
// it is the app's, and that its key beside it opens with this vault's
// recovery key; then removes the folder. Nothing else changes.
func serverTestRestoreApp(name string, stdout io.Writer) (run serverBackupsRun, err error) {
	run = serverBackupsRun{At: time.Now().UTC(), What: "test", App: name}
	started := time.Now()
	defer func() {
		run.Took = time.Since(started).Seconds()
		if err != nil {
			run.Error = err.Error()
		}
		now := time.Now().UTC()
		updateTeamSnapshotStatus(name, func(s *appSnapshotStatus) {
			if err != nil {
				s.RestoreTestFailed = err.Error()
				return
			}
			s.RestoreTested, s.RestoreTestFailed = now, ""
		})
		serverBackupsRecord(run)
	}()
	config, err := loadTeam(name)
	if err != nil {
		return run, err
	}
	backups := config.backups()
	if backups.Offsite == "" {
		return run, errors.New("it isn't copied offsite")
	}
	bucket, err := appOffsiteBucket(name, backups)
	if err != nil {
		return run, err
	}
	env := slicesConcatStrings([]string{"RESTIC_REPOSITORY=" + backups.Offsite}, appResticKeyEnv(name, "RESTIC_PASSWORD"),
		[]string{"AWS_ACCESS_KEY_ID=" + bucket.KeyID, "AWS_SECRET_ACCESS_KEY=" + bucket.Secret, "AWS_DEFAULT_REGION=" + firstNonEmpty(backups.Region, appDefaultRegion)})
	target, err := serverRestoreTestDir(name)
	if err != nil {
		return run, err
	}
	os.RemoveAll(target)
	defer os.RemoveAll(target)
	if err := os.MkdirAll(target, 0o700); err != nil {
		return run, err
	}
	latest, err := resticLatest(env, appSnapshotsDir(name), appSnapshotHost(name))
	if err != nil {
		return run, err
	}
	fmt.Fprintf(stdout, "App %s: restoring snapshot %s, taken %s, from %s…\n", name, latest.ID, latest.Time.Local().Format("Jan 2 15:04"), backups.Offsite)
	if _, err := appRestic(env, appSnapshotsDir(name), "restore", latest.ID, "--quiet", "--verify", "--target", target); err != nil {
		return run, fmt.Errorf("restoring: %w", err)
	}
	var restored appConfig
	data, err := os.ReadFile(filepath.Join(target, appConfigFile))
	if err == nil {
		err = json.Unmarshal(data, &restored)
	}
	if err != nil {
		return run, fmt.Errorf("the restored copy has no %s that reads: %v", appConfigFile, err)
	}
	if restored.Name != name && restored.madeAs() != config.madeAs() {
		return run, fmt.Errorf("the restored copy is app %s's", restored.Name)
	}
	files, size := treeSize(target)
	run.Size = size
	if latest.Summary != nil && latest.Summary.Files > 0 && files < latest.Summary.Files {
		return run, fmt.Errorf("%d files came back of the %d in the snapshot", files, latest.Summary.Files)
	}
	sealed, err := appBucketGet(backups.Offsite, bucket)
	if err == nil {
		err = sealedForThisVault(sealed)
	}
	if err != nil && vaultExists() {
		return run, err
	}
	run.Note = fmt.Sprintf("%d files, read back whole", files)
	fmt.Fprintf(stdout, "App %s: snapshot %s comes back whole: %d files, %s, read back and checked; its key opens with the recovery key.\n", name, latest.ID, files, qSize(size))
	return run, nil
}

// serverTestRestoreState does the same for the server's state: its
// folders come back, the vault's keys are this vault's, and its key opens
// with the recovery key.
func serverTestRestoreState(stdout io.Writer) (run serverBackupsRun, err error) {
	run = serverBackupsRun{At: time.Now().UTC(), What: "test"}
	started := time.Now()
	status := loadServerStateStatus()
	defer func() {
		run.Took = time.Since(started).Seconds()
		if err != nil {
			run.Error = err.Error()
			status.RestoreTestFailed = err.Error()
		} else {
			status.RestoreTested, status.RestoreTestFailed = time.Now().UTC(), ""
		}
		saveServerStateStatus(status)
		serverBackupsRecord(run)
	}()
	b := loadServerBackups()
	if !b.on() {
		return run, errors.New("the server has no bucket")
	}
	bucket, err := loadServerBucket(b)
	if err != nil {
		return run, err
	}
	key, err := serverStateKey()
	if err != nil {
		return run, err
	}
	env, cleanup, err := serverStateEnv(b, bucket, key)
	if err != nil {
		return run, err
	}
	defer cleanup()
	target, err := serverRestoreTestDir("server")
	if err != nil {
		return run, err
	}
	os.RemoveAll(target)
	defer os.RemoveAll(target)
	if err := os.MkdirAll(target, 0o700); err != nil {
		return run, err
	}
	latest, err := resticLatest(env, target, b.stateHost())
	if err != nil {
		return run, err
	}
	fmt.Fprintf(stdout, "The server's state: restoring snapshot %s, taken %s, from %s…\n", latest.ID, latest.Time.Local().Format("Jan 2 15:04"), b.stateRepository())
	if _, err := appRestic(env, target, "restore", latest.ID, "--quiet", "--verify", "--target", target); err != nil {
		return run, fmt.Errorf("restoring: %w", err)
	}
	for _, need := range []string{"org/config.json", "vault/keys.json"} {
		if !fileExists(filepath.Join(target, need)) {
			return run, fmt.Errorf("the restored copy has no %s", need)
		}
	}
	var restoredKeys vaultKeys
	data, err := os.ReadFile(filepath.Join(target, "vault", "keys.json"))
	if err == nil {
		err = json.Unmarshal(data, &restoredKeys)
	}
	if err != nil {
		return run, fmt.Errorf("the restored vault's keys.json: %v", err)
	}
	if keys, err := loadVaultKeys(); err == nil && string(keys.RecoveryPublic) != string(restoredKeys.RecoveryPublic) {
		return run, errors.New("the restored vault opens with another recovery key than this one")
	}
	sealed, found, err := appBucketGetObject(b.stateRepository(), appSealedKeyName, bucket)
	if err != nil {
		return run, err
	}
	if !found {
		return run, fmt.Errorf("no %s beside it", appSealedKeyName)
	}
	if err := sealedForThisVault(sealed); err != nil {
		return run, err
	}
	files, size := treeSize(target)
	run.Size, run.Note = size, fmt.Sprintf("%d files, read back whole", files)
	fmt.Fprintf(stdout, "The server's state: snapshot %s comes back whole: %d files, %s, with the vault and its keys; its key opens with the recovery key.\n", latest.ID, files, qSize(size))
	return run, nil
}

// serverBackupsTestRestore tests the apps named (none: every app that is
// copied offsite) and, with server, the server's state.
func serverBackupsTestRestore(apps []string, withApps, withServer bool, stdout io.Writer) error {
	unlock, err := serverBackupsLock(stdout)
	if err != nil {
		return err
	}
	defer unlock()
	if withApps && len(apps) == 0 {
		server := loadServerBackups()
		for _, line := range serverBackupsApps(server) {
			if line.Offsite != "" {
				apps = append(apps, line.Name)
			}
		}
	}
	var failed []string
	for _, name := range apps {
		if _, err := serverTestRestoreApp(name, stdout); err != nil {
			fmt.Fprintf(stdout, "App %s: the restore test failed: %v\n", name, err)
			failed = append(failed, name)
		}
	}
	if withServer {
		if _, err := serverTestRestoreState(stdout); err != nil {
			fmt.Fprintf(stdout, "The server's state: the restore test failed: %v\n", err)
			failed = append(failed, "the server's state")
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("the restore test failed for %s", strings.Join(failed, ", "))
	}
	return nil
}

// ---------------------------------------------------------------------------
// the apps in a server's folder

// serverBucketApps are the apps with a folder under <folder>/apps.
func serverBucketApps(folder string, bucket appBucket) ([]string, error) {
	if strings.HasPrefix(folder, "/") {
		entries, err := os.ReadDir(filepath.Join(folder, "apps"))
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
		var names []string
		for _, entry := range entries {
			if entry.IsDir() && appNamePattern.MatchString(entry.Name()) {
				names = append(names, entry.Name())
			}
		}
		return names, nil
	}
	rest, ok := strings.CutPrefix(folder, "s3:")
	if !ok {
		return nil, fmt.Errorf("%s isn't a bucket's address", folder)
	}
	if !strings.Contains(rest, "://") {
		rest = "https://" + rest
	}
	parsed, err := url.Parse(rest)
	if err != nil {
		return nil, err
	}
	bucketName, prefix, _ := strings.Cut(strings.Trim(parsed.Path, "/"), "/")
	if prefix != "" {
		prefix += "/"
	}
	prefix += "apps/"
	var names []string
	token := ""
	for {
		query := url.Values{"list-type": {"2"}, "prefix": {prefix}, "delimiter": {"/"}}
		if token != "" {
			query.Set("continuation-token", token)
		}
		list := parsed.Scheme + "://" + parsed.Host + "/" + bucketName + "?" + query.Encode()
		data, status, err := appBucketCall("GET", list, bucket, nil)
		if err != nil {
			return nil, err
		}
		if status != http.StatusOK {
			return nil, fmt.Errorf("listing %s: the bucket answered %d: %s", folder, status, qFirstLine(strings.TrimSpace(string(data)), ""))
		}
		var answer struct {
			Prefixes []struct {
				Prefix string `xml:"Prefix"`
			} `xml:"CommonPrefixes"`
			Truncated bool   `xml:"IsTruncated"`
			Next      string `xml:"NextContinuationToken"`
		}
		if err := xml.Unmarshal(data, &answer); err != nil {
			return nil, fmt.Errorf("listing %s: %w", folder, err)
		}
		for _, p := range answer.Prefixes {
			name := strings.TrimSuffix(strings.TrimPrefix(p.Prefix, prefix), "/")
			if appNamePattern.MatchString(name) {
				names = append(names, name)
			}
		}
		if !answer.Truncated || answer.Next == "" {
			break
		}
		token = answer.Next
	}
	sort.Strings(names)
	return names, nil
}

// ---------------------------------------------------------------------------
// hi server restore

const serverRestoreUsage = `usage: hi server restore --from s3:https://<host>/<bucket>/<server> [--region <r>] [--apps-folder <dir>] [--no-apps] [--no-start] [--drill]`

// serverRestoreCommand brings a server back on this machine from its
// folder in the bucket: its state, its vault (opened with the recovery
// key and sealed again for this machine), its admin page, and its apps,
// which stay down until an admin starts each.
func serverRestoreCommand(args []string, stdin io.Reader, stdout io.Writer) error {
	var from, region, appsFolder string
	var noApps, noStart, drill bool
	for i := 0; i < len(args); i++ {
		arg := args[i]
		value := func() (string, error) {
			if i+1 >= len(args) {
				return "", usageError{arg + " takes a value\n" + serverRestoreUsage}
			}
			i++
			return args[i], nil
		}
		var err error
		switch arg {
		case "--from":
			from, err = value()
		case "--region":
			region, err = value()
		case "--apps-folder":
			appsFolder, err = value()
		case "--no-apps":
			noApps = true
		case "--no-start":
			noStart = true
		case "--drill":
			// A rehearsal while the server still runs elsewhere: nothing
			// starts, the bucket's folder stays the other server's, and the
			// copy here never writes to it.
			drill, noStart = true, true
		default:
			return usageError{serverRestoreUsage}
		}
		if err != nil {
			return err
		}
	}
	from = strings.TrimRight(from, "/")
	if from == "" {
		return usageError{serverRestoreUsage}
	}
	name := filepath.Base(from)
	if !strings.HasPrefix(from, "/") {
		if err := appCheckBucket(from); err != nil {
			return usageError{err.Error()}
		}
		rest := strings.TrimPrefix(strings.TrimPrefix(strings.TrimPrefix(from, "s3:"), "https://"), "http://")
		if strings.Count(rest, "/") < 2 {
			return usageError{"--from is the server's folder in the bucket, as hi server backups shows it: s3:https://<host>/<bucket>/<server>"}
		}
	}
	if !serverBackupsNamePattern.MatchString(name) {
		return usageError{fmt.Sprintf("%q isn't a server's folder name", name)}
	}
	dir, err := orgDirectory("")
	if err != nil {
		return err
	}
	if fileExists(filepath.Join(dir, "config.json")) {
		return fmt.Errorf("this machine runs a server already (%s): hi server restore brings one back on a new machine", dir)
	}
	if vaultExists() {
		return fmt.Errorf("this machine has a vault already (%s): hi server restore brings one back on a new machine", vaultDir())
	}
	if !appResticInstalled() {
		return errors.New("restic isn't installed: hi install restic adds it")
	}
	b := serverBackups{Bucket: strings.TrimSuffix(from, "/"+name), Name: name, Region: region}
	bucket := appBucket{Region: b.region()}
	if !strings.HasPrefix(from, "/") {
		if bucket.KeyID, err = readSecretLine(stdin, stdout, "The bucket's access key ID"); err != nil {
			return err
		}
		if bucket.Secret, err = readSecretLine(stdin, stdout, "Its secret access key"); err != nil {
			return err
		}
	}
	words, err := readSecretLine(stdin, stdout, "The recovery key's 24 words, of the server's vault")
	if err != nil {
		return err
	}
	if !strings.HasPrefix(from, "/") && (bucket.KeyID == "" || bucket.Secret == "") || strings.TrimSpace(words) == "" {
		return errors.New("hi server restore needs the bucket's access key ID, its secret access key, and the recovery key's 24 words")
	}
	// Whose folder it is, and its key, opened with the words.
	if data, found, err := appBucketGetObject(b.folder(), serverClaimName, bucket); err != nil {
		return fmt.Errorf("reaching %s: %w", b.folder(), err)
	} else if found {
		var claim serverClaim
		json.Unmarshal(data, &claim)
		fmt.Fprintf(stdout, "%s/ is server %s's, from %s (since %s).\n", b.folder(), name, firstNonEmpty(claim.Hostname, "a machine"), claim.Since.Local().Format("Jan 2, 2006"))
	}
	sealed, err := appBucketGet(b.stateRepository(), bucket)
	if err != nil {
		return fmt.Errorf("the server's state in %s: %w", b.stateRepository(), err)
	}
	key, sealedFor, err := appOpenSealedKey(sealed, words)
	if err != nil {
		return err
	}
	if sealedFor != "hi-server:"+name {
		return fmt.Errorf("the key in %s is %s's, not server %s's", b.stateRepository(), sealedFor, name)
	}
	// The newest snapshot of the state, into a folder beside, then each
	// part into its place.
	staging, err := serverStateStaging()
	if err != nil {
		return err
	}
	staging += "-restore"
	os.RemoveAll(staging)
	if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
		return err
	}
	if err := os.MkdirAll(staging, 0o700); err != nil {
		return err
	}
	defer os.RemoveAll(staging)
	keyFile := filepath.Join(staging, ".key")
	if err := os.WriteFile(keyFile, key, 0o600); err != nil {
		return err
	}
	env := []string{"RESTIC_REPOSITORY=" + b.stateRepository(), "RESTIC_PASSWORD_FILE=" + keyFile, "AWS_ACCESS_KEY_ID=" + bucket.KeyID,
		"AWS_SECRET_ACCESS_KEY=" + bucket.Secret, "AWS_DEFAULT_REGION=" + firstNonEmpty(bucket.Region, appDefaultRegion)}
	latest, err := resticLatest(env, staging, b.stateHost())
	if err != nil {
		return fmt.Errorf("the server's state in %s: %w", b.stateRepository(), err)
	}
	fmt.Fprintf(stdout, "Restoring the server's state, snapshot %s, taken %s…\n", latest.ID, latest.Time.Local().Format("Mon Jan 2 at 15:04"))
	restored := filepath.Join(staging, "state")
	if _, err := appRestic(env, staging, "restore", latest.ID, "--quiet", "--target", restored); err != nil {
		return fmt.Errorf("restoring the server's state: %w", err)
	}
	places := []struct{ part, to string }{{"org", dir}, {"vault", vaultDir()}, {"admin", adminDir()}}
	for _, place := range places {
		if entries, err := os.ReadDir(place.to); err == nil && len(entries) > 0 && fileExists(filepath.Join(restored, place.part)) {
			return fmt.Errorf("%s is there already, with files in it: hi server restore puts the server's %s there", place.to, place.part)
		}
	}
	if !fileExists(filepath.Join(restored, "org", "config.json")) || !fileExists(filepath.Join(restored, "vault", "keys.json")) {
		return errors.New("the snapshot has no server folder or vault in it")
	}
	for _, place := range places {
		from := filepath.Join(restored, place.part)
		if !fileExists(from) {
			continue
		}
		os.Remove(place.to) // an empty folder
		if err := os.MkdirAll(filepath.Dir(place.to), 0o700); err != nil {
			return err
		}
		if err := os.Rename(from, place.to); err != nil {
			return fmt.Errorf("putting the server's %s in %s: %w", place.part, place.to, err)
		}
		fmt.Fprintf(stdout, "%s back in %s.\n", map[string]string{"org": "The server's folder is", "vault": "Its vault is", "admin": "The admin page's settings are"}[place.part], place.to)
	}
	// The vault opened with the words, and sealed for this machine.
	vaultForget()
	kind, err := vaultUnlockHere(words)
	if err != nil {
		return fmt.Errorf("opening the restored vault with the recovery key: %w", err)
	}
	fmt.Fprintf(stdout, "The vault opens with the recovery key, and from now on with %s on this machine.\n", kind)
	if drill {
		// The copy here keeps no bucket: it can't write to the folder the
		// server it came from still writes to.
		removeSecretFile(serverBucketSecret(dir))
		current := loadServerBackups()
		current.Bucket, current.Region, current.Claim = "", "", ""
		if err := saveServerBackups(current); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "A drill: the copy here has no bucket, and %s/ stays the other server's.\n", b.folder())
	}
	// This machine writes to the server's folder from now on: the bucket's
	// keys as given, and the folder's claim taken over.
	if !drill {
		current := loadServerBackups()
		current.Bucket, current.Name = b.Bucket, b.Name
		if region != "" {
			current.Region = region
		}
		if !strings.HasPrefix(from, "/") {
			encoded, _ := json.Marshal(serverBucketKeys{KeyID: bucket.KeyID, Secret: bucket.Secret})
			if err := writeSecretFile(serverBucketSecret(dir), append(encoded, '\n'), 0o600); err != nil {
				return err
			}
		}
		if claim, err := serverClaimFolder(current, bucket, true); err != nil {
			fmt.Fprintf(stdout, "hi: %v; hi server backups --bucket %s --take-over claims it\n", err, current.Bucket)
		} else {
			current.Claim = claim
		}
		if err := saveServerBackups(current); err != nil {
			return err
		}
	}
	if err := serverRestoreListen(dir, stdout); err != nil {
		fmt.Fprintf(stdout, "hi: the server's address: %v\n", err)
	}
	// The apps, each from its folder; they stay down.
	var back []string
	if !noApps {
		names, err := serverBucketApps(b.folder(), bucket)
		if err != nil {
			return fmt.Errorf("the server's state is back, but its apps aren't: %w", err)
		}
		for _, app := range names {
			folder := ""
			if appsFolder != "" {
				folder = filepath.Join(appsFolder, "app-"+app)
			}
			fmt.Fprintf(stdout, "\nApp %s:\n", app)
			if err := appRestoreFromWith(app, b.appAddress(app), bucket, words, folder, "latest", true, stdout); err != nil {
				fmt.Fprintf(stdout, "App %s didn't come back: %v\n  hi app restore %s --from %s brings it back alone.\n", app, err, app, b.appAddress(app))
				continue
			}
			back = append(back, app)
		}
	}
	fmt.Fprintln(stdout)
	if drill {
		fmt.Fprintln(stdout, "The drill is done: nothing is started here. Don't start the server or its apps on this machine while the old one runs: they would take its NetBird peers.")
		return nil
	}
	if noStart {
		fmt.Fprintln(stdout, "The server isn't started (--no-start). hi server run starts it here; hi update installs its services.")
	} else if err := serverRestoreStart(dir, stdout); err != nil {
		fmt.Fprintf(stdout, "hi: the server didn't start: %v\n", err)
	}
	fmt.Fprintln(stdout, "\nNext:")
	for _, app := range back {
		fmt.Fprintf(stdout, "  hi app up %s\n", app)
	}
	if len(back) > 0 {
		fmt.Fprintln(stdout, "  Each app's NetBird peer isn't in its snapshots: give each a setup key on the admin page (its Network), for its addresses.")
	}
	fmt.Fprintln(stdout, "  hi server status                 whether devices reach the server; a device that can't joins again (hi server join)")
	return nil
}

// serverRestoreListen keeps the server's address when it is this
// machine's, and otherwise gives it this machine's NetBird address (or
// none, for a server on a NetBird peer of its own).
func serverRestoreListen(dir string, stdout io.Writer) error {
	config, err := loadServerConfig(dir)
	if err != nil {
		return err
	}
	host, port, err := net.SplitHostPort(config.Listen)
	if config.Listen == "" || err != nil || hostIsHere(host) {
		return nil
	}
	old := config.Listen
	_, err = updateServerConfig(dir, func(c *serverConfig) error {
		switch {
		case c.Peer != nil:
			c.Listen = ""
		default:
			address, err := netbirdAddress()
			if err != nil {
				return fmt.Errorf("it listened on %s, which isn't this machine, and this machine has no NetBird address: hi server init --listen sets one", old)
			}
			c.Listen = net.JoinHostPort(address, firstNonEmpty(port, strconv.Itoa(serverDefaultPort)))
		}
		return nil
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "The server listened on %s, the old machine's address; it listens on this machine's now. Devices joined to the old address join again (hi server join).\n", old)
	return nil
}

// hostIsHere says an address is one of this machine's.
var hostIsHere = func(host string) bool {
	addresses, err := net.InterfaceAddrs()
	if err != nil {
		return false
	}
	for _, address := range addresses {
		if ip, _, err := net.ParseCIDR(address.String()); err == nil && ip.String() == host {
			return true
		}
	}
	return host == "127.0.0.1" || host == "localhost" || host == "::1"
}

// serverRestoreStart installs and starts the server's service and its
// admin page, as hi server init does.
var serverRestoreStart = func(dir string, stdout io.Writer) error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	if resolved, err := filepath.EvalSymlinks(executable); err == nil {
		executable = resolved
	}
	if err := os.MkdirAll(filepath.Dir(orgUserUnitPath()), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(orgUserUnitPath(), []byte(orgUserUnitText(executable, dir)), 0o644); err != nil {
		return err
	}
	for _, step := range [][]string{{"daemon-reload"}, {"enable", orgUnit}, {"restart", orgUnit}} {
		if out, err := appSystemctl(step...); err != nil {
			return fmt.Errorf("systemctl --user %s: %s", strings.Join(step, " "), qFirstLine(out, err.Error()))
		}
	}
	fmt.Fprintf(stdout, "The server runs, as the user service %s.\n", orgUnit)
	if err := adminStart(nil, strings.NewReader(""), stdout); err != nil {
		return fmt.Errorf("the admin page: %w", err)
	}
	return nil
}
