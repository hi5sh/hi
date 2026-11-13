package cli

// Every app needs a hi server (hi_server_apps.md, release 2): hi app new,
// hi app up, an app's service, hi update on a machine with apps, and the
// admin page's New app stop on a machine that has joined none and runs
// none, and say how to get one. A box already running isn't stopped.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// appServerJoined says whether this machine has a server: it has joined
// one, or runs one itself. Tests replace it.
var appServerJoined = func() bool {
	if connection, err := loadServerConnection(); err == nil && connection != nil && connection.URL != "" {
		return true
	}
	dir, err := orgDirectory("")
	return err == nil && fileExists(filepath.Join(dir, "config.json"))
}

// appNeedsServer is nil on a machine with a server, else what to run, as
// the admin runs it.
func appNeedsServer(what string) error {
	if appServerJoined() {
		return nil
	}
	return fmt.Errorf("%s needs a hi server, and this machine has none. Either:\n"+
		"  hi server join <server>   join your company's (ask your IT team for its address)\n"+
		"  hi server init            run one on this machine (fine for a small team or to try hi)", what)
}

// machineHasApps says whether any app's folder is on this machine.
func machineHasApps() bool {
	entries, err := os.ReadDir(appsDir())
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if appNamePattern.MatchString(entry.Name()) {
			if _, err := loadTeam(entry.Name()); err == nil || !errors.Is(err, os.ErrNotExist) {
				return true
			}
		}
	}
	return false
}
