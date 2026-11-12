package cli

// The dev box's reach: its proxy allows what development needs, and the
// team's owners allow more, a site at a time. An agent that needs a site
// asks with hi dev allow, saying what for; the owners see the ask on the
// dev page and in the channel, and allow it with a tap, at once and for
// the box's next start too. Sites the box was refused without asking show
// there as well. A site inside the network (the machine, its LAN, NetBird's
// peers, a cloud's metadata) is never allowed, and the box's proxy checks
// the address it connects to, so an allowed name can't lead there.

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

// appDevAccess is what the owners allowed and what was asked of them, in
// the dev box's folder.
type appDevAccess struct {
	Allowed  []appDevSite    `json:"allowed,omitempty"`
	Requests []appDevSiteAsk `json:"requests,omitempty"`
	// Today is the day's post of what hi's check allowed by itself, and
	// CheckPaused why the check stopped until an owner turns it on.
	Today       *appDevCheckDay `json:"today,omitempty"`
	CheckPaused string          `json:"check_paused,omitempty"`
}

// appDevSite is a site an owner allowed.
type appDevSite struct {
	Host string    `json:"host"`
	Why  string    `json:"why,omitempty"`
	By   string    `json:"by,omitempty"`
	At   time.Time `json:"at"`
	// Check is on a site hi's check allowed, until an owner keeps it.
	Check *appDevSiteCheck `json:"check,omitempty"`
}

// appDevSiteAsk is an agent's ask for a site, and the owners' answer.
type appDevSiteAsk struct {
	Host    string    `json:"host"`
	Why     string    `json:"why,omitempty"`
	Work    string    `json:"work,omitempty"` // the branch it was for
	At      time.Time `json:"at"`
	State   string    `json:"state"` // asked, allowed, denied
	By      string    `json:"by,omitempty"`
	Decided time.Time `json:"decided,omitempty"`
	// Channel and Thread are where hi posted the ask, so the answer goes
	// in its thread.
	Channel string `json:"channel,omitempty"`
	Thread  string `json:"thread,omitempty"`
	// Key is in the ask's buttons alone, and an answer from the channel
	// has to bring it: the lead, which hears the click, can't make one up.
	Key string `json:"key,omitempty"`
	// Found is what hi's check found, and why it didn't allow the site.
	Found string `json:"found,omitempty"`
}

// appDevRefused is a site the box was refused without asking.
type appDevRefused struct {
	Host  string    `json:"host"`
	Times int       `json:"times"`
	Last  time.Time `json:"last"`
}

// appDevAccessView is the dev page's: the asks, what was refused lately,
// and what the owners allowed.
type appDevAccessView struct {
	Asks    []appDevSiteAsk `json:"asks,omitempty"`
	Refused []appDevRefused `json:"refused,omitempty"`
	Allowed []appDevSite    `json:"allowed,omitempty"`
}

// appDevSiteAnswer is what hi dev allow hears.
type appDevSiteAnswer struct {
	Host    string `json:"host"`
	State   string `json:"state"` // allowed, asked, denied
	Message string `json:"message"`
}

const (
	appDevSiteAsked   = "asked"
	appDevSiteAllowed = "allowed"
	appDevSiteDenied  = "denied"
	appDevWhyMax      = 300
	// appDevRefusedFor is how long a refusal shows on the dev page.
	appDevRefusedFor = 7 * 24 * time.Hour
)

var appDevAccessMu sync.Mutex

func appDevAccessPath(team string) string {
	return filepath.Join(appDevDir(team), "access.json")
}

// appDevAllowFile is the box's live allowlist, which its proxy rereads.
func appDevAllowFile(team string) string {
	return filepath.Join(appDevDir(team), "net", "allow")
}

func loadTeamDevAccess(team string) appDevAccess {
	var access appDevAccess
	if data, err := os.ReadFile(appDevAccessPath(team)); err == nil {
		json.Unmarshal(data, &access)
	}
	return access
}

func saveTeamDevAccess(team string, access appDevAccess) error {
	data, _ := json.MarshalIndent(access, "", "  ")
	return writeFileAtomic(appDevAccessPath(team), append(data, '\n'), 0o600)
}

// appDevAllowHosts is the box's allowlist: what development needs, what
// the owners allowed, the app's own Stage and dev page, the
// organisation's domains, and what an admin allowed for every app.
func appDevAllowHosts(team string) []string {
	hosts := appDevHosts()
	add := func(host string) {
		if host != "" && !slices.Contains(hosts, host) {
			hosts = append(hosts, host)
		}
	}
	now := time.Now()
	for _, site := range loadTeamDevAccess(team).Allowed {
		if site.Check == nil || now.Before(site.Check.Until) {
			add(site.Host)
		}
	}
	for _, host := range appDevMachineHosts() {
		add(host)
	}
	for _, host := range appDevOrgHosts() {
		add(host)
	}
	for _, host := range appDevOwnHosts(team) {
		add(host)
	}
	// Production stays an ask, also under the organisation's domain.
	for _, host := range appDevProductionHosts(team) {
		if !slices.Contains(hosts, host) && boxHostAllowed(hosts, host) {
			add("!" + host)
		}
	}
	return hosts
}

// writeTeamDevAllow rewrites the live allowlist, keeping what an admin
// added with hi box allow, without drop (unless the list still has it
// another way).
func writeTeamDevAllow(team string, drop ...string) error {
	keep := appDevAllowHosts(team)
	hosts := slices.Clone(keep)
	if data, err := os.ReadFile(appDevAllowFile(team)); err == nil {
		for _, host := range parseBoxAllowlist(string(data)) {
			// A refusal ("!name") is hi's alone, and only while it's needed.
			if !slices.Contains(hosts, host) && !strings.HasPrefix(host, "!") {
				hosts = append(hosts, host)
			}
		}
	}
	hosts = slices.DeleteFunc(hosts, func(host string) bool { return slices.Contains(drop, host) && !slices.Contains(keep, host) })
	if err := os.MkdirAll(filepath.Dir(appDevAllowFile(team)), 0o700); err != nil {
		return err
	}
	// hi's own lines are marked, so the box's proxy doesn't call them news.
	owners := appDevHosts()
	for _, site := range loadTeamDevAccess(team).Allowed {
		owners = append(owners, site.Host)
	}
	lines := make([]string, len(hosts))
	for i, host := range hosts {
		lines[i] = host
		if slices.Contains(keep, host) && !slices.Contains(owners, host) {
			lines[i] += "  " + boxAllowlistMark
		}
	}
	return writeFileAtomic(appDevAllowFile(team), []byte(strings.Join(lines, "\n")+"\n"), 0o600)
}

// appDevQuietHosts are refused on purpose and not shown as refusals: the
// agents' and herdr's own update checks and calls around the proxy's
// sign-ins, which an owner shouldn't be offered to allow.
var appDevQuietHosts = []string{"api.anthropic.com", "claude.ai", "anthropic.com", "downloads.claude.ai", "statsig.anthropic.com", "herdr.dev", "sentry.io"}

// appDevSiteHost is the site's name from what an agent or owner gave: a
// name, or an address with it.
func appDevSiteHost(text string) string {
	text = strings.ToLower(strings.TrimSpace(text))
	if strings.Contains(text, "://") {
		if u, err := url.Parse(text); err == nil {
			text = u.Hostname()
		}
	}
	host, _, _ := strings.Cut(text, "/")
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return strings.TrimSuffix(host, ".")
}

// appDevPrivateSuffixes are names that are a network's own.
var appDevPrivateSuffixes = []string{".local", ".internal", ".localhost", ".lan", ".home.arpa", ".intranet", ".corp", ".localdomain"}

// appDevLookup is a name's addresses, from this machine; tests replace it.
var appDevLookup = func(host string) ([]net.IP, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return boxLookupIP(ctx, host)
}

// appDevSiteRefusal says why a site can't be allowed: it isn't a site's
// name, or it is inside the network, as this machine sees it.
func appDevSiteRefusal(host string) error {
	if !boxDomainPattern.MatchString(host) || strings.HasPrefix(host, "*.") {
		return fmt.Errorf("%q isn't a site's name, such as query1.finance.yahoo.com", host)
	}
	for _, suffix := range appDevPrivateSuffixes {
		if strings.HasSuffix(host, suffix) {
			return fmt.Errorf("%s is a private network's name; the dev box never reaches the network it's on", host)
		}
	}
	ips, err := appDevLookup(host)
	if err != nil || len(ips) == 0 {
		return fmt.Errorf("%s doesn't resolve to an address on the internet", host)
	}
	for _, ip := range ips {
		if boxPrivateIP(ip) {
			return fmt.Errorf("%s is inside the network (%s); the dev box never reaches the machine or the network it's on", host, ip)
		}
	}
	return nil
}

// devSiteAsk is an agent's ask for a site: the owners hear of it once.
func (b *appBroker) devSiteAsk(host, why, work string) (appDevSiteAnswer, error) {
	host, why = appDevSiteHost(host), strings.TrimSpace(why)
	if why == "" {
		return appDevSiteAnswer{}, errors.New("say what the site is for: hi dev allow <site> --why \"<what for>\"")
	}
	if len(why) > appDevWhyMax {
		why = why[:appDevWhyMax]
	}
	if boxHostAllowed(appDevAllowHosts(b.team), host) {
		// The app's own addresses and the organisation's domains can come
		// after the box started: its list gets them now.
		if err := writeTeamDevAllow(b.team); err != nil {
			return appDevSiteAnswer{}, err
		}
		return appDevSiteAnswer{Host: host, State: appDevSiteAllowed, Message: "The box may reach " + host + " already."}, nil
	}
	if err := appDevSiteRefusal(host); err != nil {
		return appDevSiteAnswer{}, err
	}
	appDevAccessMu.Lock()
	access := loadTeamDevAccess(b.team)
	for i := range access.Requests {
		ask := &access.Requests[i]
		if ask.Host != host {
			continue
		}
		switch ask.State {
		case appDevSiteDenied:
			appDevAccessMu.Unlock()
			return appDevSiteAnswer{Host: host, State: appDevSiteDenied,
				Message: fmt.Sprintf("%s said no to %s on %s. Tell the user, and do without it, or ask them to allow it on the dev page.", firstNonEmpty(ask.By, "An owner"), host, ask.Decided.Format("Jan 2"))}, nil
		case appDevSiteAsked:
			ask.Why, ask.Work = why, firstNonEmpty(work, ask.Work)
			err := saveTeamDevAccess(b.team, access)
			appDevAccessMu.Unlock()
			return appDevSiteAnswer{Host: host, State: appDevSiteAsked, Message: "The owners were asked for " + host + " already; they see it on the dev page. hi dev allow " + host + " --wait waits for their answer."}, err
		}
	}
	appDevAccessMu.Unlock()
	// hi's check: a site the rules vouch for is allowed by itself.
	allowed, found := b.devSiteCheck(host, why, work)
	if allowed {
		return appDevSiteAnswer{Host: host, State: appDevSiteAllowed,
			Message: "Allowed by hi's check for 7 days: " + found + ". The box reaches " + host + " now, and its subdomains; the owners hear of it."}, nil
	}
	appDevAccessMu.Lock()
	access = loadTeamDevAccess(b.team)
	key := rand.Text()
	access.Requests = append(access.Requests, appDevSiteAsk{Host: host, Why: why, Work: work, At: time.Now().UTC(), State: appDevSiteAsked, Key: key, Found: found})
	err := saveTeamDevAccess(b.team, access)
	appDevAccessMu.Unlock()
	if err != nil {
		return appDevSiteAnswer{}, err
	}
	if channel, thread := b.devSiteTell(host, why, work, key); thread != "" {
		appDevAccessMu.Lock()
		access := loadTeamDevAccess(b.team)
		for i := range access.Requests {
			if ask := &access.Requests[i]; ask.Host == host && ask.State == appDevSiteAsked {
				ask.Channel, ask.Thread = channel, thread
			}
		}
		saveTeamDevAccess(b.team, access)
		appDevAccessMu.Unlock()
	}
	return appDevSiteAnswer{Host: host, State: appDevSiteAsked,
		Message: "Asked the app's owners for " + host + ": they see it on the dev page and in the channel. hi dev allow " + host + " --wait waits for their answer."}, nil
}

// A task's asks close together share one post: the next ones within
// appDevSiteJoinFor of its first join it, up to appDevSiteJoinMax (each
// has two buttons, and Slack takes 25 in a row).
const (
	appDevSiteJoinFor = 2 * time.Minute
	appDevSiteJoinMax = 8
)

// devSiteTell posts an ask in the channel, with the dev page's link, and
// says where: in the post of the task's ask a moment ago, when there is
// one.
func (b *appBroker) devSiteTell(host, why, work, key string) (channel, thread string) {
	ask := appDevSiteAsk{Host: host, Why: why, Work: work, State: appDevSiteAsked, Key: key}
	for _, known := range loadTeamDevAccess(b.team).Requests {
		if known.Host == host && known.Key == key {
			ask.Found = known.Found
		}
	}
	config, err := loadTeam(b.team)
	if err == nil && b.channel == nil {
		err = errors.New("hi isn't connected to Slack")
	}
	if err == nil && work != "" {
		access := loadTeamDevAccess(b.team)
		if channel, thread := appDevSiteJoinable(access, work, time.Now()); thread != "" {
			group := append(appDevSiteInThread(access, channel, thread), ask)
			if err := b.devSitePost(channel, thread, group); err == nil {
				return channel, thread
			} else {
				fmt.Fprintf(os.Stderr, "hi: adding %s to the dev box's ask in the channel: %v\n", host, err)
			}
		}
	}
	if err == nil {
		channel = config.opsChannel()
		text, buttons := appDevSitePost(b.team, []appDevSiteAsk{ask})
		thread, err = b.channel.postButtons(channel, text, buttons)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "hi: telling the channel the dev box asks for %s: %v\n", host, err)
		return "", ""
	}
	return channel, thread
}

// appDevSiteJoinable is the post a task's next ask joins: one of its
// asks' posts, begun within appDevSiteJoinFor, with room.
func appDevSiteJoinable(access appDevAccess, work string, now time.Time) (channel, thread string) {
	for _, ask := range access.Requests {
		if ask.Work != work || ask.State != appDevSiteAsked || ask.Thread == "" || ask.Channel == "" {
			continue
		}
		group := appDevSiteInThread(access, ask.Channel, ask.Thread)
		first := group[0].At
		for _, other := range group {
			if other.At.Before(first) {
				first = other.At
			}
		}
		if now.Sub(first) <= appDevSiteJoinFor && len(group) < appDevSiteJoinMax {
			return ask.Channel, ask.Thread
		}
	}
	return "", ""
}

// appDevSiteInThread are the asks one post holds.
func appDevSiteInThread(access appDevAccess, channel, thread string) []appDevSiteAsk {
	var asks []appDevSiteAsk
	for _, ask := range access.Requests {
		if ask.Channel == channel && ask.Thread == thread {
			asks = append(asks, ask)
		}
	}
	return asks
}

// devSitePost puts a post's asks as they stand now: buttons for those
// still open, and who answered the others.
func (b *appBroker) devSitePost(channel, thread string, asks []appDevSiteAsk) error {
	text, buttons := appDevSitePost(b.team, asks)
	if len(buttons) == 0 {
		return b.channel.settle(channel, thread, text)
	}
	return b.channel.redraw(channel, thread, text, buttons)
}

// appDevSitePost is a post's text and buttons, for its asks.
func appDevSitePost(team string, asks []appDevSiteAsk) (string, []appSlackButton) {
	var open []appDevSiteAsk
	for _, ask := range asks {
		if ask.State == appDevSiteAsked {
			open = append(open, ask)
		}
	}
	where := " An owner allows it here, or on the dev page."
	if len(asks) > 1 {
		where = " An owner allows them here, or on the dev page."
	}
	if link := appDevPageAt(team, "sites"); link != "" {
		where = strings.TrimSuffix(where, ".") + ": " + link
	}
	var text string
	if len(asks) == 1 {
		ask := asks[0]
		text = appDevSiteAskText(ask.Host, ask.Why, ask.Work)
		if ask.Found != "" && len(open) > 0 {
			text += "\n" + ask.Found
		}
		if len(open) == 0 {
			text += "\n" + appDevSiteAnswerText(ask.By, []string{ask.Host}, ask.State == appDevSiteAllowed)
		}
	} else {
		text = fmt.Sprintf(":computer: The dev box asks to reach %d sites", len(asks))
		if work := asks[0].Work; work != "" {
			text += " (for " + work + ")"
		}
		text += ":"
		for _, ask := range asks {
			text += "\n• *" + ask.Host + "*: " + ask.Why + "."
			if ask.Found != "" && ask.State == appDevSiteAsked {
				text += " _" + ask.Found + "_"
			}
			switch ask.State {
			case appDevSiteAllowed:
				text += " :white_check_mark: " + appSlackEscape(firstNonEmpty(ask.By, "An owner")) + " allowed it."
			case appDevSiteDenied:
				text += " :no_entry: " + appSlackEscape(firstNonEmpty(ask.By, "An owner")) + " said no."
			}
		}
	}
	if len(open) == 0 {
		return text, nil
	}
	if len(asks) > 1 {
		text += "\n"
	}
	text += where
	if len(asks) == 1 {
		ask := open[0]
		return text, []appSlackButton{
			{Action: "hi_site_allow", Value: ask.Host + " " + ask.Key, Label: "Allow", Primary: true, Confirm: appDevSiteConfirm(ask)},
			{Action: "hi_site_deny", Value: ask.Host + " " + ask.Key, Label: "Say no"},
		}
	}
	var buttons []appSlackButton
	var all []string
	for _, ask := range open {
		if appDevSiteConfirm(ask) == "" {
			all = append(all, ask.Host+" "+ask.Key)
		}
	}
	if len(open) > 1 && len(all) == len(open) {
		buttons = append(buttons, appSlackButton{Action: "hi_site_allow", Value: strings.Join(all, " "), Label: "Allow all", Primary: true})
	}
	for _, ask := range open {
		buttons = append(buttons,
			appSlackButton{Action: "hi_site_allow", Value: ask.Host + " " + ask.Key, Label: appDevSiteLabel("Allow ", ask.Host), Primary: len(open) == 1, Confirm: appDevSiteConfirm(ask)},
			appSlackButton{Action: "hi_site_deny", Value: ask.Host + " " + ask.Key, Label: appDevSiteLabel("Say no to ", ask.Host)})
	}
	return text, buttons
}

// appDevSiteConfirm is what Allow asks first, for a site a threat list
// names.
func appDevSiteConfirm(ask appDevSiteAsk) string {
	if strings.Contains(ask.Found, "lists it as harmful") {
		return "A threat list names " + ask.Host + " as harmful. Let the dev box reach it all the same?"
	}
	return ""
}

// appDevSiteLabel is a button's label, within Slack's 75 characters.
func appDevSiteLabel(say, host string) string {
	if len(say)+len(host) > 75 {
		host = "…" + host[len(host)-(74-len(say)):]
	}
	return say + host
}

// appDevSiteAnswerText says an owner's answer, for one site or several.
func appDevSiteAnswerText(by string, hosts []string, allow bool) string {
	by = appSlackEscape(firstNonEmpty(by, "An owner"))
	names := "*" + strings.Join(hosts, "*, *") + "*"
	if !allow {
		return fmt.Sprintf(":no_entry: %s said no to %s.", by, names)
	}
	if len(hosts) == 1 {
		return fmt.Sprintf(":white_check_mark: %s allowed %s: the dev box reaches it now.", by, names)
	}
	return fmt.Sprintf(":white_check_mark: %s allowed %s: the dev box reaches them now.", by, names)
}

// appDevSiteAskText is how the channel hears an ask for a site.
func appDevSiteAskText(host, why, work string) string {
	text := ":computer: The dev box asks to reach *" + host + "*"
	if work != "" {
		text += " (for " + work + ")"
	}
	return text + ": " + why + "."
}

// appDevPageAt is the dev page's address, without its key, at fragment.
func appDevPageAt(team, fragment string) string {
	page, ok := loadTeamDevPage(team)
	if !ok {
		return ""
	}
	link, err := url.Parse(page.URL)
	if err != nil {
		return ""
	}
	link.RawQuery, link.Fragment = "", fragment
	return link.String()
}

// devSiteState is where an ask for a site stands.
func (b *appBroker) devSiteState(host string) appDevSiteAnswer {
	host = appDevSiteHost(host)
	if boxHostAllowed(appDevAllowHosts(b.team), host) {
		return appDevSiteAnswer{Host: host, State: appDevSiteAllowed, Message: "The box may reach " + host + " now."}
	}
	for _, ask := range loadTeamDevAccess(b.team).Requests {
		if ask.Host == host {
			if ask.State == appDevSiteDenied {
				return appDevSiteAnswer{Host: host, State: appDevSiteDenied, Message: firstNonEmpty(ask.By, "An owner") + " said no to " + host + ". Tell the user, and do without it."}
			}
			return appDevSiteAnswer{Host: host, State: appDevSiteAsked, Message: "The owners haven't answered for " + host + " yet."}
		}
	}
	return appDevSiteAnswer{Host: host, State: "", Message: host + " isn't allowed, and nobody asked for it: hi dev allow " + host + " --why \"<what for>\"."}
}

// devAccessView is what the dev page shows of the box's reach.
func (b *appBroker) devAccessView() appDevAccessView {
	access := loadTeamDevAccess(b.team)
	view := appDevAccessView{Allowed: access.Allowed}
	known := map[string]bool{}
	for _, ask := range access.Requests {
		known[ask.Host] = true
		if ask.State == appDevSiteAsked {
			ask.Key = "" // the buttons' alone
			view.Asks = append(view.Asks, ask)
		}
	}
	hosts := appDevAllowHosts(b.team)
	refused := map[string]*appDevRefused{}
	since := time.Now().Add(-appDevRefusedFor)
	for _, line := range boxReadLines(filepath.Join(appDevDir(b.team), "net", "network.log"), 5000) {
		fields := strings.Fields(line)
		if len(fields) != 3 || fields[1] != "blocked" {
			continue
		}
		at, err := time.Parse(time.RFC3339, fields[0])
		host := appDevSiteHost(fields[2])
		if err != nil || at.Before(since) || known[host] || boxHostAllowed(hosts, host) || boxHostAllowed(appDevQuietHosts, host) || !boxDomainPattern.MatchString(host) {
			continue
		}
		if refused[host] == nil {
			refused[host] = &appDevRefused{Host: host}
		}
		refused[host].Times++
		refused[host].Last = at
	}
	for _, r := range refused {
		view.Refused = append(view.Refused, *r)
	}
	sort.Slice(view.Refused, func(i, j int) bool { return view.Refused[i].Last.After(view.Refused[j].Last) })
	if len(view.Refused) > 20 {
		view.Refused = view.Refused[:20]
	}
	return view
}

// devSiteDecide is an owner's answer: allow a site (asked for or not), or
// say no to it. The agent that asked hears it, unless its hi dev allow
// --wait is still waiting, and hears it there.
func (b *appBroker) devSiteDecide(person appPersonKey, host string, allow bool) error {
	return b.devSitesDecide(person, []string{host}, allow)
}

// devSitesDecide answers several at once, as Allow all does: the channel
// and each agent hear it once.
func (b *appBroker) devSitesDecide(person appPersonKey, hosts []string, allow bool) error {
	var decided []string
	works := map[string][]string{}
	var failed error
	for _, host := range hosts {
		host = appDevSiteHost(host)
		work, err := b.devSiteDecideLocked(person, host, allow)
		if err != nil {
			failed = cmpOr(failed, err)
			continue
		}
		decided = append(decided, host)
		if work != "" && !appDevSiteWaited(b.team, host) {
			works[work] = append(works[work], host)
		}
	}
	if len(decided) > 0 {
		b.devSiteAnswered(loadTeamDevAccess(b.team), decided, person.Name, allow)
	}
	for work, hosts := range works {
		if err := b.devSiteTellAgent(work, hosts, person.Name, allow); err != nil {
			fmt.Fprintf(os.Stderr, "hi: telling %s's agent about %s: %v\n", work, strings.Join(hosts, ", "), err)
		}
	}
	return failed
}

// webSite is an owner's Allow or Say no on the ask in the channel, which
// the lead's plugin hears: user is the member ID Slack gave the click.
func (b *appBroker) webSite(user, value string, allow bool) (string, error) {
	if !slackIDPattern.MatchString(user) {
		return "", fmt.Errorf("%q isn't a Slack member ID", user)
	}
	// One site and its ask's key, or several (Allow all): each pair has
	// to be an ask still open.
	fields := strings.Fields(value)
	requests := loadTeamDevAccess(b.team).Requests
	var hosts []string
	first := ""
	for i := 0; i+1 < len(fields); i += 2 {
		host, key := appDevSiteHost(fields[i]), fields[i+1]
		first = firstNonEmpty(first, host)
		if slices.ContainsFunc(requests, func(ask appDevSiteAsk) bool {
			return ask.Host == host && ask.State == appDevSiteAsked && ask.Key != "" && ask.Key == key
		}) && !slices.Contains(hosts, host) {
			hosts = append(hosts, host)
		}
	}
	if len(hosts) == 0 || len(fields)%2 != 0 {
		return "", fmt.Errorf("the ask for %s was answered already, or isn't this one; the dev page's Security says where it stands", firstNonEmpty(first, value))
	}
	config, err := loadTeam(b.team)
	if err != nil {
		return "", err
	}
	if !containsString(config.Owners, user) {
		return "", errors.New("only the app's owners allow sites for the dev box; ask one of them")
	}
	name := user
	if b.channel != nil {
		name = firstNonEmpty(b.channel.userName(user), user)
	}
	if err := b.devSitesDecide(appPersonKey{User: user, Name: name}, hosts, allow); err != nil {
		return "", err
	}
	if allow {
		return "Allowed " + strings.Join(hosts, ", ") + " for the dev box.", nil
	}
	return "Said no to " + strings.Join(hosts, ", ") + ".", nil
}

// appDevSiteWaits is when hi dev allow --wait last looked for a site's
// answer, by team and site.
var appDevSiteWaits sync.Map

func appDevSiteWaiting(team, host string) {
	appDevSiteWaits.Store(team+" "+appDevSiteHost(host), time.Now())
}

// appDevSiteWaited says an agent's hi dev allow --wait still waits for
// the site: it looked a moment ago.
func appDevSiteWaited(team, host string) bool {
	at, ok := appDevSiteWaits.Load(team + " " + host)
	return ok && time.Since(at.(time.Time)) < 3*appDevAllowEvery+time.Second
}

// devSiteTellAgent tells the agent at work on a branch the owners'
// answer for a site it asked for, as if typed in its chat, so it needn't
// be told by hand when it stopped waiting.
func (b *appBroker) devSiteTellAgent(work string, hosts []string, by string, allow bool) error {
	spaces, err := appDevSpaces(b.team)
	if err != nil {
		return err
	}
	for _, space := range spaces {
		if space.Branch != work {
			continue
		}
		agent, _, err := devChatAgent(b.team, space.ID)
		if err != nil || !agent.Running {
			return err
		}
		names := strings.Join(hosts, ", ")
		text := fmt.Sprintf("%s allowed %s: the dev box reaches it now, and its subdomains. Carry on with what needed it.", by, names)
		if len(hosts) > 1 {
			text = fmt.Sprintf("%s allowed %s: the dev box reaches them now, and their subdomains. Carry on with what needed them.", by, names)
		}
		if !allow {
			text = fmt.Sprintf("%s said no to %s: do without it, or tell me what you need it for.", by, names)
			if len(hosts) > 1 {
				text = fmt.Sprintf("%s said no to %s: do without them, or tell me what you need them for.", by, names)
			}
		}
		return appDevHerdrDo(b.team, "agent", "prompt", agent.Pane, text)
	}
	return nil
}

// devSiteDecideLocked records the answer, and says the branch the ask
// was for.
func (b *appBroker) devSiteDecideLocked(person appPersonKey, host string, allow bool) (string, error) {
	if allow {
		if err := appDevSiteRefusal(host); err != nil {
			return "", err
		}
	} else if !boxDomainPattern.MatchString(host) {
		return "", fmt.Errorf("%q isn't a site's name", host)
	}
	appDevAccessMu.Lock()
	defer appDevAccessMu.Unlock()
	access := loadTeamDevAccess(b.team)
	now := time.Now().UTC()
	why, work, found := "", "", false
	for i := range access.Requests {
		if ask := &access.Requests[i]; ask.Host == host {
			found, why = true, ask.Why
			if ask.State == appDevSiteAsked {
				work = ask.Work
			}
			ask.State, ask.By, ask.Decided = appDevSiteDenied, person.Name, now
			if allow {
				ask.State = appDevSiteAllowed
			}
		}
	}
	if !found && !allow {
		// A refusal nobody asked for: it stops showing.
		access.Requests = append(access.Requests, appDevSiteAsk{Host: host, At: now, State: appDevSiteDenied, By: person.Name, Decided: now})
	}
	if allow && !slices.ContainsFunc(access.Allowed, func(site appDevSite) bool { return site.Host == host }) {
		access.Allowed = append(access.Allowed, appDevSite{Host: host, Why: why, By: person.Name, At: now})
	}
	if err := saveTeamDevAccess(b.team, access); err != nil {
		return "", err
	}
	if !allow {
		return work, nil
	}
	return work, writeTeamDevAllow(b.team, "")
}

// devSiteAnswered says the owners' answer in each ask's post, once a
// post: the answered asks' buttons go, with who answered in their place.
func (b *appBroker) devSiteAnswered(access appDevAccess, hosts []string, by string, allow bool) {
	if b.channel == nil {
		return
	}
	type post struct{ channel, thread string }
	var posts []post
	answered := map[post][]string{}
	for _, ask := range access.Requests {
		if !slices.Contains(hosts, ask.Host) || ask.Thread == "" || ask.Channel == "" {
			continue
		}
		at := post{ask.Channel, ask.Thread}
		if _, ok := answered[at]; !ok {
			posts = append(posts, at)
		}
		if !slices.Contains(answered[at], ask.Host) {
			answered[at] = append(answered[at], ask.Host)
		}
	}
	for _, at := range posts {
		if err := b.devSitePost(at.channel, at.thread, appDevSiteInThread(access, at.channel, at.thread)); err != nil {
			fmt.Fprintf(os.Stderr, "hi: settling the ask for %s: %v\n", strings.Join(answered[at], ", "), err)
		}
		if _, err := b.channel.postIn(at.channel, at.thread, appDevSiteAnswerText(by, answered[at], allow)); err != nil {
			fmt.Fprintf(os.Stderr, "hi: telling the channel about %s: %v\n", strings.Join(answered[at], ", "), err)
		}
	}
}

// devSiteRemove takes a site the owners allowed off the box's list.
func (b *appBroker) devSiteRemove(host string) error {
	host = appDevSiteHost(host)
	appDevAccessMu.Lock()
	defer appDevAccessMu.Unlock()
	access := loadTeamDevAccess(b.team)
	before := len(access.Allowed)
	access.Allowed = slices.DeleteFunc(access.Allowed, func(site appDevSite) bool { return site.Host == host })
	if len(access.Allowed) == before {
		return fmt.Errorf("the owners didn't allow %s", host)
	}
	// Asked again, it is a new ask.
	access.Requests = slices.DeleteFunc(access.Requests, func(ask appDevSiteAsk) bool { return ask.Host == host })
	if err := saveTeamDevAccess(b.team, access); err != nil {
		return err
	}
	return writeTeamDevAllow(b.team, host)
}

// appDevAllowAdmin is hi app dev <app> allow, on the machine: the
// sites the owners allowed, or one allowed or removed as the dev page
// does, which the box's proxy takes at once.
func appDevAllowAdmin(team string, args []string, stdout io.Writer) error {
	usage := usageError{"usage: hi app dev <app> allow [<site> [--why <what for>] | --remove <site>]"}
	b := &appBroker{team: team}
	// The answer goes in the ask's thread, as from the dev page.
	if secrets, err := loadTeamSecrets(team); err == nil && secrets.SlackBotToken != "" {
		b.channel = newLiveTeamChannel(secrets.SlackBotToken)
	}
	switch {
	case len(args) == 0:
		access := loadTeamDevAccess(team)
		if len(access.Allowed) == 0 {
			fmt.Fprintf(stdout, "The owners allowed no sites beyond hi's own. Allow one: hi app dev %s allow <site> --why \"<what for>\"\n", team)
		}
		for _, site := range access.Allowed {
			fmt.Fprintf(stdout, "%s  %s, %s", site.Host, site.By, site.At.Local().Format("2006-01-02"))
			if site.Why != "" {
				fmt.Fprintf(stdout, ": %s", site.Why)
			}
			fmt.Fprintln(stdout)
		}
		for _, ask := range access.Requests {
			if ask.State == appDevSiteAsked {
				fmt.Fprintf(stdout, "%s  asked for: %s (hi app dev %s allow %s)\n", ask.Host, ask.Why, team, ask.Host)
			}
		}
		return nil
	case len(args) == 2 && args[0] == "--remove":
		if err := b.devSiteRemove(args[1]); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "The dev box no longer reaches %s.\n", appDevSiteHost(args[1]))
		return nil
	case len(args) != 1 && !(len(args) == 3 && args[1] == "--why"), strings.HasPrefix(args[0], "-"):
		return usage
	}
	host := appDevSiteHost(args[0])
	person := appPersonKey{Name: firstNonEmpty(os.Getenv("USER"), "an admin") + " on the machine"}
	if err := b.devSiteDecide(person, host, true); err != nil {
		return err
	}
	if len(args) == 3 {
		appDevAccessMu.Lock()
		access := loadTeamDevAccess(team)
		for i := range access.Allowed {
			if access.Allowed[i].Host == host {
				access.Allowed[i].Why = args[2]
			}
		}
		err := saveTeamDevAccess(team, access)
		appDevAccessMu.Unlock()
		if err != nil {
			return err
		}
	}
	fmt.Fprintf(stdout, "The dev box reaches %s and its subdomains now, and a database's shards by that name too.\n", host)
	return nil
}

// appDevAllowWait is how long hi dev allow --wait waits, and how often it
// looks; tests shorten them.
var (
	appDevAllowWait  = 9 * time.Minute
	appDevAllowEvery = 3 * time.Second
)

// appDevAllowClient is hi dev allow, in the box.
func appDevAllowClient(args []string, stdout io.Writer, call func(method, path string, body, out any) error, branch func() (string, error)) error {
	usage := usageError{"usage: hi dev allow <site> --why <what for> [--wait]  |  hi dev allow [<site>] [--wait]"}
	var host, why string
	wait := false
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--why" && i+1 < len(args):
			i++
			why = args[i]
		case strings.HasPrefix(args[i], "--why="):
			why = strings.TrimPrefix(args[i], "--why=")
		case args[i] == "--wait":
			wait = true
		case strings.HasPrefix(args[i], "-") || host != "":
			return usage
		default:
			host = args[i]
		}
	}
	if host == "" {
		if why != "" || wait {
			return usage
		}
		var view appDevAccessView
		if err := call("GET", "/allow", nil, &view); err != nil {
			return err
		}
		printTeamDevAccess(view, stdout)
		return nil
	}
	var answer appDevSiteAnswer
	if why != "" {
		work, _ := branch()
		if err := call("POST", "/allow", map[string]string{"host": host, "why": why, "work": work}, &answer); err != nil {
			return err
		}
	} else if err := call("GET", "/allow?host="+url.QueryEscape(host), nil, &answer); err != nil {
		return err
	}
	fmt.Fprintln(stdout, answer.Message)
	if !wait || answer.State != appDevSiteAsked {
		if answer.State == appDevSiteDenied {
			return errors.New("the owners said no")
		}
		return nil
	}
	deadline := time.Now().Add(appDevAllowWait)
	for time.Now().Before(deadline) {
		time.Sleep(appDevAllowEvery)
		if err := call("GET", "/allow?host="+url.QueryEscape(answer.Host), nil, &answer); err != nil {
			continue // the team's service restarting
		}
		switch answer.State {
		case appDevSiteAllowed:
			fmt.Fprintln(stdout, "Allowed: the box reaches "+answer.Host+" now.")
			return nil
		case appDevSiteDenied:
			fmt.Fprintln(stdout, answer.Message)
			return errors.New("the owners said no")
		}
	}
	return fmt.Errorf("no answer from the owners for %s yet; tell the user it waits on the dev page, and run hi dev allow %s --wait again when they say", answer.Host, answer.Host)
}

func printTeamDevAccess(view appDevAccessView, stdout io.Writer) {
	fmt.Fprintln(stdout, "The box reaches what development needs (GitHub, npm, PyPI, Go modules, crates.io, Hugging Face, the agents' services).")
	if len(view.Allowed) > 0 {
		fmt.Fprintln(stdout, "The owners allowed:")
		for _, site := range view.Allowed {
			fmt.Fprintf(stdout, "  %s%s\n", site.Host, appDevParen(site.Why))
		}
	}
	if len(view.Asks) > 0 {
		fmt.Fprintln(stdout, "Asked, waiting for the owners:")
		for _, ask := range view.Asks {
			fmt.Fprintf(stdout, "  %s%s\n", ask.Host, appDevParen(ask.Why))
		}
	}
	if len(view.Refused) > 0 {
		fmt.Fprintln(stdout, "Refused lately, not asked for:")
		for _, r := range view.Refused {
			fmt.Fprintf(stdout, "  %s (%d times)\n", r.Host, r.Times)
		}
	}
}

func appDevParen(text string) string {
	if text == "" {
		return ""
	}
	return ": " + text
}

// appDevKeyStatus is one of the team's keys or sign-ins, for the dev
// page's Security view: whose it is, and the command that changes it.
type appDevKeyStatus struct {
	Name    string `json:"name"`
	State   string `json:"state"`
	Command string `json:"command"`
}

// appDevKeys is what the team's keys and sign-ins are, from its secrets,
// without their values.
func appDevKeys(team string) []appDevKeyStatus {
	secrets, err := loadTeamSecrets(team)
	if err != nil {
		return nil
	}
	own := func(set bool, yes, no string) string {
		if set {
			return yes
		}
		return no
	}
	claude := "This machine's sign-in"
	if secrets.ClaudeToken != "" {
		claude = "The app's own token"
		if !secrets.ClaudeSignedIn.IsZero() {
			claude += ", since " + secrets.ClaudeSignedIn.Format("Jan 2, 2006")
		}
	}
	return []appDevKeyStatus{
		{Name: "OpenRouter key", State: own(secrets.OpenRouterAPIKey != "", "The app's own", "This machine's"), Command: "hi app key " + team + " openrouter"},
		{Name: "Claude", State: claude, Command: "hi app signin " + team + " claude"},
		{Name: "ChatGPT", State: own(secrets.ChatGPT != nil, "The app's own sign-in", "This machine's sign-in (Codex's)"), Command: "hi app signin " + team + " chatgpt"},
		{Name: "Slack tokens", State: own(secrets.SlackBotToken != "" && secrets.SlackAppToken != "", "Set", "Missing"), Command: "hi app key " + team + " slack"},
	}
}
