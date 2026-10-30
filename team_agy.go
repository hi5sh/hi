package main

// Antigravity for a team: hi dev agy in the dev box, and hi team agy for
// the lead. The broker runs hi agent agy on the machine, with the
// machine's Google sign-in and Gemini API key, which never enter the
// team's boxes: images and web search on the Google AI plan, and video,
// speech, music, transcripts, analysis, and deep research with gemini-api
// when the machine has a key (box_gemini.go). It works in a folder of its
// own, with copies of the files it was given; what it makes goes into the
// dev box's ~/hi/agy/<id>/, or into the thread the lead asked from. It's
// for making things while the app is built: assets for a game, a picture
// for a page, a PDF read, a question researched. Each run is on the plan
// and the key of whoever set the machine up, so the agents ask the person
// first (the hi-dev-agy skill, the lead's skill).

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// teamAgyMaxFiles and teamAgyMaxBytes bound what one run is given.
	teamAgyMaxFiles = 20
	teamAgyMaxBytes = 200 << 20
	// teamAgyUploads is how many of what it made go into the thread.
	teamAgyUploads = 10
)

var (
	teamAgyMu sync.Mutex
	// teamAgyPoll is how often hi dev agy asks how a run is going; tests
	// shorten it.
	teamAgyPoll = 5 * time.Second
)

// teamAgyRequest is a run asked for: the task, and the files it's given:
// paths in the dev box, or the thread's files for the lead.
type teamAgyRequest struct {
	Task   string   `json:"task"`
	Files  []string `json:"files,omitempty"`
	User   string   `json:"user,omitempty"`
	Chat   string   `json:"chat,omitempty"`
	Thread string   `json:"thread,omitempty"`
}

// teamAgy is a run, in agy.json.
type teamAgy struct {
	ID      string    `json:"id"` // a-1, a-2, ...
	Task    string    `json:"task"`
	By      string    `json:"by"`
	Dev     bool      `json:"dev,omitempty"`
	Deliver string    `json:"deliver,omitempty"` // slack:<channel>:<thread>
	Inputs  []string  `json:"inputs,omitempty"`
	Status  string    `json:"status"` // running, done, failed
	Started time.Time `json:"started"`
	Ended   time.Time `json:"ended,omitzero"`
	Summary string    `json:"summary,omitempty"`
	Made    []string  `json:"made,omitempty"` // what it made, relative to its folder
	Error   string    `json:"error,omitempty"`
}

func teamAgyPath(team string) string { return filepath.Join(teamDir(team), "agy.json") }

// teamAgyDir is where the lead finds what a run made, read-only in its box
// as /team/agy.
func teamAgyDir(team string) string { return filepath.Join(teamDir(team), "agy") }

// teamAgyWorkDir is the folder a run works in: hi's state, outside the
// team's repository, so hi agent works in it in place.
func teamAgyWorkDir(team, id string) string {
	return filepath.Join(filepath.Dir(boxStateFile()), "team-agy", team, id)
}

func teamAgyBox(team, id string) string { return team + "-agy-" + strings.TrimPrefix(id, "a-") }

func loadTeamAgys(team string) []teamAgy {
	var all []teamAgy
	if data, err := os.ReadFile(teamAgyPath(team)); err == nil {
		json.Unmarshal(data, &all)
	}
	return all
}

func saveTeamAgy(team string, record teamAgy) error {
	teamAgyMu.Lock()
	defer teamAgyMu.Unlock()
	all := loadTeamAgys(team)
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
	return writeFileAtomic(teamAgyPath(team), append(data, '\n'), 0o600)
}

func findTeamAgy(team, id string) (teamAgy, error) {
	for _, record := range loadTeamAgys(team) {
		if record.ID == id {
			return record, nil
		}
	}
	return teamAgy{}, fmt.Errorf("no Antigravity run %s", id)
}

// teamAgyInput is a file a run is given.
type teamAgyInput struct {
	name string
	data []byte
}

// devAgy starts a run the dev box asked for, with files from its home or
// clone, read through os.Root so a link in the box can't reach past them.
func (b *teamBroker) devAgy(request teamAgyRequest) (teamAgy, error) {
	var inputs []teamAgyInput
	for _, boxPath := range request.Files {
		host, ok := teamDevHostPath(b.team, boxPath)
		if !ok {
			return teamAgy{}, fmt.Errorf("%s isn't in the dev box's home or clone (/box/home, /box/repo)", boxPath)
		}
		base := filepath.Join(teamDevDir(b.team), "home")
		if clean := path.Clean(boxPath); clean == "/box/repo" || strings.HasPrefix(clean, "/box/repo/") {
			base = filepath.Join(teamDevDir(b.team), "repo")
		}
		rel, err := filepath.Rel(base, host)
		if err != nil {
			return teamAgy{}, err
		}
		root, err := os.OpenRoot(base)
		if err != nil {
			return teamAgy{}, err
		}
		data, err := root.ReadFile(rel)
		root.Close()
		if err != nil {
			return teamAgy{}, fmt.Errorf("%s: %w", boxPath, err)
		}
		inputs = append(inputs, teamAgyInput{name: path.Base(boxPath), data: data})
	}
	return b.startAgy(teamAgy{Task: request.Task, By: teamDevBy, Dev: true}, inputs)
}

// leadAgy starts a run the lead asked for, for someone in the thread who
// may ask the team for work; the thread's files go with it.
func (b *teamBroker) leadAgy(request teamAgyRequest) (teamAgy, error) {
	config, err := loadTeam(b.team)
	if err != nil {
		return teamAgy{}, err
	}
	if !slackIDPattern.MatchString(request.User) || !b.teamMayAsk(config, request.User) {
		return teamAgy{}, fmt.Errorf("%s may not ask the team for work; Antigravity runs on the machine's Google plan, for contributors, developers, and owners", firstNonEmpty(request.User, "whoever wrote"))
	}
	var inputs []teamAgyInput
	for _, file := range b.threadFiles(teamTicketRequest{Chat: request.Chat, Thread: request.Thread}) {
		inputs = append(inputs, teamAgyInput{name: file.Name, data: file.Data})
	}
	record := teamAgy{Task: request.Task, By: "<@" + request.User + "> through the lead"}
	if request.Chat != "" && request.Thread != "" {
		record.Deliver = "slack:" + request.Chat + ":" + request.Thread
	}
	return b.startAgy(record, inputs)
}

// startAgy checks the machine has Antigravity, puts the files in a new
// folder, and starts hi agent agy there in the background.
func (b *teamBroker) startAgy(record teamAgy, inputs []teamAgyInput) (teamAgy, error) {
	task := strings.TrimSpace(record.Task)
	switch {
	case task == "":
		return teamAgy{}, errors.New("say what Antigravity should make or find")
	case len(inputs) > teamAgyMaxFiles:
		return teamAgy{}, fmt.Errorf("a run takes up to %d files, not %d", teamAgyMaxFiles, len(inputs))
	}
	if err := agentReady("agy"); err != nil {
		return teamAgy{}, fmt.Errorf("Antigravity isn't set up on the team's machine (%v). Ask your IT team: an admin of the machine runs hi install agy, then agy once to sign in with Google", err)
	}
	for _, earlier := range loadTeamAgys(b.team) {
		if earlier.Status == "running" {
			return teamAgy{}, fmt.Errorf("Antigravity run %s is still going; wait for it (one at a time per team)", earlier.ID)
		}
	}
	config, err := loadTeam(b.team)
	if err != nil {
		return teamAgy{}, err
	}
	record.ID = fmt.Sprintf("a-%d", len(loadTeamAgys(b.team))+1)
	work := teamAgyWorkDir(b.team, record.ID)
	if err := os.MkdirAll(filepath.Join(work, "inputs"), 0o700); err != nil {
		return teamAgy{}, err
	}
	if top := boxGit(work, "rev-parse", "--show-toplevel"); top != "" {
		return teamAgy{}, fmt.Errorf("hi's state folder is inside the git repository %s, so Antigravity can't work in a folder of its own there", top)
	}
	total := 0
	for _, input := range inputs {
		total += len(input.data)
		if total > teamAgyMaxBytes {
			return teamAgy{}, fmt.Errorf("the files come to more than %d MB", teamAgyMaxBytes>>20)
		}
		name := teamAgyFreeName(filepath.Join(work, "inputs"), teamSafeFileName(input.name))
		if err := os.WriteFile(filepath.Join(work, "inputs", name), input.data, 0o600); err != nil {
			return teamAgy{}, err
		}
		record.Inputs = append(record.Inputs, name)
	}
	taskFile := work + ".task.md"
	if err := os.WriteFile(taskFile, []byte(teamAgyTask(config, record, task)), 0o600); err != nil {
		return teamAgy{}, err
	}
	if out, err := b.agyRun(work, "agent", "agy", "--detach", "--json", "--name", teamAgyBox(b.team, record.ID), "--task-file", taskFile); err != nil {
		return teamAgy{}, fmt.Errorf("hi agent agy: %s", qFirstLine(string(out), err.Error()))
	}
	record.Status, record.Started = "running", time.Now().UTC()
	if err := saveTeamAgy(b.team, record); err != nil {
		return record, err
	}
	go b.watchAgy(record.ID)
	return record, nil
}

// teamAgyTask is what Antigravity is asked: the task, for whom, and where
// things go.
func teamAgyTask(config teamConfig, record teamAgy, task string) string {
	var who string
	if record.Dev {
		who = fmt.Sprintf("You work for the developers of the %s team's app (what it's for: %s). They put what you make into the project themselves.", config.Name, config.Purpose)
	} else {
		who = fmt.Sprintf("You work for the people in the %s team's Slack channel (their app is for: %s), through the team's lead. What you make is posted in their thread.", config.Name, config.Purpose)
	}
	text := who + "\n\n# The task\n\n" + task + "\n\n# Where things go\n\n"
	if len(record.Inputs) > 0 {
		text += "The files you were given are in inputs/: " + strings.Join(record.Inputs, ", ") + ". Don't change them.\n"
	}
	text += "Put everything you make in this folder (not in inputs/), with names that say what each is. Make what the task asks; don't write code for the app. Say in your summary what each file is.\n"
	return text
}

// teamAgyFreeName is name, or name-2, name-3, ... when it's taken in dir.
func teamAgyFreeName(dir, name string) string {
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for n := 1; ; n++ {
		candidate := name
		if n > 1 {
			candidate = stem + "-" + strconv.Itoa(n) + ext
		}
		if _, err := os.Lstat(filepath.Join(dir, candidate)); os.IsNotExist(err) {
			return candidate
		}
	}
}

// watchAgy waits for a run, then takes what it made where it was asked
// for, and removes its box.
func (b *teamBroker) watchAgy(id string) {
	record, err := findTeamAgy(b.team, id)
	if err != nil {
		return
	}
	box := teamAgyBox(b.team, id)
	out, waitErr := b.agyRun(teamAgyWorkDir(b.team, id), "agent", "wait", box, "--json")
	var report agentReport
	json.Unmarshal(out, &report)
	b.agyRun(teamAgyWorkDir(b.team, id), "box", "rm", box)
	record.Ended = time.Now().UTC()
	record.Summary = strings.TrimSpace(report.Text)
	var contract struct {
		Status  string `json:"status"`
		Summary string `json:"summary"`
	}
	json.Unmarshal(report.Report, &contract)
	record.Summary = firstNonEmpty(record.Summary, contract.Summary)
	record.Made = teamAgyMade(teamAgyWorkDir(b.team, id))
	record.Status = "done"
	if report.Status == "failed" || contract.Status == "blocked" || (waitErr != nil && report.Status == "") {
		record.Status = "failed"
		record.Error = firstNonEmpty(strings.Join(report.Warnings, "; "), errString(waitErr), contract.Status)
	}
	// Its folder goes where the lead can read it, and into the dev box's
	// home when the box asked.
	folder := filepath.Join(teamAgyDir(b.team), id)
	os.RemoveAll(folder)
	for _, name := range record.Made {
		teamAgyCopy(filepath.Join(teamAgyWorkDir(b.team, id), name), filepath.Join(folder, name))
	}
	if record.Summary != "" {
		os.MkdirAll(folder, 0o700)
		os.WriteFile(filepath.Join(folder, "summary.md"), []byte(record.Summary+"\n"), 0o600)
	}
	if record.Dev {
		teamCopyTree(folder, teamDevResultsDir(b.team, "agy", id))
	}
	saveTeamAgy(b.team, record)
	if !record.Dev {
		b.deliverAgy(record, folder)
	}
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// teamAgyMade lists the files a run made: its folder's, but for its
// inputs and hidden files.
func teamAgyMade(work string) []string {
	var made []string
	filepath.WalkDir(work, func(file string, entry os.DirEntry, err error) error {
		if err != nil || file == work {
			return nil
		}
		rel, _ := filepath.Rel(work, file)
		if strings.HasPrefix(entry.Name(), ".") || rel == "inputs" {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type().IsRegular() {
			made = append(made, filepath.ToSlash(rel))
		}
		return nil
	})
	sort.Strings(made)
	return made
}

func teamAgyCopy(from, to string) error {
	source, err := os.Open(from)
	if err != nil {
		return err
	}
	defer source.Close()
	if err := os.MkdirAll(filepath.Dir(to), 0o700); err != nil {
		return err
	}
	target, err := os.OpenFile(to, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer target.Close()
	_, err = io.Copy(target, source)
	return err
}

// deliverAgy puts what a run for the lead made into the thread, and wakes
// the lead there to say what it is.
func (b *teamBroker) deliverAgy(record teamAgy, folder string) {
	channel, thread := teamDeliverPlace(record.Deliver)
	posted := 0
	if b.channel != nil && channel != "" {
		for _, name := range record.Made {
			if posted == teamAgyUploads {
				break
			}
			if b.channel.upload(channel, thread, filepath.Join(folder, name), name) == nil {
				posted++
			}
		}
	}
	if b.wakeLead == nil || record.Deliver == "" {
		return
	}
	prompt := fmt.Sprintf("Antigravity finished %s, which you asked for in this thread: %q.\n\nStatus: %s. ", record.ID, record.Task, record.Status)
	if record.Error != "" {
		prompt += "What went wrong: " + record.Error + ". "
	}
	if posted > 0 {
		prompt += fmt.Sprintf("hi posted %d of the files it made in the thread. ", posted)
	}
	prompt += "All of it is in /team/agy/" + record.ID + "/ (" + strings.Join(record.Made, ", ") + ").\n\nIts summary:\n\n" + qClip(record.Summary) +
		"\n\nTell the person, in a few lines, what it made and what to look at; if it failed, say so and what they could ask instead."
	b.wakeLead("hi-agy-"+record.ID, prompt, record.Deliver)
}

// showAgy is a run's state, or the latest runs.
func (b *teamBroker) showAgy(id string, inBox bool) (string, error) {
	if id == "" {
		all := loadTeamAgys(b.team)
		if len(all) == 0 {
			return "No Antigravity runs yet.", nil
		}
		var lines []string
		for i := len(all) - 1; i >= 0 && len(lines) < 10; i-- {
			lines = append(lines, fmt.Sprintf("%s  %s  %s  %s", all[i].ID, all[i].Status, all[i].Started.Local().Format("Jan 2 15:04"), qClip(all[i].Task)))
		}
		return strings.Join(lines, "\n"), nil
	}
	record, err := findTeamAgy(b.team, id)
	if err != nil {
		return "", err
	}
	text := fmt.Sprintf("%s: %s, started %s.\nThe task: %s\n", record.ID, record.Status, record.Started.Local().Format("Jan 2 15:04"), record.Task)
	if record.Status == "running" {
		return text, nil
	}
	where := "/team/agy/" + record.ID + "/"
	if inBox {
		where = teamDevResultsInBox("agy", record.ID) + "/"
	}
	if record.Error != "" {
		text += "What went wrong: " + record.Error + "\n"
	}
	if len(record.Made) > 0 {
		text += "It made, in " + where + ": " + strings.Join(record.Made, ", ") + "\n"
	} else {
		text += "It made no files.\n"
	}
	if record.Summary != "" {
		text += "\n" + record.Summary + "\n"
	}
	return text, nil
}

// teamAgyRunIn is how the broker runs hi in a folder; tests replace the
// broker's agyRun.
func teamAgyRunIn(hi string) func(dir string, args ...string) ([]byte, error) {
	return func(dir string, args ...string) ([]byte, error) {
		command := exec.Command(hi, args...)
		command.Dir = dir
		var stderr strings.Builder
		command.Stderr = &stderr
		out, err := command.Output()
		if err != nil {
			return out, fmt.Errorf("%w: %s", err, strings.ReplaceAll(lastLines(strings.TrimSpace(stderr.String()), 3), "\n", " "))
		}
		return out, nil
	}
}

// ---------------------------------------------------------------------------
// hi dev agy and hi team agy

// teamAgyClient runs hi dev agy (dev) or hi team agy: it starts a run and,
// in the dev box, waits for it.
func teamAgyClient(args []string, stdin io.Reader, stdout io.Writer, call teamDevCall, dev bool, lead teamAgyRequest) error {
	usage := "usage: hi team agy \"<what to make or find>\" (the thread's files go with it); hi team agy show [<id>]"
	if dev {
		usage = "usage: hi dev agy \"<what to make or find>\" [--file <path>]...; hi dev agy show [<id>]"
	}
	if len(args) > 0 && (args[0] == "show" || args[0] == "list") {
		id := ""
		if len(args) > 1 {
			id = args[1]
		}
		var answer struct{ Text string }
		if err := call("GET", "/agy?id="+id, nil, &answer); err != nil {
			return err
		}
		fmt.Fprintln(stdout, strings.TrimRight(answer.Text, "\n"))
		return nil
	}
	request := lead
	var task []string
	for i := 0; i < len(args); i++ {
		switch {
		case dev && args[i] == "--file" && i+1 < len(args):
			i++
			file := args[i]
			if !path.IsAbs(file) {
				wd, _ := os.Getwd()
				file = path.Join(wd, file)
			}
			request.Files = append(request.Files, file)
		case args[i] == "-":
			data, err := io.ReadAll(io.LimitReader(stdin, 1<<20))
			if err != nil {
				return err
			}
			task = append(task, string(data))
		case strings.HasPrefix(args[i], "--"):
			return usageError{usage}
		default:
			task = append(task, args[i])
		}
	}
	request.Task = strings.TrimSpace(strings.Join(task, " "))
	if request.Task == "" {
		return usageError{usage}
	}
	var record teamAgy
	if err := call("POST", "/agy", request, &record); err != nil {
		return err
	}
	if !dev {
		fmt.Fprintf(stdout, "Antigravity run %s started. When it ends, hi posts what it made in this thread and wakes you to say what it is; it's also in /team/agy/%s/.\n", record.ID, record.ID)
		return nil
	}
	fmt.Fprintf(stdout, "Antigravity run %s started; waiting for it (Ctrl+C stops waiting, not the run; hi dev agy show %s later).\n", record.ID, record.ID)
	for {
		time.Sleep(teamAgyPoll)
		var answer struct{ Text string }
		if err := call("GET", "/agy?id="+record.ID, nil, &answer); err != nil {
			return err
		}
		if !strings.Contains(strings.SplitN(answer.Text, "\n", 2)[0], ": running,") {
			fmt.Fprintln(stdout, strings.TrimRight(answer.Text, "\n"))
			return nil
		}
	}
}
