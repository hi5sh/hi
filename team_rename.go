package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// hi team rename gives a team another name: the one its Slack bot goes by,
// its /hi-<name> command, and what hi calls it on this machine. What hi made
// for it under the old name outside its folder (the service, the snapshot
// timer, the boxes) is made again under the new one. Its memory bank and its
// snapshots stay under the name it was made with (teamConfig.MadeAs), so
// what it remembers and every snapshot it took carry over.

// madeAs is the name a team was made with: what its memory bank and
// its snapshots are kept under.
func (config teamConfig) madeAs() string { return firstNonEmpty(config.MadeAs, config.Name) }

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
		return usageError{"usage: hi team rename <name> <new name> [--yes]"}
	}
	old, name := names[0], names[1]
	if old == name {
		return fmt.Errorf("team %s is already named %s", old, name)
	}
	if !teamNamePattern.MatchString(name) {
		return fmt.Errorf("a team's name starts with a letter and has lowercase letters, digits, and dashes, up to 20: %q", name)
	}
	config, err := loadTeam(old)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(filepath.Join(teamsDir(), name)); err == nil || fileExists(teamUnitPath(name)) {
		return fmt.Errorf("there is already a team named %s on this machine", name)
	}
	if fileExists(teamSnapshotsDir(name)) {
		return fmt.Errorf("%s is in the way: snapshots of an earlier team named %s", teamSnapshotsDir(name), name)
	}
	for _, task := range loadTeamTasks(old) {
		if task.Status == "starting" || task.Status == "running" {
			return fmt.Errorf("task %s is %s; rename the team when its members are done, or stop it first", task.ID, task.Status)
		}
	}
	folder := teamDir(old)
	moveTo := ""
	if filepath.Base(folder) == "team-"+old {
		if next := filepath.Join(filepath.Dir(folder), "team-"+name); !fileExists(next) {
			moveTo = next
		}
	}

	fmt.Fprintf(stdout, "Renaming team %s to %s:\n", old, name)
	fmt.Fprintf(stdout, "  its Slack bot     @%s, with %s instead of %s\n", teamBotName(name), teamSlashCommand(name), teamSlashCommand(old))
	fmt.Fprintf(stdout, "  its service       %s\n", teamUnitName(name))
	if moveTo != "" {
		fmt.Fprintf(stdout, "  its folder        %s\n", moveTo)
	}
	if config.Domain {
		if domain := teamSlotDomain(old, teamSlotProduction); domain != "" {
			fmt.Fprintf(stdout, "  its app           https://%s\n", strings.Replace(domain, old, name, 1))
		}
	}
	fmt.Fprintln(stdout, "The team stops while it's done and starts again if it was up; a preview it was showing ends. Its memory, tasks, and snapshots stay.")
	if err := confirm(stdin, stdout, yes, "Rename team "+old+" to "+name+"?"); err != nil {
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
			return fmt.Errorf("moving the team's snapshots: %w", err)
		}
	}
	if moveTo != "" {
		if err := os.Rename(folder, moveTo); err != nil {
			return fmt.Errorf("moving the team's folder: %w", err)
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
	teamRenameMemoryContext(filepath.Join(folder, "lead"), old)
	if err := os.Remove(filepath.Join(teamsDir(), old)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := teamRegister(name, folder); err != nil {
		return err
	}

	fmt.Fprintf(stdout, "Team %s is now %s, in %s.\n", old, name, folder)
	if up {
		if err := teamUp(name, stdout); err != nil {
			return err
		}
	} else {
		fmt.Fprintf(stdout, "hi team up %s starts it.\n", name)
	}
	fmt.Fprintf(stdout, "\nIn Slack, give the team's app the new name and %s: open the app at https://api.slack.com/apps, choose App Manifest, and paste what this prints:\n  hi team manifest %s\n", teamSlashCommand(name), name)
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
	soul := strings.Replace(string(data), "# The "+old+" team's lead\n", "# The "+name+" team's lead\n", 1)
	soul = strings.Replace(soul, "You lead the "+old+" team,", "You lead the "+name+" team,", 1)
	if soul != string(data) {
		os.WriteFile(path, []byte(soul), 0o600)
	}
}
