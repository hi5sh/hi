package cli

import (
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	hi "github.com/hi5sh/hi"
)

// The page checks what every app needs, and what an app's answers add.
func TestAdminNeedsChecks(t *testing.T) {
	a := setupAdminTest(t)
	adminNeedPresent = func(id string) bool { return id != "herdr" && id != "restic" && id != "codex" }
	olga := a.signIn(t, "Olga")
	var view adminNeedsView
	if code := a.get(t, olga, "/api/needs", &view); code != http.StatusOK {
		t.Fatal(code)
	}
	var ids []string
	for _, need := range view.Needs {
		ids = append(ids, need.ID)
	}
	if strings.Join(ids, " ") != "podman hermes herdr restic linger" || strings.Join(view.Missing, " ") != "herdr restic" {
		t.Fatalf("every app's: %+v", view)
	}
	root := map[string]bool{}
	for _, need := range view.Needs {
		root[need.ID] = need.Root
	}
	if root["herdr"] || !root["restic"] || !root["podman"] || !root["linger"] {
		t.Fatalf("which need root: %v", root)
	}
	// New app doesn't start while one is missing.
	var failed map[string]any
	if code := a.post(t, olga, "/api/new", map[string]any{"action": "start", "name": "orders"}, &failed); code != http.StatusBadRequest ||
		!strings.Contains(failed["error"].(string), "lacks herdr, restic") {
		t.Fatalf("started without herdr: %d %v", code, failed)
	}
	// An app's answers add their agents: payments has a Codex reviewer.
	if code := a.get(t, olga, "/api/needs?name=payments", &view); code != http.StatusOK || !slices.Contains(view.Missing, "codex") {
		t.Fatalf("payments': %+v", view)
	}
	if err := adminNeedsMissingError(adminNeedsOf("payments")); err == nil || !strings.Contains(err.Error(), "Codex") {
		t.Fatal(err)
	}
}

// Install runs hi install as a job; what needs root takes the password,
// which the job's sudo gets from the page, never from a file.
func TestAdminNeedsInstall(t *testing.T) {
	a := setupAdminTest(t)
	olga := a.signIn(t, "Olga")
	var job adminJob
	if code := a.post(t, olga, "/api/needs", map[string]any{"ids": []string{"herdr"}}, &job); code != http.StatusOK ||
		strings.Join(job.Args, " ") != "install herdr" || job.Env != "" || job.Title != "Installed herdr" {
		t.Fatalf("herdr: %d %+v", code, job)
	}
	var failed map[string]any
	if code := a.post(t, olga, "/api/needs", map[string]any{"ids": []string{"restic", "herdr"}}, &failed); code != http.StatusBadRequest ||
		!strings.Contains(failed["error"].(string), "sudo password") {
		t.Fatalf("root without the password: %d %v", code, failed)
	}
	if code := a.post(t, olga, "/api/needs", map[string]any{"ids": []string{"rm -rf"}}, &failed); code != http.StatusBadRequest {
		t.Fatalf("something the page doesn't install: %d %v", code, failed)
	}
	if code := a.post(t, olga, "/api/needs", map[string]any{"ids": []string{"restic", "herdr"}, "password": "s3cret pass"}, &job); code != http.StatusOK ||
		strings.Join(job.Args, " ") != "install herdr restic" || job.Env == "" || job.Title != "Installed herdr and restic" {
		t.Fatalf("with the password: %d %+v", code, job)
	}
	data, _ := os.ReadFile(job.Env)
	var env map[string]string
	if json.Unmarshal(data, &env) != nil || env[adminAskpassEnv] == "" || strings.Contains(string(data), "s3cret") {
		t.Fatalf("the job's file: %s", data)
	}
	job, _ = loadAdminJob(job.ID)
	if saved, _ := json.Marshal(job); strings.Contains(string(saved), "s3cret") {
		t.Fatalf("the job keeps the password: %s", saved)
	}
	token := env[adminAskpassEnv]

	// The job's sudo asks the page's socket, with the token.
	if err := os.MkdirAll(adminSocketDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", adminAskpassSocket())
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go a.s.serveAskpass(listener)
	var out, errs strings.Builder
	if code := adminAskpassMain([]string{token}, &out, &errs); code != 0 || out.String() != "s3cret pass\n" {
		t.Fatalf("askpass: %d %q %q", code, out.String(), errs.String())
	}
	out.Reset()
	if code := adminAskpassMain([]string{"not-the-token"}, &out, &errs); code == 0 || out.Len() > 0 {
		t.Fatalf("another token: %d %q", code, out.String())
	}

	// The runner puts a sudo first on its PATH that asks with -A, and an
	// askpass with the token; the token leaves the commands' environment,
	// and the job's end forgets the password.
	runEnv, done, err := adminJobSudo([]string{"PATH=/usr/bin", adminAskpassEnv + "=" + token, "HOME=/h"}, "/usr/local/bin/hi")
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(runEnv, "\n")
	if strings.Contains(joined, adminAskpassEnv) || !strings.Contains(joined, "HOME=/h") {
		t.Fatal(runEnv)
	}
	var dir string
	for _, value := range runEnv {
		if path, ok := strings.CutPrefix(value, "PATH="); ok {
			dir = strings.Split(path, string(os.PathListSeparator))[0]
			if !strings.HasSuffix(path, string(os.PathListSeparator)+"/usr/bin") {
				t.Fatal(path)
			}
		}
	}
	sudo, _ := os.ReadFile(filepath.Join(dir, "sudo"))
	askpass, _ := os.ReadFile(filepath.Join(dir, "askpass"))
	if !strings.Contains(string(sudo), "/usr/bin/sudo -A") || !strings.Contains(string(askpass), "'/usr/local/bin/hi' server __askpass '"+token+"'") ||
		!strings.Contains(joined, "SUDO_ASKPASS="+filepath.Join(dir, "askpass")) {
		t.Fatalf("%s\n%s\n%s", sudo, askpass, joined)
	}
	done()
	if fileExists(dir) {
		t.Fatal("the askpass folder stayed")
	}
	if _, ok := a.s.askpass.get(token); ok {
		t.Fatal("the page still keeps the password")
	}
	// A job without a token runs as it did.
	if same, _, _ := adminJobSudo([]string{"PATH=/usr/bin"}, "/hi"); strings.Join(same, " ") != "PATH=/usr/bin" {
		t.Fatal(same)
	}
}

// The tools the page installs without the password are the ones the
// setup script installs without sudo.
func TestInstallHomeOnlyMatchesTheScript(t *testing.T) {
	found := regexp.MustCompile(`home_only=" ([^"]*) "`).FindSubmatch(hi.SetupScript)
	if found == nil || strings.Join(strings.Fields(string(found[1])), " ") != strings.Join(installHomeOnly, " ") {
		t.Fatalf("the script's: %q", found)
	}
	for _, id := range installHomeOnly {
		if _, ok := findInstallItem(id); !ok {
			t.Fatalf("%s isn't one of hi install's", id)
		}
	}
	item, ok := findInstallItem("linger")
	if !ok || item.present == nil {
		t.Fatal("no linger item")
	}
	saved := appLingers
	appLingers = func() bool { return true }
	defer func() { appLingers = saved }()
	if !itemInstalled(item) {
		t.Fatal("lingering isn't installed when the account lingers")
	}
	if !strings.Contains(string(hi.SetupScript), `sudo loginctl enable-linger "$target_user"`) {
		t.Fatal("the script doesn't make the account linger")
	}
}
