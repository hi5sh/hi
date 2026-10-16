package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
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
	if len(args) != 1 {
		return usageError{"usage: hi team domain [<domain> | off]"}
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
	if _, err := netbirdAPIToken(); err != nil {
		file, ok := stdin.(*os.File)
		if !ok || !interactive(stdin, stdout) {
			return errors.New("hi needs a NetBird API token to name the apps: set NETBIRD_API_TOKEN, from a service user that may manage the reverse proxy (NetBird: Team > Service Users)")
		}
		fmt.Fprint(stdout, "A NetBird API token, from a service user that may manage the reverse proxy (Team > Service Users): ")
		token, err := readSecret(file, stdout)
		fmt.Fprintln(stdout)
		if err != nil {
			return err
		}
		if strings.TrimSpace(string(token)) == "" {
			return errors.New("no token")
		}
		if err := saveNetbirdAPIToken(string(token)); err != nil {
			return err
		}
	}
	if err := teamCheckDomain(domain); err != nil {
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
	for _, known := range domains {
		if known.Domain != domain {
			continue
		}
		if !known.Validated {
			return fmt.Errorf("%s isn't verified in NetBird yet. %s: the CNAME for *.%s goes to %s", domain, setup, domain, firstNonEmpty(known.TargetCluster, "the proxy cluster"))
		}
		return nil
	}
	return fmt.Errorf("NetBird doesn't have %s. %s", domain, setup)
}
