# Roadmap

Implementation order for `hi`. Completed releases use `[x]`; planned work uses
`[ ]`. Detailed behavior and acceptance criteria live in `docs/specs/`.

Only work with an accepted specification in `specs/approved/` is listed below.
Drafts in `specs/ideas/` remain outside the roadmap until approved.

## Released

### v0.1.0 — Initial workstation support

- [x] Install the CLI from checksummed amd64 and arm64 release assets.
- [x] Configure and verify an Ubuntu Strix Halo workstation.
- [x] Create users with render and video access.

### v0.2.0 — General workstation installation

- [x] Install the non-hardware workstation software through `hi install`.

### v0.3.0 — NetBird enrollment

- [x] Connect a machine with `hi net <setup-key>`.

### v0.4.0 — NetBird device naming

- [x] Prompt for the NetBird device hostname and show the machine hostname as
  the default.

### v0.5.0 — NetBird security and lifecycle

- [x] Secure NetBird enrollment and add lifecycle commands.
  Approved spec: [hi_net.md](specs/approved/hi_net.md).

### v0.5.1 — AI coding tools

- [x] Install Claude Code, Codex CLI, and herdr, and verify their commands.

### v0.5.2 — Unattended installation

- [x] Install Codex without its interactive launch prompt.
- [x] Run `hi` straight from the installer with `sh -s -- <command>`.

### v0.6.0 — Colab compute

- [x] Start, list, inspect, reach, and stop Colab instances with `hi compute`:
  `up`, `ls`, `status`, `ssh`, `tunnel`, `logs`, `stop`, `hardware`, and
  `providers`, plus a guided menu for bare `hi compute`.
- [x] Enforce a maximum lifetime with a detached watcher and at every
  `hi compute ls`; confirm paid hardware; validate hardware names before
  calling Colab.
- [x] Run Python scripts to completion with `hi compute run`.
- [x] Serve a GGUF model with llama.cpp and tunnel its OpenAI-compatible API
  with `hi compute serve`, including the tested Qwen3.8-Flash-Next recipe.
- [x] Install the Colab CLI with `hi install`.
- [x] Verify against a live Colab account: CPU `up`, `ssh`, `tunnel`, `run`,
  and `stop`, and the Qwen3.8-Flash-Next recipe on a G4 (about 7 minutes to
  ready, ~84 tokens/s, 0.93 units for the whole test).
  Approved spec: [hi_compute.md](specs/approved/hi_compute.md).

Dependency: the Colab CLI (`google-colab-cli`) and a Colab Pro or Pro+ plan,
which SSH requires.

### v0.7.0 — Hugging Face compute

- [x] Install the `hf` CLI and add `hi login hf` (and `hi login colab`).
- [x] Run, list, follow, wait for, and stop Hugging Face Jobs with
  `hi compute run`, calling the Jobs REST API directly: Python scripts or
  container images, secrets, `--detach`, and `--namespace`.
- [x] Start and reach Hugging Face instances with `hi compute up`, `ssh`,
  `tunnel`, and `logs`; serve models with `hi compute serve`.
- [x] Infer the provider from the hardware name when both are signed in.
- [x] Verify against a live account (the `acme` organization): runs,
  detached runs, `up`/`stop`, and `serve` on a `t4-small` (ready in about a
  minute, ~220 tokens/s for Qwen3-0.6B), for under $0.05. SSH through the Jobs
  gateway still needs a registered key to test.
  Approved spec: [hi_compute.md](specs/approved/hi_compute.md).

Dependencies: the v0.6.0 driver interface, state, and SSH layer; the
token-read exception in [hi_login.md](specs/approved/hi_login.md).

### v0.7.1 — Agent skill, billing, guide, and self-update

- [x] Write a `hi` agent skill for Claude Code and Codex with `hi skill`,
  into the current folder or, with `--global`, the home folder; one copy in
  `.agents/skills`, linked from `.claude/skills`.
  Approved spec: [hi_skill.md](specs/approved/hi_skill.md).
- [x] Choose who pays for Hugging Face Jobs automatically, and show or save
  it with `hi compute billing`.
- [x] Close tunnels whose instance was stopped even when the SSH gateway
  stays up; verify Hugging Face SSH and tunnels live.
- [x] Publish a searchable, multi-page guide at https://hi5.sh/guide/ with
  step-by-step setup, examples, and a command reference.
- [x] Add atomic, checksummed self-updates with `hi update`.
  Approved spec: [hi_update.md](specs/approved/hi_update.md).
- [x] Accept bare hours for `--max`, and `--max none` for no limit after a
  confirmed warning.

### v0.7.2 — Styled compute menu

- [x] Give `hi compute`'s guided menu a styled terminal UI: arrow-key menus,
  filtering, validated inputs, a record of each answer, and a cost summary
  that defaults to No before anything starts.

### v0.7.3 — Update without the GitHub API

- [x] Find the latest release from GitHub's `/releases/latest` redirect, so
  `hi update` works when the API's hourly limit for the network is used up.

### v0.7.4 — Never downgrade

- [x] A bare `hi update` only installs a newer release, even while GitHub's
  latest-release redirect still points at the previous one.

### v0.8.0 — RunPod compute

- [x] Add a RunPod driver through its REST API v2 (v1 retires on
  2026-11-15): hardware with prices, `up`, `ls`, `status`, `ssh`, `tunnel`,
  `logs`, and `stop` (which terminates).
- [x] Enforce `--max` with a watchdog on the pod as well as the local watcher.
- [x] Add `hi login runpod`.
- [x] Ask for the API key in `hi login runpod` and in the menu, and save it
  in runpodctl's `~/.runpod/config.toml`.
- [x] Show which GPUs are free and suggest free alternatives when a start
  fails.
- [x] Verify against a live RunPod account: SSH, tunnel, and a pod that
  terminated itself at `--max` with no local watcher.
  Approved spec: [hi_compute.md](specs/approved/hi_compute.md).

Dependency: extends the v0.6.0 and v0.7.0 driver interface.

### v0.8.1 — Pick what to stop

- [x] A bare `hi compute stop` lists what is running and stops the chosen
  instance, or all of them, after confirming.

### v0.8.2 — Steady menus

- [x] Menu lists stay still while the cursor moves; long lists scroll only
  at the edge.

### v0.9.0 — Managed compute: server and brokered RunPod

- [x] Add `hi server` on a dedicated box in the VPN. It holds the provider
  keys, makes every provider call for connected devices, and keeps state in
  a JSON file with an audit log.
- [x] Add `hi connect` and `hi disconnect`, with an ed25519 device key that
  signs every request and is pre-approved by an admin.
- [x] Add a managed driver, so that connected devices start, list, reach,
  and stop RunPod through the server with the same `hi compute` commands.
- [x] Record a lease for every start. A reconciler stops instances at their
  `--max` and reports instances that have no lease.
- [x] Approve, deny, and stop from the server box with `hi server approve`,
  `deny`, and `stop`.
- [x] Make the request flow work for agents: `--reason`, `--no-wait`,
  `hi compute requests --wait`, exit statuses for pending and denied, and
  nobody approves their own request.
- [x] Keep a never-connected `hi` exactly as it is today. Colab is never
  managed.
- [x] Document it in the guide and teach it to agents in `hi skill`.
- [x] Verify with a live RunPod account over NetBird: enroll, approve, SSH,
  tunnel, stop, and a pod stopped by the server at `--max` (2026-09-29,
  aiw12 serving aiw9).
  Approved spec: [hi_server.md](specs/approved/hi_server.md).

### v0.9.1 — Visible key entry

- [x] Key and token prompts (`hi server provider add`, `hi login runpod`,
  `hi net`) show a `*` per character, so a paste is visibly received.
- [x] `hi server provider add` works before the server is started, and checks
  the provider name before asking for the key.

### v0.9.2 — Approvals survive sold-out hardware

- [x] When approved hardware is sold out, the server starts the cheapest free
  hardware with at least as much memory at up to twice the approved price
  (`fallback_price_factor` in the server's `config.json`), instead of failing
  and needing a second approval.
- [x] Audit entries stay on one line, and stops read `ran 2m, started by
  iman`.

### v0.10.0 — Managed compute: Slack

- [x] Add a Slack app over Socket Mode, with a manifest printed by
  `hi server slack manifest` and tokens stored by `hi server slack setup`.
  It posts enrollment and compute approval buttons, stop buttons, message
  states, and threaded alerts, and answers `/hi status` and `/hi stop`.
- [x] Add `hi server approvers`, which maps Slack members to `hi` users, so
  nobody approves their own request from Slack either.
- [x] Verify with a live Slack workspace (2026-09-29): approve from a
  button, a sold-out L4 replaced by an RTX A5000, and a Stop click.
  Approved spec: [hi_server.md](specs/approved/hi_server.md).

### v0.10.1 — Clearer Slack messages

- [x] A request that fails because hardware is sold out lists what is free
  now with as much memory, and its price.
- [x] Final Slack messages keep who approved them, and costs under a cent
  read "under $0.01".

### v0.10.2 — Friendlier /hi

- [x] `/hi stop @alice` and `/hi stop user @alice` accept Slack mentions, and
  `/hi stop <user>` stops that user's instances when no instance has the name.
- [x] After a stop from a private `/hi` answer, the answer says what was
  stopped instead of keeping a live button.
- [x] `/hi status` says "Nothing is waiting for a decision" instead of "0
  waiting".

### v0.10.3 — Slack shows user text as typed

- [x] Requesters' reasons, hostnames, and provider errors show in Slack
  exactly as typed: a reason can no longer mention people, ping `@channel`,
  or fake a link.
- [x] `/hi status` lists instances that are still starting, and `/hi stop`
  explains when the one you asked for hasn't finished starting.

### v0.10.4 — Say how long starting takes

- [x] After an approval, the message says starting takes 1–3 minutes and
  turns 🟢 when ready, shows the provider's progress, and the thread notes
  the approval at once.

### v0.10.5 — Running times that stay true

- [x] A running instance's Slack message shows "running since 12:56, stops
  at 13:06" instead of a running time that goes stale.

### v0.10.6 — More room to replace cheap hardware

- [x] A replacement for sold-out hardware may cost up to twice the approved
  price or the approved price plus $1.00/h, whichever is higher
  (`fallback_price_factor`, `fallback_price_extra`), so cheap GPUs find a
  replacement while expensive ones stay near 2×.

### v0.11.0 — Managed compute: policy, budgets, and reports

- [x] Add `policy.json` with `hi server policy show|edit|example|check`:
  per group `max_hours`, allowed `hardware`, `auto_approve`, and monthly
  budgets per user and per group. It applies on the next request.
- [x] Budgets warn and never block: over-budget requests show ⚠️ and the
  spend, always wait for a person, and the channel gets one alert a month;
  users see the spend in `hi connect status` and `hi compute ls`.
- [x] Add `hi compute extend`, approved like a start.
- [x] Answer `/hi spend`, `/hi users`, `/hi budget`, and `/hi audit`, add
  `hi server spend`, and post daily, weekly, and monthly reports.
- [x] Label requests from agents (`iman via Claude Code`), and add
  `hi connect --agent <name> --owner <user>` for agents that run on their
  own.
- [x] Verify live with a policy on aiw12 (2026-09-29): a staff start
  approved by policy, then stopped by the server at its limit.
  Approved spec: [hi_server.md](specs/approved/hi_server.md).

### v0.11.1 — Say who approved once

- [x] Waiting on a request prints "Approved by …" once, then the start's
  progress, instead of repeating the approver on every line.

### v0.12.0 — Shadeform

- [x] Add a Shadeform driver through its REST API (`X-API-KEY`): one
  integration reaches GPUs from many clouds (Hyperstack, Massed Compute,
  Lambda, Scaleway, Paperspace, Vultr, and more). Each GPU type is listed
  once at its cheapest free on-demand offer, with the cloud and region;
  `h100@lambdalabs` picks a cloud.
- [x] Register the user's SSH key with Shadeform once, set Shadeform's own
  `auto_delete` at `--max` so machines stop even with the laptop off, and
  delete on `stop`.
- [x] Add `hi login shadeform`, and let `hi server provider add shadeform`
  manage it, including replacements for sold-out offers from other clouds.
- [x] Record an instance's start when it is created, not when it is ready.
- [x] Verify live: an A4000 on Hyperstack in Oslo, booted in 3.5 minutes,
  SSH as `shadeform`.

### v0.12.1 — One row per managed instance

- [x] With two managed providers, `hi compute ls` listed each managed
  instance under both; each provider now lists only its own.
- [x] Verify managed Shadeform live: a staff start approved by policy on
  aiw12, SSH to the machine, and a stop.

### v0.12.2 — RunPod Community Cloud

- [x] Add RunPod Community Cloud as an opt-in: `--gpu rtx-4090@community`
  (25–54% cheaper than Secure Cloud, still on-demand), listed with
  `hi compute hardware --on runpod --community`.
- [x] Warn that no API tokens, passwords, SSH private keys, or sensitive data
  may ever be put on a Community machine: before every start (even with
  `--yes`), on every `ssh`, `tunnel`, `logs`, and `serve`, on the Slack
  request, and in the agent skill.
- [x] Never move an approved Secure Cloud start onto Community Cloud when
  hardware is sold out; Community starts are replaced only on Community.

### v0.13.0 — Managed compute: live dashboard

- [x] Add `hi server live` for admins on the server box: running machines
  with cost and a bar towards each limit, waiting requests, budgets, and
  activity, updated every second; `a`, `d`, and `s` approve, deny, and stop.
- [x] Add `--wall`: read-only, initials and no reasons by default, fills the
  screen and rotates what doesn't fit, dims while reconnecting.
- [x] Run the wall in a tmux session as a separate `hi-wall` account that
  screens attach to read-only over SSH, set up by `sudo hi server wall`.
- [x] Add viewer devices (`hi server viewer add`), which may only watch, and
  `hi compute live` for users' own machines.
- [x] Report client activity (`ssh`, `tunnel`, `logs`, `serve`) to the
  dashboard, with command and instance names only.
- [x] Verify the wall live on aiw12 (2026-09-29): a screen attached over
  SSH saw the dashboard; a shell command and a port forward were refused.
  Approved spec: [hi_server.md](specs/approved/hi_server.md).

### v0.13.1 — A cleaner wall

- [x] On a wall, only people's names become initials: "stopped at its time
  limit" no longer reads "time L.".
- [x] The wall's tmux session hides the status bar.

### v0.14.0 — Managed compute: Slack App Home

- [x] Add an App Home dashboard: approvers see Now (with Stop and Stop
  all), Waiting (with Approve and Deny), This month, and Devices with their
  `hi` version, flagged when outdated or silent.
- [x] Let people link their Slack account with `/hi link <user>` and
  `hi connect slack <code>` from their own device; linked users see their
  own machines in App Home and can stop them.
- [x] Send linked users direct messages when their requests are approved,
  denied (with the reason), started, near their limit, or stopped.
- [x] Verify live after updating the Slack app's manifest (2026-09-29):
  Home tab, `/hi link`, and messages about a request.
  Approved spec: [hi_server.md](specs/approved/hi_server.md).

### v0.14.1 — Home says starting

- [x] The Home tab shows an approved request that is still starting as
  "approved, starting", not as waiting.

### v0.15.0 — Guided installer

- [x] Replace the fixed `hi install` run with a menu of checkboxes, each with
  a short description, that selects which tools to install. Unchecking an
  installed tool uninstalls it, so the same menu serves as both installer and
  uninstaller.
- [x] Install and remove named tools without a terminal with
  `hi install <tool>...`, `hi install --all`, and `hi uninstall <tool>...`.

### v0.16.0 — Project bootstrap

- [x] Add built-in templates under `templates/`, embedded in `hi`: a hidden
  `base` layer (agent instructions, `make check`, CI, secret scanning), and
  the well-known `python` (uv, ruff, pyrefly, pytest) and `web` (React and
  TypeScript on Vite, npm, Biome, Vitest) templates.
- [x] Add `hi init`, guided and direct, with `--list`, `--dry-run`, `--yes`,
  `--no-setup`, and `--github`, recording what it generated in
  `.hifin/template.json`.
- [x] Read a local source from `HI_TEMPLATES_DIR` for template authors.
- [x] CI generates every built-in template, runs its `make check`, and
  rejects internal names under `templates/`.
  Approved spec: [hi_init.md](specs/approved/hi_init.md).

Dependency: `uv` for `python`, Node.js with npm for `web`. Nothing
private or quant-related goes in the built-in templates.

### v0.17.0 — Private templates through `hi server`

- [x] Give the server an ed25519 key, store it on devices at `hi connect`,
  and sign template bundles with it.
- [x] Add `hi server templates add|remove|list|sync`: mirror a private
  repository with a read-only token, check every commit, fetch every 15
  minutes, and log and alert on changes.
- [x] Serve the catalog and signed bundles to enrolled devices, limited per
  group by `template_sources` in policy.
- [x] List and generate server templates in `hi init`, cached per commit,
  with server layers extending built-in ones.
- [x] Move the quant work into the private `acme/templates`: the
  `research` template with its backtest guards, and the private skills
  `time-series-validity` and `backtest-evaluation`. Layers can `remove`
  files they inherit.
- [x] The `data-access` skill (2026-10-12), once `hi data` settled data
  access: the team's data is its Hugging Face organizations, through `hi data`.
- [x] Verify live on vmhiserver (2026-10-01): the server signed and served
  `acme/templates` at 7362a3e, a laptop stored its key, and
  `hi init research` passed `make check`. The server reads GitHub with a
  fine-grained, read-only token for that one repository (the `acme` org
  has deploy keys turned off), and a new commit reached the laptop after
  `hi server templates sync`.
  Approved spec: [hi_server.md](specs/approved/hi_server.md#template-sources);
  approved spec: [hi_init.md](specs/approved/hi_init.md).

Dependency: the v0.16.0 layers and metadata; the open decisions
(backtest engine, data access, scheduling) are tracked in
`acme/templates`.

### v0.17.1 — Template syncs that can't hang

- [x] Every git command for template sources gives up after 2 minutes, or
  after 30 seconds below 1 KB/s, so a stuck fetch no longer blocks
  `hi server templates` behind it.

### v0.17.2 — Call it the private repo

- [x] `hi init`'s menu marks server templates `(private repo)` instead of
  the source's name.
- [x] Add `hi server templates rename`, which keeps a source's token and
  history; the firm's source is now called `private`.
- [x] Docs speak of private templates and the team, and the planned
  `firm-data` skill is now `data-access`.
- [x] v0.17.3: `hi init` forgets cached sources the server no longer
  offers, so a renamed source isn't listed twice when the server is
  unreachable.

### v0.17.3 — Forget withdrawn template sources

- [x] A device forgets cached template sources the server no longer offers.

### v0.18.0 — Templates that keep up

- [x] Update a repository to the current templates with `hi init --update`:
  unedited owned files and managed blocks are replaced, hand edits are
  conflicts, and changes to the project's own files go to `docs/upgrades/`
  for an agent.
- [x] Check it in CI with `--check`, which skips private layers there unless
  `--strict`.
- [x] Bring existing repositories under a template with `hi init --adopt`.
- [x] Add the built-in `service`, `ml`, and `pipeline` templates. `ml`
  takes PyTorch from a `cpu`, `cuda`, or `rocm` dependency group; `rocm` is
  AMD's gfx1151 build for Strix Halo.
- [x] `hi init` says when a newer `hi` brings newer built-in templates.
- [x] Train with the `rocm` group on a Strix Halo machine (v0.18.2,
  aiw11, 2026-10-01): `hi init ml` installed AMD's PyTorch 2.11 for gfx1151
  through `make sync`, `make check` passed, and `make train` ran on the
  Radeon 8060S.
  Approved spec: [hi_init.md](specs/approved/hi_init.md).

The `mobile` and `cli` templates are built when the first real project of
each type starts.

### v0.18.1 — Follow renamed sources

- [x] `hi init --update` follows a private source renamed with
  `hi server templates rename`: when the recorded source is gone and one
  current source has the same layers, it uses that one and records the new
  name.

### v0.18.2 — ml picks the PyTorch build

- [x] The `ml` template's `Makefile` picks `rocm` on a Strix Halo, `cuda`
  with an NVIDIA GPU, and `cpu` otherwise, and runs everything with that
  build; before, any `uv run` swapped the ROCm build back to the CPU one.
  `hi init ml` sets it up with `make sync`.

### v0.19.0 — `hi q`: a command from plain words

- [x] `hi q <prompt>` sends the prompt with a small, redacted context (OS,
  shell, folder listing, git state, recent history, piped input) and shows
  one proposed command to run, copy, explain, or cancel.
- [x] hi classifies each command itself (read-only, changes files,
  dangerous) by parsing it; dangerous commands need `yes` typed, and globs
  in `mv`, `cp`, and `rm` show what they match first.
- [x] `--print` for scripts, `--explain '<command>'`, `--no-context`, and
  `hi q --context` to see exactly what is sent.
- [x] Providers: OpenAI-compatible endpoints, the Anthropic API, and a
  signed-in Claude Code, found from flags, saved config, or the
  environment; otherwise a first-run menu (`hi q --setup`). `hi q --status`
  names the one in use. Claude Code was tested live with Haiku (about 6
  seconds an answer); the OpenAI-compatible and Anthropic clients only
  against fake servers so far.
- [x] A local log of commands that ran.
  Approved spec: [hi_q.md](specs/approved/hi_q.md).

### v0.19.1 — Words are always the question

- [x] `hi q`'s own actions are options (`--setup`, `--status`,
  `--context`), so `hi q status of the log file` asks the model instead of
  showing the status. Everything from the first word that doesn't start with
  a dash is the question; `--` ends the options.

### v0.19.2 — OpenRouter for hi q

- [x] OpenRouter in `hi q --setup` and from `OPENROUTER_API_KEY`: only
  models that call tools are listed, all of them (the list was cut at 300),
  with fast, cheap ones first.
- [x] Models that reject tools are asked for JSON instead.

### v0.20.0 — `hi q`: shell integration, tools, and chat

- [x] `hi shell-init bash|zsh`, added to the rc file by `hi q --setup` after
  asking: the shell's current history and last exit status, `hi q …` and
  `q …` lines quoted on Enter so `( ) * ?` need no quotes, commands that ran
  in the shell's history, `cd` and `export` run in the shell itself, and in
  zsh `e` puts the command on the prompt. Tested in bash 5.3 and zsh 5.9.
- [x] The last 100 lines of the tmux pane as context.
- [x] Tools: list, read, help, which, and read-only runs, so the model looks
  before it proposes; a failed command goes back to the model for a fix,
  up to three times.
- [x] `hi q` with no question opens a chat; `hi q -c` continues the last
  conversation.
- [x] `e` edits a proposed command before it runs.

### v0.20.1 — Keep the saved key

- [x] `hi q --setup` for the same provider keeps the saved key when Enter
  is pressed at the key prompt, and lists the model in use first.

### v0.20.2 — Answers in terminal styles

- [x] `hi q` shows the Markdown in answers as terminal styles: bold,
  coloured inline code and code blocks without fences, headings, bullets,
  quotes, and links with their address dimmed. Piped output keeps the
  Markdown. No new dependency.

### v0.21.0 — A team model through `hi server`

- [x] `hi server ai set|off|remove` stores the team's OpenRouter key on the
  server, and connected devices and agents use it through a signed
  pass-through endpoint, with any model and the server's default first.
- [x] `hi q` uses the connected server without setup, lists it first in
  `hi q --setup`, and falls back to a personal provider when the server
  can't be used.
- [x] Usage per user, device, and model, without prompts, in `hi server ai`
  and `hi server spend`.
  Tested live against OpenRouter through a local server. The production
  server runs v0.21.0 and serves the team's OpenRouter key since 2026-10-02.
  Approved spec: [hi_server_ai.md](specs/approved/hi_server_ai.md).

### v0.22.0 — `hi q`: project notes, team spend, and a local model

- [x] AI spend from `hi server ai` in `hi server live` and the Slack App
  Home (release 2 of [hi_server_ai.md](specs/approved/hi_server_ai.md)).
- [x] Project notes in `.hifin/q.md`: how to test, where the logs are, what
  not to run. `hi q` uses them only after the user allows them once per
  file content. This repository has its own.
- [x] A local model as the team's default: `hi server ai set --url …
  --no-key` for upstreams without a key. Tested on 2026-10-02 with
  `halogen-qwen3.8-flash-next` already served on aiw11: correct tool calls
  and commands in 4–14 seconds, against about 2 seconds for Haiku through
  OpenRouter. The production server still uses OpenRouter.

### v0.22.1 — `hi update` restarts the server

- [x] `hi update` finds a `hi server run` still running the replaced binary,
  also when nothing new was downloaded, and asks to restart its systemd
  service (`sudo systemctl restart`, or `systemctl --user` for a user
  service). `--restart` and `--no-restart` for scripts.

### v0.22.2 — Podman in `hi install`

- [x] `hi install podman`: rootless Podman with `crun`, `uidmap`, and
  `passt`, and a subordinate ID range for users without one. Tested on aiw11
  on 2026-10-02: a rootless container with `--group-add keep-groups` sees
  the gfx1151 GPU through ROCm, which needs `crun` rather than Docker's
  `runc`. The first step towards `hi box`.

### v0.23.0 — `hi box`: local agent boxes

- [x] `hi box claude|codex [prompt]`, `shell`, and `run -- <command>`
  start a rootless container with the project, or a new git worktree of it,
  and nothing else from the home folder; with a prompt the agent runs on its
  own, without permission prompts.
- [x] No route out but hi's proxy, with `locked`, `dev`, and `open`
  presets, domains from `customizations.hi` in `devcontainer.json` after
  asking once, and `hi box allow`.
- [x] Claude Code's token is added at the proxy; the box only holds a
  placeholder. Codex's sign-in goes in for now.
- [x] `ls`, `attach`, `diff` (flagging files that run on the host), `stop`,
  and `rm`; `--gpu` for the Strix Halo; read-only git hooks and config.
- [x] The safe parts of `devcontainer.json`: `image`, `build.dockerfile`,
  `containerEnv`, and `postCreateCommand`.
  Tested on 2026-10-03: Claude Code and Codex with Docker, and Claude Code
  with rootless Podman on aiw9; `--gpu` with Podman on aiw11 (gfx1151).
  Approved spec: [hi_box.md](specs/approved/hi_box.md).

Still open from the container ideas, for later releases: one image on the
workstation and every provider (`hi compute run --image`), prebuilt project
images, pinned serving images
([hi_compute_serve_pinning.md](specs/ideas/hi_compute_serve_pinning.md)),
and image policy on the server. Podman arrived in `hi install` in v0.22.2.

### v0.23.1 — Remove every box at once

- [x] `hi box rm --all` lists every box and asks once; boxes with
  uncommitted work are kept unless `--force`, branches with commits are
  kept, and without a terminal it needs `--yes`.

### v0.24.0 — `hi data`: the team's Hugging Face datasets and models

- [x] `hi server data add <org>...` stores a read token per organization
  after checking it with the Hub; `list`, `test`, `remove`, and a menu.
- [x] A proxy under `/hf` passes only a repository's read calls on with the
  organization's token, so devices run the official `hf` with
  `HF_ENDPOINT` pointing to the server and a hi data token that lasts an
  hour. The Hugging Face token never reaches a device; file bytes come from
  the CDN and Xet storage directly.
- [x] `hi data` (a picker), `ls`, `info`, and `get`; `data` patterns per
  group in policy, everything when unset; token grants in the audit log,
  downloads in `data_usage.jsonl`.
  Tested on 2026-10-03 with `hf` 1.32.0 against the real Hub.
  Spec: [hi_data.md](specs/approved/hi_data.md).

### v0.24.1 — Search in `hi data`

- [x] `hi data`'s list has a search field: typing filters at once, every
  word must match (`gemma 27b`), and at most 15 rows show, with a count.
  Without a terminal it asks for words first and numbers only the matches.

### v0.24.2 — Buckets in `hi data`

- [x] `hi data` lists the organizations' buckets with their size and file
  count; `hi data get` syncs one with `hf buckets sync`, and `info` lists
  its largest files. The proxy passes a bucket's reads and refuses uploads,
  deletes, and settings. Tested with a public bucket through the proxy,
  and with a private 36 GB bucket from the team's server on 2026-10-03.

### v0.24.3 — Day-long data tokens, and `hi data` in the docs

- [x] A hi data token lasts a day instead of an hour, so long downloads
  don't stop when `hf` asks for its next Xet token; it still works only
  from its device, while the device and user are enrolled.
- [x] Laptops older than v0.24.2 don't see buckets, which they would try
  to download as models; counts say "1 bucket".
- [x] The guide (Download the team's data), README, website, llms.txt, and
  the agent skill cover `hi data`.

### v0.25.0 — `hi data` in code, projects, and boxes

- [x] `hi data run -- <command>` and `eval "$(hi data env)"`: `load_dataset`,
  `from_pretrained`, `hf_hub_download`, and `hf://` paths in pandas read
  the team's repositories directly. Tested with datasets 5.0.1 and pandas.
- [x] `hi data get` pins a branch or tag to its commit and records the
  download in the project's `.hifin/data.json`; `hi data get` with no name
  fetches everything recorded, and warns when a bucket changed since.
- [x] `hi box --data`: the box gets a placeholder token and hi's proxy adds
  a hi data token on the way to the server, and allows Hugging Face's
  download hosts. Tested with rootless Podman and a 548 MB Xet file.
- [x] The spec is approved: [hi_data.md](specs/approved/hi_data.md).

### v0.25.1 — The team's data on rented GPUs

- [x] `hi compute run --data <org>/<name>[/<pattern>]` downloads the team's
  data into `data/<name>` on the instance before the script starts. The
  instance can't reach the hi server, so the server hands out a signed
  link per file (about an hour, no token) and `hi` ships them in a wrapper
  that then runs the script. Tested with real links and a 548 MB file,
  and on a real Hugging Face job with 150 private files from the team.

### v0.25.2 — A clear message from an older server

- [x] `hi compute run --data` against a server older than v0.25.1 says to
  update the server, instead of "404 page not found".

### v0.26.0 — The team's data in cloud jobs, through `netbird expose`

- [x] The server opens a second listener with only the `hi data` proxy and
  publishes it with `netbird expose` while cloud runs need it, closing it
  when no run token is valid and nothing has called for 10 minutes.
- [x] Run tokens: for the repositories named with `--data`, until the
  run's time limit, accepted only through the public address.
- [x] `hi compute run --data` on Hugging Face Jobs uses it: the token goes
  as an encrypted job secret, there is no size limit or one-hour expiry,
  and the script can call `load_dataset` itself. Signed links remain for
  Colab and when the server can't expose. `hi server expose [stop]`.
  Tested on a real Hugging Face job through a server on vmhiserver.
  Spec: [hi_server_expose.md](specs/approved/hi_server_expose.md).

### v0.27.0 — `hi net expose`: a local service on a temporary public address

- [x] `hi net expose <port>` publishes a service on this machine with
  NetBird's reverse proxy, through a relay on the NetBird address so
  services on `localhost` work, with a request log.
- [x] Protected by a generated password by default; `--pin`, `--groups`
  (NetBird SSO), or `--public` instead. Ends after `--max` (default 1h, at
  most 24h) or on Ctrl+C.
- [x] `--detach` and `--json` for agents; `ls` and `stop`; each exposure in
  a connected hi server's audit log and live feed; the agent skill asks the
  user first and never goes `--public` without a yes.
  Tested on aiw9 with NetBird 0.74.4.
  Spec: [hi_net_expose.md](specs/approved/hi_net_expose.md). Later: TCP
  services and a `net_expose` group policy (release 2 of the spec).

### v0.27.1 — The agent skill knows `hi data` and `hi net expose`

- [x] The skill's description, which decides when agents load it, now names
  the team's data (`hi data`, `--data`) and `hi net expose`, and its
  introduction points to `hi data help` and `hi net expose help`. Refresh
  installed skills with `hi skill --global`, and `hi skill` in projects.

### v0.27.2 — Quieter `hi install`

- [x] Quiet pipx and the PATH hint when `~/.local/bin` is already on PATH.
- [x] `hi install node` skips npm's recommended packages.

### v0.28.0 — `hi agent`: agents get their own command

- [x] `hi agent [claude|codex] "<task>"` runs the agent in a box on a new
  worktree and waits for one report, the same for every agent: status,
  exit status, final message, its own summary block, session ID, tokens
  where the agent gives them, and the changed files from git. Without a
  name, the first agent installed and signed in.
- [x] `--json`, `--detach`, and `hi agent wait <name>`; exit 1 when the
  agent failed. `hi agent claude|codex` without a task is an interactive
  session in a box.
- [x] `hi box` is now for any code: `hi box claude|codex` and `hi box token`
  moved to `hi agent`, and print a pointer. The `locked` preset allows
  nothing; an agent's own hosts come with the agent.
- [x] The guide's new page, the agent skill, the website, and `llms.txt`.
  Tested on 2026-10-07 on aiw9 with rootless Podman, Claude Code, and Codex.
  Spec: [hi_agent.md](specs/approved/hi_agent.md), release 1.

### v0.29.0 — The team's data on every cloud machine

- [x] `hi compute up --data <org>/<name>` for RunPod and Shadeform: the
  device gets a run token for the machine's lifetime before it is created,
  and once it is up writes `HF_ENDPOINT` and the token into
  `~/.hi/data.env` over SSH, which the machine's shells load; so
  `hf download` and `load_dataset` work there, and the token never shows
  in the provider's console. The same through a managed provider, after
  approval; `--no-wait` and Community Cloud refuse `--data`.
  Spec: [hi_server_expose.md, Cloud machines](specs/approved/hi_server_expose.md#cloud-machines) and
  [Releases](specs/approved/hi_server_expose.md#releases) (2).
  Tested on 2026-10-07 on RunPod (RTX 2000 Ada) through vmhiserver:
  `hf download` and `load_dataset` of acme/bars-1d, and a refusal for a
  repository not named.

### v0.29.1 — Tasks from markdown files

- [x] Task files: `hi agent brief.md`, `-` for stdin, and `--task-file`;
  the task goes to the agent on stdin, and the report has `task_file`.
  Tested on 2026-10-07 in rootless Podman: Claude Code from an uncommitted
  brief; Codex got its task on stdin, but its sign-in had been spent (below).
  Spec: [hi_agent_bundles.md, Task files](specs/ideas/hi_agent_bundles.md#task-files-release-1).
- [x] A Codex sign-in refreshed in a box is copied back to
  `~/.codex/auth.json` when the run ends or the box is removed; before, the
  box spent the one-time refresh token and Codex on the machine stopped
  working.

### v0.29.2 — `hi agent` in folders without git

- [x] Folders without git: the agent works in place, and the report's
  changed files come from the folder's files before and after; not in the
  home folder or above it. Tested on 2026-10-07 with Claude Code and Codex.
  Spec: [hi_agent_bundles.md, Folders](specs/ideas/hi_agent_bundles.md#folders-that-arent-git-repositories-release-1).

### v0.30.0 — `hi skill` with skills.sh

- [x] `hi skills` in a terminal: search skills.sh as you type, browse
  suggestions, see a skill's description, license, and audits, and pick
  several to install; `u` updates and `x` removes. Without a terminal,
  `hi skill` still writes the hi skill.
- [x] `hi skill find`, `add`, `ls`, `show`, `update` (`--check`), and `rm`:
  skills from GitHub, any git repository, or a folder, at a recorded commit,
  in `.agents/skills` linked from `.claude/skills`, and in the
  `skills-lock.json` that `npx skills` uses, with the commit added.
- [x] skills.sh's audits on find, add, show, and update; a `high` or
  `critical` rating needs a yes or `--accept-risk`; no install telemetry,
  and `DO_NOT_TRACK` turns off searches and audits.
  Tested on 2026-10-07 against skills.sh and GitHub, and the selector in a
  real terminal.
  Spec: [hi_skills_sh.md](specs/approved/hi_skills_sh.md), release 1.

### v0.30.1 — Bundles from skills.sh

- [x] `bundles/web.json`, `office.json`, and `data.json` list skills from
  skills.sh by source and commit, with the packages each needs:
  `agent-browser`; Anthropic's `docx`, `xlsx`, `pptx`, and `pdf`; DuckDB's
  `query`, `read-file`, and `convert-file`, and `data-visualization`.
- [x] `hi skill update --bundles` (`--check`, a bundle or skill name)
  shows each change, new mentions, and audits, asks, and rewrites the
  bundle files; bundle files with unknown keys or unpinned packages are
  refused.
- [x] The ten hand-written skills are removed, and what `hi-data` added is
  in the `hi` skill's "Team data".
- [x] `hi skills` highlights like the other menus.
  Pinned on 2026-10-08 against the real repositories and skills.sh.
  Spec: [hi_skills_sh.md](specs/approved/hi_skills_sh.md), release 2.

### v0.30.2 — Finish the team's data on every cloud machine

- [x] `hi server expose revoke <run>` ends one run's token now; the server
  keeps the run until its tokens would have expired and refuses it new
  ones. `hi server expose` lists the runs with run tokens.
  Spec: [hi_server_expose.md, Run tokens](specs/approved/hi_server_expose.md#run-tokens).
- [x] "1 file", not "1 files", in `hi compute run --data`'s output, on
  the instance, and in `hi data info`.
- [x] The `hi server data` menu, tried in a real terminal against a running
  server: add, test, and remove an organization. It now shows whose token
  each organization uses in `list`, names a `hf auth login` sign-in and
  warns about it, and doesn't print each result twice.
- [x] [hi_server_expose.md](specs/approved/hi_server_expose.md) approved.
  Tested on 2026-10-08.

### v0.31.0 — Bundles in `hi agent`

- [x] `hi agent --bundle web,office` (and `hi box --bundle`): the bundles'
  skills, pinned to their commits, go in the box's home with the `hi`
  skill, and the box runs on `hi-agent:<hash>`, built once from the
  skills' needs on the box's base image and reused.
- [x] The network the skills need is shown with each reason and asked
  about; without a terminal it needs `--network` or `--allow`. `--data`
  with an open network is refused.
- [x] Bundles from hi, the team's template sources (which may name their
  own `skills/`), and `~/.local/share/hi/bundles`; `hi bundle ls`, `show`,
  and `prune`.
- [x] Front matter in briefs: `agent`, `bundles`, `network`, `allow`,
  `data`, `gpu`; what widens the box is asked about, or left out without a
  terminal.
- [x] The report's `bundles`, `skills`, and `image`.
  Tested on 2026-10-08 with rootless Podman: the `web` image (2.8 GB)
  built once, then started from the cache in a second; Claude Code browsed
  with `agent-browser` in an empty folder from a brief with front matter;
  Codex found the `data` bundle's skills in `~/.agents/skills` and used
  DuckDB; every tool in the `office` image checked.
  Spec: [hi_agent_bundles.md](specs/ideas/hi_agent_bundles.md), release 2.

### v0.31.1 — Bundles in devcontainer.json

- [x] `customizations.hi.bundles` in a project's `devcontainer.json` gives
  every box and agent there those bundles; `--bundle` adds more.
- [x] `hi skill update` asks "Use it anyway?" about a risky skill, not
  "Add it anyway?".

### v0.31.2 — Screenshots with every image

- [x] The `web` bundle's Chrome doesn't say it's headless: it gives a
  normal Chrome's user agent, not `HeadlessChrome`, and
  `navigator.webdriver` is false. Some CDNs refuse headless Chrome;
  a site's images behind such a CDN were broken in screenshots and now
  load.
- [x] The `hi` skill tells agents to scroll before a full-page screenshot,
  so lazy images load, and to open the PNG to check it.

### v0.31.3 — timg in the terminal tools

- [x] `hi install terminal` also installs
  [timg](https://github.com/hzeller/timg) from Ubuntu, which shows images
  and videos in the terminal; `hi uninstall terminal` removes it too.

### v0.31.4 — See that an agent is working

- [x] While `hi agent` and `hi agent wait` wait, one line shows a red
  scanner, the time so far, tokens in and out, the steps taken, and the
  latest step, read from the agent's session log in the box's home
  folder, for Claude Code and Codex alike. Ctrl+C clears it and says how
  to wait again.

### v0.31.5 — Stats for each agent run

- [x] The report ends with how long the run took, its steps, tokens in
  (and how many were cached) and out, the model, and for Claude Code its
  cost at API prices; `--json` gives `seconds`, `steps`, `model`,
  `tokens.cached`, and `cost_usd`. Codex's tokens come from its session
  log, so its runs have them too.

### v0.31.6 — The agent's model

- [x] `hi agent --model <model>` and `model:` in a brief's front matter.
  Without them, the box uses the model in your own Claude Code or Codex
  settings, else `opus` for Claude Code: with only a placeholder token in
  the box, Claude Code can't see the plan and chose Sonnet. The box's first
  line names the model.

### v0.32.0 — Hermes Agent

- [x] `hi install hermes` runs Nous Research's installer without its setup
  wizard; `hi uninstall hermes` removes the program and keeps `~/.hermes`.
- [x] `hi agent hermes`: Hermes in a box, from your install mounted
  read-only, on OpenRouter with your model. The key stays outside the box:
  Hermes gets a placeholder and the proxy as a custom endpoint, and the
  proxy reads only `OPENROUTER_API_KEY` from `~/.hermes/.env`. The live
  line, the report, and its stats come from Hermes' `state.db`.
  Tested on 2026-10-08 with Hermes Agent v0.21.2 and
  `google/gemini-3.8-flash`: tasks of one and four steps finished, and the
  key was in no file or variable of the box.

### v0.33.0 — `hi agent best-of`

- [x] `hi agent best-of <n> "<task>"` runs the same task in 2 to 8 boxes
  at once, with `--agents claude,codex` taken in turn. hi runs the check
  (`make check`, or `--check`) in each box after its agent ends, kills
  what the agent left running first, and keeps `--max` per agent. A table
  ranks the boxes by the check, flagged files, and diff size; `keep`
  keeps one and removes the others with their branches; `ls`, `show`,
  `rm`. No judge yet. Spec:
  [hi_agent_best_of.md](specs/approved/hi_agent_best_of.md) (release 1).
  Tested on 2026-10-08 with two Claude Code boxes.
- [x] Rounds, in the style of autoresearch: `--score "<cmd>" --lower|--higher`
  measures your last commit, then runs `--rounds n|forever` rounds, each
  from the best so far. hi scores each box itself and alone commits a result
  that beats the best, one commit per gain on `best-of/<run>/best`; agents
  see earlier attempts in the next round's task. `--edit` disqualifies
  results that touch other files; `--for`, `--budget`, `--patience`,
  `--min-gain`. The rounds run in their own process: `watch` (q leaves,
  the run goes on), `stop [--now]`, `resume`. With `--gpu`, boxes are scored
  one at a time. Spec: [hi_agent_best_of.md, Rounds](specs/approved/hi_agent_best_of.md#rounds) (release 2).

### v0.34.0 — Autoresearch templates

- [x] `hi init autoresearch-ml`, and a quant template now in the private
  repo: projects shaped for `hi agent best-of` rounds, with a fixed and tested evaluation,
  one file agents change, frozen data, a holdout outside the repository,
  and a box image built from `uv.lock` so boxes run offline. Tested from
  `hi init` to a score in a locked box, and through a real round. Spec: [hi_init.md](specs/approved/hi_init.md#built-in-template-types),
  [hi_agent_best_of.md](specs/approved/hi_agent_best_of.md#templates-and-the-guide-2026-10-08).
- [x] Project images rebuild when a file the Dockerfile copies, or a
  `build.args` value, changes.
- [x] The guide's Autoresearch section, with simple and advanced examples.

### v0.35.0 — Results of a run

- [x] `hi agent best-of --then "<command>"` runs a command on this machine,
  in the project, when the rounds end for any reason, with `HI_BEST_OF_RUN`
  set. hi keeps every attempt's files under `refs/best-of/<run>/`, and
  `ls --json` lists the runs.
- [x] The quant template's results: every attempt re-run into Parquet and
  an offline report (private repo since v0.36.3).

### v0.36.0 — A research suite for the quant template

- [x] Robustness checks, scores that must hold, regimes, capacity, a fuller
  backtester, and a report that records decisions, for the quant
  template. Tracked in the private repo with the template since v0.36.3.

### v0.36.1 — Report layout, excess returns, and Makefile notes

- [x] The quant template's report layout (private repo).
- [x] `hi init --update` notes template changes outside a managed file's hi
  block, such as a new Makefile target, in `docs/upgrades`.

### v0.36.2 — Live tokens, how agents are paid for, a model per agent

- [x] `hi agent best-of watch` and `show` read the running boxes' session
  logs: tokens in and out, steps and the latest step for each box, and the
  round's tokens so far.
- [x] The dollars are labelled as what they are: Claude Code's own cost at
  API prices, which on a subscription is usage, not a bill. hi reads each
  agent's sign-in (a Claude plan and tier, a ChatGPT plan or an OpenAI API
  key, OpenRouter for Hermes), shows it per box, and splits billed dollars
  from subscription usage.
- [x] `--agents` takes a model per agent, such as
  `hermes:qwen/qwen3.8-max-0902`, and refuses more agents than boxes.
- [x] The quant template's results print their progress (private repo).

### v0.36.3 — The quant template moves to the private repo

- [x] `autoresearch-quant` is no longer built into hi; it is in
  `acme/templates` and reaches connected devices through `hi server`,
  with its tutorial, run b-3 and its specification. The public guide and
  docs no longer describe it, and its tutorial's address redirects.
- [x] `hi init --update` follows a layer that left the built-ins to the
  one server source that has it, so projects made from the built-in
  template keep updating.

### v0.37.0 — `hi team`: a virtual team in a Slack channel

- [x] `hi team new`: a step-by-step setup that prints the team's Slack app
  manifest, checks both tokens and the channel, and makes the team's
  folder, a git repository (new or cloned), and the lead's `SOUL.md`;
  `--from <file.json>` without a terminal.
- [x] `hi team up` and `down`: a systemd user service runs the lead, Hermes'
  Slack gateway, in a box that reaches any site through the proxy, in one channel,
  without direct messages; `ls`, `status`, `logs`, `manifest`.
- [x] The broker: the lead's `hi team task`, `review`, `wait`, `merge`, and
  `stop` start members as `hi agent` on the host, and a merge needs a
  review that approved the task's latest commit.
- [x] `hi agent --from <commit>` starts a worktree from a branch.
- [x] Hermes in a box no longer installs its dependencies again for each
  home folder (`HERMES_DISABLE_LAZY_INSTALLS`).
  Tested on 2026-10-10 on aiw9: the lead's box with Hermes
  v0.21.6, a task by Claude Code, a review by Codex, and the merge.
- [x] A team's folder is `./team-<name>` where it was made (or `--dir`),
  found by name through a link; `hi team up <folder>` takes a moved or
  copied one. The bot is `team-<name>`.
- [x] Members reach any site by default (`--network open`); `network` in
  team.json narrows one.
  Approved spec: [hi_team.md](specs/approved/hi_team.md).

### v0.37.1 — `hi team new` checks each answer as it's given

- [x] Tokens, the channel, the owners, and the members are checked as they
  are given, and asked again in a terminal, so one wrong answer no longer
  ends the setup.
- [x] The marker a terminal sends after a paste no longer lands in the next
  answer (a pasted token with a line break left `\x1b[201~` in front of
  the channel ID).
- [x] A repository given as a folder on this machine is cloned, and the
  team doesn't push merges back into it; its owner pulls from the team.

### v0.37.2 — A team's log once, and followed

- [x] `hi team logs` shows each line once and only what the team prints,
  without Podman's events or its log driver's copy; `-f` follows it.
  Verified live on 2026-10-10 on aiw9: the lead answered in its Slack
  channel.

### v0.37.3 — Team leads in `hi box ls`

- [x] `hi box ls` lists the team leads' running boxes after your own, with
  their team, channel, and folder, and points to `hi team` for them.

### v0.38.0 — Previews of a team's work

- [x] The lead's `hi team preview [<task>]` runs the project in an app box
  on the host (`make run` or `npm start`, on `$PORT`, published to
  localhost only) and puts it on a temporary password-protected address
  with `hi net expose`; the lead posts it. `hi team preview stop` ends it.
  Verified on 2026-10-10 on aiw9 with team test's world clock.
- [x] New team repositories ask for a `make run` on `$PORT`, and the lead's
  instructions say to share a preview instead of telling people to run
  commands.
- [x] The lead's own edits to its hi-team skill survive restarts; a new hi
  replaces them only when its own text changed, and keeps them as
  `SKILL.md.lead`.

### v0.38.1 — No stray terminal replies; previews outlive restarts

- [x] hi no longer asks the terminal for its background colour at start,
  in every command. Terminals that answered late left the reply, such as
  `^[[34;1R`, in the shell. Bubble Tea v1 asks in its `init`, so hi builds
  with a copy without it (`third_party/bubbletea`, until v2) and assumes a
  dark background unless `COLORFGBG` says otherwise.
- [x] A team's previews and member boxes run in systemd scopes of their
  own, so restarting the team (`hi team up`) no longer stops them, and the
  service signals only `hi team __serve`, which stops the lead cleanly.

### v0.39.0 — A team's project on GitHub, with its specs

- [x] `hi team new` can put a new project on GitHub: give `owner/name`, and
  hi creates a private repository with the machine's `gh` sign-in.
  `hi team github <team> <owner/repo>` does the same for an existing team,
  or connects an empty repository. The broker pushes every merge from the
  host; no GitHub credential enters a box.
- [x] The lead's specs live in the project, `docs/specs`, writable at its
  /team/specs; the broker commits them when a task starts and before a
  merge, so members' branches start with them and they go to GitHub with
  the code. A team's earlier `specs/` moves in on its next start, its
  briefs to the lead's home folder.

### v0.40.0 — A team lead that answers when asked, and hears everything

- [x] The lead answers only when someone @-mentions it, even in its own
  threads, so people can talk among themselves in the channel.
- [x] hi keeps the channel's log in the team's `channel/` folder: every
  message, thread replies up to two weeks old, and shared files up to
  50 MB, read with the bot's token from the host every two minutes.
  `hi team channel <name> [--since 2h]` shows it; in its box the lead runs
  `hi team channel`.
- [x] The lead's `channel-watch` job reads what's new every 15 minutes and
  keeps what matters in its memory, without posting; with nothing new it
  makes no model call.
- [x] A merge keeps the lead's `docs/specs` as the lead left them: a
  member's changes to them are left out, and the lead is told. Briefs say
  the specs are the lead's. This ends Hermes' "File-mutation verifier"
  warnings in the channel after a merge changed a spec under the lead.
  Verified on 2026-10-11 on aiw9 with team test: 43 messages and an image
  logged, and the job saved a note to the lead's memory.

### v0.41.0 — A team lead with long-term memory, and no restart notices

- [x] The lead's long-term memory is Hindsight: a server in a container of
  its own on the team's network, its database in the team's `memory/`
  folder, its model calls through the team's proxy, and Hermes' hindsight
  plugin in the lead with a memory bank per team. The plugin is the one
  Hermes' catalog pins; its client library is installed once per machine
  and mounted read-only. `"memory": "off"` in `team.json` leaves it out.
  Verified on 2026-10-11 on aiw9 with team test: a turn of the lead was
  retained as facts and recalled by meaning.
- [x] The lead no longer posts "Hermes is shutting down" or "back online"
  in the channel when the team restarts.

### v0.42.0 — Preview links that open without a password

- [x] `hi net expose --link` puts the key in the link: hi's relay lets in a
  request with the key (from the link, its cookie, or a Bearer header) and
  answers anything else with a page asking for the link. The link's key
  becomes a cookie until the address ends, and the browser moves on to
  the address without it.
- [x] A team's previews use it: the lead posts one link, and the people
  open it, with no password to copy.
  Verified on 2026-10-11 on aiw9 through NetBird: 401 without the key, 303
  and the cookie with the link, then the app.

### v0.43.0 — A team that waits for your yes

- [x] New work starts only after one of the people says yes: the broker
  refuses a new task unless the lead names the approving message, a reply
  from an owner or asker after the lead's question in the same thread,
  checked against the channel's log. Continuing a task needs none; a
  small fix may start with `--small`, and the broker tells the channel.
  `hi team channel --ids` shows each message's id.
- [x] Hindsight's "recalled N memories" and "saving to memory" lines no
  longer appear in the channel.

### v0.44.0 — Pick the team to start from a list

- [x] `hi team up` without a name shows the teams on this machine, whether
  each is up, and its channel; the one you choose starts, or restarts.

### v0.45.0 — Dev and production for a team

- [x] Merges go to dev (`main`); production is a `production` branch that
  moves only on an owner's yes: `hi team release --approval <id> --notes
  <file>` makes dev's latest the next version (v1, v2, ..., tagged
  `release-<n>`), and `hi team release v2` puts production back on an
  earlier one. Each move is posted in the channel itself, with the notes.
- [x] `hi team discard <task>` drops an idea that isn't merged, with its
  follow-ups, reviews, boxes, and branches; `hi team discard dev` sets dev's
  code back to production's, as a commit that keeps the specs, and is
  posted in the channel.
- [x] At most five ideas wait unmerged; a sixth task is refused until one
  is merged or discarded.
- [x] `hi team versions`, `hi team status`, merges, and previews name the
  version: "production v3", "dev, v3 + 2 changes".

### v0.46.0 — Members get what the people gave

- [x] A team member's box gets the task's files at `/box/files`
  (read-only, `hi agent --files <folder>`): the files shared in the thread
  the work was approved in, and what the lead attaches with `hi team task
  --attach <file>`. The brief quotes that thread with its links and says
  what each file is. Follow-ups and reviews get the same files.
- [x] A lead's scheduled messages are posted as they are, without Hermes'
  "Cronjob Response" heading and job footer.

### v0.47.0 — Remove a team

- [x] `hi team rm [<name>]` stops a team and removes it from the machine:
  its systemd service, its name in the teams folder, its containers and
  members' boxes, and its folder. It asks first (`--yes` without a
  terminal); `--keep-folder` keeps the folder for `hi team up <folder>`.

### v0.48.0 — The lead is @<name>

- [x] A new team's Slack app is named after the team, so people write
  `@payments` instead of `@team-payments`.

### v0.49.0 — Reviews in the broker, and a lead's notes that last

- [x] The broker runs a task's review rounds: when a review asks for
  changes, the coder gets the notes on the same branch and is reviewed
  again, up to three reviews. The lead hears once: approved, or changes
  still needed.
- [x] A review reads the lead's current specs, and a follow-up merges the
  team's branch first when its specs moved.
- [x] The lead keeps what it learns in a `hi-team-notes` skill that hi never
  rewrites; paragraphs a lead added to a replaced `hi-team` skill move there.
- [x] `hi team up` brings an existing lead's SOUL.md up to date, keeping the
  lead's and the owners' edits.

### v0.50.0 — Links that keep working

- [x] A team has two addresses, each a `hi net expose --follow` on a port
  file: a preview keeps its link across merges, and production follows the
  production branch, renewed before its day ends.
- [x] One channel message keeps production's current link, and release
  announcements link to it.

### v0.51.0 — Check the Slack app's scopes

- [x] `hi team new` refuses a Slack token that lacks the scopes the lead
  needs, such as `files:read`, and `hi team up` warns, naming the scopes to
  add.

### v0.51.1 — Templates in `project_templates/`

- [x] A template source keeps its layers in `project_templates/<layer>/`,
  beside `skills/` and `docs/`; layers at the top still load. The private
  repo's `research` and `autoresearch-quant` moved there.
  Approved spec: [hi_init.md](specs/approved/hi_init.md#layout-of-a-source).

### v0.52.0 — Keep only gains that hold: `best-of --validate`

- [x] `hi agent best-of --validate "<command>" --validate-data <dir>
  --validate-slack <n>`: after each round, hi scores every good box's
  result on data agents never see, in a container of its own (the gains'
  tip plus only the box's `--edit` files, no network, no `.git`, the data
  read-only at `/box/data`), and keeps the winner only if its validation
  is no worse than the best's by more than the slack; otherwise the round
  is `rejected`. The baseline is validated first. Numbers stay out of
  tasks, commits, and attempt refs; `watch` and `show` have them.
- [x] `autoresearch-ml` splits its text four ways and gates every gain on
  `../<folder>-validation` with `make validate`.
- [x] The guide: [Keep only gains that hold](https://hi5.sh/guide/autoresearch/validation/).
  Approved spec: [hi_agent_best_of_validate.md](specs/approved/hi_agent_best_of_validate.md)
  (release 1; folds are release 2).

### v0.52.1 — The validation gate can't slide

- [x] `--validate` compares each winner with the highest validation number
  so far (the baseline's or any kept version's), not the latest kept one's,
  so validation never ends more than `--validate-slack` below its best
  point. In v0.52.0 every kept round could lose up to the slack, and the
  losses added up; run b-5 showed it. `run.json` keeps the mark as
  `peak_validation`; a run started on v0.52.0 and resumed starts the mark
  at its latest kept number.

### v0.52.2 — An app knows its own link; scheduled posts are the lead's

- [x] An app box gets its slot's current link in a read-only file,
  `$HI_PUBLIC_URL_FILE` (`/hi/address`), which hi rewrites when the link
  changes each day. A link written into the project went stale the next
  day.
- [x] The `hi-team` skill says the app box holds no credentials, keys and
  tokens never go in a brief or the code, and a scheduled channel post is
  a lead's `no_agent` cron script that builds the message from
  production's code. Team aiman's app scheduled its own 06:00 Slack post,
  which could never be sent from a box without a token.

### v0.53.0 — A team's keys and settings

- [x] `hi team new` asks for an OpenRouter key of the team's own (Enter
  keeps this machine's); `hi team key <name> openrouter [--unset]` sets it
  later. The lead's proxy takes it first, and no box holds it.
- [x] An app box (preview, production) calls models at
  `$OPENROUTER_BASE_URL` with a placeholder key: hi relays the calls to
  the broker's models socket, which puts the team's key on them.
- [x] `hi team env <name> KEY=VALUE`: settings that app boxes get as
  environment variables, and nobody else; the lead lists their names.
  Production starts again on the same link when they change.
- [x] The `hi-team` skill: the app's models and settings, and a key offered
  in the channel is sent to `hi team env` instead of a brief. Team aiman's
  OpenRouter key went from Slack into four briefs and the code.
  Approved spec: [hi_team_keys.md](specs/approved/hi_team_keys.md).

### v0.53.1 — The start-up summary shows `--min-gain`

- [x] `hi agent best-of`'s summary before it starts says that a result is
  kept only if it beats the best by more than `--min-gain`, when one is set.

### v0.54.0 — Folds: a validation window that stays fresh

- [x] `hi agent best-of --folds <n> --fold "<command>" [--fold-every <k>]`:
  the validation window walks forward through n folds. When a fold ends
  (after k rounds, or when `--patience` runs out), hi runs `--fold` in a
  worktree of the gains' tip with `HI_FOLD` set, commits what it changed
  as `best-of <run> fold <n>`, and scores and validates the tip again as
  the new baseline. A fold that touches `--edit` files stops the run.
  Agents see a `fold` row with the new score, never its validation.
- [x] `autoresearch-ml`: `make data FOLDS=3`, `make fold`, and
  `make loop FOLDS=3 FOLD_EVERY=10`; the last fold is the usual split.
- [x] The guide: [Folds](https://hi5.sh/guide/autoresearch/validation/#folds-a-window-that-stays-fresh).
  Approved spec: [hi_agent_best_of_validate.md](specs/approved/hi_agent_best_of_validate.md#expanding-window-release-2)
  (release 2).

### v0.54.1 — "fold 2's baseline" in the view

- [x] `hi agent best-of watch` says the best is "fold 2's baseline", not
  "the fold 2's baseline".

### v0.55.0 — Snapshots and restore for a team

- [x] `hi team snapshot`, `snapshots`, and `restore`: restic snapshots of
  a team's folder (the lead's memory, the tasks, the channel's log,
  `versions.json`, production's data, and the code), without
  `secrets.json` or `run/`. The team keeps running: the broker holds its
  locks and pauses the lead, Hindsight, and production for about a second.
  Taken daily by a systemd timer (7 daily, 4 weekly, 6 monthly kept, and
  every pre-release one), before every release, and by hand. A restore
  asks, stops the team, snapshots how things are first, and keeps the
  settings and, without `--code`, the code.
- [x] `hi team backups`: copy every snapshot to an S3-compatible bucket
  (`--offsite`, `--region`), change how many are kept (`--keep`), and print
  the key (`--show-key`). Failures reach the owners in the channel and
  `hi team status`.
- [x] An app box keeps its data in `$HI_DATA_DIR` (`/data`): production's
  is the team's `app/`, which outlives releases; a preview's is its own.
  Team aiman's app lost what it wrote at every release.
- [x] `hi install restic`.
  Approved spec: [hi_team_snapshots.md](specs/approved/hi_team_snapshots.md).

### v0.56.0 — Roll production back with its data

- [x] `hi team rollback v2 --approval <id>` (the lead, with an owner's
  yes) and `hi team rollback <name> v2` (on the machine): production back
  on v2's code with its data as v2 left it, from the snapshot taken when
  production moved off v2. What was entered since is set aside in a
  snapshot, and rolling forward puts it back; `--code-only` keeps the data.
  The lead's memory, the tasks, and the channel's log stay.
- [x] Each move of production records the versions it went between and the
  snapshot taken just before.
- [x] The broker has a second socket for the machine, which no box mounts;
  `hi team snapshot` and `hi team rollback` use it.
  Approved spec: [hi_team_snapshots.md, Rollback](specs/approved/hi_team_snapshots.md#rollback-release-2).

### v0.57.0 — Talk to the lead from the app

- [x] Every page of a team's preview and production has the helper: a
  button that opens a chat with the lead beside the page. hi net expose
  adds its script to each page load (`--inject`, `--inject-if`) and serves
  `/__hi/` from the team's service (`--route`), so it works with any
  framework and the app holds none of it.
- [x] The lead answers through Hermes' API server in its box, a
  conversation per chat, streamed; `hi team __leadapi` relays it to the
  team's service. hi copies each chat into a thread in the channel, and
  the lead's channel-watch leaves those copies out.
- [x] A yes in the chat approves work (`--approval w-3.4`), and the brief
  gets the chat; releases, rollbacks, and production's helper still need
  an owner's yes in Slack.
- [x] `hi team helper`: on in preview and production by default; the lead
  turns the preview's off when asked, and production's with an owner's
  yes; `hi team helper <name> production off` on the machine.
  Approved spec: [hi_team_helper.md](specs/approved/hi_team_helper.md) (slice 1).

### v0.58.0 — Point at what you mean

- [x] Select mode in the helper: the target button (or `Alt+S`) outlines
  the element under the pointer, `Alt+↑` and `Alt+↓` widen and narrow it, a
  click adds it to the message as a chip, Shift-click adds more; the app
  sees none of those clicks. On touch screens, tap and "Use this".
- [x] Each element reaches the lead as a reference: a selector (finder,
  vendored, without Tailwind's utility classes), its text, attributes,
  HTML, ancestors, nearest heading and label, box and colors, and the
  source file when a Vue, Svelte, or React development build gives it
  away. The Slack thread lists them, and a task's brief gets them with the
  chat. Open shadow roots work; iframes don't.
- [x] The helper's script has a fixed URL with an ETag, so a new hi's
  helper reaches pages on their next load, without new links.
  Approved spec: [hi_team_helper.md](specs/approved/hi_team_helper.md#select-mode) (slice 2).

### v0.58.1 — Alt+S from anywhere on the page

- [x] `Alt+S` starts and stops select mode wherever the focus is, the
  panel open or not; it worked only while typing in the panel.

### v0.59.0 — Show it, and hear back

- [x] Files in the helper's chat: paste a screenshot, drop files on the
  panel, or use the paperclip, up to 6 a message. The lead's model sees
  the pictures; every file is in `/team/web` in its box, in the chat's
  Slack thread, and in the files of work approved in the chat.
- [x] A picture of each element pointed at in select mode (modern-screenshot,
  vendored, loaded the first time), a small one with what is around it.
- [x] `hi team web post <chat>`: the lead tells a chat something after its
  answer, such as that the work is ready to try. The panel shows it at
  once, or a dot and a preview on the button; `hi team web` and
  `hi team web show` list and read the chats.
  Approved spec: [hi_team_helper.md](specs/approved/hi_team_helper.md#files-and-pictures) (slice 3).

### v0.59.1 — The lead is working

- [x] The helper shows a spinner and how long the lead has been at it
  from the moment a message is sent; since v0.59.0 the "is answering" line
  disappeared until the first words came, so the chat looked stuck.

### v0.60.0 — Signed in with /hi

- [x] Anyone with the app's link gets help and feedback, not the lead.
  Ask: hi answers how the app works from its guide, HELP.md, through the
  lead's model with no tools, within daily limits. Feedback: posted in the
  channel as "Feedback f-12", with a picture of the page, what was
  pointed at, and files; the lead does it only when asked, with a yes.
- [x] `/hi` in the channel DMs the person their private links (a Hermes
  plugin in the lead's home), and with them the helper is their chat with
  the lead, checked through Slack. The relay keeps the key in a cookie
  only `/__hi/` gets; keys last 14 days, and the DM is updated when an
  address changes. The manifest gains `/hi`, `commands`, and `im:write`.
- [x] `hi team web questions` and `hi team web link`; feedback in
  `hi team web` and `hi team web show f-12`.
  Approved spec: [hi_team_helper_signin.md](specs/approved/hi_team_helper_signin.md).

### v0.61.0 — Bun, and a box that can build the web template

- [x] The base image has Node.js 24 LTS from nodejs.org, checked against
  pinned sums, in place of Ubuntu 24.04's Node.js 18, which Vite and
  Vitest refuse; the `web` template's `make check` couldn't pass in a box.
- [x] Bun in the base image and as `hi install bun`, the latest release,
  checked against its sums. `hi init` says which `hi install` a missing
  setup tool needs.
- [x] The `web` template installs with Bun (`bun.lock`, a 3-day
  `minimumReleaseAge`) and type-checks with `bun check`; Vite, Vitest, and
  Biome run as before. `make check` asks an older project to move from
  `package-lock.json`.
- [x] A team's preview starts a project with a `bun.lock` with `bun install
  --frozen-lockfile && bun run start`.
  Approved spec: [hi_bun.md](specs/approved/hi_bun.md) (slice 1).

### v0.61.1 — A command per team

- [x] Each team's command is `/hi-<team>` (`/hi-timmai`), in the manifest
  and the plugin: Slack gives a command two apps in a workspace share to
  the one installed last, so `/hi` reached whichever team was reinstalled
  last. Plain `/hi` still works for the team that gets it.
- [x] The helper's chat sends the last words of the lead's answer that
  were still on their way when it finished; they could be dropped.

### v0.61.2 — New Slack tokens

- [x] `hi team key <name> slack`: the Slack app's bot and app tokens
  again, after Slack gave new ones. Each is asked for and checked with
  Slack (the bot token also for its scopes and the channel); Enter keeps
  one, and a running team restarts with them.

### v0.61.3 — /hi-<team> in the bot's DM

- [x] The team's command works in the bot's DM as well as the channel:
  Hermes dropped it there, since the lead ignores DMs, before the plugin
  saw it. The plugin now answers its commands in the Slack adapter's
  listener, privately, with the member ID Slack sets.

### v0.62.0 — The lead in a DM

- [x] People who may ask for work talk to the lead in the bot's DM, one
  conversation per DM; anyone else's DM is dropped (the plugin asks
  `hi team web may`). A yes there approves work with `--approval dm`,
  which hi checks in the DM itself; releases still need an owner in the
  channel.
- [x] Work started from a DM is announced in the channel with who asked,
  and each release says who asked for what it holds.
- [x] The manifest gains `im:history` and `message.im`, and the Messages
  tab is writable.
  Approved spec: [hi_team_dm.md](specs/approved/hi_team_dm.md).

### v0.62.1 — im:read

- [x] The manifest asks for `im:read` too: Hermes looks a DM up with it.
  `hi team up`'s warning says what each missing scope stops, rather than
  blaming `/hi-<team>` for a missing `im:history`.

### v0.62.2 — The helper under a strict policy

- [x] The helper's styles go in as a constructed stylesheet, so an app
  whose Content-Security-Policy has `style-src 'self'` no longer leaves
  the button unstyled at the bottom of the page. The pictures of what is
  pointed at still need inline styles there, so such an app's helper
  sends words without them.

### v0.63.0 — A look at new packages

- [x] A task that changes a `package.json` or its lockfile gives its
  reviewer the packages it adds, removes, and updates, with what to check
  and the audit to run, all read from git without running the project.
- [x] `hi team merge` refuses a task that puts a package in
  `trustedDependencies`, so its install script would run, without an
  owner's yes of its own (`--approval <id>`), and prints what changed in
  the packages for the lead to tell the people.
- [x] The `web` template's `make check` runs `bun audit --audit-level=high`.
  Approved spec: [hi_bun.md](specs/approved/hi_bun.md#dependency-review) (slice 2).

### v0.64.0 — The `app` template

- [x] `hi init app`: a web app on Bun alone, with `Bun.serve`, React pages
  bundled from an HTML import, SQLite in `$HI_DATA_DIR` with numbered
  migrations, `bun test` for the database, the server, and the pages,
  `HELP.md` for the helper, and `bun run build` for one executable.
  Approved spec: [hi_bun.md](specs/approved/hi_bun.md#app-step-2-bun-as-the-whole-stack) (slice 3).

### v0.64.1 — hi5sh/hi and hi5.sh

- [x] The repository moved to `hi5sh/hi`, and the site
  from hifin.sh to https://hi5.sh. `hi update` and the installer download
  from `hi5sh/hi`; older versions still update through GitHub's redirect.

### v0.65.0 — The `.hi5` folder

- [x] A project's records for `hi` (`template.json`, `data.json`, and
  `q.md`) live in `.hi5/`. `hi` still reads `.hifin/` in older projects,
  and `hi init --update` renames it to `.hi5/`.
- [x] The site at hi5.sh: HI + AI = hi5 on the home page, and the hi5
  name in the guide, the skill, and `hi`'s help.

### v0.66.0 — A team's project starts as a web app

- [x] `hi team new` starts a new repository as a blank web app: the `app`
  template with an empty page and no tables yet, the team's rules in
  `AGENTS.md`, and its purpose in `README.md`. The helper's chat is on
  every page as before. The lead builds on it unless the people need
  another stack. `empty` in the wizard, or `"empty": true` with
  `--from`, starts with only a README, as before.
  Approved spec: [hi_bun.md](specs/approved/hi_bun.md#app-step-2-bun-as-the-whole-stack) (slice 4).

### v0.67.0 — The blank app's first preview

- [x] `hi team up` puts a new team's blank app on the preview, so the
  links people get from `/hi` work at once. Only while the project is as
  `hi team new` wrote it; once work is merged, or for a project cloned or
  started empty, the lead makes the previews as before.

### v0.68.0 — A welcome page that walks people to the helper

- [x] A new team's app starts with a welcome page instead of a blank one:
  an arrow points at the chat button, and once the chat is open the page
  says what it is for, beside the panel. The helper tells the page where
  its button is and whether it is open (`<hi-helper data-corner
  data-open data-hidden>` and a `hi-helper` event).
- [x] Tests for the helper: `bun test` in happy-dom on the script hi
  serves, run by `go test` where Bun is installed and in CI whenever the
  helper changes.
- [x] A new team's lead gets its long-term memory: the Hindsight plugin
  failed to install where the lead had no plugins folder yet.

### v0.68.1 — Releases say when production has no helper

- [x] A release's or rollback's announcement in the channel says when the
  helper is off in production, so people trying it don't look for the
  chat. While it is on, the default, nothing is added.

### v0.69.0 — Answers to feedback

- [x] A reply in feedback's thread that starts with `>` is shown to the
  sender in the app's Feedback tab, under what they sent; other replies
  stay in Slack. The button gets a dot and a note when the team answers.
  A `hi_visitor` cookie, which only `/__hi/` gets, says which browser sent
  what, without anyone knowing who it is.
- [x] Feedback asks for a name: the box flashes until it's filled.
  Approved spec: [hi_team_helper_signin.md](specs/approved/hi_team_helper_signin.md#feedback).

### v0.70.0 — Addresses that stay

- [x] `hi team domain <domain>`: every team's app on the machine at
  `<team>.<domain>`, and its preview at `<team>-preview.<domain>`, on a
  domain verified in NetBird, instead of an address NetBird makes up each
  day. Renewed every 30 days on the same name, without a gap. So people's
  cookies for an app last, and the answers to their feedback reach them
  days later. If the name doesn't work, hi uses a made-up address and
  tries again after half an hour.
- [x] `hi net expose --domain <name>`, for up to 30 days: a NetBird
  service made through NetBird's API, since `netbird expose
  --with-custom-domain` always adds a random label.
  Approved spec: [hi_net_expose.md, Releases](specs/approved/hi_net_expose.md#releases) (3).

### v0.70.1 — The NetBird token from anywhere

- [x] `hi team domain` reads the NetBird API token from standard input
  when that isn't a terminal (`hi team domain <domain> < token.txt`), and
  saves a token given in `$NETBIRD_API_TOKEN` too, since the teams run as
  services without it.

### v0.70.2 — A NetBird token that works

- [x] `hi team domain` asks for another token when NetBird no longer
  knows the saved one, and takes one on request with `--new-token`.
- [x] It checks that the token may make services, not only read them: a
  Network Admin service user may list the custom domains but gets
  "permission denied" making a service; the token must be an Admin's.

### v0.70.3 — A check NetBird can't wave through

- [x] `hi team domain`'s check of the token makes its test service with
  this machine as the target: NetBird turned one without a target away as
  invalid before looking at the token's rights, so a Network Admin's token
  passed.

### v0.71.0 — A domain per team

- [x] `hi team domain <team> on|off`: the machine's domain is for the
  teams it's turned on for, off by default, so test teams keep addresses
  NetBird makes up. `off` moves a team back within a minute.
- [x] `hi team ls` no longer lists `settings.json` as a team.

### v0.72.0 — Teams on the new hi

- [x] `hi update` offers to restart the teams still running the hi they
  started with, as it does `hi server`: a team doesn't take up new
  settings or fixes until it restarts. `--restart` restarts them without
  asking, `--no-restart` prints the commands.
- [x] `hi team domain <team> on|off` says when that team still runs an
  older hi, which may not know the setting yet, and how to restart it.

### v0.72.2 — The helper sends again

- [x] Since v0.69.0 the helper sent nothing in chat or Ask: the name box,
  marked required for feedback, sits in the same form, and the browser
  refused the form while it was hidden and empty. hi checks the name
  itself, and the form has `novalidate`.

### v0.72.3 — The link once every proxy has the name

- [x] `hi net expose --domain` hands out the address only once every proxy
  NetBird answers the name from serves it. A new name's certificate
  reaches them one by one, and for a few minutes a browser that picked one
  without it got no page.

### v0.73.0 — App boxes on the new hi

- [x] A team's app boxes run the hi they started with (the models' relay,
  `hi team __app`), so after `hi update` and the team's restart, the
  preview and production move to new boxes with the new hi, beside the old
  ones and on the same addresses, without a gap. hi's version is part of
  the stamp that already did this for new settings, and a running preview
  now follows it too, as production did.

### v0.74.0 — Production is for using the app

- [x] On production, the panel opens on Ask and Feedback for everyone; the
  chat with the lead is a Change tab for people signed in with `/hi`, which
  says a change goes into dev first, with a preview link to try it, and
  reaches production with a release. Their feedback carries their Slack
  name, checked. A chat from production tells the lead the same. A
  preview's panel stays the chat.
  Spec: [hi_team_helper_signin.md](specs/approved/hi_team_helper_signin.md).

### v0.75.0 — Continuing work without losing any

- [x] `--on` given any task of an idea continues its latest work and says
  so, instead of branching from an older step and leaving work out; it is
  refused while any of the idea's work runs, when the idea split in two,
  and for merged, discarded, or review tasks, each with what to do instead.
- [x] A new task whose yes came later in the thread of an unmerged idea is
  refused, as more of that work is `--on`; `--new` says it really is
  separate. One yes may still start several tasks. Every new task lists
  the ideas still open.
- [x] A continuation is announced in the thread its work was agreed in,
  and carries what the people said there; the reviewer sends back work
  outside what they approved.
- [x] A continuation is told when dev changed the same files since, and a
  merge that conflicts starts the member's "bring it up to date" work,
  reviewed as usual, instead of only failing.
- [x] `hi team tasks` and `hi team status` show the open ideas as lines of
  tasks (t-4 > t-6 > t-7).
- [x] A continuation that was discarded or failed before it had a branch
  no longer hides the work it continued from the five-idea count.
  Spec: [hi_team_roles.md](specs/approved/hi_team_roles.md), idea 6.

### v0.76.0 — Roles that remember, and a map of the project

- [x] Each member's role has a home in the team's folder, `roles/<role>/`:
  `ROLE.md`, how it works on this team, and `LEARNED.md`, what its earlier
  tasks taught it. hi writes them once, from defaults for the coder,
  reviewer, devops, and researcher, and gives both to every brief for the
  role, whichever agent does it. The lead has them at `/team/roles`.
- [x] Members propose lessons as follow-ups starting with `Learned:`; the
  lead sees them with the report and keeps what's true.
- [x] A map of the project for the agents, `docs/map.md`, in a new team's
  app: what it has and where. Every brief says to read it, extend what it
  lists rather than build beside it, and keep it and `docs/decisions/`
  current, and to write it when it's missing; the reviewer sends back
  duplicates.
- [x] The lead's SOUL.md and skill say what goes where: the people and the
  conversation in its long-term memory, facts about the code in the map,
  decisions, and specs.
  Spec: [hi_team_roles.md](specs/approved/hi_team_roles.md), ideas 1 to 4.

### v0.77.0 — Roles from the template

- [x] A team can start from a team template: one that extends team-app,
  built in, from `HI_TEMPLATES_DIR`, or from the hi server's sources. The
  setup offers them beside "new" and "empty"; team.json records it.
- [x] A layer's `team/` folder is the team's, never the project's:
  `team/roles/<role>/ROLE.md` gives a role its instructions, ahead of hi's
  defaults, which now live in team-app's own `team/` folder. A cloned or
  empty project still takes a template's roles.
  Spec: [hi_team_roles.md](specs/approved/hi_team_roles.md), idea 5.

### v0.77.1 — An idea ends at merged work

- [x] Two lines of work that went on from the same merged task are two
  ideas: continuing one is no longer refused as a split, `hi team tasks`
  shows each line from the merged task it went on from rather than the
  whole history, and discarding one no longer discards the other. Found
  on a team that forked work before v0.75.0.
  Spec: [hi_team_roles.md](specs/approved/hi_team_roles.md), idea 6.

### v0.78.0 — Rename a team

- [x] `hi team rename <name> <new>` follows a renamed Slack app: the
  bot's handle, `/hi-<new>`, the service and the snapshot timer, the boxes
  named after the team, the snapshots' folder, and the folder when it is
  still `team-<name>`. A team that was up starts again. The memory bank and
  the snapshots stay filed under the name the team was made with
  (team.json's `made_as`), so what the lead remembers and every snapshot
  carry over. Refused while a task is running.
  Spec: [hi_team.md](specs/approved/hi_team.md), Commands.

### v0.78.1 — A renamed lead knows its name

- [x] `hi team rename` puts the new name in the two lines of the lead's
  SOUL.md that hi wrote with the old one; the rest is the lead's and
  stays.

### v0.78.2 — The rest of a renamed team's name

- [x] `hi team rename` also writes the folder's `slack-manifest.json`
  again, so it no longer gives Slack the old name and command, and puts
  the new name in the description of the lead's notes skill.

### v0.79.0 — Hermes on its own Python

- [x] A Hermes that runs on its own Python (its launcher in
  `hermes-agent/.hermes/bin`, since September 2026) runs that way in a
  team's lead box and in `hi agent hermes`: the command its launcher
  prints, its Python, and the environment its `facts.json` names, mounted
  under the box's Hermes home where Hermes looks for it, with its leases
  writable so a Hermes update doesn't remove it under a running lead.
  Before, hi kept starting Hermes from the old `venv/`, which Hermes no
  longer updates: new code on old packages, and a crash on Python 3.11.
  Hindsight's client goes into the command, since that Python ignores
  `PYTHONPATH`. An older Hermes still runs from `venv/`.

### v0.80.0 — Progress in the thread

- [x] `hi team progress <name> on` keeps a live card in the thread the
  work was agreed in (Slack's `chat.startStream` in plan mode): a title
  that says what happens now, and a line per task, running (for how long,
  in how many steps) or how it ended. The broker updates it every 20
  seconds and ends it a few minutes after nothing runs, since Slack marks
  a task still running as failed when a card stops; a task's details go
  once, since Slack adds them to what it has. Off by default.

### v0.80.1 — No hi in the app's box

- [x] hi is in an app's box only to start the app and relay its model
  calls, now off the box's PATH, so an app that looks for `hi` doesn't
  find it and try `hi agent`, which can't run there (no podman, no agents,
  no logins). Found on a team whose app preferred `hi agent codex` over
  OpenRouter and failed in the preview. The lead and the project's map
  say it: an agent's model, such as Codex's, goes through OpenRouter.

### v0.80.2 — A small fix's progress in its thread

- [x] A small fix gets a progress card too. `hi team task --small --in
  <id>` names the message it was asked for in: hi says it started in that
  thread, and the card goes there. Without `--in`, the card goes in the
  thread of hi's message in the channel. Found when a small fix ran with
  no card, since it had no yes to tell its thread.

### v0.80.3 — What needs someone at the machine

- [x] The lead's hi-team skill has a section on what only someone who
  runs hi on the team's machine can do (a key or setting for the app, the
  team's OpenRouter key, the Slack app's settings, a domain, GitHub,
  progress cards), with the commands. Whenever a plan depends on one, the
  lead says so in the same reply, with the exact command, and that people
  who don't run hi themselves ask their IT team; when it first suggests
  the option, not when the coder runs into it. Found when the lead
  suggested a data source that needs a key without saying how to add it.

### v0.80.4 — A progress card that lasts

- [x] The progress card is an ordinary reply with a plan block that hi
  edits, no longer a live message: Slack ends a live message after about
  ten minutes, even one updated every 20 seconds, and marks a task still
  running then as failed, while a task can run for an hour. A running task
  says for how long, in how many steps, and what its agent does now; hi
  edits the card only when that changes. A card someone deletes isn't
  posted again for that work.

### v0.80.5 — The lead hears how work ended, after a restart

- [x] A restart of the team (hi update --restart, a reboot) ended the
  lead's background hi team wait without its answer, and the lead went on
  telling the people the coder was at it while nobody was. The broker
  notes whether a wait or hi team show handed the lead how each piece of
  work ended; when work settles and none did within a minute, hi wakes
  the lead once, with a one-time job in its Hermes whose answer goes to
  the thread the work was agreed in. Work that settled before hi kept
  track counts as heard, so no team hears about old work.

### v0.80.6 — The progress card stays at the end of the thread

- [x] While work runs, a progress card that someone wrote below (the lead,
  most often) is posted again at the end of the thread and the old one
  deleted, so the card is the last thing in the thread. Once the work has
  settled, the card stays where it is.

### v0.80.7 — Only the specs are the lead's to write

- [x] The lead's SOUL.md says that it writes only the specs, in
  /team/specs, and that the map and a decision go into the project through
  a task for the coder: /team/repo is read-only in its box. It said facts
  about the code belong in the map, docs/decisions/, and the specs, and a
  lead tried to write a decision itself and hit the read-only folder. An
  existing SOUL.md is updated on the next start.

## Planned

### Guide — a diagram for every tool

Convention: [guide-diagrams.md](guide-diagrams.md).

- [x] A structure diagram on every tool's main page: box, agent, team,
  how `hi compute` works, managed compute and the `hi server` for admins,
  `hi net expose`, `hi init`, `hi q`, `hi data`, and autoresearch.
  `guideDiagramPages` in `guide_diagrams_test.go` keeps them there.

### Next — roles that remember

Spec: [hi_team_roles.md](specs/approved/hi_team_roles.md).

- [ ] Only if continuations clearly suffer without it: resuming a member's
  session for `--on`.

### Next — the helper

- [ ] An owner's yes in a DM for a release, announced in the channel.
- [ ] A daily digest in the channel of what was discussed in DMs.
- [ ] Pictures of what is pointed at under a `style-src 'self'` policy,
  which blocks the inline styles modern-screenshot needs.

### Later — `hi team`

Approved spec: [hi_team.md](specs/approved/hi_team.md).

- [ ] A security advisor that reviews the proxy's log, the members'
  commands, and merged changes, and tells the owners about anything risky
  rather than blocking the team; Hermes' `approvals.mode: smart` is one
  starting point.
- [ ] Restore a team whose folder is gone, or on another machine, from its
  bucket: with export and import.
- [ ] Nice to have: the lead could tell for itself when a message is meant
  for it, without an @-mention, using a small classifier trained on this
  one question, such as [Jev](https://docs.pipecat.ai/api-reference/server/classifiers/jev),
  TypeSafe's classification model, asked "is this message for the
  lead?". Until then it answers @-mentions only, which works well.

### Later — Bun

- [ ] A preview check before the lead says a preview is ready, unless
  `hi web check` covers it.
  Approved spec: [hi_bun.md](specs/approved/hi_bun.md).

### Later — More for the autoresearch templates

- [ ] Cross-validation and several seeds for `autoresearch-ml`, and its
  loss curves in a report.
- [ ] Data for `autoresearch-ml` beyond one text file.
- [ ] A record of every holdout look in `autoresearch-base`, so a holdout
  spent is visible.
- [ ] An interactive menu for autoresearch (asked for on 2026-10-17): one
  command that walks through a run's choices, such as the template and
  project, the data and folds, the agents, boxes, rounds, patience, and
  budget, with what each costs and trades off; starts the run; and then
  follows it: watch, stop, results, and the quality checks.

Finance work (`autoresearch-quant`'s data sources, fitted regime models)
moved on 2026-10-12 to the roadmap in the private templates repository,
`docs/roadmap.md`. Fitted models there need the boxes to allow more
libraries.

### Later — `hi compute`

- [ ] Runs to completion on RunPod, Shadeform, and managed providers:
  `hi compute run` there refuses today. Spec:
  [hi_compute_ssh_run.md](specs/ideas/hi_compute_ssh_run.md) (draft).
- [ ] An idle GPU flag from the providers' GPU metrics, on the dashboard
  and in `hi compute ls`. Spec:
  [hi_compute_idle.md](specs/ideas/hi_compute_idle.md) (draft).

### Later — Learn from agent-machine services

[boxd](https://docs.boxd.sh/) and similar services (Fly's Sprites, E2B,
Daytona, Modal Sandboxes) were studied for ideas that fit `hi compute` and
`hi server`. Candidates from boxd:

- Checkpoints before risky changes, and forking a running machine into an
  identical copy.
- Suspending idle machines and resuming them quickly, which pairs with the
  idle GPU flag (Later — `hi compute`).
- An HTTPS URL for every machine or `serve`, instead of an SSH tunnel.
- Environment variables and secrets set once and injected into every
  machine the user owns, never on Community Cloud (v0.12.2).
- Sharing a running machine with a teammate, recorded in the audit log.
- A live desktop view that a person can watch and take over from an agent.

- [ ] Turn the ideas worth building into draft specs in `specs/ideas/`.
  Started: [hi_compute_idle.md](specs/ideas/hi_compute_idle.md) has the
  idle GPU flag and why suspend and resume doesn't fit the current
  providers; [hi_compute_ssh_run.md](specs/ideas/hi_compute_ssh_run.md)
  covers runs on RunPod, Shadeform, and managed providers. The services
  were compared on 2026-10-01 in [hi_box.md](specs/approved/hi_box.md#services).

### Later — `hi skill`

- [ ] No private organizations hardcoded in hi: the verified owners in
  `skill_registry.go` list one, which a team's own setting, such as the
  hi server's, should add instead. The same goes for any other
  team-specific name in the code.
  Approved spec: [hi_skill.md](specs/approved/hi_skill.md).

### Later — `hi agent`

- [ ] Tiers (`--tier read|edit|full`), `--max`, `resume`, a configured
  agent order that moves on when one is out of quota, version checks, and
  nested calls through the box socket with depth, concurrency, and time
  limits. Spec: [hi_agent.md, Releases](specs/approved/hi_agent.md#releases) (2).
- [ ] Agents' sign-ins kept on the hi server, with spend limits, audit, and
  the live view. Spec: [hi_agent.md](specs/approved/hi_agent.md#sign-ins-on-the-server-release-3) (3).
- [ ] `--on <machine>` and `--remote`, and Antigravity CLI.
  Spec: [hi_agent.md, Releases](specs/approved/hi_agent.md#releases) (4).
- [ ] A planner that picks bundles for a task; bundles themselves shipped
  in v0.31.0. Spec: [hi_agent_bundles.md](specs/ideas/hi_agent_bundles.md),
  release 3 (draft).

### Later — `hi data` and `hi server expose`

Follow-ups to v0.24–v0.28, not scheduled yet.

- [ ] On hold (decided 2026-10-03): a line per organization in the weekly
  Slack report, from `data_usage.jsonl`.
  Spec: [hi_data.md, Records](specs/approved/hi_data.md#records).
- [ ] On hold (decided 2026-10-03): `hi data upload`, with its limits
  (which repositories or buckets, approval, review) to be decided then.
  Spec: [hi_data.md, Later: uploads](specs/approved/hi_data.md#later-uploads).
- [ ] A name that stays for `hi server expose`, as `hi net expose
  --domain` has (v0.70.0), so a restart of the exposure doesn't cut off
  runs already going.
  Spec: [hi_server_expose.md, Open questions](specs/approved/hi_server_expose.md#open-questions) (2) and
  [The exposure's lifetime](specs/approved/hi_server_expose.md#the-exposures-lifetime).
- [ ] `hi server expose on|off`, in place of `"expose_listen": "off"`.
  Spec: [hi_server_expose.md, Commands](specs/approved/hi_server_expose.md#commands).
- [ ] Colab through the proxy: a way to hand Colab the run token, so its
  runs get no one-hour links and can call `load_dataset`.
  Spec: [hi_data.md, Known limits](specs/approved/hi_data.md#known-limits) and
  [hi_server_expose.md, Cloud machines](specs/approved/hi_server_expose.md#cloud-machines).
- [ ] `--data` for container runs (`hi compute run <image> -- <command>`),
  with a shell-only downloader.
  Spec: [hi_data.md, Known limits](specs/approved/hi_data.md#known-limits).
- [ ] Boxes running longer than a day: the box proxy asks for a fresh hi
  data token before the old one expires.
  Spec: [hi_data.md, Known limits](specs/approved/hi_data.md#known-limits) and
  [`hi box --data`](specs/approved/hi_data.md#hi-box---data).
- [ ] More routes on the instance listener, each its own scope on the run
  token: the team model for `hi q` on cloud machines, and an idle signal so
  a machine can stop itself (with [hi_compute_idle.md](specs/ideas/hi_compute_idle.md)).
  Spec: [hi_server_expose.md, Releases](specs/approved/hi_server_expose.md#releases) (3).
- [ ] Decide one exposure for all runs or one per run, and whether to add
  NetBird's dashboard-only header check as a second lock.
  Spec: [hi_server_expose.md, Open questions](specs/approved/hi_server_expose.md#open-questions) (1, 3).
- [ ] The S3-compatible gateway for buckets (`s3.hf.co`), for tools that
  only speak S3.
  Spec: [hi_data.md, Open questions](specs/approved/hi_data.md#open-questions).

## Deferred until everything else is done

More providers come last, after every planned release above, and only if
they are needed. RunPod, Shadeform, Hugging Face Jobs, and Colab already
cover most needs.

### Deferred — Sign in with Google for a team's app

- [ ] Optional per team: people with no key link sign in with Google,
  limited to one Workspace domain such as example.com, in the relay, so key
  links keep working. Needs a Google OAuth client (Internal) with one
  return address per team. Decided 2026-10-16 to add later. Draft spec:
  [hi_team_sso.md](specs/ideas/hi_team_sso.md).

### Deferred — More compute providers

On-demand providers to add to `hi compute`, in priority order. Ranked
2026-09-29 on three criteria in order: an easy API, a low on-demand price,
and coverage across many countries. Prices are USD per GPU-hour for a single
GPU, checked 2026-09-28. Spot pricing played no part in the ranking. Specs
not yet written.

- [ ] Vast.ai. The cheapest option (H100 $1.87–2.66, A100 $0.74, RTX 4090
  $0.48) and the widest coverage, with hosts in 64 countries on six
  continents. REST with an API key, and search needs no account. Restrict it
  to verified datacenter hosts with high reliability scores, because a host
  going offline interrupts a job as surely as an eviction does. SSH goes
  through a mapped port, not port 22.
- [ ] Latitude.sh. REST with a Bearer key. Its plans endpoint returns price
  and stock per location. It has 25 locations across North America, Latin
  America, Europe, and Asia-Pacific. H100 is about $1.66 and L40S about $0.74
  (both unverified). First confirm which locations actually have GPUs.
- [ ] Prime Intellect. An aggregator with a REST API and a stock endpoint
  that includes country. H100 costs $2.43.
- [ ] TensorDock. REST v2, H100 from $2.25, and a claimed 100+ locations in
  20+ countries. Its API docs were partly unreachable.
- [ ] Novita AI. REST, with container pods reached over SSH like RunPod's.
  About 12 countries on five continents, and its on-demand prices are
  unverified.
- [ ] Verda. Finland only, but the only self-serve single B200 in the EU
  ($6.85). H100 $3.56. It has well-documented price and stock endpoints and
  is also reachable through Shadeform.
- [ ] Lambda. The simplest REST API, with 12 regions in the US, Japan, India,
  Germany, and Israel. H100 costs $3.29 PCIe or $4.29 SXM, and single H100s
  often sell out. It is also reachable through Shadeform.
- [ ] Hyperstack. REST with an API key and a Go SDK, in Canada, Norway, and
  the US. H100 PCIe is about $2.50, worked back from its spot price. It is
  also reachable through Shadeform.
- [ ] Scaleway. REST with a Go SDK and a catalog that needs no login, but EU
  only. H100 costs €2.87 and L4 €0.79. It is also reachable through
  Shadeform.

Build a direct driver for any provider reachable through Shadeform only if
going direct is clearly cheaper or more reliable. Left out:
- Nebius. H100 rises to $4.50 on 2026-10-01, and its API is gRPC.
- Spheron. Its on-demand price is high: H100 SXM costs $4.43.
- AWS, Azure, and Google Cloud. They cost 2–5 times as much on demand, and
  new accounts start with zero GPU quota.
- DigitalOcean. H100 costs $4.41, and new accounts start with zero GPU quota.
- Sales-led clouds: CoreWeave, Fluidstack, Nscale, TensorWave, and Cirrascale.
- CUDO Compute, which closed its self-serve platform on 2026-03-31.
- Container platforms without SSH: Salad, Koyeb, and Northflank.
- Model-inference APIs, which don't rent general compute: Together,
  Fireworks, Groq, and Replicate.

Dependency: each driver implements the existing `computeProvider` interface.
Supporting a host and port pair for SSH (Vast.ai, Novita) generalizes the
RunPod code.

### Deferred — More `hi q` backends

- [ ] Codex and opencode as `hi q` backends. Deferred on 2026-10-02: with the
  team model through `hi server` and OpenRouter, they add a second, slow
  path that mainly helps people without a team server.

### Deferred — Spot instances

- [ ] Add `--spot` to `hi compute up` and `hi compute run` to rent
  interruptible machines at a discount, keeping on-demand as the default.
  Show how much notice the provider gives before eviction when confirming,
  and show evicted instances as evicted, not stopped, in `hi compute ls` and
  `status`. Spec not yet written.

Dependency: needs a provider that sells spot. Colab, Hugging Face Jobs, and
RunPod do not (RunPod stopped selling spot pods in September 2026), so this
waits for a spot-capable driver such as Vast.ai, Verda, or Novita AI. Not
scheduled to a version yet.

### Deferred — Modal compute

- [ ] Add a Modal driver through Modal's official Go SDK.
  Approved spec: [hi_compute.md](specs/approved/hi_compute.md).

Waiting until Modal's Go SDK leaves beta and its package path settles. Not
scheduled to a version yet.
