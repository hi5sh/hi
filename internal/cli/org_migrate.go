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

// installedOrgUnit is the org server's system unit on this machine:
// hi-org.service, or hi-server.service where only that is installed.
func installedOrgUnit() string {
	if !fileExists(filepath.Join(systemdUnitDir, orgUnit)) && fileExists(filepath.Join(systemdUnitDir, oldOrgUnit)) {
		return oldOrgUnit
	}
	return orgUnit
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

// moveOrgUnit replaces hi-server.service with hi-org.service, which also
// restarts the server on the new binary. If the new unit doesn't come up,
// the old one is started again.
func moveOrgUnit(stdin io.Reader, stdout, stderr io.Writer) error {
	oldPath := filepath.Join(systemdUnitDir, oldOrgUnit)
	data, err := os.ReadFile(oldPath)
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp("", "hi-org-*.service")
	if err != nil {
		return err
	}
	defer os.Remove(temporary.Name())
	if _, err := temporary.WriteString(orgUnitFrom(string(data))); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	sudo := []string{}
	if os.Geteuid() != 0 {
		sudo = []string{"sudo"}
	}
	run := func(args ...string) error {
		command := append(append([]string{}, sudo...), args...)
		fmt.Fprintf(stdout, "$ %s\n", strings.Join(command, " "))
		if err := updateRun(stdin, stdout, stderr, command[0], command[1:]...); err != nil {
			return fmt.Errorf("%s failed: %w", strings.Join(command, " "), err)
		}
		return nil
	}
	steps := [][]string{
		{"install", "-m", "0644", temporary.Name(), filepath.Join(systemdUnitDir, orgUnit)},
		{"systemctl", "daemon-reload"},
		{"systemctl", "disable", "--now", oldOrgUnit},
		{"systemctl", "enable", "--now", orgUnit},
	}
	for i, step := range steps {
		if err := run(step...); err != nil {
			if i >= 2 {
				run("systemctl", "enable", "--now", oldOrgUnit)
			}
			return err
		}
	}
	if err := updateRun(nil, io.Discard, io.Discard, "systemctl", "is-active", "--quiet", orgUnit); err != nil {
		run("systemctl", "disable", "--now", orgUnit)
		run("systemctl", "enable", "--now", oldOrgUnit)
		return fmt.Errorf("%s did not come up, so %s runs again; check `journalctl -u %s`", orgUnit, oldOrgUnit, orgUnit)
	}
	fmt.Fprintf(stdout, "%s runs the org server now. %s stays, disabled; remove it when you like: sudo rm %s\n", orgUnit, oldOrgUnit, oldPath)
	return nil
}
