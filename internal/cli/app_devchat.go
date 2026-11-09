package cli

// The dev page's chat: a Claude Code session in the dev box, read from its
// transcript (~/.claude/projects/<the worktree>/<session>.jsonl, which
// Claude Code writes as it goes) and rendered as a conversation, beside
// the same session in the terminal. What an owner writes goes to the agent
// through herdr, as if typed, so the chat and the terminal stay one
// session.

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

// appDevChatEntry is one thing in a conversation, as the page draws it.
type appDevChatEntry struct {
	T       string    `json:"t"`                 // user, text, thinking, tool, result, title, note
	Text    string    `json:"text,omitempty"`    // what was said, or a tool's result
	ID      string    `json:"id,omitempty"`      // a tool call's, to put its result with it
	Name    string    `json:"name,omitempty"`    // the tool
	Summary string    `json:"summary,omitempty"` // the tool call in a line
	Detail  string    `json:"detail,omitempty"`  // the tool call's input
	Error   bool      `json:"error,omitempty"`   // a result that failed
	At      time.Time `json:"at,omitzero"`
	UUID    string    `json:"uuid,omitempty"` // a message's, which the rewind timeline names
}

// appDevChatAgent is the agent in a workspace, as herdr sees it.
type appDevChatAgent struct {
	Pane    string `json:"pane,omitempty"`
	Agent   string `json:"agent,omitempty"`  // claude, codex, ...
	Status  string `json:"status,omitempty"` // working, idle, blocked, done
	Running bool   `json:"running"`
}

// appDevChatView is what the page asks for: the session, its entries
// after the offset it has, and the agent.
type appDevChatView struct {
	Session string            `json:"session,omitempty"` // the transcript, in the box
	ID      string            `json:"id,omitempty"`      // Claude Code's session id
	Offset  int64             `json:"offset"`            // where the page reads from next
	Reset   bool              `json:"reset,omitempty"`   // a new session, or one that shrank: start again
	Hidden  int               `json:"hidden,omitempty"`  // earlier entries left out
	Entries []appDevChatEntry `json:"entries"`
	Agent   appDevChatAgent   `json:"agent"`
	Screen  string            `json:"screen,omitempty"` // the terminal, when the agent waits for an answer
	Ask     *appDevAsk        `json:"ask,omitempty"`    // what it asks there, as options to tap
	// Activity is Claude Code's own line while it works, from the
	// terminal: "Crafting… (7s · ↓ 556 tokens · thought for 4s)".
	Activity string `json:"activity,omitempty"`
	Note     string `json:"note,omitempty"`
	// Turns are the conversation's messages the page has (appDevTurns):
	// after a rewind, a branch they aren't the start of is drawn again.
	Turns string `json:"turns,omitempty"`
	// Model and Effort are what Claude Code answers with now, and Choose
	// says the person may pick them: the app's admin lets them
	// (hi app models), and Claude Code runs here.
	Model  string `json:"model,omitempty"`
	Effort string `json:"effort,omitempty"`
	Choose bool   `json:"choose,omitempty"`
}

const (
	appDevChatFirst     = 400      // entries a first load sends
	appDevChatResultMax = 6000     // a tool result's text
	appDevChatInputMax  = 4000     // a tool call's input
	appDevChatTextMax   = 64 << 10 // what an owner sends
	appDevChatFilesMax  = 20 << 20 // the files with a message, together
)

// appDevChatFiles reads the files the page sends with a message: a name,
// and the file in base64.
func appDevChatFiles(body map[string]any) ([]appAttachment, error) {
	list, _ := body["files"].([]any)
	var files []appAttachment
	total := 0
	for _, item := range list {
		file, _ := item.(map[string]any)
		name, _ := file["name"].(string)
		data, _ := file["data"].(string)
		raw, err := base64.StdEncoding.DecodeString(data)
		if err != nil {
			return nil, fmt.Errorf("couldn't read %s: %w", name, err)
		}
		total += len(raw)
		if total > appDevChatFilesMax {
			return nil, fmt.Errorf("the files come to more than %s", formatDataSize(appDevChatFilesMax))
		}
		files = append(files, appAttachment{Name: name, Data: raw})
	}
	return files, nil
}

// appDevSaveChatFiles puts a message's files in the box's home,
// ~/hi/chat/<workspace>, and says where they are in the box, for the
// agent to read them.
func appDevSaveChatFiles(team, workspace string, files []appAttachment) ([]string, error) {
	home, dir := appDevHome(team), appDevResultsDir(team, "chat", workspace)
	if len(files) > 0 {
		if err := boxedMkdirAll(home, dir, 0o700); err != nil {
			return nil, err
		}
	}
	var paths []string
	for _, file := range files {
		// One word, so the path reads as one in the message.
		name := strings.Join(strings.Fields(filepath.Base(strings.TrimSpace(file.Name))), "-")
		name = appUniqueFileName(dir, strings.TrimLeft(name, "."))
		// A new file, never one there already: a name the box took, with
		// a link or a file, moves on to the next.
		ext := filepath.Ext(name)
		stem := strings.TrimSuffix(name, ext)
		err := boxedCreateFile(home, filepath.Join(dir, name), file.Data, 0o600)
		for i := 2; errors.Is(err, fs.ErrExist) && i < 100; i++ {
			name = fmt.Sprintf("%s-%d%s", stem, i, ext)
			err = boxedCreateFile(home, filepath.Join(dir, name), file.Data, 0o600)
		}
		if err != nil {
			return nil, err
		}
		paths = append(paths, "/box/home/hi/chat/"+workspace+"/"+name)
	}
	return paths, nil
}

// appDevClaudeDir is Claude Code's folder of transcripts for a working
// directory: its path with every character but letters and digits as -.
func appDevClaudeDir(cwd string) string {
	return "/box/home/.claude/projects/" + regexp.MustCompile(`[^A-Za-z0-9]`).ReplaceAllString(cwd, "-")
}

// devChatAgent finds the agent in a workspace, with its session's
// transcript when herdr knows it.
func devChatAgent(team, workspace string) (appDevChatAgent, string, error) {
	var panes struct {
		Panes []struct {
			Pane    string `json:"pane_id"`
			Agent   string `json:"agent"`
			Status  string `json:"agent_status"`
			CWD     string `json:"cwd"`
			Session *struct {
				Kind  string `json:"kind"`
				Value string `json:"value"`
			} `json:"agent_session"`
		} `json:"panes"`
	}
	if err := appDevHerdr(team, &panes, "pane", "list", "--workspace", workspace); err != nil {
		return appDevChatAgent{}, "", err
	}
	agent := appDevChatAgent{}
	session := ""
	for _, pane := range panes.Panes {
		if agent.Pane == "" {
			agent.Pane = pane.Pane // the workspace's first pane, for a resume
		}
		if pane.Agent == "" {
			continue
		}
		agent = appDevChatAgent{Pane: pane.Pane, Agent: pane.Agent, Status: pane.Status, Running: true}
		if pane.Session != nil && strings.HasSuffix(pane.Session.Value, ".jsonl") {
			session = pane.Session.Value
		} else if pane.Session != nil && pane.Session.Value != "" && pane.CWD != "" {
			session = appDevClaudeDir(pane.CWD) + "/" + pane.Session.Value + ".jsonl"
		}
		break
	}
	return agent, session, nil
}

// appDevLatestSession is the newest transcript of a working directory,
// for a workspace whose agent has stopped.
func appDevLatestSession(team, cwd string) string {
	dir := appDevClaudeDir(cwd)
	host, ok := appDevHostPath(team, dir)
	if !ok {
		return ""
	}
	entries, _ := os.ReadDir(host)
	best, newest := "", time.Time{}
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil || !strings.HasSuffix(entry.Name(), ".jsonl") || !info.Mode().IsRegular() {
			continue
		}
		if info.ModTime().After(newest) {
			best, newest = dir+"/"+entry.Name(), info.ModTime()
		}
	}
	return best
}

var (
	appDevReminder     = regexp.MustCompile(`(?s)<system-reminder>.*?</system-reminder>`)
	appDevSlashCommand = regexp.MustCompile(`(?s)<command-name>(.*?)</command-name>.*?(?:<command-args>(.*?)</command-args>)?`)
	appDevTaskSummary  = regexp.MustCompile(`(?s)<summary>(.*?)</summary>`)
	appDevLocalTags    = regexp.MustCompile(`(?s)</?(local-command-stdout|local-command-stderr|local-command-caveat|command-message|command-name|command-args|bash-input|bash-stdout|bash-stderr)>`)
)

// appDevChatText is a message's text as people read it: without hi's and
// Claude Code's reminders, and a slash command as itself.
func appDevChatText(text string) (string, string) {
	text = strings.TrimSpace(appDevReminder.ReplaceAllString(text, ""))
	if m := appDevSlashCommand.FindStringSubmatch(text); m != nil && strings.HasPrefix(text, "<command") {
		return "note", strings.TrimSpace(m[1] + " " + m[2])
	}
	if strings.Contains(text, "<local-command-caveat>") {
		return "", ""
	}
	if strings.HasPrefix(text, "<local-command-std") || strings.HasPrefix(text, "<bash-") {
		return "note", strings.TrimSpace(appDevLocalTags.ReplaceAllString(text, ""))
	}
	if strings.HasPrefix(text, "<task-notification>") {
		// Claude Code's word that a command it left running is done, in the
		// person's turn: a note, by what its summary says.
		if m := appDevTaskSummary.FindStringSubmatch(text); m != nil {
			return "note", html.UnescapeString(strings.TrimSpace(m[1]))
		}
		return "note", "A background task finished"
	}
	if strings.HasPrefix(text, "[Request interrupted by user") {
		return "note", "Interrupted"
	}
	return "user", text
}

func appDevClip(text string, max int) string {
	if len(text) <= max {
		return text
	}
	cut := max
	for cut > 0 && !utf8RuneStart(text[cut]) {
		cut--
	}
	return text[:cut] + fmt.Sprintf("\n… (%s more)", formatDataSize(int64(len(text)-cut)))
}

func utf8RuneStart(b byte) bool { return b&0xC0 != 0x80 }

// appDevToolSummary is a tool call in a line: the command, the file, the
// pattern, as the page lists it.
func appDevToolSummary(name string, input map[string]any, cwd string) string {
	str := func(key string) string { s, _ := input[key].(string); return strings.TrimSpace(s) }
	short := func(p string) string {
		if cwd != "" && strings.HasPrefix(p, cwd+"/") {
			return strings.TrimPrefix(p, cwd+"/")
		}
		return p
	}
	switch name {
	case "Bash":
		if d := str("description"); d != "" {
			return d
		}
		line, _, _ := strings.Cut(str("command"), "\n")
		return line
	case "Read", "Edit", "Write", "MultiEdit", "NotebookEdit":
		return short(firstNonEmpty(str("file_path"), str("notebook_path")))
	case "Grep", "Glob":
		return firstNonEmpty(str("pattern"), str("glob"))
	case "WebFetch":
		return str("url")
	case "WebSearch":
		return str("query")
	case "Task", "Agent":
		return firstNonEmpty(str("description"), str("subagent_type"))
	case "TodoWrite":
		return "the to-do list"
	case "AskUserQuestion":
		if questions, _ := input["questions"].([]any); len(questions) > 0 {
			first, _ := questions[0].(map[string]any)
			if q, _ := first["question"].(string); q != "" {
				return q
			}
		}
	}
	keys := make([]string, 0, len(input))
	for key := range input {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if s, ok := input[key].(string); ok && s != "" {
			line, _, _ := strings.Cut(s, "\n")
			return line
		}
	}
	return ""
}

// appDevResultText is a tool result's text.
func appDevResultText(content any) string {
	switch c := content.(type) {
	case string:
		return c
	case []any:
		var parts []string
		for _, item := range c {
			block, _ := item.(map[string]any)
			switch block["type"] {
			case "text":
				s, _ := block["text"].(string)
				parts = append(parts, s)
			case "image":
				parts = append(parts, "[a picture]")
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

// appDevChatEntries are a transcript record's entries; most records
// (Claude Code's own bookkeeping) have none.
func appDevChatEntries(line []byte) []appDevChatEntry {
	var record struct {
		Type      string          `json:"type"`
		IsMeta    bool            `json:"isMeta"`
		Sidechain bool            `json:"isSidechain"`
		Compact   bool            `json:"isCompactSummary"`
		CWD       string          `json:"cwd"`
		Timestamp time.Time       `json:"timestamp"`
		Title     string          `json:"aiTitle"`
		Message   json.RawMessage `json:"message"`
		UUID      string          `json:"uuid"`
		// A message sent while Claude works is an attachment: a queued
		// command, which Claude reads with its next tool's result.
		Attachment struct {
			Type   string `json:"type"`
			Prompt string `json:"prompt"`
			Mode   string `json:"commandMode"`
		} `json:"attachment"`
	}
	if json.Unmarshal(line, &record) != nil || record.Sidechain {
		return nil
	}
	at := record.Timestamp
	switch record.Type {
	case "ai-title":
		if record.Title != "" {
			return []appDevChatEntry{{T: "title", Text: record.Title}}
		}
		return nil
	case "attachment":
		if a := record.Attachment; a.Type == "queued_command" && (a.Mode == "" || a.Mode == "prompt") {
			if kind, said := appDevChatText(a.Prompt); kind != "" && said != "" {
				return []appDevChatEntry{{T: kind, Text: said, At: at, UUID: record.UUID}}
			}
		}
		return nil
	case "user", "assistant":
	default:
		return nil
	}
	var message struct {
		Content json.RawMessage `json:"content"`
	}
	if json.Unmarshal(record.Message, &message) != nil {
		return nil
	}
	if record.Type == "user" && record.Compact {
		return []appDevChatEntry{{T: "note", Text: "The conversation was compacted", At: at}}
	}
	var text string
	if json.Unmarshal(message.Content, &text) == nil {
		if record.Type == "user" {
			if record.IsMeta {
				return nil
			}
			kind, said := appDevChatText(text)
			if kind == "" || said == "" {
				return nil
			}
			return []appDevChatEntry{{T: kind, Text: said, At: at, UUID: record.UUID}}
		}
		return []appDevChatEntry{{T: "text", Text: text, At: at}}
	}
	var blocks []map[string]any
	if json.Unmarshal(message.Content, &blocks) != nil {
		return nil
	}
	var entries []appDevChatEntry
	var said []string
	for _, block := range blocks {
		kind, _ := block["type"].(string)
		switch {
		case kind == "text" && record.Type == "assistant":
			if s, _ := block["text"].(string); strings.TrimSpace(s) != "" {
				entries = append(entries, appDevChatEntry{T: "text", Text: s, At: at})
			}
		case kind == "text":
			if s, _ := block["text"].(string); !record.IsMeta {
				said = append(said, s)
			}
		case kind == "image" && record.Type == "user":
			said = append(said, "[a picture]")
		case kind == "thinking":
			if s, _ := block["thinking"].(string); strings.TrimSpace(s) != "" {
				entries = append(entries, appDevChatEntry{T: "thinking", Text: appDevClip(s, appDevChatResultMax), At: at})
			}
		case kind == "tool_use":
			name, _ := block["name"].(string)
			id, _ := block["id"].(string)
			input, _ := block["input"].(map[string]any)
			detail, _ := json.MarshalIndent(input, "", "  ")
			if command, ok := input["command"].(string); ok && name == "Bash" {
				detail = []byte(command)
			}
			entries = append(entries, appDevChatEntry{T: "tool", ID: id, Name: name, Summary: appDevToolSummary(name, input, record.CWD),
				Detail: appDevClip(string(detail), appDevChatInputMax), At: at})
		case kind == "tool_result":
			id, _ := block["tool_use_id"].(string)
			failed, _ := block["is_error"].(bool)
			entries = append(entries, appDevChatEntry{T: "result", ID: id, Text: appDevClip(appDevResultText(block["content"]), appDevChatResultMax), Error: failed, At: at})
		}
	}
	if len(said) > 0 {
		if kind, text := appDevChatText(strings.Join(said, "\n")); kind != "" && text != "" {
			entries = append([]appDevChatEntry{{T: kind, Text: text, At: at, UUID: record.UUID}}, entries...)
		}
	}
	return entries
}

// readDevChat reads a transcript's entries from offset to its last whole
// line, and where to read from next; shown, when not nil, says which
// records (by where they start) are the conversation's.
func readDevChat(home, file string, offset int64, shown func(int64) bool) ([]appDevChatEntry, int64, bool, error) {
	f, err := boxedOpenFile(home, file)
	if err != nil {
		return nil, offset, false, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, offset, false, err
	}
	reset := false
	if offset < 0 || offset > info.Size() {
		offset, reset = 0, true
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return nil, offset, reset, err
	}
	reader := bufio.NewReaderSize(f, 1<<20)
	var entries []appDevChatEntry
	for {
		line, n, err := appReadBoxLine(reader)
		if err != nil {
			break // a line still being written waits for the next read
		}
		if line != nil && (shown == nil || shown(offset)) {
			entries = append(entries, appDevChatEntries(bytes.TrimSpace(line))...)
		}
		offset += n
	}
	return entries, offset, reset, nil
}

// devChat is the page's view of a workspace's session, from offset on.
func (b *appBroker) devChat(workspace, known, turns string, offset int64) (appDevChatView, error) {
	view, err := b.devChatLog(workspace, known, turns, offset)
	if err != nil {
		return view, err
	}
	// A question in the terminal isn't in the transcript until it's
	// answered, nor is what Claude is doing right now; a question comes
	// before a session, too, and from Codex.
	// herdr misses Claude Code's review of its answers, which has no "Esc
	// to cancel" under it, so Claude's screen is read whatever herdr says.
	if agent := view.Agent; agent.Running && (agent.Status == "blocked" || agent.Status == "working" || agent.Agent == "claude") {
		if text, ansi, err := devChatScreen(b.team, agent.Pane); err == nil {
			ask := appDevReadAsk(text, ansi)
			if agent.Status != "blocked" && appDevAskTabbed(ask) {
				view.Agent.Status = "blocked"
			}
			if view.Agent.Status == "blocked" {
				view.Screen = text
				view.Ask = ask
			} else if agent.Status == "working" && agent.Agent == "claude" {
				view.Activity = appDevActivity(text)
			}
		}
	}
	return view, nil
}

// devChatLog is the session's entries, from its transcript.
func (b *appBroker) devChatLog(workspace, known, turns string, offset int64) (appDevChatView, error) {
	agent, session, err := devChatAgent(b.team, workspace)
	if err != nil {
		return appDevChatView{}, err
	}
	view := appDevChatView{Agent: agent, Entries: []appDevChatEntry{}}
	if agent.Running && agent.Agent != "claude" {
		view.Note = "The chat shows Claude Code's sessions; " + agent.Agent + "'s is in the terminal."
		return view, nil
	}
	if session == "" {
		cwd := "/box/repo"
		if spaces, err := appDevSpacesCached(b.team); err == nil {
			for _, space := range spaces {
				if space.ID == workspace && space.Path != "" {
					cwd = space.Path
				}
			}
		}
		session = appDevLatestSession(b.team, cwd)
	}
	if session == "" {
		view.Note = "No Claude Code session here yet."
		return view, nil
	}
	if !strings.HasPrefix(path.Clean(session), "/box/home/.claude/projects/") {
		return view, fmt.Errorf("%s isn't a Claude Code transcript", session)
	}
	host, _ := appDevHostPath(b.team, session)
	view.Session, view.ID = session, strings.TrimSuffix(path.Base(session), ".jsonl")
	if session != known {
		offset, view.Reset = 0, true
	}
	shown, now, follows := appDevChatBranch(appDevHome(b.team), host, turns)
	if !follows {
		offset, view.Reset = 0, true // a rewind: the conversation's branch again
	}
	view.Turns = now
	entries, next, shrank, err := readDevChat(appDevHome(b.team), host, offset, shown)
	if err != nil {
		return view, err
	}
	view.Offset, view.Reset = next, view.Reset || shrank
	if offset == 0 && len(entries) > appDevChatFirst {
		// A long session: the last of it, from a turn's start.
		cut := len(entries) - appDevChatFirst
		for cut < len(entries) && entries[cut].T != "user" {
			cut++
		}
		if cut == len(entries) {
			cut = len(entries) - appDevChatFirst
		}
		view.Hidden, entries = cut, entries[cut:]
	}
	view.Entries = entries
	view.Model, view.Effort = b.devChatModel(view.ID, host)
	if config, err := loadTeam(b.team); err == nil {
		view.Choose = !config.LockModels && agent.Running
	}
	return view, nil
}

// appDevModels are the models the chat picks from, as Claude Code's
// /model takes them: each the latest of its kind.
var appDevModels = []string{"fable", "opus", "sonnet", "haiku"}

// appDevChoice is a model or effort picked in the chat, and when.
type appDevChoice struct {
	Model, Effort string
	At            time.Time
}

// devChatModel is the model and effort Claude Code answers with in a
// session: its last answer's, or what was picked since.
func (b *appBroker) devChatModel(session, host string) (model, effort string) {
	model, effort, at := appDevChatModel(host)
	b.devChoiceMu.Lock()
	defer b.devChoiceMu.Unlock()
	if choice, ok := b.devChoices[session]; ok {
		if choice.At.After(at) {
			model, effort = firstNonEmpty(choice.Model, model), firstNonEmpty(choice.Effort, effort)
		} else {
			delete(b.devChoices, session)
		}
	}
	return model, effort
}

// appDevChatModel reads the transcript's last answer for its model and
// effort.
func appDevChatModel(host string) (model, effort string, at time.Time) {
	file, err := os.Open(host)
	if err != nil {
		return "", "", at
	}
	defer file.Close()
	const tail = 256 << 10
	if info, err := file.Stat(); err == nil && info.Size() > tail {
		file.Seek(info.Size()-tail, io.SeekStart)
	}
	data, _ := io.ReadAll(file)
	lines := strings.Split(string(data), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if !strings.Contains(lines[i], `"type":"assistant"`) {
			continue
		}
		var entry struct {
			Type      string    `json:"type"`
			Timestamp time.Time `json:"timestamp"`
			Effort    string    `json:"effort"`
			Message   struct {
				Model string `json:"model"`
			} `json:"message"`
		}
		if json.Unmarshal([]byte(lines[i]), &entry) != nil || entry.Type != "assistant" || !strings.HasPrefix(entry.Message.Model, "claude-") {
			continue
		}
		return entry.Message.Model, entry.Effort, entry.Timestamp
	}
	return "", "", at
}

// devChatChoose picks the session's model or effort, with Claude Code's
// own /model and /effort, as someone would type them.
func (b *appBroker) devChatChoose(agent appDevChatAgent, session, model, effort string) error {
	config, err := loadTeam(b.team)
	if err != nil {
		return err
	}
	switch {
	case config.LockModels:
		return errors.New("the app's admin keeps the chat's model and effort as they are")
	case !agent.Running || agent.Agent != "claude":
		return errors.New("the model and effort are picked for Claude Code, and it isn't running here")
	case agent.Status == "working" || agent.Status == "blocked":
		return errors.New("Claude is busy: pick it when it's done")
	case model == "" && effort == "":
		return errors.New("a model or an effort")
	case model != "" && !slices.Contains(appDevModels, model):
		return fmt.Errorf("the model is %s, not %q", strings.Join(appDevModels, ", "), model)
	case effort != "" && !slices.Contains(agentEfforts, effort):
		return fmt.Errorf("the effort is %s, not %q", strings.Join(agentEfforts, ", "), effort)
	}
	if model != "" {
		if err := appDevHerdrDo(b.team, "agent", "prompt", agent.Pane, "/model "+model); err != nil {
			return err
		}
	}
	if effort != "" {
		if err := appDevHerdrDo(b.team, "agent", "prompt", agent.Pane, "/effort "+effort); err != nil {
			return err
		}
	}
	if session != "" {
		b.devChoiceMu.Lock()
		if b.devChoices == nil {
			b.devChoices = map[string]appDevChoice{}
		}
		choice := b.devChoices[session]
		if model != "" {
			choice.Model = "claude-" + model
		}
		if effort != "" {
			choice.Effort = effort
		}
		choice.At = time.Now()
		b.devChoices[session] = choice
		b.devChoiceMu.Unlock()
	}
	return nil
}

// devChatScreen is what an agent's terminal shows, as text and with its
// colors, which say what's selected where the text doesn't.
func devChatScreen(team, pane string) (text, ansi string, err error) {
	ansi, err = appDevBoxExec(team, "herdr", "pane", "read", pane, "--source", "visible", "--lines", "40", "--format", "ansi")
	text = appDevANSI.ReplaceAllString(strings.ReplaceAll(ansi, "\r", ""), "")
	return strings.TrimRight(text, "\n "), ansi, err
}

// appDevANSI is a terminal's escape sequence: a color, a cursor's move.
var appDevANSI = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|\x1b[()][0-9A-Za-z]`)

// appDevActivityLine is Claude Code's spinner line: a glyph, a word with
// an ellipsis, and what it has done so far in brackets.
var appDevActivityLine = regexp.MustCompile(`^\s*[^\sA-Za-z0-9]\s+([A-Z][A-Za-z'-]*…)\s*(\([^)]*\))?`)

// appDevActivity is the last spinner line on Claude Code's screen, without
// the "esc to interrupt" the chat says with its Stop button.
func appDevActivity(screen string) string {
	lines := strings.Split(screen, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		m := appDevActivityLine.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		detail := strings.Trim(m[2], "()")
		var kept []string
		for _, part := range strings.Split(detail, "·") {
			if part = strings.TrimSpace(part); part != "" && !strings.Contains(part, "esc to interrupt") {
				kept = append(kept, part)
			}
		}
		if len(kept) == 0 {
			return m[1]
		}
		return m[1] + " (" + strings.Join(kept, " · ") + ")"
	}
	return ""
}

// appDevHerdrDo runs herdr in the box for what it does, not what it says.
func appDevHerdrDo(team string, args ...string) error {
	if text, err := appDevBoxExec(team, append([]string{"herdr"}, args...)...); err != nil {
		return fmt.Errorf("herdr %s: %s", args[0]+" "+args[1], qFirstLine(strings.TrimSpace(text), err.Error()))
	}
	return nil
}

// appDevChatKeys are the keys the chat may press: an answer to a
// question in the terminal, or Esc to stop.
var appDevChatKeys = map[string]bool{"esc": true, "enter": true, "up": true, "down": true, "left": true, "right": true, "tab": true, "space": true,
	"1": true, "2": true, "3": true, "4": true, "5": true, "6": true, "7": true, "8": true, "9": true, "y": true, "n": true}

// devChatRoutes are the chat's: a session's entries, a message, keys, and
// resuming a session whose agent stopped. For the owners only.
func (b *appBroker) devChatRoutes(mux *http.ServeMux, builder func(http.ResponseWriter, *http.Request) (appPersonKey, bool),
	reply func(http.ResponseWriter, int, any), failure func(http.ResponseWriter, int, error)) {
	workspaceOK := regexp.MustCompile(`^w[0-9A-Za-z]{1,8}$`)
	mux.HandleFunc("GET "+appDevPagePrefix+"api/chat", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := builder(w, r); !ok {
			return
		}
		workspace := r.URL.Query().Get("workspace")
		if !workspaceOK.MatchString(workspace) {
			failure(w, http.StatusBadRequest, fmt.Errorf("%q isn't a workspace", workspace))
			return
		}
		offset, _ := strconv.ParseInt(r.URL.Query().Get("offset"), 10, 64)
		view, err := b.devChat(workspace, r.URL.Query().Get("session"), r.URL.Query().Get("turns"), offset)
		if err != nil {
			failure(w, http.StatusBadGateway, err)
			return
		}
		reply(w, http.StatusOK, view)
	})
	act := func(w http.ResponseWriter, r *http.Request, do func(agent appDevChatAgent, session string, body map[string]any) error) {
		if _, ok := builder(w, r); !ok {
			return
		}
		var body map[string]any
		if err := json.NewDecoder(io.LimitReader(r.Body, appDevChatTextMax+appDevChatFilesMax/3*4+64<<10)).Decode(&body); err != nil {
			failure(w, http.StatusBadRequest, err)
			return
		}
		workspace, _ := body["workspace"].(string)
		if !workspaceOK.MatchString(workspace) {
			failure(w, http.StatusBadRequest, fmt.Errorf("%q isn't a workspace", workspace))
			return
		}
		agent, session, err := devChatAgent(b.team, workspace)
		if err == nil {
			err = do(agent, session, body)
		}
		if err != nil {
			failure(w, http.StatusBadRequest, err)
			return
		}
		reply(w, http.StatusOK, map[string]string{})
	}
	mux.HandleFunc("POST "+appDevPagePrefix+"api/chat/send", func(w http.ResponseWriter, r *http.Request) {
		act(w, r, func(agent appDevChatAgent, _ string, body map[string]any) error {
			text, _ := body["text"].(string)
			text = strings.TrimSpace(text)
			files, err := appDevChatFiles(body)
			switch {
			case err != nil:
				return err
			case text == "" && len(files) == 0:
				return errors.New("nothing to send")
			case len(text) > appDevChatTextMax:
				return fmt.Errorf("the message is longer than %s", formatDataSize(appDevChatTextMax))
			case !agent.Running:
				return errors.New("no agent is running in this workspace; resume the session, or start one from the task")
			}
			paths, err := appDevSaveChatFiles(b.team, body["workspace"].(string), files)
			if err != nil {
				return err
			}
			if len(paths) > 0 {
				text = strings.TrimSpace(text + "\n\nAttached, in the box: " + strings.Join(paths, " "))
			}
			return appDevHerdrDo(b.team, "agent", "prompt", agent.Pane, text)
		})
	})
	// A picture pasted into the terminal: the box's clipboard is empty, so
	// the page puts the picture in the box and pastes its path, which
	// Claude Code takes as the picture.
	mux.HandleFunc("POST "+appDevPagePrefix+"api/term/files", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := builder(w, r); !ok {
			return
		}
		var body map[string]any
		if err := json.NewDecoder(io.LimitReader(r.Body, appDevChatFilesMax/3*4+64<<10)).Decode(&body); err != nil {
			failure(w, http.StatusBadRequest, err)
			return
		}
		files, err := appDevChatFiles(body)
		if err == nil && len(files) == 0 {
			err = errors.New("no file to put in the box")
		}
		var paths []string
		if err == nil {
			paths, err = appDevSaveChatFiles(b.team, "terminal", files)
		}
		if err != nil {
			failure(w, http.StatusBadRequest, err)
			return
		}
		reply(w, http.StatusOK, map[string]any{"paths": paths})
	})
	mux.HandleFunc("POST "+appDevPagePrefix+"api/chat/model", func(w http.ResponseWriter, r *http.Request) {
		act(w, r, func(agent appDevChatAgent, session string, body map[string]any) error {
			model, _ := body["model"].(string)
			effort, _ := body["effort"].(string)
			id, _ := body["session"].(string)
			return b.devChatChoose(agent, firstNonEmpty(id, path.Base(strings.TrimSuffix(session, ".jsonl"))), model, effort)
		})
	})
	mux.HandleFunc("POST "+appDevPagePrefix+"api/chat/keys", func(w http.ResponseWriter, r *http.Request) {
		act(w, r, func(agent appDevChatAgent, _ string, body map[string]any) error {
			list, _ := body["keys"].([]any)
			if !agent.Running || len(list) == 0 || len(list) > 4 {
				return errors.New("no agent to press keys for")
			}
			args := []string{"agent", "send-keys", agent.Pane}
			for _, item := range list {
				key, _ := item.(string)
				if !appDevChatKeys[key] {
					return fmt.Errorf("the chat doesn't press %q", key)
				}
				args = append(args, key)
			}
			return appDevHerdrDo(b.team, args...)
		})
	})
	mux.HandleFunc("POST "+appDevPagePrefix+"api/chat/answer", func(w http.ResponseWriter, r *http.Request) {
		act(w, r, func(agent appDevChatAgent, _ string, body map[string]any) error {
			var text []string
			list, _ := body["text"].([]any)
			for _, item := range list {
				line, _ := item.(string)
				text = append(text, line)
			}
			index, _ := body["option"].(float64)
			label, _ := body["label"].(string)
			typed, _ := body["typed"].(string)
			return b.answerDevAsk(agent, text, int(index), label, typed)
		})
	})
	mux.HandleFunc("GET "+appDevPagePrefix+"api/chat/points", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := builder(w, r); !ok {
			return
		}
		workspace := r.URL.Query().Get("workspace")
		if !workspaceOK.MatchString(workspace) {
			failure(w, http.StatusBadRequest, fmt.Errorf("%q isn't a workspace", workspace))
			return
		}
		view, err := b.devChatPoints(workspace)
		if err != nil {
			failure(w, http.StatusBadGateway, err)
			return
		}
		reply(w, http.StatusOK, view)
	})
	mux.HandleFunc("POST "+appDevPagePrefix+"api/chat/rewind", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := builder(w, r); !ok {
			return
		}
		var body struct{ Workspace, UUID, Mode string }
		if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&body); err != nil {
			failure(w, http.StatusBadRequest, err)
			return
		}
		if !workspaceOK.MatchString(body.Workspace) {
			failure(w, http.StatusBadRequest, fmt.Errorf("%q isn't a workspace", body.Workspace))
			return
		}
		text, err := b.devChatRewind(body.Workspace, body.UUID, body.Mode)
		if err != nil {
			failure(w, http.StatusConflict, err)
			return
		}
		reply(w, http.StatusOK, map[string]string{"text": text})
	})
	mux.HandleFunc("POST "+appDevPagePrefix+"api/chat/resume", func(w http.ResponseWriter, r *http.Request) {
		act(w, r, func(agent appDevChatAgent, _ string, body map[string]any) error {
			id, _ := body["session"].(string)
			switch {
			case agent.Running:
				return errors.New("an agent is running in this workspace already")
			case agent.Pane == "":
				return errors.New("the workspace has no pane to run Claude in")
			case !regexp.MustCompile(`^[0-9a-f-]{36}$`).MatchString(id):
				return fmt.Errorf("%q isn't a Claude Code session", id)
			}
			return appDevHerdrDo(b.team, "pane", "run", agent.Pane, "claude --resume "+id)
		})
	})
}
