package cli

import (
	"bytes"
	"context"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/caddyserver/certmagic"
	"github.com/libdns/libdns"
)

// useTestMachineID gives this machine a mark of its own.
func useTestMachineID(t *testing.T, id string) {
	previous := hiMachineID
	hiMachineID = func() string { return id }
	t.Cleanup(func() { hiMachineID = previous })
}

// A name is this machine's when its mark is there, or when nothing but a
// record at its own address, or one it kept before marks, is.
func TestDomainCheckSaysWhoHasAName(t *testing.T) {
	useTestMachineID(t, "me")
	domain := hiDomain{Domain: "dev.example.com", Zone: "example.com"}
	ip := netip.MustParseAddr("100.100.1.1")
	zone := map[string][]libdns.RR{
		"mine.dev":     {{Name: "mine.dev", Type: "A", Data: "100.100.9.9"}, {Name: "mine.dev", Type: "TXT", Data: `"hi machine=ws1 id=me"`}},
		"theirs.dev":   {{Name: "theirs.dev", Type: "A", Data: "100.100.2.2"}, {Name: "theirs.dev", Type: "TXT", Data: "hi machine=aiw11 id=other"}},
		"it.dev":       {{Name: "it.dev", Type: "CNAME", Data: "proxy.example.com."}},
		"here.dev":     {{Name: "here.dev", Type: "A", Data: "100.100.1.1"}},
		"before.dev":   {{Name: "before.dev", Type: "A", Data: "100.100.7.7"}},
		"note.dev":     {{Name: "note.dev", Type: "TXT", Data: "google-site-verification=x"}},
		"twomarks.dev": {{Name: "twomarks.dev", Type: "TXT", Data: "hi machine=ws1 id=me"}, {Name: "twomarks.dev", Type: "TXT", Data: "hi machine=aiw11 id=other"}},
	}
	for name, want := range map[string]string{
		"free.dev.example.com":   "",
		"mine.dev.example.com":   "",
		"here.dev.example.com":   "",
		"before.dev.example.com": "",
		"note.dev.example.com":   "",
		// Two marks: this machine's says it is its own.
		"twomarks.dev.example.com": "",
		"theirs.dev.example.com":   "theirs.dev.example.com is taken: the machine aiw11 has it",
		"it.dev.example.com":       "it.dev.example.com is taken: a record hi didn't make (CNAME proxy.example.com) has it",
	} {
		err := hiDomainCheck(zone, domain, []string{name}, ip, []string{"before.dev.example.com"})
		if got := ""; err != nil {
			got = err.Error()
			if got != want {
				t.Errorf("%s: %q, want %q", name, got, want)
			}
		} else if want != "" {
			t.Errorf("%s: free, want %q", name, want)
		}
	}
}

// An app whose name another machine on the domain has stays on its
// NetBird name, says why on the Network view, and doesn't ask the zone
// again every minute.
func TestTeamHTTPSRefusesAnotherMachinesName(t *testing.T) {
	b, _ := appTestDevPage(t)
	useTestMachineID(t, "me")
	dns := &appTestDNS{records: []libdns.Record{
		libdns.Address{Name: "payments-dev.dev", IP: netip.MustParseAddr("100.100.2.2")},
		libdns.TXT{Name: "payments-dev.dev", Text: "hi machine=aiw11 id=other"},
	}}
	hiTestDNS = dns
	saveHiDomain(hiDomain{Domain: "dev.example.com", Zone: "example.com", Provider: "test", Set: time.Now()})
	os.MkdirAll(filepath.Join(appDevDir("payments"), "netbird"), 0o700)
	os.WriteFile(filepath.Join(appDevDir("payments"), "netbird", "default.json"), []byte("{}"), 0o600)
	previousIP, previousIssue := appPeerIP, appHTTPSIssue
	appPeerIP = func(string) (netip.Addr, error) { return netip.MustParseAddr("100.100.14.131"), nil }
	issued := 0
	appHTTPSIssue = func(context.Context, hiDomain, hiDNSProvider) (*certmagic.Config, error) {
		issued++
		return certmagic.NewDefault(), nil
	}
	b.peerRestart = func() {}
	t.Cleanup(func() { hiTestDNS, appPeerIP, appHTTPSIssue = nil, previousIP, previousIssue })

	err := b.applyHTTPS()
	if err == nil || !strings.Contains(err.Error(), "payments-dev.dev.example.com is taken: the machine aiw11 has it") {
		t.Fatal(err)
	}
	if dns.sets != 0 || issued != 0 || appHTTPSDomain("payments") != "" || appTestDNSOf(dns, "A")[0].Data != "100.100.2.2" {
		t.Fatalf("took the name: %d sets, %d certificates", dns.sets, issued)
	}
	if said := hiDomainTakenSaid(appHTTPSTakenFile("payments")); !strings.Contains(said, "the machine aiw11") {
		t.Fatal(said)
	}
	// The next minute: the same answer, without reading the zone.
	dns.fail = io.ErrUnexpectedEOF
	if err := b.applyHTTPS(); err == nil || !strings.Contains(err.Error(), "aiw11") {
		t.Fatal(err)
	}
	// The other machine lets the name go; after the wait, hi takes it.
	dns.fail = nil
	dns.records = nil
	old := time.Now().Add(-hiDomainTakenWait - time.Minute)
	os.Chtimes(appHTTPSTakenFile("payments"), old, old)
	if err := b.applyHTTPS(); err != nil || fileExists(appHTTPSTakenFile("payments")) || len(appTestDNSOf(dns, "A")) != 4 || issued != 1 {
		t.Fatal(err, appTestDNSOf(dns, "A"), issued)
	}
}

// Names hi kept before marks get one, once, and are not refused for
// having no mark.
func TestTeamHTTPSMarksNamesFromBefore(t *testing.T) {
	b, _ := appTestDevPage(t)
	useTestMachineID(t, "me")
	ip := netip.MustParseAddr("100.100.14.131")
	names := []string{"payments.dev.example.com", "payments-stage.dev.example.com", "payments-preview.dev.example.com", "payments-dev.dev.example.com"}
	dns := &appTestDNS{}
	for _, name := range names {
		// At an address the peer had before: hi's own all the same.
		dns.records = append(dns.records, libdns.Address{Name: strings.TrimSuffix(name, ".example.com"), TTL: appHTTPSTTL, IP: netip.MustParseAddr("100.100.3.3")})
	}
	hiTestDNS = dns
	saveHiDomain(hiDomain{Domain: "dev.example.com", Zone: "example.com", Provider: "test", Set: time.Now()})
	os.MkdirAll(filepath.Join(appDevDir("payments"), "netbird"), 0o700)
	os.WriteFile(filepath.Join(appDevDir("payments"), "netbird", "default.json"), []byte("{}"), 0o600)
	writeTestFile(t, appHTTPSRecordFile("payments"), `{"names":["`+strings.Join(names, `","`)+`"],"ip":"100.100.14.131"}`, 0o600)
	previousIP, previousIssue := appPeerIP, appHTTPSIssue
	appPeerIP = func(string) (netip.Addr, error) { return ip, nil }
	appHTTPSIssue = func(context.Context, hiDomain, hiDNSProvider) (*certmagic.Config, error) {
		return certmagic.NewDefault(), nil
	}
	b.peerRestart = func() {}
	t.Cleanup(func() { hiTestDNS, appPeerIP, appHTTPSIssue = nil, previousIP, previousIssue })
	if err := b.applyHTTPS(); err != nil {
		t.Fatal(err)
	}
	if marks := appTestDNSOf(dns, "TXT"); len(marks) != 4 || dns.sets != 1 {
		t.Fatalf("%+v %d", marks, dns.sets)
	}
	if err := b.applyHTTPS(); err != nil || dns.sets != 1 || len(appTestDNSOf(dns, "TXT")) != 4 {
		t.Fatal(err, dns.sets)
	}
}

// hi server address refuses a name another machine has, before it moves
// the page; a rename takes the old name and its mark out of the zone.
func TestAdminPageNameOnASharedDomain(t *testing.T) {
	setupAdminTest(t)
	useTestMachineID(t, "me")
	dns := &appTestDNS{records: []libdns.Record{
		libdns.Address{Name: "console", IP: netip.MustParseAddr("100.100.2.2")},
		libdns.TXT{Name: "console", Text: "hi machine=aiw11 id=other"},
	}}
	hiTestDNS = dns
	t.Cleanup(func() { hiTestDNS = nil })
	saveHiDomain(hiDomain{Domain: "corp.example", Zone: "corp.example", Provider: "test", Set: time.Now()})
	config, _ := loadAdminConfig()
	config.Reach = adminReachPeer
	saveAdminConfig(config)
	os.MkdirAll(adminPeerDir(), 0o700)
	os.WriteFile(filepath.Join(adminPeerDir(), "default.json"), []byte("{}"), 0o600)
	os.WriteFile(filepath.Join(adminDir(), "netbird-fqdn"), []byte("ws1-admin.netbird.cloud\n"), 0o600)
	if label := adminDomainLabel(config); label != "ws1-admin" {
		t.Fatal(label)
	}
	err := adminAddressCommand([]string{"console.corp.example"}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "console.corp.example is taken: the machine aiw11 has it; pick another name") {
		t.Fatal(err)
	}
	if config, _ := loadAdminConfig(); config.DomainName != "" {
		t.Fatal("moved the page to a name another machine has")
	}
	// The page's name on this machine isn't an app's.
	if err := appCheckNotPageName("ws1-admin"); err == nil || !strings.Contains(err.Error(), "admin page's name") {
		t.Fatal(err)
	}
	if err := appCheckNotPageName("admin"); err == nil {
		t.Fatal("took admin for an app")
	}
	if err := appCheckNotPageName("payments"); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := adminAddressCommand([]string{"default"}, &out); err != nil || !strings.Contains(out.String(), "ws1-admin.corp.example") {
		t.Fatal(err, out.String())
	}
}

// hi keeps a name with its mark, and takes both out of the zone when it
// lets the name go; an IT team's TXT record at the name stays.
func TestDomainKeepAndDrop(t *testing.T) {
	useTestMachineID(t, "me")
	domain := hiDomain{Domain: "corp.example", Zone: "corp.example"}
	ip := netip.MustParseAddr("100.100.5.5")
	dns := &appTestDNS{records: []libdns.Record{libdns.TXT{Name: "ws1-admin", Text: "verification=x"}}}
	ctx := context.Background()
	if err := hiDomainKeep(ctx, dns, domain, []string{"ws1-admin.corp.example"}, ip, nil, appHTTPSTTL); err != nil {
		t.Fatal(err)
	}
	if a, txt := appTestDNSOf(dns, "A"), appTestDNSOf(dns, "TXT"); len(a) != 1 || a[0].Data != "100.100.5.5" || len(txt) != 2 {
		t.Fatalf("%+v %+v", a, txt)
	}
	// Again: no second mark.
	hiDomainKeep(ctx, dns, domain, []string{"ws1-admin.corp.example"}, ip, nil, appHTTPSTTL)
	if txt := appTestDNSOf(dns, "TXT"); len(txt) != 2 {
		t.Fatalf("%+v", txt)
	}
	if err := hiDomainDrop(ctx, dns, domain, []string{"ws1-admin.corp.example"}, ip, appHTTPSTTL); err != nil {
		t.Fatal(err)
	}
	if a, txt := appTestDNSOf(dns, "A"), appTestDNSOf(dns, "TXT"); len(a) != 0 || len(txt) != 1 || txt[0].Data != "verification=x" {
		t.Fatalf("%+v %+v", a, txt)
	}
}

// A machine whose page hi put at admin.<domain> before keeps it there.
func TestAdminKeepsItsOldName(t *testing.T) {
	setupAdminTest(t)
	adminKeepOldName()
	if config, _ := loadAdminConfig(); config.DomainName != "" {
		t.Fatal("named a page that had no name")
	}
	writeTestFile(t, adminHTTPSRecordFile(), `{"names":["admin.corp.example"],"ip":"100.100.5.5"}`, 0o600)
	adminKeepOldName()
	if config, _ := loadAdminConfig(); config.DomainName != "admin" {
		t.Fatalf("%+v", config)
	}
	writeTestFile(t, adminHTTPSRecordFile(), `{"names":["ws1-admin.corp.example"],"ip":"100.100.5.5"}`, 0o600)
	config, _ := loadAdminConfig()
	config.DomainName = ""
	saveAdminConfig(config)
	adminKeepOldName()
	if config, _ := loadAdminConfig(); config.DomainName != "" {
		t.Fatalf("%+v", config)
	}
}
