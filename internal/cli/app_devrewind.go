package cli

// Rewind in the dev page's chat: Claude Code's /rewind, as a timeline of
// the conversation's messages beside it. Claude Code keeps a checkpoint of
// the files it edits at each message (~/.claude/file-history/<session>/,
// named in the transcript's file-history-snapshot and -delta records), and
// /rewind puts the code, the conversation, or both back to before one. It
// has no way in but its terminal, so hi presses its keys there as a person
// would, and reads the screen at each step: the list, the message chosen,
// the options; anything else, and it backs out. Codex keeps no checkpoints
// of the code, so the timeline is Claude Code's only.
//
// A transcript is a tree: a message after a rewind follows the one before
// the message rewound to, and what came after stays in the file. The chat
// shows the branch the newest record is on, and, until the next message,
// the one a rewind left.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"
	"sync"
	"time"
)

// appDevBackup is a file as a checkpoint has it: a copy in file-history,
// or nil when it wasn't there.
type appDevBackup struct {
	Name *string `json:"backupFileName"`
	Dir  string  `json:"realParentDir"`
}

// appDevPrompt is a message someone sent, which a rewind goes back to
// before.
type appDevPrompt struct {
	UUID   string
	Text   string
	At     time.Time
	Offset int64 // where its record starts in the transcript
}

// appDevRewindIndex is what hi knows of a transcript, read as it grows.
type appDevRewindIndex struct {
	mu      sync.Mutex
	size    int64
	parent  map[string]string
	leaf    string                             // the newest record of the conversation
	prompts []appDevPrompt                     // every branch's, in the file's order
	saved   map[string]map[string]appDevBackup // by message: each file as it was then
	first   map[string]appDevBackup            // each file as it was when first tracked
	// cut is a message a rewind took out of the conversation, which the
	// transcript says only with the next message; until the leaf moves.
	cut, cutLeaf string
}

// appDevRewindMaxFile is the largest file the timeline compares; a larger
// one shows no lines added or taken out.
const appDevRewindMaxFile = 4 << 20

var appDevRewindIndexes = struct {
	sync.Mutex
	byPath map[string]*appDevRewindIndex
}{byPath: map[string]*appDevRewindIndex{}}

// appDevRewindIndexFor is a transcript's index, brought up to date; it
// comes locked.
func appDevRewindIndexFor(home, host string) (*appDevRewindIndex, error) {
	appDevRewindIndexes.Lock()
	index := appDevRewindIndexes.byPath[host]
	if index == nil {
		if len(appDevRewindIndexes.byPath) >= 32 {
			clear(appDevRewindIndexes.byPath)
		}
		index = &appDevRewindIndex{}
		appDevRewindIndexes.byPath[host] = index
	}
	appDevRewindIndexes.Unlock()
	index.mu.Lock()
	if err := index.update(home, host); err != nil {
		index.mu.Unlock()
		return nil, err
	}
	return index, nil
}

func (x *appDevRewindIndex) update(home, host string) error {
	f, err := boxedOpenFile(home, host)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if x.parent == nil || info.Size() < x.size {
		// New, or written again: read from the start (the lock stays).
		x.size, x.leaf, x.prompts, x.cut, x.cutLeaf = 0, "", nil, "", ""
		x.parent, x.saved, x.first = map[string]string{}, map[string]map[string]appDevBackup{}, map[string]appDevBackup{}
	}
	if _, err := f.Seek(x.size, 0); err != nil {
		return err
	}
	reader := bufio.NewReaderSize(f, 1<<20)
	for {
		line, n, err := appReadBoxLine(reader)
		if err != nil {
			return nil // a line still being written waits for the next read
		}
		if line != nil {
			x.add(bytes.TrimSpace(line), x.size)
		}
		x.size += n
	}
}

func (x *appDevRewindIndex) add(line []byte, offset int64) {
	var r struct {
		Type      string `json:"type"`
		UUID      string `json:"uuid"`
		Parent    string `json:"parentUuid"`
		Logical   string `json:"logicalParentUuid"` // across a compaction
		Sidechain bool   `json:"isSidechain"`
		Snapshot  *struct {
			MessageID string                  `json:"messageId"`
			Files     map[string]appDevBackup `json:"trackedFileBackups"`
		} `json:"snapshot"`
		For       string        `json:"snapshotMessageId"`
		Path      string        `json:"trackingPath"`
		Backup    *appDevBackup `json:"backup"`
		Timestamp time.Time     `json:"timestamp"`
	}
	if json.Unmarshal(line, &r) != nil || r.Sidechain {
		return
	}
	saved := func(id string) map[string]appDevBackup {
		if x.saved[id] == nil {
			x.saved[id] = map[string]appDevBackup{}
		}
		return x.saved[id]
	}
	switch r.Type {
	case "file-history-snapshot":
		if r.Snapshot != nil && r.Snapshot.MessageID != "" {
			files := saved(r.Snapshot.MessageID)
			for file, backup := range r.Snapshot.Files {
				files[file] = backup
				x.track(file, backup)
			}
		}
	case "file-history-delta":
		// A file's first edit in a turn: as it was at the turn's message.
		if r.For != "" && r.Path != "" && r.Backup != nil {
			if files := saved(r.For); files != nil {
				if _, ok := files[r.Path]; !ok {
					files[r.Path] = *r.Backup
				}
			}
			x.track(r.Path, *r.Backup)
		}
	case "user", "assistant", "system", "attachment":
		if r.UUID == "" {
			return
		}
		x.parent[r.UUID] = firstNonEmpty(r.Parent, r.Logical)
		x.leaf = r.UUID
		if r.Type != "user" {
			return
		}
		if entries := appDevChatEntries(line); len(entries) > 0 && entries[0].T == "user" {
			x.prompts = append(x.prompts, appDevPrompt{UUID: r.UUID, Text: entries[0].Text, At: r.Timestamp, Offset: offset})
		}
	}
}

func (x *appDevRewindIndex) track(file string, backup appDevBackup) {
	if _, ok := x.first[file]; !ok {
		x.first[file] = backup
	}
}

// active is the conversation's messages: those on the newest record's
// branch, before the message a rewind took out.
func (x *appDevRewindIndex) active() []appDevPrompt {
	on := map[string]bool{}
	for id := x.leaf; id != "" && !on[id]; id = x.parent[id] {
		on[id] = true
	}
	var list []appDevPrompt
	for _, prompt := range x.prompts {
		if on[prompt.UUID] {
			list = append(list, prompt)
		}
	}
	if x.cut != "" && x.leaf != x.cutLeaf {
		x.cut = "" // the next message says it now
	}
	for i, prompt := range list {
		if prompt.UUID == x.cut {
			return list[:i]
		}
	}
	return list
}

// appDevTurns says which messages the page has, as their count and the
// last: a branch the page's isn't the start of is drawn again.
func appDevTurns(active []appDevPrompt) string {
	if len(active) == 0 {
		return "0"
	}
	return fmt.Sprintf("%d:%s", len(active), active[len(active)-1].UUID)
}

// appDevTurnsFollow says the page's messages are still the conversation's
// start.
func appDevTurnsFollow(known string, active []appDevPrompt) bool {
	count, last, _ := strings.Cut(known, ":")
	n := 0
	fmt.Sscan(count, &n)
	if n <= 0 {
		return true // none yet, or not a count the page sends
	}
	return n <= len(active) && active[n-1].UUID == last
}

// appDevFileChange is what a rewind point's code is to the next: a file,
// and its lines added and taken out.
type appDevFileChange struct {
	Path    string `json:"path"`
	Added   int    `json:"added"`
	Removed int    `json:"removed"`
}

// appDevRewindPoint is a message in the timeline.
type appDevRewindPoint struct {
	UUID string    `json:"uuid"`
	Text string    `json:"text"` // its first line
	At   time.Time `json:"at,omitzero"`
	// Changes are the code's from this message to the next, or to now for
	// the last, as Claude Code's /rewind lists them.
	Changes []appDevFileChange `json:"changes,omitempty"`
	// Restore is what restoring the code to before it changes now.
	Restore []appDevFileChange `json:"restore,omitempty"`
	// Code is whether Claude Code has a checkpoint of the code there.
	Code bool `json:"code"`
}

// appDevRewindView is the timeline.
type appDevRewindView struct {
	Session string              `json:"session,omitempty"`
	Points  []appDevRewindPoint `json:"points"`
	Turns   string              `json:"turns"`
	Note    string              `json:"note,omitempty"`
}

// points is the timeline of a transcript in the box: each message, with
// what the code did after it.
func (x *appDevRewindIndex) points(team, session string) []appDevRewindPoint {
	active := x.active()
	history := path.Join("/box/home/.claude/file-history", strings.TrimSuffix(path.Base(session), ".jsonl"))
	read := func(file string, backup appDevBackup) (string, bool) {
		var where string
		if backup.Name == nil {
			return "", false
		}
		where = path.Join(history, *backup.Name)
		base, host, ok := appDevBoxBase(team, where)
		if !ok {
			return "", false
		}
		data, err := boxedReadFileLimit(base, host, appDevRewindMaxFile)
		return string(data), err == nil
	}
	now := func(file string, backup appDevBackup) (string, bool) {
		where := file
		if !path.IsAbs(where) {
			where = path.Join(backup.Dir, file)
		}
		base, host, ok := appDevBoxBase(team, where)
		if !ok {
			return "", false
		}
		data, err := boxedReadFileLimit(base, host, appDevRewindMaxFile)
		return string(data), err == nil
	}
	// at is every tracked file as it was at a message: its checkpoint, or
	// for a file first tracked later, as it was then.
	at := func(id string) map[string]appDevBackup {
		files := map[string]appDevBackup{}
		for file, backup := range x.first {
			files[file] = backup
		}
		for file, backup := range x.saved[id] {
			files[file] = backup
		}
		return files
	}
	contents := map[string]string{} // backup names read once
	content := func(file string, backup appDevBackup) (string, bool) {
		if backup.Name == nil {
			return "", false
		}
		if text, ok := contents[*backup.Name]; ok {
			return text, true
		}
		text, ok := read(file, backup)
		if ok {
			contents[*backup.Name] = text
		}
		return text, ok
	}
	disk := map[string][2]any{}
	current := func(file string, backup appDevBackup) (string, bool) {
		if got, ok := disk[file]; ok {
			return got[0].(string), got[1].(bool)
		}
		text, ok := now(file, backup)
		disk[file] = [2]any{text, ok}
		return text, ok
	}
	changes := func(from, to func(string, appDevBackup) (string, bool), files map[string]appDevBackup, next map[string]appDevBackup) []appDevFileChange {
		var list []appDevFileChange
		for _, file := range sortedKeys(files) {
			a, hadA := from(file, files[file])
			var b string
			var hadB bool
			if next != nil {
				b, hadB = to(file, next[file])
			} else {
				b, hadB = to(file, files[file])
			}
			if hadA == hadB && a == b {
				continue
			}
			added, removed := appDevLineChanges(a, b)
			list = append(list, appDevFileChange{Path: file, Added: added, Removed: removed})
		}
		return list
	}
	var points []appDevRewindPoint
	for i, prompt := range active {
		files := at(prompt.UUID)
		point := appDevRewindPoint{UUID: prompt.UUID, Text: appDevFirstLine(prompt.Text), At: prompt.At, Code: x.saved[prompt.UUID] != nil}
		if i+1 < len(active) {
			point.Changes = changes(content, content, files, at(active[i+1].UUID))
		} else {
			point.Changes = changes(content, current, files, nil)
		}
		point.Restore = changes(current, content, files, nil) // from now back to the checkpoint
		points = append(points, point)
	}
	return points
}

func appDevFirstLine(text string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(text), "\n")
	if len(line) > 300 {
		line = appDevClip(line, 300)
	}
	return line
}

// appDevLineChanges is how many lines b adds to a and takes out of it:
// a longest common run of lines, after what they start and end with.
func appDevLineChanges(a, b string) (added, removed int) {
	split := func(s string) []string {
		lines := strings.SplitAfter(s, "\n")
		if lines[len(lines)-1] == "" {
			lines = lines[:len(lines)-1]
		}
		return lines
	}
	x, y := split(a), split(b)
	for len(x) > 0 && len(y) > 0 && x[0] == y[0] {
		x, y = x[1:], y[1:]
	}
	for len(x) > 0 && len(y) > 0 && x[len(x)-1] == y[len(y)-1] {
		x, y = x[:len(x)-1], y[:len(y)-1]
	}
	if len(x)*len(y) > 4_000_000 {
		// Too long to compare line by line: the lines either has the other hasn't.
		count := map[string]int{}
		for _, line := range x {
			count[line]++
		}
		common := 0
		for _, line := range y {
			if count[line] > 0 {
				count[line]--
				common++
			}
		}
		return len(y) - common, len(x) - common
	}
	row := make([]int, len(y)+1)
	for i := range x {
		diagonal := 0
		for j := range y {
			above := row[j+1]
			if x[i] == y[j] {
				row[j+1] = diagonal + 1
			} else if row[j] > row[j+1] {
				row[j+1] = row[j]
			}
			diagonal = above
		}
	}
	common := row[len(y)]
	return len(y) - common, len(x) - common
}

// devChatPoints is a workspace's timeline: its Claude Code session's
// messages, newest last.
func (b *appBroker) devChatPoints(workspace string) (appDevRewindView, error) {
	view := appDevRewindView{Points: []appDevRewindPoint{}}
	agent, session, err := devChatAgent(b.team, workspace)
	if err != nil {
		return view, err
	}
	if agent.Running && agent.Agent != "claude" {
		view.Note = "Rewind is Claude Code's: " + agent.Agent + " keeps no checkpoints of the code."
		return view, nil
	}
	if session == "" || !strings.HasPrefix(path.Clean(session), "/box/home/.claude/projects/") {
		return view, nil
	}
	host, ok := appDevHostPath(b.team, session)
	if !ok {
		return view, nil
	}
	index, err := appDevRewindIndexFor(appDevHome(b.team), host)
	if err != nil {
		return view, err
	}
	defer index.mu.Unlock()
	view.Session = session
	view.Points = index.points(b.team, session)
	view.Turns = appDevTurns(index.active())
	return view, nil
}

// appDevRewindModes are what a rewind restores, as Claude Code's
// options say them.
var appDevRewindModes = map[string]string{
	"both":         "Restore code and conversation",
	"conversation": "Restore conversation",
	"code":         "Restore code",
}

const (
	appDevRewindTitle   = "Restore the code and/or conversation to the point before"
	appDevRewindConfirm = "Confirm you want to restore"
)

var appDevRewindOption = regexp.MustCompile(`^\s*(?:❯\s*)?(?:[↑↓]\s*)?(\d)\.\s+(.+?)\s*$`)

// appDevRewindWait is how long hi waits for Claude Code's screen to
// change after a key; tests shorten it.
var appDevRewindWait = 400 * time.Millisecond

var appDevRewinding sync.Map // workspace -> a rewind under way

// devChatRewind rewinds a workspace's Claude Code session to before a
// message: the code, the conversation, or both, by its /rewind. It says
// the message's text, which a rewind of the conversation gives back to
// send again.
func (b *appBroker) devChatRewind(workspace, uuid, mode string) (string, error) {
	label := appDevRewindModes[mode]
	if label == "" {
		return "", fmt.Errorf("a rewind restores both, the conversation, or the code, not %q", mode)
	}
	if _, busy := appDevRewinding.LoadOrStore(b.team+" "+workspace, true); busy {
		return "", errors.New("a rewind is under way here already")
	}
	defer appDevRewinding.Delete(b.team + " " + workspace)
	agent, session, err := devChatAgent(b.team, workspace)
	switch {
	case err != nil:
		return "", err
	case !agent.Running || agent.Agent != "claude":
		return "", errors.New("rewind is for a Claude Code session that's running; resume it first")
	case agent.Status == "working" || agent.Status == "blocked":
		return "", errors.New("Claude is at work: stop it first, then rewind")
	case session == "":
		return "", errors.New("hi can't find this session's transcript")
	}
	host, ok := appDevHostPath(b.team, session)
	if !ok {
		return "", fmt.Errorf("%s isn't in the dev box", session)
	}
	index, err := appDevRewindIndexFor(appDevHome(b.team), host)
	if err != nil {
		return "", err
	}
	active := index.active()
	leaf := index.leaf
	index.mu.Unlock()
	at := -1
	for i, prompt := range active {
		if prompt.UUID == uuid {
			at = i
		}
	}
	if at < 0 {
		return "", errors.New("that message isn't in the conversation now; the timeline has changed")
	}
	target := active[at]
	keys := func(keys ...string) error {
		for len(keys) > 0 {
			n := min(16, len(keys))
			if err := appDevHerdrDo(b.team, append([]string{"agent", "send-keys", agent.Pane}, keys[:n]...)...); err != nil {
				return err
			}
			keys = keys[n:]
		}
		return nil
	}
	screen := func(want func(string) bool) (string, bool) {
		var text string
		for try := 0; try < 8; try++ {
			time.Sleep(appDevRewindWait)
			text, _, _ = devChatScreen(b.team, agent.Pane)
			if want(text) {
				return text, true
			}
		}
		return text, false
	}
	has := func(s string) func(string) bool { return func(text string) bool { return strings.Contains(text, s) } }
	backOut := func(why string) (string, error) {
		keys("esc")
		if text, _ := screen(func(string) bool { return true }); strings.Contains(text, appDevRewindTitle) || strings.Contains(text, appDevRewindConfirm) {
			keys("esc")
		}
		return "", errors.New(why + " Nothing was rewound; /rewind in the terminal does it by hand.")
	}

	// Esc Esc opens the list from an empty prompt; with something typed,
	// the first two clear it.
	if err := keys("esc", "esc"); err != nil {
		return "", err
	}
	if _, open := screen(has(appDevRewindTitle)); !open {
		keys("esc", "esc")
		if _, open := screen(has(appDevRewindTitle)); !open {
			return backOut("Claude Code didn't open its rewind list.")
		}
	}
	// The list ends with "(current)", chosen: the message is as many up.
	if err := keys(slicesRepeat("up", len(active)-at)...); err != nil {
		return backOut("hi couldn't move in the list.")
	}
	want := appDevRewindKey(target.Text)
	text, chosen := screen(func(text string) bool { return appDevRewindKey(appDevRewindChosen(text)) != "" })
	got := appDevRewindKey(appDevRewindChosen(text))
	if !chosen || got == "" || !(strings.HasPrefix(want, got) || strings.HasPrefix(got, want)) || len(got) < min(12, len(want)) {
		return backOut("Claude Code's list didn't show that message where the chat has it.")
	}
	if err := keys("enter"); err != nil {
		return backOut("hi couldn't choose the message.")
	}
	text, confirm := screen(has(appDevRewindConfirm))
	if !confirm {
		return backOut("Claude Code didn't ask to confirm.")
	}
	option := ""
	for _, line := range strings.Split(text, "\n") {
		if m := appDevRewindOption.FindStringSubmatch(line); m != nil && m[2] == label {
			option = m[1]
		}
	}
	if option == "" {
		return backOut("Claude Code can't " + strings.ToLower(label[:1]) + label[1:] + " to there.")
	}
	if err := keys(option); err != nil {
		return backOut("hi couldn't choose " + label + ".")
	}
	if _, done := screen(func(text string) bool { return !strings.Contains(text, appDevRewindConfirm) }); !done {
		return backOut("Claude Code didn't finish the rewind.")
	}
	if mode == "code" {
		return "", nil
	}
	// The conversation's rewind puts the message back in Claude Code's
	// prompt; it goes to the chat's box instead, so a message sent from the
	// chat isn't typed after it. Esc Esc clears it.
	index, err = appDevRewindIndexFor(appDevHome(b.team), host)
	if err == nil {
		if index.leaf == leaf {
			index.cut, index.cutLeaf = target.UUID, leaf
		}
		index.mu.Unlock()
	}
	keys("esc", "esc")
	if text, _ := screen(func(string) bool { return true }); strings.Contains(text, appDevRewindTitle) {
		keys("esc") // the prompt was empty after all: that opened the list
	}
	return target.Text, nil
}

func slicesRepeat(s string, n int) []string {
	list := make([]string, n)
	for i := range list {
		list[i] = s
	}
	return list
}

// appDevRewindChosen is the message chosen in Claude Code's rewind list,
// as it shows it: the line with ❯, below the list's title.
func appDevRewindChosen(screen string) string {
	lines := strings.Split(screen, "\n")
	start := -1
	for i, line := range lines {
		if strings.Contains(line, appDevRewindTitle) {
			start = i
		}
	}
	if start < 0 {
		return ""
	}
	for _, line := range lines[start+1:] {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "❯"); ok {
			return strings.TrimSpace(rest)
		}
	}
	return ""
}

// appDevRewindKey is a message's first line to compare with the list's,
// which cuts it with … at the terminal's edge: its words, in one line.
func appDevRewindKey(text string) string {
	line := appDevFirstLine(text)
	line = strings.TrimSuffix(strings.TrimSpace(line), "…")
	if line == "(current)" {
		return ""
	}
	return strings.Join(strings.Fields(line), " ")
}

// appDevChatBranch is which of a transcript's records the chat shows
// (nil: all), the conversation's turns, and whether the page's turns are
// still its start.
func appDevChatBranch(home, host, known string) (func(offset int64) bool, string, bool) {
	index, err := appDevRewindIndexFor(home, host)
	if err != nil {
		return nil, "", true
	}
	defer index.mu.Unlock()
	active := index.active()
	turns, follows := appDevTurns(active), appDevTurnsFollow(known, active)
	if len(active) == len(index.prompts) {
		return nil, turns, follows
	}
	on := map[string]bool{"": true}
	for _, prompt := range active {
		on[prompt.UUID] = true
	}
	prompts := append([]appDevPrompt(nil), index.prompts...)
	return func(offset int64) bool {
		turn := ""
		for _, prompt := range prompts {
			if prompt.Offset > offset {
				break
			}
			turn = prompt.UUID
		}
		return on[turn]
	}, turns, follows
}
