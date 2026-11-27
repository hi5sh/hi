package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// With restic on the machine: an app and the server's state go to a
// bucket (a folder here), a restore test brings each back and changes
// nothing, and after the machine is "lost" (its server folder, vault,
// vault key, admin settings, and app gone) hi server restore brings the
// server back with its vault, opened by the recovery key alone, and the
// app with its settings from that vault.
func TestServerRestoreRoundTrip(t *testing.T) {
	if _, err := exec.LookPath("restic"); err != nil {
		t.Skip("restic isn't installed")
	}
	setupServerBackupsTest(t)
	vaultTest(t)
	t.Setenv("HI_ADMIN_DIR", filepath.Join(t.TempDir(), "admin"))
	if _, err := initVault(vaultSlotFile); err != nil {
		t.Fatal(err)
	}
	words := vaultTestWords(t)
	os.MkdirAll(adminDir(), 0o700)
	writeTestFile(t, filepath.Join(adminDir(), "admin.json"), `{"admins":[]}`, 0o600)
	secrets, _ := loadTeamSecrets("payments")
	secrets.SlackBotToken = "xoxb-kept-in-the-vault"
	if err := saveTeamSecrets("payments", secrets); err != nil {
		t.Fatal(err)
	}
	bucket := filepath.Join(t.TempDir(), "bucket")
	if err := saveServerBackups(serverBackups{Bucket: bucket, Name: "aiw11", Claim: "old"}); err != nil {
		t.Fatal(err)
	}
	writeSecretFile(serverBucketSecret(serverBackupsDir()), []byte(`{"key_id":"","secret":""}`), 0o600)
	if err := appSnapshotRepo("payments"); err != nil {
		t.Fatal(err)
	}
	if _, err := appTakeSnapshot("payments", appSnapshotDaily, "", appNoPause); err != nil {
		t.Fatal(err)
	}
	if err := appCopyOffsite("payments", false); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := serverBackupsState(false, &out); err != nil {
		t.Fatal(err, out.String())
	}
	if !fileExists(filepath.Join(bucket, "aiw11", "apps", "payments", "config")) || !fileExists(filepath.Join(bucket, "aiw11", "server", "config")) {
		t.Fatal("not in the bucket")
	}

	// The restore test: both come back whole, and nothing is left.
	out.Reset()
	if err := serverBackupsCommand([]string{"test-restore"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err, out.String())
	}
	if !strings.Contains(out.String(), "App payments: snapshot") || !strings.Contains(out.String(), "The server's state: snapshot") {
		t.Fatal(out.String())
	}
	if status := loadTeamSnapshotStatus("payments"); status.RestoreTested.IsZero() || status.RestoreTestFailed != "" {
		t.Fatalf("%+v", status)
	}
	if loadServerStateStatus().RestoreTested.IsZero() {
		t.Fatal("the server's test isn't in its status")
	}
	if dir, _ := serverRestoreTestDir(""); fileExists(filepath.Join(dir, "payments")) || fileExists(filepath.Join(dir, "server")) {
		t.Fatal("a restore test left its folder")
	}

	// The machine is lost.
	orgDir := serverBackupsDir()
	for _, gone := range []string{orgDir, vaultDir(), os.Getenv("HI_VAULT_KEY_DIR"), adminDir(), appDir("payments"), appSnapshotsDir("payments")} {
		if err := os.RemoveAll(gone); err != nil {
			t.Fatal(err)
		}
	}
	vaultForget()
	if vaultExists() {
		t.Fatal("the vault is still there")
	}

	// A wrong recovery key opens nothing, and leaves nothing.
	apps := t.TempDir()
	if err := serverRestoreCommand([]string{"--from", bucket + "/aiw11", "--no-start", "--apps-folder", apps}, strings.NewReader("wrong words\n"), &out); err == nil {
		t.Fatal("restored with wrong words")
	}
	if fileExists(filepath.Join(orgDir, "config.json")) || vaultExists() {
		t.Fatal("a refused restore left the server's state")
	}
	out.Reset()
	if err := serverRestoreCommand([]string{"--from", bucket + "/aiw11", "--no-start", "--apps-folder", apps}, strings.NewReader(words+"\n"), &out); err != nil {
		t.Fatal(err, out.String())
	}
	t.Log(out.String())
	if !fileExists(filepath.Join(orgDir, "config.json")) || !fileExists(filepath.Join(adminDir(), "admin.json")) {
		t.Fatal("the server's folder or the admin page's settings aren't back")
	}
	vaultForget()
	if _, err := openVault(); err != nil {
		t.Fatal("the vault doesn't open by itself on this machine:", err)
	}
	if _, err := loadTeam("payments"); err != nil {
		t.Fatal("the app isn't back:", err)
	}
	if secrets, err := loadTeamSecrets("payments"); err != nil || secrets.SlackBotToken != "xoxb-kept-in-the-vault" {
		t.Fatal("the app's settings aren't back from the vault:", secrets.SlackBotToken, err)
	}
	if !fileExists(filepath.Join(apps, "app-payments", appConfigFile)) {
		t.Fatal("the app isn't in --apps-folder")
	}
	// This machine claims the folder now, and backs up there.
	b := loadServerBackups()
	var claim serverClaim
	data, _ := os.ReadFile(filepath.Join(bucket, "aiw11", serverClaimName))
	json.Unmarshal(data, &claim)
	if b.Claim == "" || b.Claim == "old" || claim.Claim != b.Claim || b.Bucket != bucket {
		t.Fatalf("%+v %+v", b, claim)
	}
	if config, _ := loadTeam("payments"); config.backups().Offsite != filepath.Join(bucket, "aiw11", "apps", "payments") {
		t.Fatal(config.backups())
	}
	// Once more is refused: this machine has a server now.
	if err := serverRestoreCommand([]string{"--from", bucket + "/aiw11", "--no-start"}, strings.NewReader(words+"\n"), &out); err == nil || !strings.Contains(err.Error(), "runs a server already") {
		t.Fatal(err)
	}
}

// A server that listened on the old machine's address listens on this
// one's; one on a peer of its own keeps none.
func TestServerRestoreListen(t *testing.T) {
	dir := t.TempDir()
	saved := hostIsHere
	hostIsHere = func(host string) bool { return host == "127.0.0.1" }
	t.Cleanup(func() { hostIsHere = saved })
	var out bytes.Buffer
	os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"listen":"127.0.0.1:7373"}`), 0o600)
	if err := serverRestoreListen(dir, &out); err != nil || out.Len() != 0 {
		t.Fatal(err, out.String())
	}
	os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"listen":"100.100.9.9:7373","peer":{"id":"abc","name":"x-server"}}`), 0o600)
	if err := serverRestoreListen(dir, &out); err != nil {
		t.Fatal(err)
	}
	if config, _ := loadServerConfig(dir); config.Listen != "" || config.Peer == nil {
		t.Fatalf("%+v", config)
	}
}
