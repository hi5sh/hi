package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// hi app rename gives a team another name: the one its Slack bot goes by,
// its /hi-<name> command, and what hi calls it on this machine. What hi made
// for it under the old name outside its folder (the service, the snapshot
// timer, the boxes) is made again under the new one. Its memory bank and its
// snapshots stay under the name it was made with (teamConfig.MadeAs), so
// what it remembers and every snapshot it took carry over.

// madeAs is the name a team was made with: what its memory bank and
// its snapshots are kept under.
func (config teamConfig) madeAs() string { return firstNonEmpty(config.MadeAs, config.Name) }

// teamFreeMadeAs is what a new team called name is made as: its name,
// unless a team renamed since keeps its memory bank and snapshots under
// it; then name-2, name-3, and on, so the two never share them.
func teamFreeMadeAs(name string) string {
	taken := map[string]bool{}
	entries, _ := os.ReadDir(teamsDir())
	for _, entry := range entries {
		if config, err := loadTeam(entry.Name()); err == nil && teamNamePattern.MatchString(entry.Name()) {
			taken[config.madeAs()] = true
		}
	}
	made := name
	for n := 2; taken[made]; n++ {
		made = fmt.Sprintf("%s-%d", name, n)
	}
	return made
}

func teamRename(args []string, stdin io.Reader, stdout io.Writer) error {
	var names []string
	var yes bool
	for _, arg := range args {
		switch {
		case arg == "--yes" || arg == "-y":
			yes = true
		case strings.HasPrefix(arg, "-"):
			return usageError{"unknown option " + arg}
		default:
			names = append(names, arg)
		}
	}
	if len(names) != 2 {
		return usageError{"usage: hi app rename <name> <new name> [--yes]"}
	}
	old, name := names[0], names[1]
	if old == name {
		return fmt.Errorf("app %s is already named %s", old, name)
	}
	if !teamNamePattern.MatchString(name) {
		return fmt.Errorf("an app's name starts with a letter and has lowercase letters, digits, and dashes, up to 20: %q", name)
	}
	config, err := loadTeam(old)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(filepath.Join(teamsDir(), name)); err == nil || fileExists(teamUnitPath(name)) {
		return fmt.Errorf("there is already an app named %s on this machine", name)
	}
	if fileExists(teamSnapshotsDir(name)) {
		return fmt.Errorf("%s is in the way: snapshots of an earlier app named %s", teamSnapshotsDir(name), name)
	}
	for _, task := range loadTeamTasks(old) {
		if task.Status == "starting" || task.Status == "running" {
			return fmt.Errorf("task %s is %s; rename the app when its members are done, or stop it first", task.ID, task.Status)
		}
	}
	folder := teamDir(old)
	moveTo := ""
	if base := filepath.Base(folder); base == "app-"+old || base == "team-"+old {
		if next := filepath.Join(filepath.Dir(folder), "app-"+name); !fileExists(next) {
			moveTo = next
		}
	}

	fmt.Fprintf(stdout, "Renaming app %s to %s:\n", old, name)
	renamed := config
	renamed.Name = name
	fmt.Fprintf(stdout, "  its Slack bot     @%s, with %s instead of %s\n", teamBotHandle(renamed), teamSlashCommand(name), teamSlashCommand(old))
	fmt.Fprintf(stdout, "  its service       %s\n", teamUnitName(name))
	if moveTo != "" {
		fmt.Fprintf(stdout, "  its folder        %s\n", moveTo)
	}
	if config.Domain {
		if domain := teamSlotDomain(old, teamSlotProduction); domain != "" {
			fmt.Fprintf(stdout, "  its app           https://%s\n", strings.Replace(domain, old, name, 1))
		}
	}
	fmt.Fprintln(stdout, "The app stops while it's done and starts again if it was up; a preview it was showing ends. Its memory, tasks, and snapshots stay.")
	if err := confirm(stdin, stdout, yes, "Rename app "+old+" to "+name+"?"); err != nil {
		return err
	}

	up := teamState(old) == "up"
	if out, err := teamSystemctl("disable", "--now", teamUnitName(old)); err != nil && fileExists(teamUnitPath(old)) {
		return fmt.Errorf("systemctl --user disable --now %s: %s", teamUnitName(old), qFirstLine(out, err.Error()))
	}
	if err := os.Remove(teamUnitPath(old)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	teamRemoveSnapshotTimer(old)
	teamSystemctl("daemon-reload")
	// The lead's box, its memory server, and the app's boxes are named
	// after the team; they are made again from the folder. The members'
	// boxes keep their names: the tasks know them by those.
	teamRemoveContainers(old, nil)

	if fileExists(teamSnapshotsDir(old)) {
		if err := os.Rename(teamSnapshotsDir(old), teamSnapshotsDir(name)); err != nil {
			return fmt.Errorf("moving the app's snapshots: %w", err)
		}
	}
	if moveTo != "" {
		if err := os.Rename(folder, moveTo); err != nil {
			return fmt.Errorf("moving the app's folder: %w", err)
		}
		folder = moveTo
	}
	config.MadeAs = config.madeAs()
	config.Name = name
	if config.MadeAs == name {
		config.MadeAs = ""
	}
	data, _ := json.MarshalIndent(config, "", "  ")
	if err := os.WriteFile(filepath.Join(folder, "team.json"), append(data, '\n'), 0o600); err != nil {
		return err
	}
	teamRenameSoul(filepath.Join(folder, "lead"), old, name)
	teamRenameNotes(filepath.Join(folder, "lead"), old, name)
	if fileExists(filepath.Join(folder, "slack-manifest.json")) {
		os.WriteFile(filepath.Join(folder, "slack-manifest.json"), []byte(teamManifest(config)+"\n"), 0o600)
	}
	teamRenameMemoryContext(filepath.Join(folder, "lead"), old)
	if err := os.Remove(filepath.Join(teamsDir(), old)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := teamRegister(name, folder); err != nil {
		return err
	}

	fmt.Fprintf(stdout, "App %s is now %s, in %s.\n", old, name, folder)
	if up {
		if err := teamUp(name, stdout); err != nil {
			return err
		}
	} else {
		fmt.Fprintf(stdout, "hi app up %s starts it.\n", name)
	}
	fmt.Fprintf(stdout, "\nIn Slack, give the Slack app the new name and %s: open the app at https://api.slack.com/apps, choose App Manifest, and paste what this prints:\n  hi app manifest %s\n", teamSlashCommand(name), name)
	return nil
}

// teamRenameMemoryContext lets the lead's Hindsight settings say the new
// name: the context hi wrote under the old one goes, and hi writes it again
// on the next start. Context the lead tuned itself stays.
func teamRenameMemoryContext(lead, old string) {
	path := filepath.Join(lead, "hindsight", "config.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	settings := map[string]any{}
	if json.Unmarshal(data, &settings) != nil || settings["retain_context"] != teamMemoryContext(old) {
		return
	}
	delete(settings, "retain_context")
	data, _ = json.MarshalIndent(settings, "", "  ")
	os.WriteFile(path, append(data, '\n'), 0o600)
}

// teamRenameSoul puts the new name in the lines of SOUL.md that hi wrote
// with the old one (teamSoul). The rest is the lead's, and stays.
func teamRenameSoul(lead, old, name string) {
	path := filepath.Join(lead, "SOUL.md")
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	soul := string(data)
	// SOUL.md written before v2.6.67 says team, later ones app.
	for _, word := range []string{"team", "app"} {
		soul = strings.Replace(soul, "# The "+old+" "+word+"'s lead\n", "# The "+name+" "+word+"'s lead\n", 1)
		soul = strings.Replace(soul, "You lead the "+old+" "+word+",", "You lead the "+name+" "+word+",", 1)
		soul = strings.Replace(soul, "You are the "+old+" "+word+"'s lead", "You are the "+name+" "+word+"'s lead", 1)
		// A lead with a name of its own: "# Maja, the payments app's lead".
		soul = strings.Replace(soul, ", the "+old+" "+word+"'s lead\n", ", the "+name+" "+word+"'s lead\n", 1)
		soul = strings.Replace(soul, ", the "+old+" "+word+"'s lead:", ", the "+name+" "+word+"'s lead:", 1)
	}
	if soul != string(data) {
		os.WriteFile(path, []byte(soul), 0o600)
	}
}

// teamRenameNotes puts the new name in the description hi wrote for the
// lead's notes skill (teamNotes); the notes are the lead's, and stay.
func teamRenameNotes(lead, old, name string) {
	path := filepath.Join(lead, "skills", teamNotesSkill, "SKILL.md")
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	notes := string(data)
	for _, word := range []string{"team", "app"} {
		notes = strings.Replace(notes, "description: What you learned running the "+old+" "+word+",", "description: What you learned running the "+name+" "+word+",", 1)
	}
	if notes != string(data) {
		os.WriteFile(path, []byte(notes), 0o600)
	}
}
