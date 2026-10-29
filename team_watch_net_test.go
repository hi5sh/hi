package main

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// The proxy says how much each connection sent and received, and when a
// box's allowlist grows.
func TestBoxProxyLogsSizesAndNewHosts(t *testing.T) {
	dir := t.TempDir()
	allowPath, logPath := filepath.Join(dir, "allow"), filepath.Join(dir, "network.log")
	os.WriteFile(allowPath, []byte("allowed.test\n"), 0o600)
	logFile, _ := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	defer logFile.Close()
	log := &boxNetworkLog{file: logFile}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		io.WriteString(w, "upstream says hi")
	}))
	defer upstream.Close()
	proxy := &boxEgressProxy{allow: &boxAllowlist{path: allowPath, log: log}, log: log,
		dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			return net.Dial("tcp", strings.TrimPrefix(upstream.URL, "http://"))
		}}
	server := httptest.NewServer(proxy)
	defer server.Close()
	proxyURL, _ := url.Parse(server.URL)
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}
	response, err := client.Post("http://allowed.test/x", "text/plain", strings.NewReader(strings.Repeat("a", 1000)))
	if err != nil {
		t.Fatal(err)
	}
	io.ReadAll(response.Body)
	response.Body.Close()

	conn, _ := net.Dial("tcp", proxyURL.Host)
	fmt.Fprintf(conn, "CONNECT allowed.test:443 HTTP/1.1\r\nHost: allowed.test:443\r\n\r\n")
	reader := bufio.NewReader(conn)
	reader.ReadString('\n')
	reader.ReadString('\n')
	fmt.Fprintf(conn, "GET / HTTP/1.1\r\nHost: allowed.test\r\nConnection: close\r\n\r\n")
	io.ReadAll(reader)
	conn.Close()

	time.Sleep(10 * time.Millisecond)
	os.WriteFile(allowPath, []byte("allowed.test\nnew.test\n"), 0o600)
	future := time.Now().Add(time.Second)
	os.Chtimes(allowPath, future, future)
	client.Get("http://new.test/x")

	teamTestEventually(t, "no closed lines", func() bool {
		data, _ := os.ReadFile(logPath)
		return strings.Count(string(data), " closed allowed.test ") == 2
	})
	data, _ := os.ReadFile(logPath)
	if !strings.Contains(string(data), " closed allowed.test 1000 16 ") || !strings.Contains(string(data), " allowlist-added new.test\n") {
		t.Fatal(string(data))
	}
}

// The app's lookups go through the team's resolver, which logs each name
// with the slot that asked, over UDP and TCP.
func TestTeamResolverLogsLookups(t *testing.T) {
	setupTeamTest(t)
	newTeamTestBroker(t)
	// An upstream that answers every query with the query itself, marked
	// as an answer.
	upstreamUDP, _ := net.ListenPacket("udp", "127.0.0.1:0")
	defer upstreamUDP.Close()
	address := upstreamUDP.LocalAddr().String()
	upstreamTCP, err := net.Listen("tcp", address)
	if err != nil {
		t.Skip("no TCP on the same port")
	}
	defer upstreamTCP.Close()
	go func() {
		buffer := make([]byte, 512)
		for {
			n, from, err := upstreamUDP.ReadFrom(buffer)
			if err != nil {
				return
			}
			answer := append([]byte(nil), buffer[:n]...)
			answer[2] |= 0x80
			upstreamUDP.WriteTo(answer, from)
		}
	}()
	go func() {
		for {
			conn, err := upstreamTCP.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				var size [2]byte
				io.ReadFull(conn, size[:])
				query := make([]byte, binary.BigEndian.Uint16(size[:]))
				io.ReadFull(conn, query)
				query[2] |= 0x80
				conn.Write(append(size[:], query...))
			}()
		}
	}()
	saved := teamUpstreamDNS
	teamUpstreamDNS = func() string { return address }
	t.Cleanup(func() { teamUpstreamDNS = saved })
	ports, _ := teamResolverPorts("payments")
	done := make(chan struct{})
	defer close(done)
	go func() {
		if err := serveTeamResolver("payments", done); err != nil {
			t.Error(err)
		}
	}()
	query := func(name string) []byte {
		q := []byte{0x12, 0x34, 0x01, 0x00, 0, 1, 0, 0, 0, 0, 0, 0}
		for _, label := range strings.Split(name, ".") {
			q = append(q, byte(len(label)))
			q = append(q, label...)
		}
		return append(q, 0, 0, 1, 0, 1)
	}
	var answer []byte
	teamTestEventually(t, "no answer over UDP", func() bool {
		conn, err := net.Dial("udp", fmt.Sprintf("127.0.0.1:%d", ports[teamSlotProduction]))
		if err != nil {
			return false
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(200 * time.Millisecond))
		conn.Write(query("api.example.com"))
		answer = make([]byte, 512)
		n, err := conn.Read(answer)
		answer = answer[:n]
		return err == nil
	})
	if answer[0] != 0x12 || answer[2]&0x80 == 0 {
		t.Fatalf("%x", answer)
	}
	conn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", ports[teamSlotPreview]))
	if err != nil {
		t.Fatal(err)
	}
	q := query("Paste.Example.org")
	conn.Write(append([]byte{byte(len(q) >> 8), byte(len(q))}, q...))
	var size [2]byte
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := io.ReadFull(conn, size[:]); err != nil {
		t.Fatal(err)
	}
	conn.Close()
	teamTestEventually(t, "lookups not logged", func() bool {
		data, _ := os.ReadFile(teamResolveLogPath("payments"))
		return strings.Contains(string(data), " resolve-production api.example.com\n") && strings.Contains(string(data), " resolve-preview paste.example.org\n")
	})
	// The app's box gets its port through pasta, and a second nameserver.
	teamResolverOn = true
	args := strings.Join(teamAppNetworkArgs("payments", teamSlotProduction, boxEngine{name: "podman"}), " ")
	if !strings.Contains(args, fmt.Sprintf("pasta:-U,53:%d,-T,53:%d", ports[teamSlotProduction], ports[teamSlotProduction])) || !strings.Contains(args, "--dns 169.254.1.1") {
		t.Fatal(args)
	}
	if args := teamAppNetworkArgs("payments", teamSlotProduction, boxEngine{name: "docker"}); args != nil {
		t.Fatal(args)
	}
}

func testWatchLines(at time.Time, lines ...string) []string {
	var out []string
	for _, line := range lines {
		out = append(out, at.UTC().Format(time.RFC3339)+" "+line)
	}
	return out
}

func TestWatchNetworkRules(t *testing.T) {
	rig := newTestWatch(t)
	now := time.Now()
	send := func(at time.Time, logs ...apiWatchNetwork) apiHeartbeatReply {
		t.Helper()
		beat := testBeat(at)
		beat.Exempt = []string{"bucket.s3.example"}
		for _, log := range logs {
			if log.Log == "" {
				lead := log
				beat.Network = &lead
			} else {
				beat.Logs = append(beat.Logs, log)
			}
		}
		return rig.beat(beat, at)
	}
	network := func(log string, from int64, lines []string) apiWatchNetwork {
		size := int64(0)
		for _, line := range lines {
			size += int64(len(line) + 1)
		}
		return apiWatchNetwork{Log: log, From: from, To: from + size, Lines: lines}
	}
	var denies []string
	for range 21 {
		denies = append(denies, "blocked evil.example")
	}
	lead := network("", 0, testWatchLines(now, append(denies,
		"allowed 203.0.113.9",
		"closed api.example.net 60000000 100 30.0",
		"closed bucket.s3.example 90000000 0 60.0",
		"closed openrouter.ai 90000000 0 60.0")...))
	// An idle agent's own calls, refused on purpose, aren't a box steered.
	quiet := []string{"allowlist-added pastebin.com"}
	for range 30 {
		quiet = append(quiet, "blocked api.anthropic.com")
	}
	box := network("box:payments-t-3", 0, testWatchLines(now, quiet...))
	reply := send(now, lead, box)
	if reply.NetworkKept != lead.To || reply.LogsKept["box:payments-t-3"] != box.To {
		t.Fatalf("%+v", reply)
	}
	rules := strings.Join(openWatchRules(rig), " ")
	if rules != "denies: raw-ip::203.0.113.9 upload:api.example.net allowlist:box:payments-t-3:pastebin.com" {
		t.Fatal(rules)
	}
	if reply.Do != nil {
		t.Fatal("a freeze for what isn't a burst")
	}

	// A burst of sends to a paste site: an incident, and the team freezes.
	burst := network("", lead.To, testWatchLines(now.Add(time.Minute), "allowed pastebin.com", "closed pastebin.com 2000000 100 1.0"))
	reply = send(now.Add(time.Minute), burst)
	if reply.Do == nil || reply.Do.Action != "freeze" || reply.Do.By != "watch" || !strings.Contains(reply.Do.Because, "paste, file-drop, or tunnel hosts") {
		t.Fatalf("%+v", reply.Do)
	}
	if !strings.Contains(strings.Join(openWatchRules(rig), " "), "burst") {
		t.Fatal(openWatchRules(rig))
	}
	// It does, and says so: no more asking.
	beat := testBeat(now.Add(2 * time.Minute))
	beat.Stop = apiWatchStop{Frozen: true, Done: reply.Do.ID}
	reply = rig.beat(beat, now.Add(2*time.Minute))
	if reply.Do != nil || !rig.team().Beat.Stop.Frozen {
		t.Fatalf("%+v", reply.Do)
	}

	// Production looks up a host it hadn't: in the week's line, after its
	// first day.
	apps := network("apps", 0, testWatchLines(now.Add(-48*time.Hour), "resolve-production api.stripe.com"))
	send(now.Add(-48*time.Hour), apps)
	apps = network("apps", apps.To, testWatchLines(now.Add(3*time.Minute), "resolve-production api.stripe.com", "resolve-production cdn.new.example", "resolve-preview other.example"))
	send(now.Add(3*time.Minute), apps)
	newHosts := strings.Join(rig.team().NewHosts, " ")
	if newHosts != "cdn.new.example" {
		t.Fatal(newHosts)
	}

	// A day later, what didn't happen again is closed.
	send(now.Add(26 * time.Hour))
	if rules := openWatchRules(rig); len(rules) != 0 {
		t.Fatal(rules)
	}
}

// An admin freezes a team: on its next heartbeat it empties the
// allowlists (the lead keeps Slack), pauses the app, and starts nothing;
// unfreezing puts back exactly what was there.
func TestTeamFreezeAndPause(t *testing.T) {
	setupTeamTest(t)
	t.Setenv("USER", "bob")
	b := newTeamTestBroker(t)
	channel := &fakeTeamChannel{}
	b.channel = channel
	savedMachine, savedEngine := teamWatchMachine, teamWatchEngine
	t.Cleanup(func() { teamWatchMachine, teamWatchEngine = savedMachine, savedEngine })
	teamWatchMachine = (&teamTestProbe{states: map[string]string{}, disk: 0.3}).probe()
	var mu sync.Mutex
	var engine []string
	teamWatchEngine = func(args ...string) (string, error) {
		mu.Lock()
		defer mu.Unlock()
		if args[0] == "ps" {
			return "hi-box-team-payments\nhi-box-team-payments-proxy\nhi-box-team-payments-app-production-4100\nhi-box-payments-t-1\nhi-box-other\n", nil
		}
		engine = append(engine, strings.Join(args, " "))
		return "", nil
	}
	lead := filepath.Join(teamDir("payments"), "run", "proxy", "allow")
	os.MkdirAll(filepath.Dir(lead), 0o700)
	os.WriteFile(lead, []byte("*\n"), 0o600)
	saveTeamTask("payments", teamTask{ID: "t-1", Role: "coder", Box: "payments-t-1", Status: "running"})
	os.MkdirAll(boxStateFile("payments-t-1"), 0o700)
	os.WriteFile(boxStateFile("payments-t-1", "allow"), []byte("registry.npmjs.org\n"), 0o600)

	stop := func(action, why string) {
		t.Helper()
		if err := teamStopCommand(action, []string{"payments", "--why", why}, io.Discard); err != nil {
			t.Fatal(err)
		}
	}
	if err := teamStopCommand("freeze", []string{"payments"}, io.Discard); err == nil {
		t.Fatal("a freeze without why")
	}
	b.sendHeartbeat("")
	stop("freeze", "looking into a paste site")
	b.sendHeartbeat("")
	if data, _ := os.ReadFile(lead); string(data) != "slack.com\n" {
		t.Fatalf("%q", data)
	}
	if data, _ := os.ReadFile(boxStateFile("payments-t-1", "allow")); len(data) != 0 {
		t.Fatalf("%q", data)
	}
	if strings.Join(engine, "; ") != "pause hi-box-team-payments-app-production-4100" {
		t.Fatal(engine)
	}
	if len(channel.calls) == 0 || !strings.Contains(strings.Join(channel.calls, "\n"), "froze the team* (bob on the machine: looking into a paste site)") {
		t.Fatal(channel.calls)
	}
	b.sendHeartbeat("")
	w := teamLocalWatcher("payments")
	w.mu.Lock()
	watched := *w.state.Teams["payments"]
	w.mu.Unlock()
	if !watched.Beat.Stop.Frozen || !watched.WantDone {
		t.Fatalf("%+v", watched)
	}

	// Pause, then unpause: every box of the team, and only the team's.
	stop("pause", "a look at memory")
	b.sendHeartbeat("")
	stop("unpause", "")
	b.sendHeartbeat("")
	stop("unfreeze", "it was a test")
	b.sendHeartbeat("")
	if data, _ := os.ReadFile(lead); string(data) != "*\n" {
		t.Fatalf("%q", data)
	}
	if data, _ := os.ReadFile(boxStateFile("payments-t-1", "allow")); string(data) != "registry.npmjs.org\n" {
		t.Fatalf("%q", data)
	}
	want := "pause hi-box-team-payments-app-production-4100; " +
		"pause hi-box-payments-t-1; pause hi-box-team-payments; pause hi-box-team-payments-app-production-4100; pause hi-box-team-payments-proxy; " +
		"unpause hi-box-payments-t-1; unpause hi-box-team-payments; unpause hi-box-team-payments-app-production-4100; unpause hi-box-team-payments-proxy; pause hi-box-team-payments-app-production-4100; " +
		"unpause hi-box-team-payments-app-production-4100"
	if strings.Join(engine, "; ") != want {
		t.Fatal(strings.Join(engine, "; "))
	}
	if b.stopped() {
		t.Fatal("still stopped")
	}
}

func TestTeamUndeclaredPackages(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	os.WriteFile(filepath.Join(b.repo, "bun.lock"), []byte(`{"packages": {"react": ["react@19.3.0", "", {}, "sha512-a"],},}`), 0o644)
	exec.Command("git", "-C", b.repo, "add", "-A").Run()
	exec.Command("git", "-C", b.repo, "commit", "-q", "-m", "lockfile").Run()
	saveTeamVersions("payments", teamVersions{Production: 1, Releases: []teamRelease{{Version: 1, Commit: boxGit(b.repo, "rev-parse", "HEAD")}}})
	app := filepath.Join(t.TempDir(), "app")
	write := func(path, text string) {
		os.MkdirAll(filepath.Dir(filepath.Join(app, path)), 0o755)
		os.WriteFile(filepath.Join(app, path), []byte(text), 0o644)
	}
	write("node_modules/react/package.json", `{"name": "react", "version": "19.3.0"}`)
	write("node_modules/@evil/miner/package.json", `{"name": "@evil/miner", "version": "1.0.0"}`)
	write("node_modules/react/lib/fixture/package.json", `{"name": "not-a-package", "version": "0.0.0"}`)
	write(".venv/lib/python3.12/site-packages/Requests-2.32.0.dist-info/METADATA", "")
	os.MkdirAll(filepath.Join(teamDir("payments"), "run"), 0o700)
	saveTeamSlot("payments", teamPreview{Slot: teamSlotProduction, Container: "box-prod", App: app})
	savedTools, savedEngine := teamBomTools, teamWatchEngine
	t.Cleanup(func() { teamBomTools, teamWatchEngine = savedTools, savedEngine })
	teamBomTools = teamTestBomTools()
	teamWatchEngine = func(args ...string) (string, error) {
		return "libssl3t64\t3.0.13-0ubuntu3.5\ncurl\t8.5.0-2ubuntu10.6\nnetcat\t1.0\n", nil
	}
	undeclared, err := teamUndeclared("payments")
	if err != nil {
		t.Fatal(err)
	}
	if undeclared.Count != 3 || strings.Join(undeclared.Items, " ") != "deb netcat@1.0 npm @evil/miner@1.0.0 pypi requests@2.32.0" {
		t.Fatalf("%+v", undeclared)
	}
}
