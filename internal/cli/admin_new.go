package cli

// New apps on the admin page (hi_admin.md, New app; release 3): hi app
// new's ten steps as pages, each answer checked as it's given, the same
// checks as the terminal's, and kept in a draft, so a reload or a closed
// laptop loses nothing. Create writes the answers as hi app new --from
// takes them, the tokens in a file only the job reads, and runs hi app
// new and hi app up as one job. Bring one back, Rename, and Remove run
// their hi app commands as jobs too.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/slack-go/slack"
)

// adminNewSteps are the steps, in order, as the page names them.
var adminNewSteps = []string{"lead", "purpose", "slack", "channel", "people", "members", "models", "code", "network", "heartbeats"}

// adminDraft is drafts/<name>.json: a new app's answers so far, and the
// secrets given for it. It stays on the machine, 0600; the page gets
// adminDraftView, which says only whether each secret is there.
type adminDraft struct {
	Name    string          `json:"name"`
	Folder  string          `json:"folder"`
	By      string          `json:"by"`
	Started time.Time       `json:"started"`
	Updated time.Time       `json:"updated"`
	Done    map[string]bool `json:"done"`
	Answers appAnswers      `json:"answers"`
	// Job is the job that creates it, once Create was pressed.
	Job string `json:"job,omitempty"`

	BotToken      string `json:"bot_token,omitempty"`
	AppToken      string `json:"app_token,omitempty"`
	Workspace     string `json:"workspace,omitempty"`
	OpenRouterKey string `json:"openrouter_key,omitempty"`
	OwnKey        bool   `json:"own_key,omitempty"`
	ClaudeToken   string `json:"claude_token,omitempty"`
	// ChatGPTSignIn is whose ChatGPT sign-in the roles on it use: the
	// app's own (app) or this machine's Codex's (machine).
	ChatGPTSignIn string           `json:"chatgpt_signin,omitempty"`
	ChatGPT       *appChatGPTGrant `json:"chatgpt,omitempty"`
	ChatGPTModels []string         `json:"chatgpt_models,omitempty"`
	ChatGPTCode   string           `json:"chatgpt_code,omitempty"`
	ChatGPTEnds   time.Time        `json:"chatgpt_ends,omitzero"`
	ChatGPTError  string           `json:"chatgpt_error,omitempty"`
	Look          *adminAdoptLook  `json:"look,omitempty"`
}

// adminAdoptLook is what the page shows of a project before adopting it.
type adminAdoptLook struct {
	Source      string `json:"source"`
	Folder      string `json:"folder,omitempty"` // a folder on this machine
	GitHub      string `json:"github,omitempty"`
	GitLab      string `json:"gitlab,omitempty"` // host/group/name, when this machine's glab is signed in there
	Description string `json:"description,omitempty"`
	SizeMB      int    `json:"size_mb,omitempty"`
	Files       int    `json:"files,omitempty"`
	Branch      string `json:"branch,omitempty"`
	Licence     string `json:"licence,omitempty"`
	Private     bool   `json:"private,omitempty"`
	Archived    bool   `json:"archived,omitempty"`
	CanPush     bool   `json:"can_push,omitempty"`
	Big         bool   `json:"big,omitempty"` // over 1 GB: its latest commit only, or its whole history?
	Error       string `json:"error,omitempty"`
}

func adminDraftsDir() string            { return filepath.Join(adminDir(), "drafts") }
func adminDraftPath(name string) string { return filepath.Join(adminDraftsDir(), name+".json") }

var adminDraftMu sync.Mutex

func loadAdminDraft(name string) (adminDraft, error) {
	var draft adminDraft
	if !appNamePattern.MatchString(name) {
		return draft, fmt.Errorf("no new app %q", name)
	}
	data, err := os.ReadFile(adminDraftPath(name))
	if err != nil {
		return draft, fmt.Errorf("no new app %s under way", name)
	}
	if err := json.Unmarshal(data, &draft); err != nil {
		return draft, err
	}
	if draft.Done == nil {
		draft.Done = map[string]bool{}
	}
	return draft, nil
}

func saveAdminDraft(draft adminDraft) error {
	if err := os.MkdirAll(adminDraftsDir(), 0o700); err != nil {
		return err
	}
	draft.Updated = time.Now().UTC()
	data, _ := json.MarshalIndent(draft, "", "  ")
	return writeFileAtomic(adminDraftPath(draft.Name), append(data, '\n'), 0o600)
}

// adminChangeDraft loads a draft, changes it, and saves it, in turn with
// the page's other changes to it.
func adminChangeDraft(name string, change func(*adminDraft) error) (adminDraft, error) {
	adminDraftMu.Lock()
	defer adminDraftMu.Unlock()
	draft, err := loadAdminDraft(name)
	if err != nil {
		return draft, err
	}
	if draft.Job != "" {
		if job, err := loadAdminJob(draft.Job); err == nil && job.State == "running" {
			return draft, fmt.Errorf("app %s is being created; Jobs has how it goes", name)
		}
	}
	if err := change(&draft); err != nil {
		return draft, err
	}
	return draft, saveAdminDraft(draft)
}

// adminDraftView is a draft as the page sees it: no secret, only whether
// each is given, and what the steps need to show.
type adminDraftView struct {
	Name          string            `json:"name"`
	Folder        string            `json:"folder"`
	By            string            `json:"by"`
	Started       time.Time         `json:"started"`
	Updated       time.Time         `json:"updated"`
	Done          map[string]bool   `json:"done"`
	Config        appConfig         `json:"config"`
	Code          adminDraftCode    `json:"code"`
	Handle        string            `json:"handle"`
	Slash         string            `json:"slash"`
	ManifestURL   string            `json:"manifest_url"`
	Manifest      string            `json:"manifest"`
	Workspace     string            `json:"workspace,omitempty"`
	HasBot        bool              `json:"has_bot"`
	HasApp        bool              `json:"has_app"`
	HasOwnKey     bool              `json:"has_own_key"`
	OwnKey        bool              `json:"own_key"`
	HasClaude     bool              `json:"has_claude"`
	ChatGPTSignIn string            `json:"chatgpt_signin,omitempty"`
	ChatGPT       string            `json:"chatgpt,omitempty"` // signed in: the plan
	ChatGPTModels []string          `json:"chatgpt_models,omitempty"`
	ChatGPTCode   string            `json:"chatgpt_code,omitempty"`
	ChatGPTURL    string            `json:"chatgpt_url,omitempty"`
	ChatGPTError  string            `json:"chatgpt_error,omitempty"`
	HasNetBird    bool              `json:"has_netbird"`
	Heartbeat     string            `json:"heartbeat,omitempty"`
	MachineBeat   string            `json:"machine_heartbeat,omitempty"`
	Job           string            `json:"job,omitempty"`
	JobState      string            `json:"job_state,omitempty"`
	Look          *adminAdoptLook   `json:"look,omitempty"`
	Machine       adminNewMachine   `json:"machine"`
	Errors        map[string]string `json:"errors,omitempty"`
}

// adminDraftCode is the Code step's answer.
type adminDraftCode struct {
	Kind     string `json:"kind"` // new, empty, template, or adopt
	Template string `json:"template,omitempty"`
	Source   string `json:"source,omitempty"`
	Ref      string `json:"ref,omitempty"`
	Shallow  bool   `json:"shallow,omitempty"`
	Push     bool   `json:"push,omitempty"`
	NoSurvey bool   `json:"no_survey,omitempty"`
	GitHub   string `json:"github,omitempty"`
	GitLab   string `json:"gitlab,omitempty"`
}

// adminNewMachine is what this machine offers a new app.
type adminNewMachine struct {
	OpenRouter bool               `json:"openrouter"` // Hermes has a key the app can use
	Claude     bool               `json:"claude"`     // a Claude sign-in boxes can use
	ChatGPT    bool               `json:"chatgpt"`    // Codex is signed in to ChatGPT
	CodexKey   bool               `json:"codex_key"`  // Codex is on an API key
	Heartbeat  bool               `json:"heartbeat"`  // the machine has a heartbeat
	Domain     string             `json:"domain,omitempty"`
	Efforts    []string           `json:"efforts"`
	Templates  []adminNewTemplate `json:"templates"`
	Peer       string             `json:"peer"` // the app's NetBird name
	GH         bool               `json:"gh"`   // gh is here, for GitHub
	Glab       bool               `json:"glab"` // glab is here, for GitLab
	DevName    string             `json:"dev_name,omitempty"`
}

type adminNewTemplate struct {
	Name    string `json:"name"`
	Summary string `json:"summary,omitempty"`
}

func adminNewMachineOf(name string) adminNewMachine {
	machine := adminNewMachine{Efforts: agentEfforts, Templates: []adminNewTemplate{}, Peer: appPeerName(name)}
	if key, err := readDotEnvValue(hermesEnvPath(), "OPENROUTER_API_KEY"); err == nil && key != "" {
		machine.OpenRouter = true
	}
	if _, err := boxClaudeSecret(); err == nil {
		machine.Claude = true
	}
	if _, err := readCodexChatGPT(); err == nil {
		machine.ChatGPT = true
	} else if fileExists(codexAuthPath()) {
		machine.CodexKey = true
	}
	_, machine.Heartbeat = loadMachineHeartbeat()
	_, ghErr := appGHLookPath()
	_, glabErr := appGlabLookPath()
	machine.GH, machine.Glab = ghErr == nil, glabErr == nil
	if domain, ok := loadHiDomain(); ok {
		machine.Domain = domain.addressName(name, appAddressDev)
	}
	if catalog, err := loadTemplateCatalog(io.Discard); err == nil {
		for _, template := range appTemplates(catalog) {
			machine.Templates = append(machine.Templates, adminNewTemplate{Name: firstNonEmpty(template.key, template.Name), Summary: template.Summary})
		}
	}
	return machine
}

func adminDraftViewOf(draft adminDraft) adminDraftView {
	config := draft.Answers.appConfig
	config.Name = draft.Name
	view := adminDraftView{Name: draft.Name, Folder: draft.Folder, By: draft.By, Started: draft.Started, Updated: draft.Updated,
		Done: draft.Done, Config: config, Handle: appBotHandle(config), Slash: appSlashCommand(draft.Name),
		Workspace: draft.Workspace, HasBot: draft.BotToken != "", HasApp: draft.AppToken != "",
		HasOwnKey: draft.OpenRouterKey != "", OwnKey: draft.OwnKey, HasClaude: draft.ClaudeToken != "",
		ChatGPTSignIn: draft.ChatGPTSignIn, ChatGPTModels: draft.ChatGPTModels, ChatGPTError: draft.ChatGPTError,
		HasNetBird: draft.Answers.NetBirdKey != "", Heartbeat: draft.Answers.HeartbeatURL, MachineBeat: draft.Answers.MachineHeartbeatURL,
		Job: draft.Job, Look: draft.Look, Machine: adminNewMachineOf(draft.Name)}
	view.Config.Members = config.Members
	if draft.ChatGPT != nil {
		view.ChatGPT = chatGPTPlanName(*draft.ChatGPT)
	} else if draft.ChatGPTCode != "" && time.Now().Before(draft.ChatGPTEnds) {
		view.ChatGPTCode, view.ChatGPTURL = draft.ChatGPTCode, chatGPTDeviceURL()
	}
	if draft.Job != "" {
		if job, err := loadAdminJob(draft.Job); err == nil {
			view.JobState = job.State
		}
	}
	view.Manifest = appManifest(config)
	// Slack's dialog turns a link's newlines into spaces and keeps the
	// indent, so the link carries the manifest compact.
	compact := view.Manifest
	var buffer bytes.Buffer
	if json.Compact(&buffer, []byte(view.Manifest)) == nil {
		compact = buffer.String()
	}
	view.ManifestURL = "https://api.slack.com/apps?new_app=1&manifest_json=" + url.QueryEscape(compact)
	answers := draft.Answers
	switch {
	case answers.Adopt != "":
		view.Code = adminDraftCode{Kind: "adopt", Source: answers.Adopt, Ref: answers.Ref, Shallow: answers.Shallow, Push: answers.PushToSource, NoSurvey: answers.NoSurvey}
	case answers.Empty:
		view.Code.Kind = "empty"
	case answers.Template != "":
		view.Code = adminDraftCode{Kind: "template", Template: answers.Template}
	default:
		view.Code.Kind = "new"
	}
	view.Code.GitHub, view.Code.GitLab = answers.GitHub, answers.GitLab
	return view
}

// adminNewFolder is where a new app's folder goes by default: beside the
// machine's other apps' folders, or in the account's home.
func adminNewFolder(name string) string {
	counts := map[string]int{}
	for _, app := range adminAppNames() {
		counts[filepath.Dir(appDir(app))]++
	}
	best, most := "", 0
	for dir, n := range counts {
		if n > most || n == most && dir < best {
			best, most = dir, n
		}
	}
	if best == "" {
		best, _ = os.UserHomeDir()
	}
	return filepath.Join(best, "app-"+name)
}

// adminStartDraft starts a new app's draft: its name and folder, and
// answers someone wrote before (From a file), without their secrets.
func adminStartDraft(name, folder, by string, from json.RawMessage, adopt string) (adminDraft, error) {
	name = strings.TrimSpace(name)
	if !appNamePattern.MatchString(name) {
		return adminDraft{}, fmt.Errorf("an app's name starts with a letter and has lowercase letters, digits, and dashes, up to 20: %q", name)
	}
	if name == "admin" {
		return adminDraft{}, errors.New("admin is the admin page's name; pick another for the app")
	}
	if _, err := loadTeam(name); err == nil {
		return adminDraft{}, fmt.Errorf("app %s already exists, in %s", name, appDir(name))
	}
	if _, err := os.Lstat(filepath.Join(appsDir(), name)); err == nil {
		return adminDraft{}, fmt.Errorf("there's already an app named %s on this machine", name)
	}
	folder = strings.TrimSpace(folder)
	if folder == "" {
		folder = adminNewFolder(name)
	}
	if strings.HasPrefix(folder, "~/") {
		home, _ := os.UserHomeDir()
		folder = filepath.Join(home, folder[2:])
	}
	if !filepath.IsAbs(folder) {
		return adminDraft{}, fmt.Errorf("the folder is a full path, such as %s", adminNewFolder(name))
	}
	folder = filepath.Clean(folder)
	if fileExists(folder) {
		return adminDraft{}, fmt.Errorf("%s is already there; pick another folder", folder)
	}
	if !fileExists(filepath.Dir(folder)) {
		return adminDraft{}, fmt.Errorf("there's no folder %s to put it in", filepath.Dir(folder))
	}
	adminDraftMu.Lock()
	defer adminDraftMu.Unlock()
	if _, err := loadAdminDraft(name); err == nil {
		return adminDraft{}, fmt.Errorf("a new app named %s is under way already: carry on with it, or discard it", name)
	}
	now := time.Now().UTC()
	draft := adminDraft{Name: name, Folder: folder, By: by, Started: now, Done: map[string]bool{}}
	if len(from) > 0 && string(from) != "null" {
		if err := json.Unmarshal(from, &draft.Answers); err != nil {
			return adminDraft{}, fmt.Errorf("that file isn't answers as hi app new --from takes them: %w", err)
		}
		// Secrets never come in a file of answers.
		draft.Answers.NetBirdKey = ""
	}
	if draft.Answers.Members == nil {
		draft.Answers.Members = defaultTeamMembers()
	}
	draft.Answers.Name = name
	if adopt = strings.TrimSpace(adopt); adopt != "" {
		draft.Answers.Adopt, draft.Answers.Clone = adopt, ""
		draft.Look = adminLookAt(adopt)
		if draft.Answers.Purpose == "" && draft.Look.Description != "" {
			draft.Answers.Purpose = draft.Look.Description
		}
	}
	return draft, saveAdminDraft(draft)
}

// adminDrafts are the new apps under way, the latest first.
func adminDrafts() []adminDraftView {
	entries, _ := os.ReadDir(adminDraftsDir())
	views := []adminDraftView{}
	for _, entry := range entries {
		name, ok := strings.CutSuffix(entry.Name(), ".json")
		if !ok {
			continue
		}
		if draft, err := loadAdminDraft(name); err == nil {
			view := adminDraftView{Name: draft.Name, Folder: draft.Folder, By: draft.By, Started: draft.Started, Updated: draft.Updated, Done: draft.Done, Job: draft.Job}
			if draft.Job != "" {
				if job, err := loadAdminJob(draft.Job); err == nil {
					view.JobState = job.State
				}
			}
			views = append(views, view)
		}
	}
	sort.Slice(views, func(i, j int) bool { return views[i].Updated.After(views[j].Updated) })
	return views
}

// adminLookAt is a look at a project before adopting it: what GitHub says
// of it, or what a folder here holds. Nothing is fetched yet.
var adminLookAt = func(source string) *adminAdoptLook {
	look := &adminAdoptLook{Source: source}
	if folder, ok, err := appAdoptFolder(source); ok || err != nil {
		if err != nil {
			look.Error = err.Error()
			return look
		}
		look.Folder = folder
		files := appFolderFiles(folder)
		look.Files = len(files)
		look.SizeMB = int(appFolderSize(folder, files) >> 20)
		look.Big = appFolderSize(folder, files) > appAdoptBigBytes
		return look
	}
	if !reviewIsURL(source) {
		look.Error = fmt.Sprintf("%q isn't a repository's address or a folder: give github.com/owner/name, owner/name, a URL, or a folder's path", source)
		return look
	}
	cloneURL, github, err := reviewCloneURL(source)
	if err != nil {
		look.Error = err.Error()
		return look
	}
	look.GitHub = github
	if host, path, ok := appGitLabSource(cloneURL); github == "" && ok {
		look.GitLab = host + "/" + path
		project, err := appGitLabLook(host, path)
		if err != nil {
			look.Error = "GitLab didn't say: " + err.Error()
			return look
		}
		info := project.Info
		look.Description, look.SizeMB, look.Branch = info.Description, info.SizeKB/1024, info.DefaultBranch
		look.Private, look.Archived = info.Private, info.Archived
		look.Big = info.SizeKB > appAdoptBigKB
		look.CanPush = project.canPush()
	}
	if github != "" {
		info, err := appGitHubInfo(github)
		if err != nil {
			look.Error = "GitHub didn't say: " + err.Error()
			return look
		}
		look.Description, look.SizeMB, look.Branch = info.Description, info.SizeKB/1024, info.DefaultBranch
		look.Private, look.Archived = info.Private, info.Archived
		if info.License != nil {
			look.Licence = info.License.SPDX
		}
		look.Big = info.SizeKB > appAdoptBigKB
		look.CanPush = appGitHubCanPush(github)
	}
	return look
}

// adminStepInput is one step's answers from the page. Only the step's own
// fields count; a secret left empty keeps the one given before.
type adminStepInput struct {
	Name string `json:"name"`
	Step string `json:"step"`

	LeadName string `json:"lead_name"`
	Purpose  string `json:"purpose"`

	BotToken string `json:"bot_token"`
	AppToken string `json:"app_token"`

	Channel    string `json:"channel"`
	OpsChannel string `json:"ops_channel"`

	Owners []string `json:"owners"`

	Members       map[string]appMember `json:"members"`
	ChatGPTSignIn string               `json:"chatgpt_signin"`
	ClaudeToken   string               `json:"claude_token"`
	ClaudeMachine bool                 `json:"claude_machine"`

	OwnKey        bool   `json:"own_key"`
	OpenRouterKey string `json:"openrouter_key"`

	Code adminDraftCode `json:"code"`

	NetBirdKey string `json:"netbird_key"`
	Addresses  string `json:"addresses"` // domain, netbird, names, or host
	Production string `json:"production"`
	HTTPS      bool   `json:"https"`
	Host       string `json:"host"`

	Heartbeat   string `json:"heartbeat"`
	Every       string `json:"every"`
	MachineBeat string `json:"machine_heartbeat"`
}

// adminSaveStep checks one step's answers as the terminal's wizard does,
// and keeps them.
func adminSaveStep(input adminStepInput) (adminDraft, error) {
	return adminChangeDraft(input.Name, func(draft *adminDraft) error {
		answers := &draft.Answers
		config := &answers.appConfig
		config.Name = draft.Name
		handle := appBotHandle(*config)
		switch input.Step {
		case "lead":
			name := strings.TrimSpace(input.LeadName)
			lead := config.Members["lead"]
			if name == "" || name == draft.Name {
				lead.Name = ""
			} else {
				if err := appCheckLeadName(name); err != nil {
					return err
				}
				lead.Name = name
			}
			config.Members["lead"] = lead
		case "purpose":
			config.Purpose = strings.Join(strings.Fields(input.Purpose), " ")
			if len(config.Purpose) > 400 {
				return errors.New("one sentence, please: up to 400 letters")
			}
		case "slack":
			bot, app := strings.TrimSpace(firstNonEmpty(input.BotToken, draft.BotToken)), strings.TrimSpace(firstNonEmpty(input.AppToken, draft.AppToken))
			if bot == "" || app == "" {
				return errors.New("give both tokens: the app token (xapp-…) and the bot token (xoxb-…)")
			}
			workspace, err := appCheckBotToken(bot)
			if err != nil {
				return err
			}
			if err := appCheckAppToken(app); err != nil {
				return err
			}
			draft.BotToken, draft.AppToken, draft.Workspace = bot, app, workspace
		case "channel":
			if draft.BotToken == "" {
				return errors.New("give the Slack app's tokens first (Slack app)")
			}
			channel, ops := strings.TrimSpace(input.Channel), strings.TrimSpace(input.OpsChannel)
			if _, err := appCheckChannel(handle, draft.BotToken, channel); err != nil {
				return err
			}
			if ops != "" {
				if ops == channel {
					return errors.New("that's the main channel; the ops channel is a second one")
				}
				if _, err := appCheckChannel(handle, draft.BotToken, ops); err != nil {
					return fmt.Errorf("the ops channel: %w", err)
				}
			}
			config.Channel, config.OpsChannel = channel, ops
		case "people":
			if err := appCheckPeople(strings.Join(input.Owners, ",")); err != nil {
				return err
			}
			config.Owners = slices.Compact(slices.Sorted(slices.Values(input.Owners)))
		case "members":
			return adminSaveMembers(draft, input)
		case "models":
			draft.OwnKey = input.OwnKey
			if !input.OwnKey {
				if key, err := readDotEnvValue(hermesEnvPath(), "OPENROUTER_API_KEY"); err != nil || key == "" {
					return errors.New("this machine has no OpenRouter key: give the app a key of its own")
				}
				draft.OpenRouterKey = ""
				break
			}
			key := strings.TrimSpace(firstNonEmpty(input.OpenRouterKey, draft.OpenRouterKey))
			if key == "" {
				return errors.New("give the app's OpenRouter key (openrouter.ai/settings/keys)")
			}
			if err := appCheckOpenRouterKey(key); err != nil {
				return err
			}
			draft.OpenRouterKey = key
		case "code":
			return adminSaveCode(draft, input.Code)
		case "network":
			if key := strings.TrimSpace(input.NetBirdKey); key != "" {
				answers.NetBirdKey = key
			}
			switch input.Addresses {
			case appAddressesDomain:
				if _, ok := loadHiDomain(); !ok {
					return errors.New("this machine has no domain (hi net domain)")
				}
				config.Addresses, config.Names = "", nil
			case appAddressesNetBird:
				config.Addresses, config.Names = appAddressesNetBird, nil
			case appAddressesNames:
				production := strings.TrimSpace(input.Production)
				if err := appCheckHost(production); err != nil {
					return err
				}
				names := appNamesFrom(production)
				names.HTTPS = input.HTTPS
				config.Addresses, config.Names = appAddressesNames, &names
			case "host":
				host := strings.TrimSpace(input.Host)
				if err := appCheckHost(host); err != nil {
					return err
				}
				config.Addresses, config.Names = host, nil
			default:
				return errors.New("choose how the addresses are named")
			}
		case "heartbeats":
			target := strings.TrimSpace(input.Heartbeat)
			if target != "" && target != answers.HeartbeatURL {
				if err := checkHeartbeatURL(target); err != nil {
					return err
				}
				if err := heartbeatPing(target); err != nil {
					return fmt.Errorf("the heartbeat didn't arrive: %w", err)
				}
			}
			answers.HeartbeatURL, config.HeartbeatEvery = target, ""
			if every := strings.TrimSpace(input.Every); target != "" && every != "" {
				d, err := parseHeartbeatEvery(every, appHeartbeatLeast, appHeartbeatMost)
				if err != nil {
					return err
				}
				if d != appHeartbeatDefault {
					config.HeartbeatEvery = heartbeatDuration(d)
				}
			}
			answers.MachineHeartbeatURL = ""
			if machine := strings.TrimSpace(input.MachineBeat); machine != "" {
				if _, set := loadMachineHeartbeat(); !set {
					if err := checkHeartbeatURL(machine); err != nil {
						return err
					}
					answers.MachineHeartbeatURL = machine
				}
			}
		default:
			return fmt.Errorf("no step %q", input.Step)
		}
		draft.Done[input.Step] = true
		return nil
	})
}

// adminSaveMembers is the Members step: each role's agent, model, who
// pays, and effort, with the sign-ins they need and their fallbacks on
// OpenRouter, as the terminal checks them.
func adminSaveMembers(draft *adminDraft, input adminStepInput) error {
	config := &draft.Answers.appConfig
	members := map[string]appMember{}
	for role, member := range config.Members {
		members[role] = member
	}
	for role, given := range input.Members {
		member, ok := members[role]
		if !ok {
			return fmt.Errorf("the app has no role %q", role)
		}
		member.Agent, member.Model = strings.TrimSpace(given.Agent), strings.TrimSpace(given.Model)
		member.Provider, member.Fallback = "", strings.TrimSpace(given.Fallback)
		if member.Agent == "hermes" && given.Provider == appProviderChatGPT {
			member.Provider = appProviderChatGPT
		}
		member.Effort = ""
		if given.Effort != "" && given.Effort != appDefaultEffort {
			if !slices.Contains(agentEfforts, given.Effort) {
				return fmt.Errorf("%s's effort is one of %s", role, strings.Join(agentEfforts, ", "))
			}
			member.Effort = given.Effort
		}
		members[role] = member
	}
	trial := *config
	trial.Members = members
	for _, role := range appRoles(trial) {
		if err := appCheckMember(trial, role, members[role]); err != nil {
			return fmt.Errorf("%s: %w", role, err)
		}
	}
	// ChatGPT: whose sign-in, when a role is on it.
	needsChatGPT, codexKey := false, false
	for _, role := range appRoles(trial) {
		member := members[role]
		if member.Agent == "hermes" && member.Provider == appProviderChatGPT {
			needsChatGPT = true
		}
		if member.Agent == "codex" {
			if _, err := readCodexChatGPT(); err != nil && fileExists(codexAuthPath()) && input.ChatGPTSignIn != "app" {
				codexKey = true // Codex here is on an API key; the role keeps it
			} else {
				needsChatGPT = true
			}
		}
	}
	draft.ChatGPTSignIn = ""
	if needsChatGPT {
		switch input.ChatGPTSignIn {
		case "app":
			if draft.ChatGPT == nil {
				return errors.New("sign the app in to ChatGPT first: Sign in, then type the code on any device")
			}
		case "machine":
			if _, err := readCodexChatGPT(); err != nil {
				return fmt.Errorf("this machine's Codex isn't signed in to ChatGPT: %w", err)
			}
		default:
			return errors.New("choose whose ChatGPT sign-in the roles on it use: the app's own, or this machine's")
		}
		draft.ChatGPTSignIn = input.ChatGPTSignIn
		// Hermes on ChatGPT names its model; without one it takes the
		// plan's first, as the terminal offers it.
		models := draft.ChatGPTModels
		if draft.ChatGPTSignIn == "machine" {
			models = nil
			if grant, err := readCodexChatGPT(); err == nil {
				models, _ = chatGPTModels(grant)
			}
		}
		for _, role := range appRoles(trial) {
			member := members[role]
			if member.Agent != "hermes" || member.Provider != appProviderChatGPT || member.Model != "" {
				continue
			}
			if len(models) == 0 {
				return fmt.Errorf("%s: name the ChatGPT model, such as gpt-6.1-sol", role)
			}
			member.Model = models[0]
			members[role] = member
		}
	}
	// Claude: the app's own token, or this machine's sign-in.
	usesClaude := false
	for _, member := range members {
		if member.Agent == "claude" {
			usesClaude = true
		}
	}
	if usesClaude {
		if token := strings.TrimSpace(input.ClaudeToken); token != "" {
			if err := appCheckClaudeToken(token); err != nil {
				return err
			}
			draft.ClaudeToken = token
		} else if input.ClaudeMachine {
			if _, err := boxClaudeSecret(); err != nil {
				return fmt.Errorf("this machine has no Claude sign-in boxes can use: give a token from claude setup-token (%w)", err)
			}
			draft.ClaudeToken = ""
		} else if draft.ClaudeToken == "" {
			return errors.New("give the app's Claude token, from claude setup-token, or use this machine's sign-in")
		}
	} else {
		draft.ClaudeToken = ""
	}
	// Each role's fallback on OpenRouter, as the terminal checks it.
	for _, role := range appRoles(trial) {
		member := members[role]
		usual := ""
		switch {
		case member.Agent == "hermes" && member.Provider == appProviderChatGPT:
			if len(draft.ChatGPTModels) > 0 && draft.ChatGPTSignIn == "app" && !slices.Contains(draft.ChatGPTModels, member.Model) {
				return fmt.Errorf("%s: the ChatGPT plan offers %s", role, strings.Join(draft.ChatGPTModels, ", "))
			}
			usual = appFallbackModel(member)
		case member.Agent == "codex" && !codexKey:
			model := firstNonEmpty(member.Model, defaultAgentModel("codex"))
			if model != "" {
				usual = appFallbackModel(appMember{Agent: "codex", Model: model})
			}
		case member.Agent == "claude":
			fallback, err := appClaudeFallback(appMember{Agent: "claude", Model: member.Model})
			if err != nil {
				return fmt.Errorf("%s: %w", role, err)
			}
			usual = fallback
		}
		if usual == "" && member.Fallback == "" {
			continue
		}
		fallback := firstNonEmpty(member.Fallback, usual)
		if !agentModelName.MatchString(fallback) {
			return fmt.Errorf("%s: %q isn't a model name", role, fallback)
		}
		has, err := appOpenRouterHasModel(fallback)
		if err != nil {
			return fmt.Errorf("%s: OpenRouter's models can't be read: %w", role, err)
		}
		if !has {
			return fmt.Errorf("%s: OpenRouter has no %s to fall back on; give a fallback it has", role, fallback)
		}
		if fallback == usual {
			member.Fallback = ""
		}
		members[role] = member
	}
	config.Members = members
	draft.Done["members"] = true
	return nil
}

// adminSaveCode is the Code step: a new web app, an empty repository, a
// template, or a project to adopt, and GitHub.
func adminSaveCode(draft *adminDraft, code adminDraftCode) error {
	answers := &draft.Answers
	answers.Clone, answers.Empty, answers.Template = "", false, ""
	answers.Adopt, answers.Ref, answers.Shallow, answers.Full, answers.PushToSource, answers.NoSurvey = "", "", false, false, false, false
	github, gitlab := strings.TrimSpace(code.GitHub), strings.TrimSpace(code.GitLab)
	if github != "" && gitlab != "" {
		return errors.New("the project goes to GitHub or GitLab, not both")
	}
	switch code.Kind {
	case "new":
	case "empty":
		answers.Empty = true
	case "template":
		known := false
		for _, template := range adminNewMachineOf(draft.Name).Templates {
			known = known || template.Name == code.Template
		}
		if !known {
			return fmt.Errorf("no template %q here", code.Template)
		}
		answers.Template = code.Template
	case "adopt":
		source := strings.TrimSpace(code.Source)
		if source == "" {
			return errors.New("give the project: github.com/owner/name, a URL, or a folder on this machine")
		}
		if draft.Look == nil || draft.Look.Source != source {
			draft.Look = adminLookAt(source)
		}
		if draft.Look.Error != "" {
			return errors.New(draft.Look.Error)
		}
		answers.Adopt, answers.Ref, answers.NoSurvey = source, strings.TrimSpace(code.Ref), code.NoSurvey
		answers.PushToSource = code.Push && draft.Look.CanPush
		answers.Shallow = code.Shallow && draft.Look.Folder == ""
		// Over 1 GB the terminal asks: its latest commit only, or its whole
		// history. Here the page says which.
		answers.Full = draft.Look.Big && draft.Look.Folder == "" && !answers.Shallow
		if answers.Shallow && (github != "" || gitlab != "") {
			return errors.New("a copy with only the latest commit can't go to a new repository; leave it on this machine, or fetch the whole history")
		}
		if answers.PushToSource {
			github, gitlab = "", ""
		}
	default:
		return errors.New("choose what the project starts from")
	}
	if github != "" {
		if err := appCheckGitHub(github); err != nil {
			return err
		}
	}
	if gitlab != "" {
		if err := appCheckGitLab(gitlab); err != nil {
			return err
		}
	}
	answers.GitHub, answers.GitLab = github, gitlab
	draft.Done["code"] = true
	return nil
}

// adminDraftChatGPT starts the app's own ChatGPT sign-in: a code to type
// on any device, waited for in the background.
func (s *adminServer) adminDraftChatGPT(name string) (adminDraft, error) {
	draft, err := adminChangeDraft(name, func(draft *adminDraft) error {
		if draft.ChatGPTCode != "" && time.Now().Before(draft.ChatGPTEnds) && draft.ChatGPT == nil {
			return nil // one under way
		}
		device, err := adminChatGPTStart()
		if err != nil {
			return err
		}
		draft.ChatGPT, draft.ChatGPTModels, draft.ChatGPTError = nil, nil, ""
		draft.ChatGPTCode, draft.ChatGPTEnds = device.Code, time.Now().Add(15*time.Minute)
		go func() {
			grant, err := chatGPTDeviceWaitFor(device, time.Now)
			var models []string
			if err == nil {
				models, _ = chatGPTModels(grant)
			}
			adminChangeDraft(name, func(draft *adminDraft) error {
				if draft.ChatGPTCode != device.Code {
					return nil // a newer one
				}
				draft.ChatGPTCode = ""
				if err != nil {
					draft.ChatGPTError = err.Error()
					return nil
				}
				draft.ChatGPT, draft.ChatGPTModels = &grant, models
				return nil
			})
		}()
		return nil
	})
	return draft, err
}

// adminChatGPTStart is chatGPTDeviceStart; tests replace it.
var adminChatGPTStart = chatGPTDeviceStart

// adminSlackChannel and adminSlackPerson are what the pickers offer.
type adminSlackChannel struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Private bool   `json:"private,omitempty"`
}

type adminSlackPerson struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	RealName string `json:"real_name,omitempty"`
	// Email is theirs in Slack, with the users:read.email scope.
	Email string `json:"email,omitempty"`
	App   string `json:"app,omitempty"` // the app whose workspace they're in
}

// adminSlackChannels are the channels the bot is in; adminSlackPeople the
// workspace's people, without bots. Tests replace them.
var adminSlackChannels = func(botToken string) ([]adminSlackChannel, error) {
	api := slack.New(botToken)
	var channels []adminSlackChannel
	cursor := ""
	for page := 0; page < 20; page++ {
		list, next, err := api.GetConversationsForUser(&slack.GetConversationsForUserParameters{
			Types: []string{"public_channel", "private_channel"}, ExcludeArchived: true, Limit: 200, Cursor: cursor})
		if err != nil {
			return nil, err
		}
		for _, channel := range list {
			channels = append(channels, adminSlackChannel{ID: channel.ID, Name: channel.Name, Private: channel.IsPrivate})
		}
		if cursor = next; cursor == "" {
			break
		}
	}
	sort.Slice(channels, func(i, j int) bool { return channels[i].Name < channels[j].Name })
	return channels, nil
}

var adminSlackPeople = func(botToken string) ([]adminSlackPerson, error) {
	users, err := slack.New(botToken).GetUsers()
	if err != nil {
		return nil, err
	}
	var people []adminSlackPerson
	for _, user := range users {
		if user.IsBot || user.Deleted || user.ID == "USLACKBOT" {
			continue
		}
		people = append(people, adminSlackPerson{ID: user.ID, Name: firstNonEmpty(user.Profile.DisplayName, user.Name), RealName: user.RealName, Email: strings.ToLower(user.Profile.Email)})
	}
	sort.Slice(people, func(i, j int) bool {
		return strings.ToLower(firstNonEmpty(people[i].RealName, people[i].Name)) < strings.ToLower(firstNonEmpty(people[j].RealName, people[j].Name))
	})
	return people, nil
}

// adminCreate checks every step, writes the answers as hi app new --from
// takes them and the secrets in a file only the job reads, and starts the
// job: hi app new, then hi app up. The draft stays until the job is
// done, so a failure can be fixed and tried again.
func (s *adminServer) adminCreate(name string, admin adminPerson) (adminJob, error) {
	adminDraftMu.Lock()
	draft, err := loadAdminDraft(name)
	if err != nil {
		adminDraftMu.Unlock()
		return adminJob{}, err
	}
	if draft.Job != "" {
		if job, err := loadAdminJob(draft.Job); err == nil && job.State == "running" {
			adminDraftMu.Unlock()
			return adminJob{}, fmt.Errorf("app %s is being created already", name)
		}
	}
	if err := adminNeedsMissingError(adminNeedsOf(name)); err != nil {
		adminDraftMu.Unlock()
		return adminJob{}, err
	}
	adminDraftMu.Unlock()
	for _, step := range adminNewSteps {
		if !draft.Done[step] {
			return adminJob{}, fmt.Errorf("the step %s isn't done yet", step)
		}
	}
	if _, err := loadTeam(name); err == nil {
		return adminJob{}, fmt.Errorf("app %s already exists", name)
	}
	if fileExists(draft.Folder) {
		return adminJob{}, fmt.Errorf("%s is already there; start again with another folder", draft.Folder)
	}
	answers := draft.Answers
	answers.Name = name
	if err := validateTeam(answers.appConfig); err != nil {
		return adminJob{}, err
	}
	if err := os.MkdirAll(adminJobsDir(), 0o700); err != nil {
		return adminJob{}, err
	}
	stem, err := adminNewKey()
	if err != nil {
		return adminJob{}, err
	}
	stem = filepath.Join(adminJobsDir(), "new-"+name+"-"+strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, strings.ToLower(stem))[:8])
	answersPath, envPath, chatGPTPath := stem+".answers.json", stem+".env.json", stem+".chatgpt.json"
	data, _ := json.MarshalIndent(answers, "", "  ")
	if err := os.WriteFile(answersPath, data, 0o600); err != nil {
		return adminJob{}, err
	}
	env := map[string]string{"HI_APP_SLACK_BOT_TOKEN": draft.BotToken, "HI_APP_SLACK_APP_TOKEN": draft.AppToken}
	if draft.OwnKey && draft.OpenRouterKey != "" {
		env["HI_APP_OPENROUTER_API_KEY"] = draft.OpenRouterKey
	}
	if draft.ClaudeToken != "" {
		env["HI_APP_CLAUDE_TOKEN"] = draft.ClaudeToken
	}
	remove := []string{answersPath}
	if draft.ChatGPTSignIn == "app" && draft.ChatGPT != nil {
		grant, _ := json.Marshal(draft.ChatGPT)
		if err := os.WriteFile(chatGPTPath, grant, 0o600); err != nil {
			os.Remove(answersPath)
			return adminJob{}, err
		}
		env["HI_APP_CHATGPT_AUTH"] = chatGPTPath
		remove = append(remove, chatGPTPath)
	}
	secret, _ := json.Marshal(env)
	if err := os.WriteFile(envPath, secret, 0o600); err != nil {
		for _, path := range remove {
			os.Remove(path)
		}
		return adminJob{}, err
	}
	job, err := s.startJob(adminJob{Title: "Create app " + name, App: name, By: admin.Name,
		Args: []string{"app", "new", name, "--dir", draft.Folder, "--from", answersPath},
		Then: [][]string{{"app", "up", name}}, Env: envPath, Remove: append(remove, envPath), Draft: name,
		Said: "set the app up on this machine", Tried: "set the app up"})
	if err != nil {
		for _, path := range append(remove, envPath) {
			os.Remove(path)
		}
		return job, err
	}
	adminChangeDraft(name, func(draft *adminDraft) error {
		draft.Job = job.ID
		return nil
	})
	return job, nil
}

// adminDiscardDraft drops a new app under way, and its secrets with it.
func adminDiscardDraft(name string) error {
	adminDraftMu.Lock()
	defer adminDraftMu.Unlock()
	draft, err := loadAdminDraft(name)
	if err != nil {
		return err
	}
	if draft.Job != "" {
		if job, err := loadAdminJob(draft.Job); err == nil && job.State == "running" {
			return fmt.Errorf("app %s is being created; wait for its job", name)
		}
	}
	return os.Remove(adminDraftPath(name))
}

// ---------------------------------------------------------------------------
// Bring one back, Rename, Remove

// adminBack is an app's folder on this machine that no name points at:
// one removed with its folder kept, or moved, or copied here.
type adminBack struct {
	Name    string `json:"name"`
	Folder  string `json:"folder"`
	Purpose string `json:"purpose,omitempty"`
}

// adminBackCandidates are the app folders beside the machine's apps'
// folders that aren't apps here.
func adminBackCandidates() []adminBack {
	parents := map[string]bool{}
	for _, app := range adminAppNames() {
		parents[filepath.Dir(appDir(app))] = true
	}
	var found []adminBack
	for parent := range parents {
		entries, _ := os.ReadDir(parent)
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			folder := filepath.Join(parent, entry.Name())
			if back, ok := adminBackFolder(folder); ok {
				found = append(found, back)
			}
		}
	}
	sort.Slice(found, func(i, j int) bool { return found[i].Name < found[j].Name })
	return found
}

// adminBackFolder says whether a folder is an app's that isn't on this
// machine by its name now.
func adminBackFolder(folder string) (adminBack, bool) {
	var config appConfig
	data, err := os.ReadFile(filepath.Join(folder, appConfigFile))
	if err != nil {
		data, err = os.ReadFile(filepath.Join(folder, "team.json"))
	}
	if err != nil || json.Unmarshal(data, &config) != nil || !appNamePattern.MatchString(config.Name) {
		return adminBack{}, false
	}
	if _, err := os.Lstat(filepath.Join(appsDir(), config.Name)); err == nil {
		return adminBack{}, false
	}
	return adminBack{Name: config.Name, Folder: folder, Purpose: config.Purpose}, true
}

// adminAppChange is Bring one back, Rename, or Remove, as a job.
func (s *adminServer) adminAppChange(admin adminPerson, action, app, to, folder string, keep bool) (adminJob, error) {
	switch action {
	case "back":
		folder = filepath.Clean(strings.TrimSpace(folder))
		if !filepath.IsAbs(folder) {
			return adminJob{}, errors.New("give the folder's full path")
		}
		back, ok := adminBackFolder(folder)
		if !ok {
			return adminJob{}, fmt.Errorf("%s isn't the folder of an app that's missing here", folder)
		}
		return s.startJob(adminJob{Title: "Bring back " + back.Name, App: back.Name, By: admin.Name, Args: []string{"app", "up", folder},
			Said: "brought the app back on this machine", Tried: "bring the app back"})
	case "rename":
		if _, err := loadTeam(app); err != nil {
			return adminJob{}, err
		}
		to = strings.TrimSpace(to)
		if !appNamePattern.MatchString(to) {
			return adminJob{}, fmt.Errorf("an app's name starts with a letter and has lowercase letters, digits, and dashes, up to 20: %q", to)
		}
		if to == "admin" {
			return adminJob{}, errors.New("admin is the admin page's name; pick another for the app")
		}
		if _, err := os.Lstat(filepath.Join(appsDir(), to)); err == nil {
			return adminJob{}, fmt.Errorf("there is already an app named %s on this machine", to)
		}
		adminTell(app, false, fmt.Sprintf(":pencil2: %s is renaming the app to %s, from the admin page. It stops while it's done, and starts again.", admin.Name, to))
		return s.startJob(adminJob{Title: "Rename " + app + " to " + to, App: to, By: admin.Name, Args: []string{"app", "rename", app, to, "--yes"},
			Said: "renamed the app from " + app, Tried: "rename the app from " + app})
	case "remove":
		if _, err := loadTeam(app); err != nil {
			return adminJob{}, err
		}
		args := []string{"app", "rm", app, "--yes"}
		what := "removing the app from this machine, and its folder"
		if keep {
			args = append(args, "--keep-folder")
			what = "removing the app from this machine; its folder stays, so it can come back"
		}
		adminTell(app, true, fmt.Sprintf(":wave: %s is %s, from the admin page.", admin.Name, what))
		return s.startJob(adminJob{Title: "Remove " + app, App: app, By: admin.Name, Args: args})
	}
	return adminJob{}, fmt.Errorf("no such change: %q", action)
}

// ---------------------------------------------------------------------------
// the routes

func (s *adminServer) newRoutes(mux *http.ServeMux, reply func(http.ResponseWriter, int, any),
	decode func(http.ResponseWriter, *http.Request, any) bool,
	signedIn func(func(http.ResponseWriter, *http.Request, adminPerson)) http.HandlerFunc) {
	failure := func(w http.ResponseWriter, status int, err error) {
		reply(w, status, map[string]string{"error": err.Error()})
	}
	mux.HandleFunc("GET /api/new", signedIn(func(w http.ResponseWriter, r *http.Request, admin adminPerson) {
		reply(w, http.StatusOK, map[string]any{"drafts": adminDrafts(), "back": adminBackCandidates(), "folder": filepath.Dir(adminNewFolder("x"))})
	}))
	mux.HandleFunc("GET /api/new/draft", signedIn(func(w http.ResponseWriter, r *http.Request, admin adminPerson) {
		draft, err := loadAdminDraft(r.URL.Query().Get("name"))
		if err != nil {
			failure(w, http.StatusNotFound, err)
			return
		}
		reply(w, http.StatusOK, adminDraftViewOf(draft))
	}))
	mux.HandleFunc("POST /api/new", signedIn(func(w http.ResponseWriter, r *http.Request, admin adminPerson) {
		var request struct {
			Action string          `json:"action"` // start, step, chatgpt, look, create, or discard
			Name   string          `json:"name"`
			Folder string          `json:"folder"`
			Adopt  string          `json:"adopt"`
			From   json.RawMessage `json:"from"`
			Source string          `json:"source"`
		}
		var step adminStepInput
		body, err := readLimited(r, 256<<10)
		if err != nil || json.Unmarshal(body, &request) != nil {
			failure(w, http.StatusBadRequest, errors.New("that isn't a change the page makes"))
			return
		}
		var draft adminDraft
		switch request.Action {
		case "start":
			// What every app needs comes first (hi_admin.md, release 8).
			if err = adminNeedsMissingError(adminNeedsViewOf(adminNeedsBase())); err != nil {
				break
			}
			draft, err = adminStartDraft(request.Name, request.Folder, admin.Name, request.From, request.Adopt)
			if err == nil {
				adminRecord(admin.Name, request.Name, "started a new app")
			}
		case "step":
			if json.Unmarshal(body, &step) != nil {
				failure(w, http.StatusBadRequest, errors.New("that isn't a step's answers"))
				return
			}
			draft, err = adminSaveStep(step)
		case "chatgpt":
			draft, err = s.adminDraftChatGPT(request.Name)
		case "look":
			draft, err = adminChangeDraft(request.Name, func(draft *adminDraft) error {
				draft.Look = adminLookAt(strings.TrimSpace(request.Source))
				return nil
			})
		case "create":
			job, err := s.adminCreate(request.Name, admin)
			if err != nil {
				failure(w, http.StatusBadRequest, err)
				return
			}
			reply(w, http.StatusOK, job)
			return
		case "discard":
			if err := adminDiscardDraft(request.Name); err != nil {
				failure(w, http.StatusBadRequest, err)
				return
			}
			adminRecord(admin.Name, request.Name, "discarded the new app")
			reply(w, http.StatusOK, map[string]bool{"ok": true})
			return
		default:
			failure(w, http.StatusBadRequest, fmt.Errorf("no such thing to do with a new app: %q", request.Action))
			return
		}
		if err != nil {
			// The answers so far, with what was wrong, so the page keeps them.
			if saved, loadErr := loadAdminDraft(firstNonEmpty(step.Name, request.Name)); loadErr == nil {
				view := adminDraftViewOf(saved)
				view.Errors = map[string]string{firstNonEmpty(step.Step, request.Action): err.Error()}
				reply(w, http.StatusBadRequest, map[string]any{"error": err.Error(), "draft": view})
				return
			}
			failure(w, http.StatusBadRequest, err)
			return
		}
		reply(w, http.StatusOK, adminDraftViewOf(draft))
	}))
	mux.HandleFunc("GET /api/new/slack", signedIn(func(w http.ResponseWriter, r *http.Request, admin adminPerson) {
		draft, err := loadAdminDraft(r.URL.Query().Get("name"))
		if err != nil {
			failure(w, http.StatusNotFound, err)
			return
		}
		if draft.BotToken == "" {
			failure(w, http.StatusBadRequest, errors.New("give the Slack app's tokens first"))
			return
		}
		var answer any
		if r.URL.Query().Get("what") == "people" {
			answer, err = adminSlackPeople(draft.BotToken)
		} else {
			answer, err = adminSlackChannels(draft.BotToken)
		}
		if err != nil {
			failure(w, http.StatusBadGateway, fmt.Errorf("Slack didn't say: %w", err))
			return
		}
		reply(w, http.StatusOK, answer)
	}))
	mux.HandleFunc("POST /api/apps", signedIn(func(w http.ResponseWriter, r *http.Request, admin adminPerson) {
		var request struct {
			Action string `json:"action"` // back, rename, or remove
			App    string `json:"app"`
			To     string `json:"to"`
			Folder string `json:"folder"`
			Keep   bool   `json:"keep_folder"`
		}
		if !decode(w, r, &request) {
			return
		}
		job, err := s.adminAppChange(admin, request.Action, request.App, request.To, request.Folder, request.Keep)
		if err != nil {
			failure(w, http.StatusBadRequest, err)
			return
		}
		reply(w, http.StatusOK, job)
	}))
}

func readLimited(r *http.Request, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("too long")
	}
	return data, nil
}
