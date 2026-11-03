package cli

import (
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"
)

// What a team's addresses on the NetBird network are named (appConfig's
// Addresses): the machine's domain, on https, when it has one (hi net
// domain); the team's NetBird name, on plain http; a name an IT team
// keeps in its DNS by hand, pointing at the team's NetBird address, on
// plain http with the NetBird name's ports; or a name for each address,
// which an IT team keeps, pointing at the team's NetBird address (port 80
// sends each name to its address) or at its port through the IT team's
// proxy, which may end TLS in front. hi app new asks; hi app addresses
// changes it.

const (
	appAddressesDomain  = "domain"  // the machine's domain, when it has one
	appAddressesNetBird = "netbird" // the NetBird name
	appAddressesNames   = "names"   // a name for each address
)

// appNames are each address's own name, and whether TLS ends in front of
// them (the IT team's proxy), so the links say https.
type appNames struct {
	Production string `json:"production"`
	Preview    string `json:"preview"`
	Dev        string `json:"dev"`
	HTTPS      bool   `json:"https,omitempty"`
}

// name is an address's name.
func (n appNames) name(address string) string {
	switch address {
	case appSlotPreview:
		return n.Preview
	case appAddressDev:
		return n.Dev
	}
	return n.Production
}

// appAddressName is an address's own name, when the team has names.
func appAddressName(config appConfig, address string) string {
	if config.Addresses != appAddressesNames || config.Names == nil {
		return ""
	}
	return config.Names.name(address)
}

// appNamesFrom are names from one, production's: Stage's and the dev
// page's go under it, stage.<name> and dev.<name>.
func appNamesFrom(production string) appNames {
	return appNames{Production: production, Preview: "stage." + production, Dev: "dev." + production}
}

// appParseNames reads hi app addresses' names: production's alone, or
// production=, stage= (or preview=), and dev= each.
func appParseNames(args []string) (appNames, error) {
	var names appNames
	if len(args) == 1 && !strings.Contains(args[0], "=") {
		names = appNamesFrom(strings.ToLower(strings.TrimSuffix(args[0], ".")))
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
		if err := appCheckHost(name); err != nil {
			return names, err
		}
		if seen[name] {
			return names, fmt.Errorf("%s is named twice; each address needs a name of its own", name)
		}
		seen[name] = true
	}
	return names, nil
}

// appHostPattern is a host name with a domain, such as helpdesk.corp.example.
var appHostPattern = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z][a-z0-9-]{0,61}[a-z0-9]$`)

// appAddressesHost is the host name an IT team keeps for the team, or ""
// when its addresses are on the machine's domain or the NetBird name.
func appAddressesHost(config appConfig) string {
	if config.Addresses == appAddressesNetBird || config.Addresses == appAddressesNames {
		return ""
	}
	return config.Addresses
}

// appUsesMachineDomain says whether the team's addresses go on the
// machine's domain, when it has one.
func appUsesMachineDomain(team string) bool {
	config, err := loadTeam(team)
	return err != nil || config.Addresses == ""
}

func appCheckHost(host string) error {
	if !appHostPattern.MatchString(host) {
		return fmt.Errorf("a host name looks like helpdesk.corp.example, not %q", host)
	}
	return nil
}

// appAddressesWords says where the addresses are, for people.
func appAddressesWords(config appConfig) string {
	switch {
	case config.Addresses == appAddressesNetBird:
		return "on the app's NetBird name, " + appPeerName(config.Name) + ", on plain http"
	case config.Addresses == appAddressesNames && config.Names != nil:
		tls := "on plain http"
		if config.Names.HTTPS {
			tls = "on https, ended in front of them"
		}
		return fmt.Sprintf("at %s, %s, and %s, names kept by hand, %s", config.Names.Production, config.Names.Preview, config.Names.Dev, tls)
	case config.Addresses != "":
		return "at " + config.Addresses + ", a name kept in the DNS by hand, on plain http"
	}
	if domain, ok := loadHiDomain(); ok {
		return "on this machine's domain, on https: " + domain.addressName(config.Name, appAddressDev) + " and the like"
	}
	return "on the app's NetBird name, " + appPeerName(config.Name) + ", on plain http (this machine has no domain: hi net domain)"
}

// appAskAddresses is the setup's question: the machine's domain if it
// has one, a name the IT team keeps, or the NetBird name.
func appAskAddresses(stdin io.Reader, stdout io.Writer, styled bool, config *appConfig) error {
	appSay(stdout, styled, "What the addresses are named: production, the preview, and the dev page.")
	options := []appOption{}
	offered := appAddressesNetBird
	if domain, ok := loadHiDomain(); ok {
		options = append(options, appOption{appAddressesDomain, "this machine's domain, on https: " + domain.addressName(config.Name, appAddressDev) + " and the like"})
		offered = appAddressesDomain
	}
	options = append(options,
		appOption{appAddressesNames, "a name for each, which your IT team keeps: demo.example.com, stage.demo.example.com, dev.demo.example.com"},
		appOption{"host", "one name your IT team keeps in its DNS, pointing at the app's NetBird address, with ports"},
		appOption{appAddressesNetBird, "the NetBird name, " + appPeerName(config.Name) + ", on plain http"})
	answer, err := appChoose(stdin, stdout, "Addresses", options, offered)
	if err != nil {
		return err
	}
	switch answer {
	case appAddressesDomain:
		config.Addresses = ""
	case appAddressesNetBird:
		config.Addresses = appAddressesNetBird
	case appAddressesNames:
		production, err := appAskUntil(stdin, stdout, "Production's name (Stage and the dev page go under it)", false, appCheckHost)
		if err != nil {
			return err
		}
		names := appNamesFrom(production)
		tls, err := appChoose(stdin, stdout, "TLS", []appOption{{"https", "your IT team's proxy ends TLS in front of them: the links say https"}, {"http", "plain http"}}, "https")
		if err != nil {
			return err
		}
		names.HTTPS = tls == "https"
		config.Addresses, config.Names = appAddressesNames, &names
		appSay(stdout, styled, appNamesAsk(config.Name, names))
	default:
		host, err := appAskUntil(stdin, stdout, "Host name", false, appCheckHost)
		if err != nil {
			return err
		}
		config.Addresses = host
		appSay(stdout, styled, "Ask your IT team for an A record from "+host+" to the app's NetBird address: hi app dev "+config.Name,
			"shows it once the app joined. Production is http://"+host+"/, the preview :8080, and the dev page :8888.")
	}
	return nil
}

// appAddressesCommand is hi app addresses <name> [domain|netbird|<host>].
func appAddressesCommand(args []string, stdout io.Writer) error {
	usage := usageError{"usage: hi app addresses <name> [domain | netbird | <host name> | names <production's name> | names production=<name> stage=<name> dev=<name>] [--https]"}
	https := false
	if i := slices.Index(args, "--https"); i >= 0 {
		https, args = true, slices.Delete(slices.Clone(args), i, i+1)
	}
	if len(args) >= 3 && args[1] == appAddressesNames {
		return appSetNames(args[0], args[2:], https, stdout)
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
		fmt.Fprintf(stdout, "App %s's addresses are %s.\n", name, appAddressesWords(config))
		if config.Addresses == appAddressesNames && config.Names != nil {
			fmt.Fprint(stdout, appNamesAsk(name, *config.Names))
		}
		return nil
	}
	switch answer := strings.ToLower(strings.TrimSuffix(args[1], ".")); answer {
	case appAddressesDomain:
		if _, ok := loadHiDomain(); !ok {
			return errors.New("this machine has no domain; set one with hi net domain <domain> --dns <provider>, or give a host name")
		}
		config.Addresses = ""
	case appAddressesNetBird:
		config.Addresses = appAddressesNetBird
	default:
		if err := appCheckHost(answer); err != nil {
			return err
		}
		config.Addresses = answer
	}
	config.Names = nil
	return appMoveAddresses(config, stdout)
}

// appSetNames is hi app addresses <name> names ...: each address at a
// name of its own.
func appSetNames(name string, args []string, https bool, stdout io.Writer) error {
	config, err := loadTeam(name)
	if err != nil {
		return err
	}
	names, err := appParseNames(args)
	if err != nil {
		return err
	}
	names.HTTPS = https
	config.Addresses, config.Names = appAddressesNames, &names
	if err := appMoveAddresses(config, stdout); err != nil {
		return err
	}
	fmt.Fprint(stdout, appNamesAsk(name, names))
	return nil
}

// appNamesAsk is what to ask the IT team for: each name, to the team's
// NetBird address, or through their proxy to its port.
func appNamesAsk(team string, names appNames) string {
	scheme := "http"
	if names.HTTPS {
		scheme = "https"
	}
	peer := appPeerName(team)
	var b strings.Builder
	fmt.Fprintf(&b, "Ask your IT team to point each name at the app's NetBird peer, %s, either way:\n", peer)
	for _, address := range []string{appSlotProduction, appSlotPreview, appAddressDev} {
		fmt.Fprintf(&b, "  %-11s %s://%s/   an A record to its NetBird address, or a proxy to %s:%d\n", appAddressLabel(address), scheme, names.name(address), peer, appInternalPort(address))
	}
	b.WriteString("With A records, all three are on port 80 and hi sends each name to its address; NetBird's policies then can't keep anyone off the dev page by its port, and the dev page's own links are what does.\n")
	return b.String()
}

// appAddressLabel names an address for people: Production, Stage, the
// dev page.
func appAddressLabel(address string) string {
	switch address {
	case appSlotPreview:
		return "Stage"
	case appAddressDev:
		return "Dev page"
	}
	return "Production"
}

// appMoveAddresses saves where the addresses are and moves them there.
func appMoveAddresses(config appConfig, stdout io.Writer) error {
	name := config.Name
	if err := saveTeam(config); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "App %s's addresses are %s.\n", name, appAddressesWords(config))
	if appState(name) != "up" {
		fmt.Fprintf(stdout, "They move there when it starts: hi app up %s.\n", name)
		return nil
	}
	fmt.Fprintf(stdout, "App %s restarts to move them; the owners get the dev page's new link in Slack.\n", name)
	return appUp(name, stdout)
}
