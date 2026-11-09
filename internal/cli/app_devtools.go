package cli

// The dev page's Search, Checks, and Logs, in the panel beside the chat:
// the workspace's files searched with git grep, its checks (make check,
// test, lint, or the package's scripts) run in the dev box, and what
// production and Stage print and serve.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// ---- Search

// appDevSearchMatch is a line git grep found.
type appDevSearchMatch struct {
	Path   string `json:"path"`
	Line   int    `json:"line"`
	Column int    `json:"column"`
	Text   string `json:"text"`
}

const (
	appDevSearchMax     = 500 // matches the page gets
	appDevSearchPerFile = 50
	appDevSearchText    = 240 // runes of a line
)

// appDevSearchScript searches the worktree's files, tracked or new, not
// those git ignores, for at most 20 seconds.
const appDevSearchScript = `cd "$1" || exit 3
shift
timeout 20 git grep -n --column -z -I --untracked --no-color -m 50 "$@" 2>&1 | head -n 501`

func appDevSearchArgs(query string, matchCase, regex bool) ([]string, error) {
	if strings.TrimSpace(query) == "" || len(query) > 200 || strings.ContainsRune(query, 0) || strings.ContainsAny(query, "\n\r") {
		return nil, errors.New("search for something on one line, up to 200 characters")
	}
	args := []string{"-F"}
	if regex {
		if _, err := regexp.Compile(query); err != nil {
			return nil, fmt.Errorf("that isn't a regular expression: %v", err)
		}
		args = []string{"-E"}
	}
	if !matchCase {
		args = append(args, "-i")
	}
	return append(args, "-e", query, "--"), nil
}

// appDevSearchParse reads git grep -z's path NUL line NUL column NUL text.
func appDevSearchParse(out string) ([]appDevSearchMatch, bool) {
	var matches []appDevSearchMatch
	for _, line := range strings.Split(out, "\n") {
		parts := strings.SplitN(line, "\x00", 4)
		if len(parts) != 4 {
			continue
		}
		number, err1 := strconv.Atoi(parts[1])
		column, err2 := strconv.Atoi(parts[2])
		if err1 != nil || err2 != nil {
			continue
		}
		if len(matches) == appDevSearchMax {
			return matches, true
		}
		text := strings.TrimRight(parts[3], "\r")
		if utf8.RuneCountInString(text) > appDevSearchText {
			// Around the match, when it's far along the line.
			runes := []rune(text)
			start := 0
			if column > appDevSearchText/2 {
				start = min(column-appDevSearchText/2, len(runes)-appDevSearchText)
			}
			text = string(runes[start : start+appDevSearchText])
			if start > 0 {
				text = "…" + text
				column -= start - 1
			}
			text += "…"
		}
		matches = append(matches, appDevSearchMatch{Path: parts[0], Line: number, Column: column, Text: text})
	}
	return matches, false
}

func appDevSearch(team, workspace, query string, matchCase, regex bool) ([]appDevSearchMatch, bool, error) {
	args, err := appDevSearchArgs(query, matchCase, regex)
	if err != nil {
		return nil, false, err
	}
	dir, err := appDevGitDir(team, workspace)
	if err != nil {
		return nil, false, err
	}
	out, err := appDevBoxExec(team, append([]string{"sh", "-c", appDevSearchScript, "sh", dir}, args...)...)
	if err != nil {
		return nil, false, err
	}
	// git grep's own failure: its exit is head's, so by what it said.
	if text := strings.TrimSpace(out); text != "" && !strings.Contains(out, "\x00") {
		return nil, false, fmt.Errorf("git grep: %s", qFirstLine(text, "it failed"))
	}
	matches, more := appDevSearchParse(out)
	return matches, more, nil
}

// ---- Checks

// appDevCheck is a check the workspace's project has: a make target, or
// a script of its package.json.
type appDevCheck struct {
	Name    string `json:"name"`
	Command string `json:"command"`
	About   string `json:"about,omitempty"`
}

// The checks the page offers, in this order: make's own, the templates'
// (make check is what must pass before a change is done), then scripts.
var appDevCheckTargets = []string{"check", "test", "lint", "typecheck", "fix"}

const appDevChecksScript = `cd "$1" || exit 3
[ -f Makefile ] && grep -E '^[a-z][a-z_-]*:' Makefile | head -n 100
printf '\035'
[ -f package.json ] && head -c 65536 package.json
printf '\035'
for f in bun.lock bun.lockb pnpm-lock.yaml yarn.lock; do [ -f "$f" ] && echo "$f"; done
exit 0`

var appDevMakeTarget = regexp.MustCompile(`^([a-z][a-z_-]*):[^=]*?(?:##\s*(.*))?$`)

func appDevChecksParse(out string) []appDevCheck {
	parts := strings.SplitN(out, "\x1d", 3)
	for len(parts) < 3 {
		parts = append(parts, "")
	}
	about := map[string]string{}
	inMake := map[string]bool{}
	for _, line := range strings.Split(parts[0], "\n") {
		if m := appDevMakeTarget.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
			inMake[m[1]] = true
			about[m[1]] = strings.TrimSpace(m[2])
		}
	}
	var checks []appDevCheck
	for _, name := range appDevCheckTargets {
		if inMake[name] {
			checks = append(checks, appDevCheck{Name: name, Command: "make " + name, About: about[name]})
		}
	}
	if len(checks) > 0 {
		return checks
	}
	var pkg struct {
		Scripts map[string]string `json:"scripts"`
	}
	if json.Unmarshal([]byte(parts[1]), &pkg) != nil {
		return nil
	}
	runner := "npm run"
	switch lock := strings.TrimSpace(parts[2]); {
	case strings.HasPrefix(lock, "bun"):
		runner = "bun run"
	case strings.HasPrefix(lock, "pnpm"):
		runner = "pnpm run"
	case strings.HasPrefix(lock, "yarn"):
		runner = "yarn run"
	}
	for _, name := range appDevCheckTargets {
		if script, ok := pkg.Scripts[name]; ok {
			checks = append(checks, appDevCheck{Name: name, Command: runner + " " + name, About: script})
		}
	}
	return checks
}

// appDevCheckRun is a check running, or the last that ran, in a workspace.
type appDevCheckRun struct {
	Name     string    `json:"name"`
	Command  string    `json:"command"`
	Started  time.Time `json:"started"`
	Finished time.Time `json:"finished,omitzero"`
	Running  bool      `json:"running"`
	Exit     int       `json:"exit"`
	Stopped  bool      `json:"stopped,omitempty"`
	Error    string    `json:"error,omitempty"`
	Output   string    `json:"output"` // from the offset asked for
	Offset   int64     `json:"offset"` // all that was written
	Reset    bool      `json:"reset,omitempty"`

	id      string
	written int64
	buf     []byte // the last appDevCheckKeep bytes
}

const appDevCheckKeep = 256 << 10
const appDevCheckLasts = 30 * time.Minute

func (r *appDevCheckRun) Write(p []byte) (int, error) {
	appDevChecks.Lock()
	defer appDevChecks.Unlock()
	r.written += int64(len(p))
	r.buf = append(r.buf, p...)
	if len(r.buf) > appDevCheckKeep {
		r.buf = append([]byte(nil), r.buf[len(r.buf)-appDevCheckKeep:]...)
	}
	return len(p), nil
}

// appDevChecks are the runs, by team and workspace; discovered checks are
// kept a few seconds, as the page asks often.
var appDevChecks = struct {
	sync.Mutex
	runs  map[string]*appDevCheckRun
	found map[string]appDevChecksFound
}{runs: map[string]*appDevCheckRun{}, found: map[string]appDevChecksFound{}}

type appDevChecksFound struct {
	at     time.Time
	checks []appDevCheck
}

// appDevBoxStream runs a command in the dev box with its output, both
// streams, to out, until it ends or ctx does.
var appDevBoxStream = func(ctx context.Context, team string, out io.Writer, command ...string) (int, error) {
	engine, _, err := detectBoxEngine()
	if err != nil {
		return -1, err
	}
	cmd := exec.CommandContext(ctx, engine.bin, append([]string{"exec", appDevContainer(team)}, command...)...)
	cmd.Stdout, cmd.Stderr = out, out
	err = cmd.Run()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode(), nil
	}
	if err != nil {
		return -1, err
	}
	return 0, nil
}

// appDevCheckScript runs the check in its own process group, so Stop
// ends all of it, for at most half an hour; its PID goes in the file.
const appDevCheckScript = `cd "$1" || exit 3
mkdir -p /tmp/hi-checks
exec setsid -w sh -c 'echo $$ > "$1"; shift; exec timeout 1800 sh -c "$1"' sh "$2" "$3"`

func appDevChecksFor(team, workspace string) ([]appDevCheck, error) {
	key := team + "\n" + workspace
	appDevChecks.Lock()
	found, ok := appDevChecks.found[key]
	appDevChecks.Unlock()
	if ok && time.Since(found.at) < 20*time.Second {
		return found.checks, nil
	}
	dir, err := appDevGitDir(team, workspace)
	if err != nil {
		return nil, err
	}
	out, err := appDevBoxExec(team, "sh", "-c", appDevChecksScript, "sh", dir)
	if err != nil && !strings.Contains(out, "\x1d") {
		return nil, fmt.Errorf("the dev box didn't say: %s", qFirstLine(strings.TrimSpace(out), err.Error()))
	}
	checks := appDevChecksParse(out)
	appDevChecks.Lock()
	appDevChecks.found[key] = appDevChecksFound{at: time.Now(), checks: checks}
	appDevChecks.Unlock()
	return checks, nil
}

func appDevStartCheck(team, workspace, name string) error {
	checks, err := appDevChecksFor(team, workspace)
	if err != nil {
		return err
	}
	var check *appDevCheck
	for i := range checks {
		if checks[i].Name == name {
			check = &checks[i]
		}
	}
	if check == nil {
		return fmt.Errorf("the project has no %s check", name)
	}
	key := team + "\n" + workspace
	appDevChecks.Lock()
	if run := appDevChecks.runs[key]; run != nil && run.Running {
		appDevChecks.Unlock()
		return fmt.Errorf("%s is running already; stop it first", run.Command)
	}
	run := &appDevCheckRun{Name: check.Name, Command: check.Command, Started: time.Now(), Running: true,
		id: strconv.FormatInt(time.Now().UnixNano(), 36)}
	appDevChecks.runs[key] = run
	appDevChecks.Unlock()
	dir, err := appDevGitDir(team, workspace)
	if err != nil {
		appDevChecks.Lock()
		delete(appDevChecks.runs, key)
		appDevChecks.Unlock()
		return err
	}
	// The box's hooks as they are now: the run goes on after this returns.
	stream, exec := appDevBoxStream, appDevBoxExec
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), appDevCheckLasts+time.Minute)
		defer cancel()
		fmt.Fprintf(run, "$ %s\n", check.Command)
		exit, err := stream(ctx, team, run, "sh", "-c", appDevCheckScript, "sh", dir, "/tmp/hi-checks/"+run.id+".pid", check.Command)
		appDevChecks.Lock()
		run.Running, run.Exit, run.Finished = false, exit, time.Now()
		if err != nil {
			run.Error = err.Error()
		}
		appDevChecks.Unlock()
		exec(team, "rm", "-f", "/tmp/hi-checks/"+run.id+".pid")
	}()
	return nil
}

func appDevStopCheck(team, workspace string) error {
	appDevChecks.Lock()
	run := appDevChecks.runs[team+"\n"+workspace]
	if run == nil || !run.Running {
		appDevChecks.Unlock()
		return errors.New("no check is running here")
	}
	run.Stopped = true
	id := run.id
	appDevChecks.Unlock()
	out, err := appDevBoxExec(team, "sh", "-c", `kill -TERM "-$(cat "$1")"`, "sh", "/tmp/hi-checks/"+id+".pid")
	if err != nil {
		return fmt.Errorf("couldn't stop it: %s", qFirstLine(strings.TrimSpace(out), err.Error()))
	}
	return nil
}

// appDevCheckView is the last run in a workspace, its output from after.
func appDevCheckView(team, workspace string, after int64) *appDevCheckRun {
	appDevChecks.Lock()
	defer appDevChecks.Unlock()
	run := appDevChecks.runs[team+"\n"+workspace]
	if run == nil {
		return nil
	}
	view := *run
	view.buf = nil
	first := run.written - int64(len(run.buf))
	if after < first || after > run.written {
		view.Reset, after = true, first
	}
	view.Output = strings.ToValidUTF8(string(run.buf[after-first:]), "�")
	view.Offset = run.written
	return &view
}

// ---- Logs

// appDevLogLine is a line production or Stage printed, or a request its
// address served.
type appDevLogLine struct {
	At     time.Time `json:"at,omitzero"`
	Text   string    `json:"text"`
	Error  bool      `json:"error,omitempty"`  // stderr, or a 5xx
	Status int       `json:"status,omitempty"` // a request's
}

// appDevLogView is what the Logs view asks for, from after its cursor.
type appDevLogView struct {
	Slot      string          `json:"slot"`
	Source    string          `json:"source"`
	Label     string          `json:"label,omitempty"`
	Container string          `json:"container,omitempty"`
	Lines     []appDevLogLine `json:"lines"`
	Cursor    string          `json:"cursor"`
	Reset     bool            `json:"reset,omitempty"`
	Note      string          `json:"note,omitempty"`
}

const appDevLogLines = 400

// appJournal reads the user's journal; tests replace it.
var appJournal = func(args ...string) ([]byte, error) {
	return exec.Command("journalctl", append([]string{"--user", "--no-pager"}, args...)...).Output()
}

func appDevLogs(team, slot, source, after string) (appDevLogView, error) {
	if slot != appSlotProduction && slot != appSlotPreview {
		return appDevLogView{}, fmt.Errorf("no %q to read; production or preview", slot)
	}
	view := appDevLogView{Slot: slot, Source: source}
	state, ok := loadTeamSlot(team, slot)
	if !ok {
		view.Note = map[string]string{appSlotProduction: "Nothing is released yet.", appSlotPreview: "Stage shows nothing yet."}[slot]
		return view, nil
	}
	view.Label, view.Container = state.Label, state.Container
	switch source {
	case "output":
		return appDevJournalLogs(view, after)
	case "requests":
		return appDevRequestLogs(view, state.Expose, after)
	}
	return appDevLogView{}, fmt.Errorf("no %q log; output or requests", source)
}

// appDevJournalLogs are what the app's box printed, as Podman gave the
// journal: stdout at priority 6, stderr at 3. A cursor of another box (a
// release since) starts again.
func appDevJournalLogs(view appDevLogView, after string) (appDevLogView, error) {
	if view.Container == "" {
		view.Note = "hi doesn't know this one's box."
		return view, nil
	}
	cursor, box, _ := strings.Cut(after, "|")
	args := []string{"CONTAINER_NAME=" + view.Container, "-o", "json"}
	if cursor != "" && box == view.Container {
		args = append(args, "--after-cursor="+cursor, "-n", "2000")
	} else {
		args = append(args, "-n", strconv.Itoa(appDevLogLines))
		view.Reset = true
	}
	out, err := appJournal(args...)
	if err != nil && len(out) == 0 {
		return appDevLogView{}, fmt.Errorf("journalctl: %v", err)
	}
	view.Cursor = after
	if view.Reset {
		view.Cursor = ""
	}
	for _, line := range bytes.Split(out, []byte("\n")) {
		var entry struct {
			Message  json.RawMessage `json:"MESSAGE"`
			Priority string          `json:"PRIORITY"`
			At       string          `json:"__REALTIME_TIMESTAMP"`
			Cursor   string          `json:"__CURSOR"`
		}
		if len(bytes.TrimSpace(line)) == 0 || json.Unmarshal(line, &entry) != nil {
			continue
		}
		var text string
		if json.Unmarshal(entry.Message, &text) != nil {
			var raw []byte // journald's bytes, for a message that isn't UTF-8
			if json.Unmarshal(entry.Message, &raw) == nil {
				text = strings.ToValidUTF8(string(raw), "�")
			}
		}
		micro, _ := strconv.ParseInt(entry.At, 10, 64)
		view.Lines = append(view.Lines, appDevLogLine{At: time.UnixMicro(micro).UTC(), Text: strings.TrimRight(text, "\n"), Error: entry.Priority == "3"})
		view.Cursor = entry.Cursor + "|" + view.Container
	}
	if len(view.Lines) > appDevLogLines*2 {
		view.Lines = view.Lines[len(view.Lines)-appDevLogLines*2:]
	}
	return view, nil
}

var appDevRequestStatus = regexp.MustCompile(`^\S+\s+\S+\s+(\d{3})\s`)

// appDevRequestLogs are the requests the address served, from the relay's
// log: its newest, for the address's name, read from where the page got
// to. The relay writes the path, never the query, so no key.
func appDevRequestLogs(view appDevLogView, expose, after string) (appDevLogView, error) {
	file := appDevRequestLogFile(expose)
	if file == "" {
		view.Note = "This address keeps no log of its requests on this machine."
		return view, nil
	}
	handle, err := os.Open(file)
	if err != nil {
		return appDevLogView{}, err
	}
	defer handle.Close()
	info, err := handle.Stat()
	if err != nil {
		return appDevLogView{}, err
	}
	name, offsetText, _ := strings.Cut(after, "|")
	offset, _ := strconv.ParseInt(offsetText, 10, 64)
	if name != filepath.Base(file) || offset > info.Size() {
		view.Reset = true
		offset = max(0, info.Size()-64<<10)
	}
	if info.Size()-offset > 1<<20 {
		offset = info.Size() - 1<<20
	}
	data := make([]byte, info.Size()-offset)
	n, _ := handle.ReadAt(data, offset)
	data = data[:n]
	// Whole lines only; a reset starts at the first whole one.
	if view.Reset && offset > 0 {
		if i := bytes.IndexByte(data, '\n'); i >= 0 {
			data, offset = data[i+1:], offset+int64(i+1)
		}
	}
	end := bytes.LastIndexByte(data, '\n') + 1
	for _, line := range strings.Split(string(data[:end]), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		item := appDevLogLine{Text: strings.ToValidUTF8(line, "�")}
		if m := appDevRequestStatus.FindStringSubmatch(line); m != nil {
			item.Status, _ = strconv.Atoi(m[1])
			item.Error = item.Status >= 500
		}
		view.Lines = append(view.Lines, item)
	}
	if len(view.Lines) > appDevLogLines*2 {
		view.Lines = view.Lines[len(view.Lines)-appDevLogLines*2:]
	}
	view.Cursor = filepath.Base(file) + "|" + strconv.FormatInt(offset+int64(end), 10)
	return view, nil
}

// appDevRequestLogFile is the relay's newest log for an exposure: its
// name, less the random part, then the time it started.
func appDevRequestLogFile(expose string) string {
	if expose == "" {
		return ""
	}
	var state struct {
		Name string `json:"name"`
	}
	if data, err := os.ReadFile(filepath.Join(netExposeDir(), expose+".json")); err == nil {
		json.Unmarshal(data, &state)
	}
	if state.Name == "" {
		return ""
	}
	files, _ := filepath.Glob(filepath.Join(netExposeDir(), state.Name+"-*.log"))
	var started []string
	for _, file := range files {
		stamp := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(file), state.Name+"-"), ".log")
		if _, err := strconv.ParseInt(stamp, 10, 64); err == nil {
			started = append(started, file)
		}
	}
	if len(started) == 0 {
		return ""
	}
	sort.Slice(started, func(i, j int) bool { return started[i] > started[j] })
	return started[0]
}

// ---- the routes

func (b *appBroker) devToolRoutes(mux *http.ServeMux, builder func(http.ResponseWriter, *http.Request) (appPersonKey, bool),
	reply func(http.ResponseWriter, int, any), failure func(http.ResponseWriter, int, error)) {
	mux.HandleFunc("GET "+appDevPagePrefix+"api/search", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := builder(w, r); !ok {
			return
		}
		q := r.URL.Query()
		matches, more, err := appDevSearch(b.team, q.Get("workspace"), q.Get("q"), q.Get("case") != "", q.Get("regex") != "")
		if err != nil {
			failure(w, http.StatusBadRequest, err)
			return
		}
		reply(w, http.StatusOK, map[string]any{"matches": matches, "more": more})
	})
	mux.HandleFunc("GET "+appDevPagePrefix+"api/checks", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := builder(w, r); !ok {
			return
		}
		q := r.URL.Query()
		checks, err := appDevChecksFor(b.team, q.Get("workspace"))
		if err != nil {
			failure(w, http.StatusBadGateway, err)
			return
		}
		after, _ := strconv.ParseInt(q.Get("after"), 10, 64)
		reply(w, http.StatusOK, map[string]any{"checks": checks, "run": appDevCheckView(b.team, q.Get("workspace"), after)})
	})
	mux.HandleFunc("POST "+appDevPagePrefix+"api/checks", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := builder(w, r); !ok {
			return
		}
		var request struct {
			Workspace string `json:"workspace"`
			Action    string `json:"action"`
			Name      string `json:"name"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&request); err != nil {
			failure(w, http.StatusBadRequest, err)
			return
		}
		var err error
		switch request.Action {
		case "run":
			err = appDevStartCheck(b.team, request.Workspace, request.Name)
		case "stop":
			err = appDevStopCheck(b.team, request.Workspace)
		default:
			err = fmt.Errorf("no %q to do; run or stop", request.Action)
		}
		if err != nil {
			failure(w, http.StatusBadRequest, err)
			return
		}
		reply(w, http.StatusOK, map[string]any{"run": appDevCheckView(b.team, request.Workspace, 0)})
	})
	mux.HandleFunc("GET "+appDevPagePrefix+"api/logs", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := builder(w, r); !ok {
			return
		}
		q := r.URL.Query()
		view, err := appDevLogs(b.team, q.Get("slot"), q.Get("source"), q.Get("after"))
		if err != nil {
			failure(w, http.StatusBadRequest, err)
			return
		}
		reply(w, http.StatusOK, view)
	})
}

// devPageFrames is the Content-Security-Policy's frame-src: the page's
// own (VS Code), and production's and Stage's addresses, for Preview.
func (b *appBroker) devPageFrames() string {
	frames := []string{"'self'"}
	for _, slot := range []string{appSlotProduction, appSlotPreview} {
		if state, ok := loadTeamSlot(b.team, slot); ok {
			if origin := appOrigin(state.URL); origin != "" {
				frames = append(frames, origin)
			}
		}
	}
	return "frame-src " + strings.Join(frames, " ")
}

// appOrigin is a URL's scheme and host, or "" for one that isn't http(s).
func appOrigin(raw string) string {
	scheme, rest, ok := strings.Cut(raw, "://")
	if !ok || (scheme != "http" && scheme != "https") {
		return ""
	}
	host, _, _ := strings.Cut(rest, "/")
	host, _, _ = strings.Cut(host, "?")
	if host == "" || strings.ContainsAny(host, " ;'\"") {
		return ""
	}
	return scheme + "://" + host
}
