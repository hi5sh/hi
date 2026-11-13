package cli

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
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/slack-go/slack"
)

// hi app runs a team's app and its dev box: its owners build in the dev
// box with coding agents and release from the dev page, and a lead
// (Hermes) helps the people in its Slack channel, files what they ask for
// as tickets, and watches the app. A team is a folder; its containers are
// rebuilt from it. See docs/specs/approved/hi_app_dev.md.

var (
	appNamePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,19}$`)
	appRolePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,15}$`)
	slackIDPattern = regexp.MustCompile(`^[A-Z0-9]{6,20}$`)
)

// appConfig is app.json. It holds no secrets.
type appConfig struct {
	Name    string `json:"name"`
	Purpose string `json:"purpose"`
	Channel string `json:"channel"`
	// OpsChannel is where incidents, warnings, security, keys, and access
	// go (app_ops.go); empty, they go to Channel.
	OpsChannel string `json:"ops_channel,omitempty"`
	// HeartbeatEvery is how often the team's heartbeat goes, such as 5m;
	// empty is every minute. Its URL is a secret (app_heartbeat.go).
	HeartbeatEvery string   `json:"heartbeat_every,omitempty"`
	Owners         []string `json:"owners"`
	// Developers work in the dev box and on the dev page like the owners,
	// except what reaches production; Contributors report bugs as tasks.
	// Owners set both; being in the channel gives nobody a level
	// (app_access.go).
	Developers []string `json:"developers,omitempty"`
	// VibeDevelopers work in the dev page's Chat only: no terminal, VS
	// Code, or SSH.
	VibeDevelopers []string             `json:"vibe_developers,omitempty"`
	Contributors   []string             `json:"contributors,omitempty"`
	Members        map[string]appMember `json:"members"`
	Remote         string               `json:"remote,omitempty"` // where repo/ was cloned from
	Branch         string               `json:"branch"`           // the branch the lead merges into
	Memory         string               `json:"memory,omitempty"` // "off": Hermes' built-in memory only, no Hindsight
	// MemoryModel is the OpenRouter model Hindsight keeps the lead's memory
	// with; this machine's Hermes default unless set (app_memory.go).
	MemoryModel string      `json:"memory_model,omitempty"`
	Backups     *appBackups `json:"backups,omitempty"`
	// Helper turns the helper, help and feedback in the app, off for an
	// address (preview, production); on unless set (app_helper.go).
	Helper map[string]bool `json:"helper,omitempty"`
	// Public says which addresses (production, preview, dev) are public
	// too, through hi net expose; the rest are on the team's NetBird
	// network only. A team made before hi 2.5 has none: all public, until
	// an owner says otherwise (hi_app_internal.md).
	Public *appPublic `json:"public,omitempty"`
	// Domain puts the team's app on the machine's domain (hi app domain
	// <team> on): names that stay, rather than ones NetBird makes up.
	Domain bool `json:"domain,omitempty"`
	// Addresses is what the team's addresses on the NetBird network are
	// named (app_addresses.go): "" for the machine's domain, on https,
	// when it has one (hi net domain); "netbird" for the NetBird name; or
	// a host name an IT team keeps in its DNS by hand; or "names", each
	// address at a name of its own (Names).
	Addresses string `json:"addresses,omitempty"`
	// BoxMemory is the memory limit of the team's dev box ("dev") and of
	// its app's boxes, Stage's and production's ("app"), when it isn't
	// hi's (hi app limit <team> memory).
	BoxMemory map[string]string `json:"box_memory,omitempty"`
	// Names are each address's own name, which an IT team keeps
	// (docs/specs/approved/hi_app_addresses.md).
	Names *appNames `json:"names,omitempty"`
	// SitesCheck is hi's check of the sites the dev box asks for: "" for
	// the rules (the default), or "off" (hi_app_dev_sites.md).
	SitesCheck string `json:"sites_check,omitempty"`
	// DevViewProductionOff, when set, says who turned the dev box's view
	// of production off, and when (hi_app_dev_view.md).
	DevViewProductionOff string `json:"dev_view_production_off,omitempty"`
	// AllowIPs are the only addresses the team's preview and production
	// let in (hi app allow-ip <team>; hi net expose --allow-ip).
	AllowIPs []string `json:"allow_ips,omitempty"`
	// PreviewData is "empty" for a team whose preview starts empty at every
	// release; otherwise it starts on a fork of production's data
	// (app_data_fork.go). PreviewModelUSD is what the preview's model
	// calls may cost a day: 0 for appPreviewModelUSD, below 0 for no
	// limit (app_keys.go).
	PreviewData     string  `json:"preview_data,omitempty"`
	PreviewModelUSD float64 `json:"preview_model_usd,omitempty"`
	// Template is the team template the project and the roles started
	// from, one that extends team-app, such as one from the org server's
	// sources; empty is team-app itself.
	Template string `json:"template,omitempty"`
	// Adopted is where a project someone else wrote came from (hi app
	// adopt); ProjectAgentSettings lets members load its own agent
	// settings (hooks, MCP servers), off by default (app_adopt.go).
	Adopted              *appAdopted `json:"adopted,omitempty"`
	ProjectAgentSettings bool        `json:"project_agent_settings,omitempty"`
	// LockModels keeps the dev page's chat on the model and effort the
	// sessions start with: its developers don't pick them (hi app models).
	LockModels bool `json:"lock_models,omitempty"`
	// MadeAs is the name the team was made with, when hi app rename has
	// given it another since: its memory bank and its snapshots keep it.
	MadeAs  string    `json:"made_as,omitempty"`
	Created time.Time `json:"created"`
}

// appMember is one role: an agent, its model, and what its box may use.
type appMember struct {
	Agent   string   `json:"agent"`
	Model   string   `json:"model,omitempty"`
	Bundle  string   `json:"bundle,omitempty"`
	Network string   `json:"network,omitempty"` // a member box's network preset; dev by default
	Allow   []string `json:"allow,omitempty"`
	// Provider is who pays for the member's model calls: openrouter
	// (empty) or chatgpt, the team's ChatGPT plan; Fallback is the same
	// model on OpenRouter, which takes over when the plan refuses
	// (app_subscriptions.go).
	Provider string `json:"provider,omitempty"`
	Fallback string `json:"fallback,omitempty"`
	// Effort is how hard the member thinks; medium unless set.
	Effort string `json:"effort,omitempty"`
	// Name is what the lead goes by in Slack, such as a person's name;
	// the team's name unless set (appLeadName).
	Name string `json:"name,omitempty"`
}

// appSecrets is secrets.json, readable only by its owner.
type appSecrets struct {
	// loaded is the file as loadTeamSecrets read it, so saveTeamSecrets
	// writes only what the caller changed; not kept.
	loaded *appSecrets

	SlackBotToken string `json:"slack_bot_token"`
	SlackAppToken string `json:"slack_app_token"`
	// OpenRouterAPIKey is the team's own; without it the team uses this
	// machine's, from Hermes' .env (app_keys.go).
	OpenRouterAPIKey string `json:"openrouter_api_key,omitempty"`
	// Env is the settings the team's app boxes get (hi app env).
	Env map[string]string `json:"env,omitempty"`
	// EnvProduction and EnvPreview are settings one slot gets alone:
	// production's only (hi app env --production), or a value of the
	// preview's own (--preview). A name is in Env or in these, never both.
	EnvProduction map[string]string `json:"env_production,omitempty"`
	EnvPreview    map[string]string `json:"env_preview,omitempty"`
	// Files are settings the app's boxes get as files (hi app env
	// --file), base64, by the name of the variable that holds the file's
	// path; FilesProduction and FilesPreview are one slot's alone, as with
	// Env. A name is a value or a file, never both.
	Files           map[string]string `json:"files,omitempty"`
	FilesProduction map[string]string `json:"files_production,omitempty"`
	FilesPreview    map[string]string `json:"files_preview,omitempty"`
	// EnvDev and FilesDev are the dev box's own (hi app env --dev), such
	// as a test database's: it gets no others, so production's keys stay
	// out of reach of its agents.
	EnvDev   map[string]string `json:"env_dev,omitempty"`
	FilesDev map[string]string `json:"files_dev,omitempty"`
	// HeartbeatURL is where the team's heartbeat goes (app_heartbeat.go).
	HeartbeatURL string `json:"heartbeat_url,omitempty"`
	// The bucket snapshots are copied to (hi app backups).
	BackupKeyID  string `json:"backup_access_key_id,omitempty"`
	BackupSecret string `json:"backup_secret_access_key,omitempty"`
	// LeadAPIKey opens Hermes' API server in the lead's box to the team's
	// service, for the helper; made on the first start.
	LeadAPIKey string `json:"lead_api_key,omitempty"`
	// ChatGPT is the team's own ChatGPT sign-in; without it, roles on
	// ChatGPT use this machine's, Codex's (app_subscriptions.go).
	ChatGPT *appChatGPTGrant `json:"chatgpt,omitempty"`
	// ClaudeToken is the team's own, from claude setup-token; without it,
	// Claude members use this machine's sign-in.
	ClaudeToken    string    `json:"claude_token,omitempty"`
	ClaudeSignedIn time.Time `json:"claude_signed_in,omitempty"`
}

func appsDir() string {
	if dir := os.Getenv("HI_APPS_DIR"); dir != "" {
		return dir
	}
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".local", "share")
	}
	return migrateAppsMachine(filepath.Join(base, "hi", "apps"))
}

// A team's folder is wherever it was made, ./team-<name> by default.
// appsDir holds a link to each, by name, so hi app <command> <name>
// finds it from anywhere.
func appDir(name string) string {
	link := filepath.Join(appsDir(), name)
	if target, err := filepath.EvalSymlinks(link); err == nil {
		return target
	}
	return link
}

// appRegister links a team's name to its folder.
func appRegister(name, folder string) error {
	if err := os.MkdirAll(appsDir(), 0o700); err != nil {
		return err
	}
	link := filepath.Join(appsDir(), name)
	if info, err := os.Lstat(link); err == nil {
		if info.Mode()&os.ModeSymlink == 0 {
			return fmt.Errorf("%s is in the way: it should be a link to app %s's folder", link, name)
		}
		os.Remove(link)
	}
	return os.Symlink(folder, link)
}

func appUnitName(name string) string { return "hi-app-" + name + ".service" }

// appConfigFile is an app's settings in its folder; apps made before v2.6.67
// kept them in team.json, until hi app up moves them (appMigrate).
const (
	appConfigFile       = "app.json"
	legacyAppConfigFile = "team.json"
)

// defaultTeamMembers is the roster the wizard offers: the reviewer is a
// different agent from the coder, so the code is checked by someone other
// than its author. Both get the web bundle, a browser: a team builds a web
// app, and the lead's specs ask for its pages to be looked at (on team
// orbit, 2026-10-23, a coder without one built Chromium by hand); and
// the data bundle, DuckDB and charts, for the files the app works with.
func defaultTeamMembers() map[string]appMember {
	lead := readHermesModelConfig(filepath.Join(hermesHome(), "config.yaml")).Default
	return map[string]appMember{
		"lead":     {Agent: "hermes", Model: lead},
		"coder":    {Agent: "claude", Model: "opus", Bundle: "web,data"},
		"reviewer": {Agent: "codex", Bundle: "web,data"},
	}
}

func validateTeam(config appConfig) error {
	if !appNamePattern.MatchString(config.Name) {
		return fmt.Errorf("an app's name starts with a letter and has lowercase letters, digits, and dashes, up to 20: %q", config.Name)
	}
	if !slackIDPattern.MatchString(config.Channel) {
		return fmt.Errorf("the channel is a Slack channel ID, such as C07ABC123, not %q", config.Channel)
	}
	if config.OpsChannel != "" && !slackIDPattern.MatchString(config.OpsChannel) {
		return fmt.Errorf("the ops channel is a Slack channel ID, such as C07ABC123, not %q", config.OpsChannel)
	}
	if len(config.Owners) == 0 {
		return errors.New("an app needs at least one owner")
	}
	for _, id := range slices.Concat(config.Owners, config.Developers, config.VibeDevelopers, config.Contributors) {
		if !slackIDPattern.MatchString(id) && !appIsEmail(id) {
			return fmt.Errorf("people are Slack member IDs, such as U07ABC123, or email addresses, not %q", id)
		}
	}
	if config.Members["lead"].Agent != "hermes" {
		return errors.New("the lead is Hermes")
	}
	if err := appCheckLeadName(config.Members["lead"].Name); err != nil {
		return err
	}
	for role, member := range config.Members {
		if member.Name != "" && role != "lead" {
			return fmt.Errorf("%s: only the lead has a name of its own", role)
		}
	}
	for role, member := range config.Members {
		if !appRolePattern.MatchString(role) {
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
		case "", appProviderOpenRouter, "claude":
			if member.Provider == "claude" && member.Agent != "claude" {
				return fmt.Errorf("%s: a Claude plan is for Claude Code, not %s", role, agentDisplayName(member.Agent))
			}
			if member.Fallback != "" && member.Agent == "hermes" {
				return fmt.Errorf("%s: a fallback is for a member on a subscription; on OpenRouter it has none", role)
			}
		case appProviderChatGPT:
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

func loadTeam(name string) (appConfig, error) {
	var config appConfig
	if !appNamePattern.MatchString(name) {
		return config, fmt.Errorf("no app named %q", name)
	}
	migrateAppConfig(appDir(name))
	data, err := os.ReadFile(filepath.Join(appDir(name), appConfigFile))
	if errors.Is(err, os.ErrNotExist) {
		if target, linkErr := os.Readlink(filepath.Join(appsDir(), name)); linkErr == nil {
			return config, fmt.Errorf("app %s's folder %s is gone; if you moved it, run hi app up <its new folder>", name, target)
		}
		return config, fmt.Errorf("no app named %s; hi app ls lists them", name)
	}
	if err != nil {
		return config, err
	}
	if err := json.Unmarshal(data, &config); err != nil {
		return config, fmt.Errorf("app.json: %w", err)
	}
	return config, nil
}

// saveTeam writes app.json whole, or not at all: a reader never sees half
// of it, and one that can't be written as JSON (a dollar amount of inf)
// is refused, not written empty.
func saveTeam(config appConfig) error {
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("the app's settings: %w", err)
	}
	return writeFileAtomic(filepath.Join(appDir(config.Name), appConfigFile), append(data, '\n'), 0o600)
}

func loadTeamSecrets(name string) (appSecrets, error) {
	var secrets appSecrets
	data, err := os.ReadFile(filepath.Join(appDir(name), "secrets.json"))
	if err != nil {
		return secrets, err
	}
	if err := json.Unmarshal(data, &secrets); err != nil {
		return secrets, err
	}
	var loaded appSecrets
	json.Unmarshal(data, &loaded)
	secrets.loaded = &loaded
	return secrets, nil
}

// saveTeamSecrets writes secrets.json whole, under the lock the service's
// sign-in refreshes take (updateTeamSecrets). For secrets that came from
// loadTeamSecrets, only what the caller changed is written, over the file
// as it is now: a command that waited at a prompt doesn't put back a
// ChatGPT grant the service refreshed meanwhile.
func saveTeamSecrets(name string, secrets appSecrets) error {
	if secrets.loaded == nil {
		return updateTeamSecretsFile(name, func(current *appSecrets) error {
			*current = secrets
			return nil
		}, true)
	}
	return updateTeamSecretsFile(name, func(current *appSecrets) error {
		now, before, after := reflect.ValueOf(current).Elem(), reflect.ValueOf(secrets.loaded).Elem(), reflect.ValueOf(&secrets).Elem()
		for i := range after.NumField() {
			if !after.Type().Field(i).IsExported() {
				continue
			}
			if !reflect.DeepEqual(before.Field(i).Interface(), after.Field(i).Interface()) {
				now.Field(i).Set(after.Field(i))
			}
		}
		return nil
	}, false)
}

// ---------------------------------------------------------------------------
// the command

// appStageCommands take stage for preview, and --stage for --preview.
var appStageCommands = map[string]bool{"data": true, "limit": true, "bom": true, "env": true, "jobs": true, "incidents": true, "helper": true}

func runTeam(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		if os.Getenv(appSocketEnv) != "" {
			// In the lead's box: the lead's commands, not the machine's.
			fmt.Fprint(stdout, appLeadUsage())
			return 0
		}
		printTeamUsage(stdout)
		return 0
	}
	command, rest := args[0], args[1:]
	// Stage is what people call the preview: the commands that name it
	// take either.
	if appStageCommands[command] {
		rest = slices.Clone(rest)
		for i, arg := range rest {
			switch arg {
			case "stage":
				rest[i] = appSlotPreview
			case "--stage":
				rest[i] = "--preview"
			}
		}
	}
	// The lead's own commands, inside its box.
	if command == "channel" {
		return exitCode(runTeamChannel(rest, stdout), stderr)
	}
	if appLeadCommands[command] || appLeadOrHost[command] && os.Getenv(appSocketEnv) != "" {
		return exitCode(runTeamLeadCommand(command, rest, stdin, stdout), stderr)
	}
	var err error
	switch command {
	case "new":
		err = appNew(rest, stdin, stdout)
	case "adopt":
		err = appAdoptCommand(rest, stdin, stdout)
	case "up":
		if len(rest) == 0 && interactive(stdin, stdout) {
			name, pickErr := appPick(stdin, stdout, "Start or restart an app")
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
				if name, adoptErr = appAdopt(name, stdout); adoptErr != nil {
					return adoptErr
				}
			}
			return appUp(name, stdout)
		})
	case "down":
		err = withTeamName(rest, func(name string) error { return appDown(name, stdout) })
	case "move":
		err = appMoveCommand(rest, stdin, stdout)
	case "machines":
		err = appMachinesCommand(rest, stdout)
	case "__receive":
		return runAppReceive(rest, stdin, stdout, stderr)
	case "rebuild":
		err = withTeamName(rest, func(name string) error { return appRebuild(name, stdout, stderr) })
	case "rm", "remove":
		err = appRemove(rest, stdin, stdout)
	case "rename":
		err = appRename(rest, stdin, stdout)
	case "ls", "list":
		err = appList(stdout)
	case "status":
		err = withTeamName(rest, func(name string) error { return appStatus(name, stdout) })
	case "logs":
		err = appLogs(rest, stdout, stderr)
	case "upstream":
		err = appUpstreamCommand(rest, stdout)
	case "jobs":
		err = appJobsCommand(rest, stdout)
	case "incidents":
		err = appIncidentsCommand(rest, stdout)
	case "job":
		err = appJobCommand(rest, stdout)
	case "gitlab":
		if len(rest) != 2 {
			err = usageError{"usage: hi app gitlab <name> <group/project | host/group/project>"}
			break
		}
		config, loadErr := loadTeam(rest[0])
		if err = loadErr; err == nil {
			if err = appCheckGitLab(rest[1]); err == nil {
				err = appConnectGitLab(&config, rest[1], stdout)
			}
		}
	case "github":
		if len(rest) != 2 {
			err = usageError{"usage: hi app github <name> <owner/repository>"}
			break
		}
		config, loadErr := loadTeam(rest[0])
		if err = loadErr; err == nil {
			if err = appCheckGitHub(rest[1]); err == nil {
				err = appConnectGitHub(&config, rest[1], stdout)
			}
		}
	case "snapshot":
		err = appSnapshotCommand(rest, stdout)
	case "snapshots":
		err = withTeamName(rest, func(name string) error { return appSnapshotsCommand(name, stdout) })
	case "restore":
		err = appRestoreCommand(rest, stdin, stdout)
	case "rollback":
		err = appRollbackCommand(rest, stdin, stdout)
	case "data":
		err = appDataCommand(rest, stdin, stdout)
	case "say":
		err = appSayCommand(rest, stdin, stdout)
	case "dev":
		err = appDevCommand(rest, stdin, stdout)
	case "owners":
		err = appOwnersCommand(rest, stdin, stdout)
	case "access":
		err = appAccessCommand(rest, stdout)
	case "task", "tasks", "ticket", "tickets": // ticket: its name before hi 2.5.14
		err = appTicketCommand(rest, stdin, stdout)
	case "backups":
		err = appBackupsCommand(rest, stdin, stdout)
	case "key":
		err = appKey(rest, stdin, stdout)
	case "signin":
		err = appSignin(rest, stdin, stdout)
	case "member":
		err = appMemberCommand(rest, stdin, stdout)
	case "env":
		err = appEnv(rest, stdin, stdout)
	case "ops":
		err = appOpsCommand(rest, stdout)
	case "heartbeat":
		err = appHeartbeatCommand(rest, stdout)
	case "watch":
		err = appWatchCommand(rest, stdout)
	case "freeze", "unfreeze", "pause", "unpause":
		err = appStopCommand(command, rest, stdout)
	case "limit":
		err = appLimitCommand(rest, stdout)
	case "bom":
		err = appBomCommand(rest, stdout)
	case "advisories":
		err = appAdvisoriesCommand(rest, stdout)
	case "advisory":
		err = appAdvisoryCommand(rest, stdout)
	case "__app":
		return runTeamApp(rest, stderr)
	case "__resolver":
		return runTeamResolver(rest, stderr)
	case "__leadapi":
		return runTeamLeadAPI(rest, stderr)
	case "helper":
		err = appHelperCommand(rest, stdout)
	case "models":
		err = appModelsCommand(rest, stdout)
	case "domain":
		err = appDomainCommand(rest, stdin, stdout)
	case "addresses":
		err = appAddressesCommand(rest, stdout)
	case "allow-ip":
		err = appAllowIPCommand(rest, stdout)
	case "sso":
		err = appSSOCommand(rest, stdin, stdout)
	case "manifest":
		err = withTeamName(rest, func(name string) error {
			config, err := loadTeam(name)
			if err == nil {
				fmt.Fprintln(stdout, appManifestYAML(config))
			}
			return err
		})
	case "__serve":
		err = withTeamName(rest, func(name string) error { return appServe(name, stdout, stderr) })
	default:
		fmt.Fprintf(stderr, "hi: unknown app command %q\n\n", command)
		printTeamUsage(stderr)
		return 2
	}
	return exitCode(err, stderr)
}

func withTeamName(args []string, action func(string) error) error {
	if len(args) != 1 {
		return usageError{"name the app: hi app <command> <name>"}
	}
	return action(args[0])
}

func printTeamUsage(w io.Writer) {
	fmt.Fprintln(w, `hi app runs an app for the people in one Slack channel. Its owners build it
with coding agents in the app's dev box and release it from the dev page; a lead
(Hermes) helps the people in the channel, files what they ask for as tasks, and
watches the app.

usage:
  hi app new <name>                a step-by-step setup: Slack app, channel, people, members, code,
                                   in a new folder ./app-<name>
      [--dir <folder>]             the app's folder somewhere else
      [--from <file.json>]         the answers from a file; the Slack tokens from
                                   HI_APP_SLACK_BOT_TOKEN and HI_APP_SLACK_APP_TOKEN, the
                                   app's own OpenRouter key from HI_APP_OPENROUTER_API_KEY, and
                                   its ChatGPT sign-in from HI_APP_CHATGPT_AUTH (an auth.json), and
                                   its Claude token from HI_APP_CLAUDE_TOKEN
  hi app adopt <name> <source>     the same setup for a project someone else wrote, such as one on
                                   GitHub (github.com/owner/name, owner/name, or a URL): the app keeps
                                   its own copy and never pushes to it; an owner's agent surveys it
  hi app adopt <name> <folder>     the same for a folder on this machine, copied without its history:
                                   what git would commit, without .git, secrets such as .env, or
                                   links that point outside it; the folder doesn't change
      [--ref <branch|tag|commit>]  where to start; upstream's default branch otherwise
      [--github <owner/name>]      keep the app's copy in a new private GitHub repository too
      [--shallow|--full]           only its latest commit, or its whole history (a folder: all of it) even over 1 GB
      [--dir, --from, --yes]       as for hi app new; --yes doesn't ask before adopting
  hi app up [<name>]               start or restart the app and keep it up, also after a reboot;
                                   without a name, pick it from a list
  hi app up <folder>               the same for an app whose folder moved or was copied here
  hi app down <name>               stop it; its folder stays
  hi app move <name> <machine>     move it to another machine with hi: a stop of about a minute,
                                   the same addresses, its snapshots with it
      [--check]                    only what the machine has and lacks for it
      [--now]                      don't wait for running tasks; their commits stay on their branches
      [--dir <folder>]             its folder there; the same path by default
      [--yes]                      don't ask first
  hi app machines                  this machine's key for moving apps, and the machines it takes them from
  hi app machines add <machine> <key>  take apps from that machine (its key runs only hi's receiving end)
  hi app machines rm <machine>     no longer take apps from it
  hi app rebuild <name>            hi's base image again, with Ubuntu's current packages, and the
                                   machine's apps on it: what fixes the image's advisories
  hi app rm [<name>]               stop it and remove it from this machine: its service, boxes,
                                   and folder; without a name, pick it from a list
      [--keep-folder]              keep the folder; hi app up <folder> brings the app back
      [--yes]                      don't ask first
  hi app rename <name> <new>       give the app another name, after its Slack app's: its bot,
                                   its /hi-<name> command, its service; memory and snapshots stay
      [--yes]                      don't ask first
  hi app ls                        apps on this machine
  hi app status <name>             whether it is up, and its members
  hi app logs <name> [-f]          the lead's log; -f follows it
  hi app say <name> <text|->       tell the lead something from this machine: hi posts it in the
                                   channel, and the lead answers in its thread; what it asks to
                                   change, the lead files as a task
      [--wait]                     wait for the answer and print it
  hi app dev <name>                the app's dev box, where owners build with agents over SSH:
                                   whether it runs, its NetBird name, and how to get in
      [restart | netbird [<key file>] | new-host-key]  a new box; join NetBird; a new host key
      [start <task> [--agent claude|codex]]  a task's branch, worktree, and herdr workspace
      [finish <branch> [--discard]]  once it's shipped: its worktree, workspace, branch, and task
      [allow [<site> [--why <what for>] | --remove <site>]]  the sites the dev box reaches, as
                                   owners allow them on the dev page; a database's name too
  hi app owners <name>             the app's owners, who build in the dev box, and their SSH keys
      [add|remove <member ID> | key <member ID> <public key>|--remove]
  hi app access <name>             who reaches what: owners, Pro Devs and Vibe Devs (who build and
                                   release), and contributors (who report bugs); nobody else
      [<member ID> pro|vibe|contributor|none]  an owner's change, also on the dev page: a Pro
                                   Dev has Chat, terminal, and SSH, a Vibe Dev Chat; none
                                   takes the level away
      [key <member ID> <public key>|--remove]  a Pro Dev's or owner's SSH key for the dev box
  hi app task <name>               the app's tasks: GitHub Issues (gh) or GitLab issues (glab),
                                   where its repository is, or hi's own
      [list [--closed] | show <n> | add <title> [<body|->] | comment <n> <text|-> | close <n> [<comment>]]
  hi app channel <name> [--since 2h]  what was said in the channel (the last day by default)
  hi app key <name> openrouter     give the app an OpenRouter key of its own (asked, never an
                                   argument); --unset goes back to this machine's. No box holds it:
                                   the app reaches models at $OPENROUTER_BASE_URL through hi
  hi app key <name> slack          the Slack app's tokens again, when Slack gave new ones (asked
                                   for and checked; Enter keeps one); a running app restarts
  hi app ops <name> [<channel ID>|off]  a second channel for incidents, warnings, security, keys,
                                   and access; without one they go to the main channel
  hi app heartbeat <name> [<url> [--every 1m] | off | test]  a GET to an outside service, such as
                                   healthchecks.io, while the app runs: it alerts when they stop
  hi app watch <name>              the app's watcher, on its machine: health, network, advisories
      [--auto-freeze on|off]       whether it freezes the app on its own for the worst of them
  hi app freeze|unfreeze|pause|unpause <name> --why "…"  stop the app's boxes reaching anything,
                                   or pause them, within a minute, and undo it
  hi app signin <name> chatgpt     sign the app in to ChatGPT, for its roles on a ChatGPT plan: a
                                   code to type on any device; only the app's server refreshes it
  hi app signin <name> claude      the app's own Claude token, from claude setup-token (asked for):
                                   its Claude members use it before this machine's sign-in
      [--machine]                  use this machine's sign-in (Codex's, or Claude Code's) instead
  hi app member <name> <role>      a member's agent, model, and who pays for it
      [--provider openrouter|chatgpt] [--model <model>] [--fallback <model on OpenRouter>]
      [--effort low|medium|high|xhigh|max]  how hard it thinks; medium unless set
                                   on ChatGPT, the fallback (the same model on OpenRouter unless
                                   given) takes over when the plan refuses; a running app restarts
  hi app env <name>                the settings the app gets, by name
  hi app env <name> KEY=VALUE...   add or change settings: environment variables in the app's
                                   boxes only (Stage and production); KEY alone asks for the value
      [--file KEY=<path>]          a file, such as a certificate with its key: the app's boxes
                                   get it read-only, and $KEY holds its path there
      [--unset KEY]                remove one
      [--production]               production's only: a key that sends, posts, orders, or pays per
                                   call, which Stage mustn't use
      [--stage]                    a value of Stage's own, such as a test key; production
                                   keeps its own
      [--dev]                      the dev box's own, such as a test database's certificate; it
                                   gets no others, from its next start (hi app dev <name> restart)
  hi app limit <name>              what Stage's model calls may cost a day, and spent today
      [stage <usd> | stage off]      set it ($5 a day unless set); production has no limit from hi
  hi app limit <name> memory       the boxes' memory: the dev box 16g, Stage's and production's 4g
      [dev|app <size>|default]     the app's own, such as dev 24g; running boxes get it at once
    hi app bom <name> [v<n>|stage]    production's bill of materials (CycloneDX), or a release's
  hi app advisories <name>         known advisories for what production runs, from the app's hi
                                   server, which matches them every night
  hi app advisory <name> ignore <id> --because "<why>" [--package <p>]
                                   an owner's word that one doesn't apply, until it changes;
                                   unignore <id> takes it back
  hi app snapshot <name>           take a snapshot now: the lead's memory, the app's record, and
                                   its data; the app keeps running. It takes one daily too
  hi app snapshots <name>          list them
  hi app restore <name> <snapshot>  put the app back as it was: a snapshot's ID, latest, v3 (the
                                   one taken before v3 went out), or a day (2026-10-12)
      [--code]                     put the code (repo/) back too
      [--yes]                      don't ask first
  hi app rollback <name> v2        put production back on v2, with its data as v2 left it; what was
                                   entered since is kept in a snapshot, and going forward the same
                                   way puts it back
      [--code-only]                the code alone; production's data stays as it is
      [--yes]                      don't ask first
  hi app data <name>               the app's data folders: production's, and Stage's fork of
                                   it with what Stage wrote itself
  hi app data <name> fork          Stage gets a new fork of production's data now (it gets
                                   one at every release): all of it, in seconds, with no copy
  hi app data <name> copy stage production  production starts from what Stage gathered;
                                   its own data is snapshotted first
      [--yes]                      don't ask first
  hi app data <name> stage empty   Stage starts empty at every release, for an app whose
                                   people's data Stage shouldn't show; stage fork undoes it
  hi app backups <name>            where snapshots are kept, and for how long
      [--offsite s3:https://<host>/<bucket>]  copy them to an S3-compatible bucket too (asks for
                                   its keys); --offsite off stops
      [--region <region>]          the bucket's region, if it isn't us-east-1
      [--keep 7,4,6]               how many daily, weekly, and monthly snapshots to keep
      [--show-key]                 the key they are encrypted with; keep a copy elsewhere
  hi app helper <name>             whether the app's pages have the helper, help and feedback
      [stage|production on|off]    turn it on or off for an address, from the next page load
  hi app models <name> [on|off]    whether its developers pick the dev page chat's model and effort
  hi app addresses <name>          what its addresses on NetBird are named: this machine's domain
      [domain|netbird|<host>]      on https (hi net domain), the NetBird name, or a host name IT
                                   keeps in its DNS; asked in hi app new
  hi app domain [<domain>|off]     a domain verified in NetBird for the apps on this machine
  hi app domain <name> on|off      the app at <name>.<domain>, its preview at
                                   <name>-preview.<domain>; off by default
  hi app allow-ip <name>           the only addresses the app's Stage and production let in
      [<ip,cidr> | off]            (NetBird's Access Control; needs hi app domain's token);
                                   hi puts back exactly this list every minute, so add an
                                   address here, not in NetBird's dashboard
  hi app sso provider add <name> <issuer>  an OIDC provider for signing in (Google Workspace,
      [--client-id <id>] [--groups-claim <c>]  Entra ID, Keycloak, Auth0, Okta...); asks for the secret
  hi app sso <name> on <provider> people without the link sign in to the app, needing
      [--domain <d>] [--group <g>] [--anyone]  these rules; on a name that stays (hi app domain)
  hi app sso [<name> [off]]        the providers and apps; an app's rules and last sign-ins
  hi app manifest <name>           the Slack app manifest again
  hi app github <name> <owner/repo>  put the project on GitHub (a new private repository, or an
                                   empty one); the app pushes every merge there
  hi app gitlab <name> <group/project | host/group/project>
                                   the same on GitLab, gitlab.com or your own, with glab
  hi app jobs <name> [--stage]     the app's long work: each job's state, count, rate a day, last error
  hi app incidents <name> [<id>]   what went wrong while nobody looked, and what the lead did about it
                                   (--rules: the decisions the people made about the app's jobs)
  hi app upstream <name>           for an adopted project: fetch the project it came from, and say
                                   where dev stands against it and what's new there

An app is its folder. hi finds it by name through `+appsDir()+`.`)
}

// ---------------------------------------------------------------------------
// the wizard

// appSlackChecker checks tokens and the channel with Slack; tests replace
// it.
type appSlackChecker interface {
	botIdentity(botToken string) (workspace, botUser string, err error)
	// scopes are what the bot token was granted.
	scopes(botToken string) ([]string, error)
	checkAppToken(appToken string) error
	channel(botToken, channel string) (name string, member bool, err error)
}

var appSlack appSlackChecker = liveTeamSlack{}

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
	// A 429 or a 5xx has no scopes header: Slack couldn't say, which isn't
	// every scope missing.
	header := response.Header.Get("X-OAuth-Scopes")
	if response.StatusCode != http.StatusOK || header == "" {
		return nil, fmt.Errorf("Slack answered %s without the token's scopes", response.Status)
	}
	return strings.Split(header, ","), nil
}

// appSlackScopes are what a team's Slack app asks for in its manifest.
// Not assistant:write, for Slack's thinking line: Slack grants it only to
// an app with its agent experience on, which a team's app leaves off; an
// app made before v2.6.8 may have it, and the lead still uses it.
var appSlackScopes = []string{
	"app_mentions:read", "channels:history", "channels:read", "chat:write", "commands", "files:read", "files:write",
	"groups:history", "groups:read", "im:history", "im:read", "im:write", "reactions:read", "reactions:write", "users:read",
	// Finding people by email: the same person in every app, and sign-in
	// by email (hi_people.md).
	"users:read.email",
}

// appMissingScopes are the scopes the team's app needs that the bot token
// lacks: an app made from another manifest, or one not reinstalled after
// its scopes changed. None when Slack can't be asked.
func appMissingScopes(botToken string) []string {
	granted, err := appSlack.scopes(botToken)
	if err != nil {
		return nil
	}
	has := map[string]bool{}
	for _, scope := range granted {
		has[strings.TrimSpace(scope)] = true
	}
	var missing []string
	for _, scope := range appSlackScopes {
		if !has[scope] {
			missing = append(missing, scope)
		}
	}
	return missing
}

// appScopesAdvice says how to give the app the scopes it lacks.
func appScopesAdvice(missing []string) string {
	advice := fmt.Sprintf("the Slack app lacks %s, so the lead can't read what people share or post everything it should. In Slack's app settings (api.slack.com/apps), add them under OAuth & Permissions → Bot Token Scopes, then Reinstall to Workspace", strings.Join(missing, ", "))
	var lost []string
	if containsString(missing, "commands") || containsString(missing, "im:write") {
		lost = append(lost, "/hi-<name>, which DMs owners their link to the dev page")
	}
	if containsString(missing, "im:history") || containsString(missing, "im:read") {
		lost = append(lost, "talking to the lead in a DM")
	}
	if containsString(missing, "users:read.email") && len(missing) == 1 {
		// A scope alone: no command or event to add.
		return "the Slack app lacks users:read.email, so hi can't find people by their email (one person across the apps, and signing in by email as the Slack account they are). In Slack's app settings (api.slack.com/apps), add it under OAuth & Permissions → Bot Token Scopes, then Reinstall to Workspace"
	}
	if len(lost) > 0 {
		// These need the manifest's command and events too, not just
		// scopes.
		advice = fmt.Sprintf("the Slack app lacks %s, so %s can't work. In Slack's app settings (api.slack.com/apps), open App Manifest, replace it with the one hi app manifest <name> prints, Save, then Reinstall to Workspace", strings.Join(missing, ", "), strings.Join(lost, ", and "))
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

// appAnswers is what --from reads: app.json's fields, and a repository
// to clone instead of a new one.
type appAnswers struct {
	appConfig
	Clone  string `json:"clone,omitempty"`
	GitHub string `json:"github,omitempty"` // owner/name of a new private repository
	// GitLab is group/name on gitlab.com, or host/group/name on a GitLab
	// of one's own: a new private project there instead (app_gitlab.go).
	GitLab string `json:"gitlab,omitempty"`
	// Empty starts a new repository with only a README and the team's rules,
	// instead of a web app on Bun.
	Empty bool `json:"empty,omitempty"`
	// Adopt is a project someone else wrote to adopt (hi app adopt), from
	// Ref; Shallow fetches only its latest commit, and NoSurvey skips the
	// first task. PushToSource makes a clone or an adoption push its
	// merges back to where it came from, as a project of your own does.
	Adopt   string `json:"adopt,omitempty"`
	Ref     string `json:"ref,omitempty"`
	Shallow bool   `json:"shallow,omitempty"`
	// Full fetches the whole history of a project over 1 GB, which the
	// terminal asks about.
	Full         bool `json:"full,omitempty"`
	NoSurvey     bool `json:"no_survey,omitempty"`
	PushToSource bool `json:"push_to_source,omitempty"`
	// NetBirdKey is the setup key the dev box joins the team's NetBird
	// network with, once (app_dev.go).
	NetBirdKey string `json:"netbird_key,omitempty"`
	// HeartbeatURL is the team's heartbeat, kept in its secrets, and
	// MachineHeartbeatURL this machine's, when it has none yet
	// (app_heartbeat.go).
	HeartbeatURL        string `json:"heartbeat_url,omitempty"`
	MachineHeartbeatURL string `json:"machine_heartbeat_url,omitempty"`
	// adoption is the adoption the wizard or the answers prepared.
	adoption *appAdoption
	// checked: the wizard checked Slack as it went.
	checked bool
}

func appNew(args []string, stdin io.Reader, stdout io.Writer) error {
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
			return usageError{"usage: hi app new <name> [--dir <folder>] [--from <file.json>]"}
		}
	}
	if name == "" && from == "" && appStyled(stdin, stdout) {
		// The team's name is asked first: its folder is named after it.
		appSetupBanner(stdout, true, "", "")
		appSay(stdout, true, "A short name for the app, such as payments: lowercase letters, digits, and dashes.")
		var err error
		if name, err = appFieldAsk(stdin, stdout, "App name", false, func(answer string) error {
			if !appNamePattern.MatchString(answer) {
				return errors.New("an app's name starts with a letter and has lowercase letters, digits, and dashes, up to 20")
			}
			if _, err := loadTeam(answer); err == nil {
				return fmt.Errorf("app %s already exists in %s", answer, appDir(answer))
			}
			return nil
		}); err != nil {
			return err
		}
	}
	if name == "" {
		return usageError{"usage: hi app new <name> [--dir <folder>] [--from <file.json>]"}
	}
	return appCreate(name, folder, from, nil, stdin, stdout)
}

// appCreate sets up a team, from the wizard or answers in a file, with a
// project that's new, cloned, or adopted (hi app adopt).
func appCreate(name, folder, from string, adopt *appAdoption, stdin io.Reader, stdout io.Writer) error {
	if !appNamePattern.MatchString(name) {
		return fmt.Errorf("an app's name starts with a letter and has lowercase letters, digits, and dashes, up to 20: %q", name)
	}
	if name == "admin" {
		return errors.New("admin is the admin page's name (admin.<domain>, hi admin); pick another for the app")
	}
	if _, err := loadTeam(name); err == nil {
		return fmt.Errorf("app %s already exists in %s", name, appDir(name))
	}
	dir, err := filepath.Abs(firstNonEmpty(folder, "app-"+name))
	if err != nil {
		return err
	}
	if fileExists(dir) {
		return fmt.Errorf("%s already exists; choose another folder with --dir", dir)
	}
	if err := hermesInstalled(); err != nil {
		return err
	}

	var answers appAnswers
	var secrets appSecrets
	if from != "" {
		if answers, secrets, err = appAnswersFromFile(name, from); err != nil {
			return err
		}
		// A URL to clone that this machine can't push to is someone
		// else's project: adopted, unless the answers say to push there.
		if adopt == nil && answers.Adopt == "" && reviewIsURL(answers.Clone) {
			answers.Adopt, answers.Clone = answers.Clone, ""
		}
		adopt = appAdoptFromAnswers(adopt, answers)
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
		answers, secrets, err = appWizard(name, dir, adopt, stdin, stdout)
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
	config := answers.appConfig
	config.Name, config.Created = name, time.Now().UTC()
	if made := appFreeMadeAs(name); made != name {
		config.MadeAs = made
	}
	if config.Purpose == "" && answers.adoption != nil {
		config.Purpose = answers.adoption.summary.Description
	}
	if err := validateTeam(config); err != nil {
		return err
	}
	if secrets.OpenRouterAPIKey == "" {
		if key, err := readDotEnvValue(hermesEnvPath(), "OPENROUTER_API_KEY"); err != nil || key == "" {
			return errors.New(strings.ReplaceAll(appNoOpenRouterKey.Error(), "<name>", name))
		}
	}
	if !answers.checked {
		if err := appCheckSlack(config, secrets, stdout); err != nil {
			return err
		}
	}
	if answers.GitLab != "" {
		switch {
		case answers.GitHub != "":
			return errors.New("the project goes to GitHub or GitLab, not both")
		case answers.adoption != nil && answers.adoption.Shallow:
			return errors.New("a copy with only the latest commit can't go to a new GitLab project; leave out gitlab, or fetch the whole history")
		}
		if err := appCheckGitLab(answers.GitLab); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := appRegister(name, dir); err != nil {
		os.RemoveAll(dir)
		return err
	}
	if err := createTeamFolder(&config, secrets, answers.Clone, answers.Empty, answers.adoption, stdout); err != nil {
		os.RemoveAll(dir)
		os.Remove(filepath.Join(appsDir(), name))
		return err
	}
	if answers.GitHub != "" {
		if err := appConnectGitHub(&config, answers.GitHub, stdout); err != nil {
			fmt.Fprintf(stdout, "The app is set up, but not on GitHub: %v\nTry again with: hi app github %s %s\n", err, name, answers.GitHub)
		}
	}
	if answers.GitLab != "" {
		if err := appConnectGitLab(&config, answers.GitLab, stdout); err != nil {
			fmt.Fprintf(stdout, "The app is set up, but not on GitLab: %v\nTry again with: hi app gitlab %s %s\n", err, name, answers.GitLab)
		}
	}
	applyTeamHeartbeats(name, answers, stdout)
	if answers.NetBirdKey != "" {
		if err := setTeamDevNetBird(name, answers.NetBirdKey); err != nil {
			fmt.Fprintf(stdout, "The dev box isn't on NetBird: %v\nTry again with: hi app dev %s netbird\n", err, name)
		}
	}
	fmt.Fprintf(stdout, "\nTeam %s is ready in %s.\n", name, dir)
	for _, role := range appRoles(config) {
		member := config.Members[role]
		fmt.Fprintf(stdout, "  %-10s %s\n", role, appMemberLabel(member))
	}
	if config.Adopted != nil {
		fmt.Fprintf(stdout, "\nIn the dev box, ask your agent to survey %s first: it makes it run and maps it (the hi-dev-survey skill).\n", config.Adopted.Name)
	}
	fmt.Fprintf(stdout, "\nStart it with:\n  hi app up %s\n", name)
	return nil
}

// appAskProvider asks who pays for a Hermes member: OpenRouter, or the
// team's ChatGPT plan. On ChatGPT it signs the team in (once, for every
// role on it), offers the account's models, and checks the fallback, the
// same model on OpenRouter.
func appAskProvider(stdin io.Reader, stdout io.Writer, member *appMember, secrets *appSecrets, signIn *string) error {
	provider, err := appChoose(stdin, stdout, "         provider", []appOption{
		{appProviderOpenRouter, "pay per call, with the app's OpenRouter key"},
		{appProviderChatGPT, "a ChatGPT plan, falling back to OpenRouter"},
	}, appProviderOpenRouter)
	if err != nil {
		return err
	}
	if firstNonEmpty(provider, appProviderOpenRouter) == appProviderOpenRouter {
		member.Provider, member.Fallback = "", ""
		return nil
	}
	member.Provider = appProviderChatGPT
	if err := appAskChatGPTSignIn(stdin, stdout, secrets, signIn); err != nil {
		return err
	}
	grant := appChatGPTGrant{}
	if secrets.ChatGPT != nil {
		grant = *secrets.ChatGPT
	} else if grant, err = readCodexChatGPT(); err != nil {
		return err
	}
	models, err := chatGPTModels(grant)
	if err != nil {
		return err
	}
	return appAskChatGPTModel(stdin, stdout, member, models)
}

// appAskCodex signs the team in to ChatGPT for a Codex member, unless a
// role on it did already, and shows what it falls back to on OpenRouter.
// A machine whose Codex is on an API key keeps it, as before.
func appAskCodex(stdin io.Reader, stdout io.Writer, member *appMember, secrets *appSecrets, signIn *string) error {
	if _, err := readCodexChatGPT(); err != nil && *signIn == "" && fileExists(codexAuthPath()) {
		appNote(stdin, stdout, "         Codex here is on an API key; the reviewer keeps it.")
		return nil
	}
	if err := appAskChatGPTSignIn(stdin, stdout, secrets, signIn); err != nil {
		return err
	}
	model := firstNonEmpty(member.Model, defaultAgentModel("codex"))
	if model == "" {
		return nil
	}
	return appAskFallback(stdin, stdout, member, appFallbackModel(appMember{Agent: "codex", Model: model}))
}

// appAskChatGPTSignIn asks, once for the whole team, whose ChatGPT sign-in
// its roles on ChatGPT use, and signs the team in when it's its own.
func appAskChatGPTSignIn(stdin io.Reader, stdout io.Writer, secrets *appSecrets, signIn *string) error {
	if *signIn == "" {
		appNote(stdin, stdout, "         The app signs in to ChatGPT once, for every role on it: app signs in with a code on any device")
		appNote(stdin, stdout, "         (only the app's server refreshes it); machine uses this machine's, Codex's, which Codex refreshes.")
		answer, err := appChoose(stdin, stdout, "         ChatGPT sign-in", []appOption{
			{"app", "the app's own, with a code on any device"},
			{"machine", "this machine's, Codex's"},
		}, "app")
		if err != nil {
			return err
		}
		if answer == "machine" {
			if _, err := readCodexChatGPT(); err != nil {
				return err
			}
		}
		*signIn = answer
		if *signIn == "app" {
			grant, err := appChatGPTSignIn(stdout)
			if err != nil {
				return err
			}
			secrets.ChatGPT = &grant
			appNote(stdin, stdout, fmt.Sprintf("         Signed in: %s.", chatGPTPlanName(grant)))
		}
	}
	return nil
}

// appAskChatGPTModel asks a Hermes member's model from the plan's, and
// checks its fallback.
func appAskChatGPTModel(stdin io.Reader, stdout io.Writer, member *appMember, models []string) error {
	offered := member.Model
	if !containsString(models, offered) && len(models) > 0 {
		offered = models[0]
	}
	if len(models) > 0 && !appStyled(stdin, stdout) {
		appNote(stdin, stdout, fmt.Sprintf("         The plan offers %s.", strings.Join(models, ", ")))
	}
	var model string
	var err error
	if len(models) > 0 {
		var options []appOption
		for _, name := range models {
			options = append(options, appOption{value: name})
		}
		model, err = appChoose(stdin, stdout, "         model", options, offered)
	} else {
		model, err = appAskUntil(stdin, stdout, "         model", false, func(answer string) error {
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
	return appAskFallback(stdin, stdout, member, appFallbackModel(*member))
}

// appAskFallback checks a member's usual fallback is on OpenRouter, and
// asks for one that is when it isn't.
func appAskFallback(stdin io.Reader, stdout io.Writer, member *appMember, usual string) error {
	fallback := usual
	for {
		has, err := appOpenRouterHasModel(fallback)
		if err != nil {
			return err
		}
		if has {
			break
		}
		appNote(stdin, stdout, fmt.Sprintf("         OpenRouter has no %s to fall back on.", fallback))
		if fallback, err = appAskUntil(stdin, stdout, "         fallback on OpenRouter", false, func(answer string) error {
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
	appNote(stdin, stdout, fmt.Sprintf("         Fallback: %s on OpenRouter.", fallback))
	return nil
}

// appAskClaude asks, once, for the team's own Claude token (Enter: this
// machine's sign-in), and shows what a Claude member falls back to on
// OpenRouter, asking for one when OpenRouter has none.
func appAskClaude(stdin io.Reader, stdout io.Writer, member *appMember, secrets *appSecrets, asked *bool) error {
	if !*asked {
		*asked = true
		appNote(stdin, stdout, "         Claude: a token of the app's own, from claude setup-token signed in to the account that pays")
		appNote(stdin, stdout, "         (it lasts a year), or Enter for this machine's sign-in.")
		var token string
		check := func(answer string) error {
			if answer == "" {
				_, err := boxClaudeSecret()
				return err
			}
			return appCheckClaudeToken(answer)
		}
		if appStyled(stdin, stdout) {
			answer, err := appFieldAsk(stdin, stdout, "Claude token [Enter: this machine's]", true, check)
			if err != nil {
				return err
			}
			token = answer
		} else {
			for attempt := 1; ; attempt++ {
				answer, err := appReadSecret(stdin, stdout, "         Claude token [Enter: this machine's]")
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
		fallback, err := appClaudeFallback(*member)
		if err != nil {
			return err
		}
		has := false
		if fallback != "" {
			if has, err = appOpenRouterHasModel(fallback); err != nil {
				return err
			}
		}
		if has {
			appNote(stdin, stdout, fmt.Sprintf("         Fallback: %s on OpenRouter.", fallback))
			return nil
		}
		appNote(stdin, stdout, fmt.Sprintf("         OpenRouter has no fallback for %s.", firstNonEmpty(member.Model, "opus")))
		answer, err := appAskUntil(stdin, stdout, "         fallback on OpenRouter", false, func(answer string) error {
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

func appAnswersFromFile(name, path string) (appAnswers, appSecrets, error) {
	var answers appAnswers
	data, err := os.ReadFile(path)
	if err != nil {
		return answers, appSecrets{}, err
	}
	if err := json.Unmarshal(data, &answers); err != nil {
		return answers, appSecrets{}, fmt.Errorf("%s: %w", path, err)
	}
	if answers.Members == nil {
		answers.Members = defaultTeamMembers()
	}
	secrets := appSecrets{SlackBotToken: os.Getenv("HI_APP_SLACK_BOT_TOKEN"), SlackAppToken: os.Getenv("HI_APP_SLACK_APP_TOKEN"),
		OpenRouterAPIKey: os.Getenv("HI_APP_OPENROUTER_API_KEY")}
	if secrets.SlackBotToken == "" || secrets.SlackAppToken == "" {
		return answers, secrets, errors.New("set HI_APP_SLACK_BOT_TOKEN and HI_APP_SLACK_APP_TOKEN to the Slack app's tokens")
	}
	if token := os.Getenv("HI_APP_CLAUDE_TOKEN"); token != "" {
		if err := appCheckClaudeToken(token); err != nil {
			return answers, secrets, err
		}
		secrets.ClaudeToken, secrets.ClaudeSignedIn = token, time.Now().UTC()
	}
	if appUsesChatGPT(answers.appConfig) {
		// The team's own ChatGPT sign-in from a file, else this machine's.
		if path := os.Getenv("HI_APP_CHATGPT_AUTH"); path != "" {
			grant, err := readChatGPTAuthFile(path)
			if err != nil {
				return answers, secrets, err
			}
			secrets.ChatGPT = &grant
		} else if _, err := readCodexChatGPT(); err != nil {
			return answers, secrets, fmt.Errorf("a member is on ChatGPT: set HI_APP_CHATGPT_AUTH to a ChatGPT auth.json, or %w", err)
		}
	}
	return answers, secrets, nil
}

func appWizard(name, dir string, adopt *appAdoption, stdin io.Reader, stdout io.Writer) (appAnswers, appSecrets, error) {
	var answers appAnswers
	var secrets appSecrets
	config := &answers.appConfig
	config.Name = name
	config.Members = defaultTeamMembers()
	styled := appStyled(stdin, stdout)
	var err error

	appSetupBanner(stdout, styled, name, dir)

	appStep(stdout, styled, "1/10 Lead")
	appSay(stdout, styled, "The lead talks with the people in the app's Slack channel. Give it a name of its own, such as a",
		"person's, or press Enter to call it "+name+".")
	leadName, err := appAskUntil(stdin, stdout, "Lead's name ["+name+"]", false, appCheckLeadName)
	if err != nil {
		return answers, secrets, err
	}
	if leadName != "" && leadName != name {
		lead := config.Members["lead"]
		lead.Name = leadName
		config.Members["lead"] = lead
	}
	handle := appBotHandle(*config)
	appSay(stdout, styled, "In Slack it's @"+handle+".")

	appStep(stdout, styled, "2/10 Purpose")
	appSay(stdout, styled, "In one sentence, what is this app for? The lead starts from it.")
	offered := ""
	if adopt != nil {
		offered = adopt.summary.Description
	}
	if config.Purpose, err = appAsk(stdin, stdout, "Purpose", offered); err != nil {
		return answers, secrets, err
	}

	appStep(stdout, styled, "3/10 Slack app")
	appSay(stdout, styled, "Each app has a Slack app of its own. Create it from this manifest:",
		"  api.slack.com/apps → Create New App → From a manifest → YAML → paste it → Create.")
	if err := appShowManifest(stdin, stdout, styled, *config); err != nil {
		return answers, secrets, err
	}
	appSay(stdout, styled, "The app token is on the page Slack opens, Basic Information → App-Level Tokens:",
		"generate one with the connections:write scope (xapp-…).")
	if secrets.SlackAppToken, err = appAskUntil(stdin, stdout, "App token", true, appCheckAppToken); err != nil {
		return answers, secrets, err
	}
	appSay(stdout, styled, "Then Install to Workspace; the bot token is under OAuth & Permissions (xoxb-…).")
	workspace := ""
	if secrets.SlackBotToken, err = appAskUntil(stdin, stdout, "Bot token", true, func(token string) error {
		workspace, err = appCheckBotToken(token)
		return err
	}); err != nil {
		return answers, secrets, err
	}
	appSay(stdout, styled, fmt.Sprintf("Slack: both tokens work, in workspace %s.", workspace))

	appStep(stdout, styled, "4/10 Channel")
	appSay(stdout, styled, "Invite the Slack app to the app's channel first: /invite @"+handle,
		"Then copy the channel's ID: click the channel's name; the ID is at the bottom (C…).")
	channel := ""
	if config.Channel, err = appAskUntil(stdin, stdout, "Channel ID", false, func(id string) error {
		channel, err = appCheckChannel(handle, secrets.SlackBotToken, id)
		return err
	}); err != nil {
		return answers, secrets, err
	}
	appSay(stdout, styled, fmt.Sprintf("Slack: the app is in #%s.", channel))
	appSay(stdout, styled, "A second channel for operations and security, optional: incidents, warnings, downtime,",
		"advisories, keys, and access, for the people with the know-how. A private one, for owners and Pro Devs.",
		"Without it, all of that goes to the main channel. Invite the app there too.")
	if config.OpsChannel, err = appAskUntil(stdin, stdout, "Ops channel ID [Enter: none]", false, func(id string) error {
		if id == "" {
			return nil
		}
		if id == config.Channel {
			return errors.New("that's the main channel; the ops channel is a second one")
		}
		channel, err = appCheckChannel(handle, secrets.SlackBotToken, id)
		return err
	}); err != nil {
		return answers, secrets, err
	}
	if config.OpsChannel != "" {
		appSay(stdout, styled, fmt.Sprintf("Slack: the app is in #%s, for operations.", channel))
	}

	appStep(stdout, styled, "5/10 People")
	appSay(stdout, styled, "Owners build the app in the dev box and release it from the dev page. Give Slack member IDs,",
		"separated by commas: a person's profile → ⋯ → Copy member ID (U…).")
	owners, err := appAskUntil(stdin, stdout, "Owners", false, appCheckPeople)
	if err != nil {
		return answers, secrets, err
	}
	config.Owners = splitList(owners)

	appStep(stdout, styled, "6/10 Members")
	if styled {
		appSay(stdout, styled, "Each role's agent and model, and for Hermes who pays: OpenRouter, or a ChatGPT plan (falling",
			"back to OpenRouter), and how hard it thinks. Enter keeps what's offered.")
	} else {
		appSay(stdout, styled, "Each role's agent (claude, codex, or hermes) and model, and for Hermes who pays: OpenRouter, or a",
			"ChatGPT plan (falling back to OpenRouter), and how hard it thinks. Enter keeps what's offered.")
	}
	chatGPTSignIn := "" // app or machine, once asked
	claudeAsked := false
	for _, role := range appRoles(*config) {
		member := config.Members[role]
		if styled {
			err = appAskMemberStyled(stdin, stdout, *config, role, &member)
		} else {
			err = appAskMemberLine(stdin, stdout, *config, role, &member)
		}
		if err != nil {
			return answers, secrets, err
		}
		if member.Agent == "hermes" {
			if err := appAskProvider(stdin, stdout, &member, &secrets, &chatGPTSignIn); err != nil {
				return answers, secrets, err
			}
		}
		if member.Agent == "claude" {
			if err := appAskClaude(stdin, stdout, &member, &secrets, &claudeAsked); err != nil {
				return answers, secrets, err
			}
		}
		if member.Agent == "codex" {
			if err := appAskCodex(stdin, stdout, &member, &secrets, &chatGPTSignIn); err != nil {
				return answers, secrets, err
			}
		}
		var efforts []appOption
		for _, effort := range agentEfforts {
			efforts = append(efforts, appOption{value: effort})
		}
		effort, err := appChoose(stdin, stdout, "         effort", efforts, appDefaultEffort)
		if err != nil {
			return answers, secrets, err
		}
		if member.Effort = effort; effort == appDefaultEffort {
			member.Effort = ""
		}
		config.Members[role] = member
	}

	appStep(stdout, styled, "7/10 Models")
	if appUsesChatGPT(*config) {
		appSay(stdout, styled, "OpenRouter is what the roles on ChatGPT fall back on, and the lead's memory and the app itself",
			"use it. Give the app a key of its")
	} else {
		appSay(stdout, styled, "The lead, and the app itself, use models through OpenRouter. Give the app a key of its")
	}
	appSay(stdout, styled, "own (openrouter.ai/settings/keys), so its use is billed and limited apart, or press Enter for this",
		"machine's (Hermes'). No box ever holds the key; nobody needs to paste one into Slack.")
	if secrets.OpenRouterAPIKey, err = appAskOpenRouterKey(stdin, stdout, "OpenRouter key [Enter: this machine's]"); err != nil {
		return answers, secrets, err
	}

	appStep(stdout, styled, "8/10 Code")
	appSay(stdout, styled, "The app keeps its own copy of the project in "+filepath.Join(dir, "repo")+".")
	if adopt != nil {
		if adopt.local != "" {
			appSay(stdout, styled, fmt.Sprintf("It's a copy of %s, without its history: nothing there changes.", adopt.local))
		} else {
			appSay(stdout, styled, fmt.Sprintf("It's %s, adopted: the app never pushes to it.", adopt.displayName()))
		}
		answers.adoption = adopt
	} else if err := appAskRepository(stdin, stdout, styled, dir, &answers); err != nil {
		return answers, secrets, err
	}
	adoption := answers.adoption
	switch {
	case adoption != nil && (adoption.GitHub != "" || adoption.Shallow):
		if adoption.Shallow {
			appSay(stdout, styled, "With only its latest commit, the copy stays on this machine.")
		}
	case adoption != nil:
		appSay(stdout, styled, "Keep the app's copy on GitHub too? Give owner/name, and hi creates a private repository with",
			"the project's history, with this machine's GitHub sign-in (gh); the app pushes every merge there.",
			"For GitLab, give gitlab.com/group/name, or your GitLab's host/group/name, with its sign-in (glab).")
	case answers.Clone == "":
		appSay(stdout, styled, "Put it on GitHub too? Give owner/name, such as octocat/world-clock, and hi creates a private",
			"repository with this machine's GitHub sign-in (gh); the app pushes every merge there.",
			"For GitLab, give gitlab.com/group/name, or your GitLab's host/group/name, with its sign-in (glab).")
	}
	if answers.Clone == "" && !(adoption != nil && (adoption.GitHub != "" || adoption.Shallow)) {
		var place string
		if place, err = appAskUntil(stdin, stdout, "GitHub or GitLab [Enter: none, it stays on this machine]", false, func(name string) error {
			switch {
			case name == "":
				return nil
			case appIsGitLabAnswer(name):
				return appCheckGitLab(name)
			}
			return appCheckGitHub(name)
		}); err != nil {
			return answers, secrets, err
		}
		if appIsGitLabAnswer(place) {
			answers.GitLab = place
		} else {
			answers.GitHub = place
		}
	}

	appStep(stdout, styled, "9/10 Network")
	appSay(stdout, styled, "The app's addresses are on your NetBird network, and the owners build in its dev box over SSH there.")
	if styled {
		appSay(stdout, styled, strings.Split(strings.TrimRight(appDevNetBirdHelp(name), "\n"), "\n")...)
	} else {
		fmt.Fprint(stdout, appDevNetBirdHelp(name))
	}
	key, err := appReadSecret(stdin, stdout, "NetBird setup key [Enter: later, with hi app dev "+name+" netbird]")
	if err != nil && !errors.Is(err, io.EOF) {
		return answers, secrets, err
	}
	answers.NetBirdKey = strings.TrimSpace(key)
	// Without a terminal, answers that end before it keep the default.
	if err := appAskAddresses(stdin, stdout, styled, config); err != nil && !errors.Is(err, io.EOF) {
		return answers, secrets, err
	}

	appStep(stdout, styled, "10/10 Heartbeats")
	if err := appAskHeartbeats(stdin, stdout, styled, &answers); err != nil && !errors.Is(err, io.EOF) {
		return answers, secrets, err
	}
	answers.checked = true
	return answers, secrets, nil
}

// appAskMemberLine asks a role's agent and model on one line, as
// agent:model.
func appAskMemberLine(stdin io.Reader, stdout io.Writer, config appConfig, role string, member *appMember) error {
	offered := member.Agent + appModelSuffix(member.Model)
	answer, err := appAskUntil(stdin, stdout, fmt.Sprintf("%-8s [%s]", role, offered), false, func(answer string) error {
		agent, model, _ := strings.Cut(firstNonEmpty(answer, offered), ":")
		return appCheckMember(config, role, appMember{Agent: strings.TrimSpace(agent), Model: strings.TrimSpace(model)})
	})
	if err != nil {
		return err
	}
	agent, model, _ := strings.Cut(firstNonEmpty(answer, offered), ":")
	member.Agent, member.Model = strings.TrimSpace(agent), strings.TrimSpace(model)
	return nil
}

// appAskMemberStyled asks a role's agent from a list, the lead's being
// Hermes, then its model, offering the agent's usual one.
func appAskMemberStyled(stdin io.Reader, stdout io.Writer, config appConfig, role string, member *appMember) error {
	agent := member.Agent
	if role != "lead" {
		var err error
		if agent, err = appChoose(stdin, stdout, role, []appOption{
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
	model, err := appAskUntil(stdin, stdout, question, false, func(answer string) error {
		return appCheckMember(config, role, appMember{Agent: agent, Model: firstNonEmpty(answer, offered)})
	})
	if err != nil {
		return err
	}
	member.Agent, member.Model = agent, firstNonEmpty(model, offered)
	return nil
}

// appCheckMember checks one role's agent and model, with the others'.
func appCheckMember(config appConfig, role string, member appMember) error {
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

// appAskRepository asks what the team's project starts from: a new web
// app, an empty repository, a team template, or someone's project.
func appAskRepository(stdin io.Reader, stdout io.Writer, styled bool, dir string, answers *appAnswers) error {
	var offered []*templateLayer
	if catalog, err := loadTemplateCatalog(io.Discard); err == nil {
		offered = appTemplates(catalog)
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
		adoption := &appAdoption{Source: answer}
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
		options := []appOption{
			{"new", "a new web app: a blank page on Bun, with a database, tests, and the helper's chat"},
			{"empty", "a repository with only a README"},
		}
		for _, template := range offered {
			options = append(options, appOption{firstNonEmpty(template.key, template.Name), template.Summary})
		}
		options = append(options, appOption{"adopt", "someone's project, such as github.com/owner/name, or a folder here"})
		if clone, err = appChoose(stdin, stdout, "Project", options, "new"); err != nil {
			return err
		}
		if clone == "adopt" {
			appSay(stdout, styled, "The app works on its own copy and never pushes to theirs. A folder on this machine is cloned,",
				"and its owner pulls from the app.")
			if clone, err = appAskUntil(stdin, stdout, "Project to adopt", false, func(answer string) error {
				if answer == "" {
					return errors.New("give github.com/owner/name, a URL, or a folder")
				}
				return check(answer)
			}); err != nil {
				return err
			}
		}
	} else {
		appSay(stdout, styled, "Enter starts a new web app there: a blank page on Bun, with a database and tests, and the",
			"helper's chat on every page. Type empty for a repository with only a README. Or give a project",
			"someone else wrote, such as github.com/owner/name, to adopt it: the app works on its own copy",
			"and never pushes to theirs. A folder on this machine is cloned, and its owner pulls from the app.")
		if len(offered) > 0 {
			fmt.Fprintln(stdout, "Or name an app template, a web app ready for a kind of work:")
			for _, template := range offered {
				fmt.Fprintf(stdout, "  %-20s %s\n", firstNonEmpty(template.key, template.Name), template.Summary)
			}
		}
		if clone, err = appAskUntil(stdin, stdout, "Repository [new]", false, check); err != nil {
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

// appAskUntil asks until check accepts the answer; Enter gives "". In a
// terminal a wrong answer is asked again; otherwise it ends the setup.
func appAskUntil(stdin io.Reader, stdout io.Writer, question string, secret bool, check func(string) error) (string, error) {
	if appStyled(stdin, stdout) {
		return appFieldAsk(stdin, stdout, question, secret, check)
	}
	for attempt := 1; ; attempt++ {
		var answer string
		var err error
		if secret {
			answer, err = appAskSecret(stdin, stdout, question)
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

func appCheckBotToken(token string) (string, error) {
	if !strings.HasPrefix(token, "xoxb-") {
		return "", errors.New("the bot token starts with xoxb-; it is under OAuth & Permissions")
	}
	workspace, _, err := appSlack.botIdentity(token)
	if err != nil {
		return "", fmt.Errorf("Slack refused the bot token: %w", err)
	}
	if missing := appMissingScopes(token); len(missing) > 0 {
		return "", fmt.Errorf("%s, and paste the bot token again", appScopesAdvice(missing))
	}
	return workspace, nil
}

func appCheckAppToken(token string) error {
	if !strings.HasPrefix(token, "xapp-") {
		return errors.New("the app token starts with xapp-; generate one under Basic Information → App-Level Tokens")
	}
	if err := appSlack.checkAppToken(token); err != nil {
		return fmt.Errorf("Slack refused the app token (it needs connections:write, and Socket Mode on): %w", err)
	}
	return nil
}

// appCheckChannel checks the app, @handle in Slack, is in the channel.
func appCheckChannel(handle, botToken, id string) (string, error) {
	if !slackIDPattern.MatchString(id) {
		return "", fmt.Errorf("a channel ID looks like C07ABC123, not %q", id)
	}
	channel, member, err := appSlack.channel(botToken, id)
	if err != nil {
		return "", fmt.Errorf("the app can't see channel %s (invite it with /invite @%s): %w", id, handle, err)
	}
	if !member {
		return "", fmt.Errorf("the app isn't in #%s yet; invite it there with /invite @%s", channel, handle)
	}
	return channel, nil
}

func appCheckPeople(answer string) error {
	ids := splitList(answer)
	if len(ids) == 0 {
		return errors.New("give at least one member ID")
	}
	for _, id := range ids {
		// Someone outside Slack, or not in the app's workspace, is their
		// email (hi_people.md).
		if appIsEmail(appPersonID(id)) {
			continue
		}
		if !slackIDPattern.MatchString(id) || (!strings.HasPrefix(id, "U") && !strings.HasPrefix(id, "W")) {
			return fmt.Errorf("a member ID looks like U07ABC123, or an email address, not %q", id)
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

func appModelSuffix(model string) string {
	if model == "" {
		return ""
	}
	return ":" + model
}

func appMemberLabel(member appMember) string {
	label := agentDisplayName(member.Agent)
	if member.Model != "" {
		label += ", " + member.Model
	}
	switch {
	case appProvider(member) == appProviderChatGPT && firstNonEmpty(member.Fallback, appFallbackModel(member)) == "":
		label += " on ChatGPT, falling back to OpenRouter"
	case appProvider(member) == appProviderChatGPT:
		label += " on ChatGPT, falling back to " + firstNonEmpty(member.Fallback, appFallbackModel(member)) + " on OpenRouter"
	case member.Agent == "claude" && member.Fallback != "":
		label += " on Claude, falling back to " + member.Fallback + " on OpenRouter"
	case member.Agent == "claude":
		label += " on Claude, falling back to OpenRouter"
	}
	if effort := appEffort(member); effort != appDefaultEffort {
		label += ", effort " + effort
	}
	if member.Bundle != "" {
		label += ", bundle " + member.Bundle
	}
	return label
}

// appDefaultEffort is how hard a member thinks unless app.json says.
const appDefaultEffort = "medium"

func appEffort(member appMember) string { return firstNonEmpty(member.Effort, appDefaultEffort) }

// appRoles lists the lead first, then the others by name.
func appRoles(config appConfig) []string {
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

// appAsk asks one question; Enter takes the fallback.
func appAsk(stdin io.Reader, stdout io.Writer, question, fallback string) (string, error) {
	if appStyled(stdin, stdout) {
		hint := ""
		if fallback != "" {
			hint = " [" + fallback + "]"
		}
		answer, err := appFieldAsk(stdin, stdout, question+hint, false, func(answer string) error {
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
	return appAsk(stdin, stdout, question, fallback)
}

// appAskSecret reads a token without showing it.
func appAskSecret(stdin io.Reader, stdout io.Writer, label string) (string, error) {
	value, err := appReadSecret(stdin, stdout, label)
	if err == nil && value == "" {
		err = fmt.Errorf("no %s was entered", strings.ToLower(label))
	}
	return value, err
}

// appReadSecret reads a secret without showing it; Enter gives "".
func appReadSecret(stdin io.Reader, stdout io.Writer, label string) (string, error) {
	if appStyled(stdin, stdout) {
		return appFieldAsk(stdin, stdout, label, true, func(string) error { return nil })
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

// appCheckSlack checks both tokens and that the bot is in the channel.
func appCheckSlack(config appConfig, secrets appSecrets, stdout io.Writer) error {
	workspace, err := appCheckBotToken(secrets.SlackBotToken)
	if err != nil {
		return err
	}
	if err := appCheckAppToken(secrets.SlackAppToken); err != nil {
		return err
	}
	channel, err := appCheckChannel(appBotHandle(config), secrets.SlackBotToken, config.Channel)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Slack: workspace %s, channel #%s.\n", workspace, channel)
	if config.OpsChannel != "" {
		ops, err := appCheckChannel(appBotHandle(config), secrets.SlackBotToken, config.OpsChannel)
		if err != nil {
			return fmt.Errorf("the ops channel: %w", err)
		}
		fmt.Fprintf(stdout, "Slack: ops channel #%s.\n", ops)
	}
	return nil
}

// createTeamFolder makes the folder, the repository, and the lead's first
// files.
func createTeamFolder(config *appConfig, secrets appSecrets, clone string, empty bool, adopt *appAdoption, stdout io.Writer) error {
	dir := appDir(config.Name)
	// A new team's addresses are internal only (hi_app_internal.md).
	if config.Public == nil {
		config.Public = &appPublic{}
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
		// A project on a GitLab this machine's glab is signed in to is
		// fetched, and pushed to, through glab, set in the app's
		// repository only, as for a new GitLab project.
		args := []string{"clone", "-q", clone, repo}
		gitlabHost, _, gitlab := appGitLabSource(clone)
		if gitlab {
			key, value := appGitLabCredential(gitlabHost)
			args = append([]string{"-c", key + "=" + value}, args...)
		}
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			return fmt.Errorf("git clone: %s", qFirstLine(string(out), err.Error()))
		}
		if gitlab {
			key, value := appGitLabCredential(gitlabHost)
			if out, err := appGit(repo, "config", key, value); err != nil {
				return fmt.Errorf("git config: %s", out)
			}
		}
		// The team pushes merges back to a URL. A folder on this machine is
		// usually someone's working copy, which git won't push into, so its
		// owner pulls from the team instead.
		if info, err := os.Stat(clone); err == nil && info.IsDir() {
			fmt.Fprintf(stdout, "The app works on its own copy; get its work with: git pull %s %s\n", repo, firstNonEmpty(boxGit(repo, "branch", "--show-current"), "main"))
		} else {
			config.Remote = clone
		}
		if boxGit(repo, "rev-parse", "--verify", "-q", "HEAD") == "" {
			return errors.New("the repository has no commits yet; push a first commit, or let the app start a new one")
		}
	} else if err := newTeamRepo(*config, repo, empty); err != nil {
		return err
	}
	// A cloned or empty project still takes a template's roles.
	if config.Template != "" && (clone != "" || empty) {
		if _, _, err := appComposeTemplate(*config); err != nil {
			return err
		}
	}
	config.Branch = firstNonEmpty(boxGit(repo, "branch", "--show-current"), "main")
	if adopt != nil {
		if err := appSetUpAdopted(config, adopt, repo, stdout); err != nil {
			return err
		}
	}
	if err := saveTeam(*config); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "slack-manifest.json"), []byte(appManifest(*config)+"\n"), 0o600); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "lead", "SOUL.md"), []byte(appSoul(*config)), 0o600)
}

// appComposeTemplate composes the team's template, team-app or one that
// extends it, and keeps its roles in the team's folder.
func appComposeTemplate(config appConfig) (*composedTemplate, *templateCatalog, error) {
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
	name := firstNonEmpty(config.Template, hiAppTemplate)
	if config.Template != "" && !appIsTemplate(catalog, name) {
		return nil, nil, fmt.Errorf("%q isn't an app template: one that extends %s (hi init --list shows the templates)", name, hiAppTemplate)
	}
	composed, err := composeTemplateAllowingHidden(catalog.layers, name, map[string]string{"name": config.Name})
	if err != nil {
		return nil, nil, err
	}
	return composed, catalog, nil
}

// appTemplates are the templates a team can start from: those that
// extend team-app, by name.
func appTemplates(catalog *templateCatalog) []*templateLayer {
	var templates []*templateLayer
	for _, key := range sortedKeys(catalog.layers) {
		if key != hiAppTemplate && appIsTemplate(catalog, key) {
			templates = append(templates, catalog.layers[key])
		}
	}
	return templates
}

// appIsTemplate says whether a template extends team-app.
func appIsTemplate(catalog *templateCatalog, name string) bool {
	layer, ok := catalog.layers[name]
	if !ok || (layer.Hidden && name != hiAppTemplate) {
		return false
	}
	chain, err := layerChain(catalog.layers, name)
	if err != nil {
		return false
	}
	for _, layer := range chain {
		if layer.Name == hiAppTemplate {
			return true
		}
	}
	return false
}

func newTeamRepo(config appConfig, repo string, empty bool) error {
	if err := os.MkdirAll(repo, 0o755); err != nil {
		return err
	}
	var entries []initEntry
	if empty {
		files := map[string]string{
			"README.md":                 "# " + config.Name + "\n\n" + config.Purpose + "\n",
			"AGENTS.md":                 appAgentsFile,
			appSpecsPath + "/README.md": appSpecsReadme(config),
		}
		for _, name := range sortedKeys(files) {
			entries = append(entries, initEntry{path: name, data: []byte(files[name])})
		}
	} else {
		var err error
		if entries, err = hiAppEntries(config); err != nil {
			return err
		}
	}
	if err := writeInitFiles(repo, entries); err != nil {
		return err
	}
	author := []string{"-c", "user.name=" + firstNonEmpty(boxGit(".", "config", "user.name"), "hi app "+config.Name),
		"-c", "user.email=" + firstNonEmpty(boxGit(".", "config", "user.email"), "team-"+config.Name+"@hi.invalid")}
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"add", "-A"}, append(author, "commit", "-q", "-m", "Start the "+config.Name+" app's project")} {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			return fmt.Errorf("git %s: %s", args[0], qFirstLine(string(out), err.Error()))
		}
	}
	return nil
}

// hiAppTemplate is what a new team's project starts from: the app
// template with a blank page, since nearly every team builds a web app.
// The helper's chat box needs nothing from it: hi adds it to every page.
const hiAppTemplate = "hi-app"

// hiAppEntries are the files of a new team's project: the template's,
// with the team's purpose in README.md, the team's rules in AGENTS.md, and
// the lead's specs folder. The template's skills are left out: they are
// about hi, which the members' boxes don't have. Its team/ folder gives
// the team's roles.
func hiAppEntries(config appConfig) ([]initEntry, error) {
	composed, catalog, err := appComposeTemplate(config)
	if err != nil {
		return nil, err
	}
	composed.Skills = nil
	readme := string(composed.Files["README.md"])
	_, rest, _ := strings.Cut(readme, "\n\n")
	composed.Files["README.md"] = []byte("# " + titleName(config.Name) + "\n\n" + config.Purpose + "\n\n" + rest)
	composed.Files["AGENTS.md"] = append(composed.Files["AGENTS.md"], "\n"+appAgentsFile...)
	composed.Files[appSpecsPath+"/README.md"] = []byte(appSpecsReadme(config))
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

const appAgentsFile = `# Working on this project

This project is a hi app: its owners build it with coding agents in the
app's dev box, for the people in its Slack channel. The people may not
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
  hi can put it on the app's preview.
- Commit your work on your branch with a message that says what changed and
  why. There is no push: hi dev ship merges the branch into dev, and an
  owner or a developer releases dev to production.
- Keep README.md current: what the project does, how to run it, how to
  test it.
`

// appBotName is the bot's handle in Slack: the team's name, so people
// write @payments. Apps made before v0.48.0 are named team-<name>.
func appBotName(name string) string { return name }

// ---------------------------------------------------------------------------
// up, down, ls, status, logs

func appUnit(config appConfig, executable string) string {
	env := ""
	if dir := os.Getenv("HI_APPS_DIR"); dir != "" {
		env += "Environment=HI_APPS_DIR=" + dir + "\n"
	}
	// The agents, git, and podman are found as in the shell that ran hi app up.
	env += "Environment=PATH=" + os.Getenv("PATH") + "\n"
	return fmt.Sprintf(`[Unit]
Description=hi app %[1]s
After=network-online.target
Wants=network-online.target

[Service]
%[2]sExecStart=%[3]s app __serve %[1]s
Restart=always
RestartSec=10
TimeoutStopSec=60
# Only hi app __serve gets the stop signal; it stops the lead itself.
KillMode=mixed

[Install]
WantedBy=default.target
`, config.Name, env, executable)
}

// appSystemctl runs systemctl --user; tests replace it.
var appSystemctl = func(args ...string) (string, error) {
	out, err := exec.Command("systemctl", append([]string{"--user"}, args...)...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func appUnitPath(name string) string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "systemd", "user", appUnitName(name))
}

func appUp(name string, stdout io.Writer) error {
	config, err := loadTeam(name)
	if err != nil {
		return err
	}
	if err := appMovedError(name, appDir(name)); err != nil {
		return err
	}
	if err := hermesInstalled(); err != nil {
		return err
	}
	if err := appHasOpenRouterKey(name); err != nil {
		return err
	}
	if err := appMigrate(name, stdout); err != nil {
		return err
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	if resolved, err := filepath.EvalSymlinks(executable); err == nil {
		executable = resolved
	}
	unit := appUnitPath(name)
	if err := os.MkdirAll(filepath.Dir(unit), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(unit, []byte(appUnit(config, executable)), 0o644); err != nil {
		return err
	}
	if err := appInstallSnapshotTimer(config, executable); err != nil {
		return err
	}
	for _, args := range [][]string{{"daemon-reload"}, {"enable", appUnitName(name)}, {"restart", appUnitName(name)}, {"enable", "--now", appSnapshotUnitName(name) + ".timer"}} {
		if out, err := appSystemctl(args...); err != nil {
			return fmt.Errorf("systemctl --user %s: %s", strings.Join(args, " "), qFirstLine(out, err.Error()))
		}
	}
	fmt.Fprintf(stdout, "App %s is starting. It answers in its Slack channel in a minute or so.\n", name)
	if secrets, err := loadTeamSecrets(name); err == nil {
		if missing := appMissingScopes(secrets.SlackBotToken); len(missing) > 0 {
			fmt.Fprintf(stdout, "\nWarning: %s. The bot token stays the same.\n\n", appScopesAdvice(missing))
		}
	}
	if secrets, err := loadTeamSecrets(name); err == nil && secrets.HeartbeatURL == "" {
		fmt.Fprintf(stdout, "\nWarning: the app sends no heartbeat, so nobody would hear that it stopped: hi app heartbeat %s <url>.\n", name)
	}
	if warning := boxRootGroupsWarning(); warning != "" {
		fmt.Fprintf(stdout, "\nWarning: %s\n", warning)
	}
	if !appResticInstalled() {
		fmt.Fprintf(stdout, "\nWarning: restic isn't installed, so the app can't take its daily snapshots. hi install restic adds it.\n\n")
	}
	fmt.Fprintf(stdout, "  hi app status %s   whether it is up\n  hi app logs %s     the lead's log\n", name, name)
	if !appLingers() {
		fmt.Fprintf(stdout, "\nIt stops when you log out, and doesn't start at boot, until you run:\n  sudo loginctl enable-linger %s\n", currentUserName())
	}
	return nil
}

// appLingers reports whether this user's services run without a login.
var appLingers = func() bool {
	out, err := exec.Command("loginctl", "show-user", currentUserName(), "--property=Linger", "--value").Output()
	return err == nil && strings.TrimSpace(string(out)) == "yes"
}

// appAdopt registers the team in a folder under its name: a team that
// was moved, or copied from another machine.
func appAdopt(folder string, stdout io.Writer) (string, error) {
	dir, err := filepath.Abs(folder)
	if err != nil {
		return "", err
	}
	migrateAppConfig(dir)
	data, err := os.ReadFile(filepath.Join(dir, appConfigFile))
	if err != nil {
		return "", fmt.Errorf("%s isn't an app's folder: %w", dir, err)
	}
	var config appConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return "", fmt.Errorf("app.json: %w", err)
	}
	if !appNamePattern.MatchString(config.Name) {
		return "", fmt.Errorf("app.json has no valid name: %q", config.Name)
	}
	if err := appMovedError(config.Name, dir); err != nil {
		return "", err
	}
	if current := appDir(config.Name); current != dir && fileExists(filepath.Join(current, appConfigFile)) {
		return "", fmt.Errorf("another app named %s is in %s", config.Name, current)
	}
	if err := appRegister(config.Name, dir); err != nil {
		return "", err
	}
	fmt.Fprintf(stdout, "App %s is in %s.\n", config.Name, dir)
	return config.Name, nil
}

func appDown(name string, stdout io.Writer) error {
	if _, err := loadTeam(name); err != nil {
		return err
	}
	if appLegacy(name) {
		appStopLegacy(name)
	}
	if out, err := appSystemctl("disable", "--now", appUnitName(name)); err != nil && fileExists(appUnitPath(name)) {
		return fmt.Errorf("systemctl --user disable --now %s: %s", appUnitName(name), qFirstLine(out, err.Error()))
	}
	appSystemctl("disable", "--now", appSnapshotUnitName(name)+".timer")
	// The dev box keeps running when the service restarts, not when the
	// team stops.
	appStopDevBox(name)
	// The org server stops watching for its heartbeat until hi app up.
	(&appBroker{team: name}).sendHeartbeat("down")
	if config, err := loadTeam(name); err == nil {
		appHeartbeatDown(config, stdout)
	}
	fmt.Fprintf(stdout, "App %s is stopped. Its folder stays; hi app up %s starts it again.\n", name, name)
	return nil
}

// appRebuild builds hi's base image again, with Ubuntu's current
// packages, and restarts the team on it: the lead now, the preview and
// production within minutes, on the same addresses (keepLinks). The dev
// box keeps the owners' agents running until hi app dev restart. The
// image is the machine's, so it names the other teams still on the old
// one.
func appRebuild(name string, stdout, stderr io.Writer) error {
	if _, err := loadTeam(name); err != nil {
		return err
	}
	if err := appRebuildImage(stdout, stderr); err != nil {
		return err
	}
	if err := appUp(name, stdout); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "\nIts preview and production move to the new image in a few minutes, on the same addresses. The dev box moves when it restarts: hi app dev %s restart (agents working there stop; herdr brings their conversations back).\n", name)
	var others []string
	entries, _ := os.ReadDir(appsDir())
	for _, entry := range entries {
		if entry.Name() != name && appNamePattern.MatchString(entry.Name()) && appState(entry.Name()) == "up" {
			others = append(others, entry.Name())
		}
	}
	if len(others) > 0 {
		fmt.Fprintf(stdout, "The image is this machine's, so %s move to it too, by themselves in a few minutes; their dev boxes move when they restart.\n", strings.Join(others, ", "))
	}
	return nil
}

// appRebuildImage builds hi's base image again; tests replace it.
var appRebuildImage = func(stdout, stderr io.Writer) error {
	engine, _, err := detectBoxEngine()
	if err != nil {
		return err
	}
	_, err = engine.rebuildBaseImage(stdout, stderr)
	return err
}

// appStopDevBox stops a team's dev box; tests replace it.
var appStopDevBox = func(name string) {
	if engine, _, err := detectBoxEngine(); err == nil {
		stopTeamDev(engine, name)
		stopTeamPeers(engine, name)
	}
}

// appRemoveContainers removes a team's containers and its members'
// boxes; tests replace it.
var appRemoveContainers = func(name string, boxes []string) {
	engine, _, err := detectBoxEngine()
	if err != nil {
		return
	}
	for _, slot := range []string{appSlotPreview, appSlotProduction} {
		if state, ok := loadTeamSlot(name, slot); ok && state.Expose != "" {
			netExposeStop([]string{state.Expose}, io.Discard)
		}
	}
	if out, err := engine.output("ps", "-a", "--format", "{{.Names}}"); err == nil {
		for _, container := range strings.Fields(out) {
			// An older hi's one app box was hi-box-app-<name>-app, which is
			// also the lead of an app called <name>-app.
			legacy := container == appPreviewContainer(name) && !fileExists(filepath.Join(appsDir(), name+"-app"))
			if appSlotContainer(name, container) || legacy {
				engine.output("rm", "-f", container)
			}
		}
	}
	engine.output("rm", "-f", appMemoryContainer(name))
	stopTeamResolver(name)
	teardownBox(engine, boxMeta{Name: "app-" + name})
	stopTeamDev(engine, name)
	stopTeamPeers(engine, name)
	for _, box := range boxes {
		if meta, err := loadBoxMeta(box); err == nil {
			removeBox(engine, meta, true, io.Discard)
		}
	}
}

// appRemove takes a team off this machine: what hi made for it outside
// its folder (the service, the link by name, the boxes) and, unless
// --keep-folder, the folder. The Slack app, the channel, and a GitHub
// repository are not hi's to remove.
func appRemove(args []string, stdin io.Reader, stdout io.Writer) error {
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
			return usageError{"usage: hi app rm [<name>] [--keep-folder] [--yes]"}
		}
	}
	if name == "" {
		if !interactive(stdin, stdout) {
			return usageError{"usage: hi app rm <name> [--keep-folder] [--yes]"}
		}
		picked, err := appPick(stdin, stdout, "Remove an app")
		if errors.Is(err, errMenuBack) {
			return nil
		}
		if err != nil {
			return err
		}
		name = picked
	}
	if !appNamePattern.MatchString(name) {
		return fmt.Errorf("no app named %q", name)
	}
	link := filepath.Join(appsDir(), name)
	_, linkErr := os.Lstat(link)
	folder := ""
	if config, err := loadTeam(name); err == nil {
		folder = appDir(config.Name)
	} else if linkErr != nil && !fileExists(appUnitPath(name)) {
		return err
	}
	var boxes []string
	for _, task := range loadTeamTasks(name) {
		if task.Box != "" && fileExists(boxStateFile(task.Box)) {
			boxes = append(boxes, task.Box)
		}
	}

	fmt.Fprintf(stdout, "Removing app %s stops it and deletes:\n", name)
	fmt.Fprintf(stdout, "  its service       %s\n", appUnitPath(name))
	fmt.Fprintf(stdout, "  its name          %s\n", link)
	if len(boxes) > 0 {
		fmt.Fprintf(stdout, "  its members' boxes  %s\n", strings.Join(boxes, ", "))
	}
	if folder != "" && !keep {
		fmt.Fprintf(stdout, "  its folder        %s  (the project, its tasks, the channel's log, the lead's memory)\n", folder)
	}
	if err := confirm(stdin, stdout, yes, "Remove app "+name+"?"); err != nil {
		return err
	}
	if appLegacy(name) {
		appStopLegacy(name)
	}

	if out, err := appSystemctl("disable", "--now", appUnitName(name)); err != nil && fileExists(appUnitPath(name)) {
		return fmt.Errorf("systemctl --user disable --now %s: %s", appUnitName(name), qFirstLine(out, err.Error()))
	}
	if err := os.Remove(appUnitPath(name)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	appRemoveSnapshotTimer(name)
	appSystemctl("daemon-reload")
	appRemoveContainers(name, boxes)
	if err := os.Remove(link); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if folder != "" && !keep {
		if err := appRemoveAll(folder); err != nil {
			return fmt.Errorf("the app is off this machine, but not all of its folder is deleted: %w", err)
		}
	}
	fmt.Fprintf(stdout, "App %s is removed.", name)
	if folder != "" && keep {
		fmt.Fprintf(stdout, " Its folder stays in %s; hi app up %s brings the app back.", folder, folder)
	}
	if snapshots := appSnapshotsDir(name); fileExists(snapshots) {
		fmt.Fprintf(stdout, "\nIts snapshots stay in %s, with the key to them; delete that folder if you no longer want them.", snapshots)
	}
	fmt.Fprintln(stdout, "\nIts Slack app and channel, and a GitHub repository if it had one, are still there; remove them in Slack and GitHub if you no longer want them.")
	return nil
}

func appState(name string) string {
	unit := appUnitName(name)
	if appLegacy(name) && !fileExists(appUnitPath(name)) {
		unit = legacyAppUnit(name) // until hi app up moves it to the new names
	}
	out, _ := appSystemctl("is-active", unit)
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

// appLeadBoxes describes the leads' boxes that are running, for hi box
// ls: hi app starts them, not hi box.
func appLeadBoxes() []string {
	entries, err := os.ReadDir(appsDir())
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
		container := "hi-box-app-" + config.Name
		if engine.state(container) != "running" {
			continue
		}
		lines = append(lines, fmt.Sprintf("%-22s app %s's lead (Hermes), channel %s, folder %s", container, config.Name, config.Channel, appDir(config.Name)))
	}
	return lines
}

// appPick lets you choose one of the teams on this machine from a list
// that shows whether each is up.
func appPick(stdin io.Reader, stdout io.Writer, title string) (string, error) {
	entries, err := os.ReadDir(appsDir())
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
		options = append(options, fmt.Sprintf("%-20s %-6s %s", config.Name, appState(config.Name), config.Channel))
	}
	if len(names) == 0 {
		return "", errors.New("no apps yet. hi app new <name> sets one up")
	}
	choice, err := runListSelect(stdin, stdout, title, "", options, false)
	if err != nil {
		return "", err
	}
	return names[choice], nil
}

func appList(stdout io.Writer) error {
	entries, err := os.ReadDir(appsDir())
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	table := tabwriter.NewWriter(stdout, 0, 0, 3, ' ', 0)
	count := 0
	for _, entry := range entries {
		if !appNamePattern.MatchString(entry.Name()) {
			continue // settings.json, not a team
		}
		config, err := loadTeam(entry.Name())
		if err != nil {
			fmt.Fprintf(table, "%s\t%s\n", entry.Name(), err)
			continue
		}
		if count == 0 {
			fmt.Fprintln(table, "APP\tSTATE\tCHANNEL\tMEMBERS\tFOLDER")
		}
		count++
		fmt.Fprintf(table, "%s\t%s\t%s\t%s\t%s\n", config.Name, appState(config.Name), config.Channel, strings.Join(appRoles(config), ", "), appDir(config.Name))
	}
	table.Flush()
	if count == 0 {
		fmt.Fprintln(stdout, "No apps yet. hi app new <name> sets one up.")
	}
	return nil
}

func appStatus(name string, stdout io.Writer) error {
	config, err := loadTeam(name)
	if err != nil {
		return err
	}
	channels := "Channel " + config.Channel
	if config.OpsChannel != "" {
		channels += ", ops " + config.OpsChannel
	}
	fmt.Fprintf(stdout, "App %s: %s. %s. %s\n%s\n\n", name, appState(name), channels, config.Purpose, appHeartbeatLine(config))
	for _, role := range appRoles(config) {
		fmt.Fprintf(stdout, "  %-10s %s\n", role, appMemberLabel(config.Members[role]))
	}
	if view, err := appVersionsReport(name); err == nil {
		fmt.Fprintln(stdout)
		printTeamVersions(view, stdout)
	}
	if line := appSnapshotLine(name); line != "" {
		fmt.Fprintln(stdout, "\n"+line)
	}
	fmt.Fprintln(stdout, appWatchLine(name))
	if line := appFallbackLine(name); line != "" {
		fmt.Fprintln(stdout, line)
	}
	fmt.Fprintln(stdout, "\n"+appHelperLine(config))
	return nil
}

func appLogs(args []string, stdout, stderr io.Writer) error {
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
		return usageError{"usage: hi app logs <name> [-f]"}
	}
	if _, err := loadTeam(words[0]); err != nil {
		return err
	}
	// Only what hi app __serve and the lead print: not Podman's events, nor
	// the copy Podman's log driver keeps.
	journal := []string{"--user", "-u", appUnitName(words[0]), "-t", "hi", "-n", "200", "--no-pager", "-o", "cat"}
	if follow {
		journal = append(journal, "-f")
	}
	command := exec.Command("journalctl", journal...)
	command.Stdout, command.Stderr = stdout, stderr
	return command.Run()
}
