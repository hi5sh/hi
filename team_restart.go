package main

// A team's service restarts safely. While it releases, or holds the boxes
// paused for a snapshot, it keeps a note in its run/ folder; hi update
// --restart waits for the note to go before it restarts the service. A
// service that stopped anyway, by a crash or a reboot, leaves production
// paused: the next start unpauses what the team left paused.

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	teamBusyReleasing = "releasing"
	teamBusyPaused    = "paused"
)

// These are replaced in tests.
var (
	teamRestartPoll    = 2 * time.Second
	teamRestartPatient = 30 * time.Minute
)

func teamBusyPath(team, what string) string {
	return filepath.Join(teamDir(team), "run", what)
}

// teamMarkBusy notes that this process is busy with what, and returns what
// takes the note away. The note holds the process id, then lines of its
// own, such as the containers paused.
func teamMarkBusy(team, what string, lines ...string) func() {
	path := teamBusyPath(team, what)
	text := strconv.Itoa(os.Getpid()) + "\n"
	for _, line := range lines {
		text += line + "\n"
	}
	os.MkdirAll(filepath.Dir(path), 0o700)
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		return func() {}
	}
	return func() { os.Remove(path) }
}

// teamBusy says what the team's service, process pid, is busy with, such
// as "releasing"; "" when it can restart. A note of another process, one
// that has stopped, doesn't count.
func teamBusy(team string, pid int) string {
	for _, what := range []string{teamBusyReleasing, teamBusyPaused} {
		data, err := os.ReadFile(teamBusyPath(team, what))
		if err != nil {
			continue
		}
		first, _, _ := strings.Cut(string(data), "\n")
		if noted, err := strconv.Atoi(strings.TrimSpace(first)); err == nil && noted == pid {
			if _, err := os.Stat(filepath.Join(updateProcRoot, strconv.Itoa(pid))); err == nil {
				return what
			}
		}
	}
	return ""
}

// teamWaitIdle waits until the team's service can restart, saying why it
// waits; false when it is still busy after teamRestartPatient.
func teamWaitIdle(team string, pid int, stdout io.Writer) bool {
	what := teamBusy(team, pid)
	if what == "" {
		return true
	}
	fmt.Fprintf(stdout, "Team %s is %s; hi restarts it when that is done.\n", team, map[string]string{
		teamBusyReleasing: "releasing",
		teamBusyPaused:    "taking a snapshot",
	}[what])
	deadline := time.Now().Add(teamRestartPatient)
	for time.Now().Before(deadline) {
		time.Sleep(teamRestartPoll)
		if teamBusy(team, pid) == "" {
			return true
		}
	}
	return false
}

// teamUnpauseLeftovers unpauses the team's boxes that a service which
// stopped while they were paused left so: those its note names, and the
// preview's and production's.
func teamUnpauseLeftovers(engine boxEngine, team string, stderr io.Writer) {
	var containers []string
	if data, err := os.ReadFile(teamBusyPath(team, teamBusyPaused)); err == nil {
		lines := strings.Split(strings.TrimSpace(string(data)), "\n")
		if len(lines) > 1 {
			containers = append(containers, lines[1:]...)
		}
	}
	for _, slot := range []string{teamSlotProduction, teamSlotPreview} {
		if state, ok := loadTeamSlot(team, slot); ok && state.Container != "" {
			containers = append(containers, state.Container)
		}
	}
	seen := map[string]bool{}
	for _, container := range containers {
		if container = strings.TrimSpace(container); container == "" || seen[container] {
			continue
		}
		seen[container] = true
		if state, _ := engine.output("container", "inspect", "--format", "{{.State.Status}}", container); strings.TrimSpace(state) != "paused" {
			continue
		}
		if _, err := engine.output("unpause", container); err != nil {
			fmt.Fprintf(stderr, "hi: %s was left paused, and doesn't unpause: %v\n", container, err)
			continue
		}
		fmt.Fprintf(stderr, "hi: %s was left paused by the service before; unpaused it\n", container)
	}
	os.Remove(teamBusyPath(team, teamBusyPaused))
	os.Remove(teamBusyPath(team, teamBusyReleasing))
}
