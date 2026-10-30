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
  gpu3 serving gpu1).
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
  noor`.

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
- [x] Label requests from agents (`noor via Claude Code`), and add
  `hi connect --agent <name> --owner <user>` for agents that run on their
  own.
- [x] Verify live with a policy on gpu3 (2026-09-29): a staff start
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
  gpu3, SSH to the machine, and a stop.

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
- [x] Verify the wall live on gpu3 (2026-09-29): a screen attached over
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
- [x] Verify live on vmserver (2026-10-01): the server signed and served
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
  gpu2, 2026-10-01): `hi init ml` installed AMD's PyTorch 2.11 for gfx1151
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
  `halogen-qwen3.8-flash-next` already served on gpu2: correct tool calls
  and commands in 4–14 seconds, against about 2 seconds for Haiku through
  OpenRouter. The production server still uses OpenRouter.

### v0.22.1 — `hi update` restarts the server

- [x] `hi update` finds a `hi server run` still running the replaced binary,
  also when nothing new was downloaded, and asks to restart its systemd
  service (`sudo systemctl restart`, or `systemctl --user` for a user
  service). `--restart` and `--no-restart` for scripts.

### v0.22.2 — Podman in `hi install`

- [x] `hi install podman`: rootless Podman with `crun`, `uidmap`, and
  `passt`, and a subordinate ID range for users without one. Tested on gpu2
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
  with rootless Podman on gpu1; `--gpu` with Podman on gpu2 (gfx1151).
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
  Tested on a real Hugging Face job through a server on vmserver.
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
  Tested on gpu1 with NetBird 0.74.4.
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
  Tested on 2026-10-07 on gpu1 with rootless Podman, Claude Code, and Codex.
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
  Tested on 2026-10-07 on RunPod (RTX 2000 Ada) through vmserver:
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
  Tested on 2026-10-10 on gpu1: the lead's box with Hermes
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
  Verified live on 2026-10-10 on gpu1: the lead answered in its Slack
  channel.

### v0.37.3 — Team leads in `hi box ls`

- [x] `hi box ls` lists the team leads' running boxes after your own, with
  their team, channel, and folder, and points to `hi team` for them.

### v0.38.0 — Previews of a team's work

- [x] The lead's `hi team preview [<task>]` runs the project in an app box
  on the host (`make run` or `npm start`, on `$PORT`, published to
  localhost only) and puts it on a temporary password-protected address
  with `hi net expose`; the lead posts it. `hi team preview stop` ends it.
  Verified on 2026-10-10 on gpu1 with team test's world clock.
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
  Verified on 2026-10-11 on gpu1 with team test: 43 messages and an image
  logged, and the job saved a note to the lead's memory.

### v0.41.0 — A team lead with long-term memory, and no restart notices

- [x] The lead's long-term memory is Hindsight: a server in a container of
  its own on the team's network, its database in the team's `memory/`
  folder, its model calls through the team's proxy, and Hermes' hindsight
  plugin in the lead with a memory bank per team. The plugin is the one
  Hermes' catalog pins; its client library is installed once per machine
  and mounted read-only. `"memory": "off"` in `team.json` leaves it out.
  Verified on 2026-10-11 on gpu1 with team test: a turn of the lead was
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
  Verified on 2026-10-11 on gpu1 through NetBird: 401 without the key, 303
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
  production's code. Team bulletin's app scheduled its own 06:00 Slack post,
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
  in the channel is sent to `hi team env` instead of a brief. Team bulletin's
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
  Team bulletin's app lost what it wrote at every release.
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

### v0.80.8 — Notes for the members

- [x] What the lead learns that a member could use (research, how an
  outside service behaves, what the people shared) goes in
  docs/specs/notes/<topic>.md, committed with the specs, not only in the
  lead's memory, which no member reads. Every brief names the notes that
  matter for it, and every member's brief says to look there before
  researching something itself.

### v0.81.0 — The app's docs at /docs

- [x] Every team's app has its docs at `/docs` on its addresses: a page
  with two ways in, **User docs** and **Tech docs**
  ([hi_team_docs.md](specs/approved/hi_team_docs.md)). The user docs are
  `docs/user/`: why the app exists and who it's for (the owners' story),
  what's in it, how to do things. The tech docs are `docs/tech/` (the
  parts drawn with `boxes` diagrams, like the guide's, and Mermaid
  charts), the map, the decisions, the specs and notes, and a changelog
  hi writes from the releases. hi serves them from the version on the
  address, whatever the app is written in; `/docs/llms.txt` and
  `/docs/raw/` are for agents.
- [x] The helper's Ask answers from the user docs (HELP.md without them).
  The team-app template starts with both parts instead of HELP.md; the
  coder, the reviewer, and every brief keep them current.
- [x] The story is the team's north star: the lead asks an owner for it
  while it's missing, and reads it before planning. An existing SOUL.md
  gets the paragraph on the next start.
- [x] `hi net expose --route-always`: a route that stays when the helper
  is off, so the docs do.

### v0.81.1 — /docs on addresses that stay

- [x] An address on a name that stays kept its relay for 30 days, so a
  team with a domain wouldn't see `/docs` for weeks after the update.
  Each slot now remembers what its relay routes; when hi routes more, the
  address moves to a new relay on the same name with the same key
  (`hi net expose --key`), so people's links keep working and nobody gets
  new ones. Found updating a team whose addresses run until November.

### v0.81.2 — Releases post production's link

- [x] A release's message in the channel linked to hi's older message with
  production's link, so people jumped back through the channel to open
  it. It now posts production's link itself, and the lead does the same
  when people ask where production is.

### v0.81.3 — The docs' link in a release

- [x] A release's message has the docs' link on the line under
  production's link, on the same address with its key. An address on a
  relay from before `/docs` leaves it out until its next address.

### v0.82.0 — Charts drawn by pele

- [x] The docs' `mermaid` blocks are drawn by [pele](https://pele.run),
  which reads Mermaid's language and draws every chart type Mermaid has,
  in the page's colors, light or dark. hi serves it at `/docs/pele.js`
  instead of loading Mermaid from a CDN. Tried on 60 charts from real
  projects: the same ones drew as with Mermaid, clearer and faster.
- [x] Members know their charts: the lead's skill says which fits what,
  the map rule in every brief says mermaid blocks work in the docs, and
  the coder uses the `pele` package for charts in the app.
- [x] The hi guide draws `mermaid` blocks with pele too.

### v0.83.0 — A quieter channel, and hi team say

- [x] Work for dev no longer posts in the channel: work asked for in a
  DM, and small fixes started outside a thread, go to the owners in a DM
  (not to an owner who asked). The channel hears about dev once, before
  a release: `hi team release --ask` posts each change, who asked for
  it, and the preview's link, and an owner's yes in its thread releases.
- [x] `hi team say <name> <text>`: the machine's admin talks to the lead
  without Slack. hi posts it in the channel, the lead answers in its
  thread, and it is a go-ahead for the work it asks for, not a release.

### v0.83.1 — hi team say waits for a lead that is starting

- [x] A message sent while the lead's box was still starting, as right
  after `hi update --restart`, got "the lead isn't reachable yet" in its
  thread. It now waits up to five minutes for the lead.

### v0.83.2 — The release summary, complete

- [x] The summary before a release (`hi team release --ask`) has the
  preview's docs link beside its app link, as the release itself has
  production's.
- [x] Work shows as what was asked for: a fix after a review counted as
  "continuing earlier work" and left out the task it fixed, and who
  asked for it. Each piece of work is now its first task's title, with
  all its tasks (t-29, t-31) and who asked, in the summary and in the
  release's "Asked for by".
- [x] When an owner asks for the release ("push to prod"), the lead puts
  the summary in their thread (`--in <id>`), where their yes goes too.

### v0.84.0 — The relay waits for a restart

- [x] While a hi team's service restarted (`hi update --restart`), its
  addresses showed "hi net expose: nothing answers on
  /run/user/1000/hi-team-…/production.sock" for a few seconds, on /docs
  and the helper. The relay now waits up to ten seconds for the service,
  or the app, to answer again; a connection that never opened sent
  nothing, so this is safe for any request. If it still doesn't answer,
  a page load gets a page that says the app is restarting and tries
  again by itself, and anything else a short 503. hi's own readiness
  check doesn't wait.
- [x] Addresses on a team's domain move to a relay with this, on the same
  link and key, as they did for /docs in v0.81.1.

### v0.84.1 — Members draw boxes, not ASCII art

- [x] A team's tech docs had "the same thing as boxes" drawn in ASCII
  box-drawing characters, which /docs shows as text: the lead asked for
  "box diagrams" and the coder had never seen the boxes format, which
  lived only in hi's guide and the template's example page. Every brief
  now shows the boxes block's JSON with a working example and says never
  to draw in ASCII; the reviewer sends ASCII drawings back; and the
  lead's skill says to ask for boxes blocks by name.

### v0.85.0 — The docs from the helper

- [x] The helper has a **Docs** button between Ask and Feedback, which
  opens `/docs` in a new browser tab (in the same tab from a page of the
  docs), and "The app's docs" in its ⋯ menu for a preview's chat, which
  has no tabs. hi's status says where the docs are, only on an address
  whose relay sends `/docs/` to hi.

### v0.86.0 — The app's data across preview and production

Approved spec: [hi_team_data.md](specs/approved/hi_team_data.md).

- [x] Team orbit's first release started production on an empty data
  folder. Production then read 533k press releases again and called a
  model again for releases the preview had already scored, while the
  preview's workers kept running on the same keys. Every app box now gets
  `$HI_SLOT` (`preview` or `production`). The lead's skill, briefs, the
  project's `AGENTS.md`, and the templates say that background work that
  costs money, reads an outside service a lot, or acts outside the app
  runs only in production.
- [x] `hi team data <name>` lists both data folders. `hi team data <name>
  copy <from> <to>` makes one a copy of the other:
  - The source's box pauses while it is copied, so the copy is consistent.
  - The target's box restarts on the copy, on the same address.
  - Production's data is snapshotted first, and the copy is posted in the
    channel.
  - The preview's data from before is kept in `run/preview-data-before/`.
- [x] The lead runs `hi team data copy production preview` itself.
  `hi team data copy preview production` needs an owner's yes.
- [x] `hi team release --data preview` releases and starts production on a
  copy of the preview's data, under the same yes. `hi team release --ask`
  says when production has no data yet and the preview has some.

### v0.86.1 — The lead answers hi team say

- [x] A message from `hi team say` reached team orbit's lead, which
  searched the code for 13 minutes (200 model calls, repeating the same
  searches) and posted nothing; an owner asked in the thread, and a second
  conversation answered. hi now asks the lead to answer soon and leave the
  code to the member, posts "The lead is still working on this" in the
  thread after 2 minutes, and caps each turn of the lead's at 60 model
  calls (`agent.max_turns`), after which it answers with what it has.
  SOUL.md gains "Answer first."

### v0.86.2 — The lead starts the work, then answers

- [x] With v0.86.1's "answer soon", orbit's lead answered "I am on it"
  and ended its turn without starting the task: an answer ends the turn,
  and nothing happens until someone writes again. The lead now starts the
  work a message asks for (hi team task, with a short brief) in the same
  turn, then says what it started. SOUL.md's "Answer first." becomes
  "Be quick."

### v0.87.0 — A review of the whole repository

- [x] `hi agent review` has agents read a whole repository for problems
  ([hi_agent_review.md](specs/approved/hi_agent_review.md)): security
  and correctness by default, performance and maintainability on
  request. Each investigator works in a locked box on a snapshot of the
  working tree, on hi's own image, with Claude Code not loading the
  project's hooks, MCP servers, or permissions. hi checks every quoted
  line against the snapshot, and an agent that didn't raise a finding
  confirms or rejects it; findings two agents raised become one, and
  checks that disagree make it uncertain.
- [x] The report is advice: `report.md` opens with a note that the
  findings are recommendations to weigh, every finding says what it
  assumes, and each is a section that can go into a brief as it is.
  `--context` tells the reviewers what the code doesn't show. Reports
  are in `~/.local/state/hi/reviews/<id>/`, readable only by you.
- [x] `ls`, `show` (`--md`, `--json`, `--rejected`), `watch`, `stop`,
  and `rm`; `--budget`, `--max`, and `--parallel`. Nothing in the
  repository is changed, and no box, worktree, or branch is left.

### v0.88.0 — Reviews of repositories from elsewhere, and of what changed

- [x] `hi agent review github.com/owner/name` (or `owner/name`, `https://`,
  `git@`) clones the repository into the review's folder without running
  anything from it: no hooks, templates, or credential helpers from this
  machine, no LFS filters, submodules listed but not fetched, and only
  network transports. Private GitHub repositories come through `gh`; an
  address with a token in it is refused.
- [x] `--since <ref>` focuses the review on what changed, read with the
  whole repository around it. `--area` takes a brief of yours, or a name
  in the repository's `.hi5/review/` (not for a fetched repository, whose
  author wrote them).
- [x] `--compare <id|report.json>`: each earlier finding is still seen,
  not seen with its code changed, or not seen with its code unchanged,
  matched by fingerprint or by place; new findings are marked new.
- [x] `--sarif` writes `report.sarif`, `--out <dir>` a copy of the
  report, and `show --html` a page that loads nothing and runs nothing.

### v0.89.0 — A deep review before a release

- [x] A team's lead offers a deep review of the whole project before a
  risky release, with the most it may cost
  ([hi_agent_review.md](specs/approved/hi_agent_review.md#with-a-team)).
  Only an owner's yes in the channel, to a question of the lead's naming
  that figure, starts it: `hi team review --project --approval <id>
  --budget <usd>`. hi says so in the thread.
- [x] The reviewers get what the team knows: the purpose, whether
  production is on the internet and behind a password, who may use it,
  the decisions, and what the people told the lead. The coder's and
  reviewer's agents investigate, each checking the other's findings.
- [x] When it ends, the owner who said yes gets the report in a DM, the
  lead is woken in the thread to post a summary without exploit detail,
  say which findings it thinks matter here and which don't, and let the
  people choose. Fixes name the finding (`--finding r-4:F-1`), and the next
  review says what wasn't seen since which task.

### v0.89.1 — A deep review never gets production's key

- [x] The context a deep review's agents get named production's whole
  link, with the key that lets people in, and said anyone with the link
  could open it. It now gives the address without the key and says a key
  in the link guards it. Found in orbit's first deep review.

### v0.89.2 — A deep review's names, and the lead's words

- [x] A team's review boxes and report are named after the team, not its
  `repo/` folder: `hi agent review --name <name>`, which the broker passes.
- [x] The lead tells the people what a deep review found in their words:
  no file, function, or route names or jargon unless they ask, and each
  finding in a line of its own with its number. Both found in orbit's
  first deep review.

### v0.90.0 — A team on a project someone else wrote

- [x] `hi team adopt <name> <source>` makes a team whose project is one
  someone else wrote, such as one on GitHub
  ([hi_team_adopt.md](specs/approved/hi_team_adopt.md)): `hi team new` with
  the code step answered and the purpose offered from its description.
  In `hi team new`, a project's address in the code step adopts it too.
- [x] It's fetched without running anything from it, as `hi agent review`
  fetches (`fetchRepository`), and hi shows what it found before asking:
  the licence and what it means, size and history, languages, how it
  seems to run, its files for agents, and the packages with install
  scripts. Over 1 GB, hi asks before taking the whole history.
- [x] A repository this machine's `gh` can push to is the owner's choice,
  no by default; anything else is adopted. The source becomes `upstream`,
  with a push address git can't use, its tags under `upstream/`, and the
  team's branch tracks nothing. `--github` keeps a private copy, with
  the history.
- [x] One commit adds the team's rules in a hi block at the top of
  `AGENTS.md`, above the project's own notes, `docs/specs/`, and
  `.hi5/adopted.json`. It is v0 (`release-0`); `hi team discard dev`
  puts dev back to it before the first release, and releases start at v1.
- [x] The survey: on the first `hi team up`, hi has the coder make the
  project run on `$PORT` (`make hi-run` when its `run` means something
  else, which the preview now uses first), run its tests, map it, and
  say what's missing, up to $10 for Claude Code. When it ends, hi wakes
  the lead under its own message in the channel, to merge it and
  introduce the project in the people's words.
- [x] Members of an adopted team run Claude Code without the project's
  hooks, MCP servers, and permissions (`hi agent --no-project-settings`),
  unless `team.json` says `"project_agent_settings": true`. The lead's
  SOUL.md and hi-team skill say where the project came from and how to
  speak of it.

### v0.91.0 — Keeping up with the project a team adopted

- [x] The broker fetches upstream from the machine once a day, running
  nothing from it, through `gh`'s sign-in for a private repository, into
  `upstream/` branches and tags every member's worktree can read.
- [x] `hi team upstream <name>`, and `hi team upstream` in the lead's box:
  which of upstream's versions dev has, how many commits are the team's,
  what upstream has added since, its newer tags, and what the team has
  brought in.
- [x] A new upstream release (a tag such as `v2.4`) is posted in the
  channel at most once a week, and the lead is woken under it to say in
  the people's words what's new and ask whether they want it. The first
  check takes what's there as what the team started with.
- [x] `hi team task ... --upstream[=<tag>]` starts work, on a yes, by
  merging upstream's branch or a tag: the brief says how and what to
  report, the review checks the merge's own changes rather than the
  authors' commits, and `team.json` records each upstream merge.

### v0.92.0 — Contributing back to the project a team adopted

- [x] `hi team propose <task> --approval <id> --notes <file>` offers the
  adopted project a piece of the team's merged work, once an owner says
  yes: the change alone, without the team's specs, rules, and docs, on
  upstream's latest, pushed from the machine to a fork under its GitHub
  account, as a draft pull request with the lead's description and a
  line that agents made it. Work that doesn't apply waits for upstream to
  be brought in; work that brought upstream in, and work proposed
  already, are refused.
- [x] The lead's skill says when to offer it, to read the project's
  contributing guide first, and to ask an owner in plain words. The spec
  moves to approved with what was built.

### v0.93.0 — When something goes wrong

Approved spec: [hi_team_jobs.md](specs/approved/hi_team_jobs.md).

- [x] The app's long work reports as jobs: a file per job in
  `$HI_JOBS_DIR` (`/jobs` in the app's box, `run/<slot>-jobs/` on the
  machine), or `"$HI" team job <job> --done <n>`. Every brief says so, and
  that a job stops on an item it can't process unless its spec gives a
  rule: it never skips one on its own.
- [x] The broker reads production's and the preview's jobs every minute,
  without a model, and opens an incident when a job is blocked or failed,
  its count stops moving, or it stops writing; also when production's app
  stops on its own, a member's task fails, or a member's report has an
  `Alert:` follow-up. One thread and one record each
  (`incidents/i-<n>.json`); hi closes a job's incident when it moves again.
- [x] The lead is woken once per incident with the job's file, the app's
  last lines, and the people's rules for the job. It handles what can be
  undone on its own (retry, one restart, re-briefing, a rule's fix), asks
  about the rest, and logs each action with `--incident <id> --because`:
  hi posts it in the thread and keeps it in the record. hi reminds the
  owners once when a question waits a day.
- [x] `hi team jobs` gives each job's state, count, rate a day from hi's
  hourly counts, and last error; `hi team incidents` the incidents, their
  timelines, and the rules (`hi team incidents rule ... --approval <id>`);
  `hi team alert` raises one from the lead's box. A month of job history
  is kept.

### v0.94.0 — Starting with the lead

- [x] A new team's app starts with "What should your app do?" and a
  message box in the middle of the page, for someone signed in through
  Slack. Sent, the box moves over to the helper's side and fades, the
  panel opens there (production's Change tab), and the message goes to
  the lead. Anyone else still gets the arrow at the chat button. The
  helper says whether whoever is there can chat (`data-chat`) and takes a
  message from the page with a `hi-helper-send` event.
- [x] A chat in the app from someone signed in through Slack is copied
  into a thread in their DM with the bot, not the channel, where a copy of
  every chat was too much. Chats without a checked person, and threads
  already in the channel, stay there.

### v0.95.0 — The chat, beside the page

- [x] A new chat from the pen button in the helper's header, or `/clear`
  (or `/new`) typed as a message, so a new change doesn't carry the last
  one's talk; nothing goes to the lead.
- [x] The clock button lists the person's past chats on that address, the
  latest first; opening one makes it the chat again, and the lead still
  has all of it (`GET /__hi/api/conversations`, `POST
  /__hi/api/conversation/open`).
- [x] The button starts bottom left and the panel opens on the left. Its
  inner edge drags it wider or narrower (320 px to all but 240 px of the
  window), lights up while it does, and the browser keeps the width;
  `<hi-helper data-width>` tells the page.
- [x] Spec draft for `/` commands in the chat:
  [hi_team_commands.md](specs/ideas/hi_team_commands.md).
- [x] The compute guide lists Shadeform with the other providers.

### v0.95.1 — The lead follows its threads

- [x] Once someone @-mentions the lead, it answers every later reply in
  that thread without a fresh @, and so in the threads it started, such
  as "Ready for production": a plain "yes" there reaches it. A reply that
  opens by @-mentioning someone else stays between people. Hermes'
  `strict_mention` is off and `ignore_other_user_mentions` on.

### v0.95.2 — No "Interrupting current task"

- [x] When someone writes while the lead is working, it takes the message
  without posting Hermes' "Interrupting current task" and its `/busy`
  tip (`display.busy_ack_enabled: false`).

### v0.96.0 — The lead shows it is thinking

- [x] While the lead works, Slack shows that it is thinking: its own
  animated line in the thread, and "orbit is thinking…" under the reply
  box, naming what it does without paths or commands
  (`display.live_status: verb`).
- [x] The team's manifest turns on Slack's agent view (not the assistant
  view Slack retires in February 2027) and asks for `assistant:write`;
  `hi team up` warns until an app has it. Paste `hi team manifest
  <name>` into the app's App Manifest and reinstall.

### v0.96.1 — Long work hi couldn't see

- [x] A job that stops on its own (too many failures in a row, a limit)
  writes `failed` or `blocked` with why first, and long work a change
  touches is made to report: every brief says so, and the reviewer counts
  long work that would stop silently as a change needed.
- [x] Once per team whose dev has merged work and no job has reported,
  hi asks the lead in the channel to find the app's long work that hi
  can't see, and to start its fix as a small task.
  Approved spec: [hi_team_jobs.md](specs/approved/hi_team_jobs.md).

### v0.97.0 — One line per version

- [x] A new version gets one line in the channel, from the lead's bot:
  "New DEV version (v15 + 2 changes) :test_tube: -> App: link - Docs:
  link" when the preview shows a new dev, and "New PROD version (v16)
  :rocket: -> App: link - Docs: link" on a release. What it brings goes
  in the line's thread: the lead's notes (`hi team preview --notes`, or
  the merged work), and for production who approved and who asked.
- [x] An owner may release dev from their DM with the lead, `--approval
  dm`; a rollback and production's helper still need the channel.
  Approved spec: [hi_team.md](specs/approved/hi_team.md).

### v0.97.1 — A yes in a DM's thread

- [x] hi reads the replies in a DM's threads when it checks a yes given
  there: Slack's DM history leaves them out, so a yes in a thread went
  unseen and an older message at the top of the DM counted instead.
  Approved spec: [hi_team_dm.md](specs/approved/hi_team_dm.md).

### v0.98.0 — Asked where the owner is

- [x] `hi team release --ask` run from an owner's DM asks them in that
  DM, never the channel; their yes there releases dev, and the channel
  gets only the PROD line. The lead's notes saying otherwise are marked
  out of date.
- [x] In the channel, the ask is one line, with what production would
  get and the preview's links in its thread, where the owners say yes.
- [x] The lead's "is thinking…" line shows again: Hermes v0.22.2 sent its
  status to Slack's new Agent Sessions API, which takes only a lifecycle
  state and refused every call. hi hides that API from the lead's Hermes,
  which then uses `assistant.threads.setStatus`.
  Approved spec: [hi_team.md](specs/approved/hi_team.md).

### v0.98.1 — No alarm for hi's own restart

- [x] For 10 minutes after the team starts, hi holds job incidents: a
  restart cuts the app's model calls, and a job that trips on that and
  moves again raises nothing. One that doesn't recover opens its
  incident then, saying when hi started the team.
  Approved spec: [hi_team_jobs.md](specs/approved/hi_team_jobs.md).

### v0.98.2 — A sign of life in a DM

- [x] A new message at the top of a DM gets "Looking into it…" in its
  thread at once, from a Hermes hook hi writes, and the lead's status
  shows there while it works; hi deletes the line once the lead has
  answered. Slack shows a status only in a thread, and before this a DM
  had only the 👀 for minutes.
- [x] Before the lead says work is stuck or proposes a fix, it checks hi
  team jobs and hi team versions: orbit once proposed two fixes that
  production v11 already had, from memory.
  Approved spec: [hi_team_dm.md](specs/approved/hi_team_dm.md).

### v0.99.0 — Dev where it's asked for

- [x] A new dev version goes to the person working on it: hi posts the
  DEV line, its app and docs links, and the lead's notes in the thread
  or DM the lead is answering, not in the channel, where every iteration
  was noise for everyone else. `hi team preview --channel` posts it in
  the channel when the people ask for that. The lead's SOUL.md and the
  hi-team skill say so.
  Approved spec: [hi_team.md](specs/approved/hi_team.md).

### v1.0.0 — `hi agent brainstorm`, and the full version

hi is complete enough for 1.0: workstations, boxes, agents, servers and
teams all ship. This release adds brainstorms, since the best ideas
often come when several models trade ideas.

- [x] `hi agent brainstorm "<question>"` asks several agents (codex,
  claude, and gemini, which is Hermes with Google's Gemini, by default)
  the same question, each alone in a box on a snapshot of the project,
  with `--data` folders read-only or `--web` for looking at other
  services. Each must write its idea and may add artifacts; one more
  agent compiles them into one proposal, crediting each.
- [x] In a team, the lead offers a brainstorm on a really hard question,
  asking first and naming the agents; when the people ask for one, it
  starts it. On their yes, `hi team brainstorm` runs it on dev with the
  app's data, and when it ends hi posts every idea in full where it was
  asked and wakes the lead there to compile the proposal.
  Approved spec: [hi_agent_brainstorm.md](specs/approved/hi_agent_brainstorm.md).

### v1.0.1 — A long DM follows SOUL.md

- [x] A conversation already going follows the lead's SOUL.md as it is
  when the team starts. Hermes kept each session's instructions from its
  first message, so a DM with the lead, one long session, never saw a
  change: asked to brainstorm, it brainstormed alone instead of starting
  `hi team brainstorm`. hi now has each session whose instructions lack
  the current SOUL.md build them again on its next message, keeping the
  conversation.
  Approved spec: [hi_team.md](specs/approved/hi_team.md).

### v1.0.2 — A slow start is waited for

- [x] A new version gets up to 30 minutes to answer while its app still
  runs, not 3: migrations on a 4 GB database before the app listened
  took longer, and hi gave up halfway through them. An app that stops
  fails at once, with its last lines. The hi-team skill tells the lead to
  run hi team preview and hi team release in the background, since its
  terminal gives up on a command after 3 minutes.
  Approved spec: [hi_team.md](specs/approved/hi_team.md).

### v1.0.4 — The lead hands code on before its steps run out

- [x] The lead's reading of the code is scouting, for the member's head
  start: SOUL.md says it finds where the answer or the change lives,
  puts that in the brief under "Where to look", and stops, instead of
  "a few files at most". On team orbit 11 of 286 turns used all 60
  steps, mostly searching /team/repo/src to work out the fix itself,
  and the next turn read the same code again.
- [x] Past a dozen reads and searches in one turn, hi's plugin in the
  lead's Hermes adds a line to each result: you have enough pointers,
  put them in the brief and start the task, or note them on the
  incident, then answer.
- [x] At 45 of the 60 steps Hermes tells the lead once to keep what it
  found, and SOUL.md says where: the brief, the spec, or an incident
  note, which the next turn reads before the code. A check hi wakes the
  lead for hands the code to a member.
- [x] The budget, the warning, and the nudge say which model they were
  set for (gemini-3.8-flash) and are to be checked again on another.
  Approved spec: [hi_team_lead_steps.md](specs/approved/hi_team_lead_steps.md).

### v1.0.5 — The lead looks at dev before it proposes

- [x] Before proposing new work, the lead runs `hi team versions` and
  reads `docs/map.md`, says what dev already has, and proposes only
  what's missing: in SOUL.md, which every turn sees, and in the hi-team
  skill's go-ahead section. On team helpdesk the DM's top level ended at
  the lead's own Ask / Plan proposal, while the yes, t-87, and its merge
  were in a thread of the same DM, a Hermes session of its own; nine
  hours later it proposed the same four steps again.
  Approved spec: [hi_team.md](specs/approved/hi_team.md).

### v1.1.0 — A preview runs on a fork of production

Approved spec: [hi_team_data_fork.md](specs/approved/hi_team_data_fork.md).

- [x] Team orbit's preview held 47 GB to production's 157 GB and fell
  further behind each day: its workers run only in production, and
  catching up was a full copy. Now the preview runs on a fork of
  production: hi hardlinks production's files and copies in full only
  the databases and the files being written, and the preview's box mounts
  the fork read only with a folder of its own over it (a rootless Podman
  overlay). On orbit's data, 155 GB in 210k files linked in 4.6 s,
  with no disk.
- [x] The preview gets a new fork at every release, so the next
  migration runs on a copy of production's real data first.
  `hi team data <name> fork` (and `hi team data fork` in the lead's box)
  forks now; `copy production preview` is another name for it.
- [x] `hi team env <name> --production KEY` keeps a key out of the
  preview; `--preview KEY` gives it a value of its own.
- [x] Each slot has its own models socket. The preview's model calls stop
  for the day at $5 (`hi team limit <name> preview <usd|off>`), counted
  from OpenRouter's `usage.cost`.
- [x] `hi team data <name> preview empty` starts the preview empty at
  every release, for an app whose people's data previews shouldn't show.
- [x] With Docker, or where hardlinks don't work, the preview gets a
  whole copy, as before, and `hi team data` says why.

### v1.1.1 — A fork links what is new too

- [x] Orbit's first fork copied 102 GB in 124k files, with production
  paused for three minutes: a backfill had downloaded that many PDFs that
  day, and v1.1.0 copied every file changed in the last day. It copies
  what changed in the last ten minutes now, besides the databases, and
  links the rest however new.
  Approved spec: [hi_team_data_fork.md](specs/approved/hi_team_data_fork.md).

### v1.2.0 — An exposure only from some addresses, or only on the VPN

- [x] `hi net expose --allow-ip <ip,cidr>`: NetBird's proxy lets in only
  those addresses, set through NetBird's API as Reverse Proxy > Access
  Control does, since `netbird expose` has no flag for it. The address
  lets nobody in until NetBird has the rule, and hi puts it back if it
  goes.
- [x] `hi net expose --vpn`: no public address, only devices on the
  NetBird network, at `http://<NetBird name>:<port>`.
- [x] `hi team allow-ip <team> <ip,cidr>|off`: a team's preview and
  production let in only those addresses, moving to addresses with the
  rule within a minute, with the same key.
  Approved spec: [hi_net_expose.md](specs/approved/hi_net_expose.md).

### v1.3.0 — One box runs the app's workers, handed over at every deploy

Approved spec: [hi_team_workers_handover.md](specs/approved/hi_team_workers_handover.md).

- [x] Team orbit's production failed to start four times on 2026-10-21
  with "database is locked": at a deploy the new box starts beside the
  old one, and the old box's workers held the database. Now each slot has
  a workers' lock that the relay in each box holds for it, and
  `$HI_WORKERS_FILE` exists in the box that has it. At a release,
  rollback, or restart, hi tells the old box to let go, waits for its
  workers to stop (`$HI_WORKERS_FILE.stopped`, or 30 s, `HI_WORKERS_GRACE`
  up to 300), and only then starts the new box. A new box that fails
  gives the old one its workers back.
- [x] `hi team jobs` says which box runs the workers; a production box
  that waits five minutes for them is an incident.
- [x] Briefs, the reviewer's checklist, `AGENTS.md`, the lead's skill,
  and the team-app roles carry the rule; once per team, the lead checks
  an app that reports jobs and briefs the change.
- [x] Apps that ignore the rule, and boxes started by an older hi,
  overlap as before.

### v1.3.1 — A job's rate after its count starts over

- [x] `hi team jobs` showed orbit's volatility-backfill at "-73,826 a
  day": its count went from 112,221 to 0 with a new run, and the rate
  took the week's first sample. A count that goes down starts the rate
  over from there.


### v1.4.0 — A team watched from outside: the heartbeat

Approved spec: [hi_watch.md](specs/approved/hi_watch.md), release 1.

- [x] A team on a connected machine sends its hi server a signed
  heartbeat every minute: each part's state, disk, OOM kills, the last
  snapshot, open incidents, spend through hi and on the key, and the
  network log from the offset the server has.
- [x] The server opens an incident when a team goes quiet (3 minutes, 10
  after a restart it announced, none after `hi team down`) and pushes
  the owners through ntfy; it runs the health rules, keeps the network
  log, pings a dead-man's switch, and tells the team's channel what the
  lead can act on.
- [x] `hi server watch` (with `--deadman`, `--ntfy`, `--channel`,
  `subscribe`, `forget`), `/hi teams`, and a line in `hi team status`.

### v1.5.0 — Bills of materials and advisories (hi watch release 2)

Approved spec: [hi_watch.md](specs/approved/hi_watch.md), release 2.

- [x] Each release gets a CycloneDX 1.6 bill of what production runs
  (`hi team bom`): the app's lockfiles, the OS packages in hi's base
  image and the memory image, the runtimes, hi, the agents, the lead's
  skills, and the services. The heartbeat names it; the server asks for
  one it doesn't have.
- [x] The server matches every production bill against OSV each night,
  with CISA's KEV and FIRST's EPSS: an exploited advisory is an incident
  and a push, a likely or critical one with a fix an incident, the rest
  the weekly report. The app's own go to the team's channel.
- [x] `hi team advisories`, and `hi team advisory … ignore --because`,
  kept as VEX, until the advisory changes.
- [x] The dependency review lists what OSV knows for the versions a task
  adds; one known to be exploited needs an owner's yes to merge.

### v1.6.0 — Network rules and stopping a team (hi watch release 3)

Approved spec: [hi_watch.md](specs/approved/hi_watch.md), release 3.

- [x] The proxy logs a `closed` line with bytes up, bytes down, and
  seconds, and an `allowlist-added` line when a box may reach more.
- [x] The app's boxes look names up through a resolver of the team's,
  which logs each name: Podman's pasta carries the box's port 53 to it,
  with pasta's own as the second nameserver.
- [x] The heartbeat carries the lead's, the member boxes', and the app's
  logs; the server raises refused bursts, raw addresses, paste and
  tunnel hosts (`watch-hosts.txt`), uploads over 50 MB an hour, and new
  allowlist hosts, and a weekly line of hosts production hadn't looked
  up in 30 days.
- [x] `hi server watch freeze|unfreeze|pause|unpause` and `/hi team
  freeze …`: done by the team with its next heartbeat. A burst to paste
  or tunnel hosts, or an exploited advisory in the app's own packages,
  freezes on its own (`--auto-freeze off`).
- [x] Once a week the team compares production's box with its bill;
  what the bill doesn't list is an incident.

### v1.7.0 — Revoke and cut off (hi watch release 4)

Approved spec: [hi_watch.md](specs/approved/hi_watch.md), release 4.

- [x] With an OpenRouter provisioning key (`hi server watch --openrouter`)
  the server makes a team's key (`hi team key <name> openrouter
  --from-server`), and an admin can disable it (`revoke`) or replace it
  (`rekey`, delivered with the next heartbeat).
- [x] With a NetBird API token (`hi server watch --netbird`) an admin can
  take a team's machine off the network (`cutoff`): peer approval again
  where the account has it, else out of its groups, naming a policy
  that still lets All in; `reconnect` undoes it.
- [x] `/hi team revoke|rekey|cutoff|reconnect` in Slack; all four in the
  audit log, the watch channel, and pushed to the owners.

### v1.7.1 — Production must answer and its work must move

Found on team orbit: production stopped answering at boot because its
backfills held the app's only thread, and the fix switched them off by
default. After a release the history analysis then stood for 12 hours,
59,477 releases left: hi's incident opened, and the lead chose to wait for
a task without asking anyone.

- [x] Every brief, and the coder's and reviewer's roles: long work runs off
  the thread that answers requests, with short writes to the database;
  never make the app answer by switching its work off; work held back on
  purpose writes `blocked` with how to resume it.
- [x] The lead: waiting with production's work stopped is the owners'
  choice, asked with what it costs; a fix a member calls a workaround
  gets a task for the cause before the incident closes.
- [x] A production incident about a job, the app, or its workers that has
  stood 4 hours with nobody asked tells the owners in its thread, once.

### v1.7.2 — Plain /hi is the hi server's

- [x] The lead's plugin answers only the team's own `/hi-<team>`, not plain
  `/hi` as well: a team app whose manifest still had `/hi` would take it
  from the hi server's app when reinstalled.

### v1.8.0 — The lead on a ChatGPT plan

Approved spec: [hi_team_subscriptions.md](specs/approved/hi_team_subscriptions.md), release 1.

- [x] A member's `provider` (openrouter or chatgpt) and `fallback`; the
  wizard asks it for Hermes, signs the team in to ChatGPT with a device
  code, offers the plan's models, and checks the fallback on OpenRouter.
  `hi team signin <name> chatgpt [--machine]`, `hi team member`.
- [x] Only the team's service refreshes its ChatGPT sign-in, three days
  before the token runs out; the proxy puts the token and the account's
  headers on the lead's calls under `/backend-api/codex/`.
- [x] The lead on a named provider in the Responses API, with Hermes' own
  fallback to the same model on OpenRouter through the proxy; Hermes'
  notice on each switch is off, and the channel hears once per role, and
  again after a new sign-in.
- [x] Checked end to end on gpu1 with hi's proxy and Hermes: the lead
  answered on ChatGPT, and with the token gone fell back to OpenRouter.
  The fallback entry carries its own address and key: without them Hermes
  looked for an OpenRouter key of its own.

### v1.9.0 — The coder on the team's Claude plan, and each member's effort

Approved spec: [hi_team_subscriptions.md](specs/approved/hi_team_subscriptions.md), release 3.

- [x] The team's own Claude token (`hi team signin <name> claude`, the
  wizard, `HI_TEAM_CLAUDE_TOKEN`), which members' proxies read before this
  machine's sign-in.
- [x] A Claude member's proxy resends a call Claude refuses (sign-in, or
  the plan's limit) to OpenRouter's Anthropic endpoint, with each call's
  model by its OpenRouter name, and keeps to OpenRouter for 15 minutes;
  the channel hears once per role, and the fallback's spend counts with
  the team's.
- [x] Each member's effort, medium unless team.json says (`hi team member
  --effort`, the wizard): the machine's own settings never reached the
  boxes, and the reviewer ran `gpt-6.1-sol` at its default, low.
  `hi agent --effort` for any run.
- [x] Checked end to end on gpu1 with hi's proxy and Claude Code: Claude
  answered on the machine's sign-in, and with a token it refused, the
  proxy fell back to OpenRouter.

### v1.10.0 — The reviewer through the proxy

Approved spec: [hi_team_subscriptions.md](specs/approved/hi_team_subscriptions.md), release 2.

- [x] A team's Codex member calls ChatGPT through its proxy on a custom
  provider with a placeholder key: no `auth.json` in the box, and no box
  spending the team's single-use refresh token. The team's ChatGPT
  sign-in, else this machine's; a machine whose Codex is on an API key
  keeps it in the box, as before.
- [x] When ChatGPT refuses it, the proxy resends the call to OpenRouter's
  Responses API on the same model, without the reasoning ChatGPT
  encrypted for its own account; the channel hears once per role.
- [x] The wizard signs the team in to ChatGPT for a Codex member too, once
  for every role, and checks its fallback.
- [x] Checked end to end on gpu1 with hi's proxy and Codex: a tool call on
  ChatGPT, and with the token gone, the same on OpenRouter.

### v1.11.0 — Signing in again from Slack

Approved spec: [hi_team_subscriptions.md](specs/approved/hi_team_subscriptions.md), release 4.

- [x] `/hi-<team> signin chatgpt`, owners only: a device code in that
  owner's DM; the team's service waits for it, keeps the new sign-in, and
  says so in the DM. One at a time.
- [x] `/hi-<team> signin claude`, owners only: a one-time page on the
  team's app, for 15 minutes, where the owner pastes a token from claude
  setup-token, which never goes through Slack.
- [x] The channel's message when a role falls back names the Slack
  command for an owner.

### v1.11.1 — A new team's coder and reviewer have a browser

Found on team orbit: a dark-mode change took a second review because
the lead's spec asked for the pages to be looked at and the coder's box
had no browser; the fix built Chromium by hand from downloaded packages,
and every UI task would again.

- [x] A new team's coder and reviewer get the `web` bundle (a browser and
  agent-browser) by default. Orbit's got it by hand.

### v1.11.2 — And the data bundle

- [x] A new team's coder and reviewer get the `data` bundle too (DuckDB
  and charts), beside `web`. Orbit's got it by hand.

### v1.11.3 — The lead gives up on a silent call after 2 minutes

Found on team orbit: an owner's yes to a release went unanswered. A
ChatGPT call sent nothing back, and Hermes, taking the team's proxy for a
model on the machine because its address is private, would have waited
15 minutes before retrying or falling back.

- [x] The lead, and Hermes in a member's box, wait 2 minutes for a model
  call's first word (`agent.local_stream_stale_timeout`), then retry or
  fall back to OpenRouter.

### v1.11.4 — Claude Code edits with Edit

Found on team orbit: a coder's 24-minute task read files with sed and
cat 44 times and changed them with 31 Python scripts in the shell, never
with Edit; of its 101,000 tokens, about 11,000 were the code it wrote.

- [x] Claude Code's tasks say to read with Read and change files with Edit
  or Write, and to use the shell to run things.

### v1.11.5 — The lead's memory on a cheap model, and its cost counted

Found on team orbit: since v1.8.0 Hindsight kept the lead's memory with
the lead's fallback, openai/gpt-6.1-sol, about $2 in five hours, and hi
counted its calls as free because Hindsight never asks OpenRouter for the
cost.

- [x] Hindsight uses the team's `memory_model`, else this machine's Hermes
  model, never the lead's fallback by default.
- [x] The team's proxy and the app's models socket ask OpenRouter for each
  call's cost when the caller didn't, so every call counts in the team's
  spend and limits.

### v1.12.0 — Review rounds that end, and briefs that say what's new

Approved spec: [hi_team_rounds.md](specs/approved/hi_team_rounds.md). Found
on team orbit: downloading one index's prices took 7 tasks and 68
minutes, unfinished, with "ultrathink" in every coder brief.

- [x] A person's thinking keyword reaches the first task started on their
  message, once; hi replaces it in every other brief and tells the lead.
- [x] A continuation's brief no longer repeats the first brief and the
  thread: they are files, and the brief says where.
- [x] The lead hears when its brief is over 800 words; its skill and soul
  ask for a few hundred.
- [x] The reviewer lists what only the running app can show as "Verify on
  the preview:", and edge cases outside the acceptance list as "Later:";
  a second review checks the first one's changes.
- [x] An approval with preview checks tells the lead to check them on the
  preview before merging.
- [x] Two review rounds, then the people decide: going on needs their yes.
- [x] A small fix doesn't start while other work is under way, without a
  yes.

### v1.12.1 — Review notes for the lead stay the lead's

- [x] A fix round's brief lists only the coder's changes, not what the lead
  checks on the preview, what is for later, alerts, or lessons.
- [x] A review whose notes are all for the lead starts no fix round.
- [x] A check that fails without the work too, such as a new advisory, is
  an alert for the lead, not the work's change needed.

### v1.13.0 — An agent's conversation outlives its box

Approved spec: [hi_team_threads.md](specs/approved/hi_team_threads.md),
release 1.

- [x] `hi agent --session <folder>` keeps Claude Code's or Codex's session
  in a folder, and the next box given the folder resumes it.
- [x] A session isn't resumed for another agent or model, when its file is
  gone, or past three quarters of the model's window (half until v1.14.7).
- [x] A resumed run's report counts its own steps and tokens, and says
  which session it went on with.

### v1.14.1 — The coder and the reviewer keep their conversation

(v1.14.0 was tagged by mistake on v1.13.0's commit, and is the same as
v1.13.0.)

Approved spec: [hi_team_threads.md](specs/approved/hi_team_threads.md),
release 2.

- [x] Each piece of work keeps one session per role, in
  `tasks/<first task>-sessions/<role>`.
- [x] A fix round, or the lead's continuation, is the next turn in the
  coder's conversation: only what's new, without the rules, role, and
  how the work started again.
- [x] A second review is the next turn in the reviewer's: what changed
  since it last looked, and whether its changes were made.
- [x] A member whose conversation can't go on starts a new one with the
  whole brief and the last report; a resumed run that fails sets its
  session aside.
- [x] Sessions go a week after the work is merged or discarded.

### v1.14.2 — New work is tried on production's data of now

- [x] `hi team preview t-N` for new work gives the preview a fresh fork of
  production's data first, when its fork is over an hour old; a fix or
  follow-up of the work on the preview keeps its fork.

### v1.14.3 — Checks go to the people, and quick work doesn't wait on review

Found on team orbit: a chart was done and approved 15 minutes after it
was asked for; the next 22 went on three coder tasks, each reviewed, that
only checked it on the preview.

- [x] What a review lists to verify on the preview goes to the people with
  the preview, as what to try, not into a task.
- [x] A task that changes only docs/ since an approval gets no review, and
  merges on that approval.
- [x] Quick work (a small fix, or at most 100 changed lines outside docs/)
  merges while its review runs; a review that then asks for changes wakes
  the lead to fix them in dev.

### v1.14.4 — A snapshot pauses the team for seconds, not minutes

- [x] A snapshot reads what is new while everything runs, then pauses the
  lead, its memory, and production only for what changed since. On
  orbit, v21's release had paused them for about 4 minutes.

### v1.14.5 — One message for a new dev version

- [x] When hi posts the DEV line in the thread or DM, the lead doesn't post
  the same again in its own words, before or after it. On orbit it had
  posted three messages within 15 seconds.

### v1.14.6 — An owner who asks for a release isn't asked again

- [x] An owner's "push to prod" in a thread is the release's yes: the lead
  releases with it, rather than posting "Ready for production?" and
  waiting for another. On orbit an owner had to say it twice, for v21
  and v22.

### v1.14.7 and v1.14.8 — A member's conversation goes on to 75% of its window

- [x] A session is resumed until it holds 75% of the model's window, not
  half: on orbit a first build or an investigation passed half alone.
- [x] The window is the one Claude Code reports (1,000,000 tokens for
  Opus 5.5), not an assumed 200,000.

### v2.7.0 — Sign in with the organisation's provider

Spec: [hi_team_sso.md](specs/approved/hi_team_sso.md).

- [x] `hi team sso provider add <name> <issuer>` keeps an OpenID Connect
  provider on the machine (Google Workspace, Entra ID, Keycloak, Auth0,
  Okta, and others), after checking its discovery document: the issuer to
  the character (Auth0's ends in `/`), the code flow and S256 PKCE, and
  not Entra's shared `common`. It asks for the client ID and secret, or
  takes `--client-id` and the secret on standard input.
- [x] `hi team sso <team> on <provider>` lets people without the link sign
  in to the team's production and preview, on a name that stays, with
  `--domain` (Google's `hd`, or a verified email's domain), `--group`
  (the groups claim, or `--groups-claim`), or `--anyone`. It refuses
  Google without a domain and Entra by domain, and prints the two return
  addresses to register. Key links keep working.
- [x] The relay (`hi net expose --sso <file>`) sends a page load with no
  key and no session to the provider with state, PKCE, and a nonce,
  checks the ID token (`go-oidc`, `x/oauth2`) and the rules, and keeps a
  signed 14-day session; the app never sees its cookies. New rules or
  `off` end the sessions at once. `hi team sso <team>` shows the last
  sign-ins and refusals.

### v2.6.51 — gemini-api in Antigravity's boxes

- [x] An agy box gets Google's Gemini API CLI (`gemini-api`) when this
  machine has it (`hi install gemini-api`, the release checked against its
  sums) and a key (`hi agent token gemini`): video, speech, music,
  transcripts, analysis, and deep research, beside agy's own images and
  web search on the plan.
- [x] The key stays outside the box: the box's `gemini-api` is a wrapper
  with the proxy as its `--server-url` and a placeholder key, and the
  proxy puts the key on each call and sends an upload's session address
  back through itself. Webhooks, triggers, credentials, environments, and
  agent definitions are refused.
- [x] A `gemini-api` skill in the box (Google's agent instructions, what
  each command is for, and that calls are billed) tells agy when to use
  it, so a task only says what it wants.
- [x] agy's own Google sign-in doesn't reach the Gemini API: Google
  answers ACCESS_TOKEN_SCOPE_INSUFFICIENT to its token (tried
  2026-10-30). Tried end to end with a placeholder key: agy saw the skill,
  its call went through the proxy, and Google answered for the key the
  proxy put on.

### v2.6.50 — The terminal connects again by itself

- [x] When the dev page's terminal closes (the box restarted, the
  network dropped), it says why and connects again every second until a
  try works, then the notice goes. Connect again still tries at once. A
  locked page, or a Vibe Dev's, doesn't retry. The chat already polls
  every 1.5 seconds and carries on after a failed poll, so it needed
  nothing.

### v2.6.49 — hi agent agy: Antigravity, on a Google AI plan

- [x] `hi agent agy` runs Google's Antigravity CLI in a box, as the
  fourth agent after Claude Code, Codex, and Hermes, on the Google AI
  Pro or Ultra plan it's signed in with. `hi install agy` installs it;
  `agy` once signs in.
- [x] The box gets a copy of the Google sign-in
  (`~/.gemini/antigravity-cli/antigravity-oauth-token`), a trusted
  folder, and the hosts agy needs (`daily-cloudcode-pa.googleapis.com`
  and the token's refresh). Its telemetry (`play.googleapis.com`) is
  refused; measured on agy 1.4.11, 2026-10-30.
- [x] The task goes as `-p`'s value (agy can't read it from stdin), or as
  a file it reads past 100 KB. The JSON result is the report: the answer,
  the conversation, tokens, and an ERROR turn as a failure with why.
- [x] `--model` (such as `gemini-4-argon-high`) and `--effort` pass
  through; best-of, review, and brainstorm take `agy` in `--agents`.

### v2.6.48 — Vibe Devs and Pro Devs release

- [x] Release on the dev page is for every builder: owners, Pro Devs,
  and Vibe Devs. The click is their yes, logged with their name, as an
  owner's was. Starting production with Stage's data at the first
  release, rollbacks, and setting dev back to production stay the
  owners'; the server refuses them for a developer, and the page hides
  them.
- [x] The dev box's skills, `hi dev`, the lead, the guide, and the site
  say who releases now.

### v2.6.47 — No AutoFill bar over an iPad's keyboard

- [x] Safari on an iPad put its AutoFill bar (passwords, cards,
  addresses) over the keyboard while typing in Start something new, a
  box inside a form with the agent's radio buttons. The start card is no
  longer a form (Start and Ctrl+Enter still send), and the boxes for
  Start something new, New task, a task's notes, and the chat ask for no
  AutoFill.

### v2.6.46 — A message sent while Claude works stays in the chat

- [x] A message sent from the chat while Claude is at work is in the
  transcript as a queued command (an attachment Claude reads with its
  next tool's result), not a message of its own, so the chat dropped it
  once its "sending" bubble went. The chat shows it where Claude read
  it, with its pictures' paths.

### v2.6.45 — Rewind, as a timeline beside the chat

- [x] Beside a Claude Code conversation in the Chat view (on a wide
  screen), **Rewind** lists its messages as a vertical timeline: each
  with when, and what the code did after it, counted from Claude Code's
  checkpoints (`~/.claude/file-history`) as its `/rewind` counts them.
  A message opens to **Code and chat**, **Chat only**, or **Code only**;
  the confirmation says what the code goes back by.
- [x] hi drives `/rewind` in the terminal: Esc Esc opens it (clearing
  what's typed first), the message is as many up from "(current)", and
  hi checks the one chosen is the chat's, picks the option by its name,
  and clears the message Claude Code puts back in its prompt. It goes
  to the chat's box instead. Anything unexpected, and it backs out with
  Esc, having changed nothing.
- [x] The chat shows the conversation's branch: a transcript keeps what
  a rewind took out, and a message after it follows an earlier one. hi
  walks the newest record's parents (across compactions), and until the
  next message, remembers the rewind itself.
- [x] Codex 0.164 can edit a previous message (a fork of the
  conversation) but keeps no checkpoints of the code, so the timeline is
  Claude Code's only.

### v2.6.44 — Allow a site from Slack, and the agent hears it

- [x] The dev box's ask for a site has **Allow** and **Say no** buttons
  in the ops channel; an owner's click answers it as on the dev page.
  hi's plugin for the lead hears the click and asks hi, which checks the
  one who clicked is an owner, and that the button brings the ask's key,
  which only the buttons carry: the lead can't answer one by itself.
  Then the post says who answered, in place of the buttons.
- [x] An agent whose `hi dev allow --wait` stopped waiting hears the
  owners' answer in its session, as if typed in its chat; one that still
  waits hears it there, and nothing else.

### v2.6.43 — Pictures as pills in tasks too

- [x] New task and a task's notes take pictures as Start something new
  does: pasted, dropped, or picked, each is an **Image #1** pill where it
  went (**File #2** for another kind), with previews along the box's
  bottom; ✕, Backspace, or Delete takes one out. The text says
  `[image: image-1.png]` where each was, and the files go with it.

### v2.6.42 — Names beside member IDs

- [x] `hi team access` and `hi team owners` say each member's name
  beside their Slack member ID: the name hi has from the links it sent
  them, else Slack's. An admin, or an agent, sees who is who without
  looking IDs up.
- [x] `hi team owners <team> add` makes someone an owner only: a Pro
  Dev, Vibe Dev, or contributor level they had goes, so they aren't
  listed twice.

### v2.6.41 — Who asked, and who started it

- [x] Tasks shows who asked for a task, from its "Asked by … in the
  channel" or "Filed by … on the dev page", not the forge's account the
  lead files it as; the task's page says both, and the search finds
  either. `hi team tickets <team> show` says it too.
- [x] hi keeps who started each task's work (the owner or developer on
  the dev page, the one who asked the lead, or the machine's admin), and
  the Code tab's workspace says "Started by …"; Tasks says so when it
  isn't who asked. A start on the dev page leaves a note on the task, as
  the lead's does.

### v2.6.40 — Start something new is a task, with pictures

- [x] Start something new files what you wrote as a task, its first line
  the title, and starts it like one: the task's branch (`12-…`, no more
  `adhoc-…`), its worktree, and the agent told to read it. Done ✓ closes
  it.
- [x] Pictures paste (or drop) into its box as **Image #1** pills, with
  previews along the box's bottom; ✕, Backspace, or Delete takes one
  out. They go with the task as its files, `[image: image-1.png]` in its
  text where each was.

### v2.6.39 — Room to write in Start something new

- [x] The dev page's Start something new box starts small, grows tall
  when you click into it, and grows with what you write, up to most of
  the window. The explainer is gone, and the agent choice and Start
  share a row.

### v2.6.38 — No session recap in the dev box

- [x] Claude Code in the dev box starts with its session recap off
  (`awaySummaryEnabled: false`), as it starts on Opus: the chat and the
  channel say what happened while you were away. An owner who turns it
  on in `/config` keeps it on.

### v2.6.37 — The chat sees a question at once

- [x] Behind the chat, VS Code, or another tab, the dev page's terminal
  takes the size its panel would have, not xterm's 80×24: Claude Code
  drew a question into a pane too small to hold it, which the chat
  couldn't read until the Terminal view had been opened once.

### v2.6.36 — Templates pass their own check again

- [x] The templates' `.claude/settings.json` lists what agents may not
  read one per line, as biome formats it: since v2.6.26, a new web, app,
  or team app project failed its own `make check` until `make fix`.

### v2.6.35 — One Release button, and buttons that spin

- [x] The dev page's Release tab has one Release button, in Production's
  row with Set dev back to production beside it, owners only; the card
  below lists the releases alone. Releasing shows "Releasing v5…" there
  with a spinner until production runs it.
- [x] A button that asks hi for something (Open, Done, Ship to Dev, Try
  on Stage, Show dev, roll back, Make public, an incident's Restart and
  Close) spins and waits until hi answers, even when the page refreshes
  meanwhile.

### v2.6.34 — The server watches nothing

- [x] The hi server's watch is gone (hi_team_ops.md, release 4): its
  endpoints answer 410, and `hi server watch`, `/hi teams`, ntfy pushes,
  the dead-man's switch, and the OpenRouter keys it made, with revoke,
  rekey, and cut off, are removed. The rules live on as each team's
  watcher. `hi server init` suggests `hi heartbeat`, and `hi server run`
  says what replaced an old watch.

### v2.6.33 — Only unplanned restarts

- [x] The watcher's restart rule counts only the starts nobody asked for
  (a crash, a kill, a lost machine): a stop systemd asks for, for an
  update, `hi team up`, or `hi team down`, marks the next start as
  planned. A day of releases no longer opens an incident.

### v2.6.32 — The team watches itself

- [x] The hi server's health, network, and advisory rules run in each
  team's broker, on its own machine (hi_team_ops.md, release 3), and
  every incident they open is the team's, in the ops channel. Teams send
  the server nothing; it answers an old team's heartbeat with 410 and
  closes what it had open for it. `hi team watch`, `hi team freeze`,
  `unfreeze`, `pause`, and `unpause` work from the machine.

### v2.6.31 — Heartbeats

- [x] A team's heartbeat and the machine's (hi_team_ops.md, release 2):
  a GET to a service such as healthchecks.io, which alerts when they
  stop. The wizard's last step asks for both; `hi team heartbeat <team>
  <url> [--every 1m]` sends the team's from its service while it runs,
  and `hi heartbeat <url> [--every 5m]` the machine's from a systemd user
  timer. Only the URL's host is ever shown. `hi team status` says when a
  team has none.
- [x] An owner's answer to a dev box's site ask is said in the ask's
  thread.

### v2.6.30 — Permissions in ops only

- [x] Changes to permissions are said only in the ops channel: hi's posts
  about levels, access, addresses, sites, and keys go nowhere else, and
  the lead's skill says it discusses them only there or in a DM. The
  snapshots' warnings and production getting a copy of the preview's
  data move to ops too.

### v2.6.29 — An ops channel

- [x] A team can have a second Slack channel for operations and
  security (hi_team_ops.md, release 1): incidents and everything in their
  threads, jobs' and workers' notices, sign-ins that stopped, snapshots'
  warnings, access changes, and the dev box's site asks go there, and the
  main channel keeps releases, Stage, tasks, ideas, and the app. The
  wizard asks for it; `hi team ops <team> <ID>|off` sets it later. The
  lead talks in both, plainly in main and technically in ops.

### v2.6.28 — Pictures in the terminal

- [x] A picture pasted into the dev page's terminal went nowhere: Ctrl+V
  reached the box as `^V`, and Claude Code looked for a picture in the
  box's clipboard, which is empty. Ctrl+V is the browser's paste now; a
  picture goes into the box (`~/hi/chat/terminal/`) and its path is
  pasted, which Claude Code takes as the picture. Text pastes as before.

### v2.6.27 — Opus in the dev box

- [x] Claude Code in the dev box ran on Sonnet unless someone picked a
  model: it sees a placeholder for the team's sign-in, not its plan, so
  it took the API's default. hi sets Opus in the box's settings when
  nobody chose one, at each start of the team; an owner's `/model`
  choice stays.

### v2.6.26 — Agents read .env.example

- [x] New projects' Claude Code settings denied reading `.env.*`, which
  took `.env.example` too, the file the rules tell agents to add a
  setting's name to. They deny `.env`, `.env.local`, `.env.*.local`, and
  the development, production, staging, and test variants instead;
  `hi init --update` brings a project's unedited settings up to them.

### v2.6.25 — Allowing a site from the machine

- [x] `hi team dev <team> allow <site> [--why <what for>]` allows a site
  or a database's name for the dev box as the owners do on the dev page,
  `--remove` takes one off, and with no site it lists them and the asks
  waiting. The guide had pointed admins at `hi box allow
  team-<team>-dev`, which never named the dev box.

### v2.6.24 — A database from the dev box

- [x] The dev box reaches a database on the internet that speaks TLS on
  MongoDB's port, such as MongoDB Atlas, once the owners allow its name
  (`hi dev allow`): its proxy answers the box's DNS for allowed names and
  their SRV records' shards, and joins the driver's TLS connection to the
  name it asks for, end to end, so an X.509 certificate signs in. Other
  names don't resolve.
- [x] `hi team env <team> --dev` gives the dev box settings and files of
  its own, such as a test database's user; it never gets the app's.
- [x] A dev box started before this version gets its DNS from its next
  start (`hi team dev <team> restart`).

### v2.6.23 — Files as settings

- [x] `hi team env <team> --file KEY=<path>` gives the app a file, such
  as a MongoDB Atlas X.509 certificate with its private key: hi keeps it
  with the team's settings, the preview's and production's boxes get it
  read-only at `/hi-files/KEY`, and `$KEY` holds that path. `--preview`
  and `--production` work as for values; the dev box, the repository,
  and snapshots never get it. The dev page's settings form takes a file
  too.

### v2.6.22 — Background tasks as notes

- [x] When a command Claude Code left running finishes, Claude Code puts
  a `<task-notification>` in the person's turn, and the chat showed its
  raw tags as a message from them. It shows a note with its summary.

### v2.6.21 — Submit from the chat

- [x] Claude Code's review of a question's answers ("Ready to submit your
  answers?") has no "Esc to cancel" under it, so herdr didn't call the
  agent blocked and the chat showed nothing while it waited. The chat
  reads Claude's screen whatever herdr says, and shows its questions,
  the review too, whenever their tabs are on it.

### v2.6.20 — Pro Devs and Vibe Devs

- [x] A developer is a **Pro Dev** (the agents' Chat, the terminal, VS
  Code, and SSH into the dev box) or a **Vibe Dev** (the agents' Chat
  only), set on the dev page's People table or with `hi team access
  <team> <ID> pro|vibe`. A Vibe Dev's page has no terminal or VS Code and
  opens no terminal; the gate refuses them those routes and an SSH key,
  and a Pro Dev moved to Vibe Dev loses their open terminal and their
  key. The agents still run commands for a Vibe Dev: it's a simpler
  view, not a wall.

### v2.6.19 — Room for real loads

- [x] A team's dev box ran its app's tests on real data and had a
  process killed for memory three times in two minutes, at the dev box's
  8 GB. The dev box has 16 GB unless the team sets its own, and
  `hi team limit <team> memory dev|app <size>` sets the dev box's, or
  Stage's and production's boxes', for one team, up to three quarters
  of the machine. Running boxes get the new limit at once (`podman
  update`), so an agent at work isn't stopped.

### v2.6.18 — A fresh start

- [x] hi's repository starts over from this version, with no history
  before it. The docs, specs, and tests name no real organisation, team,
  machine, or person: teams are demo, orbit, quarry, helpdesk, and
  bulletin, machines gpu1 to gpu3, and domains example.com and
  corp.example.

### v2.6.17 — A name for each address, and Stage

Spec: [hi_team_addresses.md](specs/approved/hi_team_addresses.md).

- [x] `hi team addresses <team> names <production's name>` gives each
  address a name of its own, which the IT team keeps: production at the
  name, Stage at `stage.` and the dev page at `dev.` under it, or any
  three names (`production= stage= dev=`). For an IT team that won't put
  several ports on one name.
- [x] Both ways an IT team points them work at once: A records to the
  team's NetBird address, where port 80 now sends each name to its
  address, or their reverse proxy to the ports as before. `--https` is
  for TLS ended in front: the links say https. The setup asks it as a
  fourth choice, and `hi team addresses <team>` prints what to ask the IT
  team for.
- [x] **Stage** is what the preview is called where people read it: the
  dev page, the Slack links, the lead's words, and the help. Commands
  take `stage` and `--stage` as well as `preview`.

### v2.6.16 — One sentence, then the next

- [x] Taking a developer's level read "…the lead doesn't take their
  requests: their SSH key is out of the dev box…". What ended is a
  sentence of its own now: "U07ANNA has no level in team demo; they
  don't report, and the lead doesn't take their requests. Their SSH key
  is out of the dev box, and their private links have ended."

### v2.6.15 — Levels are given, on a table of everyone

- [x] Being in the team's channel gives nobody a level any more: owners
  tick Contributor for each person, as they do Developer. Without a
  level, someone can use the app and ask the lead questions, but the
  lead files no tasks for them (`hi team web may` tells it), doesn't
  answer their DMs, and `/hi-<team>` gives them no link. `team.json` has
  `contributors` in place of `no_reports`; a team's contributors start
  empty.
- [x] The dev page's People is a table: everyone with a level and
  everyone in the channel, owners first, then developers, contributors,
  and the rest, each by name, with a search box and a radio per level.
  Owner stays the machine admin's. Making a developer, taking a
  developer down, or taking a level away asks first; the channel hears
  every change.

### v2.6.14 — People are people

- [x] The dev page's People listed the team's own lead as someone
  in the channel an owner could make a developer. The channel's members
  are its people now: bots and apps, Slackbot, and deactivated accounts
  are left out, from the same users.info call that names them.

### v2.6.13 — Owners, developers, contributors, and viewers

Spec: [hi_team_dev_access.md](specs/approved/hi_team_dev_access.md).

- [x] **Developers**, whom owners set on the dev page's new People
  section (Security tab) or with `hi team access <team> <@user>
  developer`, work in the dev box and on the dev page as owners do, but
  don't release, roll back, or change production's data or settings, the
  team's keys, the sites the box reaches, the addresses, or who is a
  developer. One gate in front of the dev page lets a developer use only
  the routes named as theirs; every other route, and any added later,
  is the owners'. The page hides the owners' buttons and the Data tab.
- [x] Terminal, VS Code, agent chat, starting work, uploads, and SSH are
  one level: they all run in the same box. The page and `hi team access`
  say that a developer can read production's data through a preview,
  unless the preview starts empty.
- [x] Taking a level away counts at once: links and the SSH key end, the
  next click is refused, and an open terminal or VS Code closes within
  15 seconds, also when `hi team access` changed it. The channel hears
  each change made on the page.
- [x] Developers' SSH keys go into the dev box with the owners', set on
  the dev page (Your SSH key) or with `hi team access <team> key`; the
  lead starts work in the dev box for a developer too.
- [x] **Contributors**, everyone in the channel, get their own links from
  `/hi-<team>` (only owners did). Signed in, the helper's **Report** files
  what they send as a task at once, with their name, the page, the
  version, their browser, and what they pointed at and attached, and the
  channel hears ":lady_beetle: @Anna reported #31". An owner can stop
  someone's reports (`none`), which also ends their links.
- [x] **Viewers**' anonymous Feedback only reaches the channel, for
  people to handle: the lead no longer files it on its own.

### v2.6.12 — A box's user who is root by another name

- [x] Boxes run as the user who runs hi (`--userns=keep-id`), so an
  escape from one is that user. On gpu1 that user was in the docker and
  lxd groups, either of which is root without a password. `hi team up`
  now warns when the user is in docker, lxd, or incus-admin, and gives
  the `sudo gpasswd -d` an admin runs.

### v2.6.11 — A match a restart cut off is tried again

- [x] The hi server took a team's new bill and was restarted while matching
  it; having tried, it waited an hour to try again. A match that didn't
  end is tried again when the server starts.

### v2.6.10 — An image's advisory with a fix that works

- [x] `hi team rebuild <team>` builds hi's base image again from a fresh
  Ubuntu, with no cached layers and Ubuntu's packages upgraded, and
  restarts the team on it. Its preview and production move to the new
  image within minutes, on the same addresses, and production's bill is
  made again, so the server's next match closes what the rebuild fixed.
  It names the machine's other teams still on the old image. The base
  image upgrades Ubuntu's packages at every build now. The dev box's
  image follows the base image's ID, so `hi team dev <team> restart`
  moves it too.
- [x] An image advisory's incident said to `podman rmi` the image and
  `hi team up`. That fails while the team's boxes use the image, and
  `hi team up` doesn't build again anyway. It says `hi team rebuild` now,
  and its title says the package is in hi's base image, not the app.
- [x] One CVE was two incidents: Ubuntu's notice (USN) and its record of
  the CVE. Advisories about the same CVEs in the same package are one
  now, which names the others ("also USN-8956-1").
- [x] The weekly report said "0 with a fix" above "4 of them with a fix".
  It counts the open incidents now, says what its counts leave out, and
  how each image's count changed since the last report.
- [x] An advisory's summary is cut at 200 characters, not bytes, so the
  message never has half a character.

### v2.6.9 — An idle agent's calls aren't a box being steered

- [x] `hi server watch` counted every connection a box was refused, so
  Claude Code sitting idle in a dev box, calling api.anthropic.com around
  hi's proxy every few seconds, opened "refused 21 connections in 10
  minutes". The hosts hi refuses on purpose (the agents' and herdr's own
  calls, which the dev page already leaves out) don't count now.

### v2.6.8 — The Slack app without the agent experience

- [x] A team's Slack app has Slack's Agent experience off, and its MCP
  server: the manifest has no `agent_view`, no `assistant:write`, and no
  `app_context_changed`, and hi no longer asks an app for them. The lead
  shows it's working with a reaction on the message; Slack's "thinking…"
  line under the reply box needs the agent experience, which an owner
  can turn on in the app's settings. Apps made before keep theirs.

### v2.6.6 — The setup asks what the addresses are named

- [x] `hi team new` ends with a Network step: the NetBird setup key, then
  what the team's addresses are named. When the machine has a domain
  (`hi net domain`), it offers it, on https; or a host name the IT team
  keeps in its DNS by hand, pointing at the team's NetBird address, on
  plain http with the NetBird name's ports; or the NetBird name itself.
  Before, every team on a machine with a domain went on it, unasked.
- [x] `hi team addresses <team> [domain|netbird|<host>]` shows or changes
  it for a team made before, and restarts the team to move them.
  team.json keeps it as `addresses`.

### v2.6.5 — A setup like hi install's, and a lead with a name

- [x] At a terminal, `hi team new` asks as `hi install` does: a field at a
  time, checked as you answer, with why under it when it isn't right; a
  list for the agents, providers, sign-ins, efforts, and the project; a
  hidden field for tokens. Without a terminal it asks a line each, as
  before. Without a name, it asks for the team's first.
- [x] The Slack manifest is YAML, 32 lines instead of 70, and the setup
  offers to copy it to the clipboard (OSC 52: over SSH, and through tmux
  with `set-clipboard on` or `allow-passthrough on`).
  `hi team manifest` prints YAML too; the folder keeps
  `slack-manifest.json`.
- [x] The lead can have a name of its own, such as a person's: the
  setup's first question. The Slack app has it, its handle is the name in
  the letters Slack allows (Maja Ström is `@maja-strom`), and its soul
  and the app's helper use it. team.json keeps it as the lead's `name`.

### v2.6.4 — A new team under a renamed team's name

- [x] A team renamed with `hi team rename` keeps its memory bank and its
  snapshots under the name it was made with, so `hi team new` with that
  name again shared them with it. The new team is made as `<name>-2`
  (then `-3`, and on) instead: its own memory bank and snapshots.

### v2.6.3 — Updates see a new release

- [x] `hi update` said "up to date" a quarter of an hour after v2.6.2 was
  out: GitHub answers a `/releases/latest` request without an `Accept`
  header from a stale cache, and Go's client sends none. hi sends
  `Accept: */*` now, as browsers and curl do.

### v2.6.2 — Viewers in VS Code

- [x] VS Code on the dev page gets viewers from Open VSX, each pinned to
  a version: PDFs (`mathematic.vscode-pdf`, pdf.js 6.2 under a strict
  CSP), CSV as a table and in colours (`janisdd.vscode-edit-csv`,
  `mechatroner.rainbow-csv`), Mermaid in Markdown's preview
  (`bierner.markdown-mermaid`), and notebooks' rich outputs
  (`ms-toolsai.jupyter-renderers`). They go in beside Claude Code's, once,
  and again when hi's list changes. They draw in webviews, so they need
  the team on https; on http the view says so. Checked in Chromium on
  team quarry.
- [x] Not taken: `tomoki1207.pdf` and `cweijan.vscode-office` (pdf.js
  older than 4.2.67 with eval allowed, CVE-2024-4367; vscode-office also
  takes over Markdown's editor), `battlecook.omni-viewer` (the default
  for some 70 formats, audio and video among them), and
  `qwtel.sqlite-viewer` (its own licence, with a paid tier).

### v2.6.1 — No private organization in hi

- [x] `hi skill`'s verified owners no longer list a team's own GitHub
  organization. The hi server sends the GitHub owner of each template
  source with its catalog (`owner`), devices cache it beside the
  templates, and skills from those owners are marked ✓; `hi disconnect`
  forgets them with the cache.
  Spec: [hi_skills_sh.md](specs/approved/hi_skills_sh.md).

### v2.6.0 — Adopt a folder

Spec: [hi_team_adopt.md, A folder on this machine](specs/approved/hi_team_adopt.md#a-folder-on-this-machine).
Moving team helpdesk from hi 1.x showed the need: its code, in a clean
team, without the old history or release tags.

- [x] `hi team adopt <name> <folder>` copies a folder on this machine into
  the team's repository as one commit: what git would commit there, or
  every file but caches outside git, never `.git`. Files that look like
  secrets (`.env`, keys, `.npmrc`, `credentials*.json`) and links that
  point outside it stay behind, listed under "left out". The folder is
  only read, with git's fsmonitor and hooks off.
- [x] The rest is an adoption: the summary, the team's rules around the
  project's own, v0, `--github`, and the survey. `adopted.local` in
  team.json; `hi team upstream`, `hi dev upstream`, and `hi dev propose`
  say there's nothing to keep up with, and the daily fetch skips it.
- [x] `hi team new`'s code step still clones a folder, with its history.

### v2.5.57 — No machine called --help

- [x] `hi compute ssh --help` (and `status` and `wait`) took `--help` for
  a machine's name and asked every signed-in provider for it, failing on
  one whose token had expired; `stop`, `logs`, `hardware`, and the rest
  said "see `hi compute help`" and exited 2. `hi compute <command>
  --help` prints compute's usage now; a run's own arguments after `--`
  still go to the script.

### v2.5.56 — Help where it's asked for

- [x] `hi init --help` printed "see `hi compute help`" and exited 2: init
  shares compute's flag parsing, which turned --help into that message
  before init could print its own usage. It prints init's usage now, and
  `hi server <command> --help` points at `hi server help`.
- [x] `hi compute serve` on Hugging Face said the model "runs after 30m at
  21:05"; it says "It stops after 30m at 21:05", or that it has no time
  limit.
- [x] `hi help` describes hi team as it is since 2.4 (owners build in the
  dev box; the lead helps the people) and lists `hi net domain`;
  `hi server` names Shadeform beside RunPod for provider keys; and
  `hi update --restart` and `--no-restart` say they restart teams too.

### v2.5.55 — The Code tab stacked again on a tablet

- [x] On an iPad held upright the Code tab's chat, terminal, and VS Code
  were strips at the top of the screen (team quarry, 2026-10-26). v2.5.54
  gave the tab's layout a stronger selector than the narrow screens'
  rule, so at 900px and less it kept two columns but took the narrow
  rule's rows, and everything sat in the first, sized to its content. The
  narrow rule has the same selector now: the workspaces above, the chat,
  the terminal, or VS Code filling the rest.

### v2.5.54 — The dev page in VS Code's cards

- [x] The dev page and VS Code beside it didn't match (team quarry,
  2026-10-26): VS Code 1.141 draws its parts as rounded cards floating
  in a shell, 4px apart, in its 2026 Dark, and the page was flat, square,
  in Visual Studio Dark's colors. VS Code never had Visual Studio Dark
  either: hi moved a box's settings to it only when it started VS Code,
  not when VS Code was running already, so it kept following the
  browser's dark, which is 2026 Dark. The page is VS Code's shell now: its
  title bar's height and colors, the tabs quiet with the open one on a
  rounded wash, each tab a card, the workspaces a side bar card beside the
  chat's and the terminal's, and VS Code itself in the shell with the
  page's gap to it; buttons, inputs, lists, badges, menus, and dialogs
  rounded as VS Code's are, all in 2026 Dark. VS Code is set to 2026 Dark,
  and a box's settings move to it whenever the page starts VS Code or
  finds it running.

### v2.5.53 — An agent that starts isn't an error

- [x] Starting a task's agent on the dev page said "the workspace is
  there, but claude didn't start: ... no answer" while Claude ran fine in
  it (team quarry, 2026-10-26). herdr's pane run says nothing when it
  works, and hi read the silence as a failure. hi goes by how it ended,
  for a task, for words with no task, and for resuming a session.

### v2.5.52 — The dev page in VS Code's dark

- [x] The dev page followed the system's light or dark, beside a VS Code
  that did the same on its own (2026-10-26). The page is dark always, in
  VS Code's Dark (Visual Studio) colors: the title bar, editor-like tabs,
  its buttons, inputs, lists, menus and scroll bars, Consolas-style code,
  and the terminal in VS Code's terminal colors. VS Code in the Code tab
  opens in the same theme; a box's settings from before move to it.

### v2.5.51 — A Data tab

- [x] The dev page could copy the preview's data to production but not
  production's to the preview (team quarry, 2026-10-26). A Data tab has
  both folders side by side, what each holds and when it was written,
  and what the preview's is a copy of; **Copy production's data to the
  preview** and **Copy the preview's data to production**; **Take a
  snapshot now** and the newest snapshots, with the admin's command to
  restore one; and whether the preview starts empty at every release.
  The Release tab keeps releasing.

### v2.5.50 — Jobs in words on the dev page

- [x] The system panel's Jobs showed hi's own record of them, as JSON
  (team quarry, 2026-10-26). It lists production's and the preview's
  jobs, a line each: "prices in the preview: done, 5 of 5 tickers, 4 min
  ago", with where a blocked or failed one stopped and why, and its
  incident. hi dev status says the same.

### v2.5.49 — Production and the preview on https

- [x] With a domain (hi net domain), production is on
  https://<team>.<domain>/, the preview on <team>-preview, and the dev
  page on <team>-dev, each name in the zone at the team's NetBird address
  under the same wildcard certificate. The service passes each name on
  the peer's 443 to its address's relay; the http addresses on the
  NetBird name keep working.
- [x] The links move in place: production and the preview keep running,
  with their new links on the dev page. A dev page on https from v2.5.46,
  at <team>.<domain>, moves to <team>-dev, and the owners get its new
  link.
- [x] In NetBird's Access Control, the people's policy needs TCP 443 too.
  The three names share that port, so the dev page's check of an owner's
  link is its gate there.

### v2.5.48 — An email for Let's Encrypt

- [x] hi net domain asks, after the provider's token, for an email
  Let's Encrypt writes to if the certificate's renewal keeps failing
  (optional); hi net domain --email <address> sets it alone. With one,
  certmagic doesn't ask for it in the service's log.

### v2.5.47 — The certificate's challenge, asked of the zone

- [x] certmagic looked for the DNS-01 challenge on public resolvers, which
  remembered the name as missing for the zone's negative TTL (three hours
  at Gandi), so the certificate waited; it asks the zone's own servers
  again, as Let's Encrypt does (team quarry, 2026-10-26).

### v2.5.46 — The dev page on https

- [x] hi net domain <domain> --dns <provider>: a domain of the machine's
  own for its teams' dev pages, such as dev.example.com, so they're on
  https with a certificate browsers trust and nothing installed on any
  device. VS Code's previews and panels and the clipboard need it (team
  quarry, 2026-10-26).
- [x] Each team's page is https://<team>.<domain>/: hi keeps the name in
  the zone, at the team's NetBird address, and gets one wildcard
  certificate from Let's Encrypt over DNS-01 (certmagic), shared by the
  machine's teams and renewed before it ends; a failure waits 30 minutes
  before the next try. The team's peer passes 443 to the service, which
  answers TLS and passes the page on, and the owners get the new link.
- [x] The DNS provider is a setting, through libdns: Gandi, Cloudflare,
  Route 53, Hetzner, DigitalOcean, or RFC 2136. hi checks the token reads
  the zone before keeping it, on the machine only.
- [x] The production and preview addresses on https too (v2.5.49).

### v2.5.44 — VS Code connects behind NetBird's proxy

- [x] VS Code's connection to its server (a WebSocket) closed at once
  (1006) on quarry: NetBird's proxy doesn't pass on the host the browser
  asked for, and code-server refuses a WebSocket whose Origin isn't its
  Host. hi checks the Origin as it does for the terminal, and gives
  code-server the page's own host (team quarry, 2026-10-26).
- [x] Open VSX's files come from openvsx.eclipsecontent.org now; the box
  may reach it, so Claude Code's extension installs, and VS Code running
  from before gets the box's list and the extension when its view opens
  (v2.5.45).

### v2.5.43 — VS Code on the dev page

- [x] A third view beside Chat and Terminal, on a wide screen: VS Code
  in the browser (code-server 4.141.0, MIT) on the workspace's worktree,
  so an owner edits by hand or works beside the agent on the same files.
  hi fetches it into the dev box's home the first time and starts it on
  a socket in /box/run, so a running box gets it without a restart; the
  dev page passes code/ through to it for the owners only.
- [x] Set up for the box: its light or dark, no workspace trust, no
  telemetry or updates, no VS Code AI chat beside the agents, saving as
  you type; Claude Code's extension from Open VSX (allowed in the box),
  so /ide in the chat links Claude to it.
- [x] HTTPS for the dev page, so VS Code's previews and extensions'
  panels show (browsers keep service workers from plain http).

### v2.5.42 — A chat message survives a reload

- [x] A pasted picture's preview shows: the dev page's policy now lets
  it show the browser's own `blob:` pictures (team quarry, 2026-10-26).
- [x] The message being written in the chat, with its pictures, files
  and long pastes, is kept in the browser (IndexedDB) until it's sent,
  so a reload or a restart of the browser doesn't lose it.
- [x] Checked in real Chromium under the page's own policy.

### v2.5.41 — Pictures and long pastes in the chat

- [x] A chat message takes pictures and files: pasted (Ctrl+V), dropped
  on the box, or picked with the clip, each a small preview over the box
  with ✕ to leave it out. They go into the box's `~/hi/chat/<workspace>/`
  and the agent gets their paths with the message, up to 20 MB together
  (team quarry, 2026-10-26).
- [x] A long paste (more than 12 lines or 2,000 characters) folds into
  `[pasted text #1 +40 lines]` in the box and a preview with its first
  line, which opens when clicked; Send puts it back whole. One over 48 KB
  goes as a text file.
- [x] Checked in real Chromium: pasting a picture and a log, Enter to
  send, ✕, and a phone's width.

### v2.5.40 — A Security tab, and asks in the chat

- [x] An agent's ask for a site is a card in the chat of the work that
  asked, as a question is, not a banner over every tab; work without a
  workspace shows its asks in every chat. The Security tab counts them
  (team quarry, 2026-10-26).
- [x] A Security tab next to System: the sites the dev box reaches, the
  app's settings (its API keys) moved from System, and the team's keys
  and sign-ins, whose they are (never their values) and the command an
  admin runs to change each.

### v2.5.39 — Quiet refusals

- [x] The dev page's refusals leave out what the box is refused on
  purpose: Claude Code's and herdr's own calls and update checks
  (api.anthropic.com around the proxy's sign-in, downloads.claude.ai,
  herdr.dev), which quarry' box was refused over a thousand times.

### v2.5.38 — Agents ask for the sites they need

- [x] In the dev box, hi dev allow <site> --why "<what for>" asks the
  team's owners for a site its proxy refused, and --wait waits for their
  answer. The proxy's refusal says so, and the agents' instructions and
  hi-dev-ship tell them to ask at once rather than stop and hand an admin
  a command (team quarry, 2026-10-26: Yahoo for #9).
- [x] The ask shows at the top of the dev page on every tab, and in the
  channel with the page's link (#sites); Allow reaches the box at once
  and stays for its next start (the dev box's access.json). System lists
  what the owners allowed, to remove, and what the box was refused in
  the last week that nobody asked for, to allow or dismiss.
- [x] Never inside the network: a site under .local, .internal and the
  like, or one that resolves to a private, loopback, link-local,
  NetBird (100.64.0.0/10) or metadata address, can't be asked for or
  allowed, and the dev box's proxy connects only to the public addresses
  it checked, so an allowed name can't be pointed inside later. The
  proxy's check reaches a dev box that was running at its next start.

### v2.5.37 — A task's text as Markdown

- [x] The dev page shows a task's text and its comments as Markdown, as
  the chat does, not as plain text: bold, code, links, and lists in
  lists, keeping a paragraph's line breaks as GitHub does in issues
  (team quarry, 2026-10-26).
- [x] The Markdown view nests a list indented under an item, in Files'
  previews and the chat too.

### v2.5.36 — The question you're on

- [x] The card outlines the question Claude Code is on in blue: hi reads
  the screen with its colors, where Claude Code marks the tab with a
  background (team quarry, 2026-10-26).
- [x] Submit shows as done only once every question is (Claude Code
  always draws it with a tick), and a long question loses the bar
  Claude Code draws down its left.

### v2.5.35 — Questions you tap in the chat

- [x] A question in the agent's terminal shows in the chat as a card at
  the end of the conversation, not a terminal with keys: the question,
  its options to tap (with their details), boxes to tick, Claude Code's
  tabs, and "Type something" as a box for your own words. hi reads it
  from the screen (neither agent writes a question to its transcript
  before it's answered) and presses the keys: the option's number, the
  arrows to one without, or the number (Claude Code) or Tab (Codex)
  before typing. It answers only the question the page shows; one that
  changed meanwhile is refused.
- [x] Claude Code's AskUserQuestion, permission prompts and dialogs, and
  Codex's approvals and request_user_input (Plan mode), as each draws
  them (Claude Code 2.1.308, Codex 0.164); the screen and its keys stay
  under the card for anything it can't read. A question before a
  session starts, or from Codex, shows too.

### v2.5.34 — The lead finds hi team task start

- [x] hi team --help in the lead's box lists the lead's commands, from
  the hi-team skill's table: it showed the machine's, without task start,
  so the lead told an owner to start #9 themselves (team quarry,
  2026-10-26).
- [x] SOUL.md names the command: when an owner asks, hi team task start
  <number>.

### v2.5.33 — The lead starts a task

- [x] When an owner asks in Slack, the lead starts work on a task in the
  dev box (hi team task start), as Start does on the dev page, with
  Claude Code: only for an owner, and only while no other task is in
  progress there. It answers "Task #9 is now in progress! You should
  follow the progress in the developer view: <link>", and the task gets
  a comment saying who started it.
- [x] The dev page's link #task-9 opens that task's chat; the channel's
  link never has the address's key.
- [x] A SOUL.md from before gets the new sentence: the lead starts work
  only when an owner asks.

### v2.5.32 — Apps agents can use

- [x] hi serves `/llms.txt` and `/llms-full.txt` on every team's
  addresses, from the user docs, with the app's MCP server when
  `docs/user/mcp.md` tells of one. A relay route may be one path, such as
  `/llms.txt`. (docs/specs/approved/hi_team_agents.md)
- [x] The lead has the app's tools, from its `/mcp`: `app` on
  production, only those marked read-only, enforced by the team's service;
  `app_preview` on the preview, every one. `hi team mcp` in the lead's box
  bridges Hermes to the service, which keeps the app's session and tells
  the lead when the tools change.
- [x] The app and team-app templates serve `/mcp` (`src/mcp.ts`, the
  tools in `src/tools.ts`); the app template serves `/llms.txt`, the
  team-app has `docs/user/mcp.md`. AGENTS.md says a feature gets its tool
  in the same work, and the dev box has the `hi-dev-agents` skill.

### Later: agents beyond the team

- [ ] A sign-in for people's own agents on `/mcp` (OAuth), so they can use
  an app from Claude or ChatGPT.
- [ ] A browser for the lead (Playwright MCP) for apps without tools.

### v2.5.31 — The lead shapes tasks

- [x] The lead shapes what people ask for before it files: it looks
  first (the open tasks, the app's docs and code, and, for something
  outside the app, the web and that thing's own docs), talks it through
  (what it found, the choices, an idea of its own, the questions that
  decide it), and files when they agree. The task is written for the
  owner's coding agent: why, what it should do, how it is now and where,
  what the lead found with links, and what's open. A typo or a clear bug
  it still files at once.
- [x] The lead's web tools search and read pages, from the providers that
  need no key (Exa, then Parallel, Firecrawl, Keenable). Hermes had
  picked DuckDuckGo, which can't read a page.
- [x] A SOUL.md from 2.4 on gets the two new paragraphs where it still
  has hi's old ones; what the owners wrote stays.

### v2.5.30 — Where things run

- [x] The Release tab opens on Where things run instead of a diagram:
  the addresses, each with what it shows now. Production in green, with
  its version and, when dev has changes waiting, what they are and
  Release; the preview in blue, with what it shows (dev, or ▸ the branch
  being tried, and Show dev), and the work dev hasn't got under it, the
  one on the preview marked "← on the preview now", the rest with Try on
  preview; this page. Make public and Internal only stay on each. The
  diagram (v2.5.29) said less, in more room.

### v2.5.29 — The Release tab

- [x] The dev page has a Release tab, between Files and System: at the
  top, a diagram of how work moves and what's in use now, drawn by pele
  (the copy hi serves for the docs' charts) from the page's state: the
  pieces of work dev hasn't got, dev with its changes not released,
  production with its version and address, and the preview with its
  address and what it shows, by a dashed line; what's in use is
  outlined, production in green, the release waiting a bold arrow. Under
  it Release, Set dev back, the releases with Roll back, production's
  data, and the addresses, which were in System; Show dev under the
  diagram while a branch is tried. The tab counts the changes waiting.
- [x] On a phone the five tabs fit.

### v2.5.28 — Start something new

- [x] Open beside a workspace works with herdr 0.9's names (wB, wC):
  hi took only w1, w2, ..., and said "wC isn't a workspace".
- [x] The Code tab lists pieces of work only, a task's worktree or an
  agent at work; herdr's home (~) and the project's clone stay in the
  terminal, and the chat no longer shows the clone's old session as if
  it were current.
- [x] With no work, or + New, the chat has Start something new: what to
  do, Claude Code or Codex; hi makes an adhoc-<words> branch and
  worktree from dev, never a task's number so Done closes none, and
  starts the agent with the words.

### v2.5.27 — Done, beside the work too

- [x] A workspace's Finish says Done ✓, as its task's does; the dev
  box's agents and hi dev ship say so too.

### v2.5.26 — Done

- [x] Done ✓ beside each open task closes it when it's done: with its
  work all in dev it finishes the workspace too; with work dev hasn't
  got, that stays in the workspace and only the task closes. Reopen in a
  closed task's ⋯, and Close or Reopen in an opened task's. Both say who,
  in the task; the lead can't reopen (hi team task stays file and
  comment).
- [x] The task filter's Open, Closed, and All keep their highlight when
  the Code tab's Chat and Terminal are switched; switching them cleared
  it, and listed the closed tasks too.

### Later — The way work moves, designed

- [ ] Preview, dev, and production on the dev page need a design of
  their own: what each is, what's on which address now, and where a
  piece of work is on its way (tried, in dev, released), drawn rather
  than told in buttons and lines of text. Try on preview, Show dev, Ship
  to Dev, and Release work (v2.5.25), but the flow is still confusing to
  owners; the layout and the way it's shown should make it obvious
  without the guide.

### v2.5.25 — Show dev

- [x] The dev page says what the preview shows, at the top of the Code
  tab and in System: dev, dev as it was before what was shipped since,
  or a branch being tried. Show dev puts dev as it is back on it; before,
  only shipping something did, so a branch tried there stayed. In the
  box, hi dev preview --dev, and hi dev status says the same.
- [x] A workspace's Preview button says Try on preview: it borrows the
  preview for that branch, and changes nothing in dev.

### v2.5.24 — Chat first

- [x] The Code tab opens on Chat, on a computer too; Terminal is a click
  away, and the page remembers the choice.

### v2.5.23 — The working line holds still

- [x] The chat's working line no longer wobbles: its glyphs come from
  fonts of different heights (on Windows, · from the monospace font and
  the stars from a symbol font), so the line changed height as it
  turned. The line and the glyph's box have a fixed height now.

### v2.5.22 — The chat's working line

- [x] While Claude works, the chat's last line says so as its terminal
  does, "✻ Crafting… (7s · ↓ 556 tokens · thought for 4s)", read from
  the terminal, with the glyph turning; "Working… (12s)" when hi can't
  read it. It was a status in the chat's bar.

### v2.5.21 — Chat

- [x] The Code tab's Chat shows a workspace's Claude Code session as a
  conversation, as Claude Code's web view does, beside the same session
  in the terminal. hi reads the transcript Claude Code writes as it goes
  (~/.claude/projects/<worktree>/<session>.jsonl; herdr's hook says
  which is the running agent's) and sends the page what's new; answers
  render as Markdown, tool calls fold into a line each run. A message
  goes to the agent through herdr (agent prompt), Stop presses Esc, and
  a question in the terminal, which isn't in the transcript until it's
  answered, shows with keys to answer it. A stopped agent's session
  resumes with claude --resume. For the owners; the default on a phone.

### Later — More in the chat

- [ ] Codex's sessions (~/.codex/sessions), in the same view.
- [ ] Pictures in a message, pasted or picked, as in the task view.
- [ ] Questions (AskUserQuestion) answered with their own buttons, not
  the terminal's keys.
- [ ] Following a session by herdr's event stream rather than asking
  every second and a half.

### v2.5.20 — Code

- [x] The dev page's Terminal tab is called Code.

### v2.5.19 — Files

- [x] The dev page's File view tab is called Files, a word like the
  other tabs'.

### v2.5.18 — File view

- [x] The dev page's File view tab: the project in the dev box (dev's
  clone, or a piece of work's worktree), its folders to browse, and its
  files to preview (Markdown, with the project's pictures and links;
  text and code; pictures), download, and upload, picked or dropped,
  replacing a file only when asked. For the owners; every path goes
  through an os.Root at the worktree, so nothing outside it, through ..
  or a link, and an HTML file shows as text in a sandbox, never as a
  page.

### Later — More in the Files tab

- [ ] More formats, as teams need them: most are in VS Code's view now
  (v2.6.2). Left: SQLite tables, xlsx, and docx in VS Code, once an
  extension for them is free of the problems v2.6.2 lists; SVG drawn
  safely in the tab.
- [ ] A file's changes against dev, as a diff, beside its preview.
- [ ] The app's data folder (production's and the preview's), read-only.
- [x] Decided 2026-10-27: viewers go in VS Code (v2.6.2), and VS Code
  does new folder, rename, delete, and editing; the tab stays for
  browsing, uploads and downloads, and the diff. Looked at and not
  taken: copyparty, FileBrowser Quantum (HTML inline under only
  `script-src 'none'`, 512 MB per team), filebrowser/filebrowser
  (archived), dufs and miniserve (no previews), SFTPGo and Filestash
  (AGPL, their own accounts), Cloudreve, Nextcloud, and Seafile.

### v2.5.17 — The task's ⋯ menu shows whole

- [x] A task row's ⋯ menu shows whole; the table's box cut it off below
  its last row.

### v2.5.16 — Ship to Dev

- [x] The dev page's Ship button says Ship to Dev, beside Release, which
  puts dev in production.

### v2.5.15 — Ship from the dev page

- [x] Beside a workspace with commits dev hasn't got, Preview and Ship
  do what hi dev preview and hi dev ship do in the box, by an owner's
  click: Ship asks for the change's title (the last commit's by
  default), merges into dev, and puts dev on the preview. Work with
  nothing committed says to commit first.
- [x] Enter in the page's question dialog answers OK; before, it was
  Cancel, the dialog's first button.

### v2.5.14 — Tasks, everywhere

- [x] Tickets are tasks everywhere people see them: hi team task and hi
  dev task (ticket still works, for the lead's memory and scripts), the
  lead's SOUL.md and skill, the dev box's skills (hi-dev-tasks replaces
  hi-dev-tickets, which hi removes), its messages, and the guide. A
  task's files land in ~/hi/tasks/<n>/ in the box. Task, not issue (it
  sounds like something's broken) or GitLab's work item: one word for
  GitHub, GitLab, and hi's own. A shipped change (t-31) is a change.
  The team folder keeps tickets/, beside the changes' tasks/.

### v2.5.13 — Tabs, and tasks as a table

- [x] The dev page has three tabs along the top, Tasks, Terminal, and
  System, on every screen; it remembers the last one. The page calls
  tickets tasks.
- [x] Tasks is a table, after Orca's: each one's number, title, who filed
  it and its labels, its status (open, in progress with its agent's
  state, or closed), and when it last changed; Open, Closed, and All, a
  search, + for a new one. Start → asks which agent (it remembers the
  last) and starts it; a task with a workspace has Open →. On a phone
  the rows fold into cards.
- [x] Terminal has the workspaces beside herdr; System has two columns.

### v2.5.12 — Edit a ticket

- [x] On the dev page, the ticket's own ⋯ and each comment's have Edit,
  as on GitHub: the title and text, or a comment's text, in place, with
  Save and Cancel; "edited" shows after. An edit keeps the line that says
  what a comment attached, so deleting it still takes its files.
- [x] hi team ticket edit <n> [--title ...] <text|file|-> (and hi dev
  ticket edit): the ticket's whole new text. The lead uses it when
  someone changes what they asked for ("make it the top 20"), so the
  ticket says what they want now, and comments on what changed; before,
  the change was only a comment under the old text.

### v2.5.11 — The ticket list follows Finish

- [x] The dev page's ticket list shows a change at once: Finish's closed
  ticket goes without a refresh. The page fetches the tickets after each
  owner's action, and hi's half-minute cache of the list empties on any
  change to a ticket, whoever made it (the lead, the dev box, the page).

### v2.5.10 — Claude Code reads ~/hi without asking

- [x] In the dev box, ~/hi (a ticket's files, reviews, brainstorms) is
  one of Claude Code's working directories, in its settings beside the
  owners' own, so a mode that asks, such as auto, doesn't ask to read a
  ticket's pictures. A running box gets it when the team's service
  starts.

### v2.5.9 — Delete a ticket's comment or file

- [x] On the dev page, each comment of an opened ticket has a ⋯ menu, as
  on GitHub: copy its text, quote it in a note, or delete it, with the
  files it attached; each file has a ✕. For the owners only, logged with
  their name; on GitHub through its API, on hi's own tickets in their
  file. GitLab's comments aren't shown yet, so they're deleted there.

### v2.5.8 — What was shared goes with the ticket

- [x] When the lead files a ticket or comments on one from a thread in
  the channel, hi attaches every file people shared in that thread, once
  each, from the conversation Hermes tells the lead's commands; the lead
  no longer has to be asked. Before, a picture with the request stayed in
  Slack unless someone asked for it.
- [x] A ticket's body or comment given as a file's path is the file's
  text; before, ticket #3 on quarry had the path as its description.
- [x] A picture pasted into a ticket or a note on the dev page leaves a
  reference where it was pasted, `[image: image-3fa9c1.png]`, the name
  it has in the ticket's files.

### v2.5.7 — A ticket, opened on the dev page

- [x] A ticket's title on the dev page opens it: its whole text with its
  links, its files (pictures shown; PDFs and text open; anything else
  downloads, never runs on the dev page's address), its comments, and a
  note to add, with files, which says which owner wrote it.

### v2.5.6 — New ticket on the dev page

- [x] **+ New ticket** on the dev page files a ticket where the team's
  tickets are (GitHub, GitLab, or hi's own), with a title, what it's
  about, and files, picked or pasted as pictures; it says which owner
  filed it, and shows in the list at once. Enter in the title files it.

### v2.5.5 — Release closes its dialog

- [x] Release on the dev page closes its dialog at once and says how it
  goes ("Releasing v3…", a snapshot first, then the new box), instead of
  a greyed-out button for the minute or two a release takes. When it
  fails, the dialog comes back with the error and the notes as they were.

### v2.5.4 — The helper on the internal addresses

- [x] The helper shows on a team's internal addresses. hi added it to
  page loads it knew by Sec-Fetch-Dest, which browsers send only to
  HTTPS addresses; on plain HTTP a page load is what asks for HTML
  first, without htmx's or Turbo's header.

### v2.5.3 — The team's peer outlives a restart

- [x] The team's NetBird peer kept running when the team's service
  restarted, but NetBird inside it stopped at its next log line, since
  its output went to the service that was gone: every internal address
  was down after `hi update --restart`. NetBird logs inside the
  container now, the container ends when NetBird does, and the service
  starts a stopped peer again within a minute.

### v2.5.2 — Internal addresses follow the peer

- [x] An internal address whose name or port moved (a peer renamed in
  NetBird, or one from v2.5.0's three peers) gets its new link, and the
  owners' and people's links follow; before, it kept the old one, since
  an internal address doesn't expire.

### v2.5.1 — One internal name, a port per address

- [x] The team's addresses are on its one NetBird peer, by port:
  production on 80, the preview on 8080, the dev page on 8888, and SSH
  on 22, so a one-off key joins it, and people see one name. v2.5.0's
  three peers, names, and reusable key were messy. A new team's peer is
  named after the team; one that joined before keeps its name, and hi
  follows what NetBird calls it.

### v2.5.0 — Internal by default

- [x] A team's addresses are on its NetBird network by default, at
  `<team>`, `<team>-preview`, and `<team>-dev`, three peers of its own
  passing port 80 to a relay in the team's service, the same as `hi net
  expose`'s without NetBird's public proxy or a link key
  (docs/specs/approved/hi_team_internal.md). An owner makes an address
  public on the dev page, under Addresses. New teams start internal;
  teams from before stay public until an owner says otherwise. One
  reusable setup key joins the three peers. On 2026-10-25 NetBird's
  maintenance took every public address down for an hour.
- [x] Certificates for the internal names (Let's Encrypt, DNS-01): the
  dev page in v2.5.46, production and the preview in v2.5.49.

### v2.4.5 — The dev page asks in its own dialog

- [x] The dev page asked before Finish, Roll back, Restart, Close, and
  Remove with the browser's confirm() and prompt(), which Slack's in-app
  browser answers no without showing: on quarry, Finish did nothing. It
  asks in a dialog of its own now, and every owner's action is in the
  service's log with how it went.

### v2.4.4 — Finish a piece of work

- [x] Each workspace on the dev page says how much of its work dev has
  (everything, or commits not shipped and changes not committed), and
  **Finish** ends a shipped one: its worktree, herdr workspace with the
  agent, and branch go, and the ticket its number names closes ("Shipped
  into dev as t-31"). Work dev hasn't is kept unless **Discard** drops it.
  `hi team dev <team> finish <branch> [--discard]` on the machine; `hi dev
  ship` and the ship skill point at it.

### v2.4.3 — Keys for the dev page's terminal on a phone

- [x] On a touch screen, a bar under the dev page's terminal has the keys
  a phone's keyboard may lack: Paste, Esc, Tab, Shift+Tab, Enter, the
  arrows (in the app's cursor mode), Space, Backspace, Del, Ctrl+C,
  Ctrl+D, and a Ctrl for the next key. Its buttons keep the focus in the
  terminal, and the page keeps above the on-screen keyboard.

### v2.4.2 — The helper is help and feedback

- [x] The helper's Change tab, the chat with the lead in the app, is gone:
  Feedback reaches the lead, which files it as a ticket, and people talk
  with the lead in Slack. `/hi-<team>` sends only owners a link, to the dev
  page. A new app's welcome page points at Feedback.

### v2.4.1 — What people share goes with the ticket

- [x] Tickets take attachments: --attach <file> with add or comment, for
  the lead, the dev box, and hi team ticket on the machine. hi keeps them
  in tickets/files/<n>/ for every forge (gh can't attach a file to an
  issue), the ticket says what's attached, hi dev ticket files <n> puts
  them in ~/hi/tickets/<n>/ in the box, and Start on the dev page tells
  the agent to fetch them. On quarry, the lead could only put a path of
  its own box into the ticket, which the dev box couldn't read.

### v2.3.1 — The dev page's terminal opens through NetBird's proxy

- [x] The dev page's terminal closed at once on quarry: the WebSocket's
  Origin, the page's address, was checked against the host the request
  came for, which NetBird's proxy doesn't pass on as the browser gave it.
  The page's address as hi exposed it (run/dev-page.json) is now the one
  checked, and a refused terminal says why in the service's log.

### v2.1.3 — The preview is called what dev is after a release

- [x] `hi dev status` named the preview by what dev was when it went on:
  after quarry's v1, the preview of that same dev still said "1 change,
  nothing released yet". A preview of dev's current commit now gets dev's
  label as it is now, "the same as production v1".

### v2.1.2 — Tests that start a server pass in a box, and the dev box can commit

- [x] Boxes reach their own addresses without the proxy: `::1` and
  `0.0.0.0` join `localhost` and `127.0.0.1` in `NO_PROXY`. A server that
  listens on every address (Bun.serve, for one) gives `0.0.0.0` as its URL,
  so a test that fetched it went to the proxy, which refused it, and a new
  team's own `make check` failed in the dev box.
- [x] The dev box has a git author: commits there failed with "Author
  identity unknown". It is the box, `<team> dev box`, at
  `dev-box@<team>.invalid`, since the owners share it; an owner's own
  `[user]` in the box's home stays.
- [x] A running box gets both at its next restart
  (`hi team dev <team> restart` for the dev box).

### v2.1.1 — The dev box keeps up with hi update

- [x] The dev box runs hi from the team's folder (`dev/bin`), which the
  team's service brings up to date as it starts: before, the box mounted
  hi's file, and kept the old hi after `hi update` replaced it, while the
  service leaves the box running. A box from v2.1.0 needs one
  `hi team dev <team> restart`.
- [x] `hi dev status` leaves out "Jobs:" when the app reports none.

### v1.14.9 — A team restarts safely, and the lead's notices stay out of Slack

- [x] `hi update --restart` waits while a team releases or holds its boxes
  paused for a snapshot, says so, and restarts it once that is done (up to
  30 minutes; after that it prints the command). A restart in the middle
  left production paused, with nothing left to unpause it.
- [x] A team's service unpauses, as it starts, what the one before left
  paused: production, the preview, and what its pause note names. That
  covers a crash or a reboot in the pause.
- [x] No "💾 Self-improvement review: Skill … patched" in the threads after
  the lead's turns: hi sets Hermes' `display.memory_notifications: off`.
  The review still runs and saves what the lead learned. Found on team
  orbit (2026-10-24), twice in a morning.

### v2.0.0 — hi team dev: owners build in a dev box, not through Slack

Spec: [hi_team_dev.md](specs/approved/hi_team_dev.md). Co-developing
through the Slack lead didn't work out on orbit: nothing showed what
was going on, and the lead in front cost more than it hid. 2.0 is the
takeoff from 1.x: each team gets a dev box with herdr, where its owners
work with their agents, and the lead becomes a systems helper. The Slack
development loop is frozen and goes in release 4.

- [x] Spike: NetBird's userspace mode in a rootless box takes inbound
  SSH, and a box keeps its peer across restarts without a key; herdr runs
  headless with a worktree per ticket. NetBird's own SSH waits for SSH on
  the peer in the dashboard (spec, Spike).
- [x] Release 1 (v2.0.0): the dev box over SSH, with herdr, Claude Code
  and Codex, NetBird and sshd; `hi team owners` with SSH keys; `hi team
  dev <team> new-host-key`; `hi team new` asks for the NetBird key.
- [x] Release 2 (v2.1.0): `hi dev preview` and `hi dev ship` from the
  box; `hi team ticket` and `hi dev ticket` over gh, glab or local; the
  broker's `dev` socket; the first skills in `skills/hi/team`; the setup
  key goes once joined; a lock on starting the box.
- [x] Release 2b (v2.2.0): `hi dev review`, a second opinion from the
  other agent in a worktree of its own; `hi dev deep-review` (up to $50)
  and `hi dev brainstorm` through the broker, with their results in the
  box's home; `hi dev data [fork]`; `hi dev settings`, the preview's own
  values; `hi dev incidents`; five more skills.
- [x] Release 3 (v2.3.0): the dev page: `/hi-<team> dev` and owners'
  DMs link it; herdr in the browser (xterm.js over a WebSocket); tickets
  with Start, workspaces with their agents' state; the system panel,
  settings, and Release with the owner's click as the yes; `hi team dev
  <team> start <ticket>`.
- [x] Release 4 (v2.4.0): the lead's new role (help, tickets, status,
  the system's watch); every Slack yes on the dev page (rollback, dev
  back to production, data to production, the helper, incidents, jobs'
  rules); `hi dev upstream` and `hi dev propose`; the Slack development
  loop removed, about 9,400 lines.

## Planned

### Next — a box's escape reaches only its own team

Found when colleagues pentested a team (v2.6.10 to v2.6.12 above). Boxes
run as the account that runs hi: Podman's `--userns=keep-id` maps the
box's `box` to that account on purpose, so the files an agent writes are
the account's. An escape from any box is therefore that account, which
holds every team's folder (the real Slack tokens, the OpenRouter keys),
hi's config and device key, the machine's sign-ins, every team's
containers, and whatever else the admin keeps there. v2.6.12 took away
the root-equivalent groups on gpu1; this takes away the rest.

How: each team runs as a Linux account of its own, `hi-demo`, which owns
the team's folder. keep-id stays and maps the box to that account, so
nothing about mounts changes, and an escape holds what that team holds
and nothing else. Draft spec: [hi_team_accounts.md](specs/ideas/hi_team_accounts.md).

Giving each box a user of its own while the service stays hi's was tried
first, on gpu1 on 2026-10-28, and doesn't work rootless:
`--userns=auto` finds no free sub-ids, rootless Podman may not make
`:idmap` mounts, and without them hi can't read a file the box made 0600
(ACLs don't help: a 0600 file's mask shuts them out).

- [ ] Approve the spec; settle its questions first: whether a read-only
  shared image store saves building the base image per account, and how
  the machine's sign-ins reach a team without being in its folder.
- [ ] `hi team new` makes the account (no login, no groups, sub-ids of
  its own, lingering), its folder in `/var/lib/hi/teams/<team>`, and runs
  the team's service and boxes as it. hi moves to `/usr/local/bin`.
- [ ] The team's keys in its folder; the machine's sign-ins served from
  the admin's side; a device key per team for the hi server.
- [ ] The admin's `hi team` commands run as the team's account
  (`sudo -u`), from the admin's account, as now.
- [ ] `hi team up` moves an existing team, behind a snapshot, keeping its
  addresses, links, data, memory, and dev box; teams not moved keep
  working, with a warning.
- [ ] A test that plays the escape: as the team's account on the host, no
  other team's folder, broker, or containers, no admin home, no hi
  config, and the hi server refuses the team's key for another team.

### Next — sign-in with a real provider, before v2.7.0 is tagged

v2.7.0 (`hi team sso`) is on main but untagged: its tests use a fake
provider that signs real tokens and checks PKCE, and no real provider
has signed anyone in yet. Spec: [hi_team_sso.md](specs/approved/hi_team_sso.md).

- [ ] Keycloak on gpu2: a realm, a confidential client with both return
  addresses, and one team on a name that stays (`hi team domain <team>
  on`). `hi team sso provider add`, then `on --anyone`: a page load with
  no key goes to Keycloak and back to the page asked for, signed in; a
  fetch with no key still gets 401; the app sees neither cookie.
- [ ] The rules against real tokens: `--group` with a Group Membership
  mapper, and `--domain` with a verified and an unverified email; each
  refusal shows in `hi team sso <team>`.
- [ ] New rules and `off` sign people out with the next request, and the
  link's key works throughout, also for someone Keycloak doesn't know.
- [ ] A team that ran a relay from before v2.7.0 gets a new one on the
  same name with the same key (the relay's stamp), and the links people
  have keep working.
- [ ] The preview signs in on its own, at its own return address.
- [ ] If there's time, one hosted provider as well (Google Workspace or
  Entra ID), for its own checks: Google's `hd`, Entra's tenant issuer.
- [ ] Then tag v2.7.0, with what was fixed.

### Next — people outside Slack, by email, with sign-in

From [hi_team_dev_access.md](specs/approved/hi_team_dev_access.md)'s
"Later, with sign-in", once the app's address has `hi team sso`.

- [ ] The relay hands the verified email and name to hi's routes as
  `X-Hi-SSO`, stripped from incoming requests; the app never sees it.
- [ ] Owners add people by email as contributors (Report in the helper)
  or viewers (let in past the sign-in's rules), on People or with
  `hi team access <team> anna@example.com contributor|viewer`; anyone
  else signed in is a viewer.
- [ ] Adding a contributor by email while the helper is off where they
  can go warns: the helper is their only way to report.

### Next — the rest of a name for each address

- [ ] `--cert <fullchain.pem> --key <key.pem>`: TLS ended by hi on the
  peer's 443 with a certificate the IT team gives, each name to its
  address; an incident 30 days before it ends.
- [ ] `hi team addresses <team>` checks each name resolves, to the team's
  NetBird address or to a proxy.
- [ ] Stage in the guides' older pages and in the commands' own messages.

### Next — a conversation hears what other threads finished

Found on team helpdesk (v1.0.5 above). Each thread, and a DM's top level,
is a Hermes session of its own, and Hindsight's recall by meaning didn't
bring back a merge from another thread. The lead now looks at dev before
a plan; this makes the fact reach it without looking.

- [ ] When a session's next turn comes after work merged, released, or
  rolled back since that session's last turn, hi adds a short line to
  the turn, from its own record: "Since this conversation last spoke:
  t-87 Ask / Plan switcher merged into dev (approved in another thread
  of this DM); preview dev, v3 + 2 changes." Only what changed the
  project, newest first, a few lines at most, with where each was agreed.
- [ ] The line goes in with the turn's message, from hi's plugin in the
  lead's Hermes (which hook carries it is to find out), not in the
  system prompt, so its prompt cache stays as it is.
- [ ] Goes with carrying a turn's findings over
  ([hi_team_lead_steps.md](specs/approved/hi_team_lead_steps.md)): both
  are what the next turn needs that its conversation doesn't hold.

### Guide — a diagram for every tool

Convention: [guide-diagrams.md](guide-diagrams.md).

- [x] A structure diagram on every tool's main page: box, agent, team,
  how `hi compute` works, managed compute and the `hi server` for admins,
  `hi net expose`, `hi init`, `hi q`, `hi data`, and autoresearch.
  `guideDiagramPages` in `guide_diagrams_test.go` keeps them there.

### Later — brainstorm rounds

Spec: [hi_agent_brainstorm.md, Later](specs/approved/hi_agent_brainstorm.md#later).
Once a single round has been tried on real questions:

- [ ] Rounds: agents see each other's ideas and answer them, building on
  or challenging them, as `best-of` rounds do. Decide how many rounds,
  and whether the lead or the agents say when it's done.

### Next — the helper

- [ ] A daily digest in the channel of what was discussed in DMs.
- [ ] Pictures of what is pointed at under a `style-src 'self'` policy,
  which blocks the inline styles modern-screenshot needs.

### Later — `hi team`

Approved spec: [hi_team.md](specs/approved/hi_team.md).

- [ ] A team without NetBird: its peer gets an address on the machine's
  LAN instead, by DHCP or a fixed IP the setup asks for, and its addresses
  are named in the LAN's DNS (or the IT team's, as `hi team addresses
  <host>` does now). Owners reach the dev box over SSH on the LAN, and
  hi's own access checks stand in for NetBird's Access Control.
- [ ] https on a host name the IT team keeps (`hi team addresses <host>`),
  which is plain http now: a certificate the IT team gives hi, or one hi
  gets from an ACME server they run, since hi has no token for their DNS.

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
- [x] Adopt a project someone else wrote, such as one found on GitHub:
  `hi team adopt <name> <source>`, a copy that never pushes to the source,
  a survey that makes it run and maps it, keeping up with upstream, and
  contributing back (v0.90.0 to v0.92.0). Spec:
  [hi_team_adopt.md](specs/approved/hi_team_adopt.md).
- [ ] Explore a playground team for trying projects out: one team whose
  adopted project can be swapped (`hi team adopt playground <url>
  --replace`), keeping the old one as a branch, so trying something
  doesn't cost a new Slack app and channel. Decide whether it's worth
  bending "a new project is a new team". See
  [hi_team_adopt.md](specs/approved/hi_team_adopt.md#decisions).
- [ ] Adopt from the chat, into a team that already exists: while the
  project is still the blank app `hi team new` wrote, someone in Slack or
  the helper's chat gives the lead a repository's link ("adopt <link>",
  "let's try this <link>"), and the lead runs the adopt workflow on the
  team's own project, after an owner's yes: copy, survey, run, map, as
  `hi team adopt` does today. Once the project has work in it, the lead
  says so and offers a new team instead.
- [ ] Adopt a service or website, not only code: the people give the
  lead a link to something they like (example.com), and the lead walks
  them through it, step by step in the chat, to a foundational spec for
  itself and the owners' agents:
  - what about the service they like and want;
  - what the spec can and should leave out;
  - who the users are, and their kinds (people using it, admins, ...);
  - a color theme and design concept, in words for now (web design
    themes come later).
  The lead can research the market around it, comparing similar
  services, or, when the people ask, keep to that one service only. The
  result is the spec and the user docs' story (docs/user/index.md), with
  the people's choices, before the owners' agents build anything.

### Later — `hi watch`

Approved spec: [hi_watch.md](specs/approved/hi_watch.md). The hi server
watches each team from outside its machine: what the broker can't see
from inside (a machine that died, its own compromise, advisories for code
already merged). Goes with the security advisor under `hi team` above,
which would read the incidents this raises.

- [x] Release 1, heartbeat (v1.4.0): `__serve` sends a signed heartbeat every
  minute with each part's state, disk, OOM kills, snapshot age, spend
  through the proxy and the key's own usage, and the network log from the
  offset the server has; the server opens an incident and pushes owners
  when one stops (not after a restart it was told of), pings an outside
  dead-man's switch, and runs the health rules.
- [x] Release 2, bills and advisories (v1.5.0): a CycloneDX bill of materials per
  release (`hi team bom`), a nightly match against OSV with KEV and EPSS
  for ranking, advisory incidents, `hi team advisory … ignore`, and the
  same match in the dependency review.
- [x] Release 3, network rules and stopping (v1.6.0): the proxy's `closed` lines
  with sizes, the network rules, freeze (which takes the app boxes off
  the network and their addresses down) and pause from the server, a
  logging resolver for app boxes, and the weekly comparison of what
  production runs with its bill.
- [x] Release 4, revoke and cut off (v1.7.0): once the server makes the teams'
  OpenRouter keys and holds a NetBird API token.

What is built, in one place (details: the v1.4.0–v1.7.0 entries above,
and the spec's As-built sections):

- **Heartbeat** (team → server, every minute, signed with the device
  key): each part's state, disk, OOM kills, snapshot age, spend through
  hi and on the key, open incidents, the network log and the other logs
  from where the server's copy ends. The first device to send owns the
  team (`hi server watch forget <team>` to move it).
- **Server incidents** `w-N` in `watch/state.json`: a team gone quiet (3
  min, 10 after an announced restart), parts down, restarts, OOM, disk,
  snapshots, spend spikes, spend outside hi. Posted in the watch channel
  (`--channel`), the audit log, pushed to owners via ntfy for the urgent
  ones; those the lead can act on go back in the heartbeat's reply as
  `tell` and open a local `watch` incident.
- **Bills and advisories**: a CycloneDX bill per release (`hi team bom`),
  matched nightly against OSV, ranked with CISA KEV and FIRST EPSS into
  kev / incident / report tiers; images' advisories counted, not listed;
  `hi team advisories`, `hi team advisory <id> ignore` (kept as VEX until
  the advisory changes); the dependency review warns, and a KEV match
  needs an owner's yes to merge.
- **Network**: the proxy's `closed` lines with sizes, a logging resolver
  for app boxes (Podman, unit `hi-team-<name>-resolver`), network rules
  (denies, raw IPs, `watch-hosts.txt`, uploads, new allowlist hosts,
  bursts), and a weekly check of what production runs against its bill.
- **Stopping a team**: freeze, unfreeze, pause, unpause from `hi server
  watch` or `/hi team …`, delivered in the reply as `do` and confirmed by
  `stop.done`; auto-freeze on a burst to a watch host or a KEV in the
  app's own packages (`--auto-freeze off` to stop it).
- **Revoke and cut off**: with `--openrouter`, the server makes teams'
  keys (`hi team key <team> openrouter --from-server`), revokes and
  rekeys them; with `--netbird`, `cutoff` and `reconnect` the team's peer
  (peer approval, else its groups).
- **Running**: vmserver runs v1.7.0 since 2026-10-22 (backup in
  `~/hi-server-backup-20261022-202124`); orbit runs v1.7.1 and sends
  heartbeats since 2026-10-23, with its production lookups logged.

Switching it on, on vmserver (an admin's, with credentials only they
have; orbit sends heartbeats since v1.7.1 on 2026-10-23):

- [ ] The dead-man's switch: a healthchecks.io check (1 minute, a few
  minutes' grace) and `hi server watch --deadman <url>`. Until then
  nobody hears if the server stops.
- [ ] Pushes: an ntfy server that needs a login to read, not on
  vmserver, then `hi server watch --ntfy <url> --ntfy-token <token>`,
  and each team's owners subscribed (`hi server watch subscribe <team>`).
- [ ] Revoke and rekey: an OpenRouter provisioning key (`hi server watch
  --openrouter`), then each team moved to a key of the server's making
  (`hi team key <team> openrouter --from-server`).
- [ ] Cut off: a NetBird API token (`hi server watch --netbird`), and
  peer approval turned on or the Default policy narrowed: as it is, All
  reaches All and a cut off doesn't hold.
- [ ] After a few weeks of orbit's heartbeats: look at what the network
  rules, the spend rule, and auto-freeze raised, and tune the thresholds
  and `watch-hosts.txt` on what was noise.

What release 1–4 left out:

- [ ] The server reads each team key's usage at OpenRouter itself, so the
  "spent outside hi" rule holds on a compromised machine too; today it
  compares what the team reports.
- [ ] The logging resolver for Docker app boxes; only Podman's pasta has
  it, so a Docker team's production lookups go unlogged.
- [ ] Skills in the bill with their source and commit; hi keeps neither
  for them yet.
- [ ] A preview's bill matched too (made on `hi team bom <team> preview`,
  not matched), so a release's advisories show before it goes out.

An optional add-on, if advisories without a severity turn out to matter:

- [ ] NVD's scores for advisories OSV has none for. For each matched
  advisory with a CVE ID and no severity from GitHub, Ubuntu, or Debian,
  the server asks NVD's CVE API (`services.nvd.nist.gov/rest/json/cves/2.0`,
  with an API key: about 5 requests per 30 seconds without one, 50 with)
  for its CVSS score, including CISA's added scores, cached like the OSV
  records. A score of 9.0 or more counts as critical for the incident
  tier. Off unless the server has a key (`hi server watch --nvd`).

  Why only as an add-on: NVD names products by CPE
  (`cpe:2.3:a:vendor:product:version`), not by ecosystem and package, so
  matching an npm package or a Debian source package against it is
  guesswork, and it doesn't know a distro's backported fixes, so every
  patched Ubuntu package would match. NVD has also fallen behind adding
  CPEs and scores since 2024, and its API is slow. OSV stays what
  matches; NVD would only fill in a score, keyed by CVE ID as KEV and
  EPSS already are, so a CVE whose only severity is NVD's can still
  reach the incident tier.

The spec's open questions, to decide once it has run a while:

- [ ] Teams on a machine with no hi server: a watcher in a separate user
  on the same machine (advisories and network rules, not a dead machine),
  or a team needs a server.
- [ ] Auto-freeze: keep it on, or have every freeze wait for an admin
  (`--auto-freeze off`), from what owners say after the first freezes.
- [ ] One watch channel per server, or per group of teams.
- [ ] OWASP Dependency-Track beside the server instead of OSV's API, once
  there are many teams.
- [ ] Watching what runs no team: `hi compute` instances and workstations
  (left out of the spec).

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
  Spec: [hi_box_desktop.md](specs/ideas/hi_box_desktop.md) (draft).

- [ ] Turn the ideas worth building into draft specs in `specs/ideas/`.
  Started: [hi_compute_idle.md](specs/ideas/hi_compute_idle.md) has the
  idle GPU flag and why suspend and resume doesn't fit the current
  providers; [hi_compute_ssh_run.md](specs/ideas/hi_compute_ssh_run.md)
  covers runs on RunPod, Shadeform, and managed providers. The services
  were compared on 2026-10-01 in [hi_box.md](specs/approved/hi_box.md#services).

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
- [ ] `--desktop`: a Linux desktop in the box that the agent drives with
  `hi-desktop` and a person watches and takes over in the browser; a `cad`
  bundle with Blender, FreeCAD, CadQuery, and KiCad, scripted first.
  First test and explore everything in the spec's "Verify before
  building" on gpu2 and record it; only then approve and build.
  Spec: [hi_box_desktop.md](specs/ideas/hi_box_desktop.md) (draft).
- [ ] Explore [cloudflare/security-audit-skill](https://github.com/cloudflare/security-audit-skill)
  for `hi agent review` and the deep review before a release: a
  coverage ledger of what was checked, so later runs target the gaps and
  changed code; a fresh verifier that tries to disprove each candidate;
  `confirmed` / `needs_validation` / `rejected` findings checked against
  a JSON schema; and its attack-class prompts (AI and LLM, supply chain,
  data isolation, and more). Decide what to take before any spec.

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
