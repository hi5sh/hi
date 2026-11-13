package cli

// Every app needs a hi server on its machine (hi_server_apps.md, releases
// 2 and 3): hi app new, hi app up, an app's service, hi update on a
// machine with apps, and the admin page's New app stop on a machine that
// runs no server, and say how to start one. A box already running isn't
// stopped. Managed mode (a machine joined to a central server as a
// machine) comes later and will count too.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// appServerHere says whether this machine runs a server. Tests replace it.
var appServerHere = func() bool {
	dir, err := orgDirectory("")
	return err == nil && fileExists(filepath.Join(dir, "config.json"))
}

// appNeedsServer is nil on a machine that runs a server, else what to run,
// as the admin runs it.
func appNeedsServer(what string) error {
	if appServerHere() {
		return nil
	}
	if connection, err := loadServerConnection(); err == nil && connection != nil && connection.URL != "" {
		return fmt.Errorf("%s needs a hi server on this machine. It is joined to %s as a device, which isn't enough. Run:\n"+
			"  hi server leave\n"+
			"  hi server init --owner <Slack member ID> --email <address>", what, connection.URL)
	}
	return fmt.Errorf("%s needs a hi server on this machine. Run:\n"+
		"  hi server init --owner <Slack member ID> --email <address>", what)
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
