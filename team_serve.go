package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
)

// hi team __serve runs one team in the foreground, under systemd: the
// lead's box with Hermes' Slack gateway, its proxy, and the broker. When
// the lead stops, so does serve, and systemd starts it again.

// teamLeadHosts is the lead's allowlist: any site, so it can read the
// links people give it. The box is still its boundary: no credentials, and
// its model calls go through the proxy's token listener.
var teamLeadHosts = []string{"*"}

// teamTrustInBox is where the lead's box sees the CA bundle it trusts.
const teamTrustInBox = "/box/trust"

func teamServe(name string, stdout, stderr io.Writer) error {
	config, err := loadTeam(name)
	if err != nil {
		return err
	}
	secrets, err := loadTeamSecrets(name)
	if err != nil {
		return fmt.Errorf("the team's Slack tokens: %w", err)
	}
	if secrets.LeadAPIKey == "" {
		secrets.LeadAPIKey = netExposeKey() + netExposeKey()
		if err := saveTeamSecrets(name, secrets); err != nil {
			return err
		}
	}
	if err := teamSyncHelper(config); err != nil {
		return err
	}
	if err := hermesInstalled(); err != nil {
		return err
	}
	runtime, err := loadHermesRuntime()
	if err != nil {
		return err
	}
	engine, warning, err := detectBoxEngine()
	if err != nil {
		return err
	}
	if warning != "" {
		fmt.Fprintln(stderr, "hi: "+warning)
	}
	hi, err := os.Executable()
	if err != nil {
		return err
	}
	if resolved, err := filepath.EvalSymlinks(hi); err == nil {
		hi = resolved
	}
	dir := teamDir(name)
	proxyDir, brokerDir, home := filepath.Join(dir, "run", "proxy"), teamBrokerDir(name), filepath.Join(dir, "run", "home")
	specs, err := teamPrepareSpecs(config)
	if err != nil {
		return err
	}
	for _, sub := range []string{proxyDir, brokerDir, home, filepath.Join(dir, "lead"), teamTaskDir(name), filepath.Join(teamChannelDir(name), "files"), teamWebStore(name), teamReviewsDir(name), teamBrainstormsDir(name)} {
		if err := os.MkdirAll(sub, 0o700); err != nil {
			return err
		}
	}

	meta := boxMeta{Name: "team-" + name, Agent: "hermes", Engine: engine.name, Network: "team", Created: time.Now().UTC()}
	container := "hi-box-" + meta.Name
	// Whatever a crash left behind goes first, the memory server before the
	// network it is on.
	engine.output("rm", "-f", teamMemoryContainer(name))
	teardownBox(engine, meta)
	teamUnpauseLeftovers(engine, name, stderr)
	memorySite := ""
	if err := os.WriteFile(filepath.Join(proxyDir, "allow"), []byte(strings.Join(teamLeadHosts, "\n")+"\n"), 0o600); err != nil {
		return err
	}
	if meta.Image, err = engine.ensureBaseImage(stdout, stderr); err != nil {
		return err
	}
	if err := writeTeamKeys(name, secrets); err != nil {
		return err
	}
	if teamUsesChatGPT(config) {
		// The lead's first call already has the token; a sign-in that
		// doesn't work sends it to its fallback, and the channel hears.
		if err := prepareTeamChatGPT(name, time.Now()); err != nil {
			fmt.Fprintf(stderr, "hi: the team's ChatGPT sign-in: %v\n", err)
		}
	}
	// The lead's box gets placeholders for the Slack tokens, which its
	// proxy swaps for the real ones (box_slack.go).
	trustDir, boxSecrets := filepath.Join(dir, "run", "trust"), secrets
	swapSlack, err := prepareTeamSlackSwap(teamKeysDir(name), trustDir, secrets)
	if err != nil {
		return fmt.Errorf("the lead's Slack tokens: %w", err)
	}
	if swapSlack {
		boxSecrets.SlackBotToken, boxSecrets.SlackAppToken = boxSlackBotPlacehold, boxSlackAppPlacehold
	} else {
		fmt.Fprintln(stderr, "hi: this machine has no CA bundle, so the lead's box holds the real Slack tokens this time")
	}
	if err := startBoxNetwork(engine, &meta, proxyDir, "team:"+teamKeysDir(name), nil); err != nil {
		teardownBox(engine, meta)
		return err
	}
	defer teardownBox(engine, meta)

	// The lead's long-term memory: Hindsight on the team's network.
	memoryIP := ""
	if site, ok := teamPrepareMemory(engine, config, runtime, stderr); ok {
		if err := startTeamMemory(engine, config, meta.ProxyIP); err != nil {
			fmt.Fprintf(stderr, "hi: the lead's long-term memory (Hindsight) is off this time: %v\n", err)
		} else {
			defer engine.output("stop", "-t", "20", teamMemoryContainer(name))
			if err := teamWaitMemory(engine, name); err != nil {
				fmt.Fprintf(stderr, "hi: the lead's long-term memory (Hindsight) is off this time: %v\n", err)
				engine.output("rm", "-f", teamMemoryContainer(name))
			} else {
				memoryIP, memorySite = teamMemoryIP(meta.ProxyIP), site
			}
		}
	}
	if err := writeTeamLead(config, boxSecrets, meta.ProxyIP, memoryIP); err != nil {
		return err
	}
	if n, err := refreshTeamLeadPrompts(filepath.Join(teamDir(name), "lead")); err != nil {
		fmt.Fprintf(stderr, "hi: the lead's conversations keep their old SOUL.md until they end: %v\n", err)
	} else if n > 0 {
		fmt.Fprintf(stderr, "hi: %d of the lead's conversations pick up SOUL.md as it is now on their next message.\n", n)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// A deploy the last broker didn't finish gives the old box its
	// workers back.
	clearWorkersYields(name)
	broker := newTeamBroker(name, hi)
	teamNoteServeStart(name)
	slackChannel := newLiveTeamChannel(secrets.SlackBotToken)
	broker.channel = slackChannel
	serveBroker, err := broker.listen(ctx, filepath.Join(brokerDir, teamSocketName))
	if err != nil {
		return fmt.Errorf("the broker's socket: %w", err)
	}
	serveHost, err := broker.listenHost(ctx, teamHostSocket(name))
	if err != nil {
		return fmt.Errorf("the broker's socket for this machine: %w", err)
	}
	serveDev, err := broker.listenDev(ctx)
	if err != nil {
		return fmt.Errorf("the dev box's socket: %w", err)
	}
	serveWeb, err := broker.listenWeb(ctx)
	if err != nil {
		return fmt.Errorf("the helper's sockets: %w", err)
	}
	serveDevPage, err := broker.listenDevPage(ctx)
	if err != nil {
		return fmt.Errorf("the dev page's sockets: %w", err)
	}
	serveWeb = append(serveWeb, serveDevPage...)
	serveInternal, err := broker.listenInternal(ctx, stderr)
	if err != nil {
		return fmt.Errorf("the internal addresses' sockets: %w", err)
	}
	serveWeb = append(serveWeb, serveInternal...)
	leadAPI := filepath.Join(brokerDir, teamLeadAPISocket)
	broker.lead = teamLeadAPI(leadAPI, secrets.LeadAPIKey)
	brokerErr := make(chan error, 3+len(serveWeb))
	go func() { brokerErr <- serveBroker() }()
	go func() { brokerErr <- serveHost() }()
	go func() { brokerErr <- serveDev() }()
	for _, serve := range serveWeb {
		go func() { brokerErr <- serve() }()
	}
	broker.resume()
	if err := broker.commitSpecs(); err != nil {
		fmt.Fprintf(stderr, "hi: committing the specs: %v\n", err)
	}
	go teamWatchChannel(ctx.Done(), name, config.Channel, slackChannel, stderr)
	broker.pollChannel = func() error { return teamPollChannel(name, config.Channel, slackChannel, time.Now()) }
	go broker.keepLinks(ctx.Done(), stderr)
	go broker.keepDevPage(ctx.Done(), stderr)
	go broker.keepHTTPS(ctx.Done(), stderr)
	broker.wakeLead = func(name, prompt, deliver string) error {
		// A job that runs once, in a minute, with the hi-team skill; its
		// answer goes to the thread.
		args := append(append([]string{"exec", container}, runtime.Command...), "cron", "create", "1m", prompt,
			"--name", name, "--repeat", "1", "--skill", "hi-team", "--deliver", deliver, "--failure-deliver", "local")
		if out, err := engine.output(args...); err != nil {
			return fmt.Errorf("%s", qFirstLine(strings.TrimSpace(out), err.Error()))
		}
		return nil
	}
	go broker.startPreview(stderr)
	go broker.keepUpstream(ctx.Done(), stderr)
	go broker.watchJobs(ctx.Done(), stderr)
	go broker.keepWatched(ctx.Done(), stderr)
	go broker.keepSubscriptions(ctx.Done(), stderr)
	go func() {
		// The owners' dev box keeps running across restarts of the service.
		if err := startTeamDev(config, hi, stderr, stderr); err != nil {
			fmt.Fprintf(stderr, "hi: the dev box: %v\n", err)
		}
		// v2.5.0's production and preview peers: the team's one peer has
		// every address now.
		if engine, _, err := detectBoxEngine(); err == nil {
			stopTeamPeers(engine, name)
		}
		keepTeamPeer(ctx.Done(), name, hi, stderr)
	}()
	startTeamResolver(name, hi, ctx.Done(), stderr)
	go func() {
		if err := broker.serveModels(ctx); err != nil {
			fmt.Fprintf(stderr, "hi: the app's models (OpenRouter) are off this time: %v\n", err)
		}
	}()
	go teamEnsureChannelJob(engine, runtime, container, filepath.Join(dir, "lead"), ctx.Done(), stderr)
	go teamKeepLeadAPI(engine, container, leadAPI, ctx.Done(), stderr)

	// The lead's output reaches the journal through this process, so the
	// engine keeps no second copy.
	run := []string{"run", "--rm", "--init", "--log-driver", "none", "--name", container, "--hostname", name + "-lead", "--network", "hi-box-" + meta.Name, "--dns", "127.0.0.1"}
	run = append(run, engine.userArgs()...)
	run = append(run, boxHardening("4g")...)
	run = append(run,
		"-v", home+":/box/home",
		"-v", filepath.Join(dir, "lead")+":/box/home/.hermes",
		// The specs are the owners' since hi 2.4: the lead reads them.
		"-v", specs+":/team/specs:ro",
		"-v", filepath.Join(dir, "repo")+":/team/repo:ro",
		"-v", teamChannelDir(name)+":"+teamChannelInBox+":ro",
		"-v", teamWebStore(name)+":"+teamWebInBox+":ro",
		"-v", teamReviewsDir(name)+":/team/reviews:ro",
		"-v", teamBrainstormsDir(name)+":/team/brainstorms:ro",
		"-v", brokerDir+":/team/run",
		"-v", hi+":/usr/local/bin/hi:ro",
		"-w", "/team")
	if err := hermesMounts(runtime, filepath.Join(dir, "lead"), &run); err != nil {
		return err
	}
	noProxy := boxNoProxy + "," + meta.ProxyIP
	if memorySite != "" {
		run = append(run, "-v", memorySite+":"+memorySite+":ro")
		noProxy += "," + memoryIP
	}
	proxy := fmt.Sprintf("http://%s:%d", meta.ProxyIP, boxProxyPort)
	env := map[string]string{
		"HTTPS_PROXY": proxy, "HTTP_PROXY": proxy, "https_proxy": proxy, "http_proxy": proxy,
		"NO_PROXY": noProxy, "no_proxy": noProxy,
		"HOME": "/box/home", "HERMES_HOME": "/box/home/.hermes", "HI_BOX": meta.Name, "HI_TEAM": name,
		teamSocketEnv: "/team/run/" + teamSocketName,
		// The box is the boundary, so Hermes doesn't ask before commands,
		// doesn't install into its home, and keeps its locks in the box.
		"HERMES_YOLO_MODE": "1", "HERMES_DISABLE_LAZY_INSTALLS": "1", "PYTHONDONTWRITEBYTECODE": "1",
		"HERMES_GATEWAY_LOCK_DIR": "/box/home/.locks",
		"PATH":                    "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
	}
	if memorySite != "" {
		// Hindsight's client, beside Hermes' own packages.
		env["PYTHONPATH"] = memorySite
	}
	if swapSlack {
		// The public roots and the CA the proxy speaks to Slack with.
		run = append(run, "-v", trustDir+":"+teamTrustInBox+":ro")
		bundle := teamTrustInBox + "/" + boxSlackBundle
		for _, key := range []string{"SSL_CERT_FILE", "REQUESTS_CA_BUNDLE", "CURL_CA_BUNDLE", "NODE_EXTRA_CA_CERTS"} {
			env[key] = bundle
		}
	}
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		run = append(run, "-e", key+"="+env[key])
	}
	command := runtime.Command
	if memorySite != "" {
		var ok bool
		if command, ok = hermesWithSite(command, memorySite); !ok {
			fmt.Fprintln(stderr, "hi: Hermes runs in a way hi doesn't know, so the lead may not find Hindsight's client and keep to Hermes' built-in memory")
		}
	}
	if with, ok := hermesWithSlackStatus(command); ok {
		command = with
	}
	run = append(append(run, meta.Image), command...)
	run = append(run, "gateway", "run", "--accept-hooks")

	// systemd stops the team with SIGTERM; the lead gets time to finish
	// what it is saying.
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM, os.Interrupt)
	defer signal.Stop(signals)
	var stopped atomic.Bool
	go func() {
		if _, ok := <-signals; ok {
			stopped.Store(true)
			// The hi server hears this is a stop, not a machine that died:
			// a restart unless hi team down says otherwise.
			broker.sendHeartbeat("restart")
			engine.output("stop", "-t", "20", container)
		}
	}()
	fmt.Fprintf(stdout, "Team %s: the lead is starting in %s: %s.\n", name, container, teamMemberLabel(teamMember{Agent: "hermes", Model: firstNonEmpty(config.Members["lead"].Model, "Hermes' default"),
		Provider: config.Members["lead"].Provider, Fallback: config.Members["lead"].Fallback}))
	err = engine.interactive(nil, stdout, stderr, run...)
	cancel()
	if stopped.Load() {
		fmt.Fprintf(stdout, "Team %s stopped.\n", name)
		return nil
	}
	select {
	case brokerFailure := <-brokerErr:
		if brokerFailure != nil {
			return fmt.Errorf("the broker: %w", brokerFailure)
		}
	default:
	}
	if err == nil {
		err = errors.New("the lead stopped")
	}
	return fmt.Errorf("team %s: %w; systemd starts it again", name, err)
}

// teamBrokerDir holds the broker's socket. A socket's path must be short,
// so it lives in the user's runtime folder when there is one.
func teamBrokerDir(name string) string {
	if runtime := os.Getenv("XDG_RUNTIME_DIR"); runtime != "" && fileExists(runtime) {
		return filepath.Join(runtime, "hi-team-"+name)
	}
	return filepath.Join(teamDir(name), "run", "broker")
}

// teamLeadMaxTurns caps the model calls in one turn of the lead's; few
// answers need more than 20. teamLeadBudgetWarning has Hermes tell the
// lead once, at 45 of them, to keep what it found before the cap. Both
// were set for google/gemini-3.8-flash: on team orbit, 2026-10-18 to
// 10-20, 11 of 286 turns hit the cap, mostly reading /team/repo/src.
// Re-check them on another model (docs/specs/approved/hi_team_lead_steps.md).
const (
	teamLeadMaxTurns      = 60
	teamLeadBudgetWarning = 0.75
	// teamLeadSilentCall is how many seconds the lead waits for a model
	// call's first word before it retries or falls back.
	teamLeadSilentCall = 120
)

// teamLeadModelSettings is the model part of the lead's config.yaml. On
// OpenRouter, a custom provider at the team's proxy, which puts the key in.
// On ChatGPT, a named provider at the proxy's ChatGPT route, in the
// Responses API: a plain custom provider would drop codex_responses for
// any host but OpenAI's (both found on gpu1, 2026-10-23). Hermes falls back to the same model on OpenRouter,
// through the proxy too, when ChatGPT refuses: at once for a sign-in that
// doesn't work, after its retries for a limit, back on ChatGPT the next
// turn, or once the plan's limit resets.
func teamLeadModelSettings(config teamConfig, proxyIP string) string {
	host := readHermesModelConfig(filepath.Join(hermesHome(), "config.yaml"))
	lead := config.Members["lead"]
	openRouter := fmt.Sprintf("http://%s:%d/api/v1", proxyIP, boxInjectPort)
	apiMode := firstNonEmpty(host.APIMode, "chat_completions")
	if teamProvider(lead) == teamProviderChatGPT {
		return "model:\n  default: " + lead.Model + "\n  provider: custom:hi-chatgpt\n" +
			"providers:\n" +
			fmt.Sprintf("  hi-chatgpt:\n    api: http://%s:%d%s\n    api_key: %s\n    transport: codex_responses\n    discover_models: false\n",
				proxyIP, boxInjectPort, strings.TrimSuffix(chatGPTPath, "/"), boxClaudePlacehold) +
			fmt.Sprintf("  hi-openrouter:\n    api: %s\n    api_key: %s\n    transport: %s\n    discover_models: false\n", openRouter, boxClaudePlacehold, apiMode) +
			// The entry carries its own address and key: without them Hermes
			// looks for an OpenRouter key of its own, which the box hasn't.
			"fallback_providers:\n  - provider: custom:hi-openrouter\n    model: " + teamLeadOpenRouterModel(config) + "\n" +
			fmt.Sprintf("    base_url: %s\n    api_key: %s\n    api_mode: %s\n", openRouter, boxClaudePlacehold, apiMode)
	}
	settings := "model:\n"
	if model := firstNonEmpty(lead.Model, host.Default); model != "" {
		settings += "  default: " + model + "\n"
	}
	return settings + fmt.Sprintf("  provider: custom\n  base_url: %s\n  api_key: %s\n  api_mode: %s\n", openRouter, boxClaudePlacehold, apiMode)
}

// writeTeamLead writes what hi owns in the lead's Hermes home on every
// start: its settings, its .env, and the hi-team skill. SOUL.md and the
// memories are the lead's own and are left alone.
func writeTeamLead(config teamConfig, secrets teamSecrets, proxyIP, memoryIP string) error {
	lead := filepath.Join(teamDir(config.Name), "lead")
	settings := teamLeadModelSettings(config, proxyIP)
	settings += "terminal:\n  backend: local\n  cwd: /team\n"
	settings += "approvals:\n  mode: \"off\"\n"
	settings += "updates:\n  check: false\n"
	// hi-team gives the channel /hi (team_people.go).
	settings += "plugins:\n  auto_update_check_hours: 0\n  enabled:\n    - " + teamLeadPlugin + "\n"
	if memoryIP != "" {
		settings += "    - " + teamHindsightPlugin + "\n"
		settings += "memory:\n  provider: " + teamHindsightPlugin + "\n"
	}
	// No "Hermes is shutting down" or "back online" posts in the channel on
	// every restart of the team.
	settings += "gateway:\n  platforms:\n    slack:\n      gateway_restart_notification: false\n"
	// A DM is one conversation, as in a chat app, not a thread per message.
	settings += "      dm_top_level_threads_as_sessions: false\n"
	// A scheduled job's message is just its text, without Hermes' "Cronjob
	// Response" heading and "To stop or manage this job" line.
	settings += "cron:\n  wrap_response: false\n"
	// The web tools from the providers that need no key, Exa first and
	// then Parallel, Firecrawl, and Keenable when one fails: they search
	// and read pages. Left to itself Hermes picks DuckDuckGo, which can't
	// read a page, so the lead couldn't read the docs it found (team
	// quarry, 2026-10-26).
	settings += "web:\n  backend: exa\n"
	// The app's own tools, from its /mcp, on production (those that only
	// read) and on the preview (team_mcp.go). Hermes passes a server only
	// the environment it's given, so the bridge is told the broker's socket.
	settings += "mcp_servers:\n"
	for _, server := range []struct{ name, slot string }{{"app", teamSlotProduction}, {"app_preview", teamSlotPreview}} {
		settings += fmt.Sprintf("  %s:\n    command: hi\n    args: [team, mcp, %s]\n    env:\n      %s: /team/run/%s\n    tools:\n      resources: false\n      prompts: false\n",
			server.name, server.slot, teamSocketEnv, teamSocketName)
	}
	// No "Interrupting current task" reply, with its /busy tip, when someone
	// writes while the lead is working: the lead still takes the message.
	// Slack's thinking indicator names what the lead does, without the
	// paths and commands, which the channel has no use for.
	// No "💾 Self-improvement review: Skill … patched" after a turn: the
	// review still runs and saves what it learned, but the people in the
	// thread have no use for the notice (team orbit, 2026-10-24).
	settings += "display:\n  busy_ack_enabled: false\n  live_status: verb\n  memory_notifications: \"off\"\n"
	if teamProvider(config.Members["lead"]) != teamProviderOpenRouter {
		// Hermes would tell the thread each time it falls back; hi tells
		// the channel once (team_subscriptions.go).
		settings += "  suppress_warning_notifications: true\n"
	}
	// A turn that keeps calling tools stops and answers with what it has:
	// the lead talks and hands work to members, and a lead stuck searching
	// the code ran 200 calls in 13 minutes before anyone saw a word.
	// The proxy's address is private, so Hermes takes it for a model on
	// this machine and waits 15 minutes for a call that sends nothing; the
	// models are ChatGPT's and OpenRouter's, so it gives up after the
	// usual 2, and retries or falls back (team orbit, 2026-10-23: a
	// release waited on a ChatGPT call that never answered).
	settings += fmt.Sprintf("agent:\n  max_turns: %d\n  budget_warning_ratio: %g\n  reasoning_effort: %s\n  local_stream_stale_timeout: %d\n",
		teamLeadMaxTurns, teamLeadBudgetWarning, teamEffort(config.Members["lead"]), teamLeadSilentCall)
	if err := os.WriteFile(filepath.Join(lead, "config.yaml"), []byte(settings), 0o600); err != nil {
		return err
	}
	env := []string{
		"OPENROUTER_API_KEY=" + boxClaudePlacehold,
		"SLACK_BOT_TOKEN=" + secrets.SlackBotToken,
		"SLACK_APP_TOKEN=" + secrets.SlackAppToken,
		// One channel, and DMs with the people who may ask for work: the
		// plugin drops anyone else's (team_people.go).
		"SLACK_ALLOWED_CHANNELS=" + config.Channel,
		"SLACK_HOME_CHANNEL=" + config.Channel,
		"SLACK_DISABLE_DMS=false",
		// It answers when @-mentioned, and then follows that thread without
		// a fresh @, as it does threads it started, except for a reply that
		// opens by @-mentioning someone else. The broker logs the rest of
		// the channel for it (team_channel.go).
		"SLACK_REQUIRE_MENTION=true",
		"SLACK_IGNORE_OTHER_USER_MENTIONS=true",
	}
	if secrets.LeadAPIKey != "" {
		// Hermes' API server, inside the box only: the helper in the app
		// reaches the lead through it (team_helper.go).
		env = append(env, "API_SERVER_ENABLED=true", "API_SERVER_HOST=127.0.0.1",
			fmt.Sprintf("API_SERVER_PORT=%d", teamLeadAPIPort), "API_SERVER_KEY="+secrets.LeadAPIKey)
	}
	// Everyone in the channel may talk with the lead (hi 2.4).
	env = append(env, "SLACK_ALLOW_ALL_USERS=true")
	if err := os.WriteFile(filepath.Join(lead, ".env"), []byte(strings.Join(env, "\n")+"\n"), 0o600); err != nil {
		return err
	}
	if !fileExists(filepath.Join(lead, "SOUL.md")) {
		if err := os.WriteFile(filepath.Join(lead, "SOUL.md"), []byte(teamSoul(config)), 0o600); err != nil {
			return err
		}
	} else if err := updateTeamSoul(filepath.Join(lead, "SOUL.md"), config); err != nil {
		return err
	}
	if memoryIP != "" {
		if err := writeTeamHindsightConfig(lead, config.Name, config.madeAs(), memoryIP); err != nil {
			return err
		}
	}
	plugin := filepath.Join(lead, "plugins", teamLeadPlugin)
	if err := os.MkdirAll(plugin, 0o700); err != nil {
		return err
	}
	for name, data := range map[string][]byte{"plugin.yaml": teamLeadPluginManifest, "__init__.py": teamLeadPluginSource} {
		if err := os.WriteFile(filepath.Join(plugin, name), data, 0o600); err != nil {
			return err
		}
	}
	ack := filepath.Join(lead, "hooks", "hi-dm-ack")
	if err := os.MkdirAll(ack, 0o700); err != nil {
		return err
	}
	for name, data := range map[string][]byte{"HOOK.yaml": teamLeadAckManifest, "handler.py": teamLeadAckSource} {
		if err := os.WriteFile(filepath.Join(ack, name), data, 0o600); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Join(lead, "scripts"), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(lead, "scripts", teamChannelScript), []byte(teamChannelWatchScript), 0o700); err != nil {
		return err
	}
	notes := filepath.Join(lead, "skills", teamNotesSkill, "SKILL.md")
	if !fileExists(notes) {
		if err := os.MkdirAll(filepath.Dir(notes), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(notes, []byte(teamNotes(config)), 0o600); err != nil {
			return err
		}
	}
	return writeTeamSkill(filepath.Join(lead, "skills", "hi-team"), teamSkill(config), notes)
}

// teamNotesSkill is the lead's own skill, for what it learns: hi creates it
// and never rewrites it, so the lead's notes outlive hi's updates to the
// hi-team skill.
const teamNotesSkill = "hi-team-notes"

func teamNotes(config teamConfig) string {
	return fmt.Sprintf(`---
name: %[2]s
description: What you learned running the %[1]s team, such as pitfalls with briefs, specs, reviews, previews, and the channel. Read it with hi-team, and add to it.
---

# What the lead learned

This skill is yours: hi never rewrites it. Keep here what you learn about
running the team, one short note each. hi-team is hi's, and hi replaces it
when hi updates; where a note here disagrees with hi-team, hi-team is
current.
`, config.Name, teamNotesSkill)
}

// teamChannelWatchScript runs before each channel-watch job: it prints what
// was said since the lead last looked, or wakes nobody when nothing was.
const teamChannelWatchScript = `#!/bin/bash
# Written by hi on every start of the team: the channel's new messages for
# the channel-watch job.
out=$(/usr/local/bin/hi team channel --new 2>&1)
if [ -z "$out" ]; then
  echo '{"wakeAgent": false}'
else
  printf '%s\n' "$out"
fi
`

// teamChannelPrompt is the channel-watch job's: the lead reads what was
// said without being asked and keeps what matters.
const teamChannelPrompt = `Above is what was said in the team's Slack channel since you last looked (your own messages are there too, for context). Nobody asked you anything, and this run posts nothing in the channel.

Keep what is worth remembering in your memory: decisions and wishes for the project, ideas, who knows or cares about what, people's preferences, problems they mention, and links, videos, or files someone found worth sharing. Open a link, or look at a file under /team/channel/files, when it may matter for the project, and remember what it holds, not just that it was shared. If something needs the team's attention, remember it so you can raise it the next time someone asks you something. Skip small talk.`

// teamEnsureChannelJob adds the channel-watch job to the lead's Hermes
// once it runs, unless the job is there already (the lead may have tuned
// it).
func teamEnsureChannelJob(engine boxEngine, runtime hermesRuntime, container, lead string, done <-chan struct{}, stderr io.Writer) {
	jobs := filepath.Join(lead, "cron", "jobs.json")
	for attempt := 0; attempt < 30; attempt++ {
		select {
		case <-done:
			return
		case <-time.After(5 * time.Second):
		}
		var store struct {
			Jobs []struct {
				Name string `json:"name"`
			} `json:"jobs"`
		}
		if data, err := os.ReadFile(jobs); err == nil && json.Unmarshal(data, &store) == nil {
			for _, job := range store.Jobs {
				if job.Name == teamChannelJob {
					return
				}
			}
		}
		args := append(append([]string{"exec", container}, runtime.Command...),
			"cron", "create", "every 15m", teamChannelPrompt, "--name", teamChannelJob, "--script", teamChannelScript,
			"--skill", "hi-team", "--deliver", "local", "--failure-deliver", "local")
		out, err := engine.output(args...)
		if err == nil {
			return
		}
		if attempt == 29 {
			fmt.Fprintf(stderr, "hi: adding the lead's channel-watch job: %s\n", qFirstLine(strings.TrimSpace(out), err.Error()))
		}
	}
}

// writeTeamSkill writes hi's hi-team skill, but keeps the lead's own edits:
// Hermes improves its skills as it works. hi replaces the skill only when
// its own text changed, in a new hi, and keeps the lead's last version
// beside it.
// The lead's edits to hi's text move to its notes skill (notes) when hi
// replaces the text: each paragraph hi didn't write.
func writeTeamSkill(dir, text, notes string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	path, stamp, previous := filepath.Join(dir, "SKILL.md"), filepath.Join(dir, ".hi-sha256"), filepath.Join(dir, ".hi-text")
	sum := func(data []byte) string { return fmt.Sprintf("%x", sha256.Sum256(data)) }
	written, _ := os.ReadFile(stamp)
	current, err := os.ReadFile(path)
	switch {
	case err != nil:
		// Not there yet.
	case strings.TrimSpace(string(written)) == sum([]byte(text)):
		return nil // hi's text is the same: whatever the lead made of it stays
	case strings.TrimSpace(string(written)) != sum(current):
		// The lead changed it, and hi has new instructions.
		if err := os.WriteFile(path+".lead", current, 0o600); err != nil {
			return err
		}
		if old, err := os.ReadFile(previous); err == nil && sum(old) == strings.TrimSpace(string(written)) {
			if err := appendTeamNotes(notes, teamLeadParagraphs(string(old), string(current))); err != nil {
				return err
			}
		}
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(previous, []byte(text), 0o600); err != nil {
		return err
	}
	return os.WriteFile(stamp, []byte(sum([]byte(text))+"\n"), 0o600)
}

// teamLeadParagraphs are the paragraphs of edited that aren't in hi's text
// as hi wrote it.
func teamLeadParagraphs(hi, edited string) []string {
	ours := map[string]bool{}
	for _, paragraph := range strings.Split(hi, "\n\n") {
		ours[strings.TrimSpace(paragraph)] = true
	}
	var theirs []string
	for _, paragraph := range strings.Split(edited, "\n\n") {
		if paragraph = strings.TrimSpace(paragraph); paragraph != "" && !ours[paragraph] {
			theirs = append(theirs, paragraph)
		}
	}
	return theirs
}

func appendTeamNotes(path string, paragraphs []string) error {
	if len(paragraphs) == 0 {
		return nil
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = fmt.Fprintf(file, "\n## From hi-team, before hi updated it on %s\n\nYou had added these to hi-team. They may mention commands or steps hi has changed since.\n\n%s\n",
		time.Now().UTC().Format("Jan 2, 2006"), strings.Join(paragraphs, "\n\n"))
	return err
}

// teamManifest is the team's Slack app: Socket Mode, one channel, the
// bot's DMs, and Slack's agent view, whose assistant:write lets the lead
// show that it is thinking while it works.
func teamManifest(config teamConfig) string {
	data, _ := json.MarshalIndent(teamManifestData(config), "", "  ")
	return string(data)
}

// teamManifestData is the manifest, for teamManifest's JSON and
// teamManifestYAML. The app has the lead's name; its bot, the lead's
// handle.
func teamManifestData(config teamConfig) map[string]any {
	description := firstNonEmpty(config.Purpose, "A hi team")
	if len(description) > 139 {
		description = description[:136] + "..."
	}
	lead := "the " + config.Name + " team lead"
	if config.Members["lead"].Name != "" {
		lead = teamLeadTitle(config)
	}
	manifest := map[string]any{
		"display_information": map[string]any{"name": teamLeadName(config), "description": description, "background_color": "#1a1a2e"},
		"features": map[string]any{
			"bot_user": map[string]any{"display_name": teamBotHandle(config), "always_online": true},
			// The DM is hi's, for people's private links: they read it,
			// and write in the channel.
			"app_home":       map[string]any{"home_tab_enabled": false, "messages_tab_enabled": true, "messages_tab_read_only_enabled": false},
			"slash_commands": []map[string]any{{"command": teamSlashCommand(config.Name), "description": "A private link to the app's chat with " + lead, "should_escape": false}},
		},
		"oauth_config": map[string]any{"scopes": map[string]any{"bot": teamSlackScopes}},
		"settings": map[string]any{
			"event_subscriptions":    map[string]any{"bot_events": []string{"app_home_opened", "app_mention", "message.channels", "message.groups", "message.im", "reaction_added", "reaction_removed"}},
			"interactivity":          map[string]any{"is_enabled": true},
			"org_deploy_enabled":     false,
			"socket_mode_enabled":    true,
			"token_rotation_enabled": false,
		},
	}
	return manifest
}

func teamPeople(ids []string) string {
	var mentions []string
	for _, id := range ids {
		mentions = append(mentions, "<@"+id+">")
	}
	return strings.Join(mentions, ", ")
}

// teamSoul is the lead's identity, written once; owners may edit it.
// From hi 2.4 the lead doesn't build: the owners do, in the dev box
// (docs/specs/approved/hi_team_dev.md, release 4).
func teamSoul(config teamConfig) string {
	return fmt.Sprintf(`# %[5]s

You are %[6]s: its helper in the team's Slack channel,
for the people who use what the team builds.

The project: %[2]s

The team's owners are %[3]s. They build the project themselves, with
coding agents in the team's dev box, and release it from the team's dev
page. You don't build, merge, or release. When an owner asks you to
start work on a task, run hi team task start <number>: it starts in the
dev box. You help the people, and you make sure the owners hear what the
people need.

## The people

The people in the channel may not read code. They may be experts in their
own field who want something that works. You bridge the gap:

- Talk with them in plain language. No code, file names, or jargon unless
  they ask for it.
- Ask about what the app should do, never about code.
- Answer briefly, at the moments that matter to them.
%[4]s
## What you do

- **Help people use the app.** Answer how it works from its docs (the
  user docs in /team/repo/docs/user, the specs in /team/repo/docs/specs)
  and its code in /team/repo, which is dev as the owners shipped it,
  read-only for you. Point people to the app's /docs.
`+teamSoulShape+`- **Answer what's the status of something** from the tasks and the
  versions: whether it's filed, started, in dev to try on Stage, or
  in production. Don't promise when: the owners decide what comes next.
- **Watch the system.** hi wakes you for incidents, failing jobs, a new
  version of a project the team started from, and the backups. Handle
  what you can, and say what the people and the owners need to know.
- **Say what's the owners'.** Anything that changes the app, its data,
  its settings, or what production runs is theirs, in the dev box or on
  the dev page: say so plainly, and file the task.

The hi-team skill has the commands for each.

`+teamSoulAnswerFirst+`

Keep what you find. What a turn learns is gone when it ends unless you
write it down: put it in the task, or the incident (hi team incidents
note <id> "..."), where the next turn and the owners look.

## What you keep

Your long-term memory is for the people and the conversation: who wants
what, what was said and decided with them. Facts about the app belong in
the project, where the owners' agents read them: say what you learned in
the task, and the owners put it in the docs.

The project's story is in its user docs, docs/user/index.md: why it
exists and who it's for, in an owner's words. Read it before you answer
what the app is for. While it says the story isn't told yet, ask an owner
for it in plain words, and file it as a task.
`, config.Name, config.Purpose, teamPeople(config.Owners), teamSlackFormatting, teamLeadTitle(config), teamLeadIntro(config)) + teamSoulAdopted(config)
}

// teamSlackFormatting is how the lead writes for Slack. Hermes turns
// Markdown into Slack's own formatting, which a link inside bold or code
// breaks: Slack takes the stars as part of the address.
const teamSlackFormatting = `- Slack shows only part of Markdown. Write plain Markdown: **bold**,
  lists, and quotes work. Write a link as [what it opens](address), or
  the address alone on its own line. Never put an address in bold,
  italics, or code: Slack then takes the stars or backticks as part of the
  address, and the link breaks. Leave out headings and tables.
`

var teamSoulAnswerFirst = fmt.Sprintf(`Answer questions first: look at the code only as much as you need to
answer, a few files at most. Shaping a task is worth more of your time:
read what it touches, search the web, and read the docs of what it uses,
as the hi-team skill says. A turn of yours stops after %d steps and
answers with what it has.`, teamLeadMaxTurns)

// teamSoulShape is how the lead turns what people ask for into tasks:
// it looks, talks it through with them, and then files. Until 2.5.31 it
// asked a question or two and filed, so the owners' agents started from
// little (teamSoulRewrites).
const teamSoulShape = `- **Shape what they ask for into tasks.** An idea, a suggestion, or a
  bug report becomes a task the owners' coding agents start from in the
  dev box. Before you file, find out how the app does it now (its code
  and docs), whether it's asked for already (the open tasks: add to one
  that is there), and, when it touches something outside the app (a data
  source, a service, a library, a rule), what that says, from the web and
  its own docs. Then talk it through with them: what you found, in plain
  words, the choices there are, an idea of your own when you have a good
  one, and the questions that decide it. File it when you agree on what
  they want, and say its number.
`

// teamSoulRewrites are the paragraphs of an earlier hi's SOUL.md that
// have changed since, each with its text now. A SOUL.md is the owners'
// to edit, so only a paragraph still as hi wrote it is rewritten.
var teamSoulRewrites = [][2]string{
	{`page. You don't build, merge, or release. You start work on a task in
the dev box only when an owner asks you to: you help the people, and you
make sure the owners hear what the people need.`, `page. You don't build, merge, or release. When an owner asks you to
start work on a task, run hi team task start <number>: it starts in the
dev box. You help the people, and you make sure the owners hear what the
people need.`},
	{`page. You don't build, merge, or release, and you don't start agents on
the code: you help the people, and you make sure the owners hear what
the people need.`, `page. You don't build, merge, or release. When an owner asks you to
start work on a task, run hi team task start <number>: it starts in the
dev box. You help the people, and you make sure the owners hear what the
people need.`},
	{`- **Turn what they ask for into tasks.** An idea, a suggestion, or a
  bug report becomes a task the owners pick up in the dev box. Ask what
  makes it clear first (what they expected, what happened, how to see
  it, why it matters to them), then file it and say its number. Look at
  the open tasks first: add to one that is there rather than filing it
  twice.
`, teamSoulShape},
	{fmt.Sprintf(`Answer first. Look at the code only as much as you need to answer, a few
files at most. A turn of yours stops after %d steps and answers with what
it has.`, teamLeadMaxTurns), teamSoulAnswerFirst},
}

// teamSoulBuilds says a SOUL.md is from before hi 2.4, when the lead
// handed tasks to members.
// Since hi 2.5.14 the lead files tasks with hi team task add, so the marks
// are what only a SOUL.md from before 2.4 says.
var teamSoulBuilds = []string{"You hand tasks to the members", "The hi-team skill says how: hi team task", "hi team task --small", "## How work is done"}

// updateTeamSoul gives a SOUL.md from before hi 2.4, which still has the
// lead building, the lead's new one; the old one is kept beside it, with
// what the owners wrote in it. A later one gets teamSoulRewrites.
func updateTeamSoul(path string, config teamConfig) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	old := false
	for _, mark := range teamSoulBuilds {
		old = old || strings.Contains(string(data), mark)
	}
	if !old {
		soul := string(data)
		for _, rewrite := range teamSoulRewrites {
			soul = strings.Replace(soul, rewrite[0], rewrite[1], 1)
		}
		if soul == string(data) {
			return nil
		}
		return os.WriteFile(path, []byte(soul), 0o600)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(path), "SOUL.before-2.4.md"), data, 0o600); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(teamSoul(config)), 0o600)
}

// teamLeadPromptsScript forgets the stored system prompt of each of the
// lead's sessions that lacks the current SOUL.md. Hermes keeps a session's
// prompt from its first turn, for the prefix cache, so a long DM would
// never see a SOUL.md change; with the prompt gone, its next turn builds
// one from the current SOUL.md and keeps that.
const teamLeadPromptsScript = `
import json, sqlite3, sys
try:
    soul = open(sys.argv[2], encoding="utf-8").read().strip()
    db = sqlite3.connect(sys.argv[1], timeout=5)
    stale = [row[0] for row in db.execute(
        "select s.id, coalesce(p.prompt, s.system_prompt) from sessions s "
        "left join system_prompts p on p.hash = s.system_prompt_hash "
        "where coalesce(p.prompt, s.system_prompt) is not null") if soul not in row[1]]
    for session in stale:
        db.execute("update sessions set system_prompt_hash = null, system_prompt = null where id = ?", (session,))
    db.execute("delete from system_prompts where hash not in (select system_prompt_hash from sessions where system_prompt_hash is not null)")
    db.commit()
    print(json.dumps({"refreshed": len(stale)}))
except Exception as e:
    print(json.dumps({"error": str(e)}))
`

// refreshTeamLeadPrompts has the lead's sessions pick up SOUL.md as it is
// now on their next message, keeping their conversation. It runs before
// the lead's gateway starts, and returns how many sessions it refreshed.
func refreshTeamLeadPrompts(lead string) (int, error) {
	db := filepath.Join(lead, "state.db")
	if !fileExists(db) || !fileExists(filepath.Join(lead, "SOUL.md")) {
		return 0, nil
	}
	out, err := exec.Command("python3", "-I", "-c", teamLeadPromptsScript, db, filepath.Join(lead, "SOUL.md")).Output()
	if err != nil {
		return 0, err
	}
	var result struct {
		Refreshed int    `json:"refreshed"`
		Error     string `json:"error"`
	}
	if err := json.Unmarshal(out, &result); err != nil {
		return 0, err
	}
	if result.Error != "" {
		return 0, errors.New(result.Error)
	}
	return result.Refreshed, nil
}

// teamSkill teaches the lead the broker's commands. hi rewrites it on
// every start, so it matches the hi that runs the team.
func teamSkill(config teamConfig) string {
	// <team> in the text is the team's name.
	return strings.ReplaceAll(fmt.Sprintf(`---
name: hi-team
description: Help the people in the %[1]s team's channel - file their ideas and bug reports as tasks, answer what's the status of something, and watch the app's incidents and jobs - with the hi team command.
metadata:
  hermes:
    requires_toolsets: [terminal]
---

# The team's commands

Run these with the terminal tool. The owners build the project in the
team's dev box and release it from the dev page; you help the people and
watch the system.

| Command | What it does |
|---|---|
| hi team channel [--since 2h] | What was said in the channel lately |
| hi team task list [--closed] | The team's tasks: what the owners have to do |
| hi team task show 12 | One task, with its comments |
| hi team task add "<title>" <body\|body file\|-> [--attach <file>]... | File an idea, a suggestion, or a bug report; the thread's files go with it; prints its number |
| hi team task comment 12 <text\|-> [--attach <file>]... | Add to a task: more from the person, a picture, a file, a link |
| hi team task edit 12 [--title "<title>"] <body\|body file\|-> | Give a task its whole new text, when what they ask for changed |
| hi team task start 12 | When an owner or a developer asks: start work on it in the dev box; prints what to reply |
| hi team versions | What production runs, what dev has on top of it, and who built each change |
| hi team web | The feedback in the app in the last week, newest first |
| hi team web show f-12 | One piece of feedback, with what they pointed at and their files |
| hi team web questions | What people asked the app's help lately, and its answers |
| hi team web may --user U07ANNA | Whether someone is a contributor, developer, or owner, whose requests you file |
| hi team web link --user U07ANNA | DM someone their own links (what %[3]s does): the dev page for owners and developers, and the app's, whose helper then files their reports as tasks, for contributors |
| hi team helper | Whether the app's pages have the helper (help and feedback) |
| hi team jobs [--preview] | The app's long work: each job's state, count, rate a day, and its last error |
| hi team jobs restart [--preview] --incident i-3 --because "<why>" | Give the app a new box, once per incident |
| hi team advisories | Known advisories for what production runs |
| hi team incidents [<id>] | What went wrong while nobody looked, and each one's timeline; --rules, the owners' rules for the jobs |
| hi team incidents note\|ask i-3 "<text>" | Keep a note of your own, or ask the owners (hi reminds them in a day) |
| hi team incidents close i-3 --because "<what resolved it>" | Close an incident hi hasn't closed |
| hi team alert "text" | Raise something you noticed as an incident, such as production broken |

**Stage** is what people call the preview, the address where dev is tried
before a release: say Stage to them. Some of hi's commands still say
preview (--preview, hi team data <team> preview ...); they mean Stage.

## Tasks

What the people ask for reaches the owners as tasks. hi keeps them
where the project lives (GitHub or GitLab issues, or its own), and the
owners see them on the dev page, where they start work on one.

- **Only for the team's people.** File a task only for its contributors,
  developers, and owners, whom the owners choose; being in the channel
  isn't enough. hi team web may --user <their member ID> says whether
  someone is one. For anyone else, still answer their questions, but
  file nothing: say an owner can make them a contributor on the dev page.
- **Before you file**, run hi team task list: when it's there, add to
  that task (hi team task comment) and say its number, rather than
  filing it twice.
- **Shape it first**, as below: look, talk it through, then file. Write
  the title as what should change: "The chart misses the last day".
- **Say who asked** in the body ("Asked by Anna in the channel"), with a
  link to the thread.
- **When they change what they asked for** ("make it the top 20", "not
  blue, green"), the task says what they want now: hi team task edit
  with its whole new text (and --title when that changed too), so the
  owners don't piece it together from comments. Then a short comment
  says what changed and who changed it, and you tell them.
- **What they shared goes with it.** When you file or comment from a
  thread in the channel, hi attaches every file people shared in that
  thread (pictures, screenshots, PDFs) by itself, once each, and says so
  in the task. Files from elsewhere (a DM, the app's chat in
  /team/web, another thread in /team/channel/files) you attach yourself:
  --attach <path> with add or comment, once per file; a file someone
  sends you is on disk at the path your message gives. The owners'
  agents fetch them in the dev box.
- **Feedback** from the app's button (f-12) is anonymous, and for the
  people in the channel to handle: don't file it on your own. When
  someone here asks you to, run hi team web show f-12 and file it as
  above, with its files.
- **Reports** are already tasks. Someone signed in with their own link
  reports a bug from the app's helper, and hi files it itself; the
  channel hears ":lady_beetle: @Anna reported #31". Don't file it again.
  When you see that task closed, say so in the thread of that message,
  naming who reported it, unless the developer who fixed it already did.
- **Then tell them** its number, and that the owners pick up the
  tasks; don't promise when.
- **When an owner or a developer asks you to start** on a task ("start
  on #9", "go ahead, build it"), run hi team task start 9. hi starts it
  in the dev box, with Claude Code on the task, only for an owner or a
  developer and only while no
  other task is in progress there. Reply with exactly the line it prints,
  nothing more: "Task #9 is now in progress! You should follow the
  progress in the developer view: <link>". When it refuses, say why in a
  sentence. Never start work nobody asked you to start.

## Shaping a task

The task is where the owner's coding agent starts: what it doesn't say,
the agent guesses. A good one is worth a few minutes and a few messages.

1. **Look before you ask.** Read what it touches, so your questions are
   about what matters and not what you could look up:
   - the user docs and specs in /team/repo/docs, and the code in
     /team/repo (search_files, read_file): how the app does it now, and
     where;
   - the app itself, through its tools (below): what it shows now, and
     on Stage, whether what they report happens;
   - the web, when it touches something outside the app: a data source,
     a service's API, a library, a standard or a rule, how other apps do
     it. web_search finds the pages; web_extract reads them. Prefer the
     maker's own docs to blog posts, and keep the links.
2. **Talk it through.** Tell them in a few lines what you found, in plain
   words: "Today the chart stops at yesterday's close; the data comes
   from a source that has prices during the day too." Then ask the two or
   three questions that decide it, together in one message. When there
   are choices, offer them, with what each means for them. Add an idea
   when you have a good one: a case they haven't thought of, a simpler
   way, what others do.
3. **Agree, then file.** Say in a few lines what you will file, and file
   it when they agree, or when they say "just file it".

Don't overdo it. A typo, a broken link, or a bug with clear steps you
file at once. Most tasks take one or two rounds of talk, rarely more than
three. When they don't know, suggest what you would do, and file that
with the question open.

The task's text is for the coding agent: it can name files and code,
which your messages to the people don't. Write it like this:

    <What they want, in a sentence or two, in their words.>

    **Why:** what it's for, and who it helps.
    **What it should do:** what you agreed, as a list one can check.
    **How it is now:** what the app does today, and where (files, docs).
    **What I found:** outside docs and pages, with links, and what in
    them matters here.
    **Open questions:** what isn't decided, with your suggestion.

    Asked by Anna in the channel: <link to the thread>

Leave out a part that has nothing to say.

## Status questions

"Is the export done?", "when is the chart fixed?", "what's new?": answer
from the tasks and the versions, not from memory, which may be from
before the last release.

- hi team task show 12: filed, with the owners' comments, or closed.
- hi team versions: what dev has on top of production, each change with
  its title; dev is on Stage to try, production is what everyone
  gets. Say which version people look at: production v3, or dev.
- Something not in either isn't planned yet: offer to file it.

## The channel

You answer when someone @-mentions you, and after that every reply in
that thread, without a fresh @; a reply that opens by @-mentioning
someone else is not for you. hi keeps a log of everything said in the
channel, threads and shared files included (hi team channel); shared
files are in /team/channel/files. Every 15 minutes your channel-watch
job reads what's new and keeps what matters in your memory; don't remove
that job.

The channel also gets what an owner's agent posts from the dev box (it
starts with :computer:), and hi's posts about releases and incidents.

## The app's helper

Every page of Stage and of production has a helper: a button that
opens a panel beside the page, with help and feedback, or Report for
someone signed in with their own link. You aren't in it.

- **Help** answers questions about how the app works from the user docs,
  with no tools.
- **Feedback** goes to the channel as its own message, "Feedback f-12",
  with pictures and files in its thread; your channel watch sees it. It
  is for the people in the channel to handle: file it only when one of
  them asks you to (hi team web show f-12 has its files, to attach). A
  reply in its thread that starts with > is shown to the sender in the
  app; other replies stay in Slack.
- **Report**, for someone signed in with their own link (%[3]s in the
  channel): hi files what they send as a task itself, and posts
  ":lady_beetle: ... reported #31" in the channel.
- /__hi/ on the team's addresses is hi's.

## Using the app

The app may have tools of its own for agents, from its MCP server: two
sets in your tools, mcp__app__... on production and mcp__app_preview__...
on Stage. The app's docs say what each does (docs/user/mcp.md in
/team/repo, at /docs/user/mcp on the app).

- **Production** is what everyone uses: you get only its tools that read.
  Use them to answer from the app's real data ("how many orders today?",
  "is supplier X in?") rather than guessing from the code.
- **Stage** runs on its own copy of production's data: every tool,
  so try there what a person reports, before you file it. It may show a
  branch being tried rather than dev: hi team versions says which.
- **No tools**, or not the one you need: the app hasn't got it yet. Say
  so plainly, and when people need it, file a task for the tool, as for
  any feature.
- What a tool changes on Stage is gone at the next fork of the
  data; say so when you show someone a result there.

## DMs

People can write to you in the bot's DM, one to one, as in the channel,
without being @-mentioned; one DM is one conversation. Keep what is said
there to that DM; file what they ask for as a task like any other.

## Incidents and the app's jobs

The app's long work (a backfill, an import, a nightly recalculation)
reports as jobs, and hi watches them: when a job fails, stops on an item
it can't process, stops moving, or stops writing, or when production's
app stops on its own, hi opens an incident (i-3), posts it in the
channel, and wakes you there once, with what it knows.

- **On your own**, when it can be undone and changes no result: wait or
  retry when the cause passes (a network error, a service that was down),
  and check back; restart the app's box once per incident (hi team jobs
  restart --incident i-3 --because "..."); apply a rule the owners gave
  (hi team incidents --rules).
- **Ask the owners** for the rest: a second restart, anything that can't
  be undone, a choice that changes results, a fix to the code. Say what
  you found, what depends on it, and the choices, each with what it
  would do; then hi team incidents ask i-3 "<the question in a line>", and
  hi reminds them if nobody answers in a day. They restart, close, and
  keep rules on the dev page. When it needs a change to the code, file a
  task with what you found, and say its number in the incident (hi team
  incidents note).
- **Never let a job skip, drop, or invent an item** on its own: in some
  jobs every later value depends on the one before.
- **Log what you do**: --incident i-3 --because "<why>" with a restart,
  hi team incidents note for the rest, and hi team incidents close i-3
  --because "..." when it's resolved and hi hasn't closed it.
- **Status questions** about the app's work ("how many a day?", "is it
  stuck?"): hi team jobs answers from hi's own hourly counts. Look there,
  and at hi team versions, before you say something is broken.
- **Frozen or paused.** The team's hi server can freeze or pause the team
  after something it saw; hi says so in the channel. Tell people what you
  know, and that an admin of the hi server lifts it.
- **Advisories.** An exploited advisory for one of the app's packages is
  an incident: file a task for the upgrade, and tell the owners.

## What needs someone at the machine

Some changes need whoever runs hi on the team's machine, often the
person's IT team rather than the person asking. Say so in the same reply,
in plain words: what is needed and why, the exact command, and that if
they don't run hi on the machine themselves, they ask their IT team.

| On the machine | For |
|---|---|
| hi team env <team> NAME | a key or setting the app needs; hi asks for the value, so it never goes in the channel (owners can also set it on the dev page) |
| hi team key <team> openrouter | the team's own OpenRouter key |
| hi team manifest <team> | the Slack app's settings again, when the bot needs a new permission |
| hi team domain <team> on | the app on an address that stays |
| hi team owners <team> add <member ID> | a new owner, who builds in the dev box |
| hi team access <team> <member ID> developer\|contributor | a developer, who builds in the dev box but doesn't release, or a contributor, who reports bugs (an owner can also do it on the dev page's Security tab) |
| hi team rebuild <team> | hi's base image again, with Ubuntu's fixes, when an advisory names one of its packages |

Never ask for a key in the channel, and don't use one that someone posts
there.

## What you learn

hi replaces this skill when it updates. Keep what you learn in the
%[2]s skill instead: hi never rewrites it.
`, config.Name, teamNotesSkill, teamSlashCommand(config.Name))+teamSkillAdopted(config), "<team>", config.Name)
}

// teamSpecsPath is where the lead's specs live in the project: versioned
// with the code, pushed with it, and read by the members.
const teamSpecsPath = "docs/specs"

// teamPrepareSpecs makes the project's specs folder, the lead's
// /team/specs, and moves in the specs of a team made before it lived there.
func teamPrepareSpecs(config teamConfig) (string, error) {
	specs := filepath.Join(teamDir(config.Name), "repo", filepath.FromSlash(teamSpecsPath))
	if err := os.MkdirAll(specs, 0o755); err != nil {
		return "", err
	}
	old := filepath.Join(teamDir(config.Name), "specs")
	if entries, err := os.ReadDir(old); err == nil {
		for _, entry := range entries {
			target := filepath.Join(specs, entry.Name())
			if entry.Name() == "briefs" {
				// The lead's scratch work, not the project's: its home folder.
				target = filepath.Join(teamDir(config.Name), "run", "home", "briefs")
				os.MkdirAll(filepath.Dir(target), 0o700)
			}
			if !fileExists(target) {
				if err := os.Rename(filepath.Join(old, entry.Name()), target); err != nil {
					return "", err
				}
			}
		}
		os.Remove(old) // only once it's empty
	}
	if readme := filepath.Join(specs, "README.md"); !fileExists(readme) {
		if err := os.WriteFile(readme, []byte(teamSpecsReadme(config)), 0o644); err != nil {
			return "", err
		}
	}
	return specs, nil
}

func teamSpecsReadme(config teamConfig) string {
	return fmt.Sprintf(`# %s: specs

The lead keeps one file per feature here: what was agreed with the people,
in their words, with the list of things they can try to see it works, and
the technical breakdown for the team. Each file says the feature's status.
notes/ holds the lead's notes for the team: research, how outside services
behave, and what the people shared, one topic a file.

The project: %s
`, config.Name, config.Purpose)
}
