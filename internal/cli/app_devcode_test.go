package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// VS Code went in 2.6.144: what it left in the dev box's home goes, and
// nothing else there.
func TestTeamRemoveDevCode(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	home := filepath.Join(appDevDir("payments"), "home")
	for _, rel := range appDevCodeLeftovers {
		path := filepath.Join(home, rel)
		os.MkdirAll(filepath.Dir(path), 0o700)
		os.WriteFile(path, []byte("x"), 0o600)
	}
	os.MkdirAll(filepath.Join(home, ".hi", "code-server", "4.141.0", "bin"), 0o700)
	kept := filepath.Join(home, ".hi", "kept")
	os.WriteFile(kept, []byte("x"), 0o600)

	b.removeDevCode()
	for _, rel := range appDevCodeLeftovers {
		if _, err := os.Lstat(filepath.Join(home, rel)); !os.IsNotExist(err) {
			t.Errorf("%s is still there: %v", rel, err)
		}
	}
	if _, err := os.Stat(kept); err != nil {
		t.Fatal("removed what VS Code didn't leave:", err)
	}
	b.removeDevCode() // nothing left: nothing to do
}
