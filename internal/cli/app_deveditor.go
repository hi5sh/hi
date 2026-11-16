package cli

// Your own editor on the dev box (docs/specs/approved/hi_app_dev_orca.md,
// release 1): an owner or a Pro Dev opens a workspace in VS Code, Cursor,
// Devin Desktop (Windsurf), Zed, JetBrains, Orca, or any editor that
// works over SSH, as the box's account on the app's NetBird peer. The page makes the links
// from what hi gives it here: the peer's name, the account, and the box's
// host key, for the question an editor asks on its first connect. The
// editors put their own servers in the box's home then, which come back
// on the next connect, so snapshots leave them out.

import (
	"crypto/sha256"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
)

// appDevSSHView is how someone's own editor reaches the dev box.
type appDevSSHView struct {
	// Host is the peer's NetBird name, once NetBird gave it one: before,
	// nothing reaches the box, and the page says what an admin runs.
	Host    string `json:"host,omitempty"`
	User    string `json:"user"`
	Alias   string `json:"alias"`   // hi-<app>, the name an SSH config gives it
	Project string `json:"project"` // the project's own clone, in the box
	// HostKey is the box's host key's fingerprint, as ssh shows it.
	HostKey string `json:"host_key,omitempty"`
}

// appDevSSH is the dev box over SSH, for an owner or a Pro Dev: a Vibe
// Dev has no SSH.
func appDevSSH(team, level string) *appDevSSHView {
	if level != appLevelOwner && level != appLevelDeveloper {
		return nil
	}
	view := &appDevSSHView{Host: appPeerKnownName(team), User: "box", Alias: "hi-" + team, Project: "/box/repo"}
	if data, err := os.ReadFile(filepath.Join(appDevDir(team), "ssh", "host_ed25519.pub")); err == nil {
		view.HostKey = appSSHFingerprint(string(data))
	}
	return view
}

// appSSHFingerprint is a public key's SHA256 fingerprint, as ssh-keygen -l
// and ssh's first-connect question show it, or "" for what isn't a key.
func appSSHFingerprint(line string) string {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return ""
	}
	blob, err := base64.StdEncoding.DecodeString(fields[1])
	if err != nil || len(blob) == 0 {
		return ""
	}
	sum := sha256.Sum256(blob)
	return "SHA256:" + base64.RawStdEncoding.EncodeToString(sum[:])
}

// appDevEditorServers are what editors install in the box's home over
// SSH, a few hundred megabytes each, and again for each of their
// versions: VS Code's and the editors made from it, Zed's, JetBrains'
// IDE backends and their caches, and Orca's relay. Each comes back on the
// editor's next connect.
var appDevEditorServers = []string{
	".vscode-server", ".vscode-server-insiders", ".vscodium-server",
	".cursor-server", ".windsurf-server", ".devin-server", ".antigravity-server",
	".zed_server",
	".cache/JetBrains", ".local/share/JetBrains",
	".orca-remote",
}

// appSnapshotEditorExcludes are restic's excludes for them, in the dev
// box's home only: a project's own folder of the same name stays.
func appSnapshotEditorExcludes() []string {
	var args []string
	for _, dir := range appDevEditorServers {
		args = append(args, "--exclude", filepath.ToSlash(filepath.Join("dev", "home", dir)))
	}
	return args
}
