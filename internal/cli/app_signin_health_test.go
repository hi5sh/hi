package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestBoxPlanUsage(t *testing.T) {
	claude := http.Header{}
	claude.Set("anthropic-ratelimit-unified-status", "allowed_warning")
	claude.Set("anthropic-ratelimit-unified-7d-utilization", "0.93")
	claude.Set("anthropic-ratelimit-unified-7d-reset", "1795831200")
	status, weekly, weeklyReset, reset, ok := boxPlanUsage("claude", claude)
	if !ok || status != "allowed" || weekly != 0.93 || weeklyReset != 1795831200 || reset != 0 {
		t.Fatal(status, weekly, weeklyReset, reset, ok)
	}
	claude.Set("anthropic-ratelimit-unified-status", "rejected")
	claude.Set("anthropic-ratelimit-unified-reset", "1795800000")
	if status, _, _, reset, _ := boxPlanUsage("claude", claude); status != "rejected" || reset != 1795800000 {
		t.Fatal(status, reset)
	}
	if _, _, _, _, ok := boxPlanUsage("claude", http.Header{}); ok {
		t.Fatal("an answer that says nothing")
	}
	// ChatGPT: the weekly window is the one of a week or more, primary or
	// secondary; a window at 100% is a limit reached.
	chatgpt := http.Header{}
	chatgpt.Set("x-codex-primary-used-percent", "100")
	chatgpt.Set("x-codex-primary-window-minutes", "300")
	chatgpt.Set("x-codex-primary-reset-at", "1795800000")
	chatgpt.Set("x-codex-secondary-used-percent", "42")
	chatgpt.Set("x-codex-secondary-window-minutes", "10080")
	chatgpt.Set("x-codex-secondary-reset-at", "1795947617")
	status, weekly, weeklyReset, reset, ok = boxPlanUsage(appProviderChatGPT, chatgpt)
	if !ok || status != "rejected" || weekly != 0.42 || weeklyReset != 1795947617 || reset != 1795800000 {
		t.Fatal(status, weekly, weeklyReset, reset, ok)
	}
}

// TestBoxFailoverWritesUsage: the proxy writes the plan's usage from the
// subscription's answers when it changes, and again after a while.
func TestBoxFailoverWritesUsage(t *testing.T) {
	used := "0.10"
	primary := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("anthropic-ratelimit-unified-status", "allowed")
		w.Header().Set("anthropic-ratelimit-unified-7d-utilization", used)
		w.Header().Set("anthropic-ratelimit-unified-7d-reset", "1795831200")
		io.WriteString(w, `{"ok":true}`)
	})
	events := filepath.Join(t.TempDir(), "box.log")
	clock := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)
	fallback := &boxFallback{kind: "claude", role: "dev", events: events, log: &boxNetworkLog{}, now: func() time.Time { return clock }}
	handler := newBoxFailover(primary, fallback)
	call := func() {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"claude-opus-5-5"}`)))
		if recorder.Body.String() != `{"ok":true}` {
			t.Fatal(recorder.Body.String())
		}
	}
	lines := func() []string {
		data, _ := os.ReadFile(events)
		return strings.Split(strings.TrimSpace(string(data)), "\n")
	}
	call()
	call()
	used = "0.104" // the same percent
	call()
	if got := lines(); len(got) != 1 || !strings.HasSuffix(got[0], " usage dev claude status=allowed weekly=0.1000 weekly_reset=1795831200 reset=0") {
		t.Fatalf("%q", got)
	}
	used = "0.93"
	call()
	clock = clock.Add(boxUsageEvery)
	call()
	if got := lines(); len(got) != 3 || !strings.Contains(got[1], "weekly=0.9300") || !strings.HasPrefix(got[2], "2026-11-22T10:30:00Z usage") {
		t.Fatalf("%q", got)
	}
	event, ok := parseAppSigninEvent(lines()[1])
	if !ok || event.what != "usage" || event.kind != "claude" || event.weekly != 0.93 || event.status != "allowed" || event.weeklyReset.Unix() != 1795831200 {
		t.Fatalf("%+v", event)
	}
}

func TestSigninSettle(t *testing.T) {
	now := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)
	week := now.Add(72 * time.Hour)
	for _, test := range []struct {
		name string
		h    appSigninHealth
		want string
	}{
		{"nothing known", appSigninHealth{}, signinUnknown},
		{"checked", appSigninHealth{Checked: now}, signinOK},
		{"89% used", appSigninHealth{WeeklyKnown: true, WeeklyUsed: 0.89, WeeklyReset: week}, signinOK},
		{"91% used", appSigninHealth{WeeklyKnown: true, WeeklyUsed: 0.91, WeeklyReset: week}, signinLow},
		{"a new week", appSigninHealth{WeeklyKnown: true, WeeklyUsed: 0.95, WeeklyReset: now.Add(-time.Minute), Checked: now}, signinOK},
		{"limited", appSigninHealth{Limited: true, LimitedUntil: now.Add(time.Hour)}, signinLimited},
		{"limited, lifted", appSigninHealth{Limited: true, LimitedUntil: now.Add(-time.Minute), Checked: now}, signinOK},
		{"limited, no end", appSigninHealth{Limited: true}, signinLimited},
		{"signed out over all", appSigninHealth{SignedOut: "Claude refused the sign-in", Limited: true, WeeklyKnown: true, WeeklyUsed: 0.99}, signinSignedOut},
		{"expiring", appSigninHealth{Expires: now.Add(10 * 24 * time.Hour), Checked: now}, signinExpiring},
		{"not expiring yet", appSigninHealth{Expires: now.Add(20 * 24 * time.Hour), Checked: now}, signinOK},
	} {
		h := test.h
		h.settle(now)
		if h.State != test.want {
			t.Errorf("%s: %s (%s), want %s", test.name, h.State, h.Why, test.want)
		}
	}
	h := appSigninHealth{WeeklyKnown: true, WeeklyUsed: 0.93, WeeklyReset: week}
	h.settle(now)
	if *h.WeeklyLeft != 7 || h.Why != "7% of its weekly limit is left" || !h.Resets.Equal(week) {
		t.Fatalf("%+v", h)
	}
}

// signinFakes is the subscriptions' services as hi's checks call them.
type signinFakes struct {
	mu       sync.Mutex
	calls    []string
	claude   int     // count_tokens' answer
	weekly   float64 // ChatGPT's weekly use, percent
	agyLeft  float64
	agyAgent string
}

func (f *signinFakes) serve(t *testing.T) *httptest.Server {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		defer f.mu.Unlock()
		f.calls = append(f.calls, r.Method+" "+r.URL.Path)
		switch r.URL.Path {
		case "/v1/messages/count_tokens":
			if r.Header.Get("Authorization") != "Bearer sk-ant-oat01-good" || r.Header.Get("anthropic-beta") != "oauth-2025-04-20" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.WriteHeader(f.claude)
			io.WriteString(w, `{"input_tokens":8}`)
		case "/backend-api/wham/usage":
			if r.Header.Get("ChatGPT-Account-Id") != "acct-1" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"plan_type": "prolite", "email": "someone@example.com", "rate_limit": map[string]any{
				"allowed": true, "limit_reached": false,
				"primary_window": map[string]any{"used_percent": f.weekly, "limit_window_seconds": 604800, "reset_at": 1795947617}}})
		case "/v1internal:loadCodeAssist":
			io.WriteString(w, `{"currentTier":{"id":"free-tier","name":"Antigravity"},"paidTier":{"id":"g1-pro-tier","name":"Google AI Pro"}}`)
		case "/v1internal:retrieveUserQuotaSummary":
			// Google answers agy's own User-Agent, and an empty request.
			f.agyAgent = r.Header.Get("User-Agent")
			if f.agyAgent != "antigravity-cli" || strings.TrimSpace(string(body)) != "{}" {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"groups": []any{map[string]any{"displayName": "Claude and GPT models", "buckets": []any{
				map[string]any{"window": "weekly", "remainingFraction": f.agyLeft, "resetTime": "2026-11-29T10:20:28Z"},
				map[string]any{"window": "5h", "remainingFraction": 1, "resetTime": "2026-11-22T15:20:28Z"}}}}})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	claude, chatgpt, agy := claudeAPIBase, chatGPTBackend, agyQuotaBase
	claudeAPIBase, chatGPTBackend, agyQuotaBase = server.URL, server.URL, server.URL
	t.Cleanup(func() { claudeAPIBase, chatGPTBackend, agyQuotaBase = claude, chatgpt, agy })
	return server
}

// setupSigninApp is an app with its own Claude token, ChatGPT sign-in,
// and agy sign-in, and a dev box whose agy answers.
func setupSigninApp(t *testing.T, name string) *signinFakes {
	t.Helper()
	config := testTeamConfig()
	config.Name = name
	writeTeamForAdmin(t, config)
	now := time.Now().UTC()
	grant := &appChatGPTGrant{AccessToken: testChatGPTToken(now.Add(240*time.Hour), "acct-1", "prolite"), RefreshToken: "rt-1", AccountID: "acct-1", SignedIn: now}
	agyToken := `{"token":{"access_token":"ya29.box","refresh_token":"1//r","expiry":"2026-11-22T11:00:00Z"},"auth_method":"consumer","id_token_email":"someone@example.com"}`
	if err := saveTeamSecrets(name, appSecrets{SlackBotToken: "xoxb-" + name, ClaudeToken: "sk-ant-oat01-good", ClaudeSignedIn: now, ChatGPT: grant,
		Agy: &appAgySignIn{Token: agyToken, Email: "someone@example.com", At: now}}); err != nil {
		t.Fatal(err)
	}
	writeSigninFile(t, filepath.Join(appKeysDir(name), appClaudeKeyFile), "sk-ant-oat01-good\n", 0o600)
	writeSigninFile(t, filepath.Join(appDevHome(name), filepath.FromSlash(appDevAgyHome), "antigravity-oauth-token"), agyToken, 0o600)
	fakes := &signinFakes{claude: http.StatusOK, weekly: 12, agyLeft: 0.8}
	exec := appDevBoxExec
	appDevBoxExec = func(team string, command ...string) (string, error) {
		fakes.mu.Lock()
		defer fakes.mu.Unlock()
		fakes.calls = append(fakes.calls, "exec "+command[len(command)-1])
		return "Fetching available models...\ngemini-4-neon-high\tGemini 4 Neon (High)\n", nil
	}
	t.Cleanup(func() { appDevBoxExec = exec })
	return fakes
}

func TestUpdateAppSigninHealth(t *testing.T) {
	setupTeamTest(t)
	fakes := setupSigninApp(t, "orders")
	fakes.serve(t)
	// What the proxies wrote before hi first looked is history.
	events := filepath.Join(appEventsDir("orders"), "app-orders-dev.log")
	writeSigninFile(t, events, "2026-11-20T10:00:00Z fallback dev claude signin anthropic/claude-opus-5.5\n", 0o600)
	now := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)
	updateAppSigninHealth("orders", now, io.Discard)
	state, ok := loadAppSigninHealth("orders")
	if !ok {
		t.Fatal("no record")
	}
	claude, chatgpt, agy := state.Signins["claude"], state.Signins[appProviderChatGPT], state.Signins["agy"]
	if claude.State != signinOK || claude.Source != "own" || !strings.HasPrefix(claude.Key, "claude:") || strings.Contains(claude.Key, "good") {
		t.Fatalf("claude: %+v", claude)
	}
	if chatgpt.State != signinOK || chatgpt.Plan != "ChatGPT Pro Lite" || *chatgpt.WeeklyLeft != 88 || chatgpt.Resets.IsZero() == false {
		t.Fatalf("chatgpt: %+v", chatgpt)
	}
	if agy.State != signinOK || agy.Plan != "Google AI Pro" || *agy.WeeklyLeft != 80 || fakes.agyAgent != "antigravity-cli" {
		t.Fatalf("agy: %+v", agy)
	}
	data, _ := os.ReadFile(appSigninHealthPath("orders"))
	for _, secret := range []string{"sk-ant", "someone@example.com", "ya29", "acct-1"} {
		if strings.Contains(string(data), secret) {
			t.Fatalf("the record has %s:\n%s", secret, data)
		}
	}

	// The proxies' news: the plan's usage, then a refusal.
	appendFile := func(line string) {
		file, _ := os.OpenFile(events, os.O_APPEND|os.O_WRONLY, 0o600)
		file.WriteString(line + "\n")
		file.Close()
	}
	appendFile("2026-11-22T10:01:00Z usage dev claude status=allowed weekly=0.9300 weekly_reset=1795831200 reset=0")
	updateAppSigninHealth("orders", now.Add(2*time.Minute), io.Discard)
	state, _ = loadAppSigninHealth("orders")
	if h := state.Signins["claude"]; h.State != signinLow || *h.WeeklyLeft != 7 {
		t.Fatalf("low: %+v", h)
	}
	appendFile("2026-11-22T10:03:00Z fallback dev claude signin anthropic/claude-opus-5.5")
	fakes.claude = http.StatusUnauthorized
	updateAppSigninHealth("orders", now.Add(4*time.Minute), io.Discard)
	state, _ = loadAppSigninHealth("orders")
	if h := state.Signins["claude"]; h.State != signinSignedOut || h.Why != "Claude refused the sign-in" {
		t.Fatalf("signed out: %+v", h)
	}
	// Signed out, it's checked again within minutes, and works again.
	fakes.claude = http.StatusOK
	before := len(fakes.calls)
	updateAppSigninHealth("orders", now.Add(5*time.Minute), io.Discard)
	if len(fakes.calls) != before {
		t.Fatalf("checked again at once: %v", fakes.calls[before:])
	}
	updateAppSigninHealth("orders", now.Add(4*time.Minute+signinRecheck), io.Discard)
	state, _ = loadAppSigninHealth("orders")
	if h := state.Signins["claude"]; h.State != signinLow {
		t.Fatalf("works again: %+v", h)
	}
	// A limit reached, as an answer says; it lifts when it said.
	appendFile("2026-11-22T10:20:00Z usage dev claude status=rejected weekly=0.9300 weekly_reset=1795831200 reset=1795800000")
	updateAppSigninHealth("orders", now.Add(21*time.Minute), io.Discard)
	state, _ = loadAppSigninHealth("orders")
	if h := state.Signins["claude"]; h.State != signinLimited || h.Resets.Unix() != 1795800000 {
		t.Fatalf("limited: %+v", h)
	}
	// agy signed out in the box.
	appDevBoxExec = func(team string, command ...string) (string, error) {
		return "Fetching available models...\nError: Please sign in to view available models. Launch the CLI without arguments to sign in.\n", &exitError{}
	}
	updateAppSigninHealth("orders", now.Add(2*time.Hour), io.Discard)
	state, _ = loadAppSigninHealth("orders")
	if h := state.Signins["agy"]; h.State != signinSignedOut || h.Why != "agy isn't signed in" {
		t.Fatalf("agy: %+v", h)
	}
	// Another sign-in starts afresh.
	writeSigninFile(t, filepath.Join(appKeysDir("orders"), appClaudeKeyFile), "sk-ant-oat01-other\n", 0o600)
	fakes.claude = http.StatusOK
	updateAppSigninHealth("orders", now.Add(3*time.Hour), io.Discard)
	state, _ = loadAppSigninHealth("orders")
	if h := state.Signins["claude"]; h.State != signinSignedOut {
		// The fake knows only the first token.
		t.Fatalf("a new token: %+v", h)
	}
}

func writeSigninFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, path, content, mode)
}

type exitError struct{}

func (*exitError) Error() string { return "exit status 1" }

func TestTellSignins(t *testing.T) {
	a := setupAdminTest(t)
	for _, name := range []string{"billing", "orders"} {
		config := testTeamConfig()
		config.Name = name
		writeTeamForAdmin(t, config)
	}
	now := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)
	write := func(name string, h appSigninHealth) {
		h.Kind, h.Checked = "claude", now
		h.settle(now)
		if err := saveAppSigninHealth(name, appSigninHealthState{Signins: map[string]*appSigninHealth{"claude": &h}}); err != nil {
			t.Fatal(err)
		}
	}
	shared := appSigninHealth{Source: "shared/claude-max", Key: "claude:abc123"}
	write("billing", shared)
	write("orders", shared)
	// No Slack app on the server: nothing to say it with.
	a.s.tellSignins(now)
	if len(*a.posts) != 0 {
		t.Fatal(*a.posts)
	}
	org := t.TempDir()
	writeTestFile(t, filepath.Join(org, "slack.json"), `{"bot_token":"xoxb-server","channel":"C0SERVER"}`, 0o600)
	adminOrgDir = func() string { return org }
	a.s.tellSignins(now)
	if len(*a.posts) != 0 {
		t.Fatal("all is well, and it said:", *a.posts)
	}
	signedOut := shared
	signedOut.SignedOut = "Claude refused the sign-in"
	write("orders", signedOut)
	a.s.tellSignins(now)
	a.s.tellSignins(now.Add(time.Minute))
	if len(*a.posts) != 1 || !strings.HasPrefix((*a.posts)[0], "xoxb-server C0SERVER :warning: *Claude stopped working* for billing and orders (shared/claude-max) on ") ||
		!strings.Contains((*a.posts)[0], "Vault tab") {
		t.Fatalf("%q", *a.posts)
	}
	write("orders", shared)
	a.s.tellSignins(now.Add(2 * time.Minute))
	if len(*a.posts) != 2 || !strings.Contains((*a.posts)[1], ":white_check_mark: *Claude works again* for billing and orders") {
		t.Fatalf("%q", *a.posts)
	}
	// Low: once a week window, whatever the percent does.
	week := now.Add(72 * time.Hour)
	low := shared
	low.WeeklyKnown, low.WeeklyUsed, low.WeeklyReset = true, 0.93, week
	write("billing", low)
	write("orders", low)
	a.s.tellSignins(now.Add(3 * time.Minute))
	lower := low
	lower.WeeklyUsed = 0.96
	write("orders", lower)
	a.s.tellSignins(now.Add(4 * time.Minute))
	if len(*a.posts) != 3 || !strings.Contains((*a.posts)[2], "*Claude has 7% of its weekly limit left*") {
		t.Fatalf("%q", *a.posts)
	}
	// A limit, until when it says; lifting says nothing.
	limited := low
	limited.Limited, limited.LimitedUntil = true, now.Add(time.Hour)
	write("orders", limited)
	a.s.tellSignins(now.Add(5 * time.Minute))
	if len(*a.posts) != 4 || !strings.Contains((*a.posts)[3], "*Claude reached its usage limit* for billing and orders") || !strings.Contains((*a.posts)[3], "until Sun 22 Nov 11:00 UTC") {
		t.Fatalf("%q", *a.posts)
	}
	write("billing", shared)
	write("orders", shared)
	a.s.tellSignins(now.Add(time.Hour + time.Minute))
	if len(*a.posts) != 4 {
		t.Fatalf("%q", *a.posts)
	}
}

func TestAdminShowsSignins(t *testing.T) {
	setupAdminTest(t)
	now := time.Now().UTC()
	h := appSigninHealth{Kind: "claude", Source: "own", Key: "claude:abc", SignedOut: "Claude refused the sign-in", Checked: now}
	ok := appSigninHealth{Kind: "agy", Source: "machine", Key: "agy:machine", Checked: now}
	if err := saveAppSigninHealth("payments", appSigninHealthState{Signins: map[string]*appSigninHealth{"claude": &h, "agy": &ok}}); err != nil {
		t.Fatal(err)
	}
	view := adminAppOf("payments")
	if view.SigninWorst != signinSignedOut || len(view.Signins) != 2 || view.Signins[0].Kind != "claude" || view.Signins[1].Kind != "agy" {
		t.Fatalf("%+v", view.Signins)
	}
	if fix := view.Signins[0].Fix; !strings.Contains(fix, "/hi-payments signin claude") {
		t.Fatal(fix)
	}
	if view.Signins[1].Fix != "" || view.Signins[1].State != signinOK {
		t.Fatalf("%+v", view.Signins[1])
	}
	keys := adminMachineKeysOf()
	if len(keys.Apps) != 1 || keys.Apps[0].States["claude"] != signinSignedOut || keys.Apps[0].Claude != "machine" || keys.Apps[0].States["agy"] != signinOK {
		t.Fatalf("%+v", keys.Apps)
	}
}
