package cli

// A team's roles on the subscriptions it pays for
// (docs/specs/approved/hi_app_subscriptions.md). Release 1: the lead on a
// ChatGPT plan. The team signs in to ChatGPT once (a device code, the flow
// Codex and Hermes use); only the team's server refreshes that sign-in, and
// it puts the access token where the team's proxy reads it again for each
// call, so no box holds it. The lead falls back to OpenRouter on the same
// model when ChatGPT refuses it, and the channel is told once per role.

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	appProviderOpenRouter = "openrouter"
	appProviderChatGPT    = "chatgpt"
	// chatGPTClientID is Codex's public OAuth client, which Codex and
	// Hermes sign in to ChatGPT with.
	chatGPTClientID = "app_EMoamEEZ73f0CkXaXp7hrann"
	// chatGPTRefreshAhead is how long before its access token runs out the
	// server refreshes a team's sign-in; the token lasts ten days.
	chatGPTRefreshAhead = 3 * 24 * time.Hour
	// appChatGPTKeyFile is the access token the team's proxy reads, in the
	// team's keys folder (mounted at /secrets/team in the proxy).
	appChatGPTKeyFile = "chatgpt.json"
	// boxChatGPTEnv tells the proxy where that file is.
	boxChatGPTEnv = "HI_BOX_CHATGPT"
	// chatGPTPath is where the proxy takes calls for ChatGPT's Codex
	// endpoint; anything else goes to OpenRouter.
	chatGPTPath = "/backend-api/codex/"
)

// Tests point these elsewhere.
var (
	chatGPTIssuer  = "https://auth.openai.com"
	chatGPTBackend = "https://chatgpt.com"
	// chatGPTMinPoll is the least time between asking whether someone
	// has typed the code.
	chatGPTMinPoll = 3 * time.Second
)

// appChatGPTGrant is a team's own ChatGPT sign-in, in secrets.json. The
// refresh token works once: only the team's server uses it.
type appChatGPTGrant struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	IDToken      string    `json:"id_token,omitempty"`
	AccountID    string    `json:"account_id,omitempty"`
	SignedIn     time.Time `json:"signed_in"`
	LastRefresh  time.Time `json:"last_refresh"`
	// Failed says why the server couldn't refresh it: it stays signed out
	// until someone signs in again.
	Failed string `json:"failed,omitempty"`
}

// appProvider is who pays for a member's model calls: OpenRouter unless
// it says otherwise; a ChatGPT plan for Codex, a Claude plan for Claude
// Code.
func appProvider(member appMember) string {
	switch member.Agent {
	case "codex":
		return appProviderChatGPT
	case "claude":
		return "claude"
	}
	return firstNonEmpty(member.Provider, appProviderOpenRouter)
}

// appUsesChatGPT says whether any of the team's members runs on the
// team's ChatGPT sign-in, through hi.
func appUsesChatGPT(config appConfig) bool { return len(appChatGPTRoles(config)) > 0 }

// appChatGPTRoles are the roles on ChatGPT through hi, in appRoles'
// order: Hermes members on chatgpt, and Codex members unless this machine's
// Codex is on an API key and the team has no sign-in of its own.
func appChatGPTRoles(config appConfig) []string {
	var roles []string
	codex := -1 // not looked at yet
	for _, role := range appRoles(config) {
		member := config.Members[role]
		if member.Agent == "codex" && codex == -1 {
			codex = map[bool]int{true: 1, false: 0}[appCodexViaProxy(config.Name)]
		}
		if member.Agent == "hermes" && appProvider(member) == appProviderChatGPT || member.Agent == "codex" && codex == 1 {
			roles = append(roles, role)
		}
	}
	return roles
}

// appLeadOpenRouterModel is the lead's model on OpenRouter: its own, or,
// for a lead on a subscription, its fallback. The memory and the helper
// chat call chat completions on OpenRouter with it.
func appLeadOpenRouterModel(config appConfig) string {
	lead := config.Members["lead"]
	if appProvider(lead) != appProviderOpenRouter {
		return firstNonEmpty(lead.Fallback, appFallbackModel(lead))
	}
	return firstNonEmpty(lead.Model, readHermesModelConfig(filepath.Join(hermesHome(), "config.yaml")).Default)
}

// appFallbackModel is the OpenRouter name of a subscription model, the
// same model billed per token there.
func appFallbackModel(member appMember) string {
	if appProvider(member) == appProviderChatGPT && member.Model != "" && !strings.Contains(member.Model, "/") {
		return "openai/" + member.Model
	}
	return member.Model
}

// ---------------------------------------------------------------------------
// ChatGPT's sign-in

// chatGPTClaims is what hi reads from a ChatGPT token: when it runs out,
// the account its calls are billed to, and the plan.
type chatGPTClaims struct {
	Expires   time.Time
	AccountID string
	Plan      string
	Residency string
}

func readChatGPTClaims(token string) chatGPTClaims {
	var claims chatGPTClaims
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return claims
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return claims
	}
	var raw struct {
		Exp  int64 `json:"exp"`
		Auth struct {
			AccountID string `json:"chatgpt_account_id"`
			Plan      string `json:"chatgpt_plan_type"`
			Data      string `json:"chatgpt_data_residency"`
			Compute   string `json:"chatgpt_compute_residency"`
		} `json:"https://api.openai.com/auth"`
	}
	if json.Unmarshal(payload, &raw) != nil {
		return claims
	}
	if raw.Exp > 0 {
		claims.Expires = time.Unix(raw.Exp, 0).UTC()
	}
	claims.AccountID, claims.Plan = raw.Auth.AccountID, raw.Auth.Plan
	claims.Residency = firstNonEmpty(raw.Auth.Data, raw.Auth.Compute)
	return claims
}

// chatGPTPlanName is "ChatGPT Team plan", or "ChatGPT plan" when the
// tokens don't say which.
func chatGPTPlanName(grant appChatGPTGrant) string {
	plan := firstNonEmpty(readChatGPTClaims(grant.IDToken).Plan, readChatGPTClaims(grant.AccessToken).Plan)
	if plan == "" {
		return "ChatGPT plan"
	}
	return "ChatGPT " + titleWord(plan) + " plan"
}

// chatGPTTokens is what OpenAI's token endpoint answers.
type chatGPTTokens struct {
	IDToken      string `json:"id_token"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}

func (t chatGPTTokens) grant(previous appChatGPTGrant, now time.Time) appChatGPTGrant {
	grant := previous
	grant.AccessToken = t.AccessToken
	if t.RefreshToken != "" {
		grant.RefreshToken = t.RefreshToken
	}
	if t.IDToken != "" {
		grant.IDToken = t.IDToken
	}
	grant.AccountID = firstNonEmpty(readChatGPTClaims(t.AccessToken).AccountID, readChatGPTClaims(t.IDToken).AccountID, grant.AccountID)
	grant.LastRefresh, grant.Failed = now, ""
	return grant
}

var chatGPTClient = &http.Client{Timeout: 30 * time.Second}

// chatGPTPost posts JSON or a form to OpenAI's sign-in service.
func chatGPTPost(path string, body any, form url.Values) (*http.Response, error) {
	var request *http.Request
	if form != nil {
		request, _ = http.NewRequest("POST", chatGPTIssuer+path, strings.NewReader(form.Encode()))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	} else {
		data, _ := json.Marshal(body)
		request, _ = http.NewRequest("POST", chatGPTIssuer+path, strings.NewReader(string(data)))
		request.Header.Set("Content-Type", "application/json")
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "hi/"+version_())
	return chatGPTClient.Do(request)
}

// chatGPTDevice is a device-code sign-in under way: a person opens
// chatGPTDeviceURL and types Code.
type chatGPTDevice struct {
	ID       string
	Code     string
	Interval time.Duration
}

func chatGPTDeviceURL() string { return chatGPTIssuer + "/codex/device" }

func chatGPTDeviceStart() (chatGPTDevice, error) {
	response, err := chatGPTPost("/api/accounts/deviceauth/usercode", map[string]string{"client_id": chatGPTClientID}, nil)
	if err != nil {
		return chatGPTDevice{}, fmt.Errorf("OpenAI's sign-in can't be reached: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusTooManyRequests {
		return chatGPTDevice{}, errors.New("OpenAI is turning away sign-ins from here for now; try again in a few minutes")
	}
	if response.StatusCode != http.StatusOK {
		return chatGPTDevice{}, fmt.Errorf("OpenAI's sign-in answered %s", response.Status)
	}
	var raw struct {
		ID       string          `json:"device_auth_id"`
		Code     string          `json:"user_code"`
		Interval json.RawMessage `json:"interval"`
	}
	if err := json.NewDecoder(response.Body).Decode(&raw); err != nil || raw.ID == "" || raw.Code == "" {
		return chatGPTDevice{}, errors.New("OpenAI's sign-in gave no code")
	}
	seconds := 5
	fmt.Sscan(strings.Trim(string(raw.Interval), `"`), &seconds)
	return chatGPTDevice{ID: raw.ID, Code: raw.Code, Interval: max(time.Duration(seconds)*time.Second, chatGPTMinPoll)}, nil
}

// chatGPTDeviceWait waits until the person has signed in, up to 15
// minutes, and swaps the code for the tokens.
func chatGPTDeviceWait(device chatGPTDevice, now func() time.Time) (appChatGPTGrant, error) {
	deadline := time.Now().Add(15 * time.Minute)
	var code struct {
		Code     string `json:"authorization_code"`
		Verifier string `json:"code_verifier"`
	}
	blips := 0
	for {
		if time.Now().After(deadline) {
			return appChatGPTGrant{}, errors.New("nobody signed in within 15 minutes; run it again for a new code")
		}
		time.Sleep(device.Interval)
		response, err := chatGPTPost("/api/accounts/deviceauth/token", map[string]string{"device_auth_id": device.ID, "user_code": device.Code}, nil)
		if err != nil {
			if blips++; blips >= 6 {
				return appChatGPTGrant{}, fmt.Errorf("OpenAI's sign-in can't be reached: %w", err)
			}
			continue
		}
		blips = 0
		status := response.StatusCode
		if status == http.StatusOK {
			err = json.NewDecoder(response.Body).Decode(&code)
		}
		response.Body.Close()
		if status == http.StatusForbidden || status == http.StatusNotFound {
			continue // not signed in yet
		}
		if status != http.StatusOK || err != nil || code.Code == "" || code.Verifier == "" {
			return appChatGPTGrant{}, fmt.Errorf("OpenAI's sign-in answered %d while waiting", status)
		}
		break
	}
	tokens, err := chatGPTToken(url.Values{"grant_type": {"authorization_code"}, "code": {code.Code},
		"redirect_uri": {chatGPTIssuer + "/deviceauth/callback"}, "client_id": {chatGPTClientID}, "code_verifier": {code.Verifier}})
	if err != nil {
		return appChatGPTGrant{}, err
	}
	at := now().UTC()
	grant := tokens.grant(appChatGPTGrant{}, at)
	grant.SignedIn = at
	return grant, nil
}

// errChatGPTSignedOut is a refresh OpenAI refused: the sign-in is over.
var errChatGPTSignedOut = errors.New("signed out")

func chatGPTToken(form url.Values) (chatGPTTokens, error) {
	var tokens chatGPTTokens
	response, err := chatGPTPost("/oauth/token", nil, form)
	if err != nil {
		return tokens, fmt.Errorf("OpenAI's sign-in can't be reached: %w", err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if response.StatusCode == http.StatusBadRequest || response.StatusCode == http.StatusUnauthorized {
		return tokens, fmt.Errorf("%w (%s)", errChatGPTSignedOut, qErrorMessage(body))
	}
	if response.StatusCode != http.StatusOK {
		return tokens, fmt.Errorf("OpenAI's sign-in answered %s", response.Status)
	}
	if err := json.Unmarshal(body, &tokens); err != nil || tokens.AccessToken == "" {
		return tokens, errors.New("OpenAI's sign-in gave no access token")
	}
	return tokens, nil
}

// refreshChatGPT swaps the grant's refresh token for new tokens. The old
// refresh token is spent, used or not, once this returns.
func refreshChatGPT(grant appChatGPTGrant, now time.Time) (appChatGPTGrant, error) {
	tokens, err := chatGPTToken(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {grant.RefreshToken}, "client_id": {chatGPTClientID}})
	if err != nil {
		return grant, err
	}
	return tokens.grant(grant, now), nil
}

// readCodexChatGPT is this machine's ChatGPT sign-in, Codex's: read, and
// never refreshed by hi, so Codex's own refreshes keep working.
func readCodexChatGPT() (appChatGPTGrant, error) {
	var auth struct {
		Mode   string `json:"auth_mode"`
		Tokens struct {
			IDToken      string `json:"id_token"`
			AccessToken  string `json:"access_token"`
			RefreshToken string `json:"refresh_token"`
			AccountID    string `json:"account_id"`
		} `json:"tokens"`
		LastRefresh time.Time `json:"last_refresh"`
	}
	data, err := os.ReadFile(codexAuthPath())
	if err != nil {
		return appChatGPTGrant{}, errors.New("Codex isn't signed in to ChatGPT on this machine; run codex login")
	}
	if json.Unmarshal(data, &auth) != nil || auth.Tokens.AccessToken == "" {
		return appChatGPTGrant{}, errors.New("Codex on this machine is signed in with an API key, not ChatGPT")
	}
	return appChatGPTGrant{AccessToken: auth.Tokens.AccessToken, IDToken: auth.Tokens.IDToken, RefreshToken: auth.Tokens.RefreshToken,
		AccountID: firstNonEmpty(auth.Tokens.AccountID, readChatGPTClaims(auth.Tokens.AccessToken).AccountID), LastRefresh: auth.LastRefresh}, nil
}

// readChatGPTAuthFile reads a ChatGPT sign-in from a file, for hi app new
// --from: Codex's auth.json, or a grant as secrets.json keeps it.
func readChatGPTAuthFile(path string) (appChatGPTGrant, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return appChatGPTGrant{}, err
	}
	var file struct {
		appChatGPTGrant
		Tokens *chatGPTTokens `json:"tokens"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		return appChatGPTGrant{}, fmt.Errorf("%s: %w", path, err)
	}
	grant := file.appChatGPTGrant
	if file.Tokens != nil {
		grant = file.Tokens.grant(appChatGPTGrant{}, time.Now().UTC())
	}
	if grant.AccessToken == "" || grant.RefreshToken == "" {
		return appChatGPTGrant{}, fmt.Errorf("%s has no ChatGPT tokens", path)
	}
	if grant.SignedIn.IsZero() {
		grant.SignedIn = time.Now().UTC()
	}
	return grant, nil
}

// chatGPTModels lists the models the account may use, best first; tests
// replace it.
var chatGPTModels = func(grant appChatGPTGrant) ([]string, error) {
	for _, client := range []string{"99.0.0", "0.0.0"} {
		request, _ := http.NewRequest("GET", chatGPTBackend+chatGPTPath+"models?client_version="+client, nil)
		for key, value := range chatGPTHeaders(grant.AccessToken, grant.AccountID) {
			request.Header.Set(key, value)
		}
		response, err := chatGPTClient.Do(request)
		if err != nil {
			return nil, fmt.Errorf("ChatGPT can't be reached: %w", err)
		}
		var list struct {
			Models []struct {
				Slug       string  `json:"slug"`
				Visibility string  `json:"visibility"`
				Priority   float64 `json:"priority"`
			} `json:"models"`
		}
		err = json.NewDecoder(response.Body).Decode(&list)
		response.Body.Close()
		if response.StatusCode != http.StatusOK || err != nil || len(list.Models) == 0 {
			continue
		}
		sort.SliceStable(list.Models, func(i, j int) bool { return list.Models[i].Priority < list.Models[j].Priority })
		var slugs []string
		for _, model := range list.Models {
			if model.Slug != "" && model.Visibility != "hide" && model.Visibility != "hidden" {
				slugs = append(slugs, model.Slug)
			}
		}
		return slugs, nil
	}
	return nil, errors.New("ChatGPT didn't list the account's models")
}

// chatGPTHeaders are what ChatGPT's Codex endpoint wants with a token:
// the account to bill and, for some workspaces, where its data lives.
func chatGPTHeaders(token, account string) map[string]string {
	claims := readChatGPTClaims(token)
	headers := map[string]string{"Authorization": "Bearer " + token}
	if account = firstNonEmpty(account, claims.AccountID); account != "" {
		headers["ChatGPT-Account-ID"] = account
	}
	if claims.Residency != "" {
		headers["x-openai-internal-codex-residency"] = claims.Residency
	}
	return headers
}

// appOpenRouterModelIDs lists the models OpenRouter serves, fetched once;
// tests replace it.
var appOpenRouterModelIDs = func() ([]string, error) {
	openRouterModels.Do(func() {
		response, err := (&http.Client{Timeout: 20 * time.Second}).Get(appOpenRouterUpstream + "/api/v1/models")
		if err != nil {
			openRouterModels.err = fmt.Errorf("OpenRouter can't be reached to check the model: %w", err)
			return
		}
		defer response.Body.Close()
		var list struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if err := json.NewDecoder(response.Body).Decode(&list); err != nil || response.StatusCode != http.StatusOK {
			openRouterModels.err = fmt.Errorf("OpenRouter didn't list its models (%s)", response.Status)
			return
		}
		for _, entry := range list.Data {
			openRouterModels.ids = append(openRouterModels.ids, entry.ID)
		}
	})
	return openRouterModels.ids, openRouterModels.err
}

var openRouterModels struct {
	sync.Once
	ids []string
	err error
}

// appOpenRouterHasModel says whether OpenRouter serves a model; tests
// replace it.
var appOpenRouterHasModel = func(model string) (bool, error) {
	ids, err := appOpenRouterModelIDs()
	return containsString(ids, model), err
}

// ---------------------------------------------------------------------------
// the team's sign-in, on the machine

// appChatGPTSignIn runs a device-code sign-in at the terminal; tests
// replace it.
var appChatGPTSignIn = func(stdout io.Writer) (appChatGPTGrant, error) {
	device, err := chatGPTDeviceStart()
	if err != nil {
		return appChatGPTGrant{}, err
	}
	fmt.Fprintf(stdout, "  Open %s on any device, signed in to the ChatGPT account that pays, and type %s\n", chatGPTDeviceURL(), device.Code)
	fmt.Fprintln(stdout, "  The code works for 15 minutes. Waiting…")
	return chatGPTDeviceWait(device, time.Now)
}

// appChatGPTGrantFor is the sign-in a team's ChatGPT roles use: its own,
// or this machine's (Codex's) when it has none.
func appChatGPTGrantFor(name string) (grant appChatGPTGrant, own bool, err error) {
	if secrets, err := loadTeamSecrets(name); err == nil && secrets.ChatGPT != nil {
		return *secrets.ChatGPT, true, nil
	}
	grant, err = readCodexChatGPT()
	return grant, false, err
}

// updateTeamSecrets changes secrets.json under a lock, so the server's
// refresh and a command on the machine don't write over each other.
func updateTeamSecrets(name string, change func(*appSecrets) error) error {
	lock, err := os.OpenFile(filepath.Join(appDir(name), ".secrets.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	secrets, err := loadTeamSecrets(name)
	if err != nil {
		return fmt.Errorf("the app's secrets: %w", err)
	}
	if err := change(&secrets); err != nil {
		return err
	}
	data, _ := json.MarshalIndent(secrets, "", "  ")
	return writeFileAtomic(filepath.Join(appDir(name), "secrets.json"), append(data, '\n'), 0o600)
}

// prepareTeamChatGPT refreshes the team's own sign-in when its access
// token runs out within chatGPTRefreshAhead, and writes the token where
// the team's proxy reads it; with no sign-in that works, it removes it,
// so the proxy's calls are refused and the roles fall back. It is the
// only place a team's refresh token is used.
func prepareTeamChatGPT(name string, now time.Time) error {
	file := filepath.Join(appKeysDir(name), appChatGPTKeyFile)
	var grant appChatGPTGrant
	err := updateTeamSecrets(name, func(secrets *appSecrets) error {
		if secrets.ChatGPT == nil {
			machine, err := readCodexChatGPT()
			if err != nil {
				return err
			}
			grant = machine
			return nil
		}
		if secrets.ChatGPT.Failed == "" && readChatGPTClaims(secrets.ChatGPT.AccessToken).Expires.Before(now.Add(chatGPTRefreshAhead)) {
			refreshed, err := refreshChatGPT(*secrets.ChatGPT, now)
			if errors.Is(err, errChatGPTSignedOut) {
				secrets.ChatGPT.Failed = err.Error()
			} else if err != nil {
				// Not now; the token still works until it runs out.
				grant = *secrets.ChatGPT
				return err
			} else {
				secrets.ChatGPT = &refreshed
			}
		}
		grant = *secrets.ChatGPT
		return nil
	})
	if grant.AccessToken == "" || grant.Failed != "" || !readChatGPTClaims(grant.AccessToken).Expires.After(now) {
		os.Remove(file)
		return err
	}
	if mkErr := os.MkdirAll(filepath.Dir(file), 0o700); mkErr != nil {
		return mkErr
	}
	data, _ := json.Marshal(map[string]string{"access_token": grant.AccessToken, "account_id": grant.AccountID})
	if writeErr := writeFileAtomic(file, data, 0o600); writeErr != nil {
		return writeErr
	}
	return err
}

// ---------------------------------------------------------------------------
// telling the channel, once

// appSubscriptionsState is what the server remembers: which roles the
// channel was told fell back, the sign-ins it last saw, and how far it read
// the lead's proxy log and the members' events files.
type appSubscriptionsState struct {
	Warned         map[string]time.Time `json:"warned,omitempty"`
	SignedIn       time.Time            `json:"signed_in,omitempty"`
	ClaudeSignedIn time.Time            `json:"claude_signed_in,omitempty"`
	LogOffset      int64                `json:"log_offset"`
	EventOffsets   map[string]int64     `json:"event_offsets,omitempty"`
}

func appSubscriptionsPath(name string) string {
	return filepath.Join(appDir(name), "run", "subscriptions.json")
}

// appEventsDir is where members' proxies write their fallbacks and what
// they cost, a file per box.
func appEventsDir(name string) string { return filepath.Join(appDir(name), "run", "events") }

func loadTeamSubscriptions(name string) appSubscriptionsState {
	var state appSubscriptionsState
	if data, err := os.ReadFile(appSubscriptionsPath(name)); err == nil {
		json.Unmarshal(data, &state)
	}
	return state
}

func saveTeamSubscriptions(name string, state appSubscriptionsState) error {
	data, _ := json.MarshalIndent(state, "", "  ")
	return writeFileAtomic(appSubscriptionsPath(name), append(data, '\n'), 0o600)
}

// appChatGPTRefusals reads the proxy's log from offset for the calls
// ChatGPT refused in a way that sends a role to its fallback: "signin" (the
// sign-in doesn't work) or "limit" (the plan's limit is reached). It
// returns the last such reason and where the log ends.
func appChatGPTRefusals(name string, offset int64) (string, int64) {
	reason := ""
	network, _ := appReadNetwork(appNetworkLogPath(name), offset, 100000)
	for _, line := range network.Lines {
		fields := strings.Fields(line)
		if len(fields) < 3 || fields[1] != "chatgpt-refused" {
			continue
		}
		if _, kind, _ := strings.Cut(fields[2], ":"); kind == "signin" || kind == "limit" {
			reason = kind
		}
	}
	return reason, network.To
}

// appRefusal is a role sent to its fallback: by which provider, why, to
// which model on OpenRouter, and whether there was a key to fall back on.
type appRefusal struct {
	provider, reason, model string
	noKey                   bool
}

// appMemberFallbacks reads the members' events files from where it left
// off: "<time> fallback <role> <provider> <reason> <model> [nokey]".
func appMemberFallbacks(name string, offsets map[string]int64) map[string]appRefusal {
	found := map[string]appRefusal{}
	entries, _ := os.ReadDir(appEventsDir(name))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".log") {
			continue
		}
		network, _ := appReadNetwork(filepath.Join(appEventsDir(name), entry.Name()), offsets[entry.Name()], 100000)
		offsets[entry.Name()] = network.To
		for _, line := range network.Lines {
			fields := strings.Fields(line)
			if len(fields) < 6 || fields[1] != "fallback" {
				continue
			}
			found[fields[2]] = appRefusal{provider: fields[3], reason: fields[4], model: fields[5], noKey: len(fields) > 6 && fields[6] == "nokey"}
		}
	}
	return found
}

// keepSubscriptions keeps the team's ChatGPT sign-in fresh and tells the
// channel, once per role, when a role falls back to OpenRouter.
func (b *appBroker) keepSubscriptions(done <-chan struct{}, stderr io.Writer) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	lastPrepare := time.Time{}
	for {
		if config, err := loadTeam(b.team); err == nil {
			if appUsesChatGPT(config) && time.Since(lastPrepare) >= 15*time.Minute {
				if err := prepareTeamChatGPT(b.team, time.Now()); err != nil {
					fmt.Fprintf(stderr, "hi: the app's ChatGPT sign-in: %v\n", err)
				}
				lastPrepare = time.Now()
			}
			b.tellSubscriptions(config, time.Now())
		}
		select {
		case <-done:
			return
		case <-ticker.C:
		}
	}
}

// appClaudeRoles are the roles Claude Code plays, on a Claude plan.
func appClaudeRoles(config appConfig) []string {
	var roles []string
	for _, role := range appRoles(config) {
		if config.Members[role].Agent == "claude" {
			roles = append(roles, role)
		}
	}
	return roles
}

// tellSubscriptions posts what the channel should hear about the roles on
// subscriptions: a role that fell back, once, and a sign-in that works
// again, after which a later failure is told once more.
func (b *appBroker) tellSubscriptions(config appConfig, now time.Time) {
	state := loadTeamSubscriptions(b.team)
	if state.Warned == nil {
		state.Warned = map[string]time.Time{}
	}
	if state.EventOffsets == nil {
		state.EventOffsets = map[string]int64{}
	}
	changed := false
	secrets, _ := loadTeamSecrets(b.team)
	again := func(roles []string, signedIn time.Time, seen *time.Time, what string) {
		if signedIn.IsZero() || !signedIn.After(*seen) {
			return
		}
		if !seen.IsZero() {
			var back []string
			for _, role := range roles {
				if _, ok := state.Warned[role]; ok {
					back = append(back, "the "+role)
					delete(state.Warned, role)
				}
			}
			if len(back) > 0 {
				b.channel.post(config.opsChannel(), fmt.Sprintf(":white_check_mark: %s is signed in again; %s %s back on it.",
					what, strings.Join(back, " and "), map[bool]string{true: "are", false: "is"}[len(back) > 1]))
			}
		}
		*seen, changed = signedIn, true
	}
	if secrets.ChatGPT != nil {
		again(appChatGPTRoles(config), secrets.ChatGPT.SignedIn, &state.SignedIn, "ChatGPT ("+chatGPTPlanName(*secrets.ChatGPT)+")")
	}
	again(appClaudeRoles(config), secrets.ClaudeSignedIn, &state.ClaudeSignedIn, "Claude")

	refusals := map[string]appRefusal{}
	reason, offset := appChatGPTRefusals(b.team, state.LogOffset)
	if offset != state.LogOffset {
		state.LogOffset, changed = offset, true
	}
	// The lead's proxy log speaks for the lead; a sign-in that doesn't
	// work speaks for every role on it.
	proactive := ""
	if appUsesChatGPT(config) {
		switch _, grantErr := readCodexChatGPT(); {
		case secrets.ChatGPT != nil && secrets.ChatGPT.Failed != "":
			proactive = "expired"
		case secrets.ChatGPT == nil && grantErr != nil:
			proactive = "signin"
		}
	}
	for _, role := range appChatGPTRoles(config) {
		switch {
		case proactive != "":
			refusals[role] = appRefusal{provider: appProviderChatGPT, reason: proactive}
		case reason != "" && config.Members[role].Agent == "hermes":
			refusals[role] = appRefusal{provider: appProviderChatGPT, reason: reason}
		}
	}
	before := fmt.Sprint(state.EventOffsets)
	for role, refusal := range appMemberFallbacks(b.team, state.EventOffsets) {
		refusals[role] = refusal
	}
	if fmt.Sprint(state.EventOffsets) != before {
		changed = true
	}
	for _, role := range sortedKeys(refusals) {
		if _, told := state.Warned[role]; told {
			continue
		}
		if _, ok := config.Members[role]; !ok {
			continue
		}
		if _, err := b.channel.post(config.opsChannel(), appFallbackMessage(config, secrets, role, refusals[role])); err == nil {
			state.Warned[role], changed = now.UTC(), true
		}
	}
	if changed {
		saveTeamSubscriptions(b.team, state)
	}
}

// appFallbackMessage tells the channel a role is on OpenRouter now, why,
// and who can fix it.
func appFallbackMessage(config appConfig, secrets appSecrets, role string, refusal appRefusal) string {
	member := config.Members[role]
	host, _ := os.Hostname()
	host = firstNonEmpty(host, "the app's machine")
	plan := "ChatGPT"
	if refusal.provider == "claude" {
		plan = "Claude"
	}
	why := plan + " sign-in doesn't work"
	switch refusal.reason {
	case "expired":
		why = plan + " sign-in expired"
	case "limit":
		why = plan + " plan reached its usage limit"
	}
	fallback := firstNonEmpty(refusal.model, member.Fallback, appFallbackModel(member))
	text := fmt.Sprintf(":warning: The %s's %s, so it uses OpenRouter (%s) now, billed per token.", role, why, fallback)
	if key, err := appOpenRouterKey(config.Name); refusal.noKey || err != nil || key == "" {
		text = fmt.Sprintf(":warning: The %s's %s, and the app has no OpenRouter key to fall back on, so the %s can't work.", role, why, role)
	}
	switch {
	case refusal.reason == "limit" && refusal.provider == "claude":
		text += " It tries Claude again every 15 minutes, and goes back when the plan's limit resets."
	case refusal.reason == "limit":
		text += " It goes back to ChatGPT when the plan's limit resets."
	case refusal.provider == "claude" && secrets.ClaudeToken != "":
		text += fmt.Sprintf(" An owner gives it a new token with `%s signin claude`.", appSlashCommand(config.Name))
	case refusal.provider == "claude":
		text += fmt.Sprintf(" The app uses %s's Claude sign-in: an owner gives the app a token of its own with `%s signin claude`, or an admin runs `hi agent token claude` there. Ask your IT team if you can't.", host, appSlashCommand(config.Name))
	case secrets.ChatGPT != nil:
		text += fmt.Sprintf(" An owner signs it in again with `%s signin chatgpt`.", appSlashCommand(config.Name))
	default:
		text += fmt.Sprintf(" The app uses %s's ChatGPT sign-in (Codex's): an owner signs the app in with `%s signin chatgpt` for one of its own, or an admin runs `codex login` there. Ask your IT team if you can't.", host, appSlashCommand(config.Name))
	}
	return text + " hi won't say this again for the " + role + "."
}

// appFallbackLine is hi app status's line on roles on their fallback.
func appFallbackLine(name string) string {
	state := loadTeamSubscriptions(name)
	if len(state.Warned) == 0 {
		return ""
	}
	roles := sortedKeys(state.Warned)
	return fmt.Sprintf("On OpenRouter since their subscription refused them: the %s (%s signin chatgpt|claude in Slack, or hi app signin %s here, signs in again).", strings.Join(roles, ", the "), appSlashCommand(name), name)
}

// ---------------------------------------------------------------------------
// Claude

// appClaudeKeyFile is the team's Claude token in its keys folder, which
// members' proxies read before this machine's sign-in.
const appClaudeKeyFile = "claude-token"

// appMemberProxyArgs gives a team member's proxy what it needs beyond
// this machine's sign-in: the team's own first, the team's OpenRouter key
// to fall back on, the role, and where to write fallbacks. A Claude
// member's proxy reads the team's token, then secret, this machine's; a
// Codex member's routes its calls to ChatGPT with the team's token, which
// the team's server keeps fresh, and the rest to OpenRouter.
func appMemberProxyArgs(meta boxMeta, args []string, secret string) ([]string, string, error) {
	team, role, _ := strings.Cut(meta.Team, ":")
	config, err := loadTeam(team)
	if err != nil {
		return args, secret, err
	}
	keys, events := appKeysDir(team), appEventsDir(team)
	for _, dir := range []string{keys, events} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return args, secret, err
		}
	}
	if meta.Agent == "claude" && secret == "" && !fileExists(filepath.Join(keys, appClaudeKeyFile)) {
		return args, secret, fmt.Errorf("Claude Code is signed in neither for app %s nor on this machine; hi app signin %s claude", team, team)
	}
	args = append(args, "-v", keys+":/secrets/team:ro", "-v", events+":/events")
	fallback := "/secrets/team/openrouter.env"
	if fileExists(hermesEnvPath()) {
		args = append(args, "-v", hermesEnvPath()+":/secrets/hermes.env:ro")
		fallback += ",/secrets/hermes.env"
	}
	args = append(args, "-e", boxFallbackEnv+"="+fallback, "-e", boxRoleEnv+"="+role, "-e", boxEventsEnv+"=/events/"+meta.Name+".log")
	if model := config.Members[role].Fallback; model != "" {
		args = append(args, "-e", boxFallbackModelEnv+"="+model)
	}
	if meta.Agent == "codex" {
		// Written by the team's server before the box starts; read again
		// for each call.
		if err := prepareTeamChatGPT(team, time.Now()); err != nil && !fileExists(filepath.Join(keys, appChatGPTKeyFile)) {
			return args, secret, fmt.Errorf("the app's ChatGPT sign-in: %w", err)
		}
		args = append(args, "-e", boxChatGPTEnv+"=/secrets/team/"+appChatGPTKeyFile)
		return args, "openrouter:" + fallback, nil
	}
	// Read again for each call: a token given later is used at once.
	return args, strings.TrimSuffix("/secrets/team/"+appClaudeKeyFile+","+secret, ","), nil
}

// appCodexViaProxy says whether a team's Codex members reach ChatGPT
// through their proxy, with no sign-in in the box: when the team has its
// own ChatGPT sign-in, or this machine's Codex is signed in to ChatGPT.
// Codex on an API key keeps its auth.json in the box, as before.
func appCodexViaProxy(team string) bool {
	name, _, _ := strings.Cut(team, ":")
	if secrets, err := loadTeamSecrets(name); err == nil && secrets.ChatGPT != nil {
		return true
	}
	_, err := readCodexChatGPT()
	return err == nil
}

// codexBoxConfig is a team's Codex member's config.toml: ChatGPT's Codex
// endpoint at the box's proxy, with the placeholder for a key.
func codexBoxConfig(proxyIP string) string {
	return fmt.Sprintf("model_provider = \"hi\"\n\n[model_providers.hi]\nname = \"hi\"\nbase_url = \"http://%s:%d%s\"\nwire_api = \"responses\"\nenv_key = \"%s\"\n",
		proxyIP, boxInjectPort, strings.TrimSuffix(chatGPTPath, "/"), codexBoxKeyEnv)
}

// codexBoxKeyEnv holds the placeholder Codex sends as its key.
const codexBoxKeyEnv = "HI_BOX_KEY"

// appMemberSignedIn says whether a team's member can sign in without this
// machine's sign-in: a Claude member of a team with its own token, or a
// Codex member of one with its own ChatGPT sign-in.
func appMemberSignedIn(team, kind string) bool {
	name, _, ok := strings.Cut(team, ":")
	if !ok {
		return false
	}
	secrets, err := loadTeamSecrets(name)
	return err == nil && (kind == "claude" && secrets.ClaudeToken != "" || kind == "codex" && secrets.ChatGPT != nil)
}

// appCheckClaudeToken checks a token looks like one from claude
// setup-token.
func appCheckClaudeToken(token string) error {
	if !strings.HasPrefix(token, "sk-ant-") {
		return errors.New("that isn't a Claude token; claude setup-token prints one starting sk-ant-")
	}
	return nil
}

// appClaudeFallback is the OpenRouter model a Claude member falls back
// to for its main model: its own fallback, the newest of an alias's family
// (opus, sonnet, haiku, fable), or the model's OpenRouter name.
func appClaudeFallback(member appMember) (string, error) {
	if member.Fallback != "" {
		return member.Fallback, nil
	}
	model := firstNonEmpty(member.Model, "opus")
	if strings.HasPrefix(model, "claude-") || strings.Contains(model, "/") {
		return claudeOpenRouterModel(model), nil
	}
	ids, err := appOpenRouterModelIDs()
	if err != nil {
		return "", err
	}
	best, bestVersion := "", ""
	for _, id := range ids {
		version, ok := strings.CutPrefix(id, "anthropic/claude-"+model+"-")
		if !ok || strings.Contains(version, ":") {
			continue
		}
		if best == "" || versionNewer(version, bestVersion) {
			best, bestVersion = id, version
		}
	}
	return best, nil
}

// versionNewer compares versions such as 5.5 and 5 by their numbers.
func versionNewer(a, b string) bool {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) || i < len(bs); i++ {
		var x, y int
		if i < len(as) {
			fmt.Sscan(as[i], &x)
		}
		if i < len(bs) {
			fmt.Sscan(bs[i], &y)
		}
		if x != y {
			return x > y
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// the commands

// appSignin is hi app signin <name> chatgpt|claude [--machine].
func appSignin(args []string, stdin io.Reader, stdout io.Writer) error {
	usage := usageError{"usage: hi app signin <name> chatgpt|claude [--machine]"}
	machine := false
	var words []string
	for _, arg := range args {
		if arg == "--machine" {
			machine = true
		} else {
			words = append(words, arg)
		}
	}
	if len(words) != 2 || words[1] != appProviderChatGPT && words[1] != "claude" {
		return usage
	}
	name := words[0]
	config, err := loadTeam(name)
	if err != nil {
		return err
	}
	if words[1] == "claude" {
		return appSigninClaude(config, machine, stdin, stdout)
	}
	var grant *appChatGPTGrant
	if machine {
		if _, err := readCodexChatGPT(); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "App %s uses this machine's ChatGPT sign-in (Codex's) from now on. hi never refreshes it: Codex does.\n", name)
	} else {
		fmt.Fprintf(stdout, "Signing app %s in to ChatGPT. Its roles on ChatGPT use this sign-in; only the app's server refreshes it, and no box holds it.\n", name)
		signedIn, err := appChatGPTSignIn(stdout)
		if err != nil {
			return err
		}
		grant = &signedIn
	}
	if err := updateTeamSecrets(name, func(secrets *appSecrets) error {
		secrets.ChatGPT = grant
		return nil
	}); err != nil {
		return err
	}
	if grant != nil {
		fmt.Fprintf(stdout, "Signed in: %s.\n", chatGPTPlanName(*grant))
	}
	if !appUsesChatGPT(config) {
		fmt.Fprintf(stdout, "No role is on ChatGPT yet: hi app member %s lead --provider chatgpt puts the lead on it.\n", name)
		return nil
	}
	if err := prepareTeamChatGPT(name, time.Now()); err != nil {
		return err
	}
	if appState(name) == "up" {
		fmt.Fprintln(stdout, "The app uses it from its next call, and says so in its channel.")
	}
	return nil
}

// appSigninClaude gives the team its own Claude token, from claude
// setup-token, or with --machine goes back to this machine's sign-in. The
// members' proxies read it again for each call.
func appSigninClaude(config appConfig, machine bool, stdin io.Reader, stdout io.Writer) error {
	token := ""
	if machine {
		if _, err := boxClaudeSecret(); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "App %s's Claude members use this machine's sign-in from now on.\n", config.Name)
	} else {
		fmt.Fprintln(stdout, "Run claude setup-token in another terminal, signed in to the Claude account that pays, and paste the token it prints. It lasts a year; no box holds it.")
		var err error
		if token, err = appAskUntil(stdin, stdout, "Claude token", true, appCheckClaudeToken); err != nil {
			return err
		}
	}
	if err := updateTeamSecrets(config.Name, func(secrets *appSecrets) error {
		secrets.ClaudeToken, secrets.ClaudeSignedIn = token, time.Time{}
		if token != "" {
			secrets.ClaudeSignedIn = time.Now().UTC()
		}
		return writeTeamKeys(config.Name, *secrets)
	}); err != nil {
		return err
	}
	if token != "" {
		fmt.Fprintf(stdout, "Saved. App %s's Claude members use it from their next call.\n", config.Name)
	}
	return nil
}

// appMemberCommand is hi app member <name> <role> [--provider P]
// [--model M] [--fallback M]: a member's provider and models, checked
// as the wizard checks them. A running team restarts with them.
func appMemberCommand(args []string, stdin io.Reader, stdout io.Writer) error {
	usage := usageError{"usage: hi app member <name> <role> [--provider openrouter|chatgpt] [--model <model>] [--fallback <model on OpenRouter>] [--effort <level>]"}
	var words []string
	values := map[string]*string{}
	var provider, model, fallback, effort string
	flags := map[string]*string{"--provider": &provider, "--model": &model, "--fallback": &fallback, "--effort": &effort}
	for i := 0; i < len(args); i++ {
		if target, ok := flags[args[i]]; ok {
			if i+1 >= len(args) {
				return usage
			}
			*target = args[i+1]
			values[args[i]] = target
			i++
			continue
		}
		words = append(words, args[i])
	}
	if len(words) != 2 {
		return usage
	}
	name, role := words[0], words[1]
	config, err := loadTeam(name)
	if err != nil {
		return err
	}
	member, ok := config.Members[role]
	if !ok {
		return fmt.Errorf("app %s has no %s; its roles are %s", name, role, strings.Join(appRoles(config), ", "))
	}
	if len(values) == 0 {
		fmt.Fprintf(stdout, "%s: %s\n", role, appMemberLabel(member))
		return nil
	}
	if provider != "" {
		if provider == appProviderOpenRouter {
			provider = ""
		}
		if provider != member.Provider {
			// Another provider's model names differ.
			member.Model, member.Fallback = "", ""
		}
		member.Provider = provider
	}
	if model != "" {
		member.Model = model
		if fallback == "" {
			member.Fallback = ""
		}
	}
	if fallback != "" {
		member.Fallback = fallback
	}
	if effort != "" {
		if member.Effort = effort; effort == appDefaultEffort {
			member.Effort = ""
		}
	}
	if appProvider(member) == appProviderChatGPT && (provider != "" || model != "" || fallback != "") {
		grant, _, err := appChatGPTGrantFor(name)
		if err != nil {
			return fmt.Errorf("%w; or sign the app in: hi app signin %s chatgpt", err, name)
		}
		models, err := chatGPTModels(grant)
		if err != nil {
			return err
		}
		if member.Model == "" && len(models) > 0 && member.Agent == "hermes" {
			member.Model = models[0]
		}
		if member.Model != "" && len(models) > 0 && !containsString(models, member.Model) {
			return fmt.Errorf("the account's ChatGPT plan doesn't offer %q; it offers %s", member.Model, strings.Join(models, ", "))
		}
		if fallback := firstNonEmpty(member.Fallback, appFallbackModel(member)); fallback != "" {
			if has, err := appOpenRouterHasModel(fallback); err != nil {
				return err
			} else if !has {
				return fmt.Errorf("OpenRouter has no %s to fall back on; give one with --fallback <model on OpenRouter>", fallback)
			}
			if member.Agent == "hermes" {
				member.Fallback = fallback
			}
		}
	}
	config.Members[role] = member
	if err := validateTeam(config); err != nil {
		return err
	}
	if err := saveTeam(config); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "%s: %s\n", role, appMemberLabel(member))
	if appState(name) != "up" {
		fmt.Fprintf(stdout, "App %s uses it from its next start: hi app up %s.\n", name, name)
		return nil
	}
	fmt.Fprintf(stdout, "App %s restarts with it.\n", name)
	return appUp(name, stdout)
}
