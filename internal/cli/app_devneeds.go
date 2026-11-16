package cli

// Needs you (docs/specs/approved/hi_app_dev_orca.md, release 3): an agent
// that waits for someone (a question, a permission) gets one DM once it
// has waited appDevNeedsWait with nobody active on the dev page: to
// whoever started its work, or the app's owners when hi doesn't know who
// did. The page's own inbox and notifications are for those on it
// (helper/dev/dev.js).

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// appDevNeedsWait is how long an agent waits before its DM, and
// appDevNeedsEvery how often the broker looks. Tests make them short.
var (
	appDevNeedsWait  = 10 * time.Minute
	appDevNeedsEvery = time.Minute
)

// appDevNeedsState is what the broker remembers of the waiting agents:
// since when each waits (when its hooks don't say), and those it sent a
// DM for, by the wait's start, so a new wait gets one again.
type appDevNeedsState struct {
	mu   sync.Mutex
	seen map[string]time.Time
	sent map[string]time.Time
}

// keepNeedsYou looks every appDevNeedsEvery until done.
func (b *appBroker) keepNeedsYou(done <-chan struct{}, stderr io.Writer) {
	ticker := time.NewTicker(appDevNeedsEvery)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			if err := b.checkNeedsYou(time.Now()); err != nil {
				fmt.Fprintf(stderr, "hi: the dev box's waiting agents: %v\n", err)
			}
		}
	}
}

// checkNeedsYou sends the DMs that are due. Only a dev box that runs has
// agents: one that doesn't answers nothing, and that's no error.
func (b *appBroker) checkNeedsYou(now time.Time) error {
	if b.channel == nil || appDevPageActive(b.team) {
		return nil
	}
	spaces, err := appDevSpaces(b.team)
	if err != nil {
		return nil
	}
	config, err := loadTeam(b.team)
	if err != nil {
		return err
	}
	needs := &b.devNeeds
	needs.mu.Lock()
	if needs.seen == nil {
		needs.seen, needs.sent = map[string]time.Time{}, map[string]time.Time{}
	}
	type due struct {
		space appDevSpace
		since time.Time
	}
	var send []due
	waiting := map[string]bool{}
	for _, space := range spaces {
		if space.Status != "blocked" {
			continue
		}
		key := space.ID + " " + firstNonEmpty(space.Branch, space.Label)
		waiting[key] = true
		since := space.Since
		if since.IsZero() {
			if needs.seen[key].IsZero() {
				needs.seen[key] = now
			}
			since = needs.seen[key]
		}
		if now.Sub(since) >= appDevNeedsWait && !needs.sent[key].Equal(since) {
			needs.sent[key] = since
			send = append(send, due{space, since})
		}
	}
	for key := range needs.seen {
		if !waiting[key] {
			delete(needs.seen, key)
		}
	}
	for key := range needs.sent {
		if !waiting[key] {
			delete(needs.sent, key)
		}
	}
	needs.mu.Unlock()
	var failed error
	for _, item := range send {
		text := appDevNeedsText(config, item.space, now.Sub(item.since))
		for _, user := range appDevNeedsWho(config, item.space) {
			if _, _, err := b.channel.dm(user, text); err != nil {
				failed = fmt.Errorf("a DM to %s: %w", user, err)
			}
		}
	}
	return failed
}

// appDevPageActive says someone has the dev page open and has used it in
// the last minutes: they see the agent waiting themselves.
func appDevPageActive(team string) bool {
	for _, person := range devPresenceOf(team).people("") {
		if !person.Idle {
			return true
		}
	}
	return false
}

// appDevNeedsWho is who a waiting agent's DM goes to: whoever started its
// work, else the owners.
func appDevNeedsWho(config appConfig, space appDevSpace) []string {
	if user := appDevStarterUsers(config.Name)[space.Branch]; space.Branch != "" && slackIDPattern.MatchString(user) {
		return []string{user}
	}
	var owners []string
	for _, owner := range config.Owners {
		if slackIDPattern.MatchString(owner) {
			owners = append(owners, owner)
		}
	}
	return owners
}

func appDevNeedsText(config appConfig, space appDevSpace, waited time.Duration) string {
	what := "an answer"
	if space.Waiting != "" {
		what = space.Waiting
	}
	name := firstNonEmpty(space.Branch, space.Label)
	text := fmt.Sprintf(":raised_hand: The agent in *%s*, in the %s app's dev box, has waited %d minutes for %s, and nobody's on the dev page.",
		name, appBotName(config.Name), int(waited.Minutes()), what)
	if link := appDevWorkLink(config.Name, name); link != "" {
		text += " " + link
	}
	return text
}

// appDevWorkLink is the dev page, open on a piece of work's chat: the
// page's own address, without a key; the person's browser has theirs.
func appDevWorkLink(team, name string) string {
	page, ok := loadTeamDevPage(team)
	if !ok || page.URL == "" {
		return ""
	}
	link, err := url.Parse(page.URL)
	if err != nil {
		return ""
	}
	link.RawQuery, link.Fragment = "", "work-"+name
	return link.String()
}

// ---------------------------------------------------------------------------
// who started each piece of work, by their Slack member ID

func appDevStarterUsersPath(team string) string {
	return filepath.Join(appDir(team), "run", "dev-start-users.json")
}

// appDevStarterUsers is each branch's starter's Slack member ID, beside
// their name in dev-starts.json: for the DM when its agent waits.
func appDevStarterUsers(team string) map[string]string {
	users := map[string]string{}
	if data, err := os.ReadFile(appDevStarterUsersPath(team)); err == nil {
		json.Unmarshal(data, &users)
	}
	return users
}

func appDevStartedUser(team, branch, user string) {
	if branch == "" || !slackIDPattern.MatchString(user) {
		return
	}
	appDevStartersMu.Lock()
	defer appDevStartersMu.Unlock()
	users := appDevStarterUsers(team)
	users[branch] = user
	if data, err := json.Marshal(users); err == nil {
		os.MkdirAll(filepath.Dir(appDevStarterUsersPath(team)), 0o700)
		writeFileAtomic(appDevStarterUsersPath(team), data, 0o600)
	}
}
