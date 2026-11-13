package cli

// The admin page's long actions (hi_admin.md, How it works): each runs
// the hi command itself, in a user unit of its own (systemd-run --user),
// so it outlives the page: hi update restarts the page, and the job goes
// on. The job's file says what runs and how it ended; its log is what the
// command printed. When it ends, the app's ops channel hears who did what.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/slack-go/slack"
)

// adminJob is jobs/<id>.json.
type adminJob struct {
	ID    string   `json:"id"`
	Title string   `json:"title"`
	App   string   `json:"app,omitempty"`
	Args  []string `json:"args"`
	By    string   `json:"by"`
	// Said and Tried are what the app's channel hears when it ends: "Olga
	// restarted the app", or "Olga tried to restart the app"; Main says
	// the main channel, not the ops channel.
	Said    string    `json:"said,omitempty"`
	Tried   string    `json:"tried,omitempty"`
	Main    bool      `json:"main,omitempty"`
	Started time.Time `json:"started"`
	Ended   time.Time `json:"ended,omitzero"`
	State   string    `json:"state"` // running, done, or failed
	Code    int       `json:"code,omitempty"`
	Error   string    `json:"error,omitempty"`
	// Then are more hi commands to run after Args, each when the one
	// before it went well.
	Then [][]string `json:"then,omitempty"`
	// Env is a 0600 file of secrets for the commands, as environment
	// variables, which the runner reads and deletes at once; Remove are
	// files the runner deletes when the job ends; Draft is the new app's
	// draft (admin_new.go), dropped once the job went well.
	Env    string   `json:"env,omitempty"`
	Remove []string `json:"remove,omitempty"`
	Draft  string   `json:"draft,omitempty"`
}

func adminJobsDir() string             { return filepath.Join(adminDir(), "jobs") }
func adminJobPath(id string) string    { return filepath.Join(adminJobsDir(), id+".json") }
func adminJobLogPath(id string) string { return filepath.Join(adminJobsDir(), id+".log") }
func adminJobUnit(id string) string    { return "hi-admin-job-" + id + ".service" }

// adminJobKeep is how long a job that ended stays in Jobs.
const adminJobKeep = 14 * 24 * time.Hour

func loadAdminJob(id string) (adminJob, error) {
	if !adminJobID.MatchString(id) {
		return adminJob{}, fmt.Errorf("no job %q", id)
	}
	var job adminJob
	data, err := os.ReadFile(adminJobPath(id))
	if err != nil {
		return job, fmt.Errorf("no job %s", id)
	}
	return job, json.Unmarshal(data, &job)
}

func saveAdminJob(job adminJob) error {
	if err := os.MkdirAll(adminJobsDir(), 0o700); err != nil {
		return err
	}
	data, _ := json.MarshalIndent(job, "", "  ")
	return writeFileAtomic(adminJobPath(job.ID), append(data, '\n'), 0o600)
}

// adminLaunch starts a job's runner in a user unit of its own; tests
// replace it.
var adminLaunch = func(job adminJob, hi string) error {
	args := []string{"--user", "--unit=" + adminJobUnit(job.ID), "--collect", "--quiet",
		"--setenv=" + adminByEnv + "=" + job.By, "--setenv=PATH=" + os.Getenv("PATH")}
	for _, name := range []string{"HI_APPS_DIR", "HI_ADMIN_DIR"} {
		if value := os.Getenv(name); value != "" {
			args = append(args, "--setenv="+name+"="+value)
		}
	}
	args = append(args, hi, "server", "__job", job.ID)
	out, err := exec.Command("systemd-run", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemd-run: %s", qFirstLine(strings.TrimSpace(string(out)), err.Error()))
	}
	return nil
}

// adminJobAlive says a job's unit still runs; tests replace it.
var adminJobAlive = func(id string) bool {
	out, _ := appSystemctl("is-active", adminJobUnit(id))
	return out == "active" || out == "activating"
}

// adminByEnv tells a command the admin page runs who pressed the button,
// for what it records and says (appActor).
const adminByEnv = "HI_ADMIN_BY"

// appActor is who a command on the machine acts for: the admin who
// pressed the button, when the admin page runs it, or the account's user.
func appActor() string {
	if by := strings.TrimSpace(os.Getenv(adminByEnv)); by != "" {
		return by + " on the admin page"
	}
	return firstNonEmpty(os.Getenv("USER"), "an admin") + " on the machine"
}

// startAdminJob records a job and starts it.
func (s *adminServer) startJob(job adminJob) (adminJob, error) {
	suffix, err := adminNewKey()
	if err != nil {
		return job, err
	}
	job.ID = time.Now().UTC().Format("20060102-150405") + "-" + strings.ToLower(strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, suffix))[:6]
	job.Started, job.State = time.Now().UTC(), "running"
	if err := saveAdminJob(job); err != nil {
		return job, err
	}
	if err := adminLaunch(job, s.hi); err != nil {
		job.State, job.Error, job.Ended = "failed", err.Error(), time.Now().UTC()
		saveAdminJob(job)
		return job, err
	}
	adminRecord(job.By, job.App, strings.ToLower(job.Title[:1])+job.Title[1:])
	return job, nil
}

// adminJobs are the jobs, newest first; a job whose unit stopped without
// saying how it ended failed. Those that ended long ago go.
func adminJobs() []adminJob {
	entries, _ := os.ReadDir(adminJobsDir())
	jobs := []adminJob{}
	for _, entry := range entries {
		id, ok := strings.CutSuffix(entry.Name(), ".json")
		if !ok {
			continue
		}
		job, err := loadAdminJob(id)
		if err != nil {
			continue
		}
		if job.State == "running" && time.Since(job.Started) > 10*time.Second && !adminJobAlive(job.ID) {
			// Read again: it may have ended just now.
			if again, err := loadAdminJob(id); err == nil && again.State == "running" {
				job.State, job.Ended, job.Error = "failed", time.Now().UTC(), "it stopped without saying how it ended"
				saveAdminJob(job)
			} else if err == nil {
				job = again
			}
		}
		if job.State != "running" && time.Since(job.Ended) > adminJobKeep {
			os.Remove(adminJobPath(job.ID))
			os.Remove(adminJobLogPath(job.ID))
			continue
		}
		jobs = append(jobs, job)
	}
	sort.Slice(jobs, func(i, j int) bool { return jobs[i].Started.After(jobs[j].Started) })
	return jobs
}

// adminJobOutput is a job's log from offset on, at most 256 KiB of it.
func adminJobOutput(id string, from int64) (string, int64) {
	file, err := os.Open(adminJobLogPath(id))
	if err != nil {
		return "", from
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || from >= info.Size() {
		return "", info.Size()
	}
	if from < 0 || info.Size()-from > 256<<10 {
		from = max(0, info.Size()-256<<10)
	}
	file.Seek(from, io.SeekStart)
	data, _ := io.ReadAll(io.LimitReader(file, 256<<10))
	return string(data), from + int64(len(data))
}

// adminJobMain is hi server __job <id>: the job's runner, in its unit. It
// runs the command with its output in the job's log, and records how it
// ended.
func adminJobMain(args []string, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "usage: hi server __job <id>")
		return 2
	}
	job, err := loadAdminJob(args[0])
	if err != nil {
		fmt.Fprintf(stderr, "hi: %v\n", err)
		return 1
	}
	log, err := os.OpenFile(adminJobLogPath(job.ID), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		fmt.Fprintf(stderr, "hi: %v\n", err)
		return 1
	}
	defer log.Close()
	env := append(os.Environ(), adminByEnv+"="+job.By)
	if job.Env != "" {
		// The secrets are read once, and the file goes.
		data, readErr := os.ReadFile(job.Env)
		os.Remove(job.Env)
		var secrets map[string]string
		if readErr == nil && json.Unmarshal(data, &secrets) == nil {
			for name, value := range secrets {
				env = append(env, name+"="+value)
			}
		}
	}
	defer func() {
		for _, path := range job.Remove {
			os.Remove(path)
		}
	}()
	executable, err := adminExecutable()
	if err == nil {
		// A job that installs what needs root: its sudo asks the page.
		var done func()
		if env, done, err = adminJobSudo(env, executable); err == nil {
			defer done()
		}
	}
	for _, args := range append([][]string{job.Args}, job.Then...) {
		if err != nil {
			break
		}
		fmt.Fprintf(log, "$ hi %s\n", strings.Join(adminQuoteArgs(args), " "))
		err = adminRunCommand(executable, args, env, log)
	}
	job.Ended, job.State = time.Now().UTC(), "done"
	if err != nil {
		job.State, job.Error = "failed", err.Error()
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			job.Code = exit.ExitCode()
		}
		fmt.Fprintf(log, "\nhi: %v\n", err)
	}
	if err == nil && job.Draft != "" && appNamePattern.MatchString(job.Draft) {
		os.Remove(adminDraftPath(job.Draft))
	}
	saveAdminJob(job)
	adminTellApp(job)
	return 0
}

// adminRunCommand runs one of a job's hi commands; tests replace it.
var adminRunCommand = func(executable string, args, env []string, out io.Writer) error {
	command := exec.Command(executable, args...)
	command.Stdout, command.Stderr = out, out
	command.Env = env
	return command.Run()
}

// adminQuoteArgs quotes the arguments a shell would split.
func adminQuoteArgs(args []string) []string {
	quoted := make([]string, len(args))
	for i, arg := range args {
		if arg == "" || strings.ContainsAny(arg, " \t\"'$`\\") {
			arg = "'" + strings.ReplaceAll(arg, "'", `'\''`) + "'"
		}
		quoted[i] = arg
	}
	return quoted
}

// adminTellApp says in the app's channel what the job did, and who did it.
func adminTellApp(job adminJob) {
	if job.App == "" || job.Said == "" {
		return
	}
	text := fmt.Sprintf(":gear: %s %s, from the admin page.", job.By, job.Said)
	if job.State != "done" {
		text = fmt.Sprintf(":warning: %s tried to %s from the admin page, and it failed. Its output is in the admin page's Jobs.", job.By, job.Tried)
	}
	if err := adminTell(job.App, job.Main, text); err != nil {
		fmt.Fprintf(os.Stderr, "hi: telling app %s's channel: %v\n", job.App, err)
	}
}

// adminTell posts in an app's ops channel, or its main channel, as its
// bot: what an admin changed is said where the app's people see it.
func adminTell(app string, main bool, text string) error {
	config, err := loadTeam(app)
	if err != nil {
		return err
	}
	secrets, err := loadTeamSecrets(app)
	if err != nil || secrets.SlackBotToken == "" {
		return errors.New("the app has no Slack bot token")
	}
	channel := config.opsChannel()
	if main {
		channel = config.Channel
	}
	return adminPost(secrets.SlackBotToken, channel, text)
}

// adminPost and adminDM are Slack's, as a bot; tests replace them.
var adminPost = func(token, channel, text string) error {
	_, _, err := slack.New(token).PostMessage(channel, slack.MsgOptionText(text, false), slack.MsgOptionDisableLinkUnfurl())
	return err
}

var adminDM = func(token, user, text string) error {
	api := slack.New(token)
	im, _, _, err := api.OpenConversation(&slack.OpenConversationParameters{Users: []string{user}, ReturnIM: true})
	if err != nil {
		return err
	}
	_, _, err = api.PostMessage(im.ID, slack.MsgOptionText(text, false), slack.MsgOptionDisableLinkUnfurl())
	return err
}

// adminSendDM sends an admin a DM through the bot of any app on the
// machine that is in their workspace: the first that can.
func adminSendDM(user, text string) error {
	entries, _ := os.ReadDir(appsDir())
	tried := false
	var last error
	seen := map[string]bool{}
	// The org server's Slack app first, on its machine.
	if dir := adminOrgDir(); dir != "" {
		if config, err := loadSlackConfig(dir); err == nil && config != nil && config.BotToken != "" {
			seen[config.BotToken], tried = true, true
			if last = adminDM(config.BotToken, user, text); last == nil {
				return nil
			}
		}
	}
	for _, entry := range entries {
		if !appNamePattern.MatchString(entry.Name()) {
			continue
		}
		secrets, err := loadTeamSecrets(entry.Name())
		if err != nil || secrets.SlackBotToken == "" || seen[secrets.SlackBotToken] {
			continue
		}
		seen[secrets.SlackBotToken] = true
		tried = true
		if last = adminDM(secrets.SlackBotToken, user, text); last == nil {
			return nil
		}
	}
	if !tried {
		return errors.New("no app on this machine, nor the server, has a Slack bot to send it with")
	}
	return fmt.Errorf("no app's Slack bot could send it: %w", last)
}
