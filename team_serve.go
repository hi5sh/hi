package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
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

func teamServe(name string, stdout, stderr io.Writer) error {
	config, err := loadTeam(name)
	if err != nil {
		return err
	}
	secrets, err := loadTeamSecrets(name)
	if err != nil {
		return fmt.Errorf("the team's Slack tokens: %w", err)
	}
	if err := hermesInstalled(); err != nil {
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
	for _, sub := range []string{proxyDir, brokerDir, home, filepath.Join(dir, "lead"), teamTaskDir(name), filepath.Join(teamChannelDir(name), "files")} {
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
	memorySite := ""
	if err := os.WriteFile(filepath.Join(proxyDir, "allow"), []byte(strings.Join(teamLeadHosts, "\n")+"\n"), 0o600); err != nil {
		return err
	}
	if meta.Image, err = engine.ensureBaseImage(stdout, stderr); err != nil {
		return err
	}
	if err := startBoxNetwork(engine, &meta, proxyDir, "hermes", nil); err != nil {
		teardownBox(engine, meta)
		return err
	}
	defer teardownBox(engine, meta)

	// The lead's long-term memory: Hindsight on the team's network.
	memoryIP := ""
	if site, ok := teamPrepareMemory(engine, config, stderr); ok {
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
	if err := writeTeamLead(config, secrets, meta.ProxyIP, memoryIP); err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	broker := newTeamBroker(name, hi)
	slackChannel := newLiveTeamChannel(secrets.SlackBotToken)
	broker.channel = slackChannel
	serveBroker, err := broker.listen(ctx, filepath.Join(brokerDir, teamSocketName))
	if err != nil {
		return fmt.Errorf("the broker's socket: %w", err)
	}
	brokerErr := make(chan error, 1)
	go func() { brokerErr <- serveBroker() }()
	broker.resume()
	if err := broker.commitSpecs(); err != nil {
		fmt.Fprintf(stderr, "hi: committing the specs: %v\n", err)
	}
	go teamWatchChannel(ctx.Done(), name, config.Channel, slackChannel, stderr)
	go broker.keepLinks(ctx.Done(), stderr)
	go teamEnsureChannelJob(engine, container, filepath.Join(dir, "lead"), ctx.Done(), stderr)

	// The lead's output reaches the journal through this process, so the
	// engine keeps no second copy.
	run := []string{"run", "--rm", "--init", "--log-driver", "none", "--name", container, "--hostname", name + "-lead", "--network", "hi-box-" + meta.Name, "--dns", "127.0.0.1"}
	run = append(run, engine.userArgs()...)
	run = append(run, boxHardening("4g")...)
	run = append(run,
		"-v", home+":/box/home",
		"-v", filepath.Join(dir, "lead")+":/box/home/.hermes",
		"-v", specs+":/team/specs",
		"-v", filepath.Join(dir, "repo")+":/team/repo:ro",
		"-v", teamChannelDir(name)+":"+teamChannelInBox+":ro",
		"-v", brokerDir+":/team/run",
		"-v", hi+":/usr/local/bin/hi:ro",
		"-w", "/team")
	hermesMounts(&run)
	noProxy := "localhost,127.0.0.1," + meta.ProxyIP
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
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		run = append(run, "-e", key+"="+env[key])
	}
	install := hermesInstallDir()
	run = append(run, meta.Image, filepath.Join(install, "venv", "bin", "python"), filepath.Join(install, "hermes"), "gateway", "run", "--accept-hooks")

	// systemd stops the team with SIGTERM; the lead gets time to finish
	// what it is saying.
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM, os.Interrupt)
	defer signal.Stop(signals)
	var stopped atomic.Bool
	go func() {
		if _, ok := <-signals; ok {
			stopped.Store(true)
			engine.output("stop", "-t", "20", container)
		}
	}()
	fmt.Fprintf(stdout, "Team %s: the lead is starting in %s, model %s.\n", name, container, firstNonEmpty(config.Members["lead"].Model, "Hermes' default"))
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

// hermesMounts mounts the host's Hermes and its Python read-only, at the
// same paths.
func hermesMounts(run *[]string) {
	install := hermesInstallDir()
	*run = append(*run, "-v", install+":"+install+":ro")
	// The venv's python is a link to a Python uv installed in the home
	// folder; that comes along read-only at the same path.
	if python, err := filepath.EvalSymlinks(filepath.Join(install, "venv", "bin", "python")); err == nil {
		home, _ := os.UserHomeDir()
		if root := filepath.Dir(filepath.Dir(filepath.Dir(python))); strings.HasPrefix(root, home+"/") {
			*run = append(*run, "-v", root+":"+root+":ro")
		}
	}
}

// writeTeamLead writes what hi owns in the lead's Hermes home on every
// start: its settings, its .env, and the hi-team skill. SOUL.md and the
// memories are the lead's own and are left alone.
func writeTeamLead(config teamConfig, secrets teamSecrets, proxyIP, memoryIP string) error {
	lead := filepath.Join(teamDir(config.Name), "lead")
	host := readHermesModelConfig(filepath.Join(hermesHome(), "config.yaml"))
	model := firstNonEmpty(config.Members["lead"].Model, host.Default)
	settings := "model:\n"
	if model != "" {
		settings += "  default: " + model + "\n"
	}
	settings += fmt.Sprintf("  provider: custom\n  base_url: http://%s:%d/api/v1\n  api_key: %s\n  api_mode: %s\n", proxyIP, boxInjectPort, boxClaudePlacehold, firstNonEmpty(host.APIMode, "chat_completions"))
	settings += "terminal:\n  backend: local\n  cwd: /team\n"
	settings += "approvals:\n  mode: \"off\"\n"
	settings += "updates:\n  check: false\n"
	settings += "plugins:\n  auto_update_check_hours: 0\n"
	if memoryIP != "" {
		settings += "  enabled:\n    - " + teamHindsightPlugin + "\n"
		settings += "memory:\n  provider: " + teamHindsightPlugin + "\n"
	}
	// No "Hermes is shutting down" or "back online" posts in the channel on
	// every restart of the team.
	settings += "gateway:\n  platforms:\n    slack:\n      gateway_restart_notification: false\n"
	// A scheduled job's message is just its text, without Hermes' "Cronjob
	// Response" heading and "To stop or manage this job" line.
	settings += "cron:\n  wrap_response: false\n"
	if err := os.WriteFile(filepath.Join(lead, "config.yaml"), []byte(settings), 0o600); err != nil {
		return err
	}
	env := []string{
		"OPENROUTER_API_KEY=" + boxClaudePlacehold,
		"SLACK_BOT_TOKEN=" + secrets.SlackBotToken,
		"SLACK_APP_TOKEN=" + secrets.SlackAppToken,
		// One channel, and no direct messages.
		"SLACK_ALLOWED_CHANNELS=" + config.Channel,
		"SLACK_HOME_CHANNEL=" + config.Channel,
		"SLACK_DISABLE_DMS=true",
		// It answers only when @-mentioned, even in its own threads; the
		// broker logs the rest of the channel for it (team_channel.go).
		"SLACK_REQUIRE_MENTION=true",
		"SLACK_STRICT_MENTION=true",
	}
	if len(config.Askers) == 0 {
		env = append(env, "SLACK_ALLOW_ALL_USERS=true")
	} else {
		env = append(env, "SLACK_ALLOWED_USERS="+strings.Join(append(append([]string{}, config.Owners...), config.Askers...), ","))
	}
	if err := os.WriteFile(filepath.Join(lead, ".env"), []byte(strings.Join(env, "\n")+"\n"), 0o600); err != nil {
		return err
	}
	if !fileExists(filepath.Join(lead, "SOUL.md")) {
		if err := os.WriteFile(filepath.Join(lead, "SOUL.md"), []byte(teamSoul(config)), 0o600); err != nil {
			return err
		}
	} else if err := updateTeamSoul(filepath.Join(lead, "SOUL.md")); err != nil {
		return err
	}
	if memoryIP != "" {
		if err := writeTeamHindsightConfig(lead, config.Name, memoryIP); err != nil {
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
func teamEnsureChannelJob(engine boxEngine, container, lead string, done <-chan struct{}, stderr io.Writer) {
	jobs := filepath.Join(lead, "cron", "jobs.json")
	install := hermesInstallDir()
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
		out, err := engine.output("exec", container, filepath.Join(install, "venv", "bin", "python"), filepath.Join(install, "hermes"),
			"cron", "create", "every 15m", teamChannelPrompt, "--name", teamChannelJob, "--script", teamChannelScript,
			"--skill", "hi-team", "--deliver", "local", "--failure-deliver", "local")
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

// teamManifest is the team's Slack app: Socket Mode, one channel, no
// direct messages or assistant view.
func teamManifest(config teamConfig) string {
	description := firstNonEmpty(config.Purpose, "A hi team")
	if len(description) > 139 {
		description = description[:136] + "..."
	}
	manifest := map[string]any{
		"display_information": map[string]any{"name": teamBotName(config.Name), "description": description, "background_color": "#1a1a2e"},
		"features": map[string]any{
			"bot_user": map[string]any{"display_name": teamBotName(config.Name), "always_online": true},
		},
		"oauth_config": map[string]any{"scopes": map[string]any{"bot": []string{
			"app_mentions:read", "channels:history", "channels:read", "chat:write", "files:read", "files:write",
			"groups:history", "groups:read", "reactions:read", "reactions:write", "users:read",
		}}},
		"settings": map[string]any{
			"event_subscriptions":    map[string]any{"bot_events": []string{"app_mention", "message.channels", "message.groups", "reaction_added", "reaction_removed"}},
			"interactivity":          map[string]any{"is_enabled": true},
			"org_deploy_enabled":     false,
			"socket_mode_enabled":    true,
			"token_rotation_enabled": false,
		},
	}
	data, _ := json.MarshalIndent(manifest, "", "  ")
	return string(data)
}

func teamPeople(ids []string) string {
	var mentions []string
	for _, id := range ids {
		mentions = append(mentions, "<@"+id+">")
	}
	return strings.Join(mentions, ", ")
}

// teamSoul is the lead's identity, written once; owners may edit it.
func teamSoul(config teamConfig) string {
	var roles []string
	for _, role := range teamRoles(config)[1:] {
		roles = append(roles, fmt.Sprintf("- the %s: %s", role, teamMemberLabel(config.Members[role])))
	}
	return fmt.Sprintf(`# The %[1]s team's lead

You lead the %[1]s team, a virtual team of agents that lives in one Slack
channel and builds one project for the people in it.

The project: %[2]s

The team's owners are %[3]s. They decide what matters most, and only they
may ask you to stop, undo, or change how the team works.

## The people

The people in the channel may not read code. They may be experts in their
own field who want something that works. You bridge the gap:

- Talk with them in plain language. No code, file names, or jargon unless
  they ask for it.
- When you need a decision, ask it as a choice about what the project
  should do, never as a question about code.
- Keep them posted in short messages at the moments that matter to them:
  when work starts, when something is ready to try, and when you need a
  decision; not each step in between.
%[5]s
## Your team

You don't write code yourself. You hand tasks to the members:

%[4]s

The hi-team skill says how: hi team task, wait, and merge.

## How work is done

1. Agree with the people what will change and how they will know it works:
   a short list of things they can try. That is the acceptance list. Ask
   whether it's right, end your reply, and wait for their yes before you
   start anything (the hi-team skill says how to pass it on).
2. Write the feature's spec in /team/specs/<feature>.md: the acceptance
   list in their words, then the technical breakdown for the team.
3. Give the coder a brief with everything it needs: the goal, the
   acceptance list, the files or areas, and the tests that must pass.
4. Wait for the task. The team has the reviewer check the work, and the
   coder fix what the reviewer finds, on its own, up to three rounds; you
   hear once, when it is approved or still needs changes. Don't relay the
   reviewer's notes yourself.
5. Merge only after the reviewer approves. If changes are still needed
   after the rounds, read the review and decide: brief the coder again
   (--on), or ask the people.
6. Run hi team preview, post its link, tell the people
   what changed and what to try, and ask them to confirm it works. A feature is done when they agree it does. A bug
   they report becomes a new task for that feature.
7. Merged work is in dev, not production. When the people are happy with
   dev, ask an owner whether to release it; only their yes puts it in
   production, as the next version (the hi-team skill says how). Always
   say which version people are looking at: production v3, or dev.

Small fixes, such as a typo or a wrong label, needn't wait for the people
to agree first (hi team task --small): fix, wait for the review, merge, and tell
them what changed.

## What you keep

You hold the project's memory. Keep /team/specs current: one file per
feature, with its status. It is the project's docs/specs folder: hi commits
your changes when a task starts and when work is merged, so the members see
them and they go wherever the code goes. /team/repo is the project as merged, read-only
for you: read it to answer questions about how things work.
`, config.Name, config.Purpose, teamPeople(config.Owners), strings.Join(roles, "\n"), teamSlackFormatting)
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

// teamSoulUpdates are paragraphs an earlier hi wrote in a lead's SOUL.md,
// with what this hi says instead. SOUL.md is the lead's to edit, so hi
// replaces a paragraph only where it is still word for word its own.
var teamSoulUpdates = []struct{ old, new string }{
	// Posts at milestones, not at each step.
	{`- Keep them posted in short messages: what you're doing, what's next, and
  when to try something.`, `- Keep them posted in short messages at the moments that matter to them:
  when work starts, when something is ready to try, and when you need a
  decision; not each step in between.`},
	// The broker runs the review rounds.
	{"The hi-team skill says how: hi team task, review, wait, and merge.",
		"The hi-team skill says how: hi team task, wait, and merge."},
	{`4. Have the reviewer check the work. If it needs changes, give the coder a
   new task on the same branch (--on) with the reviewer's notes, then have
   it reviewed again.
5. Merge only after the reviewer approves.`, `4. Wait for the task. The team has the reviewer check the work, and the
   coder fix what the reviewer finds, on its own, up to three rounds; you
   hear once, when it is approved or still needs changes. Don't relay the
   reviewer's notes yourself.
5. Merge only after the reviewer approves. If changes are still needed
   after the rounds, read the review and decide: brief the coder again
   (--on), or ask the people.`},
	{`(hi team task --small): fix, review, merge, and tell them
what changed.`, `(hi team task --small): fix, wait for the review, merge, and tell
them what changed.`},
	// Links that Slack can open.
	{`  decision; not each step in between.
`, `  decision; not each step in between.
` + teamSlackFormatting},
}

// updateTeamSoul brings an existing SOUL.md's words about how the team
// works up to date with this hi.
func updateTeamSoul(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	text := string(data)
	for _, update := range teamSoulUpdates {
		// An update may add to a paragraph, so the old text stays in it.
		if !strings.Contains(text, update.new) {
			text = strings.Replace(text, update.old, update.new, 1)
		}
	}
	if text == string(data) {
		return nil
	}
	return os.WriteFile(path, []byte(text), 0o600)
}

// teamSkill teaches the lead the broker's commands. hi rewrites it on
// every start, so it matches the hi that runs the team.
func teamSkill(config teamConfig) string {
	var roles []string
	for _, role := range teamRoles(config)[1:] {
		roles = append(roles, role)
	}
	return fmt.Sprintf(`---
name: hi-team
description: Hand tasks to the %[1]s team's members (%[2]s), merge their reviewed work into dev, and release dev to production, with the hi team command.
metadata:
  hermes:
    requires_toolsets: [terminal]
---

# Working with the team

Run these with the terminal tool. Members work on the host, each in a box
of its own on a branch of the project; you get their reports.

| Command | What it does |
|---|---|
| hi team task <role> <brief.md> --approval <id> | Start new work the people said yes to; prints its ID, such as t-4 |
| hi team task coder <brief.md> --small | Start a small fix, such as a typo, without asking first; hi tells the channel |
| hi team task coder <brief.md> --on t-4 | Continue t-4's latest work on its branch, for example with more from the people |
| hi team task ... --attach <file> | Send a file with the task, such as an image shared in the channel; repeat for more |
| hi team wait t-4 | Wait for t-4, its review, and the fixes the review asked for; print how it ended |
| hi team show t-4 | Print the same without waiting |
| hi team tasks | List the tasks |
| hi team merge t-4 | Merge t-4's latest work into the project once the reviewer approved it |
| hi team review t-4 [<notes.md>] | Have the reviewer check t-4 again, with your notes; rarely needed |
| hi team stop t-4 | Stop t-4, or whichever of its reviews or fixes is working |
| hi team preview | Show the project's main branch on the preview link, for the people to try |
| hi team preview t-4 | The same for a task's branch, before it is merged |
| hi team preview stop | End the preview |
| hi team preview production | Production's own link, and hi's message in the channel that keeps it current |
| hi team versions | Production's version, what dev has on top of it, and the ideas not merged |
| hi team release --approval <id> --notes <file> | Put dev into production as the next version, once an owner says yes; hi posts it in the channel |
| hi team release v2 --approval <id> | Put production back on v2, once an owner says yes |
| hi team discard t-4 | Drop an idea that isn't merged: its tasks, reviews, and branches |
| hi team discard dev --approval <id> | Set dev's code back to production's, once one of the people says yes |

Members: %[2]s.

## Getting a go-ahead

New work starts only after one of the people says yes. Propose the plan
and the acceptance list, ask whether it's right, and end your reply there:
don't write the brief or start a task in the same reply. When they answer
yes, find their message's id with hi team channel --since 1h --ids (the
id in brackets after the name) and pass it: --approval <id>. hi checks that
the message is theirs and answers your question, and refuses the task
otherwise. Continuing a task (--on) needs no new go-ahead, and a small fix
(--small) needs none, but hi tells the channel it started.

## The channel

You answer only when someone @-mentions you, but hi keeps a log of
everything said in the channel, threads and shared files included:

| Command | What it does |
|---|---|
| hi team channel | What was said in the last day |
| hi team channel --since 2h | ... in the last two hours (or 30m, 3d) |

Shared files are in /team/channel/files. When someone @-mentions you,
look at what was said before it if the question builds on it. Every 15
minutes your channel-watch job reads what's new and keeps what matters in
your memory; don't remove that job.

## What you learn

hi replaces this skill when it updates. Keep what you learn in the
%[3]s skill instead: hi never rewrites it.

## Specs

Edit a spec with patch, or read the whole file first before rewriting it:
a merge may have changed it since you last read it. hi keeps your specs as
you left them when a member's work changes them, and says so after the
merge.

## Briefs

Write each brief to a file in your home folder first, for example
~/briefs/t-next.md, then pass its path. Not in /team/specs: that is the
project's documentation, committed with the code. A brief starts with a one-line title, then says:

- the goal, in one or two sentences;
- the acceptance list, and the tests that must cover it;
- where in the project the work goes, if you know;
- what not to change.

The member sees only the brief, the project, and the files that come with
the task, not this conversation. Everything the people gave you must reach
it:

- hi adds the thread you were approved in to the brief, in the people's
  words with their links, and gives the member the files shared in it.
- Attach everything else yourself with --attach <file>: images, documents,
  or recordings shared in another thread or earlier in the channel (they
  are in /team/channel/files; hi team channel shows which is which), and
  pages or files you fetched. Say in the brief what each one is for.
- Put every link the people gave in the brief, whole, and say what it
  shows. Members can open web pages; they can't open Slack.
- A follow-up (--on) gets the files of the task it continues, and the
  brief that work started with.

## Waiting

A task takes minutes to an hour, longer when the reviewer asks for
changes. Start hi team wait <id> with background=true and notify=true, tell
the channel once what's happening, and carry on; you are told when it
finishes. If it prints "Still working", wait again, without telling the
channel about each review round: the people hear when it's ready to try.

## Previews

After a merge, run hi team preview. It shows the new version on the
team's preview link, and says whether that link is new:

- **The same link as before:** the people's link shows the new version
  now. Tell them to reload it, and which version it is; don't post the
  link again.
- **A new link:** post it whole, its key is part of it, so the people just
  open it, on their phones too, and try the acceptance list. There is no
  password.

A preview link stays the same for up to a day: a new preview gets a new
link only when the old one has less than 12 hours left. Never tell the
people to run commands or start the app themselves.

Production has a link of its own, which hi keeps working: a release moves
it to the new version, and hi renews it each day. hi keeps one message in
the channel with production's current link, and links to it when it posts
a release. When people ask where production is, point them to that
message (hi team preview production prints its link) rather than posting
the link itself: a link you post stops working when it is renewed, and
hi can't edit your messages.

The project starts with make run, or npm start, and must listen on
0.0.0.0 at the port in $PORT. If the preview says the app didn't answer,
give the coder a task to fix that, with the lines it printed; the link
still shows the version before. If the app only listens on a fixed port,
pass it: hi team preview --port 5173.

Don't start the app in your own box: nobody can reach it there.

## Dev and production

There are two versions of the project, and the people must always know
which one they are looking at:

- **Dev** is what you merge into. The people try it in previews and say
  what works; it may have things that are thrown away later.
- **Production** is the version the people rely on. It changes only by a
  release, which needs an owner's yes. Each release has a number: v1, v2,
  and so on.

Name the version every time you post a preview link or say what changed,
in the words hi team versions and hi team preview print: "production v3",
or "dev, v3 + 2 changes", or "an idea, not in dev yet". Never call dev
"live" or "released".

When the people are happy with dev, ask an owner whether to release it:
say what production gets that it hasn't now, in their words, end your
reply, and wait. Once an owner says yes, write the release notes to a file
(a few lines for the people, not code) and run hi team release --approval
<their message id> --notes <file>. hi posts the release in the channel
itself, not in the thread, so everyone sees it. If a release turns out
badly, an owner may ask to go back: hi team release v2 --approval <id>.

## Ideas

Each new task starts an idea; continuing it (--on) doesn't. At most
5 ideas may be unmerged at once, and hi refuses a sixth. Don't let them
linger: when an idea didn't work out, or the people moved on, hi team
discard it and say so. When something merged into dev turns out wrong and
the people want all of dev's changes since production gone, ask them, then
hi team discard dev --approval <id>: dev's code goes back to production's,
and hi posts it in the channel. To drop just one merged change, give the
coder a task to take it out instead.

## Reviews and merging

A review's report starts with Approve or Changes needed. hi team merge
refuses work that no review approved, and work changed since its review.
After a merge, tell the people what changed. If a merge doesn't apply
cleanly, give the coder a new task to redo the work on the latest project.
`, config.Name, strings.Join(roles, ", "), teamNotesSkill)
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

The project: %s
`, config.Name, config.Purpose)
}
