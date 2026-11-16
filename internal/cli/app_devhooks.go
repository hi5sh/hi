package cli

// The agents' state from their hooks (docs/specs/approved/hi_app_dev_orca.md,
// release 2). herdr reads each agent's state off its screen: working,
// idle, blocked, done. Claude Code says more, and says it exactly, through
// hooks: a prompt sent, a tool about to run and done, a permission or a
// question asked, stopped. Codex says when a turn is done, through its
// notify program. hi writes both into the dev box's settings, beside the
// owners' own, herdr's, and Orca's; each runs hi dev __hook, which hands
// the event to the broker's socket and never fails the agent's turn. The
// broker keeps each workspace's last state, and the page's dots and the
// chat's status come from it, with herdr's where a hook says nothing.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
)

// appDevHookCommand is what the agents' settings run, by its path in the
// box: a session over SSH has the same.
const appDevHookCommand = "/box/bin/hi dev __hook"

// appDevHookTrust is how long a hook's word stands against herdr's: a
// turn stopped with Esc says nothing to the hooks, so after this, where
// herdr sees the agent idle, it is.
const appDevHookTrust = 15 * time.Second

// appDevClaudeHookEvents are Claude Code's events hi listens to.
var appDevClaudeHookEvents = []string{
	"SessionStart", "SessionEnd", "UserPromptSubmit",
	"PreToolUse", "PostToolUse", "PostToolUseFailure", "PermissionRequest",
	"Notification", "PreCompact", "Stop", "StopFailure",
}

// appDevHookEvent is one event, as hi dev __hook sends it to the broker.
type appDevHookEvent struct {
	Workspace string `json:"workspace"` // herdr's, from the agent's environment
	Pane      string `json:"pane,omitempty"`
	Agent     string `json:"agent"` // claude, codex
	Event     string `json:"event"` // Claude Code's hook_event_name; Stop for Codex's turn
	Session   string `json:"session,omitempty"`
	Tool      string `json:"tool,omitempty"`
	Kind      string `json:"kind,omitempty"` // a notification's type, a session start's source
	Subagent  bool   `json:"subagent,omitempty"`
}

// appDevHookState is a workspace's agent, as its hooks last said.
type appDevHookState struct {
	Agent   string    `json:"agent"`
	State   string    `json:"state"` // working, blocked, idle
	Since   time.Time `json:"since"` // in this state
	At      time.Time `json:"at"`    // the last event
	Tool    string    `json:"tool,omitempty"`
	Waiting string    `json:"waiting,omitempty"` // what it waits for, when blocked
	Session string    `json:"session,omitempty"`
	Pane    string    `json:"pane,omitempty"`
}

var (
	appDevHooksMu sync.Mutex
	appDevHooks   = map[string]map[string]appDevHookState{} // team, then workspace
)

var appDevHookWorkspace = regexp.MustCompile(`^[A-Za-z0-9_:.-]{1,64}$`)

// appDevHookRecord takes an event into its workspace's state.
func appDevHookRecord(team string, event appDevHookEvent, now time.Time) {
	if !appDevHookWorkspace.MatchString(event.Workspace) || (event.Agent != "claude" && event.Agent != "codex") {
		return
	}
	appDevHooksMu.Lock()
	defer appDevHooksMu.Unlock()
	spaces := appDevHooks[team]
	if spaces == nil {
		spaces = map[string]appDevHookState{}
		appDevHooks[team] = spaces
	}
	for id, state := range spaces {
		if now.Sub(state.At) > 24*time.Hour {
			delete(spaces, id)
		}
	}
	prev, had := spaces[event.Workspace]
	if had && (prev.Agent != event.Agent || event.Session != "" && prev.Session != "" && prev.Session != event.Session) {
		prev, had = appDevHookState{}, false // another agent, or another session: start again
	}
	next, keep := appDevHookNext(prev, event, now)
	if !keep {
		delete(spaces, event.Workspace)
		return
	}
	spaces[event.Workspace] = next
}

// appDevHookNext is a state after an event, and whether to keep one.
func appDevHookNext(prev appDevHookState, event appDevHookEvent, now time.Time) (appDevHookState, bool) {
	next := prev
	next.Agent, next.At = event.Agent, now
	if event.Session != "" {
		next.Session = event.Session
	}
	if event.Pane != "" {
		next.Pane = event.Pane
	}
	if next.State == "" {
		next.State = "idle"
	}
	set := func(state, tool, waiting string) {
		if state != next.State {
			next.Since = now
		}
		next.State, next.Tool, next.Waiting = state, tool, waiting
	}
	if event.Subagent {
		// A subagent's tools run inside the main agent's: it works on.
		if event.Event != "SubagentStop" && next.State != "blocked" {
			set("working", next.Tool, "")
		}
		return next, true
	}
	switch event.Event {
	case "SessionEnd":
		return appDevHookState{}, false
	case "SessionStart":
		if event.Kind != "compact" { // a compaction goes on with the turn
			set("idle", "", "")
		}
	case "UserPromptSubmit":
		set("working", "", "")
		next.Since = now // a new turn
	case "PreToolUse":
		switch event.Tool {
		case "AskUserQuestion":
			set("blocked", "", "a question")
		case "ExitPlanMode":
			set("blocked", "", "a plan to approve")
		default:
			set("working", event.Tool, "")
		}
	case "PostToolUse", "PostToolUseFailure":
		set("working", "", "")
	case "PermissionRequest":
		set("blocked", "", "permission"+appDevHookFor(event.Tool))
	case "Notification":
		switch event.Kind {
		case "permission_prompt":
			if next.State != "blocked" {
				set("blocked", "", "permission")
			}
		case "elicitation_dialog":
			set("blocked", "", "an answer for a tool")
		case "idle_prompt":
			if next.State != "idle" {
				set("idle", "", "")
			}
		}
	case "PreCompact":
		set("working", "", "")
	case "Stop", "StopFailure":
		set("idle", "", "")
	}
	if next.Since.IsZero() {
		next.Since = now
	}
	return next, true
}

func appDevHookFor(tool string) string {
	if tool == "" {
		return ""
	}
	return " for " + tool
}

// appDevHookStatus is a workspace's agent's state, from herdr's and its
// hooks': the hooks', for as long as herdr's agent and session are the
// ones they spoke for, unless they've been quiet a while and herdr sees
// otherwise. The state from the hooks comes back when it's the one used,
// for what it adds: the tool, what it waits for, and since when.
func appDevHookStatus(team, workspace, agent, herdr, session string, now time.Time) (string, *appDevHookState) {
	appDevHooksMu.Lock()
	state, ok := appDevHooks[team][workspace]
	appDevHooksMu.Unlock()
	if !ok || agent == "" || state.Agent != agent || session != "" && state.Session != "" && !strings.Contains(session, state.Session) {
		return herdr, nil
	}
	stale := now.Sub(state.At) > appDevHookTrust
	status := state.State
	switch state.State {
	case "working":
		if stale && (herdr == "idle" || herdr == "done") {
			status = herdr
		}
	case "idle":
		if herdr == "done" || stale && (herdr == "working" || herdr == "blocked") {
			status = herdr
		}
	}
	if status != state.State {
		return status, nil
	}
	return status, &state
}

// ---------------------------------------------------------------------------
// the agents' settings in the box

// appDevClaudeHooks puts hi's hooks in Claude Code's settings, beside the
// rest, and says whether it changed them.
func appDevClaudeHooks(settings map[string]any) bool {
	hooks, _ := settings["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}
	command := appDevHookCommand + " claude"
	changed := false
	for _, event := range appDevClaudeHookEvents {
		groups, _ := hooks[event].([]any)
		found := false
		for _, group := range groups {
			entries, _ := group.(map[string]any)["hooks"].([]any)
			for _, entry := range entries {
				if hook, _ := entry.(map[string]any); hook != nil && hook["command"] == command {
					found = true
				}
			}
		}
		if !found {
			hooks[event] = append(groups, map[string]any{"hooks": []any{map[string]any{"type": "command", "command": command, "timeout": 5}}})
			changed = true
		}
	}
	if changed {
		settings["hooks"] = hooks
	}
	return changed
}

// appDevCodexNotify is Codex's notify program in the box: hi, for each
// turn done. A top-level key, so before the config's tables.
const appDevCodexNotify = `notify = ["/box/bin/hi", "dev", "__hook", "codex"]` + "\n"

// ---------------------------------------------------------------------------
// hi dev __hook, in the box

// appDevHookClient hands an agent's hook event to the broker. Whatever
// goes wrong, it says nothing and succeeds: an agent's turn never waits
// on it, nor fails for it.
func appDevHookClient(args []string, stdin io.Reader) {
	workspace := os.Getenv("HERDR_WORKSPACE_ID")
	if len(args) == 0 || workspace == "" {
		return // not an agent herdr runs: someone's own session, over SSH
	}
	event := appDevHookEvent{Workspace: workspace, Pane: os.Getenv("HERDR_PANE_ID"), Agent: args[0]}
	switch args[0] {
	case "claude":
		var input struct {
			Event   string `json:"hook_event_name"`
			Session string `json:"session_id"`
			Tool    string `json:"tool_name"`
			Notice  string `json:"notification_type"`
			Source  string `json:"source"`
			AgentID string `json:"agent_id"`
		}
		data, _ := io.ReadAll(io.LimitReader(stdin, 4<<20))
		if json.Unmarshal(data, &input) != nil {
			return
		}
		event.Event, event.Session, event.Tool, event.Subagent = input.Event, input.Session, input.Tool, input.AgentID != ""
		event.Kind = firstNonEmpty(input.Notice, input.Source)
	case "codex":
		// Codex passes its notice as the last argument.
		var input struct {
			Type   string `json:"type"`
			Thread string `json:"thread-id"`
		}
		if len(args) < 2 || json.Unmarshal([]byte(args[len(args)-1]), &input) != nil || input.Type != "agent-turn-complete" {
			return
		}
		event.Event, event.Session = "Stop", input.Thread
	default:
		return
	}
	socket := firstNonEmpty(os.Getenv(appDevSocketEnv), appDevSocketInBox)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	client := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}}
	data, _ := json.Marshal(event)
	request, err := http.NewRequestWithContext(ctx, "POST", "http://team/hook", bytes.NewReader(data))
	if err != nil {
		return
	}
	if response, err := client.Do(request); err == nil {
		response.Body.Close()
	}
}
