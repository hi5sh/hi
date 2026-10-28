package main

// hi net domain: a domain of this machine's own for its teams' addresses
// on the NetBird network, such as dev.example.com, so they are served
// over https with a certificate browsers trust: quarry.dev.example.com for
// team quarry' production, quarry-preview and quarry-dev for its preview
// and dev page. hi keeps the names in the domain's DNS (pointing at
// each team's NetBird address, which only the network reaches) and gets
// one wildcard certificate, *.dev.example.com, from Let's Encrypt, proving
// the domain with a TXT record (DNS-01): nothing reaches the machine from
// outside. The DNS provider is a setting, through libdns, so the domain
// can move to another one with a command.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/libdns/cloudflare"
	"github.com/libdns/digitalocean"
	"github.com/libdns/gandi"
	"github.com/libdns/hetzner"
	"github.com/libdns/libdns"
	"github.com/libdns/rfc2136"
	"github.com/libdns/route53"
	"golang.org/x/net/publicsuffix"
)

// hiDomain is the machine's domain for its teams' addresses, in
// ~/.local/share/hi/domain.json, which only this user reads: it holds the
// DNS provider's token.
type hiDomain struct {
	Domain   string            `json:"domain"`             // dev.example.com
	Zone     string            `json:"zone"`               // example.com: the DNS zone it is in
	Provider string            `json:"provider"`           // gandi, cloudflare, ...
	Settings map[string]string `json:"settings,omitempty"` // the provider's, its token among them
	// Email is where Let's Encrypt writes about the certificate, such as a
	// renewal that keeps failing; optional.
	Email string    `json:"email,omitempty"`
	Set   time.Time `json:"set"`
}

// hiEmailPattern is an email address, roughly.
var hiEmailPattern = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)

// hiDNSProvider is what hi does with a DNS provider: read a zone's
// records, set and remove some, and add the certificate's TXT record.
type hiDNSProvider interface {
	libdns.RecordGetter
	libdns.RecordSetter
	libdns.RecordAppender
	libdns.RecordDeleter
}

// hiDNSField is a setting a provider takes, as its libdns package names it.
type hiDNSField struct {
	Key      string // the JSON key in the provider's libdns settings
	Label    string
	Secret   bool
	Optional bool
}

// hiDNSKind is a DNS provider hi works with.
type hiDNSKind struct {
	Name   string
	Fields []hiDNSField
	Help   string // where its token comes from
	New    func() hiDNSProvider
}

// hiDNSKinds are the providers hi can use; each is its libdns package.
var hiDNSKinds = map[string]hiDNSKind{
	"gandi": {Name: "Gandi", Fields: []hiDNSField{{Key: "bearer_token", Label: "Gandi personal access token", Secret: true}},
		Help: "a personal access token that may manage the domain's technical configuration (DNS), for that domain only",
		New:  func() hiDNSProvider { return &gandi.Provider{} }},
	"cloudflare": {Name: "Cloudflare", Fields: []hiDNSField{{Key: "api_token", Label: "Cloudflare API token", Secret: true}},
		Help: "an API token with Zone.DNS:Edit for the zone",
		New:  func() hiDNSProvider { return &cloudflare.Provider{} }},
	"route53": {Name: "Amazon Route 53", Fields: []hiDNSField{
		{Key: "access_key_id", Label: "AWS access key ID"}, {Key: "secret_access_key", Label: "AWS secret access key", Secret: true},
		{Key: "region", Label: "AWS region (Enter for us-east-1)", Optional: true}},
		Help: "an IAM user's keys that may change the zone's records (route53:ChangeResourceRecordSets, ListResourceRecordSets, ListHostedZonesByName)",
		New:  func() hiDNSProvider { return &route53.Provider{} }},
	"hetzner": {Name: "Hetzner DNS", Fields: []hiDNSField{{Key: "auth_api_token", Label: "Hetzner DNS API token", Secret: true}},
		Help: "an API token from Hetzner's DNS console",
		New:  func() hiDNSProvider { return &hetzner.Provider{} }},
	"digitalocean": {Name: "DigitalOcean", Fields: []hiDNSField{{Key: "auth_token", Label: "DigitalOcean API token", Secret: true}},
		Help: "a personal access token with the domain scope's read and write",
		New:  func() hiDNSProvider { return &digitalocean.Provider{} }},
	"rfc2136": {Name: "a DNS server of your own (RFC 2136)", Fields: []hiDNSField{
		{Key: "server", Label: "DNS server (host:port)"}, {Key: "key_name", Label: "TSIG key name"},
		{Key: "key_alg", Label: "TSIG algorithm (Enter for hmac-sha256)", Optional: true}, {Key: "key", Label: "TSIG secret (base64)", Secret: true}},
		Help: "a TSIG key the server takes dynamic updates of the zone with",
		New:  func() hiDNSProvider { return &rfc2136.Provider{} }},
}

func hiDNSKindNames() []string {
	names := make([]string, 0, len(hiDNSKinds))
	for name := range hiDNSKinds {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// hiDomainPath is where the machine's domain is kept, beside the teams.
func hiDomainPath() string { return filepath.Join(filepath.Dir(teamsDir()), "domain.json") }

// hiCertsDir holds the certificates, which every team on the machine
// shares.
func hiCertsDir() string { return filepath.Join(filepath.Dir(teamsDir()), "certs") }

func loadHiDomain() (hiDomain, bool) {
	var domain hiDomain
	data, err := os.ReadFile(hiDomainPath())
	if err != nil || json.Unmarshal(data, &domain) != nil || domain.Domain == "" {
		return hiDomain{}, false
	}
	return domain, true
}

func saveHiDomain(domain hiDomain) error {
	if err := os.MkdirAll(filepath.Dir(hiDomainPath()), 0o700); err != nil {
		return err
	}
	data, _ := json.MarshalIndent(domain, "", "  ")
	return writeFileAtomic(hiDomainPath(), append(data, '\n'), 0o600)
}

// provider is the domain's DNS provider, set up from its settings.
func (d hiDomain) provider() (hiDNSProvider, error) {
	kind, ok := hiDNSKinds[d.Provider]
	if !ok {
		if hiTestDNS != nil && d.Provider == "test" {
			return hiTestDNS, nil
		}
		return nil, fmt.Errorf("hi doesn't know the DNS provider %q; it knows %s", d.Provider, strings.Join(hiDNSKindNames(), ", "))
	}
	provider := kind.New()
	data, _ := json.Marshal(d.Settings)
	if err := json.Unmarshal(data, provider); err != nil {
		return nil, err
	}
	return provider, nil
}

// hiTestDNS is the provider "test" gives; tests set it.
var hiTestDNS hiDNSProvider

// hiDomainZone is the zone a domain is in: the part a registrar sells,
// example.com for dev.example.com, unless --zone says.
func hiDomainZone(domain string) (string, error) {
	zone, err := publicsuffix.EffectiveTLDPlusOne(domain)
	if err != nil {
		return "", fmt.Errorf("%s isn't a domain on the internet: %w", domain, err)
	}
	return zone, nil
}

// addressName is a team's address's name in the domain, one label under
// it for the wildcard certificate: quarry.dev.example.com for production,
// quarry-preview for the preview, and quarry-dev for the dev page.
func (d hiDomain) addressName(team, address string) string {
	switch address {
	case teamSlotPreview:
		return team + "-preview." + d.Domain
	case teamAddressDev:
		return team + "-dev." + d.Domain
	}
	return team + "." + d.Domain
}

// recordName is a name relative to the zone, as libdns takes it.
func (d hiDomain) recordName(fqdn string) string {
	return libdns.RelativeName(fqdn, d.Zone+".")
}

const hiDomainUsage = `usage: hi net domain                                  the domain this machine's teams' addresses use
       hi net domain <domain> --dns <provider> [--zone <zone>]
                                                     use it: hi asks for the provider's token
       hi net domain --email <address>               where Let's Encrypt writes about the certificate
       hi net domain --unset                         back to plain http on the NetBird names
providers: ` + "gandi, cloudflare, route53, hetzner, digitalocean, rfc2136"

// runNetDomain is hi net domain.
func runNetDomain(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	var domain, provider, zone string
	email, emailGiven := "", false
	unset := false
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--dns" && i+1 < len(args):
			i++
			provider = strings.ToLower(args[i])
		case args[i] == "--zone" && i+1 < len(args):
			i++
			zone = strings.ToLower(strings.TrimSuffix(args[i], "."))
		case args[i] == "--unset":
			unset = true
		case args[i] == "--email" && i+1 < len(args):
			i++
			email, emailGiven = strings.TrimSpace(args[i]), true
			if email != "" && !hiEmailPattern.MatchString(email) {
				return fmt.Errorf("%q isn't an email address", email)
			}
		case args[i] == "-h" || args[i] == "--help":
			fmt.Fprintln(stdout, hiDomainUsage)
			return nil
		case strings.HasPrefix(args[i], "-") || domain != "":
			return usageError{hiDomainUsage}
		default:
			domain = strings.ToLower(strings.TrimSuffix(args[i], "."))
		}
	}
	switch {
	case unset:
		if err := os.Remove(hiDomainPath()); err != nil && !os.IsNotExist(err) {
			return err
		}
		fmt.Fprintln(stdout, "The teams' addresses go back to plain http on their NetBird names within a minute; ask for the dev page's new link in Slack. The records hi made stay in the zone until you remove them.")
		return nil
	case domain == "" && emailGiven:
		current, ok := loadHiDomain()
		if !ok {
			return errors.New("set the domain first: hi net domain dev.example.com --dns gandi")
		}
		current.Email = email
		if err := saveHiDomain(current); err != nil {
			return err
		}
		if email == "" {
			fmt.Fprintln(stdout, "Let's Encrypt has no email for the certificate now.")
		} else {
			fmt.Fprintf(stdout, "Let's Encrypt writes to %s about the certificate, from its next one.\n", email)
		}
		return nil
	case domain == "":
		current, ok := loadHiDomain()
		if !ok {
			fmt.Fprintln(stdout, "The teams' addresses are on plain http, on their NetBird names. Give them a domain of your own for https: hi net domain dev.example.com --dns gandi")
			return nil
		}
		fmt.Fprintf(stdout, "The teams' addresses are on https at <team>.%[1]s (production), <team>-preview.%[1]s, and <team>-dev.%[1]s (the dev page), with the DNS at %[2]s (zone %[3]s), since %[4]s.\n", current.Domain, current.Provider, current.Zone, current.Set.Local().Format("Jan 2, 2006"))
		if current.Email != "" {
			fmt.Fprintf(stdout, "Let's Encrypt writes to %s about the certificate.\n", current.Email)
		} else {
			fmt.Fprintln(stdout, "Let's Encrypt has no email to write to about the certificate: hi net domain --email <address>")
		}
		return nil
	case provider == "":
		return usageError{hiDomainUsage}
	}
	if !boxDomainPattern.MatchString(domain) || strings.HasPrefix(domain, "*.") {
		return fmt.Errorf("%q isn't a domain such as dev.example.com", domain)
	}
	var err error
	if zone == "" {
		if zone, err = hiDomainZone(domain); err != nil {
			return err
		}
	}
	if domain != zone && !strings.HasSuffix(domain, "."+zone) {
		return fmt.Errorf("%s isn't in the zone %s", domain, zone)
	}
	next := hiDomain{Domain: domain, Zone: zone, Provider: provider, Settings: map[string]string{}, Email: email, Set: time.Now().UTC()}
	if provider != "test" {
		kind, ok := hiDNSKinds[provider]
		if !ok {
			return fmt.Errorf("hi doesn't know the DNS provider %q; it knows %s", provider, strings.Join(hiDNSKindNames(), ", "))
		}
		fmt.Fprintf(stdout, "%s needs %s.\n", kind.Name, kind.Help)
		for _, field := range kind.Fields {
			var value string
			if field.Secret {
				value, err = teamAskSecret(stdin, stdout, field.Label)
			} else {
				fmt.Fprintf(stdout, "%s: ", field.Label)
				value, err = readLine(stdin)
				value = strings.TrimSpace(value)
				if errors.Is(err, io.EOF) {
					err = nil
				}
			}
			if err != nil {
				return err
			}
			if value == "" && !field.Optional {
				return fmt.Errorf("no %s was entered", strings.ToLower(field.Label))
			}
			if value != "" {
				next.Settings[field.Key] = value
			}
		}
		if !emailGiven {
			fmt.Fprint(stdout, "Email for Let's Encrypt, which writes if the certificate's renewal keeps failing (optional; Enter to skip): ")
			value, err := readLine(stdin)
			if err != nil && !errors.Is(err, io.EOF) {
				return err
			}
			if value = strings.TrimSpace(value); value != "" && !hiEmailPattern.MatchString(value) {
				return fmt.Errorf("%q isn't an email address", value)
			}
			next.Email = value
		}
	}
	// The provider shows it may read the zone before hi keeps its token.
	dns, err := next.provider()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	records, err := dns.GetRecords(ctx, zone+".")
	if err != nil {
		return fmt.Errorf("%s didn't let hi read the zone %s: %w", provider, zone, err)
	}
	if err := saveHiDomain(next); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "%s answers for %s (%d records). hi keeps the token on this machine only.\n", provider, zone, len(records))
	fmt.Fprintf(stdout, "Within a few minutes each team's production is on https://<team>.%[1]s/, its preview on https://<team>-preview.%[1]s/, and its dev page on https://<team>-dev.%[1]s/, with their names in the zone pointing at the team's NetBird address and a certificate for *.%[1]s from Let's Encrypt. Then ask for the dev page's new link in Slack (/hi-<team> dev).\n", domain)
	fmt.Fprintln(stdout, "In NetBird's Access Control, let the people and the owners reach the teams' peers on TCP 443 too. Each team's peer starts again once for it, which ends SSH sessions to the dev box; the agents and the apps keep running.")
	return nil
}
