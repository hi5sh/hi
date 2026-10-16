package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// A domain for the teams' apps on this machine: a team that has it on
// (hi team domain <team> on) has its production at <team>.<domain> and its
// preview at <team>-preview.<domain>, names that stay, so the people's cookies for an app (the helper's answers to their
// feedback, their sign-in) last. Without one, NetBird makes up an address
// each day.

// teamsSettings is settings.json beside the teams' links.
type teamsSettings struct {
	Domain string `json:"domain,omitempty"`
}

func teamsSettingsPath() string { return filepath.Join(teamsDir(), "settings.json") }

func loadTeamsSettings() teamsSettings {
	var settings teamsSettings
	if data, err := os.ReadFile(teamsSettingsPath()); err == nil {
		json.Unmarshal(data, &settings)
	}
	return settings
}

func saveTeamsSettings(settings teamsSettings) error {
	if err := os.MkdirAll(teamsDir(), 0o700); err != nil {
		return err
	}
	data, _ := json.MarshalIndent(settings, "", "  ")
	return writeFileAtomic(teamsSettingsPath(), append(data, '\n'), 0o600)
}

// teamSlotDomain is the name a team's preview or production is on, or ""
// for an address NetBird makes up.
func teamSlotDomain(team, slot string) string {
	domain := loadTeamsSettings().Domain
	if domain == "" {
		return ""
	}
	if config, err := loadTeam(team); err != nil || !config.Domain {
		return ""
	}
	if slot == teamSlotPreview {
		return team + "-preview." + domain
	}
	return team + "." + domain
}

// teamDomainCommand is hi team domain [<domain>|off], and hi team domain
// <team> on|off.
func teamDomainCommand(args []string, stdin io.Reader, stdout io.Writer) error {
	settings := loadTeamsSettings()
	if len(args) == 0 {
		if settings.Domain == "" {
			fmt.Fprintln(stdout, "The teams' apps get an address from NetBird that changes each day. hi team domain <domain> gives them names that stay.")
			return nil
		}
		var on []string
		entries, _ := os.ReadDir(teamsDir())
		for _, entry := range entries {
			if config, err := loadTeam(entry.Name()); err == nil && teamNamePattern.MatchString(entry.Name()) && config.Domain {
				on = append(on, fmt.Sprintf("  %s: https://%s", config.Name, teamSlotDomain(config.Name, teamSlotProduction)))
			}
		}
		fmt.Fprintf(stdout, "The domain for the teams' apps is %s.\n", settings.Domain)
		if len(on) == 0 {
			fmt.Fprintln(stdout, "No team has it on yet: hi team domain <team> on.")
		} else {
			fmt.Fprintln(stdout, "On for:\n"+strings.Join(on, "\n"))
		}
		return nil
	}
	if len(args) == 2 && (args[1] == "on" || args[1] == "off") {
		return teamDomainSwitch(args[0], args[1] == "on", stdout)
	}
	newToken := false
	if len(args) == 2 && args[1] == "--new-token" {
		newToken, args = true, args[:1]
	}
	if len(args) != 1 {
		return usageError{"usage: hi team domain [<domain> [--new-token] | off]"}
	}
	if args[0] == "off" {
		settings.Domain = ""
		if err := saveTeamsSettings(settings); err != nil {
			return err
		}
		fmt.Fprintln(stdout, "The teams' apps go back to addresses NetBird makes up: production's within a minute, a preview with the next one, for the teams that run this version of hi.")
		return nil
	}
	domain := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(args[0]), "."))
	if !netExposeDomainName.MatchString(domain) {
		return fmt.Errorf("%q isn't a domain such as apps.example.com", args[0])
	}
	// askToken asks at a terminal, or reads a pipe or file:
	// hi team domain app.example.com < token.txt
	asked := false
	askToken := func() (string, error) {
		file, isFile := stdin.(*os.File)
		var read []byte
		var err error
		switch {
		case asked:
			return "", errors.New("no other token")
		case isFile && interactive(stdin, stdout):
			fmt.Fprint(stdout, "A NetBird API token, from a service user with the Admin role (Team > Service Users): ")
			read, err = readSecret(file, stdout)
			fmt.Fprintln(stdout)
		case isFile && !teamStdinTerminal(file):
			read, err = io.ReadAll(io.LimitReader(stdin, 4096))
		default:
			return "", errors.New("hi needs a NetBird API token, from a service user with the Admin role (NetBird: Team > Service Users): run hi team domain in a terminal, where it asks for it, or give it on standard input (hi team domain <domain> < token.txt)")
		}
		asked = true
		if err != nil {
			return "", err
		}
		token := strings.TrimSpace(string(read))
		if token == "" {
			return "", errors.New("no token")
		}
		os.Setenv("NETBIRD_API_TOKEN", token)
		return token, nil
	}
	token, err := netbirdAPIToken()
	if err != nil || newToken {
		if token, err = askToken(); err != nil {
			return err
		}
	}
	err = teamCheckDomain(domain)
	if errors.Is(err, errNetbirdToken) && !asked {
		// The saved token stopped working: ask for another.
		fmt.Fprintln(stdout, "NetBird doesn't know the saved token anymore (deleted or expired?).")
		if token, err = askToken(); err != nil {
			return err
		}
		err = teamCheckDomain(domain)
	}
	if err != nil {
		return err
	}
	// Saved, even from $NETBIRD_API_TOKEN: the teams run as services,
	// without it.
	if err := saveNetbirdAPIToken(token); err != nil {
		return err
	}
	settings.Domain = domain
	if err := saveTeamsSettings(settings); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "The domain for the teams' apps is %s. No team moves until you turn it on for it: hi team domain <team> on.\n", domain)
	return nil
}

// teamCheckDomain asks NetBird whether the domain is verified in its
// account, and says what to do if it isn't.
func teamCheckDomain(domain string) error {
	api, err := newNetbirdAPI()
	if err != nil {
		return err
	}
	var domains []struct {
		Domain        string `json:"domain"`
		Validated     bool   `json:"validated"`
		TargetCluster string `json:"target_cluster"`
	}
	if err := api.call(http.MethodGet, "/api/reverse-proxies/domains", nil, &domains); err != nil {
		return err
	}
	setup := fmt.Sprintf("In NetBird, add %s under Reverse Proxy > Custom Domains; in its DNS, add a CNAME for *.%s to the proxy cluster NetBird names (such as eu.proxy.netbird.io); then verify it there", domain, domain)
	found := false
	for _, known := range domains {
		if known.Domain != domain {
			continue
		}
		if !known.Validated {
			return fmt.Errorf("%s isn't verified in NetBird yet. %s: the CNAME for *.%s goes to %s", domain, setup, domain, firstNonEmpty(known.TargetCluster, "the proxy cluster"))
		}
		found = true
	}
	if !found {
		return fmt.Errorf("NetBird doesn't have %s. %s", domain, setup)
	}
	// Reading isn't enough: a role such as Network Admin may list the
	// domains but not make services. A service that is off, made and
	// removed at once, says whether the token may.
	// It needs a target NetBird takes, or NetBird refuses it as invalid
	// before it looks at the token's rights: this machine.
	address, err := netExposeAddress()
	if err != nil {
		return errors.New("NetBird isn't connected on this machine; connect with hi net first")
	}
	var peers []struct {
		ID string `json:"id"`
	}
	if err := api.call(http.MethodGet, "/api/peers?ip="+url.QueryEscape(address), nil, &peers); err != nil {
		return err
	}
	if len(peers) != 1 {
		return fmt.Errorf("NetBird's API knows no peer at %s, this machine", address)
	}
	check := netbirdService{Name: "hi-check." + domain, Domain: "hi-check." + domain, Mode: "http", Enabled: false,
		Targets: []netbirdTarget{{TargetID: peers[0].ID, TargetType: "peer", Protocol: "http", Path: "/", Port: 9, Enabled: true}}}
	var made netbirdService
	if err := api.call(http.MethodPost, "/api/reverse-proxies/services", check, &made); err != nil {
		if strings.Contains(err.Error(), "permission denied") {
			return errors.New("the NetBird API token may read the reverse proxy but not make services: it must be a service user's with the Admin role (Team > Service Users); hi team domain <domain> --new-token takes another")
		}
		// Anything else, such as the check's service not suiting
		// NetBird, says nothing about the token.
		return nil
	}
	if made.ID != "" {
		api.call(http.MethodDelete, "/api/reverse-proxies/services/"+url.PathEscape(made.ID), nil, nil)
	}
	return nil
}

// teamStdinTerminal says standard input is a terminal, where hi asks
// rather than reads.
func teamStdinTerminal(file *os.File) bool {
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// teamDomainSwitch turns the machine's domain on or off for one team.
func teamDomainSwitch(name string, on bool, stdout io.Writer) error {
	config, err := loadTeam(name)
	if err != nil {
		return err
	}
	domain := loadTeamsSettings().Domain
	if on && domain == "" {
		return errors.New("this machine has no domain for the teams' apps yet: hi team domain <domain> first")
	}
	config.Domain = on
	if err := saveTeam(config); err != nil {
		return err
	}
	if !on {
		fmt.Fprintf(stdout, "%s goes back to addresses NetBird makes up: production's within a minute, the preview with the next one.\n", name)
		return nil
	}
	fmt.Fprintf(stdout, "%s moves to https://%s within a minute, and its preview to https://%s with the next one. The first visit waits for the certificate.\n",
		name, teamSlotDomain(name, teamSlotProduction), teamSlotDomain(name, teamSlotPreview))
	return nil
}
