package cli

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
	savedSystemctl, savedContainers := appSystemctl, appRemoveContainers
	appSystemctl = func(args ...string) (string, error) { calls = append(calls, strings.Join(args, " ")); return "", nil }
	appRemoveContainers = func(name string, boxes []string) { removed = append(removed, name) }
	t.Cleanup(func() { appSystemctl, appRemoveContainers = savedSystemctl, savedContainers })

	folder := filepath.Join(dir, "team-payments")
	os.MkdirAll(folder, 0o700)
	if err := appRegister("payments", folder); err != nil {
		t.Fatal(err)
	}
	saveTeam(testTeamConfig())
	os.MkdirAll(filepath.Dir(appUnitPath("payments")), 0o755)
	os.WriteFile(appUnitPath("payments"), []byte("[Unit]\n"), 0o644)
	os.MkdirAll(appSnapshotsDir("payments"), 0o700)
	os.WriteFile(appSnapshotKey("payments"), []byte("key\n"), 0o600)
	if err := writeTeamHindsightConfig(filepath.Join(folder, "lead"), "payments", "payments", "10.0.0.2"); err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Join(folder, "lead", "skills", appNotesSkill), 0o700)
	os.WriteFile(filepath.Join(folder, "lead", "skills", appNotesSkill, "SKILL.md"), []byte(appNotes(testTeamConfig())), 0o600)
	os.WriteFile(filepath.Join(folder, "slack-manifest.json"), []byte(appManifest(testTeamConfig())+"\n"), 0o600)
	os.WriteFile(filepath.Join(folder, "lead", "SOUL.md"), []byte(appSoul(testTeamConfig())+"\nThe payments team's people like short answers.\n"), 0o600)

	var out bytes.Buffer
	if err := appRename([]string{"payments", "Billing", "--yes"}, strings.NewReader(""), &out); err == nil {
		t.Fatal("renamed to a name that isn't one")
	}
	if err := appRename([]string{"payments", "billing"}, strings.NewReader("y\n"), &out); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatal("renamed without asking:", err)
	}
	if err := appRename([]string{"payments", "billing", "--yes"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}

	config, err := loadTeam("billing")
	if err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(dir, "app-billing")
	if config.Name != "billing" || config.MadeAs != "payments" || appDir("billing") != moved || fileExists(folder) {
		t.Fatal(config.Name, config.MadeAs, appDir("billing"))
	}
	if _, err := os.Lstat(filepath.Join(teamsDir(), "payments")); err == nil || fileExists(appUnitPath("payments")) {
		t.Fatal("the old name is still on this machine")
	}
	if !fileExists(appSnapshotKey("billing")) || fileExists(appSnapshotsDir("payments")) || appSnapshotHost("billing") != "hi-team-payments" {
		t.Fatal("the snapshots didn't carry over")
	}
	if len(removed) != 1 || removed[0] != "payments" || !strings.Contains(strings.Join(calls, "; "), "disable --now hi-team-payments.service") {
		t.Fatal(calls, removed)
	}
	if !strings.Contains(out.String(), "/hi-billing") || !strings.Contains(out.String(), "hi app manifest billing") {
		t.Fatal(out.String())
	}

	soul, _ := os.ReadFile(filepath.Join(moved, "lead", "SOUL.md"))
	if !strings.HasPrefix(string(soul), "# The billing app's lead\n\nYou are the billing app's lead") || !strings.Contains(string(soul), "The payments team's people") {
		t.Fatal(string(soul))
	}

	notes, _ := os.ReadFile(filepath.Join(moved, "lead", "skills", appNotesSkill, "SKILL.md"))
	manifest, _ := os.ReadFile(filepath.Join(moved, "slack-manifest.json"))
	if string(notes) != appNotes(config) || string(manifest) != appManifest(config)+"\n" || strings.Contains(string(manifest), "/hi-payments") {
		t.Fatal(string(notes), string(manifest))
	}

	// The memory bank stays the one it was made with; the context hi wrote
	// says the new name on the next start.
	lead := filepath.Join(moved, "lead")
	if err := writeTeamHindsightConfig(lead, config.Name, config.madeAs(), "10.0.0.2"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(lead, "hindsight", "config.json"))
	if !strings.Contains(string(data), `"bank_id": "team-payments"`) || !strings.Contains(string(data), "the billing app's") {
		t.Fatal(string(data))
	}

	// Back to the name it was made with: nothing to remember.
	if err := appRename([]string{"billing", "payments", "--yes"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	if config, _ := loadTeam("payments"); config.MadeAs != "" {
		t.Fatal(config.MadeAs)
	}
}

func TestTeamRenameKeepsOthers(t *testing.T) {
	dir := setupTeamTest(t)
	savedSystemctl, savedContainers := appSystemctl, appRemoveContainers
	appSystemctl = func(args ...string) (string, error) { return "", nil }
	appRemoveContainers = func(name string, boxes []string) {}
	t.Cleanup(func() { appSystemctl, appRemoveContainers = savedSystemctl, savedContainers })
	for _, name := range []string{"payments", "billing"} {
		config := testTeamConfig()
		config.Name = name
		os.MkdirAll(filepath.Join(dir, "team-"+name), 0o700)
		appRegister(name, filepath.Join(dir, "team-"+name))
		saveTeam(config)
	}
	if err := appRename([]string{"payments", "billing", "--yes"}, strings.NewReader(""), &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "already an app") {
		t.Fatal(err)
	}
	os.MkdirAll(appTaskDir("payments"), 0o700)
	if err := saveTeamTask("payments", appTask{ID: "t-1", Status: "running"}); err != nil {
		t.Fatal(err)
	}
	if err := appRename([]string{"payments", "invoices", "--yes"}, strings.NewReader(""), &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "t-1") {
		t.Fatal(err)
	}
}

func TestTeamFreeMadeAsAfterRename(t *testing.T) {
	dir := setupTeamTest(t)
	savedSystemctl, savedContainers := appSystemctl, appRemoveContainers
	appSystemctl = func(args ...string) (string, error) { return "", nil }
	appRemoveContainers = func(name string, boxes []string) {}
	t.Cleanup(func() { appSystemctl, appRemoveContainers = savedSystemctl, savedContainers })
	if made := appFreeMadeAs("helpdesk"); made != "helpdesk" {
		t.Fatal(made)
	}
	config := testTeamConfig()
	config.Name = "helpdesk"
	os.MkdirAll(filepath.Join(dir, "team-helpdesk"), 0o700)
	appRegister("helpdesk", filepath.Join(dir, "team-helpdesk"))
	saveTeam(config)
	if err := appRename([]string{"helpdesk", "helpdesk-old", "--yes"}, strings.NewReader(""), &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	// The renamed team keeps memory and snapshots under helpdesk; a new
	// helpdesk is made as helpdesk-2, and the one after that helpdesk-3.
	if made := appFreeMadeAs("helpdesk"); made != "helpdesk-2" {
		t.Fatal(made)
	}
	config = testTeamConfig()
	config.Name, config.MadeAs = "helpdesk", "helpdesk-2"
	os.MkdirAll(filepath.Join(dir, "team-helpdesk"), 0o700)
	appRegister("helpdesk", filepath.Join(dir, "team-helpdesk"))
	saveTeam(config)
	if err := appRename([]string{"helpdesk", "helpdesk-new", "--yes"}, strings.NewReader(""), &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if made := appFreeMadeAs("helpdesk"); made != "helpdesk-3" {
		t.Fatal(made)
	}
}
