package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTeamRolesAreWrittenOnceAndKept(t *testing.T) {
	setupTeamTest(t)
	config := testTeamConfig()
	if err := writeTeamRoles(config); err != nil {
		t.Fatal(err)
	}
	for _, role := range teamRoles(config) {
		dir := filepath.Join(teamRolesDir("payments"), role)
		if role == "lead" {
			if fileExists(dir) {
				t.Fatal("the lead has SOUL.md, not a role folder")
			}
			continue
		}
		for _, name := range []string{teamRoleFile, teamLearnedFile} {
			if !fileExists(filepath.Join(dir, name)) {
				t.Fatalf("%s has no %s", role, name)
			}
		}
	}
	reviewer := filepath.Join(teamRolesDir("payments"), "reviewer", teamRoleFile)
	if data, _ := os.ReadFile(reviewer); !strings.Contains(string(data), "docs/map.md already lists") {
		t.Fatal(string(data))
	}
	// The owners' and the lead's edits stay.
	os.WriteFile(reviewer, []byte("# The reviewer\n\nCheck accessibility too.\n"), 0o600)
	writeTeamRoles(config)
	if data, _ := os.ReadFile(reviewer); string(data) != "# The reviewer\n\nCheck accessibility too.\n" {
		t.Fatal(string(data))
	}
	// Nothing learned yet is no section in a brief, only how to add to it.
	if brief := teamRoleBrief("payments", "coder"); strings.Contains(brief, "has learned on this team") || !strings.Contains(brief, `starting with "Learned:"`) {
		t.Fatal(brief)
	}
}

func TestTeamBriefsCarryTheRole(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	config, _ := loadTeam("payments")
	writeTeamRoles(config)
	learned := filepath.Join(teamRolesDir("payments"), "coder", teamLearnedFile)
	os.WriteFile(learned, []byte(teamLearnedDefault("coder")+"\n- Tests need TZ=UTC: the fixtures build today in local time. (t-7)\n"), 0o600)

	task := teamTestSmall(t, b, teamRequest{Brief: "Add a supplier list"})
	brief, _ := os.ReadFile(filepath.Join(teamTaskDir("payments"), task.ID+".md"))
	for _, want := range []string{
		teamSpecsRule + "\n\n" + teamMapRule,
		"## Your role on this team (roles/coder/ROLE.md)\n\n### The coder",
		"## What the coder has learned on this team (roles/coder/LEARNED.md)",
		"- Tests need TZ=UTC",
	} {
		if !strings.Contains(string(brief), want) {
			t.Fatalf("no %q in\n%s", want, brief)
		}
	}
	if strings.Contains(string(brief), "<!--") {
		t.Fatal("the brief carries LEARNED.md's comment")
	}
	// What a continuation gets from its root is the lead's text, not the role's.
	if body := teamBriefBody(string(brief)); body != "Add a supplier list" {
		t.Fatalf("%q", body)
	}

	review, err := b.startReview(teamRequest{Task: task.ID})
	if err != nil {
		t.Fatal(err)
	}
	review = waitTeamTask(t, review.ID)
	reviewBrief, _ := os.ReadFile(filepath.Join(teamTaskDir("payments"), review.ID+".md"))
	if !strings.Contains(string(reviewBrief), "## Your role on this team (roles/reviewer/ROLE.md)") || !strings.Contains(string(reviewBrief), "outside what was approved") {
		t.Fatal(string(reviewBrief))
	}

	// A long LEARNED.md is cut at a line.
	os.WriteFile(learned, []byte(strings.Repeat("- a lesson that is long enough to count (t-1)\n", 400)), 0o600)
	if brief := teamRoleBrief("payments", "coder"); !strings.Contains(brief, "(Cut here") || strings.Count(brief, "- a lesson") > teamLearnedLimit/40 {
		t.Fatal(len(brief))
	}
}

func TestTeamShowsLearnedProposals(t *testing.T) {
	report := json.RawMessage(`{"status": "complete", "summary": "Done.", "follow_ups": ["Learned: the suppliers API pages on the server", "Add an index later", "Learned:   "]}`)
	task := teamTask{ID: "t-4", Role: "coder", Agent: "claude", Status: "done", Title: "x", Report: &agentReport{Report: report}}
	var out bytes.Buffer
	printTeamTask(task, &out)
	if !strings.Contains(out.String(), "The coder proposes for /team/roles/coder/LEARNED.md") || !strings.Contains(out.String(), "  - the suppliers API pages on the server\n") || strings.Count(out.String(), "\n  - ") != 1 {
		t.Fatal(out.String())
	}
}

func TestTeamSoulLearnsAboutRoles(t *testing.T) {
	dir := t.TempDir()
	soul := filepath.Join(dir, "SOUL.md")
	// A SOUL.md an earlier hi wrote, without the paragraph on roles.
	old, _, _ := strings.Cut(teamSoul(testTeamConfig()), "\nEach member's role keeps")
	os.WriteFile(soul, []byte(old), 0o600)
	if err := updateTeamSoul(soul); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(soul); string(data) != teamSoul(testTeamConfig()) {
		t.Fatal(string(data))
	}
}
