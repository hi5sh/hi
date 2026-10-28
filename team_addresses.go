package main

import (
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"
)

// What a team's addresses on the NetBird network are named (teamConfig's
// Addresses): the machine's domain, on https, when it has one (hi net
// domain); the team's NetBird name, on plain http; a name an IT team
// keeps in its DNS by hand, pointing at the team's NetBird address, on
// plain http with the NetBird name's ports; or a name for each address,
// which an IT team keeps, pointing at the team's NetBird address (port 80
// sends each name to its address) or at its port through the IT team's
// proxy, which may end TLS in front. hi team new asks; hi team addresses
// changes it.

const (
	teamAddressesDomain  = "domain"  // the machine's domain, when it has one
	teamAddressesNetBird = "netbird" // the NetBird name
	teamAddressesNames   = "names"   // a name for each address
)

// teamNames are each address's own name, and whether TLS ends in front of
// them (the IT team's proxy), so the links say https.
type teamNames struct {
	Production string `json:"production"`
	Preview    string `json:"preview"`
	Dev        string `json:"dev"`
	HTTPS      bool   `json:"https,omitempty"`
}

// name is an address's name.
func (n teamNames) name(address string) string {
	switch address {
	case teamSlotPreview:
		return n.Preview
	case teamAddressDev:
		return n.Dev
	}
	return n.Production
}

// teamAddressName is an address's own name, when the team has names.
func teamAddressName(config teamConfig, address string) string {
	if config.Addresses != teamAddressesNames || config.Names == nil {
		return ""
	}
	return config.Names.name(address)
}

// teamNamesFrom are names from one, production's: Stage's and the dev
// page's go under it, stage.<name> and dev.<name>.
func teamNamesFrom(production string) teamNames {
	return teamNames{Production: production, Preview: "stage." + production, Dev: "dev." + production}
}

// teamParseNames reads hi team addresses' names: production's alone, or
// production=, stage= (or preview=), and dev= each.
func teamParseNames(args []string) (teamNames, error) {
	var names teamNames
	if len(args) == 1 && !strings.Contains(args[0], "=") {
		names = teamNamesFrom(strings.ToLower(strings.TrimSuffix(args[0], ".")))
	} else {
		for _, arg := range args {
			key, value, ok := strings.Cut(arg, "=")
			value = strings.ToLower(strings.TrimSuffix(value, "."))
			switch {
			case !ok:
				return names, fmt.Errorf("give production's name alone, or each as production=, stage=, and dev=, not %q", arg)
			case key == "production":
				names.Production = value
			case key == "stage" || key == "preview":
				names.Preview = value
			case key == "dev":
				names.Dev = value
			default:
				return names, fmt.Errorf("the addresses are production, stage, and dev, not %q", key)
			}
		}
	}
	seen := map[string]bool{}
	for _, name := range []string{names.Production, names.Preview, names.Dev} {
		if name == "" {
			return names, errors.New("each of production, stage, and dev needs a name")
		}
		if err := teamCheckHost(name); err != nil {
			return names, err
		}
		if seen[name] {
			return names, fmt.Errorf("%s is named twice; each address needs a name of its own", name)
		}
		seen[name] = true
	}
	return names, nil
}

// teamHostPattern is a host name with a domain, such as helpdesk.corp.example.
var teamHostPattern = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z][a-z0-9-]{0,61}[a-z0-9]$`)

// teamAddressesHost is the host name an IT team keeps for the team, or ""
// when its addresses are on the machine's domain or the NetBird name.
func teamAddressesHost(config teamConfig) string {
	if config.Addresses == teamAddressesNetBird || config.Addresses == teamAddressesNames {
		return ""
	}
	return config.Addresses
}

// teamUsesMachineDomain says whether the team's addresses go on the
// machine's domain, when it has one.
func teamUsesMachineDomain(team string) bool {
	config, err := loadTeam(team)
	return err != nil || config.Addresses == ""
}

func teamCheckHost(host string) error {
	if !teamHostPattern.MatchString(host) {
		return fmt.Errorf("a host name looks like helpdesk.corp.example, not %q", host)
	}
	return nil
}

// teamAddressesWords says where the addresses are, for people.
func teamAddressesWords(config teamConfig) string {
	switch {
	case config.Addresses == teamAddressesNetBird:
		return "on the team's NetBird name, " + teamPeerName(config.Name) + ", on plain http"
	case config.Addresses == teamAddressesNames && config.Names != nil:
		tls := "on plain http"
		if config.Names.HTTPS {
			tls = "on https, ended in front of them"
		}
		return fmt.Sprintf("at %s, %s, and %s, names kept by hand, %s", config.Names.Production, config.Names.Preview, config.Names.Dev, tls)
	case config.Addresses != "":
		return "at " + config.Addresses + ", a name kept in the DNS by hand, on plain http"
	}
	if domain, ok := loadHiDomain(); ok {
		return "on this machine's domain, on https: " + domain.addressName(config.Name, teamAddressDev) + " and the like"
	}
	return "on the team's NetBird name, " + teamPeerName(config.Name) + ", on plain http (this machine has no domain: hi net domain)"
}

// teamAskAddresses is the setup's question: the machine's domain if it
// has one, a name the IT team keeps, or the NetBird name.
func teamAskAddresses(stdin io.Reader, stdout io.Writer, styled bool, config *teamConfig) error {
	teamSay(stdout, styled, "What the addresses are named: production, the preview, and the dev page.")
	options := []teamOption{}
	offered := teamAddressesNetBird
	if domain, ok := loadHiDomain(); ok {
		options = append(options, teamOption{teamAddressesDomain, "this machine's domain, on https: " + domain.addressName(config.Name, teamAddressDev) + " and the like"})
		offered = teamAddressesDomain
	}
	options = append(options,
		teamOption{teamAddressesNames, "a name for each, which your IT team keeps: demo.example.com, stage.demo.example.com, dev.demo.example.com"},
		teamOption{"host", "one name your IT team keeps in its DNS, pointing at the team's NetBird address, with ports"},
		teamOption{teamAddressesNetBird, "the NetBird name, " + teamPeerName(config.Name) + ", on plain http"})
	answer, err := teamChoose(stdin, stdout, "Addresses", options, offered)
	if err != nil {
		return err
	}
	switch answer {
	case teamAddressesDomain:
		config.Addresses = ""
	case teamAddressesNetBird:
		config.Addresses = teamAddressesNetBird
	case teamAddressesNames:
		production, err := teamAskUntil(stdin, stdout, "Production's name (Stage and the dev page go under it)", false, teamCheckHost)
		if err != nil {
			return err
		}
		names := teamNamesFrom(production)
		tls, err := teamChoose(stdin, stdout, "TLS", []teamOption{{"https", "your IT team's proxy ends TLS in front of them: the links say https"}, {"http", "plain http"}}, "https")
		if err != nil {
			return err
		}
		names.HTTPS = tls == "https"
		config.Addresses, config.Names = teamAddressesNames, &names
		teamSay(stdout, styled, teamNamesAsk(config.Name, names))
	default:
		host, err := teamAskUntil(stdin, stdout, "Host name", false, teamCheckHost)
		if err != nil {
			return err
		}
		config.Addresses = host
		teamSay(stdout, styled, "Ask your IT team for an A record from "+host+" to the team's NetBird address: hi team dev "+config.Name,
			"shows it once the team joined. Production is http://"+host+"/, the preview :8080, and the dev page :8888.")
	}
	return nil
}

// teamAddressesCommand is hi team addresses <name> [domain|netbird|<host>].
func teamAddressesCommand(args []string, stdout io.Writer) error {
	usage := usageError{"usage: hi team addresses <name> [domain | netbird | <host name> | names <production's name> | names production=<name> stage=<name> dev=<name>] [--https]"}
	https := false
	if i := slices.Index(args, "--https"); i >= 0 {
		https, args = true, slices.Delete(slices.Clone(args), i, i+1)
	}
	if len(args) >= 3 && args[1] == teamAddressesNames {
		return teamSetNames(args[0], args[2:], https, stdout)
	}
	if len(args) == 0 || len(args) > 2 || https {
		return usage
	}
	name := args[0]
	config, err := loadTeam(name)
	if err != nil {
		return err
	}
	if len(args) == 1 {
		fmt.Fprintf(stdout, "Team %s's addresses are %s.\n", name, teamAddressesWords(config))
		if config.Addresses == teamAddressesNames && config.Names != nil {
			fmt.Fprint(stdout, teamNamesAsk(name, *config.Names))
		}
		return nil
	}
	switch answer := strings.ToLower(strings.TrimSuffix(args[1], ".")); answer {
	case teamAddressesDomain:
		if _, ok := loadHiDomain(); !ok {
			return errors.New("this machine has no domain; set one with hi net domain <domain> --dns <provider>, or give a host name")
		}
		config.Addresses = ""
	case teamAddressesNetBird:
		config.Addresses = teamAddressesNetBird
	default:
		if err := teamCheckHost(answer); err != nil {
			return err
		}
		config.Addresses = answer
	}
	config.Names = nil
	return teamMoveAddresses(config, stdout)
}

// teamSetNames is hi team addresses <name> names ...: each address at a
// name of its own.
func teamSetNames(name string, args []string, https bool, stdout io.Writer) error {
	config, err := loadTeam(name)
	if err != nil {
		return err
	}
	names, err := teamParseNames(args)
	if err != nil {
		return err
	}
	names.HTTPS = https
	config.Addresses, config.Names = teamAddressesNames, &names
	if err := teamMoveAddresses(config, stdout); err != nil {
		return err
	}
	fmt.Fprint(stdout, teamNamesAsk(name, names))
	return nil
}

// teamNamesAsk is what to ask the IT team for: each name, to the team's
// NetBird address, or through their proxy to its port.
func teamNamesAsk(team string, names teamNames) string {
	scheme := "http"
	if names.HTTPS {
		scheme = "https"
	}
	peer := teamPeerName(team)
	var b strings.Builder
	fmt.Fprintf(&b, "Ask your IT team to point each name at the team's NetBird peer, %s, either way:\n", peer)
	for _, address := range []string{teamSlotProduction, teamSlotPreview, teamAddressDev} {
		fmt.Fprintf(&b, "  %-11s %s://%s/   an A record to its NetBird address, or a proxy to %s:%d\n", teamAddressLabel(address), scheme, names.name(address), peer, teamInternalPort(address))
	}
	b.WriteString("With A records, all three are on port 80 and hi sends each name to its address; NetBird's policies then can't keep anyone off the dev page by its port, and the dev page's own links are what does.\n")
	return b.String()
}

// teamAddressLabel names an address for people: Production, Stage, the
// dev page.
func teamAddressLabel(address string) string {
	switch address {
	case teamSlotPreview:
		return "Stage"
	case teamAddressDev:
		return "Dev page"
	}
	return "Production"
}

// teamMoveAddresses saves where the addresses are and moves them there.
func teamMoveAddresses(config teamConfig, stdout io.Writer) error {
	name := config.Name
	if err := saveTeam(config); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Team %s's addresses are %s.\n", name, teamAddressesWords(config))
	if teamState(name) != "up" {
		fmt.Fprintf(stdout, "They move there when it starts: hi team up %s.\n", name)
		return nil
	}
	fmt.Fprintf(stdout, "Team %s restarts to move them; the owners get the dev page's new link in Slack.\n", name)
	return teamUp(name, stdout)
}
