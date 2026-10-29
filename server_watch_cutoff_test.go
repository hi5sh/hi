package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
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
	f.peer = netbirdPeer{ID: "p1", Name: "gpu1", IP: "127.0.0.1"}
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
