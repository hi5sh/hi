package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeTeamSlack answers the wizard's checks.
type fakeTeamSlack struct {
	badBot, notMember, fewScopes bool
}

func (f fakeTeamSlack) botIdentity(string) (string, string, error) {
	if f.badBot {
		return "", "", errors.New("invalid_auth")
	}
	return "Acme", "UBOT", nil
}
func (fakeTeamSlack) checkAppToken(string) error { return nil }
func (f fakeTeamSlack) scopes(string) ([]string, error) {
	if f.fewScopes {
		return []string{"chat:write", "channels:history"}, nil
	}
	return teamSlackScopes, nil
}
func (f fakeTeamSlack) channel(_, channel string) (string, bool, error) {
	return "payments", !f.notMember, nil
}

// setupTeamTest gives a test its own teams folder, a Hermes install with
// an OpenRouter key, and a git identity.
func setupTeamTest(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	// A broker's background work that ends after its test (a fix round, a
	// deep review) writes somewhere harmless, not to this machine's
	// teams: this runs after t.Setenv puts the old value back.
	t.Cleanup(func() { os.Setenv("HI_TEAMS_DIR", filepath.Join(os.TempDir(), "hi-test-teams")) })
	t.Setenv("HI_TEAMS_DIR", filepath.Join(dir, "teams"))
	// No test reaches this machine's containers for a team's dev box.
	savedStopDev := teamStopDevBox
	teamStopDevBox = func(string) {}
	t.Cleanup(func() { teamStopDevBox = savedStopDev })
	// Not this machine's hi server: no server templates.
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(dir, "cache"))
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
	// Previews fork production's data with an overlay, as with Podman,
	// whatever engine this machine has.
	overlay := teamOverlayEngine
	teamOverlayEngine = func() bool { return true }
	t.Cleanup(func() { teamOverlayEngine = overlay })
	// The app's boxes are fakes, with no resolver; a freeze or pause the
	// hi server asks for touches no real container.
	teamResolverOn = false
	engine := teamWatchEngine
	teamWatchEngine = func(args ...string) (string, error) { return "", nil }
	t.Cleanup(func() { teamResolverOn, teamWatchEngine = true, engine })
	// OpenRouter's models without the network, and a Claude sign-in that
	// isn't this machine's.
	ids := teamOpenRouterModelIDs
	teamOpenRouterModelIDs = func() ([]string, error) {
		return []string{"anthropic/claude-opus-5", "anthropic/claude-opus-5.5", "anthropic/claude-opus-5.5:batch", "anthropic/claude-sonnet-5.5", "openai/gpt-6.1-sol"}, nil
	}
	t.Cleanup(func() { teamOpenRouterModelIDs = ids })
	// Codex on an API key, so a reviewer keeps it, and never a real ChatGPT
	// sign-in, which would wait 15 minutes for someone to type a code.
	t.Setenv("CODEX_HOME", filepath.Join(dir, "codex"))
	writeSkillTestFile(t, filepath.Join(dir, "codex", "auth.json"), `{"auth_mode":"apikey","OPENAI_API_KEY":"sk-test"}`, 0o600)
	signIn := teamChatGPTSignIn
	teamChatGPTSignIn = func(io.Writer) (teamChatGPTGrant, error) {
		return teamChatGPTGrant{}, errors.New("no real ChatGPT sign-in in tests")
	}
	t.Cleanup(func() { teamChatGPTSignIn = signIn })
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(dir, "claude"))
	writeSkillTestFile(t, filepath.Join(dir, "claude", ".credentials.json"), `{"claudeAiOauth":{"accessToken":"sk-ant-oat-machine"}}`, 0o600)
	// No advisories from OSV unless a test asks for them.
	lookup := teamDependencyAdvisories
	teamDependencyAdvisories = func([]teamDependencyProject) ([]teamDependencyAdvisory, error) { return nil, nil }
	t.Cleanup(func() { teamDependencyAdvisories = lookup })
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
		"Slack member IDs":         func(c *teamConfig) { c.Owners = []string{"alice"} },
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
	if manifest.Display.Name != "payments" || !manifest.Settings.Socket {
		t.Fatalf("%+v", manifest)
	}
	all := strings.Join(append(manifest.OAuth.Scopes.Bot, manifest.Settings.Events.Bot...), " ")
	// One channel, and the bot's DMs: people talk to the lead there, and
	// hi checks a yes given in one.
	if !strings.Contains(all, "im:history") || !strings.Contains(all, "message.im") || !strings.Contains(all, "message.channels") ||
		!strings.Contains(all, "im:write") || !strings.Contains(all, "commands") || strings.Contains(all, "mpim") ||
		strings.Contains(all, "assistant:write") || !strings.Contains(all, "app_home_opened") {
		t.Fatal(all)
	}
	manifestText := teamManifest(testTeamConfig())
	if skill := teamSkill(testTeamConfig()); !strings.Contains(skill, "what /hi-payments does") || strings.Contains(skill, "%!") {
		t.Fatal("the skill doesn't name the team's command")
	}
	// What's asked for may be filed already: the lead looks first.
	if skill := teamSkill(testTeamConfig()); !strings.Contains(skill, "## Shaping a task") || !strings.Contains(skill, "web_extract reads them") {
		t.Error("the skill doesn't say how to shape a task")
	}
	if skill := teamSkill(testTeamConfig()); !strings.Contains(skill, "**Before you file**, run hi team task list") {
		t.Fatal("the skill doesn't send the lead to the tasks before it files one")
	}
	if teamSlashCommand("Team Ab.C") != "/hi-team-ab-c" {
		t.Fatal(teamSlashCommand("Team Ab.C"))
	}
	if !strings.Contains(manifestText, `"command": "/hi-payments"`) || !strings.Contains(manifestText, `"messages_tab_read_only_enabled": false`) ||
		strings.Contains(manifestText, `"agent_view"`) || strings.Contains(manifestText, `"assistant_view"`) || strings.Contains(manifestText, "mcp") {
		t.Fatal(manifestText)
	}
}

func TestTeamWizard(t *testing.T) {
	dir := setupTeamTest(t)
	saved := teamCheckOpenRouterKey
	teamCheckOpenRouterKey = func(string) error { return nil }
	t.Cleanup(func() { teamCheckOpenRouterKey = saved })
	answers := strings.Join([]string{
		"Åsa Berg", // the lead's name
		"Track supplier payments for the finance admins",
		"xoxb-bot", "xapp-app",
		"\x1b[201~C07PAYMENTS", // the end of a pasted token's marker, left over
		"U07OWNER1, U07OWNER2",
		"", "", "", // the lead, its provider and effort, as offered
		"", "", "high", // the coder and its Claude sign-in as offered, thinking harder
		"codex:gpt-5.5", "", // a reviewer model, its effort as offered
		"sk-or-wizard",                  // the team's own OpenRouter key
		"",                              // a new repository
		"",                              // not on GitHub
		"",                              // no NetBird key yet
		"host", "payments.corp.example", // a name the IT team keeps
	}, "\n") + "\n"
	var out bytes.Buffer
	if err := teamNew([]string{"payments"}, strings.NewReader(answers), &out); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	// The manifest is YAML, named after the lead, with its handle.
	if !strings.Contains(out.String(), "\n  name: Åsa Berg\n") || !strings.Contains(out.String(), "display_name: asa-berg") ||
		!strings.Contains(out.String(), "/invite @asa-berg") || !strings.Contains(out.String(), "hi team up payments") {
		t.Fatal(out.String())
	}
	config, err := loadTeam("payments")
	if err != nil {
		t.Fatal(err)
	}
	if config.Channel != "C07PAYMENTS" || len(config.Owners) != 2 || config.Branch != "main" || config.Members["lead"].Name != "Åsa Berg" || config.Addresses != "payments.corp.example" ||
		config.Members["reviewer"].Model != "gpt-5.5" || config.Members["coder"].Agent != "claude" || config.Members["lead"].Model != "qwen/qwen3.8-max" ||
		config.Members["coder"].Effort != "high" || config.Members["lead"].Effort != "" || config.Members["reviewer"].Effort != "" ||
		config.Members["coder"].Bundle != "web,data" || config.Members["reviewer"].Bundle != "web,data" || config.Members["lead"].Bundle != "" {
		t.Fatalf("%+v", config)
	}
	info, err := os.Stat(filepath.Join(teamDir("payments"), "secrets.json"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("secrets: %v %v", info, err)
	}
	secrets, _ := loadTeamSecrets("payments")
	if secrets.SlackBotToken != "xoxb-bot" || secrets.SlackAppToken != "xapp-app" || secrets.OpenRouterAPIKey != "sk-or-wizard" {
		t.Fatalf("%+v", secrets)
	}
	repo := filepath.Join(teamDir("payments"), "repo")
	if boxGit(repo, "log", "--oneline") == "" || !fileExists(filepath.Join(repo, "AGENTS.md")) || boxGit(repo, "status", "--porcelain") != "" {
		t.Fatal("no first commit")
	}
	// The project is a blank web app, with the team's purpose and rules.
	for _, file := range []string{"package.json", "bun.lock", "Makefile", "docs/user/index.md", "docs/tech/index.md", "src/App.tsx", "src/server.ts", templateMetadataPath} {
		if boxGit(repo, "ls-files", file) == "" {
			t.Errorf("no %s", file)
		}
	}
	// The user docs are the guide: no HELP.md beside them.
	if boxGit(repo, "ls-files", "HELP.md") != "" || !strings.HasPrefix(readTestFile(t, filepath.Join(repo, "docs/user/index.md")), "# Payments\n") {
		t.Error("HELP.md, or the user docs without the app's name")
	}
	if fileExists(filepath.Join(repo, ".agents")) || strings.Contains(readTestFile(t, filepath.Join(repo, "src/db.ts")), "addNote") {
		t.Error("the hi skill or the notes example is there")
	}
	agents := readTestFile(t, filepath.Join(repo, "AGENTS.md"))
	if !strings.Contains(agents, "make check") || !strings.Contains(agents, "bun:sqlite") || !strings.Contains(agents, "with coding agents in the") {
		t.Error(agents)
	}
	if readme := readTestFile(t, filepath.Join(repo, "README.md")); !strings.HasPrefix(readme, "# Payments\n\nTrack supplier payments for the finance admins\n\nA web app on Bun") {
		t.Error(readme)
	}
	if !strings.Contains(readTestFile(t, filepath.Join(repo, "docs/specs/README.md")), "The lead keeps one file per feature") ||
		!strings.Contains(readTestFile(t, filepath.Join(repo, "src/index.html")), "<title>Payments</title>") {
		t.Error("the specs folder or the page's title")
	}
	soul, _ := os.ReadFile(filepath.Join(teamDir("payments"), "lead", "SOUL.md"))
	if !strings.Contains(string(soul), "<@U07OWNER1>") || !strings.Contains(string(soul), "may not read code") ||
		!strings.HasPrefix(string(soul), "# Åsa Berg, the payments team's lead\n\nYou are Åsa Berg, the payments team's lead: its helper") {
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
	if err := teamNew([]string{"payments", "--from", file}, strings.NewReader(""), &out); err == nil || !strings.Contains(err.Error(), "/invite @payments") {
		t.Fatal(err)
	}
	if fileExists(teamDir("payments")) {
		t.Fatal("a failed setup leaves no folder")
	}
	// An app made from another manifest lacks scopes the lead needs.
	teamSlack = fakeTeamSlack{fewScopes: true}
	if err := teamNew([]string{"payments", "--from", file}, strings.NewReader(""), &out); err == nil || !strings.Contains(err.Error(), "lacks app_mentions:read, channels:read, commands, files:read") {
		t.Fatal(err)
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
	for _, want := range []string{"OPENROUTER_API_KEY=" + boxClaudePlacehold, "SLACK_BOT_TOKEN=xoxb-1", "SLACK_ALLOWED_CHANNELS=C07PAYMENTS", "SLACK_DISABLE_DMS=false", "SLACK_REQUIRE_MENTION=true", "SLACK_IGNORE_OTHER_USER_MENTIONS=true", "SLACK_ALLOW_ALL_USERS=true"} {
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
	if hook, err := os.ReadFile(filepath.Join(lead, "hooks", "hi-dm-ack", "HOOK.yaml")); err != nil || !strings.Contains(string(hook), "agent:start") || !fileExists(filepath.Join(lead, "hooks", "hi-dm-ack", "handler.py")) {
		t.Fatal("the DM's first-reply hook is missing", err)
	}
	settings, _ := os.ReadFile(filepath.Join(lead, "config.yaml"))
	for _, want := range []string{"default: qwen/qwen3.8-max", "base_url: http://10.234.7.2:3129/api/v1", "cwd: /team", `mode: "off"`, "cron:\n  wrap_response: false", "web:\n  backend: exa", "mcp_servers:\n  app:\n    command: hi\n    args: [team, mcp, production]\n    env:\n      HI_TEAM_SOCKET: /team/run/broker.sock", "  app_preview:\n    command: hi\n    args: [team, mcp, preview]", "display:\n  busy_ack_enabled: false\n  live_status: verb\n  memory_notifications: \"off\"", "agent:\n  max_turns: 60\n  budget_warning_ratio: 0.75"} {
		if !strings.Contains(string(settings), want) {
			t.Errorf("config.yaml lacks %s:\n%s", want, settings)
		}
	}
	if soul, _ := os.ReadFile(filepath.Join(lead, "SOUL.md")); string(soul) != "edited by an owner" {
		t.Fatal("SOUL.md was overwritten")
	}
	// A SOUL.md from before hi 2.4, with the lead building, gets the new
	// one; the old one is kept beside it.
	writeSkillTestFile(t, filepath.Join(lead, "SOUL.md"), "edited by an owner\n\nYou hand tasks to the members:\n", 0o600)
	writeTeamLead(config, teamSecrets{}, "10.234.7.2", "")
	if soul, _ := os.ReadFile(filepath.Join(lead, "SOUL.md")); string(soul) != teamSoul(config) {
		t.Fatalf("SOUL.md is:\n%s", soul)
	}
	if old, _ := os.ReadFile(filepath.Join(lead, "SOUL.before-2.4.md")); !strings.HasPrefix(string(old), "edited by an owner") {
		t.Fatal("the old SOUL.md isn't kept")
	}
	if skill, _ := os.ReadFile(filepath.Join(lead, "skills", "hi-team", "SKILL.md")); !strings.Contains(string(skill), "hi team task add") || !strings.Contains(string(skill), "in the\nhi-team-notes skill") {
		t.Fatal(string(skill))
	}
	// The lead's notes skill is made once, then left to the lead.
	notes := filepath.Join(lead, "skills", teamNotesSkill, "SKILL.md")
	if got, _ := os.ReadFile(notes); !strings.HasPrefix(string(got), "---\nname: hi-team-notes\n") {
		t.Fatal(string(got))
	}
	os.WriteFile(notes, []byte("mine"), 0o600)
	writeTeamLead(config, teamSecrets{}, "10.234.7.2", "")
	if got, _ := os.ReadFile(notes); string(got) != "mine" {
		t.Fatal("hi rewrote the lead's notes")
	}
	if !strings.Contains(string(env), "SLACK_ALLOW_ALL_USERS=true") {
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
	if strings.Join(calls, "; ") != "daemon-reload; enable hi-team-payments.service; restart hi-team-payments.service; enable --now hi-team-payments-snapshot.timer" {
		t.Fatal(calls)
	}
	// The daily snapshot: a timer, caught up after a reboot, and its service.
	timer, _ := os.ReadFile(filepath.Join(filepath.Dir(teamUnitPath("payments")), "hi-team-payments-snapshot.timer"))
	service, _ := os.ReadFile(filepath.Join(filepath.Dir(teamUnitPath("payments")), "hi-team-payments-snapshot.service"))
	if !strings.Contains(string(timer), "Persistent=true") || !strings.Contains(string(service), " team snapshot payments --daily\n") {
		t.Fatal(string(timer), string(service))
	}
	if !strings.Contains(out.String(), "enable-linger") {
		t.Fatal(out.String())
	}
	// A user in the docker group is root: an escape from a box would be.
	savedGroups := boxRootGroups
	t.Cleanup(func() { boxRootGroups = savedGroups })
	t.Setenv("USER", "hi")
	boxRootGroups = func() []string { return []string{"docker", "lxd"} }
	out.Reset()
	teamUp("payments", &out)
	if !strings.Contains(out.String(), "hi is in the docker and lxd groups, which makes it root") || !strings.Contains(out.String(), "sudo gpasswd -d hi docker\n  sudo gpasswd -d hi lxd") {
		t.Fatal(out.String())
	}
	boxRootGroups = func() []string { return nil }
	out.Reset()
	teamUp("payments", &out)
	if strings.Contains(out.String(), "gpasswd") {
		t.Fatal(out.String())
	}
}

func TestTeamRemove(t *testing.T) {
	dir := setupTeamTest(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	var calls []string
	var removed []string
	savedSystemctl, savedContainers := teamSystemctl, teamRemoveContainers
	teamSystemctl = func(args ...string) (string, error) { calls = append(calls, strings.Join(args, " ")); return "", nil }
	teamRemoveContainers = func(name string, boxes []string) { removed = append(removed, name) }
	t.Cleanup(func() { teamSystemctl, teamRemoveContainers = savedSystemctl, savedContainers })
	setup := func() string {
		folder := filepath.Join(dir, "team-payments")
		os.MkdirAll(folder, 0o700)
		if err := teamRegister("payments", folder); err != nil {
			t.Fatal(err)
		}
		saveTeam(testTeamConfig())
		os.MkdirAll(filepath.Dir(teamUnitPath("payments")), 0o755)
		os.WriteFile(teamUnitPath("payments"), []byte("[Unit]\n"), 0o644)
		return folder
	}

	folder := setup()
	var out bytes.Buffer
	if err := teamRemove([]string{"payments"}, strings.NewReader("y\n"), &out); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatal("removed without asking:", err)
	}
	if err := teamRemove([]string{"payments", "--keep-folder", "--yes"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	if !fileExists(filepath.Join(folder, "team.json")) || fileExists(teamUnitPath("payments")) || fileExists(filepath.Join(teamsDir(), "payments")) {
		t.Fatal("--keep-folder should remove only what is outside the folder")
	}
	if strings.Join(calls, "; ") != "disable --now hi-team-payments.service; disable --now hi-team-payments-snapshot.timer; daemon-reload" || len(removed) != 1 {
		t.Fatal(calls, removed)
	}
	if !strings.Contains(out.String(), "hi team up "+folder) {
		t.Fatal(out.String())
	}

	os.RemoveAll(folder)
	folder = setup()
	if err := teamRemove([]string{"payments", "--yes"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	if fileExists(folder) {
		t.Fatal("the folder is still there")
	}
	if err := teamRemove([]string{"payments", "--yes"}, strings.NewReader(""), &out); err == nil {
		t.Fatal("removed a team that isn't there")
	}
}

func TestTeamLeadCommandsNeedTheBox(t *testing.T) {
	t.Setenv(teamSocketEnv, "")
	var out, errs bytes.Buffer
	if code := runTeam([]string{"versions"}, strings.NewReader(""), &out, &errs); code != 1 || !strings.Contains(errs.String(), "for a team's lead, in its box") {
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
	// files are more files a box commits, by box and path.
	files map[string]map[string]string
	// sessions has hi agent --session keep a session, as a real run does.
	sessions bool
	// oldImages are the app boxes on hi's base image from before a rebuild.
	oldImages map[string]bool
	// exposures are the addresses hi net expose gave, by name; lasts is
	// how long the next one lasts.
	exposures map[string]netExposeState
	lasts     time.Duration
	// domainFails makes hi net expose --domain fail, as for a domain
	// NetBird doesn't have.
	domainFails bool
	// apps are the app boxes started, as "name command port".
	apps []string
}

func newTeamTestBroker(t *testing.T) *teamTestBroker {
	t.Helper()
	config := testTeamConfig()
	dir := teamDir("payments")
	for _, sub := range []string{"tasks", "lead"} {
		os.MkdirAll(filepath.Join(dir, sub), 0o700)
	}
	if err := newTeamRepo(config, filepath.Join(dir, "repo"), true); err != nil {
		t.Fatal(err)
	}
	saveTeam(config)
	b := &teamTestBroker{teamBroker: newTeamBroker("payments", "hi"), reports: map[string]string{}, exposures: map[string]netExposeState{}, lasts: 24 * time.Hour}
	b.app = func(slot, name, src, data, address, command string, port, hostPort int) error {
		if !fileExists(data) {
			t.Error("the app's box gets no data folder")
		}
		if !fileExists(filepath.Join(src, "Makefile")) {
			t.Error("the app's copy has no Makefile")
		}
		if !fileExists(filepath.Join(address, "address")) {
			t.Error("the app's box gets no address file")
		}
		b.mu.Lock()
		b.apps = append(b.apps, fmt.Sprintf("%s %s %d", name, command, port))
		b.mu.Unlock()
		server := &http.Server{Addr: fmt.Sprintf("127.0.0.1:%d", hostPort), Handler: http.NotFoundHandler()}
		go server.ListenAndServe()
		t.Cleanup(func() { server.Close() })
		return nil
	}
	b.oldImage = func(container string) bool {
		b.mu.Lock()
		defer b.mu.Unlock()
		return b.oldImages[container]
	}
	b.running = func(container string) bool {
		b.mu.Lock()
		defer b.mu.Unlock()
		for _, app := range b.apps {
			if strings.HasPrefix(app, container+" ") {
				return true
			}
		}
		return false
	}
	b.removeApp = func(container string) {
		b.mu.Lock()
		defer b.mu.Unlock()
		var kept []string
		for _, app := range b.apps {
			if !strings.HasPrefix(app, container+" ") {
				kept = append(kept, app)
			}
		}
		b.apps = kept
	}
	b.exposed = func(name string) (netExposeState, bool) {
		if teamIsInternal(name) {
			return netExposeState{Name: name}, true
		}
		b.mu.Lock()
		defer b.mu.Unlock()
		state, ok := b.exposures[name]
		return state, ok
	}
	b.run = func(args ...string) ([]byte, error) {
		b.mu.Lock()
		defer b.mu.Unlock()
		b.calls = append(b.calls, strings.Join(args, " "))
		switch {
		case args[0] == "net" && args[2] == "stop":
			delete(b.exposures, args[3])
			return nil, nil
		case args[0] == "net":
			prefix, domain, key := "", "", "k"
			for i, arg := range args {
				switch arg {
				case "--name":
					prefix = args[i+1]
				case "--domain":
					domain = args[i+1]
				case "--key":
					key = args[i+1]
				}
			}
			name := fmt.Sprintf("%s-x%d", prefix, len(b.calls))
			host := name + ".example.net"
			if domain != "" {
				if b.domainFails {
					return nil, errors.New("NetBird doesn't have it")
				}
				host = domain
			}
			state := netExposeState{Name: name, URL: "https://" + host, Link: "https://" + host + "/?hi_key=" + key, Expires: time.Now().Add(b.lasts)}
			b.exposures[name] = state
			return json.Marshal(state)
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
			// hi agent --session keeps the member's session in the folder.
			for i, arg := range args {
				if arg == "--session" && b.sessions {
					model := ""
					for j, arg := range args {
						if arg == "--model" {
							model = args[j+1]
						}
					}
					dir := args[i+1]
					os.MkdirAll(filepath.Join(dir, agentSessionFiles), 0o700)
					os.WriteFile(filepath.Join(dir, agentSessionFiles, "s.jsonl"), []byte("{}\n"), 0o600)
					state, _ := json.Marshal(agentSessionState{Agent: args[1], Model: model, ID: "session-" + args[1], File: "s.jsonl"})
					os.WriteFile(filepath.Join(dir, agentSessionStateFile), state, 0o600)
				}
			}
			if args[1] != "codex" { // the reviewer commits nothing
				work := filepath.Join(t.TempDir(), "w")
				exec.Command("git", "-C", repo, "worktree", "add", "-q", work, branch).Run()
				os.WriteFile(filepath.Join(work, box+".txt"), []byte(box), 0o644)
				// Coders mark features done in the lead's specs, too.
				os.MkdirAll(filepath.Join(work, "docs", "specs"), 0o755)
				os.WriteFile(filepath.Join(work, "docs", "specs", "suppliers.md"), []byte("Status: Done\n"), 0o644)
				os.WriteFile(filepath.Join(work, "docs", "specs", box+".md"), []byte("mine\n"), 0o644)
				for name, content := range b.files[box] {
					os.MkdirAll(filepath.Dir(filepath.Join(work, name)), 0o755)
					os.WriteFile(filepath.Join(work, name), []byte(content), 0o644)
				}
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

func TestTeamBrokerSocket(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	b.channel = &fakeTeamChannel{}
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

	// The lead files what the people ask for, and reads it back; the
	// owners close tickets.
	var out bytes.Buffer
	if err := runTeamLeadCommand("ticket", []string{"add", "The chart misses the last day", "Asked by Anna in the channel."}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	if tickets := loadTeamLocalTickets("payments"); len(tickets) != 1 || tickets[0].Author != "the lead" {
		t.Fatalf("%+v", tickets)
	}
	out.Reset()
	if err := runTeamLeadCommand("ticket", []string{"list"}, strings.NewReader(""), &out); err != nil || !strings.Contains(out.String(), "The chart misses the last day") {
		t.Fatal(out.String(), err)
	}
	if err := runTeamLeadCommand("ticket", []string{"close", "1"}, strings.NewReader(""), &out); err == nil || !strings.Contains(err.Error(), "owners close") {
		t.Fatal(err)
	}
	// What people shared in the thread goes with the ticket, without the
	// lead asking; the thread's first message isn't logged yet, and hi
	// reads the channel then. A body in a file is the file's text.
	channel := teamChannelDir("payments")
	os.MkdirAll(filepath.Join(channel, "files"), 0o700)
	thread := teamSlackTS(time.Now().Add(-time.Minute))
	share := func(ts, reply, name, data string) {
		os.WriteFile(filepath.Join(channel, "files", ts+"-"+name), []byte(data), 0o600)
		teamAppendChannel(channel, []teamChannelMessage{{TS: ts, Thread: reply, Name: "Anna", Text: "like this", Files: []teamChannelFile{{Name: name, Path: "files/" + ts + "-" + name}}}})
	}
	polled := 0
	b.pollChannel = func() error { polled++; share(thread, "", "waves.png", "png one"); return nil }
	t.Setenv("HERMES_SESSION_CHAT_ID", "C07PAYMENTS")
	t.Setenv("HERMES_SESSION_THREAD_ID", thread)
	body := filepath.Join(dir, "ticket_body.txt")
	os.WriteFile(body, []byte("Waves, not a grid.\n"), 0o600)
	if err := runTeamLeadCommand("ticket", []string{"add", "Organic waves", body}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	if shown, _ := teamTicketDo("payments", teamTicketRequest{Action: "show", Number: 2}); polled != 1 || shown.Ticket.Body != "Waves, not a grid." || len(shown.Ticket.Files) != 1 {
		t.Fatalf("%d %+v", polled, shown.Ticket)
	}
	// A comment brings what was shared since, once.
	share(teamSlackTS(time.Now()), thread, "colors.png", "png two")
	for range 2 {
		if err := runTeamLeadCommand("ticket", []string{"comment", "2", "More colour."}, strings.NewReader(""), &out); err != nil {
			t.Fatal(err)
		}
	}
	if files := teamTicketFileNames("payments", 2); polled != 1 || len(files) != 2 {
		t.Fatalf("%d %v", polled, files)
	}
	// task is the command's name; ticket, from before 2.5.14, still works.
	out.Reset()
	if err := runTeamLeadCommand("task", []string{"list"}, strings.NewReader(""), &out); err != nil || !strings.Contains(out.String(), "Tasks (") {
		t.Fatal(out.String(), err)
	}
	// A SOUL.md that says hi team task isn't taken for one from before 2.4.
	soul := filepath.Join(dir, "SOUL.md")
	os.WriteFile(soul, []byte(teamSoul(testTeamConfig())+"\nFile it with hi team task add.\n"), 0o600)
	if err := updateTeamSoul(soul, testTeamConfig()); err != nil || fileExists(filepath.Join(dir, "SOUL.before-2.4.md")) {
		t.Fatal("a new SOUL.md was replaced", err)
	}
	// A SOUL.md from 2.4 on gets hi's paragraphs as they are now (shaping
	// tasks, starting one); what the owners wrote in it stays.
	current := teamSoul(testTeamConfig())
	for _, rewrite := range teamSoulRewrites {
		if !strings.Contains(current, rewrite[1]) {
			t.Fatalf("SOUL.md hasn't %q", rewrite[1])
		}
		os.WriteFile(soul, []byte(strings.Replace(current, rewrite[1], rewrite[0], 1)+"\nThe owners' own words.\n"), 0o600)
		if err := updateTeamSoul(soul, testTeamConfig()); err != nil {
			t.Fatal(err)
		}
		if data, _ := os.ReadFile(soul); string(data) != current+"\nThe owners' own words.\n" {
			t.Fatalf("not rewritten from %q:\n%s", rewrite[0], data)
		}
	}
	if usage := teamLeadUsage(); !strings.Contains(usage, "  hi team task start 12\n      When an owner or a developer asks") || !strings.Contains(usage, "<body|body file|->") {
		t.Fatal(usage)
	}
	// What they asked for changed: the lead gives the ticket its new text.
	if err := runTeamLeadCommand("ticket", []string{"edit", "2", "--title", "Organic waves, in colour", "-"}, strings.NewReader("Waves, in colour.\n"), &out); err != nil {
		t.Fatal(err)
	}
	if shown, _ := teamTicketDo("payments", teamTicketRequest{Action: "show", Number: 2}); shown.Ticket.Title != "Organic waves, in colour" || shown.Ticket.Body != "Waves, in colour." {
		t.Fatalf("%+v", shown.Ticket)
	}
	if err := runTeamLeadCommand("ticket", []string{"edit", "2", "Only the text."}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	if shown, _ := teamTicketDo("payments", teamTicketRequest{Action: "show", Number: 2}); shown.Ticket.Title != "Organic waves, in colour" || shown.Ticket.Body != "Only the text." {
		t.Fatalf("%+v", shown.Ticket)
	}
	if err := runTeamLeadCommand("ticket", []string{"add", "Elsewhere", "/box/home/nothing.txt"}, strings.NewReader(""), &out); err == nil {
		t.Fatal("a body that's a missing file's path")
	}
	// In someone's DM, the channel's threads aren't the conversation.
	t.Setenv("HERMES_SESSION_CHAT_ID", "D07ANNA")
	if err := runTeamLeadCommand("ticket", []string{"add", "From a DM", "Text."}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	if files := teamTicketFileNames("payments", 3); len(files) != 0 {
		t.Fatal(files)
	}
	out.Reset()
	if err := runTeamLeadCommand("versions", nil, strings.NewReader(""), &out); err != nil || !strings.Contains(out.String(), "nothing released yet") {
		t.Fatal(out.String(), err)
	}
	// The lead hands out no work.
	if teamLeadCommands["task"] || teamLeadCommands["merge"] || teamLeadCommands["release"] {
		t.Fatal("the lead still has the Slack loop's commands")
	}
}

func TestTeamMovedFolder(t *testing.T) {
	dir := setupTeamTest(t)
	t.Setenv("HI_TEAM_SLACK_BOT_TOKEN", "xoxb-bot")
	t.Setenv("HI_TEAM_SLACK_APP_TOKEN", "xapp-app")
	file := filepath.Join(dir, "answers.json")
	writeSkillTestFile(t, file, `{"purpose": "x", "channel": "C07PAYMENTS", "owners": ["U07OWNER1"], "empty": true}`, 0o600)
	var out bytes.Buffer
	if err := teamNew([]string{"payments", "--dir", "elsewhere/pay", "--from", file}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	// "empty": only the README, the rules, and the specs folder.
	if files := boxGit(filepath.Join(teamDir("payments"), "repo"), "ls-files"); files != "AGENTS.md\nREADME.md\ndocs/specs/README.md" {
		t.Fatal(files)
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
	answers := "\nx\nxoxb-bot\nxapp-app\n#payments\n"
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
	writeSkillTestFile(t, filepath.Join(dir, "bun.lock"), `{}`, 0o644)
	if command, _ := teamRunCommand(dir); command != "bun install --frozen-lockfile && bun run start" {
		t.Fatal(command)
	}
	writeSkillTestFile(t, filepath.Join(dir, "Makefile"), "test:\n\tgo test\nrun: build\n\t./app\n", 0o644)
	if command, _ := teamRunCommand(dir); command != "make run" {
		t.Fatal(command)
	}
}

func TestWriteTeamSkillKeepsTheLeadsEdits(t *testing.T) {
	dir := t.TempDir()
	path, notes := filepath.Join(dir, "SKILL.md"), filepath.Join(dir, "notes.md")
	read := func(path string) string { data, _ := os.ReadFile(path); return string(data) }
	writeTeamSkill(dir, "# hi\n\nUse hi team task.", notes)
	if read(path) != "# hi\n\nUse hi team task." {
		t.Fatal(read(path))
	}
	edited := "# hi\n\nUse hi team task.\n\nPreviews can't reach the model proxy."
	os.WriteFile(path, []byte(edited), 0o600)
	writeTeamSkill(dir, "# hi\n\nUse hi team task.", notes)
	if read(path) != edited {
		t.Fatal("the lead's edit was lost on a restart")
	}
	// A new text replaces it; what the lead added moves to its notes.
	writeTeamSkill(dir, "# hi v2\n\nUse hi team task and wait.", notes)
	if read(path) != "# hi v2\n\nUse hi team task and wait." || read(path+".lead") != edited {
		t.Fatal(read(path), read(path+".lead"))
	}
	if got := read(notes); !strings.Contains(got, "## From hi-team, before hi updated it") || !strings.Contains(got, "\n\nPreviews can't reach the model proxy.\n") || strings.Contains(got, "Use hi team task") {
		t.Fatal(got)
	}
	// Unedited, a new text replaces it without a copy.
	os.Remove(path + ".lead")
	before := read(notes)
	writeTeamSkill(dir, "hi v3", notes)
	if read(path) != "hi v3" || fileExists(path+".lead") || read(notes) != before {
		t.Fatal(read(path))
	}
	// Without hi's earlier text, as for a team from an older hi, the
	// lead's version is only kept aside.
	os.Remove(filepath.Join(dir, ".hi-text"))
	os.WriteFile(path, []byte("hi v3\n\nA note."), 0o600)
	writeTeamSkill(dir, "hi v4", notes)
	if read(path) != "hi v4" || read(path+".lead") != "hi v3\n\nA note." || read(notes) != before {
		t.Fatal(read(notes))
	}
}

func TestTeamPreview(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	if _, err := b.preview(teamPreviewRequest{}); err == nil || !strings.Contains(err.Error(), "no way to start") {
		t.Fatal(err)
	}
	commit := func(file string) {
		os.WriteFile(filepath.Join(b.repo, file), []byte("run:\n\t./serve\n"), 0o644)
		exec.Command("git", "-C", b.repo, "add", "-A").Run()
		exec.Command("git", "-C", b.repo, "commit", "-q", "-m", file).Run()
	}
	commit("Makefile")
	portFile := teamSlotPortFile("payments", teamSlotPreview)
	exposed := func() []string {
		var out []string
		for _, call := range b.calls {
			if strings.HasPrefix(call, "net expose") {
				out = append(out, call)
			}
		}
		return out
	}

	// The first preview gets an address, for a day, that follows the
	// port file.
	first, err := b.preview(teamPreviewRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if !first.NewLink || !strings.HasSuffix(first.URL, "?hi_key=k") || first.Source != "main" || !strings.HasSuffix(b.apps[0], " make run 8080") {
		t.Fatalf("%+v %q", first, b.apps)
	}
	if calls := exposed(); len(calls) != 1 || !strings.Contains(calls[0], fmt.Sprintf("net expose %d --follow %s --link --detach --json --max 24h0m0s --name team-payments", first.HostPort, portFile)) {
		t.Fatal(calls)
	}
	if data, _ := os.ReadFile(portFile); strings.TrimSpace(string(data)) != fmt.Sprint(first.HostPort) {
		t.Fatalf("port file %q", data)
	}

	// The next one is on the same link: the address moves to the new box.
	commit("more")
	second, err := b.preview(teamPreviewRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if second.NewLink || second.URL != first.URL || second.HostPort == first.HostPort || second.Commit == first.Commit || len(exposed()) != 1 {
		t.Fatalf("%+v %q", second, exposed())
	}
	if data, _ := os.ReadFile(portFile); strings.TrimSpace(string(data)) != fmt.Sprint(second.HostPort) {
		t.Fatalf("port file %q", data)
	}

	// With less than half a day left, a preview gets a new address, and
	// the old one ends.
	b.mu.Lock()
	state := b.exposures[second.Expose]
	state.Expires = time.Now().Add(2 * time.Hour)
	b.exposures[second.Expose] = state
	b.mu.Unlock()
	saved, _ := loadTeamSlot("payments", teamSlotPreview)
	saved.Expires = state.Expires
	saveTeamSlot("payments", saved)
	commit("again")
	third, err := b.preview(teamPreviewRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if !third.NewLink || third.URL == first.URL || !strings.Contains(strings.Join(exposed(), "\n"), "net expose stop "+first.Expose) {
		t.Fatalf("%+v %q", third, exposed())
	}

	// A preview whose address has had its day is stopped.
	b.mu.Lock()
	delete(b.exposures, third.Expose)
	b.mu.Unlock()
	third.Expires = time.Now()
	saveTeamSlot("payments", third)
	done := make(chan struct{})
	close(done)
	b.keepLinks(done, io.Discard)
	if fileExists(teamSlotPath("payments", teamSlotPreview)) || fileExists(portFile) {
		t.Fatal("the ended preview is still there")
	}
	if _, err := b.preview(teamPreviewRequest{}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.preview(teamPreviewRequest{Stop: true}); err != nil || fileExists(teamSlotPath("payments", teamSlotPreview)) {
		t.Fatal(err)
	}
}

func TestTeamStartPreviewOnlyForTheFreshApp(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	var stderr bytes.Buffer
	apps := func() int {
		b.mu.Lock()
		defer b.mu.Unlock()
		return len(b.apps)
	}
	// An empty project waits for the lead.
	b.startPreview(&stderr)
	if apps() != 0 {
		t.Fatal(b.apps)
	}
	// The blank app hi team new writes goes on the preview, once.
	os.RemoveAll(b.repo)
	config, _ := loadTeam("payments")
	if err := newTeamRepo(config, b.repo, false); err != nil {
		t.Fatal(err)
	}
	b.startPreview(&stderr)
	if apps() != 1 || !strings.HasSuffix(b.apps[0], " make run 8080") {
		t.Fatal(b.apps, stderr.String())
	}
	if state, ok := loadTeamSlot("payments", teamSlotPreview); !ok || state.URL == "" {
		t.Fatalf("%+v", state)
	}
	b.startPreview(&stderr)
	if apps() != 1 {
		t.Fatal("a second preview of the same app")
	}
	commit := func(file string) {
		os.MkdirAll(filepath.Dir(filepath.Join(b.repo, file)), 0o755)
		os.WriteFile(filepath.Join(b.repo, file), []byte("x\n"), 0o644)
		exec.Command("git", "-C", b.repo, "add", "-A").Run()
		exec.Command("git", "-C", b.repo, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", file).Run()
	}
	// The lead's specs don't make it someone's work.
	commit("docs/specs/suppliers.md")
	b.preview(teamPreviewRequest{Stop: true})
	b.startPreview(&stderr)
	if apps() != 2 {
		t.Fatal("the app with only specs added wasn't previewed")
	}
	// Once work is merged, the lead makes the previews.
	commit("src/suppliers.ts")
	b.preview(teamPreviewRequest{Stop: true})
	b.startPreview(&stderr)
	if apps() != 2 || stderr.Len() != 0 {
		t.Fatal(b.apps, stderr.String())
	}
}

func TestTeamProductionLink(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	channel := &fakeTeamChannel{}
	b.channel = channel
	if _, err := b.preview(teamPreviewRequest{Production: true}); err == nil || !strings.Contains(err.Error(), "nothing is released") {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(b.repo, "Makefile"), []byte("run:\n\t./serve\n"), 0o644)
	exec.Command("git", "-C", b.repo, "add", "-A").Run()
	exec.Command("git", "-C", b.repo, "commit", "-q", "-m", "make run").Run()
	teamTestMerge(t, b, "Suppliers")

	// The release puts production on its own address, and hi posts its
	// link in the channel, and the link again in the release's message.
	if _, err := b.release(teamReleaseRequest{host: "Olga", Notes: "Suppliers"}); err != nil {
		t.Fatal(err)
	}
	first, ok := loadTeamSlot("payments", teamSlotProduction)
	if !ok || first.Label != "production v1" || first.Message == "" || !strings.Contains(first.Expose, "team-payments-live") {
		t.Fatalf("%+v", first)
	}
	// The app reads its address from a file hi keeps current.
	address := func() string {
		data, _ := os.ReadFile(filepath.Join(teamSlotAddressDir("payments", teamSlotProduction), "address"))
		return string(data)
	}
	if address() != first.URL+"\n" {
		t.Fatalf("the app's address file has %q", address())
	}
	calls := strings.Join(channel.calls, "\n")
	if !strings.Contains(calls, "*Production v1* is at <"+first.URL+"|this link>") || !strings.Contains(calls, "post C07PAYMENTS New PROD version (v1) :rocket: -> App: <"+first.URL+"|link> - Docs: <"+teamDocsLink(first.URL)+"|link>") {
		t.Fatal(calls)
	}

	// The next release shows on the same link; hi edits its message.
	teamTestMerge(t, b, "Search")
	if _, err := b.release(teamReleaseRequest{host: "Olga", Notes: "Search"}); err != nil {
		t.Fatal(err)
	}
	second, _ := loadTeamSlot("payments", teamSlotProduction)
	if second.URL != first.URL || second.Message != first.Message || second.Label != "production v2" {
		t.Fatalf("%+v", second)
	}
	if !strings.Contains(strings.Join(channel.calls, "\n"), "update "+testTeamConfig().Channel+" "+first.Message+" :globe_with_meridians: *Production v2*") {
		t.Fatal(channel.calls)
	}
	if b.productionNeedsWork() {
		t.Fatal("production needs work right after a release")
	}

	// Before the address ends, hi gives production a new one and edits
	// its message.
	b.mu.Lock()
	state := b.exposures[second.Expose]
	state.Expires = time.Now().Add(10 * time.Minute)
	b.exposures[second.Expose] = state
	b.mu.Unlock()
	second.Expires = state.Expires
	saveTeamSlot("payments", second)
	if !b.productionNeedsWork() {
		t.Fatal("production's address is about to end")
	}
	done := make(chan struct{})
	close(done)
	b.keepLinks(done, io.Discard)
	third, _ := loadTeamSlot("payments", teamSlotProduction)
	// The box that runs keeps running; only the address is new.
	if third.URL == second.URL || third.Container != second.Container || third.Message != first.Message || !strings.Contains(channel.calls[len(channel.calls)-1], "<"+third.URL+"|this link>") {
		t.Fatalf("%+v %q", third, channel.calls)
	}
	if address() != third.URL+"\n" {
		t.Fatalf("the app's address file has %q after the new address", address())
	}

	// A new setting starts production again, on the same link.
	saveTeamSecrets("payments", teamSecrets{Env: map[string]string{"WEATHER_API_KEY": "w-1"}})
	if !b.productionNeedsWork() {
		t.Fatal("production doesn't have the new setting")
	}
	b.keepLinks(done, io.Discard)
	fourth, _ := loadTeamSlot("payments", teamSlotProduction)
	if fourth.URL != third.URL || fourth.Container == third.Container || fourth.Settings != teamSettingsStamp("payments") || b.productionNeedsWork() {
		t.Fatalf("%+v", fourth)
	}

	// hi team rebuild built the base image again: production moves to it,
	// on the same link.
	b.mu.Lock()
	b.oldImages = map[string]bool{fourth.Container: true}
	b.mu.Unlock()
	if !b.productionNeedsWork() {
		t.Fatal("production stays on the old image")
	}
	b.keepLinks(done, io.Discard)
	fifth, _ := loadTeamSlot("payments", teamSlotProduction)
	if fifth.URL != fourth.URL || fifth.Container == fourth.Container || b.productionNeedsWork() {
		t.Fatalf("%+v", fifth)
	}
}

// hi team rebuild builds the image again and restarts the team on it,
// and names the machine's other teams still on the old one.
func TestTeamRebuild(t *testing.T) {
	setupTeamTest(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	os.MkdirAll(teamDir("payments"), 0o700)
	saveTeam(testTeamConfig())
	other := testTeamConfig()
	other.Name = "billing"
	os.MkdirAll(teamDir("billing"), 0o700)
	saveTeam(other)
	var calls []string
	savedSystemctl, savedLinger, savedImage := teamSystemctl, teamLingers, teamRebuildImage
	teamSystemctl = func(args ...string) (string, error) {
		calls = append(calls, strings.Join(args, " "))
		if args[0] == "is-active" {
			return "active", nil
		}
		return "", nil
	}
	teamLingers = func() bool { return true }
	t.Cleanup(func() { teamSystemctl, teamLingers, teamRebuildImage = savedSystemctl, savedLinger, savedImage })
	teamRebuildImage = func(stdout, stderr io.Writer) error {
		calls = append(calls, "build")
		return nil
	}
	var out bytes.Buffer
	if err := teamRebuild("payments", &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	if len(calls) < 4 || calls[0] != "build" || !slices.Contains(calls, "restart hi-team-payments.service") {
		t.Fatal(calls)
	}
	if !strings.Contains(out.String(), "hi team dev payments restart") || !strings.Contains(out.String(), "so billing still run the old one") {
		t.Fatal(out.String())
	}
	// A failed build restarts nothing.
	calls = nil
	teamRebuildImage = func(stdout, stderr io.Writer) error { return errors.New("no network") }
	if err := teamRebuild("payments", &out, io.Discard); err == nil || len(calls) != 0 {
		t.Fatal(err, calls)
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
	// They are committed, so the dev box's clone has them.
	if err := b.commitSpecs(); err != nil {
		t.Fatal(err)
	}
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
	if err := teamCheckGitHub("octocat/world-clock"); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := teamConnectGitHub(&config, "octocat/world-clock", &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(calls[len(calls)-1], "repo create octocat/world-clock --private --source . --remote origin --push") {
		t.Fatal(calls)
	}
	if saved, _ := loadTeam("payments"); saved.Remote != "https://github.com/octocat/world-clock.git" {
		t.Fatal(saved.Remote)
	}
	if boxGit(bare, "rev-parse", "main") != boxGit(b.repo, "rev-parse", "main") {
		t.Fatal("not pushed")
	}
	// Connected once is enough.
	if err := teamConnectGitHub(&config, "octocat/other", &out); err == nil || !strings.Contains(err.Error(), "already pushes to") {
		t.Fatal(err)
	}
}

func TestRefreshTeamLeadPrompts(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("no python3")
	}
	lead := t.TempDir()
	if n, err := refreshTeamLeadPrompts(lead); n != 0 || err != nil {
		t.Fatalf("no state.db: %d %v", n, err)
	}
	os.WriteFile(filepath.Join(lead, "SOUL.md"), []byte("# The lead\n\n## Brainstorms\n"), 0o600)
	script := `
import sqlite3, sys
db = sqlite3.connect(sys.argv[1])
db.executescript("""
create table system_prompts (hash text primary key, prompt text);
create table sessions (id text, system_prompt text, system_prompt_hash text);
insert into system_prompts values ('old', 'You are Hermes.\n# The lead\n');
insert into system_prompts values ('new', 'You are Hermes.\n# The lead\n\n## Brainstorms\nSkills.');
insert into sessions values ('dm', null, 'old');
insert into sessions values ('thread', null, 'new');
insert into sessions values ('legacy', 'You are Hermes.\n# The lead\n', null);
insert into sessions values ('unbuilt', null, null);
""")
db.commit()
`
	db := filepath.Join(lead, "state.db")
	if out, err := exec.Command("python3", "-c", script, db).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	n, err := refreshTeamLeadPrompts(lead)
	if err != nil || n != 2 {
		t.Fatalf("refreshed %d: %v", n, err)
	}
	out, _ := exec.Command("python3", "-c", `
import sqlite3, sys
db = sqlite3.connect(sys.argv[1])
print(",".join(f"{r[0]}={r[1]}" for r in db.execute("select id, coalesce(system_prompt_hash, system_prompt, '-') from sessions order by id")))
print(",".join(r[0] for r in db.execute("select hash from system_prompts order by hash")))
`, db).Output()
	if got := strings.TrimSpace(string(out)); got != "dm=-,legacy=-,thread=new,unbuilt=-\nnew" {
		t.Fatalf("after:\n%s", got)
	}
	if n, err := refreshTeamLeadPrompts(lead); n != 0 || err != nil {
		t.Fatalf("again: %d %v", n, err)
	}
}

func TestTeamWaitApp(t *testing.T) {
	// Nothing listens here.
	listener, _ := net.Listen("tcp", "127.0.0.1:0")
	url := "http://" + listener.Addr().String() + "/"
	listener.Close()
	start := time.Now()
	if err := teamWaitApp(url, func() bool { return false }, time.Hour); !errors.Is(err, errTeamAppStopped) || time.Since(start) > 30*time.Second {
		t.Fatalf("a stopped app: %v after %s", err, time.Since(start))
	}
	if err := teamWaitApp(url, func() bool { return true }, time.Millisecond); err == nil || errors.Is(err, errTeamAppStopped) {
		t.Fatalf("a running app past the limit: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer server.Close()
	if err := teamWaitApp(server.URL, func() bool { return false }, time.Millisecond); err != nil {
		t.Fatal(err)
	}
}

// Past a dozen reads of the code in a turn, the lead's plugin says to hand
// them on; other tools, and another turn, are left alone.
func TestTeamLeadPluginNotesReads(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("no python3")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "plugin.py"), teamLeadPluginSource, 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("python3", "-I", "-c", `
import importlib.util, sys
spec = importlib.util.spec_from_file_location("plugin", sys.argv[1])
plugin = importlib.util.module_from_spec(spec)
spec.loader.exec_module(plugin)
hooks = {}
class Ctx:
    def register_command(self, *a, **k): pass
    def register_hook(self, name, callback): hooks[name] = callback
plugin.register(Ctx())
note = hooks["transform_tool_result"]
results = [note(tool_name="search_files", result="r", turn_id="t1") for _ in range(13)]
print(results[11] is None, results[12].startswith("r\n\n[hi: that is 13 reads"))
print(note(tool_name="terminal", result="r", turn_id="t1") is None)
print(note(tool_name="read_file", result="r", turn_id="t2") is None)
`, filepath.Join(dir, "plugin.py")).CombinedOutput()
	if err != nil || string(out) != "True True\nTrue\nTrue\n" {
		t.Fatalf("%s: %v", out, err)
	}
}

func TestTeamManifestYAML(t *testing.T) {
	config := testTeamConfig()
	lead := config.Members["lead"]
	lead.Name = "Maja Ström"
	config.Members["lead"] = lead
	manifest := teamManifestYAML(config)
	if strings.Count(manifest, "\n") >= strings.Count(teamManifest(config), "\n")/2 {
		t.Errorf("the YAML is %d lines, the JSON %d", strings.Count(manifest, "\n"), strings.Count(teamManifest(config), "\n"))
	}
	// Slack reads it as it reads the JSON.
	cmd := exec.Command("python3", "-c", "import json, sys, yaml; print(json.dumps(yaml.safe_load(sys.stdin), sort_keys=True))")
	cmd.Stdin = strings.NewReader(manifest)
	out, err := cmd.Output()
	if err != nil {
		t.Skip("no python3 with yaml:", err)
	}
	var fromYAML, fromJSON any
	json.Unmarshal(out, &fromYAML)
	json.Unmarshal([]byte(teamManifest(config)), &fromJSON)
	if !reflect.DeepEqual(fromYAML, fromJSON) {
		t.Fatalf("%v\n%s", fromYAML, manifest)
	}
	if !strings.Contains(manifest, "display_name: maja-strom") || !strings.Contains(manifest, "chat with Maja Ström, the payments team's lead") {
		t.Fatal(manifest)
	}
	for name, err := range map[string]bool{"Maja": false, "A name far longer than Slack keeps for an app": true, "@maja": true, " Maja": true} {
		if (teamCheckLeadName(name) != nil) != err {
			t.Error(name)
		}
	}
}
