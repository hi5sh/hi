package cli

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

// appDevTrust trusts a folder in the dev box for an agent before it
// starts or resumes there, so its first start in a new worktree doesn't
// stop at "Trust this folder?" in the terminal, where Chat can't answer
// it. Claude Code keeps trust in ~/.claude.json, Codex in its
// config.toml, agy in its settings (appDevAgyTrust).
func appDevTrust(team, agent, dir string) error {
	switch agent {
	case "claude", "":
		return appDevClaudeTrust(team, dir)
	case "codex":
		return appDevCodexTrust(team, dir)
	case "agy":
		return appDevAgyTrust(team, dir)
	}
	return nil
}

// appDevClaudeTrust marks a folder's trust dialog as accepted in Claude
// Code's ~/.claude.json, keeping the rest of it.
func appDevClaudeTrust(team, dir string) error {
	home := appDevHome(team)
	path := filepath.Join(home, ".claude.json")
	data, ok := boxedReadToUpdate(home, path)
	if !ok {
		return nil
	}
	state := map[string]any{}
	if len(data) > 0 {
		if err := json.Unmarshal(data, &state); err != nil {
			return nil // not Claude Code's JSON: left as it is
		}
	}
	projects, _ := state["projects"].(map[string]any)
	if projects == nil {
		projects = map[string]any{}
	}
	project, _ := projects[dir].(map[string]any)
	if project == nil {
		project = map[string]any{}
	}
	if accepted, _ := project["hasTrustDialogAccepted"].(bool); accepted {
		return nil
	}
	project["hasTrustDialogAccepted"] = true
	projects[dir], state["projects"] = project, projects
	out, _ := json.MarshalIndent(state, "", "  ")
	return boxedWriteFile(home, path, out, 0o600)
}

// appDevCodexTrustSection is Codex's config.toml table trusting a folder.
func appDevCodexTrustSection(dir string) string {
	return fmt.Sprintf("\n[projects.%q]\ntrust_level = \"trusted\"\n", dir)
}

// appDevCodexTrust adds a folder's trust to Codex's config.toml. Codex
// trusts a worktree by its repository's root, so /box/repo is trusted
// with it.
func appDevCodexTrust(team, dir string) error {
	home := appDevHome(team)
	path := filepath.Join(home, ".codex", "config.toml")
	data, ok := boxedReadToUpdate(home, path)
	if !ok {
		return nil
	}
	text := string(data)
	added := false
	for _, folder := range []string{"/box/repo", dir} {
		if folder == "" || strings.Contains(text, fmt.Sprintf("[projects.%q]", folder)) {
			continue
		}
		if text != "" && !strings.HasSuffix(text, "\n") {
			text += "\n"
		}
		text += appDevCodexTrustSection(folder)
		added = true
	}
	if !added {
		return nil
	}
	if err := boxedMkdirAll(home, filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return boxedWriteFile(home, path, []byte(text), 0o600)
}
