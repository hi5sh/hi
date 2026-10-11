package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeTeamSlack answers the wizard's checks.
type fakeTeamSlack struct {
	badBot, notMember bool
}

func (f fakeTeamSlack) botIdentity(string) (string, string, error) {
	if f.badBot {
		return "", "", errors.New("invalid_auth")
	}
	return "Plejd", "UBOT", nil
}
func (fakeTeamSlack) checkAppToken(string) error { return nil }
func (f fakeTeamSlack) channel(_, channel string) (string, bool, error) {
	return "payments", !f.notMember, nil
}

// setupTeamTest gives a test its own teams folder, a Hermes install with
// an OpenRouter key, and a git identity.
func setupTeamTest(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HI_TEAMS_DIR", filepath.Join(dir, "teams"))
	hermes := filepath.Join(dir, "hermes")
	t.Setenv("HERMES_HOME", hermes)
	t.Setenv("HERMES_INSTALL_DIR", "")
	writeSkillTestFile(t, filepath.Join(hermes, "hermes-agent", "venv", "bin", "python"), "", 0o755)
	writeSkillTestFile(t, filepath.Join(hermes, "hermes-agent", "hermes"), "", 0o755)
	writeSkillTestFile(t, filepath.Join(hermes, ".env"), "OPENROUTER_API_KEY=sk-or-real\n", 0o600)
	writeSkillTestFile(t, filepath.Join(hermes, "config.yaml"), "model:\n  default: qwen/qwen3.8-max\n  provider: openrouter\n", 0o600)
	t.Setenv("GIT_AUTHOR_NAME", "Test")
	t.Setenv("GIT_AUTHOR_EMAIL", "test@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "Test")
	t.Setenv("GIT_COMMITTER_EMAIL", "test@example.com")
	t.Chdir(dir)
	saved := teamSlack
	teamSlack = fakeTeamSlack{}
	t.Cleanup(func() { teamSlack = saved })
	return dir
}

func testTeamConfig() teamConfig {
	return teamConfig{Name: "payments", Purpose: "Track supplier payments", Channel: "C07PAYMENTS", Owners: []string{"U07OWNER1"},
		Members: defaultTeamMembers(), Branch: "main"}
}

func TestValidateTeam(t *testing.T) {
	setupTeamTest(t)
	if err := validateTeam(testTeamConfig()); err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(*teamConfig){
		"a team's name":            func(c *teamConfig) { c.Name = "Payments" },
		"Slack channel ID":         func(c *teamConfig) { c.Channel = "#payments" },
		"at least one owner":       func(c *teamConfig) { c.Owners = nil },
		"Slack member IDs":         func(c *teamConfig) { c.Askers = []string{"alice"} },
		"the lead is Hermes":       func(c *teamConfig) { c.Members["lead"] = teamMember{Agent: "claude"} },
		"different agent or model": func(c *teamConfig) { c.Members["reviewer"] = teamMember{Agent: "claude", Model: "opus"} },
		"claude, codex, or hermes": func(c *teamConfig) { c.Members["tester"] = teamMember{Agent: "gemini"} },
		"locked, dev, or open":     func(c *teamConfig) { c.Members["coder"] = teamMember{Agent: "claude", Network: "wide"} },
		"short lowercase name":     func(c *teamConfig) { c.Members["Big Role"] = teamMember{Agent: "codex"} },
	}
	for want, change := range cases {
		config := testTeamConfig()
		change(&config)
		if err := validateTeam(config); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v", want, err)
		}
	}
}

func TestTeamManifest(t *testing.T) {
	var manifest struct {
		Display struct {
			Name string `json:"name"`
		} `json:"display_information"`
		OAuth struct {
			Scopes struct {
				Bot []string `json:"bot"`
			} `json:"scopes"`
		} `json:"oauth_config"`
		Settings struct {
			Socket bool `json:"socket_mode_enabled"`
			Events struct {
				Bot []string `json:"bot_events"`
			} `json:"event_subscriptions"`
		} `json:"settings"`
	}
	if err := json.Unmarshal([]byte(teamManifest(testTeamConfig())), &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Display.Name != "team-payments" || !manifest.Settings.Socket {
		t.Fatalf("%+v", manifest)
	}
	all := strings.Join(append(manifest.OAuth.Scopes.Bot, manifest.Settings.Events.Bot...), " ")
	// One channel: no direct messages.
	if strings.Contains(all, "im:") || strings.Contains(all, "message.im") || !strings.Contains(all, "message.channels") {
		t.Fatal(all)
	}
}

func TestTeamWizard(t *testing.T) {
	dir := setupTeamTest(t)
	answers := strings.Join([]string{
		"Track supplier payments for the finance admins",
		"xoxb-bot", "xapp-app",
		"\x1b[201~C07PAYMENTS", // the end of a pasted token's marker, left over
		"U07OWNER1, U07OWNER2",
		"",                      // everyone in the channel may ask
		"", "", "codex:gpt-5.5", // lead and coder as offered, a reviewer model
		"", // a new repository
		"", // not on GitHub
	}, "\n") + "\n"
	var out bytes.Buffer
	if err := teamNew([]string{"payments"}, strings.NewReader(answers), &out); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), `"name": "team-payments"`) || !strings.Contains(out.String(), "hi team up payments") {
		t.Fatal(out.String())
	}
	config, err := loadTeam("payments")
	if err != nil {
		t.Fatal(err)
	}
	if config.Channel != "C07PAYMENTS" || len(config.Owners) != 2 || config.Askers != nil || config.Branch != "main" ||
		config.Members["reviewer"].Model != "gpt-5.5" || config.Members["coder"].Agent != "claude" || config.Members["lead"].Model != "qwen/qwen3.8-max" {
		t.Fatalf("%+v", config)
	}
	info, err := os.Stat(filepath.Join(teamDir("payments"), "secrets.json"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("secrets: %v %v", info, err)
	}
	secrets, _ := loadTeamSecrets("payments")
	if secrets.SlackBotToken != "xoxb-bot" || secrets.SlackAppToken != "xapp-app" {
		t.Fatalf("%+v", secrets)
	}
	repo := filepath.Join(teamDir("payments"), "repo")
	if boxGit(repo, "log", "--oneline") == "" || !fileExists(filepath.Join(repo, "AGENTS.md")) {
		t.Fatal("no first commit")
	}
	soul, _ := os.ReadFile(filepath.Join(teamDir("payments"), "lead", "SOUL.md"))
	if !strings.Contains(string(soul), "<@U07OWNER1>") || !strings.Contains(string(soul), "may not read code") {
		t.Fatal(string(soul))
	}
	// The folder is where hi team new ran, and hi finds it by name.
	if want := filepath.Join(dir, "team-payments"); teamDir("payments") != want || !fileExists(filepath.Join(want, ".gitignore")) {
		t.Fatalf("the team is in %s", teamDir("payments"))
	}
	// The same name again is refused.
	if err := teamNew([]string{"payments"}, strings.NewReader(answers), &out); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatal(err)
	}
}

func TestTeamNewChecksSlack(t *testing.T) {
	setupTeamTest(t)
	file := filepath.Join(t.TempDir(), "team.json")
	writeSkillTestFile(t, file, `{"purpose": "x", "channel": "C07PAYMENTS", "owners": ["U07OWNER1"]}`, 0o600)
	var out bytes.Buffer
	if err := teamNew([]string{"payments", "--from", file}, strings.NewReader(""), &out); err == nil || !strings.Contains(err.Error(), "HI_TEAM_SLACK_BOT_TOKEN") {
		t.Fatal(err)
	}
	t.Setenv("HI_TEAM_SLACK_BOT_TOKEN", "xoxb-bot")
	t.Setenv("HI_TEAM_SLACK_APP_TOKEN", "xapp-app")
	teamSlack = fakeTeamSlack{notMember: true}
	if err := teamNew([]string{"payments", "--from", file}, strings.NewReader(""), &out); err == nil || !strings.Contains(err.Error(), "/invite @team-payments") {
		t.Fatal(err)
	}
	if fileExists(teamDir("payments")) {
		t.Fatal("a failed setup leaves no folder")
	}
	teamSlack = fakeTeamSlack{}
	if err := teamNew([]string{"payments", "--from", file}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
}

func TestWriteTeamLead(t *testing.T) {
	setupTeamTest(t)
	config := testTeamConfig()
	os.MkdirAll(filepath.Join(teamDir("payments"), "lead"), 0o700)
	writeSkillTestFile(t, filepath.Join(teamDir("payments"), "lead", "SOUL.md"), "edited by an owner", 0o600)
	if err := writeTeamLead(config, teamSecrets{SlackBotToken: "xoxb-1", SlackAppToken: "xapp-1"}, "10.234.7.2", ""); err != nil {
		t.Fatal(err)
	}
	lead := filepath.Join(teamDir("payments"), "lead")
	env, _ := os.ReadFile(filepath.Join(lead, ".env"))
	for _, want := range []string{"OPENROUTER_API_KEY=" + boxClaudePlacehold, "SLACK_BOT_TOKEN=xoxb-1", "SLACK_ALLOWED_CHANNELS=C07PAYMENTS", "SLACK_DISABLE_DMS=true", "SLACK_REQUIRE_MENTION=true", "SLACK_STRICT_MENTION=true", "SLACK_ALLOW_ALL_USERS=true"} {
		if !strings.Contains(string(env), want) {
			t.Errorf(".env lacks %s:\n%s", want, env)
		}
	}
	if strings.Contains(string(env), "sk-or-real") {
		t.Fatal("the real key is in the box")
	}
	if script, err := os.Stat(filepath.Join(lead, "scripts", teamChannelScript)); err != nil || script.Mode()&0o100 == 0 {
		t.Fatal("the channel-watch script is missing", err)
	}
	settings, _ := os.ReadFile(filepath.Join(lead, "config.yaml"))
	for _, want := range []string{"default: qwen/qwen3.8-max", "base_url: http://10.234.7.2:3129/api/v1", "cwd: /team", `mode: "off"`} {
		if !strings.Contains(string(settings), want) {
			t.Errorf("config.yaml lacks %s:\n%s", want, settings)
		}
	}
	if soul, _ := os.ReadFile(filepath.Join(lead, "SOUL.md")); string(soul) != "edited by an owner" {
		t.Fatal("SOUL.md was overwritten")
	}
	if skill, _ := os.ReadFile(filepath.Join(lead, "skills", "hi-team", "SKILL.md")); !strings.Contains(string(skill), "hi team merge t-4") {
		t.Fatal(string(skill))
	}
	// Named askers: only they and the owners are heard.
	config.Askers = []string{"U07ASKER1"}
	writeTeamLead(config, teamSecrets{}, "10.234.7.2", "")
	env, _ = os.ReadFile(filepath.Join(lead, ".env"))
	if !strings.Contains(string(env), "SLACK_ALLOWED_USERS=U07OWNER1,U07ASKER1") || strings.Contains(string(env), "ALLOW_ALL") {
		t.Fatal(string(env))
	}
}

func TestTeamUpWritesUnit(t *testing.T) {
	setupTeamTest(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	config := testTeamConfig()
	os.MkdirAll(teamDir("payments"), 0o700)
	saveTeam(config)
	var calls []string
	savedSystemctl, savedLinger := teamSystemctl, teamLingers
	teamSystemctl = func(args ...string) (string, error) { calls = append(calls, strings.Join(args, " ")); return "", nil }
	teamLingers = func() bool { return false }
	t.Cleanup(func() { teamSystemctl, teamLingers = savedSystemctl, savedLinger })
	var out bytes.Buffer
	if err := teamUp("payments", &out); err != nil {
		t.Fatal(err)
	}
	unit, _ := os.ReadFile(teamUnitPath("payments"))
	if !strings.Contains(string(unit), " team __serve payments\n") || !strings.Contains(string(unit), "Restart=always") || !strings.Contains(string(unit), "HI_TEAMS_DIR=") {
		t.Fatal(string(unit))
	}
	if strings.Join(calls, "; ") != "daemon-reload; enable hi-team-payments.service; restart hi-team-payments.service" {
		t.Fatal(calls)
	}
	if !strings.Contains(out.String(), "enable-linger") {
		t.Fatal(out.String())
	}
}

func TestTeamLeadCommandsNeedTheBox(t *testing.T) {
	t.Setenv(teamSocketEnv, "")
	var out, errs bytes.Buffer
	if code := runTeam([]string{"tasks"}, strings.NewReader(""), &out, &errs); code != 1 || !strings.Contains(errs.String(), "for a team's lead, in its box") {
		t.Fatal(code, errs.String())
	}
}

func TestAgentFromFlag(t *testing.T) {
	options, err := parseBoxOptions("agent", []string{"--from", "hi-box/payments-t-1", "task"})
	if err != nil || options.from != "hi-box/payments-t-1" {
		t.Fatal(options.from, err)
	}
}

// teamTestBroker is a broker whose hi agent is faked: each run commits a
// file on its branch, as a coder would.
type teamTestBroker struct {
	*teamBroker
	mu      sync.Mutex
	calls   []string
	reports map[string]string // box → the report block's JSON
}

func newTeamTestBroker(t *testing.T) *teamTestBroker {
	t.Helper()
	config := testTeamConfig()
	dir := teamDir("payments")
	for _, sub := range []string{"tasks", "lead"} {
		os.MkdirAll(filepath.Join(dir, sub), 0o700)
	}
	if err := newTeamRepo(config, filepath.Join(dir, "repo")); err != nil {
		t.Fatal(err)
	}
	saveTeam(config)
	b := &teamTestBroker{teamBroker: newTeamBroker("payments", "hi"), reports: map[string]string{}}
	b.run = func(args ...string) ([]byte, error) {
		b.mu.Lock()
		defer b.mu.Unlock()
		b.calls = append(b.calls, strings.Join(args, " "))
		switch {
		case args[0] == "agent" && args[1] != "wait":
			box := args[5]
			branch := "hi-box/" + box
			from := "main"
			for i, arg := range args {
				if arg == "--from" {
					from = args[i+1]
				}
			}
			repo := b.repo
			exec.Command("git", "-C", repo, "branch", branch, from).Run()
			if args[1] != "codex" { // the reviewer commits nothing
				work := filepath.Join(t.TempDir(), "w")
				exec.Command("git", "-C", repo, "worktree", "add", "-q", work, branch).Run()
				os.WriteFile(filepath.Join(work, box+".txt"), []byte(box), 0o644)
				// Coders mark features done in the lead's specs, too.
				os.MkdirAll(filepath.Join(work, "docs", "specs"), 0o755)
				os.WriteFile(filepath.Join(work, "docs", "specs", "suppliers.md"), []byte("Status: Done\n"), 0o644)
				os.WriteFile(filepath.Join(work, "docs", "specs", box+".md"), []byte("mine\n"), 0o644)
				exec.Command("git", "-C", work, "add", "-A").Run()
				exec.Command("git", "-C", work, "commit", "-q", "-m", box).Run()
				exec.Command("git", "-C", repo, "worktree", "remove", "--force", work).Run()
			}
			return json.Marshal(agentReport{Name: box, Branch: branch, Status: "running"})
		case args[0] == "agent" && args[1] == "wait":
			block := b.reports[args[2]]
			if block == "" {
				block = `{"status": "complete", "summary": "Done."}`
			}
			return json.Marshal(agentReport{Name: args[2], Status: "done", Report: json.RawMessage(block), Text: "All done."})
		}
		return nil, nil
	}
	return b
}

func waitTeamTask(t *testing.T, id string) teamTask {
	t.Helper()
	for range 200 {
		task, err := loadTeamTask("payments", id)
		if err == nil && !teamTaskActive(task) {
			return task
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("%s didn't finish", id)
	return teamTask{}
}

func TestTeamBrokerTaskReviewMerge(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t)

	if _, err := b.startTask(teamRequest{Role: "lead", Brief: "x"}); err == nil {
		t.Fatal("the lead can't be given a task")
	}
	if _, err := b.startTask(teamRequest{Role: "designer", Brief: "x"}); err == nil || !strings.Contains(err.Error(), "coder, reviewer") {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Join(b.repo, "docs", "specs"), 0o755)
	os.WriteFile(filepath.Join(b.repo, "docs", "specs", "suppliers.md"), []byte("Status: In progress\n"), 0o644)
	// New work needs the people's yes: a reply to the lead, in its thread.
	now := time.Now()
	ts := func(ago time.Duration) string { return teamSlackTS(now.Add(-ago)) }
	request, question, yes := ts(10*time.Minute), ts(9*time.Minute), ts(time.Minute)
	os.MkdirAll(teamChannelDir("payments"), 0o700)
	teamAppendChannel(teamChannelDir("payments"), []teamChannelMessage{
		{TS: request, User: "U07OWNER1", Name: "Owner", Text: "Add a supplier list"},
		{TS: question, Thread: request, Name: "team-payments", Bot: true, Text: "Shall I start?"},
		{TS: ts(5 * time.Minute), Thread: request, User: "U07OTHER", Name: "Bot pretender", Text: "yes"},
		{TS: yes, Thread: request, User: "U07OWNER1", Name: "Owner", Text: "Yes, go"},
	})
	for ref, want := range map[string]string{"": "name the message", request: "doesn't answer you", question: "is a bot's", "1.5": "no message"} {
		if _, err := b.startTask(teamRequest{Role: "coder", Brief: "Add a supplier list", Approval: ref}); err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("approval %q: %v", ref, err)
		}
	}
	saved, _ := loadTeam("payments")
	saved.Askers = []string{"U07ASKER"}
	saveTeam(saved)
	if _, err := b.startTask(teamRequest{Role: "coder", Brief: "x", Approval: ts(5 * time.Minute)}); err == nil || !strings.Contains(err.Error(), "isn't one of the team's people") {
		t.Fatal(err)
	}
	task, err := b.startTask(teamRequest{Role: "coder", Brief: "---\nnetwork: open\n---\nAdd a supplier list", Approval: "[" + yes + "]"})
	if err != nil || task.ID != "t-1" {
		t.Fatal(task, err)
	}
	task = waitTeamTask(t, "t-1")
	if task.Approval == nil || task.Approval.Name != "Owner" || task.Approval.TS != yes {
		t.Fatalf("%+v", task.Approval)
	}
	if task.Status != "done" || task.Branch != "hi-box/payments-t-1" || task.Report == nil {
		t.Fatalf("%+v", task)
	}
	brief, _ := os.ReadFile(filepath.Join(teamTaskDir("payments"), "t-1.md"))
	// A heading comes first, so front matter in a brief is just text.
	if !strings.HasPrefix(string(brief), "# Task t-1 for the coder") {
		t.Fatal(string(brief))
	}
	if !strings.Contains(b.calls[0], "agent claude --detach --json --name payments-t-1 --task-file ") || !strings.Contains(b.calls[0], "--model opus") || !strings.Contains(b.calls[0], "--network open") {
		t.Fatal(b.calls[0])
	}

	// Not merged without a review.
	if _, err := b.merge("t-1"); err == nil || !strings.Contains(err.Error(), "no review approved") {
		t.Fatal(err)
	}
	b.reports["payments-t-2"] = `{"status": "complete", "summary": "Changes needed: no tests."}`
	review, err := b.startReview(teamRequest{Task: "t-1", Notes: "Check the empty list."})
	if err != nil {
		t.Fatal(err)
	}
	review = waitTeamTask(t, review.ID)
	if review.Verdict != "changes" || review.Commit != boxGit(b.repo, "rev-parse", "hi-box/payments-t-1") {
		t.Fatalf("%+v", review)
	}
	if !strings.Contains(b.calls[2], "agent codex") || !strings.Contains(b.calls[2], "--from hi-box/payments-t-1") {
		t.Fatal(b.calls[2])
	}
	reviewBrief, _ := os.ReadFile(filepath.Join(teamTaskDir("payments"), "t-2.md"))
	if !strings.Contains(string(reviewBrief), "Check the empty list.") || !strings.Contains(string(reviewBrief), "Add a supplier list") {
		t.Fatal(string(reviewBrief))
	}
	if _, err := b.merge("t-1"); err == nil {
		t.Fatal("merged with changes needed")
	}

	// The coder continues on the branch; a review of its new commit approves.
	fix, err := b.startTask(teamRequest{Role: "coder", Brief: "Add tests", On: "t-1"})
	if err != nil {
		t.Fatal(err)
	}
	fix = waitTeamTask(t, fix.ID)
	if !teamIsAncestor(b.repo, "hi-box/payments-t-1", fix.Branch) {
		t.Fatal("t-3 didn't start from t-1's branch")
	}
	b.reports["payments-t-4"] = `{"status": "complete", "summary": "Approve: tests cover the list."}`
	// A review of t-1's old commit doesn't approve t-3.
	approval, err := b.startReview(teamRequest{Task: "t-3"})
	if err != nil {
		t.Fatal(err)
	}
	if approval = waitTeamTask(t, approval.ID); approval.Verdict != "approve" {
		t.Fatalf("%+v", approval)
	}
	merged, err := b.merge("t-3")
	if err != nil {
		t.Fatal(err)
	}
	if merged.Status != "merged" || boxGit(b.repo, "rev-parse", "HEAD") != merged.Merged || !fileExists(filepath.Join(b.repo, "payments-t-1.txt")) {
		t.Fatalf("%+v", merged)
	}
	// The lead's specs are as the lead left them; the coders' changes are out.
	if spec, _ := os.ReadFile(filepath.Join(b.repo, "docs", "specs", "suppliers.md")); string(spec) != "Status: In progress\n" {
		t.Fatalf("the spec is %q", spec)
	}
	if fileExists(filepath.Join(b.repo, "docs", "specs", "payments-t-1.md")) || len(merged.SpecsKept) != 3 {
		t.Fatalf("%v", merged.SpecsKept)
	}
	if boxGit(b.repo, "status", "--porcelain") != "" || boxGit(b.repo, "rev-list", "--count", "HEAD^2..HEAD") != "1" {
		t.Fatal("the merge isn't one clean commit")
	}
	if first, _ := loadTeamTask("payments", "t-1"); first.Status != "merged" {
		t.Fatalf("t-1 is %s", first.Status)
	}
	if !strings.Contains(strings.Join(b.calls, "\n"), "box rm --force payments-t-2") {
		t.Fatal(b.calls)
	}
	if boxGit(b.repo, "branch", "--list", "hi-box/*") != "" {
		t.Fatal("merged branches are kept")
	}
}

func TestTeamBrokerSocket(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	slackChannel := &fakeTeamChannel{}
	b.channel = slackChannel
	dir, err := os.MkdirTemp("", "hiteam")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	socket := filepath.Join(dir, teamSocketName)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serve, err := b.listen(ctx, socket)
	if err != nil {
		t.Fatal(err)
	}
	go serve()
	t.Setenv(teamSocketEnv, socket)
	brief := filepath.Join(dir, "brief.md")
	os.WriteFile(brief, []byte("# Add a supplier list\n\nDetails."), 0o600)
	var out bytes.Buffer
	if err := runTeamLeadCommand("task", []string{"coder", brief, "--small"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	// A small fix starts without a go-ahead, and the channel hears of it.
	if len(slackChannel.calls) != 1 || slackChannel.calls[0] != "post C07PAYMENTS Small fix started without waiting for a go-ahead (t-1): Add a supplier list" {
		t.Fatal(slackChannel.calls)
	}
	if !strings.Contains(out.String(), `t-1 started: the coder (Claude Code) works on "Add a supplier list"`) {
		t.Fatal(out.String())
	}
	out.Reset()
	if err := runTeamLeadCommand("wait", []string{"t-1"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "t-1 · coder (Claude Code) · done") || !strings.Contains(out.String(), "All done.") {
		t.Fatal(out.String())
	}
	out.Reset()
	if err := runTeamLeadCommand("merge", []string{"t-1"}, strings.NewReader(""), &out); err == nil || !strings.Contains(err.Error(), "no review approved") {
		t.Fatal(err)
	}
	if err := runTeamLeadCommand("tasks", nil, strings.NewReader(""), &out); err != nil || !strings.Contains(out.String(), "t-1") {
		t.Fatal(out.String(), err)
	}
	out.Reset()
	if err := runTeamLeadCommand("versions", nil, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	if want := "Production: nothing released yet.\nDev: 0 changes, nothing released yet.\nIdeas not merged: 1 of 5 at most.\n"; !strings.HasPrefix(out.String(), want) {
		t.Fatal(out.String())
	}
	if err := runTeamLeadCommand("release", []string{"--notes", "-", "--approval", "1.5"}, strings.NewReader("New"), &out); err == nil || !strings.Contains(err.Error(), "no message 1.5") {
		t.Fatal(err)
	}
	if err := runTeamLeadCommand("discard", []string{"t-1", "--approval", "1.5"}, strings.NewReader(""), &out); err == nil {
		t.Fatal("discarded an idea with an approval it doesn't take")
	}
	out.Reset()
	if err := runTeamLeadCommand("discard", []string{"t-1"}, strings.NewReader(""), &out); err != nil || out.String() != "Discarded t-1, with its boxes and branches.\n" {
		t.Fatal(out.String(), err)
	}
}

func TestTeamMovedFolder(t *testing.T) {
	dir := setupTeamTest(t)
	t.Setenv("HI_TEAM_SLACK_BOT_TOKEN", "xoxb-bot")
	t.Setenv("HI_TEAM_SLACK_APP_TOKEN", "xapp-app")
	file := filepath.Join(dir, "answers.json")
	writeSkillTestFile(t, file, `{"purpose": "x", "channel": "C07PAYMENTS", "owners": ["U07OWNER1"]}`, 0o600)
	var out bytes.Buffer
	if err := teamNew([]string{"payments", "--dir", "elsewhere/pay", "--from", file}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	if teamDir("payments") != filepath.Join(dir, "elsewhere", "pay") {
		t.Fatal(teamDir("payments"))
	}
	os.Rename(filepath.Join(dir, "elsewhere", "pay"), filepath.Join(dir, "moved"))
	if _, err := loadTeam("payments"); err == nil || !strings.Contains(err.Error(), "if you moved it, run hi team up <its new folder>") {
		t.Fatal(err)
	}
	name, err := teamAdopt("moved", &out)
	if err != nil || name != "payments" || teamDir("payments") != filepath.Join(dir, "moved") {
		t.Fatal(name, err, teamDir("payments"))
	}
	if _, err := loadTeam("payments"); err != nil {
		t.Fatal(err)
	}
}

func TestTeamWizardRefusesAWrongAnswer(t *testing.T) {
	setupTeamTest(t)
	answers := "x\nxoxb-bot\nxapp-app\n#payments\n"
	var out bytes.Buffer
	err := teamNew([]string{"payments"}, strings.NewReader(answers), &out)
	if err == nil || !strings.Contains(err.Error(), `a channel ID looks like C07ABC123, not "#payments"`) {
		t.Fatal(err)
	}
	if fileExists("team-payments") {
		t.Fatal("nothing is written before every answer is in")
	}
	if got := stripTerminalCodes("\x1b[200~xapp-1\x1b[201~"); got != "xapp-1" {
		t.Fatal(got)
	}
}

func TestTeamRunCommand(t *testing.T) {
	dir := t.TempDir()
	if _, err := teamRunCommand(dir); err == nil || !strings.Contains(err.Error(), "run target") {
		t.Fatal(err)
	}
	writeSkillTestFile(t, filepath.Join(dir, "package.json"), `{"scripts": {"start": "node server.js"}}`, 0o644)
	if command, _ := teamRunCommand(dir); command != "npm install --no-audit --no-fund && npm start" {
		t.Fatal(command)
	}
	writeSkillTestFile(t, filepath.Join(dir, "package-lock.json"), `{}`, 0o644)
	if command, _ := teamRunCommand(dir); !strings.HasPrefix(command, "npm ci") {
		t.Fatal(command)
	}
	writeSkillTestFile(t, filepath.Join(dir, "Makefile"), "test:\n\tgo test\nrun: build\n\t./app\n", 0o644)
	if command, _ := teamRunCommand(dir); command != "make run" {
		t.Fatal(command)
	}
}

func TestWriteTeamSkillKeepsTheLeadsEdits(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "SKILL.md")
	read := func() string { data, _ := os.ReadFile(path); return string(data) }
	writeTeamSkill(dir, "hi v1")
	if read() != "hi v1" {
		t.Fatal(read())
	}
	os.WriteFile(path, []byte("hi v1, improved by the lead"), 0o600)
	writeTeamSkill(dir, "hi v1")
	if read() != "hi v1, improved by the lead" {
		t.Fatal("the lead's edit was lost on a restart")
	}
	writeTeamSkill(dir, "hi v2")
	if lead, _ := os.ReadFile(path + ".lead"); read() != "hi v2" || string(lead) != "hi v1, improved by the lead" {
		t.Fatal(read(), string(lead))
	}
	// Unedited, a new text replaces it without a copy.
	os.Remove(path + ".lead")
	writeTeamSkill(dir, "hi v3")
	if read() != "hi v3" || fileExists(path+".lead") {
		t.Fatal(read())
	}
}

func TestTeamPreview(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	if _, err := b.preview(teamPreviewRequest{}); err == nil || !strings.Contains(err.Error(), "no way to start") {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(b.repo, "Makefile"), []byte("run:\n\t./serve\n"), 0o644)
	exec.Command("git", "-C", b.repo, "add", "-A").Run()
	exec.Command("git", "-C", b.repo, "commit", "-q", "-m", "make run").Run()

	var started []string
	b.app = func(src, command string, port, hostPort int) error {
		if !fileExists(filepath.Join(src, "Makefile")) {
			t.Error("the app's copy has no Makefile")
		}
		started = append(started, fmt.Sprintf("%s %d", command, port))
		server := &http.Server{Addr: fmt.Sprintf("127.0.0.1:%d", hostPort), Handler: http.NotFoundHandler()}
		go server.ListenAndServe()
		t.Cleanup(func() { server.Close() })
		return nil
	}
	saved := b.run
	b.run = func(args ...string) ([]byte, error) {
		if args[0] == "net" && args[2] != "stop" {
			if !strings.Contains(strings.Join(args, " "), "--detach --json --max 4h0m0s --name team-payments") {
				t.Errorf("expose: %q", args)
			}
			return json.Marshal(netExposeState{Name: "team-payments-x1", URL: "https://team-payments-x1.example.net", Password: "pw-123", Expires: time.Now().Add(4 * time.Hour)})
		}
		return saved(args...)
	}
	preview, err := b.preview(teamPreviewRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if preview.URL != "https://team-payments-x1.example.net" || preview.Password != "pw-123" || preview.Source != "main" || started[0] != "make run 8080" {
		t.Fatalf("%+v %q", preview, started)
	}
	if !fileExists(teamPreviewPath("payments")) {
		t.Fatal("no preview.json")
	}
	if _, err := b.preview(teamPreviewRequest{Stop: true}); err != nil || fileExists(teamPreviewPath("payments")) {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(b.calls, "\n"), "net expose stop team-payments-x1") {
		t.Fatal(b.calls)
	}
}

func TestTeamSpecsInTheProject(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	config, _ := loadTeam("payments")
	// A team made before: its specs move into the project.
	writeSkillTestFile(t, filepath.Join(teamDir("payments"), "specs", "clocks.md"), "# Clocks\n", 0o600)
	writeSkillTestFile(t, filepath.Join(teamDir("payments"), "specs", "briefs", "t-1.md"), "# A brief\n", 0o600)
	specs, err := teamPrepareSpecs(config)
	if err != nil || specs != filepath.Join(b.repo, "docs", "specs") || !fileExists(filepath.Join(specs, "clocks.md")) || fileExists(filepath.Join(teamDir("payments"), "specs")) ||
		fileExists(filepath.Join(specs, "briefs")) || !fileExists(filepath.Join(teamDir("payments"), "run", "home", "briefs", "t-1.md")) {
		t.Fatal(specs, err)
	}
	// A task commits them first, so the member's branch has them.
	if _, err := b.startTask(teamRequest{Role: "coder", Brief: "Build the clocks", Small: true}); err != nil {
		t.Fatal(err)
	}
	waitTeamTask(t, "t-1")
	if boxGit(b.repo, "log", "-1", "--format=%s", "--", "docs/specs/clocks.md") != "Update the specs" {
		t.Fatal(boxGit(b.repo, "log", "--oneline"))
	}
	if boxGit(b.repo, "status", "--porcelain") != "" {
		t.Fatal("repo/ isn't clean")
	}
	// A deleted spec is deleted in git too.
	os.Remove(filepath.Join(specs, "clocks.md"))
	if err := b.commitSpecs(); err != nil {
		t.Fatal(err)
	}
	if boxGit(b.repo, "ls-files", "docs/specs/clocks.md") != "" {
		t.Fatal("the deleted spec is still tracked")
	}
	// Nothing changed: no commit.
	head := boxGit(b.repo, "rev-parse", "HEAD")
	b.commitSpecs()
	if boxGit(b.repo, "rev-parse", "HEAD") != head {
		t.Fatal("an empty commit")
	}
}

func TestTeamConnectGitHub(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	config, _ := loadTeam("payments")
	bare := filepath.Join(t.TempDir(), "github.git")
	exec.Command("git", "init", "-q", "--bare", bare).Run()
	var calls []string
	savedGH, savedLook := teamGH, teamGHLookPath
	t.Cleanup(func() { teamGH, teamGHLookPath = savedGH, savedLook })
	teamGHLookPath = func() (string, error) { return "/usr/bin/gh", nil }
	teamGH = func(dir string, args ...string) (string, error) {
		calls = append(calls, strings.Join(args, " "))
		switch args[0] + " " + args[1] {
		case "auth status":
			return "Logged in", nil
		case "repo view":
			return "Could not resolve to a Repository", errors.New("exit status 1")
		case "repo create":
			// As gh does: the remote, then the push.
			exec.Command("git", "-C", dir, "remote", "add", "origin", bare).Run()
			out, err := exec.Command("git", "-C", dir, "push", "-q", "origin", "main").CombinedOutput()
			return string(out), err
		}
		return "", nil
	}
	if err := teamCheckGitHub("not a name"); err == nil {
		t.Fatal("a bad name")
	}
	if err := teamCheckGitHub("imanhabib/world-clock"); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := teamConnectGitHub(&config, "imanhabib/world-clock", &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(calls[len(calls)-1], "repo create imanhabib/world-clock --private --source . --remote origin --push") {
		t.Fatal(calls)
	}
	if saved, _ := loadTeam("payments"); saved.Remote != "https://github.com/imanhabib/world-clock.git" {
		t.Fatal(saved.Remote)
	}
	if boxGit(bare, "rev-parse", "main") != boxGit(b.repo, "rev-parse", "main") {
		t.Fatal("not pushed")
	}
	// Connected once is enough.
	if err := teamConnectGitHub(&config, "imanhabib/other", &out); err == nil || !strings.Contains(err.Error(), "already pushes to") {
		t.Fatal(err)
	}
}
