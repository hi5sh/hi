package main

// What the dev box asks of the team (docs/specs/approved/hi_team_dev.md,
// release 2). The box gets a socket of its own, with the broker's `dev`
// role: it may put a branch of its clone on the preview, ship one into
// dev, post in the channel, read the team's state, and file and close
// tickets; release 2b adds team_devkit.go. A release, a rollback, a restore, production's settings and
// the owners aren't on this socket at all: they take an owner on the page
// or an admin on the machine. Inside the box, hi dev is the client.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	// teamDevRole is the role of the work the dev box ships.
	teamDevRole = "dev"
	// teamDevSocketEnv tells hi dev in the box where the broker is.
	teamDevSocketEnv   = "HI_DEV_SOCKET"
	teamDevSocketName  = "dev.sock"
	teamDevSocketInBox = "/box/run/" + teamDevSocketName
)

// teamDevSocketDir holds the dev box's socket, beside the broker's: short,
// in the user's runtime folder when there is one.
func teamDevSocketDir(team string) string { return teamBrokerDir(team) + "-dev" }

// teamDevBranchRef is where the team's repository keeps a branch of the
// dev box's clone.
func teamDevBranchRef(branch string) string { return "box/" + branch }

var teamDevBranchPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,99}$`)

// fetchDevBranch takes a branch of the dev box's clone into the team's
// repository, as box/<branch>, and returns its commit.
func (b *teamBroker) fetchDevBranch(branch string) (string, error) {
	if !teamDevBranchPattern.MatchString(branch) || strings.Contains(branch, "..") || strings.HasSuffix(branch, ".lock") {
		return "", fmt.Errorf("%q isn't a branch name hi takes", branch)
	}
	clone := filepath.Join(teamDevDir(b.team), "repo")
	if !fileExists(filepath.Join(clone, ".git")) {
		return "", errors.New("the dev box has no clone of the project yet")
	}
	if boxGit(clone, "rev-parse", "--verify", "-q", "refs/heads/"+branch) == "" {
		return "", fmt.Errorf("the dev box's clone has no branch %s; commit on it first", branch)
	}
	ref := "refs/heads/" + teamDevBranchRef(branch)
	if out, err := teamGit(b.repo, "fetch", "-q", "--no-tags", clone, "+refs/heads/"+branch+":"+ref); err != nil {
		return "", fmt.Errorf("taking %s from the dev box: %s", branch, qFirstLine(out, err.Error()))
	}
	return boxGit(b.repo, "rev-parse", ref), nil
}

// previewDevBranch puts a branch of the dev box on the preview, with a
// fresh fork of production's data when it is new work there.
func (b *teamBroker) previewDevBranch(branch string) (teamPreview, error) {
	if _, err := b.fetchDevBranch(branch); err != nil {
		return teamPreview{}, err
	}
	data := ""
	if config, err := loadTeam(b.team); err == nil && config.PreviewData != teamPreviewEmpty && teamOverlayEngine() {
		if current, ok := loadTeamSlot(b.team, teamSlotPreview); !ok || current.Source != teamDevBranchRef(branch) {
			data = b.forkForNewWork()
		}
	}
	b.previewMu.Lock()
	defer b.previewMu.Unlock()
	next := teamPreview{Slot: teamSlotPreview, Source: teamDevBranchRef(branch), Label: branch + ", not in dev yet"}
	state, err := b.show(next, "refs/heads/"+teamDevBranchRef(branch), teamLinkKeep)
	state.Data = data
	return state, err
}

// teamDevShipRequest ships a branch of the dev box into dev.
type teamDevShipRequest struct {
	Branch string `json:"branch"`
	Title  string `json:"title,omitempty"`
	Dev    bool   `json:"dev,omitempty"` // hi dev preview --dev: dev itself, not a branch
}

// teamDevShipResult is the work shipped, and dev on the preview after.
type teamDevShipResult struct {
	Task    teamTask    `json:"task"`
	Preview teamPreview `json:"preview"`
	Warning string      `json:"warning,omitempty"`
}

// shipDevBranch merges a branch of the dev box into dev, records it as
// work, so the versions and the release say what dev has, and puts dev on
// the preview.
func (b *teamBroker) shipDevBranch(request teamDevShipRequest) (teamDevShipResult, error) {
	head, err := b.fetchDevBranch(request.Branch)
	if err != nil {
		return teamDevShipResult{}, err
	}
	task, err := b.mergeDevBranch(request, head)
	if err != nil {
		return teamDevShipResult{}, err
	}
	result := teamDevShipResult{Task: task}
	if result.Preview, err = b.preview(teamPreviewRequest{}); err != nil {
		result.Warning = "Shipped, but dev isn't on the preview: " + err.Error()
	}
	return result, nil
}

func (b *teamBroker) mergeDevBranch(request teamDevShipRequest, head string) (teamTask, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	config, err := loadTeam(b.team)
	if err != nil {
		return teamTask{}, err
	}
	if teamIsAncestor(b.repo, head, config.Branch) {
		return teamTask{}, fmt.Errorf("dev has everything on %s already", request.Branch)
	}
	base := boxGit(b.repo, "merge-base", config.Branch, head)
	title := strings.TrimSpace(request.Title)
	if title == "" {
		title = firstNonEmpty(boxGit(b.repo, "log", "-1", "--format=%s", head), request.Branch)
	}
	// What only an owner may allow stays out of what the box ships.
	var packages []teamDependencyProject
	if base != "" {
		packages = teamDependencyChanges(b.repo, base, head)
	}
	if trusted := teamDependencyTrusted(packages); len(trusted) > 0 {
		return teamTask{}, fmt.Errorf("%s adds %s to trustedDependencies, which runs their install scripts on every machine that installs the app; an admin of the machine merges that, not the dev box", request.Branch, strings.Join(trusted, ", "))
	}
	if advisories, err := teamDependencyAdvisories(packages); err == nil {
		if exploited := teamDependencyExploited(advisories); len(exploited) > 0 {
			return teamTask{}, fmt.Errorf("%s brings in %s, with an advisory known to be exploited (CISA KEV); use a fixed version", request.Branch, strings.Join(exploited, ", "))
		}
	}
	if err := b.commitSpecsLocked(config); err != nil {
		return teamTask{}, fmt.Errorf("committing the specs: %w", err)
	}
	if err := b.onTeamBranch(config); err != nil {
		return teamTask{}, err
	}
	message := fmt.Sprintf("%s\n\nBuilt in the dev box, on its branch %s.", title, request.Branch)
	if out, err := teamGit(b.repo, "merge", "--no-ff", "-m", message, "refs/heads/"+teamDevBranchRef(request.Branch)); err != nil {
		conflicted := boxGit(b.repo, "diff", "--name-only", "--diff-filter=U")
		teamGit(b.repo, "merge", "--abort")
		if conflicted == "" {
			return teamTask{}, fmt.Errorf("%s doesn't merge into dev: %s", request.Branch, qFirstLine(out, err.Error()))
		}
		return teamTask{}, fmt.Errorf("%s conflicts with dev in %s; in the box: git fetch origin && git rebase origin/%s, then ship again",
			request.Branch, strings.Join(strings.Fields(conflicted), ", "), config.Branch)
	}
	tasks := loadTeamTasks(b.team)
	n := 1
	if len(tasks) > 0 {
		n = teamTaskNumber(tasks[len(tasks)-1].ID) + 1
	}
	now := time.Now().UTC()
	task := teamTask{ID: fmt.Sprintf("t-%d", n), Role: teamDevRole, Agent: "dev box", Kind: "task", Title: title, Status: "merged",
		Branch: teamDevBranchRef(request.Branch), Merged: boxGit(b.repo, "rev-parse", "HEAD"), Created: now, Finished: now, Packages: teamDependencyText(packages)}
	if err := saveTeamTask(b.team, task); err != nil {
		return task, err
	}
	if config.Remote != "" {
		if out, err := teamGit(b.repo, "push", "-q", "origin", config.Branch); err != nil {
			task.Error = "merged, but git push failed: " + qFirstLine(out, err.Error())
			saveTeamTask(b.team, task)
		}
	}
	return task, nil
}

// teamDevSayRequest is a message the dev box posts in the channel.
type teamDevSayRequest struct {
	Text   string `json:"text"`
	Thread string `json:"thread,omitempty"`
	// Ops is hi's own post about the box's reach, for the ops channel;
	// the box can't set it.
	Ops bool `json:"-"`
}

func (b *teamBroker) devSay(request teamDevSayRequest) (string, error) {
	text := strings.TrimSpace(request.Text)
	if text == "" {
		return "", errors.New("nothing to say")
	}
	if b.channel == nil {
		return "", errors.New("hi isn't connected to Slack")
	}
	config, err := loadTeam(b.team)
	if err != nil {
		return "", err
	}
	text = ":computer: " + text
	channel := config.Channel
	if request.Ops {
		channel = config.opsChannel()
	}
	if request.Thread != "" {
		return b.channel.postIn(channel, request.Thread, text)
	}
	return b.channel.post(channel, text)
}

// teamDevStatusView is the team's state, for the dev box.
type teamDevStatusView struct {
	Production int      `json:"production"`
	Dev        string   `json:"dev"`
	Changes    []string `json:"changes,omitempty"`
	Preview    string   `json:"preview,omitempty"` // its label
	PreviewURL string   `json:"preview_url,omitempty"`
	// PreviewBranch is the dev box's branch the preview shows instead of
	// dev; PreviewOld, that it shows dev as it was, before what was shipped
	// since. Either way, Show dev puts dev as it is on it.
	PreviewBranch string   `json:"preview_branch,omitempty"`
	PreviewOld    bool     `json:"preview_old,omitempty"`
	ProductionURL string   `json:"production_url,omitempty"`
	Snapshot      string   `json:"snapshot,omitempty"`
	Incidents     []string `json:"incidents,omitempty"`
	Jobs          []string `json:"jobs,omitempty"`
}

func (b *teamBroker) devStatus() (teamDevStatusView, error) {
	view, err := teamVersionsReport(b.team)
	if err != nil {
		return teamDevStatusView{}, err
	}
	status := teamDevStatusView{Production: view.Production, Dev: view.Dev, Snapshot: teamSnapshotLine(b.team)}
	for _, task := range view.Changes {
		status.Changes = append(status.Changes, fmt.Sprintf("%s %s (%s)", task.ID, task.Title, teamTaskAskedBy(task)))
	}
	if slot, ok := loadTeamSlot(b.team, teamSlotPreview); ok {
		status.Preview, status.PreviewURL = slot.Label, slot.URL
		// A label says what dev was when it went on; a release since
		// changes what dev is called.
		if config, err := loadTeam(b.team); err == nil {
			switch {
			case strings.HasPrefix(slot.Source, teamDevBranchRef("")):
				status.PreviewBranch = strings.TrimPrefix(slot.Source, teamDevBranchRef(""))
			case slot.Source == config.Branch && slot.Commit != "" && boxGit(filepath.Join(teamDir(b.team), "repo"), "rev-parse", config.Branch) == slot.Commit:
				status.Preview = view.Dev
			case slot.Source == config.Branch:
				status.PreviewOld = true
			}
		}
	}
	if slot, ok := loadTeamSlot(b.team, teamSlotProduction); ok {
		status.ProductionURL = slot.URL
	}
	for _, incident := range loadTeamIncidents(b.team) {
		if incident.Status != "closed" {
			status.Incidents = append(status.Incidents, incident.ID+" "+incident.Title)
		}
	}
	status.Jobs = teamJobLines(b.team, time.Now())
	return status, nil
}

// listenDev opens the dev box's socket.
func (b *teamBroker) listenDev(ctx context.Context) (func() error, error) {
	dir := teamDevSocketDir(b.team)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return b.listenWith(ctx, filepath.Join(dir, teamDevSocketName), b.devHandler())
}

// devHandler is all the dev box may ask: nothing on it moves production.
func (b *teamBroker) devHandler() http.Handler {
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
	decode := func(r *http.Request, into any) error {
		// Attached files come base64-encoded, a third larger.
		return json.NewDecoder(io.LimitReader(r.Body, teamMaxAttachments*4/3+1<<20)).Decode(into)
	}
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) {
		status, err := b.devStatus()
		reply(w, status, err)
	})
	mux.HandleFunc("POST /preview", func(w http.ResponseWriter, r *http.Request) {
		var request teamDevShipRequest
		if err := decode(r, &request); err != nil {
			reply(w, nil, err)
			return
		}
		if request.Dev {
			state, err := b.preview(teamPreviewRequest{})
			reply(w, state, err)
			return
		}
		state, err := b.previewDevBranch(request.Branch)
		reply(w, state, err)
	})
	mux.HandleFunc("POST /ship", func(w http.ResponseWriter, r *http.Request) {
		var request teamDevShipRequest
		if err := decode(r, &request); err != nil {
			reply(w, nil, err)
			return
		}
		result, err := b.shipDevBranch(request)
		reply(w, result, err)
	})
	mux.HandleFunc("POST /say", func(w http.ResponseWriter, r *http.Request) {
		var request teamDevSayRequest
		if err := decode(r, &request); err != nil {
			reply(w, nil, err)
			return
		}
		ts, err := b.devSay(request)
		reply(w, map[string]string{"ts": ts}, err)
	})
	mux.HandleFunc("POST /ticket", func(w http.ResponseWriter, r *http.Request) {
		var request teamTicketRequest
		if err := decode(r, &request); err != nil {
			reply(w, nil, err)
			return
		}
		if teamTicketOwnerActions[request.Action] {
			reply(w, nil, errors.New("comments and files are deleted on the dev page, by an owner"))
			return
		}
		request.Author = "the dev box"
		result, err := teamTicketDo(b.team, request)
		reply(w, result, err)
	})
	mux.HandleFunc("POST /allow", func(w http.ResponseWriter, r *http.Request) {
		var request struct{ Host, Why, Work string }
		if err := decode(r, &request); err != nil {
			reply(w, nil, err)
			return
		}
		answer, err := b.devSiteAsk(request.Host, request.Why, request.Work)
		reply(w, answer, err)
	})
	mux.HandleFunc("GET /allow", func(w http.ResponseWriter, r *http.Request) {
		if host := r.URL.Query().Get("host"); host != "" {
			teamDevSiteWaiting(b.team, host)
			reply(w, b.devSiteState(host), nil)
			return
		}
		reply(w, b.devAccessView(), nil)
	})
	b.devKitRoutes(mux, reply, decode)
	return mux
}

// ---------------------------------------------------------------------------
// hi dev, in the dev box

func runDev(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	return exitCode(teamDevClient(args, stdin, stdout), stderr)
}

const teamDevUsage = `hi dev is how the dev box works with its team. In the box:

  hi dev status                     production's and dev's versions, what dev has, the preview,
                                    backups, open incidents, the app's jobs
  hi dev preview [<branch>]         put the branch (the current one by default) on the team's preview,
                                    with a fresh copy of production's data when it is new work there
  hi dev preview --dev              dev on the preview again, after a branch was tried there
  hi dev ship [<branch>] [-m <title>]  merge the branch into dev, and put dev on the preview;
                                    production moves only when someone releases it
  hi dev say [--thread <ts>] <text|->  post in the team's Slack channel
  hi dev allow <site> --why <what for> [--wait]
                                    ask the team's owners to let the box reach a site its proxy
                                    refused; --wait waits up to 9 minutes for their answer
  hi dev allow [<site>]             what the owners allowed, asks waiting, and refusals; or one site
  hi dev task list [--closed]       the team's tasks (GitHub, GitLab, or hi's own)
  hi dev task show <n>
  hi dev task add <title> [<body|->]
  hi dev task comment <n> <text|->
  hi dev task edit <n> [--title <title>] <body|->   the task's whole new text
  hi dev task close <n> [<comment>]
  hi dev review [--with claude|codex] [--base <ref>] [<notes|->]
                                    a second opinion on the branch's commits from the other agent
  hi dev deep-review --budget <usd> [--area a,b] [--since v2] [--context <file|->]
                                    a multi-agent review of the whole project (up to $50); the
                                    report goes to ~/hi/reviews/<id>/; list, show <id>, stop <id>
  hi dev brainstorm [--agents a,b] [--web] <question file|->
                                    several agents' ideas, into ~/hi/brainstorms/<id>/ (up to $10);
                                    list, show <id>, stop <id>
  hi dev agy "<task>" [--file <path>]...
                                    Google's Antigravity makes images, media, or research on the
                                    machine, into ~/hi/agy/<id>/; ask the user first; show [<id>]
  hi dev data [fork]                production's and the preview's data; a new fork for the preview
  hi dev settings [KEY=VALUE | KEY | --unset KEY]
                                    the app's settings' names; the preview's own values
  hi dev incidents [<id>]           what hi recorded about incidents: open ones, and the last month's
  hi dev upstream                   for an adopted project: what's new upstream, and its refs in the clone
  hi dev propose <work> <notes|->   offer shipped work back to the adopted project, as a draft pull request
`

func teamDevClient(args []string, stdin io.Reader, stdout io.Writer) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprint(stdout, teamDevUsage)
		return nil
	}
	socket := os.Getenv(teamDevSocketEnv)
	if socket == "" {
		return errors.New("hi dev works in a team's dev box; on the machine, use hi team")
	}
	call := func(method, path string, body, out any) error {
		client := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socket)
		}}}
		data, _ := json.Marshal(body)
		request, _ := http.NewRequest(method, "http://team"+path, strings.NewReader(string(data)))
		response, err := client.Do(request)
		if err != nil {
			return errors.New("the team's service isn't answering; it may be restarting, so try again in a minute")
		}
		defer response.Body.Close()
		answer, _ := io.ReadAll(response.Body)
		if response.StatusCode != http.StatusOK {
			var failure struct{ Error string }
			json.Unmarshal(answer, &failure)
			return errors.New(firstNonEmpty(failure.Error, response.Status))
		}
		return json.Unmarshal(answer, out)
	}
	currentBranch := func() (string, error) {
		branch := boxGit(".", "rev-parse", "--abbrev-ref", "HEAD")
		if branch == "" || branch == "HEAD" {
			return "", errors.New("name the branch: this folder isn't on one")
		}
		return branch, nil
	}
	command, rest := args[0], args[1:]
	switch command {
	case "status":
		var status teamDevStatusView
		if err := call("GET", "/status", nil, &status); err != nil {
			return err
		}
		printTeamDevStatus(status, stdout)
		return nil
	case "preview":
		if len(rest) > 1 {
			return usageError{"usage: hi dev preview [<branch> | --dev]"}
		}
		if len(rest) == 1 && rest[0] == "--dev" {
			var state teamPreview
			if err := call("POST", "/preview", teamDevShipRequest{Dev: true}, &state); err != nil {
				return err
			}
			fmt.Fprintf(stdout, "Dev is on the preview: %s\n", firstNonEmpty(state.URL, "(no address yet)"))
			return nil
		}
		branch := ""
		if len(rest) == 1 {
			branch = rest[0]
		} else {
			var err error
			if branch, err = currentBranch(); err != nil {
				return err
			}
		}
		var state teamPreview
		if err := call("POST", "/preview", teamDevShipRequest{Branch: branch}, &state); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "%s is on the preview: %s\n", branch, firstNonEmpty(state.URL, "(no address yet)"))
		if state.Data != "" {
			fmt.Fprintln(stdout, state.Data)
		}
		return nil
	case "ship":
		var request teamDevShipRequest
		for i := 0; i < len(rest); i++ {
			switch {
			case (rest[i] == "-m" || rest[i] == "--title") && i+1 < len(rest):
				i++
				request.Title = rest[i]
			case strings.HasPrefix(rest[i], "-"):
				return usageError{"usage: hi dev ship [<branch>] [-m <title>]"}
			case request.Branch == "":
				request.Branch = rest[i]
			default:
				return usageError{"usage: hi dev ship [<branch>] [-m <title>]"}
			}
		}
		if request.Branch == "" {
			var err error
			if request.Branch, err = currentBranch(); err != nil {
				return err
			}
		}
		var result teamDevShipResult
		if err := call("POST", "/ship", request, &result); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "%s is in dev as %s: %s.\n", request.Branch, result.Task.ID, result.Task.Title)
		if result.Preview.URL != "" {
			fmt.Fprintf(stdout, "Dev is on the preview: %s\n", result.Preview.URL)
		}
		for _, line := range []string{result.Warning, result.Task.Error} {
			if line != "" {
				fmt.Fprintln(stdout, line)
			}
		}
		fmt.Fprintln(stdout, "Production moves when an owner or a developer releases dev. In the box, git fetch origin brings dev into the clone.")
		fmt.Fprintln(stdout, "Done with this branch? Done ✓ beside its workspace on the dev page (the Code tab) removes its worktree, workspace, and branch, and closes its task.")
		return nil
	case "say":
		request := teamDevSayRequest{}
		if len(rest) >= 2 && rest[0] == "--thread" {
			request.Thread, rest = rest[1], rest[2:]
		}
		text, err := teamDevText(rest, stdin)
		if err != nil {
			return err
		}
		request.Text = text
		var answer map[string]string
		if err := call("POST", "/say", request, &answer); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Posted in the team's channel (%s).\n", answer["ts"])
		return nil
	case "allow":
		return teamDevAllowClient(rest, stdout, call, currentBranch)
	case "task", "tasks", "ticket", "tickets": // ticket: its name before hi 2.5.14
		request, err := parseTeamTicketArgs(rest, stdin)
		if err != nil {
			return err
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
	}
	if known, err := teamDevKitClient(command, rest, stdin, stdout, call); known {
		return err
	}
	fmt.Fprint(stdout, teamDevUsage)
	return usageError{"unknown command hi dev " + command}
}

// teamDevText is the words given, or stdin for "-".
func teamDevText(words []string, stdin io.Reader) (string, error) {
	if len(words) == 1 && words[0] == "-" {
		data, err := io.ReadAll(io.LimitReader(stdin, 64<<10))
		return strings.TrimSpace(string(data)), err
	}
	text := strings.TrimSpace(strings.Join(words, " "))
	if text == "" {
		return "", errors.New("nothing to say")
	}
	// A file's path is its text: agents write a long body to a file first.
	if len(words) == 1 && teamLooksLikePath(text) {
		if rest, ok := strings.CutPrefix(text, "~/"); ok {
			home, _ := os.UserHomeDir()
			text = filepath.Join(home, rest)
		}
		info, err := os.Stat(text)
		if err != nil || !info.Mode().IsRegular() {
			return "", fmt.Errorf("%s isn't a file here; give the text itself, or - and the text on stdin", text)
		}
		data, err := os.ReadFile(text)
		if err != nil {
			return "", err
		}
		if len(data) > 64<<10 {
			return "", fmt.Errorf("%s is larger than 64 KB", text)
		}
		return strings.TrimSpace(string(data)), nil
	}
	return text, nil
}

// teamLooksLikePath says a word is a file's path and nothing else: one
// line, no spaces, starting with / or ./ or ~/.
func teamLooksLikePath(text string) bool {
	return !strings.ContainsAny(text, " \t\n") && (strings.HasPrefix(text, "/") && len(text) > 1 || strings.HasPrefix(text, "./") || strings.HasPrefix(text, "~/"))
}

func printTeamDevStatus(status teamDevStatusView, w io.Writer) {
	if status.Production == 0 {
		fmt.Fprintln(w, "Production: nothing released yet.")
	} else {
		fmt.Fprintf(w, "Production: v%d %s\n", status.Production, status.ProductionURL)
	}
	fmt.Fprintf(w, "Dev: %s\n", strings.TrimPrefix(status.Dev, "dev, "))
	for _, change := range status.Changes {
		fmt.Fprintf(w, "  %s\n", change)
	}
	if status.Preview != "" {
		fmt.Fprintf(w, "Preview: %s %s\n", status.Preview, status.PreviewURL)
		switch {
		case status.PreviewBranch != "":
			fmt.Fprintln(w, "  a branch, not dev; hi dev preview --dev puts dev back on it")
		case status.PreviewOld:
			fmt.Fprintln(w, "  dev as it was, before what was shipped since; hi dev preview --dev shows dev as it is")
		}
	}
	if status.Snapshot != "" {
		fmt.Fprintf(w, "Backups: %s\n", status.Snapshot)
	}
	if len(status.Incidents) == 0 {
		fmt.Fprintln(w, "Incidents: none open")
	} else {
		fmt.Fprintf(w, "Incidents: %s\n", strings.Join(status.Incidents, "; "))
	}
	if len(status.Jobs) > 0 {
		fmt.Fprintf(w, "Jobs: %s\n", strings.Join(status.Jobs, "; "))
	}
}
