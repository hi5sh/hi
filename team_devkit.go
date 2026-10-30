package main

// The rest of the dev box's kit (docs/specs/approved/hi_team_dev.md,
// release 2b): a second opinion from the other agent, run in the box
// itself; deep reviews and brainstorms, which the broker runs as it does
// for the lead, with their reports put into the box's home instead of
// Slack; the app's data and the preview's settings; and what hi recorded
// about incidents. None of it moves production.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	// teamDevBy is who asked, in the records of what the dev box started:
	// only owners get into the box.
	teamDevBy = "an owner in the dev box"
	// teamDevDeepReviewMaxUSD is the most a deep review from the box may
	// spend; a larger one is for an admin on the machine.
	teamDevDeepReviewMaxUSD = 50
	// teamDevReviewLimit is how long the other agent has for a second
	// opinion.
	teamDevReviewLimit = 30 * time.Minute
)

// teamDevResultsDir is where a deep review's report or a brainstorm's
// ideas go in the box's home: ~/hi/<kind>/<id> in the box.
func teamDevResultsDir(team, kind, id string) string {
	return filepath.Join(teamDevDir(team), "home", "hi", kind, id)
}

func teamDevResultsInBox(kind, id string) string { return "~/hi/" + kind + "/" + id }

// ---------------------------------------------------------------------------
// the broker's side

// devDeepReview starts a deep review an owner asked their agent for, up
// to teamDevDeepReviewMaxUSD; the report goes into the box's home.
func (b *teamBroker) devDeepReview(request teamDeepReviewRequest) (teamDeepReview, error) {
	config, err := loadTeam(b.team)
	if err != nil {
		return teamDeepReview{}, err
	}
	switch {
	case request.Budget <= 0:
		return teamDeepReview{}, errors.New("a deep review needs the budget the owner agreed to: hi dev deep-review --budget 20")
	case request.Budget > teamDevDeepReviewMaxUSD:
		return teamDeepReview{}, fmt.Errorf("a deep review from the dev box may spend up to $%d; for a larger one, ask an admin of the machine to run hi agent review", teamDevDeepReviewMaxUSD)
	}
	if err := teamDeepReviewRunning(b.team); err != nil {
		return teamDeepReview{}, errors.New(strings.ReplaceAll(err.Error(), "hi team review stop", "hi dev deep-review stop"))
	}
	record := teamDeepReview{Approval: &teamApproval{Name: teamDevBy}, Budget: request.Budget, Version: request.Version, Status: "running", Dev: true}
	return b.launchDeepReview(config, request, record)
}

// devBrainstorm starts a brainstorm an owner asked their agent for; the
// ideas go into the box's home, and the agent there writes the proposal.
func (b *teamBroker) devBrainstorm(request teamBrainstormRequest) (teamBrainstorm, error) {
	config, err := loadTeam(b.team)
	if err != nil {
		return teamBrainstorm{}, err
	}
	question, err := teamBrainstormQuestion(request.Question)
	if err != nil {
		return teamBrainstorm{}, errors.New(strings.ReplaceAll(err.Error(), "--question <file>", "hi dev brainstorm <file|->"))
	}
	if err := teamBrainstormRunning(b.team); err != nil {
		return teamBrainstorm{}, errors.New(strings.ReplaceAll(err.Error(), "hi team brainstorm stop", "hi dev brainstorm stop"))
	}
	record := teamBrainstorm{Approval: &teamApproval{Name: teamDevBy}, Question: question, Web: request.Web, Status: "running", Dev: true}
	return b.launchBrainstorm(config, request, record)
}

// devShow is a deep review's or a brainstorm's state, with where its
// results are for the box: in its home when the box asked for it.
func (b *teamBroker) devShow(kind, id string) (string, error) {
	var text string
	var err error
	dev := false
	if kind == "reviews" {
		text, err = b.showDeepReviews(id)
		for _, r := range loadTeamDeepReviews(b.team) {
			dev = dev || (r.ID == id && r.Dev)
		}
	} else {
		text, err = b.showBrainstorms(id)
		for _, r := range loadTeamBrainstorms(b.team) {
			dev = dev || (r.ID == id && r.Dev)
		}
	}
	if err != nil || id == "" {
		return text, err
	}
	where := "on the machine, not in the box: it was asked for in Slack"
	if dev {
		where = teamDevResultsInBox(kind, id) + "/"
	}
	head, _, _ := strings.Cut(text, "\nThe report: /team/")
	head, _, _ = strings.Cut(head, "\nThe ideas: /team/")
	return strings.TrimRight(head, "\n") + "\n\nIts results, once it ends: " + where + "\n", nil
}

// teamDevSettingRequest sets or removes a value of the preview's own.
type teamDevSettingRequest struct {
	Key   string `json:"key"`
	Value string `json:"value,omitempty"`
	Unset bool   `json:"unset,omitempty"`
}

// devSetting changes one of the preview's settings: production's stay as
// they are, and no value is ever read back.
func (b *teamBroker) devSetting(request teamDevSettingRequest) (string, error) {
	key := request.Key
	switch {
	case !teamEnvName.MatchString(key):
		return "", fmt.Errorf("%q isn't a variable name: letters, digits, and _, not starting with a digit", key)
	case teamAppEnvReserved[key]:
		return "", fmt.Errorf("hi sets %s in the app's box itself", key)
	case !request.Unset && request.Value == "":
		return "", fmt.Errorf("no value for %s", key)
	case strings.ContainsAny(request.Value, "\r\n"):
		return "", fmt.Errorf("%s's value has a line break; a setting is one line", key)
	}
	secrets, err := loadTeamSecrets(b.team)
	if err != nil {
		return "", err
	}
	if err := teamSettingCheck(secrets, key, false); err != nil {
		return "", err
	}
	text := fmt.Sprintf("The preview gets its own %s; production keeps what it has.", key)
	if request.Unset {
		if !teamEnvUnset(&secrets, teamSlotPreview, key) {
			return "", fmt.Errorf("the preview has no setting %s", key)
		}
		text = fmt.Sprintf("The preview no longer gets %s; production keeps what it has.", key)
	} else {
		teamEnvSet(&secrets, teamSlotPreview, key, request.Value)
	}
	if err := saveTeamSecrets(b.team, secrets); err != nil {
		return "", err
	}
	return text + " The next preview has it: hi dev preview again to use it now.", nil
}

// teamContainerRunning says whether a box's container runs.
func teamContainerRunning(container string) bool {
	engine, _, err := detectBoxEngine()
	if err != nil {
		return false
	}
	out, err := engine.output("inspect", "-f", "{{.State.Running}}", container)
	return err == nil && strings.TrimSpace(out) == "true"
}

// devKitRoutes are the kit's routes on the dev box's socket.
func (b *teamBroker) devKitRoutes(mux *http.ServeMux, reply func(http.ResponseWriter, any, error), decode func(*http.Request, any) error) {
	text := func(w http.ResponseWriter, value string, err error) {
		reply(w, map[string]string{"text": value}, err)
	}
	type idRequest struct {
		ID string `json:"id"`
	}
	mux.HandleFunc("GET /deep-review", func(w http.ResponseWriter, r *http.Request) {
		value, err := b.devShow("reviews", r.URL.Query().Get("id"))
		text(w, value, err)
	})
	mux.HandleFunc("POST /deep-review", func(w http.ResponseWriter, r *http.Request) {
		var request teamDeepReviewRequest
		if err := decode(r, &request); err != nil {
			reply(w, nil, err)
			return
		}
		record, err := b.devDeepReview(request)
		reply(w, record, err)
	})
	mux.HandleFunc("POST /deep-review/stop", func(w http.ResponseWriter, r *http.Request) {
		var request idRequest
		if err := decode(r, &request); err != nil {
			reply(w, nil, err)
			return
		}
		value, err := b.stopDeepReview(request.ID)
		text(w, value, err)
	})
	mux.HandleFunc("GET /brainstorm", func(w http.ResponseWriter, r *http.Request) {
		value, err := b.devShow("brainstorms", r.URL.Query().Get("id"))
		text(w, value, err)
	})
	mux.HandleFunc("POST /brainstorm", func(w http.ResponseWriter, r *http.Request) {
		var request teamBrainstormRequest
		if err := decode(r, &request); err != nil {
			reply(w, nil, err)
			return
		}
		record, err := b.devBrainstorm(request)
		reply(w, record, err)
	})
	mux.HandleFunc("POST /brainstorm/stop", func(w http.ResponseWriter, r *http.Request) {
		var request idRequest
		if err := decode(r, &request); err != nil {
			reply(w, nil, err)
			return
		}
		value, err := b.stopBrainstorm(request.ID)
		text(w, value, err)
	})
	mux.HandleFunc("GET /agy", func(w http.ResponseWriter, r *http.Request) {
		value, err := b.showAgy(r.URL.Query().Get("id"), true)
		text(w, value, err)
	})
	mux.HandleFunc("POST /agy", func(w http.ResponseWriter, r *http.Request) {
		var request teamAgyRequest
		if err := decode(r, &request); err != nil {
			reply(w, nil, err)
			return
		}
		record, err := b.devAgy(request)
		reply(w, record, err)
	})
	mux.HandleFunc("GET /data", func(w http.ResponseWriter, r *http.Request) {
		folders := teamDataFolders(b.team, teamContainerRunning)
		for i := range folders {
			folders[i].Dir = "" // the machine's paths mean nothing in the box
		}
		reply(w, folders, nil)
	})
	mux.HandleFunc("POST /data/fork", func(w http.ResponseWriter, r *http.Request) {
		result, err := b.copyData(teamDataCopyRequest{From: teamSlotProduction, To: teamSlotPreview})
		result.Kept = ""
		reply(w, result, err)
	})
	mux.HandleFunc("GET /env", func(w http.ResponseWriter, r *http.Request) {
		secrets, err := loadTeamSecrets(b.team)
		reply(w, teamEnvNames(secrets), err)
	})
	mux.HandleFunc("POST /env", func(w http.ResponseWriter, r *http.Request) {
		var request teamDevSettingRequest
		if err := decode(r, &request); err != nil {
			reply(w, nil, err)
			return
		}
		value, err := b.devSetting(request)
		text(w, value, err)
	})
	mux.HandleFunc("GET /upstream", func(w http.ResponseWriter, r *http.Request) {
		view, err := b.upstream()
		reply(w, view, err)
	})
	mux.HandleFunc("POST /propose", func(w http.ResponseWriter, r *http.Request) {
		var request teamProposeRequest
		if err := decode(r, &request); err != nil {
			reply(w, nil, err)
			return
		}
		request.owner = teamDevBy
		work, err := b.propose(request)
		reply(w, work, err)
	})
	mux.HandleFunc("GET /incidents", func(w http.ResponseWriter, r *http.Request) {
		if id := r.URL.Query().Get("id"); id != "" {
			incident, err := loadTeamIncident(b.team, id)
			reply(w, []teamIncident{incident}, err)
			return
		}
		reply(w, teamIncidentsReport(b.team, time.Now()), nil)
	})
}

// ---------------------------------------------------------------------------
// hi dev, in the dev box

type teamDevCall func(method, path string, body, out any) error

// teamDevKitClient runs the kit's commands, and says whether it knew the
// command.
func teamDevKitClient(command string, args []string, stdin io.Reader, stdout io.Writer, call teamDevCall) (bool, error) {
	var answer struct{ Text string }
	show := func(path string) error {
		if err := call("GET", path, nil, &answer); err != nil {
			return err
		}
		fmt.Fprintln(stdout, strings.TrimRight(answer.Text, "\n"))
		return nil
	}
	switch command {
	case "review":
		return true, teamDevReview(args, stdin, stdout)
	case "agy":
		return true, teamAgyClient(args, stdin, stdout, call, true, teamAgyRequest{})
	case "deep-review":
		const usage = "usage: hi dev deep-review --budget <usd> [--area a,b] [--since v2] [--version v3] [--context <file|->]; hi dev deep-review list|show <id>|stop <id>"
		if done, err := teamDevListShowStop("/deep-review", usage, args, call, show, stdout); done {
			return true, err
		}
		var request teamDeepReviewRequest
		for i := 0; i < len(args); i++ {
			if i+1 >= len(args) {
				return true, usageError{usage}
			}
			value := args[i+1]
			switch args[i] {
			case "--budget":
				budget, err := strconv.ParseFloat(strings.TrimPrefix(value, "$"), 64)
				if err != nil || budget <= 0 {
					return true, usageError{fmt.Sprintf("--budget is dollars, such as --budget 20, not %q", value)}
				}
				request.Budget = budget
			case "--area":
				request.Areas = value
			case "--since":
				request.Since = value
			case "--version":
				request.Version = value
			case "--context":
				context, err := teamDevTextOrFile(value, stdin)
				if err != nil {
					return true, err
				}
				request.Context = context
			default:
				return true, usageError{usage}
			}
			i++
		}
		var review teamDeepReview
		if err := call("POST", "/deep-review", request, &review); err != nil {
			return true, err
		}
		fmt.Fprintf(stdout, "Deep review %s started on %s: %s, up to $%s. It changes nothing; it takes a while (often an hour).\n",
			review.ID, teamReviewWhat(review), strings.Join(review.Areas, ", "), strconv.FormatFloat(review.Budget, 'f', -1, 64))
		fmt.Fprintf(stdout, "hi dev deep-review show %s says how far it is; the report goes to %s/report.md when it ends.\n", review.ID, teamDevResultsInBox("reviews", review.ID))
		return true, nil
	case "brainstorm":
		const usage = "usage: hi dev brainstorm [--agents a,b] [--web] <question file|->; hi dev brainstorm list|show <id>|stop <id>"
		if done, err := teamDevListShowStop("/brainstorm", usage, args, call, show, stdout); done {
			return true, err
		}
		var request teamBrainstormRequest
		var question string
		for i := 0; i < len(args); i++ {
			switch {
			case args[i] == "--agents" && i+1 < len(args):
				i++
				request.Agents = args[i]
			case args[i] == "--web":
				request.Web = true
			case question == "" && (args[i] == "-" || !strings.HasPrefix(args[i], "-")):
				question = args[i]
			default:
				return true, usageError{usage}
			}
		}
		if question == "" {
			return true, usageError{usage}
		}
		text, err := teamDevTextOrFile(question, stdin)
		if err != nil {
			return true, err
		}
		request.Question = text
		var record teamBrainstorm
		if err := call("POST", "/brainstorm", request, &record); err != nil {
			return true, err
		}
		fmt.Fprintf(stdout, "Brainstorm %s started with %s, up to $%d. It changes nothing; each agent has up to %s.\n",
			record.ID, strings.Join(record.Agents, ", "), teamBrainstormBudgetUSD, teamBrainstormMax)
		fmt.Fprintf(stdout, "hi dev brainstorm show %s says how far it is; the ideas go to %s/ when it ends.\n", record.ID, teamDevResultsInBox("brainstorms", record.ID))
		return true, nil
	case "data":
		switch {
		case len(args) == 0:
			var folders []teamDataFolder
			if err := call("GET", "/data", nil, &folders); err != nil {
				return true, err
			}
			printTeamDataFolders(stdout, folders)
			return true, nil
		case len(args) == 1 && args[0] == "fork":
			var result teamDataCopy
			if err := call("POST", "/data/fork", nil, &result); err != nil {
				return true, err
			}
			printTeamDataCopy(stdout, result)
			return true, nil
		}
		return true, usageError{"usage: hi dev data [fork]"}
	case "settings", "env":
		const usage = "usage: hi dev settings [KEY=VALUE | KEY (the value from stdin) | --unset KEY]: the preview's settings"
		switch {
		case len(args) == 0:
			var names []string
			if err := call("GET", "/env", nil, &names); err != nil {
				return true, err
			}
			if len(names) == 0 {
				fmt.Fprintln(stdout, "The app gets no settings.")
			} else {
				fmt.Fprintf(stdout, "The app gets these settings as environment variables: %s\n", strings.Join(names, ", "))
			}
			fmt.Fprintln(stdout, "The box sets the preview's own values (hi dev settings KEY=VALUE); production's are an admin's, on the machine. No value is shown.")
			return true, nil
		case len(args) == 2 && args[0] == "--unset":
			return true, teamDevPostSetting(call, stdout, teamDevSettingRequest{Key: args[1], Unset: true})
		case len(args) == 1 && !strings.HasPrefix(args[0], "-"):
			key, value, given := strings.Cut(args[0], "=")
			if !given {
				data, err := io.ReadAll(io.LimitReader(stdin, 64<<10))
				if err != nil {
					return true, err
				}
				value = strings.TrimSpace(string(data))
			}
			return true, teamDevPostSetting(call, stdout, teamDevSettingRequest{Key: key, Value: value})
		}
		return true, usageError{usage}
	case "upstream":
		if len(args) != 0 {
			return true, usageError{"usage: hi dev upstream"}
		}
		var view teamUpstreamView
		if err := call("GET", "/upstream", nil, &view); err != nil {
			return true, err
		}
		printTeamUpstream(view, stdout)
		// Upstream's branches and tags, from the team's repository into
		// this clone, for the agent to merge.
		if repo := boxGit(".", "rev-parse", "--show-toplevel"); repo != "" {
			if out, err := exec.Command("git", "-C", repo, "fetch", "-q", "origin",
				"+refs/remotes/"+teamUpstream+"/*:refs/remotes/"+teamUpstream+"/*", "+refs/tags/"+teamUpstream+"/*:refs/tags/"+teamUpstream+"/*").CombinedOutput(); err != nil {
				fmt.Fprintf(stdout, "Upstream's refs didn't come into this clone: %s\n", strings.TrimSpace(string(out)))
			} else {
				fmt.Fprintf(stdout, "Upstream's branches and tags are in this clone: %s/<branch>, and tags such as %s/v2.4. Merge one on a branch of its own, then hi dev ship.\n", teamUpstream, teamUpstream)
			}
		}
		return true, nil
	case "propose":
		if len(args) != 2 {
			return true, usageError{"usage: hi dev propose <work, such as t-12> <description file|->"}
		}
		notes, err := teamDevTextOrFile(args[1], stdin)
		if err != nil {
			return true, err
		}
		var work teamTask
		if err := call("POST", "/propose", teamProposeRequest{Task: args[0], Notes: notes}, &work); err != nil {
			return true, err
		}
		fmt.Fprintf(stdout, "%s is proposed to the project the team adopted, as a draft pull request: %s\n", work.ID, work.Proposed)
		return true, nil
	case "incidents", "incident":
		if len(args) > 1 {
			return true, usageError{"usage: hi dev incidents [<id>]"}
		}
		path := "/incidents"
		if len(args) == 1 {
			path += "?id=" + url.QueryEscape(args[0])
		}
		var incidents []teamIncident
		if err := call("GET", path, nil, &incidents); err != nil {
			return true, err
		}
		if len(args) == 1 && len(incidents) == 1 {
			printTeamIncident(incidents[0], stdout)
		} else {
			printTeamIncidents(incidents, stdout)
		}
		return true, nil
	}
	return false, nil
}

// teamDevPostSetting posts a setting and prints what hi said.
func teamDevPostSetting(call teamDevCall, stdout io.Writer, request teamDevSettingRequest) error {
	var answer struct{ Text string }
	if err := call("POST", "/env", request, &answer); err != nil {
		return err
	}
	fmt.Fprintln(stdout, answer.Text)
	return nil
}

// teamDevListShowStop handles list, show <id> and stop <id>, and says
// whether it did.
func teamDevListShowStop(path, usage string, args []string, call teamDevCall, show func(string) error, stdout io.Writer) (bool, error) {
	if len(args) == 0 {
		return true, usageError{usage}
	}
	switch args[0] {
	case "list":
		return true, show(path)
	case "show":
		if len(args) != 2 {
			return true, usageError{usage}
		}
		return true, show(path + "?id=" + url.QueryEscape(args[1]))
	case "stop":
		if len(args) != 2 {
			return true, usageError{usage}
		}
		var answer struct{ Text string }
		if err := call("POST", path+"/stop", map[string]string{"id": args[1]}, &answer); err != nil {
			return true, err
		}
		fmt.Fprintln(stdout, answer.Text)
		return true, nil
	}
	return false, nil
}

// teamDevTextOrFile is a file's text, or stdin's for "-".
func teamDevTextOrFile(path string, stdin io.Reader) (string, error) {
	var data []byte
	var err error
	if path == "-" {
		data, err = io.ReadAll(io.LimitReader(stdin, 64<<10))
	} else {
		data, err = os.ReadFile(path)
	}
	return strings.TrimSpace(string(data)), err
}

// ---------------------------------------------------------------------------
// hi dev review: a second opinion from the other agent

// teamDevReviewer runs the reviewing agent in dir with the prompt, and
// returns its report; tests replace it.
var teamDevReviewer = func(agent, dir, prompt string, stderr io.Writer) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), teamDevReviewLimit)
	defer cancel()
	var cmd *exec.Cmd
	out := filepath.Join(dir, ".hi-review.md")
	switch agent {
	case "codex":
		cmd = exec.CommandContext(ctx, "codex", "exec", "--dangerously-bypass-approvals-and-sandbox", "--skip-git-repo-check", "-C", dir, "-o", out, "-")
	default:
		cmd = exec.CommandContext(ctx, "claude", "-p", "--permission-mode", "bypassPermissions", "--output-format", "text")
	}
	cmd.Dir, cmd.Stdin, cmd.Stderr = dir, strings.NewReader(prompt), stderr
	// The reviewer reads and judges; it doesn't ship, post, or file.
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, teamDevSocketEnv+"=") && !strings.HasPrefix(entry, "CLAUDECODE=") && !strings.HasPrefix(entry, "CLAUDE_CODE_ENTRYPOINT=") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	stdout, err := cmd.Output()
	if ctx.Err() != nil {
		return "", fmt.Errorf("%s didn't finish its review in %s", agent, teamDevReviewLimit)
	}
	if err != nil {
		return "", fmt.Errorf("%s: %w", agent, err)
	}
	if agent == "codex" {
		data, err := os.ReadFile(out)
		if err != nil {
			return "", errors.New("codex wrote no review")
		}
		return strings.TrimSpace(string(data)), nil
	}
	return strings.TrimSpace(string(stdout)), nil
}

// teamDevReview is hi dev review [--with claude|codex] [--base <ref>]
// [<notes|->]: the other agent reviews the branch's commits, in a copy of
// its own, and its report is printed and kept in ~/hi/reviews/.
func teamDevReview(args []string, stdin io.Reader, stdout io.Writer) error {
	const usage = "usage: hi dev review [--with claude|codex] [--base <ref>] [<what to look at|->]"
	agent, base := "", ""
	var words []string
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--with" && i+1 < len(args):
			i++
			agent = args[i]
		case args[i] == "--base" && i+1 < len(args):
			i++
			base = args[i]
		case args[i] != "-" && strings.HasPrefix(args[i], "-"):
			return usageError{usage}
		default:
			words = append(words, args[i])
		}
	}
	if agent == "" {
		// The other agent than the one asking: Claude Code sets CLAUDECODE
		// in its commands.
		agent = "claude"
		if os.Getenv("CLAUDECODE") != "" {
			agent = "codex"
		}
	}
	if agent != "claude" && agent != "codex" {
		return usageError{usage}
	}
	if _, err := exec.LookPath(agent); err != nil {
		return fmt.Errorf("%s isn't in the box", agent)
	}
	notes := ""
	if len(words) > 0 {
		var err error
		if notes, err = teamDevText(words, stdin); err != nil {
			return err
		}
	}
	repo := boxGit(".", "rev-parse", "--show-toplevel")
	if repo == "" {
		return errors.New("hi dev review works in the project's clone or one of its worktrees")
	}
	if dirty := boxGit(repo, "status", "--porcelain", "--untracked-files=no"); dirty != "" {
		return errors.New("commit first: the reviewer looks at the branch's commits, in a copy of its own")
	}
	branch := boxGit(repo, "rev-parse", "--abbrev-ref", "HEAD")
	boxGit(repo, "fetch", "-q", "origin")
	dev := strings.TrimPrefix(boxGit(repo, "rev-parse", "--abbrev-ref", "origin/HEAD"), "origin/")
	if dev == "" || dev == "origin/HEAD" {
		dev = "main"
	}
	if base == "" {
		base = boxGit(repo, "merge-base", "HEAD", "origin/"+dev)
	}
	head := boxGit(repo, "rev-parse", "HEAD")
	if base == "" || head == "" {
		return fmt.Errorf("can't tell what the branch adds to dev; name it: hi dev review --base origin/%s", dev)
	}
	commits := boxGit(repo, "rev-list", "--count", base+".."+head)
	if commits == "0" {
		return fmt.Errorf("%s has no commits that dev hasn't; nothing to review", branch)
	}
	home, _ := os.UserHomeDir()
	copyDir, err := os.MkdirTemp(filepath.Join(home, ".cache"), "hi-review-")
	if err != nil {
		if err = os.MkdirAll(filepath.Join(home, ".cache"), 0o700); err == nil {
			copyDir, err = os.MkdirTemp(filepath.Join(home, ".cache"), "hi-review-")
		}
		if err != nil {
			return err
		}
	}
	os.Remove(copyDir)
	if out, err := exec.Command("git", "-C", repo, "worktree", "add", "-q", "--detach", copyDir, head).CombinedOutput(); err != nil {
		return fmt.Errorf("a copy for the reviewer: %s", strings.TrimSpace(string(out)))
	}
	defer exec.Command("git", "-C", repo, "worktree", "remove", "--force", copyDir).Run()

	fmt.Fprintf(stdout, "Asking %s for a second opinion on %s commit(s) of %s; it reads, may run the tests, and changes nothing. It takes a few minutes.\n", agent, commits, branch)
	report, err := teamDevReviewer(agent, copyDir, teamDevReviewPrompt(branch, dev, base, notes), io.Discard)
	if err != nil {
		return err
	}
	if report == "" {
		return fmt.Errorf("%s gave no review", agent)
	}
	dir := filepath.Join(home, "hi", "reviews")
	name := fmt.Sprintf("second-%s-%s.md", strings.ReplaceAll(branch, "/", "-"), time.Now().UTC().Format("20060102-150405"))
	if err := os.MkdirAll(dir, 0o700); err == nil {
		os.WriteFile(filepath.Join(dir, name), []byte(fmt.Sprintf("# %s's review of %s (%s..%s)\n\n%s\n", agent, branch, shortCommit(base), shortCommit(head), report)), 0o600)
	}
	fmt.Fprintf(stdout, "\n%s\n\n(Kept in ~/hi/reviews/%s.)\n", report, name)
	return nil
}

// teamPreviewCheck starts a review's line about what only the running app
// can show, for the owner to try on the preview.
const teamPreviewCheck = "Verify on the preview:"

// teamDevReviewPrompt is what the reviewing agent is asked: the rules a
// team's reviewer follows, for the box.
func teamDevReviewPrompt(branch, dev, base, notes string) string {
	var text strings.Builder
	fmt.Fprintf(&text, "# A second opinion on %s\n\n", branch)
	text.WriteString("You're reviewing work another coding agent did with an owner of the team, in the team's dev box. This folder is a copy of the branch for you alone. Look at what it adds to dev:\n\n")
	fmt.Fprintf(&text, "    git log --oneline %[1]s..HEAD\n    git diff %[1]s..HEAD\n\n", base)
	fmt.Fprintf(&text, "Judge it against the project's specs (docs/specs/), its AGENTS.md or CLAUDE.md, and the code around it on %s. You may run the project's checks (make check, the tests). Don't fix anything and don't commit: say what must change.\n\n", dev)
	text.WriteString("What a review may ask for:\n\n")
	fmt.Fprintf(&text, "- What only the running app can show isn't yours to check: the real service behind a key only the app has, the app's own data, the deployed app. List each as a line starting %q, and approve if the rest is right; the owner tries those on the preview.\n", teamPreviewCheck)
	text.WriteString("- An edge case the work doesn't set out to handle is for later: a line starting \"Later:\", not a change needed, unless it loses data, leaks a secret, breaks what worked, or misses what the work is for.\n")
	text.WriteString("- A check that fails without this work too (a new advisory for a package the work didn't touch, a test that fails now and then on dev) isn't this work's change: say so in a line starting \"Alert:\", and judge the work on the rest.\n\n")
	text.WriteString("Write your review as your last message, in Markdown: start it with \"Approve:\" if it can ship as it is, or with \"Changes needed:\" and a numbered list of each change, with the file and line and why. Then the \"Verify on the preview:\", \"Later:\" and \"Alert:\" lines, if any. Keep it short: the owner and their agent read it.\n")
	if notes = strings.TrimSpace(notes); notes != "" {
		fmt.Fprintf(&text, "\n## What the owner's agent asks you to look at\n\n%s\n", notes)
	}
	return text.String()
}
