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

// The app's data across its slots (docs/specs/approved/hi_team_data.md).
// Each slot has a data folder of its own; production owns the truth and
// runs the work that costs money, and a preview works on a copy of it. A
// copy goes the other way once, with an owner's yes: when the preview
// gathered what production needs before production existed.

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
	Kept    string `json:"kept,omitempty"`
	Warning string `json:"warning,omitempty"`
}

// teamDataUsage is a folder's size, its files, and its newest change.
func teamDataUsage(dir string) (bytes int64, files int, changed time.Time) {
	filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
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
		if folder.Files == 0 {
			fmt.Fprintf(w, "%-11s %s: empty; %s\n", folder.Slot, folder.Dir, state)
			continue
		}
		fmt.Fprintf(w, "%-11s %s: %s in %d file(s), last changed %s; %s\n", folder.Slot, folder.Dir,
			teamDataSize(folder.Bytes), folder.Files, folder.Changed.Local().Format("Mon Jan 2 15:04"), state)
	}
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
		result, err := b.copyDataLocked(request.From, request.To)
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
	if result.Files == 0 {
		return result, fmt.Errorf("%s has no data to copy (%s is empty)", teamSlotName(from), src)
	}
	parent := filepath.Dir(dst)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return result, err
	}
	if free := teamFreeBytes(parent); free >= 0 && free < result.Bytes+teamDataRoom {
		return result, fmt.Errorf("the disk has %s free, and the copy needs %s and some room to spare", teamDataSize(free), teamDataSize(result.Bytes))
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
	os.RemoveAll(fresh)
	out, err := exec.Command("cp", "-a", src, fresh).CombinedOutput()
	unpause()
	if err != nil {
		os.RemoveAll(fresh)
		return result, fmt.Errorf("copying %s: %s", src, qFirstLine(strings.TrimSpace(string(out)), err.Error()))
	}
	old := fresh + "-old"
	if to == teamSlotPreview {
		// The preview's data is in no snapshot: keep the last one.
		old = filepath.Join(parent, filepath.Base(dst)+"-before")
		result.Kept = old
	}
	os.RemoveAll(old)
	if err := os.Rename(dst, old); err != nil && !errors.Is(err, os.ErrNotExist) {
		os.RemoveAll(fresh)
		return result, err
	}
	if err := os.Rename(fresh, dst); err != nil {
		os.Rename(old, dst)
		return result, err
	}
	if to == teamSlotProduction {
		os.RemoveAll(old)
	} else if _, files, _ := teamDataUsage(old); files == 0 {
		os.RemoveAll(old)
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

// teamDataCommand is hi team data <name> [copy <from> <to> [--yes]].
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
	usage := usageError{"usage: hi team data <name> [copy preview production | copy production preview] [--yes]"}
	if len(words) != 1 && (len(words) != 4 || words[1] != "copy") {
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
	if len(words) == 1 {
		printTeamDataFolders(stdout, teamDataFolders(team, engineRunning))
		return nil
	}
	from, to := words[2], words[3]
	if err := teamCheckDataSlots(from, to); err != nil {
		return err
	}
	bytes, files, _ := teamDataUsage(teamSlotDataDir(team, from))
	if files == 0 {
		return fmt.Errorf("%s has no data to copy", teamSlotName(from))
	}
	if to == teamSlotProduction {
		fmt.Fprintf(stdout, "This replaces team %s's production data with a copy of the preview's (%s). hi snapshots production's data first, and posts it in the channel.\n", team, teamDataSize(bytes))
		fmt.Fprintln(stdout, "The preview's box pauses while it is copied, and production restarts on the copy.")
	} else {
		fmt.Fprintf(stdout, "This replaces team %s's preview data with a copy of production's (%s); the preview's data from before is kept in run/preview-data-before.\n", team, teamDataSize(bytes))
		fmt.Fprintln(stdout, "Production's box pauses while it is copied, and the preview restarts on the copy.")
	}
	if err := confirm(stdin, stdout, yes, fmt.Sprintf("Copy %s's data to %s?", from, to)); err != nil {
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

func printTeamDataCopy(w io.Writer, result teamDataCopy) {
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
