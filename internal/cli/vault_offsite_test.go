package cli

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The snapshot key beside the offsite copy, sealed to the recovery key:
// a new recovery key seals it again; on a new machine, the recovery key
// and the bucket alone bring the app back.
func TestAppRestoreFromBucket(t *testing.T) {
	if _, err := exec.LookPath("restic"); err != nil {
		t.Skip("restic isn't installed")
	}
	setupTeamTest(t)
	vaultTest(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if _, err := initVault(vaultSlotFile); err != nil {
		t.Fatal(err)
	}
	dir := writeTeamFolderForSnapshots(t)
	bucket := filepath.Join(t.TempDir(), "bucket")
	config, _ := loadTeam("payments")
	config.Backups = &appBackups{Offsite: bucket}
	saveTeam(config)
	if _, err := appTakeSnapshot("payments", appSnapshotDaily, "", appNoPause); err != nil {
		t.Fatal(err)
	}
	if err := appCopyOffsite("payments", false); err != nil {
		t.Fatal(err)
	}
	sealed, err := os.ReadFile(filepath.Join(bucket, appSealedKeyName))
	if err != nil {
		t.Fatal(err)
	}
	key, _ := readSecretFile(appSnapshotKeySecret("payments"))
	if bytes.Contains(sealed, bytes.TrimSpace(key)) {
		t.Fatal("the key in the clear")
	}
	oldWords := vaultTestWords(t)
	if opened, app, err := appOpenSealedKey(sealed, oldWords); err != nil || !bytes.Equal(opened, key) || app != "payments" {
		t.Fatal(err)
	}
	// A new recovery key: the copy is sealed to it, and the old words
	// don't open it.
	store, _ := openVault()
	store.purge(vaultRecoveryPath)
	if err := vaultMakeRecovery(store.key, "ana"); err != nil {
		t.Fatal(err)
	}
	words := vaultTestWords(t)
	sealed, _ = os.ReadFile(filepath.Join(bucket, appSealedKeyName))
	if _, _, err := appOpenSealedKey(sealed, oldWords); err == nil {
		t.Fatal("the old words open the new copy")
	}
	var out bytes.Buffer
	appSnapshotsCommand("payments", &out)
	if !strings.Contains(out.String(), "sealed to the vault's recovery key") {
		t.Fatal(out.String())
	}

	// The machine is lost: a new one, with a vault of its own.
	os.RemoveAll(dir)
	os.Remove(filepath.Join(appsDir(), "payments"))
	os.RemoveAll(appSnapshotsDir("payments"))
	t.Setenv("HI_VAULT_DIR", filepath.Join(t.TempDir(), "vault"))
	t.Setenv("HI_VAULT_KEY_DIR", filepath.Join(t.TempDir(), "vault-key"))
	vaultForget()
	initVault(vaultSlotFile)
	target := filepath.Join(t.TempDir(), "app-payments")
	if err := appRestoreFrom("payments", bucket, "", target, "", strings.NewReader(oldWords+"\n"), io.Discard); err == nil || !strings.Contains(err.Error(), "isn't the one") {
		t.Fatal(err)
	}
	out.Reset()
	if err := appRestoreFrom("payments", bucket, "", target, "", strings.NewReader(words+"\n"), &out); err != nil {
		t.Fatal(err, out.String())
	}
	if !strings.Contains(out.String(), "set them again") {
		t.Fatal(out.String())
	}
	if _, err := loadTeam("payments"); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(filepath.Join(target, "app", "entries.json")); string(data) != "[1]" {
		t.Fatal(string(data))
	}
	if again, _ := readSecretFile(appSnapshotKeySecret("payments")); !bytes.Equal(again, key) {
		t.Fatal("the snapshot key isn't in the new vault")
	}
	if snapshots, err := appListSnapshots("payments"); err != nil || len(snapshots) != 1 {
		t.Fatal(snapshots, err)
	}
	// Once more is refused: the app is here.
	if err := appRestoreFrom("payments", bucket, "", target+"2", "", strings.NewReader(words+"\n"), io.Discard); err == nil || !strings.Contains(err.Error(), "is on this machine") {
		t.Fatal(err)
	}
}

// hi-key.sealed goes to an S3 bucket by a signed PUT, and comes back by a
// signed GET, beside the repository's path.
func TestAppBucketSigned(t *testing.T) {
	var got []string
	stored := map[string][]byte{}
	service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "AWS4-HMAC-SHA256 Credential=AKID/") || !strings.Contains(auth, "/eu-north-1/s3/aws4_request") || r.Header.Get("X-Amz-Content-Sha256") == "" {
			http.Error(w, "unsigned", http.StatusForbidden)
			return
		}
		got = append(got, r.Method+" "+r.URL.Path)
		if r.Method == "PUT" {
			stored[r.URL.Path], _ = io.ReadAll(r.Body)
			return
		}
		data, ok := stored[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write(data)
	}))
	t.Cleanup(service.Close)
	offsite := "s3:" + service.URL + "/backups/payments"
	keys := appBucket{KeyID: "AKID", Secret: "SECRET", Region: "eu-north-1"}
	if _, err := appBucketGet(offsite, keys); err == nil || !strings.Contains(err.Error(), "no hi-key.sealed") {
		t.Fatal(err)
	}
	if err := appBucketPut(offsite, keys, []byte("sealed")); err != nil {
		t.Fatal(err)
	}
	if data, err := appBucketGet(offsite, keys); err != nil || string(data) != "sealed" {
		t.Fatal(string(data), err)
	}
	if strings.Join(got, ", ") != "GET /backups/payments/hi-key.sealed, PUT /backups/payments/hi-key.sealed, GET /backups/payments/hi-key.sealed" {
		t.Fatal(got)
	}
	if err := appBucketPut(offsite, appBucket{KeyID: "WRONG", Secret: "x"}, []byte("x")); err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatal(err)
	}
}
