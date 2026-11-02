package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func serveTest(t *testing.T) *fakeHF {
	t.Helper()
	fake := newFakeHF(t)
	fake.finalStage = "RUNNING"
	fake.logLines = []string{"hi-state: downloading and loading x", "hi-state: ready"}
	previous := servePollEvery
	servePollEvery = time.Millisecond
	t.Cleanup(func() { servePollEvery = previous })
	return fake
}

// hi serve llm serves a model, hi serve ls shows it with its API, and hi
// serve stop stops it and nothing else.
func TestServeLLMListsAndStopsTheModel(t *testing.T) {
	serveTest(t)
	code, stdout, stderr := runHi("serve", "llm", "--name", "qwen", "--max", "1h", "--yes", "qwen3.8-flash-next")
	if code != 0 || !strings.Contains(stdout, "https://job0001--8000.hf.jobs/v1   model: qwen3.8-flash-next") ||
		!strings.Contains(stdout, "Stop it:              hi serve stop qwen") {
		t.Fatalf("hi serve llm: %d\n%s%s", code, stdout, stderr)
	}

	code, stdout, stderr = runHi("serve", "ls")
	if code != 0 || !strings.Contains(stdout, "NAME") ||
		!strings.Contains(stdout, "qwen  hf     qwen3.8-flash-next  https://job0001--8000.hf.jobs/v1  running") {
		t.Fatalf("hi serve ls: %d\n%s%s", code, stdout, stderr)
	}
	code, stdout, _ = runHi("serve", "ls", "--json")
	var rows []map[string]any
	if code != 0 || json.Unmarshal([]byte(stdout), &rows) != nil || len(rows) != 1 ||
		rows[0]["model"] != "qwen3.8-flash-next" || rows[0]["repo"] != "unsloth/Qwen3.8-Flash-Next-GGUF" || rows[0]["stops"] == nil {
		t.Fatalf("hi serve ls --json: %d %s", code, stdout)
	}

	if code, _, stderr := runHi("serve", "logs", "qwen"); code != 0 {
		t.Fatalf("hi serve logs: %d %s", code, stderr)
	}
	if code, _, stderr := runHi("compute", "up", "--on", "hf", "--gpu", "a10g-small", "--name", "other", "--yes"); code != 0 {
		t.Fatalf("up: %s", stderr)
	}
	if code, _, stderr := runHi("serve", "stop", "other"); code != 1 || !strings.Contains(stderr, "doesn't serve a model") {
		t.Fatalf("hi serve stop on a plain instance: %d %s", code, stderr)
	}
	if code, _, stderr := runHi("serve", "stop", "qwen", "--yes"); code != 0 {
		t.Fatalf("hi serve stop: %d %s", code, stderr)
	}
	if code, stdout, _ := runHi("serve", "ls"); code != 0 || !strings.Contains(stdout, "No models are being served") {
		t.Fatalf("after stop: %s", stdout)
	}
	if code, stdout, _ := runHi("compute", "ls"); code != 0 || !strings.Contains(stdout, "other") {
		t.Fatalf("hi serve stop touched another instance: %s", stdout)
	}
}

// hi compute serve is another name for hi serve llm.
func TestComputeServeIsServeLLM(t *testing.T) {
	serveTest(t)
	if code, _, stderr := runHi("compute", "serve", "--name", "qwen", "--max", "1h", "--yes", "qwen3.8-flash-next"); code != 0 {
		t.Fatalf("hi compute serve: %s", stderr)
	}
	if _, stdout, _ := runHi("serve", "ls"); !strings.Contains(stdout, "qwen3.8-flash-next") {
		t.Fatalf("hi compute serve isn't in hi serve ls:\n%s", stdout)
	}
	for _, args := range [][]string{{"serve", "llm"}, {"compute", "serve"}} {
		code, _, stderr := runHi(append(args, "--gpu", "T4", "not-a-repo")...)
		if code != 2 || !strings.Contains(stderr, "neither a recipe") {
			t.Errorf("hi %s: %d %s", strings.Join(args, " "), code, stderr)
		}
	}
}

func TestServeRefusesWhatIsntBuiltYet(t *testing.T) {
	serveTest(t)
	if code, _, stderr := runHi("serve", "llm", "--on", "auto", "qwen3.8-flash-next"); code != 2 || !strings.Contains(stderr, "--on auto isn't built yet") {
		t.Errorf("--on auto: %d %s", code, stderr)
	}
	for _, args := range [][]string{
		{"serve", "llm", "--on", "local", "--reason", "x", "qwen3.8-flash-next"},
		{"serve", "llm", "--on", "hf", "--org", "qwen3.8-flash-next"},
		{"serve", "llm", "--on", "local", "--gpu", "G4", "qwen3.8-flash-next"},
		{"serve", "llm", "--on", "hf", "--memory", "8G", "qwen3.8-flash-next"},
	} {
		if code, _, stderr := runHi(args...); code != 2 {
			t.Errorf("hi %s: %d %s", strings.Join(args, " "), code, stderr)
		}
	}
	for _, kind := range []string{"web", "files", "db"} {
		if code, _, stderr := runHi("serve", kind, "x"); code != 1 || !strings.Contains(stderr, "isn't built yet") {
			t.Errorf("hi serve %s: %d %s", kind, code, stderr)
		}
	}
	if code, _, stderr := runHi("serve", "nonsense"); code != 2 || !strings.Contains(stderr, "unknown serve command") {
		t.Errorf("hi serve nonsense: %d %s", code, stderr)
	}
	if _, stdout, _ := runHi("help"); !strings.Contains(stdout, "\n  hi serve llm <recipe>") {
		t.Errorf("hi help doesn't list hi serve:\n%s", stdout)
	}
}

// A model on an SSH provider is reached through a tunnel, which hi serve ls
// says, with the command that opens it again.
func TestServeListsATunnelledModel(t *testing.T) {
	fake := newFakeColab(t)
	fake.fakeRemoteSSH("ready")
	previous := servePollEvery
	servePollEvery = time.Millisecond
	t.Cleanup(func() { servePollEvery = previous })
	if code, _, stderr := runHi("serve", "llm", "--name", "qwen", "--yes", "qwen3.8-flash-next"); code != 0 {
		t.Fatalf("hi serve llm: %s", stderr)
	}
	code, stdout, stderr := runHi("serve", "ls")
	if code != 0 || !strings.Contains(stdout, "qwen  colab  qwen3.8-flash-next  tunnel  running") ||
		!strings.Contains(stdout, "hi compute tunnel qwen 8000:8080") {
		t.Fatalf("hi serve ls: %d\n%s%s", code, stdout, stderr)
	}
}
