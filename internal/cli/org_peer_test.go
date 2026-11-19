package cli

import (
	"bufio"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const testPeerStatus = `{"fqdn":"ws1-server.example.net","netbirdIp":"100.92.0.7/16",
 "management":{"url":"https://netbird.example.net:443","connected":true,"error":""},
 "peers":{"details":[{"fqdn":"gpu1.example.net","netbirdIp":"100.92.5.9"},{"fqdn":"laptop.example.net.","netbirdIp":"100.92.8.1"}]}}`

// fakePeerEngine stands in for podman: it records what hi asks, and keeps
// which containers run with which stamp.
type fakePeerEngine struct {
	mu      sync.Mutex
	calls   []string
	created map[string]string
	running map[string]string
}

func (f *fakePeerEngine) called(prefix string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var found []string
	for _, call := range f.calls {
		if strings.HasPrefix(call, prefix) {
			found = append(found, call)
		}
	}
	return found
}

func useFakePeerEngine(t *testing.T) *fakePeerEngine {
	t.Helper()
	f := &fakePeerEngine{created: map[string]string{}, running: map[string]string{}}
	bin := t.TempDir()
	for _, name := range []string{"podman", "netbird"} {
		writeTestFile(t, filepath.Join(bin, name), "#!/bin/sh\n", 0o755)
	}
	previousLook, previousCommand, previousHi := boxLookPath, boxCommand, serverPeerHi
	previousTimeout, previousPoll := serverPeerJoinTimeout, serverPeerPoll
	boxLookPath = func(name string) (string, error) {
		if name == "podman" || name == "netbird" {
			return filepath.Join(bin, name), nil
		}
		return "", errors.New("not found")
	}
	boxCommand = func(_ io.Reader, stdout, _ io.Writer, _ string, args ...string) error {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.calls = append(f.calls, strings.Join(args, " "))
		last := args[len(args)-1]
		switch {
		case args[0] == "image":
			return nil
		case args[0] == "container" && args[1] == "inspect":
			if _, ok := f.running[last]; ok {
				fmt.Fprint(stdout, "running")
				return nil
			}
			return errors.New("no such container")
		case args[0] == "inspect":
			fmt.Fprint(stdout, f.running[last])
		case args[0] == "create":
			name, stamp := "", ""
			for i, arg := range args {
				if arg == "--name" {
					name = args[i+1]
				}
				if strings.HasPrefix(arg, "hi.peer=") {
					stamp = strings.TrimPrefix(arg, "hi.peer=")
				}
			}
			f.created[name] = stamp
		case args[0] == "start":
			f.running[last] = f.created[last]
		case args[0] == "rm":
			delete(f.running, last)
		case args[0] == "exec":
			return errors.New("no log in the fake")
		}
		return nil
	}
	serverPeerHi = func() (string, error) { return "/usr/local/bin/hi", nil }
	serverPeerJoinTimeout, serverPeerPoll = 300*time.Millisecond, 10*time.Millisecond
	t.Cleanup(func() {
		boxLookPath, boxCommand, serverPeerHi = previousLook, previousCommand, previousHi
		serverPeerJoinTimeout, serverPeerPoll = previousTimeout, previousPoll
	})
	return f
}

// useFakePeerStatus has every peer answer with status, and NetBird write
// its identity once it has joined, as it does.
func useFakePeerStatus(t *testing.T, dir string, status func(id string) (string, error)) {
	t.Helper()
	previous := serverPeerStatusOf
	serverPeerStatusOf = func(id string) (serverPeerStatus, error) {
		text, err := status(id)
		if err != nil {
			return serverPeerStatus{}, err
		}
		parsed, err := parseServerPeerStatus([]byte(text))
		if err == nil && parsed.Connected {
			os.MkdirAll(serverPeerIdentity(dir, id), 0o700)
			os.WriteFile(filepath.Join(serverPeerIdentity(dir, id), "default.json"), []byte("{}"), 0o600)
		}
		return parsed, err
	}
	t.Cleanup(func() { serverPeerStatusOf = previous })
}

func TestServerPeerScript(t *testing.T) {
	peer := serverPeerConfig{ID: "ab12cd", Name: "ws1-server"}
	first := serverPeerScript(peer, false)
	for _, want := range []string{
		"hi box __forward 127.0.0.1:7373 unix:/hi-server/client-ab12cd.sock",
		"hi box __forward 127.0.0.1:7374 unix:/hi-server/instance.sock",
		"hi box __forward unix:/hi-server/out-ab12cd.sock 127.0.0.1:1080",
		"netbird up --hostname ws1-server --disable-dns --setup-key-file /run/netbird-key",
	} {
		if !strings.Contains(first, want) {
			t.Fatalf("no %q in\n%s", want, first)
		}
	}
	if strings.Contains(first, "--management-url") {
		t.Fatal("a management URL nobody gave")
	}
	joined := serverPeerScript(peer, true)
	if strings.Contains(joined, "--setup-key-file") || appPeerStamp(joined) == appPeerStamp(first) {
		t.Fatalf("after joining, the key stays:\n%s", joined)
	}
	peer.ManagementURL = "https://netbird.example.net"
	if !strings.Contains(serverPeerScript(peer, true), "--management-url https://netbird.example.net") {
		t.Fatal("no management URL")
	}
	for _, bad := range []string{"https://x.example.net; rm -rf /", "ftp://x", "https://x.example.net/$(id)", "netbird.example.net"} {
		if validManagementURL(bad) {
			t.Fatalf("%q passes", bad)
		}
	}
}

func TestParseServerPeerStatus(t *testing.T) {
	status, err := parseServerPeerStatus([]byte(testPeerStatus))
	if err != nil {
		t.Fatal(err)
	}
	if status.FQDN != "ws1-server.example.net" || status.Address != "100.92.0.7" || status.prefix.String() != "100.92.0.0/16" ||
		!status.Connected || status.domain() != "example.net" || status.Management != "https://netbird.example.net:443" {
		t.Fatalf("status: %+v", status)
	}
	if status.names["laptop.example.net"].String() != "100.92.8.1" || status.names["gpu1.example.net"].String() != "100.92.5.9" {
		t.Fatalf("names: %v", status.names)
	}
	if serverPeerURL(status) != "http://ws1-server.example.net:7373" {
		t.Fatal(serverPeerURL(status))
	}
}

// testNet is a serverNet with a peer on 100.92.0.0/16 whose machine is at
// 100.92.0.2 too.
func testNet(t *testing.T, dir string) *serverNet {
	t.Helper()
	n := &serverNet{}
	n.load(dir, serverConfig{Peer: &serverPeerConfig{ID: "ab12cd", Name: "ws1-server"}})
	status, _ := parseServerPeerStatus([]byte(testPeerStatus))
	n.status = status
	n.locals[mustAddr(t, "100.92.0.2")] = true
	return n
}

func TestServerNetRoutes(t *testing.T) {
	dir := t.TempDir()
	n := testNet(t, dir)
	out := serverOutSocket(dir, "ab12cd")
	for _, c := range []struct {
		address, socket, target string
		fails                   bool
	}{
		{"100.92.5.9:8080", out, "100.92.5.9:8080", false},        // on the peer's network
		{"gpu1.example.net:8080", out, "100.92.5.9:8080", false},  // a peer by its name
		{"GPU1.example.net.:8080", out, "100.92.5.9:8080", false}, // as names come
		{"ghost.example.net:8080", "", "", true},                  // no such peer
		{"100.92.0.2:8080", "", "", false},                        // the machine itself
		{"127.0.0.1:8080", "", "", false},                         // loopback
		{"openrouter.ai:443", "", "", false},                      // the internet
		{"10.1.2.3:443", "", "", false},                           // another network
		{"example.net.evil.example.org:443", "", "", false},       // not under the domain
	} {
		socket, target, err := n.route(c.address)
		if (err != nil) != c.fails || socket != c.socket || target != c.target {
			t.Fatalf("%s: %q %q %v", c.address, socket, target, err)
		}
	}
	none := &serverNet{}
	none.load(dir, serverConfig{})
	if socket, _, _ := none.route("100.92.5.9:8080"); socket != "" {
		t.Fatal("a server without a peer goes through one")
	}
}

// fakeSOCKS is NetBird's SOCKS5 proxy on a socket: it answers every
// CONNECT by dialing to, and records what was asked.
func fakeSOCKS(t *testing.T, socket, to string) *[]string {
	t.Helper()
	os.MkdirAll(filepath.Dir(socket), 0o700)
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	var mu sync.Mutex
	asked := &[]string{}
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				reader := bufio.NewReader(conn)
				head := make([]byte, 2)
				io.ReadFull(reader, head)
				io.ReadFull(reader, make([]byte, head[1]))
				conn.Write([]byte{5, 0})
				request := make([]byte, 4)
				io.ReadFull(reader, request)
				host := ""
				switch request[3] {
				case 1:
					ip := make([]byte, 4)
					io.ReadFull(reader, ip)
					host = net.IP(ip).String()
				case 3:
					size, _ := reader.ReadByte()
					name := make([]byte, size)
					io.ReadFull(reader, name)
					host = string(name)
				}
				port := make([]byte, 2)
				io.ReadFull(reader, port)
				mu.Lock()
				*asked = append(*asked, net.JoinHostPort(host, fmt.Sprint(binary.BigEndian.Uint16(port))))
				mu.Unlock()
				target, err := net.Dial("tcp", to)
				if err != nil {
					conn.Write([]byte{5, 1, 0, 1, 0, 0, 0, 0, 0, 0})
					return
				}
				defer target.Close()
				conn.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0})
				go io.Copy(target, reader)
				io.Copy(conn, target)
			}()
		}
	}()
	return asked
}

func TestServerDialsThroughItsPeer(t *testing.T) {
	dir := t.TempDir()
	n := testNet(t, dir)
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "from gpu1") }))
	t.Cleanup(model.Close)
	asked := fakeSOCKS(t, serverOutSocket(dir, "ab12cd"), strings.TrimPrefix(model.URL, "http://"))
	client := &http.Client{Transport: &http.Transport{DialContext: n.dialContext}, Timeout: 5 * time.Second}
	for _, address := range []string{"http://gpu1.example.net:8080/v1", "http://100.92.5.9:8080/v1"} {
		response, err := client.Get(address)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if string(body) != "from gpu1" {
			t.Fatalf("%s: %q", address, body)
		}
	}
	if strings.Join(*asked, " ") != "100.92.5.9:8080 100.92.5.9:8080" {
		t.Fatalf("the proxy was asked for %v", *asked)
	}
	// The internet goes direct, past the peer.
	direct := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "direct") }))
	t.Cleanup(direct.Close)
	response, err := client.Get(direct.URL)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if len(*asked) != 2 {
		t.Fatalf("loopback went through the peer: %v", *asked)
	}
	if _, err := client.Get("http://ghost.example.net:8080/"); err == nil || !strings.Contains(err.Error(), "no peer named ghost.example.net") {
		t.Fatalf("an unknown peer: %v", err)
	}
}

// writeServerConfig writes a config.json with what an admin set by hand.
func writeServerConfig(t *testing.T, dir string, config map[string]any) {
	t.Helper()
	data, _ := json.Marshal(config)
	writeTestFile(t, filepath.Join(dir, "config.json"), string(data), 0o600)
}

func readServerConfigMap(t *testing.T, dir string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	config := map[string]any{}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	return config
}

func TestServerNetJoinAndMove(t *testing.T) {
	ts := newTestServer(t)
	engine := useFakePeerEngine(t)
	writeServerConfig(t, ts.dir, map[string]any{"listen": "100.64.0.9:7373", "expose_listen": "off", "fallback_price_factor": 2})
	useFakePeerStatus(t, ts.dir, func(string) (string, error) { return testPeerStatus, nil })

	status, err := serverNetJoin(ts.dir, serverNetJoinOptions{Key: []byte("KEY-1\n"), Name: "ws1-server"}, "olga", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if serverJoinAddress(status) != "ws1-server.example.net" {
		t.Fatalf("status: %+v", status)
	}
	config, err := loadServerConfig(ts.dir)
	if err != nil || config.Peer == nil || config.Peer.Name != "ws1-server" || config.Peer.Since.IsZero() || config.OldPeer != nil || config.Listen != "100.64.0.9:7373" {
		t.Fatalf("config: %+v %v", config, err)
	}
	if raw := readServerConfigMap(t, ts.dir); raw["expose_listen"] != "off" || raw["fallback_price_factor"] != float64(2) {
		t.Fatalf("what the admin set is gone: %v", raw)
	}
	first := config.Peer.ID
	if fileExists(serverPeerKeyPath(ts.dir, first)) {
		t.Fatal("the setup key stays on disk after the peer joined")
	}
	creates := engine.called("create")
	if len(creates) != 2 || !strings.Contains(creates[0], "/run/netbird-key:ro") || strings.Contains(creates[1], "/run/netbird-key") ||
		!strings.Contains(creates[0], "NB_USE_NETSTACK_MODE=true") || !strings.Contains(creates[0], "--cap-drop=ALL") ||
		!strings.Contains(creates[0], serverSockDir(ts.dir)+":/hi-server") {
		t.Fatalf("creates:\n%s", strings.Join(creates, "\n"))
	}

	// A move: a new peer, on another network; the first answers beside it.
	status, err = serverNetJoin(ts.dir, serverNetJoinOptions{Key: []byte("KEY-2"), ManagementURL: "https://netbird.example.net/"}, "olga", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	config, _ = loadServerConfig(ts.dir)
	if config.Peer == nil || config.Peer.ID == first || config.Peer.Name != "ws1-server" || config.Peer.ManagementURL != "https://netbird.example.net" ||
		config.OldPeer == nil || config.OldPeer.ID != first || time.Until(config.OldPeer.Until) < 13*24*time.Hour {
		t.Fatalf("after the move: %+v %+v", config.Peer, config.OldPeer)
	}
	if _, err := serverNetJoin(ts.dir, serverNetJoinOptions{Key: []byte("KEY-3")}, "olga", io.Discard); err == nil || !strings.Contains(err.Error(), "still moving") {
		t.Fatalf("a move during a move: %v", err)
	}
	if audit := readTestFile(t, filepath.Join(ts.dir, "audit.jsonl")); !strings.Contains(audit, "moved the server to another NetBird network") {
		t.Fatalf("audit: %s", audit)
	}

	// forget-old: the old peer and the machine's address go.
	_, gone, err := serverForgetOld(ts.dir)
	if err != nil || strings.Join(gone, ", ") != "the peer ws1-server, 100.64.0.9:7373" {
		t.Fatalf("gone: %v %v", gone, err)
	}
	config, _ = loadServerConfig(ts.dir)
	if config.OldPeer != nil || config.Listen != "" || fileExists(serverPeerDir(ts.dir, first)) {
		t.Fatalf("after forget-old: %+v", config)
	}
	if len(engine.called("rm -f "+serverPeerContainer(first))) == 0 {
		t.Fatal("the old peer's container stays")
	}
}

func TestServerNetJoinThatFailsChangesNothing(t *testing.T) {
	ts := newTestServer(t)
	engine := useFakePeerEngine(t)
	writeServerConfig(t, ts.dir, map[string]any{"listen": "100.64.0.9:7373"})
	useFakePeerStatus(t, ts.dir, func(string) (string, error) {
		return `{"fqdn":"","netbirdIp":"","management":{"url":"https://api.netbird.io:443","connected":false,"error":"invalid setup key"}}`, nil
	})
	_, err := serverNetJoin(ts.dir, serverNetJoinOptions{Key: []byte("WRONG")}, "olga", io.Discard)
	if err == nil || !strings.Contains(err.Error(), "invalid setup key") {
		t.Fatalf("a wrong key: %v", err)
	}
	config, _ := loadServerConfig(ts.dir)
	entries, _ := os.ReadDir(serverNetDir(ts.dir))
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "peer-") {
			t.Fatalf("a failed peer's folder stays: %s", entry.Name())
		}
	}
	if config.Peer != nil || config.Listen != "100.64.0.9:7373" || len(engine.called("rm -f "+serverPeerPrefix)) == 0 {
		t.Fatalf("after a failed join: %+v", config)
	}
	if _, err := serverNetJoin(ts.dir, serverNetJoinOptions{Key: []byte("K"), Name: "Bad_Name"}, "olga", io.Discard); err == nil {
		t.Fatal("a bad name")
	}
	if _, err := serverNetJoin(ts.dir, serverNetJoinOptions{Key: []byte("K"), ManagementURL: "https://x.example.net/$(id)"}, "olga", io.Discard); err == nil {
		t.Fatal("a management URL with a shell in it")
	}
}

// movedTestServer has ts answer at two addresses: the old one (the
// machine's NetBird address) and its peer's, which devices are told of.
func movedTestServer(t *testing.T, ts *testServer) (old, peer *httptest.Server) {
	t.Helper()
	ts.server.net.load(ts.dir, serverConfig{Listen: "100.64.0.9:7373", Peer: &serverPeerConfig{ID: "ab12cd", Name: "ws1-server", Since: time.Now()}})
	status, _ := parseServerPeerStatus([]byte(testPeerStatus))
	ts.server.net.status = status
	clients := ts.server.clientHandler()
	old = httptest.NewServer(ts.server.viaHandler("listen", clients))
	peer = httptest.NewServer(ts.server.viaHandler("peer:ab12cd", clients))
	t.Cleanup(old.Close)
	t.Cleanup(peer.Close)
	previous := serverPeerURLOf
	serverPeerURLOf = func(serverPeerStatus) string { return peer.URL }
	t.Cleanup(func() { serverPeerURLOf = previous })
	return old, peer
}

func pointDeviceAt(t *testing.T, url string) serverConnection {
	t.Helper()
	connection, err := loadServerConnection()
	if err != nil || connection == nil {
		t.Fatalf("connection: %v", err)
	}
	connection.URL = url
	if err := saveServerConnection(*connection); err != nil {
		t.Fatal(err)
	}
	return *connection
}

// captureMoveNotes collects what a device says about following its server.
func captureMoveNotes(t *testing.T) *strings.Builder {
	t.Helper()
	var notes strings.Builder
	previous := serverMoveNotes
	serverMoveNotes = &notes
	t.Cleanup(func() { serverMoveNotes = previous })
	return &notes
}

func TestDeviceFollowsTheServerToItsPeer(t *testing.T) {
	ts := newTestServer(t)
	notes := captureMoveNotes(t)
	ts.connectAs(t, "alice", "students")
	old, peer := movedTestServer(t, ts)
	before := pointDeviceAt(t, old.URL)
	if before.ServerKey == "" {
		t.Fatal("no server key pinned")
	}
	code, _, stderr := runHi("server", "status")
	if code != 0 || !strings.Contains(notes.String(), "the server moved to "+peer.URL) {
		t.Fatalf("status: %d %s %s", code, stderr, notes)
	}
	after, _ := loadServerConnection()
	if after.URL != peer.URL || after.ServerKey != before.ServerKey || after.User != "alice" {
		t.Fatalf("after: %+v", after)
	}
	view := ts.server.netView()
	if len(view.OldDevices) != 1 || view.OldDevices[0].User != "alice" || view.OldDevices[0].Via != "listen" {
		t.Fatalf("devices at the old address: %+v", view.OldDevices)
	}
	// At the peer, no notice: nothing changes.
	notes.Reset()
	code, _, stderr = runHi("server", "status")
	if code != 0 || strings.Contains(notes.String(), "moved") {
		t.Fatalf("at the new address: %d %s %s", code, stderr, notes)
	}
}

func TestDeviceTakesNoForgedMove(t *testing.T) {
	ts := newTestServer(t)
	notes := captureMoveNotes(t)
	ts.connectAs(t, "alice", "students")
	_, stranger := newStrangerServer(t)
	// An address that passes the device on, with a notice someone else
	// signed, to a server with another key.
	_, forger, _ := ed25519.GenerateKey(rand.Reader)
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		moved, signature := serverMovedNotice(forger, stranger.URL)
		w.Header().Set(serverMovedHeader, moved)
		w.Header().Set(serverMovedSigHeader, signature)
		ts.server.clientHandler().ServeHTTP(w, r)
	}))
	t.Cleanup(relay.Close)
	pointDeviceAt(t, relay.URL)
	runHi("server", "status")
	if after, _ := loadServerConnection(); after.URL != relay.URL {
		t.Fatalf("followed a forged notice to %s", after.URL)
	}
	// Signed by the server, to an address that can't prove its key.
	relay2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		moved, signature := serverMovedNotice(ts.server.serverKey, stranger.URL)
		w.Header().Set(serverMovedHeader, moved)
		w.Header().Set(serverMovedSigHeader, signature)
		ts.server.clientHandler().ServeHTTP(w, r)
	}))
	t.Cleanup(relay2.Close)
	pointDeviceAt(t, relay2.URL)
	runHi("server", "status")
	if after, _ := loadServerConnection(); after.URL != relay2.URL || !strings.Contains(notes.String(), "can't reach it there yet") {
		t.Fatalf("followed to an address without the key: %s\n%s", after.URL, notes)
	}
}

// newStrangerServer is another server, with a key of its own.
func newStrangerServer(t *testing.T) (*orgServer, *httptest.Server) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "stranger")
	os.MkdirAll(dir, 0o700)
	server, err := openServer(dir, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	http := httptest.NewServer(server.viaHandler("local", server.clientHandler()))
	t.Cleanup(http.Close)
	return server, http
}

func TestJoinAgainAtTheServersNewAddress(t *testing.T) {
	ts := newTestServer(t)
	ts.connectAs(t, "alice", "students")
	_, peer := movedTestServer(t, ts)
	_, stranger := newStrangerServer(t)
	code, stdout, stderr := runHi("server", "join", peer.URL)
	if code != 0 || !strings.Contains(stdout, "is the server this device joined") {
		t.Fatalf("join at the new address: %d\n%s%s", code, stdout, stderr)
	}
	if after, _ := loadServerConnection(); after.URL != peer.URL || after.User != "alice" {
		t.Fatalf("after: %+v", after)
	}
	code, _, stderr = runHi("server", "join", stranger.URL)
	if code == 0 || !strings.Contains(stderr, "run `hi server leave` first") {
		t.Fatalf("join another server: %d %s", code, stderr)
	}
}

func TestOldPeerRetires(t *testing.T) {
	ts := newTestServer(t)
	engine := useFakePeerEngine(t)
	useFakePeerStatus(t, ts.dir, func(string) (string, error) { return testPeerStatus, nil })
	now := time.Now().UTC()
	current := &serverPeerConfig{ID: "cc3333", Name: "ws1-server", Since: now.Add(-2 * time.Hour)}
	old := &serverPeerConfig{ID: "bb2222", Name: "ws1-server", Since: now.Add(-30 * 24 * time.Hour), Until: now.Add(13 * 24 * time.Hour)}
	data, _ := json.Marshal(serverConfig{Peer: current, OldPeer: old})
	writeTestFile(t, filepath.Join(ts.dir, "config.json"), string(data), 0o600)
	os.MkdirAll(serverPeerKeyPath(ts.dir, "cc3333"), 0o700)
	keep := func() {
		ts.server.keepNet()
		ts.server.net.keeping.Lock() // its containers, kept in the background
		ts.server.net.keeping.Unlock()
	}
	config, _ := loadServerConfig(ts.dir)
	ts.server.net.load(ts.dir, config)
	keep()
	if config, _ := loadServerConfig(ts.dir); config.OldPeer == nil {
		t.Fatal("the old peer went within hours of the move")
	}
	// A day after the move, with no device at the old address for a day.
	current.Since = now.Add(-25 * time.Hour)
	data, _ = json.Marshal(serverConfig{Peer: current, OldPeer: old})
	writeTestFile(t, filepath.Join(ts.dir, "config.json"), string(data), 0o600)
	config, _ = loadServerConfig(ts.dir)
	ts.server.net.load(ts.dir, config)
	ts.server.net.lastOld = now.Add(-25 * time.Hour)
	keep()
	if config, _ := loadServerConfig(ts.dir); config.OldPeer != nil || config.Peer == nil {
		t.Fatalf("after a quiet day: %+v", config)
	}
	if len(engine.called("rm -f "+serverPeerContainer("bb2222"))) == 0 {
		t.Fatal("the old peer's container stays")
	}
	if audit := readTestFile(t, filepath.Join(ts.dir, "audit.jsonl")); !strings.Contains(audit, "removed the old NetBird peer") {
		t.Fatalf("audit: %s", audit)
	}
}

func TestExposureRunsInThePeer(t *testing.T) {
	ts, _ := newDataServer(t)
	public := httptest.NewServer(ts.server.instanceHandler())
	t.Cleanup(public.Close)
	dir := t.TempDir()
	log := filepath.Join(dir, "podman.log")
	podman := filepath.Join(dir, "podman")
	pid := filepath.Join(dir, "expose.pid")
	writeTestFile(t, podman, "#!/bin/sh\necho \"$*\" >> "+log+"\ncase \"$*\" in\n"+
		"*pkill*) kill -TERM $(cat "+pid+");;\n"+
		"*'netbird expose'*) echo $$ > "+pid+"; echo 'Service exposed successfully!'; echo '  URL:      "+public.URL+"'; exec sleep 601;;\nesac\n", 0o755)
	previous := boxLookPath
	boxLookPath = func(name string) (string, error) {
		if name == "podman" {
			return podman, nil
		}
		return "", errors.New("not found")
	}
	t.Cleanup(func() { boxLookPath = previous; ts.server.stopExposure() })
	ts.server.exposure.peer, ts.server.exposure.listen = "hi-server-peer-ab12cd", "unix:"+filepath.Join(dir, "instance.sock")
	url, err := ts.server.ensureExposure("ws1")
	if err != nil || url != public.URL {
		t.Fatalf("exposure: %q %v", url, err)
	}
	ts.server.stopExposure()
	calls := readTestFile(t, log)
	if !strings.Contains(calls, "exec hi-server-peer-ab12cd netbird expose 7374 --with-name-prefix hi-ws1") ||
		!strings.Contains(calls, "exec hi-server-peer-ab12cd pkill -INT -f ^netbird expose") {
		t.Fatalf("podman was asked:\n%s", calls)
	}
}

func TestForwardListensOnASocket(t *testing.T) {
	echo, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { echo.Close() })
	go func() {
		for {
			conn, err := echo.Accept()
			if err != nil {
				return
			}
			go func() { defer conn.Close(); io.Copy(conn, conn) }()
		}
	}()
	socket := filepath.Join(t.TempDir(), "out.sock")
	writeTestFile(t, socket, "a socket left from the last start", 0o600)
	go runBoxForward([]string{"unix:" + socket, echo.Addr().String()}, io.Discard)
	var conn net.Conn
	for range 100 {
		if conn, err = net.Dial("unix", socket); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if conn == nil {
		t.Fatalf("no socket: %v", err)
	}
	defer conn.Close()
	conn.Write([]byte("ping"))
	buffer := make([]byte, 4)
	if _, err := io.ReadFull(conn, buffer); err != nil || string(buffer) != "ping" {
		t.Fatalf("%q %v", buffer, err)
	}
}

func TestServerProofAndNotice(t *testing.T) {
	public, private, _ := ed25519.GenerateKey(rand.Reader)
	moved, signature := serverMovedNotice(private, "http://ws1-server.example.net:7373")
	decoded, _ := base64.StdEncoding.DecodeString(signature)
	if moved != "http://ws1-server.example.net:7373" || !ed25519.Verify(public, []byte("hi-server-moved\n"+moved), decoded) {
		t.Fatal("the notice doesn't verify")
	}
	proof, _ := base64.StdEncoding.DecodeString(serverProof(private, "abc"))
	if !ed25519.Verify(public, []byte("hi-server-proof\nabc"), proof) || ed25519.Verify(public, []byte("hi-server-moved\nabc"), proof) {
		t.Fatal("a proof that passes as a notice")
	}
}

func mustAddr(t *testing.T, text string) netip.Addr {
	t.Helper()
	address, err := netip.ParseAddr(text)
	if err != nil {
		t.Fatal(err)
	}
	return address
}

func TestPeerOffItsNetworkIsAnIncident(t *testing.T) {
	ts := newTestServer(t)
	useFakePeerEngine(t)
	saved := adminOrgDir
	adminOrgDir = func() string { return ts.dir }
	t.Cleanup(func() { adminOrgDir = saved })
	connected := false
	useFakePeerStatus(t, ts.dir, func(string) (string, error) {
		if connected {
			return testPeerStatus, nil
		}
		return `{"fqdn":"ws1-server.example.net","netbirdIp":"100.92.0.7/16","management":{"url":"https://netbird.example.net:443","connected":false,"error":"the management server doesn't answer"}}`, nil
	})
	peer := &serverPeerConfig{ID: "ab12cd", Name: "ws1-server", Since: time.Now()}
	os.MkdirAll(serverPeerIdentity(ts.dir, peer.ID), 0o700)
	writeTestFile(t, filepath.Join(serverPeerIdentity(ts.dir, peer.ID), "default.json"), "{}", 0o600)
	ts.server.net.load(ts.dir, serverConfig{Peer: peer})
	keep := func() {
		ts.server.keepNet()
		ts.server.net.keeping.Lock()
		ts.server.net.keeping.Unlock()
	}
	keep()
	if incidents := adminOrgIncidents(time.Now()); len(incidents) != 0 {
		t.Fatalf("an incident within the two minutes a restart takes: %+v", incidents)
	}
	ts.server.net.offSince = time.Now().Add(-3 * time.Minute)
	keep()
	incidents := adminOrgIncidents(time.Now())
	if len(incidents) != 1 || incidents[0].Status != "open" || !strings.Contains(incidents[0].Title, "ws1-server is off its network") ||
		!strings.Contains(incidents[0].Where, "the management server doesn't answer") {
		t.Fatalf("incidents: %+v", incidents)
	}
	keep() // said once
	if audit := readTestFile(t, filepath.Join(ts.dir, "audit.jsonl")); strings.Count(audit, "peer off its network") != 1 {
		t.Fatalf("audit: %s", audit)
	}
	connected = true
	keep()
	incidents = adminOrgIncidents(time.Now())
	if len(incidents) != 1 || incidents[0].Status != "closed" || !strings.Contains(incidents[0].Last, "back on it") {
		t.Fatalf("after it's back: %+v", incidents)
	}
}

func TestServerNetSaysWhereTheServerIs(t *testing.T) {
	ts := newTestServer(t)
	useFakePeerEngine(t)
	useFakePeerStatus(t, ts.dir, func(string) (string, error) { return testPeerStatus, nil })
	writeServerConfig(t, ts.dir, map[string]any{"listen": "100.64.0.9:7373"})
	code, stdout, _ := runHi("server", "net", "--dir", ts.dir)
	if code != 0 || !strings.Contains(stdout, "listens on this machine's NetBird address, 100.64.0.9:7373") || !strings.Contains(stdout, "hi server net join") {
		t.Fatalf("without a peer: %d\n%s", code, stdout)
	}
	writeServerConfig(t, ts.dir, map[string]any{"listen": "100.64.0.9:7373",
		"peer": map[string]any{"id": "ab12cd", "name": "ws1-server", "since": time.Now()}})
	code, stdout, _ = runHi("server", "net", "--dir", ts.dir)
	for _, want := range []string{
		"The server's NetBird peer: ws1-server.example.net (100.92.0.7), on its network, at netbird.example.net.",
		"Devices join with: hi server join ws1-server.example.net",
		"It still answers at 100.64.0.9:7373",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("no %q in\n%s", want, stdout)
		}
	}
	code, stdout, stderr := runHi("server", "net", "forget-old", "--dir", ts.dir)
	if code != 0 || !strings.Contains(stdout, "no longer answers at 100.64.0.9:7373") {
		t.Fatalf("forget-old: %d %s %s", code, stdout, stderr)
	}
	if code, _, stderr := runHi("server", "net", "join", "--dir", ts.dir); code == 0 || !strings.Contains(stderr, "setup key") {
		t.Fatalf("join without a key or a terminal: %d %s", code, stderr)
	}
}
