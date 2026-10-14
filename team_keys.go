package main

// A team's keys and settings: the OpenRouter key its model calls use (hi
// team key), and settings its app boxes get as environment variables (hi
// team env). Both live in secrets.json. No box ever holds the OpenRouter
// key: the lead's model calls go through its proxy, and an app's through
// the broker's models socket, which a relay in the app's box puts at an
// ordinary address. Only app boxes get the settings; the lead sees their
// names.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	teamModelsSocket = "models.sock"
	// teamAppModelsDir is where an app's box sees the models socket, and
	// teamAppModelsPort where the relay in the box listens.
	teamAppModelsDir  = "/hi-models"
	teamAppModelsPort = 3129
)

var teamOpenRouterUpstream = "https://openrouter.ai"

var teamEnvName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// teamAppEnvReserved are the variables hi sets in an app's box itself.
var teamAppEnvReserved = map[string]bool{"HOME": true, "PORT": true, "HOST": true, "PATH": true,
	"HI_PUBLIC_URL_FILE": true, "HI_DATA_DIR": true, "OPENROUTER_BASE_URL": true, "OPENROUTER_API_KEY": true}

// teamModelsDir holds the broker's models socket; app boxes mount it.
func teamModelsDir(name string) string { return teamBrokerDir(name) + "-models" }

// teamKeysDir holds the team's OpenRouter key as a .env file for its
// proxy, which reads it again for each request.
func teamKeysDir(name string) string { return filepath.Join(teamDir(name), "run", "keys") }

// writeTeamKeys puts the team's OpenRouter key where its proxy reads it,
// or removes it, so the proxy takes this machine's again.
func writeTeamKeys(name string, secrets teamSecrets) error {
	dir := teamKeysDir(name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	file := filepath.Join(dir, "openrouter.env")
	if secrets.OpenRouterAPIKey == "" {
		if err := os.Remove(file); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	return os.WriteFile(file, []byte("OPENROUTER_API_KEY="+secrets.OpenRouterAPIKey+"\n"), 0o600)
}

// teamOpenRouterKey is the team's own key, or else this machine's, from
// Hermes' .env.
func teamOpenRouterKey(name string) (string, error) {
	if secrets, err := loadTeamSecrets(name); err == nil && secrets.OpenRouterAPIKey != "" {
		return secrets.OpenRouterAPIKey, nil
	}
	return readDotEnvValue(hermesEnvPath(), "OPENROUTER_API_KEY")
}

// teamCheckOpenRouterKey asks OpenRouter whether it knows the key; tests
// replace it.
var teamCheckOpenRouterKey = func(key string) error {
	request, _ := http.NewRequest("GET", teamOpenRouterUpstream+"/api/v1/key", nil)
	request.Header.Set("Authorization", "Bearer "+key)
	response, err := (&http.Client{Timeout: 20 * time.Second}).Do(request)
	if err != nil {
		return fmt.Errorf("OpenRouter can't be reached to check the key: %w", err)
	}
	response.Body.Close()
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		return errors.New("OpenRouter refused the key; copy it again from openrouter.ai/settings/keys")
	}
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("OpenRouter answered %s when checking the key", response.Status)
	}
	return nil
}

// teamAskOpenRouterKey asks for an OpenRouter key; Enter gives "", for
// this machine's.
func teamAskOpenRouterKey(stdin io.Reader, stdout io.Writer, question string) (string, error) {
	for attempt := 1; ; attempt++ {
		key, err := teamReadSecret(stdin, stdout, question)
		if err == nil && key != "" {
			err = teamCheckOpenRouterKey(key)
		}
		if err == nil {
			return key, nil
		}
		if !isTerminal(stdin) || attempt == 5 || errors.Is(err, io.EOF) {
			return "", err
		}
		fmt.Fprintf(stdout, "  %v. Try again.\n", err)
	}
}

// teamSettingsStamp changes when what an app's box gets changes, so its
// box starts again: the settings, and how hi gives them.
func teamSettingsStamp(name string) string {
	secrets, _ := loadTeamSecrets(name)
	keys := make([]string, 0, len(secrets.Env))
	for key := range secrets.Env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	hash := sha256.New()
	for _, key := range keys {
		fmt.Fprintf(hash, "%s=%s\n", key, secrets.Env[key])
	}
	return "v1:" + hex.EncodeToString(hash.Sum(nil))[:16]
}

// teamAppEnv is what an app's box gets: its port, its address file, the
// models at an ordinary address with a placeholder key, and the team's
// settings.
func teamAppEnv(name string, port int) map[string]string {
	env := map[string]string{}
	if secrets, err := loadTeamSecrets(name); err == nil {
		for key, value := range secrets.Env {
			env[key] = value
		}
	}
	env["HOME"], env["PORT"], env["HOST"] = "/tmp", strconv.Itoa(port), "0.0.0.0"
	env["HI_PUBLIC_URL_FILE"] = teamAppAddressFile
	env["HI_DATA_DIR"] = teamAppDataDir
	env["OPENROUTER_BASE_URL"] = fmt.Sprintf("http://127.0.0.1:%d/api/v1", teamAppModelsPort)
	env["OPENROUTER_API_KEY"] = boxClaudePlacehold
	return env
}

// writeTeamAppEnvFile writes env for --env-file, so the settings stay out
// of the engine's command line, where anyone on the machine can see them.
func writeTeamAppEnvFile(name string, env map[string]string) (string, error) {
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var text strings.Builder
	for _, key := range keys {
		text.WriteString(key + "=" + env[key] + "\n")
	}
	file, err := os.CreateTemp(filepath.Join(teamDir(name), "run"), ".app-env-")
	if err != nil {
		return "", err
	}
	defer file.Close()
	if _, err := file.WriteString(text.String()); err != nil {
		os.Remove(file.Name())
		return "", err
	}
	return file.Name(), nil
}

// ---------------------------------------------------------------------------
// the models socket

// serveModels answers app boxes' model calls on the socket in the models
// folder, passing them on to OpenRouter with the team's key.
func (b *teamBroker) serveModels(ctx context.Context) error {
	dir := teamModelsDir(b.team)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	socket := filepath.Join(dir, teamModelsSocket)
	os.Remove(socket)
	listener, err := net.Listen("unix", socket)
	if err != nil {
		return err
	}
	os.Chmod(socket, 0o600)
	server := &http.Server{Handler: newTeamModelsProxy(b.team, teamOpenRouterUpstream)}
	go func() {
		<-ctx.Done()
		server.Close()
	}()
	if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// newTeamModelsProxy passes OpenRouter API calls on to upstream with the
// team's key in place of whatever key the app sent.
func newTeamModelsProxy(team, upstream string) http.Handler {
	target, _ := url.Parse(upstream)
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.FlushInterval = -1
	director := proxy.Director
	proxy.Director = func(r *http.Request) {
		director(r)
		r.Host = target.Host
		r.Header.Del("Authorization")
		if key, err := teamOpenRouterKey(team); err == nil {
			r.Header.Set("Authorization", "Bearer "+key)
		}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/v1/") {
			http.Error(w, "only OpenRouter's API is here: "+fmt.Sprintf("http://127.0.0.1:%d/api/v1/...", teamAppModelsPort), http.StatusNotFound)
			return
		}
		proxy.ServeHTTP(w, r)
	})
}

// runTeamApp is hi team __app, an app box's first process: it relays
// 127.0.0.1:<port> to the models socket, then runs the app's command and
// ends as it does.
func runTeamApp(args []string, stderr io.Writer) int {
	if len(args) != 3 {
		fmt.Fprintln(stderr, "usage: hi team __app <models socket> <port> <command>")
		return 2
	}
	socket, port, command := args[0], args[1], args[2]
	if listener, err := net.Listen("tcp", "127.0.0.1:"+port); err != nil {
		fmt.Fprintf(stderr, "hi: models aren't at 127.0.0.1:%s this time: %v\n", port, err)
	} else {
		go func() {
			for {
				conn, err := listener.Accept()
				if err != nil {
					return
				}
				go teamRelay(conn, socket)
			}
		}()
	}
	cmd := exec.Command("sh", "-c", command)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, stderr
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(stderr, "hi: %v\n", err)
		return 1
	}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	go func() {
		for sig := range signals {
			cmd.Process.Signal(sig)
		}
	}()
	err := cmd.Wait()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	if err != nil {
		return 1
	}
	return 0
}

func teamRelay(conn net.Conn, socket string) {
	defer conn.Close()
	upstream, err := net.Dial("unix", socket)
	if err != nil {
		return
	}
	defer upstream.Close()
	done := make(chan struct{}, 2)
	go func() { io.Copy(upstream, conn); done <- struct{}{} }()
	go func() { io.Copy(conn, upstream); done <- struct{}{} }()
	<-done
}

// ---------------------------------------------------------------------------
// the commands

// teamKey is hi team key <name> openrouter [--unset].
func teamKey(args []string, stdin io.Reader, stdout io.Writer) error {
	usage := usageError{"usage: hi team key <name> openrouter [--unset]"}
	var words []string
	unset := false
	for _, arg := range args {
		if arg == "--unset" {
			unset = true
		} else {
			words = append(words, arg)
		}
	}
	if len(words) != 2 || words[1] != "openrouter" {
		return usage
	}
	name := words[0]
	if _, err := loadTeam(name); err != nil {
		return err
	}
	secrets, err := loadTeamSecrets(name)
	if err != nil {
		return fmt.Errorf("the team's secrets: %w", err)
	}
	if unset {
		secrets.OpenRouterAPIKey = ""
	} else {
		fmt.Fprintln(stdout, "The team's OpenRouter key (openrouter.ai/settings/keys). No box ever holds it.")
		key, err := teamAskOpenRouterKey(stdin, stdout, "OpenRouter key")
		if err != nil {
			return err
		}
		if key == "" {
			return errors.New("no key was entered; hi team key " + name + " openrouter --unset uses this machine's")
		}
		secrets.OpenRouterAPIKey = key
	}
	if err := saveTeamSecrets(name, secrets); err != nil {
		return err
	}
	if err := writeTeamKeys(name, secrets); err != nil {
		return err
	}
	if unset {
		fmt.Fprintf(stdout, "Team %s uses this machine's OpenRouter key again (Hermes').\n", name)
	} else {
		fmt.Fprintf(stdout, "Team %s's model calls use its own OpenRouter key now: the lead's, and the app's at $OPENROUTER_BASE_URL.\n", name)
	}
	return nil
}

// teamEnv is hi team env <name> [KEY=VALUE | KEY | --unset KEY]...
func teamEnv(args []string, stdin io.Reader, stdout io.Writer) error {
	if len(args) == 0 {
		return usageError{"usage: hi team env <name> [KEY=VALUE | KEY | --unset KEY]..."}
	}
	name := args[0]
	if _, err := loadTeam(name); err != nil {
		return err
	}
	secrets, err := loadTeamSecrets(name)
	if err != nil {
		return fmt.Errorf("the team's secrets: %w", err)
	}
	if len(args) == 1 {
		names := teamEnvNames(secrets)
		if len(names) == 0 {
			fmt.Fprintf(stdout, "Team %s has no settings. Add one: hi team env %s KEY=VALUE\n", name, name)
			return nil
		}
		fmt.Fprintf(stdout, "Team %s's app gets: %s\n", name, strings.Join(names, ", "))
		return nil
	}
	if secrets.Env == nil {
		secrets.Env = map[string]string{}
	}
	var set, unset []string
	for i := 1; i < len(args); i++ {
		arg := args[i]
		if arg == "--unset" {
			if i+1 >= len(args) {
				return usageError{"usage: hi team env <name> --unset KEY"}
			}
			i++
			if _, ok := secrets.Env[args[i]]; !ok {
				return fmt.Errorf("team %s has no setting %s", name, args[i])
			}
			delete(secrets.Env, args[i])
			unset = append(unset, args[i])
			continue
		}
		key, value, given := strings.Cut(arg, "=")
		if !teamEnvName.MatchString(key) {
			return fmt.Errorf("%q isn't a variable name: letters, digits, and _, not starting with a digit", key)
		}
		if teamAppEnvReserved[key] {
			return fmt.Errorf("hi sets %s in the app's box itself", key)
		}
		if !given {
			if value, err = teamReadSecret(stdin, stdout, key); err != nil {
				return err
			}
			if value == "" {
				return fmt.Errorf("no value for %s was entered", key)
			}
		}
		if strings.ContainsAny(value, "\r\n") {
			return fmt.Errorf("%s's value has a line break; a setting is one line", key)
		}
		secrets.Env[key] = value
		set = append(set, key)
	}
	if err := saveTeamSecrets(name, secrets); err != nil {
		return err
	}
	if len(set) > 0 {
		fmt.Fprintf(stdout, "Team %s's app gets %s.\n", name, strings.Join(set, ", "))
	}
	if len(unset) > 0 {
		fmt.Fprintf(stdout, "Team %s's app no longer gets %s.\n", name, strings.Join(unset, ", "))
	}
	fmt.Fprintln(stdout, "The next preview has it, and hi restarts production on the same link within a minute while the team is up.")
	return nil
}

func teamEnvNames(secrets teamSecrets) []string {
	names := make([]string, 0, len(secrets.Env))
	for key := range secrets.Env {
		names = append(names, key)
	}
	sort.Strings(names)
	return names
}
