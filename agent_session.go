package main

// hi agent --session <folder> (docs/specs/approved/hi_team_threads.md): an
// agent's conversation outlives its box. The folder keeps a copy of the
// agent's own session file, as Claude Code (~/.claude/projects) and Codex
// (~/.codex/sessions) write it; the next box with the same folder gets it
// back in its home and resumes it (claude --resume, codex exec resume)
// instead of starting cold. When the box ends, the session's file, longer
// by this run, goes back to the folder.
//
// A session isn't resumed, and the run starts a new one, when the folder
// holds another agent's or another model's session, when its file is gone,
// or when its conversation has grown past three quarters of the model's
// window.

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	// agentSessionStateFile is the folder's record of its session.
	agentSessionStateFile = "hi-session.json"
	// agentSessionFiles holds the copy of the session's file, at its path
	// in the agent's home.
	agentSessionFiles = "files"
	// agentResumedFile, in the box's results, says what the run resumed:
	// the session's file and how much of it came before.
	agentResumedFile = "resumed.json"
	// agentClaudeWindow is the window a Claude session is measured against;
	// Codex says its own.
	agentClaudeWindow = 200_000
	// agentSessionShare is how much of the window, in percent, a session
	// may hold and still be resumed. Half was too little on team cartman:
	// a first build or an investigation alone reached it, so no follow-up
	// went on with the conversation.
	agentSessionShare = 75
)

// agentSessionState is hi-session.json.
type agentSessionState struct {
	Agent string `json:"agent"`
	Model string `json:"model,omitempty"`
	ID    string `json:"id"`
	// File is the session's file, relative to the agent's home.
	File string `json:"file"`
}

// agentResumed is resumed.json: the session file in the box's home, its
// size when the run began, and Codex's token totals by then, so this run's
// own steps and tokens can be told from the session's earlier ones.
type agentResumed struct {
	ID     string `json:"id"`
	File   string `json:"file"`
	Offset int64  `json:"offset"`
	Input  int    `json:"input,omitempty"`
	Cached int    `json:"cached,omitempty"`
	Output int    `json:"output,omitempty"`
}

var agentSessionIDPattern = regexp.MustCompile(`^[A-Za-z0-9-]{8,64}$`)

func loadAgentSession(dir string) (agentSessionState, bool) {
	var state agentSessionState
	data, err := os.ReadFile(filepath.Join(dir, agentSessionStateFile))
	if err != nil || json.Unmarshal(data, &state) != nil || !agentSessionIDPattern.MatchString(state.ID) || !filepath.IsLocal(state.File) {
		return agentSessionState{}, false
	}
	return state, true
}

// agentSessionResumable is the session a run of kind on model would
// resume from dir, or why it starts a new one.
func agentSessionResumable(dir, kind, model string) (agentSessionState, error) {
	state, ok := loadAgentSession(dir)
	switch {
	case !ok:
		return state, errors.New("no session yet")
	case state.Agent != kind:
		return state, fmt.Errorf("the session is %s's", agentDisplayName(state.Agent))
	case state.Model != model:
		return state, fmt.Errorf("the session ran on %s", firstNonEmpty(state.Model, "the default model"))
	}
	path := filepath.Join(dir, agentSessionFiles, state.File)
	if !fileExists(path) {
		return state, errors.New("the session's file is gone")
	}
	context, window, _ := agentSessionContext(kind, path)
	if window > 0 && context > window*agentSessionShare/100 {
		return state, fmt.Errorf("the conversation holds %d tokens, more than %d%% of the model's window", context, agentSessionShare)
	}
	return state, nil
}

// agentSessionContext reads a session's file: how many tokens its latest
// call sent (the conversation's size), the model's window, and, for Codex,
// the session's token totals.
func agentSessionContext(kind, path string) (context, window int, totals agentTokens) {
	file, err := os.Open(path)
	if err != nil {
		return 0, 0, totals
	}
	defer file.Close()
	if kind == "claude" {
		window = agentClaudeWindow
	}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 1<<20), 64<<20)
	for scanner.Scan() {
		line := scanner.Bytes()
		if kind == "claude" {
			var entry struct {
				Type    string `json:"type"`
				Message struct {
					Usage *struct {
						Input       int `json:"input_tokens"`
						CacheRead   int `json:"cache_read_input_tokens"`
						CacheCreate int `json:"cache_creation_input_tokens"`
					} `json:"usage"`
				} `json:"message"`
			}
			if json.Unmarshal(line, &entry) == nil && entry.Type == "assistant" && entry.Message.Usage != nil {
				usage := entry.Message.Usage
				context = usage.Input + usage.CacheRead + usage.CacheCreate
			}
			continue
		}
		var entry struct {
			Type    string `json:"type"`
			Payload struct {
				Type string `json:"type"`
				Info *struct {
					Total struct {
						Input  int `json:"input_tokens"`
						Cached int `json:"cached_input_tokens"`
						Output int `json:"output_tokens"`
					} `json:"total_token_usage"`
					Last struct {
						Input int `json:"input_tokens"`
					} `json:"last_token_usage"`
					Window int `json:"model_context_window"`
				} `json:"info"`
			} `json:"payload"`
		}
		if json.Unmarshal(line, &entry) == nil && entry.Type == "event_msg" && entry.Payload.Type == "token_count" && entry.Payload.Info != nil {
			info := entry.Payload.Info
			context, totals = info.Last.Input, agentTokens{Input: info.Total.Input, Cached: info.Total.Cached, Output: info.Total.Output}
			if info.Window > 0 {
				window = info.Window
			}
		}
	}
	return context, window, totals
}

// agentClaudeProject is the folder Claude Code keeps a working folder's
// sessions in, under ~/.claude/projects.
func agentClaudeProject(workdir string) string {
	return regexp.MustCompile(`[^A-Za-z0-9]`).ReplaceAllString(workdir, "-")
}

// prepareAgentSession puts the folder's session back in a new box's home,
// where the agent looks for it, and returns its ID: "" when the run starts
// a new session.
func prepareAgentSession(kind string, meta boxMeta, homeDir string) (string, error) {
	state, err := agentSessionResumable(meta.Session, kind, meta.Model)
	if err != nil {
		return "", nil
	}
	source := filepath.Join(meta.Session, agentSessionFiles, state.File)
	file := state.File
	if kind == "claude" {
		// Claude Code finds a session by its working folder, which is new
		// for each box.
		file = filepath.Join(".claude", "projects", agentClaudeProject(meta.Workdir), state.ID+".jsonl")
	}
	target := filepath.Join(homeDir, file)
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return "", err
	}
	os.Remove(target)
	if err := teamCopyFile(source, target); err != nil {
		return "", err
	}
	// Claude Code keeps large tool results beside the session, in a folder
	// named for it.
	if kind == "claude" {
		if extra := strings.TrimSuffix(source, ".jsonl"); fileExists(extra) {
			if err := agentCopyTree(extra, strings.TrimSuffix(target, ".jsonl")); err != nil {
				return "", err
			}
		}
	}
	info, err := os.Stat(target)
	if err != nil {
		return "", err
	}
	resumed := agentResumed{ID: state.ID, File: file, Offset: info.Size()}
	if kind == "codex" {
		_, _, totals := agentSessionContext(kind, target)
		resumed.Input, resumed.Cached, resumed.Output = totals.Input, totals.Cached, totals.Output
	}
	data, _ := json.Marshal(resumed)
	if err := os.MkdirAll(filepath.Join(homeDir, agentResultDir), 0o700); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(homeDir, agentResultDir, agentResumedFile), data, 0o600); err != nil {
		return "", err
	}
	return state.ID, nil
}

// loadAgentResumed is what a box's run resumed, if it resumed a session.
func loadAgentResumed(homeDir string) (agentResumed, bool) {
	var resumed agentResumed
	data, err := os.ReadFile(filepath.Join(homeDir, agentResultDir, agentResumedFile))
	if err != nil || json.Unmarshal(data, &resumed) != nil || resumed.ID == "" || !filepath.IsLocal(resumed.File) {
		return agentResumed{}, false
	}
	return resumed, true
}

// keepAgentSession copies a finished run's session from the box's home
// to the folder, for the next run to resume.
func keepAgentSession(meta boxMeta, id string) error {
	if meta.Session == "" || !agentSessionIDPattern.MatchString(id) {
		return nil
	}
	homeDir := boxStateFile(meta.Name, "home")
	var pattern string
	switch meta.Agent {
	case "claude":
		pattern = filepath.Join(homeDir, ".claude", "projects", "*", id+".jsonl")
	case "codex":
		pattern = filepath.Join(homeDir, ".codex", "sessions", "*", "*", "*", "rollout-*"+id+".jsonl")
	default:
		return nil
	}
	matches, _ := filepath.Glob(pattern)
	if len(matches) == 0 {
		return fmt.Errorf("the session's file isn't in the box")
	}
	source := matches[0]
	file, err := filepath.Rel(homeDir, source)
	if err != nil {
		return err
	}
	// The folder keeps one session: this one.
	store := filepath.Join(meta.Session, agentSessionFiles)
	if err := os.RemoveAll(store); err != nil {
		return err
	}
	target := filepath.Join(store, file)
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return err
	}
	if err := teamCopyFile(source, target); err != nil {
		return err
	}
	if extra := strings.TrimSuffix(source, ".jsonl"); meta.Agent == "claude" && fileExists(extra) {
		if err := agentCopyTree(extra, strings.TrimSuffix(target, ".jsonl")); err != nil {
			return err
		}
	}
	data, _ := json.MarshalIndent(agentSessionState{Agent: meta.Agent, Model: meta.Model, ID: id, File: file}, "", "  ")
	return os.WriteFile(filepath.Join(meta.Session, agentSessionStateFile), append(data, '\n'), 0o600)
}

// agentCopyTree copies a folder's regular files, keeping its layout.
func agentCopyTree(from, to string) error {
	return filepath.WalkDir(from, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(from, path)
		if err != nil {
			return err
		}
		target := filepath.Join(to, rel)
		switch {
		case entry.IsDir():
			return os.MkdirAll(target, 0o700)
		case entry.Type().IsRegular():
			source, err := os.Open(path)
			if err != nil {
				return err
			}
			defer source.Close()
			out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, source); err != nil {
				out.Close()
				return err
			}
			return out.Close()
		}
		return nil
	})
}
