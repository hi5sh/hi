package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// hi update --restart waits while the team releases, then restarts it.
// (staleServers leaves out hi's own process, so the wait is called as
// restartStaleServers calls it.)
func TestUpdateWaitsForRelease(t *testing.T) {
	t.Setenv("HI_TEAMS_DIR", t.TempDir())
	pid := strconv.Itoa(os.Getpid())
	fakeProc(t, map[string][3]string{pid: {"/usr/local/bin/hi (deleted)", "hi\x00team\x00__serve\x00aiman\x00", ""}})
	previous := teamRestartPoll
	teamRestartPoll = 10 * time.Millisecond
	defer func() { teamRestartPoll = previous }()

	done := teamMarkBusy("aiman", teamBusyReleasing)
	server := staleServer{pid: os.Getpid(), unit: "hi-team-aiman.service", user: true, team: "aiman"}
	if got := teamBusy(server.team, server.pid); got != teamBusyReleasing {
		t.Fatal(got)
	}
	go func() {
		time.Sleep(50 * time.Millisecond)
		done()
	}()
	start := time.Now()
	var out bytes.Buffer
	if !teamWaitIdle(server.team, server.pid, &out) || !strings.Contains(out.String(), "Team aiman is releasing") || time.Since(start) < 50*time.Millisecond {
		t.Fatal(out.String())
	}

	// A note left by a process that has stopped doesn't hold it up.
	os.WriteFile(teamBusyPath("aiman", teamBusyPaused), []byte("999999\nx\n"), 0o600)
	if got := teamBusy("aiman", 999999); got != "" {
		t.Fatal(got)
	}

	// Still busy after a while: not restarted, and the command is said.
	teamMarkBusy("aiman", teamBusyPaused)
	patient := teamRestartPatient
	teamRestartPatient = 30 * time.Millisecond
	defer func() { teamRestartPatient = patient }()
	out.Reset()
	if teamWaitIdle("aiman", os.Getpid(), &out) || !strings.Contains(out.String(), "taking a snapshot") {
		t.Fatal(out.String())
	}
}

// A start unpauses what the service before left paused.
func TestTeamUnpauseLeftovers(t *testing.T) {
	t.Setenv("HI_TEAMS_DIR", t.TempDir())
	os.MkdirAll(filepath.Join(teamDir("payments"), "run"), 0o700)
	saveTeamSlot("payments", teamPreview{Slot: teamSlotProduction, Container: "app-prod"})
	saveTeamSlot("payments", teamPreview{Slot: teamSlotPreview, Container: "app-preview"})
	os.WriteFile(teamBusyPath("payments", teamBusyPaused), []byte("1\nhi-box-team-payments\napp-prod\n"), 0o600)
	teamMarkBusy("payments", teamBusyReleasing)

	states := map[string]string{"app-prod": "paused", "app-preview": "running", "hi-box-team-payments": "paused"}
	var unpaused []string
	previous := boxCommand
	boxCommand = func(_ io.Reader, stdout, _ io.Writer, _ string, args ...string) error {
		switch args[0] {
		case "container":
			fmt.Fprintln(stdout, states[args[len(args)-1]])
		case "unpause":
			unpaused = append(unpaused, args[1])
		}
		return nil
	}
	defer func() { boxCommand = previous }()

	var said bytes.Buffer
	teamUnpauseLeftovers(boxEngine{name: "podman", bin: "podman"}, "payments", &said)
	if strings.Join(unpaused, " ") != "hi-box-team-payments app-prod" || !strings.Contains(said.String(), "app-prod was left paused") {
		t.Fatal(unpaused, said.String())
	}
	if fileExists(teamBusyPath("payments", teamBusyPaused)) || fileExists(teamBusyPath("payments", teamBusyReleasing)) {
		t.Fatal("the notes stayed")
	}
}
