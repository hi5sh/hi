package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// Where hi agent review's code comes from beyond this machine, the briefs
// a project adds as areas, and --since (agent_review.go).

// reviewGitHubName is owner/name, as in github.com/owner/name.
var reviewGitHubName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]*/[A-Za-z0-9._-]+$`)

// reviewIsURL reports whether a review's source is a repository elsewhere
// rather than a folder here.
func reviewIsURL(source string) bool {
	if source == "" {
		return false
	}
	if strings.Contains(source, "://") || strings.HasPrefix(source, "git@") || strings.HasPrefix(source, "github.com/") {
		return true
	}
	// owner/name, when no such folder is here.
	if reviewGitHubName.MatchString(source) {
		if _, err := os.Stat(source); err != nil {
			return true
		}
	}
	return false
}

// reviewCloneURL turns a source into the URL git clones, and owner/name
// when it's on GitHub. A URL with a user name or token in it is refused:
// private repositories come through the host's gh sign-in.
func reviewCloneURL(source string) (cloneURL, github string, err error) {
	switch {
	case strings.HasPrefix(source, "git@"):
		host, path, ok := strings.Cut(strings.TrimPrefix(source, "git@"), ":")
		if !ok || path == "" {
			return "", "", fmt.Errorf("%q isn't a repository address", source)
		}
		if host == "github.com" {
			github = strings.TrimSuffix(path, ".git")
		}
		return source, github, nil
	case strings.HasPrefix(source, "github.com/"):
		source = "https://" + source
	case reviewGitHubName.MatchString(source):
		source = "https://github.com/" + source
	}
	parsed, err := url.Parse(source)
	if err != nil || parsed.Host == "" {
		return "", "", fmt.Errorf("%q isn't a repository address", source)
	}
	switch parsed.Scheme {
	case "https", "http", "ssh", "git":
	default:
		return "", "", fmt.Errorf("hi agent review fetches over https, ssh, or git, not %s://", parsed.Scheme)
	}
	if parsed.User != nil && parsed.Scheme != "ssh" {
		return "", "", errors.New("don't put a user name or token in the address; a private GitHub repository comes through your gh sign-in (gh auth login)")
	}
	if parsed.Host == "github.com" {
		parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
		if len(parts) < 2 {
			return "", "", fmt.Errorf("%q names no repository", source)
		}
		github = parts[0] + "/" + strings.TrimSuffix(parts[1], ".git")
		source = "https://github.com/" + github + ".git"
	}
	return source, github, nil
}

// reviewCloneArgs are git's settings for fetching code nobody here has
// read: no hooks or templates from this machine, no credential helpers,
// no LFS filters, and only network transports, so a submodule can't point
// at a folder on this machine. Submodules are listed, not fetched.
var reviewCloneArgs = []string{
	"-c", "credential.helper=", "-c", "core.hooksPath=/dev/null", "-c", "core.fsmonitor=false",
	"-c", "filter.lfs.smudge=", "-c", "filter.lfs.process=", "-c", "filter.lfs.required=false",
	"-c", "protocol.file.allow=never", "-c", "protocol.ext.allow=never",
}

func reviewCloneEnv() []string {
	return append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_LFS_SKIP_SMUDGE=1", "GIT_ALLOW_PROTOCOL=https:http:ssh:git",
		"GIT_CONFIG_NOSYSTEM=1")
}

// reviewFetch is replaced in tests.
var reviewFetch = fetchReviewSource

// fetchReviewSource clones a repository into dir on this machine, running
// nothing from it: first without any sign-in, then, for a GitHub
// repository that needs one, through gh, which keeps the token out of
// the address.
func fetchReviewSource(source, dir string) error {
	cloneURL, github, err := reviewCloneURL(source)
	if err != nil {
		return err
	}
	clone := func(command string, args ...string) error {
		os.RemoveAll(dir)
		cmd := exec.Command(command, args...)
		cmd.Env = reviewCloneEnv()
		var out bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &out
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("%s", qFirstLine(lastLines(out.String(), 3), err.Error()))
		}
		return nil
	}
	args := append(append([]string{}, reviewCloneArgs...), "clone", "--quiet", "--template=", "--no-recurse-submodules", "--", cloneURL, dir)
	if err = clone("git", args...); err == nil {
		return reviewLockClone(dir)
	}
	if github == "" {
		return err
	}
	if _, lookErr := exec.LookPath("gh"); lookErr != nil {
		return fmt.Errorf("couldn't fetch %s (%v); for a private repository, install gh and sign in with gh auth login", source, err)
	}
	// gh adds its own credential helper for github.com, and git clone takes
	// the rest of the settings for the new repository.
	ghArgs := []string{"repo", "clone", github, dir, "--", "--quiet", "--template=", "--no-recurse-submodules",
		"--config", "core.hooksPath=/dev/null", "--config", "core.fsmonitor=false", "--config", "filter.lfs.smudge=",
		"--config", "filter.lfs.process=", "--config", "filter.lfs.required=false"}
	if ghErr := clone("gh", ghArgs...); ghErr != nil {
		return fmt.Errorf("couldn't fetch %s: %v; through gh: %v", source, err, ghErr)
	}
	return reviewLockClone(dir)
}

// reviewLockClone keeps the clone's settings for what hi does in it later
// (worktrees for the boxes, removing branches): no hooks from this
// machine, and no LFS filter fetching anything.
func reviewLockClone(dir string) error {
	for _, setting := range [][2]string{{"core.hooksPath", "/dev/null"}, {"core.fsmonitor", "false"},
		{"filter.lfs.smudge", ""}, {"filter.lfs.process", ""}, {"filter.lfs.required", "false"}} {
		if err := boxCommand(nil, io.Discard, io.Discard, "git", "-C", dir, "config", setting[0], setting[1]); err != nil {
			return fmt.Errorf("setting %s in the clone: %w", setting[0], err)
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// briefs as areas

// reviewBriefName is an area's name from its brief's file name.
var reviewBriefName = regexp.MustCompile(`[^a-z0-9]+`)

// reviewBrief reads an area that isn't built in: a .md file by its path,
// or, in a repository on this machine, .hi5/review/<name>.md by name. A
// repository fetched from elsewhere was written by someone else, and its
// briefs would be its author's instructions to the investigators, so
// they're only taken by path, from a file you have.
func reviewBrief(area, root string, local bool) (name, brief string, err error) {
	path := ""
	switch {
	case strings.HasSuffix(strings.ToLower(area), ".md") || strings.Contains(area, "/"):
		path = area
	case local:
		path = filepath.Join(root, ".hi5", "review", area+".md")
		if !fileExists(path) {
			return "", "", usageError{fmt.Sprintf("--area takes %s, all, a brief (.md), or a name in .hi5/review/; not %q", strings.Join(reviewAreas, ", "), area)}
		}
	default:
		return "", "", usageError{fmt.Sprintf("--area takes %s, all, or a brief (.md) of yours; a fetched repository's own .hi5/review/ briefs aren't used, since its author wrote them", strings.Join(reviewAreas, ", "))}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", fmt.Errorf("--area %s: %w", area, err)
	}
	if len(data) > reviewMaxContext {
		return "", "", fmt.Errorf("--area %s is %d KB; keep a brief under %d KB", area, len(data)>>10, reviewMaxContext>>10)
	}
	brief = strings.TrimSpace(string(data))
	// Front matter is for hi agent's briefs; here only the text counts.
	if strings.HasPrefix(brief, "---\n") {
		if end := strings.Index(brief[4:], "\n---"); end >= 0 {
			brief = strings.TrimSpace(brief[4+end+4:])
		}
	}
	if brief == "" {
		return "", "", fmt.Errorf("--area %s is empty", area)
	}
	name = strings.Trim(reviewBriefName.ReplaceAllString(strings.ToLower(strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))), "-"), "-")
	if name == "" {
		name = "brief"
	}
	if len(name) > 24 {
		name = strings.TrimRight(name[:24], "-")
	}
	if containsString(reviewAreas, name) {
		name += "-brief"
	}
	return name, brief, nil
}

// ---------------------------------------------------------------------------
// --since

// reviewSinceLimit is how many changed files a brief lists by name.
const reviewSinceLimit = 200

// reviewSince resolves --since and lists what changed from it to the
// reviewed commit, from where their histories meet.
func reviewSince(rv *review, since string) error {
	commit := boxGit(rv.Root, "rev-parse", "--verify", "-q", since+"^{commit}")
	if commit == "" {
		return fmt.Errorf("--since %s isn't a commit in this repository", since)
	}
	rv.Since, rv.SinceCommit = since, commit
	files := boxGit(rv.Root, "diff", "--name-only", commit+"..."+rv.Commit)
	rv.SinceFiles = nil
	for _, file := range strings.Split(files, "\n") {
		if file = strings.TrimSpace(file); file != "" {
			rv.SinceFiles = append(rv.SinceFiles, file)
		}
	}
	if len(rv.SinceFiles) == 0 {
		return fmt.Errorf("nothing changed since %s; review it without --since", since)
	}
	return nil
}

// reviewSinceText is what a brief says about --since.
func reviewSinceText(rv review) string {
	if rv.SinceCommit == "" {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Focus on what changed since %s (%s): `git diff %s...%s` shows it. Read the rest of the repository as you need to understand the changes, and report problems in them or caused by them, including ones they expose in code that didn't change. A problem in code the changes didn't touch, and don't make worse, is out of scope here.\n\nChanged files:\n\n",
		rv.Since, shortCommit(rv.SinceCommit), shortCommit(rv.SinceCommit), shortCommit(rv.Commit))
	for i, file := range rv.SinceFiles {
		if i == reviewSinceLimit {
			fmt.Fprintf(&b, "- and %d more (git diff --name-only shows them)\n", len(rv.SinceFiles)-reviewSinceLimit)
			break
		}
		fmt.Fprintf(&b, "- %s\n", file)
	}
	return b.String()
}
