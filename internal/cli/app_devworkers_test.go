package cli

import (
	"strings"
	"testing"
)

// The chat's workers: background tasks a session starts, until Claude
// Code says they ended, as its transcripts record them.
func TestDevChatWorkers(t *testing.T) {
	lines := []string{
		// A command run in the background, an agent, and a monitor.
		`{"type":"assistant","timestamp":"2026-11-28T10:00:00Z","message":{"content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"go test ./...","description":"Run the tests","run_in_background":true}}]}}`,
		`{"type":"user","timestamp":"2026-11-28T10:00:01Z","toolUseResult":{"stdout":"","backgroundTaskId":"bash1"},"message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":"Command running in background with ID: bash1."}]}}`,
		`{"type":"assistant","timestamp":"2026-11-28T10:00:02Z","message":{"content":[{"type":"tool_use","id":"t2","name":"Agent","input":{"description":"Find the callers","prompt":"..."}}]}}`,
		`{"type":"user","timestamp":"2026-11-28T10:00:03Z","toolUseResult":{"isAsync":true,"status":"async_launched","agentId":"agent1"},"message":{"content":[{"type":"tool_result","tool_use_id":"t2","content":"Async agent launched"}]}}`,
		`{"type":"assistant","timestamp":"2026-11-28T10:00:04Z","message":{"content":[{"type":"tool_use","id":"t3","name":"Monitor","input":{"description":"Wait for the build","command":"until false; do sleep 5; done"}}]}}`,
		`{"type":"user","timestamp":"2026-11-28T10:00:05Z","toolUseResult":{"taskId":"mon1","timeoutMs":1200000},"message":{"content":[{"type":"tool_result","tool_use_id":"t3","content":"Monitor started (task mon1)"}]}}`,
		// A foreground command is no worker.
		`{"type":"assistant","timestamp":"2026-11-28T10:00:06Z","message":{"content":[{"type":"tool_use","id":"t4","name":"Bash","input":{"command":"ls"}}]}}`,
		`{"type":"user","timestamp":"2026-11-28T10:00:07Z","toolUseResult":{"stdout":"a"},"message":{"content":[{"type":"tool_result","tool_use_id":"t4","content":"a"}]}}`,
	}
	var entries []appDevChatEntry
	for _, line := range lines {
		entries = append(entries, appDevChatEntries([]byte(line))...)
	}
	names := func(workers []appDevWorker) string {
		var out []string
		for _, w := range workers {
			out = append(out, w.Task+":"+w.Name)
		}
		return strings.Join(out, " ")
	}
	workers := appDevWorkers(entries)
	if got := names(workers); got != "bash1:Bash agent1:Agent mon1:Monitor" {
		t.Fatalf("workers = %q", got)
	}
	if workers[0].Detail != "go test ./..." || workers[0].Summary != "Run the tests" || workers[1].Detail != "" || workers[1].Summary != "Find the callers" {
		t.Fatalf("worker text = %+v", workers[:2])
	}

	more := []string{
		// A monitor's event leaves it running.
		`{"type":"attachment","attachment":{"type":"queued_command","commandMode":"task-notification","prompt":"<task-notification>\n<task-id>mon1</task-id>\n<summary>Monitor event</summary>\n<event>line</event>\n</task-notification>"}}`,
		// The command ends in the middle of a turn: no note.
		`{"type":"attachment","attachment":{"type":"queued_command","commandMode":"task-notification","prompt":"<task-notification>\n<task-id>bash1</task-id>\n<tool-use-id>t1</tool-use-id>\n<status>completed</status>\n<summary>Background command \"Run the tests\" completed (exit code 0)</summary>\n</task-notification>"}}`,
		// The agent ends between turns: a note.
		`{"type":"user","timestamp":"2026-11-28T10:01:00Z","message":{"role":"user","content":"<task-notification>\n<task-id>agent1</task-id>\n<status>completed</status>\n<summary>Agent \"Find the callers\" finished</summary>\n</task-notification>"}}`,
	}
	var later []appDevChatEntry
	for _, line := range more {
		later = append(later, appDevChatEntries([]byte(line))...)
	}
	if len(later) != 2 || later[0].T != "ended" || later[1].T != "note" || later[1].Text != `Agent "Find the callers" finished` {
		t.Fatalf("later entries = %+v", later)
	}
	if got := names(appDevWorkers(append(entries, later...))); got != "mon1:Monitor" {
		t.Fatalf("workers after = %q", got)
	}
	// A monitor that expires ends.
	expired := appDevChatEntries([]byte(`{"type":"user","message":{"role":"user","content":"<task-notification>\n<task-id>mon1</task-id>\n<summary>Monitor event</summary>\n<event>[Monitor expired after 20m with no events delivered.]</event>\n</task-notification>"}}`))
	if got := names(appDevWorkers(append(append(entries, later...), expired...))); got != "" {
		t.Fatalf("workers after expiry = %q", got)
	}
}
