package main

// Internal by default (docs/specs/approved/hi_team_internal.md): a team's
// addresses are on its NetBird network, through three peers of its own,
// <team>, <team>-preview, and <team>-dev, each passing its port 80 to a
// relay in the team's service. An owner makes one public too, on the dev
// page, through hi net expose.

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// teamPublic says which of a team's addresses are public too.
type teamPublic struct {
	Production bool `json:"production,omitempty"`
	Preview    bool `json:"preview,omitempty"`
	Dev        bool `json:"dev,omitempty"`
}

// teamAddressDev is the dev page's address, beside the slots'.
const teamAddressDev = "dev"

// teamPublicOn says whether an address is public: a team from before hi
// 2.5, with no public setting, has them all public.
func teamPublicOn(config teamConfig, address string) bool {
	if config.Public == nil {
		return true
	}
	switch address {
	case teamSlotProduction:
		return config.Public.Production
	case teamSlotPreview:
		return config.Public.Preview
	case teamAddressDev:
		return config.Public.Dev
	}
	return false
}

func teamSetPublic(config *teamConfig, address string, on bool) error {
	if config.Public == nil {
		// From all public, as a team from before has them.
		config.Public = &teamPublic{Production: true, Preview: true, Dev: true}
	}
	switch address {
	case teamSlotProduction:
		config.Public.Production = on
	case teamSlotPreview:
		config.Public.Preview = on
	case teamAddressDev:
		config.Public.Dev = on
	default:
		return fmt.Errorf("the addresses are production, preview, and dev, not %q", address)
	}
	return nil
}

// teamInternalPort is where an address is on the team's peer: production
// on port 80, so people needn't think of ports, the preview on 8080, and
// the dev page on 8888.
func teamInternalPort(address string) int {
	switch address {
	case teamSlotPreview:
		return 8080
	case teamAddressDev:
		return 8888
	}
	return 80
}

// teamNetbirdDomain is the NetBird network's domain, from this machine's
// own name on it (laptop.corp.example is on corp.example); netbird.cloud, NetBird's
// own, when it can't tell. Tests replace it.
var teamNetbirdDomain = func() string {
	teamNetbirdDomainOnce.Do(func() {
		teamNetbirdDomainName = "netbird.cloud"
		out, err := exec.Command("netbird", "status", "--json").Output()
		if err != nil {
			return
		}
		var status struct {
			FQDN string `json:"fqdn"`
		}
		if json.Unmarshal(out, &status) == nil {
			if _, domain, ok := strings.Cut(status.FQDN, "."); ok && domain != "" {
				teamNetbirdDomainName = domain
			}
		}
	})
	return teamNetbirdDomainName
}

var (
	teamNetbirdDomainOnce sync.Once
	teamNetbirdDomainName string
)

// teamInternalURL is an address on the team's NetBird network: its peer's
// name, and the address's port.
func teamInternalURL(team, address string) string {
	config, _ := loadTeam(team)
	if name := teamAddressName(config, address); name != "" {
		if config.Names.HTTPS {
			return "https://" + name + "/"
		}
		return "http://" + name + "/"
	}
	if host := teamHTTPSHost(team, address); host != "" && config.Addresses == "" {
		return "https://" + host + "/"
	}
	port := ""
	if p := teamInternalPort(address); p != 80 {
		port = fmt.Sprintf(":%d", p)
	}
	return "http://" + firstNonEmpty(teamAddressesHost(config), teamPeerName(team)) + port + "/"
}

// teamPeerName is the team's peer's name on the network, as NetBird gave
// it (dev/netbird-fqdn, which the peer's own status writes), or as hi
// asked for it: quarry.corp.example.
func teamPeerName(team string) string {
	if data, err := os.ReadFile(filepath.Join(teamDevDir(team), "netbird-fqdn")); err == nil {
		if name := strings.TrimSpace(string(data)); name != "" {
			return name
		}
	}
	return teamPeerHostname(team) + "." + teamNetbirdDomain()
}

// teamPeerHostname is the name the team's peer joins with: the team's,
// since hi 2.5.1, or <team>-dev for a peer that joined before.
func teamPeerHostname(team string) string {
	if data, err := os.ReadFile(filepath.Join(teamDevDir(team), "netbird-host")); err == nil {
		if name := strings.TrimSpace(string(data)); name != "" {
			return name
		}
	}
	if teamPeerJoined(team) {
		return teamDevHost(team)
	}
	return team
}

// teamRefreshPeerName keeps the peer's name as NetBird has it, once it is
// on the network: an admin may rename it in NetBird's dashboard.
func teamRefreshPeerName(engine boxEngine, team string, wait time.Duration) {
	deadline := time.Now().Add(wait)
	for {
		out, err := engine.output("exec", teamDevNetContainer(team), "netbird", "status", "--json")
		var status struct {
			FQDN string `json:"fqdn"`
		}
		if err == nil && json.Unmarshal([]byte(out), &status) == nil && strings.Contains(status.FQDN, ".") {
			path := filepath.Join(teamDevDir(team), "netbird-fqdn")
			if current, _ := os.ReadFile(path); strings.TrimSpace(string(current)) != status.FQDN {
				writeFileAtomic(path, []byte(status.FQDN+"\n"), 0o600)
			}
			return
		}
		if time.Now().After(deadline) {
			return
		}
		time.Sleep(5 * time.Second)
	}
}

// teamInternalExpose marks a slot's address as an internal one in its
// Expose, where a public one has its exposure's name.
const teamInternalExpose = "internal:"

func teamIsInternal(expose string) bool { return strings.HasPrefix(expose, teamInternalExpose) }

// teamExposeKindOutdated says an address is internal where it should be
// public, or public where it should be internal.
func teamExposeKindOutdated(config teamConfig, address, expose string) bool {
	if expose == "" {
		return false
	}
	return teamIsInternal(expose) == teamPublicOn(config, address)
}

// teamInternalMoved says an internal address isn't where it is now: the
// peer was renamed, or a hi from before had it elsewhere.
func teamInternalMoved(team, address, expose, link string) bool {
	return teamIsInternal(expose) && link != teamInternalURL(team, address)
}

// ---------------------------------------------------------------------------
// the internal relays

// teamInternalDir holds the internal relays' sockets, which the team's
// peers mount; nothing else is in it.
func teamInternalDir(team string) string { return teamBrokerDir(team) + "-internal" }

func teamInternalSocket(team, address string) string {
	return filepath.Join(teamInternalDir(team), address+".sock")
}

// teamInternalHelper is what an internal relay adds to what it passes on:
// the helper and the docs for a slot, the dev page's routes for the dev
// page.
func teamInternalHelper(team, address string) *netExposeHelper {
	if address == teamAddressDev {
		return &netExposeHelper{Routes: []netExposeRoute{{Prefix: teamHelperPrefix, Socket: teamDevPageSocket(team), Always: true}}, Plain: true}
	}
	return &netExposeHelper{
		Routes: []netExposeRoute{
			{Prefix: teamHelperPrefix, Socket: teamWebSocket(team, address)},
			{Prefix: teamDocsPrefix, Socket: teamWebSocket(team, address), Always: true},
			{Prefix: teamLLMsPath, Socket: teamWebSocket(team, address), Always: true},
			{Prefix: teamLLMsFullPath, Socket: teamWebSocket(team, address), Always: true},
		},
		Inject: teamHelperPrefix + "helper.js", InjectIf: teamHelperFile(team, address), Plain: true,
	}
}

// teamInternalTarget is where an internal relay passes requests: the
// slot's app as it moves between boxes, or the dev page's own listener.
func teamInternalTarget(team, address string) func() string {
	if address == teamAddressDev {
		return netExposeFollow(teamDevPagePortFile(team), "127.0.0.1", 0)
	}
	return netExposeFollow(teamSlotPortFile(team, address), "127.0.0.1", 0)
}

// listenInternal opens the internal relays' sockets.
func (b *teamBroker) listenInternal(ctx context.Context, log io.Writer) ([]func() error, error) {
	if err := os.MkdirAll(teamInternalDir(b.team), 0o700); err != nil {
		return nil, err
	}
	var serves []func() error
	relays := map[string]http.Handler{}
	for _, address := range []string{teamSlotProduction, teamSlotPreview, teamAddressDev} {
		relays[address] = newNetExposeRelayTo(teamInternalTarget(b.team, address), "", time.Time{}, io.Discard, teamInternalHelper(b.team, address), nil)
	}
	config, _ := loadTeam(b.team)
	for _, address := range []string{teamSlotProduction, teamSlotPreview, teamAddressDev} {
		handler := relays[address]
		if address == teamSlotProduction {
			// Port 80: with a name for each address, each name to its
			// address, for names that are A records to the peer.
			handler = teamNamesHandler(config, relays)
		}
		serve, err := b.listenWith(ctx, teamInternalSocket(b.team, address), handler)
		if err != nil {
			return nil, err
		}
		serves = append(serves, serve)
	}
	// The addresses over https, which the peer's 443 reaches when the
	// machine has a domain, each at its own name (team_https.go).
	socket := teamInternalSocket(b.team, teamHTTPSAddress)
	os.Remove(socket)
	listener, err := net.Listen("unix", socket)
	if err != nil {
		return nil, err
	}
	os.Chmod(socket, 0o600)
	serves = append(serves, func() error {
		return b.serve(ctx, serveHTTPS(listener, &b.httpsCerts), teamHTTPSHandler(b.team, relays))
	})
	return serves, nil
}

// teamNamesHandler sends a request to the address its Host names, when
// the team has a name for each, and to production otherwise: what port 80
// gets without names, and a name it doesn't know.
func teamNamesHandler(config teamConfig, relays map[string]http.Handler) http.Handler {
	if config.Addresses != teamAddressesNames || config.Names == nil {
		return relays[teamSlotProduction]
	}
	names := *config.Names
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		for _, address := range []string{teamSlotPreview, teamAddressDev} {
			if strings.EqualFold(strings.TrimSuffix(host, "."), names.name(address)) {
				relays[address].ServeHTTP(w, r)
				return
			}
		}
		relays[teamSlotProduction].ServeHTTP(w, r)
	})
}

// ---------------------------------------------------------------------------
// the team's peer

// teamPeerJoined says the team's peer is on the network.
func teamPeerJoined(team string) bool {
	return fileExists(filepath.Join(teamDevDir(team), "netbird", "default.json"))
}

// teamPeerScript is what the team's peer's container runs: hi passing its
// ports on (production, the preview, and the dev page to their internal
// relays, SSH to the dev box), and NetBird's userspace stack, which hands
// what reaches the peer's ports to the container's loopback.
//
// The team's service starts the container, and its output went to the
// service: when the service restarted, NetBird's next log line had nowhere
// to go and stopped it. So its output stays in the container, and the
// container ends when NetBird does, for hi to start it again.
func teamPeerScript(team, sshTarget string, joined bool) string {
	up := "netbird up --hostname " + teamPeerHostname(team) + " --disable-dns"
	if !joined {
		up += " --setup-key-file /run/netbird-key"
	}
	var forwards string
	for _, address := range []string{teamSlotProduction, teamSlotPreview, teamAddressDev} {
		forwards += fmt.Sprintf("hi box __forward 127.0.0.1:%d unix:/hi-internal/%s.sock & ", teamInternalPort(address), address)
	}
	if teamHTTPSDomain(team) != "" {
		forwards += fmt.Sprintf("hi box __forward 127.0.0.1:%d unix:/hi-internal/%s.sock & ", teamHTTPSPort, teamHTTPSAddress)
	}
	forwards += "hi box __forward 127.0.0.1:22 " + sshTarget + " & "
	return "exec >>/tmp/peer.log 2>&1; " + forwards + "netbird service run --log-file /tmp/netbird.log & nb=$!; sleep 3; " + up + "; wait $nb"
}

// keepTeamPeer starts the team's peer again when it stopped while the dev
// box runs.
func keepTeamPeer(done <-chan struct{}, team, hi string, stderr io.Writer) {
	for {
		select {
		case <-done:
			return
		case <-time.After(time.Minute):
		}
		engine, _, err := detectBoxEngine()
		if err != nil || engine.state(teamDevContainer(team)) != "running" || engine.state(teamDevNetContainer(team)) == "running" {
			continue
		}
		if err := startTeamDevNet(engine, team, hi, stderr); err != nil {
			fmt.Fprintf(stderr, "hi: the team's NetBird peer: %v\n", err)
		}
	}
}

// teamPeerArgs are the team's peer's container's: hardened, its identity,
// NetBird and hi from the machine, and the internal relays' sockets.
func teamPeerArgs(engine boxEngine, team, netbird, hi, script string, joined bool) []string {
	args := []string{"create", "--init", "--name", teamDevNetContainer(team), "--hostname", teamPeerHostname(team), "--network", engine.defaultNetwork(),
		"--label", "hi.peer=" + teamPeerStamp(script),
		"--cap-drop=ALL", "--cap-add=NET_BIND_SERVICE", "--security-opt", "no-new-privileges", "--memory", "256m",
		"-e", "NB_USE_NETSTACK_MODE=true", "-e", "NB_ENABLE_NETSTACK_LOCAL_FORWARDING=true", "-e", "NB_LOG_MAX_SIZE_MB=5",
		"-v", filepath.Join(teamDevDir(team), "netbird") + ":/var/lib/netbird", "-v", netbird + ":/usr/local/bin/netbird:ro", "-v", hi + ":/usr/local/bin/hi:ro",
		"-v", teamInternalDir(team) + ":/hi-internal"}
	if !joined {
		args = append(args, "-v", filepath.Join(teamDevDir(team), teamDevSetupKey)+":/run/netbird-key:ro")
	}
	return append(args, boxBaseImage(), "sh", "-c", script)
}

// teamPeerStamp names what the peer's container runs, so a new hi that
// runs it differently makes it again.
func teamPeerStamp(script string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(script)))[:12]
}

// stopTeamPeers removes the production and preview peers v2.5.0 made;
// since v2.5.1 the team's one peer has every address.
func stopTeamPeers(engine boxEngine, team string) {
	for _, address := range []string{teamSlotProduction, teamSlotPreview} {
		engine.output("rm", "-f", "hi-box-team-"+team+"-peer-"+address)
	}
}

// teamContainerLabel is one of a container's labels.
func teamContainerLabel(engine boxEngine, container, label string) string {
	out, err := engine.output("inspect", "-f", "{{index .Config.Labels \""+label+"\"}}", container)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// teamDialTarget dials a forward's target: host:port, or unix:<path>.
func teamDialTarget(to string) (net.Conn, error) {
	if path, ok := strings.CutPrefix(to, "unix:"); ok {
		return net.DialTimeout("unix", path, 10*time.Second)
	}
	return net.DialTimeout("tcp", to, 10*time.Second)
}
