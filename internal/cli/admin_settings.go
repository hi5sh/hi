package cli

// Each app's settings on the admin page (hi_admin.md, release 4): People,
// Agents, Slack, and Settings, the machine's keys, and people across apps.
// What the page shows is read here, never a secret's value. What it
// changes runs the hi app command itself, a secret on its stdin, never on
// its command line, under the admin's name; the change is said in the
// app's ops channel and kept in Activity.

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
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

// adminAppSettings is an app's People, Agents, Slack, and Settings.
type adminAppSettings struct {
	Name    string               `json:"name"`
	People  []adminAppPerson     `json:"people"`
	Members []adminMemberSetting `json:"members"`

	OwnOpenRouter     bool    `json:"own_openrouter"`
	MachineOpenRouter bool    `json:"machine_openrouter"`
	UsesChatGPT       bool    `json:"uses_chatgpt"`
	ChatGPT           string  `json:"chatgpt,omitempty"` // the app's own sign-in: its plan
	ChatGPTFailed     string  `json:"chatgpt_failed,omitempty"`
	MachineChatGPT    bool    `json:"machine_chatgpt"`
	UsesClaude        bool    `json:"uses_claude"`
	OwnClaude         bool    `json:"own_claude"`
	MachineClaude     bool    `json:"machine_claude"`
	StageLimit        float64 `json:"stage_limit"` // dollars a day; 0 without one
	StageSpent        float64 `json:"stage_spent"`
	StageCalls        int     `json:"stage_calls"`
	MemoryDev         string  `json:"memory_dev"`
	MemoryApp         string  `json:"memory_app"`
	MemoryOwn         bool    `json:"memory_own"`

	Channel    string `json:"channel"`
	OpsChannel string `json:"ops_channel,omitempty"`
	Handle     string `json:"handle"`
	Slash      string `json:"slash"`
	Manifest   string `json:"manifest"`
	HasBot     bool   `json:"has_bot"`
	HasApp     bool   `json:"has_app"`

	Env        []adminEnvName  `json:"env"`
	Helper     map[string]bool `json:"helper"`
	AutoFreeze bool            `json:"auto_freeze"`
	Models     bool            `json:"models"` // developers pick the chat's model and effort
	Remote     string          `json:"remote,omitempty"`
	Forge      string          `json:"forge"` // github, gitlab, or hi
	Adopted    *adminAdopted   `json:"adopted,omitempty"`
	Heartbeat  bool            `json:"heartbeat"`
	Every      string          `json:"every,omitempty"`
}

type adminAppPerson struct {
	ID    string `json:"id"`
	Name  string `json:"name,omitempty"`
	Level string `json:"level"` // owner, pro, vibe, or contributor
	Key   string `json:"key,omitempty"`
}

type adminMemberSetting struct {
	Role     string `json:"role"`
	Agent    string `json:"agent"`
	Model    string `json:"model,omitempty"`
	Provider string `json:"provider"`
	Fallback string `json:"fallback,omitempty"`
	Effort   string `json:"effort"`
	Label    string `json:"label"`
}

// adminEnvName is a setting by its name only: where it goes, and whether
// it's a file. Its value never leaves the machine.
type adminEnvName struct {
	Name  string `json:"name"`
	Scope string `json:"scope"` // all (Stage and production), production, stage, or dev
	File  bool   `json:"file,omitempty"`
}

type adminAdopted struct {
	Name   string `json:"name"`
	From   string `json:"from"`
	Follow string `json:"follow,omitempty"`
	Local  bool   `json:"local,omitempty"`
}

// adminAutoFreeze says whether the app's watcher freezes it on its own
// (hi app watch --auto-freeze), as its watch/config.json says.
func adminAutoFreeze(name string) bool {
	var config watchConfig
	if data, err := os.ReadFile(filepath.Join(appDir(name), "watch", "config.json")); err == nil {
		json.Unmarshal(data, &config)
	}
	return config.AutoFreeze != "off"
}

// adminPageLevel is a level as the page names it.
func adminPageLevel(config appConfig, id string) string {
	switch {
	case slices.Contains(config.Owners, id):
		return "owner"
	case slices.Contains(config.Developers, id):
		return "pro"
	case slices.Contains(config.VibeDevelopers, id):
		return "vibe"
	case slices.Contains(config.Contributors, id):
		return "contributor"
	}
	return "none"
}

// adminNames remembers people's names from Slack for a while: People
// across apps would ask Slack for each of them on every read.
type adminNameCache struct {
	mu    sync.Mutex
	names map[string]string
	at    map[string]time.Time
}

func (n *adminNameCache) of(config appConfig, ids []string) map[string]string {
	n.mu.Lock()
	if n.names == nil {
		n.names, n.at = map[string]string{}, map[string]time.Time{}
	}
	names := map[string]string{}
	var missing []string
	for _, id := range ids {
		if time.Since(n.at[id]) < 10*time.Minute {
			names[id] = n.names[id]
		} else {
			missing = append(missing, id)
		}
	}
	n.mu.Unlock()
	if len(missing) > 0 {
		found := appPeopleNames(config, missing)
		n.mu.Lock()
		for _, id := range missing {
			names[id] = found[id]
			n.names[id], n.at[id] = found[id], time.Now()
		}
		n.mu.Unlock()
	}
	return names
}

func (s *adminServer) appSettings(name string) (adminAppSettings, error) {
	if !appNamePattern.MatchString(name) {
		return adminAppSettings{}, fmt.Errorf("no app %q on this machine", name)
	}
	config, err := loadTeam(name)
	if err != nil {
		return adminAppSettings{}, fmt.Errorf("no app %q on this machine", name)
	}
	secrets, _ := loadTeamSecrets(name)
	view := adminAppSettings{Name: name, People: []adminAppPerson{}, Members: []adminMemberSetting{}, Env: []adminEnvName{},
		Channel: config.Channel, OpsChannel: config.OpsChannel, Handle: appBotHandle(config), Slash: appSlashCommand(name),
		Manifest: appManifest(config), HasBot: secrets.SlackBotToken != "", HasApp: secrets.SlackAppToken != "",
		Remote: config.Remote, Forge: appTicketForge(name).Kind, AutoFreeze: adminAutoFreeze(name), Models: !config.LockModels,
		Heartbeat: secrets.HeartbeatURL != "", Every: config.HeartbeatEvery,
		Helper: map[string]bool{appSlotPreview: appHelperOn(config, appSlotPreview), appSlotProduction: appHelperOn(config, appSlotProduction)}}
	ids := slices.Concat(config.Owners, config.Developers, config.VibeDevelopers, config.Contributors)
	names := s.names.of(config, ids)
	for _, id := range ids {
		person := adminAppPerson{ID: id, Name: names[id], Level: adminPageLevel(config, id)}
		if data, err := os.ReadFile(appDevKeyPath(name, id)); err == nil {
			person.Key = appSSHKeyLabel(string(data))
		}
		view.People = append(view.People, person)
	}
	for _, role := range appRoles(config) {
		member := config.Members[role]
		view.Members = append(view.Members, adminMemberSetting{Role: role, Agent: member.Agent, Model: member.Model,
			Provider: appProvider(member), Fallback: member.Fallback, Effort: appEffort(member), Label: appMemberLabel(member)})
		if member.Agent == "claude" {
			view.UsesClaude = true
		}
	}
	view.UsesChatGPT = appUsesChatGPT(config)
	view.OwnOpenRouter = secrets.OpenRouterAPIKey != ""
	if key, err := readDotEnvValue(hermesEnvPath(), "OPENROUTER_API_KEY"); err == nil && key != "" {
		view.MachineOpenRouter = true
	}
	if secrets.ChatGPT != nil {
		view.ChatGPT, view.ChatGPTFailed = chatGPTPlanName(*secrets.ChatGPT), secrets.ChatGPT.Failed
	}
	if _, err := readCodexChatGPT(); err == nil {
		view.MachineChatGPT = true
	}
	view.OwnClaude = secrets.ClaudeToken != ""
	if _, err := boxClaudeSecret(); err == nil {
		view.MachineClaude = true
	}
	view.StageLimit = appPreviewModelLimit(name)
	spend := loadTeamPreviewSpend(name)
	view.StageSpent, view.StageCalls = spend.USD, spend.Calls
	view.MemoryDev, view.MemoryApp = appBoxMemory(config, appBoxDev), appBoxMemory(config, appBoxApp)
	view.MemoryOwn = len(config.BoxMemory) > 0
	for _, set := range []struct {
		scope      string
		env, files map[string]string
	}{{"all", secrets.Env, secrets.Files}, {"production", secrets.EnvProduction, secrets.FilesProduction},
		{"stage", secrets.EnvPreview, secrets.FilesPreview}, {"dev", secrets.EnvDev, secrets.FilesDev}} {
		for _, key := range sortedKeys(set.env) {
			view.Env = append(view.Env, adminEnvName{Name: key, Scope: set.scope})
		}
		for _, key := range sortedKeys(set.files) {
			view.Env = append(view.Env, adminEnvName{Name: key, Scope: set.scope, File: true})
		}
	}
	if adopted := config.Adopted; adopted != nil {
		view.Adopted = &adminAdopted{Name: adopted.Name, From: adopted.From, Follow: adopted.Follow, Local: adopted.Local}
	}
	return view, nil
}

// adminSettingChange is one change to an app's settings from the page.
type adminSettingChange struct {
	App      string `json:"app"`
	What     string `json:"what"`
	Person   string `json:"person"`
	Level    string `json:"level"`
	Key      string `json:"key"` // an SSH public key, or a secret for stdin
	Remove   bool   `json:"remove"`
	Role     string `json:"role"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Fallback string `json:"fallback"`
	Effort   string `json:"effort"`
	Machine  bool   `json:"machine"`
	USD      string `json:"usd"`
	Box      string `json:"box"`
	Size     string `json:"size"`
	Channel  string `json:"channel"`
	Bot      string `json:"bot"`
	AppKey   string `json:"app_token"`
	Name     string `json:"name"`
	Value    string `json:"value"`
	Scope    string `json:"scope"`
	Slot     string `json:"slot"`
	On       bool   `json:"on"`
	Repo     string `json:"repo"`
	URL      string `json:"url"`
	Every    string `json:"every"`
	adminNetworkChange
}

// adminRunHi runs a hi command now, with what it asks for on stdin, as
// the admin; tests replace it.
var adminRunHi = func(hi, by string, stdin string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, hi, args...)
	command.Stdin = strings.NewReader(stdin)
	command.Env = append(os.Environ(), adminByEnv+"="+by)
	var out bytes.Buffer
	command.Stdout, command.Stderr = &out, &out
	err := command.Run()
	text := strings.TrimSpace(out.String())
	if err != nil {
		return text, errors.New(firstNonEmpty(lastLines(strings.TrimPrefix(text, "hi: "), 3), err.Error()))
	}
	return text, nil
}

// adminSetLevel gives someone a level in an app, as hi app owners and
// hi app access do: an owner is an owner only.
func (s *adminServer) adminSetLevel(admin adminPerson, app, person, level string) (string, error) {
	config, err := loadTeam(app)
	if err != nil {
		return "", err
	}
	person = strings.Trim(strings.TrimSpace(person), "<@>")
	if err := appCheckPeople(person); err != nil {
		return "", err
	}
	current := adminPageLevel(config, person)
	if current == level {
		return person + " has that level already.", nil
	}
	var outputs []string
	run := func(stdin string, args ...string) error {
		out, err := adminRunHi(s.hi, admin.Name, stdin, args...)
		if out != "" {
			outputs = append(outputs, out)
		}
		return err
	}
	switch {
	case level == "owner":
		err = run("", "app", "owners", app, "add", person)
	case current == "owner":
		if err = run("", "app", "owners", app, "remove", person); err == nil && level != "none" {
			err = run("", "app", "access", app, person, level)
		}
	case slices.Contains([]string{"pro", "vibe", "contributor", "none"}, level):
		err = run("", "app", "access", app, person, level)
	default:
		return "", fmt.Errorf("no level %q: owner, pro, vibe, contributor, or none", level)
	}
	return strings.Join(outputs, "\n"), err
}

// adminLevelWords is a level as Slack hears it.
var adminLevelWords = map[string]string{"owner": "an owner", "pro": "a Pro Dev", "vibe": "a Vibe Dev", "contributor": "a contributor"}

// adminChangeSetting makes one change to an app's settings: the command,
// what it printed, and what the app's channel hears.
func (s *adminServer) adminChangeSetting(admin adminPerson, change adminSettingChange) (map[string]any, error) {
	app := change.App
	config, err := loadTeam(app)
	if err != nil || !appNamePattern.MatchString(app) {
		return nil, fmt.Errorf("no app %q on this machine", app)
	}
	var args []string
	stdin, said := "", ""
	asJob := false
	name := func(id string) string {
		if n := s.names.of(config, []string{id})[id]; n != "" {
			return n
		}
		return "<@" + id + ">"
	}
	switch change.What {
	case "level":
		out, err := s.adminSetLevel(admin, app, change.Person, change.Level)
		if err != nil {
			return nil, err
		}
		said = "took " + name(change.Person) + "'s level away"
		if change.Level != "none" {
			said = "made " + name(change.Person) + " " + adminLevelWords[change.Level]
		}
		s.adminSaid(admin, app, said)
		return map[string]any{"text": out}, nil
	case "sshkey":
		person := strings.Trim(change.Person, "<@>")
		command := "access"
		if slices.Contains(config.Owners, person) {
			command = "owners"
		}
		args = []string{"app", command, app, "key", person}
		if change.Remove {
			args = append(args, "--remove")
			said = "took " + name(person) + "'s SSH key out of the dev box"
		} else {
			key := strings.TrimSpace(change.Key)
			if key == "" || strings.ContainsAny(key, "\n\r") {
				return nil, errors.New("paste one public key, such as the contents of ~/.ssh/id_ed25519.pub")
			}
			args = append(args, key)
			said = "gave " + name(person) + "'s SSH key to the dev box"
		}
	case "member":
		if _, ok := config.Members[change.Role]; !ok {
			return nil, fmt.Errorf("app %s has no %s", app, change.Role)
		}
		args = []string{"app", "member", app, change.Role}
		for _, flag := range []struct{ name, value string }{{"--provider", change.Provider}, {"--model", change.Model}, {"--fallback", change.Fallback}, {"--effort", change.Effort}} {
			if value := strings.TrimSpace(flag.value); value != "" {
				args = append(args, flag.name, value)
			}
		}
		if len(args) == 4 {
			return nil, errors.New("nothing to change")
		}
		said = "changed the " + change.Role + "'s agent settings (" + strings.Join(args[4:], " ") + ")"
	case "openrouter":
		args = []string{"app", "key", app, "openrouter"}
		if change.Remove {
			args = append(args, "--unset")
			said = "put the app back on this machine's OpenRouter key"
		} else {
			if strings.TrimSpace(change.Key) == "" {
				return nil, errors.New("paste the app's OpenRouter key")
			}
			stdin = strings.TrimSpace(change.Key) + "\n"
			said = "gave the app an OpenRouter key of its own"
		}
	case "claude":
		args = []string{"app", "signin", app, "claude"}
		if change.Machine {
			args = append(args, "--machine")
			said = "put the app's Claude members on this machine's sign-in"
		} else {
			if strings.TrimSpace(change.Key) == "" {
				return nil, errors.New("paste the token claude setup-token printed")
			}
			stdin = strings.TrimSpace(change.Key) + "\n"
			said = "gave the app a Claude token of its own"
		}
	case "chatgpt":
		args = []string{"app", "signin", app, "chatgpt"}
		if change.Machine {
			args = append(args, "--machine")
			said = "put the app's ChatGPT roles on this machine's sign-in"
		} else {
			asJob = true // it shows a code, and waits for someone to type it
		}
	case "limit":
		usd := strings.TrimPrefix(strings.TrimSpace(change.USD), "$")
		if usd == "" {
			return nil, errors.New("give dollars a day, or off")
		}
		args = []string{"app", "limit", app, appSlotPreview, usd}
		said = "set what Stage's model calls may cost a day: " + map[bool]string{true: "no limit", false: "$" + usd}[usd == "off"]
	case "memory":
		args = []string{"app", "limit", app, "memory"}
		if change.Box == "default" {
			args = append(args, "default")
			said = "put the boxes' memory back to hi's"
		} else {
			if change.Box != appBoxDev && change.Box != appBoxApp {
				return nil, errors.New("the dev box's, or the app's boxes'")
			}
			args = append(args, change.Box, strings.TrimSpace(change.Size))
			said = "set the " + map[string]string{appBoxDev: "dev box's", appBoxApp: "app's boxes'"}[change.Box] + " memory to " + strings.TrimSpace(change.Size)
		}
	case "ops":
		channel := strings.TrimSpace(change.Channel)
		if channel == "" {
			channel = "off"
		}
		args = []string{"app", "ops", app, channel}
		said = "set the app's ops channel"
		if channel == "off" {
			said = "turned the ops channel off: all of it goes to the main channel"
		}
	case "slack":
		if strings.TrimSpace(change.Bot) == "" && strings.TrimSpace(change.AppKey) == "" {
			return nil, errors.New("paste the token Slack gave anew")
		}
		args = []string{"app", "key", app, "slack"}
		stdin = strings.TrimSpace(change.AppKey) + "\n" + strings.TrimSpace(change.Bot) + "\n" // as hi app key asks, since v2.6.99
		said = "gave the app its Slack tokens anew"
	case "env":
		key := strings.TrimSpace(change.Name)
		if !appEnvName.MatchString(key) {
			return nil, fmt.Errorf("%q isn't a setting's name: letters, digits, and _, such as STRIPE_KEY", key)
		}
		args = []string{"app", "env", app}
		if change.Remove {
			args = append(args, "--unset", key)
			said = "removed the setting " + key
		} else {
			args = append(args, key)
			stdin = change.Value + "\n"
			said = "set " + key
		}
		switch change.Scope {
		case "", "all":
		case "production":
			args = append(args, "--production")
			said += ", production's own"
		case "stage":
			args = append(args, "--stage")
			said += ", Stage's own"
		case "dev":
			args = append(args, "--dev")
			said += ", the dev box's own"
		default:
			return nil, fmt.Errorf("no scope %q", change.Scope)
		}
	case "helper":
		if change.Slot != appSlotPreview && change.Slot != appSlotProduction {
			return nil, errors.New("Stage or production")
		}
		on := map[bool]string{true: "on", false: "off"}[change.On]
		args = []string{"app", "helper", app, change.Slot, on}
		said = "turned the helper " + on + " in " + appAddressLabel(change.Slot)
	case "models":
		on := map[bool]string{true: "on", false: "off"}[change.On]
		args = []string{"app", "models", app, on}
		said = map[bool]string{true: "let the developers pick the chat's model and effort", false: "kept the chat's model and effort as the sessions start"}[change.On]
	case "watch":
		on := map[bool]string{true: "on", false: "off"}[change.On]
		args = []string{"app", "watch", app, "--auto-freeze", on}
		said = "turned the watcher's automatic freeze " + on
	case "heartbeat":
		args = []string{"app", "heartbeat", app}
		if change.Remove {
			args = append(args, "off")
			said = "turned the app's heartbeat off"
		} else {
			url := strings.TrimSpace(change.URL)
			if err := checkHeartbeatURL(url); err != nil {
				return nil, err
			}
			args = append(args, url)
			if every := strings.TrimSpace(change.Every); every != "" {
				args = append(args, "--every", every)
			}
			said = "set the app's heartbeat"
		}
	case "github", "gitlab":
		args, asJob = []string{"app", change.What, app, strings.TrimSpace(change.Repo)}, true
	case "upstream":
		args, asJob = []string{"app", "upstream", app}, true
	default:
		handled, netArgs, netStdin, netSaid, title, err := s.adminChangeNetworkData(admin, change, config)
		if !handled {
			return nil, fmt.Errorf("no such change: %q", change.What)
		}
		if err != nil {
			return nil, err
		}
		if title != "" {
			job, err := s.startJob(adminJob{Title: title, App: app, By: admin.Name, Args: netArgs, Said: netSaid, Tried: strings.ToLower(title[:1]) + title[1:]})
			if err != nil {
				return nil, err
			}
			return map[string]any{"job": job}, nil
		}
		args, stdin, said = netArgs, netStdin, netSaid
	}
	if asJob {
		titles := map[string]string{"chatgpt": "Sign " + app + " in to ChatGPT", "github": "Put " + app + " on GitHub", "gitlab": "Put " + app + " on GitLab", "upstream": "Fetch " + app + "'s upstream"}
		saids := map[string]string{"chatgpt": "signed the app in to ChatGPT", "github": "put the project on GitHub", "gitlab": "put the project on GitLab", "upstream": ""}
		job, err := s.startJob(adminJob{Title: titles[change.What], App: app, By: admin.Name, Args: args, Said: saids[change.What], Tried: strings.ToLower(titles[change.What][:1]) + titles[change.What][1:]})
		if err != nil {
			return nil, err
		}
		return map[string]any{"job": job}, nil
	}
	out, err := adminRunHi(s.hi, admin.Name, stdin, args...)
	if err != nil {
		return nil, err
	}
	s.adminSaid(admin, app, said)
	return map[string]any{"text": out}, nil
}

// adminSaid keeps a change in Activity and says it in the app's ops
// channel.
func (s *adminServer) adminSaid(admin adminPerson, app, said string) {
	if said == "" {
		return
	}
	adminRecord(admin.Name, app, said)
	if err := adminTell(app, false, fmt.Sprintf(":gear: %s %s, from the admin page.", admin.Name, said)); err != nil {
		s.logf("telling app %s's channel: %v", app, err)
	}
}

// ---------------------------------------------------------------------------
// people across apps, and the machine's keys

// adminPeopleRow is someone with a level in one of the apps, and theirs
// in each.
type adminPeopleRow struct {
	ID     string            `json:"id"`
	Name   string            `json:"name,omitempty"`
	Levels map[string]string `json:"levels"`
}

func (s *adminServer) peopleAcrossApps() map[string]any {
	rows := map[string]*adminPeopleRow{}
	apps := []string{}
	for _, app := range adminAppNames() {
		config, err := loadTeam(app)
		if err != nil {
			continue
		}
		apps = append(apps, app)
		ids := slices.Concat(config.Owners, config.Developers, config.VibeDevelopers, config.Contributors)
		names := s.names.of(config, ids)
		for _, id := range ids {
			row := rows[id]
			if row == nil {
				row = &adminPeopleRow{ID: id, Levels: map[string]string{}}
				rows[id] = row
			}
			row.Name = firstNonEmpty(row.Name, names[id])
			row.Levels[app] = adminPageLevel(config, id)
		}
	}
	list := []adminPeopleRow{}
	for _, row := range rows {
		list = append(list, *row)
	}
	sort.Slice(list, func(i, j int) bool {
		return strings.ToLower(firstNonEmpty(list[i].Name, list[i].ID)) < strings.ToLower(firstNonEmpty(list[j].Name, list[j].ID))
	})
	return map[string]any{"apps": apps, "people": list}
}

// adminMachineKeys is the machine's keys and sign-ins, set or not, and
// which apps use their own.
type adminMachineKeys struct {
	OpenRouter bool                `json:"openrouter"`
	Claude     bool                `json:"claude"`
	ChatGPT    bool                `json:"chatgpt"`
	CodexKey   bool                `json:"codex_key"`
	Apps       []adminAppKeysInUse `json:"apps"`
}

type adminAppKeysInUse struct {
	App        string `json:"app"`
	OpenRouter string `json:"openrouter"` // own or machine
	Claude     string `json:"claude,omitempty"`
	ChatGPT    string `json:"chatgpt,omitempty"`
}

func adminMachineKeysOf() adminMachineKeys {
	machine := adminNewMachineOf("x")
	keys := adminMachineKeys{OpenRouter: machine.OpenRouter, Claude: machine.Claude, ChatGPT: machine.ChatGPT, CodexKey: machine.CodexKey, Apps: []adminAppKeysInUse{}}
	for _, app := range adminAppNames() {
		config, err := loadTeam(app)
		if err != nil {
			continue
		}
		secrets, _ := loadTeamSecrets(app)
		use := adminAppKeysInUse{App: app, OpenRouter: map[bool]string{true: "own", false: "machine"}[secrets.OpenRouterAPIKey != ""]}
		for _, member := range config.Members {
			if member.Agent == "claude" {
				use.Claude = map[bool]string{true: "own", false: "machine"}[secrets.ClaudeToken != ""]
			}
		}
		if appUsesChatGPT(config) {
			use.ChatGPT = map[bool]string{true: "own", false: "machine"}[secrets.ChatGPT != nil]
		}
		keys.Apps = append(keys.Apps, use)
	}
	return keys
}

// adminPageFor is the admin page's address for someone who is one of its
// admins, by their Slack member ID; "" for anyone else. The dev page links
// its notes that send people to an admin there.
func adminPageFor(slackID string) string {
	if slackID == "" {
		return ""
	}
	config, ok := loadAdminConfig()
	if !ok {
		return ""
	}
	if admin, found := config.admin(slackID); !found || admin.Slack != slackID {
		return ""
	}
	return adminURL(config)
}

func (s *adminServer) settingsRoutes(mux *http.ServeMux, reply func(http.ResponseWriter, int, any),
	decode func(http.ResponseWriter, *http.Request, any) bool,
	signedIn func(func(http.ResponseWriter, *http.Request, adminPerson)) http.HandlerFunc) {
	failure := func(w http.ResponseWriter, status int, err error) {
		reply(w, status, map[string]string{"error": err.Error()})
	}
	mux.HandleFunc("GET /api/app/settings", signedIn(func(w http.ResponseWriter, r *http.Request, admin adminPerson) {
		view, err := s.appSettings(r.URL.Query().Get("name"))
		if err != nil {
			failure(w, http.StatusNotFound, err)
			return
		}
		reply(w, http.StatusOK, view)
	}))
	mux.HandleFunc("POST /api/app/settings", signedIn(func(w http.ResponseWriter, r *http.Request, admin adminPerson) {
		var change adminSettingChange
		if !decode(w, r, &change) {
			return
		}
		answer, err := s.adminChangeSetting(admin, change)
		if err != nil {
			failure(w, http.StatusBadRequest, err)
			return
		}
		reply(w, http.StatusOK, answer)
	}))
	mux.HandleFunc("GET /api/people", signedIn(func(w http.ResponseWriter, r *http.Request, admin adminPerson) {
		reply(w, http.StatusOK, s.peopleAcrossApps())
	}))
	mux.HandleFunc("POST /api/people", signedIn(func(w http.ResponseWriter, r *http.Request, admin adminPerson) {
		var request struct {
			Person string            `json:"person"`
			Levels map[string]string `json:"levels"` // app → level
		}
		if !decode(w, r, &request) {
			return
		}
		var done, failed []string
		for _, app := range sortedKeys(request.Levels) {
			level := request.Levels[app]
			if _, err := s.adminChangeSetting(admin, adminSettingChange{App: app, What: "level", Person: request.Person, Level: level}); err != nil {
				failed = append(failed, app+": "+err.Error())
			} else {
				done = append(done, app)
			}
		}
		reply(w, http.StatusOK, map[string]any{"done": done, "failed": failed})
	}))
	mux.HandleFunc("GET /api/keys", signedIn(func(w http.ResponseWriter, r *http.Request, admin adminPerson) {
		reply(w, http.StatusOK, adminMachineKeysOf())
	}))
}
