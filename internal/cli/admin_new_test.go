package cli

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newAppTest is the admin page with Slack, OpenRouter, and the pickers
// faked: a new app goes through its steps as the terminal's wizard would.
func newAppTest(t *testing.T) (adminTest, *http.Cookie) {
	t.Helper()
	a := setupAdminTest(t)
	check, channels, people, look := appCheckOpenRouterKey, adminSlackChannels, adminSlackPeople, adminLookAt
	appCheckOpenRouterKey = func(key string) error {
		if key == "sk-or-bad" {
			return errors.New("OpenRouter doesn't know that key")
		}
		return nil
	}
	adminSlackChannels = func(string) ([]adminSlackChannel, error) {
		return []adminSlackChannel{{ID: "C07ORDERS", Name: "orders"}, {ID: "C07OPS", Name: "orders-ops", Private: true}}, nil
	}
	adminSlackPeople = func(string) ([]adminSlackPerson, error) {
		return []adminSlackPerson{{ID: "U07OWNER1", Name: "olga"}, {ID: "U07SAM", Name: "sam"}}, nil
	}
	adminLookAt = func(source string) *adminAdoptLook {
		return &adminAdoptLook{Source: source, GitHub: "ada/pocket-kanban", Description: "A kanban board in your pocket", SizeMB: 3}
	}
	t.Cleanup(func() {
		appCheckOpenRouterKey, adminSlackChannels, adminSlackPeople, adminLookAt = check, channels, people, look
	})
	return a, a.signIn(t, "Olga")
}

func TestAdminNewAppThroughItsSteps(t *testing.T) {
	a, olga := newAppTest(t)
	var view adminDraftView
	var failed map[string]any
	if code := a.post(t, olga, "/api/new", map[string]any{"action": "start", "name": "payments"}, &failed); code != http.StatusBadRequest {
		t.Fatalf("an app that exists: %d", code)
	}
	if code := a.post(t, olga, "/api/new", map[string]any{"action": "start", "name": "Orders!"}, &failed); code != http.StatusBadRequest {
		t.Fatalf("a bad name: %d", code)
	}
	if code := a.post(t, olga, "/api/new", map[string]any{"action": "start", "name": "orders"}, &view); code != http.StatusOK {
		t.Fatalf("start: %d %+v", code, view)
	}
	// Its folder goes beside the machine's other apps'.
	if view.Folder != filepath.Join(filepath.Dir(appDir("payments")), "app-orders") || view.Handle == "" || !strings.Contains(view.ManifestURL, "manifest_json=") {
		t.Fatalf("draft: %+v", view)
	}
	step := func(answers map[string]any) (int, string) {
		t.Helper()
		answers["action"], answers["name"] = "step", "orders"
		var out map[string]any
		code := a.post(t, olga, "/api/new", answers, &out)
		if code != http.StatusOK {
			return code, out["error"].(string)
		}
		return code, ""
	}
	for _, s := range []map[string]any{
		{"step": "lead", "lead_name": "Ada"},
		{"step": "purpose", "purpose": "Take the orders"},
	} {
		if code, err := step(s); code != http.StatusOK {
			t.Fatalf("%s: %s", s["step"], err)
		}
	}
	if code, err := step(map[string]any{"step": "slack", "bot_token": "nope", "app_token": "xapp-1"}); code == http.StatusOK || !strings.Contains(err, "xoxb-") {
		t.Fatalf("a bad bot token: %d %s", code, err)
	}
	if code, err := step(map[string]any{"step": "slack", "bot_token": "xoxb-orders", "app_token": "xapp-orders"}); code != http.StatusOK {
		t.Fatal(err)
	}
	// The pickers read Slack with the draft's bot token.
	var channels []adminSlackChannel
	if code := a.get(t, olga, "/api/new/slack?name=orders&what=channels", &channels); code != http.StatusOK || len(channels) != 2 {
		t.Fatalf("channels: %d %+v", code, channels)
	}
	if code, err := step(map[string]any{"step": "channel", "channel": "C07ORDERS", "ops_channel": "C07ORDERS"}); code == http.StatusOK || !strings.Contains(err, "main channel") {
		t.Fatalf("the same channel twice: %d %s", code, err)
	}
	for _, s := range []map[string]any{
		{"step": "channel", "channel": "C07ORDERS", "ops_channel": "C07OPS"},
		{"step": "people", "owners": []string{"U07OWNER1", "U07SAM"}},
		{"step": "members", "members": map[string]any{
			"lead":     map[string]any{"agent": "hermes", "model": "openai/gpt-6.1-sol", "effort": "high"},
			"coder":    map[string]any{"agent": "claude", "model": "opus"},
			"reviewer": map[string]any{"agent": "codex"},
		}, "claude_token": "sk-ant-oat-orders"},
	} {
		if code, err := step(s); code != http.StatusOK {
			t.Fatalf("%s: %s", s["step"], err)
		}
	}
	if code, err := step(map[string]any{"step": "models", "own_key": true, "openrouter_key": "sk-or-bad"}); code == http.StatusOK || !strings.Contains(err, "doesn't know") {
		t.Fatalf("a bad key: %d %s", code, err)
	}
	for _, s := range []map[string]any{
		{"step": "models", "own_key": true, "openrouter_key": "sk-or-orders"},
		{"step": "code", "code": map[string]any{"kind": "adopt", "source": "github.com/ada/pocket-kanban", "ref": "main"}},
		{"step": "network", "netbird_key": "nb-setup-key", "addresses": "netbird"},
		{"step": "heartbeats"},
	} {
		if code, err := step(s); code != http.StatusOK {
			t.Fatalf("%s: %s", s["step"], err)
		}
	}
	// What the page sees holds no secret.
	request, _ := http.NewRequest("GET", a.server.URL+"/api/new/draft?name=orders", nil)
	request.AddCookie(olga)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	for _, secret := range []string{"xoxb-orders", "xapp-orders", "sk-or-orders", "sk-ant-oat-orders", "nb-setup-key"} {
		if strings.Contains(string(body), secret) {
			t.Fatalf("the page got %s: %s", secret, body)
		}
	}
	json.Unmarshal(body, &view)
	if !view.HasBot || !view.HasOwnKey || !view.HasClaude || !view.HasNetBird || view.Config.Members["lead"].Name != "Ada" || view.Code.Kind != "adopt" {
		t.Fatalf("view: %+v", view)
	}

	// Create: the answers as --from takes them, the secrets in a file only
	// the job reads, and hi app up after.
	var job adminJob
	if code := a.post(t, olga, "/api/new", map[string]any{"action": "create", "name": "orders"}, &job); code != http.StatusOK {
		t.Fatalf("create: %d %+v", code, job)
	}
	if len(*a.jobs) != 1 {
		t.Fatalf("jobs: %+v", *a.jobs)
	}
	launched := (*a.jobs)[0]
	if launched.Args[0] != "app" || launched.Args[1] != "new" || launched.Args[2] != "orders" || launched.Args[4] != view.Folder || len(launched.Then) != 1 || launched.Then[0][1] != "up" || launched.Draft != "orders" {
		t.Fatalf("job: %+v", launched)
	}
	var answers appAnswers
	data, _ := os.ReadFile(launched.Args[6])
	if err := json.Unmarshal(data, &answers); err != nil || answers.Adopt != "github.com/ada/pocket-kanban" || answers.Channel != "C07ORDERS" || answers.NetBirdKey != "nb-setup-key" || len(answers.Owners) != 2 {
		t.Fatalf("answers: %v %+v", err, answers)
	}
	if info, _ := os.Stat(launched.Env); info == nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("the secrets' file: %+v", info)
	}
	var env map[string]string
	data, _ = os.ReadFile(launched.Env)
	json.Unmarshal(data, &env)
	if env["HI_APP_SLACK_BOT_TOKEN"] != "xoxb-orders" || env["HI_APP_OPENROUTER_API_KEY"] != "sk-or-orders" || env["HI_APP_CLAUDE_TOKEN"] != "sk-ant-oat-orders" {
		t.Fatalf("env: %v", env)
	}
	// hi app new --from reads them, with the secrets as the runner gives them.
	for name, value := range env {
		t.Setenv(name, value)
	}
	fromFile, secrets, err := appAnswersFromFile("orders", launched.Args[6])
	if err != nil || fromFile.Adopt != answers.Adopt || secrets.SlackBotToken != "xoxb-orders" || secrets.ClaudeToken != "sk-ant-oat-orders" || fromFile.Members["lead"].Name != "Ada" {
		t.Fatalf("--from: %v %+v %+v", err, fromFile, secrets)
	}
	// The runner reads the secrets once, runs new then up, and drops the
	// draft and the files once it went well.
	var ran [][]string
	var gotEnv []string
	run := adminRunCommand
	adminRunCommand = func(_ string, args, env []string, _ io.Writer) error {
		ran = append(ran, args)
		gotEnv = env
		return nil
	}
	t.Cleanup(func() { adminRunCommand = run })
	saveAdminJob(launched)
	if code := adminJobMain([]string{launched.ID}, io.Discard); code != 0 {
		t.Fatal(code)
	}
	if len(ran) != 2 || ran[1][1] != "up" || !strings.Contains(strings.Join(gotEnv, " "), "HI_APP_SLACK_APP_TOKEN=xapp-orders") {
		t.Fatalf("ran %v", ran)
	}
	for _, path := range append(launched.Remove, launched.Env, adminDraftPath("orders")) {
		if fileExists(path) {
			t.Errorf("%s is still there", path)
		}
	}
}

// Bring one back, Rename, and Remove run their hi app commands as jobs.
func TestAdminBringBackRenameRemove(t *testing.T) {
	a, olga := newAppTest(t)
	folder := filepath.Join(filepath.Dir(appDir("payments")), "app-ledger")
	os.MkdirAll(folder, 0o700)
	os.WriteFile(filepath.Join(folder, appConfigFile), []byte(`{"name": "ledger", "purpose": "The books"}`), 0o600)
	var home struct {
		Back []adminBack `json:"back"`
	}
	a.get(t, olga, "/api/new", &home)
	if len(home.Back) != 1 || home.Back[0].Name != "ledger" || home.Back[0].Folder != folder {
		t.Fatalf("back: %+v", home.Back)
	}
	var job adminJob
	if code := a.post(t, olga, "/api/apps", map[string]any{"action": "back", "folder": folder}, &job); code != http.StatusOK || strings.Join(job.Args, " ") != "app up "+folder {
		t.Fatalf("back: %d %+v", code, job)
	}
	if code := a.post(t, olga, "/api/apps", map[string]any{"action": "rename", "app": "payments", "to": "admin"}, &job); code != http.StatusBadRequest {
		t.Fatalf("renamed to admin: %d", code)
	}
	if code := a.post(t, olga, "/api/apps", map[string]any{"action": "rename", "app": "payments", "to": "billing"}, &job); code != http.StatusOK || strings.Join(job.Args, " ") != "app rename payments billing --yes" {
		t.Fatalf("rename: %d %+v", code, job)
	}
	if code := a.post(t, olga, "/api/apps", map[string]any{"action": "remove", "app": "payments", "keep_folder": true}, &job); code != http.StatusOK || strings.Join(job.Args, " ") != "app rm payments --yes --keep-folder" {
		t.Fatalf("remove: %d %+v", code, job)
	}
	a.mu.Lock()
	posts := strings.Join(*a.posts, "\n")
	a.mu.Unlock()
	if !strings.Contains(posts, "Olga is renaming the app to billing") || !strings.Contains(posts, "Olga is removing the app from this machine; its folder stays") {
		t.Fatalf("posts: %s", posts)
	}
}
