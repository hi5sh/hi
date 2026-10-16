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

// A domain for the teams' apps on this machine: each team's production at
// <team>.<domain> and its preview at <team>-preview.<domain>, names that
// stay, so the people's cookies for an app (the helper's answers to their
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
	if slot == teamSlotPreview {
		return team + "-preview." + domain
	}
	return team + "." + domain
}

// teamDomainCommand is hi team domain [<domain>|off].
func teamDomainCommand(args []string, stdin io.Reader, stdout io.Writer) error {
	settings := loadTeamsSettings()
	if len(args) == 0 {
		if settings.Domain == "" {
			fmt.Fprintln(stdout, "The teams' apps get an address from NetBird that changes each day. hi team domain <domain> gives them names that stay.")
			return nil
		}
		fmt.Fprintf(stdout, "The teams' apps are at <team>.%s, and their previews at <team>-preview.%s.\n", settings.Domain, settings.Domain)
		return nil
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
		fmt.Fprintln(stdout, "The teams' apps go back to addresses NetBird makes up: production's within a minute, a preview with the next one.")
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
	fmt.Fprintf(stdout, "The teams' apps move to <team>.%s within a minute, such as https://payments.%s, and their previews to <team>-preview.%s with the next preview. The first visit to each waits for its certificate.\n", domain, domain, domain)
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
	check := netbirdService{Name: "hi-check." + domain, Domain: "hi-check." + domain, Mode: "http", Enabled: false, Targets: []netbirdTarget{}}
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
