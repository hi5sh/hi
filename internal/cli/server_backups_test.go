package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
)

// fakeS3 is a bucket service that keeps what is PUT, and checks the
// requests are signed with the given key ID.
func fakeS3(t *testing.T, keyID string) (*httptest.Server, map[string][]byte) {
	t.Helper()
	var mu sync.Mutex
	stored := map[string][]byte{}
	service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 Credential="+keyID+"/") {
			http.Error(w, "<Error><Code>InvalidAccessKeyId</Code></Error>", http.StatusForbidden)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if r.Method == "PUT" {
			stored[r.URL.Path], _ = io.ReadAll(r.Body)
			return
		}
		data, ok := stored[r.URL.Path]
		if !ok {
			http.Error(w, "<Error><Code>NoSuchKey</Code></Error>", http.StatusNotFound)
			return
		}
		w.Write(data)
	}))
	t.Cleanup(service.Close)
	return service, stored
}

// setupServerBackupsTest is an app (payments) on a machine with a server.
func setupServerBackupsTest(t *testing.T) {
	t.Helper()
	setupTeamTest(t)
	dir := filepath.Join(t.TempDir(), "server")
	os.MkdirAll(dir, 0o700)
	os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"listen":"127.0.0.1:7373"}`+"\n"), 0o600)
	t.Setenv("HI_ORG_DIR", dir)
	writeTeamFolderForSnapshots(t)
}

func TestServerBackupsBucket(t *testing.T) {
	setupServerBackupsTest(t)
	service, stored := fakeS3(t, "SRVKEY")
	address := "s3:" + service.URL + "/hi5-backups"
	var out bytes.Buffer
	// No bucket in the address, and wrong keys: nothing is saved.
	if err := serverBackupsCommand([]string{"--bucket", "s3:" + service.URL}, strings.NewReader("SRVKEY\nS\n"), &out); err == nil || !strings.Contains(err.Error(), "no bucket") {
		t.Fatal(err)
	}
	if err := serverBackupsCommand([]string{"--bucket", address, "--name", "aiw11"}, strings.NewReader("WRONG\nS\n"), &out); err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatal(err)
	}
	if loadServerBackups().on() {
		t.Fatal("saved after a failure")
	}
	if err := serverBackupsCommand([]string{"--bucket", address, "--name", "aiw11"}, strings.NewReader("SRVKEY\nSRVSECRET\n"), &out); err != nil {
		t.Fatal(err, out.String())
	}
	b := loadServerBackups()
	if b.Bucket != address || b.Name != "aiw11" || b.Claim == "" {
		t.Fatalf("%+v", b)
	}
	var claim serverClaim
	if err := json.Unmarshal(stored["/hi5-backups/aiw11/hi-server.json"], &claim); err != nil || claim.Claim != b.Claim {
		t.Fatal(string(stored["/hi5-backups/aiw11/hi-server.json"]), err)
	}
	// The keys are a secret, not in backups.json.
	if data, _ := os.ReadFile(filepath.Join(serverBackupsDir(), serverBackupsFile)); strings.Contains(string(data), "SRVSECRET") {
		t.Fatal(string(data))
	}
	keys, err := loadServerBucket(b)
	if err != nil || keys.KeyID != "SRVKEY" || keys.Secret != "SRVSECRET" {
		t.Fatal(keys, err)
	}
	// Setting it again (new keys) keeps the claim.
	if err := serverBackupsCommand([]string{"--bucket", address}, strings.NewReader("SRVKEY\nSECRET2\n"), &out); err != nil || loadServerBackups().Claim != b.Claim {
		t.Fatal(err, loadServerBackups())
	}

	// Another server with the same name is refused, unless it takes over.
	other := loadServerBackups()
	other.Claim = ""
	saveServerBackups(other)
	if err := serverBackupsCommand([]string{"--bucket", address}, strings.NewReader("SRVKEY\nS\n"), &out); err == nil || !strings.Contains(err.Error(), "belongs to another server") {
		t.Fatal(err)
	}
	if err := serverBackupsCommand([]string{"--bucket", address, "--take-over"}, strings.NewReader("SRVKEY\nS\n"), &out); err != nil || loadServerBackups().Claim == b.Claim {
		t.Fatal(err)
	}

	// Off forgets the bucket and its keys.
	if err := serverBackupsCommand([]string{"--bucket", "off"}, strings.NewReader(""), &out); err != nil || loadServerBackups().on() {
		t.Fatal(err)
	}
	if _, err := loadServerBucket(loadServerBackups()); err == nil {
		t.Fatal("the keys stayed")
	}
}

func TestServerBackupsWhereAnAppCopies(t *testing.T) {
	setupServerBackupsTest(t)
	server := serverBackups{}
	config, _ := loadTeam("payments")
	check := func(want, offsite string) {
		t.Helper()
		config, _ := loadTeam("payments")
		if use := config.offsiteUse(server); use != want {
			t.Fatalf("use %s, want %s", use, want)
		}
		if got := config.backupsWith(server).Offsite; got != offsite {
			t.Fatalf("offsite %s, want %s", got, offsite)
		}
	}
	// No server bucket: nowhere.
	check(appOffsiteNone, "")
	server = serverBackups{Bucket: "s3:https://s3.eu-central-003.backblazeb2.com/hi5", Name: "aiw11", Claim: "c"}
	// An app that hasn't chosen follows the server, with its region.
	check(appOffsiteServer, "s3:https://s3.eu-central-003.backblazeb2.com/hi5/aiw11/apps/payments")
	if b := config.backupsWith(server); !b.Server || b.Region != "eu-central-003" {
		t.Fatalf("%+v", b)
	}
	// Unless the server copies only apps turned on.
	server.AppsOff = true
	check(appOffsiteNone, "")
	server.AppsOff = false
	// A bucket of its own wins; one saved without a bucket doesn't count.
	config.Backups = &appBackups{Offsite: "s3:https://s3.example.com/mine"}
	saveTeam(config)
	check(appOffsiteOwn, "s3:https://s3.example.com/mine")
	config.Backups = &appBackups{Offsite: "s3:https://s3.eu-central-003.backblazeb2.com", Region: "eu-central-003"}
	saveTeam(config)
	check(appOffsiteServer, "s3:https://s3.eu-central-003.backblazeb2.com/hi5/aiw11/apps/payments")
	config.Backups = &appBackups{Use: "off"}
	saveTeam(config)
	check(appOffsiteOff, "")
	// The server's keep is the default; the app's own wins.
	server.Daily, server.Weekly, server.Monthly = 14, 8, 12
	if b := config.backupsWith(server); b.Daily != 14 || b.Monthly != 12 {
		t.Fatalf("%+v", b)
	}
	config.Backups = &appBackups{Daily: 3, Weekly: 2, Monthly: 1}
	saveTeam(config)
	if b := config.backupsWith(server); b.Daily != 3 || b.Monthly != 1 {
		t.Fatalf("%+v", b)
	}
}

// An app following the server copies with the server's keys, to its
// folder; on and off switch it, and now copies at once.
func TestServerBackupsOnOffNow(t *testing.T) {
	setupServerBackupsTest(t)
	restic := &fakeTeamRestic{}
	restic.install(t)
	saved := appPause
	appPause = func(string) (func(), string) { return func() {}, "" }
	t.Cleanup(func() { appPause = saved })
	var out bytes.Buffer
	if err := serverBackupsCommand([]string{"on", "payments"}, strings.NewReader(""), &out); err == nil || !strings.Contains(err.Error(), "no bucket") {
		t.Fatal(err)
	}
	service, _ := fakeS3(t, "SRVKEY")
	if err := serverBackupsCommand([]string{"--bucket", "s3:" + service.URL + "/hi5", "--name", "aiw11"}, strings.NewReader("SRVKEY\nSRVSECRET\n"), &out); err != nil {
		t.Fatal(err)
	}
	// An app with its own bucket goes to the server's when turned on.
	t.Setenv("HI_APP_S3_ACCESS_KEY_ID", "OWN")
	t.Setenv("HI_APP_S3_SECRET_ACCESS_KEY", "OWNSECRET")
	if err := appBackupsCommand([]string{"payments", "--offsite", "s3:https://s3.example.com/mine"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := serverBackupsCommand([]string{"on", "--all"}, strings.NewReader(""), &out); err != nil || !strings.Contains(out.String(), "a bucket of its own") {
		t.Fatal(err, out.String())
	}
	config, _ := loadTeam("payments")
	secrets, _ := loadTeamSecrets("payments")
	if config.Backups.Use != "server" || config.Backups.Offsite != "" || secrets.BackupSecret != "" {
		t.Fatalf("%+v %+v", config.Backups, secrets)
	}
	// Now: a snapshot, copied with the server's keys to the app's folder.
	out.Reset()
	if err := serverBackupsCommand([]string{"now"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err, out.String())
	}
	appAfterSnapshots.Wait()
	copied := -1 // the last copy: the one before is to the app's own bucket
	for i, call := range restic.calls {
		if call[0] == "copy" {
			copied = i
		}
	}
	if copied < 0 {
		t.Fatal(restic.calls)
	}
	env := strings.Join(restic.envs[copied], " ")
	if !strings.Contains(env, "RESTIC_REPOSITORY=s3:"+service.URL+"/hi5/aiw11/apps/payments") || !strings.Contains(env, "AWS_ACCESS_KEY_ID=SRVKEY") || !strings.Contains(env, "AWS_SECRET_ACCESS_KEY=SRVSECRET") {
		t.Fatal(env)
	}
	if !strings.Contains(out.String(), "is in the bucket") {
		t.Fatal(out.String())
	}
	// The listing shows it copied.
	out.Reset()
	if err := serverBackupsCommand(nil, strings.NewReader(""), &out); err != nil || !strings.Contains(out.String(), "server's bucket") || !strings.Contains(out.String(), "aiw11/") {
		t.Fatal(err, out.String())
	}
	// Off: no copy, snapshots go on.
	if err := serverBackupsCommand([]string{"off", "payments"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	if config, _ := loadTeam("payments"); config.backups().Offsite != "" {
		t.Fatal(config.Backups)
	}
}

// A new time rewrites the apps' daily timers and restarts those enabled.
func TestServerBackupsPlan(t *testing.T) {
	setupServerBackupsTest(t)
	var calls []string
	saved := appSystemctl
	appSystemctl = func(args ...string) (string, error) {
		calls = append(calls, strings.Join(args, " "))
		if args[0] == "is-enabled" {
			return "enabled", nil
		}
		return "", nil
	}
	t.Cleanup(func() { appSystemctl = saved })
	config, _ := loadTeam("payments")
	os.MkdirAll(filepath.Dir(appUnitPath("payments")), 0o755)
	if err := appInstallSnapshotTimer(config, "/usr/local/bin/hi"); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := serverBackupsCommand([]string{"--at", "25:00"}, strings.NewReader(""), &out); err == nil {
		t.Fatal("25:00")
	}
	if err := serverBackupsCommand([]string{"--at", "1:30", "--keep", "14,8,12", "--apps-default", "off"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	b := loadServerBackups()
	if b.At != "01:30" || b.Daily != 14 || !b.AppsOff {
		t.Fatalf("%+v", b)
	}
	timer, _ := os.ReadFile(filepath.Join(filepath.Dir(appUnitPath("payments")), appSnapshotUnitName("payments")+".timer"))
	if !strings.Contains(string(timer), "OnCalendar=*-*-* 01:30:00") {
		t.Fatal(string(timer))
	}
	if !slices.Contains(calls, "daemon-reload") || !slices.Contains(calls, "restart "+appSnapshotUnitName("payments")+".timer") {
		t.Fatal(calls)
	}
	// hi app backups --keep default goes back to the server's.
	config.Backups = &appBackups{Daily: 1, Weekly: 1, Monthly: 1}
	saveTeam(config)
	if err := appBackupsCommand([]string{"payments", "--keep", "default"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	if config, _ := loadTeam("payments"); config.backups().Daily != 14 {
		t.Fatal(config.backups())
	}
}

// The page's Backups runs hi server backups as the admin who pressed the
// button, with the keys on stdin; now is a job.
func TestAdminBackups(t *testing.T) {
	a, ts := orgAdminTest(t)
	t.Setenv("HI_ORG_DIR", ts.dir)
	olga := a.signIn(t, "olga")
	type run struct{ args, stdin string }
	var runs []run
	previous := adminRunHi
	adminRunHi = func(_, by string, stdin string, args ...string) (string, error) {
		runs = append(runs, run{strings.Join(args, " "), stdin})
		return "ok", nil
	}
	t.Cleanup(func() { adminRunHi = previous })

	var view serverBackupsView
	if code := a.get(t, olga, "/api/backups", &view); code != http.StatusOK || view.On || view.At != "03:00" || view.Keep != "7,4,6" || len(view.Apps) == 0 {
		t.Fatalf("%d %+v", code, view)
	}
	for _, c := range []struct {
		change map[string]any
		args   string
		stdin  string
	}{
		{map[string]any{"what": "bucket", "bucket": "s3:https://s3.eu-central-003.backblazeb2.com/hi5", "region": "eu-central-003", "name": "aiw11", "key_id": "K", "secret": "S"},
			"server backups --bucket s3:https://s3.eu-central-003.backblazeb2.com/hi5 --region eu-central-003 --name aiw11", "K\nS\n"},
		{map[string]any{"what": "bucket", "bucket": "s3:https://h/b", "key_id": "K", "secret": "S", "take_over": true}, "server backups --bucket s3:https://h/b --take-over", "K\nS\n"},
		{map[string]any{"what": "off"}, "server backups --bucket off", ""},
		{map[string]any{"what": "plan", "at": "02:00", "keep": "14,8,12", "apps_default": "off", "limit_upload": "5000"}, "server backups --at 02:00 --keep 14,8,12 --apps-default off --limit-upload 5000", ""},
		{map[string]any{"what": "apps", "apps": []string{"payments"}, "on": true}, "server backups on payments", ""},
		{map[string]any{"what": "apps", "apps": []string{"payments"}, "on": false}, "server backups off payments", ""},
	} {
		runs = nil
		var answer map[string]any
		if code := a.post(t, olga, "/api/backups", c.change, &answer); code != http.StatusOK {
			t.Fatalf("%v: %d %v", c.change, code, answer)
		}
		if len(runs) != 1 || runs[0].args != c.args || runs[0].stdin != c.stdin {
			t.Fatalf("%v ran %+v", c.change, runs)
		}
	}
	// No keys, a bad app name: refused before anything runs.
	runs = nil
	var answer map[string]any
	if code := a.post(t, olga, "/api/backups", map[string]any{"what": "bucket", "bucket": "s3:https://h/b"}, &answer); code != http.StatusBadRequest {
		t.Fatal(code, answer)
	}
	if code := a.post(t, olga, "/api/backups", map[string]any{"what": "apps", "apps": []string{"../x"}, "on": true}, &answer); code != http.StatusBadRequest || len(runs) != 0 {
		t.Fatal(code, answer, runs)
	}
	// The server's state alone is a job too.
	if code := a.post(t, olga, "/api/backups", map[string]any{"what": "now", "server": true}, &answer); code != http.StatusOK || answer["job"] == nil {
		t.Fatal(code, answer)
	}
	a.mu.Lock()
	if last := (*a.jobs)[len(*a.jobs)-1]; strings.Join(last.Args, " ") != "server backups now --server" {
		t.Fatalf("%+v", last)
	}
	a.mu.Unlock()
	if code := a.get(t, olga, "/api/backups", &view); code != http.StatusOK || len(view.Running) != 1 || !view.Running[0].Server || len(view.Running[0].Apps) != 0 {
		t.Fatalf("%d %+v", code, view.Running)
	}
	// Now is a job.
	if code := a.post(t, olga, "/api/backups", map[string]any{"what": "now", "apps": []string{"payments"}}, &answer); code != http.StatusOK || answer["job"] == nil {
		t.Fatal(code, answer)
	}
	a.mu.Lock()
	last := (*a.jobs)[len(*a.jobs)-1]
	a.mu.Unlock()
	if strings.Join(last.Args, " ") != "server backups now payments" || last.By != "olga" {
		t.Fatalf("%+v", last)
	}
	// While it runs, Backups shows it, for the page to mark its apps.
	if code := a.get(t, olga, "/api/backups", &view); code != http.StatusOK || len(view.Running) != 2 || view.Running[1].ID != last.ID && view.Running[0].ID != last.ID || strings.Join(view.Running[0].Apps, " ") != "payments" {
		t.Fatalf("%d %+v", code, view.Running)
	}
}

// The server's start installs the queue's timer, and only then are the
// apps' own timers removed; hi app up then adds none.
func TestServerBackupsQueueTimer(t *testing.T) {
	setupServerBackupsTest(t)
	var calls []string
	enabled := false
	saved := appSystemctl
	appSystemctl = func(args ...string) (string, error) {
		calls = append(calls, strings.Join(args, " "))
		switch {
		case args[0] == "is-enabled" && enabled:
			return "enabled", nil
		case args[0] == "is-enabled":
			return "disabled", nil
		case args[0] == "enable":
			enabled = true
		}
		return "", nil
	}
	t.Cleanup(func() { appSystemctl = saved })
	config, _ := loadTeam("payments")
	os.MkdirAll(filepath.Dir(appUnitPath("payments")), 0o755)
	appInstallSnapshotTimer(config, "/usr/local/bin/hi")
	if serverBackupsQueued() {
		t.Fatal("queued before its timer")
	}
	var out bytes.Buffer
	if err := serverBackupsEnsureTimer(&out); err != nil {
		t.Fatal(err)
	}
	service, _ := os.ReadFile(serverBackupsServicePath())
	timer, _ := os.ReadFile(serverBackupsTimerPath())
	if !strings.Contains(string(service), " server backups run --daily") || !strings.Contains(string(timer), "OnCalendar=*-*-* 03:00:00") {
		t.Fatal(string(service), string(timer))
	}
	if !slices.Contains(calls, "enable --now hi-backups.timer") || fileExists(appSnapshotTimerPath("payments")) || !serverBackupsQueued() {
		t.Fatal(calls)
	}
	if enable := slices.Index(calls, "enable --now hi-backups.timer"); enable > slices.Index(calls, "disable --now "+appSnapshotUnitName("payments")+".timer") {
		t.Fatal("an app's timer went before the queue's was on:", calls)
	}
	// Again: nothing to change.
	calls = nil
	if err := serverBackupsEnsureTimer(&out); err != nil || len(calls) != 1 {
		t.Fatal(err, calls)
	}
	// A new time rewrites it.
	calls = nil
	if err := serverBackupsCommand([]string{"--at", "02:15"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	if timer, _ := os.ReadFile(serverBackupsTimerPath()); !strings.Contains(string(timer), "OnCalendar=*-*-* 02:15:00") || !slices.Contains(calls, "restart hi-backups.timer") {
		t.Fatal(string(timer), calls)
	}
}

// The daily queue: each app that is up, its copy waited for and checked,
// then the server's state, staged without sockets, jobs, or the lock,
// encrypted with a key from the vault that goes beside it sealed.
func TestServerBackupsDailyRun(t *testing.T) {
	setupServerBackupsTest(t)
	vaultTest(t)
	t.Setenv("HI_ADMIN_DIR", filepath.Join(t.TempDir(), "admin"))
	if _, err := initVault(vaultSlotFile); err != nil {
		t.Fatal(err)
	}
	for path, text := range map[string]string{"admin.json": "{}", "jobs/1.log": "x", "sockets/s": "x"} {
		os.MkdirAll(filepath.Dir(filepath.Join(adminDir(), path)), 0o700)
		writeTestFile(t, filepath.Join(adminDir(), path), text, 0o600)
	}
	writeTestFile(t, filepath.Join(serverBackupsDir(), "people.json"), "{}", 0o600)
	writeTestFile(t, filepath.Join(serverBackupsDir(), "backups.lock"), "", 0o600)
	restic := &fakeTeamRestic{}
	restic.install(t)
	var staged []string
	inner := appRestic
	appRestic = func(env []string, dir string, args ...string) (string, error) {
		if args[0] == "backup" && slices.Contains(args, "hi-server-aiw11") {
			filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
				if err == nil && !entry.IsDir() {
					rel, _ := filepath.Rel(dir, path)
					staged = append(staged, rel)
				}
				return nil
			})
		}
		return inner(env, dir, args...)
	}
	t.Cleanup(func() { appRestic = inner })
	savedPause, savedUp := appPause, serverAppUp
	appPause = func(string) (func(), string) { return func() {}, "" }
	serverAppUp = func(string) bool { return true }
	t.Cleanup(func() { appPause, serverAppUp = savedPause, savedUp })
	service, stored := fakeS3(t, "SRVKEY")
	var out bytes.Buffer
	if err := serverBackupsCommand([]string{"--bucket", "s3:" + service.URL + "/hi5", "--name", "aiw11", "--limit-upload", "4000"}, strings.NewReader("SRVKEY\nSRVSECRET\n"), &out); err != nil {
		t.Fatal(err, out.String())
	}
	out.Reset()
	if err := serverBackupsCommand([]string{"run", "--daily"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err, out.String())
	}
	var what []string
	for _, call := range restic.calls {
		what = append(what, call[0])
	}
	t.Log(what)
	// The app: its daily snapshot, its copy (limited), then a check with a
	// part of the data read, the first time.
	copied := slices.IndexFunc(restic.calls, func(c []string) bool { return c[0] == "copy" })
	if copied < 0 || !slices.Contains(restic.calls[copied], "--limit-upload") {
		t.Fatal(restic.calls)
	}
	checks := 0
	for _, call := range restic.calls {
		if call[0] == "check" && slices.Contains(call, "--read-data-subset=1%") {
			checks++
		}
	}
	if checks != 2 {
		t.Fatal("an app's check and the server's:", restic.calls)
	}
	// The server's state.
	sort.Strings(staged)
	joined := strings.Join(staged, " ")
	if !strings.Contains(joined, "org/people.json") || !strings.Contains(joined, "vault/keys.json") || !strings.Contains(joined, "admin/admin.json") {
		t.Fatal(staged)
	}
	if strings.Contains(joined, "backups.lock") || strings.Contains(joined, "jobs/") || strings.Contains(joined, "sockets/") || strings.Contains(joined, "vault-key") || strings.Contains(joined, ".restic-key") {
		t.Fatal(staged)
	}
	if staging, _ := serverStateStaging(); fileExists(staging) {
		t.Fatal("the staging folder stayed")
	}
	if _, ok := stored["/hi5/aiw11/server/hi-key.sealed"]; !ok {
		t.Fatal("no sealed key beside the server's state")
	}
	key, _ := readSecretFile(serverStateKeySecret(serverBackupsDir()))
	if opened, app, err := appOpenSealedKey(stored["/hi5/aiw11/server/hi-key.sealed"], vaultTestWords(t)); err != nil || !bytes.Equal(opened, bytes.TrimSpace(key)) || app != "hi-server:aiw11" {
		t.Fatal(app, err)
	}
	if fileExists(serverStateKeySecret(serverBackupsDir()).disk) {
		t.Fatal("the server's snapshot key is a file, not in the vault")
	}
	state := loadServerStateStatus()
	if state.Last.IsZero() || state.Checked.IsZero() || state.Failed != "" {
		t.Fatalf("%+v", state)
	}
	// The log, newest first.
	runs := serverBackupsRuns(10)
	var log []string
	for _, run := range runs {
		log = append(log, run.What+":"+run.App)
	}
	if strings.Join(log, " ") != "check: daily: check:payments daily:payments" {
		t.Fatal(log)
	}
	// The next day's run checks nothing: a week hasn't passed.
	restic.calls = nil
	if err := serverBackupsCommand([]string{"run", "--daily"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	if slices.ContainsFunc(restic.calls, func(c []string) bool { return c[0] == "check" }) {
		t.Fatal(restic.calls)
	}
	// now --server: the server's state alone.
	restic.calls = nil
	if err := serverBackupsCommand([]string{"now", "--server"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	if slices.ContainsFunc(restic.calls, func(c []string) bool { return c[0] == "copy" }) || !slices.ContainsFunc(restic.calls, func(c []string) bool { return c[0] == "backup" && slices.Contains(c, "hi-server-aiw11") }) {
		t.Fatal(restic.calls)
	}
	// The page sees it.
	view := serverBackupsViewOf()
	if view.Server.Last.IsZero() || len(view.Runs) == 0 || view.LimitUpload != 4000 {
		t.Fatalf("%+v", view)
	}
}
