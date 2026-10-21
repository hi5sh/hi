package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// The app's data across its slots (docs/specs/approved/hi_team_data.md,
// docs/specs/approved/hi_team_data_fork.md). Each slot has a data folder
// of its own; production owns the data, and a preview runs on a fork of
// it (team_data_fork.go). A copy goes the other way once, with an owner's
// yes: when the preview gathered what production needs before production
// existed.

// teamDataRoom is what a copy leaves free on the disk, beyond itself.
const teamDataRoom = 256 << 20

// teamDataFolder is one slot's data folder, as hi team data lists it.
type teamDataFolder struct {
	Slot    string    `json:"slot"`
	Dir     string    `json:"dir"`
	Bytes   int64     `json:"bytes"`
	Files   int       `json:"files"`
	Changed time.Time `json:"changed,omitzero"`
	// Running says the slot's app box runs.
	Running bool `json:"running"`
	// For the preview: the fork it runs on, below what it wrote itself
	// (Bytes, Files), and whether it starts empty at every release.
	Fork  *teamPreviewFork `json:"fork,omitempty"`
	Empty bool             `json:"empty,omitempty"`
}

// teamDataCopyRequest asks for one slot's data folder to become a copy of
// the other's.
type teamDataCopyRequest struct {
	From     string `json:"from"`
	To       string `json:"to"`
	Approval string `json:"approval,omitempty"`
	// Who is the person on the machine, through the host's socket.
	Who string `json:"who,omitempty"`
}

// teamDataCopy is what a copy did.
type teamDataCopy struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Bytes int64  `json:"bytes"`
	Files int    `json:"files"`
	// Snapshot holds production's data from before, for a copy to it.
	Snapshot string `json:"snapshot,omitempty"`
	// Kept is where the preview's data from before is, for a copy to it.
	Kept string `json:"kept,omitempty"`
	// Fork is the preview's new fork, for a fork.
	Fork    *teamPreviewFork `json:"fork,omitempty"`
	Warning string           `json:"warning,omitempty"`
}

// teamDataUsage is a folder's size, its files, and its newest change.
func teamDataUsage(dir string) (bytes int64, files int, changed time.Time) {
	filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || !entry.Type().IsRegular() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return nil
		}
		bytes += info.Size()
		files++
		if info.ModTime().After(changed) {
			changed = info.ModTime()
		}
		return nil
	})
	return bytes, files, changed
}

// teamDataFolders lists the team's data folders, production first.
func teamDataFolders(team string, running func(string) bool) []teamDataFolder {
	var folders []teamDataFolder
	for _, slot := range []string{teamSlotProduction, teamSlotPreview} {
		folder := teamDataFolder{Slot: slot, Dir: teamSlotDataDir(team, slot)}
		folder.Bytes, folder.Files, folder.Changed = teamDataUsage(folder.Dir)
		if state, ok := loadTeamSlot(team, slot); ok && running != nil {
			folder.Running = running(state.Container)
		}
		if slot == teamSlotPreview {
			if fork, ok := loadTeamPreviewFork(team); ok {
				folder.Fork = &fork
			}
			if config, err := loadTeam(team); err == nil {
				folder.Empty = config.PreviewData == teamPreviewEmpty
			}
		}
		folders = append(folders, folder)
	}
	return folders
}

func teamSlotName(slot string) string {
	if slot == teamSlotProduction {
		return "production"
	}
	return "the preview"
}

func printTeamDataFolders(w io.Writer, folders []teamDataFolder) {
	for _, folder := range folders {
		state := "its box is stopped"
		if folder.Running {
			state = "its box runs"
		}
		if folder.Empty {
			state += "; it starts empty at every release"
		}
		if fork := folder.Fork; fork != nil && !fork.Full {
			fmt.Fprintf(w, "%-11s %s: a fork of production%s from %s (%s in %d file(s)), and %s of its own in %d file(s); %s\n", folder.Slot, folder.Dir,
				teamForkVersion(*fork), fork.At.Local().Format("Mon Jan 2 15:04"), teamDataSize(fork.Bytes), fork.Files, teamDataSize(folder.Bytes), folder.Files, state)
			continue
		}
		if fork := folder.Fork; fork != nil && folder.Files > 0 {
			state += fmt.Sprintf("; a whole copy of production%s from %s, not a fork: %s", teamForkVersion(*fork), fork.At.Local().Format("Mon Jan 2 15:04"), fork.Why)
		}
		if folder.Files == 0 {
			fmt.Fprintf(w, "%-11s %s: empty; %s\n", folder.Slot, folder.Dir, state)
			continue
		}
		fmt.Fprintf(w, "%-11s %s: %s in %d file(s), last changed %s; %s\n", folder.Slot, folder.Dir,
			teamDataSize(folder.Bytes), folder.Files, folder.Changed.Local().Format("Mon Jan 2 15:04"), state)
	}
}

func teamForkVersion(fork teamPreviewFork) string {
	if fork.Version == 0 {
		return ""
	}
	return fmt.Sprintf(" v%d", fork.Version)
}

// teamDataSize is a size as people say it: 1.6 GB, 340 MB, 12 KB.
func teamDataSize(bytes int64) string {
	switch {
	case bytes >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(bytes)/(1<<30))
	case bytes >= 1<<20:
		return fmt.Sprintf("%d MB", bytes>>20)
	case bytes >= 1<<10:
		return fmt.Sprintf("%d KB", bytes>>10)
	}
	return fmt.Sprintf("%d bytes", bytes)
}

func teamCheckDataSlots(from, to string) error {
	valid := func(slot string) bool { return slot == teamSlotPreview || slot == teamSlotProduction }
	if !valid(from) || !valid(to) || from == to {
		return usageError{"copy from one slot to the other: preview production, or production preview"}
	}
	return nil
}

// teamFreeBytes is how much the disk that holds dir has free; -1 if it
// can't tell.
var teamFreeBytes = func(dir string) int64 {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(dir, &stat); err != nil {
		return -1
	}
	return int64(stat.Bavail) * int64(stat.Bsize)
}

// copyData makes one slot's data folder a copy of the other's: production
// from the preview with an owner's yes (or someone on the machine), the
// preview from production with none. hi posts a copy to production in the
// channel.
func (b *teamBroker) copyData(request teamDataCopyRequest) (teamDataCopy, error) {
	if err := teamCheckDataSlots(request.From, request.To); err != nil {
		return teamDataCopy{}, err
	}
	config, err := loadTeam(b.team)
	if err != nil {
		return teamDataCopy{}, err
	}
	if request.To == teamSlotPreview {
		b.mu.Lock()
		result, err := b.forkPreviewLocked()
		b.mu.Unlock()
		return result, err
	}
	var approval teamApproval
	if request.Who != "" {
		approval = teamApproval{Name: request.Who + " on the machine"}
	} else if approval, err = b.teamFindApproval(config, request.Approval, "data"); err != nil {
		return teamDataCopy{}, err
	}
	b.mu.Lock()
	versions, err := loadTeamVersions(b.team)
	if err == nil && versions.Production == 0 {
		err = errors.New("nothing is released to production yet; release with --data preview to start production with the preview's data")
	}
	if err != nil {
		b.mu.Unlock()
		return teamDataCopy{}, err
	}
	snapshot, snapshotErr := b.snapshotLocked(teamSnapshotManual, "before data from the preview")
	_, had, _ := teamDataUsage(teamSlotDataDir(b.team, teamSlotProduction))
	if snapshotErr != nil && had > 0 {
		b.mu.Unlock()
		return teamDataCopy{}, fmt.Errorf("nothing was copied: production's data couldn't be snapshotted first, so it would be lost: %w", snapshotErr)
	}
	result, err := b.copyDataLocked(request.From, request.To)
	if err != nil {
		b.mu.Unlock()
		return result, err
	}
	result.Snapshot = snapshot.ID
	versions.History = append(versions.History, teamVersionEvent{What: "production's data from the preview", Approval: &approval, At: time.Now().UTC(), Snapshot: snapshot.ID})
	saveErr := saveTeamVersions(b.team, versions)
	b.mu.Unlock()
	if saveErr != nil {
		result.Warning = "the copy isn't in versions.json: " + saveErr.Error()
	}
	// Production's address shows it again before the channel hears.
	if _, err := b.production(0); err != nil {
		result.Warning = strings.TrimSpace(result.Warning + "\nProduction didn't start again yet (hi tries again within a minute): " + err.Error())
	}
	b.announce(config, teamDataCopyAnnouncement(result, approval))
	return result, nil
}

func teamDataCopyAnnouncement(result teamDataCopy, approval teamApproval) string {
	text := fmt.Sprintf(":floppy_disk: *Production now has a copy of the preview's data* (%s), approved by %s.", teamDataSize(result.Bytes), approval.Name)
	if result.Snapshot != "" {
		text += fmt.Sprintf(" Production's data from before is kept in snapshot %s.", result.Snapshot)
	}
	return text
}

// copyDataLocked copies with b.mu held, and starts the preview again; the
// caller starts production. The source's box is paused while it is copied,
// so the copy is consistent (a paused SQLite database copies like one after
// a power cut), and the target's box is removed first.
func (b *teamBroker) copyDataLocked(from, to string) (teamDataCopy, error) {
	result := teamDataCopy{From: from, To: to}
	src, dst := teamSlotDataDir(b.team, from), teamSlotDataDir(b.team, to)
	result.Bytes, result.Files, _ = teamDataUsage(src)
	// A preview on a fork is its fork with its own writes over it; the
	// fork's files are linked, so only its own take room.
	overlay := from == teamSlotPreview && teamPreviewOverlay(b.team)
	room := result.Bytes
	if overlay {
		base, files, _ := teamDataUsage(teamPreviewBaseDir(b.team))
		result.Bytes, result.Files = result.Bytes+base, result.Files+files
	}
	if result.Files == 0 {
		return result, fmt.Errorf("%s has no data to copy (%s is empty)", teamSlotName(from), src)
	}
	parent := filepath.Dir(dst)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return result, err
	}
	if free := teamFreeBytes(parent); free >= 0 && free < room+teamDataRoom {
		return result, fmt.Errorf("the disk has %s free, and the copy needs %s and some room to spare", teamDataSize(free), teamDataSize(room))
	}

	b.previewMu.Lock()
	defer b.previewMu.Unlock()
	unpause := func() {}
	if state, ok := loadTeamSlot(b.team, from); ok && b.running(state.Container) {
		if _, err := b.engineOutput("pause", state.Container); err == nil {
			unpause = func() { b.engineOutput("unpause", state.Container) }
		}
	}
	target, hadTarget := loadTeamSlot(b.team, to)
	if hadTarget {
		b.removeApp(target.Container)
	}
	// Beside the target, under a name snapshots skip.
	fresh := filepath.Join(parent, ".restore-copy-"+filepath.Base(dst))
	teamRemoveAll(fresh)
	var err error
	if overlay {
		err = teamMergeOverlay(teamPreviewBaseDir(b.team), src, fresh)
	} else if out, copyErr := exec.Command("cp", "-a", src, fresh).CombinedOutput(); copyErr != nil {
		err = fmt.Errorf("copying %s: %s", src, qFirstLine(strings.TrimSpace(string(out)), copyErr.Error()))
	}
	unpause()
	if err != nil {
		teamRemoveAll(fresh)
		return result, err
	}
	old := fresh + "-old"
	if to == teamSlotPreview {
		// The preview's data is in no snapshot: keep the last one.
		old = filepath.Join(parent, filepath.Base(dst)+"-before")
		result.Kept = old
	}
	teamRemoveAll(old)
	if err := os.Rename(dst, old); err != nil && !errors.Is(err, os.ErrNotExist) {
		teamRemoveAll(fresh)
		return result, err
	}
	if err := os.Rename(fresh, dst); err != nil {
		os.Rename(old, dst)
		return result, err
	}
	if to == teamSlotProduction {
		teamRemoveAll(old)
	} else if _, files, _ := teamDataUsage(old); files == 0 {
		teamRemoveAll(old)
		result.Kept = ""
	}
	if to == teamSlotPreview && hadTarget {
		next := teamPreview{Slot: teamSlotPreview, Source: target.Source, Label: target.Label, Port: target.Port}
		if _, err := b.show(next, target.Commit, teamLinkRenew); err != nil {
			result.Warning = "The preview didn't start again: " + err.Error()
		}
	}
	return result, nil
}

// ---------------------------------------------------------------------------
// the command, on the host

// teamDataCommand is hi team data <name> [fork | copy <from> <to> |
// preview empty|fork] [--yes].
func teamDataCommand(args []string, stdin io.Reader, stdout io.Writer) error {
	yes := false
	var words []string
	for _, arg := range args {
		switch {
		case arg == "--yes" || arg == "-y":
			yes = true
		case strings.HasPrefix(arg, "-"):
			return usageError{"unknown option " + arg}
		default:
			words = append(words, arg)
		}
	}
	usage := usageError{"usage: hi team data <name> [fork | copy preview production | preview empty | preview fork] [--yes]"}
	if len(words) == 0 {
		return usage
	}
	team := words[0]
	if _, err := loadTeam(team); err != nil {
		return err
	}
	engineRunning := func(container string) bool {
		engine, _, err := detectBoxEngine()
		if err != nil {
			return false
		}
		out, err := engine.output("inspect", "-f", "{{.State.Running}}", container)
		return err == nil && strings.TrimSpace(out) == "true"
	}
	var from, to string
	switch {
	case len(words) == 1:
		printTeamDataFolders(stdout, teamDataFolders(team, engineRunning))
		return nil
	case len(words) == 3 && words[1] == "preview":
		return teamPreviewDataMode(team, words[2], stdout)
	case len(words) == 2 && words[1] == "fork":
		from, to = teamSlotProduction, teamSlotPreview
	case len(words) == 4 && words[1] == "copy":
		from, to = words[2], words[3]
	default:
		return usage
	}
	if err := teamCheckDataSlots(from, to); err != nil {
		return err
	}
	bytes, files, _ := teamDataUsage(teamSlotDataDir(team, from))
	if from == teamSlotPreview && teamPreviewOverlay(team) {
		base, baseFiles, _ := teamDataUsage(teamPreviewBaseDir(team))
		bytes, files = bytes+base, files+baseFiles
	}
	if files == 0 {
		return fmt.Errorf("%s has no data to copy", teamSlotName(from))
	}
	if to == teamSlotProduction {
		fmt.Fprintf(stdout, "This replaces team %s's production data with a copy of the preview's (%s). hi snapshots production's data first, and posts it in the channel.\n", team, teamDataSize(bytes))
		fmt.Fprintln(stdout, "The preview's box pauses while it is copied, and production restarts on the copy.")
	} else {
		fmt.Fprintf(stdout, "This gives team %s's preview a new fork of production's data (%s); what the preview wrote itself is kept in run/preview-data-before.\n", team, teamDataSize(bytes))
		fmt.Fprintln(stdout, "Production's box pauses for a few seconds while it is forked, and the preview restarts on the fork.")
	}
	question := fmt.Sprintf("Copy %s's data to %s?", from, to)
	if to == teamSlotPreview {
		question = "Fork production's data for the preview?"
	}
	if err := confirm(stdin, stdout, yes, question); err != nil {
		return err
	}
	var result teamDataCopy
	asked, err := teamHostCall(team, "/data/copy", teamDataCopyRequest{From: from, To: to, Who: currentUserName()}, &result)
	if !asked {
		return fmt.Errorf("team %s isn't up, or runs a hi from before hi team data; hi team up %s, then try again", team, team)
	}
	if err != nil {
		return err
	}
	printTeamDataCopy(stdout, result)
	return nil
}

// teamPreviewDataMode is hi team data <name> preview empty|fork: whether
// a preview starts on a fork of production's data at every release, or
// empty, for an app whose people's data previews shouldn't show.
func teamPreviewDataMode(team, mode string, stdout io.Writer) error {
	config, err := loadTeam(team)
	if err != nil {
		return err
	}
	switch mode {
	case "empty":
		config.PreviewData = teamPreviewEmpty
	case "fork":
		config.PreviewData = ""
	default:
		return usageError{"usage: hi team data <name> preview empty | preview fork"}
	}
	if err := saveTeam(config); err != nil {
		return err
	}
	if mode == "empty" {
		fmt.Fprintf(stdout, "Team %s's preview starts on an empty data folder at every release, never on production's data. It keeps what it has until then.\n", team)
	} else {
		fmt.Fprintf(stdout, "Team %s's preview starts on a fork of production's data at every release. hi team data %s fork gives it one now.\n", team, team)
	}
	return nil
}

func printTeamDataCopy(w io.Writer, result teamDataCopy) {
	if fork := result.Fork; fork != nil && !fork.Full {
		fmt.Fprintf(w, "The preview runs on a new fork of production's data (%s in %d file(s)); %d file(s), %s, were copied in full (the databases and the files being written), the rest linked.",
			teamDataSize(fork.Bytes), fork.Files, fork.Copied, teamDataSize(fork.CopiedBytes))
		if result.Kept != "" {
			fmt.Fprintf(w, " What it wrote itself before is in %s.", result.Kept)
		}
		fmt.Fprintln(w)
		if result.Warning != "" {
			fmt.Fprintln(w, result.Warning)
		}
		return
	}
	if fork := result.Fork; fork != nil {
		fmt.Fprintf(w, "The preview got a whole copy, not a fork: %s.\n", fork.Why)
	}
	fmt.Fprintf(w, "%s has a copy of %s's data now (%s in %d file(s)).", teamCapitalize(teamSlotName(result.To)), result.From, teamDataSize(result.Bytes), result.Files)
	if result.Snapshot != "" {
		fmt.Fprintf(w, " Its data from before is in snapshot %s.", result.Snapshot)
	}
	if result.Kept != "" {
		fmt.Fprintf(w, " Its data from before is in %s.", result.Kept)
	}
	fmt.Fprintln(w)
	if result.Warning != "" {
		fmt.Fprintln(w, result.Warning)
	}
}
