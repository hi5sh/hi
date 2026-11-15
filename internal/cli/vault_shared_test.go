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
