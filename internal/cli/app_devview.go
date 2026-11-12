package cli

// The dev box's view of Stage and production (hi_app_dev_view.md): the
// app's own output with its settings' values masked, its jobs and
// workers, and its MCP server as the lead has it, all through the dev
// box's broker socket. Nothing here writes: the box's agents run without
// asking, so they get what can't harm. The owners can turn production's
// view off.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	appDevViewLines    = 200
	appDevViewLinesMax = 2000
	// Values shorter than these aren't masked: they'd hide words.
	appDevViewMaskMin     = 8
	appDevViewMaskFileMin = 16
)

// appDevViewSlot reads production, stage, or preview: the slot.
func appDevViewSlot(word string) (string, error) {
	switch word {
	case "", appSlotProduction:
		return appSlotProduction, nil
	case "stage", appSlotPreview:
		return appSlotPreview, nil
	}
	return "", fmt.Errorf("stage or production, not %q", word)
}

// appDevViewSlotWord is how people call a slot.
func appDevViewSlotWord(slot string) string {
	if slot == appSlotPreview {
		return "Stage"
	}
	return "production"
}

// devViewRefused says why the dev box may not look at a slot now.
func (b *appBroker) devViewRefused(slot string) error {
	if slot != appSlotProduction {
		return nil
	}
	config, err := loadTeam(b.team)
	if err != nil {
		return err
	}
	if config.DevViewProductionOff != "" {
		return fmt.Errorf("the dev box's view of production is off (%s); Stage is still there: hi dev logs stage", config.DevViewProductionOff)
	}
	return nil
}

// devViewNote puts a read of production in the dev box's network log,
// beside its reach, so the watcher sees it like any other.
func (b *appBroker) devViewNote(slot, what string) {
	fmt.Fprintf(os.Stderr, "hi: the dev box reads %s's %s\n", appDevViewSlotWord(slot), what)
	if slot == appSlotProduction {
		appDevNetworkNote(b.team, "view", "production-"+what)
	}
}

// appDevViewLogs is a box's last lines from the engine; tests replace it.
var appDevViewLogs = func(container string, lines int, since string) (string, error) {
	engine, _, err := detectBoxEngine()
	if err != nil {
		return "", err
	}
	args := []string{"logs", "--tail", strconv.Itoa(lines)}
	if since != "" {
		args = append(args, "--since", since)
	}
	var out bytes.Buffer
	if err := boxCommand(nil, &out, &out, engine.bin, append(args, container)...); err != nil {
		return "", fmt.Errorf("the box's log: %s", qFirstLine(strings.TrimSpace(out.String()), err.Error()))
	}
	return out.String(), nil
}

// appDevViewMasks are the slot's settings' values, as text to mask and
// the name to put in its place, longest first.
func appDevViewMasks(team, slot string) [][2]string {
	secrets, err := loadTeamSecrets(team)
	if err != nil {
		return nil
	}
	var masks [][2]string
	own := secrets.EnvPreview
	if slot == appSlotProduction {
		own = secrets.EnvProduction
	}
	for _, set := range []map[string]string{secrets.Env, own} {
		for key, value := range set {
			if len(value) >= appDevViewMaskMin {
				masks = append(masks, [2]string{value, "‹" + key + "›"})
			}
		}
	}
	for key, value := range appBoxFiles(secrets, slot) {
		texts := []string{value}
		if decoded, err := base64.StdEncoding.DecodeString(value); err == nil {
			texts = append(texts, string(decoded))
		}
		for _, text := range texts {
			for _, line := range strings.Split(text, "\n") {
				if line = strings.TrimSpace(line); len(line) >= appDevViewMaskFileMin {
					masks = append(masks, [2]string{line, "‹" + key + "›"})
				}
			}
		}
	}
	sort.Slice(masks, func(i, j int) bool { return len(masks[i][0]) > len(masks[j][0]) })
	return masks
}

// appDevViewRequest is what hi dev logs and jobs ask.
type appDevViewRequest struct {
	Slot  string `json:"slot"`
	Lines int    `json:"lines,omitempty"`
	Since string `json:"since,omitempty"`
	Grep  string `json:"grep,omitempty"`
}

// devViewLogs is the slot's box's output, masked, then filtered.
func (b *appBroker) devViewLogs(request appDevViewRequest) (string, error) {
	slot, err := appDevViewSlot(request.Slot)
	if err != nil {
		return "", err
	}
	if err := b.devViewRefused(slot); err != nil {
		return "", err
	}
	lines := request.Lines
	if lines <= 0 {
		lines = appDevViewLines
	}
	if lines > appDevViewLinesMax {
		lines = appDevViewLinesMax
	}
	if request.Since != "" {
		if _, err := time.ParseDuration(request.Since); err != nil {
			return "", fmt.Errorf("--since takes a duration such as 30m or 2h, not %q", request.Since)
		}
	}
	state, ok := loadTeamSlot(b.team, slot)
	if !ok || state.Container == "" {
		return "", fmt.Errorf("%s has no box now", appDevViewSlotWord(slot))
	}
	b.devViewNote(slot, "logs")
	// With --grep, read all the box's lines it may: the matches are what's
	// wanted, not the last few.
	read := lines
	if request.Grep != "" {
		read = appDevViewLinesMax * 10
	}
	text, err := appDevViewLogs(state.Container, read, request.Since)
	if err != nil {
		return "", err
	}
	masks := appDevViewMasks(b.team, slot)
	var kept []string
	grep := strings.ToLower(request.Grep)
	for _, line := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		for _, mask := range masks {
			line = strings.ReplaceAll(line, mask[0], mask[1])
		}
		if grep != "" && !strings.Contains(strings.ToLower(line), grep) {
			continue
		}
		kept = append(kept, line)
	}
	if len(kept) > lines {
		kept = kept[len(kept)-lines:]
	}
	if len(kept) == 0 || (len(kept) == 1 && kept[0] == "") {
		return fmt.Sprintf("%s's box (%s) printed nothing that matches.", appDevViewSlotWord(slot), state.Container), nil
	}
	return strings.Join(kept, "\n"), nil
}

// devViewJobs is what hi app jobs shows of the slot.
func (b *appBroker) devViewJobs(request appDevViewRequest) (string, error) {
	slot, err := appDevViewSlot(request.Slot)
	if err != nil {
		return "", err
	}
	if err := b.devViewRefused(slot); err != nil {
		return "", err
	}
	b.devViewNote(slot, "jobs")
	now := time.Now()
	var out bytes.Buffer
	printTeamWorkers(appWorkersReport(b.team, slot, b.appRunning, now), now, &out)
	printTeamJobs(appJobsReport(b.team, slot, now), slot, now, &out)
	return strings.TrimRight(out.String(), "\n"), nil
}

// devViewRoutes are the view's routes on the dev box's socket.
func (b *appBroker) devViewRoutes(mux *http.ServeMux, reply func(http.ResponseWriter, any, error), decode func(*http.Request, any) error) {
	mux.HandleFunc("POST /view/logs", func(w http.ResponseWriter, r *http.Request) {
		var request appDevViewRequest
		if err := decode(r, &request); err != nil {
			reply(w, nil, err)
			return
		}
		text, err := b.devViewLogs(request)
		reply(w, map[string]string{"text": text}, err)
	})
	mux.HandleFunc("POST /view/jobs", func(w http.ResponseWriter, r *http.Request) {
		var request appDevViewRequest
		if err := decode(r, &request); err != nil {
			reply(w, nil, err)
			return
		}
		text, err := b.devViewJobs(request)
		reply(w, map[string]string{"text": text}, err)
	})
	// The app's MCP, as the lead has it: on production, the tools that
	// only read.
	mux.HandleFunc("POST /mcp/{slot}", func(w http.ResponseWriter, r *http.Request) {
		if err := b.devViewRefused(r.PathValue("slot")); err != nil {
			reply(w, nil, err)
			return
		}
		b.devViewNote(r.PathValue("slot"), "mcp")
		b.leadMCPRoute(w, r)
	})
}

// ---------------------------------------------------------------------------
// in the box

// appDevSocketClient talks to the broker over the dev box's socket.
func appDevSocketClient(socket string) *http.Client {
	return &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}}
}

// appDevViewClient is hi dev logs, jobs, mcp, and tools.
func appDevViewClient(command string, args []string, stdin io.Reader, stdout io.Writer, call func(method, path string, body, out any) error, socket string) error {
	switch command {
	case "logs", "jobs":
		request := appDevViewRequest{}
		usage := usageError{"usage: hi dev logs [stage|production] [--lines N] [--since 30m] [--grep text]"}
		if command == "jobs" {
			usage = usageError{"usage: hi dev jobs [stage|production]"}
		}
		for i := 0; i < len(args); i++ {
			switch {
			case command == "logs" && (args[i] == "--lines" || args[i] == "-n") && i+1 < len(args):
				i++
				n, err := strconv.Atoi(args[i])
				if err != nil || n <= 0 {
					return usage
				}
				request.Lines = n
			case command == "logs" && args[i] == "--since" && i+1 < len(args):
				i++
				request.Since = args[i]
			case command == "logs" && args[i] == "--grep" && i+1 < len(args):
				i++
				request.Grep = args[i]
			case !strings.HasPrefix(args[i], "-") && request.Slot == "":
				request.Slot = args[i]
			default:
				return usage
			}
		}
		if _, err := appDevViewSlot(request.Slot); err != nil {
			return err
		}
		var answer struct{ Text string }
		if err := call("POST", "/view/"+command, request, &answer); err != nil {
			return err
		}
		fmt.Fprintln(stdout, answer.Text)
		return nil
	case "mcp":
		if len(args) != 1 {
			return usageError{"usage: hi dev mcp stage|production  (the app's MCP server over stdio)"}
		}
		slot, err := appDevViewSlot(args[0])
		if err != nil {
			return err
		}
		return runTeamMCPBridge([]string{slot}, stdin, stdout, appDevSocketClient(socket))
	case "tools":
		return appDevTools(args, stdin, stdout, socket)
	}
	return fmt.Errorf("no such command: %s", command)
}

// appDevTools is hi dev tools <slot> [<tool> [<json>|-]]: the app's MCP
// tools from a shell.
func appDevTools(args []string, stdin io.Reader, stdout io.Writer, socket string) error {
	usage := usageError{"usage: hi dev tools stage|production [<tool> [<json arguments>|-]]"}
	if len(args) < 1 || len(args) > 3 {
		return usage
	}
	slot, err := appDevViewSlot(args[0])
	if err != nil {
		return err
	}
	client := appDevSocketClient(socket)
	send := func(message appMCPMessage) (*appMCPMessage, error) {
		body, _ := json.Marshal(message)
		response, err := client.Post("http://team/mcp/"+slot, "application/json", bytes.NewReader(body))
		if err != nil {
			return nil, errors.New("the app's service isn't answering; it may be restarting, so try again in a minute")
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			var failure struct{ Error string }
			json.NewDecoder(response.Body).Decode(&failure)
			return nil, errors.New(firstNonEmpty(failure.Error, response.Status))
		}
		var answer *appMCPMessage
		if err := json.NewDecoder(io.LimitReader(response.Body, appMCPMaxBody)).Decode(&answer); err != nil {
			return nil, err
		}
		if answer == nil {
			return nil, errors.New("the app gave no answer")
		}
		if answer.Error != nil {
			return nil, fmt.Errorf("%s", answer.Error.Message)
		}
		return answer, nil
	}
	if len(args) == 1 {
		answer, err := send(appMCPMessage{JSONRPC: "2.0", ID: json.RawMessage("1"), Method: "tools/list"})
		if err != nil {
			return err
		}
		var list struct {
			Tools []struct {
				Name        string `json:"name"`
				Description string `json:"description"`
				Annotations struct {
					ReadOnlyHint bool `json:"readOnlyHint"`
				} `json:"annotations"`
			} `json:"tools"`
		}
		json.Unmarshal(answer.Result, &list)
		if len(list.Tools) == 0 {
			fmt.Fprintf(stdout, "%s's app offers no tools here.\n", appDevViewSlotWord(slot))
			return nil
		}
		for _, tool := range list.Tools {
			fmt.Fprintf(stdout, "%s  %s\n", tool.Name, qFirstLine(tool.Description, ""))
		}
		return nil
	}
	arguments := json.RawMessage("{}")
	if len(args) == 3 {
		text := args[2]
		if text == "-" {
			data, err := io.ReadAll(io.LimitReader(stdin, appMCPMaxBody))
			if err != nil {
				return err
			}
			text = string(data)
		}
		if !json.Valid([]byte(text)) {
			return errors.New("the tool's arguments are JSON, such as '{\"question\": \"…\"}'")
		}
		arguments = json.RawMessage(text)
	}
	params, _ := json.Marshal(map[string]any{"name": args[1], "arguments": arguments})
	answer, err := send(appMCPMessage{JSONRPC: "2.0", ID: json.RawMessage("1"), Method: "tools/call", Params: params})
	if err != nil {
		return err
	}
	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	json.Unmarshal(answer.Result, &result)
	for _, part := range result.Content {
		if part.Type == "text" {
			fmt.Fprintln(stdout, part.Text)
		}
	}
	if result.IsError {
		return errors.New("the tool says it failed")
	}
	return nil
}

// ---------------------------------------------------------------------------
// on the machine

// appDevViewCommand is hi app dev <app> view [production on|off].
func appDevViewCommand(team string, args []string, stdout io.Writer) error {
	config, err := loadTeam(team)
	if err != nil {
		return err
	}
	if len(args) == 0 {
		if config.DevViewProductionOff != "" {
			fmt.Fprintf(stdout, "The dev box sees Stage only: production's view is off (%s). On: hi app dev %s view production on\n", config.DevViewProductionOff, team)
		} else {
			fmt.Fprintf(stdout, "The dev box sees Stage's and production's logs, jobs, and the app's tools that read (hi dev logs, jobs, tools). Off for production: hi app dev %s view production off\n", team)
		}
		return nil
	}
	if len(args) != 2 || args[0] != appSlotProduction || (args[1] != "on" && args[1] != "off") {
		return usageError{"usage: hi app dev <app> view [production on|off]"}
	}
	if args[1] == "off" {
		config.DevViewProductionOff = appActor() + ", " + time.Now().Format("2006-01-02")
	} else {
		config.DevViewProductionOff = ""
	}
	if err := saveTeam(config); err != nil {
		return err
	}
	if args[1] == "off" {
		fmt.Fprintln(stdout, "The dev box sees Stage only now: production's logs, jobs, and tools are off for it.")
	} else {
		fmt.Fprintln(stdout, "The dev box sees production's logs, jobs, and the app's tools that read, again.")
	}
	return nil
}
