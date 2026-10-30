package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// teamAgyTestSetup is a team whose machine has Antigravity, with hi agent
// replaced: the run makes logo.png and reports.
func teamAgyTestSetup(t *testing.T) (*teamTestBroker, func() []string) {
	t.Helper()
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))
	bin := filepath.Join(home, ".local", "bin")
	writeSkillTestFile(t, filepath.Join(bin, "agy"), "#!/bin/sh\n", 0o755)
	t.Setenv("PATH", bin+":/usr/bin:/bin")
	var mu sync.Mutex
	var calls []string
	b.agyRun = func(dir string, args ...string) ([]byte, error) {
		mu.Lock()
		calls = append(calls, strings.Join(args, " "))
		mu.Unlock()
		switch args[1] {
		case "agy":
			os.WriteFile(filepath.Join(dir, "logo.png"), []byte("png"), 0o600)
		case "wait":
			return []byte(`{"status":"done","text":"Made logo.png, a blue circle.","report":{"status":"complete","summary":"A logo."}}`), nil
		}
		return []byte("{}"), nil
	}
	return b, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), calls...)
	}
}

func waitTeamAgy(t *testing.T, id string) teamAgy {
	t.Helper()
	for range 200 {
		if record, _ := findTeamAgy("payments", id); record.Status != "running" {
			return record
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal(id, "still running")
	return teamAgy{}
}

// The dev box's run: its files go in through os.Root, what it made comes
// back into the box's home, and its box is removed.
func TestTeamAgyFromTheDevBox(t *testing.T) {
	b, calls := teamAgyTestSetup(t)
	if _, err := b.devAgy(teamAgyRequest{Task: "a logo"}); err == nil || !strings.Contains(err.Error(), "hi install agy") {
		t.Fatal("ran without Antigravity signed in:", err)
	}
	os.MkdirAll(filepath.Dir(agyTokenPath()), 0o700)
	os.WriteFile(agyTokenPath(), []byte(`{"auth_method":"consumer"}`), 0o600)

	devHome := filepath.Join(teamDevDir("payments"), "home")
	os.MkdirAll(devHome, 0o700)
	os.WriteFile(filepath.Join(devHome, "sketch.jpg"), []byte("jpg"), 0o600)
	secret := filepath.Join(t.TempDir(), "gemini-api-key")
	os.WriteFile(secret, []byte("AIza-real"), 0o600)
	os.Symlink(secret, filepath.Join(devHome, "key"))
	for _, outside := range []string{"/box/home/key", "/etc/passwd"} {
		if _, err := b.devAgy(teamAgyRequest{Task: "a logo", Files: []string{outside}}); err == nil {
			t.Fatalf("%s went to Antigravity", outside)
		}
	}

	record, err := b.devAgy(teamAgyRequest{Task: "a logo like the sketch", Files: []string{"/box/home/sketch.jpg"}})
	if err != nil || record.ID != "a-1" || !record.Dev {
		t.Fatal(record, err)
	}
	work := teamAgyWorkDir("payments", "a-1")
	if data, _ := os.ReadFile(filepath.Join(work, "inputs", "sketch.jpg")); string(data) != "jpg" {
		t.Fatal("the sketch isn't in its inputs")
	}
	if task, _ := os.ReadFile(work + ".task.md"); !strings.Contains(string(task), "a logo like the sketch") || !strings.Contains(string(task), "inputs/: sketch.jpg") {
		t.Fatalf("task: %s", task)
	}
	done := waitTeamAgy(t, "a-1")
	if done.Status != "done" || strings.Join(done.Made, ",") != "logo.png" || done.Summary != "Made logo.png, a blue circle." {
		t.Fatalf("%+v", done)
	}
	if data, _ := os.ReadFile(filepath.Join(teamDevResultsDir("payments", "agy", "a-1"), "logo.png")); string(data) != "png" {
		t.Fatal("the logo isn't in the dev box's home")
	}
	want := []string{"agent agy --detach --json --name payments-agy-1 --task-file " + work + ".task.md", "agent wait payments-agy-1 --json", "box rm payments-agy-1"}
	for range 100 {
		if len(calls()) == len(want) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if strings.Join(calls(), "|") != strings.Join(want, "|") {
		t.Fatalf("hi ran %q", calls())
	}
	if text, _ := b.showAgy("a-1", true); !strings.Contains(text, "It made, in ~/hi/agy/a-1/: logo.png") {
		t.Fatal(text)
	}
}

// The lead's run is for someone who may ask the team for work; what it
// made goes into their thread, and the lead is woken there.
func TestTeamAgyForTheLead(t *testing.T) {
	b, _ := teamAgyTestSetup(t)
	os.MkdirAll(filepath.Dir(agyTokenPath()), 0o700)
	os.WriteFile(agyTokenPath(), []byte(`{"auth_method":"consumer"}`), 0o600)
	channel := &fakeTeamChannel{}
	b.channel = channel
	woke := make(chan string, 1)
	b.wakeLead = func(name, prompt, deliver string) error {
		woke <- name + " " + deliver + " " + prompt
		return nil
	}
	if _, err := b.leadAgy(teamAgyRequest{Task: "a logo", User: "U07NOBODY", Chat: "C07PAYMENTS", Thread: "1730000000.000100"}); err == nil {
		t.Fatal("someone without a level started a run")
	}
	record, err := b.leadAgy(teamAgyRequest{Task: "a logo", User: "U07OWNER1", Chat: "C07PAYMENTS", Thread: "1730000000.000100"})
	if err != nil || record.Deliver != "slack:C07PAYMENTS:1730000000.000100" || record.By != "<@U07OWNER1> through the lead" {
		t.Fatal(record, err)
	}
	var woken string
	select {
	case woken = <-woke:
	case <-time.After(5 * time.Second):
		t.Fatal("the lead wasn't woken")
	}
	waitTeamAgy(t, record.ID)
	if !strings.Contains(strings.Join(channel.calls, "|"), "upload C07PAYMENTS 1730000000.000100 logo.png logo.png") {
		t.Fatalf("channel: %q", channel.calls)
	}
	if !strings.Contains(woken, "hi-agy-a-1 slack:C07PAYMENTS:1730000000.000100") || !strings.Contains(woken, "/team/agy/a-1/") {
		t.Fatalf("woken: %q", woken)
	}
	if data, _ := os.ReadFile(filepath.Join(teamAgyDir("payments"), "a-1", "logo.png")); string(data) != "png" {
		t.Fatal("the logo isn't where the lead reads it")
	}
	var saved []teamAgy
	data, _ := os.ReadFile(teamAgyPath("payments"))
	json.Unmarshal(data, &saved)
	if len(saved) != 1 || saved[0].Status != "done" {
		t.Fatalf("%+v", saved)
	}
}
