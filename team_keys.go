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
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
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
	teamAppModelsDir = "/hi-models"
	// teamAppRelay is hi in an app's box, off its PATH: it starts the app
	// and relays its model calls, and nothing else of hi works there, so
	// an app that looks for hi doesn't find it (and try hi agent).
	teamAppRelay      = "/usr/local/libexec/hi-relay"
	teamAppModelsPort = 3129
)

var teamOpenRouterUpstream = "https://openrouter.ai"

var teamEnvName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// A file setting is in its app's boxes, read-only, in teamAppFilesDir,
// named as its variable, which holds its path there.
const (
	teamAppFilesDir    = "/hi-files"
	teamFileSettingMax = 32 << 10
)

// teamSlotDev is the dev box, for its own settings (hi team env --dev).
const teamSlotDev = "dev"

// teamSlotFilesDir is a slot's file settings on the machine, written as
// its box starts.
func teamSlotFilesDir(team, slot string) string {
	return filepath.Join(teamDir(team), "run", slot+"-files")
}

// teamAppEnvReserved are the variables hi sets in an app's box itself.
var teamAppEnvReserved = map[string]bool{"HOME": true, "PORT": true, "HOST": true, "PATH": true,
	"HI_PUBLIC_URL_FILE": true, "HI_DATA_DIR": true, "HI_SLOT": true, "HI_WORKERS_FILE": true, "OPENROUTER_BASE_URL": true, "OPENROUTER_API_KEY": true}

// teamModelsDir holds the broker's models socket; app boxes mount it.
func teamModelsDir(name string) string { return teamBrokerDir(name) + "-models" }

// teamKeysDir holds the team's OpenRouter key as a .env file for its
// proxy, which reads it again for each request.
func teamKeysDir(name string) string { return filepath.Join(teamDir(name), "run", "keys") }

// writeTeamKeys puts the team's OpenRouter key where its proxy reads it,
// or removes it, so the proxy takes this machine's again; and the same
// for its Claude token.
func writeTeamKeys(name string, secrets teamSecrets) error {
	dir := teamKeysDir(name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	// The team's Claude token, which members' proxies read before this
	// machine's sign-in (team_subscriptions.go).
	claude := filepath.Join(dir, teamClaudeKeyFile)
	if secrets.ClaudeToken == "" {
		if err := os.Remove(claude); err != nil && !os.IsNotExist(err) {
			return err
		}
	} else if err := writeFileAtomic(claude, []byte(secrets.ClaudeToken+"\n"), 0o600); err != nil {
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
	if teamStyled(stdin, stdout) {
		return teamFieldAsk(stdin, stdout, question, true, func(key string) error {
			if key == "" {
				return nil
			}
			return teamCheckOpenRouterKey(key)
		})
	}
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
// box starts again: the settings, how hi gives them, and hi itself, which
// the box runs from a copy made when it started (hi team __app, the
// models' relay), so an update reaches the app boxes too.
func teamSettingsStamp(name string) string {
	secrets, _ := loadTeamSecrets(name)
	hash := sha256.New()
	fmt.Fprintf(hash, "hi=%s\n", teamStampVersion)
	for _, set := range []struct {
		prefix string
		env    map[string]string
	}{{"", secrets.Env}, {"production:", secrets.EnvProduction}, {"preview:", secrets.EnvPreview},
		{"file:", secrets.Files}, {"file:production:", secrets.FilesProduction}, {"file:preview:", secrets.FilesPreview}} {
		keys := make([]string, 0, len(set.env))
		for key := range set.env {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			fmt.Fprintf(hash, "%s%s=%s\n", set.prefix, key, set.env[key])
		}
	}
	return "v1:" + hex.EncodeToString(hash.Sum(nil))[:16]
}

// teamAppEnv is what an app's box gets: its port, its address file, its
// slot ($HI_SLOT, preview or production, so work that costs money runs in
// production only), the models at an ordinary address with a placeholder
// key, and the team's settings: those for both slots, and the slot's own.
func teamAppEnv(name, slot string, port int) map[string]string {
	env := map[string]string{}
	if secrets, err := loadTeamSecrets(name); err == nil {
		own := secrets.EnvPreview
		if slot == teamSlotProduction {
			own = secrets.EnvProduction
		}
		for _, set := range []map[string]string{secrets.Env, own} {
			for key, value := range set {
				env[key] = value
			}
		}
		for key := range teamAppFiles(secrets, slot) {
			env[key] = teamAppFilesDir + "/" + key
		}
	}
	env["HOME"], env["PORT"], env["HOST"] = "/tmp", strconv.Itoa(port), "0.0.0.0"
	env["HI_PUBLIC_URL_FILE"] = teamAppAddressFile
	env["HI_DATA_DIR"] = teamAppDataDir
	env["HI_SLOT"] = slot
	// The app's long work reports here, as files or with "$HI" team job
	// (team_jobs.go).
	env["HI_JOBS_DIR"] = teamAppJobsDir
	// The app's background workers run while this file exists
	// (team_workers.go).
	env["HI_WORKERS_FILE"] = teamAppWorkersFile
	env["HI"] = teamAppRelay
	env["OPENROUTER_BASE_URL"] = fmt.Sprintf("http://127.0.0.1:%d/api/v1", teamAppModelsPort)
	env["OPENROUTER_API_KEY"] = boxClaudePlacehold
	return env
}

// teamAppFiles are a slot's file settings, by name: those for both slots,
// and the slot's own.
func teamAppFiles(secrets teamSecrets, slot string) map[string]string {
	if slot == teamSlotDev {
		return maps.Clone(secrets.FilesDev)
	}
	own := secrets.FilesPreview
	if slot == teamSlotProduction {
		own = secrets.FilesProduction
	}
	files := map[string]string{}
	for _, set := range []map[string]string{secrets.Files, own} {
		for key, value := range set {
			files[key] = value
		}
	}
	return files
}

// writeTeamAppFiles writes a slot's file settings for its box to mount,
// each in place, so a box still running reads the new one, and removes
// those it no longer gets.
func writeTeamAppFiles(team, slot string) (string, error) {
	dir := teamSlotFilesDir(team, slot)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	secrets, err := loadTeamSecrets(team)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	files := teamAppFiles(secrets, slot)
	for key, value := range files {
		data, err := base64.StdEncoding.DecodeString(value)
		if err != nil {
			return "", fmt.Errorf("the file %s: %w", key, err)
		}
		if err := writeFileAtomic(filepath.Join(dir, key), data, 0o600); err != nil {
			return "", err
		}
	}
	entries, _ := os.ReadDir(dir)
	for _, entry := range entries {
		if _, ok := files[entry.Name()]; !ok {
			os.Remove(filepath.Join(dir, entry.Name()))
		}
	}
	return dir, nil
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

// teamSlotModelsDir is the folder of a slot's models socket, which the
// slot's box mounts: each slot has its own, so the preview's calls are
// counted against its daily limit and production's aren't.
func teamSlotModelsDir(name, slot string) string { return filepath.Join(teamModelsDir(name), slot) }

// serveModels answers app boxes' model calls on each slot's socket,
// passing them on to OpenRouter with the team's key. The socket in the
// models folder itself is for boxes started by a hi from before the
// slots had their own, as production, until they start again.
func (b *teamBroker) serveModels(ctx context.Context) error {
	sockets := map[string]string{
		filepath.Join(teamModelsDir(b.team), teamModelsSocket):                         teamSlotProduction,
		filepath.Join(teamSlotModelsDir(b.team, teamSlotProduction), teamModelsSocket): teamSlotProduction,
		filepath.Join(teamSlotModelsDir(b.team, teamSlotPreview), teamModelsSocket):    teamSlotPreview,
	}
	errs := make(chan error, len(sockets))
	for socket, slot := range sockets {
		if err := os.MkdirAll(filepath.Dir(socket), 0o700); err != nil {
			return err
		}
		os.Remove(socket)
		listener, err := net.Listen("unix", socket)
		if err != nil {
			return err
		}
		os.Chmod(socket, 0o600)
		server := &http.Server{Handler: newTeamModelsProxy(b.team, teamOpenRouterUpstream, slot)}
		go func() {
			<-ctx.Done()
			server.Close()
		}()
		go func() {
			if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
				errs <- err
				return
			}
			errs <- nil
		}()
	}
	var first error
	for range sockets {
		if err := <-errs; err != nil && first == nil {
			first = err
		}
	}
	return first
}

// newTeamModelsProxy passes OpenRouter API calls on to upstream with the
// team's key in place of whatever key the app sent. The preview's calls
// stop for the day once they cost the team's limit.
func newTeamModelsProxy(team, upstream, slot string) http.Handler {
	target, _ := url.Parse(upstream)
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.FlushInterval = -1
	director := proxy.Director
	proxy.Director = func(r *http.Request) {
		director(r)
		r.Host = target.Host
		r.Header.Del("Authorization")
		openRouterAskCost(r)
		if key, err := teamOpenRouterKey(team); err == nil {
			r.Header.Set("Authorization", "Bearer "+key)
		}
	}
	// What each call cost goes in the team's spend log, which hi watch
	// compares with what OpenRouter counts on the key (team_watch.go).
	proxy.ModifyResponse = func(response *http.Response) error {
		response.Body = &teamCostReader{body: response.Body, done: func(cost float64) {
			teamRecordSpend(team, slot, cost)
			if slot == teamSlotPreview {
				teamAddPreviewSpend(team, cost)
			}
		}}
		return nil
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/v1/") {
			http.Error(w, "only OpenRouter's API is here: "+fmt.Sprintf("http://127.0.0.1:%d/api/v1/...", teamAppModelsPort), http.StatusNotFound)
			return
		}
		if slot == teamSlotPreview {
			if limit := teamPreviewModelLimit(team); limit > 0 {
				if spent := loadTeamPreviewSpend(team); spent.USD >= limit {
					teamModelsRefuse(w, fmt.Sprintf("hi: the preview's model calls stopped for today: they cost $%.2f, and the team's limit for the preview is $%.2f a day (hi team limit on the machine). Production's calls aren't limited.", spent.USD, limit))
					return
				}
			}
		}
		proxy.ServeHTTP(w, r)
	})
}

// runTeamApp is hi team __app, an app box's first process: it relays
// 127.0.0.1:<port> to the models socket, holds the slot's workers' lock
// for the box (--workers, team_workers.go), then runs the app's command
// and ends as it does.
func runTeamApp(args []string, stderr io.Writer) int {
	workers, box := "", ""
	for len(args) >= 2 && (args[0] == "--workers" || args[0] == "--box") {
		if args[0] == "--workers" {
			workers = args[1]
		} else {
			box = args[1]
		}
		args = args[2:]
	}
	if len(args) != 3 {
		fmt.Fprintln(stderr, "usage: hi team __app [--workers <dir> --box <name>] <models socket> <port> <command>")
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
	if workers != "" && box != "" {
		done := make(chan struct{})
		defer close(done)
		go teamWorkersRelay(workers, box, firstNonEmpty(os.Getenv("HI_WORKERS_FILE"), teamAppWorkersFile), teamWorkersGrace(os.Getenv("HI_WORKERS_GRACE")), done, stderr)
	}
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

// teamKey is hi team key <name> openrouter [--unset], or slack.
func teamKey(args []string, stdin io.Reader, stdout io.Writer) error {
	usage := usageError{"usage: hi team key <name> openrouter [--unset | --from-server]\n       hi team key <name> slack"}
	if len(args) == 2 && args[1] == "slack" {
		return teamKeySlack(args[0], stdin, stdout)
	}
	var words []string
	unset, fromServer := false, false
	for _, arg := range args {
		switch arg {
		case "--unset":
			unset = true
		case "--from-server":
			fromServer = true
		default:
			words = append(words, arg)
		}
	}
	if fromServer && len(words) == 2 && words[1] == "openrouter" {
		return teamKeyFromServer(words[0], stdout)
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

// teamKeySlack is hi team key <name> slack: the Slack app's tokens again,
// after Slack gave new ones. Each is asked for and checked; Enter keeps the
// one the team has. A running team restarts with them.
func teamKeySlack(name string, stdin io.Reader, stdout io.Writer) error {
	config, err := loadTeam(name)
	if err != nil {
		return err
	}
	secrets, err := loadTeamSecrets(name)
	if err != nil {
		return fmt.Errorf("the team's secrets: %w", err)
	}
	fmt.Fprintf(stdout, "Team %s's Slack app tokens, from its settings at api.slack.com/apps. Enter keeps the one the team has.\n", name)
	bot, err := teamAskKeep(stdin, stdout, "Bot User OAuth Token (OAuth & Permissions, xoxb-…)", func(token string) error {
		if token == "" {
			return nil
		}
		if _, err := teamCheckBotToken(token); err != nil {
			return err
		}
		_, err := teamCheckChannel(teamBotHandle(config), token, config.Channel)
		return err
	})
	if err != nil {
		return err
	}
	app, err := teamAskKeep(stdin, stdout, "App-Level Token (Basic Information, xapp-…)", func(token string) error {
		if token == "" {
			return nil
		}
		return teamCheckAppToken(token)
	})
	if err != nil {
		return err
	}
	if bot == "" && app == "" {
		fmt.Fprintln(stdout, "Nothing changed.")
		return nil
	}
	if bot != "" {
		secrets.SlackBotToken = bot
	}
	if app != "" {
		secrets.SlackAppToken = app
	}
	if err := saveTeamSecrets(name, secrets); err != nil {
		return err
	}
	if teamState(name) != "up" {
		fmt.Fprintf(stdout, "Saved. Team %s uses them from its next start: hi team up %s.\n", name, name)
		return nil
	}
	fmt.Fprintf(stdout, "Saved. Team %s restarts with them.\n", name)
	return teamUp(name, stdout)
}

// teamAskKeep asks for a secret that Enter leaves as it is (""), and asks
// again, at a terminal, while check refuses it.
func teamAskKeep(stdin io.Reader, stdout io.Writer, label string, check func(string) error) (string, error) {
	if teamStyled(stdin, stdout) {
		return teamFieldAsk(stdin, stdout, label, true, check)
	}
	for attempt := 1; ; attempt++ {
		value, err := teamReadSecret(stdin, stdout, label)
		if err == nil {
			if err = check(value); err == nil {
				return value, nil
			}
		}
		if !isTerminal(stdin) || attempt == 5 || errors.Is(err, io.EOF) {
			return "", err
		}
		fmt.Fprintf(stdout, "  %v. Try again.\n", err)
	}
}

// teamEnv is hi team env <name> [--production | --preview] [KEY=VALUE |
// KEY | --file KEY=<path> | --unset KEY]...: settings for both slots, or
// with --production for production alone, or with --preview a value of
// the preview's own.
func teamEnv(args []string, stdin io.Reader, stdout io.Writer) error {
	usage := usageError{"usage: hi team env <name> [--production | --preview | --dev] [KEY=VALUE | KEY | --file KEY=<path> | --unset KEY]..."}
	if len(args) == 0 {
		return usage
	}
	name := args[0]
	if _, err := loadTeam(name); err != nil {
		return err
	}
	secrets, err := loadTeamSecrets(name)
	if err != nil {
		return fmt.Errorf("the team's secrets: %w", err)
	}
	slot := ""
	var rest []string
	for _, arg := range args[1:] {
		switch arg {
		case "--production":
			slot = teamSlotProduction
		case "--preview":
			slot = teamSlotPreview
		case "--dev":
			slot = teamSlotDev
		default:
			rest = append(rest, arg)
		}
	}
	if len(rest) == 0 {
		if slot != "" {
			return usage
		}
		names := teamEnvNames(secrets)
		if len(names) == 0 {
			fmt.Fprintf(stdout, "Team %s has no settings. Add one: hi team env %s KEY=VALUE\n", name, name)
			return nil
		}
		fmt.Fprintf(stdout, "Team %s's app gets: %s\n", name, strings.Join(names, ", "))
		return nil
	}
	var set, unset, files []string
	for i := 0; i < len(rest); i++ {
		arg := rest[i]
		if arg == "--unset" {
			if i+1 >= len(rest) {
				return usageError{"usage: hi team env <name> [--production | --preview] --unset KEY"}
			}
			i++
			if !teamEnvUnset(&secrets, slot, rest[i]) && !teamFileUnset(&secrets, slot, rest[i]) {
				return fmt.Errorf("team %s has no setting %s%s", name, rest[i], teamEnvSlotWords(slot))
			}
			unset = append(unset, rest[i])
			continue
		}
		if arg == "--file" {
			if i+1 >= len(rest) {
				return usageError{"usage: hi team env <name> [--production | --preview] --file KEY=<path>"}
			}
			i++
			key, file, _ := strings.Cut(rest[i], "=")
			if err := teamSettingCheck(secrets, key, true); err != nil {
				return err
			}
			if file == "" {
				return usageError{"usage: hi team env <name> [--production | --preview] --file KEY=<path>"}
			}
			data, err := teamFileSettingRead(file)
			if err != nil {
				return err
			}
			teamFileSet(&secrets, slot, key, data)
			set, files = append(set, key), append(files, key)
			continue
		}
		key, value, given := strings.Cut(arg, "=")
		if strings.HasPrefix(key, "-") {
			return usageError{"unknown option " + key}
		}
		if err := teamSettingCheck(secrets, key, false); err != nil {
			return err
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
		teamEnvSet(&secrets, slot, key, value)
		set = append(set, key)
	}
	if err := saveTeamSecrets(name, secrets); err != nil {
		return err
	}
	switch {
	case len(set) > 0 && slot == teamSlotDev:
		fmt.Fprintf(stdout, "Team %s's dev box gets its own %s; the app's boxes don't.\n", name, strings.Join(set, ", "))
	case len(set) > 0 && slot == teamSlotProduction:
		fmt.Fprintf(stdout, "Team %s's production gets %s; the preview doesn't, unless it has a value of its own (--preview).\n", name, strings.Join(set, ", "))
	case len(set) > 0 && slot == teamSlotPreview:
		fmt.Fprintf(stdout, "Team %s's preview gets its own %s; production keeps what it has.\n", name, strings.Join(set, ", "))
	case len(set) > 0:
		fmt.Fprintf(stdout, "Team %s's app gets %s, in the preview and production.\n", name, strings.Join(set, ", "))
	}
	if len(unset) > 0 {
		fmt.Fprintf(stdout, "Team %s's %s no longer gets %s.\n", name, map[string]string{"": "app or dev box", teamSlotProduction: "production", teamSlotPreview: "preview", teamSlotDev: "dev box"}[slot], strings.Join(unset, ", "))
	}
	where := "the app's boxes"
	if slot == teamSlotDev {
		where = "the dev box"
	}
	for _, key := range files {
		fmt.Fprintf(stdout, "In %s, $%s is %s/%s, read-only. hi keeps it; the file you gave isn't needed on this machine any more.\n", where, key, teamAppFilesDir, key)
	}
	if slot == teamSlotDev {
		fmt.Fprintf(stdout, "The dev box has it from its next start: hi team dev %s restart, when the agents are idle.\n", name)
		return nil
	}
	fmt.Fprintln(stdout, "The next preview has it, and hi restarts production on the same link within a minute while the team is up.")
	return nil
}

func teamEnvSlotWords(slot string) string {
	switch slot {
	case teamSlotProduction:
		return " for production"
	case teamSlotPreview:
		return " for the preview"
	case teamSlotDev:
		return " for the dev box"
	}
	return ""
}

// teamSettingMaps are one kind of setting: both slots', production's
// own, the preview's own, and the dev box's.
type teamSettingMaps struct{ both, production, preview, dev *map[string]string }

func teamEnvMaps(secrets *teamSecrets) teamSettingMaps {
	return teamSettingMaps{&secrets.Env, &secrets.EnvProduction, &secrets.EnvPreview, &secrets.EnvDev}
}

func teamFileMaps(secrets *teamSecrets) teamSettingMaps {
	return teamSettingMaps{&secrets.Files, &secrets.FilesProduction, &secrets.FilesPreview, &secrets.FilesDev}
}

func (m teamSettingMaps) has(key string) bool {
	for _, set := range []*map[string]string{m.both, m.production, m.preview, m.dev} {
		if _, ok := (*set)[key]; ok {
			return true
		}
	}
	return false
}

// set sets a setting for both slots (slot ""), or for one. A setting
// both had becomes the other slot's alone, so --preview gives the preview
// a value of its own and production keeps its value, and --production
// takes it away from the preview.
func (m teamSettingMaps) set(slot, key, value string) {
	for _, set := range []*map[string]string{m.both, m.production, m.preview, m.dev} {
		if *set == nil {
			*set = map[string]string{}
		}
	}
	both, production, preview := *m.both, *m.production, *m.preview
	switch slot {
	case teamSlotDev:
		(*m.dev)[key] = value
	case "":
		both[key] = value
		delete(production, key)
		delete(preview, key)
	case teamSlotProduction:
		production[key] = value
		delete(both, key)
	case teamSlotPreview:
		if kept, ok := both[key]; ok {
			production[key] = kept
			delete(both, key)
		}
		preview[key] = value
	}
}

// unset removes a setting from everywhere (slot ""), or from one slot,
// and says whether there was one. A setting both had stays the other
// slot's.
func (m teamSettingMaps) unset(slot, key string) bool {
	both, production, preview, dev := *m.both, *m.production, *m.preview, *m.dev
	_, inBoth := both[key]
	_, inProduction := production[key]
	_, inPreview := preview[key]
	_, inDev := dev[key]
	switch slot {
	case "":
		delete(both, key)
		delete(production, key)
		delete(preview, key)
		delete(dev, key)
		return inBoth || inProduction || inPreview || inDev
	case teamSlotDev:
		delete(dev, key)
		return inDev
	case teamSlotProduction:
		if inBoth {
			m.set(teamSlotPreview, key, both[key])
		}
		delete(*m.production, key)
		return inBoth || inProduction
	default:
		if inBoth {
			m.set(teamSlotProduction, key, both[key])
		}
		delete(*m.preview, key)
		return inBoth || inPreview
	}
}

func teamEnvSet(secrets *teamSecrets, slot, key, value string) {
	teamEnvMaps(secrets).set(slot, key, value)
}

func teamEnvUnset(secrets *teamSecrets, slot, key string) bool {
	return teamEnvMaps(secrets).unset(slot, key)
}

func teamFileSet(secrets *teamSecrets, slot, key string, data []byte) {
	teamFileMaps(secrets).set(slot, key, base64.StdEncoding.EncodeToString(data))
}

func teamFileUnset(secrets *teamSecrets, slot, key string) bool {
	return teamFileMaps(secrets).unset(slot, key)
}

// teamSettingCheck says what's wrong with a setting's name, for a value
// or for a file: a name is one or the other.
func teamSettingCheck(secrets teamSecrets, key string, file bool) error {
	switch {
	case !teamEnvName.MatchString(key):
		return fmt.Errorf("%q isn't a variable name: letters, digits, and _, not starting with a digit", key)
	case teamAppEnvReserved[key]:
		return fmt.Errorf("hi sets %s in the app's box itself", key)
	case file && teamEnvMaps(&secrets).has(key):
		return fmt.Errorf("%s is a value; remove it first (--unset %s) to make it a file", key, key)
	case !file && teamFileMaps(&secrets).has(key):
		return fmt.Errorf("%s is a file; remove it first (--unset %s) to make it a value", key, key)
	}
	return nil
}

// teamFileSettingRead reads a file for a file setting; ~/ is the home
// folder, which a shell leaves as it is after KEY=.
func teamFileSettingRead(file string) ([]byte, error) {
	if rest, ok := strings.CutPrefix(file, "~/"); ok {
		if home, err := os.UserHomeDir(); err == nil {
			file = filepath.Join(home, rest)
		}
	}
	info, err := os.Stat(file)
	switch {
	case err != nil:
		return nil, err
	case !info.Mode().IsRegular():
		return nil, fmt.Errorf("%s isn't a file", file)
	case info.Size() == 0:
		return nil, fmt.Errorf("%s is empty", file)
	case info.Size() > teamFileSettingMax:
		return nil, fmt.Errorf("%s is larger than %s; a file setting is for a certificate or a key", file, formatDataSize(teamFileSettingMax))
	}
	return os.ReadFile(file)
}

// teamEnvNames are the settings' names, each with the slots that get it
// when that isn't both.
func teamEnvNames(secrets teamSecrets) []string {
	names := []string{}
	for _, kind := range []struct {
		maps teamSettingMaps
		file bool
	}{{teamEnvMaps(&secrets), false}, {teamFileMaps(&secrets), true}} {
		seen := map[string]bool{}
		for _, set := range []*map[string]string{kind.maps.both, kind.maps.production, kind.maps.preview, kind.maps.dev} {
			for key := range *set {
				seen[key] = true
			}
		}
		for key := range seen {
			_, both := (*kind.maps.both)[key]
			_, production := (*kind.maps.production)[key]
			_, preview := (*kind.maps.preview)[key]
			_, dev := (*kind.maps.dev)[key]
			var about []string
			if kind.file {
				about = append(about, "a file")
			}
			switch {
			case production && preview:
				about = append(about, "a value of its own in each")
			case production:
				about = append(about, "production only")
			case preview:
				about = append(about, "the preview only")
			}
			switch {
			case dev && !both && !production && !preview:
				about = append(about, "the dev box only")
			case dev:
				about = append(about, "the dev box has its own")
			}
			if len(about) > 0 {
				key += " (" + strings.Join(about, ", ") + ")"
			}
			names = append(names, key)
		}
	}
	sort.Strings(names)
	return names
}

// teamStampVersion is the hi in teamSettingsStamp; tests change it.
var teamStampVersion = version
