package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTeamTaskFiles(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	channel := teamChannelDir("payments")
	os.MkdirAll(filepath.Join(channel, "files"), 0o700)
	os.WriteFile(filepath.Join(channel, "files", "F1-mock.png"), []byte("png"), 0o600)
	now := time.Now()
	ts := func(ago time.Duration) string { return teamSlackTS(now.Add(-ago)) }
	root, yes := ts(10*time.Minute), ts(time.Minute)
	teamAppendChannel(channel, []teamChannelMessage{
		{TS: ts(20 * time.Minute), User: "U07OWNER1", Name: "Anna", Text: "Something else entirely"},
		{TS: root, User: "U07OWNER1", Name: "Anna", Text: "A supplier page like this mockup (https://example.com/mock)",
			Files: []teamChannelFile{{Name: "mock.png", Path: "files/F1-mock.png"}, {Name: "film.mov", Link: "https://slack.example/film"}}},
		{TS: ts(9 * time.Minute), Thread: root, Name: "team-payments", Bot: true, Text: "Shall I start?"},
		{TS: yes, Thread: root, User: "U07OWNER1", Name: "Anna", Text: "Yes"},
	})

	task, err := b.startTask(teamRequest{Role: "coder", Brief: "Add a supplier page", Approval: yes,
		Attachments: []teamAttachment{{Name: "../notes.txt", Data: []byte("the lead's notes")}}})
	if err != nil {
		t.Fatal(err)
	}
	task = waitTeamTask(t, task.ID)
	if task.Files != teamTaskFilesDir("payments", "t-1") || !strings.Contains(b.calls[0], "--files "+task.Files) {
		t.Fatal(task.Files, b.calls[0])
	}
	for name, want := range map[string]string{"F1-mock.png": "png", "notes.txt": "the lead's notes"} {
		if data, _ := os.ReadFile(filepath.Join(task.Files, name)); string(data) != want {
			t.Fatalf("%s is %q", name, data)
		}
	}
	brief := b.brief(task)
	for _, want := range []string{"## What the people said", "**Anna**", "(https://example.com/mock)", "[Anna shared mock.png: /box/files/F1-mock.png]",
		"too large to download: https://slack.example/film", "- /box/files/notes.txt: from the lead", "- /box/files/F1-mock.png: shared by Anna in Slack"} {
		if !strings.Contains(brief, want) {
			t.Fatalf("the brief lacks %q:\n%s", want, brief)
		}
	}
	if strings.Contains(brief, "Something else entirely") {
		t.Fatal("the brief has a message from outside the thread")
	}

	// A follow-up gets the files and how the work started; its review, the same files.
	more, err := b.startTask(teamRequest{Role: "coder", Brief: "Fix the reviewer's notes", On: "t-1"})
	if err != nil {
		t.Fatal(err)
	}
	more = waitTeamTask(t, more.ID)
	if data, _ := os.ReadFile(filepath.Join(more.Files, "F1-mock.png")); string(data) != "png" {
		t.Fatal("the follow-up lacks the mockup")
	}
	followBrief := b.brief(more)
	if !strings.Contains(followBrief, "## How this work started (t-1)\n\nAdd a supplier page\n") || strings.Count(followBrief, "# Task ") != 1 {
		t.Fatal(followBrief)
	}
	review, err := b.startReview(teamRequest{Task: more.ID})
	if err != nil {
		t.Fatal(err)
	}
	waitTeamTask(t, review.ID)
	if !strings.Contains(b.calls[len(b.calls)-2], "--files "+more.Files) {
		t.Fatal(b.calls)
	}

	// No files, no folder.
	small, err := b.startTask(teamRequest{Role: "coder", Brief: "Fix a typo", Small: true})
	if err != nil {
		t.Fatal(err)
	}
	if small = waitTeamTask(t, small.ID); small.Files != "" || fileExists(teamTaskFilesDir("payments", small.ID)) || strings.Contains(b.brief(small), "/box/files") {
		t.Fatal(small.Files)
	}
	if _, err := b.discard(more.ID); err != nil {
		t.Fatal(err)
	}
	if fileExists(task.Files) || fileExists(more.Files) {
		t.Fatal("a discarded idea's files are kept")
	}
}

func TestAgentFilesOption(t *testing.T) {
	dir := t.TempDir()
	options, err := parseBoxOptions("agent", []string{"claude", "--files", dir, "do it"})
	if err != nil || len(options.mounts) != 1 || options.mounts[0] != dir+":/box/files:ro" {
		t.Fatal(options.mounts, err)
	}
	if _, err := parseBoxOptions("agent", []string{"--files", filepath.Join(dir, "nope")}); err == nil {
		t.Fatal("a missing folder")
	}
}
