package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The proxy puts the key on gemini-api's calls, sends an upload's session
// back through itself, and refuses what would stay running at Google.
func TestBoxGeminiInjector(t *testing.T) {
	var seen []string
	var google *httptest.Server
	google = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Method+" "+r.URL.Path+" "+r.Header.Get("X-Goog-Api-Key")+" "+r.Header.Get("Authorization"))
		if r.URL.Path == "/upload/v1beta/files" {
			w.Header().Set("X-Goog-Upload-Url", google.URL+"/upload/v1beta/files?upload_id=abc")
		}
		io.WriteString(w, `{"ok":true}`)
	}))
	defer google.Close()
	key := filepath.Join(t.TempDir(), "gemini")
	os.WriteFile(key, []byte("AIza-real\n"), 0o600)
	var logged bytes.Buffer
	proxy := httptest.NewServer(newBoxGeminiInjector(key, google.URL, &boxNetworkLog{file: &logged}))
	defer proxy.Close()
	call := func(method, path string) *http.Response {
		request, _ := http.NewRequest(method, proxy.URL+path, strings.NewReader("{}"))
		request.Header.Set("X-Goog-Api-Key", boxClaudePlacehold)
		request.Header.Set("Authorization", "Bearer agy-token")
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		return response
	}

	if response := call("POST", "/v1beta/interactions"); response.StatusCode != 200 {
		t.Fatal(response.Status)
	}
	upload := call("POST", "/upload/v1beta/files")
	if got := upload.Header.Get("X-Goog-Upload-Url"); got != proxy.URL+"/upload/v1beta/files?upload_id=abc" {
		t.Fatalf("upload session = %s", got)
	}
	call("GET", "/v1beta/agents/deep-research")
	for _, refused := range [][2]string{{"POST", "/v1beta/webhooks"}, {"POST", "/v1beta/triggers"}, {"POST", "/v1beta/credentials"},
		{"POST", "/v1beta/environments"}, {"POST", "/v1beta/agents"}, {"GET", "/v1beta/cachedContents"}} {
		if response := call(refused[0], refused[1]); response.StatusCode != http.StatusForbidden {
			t.Errorf("%v: %s", refused, response.Status)
		}
	}
	want := []string{"POST /v1beta/interactions AIza-real ", "POST /upload/v1beta/files AIza-real ", "GET /v1beta/agents/deep-research AIza-real "}
	if strings.Join(seen, "|") != strings.Join(want, "|") {
		t.Fatalf("Google saw %q", seen)
	}
	if !strings.Contains(logged.String(), "gemini POST /v1beta/interactions") || !strings.Contains(logged.String(), "gemini-refused POST /v1beta/webhooks") ||
		strings.Contains(logged.String(), "AIza-real") {
		t.Fatalf("log: %s", logged.String())
	}
}

// An agy box gets gemini-api, its skill, and the proxy's address only when
// this machine has gemini-api and a key.
func TestAgentAgyGemini(t *testing.T) {
	_, calls, _ := agentTestSetup(t)
	home, _ := os.UserHomeDir()
	bin := filepath.Join(home, ".local", "bin")
	os.WriteFile(filepath.Join(bin, "agy"), []byte("#!/bin/sh\n"), 0o755)
	t.Setenv("PATH", bin+":/usr/bin:/bin")
	os.MkdirAll(filepath.Dir(agyTokenPath()), 0o700)
	os.WriteFile(agyTokenPath(), []byte(`{"auth_method":"consumer"}`), 0o600)
	run := func(name string) (string, string) {
		var stdout, stderr bytes.Buffer
		if status := runAgent([]string{"agy", "--detach", "--name", name, "make", "a video"}, strings.NewReader(""), &stdout, &stderr); status != 0 {
			t.Fatalf("status %d: %s", status, stderr.String())
		}
		var box, proxy string
		for _, call := range *calls {
			if call[0] == "run" && strings.Contains(strings.Join(call, " "), "hi-box-"+name+" ") {
				box = strings.Join(call, " ")
			}
			if call[0] == "create" && strings.Contains(strings.Join(call, " "), "hi-box-"+name+"-proxy") {
				proxy = strings.Join(call, " ")
			}
		}
		return box, proxy
	}
	box, proxy := run("n1")
	if strings.Contains(box, "gemini-api") || strings.Contains(proxy, "gemini:") {
		t.Fatalf("gemini-api without a key:\n%s\n%s", box, proxy)
	}

	os.WriteFile(filepath.Join(bin, "gemini-api"), []byte("#!/bin/sh\n"), 0o755)
	if err := geminiTokenCommand(strings.NewReader("AIzaSyExampleExampleExample0\n"), io.Discard); err != nil {
		t.Fatal(err)
	}
	box, proxy = run("g1")
	if !strings.Contains(box, ":/opt/gemini-api/gemini-api:ro") || !strings.Contains(box, "gemini-api:/usr/local/bin/gemini-api:ro") ||
		!strings.Contains(box, "GEMINI_API_KEY="+boxClaudePlacehold) || !strings.Contains(box, geminiBoxEnv+"=http://") || strings.Contains(box, "AIzaSyExample") {
		t.Fatalf("box: %s", box)
	}
	if !strings.Contains(proxy, geminiKeyPath()+":/secrets/gemini:ro") || !strings.Contains(proxy, "gemini:/secrets/gemini") {
		t.Fatalf("proxy: %s", proxy)
	}
	if skill, _ := os.ReadFile(boxStateFile("g1", "home", ".gemini", "antigravity-cli", "skills", "gemini-api", "SKILL.md")); !strings.Contains(string(skill), "name: gemini-api") {
		t.Fatalf("skill: %s", skill)
	}
	if wrapper, _ := os.ReadFile(boxStateFile("g1", "gemini-api")); !strings.Contains(string(wrapper), `--server-url "$HI_GEMINI_URL"`) {
		t.Fatalf("wrapper: %s", wrapper)
	}
}
