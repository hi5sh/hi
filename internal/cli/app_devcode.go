package cli

// VS Code on the dev page (code-server in the dev box) went in hi 2.6.144:
// nobody used it. What it left in a dev box, a gigabyte or so, goes the
// first time the app's service starts with a hi without it.

import (
	"os"
	"path/filepath"
)

// appDevCodeLeftovers are code-server's files in the box's home: itself,
// its log, the extensions' marker, and its settings and extensions.
var appDevCodeLeftovers = []string{
	filepath.Join(".hi", "code-server"),
	filepath.Join(".hi", "code-server.log"),
	filepath.Join(".hi", "code-server-extensions"),
	filepath.Join(".local", "share", "code-server"),
}

// removeDevCode stops a code-server still running in the dev box and
// removes its files.
func (b *appBroker) removeDevCode() {
	home := filepath.Join(appDevDir(b.team), "home")
	left := false
	for _, rel := range appDevCodeLeftovers {
		if boxedExists(home, filepath.Join(home, rel)) {
			left = true
		}
	}
	if !left {
		return
	}
	if b.running(appDevContainer(b.team)) {
		appDevBoxExec(b.team, "pkill", "-f", "/box/home/.hi/code-server/")
	}
	for _, rel := range appDevCodeLeftovers {
		boxedRemoveAll(home, filepath.Join(home, rel))
	}
	os.Remove(filepath.Join(appDevSocketDir(b.team), "code.sock"))
}
