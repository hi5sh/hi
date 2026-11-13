package cli

// What an app needs, installed from the page (hi_admin.md, release 8):
// the page checks the tools an app needs on its machine before New app's
// steps, and what the answers add before Create, and installs what's
// missing with hi install, as a job. What needs root takes the sudo
// password typed on the page: the page keeps it in memory for that job
// alone, and the job's sudo asks for it over the page's own socket
// (hi server __askpass), so it's never on disk, in an argument, or in a
// log.

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// adminNeed is one tool an app needs.
type adminNeed struct {
	ID      string `json:"id"` // hi install's
	Title   string `json:"title"`
	For     string `json:"for"`
	Root    bool   `json:"root"` // installing it takes the sudo password
	Present bool   `json:"present"`
}

// installHomeOnly are the tools hi install puts in the home folder,
// which need no root; the setup script keeps the same list.
var installHomeOnly = []string{"uv", "omp", "claude", "codex", "agy", "gemini-api", "herdr"}

// adminNeedPresent says whether a tool is installed; tests replace it.
var adminNeedPresent = func(id string) bool {
	if id == "hermes" {
		return hermesPresent()
	}
	item, ok := findInstallItem(id)
	return ok && itemInstalled(item)
}

func adminNeedOf(id, title, why string) adminNeed {
	return adminNeed{ID: id, Title: title, For: why, Root: !slices.Contains(installHomeOnly, id), Present: adminNeedPresent(id)}
}

// adminNeedsBase is what every app needs.
func adminNeedsBase() []adminNeed {
	return []adminNeed{
		adminNeedOf("podman", "Podman", "every box"),
		adminNeedOf("hermes", "Hermes", "the lead; hi adds its Slack support when the lead starts"),
		adminNeedOf("herdr", "herdr", "the dev box, and with it the app's NetBird peer and addresses"),
		adminNeedOf("restic", "restic", "the snapshots"),
		adminNeedOf("linger", "Start at boot", "the app running after logout and at boot"),
	}
}

// adminNeedsOfAnswers is what an app's answers add: the agents its roles
// run, and the CLI for a copy on GitHub or GitLab.
func adminNeedsOfAnswers(config appConfig, github, gitlab bool) []adminNeed {
	var needs []adminNeed
	agents := map[string]bool{}
	for _, member := range config.Members {
		agents[member.Agent] = true
	}
	if agents["claude"] {
		needs = append(needs, adminNeedOf("claude", "Claude Code", "the roles on Claude Code"))
	}
	if agents["codex"] {
		needs = append(needs, adminNeedOf("codex", "Codex", "the roles on Codex"))
	}
	if github {
		needs = append(needs, adminNeedOf("gh", "GitHub CLI", "the copy on GitHub"))
	}
	if gitlab {
		needs = append(needs, adminNeedOf("glab", "GitLab CLI", "the copy on GitLab"))
	}
	return needs
}

// adminNeedsView is what the page shows: the needs, and whether any is
// missing.
type adminNeedsView struct {
	Needs   []adminNeed `json:"needs"`
	Missing []string    `json:"missing"`
}

func adminNeedsViewOf(needs []adminNeed) adminNeedsView {
	view := adminNeedsView{Needs: needs, Missing: []string{}}
	for _, need := range needs {
		if !need.Present {
			view.Missing = append(view.Missing, need.ID)
		}
	}
	return view
}

// adminNeedsOf is what the page checks: every app's needs, and with a
// draft's or an app's name, what its answers add.
func adminNeedsOf(name string) adminNeedsView {
	needs := adminNeedsBase()
	if appNamePattern.MatchString(name) {
		if draft, err := loadAdminDraft(name); err == nil {
			code := draft.Answers
			needs = append(needs, adminNeedsOfAnswers(code.appConfig, code.GitHub != "", code.GitLab != "")...)
		} else if config, err := loadTeam(name); err == nil {
			needs = append(needs, adminNeedsOfAnswers(config, false, false)...)
		}
	}
	return adminNeedsViewOf(needs)
}

// adminNeedsMissingError says what an app still needs, for New app and
// Create to refuse with.
func adminNeedsMissingError(view adminNeedsView) error {
	if len(view.Missing) == 0 {
		return nil
	}
	var titles []string
	for _, need := range view.Needs {
		if !need.Present {
			titles = append(titles, need.Title)
		}
	}
	return fmt.Errorf("this machine lacks %s: install %s first, above", strings.Join(titles, ", "), map[bool]string{true: "it", false: "them"}[len(titles) == 1])
}

// adminInstallJob installs tools an app needs, as hi install does. A
// tool that needs root takes the sudo password, which the page keeps for
// the job alone.
func (s *adminServer) installJob(ids []string, password string, admin adminPerson) (adminJob, error) {
	known := map[string]adminNeed{}
	for _, need := range append(adminNeedsBase(), adminNeedsOfAnswers(appConfig{Members: map[string]appMember{"a": {Agent: "claude"}, "b": {Agent: "codex"}}}, true, true)...) {
		known[need.ID] = need
	}
	var titles []string
	root := false
	ids = slices.Compact(slices.Sorted(slices.Values(ids)))
	for _, id := range ids {
		need, ok := known[id]
		if !ok {
			return adminJob{}, fmt.Errorf("the page doesn't install %q", id)
		}
		titles = append(titles, need.Title)
		root = root || need.Root
	}
	if len(ids) == 0 {
		return adminJob{}, errors.New("nothing to install")
	}
	job := adminJob{Title: "Installed " + adminAnd(titles), Args: append([]string{"install"}, ids...), By: admin.Name}
	if root {
		if password == "" {
			return adminJob{}, errors.New("installing " + adminAnd(titles) + " needs the machine's sudo password")
		}
		token, err := adminNewKey()
		if err != nil {
			return adminJob{}, err
		}
		s.askpass.put(token, password)
		file, err := adminWriteJobEnv(map[string]string{adminAskpassEnv: token})
		if err != nil {
			s.askpass.forget(token)
			return adminJob{}, err
		}
		job.Env = file
	}
	return s.startJob(job)
}

// adminAnd joins names as people say them: "herdr and restic".
func adminAnd(names []string) string {
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}

// adminWriteJobEnv writes a job's secrets file, which its runner reads
// once and deletes.
func adminWriteJobEnv(values map[string]string) (string, error) {
	if err := os.MkdirAll(adminJobsDir(), 0o700); err != nil {
		return "", err
	}
	key, err := adminNewKey()
	if err != nil {
		return "", err
	}
	path := filepath.Join(adminJobsDir(), ".env-"+key)
	data, _ := json.Marshal(values)
	return path, writeFileAtomic(path, data, 0o600)
}

// ---------------------------------------------------------------------------
// the password, for the job's sudo

// adminAskpassEnv names the job's token, which its sudo hands back for the
// password.
const adminAskpassEnv = "HI_ASKPASS_TOKEN"

// adminAskpassLasts is how long the page keeps a job's password at most;
// the job's end forgets it sooner.
var adminAskpassLasts = 30 * time.Minute

type adminAskpass struct {
	mu        sync.Mutex
	passwords map[string]adminAskpassEntry
}

type adminAskpassEntry struct {
	password string
	ends     time.Time
}

func (a *adminAskpass) put(token, password string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.passwords == nil {
		a.passwords = map[string]adminAskpassEntry{}
	}
	a.passwords[token] = adminAskpassEntry{password: password, ends: time.Now().Add(adminAskpassLasts)}
}

func (a *adminAskpass) get(token string) (string, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for key, entry := range a.passwords {
		if time.Now().After(entry.ends) {
			delete(a.passwords, key)
		}
	}
	entry, ok := a.passwords[token]
	return entry.password, ok
}

func (a *adminAskpass) forget(token string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.passwords, token)
}

func adminAskpassSocket() string { return filepath.Join(adminSocketDir(), "askpass.sock") }

// serveAskpass answers the jobs' sudo on the page's own socket: a line
// "get <token>" has the password back, "forget <token>" drops it.
func (s *adminServer) serveAskpass(listener net.Listener) {
	for {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		go func() {
			defer conn.Close()
			conn.SetDeadline(time.Now().Add(10 * time.Second))
			line, err := bufio.NewReader(io.LimitReader(conn, 256)).ReadString('\n')
			if err != nil {
				return
			}
			what, token, _ := strings.Cut(strings.TrimSpace(line), " ")
			switch what {
			case "get":
				if password, ok := s.askpass.get(token); ok {
					fmt.Fprintln(conn, password)
				}
			case "forget":
				s.askpass.forget(token)
			}
		}()
	}
}

// adminAskpassCall asks the page's socket; tests replace the socket's
// place with HI_ADMIN_DIR.
func adminAskpassCall(what, token string) (string, error) {
	conn, err := net.DialTimeout("unix", adminAskpassSocket(), 5*time.Second)
	if err != nil {
		return "", fmt.Errorf("the admin page isn't there to give the password: %w", err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(10 * time.Second))
	fmt.Fprintf(conn, "%s %s\n", what, token)
	answer, _ := io.ReadAll(io.LimitReader(conn, 4096))
	return strings.TrimRight(string(answer), "\n"), nil
}

// adminAskpassMain is hi server __askpass <token>: sudo's askpass program
// in a job, printing the password the page keeps for it.
func adminAskpassMain(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "usage: hi server __askpass <token>")
		return 2
	}
	password, err := adminAskpassCall("get", args[0])
	if err != nil || password == "" {
		fmt.Fprintln(stderr, "hi: the admin page has no password for this job any more; install again from the page")
		return 1
	}
	fmt.Fprintln(stdout, password)
	return 0
}

// adminJobSudo makes a job's sudo ask the page for the password: a sudo
// first on its PATH that runs the real one with -A, and an askpass that
// is hi server __askpass with the job's token. It returns the env to run
// with, and what to do when the job ends.
func adminJobSudo(env []string, executable string) ([]string, func(), error) {
	token := ""
	for _, value := range env {
		if name, rest, ok := strings.Cut(value, "="); ok && name == adminAskpassEnv {
			token = rest
		}
	}
	if token == "" {
		return env, func() {}, nil
	}
	dir, err := os.MkdirTemp("", "hi-askpass-")
	if err != nil {
		return env, func() {}, err
	}
	done := func() {
		os.RemoveAll(dir)
		adminAskpassCall("forget", token)
	}
	sudo := "#!/bin/sh\nexec /usr/bin/sudo -A \"$@\"\n"
	askpass := "#!/bin/sh\nexec '" + strings.ReplaceAll(executable, "'", `'\''`) + "' server __askpass '" + token + "'\n"
	if err := os.WriteFile(filepath.Join(dir, "sudo"), []byte(sudo), 0o700); err != nil {
		done()
		return env, func() {}, err
	}
	if err := os.WriteFile(filepath.Join(dir, "askpass"), []byte(askpass), 0o700); err != nil {
		done()
		return env, func() {}, err
	}
	out := []string{}
	path := ""
	for _, value := range env {
		name, rest, _ := strings.Cut(value, "=")
		switch name {
		case adminAskpassEnv:
			continue
		case "PATH":
			path = rest
			continue
		}
		out = append(out, value)
	}
	out = append(out, "PATH="+dir+string(os.PathListSeparator)+path, "SUDO_ASKPASS="+filepath.Join(dir, "askpass"))
	return out, done, nil
}
