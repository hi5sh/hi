package cli

// The apps' sign-in health on the admin page, and in the hi server's
// channel (docs/specs/approved/hi_app_signin_health.md): each app's
// service keeps the records (app_signin_health.go); the admin page shows
// them, and tells the server's channel when a sign-in breaks, has less
// than 10% of a weekly limit left, or is about to expire, once per
// credential, naming the apps that share it.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// adminSignin is a sign-in as the admin page shows it.
type adminSignin struct {
	Kind       string    `json:"kind"`
	Label      string    `json:"label"`
	Source     string    `json:"source"`
	Plan       string    `json:"plan,omitempty"`
	State      string    `json:"state"`
	Why        string    `json:"why,omitempty"`
	WeeklyLeft *int      `json:"weekly_left,omitempty"`
	Resets     time.Time `json:"resets,omitzero"`
	Seen       time.Time `json:"seen,omitzero"`
	Fix        string    `json:"fix,omitempty"`
}

var signinLabels = map[string]string{"claude": "Claude", appProviderChatGPT: "ChatGPT", "agy": "agy"}

var signinOrder = []string{"claude", appProviderChatGPT, "agy"}

// adminSigninsOf are an app's sign-ins, as its service last recorded them.
func adminSigninsOf(app string) []adminSignin {
	state, ok := loadAppSigninHealth(app)
	signins := []adminSignin{}
	if !ok {
		return signins
	}
	for _, kind := range signinOrder {
		h := state.Signins[kind]
		if h == nil {
			continue
		}
		// As of now: a limit may have lifted since the service wrote it.
		h.settle(time.Now())
		signin := adminSignin{Kind: kind, Label: signinLabels[kind], Source: h.Source, Plan: h.Plan, State: h.State, Why: h.Why,
			WeeklyLeft: h.WeeklyLeft, Resets: h.Resets, Seen: h.Seen}
		if h.State == signinSignedOut || h.State == signinExpiring {
			signin.Fix = signinFix(app, kind, h.Source)
		}
		signins = append(signins, signin)
	}
	return signins
}

// signinFix says who can sign it in again, and how.
func signinFix(app, kind, source string) string {
	machine, slash := adminMachineName(), appSlashCommand(app)
	switch {
	case kind == "claude" && strings.HasPrefix(source, "shared/"):
		return "An admin gives " + source + " a new value on the admin page's Vault tab."
	case kind == "claude" && source == "machine":
		return fmt.Sprintf("An admin runs `hi agent token claude` on %s, or an owner gives the app a token of its own: `%s signin claude` in Slack.", machine, slash)
	case kind == "claude":
		return fmt.Sprintf("An owner gives it a new token: `%s signin claude` in Slack.", slash)
	case kind == appProviderChatGPT && source == "machine":
		return fmt.Sprintf("An admin runs `codex login` on %s, or an owner gives the app a sign-in of its own: `%s signin chatgpt` in Slack.", machine, slash)
	case kind == appProviderChatGPT:
		return fmt.Sprintf("An owner signs it in again: `%s signin chatgpt` in Slack.", slash)
	case kind == "agy" && source == "machine":
		return fmt.Sprintf("An admin runs `agy` on %s and signs in with Google, then restarts %s.", machine, app)
	}
	return fmt.Sprintf("An admin gives it a new sign-in on %s: `hi app signin %s agy --file <token>`.", machine, app)
}

// adminSigninWorst is an app's worst sign-in state, "" when all work or
// aren't known yet.
func adminSigninWorst(signins []adminSignin) string {
	worst := ""
	for _, signin := range signins {
		if signin.State == signinOK || signin.State == signinUnknown {
			continue
		}
		if worst == "" || signinWorse(signin.State, worst) {
			worst = signin.State
		}
	}
	return worst
}

// ---------------------------------------------------------------------------
// telling the server's channel

// adminSigninTold is what the channel last heard of a credential.
type adminSigninTold struct {
	State string    `json:"state"`
	Mark  string    `json:"mark,omitempty"` // which limit, week, or expiry
	At    time.Time `json:"at"`
	// Apps are the apps on it then: when it stopped and they move to
	// another sign-in, they are told of as working again.
	Apps []string `json:"apps,omitempty"`
}

func adminSigninToldPath() string { return filepath.Join(adminDir(), "signin-told.json") }

// keepSigninNews tells the server's channel of the apps' sign-ins, each
// minute.
func (s *adminServer) keepSigninNews(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		s.tellSignins(time.Now())
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// adminSigninGroup is a credential and the apps on it.
type adminSigninGroup struct {
	kind, key string
	worst     *appSigninHealth
	apps      []string
}

// tellSignins posts what changed: a sign-in that broke, ran low, or will
// expire, and one that signed out and works again.
func (s *adminServer) tellSignins(now time.Time) {
	dir := adminOrgDir()
	if dir == "" {
		return
	}
	slackConfig, err := loadSlackConfig(dir)
	if err != nil || slackConfig == nil || slackConfig.BotToken == "" || slackConfig.Channel == "" {
		return
	}
	groups := map[string]*adminSigninGroup{}
	current := map[string]*appSigninHealth{} // each app's sign-in of each kind, by app|kind
	for _, app := range adminAppNames() {
		state, ok := loadAppSigninHealth(app)
		if !ok {
			continue
		}
		for kind, h := range state.Signins {
			if h.Key == "" {
				continue
			}
			h.settle(now)
			current[app+"|"+kind] = h
			id := kind + "|" + h.Key
			group := groups[id]
			if group == nil {
				group = &adminSigninGroup{kind: kind, key: h.Key, worst: h}
				groups[id] = group
			}
			if signinWorse(h.State, group.worst.State) {
				group.worst = h
			}
			group.apps = append(group.apps, app)
		}
	}
	told := map[string]adminSigninTold{}
	if data, err := os.ReadFile(adminSigninToldPath()); err == nil {
		json.Unmarshal(data, &told)
	}
	changed := false
	for _, id := range sortedKeys(groups) {
		group := groups[id]
		sort.Strings(group.apps)
		h := group.worst
		if h.State == signinUnknown {
			continue
		}
		mark := adminSigninMark(h)
		before, known := told[id]
		text := ""
		switch {
		case h.State == signinOK && known && before.State == signinSignedOut:
			// Works again: said, since it was said it stopped.
			text = adminSigninMessage(group, now)
		case h.State == signinOK:
			// Fine, or a limit that lifted when it said it would: nothing to say.
			if !known || before.State != signinOK {
				told[id], changed = adminSigninTold{State: signinOK, At: now.UTC()}, true
			}
		case !known || before.State != h.State || before.Mark != mark:
			text = adminSigninMessage(group, now)
		}
		if text == "" {
			continue
		}
		if err := adminPost(slackConfig.BotToken, slackConfig.Channel, text); err != nil {
			s.logf("telling the server's channel of %s: %v", strings.Join(group.apps, ", "), err)
			continue
		}
		told[id], changed = adminSigninTold{State: h.State, Mark: mark, At: now.UTC(), Apps: group.apps}, true
	}
	// A credential no app uses any more is forgotten. One that was said to
	// have stopped, whose apps work on another now, is said to work again,
	// with what they use.
	for _, id := range sortedKeys(told) {
		if groups[id] != nil {
			continue
		}
		before := told[id]
		if before.State == signinSignedOut && len(before.Apps) > 0 {
			kind, _, _ := strings.Cut(id, "|")
			var uses []string
			waiting := false
			for _, app := range before.Apps {
				h := current[app+"|"+kind]
				switch {
				case h == nil:
					// Gone from the machine, or no longer using one.
				case h.State == signinUnknown:
					waiting = true
				case h.State == signinSignedOut || h.State == signinLimited:
					uses = nil
					waiting = true
				default:
					uses = append(uses, app+" uses "+signinSourceWords(h.Source))
				}
			}
			if waiting {
				continue // said when it's known
			}
			if len(uses) > 0 {
				text := fmt.Sprintf(":white_check_mark: *%s works again* on %s: %s.", signinLabels[kind], adminMachineName(), strings.Join(uses, "; "))
				if err := adminPost(slackConfig.BotToken, slackConfig.Channel, text); err != nil {
					s.logf("telling the server's channel of %s: %v", strings.Join(before.Apps, ", "), err)
					continue
				}
			}
		}
		delete(told, id)
		changed = true
	}
	if changed {
		data, _ := json.MarshalIndent(told, "", "  ")
		if err := os.MkdirAll(adminDir(), 0o700); err == nil {
			writeFileAtomic(adminSigninToldPath(), append(data, '\n'), 0o600)
		}
	}
}

// signinSourceWords says whose sign-in it is.
func signinSourceWords(source string) string {
	switch {
	case source == "own":
		return "its own sign-in"
	case source == "machine":
		return "the machine's sign-in"
	}
	return source
}

// adminSigninMark tells one limit, week, or expiry from the next, so each
// is said once.
func adminSigninMark(h *appSigninHealth) string {
	switch h.State {
	case signinLimited, signinLow:
		if !h.Resets.IsZero() {
			return h.Resets.UTC().Format(time.RFC3339)
		}
	case signinExpiring:
		return h.Expires.UTC().Format("2006-01-02")
	}
	return ""
}

// adminSigninMessage is what the server's channel hears of a credential.
func adminSigninMessage(group *adminSigninGroup, now time.Time) string {
	h := group.worst
	label := signinLabels[group.kind]
	apps := strings.Join(group.apps, ", ")
	if len(group.apps) > 1 {
		apps = strings.Join(group.apps[:len(group.apps)-1], ", ") + " and " + group.apps[len(group.apps)-1]
	}
	where := " for " + apps
	switch {
	case strings.HasPrefix(h.Source, "shared/"):
		where += " (" + h.Source + ")"
	case h.Source == "machine":
		where += " (the machine's sign-in)"
	}
	where += " on " + adminMachineName()
	plan := ""
	if h.Plan != "" {
		plan = " (" + h.Plan + ")"
	}
	when := func(t time.Time) string { return t.UTC().Format("Mon 2 Jan 15:04 UTC") }
	fallback := "Its agents fall back to OpenRouter, billed per token, where the app has a key."
	if group.kind == "agy" {
		fallback = "agy can't work in the dev box until then."
	}
	switch h.State {
	case signinSignedOut:
		return fmt.Sprintf(":warning: *%s stopped working*%s: %s. %s %s", label, where, h.Why, strings.Replace(fallback, "until then", "until it's signed in again", 1), signinFix(group.apps[0], group.kind, h.Source))
	case signinLimited:
		until := ""
		if !h.Resets.IsZero() {
			until = ", until " + when(h.Resets)
		}
		wait := fallback
		if group.kind == "agy" {
			wait = "agy waits until it lifts."
		}
		return fmt.Sprintf(":hourglass_flowing_sand: *%s reached its usage limit*%s%s%s. %s", label, plan, where, until, wait)
	case signinLow:
		left := 0
		if h.WeeklyLeft != nil {
			left = *h.WeeklyLeft
		}
		resets := ""
		if !h.Resets.IsZero() {
			resets = "; it resets " + when(h.Resets)
		}
		return fmt.Sprintf(":warning: *%s has %d%% of its weekly limit left*%s%s%s.", label, left, plan, where, resets)
	case signinExpiring:
		return fmt.Sprintf(":calendar: *%s's sign-in is about to end*%s: %s. %s", label, where, h.Why, signinFix(group.apps[0], group.kind, h.Source))
	}
	return fmt.Sprintf(":white_check_mark: *%s works again*%s.", label, where)
}
