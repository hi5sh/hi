package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTeamRename(t *testing.T) {
	dir := setupTeamTest(t)
	var calls, removed []string
	savedSystemctl, savedContainers := teamSystemctl, teamRemoveContainers
	teamSystemctl = func(args ...string) (string, error) { calls = append(calls, strings.Join(args, " ")); return "", nil }
	teamRemoveContainers = func(name string, boxes []string) { removed = append(removed, name) }
	t.Cleanup(func() { teamSystemctl, teamRemoveContainers = savedSystemctl, savedContainers })

	folder := filepath.Join(dir, "team-payments")
	os.MkdirAll(folder, 0o700)
	if err := teamRegister("payments", folder); err != nil {
		t.Fatal(err)
	}
	saveTeam(testTeamConfig())
	os.MkdirAll(filepath.Dir(teamUnitPath("payments")), 0o755)
	os.WriteFile(teamUnitPath("payments"), []byte("[Unit]\n"), 0o644)
	os.MkdirAll(teamSnapshotsDir("payments"), 0o700)
	os.WriteFile(teamSnapshotKey("payments"), []byte("key\n"), 0o600)
	if err := writeTeamHindsightConfig(filepath.Join(folder, "lead"), "payments", "payments", "10.0.0.2"); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(folder, "lead", "SOUL.md"), []byte(teamSoul(testTeamConfig())+"\nThe payments team's people like short answers.\n"), 0o600)

	var out bytes.Buffer
	if err := teamRename([]string{"payments", "Billing", "--yes"}, strings.NewReader(""), &out); err == nil {
		t.Fatal("renamed to a name that isn't one")
	}
	if err := teamRename([]string{"payments", "billing"}, strings.NewReader("y\n"), &out); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatal("renamed without asking:", err)
	}
	if err := teamRename([]string{"payments", "billing", "--yes"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}

	config, err := loadTeam("billing")
	if err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(dir, "team-billing")
	if config.Name != "billing" || config.MadeAs != "payments" || teamDir("billing") != moved || fileExists(folder) {
		t.Fatal(config.Name, config.MadeAs, teamDir("billing"))
	}
	if _, err := os.Lstat(filepath.Join(teamsDir(), "payments")); err == nil || fileExists(teamUnitPath("payments")) {
		t.Fatal("the old name is still on this machine")
	}
	if !fileExists(teamSnapshotKey("billing")) || fileExists(teamSnapshotsDir("payments")) || teamSnapshotHost("billing") != "hi-team-payments" {
		t.Fatal("the snapshots didn't carry over")
	}
	if len(removed) != 1 || removed[0] != "payments" || !strings.Contains(strings.Join(calls, "; "), "disable --now hi-team-payments.service") {
		t.Fatal(calls, removed)
	}
	if !strings.Contains(out.String(), "/hi-billing") || !strings.Contains(out.String(), "hi team manifest billing") {
		t.Fatal(out.String())
	}

	soul, _ := os.ReadFile(filepath.Join(moved, "lead", "SOUL.md"))
	if !strings.HasPrefix(string(soul), "# The billing team's lead\n\nYou lead the billing team,") || !strings.Contains(string(soul), "The payments team's people") {
		t.Fatal(string(soul))
	}

	// The memory bank stays the one it was made with; the context hi wrote
	// says the new name on the next start.
	lead := filepath.Join(moved, "lead")
	if err := writeTeamHindsightConfig(lead, config.Name, config.madeAs(), "10.0.0.2"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(lead, "hindsight", "config.json"))
	if !strings.Contains(string(data), `"bank_id": "team-payments"`) || !strings.Contains(string(data), "the billing team's") {
		t.Fatal(string(data))
	}

	// Back to the name it was made with: nothing to remember.
	if err := teamRename([]string{"billing", "payments", "--yes"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	if config, _ := loadTeam("payments"); config.MadeAs != "" {
		t.Fatal(config.MadeAs)
	}
}

func TestTeamRenameKeepsOthers(t *testing.T) {
	dir := setupTeamTest(t)
	savedSystemctl, savedContainers := teamSystemctl, teamRemoveContainers
	teamSystemctl = func(args ...string) (string, error) { return "", nil }
	teamRemoveContainers = func(name string, boxes []string) {}
	t.Cleanup(func() { teamSystemctl, teamRemoveContainers = savedSystemctl, savedContainers })
	for _, name := range []string{"payments", "billing"} {
		config := testTeamConfig()
		config.Name = name
		os.MkdirAll(filepath.Join(dir, "team-"+name), 0o700)
		teamRegister(name, filepath.Join(dir, "team-"+name))
		saveTeam(config)
	}
	if err := teamRename([]string{"payments", "billing", "--yes"}, strings.NewReader(""), &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "already a team") {
		t.Fatal(err)
	}
	os.MkdirAll(teamTaskDir("payments"), 0o700)
	if err := saveTeamTask("payments", teamTask{ID: "t-1", Status: "running"}); err != nil {
		t.Fatal(err)
	}
	if err := teamRename([]string{"payments", "invoices", "--yes"}, strings.NewReader(""), &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "t-1") {
		t.Fatal(err)
	}
}
