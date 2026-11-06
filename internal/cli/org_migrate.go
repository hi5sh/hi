package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"strings"
)

// Until hi 2.6.57 the org server was hi server, and what it kept had that
// name: its state in ~/.local/state/hi/server, HI_SERVER_DIR, the unit
// hi-server.service, and a device's ~/.config/hi/server.json. Each moves
// to the new name on its own, and the old name keeps working (hi_org.md,
// Cleaning up).

const (
	orgUnit    = "hi-org.service"
	oldOrgUnit = "hi-server.service"
)

// systemdUnitDir is replaced in tests.
var systemdUnitDir = "/etc/systemd/system"

// orgStateParent is ~/.local/state/hi, or $XDG_STATE_HOME/hi.
func orgStateParent() (string, error) {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(base, "hi"), nil
}

// orgDirectoryIn is parent/org, or parent/server while only that exists.
func orgDirectoryIn(parent string) string {
	dir := filepath.Join(parent, "org")
	if _, err := os.Lstat(dir); errors.Is(err, os.ErrNotExist) && isRealDirectory(filepath.Join(parent, "server")) {
		return filepath.Join(parent, "server")
	}
	return dir
}

func isRealDirectory(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.IsDir()
}

// moveOrgDirectory moves the server's state from ~/.local/state/hi/server
// to ~/.local/state/hi/org when dir is either default and only the old one
// exists, and leaves a link at the old path for older hi binaries and
// units that still name it.
func moveOrgDirectory(dir string, stdout io.Writer) error {
	parent, err := orgStateParent()
	if err != nil {
		return err
	}
	old, target := filepath.Join(parent, "server"), filepath.Join(parent, "org")
	if clean := filepath.Clean(dir); clean != old && clean != target {
		return nil // a directory of the admin's choosing stays where it is
	}
	if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) || !isRealDirectory(old) {
		return nil
	}
	if err := os.Rename(old, target); err != nil {
		return fmt.Errorf("move the org server's state to %s: %w", target, err)
	}
	if err := os.Symlink("org", old); err != nil {
		return fmt.Errorf("moved the org server's state to %s, but couldn't link %s there: %w", target, old, err)
	}
	fmt.Fprintf(stdout, "Moved the org server's state from %s to %s; the old path links there.\n", old, target)
	return nil
}

// legacyConnectionPath is where a device kept its org server before hi
// 2.6.57.
func legacyConnectionPath() string {
	return filepath.Join(hiConfigDirectory(), "server.json")
}

// moveOrgConnection moves server.json to org.json the first time it's
// read, and links the old name there. An older hi saves server.json over
// the link; when that copy is newer, it is moved in again.
func moveOrgConnection() {
	path, old := serverConnectionPath(), legacyConnectionPath()
	oldInfo, err := os.Lstat(old)
	if err != nil || !oldInfo.Mode().IsRegular() {
		return
	}
	if info, err := os.Lstat(path); err == nil && !oldInfo.ModTime().After(info.ModTime()) {
		if os.Remove(old) == nil {
			os.Symlink(filepath.Base(path), old)
		}
		return
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return
	}
	if os.Rename(old, path) == nil {
		os.Symlink(filepath.Base(path), old)
	}
}

// orgRestartLine is how to restart the org server on this machine.
func orgRestartLine() string {
	switch system := systemOrgUnit(); {
	case fileExists(orgUserUnitPath()):
		return "systemctl --user restart " + orgUnit
	case system != "":
		return "sudo systemctl restart " + system
	}
	return "restart `hi org run`"
}

// orgUnitFrom rewrites a hi-server.service for hi-org.service: its
// description, HI_ORG_DIR in place of HI_SERVER_DIR (the new default
// directory where it named the old default, which hi org run then moves),
// and org run in place of server run. Anything else an admin added stays.
func orgUnitFrom(unit string) string {
	home := ""
	for _, line := range strings.Split(unit, "\n") {
		if name, ok := strings.CutPrefix(strings.TrimSpace(line), "User="); ok {
			if account, err := user.Lookup(strings.TrimSpace(name)); err == nil {
				home = account.HomeDir
			}
		}
	}
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	oldDefault := filepath.Join(home, ".local", "state", "hi", "server")
	lines := strings.Split(unit, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "Description="):
			lines[i] = "Description=hi org server"
		case strings.HasPrefix(trimmed, "Environment=") && strings.Contains(trimmed, "HI_SERVER_DIR="):
			fields := strings.Fields(strings.TrimPrefix(trimmed, "Environment="))
			for j, field := range fields {
				quote := ""
				if strings.HasPrefix(field, `"`) {
					quote, field = `"`, strings.Trim(field, `"`)
				}
				if value, ok := strings.CutPrefix(field, "HI_SERVER_DIR="); ok {
					if filepath.Clean(value) == oldDefault {
						value = filepath.Join(home, ".local", "state", "hi", "org")
					}
					fields[j] = quote + "HI_ORG_DIR=" + value + quote
				}
			}
			lines[i] = "Environment=" + strings.Join(fields, " ")
		case strings.HasPrefix(trimmed, "ExecStart="):
			fields := strings.Fields(trimmed)
			if len(fields) >= 3 && fields[1] == "server" && fields[2] == "run" {
				fields[1] = "org"
				lines[i] = strings.Join(fields, " ")
			}
		}
	}
	return strings.Join(lines, "\n")
}

// orgUserUnitPath is hi-org.service as a user service, beside the admin
// page's and the apps'.
func orgUserUnitPath() string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "systemd", "user", orgUnit)
}

// systemOrgUnit is the org server's system unit on this machine, from
// before it was a user service, or "".
func systemOrgUnit() string {
	for _, unit := range []string{orgUnit, oldOrgUnit} {
		if fileExists(filepath.Join(systemdUnitDir, unit)) {
			return unit
		}
	}
	return ""
}

// orgUserUnitText is the org server's user service. It can't wait for
// netbird.service, a system one, so it starts again until NetBird's
// address is there.
func orgUserUnitText(executable, dir string) string {
	return fmt.Sprintf(`[Unit]
Description=hi org server
After=network-online.target
Wants=network-online.target
StartLimitIntervalSec=0

[Service]
Environment=HI_ORG_DIR=%s
ExecStart=%s org run
Restart=always
RestartSec=5

[Install]
WantedBy=default.target
`, dir, executable)
}

// orgUserUnitFrom turns the system unit into a user service: as
// orgUnitFrom names it, without User= and Group=, started with the
// account's services. Anything else an admin added stays.
func orgUserUnitFrom(system string) string {
	var lines []string
	for _, line := range strings.Split(orgUnitFrom(system), "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "User="), strings.HasPrefix(trimmed, "Group="):
			continue
		case strings.HasPrefix(trimmed, "WantedBy="):
			line = "WantedBy=default.target"
		case trimmed == "[Unit]":
			lines = append(lines, line, "StartLimitIntervalSec=0")
			continue
		}
		lines = append(lines, line)
	}
	unit := strings.Join(lines, "\n")
	if !strings.Contains(unit, "RestartSec=") {
		unit = strings.Replace(unit, "[Service]\n", "[Service]\nRestartSec=5\n", 1)
	}
	return unit
}

// orgUnitAccount is the account the system unit runs the server as.
func orgUnitAccount(unit string) string {
	for _, line := range strings.Split(unit, "\n") {
		if name, ok := strings.CutPrefix(strings.TrimSpace(line), "User="); ok {
			return strings.TrimSpace(name)
		}
	}
	return "root"
}

// moveOrgUnit makes the org server a user service: hi-org.service in
// ~/.config/systemd/user from the system unit, the account's services kept
// running without a login, and the system unit removed, with sudo this
// once. If the user service doesn't come up, the system unit runs again.
func moveOrgUnit(stdin io.Reader, stdout, stderr io.Writer) error {
	system := systemOrgUnit()
	systemPath := filepath.Join(systemdUnitDir, system)
	data, err := os.ReadFile(systemPath)
	if err != nil {
		return err
	}
	if account := orgUnitAccount(string(data)); account != currentUserName() {
		return fmt.Errorf("%s runs the org server as %s; run hi update as %s to move it", system, account, account)
	}
	userPath := orgUserUnitPath()
	if err := os.MkdirAll(filepath.Dir(userPath), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(userPath, []byte(orgUserUnitFrom(string(data))), 0o644); err != nil {
		return err
	}
	run := func(sudo bool, args ...string) error {
		command := args
		if sudo && os.Geteuid() != 0 {
			command = append([]string{"sudo"}, args...)
		}
		fmt.Fprintf(stdout, "$ %s\n", strings.Join(command, " "))
		if err := updateRun(stdin, stdout, stderr, command[0], command[1:]...); err != nil {
			return fmt.Errorf("%s failed: %w", strings.Join(command, " "), err)
		}
		return nil
	}
	if !appLingers() {
		if err := run(true, "loginctl", "enable-linger", currentUserName()); err != nil {
			os.Remove(userPath)
			return err
		}
	}
	if err := run(true, "systemctl", "disable", "--now", system); err != nil {
		os.Remove(userPath)
		return err
	}
	back := func(err error) error {
		run(false, "systemctl", "--user", "disable", "--now", orgUnit)
		os.Remove(userPath)
		run(false, "systemctl", "--user", "daemon-reload")
		run(true, "systemctl", "enable", "--now", system)
		return err
	}
	for _, step := range [][]string{{"systemctl", "--user", "daemon-reload"}, {"systemctl", "--user", "enable", "--now", orgUnit}} {
		if err := run(false, step...); err != nil {
			return back(err)
		}
	}
	if err := updateRun(nil, io.Discard, io.Discard, "systemctl", "--user", "is-active", "--quiet", orgUnit); err != nil {
		return back(fmt.Errorf("the user service %s did not come up, so %s runs again; check `journalctl --user -u %s`", orgUnit, system, orgUnit))
	}
	removed := run(true, "rm", systemPath) == nil && run(true, "systemctl", "daemon-reload") == nil
	fmt.Fprintf(stdout, "The org server is a user service now, %s, beside the admin page: the admin page can restart it, and hi update does without sudo.\n", orgUnit)
	if !removed {
		fmt.Fprintf(stdout, "%s stays, disabled; remove it when you like: sudo rm %s\n", system, systemPath)
	}
	return nil
}
