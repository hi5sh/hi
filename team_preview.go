package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// A preview runs the team's project for the people to try: the broker
// starts it in an app box from main (or a task's branch), publishes its
// port to this machine only, and puts that on a temporary address with hi
// net expose --link, which uses this machine's NetBird: the link carries
// its key, so the people open it without a password.
// The lead gets the address; it never joins the network itself.

const (
	teamPreviewPort       = 8080 // the app's port in its box, as $PORT
	teamPreviewDefaultMax = 4 * time.Hour
	teamPreviewReady      = 3 * time.Minute // installing dependencies takes a while
)

// teamPreview is run/preview.json.
type teamPreview struct {
	Source   string    `json:"source"` // the branch, or a task's ID
	Commit   string    `json:"commit"`
	Command  string    `json:"command"`
	HostPort int       `json:"host_port"`
	URL      string    `json:"url"`
	Password string    `json:"password,omitempty"`
	Expose   string    `json:"expose"` // hi net expose's name, to stop it
	Started  time.Time `json:"started"`
	Expires  time.Time `json:"expires"`
}

func teamPreviewPath(team string) string { return filepath.Join(teamDir(team), "run", "preview.json") }

func teamPreviewContainer(team string) string { return "hi-box-team-" + team + "-app" }

// teamRunCommand is how the project starts: make run, else npm start.
func teamRunCommand(dir string) (string, error) {
	if data, err := os.ReadFile(filepath.Join(dir, "Makefile")); err == nil && regexp.MustCompile(`(?m)^run\s*:`).Match(data) {
		return "make run", nil
	}
	if data, err := os.ReadFile(filepath.Join(dir, "package.json")); err == nil {
		var pkg struct {
			Scripts map[string]string `json:"scripts"`
		}
		if json.Unmarshal(data, &pkg) == nil && pkg.Scripts["start"] != "" {
			install := "npm install --no-audit --no-fund"
			if fileExists(filepath.Join(dir, "package-lock.json")) {
				install = "npm ci --no-audit --no-fund"
			}
			return install + " && npm start", nil
		}
	}
	return "", errors.New("the project has no way to start: ask the coder to add a Makefile with a run target that starts the app on $PORT, listening on 0.0.0.0")
}

type teamPreviewRequest struct {
	Task string        `json:"task,omitempty"`
	Port int           `json:"port,omitempty"`
	Max  time.Duration `json:"max,omitempty"`
	Stop bool          `json:"stop,omitempty"`
}

// preview starts (or replaces) the team's preview, or stops it.
func (b *teamBroker) preview(request teamPreviewRequest) (teamPreview, error) {
	b.previewMu.Lock()
	defer b.previewMu.Unlock()
	b.stopPreview()
	if request.Stop {
		return teamPreview{}, nil
	}
	config, err := loadTeam(b.team)
	if err != nil {
		return teamPreview{}, err
	}
	source, ref := config.Branch, config.Branch
	if request.Task != "" {
		task, err := loadTeamTask(b.team, request.Task)
		if err != nil {
			return teamPreview{}, err
		}
		if task.Branch == "" || task.Status == "merged" {
			return teamPreview{}, fmt.Errorf("%s has no branch of its own (%s); preview %s instead", task.ID, task.Status, config.Branch)
		}
		source, ref = task.ID, task.Branch
	}
	port := request.Port
	if port == 0 {
		port = teamPreviewPort
	}
	if port < 1 || port > 65535 {
		return teamPreview{}, fmt.Errorf("%d isn't a port", port)
	}
	max := request.Max
	if max == 0 {
		max = teamPreviewDefaultMax
	}
	if max > netExposeMaxMax {
		return teamPreview{}, fmt.Errorf("a preview lasts at most %s", netExposeMaxMax)
	}

	// The app gets a copy of the commit of its own; its installs and
	// files stay out of repo/.
	src := filepath.Join(teamDir(b.team), "run", "app")
	os.RemoveAll(src)
	if err := os.MkdirAll(src, 0o700); err != nil {
		return teamPreview{}, err
	}
	commit := boxGit(b.repo, "rev-parse", ref)
	if commit == "" {
		return teamPreview{}, fmt.Errorf("%s isn't in the repository", ref)
	}
	if out, err := teamShell(b.repo, fmt.Sprintf("git archive %s | tar -x -C %s", shellQuote(commit), shellQuote(src))); err != nil {
		return teamPreview{}, fmt.Errorf("copying %s: %s", ref, qFirstLine(out, err.Error()))
	}
	command, err := teamRunCommand(src)
	if err != nil {
		return teamPreview{}, err
	}
	hostPort, err := teamFreePort()
	if err != nil {
		return teamPreview{}, err
	}
	preview := teamPreview{Source: source, Commit: commit, Command: command, HostPort: hostPort, Started: time.Now().UTC()}
	if err := b.app(src, command, port, hostPort); err != nil {
		return preview, err
	}
	if err := teamWaitHTTP(fmt.Sprintf("http://127.0.0.1:%d/", hostPort), teamPreviewReady); err != nil {
		logs, _ := b.engineOutput("logs", "--tail", "15", teamPreviewContainer(b.team))
		b.stopPreview()
		return preview, fmt.Errorf("the app didn't answer on port %d within %s; it must listen on 0.0.0.0:$PORT. Its last lines:\n%s", port, teamPreviewReady, strings.TrimSpace(logs))
	}
	out, err := b.run("net", "expose", strconv.Itoa(hostPort), "--link", "--detach", "--json", "--max", max.String(), "--name", "team-"+b.team)
	if err != nil {
		b.stopPreview()
		return preview, fmt.Errorf("hi net expose: %w (the app runs, but only on this machine)", err)
	}
	var exposed netExposeState
	if err := json.Unmarshal(out, &exposed); err != nil || exposed.URL == "" {
		b.stopPreview()
		return preview, fmt.Errorf("hi net expose gave no address: %s", qFirstLine(string(out), "no output"))
	}
	// The link carries its key, so the people just open it.
	preview.URL, preview.Password, preview.Expose, preview.Expires = firstNonEmpty(exposed.Link, exposed.URL), exposed.Password, exposed.Name, exposed.Expires
	data, _ := json.MarshalIndent(preview, "", "  ")
	if err := os.WriteFile(teamPreviewPath(b.team), append(data, '\n'), 0o600); err != nil {
		return preview, err
	}
	return preview, nil
}

// startApp runs the project in its box: the hardening every box has, no
// credentials, and its port published to this machine only.
func (b *teamBroker) startApp(src, command string, port, hostPort int) error {
	engine, _, err := detectBoxEngine()
	if err != nil {
		return err
	}
	image, err := engine.ensureBaseImage(nopWriter{}, nopWriter{})
	if err != nil {
		return err
	}
	run := []string{"run", "-d", "--init", "--name", teamPreviewContainer(b.team), "--hostname", b.team + "-app",
		"-p", fmt.Sprintf("127.0.0.1:%d:%d", hostPort, port)}
	run = append(run, engine.userArgs()...)
	run = append(run, boxHardening("4g")...)
	run = append(run, "-v", src+":/app", "-w", "/app",
		"-e", "HOME=/tmp", "-e", "PORT="+strconv.Itoa(port), "-e", "HOST=0.0.0.0",
		image, "sh", "-c", command)
	name, args := teamOutsideService(engine.bin, run)
	if out, err := exec.Command(name, args...).CombinedOutput(); err != nil {
		return fmt.Errorf("%s: %s", engine.name, qFirstLine(strings.TrimSpace(string(out)), err.Error()))
	}
	return nil
}

// teamOutsideService runs a command in a systemd scope of its own when hi
// team runs under systemd, so the containers and processes it leaves
// behind aren't stopped with the team's service.
func teamOutsideService(name string, args []string) (string, []string) {
	if os.Getenv("INVOCATION_ID") == "" {
		return name, args
	}
	if _, err := exec.LookPath("systemd-run"); err != nil {
		return name, args
	}
	return "systemd-run", append([]string{"--user", "--scope", "--quiet", "--collect", "--", name}, args...)
}

func (b *teamBroker) engineOutput(args ...string) (string, error) {
	engine, _, err := detectBoxEngine()
	if err != nil {
		return "", err
	}
	return engine.output(args...)
}

// stopPreview ends the address and the app's box, if there are any.
func (b *teamBroker) stopPreview() {
	if data, err := os.ReadFile(teamPreviewPath(b.team)); err == nil {
		var preview teamPreview
		if json.Unmarshal(data, &preview) == nil && preview.Expose != "" {
			b.run("net", "expose", "stop", preview.Expose)
		}
		os.Remove(teamPreviewPath(b.team))
	}
	b.engineOutput("rm", "-f", teamPreviewContainer(b.team))
}

type nopWriter struct{}

func (nopWriter) Write(p []byte) (int, error) { return len(p), nil }

func teamFreePort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port, nil
}

// teamWaitHTTP waits until something answers HTTP at url.
func teamWaitHTTP(url string, limit time.Duration) error {
	client := &http.Client{Timeout: 3 * time.Second}
	deadline := time.Now().Add(limit)
	for {
		if response, err := client.Get(url); err == nil {
			response.Body.Close()
			return nil
		} else if time.Now().After(deadline) {
			return err
		}
		time.Sleep(2 * time.Second)
	}
}

func teamShell(dir, script string) (string, error) {
	var out strings.Builder
	err := boxCommand(nil, &out, &out, "sh", "-c", "cd "+shellQuote(dir)+" && "+script)
	return strings.TrimSpace(out.String()), err
}
