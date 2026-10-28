package main

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
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// teamDevChatEntry is one thing in a conversation, as the page draws it.
type teamDevChatEntry struct {
	T       string    `json:"t"`                 // user, text, thinking, tool, result, title, note
	Text    string    `json:"text,omitempty"`    // what was said, or a tool's result
	ID      string    `json:"id,omitempty"`      // a tool call's, to put its result with it
	Name    string    `json:"name,omitempty"`    // the tool
	Summary string    `json:"summary,omitempty"` // the tool call in a line
	Detail  string    `json:"detail,omitempty"`  // the tool call's input
	Error   bool      `json:"error,omitempty"`   // a result that failed
	At      time.Time `json:"at,omitzero"`
}

// teamDevChatAgent is the agent in a workspace, as herdr sees it.
type teamDevChatAgent struct {
	Pane    string `json:"pane,omitempty"`
	Agent   string `json:"agent,omitempty"`  // claude, codex, ...
	Status  string `json:"status,omitempty"` // working, idle, blocked, done
	Running bool   `json:"running"`
}

// teamDevChatView is what the page asks for: the session, its entries
// after the offset it has, and the agent.
type teamDevChatView struct {
	Session string             `json:"session,omitempty"` // the transcript, in the box
	ID      string             `json:"id,omitempty"`      // Claude Code's session id
	Offset  int64              `json:"offset"`            // where the page reads from next
	Reset   bool               `json:"reset,omitempty"`   // a new session, or one that shrank: start again
	Hidden  int                `json:"hidden,omitempty"`  // earlier entries left out
	Entries []teamDevChatEntry `json:"entries"`
	Agent   teamDevChatAgent   `json:"agent"`
	Screen  string             `json:"screen,omitempty"` // the terminal, when the agent waits for an answer
	Ask     *teamDevAsk        `json:"ask,omitempty"`    // what it asks there, as options to tap
	// Activity is Claude Code's own line while it works, from the
	// terminal: "Crafting… (7s · ↓ 556 tokens · thought for 4s)".
	Activity string `json:"activity,omitempty"`
	Note     string `json:"note,omitempty"`
}

const (
	teamDevChatFirst     = 400      // entries a first load sends
	teamDevChatResultMax = 6000     // a tool result's text
	teamDevChatInputMax  = 4000     // a tool call's input
	teamDevChatTextMax   = 64 << 10 // what an owner sends
	teamDevChatFilesMax  = 20 << 20 // the files with a message, together
)

// teamDevChatFiles reads the files the page sends with a message: a name,
// and the file in base64.
func teamDevChatFiles(body map[string]any) ([]teamAttachment, error) {
	list, _ := body["files"].([]any)
	var files []teamAttachment
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
		if total > teamDevChatFilesMax {
			return nil, fmt.Errorf("the files come to more than %s", formatDataSize(teamDevChatFilesMax))
		}
		files = append(files, teamAttachment{Name: name, Data: raw})
	}
	return files, nil
}

// teamDevSaveChatFiles puts a message's files in the box's home,
// ~/hi/chat/<workspace>, and says where they are in the box, for the
// agent to read them.
func teamDevSaveChatFiles(team, workspace string, files []teamAttachment) ([]string, error) {
	dir := teamDevResultsDir(team, "chat", workspace)
	if len(files) > 0 {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, err
		}
	}
	var paths []string
	for _, file := range files {
		// One word, so the path reads as one in the message.
		name := strings.Join(strings.Fields(filepath.Base(strings.TrimSpace(file.Name))), "-")
		name = teamUniqueFileName(dir, strings.TrimLeft(name, "."))
		if err := os.WriteFile(filepath.Join(dir, name), file.Data, 0o600); err != nil {
			return nil, err
		}
		paths = append(paths, "/box/home/hi/chat/"+workspace+"/"+name)
	}
	return paths, nil
}

// teamDevClaudeDir is Claude Code's folder of transcripts for a working
// directory: its path with every character but letters and digits as -.
func teamDevClaudeDir(cwd string) string {
	return "/box/home/.claude/projects/" + regexp.MustCompile(`[^A-Za-z0-9]`).ReplaceAllString(cwd, "-")
}

// devChatAgent finds the agent in a workspace, with its session's
// transcript when herdr knows it.
func devChatAgent(team, workspace string) (teamDevChatAgent, string, error) {
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
	if err := teamDevHerdr(team, &panes, "pane", "list", "--workspace", workspace); err != nil {
		return teamDevChatAgent{}, "", err
	}
	agent := teamDevChatAgent{}
	session := ""
	for _, pane := range panes.Panes {
		if agent.Pane == "" {
			agent.Pane = pane.Pane // the workspace's first pane, for a resume
		}
		if pane.Agent == "" {
			continue
		}
		agent = teamDevChatAgent{Pane: pane.Pane, Agent: pane.Agent, Status: pane.Status, Running: true}
		if pane.Session != nil && strings.HasSuffix(pane.Session.Value, ".jsonl") {
			session = pane.Session.Value
		} else if pane.Session != nil && pane.Session.Value != "" && pane.CWD != "" {
			session = teamDevClaudeDir(pane.CWD) + "/" + pane.Session.Value + ".jsonl"
		}
		break
	}
	return agent, session, nil
}

// teamDevLatestSession is the newest transcript of a working directory,
// for a workspace whose agent has stopped.
func teamDevLatestSession(team, cwd string) string {
	dir := teamDevClaudeDir(cwd)
	host, ok := teamDevHostPath(team, dir)
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
	teamDevReminder     = regexp.MustCompile(`(?s)<system-reminder>.*?</system-reminder>`)
	teamDevSlashCommand = regexp.MustCompile(`(?s)<command-name>(.*?)</command-name>.*?(?:<command-args>(.*?)</command-args>)?`)
	teamDevTaskSummary  = regexp.MustCompile(`(?s)<summary>(.*?)</summary>`)
	teamDevLocalTags    = regexp.MustCompile(`(?s)</?(local-command-stdout|local-command-stderr|local-command-caveat|command-message|command-name|command-args|bash-input|bash-stdout|bash-stderr)>`)
)

// teamDevChatText is a message's text as people read it: without hi's and
// Claude Code's reminders, and a slash command as itself.
func teamDevChatText(text string) (string, string) {
	text = strings.TrimSpace(teamDevReminder.ReplaceAllString(text, ""))
	if m := teamDevSlashCommand.FindStringSubmatch(text); m != nil && strings.HasPrefix(text, "<command") {
		return "note", strings.TrimSpace(m[1] + " " + m[2])
	}
	if strings.Contains(text, "<local-command-caveat>") {
		return "", ""
	}
	if strings.HasPrefix(text, "<local-command-std") || strings.HasPrefix(text, "<bash-") {
		return "note", strings.TrimSpace(teamDevLocalTags.ReplaceAllString(text, ""))
	}
	if strings.HasPrefix(text, "<task-notification>") {
		// Claude Code's word that a command it left running is done, in the
		// person's turn: a note, by what its summary says.
		if m := teamDevTaskSummary.FindStringSubmatch(text); m != nil {
			return "note", html.UnescapeString(strings.TrimSpace(m[1]))
		}
		return "note", "A background task finished"
	}
	if strings.HasPrefix(text, "[Request interrupted by user") {
		return "note", "Interrupted"
	}
	return "user", text
}

func teamDevClip(text string, max int) string {
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

// teamDevToolSummary is a tool call in a line: the command, the file, the
// pattern, as the page lists it.
func teamDevToolSummary(name string, input map[string]any, cwd string) string {
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

// teamDevResultText is a tool result's text.
func teamDevResultText(content any) string {
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

// teamDevChatEntries are a transcript record's entries; most records
// (Claude Code's own bookkeeping) have none.
func teamDevChatEntries(line []byte) []teamDevChatEntry {
	var record struct {
		Type      string          `json:"type"`
		IsMeta    bool            `json:"isMeta"`
		Sidechain bool            `json:"isSidechain"`
		Compact   bool            `json:"isCompactSummary"`
		CWD       string          `json:"cwd"`
		Timestamp time.Time       `json:"timestamp"`
		Title     string          `json:"aiTitle"`
		Message   json.RawMessage `json:"message"`
	}
	if json.Unmarshal(line, &record) != nil || record.Sidechain {
		return nil
	}
	at := record.Timestamp
	switch record.Type {
	case "ai-title":
		if record.Title != "" {
			return []teamDevChatEntry{{T: "title", Text: record.Title}}
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
		return []teamDevChatEntry{{T: "note", Text: "The conversation was compacted", At: at}}
	}
	var text string
	if json.Unmarshal(message.Content, &text) == nil {
		if record.Type == "user" {
			if record.IsMeta {
				return nil
			}
			kind, said := teamDevChatText(text)
			if kind == "" || said == "" {
				return nil
			}
			return []teamDevChatEntry{{T: kind, Text: said, At: at}}
		}
		return []teamDevChatEntry{{T: "text", Text: text, At: at}}
	}
	var blocks []map[string]any
	if json.Unmarshal(message.Content, &blocks) != nil {
		return nil
	}
	var entries []teamDevChatEntry
	var said []string
	for _, block := range blocks {
		kind, _ := block["type"].(string)
		switch {
		case kind == "text" && record.Type == "assistant":
			if s, _ := block["text"].(string); strings.TrimSpace(s) != "" {
				entries = append(entries, teamDevChatEntry{T: "text", Text: s, At: at})
			}
		case kind == "text":
			if s, _ := block["text"].(string); !record.IsMeta {
				said = append(said, s)
			}
		case kind == "image" && record.Type == "user":
			said = append(said, "[a picture]")
		case kind == "thinking":
			if s, _ := block["thinking"].(string); strings.TrimSpace(s) != "" {
				entries = append(entries, teamDevChatEntry{T: "thinking", Text: teamDevClip(s, teamDevChatResultMax), At: at})
			}
		case kind == "tool_use":
			name, _ := block["name"].(string)
			id, _ := block["id"].(string)
			input, _ := block["input"].(map[string]any)
			detail, _ := json.MarshalIndent(input, "", "  ")
			if command, ok := input["command"].(string); ok && name == "Bash" {
				detail = []byte(command)
			}
			entries = append(entries, teamDevChatEntry{T: "tool", ID: id, Name: name, Summary: teamDevToolSummary(name, input, record.CWD),
				Detail: teamDevClip(string(detail), teamDevChatInputMax), At: at})
		case kind == "tool_result":
			id, _ := block["tool_use_id"].(string)
			failed, _ := block["is_error"].(bool)
			entries = append(entries, teamDevChatEntry{T: "result", ID: id, Text: teamDevClip(teamDevResultText(block["content"]), teamDevChatResultMax), Error: failed, At: at})
		}
	}
	if len(said) > 0 {
		if kind, text := teamDevChatText(strings.Join(said, "\n")); kind != "" && text != "" {
			entries = append([]teamDevChatEntry{{T: kind, Text: text, At: at}}, entries...)
		}
	}
	return entries
}

// readDevChat reads a transcript's entries from offset to its last whole
// line, and where to read from next.
func readDevChat(file string, offset int64) ([]teamDevChatEntry, int64, bool, error) {
	f, err := os.Open(file)
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
	var entries []teamDevChatEntry
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			break // a line still being written waits for the next read
		}
		offset += int64(len(line))
		entries = append(entries, teamDevChatEntries(bytes.TrimSpace(line))...)
	}
	return entries, offset, reset, nil
}

// devChat is the page's view of a workspace's session, from offset on.
func (b *teamBroker) devChat(workspace, known string, offset int64) (teamDevChatView, error) {
	view, err := b.devChatLog(workspace, known, offset)
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
			ask := teamDevReadAsk(text, ansi)
			if agent.Status != "blocked" && teamDevAskTabbed(ask) {
				view.Agent.Status = "blocked"
			}
			if view.Agent.Status == "blocked" {
				view.Screen = text
				view.Ask = ask
			} else if agent.Status == "working" && agent.Agent == "claude" {
				view.Activity = teamDevActivity(text)
			}
		}
	}
	return view, nil
}

// devChatLog is the session's entries, from its transcript.
func (b *teamBroker) devChatLog(workspace, known string, offset int64) (teamDevChatView, error) {
	agent, session, err := devChatAgent(b.team, workspace)
	if err != nil {
		return teamDevChatView{}, err
	}
	view := teamDevChatView{Agent: agent, Entries: []teamDevChatEntry{}}
	if agent.Running && agent.Agent != "claude" {
		view.Note = "The chat shows Claude Code's sessions; " + agent.Agent + "'s is in the terminal."
		return view, nil
	}
	if session == "" {
		cwd := "/box/repo"
		if spaces, err := teamDevSpacesCached(b.team); err == nil {
			for _, space := range spaces {
				if space.ID == workspace && space.Path != "" {
					cwd = space.Path
				}
			}
		}
		session = teamDevLatestSession(b.team, cwd)
	}
	if session == "" {
		view.Note = "No Claude Code session here yet."
		return view, nil
	}
	if !strings.HasPrefix(path.Clean(session), "/box/home/.claude/projects/") {
		return view, fmt.Errorf("%s isn't a Claude Code transcript", session)
	}
	host, _ := teamDevHostPath(b.team, session)
	view.Session, view.ID = session, strings.TrimSuffix(path.Base(session), ".jsonl")
	if session != known {
		offset, view.Reset = 0, true
	}
	entries, next, shrank, err := readDevChat(host, offset)
	if err != nil {
		return view, err
	}
	view.Offset, view.Reset = next, view.Reset || shrank
	if offset == 0 && len(entries) > teamDevChatFirst {
		// A long session: the last of it, from a turn's start.
		cut := len(entries) - teamDevChatFirst
		for cut < len(entries) && entries[cut].T != "user" {
			cut++
		}
		if cut == len(entries) {
			cut = len(entries) - teamDevChatFirst
		}
		view.Hidden, entries = cut, entries[cut:]
	}
	view.Entries = entries
	return view, nil
}

// devChatScreen is what an agent's terminal shows, as text and with its
// colors, which say what's selected where the text doesn't.
func devChatScreen(team, pane string) (text, ansi string, err error) {
	ansi, err = teamDevBoxExec(team, "herdr", "pane", "read", pane, "--source", "visible", "--lines", "40", "--format", "ansi")
	text = teamDevANSI.ReplaceAllString(strings.ReplaceAll(ansi, "\r", ""), "")
	return strings.TrimRight(text, "\n "), ansi, err
}

// teamDevANSI is a terminal's escape sequence: a color, a cursor's move.
var teamDevANSI = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|\x1b[()][0-9A-Za-z]`)

// teamDevActivityLine is Claude Code's spinner line: a glyph, a word with
// an ellipsis, and what it has done so far in brackets.
var teamDevActivityLine = regexp.MustCompile(`^\s*[^\sA-Za-z0-9]\s+([A-Z][A-Za-z'-]*…)\s*(\([^)]*\))?`)

// teamDevActivity is the last spinner line on Claude Code's screen, without
// the "esc to interrupt" the chat says with its Stop button.
func teamDevActivity(screen string) string {
	lines := strings.Split(screen, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		m := teamDevActivityLine.FindStringSubmatch(lines[i])
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

// teamDevHerdrDo runs herdr in the box for what it does, not what it says.
func teamDevHerdrDo(team string, args ...string) error {
	if text, err := teamDevBoxExec(team, append([]string{"herdr"}, args...)...); err != nil {
		return fmt.Errorf("herdr %s: %s", args[0]+" "+args[1], qFirstLine(strings.TrimSpace(text), err.Error()))
	}
	return nil
}

// teamDevChatKeys are the keys the chat may press: an answer to a
// question in the terminal, or Esc to stop.
var teamDevChatKeys = map[string]bool{"esc": true, "enter": true, "up": true, "down": true, "left": true, "right": true, "tab": true, "space": true,
	"1": true, "2": true, "3": true, "4": true, "5": true, "6": true, "7": true, "8": true, "9": true, "y": true, "n": true}

// devChatRoutes are the chat's: a session's entries, a message, keys, and
// resuming a session whose agent stopped. For the owners only.
func (b *teamBroker) devChatRoutes(mux *http.ServeMux, builder func(http.ResponseWriter, *http.Request) (teamPersonKey, bool),
	reply func(http.ResponseWriter, int, any), failure func(http.ResponseWriter, int, error)) {
	workspaceOK := regexp.MustCompile(`^w[0-9A-Za-z]{1,8}$`)
	mux.HandleFunc("GET "+teamDevPagePrefix+"api/chat", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := builder(w, r); !ok {
			return
		}
		workspace := r.URL.Query().Get("workspace")
		if !workspaceOK.MatchString(workspace) {
			failure(w, http.StatusBadRequest, fmt.Errorf("%q isn't a workspace", workspace))
			return
		}
		offset, _ := strconv.ParseInt(r.URL.Query().Get("offset"), 10, 64)
		view, err := b.devChat(workspace, r.URL.Query().Get("session"), offset)
		if err != nil {
			failure(w, http.StatusBadGateway, err)
			return
		}
		reply(w, http.StatusOK, view)
	})
	act := func(w http.ResponseWriter, r *http.Request, do func(agent teamDevChatAgent, session string, body map[string]any) error) {
		if _, ok := builder(w, r); !ok {
			return
		}
		var body map[string]any
		if err := json.NewDecoder(io.LimitReader(r.Body, teamDevChatTextMax+teamDevChatFilesMax/3*4+64<<10)).Decode(&body); err != nil {
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
	mux.HandleFunc("POST "+teamDevPagePrefix+"api/chat/send", func(w http.ResponseWriter, r *http.Request) {
		act(w, r, func(agent teamDevChatAgent, _ string, body map[string]any) error {
			text, _ := body["text"].(string)
			text = strings.TrimSpace(text)
			files, err := teamDevChatFiles(body)
			switch {
			case err != nil:
				return err
			case text == "" && len(files) == 0:
				return errors.New("nothing to send")
			case len(text) > teamDevChatTextMax:
				return fmt.Errorf("the message is longer than %s", formatDataSize(teamDevChatTextMax))
			case !agent.Running:
				return errors.New("no agent is running in this workspace; resume the session, or start one from the task")
			}
			paths, err := teamDevSaveChatFiles(b.team, body["workspace"].(string), files)
			if err != nil {
				return err
			}
			if len(paths) > 0 {
				text = strings.TrimSpace(text + "\n\nAttached, in the box: " + strings.Join(paths, " "))
			}
			return teamDevHerdrDo(b.team, "agent", "prompt", agent.Pane, text)
		})
	})
	mux.HandleFunc("POST "+teamDevPagePrefix+"api/chat/keys", func(w http.ResponseWriter, r *http.Request) {
		act(w, r, func(agent teamDevChatAgent, _ string, body map[string]any) error {
			list, _ := body["keys"].([]any)
			if !agent.Running || len(list) == 0 || len(list) > 4 {
				return errors.New("no agent to press keys for")
			}
			args := []string{"agent", "send-keys", agent.Pane}
			for _, item := range list {
				key, _ := item.(string)
				if !teamDevChatKeys[key] {
					return fmt.Errorf("the chat doesn't press %q", key)
				}
				args = append(args, key)
			}
			return teamDevHerdrDo(b.team, args...)
		})
	})
	mux.HandleFunc("POST "+teamDevPagePrefix+"api/chat/answer", func(w http.ResponseWriter, r *http.Request) {
		act(w, r, func(agent teamDevChatAgent, _ string, body map[string]any) error {
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
	mux.HandleFunc("POST "+teamDevPagePrefix+"api/chat/resume", func(w http.ResponseWriter, r *http.Request) {
		act(w, r, func(agent teamDevChatAgent, _ string, body map[string]any) error {
			id, _ := body["session"].(string)
			switch {
			case agent.Running:
				return errors.New("an agent is running in this workspace already")
			case agent.Pane == "":
				return errors.New("the workspace has no pane to run Claude in")
			case !regexp.MustCompile(`^[0-9a-f-]{36}$`).MatchString(id):
				return fmt.Errorf("%q isn't a Claude Code session", id)
			}
			return teamDevHerdrDo(b.team, "pane", "run", agent.Pane, "claude --resume "+id)
		})
	})
}
