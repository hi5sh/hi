package cli

// hi's own check of a site a dev box asks for (hi_app_dev_sites.md,
// release 2): free facts about who runs the site and who can put things
// there, and a site that passes the rules is allowed by itself for seven
// days, said once a day in the ops channel with Keep and Undo. A site the
// rules don't settle goes to the owners as before, with what hi found.
//
// What an allow risks is a place the box can send things to, so the rules
// ask whether the site has a long history, many people use it, no threat
// list names it, and nobody but its owner can make a name under it or
// catch what's sent there. A page's content and the agent's why are never
// evidence: an attacker writes both.

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/publicsuffix"
)

const (
	// appSiteCheckFor is how long a site hi allowed by itself stays,
	// unless an owner keeps it.
	appSiteCheckFor = 7 * 24 * time.Hour
	// appSiteCheckPerDay is how many sites hi allows by itself for one
	// app in a day; past that, asks go to the owners.
	appSiteCheckPerDay = 10
	// appSiteFactsFor is how long what hi found about a name is kept.
	appSiteFactsFor = 7 * 24 * time.Hour
	// A site passes on rank within appSiteRankPass, and is old enough
	// at a year; one whose age no registry gives passes on a rank within
	// appSiteRankNoAge, which a 30-day average doesn't give a new name.
	appSiteRankPass  = 100_000
	appSiteRankNoAge = 10_000
	appSiteAgePass   = 365 * 24 * time.Hour
	// appSiteCheckBy is who allowed a site the check allowed.
	appSiteCheckBy = "hi's check"
	// The check's settings: rules (the default) or off.
	appSiteCheckRules = "rules"
	appSiteCheckOff   = "off"
)

// appSiteOpenHosts are where anyone can put files or data, or catch what's
// sent, under names that aren't theirs: the watcher's paste sites, file
// drops, tunnels and request catchers, and file shares, short links,
// dynamic DNS, and hosted databases. Only owners allow these.
var appSiteOpenHosts = append(parseBoxAllowlist(watchHostsStarter),
	"drive.google.com", "docs.google.com", "drive.usercontent.google.com", "sites.google.com", "forms.gle",
	"dropbox.com", "dropboxusercontent.com", "box.com", "onedrive.live.com", "1drv.ms", "sharepoint.com",
	"gist.github.com", "gist.githubusercontent.com", "pastes.dev", "justpaste.it", "controlc.com",
	"bit.ly", "tinyurl.com", "t.co", "is.gd", "ow.ly", "rebrand.ly", "cutt.ly", "shorturl.at",
	"duckdns.org", "no-ip.com", "ddns.net", "dyndns.org", "freedns.afraid.org", "nip.io", "sslip.io",
	"beeceptor.com", "requestcatcher.com", "hookbin.com", "webhook.cool", "pinggy.io", "localtunnel.me",
	"tunnelmole.net", "devtunnels.ms", "ngrok-free.dev", "serveo.net",
	"mongodb.net", "supabase.co", "firebaseio.com", "firebasestorage.googleapis.com", "cloudfront.net",
	"blob.core.windows.net", "web.core.windows.net", "azurewebsites.net", "herokuapp.com", "workers.dev",
	"pages.dev", "vercel.app", "netlify.app", "github.io", "gitlab.io", "glitch.me", "replit.dev", "repl.co",
	"discord.com", "discordapp.com", "telegram.org", "t.me",
)

// appSiteFacts is what hi found about a site.
type appSiteFacts struct {
	Host string `json:"host"`
	Name string `json:"name"` // the registrable name: yahoo.com
	// Claimable is the suffix anyone can make names under (github.io),
	// and Open the entry of hi's list the site is under.
	Claimable string `json:"claimable,omitempty"`
	Open      string `json:"open,omitempty"`
	// Registered is when the name was registered; zero when no registry
	// said (NoAge says why).
	Registered time.Time `json:"registered,omitzero"`
	NoAge      string    `json:"no_age,omitempty"`
	// Rank is the name's place on Tranco's list of the million most used;
	// 0 is off the list (or no list yet: NoRank).
	Rank   int    `json:"rank,omitempty"`
	NoRank string `json:"no_rank,omitempty"`
	// Listed is the threat list that names the site; NoLists says the
	// lists didn't answer.
	Listed  string `json:"listed,omitempty"`
	NoLists string `json:"no_lists,omitempty"`
}

// pass says whether the rules allow the site by itself, and if not, why.
func (f appSiteFacts) pass(now time.Time) (bool, string) {
	switch {
	case f.Listed != "":
		return false, f.Listed + " lists it as harmful"
	case f.Claimable != "":
		return false, "anyone can make a name under " + f.Claimable
	case f.Open != "":
		return false, "anyone can put files or data there, or catch what's sent (" + f.Open + ")"
	case f.NoLists != "":
		return false, "the threat lists didn't answer (" + f.NoLists + ")"
	case f.Rank == 0 && f.NoRank != "":
		return false, f.NoRank
	case f.Rank == 0 || f.Rank > appSiteRankPass:
		return false, f.Name + " isn't among the 100,000 most used sites"
	case f.Registered.IsZero() && f.Rank > appSiteRankNoAge:
		return false, "no registry says how old " + f.Name + " is (" + firstNonEmpty(f.NoAge, "no answer") + "), and it isn't among the 10,000 most used"
	case !f.Registered.IsZero() && now.Sub(f.Registered) < appSiteAgePass:
		return false, fmt.Sprintf("%s was registered %s", f.Name, appSiteAge(now.Sub(f.Registered))+" ago")
	}
	return true, ""
}

// words are what hi found, in a line: yahoo.com, top 1,000, 29 years old.
func (f appSiteFacts) words(now time.Time) string {
	parts := []string{firstNonEmpty(f.Name, f.Host)}
	switch {
	case f.Rank == 0:
	case f.Rank <= 1_000:
		parts = append(parts, "top 1,000")
	case f.Rank <= 10_000:
		parts = append(parts, "top 10,000")
	case f.Rank <= 100_000:
		parts = append(parts, "top 100,000")
	default:
		parts = append(parts, "top 1,000,000")
	}
	if !f.Registered.IsZero() {
		parts = append(parts, appSiteAge(now.Sub(f.Registered))+" old")
	}
	return strings.Join(parts, ", ")
}

// appSiteAge is an age in days, months, or years.
func appSiteAge(d time.Duration) string {
	days := int(d.Hours()/24 + 0.5)
	switch {
	case days < 1:
		return "less than a day"
	case days == 1:
		return "1 day"
	case days < 60:
		return strconv.Itoa(days) + " days"
	case days < 730:
		return strconv.Itoa(days/30) + " months"
	}
	return strconv.Itoa(days/365) + " years"
}

// appSiteLookups are where the facts come from; tests replace them.
var appSiteLookups = struct {
	rank       func(name string) (int, error)
	registered func(ctx context.Context, name string) (time.Time, error)
	harmful    func(ctx context.Context, host string) (string, error)
}{rank: appSiteTrancoRank, registered: appSiteRDAPRegistered, harmful: appSiteThreatLists}

// appSiteFactsCache is sites.json: what hi found, by name and by host.
type appSiteFactsCache struct {
	Names map[string]appSiteNameFacts `json:"names,omitempty"`
	Hosts map[string]appSiteHostFacts `json:"hosts,omitempty"`
}

type appSiteNameFacts struct {
	Rank       int       `json:"rank,omitempty"`
	Registered time.Time `json:"registered,omitzero"`
	NoAge      string    `json:"no_age,omitempty"`
	At         time.Time `json:"at"`
}

type appSiteHostFacts struct {
	Listed string    `json:"listed,omitempty"`
	At     time.Time `json:"at"`
}

func appSiteFactsPath() string { return filepath.Join(appSitesDir(), "facts.json") }

// appSitesDir holds what hi knows about sites. It sits beside the apps
// folder, never in it: every folder in there is an app, and v2.6.137 to
// v2.6.140 put this one there, so it showed as a broken app "sites".
func appSitesDir() string {
	apps := appsDir()
	dir := filepath.Join(filepath.Dir(apps), "sites")
	appSitesMove.Do(func() { moveAppSitesOutOfApps(filepath.Join(apps, "sites"), dir) })
	return dir
}

var appSitesMove sync.Once

// moveAppSitesOutOfApps moves the files of an old apps/sites folder out.
// An app called sites is a link to its folder and is left alone, and so
// is anything in the folder that hi didn't put there.
func moveAppSitesOutOfApps(old, dir string) {
	info, err := os.Lstat(old)
	if err != nil || !info.IsDir() {
		return
	}
	for _, name := range []string{"facts.json", "tranco-top-1m.csv"} {
		from, to := filepath.Join(old, name), filepath.Join(dir, name)
		if !fileExists(from) {
			continue
		}
		if fileExists(to) {
			os.Remove(from)
			continue
		}
		if os.MkdirAll(dir, 0o700) == nil {
			os.Rename(from, to)
		}
	}
	os.Remove(old) // only when it's empty
}

var appSiteFactsMu sync.Mutex

func loadAppSiteFacts() appSiteFactsCache {
	var cache appSiteFactsCache
	if data, err := os.ReadFile(appSiteFactsPath()); err == nil {
		json.Unmarshal(data, &cache)
	}
	if cache.Names == nil {
		cache.Names = map[string]appSiteNameFacts{}
	}
	if cache.Hosts == nil {
		cache.Hosts = map[string]appSiteHostFacts{}
	}
	return cache
}

func saveAppSiteFacts(cache appSiteFactsCache) error {
	if err := os.MkdirAll(filepath.Dir(appSiteFactsPath()), 0o700); err != nil {
		return err
	}
	now := time.Now()
	for name, facts := range cache.Names {
		if now.Sub(facts.At) > appSiteFactsFor {
			delete(cache.Names, name)
		}
	}
	for host, facts := range cache.Hosts {
		if now.Sub(facts.At) > appSiteFactsFor {
			delete(cache.Hosts, host)
		}
	}
	data, _ := json.MarshalIndent(cache, "", "  ")
	return writeFileAtomic(appSiteFactsPath(), append(data, '\n'), 0o600)
}

// appSiteCheckFacts finds what's known about a site, from the cache or
// the lookups, in at most 15 seconds.
func appSiteCheckFacts(host string, now time.Time) appSiteFacts {
	host = appDevSiteHost(host)
	facts := appSiteFacts{Host: host, Name: host}
	if name, err := publicsuffix.EffectiveTLDPlusOne(host); err == nil {
		facts.Name = name
	}
	if suffix, icann := publicsuffix.PublicSuffix(host); !icann && strings.Contains(suffix, ".") && host != suffix {
		facts.Claimable = suffix
	}
	for _, open := range appSiteOpenHosts {
		if boxHostAllowed([]string{open}, host) {
			facts.Open = open
			break
		}
	}
	appSiteFactsMu.Lock()
	cache := loadAppSiteFacts()
	appSiteFactsMu.Unlock()
	name, haveName := cache.Names[facts.Name]
	hostFacts, haveHost := cache.Hosts[host]
	if haveName && now.Sub(name.At) > appSiteFactsFor {
		haveName = false
	}
	if haveHost && now.Sub(hostFacts.At) > appSiteFactsFor {
		haveHost = false
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var wait sync.WaitGroup
	var rankErr, listErr error
	if !haveName {
		name = appSiteNameFacts{At: now}
		wait.Add(2)
		go func() {
			defer wait.Done()
			name.Rank, rankErr = appSiteLookups.rank(facts.Name)
		}()
		go func() {
			defer wait.Done()
			registered, err := appSiteLookups.registered(ctx, facts.Name)
			if err != nil {
				name.NoAge = err.Error()
			}
			name.Registered = registered
		}()
	}
	if !haveHost {
		hostFacts = appSiteHostFacts{At: now}
		wait.Add(1)
		go func() {
			defer wait.Done()
			hostFacts.Listed, listErr = appSiteLookups.harmful(ctx, host)
		}()
	}
	wait.Wait()
	facts.Rank, facts.Registered, facts.NoAge, facts.Listed = name.Rank, name.Registered, name.NoAge, hostFacts.Listed
	if rankErr != nil {
		facts.NoRank = rankErr.Error()
	}
	if listErr != nil {
		facts.NoLists = listErr.Error()
	}
	// What was found is kept; a lookup that failed is tried again next
	// time (a registry with no RDAP is a finding, kept as one).
	appSiteFactsMu.Lock()
	cache = loadAppSiteFacts()
	if !haveName && rankErr == nil {
		cache.Names[facts.Name] = name
	}
	if !haveHost && listErr == nil {
		cache.Hosts[host] = hostFacts
	}
	saveAppSiteFacts(cache)
	appSiteFactsMu.Unlock()
	return facts
}

// ---------------------------------------------------------------------------
// Tranco's list of the million most used names, fetched once a week.

const appSiteTrancoURL = "https://tranco-list.eu/top-1m.csv.zip"

func appSiteTrancoPath() string { return filepath.Join(appSitesDir(), "tranco-top-1m.csv") }

var errNoTranco = errors.New("hi hasn't fetched the list of the most used sites yet; it does within minutes")

// appSiteTrancoRank is a name's place on the list, 0 when it isn't on it.
func appSiteTrancoRank(name string) (int, error) {
	file, err := os.Open(appSiteTrancoPath())
	if err != nil {
		return 0, errNoTranco
	}
	defer file.Close()
	want := []byte("," + name)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := bytes.TrimRight(scanner.Bytes(), "\r")
		if bytes.HasSuffix(line, want) {
			if rank, err := strconv.Atoi(string(line[:len(line)-len(want)])); err == nil {
				return rank, nil
			}
		}
	}
	return 0, scanner.Err()
}

var appSiteTrancoMu sync.Mutex

// appSiteKeepTranco fetches the list when it's missing or a week old.
// Every app's service keeps it, one at a time in a process; a second
// fetch from another app's is harmless.
func appSiteKeepTranco(client *http.Client) error {
	appSiteTrancoMu.Lock()
	defer appSiteTrancoMu.Unlock()
	if info, err := os.Stat(appSiteTrancoPath()); err == nil && time.Since(info.ModTime()) < appSiteFactsFor {
		return nil
	}
	request, _ := http.NewRequest(http.MethodGet, appSiteTrancoURL, nil)
	request.Header.Set("User-Agent", "hi")
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("Tranco's list: %s", response.Status)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 64<<20))
	if err != nil {
		return err
	}
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return err
	}
	for _, entry := range archive.File {
		if !strings.HasSuffix(entry.Name, ".csv") {
			continue
		}
		reader, err := entry.Open()
		if err != nil {
			return err
		}
		list, err := io.ReadAll(io.LimitReader(reader, 128<<20))
		reader.Close()
		if err != nil {
			return err
		}
		if !bytes.HasPrefix(list, []byte("1,")) {
			return errors.New("Tranco's list isn't what hi expects")
		}
		if err := os.MkdirAll(filepath.Dir(appSiteTrancoPath()), 0o700); err != nil {
			return err
		}
		return writeFileAtomic(appSiteTrancoPath(), list, 0o600)
	}
	return errors.New("Tranco's list has no CSV")
}

// ---------------------------------------------------------------------------
// The name's age, from its registry over RDAP (IANA's bootstrap says where).

const appSiteRDAPBootstrap = "https://data.iana.org/rdap/dns.json"

var (
	appSiteRDAPMu   sync.Mutex
	appSiteRDAPAt   time.Time
	appSiteRDAPBase map[string]string
	appSiteHTTP     = &http.Client{Timeout: 60 * time.Second}
)

func appSiteRDAPGet(ctx context.Context, url string, out any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	// Some registries refuse a client without a name.
	request.Header.Set("User-Agent", "hi")
	request.Header.Set("Accept", "application/rdap+json, application/json")
	response, err := appSiteHTTP.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("%s", response.Status)
	}
	return json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(out)
}

func appSiteRDAPRegistered(ctx context.Context, name string) (time.Time, error) {
	appSiteRDAPMu.Lock()
	if appSiteRDAPBase == nil || time.Since(appSiteRDAPAt) > 24*time.Hour {
		var bootstrap struct {
			Services [][][]string `json:"services"`
		}
		if err := appSiteRDAPGet(ctx, appSiteRDAPBootstrap, &bootstrap); err != nil {
			appSiteRDAPMu.Unlock()
			return time.Time{}, fmt.Errorf("IANA's RDAP list: %v", err)
		}
		bases := map[string]string{}
		for _, service := range bootstrap.Services {
			if len(service) == 2 && len(service[1]) > 0 {
				for _, tld := range service[0] {
					bases[strings.ToLower(tld)] = service[1][0]
				}
			}
		}
		appSiteRDAPBase, appSiteRDAPAt = bases, time.Now()
	}
	tld := name[strings.LastIndex(name, ".")+1:]
	base := appSiteRDAPBase[tld]
	appSiteRDAPMu.Unlock()
	if base == "" {
		return time.Time{}, fmt.Errorf(".%s has no RDAP", tld)
	}
	var domain struct {
		Events []struct {
			Action string    `json:"eventAction"`
			Date   time.Time `json:"eventDate"`
		} `json:"events"`
	}
	if err := appSiteRDAPGet(ctx, strings.TrimSuffix(base, "/")+"/domain/"+name, &domain); err != nil {
		return time.Time{}, fmt.Errorf("its registry: %v", err)
	}
	for _, event := range domain.Events {
		if event.Action == "registration" && !event.Date.IsZero() {
			return event.Date, nil
		}
	}
	return time.Time{}, errors.New("its registry doesn't say")
}

// ---------------------------------------------------------------------------
// Threat lists, asked over DNS: Quad9's 9.9.9.9 answers no name for one
// it blocks (9.9.9.10, the same without blocking, answers), and
// Cloudflare's 1.1.1.2 answers 0.0.0.0 for malware.

func appSiteResolver(server string) *net.Resolver {
	return &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
		var dialer net.Dialer
		return dialer.DialContext(ctx, "udp", server)
	}}
}

func appSiteThreatLists(ctx context.Context, host string) (string, error) {
	lookup := func(server string) ([]string, bool, error) {
		addresses, err := appSiteResolver(server).LookupHost(ctx, host)
		var dnsErr *net.DNSError
		if errors.As(err, &dnsErr) && dnsErr.IsNotFound {
			return nil, true, nil
		}
		return addresses, false, err
	}
	var wait sync.WaitGroup
	var open, cloudflare []string
	var blockedGone, openGone bool
	var errs [3]error
	wait.Add(3)
	go func() { defer wait.Done(); _, blockedGone, errs[0] = lookup("9.9.9.9:53") }()
	go func() { defer wait.Done(); open, openGone, errs[1] = lookup("9.9.9.10:53") }()
	go func() { defer wait.Done(); cloudflare, _, errs[2] = lookup("1.1.1.2:53") }()
	wait.Wait()
	if errs[0] == nil && errs[1] == nil && blockedGone && !openGone && len(open) > 0 {
		return "Quad9", nil
	}
	if errs[2] == nil && slices.Contains(cloudflare, "0.0.0.0") {
		return "Cloudflare (1.1.1.2)", nil
	}
	if errs[0] != nil && errs[2] != nil {
		return "", errors.New("Quad9 and Cloudflare didn't answer")
	}
	return "", nil
}
