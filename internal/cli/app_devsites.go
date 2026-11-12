package cli

// What every app's dev box reaches beyond what development needs and what
// its owners allowed (hi_app_dev_sites.md, release 1): the app's own Stage
// and dev page, the organisation's domains, and the sites an admin allowed
// for every app. Each is a site nobody should have to ask for: the app's
// own addresses are its own, and what's sent to the organisation's sites
// stays with the organisation.

import (
	"fmt"
	"io"
	"net/url"
	"os"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

// appsSites is settings.json's sites: the ones an admin allowed for every
// app, and whether the organisation's domains are on the list.
type appsSites struct {
	Allowed []appDevSite `json:"allowed,omitempty"`
	// OrgOff takes the organisation's domains off the list; they are on
	// by default once an admin sets them (hi people domains).
	OrgOff bool `json:"org_off,omitempty"`
}

// appDevOrgHosts are the organisation's domains, as the dev boxes reach
// them: each with its subdomains.
func appDevOrgHosts() []string {
	if loadTeamsSettings().Sites.OrgOff {
		return nil
	}
	return loadMachinePeople().Domains
}

// appDevMachineHosts are the sites an admin allowed for every app.
func appDevMachineHosts() []string {
	var hosts []string
	for _, site := range loadTeamsSettings().Sites.Allowed {
		hosts = append(hosts, site.Host)
	}
	return hosts
}

// appDevOwnCache keeps each app's own public names a few minutes: telling
// them takes a lookup each.
var appDevOwnCache sync.Map

type appDevOwnEntry struct {
	hosts      []string
	production []string
	at         time.Time
}

const appDevOwnFor = 10 * time.Minute

// appDevOwnHosts are the app's Stage and dev page, by the names people
// reach them at, when those are on the internet. Production isn't one: an
// agent calling production's API can change production's data, so it
// stays an ask. A name production shares (one name, several ports) isn't
// either, since the list knows names, not ports. A name inside the
// network stays out, as any does.
func appDevOwnHosts(team string) []string { return appDevOwn(team).hosts }

// appDevProductionHosts are the names production is at, which a wider
// line (the organisation's domain) mustn't allow by itself.
func appDevProductionHosts(team string) []string { return appDevOwn(team).production }

func appDevOwn(team string) appDevOwnEntry {
	if entry, ok := appDevOwnCache.Load(appDir(team)); ok && time.Since(entry.(appDevOwnEntry).at) < appDevOwnFor {
		return entry.(appDevOwnEntry)
	}
	hostOf := func(link string) string {
		if link == "" {
			return ""
		}
		if u, err := url.Parse(link); err == nil {
			return appDevSiteHost(u.Hostname())
		}
		return ""
	}
	production := []string{hostOf(appInternalURL(team, appSlotProduction)), appDevSiteHost(appSlotDomain(team, appSlotProduction))}
	if state, ok := loadTeamSlot(team, appSlotProduction); ok {
		production = append(production, hostOf(state.URL))
	}
	candidates := []string{hostOf(appInternalURL(team, appSlotPreview)), hostOf(appInternalURL(team, appAddressDev)), appDevSiteHost(appSlotDomain(team, appSlotPreview))}
	if state, ok := loadTeamSlot(team, appSlotPreview); ok {
		candidates = append(candidates, hostOf(state.URL))
	}
	if page, ok := loadTeamDevPage(team); ok {
		candidates = append(candidates, hostOf(page.URL))
	}
	entry := appDevOwnEntry{at: time.Now()}
	for _, host := range production {
		if host != "" && !slices.Contains(entry.production, host) {
			entry.production = append(entry.production, host)
		}
	}
	for _, host := range candidates {
		if host == "" || slices.Contains(production, host) || slices.Contains(entry.hosts, host) {
			continue
		}
		if appDevSiteRefusal(host) == nil {
			entry.hosts = append(entry.hosts, host)
		}
	}
	appDevOwnCache.Store(appDir(team), entry)
	return entry
}

// appDevSitesRefresh rewrites every app's dev box's live list, without
// drop: after the machine's sites or the organisation's domains change.
func appDevSitesRefresh(drop ...string) {
	for _, team := range adminAppNames() {
		if !fileExists(appDevDir(team)) {
			continue
		}
		if err := writeTeamDevAllow(team, drop...); err != nil {
			fmt.Fprintf(os.Stderr, "hi: the dev box's list for %s: %v\n", team, err)
		}
	}
}

// appDevSharedSite is a site the owners of several apps allowed, which
// an admin may allow for every app.
type appDevSharedSite struct {
	Host string   `json:"host"`
	Apps []string `json:"apps"`
}

// appDevSharedSites are the sites two apps or more allowed, that aren't
// the machine's already, most apps first.
func appDevSharedSites() []appDevSharedSite {
	machine := appDevMachineHosts()
	by := map[string][]string{}
	for _, team := range adminAppNames() {
		for _, site := range loadTeamDevAccess(team).Allowed {
			if !slices.Contains(by[site.Host], team) {
				by[site.Host] = append(by[site.Host], team)
			}
		}
	}
	var shared []appDevSharedSite
	for host, apps := range by {
		if len(apps) >= 2 && !boxHostAllowed(machine, host) {
			sort.Strings(apps)
			shared = append(shared, appDevSharedSite{Host: host, Apps: apps})
		}
	}
	sort.Slice(shared, func(i, j int) bool {
		if len(shared[i].Apps) != len(shared[j].Apps) {
			return len(shared[i].Apps) > len(shared[j].Apps)
		}
		return shared[i].Host < shared[j].Host
	})
	return shared
}

// appDevSitesView is the admin page's Sites, in the machine's Network.
type appDevSitesView struct {
	Allowed []appDevSite       `json:"allowed"`
	Org     []string           `json:"org"` // the organisation's domains
	OrgOn   bool               `json:"org_on"`
	Shared  []appDevSharedSite `json:"shared"`
}

func appDevSitesNow() appDevSitesView {
	sites := loadTeamsSettings().Sites
	view := appDevSitesView{Allowed: sites.Allowed, Org: loadMachinePeople().Domains, OrgOn: !sites.OrgOff, Shared: appDevSharedSites()}
	if view.Allowed == nil {
		view.Allowed = []appDevSite{}
	}
	if view.Org == nil {
		view.Org = []string{}
	}
	if view.Shared == nil {
		view.Shared = []appDevSharedSite{}
	}
	return view
}

const adminSitesUsage = `usage: hi admin sites                          what every app's dev box reaches beyond its own list
       hi admin sites allow <site> [--why <what for>]
                                                 a site for every app's dev box, and its subdomains
       hi admin sites remove <site>
       hi admin sites org on|off                 the organisation's domains (hi people domains) on
                                                 every dev box's list, on by default`

// adminSitesCommand is hi admin sites.
func adminSitesCommand(args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return adminSitesShow(stdout)
	}
	usage := usageError{adminSitesUsage}
	switch args[0] {
	case "help", "-h", "--help":
		fmt.Fprintln(stdout, adminSitesUsage)
		return nil
	case "allow":
		if len(args) < 2 {
			return usage
		}
		why := ""
		rest := args[2:]
		if len(rest) == 2 && rest[0] == "--why" {
			why = strings.TrimSpace(rest[1])
		} else if len(rest) != 0 {
			return usage
		}
		return adminSitesAllow(args[1], why, stdout)
	case "remove", "rm":
		if len(args) != 2 {
			return usage
		}
		return adminSitesRemove(args[1], stdout)
	case "org":
		if len(args) != 2 || (args[1] != "on" && args[1] != "off") {
			return usage
		}
		return adminSitesOrg(args[1] == "on", stdout)
	}
	return usage
}

func adminSitesShow(stdout io.Writer) error {
	view := appDevSitesNow()
	if len(view.Allowed) == 0 {
		fmt.Fprintln(stdout, "No sites for every app's dev box. Allow one: hi admin sites allow <site> --why \"<what for>\"")
	} else {
		fmt.Fprintln(stdout, "For every app's dev box:")
		for _, site := range view.Allowed {
			fmt.Fprintf(stdout, "  %s  %s, %s", site.Host, site.By, site.At.Local().Format("2006-01-02"))
			if site.Why != "" {
				fmt.Fprintf(stdout, ": %s", site.Why)
			}
			fmt.Fprintln(stdout)
		}
	}
	switch {
	case len(view.Org) == 0:
		fmt.Fprintln(stdout, "The organisation's domains: none set (hi people domains corp.example).")
	case view.OrgOn:
		fmt.Fprintf(stdout, "The organisation's domains are on every dev box's list: %s. Off: hi admin sites org off\n", strings.Join(view.Org, ", "))
	default:
		fmt.Fprintf(stdout, "The organisation's domains (%s) aren't on the dev boxes' lists. On: hi admin sites org on\n", strings.Join(view.Org, ", "))
	}
	if len(view.Shared) > 0 {
		fmt.Fprintln(stdout, "Allowed in several apps:")
		for _, site := range view.Shared {
			fmt.Fprintf(stdout, "  %s  %s: hi admin sites allow %s\n", site.Host, strings.Join(site.Apps, ", "), site.Host)
		}
	}
	fmt.Fprintln(stdout, "Each app's dev box also reaches its own Stage and dev page, when they're on the internet.")
	return nil
}

func adminSitesAllow(text, why string, stdout io.Writer) error {
	host := appDevSiteHost(text)
	if err := appDevSiteRefusal(host); err != nil {
		return err
	}
	if len(why) > appDevWhyMax {
		why = why[:appDevWhyMax]
	}
	settings := loadTeamsSettings()
	if slices.ContainsFunc(settings.Sites.Allowed, func(site appDevSite) bool { return site.Host == host }) {
		fmt.Fprintf(stdout, "Every app's dev box reaches %s already.\n", host)
		return nil
	}
	settings.Sites.Allowed = append(settings.Sites.Allowed, appDevSite{Host: host, Why: why, By: appActor(), At: time.Now().UTC()})
	if err := saveTeamsSettings(settings); err != nil {
		return err
	}
	appDevSitesRefresh()
	fmt.Fprintf(stdout, "Every app's dev box reaches %s now, and its subdomains.\n", host)
	return nil
}

func adminSitesRemove(text string, stdout io.Writer) error {
	host := appDevSiteHost(text)
	settings := loadTeamsSettings()
	before := len(settings.Sites.Allowed)
	settings.Sites.Allowed = slices.DeleteFunc(settings.Sites.Allowed, func(site appDevSite) bool { return site.Host == host })
	if len(settings.Sites.Allowed) == before {
		return fmt.Errorf("%s isn't one of every app's sites: hi admin sites lists them", host)
	}
	if err := saveTeamsSettings(settings); err != nil {
		return err
	}
	appDevSitesRefresh(host)
	fmt.Fprintf(stdout, "%s is off every app's list, unless an app's owners allowed it themselves.\n", host)
	return nil
}

func adminSitesOrg(on bool, stdout io.Writer) error {
	settings := loadTeamsSettings()
	settings.Sites.OrgOff = !on
	if err := saveTeamsSettings(settings); err != nil {
		return err
	}
	domains := loadMachinePeople().Domains
	if on {
		appDevSitesRefresh()
		if len(domains) == 0 {
			fmt.Fprintln(stdout, "The organisation's domains go on every dev box's list once they're set: hi people domains corp.example")
			return nil
		}
		fmt.Fprintf(stdout, "Every app's dev box reaches %s now, and their subdomains.\n", strings.Join(domains, ", "))
		return nil
	}
	appDevSitesRefresh(domains...)
	if len(domains) == 0 {
		fmt.Fprintln(stdout, "The organisation's domains won't go on the dev boxes' lists.")
		return nil
	}
	fmt.Fprintf(stdout, "%s are off the dev boxes' lists; their owners allow what they need.\n", strings.Join(domains, ", "))
	return nil
}
