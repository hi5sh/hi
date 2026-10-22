package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
)

// fakeOpenRouterKeys is OpenRouter's provisioning API for a test.
type fakeOpenRouterKeys struct {
	mu    sync.Mutex
	made  int
	keys  map[string]*openRouterKey
	calls []string
}

func (f *fakeOpenRouterKeys) start(t *testing.T) {
	f.keys = map[string]*openRouterKey{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer sk-or-prov" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		hash := strings.TrimPrefix(r.URL.Path, "/")
		f.calls = append(f.calls, r.Method+" "+r.URL.Path)
		switch {
		case r.Method == http.MethodGet && hash == "":
			json.NewEncoder(w).Encode(map[string]any{"data": []any{}})
		case r.Method == http.MethodPost:
			var in struct {
				Name string `json:"name"`
			}
			json.NewDecoder(r.Body).Decode(&in)
			f.made++
			key := &openRouterKey{Hash: "hash" + string(rune('0'+f.made)), Name: in.Name}
			f.keys[key.Hash] = key
			json.NewEncoder(w).Encode(map[string]any{"data": key, "key": "sk-or-team-" + string(rune('0'+f.made))})
		case r.Method == http.MethodPatch:
			var in struct {
				Disabled bool `json:"disabled"`
			}
			json.NewDecoder(r.Body).Decode(&in)
			f.keys[hash].Disabled = in.Disabled
			json.NewEncoder(w).Encode(map[string]any{"data": f.keys[hash]})
		case r.Method == http.MethodDelete:
			delete(f.keys, hash)
		}
	}))
	t.Cleanup(server.Close)
	saved := openRouterKeysAPI
	openRouterKeysAPI = server.URL
	t.Cleanup(func() { openRouterKeysAPI = saved })
}

// fakeNetBird is NetBird's management API for a test: one peer in two
// groups, and a policy that lets All in.
type fakeNetBird struct {
	mu       sync.Mutex
	approval bool
	peer     netbirdPeer
	groups   map[string][]string // group → peer IDs
	calls    []string
}

func (f *fakeNetBird) start(t *testing.T) string {
	f.peer = netbirdPeer{ID: "p1", Name: "aiw9", IP: "127.0.0.1"}
	f.peer.Groups = append(f.peer.Groups, struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}{"gAll", "All"}, struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}{"gTeams", "teams"})
	f.groups = map[string][]string{"gTeams": {"p1", "p2"}, "gAll": {"p1", "p2", "p3"}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.calls = append(f.calls, r.Method+" "+r.URL.Path)
		switch {
		case r.URL.Path == "/api/peers" && r.URL.Query().Get("ip") == "127.0.0.1":
			json.NewEncoder(w).Encode([]netbirdPeer{f.peer})
		case r.URL.Path == "/api/peers":
			json.NewEncoder(w).Encode([]netbirdPeer{})
		case r.URL.Path == "/api/peers/p1" && r.Method == http.MethodGet:
			json.NewEncoder(w).Encode(f.peer)
		case r.URL.Path == "/api/peers/p1" && r.Method == http.MethodPut:
			var in map[string]any
			json.NewDecoder(r.Body).Decode(&in)
			f.peer.ApprovalRequired = in["approval_required"] == true
		case r.URL.Path == "/api/accounts":
			io.WriteString(w, `[{"settings": {"extra": {"peer_approval_enabled": `+map[bool]string{true: "true", false: "false"}[f.approval]+`}}}]`)
		case strings.HasPrefix(r.URL.Path, "/api/groups/") && r.Method == http.MethodGet:
			id := strings.TrimPrefix(r.URL.Path, "/api/groups/")
			var peers []map[string]string
			for _, p := range f.groups[id] {
				peers = append(peers, map[string]string{"id": p})
			}
			json.NewEncoder(w).Encode(map[string]any{"id": id, "name": id, "peers": peers})
		case strings.HasPrefix(r.URL.Path, "/api/groups/") && r.Method == http.MethodPut:
			var in struct {
				Peers []string `json:"peers"`
			}
			json.NewDecoder(r.Body).Decode(&in)
			f.groups[strings.TrimPrefix(r.URL.Path, "/api/groups/")] = in.Peers
		case r.URL.Path == "/api/policies":
			io.WriteString(w, `[{"name": "Default", "enabled": true, "rules": [{"sources": [{"name": "All"}], "destinations": [{"name": "All"}]}]}]`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	saved := watchNetBird
	watchNetBird = func(secrets watchSecrets) (netbirdAPI, error) {
		return netbirdAPI{base: server.URL, token: secrets.NetBird, client: http.DefaultClient}, nil
	}
	t.Cleanup(func() { watchNetBird = saved })
	return server.URL
}

func TestWatchRevokeRekeyCutOff(t *testing.T) {
	ts := newTestServer(t)
	ts.connectAs(t, "anna", "staff")
	key, _ := loadDeviceKey(false)
	keys := &fakeOpenRouterKeys{}
	keys.start(t)
	netbird := &fakeNetBird{approval: true}
	netbird.start(t)
	var pushes []string
	var pushMu sync.Mutex
	ts.server.watch.config.Ntfy = "https://ntfy.example"
	ts.server.watch.push = func(config watchConfig, topic, title, text string) error {
		pushMu.Lock()
		defer pushMu.Unlock()
		pushes = append(pushes, text)
		return nil
	}
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	channel := &fakeTeamChannel{}
	b.channel = channel
	saveTeamSecrets("payments", teamSecrets{OpenRouterAPIKey: "sk-or-pasted"})
	saved, savedMachine := teamWatchClient, teamWatchMachine
	t.Cleanup(func() { teamWatchClient, teamWatchMachine = saved, savedMachine })
	teamWatchClient = func() (*serverClient, string) { return newServerClient(ts.url, key), ts.url }
	teamWatchMachine = (&teamTestProbe{states: map[string]string{}, disk: 0.3}).probe()

	// Without a provisioning key the server can't make one.
	b.sendHeartbeat("")
	if err := teamKeyFromServer("payments", io.Discard); err == nil || !strings.Contains(err.Error(), "hi server watch --openrouter") {
		t.Fatal(err)
	}
	admin := httptest.NewServer(ts.server.adminHandler())
	defer admin.Close()
	post := func(path, body string) (int, string) {
		response, err := http.Post(admin.URL+path, "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(response.Body)
		response.Body.Close()
		return response.StatusCode, string(data)
	}
	if code, body := post("/admin/watch/secret/openrouter", `{"value": "sk-or-wrong"}`); code != 400 || !strings.Contains(body, "provisioning key") {
		t.Fatal(code, body)
	}
	if code, body := post("/admin/watch/secret/openrouter", `{"value": "sk-or-prov"}`); code != 200 {
		t.Fatal(code, body)
	}
	if data, _ := os.ReadFile(ts.server.watch.secretsPath()); !strings.Contains(string(data), "sk-or-prov") {
		t.Fatal("not kept")
	}

	// The team takes a key of the server's making.
	if err := teamKeyFromServer("payments", io.Discard); err != nil {
		t.Fatal(err)
	}
	if secrets, _ := loadTeamSecrets("payments"); secrets.OpenRouterAPIKey != "sk-or-team-1" {
		t.Fatal(secrets.OpenRouterAPIKey)
	}
	if keys.keys["hash1"].Name != "hi team payments on "+ts.server.watch.view().Teams[0].Hostname {
		t.Fatalf("%+v", keys.keys["hash1"])
	}

	// Revoke: disabled at OpenRouter, the owners pushed, no new key on
	// the machine's word.
	if code, body := post("/admin/watch/act/payments/revoke", `{"because": "it posts to a paste site", "as": "bob"}`); code != 200 {
		t.Fatal(code, body)
	}
	if !keys.keys["hash1"].Disabled {
		t.Fatal("not disabled")
	}
	teamTestEventually(t, "no push", func() bool { pushMu.Lock(); defer pushMu.Unlock(); return len(pushes) == 1 })
	if err := teamKeyFromServer("payments", io.Discard); err == nil || !strings.Contains(err.Error(), "revoked") {
		t.Fatal(err)
	}
	// Rekey: a new key, the old deleted, delivered with the next heartbeat.
	if code, body := post("/admin/watch/act/payments/rekey", `{"as": "bob"}`); code != 200 {
		t.Fatal(code, body)
	}
	if _, ok := keys.keys["hash1"]; ok || keys.keys["hash2"] == nil {
		t.Fatal(keys.keys)
	}
	b.sendHeartbeat("")
	if secrets, _ := loadTeamSecrets("payments"); secrets.OpenRouterAPIKey != "sk-or-team-2" {
		t.Fatal(secrets.OpenRouterAPIKey)
	}
	if data, _ := os.ReadFile(teamKeysDir("payments") + "/openrouter.env"); !strings.Contains(string(data), "sk-or-team-2") {
		t.Fatal(string(data))
	}
	b.sendHeartbeat("")
	if data, _ := os.ReadFile(ts.server.watch.secretsPath()); strings.Contains(string(data), "sk-or-team-2") {
		t.Fatal("the delivered key stays on the server")
	}

	// Cut off, with peer approval: approval required again, and undone.
	if code, body := post("/admin/watch/secret/netbird", `{"value": "nbp_token"}`); code != 200 {
		t.Fatal(code, body)
	}
	if code, body := post("/admin/watch/act/payments/cutoff", `{"as": "bob"}`); code != 200 {
		t.Fatal(code, body)
	}
	if !netbird.peer.ApprovalRequired || ts.server.watch.view().Teams[0].CutOff == nil {
		t.Fatal("not cut off")
	}
	if code, body := post("/admin/watch/act/payments/reconnect", `{"as": "bob"}`); code != 200 || netbird.peer.ApprovalRequired {
		t.Fatal(code, body)
	}

	// Without peer approval: out of its groups, but All's policy is named.
	netbird.approval = false
	cut, err := ts.server.cutOffTeam("payments", "bob", "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(netbird.groups["gTeams"], ",") != "p2" || len(netbird.groups["gAll"]) != 3 || !strings.Contains(cut.Still, "Policy Default still lets every peer in") {
		t.Fatalf("%v %+v", netbird.groups, cut)
	}
	if err := ts.server.reconnectTeam("payments", "bob", ""); err != nil || strings.Join(netbird.groups["gTeams"], ",") != "p1,p2" {
		t.Fatal(err, netbird.groups)
	}
	var text strings.Builder
	printWatchView(ts.server.watch.view(), ts.server.watch.view().Teams[0].Last, &text)
	if strings.Contains(text.String(), "CUT OFF") || strings.Contains(text.String(), "isn't the server's") {
		t.Fatal(text.String())
	}
}
