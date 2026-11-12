package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// v2.6.137 to v2.6.140 kept the site files in apps/sites, which listed as
// a broken app. They move out beside the apps folder.
func TestMoveAppSitesOutOfApps(t *testing.T) {
	base := t.TempDir()
	apps, dir := filepath.Join(base, "apps"), filepath.Join(base, "sites")
	old := filepath.Join(apps, "sites")
	os.MkdirAll(old, 0o700)
	os.WriteFile(filepath.Join(old, "tranco-top-1m.csv"), []byte("1,example.com\n"), 0o600)
	os.WriteFile(filepath.Join(old, "facts.json"), []byte("{}\n"), 0o600)

	moveAppSitesOutOfApps(old, dir)

	if fileExists(old) {
		t.Fatal("apps/sites is still there, so it still lists as an app")
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "tranco-top-1m.csv")); string(data) != "1,example.com\n" {
		t.Fatalf("the list didn't move: %q", data)
	}
	if !fileExists(filepath.Join(dir, "facts.json")) {
		t.Fatal("the facts didn't move")
	}
}

func TestMoveAppSitesLeavesAnAppCalledSites(t *testing.T) {
	base := t.TempDir()
	apps := filepath.Join(base, "apps")
	folder := filepath.Join(base, "app-sites")
	os.MkdirAll(apps, 0o700)
	os.MkdirAll(folder, 0o700)
	os.WriteFile(filepath.Join(folder, "facts.json"), []byte("the app's own\n"), 0o600)
	old := filepath.Join(apps, "sites")
	os.Symlink(folder, old)

	moveAppSitesOutOfApps(old, filepath.Join(base, "sites"))

	if data, _ := os.ReadFile(filepath.Join(folder, "facts.json")); string(data) != "the app's own\n" {
		t.Fatal("hi moved a file out of an app called sites")
	}
	if fileExists(filepath.Join(base, "sites")) {
		t.Fatal("hi made its sites folder out of an app's")
	}
}

func TestMoveAppSitesKeepsFilesItDidntPutThere(t *testing.T) {
	base := t.TempDir()
	old := filepath.Join(base, "apps", "sites")
	os.MkdirAll(old, 0o700)
	os.WriteFile(filepath.Join(old, "notes.txt"), []byte("mine\n"), 0o600)

	moveAppSitesOutOfApps(old, filepath.Join(base, "sites"))

	if !fileExists(filepath.Join(old, "notes.txt")) {
		t.Fatal("hi took away a file it didn't put there")
	}
}
