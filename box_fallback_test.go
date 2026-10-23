package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestClaudeOpenRouterModel(t *testing.T) {
	for model, want := range map[string]string{
		"claude-opus-5-5":            "anthropic/claude-opus-5.5",
		"claude-haiku-4-5-20251001":  "anthropic/claude-haiku-4.5",
		"claude-opus-5":              "anthropic/claude-opus-5",
		"claude-sonnet-5-5[1m]":      "anthropic/claude-sonnet-5.5",
		"anthropic/claude-fable-5.1": "anthropic/claude-fable-5.1",
		"":                           "",
	} {
		if got := claudeOpenRouterModel(model); got != want {
			t.Errorf("%q: %q, want %q", model, got, want)
		}
	}
}

func TestClaudeRefusal(t *testing.T) {
	header := func(pairs ...string) http.Header {
		h := http.Header{}
		for i := 0; i < len(pairs); i += 2 {
			h.Set(pairs[i], pairs[i+1])
		}
		return h
	}
	cases := []struct {
		status int
		header http.Header
		want   string
	}{
		{401, nil, "signin"},
		{403, nil, "signin"},
		{429, header("anthropic-ratelimit-unified-status", "rejected"), "limit"},
		{429, header("retry-after", "3600"), "limit"},
		{429, header("retry-after", "20"), ""}, // Claude Code waits and retries itself
		{529, nil, ""},
		{200, nil, ""},
	}
	for _, c := range cases {
		if got := claudeRefusal(c.status, c.header); got != c.want {
			t.Errorf("%d %v: %q, want %q", c.status, c.header, got, c.want)
		}
	}
}

func TestBoxClaudeFailover(t *testing.T) {
	claudeStatus := http.StatusUnauthorized
	claudeCalls := 0
	claude := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claudeCalls++
		w.WriteHeader(claudeStatus)
		io.WriteString(w, `{"type":"error","error":{"type":"authentication_error","message":"OAuth token has expired"}}`)
	}))
	defer claude.Close()
	var got struct {
		path, auth, beta, model string
	}
	openRouter := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		got.path, got.auth, got.beta, got.model = r.URL.Path+"?"+r.URL.RawQuery, r.Header.Get("Authorization"), r.Header.Get("anthropic-beta"), body["model"].(string)
		io.WriteString(w, `{"type":"message","content":[{"type":"text","text":"from openrouter"}],"usage":{"cost":0.012}}`)
	}))
	defer openRouter.Close()
	dir := t.TempDir()
	keys, events := filepath.Join(dir, "openrouter.env"), filepath.Join(dir, "events", "box-1.log")
	os.MkdirAll(filepath.Dir(events), 0o700)
	os.WriteFile(keys, []byte("OPENROUTER_API_KEY=sk-or-team\n"), 0o600)
	token := filepath.Join(dir, "claude-token")
	os.WriteFile(token, []byte("sk-ant-oat-team\n"), 0o600)
	var logged bytes.Buffer
	log := &boxNetworkLog{file: &logged}
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	fallback := &boxFallback{kind: "claude", keys: filepath.Join(dir, "missing.env") + "," + keys, role: "coder", events: events, upstream: openRouter.URL, log: log,
		now: func() time.Time { return now }}
	proxy := httptest.NewServer(newBoxFailover(newBoxClaudeInjector(filepath.Join(dir, "none")+","+token, claude.URL, log), fallback))
	defer proxy.Close()
	call := func(model string) (int, string) {
		request, _ := http.NewRequest("POST", proxy.URL+"/v1/messages?beta=true", strings.NewReader(`{"model":"`+model+`","max_tokens":10,"messages":[]}`))
		request.Header.Set("Authorization", "Bearer "+boxClaudePlacehold)
		request.Header.Set("anthropic-beta", "oauth-2025-04-20,interleaved-thinking-2025-05-14")
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		data, _ := io.ReadAll(response.Body)
		return response.StatusCode, string(data)
	}
	// Claude refuses the sign-in: the same call goes to OpenRouter, with
	// the model's name there, the team's key, and no OAuth beta flag.
	if status, body := call("claude-opus-5-5"); status != 200 || !strings.Contains(body, "from openrouter") {
		t.Fatal(status, body)
	}
	if got.path != "/api/v1/messages?beta=true" || got.auth != "Bearer sk-or-team" || got.beta != "interleaved-thinking-2025-05-14" || got.model != "anthropic/claude-opus-5.5" {
		t.Fatalf("%+v", got)
	}
	if text := readTestFile(t, events); !strings.Contains(text, "fallback coder claude signin anthropic/claude-opus-5.5\n") || !strings.Contains(text, "spend 0.012") {
		t.Fatal(text)
	}
	if !strings.Contains(logged.String(), "claude-refused 401:signin") {
		t.Fatal(logged.String())
	}
	// For the next 15 minutes the calls go straight to OpenRouter, a
	// smaller model by its own name.
	if call("claude-haiku-4-5-20251001"); claudeCalls != 1 || got.model != "anthropic/claude-haiku-4.5" {
		t.Fatal(claudeCalls, got.model)
	}
	// Then Claude is tried again, and answers.
	now = now.Add(16 * time.Minute)
	claudeStatus = http.StatusOK
	if status, body := call("claude-opus-5-5"); status != 200 || claudeCalls != 2 || !strings.Contains(body, "authentication_error") {
		t.Fatal(status, body, claudeCalls) // Claude's own body, passed through
	}
	// A member's own fallback model is used for every call.
	fallback.model = "anthropic/claude-opus-5"
	claudeStatus = http.StatusForbidden
	now = now.Add(16 * time.Minute)
	if status, _ := call("claude-haiku-4-5"); status != 200 || got.model != "anthropic/claude-opus-5" {
		t.Fatal(status, got.model)
	}
}

func TestBoxClaudeFailoverWithoutAKey(t *testing.T) {
	claude := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("retry-after", "7200")
		w.WriteHeader(http.StatusTooManyRequests)
		io.WriteString(w, `{"type":"error","error":{"type":"rate_limit_error","message":"usage limit"}}`)
	}))
	defer claude.Close()
	dir := t.TempDir()
	token := filepath.Join(dir, "claude-token")
	os.WriteFile(token, []byte("sk-ant-oat-team\n"), 0o600)
	events := filepath.Join(dir, "box.log")
	log := &boxNetworkLog{file: io.Discard}
	fallback := &boxFallback{kind: "claude", keys: filepath.Join(dir, "none.env"), role: "coder", events: events, upstream: "http://127.0.0.1:1", log: log}
	proxy := httptest.NewServer(newBoxFailover(newBoxClaudeInjector(token, claude.URL, log), fallback))
	defer proxy.Close()
	request, _ := http.NewRequest("POST", proxy.URL+"/v1/messages", strings.NewReader(`{"model":"claude-opus-5-5"}`))
	request.Header.Set("Authorization", "Bearer "+boxClaudePlacehold)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(response.Body)
	response.Body.Close()
	// Claude's refusal goes back as it was, and the event says there was
	// no key.
	if response.StatusCode != http.StatusTooManyRequests || !strings.Contains(string(data), "usage limit") ||
		!strings.Contains(readTestFile(t, events), "fallback coder claude limit anthropic/claude-opus-5.5 nokey") {
		t.Fatal(response.StatusCode, string(data), readTestFile(t, events))
	}
}

func TestBoxClaudeFailoverPassesAShortRateLimit(t *testing.T) {
	claude := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("retry-after", "10")
		w.WriteHeader(http.StatusTooManyRequests)
		io.WriteString(w, "slow down")
	}))
	defer claude.Close()
	dir := t.TempDir()
	token := filepath.Join(dir, "claude-token")
	os.WriteFile(token, []byte("sk-ant-oat-team\n"), 0o600)
	openRouterCalled := false
	openRouter := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { openRouterCalled = true }))
	defer openRouter.Close()
	keys := filepath.Join(dir, "k.env")
	os.WriteFile(keys, []byte("OPENROUTER_API_KEY=sk-or\n"), 0o600)
	log := &boxNetworkLog{file: io.Discard}
	proxy := httptest.NewServer(newBoxFailover(newBoxClaudeInjector(token, claude.URL, log), &boxFallback{kind: "claude", keys: keys, upstream: openRouter.URL, log: log}))
	defer proxy.Close()
	response, err := http.Post(proxy.URL+"/v1/messages", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != 429 || string(data) != "slow down" || response.Header.Get("retry-after") != "10" || openRouterCalled {
		t.Fatal(response.StatusCode, string(data), openRouterCalled)
	}
}

func TestBoxChatGPTFailover(t *testing.T) {
	chatGPT := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, `{"detail":"token expired"}`)
	}))
	defer chatGPT.Close()
	var got struct {
		path, auth, account, originator string
		body                            map[string]any
	}
	openRouter := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.path, got.auth, got.account, got.originator = r.URL.Path, r.Header.Get("Authorization"), r.Header.Get("ChatGPT-Account-ID"), r.Header.Get("originator")
		json.NewDecoder(r.Body).Decode(&got.body)
		io.WriteString(w, `{"output":[{"type":"message","content":[{"type":"output_text","text":"from openrouter"}]}],"usage":{"cost":0.003}}`)
	}))
	defer openRouter.Close()
	dir := t.TempDir()
	token := filepath.Join(dir, "chatgpt.json")
	os.WriteFile(token, []byte(`{"access_token":"`+testChatGPTToken(time.Now().Add(time.Hour), "acct-1", "team")+`"}`), 0o600)
	keys := filepath.Join(dir, "openrouter.env")
	os.WriteFile(keys, []byte("OPENROUTER_API_KEY=sk-or-team\n"), 0o600)
	events := filepath.Join(dir, "box.log")
	var logged bytes.Buffer
	log := &boxNetworkLog{file: &logged}
	fallback := &boxFallback{kind: teamProviderChatGPT, keys: keys, role: "reviewer", events: events, upstream: openRouter.URL, log: log}
	// Codex names itself: the proxy leaves its originator alone.
	proxy := httptest.NewServer(boxRouteChatGPT(newBoxFailover(newBoxChatGPTInjector(token, chatGPT.URL, "", log), fallback), http.NotFoundHandler()))
	defer proxy.Close()
	body := `{"model":"gpt-6.1-sol","store":false,"include":["reasoning.encrypted_content"],"input":[` +
		`{"type":"message","role":"user","content":[{"type":"input_text","text":"review it"}]},` +
		`{"type":"reasoning","encrypted_content":"gAAAA-chatgpt-only"},` +
		`{"type":"function_call","call_id":"c1","name":"shell","arguments":"{}"}]}`
	request, _ := http.NewRequest("POST", proxy.URL+"/backend-api/codex/responses", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+boxClaudePlacehold)
	request.Header.Set("originator", "codex_exec")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != 200 || !strings.Contains(string(data), "from openrouter") {
		t.Fatal(response.StatusCode, string(data))
	}
	input, _ := got.body["input"].([]any)
	if got.path != "/api/v1/responses" || got.auth != "Bearer sk-or-team" || got.account != "" || got.originator != "" ||
		got.body["model"] != "openai/gpt-6.1-sol" || got.body["include"] != nil || len(input) != 2 || strings.Contains(fmt.Sprint(input), "reasoning") {
		t.Fatalf("%+v", got)
	}
	if text := readTestFile(t, events); !strings.Contains(text, "fallback reviewer chatgpt signin openai/gpt-6.1-sol\n") || !strings.Contains(text, "spend 0.003") {
		t.Fatal(text)
	}
	if !strings.Contains(logged.String(), "chatgpt-refused 401:signin") || strings.Contains(logged.String(), "claude-refused") {
		t.Fatal(logged.String())
	}
	// While on OpenRouter, Codex's other calls aren't model calls: not found.
	response, _ = http.Get(proxy.URL + "/backend-api/codex/models")
	if response.StatusCode != http.StatusNotFound {
		t.Fatal(response.StatusCode)
	}
	// A short rate limit goes back to Codex, which retries.
	if chatGPTRefusal(429, nil, []byte(`{"error":{"type":"rate_limit_exceeded"}}`)) != "" || chatGPTRefusal(429, nil, []byte(`{"error":{"type":"usage_limit_reached"}}`)) != "limit" {
		t.Fatal("429s")
	}
}
