package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// hi team adopt <name> <folder>: a project on this machine, copied without
// its history (docs/specs/approved/hi_team_adopt.md, A folder on this
// machine). hi copies what git would commit, leaves .git, secrets, and
// links that point outside the folder behind, and makes one commit of it;
// the folder is only read, and there is no upstream to keep up with.

// teamAdoptBigBytes is the size over which hi asks before copying a folder.
const teamAdoptBigBytes = 1 << 30

// teamAdoptFolder says whether a source names a folder on this machine,
// and gives its absolute path. A path that starts like one is a folder
// even when it's missing, so the error says so rather than fetching it;
// github.com/owner/name and URLs never are.
func teamAdoptFolder(source string) (string, bool, error) {
	if strings.Contains(source, "://") || strings.HasPrefix(source, "git@") || strings.HasPrefix(source, "github.com/") {
		return "", false, nil
	}
	path := source
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", true, err
		}
		path = filepath.Join(home, strings.TrimPrefix(path, "~"))
	}
	looksLikeOne := strings.HasPrefix(source, "/") || strings.HasPrefix(source, "./") || strings.HasPrefix(source, "../") ||
		source == "." || source == ".." || path != source
	info, err := os.Stat(path)
	switch {
	case err == nil && info.IsDir():
	case looksLikeOne && err == nil:
		return "", true, fmt.Errorf("%s isn't a folder", source)
	case looksLikeOne:
		return "", true, fmt.Errorf("there's no folder %s", source)
	default:
		return "", false, nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", true, err
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	return abs, true, nil
}

// teamAdoptSecretName is a file name that looks like it holds a secret.
var teamAdoptSecretName = regexp.MustCompile(`^(\.env(\..+)?|.+\.(pem|key|p12|pfx|kdbx)|id_(rsa|dsa|ecdsa|ed25519)(_sk)?|\.netrc|\.npmrc|\.pypirc|secrets?\..+|credentials.*\.json)$`)

// teamAdoptSecret says whether a file looks like it holds a secret; an
// example of one, such as .env.example, doesn't.
func teamAdoptSecret(name string) bool {
	base := strings.ToLower(filepath.Base(name))
	for _, example := range []string{".example", ".sample", ".template", ".dist"} {
		if strings.HasSuffix(base, example) {
			return false
		}
	}
	return teamAdoptSecretName.MatchString(base)
}

// teamAdoptCaches are folders hi doesn't copy from a folder outside git.
var teamAdoptCaches = map[string]bool{".git": true, "node_modules": true, "__pycache__": true, ".venv": true, "venv": true,
	".mypy_cache": true, ".pytest_cache": true, ".ruff_cache": true, ".next": true, ".turbo": true, ".cache": true}

// teamFolderFiles lists what to copy from a folder, as paths relative to
// it: what git would commit when it's in a repository, else every file
// but caches. .git is never in it.
func teamFolderFiles(folder string) []string {
	var files []string
	inGit := false
	if out, err := teamFolderGit(folder, "rev-parse", "--is-inside-work-tree"); err == nil && strings.TrimSpace(string(out)) == "true" {
		// fsmonitor is the one setting in the folder's git config that runs
		// a command for ls-files; it stays off.
		if out, err := teamFolderGit(folder, "ls-files", "-z", "-c", "-o", "--exclude-standard"); err == nil {
			inGit = true
			seen := map[string]bool{}
			for _, name := range strings.Split(string(out), "\x00") {
				if name == "" || seen[name] {
					continue
				}
				seen[name] = true
				// A submodule is listed as its folder: copy what's in it.
				if info, err := os.Lstat(filepath.Join(folder, name)); err == nil && info.IsDir() {
					files = append(files, teamWalkFolder(folder, name)...)
					continue
				}
				files = append(files, filepath.FromSlash(name))
			}
		}
	}
	if !inGit {
		files = teamWalkFolder(folder, ".")
	}
	kept := files[:0]
	for _, name := range files {
		if parts := strings.Split(filepath.ToSlash(name), "/"); !teamHasPart(parts, ".git") {
			kept = append(kept, name)
		}
	}
	sort.Strings(kept)
	return kept
}

func teamHasPart(parts []string, part string) bool {
	for _, p := range parts {
		if p == part {
			return true
		}
	}
	return false
}

// teamFolderGit is git in the folder being adopted, with fsmonitor and
// hooks off, so nothing from the folder's git config runs.
func teamFolderGit(folder string, args ...string) ([]byte, error) {
	command := exec.Command("git", append([]string{"-C", folder, "-c", "core.fsmonitor=false", "-c", "core.hooksPath=/dev/null"}, args...)...)
	command.Stderr = io.Discard
	return command.Output()
}

// teamWalkFolder lists the files under a folder's subfolder, but caches.
func teamWalkFolder(folder, sub string) []string {
	var files []string
	filepath.WalkDir(filepath.Join(folder, sub), func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() {
			if teamAdoptCaches[entry.Name()] && path != filepath.Join(folder, sub) {
				return filepath.SkipDir
			}
			return nil
		}
		if rel, err := filepath.Rel(folder, path); err == nil {
			files = append(files, rel)
		}
		return nil
	})
	return files
}

// teamFolderCopy is what a copy took, and what it left behind and why.
type teamFolderCopy struct {
	Files int
	Bytes int64
	Left  []string // "path: why"
}

// teamCopyFolder copies files from the folder into the stage, leaving out
// secrets, links that point outside it, and anything that isn't a file or
// a link.
func teamCopyFolder(folder, stage string, files []string) (teamFolderCopy, error) {
	var result teamFolderCopy
	for _, name := range files {
		source := filepath.Join(folder, name)
		info, err := os.Lstat(source)
		if err != nil {
			continue // gone since it was listed, or a tracked file deleted by hand
		}
		target := filepath.Join(stage, name)
		switch {
		case teamAdoptSecret(name):
			result.Left = append(result.Left, filepath.ToSlash(name)+": looks like a secret")
			continue
		case info.Mode()&fs.ModeSymlink != 0:
			link, err := os.Readlink(source)
			if err != nil {
				continue
			}
			resolved := link
			if !filepath.IsAbs(resolved) {
				resolved = filepath.Join(filepath.Dir(source), link)
			}
			if rel, err := filepath.Rel(folder, filepath.Clean(resolved)); err != nil || filepath.IsAbs(link) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				result.Left = append(result.Left, filepath.ToSlash(name)+": a link outside the folder")
				continue
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return result, err
			}
			if err := os.Symlink(link, target); err != nil {
				return result, err
			}
		case info.Mode().IsRegular():
			if err := teamCopyFolderFile(source, target, info.Mode().Perm()); err != nil {
				return result, fmt.Errorf("copying %s: %w", name, err)
			}
			result.Bytes += info.Size()
		default:
			result.Left = append(result.Left, filepath.ToSlash(name)+": not a file")
			continue
		}
		result.Files++
	}
	return result, nil
}

// teamCopyFolderFile copies a file of the folder, keeping its mode.
func teamCopyFolderFile(source, target string, mode fs.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode|0o200)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// teamFolderSize adds up what a copy would take, before copying it.
func teamFolderSize(folder string, files []string) int64 {
	var total int64
	for _, name := range files {
		if info, err := os.Lstat(filepath.Join(folder, name)); err == nil && info.Mode().IsRegular() {
			total += info.Size()
		}
	}
	return total
}

// prepareFolder is prepare for a folder on this machine: it copies the
// folder into the stage as one commit and shows the owner what was found.
func (adopt *teamAdoption) prepareFolder(folder, dir string, stdin io.Reader, stdout io.Writer) (bool, error) {
	switch {
	case adopt.Ref != "":
		return false, usageError{"--ref is for a repository to fetch; a folder is copied as it is"}
	case adopt.Shallow:
		return false, usageError{"--shallow is for a repository to fetch; a folder is copied without its history anyway"}
	case adopt.Push:
		return false, errors.New("a folder is copied, never pushed to: leave out push_to_source")
	}
	if adopt.GitHub != "" {
		if err := teamCheckGitHub(adopt.GitHub); err != nil {
			return false, err
		}
	}
	adopt.local, adopt.url = folder, folder
	adopt.stage = dir + ".adopting"
	stageAbs, _ := filepath.Abs(adopt.stage)
	if rel, err := filepath.Rel(folder, stageAbs); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false, fmt.Errorf("the team's folder would be inside %s: choose another one with --dir", folder)
	}
	if fileExists(adopt.stage) {
		return false, fmt.Errorf("%s is in the way: remove it, or choose another folder with --dir", adopt.stage)
	}
	files := teamFolderFiles(folder)
	if len(files) == 0 {
		return false, fmt.Errorf("%s has no files to adopt", folder)
	}
	if size := teamFolderSize(folder, files); size > teamAdoptBigBytes && !adopt.Full {
		fmt.Fprintf(stdout, "%s holds %s to copy.\n", folder, teamSize(float64(size)/(1<<20)))
		if !isTerminal(stdin) && adopt.Yes {
			return false, errors.New("it's over 1 GB: say --full to copy it anyway")
		}
		ok, err := teamYesNo(stdin, stdout, "Copy all of it?", false)
		if err != nil || !ok {
			return false, err
		}
	}
	fmt.Fprintf(stdout, "Copying %s, without its history…\n", folder)
	if err := os.MkdirAll(adopt.stage, 0o755); err != nil {
		return false, err
	}
	copied, err := teamCopyFolder(folder, adopt.stage, files)
	if err == nil {
		err = teamFolderCommit(folder, adopt.stage)
	}
	if err != nil {
		adopt.discard()
		return false, err
	}
	if out, err := teamFolderGit(folder, "rev-parse", "-q", "--verify", "HEAD"); err == nil {
		adopt.sourceCommit = strings.TrimSpace(string(out))
	}
	adopt.info = teamRepoInfo{SizeKB: int(copied.Bytes / 1024)}
	adopt.summary = teamSummarize(adopt.stage, adopt.info)
	adopt.summary.Name = adopt.displayName()
	adopt.summary.Local = fmt.Sprintf("%s, %s", teamPlural(copied.Files, "file"), "copied without its history")
	adopt.summary.Left = copied.Left
	fmt.Fprintln(stdout)
	printTeamAdoptSummary(adopt.summary, stdout)
	if adopt.Yes {
		return true, nil
	}
	ok, err := teamYesNo(stdin, stdout, fmt.Sprintf("Adopt it? The team keeps its own copy; nothing in %s changes.", folder), true)
	if err != nil || !ok {
		adopt.discard()
	}
	return ok, err
}

// teamFolderCommit makes the copy a repository of one commit, on the
// folder's branch's name, or main.
func teamFolderCommit(folder, stage string) error {
	branch := "main"
	if out, err := teamFolderGit(folder, "branch", "--show-current"); err == nil && strings.TrimSpace(string(out)) != "" {
		branch = strings.TrimSpace(string(out))
	}
	message := "Code from " + folder + "\n\nCopied by hi team adopt, without its history."
	for _, args := range [][]string{{"init", "-q", "-b", branch}, {"add", "-A", "--force"}, {"commit", "-q", "--no-verify", "-m", message}} {
		if out, err := teamGit(stage, args...); err != nil {
			return fmt.Errorf("git %s: %s", args[0], qFirstLine(out, err.Error()))
		}
	}
	return nil
}
