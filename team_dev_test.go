package main

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

const teamDevTestKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIGv0b7kq0Y1oXz6Yq3m4R9f2cW8u5t1pE3hJ7nL2aB9c ann@laptop"

func setupTeamDevTest(t *testing.T) teamConfig {
	t.Helper()
	dir := setupTeamTest(t)
	t.Setenv("HOME", t.TempDir()) // no sign-ins of this machine's
	folder := filepath.Join(dir, "team-payments")
	os.MkdirAll(folder, 0o700)
	if err := teamRegister("payments", folder); err != nil {
		t.Fatal(err)
	}
	config := testTeamConfig()
	if err := saveTeam(config); err != nil {
		t.Fatal(err)
	}
	return config
}

// IT admins change the owners on the machine; an owner's key is what gets
// them into the dev box, and only owners' keys do.
func TestTeamOwners(t *testing.T) {
	setupTeamDevTest(t)
	run := func(args ...string) (string, error) {
		var out bytes.Buffer
		err := teamOwnersCommand(append([]string{"payments"}, args...), strings.NewReader(""), &out)
		return out.String(), err
	}
	authorized := func() string {
		data, _ := os.ReadFile(filepath.Join(teamDevDir("payments"), "ssh", "authorized_keys"))
		return string(data)
	}
	if _, err := run("key", "U07OWNER2", teamDevTestKey); err == nil || !strings.Contains(err.Error(), "isn't an owner") {
		t.Fatal("a key for someone who isn't an owner:", err)
	}
	if out, err := run("add", "<@U07OWNER2>"); err != nil || !strings.Contains(out, "is an owner") {
		t.Fatal(out, err)
	}
	if config, _ := loadTeam("payments"); strings.Join(config.Owners, ",") != "U07OWNER1,U07OWNER2" {
		t.Fatal(config.Owners)
	}
	for _, bad := range []string{"-----BEGIN OPENSSH PRIVATE KEY-----", "hunter2"} {
		if _, err := run("key", "U07OWNER2", bad); err == nil {
			t.Fatal("took", bad)
		}
	}
	if out, err := run("key", "U07OWNER2", teamDevTestKey); err != nil || !strings.Contains(out, "ssh-ed25519 ann@laptop") {
		t.Fatal(out, err)
	}
	if !strings.Contains(authorized(), teamDevTestKey) {
		t.Fatal("the box doesn't have the key:", authorized())
	}
	// A new key replaces the old one.
	newer := strings.Replace(teamDevTestKey, "ann@laptop", "ann@desktop", 1)
	run("key", "U07OWNER2", newer)
	if got := authorized(); strings.Contains(got, "ann@laptop") || !strings.Contains(got, "ann@desktop") {
		t.Fatal(got)
	}
	if out, _ := run(); !strings.Contains(out, "U07OWNER2") || !strings.Contains(out, "ann@desktop") || !strings.Contains(out, "no SSH key") {
		t.Fatal(out)
	}

	// Removing an owner takes their key out and ends their links.
	people := teamPeopleFile{Keys: []teamPersonKey{{Key: "k", User: "U07OWNER2", Ends: farFuture()}}}
	saveTeamPeople("payments", people)
	if out, err := run("remove", "U07OWNER2"); err != nil || !strings.Contains(out, "links have ended") {
		t.Fatal(out, err)
	}
	if strings.Contains(authorized(), "ann@") || fileExists(teamDevKeyPath("payments", "U07OWNER2")) {
		t.Fatal("the key stayed")
	}
	if key := loadTeamPeople("payments").Keys[0]; key.Ends.After(time.Now()) {
		t.Fatal("the link still works")
	}
	if _, err := run("remove", "U07OWNER1"); err == nil || !strings.Contains(err.Error(), "only owner") {
		t.Fatal("removed the only owner:", err)
	}

	run("key", "U07OWNER1", teamDevTestKey)
	if out, err := run("key", "U07OWNER1", "--remove"); err != nil || strings.Contains(authorized(), "ssh-ed25519") {
		t.Fatal(out, err, authorized())
	}
}

// The service starts the dev box with its proxy, sshd and herdr, and its
// NetBird peer: with the setup key the first time, as the same peer after.
func TestTeamDevStart(t *testing.T) {
	config := setupTeamDevTest(t)
	repo := filepath.Join(teamDir("payments"), "repo")
	os.MkdirAll(repo, 0o700)
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"commit", "-q", "--allow-empty", "-m", "start"}} {
		if out, err := teamGit(repo, args...); err != nil {
			t.Fatal(out, err)
		}
	}
	os.MkdirAll(teamKeysDir("payments"), 0o700)
	os.WriteFile(filepath.Join(teamKeysDir("payments"), teamClaudeKeyFile), []byte("token"), 0o600)
	bin := t.TempDir()
	for _, name := range []string{"herdr", "claude", "netbird", "podman"} {
		os.WriteFile(filepath.Join(bin, name), nil, 0o755)
	}
	if err := setTeamDevNetBird("payments", "6F1A2B3C-4D5E-6F70-8192-A3B4C5D6E7F8"); err != nil {
		t.Fatal(err)
	}

	var calls []string
	running := map[string]bool{}
	previousCommand, previousLook := boxCommand, boxLookPath
	boxLookPath = func(name string) (string, error) {
		path := filepath.Join(bin, name)
		if !fileExists(path) {
			return "", os.ErrNotExist
		}
		return path, nil
	}
	peerStamp := ""
	boxCommand = func(_ io.Reader, stdout, _ io.Writer, _ string, args ...string) error {
		line := strings.Join(args, " ")
		calls = append(calls, line)
		if args[0] == "inspect" && strings.Contains(line, "hi.peer") {
			stdout.Write([]byte(peerStamp + "\n"))
			return nil
		}
		if args[0] == "container" && args[1] == "inspect" {
			if running[args[len(args)-1]] {
				stdout.Write([]byte("running\n"))
				return nil
			}
			return os.ErrNotExist
		}
		return nil
	}
	t.Cleanup(func() { boxCommand, boxLookPath = previousCommand, previousLook })

	hi := filepath.Join(t.TempDir(), "hi")
	os.WriteFile(hi, []byte("hi one"), 0o755)
	if err := startTeamDev(config, hi, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	state, ok := loadTeamDevState("payments")
	if !ok {
		t.Fatal("no state")
	}
	ip := teamDevBoxIP(state.ProxyIP, 3)
	find := func(prefix string) string {
		for _, call := range calls {
			if strings.HasPrefix(call, prefix) {
				return call
			}
		}
		t.Fatalf("no %q in\n%s", prefix, strings.Join(calls, "\n"))
		return ""
	}
	box := find("run -d --init --name hi-box-team-payments-dev ")
	for _, want := range []string{"--ip " + ip, "--userns=keep-id", "--cap-drop=ALL", "/dev/ssh:/box/dev/ssh:ro", "/dev/bin:/box/bin:ro", "/repo:/team/origin:ro", "/dev/repo:/box/repo",
		"-e CLAUDE_CODE_OAUTH_TOKEN=" + boxClaudePlacehold, "-e ANTHROPIC_BASE_URL=http://" + state.ProxyIP, "herdr server", "/usr/sbin/sshd -f /box/dev/ssh/sshd_config"} {
		if !strings.Contains(box, want) {
			t.Errorf("the box lacks %q: %s", want, box)
		}
	}
	proxy := find("create --name hi-box-team-payments-dev-proxy ")
	if !strings.Contains(proxy, "/secrets/team:ro") || !strings.Contains(proxy, "__proxy /state/allow /state/network.log /secrets/team/"+teamClaudeKeyFile) {
		t.Errorf("the proxy hasn't the team's Claude sign-in: %s", proxy)
	}
	peer := find("create --init --name hi-box-team-payments-dev-net ")
	for _, want := range []string{"NB_USE_NETSTACK_MODE=true", "--setup-key-file /run/netbird-key", "--hostname payments ", "hi box __forward 127.0.0.1:22 " + ip + ":2222"} {
		if !strings.Contains(peer, want) {
			t.Errorf("the peer lacks %q: %s", want, peer)
		}
	}
	find("network connect --ip " + teamDevBoxIP(state.ProxyIP, 4) + " hi-box-team-payments-dev hi-box-team-payments-dev-net")

	home := filepath.Join(teamDevDir("payments"), "home")
	if env, _ := os.ReadFile(filepath.Join(home, ".hi-env")); !strings.Contains(string(env), "export ANTHROPIC_BASE_URL=") {
		t.Fatal(string(env))
	}
	if profile, _ := os.ReadFile(filepath.Join(home, ".bashrc")); strings.Count(string(profile), ".hi-env") != 2 {
		t.Fatal(string(profile))
	}
	if !fileExists(filepath.Join(teamDevDir("payments"), "repo", ".git")) {
		t.Fatal("no clone")
	}

	// Running, and joined: a restart of the service leaves the box, and
	// the used key goes.
	running["hi-box-team-payments-dev"], running["hi-box-team-payments-dev-net"] = true, true
	os.MkdirAll(filepath.Join(teamDevDir("payments"), "netbird"), 0o700)
	os.WriteFile(filepath.Join(teamDevDir("payments"), "netbird", "default.json"), []byte("{}"), 0o600)
	// The running peer runs what this hi would.
	peerStamp = teamPeerStamp(teamPeerScript("payments", fmt.Sprintf("%s:%d", ip, teamDevSSHPort), true))
	calls = nil
	os.WriteFile(hi, []byte("hi two"), 0o755)
	if err := startTeamDev(config, hi, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	// A new hi reaches the running box.
	if data, _ := os.ReadFile(filepath.Join(teamDevDir("payments"), "bin", "hi")); string(data) != "hi two" {
		t.Fatalf("the box's hi is %q", data)
	}
	for _, call := range calls {
		if strings.HasPrefix(call, "run ") || strings.HasPrefix(call, "rm ") || strings.HasPrefix(call, "create ") {
			t.Fatal("touched a running box:", call)
		}
	}
	if fileExists(filepath.Join(teamDevDir("payments"), teamDevSetupKey)) {
		t.Fatal("the used setup key stayed")
	}
	// A new box joins as the same peer, without a key.
	running["hi-box-team-payments-dev-net"] = false
	calls = nil
	startTeamDevNet(engineForTest(), "payments", "/usr/local/bin/hi", io.Discard)
	if peer := find("create --init --name hi-box-team-payments-dev-net "); strings.Contains(peer, "setup-key") {
		t.Fatal("joined again with a key:", peer)
	}
}

func farFuture() time.Time { return time.Now().AddDate(1, 0, 0) }

func engineForTest() boxEngine { return boxEngine{name: "podman", bin: "podman"} }

// What an owner set in the box's home stays when it starts again.
func TestTeamDevHomeKeepsOwnersSettings(t *testing.T) {
	setupTeamDevTest(t)
	home := filepath.Join(teamDevDir("payments"), "home")
	os.MkdirAll(filepath.Join(home, ".claude"), 0o700)
	os.WriteFile(filepath.Join(home, ".claude", "settings.json"), []byte(`{"permissions":{"defaultMode":"acceptEdits","additionalDirectories":["/data"]},"model":"opus"}`), 0o600)
	os.WriteFile(filepath.Join(home, ".claude.json"), []byte(`{"theme":"dark"}`), 0o600)
	for i := 0; i < 2; i++ {
		if err := prepareTeamDevHome("payments", "10.234.9.2", teamDevEnv("payments", "10.234.9.2")); err != nil {
			t.Fatal(err)
		}
	}
	// ~/hi is a working directory once, beside the owners' own.
	if data, _ := os.ReadFile(filepath.Join(home, ".claude", "settings.json")); !strings.Contains(string(data), "acceptEdits") ||
		!strings.Contains(string(data), `"/data"`) || strings.Count(string(data), `"/box/home/hi"`) != 1 {
		t.Fatal(string(data))
	}
	if data, _ := os.ReadFile(filepath.Join(home, ".claude.json")); !strings.Contains(string(data), `"theme": "dark"`) || !strings.Contains(string(data), "hasCompletedOnboarding") {
		t.Fatal(string(data))
	}
	if data, _ := os.ReadFile(filepath.Join(home, ".profile")); strings.Count(string(data), teamDevSourceLine) != 1 {
		t.Fatal(string(data))
	}
	if data, _ := os.ReadFile(filepath.Join(home, ".gitconfig")); strings.Count(string(data), "[user]") != 1 || !strings.Contains(string(data), "name = payments dev box") || !strings.Contains(string(data), "email = dev-box@payments.invalid") {
		t.Fatal(string(data))
	}
	// An owner's own name stays.
	os.WriteFile(filepath.Join(home, ".gitconfig"), []byte("[user]\n\tname = An Owner\n"), 0o600)
	if err := prepareTeamDevHome("payments", "10.234.9.2", teamDevEnv("payments", "10.234.9.2")); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(filepath.Join(home, ".gitconfig")); string(data) != "[user]\n\tname = An Owner\n" {
		t.Fatal(string(data))
	}
}

// Requests to the box's own addresses skip the proxy, including 0.0.0.0,
// the URL of a server that listens on every address.
func TestTeamDevEnvSkipsProxyForLocalAddresses(t *testing.T) {
	env := teamDevEnv("payments", "10.234.9.2")
	for _, key := range []string{"NO_PROXY", "no_proxy"} {
		hosts := strings.Split(env[key], ",")
		for _, want := range []string{"localhost", "127.0.0.1", "::1", "0.0.0.0", "10.234.9.2"} {
			if !slices.Contains(hosts, want) {
				t.Fatal(key, env[key])
			}
		}
	}
}

// The dev box's NetBird identity and setup key stay out of snapshots.
func TestTeamSnapshotPathsLeaveDevSecrets(t *testing.T) {
	dir := t.TempDir()
	for _, path := range []string{"dev/home/x", "dev/repo/x", "dev/netbird/default.json", "dev/" + teamDevSetupKey, "team.json"} {
		os.MkdirAll(filepath.Dir(filepath.Join(dir, path)), 0o700)
		os.WriteFile(filepath.Join(dir, path), nil, 0o600)
	}
	paths, err := teamSnapshotPaths(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(paths, " ")
	if got != "dev/home dev/repo team.json" {
		t.Fatal(got)
	}
}

func TestTeamForward(t *testing.T) {
	target, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	go func() {
		conn, err := target.Accept()
		if err == nil {
			io.Copy(conn, conn) // echo
			conn.Close()
		}
	}()
	front, _ := net.Listen("tcp", "127.0.0.1:0")
	defer front.Close()
	go teamForward(front, target.Addr().String())
	conn, err := net.Dial("tcp", front.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.Write([]byte("hello"))
	reply := make([]byte, 5)
	if _, err := io.ReadFull(conn, reply); err != nil || string(reply) != "hello" {
		t.Fatal(string(reply), err)
	}
}
