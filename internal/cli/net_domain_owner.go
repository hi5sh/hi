package cli

// Several machines on one domain (hi_net_domain_googleclouddns.md): each
// keeps only its own names. Beside each name's A record hi puts a TXT
// record that names the machine, and it refuses a name that another
// machine's mark has, or a record hi didn't make that points elsewhere.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/libdns/libdns"
)

// hiDomainMarkPrefix starts the TXT record that says which machine keeps
// a name: "hi machine=aiw9 id=3f2a…".
const hiDomainMarkPrefix = "hi machine="

// hiMachineID is this machine's own, from /etc/machine-id, hashed so the
// zone doesn't show it; tests replace it.
var hiMachineID = func() string {
	data, err := os.ReadFile("/etc/machine-id")
	if id := strings.TrimSpace(string(data)); err == nil && id != "" {
		sum := sha256.Sum256([]byte("hi-domain-mark\n" + id))
		return hex.EncodeToString(sum[:8])
	}
	name, _ := os.Hostname()
	sum := sha256.Sum256([]byte("hi-domain-mark\nhost " + name))
	return hex.EncodeToString(sum[:8])
}

// hiDomainMark is this machine's mark.
func hiDomainMark() string {
	return hiDomainMarkPrefix + adminMachineName() + " id=" + hiMachineID()
}

// hiDomainMarkOf reads a mark: the machine's name and id.
func hiDomainMarkOf(text string) (machine, id string, ok bool) {
	rest, found := strings.CutPrefix(strings.Trim(text, `"`), hiDomainMarkPrefix)
	if !found {
		return "", "", false
	}
	machine, id, _ = strings.Cut(rest, " id=")
	return machine, id, id != ""
}

// hiDomainTaken is a name hi won't keep: another machine, or the IT team,
// has it.
type hiDomainTaken struct {
	Name string
	By   string // "the machine aiw9", "a record hi didn't make (A 10.0.0.5)"
}

func (e *hiDomainTaken) Error() string { return e.Name + " is taken: " + e.By + " has it" }

// hiDomainZoneNames is what the zone has at each name: its records.
func hiDomainZoneNames(ctx context.Context, dns hiDNSProvider, domain hiDomain) (map[string][]libdns.RR, error) {
	records, err := dns.GetRecords(ctx, domain.Zone+".")
	if err != nil {
		return nil, fmt.Errorf("reading %s at %s: %w", domain.Zone, domain.Provider, err)
	}
	at := map[string][]libdns.RR{}
	for _, record := range records {
		rr := record.RR()
		at[strings.ToLower(rr.Name)] = append(at[strings.ToLower(rr.Name)], rr)
	}
	return at, nil
}

// hiDomainCheck says whether this machine may keep the names at ip: none
// has another machine's mark, or a record hi didn't make that points
// elsewhere. mine are the names this machine kept before marks: their
// records are its own.
func hiDomainCheck(zone map[string][]libdns.RR, domain hiDomain, names []string, ip netip.Addr, mine []string) error {
	me := hiMachineID()
	for _, name := range names {
		var marked, ours bool
		var other, foreign string
		for _, rr := range zone[strings.ToLower(domain.recordName(name))] {
			switch rr.Type {
			case "TXT":
				if machine, id, ok := hiDomainMarkOf(rr.Data); ok {
					marked = true
					if id == me {
						ours = true
					} else {
						other = machine
					}
				}
			case "A", "AAAA", "CNAME":
				if ip.IsValid() && rr.Data == ip.String() {
					continue
				}
				foreign = rr.Type + " " + strings.TrimSuffix(rr.Data, ".")
			}
		}
		switch {
		case ours:
		case other != "":
			return &hiDomainTaken{Name: name, By: "the machine " + other}
		case foreign != "" && !marked && !slices.Contains(mine, name):
			return &hiDomainTaken{Name: name, By: "a record hi didn't make (" + foreign + ")"}
		}
	}
	return nil
}

// hiDomainKeep puts the names at ip in the zone, with this machine's mark
// beside each, once hiDomainCheck lets it.
func hiDomainKeep(ctx context.Context, dns hiDNSProvider, domain hiDomain, names []string, ip netip.Addr, mine []string, ttl time.Duration) error {
	zone, err := hiDomainZoneNames(ctx, dns, domain)
	if err != nil {
		return err
	}
	if err := hiDomainCheck(zone, domain, names, ip, mine); err != nil {
		return err
	}
	var records, marks []libdns.Record
	mark := hiDomainMark()
	for _, name := range names {
		records = append(records, libdns.Address{Name: domain.recordName(name), TTL: ttl, IP: ip})
		has := false
		for _, rr := range zone[strings.ToLower(domain.recordName(name))] {
			if rr.Type == "TXT" && strings.Trim(rr.Data, `"`) == mark {
				has = true
			}
		}
		if !has {
			// Appended, not set: a TXT record of the IT team's at the name
			// stays.
			marks = append(marks, libdns.TXT{Name: domain.recordName(name), TTL: ttl, Text: mark})
		}
	}
	if _, err := dns.SetRecords(ctx, domain.Zone+".", records); err != nil {
		return fmt.Errorf("setting %s in %s at %s: %w", strings.Join(names, ", "), domain.Zone, domain.Provider, err)
	}
	if len(marks) > 0 {
		if _, err := dns.AppendRecords(ctx, domain.Zone+".", marks); err != nil {
			return fmt.Errorf("marking %s as this machine's in %s at %s: %w", strings.Join(names, ", "), domain.Zone, domain.Provider, err)
		}
	}
	return nil
}

// hiDomainDrop takes names this machine kept at ip out of the zone, with
// their marks.
func hiDomainDrop(ctx context.Context, dns hiDNSProvider, domain hiDomain, names []string, ip netip.Addr, ttl time.Duration) error {
	var records []libdns.Record
	for _, name := range names {
		if ip.IsValid() {
			records = append(records, libdns.Address{Name: domain.recordName(name), TTL: ttl, IP: ip})
		}
		records = append(records, libdns.TXT{Name: domain.recordName(name), TTL: ttl, Text: hiDomainMark()})
	}
	_, err := dns.DeleteRecords(ctx, domain.Zone+".", records)
	return err
}

// hiDomainTakenWait is how long hi waits to look at a taken name again:
// the zone changes when people act, not every minute.
const hiDomainTakenWait = 10 * time.Minute

// hiDomainTakenNow is why a name was taken, kept in a file, while it is
// newer than hiDomainTakenWait; "" when there's none.
func hiDomainTakenNow(file string) string {
	info, err := os.Stat(file)
	if err != nil || time.Since(info.ModTime()) > hiDomainTakenWait {
		return ""
	}
	data, _ := os.ReadFile(file)
	return strings.TrimSpace(string(data))
}

// hiDomainTakenSaid is what the Network view shows: why a name was taken,
// however old.
func hiDomainTakenSaid(file string) string {
	data, _ := os.ReadFile(file)
	return strings.TrimSpace(string(data))
}

// hiDomainNoteTaken keeps why a name was taken, or forgets it.
func hiDomainNoteTaken(file string, err error) {
	var taken *hiDomainTaken
	if errors.As(err, &taken) {
		writeFileAtomic(file, []byte(taken.Error()+"\n"), 0o600)
		return
	}
	if err == nil {
		os.Remove(file)
	}
}
