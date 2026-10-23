package main

// Signing a team in again from Slack (docs/specs/approved/hi_team_subscriptions.md,
// release 4): /hi-<team> signin chatgpt|claude, by an owner, since a
// sign-in decides whose subscription pays. ChatGPT's sign-in is a device
// code, sent to that owner alone in a DM; the team's service waits for it
// and keeps the new sign-in. Claude has no device code: the owner gets a
// one-time page on the team's app, where they paste a token from claude
// setup-token, so it never goes through Slack.

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"html"
	"net/http"
	"strings"
	"sync"
	"time"
)

// teamSigninLasts is how long a sign-in from Slack waits for the owner.
const teamSigninLasts = 15 * time.Minute

// teamSignins are the sign-ins under way: at most one ChatGPT device code
// at a time, and Claude's one-time pages by their secret.
type teamSignins struct {
	mu      sync.Mutex
	chatgpt time.Time // when the device code under way runs out
	claude  map[string]teamClaudeSignin
}

type teamClaudeSignin struct {
	User string
	Ends time.Time
}

// chatGPTDeviceWaitFor is chatGPTDeviceWait; tests replace it.
var chatGPTDeviceWaitFor = chatGPTDeviceWait

// webSignin is /hi-<team> signin chatgpt|claude, from the plugin with the
// member ID Slack gave: what to tell the person who typed it.
func (b *teamBroker) webSignin(user, provider string) (string, error) {
	if !slackIDPattern.MatchString(user) {
		return "", fmt.Errorf("%q isn't a Slack member ID", user)
	}
	if provider != teamProviderChatGPT && provider != "claude" {
		return "", fmt.Errorf("sign in to chatgpt or claude: %s signin chatgpt", teamSlashCommand(b.team))
	}
	config, err := loadTeam(b.team)
	if err != nil {
		return "", err
	}
	if b.channel == nil {
		return "", fmt.Errorf("hi isn't connected to Slack, so it can't send you the sign-in")
	}
	if !containsString(config.Owners, user) {
		var owners []string
		for _, owner := range config.Owners {
			owners = append(owners, "<@"+owner+">")
		}
		return "", fmt.Errorf("only the team's owners may sign it in, since a sign-in decides whose subscription pays. Ask an owner: %s", strings.Join(owners, ", "))
	}
	if provider == teamProviderChatGPT {
		return b.signinChatGPT(user)
	}
	return b.signinClaude(user)
}

// signinChatGPT DMs the owner a device code and waits for them in the
// background; the DM says how it went.
func (b *teamBroker) signinChatGPT(user string) (string, error) {
	b.signins.mu.Lock()
	if time.Now().Before(b.signins.chatgpt) {
		b.signins.mu.Unlock()
		return "", fmt.Errorf("a ChatGPT sign-in is already waiting for its code; it runs out within %d minutes", int(teamSigninLasts.Minutes()))
	}
	b.signins.chatgpt = time.Now().Add(teamSigninLasts)
	b.signins.mu.Unlock()
	done := func() {
		b.signins.mu.Lock()
		b.signins.chatgpt = time.Time{}
		b.signins.mu.Unlock()
	}
	device, err := chatGPTDeviceStart()
	if err != nil {
		done()
		return "", err
	}
	text := fmt.Sprintf(":key: To sign the *%s* team in to ChatGPT, open <%s> on any device, signed in to the ChatGPT account that should pay, and type *%s*. The code works for %d minutes. You asked for this with %s signin chatgpt; if you didn't, ignore it.",
		teamBotName(b.team), chatGPTDeviceURL(), device.Code, int(teamSigninLasts.Minutes()), teamSlashCommand(b.team))
	dm, ts, err := b.channel.dm(user, text)
	if err != nil {
		done()
		return "", fmt.Errorf("Slack wouldn't take the DM (%v); the team's Slack app needs the im:write scope, from hi team manifest %s", err, b.team)
	}
	go func() {
		defer done()
		grant, err := chatGPTDeviceWaitFor(device, time.Now)
		if err == nil {
			err = updateTeamSecrets(b.team, func(secrets *teamSecrets) error {
				secrets.ChatGPT = &grant
				return nil
			})
		}
		if err == nil {
			err = prepareTeamChatGPT(b.team, time.Now())
		}
		if err != nil {
			b.channel.update(dm, ts, fmt.Sprintf(":x: The ChatGPT sign-in didn't finish: %s. %s signin chatgpt gives you a new code.", teamSlackEscape(err.Error()), teamSlashCommand(b.team)))
			return
		}
		b.channel.update(dm, ts, fmt.Sprintf(":white_check_mark: The *%s* team is signed in to ChatGPT: %s. Its roles on ChatGPT use it from their next call.", teamBotName(b.team), chatGPTPlanName(grant)))
	}()
	return "I've sent you a code in a DM. Type it at OpenAI within 15 minutes, and the team signs in.", nil
}

// signinClaude DMs the owner a one-time page on the team's app, where they
// paste a token from claude setup-token.
func (b *teamBroker) signinClaude(user string) (string, error) {
	base := ""
	for _, slot := range []string{teamSlotProduction, teamSlotPreview} {
		if state, ok := loadTeamSlot(b.team, slot); ok && state.URL != "" {
			if _, exposed := b.exposed(state.Expose); exposed {
				base, _, _ = strings.Cut(state.URL, "?")
				break
			}
		}
	}
	if base == "" {
		return "", fmt.Errorf("the team's app has no address yet, so there is no page to paste the token on; an admin runs hi team signin %s claude on the team's machine", b.team)
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	id := base64.RawURLEncoding.EncodeToString(raw)
	b.signins.mu.Lock()
	if b.signins.claude == nil {
		b.signins.claude = map[string]teamClaudeSignin{}
	}
	now := time.Now()
	for other, signin := range b.signins.claude {
		if now.After(signin.Ends) || signin.User == user {
			delete(b.signins.claude, other)
		}
	}
	b.signins.claude[id] = teamClaudeSignin{User: user, Ends: now.Add(teamSigninLasts)}
	b.signins.mu.Unlock()
	link := strings.TrimSuffix(base, "/") + teamHelperPrefix + "signin/" + id
	text := fmt.Sprintf(":key: To give the *%s* team a Claude token: run `claude setup-token` on any computer with Claude Code, signed in to the Claude account that should pay, and paste the token it prints on <%s|this page>. The page works once, for %d minutes; the token never goes through Slack. You asked for this with %s signin claude; if you didn't, ignore it.",
		teamBotName(b.team), link, int(teamSigninLasts.Minutes()), teamSlashCommand(b.team))
	if _, _, err := b.channel.dm(user, text); err != nil {
		return "", fmt.Errorf("Slack wouldn't take the DM (%v); the team's Slack app needs the im:write scope, from hi team manifest %s", err, b.team)
	}
	return "I've sent you a page in a DM, where you paste the token. It works once, for 15 minutes.", nil
}

// claudeSigninPage is the one-time page: a form, and what it takes.
func (b *teamBroker) claudeSigninPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'")
	id := r.PathValue("id")
	b.signins.mu.Lock()
	signin, ok := b.signins.claude[id]
	if ok && time.Now().After(signin.Ends) {
		delete(b.signins.claude, id)
		ok = false
	}
	b.signins.mu.Unlock()
	page := func(status int, body string) {
		w.WriteHeader(status)
		fmt.Fprintf(w, `<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>Claude token</title>
<style>body{font:16px system-ui,sans-serif;max-width:34rem;margin:3rem auto;padding:0 1rem;color:#222}input{width:100%%;font:inherit;padding:.5rem;box-sizing:border-box}button{margin-top:1rem;font:inherit;padding:.5rem 1rem}p.note{color:#666}</style></head><body>%s</body></html>`, body)
	}
	if !ok {
		page(http.StatusNotFound, fmt.Sprintf("<h1>This page has ended</h1><p>It works once, for %d minutes. %s signin claude in the team's Slack channel gives you a new one.</p>", int(teamSigninLasts.Minutes()), html.EscapeString(teamSlashCommand(b.team))))
		return
	}
	if r.Method == http.MethodGet {
		page(http.StatusOK, fmt.Sprintf(`<h1>The %s team's Claude token</h1><p>Paste the token <code>claude setup-token</code> printed. It starts <code>sk-ant-</code> and lasts a year.</p>
<form method="post"><input name="token" type="password" autocomplete="off" required autofocus><button type="submit">Save</button></form><p class="note">Only the team's service sees it: no box, no chat, no Slack.</p>`, html.EscapeString(teamBotName(b.team))))
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	token := strings.TrimSpace(r.FormValue("token"))
	if err := teamCheckClaudeToken(token); err != nil {
		page(http.StatusBadRequest, "<h1>That isn't a Claude token</h1><p>"+html.EscapeString(err.Error())+`. <a href="">Try again</a>.</p>`)
		return
	}
	b.signins.mu.Lock()
	_, still := b.signins.claude[id]
	delete(b.signins.claude, id)
	b.signins.mu.Unlock()
	if !still {
		page(http.StatusNotFound, "<h1>This page has ended</h1>")
		return
	}
	if err := updateTeamSecrets(b.team, func(secrets *teamSecrets) error {
		secrets.ClaudeToken, secrets.ClaudeSignedIn = token, time.Now().UTC()
		return writeTeamKeys(b.team, *secrets)
	}); err != nil {
		page(http.StatusInternalServerError, "<h1>It wasn't saved</h1><p>"+html.EscapeString(err.Error())+"</p>")
		return
	}
	if b.channel != nil {
		b.channel.dm(signin.User, fmt.Sprintf(":white_check_mark: The *%s* team has its new Claude token. Its Claude members use it from their next call.", teamBotName(b.team)))
	}
	page(http.StatusOK, fmt.Sprintf("<h1>Saved</h1><p>The %s team's Claude members use it from their next call. You can close this page.</p>", html.EscapeString(teamBotName(b.team))))
}
