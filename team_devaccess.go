package main

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

// teamDevAccess is what the owners allowed and what was asked of them, in
// the dev box's folder.
type teamDevAccess struct {
	Allowed  []teamDevSite    `json:"allowed,omitempty"`
	Requests []teamDevSiteAsk `json:"requests,omitempty"`
}

// teamDevSite is a site an owner allowed.
type teamDevSite struct {
	Host string    `json:"host"`
	Why  string    `json:"why,omitempty"`
	By   string    `json:"by,omitempty"`
	At   time.Time `json:"at"`
}

// teamDevSiteAsk is an agent's ask for a site, and the owners' answer.
type teamDevSiteAsk struct {
	Host    string    `json:"host"`
	Why     string    `json:"why,omitempty"`
	Work    string    `json:"work,omitempty"` // the branch it was for
	At      time.Time `json:"at"`
	State   string    `json:"state"` // asked, allowed, denied
	By      string    `json:"by,omitempty"`
	Decided time.Time `json:"decided,omitempty"`
}

// teamDevRefused is a site the box was refused without asking.
type teamDevRefused struct {
	Host  string    `json:"host"`
	Times int       `json:"times"`
	Last  time.Time `json:"last"`
}

// teamDevAccessView is the dev page's: the asks, what was refused lately,
// and what the owners allowed.
type teamDevAccessView struct {
	Asks    []teamDevSiteAsk `json:"asks,omitempty"`
	Refused []teamDevRefused `json:"refused,omitempty"`
	Allowed []teamDevSite    `json:"allowed,omitempty"`
}

// teamDevSiteAnswer is what hi dev allow hears.
type teamDevSiteAnswer struct {
	Host    string `json:"host"`
	State   string `json:"state"` // allowed, asked, denied
	Message string `json:"message"`
}

const (
	teamDevSiteAsked   = "asked"
	teamDevSiteAllowed = "allowed"
	teamDevSiteDenied  = "denied"
	teamDevWhyMax      = 300
	// teamDevRefusedFor is how long a refusal shows on the dev page.
	teamDevRefusedFor = 7 * 24 * time.Hour
)

var teamDevAccessMu sync.Mutex

func teamDevAccessPath(team string) string {
	return filepath.Join(teamDevDir(team), "access.json")
}

// teamDevAllowFile is the box's live allowlist, which its proxy rereads.
func teamDevAllowFile(team string) string {
	return filepath.Join(teamDevDir(team), "net", "allow")
}

func loadTeamDevAccess(team string) teamDevAccess {
	var access teamDevAccess
	if data, err := os.ReadFile(teamDevAccessPath(team)); err == nil {
		json.Unmarshal(data, &access)
	}
	return access
}

func saveTeamDevAccess(team string, access teamDevAccess) error {
	data, _ := json.MarshalIndent(access, "", "  ")
	return writeFileAtomic(teamDevAccessPath(team), append(data, '\n'), 0o600)
}

// teamDevAllowHosts is the box's allowlist: what development needs, and
// what the owners allowed.
func teamDevAllowHosts(team string) []string {
	hosts := teamDevHosts()
	for _, site := range loadTeamDevAccess(team).Allowed {
		if !slices.Contains(hosts, site.Host) {
			hosts = append(hosts, site.Host)
		}
	}
	return hosts
}

// writeTeamDevAllow rewrites the live allowlist, keeping what an admin
// added with hi box allow, without drop.
func writeTeamDevAllow(team, drop string) error {
	hosts := teamDevAllowHosts(team)
	if data, err := os.ReadFile(teamDevAllowFile(team)); err == nil {
		for _, host := range parseBoxAllowlist(string(data)) {
			if !slices.Contains(hosts, host) {
				hosts = append(hosts, host)
			}
		}
	}
	hosts = slices.DeleteFunc(hosts, func(host string) bool { return host == drop })
	if err := os.MkdirAll(filepath.Dir(teamDevAllowFile(team)), 0o700); err != nil {
		return err
	}
	return writeFileAtomic(teamDevAllowFile(team), []byte(strings.Join(hosts, "\n")+"\n"), 0o600)
}

// teamDevQuietHosts are refused on purpose and not shown as refusals: the
// agents' and herdr's own update checks and calls around the proxy's
// sign-ins, which an owner shouldn't be offered to allow.
var teamDevQuietHosts = []string{"api.anthropic.com", "claude.ai", "anthropic.com", "downloads.claude.ai", "statsig.anthropic.com", "herdr.dev", "sentry.io"}

// teamDevSiteHost is the site's name from what an agent or owner gave: a
// name, or an address with it.
func teamDevSiteHost(text string) string {
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

// teamDevPrivateSuffixes are names that are a network's own.
var teamDevPrivateSuffixes = []string{".local", ".internal", ".localhost", ".lan", ".home.arpa", ".intranet", ".corp", ".localdomain"}

// teamDevLookup is a name's addresses, from this machine; tests replace it.
var teamDevLookup = func(host string) ([]net.IP, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return boxLookupIP(ctx, host)
}

// teamDevSiteRefusal says why a site can't be allowed: it isn't a site's
// name, or it is inside the network, as this machine sees it.
func teamDevSiteRefusal(host string) error {
	if !boxDomainPattern.MatchString(host) || strings.HasPrefix(host, "*.") {
		return fmt.Errorf("%q isn't a site's name, such as query1.finance.yahoo.com", host)
	}
	for _, suffix := range teamDevPrivateSuffixes {
		if strings.HasSuffix(host, suffix) {
			return fmt.Errorf("%s is a private network's name; the dev box never reaches the network it's on", host)
		}
	}
	ips, err := teamDevLookup(host)
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
func (b *teamBroker) devSiteAsk(host, why, work string) (teamDevSiteAnswer, error) {
	host, why = teamDevSiteHost(host), strings.TrimSpace(why)
	if why == "" {
		return teamDevSiteAnswer{}, errors.New("say what the site is for: hi dev allow <site> --why \"<what for>\"")
	}
	if len(why) > teamDevWhyMax {
		why = why[:teamDevWhyMax]
	}
	if boxHostAllowed(teamDevAllowHosts(b.team), host) {
		return teamDevSiteAnswer{Host: host, State: teamDevSiteAllowed, Message: "The box may reach " + host + " already."}, nil
	}
	if err := teamDevSiteRefusal(host); err != nil {
		return teamDevSiteAnswer{}, err
	}
	teamDevAccessMu.Lock()
	access := loadTeamDevAccess(b.team)
	for i := range access.Requests {
		ask := &access.Requests[i]
		if ask.Host != host {
			continue
		}
		switch ask.State {
		case teamDevSiteDenied:
			teamDevAccessMu.Unlock()
			return teamDevSiteAnswer{Host: host, State: teamDevSiteDenied,
				Message: fmt.Sprintf("%s said no to %s on %s. Tell the user, and do without it, or ask them to allow it on the dev page.", firstNonEmpty(ask.By, "An owner"), host, ask.Decided.Format("Jan 2"))}, nil
		case teamDevSiteAsked:
			ask.Why, ask.Work = why, firstNonEmpty(work, ask.Work)
			err := saveTeamDevAccess(b.team, access)
			teamDevAccessMu.Unlock()
			return teamDevSiteAnswer{Host: host, State: teamDevSiteAsked, Message: "The owners were asked for " + host + " already; they see it on the dev page. hi dev allow " + host + " --wait waits for their answer."}, err
		}
	}
	access.Requests = append(access.Requests, teamDevSiteAsk{Host: host, Why: why, Work: work, At: time.Now().UTC(), State: teamDevSiteAsked})
	err := saveTeamDevAccess(b.team, access)
	teamDevAccessMu.Unlock()
	if err != nil {
		return teamDevSiteAnswer{}, err
	}
	b.devSiteTell(host, why, work)
	return teamDevSiteAnswer{Host: host, State: teamDevSiteAsked,
		Message: "Asked the team's owners for " + host + ": they see it on the dev page and in the channel. hi dev allow " + host + " --wait waits for their answer."}, nil
}

// devSiteTell posts an ask in the channel, with the dev page's link.
func (b *teamBroker) devSiteTell(host, why, work string) {
	text := "The dev box asks to reach *" + host + "*"
	if work != "" {
		text += " (for " + work + ")"
	}
	text += ": " + why + "."
	if link := teamDevPageAt(b.team, "sites"); link != "" {
		text += " An owner can allow it on the dev page: " + link
	} else {
		text += " An owner can allow it on the dev page."
	}
	if _, err := b.devSay(teamDevSayRequest{Text: text}); err != nil {
		fmt.Fprintf(os.Stderr, "hi: telling the channel the dev box asks for %s: %v\n", host, err)
	}
}

// teamDevPageAt is the dev page's address, without its key, at fragment.
func teamDevPageAt(team, fragment string) string {
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
func (b *teamBroker) devSiteState(host string) teamDevSiteAnswer {
	host = teamDevSiteHost(host)
	if boxHostAllowed(teamDevAllowHosts(b.team), host) {
		return teamDevSiteAnswer{Host: host, State: teamDevSiteAllowed, Message: "The box may reach " + host + " now."}
	}
	for _, ask := range loadTeamDevAccess(b.team).Requests {
		if ask.Host == host {
			if ask.State == teamDevSiteDenied {
				return teamDevSiteAnswer{Host: host, State: teamDevSiteDenied, Message: firstNonEmpty(ask.By, "An owner") + " said no to " + host + ". Tell the user, and do without it."}
			}
			return teamDevSiteAnswer{Host: host, State: teamDevSiteAsked, Message: "The owners haven't answered for " + host + " yet."}
		}
	}
	return teamDevSiteAnswer{Host: host, State: "", Message: host + " isn't allowed, and nobody asked for it: hi dev allow " + host + " --why \"<what for>\"."}
}

// devAccessView is what the dev page shows of the box's reach.
func (b *teamBroker) devAccessView() teamDevAccessView {
	access := loadTeamDevAccess(b.team)
	view := teamDevAccessView{Allowed: access.Allowed}
	known := map[string]bool{}
	for _, ask := range access.Requests {
		known[ask.Host] = true
		if ask.State == teamDevSiteAsked {
			view.Asks = append(view.Asks, ask)
		}
	}
	hosts := teamDevAllowHosts(b.team)
	refused := map[string]*teamDevRefused{}
	since := time.Now().Add(-teamDevRefusedFor)
	for _, line := range boxReadLines(filepath.Join(teamDevDir(b.team), "net", "network.log"), 5000) {
		fields := strings.Fields(line)
		if len(fields) != 3 || fields[1] != "blocked" {
			continue
		}
		at, err := time.Parse(time.RFC3339, fields[0])
		host := teamDevSiteHost(fields[2])
		if err != nil || at.Before(since) || known[host] || boxHostAllowed(hosts, host) || boxHostAllowed(teamDevQuietHosts, host) || !boxDomainPattern.MatchString(host) {
			continue
		}
		if refused[host] == nil {
			refused[host] = &teamDevRefused{Host: host}
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
// say no to it.
func (b *teamBroker) devSiteDecide(person teamPersonKey, host string, allow bool) error {
	host = teamDevSiteHost(host)
	if allow {
		if err := teamDevSiteRefusal(host); err != nil {
			return err
		}
	} else if !boxDomainPattern.MatchString(host) {
		return fmt.Errorf("%q isn't a site's name", host)
	}
	teamDevAccessMu.Lock()
	defer teamDevAccessMu.Unlock()
	access := loadTeamDevAccess(b.team)
	now := time.Now().UTC()
	why, found := "", false
	for i := range access.Requests {
		if ask := &access.Requests[i]; ask.Host == host {
			found, why = true, ask.Why
			ask.State, ask.By, ask.Decided = teamDevSiteDenied, person.Name, now
			if allow {
				ask.State = teamDevSiteAllowed
			}
		}
	}
	if !found && !allow {
		// A refusal nobody asked for: it stops showing.
		access.Requests = append(access.Requests, teamDevSiteAsk{Host: host, At: now, State: teamDevSiteDenied, By: person.Name, Decided: now})
	}
	if allow && !slices.ContainsFunc(access.Allowed, func(site teamDevSite) bool { return site.Host == host }) {
		access.Allowed = append(access.Allowed, teamDevSite{Host: host, Why: why, By: person.Name, At: now})
	}
	if err := saveTeamDevAccess(b.team, access); err != nil {
		return err
	}
	if !allow {
		return nil
	}
	return writeTeamDevAllow(b.team, "")
}

// devSiteRemove takes a site the owners allowed off the box's list.
func (b *teamBroker) devSiteRemove(host string) error {
	host = teamDevSiteHost(host)
	teamDevAccessMu.Lock()
	defer teamDevAccessMu.Unlock()
	access := loadTeamDevAccess(b.team)
	before := len(access.Allowed)
	access.Allowed = slices.DeleteFunc(access.Allowed, func(site teamDevSite) bool { return site.Host == host })
	if len(access.Allowed) == before {
		return fmt.Errorf("the owners didn't allow %s", host)
	}
	// Asked again, it is a new ask.
	access.Requests = slices.DeleteFunc(access.Requests, func(ask teamDevSiteAsk) bool { return ask.Host == host })
	if err := saveTeamDevAccess(b.team, access); err != nil {
		return err
	}
	return writeTeamDevAllow(b.team, host)
}

// teamDevAllowWait is how long hi dev allow --wait waits, and how often it
// looks; tests shorten them.
var (
	teamDevAllowWait  = 9 * time.Minute
	teamDevAllowEvery = 3 * time.Second
)

// teamDevAllowClient is hi dev allow, in the box.
func teamDevAllowClient(args []string, stdout io.Writer, call func(method, path string, body, out any) error, branch func() (string, error)) error {
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
		var view teamDevAccessView
		if err := call("GET", "/allow", nil, &view); err != nil {
			return err
		}
		printTeamDevAccess(view, stdout)
		return nil
	}
	var answer teamDevSiteAnswer
	if why != "" {
		work, _ := branch()
		if err := call("POST", "/allow", map[string]string{"host": host, "why": why, "work": work}, &answer); err != nil {
			return err
		}
	} else if err := call("GET", "/allow?host="+url.QueryEscape(host), nil, &answer); err != nil {
		return err
	}
	fmt.Fprintln(stdout, answer.Message)
	if !wait || answer.State != teamDevSiteAsked {
		if answer.State == teamDevSiteDenied {
			return errors.New("the owners said no")
		}
		return nil
	}
	deadline := time.Now().Add(teamDevAllowWait)
	for time.Now().Before(deadline) {
		time.Sleep(teamDevAllowEvery)
		if err := call("GET", "/allow?host="+url.QueryEscape(answer.Host), nil, &answer); err != nil {
			continue // the team's service restarting
		}
		switch answer.State {
		case teamDevSiteAllowed:
			fmt.Fprintln(stdout, "Allowed: the box reaches "+answer.Host+" now.")
			return nil
		case teamDevSiteDenied:
			fmt.Fprintln(stdout, answer.Message)
			return errors.New("the owners said no")
		}
	}
	return fmt.Errorf("no answer from the owners for %s yet; tell the user it waits on the dev page, and run hi dev allow %s --wait again when they say", answer.Host, answer.Host)
}

func printTeamDevAccess(view teamDevAccessView, stdout io.Writer) {
	fmt.Fprintln(stdout, "The box reaches what development needs (GitHub, npm, PyPI, Go modules, crates.io, Hugging Face, the agents' services).")
	if len(view.Allowed) > 0 {
		fmt.Fprintln(stdout, "The owners allowed:")
		for _, site := range view.Allowed {
			fmt.Fprintf(stdout, "  %s%s\n", site.Host, teamDevParen(site.Why))
		}
	}
	if len(view.Asks) > 0 {
		fmt.Fprintln(stdout, "Asked, waiting for the owners:")
		for _, ask := range view.Asks {
			fmt.Fprintf(stdout, "  %s%s\n", ask.Host, teamDevParen(ask.Why))
		}
	}
	if len(view.Refused) > 0 {
		fmt.Fprintln(stdout, "Refused lately, not asked for:")
		for _, r := range view.Refused {
			fmt.Fprintf(stdout, "  %s (%d times)\n", r.Host, r.Times)
		}
	}
}

func teamDevParen(text string) string {
	if text == "" {
		return ""
	}
	return ": " + text
}

// teamDevKeyStatus is one of the team's keys or sign-ins, for the dev
// page's Security tab: whose it is, and the command that changes it.
type teamDevKeyStatus struct {
	Name    string `json:"name"`
	State   string `json:"state"`
	Command string `json:"command"`
}

// teamDevKeys is what the team's keys and sign-ins are, from its secrets,
// without their values.
func teamDevKeys(team string) []teamDevKeyStatus {
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
		claude = "The team's own token"
		if !secrets.ClaudeSignedIn.IsZero() {
			claude += ", since " + secrets.ClaudeSignedIn.Format("Jan 2, 2006")
		}
	}
	return []teamDevKeyStatus{
		{Name: "OpenRouter key", State: own(secrets.OpenRouterAPIKey != "", "The team's own", "This machine's"), Command: "hi team key " + team + " openrouter"},
		{Name: "Claude", State: claude, Command: "hi team signin " + team + " claude"},
		{Name: "ChatGPT", State: own(secrets.ChatGPT != nil, "The team's own sign-in", "This machine's sign-in (Codex's)"), Command: "hi team signin " + team + " chatgpt"},
		{Name: "Slack tokens", State: own(secrets.SlackBotToken != "" && secrets.SlackAppToken != "", "Set", "Missing"), Command: "hi team key " + team + " slack"},
	}
}
