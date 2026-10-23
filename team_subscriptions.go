package main

// A team's roles on the subscriptions it pays for
// (docs/specs/approved/hi_team_subscriptions.md). Release 1: the lead on a
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
	teamProviderOpenRouter = "openrouter"
	teamProviderChatGPT    = "chatgpt"
	// chatGPTClientID is Codex's public OAuth client, which Codex and
	// Hermes sign in to ChatGPT with.
	chatGPTClientID = "app_EMoamEEZ73f0CkXaXp7hrann"
	// chatGPTRefreshAhead is how long before its access token runs out the
	// server refreshes a team's sign-in; the token lasts ten days.
	chatGPTRefreshAhead = 3 * 24 * time.Hour
	// teamChatGPTKeyFile is the access token the team's proxy reads, in the
	// team's keys folder (mounted at /secrets/team in the proxy).
	teamChatGPTKeyFile = "chatgpt.json"
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
)

// teamChatGPTGrant is a team's own ChatGPT sign-in, in secrets.json. The
// refresh token works once: only the team's server uses it.
type teamChatGPTGrant struct {
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

// teamProvider is who pays for a member's model calls: OpenRouter unless
// it says otherwise.
func teamProvider(member teamMember) string {
	return firstNonEmpty(member.Provider, teamProviderOpenRouter)
}

// teamUsesChatGPT says whether any of the team's members runs on ChatGPT.
func teamUsesChatGPT(config teamConfig) bool {
	for _, member := range config.Members {
		if teamProvider(member) == teamProviderChatGPT {
			return true
		}
	}
	return false
}

// teamChatGPTRoles are the roles on ChatGPT, in teamRoles' order.
func teamChatGPTRoles(config teamConfig) []string {
	var roles []string
	for _, role := range teamRoles(config) {
		if teamProvider(config.Members[role]) == teamProviderChatGPT {
			roles = append(roles, role)
		}
	}
	return roles
}

// teamLeadOpenRouterModel is the lead's model on OpenRouter: its own, or,
// for a lead on a subscription, its fallback. The memory and the helper
// chat call chat completions on OpenRouter with it.
func teamLeadOpenRouterModel(config teamConfig) string {
	lead := config.Members["lead"]
	if teamProvider(lead) != teamProviderOpenRouter {
		return firstNonEmpty(lead.Fallback, teamFallbackModel(lead))
	}
	return firstNonEmpty(lead.Model, readHermesModelConfig(filepath.Join(hermesHome(), "config.yaml")).Default)
}

// teamFallbackModel is the OpenRouter name of a subscription model, the
// same model billed per token there.
func teamFallbackModel(member teamMember) string {
	if teamProvider(member) == teamProviderChatGPT && member.Model != "" && !strings.Contains(member.Model, "/") {
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
func chatGPTPlanName(grant teamChatGPTGrant) string {
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

func (t chatGPTTokens) grant(previous teamChatGPTGrant, now time.Time) teamChatGPTGrant {
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
	return chatGPTDevice{ID: raw.ID, Code: raw.Code, Interval: time.Duration(max(seconds, 3)) * time.Second}, nil
}

// chatGPTDeviceWait waits until the person has signed in, up to 15
// minutes, and swaps the code for the tokens.
func chatGPTDeviceWait(device chatGPTDevice, now func() time.Time) (teamChatGPTGrant, error) {
	deadline := time.Now().Add(15 * time.Minute)
	var code struct {
		Code     string `json:"authorization_code"`
		Verifier string `json:"code_verifier"`
	}
	blips := 0
	for {
		if time.Now().After(deadline) {
			return teamChatGPTGrant{}, errors.New("nobody signed in within 15 minutes; run it again for a new code")
		}
		time.Sleep(device.Interval)
		response, err := chatGPTPost("/api/accounts/deviceauth/token", map[string]string{"device_auth_id": device.ID, "user_code": device.Code}, nil)
		if err != nil {
			if blips++; blips >= 6 {
				return teamChatGPTGrant{}, fmt.Errorf("OpenAI's sign-in can't be reached: %w", err)
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
			return teamChatGPTGrant{}, fmt.Errorf("OpenAI's sign-in answered %d while waiting", status)
		}
		break
	}
	tokens, err := chatGPTToken(url.Values{"grant_type": {"authorization_code"}, "code": {code.Code},
		"redirect_uri": {chatGPTIssuer + "/deviceauth/callback"}, "client_id": {chatGPTClientID}, "code_verifier": {code.Verifier}})
	if err != nil {
		return teamChatGPTGrant{}, err
	}
	at := now().UTC()
	grant := tokens.grant(teamChatGPTGrant{}, at)
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
func refreshChatGPT(grant teamChatGPTGrant, now time.Time) (teamChatGPTGrant, error) {
	tokens, err := chatGPTToken(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {grant.RefreshToken}, "client_id": {chatGPTClientID}})
	if err != nil {
		return grant, err
	}
	return tokens.grant(grant, now), nil
}

// readCodexChatGPT is this machine's ChatGPT sign-in, Codex's: read, and
// never refreshed by hi, so Codex's own refreshes keep working.
func readCodexChatGPT() (teamChatGPTGrant, error) {
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
		return teamChatGPTGrant{}, errors.New("Codex isn't signed in to ChatGPT on this machine; run codex login")
	}
	if json.Unmarshal(data, &auth) != nil || auth.Tokens.AccessToken == "" {
		return teamChatGPTGrant{}, errors.New("Codex on this machine is signed in with an API key, not ChatGPT")
	}
	return teamChatGPTGrant{AccessToken: auth.Tokens.AccessToken, IDToken: auth.Tokens.IDToken, RefreshToken: auth.Tokens.RefreshToken,
		AccountID: firstNonEmpty(auth.Tokens.AccountID, readChatGPTClaims(auth.Tokens.AccessToken).AccountID), LastRefresh: auth.LastRefresh}, nil
}

// readChatGPTAuthFile reads a ChatGPT sign-in from a file, for hi team new
// --from: Codex's auth.json, or a grant as secrets.json keeps it.
func readChatGPTAuthFile(path string) (teamChatGPTGrant, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return teamChatGPTGrant{}, err
	}
	var file struct {
		teamChatGPTGrant
		Tokens *chatGPTTokens `json:"tokens"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		return teamChatGPTGrant{}, fmt.Errorf("%s: %w", path, err)
	}
	grant := file.teamChatGPTGrant
	if file.Tokens != nil {
		grant = file.Tokens.grant(teamChatGPTGrant{}, time.Now().UTC())
	}
	if grant.AccessToken == "" || grant.RefreshToken == "" {
		return teamChatGPTGrant{}, fmt.Errorf("%s has no ChatGPT tokens", path)
	}
	if grant.SignedIn.IsZero() {
		grant.SignedIn = time.Now().UTC()
	}
	return grant, nil
}

// chatGPTModels lists the models the account may use, best first; tests
// replace it.
var chatGPTModels = func(grant teamChatGPTGrant) ([]string, error) {
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

// teamOpenRouterHasModel says whether OpenRouter serves a model; tests
// replace it.
var teamOpenRouterHasModel = func(model string) (bool, error) {
	openRouterModels.Do(func() {
		response, err := (&http.Client{Timeout: 20 * time.Second}).Get(teamOpenRouterUpstream + "/api/v1/models")
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
		openRouterModels.ids = map[string]bool{}
		for _, entry := range list.Data {
			openRouterModels.ids[entry.ID] = true
		}
	})
	return openRouterModels.ids[model], openRouterModels.err
}

var openRouterModels struct {
	sync.Once
	ids map[string]bool
	err error
}

// ---------------------------------------------------------------------------
// the team's sign-in, on the machine

// teamChatGPTSignIn runs a device-code sign-in at the terminal; tests
// replace it.
var teamChatGPTSignIn = func(stdout io.Writer) (teamChatGPTGrant, error) {
	device, err := chatGPTDeviceStart()
	if err != nil {
		return teamChatGPTGrant{}, err
	}
	fmt.Fprintf(stdout, "  Open %s on any device, signed in to the ChatGPT account that pays, and type %s\n", chatGPTDeviceURL(), device.Code)
	fmt.Fprintln(stdout, "  The code works for 15 minutes. Waiting…")
	return chatGPTDeviceWait(device, time.Now)
}

// teamChatGPTGrantFor is the sign-in a team's ChatGPT roles use: its own,
// or this machine's (Codex's) when it has none.
func teamChatGPTGrantFor(name string) (grant teamChatGPTGrant, own bool, err error) {
	if secrets, err := loadTeamSecrets(name); err == nil && secrets.ChatGPT != nil {
		return *secrets.ChatGPT, true, nil
	}
	grant, err = readCodexChatGPT()
	return grant, false, err
}

// updateTeamSecrets changes secrets.json under a lock, so the server's
// refresh and a command on the machine don't write over each other.
func updateTeamSecrets(name string, change func(*teamSecrets) error) error {
	lock, err := os.OpenFile(filepath.Join(teamDir(name), ".secrets.lock"), os.O_CREATE|os.O_RDWR, 0o600)
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
		return fmt.Errorf("the team's secrets: %w", err)
	}
	if err := change(&secrets); err != nil {
		return err
	}
	data, _ := json.MarshalIndent(secrets, "", "  ")
	return writeFileAtomic(filepath.Join(teamDir(name), "secrets.json"), append(data, '\n'), 0o600)
}

// prepareTeamChatGPT refreshes the team's own sign-in when its access
// token runs out within chatGPTRefreshAhead, and writes the token where
// the team's proxy reads it; with no sign-in that works, it removes it,
// so the proxy's calls are refused and the roles fall back. It is the
// only place a team's refresh token is used.
func prepareTeamChatGPT(name string, now time.Time) error {
	file := filepath.Join(teamKeysDir(name), teamChatGPTKeyFile)
	var grant teamChatGPTGrant
	err := updateTeamSecrets(name, func(secrets *teamSecrets) error {
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

// teamSubscriptionsState is what the server remembers: which roles the
// channel was told fell back, the sign-in it last saw, and how far it read
// the proxy's log.
type teamSubscriptionsState struct {
	Warned    map[string]time.Time `json:"warned,omitempty"`
	SignedIn  time.Time            `json:"signed_in,omitempty"`
	LogOffset int64                `json:"log_offset"`
}

func teamSubscriptionsPath(name string) string {
	return filepath.Join(teamDir(name), "run", "subscriptions.json")
}

func loadTeamSubscriptions(name string) teamSubscriptionsState {
	var state teamSubscriptionsState
	if data, err := os.ReadFile(teamSubscriptionsPath(name)); err == nil {
		json.Unmarshal(data, &state)
	}
	return state
}

func saveTeamSubscriptions(name string, state teamSubscriptionsState) error {
	data, _ := json.MarshalIndent(state, "", "  ")
	return writeFileAtomic(teamSubscriptionsPath(name), append(data, '\n'), 0o600)
}

// teamChatGPTRefusals reads the proxy's log from offset for the calls
// ChatGPT refused in a way that sends a role to its fallback: "signin" (the
// sign-in doesn't work) or "limit" (the plan's limit is reached). It
// returns the last such reason and where the log ends.
func teamChatGPTRefusals(name string, offset int64) (string, int64) {
	reason := ""
	network, _ := teamReadNetwork(teamNetworkLogPath(name), offset, 100000)
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

// keepSubscriptions keeps the team's ChatGPT sign-in fresh and tells the
// channel, once per role, when a role falls back to OpenRouter.
func (b *teamBroker) keepSubscriptions(done <-chan struct{}, stderr io.Writer) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	lastPrepare := time.Time{}
	for {
		config, err := loadTeam(b.team)
		if err == nil && teamUsesChatGPT(config) {
			if time.Since(lastPrepare) >= 15*time.Minute {
				if err := prepareTeamChatGPT(b.team, time.Now()); err != nil {
					fmt.Fprintf(stderr, "hi: the team's ChatGPT sign-in: %v\n", err)
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

// tellSubscriptions posts what the channel should hear about the ChatGPT
// roles: a role that fell back, once, and a sign-in that works again.
func (b *teamBroker) tellSubscriptions(config teamConfig, now time.Time) {
	state := loadTeamSubscriptions(b.team)
	if state.Warned == nil {
		state.Warned = map[string]time.Time{}
	}
	changed := false
	grant, own, grantErr := teamChatGPTGrantFor(b.team)
	if own && grant.SignedIn.After(state.SignedIn) {
		// Signed in again: a later failure is told once more.
		if !state.SignedIn.IsZero() {
			var back []string
			for _, role := range teamChatGPTRoles(config) {
				if _, ok := state.Warned[role]; ok {
					back = append(back, "the "+role)
					delete(state.Warned, role)
				}
			}
			if len(back) > 0 {
				b.channel.post(config.Channel, fmt.Sprintf(":white_check_mark: ChatGPT is signed in again (%s); %s %s back on it.",
					chatGPTPlanName(grant), strings.Join(back, " and "), map[bool]string{true: "are", false: "is"}[len(back) > 1]))
			}
		}
		state.SignedIn, changed = grant.SignedIn, true
	}
	reason, offset := teamChatGPTRefusals(b.team, state.LogOffset)
	if offset != state.LogOffset {
		state.LogOffset, changed = offset, true
	}
	switch {
	case own && grant.Failed != "":
		reason = "expired"
	case !own && grantErr != nil:
		reason = "signin"
	}
	if reason != "" {
		for _, role := range teamChatGPTRoles(config) {
			if _, told := state.Warned[role]; told {
				continue
			}
			if _, err := b.channel.post(config.Channel, teamFallbackMessage(config, role, reason, own)); err == nil {
				state.Warned[role], changed = now.UTC(), true
			}
		}
	}
	if changed {
		saveTeamSubscriptions(b.team, state)
	}
}

// teamFallbackMessage tells the channel a role is on OpenRouter now, why,
// and who can fix it.
func teamFallbackMessage(config teamConfig, role, reason string, own bool) string {
	member := config.Members[role]
	fallback := firstNonEmpty(member.Fallback, teamFallbackModel(member))
	why := "ChatGPT sign-in doesn't work"
	switch reason {
	case "expired":
		why = "ChatGPT sign-in expired"
	case "limit":
		why = "ChatGPT plan reached its usage limit"
	}
	host, _ := os.Hostname()
	host = firstNonEmpty(host, "the team's machine")
	text := fmt.Sprintf(":warning: The %s's %s, so it uses OpenRouter (%s) now, billed per token.", role, why, fallback)
	if key, err := teamOpenRouterKey(config.Name); err != nil || key == "" {
		text = fmt.Sprintf(":warning: The %s's %s, and the team has no OpenRouter key to fall back on, so the %s can't answer.", role, why, role)
	}
	switch {
	case reason == "limit":
		text += " It goes back to ChatGPT when the plan's limit resets."
	case own:
		text += fmt.Sprintf(" To sign it in again, an admin runs `hi team signin %s chatgpt` on %s. Ask your IT team if you can't.", config.Name, host)
	default:
		text += fmt.Sprintf(" The team uses %s's ChatGPT sign-in (Codex's): an admin runs `codex login` there, or `hi team signin %s chatgpt` for a sign-in of the team's own. Ask your IT team if you can't.", host, config.Name)
	}
	return text + " hi won't say this again for the " + role + "."
}

// teamFallbackLine is hi team status's line on roles on their fallback.
func teamFallbackLine(name string) string {
	state := loadTeamSubscriptions(name)
	if len(state.Warned) == 0 {
		return ""
	}
	roles := sortedKeys(state.Warned)
	return fmt.Sprintf("On OpenRouter since ChatGPT refused them: the %s (hi team signin %s chatgpt signs in again).", strings.Join(roles, ", the "), name)
}

// ---------------------------------------------------------------------------
// the commands

// teamSignin is hi team signin <name> chatgpt [--machine].
func teamSignin(args []string, stdout io.Writer) error {
	usage := usageError{"usage: hi team signin <name> chatgpt [--machine]"}
	machine := false
	var words []string
	for _, arg := range args {
		if arg == "--machine" {
			machine = true
		} else {
			words = append(words, arg)
		}
	}
	if len(words) != 2 || words[1] != teamProviderChatGPT {
		return usage
	}
	name := words[0]
	config, err := loadTeam(name)
	if err != nil {
		return err
	}
	var grant *teamChatGPTGrant
	if machine {
		if _, err := readCodexChatGPT(); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Team %s uses this machine's ChatGPT sign-in (Codex's) from now on. hi never refreshes it: Codex does.\n", name)
	} else {
		fmt.Fprintf(stdout, "Signing team %s in to ChatGPT. Its roles on ChatGPT use this sign-in; only the team's server refreshes it, and no box holds it.\n", name)
		signedIn, err := teamChatGPTSignIn(stdout)
		if err != nil {
			return err
		}
		grant = &signedIn
	}
	if err := updateTeamSecrets(name, func(secrets *teamSecrets) error {
		secrets.ChatGPT = grant
		return nil
	}); err != nil {
		return err
	}
	if grant != nil {
		fmt.Fprintf(stdout, "Signed in: %s.\n", chatGPTPlanName(*grant))
	}
	if !teamUsesChatGPT(config) {
		fmt.Fprintf(stdout, "No role is on ChatGPT yet: hi team member %s lead --provider chatgpt puts the lead on it.\n", name)
		return nil
	}
	if err := prepareTeamChatGPT(name, time.Now()); err != nil {
		return err
	}
	if teamState(name) == "up" {
		fmt.Fprintln(stdout, "The team uses it from its next call, and says so in its channel.")
	}
	return nil
}

// teamMemberCommand is hi team member <name> <role> [--provider P]
// [--model M] [--fallback M]: a member's provider and models, checked
// as the wizard checks them. A running team restarts with them.
func teamMemberCommand(args []string, stdin io.Reader, stdout io.Writer) error {
	usage := usageError{"usage: hi team member <name> <role> [--provider openrouter|chatgpt] [--model <model>] [--fallback <model on OpenRouter>]"}
	var words []string
	values := map[string]*string{}
	var provider, model, fallback string
	flags := map[string]*string{"--provider": &provider, "--model": &model, "--fallback": &fallback}
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
		return fmt.Errorf("team %s has no %s; its roles are %s", name, role, strings.Join(teamRoles(config), ", "))
	}
	if len(values) == 0 {
		fmt.Fprintf(stdout, "%s: %s\n", role, teamMemberLabel(member))
		return nil
	}
	if provider != "" {
		if provider == teamProviderOpenRouter {
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
	if teamProvider(member) == teamProviderChatGPT {
		grant, _, err := teamChatGPTGrantFor(name)
		if err != nil {
			return fmt.Errorf("%w; or sign the team in: hi team signin %s chatgpt", err, name)
		}
		models, err := chatGPTModels(grant)
		if err != nil {
			return err
		}
		if member.Model == "" && len(models) > 0 {
			member.Model = models[0]
		}
		if len(models) > 0 && !containsString(models, member.Model) {
			return fmt.Errorf("the account's ChatGPT plan doesn't offer %q; it offers %s", member.Model, strings.Join(models, ", "))
		}
		member.Fallback = firstNonEmpty(member.Fallback, teamFallbackModel(member))
		if has, err := teamOpenRouterHasModel(member.Fallback); err != nil {
			return err
		} else if !has {
			return fmt.Errorf("OpenRouter has no %s to fall back on; give one with --fallback <model on OpenRouter>", member.Fallback)
		}
	}
	config.Members[role] = member
	if err := validateTeam(config); err != nil {
		return err
	}
	if err := saveTeam(config); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "%s: %s\n", role, teamMemberLabel(member))
	if teamState(name) != "up" {
		fmt.Fprintf(stdout, "Team %s uses it from its next start: hi team up %s.\n", name, name)
		return nil
	}
	fmt.Fprintf(stdout, "Team %s restarts with it.\n", name)
	return teamUp(name, stdout)
}
