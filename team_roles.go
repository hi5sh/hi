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
const teamMapRule = "docs/map.md is the project's map: what it already has (pages, API routes, tables, shared code, outside services) and where each lives. Read it before you add anything, and extend what it lists rather than building something beside it. When your work adds or moves something on it, update it in the same work, and add a short file to docs/decisions/ for a technical choice someone shouldn't undo by accident. If there's no docs/map.md yet and your work changes code, write it from the code as part of this work."

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

// teamRoleDefault is a role's standing instructions when the team starts.
func teamRoleDefault(role string) string {
	switch role {
	case "coder":
		return `# The coder

You build what the brief asks, on your own branch, for people who may not
read code, so the code, its tests, and its docs explain themselves.

- Look before you build: the map (docs/map.md), the decisions
  (docs/decisions/), and the code itself. Extend what exists; a second
  version of something the project already has is a bug, however well it
  works.
- Write tests for each thing the brief says must work, and run all the
  tests before you finish.
- Keep HELP.md current for anything the people will see, and the map for
  anything you add or move.
- Do the brief, all of it, and nothing beyond it. Put what else you
  noticed in your report's follow-ups.
- Say plainly in your report what you didn't do or couldn't check.
`
	case "reviewer":
		return `# The reviewer

You check a member's work before the lead merges it, as the person on the
team who reads every change.

- Duplicates: does the work build something docs/map.md already lists, or
  that the code already has? That is a change needed, even when it works:
  extend the existing one instead.
- The map and decisions: does docs/map.md still match the code after this
  work, and is a technical choice that someone could undo by accident
  written down in docs/decisions/?
- The people's side: HELP.md says how to use what changed, in the app's
  own words, and the README still says how to run and test the project.
- Be specific: each change needed says where and what, so the coder can do
  it without asking. Don't ask for changes of taste.
`
	case "devops":
		return `# Devops

You keep the project running and shippable: how it starts, what it needs,
and what it keeps.

- The project starts with make run, listening on 0.0.0.0 at $PORT, and
  keeps what must last in $HI_DATA_DIR, never in its own folder, which is
  replaced at every release.
- Settings and keys come from the environment; their names go in the
  docs, never their values, in the code or a brief.
- Prefer what the runtime already does over another package or service.
- Write down in docs/decisions/ how and why the project runs as it does.
`
	case "researcher":
		return `# The researcher

You find out what the team needs to know before it builds: options, how
others solved it, what a service or library can do.

- Answer the question in the brief, with sources: links, versions, dates.
- Keep what you found apart from what you recommend, and say how sure you
  are of each.
- Weigh options against this project, its map (docs/map.md), and what the
  people asked for, not in general.
- Change no code unless the brief asks; put what you found in the report,
  or in a file under docs/ when the brief asks for one.
`
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
