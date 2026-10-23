package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// testChatGPTToken is a token shaped as ChatGPT's: what hi reads is in its
// claims, and nothing checks the signature.
func testChatGPTToken(expires time.Time, account, plan string) string {
	claims, _ := json.Marshal(map[string]any{"exp": expires.Unix(), "https://api.openai.com/auth": map[string]string{
		"chatgpt_account_id": account, "chatgpt_plan_type": plan}})
	return "e30." + base64.RawURLEncoding.EncodeToString(claims) + ".sig"
}

// fakeChatGPTChoices answers the wizard's questions about ChatGPT without
// the network: a sign-in, the plan's models, and OpenRouter's.
func fakeChatGPTChoices(t *testing.T, onOpenRouter map[string]bool) *int {
	t.Helper()
	signIns := 0
	signIn, models, has := teamChatGPTSignIn, chatGPTModels, teamOpenRouterHasModel
	teamChatGPTSignIn = func(stdout io.Writer) (teamChatGPTGrant, error) {
		signIns++
		return teamChatGPTGrant{AccessToken: testChatGPTToken(time.Now().Add(240*time.Hour), "acct-1", "team"), RefreshToken: "rt-1",
			AccountID: "acct-1", SignedIn: time.Now().UTC()}, nil
	}
	chatGPTModels = func(teamChatGPTGrant) ([]string, error) {
		return []string{"gpt-6.1-sol", "gpt-6-luna", "gpt-5.3-codex-spark"}, nil
	}
	teamOpenRouterHasModel = func(model string) (bool, error) { return onOpenRouter[model], nil }
	t.Cleanup(func() { teamChatGPTSignIn, chatGPTModels, teamOpenRouterHasModel = signIn, models, has })
	return &signIns
}

func TestTeamWizardPutsTheLeadOnChatGPT(t *testing.T) {
	setupTeamTest(t)
	saved := teamCheckOpenRouterKey
	teamCheckOpenRouterKey = func(string) error { return nil }
	t.Cleanup(func() { teamCheckOpenRouterKey = saved })
	signIns := fakeChatGPTChoices(t, map[string]bool{"openai/gpt-6.1-sol": true, "anthropic/claude-opus-5.5": true, "openai/gpt-5.5": true})
	answers := strings.Join([]string{
		"Track supplier payments for the finance admins",
		"xoxb-bot", "xapp-app", "C07PAYMENTS", "U07OWNER1", "",
		"", // the lead: Hermes, as offered
		"chatgpt",
		"",                              // the team's own sign-in
		"",                              // the plan's first model
		"",                              // its effort
		"", "", "", "codex:gpt-5.5", "", // the coder, its Claude sign-in and effort as offered, a reviewer model
		"", // this machine's OpenRouter key
		"", "",
	}, "\n") + "\n"
	var out bytes.Buffer
	if err := teamNew([]string{"payments"}, strings.NewReader(answers), &out); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	config, _ := loadTeam("payments")
	lead := config.Members["lead"]
	if lead.Agent != "hermes" || lead.Provider != "chatgpt" || lead.Model != "gpt-6.1-sol" || lead.Fallback != "" ||
		teamLeadOpenRouterModel(config) != "openai/gpt-6.1-sol" {
		t.Fatalf("%+v", lead)
	}
	secrets, _ := loadTeamSecrets("payments")
	if *signIns != 1 || secrets.ChatGPT == nil || secrets.ChatGPT.RefreshToken != "rt-1" {
		t.Fatalf("%d sign-ins, %+v", *signIns, secrets.ChatGPT)
	}
	text := out.String()
	for _, want := range []string{"Signed in: ChatGPT Team plan.", "The plan offers gpt-6.1-sol, gpt-6-luna",
		"Fallback: openai/gpt-6.1-sol on OpenRouter.", "OpenRouter is what the roles on ChatGPT fall back on",
		"Hermes, gpt-6.1-sol on ChatGPT, falling back to openai/gpt-6.1-sol on OpenRouter"} {
		if !strings.Contains(text, want) {
			t.Errorf("no %q in:\n%s", want, text)
		}
	}
}

func TestTeamWizardAsksAFallbackOpenRouterLacks(t *testing.T) {
	setupTeamTest(t)
	fakeChatGPTChoices(t, map[string]bool{"openai/gpt-6.1-sol": true})
	member := teamMember{Agent: "hermes"}
	var secrets teamSecrets
	signIn := ""
	if err := teamAskProvider(strings.NewReader("bedrock\n"), io.Discard, &member, &secrets, &signIn); err == nil || !strings.Contains(err.Error(), `not "bedrock"`) {
		t.Fatal(err)
	}
	answers := "chatgpt\n\ngpt-5.3-codex-spark\nopenai/gpt-6-spark\nopenai/gpt-6.1-sol\n"
	var out bytes.Buffer
	if err := teamAskProvider(strings.NewReader(answers), &out, &member, &secrets, &signIn); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if member.Model != "gpt-5.3-codex-spark" || member.Fallback != "openai/gpt-6.1-sol" || !strings.Contains(out.String(), "OpenRouter has no openai/gpt-5.3-codex-spark") ||
		!strings.Contains(out.String(), "OpenRouter has no openai/gpt-6-spark") {
		t.Fatalf("%+v\n%s", member, out.String())
	}
	// A second role on ChatGPT uses the same sign-in, without asking.
	second := teamMember{Agent: "hermes"}
	out.Reset()
	if err := teamAskProvider(strings.NewReader("chatgpt\n\n"), &out, &second, &secrets, &signIn); err != nil || strings.Contains(out.String(), "sign-in [team]") {
		t.Fatalf("%v\n%s", err, out.String())
	}
}

func TestValidateTeamProviders(t *testing.T) {
	setupTeamTest(t)
	ok := testTeamConfig()
	ok.Members["lead"] = teamMember{Agent: "hermes", Provider: "chatgpt", Model: "gpt-6.1-sol", Fallback: "openai/gpt-6.1"}
	if err := validateTeam(ok); err != nil {
		t.Fatal(err)
	}
	cases := map[string]teamMember{
		"openrouter, chatgpt, or claude, not":   {Agent: "hermes", Provider: "bedrock"},
		"a Claude plan is for Claude Code":      {Agent: "hermes", Provider: "claude"},
		"a ChatGPT plan is for Hermes or Codex": {Agent: "claude", Provider: "chatgpt", Model: "opus"},
		"name the ChatGPT model":                {Agent: "hermes", Provider: "chatgpt"},
		"a fallback is for a member":            {Agent: "hermes", Fallback: "openai/gpt-6"},
		"isn't a model name":                    {Agent: "hermes", Provider: "chatgpt", Model: "gpt-6", Fallback: "no spaces please"},
	}
	for want, member := range cases {
		config := testTeamConfig()
		role := "lead"
		if member.Agent != "hermes" {
			role = "reviewer"
		}
		config.Members[role] = member
		if err := validateTeam(config); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v", want, err)
		}
	}
}

func TestTeamLeadModelSettings(t *testing.T) {
	setupTeamTest(t)
	config := testTeamConfig()
	openRouter := teamLeadModelSettings(config, "10.234.5.2")
	if !strings.Contains(openRouter, "  default: qwen/qwen3.8-max\n  provider: custom\n  base_url: http://10.234.5.2:") || strings.Contains(openRouter, "fallback") {
		t.Fatal(openRouter)
	}
	config.Members["lead"] = teamMember{Agent: "hermes", Provider: "chatgpt", Model: "gpt-6.1-sol"}
	chatGPT := teamLeadModelSettings(config, "10.234.5.2")
	for _, want := range []string{"  default: gpt-6.1-sol\n  provider: custom:hi-chatgpt\n", "    api: http://10.234.5.2:3128/backend-api/codex\n",
		"    transport: codex_responses\n    discover_models: false\n", "  hi-openrouter:\n    api: http://10.234.5.2:3128/api/v1\n",
		"fallback_providers:\n  - provider: custom:hi-openrouter\n    model: openai/gpt-6.1-sol\n    base_url: http://10.234.5.2:3128/api/v1\n    api_key: " + boxClaudePlacehold + "\n    api_mode: chat_completions\n", "    api_key: " + boxClaudePlacehold + "\n"} {
		want = strings.ReplaceAll(want, ":3128", ":"+strconv.Itoa(boxInjectPort))
		if !strings.Contains(chatGPT, want) {
			t.Errorf("no %q in\n%s", want, chatGPT)
		}
	}
	// The lead's whole config: Hermes' own fallback notices are off.
	dir := teamDir("payments")
	os.MkdirAll(filepath.Join(dir, "lead"), 0o700)
	if err := writeTeamLead(config, teamSecrets{}, "10.234.5.2", ""); err != nil {
		t.Fatal(err)
	}
	if settings := readTestFile(t, filepath.Join(dir, "lead", "config.yaml")); !strings.Contains(settings, "  live_status: verb\n  suppress_warning_notifications: true\n") {
		t.Fatal(settings)
	}
}

func TestBoxChatGPTInjector(t *testing.T) {
	var got http.Header
	var gotPath string
	status, body := http.StatusOK, `{"ok":true}`
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, gotPath = r.Header.Clone(), r.URL.Path
		w.WriteHeader(status)
		io.WriteString(w, body)
	}))
	defer upstream.Close()
	dir := t.TempDir()
	tokenFile := filepath.Join(dir, "chatgpt.json")
	token := testChatGPTToken(time.Now().Add(time.Hour), "acct-claims", "team")
	os.WriteFile(tokenFile, []byte(`{"access_token":"`+token+`","account_id":"acct-file"}`), 0o600)
	var logged bytes.Buffer
	log := &boxNetworkLog{file: &logged}
	openRouter := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "openrouter "+r.URL.Path) })
	proxy := httptest.NewServer(boxRouteChatGPT(newBoxChatGPTInjector(tokenFile, upstream.URL, "hermes-agent", log), openRouter))
	defer proxy.Close()
	call := func(path string) string {
		request, _ := http.NewRequest("POST", proxy.URL+path, strings.NewReader(`{"model":"gpt-6.1-sol"}`))
		request.Header.Set("Authorization", "Bearer "+boxClaudePlacehold)
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		data, _ := io.ReadAll(response.Body)
		return string(data)
	}
	if answer := call("/backend-api/codex/responses"); answer != `{"ok":true}` || gotPath != "/backend-api/codex/responses" ||
		got.Get("Authorization") != "Bearer "+token || got.Get("ChatGPT-Account-ID") != "acct-file" || got.Get("originator") != "hermes-agent" {
		t.Fatalf("%s %s %v", answer, gotPath, got)
	}
	if answer := call("/api/v1/chat/completions"); answer != "openrouter /api/v1/chat/completions" {
		t.Fatal(answer)
	}
	for _, refusal := range []struct {
		status int
		body   string
		logged string
	}{
		{http.StatusUnauthorized, `{"detail":"token expired"}`, "chatgpt-refused 401:signin"},
		{http.StatusTooManyRequests, `{"error":{"type":"usage_limit_reached","resets_at":1792800000}}`, "chatgpt-refused 429:limit"},
		{http.StatusTooManyRequests, `{"error":{"type":"rate_limit_exceeded"}}`, "chatgpt-refused 429:other"},
	} {
		status, body = refusal.status, refusal.body
		logged.Reset()
		// The body still reaches Hermes, which decides what to do.
		if answer := call("/backend-api/codex/responses"); answer != refusal.body || !strings.Contains(logged.String(), refusal.logged) {
			t.Errorf("%d: %q, logged %q", refusal.status, answer, logged.String())
		}
	}
	// Without the token file the placeholder goes on, and ChatGPT refuses.
	os.Remove(tokenFile)
	status, body = http.StatusOK, "{}"
	logged.Reset()
	call("/backend-api/codex/responses")
	if got.Get("Authorization") != "Bearer "+boxClaudePlacehold || !strings.Contains(logged.String(), "chatgpt-sign-in-problem") {
		t.Fatal(got, logged.String())
	}
}

// fakeChatGPTIssuer is OpenAI's sign-in service: a device code that is
// signed in on the second poll, and refreshes that rotate the refresh
// token, or refuse it once refuse is set.
type fakeChatGPTIssuer struct {
	mu        sync.Mutex
	polls     int
	refreshes []string
	refuse    bool
}

func (f *fakeChatGPTIssuer) start(t *testing.T) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		switch r.URL.Path {
		case "/api/accounts/deviceauth/usercode":
			io.WriteString(w, `{"device_auth_id":"dev-1","user_code":"ABCD-1234","interval":"0"}`)
		case "/api/accounts/deviceauth/token":
			if f.polls++; f.polls < 2 {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			io.WriteString(w, `{"authorization_code":"code-1","code_verifier":"verifier-1"}`)
		case "/oauth/token":
			r.ParseForm()
			form := r.PostForm
			if form.Get("grant_type") == "refresh_token" {
				f.refreshes = append(f.refreshes, form.Get("refresh_token"))
				if f.refuse {
					w.WriteHeader(http.StatusBadRequest)
					io.WriteString(w, `{"error":{"message":"refresh_token_reused"}}`)
					return
				}
			} else if form.Get("code") != "code-1" || form.Get("code_verifier") != "verifier-1" || !strings.HasSuffix(form.Get("redirect_uri"), "/deviceauth/callback") {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			next := "rt-" + url.QueryEscape(form.Get("grant_type")) + "-" + strconv.Itoa(len(f.refreshes))
			json.NewEncoder(w).Encode(map[string]string{"access_token": testChatGPTToken(time.Now().Add(240*time.Hour), "acct-1", "pro"),
				"id_token": testChatGPTToken(time.Now().Add(time.Hour), "acct-1", "pro"), "refresh_token": next})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	saved := chatGPTIssuer
	chatGPTIssuer = server.URL
	t.Cleanup(func() { chatGPTIssuer = saved })
}

func TestChatGPTDeviceSignIn(t *testing.T) {
	issuer := &fakeChatGPTIssuer{}
	issuer.start(t)
	device, err := chatGPTDeviceStart()
	if err != nil || device.Code != "ABCD-1234" {
		t.Fatal(device, err)
	}
	device.Interval = time.Millisecond
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	grant, err := chatGPTDeviceWait(device, func() time.Time { return now })
	if err != nil || grant.RefreshToken != "rt-authorization_code-0" || grant.AccountID != "acct-1" || !grant.SignedIn.Equal(now) ||
		chatGPTPlanName(grant) != "ChatGPT Pro plan" || issuer.polls != 2 {
		t.Fatalf("%+v %v", grant, err)
	}
}

func TestPrepareTeamChatGPT(t *testing.T) {
	setupTeamTest(t)
	newTeamTestBroker(t)
	issuer := &fakeChatGPTIssuer{}
	issuer.start(t)
	now := time.Now().UTC()
	fresh := teamChatGPTGrant{AccessToken: testChatGPTToken(now.Add(9*24*time.Hour), "acct-1", "team"), RefreshToken: "rt-0", SignedIn: now}
	saveTeamSecrets("payments", teamSecrets{SlackBotToken: "xoxb", ChatGPT: &fresh})
	key := filepath.Join(teamKeysDir("payments"), teamChatGPTKeyFile)
	readKey := func() map[string]string {
		var file map[string]string
		data, err := os.ReadFile(key)
		if err != nil {
			return nil
		}
		json.Unmarshal(data, &file)
		return file
	}
	// Nine days left: no refresh; the proxy gets the token.
	if err := prepareTeamChatGPT("payments", now); err != nil || len(issuer.refreshes) != 0 || readKey()["access_token"] != fresh.AccessToken {
		t.Fatal(err, issuer.refreshes, readKey())
	}
	// Two days left: refreshed, once, and the new refresh token kept.
	soon := fresh
	soon.AccessToken = testChatGPTToken(now.Add(48*time.Hour), "acct-1", "team")
	saveTeamSecrets("payments", teamSecrets{SlackBotToken: "xoxb", ChatGPT: &soon})
	if err := prepareTeamChatGPT("payments", now); err != nil {
		t.Fatal(err)
	}
	secrets, _ := loadTeamSecrets("payments")
	if len(issuer.refreshes) != 1 || issuer.refreshes[0] != "rt-0" || secrets.ChatGPT.RefreshToken != "rt-refresh_token-1" ||
		secrets.SlackBotToken != "xoxb" || readKey()["access_token"] != secrets.ChatGPT.AccessToken || readKey()["account_id"] != "acct-1" {
		t.Fatalf("%v %+v %v", issuer.refreshes, secrets.ChatGPT, readKey())
	}
	// OpenAI refuses a refresh: signed out, and the proxy has no token, so
	// the lead falls back rather than calling with one that will lapse.
	expiring := *secrets.ChatGPT
	expiring.AccessToken = testChatGPTToken(now.Add(time.Hour), "acct-1", "team")
	saveTeamSecrets("payments", teamSecrets{ChatGPT: &expiring})
	issuer.refuse = true
	prepareTeamChatGPT("payments", now)
	secrets, _ = loadTeamSecrets("payments")
	if secrets.ChatGPT.Failed == "" || readKey() != nil {
		t.Fatalf("%+v %v", secrets.ChatGPT, readKey())
	}
	// And it isn't tried again until someone signs in.
	prepareTeamChatGPT("payments", now)
	if len(issuer.refreshes) != 2 {
		t.Fatal(issuer.refreshes)
	}
}

func TestPrepareTeamChatGPTUsesThisMachinesWithoutRefreshing(t *testing.T) {
	dir := setupTeamTest(t)
	newTeamTestBroker(t)
	issuer := &fakeChatGPTIssuer{}
	issuer.start(t)
	t.Setenv("CODEX_HOME", filepath.Join(dir, "codex"))
	token := testChatGPTToken(time.Now().Add(time.Hour), "acct-9", "plus")
	writeSkillTestFile(t, filepath.Join(dir, "codex", "auth.json"), `{"auth_mode":"chatgpt","tokens":{"access_token":"`+token+`","refresh_token":"rt-codex"}}`, 0o600)
	saveTeamSecrets("payments", teamSecrets{SlackBotToken: "xoxb"})
	if err := prepareTeamChatGPT("payments", time.Now()); err != nil {
		t.Fatal(err)
	}
	if data := readTestFile(t, filepath.Join(teamKeysDir("payments"), teamChatGPTKeyFile)); !strings.Contains(data, token) || !strings.Contains(data, "acct-9") || len(issuer.refreshes) != 0 {
		t.Fatal(data, issuer.refreshes)
	}
}

func TestTeamTellsTheChannelOncePerRole(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	channel := &fakeTeamChannel{}
	b.channel = channel
	config := testTeamConfig()
	config.Members["lead"] = teamMember{Agent: "hermes", Provider: "chatgpt", Model: "gpt-6.1-sol"}
	saveTeam(config)
	now := time.Now().UTC()
	grant := teamChatGPTGrant{AccessToken: testChatGPTToken(now.Add(240*time.Hour), "acct-1", "team"), RefreshToken: "rt", SignedIn: now.Add(-time.Hour)}
	saveTeamSecrets("payments", teamSecrets{ChatGPT: &grant})
	logPath := teamNetworkLogPath("payments")
	os.MkdirAll(filepath.Dir(logPath), 0o700)
	appendLog := func(lines ...string) {
		file, _ := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		for _, line := range lines {
			file.WriteString(now.Format(time.RFC3339) + " " + line + "\n")
		}
		file.Close()
	}
	posts := func() []string {
		var out []string
		for _, call := range channel.calls {
			if strings.HasPrefix(call, "post ") {
				out = append(out, call)
			}
		}
		return out
	}
	// The first look only notes the sign-in; a short rate limit is no
	// reason to tell anyone.
	appendLog("chatgpt /backend-api/codex/responses", "chatgpt-refused 429:other")
	b.tellSubscriptions(config, now)
	if len(posts()) != 0 {
		t.Fatal(posts())
	}
	appendLog("chatgpt-refused 401:signin", "openrouter /api/v1/chat/completions")
	b.tellSubscriptions(config, now)
	if got := posts(); len(got) != 1 || !strings.Contains(got[0], "The lead's ChatGPT sign-in doesn't work, so it uses OpenRouter (openai/gpt-6.1-sol) now") ||
		!strings.Contains(got[0], "`/hi-payments signin chatgpt`") {
		t.Fatal(got)
	}
	// Failing again, and back and over again, is not told again.
	appendLog("chatgpt-refused 401:signin", "chatgpt-refused 429:limit")
	b.tellSubscriptions(config, now)
	if len(posts()) != 1 {
		t.Fatal(posts())
	}
	var status bytes.Buffer
	teamStatus("payments", &status)
	if !strings.Contains(status.String(), "On OpenRouter since their subscription refused them: the lead (/hi-payments signin") {
		t.Fatal(status.String())
	}
	// Signing in again says so, and a later failure is told once more.
	grant.SignedIn = now
	saveTeamSecrets("payments", teamSecrets{ChatGPT: &grant})
	b.tellSubscriptions(config, now)
	if got := posts(); len(got) != 2 || !strings.Contains(got[1], "ChatGPT (ChatGPT Team plan) is signed in again; the lead is back on it.") {
		t.Fatal(got)
	}
	appendLog("chatgpt-refused 429:limit")
	b.tellSubscriptions(config, now)
	if got := posts(); len(got) != 3 || !strings.Contains(got[2], "plan reached its usage limit") || !strings.Contains(got[2], "when the plan's limit resets") {
		t.Fatal(got)
	}
}

func TestTeamFallbackMessageWithoutAKey(t *testing.T) {
	dir := setupTeamTest(t)
	newTeamTestBroker(t)
	os.WriteFile(filepath.Join(dir, "hermes", ".env"), nil, 0o600)
	config := testTeamConfig()
	config.Members["lead"] = teamMember{Agent: "hermes", Provider: "chatgpt", Model: "gpt-6.1-sol", Fallback: "openai/gpt-6.1"}
	if text := teamFallbackMessage(config, teamSecrets{}, "lead", teamRefusal{provider: "chatgpt", reason: "expired"}); !strings.Contains(text, "no OpenRouter key to fall back on") || !strings.Contains(text, "codex login") || !strings.Contains(text, "/hi-payments signin chatgpt") {
		t.Fatal(text)
	}
}

func TestTeamMemberCommand(t *testing.T) {
	setupTeamTest(t)
	newTeamTestBroker(t)
	fakeChatGPTChoices(t, map[string]bool{"openai/gpt-6-luna": true})
	grant := teamChatGPTGrant{AccessToken: testChatGPTToken(time.Now().Add(240*time.Hour), "acct-1", "team"), RefreshToken: "rt", SignedIn: time.Now()}
	saveTeamSecrets("payments", teamSecrets{ChatGPT: &grant})
	var out bytes.Buffer
	if err := teamMemberCommand([]string{"payments", "lead", "--provider", "chatgpt", "--model", "gpt-7"}, nil, &out); err == nil || !strings.Contains(err.Error(), "doesn't offer \"gpt-7\"") {
		t.Fatal(err)
	}
	if err := teamMemberCommand([]string{"payments", "lead", "--provider", "chatgpt", "--model", "gpt-6.1-sol"}, nil, &out); err == nil || !strings.Contains(err.Error(), "--fallback") {
		t.Fatal(err)
	}
	if err := teamMemberCommand([]string{"payments", "lead", "--provider", "chatgpt", "--model", "gpt-6-luna"}, nil, &out); err != nil {
		t.Fatal(err)
	}
	config, _ := loadTeam("payments")
	if lead := config.Members["lead"]; lead.Provider != "chatgpt" || lead.Model != "gpt-6-luna" || lead.Fallback != "openai/gpt-6-luna" ||
		!strings.Contains(out.String(), "uses it from its next start") {
		t.Fatalf("%+v\n%s", lead, out.String())
	}
	// Back on OpenRouter: the ChatGPT model and fallback go.
	out.Reset()
	if err := teamMemberCommand([]string{"payments", "lead", "--provider", "openrouter", "--model", "qwen/qwen3.8-max"}, nil, &out); err != nil {
		t.Fatal(err)
	}
	config, _ = loadTeam("payments")
	if lead := config.Members["lead"]; lead.Provider != "" || lead.Fallback != "" || lead.Model != "qwen/qwen3.8-max" {
		t.Fatalf("%+v", lead)
	}
}

func TestTeamNewFromFileTakesAChatGPTSignIn(t *testing.T) {
	dir := setupTeamTest(t)
	t.Setenv("HI_TEAM_SLACK_BOT_TOKEN", "xoxb-bot")
	t.Setenv("HI_TEAM_SLACK_APP_TOKEN", "xapp-app")
	t.Setenv("CODEX_HOME", filepath.Join(dir, "no-codex"))
	answers := filepath.Join(dir, "answers.json")
	writeSkillTestFile(t, answers, `{"purpose":"p","channel":"C07PAYMENTS","owners":["U07OWNER1"],"members":{"lead":{"agent":"hermes","provider":"chatgpt","model":"gpt-6.1-sol"},"coder":{"agent":"claude"}}}`, 0o600)
	if _, _, err := teamAnswersFromFile("payments", answers); err == nil || !strings.Contains(err.Error(), "HI_TEAM_CHATGPT_AUTH") {
		t.Fatal(err)
	}
	auth := filepath.Join(dir, "auth.json")
	writeSkillTestFile(t, auth, `{"auth_mode":"chatgpt","tokens":{"access_token":"`+testChatGPTToken(time.Now().Add(time.Hour), "acct-2", "team")+`","refresh_token":"rt-file"}}`, 0o600)
	t.Setenv("HI_TEAM_CHATGPT_AUTH", auth)
	_, secrets, err := teamAnswersFromFile("payments", answers)
	if err != nil || secrets.ChatGPT == nil || secrets.ChatGPT.RefreshToken != "rt-file" || secrets.ChatGPT.AccountID != "acct-2" {
		t.Fatalf("%v %+v", err, secrets.ChatGPT)
	}
}

func TestTeamMemberProxyArgs(t *testing.T) {
	setupTeamTest(t)
	newTeamTestBroker(t)
	config := testTeamConfig()
	config.Members["coder"] = teamMember{Agent: "claude", Model: "opus", Fallback: "anthropic/claude-opus-5"}
	saveTeam(config)
	args, secret, err := teamMemberProxyArgs(boxMeta{Name: "payments-t-7", Agent: "claude", Team: "payments:coder"}, nil, "/secrets/claude")
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	for _, want := range []string{teamKeysDir("payments") + ":/secrets/team:ro", teamEventsDir("payments") + ":/events",
		boxFallbackEnv + "=/secrets/team/openrouter.env,/secrets/hermes.env", boxRoleEnv + "=coder",
		boxEventsEnv + "=/events/payments-t-7.log", boxFallbackModelEnv + "=anthropic/claude-opus-5"} {
		if !strings.Contains(joined, want) {
			t.Errorf("no %q in %s", want, joined)
		}
	}
	if secret != "/secrets/team/claude-token,/secrets/claude" {
		t.Fatal(secret)
	}
	// Signed in nowhere: refused before a box is made.
	if _, _, err := teamMemberProxyArgs(boxMeta{Name: "b", Agent: "claude", Team: "payments:coder"}, nil, ""); err == nil || !strings.Contains(err.Error(), "hi team signin payments claude") {
		t.Fatal(err)
	}
	// With the team's own token, this machine's isn't needed.
	saveTeamSecrets("payments", teamSecrets{ClaudeToken: "sk-ant-oat-team"})
	writeTeamKeys("payments", teamSecrets{ClaudeToken: "sk-ant-oat-team"})
	if _, secret, err := teamMemberProxyArgs(boxMeta{Name: "b", Agent: "claude", Team: "payments:coder"}, nil, ""); err != nil || secret != "/secrets/team/claude-token" ||
		!teamMemberSignedIn("payments:coder", "claude") || teamMemberSignedIn("payments:reviewer", "codex") {
		t.Fatal(secret, err)
	}
}

func TestTeamSigninClaude(t *testing.T) {
	setupTeamTest(t)
	newTeamTestBroker(t)
	saveTeamSecrets("payments", teamSecrets{SlackBotToken: "xoxb"})
	var out bytes.Buffer
	if err := teamSignin([]string{"payments", "claude"}, strings.NewReader("not-a-token\n"), &out); err == nil || !strings.Contains(err.Error(), "sk-ant-") {
		t.Fatal(err)
	}
	if err := teamSignin([]string{"payments", "claude"}, strings.NewReader("sk-ant-oat-team\n"), &out); err != nil {
		t.Fatal(err)
	}
	secrets, _ := loadTeamSecrets("payments")
	if secrets.ClaudeToken != "sk-ant-oat-team" || secrets.ClaudeSignedIn.IsZero() || secrets.SlackBotToken != "xoxb" ||
		readTestFile(t, filepath.Join(teamKeysDir("payments"), teamClaudeKeyFile)) != "sk-ant-oat-team\n" {
		t.Fatalf("%+v", secrets)
	}
	// Back to this machine's: the token file goes.
	if err := teamSignin([]string{"payments", "claude", "--machine"}, nil, &out); err != nil {
		t.Fatal(err)
	}
	if secrets, _ := loadTeamSecrets("payments"); secrets.ClaudeToken != "" || fileExists(filepath.Join(teamKeysDir("payments"), teamClaudeKeyFile)) {
		t.Fatalf("%+v", secrets)
	}
}

func TestTeamClaudeFallback(t *testing.T) {
	setupTeamTest(t)
	for _, c := range []struct {
		member teamMember
		want   string
	}{
		{teamMember{Agent: "claude", Model: "opus"}, "anthropic/claude-opus-5.5"},
		{teamMember{Agent: "claude"}, "anthropic/claude-opus-5.5"},
		{teamMember{Agent: "claude", Model: "sonnet"}, "anthropic/claude-sonnet-5.5"},
		{teamMember{Agent: "claude", Model: "claude-opus-5-5"}, "anthropic/claude-opus-5.5"},
		{teamMember{Agent: "claude", Model: "opus", Fallback: "anthropic/claude-opus-5"}, "anthropic/claude-opus-5"},
		{teamMember{Agent: "claude", Model: "haiku"}, ""},
	} {
		if got, err := teamClaudeFallback(c.member); err != nil || got != c.want {
			t.Errorf("%+v: %q %v, want %q", c.member, got, err, c.want)
		}
	}
	// The wizard asks for one OpenRouter hasn't.
	member := teamMember{Agent: "claude", Model: "haiku"}
	var secrets teamSecrets
	asked := false
	var out bytes.Buffer
	if err := teamAskClaude(strings.NewReader("sk-ant-oat-x\nanthropic/claude-sonnet-5.5\n"), &out, &member, &secrets, &asked); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if member.Fallback != "anthropic/claude-sonnet-5.5" || secrets.ClaudeToken != "sk-ant-oat-x" || !strings.Contains(out.String(), "OpenRouter has no fallback for haiku") {
		t.Fatalf("%+v %+v\n%s", member, secrets, out.String())
	}
}

func TestTeamTellsTheChannelAboutTheCoder(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	channel := &fakeTeamChannel{}
	b.channel = channel
	config := testTeamConfig()
	now := time.Now().UTC()
	saveTeamSecrets("payments", teamSecrets{ClaudeToken: "sk-ant-oat-team", ClaudeSignedIn: now.Add(-time.Hour)})
	os.MkdirAll(teamEventsDir("payments"), 0o700)
	events := filepath.Join(teamEventsDir("payments"), "payments-t-3.log")
	appendEvents := func(lines ...string) {
		file, _ := os.OpenFile(events, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		for _, line := range lines {
			file.WriteString(now.Format(time.RFC3339) + " " + line + "\n")
		}
		file.Close()
	}
	posts := func() []string {
		var out []string
		for _, call := range channel.calls {
			if strings.HasPrefix(call, "post ") {
				out = append(out, call)
			}
		}
		return out
	}
	b.tellSubscriptions(config, now)
	appendEvents("fallback coder claude signin anthropic/claude-opus-5.5", "spend 0.02")
	b.tellSubscriptions(config, now)
	if got := posts(); len(got) != 1 || !strings.Contains(got[0], "The coder's Claude sign-in doesn't work, so it uses OpenRouter (anthropic/claude-opus-5.5) now") ||
		!strings.Contains(got[0], "`/hi-payments signin claude`") {
		t.Fatal(got)
	}
	// A second box failing over isn't told again.
	other := filepath.Join(teamEventsDir("payments"), "payments-t-4.log")
	os.WriteFile(other, []byte(now.Format(time.RFC3339)+" fallback coder claude limit anthropic/claude-opus-5.5\n"), 0o600)
	b.tellSubscriptions(config, now)
	if len(posts()) != 1 {
		t.Fatal(posts())
	}
	// The fallback's spend counts with the team's.
	if spent := teamSpendSince("payments", now.Add(-time.Minute)); spent < 0.02 {
		t.Fatal(spent)
	}
	// A new token: told, and a later failure is told once more.
	saveTeamSecrets("payments", teamSecrets{ClaudeToken: "sk-ant-oat-new", ClaudeSignedIn: now})
	b.tellSubscriptions(config, now)
	appendEvents("fallback coder claude limit anthropic/claude-opus-5.5 nokey")
	b.tellSubscriptions(config, now)
	if got := posts(); len(got) != 3 || !strings.Contains(got[1], "Claude is signed in again; the coder is back on it.") ||
		!strings.Contains(got[2], "no OpenRouter key to fall back on") || !strings.Contains(got[2], "tries Claude again every 15 minutes") {
		t.Fatal(got)
	}
}

func TestAgentEffortReachesEachAgent(t *testing.T) {
	setupTeamTest(t)
	// The agents' commands, as hi agent builds them in a box.
	home := t.TempDir()
	options, err := parseBoxOptions("agent", []string{"--effort", "max", "--team", "payments:coder"})
	if err != nil || options.effort != "max" || options.team != "payments:coder" {
		t.Fatal(options, err)
	}
	if _, err := parseBoxOptions("agent", []string{"--effort", "ultra"}); err == nil || !strings.Contains(err.Error(), "low, medium, high, xhigh, max") {
		t.Fatal(err)
	}
	if _, err := parseBoxOptions("agent", []string{"--team", "payments"}); err == nil {
		t.Fatal("a team without a role")
	}
	bin := filepath.Join(t.TempDir(), "agents", "bin")
	writeSkillTestFile(t, filepath.Join(bin, "claude"), "#!/bin/sh\n", 0o755)
	writeSkillTestFile(t, filepath.Join(bin, "codex"), "#!/bin/sh\n", 0o755)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	meta := boxMeta{Name: "b", Workdir: "/w", Model: "opus", Effort: "high", ProxyIP: "10.0.0.2"}
	var run []string
	command, err := agentBoxSetup("claude", "do it", meta, home, &run, map[string]string{})
	if err != nil || !strings.Contains(strings.Join(command, " "), " --effort high <") {
		t.Fatal(command, err)
	}
	if interactive, _ := agentBoxSetup("claude", "", meta, home, &run, map[string]string{}); strings.Join(interactive, " ") != "claude --dangerously-skip-permissions --model opus --effort high" {
		t.Fatal(interactive)
	}
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)
	os.WriteFile(filepath.Join(codexHome, "auth.json"), []byte("{}"), 0o600)
	if command, err = agentBoxSetup("codex", "do it", meta, home, &run, map[string]string{}); err != nil || !strings.Contains(strings.Join(command, " "), "-c model_reasoning_effort=high") {
		t.Fatal(command, err)
	}
	// A team's members: medium unless team.json says, and the lead too.
	config := testTeamConfig()
	if err := validateTeam(config); err != nil {
		t.Fatal(err)
	}
	config.Members["reviewer"] = teamMember{Agent: "codex", Effort: "extreme"}
	if err := validateTeam(config); err == nil || !strings.Contains(err.Error(), "the effort is") {
		t.Fatal(err)
	}
	if teamEffort(teamMember{}) != "medium" || teamEffort(teamMember{Effort: "high"}) != "high" ||
		!strings.Contains(teamMemberLabel(teamMember{Agent: "codex", Effort: "high"}), "effort high") ||
		strings.Contains(teamMemberLabel(teamMember{Agent: "codex"}), "effort") {
		t.Fatal("labels")
	}
	dir := teamDir("payments")
	os.MkdirAll(filepath.Join(dir, "lead"), 0o700)
	saveTeam(testTeamConfig())
	if err := writeTeamLead(testTeamConfig(), teamSecrets{}, "10.234.5.2", ""); err != nil {
		t.Fatal(err)
	}
	if settings := readTestFile(t, filepath.Join(dir, "lead", "config.yaml")); !strings.Contains(settings, "  reasoning_effort: medium\n  local_stream_stale_timeout: 120\n") {
		t.Fatal(settings)
	}
}

func TestTeamCodexMemberThroughTheProxy(t *testing.T) {
	setupTeamTest(t)
	newTeamTestBroker(t)
	config := testTeamConfig()
	// This machine's Codex is on an API key and the team has no ChatGPT
	// sign-in: the reviewer keeps auth.json in its box, as before.
	if teamCodexViaProxy("payments") || teamUsesChatGPT(config) {
		t.Fatal("on an API key")
	}
	now := time.Now().UTC()
	grant := teamChatGPTGrant{AccessToken: testChatGPTToken(now.Add(240*time.Hour), "acct-1", "team"), RefreshToken: "rt", SignedIn: now}
	saveTeamSecrets("payments", teamSecrets{ChatGPT: &grant})
	if !teamCodexViaProxy("payments:reviewer") || strings.Join(teamChatGPTRoles(config), ",") != "reviewer" {
		t.Fatal(teamChatGPTRoles(config))
	}
	args, secret, err := teamMemberProxyArgs(boxMeta{Name: "payments-t-9", Agent: "codex", Team: "payments:reviewer"}, nil, "")
	if err != nil || secret != "openrouter:/secrets/team/openrouter.env,/secrets/hermes.env" ||
		!strings.Contains(strings.Join(args, " "), boxChatGPTEnv+"=/secrets/team/chatgpt.json") || !strings.Contains(strings.Join(args, " "), boxRoleEnv+"=reviewer") {
		t.Fatal(args, secret, err)
	}
	if !fileExists(filepath.Join(teamKeysDir("payments"), teamChatGPTKeyFile)) {
		t.Fatal("no token for the proxy")
	}
	// The box gets Codex's config at its proxy and a placeholder, and no
	// sign-in.
	bin := filepath.Join(t.TempDir(), "agents", "bin")
	writeSkillTestFile(t, filepath.Join(bin, "codex"), "#!/bin/sh\n", 0o755)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	home := t.TempDir()
	env := map[string]string{}
	var run []string
	if _, err := agentBoxSetup("codex", "review it", boxMeta{Name: "payments-t-9", Team: "payments:reviewer", ProxyIP: "10.234.9.2", Model: "gpt-6.1-sol"}, home, &run, env); err != nil {
		t.Fatal(err)
	}
	configText := readTestFile(t, filepath.Join(home, ".codex", "config.toml"))
	if !strings.Contains(configText, `base_url = "http://10.234.9.2:`+strconv.Itoa(boxInjectPort)+`/backend-api/codex"`) || !strings.Contains(configText, `wire_api = "responses"`) ||
		env[codexBoxKeyEnv] != boxClaudePlacehold || fileExists(filepath.Join(home, ".codex", "auth.json")) {
		t.Fatal(configText, env)
	}
	if !teamMemberSignedIn("payments:reviewer", "codex") {
		t.Fatal("the team's sign-in is enough")
	}
	if label := teamMemberLabel(teamMember{Agent: "codex", Model: "gpt-6.1-sol"}); !strings.Contains(label, "on ChatGPT, falling back to openai/gpt-6.1-sol on OpenRouter") {
		t.Fatal(label)
	}
}
