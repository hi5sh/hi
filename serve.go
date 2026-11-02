package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"time"
)

// hi serve llm serves a model, wherever it runs; today on rented hardware,
// as hi compute serve always did (hi_org.md). hi compute serve still runs
// it. Which instances serve which model is kept in serving.json next to
// the compute records, so hi serve ls can show them with their API.

// servedModel is one thing hi serve started: on rented hardware, where
// Provider names the provider, or on this machine, where it is "local".
// Records written before v2.6.59 have no kind, and read as llm.
type servedModel struct {
	Name     string    `json:"name"`
	Provider string    `json:"provider"`
	Kind     string    `json:"kind,omitempty"`
	Recipe   string    `json:"recipe,omitempty"`
	Model    string    `json:"model"` // the id in the API, the recipe's alias
	Repo     string    `json:"repo"`
	Quant    string    `json:"quant"`
	URL      string    `json:"url,omitempty"` // the provider's own address; "" when reached through a tunnel
	Port     int       `json:"port,omitempty"`
	Started  time.Time `json:"started"`

	// On this machine only.
	Revision string    `json:"revision,omitempty"`
	Backend  string    `json:"backend,omitempty"`
	Image    string    `json:"image,omitempty"`
	Need     int64     `json:"need,omitempty"`   // bytes in all, GPU and host
	Memory   int64     `json:"memory,omitempty"` // the box's limit, in bytes
	Context  int       `json:"context,omitempty"`
	Args     []string  `json:"args,omitempty"`
	ModelDir string    `json:"model_dir,omitempty"` // under ~/.local/share/hi/models
	Deadline time.Time `json:"deadline,omitzero"`
	// Shared: a key to hand over; Org: named to the org server. Either
	// publishes the front on Address, this machine's NetBird address.
	Shared     bool   `json:"shared,omitempty"`
	Org        bool   `json:"org,omitempty"`
	OrgRequest string `json:"org_request,omitempty"`
	Address    string `json:"address,omitempty"`
}

func (s servedModel) kind() string {
	if s.Kind == "" {
		return "llm"
	}
	return s.Kind
}

func runServe(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printServeUsage(stdout)
		return 0
	}
	command, rest := args[0], args[1:]
	switch command {
	case "help", "-h", "--help":
		printServeUsage(stdout)
		return 0
	case "__front":
		return runServeFront(rest, stderr)
	case "llm":
		if len(rest) == 0 {
			return exitCode(serveRecipesCommand([]string{"llm"}, stdout, stderr), stderr)
		}
		return runCompute(append([]string{"serve"}, rest...), stdin, stdout, stderr)
	case "web", "files", "db":
		fmt.Fprintf(stderr, "hi: hi serve %s isn't built yet; hi serve llm is\n", command)
		return 1
	case "ls":
		return runCompute(append([]string{"__serve-ls"}, rest...), stdin, stdout, stderr)
	case "detect":
		return exitCode(serveDetectCommand(rest, stdout, stderr), stderr)
	case "recipes":
		return exitCode(serveRecipesCommand(rest, stdout, stderr), stderr)
	case "pull":
		return exitCode(servePullCommand(rest, stdin, stdout, stderr), stderr)
	case "models":
		return exitCode(serveModelsCommand(rest, stdin, stdout, stderr), stderr)
	case "start":
		return exitCode(serveStartCommand(rest, stdout, stderr), stderr)
	case "rm":
		return exitCode(serveRemoveCommand(rest, stdout, stderr), stderr)
	case "status":
		return exitCode(serveStatusCommand(rest, stdout, stderr), stderr)
	case "key":
		return exitCode(serveKeyCommand(rest, stdout, stderr), stderr)
	case "stop", "logs":
		if len(rest) == 0 {
			fmt.Fprintf(stderr, "usage: hi serve %s <name>\n", command)
			return 2
		}
		if record, ok := localRecord(rest); ok {
			if command == "stop" {
				return exitCode(localStop(record, stdout, stderr), stderr)
			}
			return exitCode(localLogs(record, rest, stdout, stderr), stderr)
		}
		if code := serveKnows(rest, stderr, command); code != 0 {
			return code
		}
		return runCompute(append([]string{command}, rest...), stdin, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "hi: unknown serve command %q\n\n", command)
		printServeUsage(stderr)
		return 2
	}
}

// localRecord is the record of a thing served on this machine that args
// name, if they name one.
func localRecord(args []string) (servedModel, bool) {
	served, err := loadServed()
	if err != nil {
		return servedModel{}, false
	}
	for _, arg := range args {
		if record, ok := served[arg]; ok && record.Provider == "local" && !strings.HasPrefix(arg, "-") {
			return record, true
		}
	}
	return servedModel{}, false
}

func printServeUsage(w io.Writer) {
	fmt.Fprintln(w, `hi serve serves a model with an OpenAI-compatible API, on rented hardware
or on this machine.

Usage:
  hi serve llm <recipe | owner/repo-GGUF --quant Q> [options]
                                 Serve a GGUF model with llama.cpp: on rented hardware, with
                                 its API tunnelled here (also: hi compute serve), or with
                                 --on local, on this machine's GPU
  hi serve llm                   The llm recipes
  hi serve ls [--json]           What you serve, here and rented: where, its API, its state
  hi serve status <name>         Its address, health, memory, and image
  hi serve logs <name> [--follow]
                                 The server's setup and logs
  hi serve stop <name>           Rented: stop it, and the machine under it.
                                 Local: stop it, and keep it to start again
  hi serve start <name> [--max <duration>]
                                 Start a stopped local one again, on the same port
  hi serve rm <name>             Stop a local one and forget it; its model stays
  hi serve key <name> [--new]    A shared one's key to hand over, or a new one

  hi serve detect                What this machine can serve on: GPU, Podman, memory, disk
  hi serve recipes [llm]         The recipes, where each runs, and whether it fits here
  hi serve pull llm <recipe>     Download a model and its image now, and serve nothing
  hi serve models [--json]       Models downloaded here, their size, and what uses them
  hi serve models rm <owner/repo>
                                 Delete a downloaded model that nothing serves

Options for llm:
  --on <where>       local, or colab, hf, runpod, or shadeform (default: the recipe's
                     provider, or the only signed-in one)
  --gpu <hardware>   Hardware name from hi compute hardware (recipes have a default)
  --name <name>      Instance name; an existing one is reused (rented)
  --max <duration>   Stop after this long, e.g. 30m or 4h (local: none by default)
  --quant <quant>    GGUF quant, e.g. Q4_K_M (recipes have a default)
  --ctx <tokens>     Context length
  --alias <id>       The model's id in the API
  --args "<args>"    Extra llama-server arguments
  --port <port>      Local port for the API (default 8080; local: the next free one)
  --memory <size>    Local: the box's memory limit (default: the recipe's)
  --backend rocm     Local: the GPU backend (rocm; vulkan comes later)
  --share            Local: also on this machine's NetBird address, with a key you hand
                     to teammates (hi serve key)
  --org              Local: also on the NetBird address, for the org server, which an
                     admin approves; devices then use it through hi org ai as local/<name>
  --reason <text>    Why you need it; approvers see it (managed providers, --org)
  --yes, --dry-run

Recipes: `+fmt.Sprint(serveRecipeNames()))
}

// serveKnows refuses a name hi serve didn't start, so hi serve stop only
// ever stops a model server.
func serveKnows(args []string, stderr io.Writer, command string) int {
	positional := []string{}
	for _, arg := range args {
		if len(arg) > 0 && arg[0] != '-' {
			positional = append(positional, arg)
		}
	}
	if len(positional) != 1 {
		fmt.Fprintf(stderr, "usage: hi serve %s <name>\n", command)
		return 2
	}
	served, err := loadServed()
	if err != nil {
		return exitCode(err, stderr)
	}
	name := instanceName(positional[0])
	if _, ok := served[name]; !ok {
		fmt.Fprintf(stderr, "hi: %s doesn't serve a model hi serve started; see `hi serve ls`, or `hi compute %s %s`\n", name, command, name)
		return 1
	}
	return 0
}

// serveListCommand shows the served models that are still up, and forgets
// the rest.
func serveListCommand(args []string, stdout, stderr io.Writer) error {
	flags := newComputeFlags("serve ls", stderr)
	asJSON := flags.Bool("json", false, "print JSON")
	positional, err := parseInterspersedFlags(flags, args)
	if err != nil {
		return err
	}
	if len(positional) != 0 {
		return usageError{"usage: hi serve ls [--json]"}
	}
	served, err := loadServed()
	if err != nil {
		return err
	}
	type row struct {
		servedModel
		State string    `json:"state"`
		Stops time.Time `json:"stops,omitzero"`
	}
	var rows []row
	rented := false
	for _, model := range served {
		rented = rented || model.Provider != "local"
	}
	var up map[string]listedInstance
	if rented {
		listed, err := listInstances(io.Discard, stderr)
		if err != nil {
			return err
		}
		up = map[string]listedInstance{}
		for _, item := range listed {
			up[item.provider+"/"+item.instance.name] = item
		}
	}
	forgot := false
	for name, model := range served {
		if model.Provider == "local" {
			rows = append(rows, row{servedModel: model, State: localState(model).text, Stops: model.Deadline})
			continue
		}
		item, ok := up[model.Provider+"/"+name]
		if !ok {
			delete(served, name)
			forgot = true
			continue
		}
		r := row{servedModel: model, State: "running"}
		switch {
		case item.record != nil:
			r.Stops = item.record.Deadline
		case item.instance.managed:
			r.Stops = item.instance.deadline
		}
		rows = append(rows, r)
	}
	if forgot {
		if err := writeServed(served); err != nil {
			return err
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Started.Before(rows[j].Started) })
	if *asJSON {
		if rows == nil {
			rows = []row{}
		}
		data, _ := json.MarshalIndent(rows, "", "  ")
		fmt.Fprintln(stdout, string(data))
		return nil
	}
	if len(rows) == 0 {
		fmt.Fprintln(stdout, "No models are being served. Start one with hi serve llm <recipe>.")
		return nil
	}
	now := computeNow()
	table := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(table, "NAME\tWHERE\tMODEL\tAPI\tSTATE\tSTOPS IN")
	tunnelled := ""
	for _, r := range rows {
		api := r.URL + "/v1"
		if r.URL == "" {
			api = "tunnel"
			tunnelled = r.Name
		}
		where := r.Provider
		var shared []string
		if r.Shared {
			shared = append(shared, "shared")
		}
		if r.Org {
			shared = append(shared, "org")
		}
		if len(shared) > 0 {
			where += " (" + strings.Join(shared, ", ") + ")"
		}
		fmt.Fprintf(table, "%s\t%s\t%s\t%s\t%s\t%s\n", r.Name, where, r.Model, api, r.State, stopsInText(r.Stops, now))
	}
	table.Flush()
	if tunnelled != "" {
		fmt.Fprintf(stdout, "\nA tunnelled API is reached while its tunnel runs: hi compute tunnel %s %d:%d\n",
			tunnelled, serveRemotePort, serveLocalPort)
	}
	return nil
}

func servedPath() (string, error) {
	directory, err := computeStateDirectory()
	if err != nil {
		return "", err
	}
	return filepath.Join(directory, "serving.json"), nil
}

func loadServed() (map[string]servedModel, error) {
	path, err := servedPath()
	if err != nil {
		return nil, err
	}
	served := map[string]servedModel{}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return served, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &served); err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return served, nil
}

func writeServed(served map[string]servedModel) error {
	path, err := servedPath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(served, "", "  ")
	if err != nil {
		return err
	}
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}

// recordServed notes a model hi serve started; a failure to write it only
// leaves the model out of hi serve ls.
func recordServed(model servedModel, stderr io.Writer) {
	served, err := loadServed()
	if err == nil {
		served[model.Name] = model
		err = writeServed(served)
	}
	if err != nil {
		fmt.Fprintf(stderr, "hi: warning: hi serve ls won't show %s: %v\n", model.Name, err)
	}
}
