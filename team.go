package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/slack-go/slack"
)

// hi team runs a team's app and its dev box: its owners build in the dev
// box with coding agents and release from the dev page, and a lead
// (Hermes) helps the people in its Slack channel, files what they ask for
// as tickets, and watches the app. A team is a folder; its containers are
// rebuilt from it. See docs/specs/approved/hi_team_dev.md.

var (
	teamNamePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,19}$`)
	teamRolePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,15}$`)
	slackIDPattern  = regexp.MustCompile(`^[A-Z0-9]{6,20}$`)
)

// teamConfig is team.json. It holds no secrets.
type teamConfig struct {
	Name    string   `json:"name"`
	Purpose string   `json:"purpose"`
	Channel string   `json:"channel"`
	Owners  []string `json:"owners"`
	// Developers work in the dev box and on the dev page like the owners,
	// except what reaches production; Contributors report bugs as tasks.
	// Owners set both; being in the channel gives nobody a level
	// (team_access.go).
	Developers   []string              `json:"developers,omitempty"`
	Contributors []string              `json:"contributors,omitempty"`
	Members      map[string]teamMember `json:"members"`
	Remote       string                `json:"remote,omitempty"` // where repo/ was cloned from
	Branch       string                `json:"branch"`           // the branch the lead merges into
	Memory       string                `json:"memory,omitempty"` // "off": Hermes' built-in memory only, no Hindsight
	// MemoryModel is the OpenRouter model Hindsight keeps the lead's memory
	// with; this machine's Hermes default unless set (team_memory.go).
	MemoryModel string       `json:"memory_model,omitempty"`
	Backups     *teamBackups `json:"backups,omitempty"`
	// Helper turns the helper, help and feedback in the app, off for an
	// address (preview, production); on unless set (team_helper.go).
	Helper map[string]bool `json:"helper,omitempty"`
	// Public says which addresses (production, preview, dev) are public
	// too, through hi net expose; the rest are on the team's NetBird
	// network only. A team made before hi 2.5 has none: all public, until
	// an owner says otherwise (hi_team_internal.md).
	Public *teamPublic `json:"public,omitempty"`
	// Domain puts the team's app on the machine's domain (hi team domain
	// <team> on): names that stay, rather than ones NetBird makes up.
	Domain bool `json:"domain,omitempty"`
	// Addresses is what the team's addresses on the NetBird network are
	// named (team_addresses.go): "" for the machine's domain, on https,
	// when it has one (hi net domain); "netbird" for the NetBird name; or
	// a host name an IT team keeps in its DNS by hand; or "names", each
	// address at a name of its own (Names).
	Addresses string `json:"addresses,omitempty"`
	// Names are each address's own name, which an IT team keeps
	// (docs/specs/approved/hi_team_addresses.md).
	Names *teamNames `json:"names,omitempty"`
	// AllowIPs are the only addresses the team's preview and production
	// let in (hi team allow-ip <team>; hi net expose --allow-ip).
	AllowIPs []string `json:"allow_ips,omitempty"`
	// PreviewData is "empty" for a team whose preview starts empty at every
	// release; otherwise it starts on a fork of production's data
	// (team_data_fork.go). PreviewModelUSD is what the preview's model
	// calls may cost a day: 0 for teamPreviewModelUSD, below 0 for no
	// limit (team_keys.go).
	PreviewData     string  `json:"preview_data,omitempty"`
	PreviewModelUSD float64 `json:"preview_model_usd,omitempty"`
	// Template is the team template the project and the roles started
	// from, one that extends team-app, such as one from the hi server's
	// sources; empty is team-app itself.
	Template string `json:"template,omitempty"`
	// Adopted is where a project someone else wrote came from (hi team
	// adopt); ProjectAgentSettings lets members load its own agent
	// settings (hooks, MCP servers), off by default (team_adopt.go).
	Adopted              *teamAdopted `json:"adopted,omitempty"`
	ProjectAgentSettings bool         `json:"project_agent_settings,omitempty"`
	// MadeAs is the name the team was made with, when hi team rename has
	// given it another since: its memory bank and its snapshots keep it.
	MadeAs  string    `json:"made_as,omitempty"`
	Created time.Time `json:"created"`
}

// teamMember is one role: an agent, its model, and what its box may use.
type teamMember struct {
	Agent   string   `json:"agent"`
	Model   string   `json:"model,omitempty"`
	Bundle  string   `json:"bundle,omitempty"`
	Network string   `json:"network,omitempty"` // a member box's network preset; dev by default
	Allow   []string `json:"allow,omitempty"`
	// Provider is who pays for the member's model calls: openrouter
	// (empty) or chatgpt, the team's ChatGPT plan; Fallback is the same
	// model on OpenRouter, which takes over when the plan refuses
	// (team_subscriptions.go).
	Provider string `json:"provider,omitempty"`
	Fallback string `json:"fallback,omitempty"`
	// Effort is how hard the member thinks; medium unless set.
	Effort string `json:"effort,omitempty"`
	// Name is what the lead goes by in Slack, such as a person's name;
	// the team's name unless set (teamLeadName).
	Name string `json:"name,omitempty"`
}

// teamSecrets is secrets.json, readable only by its owner.
type teamSecrets struct {
	SlackBotToken string `json:"slack_bot_token"`
	SlackAppToken string `json:"slack_app_token"`
	// OpenRouterAPIKey is the team's own; without it the team uses this
	// machine's, from Hermes' .env (team_keys.go).
	OpenRouterAPIKey string `json:"openrouter_api_key,omitempty"`
	// Env is the settings the team's app boxes get (hi team env).
	Env map[string]string `json:"env,omitempty"`
	// EnvProduction and EnvPreview are settings one slot gets alone:
	// production's only (hi team env --production), or a value of the
	// preview's own (--preview). A name is in Env or in these, never both.
	EnvProduction map[string]string `json:"env_production,omitempty"`
	EnvPreview    map[string]string `json:"env_preview,omitempty"`
	// The bucket snapshots are copied to (hi team backups).
	BackupKeyID  string `json:"backup_access_key_id,omitempty"`
	BackupSecret string `json:"backup_secret_access_key,omitempty"`
	// LeadAPIKey opens Hermes' API server in the lead's box to the team's
	// service, for the helper; made on the first start.
	LeadAPIKey string `json:"lead_api_key,omitempty"`
	// ChatGPT is the team's own ChatGPT sign-in; without it, roles on
	// ChatGPT use this machine's, Codex's (team_subscriptions.go).
	ChatGPT *teamChatGPTGrant `json:"chatgpt,omitempty"`
	// ClaudeToken is the team's own, from claude setup-token; without it,
	// Claude members use this machine's sign-in.
	ClaudeToken    string    `json:"claude_token,omitempty"`
	ClaudeSignedIn time.Time `json:"claude_signed_in,omitempty"`
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
// than its author. Both get the web bundle, a browser: a team builds a web
// app, and the lead's specs ask for its pages to be looked at (on team
// orbit, 2026-10-23, a coder without one built Chromium by hand); and
// the data bundle, DuckDB and charts, for the files the app works with.
func defaultTeamMembers() map[string]teamMember {
	lead := readHermesModelConfig(filepath.Join(hermesHome(), "config.yaml")).Default
	return map[string]teamMember{
		"lead":     {Agent: "hermes", Model: lead},
		"coder":    {Agent: "claude", Model: "opus", Bundle: "web,data"},
		"reviewer": {Agent: "codex", Bundle: "web,data"},
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
	for _, id := range slices.Concat(config.Owners, config.Developers, config.Contributors) {
		if !slackIDPattern.MatchString(id) {
			return fmt.Errorf("people are Slack member IDs, such as U07ABC123, not %q", id)
		}
	}
	if config.Members["lead"].Agent != "hermes" {
		return errors.New("the lead is Hermes")
	}
	if err := teamCheckLeadName(config.Members["lead"].Name); err != nil {
		return err
	}
	for role, member := range config.Members {
		if member.Name != "" && role != "lead" {
			return fmt.Errorf("%s: only the lead has a name of its own", role)
		}
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
		switch member.Provider {
		case "", teamProviderOpenRouter, "claude":
			if member.Provider == "claude" && member.Agent != "claude" {
				return fmt.Errorf("%s: a Claude plan is for Claude Code, not %s", role, agentDisplayName(member.Agent))
			}
			if member.Fallback != "" && member.Agent == "hermes" {
				return fmt.Errorf("%s: a fallback is for a member on a subscription; on OpenRouter it has none", role)
			}
		case teamProviderChatGPT:
			if member.Agent != "hermes" && member.Agent != "codex" {
				return fmt.Errorf("%s: a ChatGPT plan is for Hermes or Codex, not %s", role, agentDisplayName(member.Agent))
			}
			if member.Model == "" && member.Agent == "hermes" {
				return fmt.Errorf("%s: name the ChatGPT model, such as gpt-6.1-sol", role)
			}
		default:
			return fmt.Errorf("%s: the provider is openrouter, chatgpt, or claude, not %q", role, member.Provider)
		}
		if member.Effort != "" && !containsString(agentEfforts, member.Effort) {
			return fmt.Errorf("%s: the effort is %s, not %q", role, strings.Join(agentEfforts, ", "), member.Effort)
		}
		if member.Fallback != "" && !agentModelName.MatchString(member.Fallback) {
			return fmt.Errorf("%s: %q isn't a model name", role, member.Fallback)
		}
	}
	if config.MemoryModel != "" && (!agentModelName.MatchString(config.MemoryModel) || !strings.Contains(config.MemoryModel, "/")) {
		return fmt.Errorf("the memory model is an OpenRouter model, such as google/gemini-3.8-flash, not %q", config.MemoryModel)
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

// teamStageCommands take stage for preview, and --stage for --preview.
var teamStageCommands = map[string]bool{"data": true, "limit": true, "bom": true, "env": true, "jobs": true, "incidents": true, "helper": true}

func runTeam(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		if os.Getenv(teamSocketEnv) != "" {
			// In the lead's box: the lead's commands, not the machine's.
			fmt.Fprint(stdout, teamLeadUsage())
			return 0
		}
		printTeamUsage(stdout)
		return 0
	}
	command, rest := args[0], args[1:]
	// Stage is what people call the preview: the commands that name it
	// take either.
	if teamStageCommands[command] {
		rest = slices.Clone(rest)
		for i, arg := range rest {
			switch arg {
			case "stage":
				rest[i] = teamSlotPreview
			case "--stage":
				rest[i] = "--preview"
			}
		}
	}
	// The lead's own commands, inside its box.
	if command == "channel" {
		return exitCode(runTeamChannel(rest, stdout), stderr)
	}
	if teamLeadCommands[command] || teamLeadOrHost[command] && os.Getenv(teamSocketEnv) != "" {
		return exitCode(runTeamLeadCommand(command, rest, stdin, stdout), stderr)
	}
	var err error
	switch command {
	case "new":
		err = teamNew(rest, stdin, stdout)
	case "adopt":
		err = teamAdoptCommand(rest, stdin, stdout)
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
	case "rebuild":
		err = withTeamName(rest, func(name string) error { return teamRebuild(name, stdout, stderr) })
	case "rm", "remove":
		err = teamRemove(rest, stdin, stdout)
	case "rename":
		err = teamRename(rest, stdin, stdout)
	case "ls", "list":
		err = teamList(stdout)
	case "status":
		err = withTeamName(rest, func(name string) error { return teamStatus(name, stdout) })
	case "logs":
		err = teamLogs(rest, stdout, stderr)
	case "upstream":
		err = teamUpstreamCommand(rest, stdout)
	case "jobs":
		err = teamJobsCommand(rest, stdout)
	case "incidents":
		err = teamIncidentsCommand(rest, stdout)
	case "job":
		err = teamJobCommand(rest, stdout)
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
	case "snapshot":
		err = teamSnapshotCommand(rest, stdout)
	case "snapshots":
		err = withTeamName(rest, func(name string) error { return teamSnapshotsCommand(name, stdout) })
	case "restore":
		err = teamRestoreCommand(rest, stdin, stdout)
	case "rollback":
		err = teamRollbackCommand(rest, stdin, stdout)
	case "data":
		err = teamDataCommand(rest, stdin, stdout)
	case "say":
		err = teamSayCommand(rest, stdin, stdout)
	case "dev":
		err = teamDevCommand(rest, stdin, stdout)
	case "owners":
		err = teamOwnersCommand(rest, stdin, stdout)
	case "access":
		err = teamAccessCommand(rest, stdout)
	case "task", "tasks", "ticket", "tickets": // ticket: its name before hi 2.5.14
		err = teamTicketCommand(rest, stdin, stdout)
	case "backups":
		err = teamBackupsCommand(rest, stdin, stdout)
	case "key":
		err = teamKey(rest, stdin, stdout)
	case "signin":
		err = teamSignin(rest, stdin, stdout)
	case "member":
		err = teamMemberCommand(rest, stdin, stdout)
	case "env":
		err = teamEnv(rest, stdin, stdout)
	case "limit":
		err = teamLimitCommand(rest, stdout)
	case "bom":
		err = teamBomCommand(rest, stdout)
	case "advisories":
		err = teamAdvisoriesCommand(rest, stdout)
	case "advisory":
		err = teamAdvisoryCommand(rest, stdout)
	case "__app":
		return runTeamApp(rest, stderr)
	case "__resolver":
		return runTeamResolver(rest, stderr)
	case "__leadapi":
		return runTeamLeadAPI(rest, stderr)
	case "helper":
		err = teamHelperCommand(rest, stdout)
	case "domain":
		err = teamDomainCommand(rest, stdin, stdout)
	case "addresses":
		err = teamAddressesCommand(rest, stdout)
	case "allow-ip":
		err = teamAllowIPCommand(rest, stdout)
	case "sso":
		err = teamSSOCommand(rest, stdin, stdout)
	case "manifest":
		err = withTeamName(rest, func(name string) error {
			config, err := loadTeam(name)
			if err == nil {
				fmt.Fprintln(stdout, teamManifestYAML(config))
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
	fmt.Fprintln(w, `hi team runs a team's app for the people in one Slack channel. Its owners build it
with coding agents in the team's dev box and release it from the dev page; a lead
(Hermes) helps the people in the channel, files what they ask for as tasks, and
watches the app.

usage:
  hi team new <name>               a step-by-step setup: Slack app, channel, people, members, code,
                                   in a new folder ./team-<name>
      [--dir <folder>]             the team's folder somewhere else
      [--from <file.json>]         the answers from a file; the Slack tokens from
                                   HI_TEAM_SLACK_BOT_TOKEN and HI_TEAM_SLACK_APP_TOKEN, the
                                   team's own OpenRouter key from HI_TEAM_OPENROUTER_API_KEY, and
                                   its ChatGPT sign-in from HI_TEAM_CHATGPT_AUTH (an auth.json), and
                                   its Claude token from HI_TEAM_CLAUDE_TOKEN
  hi team adopt <name> <source>    the same setup for a project someone else wrote, such as one on
                                   GitHub (github.com/owner/name, owner/name, or a URL): the team keeps
                                   its own copy and never pushes to it; an owner's agent surveys it
  hi team adopt <name> <folder>    the same for a folder on this machine, copied without its history:
                                   what git would commit, without .git, secrets such as .env, or
                                   links that point outside it; the folder doesn't change
      [--ref <branch|tag|commit>]  where to start; upstream's default branch otherwise
      [--github <owner/name>]      keep the team's copy in a new private GitHub repository too
      [--shallow|--full]           only its latest commit, or its whole history (a folder: all of it) even over 1 GB
      [--dir, --from, --yes]       as for hi team new; --yes doesn't ask before adopting
  hi team up [<name>]              start or restart the team and keep it up, also after a reboot;
                                   without a name, pick it from a list
  hi team up <folder>              the same for a team whose folder moved or was copied here
  hi team down <name>              stop it; its folder stays
  hi team rebuild <name>           hi's base image again, with Ubuntu's current packages, and the
                                   team on it: what fixes the image's advisories
  hi team rm [<name>]              stop it and remove it from this machine: its service, boxes,
                                   and folder; without a name, pick it from a list
      [--keep-folder]              keep the folder; hi team up <folder> brings the team back
      [--yes]                      don't ask first
  hi team rename <name> <new>      give the team another name, after its Slack app's: its bot,
                                   its /hi-<name> command, its service; memory and snapshots stay
      [--yes]                      don't ask first
  hi team ls                       teams on this machine
  hi team status <name>            whether it is up, and its members
  hi team logs <name> [-f]         the lead's log; -f follows it
  hi team say <name> <text|->      tell the lead something from this machine: hi posts it in the
                                   channel, and the lead answers in its thread; what it asks to
                                   change, the lead files as a task
      [--wait]                     wait for the answer and print it
  hi team dev <name>               the team's dev box, where owners build with agents over SSH:
                                   whether it runs, its NetBird name, and how to get in
      [restart | netbird [<key file>] | new-host-key]  a new box; join NetBird; a new host key
      [start <task> [--agent claude|codex]]  a task's branch, worktree, and herdr workspace
      [finish <branch> [--discard]]  once it's shipped: its worktree, workspace, branch, and task
  hi team owners <name>            the team's owners, who build in the dev box, and their SSH keys
      [add|remove <member ID> | key <member ID> <public key>|--remove]
  hi team access <name>            who reaches what: owners, developers (who build like owners,
                                   but don't release), and contributors (the channel, who report bugs)
      [<member ID> developer|contributor|none]  an owner's change, also on the dev page; none
                                   stops someone's reports
      [key <member ID> <public key>|--remove]  a developer's or owner's SSH key for the dev box
  hi team task <name>              the team's tasks: GitHub Issues (gh) or GitLab issues (glab),
                                   where its repository is, or hi's own
      [list [--closed] | show <n> | add <title> [<body|->] | comment <n> <text|-> | close <n> [<comment>]]
  hi team channel <name> [--since 2h]  what was said in the channel (the last day by default)
  hi team key <name> openrouter    give the team an OpenRouter key of its own (asked, never an
                                   argument); --unset goes back to this machine's. No box holds it:
                                   the app reaches models at $OPENROUTER_BASE_URL through hi
  hi team key <name> slack         the Slack app's tokens again, when Slack gave new ones (asked
                                   for and checked; Enter keeps one); a running team restarts
  hi team signin <name> chatgpt    sign the team in to ChatGPT, for its roles on a ChatGPT plan: a
                                   code to type on any device; only the team's server refreshes it
  hi team signin <name> claude     the team's own Claude token, from claude setup-token (asked for):
                                   its Claude members use it before this machine's sign-in
      [--machine]                  use this machine's sign-in (Codex's, or Claude Code's) instead
  hi team member <name> <role>     a member's agent, model, and who pays for it
      [--provider openrouter|chatgpt] [--model <model>] [--fallback <model on OpenRouter>]
      [--effort low|medium|high|xhigh|max]  how hard it thinks; medium unless set
                                   on ChatGPT, the fallback (the same model on OpenRouter unless
                                   given) takes over when the plan refuses; a running team restarts
  hi team env <name>               the settings the team's app gets, by name
  hi team env <name> KEY=VALUE...  add or change settings: environment variables in the app's
                                   boxes only (Stage and production); KEY alone asks for the value
      [--unset KEY]                remove one
      [--production]               production's only: a key that sends, posts, orders, or pays per
                                   call, which Stage mustn't use
      [--stage]                    a value of Stage's own, such as a test key; production
                                   keeps its own
  hi team limit <name>             what Stage's model calls may cost a day, and spent today
      [stage <usd> | stage off]      set it ($5 a day unless set); production has no limit from hi
    hi team bom <name> [v<n>|stage]    production's bill of materials (CycloneDX), or a release's
  hi team advisories <name>        known advisories for what production runs, from the team's hi
                                   server, which matches them every night
  hi team advisory <name> ignore <id> --because "<why>" [--package <p>]
                                   an owner's word that one doesn't apply, until it changes;
                                   unignore <id> takes it back
  hi team snapshot <name>          take a snapshot now: the lead's memory, the team's record, and
                                   the app's data; the team keeps running. It takes one daily too
  hi team snapshots <name>         list them
  hi team restore <name> <snapshot>  put the team back as it was: a snapshot's ID, latest, v3 (the
                                   one taken before v3 went out), or a day (2026-10-12)
      [--code]                     put the code (repo/) back too
      [--yes]                      don't ask first
  hi team rollback <name> v2       put production back on v2, with its data as v2 left it; what was
                                   entered since is kept in a snapshot, and going forward the same
                                   way puts it back
      [--code-only]                the code alone; production's data stays as it is
      [--yes]                      don't ask first
  hi team data <name>              the app's data folders: production's, and Stage's fork of
                                   it with what Stage wrote itself
  hi team data <name> fork         Stage gets a new fork of production's data now (it gets
                                   one at every release): all of it, in seconds, with no copy
  hi team data <name> copy stage production  production starts from what Stage gathered;
                                   its own data is snapshotted first
      [--yes]                      don't ask first
  hi team data <name> stage empty  Stage starts empty at every release, for an app whose
                                   people's data Stage shouldn't show; stage fork undoes it
  hi team backups <name>           where snapshots are kept, and for how long
      [--offsite s3:https://<host>/<bucket>]  copy them to an S3-compatible bucket too (asks for
                                   its keys); --offsite off stops
      [--region <region>]          the bucket's region, if it isn't us-east-1
      [--keep 7,4,6]               how many daily, weekly, and monthly snapshots to keep
      [--show-key]                 the key they are encrypted with; keep a copy elsewhere
  hi team helper <name>            whether the app's pages have the helper, help and feedback
      [stage|production on|off]    turn it on or off for an address, from the next page load
  hi team addresses <name>         what its addresses on NetBird are named: this machine's domain
      [domain|netbird|<host>]      on https (hi net domain), the NetBird name, or a host name IT
                                   keeps in its DNS; asked in hi team new
  hi team domain [<domain>|off]    a domain verified in NetBird for the apps on this machine
  hi team domain <name> on|off     the team's app at <name>.<domain>, its preview at
                                   <name>-preview.<domain>; off by default
  hi team allow-ip <name>          the only addresses the app's Stage and production let in
      [<ip,cidr> | off]            (NetBird's Access Control; needs hi team domain's token);
                                   hi puts back exactly this list every minute, so add an
                                   address here, not in NetBird's dashboard
  hi team sso provider add <name> <issuer>  an OIDC provider for signing in (Google Workspace,
      [--client-id <id>] [--groups-claim <c>]  Entra ID, Keycloak, Auth0, Okta...); asks for the secret
  hi team sso <name> on <provider> people without the link sign in to the app, needing
      [--domain <d>] [--group <g>] [--anyone]  these rules; on a name that stays (hi team domain)
  hi team sso [<name> [off]]       the providers and teams; a team's rules and last sign-ins
  hi team manifest <name>          the Slack app manifest again
  hi team github <name> <owner/repo>  put the project on GitHub (a new private repository, or an
                                   empty one); the team pushes every merge there
  hi team jobs <name> [--stage]    the app's long work: each job's state, count, rate a day, last error
  hi team incidents <name> [<id>]  what went wrong while nobody looked, and what the lead did about it
                                   (--rules: the decisions the people made about the app's jobs)
  hi team upstream <name>          for an adopted project: fetch the project it came from, and say
                                   where dev stands against it and what's new there

A team is its folder. hi finds it by name through `+teamsDir()+`.`)
}

// ---------------------------------------------------------------------------
// the wizard

// teamSlackChecker checks tokens and the channel with Slack; tests replace
// it.
type teamSlackChecker interface {
	botIdentity(botToken string) (workspace, botUser string, err error)
	// scopes are what the bot token was granted.
	scopes(botToken string) ([]string, error)
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

func (liveTeamSlack) scopes(botToken string) ([]string, error) {
	request, _ := http.NewRequest(http.MethodPost, "https://slack.com/api/auth.test", nil)
	request.Header.Set("Authorization", "Bearer "+botToken)
	response, err := (&http.Client{Timeout: 15 * time.Second}).Do(request)
	if err != nil {
		return nil, err
	}
	response.Body.Close()
	return strings.Split(response.Header.Get("X-OAuth-Scopes"), ","), nil
}

// teamSlackScopes are what a team's Slack app asks for in its manifest.
// Not assistant:write, for Slack's thinking line: Slack grants it only to
// an app with its agent experience on, which a team's app leaves off; an
// app made before v2.6.8 may have it, and the lead still uses it.
var teamSlackScopes = []string{
	"app_mentions:read", "channels:history", "channels:read", "chat:write", "commands", "files:read", "files:write",
	"groups:history", "groups:read", "im:history", "im:read", "im:write", "reactions:read", "reactions:write", "users:read",
}

// teamMissingScopes are the scopes the team's app needs that the bot token
// lacks: an app made from another manifest, or one not reinstalled after
// its scopes changed. None when Slack can't be asked.
func teamMissingScopes(botToken string) []string {
	granted, err := teamSlack.scopes(botToken)
	if err != nil {
		return nil
	}
	has := map[string]bool{}
	for _, scope := range granted {
		has[strings.TrimSpace(scope)] = true
	}
	var missing []string
	for _, scope := range teamSlackScopes {
		if !has[scope] {
			missing = append(missing, scope)
		}
	}
	return missing
}

// teamScopesAdvice says how to give the app the scopes it lacks.
func teamScopesAdvice(missing []string) string {
	advice := fmt.Sprintf("the Slack app lacks %s, so the lead can't read what people share or post everything it should. In Slack's app settings (api.slack.com/apps), add them under OAuth & Permissions → Bot Token Scopes, then Reinstall to Workspace", strings.Join(missing, ", "))
	var lost []string
	if containsString(missing, "commands") || containsString(missing, "im:write") {
		lost = append(lost, "/hi-<team>, which DMs owners their link to the dev page")
	}
	if containsString(missing, "im:history") || containsString(missing, "im:read") {
		lost = append(lost, "talking to the lead in a DM")
	}
	if len(lost) > 0 {
		// These need the manifest's command and events too, not just
		// scopes.
		advice = fmt.Sprintf("the Slack app lacks %s, so %s can't work. In Slack's app settings (api.slack.com/apps), open App Manifest, replace it with the one hi team manifest <name> prints, Save, then Reinstall to Workspace", strings.Join(missing, ", "), strings.Join(lost, ", and "))
	}
	return advice
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
	// Empty starts a new repository with only a README and the team's rules,
	// instead of a web app on Bun.
	Empty bool `json:"empty,omitempty"`
	// Adopt is a project someone else wrote to adopt (hi team adopt), from
	// Ref; Shallow fetches only its latest commit, and NoSurvey skips the
	// first task. PushToSource makes a clone or an adoption push its
	// merges back to where it came from, as a project of your own does.
	Adopt        string `json:"adopt,omitempty"`
	Ref          string `json:"ref,omitempty"`
	Shallow      bool   `json:"shallow,omitempty"`
	NoSurvey     bool   `json:"no_survey,omitempty"`
	PushToSource bool   `json:"push_to_source,omitempty"`
	// NetBirdKey is the setup key the dev box joins the team's NetBird
	// network with, once (team_dev.go).
	NetBirdKey string `json:"netbird_key,omitempty"`
	// adoption is the adoption the wizard or the answers prepared.
	adoption *teamAdoption
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
	if name == "" && from == "" && teamStyled(stdin, stdout) {
		// The team's name is asked first: its folder is named after it.
		teamSetupBanner(stdout, true, "", "")
		teamSay(stdout, true, "A short name for the team, such as payments: lowercase letters, digits, and dashes.")
		var err error
		if name, err = teamFieldAsk(stdin, stdout, "Team name", false, func(answer string) error {
			if !teamNamePattern.MatchString(answer) {
				return errors.New("a team's name starts with a letter and has lowercase letters, digits, and dashes, up to 20")
			}
			if _, err := loadTeam(answer); err == nil {
				return fmt.Errorf("team %s already exists in %s", answer, teamDir(answer))
			}
			return nil
		}); err != nil {
			return err
		}
	}
	if name == "" {
		return usageError{"usage: hi team new <name> [--dir <folder>] [--from <file.json>]"}
	}
	return teamCreate(name, folder, from, nil, stdin, stdout)
}

// teamCreate sets up a team, from the wizard or answers in a file, with a
// project that's new, cloned, or adopted (hi team adopt).
func teamCreate(name, folder, from string, adopt *teamAdoption, stdin io.Reader, stdout io.Writer) error {
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
		if answers, secrets, err = teamAnswersFromFile(name, from); err != nil {
			return err
		}
		// A URL to clone that this machine can't push to is someone
		// else's project: adopted, unless the answers say to push there.
		if adopt == nil && answers.Adopt == "" && reviewIsURL(answers.Clone) {
			answers.Adopt, answers.Clone = answers.Clone, ""
		}
		adopt = teamAdoptFromAnswers(adopt, answers)
	}
	if adopt != nil {
		defer adopt.discard()
		ok, err := adopt.prepare(dir, stdin, stdout)
		if err != nil {
			return err
		}
		if !ok {
			return errors.New("nothing was adopted or set up")
		}
		if adopt.Push {
			// A project of the owner's own: cloned, and pushed to, as before.
			answers.Clone, adopt = adopt.url, nil
		}
	}
	if from == "" {
		answers, secrets, err = teamWizard(name, dir, adopt, stdin, stdout)
		if answers.adoption != nil && answers.adoption != adopt {
			defer answers.adoption.discard()
		}
		if err != nil {
			return err
		}
	} else {
		answers.adoption = adopt
	}
	if answers.adoption != nil {
		answers.adoption.GitHub = firstNonEmpty(answers.adoption.GitHub, answers.GitHub)
		answers.GitHub = answers.adoption.GitHub
	}
	config := answers.teamConfig
	config.Name, config.Created = name, time.Now().UTC()
	if made := teamFreeMadeAs(name); made != name {
		config.MadeAs = made
	}
	if config.Purpose == "" && answers.adoption != nil {
		config.Purpose = answers.adoption.summary.Description
	}
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
	if err := createTeamFolder(&config, secrets, answers.Clone, answers.Empty, answers.adoption, stdout); err != nil {
		os.RemoveAll(dir)
		os.Remove(filepath.Join(teamsDir(), name))
		return err
	}
	if answers.GitHub != "" {
		if err := teamConnectGitHub(&config, answers.GitHub, stdout); err != nil {
			fmt.Fprintf(stdout, "The team is set up, but not on GitHub: %v\nTry again with: hi team github %s %s\n", err, name, answers.GitHub)
		}
	}
	if answers.NetBirdKey != "" {
		if err := setTeamDevNetBird(name, answers.NetBirdKey); err != nil {
			fmt.Fprintf(stdout, "The dev box isn't on NetBird: %v\nTry again with: hi team dev %s netbird\n", err, name)
		}
	}
	fmt.Fprintf(stdout, "\nTeam %s is ready in %s.\n", name, dir)
	for _, role := range teamRoles(config) {
		member := config.Members[role]
		fmt.Fprintf(stdout, "  %-10s %s\n", role, teamMemberLabel(member))
	}
	if config.Adopted != nil {
		fmt.Fprintf(stdout, "\nIn the dev box, ask your agent to survey %s first: it makes it run and maps it (the hi-dev-survey skill).\n", config.Adopted.Name)
	}
	fmt.Fprintf(stdout, "\nStart it with:\n  hi team up %s\n", name)
	return nil
}

// teamAskProvider asks who pays for a Hermes member: OpenRouter, or the
// team's ChatGPT plan. On ChatGPT it signs the team in (once, for every
// role on it), offers the account's models, and checks the fallback, the
// same model on OpenRouter.
func teamAskProvider(stdin io.Reader, stdout io.Writer, member *teamMember, secrets *teamSecrets, signIn *string) error {
	provider, err := teamChoose(stdin, stdout, "         provider", []teamOption{
		{teamProviderOpenRouter, "pay per call, with the team's OpenRouter key"},
		{teamProviderChatGPT, "a ChatGPT plan, falling back to OpenRouter"},
	}, teamProviderOpenRouter)
	if err != nil {
		return err
	}
	if firstNonEmpty(provider, teamProviderOpenRouter) == teamProviderOpenRouter {
		member.Provider, member.Fallback = "", ""
		return nil
	}
	member.Provider = teamProviderChatGPT
	if err := teamAskChatGPTSignIn(stdin, stdout, secrets, signIn); err != nil {
		return err
	}
	grant := teamChatGPTGrant{}
	if secrets.ChatGPT != nil {
		grant = *secrets.ChatGPT
	} else if grant, err = readCodexChatGPT(); err != nil {
		return err
	}
	models, err := chatGPTModels(grant)
	if err != nil {
		return err
	}
	return teamAskChatGPTModel(stdin, stdout, member, models)
}

// teamAskCodex signs the team in to ChatGPT for a Codex member, unless a
// role on it did already, and shows what it falls back to on OpenRouter.
// A machine whose Codex is on an API key keeps it, as before.
func teamAskCodex(stdin io.Reader, stdout io.Writer, member *teamMember, secrets *teamSecrets, signIn *string) error {
	if _, err := readCodexChatGPT(); err != nil && *signIn == "" && fileExists(codexAuthPath()) {
		teamNote(stdin, stdout, "         Codex here is on an API key; the reviewer keeps it.")
		return nil
	}
	if err := teamAskChatGPTSignIn(stdin, stdout, secrets, signIn); err != nil {
		return err
	}
	model := firstNonEmpty(member.Model, defaultAgentModel("codex"))
	if model == "" {
		return nil
	}
	return teamAskFallback(stdin, stdout, member, teamFallbackModel(teamMember{Agent: "codex", Model: model}))
}

// teamAskChatGPTSignIn asks, once for the whole team, whose ChatGPT sign-in
// its roles on ChatGPT use, and signs the team in when it's its own.
func teamAskChatGPTSignIn(stdin io.Reader, stdout io.Writer, secrets *teamSecrets, signIn *string) error {
	if *signIn == "" {
		teamNote(stdin, stdout, "         The team signs in to ChatGPT once, for every role on it: team signs in with a code on any device")
		teamNote(stdin, stdout, "         (only the team's server refreshes it); machine uses this machine's, Codex's, which Codex refreshes.")
		answer, err := teamChoose(stdin, stdout, "         ChatGPT sign-in", []teamOption{
			{"team", "the team's own, with a code on any device"},
			{"machine", "this machine's, Codex's"},
		}, "team")
		if err != nil {
			return err
		}
		if answer == "machine" {
			if _, err := readCodexChatGPT(); err != nil {
				return err
			}
		}
		*signIn = answer
		if *signIn == "team" {
			grant, err := teamChatGPTSignIn(stdout)
			if err != nil {
				return err
			}
			secrets.ChatGPT = &grant
			teamNote(stdin, stdout, fmt.Sprintf("         Signed in: %s.", chatGPTPlanName(grant)))
		}
	}
	return nil
}

// teamAskChatGPTModel asks a Hermes member's model from the plan's, and
// checks its fallback.
func teamAskChatGPTModel(stdin io.Reader, stdout io.Writer, member *teamMember, models []string) error {
	offered := member.Model
	if !containsString(models, offered) && len(models) > 0 {
		offered = models[0]
	}
	if len(models) > 0 && !teamStyled(stdin, stdout) {
		teamNote(stdin, stdout, fmt.Sprintf("         The plan offers %s.", strings.Join(models, ", ")))
	}
	var model string
	var err error
	if len(models) > 0 {
		var options []teamOption
		for _, name := range models {
			options = append(options, teamOption{value: name})
		}
		model, err = teamChoose(stdin, stdout, "         model", options, offered)
	} else {
		model, err = teamAskUntil(stdin, stdout, "         model", false, func(answer string) error {
			if answer == "" {
				return errors.New("give the model")
			}
			return nil
		})
	}
	if err != nil {
		return err
	}
	member.Model, member.Fallback = firstNonEmpty(model, offered), ""
	return teamAskFallback(stdin, stdout, member, teamFallbackModel(*member))
}

// teamAskFallback checks a member's usual fallback is on OpenRouter, and
// asks for one that is when it isn't.
func teamAskFallback(stdin io.Reader, stdout io.Writer, member *teamMember, usual string) error {
	fallback := usual
	for {
		has, err := teamOpenRouterHasModel(fallback)
		if err != nil {
			return err
		}
		if has {
			break
		}
		teamNote(stdin, stdout, fmt.Sprintf("         OpenRouter has no %s to fall back on.", fallback))
		if fallback, err = teamAskUntil(stdin, stdout, "         fallback on OpenRouter", false, func(answer string) error {
			if !agentModelName.MatchString(answer) {
				return fmt.Errorf("%q isn't a model name", answer)
			}
			return nil
		}); err != nil {
			return err
		}
	}
	if fallback != usual {
		member.Fallback = fallback
	}
	teamNote(stdin, stdout, fmt.Sprintf("         Fallback: %s on OpenRouter.", fallback))
	return nil
}

// teamAskClaude asks, once, for the team's own Claude token (Enter: this
// machine's sign-in), and shows what a Claude member falls back to on
// OpenRouter, asking for one when OpenRouter has none.
func teamAskClaude(stdin io.Reader, stdout io.Writer, member *teamMember, secrets *teamSecrets, asked *bool) error {
	if !*asked {
		*asked = true
		teamNote(stdin, stdout, "         Claude: a token of the team's own, from claude setup-token signed in to the account that pays")
		teamNote(stdin, stdout, "         (it lasts a year), or Enter for this machine's sign-in.")
		var token string
		check := func(answer string) error {
			if answer == "" {
				_, err := boxClaudeSecret()
				return err
			}
			return teamCheckClaudeToken(answer)
		}
		if teamStyled(stdin, stdout) {
			answer, err := teamFieldAsk(stdin, stdout, "Claude token [Enter: this machine's]", true, check)
			if err != nil {
				return err
			}
			token = answer
		} else {
			for attempt := 1; ; attempt++ {
				answer, err := teamReadSecret(stdin, stdout, "         Claude token [Enter: this machine's]")
				if err == nil {
					err = check(answer)
				}
				if err == nil {
					token = answer
					break
				}
				if !isTerminal(stdin) || attempt == 5 || errors.Is(err, io.EOF) {
					return err
				}
				fmt.Fprintf(stdout, "  %v. Try again.\n", err)
			}
		}
		if token != "" {
			secrets.ClaudeToken, secrets.ClaudeSignedIn = token, time.Now().UTC()
		}
	}
	for {
		fallback, err := teamClaudeFallback(*member)
		if err != nil {
			return err
		}
		has := false
		if fallback != "" {
			if has, err = teamOpenRouterHasModel(fallback); err != nil {
				return err
			}
		}
		if has {
			teamNote(stdin, stdout, fmt.Sprintf("         Fallback: %s on OpenRouter.", fallback))
			return nil
		}
		teamNote(stdin, stdout, fmt.Sprintf("         OpenRouter has no fallback for %s.", firstNonEmpty(member.Model, "opus")))
		answer, err := teamAskUntil(stdin, stdout, "         fallback on OpenRouter", false, func(answer string) error {
			if !agentModelName.MatchString(answer) {
				return fmt.Errorf("%q isn't a model name", answer)
			}
			return nil
		})
		if err != nil {
			return err
		}
		member.Fallback = answer
	}
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
	secrets := teamSecrets{SlackBotToken: os.Getenv("HI_TEAM_SLACK_BOT_TOKEN"), SlackAppToken: os.Getenv("HI_TEAM_SLACK_APP_TOKEN"),
		OpenRouterAPIKey: os.Getenv("HI_TEAM_OPENROUTER_API_KEY")}
	if secrets.SlackBotToken == "" || secrets.SlackAppToken == "" {
		return answers, secrets, errors.New("set HI_TEAM_SLACK_BOT_TOKEN and HI_TEAM_SLACK_APP_TOKEN to the Slack app's tokens")
	}
	if token := os.Getenv("HI_TEAM_CLAUDE_TOKEN"); token != "" {
		if err := teamCheckClaudeToken(token); err != nil {
			return answers, secrets, err
		}
		secrets.ClaudeToken, secrets.ClaudeSignedIn = token, time.Now().UTC()
	}
	if teamUsesChatGPT(answers.teamConfig) {
		// The team's own ChatGPT sign-in from a file, else this machine's.
		if path := os.Getenv("HI_TEAM_CHATGPT_AUTH"); path != "" {
			grant, err := readChatGPTAuthFile(path)
			if err != nil {
				return answers, secrets, err
			}
			secrets.ChatGPT = &grant
		} else if _, err := readCodexChatGPT(); err != nil {
			return answers, secrets, fmt.Errorf("a member is on ChatGPT: set HI_TEAM_CHATGPT_AUTH to a ChatGPT auth.json, or %w", err)
		}
	}
	return answers, secrets, nil
}

func teamWizard(name, dir string, adopt *teamAdoption, stdin io.Reader, stdout io.Writer) (teamAnswers, teamSecrets, error) {
	var answers teamAnswers
	var secrets teamSecrets
	config := &answers.teamConfig
	config.Name = name
	config.Members = defaultTeamMembers()
	styled := teamStyled(stdin, stdout)
	var err error

	teamSetupBanner(stdout, styled, name, dir)

	teamStep(stdout, styled, "1/9 Lead")
	teamSay(stdout, styled, "The lead talks with the people in the team's Slack channel. Give it a name of its own, such as a",
		"person's, or press Enter to call it "+name+".")
	leadName, err := teamAskUntil(stdin, stdout, "Lead's name ["+name+"]", false, teamCheckLeadName)
	if err != nil {
		return answers, secrets, err
	}
	if leadName != "" && leadName != name {
		lead := config.Members["lead"]
		lead.Name = leadName
		config.Members["lead"] = lead
	}
	handle := teamBotHandle(*config)
	teamSay(stdout, styled, "In Slack it's @"+handle+".")

	teamStep(stdout, styled, "2/9 Purpose")
	teamSay(stdout, styled, "In one sentence, what is this team for? The lead starts from it.")
	offered := ""
	if adopt != nil {
		offered = adopt.summary.Description
	}
	if config.Purpose, err = teamAsk(stdin, stdout, "Purpose", offered); err != nil {
		return answers, secrets, err
	}

	teamStep(stdout, styled, "3/9 Slack app")
	teamSay(stdout, styled, "Each team has a Slack app of its own. Create it from this manifest:",
		"  api.slack.com/apps → Create New App → From a manifest → YAML → paste it → Create,",
		"  then Install to Workspace.")
	if err := teamShowManifest(stdin, stdout, styled, *config); err != nil {
		return answers, secrets, err
	}
	teamSay(stdout, styled, "The bot token is under OAuth & Permissions (xoxb-…). The app token is under",
		"Basic Information → App-Level Tokens: generate one with the connections:write scope (xapp-…).")
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
	teamSay(stdout, styled, fmt.Sprintf("Slack: both tokens work, in workspace %s.", workspace))

	teamStep(stdout, styled, "4/9 Channel")
	teamSay(stdout, styled, "Invite the app to the team's channel first: /invite @"+handle,
		"Then copy the channel's ID: click the channel's name; the ID is at the bottom (C…).")
	channel := ""
	if config.Channel, err = teamAskUntil(stdin, stdout, "Channel ID", false, func(id string) error {
		channel, err = teamCheckChannel(handle, secrets.SlackBotToken, id)
		return err
	}); err != nil {
		return answers, secrets, err
	}
	teamSay(stdout, styled, fmt.Sprintf("Slack: the app is in #%s.", channel))

	teamStep(stdout, styled, "5/9 People")
	teamSay(stdout, styled, "Owners build the app in the dev box and release it from the dev page. Give Slack member IDs,",
		"separated by commas: a person's profile → ⋯ → Copy member ID (U…).")
	owners, err := teamAskUntil(stdin, stdout, "Owners", false, teamCheckPeople)
	if err != nil {
		return answers, secrets, err
	}
	config.Owners = splitList(owners)

	teamStep(stdout, styled, "6/9 Members")
	if styled {
		teamSay(stdout, styled, "Each role's agent and model, and for Hermes who pays: OpenRouter, or a ChatGPT plan (falling",
			"back to OpenRouter), and how hard it thinks. Enter keeps what's offered.")
	} else {
		teamSay(stdout, styled, "Each role's agent (claude, codex, or hermes) and model, and for Hermes who pays: OpenRouter, or a",
			"ChatGPT plan (falling back to OpenRouter), and how hard it thinks. Enter keeps what's offered.")
	}
	chatGPTSignIn := "" // team or machine, once asked
	claudeAsked := false
	for _, role := range teamRoles(*config) {
		member := config.Members[role]
		if styled {
			err = teamAskMemberStyled(stdin, stdout, *config, role, &member)
		} else {
			err = teamAskMemberLine(stdin, stdout, *config, role, &member)
		}
		if err != nil {
			return answers, secrets, err
		}
		if member.Agent == "hermes" {
			if err := teamAskProvider(stdin, stdout, &member, &secrets, &chatGPTSignIn); err != nil {
				return answers, secrets, err
			}
		}
		if member.Agent == "claude" {
			if err := teamAskClaude(stdin, stdout, &member, &secrets, &claudeAsked); err != nil {
				return answers, secrets, err
			}
		}
		if member.Agent == "codex" {
			if err := teamAskCodex(stdin, stdout, &member, &secrets, &chatGPTSignIn); err != nil {
				return answers, secrets, err
			}
		}
		var efforts []teamOption
		for _, effort := range agentEfforts {
			efforts = append(efforts, teamOption{value: effort})
		}
		effort, err := teamChoose(stdin, stdout, "         effort", efforts, teamDefaultEffort)
		if err != nil {
			return answers, secrets, err
		}
		if member.Effort = effort; effort == teamDefaultEffort {
			member.Effort = ""
		}
		config.Members[role] = member
	}

	teamStep(stdout, styled, "7/9 Models")
	if teamUsesChatGPT(*config) {
		teamSay(stdout, styled, "OpenRouter is what the roles on ChatGPT fall back on, and the lead's memory and the app the team",
			"builds use it. Give the team a key of its")
	} else {
		teamSay(stdout, styled, "The lead, and the app the team builds, use models through OpenRouter. Give the team a key of its")
	}
	teamSay(stdout, styled, "own (openrouter.ai/settings/keys), so its use is billed and limited apart, or press Enter for this",
		"machine's (Hermes'). No box ever holds the key; nobody needs to paste one into Slack.")
	if secrets.OpenRouterAPIKey, err = teamAskOpenRouterKey(stdin, stdout, "OpenRouter key [Enter: this machine's]"); err != nil {
		return answers, secrets, err
	}

	teamStep(stdout, styled, "8/9 Code")
	teamSay(stdout, styled, "The team keeps its own copy of the project in "+filepath.Join(dir, "repo")+".")
	if adopt != nil {
		if adopt.local != "" {
			teamSay(stdout, styled, fmt.Sprintf("It's a copy of %s, without its history: nothing there changes.", adopt.local))
		} else {
			teamSay(stdout, styled, fmt.Sprintf("It's %s, adopted: the team never pushes to it.", adopt.displayName()))
		}
		answers.adoption = adopt
	} else if err := teamAskRepository(stdin, stdout, styled, dir, &answers); err != nil {
		return answers, secrets, err
	}
	adoption := answers.adoption
	switch {
	case adoption != nil && (adoption.GitHub != "" || adoption.Shallow):
		if adoption.Shallow {
			teamSay(stdout, styled, "With only its latest commit, the copy stays on this machine.")
		}
	case adoption != nil:
		teamSay(stdout, styled, "Keep the team's copy on GitHub too? Give owner/name, and hi creates a private repository with",
			"the project's history, with this machine's GitHub sign-in (gh); the team pushes every merge there.")
	case answers.Clone == "":
		teamSay(stdout, styled, "Put it on GitHub too? Give owner/name, such as octocat/world-clock, and hi creates a private",
			"repository with this machine's GitHub sign-in (gh); the team pushes every merge there.")
	}
	if answers.Clone == "" && !(adoption != nil && (adoption.GitHub != "" || adoption.Shallow)) {
		if answers.GitHub, err = teamAskUntil(stdin, stdout, "GitHub [Enter: none, it stays on this machine]", false, func(name string) error {
			if name == "" {
				return nil
			}
			return teamCheckGitHub(name)
		}); err != nil {
			return answers, secrets, err
		}
	}

	teamStep(stdout, styled, "9/9 Network")
	teamSay(stdout, styled, "The team's addresses are on your NetBird network, and the owners build in its dev box over SSH there.")
	if styled {
		teamSay(stdout, styled, strings.Split(strings.TrimRight(teamDevNetBirdHelp(name), "\n"), "\n")...)
	} else {
		fmt.Fprint(stdout, teamDevNetBirdHelp(name))
	}
	key, err := teamReadSecret(stdin, stdout, "NetBird setup key [Enter: later, with hi team dev "+name+" netbird]")
	if err != nil && !errors.Is(err, io.EOF) {
		return answers, secrets, err
	}
	answers.NetBirdKey = strings.TrimSpace(key)
	// Without a terminal, answers that end before it keep the default.
	if err := teamAskAddresses(stdin, stdout, styled, config); err != nil && !errors.Is(err, io.EOF) {
		return answers, secrets, err
	}
	answers.checked = true
	return answers, secrets, nil
}

// teamAskMemberLine asks a role's agent and model on one line, as
// agent:model.
func teamAskMemberLine(stdin io.Reader, stdout io.Writer, config teamConfig, role string, member *teamMember) error {
	offered := member.Agent + teamModelSuffix(member.Model)
	answer, err := teamAskUntil(stdin, stdout, fmt.Sprintf("%-8s [%s]", role, offered), false, func(answer string) error {
		agent, model, _ := strings.Cut(firstNonEmpty(answer, offered), ":")
		return teamCheckMember(config, role, teamMember{Agent: strings.TrimSpace(agent), Model: strings.TrimSpace(model)})
	})
	if err != nil {
		return err
	}
	agent, model, _ := strings.Cut(firstNonEmpty(answer, offered), ":")
	member.Agent, member.Model = strings.TrimSpace(agent), strings.TrimSpace(model)
	return nil
}

// teamAskMemberStyled asks a role's agent from a list, the lead's being
// Hermes, then its model, offering the agent's usual one.
func teamAskMemberStyled(stdin io.Reader, stdout io.Writer, config teamConfig, role string, member *teamMember) error {
	agent := member.Agent
	if role != "lead" {
		var err error
		if agent, err = teamChoose(stdin, stdout, role, []teamOption{
			{"claude", "Claude Code"},
			{"codex", "Codex"},
			{"hermes", "Hermes"},
		}, member.Agent); err != nil {
			return err
		}
	} else {
		(&styledUI{in: stdin, out: stdout}).answered(role, "hermes")
	}
	offered := member.Model
	if agent != member.Agent {
		offered = defaultAgentModel(agent)
	}
	question := role + "'s model [" + firstNonEmpty(offered, "the agent's own") + "]"
	model, err := teamAskUntil(stdin, stdout, question, false, func(answer string) error {
		return teamCheckMember(config, role, teamMember{Agent: agent, Model: firstNonEmpty(answer, offered)})
	})
	if err != nil {
		return err
	}
	member.Agent, member.Model = agent, firstNonEmpty(model, offered)
	return nil
}

// teamCheckMember checks one role's agent and model, with the others'.
func teamCheckMember(config teamConfig, role string, member teamMember) error {
	if role == "lead" && member.Agent != "hermes" {
		return errors.New("the lead is Hermes")
	}
	if !agentKinds[member.Agent] {
		return fmt.Errorf("the agent is claude, codex, or hermes, not %q", member.Agent)
	}
	if member.Model != "" && !agentModelName.MatchString(member.Model) {
		return fmt.Errorf("%q isn't a model name", member.Model)
	}
	if coder := config.Members["coder"]; role == "reviewer" && coder.Agent == member.Agent && coder.Model == member.Model {
		return errors.New("the reviewer must be a different agent or model from the coder")
	}
	return nil
}

// teamAskRepository asks what the team's project starts from: a new web
// app, an empty repository, a team template, or someone's project.
func teamAskRepository(stdin io.Reader, stdout io.Writer, styled bool, dir string, answers *teamAnswers) error {
	var offered []*templateLayer
	if catalog, err := loadTemplateCatalog(io.Discard); err == nil {
		offered = teamTemplates(catalog)
	}
	templates := map[string]bool{}
	for _, template := range offered {
		templates[firstNonEmpty(template.key, template.Name)] = true
	}
	check := func(answer string) error {
		if answer == "" || answer == "new" || answer == "empty" || templates[answer] || !reviewIsURL(answer) {
			return nil
		}
		// Someone else's project, or one of yours to push to.
		adoption := &teamAdoption{Source: answer}
		ok, err := adoption.prepare(dir, stdin, stdout)
		switch {
		case err != nil:
			return err
		case !ok:
			return errors.New("not adopted; give another, or press Enter for a new app")
		case adoption.Push:
			answers.Clone = adoption.url
		default:
			answers.adoption = adoption
		}
		return nil
	}
	var clone string
	var err error
	if styled {
		options := []teamOption{
			{"new", "a new web app: a blank page on Bun, with a database, tests, and the helper's chat"},
			{"empty", "a repository with only a README"},
		}
		for _, template := range offered {
			options = append(options, teamOption{firstNonEmpty(template.key, template.Name), template.Summary})
		}
		options = append(options, teamOption{"adopt", "someone's project, such as github.com/owner/name, or a folder here"})
		if clone, err = teamChoose(stdin, stdout, "Project", options, "new"); err != nil {
			return err
		}
		if clone == "adopt" {
			teamSay(stdout, styled, "The team works on its own copy and never pushes to theirs. A folder on this machine is cloned,",
				"and its owner pulls from the team.")
			if clone, err = teamAskUntil(stdin, stdout, "Project to adopt", false, func(answer string) error {
				if answer == "" {
					return errors.New("give github.com/owner/name, a URL, or a folder")
				}
				return check(answer)
			}); err != nil {
				return err
			}
		}
	} else {
		teamSay(stdout, styled, "Enter starts a new web app there: a blank page on Bun, with a database and tests, and the",
			"helper's chat on every page. Type empty for a repository with only a README. Or give a project",
			"someone else wrote, such as github.com/owner/name, to adopt it: the team works on its own copy",
			"and never pushes to theirs. A folder on this machine is cloned, and its owner pulls from the team.")
		if len(offered) > 0 {
			fmt.Fprintln(stdout, "Or name a team template, a web app ready for a kind of work:")
			for _, template := range offered {
				fmt.Fprintf(stdout, "  %-20s %s\n", firstNonEmpty(template.key, template.Name), template.Summary)
			}
		}
		if clone, err = teamAskUntil(stdin, stdout, "Repository [new]", false, check); err != nil {
			return err
		}
	}
	switch {
	case answers.adoption != nil || answers.Clone != "":
	case clone == "" || clone == "new":
	case clone == "empty":
		answers.Empty = true
	case templates[clone]:
		answers.Template = clone
	default:
		answers.Clone = clone
	}
	return nil
}

// teamAskUntil asks until check accepts the answer; Enter gives "". In a
// terminal a wrong answer is asked again; otherwise it ends the setup.
func teamAskUntil(stdin io.Reader, stdout io.Writer, question string, secret bool, check func(string) error) (string, error) {
	if teamStyled(stdin, stdout) {
		return teamFieldAsk(stdin, stdout, question, secret, check)
	}
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
	if missing := teamMissingScopes(token); len(missing) > 0 {
		return "", fmt.Errorf("%s, and paste the bot token again", teamScopesAdvice(missing))
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

// teamCheckChannel checks the app, @handle in Slack, is in the channel.
func teamCheckChannel(handle, botToken, id string) (string, error) {
	if !slackIDPattern.MatchString(id) {
		return "", fmt.Errorf("a channel ID looks like C07ABC123, not %q", id)
	}
	channel, member, err := teamSlack.channel(botToken, id)
	if err != nil {
		return "", fmt.Errorf("the app can't see channel %s (invite it with /invite @%s): %w", id, handle, err)
	}
	if !member {
		return "", fmt.Errorf("the app isn't in #%s yet; invite it there with /invite @%s", channel, handle)
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
	switch {
	case teamProvider(member) == teamProviderChatGPT && firstNonEmpty(member.Fallback, teamFallbackModel(member)) == "":
		label += " on ChatGPT, falling back to OpenRouter"
	case teamProvider(member) == teamProviderChatGPT:
		label += " on ChatGPT, falling back to " + firstNonEmpty(member.Fallback, teamFallbackModel(member)) + " on OpenRouter"
	case member.Agent == "claude" && member.Fallback != "":
		label += " on Claude, falling back to " + member.Fallback + " on OpenRouter"
	case member.Agent == "claude":
		label += " on Claude, falling back to OpenRouter"
	}
	if effort := teamEffort(member); effort != teamDefaultEffort {
		label += ", effort " + effort
	}
	if member.Bundle != "" {
		label += ", bundle " + member.Bundle
	}
	return label
}

// teamDefaultEffort is how hard a member thinks unless team.json says.
const teamDefaultEffort = "medium"

func teamEffort(member teamMember) string { return firstNonEmpty(member.Effort, teamDefaultEffort) }

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
	if teamStyled(stdin, stdout) {
		hint := ""
		if fallback != "" {
			hint = " [" + fallback + "]"
		}
		answer, err := teamFieldAsk(stdin, stdout, question+hint, false, func(answer string) error {
			if answer == "" && fallback == "" {
				return errors.New("give an answer")
			}
			return nil
		})
		return firstNonEmpty(answer, fallback), err
	}
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
	value, err := teamReadSecret(stdin, stdout, label)
	if err == nil && value == "" {
		err = fmt.Errorf("no %s was entered", strings.ToLower(label))
	}
	return value, err
}

// teamReadSecret reads a secret without showing it; Enter gives "".
func teamReadSecret(stdin io.Reader, stdout io.Writer, label string) (string, error) {
	if teamStyled(stdin, stdout) {
		return teamFieldAsk(stdin, stdout, label, true, func(string) error { return nil })
	}
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
	return strings.TrimSpace(stripTerminalCodes(value)), nil
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
	channel, err := teamCheckChannel(teamBotHandle(config), secrets.SlackBotToken, config.Channel)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Slack: workspace %s, channel #%s.\n", workspace, channel)
	return nil
}

// createTeamFolder makes the folder, the repository, and the lead's first
// files.
func createTeamFolder(config *teamConfig, secrets teamSecrets, clone string, empty bool, adopt *teamAdoption, stdout io.Writer) error {
	dir := teamDir(config.Name)
	// A new team's addresses are internal only (hi_team_internal.md).
	if config.Public == nil {
		config.Public = &teamPublic{}
	}
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
	if adopt != nil {
		if err := os.Rename(adopt.stage, repo); err != nil {
			return err
		}
		adopt.stage = ""
	} else if clone != "" {
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
	} else if err := newTeamRepo(*config, repo, empty); err != nil {
		return err
	}
	// A cloned or empty project still takes a template's roles.
	if config.Template != "" && (clone != "" || empty) {
		if _, _, err := teamComposeTemplate(*config); err != nil {
			return err
		}
	}
	config.Branch = firstNonEmpty(boxGit(repo, "branch", "--show-current"), "main")
	if adopt != nil {
		if err := teamSetUpAdopted(config, adopt, repo, stdout); err != nil {
			return err
		}
	}
	if err := saveTeam(*config); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "slack-manifest.json"), []byte(teamManifest(*config)+"\n"), 0o600); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "lead", "SOUL.md"), []byte(teamSoul(*config)), 0o600)
}

// teamComposeTemplate composes the team's template, team-app or one that
// extends it, and keeps its roles in the team's folder.
func teamComposeTemplate(config teamConfig) (*composedTemplate, *templateCatalog, error) {
	catalog := &templateCatalog{}
	var err error
	if config.Template == "" {
		catalog.layers, err = builtinTemplateLayers()
	} else {
		catalog, err = loadTemplateCatalog(os.Stderr)
	}
	if err != nil {
		return nil, nil, err
	}
	name := firstNonEmpty(config.Template, teamAppTemplate)
	if config.Template != "" && !teamIsTemplate(catalog, name) {
		return nil, nil, fmt.Errorf("%q isn't a team template: one that extends %s (hi init --list shows the templates)", name, teamAppTemplate)
	}
	composed, err := composeTemplateAllowingHidden(catalog.layers, name, map[string]string{"name": config.Name})
	if err != nil {
		return nil, nil, err
	}
	return composed, catalog, nil
}

// teamTemplates are the templates a team can start from: those that
// extend team-app, by name.
func teamTemplates(catalog *templateCatalog) []*templateLayer {
	var templates []*templateLayer
	for _, key := range sortedKeys(catalog.layers) {
		if key != teamAppTemplate && teamIsTemplate(catalog, key) {
			templates = append(templates, catalog.layers[key])
		}
	}
	return templates
}

// teamIsTemplate says whether a template extends team-app.
func teamIsTemplate(catalog *templateCatalog, name string) bool {
	layer, ok := catalog.layers[name]
	if !ok || (layer.Hidden && name != teamAppTemplate) {
		return false
	}
	chain, err := layerChain(catalog.layers, name)
	if err != nil {
		return false
	}
	for _, layer := range chain {
		if layer.Name == teamAppTemplate {
			return true
		}
	}
	return false
}

func newTeamRepo(config teamConfig, repo string, empty bool) error {
	if err := os.MkdirAll(repo, 0o755); err != nil {
		return err
	}
	var entries []initEntry
	if empty {
		files := map[string]string{
			"README.md":                  "# " + config.Name + "\n\n" + config.Purpose + "\n",
			"AGENTS.md":                  teamAgentsFile,
			teamSpecsPath + "/README.md": teamSpecsReadme(config),
		}
		for _, name := range sortedKeys(files) {
			entries = append(entries, initEntry{path: name, data: []byte(files[name])})
		}
	} else {
		var err error
		if entries, err = teamAppEntries(config); err != nil {
			return err
		}
	}
	if err := writeInitFiles(repo, entries); err != nil {
		return err
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

// teamAppTemplate is what a new team's project starts from: the app
// template with a blank page, since nearly every team builds a web app.
// The helper's chat box needs nothing from it: hi adds it to every page.
const teamAppTemplate = "team-app"

// teamAppEntries are the files of a new team's project: the template's,
// with the team's purpose in README.md, the team's rules in AGENTS.md, and
// the lead's specs folder. The template's skills are left out: they are
// about hi, which the members' boxes don't have. Its team/ folder gives
// the team's roles.
func teamAppEntries(config teamConfig) ([]initEntry, error) {
	composed, catalog, err := teamComposeTemplate(config)
	if err != nil {
		return nil, err
	}
	composed.Skills = nil
	readme := string(composed.Files["README.md"])
	_, rest, _ := strings.Cut(readme, "\n\n")
	composed.Files["README.md"] = []byte("# " + titleName(config.Name) + "\n\n" + config.Purpose + "\n\n" + rest)
	composed.Files["AGENTS.md"] = append(composed.Files["AGENTS.md"], "\n"+teamAgentsFile...)
	composed.Files[teamSpecsPath+"/README.md"] = []byte(teamSpecsReadme(config))
	entries, skillSources, err := templateEntries(catalog, composed)
	if err != nil {
		return nil, err
	}
	metadata, err := templateMetadata(composed, entries, skillSources, config.Name, nil)
	if err != nil {
		return nil, err
	}
	return append(entries, initEntry{path: templateMetadataPath, data: metadata}), nil
}

const teamAgentsFile = `# Working on this project

This project is built by a hi team: its owners, with coding agents in the
team's dev box, for the people in its Slack channel. The people may not
read code, so the code, its tests, and its docs must explain themselves.

- docs/specs/ is what was agreed, feature by feature. Larger work starts
  with its spec, agreed with the owner; keep a feature's spec current with
  its code.
- docs/map.md is the map of what the project already has and where it
  lives. Read it before adding anything, extend what it lists rather than
  building beside it, and keep it current in the same work as a change.
  A technical choice someone shouldn't undo by accident gets a short file
  in docs/decisions/.
- Keep each branch to one piece of work. If something else needs doing,
  file it as a task (hi dev task add) rather than fixing it on the way.
- Every change comes with tests for what it must do. Run them before you
  ship, and say whether they pass.
- Data the app must keep (the people's entries, uploads, a database) goes
  in the folder named by $HI_DATA_DIR, never in the project's own folder,
  which is a fresh copy of the code at every release.
- $HI_SLOT is preview or production. Work in the background that costs
  money (a model call per item), reads an outside service a lot (a
  backfill, a scrape), or does something outside the app (sends mail,
  posts, places orders) runs in full only when $HI_SLOT is production; in
  a preview it runs on a small sample, such as the last few days. Preview
  and production run at the same time. Production owns the data; the
  preview runs on a fork of it from the last release, and what it writes
  stays its own. A setting may be production's only: code that needs it
  does without it in the preview.
- Background workers start once the file named by $HI_WORKERS_FILE
  exists and stop when it goes away: at a release hi stops the old
  version's workers before the new version starts. When the last one has
  stopped, the app creates $HI_WORKERS_FILE.stopped. Without the
  variable, as in the tests, they run as before. The database waits for
  a lock rather than failing (SQLite: a busy timeout of 10 s or more).
- The project starts with ` + "`make run`" + `: a Makefile run target that starts the
  app on the port in $PORT (8080 if it's unset), listening on 0.0.0.0, so
  hi can put it on the team's preview.
- Commit your work on your branch with a message that says what changed and
  why. There is no push: hi dev ship merges the branch into dev, and an
  owner releases dev to production.
- Keep README.md current: what the project does, how to run it, how to
  test it.
`

// teamBotName is the bot's handle in Slack: the team's name, so people
// write @payments. Apps made before v0.48.0 are named team-<name>.
func teamBotName(name string) string { return name }

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
	if err := teamInstallSnapshotTimer(config, executable); err != nil {
		return err
	}
	for _, args := range [][]string{{"daemon-reload"}, {"enable", teamUnitName(name)}, {"restart", teamUnitName(name)}, {"enable", "--now", teamSnapshotUnitName(name) + ".timer"}} {
		if out, err := teamSystemctl(args...); err != nil {
			return fmt.Errorf("systemctl --user %s: %s", strings.Join(args, " "), qFirstLine(out, err.Error()))
		}
	}
	fmt.Fprintf(stdout, "Team %s is starting. It answers in its Slack channel in a minute or so.\n", name)
	if secrets, err := loadTeamSecrets(name); err == nil {
		if missing := teamMissingScopes(secrets.SlackBotToken); len(missing) > 0 {
			fmt.Fprintf(stdout, "\nWarning: %s. The bot token stays the same.\n\n", teamScopesAdvice(missing))
		}
	}
	if client, _ := teamWatchClient(); client == nil {
		fmt.Fprintf(stdout, "\nWarning: %s.\n", strings.TrimSuffix(teamCapitalize(errTeamNotWatched.Error()), "."))
	}
	if warning := boxRootGroupsWarning(); warning != "" {
		fmt.Fprintf(stdout, "\nWarning: %s\n", warning)
	}
	if !teamResticInstalled() {
		fmt.Fprintf(stdout, "\nWarning: restic isn't installed, so the team can't take its daily snapshots. hi install restic adds it.\n\n")
	}
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
	teamSystemctl("disable", "--now", teamSnapshotUnitName(name)+".timer")
	// The dev box keeps running when the service restarts, not when the
	// team stops.
	teamStopDevBox(name)
	// The hi server stops watching for its heartbeat until hi team up.
	(&teamBroker{team: name}).sendHeartbeat("down")
	fmt.Fprintf(stdout, "Team %s is stopped. Its folder stays; hi team up %s starts it again.\n", name, name)
	return nil
}

// teamRebuild builds hi's base image again, with Ubuntu's current
// packages, and restarts the team on it: the lead now, the preview and
// production within minutes, on the same addresses (keepLinks). The dev
// box keeps the owners' agents running until hi team dev restart. The
// image is the machine's, so it names the other teams still on the old
// one.
func teamRebuild(name string, stdout, stderr io.Writer) error {
	if _, err := loadTeam(name); err != nil {
		return err
	}
	if err := teamRebuildImage(stdout, stderr); err != nil {
		return err
	}
	if err := teamUp(name, stdout); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "\nIts preview and production move to the new image in a few minutes, on the same addresses. The dev box moves when it restarts: hi team dev %s restart (agents working there stop; herdr brings their conversations back).\n", name)
	var others []string
	entries, _ := os.ReadDir(teamsDir())
	for _, entry := range entries {
		if entry.Name() != name && teamNamePattern.MatchString(entry.Name()) && teamState(entry.Name()) == "up" {
			others = append(others, entry.Name())
		}
	}
	if len(others) > 0 {
		fmt.Fprintf(stdout, "The image is this machine's, so %s still run the old one: hi team up <name> restarts each on the new one.\n", strings.Join(others, ", "))
	}
	return nil
}

// teamRebuildImage builds hi's base image again; tests replace it.
var teamRebuildImage = func(stdout, stderr io.Writer) error {
	engine, _, err := detectBoxEngine()
	if err != nil {
		return err
	}
	_, err = engine.rebuildBaseImage(stdout, stderr)
	return err
}

// teamStopDevBox stops a team's dev box; tests replace it.
var teamStopDevBox = func(name string) {
	if engine, _, err := detectBoxEngine(); err == nil {
		stopTeamDev(engine, name)
		stopTeamPeers(engine, name)
	}
}

// teamRemoveContainers removes a team's containers and its members'
// boxes; tests replace it.
var teamRemoveContainers = func(name string, boxes []string) {
	engine, _, err := detectBoxEngine()
	if err != nil {
		return
	}
	for _, slot := range []string{teamSlotPreview, teamSlotProduction} {
		if state, ok := loadTeamSlot(name, slot); ok && state.Expose != "" {
			netExposeStop([]string{state.Expose}, io.Discard)
		}
	}
	if out, err := engine.output("ps", "-a", "--format", "{{.Names}}"); err == nil {
		for _, container := range strings.Fields(out) {
			if strings.HasPrefix(container, teamPreviewContainer(name)) {
				engine.output("rm", "-f", container)
			}
		}
	}
	engine.output("rm", "-f", teamMemoryContainer(name))
	stopTeamResolver(name)
	teardownBox(engine, boxMeta{Name: "team-" + name})
	stopTeamDev(engine, name)
	stopTeamPeers(engine, name)
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
	teamRemoveSnapshotTimer(name)
	teamSystemctl("daemon-reload")
	teamRemoveContainers(name, boxes)
	if err := os.Remove(link); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if folder != "" && !keep {
		if err := teamRemoveAll(folder); err != nil {
			return fmt.Errorf("the team is off this machine, but not all of its folder is deleted: %w", err)
		}
	}
	fmt.Fprintf(stdout, "Team %s is removed.", name)
	if folder != "" && keep {
		fmt.Fprintf(stdout, " Its folder stays in %s; hi team up %s brings the team back.", folder, folder)
	}
	if snapshots := teamSnapshotsDir(name); fileExists(snapshots) {
		fmt.Fprintf(stdout, "\nIts snapshots stay in %s, with the key to them; delete that folder if you no longer want them.", snapshots)
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
		if !teamNamePattern.MatchString(entry.Name()) {
			continue // settings.json, not a team
		}
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
	if line := teamSnapshotLine(name); line != "" {
		fmt.Fprintln(stdout, "\n"+line)
	}
	fmt.Fprintln(stdout, teamWatchLine(name))
	if line := teamFallbackLine(name); line != "" {
		fmt.Fprintln(stdout, line)
	}
	fmt.Fprintln(stdout, "\n"+teamHelperLine(config))
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
	if len(words) != 1 {
		return usageError{"usage: hi team logs <name> [-f]"}
	}
	if _, err := loadTeam(words[0]); err != nil {
		return err
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
