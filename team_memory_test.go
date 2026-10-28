package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTeamInstallHindsightPluginInANewLead(t *testing.T) {
	hermes, share := t.TempDir(), t.TempDir()
	t.Setenv("HERMES_INSTALL_DIR", hermes)
	t.Setenv("XDG_DATA_HOME", share)
	sha := "0123456789abcdef0123456789abcdef01234567"
	writeSkillTestFile(t, filepath.Join(hermes, "plugin-catalog", "hindsight.yaml"),
		"repo: https://example.com/plugins.git\nsha: \""+sha+"\"\nsubdir: hindsight\n", 0o644)
	// The plugin is downloaded already: no git here.
	writeSkillTestFile(t, filepath.Join(hiShareDir(), "hindsight-plugin-"+sha[:12], "plugin.yaml"), "name: hindsight\n", 0o644)

	// A new team's lead folder, with no plugins folder in it yet.
	lead := filepath.Join(t.TempDir(), "lead")
	os.MkdirAll(lead, 0o700)
	if err := teamInstallHindsightPlugin(lead); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{"plugin.yaml", ".hermes-catalog.json"} {
		if !fileExists(filepath.Join(lead, "plugins", "hindsight", file)) {
			t.Errorf("no %s", file)
		}
	}
	// The same commit again changes nothing.
	if err := teamInstallHindsightPlugin(lead); err != nil {
		t.Fatal(err)
	}
}
