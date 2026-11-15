package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func budgetTestApp(t *testing.T, usd float64) {
	t.Helper()
	setupTeamTest(t)
	os.MkdirAll(appDir("payments"), 0o700)
	saveTeam(testTeamConfig())
	saveTeamSecrets("payments", appSecrets{SlackBotToken: "xoxb-1", OpenRouterAPIKey: "sk-or-app"})
	now := time.Now().UTC()
	path := appCallsLogPath("payments", now)
	os.MkdirAll(filepath.Dir(path), 0o700)
	line := now.Format(time.RFC3339) + ` call {"who":"production","via":"openrouter","model":"m","in":1,"out":1,"usd":` + jsonNumber(usd) + `,"key":"app"}` + "\n"
	os.WriteFile(path, []byte(line), 0o600)
	appSumModelCalls("payments")
}

func jsonNumber(f float64) string { data, _ := json.Marshal(f); return string(data) }

// A budget: 80% told once, 100% told and every paid call refused, a
// raised budget lets calls through and says so.
func TestModelBudget(t *testing.T) {
	budgetTestApp(t, 85)
	var said []string
	saved := appOpsSay
	t.Cleanup(func() { appOpsSay = saved })
	appOpsSay = func(config appConfig, text string) { said = append(said, text) }
	monthly := 100.0
	if text, err := appSetBudget("payments", &monthly, nil, "ana"); err != nil || !strings.Contains(text, "$100.00 a month") {
		t.Fatal(text, err)
	}
	appWriteBudget("payments", time.Now())
	if len(said) != 1 || !strings.Contains(said[0], "spent $85.00 of its $100.00") || !strings.Contains(said[0], "(85%)") {
		t.Fatal(said)
	}
	if appBudgetStop(appBudgetPath("payments"), time.Now()) != "" {
		t.Fatal("stopped at 85%")
	}
	// Spent: told, and the proxies refuse.
	lower := 80.0
	appSetBudget("payments", &lower, nil, "ana")
	if len(said) != 2 || !strings.Contains(said[1], "spent its $80.00") || !strings.Contains(said[1], "plan go on") {
		t.Fatal(said)
	}
	message := appBudgetStop(appBudgetPath("payments"), time.Now())
	if !strings.Contains(message, "calls that cost money stop") {
		t.Fatal(message)
	}
	// A state from last month stops nothing: a new month's is coming.
	if appBudgetStop(appBudgetPath("payments"), time.Now().AddDate(0, 1, 0)) != "" {
		t.Fatal("last month's budget stopped this month's calls")
	}
	// Raised: through again, told once.
	higher := 200.0
	appSetBudget("payments", &higher, nil, "ana")
	appWriteBudget("payments", time.Now())
	if last := said[len(said)-1]; !strings.Contains(last, "go through again") || strings.Count(strings.Join(said, "\n"), "go through again") != 1 {
		t.Fatal(said)
	}
	// A daily budget over the monthly is refused; off clears it.
	daily := 500.0
	if _, err := appSetBudget("payments", nil, &daily, "ana"); err == nil {
		t.Fatal("a daily budget over the monthly")
	}
	var out bytes.Buffer
	if err := appBudgetCommand([]string{"payments", "--monthly", "off"}, &out); err != nil || !strings.Contains(out.String(), "model budget: none") {
		t.Fatal(out.String(), err)
	}
	if config, _ := loadTeam("payments"); config.ModelBudget != nil {
		t.Fatal(config.ModelBudget)
	}
}

// At 100% every proxy refuses calls that cost money, in its API's shape;
// a model list and a plan's call aren't refused.
func TestModelBudgetRefused(t *testing.T) {
	budgetTestApp(t, 50)
	limit := 10.0
	appSetBudget("payments", &limit, nil, "ana")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"usage":{"cost":0.01}}`)
	}))
	defer upstream.Close()
	post := func(handler http.Handler, path string) (int, string) {
		server := httptest.NewServer(handler)
		defer server.Close()
		request, _ := http.NewRequest("POST", server.URL+path, strings.NewReader(`{"model":"m"}`))
		request.Header.Set("Authorization", "Bearer "+boxClaudePlacehold)
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		data, _ := io.ReadAll(response.Body)
		return response.StatusCode, string(data)
	}
	// The models socket.
	if status, body := post(newTeamModelsProxy("payments", upstream.URL, appSlotProduction), "/api/v1/chat/completions"); status != http.StatusPaymentRequired || !strings.Contains(body, `"insufficient_quota"`) || !strings.Contains(body, "spent its $10.00") {
		t.Fatal(status, body)
	}
	if status, _ := post(newTeamModelsProxy("payments", upstream.URL, appSlotProduction), "/api/v1/models"); status == http.StatusPaymentRequired {
		t.Fatal("a model list refused")
	}
	// A box's proxy: its OpenRouter injector, with the app's keys folder
	// where the proxy has it.
	keys := filepath.Join(t.TempDir(), "secrets", "team")
	os.MkdirAll(keys, 0o700)
	data, _ := os.ReadFile(appBudgetPath("payments"))
	os.WriteFile(filepath.Join(keys, appBudgetFile), data, 0o644)
	os.WriteFile(filepath.Join(keys, "openrouter.env"), []byte("OPENROUTER_API_KEY=sk-or-app\n"), 0o600)
	env := filepath.Join(keys, "openrouter.env")
	var logged bytes.Buffer
	gate := boxBudgetGate(newBoxOpenRouterInjector(env, upstream.URL, &boxNetworkLog{file: &logged}), boxBudgetPath(env), &boxNetworkLog{file: &logged})
	if status, body := post(gate, "/api/v1/chat/completions"); status != http.StatusPaymentRequired || !strings.Contains(body, "spent its $10.00") {
		t.Fatal(status, body)
	}
	if !strings.Contains(logged.String(), "budget-refused /api/v1/chat/completions") {
		t.Fatal(logged.String())
	}
	// The fallback to OpenRouter answers Claude Code in Anthropic's shape.
	fallback := &boxFallback{kind: "claude", keys: env, log: &boxNetworkLog{file: &logged}, upstream: upstream.URL}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(`{"model":"claude-opus-5-5"}`))
	fallback.serve(recorder, request, []byte(`{"model":"claude-opus-5-5"}`), nil)
	if recorder.Code != http.StatusPaymentRequired || !strings.Contains(recorder.Body.String(), `"billing_error"`) {
		t.Fatal(recorder.Code, recorder.Body.String())
	}
	// Claude Code's own calls, on a plan, go on.
	claude := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"model":"claude-opus-5-5","usage":{"input_tokens":3,"output_tokens":1}}`)
	}))
	defer claude.Close()
	secret := filepath.Join(t.TempDir(), "token")
	os.WriteFile(secret, []byte("sk-ant-oat01-plan\n"), 0o600)
	if status, _ := post(newBoxClaudeInjector(secret, claude.URL, &boxNetworkLog{file: &logged}), "/v1/messages"); status != http.StatusOK {
		t.Fatal("a plan's call refused:", status)
	}
	if !strings.Contains(logged.String(), `"via":"anthropic","model":"claude-opus-5-5","in":3,"out":1,"key":"plan"`) {
		t.Fatal(logged.String())
	}
}
