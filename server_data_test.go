package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

var repoInfoPath = regexp.MustCompile(`^/api/(datasets|models)/acme/[^/]+(/revision/[^/]+)?$`)

// fakeHub is a Hugging Face Hub that knows one organization, acme, and
// records the calls the proxy passes on.
type fakeHub struct {
	server *httptest.Server
	mu     sync.Mutex
	calls  []string // "METHOD path auth"
}

func newFakeHub(t *testing.T) *fakeHub {
	t.Helper()
	hub := &fakeHub{}
	hub.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		hub.mu.Lock()
		hub.calls = append(hub.calls, r.Method+" "+r.URL.EscapedPath()+" "+auth)
		hub.mu.Unlock()
		if auth != "Bearer hf_team" {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"error":"Invalid credentials"}`))
			return
		}
		updated := `"lastModified":"2026-09-30T12:00:00.000Z"`
		switch {
		case repoInfoPath.MatchString(r.URL.Path):
			sha := strings.Repeat("a1", 20)
			if strings.HasSuffix(r.URL.Path, "/revision/v2") {
				sha = strings.Repeat("b2", 20)
			}
			w.Write([]byte(`{"sha":"` + sha + `"}`))
		case r.URL.Path == "/api/whoami-v2":
			w.Write([]byte(`{"name":"svc","orgs":[{"name":"acme"}],"auth":{"accessToken":{"role":"read"}}}`))
		case r.URL.Path == "/api/datasets" && r.URL.Query().Get("author") == "acme":
			w.Write([]byte(`[{"id":"acme/bars","private":true,` + updated + `,"sha":"abc123","mainSize":12400000000},` +
				`{"id":"acme/fills","private":true,` + updated + `,"sha":"def456","mainSize":3100000000}]`))
		case r.URL.Path == "/api/models" && r.URL.Query().Get("author") == "acme":
			w.Write([]byte(`[{"id":"acme/ranker","private":true,` + updated + `,"sha":"0a1b2c"},` +
				`{"id":"acme/bars","private":true,` + updated + `,"sha":"999999"}]`))
		case r.URL.Path == "/api/buckets/acme":
			w.Write([]byte(`[{"id":"acme/scratch","private":true,"updatedAt":"2026-09-30T12:00:00.000Z","size":80200000000,"totalFiles":412}]`))
		case r.URL.Path == "/api/buckets/acme/scratch/tree":
			w.Write([]byte(`[{"type":"directory","path":"runs"},{"type":"file","path":"runs/a.parquet","size":7000},{"type":"file","path":"notes.md","size":12}]`))
		case r.URL.Path == "/api/datasets" || r.URL.Path == "/api/models" || strings.HasPrefix(r.URL.Path, "/api/buckets/") && strings.Count(r.URL.Path, "/") == 3:
			w.Write([]byte(`[]`))
		case strings.HasPrefix(r.URL.Path, "/api/datasets/acme/bars/tree/"):
			w.Write([]byte(`[{"type":"directory","path":"data"},{"type":"file","path":"data/day.parquet","size":5000},{"type":"file","path":"data/week.parquet","size":9000},{"type":"file","path":"README.md","size":11}]`))
		case strings.HasPrefix(r.URL.Path, "/datasets/acme/bars/resolve/"+strings.Repeat("a1", 20)+"/data/"):
			w.Header().Set("Location", "https://cdn.example/signed/"+filepath.Base(r.URL.Path)+"?Expires=1")
			w.WriteHeader(http.StatusFound)
		case r.URL.Path == "/datasets/acme/bars/resolve/"+strings.Repeat("a1", 20)+"/README.md":
			w.Header().Set("Location", "/api/resolve-cache/datasets/acme/bars/"+strings.Repeat("a1", 20)+"/README.md")
			w.WriteHeader(http.StatusTemporaryRedirect)
		case strings.HasPrefix(r.URL.Path, "/api/resolve-cache/datasets/acme/bars/"):
			w.Write([]byte("# Bars data"))
		case r.URL.Path == "/acme/ranker/resolve/main/config.json":
			w.Header().Set("Location", "/api/resolve-cache/models/acme/ranker/0a1b2c/config.json")
			w.Header().Set("Link", `<`+hub.server.URL+`/api/models/acme/ranker/xet-read-token/0a1b2c>; rel="xet-auth"`)
			w.WriteHeader(http.StatusTemporaryRedirect)
		case r.URL.Path == "/datasets/acme/bars/resolve/main/data/day.parquet":
			w.Header().Set("Location", "https://cdn.example/signed/day.parquet?Expires=1")
			w.Header().Set("X-Linked-Size", "5000")
			w.WriteHeader(http.StatusFound)
		default:
			w.Write([]byte(`{"ok":true}`))
		}
	}))
	t.Cleanup(hub.server.Close)
	previous := dataHubURL
	dataHubURL = hub.server.URL
	t.Cleanup(func() { dataHubURL = previous })
	return hub
}

func (h *fakeHub) passed() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.calls...)
}

// newDataServer is a test server serving acme, with alice's device
// enrolled in group students.
func newDataServer(t *testing.T) (*testServer, *fakeHub) {
	t.Helper()
	hub := newFakeHub(t)
	ts := newTestServer(t)
	if _, err := ts.server.addDataOrgs([]string{"acme"}, "hf_team", "bob"); err != nil {
		t.Fatal(err)
	}
	ts.connectAs(t, "alice", "students")
	return ts, hub
}

func (ts *testServer) aliceDevice(t *testing.T) serverDevice {
	t.Helper()
	ts.server.mu.Lock()
	defer ts.server.mu.Unlock()
	for _, device := range ts.server.state.Devices {
		if device.User == "alice" {
			return *device
		}
	}
	t.Fatal("alice has no device")
	return serverDevice{}
}

// proxyCall makes a call to the proxy as hf would.
func proxyCall(t *testing.T, ts *testServer, method, path, token string) *http.Response {
	t.Helper()
	request, _ := http.NewRequest(method, ts.url+path, nil)
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	return response
}

func TestAddingADataOrgChecksTheToken(t *testing.T) {
	newFakeHub(t)
	ts := newTestServer(t)
	if _, err := ts.server.addDataOrgs([]string{"acme"}, "hf_wrong", "bob"); err == nil || !strings.Contains(err.Error(), "refused the token") {
		t.Fatalf("a wrong token: %v", err)
	}
	if _, err := ts.server.addDataOrgs([]string{"acme", "elsewhere"}, "hf_team", "bob"); err == nil || !strings.Contains(err.Error(), "not a member of elsewhere") {
		t.Fatalf("an organization the account isn't in: %v", err)
	}
	if len(ts.server.dataOrgs()) != 0 {
		t.Fatal("a failed add stored a token")
	}
	results, err := ts.server.addDataOrgs([]string{"acme"}, "hf_team", "bob")
	if err != nil || len(results) != 1 || results[0].Datasets != 2 || results[0].Models != 2 || results[0].Buckets != 1 || results[0].Account != "svc" {
		t.Fatalf("add: %+v, %v", results, err)
	}
	keys, _ := os.ReadFile(filepath.Join(ts.dir, "keys.json"))
	if !strings.Contains(string(keys), `"data:acme": "hf_team"`) {
		t.Fatalf("keys.json = %s", keys)
	}
	audit, _ := os.ReadFile(filepath.Join(ts.dir, "audit.jsonl"))
	if !strings.Contains(string(audit), "connected data organization") || strings.Contains(string(audit), "hf_team") {
		t.Fatalf("audit = %s", audit)
	}
}

func TestHiDataListsWhatTheGroupMayRead(t *testing.T) {
	ts, _ := newDataServer(t)
	code, stdout, stderr := runHi("data", "ls")
	if code != 0 || !strings.Contains(stdout, "dataset  acme/bars") || !strings.Contains(stdout, "12.4 GB") ||
		!strings.Contains(stdout, "model    acme/ranker") || !strings.Contains(stdout, "bucket   acme/scratch") || !strings.Contains(stdout, "80.2 GB") {
		t.Fatalf("ls: code %d\n%s%s", code, stdout, stderr)
	}
	code, stdout, _ = runHi("data", "ls", "--kind", "model", "--json")
	var catalog apiDataCatalog
	if code != 0 || json.Unmarshal([]byte(stdout), &catalog) != nil || len(catalog.Items) != 2 || catalog.Items[0].Kind != "model" {
		t.Fatalf("ls --json: code %d\n%s", code, stdout)
	}

	writeTestFile(t, policyPath(ts.dir), `{"groups":{"students":{"data":["acme/fills"]}}}`, 0o600)
	code, stdout, _ = runHi("data", "ls")
	if code != 0 || strings.Contains(stdout, "acme/bars") || !strings.Contains(stdout, "acme/fills") {
		t.Fatalf("ls with a policy:\n%s", stdout)
	}
	writeTestFile(t, policyPath(ts.dir), `{"groups":{"students":{"data":[]}}}`, 0o600)
	if _, stdout, _ = runHi("data", "ls"); !strings.Contains(stdout, "Nothing for you to download") {
		t.Fatalf("ls with an empty list:\n%s", stdout)
	}
}

func TestDataTokensFollowPolicy(t *testing.T) {
	ts, _ := newDataServer(t)
	client, connection, err := dataClient()
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := dataToken(client, connection, "dataset:acme/bars"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := dataToken(client, connection, "dataset:other/bars"); err == nil || !strings.Contains(err.Error(), "doesn't serve the other organization") {
		t.Fatalf("another organization: %v", err)
	}
	if _, _, err := dataToken(client, connection, "space:acme/app"); err == nil || !strings.Contains(err.Error(), "invalid scope") {
		t.Fatalf("a space: %v", err)
	}
	writeTestFile(t, policyPath(ts.dir), `{"groups":{"students":{"data":["acme/fills"]}}}`, 0o600)
	if _, _, err := dataToken(client, connection, "dataset:acme/bars"); err == nil || !strings.Contains(err.Error(), "may not read acme/bars") {
		t.Fatalf("a repository outside policy: %v", err)
	}
}

func TestDataProxyPassesReadsWithTheTeamToken(t *testing.T) {
	ts, hub := newDataServer(t)
	device := ts.aliceDevice(t)
	token := ts.server.issueDataToken(dataClaims{Device: device.Fingerprint, User: "alice", Scope: "model:acme/ranker",
		Expires: computeNow().Add(time.Hour).Unix()})

	response := proxyCall(t, ts, http.MethodHead, "/hf/acme/ranker/resolve/main/config.json", token)
	if response.StatusCode != http.StatusTemporaryRedirect {
		t.Fatalf("resolve: %d", response.StatusCode)
	}
	// Redirects and links within the Hub stay on the proxy.
	if location := response.Header.Get("Location"); location != ts.url+"/hf/api/resolve-cache/models/acme/ranker/0a1b2c/config.json" {
		t.Fatalf("Location = %q", location)
	}
	if link := response.Header.Get("Link"); !strings.Contains(link, ts.url+"/hf/api/models/acme/ranker/xet-read-token/0a1b2c") {
		t.Fatalf("Link = %q", link)
	}
	if response := proxyCall(t, ts, http.MethodGet, "/hf/api/resolve-cache/models/acme/ranker/0a1b2c/onnx%2Fconfig.json", token); response.StatusCode != http.StatusOK {
		t.Fatalf("resolve-cache with a nested file: %d", response.StatusCode)
	}
	for _, path := range []string{"/hf/api/models/acme/ranker/revision/main", "/hf/api/models/acme/ranker/tree/main?recursive=true",
		"/hf/api/models/acme/ranker/xet-read-token/0a1b2c"} {
		if response := proxyCall(t, ts, http.MethodGet, path, token); response.StatusCode != http.StatusOK {
			t.Fatalf("%s: %d", path, response.StatusCode)
		}
	}
	calls := hub.passed()
	last := calls[len(calls)-1]
	if !strings.HasSuffix(last, "Bearer hf_team") || strings.Contains(strings.Join(calls, "\n"), dataTokenPrefix) {
		t.Fatalf("the hub saw:\n%s", strings.Join(calls, "\n"))
	}
	usage, _ := os.ReadFile(filepath.Join(ts.dir, "data_usage.jsonl"))
	lines := strings.Split(strings.TrimSpace(string(usage)), "\n")
	// The redirect within the Hub isn't counted; where it lands and the Xet
	// grant are.
	if len(lines) != 2 || !strings.Contains(lines[0], `"file":"onnx/config.json"`) ||
		!strings.Contains(lines[1], `"method":"xet"`) || !strings.Contains(lines[1], `"revision":"0a1b2c"`) {
		t.Fatalf("usage:\n%s", usage)
	}

	dataset := ts.server.issueDataToken(dataClaims{Device: device.Fingerprint, User: "alice", Scope: "*", Expires: computeNow().Add(time.Hour).Unix()})
	response = proxyCall(t, ts, http.MethodGet, "/hf/datasets/acme/bars/resolve/main/data/day.parquet", dataset)
	if response.StatusCode != http.StatusFound || response.Header.Get("Location") != "https://cdn.example/signed/day.parquet?Expires=1" {
		t.Fatalf("a CDN redirect: %d %q", response.StatusCode, response.Header.Get("Location"))
	}
	usage, _ = os.ReadFile(filepath.Join(ts.dir, "data_usage.jsonl"))
	if !strings.Contains(string(usage), `"file":"data/day.parquet","method":"GET","status":302,"size":5000`) {
		t.Fatalf("usage:\n%s", usage)
	}
}

func TestDataProxyRefusesEverythingElse(t *testing.T) {
	ts, hub := newDataServer(t)
	device := ts.aliceDevice(t)
	claims := dataClaims{Device: device.Fingerprint, User: "alice", Scope: "model:acme/ranker", Expires: computeNow().Add(time.Hour).Unix()}
	token := ts.server.issueDataToken(claims)
	expired := claims
	expired.Expires = computeNow().Add(-time.Minute).Unix()
	other := claims
	other.User = "mallory"

	cases := []struct {
		method, path, token string
		status              int
	}{
		{http.MethodGet, "/hf/api/models/acme/ranker", "", http.StatusUnauthorized},
		{http.MethodGet, "/hf/api/models/acme/ranker", "hf_team", http.StatusUnauthorized},
		{http.MethodGet, "/hf/api/models/acme/ranker", ts.server.issueDataToken(expired), http.StatusUnauthorized},
		{http.MethodGet, "/hf/api/models/acme/ranker", ts.server.issueDataToken(other), http.StatusUnauthorized},
		{http.MethodGet, "/hf/api/models/acme/ranker", token[:len(token)-4] + "AAAA", http.StatusUnauthorized},
		// Writes, and anything but a repository's reads.
		{http.MethodPost, "/hf/api/models/acme/ranker/commit/main", token, http.StatusForbidden},
		{http.MethodPost, "/hf/api/repos/create", token, http.StatusForbidden},
		{http.MethodDelete, "/hf/api/models/acme/ranker", token, http.StatusForbidden},
		{http.MethodPut, "/hf/acme/ranker/resolve/main/config.json", token, http.StatusForbidden},
		{http.MethodGet, "/hf/api/whoami-v2", token, http.StatusForbidden},
		{http.MethodGet, "/hf/api/models?author=acme", token, http.StatusForbidden},
		{http.MethodGet, "/hf/api/spaces/acme/app", token, http.StatusForbidden},
		{http.MethodGet, "/hf/api/models/acme/ranker/settings", token, http.StatusForbidden},
		// Climbing out of the repository, plainly or encoded.
		{http.MethodGet, "/hf/api/models/acme/ranker/tree/main/..%2F..%2F..%2Fbars", token, http.StatusForbidden},
		{http.MethodGet, "/hf/acme/ranker/resolve/main/%2E%2E/%2E%2E/bars/resolve/main/x", token, http.StatusForbidden},
		{http.MethodGet, "/hf/api/models/acme%2Fbars/x/revision/main", token, http.StatusForbidden},
		// Another repository than the token's.
		{http.MethodGet, "/hf/api/models/acme/bars", token, http.StatusForbidden},
		{http.MethodGet, "/hf/datasets/acme/bars/resolve/main/x", token, http.StatusForbidden},
	}
	before := len(hub.passed())
	for _, c := range cases {
		if response := proxyCall(t, ts, c.method, c.path, c.token); response.StatusCode != c.status {
			t.Errorf("%s %s: %d, want %d", c.method, c.path, response.StatusCode, c.status)
		}
	}
	if passed := hub.passed()[before:]; len(passed) != 0 {
		t.Fatalf("refused calls reached the hub:\n%s", strings.Join(passed, "\n"))
	}

	// Policy applies to tokens already issued.
	writeTestFile(t, policyPath(ts.dir), `{"groups":{"students":{"data":["acme/fills"]}}}`, 0o600)
	if response := proxyCall(t, ts, http.MethodGet, "/hf/api/models/acme/ranker", token); response.StatusCode != http.StatusForbidden {
		t.Fatalf("a token after the policy changed: %d", response.StatusCode)
	}
}

// fakeHFCommand installs an hf that records its arguments and the endpoint and
// token it was given.
func fakeHFCommand(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	record := filepath.Join(dir, "record")
	script := filepath.Join(dir, "hf")
	writeTestFile(t, script, "#!/bin/sh\n{ echo \"args: $*\"; echo \"endpoint: $HF_ENDPOINT\"; echo \"token: $HF_TOKEN\"; echo \"hub token: $HUGGING_FACE_HUB_TOKEN\"; } >> "+record+"\n", 0o755)
	previous := dataHFCommand
	dataHFCommand = script
	t.Cleanup(func() { dataHFCommand = previous })
	return record
}

func TestHiDataGetRunsHFThroughTheServer(t *testing.T) {
	ts, _ := newDataServer(t)
	record := fakeHFCommand(t)
	t.Setenv("HF_TOKEN", "hf_personal")
	t.Setenv("HUGGING_FACE_HUB_TOKEN", "hf_personal")
	t.Chdir(t.TempDir())

	code, _, stderr := runHi("data", "get", "acme/bars")
	if code != 2 || !strings.Contains(stderr, "say dataset:acme/bars or model:acme/bars") {
		t.Fatalf("an ambiguous name: code %d\n%s", code, stderr)
	}
	code, _, stderr = runHi("data", "get", "dataset:acme/bars", "--include", "*.parquet", "--include", "README.md", "--exclude", "old/*")
	if code != 0 {
		t.Fatalf("get: code %d\n%s", code, stderr)
	}
	data, _ := os.ReadFile(record)
	got := string(data)
	if !strings.Contains(got, "args: download acme/bars --repo-type dataset --local-dir data/bars --revision "+strings.Repeat("a1", 20)+" --include *.parquet --include README.md --exclude old/*") ||
		!strings.Contains(got, "endpoint: "+ts.url+"/hf\n") || !strings.Contains(got, "token: "+dataTokenPrefix) ||
		strings.Contains(got, "hf_personal") {
		t.Fatalf("hf ran with:\n%s", got)
	}

	code, _, stderr = runHi("data", "get", "acme/ranker", "--to", "models/r", "--revision", "v2")
	data, _ = os.ReadFile(record)
	if code != 0 || !strings.Contains(string(data), "args: download acme/ranker --local-dir models/r --revision "+strings.Repeat("b2", 20)+"\n") {
		t.Fatalf("get a model: code %d\n%s%s", code, data, stderr)
	}

	code, _, stderr = runHi("data", "get", "acme/scratch", "--include", "runs/*")
	data, _ = os.ReadFile(record)
	if code != 0 || !strings.Contains(string(data), "args: buckets sync hf://buckets/acme/scratch data/scratch --include runs/*\n") {
		t.Fatalf("get a bucket: code %d\n%s%s", code, data, stderr)
	}
	if code, _, stderr = runHi("data", "get", "acme/scratch", "--revision", "v1"); code != 2 || !strings.Contains(stderr, "no revisions") {
		t.Fatalf("a bucket with a revision: code %d\n%s", code, stderr)
	}

	code, _, stderr = runHi("data", "get", "acme/missing")
	if code == 0 || !strings.Contains(stderr, "not among what you may download") {
		t.Fatalf("a missing repository: code %d\n%s", code, stderr)
	}
}

func TestHiDataNeedsAServerAndHF(t *testing.T) {
	newTestServer(t)
	code, _, stderr := runHi("data", "ls")
	if code == 0 || !strings.Contains(stderr, "hi connect") {
		t.Fatalf("without a server: code %d\n%s", code, stderr)
	}

	newDataServer(t)
	previous := dataHFCommand
	dataHFCommand = "hf-not-installed"
	t.Cleanup(func() { dataHFCommand = previous })
	code, _, stderr = runHi("data", "get", "acme/ranker")
	if code == 0 || !strings.Contains(stderr, "pip install -U huggingface_hub") {
		t.Fatalf("without hf: code %d\n%s", code, stderr)
	}
}

func TestServerDataCommandWorksWithoutARunningServer(t *testing.T) {
	newFakeHub(t)
	ts := newTestServer(t)
	writeTestFile(t, filepath.Join(ts.dir, "config.json"), `{"listen":"127.0.0.1:0"}`, 0o600)
	tokenFile := filepath.Join(t.TempDir(), "token")
	writeTestFile(t, tokenFile, "hf_team\n", 0o600)

	code, stdout, stderr := runHi("server", "data", "add", "acme", "--token-file", tokenFile, "--dir", ts.dir)
	if code != 0 || !strings.Contains(stdout, "✓ acme: 2 datasets, 2 models, 1 bucket (token of svc, read-only)") {
		t.Fatalf("add: code %d\n%s%s", code, stdout, stderr)
	}
	code, stdout, _ = runHi("server", "data", "list", "--dir", ts.dir)
	if code != 0 || !strings.Contains(stdout, "✓ acme: 2 datasets, 2 models, 1 bucket (token of svc, read-only)") {
		t.Fatalf("list: code %d\n%s", code, stdout)
	}
	code, stdout, _ = runHi("server", "data", "test", "--dir", ts.dir)
	if code != 0 || !strings.Contains(stdout, "token of svc") {
		t.Fatalf("test: code %d\n%s", code, stdout)
	}
	code, _, _ = runHi("server", "data", "remove", "acme", "--dir", ts.dir)
	keys, _ := os.ReadFile(filepath.Join(ts.dir, "keys.json"))
	state, _ := os.ReadFile(filepath.Join(ts.dir, "state.json"))
	if code != 0 || strings.Contains(string(keys), "hf_team") || strings.Contains(string(state), `"account"`) {
		t.Fatalf("remove: code %d, keys %s", code, keys)
	}
	if code, _, stderr = runHi("server", "data", "add", "acme", "--dir", ts.dir); code == 0 || !strings.Contains(stderr, "no token given") {
		t.Fatalf("add without a token: code %d\n%s", code, stderr)
	}
}

func TestPolicyRejectsBadDataPatterns(t *testing.T) {
	if _, err := parsePolicy([]byte(`{"groups":{"staff":{"data":["acme/["]}}}`)); err == nil {
		t.Fatal("a broken pattern was accepted")
	}
	policy, err := parsePolicy([]byte(`{"groups":{"staff":{"data":["acme/*","Other/x"]}}}`))
	if err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]bool{"acme/bars": true, "ACME/bars": true, "other/x": true, "other/y": false, "acme": false} {
		if dataAllowed(policy, "staff", id) != want {
			t.Errorf("%s: want %v", id, want)
		}
	}
	if !dataAllowed(policy, "students", "any/thing") {
		t.Error("a group without a data field should read everything")
	}
}

func TestDataProxyPassesBucketReadsOnly(t *testing.T) {
	ts, hub := newDataServer(t)
	device := ts.aliceDevice(t)
	token := ts.server.issueDataToken(dataClaims{Device: device.Fingerprint, User: "alice", Scope: "bucket:acme/scratch",
		Expires: computeNow().Add(time.Hour).Unix()})
	for _, c := range []struct {
		method, path string
		status       int
	}{
		{http.MethodGet, "/hf/api/buckets/acme/scratch", http.StatusOK},
		{http.MethodGet, "/hf/api/buckets/acme/scratch/tree?recursive=true", http.StatusOK},
		{http.MethodGet, "/hf/api/buckets/acme/scratch/xet-read-token", http.StatusOK},
		{http.MethodHead, "/hf/buckets/acme/scratch/resolve/runs/a.parquet", http.StatusOK},
		{http.MethodPost, "/hf/api/buckets/acme/scratch/paths-info", http.StatusOK},
		// Uploads, deletes, and settings are refused.
		{http.MethodPost, "/hf/api/buckets/acme/scratch/batch", http.StatusForbidden},
		{http.MethodGet, "/hf/api/buckets/acme/scratch/xet-write-token", http.StatusForbidden},
		{http.MethodDelete, "/hf/api/buckets/acme/scratch", http.StatusForbidden},
		{http.MethodPut, "/hf/api/buckets/acme/scratch/settings", http.StatusForbidden},
		{http.MethodPost, "/hf/api/buckets/acme/new", http.StatusForbidden},
		// The bucket list of the organization and other buckets are not for this token.
		{http.MethodGet, "/hf/api/buckets/acme", http.StatusForbidden},
		{http.MethodGet, "/hf/api/buckets/acme/other", http.StatusForbidden},
	} {
		if response := proxyCall(t, ts, c.method, c.path, token); response.StatusCode != c.status {
			t.Errorf("%s %s: %d, want %d", c.method, c.path, response.StatusCode, c.status)
		}
	}
	for _, call := range hub.passed() {
		if strings.Contains(call, "batch") || strings.Contains(call, "write") || strings.HasPrefix(call, "DELETE") {
			t.Fatalf("a write reached the hub: %s", call)
		}
	}

	code, stdout, stderr := runHi("data", "info", "acme/scratch")
	if code != 0 || !strings.Contains(stdout, "bucket acme/scratch") || !strings.Contains(stdout, "2 files, 7.0 KB") ||
		!strings.Contains(stdout, "runs/a.parquet") || strings.Contains(stdout, " at ") {
		t.Fatalf("info: code %d\n%s%s", code, stdout, stderr)
	}
}

func TestOldClientsDontSeeBuckets(t *testing.T) {
	for agent, want := range map[string]bool{"hi/v0.24.1": false, "hi/v0.24.0": false, "hi/v0.23.9": false,
		"hi/v0.24.2": true, "hi/v0.25.0": true, "hi/v1.0.0": true, "hi/dev": true, "curl/8": true} {
		if clientKnowsBuckets(agent) != want {
			t.Errorf("%s: want %v", agent, want)
		}
	}
}

func TestHiDataGetRecordsAndRestoresInAProject(t *testing.T) {
	newDataServer(t)
	record := fakeHFCommand(t)
	project := t.TempDir()
	if err := os.MkdirAll(filepath.Join(project, ".hi5"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(project, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(filepath.Join(project, "src"))

	if code, _, stderr := runHi("data", "get", "dataset:acme/bars", "--to", "../data/bars", "--include", "*.parquet"); code != 0 || !strings.Contains(stderr, "Recorded in .hi5/data.json") {
		t.Fatalf("get: code %d\n%s", code, stderr)
	}
	if code, _, stderr := runHi("data", "get", "acme/scratch", "--to", "../data/scratch"); code != 0 {
		t.Fatalf("get a bucket: code %d\n%s", code, stderr)
	}
	if code, _, stderr := runHi("data", "get", "acme/ranker", "--to", "/tmp/elsewhere"); code != 0 || !strings.Contains(stderr, "outside the project") {
		t.Fatalf("get outside the project: code %d\n%s", code, stderr)
	}
	if code, _, _ := runHi("data", "get", "acme/fills", "--no-record"); code != 0 {
		t.Fatal("get --no-record failed")
	}
	records, err := loadDataRecords(project)
	if err != nil || len(records.Data) != 2 {
		t.Fatalf("records: %+v, %v", records, err)
	}
	bars, scratch := records.Data[0], records.Data[1]
	if bars.ID != "acme/bars" || bars.Revision != strings.Repeat("a1", 20) || bars.To != "data/bars" || len(bars.Include) != 1 {
		t.Fatalf("dataset record: %+v", bars)
	}
	if scratch.Kind != "bucket" || scratch.FilesHash == "" || scratch.Files != 2 || scratch.To != "data/scratch" {
		t.Fatalf("bucket record: %+v", scratch)
	}

	// Fetching again uses the recorded commit and folders, and says when a
	// bucket changed since.
	scratch.FilesHash = "older"
	data, _ := json.Marshal(dataRecords{Data: []dataRecord{bars, scratch}})
	writeTestFile(t, filepath.Join(project, ".hi5", "data.json"), string(data), 0o644)
	os.Remove(record)
	code, _, stderr := runHi("data", "get")
	calls, _ := os.ReadFile(record)
	if code != 0 || !strings.Contains(string(calls), "args: download acme/bars --repo-type dataset --local-dir ../data/bars --revision "+strings.Repeat("a1", 20)+" --include *.parquet") ||
		!strings.Contains(string(calls), "args: buckets sync hf://buckets/acme/scratch ../data/scratch") ||
		!strings.Contains(stderr, "bucket acme/scratch has changed since it was recorded") {
		t.Fatalf("restore: code %d\n%s%s", code, calls, stderr)
	}
	if after, _ := loadDataRecords(project); after.Data[1].FilesHash != "older" {
		t.Fatal("fetching again changed the record")
	}

	t.Chdir(t.TempDir())
	if code, _, stderr := runHi("data", "get"); code != 2 || !strings.Contains(stderr, "no .hi5 folder") {
		t.Fatalf("get with no name outside a project: code %d\n%s", code, stderr)
	}
}

func TestHiDataRunAndEnv(t *testing.T) {
	ts, _ := newDataServer(t)
	t.Setenv("HF_TOKEN", "hf_personal")
	code, stdout, stderr := runHi("data", "run", "--", "sh", "-c", `echo "$HF_ENDPOINT $HF_TOKEN"`)
	if code != 0 || !strings.HasPrefix(stdout, ts.url+"/hf "+dataTokenPrefix) || strings.Contains(stdout, "hf_personal") {
		t.Fatalf("run: code %d\n%s%s", code, stdout, stderr)
	}
	if code, _, _ := runHi("data", "run", "sh", "-c", "exit 7"); code != 7 {
		t.Fatalf("run passes the exit status on: %d", code)
	}
	code, stdout, _ = runHi("data", "env")
	if code != 0 || !strings.Contains(stdout, "export HF_ENDPOINT='"+ts.url+"/hf'\n") || !strings.Contains(stdout, "export HF_TOKEN="+dataTokenPrefix) {
		t.Fatalf("env: code %d\n%s", code, stdout)
	}
	audit, _ := os.ReadFile(filepath.Join(ts.dir, "audit.jsonl"))
	if !strings.Contains(string(audit), `"action":"data token","subject":"*"`) {
		t.Fatalf("audit = %s", audit)
	}
	if code, _, stderr := runHi("data", "run"); code != 2 || !strings.Contains(stderr, "usage") {
		t.Fatalf("run without a command: %d %s", code, stderr)
	}
}

func TestDataLinksForComputeRuns(t *testing.T) {
	ts, _ := newDataServer(t)
	client, _, err := dataClient()
	if err != nil {
		t.Fatal(err)
	}
	var links apiDataLinks
	if err := client.call(http.MethodPost, "/v1/data/links", apiDataLinksRequest{Scope: "dataset:acme/bars"}, &links); err != nil {
		t.Fatal(err)
	}
	if links.Revision != strings.Repeat("a1", 20) || len(links.Files) != 3 {
		t.Fatalf("links: %+v", links)
	}
	byPath := map[string]apiDataLink{}
	for _, file := range links.Files {
		byPath[file.Path] = file
	}
	if byPath["data/day.parquet"].URL != "https://cdn.example/signed/day.parquet?Expires=1" || byPath["README.md"].URL != "" || string(byPath["README.md"].Content) != "# Bars data" {
		t.Fatalf("files: %+v", byPath)
	}

	if err := client.call(http.MethodPost, "/v1/data/links", apiDataLinksRequest{Scope: "dataset:acme/bars", Include: []string{"data/d*"}}, &links); err != nil || len(links.Files) != 1 {
		t.Fatalf("a pattern: %+v, %v", links, err)
	}
	if err := client.call(http.MethodPost, "/v1/data/links", apiDataLinksRequest{Scope: "dataset:acme/bars", Include: []string{"nothing/*"}}, &links); err == nil || !strings.Contains(err.Error(), "no files") {
		t.Fatalf("a pattern that matches nothing: %v", err)
	}
	if err := client.call(http.MethodPost, "/v1/data/links", apiDataLinksRequest{Scope: "*"}, &links); err == nil {
		t.Fatal("links for every repository at once")
	}
	writeTestFile(t, policyPath(ts.dir), `{"groups":{"students":{"data":["acme/fills"]}}}`, 0o600)
	if err := client.call(http.MethodPost, "/v1/data/links", apiDataLinksRequest{Scope: "dataset:acme/bars"}, &links); err == nil || !strings.Contains(err.Error(), "may not read") {
		t.Fatalf("links outside policy: %v", err)
	}
	audit, _ := os.ReadFile(filepath.Join(ts.dir, "audit.jsonl"))
	if !strings.Contains(string(audit), `"action":"data links"`) || strings.Contains(string(audit), "cdn.example") {
		t.Fatalf("audit = %s", audit)
	}
}

func TestDataRunRefs(t *testing.T) {
	for value, want := range map[string]dataRunRef{
		"acme/fdb":                   {ref: "acme/fdb", id: "acme/fdb"},
		"bucket:acme/fdb/runs":       {ref: "bucket:acme/fdb", id: "acme/fdb", include: "runs/*"},
		"acme/fdb/runs/2026-09/*":    {ref: "acme/fdb", id: "acme/fdb", include: "runs/2026-09/*"},
		"acme/bars/data/day.parquet": {ref: "acme/bars", id: "acme/bars", include: "data/day.parquet"},
	} {
		got, err := parseDataRunRef(value)
		if err != nil || got != want {
			t.Errorf("%s: %+v, %v", value, got, err)
		}
	}
	if _, err := parseDataRunRef("fdb"); err == nil {
		t.Error("a name without an organization was accepted")
	}
}

// TestDataRunWrapperDownloadsThenRunsTheScript runs the wrapper with
// Python against a local file server standing in for the CDN.
func TestDataRunWrapperDownloadsThenRunsTheScript(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("no python3")
	}
	cdn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/expired" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.Write([]byte(strings.Repeat("x", 5000)))
	}))
	defer cdn.Close()
	script := "# /// script\n# dependencies = [\"numpy\"]\n# ///\nimport os, sys\nprint('script', sys.argv[1:], os.path.getsize('data/bars/data/day.parquet'), open('data/bars/README.md').read())\n"
	repos := []dataRunRepo{{ID: "acme/bars", To: "data/bars", Files: []apiDataLink{
		{Path: "data/day.parquet", Size: 5000, URL: cdn.URL + "/day"},
		{Path: "README.md", Size: 11, Content: []byte("# Bars data")},
	}}}
	wrapper, err := dataRunWrapper("train.py", []byte(script), repos)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(wrapper), "# /// script\n# dependencies = [\"numpy\"]\n# ///\n") {
		t.Fatalf("the inline metadata isn't kept at the top:\n%s", wrapper[:200])
	}
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "train.py"), string(wrapper), 0o644)
	command := exec.Command(python, "train.py", "--epochs", "2")
	command.Dir = dir
	output, err := command.CombinedOutput()
	if err != nil || !strings.Contains(string(output), "script ['--epochs', '2'] 5000 # Bars data") || !strings.Contains(string(output), "hi data: ready") {
		t.Fatalf("wrapper: %v\n%s", err, output)
	}

	repos[0].Files[0] = apiDataLink{Path: "data/other.parquet", Size: 5000, URL: cdn.URL + "/expired"}
	wrapper, _ = dataRunWrapper("train.py", []byte(script), repos)
	writeTestFile(t, filepath.Join(dir, "train.py"), string(wrapper), 0o644)
	command = exec.Command(python, "train.py")
	command.Dir = dir
	output, err = command.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "has expired") {
		t.Fatalf("an expired link: %v\n%s", err, output)
	}
}

func TestPrepareDataRunWrapsTheScript(t *testing.T) {
	newDataServer(t)
	dir := t.TempDir()
	script := filepath.Join(dir, "train.py")
	writeTestFile(t, script, "print('hi')\n", 0o644)
	request := runRequest{script: script}
	var stderr strings.Builder
	cleanup, err := prepareDataRun(&request, []string{"dataset:acme/bars/data"}, "hf", &stderr)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	wrapper, _ := os.ReadFile(request.script)
	if request.script == script || filepath.Base(request.script) != "train.py" || !strings.Contains(string(wrapper), "_NAME = \"train.py\"") ||
		!strings.Contains(stderr.String(), "2 files, 14.0 KB at a1a1a1a, into data/bars on the instance") {
		t.Fatalf("wrapper %s:\n%s\n%s", request.script, wrapper, stderr.String())
	}
	if _, err := prepareDataRun(&runRequest{image: "python:3.12"}, []string{"acme/bars"}, "hf", &stderr); err == nil {
		t.Fatal("--data with an image run was accepted")
	}
}

func TestOAuthDataTokensAreNamed(t *testing.T) {
	who := hubWhoami{Name: "quantbert"}
	who.Auth.Type = "oauth"
	if who.role() != "oauth" || describeHubRole(who.role()) != "a sign-in" {
		t.Fatalf("role %q", who.role())
	}
	var out strings.Builder
	printDataOrgs([]apiDataOrgStatus{{Org: "acme", Account: "quantbert", Role: "oauth", Datasets: 1}}, &out)
	if !strings.Contains(out.String(), "(token of quantbert, a sign-in)") || !strings.Contains(out.String(), "hf auth login") {
		t.Fatal(out.String())
	}
}

func TestHiDataGetRecordsInALegacyProjectFolder(t *testing.T) {
	newDataServer(t)
	fakeHFCommand(t)
	project := t.TempDir()
	if err := os.MkdirAll(filepath.Join(project, ".hifin"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(project)
	if code, _, stderr := runHi("data", "get", "dataset:acme/bars"); code != 0 || !strings.Contains(stderr, "Recorded in .hifin/data.json") {
		t.Fatalf("get: code %d\n%s", code, stderr)
	}
	if records, err := loadDataRecords(project); err != nil || len(records.Data) != 1 {
		t.Fatalf("records: %v %v", records, err)
	}
	if _, err := os.Stat(filepath.Join(project, ".hi5")); err == nil {
		t.Error("a .hi5 folder was made next to .hifin")
	}
}
