package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Hermes Agent (Nous Research) runs from its own install folder with its
// own Python, both mounted read-only into the box. Its model calls go to
// OpenRouter through the proxy's token listener, which puts the API key in
// place of a placeholder, so the key never enters the box. Its sessions
// are in a SQLite file in its home folder, which hi reads with python3.

func hermesHome() string {
	if home := os.Getenv("HERMES_HOME"); home != "" {
		return home
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".hermes")
}

func hermesEnvPath() string { return filepath.Join(hermesHome(), ".env") }

// hermesRuntime is how this machine's Hermes runs: the command its
// arguments follow, and the folders outside its install that a box mounts
// read-only at the same paths.
type hermesRuntime struct {
	Command []string
	Mounts  []string
	// A Hermes with its own Python keeps its packages in an environment
	// under ~/.hermes/installs/<Key>, named by Facts (facts.json), and
	// reads it only from under the Hermes home it runs with.
	Key         string
	Environment string // the environment's folder, holding venv/ and .leases/
	Facts       []byte
}

// hermesLauncher is the launcher a Hermes with its own Python (since
// September 2026) publishes; it says the command it runs.
func hermesLauncher() string { return filepath.Join(hermesInstallDir(), ".hermes", "bin", "hermes") }

func hermesLegacyPython() string { return filepath.Join(hermesInstallDir(), "venv", "bin", "python") }

func hermesPresent() bool {
	return fileExists(hermesLauncher()) || fileExists(hermesLegacyPython()) && fileExists(filepath.Join(hermesInstallDir(), "hermes"))
}

// loadHermesRuntime finds the command and the folders it needs; tests
// replace it.
var loadHermesRuntime = func() (hermesRuntime, error) {
	install := hermesInstallDir()
	home, _ := os.UserHomeDir()
	var runtime hermesRuntime
	if !fileExists(hermesLauncher()) {
		// An older Hermes runs from its venv, whose python is a link to a
		// Python uv installed in the home folder.
		if !hermesPresent() {
			return runtime, errors.New("Hermes is not installed on this machine; install it with hi install hermes")
		}
		runtime.Command = []string{hermesLegacyPython(), filepath.Join(install, "hermes")}
		if resolved, err := filepath.EvalSymlinks(hermesLegacyPython()); err == nil {
			if root := filepath.Dir(filepath.Dir(filepath.Dir(resolved))); strings.HasPrefix(root, home+"/") {
				runtime.Mounts = append(runtime.Mounts, root)
			}
		}
		return runtime, nil
	}
	out, err := exec.Command(hermesLauncher(), "--print-runtime-command").Output()
	if err != nil {
		return runtime, fmt.Errorf("asking Hermes how it runs: %w", err)
	}
	if err := json.Unmarshal(out, &runtime.Command); err != nil || len(runtime.Command) == 0 {
		return runtime, fmt.Errorf("Hermes' launcher said something hi can't read: %s", qFirstLine(strings.TrimSpace(string(out)), "nothing"))
	}
	// Its Python is a folder of its own: <root>/bin/python3.
	python := runtime.Command[0]
	if resolved, err := filepath.EvalSymlinks(python); err == nil {
		python = resolved
	}
	if root := filepath.Dir(filepath.Dir(python)); strings.HasPrefix(root, home+"/") {
		runtime.Mounts = append(runtime.Mounts, root)
	}
	// Its packages: the environment facts.json names, in the Hermes home
	// that installed it (the one the install sits in, else ~/.hermes).
	resolved, err := filepath.EvalSymlinks(install)
	if err != nil {
		return runtime, err
	}
	sum := sha256.Sum256([]byte(resolved))
	runtime.Key = hex.EncodeToString(sum[:])[:16]
	for _, root := range []string{filepath.Dir(resolved), filepath.Join(home, ".hermes")} {
		data, err := os.ReadFile(filepath.Join(root, "installs", runtime.Key, "facts.json"))
		if err != nil {
			continue
		}
		var facts struct {
			Packages struct {
				Venv struct {
					Environment string `json:"environment"`
				} `json:"venv"`
			} `json:"packages"`
		}
		if json.Unmarshal(data, &facts) != nil || facts.Packages.Venv.Environment == "" {
			continue
		}
		environment := filepath.Dir(facts.Packages.Venv.Environment)
		if filepath.Dir(filepath.Dir(environment)) != filepath.Join(root, "installs", runtime.Key) || !fileExists(environment) {
			continue
		}
		runtime.Environment, runtime.Facts = environment, data
		return runtime, nil
	}
	return runtime, errors.New("Hermes has no packages installed for hi to give a box; run hermes pm repair")
}

// hermesMounts mounts the host's Hermes, its Python, and its packages
// read-only, at the same paths, for a box whose Hermes home is
// hermesHome on this machine and /box/home/.hermes in the box.
func hermesMounts(runtime hermesRuntime, hermesHome string, run *[]string) error {
	install := hermesInstallDir()
	*run = append(*run, "-v", install+":"+install+":ro")
	for _, dir := range runtime.Mounts {
		*run = append(*run, "-v", dir+":"+dir+":ro")
	}
	if runtime.Environment == "" {
		return nil
	}
	// The environment goes where Hermes looks for it, under the box's
	// Hermes home, with a facts.json that names it there. Its leases stay
	// writable: a lead's lease keeps a Hermes update here from removing
	// the environment while the lead runs on it.
	state := filepath.Join(hermesHome, "installs")
	if err := os.RemoveAll(state); err != nil {
		return err
	}
	hostState := filepath.Dir(filepath.Dir(runtime.Environment))
	boxState := "/box/home/.hermes/installs/" + runtime.Key
	name := filepath.Base(runtime.Environment)
	if err := os.MkdirAll(filepath.Join(state, runtime.Key, "environments", name), 0o700); err != nil {
		return err
	}
	facts := strings.ReplaceAll(string(runtime.Facts), hostState+"/", boxState+"/")
	if err := os.WriteFile(filepath.Join(state, runtime.Key, "facts.json"), []byte(facts), 0o600); err != nil {
		return err
	}
	inBox := boxState + "/environments/" + name
	*run = append(*run, "-v", runtime.Environment+":"+inBox+":ro")
	if leases := filepath.Join(runtime.Environment, ".leases"); fileExists(leases) {
		*run = append(*run, "-v", leases+":"+inBox+"/.leases")
	}
	return nil
}

// hermesWithSite puts a folder of packages on Hermes' path. Its own
// Python runs isolated (-I), without PYTHONPATH, so the folder goes into
// the code it runs; an older Hermes reads PYTHONPATH, which the caller
// sets too. It reports false when the command has neither shape.
func hermesWithSite(command []string, site string) ([]string, bool) {
	if len(command) >= 4 && command[1] == "-I" && command[2] == "-c" {
		with := append([]string(nil), command...)
		quoted, _ := json.Marshal(site)
		with[3] = "import sys; sys.path.append(" + string(quoted) + "); " + with[3]
		return with, true
	}
	return command, len(command) >= 2 && filepath.Base(command[0]) == "python" && filepath.Base(filepath.Dir(filepath.Dir(command[0]))) == "venv"
}

// hermesSlackStatusPrelude hides Slack's Agent Sessions API from Hermes,
// so its Slack adapter shows the lead's status with
// assistant.threads.setStatus. Hermes v0.22.2 prefers
// agents.sessions.setStatus once slack-sdk has it (3.44), but passes it
// the status line, where that method takes a lifecycle state: Slack
// refuses every call, Hermes logs it only in debug, and the thread shows
// nothing while the lead works.
const hermesSlackStatusPrelude = `import sys, importlib.util
class _HiSlackStatus:
    def find_spec(self, name, path=None, target=None):
        if name != "slack_sdk.web.async_client":
            return None
        sys.meta_path.remove(self)
        spec = importlib.util.find_spec(name)
        if spec is None or spec.loader is None:
            return spec
        run = spec.loader.exec_module
        def exec_module(module):
            run(module)
            for method in ("agents_sessions_setStatus", "agents_sessions_rename"):
                if method in vars(module.AsyncWebClient):
                    delattr(module.AsyncWebClient, method)
        spec.loader.exec_module = exec_module
        return spec
sys.meta_path.insert(0, _HiSlackStatus())
`

// hermesWithSlackStatus runs hermesSlackStatusPrelude before Hermes, in
// a command of the shape hermesWithSite knows; it reports false for any
// other.
func hermesWithSlackStatus(command []string) ([]string, bool) {
	if len(command) >= 4 && command[1] == "-I" && command[2] == "-c" {
		with := append([]string(nil), command...)
		with[3] = hermesSlackStatusPrelude + with[3]
		return with, true
	}
	return command, false
}

func hermesInstallDir() string {
	return firstNonEmpty(os.Getenv("HERMES_INSTALL_DIR"), filepath.Join(hermesHome(), "hermes-agent"))
}

// hermesModelConfig is the model block of Hermes' config.yaml.
type hermesModelConfig struct {
	Default, Provider, BaseURL, APIMode string
}

// readHermesModelConfig reads the keys under the top-level model: block of
// config.yaml; nothing else in the file is needed.
func readHermesModelConfig(path string) hermesModelConfig {
	var config hermesModelConfig
	data, err := os.ReadFile(path)
	if err != nil {
		return config
	}
	inModel := false
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		if !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
			inModel = strings.TrimSpace(line) == "model:"
			continue
		}
		if !inModel {
			continue
		}
		key, value, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok {
			continue
		}
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		switch strings.TrimSpace(key) {
		case "default":
			config.Default = value
		case "provider":
			config.Provider = value
		case "base_url":
			config.BaseURL = value
		case "api_mode":
			config.APIMode = value
		}
	}
	return config
}

// hermesInstalled checks that Hermes is installed and has an OpenRouter
// key for the proxy to put in place of a box's placeholder.
func hermesInstalled() error {
	if !hermesPresent() {
		return errors.New("Hermes is not installed on this machine; install it with hi install hermes")
	}
	if _, err := readDotEnvValue(hermesEnvPath(), "OPENROUTER_API_KEY"); err != nil {
		return errors.New("Hermes has no OpenRouter key in ~/.hermes/.env; run hermes setup")
	}
	return nil
}

// hermesReady checks that Hermes is installed and uses OpenRouter with a
// key, the one provider hi can keep the key outside the box for.
func hermesReady() error {
	if !hermesPresent() {
		return errors.New("Hermes is not installed on this machine; install it with hi install hermes")
	}
	config := readHermesModelConfig(filepath.Join(hermesHome(), "config.yaml"))
	if provider := firstNonEmpty(config.Provider, "openrouter"); provider != "openrouter" {
		return fmt.Errorf("hi agent hermes works with OpenRouter, and Hermes here uses %s; run hermes model and pick OpenRouter", provider)
	}
	if _, err := readDotEnvValue(hermesEnvPath(), "OPENROUTER_API_KEY"); err != nil {
		return errors.New("Hermes has no OpenRouter key in ~/.hermes/.env; run hermes setup")
	}
	return nil
}

// hermesBoxSetup mounts Hermes and its Python, writes its config and a
// placeholder key in the box's home folder, and returns its command.
func hermesBoxSetup(prompt string, meta boxMeta, homeDir, results string, run *[]string) ([]string, error) {
	runtime, err := loadHermesRuntime()
	if err != nil {
		return nil, err
	}
	hermesDir := filepath.Join(homeDir, ".hermes")
	if err := os.MkdirAll(hermesDir, 0o700); err != nil {
		return nil, err
	}
	if err := hermesMounts(runtime, hermesDir, run); err != nil {
		return nil, err
	}
	host := readHermesModelConfig(filepath.Join(hermesHome(), "config.yaml"))
	model := firstNonEmpty(meta.Model, host.Default)
	// Hermes sends the OpenRouter key only to openrouter.ai, so the box
	// names the proxy as a custom endpoint with the placeholder as its key.
	config := fmt.Sprintf("model:\n  provider: custom\n  base_url: http://%s:%d/api/v1\n  api_key: %s\n  api_mode: %s\n", meta.ProxyIP, boxInjectPort, boxClaudePlacehold, firstNonEmpty(host.APIMode, "chat_completions"))
	if model != "" {
		config = "model:\n  default: " + model + "\n" + strings.TrimPrefix(config, "model:\n")
	}
	config += "terminal:\n  backend: local\n  cwd: .\n"
	if err := os.WriteFile(filepath.Join(hermesDir, "config.yaml"), []byte(config), 0o600); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(hermesDir, ".env"), []byte("OPENROUTER_API_KEY="+boxClaudePlacehold+"\n"), 0o600); err != nil {
		return nil, err
	}
	hermes := runtime.Command
	if prompt == "" {
		return append(append([]string(nil), hermes...), "chat", "--yolo"), nil
	}
	// The final answer goes to a file and to the log; the session ID goes
	// to the log on stderr.
	var quoted []string
	for _, arg := range hermes {
		quoted = append(quoted, shellQuote(arg))
	}
	script := strings.Join(quoted, " ") + " chat --query-file " + results + "/task.md -Q --yolo > " + results + "/last.txt; status=$?; cat " + results + "/last.txt; exit $status"
	return []string{"sh", "-c", script}, nil
}

// hermesSessionScript prints, as JSON, the newest session in a Hermes
// state.db: tokens, tool calls, model, cost, and the latest tool call.
const hermesSessionScript = `
import json, sqlite3, sys
try:
    db = sqlite3.connect("file:" + sys.argv[1] + "?mode=ro", uri=True, timeout=1)
    row = db.execute("select id, model, input_tokens, output_tokens, cache_read_tokens, tool_call_count, started_at, ended_at from sessions order by started_at desc limit 1").fetchone()
    if not row:
        print("{}"); sys.exit()
    out = dict(zip(["id", "model", "input", "output", "cached", "tools", "started", "ended"], row))
    cost = db.execute("select sum(coalesce(actual_cost_usd, estimated_cost_usd)), sum(input_tokens), sum(output_tokens), sum(cache_read_tokens), max(model) from session_model_usage where session_id = ?", (row[0],)).fetchone()
    if cost and cost[1]:
        out["cost"], out["input"], out["output"], out["cached"] = cost[0], cost[1], cost[2], cost[3]
        out["model"] = out["model"] or cost[4]
    last = db.execute("select tool_calls from messages where session_id = ? and role = 'assistant' and tool_calls is not null order by id desc limit 1", (row[0],)).fetchone()
    if last:
        out["last_calls"] = last[0]
    out["steps"] = db.execute("select count(*) from messages where session_id = ? and role = 'tool'", (row[0],)).fetchone()[0]
    print(json.dumps(out, default=str))
except Exception as e:
    print(json.dumps({"error": str(e)}))
`

type hermesSession struct {
	ID        string   `json:"id"`
	Model     string   `json:"model"`
	Input     int      `json:"input"`
	Output    int      `json:"output"`
	Cached    int      `json:"cached"`
	Steps     int      `json:"steps"`
	Cost      *float64 `json:"cost"`
	LastCalls *string  `json:"last_calls"`
	Error     string   `json:"error"`
}

// readHermesSession reads the newest session from a box's Hermes home.
func readHermesSession(home string) (hermesSession, bool) {
	var session hermesSession
	db := filepath.Join(home, ".hermes", "state.db")
	if !fileExists(db) {
		return session, false
	}
	out, err := exec.Command("python3", "-c", hermesSessionScript, db).Output()
	if err != nil || json.Unmarshal(out, &session) != nil || session.Error != "" || session.ID == "" {
		return session, false
	}
	return session, true
}

// hermesProgress turns a session into the status line's progress.
func hermesProgress(session hermesSession) agentProgress {
	// Hermes counts input without the cached part; hi's "in" includes it.
	progress := agentProgress{in: session.Input + session.Cached, out: session.Output, cached: session.Cached, steps: session.Steps, model: session.Model}
	if session.LastCalls != nil && *session.LastCalls != "" {
		var calls []struct {
			Function struct {
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			} `json:"function"`
		}
		if json.Unmarshal([]byte(*session.LastCalls), &calls) == nil && len(calls) > 0 {
			var arguments map[string]any
			json.Unmarshal([]byte(calls[len(calls)-1].Function.Arguments), &arguments)
			progress.step = claudeStep(calls[len(calls)-1].Function.Name, arguments)
		}
	}
	return progress
}
