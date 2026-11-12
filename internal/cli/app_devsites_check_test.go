package cli

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// No test reaches a registry or a resolver on the internet: the check's
// lookups fail unless a test gives them answers.
func init() {
	appSiteLookups.registered = func(context.Context, string) (time.Time, error) {
		return time.Time{}, errors.New("no lookups in tests")
	}
	appSiteLookups.harmful = func(context.Context, string) (string, error) {
		return "", errors.New("no lookups in tests")
	}
}

// appTestSiteFacts answers the check's lookups from a table: a name's
// rank and age, and a host a threat list names.
func appTestSiteFacts(t *testing.T, ranks map[string]int, ages map[string]time.Duration, listed map[string]string) {
	t.Helper()
	saved := appSiteLookups
	appSiteLookups.rank = func(name string) (int, error) { return ranks[name], nil }
	appSiteLookups.registered = func(_ context.Context, name string) (time.Time, error) {
		if age, ok := ages[name]; ok {
			return time.Now().Add(-age), nil
		}
		return time.Time{}, errors.New(".io has no RDAP")
	}
	appSiteLookups.harmful = func(_ context.Context, host string) (string, error) { return listed[host], nil }
	t.Cleanup(func() { appSiteLookups = saved })
}

const appTestYear = 365 * 24 * time.Hour

// The rules: popular, old, unlisted, and nobody else's to publish on.
func TestSiteCheckRules(t *testing.T) {
	setupTeamTest(t)
	appTestSiteFacts(t,
		map[string]int{"yahoo.com": 40, "google.com": 1, "github.io": 900, "itch.io": 2000, "young.example": 5000, "deep.example": 400_000, "kenney.nl": 60_000},
		map[string]time.Duration{"yahoo.com": 29 * appTestYear, "google.com": 28 * appTestYear, "young.example": 41 * 24 * time.Hour, "deep.example": 9 * appTestYear, "kenney.nl": 19 * appTestYear, "bad.example": 3 * appTestYear},
		map[string]string{"x.bad.example": "Quad9"})
	now := time.Now()
	for host, want := range map[string]string{
		"query1.finance.yahoo.com":  "",
		"kenney.nl":                 "",
		"itch.io":                   "", // no age, but among the 10,000 most used
		"drive.google.com":          "anyone can put files or data there",
		"someone.github.io":         "anyone can make a name under github.io",
		"young.example":             "was registered 41 days ago",
		"deep.example":              "isn't among the 100,000 most used",
		"x.bad.example":             "Quad9 lists it as harmful",
		"nowhere.example":           "isn't among the 100,000 most used",
		"cluster0.abcd.mongodb.net": "anyone can put files or data there",
	} {
		ok, because := appSiteCheckFacts(host, now).pass(now)
		if want == "" && !ok {
			t.Errorf("%s: %s", host, because)
		}
		if want != "" && (ok || !strings.Contains(because, want)) {
			t.Errorf("%s: %v %q, want %q", host, ok, because, want)
		}
	}
	if words := appSiteCheckFacts("query1.finance.yahoo.com", now).words(now); words != "yahoo.com, top 1,000, 29 years old" {
		t.Fatal(words)
	}
	// Kept a week: a second check asks nobody.
	appSiteLookups.rank = func(string) (int, error) { t.Fatal("asked again"); return 0, nil }
	appSiteCheckFacts("query1.finance.yahoo.com", now)
	// The threat lists not answering isn't a pass.
	appSiteLookups.harmful = func(context.Context, string) (string, error) {
		return "", errors.New("Quad9 and Cloudflare didn't answer")
	}
	if ok, because := appSiteCheckFacts("www.yahoo.com", now).pass(now); ok || !strings.Contains(because, "didn't answer") {
		t.Fatal(ok, because)
	}
}

// Tranco's list is fetched, unzipped, and read for a name's rank.
func TestSiteCheckTranco(t *testing.T) {
	setupTeamTest(t)
	if _, err := appSiteTrancoRank("yahoo.com"); !errors.Is(err, errNoTranco) {
		t.Fatal(err)
	}
	var zipped bytes.Buffer
	archive := zip.NewWriter(&zipped)
	file, _ := archive.Create("top-1m.csv")
	file.Write([]byte("1,google.com\r\n2,yahoo.com\r\n3,notyahoo.com\r\n"))
	archive.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(zipped.Bytes()) }))
	defer server.Close()
	client := server.Client()
	client.Transport = rewriteTransport{to: server.URL}
	if err := appSiteKeepTranco(client); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]int{"yahoo.com": 2, "google.com": 1, "ahoo.com": 0, "example.com": 0} {
		if rank, err := appSiteTrancoRank(name); err != nil || rank != want {
			t.Errorf("%s: %d %v", name, rank, err)
		}
	}
}

// rewriteTransport sends every request to a test server.
type rewriteTransport struct{ to string }

func (r rewriteTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	clone := request.Clone(request.Context())
	target, _ := http.NewRequest(request.Method, r.to+request.URL.Path, nil)
	clone.URL, clone.Host = target.URL, target.URL.Host
	return http.DefaultTransport.RoundTrip(clone)
}

// An ask the rules vouch for is allowed at once, for 7 days, said in the
// day's post with Keep and Undo; one they don't goes to the owners with
// what hi found.
func TestSiteCheckAllows(t *testing.T) {
	b, _ := appTestDevPage(t)
	appTestLookup(t)
	appTestSiteFacts(t, map[string]int{"yahoo.com": 40, "example.com": 500}, map[string]time.Duration{"yahoo.com": 29 * appTestYear, "example.com": 30 * 24 * time.Hour}, nil)
	channel := &fakeTeamChannel{}
	b.channel = channel
	os.MkdirAll(filepath.Join(appDevDir("payments"), "net"), 0o700)
	os.WriteFile(filepath.Join(appDevDir("payments"), "net", "network.log"), nil, 0o600)

	answer, err := b.devSiteAsk("query1.finance.yahoo.com", "daily prices", "9-game")
	if err != nil || answer.State != appDevSiteAllowed || !strings.Contains(answer.Message, "Allowed by hi's check for 7 days: yahoo.com, top 1,000, 29 years old") {
		t.Fatal(answer, err)
	}
	if !boxHostAllowed(appTestAllowFile(t, "payments"), "query1.finance.yahoo.com") {
		t.Fatal("not on the box's list")
	}
	// hi's line: the watcher opens no incident for it, the day's post says it.
	if raw, _ := os.ReadFile(appDevAllowFile("payments")); !strings.Contains(string(raw), "\nquery1.finance.yahoo.com  # hi\n") {
		t.Fatal(string(raw))
	}
	access := loadTeamDevAccess("payments")
	site := access.Allowed[0]
	if site.By != appSiteCheckBy || site.Check == nil || site.Check.Until.Sub(time.Now()) < 6*24*time.Hour || access.Today == nil || access.Today.Thread == "" {
		t.Fatal(access)
	}
	if !strings.Contains(channel.calls[0], "hi allowed 1 site for the dev box by itself today") || !strings.Contains(channel.calls[0], "[Keep query1.finance.yahoo.com hi_site_keep=query1.finance.yahoo.com "+site.Check.Key+"]") {
		t.Fatal(channel.calls)
	}
	if log, _ := os.ReadFile(filepath.Join(appDevDir("payments"), "net", "network.log")); !strings.Contains(string(log), "checked query1.finance.yahoo.com") {
		t.Fatal(string(log))
	}

	// Too young: the owners' post says why.
	answer, _ = b.devSiteAsk("api.example.com", "the API", "9-game")
	if answer.State != appDevSiteAsked {
		t.Fatal(answer)
	}
	var post string
	for _, call := range channel.calls {
		if strings.HasPrefix(call, "postButtons ") && strings.Contains(call, "api.example.com") {
			post = call
		}
	}
	if !strings.Contains(post, "hi's check: not allowed by itself, example.com was registered 30 days ago") {
		t.Fatal(post)
	}

	// The second site of the day redraws the day's post.
	appTestSiteFacts(t, map[string]int{"yahoo.com": 40, "example.org": 90}, map[string]time.Duration{"yahoo.com": 29 * appTestYear, "example.org": 25 * appTestYear}, nil)
	b.devSiteAsk("docs.example.org", "its docs", "9-game")
	if last := channel.calls[len(channel.calls)-1]; !strings.HasPrefix(last, "redraw ") || !strings.Contains(last, "hi allowed 2 sites") {
		t.Fatal(last)
	}

	// Keep from Slack: the owner's for good. Undo: off, and a no.
	access = loadTeamDevAccess("payments")
	keys := map[string]string{}
	for _, site := range access.Allowed {
		keys[site.Host] = site.Check.Key
	}
	if _, err := b.webSiteCheck("U07ASKER1", "query1.finance.yahoo.com "+keys["query1.finance.yahoo.com"], true); err == nil {
		t.Fatal("someone else kept it")
	}
	if _, err := b.webSiteCheck("U07OWNER1", "query1.finance.yahoo.com made-up", true); err == nil {
		t.Fatal("a made-up key")
	}
	if line, err := b.webSiteCheck("U07OWNER1", "query1.finance.yahoo.com "+keys["query1.finance.yahoo.com"], true); err != nil || line != "Kept query1.finance.yahoo.com for the dev box." {
		t.Fatal(line, err)
	}
	if line, err := b.webSiteCheck("U07OWNER1", "docs.example.org "+keys["docs.example.org"], false); err != nil || !strings.HasPrefix(line, "Undid docs.example.org") {
		t.Fatal(line, err)
	}
	if last := channel.calls[len(channel.calls)-1]; !strings.HasPrefix(last, "settle ") || !strings.Contains(last, "kept it.") || !strings.Contains(last, "undid it.") {
		t.Fatal(last)
	}
	file := appTestAllowFile(t, "payments")
	if !boxHostAllowed(file, "query1.finance.yahoo.com") || boxHostAllowed(file, "docs.example.org") {
		t.Fatal(file)
	}
	// The no is final: asked again, the check doesn't look.
	if answer, _ := b.devSiteAsk("docs.example.org", "its docs", "9-game"); answer.State != appDevSiteDenied {
		t.Fatal(answer)
	}
	// Kept for good: past seven days it stays.
	b.devSitesExpire(time.Now().Add(8 * 24 * time.Hour))
	if !boxHostAllowed(appTestAllowFile(t, "payments"), "query1.finance.yahoo.com") {
		t.Fatal("a kept site ended")
	}
}

// What nobody kept ends after seven days; the check stops at ten a day,
// when it's off, on an incident about the dev box, and after a site a
// threat list names, until an owner turns it on.
func TestSiteCheckLimits(t *testing.T) {
	b, _ := appTestDevPage(t)
	appTestLookup(t)
	appTestSiteFacts(t, map[string]int{"yahoo.com": 40, "example.com": 70, "example.org": 80}, map[string]time.Duration{"yahoo.com": 29 * appTestYear, "example.com": 30 * appTestYear, "example.org": 30 * appTestYear}, map[string]string{"api.example.com": "Quad9"})
	b.channel = &fakeTeamChannel{}
	os.MkdirAll(filepath.Join(appDevDir("payments"), "net"), 0o700)

	b.devSiteAsk("query1.finance.yahoo.com", "prices", "")
	b.devSitesExpire(time.Now().Add(8 * 24 * time.Hour))
	if boxHostAllowed(appTestAllowFile(t, "payments"), "query1.finance.yahoo.com") || len(loadTeamDevAccess("payments").Allowed) != 0 {
		t.Fatal("not ended")
	}
	if boxHostAllowed(appDevAllowHosts("payments"), "query1.finance.yahoo.com") {
		t.Fatal("still allowed")
	}

	// Ten a day.
	access := loadTeamDevAccess("payments")
	access.Today = &appDevCheckDay{Day: time.Now().Format(time.DateOnly)}
	for i := 0; i < appSiteCheckPerDay; i++ {
		access.Today.Sites = append(access.Today.Sites, appDevCheckEntry{Host: "s" + string(rune('a'+i)) + ".example", Key: "k"})
	}
	saveTeamDevAccess("payments", access)
	if answer, _ := b.devSiteAsk("query1.finance.yahoo.com", "prices", ""); answer.State != appDevSiteAsked {
		t.Fatal("past ten a day:", answer)
	}
	if ask := loadTeamDevAccess("payments").Requests[0]; !strings.Contains(ask.Found, "as many as it may") {
		t.Fatal(ask.Found)
	}
	access = loadTeamDevAccess("payments")
	access.Today, access.Requests = nil, nil
	saveTeamDevAccess("payments", access)

	// A listed site pauses the check, and its Allow asks first.
	answer, _ := b.devSiteAsk("api.example.com", "the API", "")
	ask := loadTeamDevAccess("payments").Requests[0]
	if answer.State != appDevSiteAsked || !strings.Contains(ask.Found, "Quad9 lists it as harmful") || appDevSiteConfirm(ask) == "" {
		t.Fatal(answer, ask)
	}
	if paused := loadTeamDevAccess("payments").CheckPaused; !strings.Contains(paused, "api.example.com") {
		t.Fatal(paused)
	}
	if answer, _ := b.devSiteAsk("docs.example.org", "docs", ""); answer.State != appDevSiteAsked {
		t.Fatal("paused, it allowed:", answer)
	}
	var out bytes.Buffer
	if err := appDevSitesCheckCommand("payments", []string{"rules"}, &out); err != nil || loadTeamDevAccess("payments").CheckPaused != "" {
		t.Fatal(err)
	}

	// Off: the owners decide everything.
	access = loadTeamDevAccess("payments")
	access.Requests = nil
	saveTeamDevAccess("payments", access)
	if err := appDevSitesCheckCommand("payments", []string{"off"}, &out); err != nil {
		t.Fatal(err)
	}
	if config, _ := loadTeam("payments"); config.SitesCheck != "off" {
		t.Fatal(config.SitesCheck)
	}
	if answer, err := b.devSiteAsk("query1.finance.yahoo.com", "x", ""); answer.State != appDevSiteAsked {
		t.Fatal("off, it allowed:", answer, err)
	}
	if err := appDevSitesCheckCommand("payments", []string{"model"}, &out); err == nil {
		t.Fatal("model isn't built")
	}
	appDevSitesCheckCommand("payments", []string{"rules"}, &out)
	access = loadTeamDevAccess("payments")
	access.Requests = nil
	saveTeamDevAccess("payments", access)

	// An incident about the dev box.
	b.openIncident(appIncident{Kind: "watch", Slot: appSlotProduction, Title: "payments may now reach x.example from member box " + appDevBox("payments")}, "x", "", nil)
	if answer, err := b.devSiteAsk("query1.finance.yahoo.com", "x", ""); answer.State != appDevSiteAsked {
		t.Fatal("an incident is open, it allowed:", answer, err)
	}
}
