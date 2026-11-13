package cli

import (
	"bytes"
	"errors"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// appTestSitesLookup answers the names these tests use: public ones on
// the internet, NetBird's inside.
func appTestSitesLookup(t *testing.T) {
	previous := appDevLookup
	appDevLookup = func(host string) ([]net.IP, error) {
		switch {
		case strings.HasSuffix(host, ".corp.example"):
			return []net.IP{net.ParseIP("100.92.4.11")}, nil
		case strings.HasSuffix(host, "example.com"), strings.HasSuffix(host, "example.org"), strings.HasSuffix(host, "example.net"):
			return []net.IP{net.ParseIP("203.0.113.7")}, nil
		}
		return nil, errors.New("no such host")
	}
	t.Cleanup(func() { appDevLookup = previous })
}

func appTestAllowFile(t *testing.T, team string) []string {
	t.Helper()
	data, err := os.ReadFile(appDevAllowFile(team))
	if err != nil {
		t.Fatal(err)
	}
	return parseBoxAllowlist(string(data))
}

// The downloads development needs are in the dev preset.
func TestDevSitesPreset(t *testing.T) {
	for _, host := range []string{"cdn.playwright.dev", "playwright.download.prss.microsoft.com", "releases.astral.sh", "deb.debian.org", "archive.ubuntu.com", "cdn.jsdelivr.net", "unpkg.com", "us.aws.cdn.hf.co"} {
		if !boxHostAllowed(appDevHosts(), host) {
			t.Errorf("%s isn't allowed", host)
		}
	}
	if boxHostAllowed(appDevHosts(), "webhook.site") {
		t.Fatal("too wide")
	}
}

// A "!name" line refuses that name, which a wider line allows, unless
// the name has a line of its own.
func TestBoxHostAllowedRefusal(t *testing.T) {
	hosts := []string{"example.com", "!demo.example.com"}
	for host, want := range map[string]bool{"www.example.com": true, "demo.example.com": false, "DEMO.example.com.": false, "x.demo.example.com": true, "example.com.evil.test": false} {
		if boxHostAllowed(hosts, host) != want {
			t.Errorf("%s: %v", host, !want)
		}
	}
	if !boxHostAllowed(append(hosts, "demo.example.com"), "demo.example.com") {
		t.Fatal("an owner's allow lost to the refusal")
	}
	if boxHostAllowed([]string{"!demo.example.com"}, "demo.example.com") {
		t.Fatal("a refusal allowed")
	}
}

// The app's own Stage and dev page are on its list when they're on the
// internet; production isn't, also under the organisation's domain,
// until an owner allows it; the organisation's domains go off with org
// off.
func TestDevSitesOwnAndOrg(t *testing.T) {
	b, _ := appTestDevPage(t)
	appTestSitesLookup(t)
	os.MkdirAll(filepath.Join(appDevDir("payments"), "net"), 0o700)
	config, _ := loadTeam("payments")
	names := appNamesFrom("demo.example.com")
	config.Addresses, config.Names = appAddressesNames, &names
	saveTeam(config)
	appDevOwnCache.Delete(appDir("payments"))

	own := appDevOwnHosts("payments")
	if !slices.Equal(own, []string{"stage.demo.example.com", "dev.demo.example.com"}) {
		t.Fatal(own)
	}
	hosts := appDevAllowHosts("payments")
	if boxHostAllowed(hosts, "demo.example.com") || !boxHostAllowed(hosts, "stage.demo.example.com") {
		t.Fatal(hosts)
	}

	// Names inside the network stay out.
	inside := appNamesFrom("demo.corp.example")
	config.Names = &inside
	saveTeam(config)
	appDevOwnCache.Delete(appDir("payments"))
	if own := appDevOwnHosts("payments"); len(own) != 0 {
		t.Fatal("inside the network:", own)
	}
	config.Names = &names
	saveTeam(config)
	appDevOwnCache.Delete(appDir("payments"))

	// The organisation's domain: its sites, but not production.
	if err := changeMachinePeople(func(m *machinePeople) error { m.Domains = []string{"example.com"}; return nil }); err != nil {
		t.Fatal(err)
	}
	appDevSitesRefresh()
	file := appTestAllowFile(t, "payments")
	if !boxHostAllowed(file, "www.example.com") || boxHostAllowed(file, "demo.example.com") || boxHostAllowed(file, "example.com.evil.test") {
		t.Fatal(file)
	}
	answer, err := b.devSiteAsk("demo.example.com", "check production's /health", "9-game")
	if err != nil || answer.State != appDevSiteAsked {
		t.Fatal("production wasn't an ask:", answer, err)
	}
	if err := b.devSiteDecide(appPersonKey{User: "U07OWNER1", Name: "Olga"}, "demo.example.com", true); err != nil {
		t.Fatal(err)
	}
	if file := appTestAllowFile(t, "payments"); !boxHostAllowed(file, "demo.example.com") {
		t.Fatal("the owner's allow:", file)
	}

	var out bytes.Buffer
	if err := adminSitesOrg(false, &out); err != nil || !strings.Contains(out.String(), "example.com are off") {
		t.Fatal(out.String(), err)
	}
	file = appTestAllowFile(t, "payments")
	if boxHostAllowed(file, "www.example.com") || !boxHostAllowed(file, "demo.example.com") || slices.Contains(file, "!demo.example.com") {
		t.Fatal("org off:", file)
	}
	if !loadTeamsSettings().Sites.OrgOff {
		t.Fatal("not kept")
	}
}

// An admin's sites go on every app's list and off again; a site the
// owners of two apps allowed shows to the admin.
func TestDevSitesMachine(t *testing.T) {
	appTestDevPage(t)
	appTestSitesLookup(t)
	os.MkdirAll(filepath.Join(appDevDir("payments"), "net"), 0o700)
	os.MkdirAll(filepath.Join(appDevDir("billing"), "net"), 0o700)
	t.Setenv(adminByEnv, "Ada")
	var out bytes.Buffer
	if err := adminSitesAllow("https://files.example.net/x", "shared fixtures", &out); err != nil || !strings.Contains(out.String(), "reaches files.example.net now") {
		t.Fatal(out.String(), err)
	}
	for _, team := range []string{"payments", "billing"} {
		if !boxHostAllowed(appTestAllowFile(t, team), "files.example.net") {
			t.Fatal(team)
		}
	}
	if site := loadTeamsSettings().Sites.Allowed[0]; site.By != "Ada on the admin page" || site.Why != "shared fixtures" {
		t.Fatal(site)
	}
	if err := adminSitesAllow("nas.corp.example", "", &out); err == nil {
		t.Fatal("a name inside the network")
	}

	// The owners of payments allowed it too: it stays there.
	saveTeamDevAccess("payments", appDevAccess{Allowed: []appDevSite{{Host: "files.example.net", By: "Olga"}, {Host: "api.example.com", By: "Olga"}}})
	saveTeamDevAccess("billing", appDevAccess{Allowed: []appDevSite{{Host: "api.example.com", By: "Bo"}}})
	out.Reset()
	if err := adminSitesRemove("files.example.net", &out); err != nil {
		t.Fatal(err)
	}
	if !boxHostAllowed(appTestAllowFile(t, "payments"), "files.example.net") || boxHostAllowed(appTestAllowFile(t, "billing"), "files.example.net") {
		t.Fatal("remove")
	}
	if err := adminSitesRemove("files.example.net", &out); err == nil {
		t.Fatal("removed twice")
	}

	shared := appDevSharedSites()
	if len(shared) != 1 || shared[0].Host != "api.example.com" || !slices.Equal(shared[0].Apps, []string{"billing", "payments"}) {
		t.Fatal(shared)
	}
	out.Reset()
	adminSitesShow(&out)
	if !strings.Contains(out.String(), "api.example.com  billing, payments: hi server sites allow api.example.com") {
		t.Fatal(out.String())
	}
	if err := adminSitesCommand([]string{"org", "maybe"}, &out); err == nil {
		t.Fatal("org maybe")
	}
}

// A task's asks a moment apart share one post with Allow all, which
// allows them all and says so once; one later gets its own post.
func TestDevSitesOnePost(t *testing.T) {
	b, _ := appTestDevPage(t)
	appTestLookup(t)
	channel := &fakeTeamChannel{}
	b.channel = channel
	os.MkdirAll(filepath.Join(appDevDir("payments"), "net"), 0o700)
	if _, err := b.devSiteAsk("api.example.com", "the API", "9-game"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.devSiteAsk("docs.example.org", "its docs", "9-game"); err != nil {
		t.Fatal(err)
	}
	access := loadTeamDevAccess("payments")
	if len(access.Requests) != 2 || access.Requests[0].Thread == "" || access.Requests[0].Thread != access.Requests[1].Thread {
		t.Fatal(access.Requests)
	}
	k1, k2 := access.Requests[0].Key, access.Requests[1].Key
	posts, redraw := 0, ""
	for _, call := range channel.calls {
		if strings.HasPrefix(call, "postButtons ") {
			posts++
		}
		if strings.HasPrefix(call, "redraw ") {
			redraw = call
		}
	}
	all := "api.example.com " + k1 + " docs.example.org " + k2
	if posts != 1 || !strings.Contains(redraw, "asks to reach 2 sites (for 9-game)") || !strings.Contains(redraw, "[Allow all hi_site_allow="+all+"]") ||
		!strings.Contains(redraw, "[Say no to docs.example.org hi_site_deny=docs.example.org "+k2+"]") {
		t.Fatal(posts, redraw)
	}

	// Another task's ask, or this one's later, has a post of its own.
	access.Requests[0].At = time.Now().Add(-3 * time.Minute)
	access.Requests[1].At = access.Requests[0].At
	saveTeamDevAccess("payments", access)
	b.devSiteAsk("query1.finance.yahoo.com", "prices", "9-game")
	posts = 0
	for _, call := range channel.calls {
		if strings.HasPrefix(call, "postButtons ") {
			posts++
		}
	}
	if posts != 2 {
		t.Fatal(channel.calls)
	}

	// Saying no to one leaves the other's buttons.
	if line, err := b.webSite("U07OWNER1", "docs.example.org "+k2, false); err != nil || line != "Said no to docs.example.org." {
		t.Fatal(line, err)
	}
	if last := channel.calls[len(channel.calls)-2]; !strings.HasPrefix(last, "redraw ") || !strings.Contains(last, "said no.") || !strings.Contains(last, "[Allow api.example.com") || strings.Contains(last, "Allow all") {
		t.Fatal(last)
	}
	// Allow all, with one of its two answered: the other is allowed.
	if line, err := b.webSite("U07OWNER1", all, true); err != nil || line != "Allowed api.example.com for the dev box." {
		t.Fatal(line, err)
	}
	if last := channel.calls[len(channel.calls)-2]; !strings.HasPrefix(last, "settle ") || !strings.Contains(last, "allowed it.") {
		t.Fatal(last)
	}
	if !boxHostAllowed(appDevAllowHosts("payments"), "api.example.com") || boxHostAllowed(appDevAllowHosts("payments"), "docs.example.org") {
		t.Fatal(appDevAllowHosts("payments"))
	}
	if _, err := b.webSite("U07OWNER1", all, true); err == nil {
		t.Fatal("answered twice")
	}
	if _, err := b.webSite("U07OWNER1", "api.example.com", true); err == nil {
		t.Fatal("no key")
	}
}

// Allow all allows each, and the thread hears it once.
func TestDevSitesAllowAll(t *testing.T) {
	b, _ := appTestDevPage(t)
	appTestLookup(t)
	channel := &fakeTeamChannel{}
	b.channel = channel
	os.MkdirAll(filepath.Join(appDevDir("payments"), "net"), 0o700)
	b.devSiteAsk("api.example.com", "the API", "9-game")
	b.devSiteAsk("docs.example.org", "its docs", "9-game")
	access := loadTeamDevAccess("payments")
	all := "api.example.com " + access.Requests[0].Key + " docs.example.org " + access.Requests[1].Key
	if _, err := b.webSite("U07ASKER1", all, true); err == nil {
		t.Fatal("someone else allowed them")
	}
	if line, err := b.webSite("U07OWNER1", all, true); err != nil || line != "Allowed api.example.com, docs.example.org for the dev box." {
		t.Fatal(line, err)
	}
	said := 0
	for _, call := range channel.calls {
		if strings.HasPrefix(call, "postIn ") {
			said++
			if !strings.Contains(call, "allowed *api.example.com*, *docs.example.org*: the dev box reaches them now.") {
				t.Fatal(call)
			}
		}
	}
	if said != 1 {
		t.Fatal(channel.calls)
	}
}

// hi's own lines on a box's list are marked, and the proxy logs them so
// the watcher opens no incident for them; an owner's allow is news.
func TestDevSitesQuietForWatcher(t *testing.T) {
	b, _ := appTestDevPage(t)
	appTestSitesLookup(t)
	os.MkdirAll(filepath.Join(appDevDir("payments"), "net"), 0o700)
	changeMachinePeople(func(m *machinePeople) error { m.Domains = []string{"example.com"}; return nil })
	b.devSiteDecide(appPersonKey{User: "U07OWNER1", Name: "Olga"}, "docs.example.org", true)
	data, _ := os.ReadFile(appDevAllowFile("payments"))
	text := string(data)
	if !strings.Contains(text, "\nexample.com  # hi\n") || strings.Contains(text, "docs.example.org  # hi") || !strings.HasPrefix(text, "github.com  # hi\n") {
		t.Fatal(text)
	}

	path := filepath.Join(t.TempDir(), "allow")
	os.WriteFile(path, []byte("github.com\n"), 0o600)
	var logged bytes.Buffer
	list := &boxAllowlist{path: path, log: &boxNetworkLog{file: &logged}}
	list.reload()
	os.WriteFile(path, []byte("github.com\ncorp.example  # hi\napi.example.com\n"), 0o600)
	later := time.Now().Add(time.Minute)
	os.Chtimes(path, later, later)
	list.reload()
	if !strings.Contains(logged.String(), "allowlist-added-by-hi corp.example") || !strings.Contains(logged.String(), "allowlist-added api.example.com") || strings.Contains(logged.String(), "allowlist-added corp.example") {
		t.Fatal(logged.String())
	}
	if !list.allows("www.corp.example") {
		t.Fatal("the marked line doesn't allow")
	}
}
