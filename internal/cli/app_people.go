package cli

import (
	"crypto/rand"
	"crypto/subtle"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	hi "github.com/hi5sh/hi"
)

// Who is talking in the helper: someone who may ask the team for work
// types /hi in the channel, and hi sends them a private link in a DM, with
// a key of their own. The relay keeps the key in a cookie only /__hi/
// gets and hands it on as X-Hi-User, so the helper knows who it is, as
// Slack does. Anyone else with the app's ordinary link gets help and
// feedback (app_feedback.go;
// docs/specs/approved/hi_app_helper_signin.md).

// The lead's Hermes gets a plugin that gives the channel /hi-<team>.
const appLeadPlugin = "hi-team"

// appSlashCommand is the team's command for a private link: one per
// team, since Slack gives a command two apps in a workspace share to the
// one installed last (and org server's app has /hi).
func appSlashCommand(team string) string {
	name := strings.Trim(regexp.MustCompile(`[^a-z0-9_-]+`).ReplaceAllString(strings.ToLower(team), "-"), "-")
	if len(name) > 28 {
		name = name[:28] // Slack's commands are 32 characters at most
	}
	return "/hi-" + name
}

var appLeadPluginManifest = hi.AppLeadPluginManifest

var appLeadPluginSource = hi.AppLeadPluginSource

// The lead's first reply to a new message in a DM, so Slack shows its
// status while it works (a Hermes hook).
var appLeadAckManifest = hi.AppLeadAckManifest

var appLeadAckSource = hi.AppLeadAckSource

// appKeyLasts is how long a person's key works; /hi again makes a new one
// and ends the old.
const appKeyLasts = 14 * 24 * time.Hour

// appPersonKey is one person's key, in people.json.
type appPersonKey struct {
	Key  string    `json:"key"`
	User string    `json:"user"` // their Slack member ID
	Name string    `json:"name"`
	Made time.Time `json:"made"`
	Ends time.Time `json:"ends"`
	// The DM with the links, which hi keeps current as addresses change.
	DM   string `json:"dm,omitempty"`
	DMTS string `json:"dm_ts,omitempty"`
}

// appPeopleFile is people.json, beside secrets.json: no box mounts it. The
// keys are kept as they are, not hashed, since hi writes them into the
// DM's links again whenever an address changes.
type appPeopleFile struct {
	Keys []appPersonKey `json:"keys"`
}

func appPeoplePath(team string) string { return filepath.Join(appDir(team), "people.json") }

func loadTeamPeople(team string) appPeopleFile {
	var people appPeopleFile
	if data, err := os.ReadFile(appPeoplePath(team)); err == nil {
		json.Unmarshal(data, &people)
	}
	return people
}

func saveTeamPeople(team string, people appPeopleFile) error {
	data, _ := json.MarshalIndent(people, "", "  ")
	return writeFileAtomic(appPeoplePath(team), append(data, '\n'), 0o600)
}

// appMayAsk says whether someone may ask the team for work: an owner, a
// developer, or a contributor. Being in the channel isn't enough.
func (b *appBroker) appMayAsk(config appConfig, user string) bool {
	return b.level(config, user) != appLevelNone
}

// webUser is the person a key from X-Hi-User belongs to. ended says the
// key was one, but isn't anymore: it ran out, a newer /hi replaced it, or
// its person may no longer ask for work.
func (b *appBroker) webUser(config appConfig, key string) (person appPersonKey, ok, ended bool) {
	if key == "" {
		return person, false, false
	}
	b.peopleMu.Lock()
	people := loadTeamPeople(b.team)
	b.peopleMu.Unlock()
	for _, candidate := range people.Keys {
		if subtle.ConstantTimeCompare([]byte(candidate.Key), []byte(key)) == 1 {
			person, ok = candidate, true
		}
	}
	if !ok {
		// Unknown: a key from before a newer /hi, or none hi made.
		return appPersonKey{}, false, true
	}
	if time.Now().After(person.Ends) {
		return appPersonKey{}, false, true
	}
	return person, true, false
}

// webLink is hi app web link --user <id>: a new key for someone who may
// ask for work, sent to them in a DM with the links. Their old key ends.
// The plugin behind /hi runs it with the ID Slack gave; the lead may run it
// too, and the links still go only to that person.
//
// dev asks for the dev page, which only owners get; an owner's DM has its
// link either way.
func (b *appBroker) webLink(user string, dev bool) (string, error) {
	if !slackIDPattern.MatchString(user) {
		return "", fmt.Errorf("%q isn't a Slack member ID", user)
	}
	config, err := loadTeam(b.team)
	if err != nil {
		return "", err
	}
	if b.channel == nil {
		return "", errors.New("hi isn't connected to Slack, so it can't send a link")
	}
	// Private links: the dev page's for owners and developers, and the
	// app's for everyone in the channel, whose helper then files what
	// they report as a task. Anyone else uses the app's ordinary link,
	// whose helper has help and feedback.
	level := b.level(config, user)
	switch {
	case dev && !appBuilds(config, user):
		return "", fmt.Errorf("the dev page is for the %s app's owners and developers; an owner makes someone a developer", appBotName(b.team))
	case level == appLevelNone:
		return fmt.Sprintf("Private links are for the %s app's contributors, developers, and owners; an owner adds contributors on the dev page. The app's own link works for everyone, with help and feedback in its helper.", appBotName(b.team)), nil
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	now := time.Now().UTC()
	key := appPersonKey{Key: base64.RawURLEncoding.EncodeToString(raw), User: user, Name: firstNonEmpty(b.channel.userName(user), user), Made: now, Ends: now.Add(appKeyLasts)}

	b.peopleMu.Lock()
	defer b.peopleMu.Unlock()
	people := loadTeamPeople(b.team)
	var kept []appPersonKey
	for _, old := range people.Keys {
		switch {
		case old.User == user:
			if old.DM != "" && now.Before(old.Ends) {
				b.channel.update(old.DM, old.DMTS, ":lock: These links have ended: you asked for new ones with "+appSlashCommand(b.team)+".")
			}
		case now.Before(old.Ends.Add(30 * 24 * time.Hour)):
			kept = append(kept, old)
		}
	}
	dm, ts, err := b.channel.dm(user, b.appLinkText(key))
	if err != nil {
		return "", fmt.Errorf("Slack wouldn't take the DM (%v); the app's Slack app needs the im:write scope, from hi app manifest %s", err, b.team)
	}
	key.DM, key.DMTS = dm, ts
	people.Keys = append(kept, key)
	if err := saveTeamPeople(b.team, people); err != nil {
		return "", err
	}
	if dev || level != appLevelContributor {
		return fmt.Sprintf("I've sent you your private link to the %s app's dev page in a DM, %s, with the app's. They work for %d days.", appBotName(b.team), key.Name, int(appKeyLasts.Hours()/24)), nil
	}
	return fmt.Sprintf("I've sent you your private links to the %s app in a DM, %s. They work for %d days.", appBotName(b.team), key.Name, int(appKeyLasts.Hours()/24)), nil
}

// devPageLinkFor is a new link to the dev page for an owner or developer,
// for the admin page to open: a key of their own, beside the ones /hi sent
// them, which keep working. Someone who doesn't build gets none.
func (b *appBroker) devPageLinkFor(user string) (string, error) {
	if !slackIDPattern.MatchString(user) && !appIsEmail(user) {
		return "", fmt.Errorf("%q isn't a Slack member ID", user)
	}
	config, err := loadTeam(b.team)
	if err != nil {
		return "", err
	}
	if !appBuilds(config, user) {
		return "", fmt.Errorf("the dev page is for the %s app's owners and developers", appBotName(b.team))
	}
	page, ok := loadTeamDevPage(b.team)
	if !ok || page.URL == "" {
		return "", errors.New("the dev page has no address yet; it has one a minute after the app starts")
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	now := time.Now().UTC()
	name := user
	if person, ok := loadMachinePeople().person(user); ok && person.Name != "" {
		name = person.Name
	} else if b.channel != nil && !appIsEmail(user) {
		name = firstNonEmpty(b.channel.userName(user), user)
	}
	key := appPersonKey{Key: base64.RawURLEncoding.EncodeToString(raw), User: user, Name: name, Made: now, Ends: now.Add(appKeyLasts)}
	b.peopleMu.Lock()
	defer b.peopleMu.Unlock()
	people := loadTeamPeople(b.team)
	people.Keys = slices.DeleteFunc(people.Keys, func(old appPersonKey) bool { return now.After(old.Ends.Add(30 * 24 * time.Hour)) })
	people.Keys = append(people.Keys, key)
	if err := saveTeamPeople(b.team, people); err != nil {
		return "", err
	}
	return appDevPageLink(page, key.Key), nil
}

// appLinkText is the DM with a person's links: preview's and production's,
// each with their key.
func (b *appBroker) appLinkText(key appPersonKey) string {
	var lines []string
	for _, slot := range []string{appSlotPreview, appSlotProduction} {
		state, ok := loadTeamSlot(b.team, slot)
		if !ok || state.URL == "" {
			continue
		}
		if _, exposed := b.exposed(state.Expose); !exposed {
			continue
		}
		link := state.URL
		if strings.Contains(link, "?") {
			link += "&"
		} else {
			link += "?"
		}
		link += netExposeUserParam + "=" + key.Key
		what := appAddressLabel(slot)
		if state.Label != "" {
			what += " (" + appSlackEscape(state.Label) + ")"
		}
		lines = append(lines, fmt.Sprintf("• %s: <%s|open the app>", what, link))
	}
	// Owners and developers build in the dev box: their DM has its page
	// too.
	builds := false
	if config, err := loadTeam(b.team); err == nil && appBuilds(config, key.User) {
		builds = true
		if page, ok := loadTeamDevPage(b.team); ok {
			if _, exposed := b.exposed(page.Expose); exposed {
				lines = append(lines, fmt.Sprintf("• Dev page: <%s|herdr, the tasks, and Release> (owners and developers only)", appDevPageLink(page, key.Key)))
			}
		}
	}
	text := fmt.Sprintf(":wave: Your private links for the *%s* app, %s: its dev page, where you build, and the app.\n", appBotName(b.team), appSlackEscape(key.Name))
	if !builds {
		text = fmt.Sprintf(":wave: Your private links to the *%s* app, %s. Signed in with them, the helper (the button in the corner) has Report: what you send there becomes a task for the people who build the app, with your name on it.\n", appBotName(b.team), appSlackEscape(key.Name))
	}
	if len(lines) == 0 {
		text += "The app has no address right now; this message gets the links when it does.\n"
	} else {
		text += strings.Join(lines, "\n") + "\n"
	}
	keep := "whoever opens them reports as you"
	if builds {
		keep = "whoever opens the dev page can build as you"
	}
	text += fmt.Sprintf("They work until <!date^%d^{date_short_pretty}|%s>, and hi updates them here when an address changes. Keep them to yourself: %s. %s gives you new ones and ends these. You can also write to the lead right here.",
		key.Ends.Unix(), key.Ends.Format("2 Jan"), keep, appSlashCommand(b.team))
	return text
}

// refreshLinks writes the links in each person's DM again: an address
// changed. It never fails what changed it.
func (b *appBroker) refreshLinks() {
	if b.channel == nil {
		return
	}
	b.peopleMu.Lock()
	defer b.peopleMu.Unlock()
	now := time.Now()
	for _, key := range loadTeamPeople(b.team).Keys {
		if key.DM != "" && now.Before(key.Ends) {
			b.channel.update(key.DM, key.DMTS, b.appLinkText(key))
		}
	}
}
