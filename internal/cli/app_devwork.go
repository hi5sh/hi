package cli

// What the dev box asks of the team (docs/specs/approved/hi_app_dev.md,
// release 2). The box gets a socket of its own, with the broker's `dev`
// role: it may put a branch of its clone on the preview, ship one into
// dev, post in the channel, read the team's state, and file and close
// tickets; release 2b adds app_devkit.go. A release, a rollback, a restore, production's settings and
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
	// appDevRole is the role of the work the dev box ships.
	appDevRole = "dev"
	// appDevSocketEnv tells hi dev in the box where the broker is.
	appDevSocketEnv   = "HI_DEV_SOCKET"
	appDevSocketName  = "dev.sock"
	appDevSocketInBox = "/box/run/" + appDevSocketName
)

// appDevSocketDir holds the dev box's socket, beside the broker's: short,
// in the user's runtime folder when there is one.
func appDevSocketDir(team string) string { return appBrokerDir(team) + "-dev" }

// appDevBranchRef is where the team's repository keeps a branch of the
// dev box's clone.
func appDevBranchRef(branch string) string { return "box/" + branch }

var appDevBranchPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,99}$`)

// fetchDevBranch takes a branch of the dev box's clone into the team's
// repository, as box/<branch>, and returns its commit.
func (b *appBroker) fetchDevBranch(branch string) (string, error) {
	if !appDevBranchPattern.MatchString(branch) || strings.Contains(branch, "..") || strings.HasSuffix(branch, ".lock") {
		return "", fmt.Errorf("%q isn't a branch name hi takes", branch)
	}
	clone := filepath.Join(appDevDir(b.team), "repo")
	if !fileExists(filepath.Join(clone, ".git")) {
		return "", errors.New("the dev box has no clone of the project yet")
	}
	if boxGit(clone, "rev-parse", "--verify", "-q", "refs/heads/"+branch) == "" {
		return "", fmt.Errorf("the dev box's clone has no branch %s; commit on it first", branch)
	}
	ref := "refs/heads/" + appDevBranchRef(branch)
	if out, err := appGit(b.repo, "fetch", "-q", "--no-tags", clone, "+refs/heads/"+branch+":"+ref); err != nil {
		return "", fmt.Errorf("taking %s from the dev box: %s", branch, qFirstLine(out, err.Error()))
	}
	return boxGit(b.repo, "rev-parse", ref), nil
}

// previewDevBranch puts a branch of the dev box on the preview, with a
// fresh fork of production's data when it is new work there.
func (b *appBroker) previewDevBranch(branch string) (appPreview, error) {
	if _, err := b.fetchDevBranch(branch); err != nil {
		return appPreview{}, err
	}
	data := ""
	if config, err := loadTeam(b.team); err == nil && config.PreviewData != appPreviewEmpty && appOverlayEngine() {
		if current, ok := loadTeamSlot(b.team, appSlotPreview); !ok || current.Source != appDevBranchRef(branch) {
			data = b.forkForNewWork()
		}
	}
	b.previewMu.Lock()
	defer b.previewMu.Unlock()
	next := appPreview{Slot: appSlotPreview, Source: appDevBranchRef(branch), Label: branch + ", not in dev yet"}
	state, err := b.show(next, "refs/heads/"+appDevBranchRef(branch), appLinkKeep)
	state.Data = data
	return state, err
}

// appDevShipRequest ships a branch of the dev box into dev.
type appDevShipRequest struct {
	Branch string `json:"branch"`
	Title  string `json:"title,omitempty"`
	Dev    bool   `json:"dev,omitempty"` // hi dev preview --dev: dev itself, not a branch
	// TakeOut is the change dev had last, taken out for this one in the
	// same step: the dev page's, after a conflict, never the box's.
	TakeOut string `json:"-"`
	By      string `json:"-"` // who, for the record
	// Pushed says Branch is one pushed to the app's remote (pushed/<branch>),
	// not the dev box's; Author, who pushed it, for the record.
	Pushed bool   `json:"pushed,omitempty"`
	Author string `json:"-"`
}

// ref is the branch's ref in the team's repository.
func (r appDevShipRequest) ref() string {
	if r.Pushed {
		return appPushedRef(r.Branch)
	}
	return appDevBranchRef(r.Branch)
}

// appDevConflictError is a branch that conflicts with dev: the files.
type appDevConflictError struct {
	Branch string
	Files  []string
	Dev    string // dev's branch
	Pushed bool   // a branch pushed to the remote, not the dev box's
}

func (e appDevConflictError) Error() string {
	if e.Pushed {
		return fmt.Sprintf("%s conflicts with dev in %s; on the machine it was pushed from: git fetch origin && git rebase origin/%s, then push it again",
			e.Branch, strings.Join(e.Files, ", "), e.Dev)
	}
	return fmt.Sprintf("%s conflicts with dev in %s; in the box: git fetch origin && git rebase origin/%s, then ship again",
		e.Branch, strings.Join(e.Files, ", "), e.Dev)
}

// appDevTakenOut is the change a branch had in dev, when it was taken out
// since (Take out, or dev set back to production): its commits are in
// dev's history, so a merge would add nothing, and shipping it again puts
// the change back.
func appDevTakenOut(tasks []appTask, branch string) *appTask {
	return appBranchTakenOut(tasks, appDevBranchRef(branch))
}

// appBranchTakenOut is appDevTakenOut for the team's repository's ref: a
// branch of the dev box (box/<branch>) or one pushed (pushed/<branch>).
func appBranchTakenOut(tasks []appTask, ref string) *appTask {
	var last *appTask
	for i := range tasks {
		if tasks[i].Kind == "task" && tasks[i].Branch == ref {
			last = &tasks[i]
		}
	}
	if last != nil && last.Status == "discarded" && last.Merged != "" {
		return last
	}
	return nil
}

// appDevShipped says a branch's work is in dev: its last change was
// merged, and not taken out since.
func appDevShipped(tasks []appTask, branch string) bool {
	return appBranchShipped(tasks, appDevBranchRef(branch))
}

func appBranchShipped(tasks []appTask, ref string) bool {
	var last *appTask
	for i := range tasks {
		if tasks[i].Kind == "task" && tasks[i].Branch == ref {
			last = &tasks[i]
		}
	}
	return last != nil && last.Status == "merged"
}

// appGitParent1 adds -m 1 for a merge commit, which revert and
// cherry-pick need to know which side was dev.
func appGitParent1(repo, commit string, args []string) []string {
	if len(strings.Fields(boxGit(repo, "rev-list", "--parents", "-n", "1", commit))) > 2 {
		args = append(args, "-m", "1")
	}
	return append(args, commit)
}

// appDevChangeFiles are the files a change in dev changed.
func appDevChangeFiles(repo string, task appTask) []string {
	return strings.Fields(boxGit(repo, "diff", "--name-only", task.Merged+"^1", task.Merged))
}

// appDevShipResult is the work shipped, and dev on the preview after.
type appDevShipResult struct {
	Task    appTask    `json:"task"`
	Preview appPreview `json:"preview"`
	Warning string     `json:"warning,omitempty"`
}

// shipDevBranch merges a branch of the dev box into dev, records it as
// work, so the versions and the release say what dev has, and puts dev on
// the preview.
func (b *appBroker) shipDevBranch(request appDevShipRequest) (appDevShipResult, error) {
	head, err := b.fetchDevBranch(request.Branch)
	if err != nil {
		return appDevShipResult{}, err
	}
	task, err := b.mergeDevBranch(request, head)
	if err != nil {
		return appDevShipResult{}, err
	}
	result := appDevShipResult{Task: task}
	if result.Preview, err = b.preview(appPreviewRequest{}); err != nil {
		result.Warning = "Shipped, but dev isn't on the preview: " + err.Error()
	}
	return result, nil
}

func (b *appBroker) mergeDevBranch(request appDevShipRequest, head string) (appTask, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	config, err := loadTeam(b.team)
	if err != nil {
		return appTask{}, err
	}
	var again *appTask
	if appIsAncestor(b.repo, head, config.Branch) {
		if again = appBranchTakenOut(loadTeamTasks(b.team), request.ref()); again == nil {
			return appTask{}, fmt.Errorf("dev has everything on %s already", request.Branch)
		}
	}
	base := boxGit(b.repo, "merge-base", config.Branch, head)
	title := strings.TrimSpace(request.Title)
	if title == "" {
		title = firstNonEmpty(boxGit(b.repo, "log", "-1", "--format=%s", head), request.Branch)
	}
	// What only an owner may allow stays out of what the box ships.
	var packages []appDependencyProject
	if base != "" {
		packages = appDependencyChanges(b.repo, base, head)
	}
	if trusted := appDependencyTrusted(packages); len(trusted) > 0 {
		return appTask{}, fmt.Errorf("%s adds %s to trustedDependencies, which runs their install scripts on every machine that installs the app; an admin of the machine merges that, not the dev box", request.Branch, strings.Join(trusted, ", "))
	}
	if advisories, err := appDependencyAdvisories(packages); err == nil {
		if exploited := appDependencyExploited(advisories); len(exploited) > 0 {
			return appTask{}, fmt.Errorf("%s brings in %s, with an advisory known to be exploited (CISA KEV); use a fixed version", request.Branch, strings.Join(exploited, ", "))
		}
	}
	if err := b.commitSpecsLocked(config); err != nil {
		return appTask{}, fmt.Errorf("committing the specs: %w", err)
	}
	if err := b.onTeamBranch(config); err != nil {
		return appTask{}, err
	}
	before := boxGit(b.repo, "rev-parse", "HEAD")
	// undo puts dev back as it was before this step, a take-out with it.
	undo := func() {
		appGit(b.repo, "merge", "--abort")
		appGit(b.repo, "cherry-pick", "--abort")
		appGit(b.repo, "revert", "--abort")
		appGit(b.repo, "reset", "-q", "--hard", before)
	}
	var takenOut *appTask
	if request.TakeOut != "" {
		changes := appDevChanges(b.repo, loadTeamTasks(b.team))
		if len(changes) == 0 || changes[len(changes)-1].ID != request.TakeOut {
			return appTask{}, errors.New("what's on the preview changed since: look at it again")
		}
		last := changes[len(changes)-1]
		if out, err := appGit(b.repo, appGitParent1(b.repo, last.Merged, []string{"revert", "--no-commit"})...); err != nil {
			undo()
			return appTask{}, fmt.Errorf("%q can't be taken out on its own: what was added after it changed the same lines (%s)", last.Title, qFirstLine(out, err.Error()))
		}
		if out, err := appGit(b.repo, "commit", "-q", "--allow-empty", "-m", fmt.Sprintf("Take %q out of dev\n\nFor %s, which changes the same lines.", last.Title, request.Branch)); err != nil {
			undo()
			return appTask{}, fmt.Errorf("git commit: %s", qFirstLine(out, err.Error()))
		}
		takenOut = &last
	}
	from := fmt.Sprintf("Built in the dev box, on its branch %s", request.Branch)
	if request.Pushed {
		from = fmt.Sprintf("From %s, pushed to %s by %s", request.Branch, appForgeName(config.Remote), request.Author)
	}
	message := fmt.Sprintf("%s\n\n%s.", title, from)
	var out string
	if again != nil {
		// Taken out before: its change again, as a commit of its own.
		message = fmt.Sprintf("%s\n\n%s; added again after it was taken out.", title, from)
		if out, err = appGit(b.repo, appGitParent1(b.repo, again.Merged, []string{"cherry-pick", "--no-commit"})...); err == nil {
			out, err = appGit(b.repo, "commit", "-q", "-m", message)
		}
	} else {
		out, err = appGit(b.repo, "merge", "--no-ff", "-m", message, "refs/heads/"+request.ref())
	}
	if err != nil {
		conflicted := boxGit(b.repo, "diff", "--name-only", "--diff-filter=U")
		undo()
		if conflicted == "" {
			return appTask{}, fmt.Errorf("%s doesn't merge into dev: %s", request.Branch, qFirstLine(out, err.Error()))
		}
		return appTask{}, appDevConflictError{Branch: request.Branch, Files: strings.Fields(conflicted), Dev: config.Branch, Pushed: request.Pushed}
	}
	if takenOut != nil {
		takenOut.Status = "discarded"
		saveTeamTask(b.team, *takenOut)
		if versions, err := loadTeamVersions(b.team); err == nil {
			versions.History = append(versions.History, appVersionEvent{What: fmt.Sprintf("took %s out of dev, for %s", takenOut.ID, request.Branch), Approval: &appApproval{Name: firstNonEmpty(request.By, "the dev page")}, At: time.Now().UTC()})
			saveTeamVersions(b.team, versions)
		}
	}
	tasks := loadTeamTasks(b.team)
	n := 1
	if len(tasks) > 0 {
		n = appTaskNumber(tasks[len(tasks)-1].ID) + 1
	}
	now := time.Now().UTC()
	agent := "dev box"
	if request.Pushed {
		agent = fmt.Sprintf("pushed to %s by %s", appForgeName(config.Remote), request.Author)
	}
	task := appTask{ID: fmt.Sprintf("t-%d", n), Role: appDevRole, Agent: agent, Kind: "task", Title: title, Status: "merged",
		Branch: request.ref(), Merged: boxGit(b.repo, "rev-parse", "HEAD"), Created: now, Finished: now, Packages: appDependencyText(packages)}
	if err := saveTeamTask(b.team, task); err != nil {
		return task, err
	}
	if config.Remote != "" {
		if out, err := appGit(b.repo, "push", "-q", "origin", config.Branch); err != nil {
			task.Error = "merged, but git push failed: " + qFirstLine(out, err.Error())
			if boxGit(b.repo, "rev-parse", "--verify", "-q", "refs/heads/"+appPushedRef(config.Branch)) != "" {
				task.Error += fmt.Sprintf(". Someone pushed to %s on %s: add that branch too, then hi can push", config.Branch, appForgeName(config.Remote))
			}
			saveTeamTask(b.team, task)
		}
	}
	return task, nil
}

// appDevSayRequest is a message the dev box posts in the channel.
type appDevSayRequest struct {
	Text   string `json:"text"`
	Thread string `json:"thread,omitempty"`
	// Ops is hi's own post about the box's reach, for the ops channel;
	// the box can't set it.
	Ops bool `json:"-"`
}

func (b *appBroker) devSay(request appDevSayRequest) (string, error) {
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

// appDevStatusView is the team's state, for the dev box.
type appDevStatusView struct {
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

func (b *appBroker) devStatus() (appDevStatusView, error) {
	view, err := appVersionsReport(b.team)
	if err != nil {
		return appDevStatusView{}, err
	}
	status := appDevStatusView{Production: view.Production, Dev: view.Dev, Snapshot: appSnapshotLine(b.team)}
	for _, task := range view.Changes {
		status.Changes = append(status.Changes, fmt.Sprintf("%s %s (%s)", task.ID, task.Title, appTaskAskedBy(task)))
	}
	if slot, ok := loadTeamSlot(b.team, appSlotPreview); ok {
		status.Preview, status.PreviewURL = slot.Label, slot.URL
		// A label says what dev was when it went on; a release since
		// changes what dev is called.
		if config, err := loadTeam(b.team); err == nil {
			switch {
			case strings.HasPrefix(slot.Source, appDevBranchRef("")):
				status.PreviewBranch = strings.TrimPrefix(slot.Source, appDevBranchRef(""))
			case strings.HasPrefix(slot.Source, appPushedPrefix):
				status.PreviewBranch = slot.Source // pushed/<branch>
			case slot.Source == config.Branch && slot.Commit != "" && boxGit(filepath.Join(appDir(b.team), "repo"), "rev-parse", config.Branch) == slot.Commit:
				status.Preview = view.Dev
			case slot.Source == config.Branch:
				status.PreviewOld = true
			}
		}
	}
	if slot, ok := loadTeamSlot(b.team, appSlotProduction); ok {
		status.ProductionURL = slot.URL
	}
	for _, incident := range loadTeamIncidents(b.team) {
		if incident.Status != "closed" {
			status.Incidents = append(status.Incidents, incident.ID+" "+incident.Title)
		}
	}
	status.Jobs = appJobLines(b.team, time.Now())
	return status, nil
}

// listenDev opens the dev box's socket.
func (b *appBroker) listenDev(ctx context.Context) (func() error, error) {
	dir := appDevSocketDir(b.team)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return b.listenWith(ctx, filepath.Join(dir, appDevSocketName), b.devHandler())
}

// devHandler is all the dev box may ask: nothing on it moves production.
func (b *appBroker) devHandler() http.Handler {
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
		return json.NewDecoder(io.LimitReader(r.Body, appMaxAttachments*4/3+1<<20)).Decode(into)
	}
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) {
		status, err := b.devStatus()
		reply(w, status, err)
	})
	mux.HandleFunc("GET /web", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		view, err := b.devWeb(ctx)
		reply(w, view, err)
	})
	mux.HandleFunc("POST /preview", func(w http.ResponseWriter, r *http.Request) {
		var request appDevShipRequest
		if err := decode(r, &request); err != nil {
			reply(w, nil, err)
			return
		}
		if request.Dev {
			state, err := b.preview(appPreviewRequest{})
			reply(w, state, err)
			return
		}
		if request.Pushed {
			state, err := b.previewPushedBranch(request.Branch)
			reply(w, state, err)
			return
		}
		state, err := b.previewDevBranch(request.Branch)
		reply(w, state, err)
	})
	mux.HandleFunc("POST /ship", func(w http.ResponseWriter, r *http.Request) {
		var request appDevShipRequest
		if err := decode(r, &request); err != nil {
			reply(w, nil, err)
			return
		}
		if request.Pushed {
			result, err := b.shipPushedBranch(request)
			reply(w, result, err)
			return
		}
		result, err := b.shipDevBranch(request)
		reply(w, result, err)
	})
	mux.HandleFunc("GET /branches", func(w http.ResponseWriter, r *http.Request) {
		config, err := loadTeam(b.team)
		if err != nil {
			reply(w, nil, err)
			return
		}
		if r.URL.Query().Get("fetch") != "" && config.Remote != "" {
			fetchTeamPushed(config, true)
		}
		reply(w, appPushedBranches(config), nil)
	})
	mux.HandleFunc("POST /say", func(w http.ResponseWriter, r *http.Request) {
		var request appDevSayRequest
		if err := decode(r, &request); err != nil {
			reply(w, nil, err)
			return
		}
		ts, err := b.devSay(request)
		reply(w, map[string]string{"ts": ts}, err)
	})
	mux.HandleFunc("POST /ticket", func(w http.ResponseWriter, r *http.Request) {
		var request appTicketRequest
		if err := decode(r, &request); err != nil {
			reply(w, nil, err)
			return
		}
		if appTicketOwnerActions[request.Action] {
			reply(w, nil, errors.New("comments and files are deleted on the dev page, by an owner"))
			return
		}
		request.Author = "the dev box"
		result, err := appTicketDo(b.team, request)
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
			appDevSiteWaiting(b.team, host)
			reply(w, b.devSiteState(host), nil)
			return
		}
		reply(w, b.devAccessView(), nil)
	})
	mux.HandleFunc("POST /note", func(w http.ResponseWriter, r *http.Request) {
		var request struct{ Workspace, Cwd, Text string }
		if err := decode(r, &request); err != nil {
			reply(w, nil, err)
			return
		}
		work, err := b.devNote(request.Workspace, request.Cwd, request.Text)
		reply(w, map[string]string{"work": work}, err)
	})
	mux.HandleFunc("POST /hook", func(w http.ResponseWriter, r *http.Request) {
		var event appDevHookEvent
		if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&event); err != nil {
			reply(w, nil, err)
			return
		}
		appDevHookRecord(b.team, event, time.Now())
		reply(w, struct{}{}, nil)
	})
	b.devKitRoutes(mux, reply, decode)
	b.devViewRoutes(mux, reply, decode)
	return mux
}

// ---------------------------------------------------------------------------
// hi dev, in the dev box

func runDev(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	return exitCode(appDevClient(args, stdin, stdout), stderr)
}

const appDevUsage = `hi dev is how the dev box works with the rest of the app. In the box:

  hi dev status                     production's and dev's versions, what dev has, the preview,
                                    backups, open incidents, the app's jobs
  hi dev web                        where the people see you and the app: the dev page, the
                                    addresses, what is in front of them, what the Preview couldn't show
  hi dev preview [<branch>]         put the branch (the current one by default) on the app's preview,
                                    with a fresh copy of production's data when it is new work there
  hi dev preview --dev              dev on the preview again, after a branch was tried there
  hi dev ship [<branch>] [-m <title>]  merge the branch into dev, and put dev on the preview;
                                    production moves only when someone releases it
  hi dev branches [--fetch]         the branches people pushed to the app's GitHub or GitLab
                                    project from their own machines; git fetch origin brings
                                    them into the clone as origin/pushed/<branch>
  hi dev preview --pushed <branch>  a pushed branch on the preview, as it was pushed
  hi dev ship --pushed <branch> [-m <title>]
                                    merge a pushed branch into dev, as its author's work
  hi dev say [--thread <ts>] <text|->  post in the app's Slack channel
  hi dev note "<a line>" | --clear  where this workspace's work stands, on the dev page and in
                                    Needs you, until the next ("tests pass; waiting for the API key")
  hi dev allow <site> --why <what for> [--wait]
                                    ask the app's owners to let the box reach a site its proxy
                                    refused; --wait waits up to 9 minutes for their answer
  hi dev allow [<site>]             what the owners allowed, asks waiting, and refusals; or one site
  hi dev task list [--closed]       the app's tasks (GitHub, GitLab, or hi's own)
  hi dev task show <n>
  hi dev task add <title> [<body|->] [--attach <file>]...
  hi dev task comment <n> <text|-> [--attach <file>]...
  hi dev task files <n>             the files attached to a task
  hi dev task edit <n> [--title <title>] <body|->   the task's whole new text
  hi dev task close <n> [<comment>]
  hi dev review [--with claude|codex] [--base <ref>] [<notes|->]
                                    a second opinion on the branch's commits from the other agent
  hi dev deep-review --budget <usd> [--area a,b] [--since v2] [--version v3] [--context <file|->]
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
  hi dev logs [stage|production] [--lines N] [--since 30m] [--grep text]
                                    the app's own output (production by default), its settings masked
  hi dev jobs [stage|production]    the app's jobs and workers: state, progress, last error
  hi dev tools stage|production [<tool> [<json>|-]]
                                    the app's MCP tools (on production, those that only read), or one's answer
  hi dev mcp stage|production       the same as an MCP server over stdio
  hi dev upstream                   for an adopted project: what's new upstream, and its refs in the clone
  hi dev propose <work> <notes|->   offer shipped work back to the adopted project, as a draft pull request
`

func appDevClient(args []string, stdin io.Reader, stdout io.Writer) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprint(stdout, appDevUsage)
		return nil
	}
	if args[0] == "__hook" {
		appDevHookClient(args[1:], stdin, stdout)
		return nil
	}
	// hi dev <command> --help shows the command's usage: never its text,
	// as when hi dev say --help posted "--help" in the channel.
	if len(args) >= 2 && (args[1] == "--help" || args[1] == "-h") {
		if usage := appDevCommandUsage(args[0]); usage != "" {
			fmt.Fprint(stdout, usage)
			return nil
		}
		fmt.Fprint(stdout, appDevUsage)
		return nil
	}
	socket := os.Getenv(appDevSocketEnv)
	if socket == "" {
		return errors.New("hi dev works in an app's dev box; on the machine, use hi app")
	}
	call := func(method, path string, body, out any) error {
		client := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socket)
		}}}
		data, _ := json.Marshal(body)
		request, _ := http.NewRequest(method, "http://team"+path, strings.NewReader(string(data)))
		response, err := client.Do(request)
		if err != nil {
			return errors.New("the app's service isn't answering; it may be restarting, so try again in a minute")
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
	case "logs", "jobs", "mcp", "tools":
		return appDevViewClient(command, rest, stdin, stdout, call, socket)
	case "status":
		var status appDevStatusView
		if err := call("GET", "/status", nil, &status); err != nil {
			return err
		}
		printTeamDevStatus(status, stdout)
		return nil
	case "web":
		var view appDevWebView
		if err := call("GET", "/web", nil, &view); err != nil {
			return err
		}
		printTeamDevWeb(view, time.Now(), stdout)
		return nil
	case "branches":
		path := "/branches"
		switch {
		case len(rest) == 1 && rest[0] == "--fetch":
			path += "?fetch=1"
		case len(rest) > 0:
			return usageError{"usage: hi dev branches [--fetch]"}
		}
		var view appPushedView
		if err := call("GET", path, nil, &view); err != nil {
			return err
		}
		printPushedBranches(view, stdout)
		if len(view.Branches) > 0 {
			fmt.Fprintln(stdout, "git fetch origin brings them into the clone as origin/pushed/<branch>.")
		}
		return nil
	case "preview":
		if len(rest) == 2 && rest[0] == "--pushed" {
			var state appPreview
			if err := call("POST", "/preview", appDevShipRequest{Branch: rest[1], Pushed: true}, &state); err != nil {
				return err
			}
			fmt.Fprintf(stdout, "%s, as pushed, is on the preview: %s\n", rest[1], firstNonEmpty(state.URL, "(no address yet)"))
			if state.Data != "" {
				fmt.Fprintln(stdout, state.Data)
			}
			return nil
		}
		if len(rest) > 1 {
			return usageError{"usage: hi dev preview [<branch> | --dev | --pushed <branch>]"}
		}
		if len(rest) == 1 && rest[0] == "--dev" {
			var state appPreview
			if err := call("POST", "/preview", appDevShipRequest{Dev: true}, &state); err != nil {
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
		var state appPreview
		if err := call("POST", "/preview", appDevShipRequest{Branch: branch}, &state); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "%s is on the preview: %s\n", branch, firstNonEmpty(state.URL, "(no address yet)"))
		if state.Data != "" {
			fmt.Fprintln(stdout, state.Data)
		}
		return nil
	case "ship":
		var request appDevShipRequest
		for i := 0; i < len(rest); i++ {
			switch {
			case (rest[i] == "-m" || rest[i] == "--title") && i+1 < len(rest):
				i++
				request.Title = rest[i]
			case rest[i] == "--pushed":
				request.Pushed = true
			case strings.HasPrefix(rest[i], "-"):
				return usageError{"usage: hi dev ship [<branch> | --pushed <branch>] [-m <title>]"}
			case request.Branch == "":
				request.Branch = rest[i]
			default:
				return usageError{"usage: hi dev ship [<branch>] [-m <title>]"}
			}
		}
		if request.Pushed && request.Branch == "" {
			return usageError{"usage: hi dev ship --pushed <branch> [-m <title>]"}
		}
		if request.Branch == "" {
			var err error
			if request.Branch, err = currentBranch(); err != nil {
				return err
			}
		}
		var result appDevShipResult
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
		if request.Pushed {
			return nil
		}
		fmt.Fprintln(stdout, "Done with this branch? Done ✓ beside its workspace on the dev page (in Workspaces) removes its worktree, workspace, and branch, and closes its task.")
		return nil
	case "say":
		request := appDevSayRequest{}
		if len(rest) >= 2 && rest[0] == "--thread" {
			request.Thread, rest = rest[1], rest[2:]
		}
		text, err := appDevText(rest, stdin)
		if err != nil {
			return err
		}
		request.Text = text
		var answer map[string]string
		if err := call("POST", "/say", request, &answer); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Posted in the app's channel (%s).\n", answer["ts"])
		return nil
	case "note":
		// The agent's line about where its work stands, on the dev page.
		request := struct{ Workspace, Cwd, Text string }{Workspace: os.Getenv("HERDR_WORKSPACE_ID")}
		request.Cwd, _ = os.Getwd()
		if len(rest) == 1 && rest[0] == "--clear" {
			rest = nil
		} else if len(rest) == 0 || len(rest) == 1 && strings.HasPrefix(rest[0], "-") && !strings.Contains(rest[0], " ") {
			return errors.New("say where the work stands, in a line: hi dev note \"tests pass; waiting for the API key\" (--clear takes it away)")
		}
		request.Text = strings.Join(rest, " ")
		var answer map[string]string
		if err := call("POST", "/note", request, &answer); err != nil {
			return err
		}
		if request.Text == "" {
			fmt.Fprintf(stdout, "The note on %s is gone.\n", answer["work"])
		} else {
			fmt.Fprintf(stdout, "%s's note is on the dev page.\n", answer["work"])
		}
		return nil
	case "allow":
		return appDevAllowClient(rest, stdout, call, currentBranch)
	case "task", "tasks", "ticket", "tickets": // ticket: its name before hi 2.5.14
		request, err := parseTeamTicketArgs(rest, stdin)
		if err != nil {
			return err
		}
		var result appTicketResult
		if err := call("POST", "/ticket", request, &result); err != nil {
			return err
		}
		if request.Action == "files" {
			return appSaveFetchedTicketFiles(request.Number, result, stdout)
		}
		printTeamTicketResult(request, result, stdout)
		return nil
	}
	if known, err := appDevKitClient(command, rest, stdin, stdout, call); known {
		return err
	}
	fmt.Fprint(stdout, appDevUsage)
	return usageError{"unknown command hi dev " + command}
}

// appDevText is the words given, or stdin for "-".
// appDevCommandUsage is the lines of hi dev's usage about one command.
func appDevCommandUsage(command string) string {
	var out strings.Builder
	in := false
	for _, line := range strings.Split(appDevUsage, "\n") {
		switch {
		case strings.HasPrefix(line, "  hi dev "):
			fields := strings.Fields(line)
			in = len(fields) >= 3 && fields[2] == command
		case !strings.HasPrefix(line, "    "):
			in = false
		}
		if in {
			out.WriteString(line + "\n")
		}
	}
	return out.String()
}

func appDevText(words []string, stdin io.Reader) (string, error) {
	// An option alone isn't a message: hi dev say --verbose posts nothing.
	if len(words) == 1 && strings.HasPrefix(words[0], "-") && words[0] != "-" && !strings.ContainsAny(words[0], " \n") {
		return "", fmt.Errorf("%s isn't an option here; give the text itself (quoted), or - and the text on stdin", words[0])
	}
	if len(words) == 1 && words[0] == "-" {
		data, err := io.ReadAll(io.LimitReader(stdin, 64<<10))
		return strings.TrimSpace(string(data)), err
	}
	text := strings.TrimSpace(strings.Join(words, " "))
	if text == "" {
		return "", errors.New("nothing to say")
	}
	// A file's path is its text: agents write a long body to a file first.
	if len(words) == 1 && appLooksLikePath(text) {
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

// appLooksLikePath says a word is a file's path and nothing else: one
// line, no spaces, starting with / or ./ or ~/.
func appLooksLikePath(text string) bool {
	return !strings.ContainsAny(text, " \t\n") && (strings.HasPrefix(text, "/") && len(text) > 1 || strings.HasPrefix(text, "./") || strings.HasPrefix(text, "~/"))
}

func printTeamDevStatus(status appDevStatusView, w io.Writer) {
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
