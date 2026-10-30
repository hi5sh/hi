package main

// A team's dev box (docs/specs/approved/hi_team_dev.md, release 1): one
// long-lived box per team, where its owners build with Claude Code or
// Codex in herdr. The team's service starts it, and leaves it running
// when it restarts, so agents keep working; hi team down stops it.
//
//   - The box has its own clone of the team's repository (/box/repo),
//     with the team's repository read-only as origin: it may fetch, and
//     its commits stay in its clone until the broker takes them.
//   - Its proxy puts the team's Claude and ChatGPT sign-ins on the agents'
//     calls, so the box holds no token, as for hi agent.
//   - Owners reach it over SSH on the team's NetBird network: a small
//     NetBird peer beside the box, in NetBird's userspace mode, forwards
//     port 22 to the box's sshd, which takes the owners' keys only. The
//     box itself stays on its internal network.
//   - herdr's server, started with the box, keeps the agents and their
//     sessions; its state is in the box's home in the team's folder.

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	// teamDevSSHPort is the box's sshd; its NetBird peer takes port 22.
	teamDevSSHPort = 2222
	// teamDevSetupKey is the NetBird setup key the dev box joins with
	// once, in the team's dev folder; hi removes it once the peer exists.
	teamDevSetupKey = "netbird-setup-key"
	// teamDevMemory is the dev box's memory unless the team has its own:
	// agents, builds, and tests, which may load real data.
	teamDevMemory = "16g"
)

func teamDevDir(team string) string { return filepath.Join(teamDir(team), "dev") }

// teamDevBox is the dev box's name: its network and proxy are named after
// it, as a box's are.
func teamDevBox(team string) string       { return "team-" + team + "-dev" }
func teamDevContainer(team string) string { return "hi-box-" + teamDevBox(team) }

// teamDevHiDir is where hi dev puts what it fetches in the box.
const teamDevHiDir = "/box/home/hi"

func teamDevNetContainer(team string) string { return teamDevContainer(team) + "-net" }

// teamDevHost is the box's name on the team's NetBird network.
func teamDevHost(team string) string { return team + "-dev" }

func teamDevKeyPath(team, user string) string {
	return filepath.Join(teamDevDir(team), "keys", user+".pub")
}

// teamDevBoxIP is the box's address on its network, beside its proxy's.
func teamDevBoxIP(proxyIP string, host int) string {
	return strings.TrimSuffix(proxyIP, ".2") + fmt.Sprintf(".%d", host)
}

// ---------------------------------------------------------------------------
// the image

// teamDevContainerfile is hi's base image with an SSH server.
func teamDevContainerfile(base string) string {
	return "FROM " + base + "\n" +
		"RUN apt-get update \\\n" +
		" && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends openssh-server \\\n" +
		" && rm -rf /var/lib/apt/lists/*\n"
}

func ensureTeamDevImage(engine boxEngine, stdout, stderr io.Writer) (string, error) {
	base, err := engine.ensureBaseImage(stdout, stderr)
	if err != nil {
		return "", err
	}
	file := teamDevContainerfile(base)
	// The base image's ID too: after hi team rebuild, the tag is the same
	// but the image is new.
	id, _ := engine.output("image", "inspect", "-f", "{{.Id}}", base)
	digest := sha256.Sum256([]byte(file + "\x00" + strings.TrimSpace(id)))
	image := "localhost/hi-box-dev:" + hex.EncodeToString(digest[:])[:12]
	if engine.exists("image", image) {
		return image, nil
	}
	fmt.Fprintf(stdout, "Building the dev box image %s (once)...\n", image)
	dir, err := os.MkdirTemp("", "hi-box-dev-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	if err := os.WriteFile(filepath.Join(dir, "Containerfile"), []byte(file), 0o644); err != nil {
		return "", err
	}
	if err := engine.interactive(nil, stdout, stderr, "build", "-t", image, "-f", filepath.Join(dir, "Containerfile"), dir); err != nil {
		return "", fmt.Errorf("building the dev box image: %w", err)
	}
	return image, nil
}

// ---------------------------------------------------------------------------
// SSH

const teamDevSSHDConfig = `# Written by hi on each start of the dev box.
Port 2222
HostKey /box/dev/ssh/host_ed25519
AuthorizedKeysFile /box/dev/ssh/authorized_keys
AllowUsers box
PasswordAuthentication no
KbdInteractiveAuthentication no
UsePAM no
StrictModes no
PermitUserEnvironment no
PrintMotd no
PidFile /box/home/.sshd.pid
Subsystem sftp internal-sftp
`

// writeTeamDevSSH writes what the box's sshd reads: its settings, and the
// keys of the team's owners, without those of people who no longer are.
func writeTeamDevSSH(config teamConfig) error {
	dir := filepath.Join(teamDevDir(config.Name), "ssh")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := writeFileAtomic(filepath.Join(dir, "sshd_config"), []byte(teamDevSSHDConfig), 0o600); err != nil {
		return err
	}
	var lines []string
	for _, builder := range slices.Concat(config.Owners, config.Developers) { // Vibe Devs have no SSH
		if data, err := os.ReadFile(teamDevKeyPath(config.Name, builder)); err == nil {
			lines = append(lines, strings.TrimSpace(string(data)))
		}
	}
	text := ""
	if len(lines) > 0 {
		text = strings.Join(lines, "\n") + "\n"
	}
	return writeFileAtomic(filepath.Join(dir, "authorized_keys"), []byte(text), 0o600)
}

// teamDevHostKey makes the box's SSH host key once, with the image's
// ssh-keygen; hi team dev <team> new-host-key makes a new one.
func teamDevHostKey(engine boxEngine, team, image string) error {
	dir := filepath.Join(teamDevDir(team), "ssh")
	if fileExists(filepath.Join(dir, "host_ed25519")) {
		return nil
	}
	args := append([]string{"run", "--rm"}, engine.userArgs()...)
	args = append(args, "-v", dir+":/out", image, "ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", "/out/host_ed25519", "-C", teamDevHost(team))
	if out, err := engine.output(args...); err != nil {
		return fmt.Errorf("the dev box's SSH host key: %s", qFirstLine(out, err.Error()))
	}
	return nil
}

// teamSSHKeyPattern is one OpenSSH public key on a line.
var teamSSHKeyPattern = regexp.MustCompile(`^(ssh-ed25519|ssh-rsa|ecdsa-sha2-nistp(256|384|521)|sk-ssh-ed25519@openssh\.com|sk-ecdsa-sha2-nistp256@openssh\.com) [A-Za-z0-9+/]+=*( [^\r\n]*)?$`)

// ---------------------------------------------------------------------------
// the box's home and repository

// teamDevEnv is the agents' environment: the box's proxy, and the
// placeholders it swaps for the team's sign-ins. herdr's server gets it
// from the box; an SSH login gets it from ~/.hi-env.
func teamDevEnv(team, proxyIP string) map[string]string {
	proxy := fmt.Sprintf("http://%s:%d", proxyIP, boxProxyPort)
	noProxy := boxNoProxy + "," + proxyIP
	return map[string]string{
		"HTTPS_PROXY": proxy, "HTTP_PROXY": proxy, "https_proxy": proxy, "http_proxy": proxy,
		"NO_PROXY": noProxy, "no_proxy": noProxy,
		"HOME": "/box/home", "HI_BOX": teamDevBox(team), "HI_TEAM": team,
		"PATH": "/box/bin:/opt/codex/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		// Claude Code, on the team's sign-in through the proxy.
		"CLAUDE_CODE_OAUTH_TOKEN": boxClaudePlacehold,
		"ANTHROPIC_BASE_URL":      fmt.Sprintf("http://%s:%d", proxyIP, boxInjectPort),
		"DISABLE_AUTOUPDATER":     "1",
		"CLAUDE_CODE_TMPDIR":      "/box/home/.tmp",
		// hi dev reaches the team's broker here (team_devwork.go).
		teamDevSocketEnv: teamDevSocketInBox,
		// Codex, on the team's ChatGPT sign-in through the proxy.
		codexBoxKeyEnv: boxClaudePlacehold,
	}
}

// teamDevSettings are the dev box's own settings, values and files, as
// its environment: a file's variable holds its path in the box.
func teamDevSettings(team string) map[string]string {
	secrets, err := loadTeamSecrets(team)
	if err != nil {
		return nil
	}
	env := maps.Clone(secrets.EnvDev)
	if env == nil {
		env = map[string]string{}
	}
	for key := range secrets.FilesDev {
		env[key] = teamAppFilesDir + "/" + key
	}
	return env
}

// teamDevEnvFile is ~/.hi-env, which an SSH login's shell reads.
func teamDevEnvFile(env map[string]string) string {
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var text strings.Builder
	text.WriteString("# Written by hi on each start of the dev box: the agents reach their\n# models and the internet through the box's proxy.\n")
	for _, key := range keys {
		fmt.Fprintf(&text, "export %s=%s\n", key, shellQuote(env[key]))
	}
	return text.String()
}

const teamDevSourceLine = `[ -f "$HOME/.hi-env" ] && . "$HOME/.hi-env"`

// prepareTeamDevHome sets the box's home up for the agents: their
// sign-ins through the proxy, and permissions skipped, since the box is
// the boundary. What an owner set themselves stays.
func prepareTeamDevHome(team, proxyIP string, env map[string]string) error {
	home := filepath.Join(teamDevDir(team), "home")
	for _, dir := range []string{".tmp", ".claude", ".codex"} {
		if err := os.MkdirAll(filepath.Join(home, dir), 0o700); err != nil {
			return err
		}
	}
	if err := writeFileAtomic(filepath.Join(home, ".hi-env"), []byte(teamDevEnvFile(env)), 0o600); err != nil {
		return err
	}
	for _, name := range []string{".bashrc", ".profile"} {
		path := filepath.Join(home, name)
		data, _ := os.ReadFile(path)
		if !strings.Contains(string(data), teamDevSourceLine) {
			text := string(data)
			if text != "" && !strings.HasSuffix(text, "\n") {
				text += "\n"
			}
			if err := os.WriteFile(path, []byte(text+teamDevSourceLine+"\n"), 0o600); err != nil {
				return err
			}
		}
	}
	if err := teamDevGitIdentity(home, team); err != nil {
		return err
	}
	// Claude Code: no first-run questions, and no permission prompts.
	state := map[string]any{}
	if data, err := os.ReadFile(filepath.Join(home, ".claude.json")); err == nil {
		json.Unmarshal(data, &state)
	}
	state["hasCompletedOnboarding"], state["bypassPermissionsModeAccepted"] = true, true
	projects, _ := state["projects"].(map[string]any)
	if projects == nil {
		projects = map[string]any{}
	}
	repo, _ := projects["/box/repo"].(map[string]any)
	if repo == nil {
		repo = map[string]any{}
	}
	repo["hasTrustDialogAccepted"] = true
	projects["/box/repo"], state["projects"] = repo, projects
	data, _ := json.MarshalIndent(state, "", "  ")
	if err := writeFileAtomic(filepath.Join(home, ".claude.json"), data, 0o600); err != nil {
		return err
	}
	if err := teamDevClaudeSettings(home); err != nil {
		return err
	}
	// Codex: ChatGPT through the proxy, no approvals, no sandbox of its own.
	if teamCodexViaProxy(team) {
		codex := "# Written by hi on each start of the dev box.\napproval_policy = \"never\"\nsandbox_mode = \"danger-full-access\"\n" + codexBoxConfig(proxyIP)
		if err := writeFileAtomic(filepath.Join(home, ".codex", "config.toml"), []byte(codex), 0o600); err != nil {
			return err
		}
	}
	return nil
}

// teamDevClaudeSettings puts what hi needs in Claude Code's settings in the
// box, beside the owners' own: on each start of the team's service, so a
// running box gets them too.
func teamDevClaudeSettings(home string) error {
	settingsPath := filepath.Join(home, ".claude", "settings.json")
	settings := map[string]any{}
	if data, err := os.ReadFile(settingsPath); err == nil {
		json.Unmarshal(data, &settings)
	}
	permissions, _ := settings["permissions"].(map[string]any)
	if permissions == nil {
		permissions = map[string]any{}
	}
	changed := false
	if _, set := permissions["defaultMode"]; !set {
		permissions["defaultMode"] = "bypassPermissions"
		changed = true
	}
	// ~/hi is where hi dev puts what the agent reads (a ticket's files, a
	// review): a working directory of every session, so a mode that asks,
	// such as auto, doesn't ask to read there. The owners' own stay.
	extra, _ := permissions["additionalDirectories"].([]any)
	if !slices.Contains(extra, any(teamDevHiDir)) {
		permissions["additionalDirectories"] = append(extra, teamDevHiDir)
		changed = true
	}
	// Opus, unless an owner chose another with /model. Claude Code in the
	// box sees a placeholder for the team's sign-in, not its plan, so it
	// takes the API's default, Sonnet.
	if _, set := settings["model"]; !set {
		settings["model"] = "opus"
		changed = true
	}
	// No session recap, unless an owner turned it back on in /config: the
	// chat and the channel say what happened while you were away.
	if _, set := settings["awaySummaryEnabled"]; !set {
		settings["awaySummaryEnabled"] = false
		changed = true
	}
	if changed {
		settings["permissions"] = permissions
		data, _ := json.MarshalIndent(settings, "", "  ")
		if err := writeFileAtomic(settingsPath, data, 0o600); err != nil {
			return err
		}
	}
	return nil
}

// prepareTeamDevRepo clones the team's repository for the box once; its
// origin is the team's repository, read-only in the box.
func prepareTeamDevRepo(team string) error {
	repo := filepath.Join(teamDevDir(team), "repo")
	if fileExists(filepath.Join(repo, ".git")) {
		return nil
	}
	if err := os.MkdirAll(teamDevDir(team), 0o700); err != nil {
		return err
	}
	if out, err := teamGit(teamDevDir(team), "clone", "-q", filepath.Join(teamDir(team), "repo"), repo); err != nil {
		return fmt.Errorf("cloning the team's repository for the dev box: %s", qFirstLine(out, err.Error()))
	}
	if out, err := teamGit(repo, "remote", "set-url", "origin", "/team/origin"); err != nil {
		return fmt.Errorf("the dev box's clone: %s", qFirstLine(out, err.Error()))
	}
	return nil
}

// ---------------------------------------------------------------------------
// the proxy

// teamDevProxyArgs gives the dev box's proxy the team's Claude sign-in
// (or this machine's), and its ChatGPT one when Codex goes through the
// proxy, with OpenRouter when either refuses.
func teamDevProxyArgs(team string, args []string) ([]string, string, error) {
	keys, events := teamKeysDir(team), teamEventsDir(team)
	for _, dir := range []string{keys, events} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return args, "", err
		}
	}
	args = append(args, "-v", keys+":/secrets/team:ro", "-v", events+":/events")
	var secrets []string
	if fileExists(filepath.Join(keys, teamClaudeKeyFile)) {
		secrets = append(secrets, "/secrets/team/"+teamClaudeKeyFile)
	}
	if source, err := boxClaudeSecret(); err == nil {
		args = append(args, "-v", source+":/secrets/claude:ro")
		secrets = append(secrets, "/secrets/claude")
	}
	if len(secrets) == 0 {
		return args, "", fmt.Errorf("Claude Code is signed in neither for team %s nor on this machine; hi team signin %s claude", team, team)
	}
	fallback := "/secrets/team/openrouter.env"
	if fileExists(hermesEnvPath()) {
		args = append(args, "-v", hermesEnvPath()+":/secrets/hermes.env:ro")
		fallback += ",/secrets/hermes.env"
	}
	args = append(args, "-e", boxFallbackEnv+"="+fallback, "-e", boxRoleEnv+"=dev", "-e", boxEventsEnv+"=/events/"+teamDevBox(team)+".log",
		"-e", boxPublicOnlyEnv+"=1", "-e", boxAskEnv+"="+teamDevBox(team))
	if teamCodexViaProxy(team) {
		if err := prepareTeamChatGPT(team, time.Now()); err == nil || fileExists(filepath.Join(keys, teamChatGPTKeyFile)) {
			args = append(args, "-e", boxChatGPTEnv+"=/secrets/team/"+teamChatGPTKeyFile)
		}
	}
	return args, strings.Join(secrets, ","), nil
}

// teamDevHosts is the dev box's allowlist: what development needs,
// Codex's sign-in hosts, and Open VSX for VS Code's extensions.
func teamDevHosts() []string {
	return append(append(boxPresetHosts("dev"), agentHosts["codex"]...), teamDevCodeHosts...)
}

// ---------------------------------------------------------------------------
// starting and stopping

// startTeamDev starts the dev box, and its NetBird peer, unless they run;
// a running box only gets the owners' keys as they are now. Tests replace
// it.
var startTeamDev = func(config teamConfig, hi string, stdout, stderr io.Writer) error {
	engine, _, err := detectBoxEngine()
	if err != nil {
		return err
	}
	name := config.Name
	// The service and hi team dev may start it at the same moment.
	unlock, err := teamDevLock(name)
	if err != nil {
		return err
	}
	defer unlock()
	if err := writeTeamDevSSH(config); err != nil {
		return err
	}
	// The box runs hi from its folder, so a new hi reaches a running box.
	if err := copyTeamDevHi(name, hi); err != nil {
		return err
	}
	if engine.state(teamDevContainer(name)) == "running" {
		// A new hi brings its skills without a new box.
		if err := installTeamDevKit(config, filepath.Join(teamDevDir(name), "home")); err != nil {
			fmt.Fprintf(stderr, "hi: the dev box's skills: %v\n", err)
		}
		if err := teamDevClaudeSettings(filepath.Join(teamDevDir(name), "home")); err != nil {
			fmt.Fprintf(stderr, "hi: the dev box's Claude Code settings: %v\n", err)
		}
		return startTeamDevNet(engine, name, hi, stderr)
	}
	herdr, err := boxHostBinary("herdr")
	if err != nil {
		return errors.New("herdr isn't installed on this machine, so the team has no dev box; install it from herdr.dev, then hi team dev " + name + " restart")
	}
	claude, err := boxHostBinary("claude")
	if err != nil {
		return errors.New("Claude Code isn't installed on this machine, so the team has no dev box; hi install claude, then hi team dev " + name + " restart")
	}
	// What a box that stopped left behind.
	engine.output("rm", "-f", teamDevNetContainer(name))
	teardownBox(engine, boxMeta{Name: teamDevBox(name)})

	dev := teamDevDir(name)
	netDir := filepath.Join(dev, "net")
	for _, dir := range []string{filepath.Join(dev, "home"), netDir, teamDevSocketDir(name)} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	image, err := ensureTeamDevImage(engine, stdout, stderr)
	if err != nil {
		return err
	}
	if err := teamDevHostKey(engine, name, image); err != nil {
		return err
	}
	if err := prepareTeamDevRepo(name); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(netDir, "allow"), []byte(strings.Join(teamDevAllowHosts(name), "\n")+"\n"), 0o600); err != nil {
		return err
	}
	meta := boxMeta{Name: teamDevBox(name), Agent: "claude", Team: name + ":dev", Engine: engine.name, Network: "dev", Image: image, Created: time.Now().UTC()}
	if err := startBoxNetwork(engine, &meta, netDir, "dev:"+name, nil); err != nil {
		teardownBox(engine, meta)
		return err
	}
	env := teamDevEnv(name, meta.ProxyIP)
	// The dev box's own settings (hi team env --dev); hi's own come first.
	settings := teamDevSettings(name)
	for key := range settings {
		if _, ok := env[key]; ok {
			delete(settings, key)
		}
	}
	files, err := writeTeamAppFiles(name, teamSlotDev)
	if err != nil {
		teardownBox(engine, meta)
		return err
	}
	login := maps.Clone(env)
	maps.Copy(login, settings)
	if err := prepareTeamDevHome(name, meta.ProxyIP, login); err != nil {
		teardownBox(engine, meta)
		return err
	}
	if err := installTeamDevKit(config, filepath.Join(dev, "home")); err != nil {
		teardownBox(engine, meta)
		return fmt.Errorf("the dev box's skills: %w", err)
	}
	run := []string{"run", "-d", "--init", "--name", teamDevContainer(name), "--hostname", teamDevHost(name),
		"--network", "hi-box-" + meta.Name, "--ip", teamDevBoxIP(meta.ProxyIP, 3), "--dns", meta.ProxyIP}
	run = append(run, engine.userArgs()...)
	run = append(run, boxHardening(teamBoxMemory(config, teamBoxDev))...)
	run = append(run,
		"-v", filepath.Join(dev, "home")+":/box/home",
		"-v", filepath.Join(dev, "repo")+":/box/repo",
		"-v", filepath.Join(teamDir(name), "repo")+":/team/origin:ro",
		"-v", filepath.Join(dev, "ssh")+":/box/dev/ssh:ro",
		"-v", teamDevSocketDir(name)+":/box/run",
		"-v", claude+":/usr/local/bin/claude:ro",
		"-v", herdr+":/usr/local/bin/herdr:ro",
		"-v", filepath.Join(dev, "bin")+":/box/bin:ro",
		"-v", files+":"+teamAppFilesDir+":ro",
		"-w", "/box/repo")
	if codex, err := boxHostBinary("codex"); err == nil {
		run = append(run, "-v", filepath.Dir(filepath.Dir(codex))+":/opt/codex:ro")
	}
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		run = append(run, "-e", key+"="+env[key])
	}
	// The owners' settings from a file, off the engine's command line,
	// where anyone on the machine can see them.
	envFile, err := writeTeamAppEnvFile(name, settings)
	if err != nil {
		teardownBox(engine, meta)
		return err
	}
	defer os.Remove(envFile)
	run = append(run, "--env-file", envFile)
	// sshd for the owners, then herdr's server, which keeps the agents.
	run = append(run, image, "sh", "-c", "/usr/sbin/sshd -f /box/dev/ssh/sshd_config -E /box/home/.sshd.log && exec herdr server")
	if out, err := engine.output(run...); err != nil {
		teardownBox(engine, meta)
		return fmt.Errorf("starting the dev box: %s", qFirstLine(out, err.Error()))
	}
	if err := saveTeamDevState(name, teamDevState{ProxyIP: meta.ProxyIP, Image: image, Started: meta.Created}); err != nil {
		return err
	}
	return startTeamDevNet(engine, name, hi, stderr)
}

// teamDevLock holds the dev box's lock until the returned func.
func teamDevLock(team string) (func(), error) {
	if err := os.MkdirAll(teamDevDir(team), 0o700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(filepath.Join(teamDevDir(team), ".lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		file.Close()
		return nil, err
	}
	return func() { syscall.Flock(int(file.Fd()), syscall.LOCK_UN); file.Close() }, nil
}

// teamDevJoined waits up to wait for the team's peers to have joined
// NetBird, then removes the setup key they used; it says whether they all
// did.
func teamDevJoined(team string, wait time.Duration) bool {
	deadline := time.Now().Add(wait)
	for {
		if teamPeerJoined(team) {
			os.Remove(filepath.Join(teamDevDir(team), teamDevSetupKey))
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(teamDevJoinPoll)
	}
}

// teamDevJoinPoll is how often hi looks for the peer; tests shorten it.
var teamDevJoinPoll = 2 * time.Second

// teamDevState is what hi knows of the running dev box, in dev/state.json.
type teamDevState struct {
	ProxyIP string    `json:"proxy_ip"`
	Image   string    `json:"image"`
	Started time.Time `json:"started"`
}

func saveTeamDevState(team string, state teamDevState) error {
	data, _ := json.MarshalIndent(state, "", "  ")
	return writeFileAtomic(filepath.Join(teamDevDir(team), "state.json"), append(data, '\n'), 0o600)
}

func loadTeamDevState(team string) (teamDevState, bool) {
	var state teamDevState
	data, err := os.ReadFile(filepath.Join(teamDevDir(team), "state.json"))
	if err != nil || json.Unmarshal(data, &state) != nil || state.ProxyIP == "" {
		return state, false
	}
	return state, true
}

// startTeamDevNet starts the dev box's NetBird peer, unless it runs: it
// joins with the setup key the first time, and is the same peer after
// that, from the identity in dev/netbird. Without either, or without
// NetBird on this machine, the box has no peer yet.
func startTeamDevNet(engine boxEngine, team, hi string, stderr io.Writer) error {
	dev := teamDevDir(team)
	identityDir, keyFile := filepath.Join(dev, "netbird"), filepath.Join(dev, teamDevSetupKey)
	joined := fileExists(filepath.Join(identityDir, "default.json"))
	if joined {
		os.Remove(keyFile) // used: the peer exists
	}
	if !joined && !fileExists(keyFile) {
		return nil
	}
	netbird, err := boxHostBinary("netbird")
	if err != nil {
		return errors.New("NetBird isn't installed on this machine, so the dev box isn't on the team's network; hi install netbird")
	}
	state, ok := loadTeamDevState(team)
	if !ok {
		return errors.New("the dev box hasn't started yet")
	}
	// NetBird's userspace stack hands what reaches the peer's port 22 to
	// this container's loopback, and hi passes it on to the box's sshd;
	// port 80 goes to the dev page's internal relay.
	script := teamPeerScript(team, fmt.Sprintf("%s:%d", teamDevBoxIP(state.ProxyIP, 3), teamDevSSHPort), joined)
	if engine.state(teamDevNetContainer(team)) == "running" && teamContainerLabel(engine, teamDevNetContainer(team), "hi.peer") == teamPeerStamp(script) {
		go teamRefreshPeerName(engine, team, time.Minute)
		return nil
	}
	if !joined {
		// The name it joins with stays its name for hi.
		writeFileAtomic(filepath.Join(dev, "netbird-host"), []byte(teamPeerHostname(team)+"\n"), 0o600)
	}
	if err := os.MkdirAll(identityDir, 0o700); err != nil {
		return err
	}
	if err := os.MkdirAll(teamInternalDir(team), 0o700); err != nil {
		return err
	}
	engine.output("rm", "-f", teamDevNetContainer(team))
	args := teamPeerArgs(engine, team, netbird, hi, script, joined)
	if out, err := engine.output(args...); err != nil {
		return fmt.Errorf("the dev box's NetBird peer: %s", qFirstLine(out, err.Error()))
	}
	if out, err := engine.output("network", "connect", "--ip", teamDevBoxIP(state.ProxyIP, 4), "hi-box-"+teamDevBox(team), teamDevNetContainer(team)); err != nil {
		return fmt.Errorf("the dev box's NetBird peer: %s", qFirstLine(out, err.Error()))
	}
	if out, err := engine.output("start", teamDevNetContainer(team)); err != nil {
		return fmt.Errorf("the dev box's NetBird peer: %s", qFirstLine(out, err.Error()))
	}
	if !joined {
		// The key goes as soon as the peer exists.
		go teamDevJoined(team, 5*time.Minute)
	}
	go teamRefreshPeerName(engine, team, 5*time.Minute)
	return nil
}

// stopTeamDev removes the dev box, its peer, its proxy, and its network;
// what it keeps is in the team's folder.
func stopTeamDev(engine boxEngine, team string) {
	engine.output("rm", "-f", teamDevNetContainer(team))
	teardownBox(engine, boxMeta{Name: teamDevBox(team)})
}

// runBoxForward is `hi box __forward <listen> <target>`: TCP from one
// address to another, for the dev box's NetBird peer.
func runBoxForward(args []string, stderr io.Writer) int {
	if len(args) != 2 {
		fmt.Fprintln(stderr, "usage: hi box __forward <listen address> <target address>")
		return 2
	}
	listener, err := net.Listen("tcp", args[0])
	if err != nil {
		fmt.Fprintf(stderr, "hi: %v\n", err)
		return 1
	}
	err = teamForward(listener, args[1])
	fmt.Fprintf(stderr, "hi: %v\n", err)
	return 1
}

// teamForward passes each connection to listener on to target, until the
// listener fails.
func teamForward(listener net.Listener, to string) error {
	for {
		client, err := listener.Accept()
		if err != nil {
			return err
		}
		go func() {
			defer client.Close()
			target, err := teamDialTarget(to)
			if err != nil {
				return
			}
			defer target.Close()
			done := make(chan struct{}, 2)
			go func() { io.Copy(target, client); done <- struct{}{} }()
			go func() { io.Copy(client, target); done <- struct{}{} }()
			<-done
		}()
	}
}

// ---------------------------------------------------------------------------
// hi team dev

func teamDevCommand(args []string, stdin io.Reader, stdout io.Writer) error {
	usage := usageError{"usage: hi team dev <team> [status | start <task> [--agent claude|codex] | finish <branch> [--discard] | restart | netbird [<setup key file>] | new-host-key]"}
	if len(args) == 0 {
		return usage
	}
	config, err := loadTeam(args[0])
	if err != nil {
		return err
	}
	action := "status"
	if len(args) > 1 {
		action = args[1]
	}
	hi, err := teamExecutable()
	if err != nil {
		return err
	}
	switch {
	case action == "status" && len(args) <= 2:
		return teamDevStatus(config, stdout)
	case action == "start" && (len(args) == 3 || len(args) == 5 && args[3] == "--agent"):
		number, err := strconv.Atoi(strings.TrimPrefix(args[2], "#"))
		if err != nil || number <= 0 {
			return usageError{"usage: hi team dev <team> start <task> [--agent claude|codex]"}
		}
		agent := ""
		if len(args) == 5 {
			agent = args[4]
		}
		branch, err := teamDevStart(config, number, agent, firstNonEmpty(os.Getenv("USER"), "an admin")+" on the machine")
		if branch != "" {
			fmt.Fprintf(stdout, "Task %d has its branch, worktree, and herdr workspace in the dev box: %s.\n", number, branch)
		}
		return err
	case action == "finish" && (len(args) == 3 || len(args) == 4 && args[3] == "--discard"):
		text, err := teamDevFinish(config, args[2], firstNonEmpty(os.Getenv("USER"), "an admin")+" on the machine", len(args) == 4)
		if err != nil {
			return err
		}
		fmt.Fprintln(stdout, text)
		return nil
	case action == "allow":
		return teamDevAllowAdmin(config.Name, args[2:], stdout)
	case action == "restart" && len(args) == 2:
		engine, _, err := detectBoxEngine()
		if err != nil {
			return err
		}
		fmt.Fprintln(stdout, "Restarting the dev box: agents that are working stop; herdr brings their conversations back.")
		stopTeamDev(engine, config.Name)
		if err := startTeamDev(config, hi, stdout, stdout); err != nil {
			return err
		}
		return teamDevStatus(config, stdout)
	case action == "netbird" && len(args) <= 3:
		key := ""
		if len(args) == 3 {
			data, err := os.ReadFile(args[2])
			if err != nil {
				return err
			}
			key = strings.TrimSpace(string(data))
		} else {
			fmt.Fprint(stdout, teamDevNetBirdHelp(config.Name))
			if key, err = teamAskSecret(stdin, stdout, "NetBird setup key"); err != nil {
				return err
			}
		}
		if err := setTeamDevNetBird(config.Name, key); err != nil {
			return err
		}
		engine, _, err := detectBoxEngine()
		if err != nil {
			return err
		}
		engine.output("rm", "-f", teamDevNetContainer(config.Name))
		if err := startTeamDev(config, hi, stdout, stdout); err != nil {
			return err
		}
		if teamDevJoined(config.Name, time.Minute) {
			fmt.Fprintf(stdout, "The team is on its NetBird network as %s; hi has removed the setup key it used.\n", teamPeerHostname(config.Name))
		} else {
			fmt.Fprintf(stdout, "The team is joining its NetBird network as %s; hi team dev %s shows when it has.\n", teamPeerHostname(config.Name), config.Name)
		}
		return nil
	case action == "new-host-key" && len(args) == 2:
		os.Remove(filepath.Join(teamDevDir(config.Name), "ssh", "host_ed25519"))
		os.Remove(filepath.Join(teamDevDir(config.Name), "ssh", "host_ed25519.pub"))
		engine, _, err := detectBoxEngine()
		if err != nil {
			return err
		}
		stopTeamDev(engine, config.Name)
		if err := startTeamDev(config, hi, stdout, stdout); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "The dev box has a new SSH host key. Owners' ssh warns once that it changed; they remove the old one with:\n  ssh-keygen -R %s\n", teamPeerHostname(config.Name))
		return nil
	}
	return usage
}

// teamExecutable is hi's own file, as boxes mount it.
func teamExecutable() (string, error) {
	hi, err := os.Executable()
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(hi); err == nil {
		hi = resolved
	}
	return hi, nil
}

// setTeamDevNetBird keeps a setup key for the dev box to join with, and
// forgets the peer it had, so it joins again with this one.
func setTeamDevNetBird(team, key string) error {
	if !regexp.MustCompile(`^[A-Za-z0-9-]{20,100}$`).MatchString(key) {
		return errors.New("that isn't a NetBird setup key: copy it from NetBird's dashboard, Add Peer → Server")
	}
	dev := teamDevDir(team)
	if err := os.MkdirAll(dev, 0o700); err != nil {
		return err
	}
	// Peers that have joined stay as they are; the key is for the rest.
	return writeFileAtomic(filepath.Join(dev, teamDevSetupKey), []byte(key+"\n"), 0o600)
}

func teamDevNetBirdHelp(team string) string {
	return fmt.Sprintf(`The team joins its NetBird network as one peer, %[1]s: production on port 80, the preview on 8080, the dev page on 8888, and SSH to the dev box on 22.
In NetBird's dashboard:
  1. Add Peer → Server: a one-off key (not ephemeral), with an auto-group such as hi-%[2]s.
  2. Access Control: a policy from the people's group to hi-%[2]s, TCP ports 80 and 8080, and one from the owners' group, TCP 8888 and 22.
hi uses the key once; after that the peer stays the same, also when it is replaced.
`, teamPeerHostname(team), team)
}

func teamDevStatus(config teamConfig, stdout io.Writer) error {
	engine, _, err := detectBoxEngine()
	if err != nil {
		return err
	}
	name := config.Name
	box := firstNonEmpty(engine.state(teamDevContainer(name)), "not started")
	fmt.Fprintf(stdout, "Dev box of team %s: %s\n", name, box)
	switch {
	case engine.state(teamDevNetContainer(name)) == "running":
		address := ""
		if out, err := engine.output("exec", teamDevNetContainer(name), "netbird", "status"); err == nil {
			for _, line := range strings.Split(out, "\n") {
				if value, ok := strings.CutPrefix(strings.TrimSpace(line), "FQDN:"); ok {
					address = strings.TrimSpace(value)
				}
			}
		}
		fmt.Fprintf(stdout, "NetBird: %s\n", firstNonEmpty(address, teamPeerHostname(name)+" (joining)"))
	case fileExists(filepath.Join(teamDevDir(name), teamDevSetupKey)) || fileExists(filepath.Join(teamDevDir(name), "netbird", "default.json")):
		fmt.Fprintln(stdout, "NetBird: not running")
	default:
		fmt.Fprintf(stdout, "NetBird: not on the team's network yet; hi team dev %s netbird\n", name)
	}
	fmt.Fprintln(stdout, "Owners:")
	for _, owner := range config.Owners {
		fmt.Fprintf(stdout, "  %-12s %s\n", owner, teamDevKeyLabel(name, owner))
	}
	if len(config.Developers) > 0 {
		fmt.Fprintln(stdout, "Pro Devs:")
		for _, developer := range config.Developers {
			fmt.Fprintf(stdout, "  %-12s %s\n", developer, teamDevKeyLabel(name, developer))
		}
	}
	fmt.Fprintf(stdout, "Get in with:\n  ssh box@%s          then: herdr\n  herdr --remote box@%s\n", teamPeerHostname(name), teamPeerHostname(name))
	return nil
}

// teamSSHKeyLabel names a key by its type and comment.
func teamSSHKeyLabel(key string) string {
	fields := strings.Fields(key)
	switch {
	case len(fields) >= 3:
		return fields[0] + " " + strings.Join(fields[2:], " ")
	case len(fields) > 0:
		return fields[0]
	}
	return "?"
}

// ---------------------------------------------------------------------------
// hi team owners

func teamOwnersCommand(args []string, stdin io.Reader, stdout io.Writer) error {
	usage := usageError{"usage: hi team owners <team> [add <member ID> | remove <member ID> | key <member ID> <public key> | key <member ID> --remove]"}
	if len(args) == 0 {
		return usage
	}
	config, err := loadTeam(args[0])
	if err != nil {
		return err
	}
	if len(args) == 1 {
		fmt.Fprintf(stdout, "Owners of team %s, who build in its dev box:\n", config.Name)
		names := teamPeopleNames(config, config.Owners)
		for _, owner := range config.Owners {
			key := "no SSH key"
			if data, err := os.ReadFile(teamDevKeyPath(config.Name, owner)); err == nil {
				key = teamSSHKeyLabel(string(data))
			}
			fmt.Fprintf(stdout, "  %s %s\n", teamPersonColumn(owner, names), key)
		}
		return nil
	}
	if len(args) < 3 {
		return usage
	}
	action, user := args[1], strings.Trim(args[2], "<@>")
	if err := teamCheckPeople(user); err != nil {
		return err
	}
	switch {
	case action == "add" && len(args) == 3:
		if containsString(config.Owners, user) {
			fmt.Fprintf(stdout, "%s is an owner of team %s already.\n", user, config.Name)
			return nil
		}
		// An owner is an owner only: a level they had goes.
		without := func(list []string) []string {
			return slices.DeleteFunc(slices.Clone(list), func(id string) bool { return id == user })
		}
		config.Owners = append(config.Owners, user)
		config.Developers, config.VibeDevelopers, config.Contributors = without(config.Developers), without(config.VibeDevelopers), without(config.Contributors)
		if err := saveTeam(config); err != nil {
			return err
		}
		who := user
		if name := teamPeopleNames(config, []string{user})[user]; name != "" {
			who += " (" + name + ")"
		}
		fmt.Fprintf(stdout, "%s is an owner of team %s. For the dev box they give a public SSH key:\n  hi team owners %s key %s \"$(cat ~/.ssh/id_ed25519.pub)\"\n", who, config.Name, config.Name, user)
	case action == "remove" && len(args) == 3:
		if !containsString(config.Owners, user) {
			return fmt.Errorf("%s isn't an owner of team %s", user, config.Name)
		}
		if len(config.Owners) == 1 {
			return fmt.Errorf("%s is team %s's only owner; add another first", user, config.Name)
		}
		var kept []string
		for _, owner := range config.Owners {
			if owner != user {
				kept = append(kept, owner)
			}
		}
		config.Owners = kept
		if err := saveTeam(config); err != nil {
			return err
		}
		os.Remove(teamDevKeyPath(config.Name, user))
		ended := teamEndLinks(config.Name, user)
		fmt.Fprintf(stdout, "%s is no longer an owner of team %s: their SSH key is out of the dev box", user, config.Name)
		if ended > 0 {
			fmt.Fprint(stdout, ", and their private links have ended")
		}
		fmt.Fprintln(stdout, ".")
	case action == "key" && len(args) == 4 && args[3] == "--remove":
		if err := teamSetDevKey(config, user, ""); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "%s's SSH key is out of team %s's dev box.\n", user, config.Name)
	case action == "key" && len(args) >= 4:
		key := strings.TrimSpace(strings.Join(args[3:], " "))
		if data, err := os.ReadFile(key); err == nil {
			key = strings.TrimSpace(string(data)) // a file with the key
		}
		if err := teamSetDevKey(config, user, key); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "%s's SSH key for team %s's dev box is %s; a key they had before no longer works.\n", user, config.Name, teamSSHKeyLabel(key))
	default:
		return usage
	}
	// The box reads the keys as they are now.
	if err := writeTeamDevSSH(config); err != nil {
		return err
	}
	return nil
}

// teamEndLinks ends someone's private links to the team's app, and says
// how many it ended.
func teamEndLinks(team, user string) int {
	people := loadTeamPeople(team)
	now, ended := time.Now().UTC(), 0
	for i, key := range people.Keys {
		if key.User == user && now.Before(key.Ends) {
			people.Keys[i].Ends = now
			ended++
		}
	}
	if ended > 0 {
		saveTeamPeople(team, people)
	}
	return ended
}

// ---------------------------------------------------------------------------
// the dev kit

// teamDevSkills are the skills for hi's teams, installed for each agent in
// the dev box (skills/hi/team).
//
//go:embed skills/hi/team
var teamDevSkills embed.FS

// teamDevInstructions is what the agents in the dev box are told of it,
// in a block of their user-level instructions that hi keeps current.
const teamDevInstructions = `<!-- Written by hi on each start of the dev box: changes here are replaced. -->
# The %[1]s team's dev box

You run in the %[1]s team's dev box, a container on the team's machine.
The people who use you are the team's owners: its developers.

- **The project** is a clone in /box/repo. Each piece of work gets its
  own branch and worktree: herdr worktree create --cwd /box/repo --branch <name>.
  Commit there. origin is the team's repository, read-only: there is no
  push; work leaves the box with hi dev (the hi-dev-ship skill).
- **Dev and production:** hi dev preview puts a branch on the team's
  preview, with production's data; hi dev ship merges it into dev (%[2]s).
  Only an owner releases dev to production.
- **The team:** hi dev status (what runs where), hi dev task (the team's
  tasks), hi dev say (its Slack channel), hi dev incidents (what failed).
  Their skills say more.
- **Help:** hi dev review asks the other agent for a second opinion before
  shipping; hi dev deep-review and hi dev brainstorm run several agents,
  and cost money: ask the user for the budget first. hi dev data and hi
  dev settings are the app's data and the preview's settings.
- **The box is the boundary:** you don't ask before commands. The internet
  goes through the box's proxy, which allows what development needs. When
  it refuses a site you need (a 403 from the proxy), ask the owners for it
  at once: hi dev allow <host> --why "<what for, in a line>" --wait. They
  allow it on the dev page or say no; don't work around a refusal.
- **How the team works:** tests with the code; docs (docs/) with what
  changes for the people; specs (docs/specs/) before larger work, agreed
  with the user. What you can't check here, such as how it looks on a
  phone, say so, and give the preview link to try.
<!-- End of hi's block. -->
`

// teamMapRule is how the box's agents keep the project's map and docs.
const teamMapRule = "docs/map.md is the project's map: what it already has (pages, API routes, tables, shared code, outside services) and where each lives. Read it before you add anything, and extend what it lists rather than building something beside it. When your work adds or moves something on it, update it in the same work, and add a short file to docs/decisions/ for a technical choice someone shouldn't undo by accident. If there's no docs/map.md yet and your work changes code, write it from the code as part of this work. The docs at /docs come from the project too: update the user docs (docs/user/) for anything the people will see, and docs/tech/index.md when the parts or how they connect change. Draw with mermaid blocks there (every kind of Mermaid chart, drawn by pele); for charts in the app itself, use the pele package, whose render(text) returns an SVG."

// teamDrawRule is how to draw in the docs: without the format, agents
// drew boxes as ASCII art, which /docs shows as text.
const teamDrawRule = "Drawing in the docs: /docs draws two kinds of fenced block, and nothing else. Never draw with ASCII or box-drawing characters (┌─┐, +--+, |, ->) in a code block; it shows as text.\n" +
	"- A boxes block draws the structure, what runs where and what is inside what, as cards in boxes with numbered marks and a key. It is JSON:\n" +
	"```boxes\n" +
	`{"caption": "The whole diagram in sentences, for screen readers.",` + "\n" +
	` "you": "People using the app", "via": "the app's address",` + "\n" +
	` "frame": "the team's machine", "frame_mark": 1,` + "\n" +
	` "groups": [{"name": "app box", "label": "rootless container", "mark": 2, "summary": "One line on the part.",` + "\n" +
	`   "items": [{"title": "src/server.ts", "role": "Bun server", "does": "One sentence; ` + "`code`" + ` is fine.", "chip": "Bun", "tone": "mint", "entry": "pages and API"}]},` + "\n" +
	`  {"name": "data", "via": "SQL", "mark": 3, "items": [{"title": "app.db", "role": "SQLite", "does": "Every order.", "chip": "SQLite", "tone": "dim"}]}],` + "\n" +
	` "after": [{"name": "outside", "via": "HTTPS", "items": [{"title": "api.example.com", "role": "forecasts", "does": "One sentence.", "chip": "API", "tone": "dim"}]}],` + "\n" +
	` "key": [{"mark": 1, "term": "The team's machine", "text": "One sentence."}, {"mark": 2, "term": "The app box", "text": "One sentence."}, {"mark": 3, "term": "The data", "text": "One sentence."}]}` + "\n" +
	"```\n" +
	"  Groups are the parts inside the frame, one to four, in the order things happen; via is the arrow into a group; after holds what runs outside the frame. A sentence per card, every mark in the key. Tones: accent, mint, amber, sky, violet, red, dim.\n" +
	"- A mermaid block draws how things move and change, with every kind of Mermaid chart (drawn by pele): sequenceDiagram for a request, stateDiagram-v2, erDiagram for the tables, flowchart, timeline, gantt, and the rest.\n" +
	"Open the page on the preview's /docs to see them drawn; a block that doesn't parse says why there."

const teamDevBlockStart, teamDevBlockEnd = "<!-- Written by hi on each start of the dev box", "<!-- End of hi's block. -->"

// copyTeamDevHi puts hi in dev/bin, which the box mounts as a folder: a
// mounted file would stay the old hi after hi update replaced it.
func copyTeamDevHi(team, hi string) error {
	data, err := os.ReadFile(hi)
	if err != nil {
		return fmt.Errorf("hi for the dev box: %w", err)
	}
	dir := filepath.Join(teamDevDir(team), "bin")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if old, err := os.ReadFile(filepath.Join(dir, "hi")); err == nil && bytes.Equal(old, data) {
		return nil
	}
	return writeFileAtomic(filepath.Join(dir, "hi"), data, 0o755)
}

// installTeamDevKit writes the skills, herdr's own, and the instructions
// block into the box's home; what an owner added around them stays.
func installTeamDevKit(config teamConfig, home string) error {
	skills, err := fs.Sub(teamDevSkills, "skills/hi/team")
	if err != nil {
		return err
	}
	entries, err := fs.ReadDir(skills, ".")
	if err != nil {
		return err
	}
	write := func(name string, data []byte) error {
		dir := filepath.Join(home, ".agents", "skills", name)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
		if err := writeFileAtomic(filepath.Join(dir, "SKILL.md"), data, 0o600); err != nil {
			return err
		}
		// Claude Code reads .claude/skills: a link to the same folder, as
		// hi skill makes.
		link := filepath.Join(home, ".claude", "skills", name)
		if err := os.MkdirAll(filepath.Dir(link), 0o700); err != nil {
			return err
		}
		if target, err := os.Readlink(link); err != nil || target != filepath.Join("..", "..", ".agents", "skills", name) {
			os.RemoveAll(link)
			return os.Symlink(filepath.Join("..", "..", ".agents", "skills", name), link)
		}
		return nil
	}
	// Skills an older hi wrote under another name.
	for _, retired := range []string{"hi-dev-tickets"} {
		os.RemoveAll(filepath.Join(home, ".agents", "skills", retired))
		os.Remove(filepath.Join(home, ".claude", "skills", retired))
	}
	for _, entry := range entries {
		data, err := fs.ReadFile(skills, entry.Name()+"/SKILL.md")
		if err != nil {
			continue
		}
		if err := write(entry.Name(), data); err != nil {
			return err
		}
	}
	if herdr, err := boxHostBinary("herdr"); err == nil {
		if out, err := teamDevHerdrSkill(herdr); err == nil && strings.HasPrefix(out, "---") {
			write("herdr", []byte(out))
		}
	}
	block := fmt.Sprintf(teamDevInstructions, config.Name, config.Branch)
	block = strings.Replace(block, teamDevBlockEnd, "\n## The project's docs\n\n"+teamMapRule+"\n\n"+teamDrawRule+"\n\n## The app's long work\n\n"+teamJobsRule+"\n"+teamDevBlockEnd, 1)
	if adopted := config.Adopted; adopted != nil && adopted.Local {
		block = strings.Replace(block, teamDevBlockEnd, fmt.Sprintf("- **Adopted:** this project is a copy of %s, a folder on the team's machine, without its history; it has no upstream. When docs/map.md is missing, offer a survey first (the hi-dev-survey skill).\n", adopted.From)+teamDevBlockEnd, 1)
	} else if adopted != nil {
		// The project came from elsewhere: keeping up with it, and the
		// survey that maps it, are the owner's to ask for.
		block = strings.Replace(block, teamDevBlockEnd, fmt.Sprintf("- **Adopted:** this project came from %s (%s), and keeps it as upstream: the hi-dev-upstream skill keeps up with it. When docs/map.md is missing, offer a survey first (the hi-dev-survey skill).\n", adopted.Name, adopted.From)+teamDevBlockEnd, 1)
	}
	for _, path := range []string{filepath.Join(home, ".claude", "CLAUDE.md"), filepath.Join(home, ".codex", "AGENTS.md")} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return err
		}
		data, _ := os.ReadFile(path)
		if err := writeFileAtomic(path, []byte(teamDevWithBlock(string(data), block)), 0o600); err != nil {
			return err
		}
	}
	return nil
}

// teamDevGitIdentity gives the box's commits an author: without one, git
// refuses to commit. Owners share the box and its agents, so the author is
// the box, at an address that can't be mistaken for a real one; an owner's
// own [user], in either of git's files in the home, stays.
func teamDevGitIdentity(home, team string) error {
	path := filepath.Join(home, ".gitconfig")
	data, _ := os.ReadFile(path)
	xdg, _ := os.ReadFile(filepath.Join(home, ".config", "git", "config"))
	if strings.Contains(string(data), "[user]") || strings.Contains(string(xdg), "[user]") {
		return nil
	}
	text := string(data)
	if text != "" && !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	text += fmt.Sprintf("[user]\n\tname = %s dev box\n\temail = dev-box@%s.invalid\n", team, team)
	return writeFileAtomic(path, []byte(text), 0o600)
}

// teamDevHerdrSkill is herdr's own skill, from herdr --skill; tests
// replace it.
var teamDevHerdrSkill = func(herdr string) (string, error) {
	out, err := exec.Command(herdr, "--skill").Output()
	return string(out), err
}

// teamDevWithBlock puts hi's block in text, in place of the one before.
func teamDevWithBlock(text, block string) string {
	if start := strings.Index(text, teamDevBlockStart); start >= 0 {
		if end := strings.Index(text[start:], teamDevBlockEnd); end >= 0 {
			rest := strings.TrimLeft(text[start+end+len(teamDevBlockEnd):], "\n")
			text = text[:start] + rest
		}
	}
	text = strings.TrimLeft(text, "\n")
	if text == "" {
		return block
	}
	return block + "\n" + text
}
