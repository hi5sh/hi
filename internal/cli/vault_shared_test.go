package cli

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func vaultSharedTestApp(t *testing.T, name string) {
	t.Helper()
	os.MkdirAll(appDir(name), 0o700)
	config := testTeamConfig()
	config.Name = name
	if err := saveTeam(config); err != nil {
		t.Fatal(err)
	}
	saveTeamSecrets(name, appSecrets{SlackBotToken: "xoxb-1"})
}

// A shared key: admins set it and give it; an app given it uses it by
// reference, which app.json keeps, not the value; a change reaches the
// apps; it can't be removed while used; taking it back stops it.
func TestVaultShared(t *testing.T) {
	setupTeamTest(t)
	vaultTest(t)
	initVault(vaultSlotFile)
	vaultSharedTestApp(t, "payments")
	vaultSharedTestApp(t, "orders")
	var out bytes.Buffer
	if err := vaultSharedCommand([]string{"set", "market"}, strings.NewReader("mk-1\n"), &out); err != nil {
		t.Fatal(err)
	}
	if err := appEnv([]string{"payments", "MARKET_API_KEY", "--from", "shared/market"}, strings.NewReader(""), io.Discard); err == nil || !strings.Contains(err.Error(), "isn't given to app payments") {
		t.Fatal(err)
	}
	if err := vaultSharedCommand([]string{"give", "market", "payments"}, nil, &out); err != nil {
		t.Fatal(err)
	}
	appEnv([]string{"payments", "MARKET_API_KEY=own"}, strings.NewReader(""), io.Discard)
	if err := appEnv([]string{"payments", "MARKET_API_KEY", "--from", "shared/market"}, strings.NewReader(""), io.Discard); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(appDir("payments"), appConfigFile))
	if !strings.Contains(string(data), `"MARKET_API_KEY": "shared/market"`) || strings.Contains(string(data), "mk-1") {
		t.Fatal(string(data))
	}
	if secrets, _ := loadTeamSecrets("payments"); secrets.Env["MARKET_API_KEY"] != "" {
		t.Fatal("its own value stays beside the reference")
	}
	out.Reset()
	appEnv([]string{"payments"}, strings.NewReader(""), &out)
	if !strings.Contains(out.String(), "MARKET_API_KEY (from shared/market)") {
		t.Fatal(out.String())
	}
	for slot, want := range map[string]string{appSlotProduction: "mk-1", appSlotPreview: "mk-1", appSlotDev: ""} {
		if values, _, _ := appSlotSettings("payments", slot); values["MARKET_API_KEY"] != want {
			t.Fatalf("%s: %q", slot, values["MARKET_API_KEY"])
		}
	}
	if err := appEnv([]string{"orders", "MARKET_API_KEY", "--from", "shared/market"}, strings.NewReader(""), io.Discard); err == nil {
		t.Fatal("an app not given it took it")
	}
	// A new value reaches the app.
	vaultSharedCommand([]string{"set", "market"}, strings.NewReader("mk-2\n"), &out)
	if values, _, _ := appSlotSettings("payments", appSlotProduction); values["MARKET_API_KEY"] != "mk-2" {
		t.Fatal(values["MARKET_API_KEY"])
	}
	out.Reset()
	vaultSharedCommand(nil, nil, &out)
	if !strings.Contains(out.String(), "given to payments; used by payments") || strings.Contains(out.String(), "sk-or") {
		t.Fatal(out.String())
	}
	if err := vaultSharedCommand([]string{"rm", "market"}, nil, &out); err == nil || !strings.Contains(err.Error(), "payments use") {
		t.Fatal(err)
	}

	// A move takes it along; a machine without it gets it, given to the
	// app alone.
	done, err := vaultExportShared("payments")
	if err != nil || !fileExists(appSharedExportFile("payments")) {
		t.Fatal(err)
	}
	moved, _ := os.ReadFile(appSharedExportFile("payments"))
	firstVault, firstKey := os.Getenv("HI_VAULT_DIR"), os.Getenv("HI_VAULT_KEY_DIR")
	t.Setenv("HI_VAULT_DIR", filepath.Join(t.TempDir(), "vault"))
	t.Setenv("HI_VAULT_KEY_DIR", filepath.Join(t.TempDir(), "vault-key"))
	vaultForget()
	initVault(vaultSlotFile)
	out.Reset()
	if err := vaultImportShared("payments", &out); err != nil || !strings.Contains(out.String(), "Copied shared/market") || fileExists(appSharedExportFile("payments")) {
		t.Fatal(err, out.String())
	}
	if values, _, _ := appSlotSettings("payments", appSlotProduction); values["MARKET_API_KEY"] != "mk-2" {
		t.Fatal(values["MARKET_API_KEY"])
	}
	os.WriteFile(appSharedExportFile("payments"), moved, 0o600)
	done()
	if fileExists(appSharedExportFile("payments")) {
		t.Fatal("the export stays")
	}
	t.Setenv("HI_VAULT_DIR", firstVault)
	t.Setenv("HI_VAULT_KEY_DIR", firstKey)
	vaultForget()

	// Taken back: the box no longer gets it, and then it can go.
	vaultSharedCommand([]string{"give", "market", "payments", "--take"}, nil, &out)
	if values, _, _ := appSlotSettings("payments", appSlotProduction); values["MARKET_API_KEY"] != "" {
		t.Fatal("a key taken back still reaches the app")
	}
	if err := appEnv([]string{"payments", "--unset", "MARKET_API_KEY"}, strings.NewReader(""), io.Discard); err != nil {
		t.Fatal(err)
	}
	if err := vaultSharedCommand([]string{"rm", "market"}, nil, &out); err != nil {
		t.Fatal(err)
	}
	if audit := vaultAuditText(t); strings.Contains(audit, "sk-or") {
		t.Fatal("a value in the audit log")
	}
}

// The page gives a shared key to exactly the apps ticked: several at
// once, the others taken back, but not one that uses it.
func TestVaultSharedSetApps(t *testing.T) {
	setupTeamTest(t)
	vaultTest(t)
	initVault(vaultSlotFile)
	for _, app := range []string{"payments", "orders", "billing"} {
		vaultSharedTestApp(t, app)
	}
	vaultSharedSet("market", "mk-1", "ana")
	if said, err := vaultSharedSetApps("market", []string{"payments", "orders", "billing"}, "ana"); err != nil || !strings.Contains(said, "given to payments, orders, billing") {
		t.Fatal(said, err)
	}
	if grant := loadVaultShared()["market"]; strings.Join(grant.Apps, ",") != "billing,orders,payments" {
		t.Fatal(grant.Apps)
	}
	appEnv([]string{"payments", "MARKET_API_KEY", "--from", "shared/market"}, strings.NewReader(""), io.Discard)
	if _, err := vaultSharedSetApps("market", []string{"orders"}, "ana"); err == nil || !strings.Contains(err.Error(), "payments uses shared/market") {
		t.Fatal(err)
	}
	if _, err := vaultSharedSetApps("market", []string{"payments"}, "ana"); err != nil {
		t.Fatal(err)
	}
	if grant := loadVaultShared()["market"]; strings.Join(grant.Apps, ",") != "payments" {
		t.Fatal(grant.Apps)
	}
}

// An app's agents use a shared key as their OpenRouter key, by reference:
// the app's own goes, the proxy gets the shared value and each new one,
// and taking it back falls back to this machine's.
func TestAppOpenRouterFromShared(t *testing.T) {
	setupTeamTest(t)
	vaultTest(t)
	initVault(vaultSlotFile)
	b := newTeamTestBroker(t)
	saveTeamSecrets("payments", appSecrets{SlackBotToken: "xoxb-1", OpenRouterAPIKey: "sk-or-own"})
	saved := appCheckOpenRouterKey
	t.Cleanup(func() { appCheckOpenRouterKey = saved })
	appCheckOpenRouterKey = func(string) error { return nil }
	vaultSharedSet("openrouter", "sk-or-shared-1", "ana")
	var out bytes.Buffer
	if err := appKey([]string{"payments", "openrouter", "--from", "shared/openrouter"}, strings.NewReader(""), &out); err == nil || !strings.Contains(err.Error(), "isn't shared to app payments") {
		t.Fatal(err)
	}
	vaultSharedGive("openrouter", []string{"payments"}, false, "ana")
	if err := appKey([]string{"payments", "openrouter", "--from", "shared/openrouter"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "use shared/openrouter now") || !strings.Contains(out.String(), "revoke it there") {
		t.Fatal(out.String())
	}
	config, _ := loadTeam("payments")
	secrets, _ := loadTeamSecrets("payments")
	if config.OpenRouterFrom != "shared/openrouter" || secrets.OpenRouterAPIKey != "" {
		t.Fatal(config.OpenRouterFrom, secrets.OpenRouterAPIKey)
	}
	file := filepath.Join(appKeysDir("payments"), "openrouter.env")
	if data, _ := os.ReadFile(file); string(data) != "OPENROUTER_API_KEY=sk-or-shared-1\n" {
		t.Fatal(string(data))
	}
	if key, _ := appOpenRouterKey("payments"); key != "sk-or-shared-1" {
		t.Fatal(key)
	}
	if state := appOpenRouterState("payments", secrets); state != "From shared/openrouter" {
		t.Fatal(state)
	}
	// A new value reaches the proxy with the minute's settings.
	vaultSharedSet("openrouter", "sk-or-shared-2", "ana")
	b.syncSettings(io.Discard)
	if data, _ := os.ReadFile(file); string(data) != "OPENROUTER_API_KEY=sk-or-shared-2\n" {
		t.Fatal(string(data))
	}
	// It's in use: it can't be removed, nor taken back on the page.
	if _, err := vaultSharedRemove("openrouter", "ana"); err == nil {
		t.Fatal("removed a key an app's agents use")
	}
	if _, err := vaultSharedSetApps("openrouter", nil, "ana"); err == nil {
		t.Fatal("took it back from an app that uses it")
	}
	// Back to this machine's key.
	if err := appKey([]string{"payments", "openrouter", "--unset"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	config, _ = loadTeam("payments")
	if config.OpenRouterFrom != "" || fileExists(file) {
		t.Fatal(config.OpenRouterFrom)
	}
	if audit := vaultAuditText(t); strings.Contains(audit, "sk-or") {
		t.Fatal("a key in the audit log")
	}
}
