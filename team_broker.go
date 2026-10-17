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
	"text/tabwriter"
	"time"
)

// The broker is the lead's only way to put members to work. hi team
// __serve listens on a socket in the team's run/ folder, which the lead's
// box gets at /team/run; the lead runs hi team task, review, wait, and
// merge there. The broker checks each request against team.json, then
// runs the member as hi agent on the host, in a box of its own on a branch
// of repo/. The lead never holds the agents' or the repository's
// credentials, and nothing it asks for widens a member's box.
//
// A task's work is reviewed without the lead: when a member finishes, the
// broker has the reviewer check it, and gives the coder the reviewer's
// notes on the same branch, up to teamReviewRounds reviews. The lead waits
// on the task it started and hears the outcome once.

const (
	teamSocketName = "broker.sock"
	teamSocketEnv  = "HI_TEAM_SOCKET"
	// teamMaxBrief is the largest brief the broker takes.
	teamMaxBrief = 256 << 10
	// teamReviewRounds is how many reviews a task's work gets before the
	// broker hands it back to the lead with changes still needed.
	teamReviewRounds = 3
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
	Hints    []string  `json:"hints,omitempty"`
	Created  time.Time `json:"created"`
	Finished time.Time `json:"finished,omitempty"`
}

// teamSpecsRule ends every brief: the specs are the lead's, and a merge
// leaves out a member's changes to them.
const teamSpecsRule = "docs/specs/ is the lead's: read it, but don't change it, not even a feature's status; the lead updates it."

var (
	teamTaskID     = regexp.MustCompile(`^t-[0-9]{1,6}$`)
	teamVersionArg = regexp.MustCompile(`^v[1-9][0-9]{0,5}$`)
)

func teamTaskDir(team string) string { return filepath.Join(teamDir(team), "tasks") }

func loadTeamTask(team, id string) (teamTask, error) {
	var task teamTask
	if !teamTaskID.MatchString(id) {
		return task, fmt.Errorf("a task is named like t-3, not %q", id)
	}
	data, err := os.ReadFile(filepath.Join(teamTaskDir(team), id+".json"))
	if errors.Is(err, os.ErrNotExist) {
		return task, fmt.Errorf("no task %s", id)
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

func printTeamTasks(tasks []teamTask, last int, w io.Writer) {
	printTeamIdeas(tasks, w)
	if len(tasks) > last {
		tasks = tasks[len(tasks)-last:]
	}
	table := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(table, "TASK\tROLE\tSTATUS\tSTARTED\tTITLE")
	for _, task := range tasks {
		status := task.Status
		if task.Verdict != "" {
			status += " (" + task.Verdict + ")"
		}
		fmt.Fprintf(table, "%s\t%s\t%s\t%s\t%s\n", task.ID, task.Role, status, task.Created.Local().Format("Jan 2 15:04"), qClip(task.Title))
	}
	table.Flush()
}

// printTeamIdeas lists the ideas not merged yet, each as the line of tasks
// it grew from, so the lead sees which work a follow-up belongs to.
func printTeamIdeas(tasks []teamTask, w io.Writer) {
	ideas := teamOpenIdeas(tasks)
	if len(ideas) == 0 {
		return
	}
	fmt.Fprintf(w, "Not merged yet (%d of %d ideas); continue one with --on its latest task:\n", len(ideas), teamMaxIdeas)
	for _, idea := range ideas {
		var line []string
		chain := teamChain(tasks, idea.ID)
		if first := chain[0]; first.On != "" {
			// The line goes on from merged work.
			line = append(line, first.On+" (merged)")
		}
		for _, task := range chain {
			line = append(line, task.ID)
		}
		status := idea.Status
		for _, task := range tasks {
			if task.Kind == "review" && task.Review == idea.ID && task.Verdict != "" {
				status = idea.Status + ", review: " + task.Verdict
			}
		}
		if root, err := teamFindTask(tasks, teamChainRoot(tasks, idea.ID)); err == nil && root.Approval != nil {
			status += "; asked by " + root.Approval.Name
		}
		fmt.Fprintf(w, "  %s (%s) %s\n", idea.ID, status, qClip(idea.Title))
		if len(line) > 1 {
			fmt.Fprintf(w, "      %s\n", strings.Join(line, " > "))
		}
	}
	fmt.Fprintln(w)
}

// teamFindTask is a task among some, by ID.
func teamFindTask(tasks []teamTask, id string) (teamTask, error) {
	for _, task := range tasks {
		if task.ID == id {
			return task, nil
		}
	}
	return teamTask{}, fmt.Errorf("no task %s", id)
}

// ---------------------------------------------------------------------------
// the broker, on the host

type teamBroker struct {
	team string
	repo string
	hi   string // this hi binary
	mu   sync.Mutex
	// previewMu keeps one preview start or stop at a time.
	previewMu sync.Mutex
	// app starts the project's box; tests replace it.
	app func(name, src, data, address, command string, port, hostPort int) error
	// exposed finds a running hi net expose by name, and running says an
	// app's box runs; tests replace them.
	exposed func(name string) (netExposeState, bool)
	running func(container string) bool
	// channel is the team's Slack channel, to check approvals that aren't
	// logged yet and to say when a small fix starts; nil in tests.
	channel teamChannelSource
	// run starts hi with arguments in repo/ and returns its stdout; tests
	// replace it.
	run func(args ...string) ([]byte, error)
	// reviewRounds is how many reviews the broker starts on its own for a
	// task's work; 0 leaves reviews to the lead, as most tests do.
	reviewRounds int
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
	// peopleMu guards people.json, the keys of people signed in with /hi
	// (team_people.go).
	peopleMu sync.Mutex
	// help answers a question in the app from its guide, with no tools;
	// tests replace it. counts are the day's questions and feedback, for
	// the limits (team_feedback.go).
	help   func(ctx context.Context, team string, messages []teamHelpTurn, delta func(string)) (string, error)
	counts teamWebCounts
}

func newTeamBroker(team, hi string) *teamBroker {
	b := &teamBroker{team: team, repo: filepath.Join(teamDir(team), "repo"), hi: hi, reviewRounds: teamReviewRounds, answering: map[string]bool{}, webListeners: map[string]map[chan teamWebMessage]bool{}}
	b.help = teamHelpModel
	b.lead = func(context.Context, string, string, []teamLeadPicture, func(string)) (string, error) {
		return "", errors.New("the lead's API server isn't set up")
	}
	b.app = b.startApp
	b.takeSnapshot = func(kind, label string) (teamSnapshot, error) {
		return teamTakeSnapshot(team, kind, label, teamPause)
	}
	b.restoreData = func(id string) error { return teamRestoreData(team, id) }
	b.exposed = teamExposed
	b.running = b.appRunning
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
		if _, err := loadBoxMeta(task.Box); err != nil {
			task.Status, task.Error, task.Finished = "failed", "hi team stopped before the task's box started", time.Now().UTC()
			saveTeamTask(b.team, task)
			continue
		}
		go b.wait(task)
	}
}

type teamRequest struct {
	Role  string `json:"role,omitempty"`
	Brief string `json:"brief,omitempty"`
	Task  string `json:"task,omitempty"`
	On    string `json:"on,omitempty"`
	Notes string `json:"notes,omitempty"`
	// Approval is the people's message that approved new work; Small
	// starts a small fix without one.
	Approval string `json:"approval,omitempty"`
	Small    bool   `json:"small,omitempty"`
	// In is the message a small fix was asked for in.
	In string `json:"in,omitempty"`
	// New starts new work even though its yes is in the thread of work
	// that isn't merged yet.
	New bool `json:"new,omitempty"`
	// Attachments are files the lead sends with a task, from its box.
	Attachments []teamAttachment `json:"attachments,omitempty"`
	// hints go to the new task's Hints.
	hints []string
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
	read := func(r *http.Request) (teamRequest, error) {
		var request teamRequest
		// Attached files come base64-encoded, a third larger.
		err := json.NewDecoder(io.LimitReader(r.Body, teamMaxBrief+teamMaxAttachments*4/3+64<<10)).Decode(&request)
		return request, err
	}
	mux.HandleFunc("GET /env", func(w http.ResponseWriter, r *http.Request) {
		secrets, err := loadTeamSecrets(b.team)
		reply(w, teamEnvNames(secrets), err)
	})
	mux.HandleFunc("POST /task", func(w http.ResponseWriter, r *http.Request) {
		request, err := read(r)
		if err != nil {
			reply(w, nil, err)
			return
		}
		task, err := b.startTask(request)
		reply(w, task, err)
	})
	mux.HandleFunc("POST /review", func(w http.ResponseWriter, r *http.Request) {
		request, err := read(r)
		if err != nil {
			reply(w, nil, err)
			return
		}
		task, err := b.startReview(request)
		reply(w, task, err)
	})
	mux.HandleFunc("POST /merge", func(w http.ResponseWriter, r *http.Request) {
		request, err := read(r)
		if err != nil {
			reply(w, nil, err)
			return
		}
		task, err := b.merge(request.Task, request.Approval)
		reply(w, task, err)
	})
	mux.HandleFunc("POST /stop", func(w http.ResponseWriter, r *http.Request) {
		request, err := read(r)
		if err != nil {
			reply(w, nil, err)
			return
		}
		task, err := b.stop(request.Task)
		reply(w, task, err)
	})
	mux.HandleFunc("POST /preview", func(w http.ResponseWriter, r *http.Request) {
		var request teamPreviewRequest
		if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&request); err != nil {
			reply(w, nil, err)
			return
		}
		preview, err := b.preview(request)
		reply(w, preview, err)
	})
	mux.HandleFunc("POST /release", func(w http.ResponseWriter, r *http.Request) {
		var request teamReleaseRequest
		if err := json.NewDecoder(io.LimitReader(r.Body, 16<<10)).Decode(&request); err != nil {
			reply(w, nil, err)
			return
		}
		request.Data = r.URL.Query().Get("data") == "1"
		result, err := b.release(request)
		reply(w, result, err)
	})
	mux.HandleFunc("POST /discard", func(w http.ResponseWriter, r *http.Request) {
		request, err := read(r)
		if err != nil {
			reply(w, nil, err)
			return
		}
		var tasks []teamTask
		if request.Task == "dev" {
			tasks, err = b.resetDev(request.Approval)
		} else {
			tasks, err = b.discard(request.Task)
		}
		reply(w, tasks, err)
	})
	mux.HandleFunc("GET /helper", func(w http.ResponseWriter, r *http.Request) {
		config, err := loadTeam(b.team)
		reply(w, teamHelperLine(config), err)
	})
	mux.HandleFunc("POST /helper", func(w http.ResponseWriter, r *http.Request) {
		var request teamHelperRequest
		if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&request); err != nil {
			reply(w, nil, err)
			return
		}
		config, err := b.setHelper(request)
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
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&request); err != nil {
			reply(w, nil, err)
			return
		}
		line, err := b.webLink(request.User)
		reply(w, line, err)
	})
	mux.HandleFunc("POST /web/post", func(w http.ResponseWriter, r *http.Request) {
		var post teamWebPost
		if err := json.NewDecoder(io.LimitReader(r.Body, teamWebMaxUpload*4/3+(1<<20))).Decode(&post); err != nil {
			reply(w, nil, err)
			return
		}
		line, err := b.webPost(post)
		reply(w, line, err)
	})
	mux.HandleFunc("GET /versions", func(w http.ResponseWriter, r *http.Request) {
		view, err := teamVersionsReport(b.team)
		reply(w, view, err)
	})
	mux.HandleFunc("GET /tasks", func(w http.ResponseWriter, r *http.Request) {
		reply(w, loadTeamTasks(b.team), nil)
	})
	mux.HandleFunc("GET /task", func(w http.ResponseWriter, r *http.Request) {
		id := r.URL.Query().Get("id")
		wait, _ := time.ParseDuration(r.URL.Query().Get("wait"))
		deadline := time.Now().Add(min(wait, 30*time.Minute))
		for {
			steps, err := teamSteps(b.team, id)
			if err != nil || !teamTaskActive(steps[len(steps)-1]) || time.Now().After(deadline) {
				var view teamTaskView
				if err == nil {
					view.teamTask = steps[0]
					if len(steps) > 1 {
						view.Steps = steps
					}
				}
				reply(w, view, err)
				return
			}
			select {
			case <-r.Context().Done():
				return
			case <-time.After(2 * time.Second):
			}
		}
	})
	return mux
}

func teamTaskActive(task teamTask) bool { return task.Status == "starting" || task.Status == "running" }

// teamTaskView is what the lead gets about a task: the task, and when the
// broker had its work reviewed, every step since, the task first.
type teamTaskView struct {
	teamTask
	Steps []teamTask `json:"steps,omitempty"`
}

// teamSteps is a task and the reviews and fixes the broker started after
// it, in order.
func teamSteps(team, id string) ([]teamTask, error) {
	task, err := loadTeamTask(team, id)
	if err != nil {
		return nil, err
	}
	steps := []teamTask{task}
	for task.Next != "" && len(steps) <= 2*teamReviewRounds+1 {
		if task, err = loadTeamTask(team, task.Next); err != nil {
			break
		}
		steps = append(steps, task)
	}
	return steps, nil
}

// teamLatestWork is the latest work of a task's review loop: the last fix
// the broker started for it, or the task itself. busy is the step still
// working, if one is.
func teamLatestWork(team, id string) (teamTask, *teamTask, error) {
	steps, err := teamSteps(team, id)
	if err != nil {
		return teamTask{}, nil, err
	}
	work := steps[0]
	var busy *teamTask
	for i, step := range steps {
		if step.Kind == "task" {
			work = step
		}
		if teamTaskActive(step) && busy == nil {
			busy = &steps[i]
		}
	}
	return work, busy, nil
}

// newTask numbers a task and saves it before its box starts.
func (b *teamBroker) newTask(task teamTask) (teamTask, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	tasks := loadTeamTasks(b.team)
	n := 1
	if len(tasks) > 0 {
		n = teamTaskNumber(tasks[len(tasks)-1].ID) + 1
	}
	task.ID = fmt.Sprintf("t-%d", n)
	task.Box = b.team + "-" + task.ID
	task.Status, task.Created = "starting", time.Now().UTC()
	return task, saveTeamTask(b.team, task)
}

func (b *teamBroker) member(role string) (teamMember, error) {
	config, err := loadTeam(b.team)
	if err != nil {
		return teamMember{}, err
	}
	if role == "lead" {
		return teamMember{}, errors.New("the lead doesn't hand tasks to itself")
	}
	member, ok := config.Members[role]
	if !ok {
		return teamMember{}, fmt.Errorf("the team has no %s; its members are %s", role, strings.Join(teamRoles(config)[1:], ", "))
	}
	return member, nil
}

func (b *teamBroker) startTask(request teamRequest) (teamTask, error) {
	if request.On != "" {
		on, hints, err := b.continuing(request.On)
		if err != nil {
			return teamTask{}, err
		}
		request.On, request.hints = on, hints
	}
	task, start, err := b.newWork(request, 0)
	if err != nil {
		return task, err
	}
	if task.On != "" {
		b.announceContinuation(task)
	}
	go start()
	return task, nil
}

// continuing is the task that work continuing id goes on from: id, or the
// latest work of its idea, so nothing done since is left out. It refuses
// what can't be continued, saying what to do instead, and while any of
// the idea's work is still going: one piece of work per idea at a time.
func (b *teamBroker) continuing(id string) (string, []string, error) {
	on, err := loadTeamTask(b.team, id)
	if err != nil {
		return "", nil, err
	}
	switch {
	case on.Kind == "review":
		return "", nil, fmt.Errorf("%s is a review; continue the work it reviewed: --on %s", id, on.Review)
	case on.Status == "merged":
		return "", nil, fmt.Errorf("%s is merged already: its code is in dev, where new work starts. Start a new task without --on; it needs a yes, as new work does", id)
	case on.Status == "discarded":
		return "", nil, fmt.Errorf("%s was discarded. To do it after all, start a new task, with a yes; hi team show %s has its brief", id, id)
	}
	tasks := loadTeamTasks(b.team)
	if busy := teamIdeaBusy(tasks, id); busy != nil {
		waitFor := busy.ID
		if busy.Kind == "review" {
			waitFor = busy.Review
		}
		if busy.Kind == "review" || busy.Round > 0 {
			return "", nil, fmt.Errorf("%s's work is still in review (%s is working on it); hi team wait %s waits for the outcome", id, busy.ID, waitFor)
		}
		return "", nil, fmt.Errorf("%s's work is still going (%s is working on it); hi team wait %s waits for it, then continue with --on %s", id, busy.ID, waitFor, busy.ID)
	}
	latest := teamIdeaLatest(tasks, id)
	switch len(latest) {
	case 0:
		return id, nil, nil
	case 1:
	default:
		var lines []string
		for _, task := range latest {
			lines = append(lines, fmt.Sprintf("%s (%s) %s", task.ID, task.Status, task.Title))
		}
		return "", nil, fmt.Errorf("the work %s belongs to has split into %d lines, none merged:\n  %s\nContinue the one to keep with --on, and drop the other with hi team discard, so no work is left behind",
			id, len(latest), strings.Join(lines, "\n  "))
	}
	work := latest[0]
	var hints []string
	if work.ID != id {
		hints = append(hints, fmt.Sprintf("%s was continued since, so this continues %s (%s), with all the work so far.", id, work.ID, work.Title))
	}
	if work.Status == "failed" || work.Status == "stopped" {
		hints = append(hints, fmt.Sprintf("%s %s before it finished; this continues from what it committed.", work.ID, work.Status))
	}
	return work.ID, hints, nil
}

// announceContinuation says in the thread the work was agreed in what the
// lead added to it, so the people see what goes on from their yes.
func (b *teamBroker) announceContinuation(task teamTask) {
	if b.channel == nil {
		return
	}
	root, err := loadTeamTask(b.team, teamChainRoot(loadTeamTasks(b.team), task.On))
	if err != nil || root.Approval == nil {
		return
	}
	thread := teamApprovalSlackThread(*root.Approval)
	config, err := loadTeam(b.team)
	if thread == "" || err != nil {
		return
	}
	b.channel.postIn(config.Channel, thread, fmt.Sprintf("More on this (%s): %s", task.ID, teamSlackEscape(task.Title)))
}

// newWork saves a member's task, and returns it with what starts its box.
func (b *teamBroker) newWork(request teamRequest, round int) (teamTask, func(), error) {
	member, err := b.member(request.Role)
	if err != nil {
		return teamTask{}, nil, err
	}
	brief := strings.TrimSpace(request.Brief)
	if brief == "" {
		return teamTask{}, nil, errors.New("the brief is empty")
	}
	if len(brief) > teamMaxBrief {
		return teamTask{}, nil, fmt.Errorf("the brief is longer than %d KB", teamMaxBrief>>10)
	}
	task := teamTask{Role: request.Role, Agent: member.Agent, Model: member.Model, Kind: "task", Title: teamTitle(brief), Round: round}
	from := ""
	if request.On != "" {
		on, err := loadTeamTask(b.team, request.On)
		if err != nil {
			return teamTask{}, nil, err
		}
		if on.Kind != "task" || on.Branch == "" || teamTaskActive(on) || on.Status == "discarded" || on.Status == "merged" {
			return teamTask{}, nil, fmt.Errorf("%s isn't a finished task with a branch to continue", on.ID)
		}
		task.On, from = on.ID, on.Branch
	} else if request.Small {
		task.Small = true
		if request.In != "" {
			config, err := loadTeam(b.team)
			if err != nil {
				return teamTask{}, nil, err
			}
			if task.Thread, err = b.teamFindThread(config, request.In); err != nil {
				return teamTask{}, nil, err
			}
		}
	} else {
		config, err := loadTeam(b.team)
		if err != nil {
			return teamTask{}, nil, err
		}
		approval, err := b.teamFindApproval(config, request.Approval, "task")
		if err != nil {
			return teamTask{}, nil, err
		}
		if idea := teamIdeaInThread(loadTeamTasks(b.team), approval); idea != nil && !request.New {
			return teamTask{}, nil, fmt.Errorf("this yes is in the thread of %s (%s), which isn't merged yet. If it's more of that work, continue it: hi team task %s <brief> --on %s, with no new yes. If it really is separate work, add --new",
				idea.ID, idea.Title, idea.Role, idea.ID)
		}
		task.Approval = &approval
	}
	task.Hints = request.hints
	if task.On == "" {
		if ideas := teamOpenIdeas(loadTeamTasks(b.team)); len(ideas) > 0 {
			var lines []string
			for _, idea := range ideas {
				lines = append(lines, fmt.Sprintf("%s (%s) %s", idea.ID, idea.Status, idea.Title))
			}
			task.Hints = append(task.Hints, "Not merged yet: "+strings.Join(lines, "; ")+". If this is more of one of them, stop it and continue that one with --on instead.")
		}
	}
	if err := b.roomForIdea(task.On); err != nil {
		return teamTask{}, nil, err
	}
	// The member's branch starts from main: with the latest specs.
	if err := b.commitSpecs(); err != nil {
		return teamTask{}, nil, fmt.Errorf("committing the specs: %w", err)
	}
	if task, err = b.newTask(task); err != nil {
		return task, nil, err
	}
	// A heading first, so the brief can't carry front matter that would
	// widen the box.
	text := fmt.Sprintf("# Task %s for the %s of the %s team\n\n%s\n", task.ID, task.Role, b.team, brief)
	text += "\n" + teamSpecsRule + "\n\n" + teamMapRule + "\n"
	if task.On != "" {
		text += fmt.Sprintf("\nYou continue task %s: its commits are already on your branch.\n", task.On)
		if config, err := loadTeam(b.team); err == nil {
			text += teamSpecsMoved(config.Branch)
			if moved := teamMovedFiles(b.repo, config.Branch, from); len(moved) > 0 {
				text += teamCodeMoved(config.Branch, moved)
			}
		}
	}
	files, extra, err := b.teamTaskFiles(task, request.Attachments)
	if err != nil {
		b.fail(task, err)
		return task, nil, fmt.Errorf("the task's files: %w", err)
	}
	text += extra + teamRoleBrief(b.team, task.Role)
	if files != "" {
		task.Files = files
		saveTeamTask(b.team, task)
	}
	if task.Small && b.channel != nil {
		if config, err := loadTeam(b.team); err == nil {
			text := fmt.Sprintf("Small fix started without waiting for a go-ahead (%s): %s", task.ID, task.Title)
			if task.Thread != "" {
				b.channel.postIn(config.Channel, task.Thread, text)
			} else if ts, err := b.channel.post(config.Channel, text); err == nil {
				// Its progress card goes in this message's thread.
				task.Thread = ts
				saveTeamTask(b.team, task)
			}
		}
	}
	if task.Approval != nil && strings.HasPrefix(task.Approval.TS, "dm:") && b.channel != nil {
		// Work agreed in a DM is the team's business too.
		if config, err := loadTeam(b.team); err == nil {
			b.channel.post(config.Channel, fmt.Sprintf(":speech_balloon: %s asked for this in a DM with the lead: *%s* (%s)",
				teamSlackEscape(task.Approval.Name), teamSlackEscape(task.Title), task.ID))
		}
	}
	return task, func() { b.launch(task, member, text, from, files) }, nil
}

// roomForIdea refuses a task that would start one idea too many. Continuing
// the latest task of an idea (--on) adds none.
func (b *teamBroker) roomForIdea(on string) error {
	ideas := teamOpenIdeas(loadTeamTasks(b.team))
	var names []string
	for _, idea := range ideas {
		if idea.ID == on {
			return nil
		}
		names = append(names, idea.ID+" ("+idea.Status+") "+idea.Title)
	}
	if len(ideas) < teamMaxIdeas {
		return nil
	}
	return fmt.Errorf("%d ideas aren't merged yet, the most there may be:\n  %s\nMerge one, or ask the people which to drop and hi team discard it, before starting another",
		len(ideas), strings.Join(names, "\n  "))
}

func (b *teamBroker) startReview(request teamRequest) (teamTask, error) {
	reviewed, err := loadTeamTask(b.team, request.Task)
	if err != nil {
		return teamTask{}, err
	}
	if reviewed.Kind != "task" || reviewed.Status != "done" || reviewed.Branch == "" {
		return teamTask{}, fmt.Errorf("%s isn't a finished task with work to review (%s)", reviewed.ID, reviewed.Status)
	}
	task, start, err := b.newReview(reviewed, request.Notes, 0)
	if err != nil {
		return task, err
	}
	go start()
	return task, nil
}

// newReview saves a review of a finished task's latest commit, and returns
// it with what starts the reviewer's box.
func (b *teamBroker) newReview(reviewed teamTask, notes string, round int) (teamTask, func(), error) {
	member, err := b.member("reviewer")
	if err != nil {
		return teamTask{}, nil, err
	}
	config, err := loadTeam(b.team)
	if err != nil {
		return teamTask{}, nil, err
	}
	commit := boxGit(b.repo, "rev-parse", reviewed.Branch)
	base := boxGit(b.repo, "merge-base", config.Branch, reviewed.Branch)
	if commit == "" || base == "" {
		return teamTask{}, nil, fmt.Errorf("%s's branch %s is gone", reviewed.ID, reviewed.Branch)
	}
	if commit == base {
		return teamTask{}, nil, fmt.Errorf("%s committed nothing to review", reviewed.ID)
	}
	task := teamTask{Role: "reviewer", Agent: member.Agent, Model: member.Model, Kind: "review", Review: reviewed.ID, Commit: commit,
		Title: "Review " + reviewed.ID + ": " + reviewed.Title, Round: round}
	if task, err = b.newTask(task); err != nil {
		return task, nil, err
	}
	if reviewed.Files != "" && fileExists(reviewed.Files) {
		task.Files = reviewed.Files
		saveTeamTask(b.team, task)
	}
	brief := teamReviewBrief(b.team, config.Branch, task, reviewed, base, b.brief(reviewed), notes)
	if packages := teamDependencyChanges(b.repo, base, commit); len(packages) > 0 {
		brief += teamReviewPackages(packages)
	}
	brief += teamRoleBrief(b.team, "reviewer")
	return task, func() { b.launch(task, member, brief, reviewed.Branch, task.Files) }, nil
}

// brief is the text a task was given.
func (b *teamBroker) brief(task teamTask) string {
	data, _ := os.ReadFile(filepath.Join(teamTaskDir(b.team), task.ID+".md"))
	return strings.TrimSpace(string(data))
}

// teamSpecsMoved tells a member continuing a branch that the lead's specs
// may have changed on the team's branch since it started.
func teamSpecsMoved(branch string) string {
	return fmt.Sprintf("Your branch started from %[1]s, and the lead may have changed docs/specs/ there since. If git diff HEAD %[1]s -- docs/specs/ shows changes, run git merge %[1]s first, so you work to the current specs.\n", branch)
}

// teamMovedFiles are the files the team's branch changed since work began
// on another branch that the work changed too, specs aside: where a merge
// may conflict.
func teamMovedFiles(repo, branch, work string) []string {
	base := boxGit(repo, "merge-base", branch, work)
	if base == "" {
		return nil
	}
	mine := map[string]bool{}
	for _, name := range strings.Split(boxGit(repo, "diff", "--name-only", base, work), "\n") {
		mine[name] = true
	}
	var both []string
	for _, name := range strings.Split(boxGit(repo, "diff", "--name-only", base, branch), "\n") {
		if name != "" && mine[name] && !strings.HasPrefix(name, teamSpecsPath+"/") {
			both = append(both, name)
		}
	}
	return both
}

// teamCodeMoved tells a member continuing a branch that the team's branch
// changed the same files since.
func teamCodeMoved(branch string, files []string) string {
	return fmt.Sprintf("%[1]s has changed since this work began, in files it changes too: %[2]s. Run git merge %[1]s before anything else and resolve any conflicts, so you build on the current code, and commit the merge.\n",
		branch, strings.Join(files, ", "))
}

func teamReviewBrief(team, branch string, task, reviewed teamTask, base, brief, notes string) string {
	var text strings.Builder
	fmt.Fprintf(&text, "# Review %s for the %s team\n\n", task.ID, team)
	fmt.Fprintf(&text, "You are the team's reviewer. The %s did task %s on this branch. Review its work before the lead merges it.\n\n", reviewed.Role, reviewed.ID)
	fmt.Fprintf(&text, "The work is the commits since %s:\n\n    git log --oneline %s..HEAD\n    git diff %s...HEAD\n\n", base[:min(len(base), 12)], base, base)
	fmt.Fprintf(&text, "The current specs are the ones on %[1]s: this branch's docs/specs/ may be older, from when the work started. Read them with git show %[1]s:docs/specs/<file>, and see what changed with git diff HEAD %[1]s -- docs/specs/. Judge the work against those, not against an older copy on the branch.\n\n", branch)
	text.WriteString("Check that:\n\n")
	text.WriteString("- it does what the brief asks, and nothing beyond it or beyond what the people approved (their words are in the brief below, or in how the work started): work they didn't ask for is a change needed, \"outside what was approved\", however useful;\n")
	text.WriteString("- it has tests for what the brief says must work, and the tests pass when you run them;\n")
	text.WriteString("- it doesn't break what worked before, leak secrets, or leave debugging behind;\n")
	text.WriteString("- the README still says how to run and test the project.\n\n")
	text.WriteString("Don't fix anything yourself and don't commit: say what must change.\n\n")
	text.WriteString("In your report, start the summary with \"Approve:\" if it can be merged as it is, or with \"Changes needed:\" and put each change in follow_ups.\n\n")
	if notes = strings.TrimSpace(notes); notes != "" {
		fmt.Fprintf(&text, "## The lead's notes\n\n%s\n\n", notes)
	}
	fmt.Fprintf(&text, "## The brief for %s\n\n%s\n", reviewed.ID, brief)
	if reviewed.Report != nil && len(reviewed.Report.Report) > 0 && string(reviewed.Report.Report) != "null" {
		fmt.Fprintf(&text, "\n## The %s's report\n\n%s\n", reviewed.Role, string(reviewed.Report.Report))
	}
	return text.String()
}

// teamReviewPackages asks the reviewer to check the packages a task
// changes.
func teamReviewPackages(packages []teamDependencyProject) string {
	var text strings.Builder
	fmt.Fprintf(&text, "\n## Packages\n\nThis work changes the project's packages:\n\n%s\n\n", teamDependencyText(packages))
	text.WriteString("For each package it adds, check that the work needs it, that it is widely used and maintained, and that its license allows this use; a smaller package, or a few lines of code, is often better. ")
	text.WriteString("Run the install (bun install --frozen-lockfile, or npm ci for a package-lock.json) and the audit (bun audit, or npm audit): a known vulnerability is a change needed. ")
	text.WriteString("A name new in trustedDependencies runs its own install script on every machine that installs the project: approve it only if the work can't do without it, and say why in your summary. The merge also needs an owner's yes for it.\n")
	return text.String()
}

// launch starts the member's box with hi agent, then waits for it.
func (b *teamBroker) launch(task teamTask, member teamMember, brief, from, files string) {
	path := filepath.Join(teamTaskDir(b.team), task.ID+".md")
	if err := os.WriteFile(path, []byte(brief), 0o600); err != nil {
		b.fail(task, err)
		return
	}
	args := []string{"agent", task.Agent, "--detach", "--json", "--name", task.Box, "--task-file", path}
	if member.Model != "" {
		args = append(args, "--model", member.Model)
	}
	if member.Bundle != "" {
		args = append(args, "--bundle", member.Bundle)
	}
	// Members reach any site unless team.json narrows it; the box is the
	// boundary.
	args = append(args, "--network", firstNonEmpty(member.Network, "open"))
	for _, host := range member.Allow {
		args = append(args, "--allow", host)
	}
	if from != "" {
		args = append(args, "--from", from)
	}
	if files != "" {
		args = append(args, "--files", files)
	}
	out, err := b.run(args...)
	if err != nil {
		b.fail(task, err)
		return
	}
	var started agentReport
	if err := json.Unmarshal(out, &started); err != nil {
		b.fail(task, fmt.Errorf("hi agent: %w", err))
		return
	}
	task.Status, task.Branch = "running", started.Branch
	saveTeamTask(b.team, task)
	b.wait(task)
}

// wait collects a running task's report when its box stops.
func (b *teamBroker) wait(task teamTask) {
	out, err := b.run("agent", "wait", task.Box, "--json")
	var report agentReport
	if jsonErr := json.Unmarshal(out, &report); jsonErr != nil {
		if err == nil {
			err = jsonErr
		}
		b.fail(task, err)
		return
	}
	current, loadErr := loadTeamTask(b.team, task.ID)
	if loadErr == nil && current.Status == "stopped" {
		task.Status = "stopped"
	} else if report.Status == "failed" {
		task.Status = "failed"
	} else {
		task.Status = "done"
	}
	task.Report, task.Finished = &report, time.Now().UTC()
	if task.Kind == "review" {
		task.Verdict = teamVerdict(report)
	}
	// The next step is saved before this one shows as finished, so the lead
	// waiting on the loop never sees it end early.
	next, start := b.nextStep(task)
	task.Next = next
	saveTeamTask(b.team, task)
	if start != nil {
		start()
	}
}

// nextStep starts the review loop's next step after a finished one: a
// review of a member's work, or the coder's fix of what a review asked
// for. It returns the step and what starts its box, or nothing when the
// loop ends there and the lead takes over.
func (b *teamBroker) nextStep(task teamTask) (string, func()) {
	if task.Status != "done" || b.reviewRounds == 0 {
		return "", nil
	}
	if task.Kind == "task" {
		if task.Role == "reviewer" || task.Branch == "" {
			return "", nil
		}
		// A member that committed nothing, or a team without a reviewer,
		// has nothing to review.
		review, start, err := b.newReview(task, "", task.Round+1)
		if err != nil {
			return "", nil
		}
		return review.ID, start
	}
	reviewed, err := loadTeamTask(b.team, task.Review)
	if err != nil || reviewed.Next != task.ID || reviewed.Status != "done" || task.Verdict == "approve" || task.Round >= b.reviewRounds {
		return "", nil
	}
	fix, start, err := b.newWork(teamRequest{Role: reviewed.Role, Brief: teamFixBrief(task, reviewed, b.reviewRounds), On: reviewed.ID}, task.Round)
	if err != nil {
		return "", nil
	}
	return fix.ID, start
}

// teamFixBrief gives the coder a review's notes on its work.
func teamFixBrief(review, reviewed teamTask, rounds int) string {
	var block struct {
		Summary   string   `json:"summary"`
		FollowUps []string `json:"follow_ups"`
	}
	if review.Report != nil {
		json.Unmarshal(review.Report.Report, &block)
	}
	var text strings.Builder
	fmt.Fprintf(&text, "Fix what review %s found in %s\n\n", review.ID, reviewed.ID)
	fmt.Fprintf(&text, "The reviewer checked your work on this branch (review %d of %d) and asks for changes before it is merged. Make them, with tests where they apply, and commit. Change nothing else; if you disagree with a note, say why in your report.\n\n", review.Round, rounds)
	fmt.Fprintf(&text, "## The reviewer's summary\n\n%s\n", firstNonEmpty(strings.TrimSpace(block.Summary), "(none)"))
	if len(block.FollowUps) > 0 {
		text.WriteString("\n## What must change\n\n")
		for _, change := range block.FollowUps {
			fmt.Fprintf(&text, "- %s\n", strings.TrimSpace(change))
		}
	} else if review.Report != nil {
		if note := strings.TrimSpace(agentReportBlock.ReplaceAllString(review.Report.Text, "")); note != "" {
			fmt.Fprintf(&text, "\n## The reviewer's notes\n\n%s\n", teamClipText(note, teamMaxConversation))
		}
	}
	return text.String()
}

func (b *teamBroker) fail(task teamTask, err error) {
	task.Status, task.Error, task.Finished = "failed", err.Error(), time.Now().UTC()
	saveTeamTask(b.team, task)
}

// teamVerdict reads a review's report: approve only when the reviewer
// finished and said so.
func teamVerdict(report agentReport) string {
	var block struct {
		Status  string `json:"status"`
		Summary string `json:"summary"`
	}
	json.Unmarshal(report.Report, &block)
	if report.Status == "done" && block.Status == "complete" && strings.HasPrefix(strings.ToLower(strings.TrimSpace(block.Summary)), "approve") {
		return "approve"
	}
	return "changes"
}

func (b *teamBroker) stop(id string) (teamTask, error) {
	// Stopping a task stops whichever step of its review loop is working.
	task, busy, err := teamLatestWork(b.team, id)
	if err != nil {
		return task, err
	}
	if busy == nil {
		return task, fmt.Errorf("%s isn't running (%s)", id, task.Status)
	}
	task = *busy
	task.Status = "stopped"
	if err := saveTeamTask(b.team, task); err != nil {
		return task, err
	}
	if _, err := b.run("box", "stop", task.Box); err != nil {
		return task, err
	}
	return task, nil
}

// merge merges a task's branch into the team's branch, once a review of
// its latest commit approved it. A task the broker had fixed merges as its
// latest fix.
func (b *teamBroker) merge(id, approvalRef string) (teamTask, error) {
	task, err := b.mergeWork(id, approvalRef)
	var conflict *teamMergeConflict
	if errors.As(err, &conflict) {
		return task, b.catchUp(task, conflict)
	}
	return task, err
}

// teamMergeConflict is work that doesn't merge into the team's branch:
// the branch changed the same lines since.
type teamMergeConflict struct {
	branch string
	files  []string
	out    string
}

func (c *teamMergeConflict) Error() string { return "the merge conflicts: " + c.out }

// catchUp redirects a merge that conflicts: the member who did the work
// merges the team's branch into it and resolves the conflicts, as a
// continuation the broker starts and has reviewed like any other. The
// lead merges that once it's approved.
func (b *teamBroker) catchUp(task teamTask, conflict *teamMergeConflict) error {
	where := ""
	if len(conflict.files) > 0 {
		where = " in " + strings.Join(conflict.files, ", ")
	}
	brief := fmt.Sprintf("Bring %[1]s up to date with %[2]s\n\n%[2]s changed since this work began, and the two no longer merge: they change the same lines%[3]s. Run git merge %[2]s, resolve each conflict so that both what %[2]s has now and what this work adds keep working, run the tests, and commit the merge. Change nothing else.\n",
		task.ID, conflict.branch, where)
	fix, start, err := b.newWork(teamRequest{Role: task.Role, Brief: brief, On: task.ID}, 0)
	if err != nil {
		return fmt.Errorf("%s doesn't merge cleanly into %s (it conflicts%s), and hi couldn't start the work to bring it up to date: %v. Give the %s a task on it with --on %s", task.ID, conflict.branch, where, err, task.Role, task.ID)
	}
	go start()
	return fmt.Errorf("%s doesn't merge cleanly into %s: both changed the same lines%s. hi started %s: the %s merges %s into the work and resolves them, and the reviewer checks it as usual. hi team wait %s, then hi team merge %s",
		task.ID, conflict.branch, where, fix.ID, task.Role, conflict.branch, fix.ID, fix.ID)
}

// mergeWork merges a task's latest reviewed work into the team's branch.
func (b *teamBroker) mergeWork(id, approvalRef string) (teamTask, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	task, busy, err := teamLatestWork(b.team, id)
	if err != nil {
		return task, err
	}
	if busy != nil {
		return task, fmt.Errorf("%s's work is still in review (%s is working on it); hi team wait %s waits for the outcome", id, busy.ID, id)
	}
	if task.Kind != "task" || task.Status != "done" || task.Branch == "" {
		return task, fmt.Errorf("%s isn't a finished task with a branch (%s)", task.ID, task.Status)
	}
	config, err := loadTeam(b.team)
	if err != nil {
		return task, err
	}
	head := boxGit(b.repo, "rev-parse", task.Branch)
	if head == "" {
		return task, fmt.Errorf("%s's branch %s is gone", task.ID, task.Branch)
	}
	var approval *teamTask
	for _, review := range loadTeamTasks(b.team) {
		if review.Kind == "review" && review.Review == task.ID && review.Commit == head && review.Status == "done" && review.Verdict == "approve" {
			approval = &review
		}
	}
	if approval == nil {
		return task, fmt.Errorf("no review approved %s's latest commit; run hi team review %s", task.ID, task.ID)
	}
	var packages []teamDependencyProject
	if base := boxGit(b.repo, "merge-base", config.Branch, head); base != "" {
		packages = teamDependencyChanges(b.repo, base, head)
	}
	if trusted := teamDependencyTrusted(packages); len(trusted) > 0 {
		if approvalRef == "" {
			return task, fmt.Errorf("%s adds %s to trustedDependencies, which runs their install scripts on every machine that installs the app. "+
				"Only an owner may allow that: ask the owners in the channel in plain words (which package, that it runs its own code when it is installed, and why the app needs it), end your reply, "+
				"and once an owner says yes, run hi team merge %s --approval <id>", task.ID, strings.Join(trusted, ", "), id)
		}
		yes, err := b.teamFindApproval(config, approvalRef, "trust")
		if err != nil {
			return task, err
		}
		task.TrustApproval = &yes
	}
	task.Packages = teamDependencyText(packages)
	if err := b.commitSpecsLocked(config); err != nil {
		return task, fmt.Errorf("committing the specs: %w", err)
	}
	if err := b.onTeamBranch(config); err != nil {
		return task, err
	}
	message := fmt.Sprintf("%s\n\nTask %s by the %s, approved in review %s by the reviewer.", task.Title, task.ID, task.Role, approval.ID)
	if out, err := teamGit(b.repo, "merge", "--no-ff", "-m", message, task.Branch); err != nil {
		conflicted := boxGit(b.repo, "diff", "--name-only", "--diff-filter=U")
		teamGit(b.repo, "merge", "--abort")
		if conflicted == "" {
			return task, fmt.Errorf("%s doesn't merge into %s: %s", task.ID, config.Branch, qFirstLine(out, err.Error()))
		}
		return task, &teamMergeConflict{branch: config.Branch, files: strings.Split(conflicted, "\n"), out: qFirstLine(out, err.Error())}
	}
	// The specs are the lead's: a member's changes to them, such as marking a
	// feature done before the people agree it is, are left out, so the
	// files never change under the lead.
	if kept := boxGit(b.repo, "diff", "--name-only", "HEAD^1", "HEAD", "--", teamSpecsPath); kept != "" {
		if out, err := teamGit(b.repo, "restore", "--source=HEAD^1", "--staged", "--worktree", "--", teamSpecsPath); err != nil {
			return task, fmt.Errorf("keeping the specs: %s", qFirstLine(out, err.Error()))
		}
		if out, err := teamGit(b.repo, "commit", "-q", "--amend", "--no-edit"); err != nil {
			return task, fmt.Errorf("keeping the specs: %s", qFirstLine(out, err.Error()))
		}
		task.SpecsKept = strings.Fields(kept)
	}
	task.Status, task.Merged = "merged", boxGit(b.repo, "rev-parse", "HEAD")
	if err := saveTeamTask(b.team, task); err != nil {
		return task, err
	}
	if config.Remote != "" {
		if out, err := teamGit(b.repo, "push", "-q", "origin", config.Branch); err != nil {
			task.Error = "merged, but git push failed: " + qFirstLine(out, err.Error())
			saveTeamTask(b.team, task)
		}
	}
	// The boxes of this task, the tasks it continued, and their reviews are
	// done with, and so are their branches.
	tasks := loadTeamTasks(b.team)
	merged := map[string]bool{}
	for _, other := range tasks {
		if other.Kind == "task" && !teamTaskActive(other) && teamIsAncestor(b.repo, other.Branch, "HEAD") {
			merged[other.ID] = true
			if other.Status == "done" {
				other.Status = "merged"
				saveTeamTask(b.team, other)
			}
		}
	}
	for _, other := range tasks {
		if (merged[other.ID] || merged[other.Review]) && !teamTaskActive(other) {
			b.run("box", "rm", "--force", other.Box)
			if other.Kind == "task" {
				os.RemoveAll(teamTaskFilesDir(b.team, other.ID))
			}
			if other.Branch != "" {
				teamGit(b.repo, "branch", "-D", other.Branch)
			}
		}
	}
	return task, nil
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

// teamTitle is a brief's first line, without its heading marks.
func teamTitle(brief string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(brief), "\n")
	return qClip(strings.TrimSpace(strings.TrimLeft(line, "# ")))
}

// ---------------------------------------------------------------------------
// the lead's commands, in its box

var teamLeadCommands = map[string]bool{"task": true, "review": true, "wait": true, "show": true, "tasks": true, "merge": true, "stop": true, "preview": true,
	"versions": true, "release": true, "discard": true, "web": true}

// teamLeadOrHost are commands both the lead and the people on the machine
// have; in the lead's box they go to its socket.
var teamLeadOrHost = map[string]bool{"env": true, "rollback": true, "helper": true}

func runTeamLeadCommand(command string, args []string, stdin io.Reader, stdout io.Writer) error {
	// --approval dm is a yes in the DM the lead is answering: Hermes says
	// whose it is.
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "--approval" && args[i+1] == "dm" {
			user := os.Getenv("HERMES_SESSION_USER_ID")
			if os.Getenv("HERMES_SESSION_CHAT_TYPE") != "dm" || user == "" {
				return errors.New("--approval dm is for a yes in the DM you are answering; elsewhere, name them: --approval dm:<their member ID>")
			}
			args[i+1] = "dm:" + user
		}
	}
	socket := os.Getenv(teamSocketEnv)
	if socket == "" {
		return errors.New("hi team " + command + " is for a team's lead, in its box; on this machine, hi team status <name> shows a team's tasks")
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
	var task teamTask
	switch command {
	case "task":
		var request teamRequest
		var words []string
		for i := 0; i < len(args); i++ {
			if args[i] == "--on" && i+1 < len(args) {
				request.On = args[i+1]
				i++
			} else if args[i] == "--approval" && i+1 < len(args) {
				request.Approval = args[i+1]
				i++
			} else if args[i] == "--small" {
				request.Small = true
			} else if args[i] == "--in" && i+1 < len(args) {
				request.In = args[i+1]
				i++
			} else if args[i] == "--new" {
				request.New = true
			} else if args[i] == "--attach" && i+1 < len(args) {
				attachment, err := teamReadAttachment(args[i+1])
				if err != nil {
					return err
				}
				request.Attachments = append(request.Attachments, attachment)
				i++
			} else {
				words = append(words, args[i])
			}
		}
		if len(words) != 2 {
			return usageError{"usage: hi team task <role> <brief.md|-> [--approval <message id> [--new] | --small | --on <task>] [--attach <file>]..."}
		}
		brief, err := teamReadBrief(words[1], stdin)
		if err != nil {
			return err
		}
		request.Role, request.Brief = words[0], brief
		if err := call("POST", "/task", request, &task); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "%s started: the %s (%s) works on %q.\n", task.ID, task.Role, agentDisplayName(task.Agent), task.Title)
		for _, hint := range task.Hints {
			fmt.Fprintln(stdout, hint)
		}
		if task.Files != "" {
			fmt.Fprintln(stdout, "It has the files from the conversation, and yours, at /box/files; its brief says what each is.")
		}
		fmt.Fprintf(stdout, "hi team wait %s waits for its report.\n", task.ID)
	case "review":
		if len(args) < 1 || len(args) > 2 {
			return usageError{"usage: hi team review <task> [<notes.md>]"}
		}
		request := teamRequest{Task: args[0]}
		if len(args) == 2 {
			notes, err := teamReadBrief(args[1], stdin)
			if err != nil {
				return err
			}
			request.Notes = notes
		}
		if err := call("POST", "/review", request, &task); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "%s started: the reviewer checks %s.\nhi team wait %s waits for the verdict.\n", task.ID, args[0], task.ID)
	case "wait", "show":
		if len(args) != 1 {
			return usageError{"usage: hi team " + command + " <task>"}
		}
		wait := "0s"
		if command == "wait" {
			wait = "25m"
		}
		var view teamTaskView
		if err := call("GET", "/task?id="+url.QueryEscape(args[0])+"&wait="+wait, nil, &view); err != nil {
			return err
		}
		printTeamTaskView(view, stdout)
	case "tasks":
		var tasks []teamTask
		if err := call("GET", "/tasks", nil, &tasks); err != nil {
			return err
		}
		if len(tasks) == 0 {
			fmt.Fprintln(stdout, "No tasks yet.")
			return nil
		}
		printTeamTasks(tasks, 30, stdout)
	case "merge":
		request := teamRequest{}
		for i := 0; i < len(args); i++ {
			switch {
			case args[i] == "--approval" && i+1 < len(args):
				request.Approval = args[i+1]
				i++
			case request.Task == "" && !strings.HasPrefix(args[i], "-"):
				request.Task = args[i]
			default:
				return usageError{"usage: hi team merge <task> [--approval <id>]  (an owner's yes, when the task trusts a package's install script)"}
			}
		}
		if request.Task == "" {
			return usageError{"usage: hi team merge <task> [--approval <id>]"}
		}
		if err := call("POST", "/merge", request, &task); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Merged %s (%s) as %s.\n", task.ID, task.Title, task.Merged[:min(len(task.Merged), 12)])
		if task.Packages != "" {
			fmt.Fprintf(stdout, "It changes the app's packages; tell the people in plain words what it adds and why:\n%s\n", task.Packages)
		}
		if len(task.SpecsKept) > 0 {
			fmt.Fprintf(stdout, "The task changed your specs (%s); the merge kept yours. Update them yourself if they need it.\n", strings.Join(task.SpecsKept, ", "))
		}
		if task.Error != "" {
			fmt.Fprintln(stdout, task.Error)
		}
		var view teamVersionsView
		if call("GET", "/versions", nil, &view) == nil {
			fmt.Fprintf(stdout, "It is in dev now: %s. Production didn't change.\n", strings.TrimPrefix(view.Dev, "dev, "))
		}
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
		fmt.Fprintf(stdout, "Models: $OPENROUTER_BASE_URL with $OPENROUTER_API_KEY, in previews and production.\n")
	case "versions":
		if len(args) != 0 {
			return usageError{"usage: hi team versions"}
		}
		var view teamVersionsView
		if err := call("GET", "/versions", nil, &view); err != nil {
			return err
		}
		printTeamVersions(view, stdout)
	case "release":
		var request teamReleaseRequest
		for i := 0; i < len(args); i++ {
			switch {
			case args[i] == "--approval" && i+1 < len(args):
				request.Approval = args[i+1]
				i++
			case args[i] == "--notes" && i+1 < len(args):
				notes, err := teamReadBrief(args[i+1], stdin)
				if err != nil {
					return err
				}
				request.Notes = notes
				i++
			case request.Version == 0 && teamVersionArg.MatchString(args[i]):
				request.Version, _ = strconv.Atoi(strings.TrimPrefix(args[i], "v"))
			default:
				return usageError{"usage: hi team release --approval <id> --notes <file|->  (dev to production as the next version)\n       hi team release v2 --approval <id> [--notes <file|->]  (production back to an earlier version)"}
			}
		}
		var result teamReleaseResult
		if err := call("POST", "/release", request, &result); err != nil {
			return err
		}
		if request.Version != 0 {
			fmt.Fprintf(stdout, "Production is on v%d now (it was on v%d). hi posted it in the channel.\n", result.Release.Version, result.Previous)
		} else {
			fmt.Fprintf(stdout, "Released v%d: production is dev's latest now. hi posted it in the channel with your notes.\n", result.Release.Version)
		}
		if result.Warning != "" {
			fmt.Fprintln(stdout, result.Warning)
		}
	case "rollback":
		var request teamReleaseRequest
		for i := 0; i < len(args); i++ {
			switch {
			case args[i] == "--approval" && i+1 < len(args):
				request.Approval = args[i+1]
				i++
			case request.Version == 0 && teamVersionArg.MatchString(args[i]):
				request.Version, _ = strconv.Atoi(strings.TrimPrefix(args[i], "v"))
			default:
				return usageError{"usage: hi team rollback v2 --approval <id>  (production back on v2, with its data as v2 left it)"}
			}
		}
		if request.Version == 0 {
			return usageError{"usage: hi team rollback v2 --approval <id>"}
		}
		var result teamReleaseResult
		if err := call("POST", "/release?data=1", request, &result); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Production is on v%d now (it was on v%d), with its data as of %s. What was entered since is in snapshot %s. hi posted it in the channel.\n",
			result.Release.Version, result.Previous, result.DataAt.Local().Format("Mon Jan 2 at 15:04"), result.Snapshot)
		if result.Warning != "" {
			fmt.Fprintln(stdout, result.Warning)
		}
	case "discard":
		request := teamRequest{}
		for i := 0; i < len(args); i++ {
			if args[i] == "--approval" && i+1 < len(args) {
				request.Approval = args[i+1]
				i++
			} else if request.Task == "" {
				request.Task = args[i]
			} else {
				request.Task = ""
				break
			}
		}
		if request.Task == "" || (request.Task != "dev" && request.Approval != "") {
			return usageError{"usage: hi team discard <task>  (an idea that isn't merged)\n       hi team discard dev --approval <id>  (dev's code back to production's)"}
		}
		var tasks []teamTask
		if err := call("POST", "/discard", request, &tasks); err != nil {
			return err
		}
		var ids []string
		for _, task := range tasks {
			ids = append(ids, task.ID)
		}
		if request.Task == "dev" {
			fmt.Fprintf(stdout, "Dev's code is production's again; hi posted it in the channel. Set aside: %s.\n", firstNonEmpty(strings.Join(ids, ", "), "nothing merged as a task"))
		} else {
			fmt.Fprintf(stdout, "Discarded %s, with its boxes and branches.\n", strings.Join(ids, ", "))
		}
	case "preview":
		var request teamPreviewRequest
		for i := 0; i < len(args); i++ {
			switch {
			case args[i] == "stop" && len(args) == 1:
				request.Stop = true
			case args[i] == "--port" && i+1 < len(args):
				port, err := strconv.Atoi(args[i+1])
				if err != nil {
					return usageError{"--port is a number"}
				}
				request.Port = port
				i++
			case request.Task == "" && !request.Production && args[i] == "production":
				request.Production = true
			case request.Task == "" && !request.Production && teamTaskID.MatchString(args[i]):
				request.Task = args[i]
			default:
				return usageError{"usage: hi team preview [<task> | production] [--port <n>] | hi team preview stop"}
			}
		}
		var preview teamPreview
		if err := call("POST", "/preview", request, &preview); err != nil {
			return err
		}
		if request.Stop {
			fmt.Fprintln(stdout, "The preview is stopped.")
			return nil
		}
		printTeamPreview(stdout, preview)
		return nil
	case "helper":
		usage := usageError{"usage: hi team helper  (whether the app has the helper)\n       hi team helper preview on|off\n       hi team helper production on|off --approval <id>  (an owner's yes in Slack)"}
		var request teamHelperRequest
		var words []string
		for i := 0; i < len(args); i++ {
			if args[i] == "--approval" && i+1 < len(args) {
				request.Approval = args[i+1]
				i++
			} else {
				words = append(words, args[i])
			}
		}
		var line string
		switch {
		case len(words) == 0 && request.Approval == "":
			if err := call("GET", "/helper", nil, &line); err != nil {
				return err
			}
		case len(words) == 2 && (words[1] == "on" || words[1] == "off"):
			request.Slot, request.On = words[0], words[1] == "on"
			if err := call("POST", "/helper", request, &line); err != nil {
				return err
			}
		default:
			return usage
		}
		fmt.Fprintln(stdout, line)
	case "web":
		usage := usageError{"usage: hi team web  (the chats and feedback in the app, last week)\n       hi team web show <chat|feedback>\n       hi team web post <chat> <text|-> [--attach <file>]...\n       hi team web questions  (what people asked the app's help)\n       hi team web link --user <Slack member ID>  (DM them their private links)"}
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
		case len(args) == 2 && args[0] == "show":
			if err := call("GET", "/web/show?id="+url.QueryEscape(args[1]), nil, &line); err != nil {
				return err
			}
		case len(args) >= 3 && args[0] == "post":
			post := teamWebPost{Conversation: args[1]}
			var words []string
			for i := 2; i < len(args); i++ {
				if args[i] == "--attach" && i+1 < len(args) {
					attachment, err := teamReadAttachment(args[i+1])
					if err != nil {
						return err
					}
					post.Attachments = append(post.Attachments, attachment)
					i++
				} else {
					words = append(words, args[i])
				}
			}
			post.Text = strings.Join(words, " ")
			if post.Text == "-" {
				data, err := io.ReadAll(io.LimitReader(stdin, teamWebMaxText+1))
				if err != nil {
					return err
				}
				post.Text = string(data)
			}
			if err := call("POST", "/web/post", post, &line); err != nil {
				return err
			}
		default:
			return usage
		}
		fmt.Fprintln(stdout, strings.TrimRight(line, "\n"))
	case "stop":
		if len(args) != 1 {
			return usageError{"usage: hi team stop <task>"}
		}
		if err := call("POST", "/stop", teamRequest{Task: args[0]}, &task); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Stopped %s.\n", task.ID)
	}
	return nil
}

// teamReadAttachment reads a file the lead attaches to a task.
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

func teamReadBrief(path string, stdin io.Reader) (string, error) {
	var data []byte
	var err error
	if path == "-" {
		data, err = io.ReadAll(io.LimitReader(stdin, teamMaxBrief+1))
	} else {
		data, err = os.ReadFile(path)
	}
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// printTeamTaskView is what the lead reads about a task: with its review
// loop, the steps, how it ended, the latest work and the last review.
func printTeamTaskView(view teamTaskView, w io.Writer) {
	if len(view.Steps) < 2 {
		printTeamTask(view.teamTask, w)
		return
	}
	first, last := view.Steps[0], view.Steps[len(view.Steps)-1]
	work := first
	for _, step := range view.Steps {
		if step.Kind == "task" {
			work = step
		}
	}
	fmt.Fprintf(w, "%s · %s (%s) · reviewed by the team\nTitle: %s\n\nSteps:\n", first.ID, first.Role, agentDisplayName(first.Agent), first.Title)
	table := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, step := range view.Steps {
		what := "work"
		if step.Kind == "review" {
			what = fmt.Sprintf("review %d of %s", step.Round, step.Review)
		} else if step.On != "" {
			what = "fix on " + step.On
		}
		status := step.Status
		if step.Verdict != "" {
			status += " (" + step.Verdict + ")"
		}
		fmt.Fprintf(table, "  %s\t%s\t%s\t%s\n", step.ID, step.Role, what, status)
	}
	table.Flush()
	fmt.Fprintln(w)
	switch {
	case teamTaskActive(last):
		fmt.Fprintf(w, "Still working: %s, the %s. The team fixes what the reviewer finds on its own; hi team wait %s waits again.\n", last.ID, last.Role, first.ID)
		return
	case last.Kind == "review" && last.Verdict == "approve":
		fmt.Fprintf(w, "Approved: the reviewer approved %s's latest commit. hi team merge %s merges it.\n", work.ID, first.ID)
	case last.Kind == "review" && last.Status == "done":
		fmt.Fprintf(w, "Changes still needed after %d reviews. Read the review below, then decide: continue with hi team task coder <brief.md> --on %s, or ask the people.\n", last.Round, first.ID)
	default:
		fmt.Fprintf(w, "%s %s; its report is below.\n", last.ID, last.Status)
	}
	fmt.Fprintln(w)
	printTeamTask(work, w)
	if last.ID != work.ID {
		fmt.Fprintln(w)
		printTeamTask(last, w)
	}
}

// printTeamTask is what the lead reads about one task.
func printTeamTask(task teamTask, w io.Writer) {
	fmt.Fprintf(w, "%s · %s (%s) · %s\n", task.ID, task.Role, agentDisplayName(task.Agent), task.Status)
	fmt.Fprintf(w, "Title: %s\n", task.Title)
	if task.Review != "" {
		fmt.Fprintf(w, "Reviews: %s at %s\n", task.Review, task.Commit[:min(len(task.Commit), 12)])
	}
	if task.On != "" {
		fmt.Fprintf(w, "Continues: %s\n", task.On)
	}
	if task.Branch != "" {
		fmt.Fprintf(w, "Branch: %s\n", task.Branch)
	}
	if task.Verdict != "" {
		fmt.Fprintf(w, "Verdict: %s\n", task.Verdict)
	}
	if task.Merged != "" {
		fmt.Fprintf(w, "Merged: %s\n", task.Merged[:min(len(task.Merged), 12)])
	}
	if task.Error != "" {
		fmt.Fprintf(w, "Error: %s\n", task.Error)
	}
	if teamTaskActive(task) {
		fmt.Fprintf(w, "Still working; hi team wait %s waits again.\n", task.ID)
		return
	}
	if report := task.Report; report != nil {
		if len(report.Report) > 0 && string(report.Report) != "null" {
			fmt.Fprintf(w, "Report: %s\n", string(report.Report))
		}
		if len(report.ChangedFiles) > 0 {
			fmt.Fprintf(w, "Changed: %s\n", strings.Join(report.ChangedFiles, ", "))
		}
		if stats := agentStats(*report); stats != "" {
			fmt.Fprintf(w, "Stats: %s\n", stats)
		}
		for _, warning := range report.Warnings {
			fmt.Fprintf(w, "Warning: %s\n", warning)
		}
		if text := strings.TrimSpace(agentReportBlock.ReplaceAllString(report.Text, "")); text != "" {
			fmt.Fprintf(w, "\n%s\n", text)
		}
		if proposals := teamLearnedProposals(report); len(proposals) > 0 {
			fmt.Fprintf(w, "\nThe %s proposes for /team/roles/%s/%s; add what's true and useful, in a line each with %s:\n", task.Role, task.Role, teamLearnedFile, task.ID)
			for _, proposal := range proposals {
				fmt.Fprintf(w, "  - %s\n", proposal)
			}
		}
	}
}
