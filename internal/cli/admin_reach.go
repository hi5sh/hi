package cli

// How people reach the admin page, on port 1984 (hi_admin.md, Reaching
// it): on a NetBird peer of its own where podman runs, the small rootless
// container apps' addresses use, named <machine>-admin; or on the
// machine's own NetBird address, as the org server listens. On the peer,
// port 80 is for a name the IT team keeps, and 443 is <peer>.<domain> when
// the machine has a domain. It is never public.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	adminPeerContainer = "hi-admin-peer"
	adminSetupKeyFile  = "netbird-key"
)

func adminPeerDir() string         { return filepath.Join(adminDir(), "netbird") }
func adminSetupKeyPath() string    { return filepath.Join(adminDir(), adminSetupKeyFile) }
func adminHTTPSSocket() string     { return filepath.Join(adminSocketDir(), "https.sock") }
func adminHTTPSDomainFile() string { return filepath.Join(adminDir(), "https-domain") }
func adminHTTPSRecordFile() string { return filepath.Join(adminDir(), "https-record.json") }
func adminHTTPSTakenFile() string  { return filepath.Join(adminDir(), "https-taken") }

// adminPeerJoined says the admin page's peer is on the network.
func adminPeerJoined() bool { return fileExists(filepath.Join(adminPeerDir(), "default.json")) }

var adminHostPart = regexp.MustCompile(`[^a-z0-9-]+`)

// adminPeerHostname is the name the peer joins with, <machine>-admin, or
// the one it joined with.
func adminPeerHostname() string {
	if data, err := os.ReadFile(filepath.Join(adminDir(), "netbird-host")); err == nil {
		if name := strings.TrimSpace(string(data)); name != "" {
			return name
		}
	}
	machine := strings.Trim(adminHostPart.ReplaceAllString(adminMachineName(), "-"), "-")
	return firstNonEmpty(machine, "hi") + "-admin"
}

// adminPeerName is the peer's name on the network, as NetBird gave it.
func adminPeerName() string {
	if data, err := os.ReadFile(filepath.Join(adminDir(), "netbird-fqdn")); err == nil {
		if name := strings.TrimSpace(string(data)); name != "" {
			return name
		}
	}
	return adminPeerHostname() + "." + appNetbirdDomain()
}

// adminMachineNetBirdName is this machine's own name on NetBird, or its
// NetBird address; tests replace it.
var adminMachineNetBirdName = func() string {
	adminNameMu.Lock()
	defer adminNameMu.Unlock()
	if time.Since(adminNameAt) < time.Minute && adminNameValue != "" {
		return adminNameValue
	}
	adminNameValue, adminNameAt = adminAskNetBirdName(), time.Now()
	return adminNameValue
}

// The machine's NetBird name, asked of NetBird once a minute at most: the
// page reads its address every few seconds.
var (
	adminNameMu    sync.Mutex
	adminNameAt    time.Time
	adminNameValue string
)

func adminAskNetBirdName() string {
	out, err := exec.Command("netbird", "status", "--json").Output()
	if err == nil {
		var status struct {
			FQDN string `json:"fqdn"`
		}
		if json.Unmarshal(out, &status) == nil && strings.Contains(status.FQDN, ".") {
			return strings.TrimSuffix(status.FQDN, ".")
		}
	}
	address, _ := netbirdAddress()
	return address
}

// adminOnMachineAddress says the page listens on the machine's NetBird
// address: a machine without podman, or one whose peer hasn't joined yet.
func adminOnMachineAddress(config adminConfig) bool {
	return config.Reach == adminReachNetBird || config.Reach == adminReachPeer && !adminPeerJoined()
}

// adminServerPeer is the name of the machine's server's NetBird peer
// when it carries the page: the page has no peer of its own (hi_server_peer.md).
func adminServerPeer(config adminConfig) string {
	if config.Reach == adminReachLocal || adminPeerJoined() {
		return ""
	}
	dir := adminOrgDir()
	if dir == "" {
		return ""
	}
	if server, err := loadServerConfig(dir); err != nil || server.Peer == nil {
		return ""
	}
	var name serverPeerName
	if data, err := os.ReadFile(serverPeerNamePath(dir)); err == nil && json.Unmarshal(data, &name) == nil {
		return firstNonEmpty(name.FQDN, name.Address)
	}
	return ""
}

// adminServerTarget is where the server's people reach it, for its
// NetBird policy: its own peer when it has one (hi server net join), else
// this machine.
func adminServerTarget(dir string) string {
	if server, err := loadServerConfig(dir); err == nil && server.Peer != nil {
		var name serverPeerName
		if data, err := os.ReadFile(serverPeerNamePath(dir)); err == nil && json.Unmarshal(data, &name) == nil && firstNonEmpty(name.FQDN, name.Address) != "" {
			return "its peer " + firstNonEmpty(name.FQDN, name.Address)
		}
		return "its peer " + server.Peer.Name
	}
	return "this machine, " + firstNonEmpty(adminMachineNetBirdName(), adminMachineName())
}

// adminHTTPSDomain is the domain the page is on over https, once its
// record and the certificate are there.
func adminHTTPSDomain() string {
	data, err := os.ReadFile(adminHTTPSDomainFile())
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// adminDomainLabel is the page's name under the machine's domain: the one
// hi server address gave it, else the name of the peer that carries it.
func adminDomainLabel(config adminConfig) string {
	return firstNonEmpty(config.DomainName, adminDefaultDomainLabel(config))
}

// adminDefaultDomainLabel is the page's name under the machine's domain
// when none is set: its NetBird peer's, so the page has one name on the
// network and in DNS (aiw11-server.corp.example, as the peer is
// aiw11-server.netbird.cloud), and machines that share the domain don't
// want the same name.
func adminDefaultDomainLabel(config adminConfig) string {
	var name string
	if config.Reach == adminReachPeer && adminPeerJoined() {
		name = adminPeerName()
	} else if peer := adminServerPeer(config); peer != "" {
		name = peer
		if _, err := netip.ParseAddr(peer); err == nil {
			// NetBird gave only an address: the name it joined with.
			name = ""
			if server, err := loadServerConfig(adminOrgDir()); err == nil && server.Peer != nil {
				name = server.Peer.Name
			}
		}
	}
	label, _, _ := strings.Cut(strings.ToLower(name), ".")
	if !adminDomainLabelPattern.MatchString(label) {
		return adminPeerHostname()
	}
	return label
}

// adminKeepOldName keeps the page at admin.<domain> on a machine where hi
// put it there before the peer's name was the default (v2.6.282), so its
// address doesn't move.
func adminKeepOldName() {
	unlock := adminLock()
	defer unlock()
	config, ok := loadAdminConfig()
	if !ok || config.DomainName != "" {
		return
	}
	var record appHTTPSRecord
	if data, err := os.ReadFile(adminHTTPSRecordFile()); err != nil || json.Unmarshal(data, &record) != nil {
		return
	}
	for _, name := range record.Names {
		if strings.HasPrefix(name, "admin.") {
			config.DomainName = "admin"
			saveAdminConfig(config)
			return
		}
	}
}

// adminHasDomain says the page is, or will be, at <name>.<domain>: the
// machine has a domain, no name the IT team keeps is set, and a peer
// carries the page, its own or the server's.
func adminHasDomain(config adminConfig) bool {
	_, ok := loadHiDomain()
	return ok && config.Host == "" && (config.Reach == adminReachPeer && adminPeerJoined() || adminServerPeer(config) != "")
}

// adminDomainTarget is the NetBird address the page's name points at: its
// own peer's, or the server's peer's when that carries the page.
func adminDomainTarget(config adminConfig) (netip.Addr, error) {
	if config.Reach == adminReachPeer && adminPeerJoined() {
		engine, _, err := detectBoxEngine()
		if err != nil {
			return netip.Addr{}, err
		}
		_, ip, err := adminPeerStatus(engine)
		if err != nil || !ip.IsValid() {
			return netip.Addr{}, errors.New("the peer's NetBird address isn't there yet")
		}
		return ip, nil
	}
	if dir := adminOrgDir(); dir != "" && adminServerPeer(config) != "" {
		var name serverPeerName
		if data, err := os.ReadFile(serverPeerNamePath(dir)); err == nil && json.Unmarshal(data, &name) == nil {
			if ip, err := netip.ParseAddr(name.Address); err == nil {
				return ip, nil
			}
			if prefix, err := netip.ParsePrefix(name.Address); err == nil {
				return prefix.Addr(), nil
			}
		}
		return netip.Addr{}, errors.New("the server's peer's NetBird address isn't there yet")
	}
	return netip.Addr{}, errors.New("no peer carries the page")
}

// adminURL is where the admin page is, as its links say.
func adminURL(config adminConfig) string {
	switch {
	case config.Host != "" && config.HTTPS:
		return "https://" + config.Host + "/"
	case config.Host != "":
		return "http://" + config.Host + "/"
	case adminHasDomain(config) && adminHTTPSDomain() != "":
		return "https://" + adminDomainLabel(config) + "." + adminHTTPSDomain() + "/"
	case config.Reach == adminReachPeer && adminPeerJoined():
		return fmt.Sprintf("http://%s:%d/", adminPeerName(), adminPort)
	case adminServerPeer(config) != "":
		return fmt.Sprintf("http://%s:%d/", adminServerPeer(config), adminPort)
	case adminOnMachineAddress(config):
		if name := adminMachineNetBirdName(); name != "" {
			return fmt.Sprintf("http://%s:%d/", name, adminPort)
		}
	}
	return fmt.Sprintf("http://127.0.0.1:%d/", adminPort)
}

// adminPolicyTarget is what NetBird's policy, or the IT team's record,
// points at: the peer, or the machine.
func adminPolicyTarget(config adminConfig) string {
	if peer := adminServerPeer(config); peer != "" {
		return "the server's peer " + peer
	}
	if config.Reach == adminReachPeer {
		return "the peer " + adminPeerHostname()
	}
	return "this machine, " + firstNonEmpty(adminMachineNetBirdName(), adminMachineName())
}

// adminReachLine says how people reach the page, and what it waits for.
func adminReachLine(config adminConfig) string {
	switch config.Reach {
	case adminReachLocal:
		return fmt.Sprintf("On this machine only. From your computer: ssh -L %d:127.0.0.1:%d %s, then open http://127.0.0.1:%d/.", adminPort, adminPort, adminMachineName(), adminPort)
	}
	if peer := adminServerPeer(config); peer != "" {
		return "On the server's NetBird peer, " + peer + ", beside the server, for whoever NetBird lets reach it on TCP 1984; on this machine's NetBird address too, and at http://127.0.0.1:1984/."
	}
	switch config.Reach {
	case adminReachNetBird:
		return "On this machine's NetBird address, for whoever NetBird lets reach it, and on this machine at http://127.0.0.1:1984/."
	}
	switch {
	case adminPeerJoined():
		return "On its own NetBird peer, " + adminPeerName() + ", and on this machine at http://127.0.0.1:1984/."
	case fileExists(adminSetupKeyPath()):
		return "Its NetBird peer, " + adminPeerHostname() + ", is joining with the setup key; until it has, the page is on this machine's NetBird address, for whoever reaches the machine."
	}
	return "It has no NetBird peer of its own yet, so it is on this machine's NetBird address, for whoever reaches the machine. Give it a setup key, on the page (Machine) or with hi server init --netbird <file>, and it moves to " + adminPeerHostname() + ", which only the admins' NetBird policy reaches."
}

// adminSaveSetupKey keeps the peer's setup key until the peer has joined.
func adminSaveSetupKey(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return adminSaveSetupKeyText(string(data))
}

func adminSaveSetupKeyText(text string) error {
	key := strings.TrimSpace(text)
	if key == "" || len(key) > 200 || strings.ContainsAny(key, " \t\n") {
		return errors.New("that isn't a NetBird setup key: one line, as NetBird's dashboard shows it (Setup Keys)")
	}
	if err := os.MkdirAll(adminDir(), 0o700); err != nil {
		return err
	}
	return writeFileAtomic(adminSetupKeyPath(), []byte(key+"\n"), 0o600)
}

// ---------------------------------------------------------------------------
// the peer

// adminPeerScript is what the peer's container runs: hi passing ports
// 1984 and 80 to the page's socket, 443 to its TLS socket with a domain,
// and NetBird's userspace stack, which hands what reaches the peer to the
// container's loopback. Its output stays in the container, as an app
// peer's does.
func adminPeerScript(joined, https bool) string {
	up := "netbird up --hostname " + adminPeerHostname() + " --disable-dns"
	if !joined {
		up += " --setup-key-file /run/netbird-key"
	}
	forwards := fmt.Sprintf("hi box __forward 127.0.0.1:%d unix:/hi-admin/http.sock & hi box __forward 127.0.0.1:80 unix:/hi-admin/http.sock & ", adminPort)
	if https {
		forwards += "hi box __forward 127.0.0.1:443 unix:/hi-admin/https.sock & "
	}
	return "exec >>/tmp/peer.log 2>&1; " + forwards + "netbird service run --log-file /tmp/netbird.log & nb=$!; sleep 3; " + up + "; wait $nb"
}

// adminKeepPeer has the peer running as its script says now: started the
// first time with the setup key, again when it stopped, and made again
// when hi runs it differently. It does nothing without a key or a peer.
func adminKeepPeer(hi string) error {
	joined := adminPeerJoined()
	if joined {
		os.Remove(adminSetupKeyPath()) // used: the peer exists
	}
	if !joined && !fileExists(adminSetupKeyPath()) {
		return nil
	}
	engine, _, err := detectBoxEngine()
	if err != nil {
		return err
	}
	netbird, err := boxHostBinary("netbird")
	if err != nil {
		return errors.New("NetBird isn't installed on this machine, so the admin page has no peer; hi install netbird")
	}
	script := adminPeerScript(joined, adminHTTPSDomain() != "")
	if engine.state(adminPeerContainer) == "running" && appContainerLabel(engine, adminPeerContainer, "hi.peer") == appPeerStamp(script) {
		adminRefreshPeerName(engine)
		return nil
	}
	image, err := engine.ensureBaseImage(os.Stderr, os.Stderr)
	if err != nil {
		return fmt.Errorf("hi's box image, which the peer runs: %w", err)
	}
	if !joined {
		writeFileAtomic(filepath.Join(adminDir(), "netbird-host"), []byte(adminPeerHostname()+"\n"), 0o600)
	}
	for _, dir := range []string{adminPeerDir(), adminSocketDir()} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	engine.output("rm", "-f", adminPeerContainer)
	args := []string{"create", "--init", "--name", adminPeerContainer, "--hostname", adminPeerHostname(), "--network", engine.defaultNetwork(),
		"--label", "hi.peer=" + appPeerStamp(script),
		"--cap-drop=ALL", "--cap-add=NET_BIND_SERVICE", "--security-opt", "no-new-privileges", "--memory", "256m",
		"-e", "NB_USE_NETSTACK_MODE=true", "-e", "NB_ENABLE_NETSTACK_LOCAL_FORWARDING=true", "-e", "NB_LOG_MAX_SIZE_MB=5",
		"-v", adminPeerDir() + ":/var/lib/netbird", "-v", netbird + ":/usr/local/bin/netbird:ro", "-v", hi + ":/usr/local/bin/hi:ro",
		"-v", adminSocketDir() + ":/hi-admin"}
	if !joined {
		args = append(args, "-v", adminSetupKeyPath()+":/run/netbird-key:ro")
	}
	args = append(args, image, "sh", "-c", script)
	if out, err := engine.output(args...); err != nil {
		return fmt.Errorf("the admin page's NetBird peer: %s", qFirstLine(out, err.Error()))
	}
	if out, err := engine.output("start", adminPeerContainer); err != nil {
		return fmt.Errorf("the admin page's NetBird peer: %s", qFirstLine(out, err.Error()))
	}
	return nil
}

// adminPeerStatus is what the peer's NetBird says of itself.
func adminPeerStatus(engine boxEngine) (fqdn string, ip netip.Addr, err error) {
	out, err := engine.quietExec(adminPeerContainer, "netbird", "status", "--json")
	if err != nil {
		return "", ip, err
	}
	var status struct {
		FQDN string `json:"fqdn"`
		IP   string `json:"netbirdIp"`
	}
	if err := json.Unmarshal([]byte(out), &status); err != nil {
		return "", ip, err
	}
	if prefix, err := netip.ParsePrefix(status.IP); err == nil {
		ip = prefix.Addr()
	}
	return strings.TrimSuffix(status.FQDN, "."), ip, nil
}

// adminRefreshPeerName keeps the peer's name as NetBird has it: an admin
// may rename it in NetBird's dashboard.
func adminRefreshPeerName(engine boxEngine) {
	fqdn, _, err := adminPeerStatus(engine)
	if err != nil || !strings.Contains(fqdn, ".") {
		return
	}
	path := filepath.Join(adminDir(), "netbird-fqdn")
	if current, _ := os.ReadFile(path); strings.TrimSpace(string(current)) != fqdn {
		writeFileAtomic(path, []byte(fqdn+"\n"), 0o600)
	}
}

// adminStopPeer removes the peer's container; its identity stays.
var adminStopPeer = func() {
	if engine, _, err := detectBoxEngine(); err == nil {
		engine.output("rm", "-f", adminPeerContainer)
	}
}

// ---------------------------------------------------------------------------
// <name>.<domain>

// adminApplyHTTPS has the page at <name>.<domain> (its peer's name,
// unless hi server address named it) while the machine has a domain and a peer
// carries the page, its own or the server's: the name's record at the
// peer's address, the domain's wildcard certificate (the one apps'
// addresses use), and the peer passing 443 on. A name the page had before
// leaves the zone. It says whether the peer must run differently.
func (s *adminServer) applyHTTPS(config adminConfig) (bool, error) {
	domain, ok := loadHiDomain()
	current := adminHTTPSDomain()
	if !ok || !adminHasDomain(config) {
		if current != "" {
			os.Remove(adminHTTPSDomainFile())
			return true, nil
		}
		return false, nil
	}
	ip, err := adminDomainTarget(config)
	if err != nil {
		return false, err
	}
	dns, err := domain.provider()
	if err != nil {
		return false, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	name := adminDomainLabel(config) + "." + domain.Domain
	recordPath := adminHTTPSRecordFile()
	var record appHTTPSRecord
	if data, err := os.ReadFile(recordPath); err == nil {
		json.Unmarshal(data, &record)
	}
	if strings.Join(record.Names, " ") != name || record.IP != ip.String() || !record.Marked {
		takenFile := adminHTTPSTakenFile()
		if why := hiDomainTakenNow(takenFile); why != "" {
			return false, errors.New(why)
		}
		// Only names hi kept before marks are this machine's without one.
		var mine []string
		if !record.Marked {
			mine = record.Names
		}
		err := hiDomainKeep(ctx, dns, domain, []string{name}, ip, mine, appHTTPSTTL)
		hiDomainNoteTaken(takenFile, err)
		if err != nil {
			return false, err
		}
		// The name it had before, when it was renamed: out of the zone.
		old, _ := netip.ParseAddr(record.IP)
		for _, was := range record.Names {
			if was != name && strings.HasSuffix(was, "."+domain.Domain) {
				if err := hiDomainDrop(ctx, dns, domain, []string{was}, old, appHTTPSTTL); err != nil {
					s.logf("removing %s from %s: %v", was, domain.Zone, err)
				}
			}
		}
		record = appHTTPSRecord{Names: []string{name}, IP: ip.String(), Marked: true, Set: time.Now().UTC()}
		data, _ := json.MarshalIndent(record, "", "  ")
		writeFileAtomic(recordPath, append(data, '\n'), 0o600)
	}
	s.certs.mu.Lock()
	have := s.certs.config != nil && s.certs.domain.Domain == domain.Domain && s.certs.domain.Provider == domain.Provider
	failed := s.certs.failed
	s.certs.mu.Unlock()
	if !have {
		if time.Since(failed) < appHTTPSRetry {
			return false, nil
		}
		issued, err := appHTTPSIssue(ctx, domain, dns)
		s.certs.mu.Lock()
		if err != nil {
			s.certs.failed = time.Now()
		} else {
			s.certs.config, s.certs.domain, s.certs.ready = issued, domain, true
		}
		s.certs.mu.Unlock()
		if err != nil {
			return false, fmt.Errorf("the certificate for *.%s (hi tries again in %s): %w", domain.Domain, appHTTPSRetry, err)
		}
	}
	if current != domain.Domain {
		if err := writeFileAtomic(adminHTTPSDomainFile(), []byte(domain.Domain+"\n"), 0o600); err != nil {
			return false, err
		}
		return true, nil
	}
	return false, nil
}

// ---------------------------------------------------------------------------
// keeping it reachable

// keepReach keeps the page reachable as admin.json says, every 30
// seconds: the peer, the machine's NetBird address, and https.
func (s *adminServer) keepReach(ctx context.Context) {
	var machine net.Listener
	var machineAddress string
	for {
		config, _ := loadAdminConfig()
		if config.Reach != adminReachLocal {
			if changed, err := s.applyHTTPS(config); err != nil {
				s.logf("the admin page at %s.<domain>: %v", adminDomainLabel(config), err)
			} else if changed && adminPeerJoined() {
				adminStopPeer() // made again with 443, or without
			}
			// The server's peer, when it carries the page, follows by
			// itself: its script has 443 while the page has a domain.
		}
		if config.Reach == adminReachPeer {
			if err := adminKeepPeer(s.hi); err != nil {
				s.logf("%v", err)
			}
		}
		// The machine's NetBird address: on a machine without podman, or
		// until the peer has joined. Its address can come late at boot, or
		// change.
		want := ""
		if adminOnMachineAddress(config) {
			if address, err := netbirdAddress(); err == nil {
				want = net.JoinHostPort(address, fmt.Sprint(adminPort))
			}
		}
		if machine != nil && machineAddress != want {
			machine.Close()
			machine, machineAddress = nil, ""
		}
		if machine == nil && want != "" {
			if listener, err := net.Listen("tcp", want); err != nil {
				s.logf("listening on %s: %v", want, err)
			} else {
				machine, machineAddress = listener, want
				go s.serveOn(listener)
			}
		}
		select {
		case <-ctx.Done():
			if machine != nil {
				machine.Close()
			}
			return
		case <-s.wake:
		case <-time.After(30 * time.Second):
		}
	}
}

// adminCheckDomainLabel checks a name for the page under the machine's
// domain: one label, and not one of an app's names there.
func adminCheckDomainLabel(label string) error {
	if !adminDomainLabelPattern.MatchString(label) {
		return fmt.Errorf("%q isn't one name under the domain, such as admin or console: small letters, digits and -, no dots", label)
	}
	domain := hiDomain{Domain: "x"}
	for _, app := range adminAppNames() {
		for _, address := range appHTTPSAddresses {
			for _, name := range domain.addressNames(app, address) {
				if label+".x" == name {
					return fmt.Errorf("%s is the app %s's name", label, app)
				}
			}
		}
	}
	return nil
}

// appCheckNotPageName refuses an app's name when one of its names under
// the domain is the admin page's: admin, which older machines keep, or
// the page's name on this machine.
func appCheckNotPageName(app string) error {
	labels := []string{"admin"}
	if config, ok := loadAdminConfig(); ok {
		labels = append(labels, adminDomainLabel(config))
	}
	domain := hiDomain{Domain: "x"}
	for _, address := range appHTTPSAddresses {
		for _, name := range domain.addressNames(app, address) {
			for _, label := range labels {
				if name == label+".x" {
					return fmt.Errorf("%s is the admin page's name (%s.<domain>); pick another for the app", label, label)
				}
			}
		}
	}
	return nil
}

var adminDomainLabelPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
