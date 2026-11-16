package cli

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Owners and Pro Devs get the dev box over SSH for their own editor: the
// peer's NetBird name once NetBird gave it, the account, and the host
// key's fingerprint. A Vibe Dev gets none of it.
func TestTeamDevEditorSSH(t *testing.T) {
	b, _ := appTestDevPage(t)
	view := b.devPageView(appPersonKey{User: "U07OWNER1", Name: "Olga"}, appLevelOwner)
	if view.SSH == nil || view.SSH.Host != "" || view.SSH.User != "box" || view.SSH.Alias != "hi-payments" || view.SSH.Project != "/box/repo" || view.SSH.HostKey != "" {
		t.Fatalf("before NetBird: %+v", view.SSH)
	}
	dir := filepath.Join(appDevDir("payments"), "ssh")
	os.MkdirAll(dir, 0o700)
	os.WriteFile(filepath.Join(appDevDir("payments"), "netbird-fqdn"), []byte("payments.corp.example\n"), 0o600)
	os.WriteFile(filepath.Join(dir, "host_ed25519.pub"), []byte("ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIIs8wkh6sA4GbEsMh8SitPfi5LOTDmY5P5Y/XoUFZ3Mg payments\n"), 0o600)
	for _, level := range []string{appLevelOwner, appLevelDeveloper} {
		view = b.devPageView(appPersonKey{User: "U07DEV001", Name: "Dev"}, level)
		// What ssh-keygen -lf says of that key.
		if view.SSH == nil || view.SSH.Host != "payments.corp.example" || view.SSH.HostKey != "SHA256:3gYn+FempvTwaBLROqwPHcpG3cXYFuDkme2fAbTTrU4" {
			t.Fatalf("%s: %+v", level, view.SSH)
		}
	}
	if view := b.devPageView(appPersonKey{User: "U07VIBE01", Name: "Vi"}, appLevelVibe); view.SSH != nil {
		t.Fatalf("a Vibe Dev: %+v", view.SSH)
	}
	if got := appSSHFingerprint("not a key"); got != "" {
		t.Fatal(got)
	}
}

// Snapshots leave out what editors install in the dev box's home, and
// only there.
func TestAppSnapshotEditorExcludes(t *testing.T) {
	args := appSnapshotEditorExcludes()
	for _, want := range []string{"dev/home/.vscode-server", "dev/home/.cursor-server", "dev/home/.zed_server", "dev/home/.cache/JetBrains"} {
		if i := slices.Index(args, want); i < 1 || args[i-1] != "--exclude" {
			t.Errorf("no --exclude %s in %v", want, args)
		}
	}
	for _, arg := range args {
		if arg != "--exclude" && !strings.HasPrefix(arg, "dev/home/.") {
			t.Errorf("%s isn't in the dev box's home", arg)
		}
	}
}
