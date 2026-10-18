package main

import (
	"bytes"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func newTeamKeysTest(t *testing.T) {
	t.Helper()
	setupTeamTest(t)
	saved := teamCheckOpenRouterKey
	teamCheckOpenRouterKey = func(key string) error {
		if key == "sk-or-bad" {
			return io.ErrUnexpectedEOF
		}
		return nil
	}
	t.Cleanup(func() { teamCheckOpenRouterKey = saved })
	file := filepath.Join(t.TempDir(), "team.json")
	writeSkillTestFile(t, file, `{"purpose": "x", "channel": "C07PAYMENTS", "owners": ["U07OWNER1"]}`, 0o600)
	t.Setenv("HI_TEAM_SLACK_BOT_TOKEN", "xoxb-bot")
	t.Setenv("HI_TEAM_SLACK_APP_TOKEN", "xapp-app")
	t.Setenv("HI_TEAM_OPENROUTER_API_KEY", "sk-or-team")
	if err := teamNew([]string{"payments", "--from", file}, strings.NewReader(""), io.Discard); err != nil {
		t.Fatal(err)
	}
}

func TestTeamKey(t *testing.T) {
	newTeamKeysTest(t)
	if secrets, _ := loadTeamSecrets("payments"); secrets.OpenRouterAPIKey != "sk-or-team" {
		t.Fatalf("hi team new --from keeps the team's key: %+v", secrets)
	}
	if key, _ := teamOpenRouterKey("payments"); key != "sk-or-team" {
		t.Fatal(key)
	}
	keys := filepath.Join(teamKeysDir("payments"), "openrouter.env")

	// Back to this machine's key.
	var out bytes.Buffer
	if err := teamKey([]string{"payments", "openrouter", "--unset"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	if key, _ := teamOpenRouterKey("payments"); key != "sk-or-real" || fileExists(keys) {
		t.Fatal(key, fileExists(keys))
	}
	// A key OpenRouter refuses isn't kept.
	if err := teamKey([]string{"payments", "openrouter"}, strings.NewReader("sk-or-bad\n"), &out); err == nil {
		t.Fatal("a refused key was kept")
	}
	if err := teamKey([]string{"payments", "openrouter"}, strings.NewReader("sk-or-new\n"), &out); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(keys)
	if string(data) != "OPENROUTER_API_KEY=sk-or-new\n" || strings.Contains(out.String(), "sk-or-new") {
		t.Fatalf("%q %q", data, out.String())
	}
	if err := teamKey([]string{"payments", "anthropic"}, strings.NewReader(""), &out); err == nil {
		t.Fatal("only openrouter")
	}
}

func TestTeamKeySlack(t *testing.T) {
	newTeamKeysTest(t)
	saved := teamSystemctl
	teamSystemctl = func(args ...string) (string, error) { return "inactive", nil }
	defer func() { teamSystemctl = saved }()
	before, _ := loadTeamSecrets("payments")
	var out bytes.Buffer
	// A token that isn't a bot token isn't taken.
	if err := teamKey([]string{"payments", "slack"}, strings.NewReader("xapp-wrong\n"), &out); err == nil || !strings.Contains(err.Error(), "starts with xoxb-") {
		t.Fatal(err)
	}
	// Enter keeps the app token.
	if err := teamKey([]string{"payments", "slack"}, strings.NewReader("xoxb-new-bot\n\n"), &out); err != nil {
		t.Fatal(err, out.String())
	}
	after, _ := loadTeamSecrets("payments")
	if after.SlackBotToken != "xoxb-new-bot" || after.SlackAppToken != before.SlackAppToken ||
		!strings.Contains(out.String(), "hi team up payments") || strings.Contains(out.String(), "xoxb-new-bot") {
		t.Fatalf("%+v %q", after, out.String())
	}
	out.Reset()
	if err := teamKey([]string{"payments", "slack"}, strings.NewReader("\n\n"), &out); err != nil || !strings.Contains(out.String(), "Nothing changed") {
		t.Fatal(err, out.String())
	}
	// Slack refusing it, in the end, changes nothing.
	saved2 := teamSlack
	teamSlack = fakeTeamSlack{badBot: true}
	defer func() { teamSlack = saved2 }()
	if err := teamKey([]string{"payments", "slack"}, strings.NewReader("xoxb-refused\n"), &out); err == nil {
		t.Fatal("a refused token was taken")
	}
	if kept, _ := loadTeamSecrets("payments"); kept.SlackBotToken != "xoxb-new-bot" {
		t.Fatal(kept.SlackBotToken)
	}
}

func TestTeamEnv(t *testing.T) {
	newTeamKeysTest(t)
	var out bytes.Buffer
	if err := teamEnv([]string{"payments"}, strings.NewReader(""), &out); err != nil || !strings.Contains(out.String(), "no settings") {
		t.Fatal(err, out.String())
	}
	before := teamSettingsStamp("payments")
	// KEY alone asks for the value.
	if err := teamEnv([]string{"payments", "WEATHER_API_KEY=w-123", "MAPS_KEY"}, strings.NewReader("m-456\n"), &out); err != nil {
		t.Fatal(err)
	}
	secrets, _ := loadTeamSecrets("payments")
	if secrets.Env["WEATHER_API_KEY"] != "w-123" || secrets.Env["MAPS_KEY"] != "m-456" || secrets.SlackBotToken != "xoxb-bot" {
		t.Fatalf("%+v", secrets)
	}
	if teamSettingsStamp("payments") == before {
		t.Fatal("new settings don't change the stamp, so production wouldn't get them")
	}
	// The list has names, never values.
	out.Reset()
	teamEnv([]string{"payments"}, strings.NewReader(""), &out)
	if !strings.Contains(out.String(), "MAPS_KEY, WEATHER_API_KEY") || strings.Contains(out.String(), "w-123") {
		t.Fatal(out.String())
	}
	for _, bad := range []string{"PORT=1", "OPENROUTER_API_KEY=x", "1X=y", "A-B=c", "MULTI=a\nb"} {
		if err := teamEnv([]string{"payments", bad}, strings.NewReader(""), &out); err == nil {
			t.Errorf("%q was taken", bad)
		}
	}
	if err := teamEnv([]string{"payments", "--unset", "MAPS_KEY"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	if err := teamEnv([]string{"payments", "--unset", "MAPS_KEY"}, strings.NewReader(""), &out); err == nil {
		t.Fatal("removing a setting that isn't there")
	}

	// The app's box gets the settings, the models, and its address, with
	// the placeholder for a key; the file has them and nobody else can read it.
	env := teamAppEnv("payments", teamSlotPreview, 8080)
	if env["WEATHER_API_KEY"] != "w-123" || env["MAPS_KEY"] != "" || env["PORT"] != "8080" || env["OPENROUTER_API_KEY"] != boxClaudePlacehold ||
		env["OPENROUTER_BASE_URL"] != "http://127.0.0.1:3129/api/v1" || env["HI_PUBLIC_URL_FILE"] != teamAppAddressFile {
		t.Fatalf("%v", env)
	}
	for _, value := range env {
		if strings.Contains(value, "sk-or") {
			t.Fatalf("the app's box gets the key: %v", env)
		}
	}
	file, err := writeTeamAppEnvFile("payments", env)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(file)
	info, _ := os.Stat(file)
	data, _ := os.ReadFile(file)
	if info.Mode().Perm() != 0o600 || !strings.Contains(string(data), "WEATHER_API_KEY=w-123\n") {
		t.Fatal(info.Mode(), string(data))
	}
}

func TestTeamModelsProxy(t *testing.T) {
	newTeamKeysTest(t)
	var seen []string
	openrouter := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.URL.Path+" "+r.Header.Get("Authorization"))
		io.WriteString(w, "ok")
	}))
	defer openrouter.Close()
	proxy := httptest.NewServer(newTeamModelsProxy("payments", openrouter.URL))
	defer proxy.Close()
	ask := func(path string) int {
		request, _ := http.NewRequest("POST", proxy.URL+path, strings.NewReader("{}"))
		request.Header.Set("Authorization", "Bearer "+boxClaudePlacehold)
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		return response.StatusCode
	}
	if ask("/api/v1/chat/completions") != 200 {
		t.Fatal(seen)
	}
	// The machine's key once the team's is removed, without a restart.
	teamKey([]string{"payments", "openrouter", "--unset"}, strings.NewReader(""), io.Discard)
	ask("/api/v1/images")
	if ask("/elsewhere") != http.StatusNotFound {
		t.Fatal("only OpenRouter's API")
	}
	if strings.Join(seen, "\n") != "/api/v1/chat/completions Bearer sk-or-team\n/api/v1/images Bearer sk-or-real" {
		t.Fatal(seen)
	}
}

func TestBoxOpenRouterInjectorTeamKeyFirst(t *testing.T) {
	dir := t.TempDir()
	team, hermes := filepath.Join(dir, "team.env"), filepath.Join(dir, "hermes.env")
	writeSkillTestFile(t, hermes, "OPENROUTER_API_KEY=sk-or-machine\n", 0o600)
	var keys []string
	openrouter := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		keys = append(keys, r.Header.Get("Authorization"))
	}))
	defer openrouter.Close()
	var logged bytes.Buffer
	injector := httptest.NewServer(newBoxOpenRouterInjector(team+","+hermes, openrouter.URL, &boxNetworkLog{file: &logged}))
	defer injector.Close()
	call := func() {
		request, _ := http.NewRequest("POST", injector.URL+"/api/v1/chat/completions", nil)
		request.Header.Set("Authorization", "Bearer "+boxClaudePlacehold)
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
	}
	call()
	writeSkillTestFile(t, team, "OPENROUTER_API_KEY=sk-or-team\n", 0o600)
	call()
	if strings.Join(keys, ",") != "Bearer sk-or-machine,Bearer sk-or-team" {
		t.Fatal(keys)
	}
}

func TestRunTeamAppRelaysModels(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "models.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "models "+r.URL.Path) })}
	go server.Serve(listener)
	defer server.Close()
	free, _ := net.Listen("tcp", "127.0.0.1:0")
	port := strconv.Itoa(free.Addr().(*net.TCPAddr).Port)
	free.Close()

	done := make(chan int, 1)
	go func() { done <- runTeamApp([]string{socket, port, "sleep 3; exit 3"}, io.Discard) }()
	var body []byte
	for start := time.Now(); time.Since(start) < 2*time.Second; time.Sleep(50 * time.Millisecond) {
		response, err := http.Get("http://127.0.0.1:" + port + "/api/v1/models")
		if err == nil {
			body, _ = io.ReadAll(response.Body)
			response.Body.Close()
			break
		}
	}
	if string(body) != "models /api/v1/models" {
		t.Fatalf("%q", body)
	}
	if code := <-done; code != 3 {
		t.Fatal("the app's exit status:", code)
	}
}
