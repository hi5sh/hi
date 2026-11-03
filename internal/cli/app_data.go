package cli

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

// The app's data across its slots (docs/specs/approved/hi_app_data.md,
// docs/specs/approved/hi_app_data_fork.md). Each slot has a data folder
// of its own; production owns the data, and a preview runs on a fork of
// it (app_data_fork.go). A copy goes the other way once, with an owner's
// yes: when the preview gathered what production needs before production
// existed.

// appDataRoom is what a copy leaves free on the disk, beyond itself.
const appDataRoom = 256 << 20

// appDataFolder is one slot's data folder, as hi app data lists it.
type appDataFolder struct {
	Slot    string    `json:"slot"`
	Dir     string    `json:"dir"`
	Bytes   int64     `json:"bytes"`
	Files   int       `json:"files"`
	Changed time.Time `json:"changed,omitzero"`
	// Running says the slot's app box runs.
	Running bool `json:"running"`
	// For the preview: the fork it runs on, below what it wrote itself
	// (Bytes, Files), and whether it starts empty at every release.
	Fork  *appPreviewFork `json:"fork,omitempty"`
	Empty bool            `json:"empty,omitempty"`
}

// appDataCopyRequest asks for one slot's data folder to become a copy of
// the other's.
type appDataCopyRequest struct {
	From string `json:"from"`
	To   string `json:"to"`
	// Who is the person on the machine, through the host's socket.
	Who string `json:"who,omitempty"`
	// owner is an owner on the dev page, whose click is the yes; no
	// socket sets it.
	owner string
}

// appDataCopy is what a copy did.
type appDataCopy struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Bytes int64  `json:"bytes"`
	Files int    `json:"files"`
	// Snapshot holds production's data from before, for a copy to it.
	Snapshot string `json:"snapshot,omitempty"`
	// Kept is where the preview's data from before is, for a copy to it.
	Kept string `json:"kept,omitempty"`
	// Fork is the preview's new fork, for a fork.
	Fork    *appPreviewFork `json:"fork,omitempty"`
	Warning string          `json:"warning,omitempty"`
}

// appDataUsage is a folder's size, its files, and its newest change.
func appDataUsage(dir string) (bytes int64, files int, changed time.Time) {
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

// appDataFolders lists the team's data folders, production first.
func appDataFolders(team string, running func(string) bool) []appDataFolder {
	var folders []appDataFolder
	for _, slot := range []string{appSlotProduction, appSlotPreview} {
		folder := appDataFolder{Slot: slot, Dir: appSlotDataDir(team, slot)}
		folder.Bytes, folder.Files, folder.Changed = appDataUsage(folder.Dir)
		if state, ok := loadTeamSlot(team, slot); ok && running != nil {
			folder.Running = running(state.Container)
		}
		if slot == appSlotPreview {
			if fork, ok := loadTeamPreviewFork(team); ok {
				folder.Fork = &fork
			}
			if config, err := loadTeam(team); err == nil {
				folder.Empty = config.PreviewData == appPreviewEmpty
			}
		}
		folders = append(folders, folder)
	}
	return folders
}

func appSlotName(slot string) string {
	if slot == appSlotProduction {
		return "production"
	}
	return "the preview"
}

func printTeamDataFolders(w io.Writer, folders []appDataFolder) {
	for _, folder := range folders {
		// The dev box gets no paths: the machine's mean nothing there.
		label := folder.Slot
		if folder.Dir != "" {
			label = fmt.Sprintf("%-11s %s", folder.Slot, folder.Dir)
		}
		state := "its box is stopped"
		if folder.Running {
			state = "its box runs"
		}
		if folder.Empty {
			state += "; it starts empty at every release"
		}
		if fork := folder.Fork; fork != nil && !fork.Full {
			fmt.Fprintf(w, "%s: a fork of production%s from %s (%s in %d file(s)), and %s of its own in %d file(s); %s\n", label,
				appForkVersion(*fork), fork.At.Local().Format("Mon Jan 2 15:04"), appDataSize(fork.Bytes), fork.Files, appDataSize(folder.Bytes), folder.Files, state)
			continue
		}
		if fork := folder.Fork; fork != nil && folder.Files > 0 {
			state += fmt.Sprintf("; a whole copy of production%s from %s, not a fork: %s", appForkVersion(*fork), fork.At.Local().Format("Mon Jan 2 15:04"), fork.Why)
		}
		if folder.Files == 0 {
			fmt.Fprintf(w, "%s: empty; %s\n", label, state)
			continue
		}
		fmt.Fprintf(w, "%s: %s in %d file(s), last changed %s; %s\n", label,
			appDataSize(folder.Bytes), folder.Files, folder.Changed.Local().Format("Mon Jan 2 15:04"), state)
	}
}

func appForkVersion(fork appPreviewFork) string {
	if fork.Version == 0 {
		return ""
	}
	return fmt.Sprintf(" v%d", fork.Version)
}

// appDataSize is a size as people say it: 1.6 GB, 340 MB, 12 KB.
func appDataSize(bytes int64) string {
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

func appCheckDataSlots(from, to string) error {
	valid := func(slot string) bool { return slot == appSlotPreview || slot == appSlotProduction }
	if !valid(from) || !valid(to) || from == to {
		return usageError{"copy from one slot to the other: preview production, or production preview"}
	}
	return nil
}

// appFreeBytes is how much the disk that holds dir has free; -1 if it
// can't tell.
var appFreeBytes = func(dir string) int64 {
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
func (b *appBroker) copyData(request appDataCopyRequest) (appDataCopy, error) {
	if err := appCheckDataSlots(request.From, request.To); err != nil {
		return appDataCopy{}, err
	}
	config, err := loadTeam(b.team)
	if err != nil {
		return appDataCopy{}, err
	}
	if request.To == appSlotPreview {
		b.mu.Lock()
		result, err := b.forkPreviewLocked()
		b.mu.Unlock()
		return result, err
	}
	var approval appApproval
	if request.owner != "" {
		approval = appApproval{Name: request.owner}
	} else if request.Who != "" {
		approval = appApproval{Name: request.Who + " on the machine"}
	} else {
		return appDataCopy{}, errors.New("production's data is the owners': they copy the preview's on the dev page")
	}
	b.mu.Lock()
	versions, err := loadTeamVersions(b.team)
	if err == nil && versions.Production == 0 {
		err = errors.New("nothing is released to production yet; release with --data preview to start production with the preview's data")
	}
	if err != nil {
		b.mu.Unlock()
		return appDataCopy{}, err
	}
	snapshot, snapshotErr := b.snapshotLocked(appSnapshotManual, "before data from the preview")
	_, had, _ := appDataUsage(appSlotDataDir(b.team, appSlotProduction))
	if snapshotErr != nil && had > 0 {
		b.mu.Unlock()
		return appDataCopy{}, fmt.Errorf("nothing was copied: production's data couldn't be snapshotted first, so it would be lost: %w", snapshotErr)
	}
	result, err := b.copyDataLocked(request.From, request.To)
	if err != nil {
		b.mu.Unlock()
		return result, err
	}
	result.Snapshot = snapshot.ID
	versions.History = append(versions.History, appVersionEvent{What: "production's data from the preview", Approval: &approval, At: time.Now().UTC(), Snapshot: snapshot.ID})
	saveErr := saveTeamVersions(b.team, versions)
	b.mu.Unlock()
	if saveErr != nil {
		result.Warning = "the copy isn't in versions.json: " + saveErr.Error()
	}
	// Production's address shows it again before the channel hears.
	if _, err := b.production(0); err != nil {
		result.Warning = strings.TrimSpace(result.Warning + "\nProduction didn't start again yet (hi tries again within a minute): " + err.Error())
	}
	b.announceOps(config, appDataCopyAnnouncement(result, approval))
	return result, nil
}

func appDataCopyAnnouncement(result appDataCopy, approval appApproval) string {
	text := fmt.Sprintf(":floppy_disk: *Production now has a copy of the preview's data* (%s), approved by %s.", appDataSize(result.Bytes), approval.Name)
	if result.Snapshot != "" {
		text += fmt.Sprintf(" Production's data from before is kept in snapshot %s.", result.Snapshot)
	}
	return text
}

// copyDataLocked copies with b.mu held, and starts the preview again; the
// caller starts production. The source's box is paused while it is copied,
// so the copy is consistent (a paused SQLite database copies like one after
// a power cut), and the target's box is removed first.
func (b *appBroker) copyDataLocked(from, to string) (appDataCopy, error) {
	result := appDataCopy{From: from, To: to}
	src, dst := appSlotDataDir(b.team, from), appSlotDataDir(b.team, to)
	result.Bytes, result.Files, _ = appDataUsage(src)
	// A preview on a fork is its fork with its own writes over it; the
	// fork's files are linked, so only its own take room.
	overlay := from == appSlotPreview && appPreviewOverlay(b.team)
	room := result.Bytes
	if overlay {
		base, files, _ := appDataUsage(appPreviewBaseDir(b.team))
		result.Bytes, result.Files = result.Bytes+base, result.Files+files
	}
	if result.Files == 0 {
		return result, fmt.Errorf("%s has no data to copy (%s is empty)", appSlotName(from), src)
	}
	parent := filepath.Dir(dst)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return result, err
	}
	if free := appFreeBytes(parent); free >= 0 && free < room+appDataRoom {
		return result, fmt.Errorf("the disk has %s free, and the copy needs %s and some room to spare", appDataSize(free), appDataSize(room))
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
	appRemoveAll(fresh)
	var err error
	if overlay {
		err = appMergeOverlay(appPreviewBaseDir(b.team), src, fresh)
	} else if out, copyErr := exec.Command("cp", "-a", src, fresh).CombinedOutput(); copyErr != nil {
		err = fmt.Errorf("copying %s: %s", src, qFirstLine(strings.TrimSpace(string(out)), copyErr.Error()))
	}
	unpause()
	if err != nil {
		appRemoveAll(fresh)
		return result, err
	}
	old := fresh + "-old"
	if to == appSlotPreview {
		// The preview's data is in no snapshot: keep the last one.
		old = filepath.Join(parent, filepath.Base(dst)+"-before")
		result.Kept = old
	}
	appRemoveAll(old)
	if err := os.Rename(dst, old); err != nil && !errors.Is(err, os.ErrNotExist) {
		appRemoveAll(fresh)
		return result, err
	}
	if err := os.Rename(fresh, dst); err != nil {
		os.Rename(old, dst)
		return result, err
	}
	if to == appSlotProduction {
		appRemoveAll(old)
	} else if _, files, _ := appDataUsage(old); files == 0 {
		appRemoveAll(old)
		result.Kept = ""
	}
	if to == appSlotPreview && hadTarget {
		next := appPreview{Slot: appSlotPreview, Source: target.Source, Label: target.Label, Port: target.Port}
		if _, err := b.show(next, target.Commit, appLinkRenew); err != nil {
			result.Warning = "The preview didn't start again: " + err.Error()
		}
	}
	return result, nil
}

// ---------------------------------------------------------------------------
// the command, on the host

// appDataCommand is hi app data <name> [fork | copy <from> <to> |
// preview empty|fork] [--yes].
func appDataCommand(args []string, stdin io.Reader, stdout io.Writer) error {
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
	usage := usageError{"usage: hi app data <name> [fork | copy preview production | preview empty | preview fork] [--yes]"}
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
		printTeamDataFolders(stdout, appDataFolders(team, engineRunning))
		return nil
	case len(words) == 3 && words[1] == "preview":
		return appPreviewDataMode(team, words[2], stdout)
	case len(words) == 2 && words[1] == "fork":
		from, to = appSlotProduction, appSlotPreview
	case len(words) == 4 && words[1] == "copy":
		from, to = words[2], words[3]
	default:
		return usage
	}
	if err := appCheckDataSlots(from, to); err != nil {
		return err
	}
	bytes, files, _ := appDataUsage(appSlotDataDir(team, from))
	if from == appSlotPreview && appPreviewOverlay(team) {
		base, baseFiles, _ := appDataUsage(appPreviewBaseDir(team))
		bytes, files = bytes+base, files+baseFiles
	}
	if files == 0 {
		return fmt.Errorf("%s has no data to copy", appSlotName(from))
	}
	if to == appSlotProduction {
		fmt.Fprintf(stdout, "This replaces app %s's production data with a copy of the preview's (%s). hi snapshots production's data first, and posts it in the channel.\n", team, appDataSize(bytes))
		fmt.Fprintln(stdout, "The preview's box pauses while it is copied, and production restarts on the copy.")
	} else {
		fmt.Fprintf(stdout, "This gives app %s's preview a new fork of production's data (%s); what the preview wrote itself is kept in run/preview-data-before.\n", team, appDataSize(bytes))
		fmt.Fprintln(stdout, "Production's box pauses for a few seconds while it is forked, and the preview restarts on the fork.")
	}
	question := fmt.Sprintf("Copy %s's data to %s?", from, to)
	if to == appSlotPreview {
		question = "Fork production's data for the preview?"
	}
	if err := confirm(stdin, stdout, yes, question); err != nil {
		return err
	}
	var result appDataCopy
	asked, err := appHostCall(team, "/data/copy", appDataCopyRequest{From: from, To: to, Who: currentUserName()}, &result)
	if !asked {
		return fmt.Errorf("app %s isn't up, or runs a hi from before hi app data; hi app up %s, then try again", team, team)
	}
	if err != nil {
		return err
	}
	printTeamDataCopy(stdout, result)
	return nil
}

// appPreviewDataMode is hi app data <name> preview empty|fork: whether
// a preview starts on a fork of production's data at every release, or
// empty, for an app whose people's data previews shouldn't show.
func appPreviewDataMode(team, mode string, stdout io.Writer) error {
	config, err := loadTeam(team)
	if err != nil {
		return err
	}
	switch mode {
	case "empty":
		config.PreviewData = appPreviewEmpty
	case "fork":
		config.PreviewData = ""
	default:
		return usageError{"usage: hi app data <name> preview empty | preview fork"}
	}
	if err := saveTeam(config); err != nil {
		return err
	}
	if mode == "empty" {
		fmt.Fprintf(stdout, "App %s's preview starts on an empty data folder at every release, never on production's data. It keeps what it has until then.\n", team)
	} else {
		fmt.Fprintf(stdout, "App %s's preview starts on a fork of production's data at every release. hi app data %s fork gives it one now.\n", team, team)
	}
	return nil
}

func printTeamDataCopy(w io.Writer, result appDataCopy) {
	if fork := result.Fork; fork != nil && !fork.Full {
		fmt.Fprintf(w, "The preview runs on a new fork of production's data (%s in %d file(s)); %d file(s), %s, were copied in full (the databases and the files being written), the rest linked.",
			appDataSize(fork.Bytes), fork.Files, fork.Copied, appDataSize(fork.CopiedBytes))
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
	fmt.Fprintf(w, "%s has a copy of %s's data now (%s in %d file(s)).", appCapitalize(appSlotName(result.To)), result.From, appDataSize(result.Bytes), result.Files)
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
