package main

// A team's addresses on https, when the machine has a domain (hi net
// domain): the team's service keeps their names in the domain's DNS,
// pointing at the team's NetBird address, has the domain's wildcard
// certificate (certmagic, from Let's Encrypt over DNS-01, renewed as it
// nears its end), and answers TLS on a socket the team's peer passes its
// port 443 to, passing each name to its address. Once the names and the
// certificate are there, the links are https://<team>.<domain>/ for
// production, <team>-preview for the preview, and <team>-dev for the dev
// page, which the owners get in their DMs.

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/caddyserver/certmagic"
	"github.com/libdns/libdns"
	"go.uber.org/zap"
)

const (
	teamHTTPSAddress = "https" // its internal socket's name
	teamHTTPSPort    = 443
	teamHTTPSTTL     = 5 * time.Minute
)

// teamHTTPSDomainFile holds the domain the addresses are on over https,
// once their records and the certificate are there; the peer passes 443
// on, and the links are https, while it does.
func teamHTTPSDomainFile(team string) string { return filepath.Join(teamDevDir(team), "https-domain") }

// teamHTTPSOldHostFile is where hi 2.5.46 kept the dev page's https name,
// when only the dev page was on https, at <team>.<domain>.
func teamHTTPSOldHostFile(team string) string { return filepath.Join(teamDevDir(team), "https-host") }

// teamHTTPSDomain is the domain the team's addresses are on over https, or
// "" while they have none.
func teamHTTPSDomain(team string) string {
	data, err := os.ReadFile(teamHTTPSDomainFile(team))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// teamHTTPSHost is an address's https name, or "" while it has none.
func teamHTTPSHost(team, address string) string {
	domain := teamHTTPSDomain(team)
	if domain == "" {
		return ""
	}
	return hiDomain{Domain: domain}.addressName(team, address)
}

// teamHTTPSAddresses are the addresses on https, each at its own name.
var teamHTTPSAddresses = []string{teamSlotProduction, teamSlotPreview, teamAddressDev}

// teamHTTPSRecord is the records hi last set, so it sets them only when
// the peer's address or the names change.
type teamHTTPSRecord struct {
	Names []string `json:"names"`
	IP    string   `json:"ip"`
	Set   time.Time
}

func teamHTTPSRecordFile(team string) string {
	return filepath.Join(teamDevDir(team), "https-record.json")
}

// teamPeerIP is the team's peer's NetBird address; tests replace it.
var teamPeerIP = func(team string) (netip.Addr, error) {
	engine, _, err := detectBoxEngine()
	if err != nil {
		return netip.Addr{}, err
	}
	out, err := engine.output("exec", teamDevNetContainer(team), "netbird", "status", "--json")
	if err != nil {
		return netip.Addr{}, err
	}
	var status struct {
		IP string `json:"netbirdIp"`
	}
	if err := json.Unmarshal([]byte(out), &status); err != nil {
		return netip.Addr{}, err
	}
	prefix, err := netip.ParsePrefix(status.IP)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("the peer's NetBird address %q: %w", status.IP, err)
	}
	return prefix.Addr(), nil
}

// teamHTTPSCerts is the broker's certificates: certmagic's, for the
// domain it was made for.
type teamHTTPSCerts struct {
	mu     sync.Mutex
	domain hiDomain
	config *certmagic.Config
	ready  bool
	// failed is when getting the certificate last failed: Let's Encrypt
	// takes few failures an hour, so the next try waits.
	failed time.Time
}

// teamHTTPSRetry is how long after a failure hi tries for the certificate
// again.
const teamHTTPSRetry = 30 * time.Minute

// teamHTTPSIssue has the certificate for a domain: certmagic's, on the
// machine's shared storage; tests replace it.
var teamHTTPSIssue = func(ctx context.Context, domain hiDomain, dns hiDNSProvider) (*certmagic.Config, error) {
	logger := zap.NewNop()
	var config *certmagic.Config
	cache := certmagic.NewCache(certmagic.CacheOptions{
		GetConfigForCert: func(certmagic.Certificate) (*certmagic.Config, error) { return config, nil },
		Logger:           logger,
	})
	config = certmagic.New(cache, certmagic.Config{Storage: &certmagic.FileStorage{Path: hiCertsDir()}, Logger: logger})
	config.Issuers = []certmagic.Issuer{certmagic.NewACMEIssuer(config, certmagic.ACMEIssuer{
		CA: certmagic.LetsEncryptProductionCA, Email: domain.Email, Agreed: true, DisableHTTPChallenge: true, DisableTLSALPNChallenge: true, Logger: logger,
		// No resolvers of hi's own: certmagic then asks the zone's own
		// servers whether the challenge is there. A public resolver may
		// remember the name was missing for the zone's negative TTL (three
		// hours at Gandi), longer than the wait (quarry, 2026-10-26).
		DNS01Solver: &certmagic.DNS01Solver{DNSManager: certmagic.DNSManager{DNSProvider: dns, PropagationTimeout: 10 * time.Minute, Logger: logger}},
	})}
	if err := config.ManageSync(ctx, []string{"*." + domain.Domain}); err != nil {
		cache.Stop()
		return nil, err
	}
	return config, nil
}

// getCertificate answers the TLS handshake with the domain's certificate.
func (c *teamHTTPSCerts) getCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	c.mu.Lock()
	config := c.config
	c.mu.Unlock()
	if config == nil {
		return nil, errors.New("no certificate yet")
	}
	return config.GetCertificate(hello)
}

// keepHTTPS has the team's addresses on https while the machine has a
// domain, and back on their NetBird name when it hasn't, every minute.
func (b *teamBroker) keepHTTPS(done <-chan struct{}, stderr io.Writer) {
	for {
		if err := b.applyHTTPS(); err != nil {
			fmt.Fprintf(stderr, "hi: the team's addresses on https: %v\n", err)
		}
		select {
		case <-done:
			return
		case <-time.After(time.Minute):
		}
	}
}

// applyHTTPS does one round of keepHTTPS: the records, the certificate,
// then the peer and the links.
func (b *teamBroker) applyHTTPS() error {
	domain, ok := loadHiDomain()
	current := teamHTTPSDomain(b.team)
	old := fileExists(teamHTTPSOldHostFile(b.team))
	// A team may keep its addresses off the machine's domain (hi team
	// addresses).
	if !ok || !teamPeerJoined(b.team) || !teamUsesMachineDomain(b.team) {
		if current != "" || old {
			// No domain any more: plain http on the NetBird name again.
			os.Remove(teamHTTPSDomainFile(b.team))
			os.Remove(teamHTTPSOldHostFile(b.team))
			b.httpsCerts.mu.Lock()
			b.httpsCerts.config, b.httpsCerts.ready = nil, false
			b.httpsCerts.mu.Unlock()
			b.restartPeer()
		}
		return nil
	}
	dns, err := domain.provider()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	ip, err := teamPeerIP(b.team)
	if err != nil {
		return fmt.Errorf("the team's NetBird address: %w", err)
	}
	// The addresses' names, at the peer's address.
	var names []string
	var records []libdns.Record
	for _, address := range teamHTTPSAddresses {
		name := domain.addressName(b.team, address)
		names = append(names, name)
		records = append(records, libdns.Address{Name: domain.recordName(name), TTL: teamHTTPSTTL, IP: ip})
	}
	var record teamHTTPSRecord
	if data, err := os.ReadFile(teamHTTPSRecordFile(b.team)); err == nil {
		json.Unmarshal(data, &record)
	}
	if strings.Join(record.Names, " ") != strings.Join(names, " ") || record.IP != ip.String() {
		if _, err := dns.SetRecords(ctx, domain.Zone+".", records); err != nil {
			return fmt.Errorf("setting %s in %s at %s: %w", strings.Join(names, ", "), domain.Zone, domain.Provider, err)
		}
		record = teamHTTPSRecord{Names: names, IP: ip.String(), Set: time.Now().UTC()}
		data, _ := json.MarshalIndent(record, "", "  ")
		if err := writeFileAtomic(teamHTTPSRecordFile(b.team), append(data, '\n'), 0o600); err != nil {
			return err
		}
	}
	// The certificate, the domain's wildcard.
	b.httpsCerts.mu.Lock()
	config := b.httpsCerts.config
	same := b.httpsCerts.domain.Domain == domain.Domain && b.httpsCerts.domain.Provider == domain.Provider && b.httpsCerts.domain.Email == domain.Email
	b.httpsCerts.mu.Unlock()
	if config == nil || !same {
		b.httpsCerts.mu.Lock()
		failed := b.httpsCerts.failed
		b.httpsCerts.mu.Unlock()
		if time.Since(failed) < teamHTTPSRetry {
			return nil
		}
		config, err := teamHTTPSIssue(ctx, domain, dns)
		if err != nil {
			b.httpsCerts.mu.Lock()
			b.httpsCerts.failed = time.Now()
			b.httpsCerts.mu.Unlock()
			return fmt.Errorf("the certificate for *.%s (hi tries again in %s): %w", domain.Domain, teamHTTPSRetry, err)
		}
		b.httpsCerts.mu.Lock()
		b.httpsCerts.config, b.httpsCerts.domain, b.httpsCerts.ready = config, domain, true
		b.httpsCerts.mu.Unlock()
	}
	// The peer passes 443 on, and the links are https.
	if current != domain.Domain || old {
		if err := writeFileAtomic(teamHTTPSDomainFile(b.team), []byte(domain.Domain+"\n"), 0o600); err != nil {
			return err
		}
		os.Remove(teamHTTPSOldHostFile(b.team))
		b.restartPeer()
	}
	return nil
}

// restartPeer has the team's peer run as its script says now, and the
// links follow; tests replace it.
func (b *teamBroker) restartPeer() {
	if b.peerRestart != nil {
		b.peerRestart()
		return
	}
	if engine, _, err := detectBoxEngine(); err == nil && engine.state(teamDevContainer(b.team)) == "running" {
		if err := startTeamDevNet(engine, b.team, b.hi, os.Stderr); err != nil {
			fmt.Fprintf(os.Stderr, "hi: the team's NetBird peer: %v\n", err)
		}
	}
	b.applyDevPage(true, os.Stderr)
	b.applyInternalLinks()
}

// applyInternalLinks moves production's and the preview's internal links
// to where they are now, https or the NetBird name, in place: the boxes
// keep running, as only the link changed.
func (b *teamBroker) applyInternalLinks() {
	b.previewMu.Lock()
	defer b.previewMu.Unlock()
	for _, slot := range []string{teamSlotProduction, teamSlotPreview} {
		state, ok := loadTeamSlot(b.team, slot)
		if !ok || !teamInternalMoved(b.team, slot, state.Expose, state.URL) {
			continue
		}
		state.URL = teamInternalURL(b.team, slot)
		saveTeamSlot(b.team, state)
		writeTeamSlotAddress(b.team, slot, state.URL)
	}
}

// teamHTTPSHandler passes each https name to its address's relay.
func teamHTTPSHandler(team string, relays map[string]http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		for address, relay := range relays {
			if name := teamHTTPSHost(team, address); name != "" && strings.EqualFold(host, name) {
				relay.ServeHTTP(w, r)
				return
			}
		}
		http.Error(w, "hi: nothing of this team's is at "+host, http.StatusMisdirectedRequest)
	})
}

// serveHTTPS answers TLS on a listener, then the handler's http.
func serveHTTPS(listener net.Listener, certs *teamHTTPSCerts) net.Listener {
	return tls.NewListener(listener, &tls.Config{GetCertificate: certs.getCertificate, MinVersion: tls.VersionTLS12, NextProtos: []string{"http/1.1"}})
}
