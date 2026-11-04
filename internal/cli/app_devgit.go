package cli

// The chat's Source control view: a workspace's changes not committed,
// staged or not, with each file's diff; staging, discarding, and a commit;
// and the branch's last commits, marked when dev hasn't them yet. git runs
// in the dev box, on the workspace's worktree, as the agents' git does.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"
)

// appDevGitFile is a file with changes: its status letter, git's (M, A,
// D, R, C, T), ? for one git doesn't know yet, or ! for a conflict.
type appDevGitFile struct {
	Path    string `json:"path"`
	Old     string `json:"old,omitempty"` // a rename's or copy's first name
	Status  string `json:"status"`
	Added   int    `json:"added,omitempty"`
	Removed int    `json:"removed,omitempty"`
	Binary  bool   `json:"binary,omitempty"`
}

// appDevGitCommit is one of the branch's commits.
type appDevGitCommit struct {
	Hash    string    `json:"hash"`
	Short   string    `json:"short"`
	Subject string    `json:"subject"`
	Author  string    `json:"author"`
	At      time.Time `json:"at"`
	New     bool      `json:"new,omitempty"` // dev hasn't it
}

// appDevGitView is what the view shows of a workspace.
type appDevGitView struct {
	Path      string            `json:"path"`
	Branch    string            `json:"branch"`
	Base      string            `json:"base"`             // dev's branch, as the clone has it
	Ahead     int               `json:"ahead"`            // commits dev hasn't
	Behind    int               `json:"behind,omitempty"` // dev's commits the branch hasn't
	Staged    []appDevGitFile   `json:"staged"`
	Changes   []appDevGitFile   `json:"changes"`
	Conflicts []appDevGitFile   `json:"conflicts,omitempty"`
	Commits   []appDevGitCommit `json:"commits"`
}

const (
	appDevGitDiffMax    = 512 << 10
	appDevGitMessageMax = 8 << 10
)

// appDevGitDir is the folder in the box a workspace's git runs in: its
// worktree, or the project's clone for a workspace with none.
func appDevGitDir(team, workspace string) (string, error) {
	spaces, err := appDevSpacesCached(team)
	if err != nil {
		return "", err
	}
	for _, space := range spaces {
		if space.ID == workspace {
			return firstNonEmpty(space.Path, "/box/repo"), nil
		}
	}
	return "", fmt.Errorf("the dev box has no workspace %s", workspace)
}

// appDevGitPath is a path the page names, in the worktree: relative, and
// not out of it.
func appDevGitPath(p string) (string, error) {
	clean := path.Clean(p)
	if p == "" || strings.ContainsRune(p, 0) || path.IsAbs(clean) || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || clean == ".git" || strings.HasPrefix(clean, ".git/") {
		return "", fmt.Errorf("%q isn't a file of the workspace", p)
	}
	return clean, nil
}

// appDevGitStatusScript prints, between group separators: the branch, git
// status, the changes' line counts not staged and staged, the last
// commits, those dev hasn't, and how many of dev's the branch hasn't.
const appDevGitStatusScript = `cd "$1" || exit 3
base="origin/$2"
git symbolic-ref --short -q HEAD || git rev-parse --short HEAD 2>/dev/null
printf '\035'
git status --porcelain=v1 -z -uall
printf '\035'
git diff --numstat -z -M
printf '\035'
git diff --cached --numstat -z -M 2>/dev/null
printf '\035'
git log -n 20 --format='%H%x1f%h%x1f%s%x1f%an%x1f%at%x1e' 2>/dev/null
printf '\035'
git rev-list -n 500 "$base..HEAD" 2>/dev/null
printf '\035'
git rev-list --count "HEAD..$base" 2>/dev/null
exit 0`

func (b *appBroker) devGitStatus(workspace string) (appDevGitView, error) {
	dir, err := appDevGitDir(b.team, workspace)
	if err != nil {
		return appDevGitView{}, err
	}
	config, err := loadTeam(b.team)
	if err != nil {
		return appDevGitView{}, err
	}
	out, err := appDevBoxExec(b.team, "sh", "-c", appDevGitStatusScript, "sh", dir, config.Branch)
	if err != nil {
		return appDevGitView{}, fmt.Errorf("git in %s: %s", dir, qFirstLine(strings.TrimSpace(out), err.Error()))
	}
	view := appDevGitParse(out)
	view.Path, view.Base = dir, config.Branch
	return view, nil
}

// appDevGitParse reads appDevGitStatusScript's output.
func appDevGitParse(out string) appDevGitView {
	parts := strings.Split(out, "\035")
	for len(parts) < 7 {
		parts = append(parts, "")
	}
	view := appDevGitView{Branch: strings.TrimSpace(parts[0]), Staged: []appDevGitFile{}, Changes: []appDevGitFile{}, Commits: []appDevGitCommit{}}
	unstaged, staged := appDevGitNumstat(parts[2]), appDevGitNumstat(parts[3])
	counted := func(file appDevGitFile, counts map[string]appDevGitFile) appDevGitFile {
		if n, ok := counts[file.Path]; ok {
			file.Added, file.Removed, file.Binary = n.Added, n.Removed, n.Binary
		}
		return file
	}
	fields := strings.Split(parts[1], "\x00")
	for i := 0; i < len(fields); i++ {
		entry := fields[i]
		if len(entry) < 4 {
			continue
		}
		x, y, name := entry[0], entry[1], entry[3:]
		old := ""
		if x == 'R' || x == 'C' {
			if i+1 < len(fields) {
				old = fields[i+1]
			}
			i++
		}
		switch {
		case x == '?':
			view.Changes = append(view.Changes, appDevGitFile{Path: name, Status: "?"})
		case x == '!':
		case x == 'U' || y == 'U' || x == 'A' && y == 'A' || x == 'D' && y == 'D':
			view.Conflicts = append(view.Conflicts, appDevGitFile{Path: name, Status: "!"})
		default:
			if x != ' ' {
				view.Staged = append(view.Staged, counted(appDevGitFile{Path: name, Old: old, Status: string(x)}, staged))
			}
			if y != ' ' {
				view.Changes = append(view.Changes, counted(appDevGitFile{Path: name, Status: string(y)}, unstaged))
			}
		}
	}
	shipped := map[string]bool{}
	for _, hash := range strings.Fields(parts[5]) {
		shipped[hash] = true
	}
	view.Ahead = len(shipped)
	for _, record := range strings.Split(parts[4], "\x1e") {
		f := strings.Split(strings.TrimLeft(record, "\n"), "\x1f")
		if len(f) != 5 {
			continue
		}
		at, _ := strconv.ParseInt(strings.TrimSpace(f[4]), 10, 64)
		view.Commits = append(view.Commits, appDevGitCommit{Hash: f[0], Short: f[1], Subject: f[2], Author: f[3], At: time.Unix(at, 0).UTC(), New: shipped[f[0]]})
	}
	view.Behind, _ = strconv.Atoi(strings.TrimSpace(parts[6]))
	return view
}

// appDevGitNumstat reads git diff --numstat -z: each file's lines added
// and removed, - for a binary file; a rename names both after an empty one.
func appDevGitNumstat(text string) map[string]appDevGitFile {
	counts := map[string]appDevGitFile{}
	fields := strings.Split(text, "\x00")
	for i := 0; i < len(fields); i++ {
		f := strings.SplitN(strings.TrimLeft(fields[i], "\n"), "\t", 3)
		if len(f) != 3 {
			continue
		}
		name := f[2]
		if name == "" && i+2 < len(fields) {
			name = fields[i+2]
			i += 2
		}
		added, errA := strconv.Atoi(f[0])
		removed, errR := strconv.Atoi(f[1])
		counts[name] = appDevGitFile{Path: name, Added: added, Removed: removed, Binary: errA != nil || errR != nil}
	}
	return counts
}

// appDevGitDiffScript prints a diff: what isn't staged, what is, or a new
// file whole. git diff --no-index says 1 when there are differences.
const appDevGitDiffScript = `cd "$1" || exit 3
export GIT_LITERAL_PATHSPECS=1
case "$2" in
staged) shift 2; git diff --cached -M -- "$@" ;;
new) git diff --no-index -- /dev/null "$3"; [ $? -le 1 ] ;;
*) shift 2; git diff -M -- "$@" ;;
esac`

func (b *appBroker) devGitDiff(workspace, kind string, paths []string) (string, error) {
	dir, err := appDevGitDir(b.team, workspace)
	if err != nil {
		return "", err
	}
	if len(paths) == 0 {
		return "", errors.New("which file's diff?")
	}
	for i, p := range paths {
		if paths[i], err = appDevGitPath(p); err != nil {
			return "", err
		}
	}
	out, err := appDevBoxExec(b.team, append([]string{"sh", "-c", appDevGitDiffScript, "sh", dir, kind}, paths...)...)
	if err != nil {
		return "", fmt.Errorf("git diff: %s", qFirstLine(strings.TrimSpace(out), err.Error()))
	}
	if len(out) > appDevGitDiffMax {
		out = appDevClip(out, appDevGitDiffMax) + "\n… (the rest of the diff is too long to show here)\n"
	}
	return out, nil
}

// appDevGitDoScript stages, unstages, discards, or commits. Discarding
// puts a file back as the last commit has it, or takes out a file the
// last commit hasn't; a commit with nothing staged commits everything.
const appDevGitDoScript = `cd "$1" || exit 3
export GIT_LITERAL_PATHSPECS=1
action="$2"; message="$3"; shift 3
case "$action" in
stage) if [ $# -eq 0 ]; then git add -A; else git add -A -- "$@"; fi ;;
unstage) if [ $# -eq 0 ]; then git reset -q; else git reset -q -- "$@"; fi ;;
discard)
  for p in "$@"; do
    if git cat-file -e "HEAD:$p" 2>/dev/null; then
      git restore -s HEAD -SW -- "$p" || exit 1
    else
      git rm -q --cached --ignore-unmatch -- "$p" >/dev/null && git clean -fq -- "$p" || exit 1
    fi
  done ;;
commit)
  if git diff --cached --quiet 2>/dev/null; then git add -A || exit 1; fi
  if git rev-parse -q --verify HEAD >/dev/null && git diff --cached --quiet; then echo "There's nothing to commit." >&2; exit 1; fi
  git commit -q -m "$message" 2>&1 || exit 1 ;;
*) echo "no such action" >&2; exit 2 ;;
esac`

// appDevGitRequest is what the view asks git to do.
type appDevGitRequest struct {
	Workspace string   `json:"workspace"`
	Action    string   `json:"action"` // stage, unstage, discard, commit
	Paths     []string `json:"paths"`
	Message   string   `json:"message"`
}

func (b *appBroker) devGitDo(request appDevGitRequest) error {
	dir, err := appDevGitDir(b.team, request.Workspace)
	if err != nil {
		return err
	}
	message := strings.TrimSpace(request.Message)
	switch request.Action {
	case "stage", "unstage":
	case "discard":
		if len(request.Paths) == 0 {
			return errors.New("which files to discard?")
		}
	case "commit":
		if message == "" {
			return errors.New("say what the commit does, in its message")
		}
		if len(message) > appDevGitMessageMax {
			return errors.New("that message is too long for a commit")
		}
		request.Paths = nil
	default:
		return fmt.Errorf("%q isn't something the view does", request.Action)
	}
	paths := make([]string, len(request.Paths))
	for i, p := range request.Paths {
		if paths[i], err = appDevGitPath(p); err != nil {
			return err
		}
	}
	out, err := appDevBoxExec(b.team, append([]string{"sh", "-c", appDevGitDoScript, "sh", dir, request.Action, message}, paths...)...)
	if err != nil {
		return errors.New(qFirstLine(strings.TrimSpace(out), err.Error()))
	}
	return nil
}

// devGitRoutes are the Source control view's: the status, a file's diff,
// and an action. For the owners only.
func (b *appBroker) devGitRoutes(mux *http.ServeMux, builder func(http.ResponseWriter, *http.Request) (appPersonKey, bool),
	reply func(http.ResponseWriter, int, any), failure func(http.ResponseWriter, int, error)) {
	mux.HandleFunc("GET "+appDevPagePrefix+"api/git", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := builder(w, r); !ok {
			return
		}
		view, err := b.devGitStatus(r.URL.Query().Get("workspace"))
		if err != nil {
			failure(w, http.StatusBadGateway, err)
			return
		}
		reply(w, http.StatusOK, view)
	})
	mux.HandleFunc("GET "+appDevPagePrefix+"api/git/diff", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := builder(w, r); !ok {
			return
		}
		query := r.URL.Query()
		diff, err := b.devGitDiff(query.Get("workspace"), query.Get("kind"), query["path"])
		if err != nil {
			failure(w, http.StatusBadRequest, err)
			return
		}
		reply(w, http.StatusOK, map[string]string{"diff": diff})
	})
	mux.HandleFunc("POST "+appDevPagePrefix+"api/git", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := builder(w, r); !ok {
			return
		}
		var request appDevGitRequest
		if err := json.NewDecoder(io.LimitReader(r.Body, 256<<10)).Decode(&request); err != nil {
			failure(w, http.StatusBadRequest, err)
			return
		}
		if err := b.devGitDo(request); err != nil {
			failure(w, http.StatusBadRequest, err)
			return
		}
		view, err := b.devGitStatus(request.Workspace)
		if err != nil {
			failure(w, http.StatusBadGateway, err)
			return
		}
		reply(w, http.StatusOK, view)
	})
}
