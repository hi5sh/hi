package main

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"
)

//go:embed scripts/compute/serve-llama-cpp.sh
var serveLlamaCppScript []byte

const (
	serveRemotePort  = 8000
	serveLocalPort   = 8080
	servePollTimeout = 45 * time.Second
	serveReadyWithin = 45 * time.Minute
)

// servePollEvery is shortened in tests.
var servePollEvery = 10 * time.Second

// serveRecipe is a tested model setup, read from a recipe file
// (serve_recipe.go). Flags are hardware-specific, so a recipe names the
// hardware to use on each provider, and its arguments there.
type serveRecipe struct {
	name        string
	description string
	repo        string
	revision    string // the Hub commit served locally; rented hardware takes the latest
	quant       string
	context     int
	alias       string
	args        string              // llama-server arguments on the chosen rented provider (forProvider)
	common      []string            // arguments everywhere
	rented      map[string][]string // arguments on one rented provider, before common
	override    bool                // --args replaced the arguments
	hardware    map[string]string
	tested      string // the chosen provider's tested line (forProvider)
	tests       map[string]string
	local       map[string]localProfile // by backend
}

// serveProvider is implemented by providers whose instance runs the server
// itself and reports progress through their own API instead of SSH.
type serveProvider interface {
	serverState(name string) (string, error)
	serverURL(name string) (string, error)
}

func computeServeCommand(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	flags := newComputeFlags("serve", stderr)
	on := flags.String("on", "", "provider")
	gpu := flags.String("gpu", "", "hardware")
	name := flags.String("name", "", "instance name")
	maxLifetime := &lifetimeFlag{value: defaultInstanceMax}
	flags.Var(maxLifetime, "max", "maximum lifetime: hours (2), 30m, 2d, or none")
	quant := flags.String("quant", "", "GGUF quant")
	contextSize := flags.Int("ctx", 0, "context length")
	alias := flags.String("alias", "", "model id in the API")
	extra := flags.String("args", "", "extra llama-server arguments")
	localPort := flags.Int("port", serveLocalPort, "local port")
	yes := flags.Bool("yes", false, "skip confirmation")
	dryRun := flags.Bool("dry-run", false, "show without starting")
	namespace := flags.String("namespace", "", "account or organization to bill")
	reason := flags.String("reason", "", "why you need it (managed providers)")
	memory := flags.String("memory", "", "local: the box's memory limit")
	backend := flags.String("backend", "", "local: rocm, vulkan, cuda, or cpu")
	share := flags.Bool("share", false, "local: also on this machine's NetBird address, with a key to hand over")
	org := flags.Bool("org", false, "local: also on this machine's NetBird address, for the org server's hi org ai")
	positional, err := parseInterspersedFlags(flags, args)
	if err != nil {
		return err
	}
	if len(positional) != 1 {
		return usageError{"usage: hi serve llm [options] <recipe | owner/repo-GGUF --quant Q>\n" +
			"recipes: " + strings.Join(serveRecipeNames(), ", ")}
	}

	set := map[string]bool{}
	flags.Visit(func(f *flag.Flag) { set[f.Name] = true })
	if *on == "local" {
		if set["reason"] && !*org {
			return usageError{"--reason is for --org, where approvers see it, or for rented hardware"}
		}
		for _, name := range []string{"gpu", "namespace"} {
			if set[name] {
				return usageError{fmt.Sprintf("--%s is for rented hardware, not --on local", name)}
			}
		}
		lifetime := noLimit
		if set["max"] {
			lifetime = maxLifetime.value
		}
		if _, err := parsePort(strconv.Itoa(*localPort)); err != nil {
			return err
		}
		return serveLocalCommand(localRequest{argument: positional[0], quant: *quant, alias: *alias, extra: *extra,
			name: *name, memory: *memory, backend: *backend, context: *contextSize, port: *localPort, portSet: set["port"],
			max: lifetime, yes: *yes, dryRun: *dryRun, share: *share, org: *org, reason: *reason}, stdin, stdout, stderr)
	}
	if *on == "auto" {
		return usageError{"--on auto isn't built yet; serve with --on local, or on colab, hf, runpod, or shadeform"}
	}
	for _, name := range []string{"memory", "backend", "share", "org"} {
		if set[name] {
			return usageError{fmt.Sprintf("--%s is for --on local", name)}
		}
	}
	recipe, err := resolveServeRecipe(positional[0], *quant, *contextSize, *alias, *extra)
	if err != nil {
		return err
	}
	if _, err := parsePort(strconv.Itoa(*localPort)); err != nil {
		return err
	}

	// Without --gpu, a recipe's own hardware picks the provider when only one
	// ready provider has an entry for it.
	if *gpu == "" && *on == "" && os.Getenv("HI_COMPUTE_PROVIDER") == "" {
		var candidates []string
		for _, provider := range computeProviders {
			if status := provider.check(); status.installed && status.signedIn && recipe.hardware[provider.name()] != "" {
				candidates = append(candidates, provider.name())
			}
		}
		if len(candidates) == 1 {
			*on = candidates[0]
		}
	}
	provider, err := resolveProvider(*on, *gpu)
	if err != nil {
		return err
	}
	recipe = recipe.forProvider(provider.name())
	if *gpu == "" {
		*gpu = recipe.hardware[provider.name()]
	}
	if *gpu == "" {
		return usageError{"choose hardware with --gpu; see `hi compute hardware`"}
	}
	hardware, err := resolveHardware(provider, *gpu)
	if err != nil {
		return err
	}
	if hardware.kind != "GPU" {
		return usageError{fmt.Sprintf("serving needs a GPU; %s is a %s", hardware.name, hardware.kind)}
	}
	if err := validateLifetime(provider, maxLifetime.value); err != nil {
		return err
	}
	instance, err := chooseInstanceName(*name, hardware.name)
	if err != nil {
		return err
	}
	if recipe.tested != "" {
		fmt.Fprintf(stdout, "Recipe %s, tested on %s.\n", recipe.name, recipe.tested)
	}

	running := false
	if *name != "" {
		if instances, err := provider.list(); err == nil {
			for _, candidate := range instances {
				running = running || candidate.name == instance
			}
		}
	}
	server, apiServed := provider.(serveProvider)
	if running {
		if apiServed {
			return fmt.Errorf("%s is already running; stop it first or choose another --name", instance)
		}
		fmt.Fprintf(stdout, "Reusing %s/%s.\n", provider.name(), instance)
	} else {
		request := upRequest{name: instance, hardware: hardware, max: maxLifetime.value, namespace: *namespace, reason: *reason}
		if apiServed {
			request.serve = &recipe
		}
		if err := startInstance(provider, request, *yes, *dryRun, stdin, stdout, stderr); err != nil {
			return err
		}
	}
	if *dryRun {
		fmt.Fprintf(stdout, "Would serve %s:%s on port %d.\n", recipe.repo, recipe.quant, serveRemotePort)
		return nil
	}
	served := servedModel{Name: instance, Provider: provider.name(), Model: recipe.alias, Repo: recipe.repo,
		Quant: recipe.quant, Port: *localPort, Started: computeNow()}
	recordServed(served, stderr)

	if apiServed {
		fmt.Fprintf(stdout, "Serving %s:%s on %s; the model downloads while the server starts.\n",
			recipe.repo, recipe.quant, instance)
		if err := waitForServer(instance, stdout, func() (string, error) { return server.serverState(instance) }); err != nil {
			return err
		}
		url, err := server.serverURL(instance)
		if err != nil {
			return err
		}
		served.URL, served.Port = url, 0
		recordServed(served, stderr)
		fmt.Fprintf(stdout, "\nOpenAI-compatible API: %s/v1   model: %s\n", url, recipe.alias)
		lifetime := "It has no time limit."
		if maxLifetime.value != noLimit {
			lifetime = "It stops after " + formatDuration(maxLifetime.value) + " at " + computeNow().Add(maxLifetime.value).Format("15:04") + "."
		}
		fmt.Fprintf(stdout, "It needs your %s token as the API key. %s\n", provider.name(), lifetime)
		fmt.Fprintf(stdout, "Local tunnel instead: hi compute tunnel %s %d:%d\n", instance, serveRemotePort, *localPort)
		fmt.Fprintf(stdout, "Stop it:              hi serve stop %s\n", instance)
		return nil
	}

	target, err := reach(provider, instance, stderr)
	if err != nil {
		return err
	}
	defer reportActivity(provider, instance, "serve")()
	fmt.Fprintf(stdout, "Starting %s:%s on %s (a fresh machine needs a few minutes to build and download).\n",
		recipe.repo, recipe.quant, instance)
	if err := startRemoteServer(target, recipe, stderr); err != nil {
		return err
	}
	if err := waitForServer(instance, stdout, func() (string, error) { return sshServerState(target) }); err != nil {
		return err
	}

	fmt.Fprintf(stdout, "\nOpenAI-compatible API: http://127.0.0.1:%d/v1   model: %s\n", *localPort, recipe.alias)
	return computeTunnelCommand([]string{instance, fmt.Sprintf("%d:%d", serveRemotePort, *localPort)},
		stdin, stdout, stderr)
}

func serveRecipeNames() []string {
	names := make([]string, 0, len(serveRecipes))
	for name := range serveRecipes {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// resolveServeRecipe returns a built-in recipe, or an ad hoc one for a
// Hugging Face GGUF repository. Flags override either.
func resolveServeRecipe(argument, quant string, contextSize int, alias, extra string) (serveRecipe, error) {
	recipe, known := serveRecipes[strings.ToLower(argument)]
	if !known {
		owner, repo, ok := strings.Cut(argument, "/")
		if !ok || owner == "" || repo == "" || strings.Contains(repo, "/") {
			return serveRecipe{}, usageError{fmt.Sprintf(
				"%q is neither a recipe (%s) nor a Hugging Face repository like owner/Model-GGUF",
				argument, strings.Join(serveRecipeNames(), ", "))}
		}
		if quant == "" {
			return serveRecipe{}, usageError{"choose a quant with --quant, such as Q4_K_M"}
		}
		recipe = serveRecipe{name: argument, repo: argument, context: 32768, alias: strings.ToLower(repo)}
	}
	if quant != "" {
		recipe.quant = quant
	}
	if contextSize > 0 {
		recipe.context = contextSize
	}
	if alias != "" {
		recipe.alias = alias
	}
	if extra != "" {
		recipe.args, recipe.override = extra, true
	}
	for _, value := range []string{recipe.repo, recipe.quant, recipe.alias} {
		if strings.ContainsAny(value, "'\"`$\\ \n") {
			return serveRecipe{}, usageError{fmt.Sprintf("invalid characters in %q", value)}
		}
	}
	return recipe, nil
}

// startRemoteServer uploads the serve script over SSH and starts it detached,
// so a dropped connection never stops setup.
func startRemoteServer(target sshTarget, recipe serveRecipe, stderr io.Writer) error {
	upload := "mkdir -p ~/.hi && cat > ~/.hi/serve-llama-cpp.sh && chmod 700 ~/.hi/serve-llama-cpp.sh"
	if _, err := remoteOutput(target, bytes.NewReader(serveLlamaCppScript), 2*time.Minute, upload); err != nil {
		return fmt.Errorf("upload the serve script: %w", err)
	}
	environment := []string{
		"REPO=" + shellQuote(recipe.repo),
		"QUANT=" + shellQuote(recipe.quant),
		"CTX=" + strconv.Itoa(recipe.context),
		"PORT=" + strconv.Itoa(serveRemotePort),
		"ALIAS=" + shellQuote(recipe.alias),
		"EXTRA_ARGS=" + shellQuote(recipe.args),
	}
	start := "rm -f ~/.hi/state; " + strings.Join(environment, " ") +
		" setsid nohup bash ~/.hi/serve-llama-cpp.sh > /dev/null 2>&1 < /dev/null &"
	if _, err := remoteOutput(target, nil, time.Minute, start); err != nil {
		return fmt.Errorf("start the server: %w", err)
	}
	return nil
}

// sshServerState reads the remote state contract over a short SSH call.
func sshServerState(target sshTarget) (string, error) {
	output, err := remoteOutput(target, nil, servePollTimeout,
		"cat ~/.hi/state 2>/dev/null; du -sh /content/hi/models ~/.hi/models 2>/dev/null | head -1 | cut -f1")
	if err != nil {
		return "", err
	}
	lines := strings.Split(strings.TrimSpace(output), "\n")
	state := strings.TrimSpace(lines[0])
	if len(lines) > 1 && strings.TrimSpace(lines[1]) != "" && state != "" && !strings.HasPrefix(state, "ready") {
		state += "  (model on disk: " + strings.TrimSpace(lines[1]) + ")"
	}
	return state, nil
}

// waitForServer polls a server's state until it is ready or failed. A failed
// poll only means no update this time.
func waitForServer(instance string, stdout io.Writer, state func() (string, error)) error {
	deadline := computeNow().Add(serveReadyWithin)
	last := ""
	for computeNow().Before(deadline) {
		time.Sleep(servePollEvery)
		progress, err := state()
		if err != nil || progress == "" {
			continue
		}
		if progress != last {
			fmt.Fprintf(stdout, "  [%s] %s\n", computeNow().Format("15:04:05"), progress)
			last = progress
		}
		if strings.HasPrefix(progress, "ready") {
			return nil
		}
		if strings.HasPrefix(progress, "failed") {
			return fmt.Errorf("%s; see `hi serve logs %s`", progress, instance)
		}
	}
	return fmt.Errorf("the server was not ready after %s; see `hi serve logs %s`",
		formatDuration(serveReadyWithin), instance)
}

func remoteOutput(target sshTarget, stdin io.Reader, timeout time.Duration, command string) (string, error) {
	ssh, err := exec.LookPath("ssh")
	if err != nil {
		return "", errors.New("ssh is not installed; install openssh-client")
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	args := append([]string{"-o", "BatchMode=yes"}, target.options...)
	args = append(args, target.destination, command)
	var output bytes.Buffer
	process := exec.CommandContext(ctx, ssh, args...)
	process.Stdin = stdin
	process.Stdout = &output
	process.Stderr = &output
	if err := process.Run(); err != nil {
		if ctx.Err() != nil {
			return output.String(), fmt.Errorf("no answer within %s", timeout)
		}
		return output.String(), fmt.Errorf("%w: %s", err, strings.TrimSpace(output.String()))
	}
	return output.String(), nil
}

func computeLogsCommand(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	flags := newComputeFlags("logs", stderr)
	follow := flags.Bool("follow", false, "keep printing new lines")
	lines := flags.Int("n", 40, "lines per log")
	positional, err := parseInterspersedFlags(flags, args)
	if err != nil {
		return err
	}
	if len(positional) != 1 {
		return usageError{"usage: hi compute logs <name> [--follow] [-n lines]"}
	}
	provider, name, err := findInstance(positional[0])
	if err != nil {
		return err
	}
	return provider.logs(name, *follow, *lines, stdin, stdout, stderr)
}

// sshLogs prints the remote state contract's logs over SSH.
func sshLogs(provider computeProvider, name string, follow bool, lines int, stdin io.Reader, stdout, stderr io.Writer) error {
	target, err := reach(provider, name, stderr)
	if err != nil {
		return err
	}
	defer reportActivity(provider, name, "logs")()
	tail := fmt.Sprintf("tail -n %d", lines)
	if follow {
		tail += " -F"
	}
	command := "cat ~/.hi/state 2>/dev/null; ls ~/.hi/logs/*.log >/dev/null 2>&1 && " + tail +
		" ~/.hi/logs/*.log || echo 'No logs yet.'"
	sshArgs := append(append([]string{}, target.options...), target.destination, command)
	return runSSH(sshArgs, stdin, stdout, stderr)
}
