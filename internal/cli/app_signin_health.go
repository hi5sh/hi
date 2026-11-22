package cli

// Sign-in health (docs/specs/approved/hi_app_signin_health.md): whether
// each subscription an app's agents run on works. The app's service keeps
// one record per sign-in, Claude's, ChatGPT's, and agy's, in
// run/signin-health.json, from what its proxies saw (the plan's usage on
// each answer, refusals) and from checks of its own that cost nothing: a
// Claude token counted against (count_tokens), ChatGPT's usage
// (wham/usage), and agy in the dev box (agy models, then Google's quota
// summary). The admin page shows the records and tells the hi server's
// channel when one breaks, or has less than 10% of a weekly limit left
// (admin_signin_health.go).

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The states of a sign-in, worst first.
const (
	signinSignedOut = "signed-out"
	signinLimited   = "limited"
	signinLow       = "low"
	signinExpiring  = "expiring"
	signinOK        = "ok"
	signinUnknown   = "unknown"
)

const (
	// signinLowLeft is the share of a weekly limit under which a sign-in
	// is low.
	signinLowLeft = 0.10
	// signinExpiringWithin is how long before its end a sign-in is said to
	// expire.
	signinExpiringWithin = 14 * 24 * time.Hour
	// claudeTokenLasts is how long a claude setup-token works.
	claudeTokenLasts = 365 * 24 * time.Hour
)

// How often each sign-in is checked, and while it is signed out.
var (
	signinCheckEvery = map[string]time.Duration{"claude": time.Hour, appProviderChatGPT: 15 * time.Minute, "agy": time.Hour}
	signinRecheck    = 10 * time.Minute
)

// Tests point these elsewhere.
var (
	claudeAPIBase = "https://api.anthropic.com"
	agyQuotaBase  = "https://daily-cloudcode-pa.googleapis.com"
	signinClient  = &http.Client{Timeout: 20 * time.Second}
)

// appSigninHealth is one sign-in's health: what the admin page shows, and
// the facts it comes from.
type appSigninHealth struct {
	Kind   string `json:"kind"`   // claude, chatgpt, agy
	Source string `json:"source"` // own, shared/<name>, machine
	// Key names the credential, hashed, so apps that share one are told
	// of together.
	Key        string    `json:"key"`
	Plan       string    `json:"plan,omitempty"`
	State      string    `json:"state"`
	Why        string    `json:"why,omitempty"`
	WeeklyLeft *int      `json:"weekly_left,omitempty"` // percent
	Resets     time.Time `json:"resets,omitzero"`       // a limit's end, or the weekly window's
	Seen       time.Time `json:"seen,omitzero"`         // when it was last known

	SignedOut    string    `json:"signed_out,omitempty"` // why
	Limited      bool      `json:"limited,omitempty"`
	LimitedUntil time.Time `json:"limited_until,omitzero"`
	WeeklyUsed   float64   `json:"weekly_used,omitempty"`
	WeeklyKnown  bool      `json:"weekly_known,omitempty"`
	WeeklyReset  time.Time `json:"weekly_reset,omitzero"`
	Expires      time.Time `json:"expires,omitzero"`
	Checked      time.Time `json:"checked,omitzero"`
}

// appSigninHealthState is run/signin-health.json.
type appSigninHealthState struct {
	Signins map[string]*appSigninHealth `json:"signins"`
	// Offsets are how far the events files were read.
	Offsets map[string]int64 `json:"offsets,omitempty"`
	// ProxyRestarted is when the dev box's proxy last got a new hi.
	ProxyRestarted time.Time `json:"proxy_restarted,omitzero"`
}

func appSigninHealthPath(name string) string {
	return filepath.Join(appDir(name), "run", "signin-health.json")
}

func loadAppSigninHealth(name string) (appSigninHealthState, bool) {
	var state appSigninHealthState
	data, err := os.ReadFile(appSigninHealthPath(name))
	if err != nil || json.Unmarshal(data, &state) != nil {
		return appSigninHealthState{Signins: map[string]*appSigninHealth{}, Offsets: map[string]int64{}}, false
	}
	if state.Signins == nil {
		state.Signins = map[string]*appSigninHealth{}
	}
	if state.Offsets == nil {
		state.Offsets = map[string]int64{}
	}
	return state, true
}

func saveAppSigninHealth(name string, state appSigninHealthState) error {
	data, _ := json.MarshalIndent(state, "", "  ")
	if err := os.MkdirAll(filepath.Dir(appSigninHealthPath(name)), 0o700); err != nil {
		return err
	}
	return writeFileAtomic(appSigninHealthPath(name), append(data, '\n'), 0o600)
}

// signinRank orders the states, worst first.
var signinRank = map[string]int{signinSignedOut: 0, signinLimited: 1, signinLow: 2, signinExpiring: 3, signinOK: 4, signinUnknown: 5}

// signinWorse says whether state a is worse than b.
func signinWorse(a, b string) bool {
	ra, okA := signinRank[a]
	rb, okB := signinRank[b]
	if !okA {
		ra = len(signinRank)
	}
	if !okB {
		rb = len(signinRank)
	}
	return ra < rb
}

// settle works out the state from the facts, at now.
func (h *appSigninHealth) settle(now time.Time) {
	h.WeeklyLeft, h.Resets, h.Why = nil, time.Time{}, ""
	if h.WeeklyKnown && !h.WeeklyReset.IsZero() && !now.Before(h.WeeklyReset) {
		// A new week: what it used isn't known until it's seen again.
		h.WeeklyKnown, h.WeeklyUsed = false, 0
	}
	if h.WeeklyKnown {
		left := int((1-h.WeeklyUsed)*100 + 0.5)
		left = max(0, min(100, left))
		h.WeeklyLeft = &left
	}
	if h.Limited && !h.LimitedUntil.IsZero() && !now.Before(h.LimitedUntil) {
		h.Limited, h.LimitedUntil = false, time.Time{}
	}
	switch {
	case h.SignedOut != "":
		h.State, h.Why = signinSignedOut, h.SignedOut
	case h.Limited:
		h.State, h.Why, h.Resets = signinLimited, "its usage limit is reached", h.LimitedUntil
	case !h.Expires.IsZero() && h.Expires.Sub(now) < signinExpiringWithin:
		h.State = signinExpiring
		h.Why = "the sign-in expires " + h.Expires.UTC().Format("Mon 2 Jan 2006")
		if !h.Expires.After(now) {
			h.Why = "the sign-in expired " + h.Expires.UTC().Format("Mon 2 Jan 2006")
		}
	case h.WeeklyKnown && 1-h.WeeklyUsed < signinLowLeft:
		h.State, h.Why, h.Resets = signinLow, fmt.Sprintf("%d%% of its weekly limit is left", *h.WeeklyLeft), h.WeeklyReset
	case h.WeeklyKnown || !h.Checked.IsZero() || !h.Seen.IsZero():
		h.State = signinOK
	default:
		h.State = signinUnknown
	}
}

// works says the sign-in answered as signed in, at at.
func (h *appSigninHealth) works(at time.Time) {
	h.SignedOut = ""
	if at.After(h.Seen) {
		h.Seen = at
	}
}

// signedOut says it was refused as signed out, at at.
func (h *appSigninHealth) signedOut(why string, at time.Time) {
	h.SignedOut = why
	if at.After(h.Seen) {
		h.Seen = at
	}
}

// weekly takes the share of the weekly limit used and when it resets.
func (h *appSigninHealth) weekly(used float64, reset time.Time) {
	h.WeeklyUsed, h.WeeklyKnown, h.WeeklyReset = max(0, min(1, used)), true, reset
}

// limit says a limit is reached until until (zero: not known), or not.
func (h *appSigninHealth) limit(reached bool, until time.Time) {
	h.Limited, h.LimitedUntil = reached, time.Time{}
	if reached {
		h.LimitedUntil = until
	}
}

// signinHash is a short hash of a credential, for its key.
func signinHash(text string) string {
	sum := sha256.Sum256([]byte(text))
	return fmt.Sprintf("%x", sum[:6])
}

// ---------------------------------------------------------------------------
// which sign-ins an app has

// appSigninCred is a sign-in an app's agents use: where it comes from, and
// what checks it.
type appSigninCred struct {
	kind, source, key string
	token             string    // what is sent: Claude's or ChatGPT's access token
	account           string    // ChatGPT's account ID
	full              bool      // a Claude sign-in of Claude Code's own, which may read its usage
	expires           time.Time // when the sign-in itself ends, when known
	problem           string    // why it can't be used, when it can't
}

// appClaudeCred is the Claude sign-in the app's agents and dev box use:
// the app's own token, a shared one, or this machine's.
func appClaudeCred(name string, config appConfig, secrets appSecrets) appSigninCred {
	cred := appSigninCred{kind: "claude", source: "own"}
	if config.ClaudeFrom != "" {
		cred.source = config.ClaudeFrom
	}
	if data, err := os.ReadFile(filepath.Join(appKeysDir(name), appClaudeKeyFile)); err == nil && strings.TrimSpace(string(data)) != "" {
		cred.token = strings.TrimSpace(string(data))
		cred.key = "claude:" + signinHash(cred.token)
		if cred.source == "own" && !secrets.ClaudeSignedIn.IsZero() {
			cred.expires = secrets.ClaudeSignedIn.Add(claudeTokenLasts)
		}
		return cred
	}
	if config.ClaudeFrom != "" || secrets.ClaudeToken != "" {
		// Not written for the proxies yet: the service writes it within a
		// minute.
		cred.problem = "the app's Claude token isn't there for its proxies"
		cred.key = "claude:" + firstNonEmpty(config.ClaudeFrom, signinHash(secrets.ClaudeToken))
		return cred
	}
	cred.source, cred.key = "machine", "claude:machine"
	path, err := boxClaudeSecret()
	if err != nil {
		cred.problem = "neither the app nor this machine has a Claude sign-in"
		return cred
	}
	token, err := readBoxClaudeToken(path)
	cred.token = token
	if err != nil {
		cred.problem = err.Error()
	}
	if path == boxTokenPath() {
		if info, err := os.Stat(path); err == nil {
			cred.expires = info.ModTime().Add(claudeTokenLasts)
		}
	} else {
		cred.full = true
		var credentials struct {
			ClaudeAiOauth struct {
				RefreshExpires int64 `json:"refreshTokenExpiresAt"`
			} `json:"claudeAiOauth"`
		}
		if data, err := os.ReadFile(path); err == nil && json.Unmarshal(data, &credentials) == nil && credentials.ClaudeAiOauth.RefreshExpires > 0 {
			cred.expires = time.UnixMilli(credentials.ClaudeAiOauth.RefreshExpires).UTC()
		}
	}
	return cred
}

// chatGPTUserID is the ChatGPT user a token is for, from its claims.
func chatGPTUserID(token string) string {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return ""
	}
	var raw struct {
		Auth struct {
			User string `json:"chatgpt_user_id"`
		} `json:"https://api.openai.com/auth"`
		Subject string `json:"sub"`
	}
	json.Unmarshal(payload, &raw)
	return firstNonEmpty(raw.Auth.User, raw.Subject)
}

// appChatGPTCred is the ChatGPT sign-in the app's roles and dev box use.
func appChatGPTCred(secrets appSecrets) appSigninCred {
	cred := appSigninCred{kind: appProviderChatGPT, source: "own"}
	grant := secrets.ChatGPT
	if grant == nil {
		cred.source, cred.key = "machine", "chatgpt:machine"
		machine, err := readCodexChatGPT()
		if err != nil {
			cred.problem = err.Error()
			return cred
		}
		grant = &machine
	} else {
		cred.key = "chatgpt:" + signinHash(grant.AccountID+" "+chatGPTUserID(firstNonEmpty(grant.IDToken, grant.AccessToken)))
		if grant.Failed != "" {
			cred.problem = "it signed out: " + grant.Failed
			return cred
		}
	}
	cred.token, cred.account = grant.AccessToken, firstNonEmpty(grant.AccountID, readChatGPTClaims(grant.AccessToken).AccountID)
	if expires := readChatGPTClaims(grant.AccessToken).Expires; !expires.IsZero() && !expires.After(time.Now()) {
		cred.problem = "its access token ran out"
	}
	return cred
}

// appAgyCred is the agy sign-in in the app's dev box.
func appAgyCred(secrets appSecrets) appSigninCred {
	cred := appSigninCred{kind: "agy", source: "own", key: "agy:" + signinHash(secrets.Agy.Email+" "+secrets.Agy.Token)}
	if secrets.Agy.Email != "" {
		cred.key = "agy:" + signinHash(secrets.Agy.Email)
	}
	if secrets.Agy.Machine {
		cred.source, cred.key = "machine", "agy:machine"
	}
	return cred
}

// appSigninCreds are the sign-ins an app's agents use, by kind. An error
// says they can't be known now (the app's secrets can't be read).
func appSigninCreds(name string) (map[string]appSigninCred, error) {
	creds := map[string]appSigninCred{}
	config, err := loadTeam(name)
	if err != nil {
		return nil, err
	}
	secrets, err := loadTeamSecrets(name)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("the app's secrets: %w", err)
	}
	// Claude Code is in every dev box, and on the Claude members.
	creds["claude"] = appClaudeCred(name, config, secrets)
	if appUsesChatGPT(config) || appCodexViaProxy(name) || secrets.ChatGPT != nil {
		creds[appProviderChatGPT] = appChatGPTCred(secrets)
	}
	if secrets.Agy != nil {
		creds["agy"] = appAgyCred(secrets)
	}
	return creds, nil
}

// ---------------------------------------------------------------------------
// what the proxies saw

// appSigninEvent is a line of an events file this reads: the plan's usage
// on an answer, or a refusal that sent a role to OpenRouter.
type appSigninEvent struct {
	at                 time.Time
	what, kind         string // usage or fallback; claude or chatgpt
	reason             string // fallback's: signin, limit
	status             string // usage's: allowed, rejected
	weekly             float64
	weeklyReset, reset time.Time
}

func parseAppSigninEvent(line string) (appSigninEvent, bool) {
	fields := strings.Fields(line)
	if len(fields) < 4 {
		return appSigninEvent{}, false
	}
	at, err := time.Parse(time.RFC3339, fields[0])
	if err != nil {
		return appSigninEvent{}, false
	}
	event := appSigninEvent{at: at, what: fields[1], kind: fields[3], weekly: -1}
	switch {
	case event.what == "fallback" && len(fields) >= 5:
		event.reason = fields[4]
	case event.what == "usage":
		unix := func(text string) time.Time {
			if n, err := strconv.ParseInt(text, 10, 64); err == nil && n > 0 {
				return time.Unix(n, 0).UTC()
			}
			return time.Time{}
		}
		for _, field := range fields[4:] {
			key, value, _ := strings.Cut(field, "=")
			switch key {
			case "status":
				event.status = value
			case "weekly":
				if used, err := strconv.ParseFloat(value, 64); err == nil {
					event.weekly = used
				}
			case "weekly_reset":
				event.weeklyReset = unix(value)
			case "reset":
				event.reset = unix(value)
			}
		}
	default:
		return appSigninEvent{}, false
	}
	return event, true
}

// readAppSigninEvents reads the events files from where it left off. The
// first time, it starts at their ends: what happened before is history.
func readAppSigninEvents(name string, state *appSigninHealthState, first bool) []appSigninEvent {
	var events []appSigninEvent
	entries, _ := os.ReadDir(appEventsDir(name))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".log") {
			continue
		}
		path := filepath.Join(appEventsDir(name), entry.Name())
		if first {
			if info, err := os.Stat(path); err == nil {
				state.Offsets[entry.Name()] = info.Size()
			}
			continue
		}
		network, _ := appReadNetwork(path, state.Offsets[entry.Name()], 100000)
		state.Offsets[entry.Name()] = network.To
		for _, line := range network.Lines {
			if event, ok := parseAppSigninEvent(line); ok {
				events = append(events, event)
			}
		}
	}
	sort.SliceStable(events, func(i, j int) bool { return events[i].at.Before(events[j].at) })
	return events
}

// apply takes an event the proxies wrote into the record.
func (h *appSigninHealth) apply(event appSigninEvent) {
	plan := map[string]string{"claude": "Claude", appProviderChatGPT: "ChatGPT"}[h.Kind]
	switch event.what {
	case "fallback":
		switch event.reason {
		case "signin":
			h.signedOut(plan+" refused the sign-in", event.at)
		case "limit":
			h.works(event.at)
			if !h.Limited || !h.LimitedUntil.After(event.at) {
				h.limit(true, time.Time{})
			}
		}
	case "usage":
		h.works(event.at)
		h.limit(event.status == "rejected", event.reset)
		if event.weekly >= 0 {
			h.weekly(event.weekly, event.weeklyReset)
		}
	}
}

// ---------------------------------------------------------------------------
// the checks

// signinCall makes a check's call: its status and up to 64 KB of its body.
func signinCall(method, url string, header map[string]string, body any) (int, []byte, error) {
	var reader io.Reader
	if body != nil {
		data, _ := json.Marshal(body)
		reader = bytes.NewReader(data)
	}
	request, err := http.NewRequest(method, url, reader)
	if err != nil {
		return 0, nil, err
	}
	for name, value := range header {
		request.Header.Set(name, value)
	}
	response, err := signinClient.Do(request)
	if err != nil {
		return 0, nil, err
	}
	defer response.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	return response.StatusCode, data, nil
}

// checkClaude asks whether a Claude sign-in works. A sign-in of Claude
// Code's own reads its usage too; a setup-token may only count tokens
// (its scope is user:inference), which costs nothing and says it works.
func checkClaude(cred appSigninCred, h *appSigninHealth, now time.Time) error {
	header := map[string]string{"Authorization": "Bearer " + cred.token, "anthropic-beta": "oauth-2025-04-20",
		"anthropic-version": "2023-06-01", "Content-Type": "application/json", "User-Agent": "hi/" + version}
	if cred.full {
		status, data, err := signinCall(http.MethodGet, claudeAPIBase+"/api/oauth/usage", header, nil)
		if err != nil {
			return err
		}
		if status == http.StatusUnauthorized {
			h.signedOut("Claude refused the sign-in", now)
			return nil
		}
		if status == http.StatusOK {
			var usage struct {
				FiveHour *struct {
					Utilization float64   `json:"utilization"`
					Resets      time.Time `json:"resets_at"`
				} `json:"five_hour"`
				SevenDay *struct {
					Utilization float64   `json:"utilization"`
					Resets      time.Time `json:"resets_at"`
				} `json:"seven_day"`
			}
			if json.Unmarshal(data, &usage) == nil {
				h.works(now)
				if usage.SevenDay != nil {
					h.weekly(usage.SevenDay.Utilization/100, usage.SevenDay.Resets.UTC())
				}
				switch {
				case usage.SevenDay != nil && usage.SevenDay.Utilization >= 100:
					h.limit(true, usage.SevenDay.Resets.UTC())
				case usage.FiveHour != nil && usage.FiveHour.Utilization >= 100:
					h.limit(true, usage.FiveHour.Resets.UTC())
				default:
					h.limit(false, time.Time{})
				}
				return nil
			}
		}
		// Not a usage it can read: count tokens instead.
	}
	status, data, err := signinCall(http.MethodPost, claudeAPIBase+"/v1/messages/count_tokens", header,
		map[string]any{"model": "claude-haiku-4-5", "messages": []map[string]string{{"role": "user", "content": "hi"}}})
	switch {
	case err != nil:
		return err
	case status == http.StatusOK:
		h.works(now)
	case status == http.StatusUnauthorized:
		h.signedOut("Claude refused the sign-in", now)
	default:
		return fmt.Errorf("Claude answered %d: %s", status, qFirstLine(string(data), ""))
	}
	return nil
}

// checkChatGPT reads a ChatGPT sign-in's usage, as Codex's /status does.
func checkChatGPT(cred appSigninCred, h *appSigninHealth, now time.Time) error {
	status, data, err := signinCall(http.MethodGet, chatGPTBackend+"/backend-api/wham/usage", map[string]string{"Authorization": "Bearer " + cred.token,
		"ChatGPT-Account-Id": cred.account, "User-Agent": "codex_cli_rs/0.164.0 (hi " + version + ")", "originator": "codex_cli_rs"}, nil)
	switch {
	case err != nil:
		return err
	case status == http.StatusUnauthorized:
		h.signedOut("ChatGPT refused the sign-in", now)
		return nil
	case status != http.StatusOK:
		return fmt.Errorf("ChatGPT answered %d: %s", status, qFirstLine(string(data), ""))
	}
	type window struct {
		Used    float64 `json:"used_percent"`
		Seconds int64   `json:"limit_window_seconds"`
		ResetAt int64   `json:"reset_at"`
	}
	var usage struct {
		Plan      string `json:"plan_type"`
		RateLimit struct {
			Allowed   *bool   `json:"allowed"`
			Reached   bool    `json:"limit_reached"`
			Primary   *window `json:"primary_window"`
			Secondary *window `json:"secondary_window"`
		} `json:"rate_limit"`
	}
	if err := json.Unmarshal(data, &usage); err != nil {
		return fmt.Errorf("ChatGPT's usage: %v", err)
	}
	h.works(now)
	if usage.Plan != "" {
		h.Plan = "ChatGPT " + firstNonEmpty(map[string]string{"prolite": "Pro Lite", "edu": "Edu"}[usage.Plan], titleWord(usage.Plan))
	}
	limits := usage.RateLimit
	var until time.Time
	for _, w := range []*window{limits.Primary, limits.Secondary} {
		if w == nil {
			continue
		}
		reset := time.Unix(w.ResetAt, 0).UTC()
		if w.Seconds >= 6*24*3600 {
			h.weekly(w.Used/100, reset)
		}
		if w.Used >= 100 && reset.After(until) {
			until = reset
		}
	}
	h.limit(limits.Reached || limits.Allowed != nil && !*limits.Allowed, until)
	return nil
}

// errAgyNoBox is the dev box not running: agy can't be asked now.
var errAgyNoBox = errors.New("the dev box isn't running")

// checkAgy runs agy models in the dev box, which fails signed out and
// refreshes agy's token there, then reads Google's quota summary with it.
// agy's quota covers the other makers' models it offers (Claude, GPT),
// not Gemini's.
func checkAgy(name string, h *appSigninHealth, now time.Time) error {
	out, err := appDevBoxExec(name, "sh", "-c", "test -x /box/bin/agy || exit 97; cd /tmp && exec timeout 60 /box/bin/agy models")
	if err != nil {
		text := strings.ToLower(out + " " + err.Error())
		switch {
		case strings.Contains(text, "sign in") || strings.Contains(text, "not logged in"):
			h.signedOut("agy isn't signed in", now)
			return nil
		case strings.Contains(text, "no such container") || strings.Contains(text, "not running") || strings.Contains(text, "exit status 97") || strings.Contains(text, "can only"):
			return errAgyNoBox
		}
		return fmt.Errorf("agy models: %s", qFirstLine(strings.TrimSpace(out), err.Error()))
	}
	h.works(now)
	data, err := boxedReadFile(appDevHome(name), filepath.Join(appDevHome(name), filepath.FromSlash(appDevAgyHome), "antigravity-oauth-token"))
	if err != nil {
		return fmt.Errorf("agy's token in the dev box: %v", err)
	}
	var token struct {
		Token struct {
			Access string `json:"access_token"`
		} `json:"token"`
	}
	if json.Unmarshal(data, &token) != nil || token.Token.Access == "" {
		return errors.New("agy's token in the dev box can't be read")
	}
	// Google answers only agy's own User-Agent, and an empty request.
	header := map[string]string{"Authorization": "Bearer " + token.Token.Access, "Content-Type": "application/json", "User-Agent": "antigravity-cli"}
	if status, body, err := signinCall(http.MethodPost, agyQuotaBase+"/v1internal:loadCodeAssist", header,
		map[string]any{"metadata": map[string]string{"ideType": "IDE_UNSPECIFIED", "platform": "PLATFORM_UNSPECIFIED", "pluginType": "GEMINI"}}); err == nil && status == http.StatusOK {
		var tiers struct {
			Current *struct{ Name string } `json:"currentTier"`
			Paid    *struct{ Name string } `json:"paidTier"`
		}
		if json.Unmarshal(body, &tiers) == nil {
			switch {
			case tiers.Paid != nil && tiers.Paid.Name != "":
				h.Plan = tiers.Paid.Name
			case tiers.Current != nil && tiers.Current.Name != "":
				h.Plan = tiers.Current.Name
			}
		}
	}
	status, body, err := signinCall(http.MethodPost, agyQuotaBase+"/v1internal:retrieveUserQuotaSummary", header, map[string]any{})
	switch {
	case err != nil:
		return err
	case status != http.StatusOK:
		return fmt.Errorf("Google's quota summary answered %d: %s", status, qFirstLine(string(body), ""))
	}
	var summary struct {
		Groups []struct {
			Buckets []struct {
				Window    string    `json:"window"`
				Remaining float64   `json:"remainingFraction"`
				Reset     time.Time `json:"resetTime"`
			} `json:"buckets"`
		} `json:"groups"`
	}
	if err := json.Unmarshal(body, &summary); err != nil {
		return fmt.Errorf("Google's quota summary: %v", err)
	}
	weeklyLeft, weeklyReset, found := 1.0, time.Time{}, false
	var until time.Time
	for _, group := range summary.Groups {
		for _, bucket := range group.Buckets {
			if bucket.Window == "weekly" && (!found || bucket.Remaining < weeklyLeft) {
				weeklyLeft, weeklyReset, found = bucket.Remaining, bucket.Reset.UTC(), true
			}
			if bucket.Remaining <= 0 && bucket.Reset.After(until) {
				until = bucket.Reset.UTC()
			}
		}
	}
	if found {
		h.weekly(1-weeklyLeft, weeklyReset)
	}
	h.limit(!until.IsZero(), until)
	return nil
}

// ---------------------------------------------------------------------------
// keeping it

// keepSigninHealth brings the app's records up to date: what its proxies
// wrote since, and the checks that are due. It runs each minute in the
// app's service.
func (b *appBroker) keepSigninHealth(now time.Time, stderr io.Writer) {
	updateAppSigninHealth(b.team, now, stderr)
	b.refreshDevProxy(now, stderr)
}

func updateAppSigninHealth(name string, now time.Time, stderr io.Writer) {
	creds, err := appSigninCreds(name)
	if err != nil {
		// What was known stays until they can be read again (a sealed
		// vault says so itself).
		return
	}
	state, had := loadAppSigninHealth(name)
	for kind := range state.Signins {
		if _, ok := creds[kind]; !ok {
			delete(state.Signins, kind)
		}
	}
	for kind, cred := range creds {
		h := state.Signins[kind]
		if h == nil || h.Key != cred.key || h.Source != cred.source {
			// Another sign-in: what was known of the last one goes.
			h = &appSigninHealth{Kind: kind}
			state.Signins[kind] = h
		}
		h.Source, h.Key, h.Expires = cred.source, cred.key, cred.expires
	}
	for _, event := range readAppSigninEvents(name, &state, !had) {
		if h := state.Signins[event.kind]; h != nil {
			h.apply(event)
		}
	}
	for _, kind := range sortedKeys(creds) {
		cred, h := creds[kind], state.Signins[kind]
		every := signinCheckEvery[kind]
		if h.SignedOut != "" || cred.problem != "" {
			every = signinRecheck
		}
		if !h.Checked.IsZero() && now.Sub(h.Checked) < every {
			continue
		}
		var err error
		switch {
		case cred.problem != "":
			h.signedOut(cred.problem, now)
		case kind == "claude":
			err = checkClaude(cred, h, now)
		case kind == appProviderChatGPT:
			err = checkChatGPT(cred, h, now)
		case kind == "agy":
			err = checkAgy(name, h, now)
		}
		switch {
		case errors.Is(err, errAgyNoBox):
			// Asked again soon, for when the box is up; what was known stays.
			h.Checked = now.Add(signinRecheck - every)
		case err != nil:
			// Not answered: what was known stays, and it asks again soon.
			h.Checked = now.Add(signinRecheck - every)
			if stderr != nil {
				fmt.Fprintf(stderr, "hi: checking app %s's %s sign-in: %v\n", name, kind, err)
			}
		default:
			h.Checked = now
		}
	}
	for _, h := range state.Signins {
		h.settle(now)
	}
	if err := saveAppSigninHealth(name, state); err != nil && stderr != nil {
		fmt.Fprintf(stderr, "hi: app %s's sign-in health: %v\n", name, err)
	}
}

// appDevProxyFresh are the apps whose dev box's proxy runs this hi.
var appDevProxyFresh sync.Map

// refreshDevProxy restarts the dev box's proxy when it runs an older hi
// than the machine's, so what a new hi's proxy notes (the plans' usage)
// reaches a dev box that keeps running across updates. Only while no agent
// in the box is at work: a restart cuts the calls it is making.
func (b *appBroker) refreshDevProxy(now time.Time, stderr io.Writer) {
	// Once its proxy runs this hi, or the box isn't up (it starts with this
	// hi), there's nothing to do until the service runs another.
	if _, done := appDevProxyFresh.Load(b.team); done {
		return
	}
	executable, err := os.Executable()
	if err != nil {
		return
	}
	if resolved, err := filepath.EvalSymlinks(executable); err == nil {
		executable = resolved
	}
	info, err := os.Stat(executable)
	if err != nil {
		return
	}
	engine, _, err := detectBoxEngine()
	if err != nil {
		return
	}
	proxy := "hi-box-" + appDevBox(b.team) + "-proxy"
	if engine.state(proxy) != "running" {
		appDevProxyFresh.Store(b.team, true)
		return
	}
	out, err := engine.output("container", "inspect", "--format", "{{.State.StartedAt}}", proxy)
	if err != nil {
		return
	}
	started := time.Time{}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999999 -0700 MST"} {
		if t, err := time.Parse(layout, strings.TrimSpace(out)); err == nil {
			started = t
			break
		}
	}
	if started.IsZero() || !info.ModTime().After(started) {
		appDevProxyFresh.Store(b.team, true)
		return
	}
	spaces, err := appDevSpacesCached(b.team)
	if err != nil {
		return
	}
	for _, space := range spaces {
		if space.Status == "working" {
			return
		}
	}
	if out, err := engine.output("restart", proxy); err != nil {
		fmt.Fprintf(stderr, "hi: giving app %s's dev box proxy the new hi: %s\n", b.team, qFirstLine(out, err.Error()))
		return
	}
	appDevProxyFresh.Store(b.team, true)
	fmt.Fprintf(stderr, "hi: app %s's dev box proxy runs hi %s now\n", b.team, version)
	if state, ok := loadAppSigninHealth(b.team); ok {
		state.ProxyRestarted = now
		saveAppSigninHealth(b.team, state)
	}
}
