package cli

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
	appModelsSocket = "models.sock"
	// appBoxModelsDir is where an app's box sees the models socket, and
	// appBoxModelsPort where the relay in the box listens.
	appBoxModelsDir = "/hi-models"
	// appBoxRelay is hi in an app's box, off its PATH: it starts the app
	// and relays its model calls, and nothing else of hi works there, so
	// an app that looks for hi doesn't find it (and try hi agent).
	appBoxRelay      = "/usr/local/libexec/hi-relay"
	appBoxModelsPort = 3129
)

var appOpenRouterUpstream = "https://openrouter.ai"

var appEnvName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// A file setting is in its app's boxes, read-only, in appBoxFilesDir,
// named as its variable, which holds its path there.
const (
	appBoxFilesDir    = "/hi-files"
	appFileSettingMax = 32 << 10
)

// appSlotDev is the dev box, for its own settings (hi app env --dev).
const appSlotDev = "dev"

// appSlotFilesDir is a slot's file settings on the machine, written as
// its box starts.
func appSlotFilesDir(team, slot string) string {
	return filepath.Join(appDir(team), "run", slot+"-files")
}

// appBoxEnvReserved are the variables hi sets in an app's box itself.
var appBoxEnvReserved = map[string]bool{"HOME": true, "PORT": true, "HOST": true, "PATH": true,
	"HI_PUBLIC_URL_FILE": true, "HI_DATA_DIR": true, "HI_SLOT": true, "HI_WORKERS_FILE": true, "OPENROUTER_BASE_URL": true, "OPENROUTER_API_KEY": true}

// appModelsDir holds the broker's models socket; app boxes mount it.
func appModelsDir(name string) string { return appBrokerDir(name) + "-models" }

// appKeysDir holds the team's OpenRouter key as a .env file for its
// proxy, which reads it again for each request.
func appKeysDir(name string) string { return filepath.Join(appDir(name), "run", "keys") }

// writeTeamKeys puts the team's OpenRouter key where its proxy reads it,
// or removes it, so the proxy takes this machine's again; and the same
// for its Claude token.
func writeTeamKeys(name string, secrets appSecrets) error {
	dir := appKeysDir(name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	// The team's Claude token, which members' proxies read before this
	// machine's sign-in (app_subscriptions.go).
	claude := filepath.Join(dir, appClaudeKeyFile)
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

// appOpenRouterKey is the team's own key, or else this machine's, from
// Hermes' .env.
func appOpenRouterKey(name string) (string, error) {
	if secrets, err := loadTeamSecrets(name); err == nil && secrets.OpenRouterAPIKey != "" {
		return secrets.OpenRouterAPIKey, nil
	}
	return readDotEnvValue(hermesEnvPath(), "OPENROUTER_API_KEY")
}

// appCheckOpenRouterKey asks OpenRouter whether it knows the key; tests
// replace it.
var appCheckOpenRouterKey = func(key string) error {
	request, _ := http.NewRequest("GET", appOpenRouterUpstream+"/api/v1/key", nil)
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

// appAskOpenRouterKey asks for an OpenRouter key; Enter gives "", for
// this machine's.
func appAskOpenRouterKey(stdin io.Reader, stdout io.Writer, question string) (string, error) {
	if appStyled(stdin, stdout) {
		return appFieldAsk(stdin, stdout, question, true, func(key string) error {
			if key == "" {
				return nil
			}
			return appCheckOpenRouterKey(key)
		})
	}
	for attempt := 1; ; attempt++ {
		key, err := appReadSecret(stdin, stdout, question)
		if err == nil && key != "" {
			err = appCheckOpenRouterKey(key)
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

// appSettingsStamp changes when what an app's box gets changes, so its
// box starts again: the settings, how hi gives them, and hi itself, which
// the box runs from a copy made when it started (hi team __app, the
// models' relay), so an update reaches the app boxes too.
func appSettingsStamp(name string) string {
	secrets, _ := loadTeamSecrets(name)
	hash := sha256.New()
	fmt.Fprintf(hash, "hi=%s\n", appStampVersion)
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

// appBoxEnv is what an app's box gets: its port, its address file, its
// slot ($HI_SLOT, preview or production, so work that costs money runs in
// production only), the models at an ordinary address with a placeholder
// key, and the team's settings: those for both slots, and the slot's own.
func appBoxEnv(name, slot string, port int) map[string]string {
	env := map[string]string{}
	if secrets, err := loadTeamSecrets(name); err == nil {
		own := secrets.EnvPreview
		if slot == appSlotProduction {
			own = secrets.EnvProduction
		}
		for _, set := range []map[string]string{secrets.Env, own} {
			for key, value := range set {
				env[key] = value
			}
		}
		for key := range appBoxFiles(secrets, slot) {
			env[key] = appBoxFilesDir + "/" + key
		}
	}
	env["HOME"], env["PORT"], env["HOST"] = "/tmp", strconv.Itoa(port), "0.0.0.0"
	env["HI_PUBLIC_URL_FILE"] = appBoxAddressFile
	env["HI_DATA_DIR"] = appBoxDataDir
	env["HI_SLOT"] = slot
	// The app's long work reports here, as files or with "$HI" app job
	// (app_jobs.go).
	env["HI_JOBS_DIR"] = appBoxJobsDir
	// The app's background workers run while this file exists
	// (app_workers.go).
	env["HI_WORKERS_FILE"] = appBoxWorkersFile
	env["HI"] = appBoxRelay
	env["OPENROUTER_BASE_URL"] = fmt.Sprintf("http://127.0.0.1:%d/api/v1", appBoxModelsPort)
	env["OPENROUTER_API_KEY"] = boxClaudePlacehold
	return env
}

// appBoxFiles are a slot's file settings, by name: those for both slots,
// and the slot's own.
func appBoxFiles(secrets appSecrets, slot string) map[string]string {
	if slot == appSlotDev {
		return maps.Clone(secrets.FilesDev)
	}
	own := secrets.FilesPreview
	if slot == appSlotProduction {
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
	dir := appSlotFilesDir(team, slot)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	secrets, err := loadTeamSecrets(team)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	files := appBoxFiles(secrets, slot)
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
	file, err := os.CreateTemp(filepath.Join(appDir(name), "run"), ".app-env-")
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

// appSlotModelsDir is the folder of a slot's models socket, which the
// slot's box mounts: each slot has its own, so the preview's calls are
// counted against its daily limit and production's aren't.
func appSlotModelsDir(name, slot string) string { return filepath.Join(appModelsDir(name), slot) }

// serveModels answers app boxes' model calls on each slot's socket,
// passing them on to OpenRouter with the team's key. The socket in the
// models folder itself is for boxes started by a hi from before the
// slots had their own, as production, until they start again.
func (b *appBroker) serveModels(ctx context.Context) error {
	sockets := map[string]string{
		filepath.Join(appModelsDir(b.team), appModelsSocket):                        appSlotProduction,
		filepath.Join(appSlotModelsDir(b.team, appSlotProduction), appModelsSocket): appSlotProduction,
		filepath.Join(appSlotModelsDir(b.team, appSlotPreview), appModelsSocket):    appSlotPreview,
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
		server := &http.Server{Handler: newTeamModelsProxy(b.team, appOpenRouterUpstream, slot)}
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
		if key, err := appOpenRouterKey(team); err == nil {
			r.Header.Set("Authorization", "Bearer "+key)
		}
	}
	// What each call cost goes in the team's spend log, which hi watch
	// compares with what OpenRouter counts on the key (app_watch.go).
	proxy.ModifyResponse = func(response *http.Response) error {
		response.Body = &appCostReader{body: response.Body, done: func(cost float64) {
			appRecordSpend(team, slot, cost)
			if slot == appSlotPreview {
				appAddPreviewSpend(team, cost)
			}
		}}
		return nil
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/v1/") {
			http.Error(w, "only OpenRouter's API is here: "+fmt.Sprintf("http://127.0.0.1:%d/api/v1/...", appBoxModelsPort), http.StatusNotFound)
			return
		}
		if slot == appSlotPreview {
			if limit := appPreviewModelLimit(team); limit > 0 {
				if spent := loadTeamPreviewSpend(team); spent.USD >= limit {
					appModelsRefuse(w, fmt.Sprintf("hi: the preview's model calls stopped for today: they cost $%.2f, and the app's limit for the preview is $%.2f a day (hi app limit on the machine). Production's calls aren't limited.", spent.USD, limit))
					return
				}
			}
		}
		proxy.ServeHTTP(w, r)
	})
}

// runTeamApp is hi team __app, an app box's first process: it relays
// 127.0.0.1:<port> to the models socket, holds the slot's workers' lock
// for the box (--workers, app_workers.go), then runs the app's command
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
		fmt.Fprintln(stderr, "usage: hi app __app [--workers <dir> --box <name>] <models socket> <port> <command>")
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
				go appRelay(conn, socket)
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
		go appWorkersRelay(workers, box, firstNonEmpty(os.Getenv("HI_WORKERS_FILE"), appBoxWorkersFile), appWorkersGrace(os.Getenv("HI_WORKERS_GRACE")), done, stderr)
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

func appRelay(conn net.Conn, socket string) {
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

// appKey is hi app key <name> openrouter [--unset], or slack.
func appKey(args []string, stdin io.Reader, stdout io.Writer) error {
	usage := usageError{"usage: hi app key <name> openrouter [--unset]\n       hi app key <name> slack"}
	if len(args) == 2 && args[1] == "slack" {
		return appKeySlack(args[0], stdin, stdout)
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
	if fromServer {
		// The org server made teams' keys until hi 2.6.34 (hi_app_ops.md).
		return errors.New("the server no longer makes apps' keys; give the app its own: hi app key <name> openrouter")
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
		return fmt.Errorf("the app's secrets: %w", err)
	}
	if unset {
		secrets.OpenRouterAPIKey = ""
	} else {
		fmt.Fprintln(stdout, "The app's OpenRouter key (openrouter.ai/settings/keys). No box ever holds it.")
		key, err := appAskOpenRouterKey(stdin, stdout, "OpenRouter key")
		if err != nil {
			return err
		}
		if key == "" {
			return errors.New("no key was entered; hi app key " + name + " openrouter --unset uses this machine's")
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
		fmt.Fprintf(stdout, "App %s uses this machine's OpenRouter key again (Hermes').\n", name)
	} else {
		fmt.Fprintf(stdout, "App %s's model calls use its own OpenRouter key now: the lead's, and the app's at $OPENROUTER_BASE_URL.\n", name)
	}
	return nil
}

// appKeySlack is hi app key <name> slack: the Slack app's tokens again,
// after Slack gave new ones. Each is asked for and checked; Enter keeps the
// one the team has. A running team restarts with them.
func appKeySlack(name string, stdin io.Reader, stdout io.Writer) error {
	config, err := loadTeam(name)
	if err != nil {
		return err
	}
	secrets, err := loadTeamSecrets(name)
	if err != nil {
		return fmt.Errorf("the app's secrets: %w", err)
	}
	fmt.Fprintf(stdout, "App %s's Slack app tokens, from its settings at api.slack.com/apps. Enter keeps the one the app has.\n", name)
	app, err := appAskKeep(stdin, stdout, "App-Level Token (Basic Information, xapp-…)", func(token string) error {
		if token == "" {
			return nil
		}
		return appCheckAppToken(token)
	})
	if err != nil {
		return err
	}
	bot, err := appAskKeep(stdin, stdout, "Bot User OAuth Token (OAuth & Permissions, xoxb-…)", func(token string) error {
		if token == "" {
			return nil
		}
		if _, err := appCheckBotToken(token); err != nil {
			return err
		}
		_, err := appCheckChannel(appBotHandle(config), token, config.Channel)
		return err
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
	if appState(name) != "up" {
		fmt.Fprintf(stdout, "Saved. App %s uses them from its next start: hi app up %s.\n", name, name)
		return nil
	}
	fmt.Fprintf(stdout, "Saved. App %s restarts with them.\n", name)
	return appUp(name, stdout)
}

// appAskKeep asks for a secret that Enter leaves as it is (""), and asks
// again, at a terminal, while check refuses it.
func appAskKeep(stdin io.Reader, stdout io.Writer, label string, check func(string) error) (string, error) {
	if appStyled(stdin, stdout) {
		return appFieldAsk(stdin, stdout, label, true, check)
	}
	for attempt := 1; ; attempt++ {
		value, err := appReadSecret(stdin, stdout, label)
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

// appEnv is hi app env <name> [--production | --preview] [KEY=VALUE |
// KEY | --file KEY=<path> | --unset KEY]...: settings for both slots, or
// with --production for production alone, or with --preview a value of
// the preview's own.
func appEnv(args []string, stdin io.Reader, stdout io.Writer) error {
	usage := usageError{"usage: hi app env <name> [--production | --preview | --dev] [KEY=VALUE | KEY | --file KEY=<path> | --unset KEY]..."}
	if len(args) == 0 {
		return usage
	}
	name := args[0]
	if _, err := loadTeam(name); err != nil {
		return err
	}
	secrets, err := loadTeamSecrets(name)
	if err != nil {
		return fmt.Errorf("the app's secrets: %w", err)
	}
	slot := ""
	var rest []string
	for _, arg := range args[1:] {
		switch arg {
		case "--production":
			slot = appSlotProduction
		case "--preview":
			slot = appSlotPreview
		case "--dev":
			slot = appSlotDev
		default:
			rest = append(rest, arg)
		}
	}
	if len(rest) == 0 {
		if slot != "" {
			return usage
		}
		names := appEnvNames(secrets)
		if len(names) == 0 {
			fmt.Fprintf(stdout, "App %s has no settings. Add one: hi app env %s KEY=VALUE\n", name, name)
			return nil
		}
		fmt.Fprintf(stdout, "App %s gets: %s\n", name, strings.Join(names, ", "))
		return nil
	}
	var set, unset, files []string
	for i := 0; i < len(rest); i++ {
		arg := rest[i]
		if arg == "--unset" {
			if i+1 >= len(rest) {
				return usageError{"usage: hi app env <name> [--production | --preview] --unset KEY"}
			}
			i++
			if !appEnvUnset(&secrets, slot, rest[i]) && !appFileUnset(&secrets, slot, rest[i]) {
				return fmt.Errorf("app %s has no setting %s%s", name, rest[i], appEnvSlotWords(slot))
			}
			unset = append(unset, rest[i])
			continue
		}
		if arg == "--file" {
			if i+1 >= len(rest) {
				return usageError{"usage: hi app env <name> [--production | --preview] --file KEY=<path>"}
			}
			i++
			key, file, _ := strings.Cut(rest[i], "=")
			if err := appSettingCheck(secrets, key, true); err != nil {
				return err
			}
			if file == "" {
				return usageError{"usage: hi app env <name> [--production | --preview] --file KEY=<path>"}
			}
			data, err := appFileSettingRead(file)
			if err != nil {
				return err
			}
			appFileSet(&secrets, slot, key, data)
			set, files = append(set, key), append(files, key)
			continue
		}
		key, value, given := strings.Cut(arg, "=")
		if strings.HasPrefix(key, "-") {
			return usageError{"unknown option " + key}
		}
		if err := appSettingCheck(secrets, key, false); err != nil {
			return err
		}
		if !given {
			if value, err = appReadSecret(stdin, stdout, key); err != nil {
				return err
			}
			if value == "" {
				return fmt.Errorf("no value for %s was entered", key)
			}
		}
		if strings.ContainsAny(value, "\r\n") {
			return fmt.Errorf("%s's value has a line break; a setting is one line", key)
		}
		appEnvSet(&secrets, slot, key, value)
		set = append(set, key)
	}
	if err := saveTeamSecrets(name, secrets); err != nil {
		return err
	}
	switch {
	case len(set) > 0 && slot == appSlotDev:
		fmt.Fprintf(stdout, "App %s's dev box gets its own %s; the app's boxes don't.\n", name, strings.Join(set, ", "))
	case len(set) > 0 && slot == appSlotProduction:
		fmt.Fprintf(stdout, "App %s's production gets %s; the preview doesn't, unless it has a value of its own (--preview).\n", name, strings.Join(set, ", "))
	case len(set) > 0 && slot == appSlotPreview:
		fmt.Fprintf(stdout, "App %s's preview gets its own %s; production keeps what it has.\n", name, strings.Join(set, ", "))
	case len(set) > 0:
		fmt.Fprintf(stdout, "App %s gets %s, in the preview and production.\n", name, strings.Join(set, ", "))
	}
	if len(unset) > 0 {
		fmt.Fprintf(stdout, "App %s's %s no longer gets %s.\n", name, map[string]string{"": "app or dev box", appSlotProduction: "production", appSlotPreview: "preview", appSlotDev: "dev box"}[slot], strings.Join(unset, ", "))
	}
	where := "the app's boxes"
	if slot == appSlotDev {
		where = "the dev box"
	}
	for _, key := range files {
		fmt.Fprintf(stdout, "In %s, $%s is %s/%s, read-only. hi keeps it; the file you gave isn't needed on this machine any more.\n", where, key, appBoxFilesDir, key)
	}
	if slot == appSlotDev {
		fmt.Fprintf(stdout, "The dev box has it from its next start: hi app dev %s restart, when the agents are idle.\n", name)
		return nil
	}
	fmt.Fprintln(stdout, "The next preview has it, and hi restarts production on the same link within a minute while the app is up.")
	return nil
}

func appEnvSlotWords(slot string) string {
	switch slot {
	case appSlotProduction:
		return " for production"
	case appSlotPreview:
		return " for the preview"
	case appSlotDev:
		return " for the dev box"
	}
	return ""
}

// appSettingMaps are one kind of setting: both slots', production's
// own, the preview's own, and the dev box's.
type appSettingMaps struct{ both, production, preview, dev *map[string]string }

func appEnvMaps(secrets *appSecrets) appSettingMaps {
	return appSettingMaps{&secrets.Env, &secrets.EnvProduction, &secrets.EnvPreview, &secrets.EnvDev}
}

func appFileMaps(secrets *appSecrets) appSettingMaps {
	return appSettingMaps{&secrets.Files, &secrets.FilesProduction, &secrets.FilesPreview, &secrets.FilesDev}
}

func (m appSettingMaps) has(key string) bool {
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
func (m appSettingMaps) set(slot, key, value string) {
	for _, set := range []*map[string]string{m.both, m.production, m.preview, m.dev} {
		if *set == nil {
			*set = map[string]string{}
		}
	}
	both, production, preview := *m.both, *m.production, *m.preview
	switch slot {
	case appSlotDev:
		(*m.dev)[key] = value
	case "":
		both[key] = value
		delete(production, key)
		delete(preview, key)
	case appSlotProduction:
		production[key] = value
		delete(both, key)
	case appSlotPreview:
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
func (m appSettingMaps) unset(slot, key string) bool {
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
	case appSlotDev:
		delete(dev, key)
		return inDev
	case appSlotProduction:
		if inBoth {
			m.set(appSlotPreview, key, both[key])
		}
		delete(*m.production, key)
		return inBoth || inProduction
	default:
		if inBoth {
			m.set(appSlotProduction, key, both[key])
		}
		delete(*m.preview, key)
		return inBoth || inPreview
	}
}

func appEnvSet(secrets *appSecrets, slot, key, value string) {
	appEnvMaps(secrets).set(slot, key, value)
}

func appEnvUnset(secrets *appSecrets, slot, key string) bool {
	return appEnvMaps(secrets).unset(slot, key)
}

func appFileSet(secrets *appSecrets, slot, key string, data []byte) {
	appFileMaps(secrets).set(slot, key, base64.StdEncoding.EncodeToString(data))
}

func appFileUnset(secrets *appSecrets, slot, key string) bool {
	return appFileMaps(secrets).unset(slot, key)
}

// appSettingCheck says what's wrong with a setting's name, for a value
// or for a file: a name is one or the other.
func appSettingCheck(secrets appSecrets, key string, file bool) error {
	switch {
	case !appEnvName.MatchString(key):
		return fmt.Errorf("%q isn't a variable name: letters, digits, and _, not starting with a digit", key)
	case appBoxEnvReserved[key]:
		return fmt.Errorf("hi sets %s in the app's box itself", key)
	case file && appEnvMaps(&secrets).has(key):
		return fmt.Errorf("%s is a value; remove it first (--unset %s) to make it a file", key, key)
	case !file && appFileMaps(&secrets).has(key):
		return fmt.Errorf("%s is a file; remove it first (--unset %s) to make it a value", key, key)
	}
	return nil
}

// appFileSettingRead reads a file for a file setting; ~/ is the home
// folder, which a shell leaves as it is after KEY=.
func appFileSettingRead(file string) ([]byte, error) {
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
	case info.Size() > appFileSettingMax:
		return nil, fmt.Errorf("%s is larger than %s; a file setting is for a certificate or a key", file, formatDataSize(appFileSettingMax))
	}
	return os.ReadFile(file)
}

// appEnvNames are the settings' names, each with the slots that get it
// when that isn't both.
func appEnvNames(secrets appSecrets) []string {
	names := []string{}
	for _, kind := range []struct {
		maps appSettingMaps
		file bool
	}{{appEnvMaps(&secrets), false}, {appFileMaps(&secrets), true}} {
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

// appStampVersion is the hi in appSettingsStamp; tests change it.
var appStampVersion = version
