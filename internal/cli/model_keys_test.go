package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// fakeOpenRouterKeys is OpenRouter's key API for a management key.
type fakeOpenRouterKeys struct {
	mu    sync.Mutex
	calls []string
	keys  map[string]map[string]any
	n     int
}

func newFakeOpenRouterKeys(t *testing.T) *fakeOpenRouterKeys {
	f := &fakeOpenRouterKeys{keys: map[string]map[string]any{}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer mgmt-key" {
			w.WriteHeader(http.StatusUnauthorized)
			io.WriteString(w, `{"error":{"message":"not a management key"}}`)
			return
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		path := strings.TrimPrefix(r.URL.Path, "/api/v1")
		data, _ := json.Marshal(body)
		f.calls = append(f.calls, r.Method+" "+path+" "+string(data))
		hash := strings.TrimPrefix(path, "/keys/")
		switch {
		case r.Method == "GET" && path == "/keys":
			io.WriteString(w, `{"data":[]}`)
		case r.Method == "POST" && path == "/keys":
			f.n++
			hash := "h" + strconv.Itoa(f.n)
			body["hash"], body["usage_monthly"] = hash, 3.5
			f.keys[hash] = body
			json.NewEncoder(w).Encode(map[string]any{"data": body, "key": "sk-or-v1-made-" + strconv.Itoa(f.n)})
		case r.Method == "PATCH":
			for k, v := range body {
				f.keys[hash][k] = v
			}
			json.NewEncoder(w).Encode(map[string]any{"data": f.keys[hash]})
		case r.Method == "DELETE":
			delete(f.keys, hash)
			io.WriteString(w, `{"deleted":true}`)
		case r.Method == "GET":
			json.NewEncoder(w).Encode(map[string]any{"data": f.keys[hash]})
		}
	}))
	t.Cleanup(server.Close)
	saved := appOpenRouterUpstream
	t.Cleanup(func() { appOpenRouterUpstream = saved })
	appOpenRouterUpstream = server.URL
	return f
}

func (f *fakeOpenRouterKeys) log() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return strings.Join(f.calls, "\n")
}

// Keys hi makes: the management key checked and kept in the vault; a
// key per app with its budget as the limit, the limit following the
// budget, rotated with the old one deleted; disabled, enabled, deleted;
// back to the machine's key deletes it; never the management key in a
// box's reach.
func TestOpenRouterManagedKeys(t *testing.T) {
	setupTeamTest(t)
	vaultTest(t)
	initVault(vaultSlotFile)
	vaultSharedTestApp(t, "payments")
	f := newFakeOpenRouterKeys(t)
	var out bytes.Buffer
	if err := appKey([]string{"payments", "openrouter", "--managed"}, strings.NewReader(""), &out); err == nil || !strings.Contains(err.Error(), "no OpenRouter management key") {
		t.Fatal(err)
	}
	if err := openRouterCommand([]string{"--management-key"}, strings.NewReader("sk-or-v1-regular\n"), &out); err == nil {
		t.Fatal("took a regular key as a management key")
	}
	if err := openRouterCommand([]string{"--management-key"}, strings.NewReader("mgmt-key\n"), &out); err != nil {
		t.Fatal(err)
	}
	fifty := 50.0
	appSetBudget("payments", &fifty, nil, "ana")
	out.Reset()
	if err := appKey([]string{"payments", "openrouter", "--managed"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.log(), `POST /keys {"limit":50,"limit_reset":"monthly","name":"hi `) || !strings.Contains(out.String(), "$50.00 a month, its budget") {
		t.Fatal(f.log(), out.String())
	}
	config, _ := loadTeam("payments")
	secrets, _ := loadTeamSecrets("payments")
	if config.OpenRouterManaged != "h1" || secrets.OpenRouterAPIKey != "sk-or-v1-made-1" {
		t.Fatal(config.OpenRouterManaged, secrets.OpenRouterAPIKey)
	}
	// The budget changes the key's limit.
	hundred := 100.0
	appSetBudget("payments", &hundred, nil, "ana")
	if !strings.Contains(f.log(), `PATCH /keys/h1 {"limit":100,"limit_reset":"monthly"}`) {
		t.Fatal(f.log())
	}
	// Rotate: a new key in, the old one deleted.
	out.Reset()
	appKey([]string{"payments", "openrouter", "--rotate"}, strings.NewReader(""), &out)
	if config, _ := loadTeam("payments"); config.OpenRouterManaged != "h2" || !strings.Contains(f.log(), "DELETE /keys/h1") {
		t.Fatal(config.OpenRouterManaged, f.log())
	}
	if data, _ := os.ReadFile(filepath.Join(appKeysDir("payments"), "openrouter.env")); string(data) != "OPENROUTER_API_KEY=sk-or-v1-made-2\n" {
		t.Fatal(string(data))
	}
	// Removed, back, purged.
	config, _ = loadTeam("payments")
	if said := appManagedKeyState(config, "disable"); said != "its OpenRouter key is disabled" || !strings.Contains(f.log(), `PATCH /keys/h2 {"disabled":true}`) {
		t.Fatal(said, f.log())
	}
	if said := appManagedKeyState(config, "enable"); said != "its OpenRouter key is enabled again" {
		t.Fatal(said)
	}
	// What OpenRouter counts, beside hi's.
	out.Reset()
	openRouterCommand(nil, strings.NewReader(""), &out)
	if !strings.Contains(out.String(), "$3.50 this month of $100.00") {
		t.Fatal(out.String())
	}
	// Back to this machine's key: the one hi made is deleted.
	out.Reset()
	if err := appKey([]string{"payments", "openrouter", "--unset"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	if config, _ := loadTeam("payments"); config.OpenRouterManaged != "" || !strings.Contains(f.log(), "DELETE /keys/h2") {
		t.Fatal(config.OpenRouterManaged, f.log())
	}
	// The management key is in no file a box reads.
	filepath.Walk(appDir("payments"), func(path string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			if data, _ := os.ReadFile(path); bytes.Contains(data, []byte("mgmt-key")) {
				t.Errorf("the management key is in %s", path)
			}
		}
		return nil
	})
	if audit := vaultAuditText(t); strings.Contains(audit, "mgmt-key") || strings.Contains(audit, "sk-or-v1-made") {
		t.Fatal("a key in the audit log")
	}
}

// A new app made with openrouter: managed gets a key hi makes.
func TestAppNewWithManagedKey(t *testing.T) {
	dir := setupTeamTest(t)
	vaultTest(t)
	initVault(vaultSlotFile)
	f := newFakeOpenRouterKeys(t)
	store, _ := openVault()
	store.put(openRouterManagementPath, vaultData{Values: map[string]string{"management": "mgmt-key"}}, "ana")
	t.Setenv("HI_APP_SLACK_BOT_TOKEN", "xoxb-bot")
	t.Setenv("HI_APP_SLACK_APP_TOKEN", "xapp-app")
	file := filepath.Join(dir, "answers.json")
	writeSkillTestFile(t, file, `{"purpose": "x", "channel": "C07PAYMENTS", "owners": ["U07OWNER1"], "empty": true, "openrouter": "managed", "model_budget": {"monthly": 30}}`, 0o600)
	var out bytes.Buffer
	if err := appNew([]string{"payments", "--from", file}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err, out.String())
	}
	config, _ := loadTeam("payments")
	if config.OpenRouterManaged == "" || !strings.Contains(f.log(), `"limit":30`) {
		t.Fatal(config.OpenRouterManaged, f.log())
	}
	if m := adminNewMachineOf("orders"); !m.Managed {
		t.Fatal("the page's wizard doesn't offer it")
	}
}
