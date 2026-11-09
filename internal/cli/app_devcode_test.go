package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// VS Code on the dev page: fetched once into the box's home, started on
// its socket, and passed through for the owners only.
func TestTeamDevCode(t *testing.T) {
	b, _ := appTestDevPage(t)
	b.running = func(container string) bool { return container == appDevContainer("payments") }
	handler := b.devPageHandler()

	// A release with what code-server has, and two names that would leave
	// its folder.
	var archive bytes.Buffer
	gz := gzip.NewWriter(&archive)
	tw := tar.NewWriter(gz)
	prefix := "code-server-" + appCodeServerVersion + "-linux-amd64/"
	for name, body := range map[string]string{prefix + "bin/code-server": "#!/bin/sh\n", prefix + "../escape": "no", prefix + "lib/node": "node"} {
		tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg})
		tw.Write([]byte(body))
	}
	tw.WriteHeader(&tar.Header{Name: prefix + "lib/passwd", Linkname: "../../../../etc/passwd", Typeflag: tar.TypeSymlink})
	tw.Close()
	gz.Close()
	fetched := 0
	previousFetch, previousStart, previousWait := appDevCodeFetch, appDevCodeStart, appDevCodeWait
	appDevCodeFetch = func(ctx context.Context, url string) (io.ReadCloser, error) {
		fetched++
		if !strings.Contains(url, "/v"+appCodeServerVersion+"/") {
			t.Error(url)
		}
		return io.NopCloser(bytes.NewReader(archive.Bytes())), nil
	}
	var started []string
	appDevCodeStart = func(team string, command ...string) error {
		started = append(started, strings.Join(command, " "))
		listener, err := net.Listen("unix", appDevCodeSocket(team))
		if err != nil {
			return err
		}
		server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, r.Method+" "+r.URL.RequestURI()+" host="+r.Host)
		})}
		go server.Serve(listener)
		t.Cleanup(func() { server.Close() })
		return nil
	}
	appDevCodeWait = 5 * time.Second
	t.Cleanup(func() {
		appDevCodeFetch, appDevCodeStart, appDevCodeWait = previousFetch, previousStart, previousWait
	})
	os.MkdirAll(appDevSocketDir("payments"), 0o700)

	if code, _ := appTestDevPageCall(t, handler, appTestAskerKey, "POST", "api/code/start", map[string]any{}); code == 200 {
		t.Fatal("someone who isn't an owner started VS Code")
	}
	if code, body := appTestDevPageCall(t, handler, appTestOwnerKey, "POST", "api/code/start", map[string]any{}); code != 200 {
		t.Fatal(code, body)
	}
	dir, box := appDevCodeHome("payments")
	if !fileExists(filepath.Join(dir, "bin", "code-server")) || fileExists(filepath.Join(filepath.Dir(dir), "escape")) || fileExists(filepath.Join(dir, "lib", "passwd")) {
		t.Fatal("unpacked wrong")
	}
	if len(started) != 1 || !strings.Contains(started[0], box+"/bin/code-server' '--socket' '/box/run/code.sock'") || !strings.Contains(started[0], "'--auth' 'none'") {
		t.Fatalf("%q", started)
	}
	settings, _ := os.ReadFile(filepath.Join(appDevDir("payments"), "home", ".local", "share", "code-server", "User", "settings.json"))
	if !strings.Contains(string(settings), `"files.autoSave": "afterDelay"`) || !strings.Contains(string(settings), appDevCodeTheme) {
		t.Fatal(string(settings))
	}
	if allow, _ := os.ReadFile(appDevAllowFile("payments")); !strings.Contains(string(allow), "open-vsx.org") {
		t.Fatal(string(allow))
	}
	// Running, it isn't fetched or started again, but the box's list is
	// this hi's, and settings from an older hi move to the theme: the
	// system's light or dark, or v2.5.52's Visual Studio Dark.
	os.WriteFile(appDevAllowFile("payments"), []byte("github.com\n"), 0o600)
	settingsPath := filepath.Join(appDevDir("payments"), "home", ".local", "share", "code-server", "User", "settings.json")
	for _, old := range appDevCodeOldThemes {
		os.WriteFile(settingsPath, []byte("{\n  "+old+"\n  \"editor.fontSize\": 15\n}\n"), 0o600)
		appTestDevPageCall(t, handler, appTestOwnerKey, "POST", "api/code/start", map[string]any{})
		if settings, _ := os.ReadFile(settingsPath); string(settings) != "{\n  "+appDevCodeTheme+"\n  \"editor.fontSize\": 15\n}\n" {
			t.Fatal(string(settings))
		}
	}
	if fetched != 1 || len(started) != 1 {
		t.Fatal(fetched, started)
	}
	if allow, _ := os.ReadFile(appDevAllowFile("payments")); !strings.Contains(string(allow), "openvsx.eclipsecontent.org") {
		t.Fatal(string(allow))
	}
	// Passed through without the page's path, keeping the Host; owners only.
	code, body := appTestDevPageCall(t, handler, appTestOwnerKey, "GET", "code/static/out/x.js?v=1", nil)
	if code != 200 || !strings.HasPrefix(body, "GET /static/out/x.js?v=1 host=") {
		t.Fatal(code, body)
	}
	if code, body := appTestDevPageCall(t, handler, appTestOwnerKey, "POST", "code/mint-key", map[string]any{}); code != 200 || !strings.HasPrefix(body, "POST /mint-key") {
		t.Fatal(code, body)
	}
	// Behind NetBird's proxy the Host isn't the page's: code-server gets
	// the page's own when the Origin is it, and not another site's.
	page, _ := json.Marshal(appDevPageState{URL: "http://payments-dev.corp.example:8888/?hi_key=x"})
	os.MkdirAll(filepath.Dir(appDevPageStatePath("payments")), 0o700)
	os.WriteFile(appDevPageStatePath("payments"), page, 0o600)
	for origin, want := range map[string]string{"http://payments-dev.corp.example:8888": "host=payments-dev.corp.example:8888", "http://evil.example": "host=example.com"} {
		request := httptest.NewRequest("GET", appDevPagePrefix+"code/stable-1", nil)
		request.Header.Set(netExposeUserHeader, appTestOwnerKey)
		request.Header.Set("Origin", origin)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if !strings.HasSuffix(recorder.Body.String(), want) {
			t.Errorf("%s: %s", origin, recorder.Body.String())
		}
	}
	if code, _ := appTestDevPageCall(t, handler, appTestOwnerKey, "GET", "code", nil); code != http.StatusFound {
		t.Fatal("code without its slash:", code)
	}
	if code, body := appTestDevPageCall(t, handler, appTestAskerKey, "GET", "code/", nil); code != http.StatusUnauthorized || strings.Contains(body, "GET /") {
		t.Fatal("someone who isn't an owner reached VS Code:", code, body)
	}
}

// The extensions go in with one call, again only when hi's list changes.
func TestTeamDevCodeExtensions(t *testing.T) {
	b, _ := appTestDevPage(t)
	var calls [][]string
	previous := appDevBoxExec
	appDevBoxExec = func(team string, command ...string) (string, error) {
		calls = append(calls, command)
		return "", nil
	}
	t.Cleanup(func() { appDevBoxExec = previous })

	b.devCodeExtensions("/code", appDevCodeExtensions)
	if len(calls) != 1 {
		t.Fatalf("%d installs, want 1", len(calls))
	}
	command := strings.Join(calls[0], " ")
	for _, extension := range appDevCodeExtensions {
		if !strings.Contains(command, "--install-extension "+extension) {
			t.Errorf("%s not installed: %s", extension, command)
		}
	}
	for _, extension := range appDevCodeExtensions[1:] {
		if !strings.Contains(extension, "@") {
			t.Errorf("%s isn't pinned to a version", extension)
		}
	}

	b.devCodeExtensions("/code", appDevCodeExtensions)
	if len(calls) != 1 {
		t.Error("the same list was installed again")
	}
	saved := appDevCodeExtensions
	t.Cleanup(func() { appDevCodeExtensions = saved })
	appDevCodeExtensions = append(append([]string{}, saved...), "example.viewer@1.0.0")
	b.devCodeExtensions("/code", appDevCodeExtensions)
	if len(calls) != 2 {
		t.Error("a changed list wasn't installed")
	}
}
