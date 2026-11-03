package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// savedOrgClient is the device's own way to the org server, which the
// local fake replaces.
var savedOrgClient = serveOrgClient

// --share publishes the front on the NetBird address with a key that only
// the front sees, prints what to hand over once, and hi serve key prints it
// again or replaces it.
func TestServeLocalShares(t *testing.T) {
	f := newFakeLocal(t)
	serveNetBirdAddress = func() (string, error) { return "100.70.1.2", nil }
	var free []string
	servePortFree = func(host string, port int) bool {
		free = append(free, fmt.Sprintf("%s:%d", host, port))
		return true
	}
	port := strconv.Itoa(f.port)
	code, stdout, stderr := runHi("serve", "llm", "qwen3.8-flash-next", "--on", "local", "--share", "--port", port, "--yes")
	if code != 0 {
		t.Fatalf("serve: %d\n%s%s", code, stdout, stderr)
	}
	if strings.Join(free, " ") != "127.0.0.1:"+port+" 100.70.1.2:"+port {
		t.Fatalf("the port was checked on %v", free)
	}
	key := readServeKey("qwen3-8-flash-next", serveSharedKey)
	if !strings.HasPrefix(key, "hi-serve-") || len(key) < 40 {
		t.Fatalf("key = %q", key)
	}
	for _, want := range []string{
		"front on 127.0.0.1 and 100.70.1.2:" + port,
		"OPENAI_BASE_URL=http://100.70.1.2:" + port + "/v1\n",
		"OPENAI_API_KEY=" + key + "\n",
		"hi serve key qwen3-8-flash-next prints it",
		"NetBird's access control",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("serve output lacks %q:\n%s", want, stdout)
		}
	}
	dir := serveKeysDir("qwen3-8-flash-next")
	if info, err := os.Stat(dir); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("keys folder: %v %v", info, err)
	}
	if info, err := os.Stat(filepath.Join(dir, serveSharedKey)); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("key file: %v %v", info, err)
	}
	if fileExists(filepath.Join(dir, serveOrgKey)) {
		t.Fatal("--share alone made the org's key")
	}
	front := quadletFile(t, "hi-serve-qwen3-8-flash-next-front.container")
	for _, want := range []string{
		"Exec=serve __front :8080 http://hi-serve-qwen3-8-flash-next:8080 --shared :8081 --keys /run/hi/keys\n",
		"Volume=" + dir + ":/run/hi/keys:ro\n",
		"PublishPort=127.0.0.1:" + port + ":8080\nPublishPort=100.70.1.2:" + port + ":8081\n",
		"StartLimitIntervalSec=0\n", "RestartSec=10\n",
	} {
		if !strings.Contains(front, want) {
			t.Fatalf("the front's unit lacks %q:\n%s", want, front)
		}
	}
	box := quadletFile(t, "hi-serve-qwen3-8-flash-next.container")
	if strings.Contains(box, "keys") || strings.Contains(box, key) || strings.Contains(front, key) {
		t.Fatalf("the key reached the box, or a unit:\n%s\n%s", box, front)
	}

	if code, stdout, _ := runHi("serve", "ls"); code != 0 || !strings.Contains(stdout, "local (shared)") {
		t.Fatalf("ls: %s", stdout)
	}
	code, stdout, _ = runHi("serve", "status", "qwen3-8-flash-next")
	if code != 0 || !strings.Contains(stdout, "Shared:   http://100.70.1.2:"+port+"/v1, with a key") ||
		!strings.Contains(stdout, "OPENAI_API_KEY=$(hi serve key qwen3-8-flash-next)") || strings.Contains(stdout, key) {
		t.Fatalf("status: %s", stdout)
	}
	if code, stdout, _ := runHi("serve", "key", "qwen3-8-flash-next"); code != 0 || stdout != key+"\n" {
		t.Fatalf("key: %d %q", code, stdout)
	}
	code, stdout, stderr = runHi("serve", "key", "qwen3-8-flash-next", "--new")
	if code != 0 || strings.TrimSpace(stdout) == key || strings.TrimSpace(stdout) != readServeKey("qwen3-8-flash-next", serveSharedKey) ||
		!strings.Contains(stderr, "the old one stops working") {
		t.Fatalf("key --new: %d %q %s", code, stdout, stderr)
	}

	// Started again, it asks NetBird for the address again.
	serveNetBirdAddress = func() (string, error) { return "100.70.9.9", nil }
	runHi("serve", "stop", "qwen3-8-flash-next")
	code, stdout, stderr = runHi("serve", "start", "qwen3-8-flash-next")
	if code != 0 || !strings.Contains(stdout, "Shared, with the same key, at http://100.70.9.9:"+port+"/v1") ||
		!strings.Contains(quadletFile(t, "hi-serve-qwen3-8-flash-next-front.container"), "PublishPort=100.70.9.9:"+port+":8081\n") {
		t.Fatalf("start: %d %s%s", code, stdout, stderr)
	}

	if code, _, _ := runHi("serve", "rm", "qwen3-8-flash-next"); code != 0 || fileExists(dir) {
		t.Fatalf("rm left the keys: %d", code)
	}
}

// Without NetBird, --share starts nothing; a server that isn't shared has
// no key to print.
func TestServeLocalShareNeedsNetBird(t *testing.T) {
	f := newFakeLocal(t)
	code, _, stderr := runHi("serve", "llm", "qwen3.8-flash-next", "--on", "local", "--share", "--yes")
	if code == 0 || !strings.Contains(stderr, "publish on this machine's NetBird address: no NetBird interface") || f.gets != 0 {
		t.Fatalf("share without NetBird: %d %s", code, stderr)
	}
	if code, _, stderr := runHi("serve", "llm", "qwen3.8-flash-next", "--on", "local", "--org", "--yes"); code == 0 || !strings.Contains(stderr, "NetBird") {
		t.Fatalf("org without NetBird: %d %s", code, stderr)
	}
	if code, _, stderr := runHi("serve", "llm", "qwen3.8-flash-next", "--on", "colab", "--share"); code == 0 || !strings.Contains(stderr, "--share is for --on local") {
		t.Fatalf("share on colab: %d %s", code, stderr)
	}
	port := strconv.Itoa(f.port)
	if code, stdout, stderr := runHi("serve", "llm", "qwen3.8-flash-next", "--on", "local", "--port", port, "--yes"); code != 0 {
		t.Fatalf("serve: %d %s%s", code, stdout, stderr)
	}
	if code, _, stderr := runHi("serve", "key", "qwen3-8-flash-next"); code == 0 || !strings.Contains(stderr, "isn't shared") {
		t.Fatalf("key of a server that isn't shared: %d %s", code, stderr)
	}
}

// The front asks for no key on its first listener, and lets a request
// through its second only with one of its keys, which it doesn't pass on;
// each request gets an access line that names the key.
func TestServeFrontChecksKeysAndWritesAccessLines(t *testing.T) {
	var seen []*http.Request
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r)
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer upstream.Close()
	address, _ := url.Parse(upstream.URL)
	keysDir := t.TempDir()
	os.WriteFile(filepath.Join(keysDir, "shared"), []byte("key-one\n"), 0o600)
	os.WriteFile(filepath.Join(keysDir, "org"), []byte("key-org\n"), 0o600)
	var log bytes.Buffer
	access := &serveAccessLog{out: &log}
	proxy := newServeFront(address, io.Discard)
	local := httptest.NewServer(access.wrap(proxy, nil))
	defer local.Close()
	keys := &serveKeys{dir: keysDir}
	shared := httptest.NewServer(access.wrap(proxy, keys))
	defer shared.Close()

	post := func(base, key, user string) int {
		request, _ := http.NewRequest(http.MethodPost, base+"/v1/chat/completions", strings.NewReader(`{}`))
		if key != "" {
			request.Header.Set("Authorization", "Bearer "+key)
		}
		if user != "" {
			request.Header.Set(serveUserHeader, user)
		}
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		return response.StatusCode
	}
	if status := post(local.URL, "", ""); status != http.StatusOK {
		t.Fatalf("local: %d", status)
	}
	if status := post(shared.URL, "", ""); status != http.StatusUnauthorized {
		t.Fatalf("no key: %d", status)
	}
	if status := post(shared.URL, "key-two", ""); status != http.StatusUnauthorized {
		t.Fatalf("wrong key: %d", status)
	}
	if status := post(shared.URL, "key-one", "mallory"); status != http.StatusOK {
		t.Fatalf("shared key: %d", status)
	}
	if status := post(shared.URL, "key-org", "alice"); status != http.StatusOK {
		t.Fatalf("org key: %d", status)
	}
	http.Get(local.URL + "/health")
	if len(seen) != 4 {
		t.Fatalf("%d requests reached the box", len(seen))
	}
	for _, request := range seen[1:3] {
		if request.Header.Get("Authorization") != "" || request.Header.Get(serveUserHeader) != "" {
			t.Fatalf("the key or user reached the box: %v", request.Header)
		}
	}
	lines := log.String()
	for _, want := range []string{" local POST /v1/chat/completions 200 ", " no-key POST /v1/chat/completions 401 ",
		" shared POST /v1/chat/completions 200 ", " org:alice POST /v1/chat/completions 200 "} {
		if !strings.Contains(lines, want) {
			t.Fatalf("access lines lack %q:\n%s", want, lines)
		}
	}
	if strings.Count(lines, "no-key") != 2 || strings.Contains(lines, "mallory") || strings.Contains(lines, "/health") {
		t.Fatalf("access lines:\n%s", lines)
	}

	// A new key works without a restart, and the old one stops.
	keys.read = time.Time{}
	os.WriteFile(filepath.Join(keysDir, "shared"), []byte("key-three\n"), 0o600)
	if post(shared.URL, "key-three", "") != http.StatusOK || post(shared.URL, "key-one", "") != http.StatusUnauthorized {
		t.Fatal("the new key didn't replace the old one")
	}
}

// --org names a ready server to the org: once an admin approves it, devices
// reach it through hi org ai as local/<name>, with the org's key and their
// user, at no cost; stop marks it down, start up, and rm removes it.
func TestServeLocalNamedToTheOrg(t *testing.T) {
	f := newFakeLocal(t)
	ts, _ := newAIServer(t)
	f.answer = map[string]any{"model": "qwen3.8-flash-next", "choices": []any{map[string]any{"message": map[string]any{"content": "ok"}}},
		"usage": map[string]any{"prompt_tokens": 10, "completion_tokens": 2}}
	serveNetBirdAddress = func() (string, error) { return "127.0.0.1", nil }
	_, netbirdRange, _ = net.ParseCIDR("127.0.0.0/8")
	serveOrgClient = savedOrgClient
	port := strconv.Itoa(f.port)

	code, stdout, stderr := runHi("serve", "llm", "qwen3.8-flash-next", "--on", "local", "--org", "--port", port, "--yes",
		"--reason", "bulk tagging for the team")
	if code != 0 || !strings.Contains(stdout, "Asked the org server to add it to hi org ai as model local/qwen3-8-flash-next (s-") {
		t.Fatalf("serve --org: %d\n%s%s", code, stdout, stderr)
	}
	if strings.Contains(stdout, "OPENAI_API_KEY") || fileExists(filepath.Join(serveKeysDir("qwen3-8-flash-next"), serveSharedKey)) {
		t.Fatalf("--org alone made a key to hand over:\n%s", stdout)
	}
	id := ts.pending(t, "serve")
	ts.server.mu.Lock()
	request := *ts.server.state.Requests[id]
	orgKey := ts.server.keys[serveKeyPrefix+"qwen3-8-flash-next"]
	ts.server.mu.Unlock()
	if request.Reason != "bulk tagging for the team" || request.Endpoint != "http://127.0.0.1:"+port+"/v1" || orgKey != readServeKey("qwen3-8-flash-next", serveOrgKey) {
		t.Fatalf("request = %+v, key %q", request, orgKey)
	}
	text, blocks := renderSlackRequest(request, nil, "students", true, computeNow())
	if !strings.Contains(text, "wants to serve a model to hi org ai: `local/qwen3-8-flash-next`") || len(blocks) != 2 {
		t.Fatalf("slack: %s (%d blocks)", text, len(blocks))
	}
	if _, stdout, _ := runHi("serve", "status", "qwen3-8-flash-next"); !strings.Contains(stdout, "Org:      waiting for an admin's approval ("+id+")") {
		t.Fatalf("status before approval: %s", stdout)
	}
	client, err := serveOrgClient()
	if err != nil {
		t.Fatal(err)
	}
	chat := func() (int, string) {
		var answer map[string]any
		err := client.call(http.MethodPost, "/v1/ai/chat/completions", map[string]any{"model": "local/qwen3-8-flash-next",
			"messages": []any{map[string]string{"role": "user", "content": "hi"}}}, &answer)
		if err != nil {
			if reply, ok := err.(*serverReplyError); ok {
				return reply.status, reply.message
			}
			return 0, err.Error()
		}
		data, _ := json.Marshal(answer)
		return http.StatusOK, string(data)
	}
	if status, message := chat(); status != http.StatusServiceUnavailable || !strings.Contains(message, "no model local/qwen3-8-flash-next is served") {
		t.Fatalf("chat before approval: %d %s", status, message)
	}

	if _, err := ts.server.decide(id, "bob", true, "", ""); err != nil {
		t.Fatal(err)
	}
	if _, stdout, _ := runHi("serve", "status", "qwen3-8-flash-next"); !strings.Contains(stdout, "Org:      in hi org ai as model local/qwen3-8-flash-next") {
		t.Fatalf("status after approval: %s", stdout)
	}
	status, message := chat()
	if status != http.StatusOK || !strings.Contains(message, `"content":"ok"`) {
		t.Fatalf("chat: %d %s", status, message)
	}
	f.mu.Lock()
	var last *http.Request
	for _, request := range f.front {
		if request.URL.Path == "/v1/chat/completions" {
			last = request
		}
	}
	f.mu.Unlock()
	if last.Header.Get("Authorization") != "Bearer "+orgKey || last.Header.Get(serveUserHeader) != "alice" || last.Form.Get("model") != "qwen3.8-flash-next" {
		t.Fatalf("the front got %v, model %s", last.Header, last.Form.Get("model"))
	}
	usage, _ := os.ReadFile(filepath.Join(ts.dir, "ai_usage.jsonl"))
	if !strings.Contains(string(usage), `"model":"local/qwen3-8-flash-next","prompt_tokens":10,"completion_tokens":2,"status":200`) || strings.Contains(string(usage), "cost") {
		t.Fatalf("usage: %s", usage)
	}
	var models struct {
		Data []map[string]any `json:"data"`
	}
	if err := client.call(http.MethodGet, "/v1/ai/models", nil, &models); err != nil || models.Data[len(models.Data)-1]["id"] != "local/qwen3-8-flash-next" {
		t.Fatalf("models: %v %v", models, err)
	}
	if text := describeServed(ts.server.servedList()); !strings.Contains(text, "- local/qwen3-8-flash-next: qwen3.8-flash-next on ") || !strings.Contains(text, "by alice, up") {
		t.Fatalf("hi org ai: %s", text)
	}

	if code, _, stderr := runHi("serve", "stop", "qwen3-8-flash-next"); code != 0 || stderr != "" {
		t.Fatalf("stop: %d %s", code, stderr)
	}
	if status, message := chat(); status != http.StatusServiceUnavailable || !strings.Contains(message, "is stopped on") {
		t.Fatalf("chat while stopped: %d %s", status, message)
	}
	if code, _, stderr := runHi("serve", "start", "qwen3-8-flash-next"); code != 0 || stderr != "" {
		t.Fatalf("start: %d %s", code, stderr)
	}
	if status, _ := chat(); status != http.StatusOK {
		t.Fatalf("chat after start: %d", status)
	}
	if code, _, stderr := runHi("serve", "rm", "qwen3-8-flash-next"); code != 0 || stderr != "" {
		t.Fatalf("rm: %d %s", code, stderr)
	}
	ts.server.mu.Lock()
	_, still := ts.server.state.Served["qwen3-8-flash-next"]
	_, keyKept := ts.server.keys[serveKeyPrefix+"qwen3-8-flash-next"]
	ts.server.mu.Unlock()
	if still || keyKept {
		t.Fatal("rm left it in the org")
	}
}

// The org server reaches only NetBird addresses, refuses a name another
// user serves, and forgets a denied server's key.
func TestOrgServeRefusesAndDenies(t *testing.T) {
	if validServedURL("http://100.70.1.2:8080/v1") != nil {
		t.Fatal("a NetBird address was refused")
	}
	for _, bad := range []string{"http://10.0.0.5:8080/v1", "http://169.254.169.254/v1", "https://100.70.1.2:8080/v1",
		"http://100.70.1.2:8080/admin", "http://example.com:8080/v1", "http://100.70.1.2/v1"} {
		if validServedURL(bad) == nil {
			t.Errorf("%s was taken", bad)
		}
	}
	ts := newTestServer(t)
	ts.connectAs(t, "alice", "students")
	ts.server.state.Served = map[string]*serverServed{"taken": {Name: "taken", User: "carol", Approved: true}}
	client, err := savedOrgClient()
	if err != nil {
		t.Fatal(err)
	}
	body := apiServeUpstream{Name: "taken", Model: "m", URL: "http://100.70.1.2:8080/v1", Key: strings.Repeat("k", 30), Reason: "r", Up: true}
	if err := client.call(http.MethodPost, "/v1/serve", body, nil); err == nil || !strings.Contains(err.Error(), "carol already serves") {
		t.Fatalf("taken name: %v", err)
	}
	body.Name = "mine"
	var answer apiServeUpstream
	if err := client.call(http.MethodPost, "/v1/serve", body, &answer); err != nil || answer.State != "pending" {
		t.Fatalf("ask: %+v %v", answer, err)
	}
	if _, err := ts.server.decide(answer.Request, "bob", false, "", "not now"); err != nil {
		t.Fatal(err)
	}
	ts.server.mu.Lock()
	_, kept := ts.server.state.Served["mine"]
	_, keyKept := ts.server.keys[serveKeyPrefix+"mine"]
	ts.server.mu.Unlock()
	if kept || keyKept {
		t.Fatal("a denied server was kept")
	}
}

// A model in the team's private organizations is read through the org
// server's Hub proxy with a hi data token, not with a token of this machine.
func TestServePullsAPrivateModelThroughTheOrg(t *testing.T) {
	f := newFakeLocal(t)
	f.private = true
	if code, _, stderr := runHi("serve", "pull", "llm", "qwen3.8-flash-next", "--yes"); code == 0 || !strings.Contains(stderr, "the org server, which says: not connected") {
		t.Fatalf("private without the org: %d %s", code, stderr)
	}
	var asked string
	serveDataToken = func(repo string) (string, string, error) {
		asked = repo
		return serveHub, "data-token", nil
	}
	code, stdout, stderr := runHi("serve", "pull", "llm", "qwen3.8-flash-next", "--yes")
	if code != 0 || asked != "unsloth/Qwen3.8-Flash-Next-GGUF" || f.gets != 2 {
		t.Fatalf("pull: %d %s%s (asked %q, %d downloads)", code, stdout, stderr, asked, f.gets)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if last := f.hubAuth[len(f.hubAuth)-1]; last != "Bearer data-token" {
		t.Fatalf("the download went without the token: %q", last)
	}
}

// hi update offers to restart a front whose hi has been replaced: it runs
// the host's binary from inside its container, under another path.
func TestUpdateRestartsOldFronts(t *testing.T) {
	requireLinux(t)
	fakeReleases(t, "v0.9.0", false)
	path := installedBinary(t, "v0.7.0")
	old := filepath.Join(t.TempDir(), "hi")
	os.WriteFile(old, []byte("old"), 0o755)
	slice := "0::/user.slice/user-1000.slice/user@1000.service/app.slice/"
	fakeProc(t, map[string][3]string{
		"201": {old, "/usr/local/bin/hi\x00serve\x00__front\x00:8080\x00http://hi-serve-qwen:8080\x00", slice + "hi-serve-qwen-front.service/libpod-payload-abc\n"},
		"202": {path, "/usr/local/bin/hi\x00serve\x00__front\x00:8080\x00http://hi-serve-fresh:8080\x00", slice + "hi-serve-fresh-front.service/libpod-payload-def\n"},
		"203": {old, "/usr/local/bin/hi\x00serve\x00__front\x00:8080\x00http://x:8080\x00", slice + "something-else.service\n"},
	})
	runs := recordUpdateRuns(t)
	code, stdout, stderr := runUpdateTest("--restart")
	if code != 0 {
		t.Fatalf("code %d: %s", code, stderr)
	}
	want := "systemctl --user restart hi-serve-qwen-front.service|systemctl --user is-active --quiet hi-serve-qwen-front.service"
	if strings.Join(*runs, "|") != want || !strings.Contains(stdout, "hi-serve-qwen-front.service runs v0.9.0.") {
		t.Fatalf("runs = %q\n%s", *runs, stdout)
	}
}

// A client that leaves halfway through a stream still gets its access line.
func TestServeFrontWritesTheLineOfACutStream(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for i := 0; i < 200; i++ {
			if _, err := fmt.Fprintf(w, "data: %d %s\n\n", i, strings.Repeat("x", 4096)); err != nil {
				return
			}
			w.(http.Flusher).Flush()
			time.Sleep(5 * time.Millisecond)
		}
	}))
	defer upstream.Close()
	address, _ := url.Parse(upstream.URL)
	keysDir := t.TempDir()
	os.WriteFile(filepath.Join(keysDir, "shared"), []byte("key-one\n"), 0o600)
	var log syncBuffer
	front := httptest.NewServer((&serveAccessLog{out: &log}).wrap(newServeFront(address, io.Discard), &serveKeys{dir: keysDir}))
	defer front.Close()
	request, _ := http.NewRequest(http.MethodPost, front.URL+"/v1/chat/completions", strings.NewReader(`{}`))
	request.Header.Set("Authorization", "Bearer key-one")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Read(make([]byte, 100))
	response.Body.Close()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if strings.Contains(log.String(), " shared POST /v1/chat/completions 200 ") {
			if !strings.HasSuffix(strings.TrimSpace(log.String()), " cut") {
				t.Fatalf("line: %s", log.String())
			}
			return
		}
	}
	t.Fatalf("no access line for the cut stream: %q", log.String())
}

type syncBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *syncBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(data)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}
