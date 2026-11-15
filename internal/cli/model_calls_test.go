package cli

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"testing/iotest"
	"time"
)

// readUsage passes body through the cost reader a few bytes at a time,
// as a slow stream comes, and gives the call it read.
func readUsage(t *testing.T, body string) modelCall {
	t.Helper()
	var got modelCall
	called := false
	reader := &appCostReader{body: io.NopCloser(iotest.HalfReader(strings.NewReader(body))), call: func(c modelCall) { got, called = c, true },
		seed: modelCall{Who: "lead", Via: "openrouter", Key: "app", KFP: "aaf58e45"}}
	io.Copy(io.Discard, reader)
	reader.Close()
	if !called {
		t.Fatal("no call read from", body)
	}
	return got
}

// The model, tokens, and cost from each API's answer, streamed or not,
// and never a "cost" in the model's own words.
func TestModelUsageShapes(t *testing.T) {
	openrouter := readUsage(t, `{"id":"gen-1","model":"anthropic/claude-sonnet-4.6","choices":[{"message":{"content":"say \"cost\": 99 and \"usage\""}}],`+
		`"usage":{"prompt_tokens":100,"completion_tokens":20,"prompt_tokens_details":{"cached_tokens":60},"completion_tokens_details":{"reasoning_tokens":5},"cost":0.01}}`)
	if openrouter != (modelCall{Who: "lead", Via: "openrouter", Model: "anthropic/claude-sonnet-4.6", In: 100, Out: 20, Cached: 60, Reasoning: 5, USD: 0.01, Key: "app", KFP: "aaf58e45", ID: "gen-1"}) {
		t.Fatalf("%+v", openrouter)
	}
	stream := readUsage(t, "data: {\"id\":\"gen-2\",\"model\":\"openai/gpt-6.1\",\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n"+
		"data: {\"id\":\"gen-2\",\"model\":\"openai/gpt-6.1\",\"choices\":[],\"usage\":{\"prompt_tokens\":40,\"completion_tokens\":3,\"cost\":0.002}}\n\ndata: [DONE]\n\n")
	if stream.Model != "openai/gpt-6.1" || stream.In != 40 || stream.Out != 3 || stream.USD != 0.002 || stream.ID != "gen-2" {
		t.Fatalf("%+v", stream)
	}
	anthropic := readUsage(t, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"model\":\"claude-opus-5-5\",\"usage\":{\"input_tokens\":10,\"cache_read_input_tokens\":90,\"output_tokens\":1}}}\n\n"+
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"text\":\"the usage is \\\"cost\\\"\"}}\n\n"+
		"event: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":42}}\n\n")
	if anthropic.Model != "claude-opus-5-5" || anthropic.In != 100 || anthropic.Cached != 90 || anthropic.Out != 42 || anthropic.USD != 0 {
		t.Fatalf("%+v", anthropic)
	}
	responses := readUsage(t, "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"model\":\"gpt-6.1-codex\"}}\n\n"+
		"event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"model\":\"gpt-6.1-codex\",\"usage\":{\"input_tokens\":50,\"output_tokens\":7,"+
		"\"input_tokens_details\":{\"cached_tokens\":10},\"output_tokens_details\":{\"reasoning_tokens\":3}}}}\n\n")
	if responses.Model != "gpt-6.1-codex" || responses.In != 50 || responses.Out != 7 || responses.Cached != 10 || responses.Reasoning != 3 {
		t.Fatalf("%+v", responses)
	}
	// An answer with no usage (a model list) is no call.
	reader := &appCostReader{body: io.NopCloser(strings.NewReader(`{"data":[{"id":"m"}]}`)), call: func(modelCall) { t.Fatal("a call from a model list") }}
	io.Copy(io.Discard, reader)
	reader.Close()
}

// The app's service sums every source's call lines once: the models
// socket's, the lead's network log, a member's and the dev box's events,
// and an older proxy's spend lines; a plan's calls apart.
func TestModelCallSums(t *testing.T) {
	setupTeamTest(t)
	os.MkdirAll(appDir("payments"), 0o700)
	saveTeam(testTeamConfig())
	now := time.Now().UTC()
	at := now.Format(time.RFC3339)
	write := func(path string, lines ...string) {
		os.MkdirAll(filepath.Dir(path), 0o700)
		file, _ := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		for _, line := range lines {
			file.WriteString(at + " " + line + "\n")
		}
		file.Close()
	}
	write(appCallsLogPath("payments", now), `call {"who":"production","via":"openrouter","model":"m1","in":100,"out":10,"usd":0.5,"key":"app","kfp":"aaf58e45"}`)
	write(appNetworkLogPath("payments"), "allow api.slack.com", `call {"who":"lead","via":"openrouter","model":"m2","in":1000,"out":50,"usd":1.25,"key":"app","kfp":"aaf58e45"}`, "spend 0.25")
	write(filepath.Join(appEventsDir("payments"), "coder.log"), `call {"who":"coder","via":"anthropic","model":"claude-opus-5-5","in":5000,"out":900,"key":"plan"}`,
		"fallback coder claude signin m3")
	write(filepath.Join(appEventsDir("payments"), appDevBox("payments")+".log"), "spend 0.1")
	if err := appSumModelCalls("payments"); err != nil {
		t.Fatal(err)
	}
	// Read again: nothing new, nothing counted twice; a new line is.
	appSumModelCalls("payments")
	write(appCallsLogPath("payments", now), `call {"who":"preview","via":"openrouter","model":"m1","in":10,"out":1,"usd":0.05,"key":"app","kfp":"aaf58e45"}`)
	appSumModelCalls("payments")

	spend := appModelSpend("payments", now.Format("2006-01"))
	if diff := spend.USD - (0.5 + 1.25 + 0.25 + 0.1 + 0.05); diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("%.4f %+v", spend.USD, spend)
	}
	who := map[string]float64{}
	for _, row := range spend.ByWho {
		who[row.Name] = row.USD
	}
	if who["lead"] != 1.5 || who["production"] != 0.5 || who["dev"] != 0.1 || who["preview"] != 0.05 || who["coder"] != 0 {
		t.Fatal(who)
	}
	if len(spend.Plans) != 1 || spend.Plans[0].Name != "coder (anthropic)" || spend.Plans[0].In != 5000 {
		t.Fatalf("%+v", spend.Plans)
	}
	if hours := loadModelHours("payments"); hours[now.Truncate(time.Hour).Format(time.RFC3339)]["aaf58e45"] != 1.8 {
		t.Fatal(hours)
	}
	if got := appMonthUSD("payments"); got < 2.149 || got > 2.151 {
		t.Fatal(got)
	}
	// The watcher counts the same calls.
	if got := appSpendSince("payments", now.Add(-time.Hour)); got < 2.149 || got > 2.151 {
		t.Fatal("the watcher:", got)
	}
	var out bytes.Buffer
	if err := appBudgetCommand([]string{"payments"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "$2.15 on models through hi") || !strings.Contains(out.String(), "On a plan") || !strings.Contains(out.String(), "coder (anthropic)") {
		t.Fatal(out.String())
	}
}

// The app's models socket writes a call line for each answer, with who
// (the slot), the key's fingerprint, and never the key.
func TestModelsSocketRecordsCalls(t *testing.T) {
	setupTeamTest(t)
	os.MkdirAll(appDir("payments"), 0o700)
	saveTeam(testTeamConfig())
	saveTeamSecrets("payments", appSecrets{SlackBotToken: "xoxb-1", OpenRouterAPIKey: "sk-or-app"})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"model":"m1","usage":{"prompt_tokens":12,"completion_tokens":3,"cost":0.004}}`)
	}))
	defer upstream.Close()
	proxy := httptest.NewServer(newTeamModelsProxy("payments", upstream.URL, appSlotProduction))
	defer proxy.Close()
	response, err := http.Post(proxy.URL+"/api/v1/chat/completions", "application/json", strings.NewReader(`{"model":"m1"}`))
	if err != nil {
		t.Fatal(err)
	}
	io.ReadAll(response.Body)
	response.Body.Close()
	data, _ := os.ReadFile(appCallsLogPath("payments", time.Now()))
	line := string(data)
	if !strings.Contains(line, `call {"who":"production","via":"openrouter","model":"m1","in":12,"out":3,"usd":0.004,"key":"app","kfp":"`+callKeyFingerprint("sk-or-app")+`"}`) || strings.Contains(line, "sk-or-app") {
		t.Fatal(line)
	}
}

// A shared key spending outside hi's apps: OpenRouter counts more on it
// in an hour than the apps here spent with it, by more than $1 and 10%;
// told once in six hours; equal isn't.
func TestSharedKeySpentOutside(t *testing.T) {
	setupTeamTest(t)
	vaultTest(t)
	initVault(vaultSlotFile)
	vaultSharedTestApp(t, "payments")
	vaultSharedSet("openrouter", "sk-or-shared", "ana")
	vaultSharedGive("openrouter", []string{"payments"}, false, "ana")
	config, _ := loadTeam("payments")
	config.OpenRouterFrom = "shared/openrouter"
	saveTeam(config)
	usage := 10.0
	saved := openRouterKeyUsage
	t.Cleanup(func() { openRouterKeyUsage = saved })
	openRouterKeyUsage = func(key string) (float64, error) {
		if key != "sk-or-shared" {
			t.Fatal("asked with another key")
		}
		return usage, nil
	}
	sharedKeyWatch.Lock()
	sharedKeyWatch.samples = map[string][]sharedKeySample{}
	sharedKeyWatch.Unlock()
	start := time.Now()
	if told := checkSharedKeys(start); len(told) != 0 {
		t.Fatal(told)
	}
	// An hour on: the apps spent $2 with it, and OpenRouter counted $2.
	hour := start.Add(30 * time.Minute).UTC().Truncate(time.Hour).Format(time.RFC3339)
	os.MkdirAll(appModelsDirOf("payments"), 0o700)
	writeHours := func(usd float64) {
		data := `{"` + hour + `":{"` + callKeyFingerprint("sk-or-shared") + `":` + strconv.FormatFloat(usd, 'f', -1, 64) + `}}`
		os.WriteFile(appSpendHoursPath("payments"), []byte(data), 0o600)
	}
	writeHours(2)
	usage = 12
	if told := checkSharedKeys(start.Add(time.Hour)); len(told) != 0 {
		t.Fatal("equal spend told:", told)
	}
	// OpenRouter counted $7 more; the apps $2: $5 outside.
	usage = 19
	told := checkSharedKeys(start.Add(time.Hour + time.Minute))
	if len(told) != 1 || !strings.Contains(told[0], "shared/openrouter spent $") || !strings.Contains(told[0], "outside hi's apps") || strings.Contains(told[0], "sk-or") {
		t.Fatal(told)
	}
	if again := checkSharedKeys(start.Add(time.Hour + 2*time.Minute)); len(again) != 0 {
		t.Fatal("told twice:", again)
	}
	rows, _ := vaultSharedRows()
	if len(rows) != 1 || rows[0].Outside == nil {
		t.Fatalf("%+v", rows)
	}
}
