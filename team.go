package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/slack-go/slack"
)

// hi team runs a virtual team of agents in one Slack channel: a lead
// (Hermes) that talks with the people and keeps the project's memory, and
// members (Claude Code, Codex, Hermes) it hands tasks to, each in a box of
// its own. A team is a folder; its containers are rebuilt from it. See
// docs/specs/ideas/hi_team.md.

var (
	teamNamePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,19}$`)
	teamRolePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,15}$`)
	slackIDPattern  = regexp.MustCompile(`^[A-Z0-9]{6,20}$`)
)

// teamConfig is team.json. It holds no secrets.
type teamConfig struct {
	Name    string                `json:"name"`
	Purpose string                `json:"purpose"`
	Channel string                `json:"channel"`
	Owners  []string              `json:"owners"`
	Askers  []string              `json:"askers,omitempty"` // empty: everyone in the channel
	Members map[string]teamMember `json:"members"`
	Remote  string                `json:"remote,omitempty"` // where repo/ was cloned from
	Branch  string                `json:"branch"`           // the branch the lead merges into
	Memory  string                `json:"memory,omitempty"` // "off": Hermes' built-in memory only, no Hindsight
	Created time.Time             `json:"created"`
}

// teamMember is one role: an agent, its model, and what its box may use.
type teamMember struct {
	Agent   string   `json:"agent"`
	Model   string   `json:"model,omitempty"`
	Bundle  string   `json:"bundle,omitempty"`
	Network string   `json:"network,omitempty"` // a member box's network preset; dev by default
	Allow   []string `json:"allow,omitempty"`
}

// teamSecrets is secrets.json, readable only by its owner.
type teamSecrets struct {
	SlackBotToken string `json:"slack_bot_token"`
	SlackAppToken string `json:"slack_app_token"`
}

func teamsDir() string {
	if dir := os.Getenv("HI_TEAMS_DIR"); dir != "" {
		return dir
	}
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(base, "hi", "teams")
}

// A team's folder is wherever it was made, ./team-<name> by default.
// teamsDir holds a link to each, by name, so hi team <command> <name>
// finds it from anywhere.
func teamDir(name string) string {
	link := filepath.Join(teamsDir(), name)
	if target, err := filepath.EvalSymlinks(link); err == nil {
		return target
	}
	return link
}

// teamRegister links a team's name to its folder.
func teamRegister(name, folder string) error {
	if err := os.MkdirAll(teamsDir(), 0o700); err != nil {
		return err
	}
	link := filepath.Join(teamsDir(), name)
	if info, err := os.Lstat(link); err == nil {
		if info.Mode()&os.ModeSymlink == 0 {
			return fmt.Errorf("%s is in the way: it should be a link to team %s's folder", link, name)
		}
		os.Remove(link)
	}
	return os.Symlink(folder, link)
}

func teamUnitName(name string) string { return "hi-team-" + name + ".service" }

// defaultTeamMembers is the roster the wizard offers: the reviewer is a
// different agent from the coder, so the code is checked by someone other
// than its author.
func defaultTeamMembers() map[string]teamMember {
	lead := readHermesModelConfig(filepath.Join(hermesHome(), "config.yaml")).Default
	return map[string]teamMember{
		"lead":     {Agent: "hermes", Model: lead},
		"coder":    {Agent: "claude", Model: "opus"},
		"reviewer": {Agent: "codex"},
	}
}

func validateTeam(config teamConfig) error {
	if !teamNamePattern.MatchString(config.Name) {
		return fmt.Errorf("a team's name starts with a letter and has lowercase letters, digits, and dashes, up to 20: %q", config.Name)
	}
	if !slackIDPattern.MatchString(config.Channel) {
		return fmt.Errorf("the channel is a Slack channel ID, such as C07ABC123, not %q", config.Channel)
	}
	if len(config.Owners) == 0 {
		return errors.New("a team needs at least one owner")
	}
	for _, id := range append(append([]string{}, config.Owners...), config.Askers...) {
		if !slackIDPattern.MatchString(id) {
			return fmt.Errorf("people are Slack member IDs, such as U07ABC123, not %q", id)
		}
	}
	if config.Members["lead"].Agent != "hermes" {
		return errors.New("the lead is Hermes")
	}
	for role, member := range config.Members {
		if !teamRolePattern.MatchString(role) {
			return fmt.Errorf("a role is a short lowercase name, such as coder, not %q", role)
		}
		if !agentKinds[member.Agent] {
			return fmt.Errorf("%s: the agent is claude, codex, or hermes, not %q", role, member.Agent)
		}
		if member.Model != "" && !agentModelName.MatchString(member.Model) {
			return fmt.Errorf("%s: %q isn't a model name", role, member.Model)
		}
		if _, ok := boxPresets[member.Network]; member.Network != "" && !ok {
			return fmt.Errorf("%s: the network is locked, dev, or open, not %q", role, member.Network)
		}
	}
	coder, hasCoder := config.Members["coder"]
	reviewer, hasReviewer := config.Members["reviewer"]
	if hasCoder && hasReviewer && coder.Agent == reviewer.Agent && coder.Model == reviewer.Model {
		return errors.New("the reviewer must be a different agent or model from the coder")
	}
	return nil
}

func loadTeam(name string) (teamConfig, error) {
	var config teamConfig
	if !teamNamePattern.MatchString(name) {
		return config, fmt.Errorf("no team named %q", name)
	}
	data, err := os.ReadFile(filepath.Join(teamDir(name), "team.json"))
	if errors.Is(err, os.ErrNotExist) {
		if target, linkErr := os.Readlink(filepath.Join(teamsDir(), name)); linkErr == nil {
			return config, fmt.Errorf("team %s's folder %s is gone; if you moved it, run hi team up <its new folder>", name, target)
		}
		return config, fmt.Errorf("no team named %s; hi team ls lists them", name)
	}
	if err != nil {
		return config, err
	}
	if err := json.Unmarshal(data, &config); err != nil {
		return config, fmt.Errorf("team.json: %w", err)
	}
	return config, nil
}

func saveTeam(config teamConfig) error {
	data, _ := json.MarshalIndent(config, "", "  ")
	return os.WriteFile(filepath.Join(teamDir(config.Name), "team.json"), append(data, '\n'), 0o600)
}

func loadTeamSecrets(name string) (teamSecrets, error) {
	var secrets teamSecrets
	data, err := os.ReadFile(filepath.Join(teamDir(name), "secrets.json"))
	if err != nil {
		return secrets, err
	}
	return secrets, json.Unmarshal(data, &secrets)
}

func saveTeamSecrets(name string, secrets teamSecrets) error {
	data, _ := json.MarshalIndent(secrets, "", "  ")
	return os.WriteFile(filepath.Join(teamDir(name), "secrets.json"), append(data, '\n'), 0o600)
}

// ---------------------------------------------------------------------------
// the command

func runTeam(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		printTeamUsage(stdout)
		return 0
	}
	command, rest := args[0], args[1:]
	// The lead's own commands, inside its box.
	if command == "channel" {
		return exitCode(runTeamChannel(rest, stdout), stderr)
	}
	if teamLeadCommands[command] {
		return exitCode(runTeamLeadCommand(command, rest, stdin, stdout), stderr)
	}
	var err error
	switch command {
	case "new":
		err = teamNew(rest, stdin, stdout)
	case "up":
		if len(rest) == 0 && interactive(stdin, stdout) {
			name, pickErr := teamPick(stdin, stdout, "Start or restart a team")
			if errors.Is(pickErr, errMenuBack) {
				return 0
			}
			if pickErr != nil {
				return exitCode(pickErr, stderr)
			}
			rest = []string{name}
		}
		err = withTeamName(rest, func(name string) error {
			// A folder instead of a name: a team that was moved, or copied
			// from another machine.
			if strings.ContainsRune(name, '/') || name == "." {
				var adoptErr error
				if name, adoptErr = teamAdopt(name, stdout); adoptErr != nil {
					return adoptErr
				}
			}
			return teamUp(name, stdout)
		})
	case "down":
		err = withTeamName(rest, func(name string) error { return teamDown(name, stdout) })
	case "rm", "remove":
		err = teamRemove(rest, stdin, stdout)
	case "ls", "list":
		err = teamList(stdout)
	case "status":
		err = withTeamName(rest, func(name string) error { return teamStatus(name, stdout) })
	case "logs":
		err = teamLogs(rest, stdout, stderr)
	case "github":
		if len(rest) != 2 {
			err = usageError{"usage: hi team github <name> <owner/repository>"}
			break
		}
		config, loadErr := loadTeam(rest[0])
		if err = loadErr; err == nil {
			if err = teamCheckGitHub(rest[1]); err == nil {
				err = teamConnectGitHub(&config, rest[1], stdout)
			}
		}
	case "manifest":
		err = withTeamName(rest, func(name string) error {
			config, err := loadTeam(name)
			if err == nil {
				fmt.Fprintln(stdout, teamManifest(config))
			}
			return err
		})
	case "__serve":
		err = withTeamName(rest, func(name string) error { return teamServe(name, stdout, stderr) })
	default:
		fmt.Fprintf(stderr, "hi: unknown team command %q\n\n", command)
		printTeamUsage(stderr)
		return 2
	}
	return exitCode(err, stderr)
}

func withTeamName(args []string, action func(string) error) error {
	if len(args) != 1 {
		return usageError{"name the team: hi team <command> <name>"}
	}
	return action(args[0])
}

func printTeamUsage(w io.Writer) {
	fmt.Fprintln(w, `hi team runs a virtual team of agents in one Slack channel: a lead
(Hermes) that talks with the people in plain language and keeps the
project's memory, and members it hands tasks to (a coder, a reviewer),
each in a box of its own.

usage:
  hi team new <name>               a step-by-step setup: Slack app, channel, people, members, code,
                                   in a new folder ./team-<name>
      [--dir <folder>]             the team's folder somewhere else
      [--from <file.json>]         the answers from a file; the Slack tokens from
                                   HI_TEAM_SLACK_BOT_TOKEN and HI_TEAM_SLACK_APP_TOKEN
  hi team up [<name>]              start or restart the team and keep it up, also after a reboot;
                                   without a name, pick it from a list
  hi team up <folder>              the same for a team whose folder moved or was copied here
  hi team down <name>              stop it; its folder stays
  hi team rm [<name>]              stop it and remove it from this machine: its service, boxes,
                                   and folder; without a name, pick it from a list
      [--keep-folder]              keep the folder; hi team up <folder> brings the team back
      [--yes]                      don't ask first
  hi team ls                       teams on this machine
  hi team status <name>            whether it is up, its members, and its tasks
  hi team logs <name> [<task>] [-f]  the lead's log, or a task's; -f follows it
  hi team channel <name> [--since 2h]  what was said in the channel (the last day by default)
  hi team manifest <name>          the Slack app manifest again
  hi team github <name> <owner/repo>  put the project on GitHub (a new private repository, or an
                                   empty one); the team pushes every merge there

A team is its folder. hi finds it by name through `+teamsDir()+`.`)
}

// ---------------------------------------------------------------------------
// the wizard

// teamSlackChecker checks tokens and the channel with Slack; tests replace
// it.
type teamSlackChecker interface {
	botIdentity(botToken string) (workspace, botUser string, err error)
	checkAppToken(appToken string) error
	channel(botToken, channel string) (name string, member bool, err error)
}

var teamSlack teamSlackChecker = liveTeamSlack{}

type liveTeamSlack struct{}

func (liveTeamSlack) botIdentity(botToken string) (string, string, error) {
	response, err := slack.New(botToken).AuthTest()
	if err != nil {
		return "", "", err
	}
	return response.Team, response.UserID, nil
}

func (liveTeamSlack) checkAppToken(appToken string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_, _, err := slack.New("", slack.OptionAppLevelToken(appToken)).StartSocketModeContext(ctx)
	return err
}

func (liveTeamSlack) channel(botToken, channel string) (string, bool, error) {
	info, err := slack.New(botToken).GetConversationInfo(&slack.GetConversationInfoInput{ChannelID: channel})
	if err != nil {
		return "", false, err
	}
	return info.Name, info.IsMember, nil
}

// teamAnswers is what --from reads: team.json's fields, and a repository
// to clone instead of a new one.
type teamAnswers struct {
	teamConfig
	Clone  string `json:"clone,omitempty"`
	GitHub string `json:"github,omitempty"` // owner/name of a new private repository
	// checked: the wizard checked Slack as it went.
	checked bool
}

func teamNew(args []string, stdin io.Reader, stdout io.Writer) error {
	var name, from, folder string
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--from" && i+1 < len(args):
			i++
			from = args[i]
		case strings.HasPrefix(args[i], "--from="):
			from = strings.TrimPrefix(args[i], "--from=")
		case args[i] == "--dir" && i+1 < len(args):
			i++
			folder = args[i]
		case strings.HasPrefix(args[i], "--dir="):
			folder = strings.TrimPrefix(args[i], "--dir=")
		case strings.HasPrefix(args[i], "-"):
			return usageError{"unknown option " + args[i]}
		case name == "":
			name = args[i]
		default:
			return usageError{"usage: hi team new <name> [--dir <folder>] [--from <file.json>]"}
		}
	}
	if name == "" {
		return usageError{"usage: hi team new <name> [--dir <folder>] [--from <file.json>]"}
	}
	if !teamNamePattern.MatchString(name) {
		return fmt.Errorf("a team's name starts with a letter and has lowercase letters, digits, and dashes, up to 20: %q", name)
	}
	if _, err := loadTeam(name); err == nil {
		return fmt.Errorf("team %s already exists in %s", name, teamDir(name))
	}
	dir, err := filepath.Abs(firstNonEmpty(folder, "team-"+name))
	if err != nil {
		return err
	}
	if fileExists(dir) {
		return fmt.Errorf("%s already exists; choose another folder with --dir", dir)
	}
	if err := hermesInstalled(); err != nil {
		return err
	}

	var answers teamAnswers
	var secrets teamSecrets
	if from != "" {
		answers, secrets, err = teamAnswersFromFile(name, from)
	} else {
		answers, secrets, err = teamWizard(name, dir, stdin, stdout)
	}
	if err != nil {
		return err
	}
	config := answers.teamConfig
	config.Name, config.Created = name, time.Now().UTC()
	if err := validateTeam(config); err != nil {
		return err
	}
	if !answers.checked {
		if err := teamCheckSlack(config, secrets, stdout); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := teamRegister(name, dir); err != nil {
		os.RemoveAll(dir)
		return err
	}
	if err := createTeamFolder(&config, secrets, answers.Clone, stdout); err != nil {
		os.RemoveAll(dir)
		os.Remove(filepath.Join(teamsDir(), name))
		return err
	}
	if answers.GitHub != "" {
		if err := teamConnectGitHub(&config, answers.GitHub, stdout); err != nil {
			fmt.Fprintf(stdout, "The team is set up, but not on GitHub: %v\nTry again with: hi team github %s %s\n", err, name, answers.GitHub)
		}
	}
	fmt.Fprintf(stdout, "\nTeam %s is ready in %s.\n", name, dir)
	for _, role := range teamRoles(config) {
		member := config.Members[role]
		fmt.Fprintf(stdout, "  %-10s %s\n", role, teamMemberLabel(member))
	}
	fmt.Fprintf(stdout, "\nStart it with:\n  hi team up %s\n", name)
	return nil
}

func teamAnswersFromFile(name, path string) (teamAnswers, teamSecrets, error) {
	var answers teamAnswers
	data, err := os.ReadFile(path)
	if err != nil {
		return answers, teamSecrets{}, err
	}
	if err := json.Unmarshal(data, &answers); err != nil {
		return answers, teamSecrets{}, fmt.Errorf("%s: %w", path, err)
	}
	if answers.Members == nil {
		answers.Members = defaultTeamMembers()
	}
	secrets := teamSecrets{SlackBotToken: os.Getenv("HI_TEAM_SLACK_BOT_TOKEN"), SlackAppToken: os.Getenv("HI_TEAM_SLACK_APP_TOKEN")}
	if secrets.SlackBotToken == "" || secrets.SlackAppToken == "" {
		return answers, secrets, errors.New("set HI_TEAM_SLACK_BOT_TOKEN and HI_TEAM_SLACK_APP_TOKEN to the Slack app's tokens")
	}
	return answers, secrets, nil
}

func teamWizard(name, dir string, stdin io.Reader, stdout io.Writer) (teamAnswers, teamSecrets, error) {
	var answers teamAnswers
	var secrets teamSecrets
	config := &answers.teamConfig
	config.Name = name
	var err error

	fmt.Fprintf(stdout, "Setting up team %s in %s. Each answer is checked as you give it.\n\n", name, dir)

	fmt.Fprintln(stdout, "1/6 Purpose")
	fmt.Fprintln(stdout, "In one sentence, what is this team for? The lead starts from it.")
	if config.Purpose, err = teamAsk(stdin, stdout, "Purpose", ""); err != nil {
		return answers, secrets, err
	}

	fmt.Fprintln(stdout, "\n2/6 Slack app")
	fmt.Fprintln(stdout, "Each team has a Slack app of its own. Create it from this manifest:")
	fmt.Fprintln(stdout, "  api.slack.com/apps → Create New App → From a manifest → paste it → Create,")
	fmt.Fprintln(stdout, "  then Install to Workspace.")
	fmt.Fprintln(stdout)
	fmt.Fprintln(stdout, teamManifest(*config))
	fmt.Fprintln(stdout)
	fmt.Fprintln(stdout, "The bot token is under OAuth & Permissions (xoxb-…). The app token is under")
	fmt.Fprintln(stdout, "Basic Information → App-Level Tokens: generate one with the connections:write scope (xapp-…).")
	workspace := ""
	if secrets.SlackBotToken, err = teamAskUntil(stdin, stdout, "Bot token", true, func(token string) error {
		workspace, err = teamCheckBotToken(token)
		return err
	}); err != nil {
		return answers, secrets, err
	}
	if secrets.SlackAppToken, err = teamAskUntil(stdin, stdout, "App token", true, teamCheckAppToken); err != nil {
		return answers, secrets, err
	}
	fmt.Fprintf(stdout, "Slack: both tokens work, in workspace %s.\n", workspace)

	fmt.Fprintln(stdout, "\n3/6 Channel")
	fmt.Fprintln(stdout, "Invite the app to the team's channel first: /invite @"+teamBotName(name))
	fmt.Fprintln(stdout, "Then copy the channel's ID: click the channel's name; the ID is at the bottom (C…).")
	channel := ""
	if config.Channel, err = teamAskUntil(stdin, stdout, "Channel ID", false, func(id string) error {
		channel, err = teamCheckChannel(name, secrets.SlackBotToken, id)
		return err
	}); err != nil {
		return answers, secrets, err
	}
	fmt.Fprintf(stdout, "Slack: the app is in #%s.\n", channel)

	fmt.Fprintln(stdout, "\n4/6 People")
	fmt.Fprintln(stdout, "Owners approve what matters, such as merging and stopping the team. Give Slack member IDs,")
	fmt.Fprintln(stdout, "separated by commas: a person's profile → ⋯ → Copy member ID (U…).")
	owners, err := teamAskUntil(stdin, stdout, "Owners", false, teamCheckPeople)
	if err != nil {
		return answers, secrets, err
	}
	config.Owners = splitList(owners)
	fmt.Fprintln(stdout, "Who else may ask the team for work? Enter lets everyone in the channel.")
	askers, err := teamAskUntil(stdin, stdout, "Others", false, func(answer string) error {
		if answer == "" {
			return nil
		}
		return teamCheckPeople(answer)
	})
	if err != nil {
		return answers, secrets, err
	}
	config.Askers = splitList(askers)

	fmt.Fprintln(stdout, "\n5/6 Members")
	fmt.Fprintln(stdout, "Each role's agent (claude, codex, or hermes) and model. Enter keeps what's offered.")
	config.Members = defaultTeamMembers()
	for _, role := range teamRoles(*config) {
		member := config.Members[role]
		offered := member.Agent + teamModelSuffix(member.Model)
		answer, err := teamAskUntil(stdin, stdout, fmt.Sprintf("%-8s [%s]", role, offered), false, func(answer string) error {
			agent, model, _ := strings.Cut(firstNonEmpty(answer, offered), ":")
			trial := *config
			trial.Members = map[string]teamMember{}
			for other, existing := range config.Members {
				trial.Members[other] = existing
			}
			trial.Members[role] = teamMember{Agent: strings.TrimSpace(agent), Model: strings.TrimSpace(model)}
			if role == "lead" && trial.Members[role].Agent != "hermes" {
				return errors.New("the lead is Hermes")
			}
			for other, m := range trial.Members {
				if other == role {
					if !agentKinds[m.Agent] {
						return fmt.Errorf("the agent is claude, codex, or hermes, not %q", m.Agent)
					}
					if m.Model != "" && !agentModelName.MatchString(m.Model) {
						return fmt.Errorf("%q isn't a model name", m.Model)
					}
				}
			}
			if coder, reviewer := trial.Members["coder"], trial.Members["reviewer"]; role == "reviewer" && coder.Agent == reviewer.Agent && coder.Model == reviewer.Model {
				return errors.New("the reviewer must be a different agent or model from the coder")
			}
			return nil
		})
		if err != nil {
			return answers, secrets, err
		}
		agent, model, _ := strings.Cut(firstNonEmpty(answer, offered), ":")
		member.Agent, member.Model = strings.TrimSpace(agent), strings.TrimSpace(model)
		config.Members[role] = member
	}

	fmt.Fprintln(stdout, "\n6/6 Code")
	fmt.Fprintln(stdout, "The team keeps its own copy of the project in "+filepath.Join(dir, "repo")+".")
	fmt.Fprintln(stdout, "Enter starts a new git repository there. Or give a repository to clone: a URL, cloned with this")
	fmt.Fprintln(stdout, "machine's git credentials, or a folder on this machine.")
	clone, err := teamAskUntil(stdin, stdout, "Repository [new]", false, func(string) error { return nil })
	if err != nil {
		return answers, secrets, err
	}
	if clone != "new" {
		answers.Clone = clone
	}
	if answers.Clone == "" {
		fmt.Fprintln(stdout, "Put it on GitHub too? Give owner/name, such as imanhabib/world-clock, and hi creates a private")
		fmt.Fprintln(stdout, "repository with this machine's GitHub sign-in (gh); the team pushes every merge there.")
		if answers.GitHub, err = teamAskUntil(stdin, stdout, "GitHub [Enter keeps it on this machine]", false, func(name string) error {
			if name == "" {
				return nil
			}
			return teamCheckGitHub(name)
		}); err != nil {
			return answers, secrets, err
		}
	}
	answers.checked = true
	return answers, secrets, nil
}

// teamAskUntil asks until check accepts the answer; Enter gives "". In a
// terminal a wrong answer is asked again; otherwise it ends the setup.
func teamAskUntil(stdin io.Reader, stdout io.Writer, question string, secret bool, check func(string) error) (string, error) {
	for attempt := 1; ; attempt++ {
		var answer string
		var err error
		if secret {
			answer, err = teamAskSecret(stdin, stdout, question)
		} else {
			fmt.Fprintf(stdout, "%s: ", question)
			var line string
			line, err = readLine(stdin)
			if !isTerminal(stdin) {
				fmt.Fprintln(stdout)
			}
			if errors.Is(err, io.EOF) && line != "" {
				err = nil
			}
			answer = strings.TrimSpace(stripTerminalCodes(line))
		}
		if err == nil {
			if err = check(answer); err == nil {
				return answer, nil
			}
		}
		if !isTerminal(stdin) || attempt == 5 || errors.Is(err, io.EOF) {
			return "", err
		}
		fmt.Fprintf(stdout, "  %v. Try again.\n", err)
	}
}

func teamCheckBotToken(token string) (string, error) {
	if !strings.HasPrefix(token, "xoxb-") {
		return "", errors.New("the bot token starts with xoxb-; it is under OAuth & Permissions")
	}
	workspace, _, err := teamSlack.botIdentity(token)
	if err != nil {
		return "", fmt.Errorf("Slack refused the bot token: %w", err)
	}
	return workspace, nil
}

func teamCheckAppToken(token string) error {
	if !strings.HasPrefix(token, "xapp-") {
		return errors.New("the app token starts with xapp-; generate one under Basic Information → App-Level Tokens")
	}
	if err := teamSlack.checkAppToken(token); err != nil {
		return fmt.Errorf("Slack refused the app token (it needs connections:write, and Socket Mode on): %w", err)
	}
	return nil
}

func teamCheckChannel(team, botToken, id string) (string, error) {
	if !slackIDPattern.MatchString(id) {
		return "", fmt.Errorf("a channel ID looks like C07ABC123, not %q", id)
	}
	channel, member, err := teamSlack.channel(botToken, id)
	if err != nil {
		return "", fmt.Errorf("the app can't see channel %s (invite it with /invite @%s): %w", id, teamBotName(team), err)
	}
	if !member {
		return "", fmt.Errorf("the app isn't in #%s yet; invite it there with /invite @%s", channel, teamBotName(team))
	}
	return channel, nil
}

func teamCheckPeople(answer string) error {
	ids := splitList(answer)
	if len(ids) == 0 {
		return errors.New("give at least one member ID")
	}
	for _, id := range ids {
		if !slackIDPattern.MatchString(id) || (!strings.HasPrefix(id, "U") && !strings.HasPrefix(id, "W")) {
			return fmt.Errorf("a member ID looks like U07ABC123, not %q", id)
		}
	}
	return nil
}

// stripTerminalCodes removes escape sequences a terminal may send, such as
// the markers around a paste.
func stripTerminalCodes(text string) string {
	return terminalCodes.ReplaceAllString(text, "")
}

var terminalCodes = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b`)

func teamModelSuffix(model string) string {
	if model == "" {
		return ""
	}
	return ":" + model
}

func teamMemberLabel(member teamMember) string {
	label := agentDisplayName(member.Agent)
	if member.Model != "" {
		label += ", " + member.Model
	}
	if member.Bundle != "" {
		label += ", bundle " + member.Bundle
	}
	return label
}

// teamRoles lists the lead first, then the others by name.
func teamRoles(config teamConfig) []string {
	roles := []string{}
	for role := range config.Members {
		if role != "lead" {
			roles = append(roles, role)
		}
	}
	sort.Strings(roles)
	if _, ok := config.Members["lead"]; ok {
		roles = append([]string{"lead"}, roles...)
	}
	return roles
}

func splitList(text string) []string {
	var out []string
	for _, part := range strings.FieldsFunc(text, func(r rune) bool { return r == ',' || r == ' ' }) {
		out = append(out, part)
	}
	return out
}

// teamAsk asks one question; Enter takes the fallback.
func teamAsk(stdin io.Reader, stdout io.Writer, question, fallback string) (string, error) {
	if fallback != "" {
		fmt.Fprintf(stdout, "%s [%s]: ", question, fallback)
	} else {
		fmt.Fprintf(stdout, "%s: ", question)
	}
	line, err := readLine(stdin)
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	if !isTerminal(stdin) {
		fmt.Fprintln(stdout)
	}
	if line = strings.TrimSpace(stripTerminalCodes(line)); line != "" {
		return line, nil
	}
	if fallback != "" {
		return fallback, nil
	}
	if errors.Is(err, io.EOF) {
		return "", fmt.Errorf("no answer for %s", strings.ToLower(question))
	}
	return teamAsk(stdin, stdout, question, fallback)
}

// teamAskSecret reads a token without showing it.
func teamAskSecret(stdin io.Reader, stdout io.Writer, label string) (string, error) {
	fmt.Fprintf(stdout, "%s (paste it; it shows as *): ", label)
	var value string
	if file, ok := stdin.(*os.File); ok && isTerminal(stdin) {
		secret, err := readSecret(file, stdout)
		fmt.Fprintln(stdout)
		if err != nil {
			return "", err
		}
		value = string(secret)
	} else {
		line, err := readLine(stdin)
		fmt.Fprintln(stdout)
		if err != nil && !errors.Is(err, io.EOF) {
			return "", err
		}
		value = line
	}
	if value = strings.TrimSpace(stripTerminalCodes(value)); value == "" {
		return "", fmt.Errorf("no %s was entered", strings.ToLower(label))
	}
	return value, nil
}

// teamCheckSlack checks both tokens and that the bot is in the channel.
func teamCheckSlack(config teamConfig, secrets teamSecrets, stdout io.Writer) error {
	workspace, err := teamCheckBotToken(secrets.SlackBotToken)
	if err != nil {
		return err
	}
	if err := teamCheckAppToken(secrets.SlackAppToken); err != nil {
		return err
	}
	channel, err := teamCheckChannel(config.Name, secrets.SlackBotToken, config.Channel)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Slack: workspace %s, channel #%s.\n", workspace, channel)
	return nil
}

// createTeamFolder makes the folder, the repository, and the lead's first
// files.
func createTeamFolder(config *teamConfig, secrets teamSecrets, clone string, stdout io.Writer) error {
	dir := teamDir(config.Name)
	for _, sub := range []string{"lead", "tasks", "run", "logs"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o700); err != nil {
			return err
		}
	}
	if err := saveTeamSecrets(config.Name, secrets); err != nil {
		return err
	}
	// In case the folder is inside a repository or a synced folder.
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("secrets.json\nrun/\n"), 0o600); err != nil {
		return err
	}
	repo := filepath.Join(dir, "repo")
	if clone != "" {
		fmt.Fprintf(stdout, "Cloning %s…\n", clone)
		if out, err := exec.Command("git", "clone", "-q", clone, repo).CombinedOutput(); err != nil {
			return fmt.Errorf("git clone: %s", qFirstLine(string(out), err.Error()))
		}
		// The team pushes merges back to a URL. A folder on this machine is
		// usually someone's working copy, which git won't push into, so its
		// owner pulls from the team instead.
		if info, err := os.Stat(clone); err == nil && info.IsDir() {
			fmt.Fprintf(stdout, "The team works on its own copy; get its work with: git pull %s %s\n", repo, firstNonEmpty(boxGit(repo, "branch", "--show-current"), "main"))
		} else {
			config.Remote = clone
		}
		if boxGit(repo, "rev-parse", "--verify", "-q", "HEAD") == "" {
			return errors.New("the repository has no commits yet; push a first commit, or let the team start a new one")
		}
	} else if err := newTeamRepo(*config, repo); err != nil {
		return err
	}
	config.Branch = firstNonEmpty(boxGit(repo, "branch", "--show-current"), "main")
	if err := saveTeam(*config); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "slack-manifest.json"), []byte(teamManifest(*config)+"\n"), 0o600); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "lead", "SOUL.md"), []byte(teamSoul(*config)), 0o600)
}

func newTeamRepo(config teamConfig, repo string) error {
	if err := os.MkdirAll(repo, 0o755); err != nil {
		return err
	}
	files := map[string]string{
		"README.md":                  "# " + config.Name + "\n\n" + config.Purpose + "\n",
		"AGENTS.md":                  teamAgentsFile,
		teamSpecsPath + "/README.md": teamSpecsReadme(config),
	}
	for name, content := range files {
		os.MkdirAll(filepath.Dir(filepath.Join(repo, name)), 0o755)
		if err := os.WriteFile(filepath.Join(repo, name), []byte(content), 0o644); err != nil {
			return err
		}
	}
	author := []string{"-c", "user.name=" + firstNonEmpty(boxGit(".", "config", "user.name"), "hi team "+config.Name),
		"-c", "user.email=" + firstNonEmpty(boxGit(".", "config", "user.email"), "team-"+config.Name+"@hi.invalid")}
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"add", "-A"}, append(author, "commit", "-q", "-m", "Start the "+config.Name+" team's project")} {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			return fmt.Errorf("git %s: %s", args[0], qFirstLine(string(out), err.Error()))
		}
	}
	return nil
}

const teamAgentsFile = `# Working on this project

This project is built by a hi team: a lead that talks with the people who
use it, and agents that each do one task at a time. The people may not read
code, so the code, its tests, and its docs must explain themselves.

- docs/specs/ is the lead's: what was agreed with the people, feature by
  feature. Read it; don't change it.
- Work only on the task in your brief. If something else needs doing, say
  so in your report's follow-ups.
- Every change comes with tests for what the brief says must work. Run them
  before you finish, and say in your report whether they pass.
- The project starts with ` + "`make run`" + `: a Makefile run target that starts the
  app on the port in $PORT (8080 if it's unset), listening on 0.0.0.0, so
  the lead can show the people a preview.
- Commit your work on your branch with a message that says what changed and
  why. Never push, and never change the main branch yourself: the lead
  merges after a review.
- Keep README.md current: what the project does, how to run it, how to
  test it.
`

// teamBotName is the bot's handle in Slack.
func teamBotName(name string) string { return "team-" + name }

// ---------------------------------------------------------------------------
// up, down, ls, status, logs

func teamUnit(config teamConfig, executable string) string {
	env := ""
	if dir := os.Getenv("HI_TEAMS_DIR"); dir != "" {
		env += "Environment=HI_TEAMS_DIR=" + dir + "\n"
	}
	// The agents, git, and podman are found as in the shell that ran hi team up.
	env += "Environment=PATH=" + os.Getenv("PATH") + "\n"
	return fmt.Sprintf(`[Unit]
Description=hi team %[1]s
After=network-online.target
Wants=network-online.target

[Service]
%[2]sExecStart=%[3]s team __serve %[1]s
Restart=always
RestartSec=10
TimeoutStopSec=60
# Only hi team __serve gets the stop signal; it stops the lead itself.
KillMode=mixed

[Install]
WantedBy=default.target
`, config.Name, env, executable)
}

// teamSystemctl runs systemctl --user; tests replace it.
var teamSystemctl = func(args ...string) (string, error) {
	out, err := exec.Command("systemctl", append([]string{"--user"}, args...)...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func teamUnitPath(name string) string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "systemd", "user", teamUnitName(name))
}

func teamUp(name string, stdout io.Writer) error {
	config, err := loadTeam(name)
	if err != nil {
		return err
	}
	if err := hermesInstalled(); err != nil {
		return err
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	if resolved, err := filepath.EvalSymlinks(executable); err == nil {
		executable = resolved
	}
	unit := teamUnitPath(name)
	if err := os.MkdirAll(filepath.Dir(unit), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(unit, []byte(teamUnit(config, executable)), 0o644); err != nil {
		return err
	}
	for _, args := range [][]string{{"daemon-reload"}, {"enable", teamUnitName(name)}, {"restart", teamUnitName(name)}} {
		if out, err := teamSystemctl(args...); err != nil {
			return fmt.Errorf("systemctl --user %s: %s", strings.Join(args, " "), qFirstLine(out, err.Error()))
		}
	}
	fmt.Fprintf(stdout, "Team %s is starting. It answers in its Slack channel in a minute or so.\n", name)
	fmt.Fprintf(stdout, "  hi team status %s   whether it is up\n  hi team logs %s     the lead's log\n", name, name)
	if !teamLingers() {
		fmt.Fprintf(stdout, "\nIt stops when you log out, and doesn't start at boot, until you run:\n  sudo loginctl enable-linger %s\n", currentUserName())
	}
	return nil
}

// teamLingers reports whether this user's services run without a login.
var teamLingers = func() bool {
	out, err := exec.Command("loginctl", "show-user", currentUserName(), "--property=Linger", "--value").Output()
	return err == nil && strings.TrimSpace(string(out)) == "yes"
}

// teamAdopt registers the team in a folder under its name: a team that
// was moved, or copied from another machine.
func teamAdopt(folder string, stdout io.Writer) (string, error) {
	dir, err := filepath.Abs(folder)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(filepath.Join(dir, "team.json"))
	if err != nil {
		return "", fmt.Errorf("%s isn't a team's folder: %w", dir, err)
	}
	var config teamConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return "", fmt.Errorf("team.json: %w", err)
	}
	if !teamNamePattern.MatchString(config.Name) {
		return "", fmt.Errorf("team.json has no valid name: %q", config.Name)
	}
	if current := teamDir(config.Name); current != dir && fileExists(filepath.Join(current, "team.json")) {
		return "", fmt.Errorf("another team named %s is in %s", config.Name, current)
	}
	if err := teamRegister(config.Name, dir); err != nil {
		return "", err
	}
	fmt.Fprintf(stdout, "Team %s is in %s.\n", config.Name, dir)
	return config.Name, nil
}

func teamDown(name string, stdout io.Writer) error {
	if _, err := loadTeam(name); err != nil {
		return err
	}
	if out, err := teamSystemctl("disable", "--now", teamUnitName(name)); err != nil && fileExists(teamUnitPath(name)) {
		return fmt.Errorf("systemctl --user disable --now %s: %s", teamUnitName(name), qFirstLine(out, err.Error()))
	}
	fmt.Fprintf(stdout, "Team %s is stopped. Its folder stays; hi team up %s starts it again.\n", name, name)
	return nil
}

// teamRemoveContainers removes a team's containers and its members'
// boxes; tests replace it.
var teamRemoveContainers = func(name string, boxes []string) {
	engine, _, err := detectBoxEngine()
	if err != nil {
		return
	}
	engine.output("rm", "-f", teamPreviewContainer(name))
	engine.output("rm", "-f", teamMemoryContainer(name))
	teardownBox(engine, boxMeta{Name: "team-" + name})
	for _, box := range boxes {
		if meta, err := loadBoxMeta(box); err == nil {
			removeBox(engine, meta, true, io.Discard)
		}
	}
}

// teamRemove takes a team off this machine: what hi made for it outside
// its folder (the service, the link by name, the boxes) and, unless
// --keep-folder, the folder. The Slack app, the channel, and a GitHub
// repository are not hi's to remove.
func teamRemove(args []string, stdin io.Reader, stdout io.Writer) error {
	var name string
	var keep, yes bool
	for _, arg := range args {
		switch {
		case arg == "--keep-folder":
			keep = true
		case arg == "--yes" || arg == "-y":
			yes = true
		case strings.HasPrefix(arg, "-"):
			return usageError{"unknown option " + arg}
		case name == "":
			name = arg
		default:
			return usageError{"usage: hi team rm [<name>] [--keep-folder] [--yes]"}
		}
	}
	if name == "" {
		if !interactive(stdin, stdout) {
			return usageError{"usage: hi team rm <name> [--keep-folder] [--yes]"}
		}
		picked, err := teamPick(stdin, stdout, "Remove a team")
		if errors.Is(err, errMenuBack) {
			return nil
		}
		if err != nil {
			return err
		}
		name = picked
	}
	if !teamNamePattern.MatchString(name) {
		return fmt.Errorf("no team named %q", name)
	}
	link := filepath.Join(teamsDir(), name)
	_, linkErr := os.Lstat(link)
	folder := ""
	if config, err := loadTeam(name); err == nil {
		folder = teamDir(config.Name)
	} else if linkErr != nil && !fileExists(teamUnitPath(name)) {
		return err
	}
	var boxes []string
	for _, task := range loadTeamTasks(name) {
		if task.Box != "" && fileExists(boxStateFile(task.Box)) {
			boxes = append(boxes, task.Box)
		}
	}

	fmt.Fprintf(stdout, "Removing team %s stops it and deletes:\n", name)
	fmt.Fprintf(stdout, "  its service       %s\n", teamUnitPath(name))
	fmt.Fprintf(stdout, "  its name          %s\n", link)
	if len(boxes) > 0 {
		fmt.Fprintf(stdout, "  its members' boxes  %s\n", strings.Join(boxes, ", "))
	}
	if folder != "" && !keep {
		fmt.Fprintf(stdout, "  its folder        %s  (the project, its tasks, the channel's log, the lead's memory)\n", folder)
	}
	if err := confirm(stdin, stdout, yes, "Remove team "+name+"?"); err != nil {
		return err
	}

	if out, err := teamSystemctl("disable", "--now", teamUnitName(name)); err != nil && fileExists(teamUnitPath(name)) {
		return fmt.Errorf("systemctl --user disable --now %s: %s", teamUnitName(name), qFirstLine(out, err.Error()))
	}
	if err := os.Remove(teamUnitPath(name)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	teamSystemctl("daemon-reload")
	teamRemoveContainers(name, boxes)
	if err := os.Remove(link); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if folder != "" && !keep {
		if err := os.RemoveAll(folder); err != nil {
			return fmt.Errorf("the team is off this machine, but not all of its folder is deleted: %w", err)
		}
	}
	fmt.Fprintf(stdout, "Team %s is removed.", name)
	if folder != "" && keep {
		fmt.Fprintf(stdout, " Its folder stays in %s; hi team up %s brings the team back.", folder, folder)
	}
	fmt.Fprintln(stdout, "\nIts Slack app and channel, and a GitHub repository if it had one, are still there; remove them in Slack and GitHub if you no longer want them.")
	return nil
}

func teamState(name string) string {
	out, _ := teamSystemctl("is-active", teamUnitName(name))
	if out == "" {
		return "down"
	}
	if out == "active" {
		return "up"
	}
	if out == "inactive" {
		return "down"
	}
	return out
}

// teamLeadBoxes describes the leads' boxes that are running, for hi box
// ls: hi team starts them, not hi box.
func teamLeadBoxes() []string {
	entries, err := os.ReadDir(teamsDir())
	if err != nil || len(entries) == 0 {
		return nil
	}
	engine, _, err := detectBoxEngine()
	if err != nil {
		return nil
	}
	var lines []string
	for _, entry := range entries {
		config, err := loadTeam(entry.Name())
		if err != nil {
			continue
		}
		container := "hi-box-team-" + config.Name
		if engine.state(container) != "running" {
			continue
		}
		lines = append(lines, fmt.Sprintf("%-22s team %s's lead (Hermes), channel %s, folder %s", container, config.Name, config.Channel, teamDir(config.Name)))
	}
	return lines
}

// teamPick lets you choose one of the teams on this machine from a list
// that shows whether each is up.
func teamPick(stdin io.Reader, stdout io.Writer, title string) (string, error) {
	entries, err := os.ReadDir(teamsDir())
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	var names, options []string
	for _, entry := range entries {
		config, err := loadTeam(entry.Name())
		if err != nil {
			continue
		}
		names = append(names, config.Name)
		options = append(options, fmt.Sprintf("%-20s %-6s %s", config.Name, teamState(config.Name), config.Channel))
	}
	if len(names) == 0 {
		return "", errors.New("no teams yet. hi team new <name> sets one up")
	}
	choice, err := runListSelect(stdin, stdout, title, "", options, false)
	if err != nil {
		return "", err
	}
	return names[choice], nil
}

func teamList(stdout io.Writer) error {
	entries, err := os.ReadDir(teamsDir())
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	table := tabwriter.NewWriter(stdout, 0, 0, 3, ' ', 0)
	count := 0
	for _, entry := range entries {
		config, err := loadTeam(entry.Name())
		if err != nil {
			fmt.Fprintf(table, "%s\t%s\n", entry.Name(), err)
			continue
		}
		if count == 0 {
			fmt.Fprintln(table, "TEAM\tSTATE\tCHANNEL\tMEMBERS\tFOLDER")
		}
		count++
		fmt.Fprintf(table, "%s\t%s\t%s\t%s\t%s\n", config.Name, teamState(config.Name), config.Channel, strings.Join(teamRoles(config), ", "), teamDir(config.Name))
	}
	table.Flush()
	if count == 0 {
		fmt.Fprintln(stdout, "No teams yet. hi team new <name> sets one up.")
	}
	return nil
}

func teamStatus(name string, stdout io.Writer) error {
	config, err := loadTeam(name)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Team %s: %s. Channel %s. %s\n\n", name, teamState(name), config.Channel, config.Purpose)
	for _, role := range teamRoles(config) {
		fmt.Fprintf(stdout, "  %-10s %s\n", role, teamMemberLabel(config.Members[role]))
	}
	if view, err := teamVersionsReport(name); err == nil {
		fmt.Fprintln(stdout)
		printTeamVersions(view, stdout)
	}
	tasks := loadTeamTasks(name)
	if len(tasks) == 0 {
		fmt.Fprintln(stdout, "\nNo tasks yet.")
		return nil
	}
	fmt.Fprintln(stdout)
	printTeamTasks(tasks, 15, stdout)
	return nil
}

func teamLogs(args []string, stdout, stderr io.Writer) error {
	follow := false
	var words []string
	for _, arg := range args {
		if arg == "-f" || arg == "--follow" {
			follow = true
		} else {
			words = append(words, arg)
		}
	}
	if len(words) < 1 || len(words) > 2 {
		return usageError{"usage: hi team logs <name> [<task>] [-f]"}
	}
	if _, err := loadTeam(words[0]); err != nil {
		return err
	}
	if len(words) == 2 {
		task, err := loadTeamTask(words[0], words[1])
		if err != nil {
			return err
		}
		engine, _, err := detectBoxEngine()
		if err != nil {
			return err
		}
		logs := []string{"logs"}
		if follow {
			logs = append(logs, "-f")
		}
		return engine.interactive(nil, stdout, stderr, append(logs, "hi-box-"+task.Box)...)
	}
	// Only what hi team __serve and the lead print: not Podman's events, nor
	// the copy Podman's log driver keeps.
	journal := []string{"--user", "-u", teamUnitName(words[0]), "-t", "hi", "-n", "200", "--no-pager", "-o", "cat"}
	if follow {
		journal = append(journal, "-f")
	}
	command := exec.Command("journalctl", journal...)
	command.Stdout, command.Stderr = stdout, stderr
	return command.Run()
}
