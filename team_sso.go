package main

// hi team sso: people reach a team's app without a key link by signing in
// with the organisation's OpenID Connect provider, under the team's rules
// (docs/specs/approved/hi_team_sso.md). The machine keeps the providers;
// each team that has it on gets sso.json in its folder, which its relays
// read (hi net expose --sso, net_expose_sso.go).

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
)

// teamSSOProvider is one provider on this machine: a web client the
// organisation registered for hi.
type teamSSOProvider struct {
	Issuer       string `json:"issuer"`
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	GroupsClaim  string `json:"groups_claim,omitempty"`
}

func teamSSOProvidersPath() string { return filepath.Join(teamsDir(), "sso-providers.json") }

func loadTeamSSOProviders() map[string]teamSSOProvider {
	providers := map[string]teamSSOProvider{}
	if data, err := os.ReadFile(teamSSOProvidersPath()); err == nil {
		json.Unmarshal(data, &providers)
	}
	return providers
}

func saveTeamSSOProviders(providers map[string]teamSSOProvider) error {
	if err := os.MkdirAll(teamsDir(), 0o700); err != nil {
		return err
	}
	data, _ := json.MarshalIndent(providers, "", "  ")
	return writeFileAtomic(teamSSOProvidersPath(), append(data, '\n'), 0o600)
}

// teamSSOFile is the team's sign-in, for its relays; none while it's off.
func teamSSOFile(team string) string { return filepath.Join(teamDir(team), "sso.json") }

// teamSSOLog is the team's sign-ins and refusals, a JSON line each.
func teamSSOLog(team string) string { return filepath.Join(teamDir(team), "sso.log") }

func loadTeamSSO(team string) (netExposeSSOConfig, bool) {
	var config netExposeSSOConfig
	data, err := os.ReadFile(teamSSOFile(team))
	return config, err == nil && json.Unmarshal(data, &config) == nil
}

func saveTeamSSO(team string, config netExposeSSOConfig) error {
	data, _ := json.MarshalIndent(config, "", "  ")
	return writeFileAtomic(teamSSOFile(team), append(data, '\n'), 0o600)
}

// teamSSOProviderName is a provider's name on this machine, such as acme.
var teamSSOProviderName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,30}$`)

// teamSSOHTTP fetches discovery documents; tests replace it.
var teamSSOHTTP = &http.Client{Timeout: 15 * time.Second}

// teamSSOEntra says the issuer is Microsoft Entra ID's, where the email
// isn't checked and the tenant is in the issuer.
func teamSSOEntra(issuer string) bool {
	parsed, err := url.Parse(issuer)
	return err == nil && strings.EqualFold(parsed.Host, "login.microsoftonline.com")
}

// teamSSOCommand is hi team sso.
func teamSSOCommand(args []string, stdin io.Reader, stdout io.Writer) error {
	usage := usageError{`usage: hi team sso
       hi team sso provider add <name> <issuer> [--client-id <id>] [--groups-claim <claim>]
       hi team sso provider rm <name>
       hi team sso <team> on <provider> [--domain <d>[,<d>]] [--group <g>[,<g>]] [--anyone]
       hi team sso <team> [off]`}
	switch {
	case len(args) == 0:
		return teamSSOList(stdout)
	case args[0] == "provider" && len(args) >= 2 && args[1] == "add":
		return teamSSOProviderAdd(args[2:], stdin, stdout)
	case args[0] == "provider" && len(args) == 3 && (args[1] == "rm" || args[1] == "remove"):
		return teamSSOProviderRemove(args[2], stdout)
	case args[0] == "provider" && len(args) == 1:
		return teamSSOList(stdout)
	case args[0] == "provider":
		return usage
	case len(args) == 1:
		return teamSSOShow(args[0], stdout)
	case len(args) == 2 && args[1] == "off":
		return teamSSOOff(args[0], stdout)
	case len(args) >= 3 && args[1] == "on":
		return teamSSOOn(args[0], args[2:], stdout)
	}
	return usage
}

// teamSSODiscovery is what hi reads of a provider's discovery document.
type teamSSODiscovery struct {
	Issuer         string   `json:"issuer"`
	Authorization  string   `json:"authorization_endpoint"`
	Token          string   `json:"token_endpoint"`
	Keys           string   `json:"jwks_uri"`
	ResponseTypes  []string `json:"response_types_supported"`
	ChallengeTypes []string `json:"code_challenge_methods_supported"`
}

// teamSSODiscover reads the issuer's discovery document, and says what is
// wrong for hi with it.
func teamSSODiscover(issuer string) error {
	parsed, err := url.Parse(issuer)
	if err != nil || parsed.Host == "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("%q isn't an issuer address such as https://login.example.com/realms/acme", issuer)
	}
	local := parsed.Hostname() == "localhost" || parsed.Hostname() == "127.0.0.1"
	if parsed.Scheme != "https" && !(parsed.Scheme == "http" && local) {
		return fmt.Errorf("the issuer %s isn't on https", issuer)
	}
	if teamSSOEntra(issuer) {
		tenant, _, _ := strings.Cut(strings.Trim(parsed.Path, "/"), "/")
		if tenant == "common" || tenant == "organizations" || tenant == "consumers" || tenant == "" {
			return errors.New("that's Entra's address for many organisations, whose issuer is a {tenantid} placeholder: give the tenant's own, https://login.microsoftonline.com/<tenant-id>/v2.0 (Entra: Overview > Tenant ID)")
		}
	}
	address := strings.TrimSuffix(issuer, "/") + "/.well-known/openid-configuration"
	response, err := teamSSOHTTP.Get(address)
	if err != nil {
		return fmt.Errorf("the provider's discovery document: %w", err)
	}
	defer response.Body.Close()
	var found teamSSODiscovery
	if response.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&found) != nil || found.Issuer == "" {
		return fmt.Errorf("no OpenID Connect discovery document at %s (HTTP %d): hi signs in with OIDC providers only; GitHub, for one, isn't", address, response.StatusCode)
	}
	switch {
	case found.Issuer != issuer && strings.TrimSuffix(found.Issuer, "/") == strings.TrimSuffix(issuer, "/"):
		return fmt.Errorf("the provider calls itself %s, and ID tokens must match it to the character: add it with that", found.Issuer)
	case found.Issuer != issuer:
		return fmt.Errorf("the document at %s is for issuer %s, not %s", address, found.Issuer, issuer)
	case found.Authorization == "" || found.Token == "" || found.Keys == "":
		return fmt.Errorf("the provider's document names no authorization, token, or key address: %s", address)
	case len(found.ResponseTypes) > 0 && !slices.Contains(found.ResponseTypes, "code"):
		return errors.New("the provider doesn't offer the authorization code flow, which hi uses")
	case len(found.ChallengeTypes) > 0 && !slices.Contains(found.ChallengeTypes, "S256"):
		return errors.New("the provider doesn't take an S256 PKCE challenge, which hi sends")
	}
	return nil
}

// teamSSOProviderAdd is hi team sso provider add: a new provider, or new
// details for one, which the teams using it get at once.
func teamSSOProviderAdd(args []string, stdin io.Reader, stdout io.Writer) error {
	flags := &flagSet{newComputeFlags("team sso provider add", io.Discard)}
	clientID := flags.String("client-id", "", "the client ID")
	groupsClaim := flags.String("groups-claim", "", "the claim with the person's groups")
	positional, err := flags.parse(args)
	if err != nil {
		return err
	}
	if len(positional) != 2 {
		return usageError{"usage: hi team sso provider add <name> <issuer> [--client-id <id>] [--groups-claim <claim>]"}
	}
	name, issuer := positional[0], strings.TrimSpace(positional[1])
	if !teamSSOProviderName.MatchString(name) {
		return fmt.Errorf("a provider's name is lowercase letters, digits, and dashes, such as acme, not %q", name)
	}
	if err := teamSSODiscover(issuer); err != nil {
		return err
	}
	provider := teamSSOProvider{Issuer: issuer, ClientID: strings.TrimSpace(*clientID), GroupsClaim: strings.TrimSpace(*groupsClaim)}
	// The ID and secret: asked at a terminal, or the secret read from a
	// pipe or file: hi team sso provider add acme <issuer> --client-id <id> < secret.txt
	file, isFile := stdin.(*os.File)
	atTerminal := isFile && interactive(stdin, stdout)
	if provider.ClientID == "" {
		if !atTerminal {
			return errors.New("give the client ID with --client-id, or run this in a terminal, where hi asks for it")
		}
		fmt.Fprint(stdout, "The client ID: ")
		line, _ := bufio.NewReader(io.LimitReader(stdin, 4096)).ReadString('\n')
		if provider.ClientID = strings.TrimSpace(line); provider.ClientID == "" {
			return errors.New("no client ID")
		}
	}
	var secret []byte
	switch {
	case atTerminal:
		fmt.Fprint(stdout, "The client secret: ")
		secret, err = readSecret(file, stdout)
		fmt.Fprintln(stdout)
	case isFile && !teamStdinTerminal(file):
		secret, err = io.ReadAll(io.LimitReader(stdin, 4096))
	default:
		return errors.New("hi needs the client secret: run this in a terminal, where it asks for it, or give it on standard input (< secret.txt)")
	}
	if err != nil {
		return err
	}
	if provider.ClientSecret = strings.TrimSpace(string(secret)); provider.ClientSecret == "" {
		return errors.New("no client secret")
	}
	providers := loadTeamSSOProviders()
	_, had := providers[name]
	providers[name] = provider
	if err := saveTeamSSOProviders(providers); err != nil {
		return err
	}
	var teams []string
	for _, team := range teamSSOTeams() {
		config, _ := loadTeamSSO(team)
		if config.Provider != name {
			continue
		}
		if err := saveTeamSSO(team, teamSSOConfigFor(name, provider, config)); err != nil {
			return err
		}
		teams = append(teams, team)
	}
	if had {
		fmt.Fprintf(stdout, "Provider %s is now %s.", name, issuer)
	} else {
		fmt.Fprintf(stdout, "Provider %s is %s.", name, issuer)
	}
	if len(teams) > 0 {
		fmt.Fprintf(stdout, " Teams %s sign in with it as it is now.\n", strings.Join(teams, ", "))
	} else {
		fmt.Fprintf(stdout, " Turn it on for a team: hi team sso <team> on %s --domain <domain> (or --group, or --anyone).\n", name)
	}
	return nil
}

// teamSSOConfigFor is a team's sign-in with the provider as it is now,
// keeping the team's rules. A new issuer or client ends the sessions.
func teamSSOConfigFor(name string, provider teamSSOProvider, config netExposeSSOConfig) netExposeSSOConfig {
	if config.Key == "" || config.Issuer != provider.Issuer || config.ClientID != provider.ClientID {
		config.Key = netExposeSSORandom() + netExposeSSORandom()
	}
	config.Provider, config.Issuer, config.ClientID, config.ClientSecret, config.GroupsClaim = name, provider.Issuer, provider.ClientID, provider.ClientSecret, provider.GroupsClaim
	return config
}

// teamSSOProviderRemove is hi team sso provider rm.
func teamSSOProviderRemove(name string, stdout io.Writer) error {
	providers := loadTeamSSOProviders()
	if _, ok := providers[name]; !ok {
		return fmt.Errorf("no provider named %s on this machine", name)
	}
	var using []string
	for _, team := range teamSSOTeams() {
		if config, _ := loadTeamSSO(team); config.Provider == name {
			using = append(using, team)
		}
	}
	if len(using) > 0 {
		return fmt.Errorf("teams %s sign in with %s: hi team sso <team> off first", strings.Join(using, ", "), name)
	}
	delete(providers, name)
	if err := saveTeamSSOProviders(providers); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Provider %s is gone from this machine. Its client stays registered with the provider until someone there removes it.\n", name)
	return nil
}

// teamSSOTeams are the teams that have sign-in on.
func teamSSOTeams() []string {
	var teams []string
	entries, _ := os.ReadDir(teamsDir())
	for _, entry := range entries {
		if teamNamePattern.MatchString(entry.Name()) && fileExists(teamSSOFile(entry.Name())) {
			teams = append(teams, entry.Name())
		}
	}
	sort.Strings(teams)
	return teams
}

// teamSSOOn is hi team sso <team> on <provider> and its rules.
func teamSSOOn(team string, args []string, stdout io.Writer) error {
	flags := &flagSet{newComputeFlags("team sso on", io.Discard)}
	domains := flags.String("domain", "", "accounts on these domains")
	groups := flags.String("group", "", "people in these groups")
	anyone := flags.Bool("anyone", false, "everyone the provider signs in")
	positional, err := flags.parse(args)
	if err != nil {
		return err
	}
	if len(positional) != 1 {
		return usageError{"usage: hi team sso <team> on <provider> [--domain <d>[,<d>]] [--group <g>[,<g>]] [--anyone]"}
	}
	name := positional[0]
	if _, err := loadTeam(team); err != nil {
		return err
	}
	provider, ok := loadTeamSSOProviders()[name]
	if !ok {
		return fmt.Errorf("no provider named %s on this machine: hi team sso provider add %s <issuer> first", name, name)
	}
	if teamSlotDomain(team, teamSlotProduction) == "" {
		return fmt.Errorf("signing in needs %s on a name that stays, where the provider sends people back: hi team domain %s on first", team, team)
	}
	split := func(text string) []string {
		var values []string
		for value := range strings.SplitSeq(text, ",") {
			if value = strings.TrimSpace(value); value != "" {
				values = append(values, value)
			}
		}
		return values
	}
	config, _ := loadTeamSSO(team)
	config.Domains, config.Groups, config.Anyone = split(strings.ToLower(*domains)), split(*groups), *anyone
	for _, domain := range config.Domains {
		if !netExposeDomainName.MatchString(domain) {
			return fmt.Errorf("--domain %q isn't a domain such as example.com", domain)
		}
	}
	google, entra := netExposeSSOGoogle(provider.Issuer), teamSSOEntra(provider.Issuer)
	switch {
	case len(config.Domains) == 0 && len(config.Groups) == 0 && !config.Anyone:
		return errors.New("say who gets in: --domain example.com, --group <group>, or --anyone for everyone the provider signs in")
	case config.Anyone && (len(config.Domains) > 0 || len(config.Groups) > 0):
		return errors.New("--anyone goes without --domain and --group")
	case google && len(config.Domains) == 0:
		return errors.New("Google signs in every Google account: give the Workspace's domain, --domain example.com")
	case entra && len(config.Domains) > 0:
		return errors.New("Entra ID doesn't check the email it sends, so hi can't go by its domain: the tenant in the issuer already keeps out other organisations (--anyone), or use --group")
	}
	config.Log = teamSSOLog(team)
	before := config.Key
	config = teamSSOConfigFor(name, provider, config)
	if config.Key == before {
		// New rules end the sessions made under the old ones.
		config.Key = netExposeSSORandom() + netExposeSSORandom()
	}
	if err := saveTeamSSO(team, config); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "%s's app lets in %s, signed in with %s, as well as whoever has its link. Register these return addresses with the provider, if they aren't yet:\n", team, netExposeSSORules(&config), name)
	for _, slot := range []string{teamSlotProduction, teamSlotPreview} {
		fmt.Fprintf(stdout, "  https://%s%s\n", teamSlotDomain(team, slot), netExposeSSOCallback)
	}
	teamWarnOldHi(team, stdout)
	return nil
}

// teamSSOOff is hi team sso <team> off: the key only, and every session
// ends with the file.
func teamSSOOff(team string, stdout io.Writer) error {
	if _, err := loadTeam(team); err != nil {
		return err
	}
	if err := os.Remove(teamSSOFile(team)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	fmt.Fprintf(stdout, "%s's app lets in only whoever has its link again; everyone signed in is signed out.\n", team)
	return nil
}

// teamSSOList is hi team sso: the providers, and the teams using them.
func teamSSOList(stdout io.Writer) error {
	providers := loadTeamSSOProviders()
	if len(providers) == 0 {
		fmt.Fprintln(stdout, "No sign-in provider on this machine yet. hi team sso provider add <name> <issuer> adds one, such as an organisation's Google Workspace, Entra ID, Keycloak, Auth0, or Okta.")
		return nil
	}
	using := map[string][]string{}
	for _, team := range teamSSOTeams() {
		config, _ := loadTeamSSO(team)
		using[config.Provider] = append(using[config.Provider], fmt.Sprintf("%s (%s)", team, netExposeSSORules(&config)))
	}
	names := make([]string, 0, len(providers))
	for name := range providers {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fmt.Fprintf(stdout, "%s: %s\n", name, providers[name].Issuer)
		if teams := using[name]; len(teams) > 0 {
			fmt.Fprintf(stdout, "  for %s\n", strings.Join(teams, ", "))
		} else {
			fmt.Fprintln(stdout, "  no team yet")
		}
	}
	return nil
}

// teamSSOShown is how many of the last sign-ins hi team sso <team> shows.
const teamSSOShown = 10

// teamSSOShow is hi team sso <team>: its provider, rules, and last
// sign-ins.
func teamSSOShow(team string, stdout io.Writer) error {
	if _, err := loadTeam(team); err != nil {
		return err
	}
	config, ok := loadTeamSSO(team)
	if !ok {
		fmt.Fprintf(stdout, "%s's app lets in whoever has its link. hi team sso %s on <provider> lets people sign in as well.\n", team, team)
		return nil
	}
	fmt.Fprintf(stdout, "%s's app lets in %s, signed in with %s (%s), as well as whoever has its link.\n", team, netExposeSSORules(&config), config.Provider, config.Issuer)
	data, _ := os.ReadFile(teamSSOLog(team))
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	var records []netExposeSSORecord
	for _, line := range lines[max(0, len(lines)-teamSSOShown):] {
		var record netExposeSSORecord
		if json.Unmarshal([]byte(line), &record) == nil {
			records = append(records, record)
		}
	}
	if len(records) == 0 {
		fmt.Fprintln(stdout, "Nobody has signed in yet.")
		return nil
	}
	fmt.Fprintln(stdout, "Last sign-ins:")
	for _, record := range records {
		line := fmt.Sprintf("  %s  %-7s %s  %s", record.Time.Local().Format("2006-01-02 15:04"), record.Result, firstNonEmpty(record.Email, "(no email)"), record.Host)
		if record.Reason != "" {
			line += ": " + record.Reason
		}
		fmt.Fprintln(stdout, line)
	}
	return nil
}
