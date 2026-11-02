package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"text/tabwriter"
	"time"
)

// hi serve llm serves a model, wherever it runs; today on rented hardware,
// as hi compute serve always did (hi_org.md). hi compute serve still runs
// it. Which instances serve which model is kept in serving.json next to
// the compute records, so hi serve ls can show them with their API.

// servedModel is one model hi serve started.
type servedModel struct {
	Name     string    `json:"name"`
	Provider string    `json:"provider"`
	Model    string    `json:"model"` // the id in the API, the recipe's alias
	Repo     string    `json:"repo"`
	Quant    string    `json:"quant"`
	URL      string    `json:"url,omitempty"` // the provider's own address; "" when reached through a tunnel
	Port     int       `json:"port,omitempty"`
	Started  time.Time `json:"started"`
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
	case "llm":
		return runCompute(append([]string{"serve"}, rest...), stdin, stdout, stderr)
	case "ls":
		return runCompute(append([]string{"__serve-ls"}, rest...), stdin, stdout, stderr)
	case "stop", "logs":
		if len(rest) == 0 {
			fmt.Fprintf(stderr, "usage: hi serve %s <name>\n", command)
			return 2
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

func printServeUsage(w io.Writer) {
	fmt.Fprintln(w, `hi serve serves a model with an OpenAI-compatible API.

Usage:
  hi serve llm <recipe | owner/repo-GGUF --quant Q> [options]
                                 Serve a GGUF model with llama.cpp on rented hardware,
                                 and tunnel its API here (also: hi compute serve)
  hi serve ls [--json]           The models you serve: where, their API, and until when
  hi serve logs <name> [--follow]
                                 The server's setup and logs
  hi serve stop <name>           Stop it, and the machine under it

Options for llm:
  --on <provider>    colab, hf, runpod, or shadeform (default: the recipe's, or the
                     only signed-in provider)
  --gpu <hardware>   Hardware name from hi compute hardware (recipes have a default)
  --name <name>      Instance name; an existing one is reused
  --max <duration>   Stop after this long, e.g. 30m or 4h
  --quant <quant>    GGUF quant, e.g. Q4_K_M (recipes have a default)
  --ctx <tokens>     Context length
  --alias <id>       The model's id in the API
  --args "<args>"    Extra llama-server arguments
  --port <port>      Local port for the API (default 8080)
  --reason <text>    Why you need it; approvers see it (managed providers)
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
		Stops time.Time `json:"stops,omitzero"`
	}
	var rows []row
	if len(served) > 0 {
		listed, err := listInstances(io.Discard, stderr)
		if err != nil {
			return err
		}
		up := map[string]listedInstance{}
		for _, item := range listed {
			up[item.provider+"/"+item.instance.name] = item
		}
		for name, model := range served {
			item, ok := up[model.Provider+"/"+name]
			if !ok {
				delete(served, name)
				continue
			}
			r := row{servedModel: model}
			switch {
			case item.record != nil:
				r.Stops = item.record.Deadline
			case item.instance.managed:
				r.Stops = item.instance.deadline
			}
			rows = append(rows, r)
		}
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
	fmt.Fprintln(table, "NAME\tPROVIDER\tMODEL\tAPI\tSTOPS IN")
	tunnelled := ""
	for _, r := range rows {
		api := r.URL + "/v1"
		if r.URL == "" {
			api = "tunnel"
			tunnelled = r.Name
		}
		fmt.Fprintf(table, "%s\t%s\t%s\t%s\t%s\n", r.Name, r.Provider, r.Model, api, stopsInText(r.Stops, now))
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
