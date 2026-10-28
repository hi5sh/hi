package main

// The app's MCP server, for the lead (docs/specs/approved/hi_team_agents.md).
// An app may serve MCP at /mcp. The lead's Hermes reaches it through
// hi team mcp production|preview in its box, a bridge from stdio to the
// broker, which passes the messages to the app. The broker, outside the
// lead's box, keeps production to the tools that only read: on the
// preview, which runs on its own copy of production's data, every tool.

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	// teamMCPPath is where an app serves MCP, Streamable HTTP.
	teamMCPPath = "/mcp"
	// teamMCPProtocol is the version hi speaks to the app, and offers the
	// lead when it asks for none.
	teamMCPProtocol = "2025-06-18"
	// teamMCPMaxBody caps a message either way.
	teamMCPMaxBody = 8 << 20
)

// teamMCPPoll is how often the bridge looks whether the app's tools
// changed (a release, a branch on the preview); tests shorten it.
var teamMCPPoll = time.Minute

// errTeamNoMCP says the app serves no MCP: no /mcp, or no app running.
var errTeamNoMCP = errors.New("the app has no MCP server at " + teamMCPPath)

type teamMCPMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *teamMCPError   `json:"error,omitempty"`
}

type teamMCPError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// teamMCPTool is what hi reads of a tool; the rest passes as the app
// wrote it.
type teamMCPTool struct {
	Name        string `json:"name"`
	Annotations struct {
		ReadOnlyHint bool `json:"readOnlyHint"`
	} `json:"annotations"`
}

// teamMCPApps keeps the app's MCP session for each address.
type teamMCPApps struct {
	mu       sync.Mutex
	sessions map[string]*teamMCPSession
}

type teamMCPSession struct {
	id           string // Mcp-Session-Id, when the app keeps sessions
	instructions string
	ready        bool
}

func teamMCPSlot(slot string) bool { return slot == teamSlotProduction || slot == teamSlotPreview }

// appMCPURL is the app's /mcp on the address, as it runs now.
func appMCPURL(team, slot string) (string, error) {
	data, err := os.ReadFile(teamSlotPortFile(team, slot))
	if err != nil {
		return "", errTeamNoMCP
	}
	port, err := parsePort(strings.TrimSpace(string(data)))
	if err != nil {
		return "", errTeamNoMCP
	}
	return "http://" + net.JoinHostPort("127.0.0.1", fmt.Sprint(port)) + teamMCPPath, nil
}

// teamMCPPost sends one message to the app and returns its answer: nil for
// a notification. It reads a JSON answer or an event stream's.
func teamMCPPost(ctx context.Context, address, session string, message teamMCPMessage) (*teamMCPMessage, string, error) {
	body, _ := json.Marshal(message)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, address, bytes.NewReader(body))
	if err != nil {
		return nil, "", err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set("MCP-Protocol-Version", teamMCPProtocol)
	if session != "" {
		request.Header.Set("Mcp-Session-Id", session)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, "", errTeamNoMCP
	}
	defer response.Body.Close()
	session = response.Header.Get("Mcp-Session-Id")
	switch {
	case response.StatusCode == http.StatusAccepted:
		return nil, session, nil
	case response.StatusCode == http.StatusNotFound && request.Header.Get("Mcp-Session-Id") != "":
		return nil, "", errTeamMCPSessionGone
	case response.StatusCode == http.StatusNotFound || response.StatusCode == http.StatusMethodNotAllowed:
		return nil, "", errTeamNoMCP
	case response.StatusCode != http.StatusOK:
		data, _ := io.ReadAll(io.LimitReader(response.Body, 512))
		return nil, "", fmt.Errorf("the app's %s answered %s: %s", teamMCPPath, response.Status, strings.TrimSpace(string(data)))
	}
	reader := io.LimitReader(response.Body, teamMCPMaxBody)
	if strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") {
		answer, err := teamMCPFromStream(reader, message.ID)
		return answer, session, err
	}
	var answer teamMCPMessage
	if err := json.NewDecoder(reader).Decode(&answer); err != nil {
		return nil, "", fmt.Errorf("the app's %s answered no JSON-RPC: %w", teamMCPPath, err)
	}
	return &answer, session, nil
}

var errTeamMCPSessionGone = errors.New("the app's MCP session ended")

// teamMCPFromStream is the answer to id in an event stream; the app's
// notifications on the way are dropped.
func teamMCPFromStream(stream io.Reader, id json.RawMessage) (*teamMCPMessage, error) {
	scanner := bufio.NewScanner(stream)
	scanner.Buffer(make([]byte, 64<<10), teamMCPMaxBody)
	var data strings.Builder
	for scanner.Scan() {
		line := scanner.Text()
		if rest, ok := strings.CutPrefix(line, "data:"); ok {
			data.WriteString(strings.TrimPrefix(rest, " "))
			continue
		}
		if line != "" || data.Len() == 0 {
			continue
		}
		var message teamMCPMessage
		if json.Unmarshal([]byte(data.String()), &message) == nil && message.Method == "" && bytes.Equal(message.ID, id) {
			return &message, nil
		}
		data.Reset()
	}
	if data.Len() > 0 {
		var message teamMCPMessage
		if json.Unmarshal([]byte(data.String()), &message) == nil && bytes.Equal(message.ID, id) {
			return &message, nil
		}
	}
	return nil, fmt.Errorf("the app's %s stream ended without an answer", teamMCPPath)
}

// appMCP sends a request to the app on the address, starting its session
// first, and once more when the app has forgotten it.
func (b *teamBroker) appMCP(ctx context.Context, slot string, message teamMCPMessage) (*teamMCPMessage, error) {
	address, err := appMCPURL(b.team, slot)
	if err != nil {
		return nil, err
	}
	for attempt := 0; ; attempt++ {
		session, err := b.appMCPSession(ctx, slot, address)
		if err != nil {
			return nil, err
		}
		answer, _, err := teamMCPPost(ctx, address, session.id, message)
		if (errors.Is(err, errTeamMCPSessionGone) || errors.Is(err, errTeamNoMCP)) && attempt == 0 {
			// A release or a restart: a new app, which knows no session.
			b.mcp.forget(slot)
			continue
		}
		if err == nil && answer == nil {
			return nil, fmt.Errorf("the app's %s didn't answer %s", teamMCPPath, message.Method)
		}
		return answer, err
	}
}

func (a *teamMCPApps) forget(slot string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.sessions, slot)
}

// appMCPSession is the app's session on the address, started with
// initialize when there is none.
func (b *teamBroker) appMCPSession(ctx context.Context, slot, address string) (teamMCPSession, error) {
	b.mcp.mu.Lock()
	defer b.mcp.mu.Unlock()
	if session := b.mcp.sessions[slot]; session != nil && session.ready {
		return *session, nil
	}
	params, _ := json.Marshal(map[string]any{
		"protocolVersion": teamMCPProtocol,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]string{"name": "hi-team-" + b.team, "version": version},
	})
	answer, id, err := teamMCPPost(ctx, address, "", teamMCPMessage{JSONRPC: "2.0", ID: json.RawMessage(`0`), Method: "initialize", Params: params})
	if err != nil {
		return teamMCPSession{}, err
	}
	if answer == nil || answer.Error != nil {
		// A stateless app may answer tools without it.
		answer = &teamMCPMessage{}
	}
	var result struct {
		Instructions string `json:"instructions"`
	}
	json.Unmarshal(answer.Result, &result)
	teamMCPPost(ctx, address, id, teamMCPMessage{JSONRPC: "2.0", Method: "notifications/initialized"})
	session := &teamMCPSession{id: id, instructions: result.Instructions, ready: true}
	if b.mcp.sessions == nil {
		b.mcp.sessions = map[string]*teamMCPSession{}
	}
	b.mcp.sessions[slot] = session
	return *session, nil
}

// appMCPTools is every tool the app offers on the address, page by page.
func (b *teamBroker) appMCPTools(ctx context.Context, slot string) ([]json.RawMessage, error) {
	var tools []json.RawMessage
	cursor := ""
	for page := 0; page < 20; page++ {
		params := json.RawMessage(`{}`)
		if cursor != "" {
			params, _ = json.Marshal(map[string]string{"cursor": cursor})
		}
		answer, err := b.appMCP(ctx, slot, teamMCPMessage{JSONRPC: "2.0", ID: json.RawMessage(`"hi-tools"`), Method: "tools/list", Params: params})
		if err != nil {
			return nil, err
		}
		if answer.Error != nil {
			return nil, fmt.Errorf("the app's tools/list: %s", answer.Error.Message)
		}
		var result struct {
			Tools      []json.RawMessage `json:"tools"`
			NextCursor string            `json:"nextCursor"`
		}
		if err := json.Unmarshal(answer.Result, &result); err != nil {
			return nil, fmt.Errorf("the app's tools/list: %w", err)
		}
		tools = append(tools, result.Tools...)
		if cursor = result.NextCursor; cursor == "" {
			break
		}
	}
	return tools, nil
}

// teamMCPLeadTools are the tools the lead may use on the address: on
// production, those that only read.
func teamMCPLeadTools(slot string, tools []json.RawMessage) []json.RawMessage {
	kept := []json.RawMessage{}
	for _, raw := range tools {
		var tool teamMCPTool
		if json.Unmarshal(raw, &tool) != nil || tool.Name == "" {
			continue
		}
		if slot == teamSlotProduction && !tool.Annotations.ReadOnlyHint {
			continue
		}
		kept = append(kept, raw)
	}
	return kept
}

// teamMCPInstructions is what the lead's Hermes is told of the server.
func teamMCPInstructions(team, slot, app string, err error) string {
	where := "production, what everyone uses: only the app's tools that read, never one that changes something"
	if slot == teamSlotPreview {
		where = "the preview, on its own copy of production's data: every tool. It may show a branch being tried rather than dev: hi team versions says which"
	}
	if errors.Is(err, errTeamNoMCP) {
		return fmt.Sprintf("The %s app on %s. It has no MCP server at %s yet, so it has no tools: the owners add them, and docs/user/mcp.md says what they do.", team, slot, teamMCPPath)
	}
	if err != nil {
		return fmt.Sprintf("The %s app on %s. Its MCP server isn't answering now: %s.", team, slot, err)
	}
	text := fmt.Sprintf("The %s app on %s. The app's own tools, on %s.", team, slot, where)
	if app != "" {
		text += "\n\n" + app
	}
	return text
}

// leadMCP answers one message from the lead's bridge: nil for a
// notification.
func (b *teamBroker) leadMCP(ctx context.Context, slot string, message teamMCPMessage) *teamMCPMessage {
	answer := func(result any) *teamMCPMessage {
		data, _ := json.Marshal(result)
		return &teamMCPMessage{JSONRPC: "2.0", ID: message.ID, Result: data}
	}
	failure := func(code int, text string) *teamMCPMessage {
		return &teamMCPMessage{JSONRPC: "2.0", ID: message.ID, Error: &teamMCPError{Code: code, Message: text}}
	}
	if len(message.ID) == 0 {
		return nil
	}
	switch message.Method {
	case "initialize":
		var params struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		json.Unmarshal(message.Params, &params)
		address, err := appMCPURL(b.team, slot)
		instructions := ""
		if err == nil {
			var session teamMCPSession
			session, err = b.appMCPSession(ctx, slot, address)
			instructions = session.instructions
		}
		return answer(map[string]any{
			"protocolVersion": firstNonEmpty(params.ProtocolVersion, teamMCPProtocol),
			"capabilities":    map[string]any{"tools": map[string]bool{"listChanged": true}},
			"serverInfo":      map[string]string{"name": "app-" + slot, "version": version},
			"instructions":    teamMCPInstructions(b.team, slot, instructions, err),
		})
	case "ping":
		return answer(map[string]any{})
	case "tools/list":
		tools, err := b.appMCPTools(ctx, slot)
		if errors.Is(err, errTeamNoMCP) {
			return answer(map[string]any{"tools": []any{}})
		}
		if err != nil {
			return failure(-32603, err.Error())
		}
		return answer(map[string]any{"tools": teamMCPLeadTools(slot, tools)})
	case "tools/call":
		var params struct {
			Name string `json:"name"`
		}
		json.Unmarshal(message.Params, &params)
		if slot == teamSlotProduction {
			tools, err := b.appMCPTools(ctx, slot)
			if err != nil {
				return failure(-32603, err.Error())
			}
			allowed := false
			for _, raw := range teamMCPLeadTools(slot, tools) {
				var tool teamMCPTool
				json.Unmarshal(raw, &tool)
				allowed = allowed || tool.Name == params.Name
			}
			if !allowed {
				fmt.Fprintf(os.Stderr, "team %s: the lead may not use %s on production\n", b.team, params.Name)
				return answer(map[string]any{"isError": true, "content": []map[string]string{{"type": "text",
					"text": params.Name + " isn't one of the app's tools that only read, so the lead can't use it on production. Try it on the preview (app_preview), on its own copy of the data."}}})
			}
		}
		fmt.Fprintf(os.Stderr, "team %s: the lead uses %s on %s\n", b.team, params.Name, slot)
		forwarded, err := b.appMCP(ctx, slot, message)
		if err != nil {
			return failure(-32603, err.Error())
		}
		forwarded.ID = message.ID
		return forwarded
	}
	return failure(-32601, "hi passes on only the app's tools, not "+message.Method)
}

// leadMCPRoute is POST /mcp/{slot} on the lead's socket: a JSON-RPC
// message in, its answer out, or null for a notification.
func (b *teamBroker) leadMCPRoute(w http.ResponseWriter, r *http.Request) {
	slot := r.PathValue("slot")
	w.Header().Set("Content-Type", "application/json")
	if !teamMCPSlot(slot) {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "the app's MCP is on production or preview, not " + slot})
		return
	}
	var message teamMCPMessage
	if err := json.NewDecoder(io.LimitReader(r.Body, teamMCPMaxBody)).Decode(&message); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	json.NewEncoder(w).Encode(b.leadMCP(r.Context(), slot, message))
}

// ---------------------------------------------------------------------------
// the bridge, in the lead's box

// runTeamMCPBridge is hi team mcp <address>: Hermes' MCP server for the
// app on that address, over stdio, one JSON-RPC message a line.
func runTeamMCPBridge(args []string, stdin io.Reader, stdout io.Writer, client *http.Client) error {
	if len(args) != 1 || !teamMCPSlot(args[0]) {
		return usageError{"usage: hi team mcp production|preview  (the app's MCP server, for the lead's Hermes, over stdio)"}
	}
	slot := args[0]
	post := func(ctx context.Context, message teamMCPMessage) (*teamMCPMessage, error) {
		body, _ := json.Marshal(message)
		request, _ := http.NewRequestWithContext(ctx, http.MethodPost, "http://team/mcp/"+slot, bytes.NewReader(body))
		response, err := client.Do(request)
		if err != nil {
			return nil, fmt.Errorf("the team's broker isn't answering: %w", err)
		}
		defer response.Body.Close()
		var answer *teamMCPMessage
		if response.StatusCode != http.StatusOK {
			var failure struct{ Error string }
			json.NewDecoder(response.Body).Decode(&failure)
			return nil, errors.New(firstNonEmpty(failure.Error, response.Status))
		}
		err = json.NewDecoder(io.LimitReader(response.Body, teamMCPMaxBody)).Decode(&answer)
		return answer, err
	}
	var writeMu sync.Mutex
	write := func(message *teamMCPMessage) {
		data, _ := json.Marshal(message)
		writeMu.Lock()
		defer writeMu.Unlock()
		stdout.Write(append(data, '\n'))
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// The tools as last listed: when they change, Hermes lists them again.
	var seenMu sync.Mutex
	seen, initialized := "", false
	toolsHash := func(answer *teamMCPMessage) string {
		if answer == nil || answer.Error != nil {
			return ""
		}
		return fmt.Sprintf("%x", sha256.Sum256(answer.Result))
	}
	go func() {
		ticker := time.NewTicker(teamMCPPoll)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			answer, err := post(ctx, teamMCPMessage{JSONRPC: "2.0", ID: json.RawMessage(`"hi-poll"`), Method: "tools/list"})
			hash := toolsHash(answer)
			if err != nil || hash == "" {
				continue
			}
			seenMu.Lock()
			changed := initialized && seen != "" && hash != seen
			seen = hash
			seenMu.Unlock()
			if changed {
				write(&teamMCPMessage{JSONRPC: "2.0", Method: "notifications/tools/list_changed"})
			}
		}
	}()
	var wait sync.WaitGroup
	scanner := bufio.NewScanner(stdin)
	scanner.Buffer(make([]byte, 64<<10), teamMCPMaxBody)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var message teamMCPMessage
		if err := json.Unmarshal(line, &message); err != nil {
			write(&teamMCPMessage{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &teamMCPError{Code: -32700, Message: err.Error()}})
			continue
		}
		if message.Method == "notifications/initialized" {
			seenMu.Lock()
			initialized = true
			seenMu.Unlock()
		}
		if len(message.ID) == 0 {
			continue
		}
		wait.Add(1)
		go func() {
			defer wait.Done()
			answer, err := post(ctx, message)
			if err != nil {
				answer = &teamMCPMessage{JSONRPC: "2.0", ID: message.ID, Error: &teamMCPError{Code: -32603, Message: err.Error()}}
			}
			if answer == nil {
				return
			}
			if message.Method == "tools/list" {
				seenMu.Lock()
				seen = toolsHash(answer)
				seenMu.Unlock()
			}
			write(answer)
		}()
	}
	wait.Wait()
	return scanner.Err()
}
