package cli

// Each app's Network and Data on the admin page, the machine's Network
// with what to ask the IT team, and every app's Advisories (hi_admin.md,
// release 5). Reads come from the app's files, or from the hi app
// command's own words where it says them best; changes run the command,
// as the admin, and what can't be undone needs the app's name typed.

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"
)

// adminNetworkChange is the Network and Data sections' and Advisories'
// part of a change (adminSettingChange carries it).
type adminNetworkChange struct {
	Mode       string `json:"mode"` // addresses: domain, netbird, host, names
	Host       string `json:"host"`
	Production string `json:"production"`
	HTTPS      bool   `json:"https"`
	SSO        string `json:"sso"` // a sign-in provider's name
	Domains    string `json:"domains"`
	Groups     string `json:"groups"`
	Anyone     bool   `json:"anyone"`
	IPs        string `json:"ips"`
	Snapshot   string `json:"snapshot"`
	Version    int    `json:"version"`
	Code       bool   `json:"code"`
	Confirm    string `json:"confirm"` // the app's name, typed
	Keep       string `json:"keep"`
	Offsite    string `json:"offsite"`
	Region     string `json:"region"`
	KeyID      string `json:"key_id"`
	Secret     string `json:"secret"`
	ID         string `json:"id"`
	Package    string `json:"package"`
	Because    string `json:"because"`
}

// adminSnapshotRef is what hi app restore takes: a snapshot's ID, latest,
// a release (v3), or a day (2026-10-12).
var adminSnapshotRef = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,39}$`)

// adminAppNetwork is an app's Network section.
type adminAppNetwork struct {
	Name      string         `json:"name"`
	Peer      string         `json:"peer"`
	Joined    bool           `json:"joined"`
	Dev       string         `json:"dev"`       // hi app dev <name>: the dev box and its NetBird name
	Addresses string         `json:"addresses"` // how they're named, in words
	Mode      string         `json:"mode"`      // domain, netbird, host, or names
	Host      string         `json:"host,omitempty"`
	Names     *appNames      `json:"names,omitempty"`
	Domain    string         `json:"domain,omitempty"` // the machine's apps' domain, when it has one
	DomainOn  bool           `json:"domain_on"`
	AllowIPs  []string       `json:"allow_ips"`
	SSO       string         `json:"sso"` // hi app sso <name>, in its words
	Providers []string       `json:"providers"`
	URLs      []adminAddress `json:"urls"`
	Ask       string         `json:"ask"` // what to ask the IT team for it
}

// adminHiText is a read-only hi command's words, for a section that
// shows them as the command does; "" when it said nothing.
func (s *adminServer) adminHiText(by string, args ...string) string {
	out, err := adminRunHi(s.hi, by, "", args...)
	if err != nil {
		return firstNonEmpty(out, err.Error())
	}
	return out
}

func (s *adminServer) appNetwork(name string, admin adminPerson) (adminAppNetwork, error) {
	config, err := loadTeam(name)
	if err != nil || !appNamePattern.MatchString(name) {
		return adminAppNetwork{}, fmt.Errorf("no app %q on this machine", name)
	}
	view := adminAppNetwork{Name: name, Peer: appPeerName(name), Joined: appPeerJoined(name), Addresses: appAddressesWords(config),
		DomainOn: config.Domain, AllowIPs: config.AllowIPs, Providers: []string{}, URLs: adminAppOf(name).Addresses}
	if view.AllowIPs == nil {
		view.AllowIPs = []string{}
	}
	switch {
	case config.Addresses == appAddressesNetBird:
		view.Mode = "netbird"
	case config.Addresses == appAddressesNames:
		view.Mode, view.Names = "names", config.Names
	case config.Addresses != "":
		view.Mode, view.Host = "host", config.Addresses
	default:
		view.Mode = "domain"
	}
	if settings := loadTeamsSettings(); settings.Domain != "" {
		view.Domain = settings.Domain
	}
	view.Providers = adminSSOProviders()
	view.Dev = s.adminHiText(admin.Name, "app", "dev", name)
	view.SSO = s.adminHiText(admin.Name, "app", "sso", name)
	view.Ask = adminITAsk(config)
	return view, nil
}

// adminSSOProviders are the sign-in providers set up on this machine.
func adminSSOProviders() []string {
	names := sortedKeys(loadTeamSSOProviders())
	if names == nil {
		names = []string{}
	}
	return names
}

// adminITAsk is what to ask the IT team for an app: its NetBird peer's
// policies, and the names its addresses need.
func adminITAsk(config appConfig) string {
	name := config.Name
	peer := appPeerHostname(name)
	var b strings.Builder
	fmt.Fprintf(&b, "App %s (its NetBird peer %s, in the group hi-%s):\n", name, peer, name)
	fmt.Fprintf(&b, "  - A NetBird policy from the app's people's group to hi-%s, TCP 80 and 8080 (production and Stage).\n", name)
	fmt.Fprintf(&b, "  - One from its owners' group to hi-%s, TCP 8888 and 22 (the dev page, and SSH to the dev box).\n", name)
	switch {
	case config.Addresses == appAddressesNames && config.Names != nil:
		b.WriteString("  - " + strings.ReplaceAll(strings.TrimSpace(appNamesAsk(name, *config.Names)), "\n", "\n    ") + "\n")
	case config.Addresses != "" && config.Addresses != appAddressesNetBird:
		fmt.Fprintf(&b, "  - An A record from %s to the app's NetBird address (hi app dev %s shows it once it joined).\n", config.Addresses, name)
	case config.Addresses == "":
		if domain, ok := loadHiDomain(); ok {
			fmt.Fprintf(&b, "  - Nothing in DNS: its names are under this machine's domain, %s, which hi keeps.\n", domain.addressName(name, appSlotProduction))
		}
	}
	if !appPeerJoined(name) {
		fmt.Fprintf(&b, "  - It hasn't joined NetBird yet: a setup key for it (Add Peer → Server, one-off, with the auto-group hi-%s), given in its Network section.\n", name)
	}
	return b.String()
}

// adminAppData is an app's Data section.
type adminAppData struct {
	Name       string         `json:"name"`
	Snapshots  []appSnapshot  `json:"snapshots"`
	SnapError  string         `json:"snap_error,omitempty"`
	Production int            `json:"production"`
	Releases   []adminRelease `json:"releases"`
	Data       string         `json:"data"`    // hi app data <name>
	Backups    string         `json:"backups"` // hi app backups <name>
	Jobs       string         `json:"jobs"`    // hi app jobs <name>
	StageEmpty bool           `json:"stage_empty"`
}

func (s *adminServer) appData(name string, admin adminPerson) (adminAppData, error) {
	config, err := loadTeam(name)
	if err != nil || !appNamePattern.MatchString(name) {
		return adminAppData{}, fmt.Errorf("no app %q on this machine", name)
	}
	view := adminAppData{Name: name, Snapshots: []appSnapshot{}, Releases: []adminRelease{}, StageEmpty: config.PreviewData == appPreviewEmpty}
	if snapshots, err := appListSnapshots(name); err != nil {
		view.SnapError = err.Error()
	} else if snapshots != nil {
		view.Snapshots = snapshots
		sort.Slice(view.Snapshots, func(i, j int) bool { return view.Snapshots[i].Time.After(view.Snapshots[j].Time) })
	}
	if detail, err := adminAppDetailOf(name, admin); err == nil {
		view.Production, view.Releases = detail.Production, detail.Releases
	}
	view.Data = s.adminHiText(admin.Name, "app", "data", name)
	view.Backups = s.adminHiText(admin.Name, "app", "backups", name)
	view.Jobs = s.adminHiText(admin.Name, "app", "jobs", name)
	return view, nil
}

// adminMachineNetwork is the machine's Network view.
type adminMachineNetwork struct {
	Domain    string            `json:"domain,omitempty"` // the apps' domain (hi app domain)
	NetDomain string            `json:"net_domain,omitempty"`
	HasToken  bool              `json:"has_token"`
	SSO       string            `json:"sso"` // hi app sso, in its words
	Providers []string          `json:"providers"`
	Apps      []adminAppNetwork `json:"apps"`
	Expose    string            `json:"expose,omitempty"` // the org server's public URL for cloud runs
	Ask       string            `json:"ask"`              // all of what to ask the IT team, to send
}

func (s *adminServer) machineNetwork(admin adminPerson) adminMachineNetwork {
	view := adminMachineNetwork{Providers: adminSSOProviders(), Apps: []adminAppNetwork{}}
	view.Domain = loadTeamsSettings().Domain
	if domain, ok := loadHiDomain(); ok {
		view.NetDomain = domain.addressName("<app>", appSlotProduction)
	}
	if _, err := netbirdAPIToken(); err == nil {
		view.HasToken = true
	}
	view.SSO = s.adminHiText(admin.Name, "app", "sso")
	var ask strings.Builder
	config, _ := loadAdminConfig()
	if config.Reach != adminReachLocal {
		fmt.Fprintf(&ask, "The admin page (%s):\n  - A NetBird policy from the admins' group to %s, TCP %d", adminURL(config), adminPolicyTarget(config), adminPort)
		if adminHasDomain(config) {
			ask.WriteString(" and 443")
		}
		ask.WriteString(".\n")
	}
	for _, name := range adminAppNames() {
		app, err := loadTeam(name)
		if err != nil {
			continue
		}
		view.Apps = append(view.Apps, adminAppNetwork{Name: name, Peer: appPeerName(name), Joined: appPeerJoined(name),
			Addresses: appAddressesWords(app), DomainOn: app.Domain, AllowIPs: app.AllowIPs})
		if ask.Len() > 0 {
			ask.WriteString("\n")
		}
		ask.WriteString(adminITAsk(app))
	}
	if dir := adminOrgDir(); dir != "" {
		var expose map[string]any
		if err := adminOrgCall(http.MethodGet, "/admin/expose", nil, &expose); err == nil {
			if url, _ := expose["url"].(string); url != "" {
				view.Expose = url
			}
		}
		fmt.Fprintf(&ask, "\nThe org server: a NetBird policy from the org's people to this machine, TCP %d (devices join, ask, and approve there).\n", serverDefaultPort)
	}
	view.Ask = ask.String()
	return view
}

// adminAdvisory is one of an app's advisories, for Advisories.
type adminAdvisory struct {
	apiAdvisory
	App  string `json:"app"`
	Tier string `json:"tier"` // kev, incident, report, or "" when ignored
}

func adminAdvisories() map[string]any {
	list := []adminAdvisory{}
	notes := map[string]string{}
	for _, name := range adminAppNames() {
		report := appAdvisoriesReport(name)
		switch {
		case report.Bom == "":
			notes[name] = "nothing released yet"
		case report.At.IsZero():
			notes[name] = "not matched yet: within a day of a release"
		}
		for _, advisory := range report.Advisories {
			list = append(list, adminAdvisory{apiAdvisory: advisory, App: name, Tier: advisory.tier()})
		}
	}
	rank := map[string]int{"kev": 0, "incident": 1, "report": 2, "": 3}
	sort.SliceStable(list, func(i, j int) bool {
		if rank[list[i].Tier] != rank[list[j].Tier] {
			return rank[list[i].Tier] < rank[list[j].Tier]
		}
		return list[i].EPSS > list[j].EPSS
	})
	return map[string]any{"advisories": list, "notes": notes}
}

// adminChangeNetworkData makes a Network, Data, or advisory change: the
// command to run, what goes on its stdin, whether it's a job, and what the
// app's channel hears. handled is false for anything else.
func (s *adminServer) adminChangeNetworkData(admin adminPerson, change adminSettingChange, config appConfig) (handled bool, args []string, stdin, said string, job string, err error) {
	app := config.Name
	n := change.adminNetworkChange
	typed := func(what string) error {
		if strings.TrimSpace(n.Confirm) != app {
			return fmt.Errorf("type the app's name, %s, to %s", app, what)
		}
		return nil
	}
	switch change.What {
	case "netbird":
		key := strings.TrimSpace(change.Key)
		if key == "" {
			return true, nil, "", "", "", errors.New("paste the NetBird setup key")
		}
		file, ferr := os.CreateTemp(adminDir(), "netbird-*.key")
		if ferr != nil {
			return true, nil, "", "", "", ferr
		}
		file.Chmod(0o600)
		file.WriteString(key + "\n")
		file.Close()
		// hi app dev <name> netbird reads the key from the file, once, and
		// deletes it.
		return true, []string{"app", "dev", app, "netbird", file.Name()}, "", "gave the dev box its NetBird setup key", "", nil
	case "addresses":
		switch n.Mode {
		case "domain", "netbird":
			args = []string{"app", "addresses", app, n.Mode}
		case "host":
			if err := appCheckHost(strings.TrimSpace(n.Host)); err != nil {
				return true, nil, "", "", "", err
			}
			args = []string{"app", "addresses", app, strings.TrimSpace(n.Host)}
		case "names":
			if err := appCheckHost(strings.TrimSpace(n.Production)); err != nil {
				return true, nil, "", "", "", err
			}
			args = []string{"app", "addresses", app, "names", strings.TrimSpace(n.Production)}
			if n.HTTPS {
				args = append(args, "--https")
			}
		default:
			return true, nil, "", "", "", errors.New("domain, netbird, a host, or names")
		}
		return true, args, "", "moved the app's addresses", "Move " + app + "'s addresses", nil
	case "domain":
		on := map[bool]string{true: "on", false: "off"}[change.On]
		return true, []string{"app", "domain", app, on}, "", "turned the app's name on the machine's domain " + on, "", nil
	case "allowip":
		ips := strings.TrimSpace(n.IPs)
		if ips == "" {
			ips = "off"
		}
		said = "let only " + ips + " into the app"
		if ips == "off" {
			said = "let any address with its link into the app again"
		}
		return true, []string{"app", "allow-ip", app, ips}, "", said, "", nil
	case "sso":
		if n.SSO == "" {
			return true, []string{"app", "sso", app, "off"}, "", "turned the app's sign-in off", "", nil
		}
		args = []string{"app", "sso", app, "on", n.SSO}
		if d := strings.TrimSpace(n.Domains); d != "" {
			args = append(args, "--domain", d)
		}
		if g := strings.TrimSpace(n.Groups); g != "" {
			args = append(args, "--group", g)
		}
		if n.Anyone {
			args = append(args, "--anyone")
		}
		return true, args, "", "turned the app's sign-in on, with " + n.SSO, "", nil
	case "restore":
		if err := typed("restore it"); err != nil {
			return true, nil, "", "", "", err
		}
		if !adminSnapshotRef.MatchString(n.Snapshot) {
			return true, nil, "", "", "", fmt.Errorf("no snapshot %q", n.Snapshot)
		}
		args = []string{"app", "restore", app, n.Snapshot, "--yes"}
		if n.Code {
			args = append(args, "--code")
		}
		return true, args, "", "restored the app from the snapshot " + n.Snapshot, "Restore " + app + " from " + n.Snapshot, nil
	case "rollback":
		if err := typed("roll it back"); err != nil {
			return true, nil, "", "", "", err
		}
		if n.Version <= 0 {
			return true, nil, "", "", "", errors.New("which version")
		}
		args = []string{"app", "rollback", app, fmt.Sprintf("v%d", n.Version), "--yes"}
		if n.Code { // the code only: production's data stays as it is
			args = append(args, "--code-only")
		}
		return true, args, "", fmt.Sprintf("rolled production back to v%d", n.Version), fmt.Sprintf("Roll %s back to v%d", app, n.Version), nil
	case "fork":
		return true, []string{"app", "data", app, "fork"}, "", "gave Stage a new fork of production's data", "", nil
	case "copy":
		if err := typed("copy Stage's data to production"); err != nil {
			return true, nil, "", "", "", err
		}
		return true, []string{"app", "data", app, "copy", appSlotPreview, appSlotProduction, "--yes"}, "", "copied Stage's data to production (production's own was snapshotted first)", "Copy " + app + "'s Stage data to production", nil
	case "stagedata":
		mode := "fork"
		if change.On {
			mode = "empty"
		}
		return true, []string{"app", "data", app, appSlotPreview, mode}, "", map[bool]string{true: "made Stage start empty at every release", false: "made Stage start from a fork of production's data again"}[change.On], "", nil
	case "keep":
		return true, []string{"app", "backups", app, "--keep", strings.TrimSpace(n.Keep)}, "", "changed how many snapshots the app keeps: " + strings.TrimSpace(n.Keep), "", nil
	case "offsite":
		target := strings.TrimSpace(n.Offsite)
		if target == "" || target == "off" {
			return true, []string{"app", "backups", app, "--offsite", "off"}, "", "stopped copying the app's snapshots offsite", "", nil
		}
		args = []string{"app", "backups", app, "--offsite", target}
		if region := strings.TrimSpace(n.Region); region != "" {
			args = append(args, "--region", region)
		}
		return true, args, strings.TrimSpace(n.KeyID) + "\n" + strings.TrimSpace(n.Secret) + "\n", "copies the app's snapshots offsite now", "", nil
	case "advisory":
		id := strings.TrimSpace(n.ID)
		if id == "" || strings.ContainsAny(id, " \n") {
			return true, nil, "", "", "", errors.New("which advisory")
		}
		if change.Remove {
			return true, []string{"app", "advisory", app, "unignore", id}, "", "took back ignoring " + id, "", nil
		}
		because := strings.Join(strings.Fields(n.Because), " ")
		if because == "" {
			return true, nil, "", "", "", errors.New("say why it doesn't apply: the app's people see it")
		}
		args = []string{"app", "advisory", app, "ignore", id, "--because", because}
		if p := strings.TrimSpace(n.Package); p != "" {
			args = append(args, "--package", p)
		}
		return true, args, "", "said " + id + " doesn't apply: " + because, "", nil
	}
	return false, nil, "", "", "", nil
}

// adminMachineChange is a change in the machine's Network view.
func (s *adminServer) adminMachineChange(admin adminPerson, change adminSettingChange) (map[string]any, error) {
	n := change.adminNetworkChange
	var args []string
	stdin, said := "", ""
	switch change.What {
	case "domain":
		domain := strings.TrimSpace(change.Name)
		if domain == "" || domain == "off" {
			args, said = []string{"app", "domain", "off"}, "turned the apps' domain off"
		} else {
			args, said = []string{"app", "domain", domain}, "set the apps' domain to "+domain
			if token := strings.TrimSpace(change.Key); token != "" {
				stdin = token + "\n"
			}
		}
	case "provider":
		if change.Remove {
			args, said = []string{"app", "sso", "provider", "rm", strings.TrimSpace(n.SSO)}, "removed the sign-in provider "+n.SSO
			break
		}
		name, issuer := strings.TrimSpace(n.SSO), strings.TrimSpace(n.Host)
		if name == "" || issuer == "" {
			return nil, errors.New("its name and its issuer's address")
		}
		args = []string{"app", "sso", "provider", "add", name, issuer}
		if id := strings.TrimSpace(change.Name); id != "" {
			args = append(args, "--client-id", id)
		}
		if claim := strings.TrimSpace(n.Groups); claim != "" {
			args = append(args, "--groups-claim", claim)
		}
		stdin = strings.TrimSpace(n.Secret) + "\n"
		said = "added the sign-in provider " + name
	default:
		return nil, fmt.Errorf("no such change: %q", change.What)
	}
	out, err := adminRunHi(s.hi, admin.Name, stdin, args...)
	if err != nil {
		return nil, err
	}
	adminRecord(admin.Name, "", said)
	return map[string]any{"text": out}, nil
}

// adminBom is production's bill of materials, or a release's, as a file.
func (s *adminServer) adminBom(w http.ResponseWriter, admin adminPerson, name, release string) {
	args := []string{"app", "bom", name}
	if release != "" {
		if !strings.HasPrefix(release, "v") || strings.Trim(release[1:], "0123456789") != "" {
			http.Error(w, "a release such as v3", http.StatusBadRequest)
			return
		}
		args = append(args, release)
	}
	out, err := adminRunHi(s.hi, admin.Name, "", args...)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s-bom-%s.cdx.json"`, name, firstNonEmpty(release, "production")))
	w.Write([]byte(out))
}

func (s *adminServer) networkRoutes(mux *http.ServeMux, reply func(http.ResponseWriter, int, any),
	decode func(http.ResponseWriter, *http.Request, any) bool,
	signedIn func(func(http.ResponseWriter, *http.Request, adminPerson)) http.HandlerFunc) {
	failure := func(w http.ResponseWriter, status int, err error) {
		reply(w, status, map[string]string{"error": err.Error()})
	}
	mux.HandleFunc("GET /api/app/network", signedIn(func(w http.ResponseWriter, r *http.Request, admin adminPerson) {
		view, err := s.appNetwork(r.URL.Query().Get("name"), admin)
		if err != nil {
			failure(w, http.StatusNotFound, err)
			return
		}
		reply(w, http.StatusOK, view)
	}))
	mux.HandleFunc("GET /api/app/data", signedIn(func(w http.ResponseWriter, r *http.Request, admin adminPerson) {
		view, err := s.appData(r.URL.Query().Get("name"), admin)
		if err != nil {
			failure(w, http.StatusNotFound, err)
			return
		}
		reply(w, http.StatusOK, view)
	}))
	mux.HandleFunc("GET /api/app/bom", signedIn(func(w http.ResponseWriter, r *http.Request, admin adminPerson) {
		name := r.URL.Query().Get("name")
		if _, err := loadTeam(name); err != nil || !appNamePattern.MatchString(name) {
			failure(w, http.StatusNotFound, fmt.Errorf("no app %q", name))
			return
		}
		s.adminBom(w, admin, name, r.URL.Query().Get("release"))
	}))
	mux.HandleFunc("GET /api/network", signedIn(func(w http.ResponseWriter, r *http.Request, admin adminPerson) {
		reply(w, http.StatusOK, s.machineNetwork(admin))
	}))
	mux.HandleFunc("POST /api/network", signedIn(func(w http.ResponseWriter, r *http.Request, admin adminPerson) {
		var change adminSettingChange
		if !decode(w, r, &change) {
			return
		}
		answer, err := s.adminMachineChange(admin, change)
		if err != nil {
			failure(w, http.StatusBadRequest, err)
			return
		}
		reply(w, http.StatusOK, answer)
	}))
	mux.HandleFunc("GET /api/advisories", signedIn(func(w http.ResponseWriter, r *http.Request, admin adminPerson) {
		reply(w, http.StatusOK, adminAdvisories())
	}))
}
