package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
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

// appBrainstormBudgetUSD is the most a team's brainstorm may spend, and
// appBrainstormMax each agent's time.
const (
	appBrainstormBudgetUSD = 10
	appBrainstormMax       = "20m"
)

// appBrainstorm is one brainstorm, in brainstorms.json.
type appBrainstorm struct {
	ID       string       `json:"id"` // hi agent brainstorm's, such as b-2
	Approval *appApproval `json:"approval"`
	Agents   []string     `json:"agents"`
	Question string       `json:"question"`
	Web      bool         `json:"web,omitempty"`
	Commit   string       `json:"commit"`
	// Deliver is where the lead was asked and hears the end, as Hermes
	// says it: slack:<channel>:<thread>.
	Deliver string    `json:"deliver"`
	Status  string    `json:"status"` // running, done, stopped, or failed
	Started time.Time `json:"started"`
	Ended   time.Time `json:"ended,omitzero"`
	Told    bool      `json:"told,omitempty"`
	// Dev says an owner in the dev box asked for it; its ideas go into
	// the box's home.
	Dev bool `json:"dev,omitempty"`
}

// appBrainstormRequest is the lead's hi app brainstorm.
type appBrainstormRequest struct {
	Agents   string `json:"agents,omitempty"`
	Question string `json:"question"`
	Web      bool   `json:"web,omitempty"`
	Deliver  string `json:"deliver,omitempty"`
}

var (
	appBrainstormMu sync.Mutex
	// appBrainstormPoll is how often the broker looks at a running
	// brainstorm; tests shorten it.
	appBrainstormPoll = 30 * time.Second
)

func appBrainstormsDir(team string) string  { return filepath.Join(appDir(team), "brainstorms") }
func appBrainstormsPath(team string) string { return filepath.Join(appDir(team), "brainstorms.json") }

func loadTeamBrainstorms(team string) []appBrainstorm {
	var all []appBrainstorm
	if data, err := os.ReadFile(appBrainstormsPath(team)); err == nil {
		json.Unmarshal(data, &all)
	}
	return all
}

// saveTeamBrainstorm adds or replaces one brainstorm in brainstorms.json.
func saveTeamBrainstorm(team string, record appBrainstorm) error {
	appBrainstormMu.Lock()
	defer appBrainstormMu.Unlock()
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
	return writeFileAtomic(appBrainstormsPath(team), append(data, '\n'), 0o600)
}

// appBrainstormQuestion checks a brainstorm's question.
func appBrainstormQuestion(question string) (string, error) {
	question = strings.TrimSpace(question)
	if question == "" {
		return "", errors.New("write the question for the agents in a file, with what they should know, and pass it: --question <file>")
	}
	if len(question) > brainstormMaxQuestion {
		return "", fmt.Errorf("the question is %d KB; keep it under %d KB", len(question)>>10, brainstormMaxQuestion>>10)
	}
	return question, nil
}

// appBrainstormRunning refuses a second brainstorm while one runs.
func appBrainstormRunning(team string) error {
	for _, earlier := range loadTeamBrainstorms(team) {
		if earlier.Status == "running" {
			return fmt.Errorf("brainstorm %s is still running; wait for it, or stop it with hi app brainstorm stop %s", earlier.ID, earlier.ID)
		}
	}
	return nil
}

// launchBrainstorm starts a brainstorm whose yes was checked, on dev.
func (b *appBroker) launchBrainstorm(config appConfig, request appBrainstormRequest, record appBrainstorm) (appBrainstorm, error) {
	question := record.Question
	if record.Commit = boxGit(b.repo, "rev-parse", "--verify", "-q", config.Branch); record.Commit == "" {
		return appBrainstorm{}, fmt.Errorf("the project has no %s branch yet", config.Branch)
	}
	if err := os.MkdirAll(appBrainstormsDir(b.team), 0o700); err != nil {
		return appBrainstorm{}, err
	}
	questionFile := filepath.Join(appBrainstormsDir(b.team), "question.md")
	contextFile := filepath.Join(appBrainstormsDir(b.team), "context.md")
	data := appSlotDataDir(b.team, appSlotProduction)
	withData := !request.Web && appDirHasFiles(data)
	context := fmt.Sprintf("This is the project of an app that agents build in a Slack channel, for the people in it.\n\nWhat it's for: %s\n", config.Purpose)
	if record.Dev {
		context = fmt.Sprintf("This is the project of the %s app, which its owners build with coding agents.\n\nWhat it's for: %s\n", config.Name, config.Purpose)
	}
	if withData {
		context += "\nThe app's data, as production has it, is in /data/" + filepath.Base(data) + ", read-only. It is the people's: use it to understand them, and don't copy it into what you write beyond a few examples.\n"
	}
	if err := os.WriteFile(questionFile, []byte(question+"\n"), 0o600); err != nil {
		return appBrainstorm{}, err
	}
	if err := os.WriteFile(contextFile, []byte(context), 0o600); err != nil {
		return appBrainstorm{}, err
	}
	args := []string{"agent", "brainstorm", "--question", questionFile, "--context", contextFile, "--name", b.team, "--ref", record.Commit,
		"--budget", fmt.Sprint(appBrainstormBudgetUSD), "--max", appBrainstormMax, "--no-proposal", "--yes", "--detach", "--json"}
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
		return appBrainstorm{}, fmt.Errorf("hi agent brainstorm: %w", err)
	}
	var started brainstorm
	if err := json.Unmarshal(out, &started); err != nil || started.ID == "" {
		return appBrainstorm{}, fmt.Errorf("hi agent brainstorm: %s", qFirstLine(string(out), "no brainstorm started"))
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

// appDirHasFiles says whether a folder has anything in it.
func appDirHasFiles(dir string) bool {
	entries, err := os.ReadDir(dir)
	return err == nil && len(entries) > 0
}

// watchBrainstorm waits for a brainstorm to end, keeps its ideas in
// brainstorms/<id>/, posts every idea in full where it was asked, and wakes
// the lead there to compile them.
func (b *appBroker) watchBrainstorm(id string) {
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
		time.Sleep(appBrainstormPoll)
	}
}

func (b *appBroker) endBrainstorm(id, state string, bs *brainstorm) {
	var record *appBrainstorm
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
	folder := filepath.Join(appBrainstormsDir(b.team), id)
	os.RemoveAll(folder)
	if err := os.MkdirAll(folder, 0o700); err == nil {
		appCopyTree(brainstormStateFile(id, "ideas"), filepath.Join(folder, "ideas"))
		for _, name := range []string{"report.md", "question.md"} {
			if data, err := os.ReadFile(brainstormStateFile(id, name)); err == nil {
				os.WriteFile(filepath.Join(folder, name), data, 0o600)
			}
		}
	}
	// The dev box's own go into its home, for the agent that asked.
	if record.Dev {
		appCopyTree(folder, appDevResultsDir(b.team, "brainstorms", id))
		record.Told = true
		saveTeamBrainstorm(b.team, *record)
		return
	}
	// Every idea in full goes where it was asked, as a file, for the people
	// who want to read them; the lead writes the proposal.
	channel, thread := appDeliverPlace(record.Deliver)
	if b.channel != nil && channel != "" && fileExists(filepath.Join(folder, "report.md")) {
		b.channel.upload(channel, thread, filepath.Join(folder, "report.md"), "Brainstorm "+id+": every idea in full")
	}
	if b.wakeLead != nil {
		if err := b.wakeLead("hi-brainstorm-"+id, appBrainstormPrompt(*record, *bs), record.Deliver); err != nil {
			return
		}
	}
	record.Told = true
	saveTeamBrainstorm(b.team, *record)
}

// appDeliverPlace reads slack:<channel>:<thread>.
func appDeliverPlace(deliver string) (channel, thread string) {
	rest, ok := strings.CutPrefix(deliver, "slack:")
	if !ok {
		return "", ""
	}
	channel, thread, _ = strings.Cut(rest, ":")
	return channel, thread
}

// appCopyTree copies a folder's regular files.
func appCopyTree(from, to string) {
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

// appBrainstormPrompt wakes the lead when a brainstorm ends.
func appBrainstormPrompt(record appBrainstorm, bs brainstorm) string {
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
func (b *appBroker) resumeBrainstorms() {
	for _, record := range loadTeamBrainstorms(b.team) {
		if record.Status == "running" || !record.Told {
			go b.watchBrainstorm(record.ID)
		}
	}
}

func (b *appBroker) stopBrainstorm(id string) (string, error) {
	for _, record := range loadTeamBrainstorms(b.team) {
		if record.ID == id {
			out, err := b.run("agent", "brainstorm", "stop", id)
			return strings.TrimSpace(string(out)), err
		}
	}
	return "", fmt.Errorf("no brainstorm %s; hi app brainstorm list shows them", id)
}

// showBrainstorms is the team's brainstorms, or one brainstorm's state.
func (b *appBroker) showBrainstorms(id string) (string, error) {
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
	return "", fmt.Errorf("no brainstorm %s; hi app brainstorm list shows them", id)
}
