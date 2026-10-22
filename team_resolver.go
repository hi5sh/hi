package main

// hi watch, release 3: the app's boxes look names up through a resolver of
// the team's, which logs every name, so the hi server sees where
// production connects. The app keeps its full internet: a resolver breaks
// no protocol, unlike a proxy. Each slot has a port on this machine;
// Podman's pasta carries the box's DNS (port 53, UDP and TCP) to it, and
// the box's second nameserver, pasta's own, answers when the resolver
// doesn't within two seconds. A lookup by raw address or over
// DNS-over-HTTPS isn't seen.
//
// The resolver runs as a service of its own (hi-team-<name>-resolver), not
// in __serve, so the app's lookups don't wait while the team restarts.
//
// Spec: docs/specs/approved/hi_watch.md (release 3).

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

func teamResolverPath(team string) string {
	return filepath.Join(teamDir(team), "run", "resolver.json")
}
func teamResolveLogPath(team string) string {
	return filepath.Join(teamDir(team), "run", "proxy", "resolve.log")
}
func teamResolverUnit(team string) string { return "hi-team-" + team + "-resolver" }

// teamResolverPorts are the ports the team's resolver listens on, one per
// slot, chosen once and kept, so a box started before a restart still
// reaches it.
func teamResolverPorts(team string) (map[string]int, error) {
	ports := map[string]int{}
	if data, err := os.ReadFile(teamResolverPath(team)); err == nil && json.Unmarshal(data, &ports) == nil && ports[teamSlotProduction] > 0 && ports[teamSlotPreview] > 0 {
		return ports, nil
	}
	for _, slot := range []string{teamSlotProduction, teamSlotPreview} {
		port, err := teamFreeUDPPort()
		if err != nil {
			return nil, err
		}
		ports[slot] = port
	}
	if err := os.MkdirAll(filepath.Dir(teamResolverPath(team)), 0o700); err != nil {
		return nil, err
	}
	data, _ := json.Marshal(ports)
	return ports, writeFileAtomic(teamResolverPath(team), append(data, '\n'), 0o600)
}

// teamFreeUDPPort is a port free for both UDP and TCP on 127.0.0.1.
func teamFreeUDPPort() (int, error) {
	for range 20 {
		udp, err := net.ListenPacket("udp", "127.0.0.1:0")
		if err != nil {
			return 0, err
		}
		port := udp.LocalAddr().(*net.UDPAddr).Port
		tcp, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
		udp.Close()
		if err == nil {
			tcp.Close()
			return port, nil
		}
	}
	return 0, errors.New("no free port for the team's resolver")
}

// teamAppNetworkArgs are what an app's box gets to look names up through
// the team's resolver: only with Podman's pasta, and nothing when the
// resolver has no port.
func teamAppNetworkArgs(team, slot string, engine boxEngine) []string {
	if engine.name != "podman" || !teamResolverOn {
		return nil
	}
	ports, err := teamResolverPorts(team)
	if err != nil || ports[slot] == 0 {
		return nil
	}
	port := strconv.Itoa(ports[slot])
	return []string{"--network", "pasta:-U,53:" + port + ",-T,53:" + port,
		"--dns", "127.0.0.1", "--dns", "169.254.1.1", "--dns-option", "timeout:2", "--dns-option", "attempts:1"}
}

// teamResolverOn is false in tests, whose app boxes are fakes.
var teamResolverOn = true

// startTeamResolver starts the team's resolver as a service of its own,
// again, so it runs this hi; outside systemd it runs in this process.
func startTeamResolver(team, hi string, done <-chan struct{}, stderr io.Writer) {
	if _, err := teamResolverPorts(team); err != nil {
		fmt.Fprintf(stderr, "hi: the app's resolver is off this time: %v\n", err)
		return
	}
	if os.Getenv("INVOCATION_ID") == "" {
		go func() {
			if err := serveTeamResolver(team, done); err != nil {
				fmt.Fprintf(stderr, "hi: the app's resolver: %v\n", err)
			}
		}()
		return
	}
	unit := teamResolverUnit(team)
	exec.Command("systemctl", "--user", "stop", unit).Run()
	exec.Command("systemctl", "--user", "reset-failed", unit).Run()
	out, err := exec.Command("systemd-run", "--user", "--unit", unit, "--quiet", "--collect",
		"--property=Restart=always", "--property=RestartSec=2", "--description=hi team "+team+": the app's resolver",
		hi, "team", "__resolver", team).CombinedOutput()
	if err != nil {
		fmt.Fprintf(stderr, "hi: the app's resolver: %s\n", qFirstLine(strings.TrimSpace(string(out)), err.Error()))
	}
}

// stopTeamResolver stops it, when the team is removed.
func stopTeamResolver(team string) {
	exec.Command("systemctl", "--user", "stop", teamResolverUnit(team)).Run()
}

// runTeamResolver is hi team __resolver <team>.
func runTeamResolver(args []string, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "usage: hi team __resolver <team>")
		return 2
	}
	if err := serveTeamResolver(args[0], nil); err != nil {
		fmt.Fprintf(stderr, "hi: %v\n", err)
		return 1
	}
	return 0
}

// teamUpstreamDNS is this machine's first nameserver; tests replace it.
var teamUpstreamDNS = teamFirstNameserver

func teamFirstNameserver() string {
	data, _ := os.ReadFile("/etc/resolv.conf")
	for _, line := range strings.Split(string(data), "\n") {
		if fields := strings.Fields(line); len(fields) >= 2 && fields[0] == "nameserver" {
			return net.JoinHostPort(fields[1], "53")
		}
	}
	return "127.0.0.53:53"
}

func serveTeamResolver(team string, done <-chan struct{}) error {
	ports, err := teamResolverPorts(team)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(teamResolveLogPath(team)), 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(teamResolveLogPath(team), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	log := &boxNetworkLog{file: file}
	resolver := &teamResolver{upstream: teamUpstreamDNS(), log: log}
	errs := make(chan error, 4)
	var closers []io.Closer
	for slot, port := range ports {
		address := "127.0.0.1:" + strconv.Itoa(port)
		udp, err := net.ListenPacket("udp", address)
		if err != nil {
			return fmt.Errorf("the %s resolver on %s: %w", slot, address, err)
		}
		tcp, err := net.Listen("tcp", address)
		if err != nil {
			udp.Close()
			return fmt.Errorf("the %s resolver on %s: %w", slot, address, err)
		}
		closers = append(closers, udp, tcp)
		go func() { errs <- resolver.serveUDP(udp, slot) }()
		go func() { errs <- resolver.serveTCP(tcp, slot) }()
	}
	defer func() {
		for _, closer := range closers {
			closer.Close()
		}
	}()
	select {
	case err := <-errs:
		return err
	case <-done:
		return nil
	}
}

type teamResolver struct {
	upstream string
	log      *boxNetworkLog
	mu       sync.Mutex
}

// teamDNSName is the name a DNS query asks about.
func teamDNSName(query []byte) string {
	if len(query) < 13 {
		return ""
	}
	var labels []string
	for i := 12; i < len(query) && query[i] != 0; {
		n := int(query[i])
		if n&0xc0 != 0 || i+1+n > len(query) {
			return ""
		}
		labels = append(labels, string(query[i+1:i+1+n]))
		i += 1 + n
	}
	return strings.ToLower(strings.Join(labels, "."))
}

func (r *teamResolver) note(slot string, query []byte) {
	if name := teamDNSName(query); name != "" {
		r.log.record("resolve-"+slot, name)
	}
}

func (r *teamResolver) serveUDP(conn net.PacketConn, slot string) error {
	buffer := make([]byte, 4096)
	for {
		n, from, err := conn.ReadFrom(buffer)
		if err != nil {
			return err
		}
		query := append([]byte(nil), buffer[:n]...)
		r.note(slot, query)
		go func() {
			upstream, err := net.DialTimeout("udp", r.upstream, 2*time.Second)
			if err != nil {
				return
			}
			defer upstream.Close()
			upstream.SetDeadline(time.Now().Add(4 * time.Second))
			if _, err := upstream.Write(query); err != nil {
				return
			}
			answer := make([]byte, 65535)
			if n, err := upstream.Read(answer); err == nil {
				conn.WriteTo(answer[:n], from)
			}
		}()
	}
}

func (r *teamResolver) serveTCP(listener net.Listener, slot string) error {
	for {
		client, err := listener.Accept()
		if err != nil {
			return err
		}
		go func() {
			defer client.Close()
			upstream, err := net.DialTimeout("tcp", r.upstream, 2*time.Second)
			if err != nil {
				return
			}
			defer upstream.Close()
			for {
				client.SetDeadline(time.Now().Add(10 * time.Second))
				var size [2]byte
				if _, err := io.ReadFull(client, size[:]); err != nil {
					return
				}
				query := make([]byte, binary.BigEndian.Uint16(size[:]))
				if _, err := io.ReadFull(client, query); err != nil {
					return
				}
				r.note(slot, query)
				upstream.SetDeadline(time.Now().Add(5 * time.Second))
				if _, err := upstream.Write(append(size[:], query...)); err != nil {
					return
				}
				if _, err := io.ReadFull(upstream, size[:]); err != nil {
					return
				}
				answer := make([]byte, binary.BigEndian.Uint16(size[:]))
				if _, err := io.ReadFull(upstream, answer); err != nil {
					return
				}
				if _, err := client.Write(append(size[:], answer...)); err != nil {
					return
				}
			}
		}()
	}
}
