package cli

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
	t.Setenv("HI_APPS_DIR", t.TempDir())
	pid := strconv.Itoa(os.Getpid())
	fakeProc(t, map[string][3]string{pid: {"/usr/local/bin/hi (deleted)", "hi\x00team\x00__serve\x00bulletin\x00", ""}})
	previous := appRestartPoll
	appRestartPoll = 10 * time.Millisecond
	defer func() { appRestartPoll = previous }()

	done := appMarkBusy("bulletin", appBusyReleasing)
	server := staleServer{pid: os.Getpid(), unit: "hi-app-bulletin.service", user: true, team: "bulletin"}
	if got := appBusy(server.team, server.pid); got != appBusyReleasing {
		t.Fatal(got)
	}
	go func() {
		time.Sleep(50 * time.Millisecond)
		done()
	}()
	start := time.Now()
	var out bytes.Buffer
	if !appWaitIdle(server.team, server.pid, &out) || !strings.Contains(out.String(), "App bulletin is releasing") || time.Since(start) < 50*time.Millisecond {
		t.Fatal(out.String())
	}

	// A note left by a process that has stopped doesn't hold it up.
	os.WriteFile(appBusyPath("bulletin", appBusyPaused), []byte("999999\nx\n"), 0o600)
	if got := appBusy("bulletin", 999999); got != "" {
		t.Fatal(got)
	}

	// Still busy after a while: not restarted, and the command is said.
	appMarkBusy("bulletin", appBusyPaused)
	patient := appRestartPatient
	appRestartPatient = 30 * time.Millisecond
	defer func() { appRestartPatient = patient }()
	out.Reset()
	if appWaitIdle("bulletin", os.Getpid(), &out) || !strings.Contains(out.String(), "taking a snapshot") {
		t.Fatal(out.String())
	}
}

// A start unpauses what the service before left paused.
func TestTeamUnpauseLeftovers(t *testing.T) {
	t.Setenv("HI_APPS_DIR", t.TempDir())
	os.MkdirAll(filepath.Join(appDir("payments"), "run"), 0o700)
	saveTeamSlot("payments", appPreview{Slot: appSlotProduction, Container: "app-prod"})
	saveTeamSlot("payments", appPreview{Slot: appSlotPreview, Container: "app-preview"})
	os.WriteFile(appBusyPath("payments", appBusyPaused), []byte("1\nhi-box-app-payments\napp-prod\n"), 0o600)
	appMarkBusy("payments", appBusyReleasing)

	states := map[string]string{"app-prod": "paused", "app-preview": "running", "hi-box-app-payments": "paused"}
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
	appUnpauseLeftovers(boxEngine{name: "podman", bin: "podman"}, "payments", &said)
	if strings.Join(unpaused, " ") != "hi-box-app-payments app-prod" || !strings.Contains(said.String(), "app-prod was left paused") {
		t.Fatal(unpaused, said.String())
	}
	if fileExists(appBusyPath("payments", appBusyPaused)) || fileExists(appBusyPath("payments", appBusyReleasing)) {
		t.Fatal("the notes stayed")
	}
}
