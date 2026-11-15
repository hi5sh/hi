package cli

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

// hi's own paths moved to a team's store: copied and read back, then read
// and written there; the built-in copy stands in while it's away, until
// --forget; hi's login to it, and the recovery key, stay here.
func TestVaultMove(t *testing.T) {
	setupTeamTest(t)
	vaultTest(t)
	initVault(vaultSlotFile)
	vaultSharedTestApp(t, "payments")
	appEnv([]string{"payments", "DB_URL=postgres://one"}, strings.NewReader(""), io.Discard)
	vaultMovedForget()
	t.Cleanup(vaultMovedForget)
	bao := newFakeBao(t)
	all := []string{"kv/data/hi/", "kv/metadata/hi/", "kv/destroy/hi/"}
	bao.read, bao.write = all, all

	var out bytes.Buffer
	if err := vaultMoveCommand([]string{"vault://kv", "--address", bao.server.URL}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "can't log in") || !strings.Contains(out.String(), `"kv/data/*"     { capabilities = ["create", "read", "update", "delete", "list"] }`) ||
		!strings.Contains(out.String(), "it keeps this key") {
		t.Fatal(out.String())
	}
	conns, _ := loadVaultConns()
	bao.register(vaultConnPublicPEM(conns.Pending.Seed))
	out.Reset()
	if err := vaultMoveCommand(nil, strings.NewReader(""), &out); err != nil || !strings.Contains(out.String(), "paths are in "+bao.server.URL) {
		t.Fatal(err, out.String())
	}
	bao.mu.Lock()
	_, moved := bao.kv2["hi/app/payments/shared"]
	_, login := bao.kv2[vaultConnPath]
	_, recovery := bao.kv2[vaultRecoveryPath]
	bao.mu.Unlock()
	if !moved || login || recovery {
		t.Fatal("what moved:", moved, login, recovery)
	}
	if values, _, err := appSlotSettings("payments", appSlotProduction); err != nil || values["DB_URL"] != "postgres://one" {
		t.Fatal(values, err)
	}
	// A change goes there; its history and a rollback too.
	appEnv([]string{"payments", "DB_URL=postgres://two"}, strings.NewReader(""), io.Discard)
	bao.mu.Lock()
	versions := len(bao.kv2["hi/app/payments/shared"])
	bao.mu.Unlock()
	if versions != 2 {
		t.Fatal(versions)
	}
	store, _ := openVault()
	file, _ := store.load("hi/app/payments/shared")
	if len(file.Versions) != 2 || strings.Join(file.Versions[1].Changed, ",") != "DB_URL" {
		t.Fatalf("%+v", file.Versions)
	}
	if err := store.rollback("hi/app/payments/shared", 1, "ana"); err != nil {
		t.Fatal(err)
	}
	if values, _, _ := appSlotSettings("payments", appSlotProduction); values["DB_URL"] != "postgres://one" {
		t.Fatal(values)
	}
	if err := store.forget("hi/app/payments/shared", 2); err != nil {
		t.Fatal(err)
	}
	if err := store.forget("hi/app/payments/shared", 3); err == nil {
		t.Fatal("forgot the current version")
	}
	out.Reset()
	vaultListCommand(nil, &out)
	if !strings.Contains(out.String(), "hi/app/payments/shared") || strings.Contains(out.String(), "postgres") {
		t.Fatal(out.String())
	}
	out.Reset()
	vaultStatus(&out)
	if !strings.Contains(out.String(), "hi's paths are in "+bao.server.URL) {
		t.Fatal(out.String())
	}

	// Sealed: the built-in copy stands in.
	bao.mu.Lock()
	bao.sealed = true
	bao.mu.Unlock()
	if values, _, err := appSlotSettings("payments", appSlotProduction); err != nil || values["DB_URL"] != "postgres://one" {
		t.Fatal(values, err)
	}
	bao.mu.Lock()
	bao.sealed = false
	bao.mu.Unlock()

	// A rename moves the paths there.
	if err := vaultRenameApp("payments", "billing"); err != nil {
		t.Fatal(err)
	}
	bao.mu.Lock()
	_, old := bao.kv2["hi/app/payments/shared"]
	_, renamed := bao.kv2["hi/app/billing/shared"]
	bao.mu.Unlock()
	if old || !renamed {
		t.Fatal("rename:", old, renamed)
	}
	vaultRenameApp("billing", "payments")

	// --forget: the copy's values go, and nothing stands in.
	out.Reset()
	if err := vaultMoveCommand([]string{"--forget", "--yes"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	if data, _, ok, _ := store.getLocal("hi/app/payments/shared"); ok || data.Values["DB_URL"] != "" {
		t.Fatal("the copy stays")
	}
	if values, _, err := appSlotSettings("payments", appSlotProduction); err != nil || values["DB_URL"] != "postgres://one" {
		t.Fatal(values, err)
	}
	appEnv([]string{"payments", "DB_URL=postgres://three"}, strings.NewReader(""), io.Discard)
	if data, _, ok, _ := store.getLocal("hi/app/payments/shared"); ok || data.Values["DB_URL"] != "" {
		t.Fatal("a value written here after --forget")
	}
	bao.mu.Lock()
	bao.sealed = true
	bao.mu.Unlock()
	vaultForget()
	if _, _, err := appSlotSettings("payments", appSlotProduction); err == nil {
		t.Fatal("read with the store sealed and no copy")
	}
	bao.mu.Lock()
	bao.sealed = false
	bao.mu.Unlock()
	// A purge deletes them there.
	if n, err := vaultPurgeApp("payments"); err != nil || n == 0 {
		t.Fatal(n, err)
	}
	bao.mu.Lock()
	_, left := bao.kv2["hi/app/payments/shared"]
	bao.mu.Unlock()
	if left {
		t.Fatal("purged path stays there")
	}
}
