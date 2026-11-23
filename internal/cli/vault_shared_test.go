package cli

import (
	"bytes"
	"io"
	"net/http"
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

// A new app made with a shared OpenRouter key: the key must exist; the
// app gets it shared, and its agents use it, with no key of its own.
func TestAppNewWithSharedOpenRouter(t *testing.T) {
	dir := setupTeamTest(t)
	vaultTest(t)
	initVault(vaultSlotFile)
	t.Setenv("HI_APP_SLACK_BOT_TOKEN", "xoxb-bot")
	t.Setenv("HI_APP_SLACK_APP_TOKEN", "xapp-app")
	t.Setenv("HI_APP_OPENROUTER_API_KEY", "sk-or-own")
	file := filepath.Join(dir, "answers.json")
	writeSkillTestFile(t, file, `{"purpose": "x", "channel": "C07PAYMENTS", "owners": ["U07OWNER1"], "empty": true, "openrouter_from": "shared/openrouter"}`, 0o600)
	if err := appNew([]string{"payments", "--from", file}, strings.NewReader(""), io.Discard); err == nil || !strings.Contains(err.Error(), "no shared key") {
		t.Fatal(err)
	}
	vaultSharedSet("openrouter", "sk-or-shared", "ana")
	var out bytes.Buffer
	if err := appNew([]string{"payments", "--from", file}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err, out.String())
	}
	config, _ := loadTeam("payments")
	secrets, _ := loadTeamSecrets("payments")
	if config.OpenRouterFrom != "shared/openrouter" || secrets.OpenRouterAPIKey != "" {
		t.Fatal(config.OpenRouterFrom, secrets.OpenRouterAPIKey)
	}
	if grant := loadVaultShared()["openrouter"]; grant == nil || strings.Join(grant.Apps, ",") != "payments" {
		t.Fatal(grant)
	}
	if key, _ := appOpenRouterKey("payments"); key != "sk-or-shared" {
		t.Fatal(key)
	}
	// The page's wizard offers it.
	if m := adminNewMachineOf("orders"); strings.Join(m.Shared, ",") != "openrouter" {
		t.Fatal(m.Shared)
	}
}

// An app's Claude members use a shared key as their token, by
// reference: it must look like a Claude token; the app's own goes; the
// proxies' file gets the shared value and each new one; and a token of
// its own again ends the reference.
func TestAppClaudeFromShared(t *testing.T) {
	setupTeamTest(t)
	vaultTest(t)
	initVault(vaultSlotFile)
	b := newTeamTestBroker(t)
	saveTeamSecrets("payments", appSecrets{SlackBotToken: "xoxb-1", ClaudeToken: "sk-ant-own"})
	vaultSharedSet("claude", "sk-ant-oat-shared-1", "ana")
	vaultSharedSet("openrouter", "sk-or-shared", "ana")
	var out bytes.Buffer
	if err := appSignin([]string{"payments", "claude", "--from", "shared/claude"}, strings.NewReader(""), &out); err == nil || !strings.Contains(err.Error(), "isn't shared to app payments") {
		t.Fatal(err)
	}
	vaultSharedGive("claude", []string{"payments"}, false, "ana")
	vaultSharedGive("openrouter", []string{"payments"}, false, "ana")
	if err := appSignin([]string{"payments", "claude", "--from", "shared/openrouter"}, strings.NewReader(""), &out); err == nil || !strings.Contains(err.Error(), "as a Claude token") {
		t.Fatal(err)
	}
	if err := appSignin([]string{"payments", "claude", "--from", "shared/claude"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	config, _ := loadTeam("payments")
	secrets, _ := loadTeamSecrets("payments")
	if config.ClaudeFrom != "shared/claude" || secrets.ClaudeToken != "" {
		t.Fatal(config.ClaudeFrom, secrets.ClaudeToken)
	}
	file := filepath.Join(appKeysDir("payments"), appClaudeKeyFile)
	if data, _ := os.ReadFile(file); string(data) != "sk-ant-oat-shared-1\n" {
		t.Fatal(string(data))
	}
	if !appMemberSignedIn("payments:coder", "claude") {
		t.Fatal("a member on a shared token isn't signed in")
	}
	if users := appSharedUsers()["claude"]; strings.Join(users, ",") != "payments" {
		t.Fatal(users)
	}
	vaultSharedSet("claude", "sk-ant-oat-shared-2", "ana")
	b.syncSettings(io.Discard)
	if data, _ := os.ReadFile(file); string(data) != "sk-ant-oat-shared-2\n" {
		t.Fatal(string(data))
	}
	if _, err := vaultSharedRemove("claude", "ana"); err == nil {
		t.Fatal("removed a token an app's members use")
	}
	// A token of its own again ends the reference.
	if err := appSignin([]string{"payments", "claude"}, strings.NewReader("sk-ant-oat-own-2\n"), &out); err != nil {
		t.Fatal(err)
	}
	if config, _ := loadTeam("payments"); config.ClaudeFrom != "" {
		t.Fatal(config.ClaudeFrom)
	}
	if data, _ := os.ReadFile(file); string(data) != "sk-ant-oat-own-2\n" {
		t.Fatal(string(data))
	}
	if audit := vaultAuditText(t); strings.Contains(audit, "sk-ant") {
		t.Fatal("a token in the audit log")
	}
}

// A new app made with a shared Claude token gets it shared.
func TestAppNewWithSharedClaude(t *testing.T) {
	dir := setupTeamTest(t)
	vaultTest(t)
	initVault(vaultSlotFile)
	t.Setenv("HI_APP_SLACK_BOT_TOKEN", "xoxb-bot")
	t.Setenv("HI_APP_SLACK_APP_TOKEN", "xapp-app")
	t.Setenv("HI_APP_OPENROUTER_API_KEY", "sk-or-own")
	vaultSharedSet("claude", "sk-ant-oat-shared", "ana")
	file := filepath.Join(dir, "answers.json")
	writeSkillTestFile(t, file, `{"purpose": "x", "channel": "C07PAYMENTS", "owners": ["U07OWNER1"], "empty": true, "claude_from": "shared/claude"}`, 0o600)
	var out bytes.Buffer
	if err := appNew([]string{"payments", "--from", file}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err, out.String())
	}
	if config, _ := loadTeam("payments"); config.ClaudeFrom != "shared/claude" {
		t.Fatal(config.ClaudeFrom)
	}
	if grant := loadVaultShared()["claude"]; grant == nil || strings.Join(grant.Apps, ",") != "payments" {
		t.Fatal(grant)
	}
	if data, _ := os.ReadFile(filepath.Join(appKeysDir("payments"), appClaudeKeyFile)); string(data) != "sk-ant-oat-shared\n" {
		t.Fatal(string(data))
	}
}

// The page's wizard takes shared keys for the Claude token and the
// OpenRouter key, checks each is the kind it's for, and the new app's
// answers name them.
func TestAdminNewWithSharedKeys(t *testing.T) {
	a, olga := newAppTest(t)
	vaultTest(t)
	initVault(vaultSlotFile)
	vaultSharedSet("claude", "sk-ant-oat-shared", "ana")
	vaultSharedSet("openrouter", "sk-or-shared", "ana")
	var view adminDraftView
	if code := a.post(t, olga, "/api/new", map[string]any{"action": "start", "name": "orders"}, &view); code != http.StatusOK {
		t.Fatal(code)
	}
	step := func(answers map[string]any) string {
		t.Helper()
		answers["action"], answers["name"] = "step", "orders"
		var out map[string]any
		if code := a.post(t, olga, "/api/new", answers, &out); code != http.StatusOK {
			return out["error"].(string)
		}
		return ""
	}
	members := map[string]any{"lead": map[string]any{"agent": "hermes", "model": "openai/gpt-6.1-sol"}, "coder": map[string]any{"agent": "claude", "model": "claude-opus-5-5"}}
	if err := step(map[string]any{"step": "members", "members": members, "claude_from": "shared/openrouter"}); !strings.Contains(err, "as a Claude token") {
		t.Fatal(err)
	}
	if err := step(map[string]any{"step": "members", "members": members, "claude_from": "shared/claude"}); err != "" {
		t.Fatal(err)
	}
	if err := step(map[string]any{"step": "models", "openrouter_from": "shared/openrouter"}); err != "" {
		t.Fatal(err)
	}
	if code := a.get(t, olga, "/api/new/draft?name=orders", &view); code != http.StatusOK {
		t.Fatal(code)
	}
	if view.Config.ClaudeFrom != "shared/claude" || view.Config.OpenRouterFrom != "shared/openrouter" || view.HasClaude {
		t.Fatalf("%+v", view.Config)
	}
	if strings.Join(view.Machine.Shared, ",") != "claude,openrouter" {
		t.Fatal(view.Machine.Shared)
	}
}

// An app moving here that uses a shared key this machine has of its own:
// the move's check stops with the command that gives it, which takes an
// app that isn't here yet with --coming; given, the check passes.
func TestVaultSharedGiveComing(t *testing.T) {
	setupTeamTest(t)
	vaultTest(t)
	initVault(vaultSlotFile)
	vaultSharedTestApp(t, "payments")
	var out bytes.Buffer
	if err := vaultSharedCommand([]string{"set", "openrouter"}, strings.NewReader("sk-or-here\n"), &out); err != nil {
		t.Fatal(err)
	}
	vaultSharedGive("openrouter", []string{"payments"}, false, "ana")
	needs := appMoveNeeds{App: "orders", Folder: filepath.Join(t.TempDir(), "app-orders"), Version: version, Shared: []string{"openrouter"}}
	report := appReceiveCheck(needs)
	if len(report.Problems) != 1 || !strings.Contains(report.Problems[0], "hi server vault shared give openrouter orders --coming") {
		t.Fatalf("%q", report.Problems)
	}
	if err := vaultSharedCommand([]string{"give", "openrouter", "orders"}, nil, &out); err == nil || !strings.Contains(err.Error(), "--coming") {
		t.Fatal("given to an app that isn't here:", err)
	}
	if err := vaultSharedCommand([]string{"give", "openrouter", "orders", "--coming"}, nil, &out); err != nil {
		t.Fatal(err)
	}
	for _, problem := range appReceiveCheck(needs).Problems {
		if strings.Contains(problem, "shared/") {
			t.Fatal(problem)
		}
	}
	// What the sending machine says the app uses.
	config := testTeamConfig()
	config.OpenRouterFrom, config.ClaudeFrom = "shared/openrouter", "shared/claude"
	config.EnvFrom = map[string]map[string]string{"": {"MARKET_API_KEY": "shared/market", "OTHER": "shared/openrouter"}}
	if got := strings.Join(appSharedNamesOf(config), " "); got != "openrouter claude market" {
		t.Fatal(got)
	}
}

// An app on the machine's Claude sign-in, moving to a machine with none
// it can use: the check warns, with the shared key there when it has one.
func TestAppMoveWarnsOfNoClaudeThere(t *testing.T) {
	setupTeamTest(t)
	vaultTest(t)
	initVault(vaultSlotFile)
	saved := appMoveClaudeHere
	t.Cleanup(func() { appMoveClaudeHere = saved })
	needs := appMoveNeeds{App: "orders", Folder: filepath.Join(t.TempDir(), "app-orders"), Version: version, ClaudeMachine: true}
	appMoveClaudeHere = func() error { return nil }
	for _, warning := range appReceiveCheck(needs).Warnings {
		if strings.Contains(warning, "Claude") {
			t.Fatal("signed in, and warned:", warning)
		}
	}
	appMoveClaudeHere = func() error { return io.EOF }
	var out bytes.Buffer
	vaultSharedCommand([]string{"set", "claude"}, strings.NewReader("sk-ant-oat01-x\n"), &out)
	found := false
	for _, warning := range appReceiveCheck(needs).Warnings {
		found = found || strings.Contains(warning, "hi server vault shared give claude orders and hi app signin orders claude --from shared/claude")
	}
	if !found {
		t.Fatal(appReceiveCheck(needs).Warnings)
	}
}
