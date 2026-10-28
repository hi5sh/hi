package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/caddyserver/certmagic"
	"github.com/libdns/libdns"
)

// teamTestDNS is a zone in memory, as a DNS provider has it.
type teamTestDNS struct {
	mu      sync.Mutex
	records []libdns.Record
	sets    int
	fail    error
}

func (d *teamTestDNS) GetRecords(ctx context.Context, zone string) ([]libdns.Record, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.fail != nil {
		return nil, d.fail
	}
	return append([]libdns.Record{}, d.records...), nil
}

func (d *teamTestDNS) SetRecords(ctx context.Context, zone string, recs []libdns.Record) ([]libdns.Record, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.sets++
	for _, rec := range recs {
		kept := d.records[:0]
		for _, old := range d.records {
			if old.RR().Name != rec.RR().Name || old.RR().Type != rec.RR().Type {
				kept = append(kept, old)
			}
		}
		d.records = append(kept, rec)
	}
	return recs, nil
}

func (d *teamTestDNS) AppendRecords(ctx context.Context, zone string, recs []libdns.Record) ([]libdns.Record, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.records = append(d.records, recs...)
	return recs, nil
}

func (d *teamTestDNS) DeleteRecords(ctx context.Context, zone string, recs []libdns.Record) ([]libdns.Record, error) {
	return recs, nil
}

// teamTestCert is a certificate of its own for names, as Let's Encrypt's
// would be.
func teamTestCert(t *testing.T, names ...string) tls.Certificate {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: names[0]}, DNSNames: names,
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

// hi net domain checks the provider can read the zone before it keeps
// its token.
func TestNetDomain(t *testing.T) {
	setupTeamTest(t)
	dns := &teamTestDNS{}
	hiTestDNS = dns
	t.Cleanup(func() { hiTestDNS = nil })
	var out bytes.Buffer
	if err := runNetDomain(nil, strings.NewReader(""), &out, io.Discard); err != nil || !strings.Contains(out.String(), "plain http") {
		t.Fatal(err, out.String())
	}
	dns.fail = errors.New("401 Unauthorized")
	if err := runNetDomain([]string{"dev.example.com", "--dns", "test"}, strings.NewReader(""), &out, io.Discard); err == nil || !strings.Contains(err.Error(), "didn't let hi read the zone example.com") {
		t.Fatal(err)
	}
	if _, ok := loadHiDomain(); ok {
		t.Fatal("kept a token the provider refused")
	}
	dns.fail = nil
	out.Reset()
	if err := runNetDomain([]string{"dev.example.com", "--dns", "test"}, strings.NewReader(""), &out, io.Discard); err != nil || !strings.Contains(out.String(), "https://<team>.dev.example.com/") {
		t.Fatal(err, out.String())
	}
	domain, ok := loadHiDomain()
	if !ok || domain.Zone != "example.com" || domain.addressName("quarry", teamSlotProduction) != "quarry.dev.example.com" ||
		domain.addressName("quarry", teamSlotPreview) != "quarry-preview.dev.example.com" || domain.addressName("quarry", teamAddressDev) != "quarry-dev.dev.example.com" || domain.recordName("quarry.dev.example.com") != "quarry.dev" {
		t.Fatalf("%+v", domain)
	}
	if info, _ := os.Stat(hiDomainPath()); info.Mode().Perm() != 0o600 {
		t.Fatal("the token is readable by others:", info.Mode())
	}
	for _, args := range [][]string{{"dev.example.com", "--dns", "nobody"}, {"*.example.com", "--dns", "test"}, {"dev.example.com"}, {"dev.example.com", "--dns", "test", "--zone", "other.org"}} {
		if err := runNetDomain(args, strings.NewReader(""), io.Discard, io.Discard); err == nil {
			t.Errorf("%q", args)
		}
	}
	// A provider's settings go to its libdns package by their names.
	gandi := hiDomain{Provider: "gandi", Settings: map[string]string{"bearer_token": "pat"}}
	provider, err := gandi.provider()
	data, _ := json.Marshal(provider)
	if err != nil || !strings.Contains(string(data), `"bearer_token":"pat"`) {
		t.Fatal(err, string(data))
	}
	// The email for Let's Encrypt: optional, asked after the token, and
	// changed on its own.
	if err := runNetDomain([]string{"--email", "not-an-address"}, strings.NewReader(""), io.Discard, io.Discard); err == nil {
		t.Fatal("took an email that isn't one")
	}
	out.Reset()
	if err := runNetDomain([]string{"--email", "ops@example.com"}, strings.NewReader(""), &out, io.Discard); err != nil || !strings.Contains(out.String(), "writes to ops@example.com") {
		t.Fatal(err, out.String())
	}
	if domain, _ := loadHiDomain(); domain.Email != "ops@example.com" || domain.Provider != "test" {
		t.Fatalf("%+v", domain)
	}
	out.Reset()
	runNetDomain(nil, strings.NewReader(""), &out, io.Discard)
	if !strings.Contains(out.String(), "writes to ops@example.com") {
		t.Fatal(out.String())
	}
	if err := runNetDomain([]string{"--unset"}, strings.NewReader(""), io.Discard, io.Discard); err != nil || fileExists(hiDomainPath()) {
		t.Fatal(err)
	}
}

// A team's addresses go to https: their names at the peer's address, the
// certificate, the peer's 443, and the links; and back without a domain.
func TestTeamHTTPS(t *testing.T) {
	b, _ := teamTestDevPage(t)
	dns := &teamTestDNS{}
	hiTestDNS = dns
	saveHiDomain(hiDomain{Domain: "dev.example.com", Zone: "example.com", Provider: "test", Set: time.Now()})
	os.MkdirAll(filepath.Join(teamDevDir("payments"), "netbird"), 0o700)
	os.WriteFile(filepath.Join(teamDevDir("payments"), "netbird", "default.json"), []byte("{}"), 0o600)
	ip := netip.MustParseAddr("100.100.14.131")
	previousIP, previousIssue := teamPeerIP, teamHTTPSIssue
	teamPeerIP = func(string) (netip.Addr, error) { return ip, nil }
	issued := 0
	teamHTTPSIssue = func(ctx context.Context, domain hiDomain, provider hiDNSProvider) (*certmagic.Config, error) {
		issued++
		config := certmagic.NewDefault()
		if _, err := config.CacheUnmanagedTLSCertificate(ctx, teamTestCert(t, "*."+domain.Domain), nil); err != nil {
			return nil, err
		}
		return config, nil
	}
	restarts := 0
	b.peerRestart = func() { restarts++ }
	t.Cleanup(func() { hiTestDNS, teamPeerIP, teamHTTPSIssue = nil, previousIP, previousIssue })

	if teamInternalURL("payments", teamAddressDev) == "" || strings.HasPrefix(teamInternalURL("payments", teamAddressDev), "https") {
		t.Fatal("https before there's a domain")
	}
	if err := b.applyHTTPS(); err != nil {
		t.Fatal(err)
	}
	records, _ := dns.GetRecords(context.Background(), "example.com.")
	var names []string
	for _, record := range records {
		if record.RR().Type != "A" || record.RR().Data != ip.String() {
			t.Fatalf("%+v", record)
		}
		names = append(names, record.RR().Name)
	}
	if strings.Join(names, " ") != "payments.dev payments-preview.dev payments-dev.dev" {
		t.Fatal(names)
	}
	if teamInternalURL("payments", teamSlotProduction) != "https://payments.dev.example.com/" || teamInternalURL("payments", teamSlotPreview) != "https://payments-preview.dev.example.com/" ||
		teamInternalURL("payments", teamAddressDev) != "https://payments-dev.dev.example.com/" || restarts != 1 || issued != 1 {
		t.Fatal(teamInternalURL("payments", teamAddressDev), restarts, issued)
	}
	if script := teamPeerScript("payments", "10.0.0.3:2222", true); !strings.Contains(script, "127.0.0.1:443 unix:/hi-internal/https.sock") {
		t.Fatal(script)
	}
	// Nothing changed: no record, certificate, or restart again.
	if err := b.applyHTTPS(); err != nil || dns.sets != 1 || issued != 1 || restarts != 1 {
		t.Fatal(err, dns.sets, issued, restarts)
	}
	// A new address for the peer: its record follows.
	ip = netip.MustParseAddr("100.100.20.7")
	b.applyHTTPS()
	if records, _ := dns.GetRecords(context.Background(), "example.com."); len(records) != 3 || records[0].RR().Data != "100.100.20.7" || dns.sets != 2 || restarts != 1 {
		t.Fatalf("%+v %d", records, restarts)
	}
	// A failure waits before trying again, not every minute.
	b.httpsCerts.mu.Lock()
	b.httpsCerts.config, b.httpsCerts.domain = nil, hiDomain{}
	b.httpsCerts.mu.Unlock()
	working := teamHTTPSIssue
	teamHTTPSIssue = func(context.Context, hiDomain, hiDNSProvider) (*certmagic.Config, error) {
		issued++
		return nil, errors.New("too many failed authorizations")
	}
	if err := b.applyHTTPS(); err == nil || !strings.Contains(err.Error(), "tries again in 30m") || b.applyHTTPS() != nil || issued != 2 {
		t.Fatal(err, issued)
	}
	teamHTTPSIssue = working
	b.httpsCerts.mu.Lock()
	b.httpsCerts.failed = time.Time{}
	b.httpsCerts.mu.Unlock()
	if err := b.applyHTTPS(); err != nil || issued != 3 {
		t.Fatal(err, issued)
	}
	// The https socket answers with the certificate, and passes each name
	// to its address.
	relays := map[string]http.Handler{}
	for _, address := range teamHTTPSAddresses {
		relays[address] = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, address) })
	}
	for name, want := range map[string]string{"payments.dev.example.com": "production", "payments-preview.dev.example.com": "preview", "payments-dev.dev.example.com": "dev", "other.dev.example.com": "nothing of this team's"} {
		server, client := net.Pipe()
		go func() {
			listener := &teamOneConn{conn: server}
			http.Serve(serveHTTPS(listener, &b.httpsCerts), teamHTTPSHandler("payments", relays))
		}()
		conn := tls.Client(client, &tls.Config{ServerName: name, InsecureSkipVerify: true})
		if err := conn.Handshake(); err != nil || conn.ConnectionState().PeerCertificates[0].DNSNames[0] != "*.dev.example.com" {
			t.Fatal(err)
		}
		io.WriteString(conn, "GET / HTTP/1.1\r\nHost: "+name+"\r\nConnection: close\r\n\r\n")
		body, _ := io.ReadAll(conn)
		if !strings.Contains(string(body), want) {
			t.Errorf("%s: %s", name, body)
		}
		conn.Close()
	}
	// A team that keeps its addresses off the machine's domain goes back
	// to the NetBird name; a host name IT keeps takes its place.
	var out bytes.Buffer
	if err := teamAddressesCommand([]string{"payments", "netbird"}, &out); err != nil || !strings.Contains(out.String(), "NetBird name") {
		t.Fatal(err, out.String())
	}
	if err := b.applyHTTPS(); err != nil || teamHTTPSDomain("payments") != "" || restarts != 2 || strings.HasPrefix(teamInternalURL("payments", teamAddressDev), "https") {
		t.Fatal(err, restarts, teamInternalURL("payments", teamAddressDev))
	}
	if err := teamAddressesCommand([]string{"payments", "Payments.Corp.Example."}, &out); err != nil {
		t.Fatal(err)
	}
	if teamInternalURL("payments", teamSlotProduction) != "http://payments.corp.example/" || teamInternalURL("payments", teamAddressDev) != "http://payments.corp.example:8888/" {
		t.Fatal(teamInternalURL("payments", teamAddressDev))
	}
	if err := teamAddressesCommand([]string{"payments", "not a host"}, &out); err == nil {
		t.Fatal("a host name with spaces")
	}
	if err := teamAddressesCommand([]string{"payments", "domain"}, &out); err != nil {
		t.Fatal(err)
	}
	if err := b.applyHTTPS(); err != nil || teamInternalURL("payments", teamAddressDev) != "https://payments-dev.dev.example.com/" || restarts != 3 {
		t.Fatal(err, restarts, teamInternalURL("payments", teamAddressDev))
	}
	// Without the domain, plain http on the NetBird name again.
	os.Remove(hiDomainPath())
	if err := b.applyHTTPS(); err != nil || teamHTTPSDomain("payments") != "" || restarts != 4 || strings.HasPrefix(teamInternalURL("payments", teamAddressDev), "https") ||
		strings.HasPrefix(teamInternalURL("payments", teamSlotProduction), "https") {
		t.Fatal(err, restarts)
	}
	if strings.Contains(teamPeerScript("payments", "10.0.0.3:2222", true), ":443") {
		t.Fatal("the peer still passes 443 on")
	}
}

// A team's dev page on https from hi 2.5.46, at <team>.<domain>, moves to
// <team>-dev, and production's internal link to https, in place.
func TestTeamHTTPSFromDevPageOnly(t *testing.T) {
	b, _ := teamTestDevPage(t)
	hiTestDNS = &teamTestDNS{}
	saveHiDomain(hiDomain{Domain: "dev.example.com", Zone: "example.com", Provider: "test", Set: time.Now()})
	os.MkdirAll(filepath.Join(teamDevDir("payments"), "netbird"), 0o700)
	os.WriteFile(filepath.Join(teamDevDir("payments"), "netbird", "default.json"), []byte("{}"), 0o600)
	os.WriteFile(teamHTTPSOldHostFile("payments"), []byte("payments.dev.example.com\n"), 0o600)
	os.WriteFile(teamHTTPSRecordFile("payments"), []byte(`{"name": "payments.dev.example.com", "ip": "100.100.14.131"}`), 0o600)
	previousIP, previousIssue := teamPeerIP, teamHTTPSIssue
	teamPeerIP = func(string) (netip.Addr, error) { return netip.MustParseAddr("100.100.14.131"), nil }
	teamHTTPSIssue = func(ctx context.Context, domain hiDomain, provider hiDNSProvider) (*certmagic.Config, error) {
		return certmagic.NewDefault(), nil
	}
	t.Cleanup(func() { hiTestDNS, teamPeerIP, teamHTTPSIssue = nil, previousIP, previousIssue })
	production := teamPreview{Slot: teamSlotProduction, URL: teamInternalURL("payments", teamSlotProduction), Expose: teamInternalExpose + teamSlotProduction, Container: "app-1"}
	os.MkdirAll(filepath.Dir(teamSlotPath("payments", teamSlotProduction)), 0o700)
	if err := saveTeamSlot("payments", production); err != nil {
		t.Fatal(err)
	}
	b.peerRestart = func() { b.applyInternalLinks() }
	if err := b.applyHTTPS(); err != nil {
		t.Fatal(err)
	}
	if fileExists(teamHTTPSOldHostFile("payments")) || teamInternalURL("payments", teamAddressDev) != "https://payments-dev.dev.example.com/" {
		t.Fatal(teamInternalURL("payments", teamAddressDev))
	}
	if state, _ := loadTeamSlot("payments", teamSlotProduction); state.URL != "https://payments.dev.example.com/" || state.Container != "app-1" {
		t.Fatalf("%+v", state)
	}
	if records, _ := hiTestDNS.GetRecords(context.Background(), "example.com."); len(records) != 3 {
		t.Fatalf("%+v", records)
	}
}

// teamOneConn is a listener with one connection.
type teamOneConn struct {
	conn net.Conn
	done bool
	mu   sync.Mutex
}

func (l *teamOneConn) Accept() (net.Conn, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.done {
		select {}
	}
	l.done = true
	return l.conn, nil
}
func (l *teamOneConn) Close() error   { return nil }
func (l *teamOneConn) Addr() net.Addr { return &net.UnixAddr{Name: "test", Net: "unix"} }
