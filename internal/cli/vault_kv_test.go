package cli

import (
	"bytes"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeBao is enough of OpenBao for hi: KV v2 at kv/, KV v1 at old/, JWT
// and AppRole logins, a policy per token, and being sealed.
type fakeBao struct {
	t         *testing.T
	mu        sync.Mutex
	server    *httptest.Server
	sealed    bool
	namespace string
	kv2       map[string][]fakeBaoVersion
	kv1       map[string]map[string]any
	pubkeys   []ed25519.PublicKey
	subject   string
	roleID    string
	secretID  string
	// read and write are the path prefixes a token may read and write,
	// such as kv/data/corp/.
	read, write []string
	tokens      map[string]bool
	logins      int
}

type fakeBaoVersion struct {
	data      map[string]any
	created   time.Time
	destroyed bool
}

func newFakeBao(t *testing.T) *fakeBao {
	b := &fakeBao{t: t, kv2: map[string][]fakeBaoVersion{}, kv1: map[string]map[string]any{}, tokens: map[string]bool{}}
	b.server = httptest.NewServer(http.HandlerFunc(b.serve))
	t.Cleanup(b.server.Close)
	return b
}

func (b *fakeBao) put(path string, data map[string]any) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.kv2[path] = append(b.kv2[path], fakeBaoVersion{data: data, created: time.Now()})
	return len(b.kv2[path])
}

func (b *fakeBao) register(pemText string) {
	block, _ := pem.Decode([]byte(pemText))
	if block == nil {
		b.t.Fatalf("no PEM: %q", pemText)
	}
	key, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		b.t.Fatal(err)
	}
	b.mu.Lock()
	b.pubkeys = append(b.pubkeys, key.(ed25519.PublicKey))
	b.mu.Unlock()
}

func (b *fakeBao) fail(w http.ResponseWriter, status int, why string) {
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]any{"errors": []string{why}})
}

func (b *fakeBao) may(prefixes []string, path string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

func (b *fakeBao) serve(w http.ResponseWriter, r *http.Request) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.sealed {
		b.fail(w, http.StatusServiceUnavailable, "Vault is sealed")
		return
	}
	if b.namespace != "" && r.Header.Get("X-Vault-Namespace") != b.namespace {
		b.fail(w, http.StatusForbidden, "no namespace")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/v1/")
	var body map[string]any
	if data, _ := io.ReadAll(r.Body); len(data) > 0 {
		json.Unmarshal(data, &body)
	}
	reply := func(value any) { json.NewEncoder(w).Encode(value) }
	switch {
	case path == "auth/jwt/login":
		if !b.checkJWT(fmt.Sprint(body["jwt"])) || body["role"] != "hi-server-"+vaultServerName() {
			b.fail(w, http.StatusBadRequest, "error validating token: invalid signature")
			return
		}
	case path == "auth/approle/login":
		if body["role_id"] != b.roleID || body["secret_id"] != b.secretID || b.roleID == "" {
			b.fail(w, http.StatusBadRequest, "invalid role or secret ID")
			return
		}
	}
	if strings.HasPrefix(path, "auth/") {
		b.logins++
		token := "tok-" + strconv.Itoa(b.logins)
		b.tokens[token] = true
		reply(map[string]any{"auth": map[string]any{"client_token": token, "lease_duration": 900}})
		return
	}
	if !b.tokens[r.Header.Get("X-Vault-Token")] {
		b.fail(w, http.StatusForbidden, "permission denied")
		return
	}
	if mount, ok := strings.CutPrefix(path, "sys/internal/ui/mounts/"); ok {
		version := "2"
		if mount == "old" {
			version = "1"
		}
		reply(map[string]any{"data": map[string]any{"options": map[string]any{"version": version}}})
		return
	}
	writing := r.Method == "POST" || r.Method == "PUT" || r.Method == "DELETE"
	allowed := b.may(b.read, path)
	if writing {
		allowed = b.may(b.write, path)
	}
	if !allowed {
		b.fail(w, http.StatusForbidden, "permission denied")
		return
	}
	switch {
	case strings.HasPrefix(path, "old/"):
		key := strings.TrimPrefix(path, "old/")
		if writing {
			b.kv1[key] = body
			w.WriteHeader(http.StatusNoContent)
			return
		}
		data, ok := b.kv1[key]
		if !ok {
			b.fail(w, http.StatusNotFound, "")
			return
		}
		reply(map[string]any{"data": data})
	case strings.HasPrefix(path, "kv/data/"):
		key := strings.TrimPrefix(path, "kv/data/")
		if r.Method == "POST" {
			data, _ := body["data"].(map[string]any)
			b.kv2[key] = append(b.kv2[key], fakeBaoVersion{data: data, created: time.Now()})
			reply(map[string]any{"data": map[string]any{"version": len(b.kv2[key])}})
			return
		}
		versions := b.kv2[key]
		n := len(versions)
		if v := r.URL.Query().Get("version"); v != "" {
			n, _ = strconv.Atoi(v)
		}
		if n < 1 || n > len(versions) {
			b.fail(w, http.StatusNotFound, "")
			return
		}
		version := versions[n-1]
		var data any = version.data
		if version.destroyed {
			data = nil
		}
		reply(map[string]any{"data": map[string]any{"data": data, "metadata": map[string]any{"version": n, "created_time": version.created.Format(time.RFC3339Nano), "destroyed": version.destroyed}}})
	case strings.HasPrefix(path, "kv/metadata/"):
		key := strings.TrimPrefix(path, "kv/metadata/")
		if r.Method == "LIST" {
			seen := map[string]bool{}
			for name := range b.kv2 {
				if rest, ok := strings.CutPrefix(name, key); ok && rest != "" {
					first, _, nested := strings.Cut(rest, "/")
					if nested {
						first += "/"
					}
					seen[first] = true
				}
			}
			if len(seen) == 0 {
				b.fail(w, http.StatusNotFound, "")
				return
			}
			keys := []string{}
			for k := range seen {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			reply(map[string]any{"data": map[string]any{"keys": keys}})
			return
		}
		if r.Method == "DELETE" {
			delete(b.kv2, key)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		versions, ok := b.kv2[key]
		if !ok {
			b.fail(w, http.StatusNotFound, "")
			return
		}
		listed := map[string]any{}
		for i, v := range versions {
			listed[strconv.Itoa(i+1)] = map[string]any{"created_time": v.created.Format(time.RFC3339Nano), "destroyed": v.destroyed, "deletion_time": ""}
		}
		reply(map[string]any{"data": map[string]any{"current_version": len(versions), "versions": listed}})
	case strings.HasPrefix(path, "kv/destroy/"):
		key := strings.TrimPrefix(path, "kv/destroy/")
		list, _ := body["versions"].([]any)
		for _, v := range list {
			n := int(v.(float64))
			if n >= 1 && n <= len(b.kv2[key]) {
				b.kv2[key][n-1].destroyed = true
				b.kv2[key][n-1].data = nil
			}
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		b.fail(w, http.StatusNotFound, "")
	}
}

func (b *fakeBao) checkJWT(token string) bool {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return false
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return false
	}
	good := false
	for _, key := range b.pubkeys {
		if ed25519.Verify(key, []byte(parts[0]+"."+parts[1]), signature) {
			good = true
		}
	}
	claims, _ := base64.RawURLEncoding.DecodeString(parts[1])
	var c struct {
		Sub string  `json:"sub"`
		Aud string  `json:"aud"`
		Exp float64 `json:"exp"`
	}
	json.Unmarshal(claims, &c)
	return good && c.Sub == "hi-server:"+vaultServerName() && c.Aud == "hi" && time.Unix(int64(c.Exp), 0).After(time.Now())
}

// vaultConnTestSetup is a machine with a vault, an app, and a team store
// connected as corp/, which hi logs in to.
func vaultConnTestSetup(t *testing.T) (*fakeBao, *[]string) {
	t.Helper()
	setupTeamTest(t)
	vaultTest(t)
	initVault(vaultSlotFile)
	vaultSharedTestApp(t, "payments")
	bao := newFakeBao(t)
	bao.read = []string{"kv/data/corp/", "kv/metadata/corp/"}
	var said []string
	savedSay, savedAway := appOpsSay, vaultSourceAwayAfter
	t.Cleanup(func() { appOpsSay, vaultSourceAwayAfter = savedSay, savedAway })
	appOpsSay = func(config appConfig, text string) { said = append(said, config.Name+": "+text) }
	vaultSourceAwayAfter = 0
	vaultKVMu.Lock()
	vaultKVClients = map[string]*vaultKV{}
	vaultKVMu.Unlock()
	var out bytes.Buffer
	if err := vaultConnectCommand([]string{"corp/", "vault://kv/corp", "--address", bao.server.URL}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "can't log in yet") || !strings.Contains(out.String(), "bound_subject=hi-server:"+vaultServerName()) ||
		!strings.Contains(out.String(), `path "kv/data/corp/*"     { capabilities = ["read"] }`) || !strings.Contains(out.String(), "BEGIN PUBLIC KEY") {
		t.Fatal(out.String())
	}
	conns, _ := loadVaultConns()
	conn, _ := conns.find("corp")
	bao.register(vaultConnPublicPEM(conn.Seed))
	out.Reset()
	if err := vaultLoginCommand(nil, &out); err != nil || !strings.Contains(out.String(), "corp/ (kv/corp in "+bao.server.URL+", jwt as hi-server-"+vaultServerName()+"): logs in.") {
		t.Fatal(err, out.String())
	}
	return bao, &said
}
