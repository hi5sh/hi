package cli

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

const testRevision = "766911a6b7369840a91dbcd95f9f997acaab6cd6"

// fakeLocal is this machine as hi serve --on local sees it: a Strix Halo
// with Podman, systemd, the Hub, and a server answering on its port.
type fakeLocal struct {
	t        *testing.T
	mu       sync.Mutex
	root     string
	files    map[string][]byte // the Hub's files, by path in the repository
	gets     int               // downloads the Hub served
	cut      bool              // the next download breaks halfway
	podman   []string
	system   []string
	active   map[string]string // unit -> ActiveState
	status   map[string]string // unit -> ExecMainStatus
	images   map[string]bool
	health   int // answers until /health says ready
	port     int
	journals []string
	private  bool            // the Hub shows the model only with the org's data token
	hubAuth  []string        // Authorization on the Hub's requests
	front    []*http.Request // requests to /v1 on the front
	answer   map[string]any  // the front's chat answer
}

func newFakeLocal(t *testing.T) *fakeLocal {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	f := &fakeLocal{t: t, root: t.TempDir(), active: map[string]string{}, status: map[string]string{}, images: map[string]bool{},
		files: map[string][]byte{
			"UD-Q3_K_XL/Qwen3.8-Flash-Next-UD-Q3_K_XL-00001-of-00002.gguf": bytes.Repeat([]byte("a"), 3<<20),
			"UD-Q3_K_XL/Qwen3.8-Flash-Next-UD-Q3_K_XL-00002-of-00002.gguf": bytes.Repeat([]byte("b"), 5<<20),
			"Qwen3.8-Flash-Next-Q3_K_XL.gguf":                              []byte("not this quant"),
			"mmproj-F16.gguf":                                              []byte("not the projector"),
		}}
	for _, dir := range []string{"sys/class/kfd/kfd/topology/nodes/0", "sys/class/kfd/kfd/topology/nodes/1", "dev/dri", "proc"} {
		os.MkdirAll(filepath.Join(f.root, dir), 0o755)
	}
	os.WriteFile(filepath.Join(f.root, "sys/class/kfd/kfd/topology/nodes/0/properties"), []byte("cpu_cores_count 32\ngfx_target_version 0\n"), 0o644)
	os.WriteFile(filepath.Join(f.root, "sys/class/kfd/kfd/topology/nodes/1/properties"), []byte("simd_count 80\ngfx_target_version 110501\n"), 0o644)
	os.WriteFile(filepath.Join(f.root, "dev/kfd"), nil, 0o644)
	f.memory(128, 120)

	hub := httptest.NewServer(http.HandlerFunc(f.serveHub))
	t.Cleanup(hub.Close)
	health := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if strings.HasPrefix(r.URL.Path, "/v1/") {
			// The front, as the org server reaches it: the box's API.
			f.front = append(f.front, r.Clone(r.Context()))
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			f.front[len(f.front)-1].Form = url.Values{"model": {fmt.Sprint(body["model"])}}
			if r.URL.Path == "/v1/models" {
				fmt.Fprint(w, `{"data":[{"id":"qwen3.8-flash-next"}]}`)
				return
			}
			json.NewEncoder(w).Encode(f.answer)
			return
		}
		if f.health > 0 {
			f.health--
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		fmt.Fprint(w, `{"status":"ok"}`)
	}))
	t.Cleanup(health.Close)
	address, _ := url.Parse(health.URL)
	f.port, _ = strconv.Atoi(address.Port())

	saved := []any{serveHub, serveSysRoot, serveSystemctl, serveJournal, serveLingers, serveGroupNames, servePortFree,
		serveHealthEvery, serveExecutable, boxCommand, boxLookPath, serveRetryScale}
	savedShare, savedOrg, savedData, savedRange := serveNetBirdAddress, serveOrgClient, serveDataToken, netbirdRange
	t.Cleanup(func() {
		serveNetBirdAddress, serveOrgClient, serveDataToken, netbirdRange = savedShare, savedOrg, savedData, savedRange
		serveHub, serveSysRoot = saved[0].(string), saved[1].(string)
		serveSystemctl = saved[2].(func(...string) (string, error))
		serveJournal = saved[3].(func(io.Writer, io.Writer, ...string) error)
		serveLingers = saved[4].(func() bool)
		serveGroupNames = saved[5].(func() []string)
		servePortFree = saved[6].(func(string, int) bool)
		serveHealthEvery = saved[7].(time.Duration)
		serveExecutable = saved[8].(func() (string, error))
		boxCommand = saved[9].(func(io.Reader, io.Writer, io.Writer, string, ...string) error)
		boxLookPath = saved[10].(func(string) (string, error))
		serveRetryScale = saved[11].(time.Duration)
	})
	serveHub, serveSysRoot, serveHealthEvery, serveRetryScale = hub.URL, f.root, time.Millisecond, 0
	serveLingers = func() bool { return true }
	serveGroupNames = func() []string { return []string{"hi", "render", "video"} }
	servePortFree = func(string, int) bool { return true }
	serveNetBirdAddress = func() (string, error) { return "", errors.New("no NetBird interface (wt0) found") }
	serveDataToken = func(string) (string, string, error) { return "", "", errors.New("not connected to an org server") }
	serveExecutable = func() (string, error) { return "/home/hi/.local/bin/hi", nil }
	boxLookPath = func(name string) (string, error) {
		if name == "podman" {
			return "/usr/bin/podman", nil
		}
		return "", fmt.Errorf("no %s", name)
	}
	boxCommand = f.runPodman
	serveSystemctl = f.systemctl
	serveJournal = func(stdout, stderr io.Writer, args ...string) error {
		f.mu.Lock()
		f.journals = append(f.journals, strings.Join(args, " "))
		f.mu.Unlock()
		fmt.Fprintln(stdout, "llama-server log line")
		return nil
	}
	return f
}

func (f *fakeLocal) memory(totalGB, availableGB int) {
	os.WriteFile(filepath.Join(f.root, "proc/meminfo"),
		[]byte(fmt.Sprintf("MemTotal:       %d kB\nMemFree:  1 kB\nMemAvailable:   %d kB\n", totalGB<<20, availableGB<<20)), 0o644)
}

func (f *fakeLocal) serveHub(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	repo := "/unsloth/Qwen3.8-Flash-Next-GGUF"
	f.hubAuth = append(f.hubAuth, r.Header.Get("Authorization"))
	if f.private && r.Header.Get("Authorization") != "Bearer data-token" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	switch {
	case r.URL.Path == "/api/models"+repo+"/revision/"+testRevision || r.URL.Path == "/api/models"+repo+"/revision/main":
		var siblings []map[string]any
		for name, data := range f.files {
			sum := sha256.Sum256(data)
			siblings = append(siblings, map[string]any{"rfilename": name, "size": len(data),
				"lfs": map[string]any{"sha256": hex.EncodeToString(sum[:]), "size": len(data)}})
		}
		siblings = append(siblings, map[string]any{"rfilename": "README.md", "size": 10})
		json.NewEncoder(w).Encode(map[string]any{"sha": testRevision, "gated": false,
			"cardData": map[string]any{"license": "apache-2.0"}, "siblings": siblings})
	case strings.HasPrefix(r.URL.Path, repo+"/resolve/"+testRevision+"/"):
		name := strings.TrimPrefix(r.URL.Path, repo+"/resolve/"+testRevision+"/")
		data, ok := f.files[name]
		if !ok {
			http.NotFound(w, r)
			return
		}
		f.gets++
		if f.cut && r.Header.Get("Range") == "" {
			f.cut = false
			w.Header().Set("Content-Length", strconv.Itoa(len(data)))
			w.WriteHeader(http.StatusOK)
			w.Write(data[:len(data)/2])
			panic(http.ErrAbortHandler)
		}
		http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(data))
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeLocal) runPodman(stdin io.Reader, stdout, stderr io.Writer, name string, args ...string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.podman = append(f.podman, strings.Join(args, " "))
	switch {
	case len(args) > 0 && args[0] == "info":
		fmt.Fprintln(stdout, "5.7.0 crun")
	case len(args) >= 2 && args[0] == "image" && args[1] == "inspect":
		if !f.images[args[2]] {
			return fmt.Errorf("no such image")
		}
	case len(args) > 0 && args[0] == "pull":
		f.images[args[1]] = true
	case len(args) >= 2 && args[0] == "ps" && args[1] == "-q":
		fmt.Fprintln(stdout, "team1\nserved1")
	case len(args) > 0 && args[0] == "inspect" && strings.Contains(strings.Join(args, " "), "hi.serve"):
		os.MkdirAll(filepath.Join(f.root, "sys/fs/cgroup/team1"), 0o755)
		os.WriteFile(filepath.Join(f.root, "sys/fs/cgroup/team1/memory.current"), []byte(strconv.Itoa(2<<30)), 0o644)
		// A team's box limited to 10 GB that uses 2, and a served one.
		fmt.Fprintf(stdout, "%d|/team1|<no value>\n%d|/served1|other\n", 10<<30, 72<<30)
	}
	return nil
}

func (f *fakeLocal) systemctl(args ...string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.system = append(f.system, strings.Join(args, " "))
	switch args[0] {
	case "start":
		for _, unit := range args[1:] {
			f.active[unit] = "active"
			f.status[unit] = "0"
		}
	case "stop":
		for _, unit := range args[1:] {
			if f.active[unit] != "failed" {
				f.active[unit] = "inactive"
			}
		}
	case "show":
		active := f.active[args[1]]
		if active == "" {
			active = "inactive"
		}
		return fmt.Sprintf("LoadState=loaded\nActiveState=%s\nSubState=running\nResult=success\nExecMainStatus=%s\nActiveEnterTimestamp=Mon 2026-11-02 14:00:00 CET\n", active, f.status[args[1]]), nil
	}
	return "", nil
}

func (f *fakeLocal) calls() (string, string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return strings.Join(f.podman, "\n"), strings.Join(f.system, "\n")
}

func quadletFile(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "containers", "systemd", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// hi serve llm --on local downloads the recipe's files at its revision,
// checks them, pulls its image, writes its units, and waits until it
// answers; stop keeps it, start brings it back, and rm forgets it.
func TestServeLocalServesStopsStartsAndRemoves(t *testing.T) {
	f := newFakeLocal(t)
	f.health = 3
	port := strconv.Itoa(f.port)
	code, stdout, stderr := runHi("serve", "llm", "qwen3.8-flash-next", "--on", "local", "--port", port, "--yes")
	if code != 0 {
		t.Fatalf("serve: %d\n%s%s", code, stdout, stderr)
	}
	for _, want := range []string{
		"detect     Strix Halo (gfx1151), /dev/kfd and /dev/dri, Podman 5.7.0 with crun; 120 GB available, apps' boxes may still take 8.0 GB",
		"check      it needs 91.0 GB with its context: fits, 13.0 GB left; 8 MB to download",
		"tested on Strix Halo, 2026-11-02",
		"License: apache-2.0",
		"UD-Q3_K_XL/Qwen3.8-Flash-Next-UD-Q3_K_XL-00001-of-00002.gguf",
		"loading the model",
		"ready      OpenAI-compatible API: http://127.0.0.1:" + port + "/v1   model: qwen3.8-flash-next",
		"comes back after a reboot",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("serve output lacks %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "mmproj") || strings.Contains(stdout, "Next-Q3_K_XL.gguf") {
		t.Fatalf("other quants' files were taken:\n%s", stdout)
	}
	modelDir := filepath.Join(os.Getenv("XDG_DATA_HOME"), "hi", "models", "unsloth--Qwen3.8-Flash-Next-GGUF")
	if data, err := os.ReadFile(filepath.Join(modelDir, "UD-Q3_K_XL", "Qwen3.8-Flash-Next-UD-Q3_K_XL-00002-of-00002.gguf")); err != nil || len(data) != 5<<20 {
		t.Fatalf("the model wasn't downloaded: %v", err)
	}
	podman, system := f.calls()
	if !strings.Contains(podman, "pull ghcr.io/ggml-org/llama.cpp:server-rocm@sha256:1bc7c96d") {
		t.Fatalf("the image wasn't pulled:\n%s", podman)
	}
	if !strings.Contains(system, "daemon-reload\nstart hi-serve-qwen3-8-flash-next.service hi-serve-qwen3-8-flash-next-front.service") {
		t.Fatalf("the units weren't started:\n%s", system)
	}

	box := quadletFile(t, "hi-serve-qwen3-8-flash-next.container")
	for _, want := range []string{
		"Image=ghcr.io/ggml-org/llama.cpp:server-rocm@sha256:1bc7c96d72e2e948d30fe8ff10ee204881d86a9260290bab352c4eb770ce7756\n",
		"Exec=--host 0.0.0.0 --port 8080 -m /models/UD-Q3_K_XL/Qwen3.8-Flash-Next-UD-Q3_K_XL-00001-of-00002.gguf -ngl 999 -c 131072 --alias qwen3.8-flash-next --temp 1.0",
		"Volume=" + modelDir + ":/models:ro\n",
		"Network=hi-serve-qwen3-8-flash-next.network\n",
		"AddDevice=/dev/kfd\nAddDevice=/dev/dri\n",
		"ReadOnly=true\n", "DropCapability=ALL\n", "NoNewPrivileges=true\n",
		"PodmanArgs=--memory=73728m --oom-score-adj=1000 --group-add=keep-groups\n",
		"Label=hi.serve=qwen3-8-flash-next\n",
		"Restart=on-failure\n", "RestartPreventExitStatus=137\n",
		"[Install]\nWantedBy=default.target\n",
	} {
		if !strings.Contains(box, want) {
			t.Fatalf("the box's unit lacks %q:\n%s", want, box)
		}
	}
	if strings.Contains(box, "-ot") || strings.Contains(box, "PublishPort") {
		t.Fatalf("the box got rented arguments, or a port of its own:\n%s", box)
	}
	front := quadletFile(t, "hi-serve-qwen3-8-flash-next-front.container")
	for _, want := range []string{
		"PodmanArgs=--memory=256m --entrypoint=/usr/local/bin/hi\n",
		"Exec=serve __front :8080 http://hi-serve-qwen3-8-flash-next:8080\n",
		"Volume=/home/hi/.local/bin/hi:/usr/local/bin/hi:ro\n",
		"Network=hi-serve-qwen3-8-flash-next.network\nNetwork=hi-serve-qwen3-8-flash-next-front.network\n",
		"PublishPort=127.0.0.1:" + port + ":8080\n",
		"ReadOnly=true\n", "DropCapability=ALL\n",
	} {
		if !strings.Contains(front, want) {
			t.Fatalf("the front's unit lacks %q:\n%s", want, front)
		}
	}
	if network := quadletFile(t, "hi-serve-qwen3-8-flash-next.network"); !strings.Contains(network, "Internal=true\n") {
		t.Fatalf("the box's network has a way out:\n%s", network)
	}

	code, stdout, _ = runHi("serve", "ls")
	if code != 0 || !strings.Contains(stdout, "qwen3-8-flash-next  local  qwen3.8-flash-next  http://127.0.0.1:"+port+"/v1  running  no limit") {
		t.Fatalf("ls: %s", stdout)
	}
	code, stdout, _ = runHi("serve", "status", "qwen3-8-flash-next")
	if code != 0 || !strings.Contains(stdout, "State:    running") || !strings.Contains(stdout, "needs 91.0 GB in all; the box's host side is limited to 72.0 GB") {
		t.Fatalf("status: %s", stdout)
	}
	code, stdout, _ = runHi("serve", "models")
	if code != 0 || !strings.Contains(stdout, "unsloth/Qwen3.8-Flash-Next-GGUF  8 MB  qwen3-8-flash-next") {
		t.Fatalf("models: %s", stdout)
	}
	if code, _, stderr := runHi("serve", "models", "rm", "unsloth/Qwen3.8-Flash-Next-GGUF", "--yes"); code == 0 || !strings.Contains(stderr, "is used by qwen3-8-flash-next") {
		t.Fatalf("models rm while served: %d %s", code, stderr)
	}
	if code, _, _ := runHi("serve", "logs", "qwen3-8-flash-next"); code != 0 || !strings.Contains(strings.Join(f.journals, "\n"), "-u hi-serve-qwen3-8-flash-next.service -u hi-serve-qwen3-8-flash-next-front.service -n 40") {
		t.Fatalf("logs: %v", f.journals)
	}

	// Stopped, it is kept, and doesn't start at boot.
	code, stdout, stderr = runHi("serve", "stop", "qwen3-8-flash-next")
	if code != 0 || !strings.Contains(stdout, "Stopped qwen3-8-flash-next, and kept it") {
		t.Fatalf("stop: %d %s%s", code, stdout, stderr)
	}
	if box := quadletFile(t, "hi-serve-qwen3-8-flash-next.container"); strings.Contains(box, "[Install]") {
		t.Fatalf("a stopped box still starts at boot:\n%s", box)
	}
	if _, stdout, _ = runHi("serve", "ls"); !strings.Contains(stdout, "  stopped  ") {
		t.Fatalf("ls after stop: %s", stdout)
	}
	gets := f.gets
	code, stdout, stderr = runHi("serve", "start", "qwen3-8-flash-next")
	if code != 0 || !strings.Contains(stdout, "ready") || f.gets != gets {
		t.Fatalf("start: %d %s%s (downloads %d → %d)", code, stdout, stderr, gets, f.gets)
	}
	if box := quadletFile(t, "hi-serve-qwen3-8-flash-next.container"); !strings.Contains(box, "[Install]") || !strings.Contains(box, ":"+port+":") && !strings.Contains(quadletFile(t, "hi-serve-qwen3-8-flash-next-front.container"), ":"+port+":") {
		t.Fatalf("start didn't bring it back as it was:\n%s", box)
	}

	code, stdout, stderr = runHi("serve", "rm", "qwen3-8-flash-next")
	if code != 0 || !strings.Contains(stdout, "Removed qwen3-8-flash-next") {
		t.Fatalf("rm: %d %s%s", code, stdout, stderr)
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "containers", "systemd", "hi-serve-qwen3-8-flash-next.container")); err == nil {
		t.Fatal("rm left the units")
	}
	podman, _ = f.calls()
	if !strings.Contains(podman, "ps -aq --filter label=hi.serve=qwen3-8-flash-next") || !strings.Contains(podman, "network ls -q --filter label=hi.serve=qwen3-8-flash-next") {
		t.Fatalf("rm didn't clean up by label:\n%s", podman)
	}
	if _, stdout, _ = runHi("serve", "ls"); !strings.Contains(stdout, "No models are being served") {
		t.Fatalf("ls after rm: %s", stdout)
	}
	if code, stdout, stderr := runHi("serve", "models", "rm", "unsloth/Qwen3.8-Flash-Next-GGUF", "--yes"); code != 0 {
		t.Fatalf("models rm: %d %s%s", code, stdout, stderr)
	}
	if _, err := os.Stat(modelDir); err == nil {
		t.Fatal("models rm left the model")
	}
}

// A model that doesn't fit in what teams leave is refused before any
// download, with the numbers, and --dry-run changes nothing.
func TestServeLocalRefusesWhatDoesntFitAndDryRunChangesNothing(t *testing.T) {
	f := newFakeLocal(t)
	f.memory(128, 90)
	code, stdout, stderr := runHi("serve", "llm", "qwen3.8-flash-next", "--on", "local", "--yes")
	if code == 0 || !strings.Contains(stderr, "it needs 91.0 GB with its context, and this machine has 74.0 GB for it: 90.0 GB available, less 8.0 GB that apps' boxes may still take and 8.0 GB of headroom. It is 17.0 GB short") {
		t.Fatalf("too big: %d\n%s%s", code, stdout, stderr)
	}
	if _, system := f.calls(); f.gets != 0 || system != "" {
		t.Fatalf("a refused model downloaded %d files or touched systemd: %s", f.gets, system)
	}

	f.memory(128, 120)
	code, stdout, stderr = runHi("serve", "llm", "qwen3.8-flash-next", "--on", "local", "--dry-run", "--max", "2h")
	if code != 0 || !strings.Contains(stdout, "--- hi-serve-qwen3-8-flash-next.container") || !strings.Contains(stdout, "Nothing was changed") ||
		!strings.Contains(stdout, "a timer that runs hi serve stop qwen3-8-flash-next") {
		t.Fatalf("dry run: %d\n%s%s", code, stdout, stderr)
	}
	podman, system := f.calls()
	if f.gets != 0 || system != "" || strings.Contains(podman, "pull") {
		t.Fatalf("the dry run changed something: %d downloads, systemd %q, podman %q", f.gets, system, podman)
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("XDG_DATA_HOME"), "hi")); err == nil {
		t.Fatal("the dry run made the models folder")
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "containers")); err == nil {
		t.Fatal("the dry run wrote units")
	}
}

// On a machine without a usable GPU or Podman, detect and serve say what
// is missing and which hi install adds it, and start nothing.
func TestServeLocalSaysWhatIsMissing(t *testing.T) {
	f := newFakeLocal(t)
	os.RemoveAll(filepath.Join(f.root, "sys"))
	boxLookPath = func(string) (string, error) { return "", fmt.Errorf("none") }
	serveLingers = func() bool { return false }
	code, stdout, _ := runHi("serve", "detect")
	for _, want := range []string{"GPU:        none found", "hi install strix", "hi install podman", "sudo loginctl enable-linger"} {
		if code != 0 || !strings.Contains(stdout, want) {
			t.Fatalf("detect lacks %q: %d\n%s", want, code, stdout)
		}
	}
	code, _, stderr := runHi("serve", "llm", "qwen3.8-flash-next", "--on", "local", "--yes")
	if code == 0 || !strings.Contains(stderr, "can't serve it yet") || !strings.Contains(stderr, "hi install podman") {
		t.Fatalf("serve: %d %s", code, stderr)
	}
	if _, system := f.calls(); f.gets != 0 || system != "" {
		t.Fatalf("started something: %s", system)
	}
}

// A box the kernel killed for memory shows as such, and --args can't set
// where the server listens.
func TestServeLocalOutOfMemoryAndArgs(t *testing.T) {
	f := newFakeLocal(t)
	port := strconv.Itoa(f.port)
	if code, stdout, stderr := runHi("serve", "llm", "qwen3.8-flash-next", "--on", "local", "--port", port, "--yes", "--name", "big"); code != 0 {
		t.Fatalf("serve: %s%s", stdout, stderr)
	}
	f.mu.Lock()
	f.active["hi-serve-big.service"], f.status["hi-serve-big.service"] = "failed", "137"
	f.mu.Unlock()
	if _, stdout, _ := runHi("serve", "ls"); !strings.Contains(stdout, "stopped: out of memory") {
		t.Fatalf("ls: %s", stdout)
	}
	if _, stdout, _ := runHi("serve", "status", "big"); !strings.Contains(stdout, "checks memory again first") {
		t.Fatalf("status: %s", stdout)
	}
	if code, _, stderr := runHi("serve", "llm", "qwen3.8-flash-next", "--on", "local", "--name", "other", "--args", "--port 9000", "--yes"); code == 0 || !strings.Contains(stderr, "--port is set by hi") {
		t.Fatalf("--args --port: %d %s", code, stderr)
	}
	if code, _, stderr := runHi("serve", "llm", "qwen3.8-flash-next", "--on", "local", "--name", "big", "--yes"); code == 0 || !strings.Contains(stderr, "already served here") {
		t.Fatalf("same name: %d %s", code, stderr)
	}
	if code, _, stderr := runHi("serve", "llm", "qwen3.8-flash-next", "--on", "local", "--name", "models", "--yes"); code == 0 || !strings.Contains(stderr, "hi serve's own words") {
		t.Fatalf("a verb as a name: %d %s", code, stderr)
	}
}

// A download that breaks resumes where it stopped, and a file that doesn't
// match the Hub's SHA-256 is never moved into place.
func TestServeDownloadResumesAndChecks(t *testing.T) {
	f := newFakeLocal(t)
	f.cut = true
	var out bytes.Buffer
	model, err := hubModelFiles("unsloth/Qwen3.8-Flash-Next-GGUF", testRevision, "UD-Q3_K_XL")
	if err != nil || len(model.Files) != 2 {
		t.Fatalf("files: %v %v", model.Files, err)
	}
	dir := t.TempDir()
	if err := downloadModel(dir, model, &out); err != nil {
		t.Fatalf("download: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "resuming") {
		t.Fatalf("no resume:\n%s", out.String())
	}
	if toDownload(dir, model) != 0 {
		t.Fatal("a downloaded model still has something to download")
	}

	bad := model
	bad.Files = []hubFile{model.Files[0]}
	bad.Files[0].SHA256 = strings.Repeat("0", 64)
	other := t.TempDir()
	if err := downloadModel(other, bad, &out); err == nil || !strings.Contains(err.Error(), "doesn't match the Hub's SHA-256") {
		t.Fatalf("a bad file: %v", err)
	}
	entries, _ := filepath.Glob(filepath.Join(other, "*", "*"))
	if len(entries) != 0 {
		t.Fatalf("a bad file was kept: %v", entries)
	}
}

func TestQuantFileAndQuadletValue(t *testing.T) {
	for name, want := range map[string]bool{
		"UD-Q3_K_XL/Qwen-UD-Q3_K_XL-00001-of-00003.gguf": true,
		"Qwen3-0.6B-Q4_K_M.gguf":                         true,
		"Qwen3-0.6B-UD-Q4_K_M.gguf":                      false,
		"mmproj-Q4_K_M.gguf":                             false,
		"Qwen3-0.6B-Q4_K_M.md":                           false,
		"Qwen3-0.6B-IQ4_K_M.gguf":                        false,
	} {
		if got := quantFile(name, strings.Split("Q4_K_M UD-Q3_K_XL", " ")[map[bool]int{true: 1, false: 0}[strings.Contains(name, "UD-Q3")]]); got != want {
			t.Errorf("quantFile(%q) = %v", name, got)
		}
	}
	if got := quadletValue(`per_layer_token_embd\.weight=CPU`); got != `"per_layer_token_embd\\.weight=CPU"` {
		t.Errorf("quadletValue = %s", got)
	}
	if got := quadletValue("50%"); got != "50%%" {
		t.Errorf("quadletValue = %s", got)
	}
}

// The front passes a streamed answer on as it comes.
func TestServeFrontStreams(t *testing.T) {
	release := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "data: first\n\n")
		w.(http.Flusher).Flush()
		<-release
		fmt.Fprint(w, "data: second\n\n")
	}))
	defer upstream.Close()
	address, _ := url.Parse(upstream.URL)
	front := httptest.NewServer(newServeFront(address, io.Discard))
	defer front.Close()
	response, err := http.Get(front.URL + "/v1/chat/completions")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	line, err := bufio.NewReader(response.Body).ReadString('\n')
	close(release)
	if err != nil || line != "data: first\n" {
		t.Fatalf("the first chunk didn't arrive before the answer ended: %q %v", line, err)
	}
}
