package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Roles. A member of a team is a position, not a run: each task is a
// fresh session in a fresh box, but the position keeps a home in the
// team's folder, roles/<role>/, with its standing instructions (ROLE.md)
// and what it has learned on this team (LEARNED.md). The broker puts both
// in every brief for the role, so what one coder learned reaches the
// next, and switching the coder to another agent keeps it. Members
// propose lessons as follow-ups that start with "Learned:"; the lead,
// who has /team/roles writable, decides what to keep.
// Spec: docs/specs/approved/hi_team_roles.md.

const (
	teamRoleFile    = "ROLE.md"
	teamLearnedFile = "LEARNED.md"
	// teamLearnedLimit is the most of LEARNED.md a brief carries; the lead
	// keeps it to about half that.
	teamLearnedLimit = 6 << 10
	// teamLearnedPrefix starts a follow-up that proposes a lesson.
	teamLearnedPrefix = "Learned:"
)

// teamMapRule follows the specs rule in every brief: the project's map is
// how a member finds what already exists before building.
const teamMapRule = "docs/map.md is the project's map: what it already has (pages, API routes, tables, shared code, outside services) and where each lives. Read it before you add anything, and extend what it lists rather than building something beside it. When your work adds or moves something on it, update it in the same work, and add a short file to docs/decisions/ for a technical choice someone shouldn't undo by accident. If there's no docs/map.md yet and your work changes code, write it from the code as part of this work. The docs at /docs come from the project too: update the user docs (docs/user/) for anything the people will see, and docs/tech/index.md when the parts or how they connect change."

func teamRolesDir(team string) string { return filepath.Join(teamDir(team), "roles") }

// writeTeamRoles gives every member's role a home with its files, leaving
// what's there alone: the files are the lead's and the owners' to edit.
func writeTeamRoles(config teamConfig) error {
	for _, role := range teamRoles(config) {
		if role == "lead" {
			continue
		}
		dir := filepath.Join(teamRolesDir(config.Name), role)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
		files := map[string]string{teamRoleFile: teamRoleDefault(role), teamLearnedFile: teamLearnedDefault(role)}
		for _, name := range sortedKeys(files) {
			if path := filepath.Join(dir, name); !fileExists(path) {
				if err := os.WriteFile(path, []byte(files[name]), 0o600); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// writeTeamTemplateFiles keeps a team template's roles, from its team/
// folder (roles/<role>/ROLE.md and LEARNED.md), before hi fills in the
// rest: the members' roles, and the others the template wrote itself
// rather than took from hi's team-app. Other files there are left out.
func writeTeamTemplateFiles(config teamConfig, files map[string][]byte) error {
	for _, name := range sortedKeys(files) {
		parts := strings.Split(name, "/")
		if len(parts) != 3 || parts[0] != "roles" || !teamRolePattern.MatchString(parts[1]) || (parts[2] != teamRoleFile && parts[2] != teamLearnedFile) {
			continue
		}
		if _, member := config.Members[parts[1]]; !member && (parts[2] != teamRoleFile || string(files[name]) == teamRoleDefault(parts[1])) {
			continue
		}
		dir := filepath.Join(teamRolesDir(config.Name), parts[1])
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
		if path := filepath.Join(dir, parts[2]); !fileExists(path) {
			if err := os.WriteFile(path, files[name], 0o600); err != nil {
				return err
			}
		}
	}
	return nil
}

// teamExtraRoles are the roles the team's folder has that no member
// fills, such as a template's researcher.
func teamExtraRoles(config teamConfig) []string {
	entries, _ := os.ReadDir(teamRolesDir(config.Name))
	var extra []string
	for _, entry := range entries {
		if _, member := config.Members[entry.Name()]; entry.IsDir() && !member {
			extra = append(extra, entry.Name())
		}
	}
	return extra
}

// teamRoleBrief is what a brief carries about the member's position: its
// instructions, what it has learned, and how to add to that.
func teamRoleBrief(team, role string) string {
	dir := filepath.Join(teamRolesDir(team), role)
	var text strings.Builder
	if data, err := os.ReadFile(filepath.Join(dir, teamRoleFile)); err == nil && strings.TrimSpace(string(data)) != "" {
		fmt.Fprintf(&text, "\n## Your role on this team (roles/%s/%s)\n\n%s\n", role, teamRoleFile, teamDemoteHeadings(strings.TrimSpace(string(data))))
	}
	if learned := teamLearnedEntries(filepath.Join(dir, teamLearnedFile)); learned != "" {
		if len(learned) > teamLearnedLimit {
			learned = learned[:teamLearnedLimit]
			if i := strings.LastIndex(learned, "\n"); i > 0 {
				learned = learned[:i]
			}
			learned += "\n\n(Cut here: the file is longer than a brief carries.)"
		}
		fmt.Fprintf(&text, "\n## What the %s has learned on this team (roles/%s/%s)\n\nFrom earlier tasks. Trust it as you would a colleague's note: check it when it matters.\n\n%s\n", role, role, teamLearnedFile, learned)
	}
	fmt.Fprintf(&text, "\nIf you learned something about this project that the next %s should know and that the code and the map don't make plain (a trap, a quirk, a convention), add it to your report's follow_ups as one line starting with %q.\n", role, teamLearnedPrefix)
	return text.String()
}

var teamHTMLComment = regexp.MustCompile(`(?s)<!--.*?-->`)

// teamLearnedEntries is LEARNED.md without its heading and comments, or
// "" when nothing has been learned yet.
func teamLearnedEntries(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	text := teamHTMLComment.ReplaceAllString(string(data), "")
	var lines []string
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "# ") {
			continue
		}
		lines = append(lines, line)
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// teamDemoteHeadings puts a file's headings below the brief's own.
func teamDemoteHeadings(text string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, "#") {
			lines[i] = "##" + line
		}
	}
	return strings.Join(lines, "\n")
}

// teamLearnedProposals are a report's follow-ups that propose a lesson.
func teamLearnedProposals(report *agentReport) []string {
	if report == nil {
		return nil
	}
	var block struct {
		FollowUps []string `json:"follow_ups"`
	}
	json.Unmarshal(report.Report, &block)
	var proposals []string
	for _, followUp := range block.FollowUps {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(followUp), teamLearnedPrefix); ok && strings.TrimSpace(rest) != "" {
			proposals = append(proposals, strings.TrimSpace(rest))
		}
	}
	return proposals
}

func teamLearnedDefault(role string) string {
	return fmt.Sprintf(`# What the %[1]s has learned on this team

<!-- What earlier tasks taught this role that the code and docs/map.md
don't make plain: a trap, a quirk, a convention. One line each, with the
task that taught it, such as "Tests need TZ=UTC: the fixtures build today
in local time. (t-7)". The lead keeps it: it adds what members propose in
their reports, merges and drops lines that no longer hold, and moves what
belongs to the project into the repository. Keep it under about 3 KB. -->
`, role)
}

// teamRoleDefault is a role's standing instructions when the team starts
// without a template's: the built-in team template's for the roles it
// has, and a general one for the rest.
func teamRoleDefault(role string) string {
	if data, err := builtinTemplateFiles.ReadFile("templates/" + teamAppTemplate + "/" + teamTemplateDir + "/roles/" + role + "/" + teamRoleFile); err == nil {
		return string(data)
	}
	return fmt.Sprintf(`# The %[1]s

You do the tasks the lead gives the %[1]s, one at a time, on your own
branch, for people who may not read code.

- Look before you build: the map (docs/map.md), the decisions
  (docs/decisions/), and the code itself, and extend what exists.
- Do the brief, all of it, and nothing beyond it; put what else you
  noticed in your report's follow-ups.
- Say plainly in your report what you didn't do or couldn't check.
`, role)
}
