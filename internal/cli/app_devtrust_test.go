package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Each agent's first start in a new worktree, from the page: the folder
// is trusted first, so no agent stops at "Trust this folder?" in the
// terminal; what else is in the files stays, and a second start adds
// nothing twice.
func TestAppDevTrust(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	appTestDevPage(t)
	home := appDevHome("payments")
	os.MkdirAll(filepath.Join(home, ".codex"), 0o700)
	os.WriteFile(filepath.Join(home, ".claude.json"), []byte(`{"hasCompletedOnboarding":true,"projects":{"/box/repo":{"hasTrustDialogAccepted":true,"allowedTools":["Bash"]}}}`), 0o600)
	os.WriteFile(filepath.Join(home, ".codex", "config.toml"), []byte("approval_policy = \"never\"\n[model_providers.hi]\nname = \"hi\""), 0o600)
	dir := "/box/home/.herdr/worktrees/repo/16-countdown"

	for range 2 {
		for _, agent := range []string{"claude", "codex"} {
			if err := appDevTrust("payments", agent, dir); err != nil {
				t.Fatal(agent, err)
			}
		}
	}
	claude, _ := os.ReadFile(filepath.Join(home, ".claude.json"))
	if s := string(claude); !strings.Contains(s, `"`+dir+`": {`) || !strings.Contains(s, `"allowedTools"`) || !strings.Contains(s, `"hasCompletedOnboarding": true`) {
		t.Fatal("Claude Code's:", s)
	}
	codex, _ := os.ReadFile(filepath.Join(home, ".codex", "config.toml"))
	want := "approval_policy = \"never\"\n[model_providers.hi]\nname = \"hi\"\n\n[projects.\"/box/repo\"]\ntrust_level = \"trusted\"\n\n[projects.\"" + dir + "\"]\ntrust_level = \"trusted\"\n"
	if string(codex) != want {
		t.Fatalf("Codex's:\n%s", codex)
	}
	// No config of Codex's yet: one with the trust alone.
	os.Remove(filepath.Join(home, ".codex", "config.toml"))
	if err := appDevTrust("payments", "codex", dir); err != nil {
		t.Fatal(err)
	}
	if codex, _ := os.ReadFile(filepath.Join(home, ".codex", "config.toml")); !strings.HasPrefix(string(codex), "\n[projects.\"/box/repo\"]") {
		t.Fatalf("Codex's new:\n%s", codex)
	}
}
