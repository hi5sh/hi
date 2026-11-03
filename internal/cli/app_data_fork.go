package cli

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
// (docs/specs/approved/hi_app_data_fork.md). The preview's box sees
// production's data as of the fork, read only, with a folder of its own
// above it that takes whatever the preview writes: an overlay, which
// Podman mounts rootless. The fork hardlinks production's files, which
// takes seconds and no disk, and copies in full the files production
// changes in place (databases, and whatever it was writing as it paused),
// so production writing to them later changes nothing in the preview.

// appPreviewEmpty is appConfig.PreviewData for a preview that starts
// empty at every release.
const appPreviewEmpty = "empty"

// appForkRecent is how recently a file changed for the fork to copy it
// in full rather than link it: production may have been writing it as it
// paused. Only that: files written once and kept are linked however new.
// A day was too long: on team orbit a backfill had downloaded 124k PDFs
// that day, and the first fork copied 102 GB of them, with production
// paused for three minutes.
const appForkRecent = 10 * time.Minute

// appPreviewFork is run/preview-fork.json: what the preview's data is a
// fork of.
type appPreviewFork struct {
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

// appPreviewBaseDir is the fork, below the preview's own folder.
func appPreviewBaseDir(team string) string {
	return filepath.Join(appDir(team), "run", "preview-base")
}

// appPreviewWorkDir is the overlay's work folder.
func appPreviewWorkDir(team string) string {
	return filepath.Join(appDir(team), "run", "preview-work")
}

func appPreviewForkPath(team string) string {
	return filepath.Join(appDir(team), "run", "preview-fork.json")
}

func loadTeamPreviewFork(team string) (appPreviewFork, bool) {
	var fork appPreviewFork
	data, err := os.ReadFile(appPreviewForkPath(team))
	if err != nil || json.Unmarshal(data, &fork) != nil {
		return appPreviewFork{}, false
	}
	return fork, true
}

func saveTeamPreviewFork(team string, fork appPreviewFork) error {
	data, _ := json.MarshalIndent(fork, "", "  ")
	return writeFileAtomic(appPreviewForkPath(team), append(data, '\n'), 0o600)
}

// appPreviewOverlay says the preview's box mounts its data as an overlay
// on a fork.
func appPreviewOverlay(team string) bool {
	fork, ok := loadTeamPreviewFork(team)
	if !ok || fork.Full {
		return false
	}
	info, err := os.Stat(appPreviewBaseDir(team))
	return err == nil && info.IsDir()
}

// appOverlayEngine says the box engine mounts overlays rootless: Podman
// does (-v …:O,upperdir=…), Docker doesn't. Tests replace it.
var appOverlayEngine = func() bool {
	engine, _, err := detectBoxEngine()
	return err == nil && engine.name == "podman"
}

// appBoxDataMount is how an app's box gets its data folder at /data: the
// preview's as an overlay on its fork, every other as the folder itself.
func appBoxDataMount(team, slot, data string) []string {
	if slot == appSlotPreview && appPreviewOverlay(team) && appOverlayEngine() {
		return []string{"-v", fmt.Sprintf("%s:%s:O,upperdir=%s,workdir=%s", appPreviewBaseDir(team), appBoxDataDir, data, appPreviewWorkDir(team))}
	}
	return []string{"-v", data + ":" + appBoxDataDir}
}

// appRemoveAll removes a folder an overlay wrote to. The overlay leaves
// files there that belong to the box's user namespace, which only
// podman unshare can remove.
func appRemoveAll(path string) error {
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

// appForkDatabases finds the files in dir that production changes in
// place: SQLite and DuckDB databases, their journals, and whatever is in a
// PostgreSQL data folder. They are copied in full; the rest is linked.
// It reads each file's first bytes, so it runs before production pauses.
func appForkDatabases(dir string) map[string]bool {
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
		if appDatabaseFile(path) {
			databases[rel] = true
		}
		return nil
	})
	return databases
}

// appDatabaseFile says a file is a database or its journal, by its name
// or its first bytes.
func appDatabaseFile(path string) bool {
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

// appForkInto forks src: every file is hardlinked into base, except the
// databases and what changed since recent, which are copied into own in
// full, so the overlay finds them there.
func appForkInto(src, base, own string, databases map[string]bool, recent time.Time) (appPreviewFork, error) {
	var fork appPreviewFork
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
			if err := appMirrorDirs(src, own, filepath.Dir(rel)); err != nil {
				return err
			}
			fork.Copied++
			fork.CopiedBytes += info.Size()
			return appCopyDataFile(path, filepath.Join(own, rel), info)
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

// appMirrorDirs makes the folders of rel in dst, with the modes they have
// in src.
func appMirrorDirs(src, dst, rel string) error {
	if rel == "." {
		return nil
	}
	if err := appMirrorDirs(src, dst, filepath.Dir(rel)); err != nil {
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

// appCopyDataFile copies a file whole, with its mode and time. Go copies
// with copy_file_range, which shares the blocks on a filesystem that can
// (XFS, Btrfs).
func appCopyDataFile(src, dst string, info fs.FileInfo) error {
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

// appForkPlan is what a fork of src needs before production pauses: the
// databases, and how much it copies in full.
func appForkPlan(src string, recent time.Time) (databases map[string]bool, copied int64) {
	databases = appForkDatabases(src)
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
func (b *appBroker) forkPreviewLocked() (appDataCopy, error) {
	result := appDataCopy{From: appSlotProduction, To: appSlotPreview}
	src := appSlotDataDir(b.team, appSlotProduction)
	result.Bytes, result.Files, _ = appDataUsage(src)
	if result.Files == 0 {
		return result, fmt.Errorf("production has no data to fork (%s is empty)", src)
	}
	why := ""
	run := filepath.Join(appDir(b.team), "run")
	switch {
	case !appOverlayEngine():
		why = "the box engine isn't Podman, which mounts the overlay"
	case strings.ContainsAny(run+src, ",:"):
		why = "the app's folder has a comma or colon in its path, which the overlay's mount can't take"
	}
	if why != "" {
		return b.copyWholeLocked(result, why)
	}
	recent := time.Now().Add(-appForkRecent)
	databases, copied := appForkPlan(src, recent)
	if free := appFreeBytes(run); free >= 0 && free < copied+appDataRoom {
		return result, fmt.Errorf("the disk has %s free, and the fork copies %s in full (the databases and the files being written) and needs some room to spare", appDataSize(free), appDataSize(copied))
	}

	b.previewMu.Lock()
	target, hadTarget := loadTeamSlot(b.team, appSlotPreview)
	if hadTarget {
		b.removeApp(target.Container)
	}
	base, own := filepath.Join(run, ".fork-base"), filepath.Join(run, ".fork-data")
	appRemoveAll(base)
	appRemoveAll(own)
	unpause := b.pauseSlot(appSlotProduction)
	fork, err := appForkInto(src, base, own, databases, recent)
	unpause()
	if err != nil {
		appRemoveAll(base)
		appRemoveAll(own)
		b.previewMu.Unlock()
		if errors.Is(err, errTeamForkNoLinks) {
			return b.copyWholeLocked(result, "hardlinks don't work in the app's folder")
		}
		b.restartPreview(target, hadTarget, &result)
		return result, fmt.Errorf("forking production's data: %w", err)
	}
	fork.At = time.Now().UTC()
	if versions, err := loadTeamVersions(b.team); err == nil {
		fork.Version = versions.Production
	}

	// The new fork goes in place; the preview's own writes are kept once.
	data := appSlotDataDir(b.team, appSlotPreview)
	kept := data + "-before"
	appRemoveAll(kept)
	if err := os.Rename(data, kept); err != nil && !errors.Is(err, os.ErrNotExist) {
		appRemoveAll(base)
		appRemoveAll(own)
		b.restartPreviewLocked(target, hadTarget, &result)
		b.previewMu.Unlock()
		return result, err
	}
	if _, files, _ := appDataUsage(kept); files > 0 {
		result.Kept = kept
	} else {
		appRemoveAll(kept)
	}
	appRemoveAll(appPreviewBaseDir(b.team))
	appRemoveAll(appPreviewWorkDir(b.team))
	err = errors.Join(os.Rename(own, data), os.Rename(base, appPreviewBaseDir(b.team)), os.MkdirAll(appPreviewWorkDir(b.team), 0o700), saveTeamPreviewFork(b.team, fork))
	if err != nil {
		// Whatever the preview's folder holds now, it starts on it alone.
		os.Remove(appPreviewForkPath(b.team))
		b.restartPreviewLocked(target, hadTarget, &result)
		b.previewMu.Unlock()
		return result, err
	}
	result.Fork = &fork
	b.restartPreviewLocked(target, hadTarget, &result)
	b.previewMu.Unlock()
	return result, nil
}

// appForkStale is how old the preview's fork may be when new work goes
// on the preview: older, it gets a fresh one first.
const appForkStale = time.Hour

// forkForNewWork gives the preview a fresh fork of production's data for
// new work, unless the fork is under appForkStale old, and says so.
func (b *appBroker) forkForNewWork() string {
	if fork, ok := loadTeamPreviewFork(b.team); ok && time.Since(fork.At) < appForkStale {
		return ""
	}
	b.mu.Lock()
	result, err := b.forkPreviewLocked()
	b.mu.Unlock()
	if err != nil {
		if result.Files == 0 {
			return "" // production has no data yet
		}
		return "The preview kept its data from before: refreshing it from production's failed (" + err.Error() + ")."
	}
	text := fmt.Sprintf("Before showing this new work, hi gave the preview a fresh copy of production's data (%s), so it is tried on what production has now.", appDataSize(result.Bytes))
	if result.Kept != "" {
		text += " What people tried in the preview before is kept once, apart."
	}
	return text
}

// copyWholeLocked gives the preview a whole copy of production's data,
// for where an overlay can't be had, and records why.
func (b *appBroker) copyWholeLocked(result appDataCopy, why string) (appDataCopy, error) {
	// The preview starts again in the copy without an overlay.
	fork := appPreviewFork{At: time.Now().UTC(), Bytes: result.Bytes, Files: result.Files, Copied: result.Files, CopiedBytes: result.Bytes, Full: true, Why: why}
	if versions, err := loadTeamVersions(b.team); err == nil {
		fork.Version = versions.Production
	}
	saveTeamPreviewFork(b.team, fork)
	appRemoveAll(appPreviewBaseDir(b.team))
	appRemoveAll(appPreviewWorkDir(b.team))
	copied, err := b.copyDataLocked(appSlotProduction, appSlotPreview)
	if err != nil {
		return copied, err
	}
	copied.Fork = &fork
	return copied, nil
}

// emptyPreviewLocked starts the preview again on an empty data folder,
// with b.mu held, for a team whose previews don't show production's data
// (hi app data <name> preview empty).
func (b *appBroker) emptyPreviewLocked() (appDataCopy, error) {
	result := appDataCopy{From: "", To: appSlotPreview}
	b.previewMu.Lock()
	defer b.previewMu.Unlock()
	target, hadTarget := loadTeamSlot(b.team, appSlotPreview)
	if hadTarget {
		b.removeApp(target.Container)
	}
	data := appSlotDataDir(b.team, appSlotPreview)
	kept := data + "-before"
	appRemoveAll(kept)
	if err := os.Rename(data, kept); err != nil && !errors.Is(err, os.ErrNotExist) {
		return result, err
	}
	if _, files, _ := appDataUsage(kept); files > 0 {
		result.Kept = kept
	} else {
		appRemoveAll(kept)
	}
	appRemoveAll(appPreviewBaseDir(b.team))
	appRemoveAll(appPreviewWorkDir(b.team))
	os.Remove(appPreviewForkPath(b.team))
	if err := os.MkdirAll(data, 0o700); err != nil {
		return result, err
	}
	b.restartPreviewLocked(target, hadTarget, &result)
	return result, nil
}

// pauseSlot pauses a slot's box while its data is read, and returns what
// unpauses it.
func (b *appBroker) pauseSlot(slot string) func() {
	if state, ok := loadTeamSlot(b.team, slot); ok && b.running(state.Container) {
		if _, err := b.engineOutput("pause", state.Container); err == nil {
			return func() { b.engineOutput("unpause", state.Container) }
		}
	}
	return func() {}
}

// restartPreview starts the preview again on the version it showed.
func (b *appBroker) restartPreview(target appPreview, had bool, result *appDataCopy) {
	b.previewMu.Lock()
	defer b.previewMu.Unlock()
	b.restartPreviewLocked(target, had, result)
}

func (b *appBroker) restartPreviewLocked(target appPreview, had bool, result *appDataCopy) {
	if !had {
		return
	}
	next := appPreview{Slot: appSlotPreview, Source: target.Source, Label: target.Label, Port: target.Port}
	if _, err := b.show(next, target.Commit, appLinkRenew); err != nil {
		result.Warning = strings.TrimSpace(result.Warning + "\nThe preview didn't start again: " + err.Error())
	}
}

// previewAfterRelease gives the preview production's data again after a
// release, so it runs production's code on a new fork of it: the next
// migration runs on a copy of production's real data first. A team whose
// previews start empty gets an empty one. Nothing happens while
// production has no data, so a preview that gathered what production
// lacks keeps it, or without an overlay, where a fork is a whole copy.
func (b *appBroker) previewAfterRelease() (appDataCopy, bool, error) {
	config, err := loadTeam(b.team)
	if err != nil {
		return appDataCopy{}, false, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if config.PreviewData == appPreviewEmpty {
		result, err := b.emptyPreviewLocked()
		return result, true, err
	}
	if _, files, _ := appDataUsage(appSlotDataDir(b.team, appSlotProduction)); files == 0 || !appOverlayEngine() {
		return appDataCopy{}, false, nil
	}
	result, err := b.forkPreviewLocked()
	return result, true, err
}

// appMergeOverlay writes the preview's data as its box sees it, its
// fork with its own writes over it, into dst, which doesn't exist yet:
// the fork's files linked, the preview's own copied, and what the preview
// deleted left out.
func appMergeOverlay(base, upper, dst string) error {
	if _, err := appForkInto(base, dst, dst+".unused", nil, time.Now().Add(time.Hour)); err != nil {
		if !errors.Is(err, errTeamForkNoLinks) {
			return err
		}
		appRemoveAll(dst)
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
		case appOverlayWhiteout(info):
			return os.RemoveAll(target)
		case name == ".wh..wh..opq":
			return nil // taken care of with its folder
		case strings.HasPrefix(name, ".wh."):
			return os.RemoveAll(filepath.Join(filepath.Dir(target), strings.TrimPrefix(name, ".wh.")))
		case entry.IsDir():
			// A folder the preview made again hides the fork's.
			if appOverlayOpaque(path) || fileExists(filepath.Join(path, ".wh..wh..opq")) {
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
		return appCopyDataFile(path, target, info)
	})
}
