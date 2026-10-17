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

func TestTeamAppKeepsTeamFilesOutOfTheProject(t *testing.T) {
	layers, err := builtinTemplateLayers()
	if err != nil {
		t.Fatal(err)
	}
	composed, err := composeTemplateAllowingHidden(layers, teamAppTemplate, map[string]string{"name": "payments"})
	if err != nil {
		t.Fatal(err)
	}
	for name := range composed.Files {
		if strings.HasPrefix(name, teamTemplateDir+"/") {
			t.Fatalf("%s is in the project", name)
		}
	}
	if _, ok := composed.Files["docs/map.md"]; !ok {
		t.Fatal("no map in the project")
	}
	if string(composed.Team["roles/reviewer/ROLE.md"]) != teamRoleDefault("reviewer") || !strings.Contains(teamRoleDefault("reviewer"), "docs/map.md") {
		t.Fatal("the reviewer's role isn't the template's")
	}
	if !strings.HasPrefix(teamRoleDefault("designer"), "# The designer") {
		t.Fatal(teamRoleDefault("designer"))
	}
}

func TestTeamFromATemplate(t *testing.T) {
	dir := setupTeamTest(t)
	t.Setenv("HI_TEAM_SLACK_BOT_TOKEN", "xoxb-bot")
	t.Setenv("HI_TEAM_SLACK_APP_TOKEN", "xapp-app")
	source := filepath.Join(dir, "source")
	writeSkillTestFile(t, filepath.Join(source, "desk", "layer.json"), `{"name": "desk", "summary": "A research desk", "extends": "team-app", "schema": 1}`, 0o644)
	writeSkillTestFile(t, filepath.Join(source, "desk", "team", "roles", "coder", "ROLE.md"), "# The coder\n\nBacktests come with their data's dates.\n", 0o644)
	writeSkillTestFile(t, filepath.Join(source, "desk", "team", "roles", "researcher", "ROLE.md"), "# The researcher\n\nCite the data.\n", 0o644)
	writeSkillTestFile(t, filepath.Join(source, "desk", "team", "notes.txt"), "not a role\n", 0o644)
	writeSkillTestFile(t, filepath.Join(source, "desk", "docs", "desk.md"), "# Hifin Template Name desk\n", 0o644)
	writeSkillTestFile(t, filepath.Join(source, "plain", "layer.json"), `{"name": "plain", "summary": "Not a team's", "extends": "app", "schema": 1}`, 0o644)
	t.Setenv("HI_TEMPLATES_DIR", source)

	answers := filepath.Join(dir, "answers.json")
	writeSkillTestFile(t, answers, `{"purpose": "x", "channel": "C07PAYMENTS", "owners": ["U07OWNER1"], "template": "plain"}`, 0o600)
	var out bytes.Buffer
	if err := teamNew([]string{"payments", "--from", answers}, strings.NewReader(""), &out); err == nil || !strings.Contains(err.Error(), `"plain" isn't a team template`) {
		t.Fatal(err)
	}

	writeSkillTestFile(t, answers, `{"purpose": "x", "channel": "C07PAYMENTS", "owners": ["U07OWNER1"], "template": "desk"}`, 0o600)
	out.Reset()
	if err := teamNew([]string{"payments", "--from", answers}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	roles := teamRolesDir("payments")
	if data, _ := os.ReadFile(filepath.Join(roles, "coder", teamRoleFile)); !strings.Contains(string(data), "Backtests come with their data's dates") {
		t.Fatal(string(data))
	}
	// The roles the template doesn't have come from hi's defaults.
	if data, _ := os.ReadFile(filepath.Join(roles, "reviewer", teamRoleFile)); string(data) != teamRoleDefault("reviewer") {
		t.Fatal(string(data))
	}
	if !strings.Contains(out.String(), "The template also has roles for researcher") || fileExists(filepath.Join(roles, "notes.txt")) {
		t.Fatal(out.String())
	}
	repo := filepath.Join(teamDir("payments"), "repo")
	files := boxGit(repo, "ls-files")
	if !strings.Contains(files, "docs/desk.md") || !strings.Contains(files, "docs/map.md") || strings.Contains(files, "team/") {
		t.Fatal(files)
	}
	if config, _ := loadTeam("payments"); config.Template != "desk" {
		t.Fatalf("%+v", config)
	}
}

func TestTeamClonedTakesATemplatesRoles(t *testing.T) {
	dir := setupTeamTest(t)
	t.Setenv("HI_TEAM_SLACK_BOT_TOKEN", "xoxb-bot")
	t.Setenv("HI_TEAM_SLACK_APP_TOKEN", "xapp-app")
	source := filepath.Join(dir, "source")
	writeSkillTestFile(t, filepath.Join(source, "desk", "layer.json"), `{"name": "desk", "summary": "A research desk", "extends": "team-app", "schema": 1}`, 0o644)
	writeSkillTestFile(t, filepath.Join(source, "desk", "team", "roles", "coder", "ROLE.md"), "# The coder\n\nFrom the desk.\n", 0o644)
	t.Setenv("HI_TEMPLATES_DIR", source)
	answers := filepath.Join(dir, "answers.json")
	writeSkillTestFile(t, answers, `{"purpose": "x", "channel": "C07PAYMENTS", "owners": ["U07OWNER1"], "template": "desk", "empty": true}`, 0o600)
	if err := teamNew([]string{"payments", "--from", answers}, strings.NewReader(""), &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(filepath.Join(teamRolesDir("payments"), "coder", teamRoleFile)); string(data) != "# The coder\n\nFrom the desk.\n" {
		t.Fatal(string(data))
	}
	if files := boxGit(filepath.Join(teamDir("payments"), "repo"), "ls-files"); files != "AGENTS.md\nREADME.md\ndocs/specs/README.md" {
		t.Fatal(files)
	}
}
