package cli

// What hi's check does with a site it allows by itself: for seven days,
// ten a day at most, said once a day in one post in the ops channel with
// Keep and Undo, marked on the dev page, and paused by trouble
// (hi_app_dev_sites.md, release 2).

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// appDevSiteCheck is on a site hi's check allowed, until an owner keeps
// it.
type appDevSiteCheck struct {
	Found string    `json:"found"` // what hi found: yahoo.com, top 1,000, 29 years old
	Until time.Time `json:"until"`
	// Key is in the daily post's buttons alone, as an ask's is.
	Key string `json:"key,omitempty"`
}

// appDevCheckDay is the day's post of what hi allowed by itself.
type appDevCheckDay struct {
	Day     string             `json:"day"` // 2026-11-12, the machine's day
	Channel string             `json:"channel,omitempty"`
	Thread  string             `json:"thread,omitempty"`
	Sites   []appDevCheckEntry `json:"sites"`
}

type appDevCheckEntry struct {
	Host  string `json:"host"`
	Found string `json:"found"`
	Why   string `json:"why,omitempty"`
	Work  string `json:"work,omitempty"`
	Key   string `json:"key"`
	State string `json:"state,omitempty"` // "", kept, or undone
	By    string `json:"by,omitempty"`
}

// appDevSitesCheckMode is the app's setting: rules (the default) or off.
func appDevSitesCheckMode(config appConfig) string {
	if config.SitesCheck == appSiteCheckOff {
		return appSiteCheckOff
	}
	return appSiteCheckRules
}

// devSiteCheckPaused says why the check stands aside now, or "".
func (b *appBroker) devSiteCheckPaused(access appDevAccess, config appConfig) string {
	if appDevSitesCheckMode(config) == appSiteCheckOff {
		return "it's off for this app"
	}
	if access.CheckPaused != "" {
		return "it's paused: " + access.CheckPaused
	}
	if stop := loadTeamWatchStop(b.team); stop.Frozen || stop.Paused {
		return "the app is frozen or paused"
	}
	box := appDevBox(b.team)
	for _, incident := range loadTeamIncidents(b.team) {
		if incident.Kind == "watch" && incident.Status != "closed" && strings.Contains(incident.Title, box) {
			return "an incident about the dev box is open (" + incident.ID + ")"
		}
	}
	today := time.Now().Format(time.DateOnly)
	if access.Today != nil && access.Today.Day == today && len(access.Today.Sites) >= appSiteCheckPerDay {
		return fmt.Sprintf("it allowed %d sites by itself today, as many as it may", appSiteCheckPerDay)
	}
	return ""
}

// devSiteCheck runs the check on a new ask. It allows the site by itself
// and says so, or says why not, which goes in the owners' post.
func (b *appBroker) devSiteCheck(host, why, work string) (allowed bool, found string) {
	config, err := loadTeam(b.team)
	if err != nil {
		return false, ""
	}
	if paused := b.devSiteCheckPaused(loadTeamDevAccess(b.team), config); paused != "" {
		if appDevSitesCheckMode(config) == appSiteCheckOff {
			return false, ""
		}
		return false, "hi's check didn't look: " + paused
	}
	now := time.Now()
	facts := appSiteCheckFacts(host, now)
	ok, because := facts.pass(now)
	appDevNetworkNote(b.team, "checked", host)
	if !ok {
		if facts.Listed != "" {
			// A site a threat list names stops the check until an owner
			// turns it on again: the agent may have been misled.
			appDevAccessMu.Lock()
			access := loadTeamDevAccess(b.team)
			access.CheckPaused = fmt.Sprintf("the dev box asked for %s, which %s lists as harmful", host, facts.Listed)
			saveTeamDevAccess(b.team, access)
			appDevAccessMu.Unlock()
			return false, ":warning: " + because + ". hi's check stops allowing sites by itself until an owner turns it on again (hi app dev " + b.team + " sites check rules)."
		}
		return false, "hi's check: not allowed by itself, " + because + " (" + facts.words(now) + ")."
	}

	key := rand.Text()
	found = facts.words(now)
	appDevAccessMu.Lock()
	access := loadTeamDevAccess(b.team)
	access.Allowed = append(access.Allowed, appDevSite{Host: host, Why: why, By: appSiteCheckBy, At: now.UTC(),
		Check: &appDevSiteCheck{Found: found, Until: now.Add(appSiteCheckFor).UTC(), Key: key}})
	access.Requests = slices.DeleteFunc(access.Requests, func(ask appDevSiteAsk) bool { return ask.Host == host })
	access.Requests = append(access.Requests, appDevSiteAsk{Host: host, Why: why, Work: work, At: now.UTC(), State: appDevSiteAllowed, By: appSiteCheckBy, Decided: now.UTC()})
	today := now.Format(time.DateOnly)
	if access.Today == nil || access.Today.Day != today {
		access.Today = &appDevCheckDay{Day: today}
	}
	access.Today.Sites = append(access.Today.Sites, appDevCheckEntry{Host: host, Found: found, Why: why, Work: work, Key: key})
	day := *access.Today
	err = saveTeamDevAccess(b.team, access)
	appDevAccessMu.Unlock()
	if err == nil {
		err = writeTeamDevAllow(b.team)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "hi: allowing %s by hi's check: %v\n", host, err)
		return false, ""
	}
	b.devCheckPost(day)
	return true, found
}

// appDevNetworkNote puts a line in the dev box's network log, beside the
// proxy's.
func appDevNetworkNote(team, decision, host string) {
	path := filepath.Join(appDevDir(team), "net", "network.log")
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer file.Close()
	fmt.Fprintf(file, "%s %s %s\n", time.Now().UTC().Format(time.RFC3339), decision, host)
}

// devCheckPost puts the day's post as it stands: one post a day, made
// with the day's first site and redrawn with each next one and each Keep
// or Undo.
func (b *appBroker) devCheckPost(day appDevCheckDay) {
	if b.channel == nil || len(day.Sites) == 0 {
		return
	}
	text, buttons := appDevCheckPostText(b.team, day)
	if day.Thread == "" {
		config, err := loadTeam(b.team)
		if err != nil {
			return
		}
		channel := config.opsChannel()
		thread, err := b.channel.postButtons(channel, text, buttons)
		if err != nil {
			fmt.Fprintf(os.Stderr, "hi: the day's post of sites hi allowed: %v\n", err)
			return
		}
		appDevAccessMu.Lock()
		access := loadTeamDevAccess(b.team)
		if access.Today != nil && access.Today.Day == day.Day {
			access.Today.Channel, access.Today.Thread = channel, thread
			saveTeamDevAccess(b.team, access)
		}
		appDevAccessMu.Unlock()
		return
	}
	var err error
	if len(buttons) == 0 {
		err = b.channel.settle(day.Channel, day.Thread, text)
	} else {
		err = b.channel.redraw(day.Channel, day.Thread, text, buttons)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "hi: the day's post of sites hi allowed: %v\n", err)
	}
}

// appDevCheckPostText is the day's post: a line for each site, and Keep
// and Undo for those nobody answered.
func appDevCheckPostText(team string, day appDevCheckDay) (string, []appSlackButton) {
	n := len(day.Sites)
	text := fmt.Sprintf(":robot_face: hi allowed %d site for the dev box by itself today, for 7 days, on its check:", n)
	if n != 1 {
		text = fmt.Sprintf(":robot_face: hi allowed %d sites for the dev box by itself today, for 7 days each, on its check:", n)
	}
	var buttons []appSlackButton
	for _, site := range day.Sites {
		text += "\n• *" + site.Host + "*: " + site.Found
		if site.Why != "" {
			text += ". Asked"
			if site.Work != "" {
				text += " for " + site.Work
			}
			text += ": " + site.Why
		}
		text += "."
		switch site.State {
		case "kept":
			text += " :white_check_mark: " + appSlackEscape(site.By) + " kept it."
		case "undone":
			text += " :no_entry: " + appSlackEscape(site.By) + " undid it."
		default:
			buttons = append(buttons,
				appSlackButton{Action: "hi_site_keep", Value: site.Host + " " + site.Key, Label: appDevSiteLabel("Keep ", site.Host)},
				appSlackButton{Action: "hi_site_undo", Value: site.Host + " " + site.Key, Label: appDevSiteLabel("Undo ", site.Host)})
		}
	}
	if len(buttons) > 0 {
		text += "\nKeep makes one an owner's allow, for good; Undo takes it off and says no."
		if link := appDevPageAt(team, "sites"); link != "" {
			text += " Or on the dev page: " + link
		}
	}
	return text, buttons
}

// devSiteKeep makes a site hi's check allowed an owner's, for good.
func (b *appBroker) devSiteKeep(person appPersonKey, host string) error {
	host = appDevSiteHost(host)
	appDevAccessMu.Lock()
	access := loadTeamDevAccess(b.team)
	i := slices.IndexFunc(access.Allowed, func(site appDevSite) bool { return site.Host == host && site.Check != nil })
	if i < 0 {
		appDevAccessMu.Unlock()
		return fmt.Errorf("hi's check didn't allow %s, or an owner kept or undid it already", host)
	}
	access.Allowed[i].By, access.Allowed[i].At, access.Allowed[i].Check = person.Name, time.Now().UTC(), nil
	day := appDevCheckAnswered(&access, host, "kept", person.Name)
	err := saveTeamDevAccess(b.team, access)
	appDevAccessMu.Unlock()
	if err != nil {
		return err
	}
	if day != nil {
		b.devCheckPost(*day)
	}
	return nil
}

// devSiteUndo takes a site hi's check allowed off, and records the
// owner's no, which hi's check never overrides.
func (b *appBroker) devSiteUndo(person appPersonKey, host string) error {
	host = appDevSiteHost(host)
	appDevAccessMu.Lock()
	access := loadTeamDevAccess(b.team)
	before := len(access.Allowed)
	access.Allowed = slices.DeleteFunc(access.Allowed, func(site appDevSite) bool { return site.Host == host && site.Check != nil })
	if len(access.Allowed) == before {
		appDevAccessMu.Unlock()
		return fmt.Errorf("hi's check didn't allow %s, or an owner kept or undid it already", host)
	}
	now := time.Now().UTC()
	access.Requests = slices.DeleteFunc(access.Requests, func(ask appDevSiteAsk) bool { return ask.Host == host })
	access.Requests = append(access.Requests, appDevSiteAsk{Host: host, At: now, State: appDevSiteDenied, By: person.Name, Decided: now})
	day := appDevCheckAnswered(&access, host, "undone", person.Name)
	err := saveTeamDevAccess(b.team, access)
	appDevAccessMu.Unlock()
	if err == nil {
		err = writeTeamDevAllow(b.team, host)
	}
	if err != nil {
		return err
	}
	if day != nil {
		b.devCheckPost(*day)
	}
	return nil
}

// appDevCheckAnswered marks a site's line in the day's post, when it is
// today's.
func appDevCheckAnswered(access *appDevAccess, host, state, by string) *appDevCheckDay {
	if access.Today == nil {
		return nil
	}
	for i := range access.Today.Sites {
		if site := &access.Today.Sites[i]; site.Host == host && site.State == "" {
			site.State, site.By = state, by
			day := *access.Today
			return &day
		}
	}
	return nil
}

// webSiteCheck is an owner's Keep or Undo in the day's post.
func (b *appBroker) webSiteCheck(user, value string, keep bool) (string, error) {
	if !slackIDPattern.MatchString(user) {
		return "", fmt.Errorf("%q isn't a Slack member ID", user)
	}
	host, key, _ := strings.Cut(strings.TrimSpace(value), " ")
	host = appDevSiteHost(host)
	if key == "" || !slices.ContainsFunc(loadTeamDevAccess(b.team).Allowed, func(site appDevSite) bool {
		return site.Host == host && site.Check != nil && site.Check.Key == key
	}) {
		return "", fmt.Errorf("%s was kept or undone already, or isn't this one; the dev page's Security says where it stands", host)
	}
	config, err := loadTeam(b.team)
	if err != nil {
		return "", err
	}
	if !containsString(config.Owners, user) {
		return "", errors.New("only the app's owners keep or undo what hi allowed; ask one of them")
	}
	name := user
	if b.channel != nil {
		name = firstNonEmpty(b.channel.userName(user), user)
	}
	person := appPersonKey{User: user, Name: name}
	if keep {
		if err := b.devSiteKeep(person, host); err != nil {
			return "", err
		}
		return "Kept " + host + " for the dev box.", nil
	}
	if err := b.devSiteUndo(person, host); err != nil {
		return "", err
	}
	return "Undid " + host + ": the dev box doesn't reach it now.", nil
}

// devSitesExpire takes off what hi's check allowed whose seven days are
// over, and nobody kept.
func (b *appBroker) devSitesExpire(now time.Time) {
	appDevAccessMu.Lock()
	access := loadTeamDevAccess(b.team)
	var gone []string
	access.Allowed = slices.DeleteFunc(access.Allowed, func(site appDevSite) bool {
		if site.Check != nil && now.After(site.Check.Until) {
			gone = append(gone, site.Host)
			return true
		}
		return false
	})
	if len(gone) == 0 {
		appDevAccessMu.Unlock()
		return
	}
	// Asked again, it is a new ask, and the check looks again.
	access.Requests = slices.DeleteFunc(access.Requests, func(ask appDevSiteAsk) bool {
		return slices.Contains(gone, ask.Host) && ask.State == appDevSiteAllowed && ask.By == appSiteCheckBy
	})
	err := saveTeamDevAccess(b.team, access)
	appDevAccessMu.Unlock()
	if err == nil {
		err = writeTeamDevAllow(b.team, gone...)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "hi: ending what hi's check allowed: %v\n", err)
	}
}

// keepDevSites ends what hi's check allowed when its days are over, and
// keeps the list of the most used sites fresh.
func (b *appBroker) keepDevSites(done <-chan struct{}, stderr io.Writer) {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()
	for {
		b.devSitesExpire(time.Now())
		if config, err := loadTeam(b.team); err == nil && appDevSitesCheckMode(config) != appSiteCheckOff {
			if err := appSiteKeepTranco(appSiteHTTP); err != nil {
				fmt.Fprintf(stderr, "hi: the list of the most used sites, for hi's check: %v\n", err)
			}
		}
		select {
		case <-done:
			return
		case <-ticker.C:
		}
	}
}

// appDevSitesCheckCommand is hi app dev <app> sites check [off|rules].
func appDevSitesCheckCommand(team string, args []string, stdout io.Writer) error {
	usage := usageError{"usage: hi app dev <app> sites check [off|rules]"}
	config, err := loadTeam(team)
	if err != nil {
		return err
	}
	if len(args) == 0 {
		access := loadTeamDevAccess(team)
		mode := appDevSitesCheckMode(config)
		if mode == appSiteCheckOff {
			fmt.Fprintf(stdout, "hi's check is off: every site the dev box asks for goes to the owners. On: hi app dev %s sites check rules\n", team)
		} else {
			fmt.Fprintln(stdout, "hi's check allows a site by itself for 7 days when it is among the 100,000 most used, over a year old, on no threat list, and nobody but its owner can make names or put files there; it goes to the owners otherwise.")
			if access.CheckPaused != "" {
				fmt.Fprintf(stdout, "Paused: %s. On again: hi app dev %s sites check rules\n", access.CheckPaused, team)
			}
		}
		var allowed []string
		for _, site := range access.Allowed {
			if site.Check != nil {
				allowed = append(allowed, fmt.Sprintf("  %s  until %s: %s", site.Host, site.Check.Until.Local().Format("2006-01-02"), site.Check.Found))
			}
		}
		if len(allowed) > 0 {
			fmt.Fprintln(stdout, "Allowed by hi's check now:\n"+strings.Join(allowed, "\n"))
		}
		return nil
	}
	if len(args) != 1 || (args[0] != appSiteCheckOff && args[0] != appSiteCheckRules) {
		return usage
	}
	config.SitesCheck = args[0]
	if args[0] == appSiteCheckRules {
		config.SitesCheck = "" // the default
	}
	if err := saveTeam(config); err != nil {
		return err
	}
	appDevAccessMu.Lock()
	access := loadTeamDevAccess(team)
	access.CheckPaused = ""
	err = saveTeamDevAccess(team, access)
	appDevAccessMu.Unlock()
	if err != nil {
		return err
	}
	if args[0] == appSiteCheckOff {
		fmt.Fprintln(stdout, "hi's check is off: every site the dev box asks for goes to the owners. What it allowed stays until its days are over.")
	} else {
		fmt.Fprintln(stdout, "hi's check is on: it allows a site the rules vouch for by itself, for 7 days, and says so in the ops channel once a day.")
	}
	return nil
}
