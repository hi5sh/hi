package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// teamSSOSecret is a file with the client secret, as standard input.
func teamSSOSecret(t *testing.T) *os.File {
	t.Helper()
	path := filepath.Join(t.TempDir(), "secret")
	os.WriteFile(path, []byte("s3cret\n"), 0o600)
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { file.Close() })
	return file
}

func TestTeamSSO(t *testing.T) {
	setupTeamTest(t)
	provider := newFakeOIDC(t)
	os.MkdirAll(teamDir("payments"), 0o700)
	config := testTeamConfig()
	if err := saveTeam(config); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	add := func(args ...string) error {
		return teamSSOCommand(append([]string{"provider", "add"}, args...), teamSSOSecret(t), &out)
	}
	// The client ID is needed when hi can't ask for it.
	if err := add("acme", provider.URL); err == nil || !strings.Contains(err.Error(), "--client-id") {
		t.Fatal(err)
	}
	if err := add("acme", provider.URL+"/", "--client-id", "hi-client"); err == nil || !strings.Contains(err.Error(), "calls itself "+provider.URL+",") {
		t.Fatal("the issuer with a slash the provider hasn't:", err)
	}
	if err := add("acme", provider.URL, "--client-id", "hi-client"); err != nil {
		t.Fatal(err)
	}
	if got := loadTeamSSOProviders()["acme"]; got.ClientSecret != "s3cret" || got.ClientID != "hi-client" {
		t.Fatalf("saved %+v", got)
	}
	if info, _ := os.Stat(teamSSOProvidersPath()); info.Mode().Perm() != 0o600 {
		t.Fatal("the providers' file is", info.Mode())
	}
	// A name that stays first.
	if err := teamSSOCommand([]string{"payments", "on", "acme", "--domain", "example.com"}, nil, &out); err == nil || !strings.Contains(err.Error(), "hi team domain payments on") {
		t.Fatal(err)
	}
	saveTeamsSettings(teamsSettings{Domain: "apps.example.com"})
	config.Domain = true
	saveTeam(config)
	if err := teamSSOCommand([]string{"payments", "on", "acme"}, nil, &out); err == nil || !strings.Contains(err.Error(), "--anyone") {
		t.Fatal("no rules:", err)
	}
	out.Reset()
	if err := teamSSOCommand([]string{"payments", "on", "acme", "--domain", "Example.com"}, nil, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "https://payments.apps.example.com/__hi/auth/callback") || !strings.Contains(out.String(), "https://payments-preview.apps.example.com/__hi/auth/callback") {
		t.Fatal(out.String())
	}
	sso, ok := loadTeamSSO("payments")
	if !ok || sso.Issuer != provider.URL || sso.ClientSecret != "s3cret" || sso.Domains[0] != "example.com" || len(sso.Key) < 32 || sso.Log != teamSSOLog("payments") {
		t.Fatalf("%+v", sso)
	}
	// The relays get the file.
	if args := strings.Join(teamExposeHelperArgs("payments", teamSlotProduction), " "); !strings.Contains(args, "--sso "+teamSSOFile("payments")) {
		t.Fatal(args)
	}
	// New rules end the sessions; a new secret doesn't.
	key := sso.Key
	teamSSOCommand([]string{"payments", "on", "acme", "--anyone"}, nil, &out)
	if sso, _ = loadTeamSSO("payments"); sso.Key == key || !sso.Anyone || len(sso.Domains) > 0 {
		t.Fatalf("%+v", sso)
	}
	key = sso.Key
	if err := add("acme", provider.URL, "--client-id", "hi-client"); err != nil || !strings.Contains(out.String(), "Teams payments") {
		t.Fatal(err, out.String())
	}
	if sso, _ = loadTeamSSO("payments"); sso.Key != key {
		t.Fatal("a new secret ended the sessions")
	}
	// The provider stays while a team uses it.
	if err := teamSSOCommand([]string{"provider", "rm", "acme"}, nil, &out); err == nil || !strings.Contains(err.Error(), "payments") {
		t.Fatal(err)
	}
	// The last sign-ins.
	line, _ := json.Marshal(netExposeSSORecord{Host: "payments.apps.example.com", Result: "refused", Email: "eve@elsewhere.com", Reason: "not on example.com"})
	os.WriteFile(teamSSOLog("payments"), append(line, '\n'), 0o600)
	out.Reset()
	teamSSOCommand([]string{"payments"}, nil, &out)
	if !strings.Contains(out.String(), "everyone acme signs in") || !strings.Contains(out.String(), "eve@elsewhere.com  payments.apps.example.com: not on example.com") {
		t.Fatal(out.String())
	}
	out.Reset()
	teamSSOCommand(nil, nil, &out)
	if !strings.Contains(out.String(), "acme: "+provider.URL+"\n  for payments (everyone acme signs in)") {
		t.Fatal(out.String())
	}
	if err := teamSSOCommand([]string{"payments", "off"}, nil, &out); err != nil || fileExists(teamSSOFile("payments")) {
		t.Fatal(err)
	}
	if err := teamSSOCommand([]string{"provider", "rm", "acme"}, nil, &out); err != nil || len(loadTeamSSOProviders()) != 0 {
		t.Fatal(err)
	}
}

func TestTeamSSOProviderRules(t *testing.T) {
	setupTeamTest(t)
	os.MkdirAll(teamDir("payments"), 0o700)
	config := testTeamConfig()
	config.Domain = true
	saveTeam(config)
	saveTeamsSettings(teamsSettings{Domain: "apps.example.com"})
	saveTeamSSOProviders(map[string]teamSSOProvider{
		"google": {Issuer: "https://accounts.google.com", ClientID: "id", ClientSecret: "s"},
		"entra":  {Issuer: "https://login.microsoftonline.com/0000-tenant/v2.0", ClientID: "id", ClientSecret: "s"},
	})
	var out strings.Builder
	for _, refused := range [][]string{
		{"payments", "on", "google", "--anyone"},
		{"payments", "on", "google", "--group", "staff"},
		{"payments", "on", "entra", "--domain", "example.com"},
		{"payments", "on", "entra", "--anyone", "--group", "staff"},
	} {
		if err := teamSSOCommand(refused, nil, &out); err == nil {
			t.Fatal("allowed:", refused)
		}
	}
	for _, allowed := range [][]string{
		{"payments", "on", "google", "--domain", "example.com,example.org"},
		{"payments", "on", "entra", "--anyone"},
	} {
		if err := teamSSOCommand(allowed, nil, &out); err != nil {
			t.Fatal(allowed, err)
		}
	}
}

func TestTeamSSODiscover(t *testing.T) {
	if err := teamSSODiscover("https://login.microsoftonline.com/common/v2.0"); err == nil || !strings.Contains(err.Error(), "<tenant-id>") {
		t.Fatal(err)
	}
	if err := teamSSODiscover("http://login.example.com"); err == nil || !strings.Contains(err.Error(), "https") {
		t.Fatal(err)
	}
	// GitHub: no discovery document.
	none := httptest.NewServer(http.NotFoundHandler())
	defer none.Close()
	if err := teamSSODiscover(none.URL); err == nil || !strings.Contains(err.Error(), "OIDC providers only") {
		t.Fatal(err)
	}
	// No PKCE.
	var noPKCE *httptest.Server
	noPKCE = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"issuer": noPKCE.URL, "authorization_endpoint": "a", "token_endpoint": "t", "jwks_uri": "k",
			"code_challenge_methods_supported": []string{"plain"}})
	}))
	defer noPKCE.Close()
	if err := teamSSODiscover(noPKCE.URL); err == nil || !strings.Contains(err.Error(), "S256") {
		t.Fatal(err)
	}
}
