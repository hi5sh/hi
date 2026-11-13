package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// After an update, a running `hi server run` (or `hi org run`, its other name) or
// app (`hi app __serve`, `hi team __serve` in older units) still runs the
// old binary until it restarts. hi update finds them, and the systemd unit
// that runs them, and offers to restart them.

// These are replaced in tests.
var (
	updateProcRoot   = "/proc"
	updateIsTerminal = isTerminal
	updateRun        = func(stdin io.Reader, stdout, stderr io.Writer, name string, args ...string) error {
		command := exec.Command(name, args...)
		command.Stdin, command.Stdout, command.Stderr = stdin, stdout, stderr
		return command.Run()
	}
)

// staleServer is an org server, or a team, running a binary that has since
// been replaced.
type staleServer struct {
	pid  int
	unit string // such as hi-org.service, or "" when systemd doesn't run it
	user bool   // a systemd --user unit
	team string // the team's name; "" for a hi server
	// admin is the admin page (hi admin serve), a user service.
	admin bool
	// front is the served thing whose front this is (hi serve, on this
	// machine): hi run in a container, from the host's binary.
	front string
}

// what names it in a sentence.
func (s staleServer) what() string {
	if s.team != "" {
		return "App " + s.team
	}
	if s.admin {
		return "The admin page"
	}
	if s.front != "" {
		return "The front of " + s.front
	}
	return "A hi server"
}

// staleServers lists hi servers started from path whose binary has been
// replaced since: Linux shows their executable as "<path> (deleted)".
func staleServers(path string) []staleServer {
	entries, err := os.ReadDir(updateProcRoot)
	if err != nil {
		return nil
	}
	var servers []staleServer
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid == os.Getpid() {
			continue
		}
		dir := filepath.Join(updateProcRoot, entry.Name())
		executable, err := os.Readlink(filepath.Join(dir, "exe"))
		if err != nil {
			continue
		}
		command, err := os.ReadFile(filepath.Join(dir, "cmdline"))
		if err != nil {
			continue
		}
		args := strings.Split(strings.TrimRight(string(command), "\x00"), "\x00")
		server := staleServer{pid: pid}
		if len(args) >= 4 && args[1] == "serve" && args[2] == "__front" {
			// The front runs the host's binary under another path, mounted
			// into its container: it is old when it isn't the file at path.
			running, err := os.Stat(filepath.Join(dir, "exe"))
			installed, statErr := os.Stat(path)
			if err != nil || statErr != nil || os.SameFile(running, installed) {
				continue
			}
			server.unit, server.user = systemdUnit(filepath.Join(dir, "cgroup"))
			name, ok := strings.CutPrefix(strings.TrimSuffix(server.unit, "-front.service"), "hi-serve-")
			if !ok || !server.user || !strings.HasSuffix(server.unit, "-front.service") {
				continue
			}
			server.front = name
			servers = append(servers, server)
			continue
		}
		if executable != path+" (deleted)" {
			continue
		}
		switch {
		case len(args) >= 3 && (args[1] == "org" || args[1] == "server") && args[2] == "run":
		case len(args) == 4 && (args[1] == "app" || args[1] == "team") && args[2] == "__serve":
			server.team = args[3]
		case len(args) == 3 && args[1] == "admin" && args[2] == "serve":
			server.admin = true
		default:
			continue
		}
		server.unit, server.user = systemdUnit(filepath.Join(dir, "cgroup"))
		servers = append(servers, server)
	}
	return servers
}

// systemdUnit reads the service a process belongs to from its cgroup, such
// as 0::/system.slice/hi-org.service.
func systemdUnit(cgroupPath string) (string, bool) {
	data, err := os.ReadFile(cgroupPath)
	if err != nil {
		return "", false
	}
	for _, line := range strings.Split(string(data), "\n") {
		parts := strings.SplitN(line, ":", 3)
		if len(parts) != 3 {
			continue
		}
		segments := strings.Split(parts[2], "/")
		for i := len(segments) - 1; i >= 0; i-- {
			if strings.HasSuffix(segments[i], ".service") {
				if strings.HasPrefix(segments[i], "user@") {
					return "", false // the user's manager itself, not a unit in it
				}
				return segments[i], strings.Contains(parts[2], "/user@")
			}
		}
	}
	return "", false
}

// restartStaleServers offers to restart servers that run an old binary.
// mode is "ask", "yes", or "no".
func restartStaleServers(path, newVersion, mode string, stdin io.Reader, stdout, stderr io.Writer) error {
	offered := false
	var failed []string
	for _, server := range staleServers(path) {
		if server.team == "" && !server.admin && server.front == "" && !server.user && (server.unit == orgUnit || server.unit == oldOrgUnit) && systemOrgUnit() != "" {
			offered = true
			moved, err := offerOrgUnitMove(mode, stdin, stdout, stderr)
			if err != nil {
				return err
			}
			if moved {
				fmt.Fprintf(stdout, "%s runs %s.\n", orgUnit, newVersion)
				continue
			}
		}
		if server.unit == "" {
			fmt.Fprintf(stdout, "%s (process %d) still runs the old version; restart it to run %s.\n", server.what(), server.pid, newVersion)
			continue
		}
		down := "It is down for about a second."
		if server.front != "" {
			down = "Open connections to " + server.front + " drop; the model keeps running."
		}
		if server.team != "" {
			down = "Its lead restarts, which takes about half a minute; the app stays up, and moves to new boxes with the new hi in the next few minutes."
		}
		if server.admin {
			down = "The page is down for a second; its jobs go on."
		}
		command := []string{"systemctl", "restart", server.unit}
		switch {
		case server.user:
			command = []string{"systemctl", "--user", "restart", server.unit}
		case os.Geteuid() != 0:
			command = append([]string{"sudo"}, command...)
		}
		// sudo can't ask without a terminal, such as from the admin page's
		// Update hi: -n fails at once where it would ask, and the rest are
		// restarted all the same.
		if command[0] == "sudo" && !updateIsTerminal(stdin) {
			command = append([]string{"sudo", "-n"}, command[1:]...)
		}
		line := strings.Join(command, " ")
		if mode == "no" || (mode == "ask" && !updateIsTerminal(stdin)) {
			fmt.Fprintf(stdout, "%s still runs the old version. Restart it to run %s:\n  %s\n", server.unit, newVersion, line)
			continue
		}
		if mode == "ask" {
			fmt.Fprintf(stdout, "%s still runs the old version. Restart it now so it runs %s? %s [Y/n] ", server.unit, newVersion, down)
			answer, _ := readLine(stdin)
			if answer = strings.ToLower(strings.TrimSpace(answer)); answer != "" && answer != "y" && answer != "yes" {
				fmt.Fprintf(stdout, "Not restarted. When you are ready:\n  %s\n", line)
				continue
			}
		}
		if server.team != "" && !appWaitIdle(server.team, server.pid, stdout) {
			fmt.Fprintf(stdout, "App %s is still busy, so it isn't restarted. When it is done:\n  %s\n", server.team, line)
			continue
		}
		fmt.Fprintf(stdout, "$ %s\n", line)
		if err := updateRun(stdin, stdout, stderr, command[0], command[1:]...); err != nil {
			failed = append(failed, fmt.Sprintf("%s failed: %v", line, err))
			continue // one that fails doesn't keep the others on the old hi
		}
		check := []string{"is-active", "--quiet", server.unit}
		if server.user {
			check = append([]string{"--user"}, check...)
		}
		if err := updateRun(nil, io.Discard, io.Discard, "systemctl", check...); err != nil {
			logs := "journalctl -u " + server.unit
			if server.user {
				logs = "journalctl --user -u " + server.unit
			}
			failed = append(failed, fmt.Sprintf("%s did not come back up; check `%s`", server.unit, logs))
			continue
		}
		fmt.Fprintf(stdout, "%s runs %s.\n", server.unit, newVersion)
		if server.team != "" && server.unit == legacyAppUnit(server.team) {
			fmt.Fprintf(stdout, "  It still has the names from before v2.6.67; hi app up %s moves it to the new ones (its addresses change).\n", server.team)
		}
	}
	if !offered && systemOrgUnit() != "" {
		if _, err := offerOrgUnitMove(mode, stdin, stdout, stderr); err != nil {
			failed = append(failed, err.Error())
		}
	}
	if len(failed) > 0 {
		return errors.New(strings.Join(failed, "\n"))
	}
	return nil
}

// offerOrgUnitMove offers to make the org server a user service
// (org_migrate.go), which restarts it. It needs sudo this once, so it
// says how, rather than asking, without a terminal or with --no-restart.
func offerOrgUnitMove(mode string, stdin io.Reader, stdout, stderr io.Writer) (bool, error) {
	system := systemOrgUnit()
	if mode == "no" || !updateIsTerminal(stdin) {
		fmt.Fprintf(stdout, "The server becomes a user service in this hi, so the admin page can restart it. Run hi update on a terminal on this machine to move %s, with sudo this once. Until then it runs as before.\n", system)
		return false, nil
	}
	if mode == "ask" {
		fmt.Fprintf(stdout, "The server becomes a user service, beside the admin page and the apps, so the admin page can restart it. Move %s now? It needs sudo this once, and the server is down for about a second. [Y/n] ", system)
		answer, _ := readLine(stdin)
		if answer = strings.ToLower(strings.TrimSpace(answer)); answer != "" && answer != "y" && answer != "yes" {
			fmt.Fprintln(stdout, "Not moved; hi update asks again next time.")
			return false, nil
		}
	}
	return true, moveOrgUnit(stdin, stdout, stderr)
}
