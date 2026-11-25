package cli

import (
	"strings"
	"testing"
	"time"
)

// A setting from a shared key, on the dev page: only one an admin shared
// to the app; the app keeps the reference, the list says it, and Remove
// drops it.
func TestDevPageSettingFromShared(t *testing.T) {
	b, _ := appTestDevPage(t)
	vaultTest(t)
	initVault(vaultSlotFile)
	saveTeamSecrets("payments", appSecrets{SlackBotToken: "xoxb-1"})
	handler := b.devPageHandler()
	vaultSharedSet("market", "mk-secret-1", "ana")
	if code, _ := appTestDevPageCall(t, handler, appTestOwnerKey, "POST", "api/settings", appDevPageSetting{Key: "MARKET_KEY", From: "shared/market"}); code == 200 {
		t.Fatal("took a shared key not shared to the app")
	}
	vaultSharedGive("market", []string{"payments"}, false, "ana")
	if code, body := appTestDevPageCall(t, handler, appTestOwnerKey, "POST", "api/settings", appDevPageSetting{Key: "MARKET_KEY", From: "shared/market"}); code != 200 {
		t.Fatal(code, body)
	}
	if config, _ := loadTeam("payments"); config.EnvFrom[""]["MARKET_KEY"] != "shared/market" {
		t.Fatal(config.EnvFrom)
	}
	view := b.devPageView(appPersonKey{User: "U07OWNER1", Name: "Olga"}, "owner")
	if !strings.Contains(strings.Join(view.Settings, "|"), "MARKET_KEY (from shared/market)") || strings.Join(view.Shared, ",") != "shared/market" {
		t.Fatal(view.Settings, view.Shared)
	}
	if code, body := appTestDevPageCall(t, handler, appTestOwnerKey, "POST", "api/settings", appDevPageSetting{Key: "MARKET_KEY", Unset: true}); code != 200 {
		t.Fatal(code, body)
	}
	if config, _ := loadTeam("payments"); config.EnvFrom[""]["MARKET_KEY"] != "" {
		t.Fatal(config.EnvFrom)
	}
}

// The admin page: an app's Settings lists references and sets one; the
// Vault tab changes a name in an app's settings path through hi app env,
// and says where every other path is changed.
func TestAdminSettingsAndVaultNames(t *testing.T) {
	setupTeamTest(t)
	vaultTest(t)
	initVault(vaultSlotFile)
	vaultSharedTestApp(t, "payments")
	vaultSharedSet("market", "mk-secret-1", "ana")
	vaultSharedGive("market", []string{"payments"}, false, "ana")
	var out strings.Builder
	if err := appEnv([]string{"payments", "MARKET_KEY", "--from", "shared/market"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	view, err := (&adminServer{log: &strings.Builder{}, asked: map[string]time.Time{}}).appSettings("payments")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range view.Env {
		found = found || e.Name == "MARKET_KEY" && e.From == "shared/market" && e.Scope == "all"
	}
	if !found || strings.Join(view.Shared, ",") != "shared/market" {
		t.Fatalf("%+v %v", view.Env, view.Shared)
	}

	owner := adminPerson{Name: "Ana", MachineOwner: true}
	if _, _, err := adminVaultChange(owner, "Ana", "path-set", "sk_live_1", "STRIPE_KEY", "hi/app/payments/production", 0, "", false, nil); err != nil {
		t.Fatal(err)
	}
	secrets, _ := loadTeamSecrets("payments")
	if secrets.EnvProduction["STRIPE_KEY"] != "sk_live_1" || secrets.Env["STRIPE_KEY"] != "" {
		t.Fatalf("%+v", secrets)
	}
	if _, _, err := adminVaultChange(owner, "Ana", "path-remove", "", "STRIPE_KEY", "hi/app/payments/production", 0, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if secrets, _ := loadTeamSecrets("payments"); secrets.EnvProduction["STRIPE_KEY"] != "" {
		t.Fatal("not removed")
	}
	if _, _, err := adminVaultChange(owner, "Ana", "path-set", "x", "SLACK_BOT_TOKEN", "hi/app/payments/hi", 0, "", false, nil); err == nil || !strings.Contains(err.Error(), "Slack") {
		t.Fatal("changed hi's own path by name:", err)
	}
	if _, ok := adminVaultSettingsPath("hi/app/nobody/shared"); ok {
		t.Fatal("an app that isn't here")
	}
	if how := adminVaultPathHow("hi/shared/market"); !strings.Contains(how, "Change value") {
		t.Fatal(how)
	}
}

// The dev page's default: a variable for the dev box, the preview, and
// production at once, listed once, and Remove takes it from all three.
func TestDevPageSettingAll(t *testing.T) {
	b, _ := appTestDevPage(t)
	vaultTest(t)
	initVault(vaultSlotFile)
	saveTeamSecrets("payments", appSecrets{SlackBotToken: "xoxb-1"})
	handler := b.devPageHandler()
	if code, body := appTestDevPageCall(t, handler, appTestOwnerKey, "POST", "api/settings", appDevPageSetting{Key: "API_URL", Value: "https://api.example.com", Slot: "all"}); code != 200 {
		t.Fatal(code, body)
	}
	secrets, _ := loadTeamSecrets("payments")
	if secrets.Env["API_URL"] != "https://api.example.com" || secrets.EnvDev["API_URL"] != "https://api.example.com" {
		t.Fatalf("%+v", secrets)
	}
	if appDevSettings("payments")["API_URL"] != "https://api.example.com" {
		t.Fatal(appDevSettings("payments"))
	}
	vaultSharedSet("market", "mk-secret-1", "ana")
	vaultSharedGive("market", []string{"payments"}, false, "ana")
	if code, body := appTestDevPageCall(t, handler, appTestOwnerKey, "POST", "api/settings", appDevPageSetting{Key: "MARKET_KEY", From: "shared/market", Slot: "all"}); code != 200 {
		t.Fatal(code, body)
	}
	view := b.devPageView(appPersonKey{User: "U07OWNER1", Name: "Olga"}, "owner")
	if got := strings.Join(view.Settings, "|"); got != "API_URL (the dev box too)|MARKET_KEY (from shared/market, the dev box too)" {
		t.Fatal(got)
	}
	for _, key := range []string{"API_URL", "MARKET_KEY"} {
		if code, body := appTestDevPageCall(t, handler, appTestOwnerKey, "POST", "api/settings", appDevPageSetting{Key: key, Unset: true}); code != 200 {
			t.Fatal(code, body)
		}
	}
	secrets, _ = loadTeamSecrets("payments")
	config, _ := loadTeam("payments")
	if len(secrets.Env)+len(secrets.EnvDev)+len(config.EnvFrom) != 0 {
		t.Fatalf("%+v %v", secrets, config.EnvFrom)
	}
}
