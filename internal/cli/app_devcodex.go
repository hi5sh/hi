package cli

// Codex in Chat, and every session (docs/specs/approved/hi_app_dev_orca.md,
// release 6). Codex keeps each session in
// ~/.codex/sessions/YYYY/MM/DD/rollout-<time>-<id>.jsonl; its
// item_completed records are what it said and did, as Chat draws Claude
// Code's: the person's messages, its answers, its commands with their
// output, its reasoning's summary, the files it changed. A workspace's
// sessions, both agents', newest first, each resume from the page.

import (
	"bufio"
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const appDevCodexSessions = "/box/home/.codex/sessions"

// appDevIsCodexSession says a session's file is one of Codex's.
func appDevIsCodexSession(session string) bool {
	clean := path.Clean(session)
	return strings.HasPrefix(clean, appDevCodexSessions+"/") && strings.HasPrefix(path.Base(clean), "rollout-") && strings.HasSuffix(clean, ".jsonl")
}

// appDevCodexID is a Codex session's id, from its file's name.
var appDevCodexIDPattern = regexp.MustCompile(`([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})\.jsonl$`)

func appDevCodexID(session string) string {
	if m := appDevCodexIDPattern.FindStringSubmatch(session); m != nil {
		return m[1]
	}
	return ""
}

// appDevCodexEntries are one line of a Codex session, as Chat draws it.
func appDevCodexEntries(line []byte) []appDevChatEntry {
	var record struct {
		Timestamp time.Time `json:"timestamp"`
		Type      string    `json:"type"`
		Payload   struct {
			Type string         `json:"type"`
			Item map[string]any `json:"item"`
		} `json:"payload"`
	}
	if json.Unmarshal(line, &record) != nil || record.Type != "event_msg" || record.Payload.Type != "item_completed" || record.Payload.Item == nil {
		return nil
	}
	at, item := record.Timestamp, record.Payload.Item
	str := func(m map[string]any, key string) string { s, _ := m[key].(string); return s }
	texts := func(key string) string {
		var parts []string
		list, _ := item[key].([]any)
		for _, part := range list {
			switch p := part.(type) {
			case string:
				parts = append(parts, p)
			case map[string]any:
				parts = append(parts, str(p, "text"))
			}
		}
		return strings.TrimSpace(strings.Join(parts, "\n"))
	}
	id := str(item, "id")
	switch str(item, "type") {
	case "UserMessage":
		text := texts("content")
		// What Codex adds to the first message itself: the instructions and
		// the environment, not the person's words.
		if text == "" || strings.HasPrefix(text, "# AGENTS.md instructions") || strings.HasPrefix(text, "<environment_context>") || strings.HasPrefix(text, "<user_instructions>") {
			return nil
		}
		return []appDevChatEntry{{T: "user", Text: text, At: at, UUID: id}}
	case "AgentMessage":
		if text := texts("content"); text != "" {
			return []appDevChatEntry{{T: "text", Text: text, At: at}}
		}
	case "Reasoning":
		if text := texts("summary_text"); text != "" {
			return []appDevChatEntry{{T: "thinking", Text: appDevClip(text, appDevChatResultMax), At: at}}
		}
	case "CommandExecution":
		command := appDevCodexCommand(item["command"])
		summary, _, _ := strings.Cut(command, "\n")
		entries := []appDevChatEntry{{T: "tool", ID: id, Name: "Bash", Summary: summary, Detail: appDevClip(command, appDevChatInputMax), At: at}}
		code, hasCode := item["exit_code"].(float64)
		entries = append(entries, appDevChatEntry{T: "result", ID: id, Text: appDevClip(str(item, "aggregated_output"), appDevChatResultMax),
			Error: (hasCode && code != 0) || str(item, "status") == "failed", At: at})
		return entries
	case "FileChange":
		var paths []string
		changes, _ := item["changes"].([]any)
		for _, change := range changes {
			if c, ok := change.(map[string]any); ok && str(c, "path") != "" {
				paths = append(paths, str(c, "path"))
			}
		}
		detail, _ := json.MarshalIndent(item["changes"], "", "  ")
		return []appDevChatEntry{{T: "tool", ID: id, Name: "Edit", Summary: strings.Join(paths, ", "), Detail: appDevClip(string(detail), appDevChatInputMax), At: at},
			{T: "result", ID: id, Text: firstNonEmpty(str(item, "status"), "done"), Error: str(item, "status") == "failed", At: at}}
	case "McpToolCall":
		name := strings.Trim(str(item, "server")+"."+str(item, "tool"), ".")
		detail, _ := json.MarshalIndent(item["arguments"], "", "  ")
		result, _ := json.Marshal(firstNonNil(item["result"], item["error"]))
		return []appDevChatEntry{{T: "tool", ID: id, Name: name, Summary: name, Detail: appDevClip(string(detail), appDevChatInputMax), At: at},
			{T: "result", ID: id, Text: appDevClip(string(result), appDevChatResultMax), Error: item["error"] != nil, At: at}}
	case "WebSearch":
		return []appDevChatEntry{{T: "tool", ID: id, Name: "WebSearch", Summary: str(item, "query"), At: at}}
	}
	return nil
}

func firstNonNil(values ...any) any {
	for _, v := range values {
		if v != nil {
			return v
		}
	}
	return nil
}

// appDevCodexCommand is a command as the person would type it: without
// the shell Codex wraps it in.
func appDevCodexCommand(value any) string {
	var words []string
	switch v := value.(type) {
	case string:
		return v
	case []any:
		for _, w := range v {
			s, _ := w.(string)
			words = append(words, s)
		}
	}
	if len(words) == 3 && (words[0] == "/bin/bash" || words[0] == "bash" || words[0] == "/bin/sh" || words[0] == "sh") && (words[1] == "-lc" || words[1] == "-c") {
		return words[2]
	}
	return strings.Join(words, " ")
}

// appDevCodexMeta is a Codex session's first line: where it ran, and
// whether a person ran it (exec is hi dev review's and other runs').
type appDevCodexMeta struct {
	ID, CWD, Source string
	At              time.Time
}

func appDevReadCodexMeta(team, host string) (appDevCodexMeta, bool) {
	f, err := boxedOpenFile(appDevHome(team), host) // the box writes its home: no link out of it

	if err != nil {
		return appDevCodexMeta{}, false
	}
	defer f.Close()
	reader := bufio.NewReaderSize(f, 64<<10)
	line, err := reader.ReadBytes('\n')
	if len(line) == 0 && err != nil {
		return appDevCodexMeta{}, false
	}
	var record struct {
		Type    string `json:"type"`
		Payload struct {
			ID        string    `json:"id"`
			CWD       string    `json:"cwd"`
			Source    any       `json:"source"`
			Timestamp time.Time `json:"timestamp"`
		} `json:"payload"`
	}
	// A first line can be long (the instructions ride along): only its start matters.
	if json.Unmarshal(line, &record) != nil || record.Type != "session_meta" {
		return appDevCodexMeta{}, false
	}
	source, _ := record.Payload.Source.(string)
	return appDevCodexMeta{ID: record.Payload.ID, CWD: record.Payload.CWD, Source: source, At: record.Payload.Timestamp}, true
}

// appDevSession is one of a workspace's sessions, for its history.
type appDevSession struct {
	Agent   string    `json:"agent"` // claude, codex
	ID      string    `json:"id"`
	Session string    `json:"session"` // its file, in the box
	At      time.Time `json:"at"`      // its last change
	Title   string    `json:"title,omitempty"`
}

// appDevSessionsMax is how many of a workspace's sessions the page lists.
const appDevSessionsMax = 30

// appDevSessions are a folder's sessions, both agents', newest first: the
// ones people had there, not hi's own runs (a review's, a brainstorm's).
func appDevSessions(team, cwd string) []appDevSession {
	var sessions []appDevSession
	if dir, ok := appDevHostPath(team, appDevClaudeDir(cwd)); ok {
		entries, _ := os.ReadDir(dir)
		for _, entry := range entries {
			info, err := entry.Info()
			if err != nil || !info.Mode().IsRegular() || !strings.HasSuffix(entry.Name(), ".jsonl") {
				continue
			}
			id := strings.TrimSuffix(entry.Name(), ".jsonl")
			sessions = append(sessions, appDevSession{Agent: "claude", ID: id, Session: appDevClaudeDir(cwd) + "/" + entry.Name(), At: info.ModTime().UTC(),
				Title: appDevSessionTitle(team, filepath.Join(dir, entry.Name()), appDevChatEntries)})
		}
	}
	for _, found := range appDevCodexFiles(team) {
		meta, ok := appDevReadCodexMeta(team, found.host)
		if !ok || meta.CWD != cwd || meta.Source == "exec" {
			continue
		}
		sessions = append(sessions, appDevSession{Agent: "codex", ID: firstNonEmpty(meta.ID, appDevCodexID(found.box)), Session: found.box, At: found.at,
			Title: appDevSessionTitle(team, found.host, appDevCodexEntries)})
	}
	sort.Slice(sessions, func(i, j int) bool { return sessions[i].At.After(sessions[j].At) })
	if len(sessions) > appDevSessionsMax {
		sessions = sessions[:appDevSessionsMax]
	}
	return sessions
}

type appDevCodexFile struct {
	box, host string
	at        time.Time
}

// appDevCodexFiles are Codex's session files in the box, newest first.
func appDevCodexFiles(team string) []appDevCodexFile {
	root, ok := appDevHostPath(team, appDevCodexSessions)
	if !ok {
		return nil
	}
	matches, _ := filepath.Glob(filepath.Join(root, "*", "*", "*", "rollout-*.jsonl"))
	var files []appDevCodexFile
	for _, host := range matches {
		info, err := os.Lstat(host)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		rel, err := filepath.Rel(root, host)
		if err != nil {
			continue
		}
		files = append(files, appDevCodexFile{box: appDevCodexSessions + "/" + filepath.ToSlash(rel), host: host, at: info.ModTime().UTC()})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].at.After(files[j].at) })
	return files
}

// appDevSessionTitle is a session's first message from the person, in a line.
func appDevSessionTitle(team, host string, parse func([]byte) []appDevChatEntry) string {
	f, err := boxedOpenFile(appDevHome(team), host)
	if err != nil {
		return ""
	}
	defer f.Close()
	reader := bufio.NewReaderSize(f, 1<<20)
	title := ""
	for n := 0; n < 400; n++ {
		line, _, err := appReadBoxLine(reader)
		if err != nil {
			break
		}
		for _, entry := range parse(line) {
			if entry.T == "title" {
				return appDevClip(entry.Text, 100) // Claude Code's own title for it
			}
			if entry.T == "user" && title == "" {
				first, _, _ := strings.Cut(strings.TrimSpace(entry.Text), "\n")
				title = appDevClip(first, 100)
			}
		}
	}
	return title
}

// appDevLatestCodexSession is the newest of a folder's Codex sessions.
func appDevLatestCodexSession(team, cwd string) string {
	for _, found := range appDevCodexFiles(team) {
		if meta, ok := appDevReadCodexMeta(team, found.host); ok && meta.CWD == cwd && meta.Source != "exec" {
			return found.box
		}
	}
	return ""
}

// appDevCodexSessionByID is a Codex session's file, by its id.
func appDevCodexSessionByID(team, id string) string {
	if id == "" {
		return ""
	}
	for _, found := range appDevCodexFiles(team) {
		if strings.HasSuffix(found.box, id+".jsonl") {
			return found.box
		}
	}
	return ""
}
