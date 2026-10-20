package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// A team's brainstorm is hi agent brainstorm on the team's project, which
// the lead asks the people about first: it names the agents, and starts
// on a yes. The agents' ideas come back to the lead, which compiles them
// into one proposal for the people, in the thread or DM it was asked in.
// See docs/specs/approved/hi_agent_brainstorm.md.

// teamBrainstormBudgetUSD is the most a team's brainstorm may spend, and
// teamBrainstormMax each agent's time.
const (
	teamBrainstormBudgetUSD = 10
	teamBrainstormMax       = "20m"
)

// teamBrainstorm is one brainstorm, in brainstorms.json.
type teamBrainstorm struct {
	ID       string        `json:"id"` // hi agent brainstorm's, such as b-2
	Approval *teamApproval `json:"approval"`
	Agents   []string      `json:"agents"`
	Question string        `json:"question"`
	Web      bool          `json:"web,omitempty"`
	Commit   string        `json:"commit"`
	// Deliver is where the lead was asked and hears the end, as Hermes
	// says it: slack:<channel>:<thread>.
	Deliver string    `json:"deliver"`
	Status  string    `json:"status"` // running, done, stopped, or failed
	Started time.Time `json:"started"`
	Ended   time.Time `json:"ended,omitzero"`
	Told    bool      `json:"told,omitempty"`
}

// teamBrainstormRequest is the lead's hi team brainstorm.
type teamBrainstormRequest struct {
	Approval string `json:"approval"`
	Agents   string `json:"agents,omitempty"`
	Question string `json:"question"`
	Web      bool   `json:"web,omitempty"`
	Deliver  string `json:"deliver,omitempty"`
}

var (
	teamBrainstormMu sync.Mutex
	// teamBrainstormPoll is how often the broker looks at a running
	// brainstorm; tests shorten it.
	teamBrainstormPoll = 30 * time.Second
)

func teamBrainstormsDir(team string) string  { return filepath.Join(teamDir(team), "brainstorms") }
func teamBrainstormsPath(team string) string { return filepath.Join(teamDir(team), "brainstorms.json") }

func loadTeamBrainstorms(team string) []teamBrainstorm {
	var all []teamBrainstorm
	if data, err := os.ReadFile(teamBrainstormsPath(team)); err == nil {
		json.Unmarshal(data, &all)
	}
	return all
}

// saveTeamBrainstorm adds or replaces one brainstorm in brainstorms.json.
func saveTeamBrainstorm(team string, record teamBrainstorm) error {
	teamBrainstormMu.Lock()
	defer teamBrainstormMu.Unlock()
	all := loadTeamBrainstorms(team)
	replaced := false
	for i := range all {
		if all[i].ID == record.ID {
			all[i], replaced = record, true
		}
	}
	if !replaced {
		all = append(all, record)
	}
	data, _ := json.MarshalIndent(all, "", "  ")
	return writeFileAtomic(teamBrainstormsPath(team), append(data, '\n'), 0o600)
}

// brainstormAgents says who would take part by default: the agents ready
// on this machine, for the lead to name when it asks.
func (b *teamBroker) brainstormAgents() string {
	names, _, models, left, err := parseBrainstormAgents("")
	if err != nil {
		return "No agent is ready on this machine for a brainstorm: " + err.Error()
	}
	var ready []string
	for i, name := range names {
		if name == "gemini" {
			name += " (" + models[i] + ")"
		}
		ready = append(ready, name)
	}
	text := "Ready to take part: " + strings.Join(ready, ", ") + "."
	if len(left) > 0 {
		text += " Not set up on this machine: " + strings.Join(left, ", ") + "; whoever runs hi here can set them up (hi install codex, hi install hermes)."
	}
	return text
}

// startBrainstorm checks the yes and starts hi agent brainstorm on dev,
// with the proposal left to the lead.
func (b *teamBroker) startBrainstorm(request teamBrainstormRequest) (teamBrainstorm, error) {
	config, err := loadTeam(b.team)
	if err != nil {
		return teamBrainstorm{}, err
	}
	question := strings.TrimSpace(request.Question)
	if question == "" {
		return teamBrainstorm{}, errors.New("write the question for the agents in a file, with what they should know, and pass it: --question <file>")
	}
	if len(question) > brainstormMaxQuestion {
		return teamBrainstorm{}, fmt.Errorf("the question is %d KB; keep it under %d KB", len(question)>>10, brainstormMaxQuestion>>10)
	}
	for _, earlier := range loadTeamBrainstorms(b.team) {
		if earlier.Status == "running" {
			return teamBrainstorm{}, fmt.Errorf("brainstorm %s is still running; wait for it, or stop it with hi team brainstorm stop %s", earlier.ID, earlier.ID)
		}
	}
	approval, err := b.teamFindApproval(config, request.Approval, "brainstorm")
	if err != nil {
		return teamBrainstorm{}, err
	}
	record := teamBrainstorm{Approval: &approval, Question: question, Web: request.Web, Status: "running"}
	if record.Commit = boxGit(b.repo, "rev-parse", "--verify", "-q", config.Branch); record.Commit == "" {
		return teamBrainstorm{}, fmt.Errorf("the project has no %s branch yet", config.Branch)
	}
	record.Deliver = request.Deliver
	if !strings.HasPrefix(record.Deliver, "slack:") {
		record.Deliver = "slack:" + config.Channel
		if thread := teamApprovalSlackThread(approval); thread != "" {
			record.Deliver += ":" + thread
		}
	}

	if err := os.MkdirAll(teamBrainstormsDir(b.team), 0o700); err != nil {
		return teamBrainstorm{}, err
	}
	questionFile := filepath.Join(teamBrainstormsDir(b.team), "question.md")
	contextFile := filepath.Join(teamBrainstormsDir(b.team), "context.md")
	data := teamSlotDataDir(b.team, teamSlotProduction)
	withData := !request.Web && teamDirHasFiles(data)
	context := fmt.Sprintf("This is the project of a team of agents in a Slack channel, for the people in it.\n\nWhat it's for: %s\n", config.Purpose)
	if withData {
		context += "\nThe app's data, as production has it, is in /data/" + filepath.Base(data) + ", read-only. It is the people's: use it to understand them, and don't copy it into what you write beyond a few examples.\n"
	}
	if err := os.WriteFile(questionFile, []byte(question+"\n"), 0o600); err != nil {
		return teamBrainstorm{}, err
	}
	if err := os.WriteFile(contextFile, []byte(context), 0o600); err != nil {
		return teamBrainstorm{}, err
	}
	args := []string{"agent", "brainstorm", "--question", questionFile, "--context", contextFile, "--name", b.team, "--ref", record.Commit,
		"--budget", fmt.Sprint(teamBrainstormBudgetUSD), "--max", teamBrainstormMax, "--no-proposal", "--yes", "--detach", "--json"}
	if request.Agents != "" {
		args = append(args, "--agents", request.Agents)
	}
	if request.Web {
		args = append(args, "--web")
	} else if withData {
		args = append(args, "--data", data)
	}
	out, err := b.run(args...)
	if err != nil {
		return teamBrainstorm{}, fmt.Errorf("hi agent brainstorm: %w", err)
	}
	var started brainstorm
	if err := json.Unmarshal(out, &started); err != nil || started.ID == "" {
		return teamBrainstorm{}, fmt.Errorf("hi agent brainstorm: %s", qFirstLine(string(out), "no brainstorm started"))
	}
	record.ID, record.Started = started.ID, time.Now().UTC()
	for _, run := range started.Runs {
		if run.Kind == "idea" {
			record.Agents = append(record.Agents, brainstormRunLabel(run))
		}
	}
	if err := saveTeamBrainstorm(b.team, record); err != nil {
		return record, err
	}
	go b.watchBrainstorm(record.ID)
	return record, nil
}

// teamDirHasFiles says whether a folder has anything in it.
func teamDirHasFiles(dir string) bool {
	entries, err := os.ReadDir(dir)
	return err == nil && len(entries) > 0
}

// watchBrainstorm waits for a brainstorm to end, keeps its ideas in
// brainstorms/<id>/, posts every idea in full where it was asked, and wakes
// the lead there to compile them.
func (b *teamBroker) watchBrainstorm(id string) {
	for {
		bs, err := loadBrainstorm(id)
		if err != nil {
			b.endBrainstorm(id, "failed", nil)
			return
		}
		if bs.State != "running" {
			b.endBrainstorm(id, bs.State, &bs)
			return
		}
		time.Sleep(teamBrainstormPoll)
	}
}

func (b *teamBroker) endBrainstorm(id, state string, bs *brainstorm) {
	var record *teamBrainstorm
	for _, r := range loadTeamBrainstorms(b.team) {
		if r.ID == id {
			record = &r
		}
	}
	if record == nil || record.Told {
		return
	}
	record.Status, record.Ended = state, time.Now().UTC()
	record.Told = bs == nil
	saveTeamBrainstorm(b.team, *record)
	if bs == nil {
		return
	}
	// The ideas stay in the team's folder, read-only in the lead's box.
	folder := filepath.Join(teamBrainstormsDir(b.team), id)
	os.RemoveAll(folder)
	if err := os.MkdirAll(folder, 0o700); err == nil {
		teamCopyTree(brainstormStateFile(id, "ideas"), filepath.Join(folder, "ideas"))
		for _, name := range []string{"report.md", "question.md"} {
			if data, err := os.ReadFile(brainstormStateFile(id, name)); err == nil {
				os.WriteFile(filepath.Join(folder, name), data, 0o600)
			}
		}
	}
	// Every idea in full goes where it was asked, as a file, for the people
	// who want to read them; the lead writes the proposal.
	channel, thread := teamDeliverPlace(record.Deliver)
	if b.channel != nil && channel != "" && fileExists(filepath.Join(folder, "report.md")) {
		b.channel.upload(channel, thread, filepath.Join(folder, "report.md"), "Brainstorm "+id+": every idea in full")
	}
	if b.wakeLead != nil {
		if err := b.wakeLead("hi-brainstorm-"+id, teamBrainstormPrompt(*record, *bs), record.Deliver); err != nil {
			return
		}
	}
	record.Told = true
	saveTeamBrainstorm(b.team, *record)
}

// teamDeliverPlace reads slack:<channel>:<thread>.
func teamDeliverPlace(deliver string) (channel, thread string) {
	rest, ok := strings.CutPrefix(deliver, "slack:")
	if !ok {
		return "", ""
	}
	channel, thread, _ = strings.Cut(rest, ":")
	return channel, thread
}

// teamCopyTree copies a folder's regular files.
func teamCopyTree(from, to string) {
	filepath.WalkDir(from, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !entry.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(from, path)
		if err != nil {
			return nil
		}
		if data, err := os.ReadFile(path); err == nil {
			target := filepath.Join(to, rel)
			if os.MkdirAll(filepath.Dir(target), 0o700) == nil {
				os.WriteFile(target, data, 0o600)
			}
		}
		return nil
	})
}

// teamBrainstormPrompt wakes the lead when a brainstorm ends.
func teamBrainstormPrompt(record teamBrainstorm, bs brainstorm) string {
	var ideas, missing []string
	for _, run := range bs.Runs {
		if run.Kind != "idea" {
			continue
		}
		if run.State == "done" {
			line := fmt.Sprintf("- %s: /team/brainstorms/%s/ideas/%s/response.md", brainstormRunLabel(run), bs.ID, run.ID)
			if len(run.Artifacts) > 0 {
				line += fmt.Sprintf(", with artifacts in /team/brainstorms/%s/ideas/%s/artifacts/ (%s)", bs.ID, run.ID, strings.Join(run.Artifacts, ", "))
			}
			ideas = append(ideas, line)
		} else {
			missing = append(missing, fmt.Sprintf("%s (%s)", run.Name, describeBrainstormRun(run)))
		}
	}
	ended := fmt.Sprintf("It has ended with %s.", plural(len(ideas), "idea"))
	if len(missing) > 0 {
		ended += " No idea came from " + strings.Join(missing, ", ") + "."
	}
	if len(ideas) == 0 {
		return fmt.Sprintf(`This is hi, not a person. The brainstorm %s that %s agreed to is over, and no agent wrote an idea: %s. Tell the people here, in a line or two, and offer to answer the question yourself or to try again.`,
			bs.ID, firstNonEmpty(record.Approval.Name, "they"), strings.Join(missing, ", "))
	}
	return fmt.Sprintf(`This is hi, not a person. The brainstorm %[1]s that %[2]s agreed to is over. %[3]s hi posted every idea in full here, as a file.

The question was /team/brainstorms/%[1]s/question.md. The ideas, each written alone without seeing the others:

%[4]s

Read every idea and look at its artifacts, then compile them into one coherent proposal, and post it here for the people, in their words:

- The proposal: what you'd do, in a few sentences anyone can follow.
- Where the ideas agree, and the best of each, naming the agent it came from (codex, claude, gemini).
- Where they differ, which way you'd go, and why.
- The first steps, and the questions only the people can answer.

It's a proposal: nothing is built until they agree, and work on it starts as usual, with an acceptance list and their yes.`,
		bs.ID, firstNonEmpty(record.Approval.Name, "they"), ended, strings.Join(ideas, "\n"))
}

// resumeBrainstorms watches the brainstorms that were running, or ended
// without the lead hearing, when the broker stopped.
func (b *teamBroker) resumeBrainstorms() {
	for _, record := range loadTeamBrainstorms(b.team) {
		if record.Status == "running" || !record.Told {
			go b.watchBrainstorm(record.ID)
		}
	}
}

func (b *teamBroker) stopBrainstorm(id string) (string, error) {
	for _, record := range loadTeamBrainstorms(b.team) {
		if record.ID == id {
			out, err := b.run("agent", "brainstorm", "stop", id)
			return strings.TrimSpace(string(out)), err
		}
	}
	return "", fmt.Errorf("no brainstorm %s; hi team brainstorm list shows them", id)
}

// showBrainstorms is the team's brainstorms, or one brainstorm's state.
func (b *teamBroker) showBrainstorms(id string) (string, error) {
	var out bytes.Buffer
	all := loadTeamBrainstorms(b.team)
	if id == "" {
		if len(all) == 0 {
			return "No brainstorms yet.", nil
		}
		for _, r := range all {
			fmt.Fprintf(&out, "%s  %-8s %s, agreed by %s on %s: %s\n", r.ID, r.Status, strings.Join(r.Agents, ", "), r.Approval.Name, r.Started.Format("2006-01-02"), reviewLine(r.Question, 60))
		}
		return strings.TrimRight(out.String(), "\n"), nil
	}
	for _, r := range all {
		if r.ID == id {
			bs, err := loadBrainstorm(id)
			if err != nil {
				return "", err
			}
			printBrainstormSummary(bs, &out)
			fmt.Fprintf(&out, "\nThe ideas: /team/brainstorms/%s/, once it ends.\n", id)
			return out.String(), nil
		}
	}
	return "", fmt.Errorf("no brainstorm %s; hi team brainstorm list shows them", id)
}

// runTeamBrainstormCommand is the lead's hi team brainstorm, in its box.
func runTeamBrainstormCommand(args []string, stdin io.Reader, stdout io.Writer, call func(method, path string, body any, out any) error) error {
	const usage = "usage: hi team brainstorm --approval <id> --question <file|-> [--agents codex,claude,gemini] [--web]; hi team brainstorm agents|list; hi team brainstorm show|stop <id>"
	var text struct{ Text string }
	if len(args) > 0 {
		switch args[0] {
		case "agents", "list":
			if len(args) != 1 {
				return usageError{usage}
			}
			path := "/brainstorm"
			if args[0] == "agents" {
				path = "/brainstorm/agents"
			}
			if err := call("GET", path, nil, &text); err != nil {
				return err
			}
			fmt.Fprintln(stdout, text.Text)
			return nil
		case "show", "stop":
			if len(args) != 2 {
				return usageError{usage}
			}
			if args[0] == "show" {
				if err := call("GET", "/brainstorm?id="+url.QueryEscape(args[1]), nil, &text); err != nil {
					return err
				}
			} else if err := call("POST", "/brainstorm/stop", teamRequest{Task: args[1]}, &text); err != nil {
				return err
			}
			fmt.Fprintln(stdout, text.Text)
			return nil
		}
	}
	var request teamBrainstormRequest
	for i := 0; i < len(args); i++ {
		value := func() (string, error) {
			if i+1 >= len(args) {
				return "", usageError{usage}
			}
			i++
			return args[i], nil
		}
		var err error
		switch args[i] {
		case "--approval":
			request.Approval, err = value()
		case "--agents":
			request.Agents, err = value()
		case "--web":
			request.Web = true
		case "--question":
			var path string
			if path, err = value(); err == nil {
				request.Question, err = teamReadBrief(path, stdin)
			}
		default:
			return usageError{usage}
		}
		if err != nil {
			return err
		}
	}
	if request.Approval == "" || strings.TrimSpace(request.Question) == "" {
		return usageError{usage}
	}
	// The lead hears the end where it was asked: Hermes says where.
	if chat := os.Getenv("HERMES_SESSION_CHAT_ID"); chat != "" {
		request.Deliver = "slack:" + chat
		if thread := os.Getenv("HERMES_SESSION_THREAD_ID"); thread != "" {
			request.Deliver += ":" + thread
		}
	}
	var record teamBrainstorm
	if err := call("POST", "/brainstorm", request, &record); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Brainstorm %s started with %s, each alone, on dev (%s): up to %s each, at most $%d in all. It changes nothing. When it ends, hi posts every idea in full here and wakes you here to compile them into one proposal for the people. Tell them it started and roughly when to expect it, then end your turn.\n",
		record.ID, strings.Join(record.Agents, ", "), shortCommit(record.Commit), teamBrainstormMax, teamBrainstormBudgetUSD)
	return nil
}
