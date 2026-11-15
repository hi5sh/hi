package cli

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// An app takes a team store's path: each string under a variable's name
// becomes a setting, the rest left out and said why; a name it has from
// elsewhere is refused; an override is per slot.
func TestVaultConnectFrom(t *testing.T) {
	bao, said := vaultConnTestSetup(t)
	bao.put("corp/payments/prod", map[string]any{"DB_URL": "postgres://one", "API_KEY": "k-1", "bad-name": "x", "PORT": "1", "COUNT": 5})
	bao.put("corp/payments/extra", map[string]any{"DB_URL": "postgres://other", "MAIL_URL": "smtp://m"})
	var out bytes.Buffer
	if err := appEnv([]string{"payments", "--from", "corp/payments/prod"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "takes corp/payments/prod (version 1): API_KEY, DB_URL") || !strings.Contains(out.String(), "bad-name is left out: it isn't a variable name") ||
		!strings.Contains(out.String(), "PORT is left out: hi sets it") || !strings.Contains(out.String(), "COUNT is left out: its value isn't a string") {
		t.Fatal(out.String())
	}
	data, _ := os.ReadFile(filepath.Join(appDir("payments"), appConfigFile))
	if strings.Contains(string(data), "postgres://one") || !strings.Contains(string(data), `"from": "corp/payments/prod"`) {
		t.Fatal(string(data))
	}
	if values, _, err := appSlotSettings("payments", appSlotProduction); err != nil || values["DB_URL"] != "postgres://one" || values["API_KEY"] != "k-1" || values["COUNT"] != "" {
		t.Fatal(values, err)
	}
	if values, _, _ := appSlotSettings("payments", appSlotDev); values["DB_URL"] != "" {
		t.Fatal("the dev box got production's settings")
	}
	// A second source with a name the first gives: refused, then skipped.
	if err := appEnv([]string{"payments", "--from", "corp/payments/extra"}, strings.NewReader(""), io.Discard); err == nil || !strings.Contains(err.Error(), "DB_URL (from corp/payments/prod)") {
		t.Fatal(err)
	}
	if err := appEnv([]string{"payments", "--from", "corp/payments/extra", "--skip", "DB_URL"}, strings.NewReader(""), io.Discard); err != nil {
		t.Fatal(err)
	}
	// Setting a name a source gives: refused unless it overrides, per slot.
	if err := appEnv([]string{"payments", "--preview", "MAIL_URL=smtp://stage"}, strings.NewReader(""), io.Discard); err == nil || !strings.Contains(err.Error(), "MAIL_URL comes from corp/payments/extra. --override") {
		t.Fatal(err)
	}
	if err := appEnv([]string{"payments", "--preview", "MAIL_URL=smtp://stage", "--override"}, strings.NewReader(""), io.Discard); err != nil {
		t.Fatal(err)
	}
	stage, _, _ := appSlotSettings("payments", appSlotPreview)
	production, _, _ := appSlotSettings("payments", appSlotProduction)
	if stage["MAIL_URL"] != "smtp://stage" || production["MAIL_URL"] != "smtp://m" || production["DB_URL"] != "postgres://one" {
		t.Fatal(stage, production)
	}
	out.Reset()
	appEnv([]string{"payments"}, strings.NewReader(""), &out)
	if !strings.Contains(out.String(), "API_KEY, DB_URL (from corp/payments/prod)") || !strings.Contains(out.String(), "MAIL_URL overridden here (Stage); corp/payments/extra has its own") {
		t.Fatal(out.String())
	}

	// A new version reaches the app, and its channel hears the names.
	bao.put("corp/payments/prod", map[string]any{"DB_URL": "postgres://one", "API_KEY": "k-2"})
	vaultPollSources()
	if values, _, _ := appSlotSettings("payments", appSlotProduction); values["API_KEY"] != "k-2" {
		t.Fatal(values)
	}
	if len(*said) != 1 || !strings.Contains((*said)[0], "corp/payments/prod is version 2 now (changed API_KEY)") || strings.Contains((*said)[0], "k-2") {
		t.Fatal(*said)
	}
	vaultPollSources()
	if len(*said) != 1 {
		t.Fatal("an unchanged version was told", *said)
	}
	// Sealed: the copy serves, and the channel hears it once.
	bao.mu.Lock()
	bao.sealed = true
	bao.mu.Unlock()
	vaultPollSources()
	vaultPollSources()
	if values, _, err := appSlotSettings("payments", appSlotProduction); err != nil || values["API_KEY"] != "k-2" {
		t.Fatal(values, err)
	}
	away := 0
	for _, line := range *said {
		if strings.Contains(line, "didn't answer (the team's store is sealed); the app keeps corp/payments/prod version 2") {
			away++
		}
	}
	if away != 1 || len(*said) != 2 || !strings.Contains((*said)[1], "corp/payments/extra version 1") {
		t.Fatal("not one message for the outage:", *said)
	}
	bao.mu.Lock()
	bao.sealed = false
	bao.mu.Unlock()
	vaultPollSources()
	if last := (*said)[len(*said)-1]; !strings.Contains(last, "answers again; corp/payments/extra") || !strings.Contains(last, "answers again; corp/payments/prod") {
		t.Fatal(*said)
	}
	// A refusal is told as one, with what to ask for.
	bao.mu.Lock()
	bao.read = nil
	bao.mu.Unlock()
	vaultPollSources()
	if last := (*said)[len(*said)-1]; !strings.Contains(last, "refused hi's read of corp/payments/prod") || !strings.Contains(last, "ask its owners for read") {
		t.Fatal(*said)
	}
	bao.mu.Lock()
	bao.read = []string{"kv/data/corp/", "kv/metadata/corp/"}
	bao.mu.Unlock()
	vaultPollSources()

	// A later clash: the store adds a name the app sets itself; the app
	// keeps its own, and the channel hears both ways out.
	appEnv([]string{"payments", "OWN_KEY=mine"}, strings.NewReader(""), io.Discard)
	bao.put("corp/payments/prod", map[string]any{"DB_URL": "postgres://one", "API_KEY": "k-2", "OWN_KEY": "theirs"})
	vaultPollSources()
	if values, _, _ := appSlotSettings("payments", appSlotProduction); values["OWN_KEY"] != "mine" {
		t.Fatal(values)
	}
	if last := (*said)[len(*said)-1]; !strings.Contains(last, "OWN_KEY comes from the app too, which had it first and keeps it") {
		t.Fatal(*said)
	}

	// A pin holds a version.
	appEnv([]string{"payments", "--unset-from", "corp/payments/prod"}, strings.NewReader(""), io.Discard)
	if err := appEnv([]string{"payments", "--from", "corp/payments/prod?version=1"}, strings.NewReader(""), io.Discard); err != nil {
		t.Fatal(err)
	}
	bao.put("corp/payments/prod", map[string]any{"DB_URL": "postgres://later", "API_KEY": "k-4"})
	vaultPollSources()
	if values, _, _ := appSlotSettings("payments", appSlotProduction); values["API_KEY"] != "k-1" {
		t.Fatal(values)
	}
	out.Reset()
	vaultListCommand(nil, &out)
	if !strings.Contains(out.String(), "corp/payments/prod?version=1") || strings.Contains(out.String(), "k-1") || strings.Contains(out.String(), "postgres") {
		t.Fatal(out.String())
	}

	// Disconnect: refused while used.
	if err := vaultDisconnectCommand([]string{"corp"}, io.Discard); err == nil || !strings.Contains(err.Error(), "payments take settings from corp/") {
		t.Fatal(err)
	}
	appEnv([]string{"payments", "--unset-from", "corp/payments/prod?version=1"}, strings.NewReader(""), io.Discard)
	appEnv([]string{"payments", "--unset-from", "corp/payments/extra"}, strings.NewReader(""), io.Discard)
	if err := vaultDisconnectCommand([]string{"corp"}, io.Discard); err != nil {
		t.Fatal(err)
	}
	if audit := vaultAuditText(t); strings.Contains(audit, "postgres") || strings.Contains(audit, "k-1") {
		t.Fatal("a value in the audit log")
	}
}

// An AppRole login, a KV v1 mount, a namespace; and http only to this
// machine.
func TestVaultConnectAppRole(t *testing.T) {
	setupTeamTest(t)
	vaultTest(t)
	initVault(vaultSlotFile)
	vaultSharedTestApp(t, "payments")
	bao := newFakeBao(t)
	bao.roleID, bao.secretID, bao.namespace = "role-1", "secret-1", "team-a"
	bao.read = []string{"old/legacy/"}
	bao.kv1["legacy/payments"] = map[string]any{"TOKEN": "t-1"}
	if err := vaultConnectCommand([]string{"legacy", "vault://old/legacy", "--address", "http://bao.corp.example:8200"}, strings.NewReader(""), io.Discard); err == nil || !strings.Contains(err.Error(), "https://") {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := vaultConnectCommand([]string{"legacy", "vault://old/legacy", "--address", bao.server.URL, "--login", "approle", "--namespace", "team-a", "--secret-id-ttl", "7d"},
		strings.NewReader("role-1\nsecret-1\n"), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "hi logs in to it") || !strings.Contains(out.String(), "auth/approle/role/hi-server-") {
		t.Fatal(out.String())
	}
	out.Reset()
	vaultLoginCommand(nil, &out)
	if !strings.Contains(out.String(), "secret ID ends") {
		t.Fatal(out.String())
	}
	if err := appEnv([]string{"payments", "--from", "legacy/payments"}, strings.NewReader(""), io.Discard); err != nil {
		t.Fatal(err)
	}
	if values, _, _ := appSlotSettings("payments", appSlotProduction); values["TOKEN"] != "t-1" {
		t.Fatal(values)
	}
	// KV v1 has no versions: a change is found by what was read.
	bao.mu.Lock()
	bao.kv1["legacy/payments"] = map[string]any{"TOKEN": "t-2"}
	bao.mu.Unlock()
	vaultPollSources()
	if values, _, _ := appSlotSettings("payments", appSlotProduction); values["TOKEN"] != "t-2" {
		t.Fatal(values)
	}
}

// A new key pair: the old one logs in until the owners swap the public
// key; then the old one is forgotten.
func TestVaultLoginNewKey(t *testing.T) {
	bao, _ := vaultConnTestSetup(t)
	var out bytes.Buffer
	if err := vaultLoginCommand([]string{"--new-key"}, &out); err != nil || !strings.Contains(out.String(), "a new key pair") {
		t.Fatal(err, out.String())
	}
	vaultKVMu.Lock()
	vaultKVClients = map[string]*vaultKV{}
	vaultKVMu.Unlock()
	out.Reset()
	vaultLoginCommand(nil, &out)
	if !strings.Contains(out.String(), ": logs in.") {
		t.Fatal("the old key stopped before the swap:", out.String())
	}
	conns, _ := loadVaultConns()
	conn, _ := conns.find("corp")
	bao.register(vaultConnPublicPEM(conn.Seed))
	out.Reset()
	vaultLoginCommand(nil, &out)
	conns, _ = loadVaultConns()
	if conn, _ := conns.find("corp"); !strings.Contains(out.String(), "the old one is forgotten") || len(conn.OldSeed) != 0 {
		t.Fatal(out.String())
	}
}
