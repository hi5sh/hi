package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The broker is the team's service: hi team __serve listens on a socket in
// the team's run/ folder, which the lead's box gets at /team/run, where the
// lead files tickets, reads the versions, the app's chats, jobs, and
// incidents, and restarts the app once in an incident. The owners build in
// the dev box, on a socket of its own (team_devwork.go), and release from
// the dev page (team_devpage.go). The lead never holds the repository's
// credentials, and nothing it asks for moves production.

const (
	teamSocketName = "broker.sock"
	teamSocketEnv  = "HI_TEAM_SOCKET"
)

// teamTask is tasks/<id>.json.
type teamTask struct {
	ID     string `json:"id"`
	Role   string `json:"role"`
	Agent  string `json:"agent"`
	Model  string `json:"model,omitempty"`
	Kind   string `json:"kind"`             // task or review
	On     string `json:"on,omitempty"`     // a task this one continues, on its branch
	Review string `json:"review,omitempty"` // for a review: the task it reviews
	Commit string `json:"commit,omitempty"` // for a review: the commit it reviewed
	// Next is the review or fix the broker started when this one finished;
	// Round, the review round the two belong to.
	Next    string       `json:"next,omitempty"`
	Round   int          `json:"round,omitempty"`
	Title   string       `json:"title"`
	Box     string       `json:"box"`
	Branch  string       `json:"branch,omitempty"`
	Status  string       `json:"status"`            // starting, running, done, failed, stopped, merged, discarded
	Verdict string       `json:"verdict,omitempty"` // for a review: approve or changes
	Error   string       `json:"error,omitempty"`
	Report  *agentReport `json:"report,omitempty"`
	Merged  string       `json:"merged,omitempty"` // the merge commit
	// SpecsKept are the lead's spec files the task changed; the merge left
	// those changes out.
	SpecsKept []string `json:"specs_kept,omitempty"`
	// Files is the folder of files the member gets at /box/files.
	Files string `json:"files,omitempty"`
	// Session is the folder that keeps the member's conversation for the
	// piece of work (hi agent --session), and Resumes says this step goes
	// on with it (team_threads.go).
	Session string `json:"session,omitempty"`
	Resumes bool   `json:"resumes,omitempty"`
	// Approval is the message the work started on; Small, a fix started
	// without one.
	Approval *teamApproval `json:"approval,omitempty"`
	Small    bool          `json:"small,omitempty"`
	// Thread is a small fix's Slack thread: the one it was asked for in
	// (--in), else that of hi's message that it started.
	Thread string `json:"thread,omitempty"`
	// Packages is what a merged task changed in the project's packages, and
	// TrustApproval the owner's yes to new install scripts.
	Packages      string        `json:"packages,omitempty"`
	TrustApproval *teamApproval `json:"trust_approval,omitempty"`
	// Hints are what the broker told the lead when the task started: the
	// work it continues instead, the ideas still open.
	Hints []string `json:"hints,omitempty"`
	// Findings are the deep review's findings the task sets out to fix
	// (--finding), so the next review can say they weren't seen since.
	Findings []teamTaskFinding `json:"findings,omitempty"`
	// Survey is an adopted project's first task, which hi starts
	// (team_adopt.go). UpstreamLabel and UpstreamCommit are what a task
	// with --upstream brings in from it (team_upstream.go).
	Survey         bool   `json:"survey,omitempty"`
	UpstreamLabel  string `json:"upstream_label,omitempty"`
	UpstreamCommit string `json:"upstream_commit,omitempty"`
	// Proposed is the draft pull request that offered merged work back to
	// the adopted project, on ProposeApproval, an owner's yes
	// (team_propose.go).
	Proposed        string        `json:"proposed,omitempty"`
	ProposedAt      time.Time     `json:"proposed_at,omitempty"`
	ProposeApproval *teamApproval `json:"propose_approval,omitempty"`
	// Incident is the incident its failure opened, or added to; ForIncident
	// the one the lead started it for (team_incidents.go).
	Incident    string    `json:"incident,omitempty"`
	ForIncident string    `json:"for_incident,omitempty"`
	Created     time.Time `json:"created"`
	Finished    time.Time `json:"finished,omitempty"`
}

// teamSpecsRule ends every brief: the specs are the lead's, and a merge
// leaves out a member's changes to them.
const teamSpecsRule = "docs/specs/ is the lead's: read it, but don't change it, not even a feature's status; the lead updates it. docs/specs/notes/ holds the lead's notes for the team (research, how outside services behave, what the people shared): read the ones your brief names, and look there before you research something yourself."

var (
	teamTaskID     = regexp.MustCompile(`^t-[0-9]{1,6}$`)
	teamVersionArg = regexp.MustCompile(`^v[1-9][0-9]{0,5}$`)
)

func teamTaskDir(team string) string { return filepath.Join(teamDir(team), "tasks") }

func loadTeamTask(team, id string) (teamTask, error) {
	var task teamTask
	if !teamTaskID.MatchString(id) {
		return task, fmt.Errorf("a shipped change is named like t-3, not %q", id)
	}
	data, err := os.ReadFile(filepath.Join(teamTaskDir(team), id+".json"))
	if errors.Is(err, os.ErrNotExist) {
		return task, fmt.Errorf("no shipped change %s", id)
	}
	if err != nil {
		return task, err
	}
	return task, json.Unmarshal(data, &task)
}

func saveTeamTask(team string, task teamTask) error {
	data, _ := json.MarshalIndent(task, "", "  ")
	path := filepath.Join(teamTaskDir(team), task.ID+".json")
	if err := os.WriteFile(path+".tmp", append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(path+".tmp", path)
}

// loadTeamTasks lists a team's tasks, oldest first.
func loadTeamTasks(team string) []teamTask {
	entries, _ := os.ReadDir(teamTaskDir(team))
	var tasks []teamTask
	for _, entry := range entries {
		id, ok := strings.CutSuffix(entry.Name(), ".json")
		if !ok {
			continue
		}
		if task, err := loadTeamTask(team, id); err == nil {
			tasks = append(tasks, task)
		}
	}
	sort.Slice(tasks, func(i, j int) bool { return teamTaskNumber(tasks[i].ID) < teamTaskNumber(tasks[j].ID) })
	return tasks
}

func teamTaskNumber(id string) int {
	n, _ := strconv.Atoi(strings.TrimPrefix(id, "t-"))
	return n
}

// ---------------------------------------------------------------------------
// the broker, on the host

type teamBroker struct {
	team string
	repo string
	hi   string // this hi binary
	// httpsCerts is the machine's domain's certificate, for the dev page on
	// https (team_https.go); peerRestart replaces restartPeer in tests.
	httpsCerts  teamHTTPSCerts
	peerRestart func()
	mu          sync.Mutex
	// previewMu keeps one preview start or stop at a time.
	previewMu sync.Mutex
	// app starts the project's box for a slot; tests replace it.
	app func(slot, name, src, data, address, command string, port, hostPort int) error
	// exposed finds a running hi net expose by name, and running says an
	// app's box runs; tests replace them.
	// devPageMu guards the dev page's address, and devPageFailed is when
	// putting it on one last failed.
	devPageMu     sync.Mutex
	devPageFailed time.Time
	exposed       func(name string) (netExposeState, bool)
	running       func(container string) bool
	// oldImage says an app's box runs hi's base image as it was before
	// hi team rebuild built it again; tests replace it.
	oldImage func(container string) bool
	// answers says the app on a host port answers at all (team_health.go);
	// tests replace it.
	answers func(hostPort int) bool
	// removeApp removes an app's box; tests replace it.
	removeApp func(container string)
	// wakeLead adds a job to the lead's Hermes that runs once, soon, with a
	// prompt, its answer delivered to a Slack thread (team_handoff.go);
	// nil in tests that don't set it.
	wakeLead func(name, prompt, deliver string) error
	// pollChannel logs what's new in the channel now; nil in tests.
	pollChannel func() error
	// channel is the team's Slack channel, to check approvals that aren't
	// logged yet and to say when a small fix starts; nil in tests.
	channel teamChannelSource
	// run starts hi with arguments in repo/ and returns its stdout; tests
	// replace it.
	run func(args ...string) ([]byte, error)
	// takeSnapshot backs up the team's folder, with the boxes paused; tests
	// replace it.
	takeSnapshot func(kind, label string) (teamSnapshot, error)
	// restoreData puts app/ back from a snapshot; tests replace it.
	restoreData func(id string) error
	// lead answers a message from the app's chat as the lead, calling delta
	// with its text as it comes; tests replace it (team_helper.go).
	lead func(ctx context.Context, conversation, input string, pictures []teamLeadPicture, delta func(string)) (string, error)
	// webMu guards the web chats; answering, those the lead is answering;
	// webListeners, the panels that show each chat (team_web.go).
	webMu        sync.Mutex
	answering    map[string]bool
	webListeners map[string]map[chan teamWebMessage]bool
	// sayMu guards run/say.json, the messages from hi team say.
	sayMu sync.Mutex
	// jobsMu guards run/jobs.json, and incidentMu the incidents
	// (team_jobs.go, team_incidents.go). started is when this broker
	// started: a box that stopped before it isn't an incident. appLogs and
	// appExit read an app's box; tests replace them.
	jobsMu     sync.Mutex
	incidentMu sync.Mutex
	started    time.Time
	appLogs    func(container string) string
	appExit    func(container string) (exit int, finished time.Time, ok bool)
	// signins are owners' sign-ins under way from Slack
	// (team_signin_slack.go).
	signins teamSignins
	// peopleMu guards people.json, the keys of people signed in with /hi
	// (team_people.go).
	peopleMu sync.Mutex
	// help answers a question in the app from its guide, with no tools;
	// tests replace it. counts are the day's questions and feedback, for
	// the limits (team_feedback.go).
	help   func(ctx context.Context, team string, messages []teamHelpTurn, delta func(string)) (string, error)
	counts teamWebCounts
	// mcp keeps the app's MCP sessions, for the lead (team_mcp.go).
	mcp teamMCPApps
}

func newTeamBroker(team, hi string) *teamBroker {
	b := &teamBroker{team: team, repo: filepath.Join(teamDir(team), "repo"), hi: hi, answering: map[string]bool{}, webListeners: map[string]map[chan teamWebMessage]bool{}}
	b.help = teamHelpModel
	b.started = time.Now()
	b.appLogs = func(container string) string {
		return b.lastLines(container, teamIncidentLogLines)
	}
	b.appExit = b.appExitState
	b.lead = func(context.Context, string, string, []teamLeadPicture, func(string)) (string, error) {
		return "", errors.New("the lead's API server isn't set up")
	}
	b.app = b.startApp
	b.takeSnapshot = func(kind, label string) (teamSnapshot, error) {
		return teamTakeSnapshot(team, kind, label, teamPause)
	}
	b.restoreData = func(id string) error { return teamRestoreData(team, id) }
	// An internal address is always there: its peer and relay are the
	// team's own, not an exposure.
	b.exposed = func(name string) (netExposeState, bool) {
		if teamIsInternal(name) {
			return netExposeState{Name: name}, true
		}
		return teamExposed(name)
	}
	b.running = b.appRunning
	b.oldImage = b.appOnOldImage
	b.answers = teamAppAnswers
	b.removeApp = func(container string) { b.engineOutput("rm", "-f", container) }
	b.run = func(args ...string) ([]byte, error) {
		// What a member or a preview starts must outlive a restart of the
		// team; only the waiters belong to it, and resume starts them again.
		name, all := b.hi, args
		if !(len(args) > 1 && args[0] == "agent" && args[1] == "wait") {
			name, all = teamOutsideService(b.hi, args)
		}
		command := exec.Command(name, all...)
		command.Dir = b.repo
		var stderr strings.Builder
		command.Stderr = &stderr
		out, err := command.Output()
		if err != nil {
			return out, fmt.Errorf("%w: %s", err, strings.ReplaceAll(lastLines(strings.TrimSpace(stderr.String()), 3), "\n", " "))
		}
		return out, nil
	}
	return b
}

// listen opens the lead's socket, then serves it until ctx ends.
func (b *teamBroker) listen(ctx context.Context, socket string) (func() error, error) {
	return b.listenWith(ctx, socket, b.handler())
}

// listenHost opens the host's socket, which no box sees.
func (b *teamBroker) listenHost(ctx context.Context, socket string) (func() error, error) {
	if err := os.MkdirAll(filepath.Dir(socket), 0o700); err != nil {
		return nil, err
	}
	return b.listenWith(ctx, socket, b.hostHandler())
}

func (b *teamBroker) listenWith(ctx context.Context, socket string, handler http.Handler) (func() error, error) {
	os.Remove(socket)
	listener, err := net.Listen("unix", socket)
	if err != nil {
		return nil, err
	}
	os.Chmod(socket, 0o600)
	return func() error { return b.serve(ctx, listener, handler) }, nil
}

func (b *teamBroker) serve(ctx context.Context, listener net.Listener, handler http.Handler) error {
	server := &http.Server{Handler: handler}
	go func() {
		<-ctx.Done()
		server.Close()
	}()
	if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// resume picks up tasks that were running when the broker last stopped.
func (b *teamBroker) resume() {
	for _, task := range loadTeamTasks(b.team) {
		if task.Status != "running" && task.Status != "starting" {
			continue
		}
		// From hi 2.4 the lead hands out no work: what ran is let go.
		task.Status, task.Error, task.Finished = "stopped", "hi 2.4 builds in the dev box; the lead no longer hands out tasks", time.Now().UTC()
		saveTeamTask(b.team, task)
	}
	b.resumeDeepReviews()
	b.resumeBrainstorms()
}

func (b *teamBroker) handler() http.Handler {
	mux := http.NewServeMux()
	reply := func(w http.ResponseWriter, value any, err error) {
		w.Header().Set("Content-Type", "application/json")
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		json.NewEncoder(w).Encode(value)
	}
	mux.HandleFunc("GET /env", func(w http.ResponseWriter, r *http.Request) {
		secrets, err := loadTeamSecrets(b.team)
		reply(w, teamEnvNames(secrets), err)
	})
	// The lead files what the people ask for; it doesn't close tickets.
	mux.HandleFunc("POST /ticket", func(w http.ResponseWriter, r *http.Request) {
		var request teamTicketRequest
		if err := json.NewDecoder(io.LimitReader(r.Body, teamMaxAttachments*4/3+1<<20)).Decode(&request); err != nil {
			reply(w, nil, err)
			return
		}
		if request.Action == "close" || teamTicketOwnerActions[request.Action] {
			reply(w, nil, errors.New("the owners close tasks, once the work is shipped, and keep them tidy on the dev page; add what you know with hi team task comment"))
			return
		}
		request.Author = "the lead"
		if request.Action == "add" || request.Action == "comment" {
			request.Files = append(request.Files, b.threadFiles(request)...)
		}
		result, err := teamTicketDo(b.team, request)
		reply(w, result, err)
	})
	mux.HandleFunc("GET /data", func(w http.ResponseWriter, r *http.Request) {
		reply(w, teamDataFolders(b.team, b.running), nil)
	})
	mux.HandleFunc("GET /helper", func(w http.ResponseWriter, r *http.Request) {
		config, err := loadTeam(b.team)
		reply(w, teamHelperLine(config), err)
	})
	mux.HandleFunc("GET /web", func(w http.ResponseWriter, r *http.Request) {
		reply(w, teamWebList(b.team, time.Now()), nil)
	})
	mux.HandleFunc("GET /web/show", func(w http.ResponseWriter, r *http.Request) {
		id := r.URL.Query().Get("id")
		if strings.HasPrefix(id, "f-") {
			text, err := teamFeedbackShow(b.team, id)
			reply(w, text, err)
			return
		}
		text, err := teamWebShow(b.team, id)
		reply(w, text, err)
	})
	mux.HandleFunc("GET /web/questions", func(w http.ResponseWriter, r *http.Request) {
		reply(w, teamWebQuestionsText(b.team, time.Now()), nil)
	})
	mux.HandleFunc("GET /web/may", func(w http.ResponseWriter, r *http.Request) {
		config, err := loadTeam(b.team)
		user := r.URL.Query().Get("user")
		if err == nil && (!slackIDPattern.MatchString(user) || !b.teamMayAsk(config, user)) {
			err = fmt.Errorf("%s may not ask the team for work", user)
		}
		reply(w, "yes", err)
	})
	mux.HandleFunc("POST /web/link", func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			User string `json:"user"`
			Dev  bool   `json:"dev,omitempty"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&request); err != nil {
			reply(w, nil, err)
			return
		}
		line, err := b.webLink(request.User, request.Dev)
		reply(w, line, err)
	})
	mux.HandleFunc("POST /web/signin", func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			User     string `json:"user"`
			Provider string `json:"provider"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&request); err != nil {
			reply(w, nil, err)
			return
		}
		line, err := b.webSignin(request.User, request.Provider)
		reply(w, line, err)
	})
	b.incidentRoutes(mux, reply)
	mux.HandleFunc("GET /upstream", func(w http.ResponseWriter, r *http.Request) {
		view, err := b.upstream()
		reply(w, view, err)
	})
	mux.HandleFunc("GET /versions", func(w http.ResponseWriter, r *http.Request) {
		view, err := teamVersionsReport(b.team)
		reply(w, view, err)
	})
	mux.HandleFunc("POST /mcp/{slot}", b.leadMCPRoute)
	// An owner asked the lead to start a task in the dev box.
	mux.HandleFunc("POST /start", func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Number int    `json:"number"`
			User   string `json:"user"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&request); err != nil {
			reply(w, nil, err)
			return
		}
		line, err := b.leadStart(request.Number, request.User)
		reply(w, line, err)
	})
	return mux
}

// commitSpecs commits the lead's changes to the project's specs folder to
// the team's branch, and pushes them when the team has a remote. They are
// the only commits that skip review: documentation, not code.
func (b *teamBroker) commitSpecs() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	config, err := loadTeam(b.team)
	if err != nil {
		return err
	}
	return b.commitSpecsLocked(config)
}

func (b *teamBroker) commitSpecsLocked(config teamConfig) error {
	if boxGit(b.repo, "status", "--porcelain", "--", teamSpecsPath) == "" {
		return nil
	}
	if current := boxGit(b.repo, "branch", "--show-current"); current != config.Branch {
		return fmt.Errorf("repo/ is on %s, not %s", current, config.Branch)
	}
	if out, err := teamGit(b.repo, "add", "-A", "--", teamSpecsPath); err != nil {
		return fmt.Errorf("git add: %s", out)
	}
	if out, err := teamGit(b.repo, "commit", "-q", "-m", "Update the specs", "--", teamSpecsPath); err != nil {
		return fmt.Errorf("git commit: %s", out)
	}
	if config.Remote != "" {
		if out, err := teamGit(b.repo, "push", "-q", "origin", config.Branch); err != nil {
			return fmt.Errorf("committed, but git push failed: %s", qFirstLine(out, err.Error()))
		}
	}
	return nil
}

func teamIsAncestor(repo, branch, of string) bool {
	if branch == "" {
		return false
	}
	return exec.Command("git", "-C", repo, "merge-base", "--is-ancestor", branch, of).Run() == nil
}

// teamGit runs git in the repository as the team's lead.
func teamGit(repo string, args ...string) (string, error) {
	name := firstNonEmpty(boxGit(repo, "config", "user.name"), "hi team")
	email := firstNonEmpty(boxGit(repo, "config", "user.email"), "team@hi.invalid")
	out, err := exec.Command("git", append([]string{"-C", repo, "-c", "user.name=" + name, "-c", "user.email=" + email}, args...)...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// ---------------------------------------------------------------------------
// the lead's commands, in its box

// teamLeadUsage is hi team --help in the lead's box: the commands from
// the hi-team skill's table, so the two never differ.
func teamLeadUsage() string {
	var text strings.Builder
	text.WriteString("The team's commands, for its lead (the hi-team skill says when to use each):\n\n")
	for _, line := range strings.Split(teamSkill(teamConfig{Name: "<team>"}), "\n") {
		if !strings.HasPrefix(line, "| hi team ") {
			continue
		}
		cells := strings.Split(strings.ReplaceAll(strings.Trim(line, "| "), `\|`, "\x00"), " | ")
		if len(cells) != 2 {
			continue
		}
		fmt.Fprintf(&text, "  %s\n      %s\n", strings.ReplaceAll(cells[0], "\x00", "|"), strings.ReplaceAll(cells[1], "\x00", "|"))
	}
	return text.String()
}

var teamLeadCommands = map[string]bool{"versions": true, "web": true, "alert": true, "mcp": true}

// teamLeadOrHost are commands both the lead and the people on the machine
// have; in the lead's box they go to its socket.
var teamLeadOrHost = map[string]bool{"env": true, "helper": true, "data": true, "upstream": true, "jobs": true, "incidents": true, "advisories": true,
	"task": true, "tasks": true, "ticket": true, "tickets": true}

func runTeamLeadCommand(command string, args []string, stdin io.Reader, stdout io.Writer) error {
	socket := os.Getenv(teamSocketEnv)
	if socket == "" {
		return errors.New("hi team " + command + " is for a team's lead, in its box; on this machine, hi team status <name> shows a team")
	}
	client := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}}
	call := func(method, path string, body any, out any) error {
		var reader io.Reader
		if body != nil {
			data, _ := json.Marshal(body)
			reader = strings.NewReader(string(data))
		}
		request, _ := http.NewRequest(method, "http://team"+path, reader)
		response, err := client.Do(request)
		if err != nil {
			return fmt.Errorf("the team's broker isn't answering: %w", err)
		}
		defer response.Body.Close()
		data, _ := io.ReadAll(response.Body)
		if response.StatusCode != http.StatusOK {
			var failure struct{ Error string }
			json.Unmarshal(data, &failure)
			return errors.New(firstNonEmpty(failure.Error, response.Status))
		}
		return json.Unmarshal(data, out)
	}
	switch command {
	case "mcp":
		return runTeamMCPBridge(args, stdin, stdout, client)
	case "task", "tasks", "ticket", "tickets":
		if len(args) > 0 && args[0] == "start" {
			// Hermes tells its commands who wrote: only an owner starts work.
			number, err := strconv.Atoi(strings.TrimPrefix(strings.Join(args[1:], ""), "#"))
			if len(args) != 2 || err != nil {
				return usageError{"usage: hi team task start <n>  (when an owner asks: work on it starts in the dev box)"}
			}
			var line string
			if err := call("POST", "/start", map[string]any{"number": number, "user": os.Getenv("HERMES_SESSION_USER_ID")}, &line); err != nil {
				return err
			}
			fmt.Fprintln(stdout, line)
			return nil
		}
		request, err := parseTeamTicketArgs(args, stdin)
		if err != nil {
			return err
		}
		// Hermes tells its commands the conversation they're in: the files
		// shared there go with the ticket.
		if request.Action == "add" || request.Action == "comment" {
			request.Chat = os.Getenv("HERMES_SESSION_CHAT_ID")
			request.Thread = firstNonEmpty(os.Getenv("HERMES_SESSION_THREAD_ID"), os.Getenv("HERMES_SESSION_MESSAGE_ID"))
		}
		var result teamTicketResult
		if err := call("POST", "/ticket", request, &result); err != nil {
			return err
		}
		if request.Action == "files" {
			return teamSaveFetchedTicketFiles(request.Number, result, stdout)
		}
		printTeamTicketResult(request, result, stdout)
		return nil
	case "env":
		if len(args) != 0 {
			return usageError{"usage: hi team env (the names of the settings the app gets; the people add them on the machine)"}
		}
		var names []string
		if err := call("GET", "/env", nil, &names); err != nil {
			return err
		}
		if len(names) == 0 {
			fmt.Fprintln(stdout, "The app gets no settings. The people add them on the machine: hi team env <team> KEY=VALUE")
		} else {
			fmt.Fprintf(stdout, "The app gets these settings as environment variables: %s\n", strings.Join(names, ", "))
		}
		fmt.Fprintf(stdout, "A setting marked production only isn't in the preview: there the app must do without it. The people set those on the machine with hi team env <team> --production KEY, for a key that sends, posts, orders, or pays per call.\n")
		fmt.Fprintf(stdout, "Models: $OPENROUTER_BASE_URL with $OPENROUTER_API_KEY, in previews and production; the preview's calls stop for the day at a limit ($5 unless the people set another), and production's have none. $HI_SLOT says which one the app is (preview or production), and $HI_DATA_DIR is its data folder.\n")
	case "versions":
		if len(args) != 0 {
			return usageError{"usage: hi team versions"}
		}
		var view teamVersionsView
		if err := call("GET", "/versions", nil, &view); err != nil {
			return err
		}
		printTeamVersions(view, stdout)
	case "data":
		if len(args) != 0 {
			return usageError{"usage: hi team data  (the data folders; the owners fork and copy them on the dev page)"}
		}
		var folders []teamDataFolder
		if err := call("GET", "/data", nil, &folders); err != nil {
			return err
		}
		printTeamDataFolders(stdout, folders)
		fmt.Fprintln(stdout, "The app sees its own as $HI_DATA_DIR, and $HI_SLOT says which it is. Production owns the data; the preview runs on a fork of it, and what it writes stays its own. The owners give the preview a new fork, or production the preview's data, from the dev box and the dev page.")
	case "helper":
		if len(args) != 0 {
			return usageError{"usage: hi team helper  (whether the app has the helper; the owners turn it on or off on the dev page)"}
		}
		var line string
		if err := call("GET", "/helper", nil, &line); err != nil {
			return err
		}
		fmt.Fprintln(stdout, line)
	case "web":
		usage := usageError{"usage: hi team web  (the feedback in the app, last week)\n       hi team web show <feedback>\n       hi team web questions  (what people asked the app's help)\n       hi team web link --user <Slack member ID>  (DM someone their own links: the app's, and the dev page for owners and developers)\n       hi team web signin chatgpt|claude --user <Slack member ID>  (an owner signs the team in again, in a DM)"}
		var line string
		switch {
		case len(args) == 0:
			if err := call("GET", "/web", nil, &line); err != nil {
				return err
			}
		case len(args) == 1 && args[0] == "questions":
			if err := call("GET", "/web/questions", nil, &line); err != nil {
				return err
			}
		case len(args) == 3 && args[0] == "may" && args[1] == "--user":
			// For the plugin: whether someone's DM is the lead's to answer.
			if err := call("GET", "/web/may?user="+url.QueryEscape(args[2]), nil, &line); err != nil {
				return err
			}
		case len(args) == 3 && args[0] == "link" && args[1] == "--user":
			if err := call("POST", "/web/link", map[string]string{"user": args[2]}, &line); err != nil {
				return err
			}
		case len(args) == 4 && args[0] == "link" && args[1] == "--user" && args[3] == "--dev":
			// For the plugin: /hi-<team> dev, owners only.
			if err := call("POST", "/web/link", map[string]any{"user": args[2], "dev": true}, &line); err != nil {
				return err
			}
		case len(args) == 4 && args[0] == "signin" && args[2] == "--user":
			// For the plugin: /hi-<team> signin chatgpt|claude, owners only.
			if err := call("POST", "/web/signin", map[string]string{"provider": args[1], "user": args[3]}, &line); err != nil {
				return err
			}
		case len(args) == 2 && args[0] == "show":
			if err := call("GET", "/web/show?id="+url.QueryEscape(args[1]), nil, &line); err != nil {
				return err
			}
		default:
			return usage
		}
		fmt.Fprintln(stdout, strings.TrimRight(line, "\n"))
	case "jobs", "incidents", "alert":
		return runTeamIncidentLeadCommand(command, args, stdout, call)
	case "advisories":
		var view teamAdvisoriesView
		if err := call("GET", "/advisories", nil, &view); err != nil {
			return err
		}
		printTeamAdvisories(view, stdout)
		return nil
	case "upstream":
		if len(args) != 0 {
			return usageError{"usage: hi team upstream"}
		}
		var view teamUpstreamView
		if err := call("GET", "/upstream", nil, &view); err != nil {
			return err
		}
		printTeamUpstream(view, stdout)
	}
	return nil
}

// teamReadAttachment reads a file to send with a message.
func teamReadAttachment(path string) (teamAttachment, error) {
	info, err := os.Stat(path)
	if err != nil {
		return teamAttachment{}, err
	}
	if !info.Mode().IsRegular() {
		return teamAttachment{}, fmt.Errorf("%s isn't a file; attach files one by one", path)
	}
	if info.Size() > teamMaxAttachment {
		return teamAttachment{}, fmt.Errorf("%s is larger than %d MB", path, teamMaxAttachment>>20)
	}
	data, err := os.ReadFile(path)
	return teamAttachment{Name: filepath.Base(path), Data: data}, err
}
