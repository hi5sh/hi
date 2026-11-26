package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
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
		{map[string]any{"what": "plan", "at": "02:00", "keep": "14,8,12", "apps_default": "off"}, "server backups --at 02:00 --keep 14,8,12 --apps-default off", ""},
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
}
