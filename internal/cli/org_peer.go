package cli

// The server's NetBird peer (docs/specs/approved/hi_server_peer.md): a
// small rootless container, made as the admin page's is (admin_reach.go),
// joined with a setup key of its own as <machine>-server. The server keeps
// running on the machine; the peer passes its port 7373 to the server's
// client-<id>.sock, 7374 to instance.sock (cloud runs), and NetBird's
// SOCKS5 proxy to out-<id>.sock, the server's way into its network. So
// the server moves to another network without the machine: a new peer
// joins, the old one answers beside it, and devices follow, told with a
// notice signed with the server key.

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/proxy"
)

const (
	serverPeerPrefix    = "hi-server-peer-"
	serverPeerSOCKSPort = 1080
	// serverLocalListen is where the machine's own hi and apps reach the
	// server, whatever network its peer is on.
	serverLocalListen = "127.0.0.1:7373"
	// An old peer answers for 14 days at most, and goes sooner once no
	// device has called it for a day.
	serverOldPeerFor   = 14 * 24 * time.Hour
	serverOldQuietFor  = 24 * time.Hour
	serverPeerOffAfter = 2 * time.Minute

	// A move's notice, on every answer through an old address, and the
	// proof a new address gives that it is the same server.
	serverMovedHeader     = "Hi-Server-Moved"
	serverMovedSigHeader  = "Hi-Server-Moved-Signature"
	serverChallengeHeader = "Hi-Server-Challenge"
	serverProofHeader     = "Hi-Server-Proof"
)

// These are replaced in tests.
var (
	serverPeerJoinTimeout = 90 * time.Second
	serverPeerPoll        = 2 * time.Second
	// serverPeerStatusOf is what a peer's NetBird says of itself.
	serverPeerStatusOf = func(id string) (serverPeerStatus, error) {
		engine, _, err := detectBoxEngine()
		if err != nil {
			return serverPeerStatus{}, err
		}
		out, err := engine.quietExec(serverPeerContainer(id), "netbird", "status", "--json")
		if err != nil {
			return serverPeerStatus{}, err
		}
		return parseServerPeerStatus([]byte(out))
	}
	// serverPeerHi is the hi binary the peer's forwards run.
	serverPeerHi = func() (string, error) {
		executable, err := os.Executable()
		if err != nil {
			return "", err
		}
		if resolved, err := filepath.EvalSymlinks(executable); err == nil {
			executable = resolved
		}
		return executable, nil
	}
)

// serverPeerConfig is a peer in config.json.
type serverPeerConfig struct {
	ID            string `json:"id"`   // its folder and container
	Name          string `json:"name"` // the hostname it joins with
	ManagementURL string `json:"management_url,omitempty"`
	// Since is when it became the server's peer; Until, an old peer's, is
	// when it goes whatever happens.
	Since time.Time `json:"since"`
	Until time.Time `json:"until,omitzero"`
}

func serverNetDir(dir string) string      { return filepath.Join(dir, "net") }
func serverSockDir(dir string) string     { return filepath.Join(dir, "net", "sock") }
func serverPeerDir(dir, id string) string { return filepath.Join(dir, "net", "peer-"+id) }
func serverPeerIdentity(dir, id string) string {
	return filepath.Join(serverPeerDir(dir, id), "netbird")
}
func serverPeerKeyPath(dir, id string) string {
	return filepath.Join(serverPeerDir(dir, id), "setup-key")
}
func serverPeerContainer(id string) string { return serverPeerPrefix + id }
func serverClientSocket(dir, id string) string {
	return filepath.Join(serverSockDir(dir), "client-"+id+".sock")
}
func serverOutSocket(dir, id string) string {
	return filepath.Join(serverSockDir(dir), "out-"+id+".sock")
}
func serverInstanceSocket(dir string) string {
	return filepath.Join(serverSockDir(dir), "instance.sock")
}

// serverPeerJoined says the peer has an identity on its network.
func serverPeerJoined(dir, id string) bool {
	return fileExists(filepath.Join(serverPeerIdentity(dir, id), "default.json"))
}

var serverPeerNamePattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// serverPeerDefaultName is <machine>-server.
func serverPeerDefaultName() string {
	machine := strings.Trim(adminHostPart.ReplaceAllString(strings.ToLower(adminMachineName()), "-"), "-")
	if machine == "" {
		machine = "hi"
	}
	if len(machine) > 56 {
		machine = strings.Trim(machine[:56], "-")
	}
	return machine + "-server"
}

// validManagementURL is an http(s) URL with nothing a shell would read.
func validManagementURL(text string) bool {
	parsed, err := url.Parse(text)
	return err == nil && (parsed.Scheme == "https" || parsed.Scheme == "http") && parsed.Host != "" &&
		!strings.ContainsAny(text, " \t\n'\"\\`$;&|<>(){}*?!#~")
}

func newServerPeerID() string {
	buffer := make([]byte, 3)
	rand.Read(buffer)
	return hex.EncodeToString(buffer)
}

// ---------------------------------------------------------------------------
// config.json

func loadServerConfig(dir string) (serverConfig, error) {
	var config serverConfig
	data, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		return config, fmt.Errorf("no server in %s; run `hi server init` first", dir)
	}
	if err := json.Unmarshal(data, &config); err != nil {
		return config, fmt.Errorf("read %s: %w", filepath.Join(dir, "config.json"), err)
	}
	return config, nil
}

// updateServerConfig changes config.json's address and peers, and keeps
// whatever else an admin wrote in it.
func updateServerConfig(dir string, change func(*serverConfig) error) (serverConfig, error) {
	path := filepath.Join(dir, "config.json")
	config, err := loadServerConfig(dir)
	if err != nil {
		return config, err
	}
	raw := map[string]json.RawMessage{}
	if data, err := os.ReadFile(path); err == nil {
		json.Unmarshal(data, &raw)
	}
	if err := change(&config); err != nil {
		return config, err
	}
	encoded, err := json.Marshal(config)
	if err != nil {
		return config, err
	}
	fields := map[string]json.RawMessage{}
	json.Unmarshal(encoded, &fields)
	for _, key := range []string{"listen", "peer", "old_peer", "local_listen"} {
		if value, ok := fields[key]; ok {
			raw[key] = value
		} else {
			delete(raw, key)
		}
	}
	data, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return config, err
	}
	return config, writeFileAtomic(path, append(data, '\n'), 0o600)
}

// ---------------------------------------------------------------------------
// the peer's container

// serverPeerScript is what a peer's container runs: hi passing ports 7373
// and 7374 to the server's sockets and a socket to NetBird's SOCKS5 proxy,
// and NetBird's userspace stack, which hands what reaches the peer to the
// container's loopback. Its output stays in the container.
func serverPeerScript(peer serverPeerConfig, joined bool) string {
	up := "netbird up --hostname " + peer.Name + " --disable-dns"
	if peer.ManagementURL != "" {
		up += " --management-url " + peer.ManagementURL
	}
	if !joined {
		up += " --setup-key-file /run/netbird-key"
	}
	forwards := fmt.Sprintf("hi box __forward 127.0.0.1:%d unix:/hi-server/client-%s.sock & ", serverDefaultPort, peer.ID) +
		fmt.Sprintf("hi box __forward 127.0.0.1:%d unix:/hi-server/instance.sock & ", exposeDefaultPort) +
		fmt.Sprintf("hi box __forward unix:/hi-server/out-%s.sock 127.0.0.1:%d & ", peer.ID, serverPeerSOCKSPort)
	return "exec >>/tmp/peer.log 2>&1; " + forwards + "netbird service run --log-file /tmp/netbird.log & nb=$!; sleep 3; " + up + "; wait $nb"
}

// serverPeerEngine is the container engine a peer runs in, with NetBird
// on the machine to mount into it.
func serverPeerEngine() (boxEngine, error) {
	engine, _, err := detectBoxEngine()
	if err != nil {
		return engine, errors.New("the server's NetBird peer runs in a container, and this machine has no Podman: hi install podman")
	}
	if _, err := boxHostBinary("netbird"); err != nil {
		return engine, errors.New("the server's NetBird peer runs NetBird, which isn't installed here: hi install netbird")
	}
	return engine, nil
}

// serverKeepPeer has a peer running as its script says now: made the
// first time with its setup key, started again when it stopped, and made
// again when hi runs it differently.
func serverKeepPeer(dir string, peer serverPeerConfig, progress io.Writer) error {
	joined := serverPeerJoined(dir, peer.ID)
	if joined {
		os.Remove(serverPeerKeyPath(dir, peer.ID)) // used: the peer exists
	} else if !fileExists(serverPeerKeyPath(dir, peer.ID)) {
		return fmt.Errorf("the server's peer %s has neither joined nor a setup key; give it one: hi server net join", peer.Name)
	}
	engine, err := serverPeerEngine()
	if err != nil {
		return err
	}
	netbird, _ := boxHostBinary("netbird")
	hi, err := serverPeerHi()
	if err != nil {
		return err
	}
	name := serverPeerContainer(peer.ID)
	script := serverPeerScript(peer, joined)
	if engine.state(name) == "running" && appContainerLabel(engine, name, "hi.peer") == appPeerStamp(script) {
		return nil
	}
	image, err := engine.ensureBaseImage(progress, progress)
	if err != nil {
		return fmt.Errorf("hi's box image, which the server's peer runs: %w", err)
	}
	for _, folder := range []string{serverNetDir(dir), serverPeerDir(dir, peer.ID), serverPeerIdentity(dir, peer.ID), serverSockDir(dir)} {
		if err := os.MkdirAll(folder, 0o700); err != nil {
			return err
		}
	}
	engine.output("rm", "-f", name)
	args := []string{"create", "--init", "--name", name, "--hostname", peer.Name, "--network", engine.defaultNetwork(),
		"--label", "hi.peer=" + appPeerStamp(script), "--label", "hi.server=" + dir,
		"--cap-drop=ALL", "--cap-add=NET_BIND_SERVICE", "--security-opt", "no-new-privileges", "--memory", "256m",
		"-e", "NB_USE_NETSTACK_MODE=true", "-e", "NB_ENABLE_NETSTACK_LOCAL_FORWARDING=true", "-e", "NB_LOG_MAX_SIZE_MB=5",
		"-e", fmt.Sprintf("NB_SOCKS5_LISTENER_PORT=%d", serverPeerSOCKSPort),
		"-v", serverPeerIdentity(dir, peer.ID) + ":/var/lib/netbird", "-v", netbird + ":/usr/local/bin/netbird:ro", "-v", hi + ":/usr/local/bin/hi:ro",
		"-v", serverSockDir(dir) + ":/hi-server"}
	if !joined {
		args = append(args, "-v", serverPeerKeyPath(dir, peer.ID)+":/run/netbird-key:ro")
	}
	args = append(args, image, "sh", "-c", script)
	if out, err := engine.output(args...); err != nil {
		return fmt.Errorf("the server's NetBird peer: %s", qFirstLine(out, err.Error()))
	}
	if out, err := engine.output("start", name); err != nil {
		return fmt.Errorf("the server's NetBird peer: %s", qFirstLine(out, err.Error()))
	}
	return nil
}

// serverRemovePeer removes a peer's container and its identity.
func serverRemovePeer(dir, id string) {
	if engine, _, err := detectBoxEngine(); err == nil {
		engine.output("rm", "-f", serverPeerContainer(id))
	}
	os.RemoveAll(serverPeerDir(dir, id))
	os.Remove(serverClientSocket(dir, id))
	os.Remove(serverOutSocket(dir, id))
}

// serverPeerLog is the end of what a peer's container said, for when it
// doesn't join.
func serverPeerLog(id string) string {
	engine, _, err := detectBoxEngine()
	if err != nil {
		return ""
	}
	out, err := engine.output("exec", serverPeerContainer(id), "sh", "-c", "tail -n 4 /tmp/peer.log 2>/dev/null")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// ---------------------------------------------------------------------------
// the peer's state

// serverPeerStatus is what a peer's NetBird says of itself.
type serverPeerStatus struct {
	FQDN       string `json:"fqdn,omitempty"`
	Address    string `json:"address,omitempty"`
	Management string `json:"management,omitempty"` // its management server's URL
	Connected  bool   `json:"connected"`
	Problem    string `json:"problem,omitempty"`
	prefix     netip.Prefix
	// names are the peers it sees, by name, for the way out: NetBird's
	// SOCKS5 proxy doesn't resolve them with the peer's DNS off.
	names map[string]netip.Addr
}

func (p serverPeerStatus) domain() string {
	_, domain, _ := strings.Cut(p.FQDN, ".")
	return domain
}

func parseServerPeerStatus(data []byte) (serverPeerStatus, error) {
	var raw struct {
		FQDN       string `json:"fqdn"`
		IP         string `json:"netbirdIp"`
		Management struct {
			URL       string `json:"url"`
			Connected bool   `json:"connected"`
			Error     string `json:"error"`
		} `json:"management"`
		Peers struct {
			Details []struct {
				FQDN string `json:"fqdn"`
				IP   string `json:"netbirdIp"`
			} `json:"details"`
		} `json:"peers"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return serverPeerStatus{}, fmt.Errorf("NetBird's status: %w", err)
	}
	status := serverPeerStatus{FQDN: strings.TrimSuffix(raw.FQDN, "."), Management: raw.Management.URL,
		Connected: raw.Management.Connected, Problem: raw.Management.Error, names: map[string]netip.Addr{}}
	if prefix, err := netip.ParsePrefix(raw.IP); err == nil {
		status.Address, status.prefix = prefix.Addr().String(), prefix.Masked()
	}
	for _, peer := range raw.Peers.Details {
		name := strings.ToLower(strings.TrimSuffix(peer.FQDN, "."))
		address, err := netip.ParseAddr(strings.Split(peer.IP, "/")[0])
		if name != "" && err == nil {
			status.names[name] = address
		}
	}
	return status, nil
}

// serverWaitPeer waits for a peer to be on its network.
func serverWaitPeer(id string, timeout time.Duration) (serverPeerStatus, error) {
	deadline := time.Now().Add(timeout)
	var last serverPeerStatus
	var lastErr error
	for {
		status, err := serverPeerStatusOf(id)
		if err == nil && status.Connected && status.FQDN != "" && status.Address != "" {
			return status, nil
		}
		if err == nil {
			last = status
		}
		lastErr = err
		if time.Now().After(deadline) {
			switch {
			case last.Problem != "":
				return last, fmt.Errorf("it didn't join the network; NetBird says: %s", last.Problem)
			case lastErr != nil:
				return last, fmt.Errorf("it didn't join the network: %v", lastErr)
			}
			return last, fmt.Errorf("it didn't join the network in %s", timeout)
		}
		time.Sleep(serverPeerPoll)
	}
}

// serverJoinAddress is what devices join with: the peer's name.
func serverJoinAddress(status serverPeerStatus) string {
	if status.FQDN != "" {
		return status.FQDN
	}
	return status.Address
}

// serverPeerURLOf is the server's address at its peer, which devices are
// told of; tests replace it.
var serverPeerURLOf = serverPeerURL

func serverPeerURL(status serverPeerStatus) string {
	if address := serverJoinAddress(status); address != "" {
		return "http://" + net.JoinHostPort(address, fmt.Sprint(serverDefaultPort))
	}
	return ""
}

// ---------------------------------------------------------------------------
// joining, and moving to another network

// serverNetJoinOptions is hi server net join's.
type serverNetJoinOptions struct {
	Key           []byte
	ManagementURL string
	Name          string
}

// serverNetJoin gives the server a NetBird peer of its own, or moves it to
// another network: a new peer joins, and becomes the server's once it has;
// the one before answers beside it until devices have followed. Joining
// fails? Nothing else has changed.
func serverNetJoin(dir string, options serverNetJoinOptions, as string, progress io.Writer) (serverPeerStatus, error) {
	config, err := loadServerConfig(dir)
	if err != nil {
		return serverPeerStatus{}, err
	}
	if config.OldPeer != nil {
		return serverPeerStatus{}, fmt.Errorf("the server is still moving from %s: once its devices have followed, or now with hi server net forget-old, it can move again", config.OldPeer.Name)
	}
	name := strings.ToLower(strings.TrimSpace(options.Name))
	if name == "" && config.Peer != nil {
		name = config.Peer.Name
	}
	if name == "" {
		name = serverPeerDefaultName()
	}
	if !serverPeerNamePattern.MatchString(name) {
		return serverPeerStatus{}, usageError{fmt.Sprintf("the peer's name %q: lowercase letters, digits, and dashes, up to 63", name)}
	}
	management := strings.TrimRight(strings.TrimSpace(options.ManagementURL), "/")
	if management != "" && !validManagementURL(management) {
		return serverPeerStatus{}, usageError{fmt.Sprintf("--management-url %q: the network's management server, such as https://netbird.example.com", management)}
	}
	key := bytes.TrimSpace(options.Key)
	if len(key) == 0 {
		return serverPeerStatus{}, errors.New("a NetBird setup key, from the network's dashboard (Setup Keys)")
	}
	peer := serverPeerConfig{ID: newServerPeerID(), Name: name, ManagementURL: management}
	if err := os.MkdirAll(serverPeerDir(dir, peer.ID), 0o700); err != nil {
		return serverPeerStatus{}, err
	}
	if err := writeFileAtomic(serverPeerKeyPath(dir, peer.ID), append(key, '\n'), 0o600); err != nil {
		os.RemoveAll(serverPeerDir(dir, peer.ID))
		return serverPeerStatus{}, err
	}
	fail := func(err error) (serverPeerStatus, error) {
		if log := serverPeerLog(peer.ID); log != "" {
			err = fmt.Errorf("%w\n%s", err, log)
		}
		serverRemovePeer(dir, peer.ID)
		return serverPeerStatus{}, err
	}
	where := "its network"
	if management != "" {
		where = "the network at " + serverNetworkName(management)
	}
	fmt.Fprintf(progress, "The server's peer %s is joining %s…\n", name, where)
	if err := serverKeepPeer(dir, peer, progress); err != nil {
		return fail(err)
	}
	status, err := serverWaitPeer(peer.ID, serverPeerJoinTimeout)
	if err != nil {
		return fail(err)
	}
	now := time.Now().UTC()
	moving := config.Peer != nil
	config, err = updateServerConfig(dir, func(c *serverConfig) error {
		peer.Since = now
		if c.Peer != nil {
			old := *c.Peer
			old.Until = now.Add(serverOldPeerFor)
			c.OldPeer = &old
		}
		c.Peer = &peer
		return nil
	})
	if err != nil {
		return fail(err)
	}
	// Made again without its setup key, which the identity replaces.
	if err := serverKeepPeer(dir, peer, io.Discard); err != nil {
		fmt.Fprintf(progress, "hi: the peer: %v\n", err)
	}
	what := "gave the server a NetBird peer"
	if moving {
		what = "moved the server to another NetBird network"
	}
	serverAuditOffline(dir, as, what, status.FQDN, where)
	serverReloadNet(dir, as)
	serverLocalFollow(dir, config)
	return status, nil
}

// serverForgetOld stops the server answering at its old addresses: the
// peer from before a move, and the machine's NetBird address it listened
// on before it had a peer.
func serverForgetOld(dir string) (serverConfig, []string, error) {
	var gone []string
	var oldID string
	config, err := updateServerConfig(dir, func(c *serverConfig) error {
		if c.Peer == nil {
			return errors.New("the server has no NetBird peer, so its machine's address is the only one it has")
		}
		if c.OldPeer != nil {
			oldID = c.OldPeer.ID
			gone = append(gone, "the peer "+c.OldPeer.Name)
			c.OldPeer = nil
		}
		if c.Listen != "" {
			gone = append(gone, c.Listen)
			c.Listen = ""
		}
		return nil
	})
	if err != nil {
		return config, nil, err
	}
	if oldID != "" {
		serverRemovePeer(dir, oldID)
	}
	return config, gone, nil
}

// serverReloadNet tells the running server its peers changed.
func serverReloadNet(dir, as string) {
	if !fileExists(filepath.Join(dir, "admin.sock")) {
		return
	}
	adminCall(dir, http.MethodPost, "/admin/net", map[string]string{"action": "reload", "as": as}, nil)
}

// serverAuditOffline records a change in the server's audit when the
// server isn't there to; the running one records it itself on reload.
func serverAuditOffline(dir, as, action, target, detail string) {
	if fileExists(filepath.Join(dir, "admin.sock")) {
		if err := adminCall(dir, http.MethodPost, "/admin/net", map[string]string{"action": "audit", "as": as, "what": action, "target": target, "detail": detail}, nil); err == nil {
			return
		}
	}
	if server, err := openServer(dir, io.Discard); err == nil {
		server.audit(as, action, target, detail)
	}
}

// serverLocalFollow points the machine's own hi at the server on
// 127.0.0.1 once the server has a peer: it was joined at the machine's
// NetBird address, which may stop being the server's.
func serverLocalFollow(dir string, config serverConfig) {
	if dir != orgDefaultDirectory() || config.Listen == "" {
		return
	}
	connection, err := loadServerConnection()
	if err != nil || connection == nil {
		return
	}
	if old, err := normalizeServerURL(config.Listen); err != nil || connection.URL != old {
		return
	}
	local := "http://" + serverLocalListen
	if response, err := (&http.Client{Timeout: 3 * time.Second}).Get(local + "/v1/me"); err == nil {
		response.Body.Close()
		connection.URL = local
		saveServerConnection(*connection)
	}
}

// orgDefaultDirectory is the machine's server folder, or "".
func orgDefaultDirectory() string {
	dir, err := orgDirectory("")
	if err != nil {
		return ""
	}
	return dir
}

// ---------------------------------------------------------------------------
// the running server's side

type serverViaKey struct{}

// serverVia is which of the server's addresses a call came in at:
// "peer:<id>", "listen" (the machine's NetBird address), or "local".
func serverVia(ctx context.Context) string {
	via, _ := ctx.Value(serverViaKey{}).(string)
	return via
}

// serverNet is the running server's peers and addresses.
type serverNet struct {
	mu     sync.Mutex
	dir    string
	peer   *serverPeerConfig
	old    *serverPeerConfig
	listen string // the machine's NetBird address, from before its peer
	local  string
	status serverPeerStatus // the current peer's, read every half minute
	read   time.Time
	// problem is why the peer isn't running; offSince, since when it
	// hasn't been on its network.
	problem  string
	offSince time.Time
	offSaid  bool // the incident is open: the audit and Slack said so
	// lastOld is the last call at an old address; oldDevices, each
	// device's last there.
	lastOld    time.Time
	oldDevices map[string]serverOldDevice
	servers    map[string]*http.Server // by via
	keeping    sync.Mutex
	direct     net.Dialer
	locals     map[netip.Addr]bool
}

type serverOldDevice struct {
	User     string    `json:"user"`
	Hostname string    `json:"hostname"`
	Via      string    `json:"via"`
	Last     time.Time `json:"last"`
}

// load takes the peers and addresses config.json has.
func (n *serverNet) load(dir string, config serverConfig) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.dir, n.peer, n.old, n.listen = dir, config.Peer, config.OldPeer, config.Listen
	n.local = serverLocalListen
	if config.LocalListen != "" {
		n.local = config.LocalListen
	}
	if n.local == "off" || n.local == config.Listen {
		n.local = ""
	}
	if n.oldDevices == nil {
		n.oldDevices = map[string]serverOldDevice{}
	}
	if n.lastOld.IsZero() {
		n.lastOld = time.Now()
	}
	n.direct = net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
	if n.locals == nil {
		n.locals = map[netip.Addr]bool{}
		if addresses, err := net.InterfaceAddrs(); err == nil {
			for _, address := range addresses {
				if prefix, err := netip.ParsePrefix(address.String()); err == nil {
					n.locals[prefix.Addr()] = true
				}
			}
		}
	}
}

func (n *serverNet) current() (*serverPeerConfig, serverPeerStatus) {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.peer, n.status
}

// isOld says a call came in at an address the server is moving away from.
func (n *serverNet) isOld(via string) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	switch {
	case n.peer == nil:
		return false
	case via == "listen":
		return true
	case n.old != nil && via == "peer:"+n.old.ID:
		return true
	}
	return false
}

// noteDevice records a device's call at an old address.
func (n *serverNet) noteDevice(via string, device *serverDevice) {
	if !n.isOld(via) {
		return
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	n.lastOld = time.Now()
	n.oldDevices[device.Fingerprint] = serverOldDevice{User: device.User, Hostname: device.Hostname, Via: via, Last: n.lastOld}
}

// route says whether address is on the peer's network, and the address
// to ask its SOCKS5 proxy for: a NetBird address, or a name it resolves
// from the peers it sees. The machine's own addresses go direct.
func (n *serverNet) route(address string) (socket, target string, err error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return "", "", nil
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.peer == nil || !n.status.prefix.IsValid() {
		return "", "", nil
	}
	socket = serverOutSocket(n.dir, n.peer.ID)
	if ip, err := netip.ParseAddr(host); err == nil {
		if n.status.prefix.Contains(ip) && !n.locals[ip] && !ip.IsLoopback() {
			return socket, address, nil
		}
		return "", "", nil
	}
	name := strings.ToLower(strings.TrimSuffix(host, "."))
	domain := n.status.domain()
	if domain == "" || !strings.HasSuffix(name, "."+domain) {
		return "", "", nil
	}
	ip, ok := n.status.names[name]
	if !ok {
		return "", "", fmt.Errorf("no peer named %s on the server's network", name)
	}
	return socket, net.JoinHostPort(ip.String(), port), nil
}

// dialContext is the server's way out: through its peer to what's on its
// network, direct to everything else.
func (n *serverNet) dialContext(ctx context.Context, network, address string) (net.Conn, error) {
	socket, target, err := n.route(address)
	if err != nil {
		return nil, err
	}
	if socket == "" {
		return n.direct.DialContext(ctx, network, address)
	}
	dialer, err := proxy.SOCKS5("unix", socket, nil, &net.Dialer{Timeout: 10 * time.Second})
	if err != nil {
		return nil, err
	}
	conn, err := dialer.(proxy.ContextDialer).DialContext(ctx, "tcp", target)
	if err != nil {
		return nil, fmt.Errorf("through the server's NetBird peer: %w", err)
	}
	return conn, nil
}

// movedNotice is the header pair that tells a device the server moved:
// its new address, signed with the server key.
func serverMovedNotice(key ed25519.PrivateKey, newURL string) (string, string) {
	signature := ed25519.Sign(key, []byte("hi-server-moved\n"+newURL))
	return newURL, base64.StdEncoding.EncodeToString(signature)
}

func serverProof(key ed25519.PrivateKey, challenge string) string {
	return base64.StdEncoding.EncodeToString(ed25519.Sign(key, []byte("hi-server-proof\n"+challenge)))
}

// viaHandler marks each call with the address it came in at, proves the
// server's key to a device that asks, and tells devices at an old address
// where the server is now.
func (s *orgServer) viaHandler(via string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if challenge := r.Header.Get(serverChallengeHeader); challenge != "" && len(challenge) <= 128 {
			w.Header().Set(serverProofHeader, serverProof(s.serverKey, challenge))
		}
		if s.net.isOld(via) {
			if _, status := s.net.current(); serverPeerURLOf(status) != "" {
				moved, signature := serverMovedNotice(s.serverKey, serverPeerURLOf(status))
				w.Header().Set(serverMovedHeader, moved)
				w.Header().Set(serverMovedSigHeader, signature)
			}
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), serverViaKey{}, via)))
	})
}

// openClient serves the client API on one more address.
func (s *orgServer) openClient(via string, listener net.Listener, handler http.Handler) {
	server := &http.Server{Handler: s.viaHandler(via, handler), ReadHeaderTimeout: 10 * time.Second}
	s.net.mu.Lock()
	if s.net.servers == nil {
		s.net.servers = map[string]*http.Server{}
	}
	if previous := s.net.servers[via]; previous != nil {
		previous.Close()
	}
	s.net.servers[via] = server
	s.net.mu.Unlock()
	go server.Serve(listener)
}

func (s *orgServer) closeClient(via string) {
	s.net.mu.Lock()
	server := s.net.servers[via]
	delete(s.net.servers, via)
	s.net.mu.Unlock()
	if server != nil {
		server.Close()
	}
}

func (s *orgServer) shutdownClients(ctx context.Context) {
	s.net.mu.Lock()
	servers := make([]*http.Server, 0, len(s.net.servers))
	for _, server := range s.net.servers {
		servers = append(servers, server)
	}
	s.net.servers = nil
	s.net.mu.Unlock()
	for _, server := range servers {
		server.Shutdown(ctx)
	}
}

// listenPeers serves the client API on each peer's socket, and stops on
// those of peers that are gone.
func (s *orgServer) listenPeers(handler http.Handler) {
	s.net.mu.Lock()
	want := map[string]string{}
	for _, peer := range []*serverPeerConfig{s.net.peer, s.net.old} {
		if peer != nil {
			want["peer:"+peer.ID] = serverClientSocket(s.dir, peer.ID)
		}
	}
	var gone []string
	for via := range s.net.servers {
		if strings.HasPrefix(via, "peer:") && want[via] == "" {
			gone = append(gone, via)
		}
	}
	for via := range want {
		if s.net.servers[via] != nil {
			delete(want, via)
		}
	}
	s.net.mu.Unlock()
	for _, via := range gone {
		s.closeClient(via)
	}
	if len(want) == 0 {
		return
	}
	if err := os.MkdirAll(serverSockDir(s.dir), 0o700); err != nil {
		fmt.Fprintf(s.log, "hi server: the peer's sockets: %v\n", err)
		return
	}
	for via, socket := range want {
		os.Remove(socket)
		listener, err := net.Listen("unix", socket)
		if err != nil {
			fmt.Fprintf(s.log, "hi server: %s: %v\n", socket, err)
			continue
		}
		os.Chmod(socket, 0o600)
		s.openClient(via, listener, handler)
	}
}

// keepNet is the server's half-minute look at its peers: each running,
// what the current one says of itself, and an old one gone once devices
// have followed, or after 14 days. It runs in the background, so a
// container engine that hangs never holds up the server's loop.
func (s *orgServer) keepNet() {
	n := &s.net
	n.mu.Lock()
	peer, old, dir := n.peer, n.old, n.dir
	n.mu.Unlock()
	if peer == nil || !n.keeping.TryLock() {
		return
	}
	go func() {
		defer n.keeping.Unlock()
		problem := ""
		for _, p := range []*serverPeerConfig{peer, old} {
			if p == nil {
				continue
			}
			if err := serverKeepPeer(dir, *p, io.Discard); err != nil && p == peer {
				problem = err.Error()
			}
		}
		status, err := serverPeerStatusOf(peer.ID)
		now := time.Now()
		n.mu.Lock()
		n.problem = problem
		if err == nil {
			n.status, n.read = status, now
		}
		if err == nil && status.Connected {
			n.offSince = time.Time{}
		} else if n.offSince.IsZero() {
			n.offSince = now
		}
		retire := old != nil && (now.After(old.Until) ||
			now.Sub(peer.Since) > serverOldQuietFor && now.Sub(n.lastOld) > serverOldQuietFor)
		n.mu.Unlock()
		if retire {
			s.retireOldPeer(*old)
		}
		s.sayPeerOff(*peer, now)
	}()
}

// sayPeerOff opens an incident once the peer has been off its network for
// longer than a restart takes, and closes it when it's back: in the audit,
// where Incidents reads it, and in the ops channel.
func (s *orgServer) sayPeerOff(peer serverPeerConfig, now time.Time) {
	since, why := s.net.offNetwork(now)
	n := &s.net
	n.mu.Lock()
	open, said := !since.IsZero(), n.offSaid
	n.offSaid = open
	n.mu.Unlock()
	switch {
	case open && !said:
		s.audit("server", "peer off its network", peer.Name, firstNonEmpty(why, "NetBird doesn't say why"))
		text := fmt.Sprintf("⚠️ The server's NetBird peer %s has been off its network since %s", peer.Name, since.Local().Format("15:04"))
		if why != "" {
			text += ": " + why
		}
		s.notifyChannel(text + ". Devices reach the server there again once it's back; hi server net on the server's machine says more.")
	case !open && said:
		s.audit("server", "peer back on its network", peer.Name, "")
		s.notifyChannel(fmt.Sprintf("The server's NetBird peer %s is back on its network.", peer.Name))
	}
}

// retireOldPeer removes the peer from before a move.
func (s *orgServer) retireOldPeer(old serverPeerConfig) {
	config, err := updateServerConfig(s.dir, func(c *serverConfig) error {
		if c.OldPeer == nil || c.OldPeer.ID != old.ID {
			return errors.New("gone already")
		}
		c.OldPeer = nil
		return nil
	})
	if err != nil {
		return
	}
	s.net.load(s.dir, config)
	s.closeClient("peer:" + old.ID)
	serverRemovePeer(s.dir, old.ID)
	s.audit("server", "removed the old NetBird peer", old.Name, "its devices followed, or 14 days passed")
}

// offNetwork says since when the peer hasn't been on its network, past
// the two minutes a restart takes; zero when it is.
func (n *serverNet) offNetwork(now time.Time) (time.Time, string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.peer == nil || n.offSince.IsZero() || now.Sub(n.offSince) < serverPeerOffAfter {
		return time.Time{}, ""
	}
	why := n.problem
	if why == "" {
		why = n.status.Problem
	}
	return n.offSince, why
}

// serverNetView is GET /admin/net: the server's addresses for the page and
// hi server net.
type serverNetView struct {
	Peer       *serverPeerView   `json:"peer,omitempty"`
	Old        *serverPeerView   `json:"old,omitempty"`
	Listen     string            `json:"listen,omitempty"` // the machine's NetBird address, from before its peer
	Join       string            `json:"join,omitempty"`   // what devices join with
	Local      string            `json:"local,omitempty"`
	OldDevices []serverOldDevice `json:"old_devices,omitempty"`
}

type serverPeerView struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	FQDN      string    `json:"fqdn,omitempty"`
	Address   string    `json:"address,omitempty"`
	Network   string    `json:"network,omitempty"` // its management server's host
	Connected bool      `json:"connected"`
	Problem   string    `json:"problem,omitempty"`
	Since     time.Time `json:"since"`
	Until     time.Time `json:"until,omitzero"`
}

func serverPeerViewOf(peer *serverPeerConfig, status serverPeerStatus, problem string) *serverPeerView {
	if peer == nil {
		return nil
	}
	view := &serverPeerView{ID: peer.ID, Name: peer.Name, FQDN: status.FQDN, Address: status.Address,
		Connected: status.Connected, Problem: firstNonEmpty(problem, status.Problem), Since: peer.Since, Until: peer.Until}
	view.Network = serverNetworkName(firstNonEmpty(status.Management, peer.ManagementURL, "https://api.netbird.io"))
	return view
}

// serverNetworkName is a management server's host, as people know it:
// without its port.
func serverNetworkName(management string) string {
	if parsed, err := url.Parse(management); err == nil && parsed.Hostname() != "" {
		return parsed.Hostname()
	}
	return qHost(management)
}

func (s *orgServer) netView() serverNetView {
	n := &s.net
	n.mu.Lock()
	peer, old, status, problem := n.peer, n.old, n.status, n.problem
	view := serverNetView{Listen: n.listen, Local: n.local}
	for _, device := range n.oldDevices {
		view.OldDevices = append(view.OldDevices, device)
	}
	n.mu.Unlock()
	sort.Slice(view.OldDevices, func(i, j int) bool { return view.OldDevices[i].Last.After(view.OldDevices[j].Last) })
	view.Peer = serverPeerViewOf(peer, status, problem)
	if old != nil {
		oldStatus, _ := serverPeerStatusOf(old.ID)
		view.Old = serverPeerViewOf(old, oldStatus, "")
	}
	if peer != nil {
		view.Join = serverJoinAddress(status)
	} else if view.Listen != "" {
		view.Join = view.Listen
	}
	return view
}

// netRoutes are /admin/net: the addresses, and reloading them after hi
// server net join or forget-old changed config.json.
func (s *orgServer) netRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /admin/net", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, s.netView())
	})
	mux.HandleFunc("POST /admin/net", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Action string `json:"action"`
			As     string `json:"as"`
			What   string `json:"what"`
			Target string `json:"target"`
			Detail string `json:"detail"`
		}
		json.NewDecoder(io.LimitReader(r.Body, serverMaxBody)).Decode(&input)
		switch input.Action {
		case "audit":
			s.audit(input.As, input.What, input.Target, input.Detail)
		case "reload":
			config, err := loadServerConfig(s.dir)
			if err != nil {
				writeAPIError(w, http.StatusInternalServerError, err.Error())
				return
			}
			s.reloadNet(config, s.clients)
		default:
			writeAPIError(w, http.StatusBadRequest, "reload or audit")
			return
		}
		writeJSON(w, http.StatusOK, s.netView())
	})
}

// reloadNet takes config.json's peers and addresses while the server runs.
func (s *orgServer) reloadNet(config serverConfig, clients http.Handler) {
	s.net.load(s.dir, config)
	if clients != nil {
		s.listenPeers(clients)
		if config.Listen == "" {
			s.closeClient("listen")
		}
	}
	s.exposure.mu.Lock()
	var stop func()
	if config.Peer != nil && config.ExposeListen != "off" {
		peer := serverPeerContainer(config.Peer.ID)
		if s.exposure.cmd != nil && s.exposure.peer != peer {
			// Made by the address before: the next run makes one on this.
			stop = s.exposure.stop
		}
		s.exposure.peer, s.exposure.listen = peer, "unix:"+serverInstanceSocket(s.dir)
	}
	s.exposure.mu.Unlock()
	if stop != nil {
		stop()
	}
	if clients != nil {
		s.openInstance()
	}
	s.keepNet()
}

// ---------------------------------------------------------------------------
// a device's side: following a move

// serverMoveNotes is where a device says it follows the server; tests
// replace it.
var serverMoveNotes io.Writer = os.Stderr

// serverMoveWatch notes a move's notice on the server's answers.
type serverMoveWatch struct {
	base   http.RoundTripper
	mu     sync.Mutex
	moved  string
	signed string
}

func (w *serverMoveWatch) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := w.base.RoundTrip(request)
	if err == nil {
		if moved := response.Header.Get(serverMovedHeader); moved != "" {
			w.mu.Lock()
			w.moved, w.signed = moved, response.Header.Get(serverMovedSigHeader)
			w.mu.Unlock()
		}
	}
	return response, err
}

func (w *serverMoveWatch) take() (string, string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	moved, signed := w.moved, w.signed
	w.moved, w.signed = "", ""
	return moved, signed
}

// serverProves asks the server at url to sign a challenge, and checks the
// signature with the server key this device has. So a device takes a new
// address only from the server it joined.
func serverProves(serverURL string, device ed25519.PrivateKey, pinned ed25519.PublicKey) error {
	buffer := make([]byte, 16)
	rand.Read(buffer)
	challenge := hex.EncodeToString(buffer)
	request, err := http.NewRequest(http.MethodGet, serverURL+"/v1/me", nil)
	if err != nil {
		return err
	}
	request.Header.Set("User-Agent", "hi/"+version)
	request.Header.Set(serverChallengeHeader, challenge)
	signRequest(request, device, nil)
	response, err := (&http.Client{Timeout: 15 * time.Second}).Do(request)
	if err != nil {
		return fmt.Errorf("can't reach the server at %s: %w", serverURL, err)
	}
	response.Body.Close()
	proof, err := base64.StdEncoding.DecodeString(response.Header.Get(serverProofHeader))
	if err != nil || !ed25519.Verify(pinned, []byte("hi-server-proof\n"+challenge), proof) {
		return fmt.Errorf("%s doesn't answer with the key of the server this device joined", serverURL)
	}
	return nil
}

// followMove takes a move's notice: signed with the server key this
// device has, and the new address answering with it, it becomes the
// device's server address.
func (c *serverClient) followMove() {
	if c.watch == nil {
		return
	}
	moved, signed := c.watch.take()
	if moved == "" || moved == c.url {
		return
	}
	connection, err := loadServerConnection()
	if err != nil || connection == nil || connection.URL != c.url {
		return
	}
	pinned, err := connection.serverPublicKey()
	if err != nil {
		return
	}
	signature, err := base64.StdEncoding.DecodeString(signed)
	if err != nil || !ed25519.Verify(pinned, []byte("hi-server-moved\n"+moved), signature) {
		return
	}
	if err := serverProves(moved, c.key, pinned); err != nil {
		if !c.warned {
			fmt.Fprintf(serverMoveNotes, "hi: the server moved to %s, but this device can't reach it there yet (%v); ask your IT team to let it reach %s on TCP %d\n", moved, err, qHost(moved), serverDefaultPort)
			c.warned = true
		}
		return
	}
	connection.URL = moved
	if saveServerConnection(*connection) == nil {
		fmt.Fprintf(serverMoveNotes, "hi: the server moved to %s; this device follows.\n", moved)
		c.url = moved
	}
}
