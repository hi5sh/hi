package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestHermesConfigAndKey(t *testing.T) {
	dir := t.TempDir()
	writeSkillTestFile(t, filepath.Join(dir, "config.yaml"), "model:\n  default: google/gemini-3.8-flash\n  provider: openrouter\n  # a comment\n  base_url: https://openrouter.ai/api/v1\nagent:\n  default: other\n", 0o600)
	config := readHermesModelConfig(filepath.Join(dir, "config.yaml"))
	if config.Default != "google/gemini-3.8-flash" || config.Provider != "openrouter" || config.BaseURL != "https://openrouter.ai/api/v1" {
		t.Fatalf("%+v", config)
	}
	env := filepath.Join(dir, ".env")
	writeSkillTestFile(t, env, "SLACK_BOT_TOKEN=xoxb-other\nexport OPENROUTER_API_KEY=\"sk-or-real\"\n", 0o600)
	if key, err := readDotEnvValue(env, "OPENROUTER_API_KEY"); err != nil || key != "sk-or-real" {
		t.Fatalf("%q %v", key, err)
	}
	if _, err := readDotEnvValue(env, "MISSING"); err == nil {
		t.Fatal("a missing key")
	}
}

func TestBoxOpenRouterInjector(t *testing.T) {
	var seen []string
	openrouter := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.URL.Path+" "+r.Header.Get("Authorization"))
		io.WriteString(w, `{"ok":true}`)
	}))
	defer openrouter.Close()
	env := filepath.Join(t.TempDir(), ".env")
	writeSkillTestFile(t, env, "SLACK_BOT_TOKEN=xoxb-other\nOPENROUTER_API_KEY=sk-or-real\n", 0o600)
	var logged bytes.Buffer
	injector := httptest.NewServer(newBoxOpenRouterInjector(env, openrouter.URL, &boxNetworkLog{file: &logged}))
	defer injector.Close()

	for _, auth := range []string{"Bearer " + boxClaudePlacehold, "Bearer sk-or-something-else"} {
		request, _ := http.NewRequest(http.MethodPost, injector.URL+"/api/v1/chat/completions", strings.NewReader("{}"))
		request.Header.Set("Authorization", auth)
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
	}
	// The placeholder becomes the key; anything else goes on as it was.
	if seen[0] != "/api/v1/chat/completions Bearer sk-or-real" || seen[1] != "/api/v1/chat/completions Bearer sk-or-something-else" {
		t.Fatalf("upstream saw %q", seen)
	}
	if !strings.Contains(logged.String(), "openrouter /api/v1/chat/completions") || strings.Contains(logged.String(), "sk-or-real") {
		t.Fatalf("log: %s", logged.String())
	}
}

func TestHermesBoxSetup(t *testing.T) {
	hermes := t.TempDir()
	t.Setenv("HERMES_HOME", hermes)
	t.Setenv("HERMES_INSTALL_DIR", "")
	writeSkillTestFile(t, filepath.Join(hermes, "config.yaml"), "model:\n  default: google/gemini-3.8-flash\n  provider: openrouter\n", 0o600)
	writeSkillTestFile(t, filepath.Join(hermes, ".env"), "OPENROUTER_API_KEY=sk-or-real\n", 0o600)
	writeSkillTestFile(t, filepath.Join(hermes, "hermes-agent", "hermes"), "", 0o755)
	writeSkillTestFile(t, filepath.Join(hermes, "hermes-agent", "venv", "bin", "python"), "", 0o755)
	if err := hermesReady(); err != nil {
		t.Fatal(err)
	}
	if got := defaultAgentModel("hermes"); got != "google/gemini-3.8-flash" {
		t.Fatalf("default model %q", got)
	}

	home := t.TempDir()
	var run []string
	command, err := hermesBoxSetup("Go.", boxMeta{ProxyIP: "10.234.1.2", Model: "anthropic/claude-sonnet-5.5"}, home, "/box/home/.hi-agent", &run)
	if err != nil {
		t.Fatal(err)
	}
	config, _ := os.ReadFile(filepath.Join(home, ".hermes", "config.yaml"))
	for _, want := range []string{"default: anthropic/claude-sonnet-5.5", "provider: custom", "base_url: http://10.234.1.2:3129/api/v1", "api_key: " + boxClaudePlacehold} {
		if !strings.Contains(string(config), want) {
			t.Errorf("config has no %q:\n%s", want, config)
		}
	}
	// The real key stays out of the box's home.
	filepath.Walk(home, func(path string, info os.FileInfo, err error) error {
		if data, _ := os.ReadFile(path); bytes.Contains(data, []byte("sk-or-real")) {
			t.Errorf("%s has the key", path)
		}
		return nil
	})
	if !strings.Contains(strings.Join(run, " "), filepath.Join(hermes, "hermes-agent")+":ro") ||
		!strings.Contains(strings.Join(command, " "), "chat --query-file /box/home/.hi-agent/task.md -Q --yolo") {
		t.Fatalf("%v %v", run, command)
	}

	// Another provider isn't supported yet.
	writeSkillTestFile(t, filepath.Join(hermes, "config.yaml"), "model:\n  provider: nous\n", 0o600)
	if err := hermesReady(); err == nil || !strings.Contains(err.Error(), "OpenRouter") {
		t.Fatalf("another provider: %v", err)
	}
}

func TestHermesSession(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("no python3")
	}
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, ".hermes"), 0o700)
	db := filepath.Join(home, ".hermes", "state.db")
	script := `
import sqlite3, sys
db = sqlite3.connect(sys.argv[1])
db.executescript("""
create table sessions (id text, model text, input_tokens int, output_tokens int, cache_read_tokens int, tool_call_count int, started_at real, ended_at real);
create table session_model_usage (session_id text, model text, input_tokens int, output_tokens int, cache_read_tokens int, estimated_cost_usd real, actual_cost_usd real);
create table messages (id integer primary key, session_id text, role text, tool_calls text);
insert into sessions values ('old', 'm', 1, 1, 0, 0, 1, 2);
insert into sessions values ('s1', 'google/gemini-3.8-flash', 10, 5, 0, 2, 5, null);
insert into session_model_usage values ('s1', 'google/gemini-3.8-flash', 46000, 1200, 14000, 0.0, null);
insert into messages (session_id, role, tool_calls) values ('s1', 'assistant', '[{"function": {"name": "terminal", "arguments": "{\\"command\\": \\"python3 primes.py\\"}"}}]');
insert into messages (session_id, role, tool_calls) values ('s1', 'tool', null);
insert into messages (session_id, role, tool_calls) values ('s1', 'assistant', '[{"function": {"name": "write_file", "arguments": "{\\"path\\": \\"/w/summary.txt\\"}"}}]');
insert into messages (session_id, role, tool_calls) values ('s1', 'tool', null);
""")
db.commit()
`
	if out, err := exec.Command("python3", "-c", script, db).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	session, ok := readHermesSession(home)
	if !ok || session.ID != "s1" {
		t.Fatalf("%+v", session)
	}
	progress := hermesProgress(session)
	if progress.in != 60000 || progress.cached != 14000 || progress.out != 1200 || progress.steps != 2 || progress.step != "write_file: summary.txt" || progress.model != "google/gemini-3.8-flash" {
		t.Fatalf("%+v", progress)
	}
	if reader := newAgentProgressReader("hermes", home); reader.poll().steps != 2 {
		t.Fatal("the status line's reader")
	}
}

func TestHermesRuntimeFromLauncher(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	hermes := filepath.Join(home, ".hermes")
	t.Setenv("HERMES_HOME", hermes)
	t.Setenv("HERMES_INSTALL_DIR", "")
	install := filepath.Join(hermes, "hermes-agent")
	python := filepath.Join(hermes, "tools", "python-3.14", "bin", "python3")
	writeSkillTestFile(t, python, "", 0o755)
	writeSkillTestFile(t, filepath.Join(install, ".hermes", "bin", "hermes"),
		"#!/bin/sh\n[ \"$1\" = --print-runtime-command ] && echo '[\""+python+"\", \"-I\", \"-c\", \"import hermes_bootstrap; run()\"]'\n", 0o755)
	if !hermesPresent() {
		t.Fatal("a Hermes with only its launcher isn't found")
	}
	if _, err := loadHermesRuntime(); err == nil || !strings.Contains(err.Error(), "pm repair") {
		t.Fatal("a Hermes without packages:", err)
	}

	// Its packages: the environment facts.json names, under installs/<key>.
	sum := sha256.Sum256([]byte(install))
	key := hex.EncodeToString(sum[:])[:16]
	state := filepath.Join(hermes, "installs", key)
	environment := filepath.Join(state, "environments", "g1")
	os.MkdirAll(filepath.Join(environment, "venv"), 0o755)
	os.MkdirAll(filepath.Join(environment, ".leases"), 0o755)
	writeSkillTestFile(t, filepath.Join(state, "facts.json"), `{"packages": {"venv": {"environment": "`+environment+`/venv", "resolved_lock": "`+environment+`/workspace/uv.lock"}}, "schema": 1}`, 0o600)
	runtime, err := loadHermesRuntime()
	if err != nil {
		t.Fatal(err)
	}
	if len(runtime.Command) != 4 || runtime.Command[0] != python || runtime.Key != key || runtime.Environment != environment {
		t.Fatal(runtime)
	}
	if strings.Join(runtime.Mounts, " ") != filepath.Join(hermes, "tools", "python-3.14") {
		t.Fatal(runtime.Mounts)
	}

	// In the box, the environment is under the box's Hermes home, where
	// Hermes looks for it, and its leases are writable.
	lead := t.TempDir()
	os.MkdirAll(filepath.Join(lead, "installs", key, "environments", "g0"), 0o700)
	var run []string
	if err := hermesMounts(runtime, lead, &run); err != nil {
		t.Fatal(err)
	}
	inBox := "/box/home/.hermes/installs/" + key + "/environments/g1"
	if want := []string{"-v", install + ":" + install + ":ro", "-v", runtime.Mounts[0] + ":" + runtime.Mounts[0] + ":ro",
		"-v", environment + ":" + inBox + ":ro", "-v", environment + "/.leases:" + inBox + "/.leases"}; strings.Join(run, " ") != strings.Join(want, " ") {
		t.Fatal(run)
	}
	facts, _ := os.ReadFile(filepath.Join(lead, "installs", key, "facts.json"))
	if !strings.Contains(string(facts), `"`+inBox+`/venv"`) || !strings.Contains(string(facts), inBox+"/workspace/uv.lock") || strings.Contains(string(facts), state) {
		t.Fatal(string(facts))
	}
	if fileExists(filepath.Join(lead, "installs", key, "environments", "g0")) {
		t.Fatal("an older environment's place is still there")
	}

	with, ok := hermesWithSite(runtime.Command, "/home/u/.local/share/hi/hindsight")
	if !ok || with[3] != `import sys; sys.path.append("/home/u/.local/share/hi/hindsight"); import hermes_bootstrap; run()` || runtime.Command[3] != "import hermes_bootstrap; run()" {
		t.Fatal(with)
	}
	if _, ok := hermesWithSite([]string{"/h/hermes-agent/venv/bin/python", "/h/hermes-agent/hermes"}, "/s"); !ok {
		t.Fatal("an older Hermes reads PYTHONPATH")
	}
	if _, ok := hermesWithSite([]string{"/usr/bin/hermes"}, "/s"); ok {
		t.Fatal("a command hi doesn't know can't take the folder")
	}
}

func TestHermesWithSlackStatus(t *testing.T) {
	command := []string{"/py/bin/python3", "-I", "-c", "import hermes_bootstrap"}
	with, ok := hermesWithSlackStatus(command)
	if !ok || !strings.HasPrefix(with[3], hermesSlackStatusPrelude) || !strings.HasSuffix(with[3], "\nimport hermes_bootstrap") || command[3] != "import hermes_bootstrap" {
		t.Fatalf("%q", with)
	}
	if _, ok := hermesWithSlackStatus([]string{"/usr/bin/hermes"}); ok {
		t.Fatal("changed a command of another shape")
	}
}
