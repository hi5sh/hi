package main

import (
	"encoding/json"
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

// A preview on a fork of production
// (docs/specs/approved/hi_team_data_fork.md). The preview's box sees
// production's data as of the fork, read only, with a folder of its own
// above it that takes whatever the preview writes: an overlay, which
// Podman mounts rootless. The fork hardlinks production's files, which
// takes seconds and no disk, and copies in full the files production
// changes in place (databases, and whatever changed in the last day), so
// production writing to them later changes nothing in the preview.

// teamPreviewEmpty is teamConfig.PreviewData for a preview that starts
// empty at every release.
const teamPreviewEmpty = "empty"

// teamForkRecent is how recently a file changed for the fork to copy it
// in full rather than link it: production may still be writing to it.
const teamForkRecent = 24 * time.Hour

// teamPreviewFork is run/preview-fork.json: what the preview's data is a
// fork of.
type teamPreviewFork struct {
	At time.Time `json:"at"`
	// Version is production's version when it was forked.
	Version int `json:"version,omitempty"`
	// Bytes and Files are production's data then.
	Bytes int64 `json:"bytes"`
	Files int   `json:"files"`
	// Copied files went into the preview's own folder in full.
	Copied      int   `json:"copied"`
	CopiedBytes int64 `json:"copied_bytes"`
	// Full says the preview's folder is a whole copy, with no overlay:
	// Why says why (Docker, links that don't work there).
	Full bool   `json:"full,omitempty"`
	Why  string `json:"why,omitempty"`
}

// teamPreviewBaseDir is the fork, below the preview's own folder.
func teamPreviewBaseDir(team string) string {
	return filepath.Join(teamDir(team), "run", "preview-base")
}

// teamPreviewWorkDir is the overlay's work folder.
func teamPreviewWorkDir(team string) string {
	return filepath.Join(teamDir(team), "run", "preview-work")
}

func teamPreviewForkPath(team string) string {
	return filepath.Join(teamDir(team), "run", "preview-fork.json")
}

func loadTeamPreviewFork(team string) (teamPreviewFork, bool) {
	var fork teamPreviewFork
	data, err := os.ReadFile(teamPreviewForkPath(team))
	if err != nil || json.Unmarshal(data, &fork) != nil {
		return teamPreviewFork{}, false
	}
	return fork, true
}

func saveTeamPreviewFork(team string, fork teamPreviewFork) error {
	data, _ := json.MarshalIndent(fork, "", "  ")
	return writeFileAtomic(teamPreviewForkPath(team), append(data, '\n'), 0o600)
}

// teamPreviewOverlay says the preview's box mounts its data as an overlay
// on a fork.
func teamPreviewOverlay(team string) bool {
	fork, ok := loadTeamPreviewFork(team)
	if !ok || fork.Full {
		return false
	}
	info, err := os.Stat(teamPreviewBaseDir(team))
	return err == nil && info.IsDir()
}

// teamOverlayEngine says the box engine mounts overlays rootless: Podman
// does (-v …:O,upperdir=…), Docker doesn't. Tests replace it.
var teamOverlayEngine = func() bool {
	engine, _, err := detectBoxEngine()
	return err == nil && engine.name == "podman"
}

// teamAppDataMount is how an app's box gets its data folder at /data: the
// preview's as an overlay on its fork, every other as the folder itself.
func teamAppDataMount(team, slot, data string) []string {
	if slot == teamSlotPreview && teamPreviewOverlay(team) && teamOverlayEngine() {
		return []string{"-v", fmt.Sprintf("%s:%s:O,upperdir=%s,workdir=%s", teamPreviewBaseDir(team), teamAppDataDir, data, teamPreviewWorkDir(team))}
	}
	return []string{"-v", data + ":" + teamAppDataDir}
}

// teamRemoveAll removes a folder an overlay wrote to. The overlay leaves
// files there that belong to the box's user namespace, which only
// podman unshare can remove.
func teamRemoveAll(path string) error {
	err := os.RemoveAll(path)
	if err == nil {
		return nil
	}
	if engine, _, engineErr := detectBoxEngine(); engineErr == nil && engine.name == "podman" {
		if exec.Command(engine.bin, "unshare", "rm", "-rf", path).Run() == nil {
			return nil
		}
	}
	return err
}

// teamForkDatabases finds the files in dir that production changes in
// place: SQLite and DuckDB databases, their journals, and whatever is in a
// PostgreSQL data folder. They are copied in full; the rest is linked.
// It reads each file's first bytes, so it runs before production pauses.
func teamForkDatabases(dir string) map[string]bool {
	databases := map[string]bool{}
	var postgres []string
	filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(dir, path)
		if entry.IsDir() {
			if fileExists(filepath.Join(path, "PG_VERSION")) {
				postgres = append(postgres, rel+string(filepath.Separator))
			}
			return nil
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		for _, prefix := range postgres {
			if strings.HasPrefix(rel, prefix) || prefix == "."+string(filepath.Separator) {
				databases[rel] = true
				return nil
			}
		}
		if teamDatabaseFile(path) {
			databases[rel] = true
		}
		return nil
	})
	return databases
}

// teamDatabaseFile says a file is a database or its journal, by its name
// or its first bytes.
func teamDatabaseFile(path string) bool {
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if strings.HasSuffix(path, suffix) {
			return true
		}
	}
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()
	head := make([]byte, 16)
	n, _ := io.ReadFull(file, head)
	head = head[:n]
	return strings.HasPrefix(string(head), "SQLite format 3\x00") || len(head) >= 12 && string(head[8:12]) == "DUCK"
}

var errTeamForkNoLinks = errors.New("hardlinks don't work here")

// teamForkInto forks src: every file is hardlinked into base, except the
// databases and what changed since recent, which are copied into own in
// full, so the overlay finds them there.
func teamForkInto(src, base, own string, databases map[string]bool, recent time.Time) (teamPreviewFork, error) {
	var fork teamPreviewFork
	if err := os.MkdirAll(base, 0o700); err != nil {
		return fork, err
	}
	if err := os.MkdirAll(own, 0o700); err != nil {
		return fork, err
	}
	err := filepath.WalkDir(src, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		if rel == "." {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		switch {
		case entry.IsDir():
			return os.Mkdir(filepath.Join(base, rel), info.Mode().Perm()|0o700)
		case entry.Type()&fs.ModeSymlink != 0:
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(target, filepath.Join(base, rel))
		case !entry.Type().IsRegular():
			return nil // sockets and pipes stay with production
		}
		fork.Files++
		fork.Bytes += info.Size()
		if databases[rel] || info.ModTime().After(recent) {
			if err := teamMirrorDirs(src, own, filepath.Dir(rel)); err != nil {
				return err
			}
			fork.Copied++
			fork.CopiedBytes += info.Size()
			return teamCopyDataFile(path, filepath.Join(own, rel), info)
		}
		if err := os.Link(path, filepath.Join(base, rel)); err != nil {
			if errors.Is(err, syscall.EXDEV) || errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.ENOTSUP) {
				return errTeamForkNoLinks
			}
			return err
		}
		return nil
	})
	return fork, err
}

// teamMirrorDirs makes the folders of rel in dst, with the modes they have
// in src.
func teamMirrorDirs(src, dst, rel string) error {
	if rel == "." {
		return nil
	}
	if err := teamMirrorDirs(src, dst, filepath.Dir(rel)); err != nil {
		return err
	}
	target := filepath.Join(dst, rel)
	if _, err := os.Lstat(target); err == nil {
		return nil
	}
	mode := os.FileMode(0o700)
	if info, err := os.Stat(filepath.Join(src, rel)); err == nil {
		mode = info.Mode().Perm() | 0o700
	}
	return os.Mkdir(target, mode)
}

// teamCopyDataFile copies a file whole, with its mode and time. Go copies
// with copy_file_range, which shares the blocks on a filesystem that can
// (XFS, Btrfs).
func teamCopyDataFile(src, dst string, info fs.FileInfo) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Chtimes(dst, info.ModTime(), info.ModTime())
}

// teamForkPlan is what a fork of src needs before production pauses: the
// databases, and how much it copies in full.
func teamForkPlan(src string, recent time.Time) (databases map[string]bool, copied int64) {
	databases = teamForkDatabases(src)
	filepath.WalkDir(src, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || !entry.Type().IsRegular() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(src, path)
		if databases[rel] || info.ModTime().After(recent) {
			copied += info.Size()
		}
		return nil
	})
	return databases, copied
}

// forkPreviewLocked gives the preview a new fork of production's data,
// with b.mu held, and starts the preview again on it. What the preview
// wrote itself is kept once, in run/preview-data-before/. Without an
// overlay (Docker, a folder where hardlinks don't work), the preview gets
// a whole copy, as before forks.
func (b *teamBroker) forkPreviewLocked() (teamDataCopy, error) {
	result := teamDataCopy{From: teamSlotProduction, To: teamSlotPreview}
	src := teamSlotDataDir(b.team, teamSlotProduction)
	result.Bytes, result.Files, _ = teamDataUsage(src)
	if result.Files == 0 {
		return result, fmt.Errorf("production has no data to fork (%s is empty)", src)
	}
	why := ""
	run := filepath.Join(teamDir(b.team), "run")
	switch {
	case !teamOverlayEngine():
		why = "the box engine isn't Podman, which mounts the overlay"
	case strings.ContainsAny(run+src, ",:"):
		why = "the team's folder has a comma or colon in its path, which the overlay's mount can't take"
	}
	if why != "" {
		return b.copyWholeLocked(result, why)
	}
	recent := time.Now().Add(-teamForkRecent)
	databases, copied := teamForkPlan(src, recent)
	if free := teamFreeBytes(run); free >= 0 && free < copied+teamDataRoom {
		return result, fmt.Errorf("the disk has %s free, and the fork copies %s in full (the databases and the last day's files) and needs some room to spare", teamDataSize(free), teamDataSize(copied))
	}

	b.previewMu.Lock()
	target, hadTarget := loadTeamSlot(b.team, teamSlotPreview)
	if hadTarget {
		b.removeApp(target.Container)
	}
	base, own := filepath.Join(run, ".fork-base"), filepath.Join(run, ".fork-data")
	teamRemoveAll(base)
	teamRemoveAll(own)
	unpause := b.pauseSlot(teamSlotProduction)
	fork, err := teamForkInto(src, base, own, databases, recent)
	unpause()
	if err != nil {
		teamRemoveAll(base)
		teamRemoveAll(own)
		b.previewMu.Unlock()
		if errors.Is(err, errTeamForkNoLinks) {
			return b.copyWholeLocked(result, "hardlinks don't work in the team's folder")
		}
		b.restartPreview(target, hadTarget, &result)
		return result, fmt.Errorf("forking production's data: %w", err)
	}
	fork.At = time.Now().UTC()
	if versions, err := loadTeamVersions(b.team); err == nil {
		fork.Version = versions.Production
	}

	// The new fork goes in place; the preview's own writes are kept once.
	data := teamSlotDataDir(b.team, teamSlotPreview)
	kept := data + "-before"
	teamRemoveAll(kept)
	if err := os.Rename(data, kept); err != nil && !errors.Is(err, os.ErrNotExist) {
		teamRemoveAll(base)
		teamRemoveAll(own)
		b.restartPreviewLocked(target, hadTarget, &result)
		b.previewMu.Unlock()
		return result, err
	}
	if _, files, _ := teamDataUsage(kept); files > 0 {
		result.Kept = kept
	} else {
		teamRemoveAll(kept)
	}
	teamRemoveAll(teamPreviewBaseDir(b.team))
	teamRemoveAll(teamPreviewWorkDir(b.team))
	err = errors.Join(os.Rename(own, data), os.Rename(base, teamPreviewBaseDir(b.team)), os.MkdirAll(teamPreviewWorkDir(b.team), 0o700), saveTeamPreviewFork(b.team, fork))
	if err != nil {
		// Whatever the preview's folder holds now, it starts on it alone.
		os.Remove(teamPreviewForkPath(b.team))
		b.restartPreviewLocked(target, hadTarget, &result)
		b.previewMu.Unlock()
		return result, err
	}
	result.Fork = &fork
	b.restartPreviewLocked(target, hadTarget, &result)
	b.previewMu.Unlock()
	return result, nil
}

// copyWholeLocked gives the preview a whole copy of production's data,
// for where an overlay can't be had, and records why.
func (b *teamBroker) copyWholeLocked(result teamDataCopy, why string) (teamDataCopy, error) {
	// The preview starts again in the copy without an overlay.
	fork := teamPreviewFork{At: time.Now().UTC(), Bytes: result.Bytes, Files: result.Files, Copied: result.Files, CopiedBytes: result.Bytes, Full: true, Why: why}
	if versions, err := loadTeamVersions(b.team); err == nil {
		fork.Version = versions.Production
	}
	saveTeamPreviewFork(b.team, fork)
	teamRemoveAll(teamPreviewBaseDir(b.team))
	teamRemoveAll(teamPreviewWorkDir(b.team))
	copied, err := b.copyDataLocked(teamSlotProduction, teamSlotPreview)
	if err != nil {
		return copied, err
	}
	copied.Fork = &fork
	return copied, nil
}

// emptyPreviewLocked starts the preview again on an empty data folder,
// with b.mu held, for a team whose previews don't show production's data
// (hi team data <name> preview empty).
func (b *teamBroker) emptyPreviewLocked() (teamDataCopy, error) {
	result := teamDataCopy{From: "", To: teamSlotPreview}
	b.previewMu.Lock()
	defer b.previewMu.Unlock()
	target, hadTarget := loadTeamSlot(b.team, teamSlotPreview)
	if hadTarget {
		b.removeApp(target.Container)
	}
	data := teamSlotDataDir(b.team, teamSlotPreview)
	kept := data + "-before"
	teamRemoveAll(kept)
	if err := os.Rename(data, kept); err != nil && !errors.Is(err, os.ErrNotExist) {
		return result, err
	}
	if _, files, _ := teamDataUsage(kept); files > 0 {
		result.Kept = kept
	} else {
		teamRemoveAll(kept)
	}
	teamRemoveAll(teamPreviewBaseDir(b.team))
	teamRemoveAll(teamPreviewWorkDir(b.team))
	os.Remove(teamPreviewForkPath(b.team))
	if err := os.MkdirAll(data, 0o700); err != nil {
		return result, err
	}
	b.restartPreviewLocked(target, hadTarget, &result)
	return result, nil
}

// pauseSlot pauses a slot's box while its data is read, and returns what
// unpauses it.
func (b *teamBroker) pauseSlot(slot string) func() {
	if state, ok := loadTeamSlot(b.team, slot); ok && b.running(state.Container) {
		if _, err := b.engineOutput("pause", state.Container); err == nil {
			return func() { b.engineOutput("unpause", state.Container) }
		}
	}
	return func() {}
}

// restartPreview starts the preview again on the version it showed.
func (b *teamBroker) restartPreview(target teamPreview, had bool, result *teamDataCopy) {
	b.previewMu.Lock()
	defer b.previewMu.Unlock()
	b.restartPreviewLocked(target, had, result)
}

func (b *teamBroker) restartPreviewLocked(target teamPreview, had bool, result *teamDataCopy) {
	if !had {
		return
	}
	next := teamPreview{Slot: teamSlotPreview, Source: target.Source, Label: target.Label, Port: target.Port}
	if _, err := b.show(next, target.Commit, teamLinkRenew); err != nil {
		result.Warning = strings.TrimSpace(result.Warning + "\nThe preview didn't start again: " + err.Error())
	}
}

// previewAfterRelease gives the preview production's data again after a
// release, so it runs production's code on a new fork of it: the next
// migration runs on a copy of production's real data first. A team whose
// previews start empty gets an empty one. Nothing happens while
// production has no data, so a preview that gathered what production
// lacks keeps it, or without an overlay, where a fork is a whole copy.
func (b *teamBroker) previewAfterRelease() (teamDataCopy, bool, error) {
	config, err := loadTeam(b.team)
	if err != nil {
		return teamDataCopy{}, false, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if config.PreviewData == teamPreviewEmpty {
		result, err := b.emptyPreviewLocked()
		return result, true, err
	}
	if _, files, _ := teamDataUsage(teamSlotDataDir(b.team, teamSlotProduction)); files == 0 || !teamOverlayEngine() {
		return teamDataCopy{}, false, nil
	}
	result, err := b.forkPreviewLocked()
	return result, true, err
}

// teamMergeOverlay writes the preview's data as its box sees it, its
// fork with its own writes over it, into dst, which doesn't exist yet:
// the fork's files linked, the preview's own copied, and what the preview
// deleted left out.
func teamMergeOverlay(base, upper, dst string) error {
	if _, err := teamForkInto(base, dst, dst+".unused", nil, time.Now().Add(time.Hour)); err != nil {
		if !errors.Is(err, errTeamForkNoLinks) {
			return err
		}
		teamRemoveAll(dst)
		if out, err := exec.Command("cp", "-a", base, dst).CombinedOutput(); err != nil {
			return fmt.Errorf("copying %s: %s", base, qFirstLine(strings.TrimSpace(string(out)), err.Error()))
		}
	}
	os.RemoveAll(dst + ".unused")
	return filepath.WalkDir(upper, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(upper, path)
		if rel == "." {
			return nil
		}
		target := filepath.Join(dst, rel)
		name := entry.Name()
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		switch {
		case teamOverlayWhiteout(info):
			return os.RemoveAll(target)
		case name == ".wh..wh..opq":
			return nil // taken care of with its folder
		case strings.HasPrefix(name, ".wh."):
			return os.RemoveAll(filepath.Join(filepath.Dir(target), strings.TrimPrefix(name, ".wh.")))
		case entry.IsDir():
			// A folder the preview made again hides the fork's.
			if teamOverlayOpaque(path) || fileExists(filepath.Join(path, ".wh..wh..opq")) {
				os.RemoveAll(target)
			}
			if existing, err := os.Lstat(target); err == nil && !existing.IsDir() {
				os.Remove(target)
			}
			if err := os.MkdirAll(target, info.Mode().Perm()|0o700); err != nil {
				return err
			}
			return nil
		case info.Mode()&fs.ModeSymlink != 0:
			os.RemoveAll(target)
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		case !info.Mode().IsRegular():
			return nil
		}
		// Never written in place: it may be a link to the fork's file.
		os.RemoveAll(target)
		return teamCopyDataFile(path, target, info)
	})
}

// printTeamReleasePreview tells the lead what a release did to the
// preview's data.
func printTeamReleasePreview(w io.Writer, preview *teamDataCopy) {
	switch {
	case preview == nil:
	case preview.Fork != nil && !preview.Fork.Full:
		fmt.Fprintf(w, "The preview runs on a new fork of production's data now (%s), so it shows what production has; what was tried in it before is gone from it", teamDataSize(preview.Fork.Bytes))
		if preview.Kept != "" {
			fmt.Fprint(w, " (kept once on the machine)")
		}
		fmt.Fprintln(w, ".")
	case preview.Fork == nil:
		fmt.Fprintln(w, "The preview starts on an empty data folder again, as the team set it (hi team data <team> preview empty).")
	}
}
