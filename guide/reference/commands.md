---
title: All commands
description: Every hi command and option in one place.
---

## Overview

| Command                          | Does                                                   |
|----------------------------------|--------------------------------------------------------|
| `hi compute …`                   | Rent and use remote machines; see below                |
| `hi login <hf|colab|runpod|shadeform>` | Sign in to a compute provider               |
| `hi connect <server>`            | Join a hi server; `status`, `key`                      |
| `hi disconnect`                  | Leave it; use your own keys again                      |
| `hi data …`                      | Download your team's Hugging Face datasets, models, and buckets; see below |
| `hi server …`                    | Run the server that brokers compute; see below         |
| `hi init [<template> <dir>]`     | Start a project from a template; `--update`, `--adopt` |
| `hi q [<what you want>]`         | Ask for a shell command, or chat; see below            |
| `hi shell-init bash|zsh`        | Shell integration for `hi q`                           |
| `hi agent …`                     | Hand a task to Claude Code, Codex, or Hermes in a box; see below |
| `hi team …`                      | A team's app in a Slack channel, built by its owners in a dev box; see below |
| `hi box …`                       | Run a shell or a command in a rootless box; see below  |
| `hi bundle …`                    | Bundles of skills for `hi agent --bundle`; see below   |
| `hi skill …`                     | Find, install, and update agent skills; see below      |
| `hi install`                     | Choose workstation software from a menu                |
| `hi uninstall <tool>…`           | Remove workstation software                            |
| `hi verify strix`                | Check a Strix Halo workstation                         |
| `hi net`                         | Enroll in NetBird; `status`, `down`, `reconnect`       |
| `hi net expose <port>`           | A local service on a temporary public address; see below |
| `hi net domain [<domain> --dns <provider> \| --unset]` | https for the teams' addresses, under a domain of yours; see [The addresses on https](/guide/team/dev/#the-addresses-on-https) |
| `hi adduser <name>`              | Create a user with GPU access                          |
| `hi update [--check] [--version <tag>] [--restart|--no-restart]` | Update `hi`; offer to restart `hi server` and team services still on the old version |
| `hi version`, `hi help`          | Version and help                                       |

## hi compute

| Command                                              | Does                                                  |
|------------------------------------------------------|-------------------------------------------------------|
| `hi compute`                                         | Guided menu in a terminal; help otherwise             |
| `hi compute providers`                               | Which providers are signed in, and what is missing     |
| `hi compute hardware [--on <provider>] [--community]` | Hardware, memory, and prices; `--community` lists RunPod Community Cloud |
| `hi compute billing [<account> | --clear]`          | Who pays on Hugging Face, and the Colab balance       |
| `hi compute up [options]`                            | Start an instance                                      |
| `hi compute run [options] <script.py> [-- args]`     | Run a Python script to completion                      |
| `hi compute run [options] <image> -- <command>`      | Run a container command to completion (Hugging Face)   |
| `hi compute serve [options] <recipe | owner/repo-GGUF>` | Serve a GGUF model with an OpenAI-compatible API   |
| `hi compute ls`                                      | Everything running, with limits                        |
| `hi compute status <name>`                           | One run or instance in detail                          |
| `hi compute ssh <name> [-- <command>]`               | Shell, or one command                                  |
| `hi compute tunnel <name> <port>[:<local>]`          | Forward a remote port to `127.0.0.1`                   |
| `hi compute logs <name> [--follow] [-n <lines>]`     | Output and setup logs                                  |
| `hi compute wait <name>…`                            | Wait for runs to end; exits with their status          |
| `hi compute stop [<name> | --all] [--yes]`          | Stop and release; without a name, pick from a list    |
| `hi compute proxy <name>`                            | The SSH transport, for `ProxyCommand` (Colab)          |
| `hi compute requests [<id> [--wait] [--timeout <d>]] [--json]` | Your requests to the hi server; exit 3 pending, 4 denied |
| `hi compute extend <name> <duration> [--reason <r>] [--no-wait]` | Ask the hi server for more time |
| `hi compute live`                                    | Live view of your managed machines                     |

### Options for up, run, and serve

| Option              | up | run | serve | Meaning                                              |
|---------------------|----|-----|-------|------------------------------------------------------|
| `--on <provider>`   | ✓  | ✓   | ✓     | `colab`, `hf`, `runpod`, or `shadeform`              |
| `--gpu <hardware>`  | ✓  | ✓   | ✓     | Hardware name from `hi compute hardware`             |
| `--name <name>`     | ✓  | ✓   | ✓     | Name for later commands                              |
| `--max <duration>`  | ✓  | ✓   | ✓     | Maximum lifetime: hours (`2`, `1.5`), `30m`, `2d`, or `none` (asks first) |
| `--yes`             | ✓  | ✓   | ✓     | Skip the cost confirmation                           |
| `--dry-run`         | ✓  | ✓   | ✓     | Show the request; start nothing                      |
| `--reason <text>`   | ✓  |     | ✓     | Why you need it; approvers see it (managed providers) |
| `--no-wait`         | ✓  |     |       | Return while approval is pending; exit 3 (managed providers) |
| `--namespace <ns>`  | ✓  | ✓   | ✓     | Hugging Face account to bill this once               |
| `--high-mem`        | ✓  | ✓   |       | High-RAM machine (Colab)                             |
| `--image <image>`   | ✓  |     |       | Container image (Hugging Face, RunPod)               |
| `--env KEY=VALUE`   |    | ✓   |       | Environment variable; repeatable                     |
| `--secret KEY`      |    | ✓   |       | Encrypted secret from your shell (Hugging Face)      |
| `--data <org>/<name>[/<pattern>]` | ✓ | ✓ | | run: your team's data into `data/<name>` first. up (RunPod, Shadeform; whole repositories): `HF_ENDPOINT` and `HF_TOKEN` in the machine's shells (`hi data`) |
| `--detach`          |    | ✓   |       | Return after starting (Hugging Face)                 |
| `--quant <quant>`   |    |     | ✓     | GGUF quantization, such as `Q4_K_M`                  |
| `--ctx <tokens>`    |    |     | ✓     | Context length                                       |
| `--alias <id>`      |    |     | ✓     | Model name in the API                                |
| `--args "<args>"`   |    |     | ✓     | Extra `llama-server` arguments                       |
| `--port <port>`     |    |     | ✓     | Local port for the Colab tunnel; default 8080        |

Defaults: `--gpu` is the provider's cheapest CPU; `--max` is `4h` for `up` and
`serve` and `1h` for `run`, at most `24h` on Colab. Flags may come before or
after names for `logs`, `serve`, `stop`, and `billing`.

## hi connect and hi server

See [Managed compute for your team](/guide/compute/managed/).

| Command                                             | Does                                                 |
|-----------------------------------------------------|------------------------------------------------------|
| `hi connect <server> [--user <name>] [--no-wait]`   | Enroll this device and wait for approval             |
| `hi connect <server> --agent <name> --owner <user>` | Enroll an agent that runs on its own                 |
| `hi connect status`                                 | Server, user, group, and managed providers           |
| `hi connect key`                                    | This device's public key, for an admin to pre-approve |
| `hi connect slack <code>`                           | Link your Slack account, with the code from `/hi link` |
| `hi disconnect`                                     | Forget the server and delete the device key          |
| `hi server init [--listen <addr>]`                  | Create the server's state                            |
| `hi server run`                                     | Serve clients                                        |
| `hi server provider add|remove|list [<provider>]` | Provider keys (RunPod, Shadeform)                   |
| `hi server user add <u> --group <g> [--key <k>]`    | Add a user; `--key` pre-approves a device            |
| `hi server user remove <u>`, `user list`            | Remove a user and their devices; list users          |
| `hi server requests [--all]`                        | What is waiting for a decision                       |
| `hi server approve <id> [--group <g>]`              | Approve an enrollment or compute request             |
| `hi server deny <id> [--reason <r>]`                | Deny a request                                       |
| `hi server ls`                                      | Everything running, for every user                   |
| `hi server stop <name> | --user <u> | --all`      | Stop instances                                       |
| `hi server audit [--since <d>]`                     | The audit log                                        |
| `hi server slack setup`, `slack manifest`           | Connect a Slack app; print its manifest              |
| `hi server approvers add <id> --name <user>`        | Let a Slack member approve and stop; `remove`, `list` |
| `hi server policy show|edit|example|check`       | Group limits, auto-approve, and budgets              |
| `hi server spend [--since <d>]`                     | Spend per user and group, compute and models         |
| `hi server ai [set [--url <u>] [--model <m>] [--no-key]|off|remove]` | Pass `hi q`'s requests to OpenRouter with your team's key |
| `hi server live [--wall] [--names full] [--reasons]` | Live dashboard; `--wall` is read-only               |
| `sudo hi server wall setup|add|remove|list`       | Show the wall on screens over SSH                    |
| `hi server viewer add <name> --key <k>`             | A device that may only watch                         |
| `hi server templates add <name> <git-url> [--ref <r>]` | Serve a private repository's templates and skills |
| `hi server templates list|sync|remove [<name>]`    | Show, fetch now, or stop serving template sources  |
| `hi server templates rename <name> <new-name>`      | Rename a source; keeps its token and history         |
| `hi server data`                                    | Menu: Hugging Face organizations for `hi data`       |
| `hi server data add <org>... [--token-file <f>]`    | Serve organizations' datasets, models, and buckets; asks for a read token |
| `hi server data list|test|remove [<org>]`          | Show, check, or stop serving organizations           |
| `hi server expose [stop]`                           | The public address cloud runs reach `hi data` through, and the runs using it; close it |
| `hi server expose revoke <run>`                     | End one cloud run's token now                                             |

## hi data

Needs a connected server with organizations added, and `hf`
(`pip install -U huggingface_hub`). The Hugging Face token stays on the
server; `hf` runs with `HF_ENDPOINT` pointing to it.

| Command                                             | Does                                                 |
|-----------------------------------------------------|------------------------------------------------------|
| `hi data`                                           | Search by words, pick a dataset or model, and download it |
| `hi data ls [<org>] [--kind dataset|model|bucket] [--json]` | What you may download                   |
| `hi data info <org>/<name>`                         | Size, largest files, and last update                 |
| `hi data get <org>/<name> [--to <dir>]`             | Download it with `hf` (buckets with `hf buckets sync`); default folder `./data/<name>` |
| ↳ `--revision <rev>`, `--include <glob>`, `--exclude <glob>` | A branch, tag, or commit; filters, repeatable |
| ↳ `--no-record`                                     | Don't record it in the project's `.hi5/data.json`  |
| `hi data get`                                       | In a project: everything `.hi5/data.json` records, at the recorded commits |
| `hi data run -- <command>`                          | Run a command that reads your team's data directly    |
| `hi data env`                                       | `HF_ENDPOINT` and `HF_TOKEN` for `eval` in a shell    |

Put `dataset:`, `model:`, or `bucket:` in front when several share a name.
Buckets have no revisions. A group's
`data` patterns in policy (`["acme/*"]`) limit what it sees; without
them it sees everything.

## hi login

```sh
hi login hf       # hf auth login, then hf auth whoami
hi login colab    # the Colab CLI's browser sign-in, then the balance
hi login runpod   # asks for the API key, checks it, saves it in ~/.runpod/config.toml
hi login shadeform # asks for the API key, checks it, saves it in ~/.config/hi/shadeform_key
```

## hi init

```sh
hi init                          # choose a template, name, and directory
hi init python pricing-tools     # create ./pricing-tools from the python template
hi init web . --name dashboard   # apply the web template to this directory
hi init --list                   # the templates
hi init --update                 # bring this repository up to the current templates
hi init --update --check         # change nothing; fail when it is behind (for CI)
hi init --adopt python           # bring this existing repository under a template
```

| Option                  | Meaning                                              |
|-------------------------|------------------------------------------------------|
| `--name <name>`         | Project name; by default the directory's name        |
| `--dry-run`             | Print the plan; write nothing                        |
| `--yes`                 | Do not ask for confirmation                          |
| `--no-setup`            | Write the files; run no setup command                |
| `--github <owner/repo>` | Also create a private GitHub repository and push     |
| `--force`               | With `--update` or `--adopt`: overwrite files hi owns even when edited by hand |
| `--strict`              | With `--check`: fail when private layers can't be checked |

Templates: `python`, `web`, `app`, `service`, `pipeline`, `ml`,
`autoresearch-ml`, and on a connected
device your team's private ones.

## hi q

| Command                          | Does                                                         |
|----------------------------------|--------------------------------------------------------------|
| `hi q`                           | Chat: ask, run, and follow up; `/clear`, `/context`, `/model`, `/exit` or Ctrl-D |
| `hi q <what you want>`           | Propose one command; Enter runs it, e edits, c copies, ? explains, Esc cancels |
| `hi q -c [question]`             | Continue the last conversation                               |
| `<cmd> | hi q <question>`       | Ask about piped output, up to 32 KB                          |
| `hi q --explain '<command>'`     | Explain a command without running it                         |
| `hi q --print <question>`        | Print only the command, for scripts                          |
| `hi q --setup`                   | Choose the model (Claude Code, OpenRouter, an OpenAI-compatible endpoint, or an Anthropic key), and set up the shell |
| `hi q --status`                  | The model in use, where it came from, and the shell integration |
| `hi q --context`                 | What is sent with each question                              |
| `hi shell-init bash|zsh`        | The shell integration script, for `eval` in an rc file       |

Everything from the first word that doesn't start with `-` is the
question, so `hi q status of the log file` asks the model. hi's own options
go before it; put `--` before a question that starts with a dash.

Options: `--provider server|openai|openrouter|anthropic|claude` and `--model <name>`
for one question or chat, `--no-context` to send only the question, and
`--yes` to run read-only commands without asking. Dangerous commands always
need `yes` typed. See [Ask for a command](/guide/q/).

## hi agent

| Command                                  | Does                                                     |
|------------------------------------------|----------------------------------------------------------|
| `hi agent [claude|codex|hermes] "<task>"` | Run a task in a box on a new worktree (outside git, in the folder itself) and wait for the report; without a name, the first agent installed and signed in |
| `hi agent [claude|codex|hermes] <brief.md>` | The task is the file's contents; `-` reads it from stdin |
| `hi agent claude|codex|hermes`          | An interactive session in a box, without permission prompts |
| `hi agent wait <name> [--json]`          | Wait for a run and print its report                      |
| `hi agent best-of <n> "<task>"`          | The same task in n boxes (2 to 8) at once; hi checks each and ranks them |
| `hi agent best-of ls [--json]`           | Best-of runs and their state                             |
| `hi agent best-of show <run> [--full]`   | The table again; `--full` adds each box's diff           |
| `hi agent best-of keep <run> <box>`      | Keep one box and its branch, remove the others           |
| `hi agent best-of rm <run>`              | Remove every box of a run and its branches               |
| `hi agent best-of <n> --score "<cmd>" --lower|--higher --rounds <n>|forever "<task>"` | Rounds: each starts from the best so far, and hi commits a result only when its score beats it |
| `hi agent best-of watch <run>`           | Follow a run with rounds; q leaves the view, the run goes on |
| `hi agent best-of stop <run> [--now]`    | Stop after the current round; `--now` removes its boxes too |
| `hi agent best-of resume <run>`          | Go on from the best so far; `--rounds`, `--for`, `--budget` give more |
| `hi agent review [<folder>|<url>]`      | Agents review a whole repository, here or fetched from a URL, each in a locked box on a snapshot; hi checks every quoted line and another agent confirms or rejects each finding |
| `hi agent review ls`                     | Reviews, their state, cost, and counts                   |
| `hi agent review show <id> [--md|--html|--sarif|--json|--rejected]` | The findings; `--md` the whole report, `--html` a page to read in a browser, `--sarif` SARIF, `--json` the record |
| `hi agent review watch <id>`             | Follow a review; Ctrl+C stops following, not the review  |
| `hi agent review stop <id>`              | Stop it, keep what's done, and write the report          |
| `hi agent review rm <id>`                | Remove a review, its report, and its snapshot            |
| `hi agent token claude`                  | Store a long-lived token from `claude setup-token`       |

Options: `--detach` to start and return, `--json` for the report as JSON on
stdout, `--task-file <path>` for a task file whose name doesn't end in
`.md`, `--model <model>` for the agent's model (without it, the one in your
own Claude Code, Codex, or Hermes settings, else `opus` for Claude Code),
`--from <commit>` to start the worktree from a commit or branch instead of
`HEAD`, `--files <folder>` for files the agent may read at `/box/files`,
`--session <folder>` to keep Claude Code's or Codex's conversation in a
folder and go on with it in the next box given the same folder,
`--bundle a,b` for bundles of skills and the tools they need,
`--no-project-settings` for Claude Code without the project's own hooks,
MCP servers, and permissions, `--max-budget-usd <n>` for the most Claude
Code may spend, and the box options below. A brief's front matter can set `agent`, `model`, `bundles`,
`network`, `allow`, `data`, and `gpu`. Exits with 0 when the agent is done and
1 when it failed. See [Hand tasks to agents](/guide/agent/).

`review` takes `--area security,correctness,performance,maintainability`,
`all`, a brief (`.md`) of yours, or a name in the repository's
`.hi5/review/` (default: `security,correctness`), `--agents claude,codex`
(in turn), `--judge <agent>`, `--passes <n>` (1 to 3), `--ref <ref>` to
review a commit instead of the working tree, `--since <ref>` to focus on
what changed since, `--path <dir>` to narrow it, `--context <file.md>` for
what the code doesn't show, `--compare <id|report.json>` against an
earlier review, `--sarif`, `--out <dir>` for a copy of the report, `--name <name>` for what box names and the report call the project, `--budget <dollars>`,
`--max <duration>` per run (default 30m), `--parallel <k>` (default 3),
`--detach`, `--json`, and `--yes`. Reports are in
`~/.local/state/hi/reviews/<id>/`. See [Review a whole repository](/guide/agent/review/).

`best-of` also takes `--agents claude,codex` (taken in turn), `--check
"<command>"` (default: `make check`), `--max <duration>` per agent, and
`--yes` to start without asking; it exits with 1 when no box passed. With
rounds: `--for <duration>`, `--budget <dollars>`, `--patience <n>`,
`--min-gain <number>`, `--edit a,b` for the files an agent may change, and
`--then "<command>"`, run on this machine when the rounds end. To keep a
gain only if it also holds on data agents never see: `--validate
"<command>"` (run by hi after each round in a container without network or
`.git`, with only the `--edit` files from the box; needs `--edit`),
`--validate-data <dir>` (a folder outside the project, read-only at
`/box/data` there), and `--validate-slack <number>` (how much worse than the
highest validation so far the number may be; default 0); a winner that fails is
`rejected`. `--folds <n> --fold "<command>"` walks the validation window
forward through n folds, after `--fold-every <k>` rounds or when
`--patience` runs out; each fold starts from a new baseline. See [Keep only gains that hold](/guide/autoresearch/validation/).
See [Autoresearch](/guide/autoresearch/).

## hi team

See [Agent teams](/guide/team/): [set up a team](/guide/team/setup/) and [how the team works](/guide/team/how-it-works/).

| Command | What it does |
|---|---|
| `hi team new <name> [--dir <folder>] [--from <file.json>]` | Set up a team: its Slack app, channel, owners, members, and code, and a NetBird key for its dev box |
| `hi team adopt <name> <source> [--ref <branch\|tag\|commit>] [--github <owner/name>] [--shallow\|--full] [--dir <folder>] [--from <file.json>] [--yes]` | The same setup for a project someone else wrote (`github.com/owner/name`, `owner/name`, or a URL), fetched without running anything from it: the team keeps its own copy, with the project as `upstream` that never takes a push, and an owner's agent surveys it in the dev box; see [Start from someone else's project](/guide/team/adopt/) |
| `hi team adopt <name> <folder> [--github <owner/name>] [--full] [--dir <folder>] [--from <file.json>] [--yes]` | The same for a folder on this machine, copied without its history: what git would commit, leaving `.git`, files that look like secrets, and links outside it behind; the folder doesn't change, and there's no upstream |
| `hi team up [<name>]`                     | Start or restart it as a systemd user service, also at boot; without a name, pick it from a list |
| `hi team down <name>`                     | Stop it; its folder stays |
| `hi team rebuild <name>`                  | Build hi's base image again with Ubuntu's current packages, and restart the team on it; what fixes the image's advisories |
| `hi team rename <name> <new> [--yes]`     | Another name, after its Slack app's: its bot, `/hi-<name>` command, and service; memory and snapshots stay |
| `hi team rm [<name>] [--keep-folder]`     | Stop it and remove it from this machine: its service, its boxes, and its folder; `--keep-folder` keeps the folder |
| `hi team ls`                              | Teams on this machine |
| `hi team status <name>`                   | Up or down, its members, production's version and what dev has on top of it, backups, and the helper |
| `hi team logs <name> [-f]`                | The lead's log; `-f` follows it |
| `hi team channel <name> [--since 2h]`     | What was said in the team's channel, threads and shared files included |
| `hi team key <name> openrouter [--unset]` | Give the team an OpenRouter key of its own (asked for, never an argument), or go back to this machine's; no box holds it |
| `hi team signin <name> chatgpt [--machine]` | Sign the team in to ChatGPT, for its roles on a ChatGPT plan: a code to type on any device; only the team's service refreshes it, and no box holds it. `--machine` uses this machine's sign-in (Codex's) |
| `hi team signin <name> claude [--machine]` | The team's own Claude token, from `claude setup-token` (asked for): Claude Code in the dev box, and the team's Claude members, use it before this machine's sign-in, and fall back to OpenRouter when Claude refuses |
| `hi team member <name> <role> [--provider openrouter\|chatgpt] [--model M] [--fallback M] [--effort L]` | A member's provider, models, and effort (low, medium, high, xhigh, max; medium unless set); on ChatGPT the fallback, the same model on OpenRouter unless given, takes over when the plan refuses; a running team restarts |
| `hi team key <name> slack` | The Slack app's bot and app tokens again, after Slack gave new ones: asked for and checked, Enter keeps one; a running team restarts with them; the lead's box gets placeholders, and the team's proxy puts the tokens on its calls to Slack |
| `hi team env <name> [KEY=VALUE | KEY | --file KEY=<path> | --unset KEY]...` | The settings the team's app boxes get as environment variables: list their names, add or change them (`KEY` alone asks for the value), give a file such as a certificate (read-only in the boxes at `$KEY`), or remove one; `--dev` for the dev box's own, which gets no others |
| `hi team addresses <name> [domain\|netbird\|<host>]` | What the team's addresses are named: the machine's domain on https, the NetBird name, or one name the IT team keeps, with ports |
| `hi team addresses <name> names <production's name>\|production=… stage=… dev=… [--https]` | A name for each address, which the IT team keeps; `--https` when their proxy ends TLS in front |
| `hi team domain [<domain> [--new-token]|off]` | A domain verified in NetBird for the teams' apps on this machine, with a NetBird Admin service user's token |
| `hi team allow-ip <name> [<ip,cidr>|off]` | The only addresses the team's preview and production let in, through NetBird's Access Control; hi puts back exactly this list every minute, so set it here rather than in NetBird's dashboard |
| `hi team domain <name> on|off` | The team's app at `<name>.<domain>` and its preview at `<name>-preview.<domain>`, names that stay; off by default |
| `hi team sso provider add <name> <issuer> [--client-id <id>] [--groups-claim <claim>]` | An OpenID Connect provider for signing in to the teams' apps (Google Workspace, Entra ID, Keycloak, Auth0, Okta…); checks the issuer and asks for the client secret |
| `hi team sso <name> on <provider> [--domain <d>] [--group <g>] [--anyone]` | People without the link sign in to the app and its preview, if the rules let them in; needs `hi team domain <name> on`; prints the return addresses to register |
| `hi team sso [<name> [off]]` | The providers and the teams using them; a team's rules and last sign-ins; `off` signs everyone out |
| `hi team sso provider rm <name>` | Forget a provider no team uses |
| `hi team helper <name> [preview|production on|off]` | Whether the app's pages have the helper, help and feedback beside the page, or turn it on or off for an address from the next page load |
| `hi team snapshot <name>`                 | Take a snapshot now of the lead's memory, the team's record, and production's data; the team keeps running. It takes one daily and before every release too |
| `hi team snapshots <name>`                | List the snapshots: when, why, their size, and whether they are offsite |
| `hi team restore <name> <snapshot> [--code] [--yes]` | Put the team back as it was in a snapshot (its ID, `latest`, `v3` for the one before v3, or a day); asks first, and takes a snapshot of now so it can be undone; `--code` puts `repo/` back too |
| `hi team jobs <name> [--preview] [--json]` | The app's long work in production (or the preview): each job's state, how far it is, how many a day, where it is, and its last error; see [When something goes wrong](/guide/team/incidents/) |
| `hi team incidents <name> [<id>] [--rules] [--json]` | The open incidents, then the last month's; one incident's timeline of what hi saw, what the lead did and why, and what the people decided; `--rules`, the people's decisions about the app's jobs |
| `"$HI" team job <job> [--running\|--blocked\|--failed\|--finished] [--done <n>] [--total <n>] [--unit <word>] [--item <id>] [--error <text>] [--stall-after <duration>]` | In the app's box: report a job's progress in `$HI_JOBS_DIR`, for hi to watch |
| `hi team dev <name> [start <task> [--agent claude\|codex]\|finish <branch> [--discard]\|restart\|netbird [<key file>]\|new-host-key]` | The team's dev box, where its owners build with agents over SSH or from the dev page: whether it runs, its NetBird name, and how to get in; `start` gives a task its branch, worktree, and herdr workspace; `finish` ends a shipped one |
| `hi team heartbeat <name> [<url> [--every 1m] \| off \| test]` | The team's heartbeat: a GET to a service such as healthchecks.io at the interval (30s to 1h) while the team runs, so the service alerts when it stops. Without arguments, where it goes and the last one |
| `hi heartbeat [<url> [--every 5m] \| off \| test]` | This machine's heartbeat, from a systemd user timer (1m to 1h), for a machine that runs teams or a hi server |
| `hi team watch <name> [--auto-freeze on\|off]` | The team's own watcher, on its machine: health, network, and advisory rules each minute, and what it has open; whether it freezes the team on its own for an exploited advisory in the app or a burst to a watched host |
| `hi team freeze\|unfreeze\|pause\|unpause <name> --why "…"` | Cut the team's boxes off from the internet and pause its app, or pause every box, within a minute; and undo it. Said in the ops channel |
| `hi team ops <name> [<channel ID>\|off]` | A second Slack channel for incidents, warnings, security, keys, access, and the dev box's site asks; without one they go to the main channel. A running team's lead restarts to talk in both |
| `hi team dev <name> allow [<site> [--why <what for>] \| --remove <site>]` | The sites the dev box reaches, a database's name too, as the owners allow them on the dev page; with no site, the list and the asks waiting |
| `hi team owners <name> [add\|remove <member ID>]` | The team's owners, who build in the dev box; removing one ends their SSH key and links |
| `hi team owners <name> key <member ID> <public key>\|--remove` | An owner's SSH key for the dev box; a new one replaces the old |
| `hi team access <name>` | Who reaches what: owners, developers, and contributors; anyone else has no level |
| `hi team access <name> <member ID> pro\|vibe\|contributor\|none` | An owner's change, also on the dev page's People table: a Pro Dev builds with Chat, terminal, VS Code, and SSH but doesn't release; a Vibe Dev builds in Chat only; a contributor reports bugs as tasks; none takes the level away |
| `hi team access <name> key <member ID> <public key>\|--remove` | A developer's or owner's SSH key for the dev box |
| `hi team task <name> [list [--closed]\|show <n>\|add <title> [<body\|->]\|comment <n> <text\|->\|edit <n> [--title <t>] <body\|->\|close <n> [<comment>]]` | The team's tasks: GitHub Issues (`gh`), GitLab issues (`glab`), where its repository is, or hi's own; `--attach <file>` with `add` or `comment` |
| `hi dev preview\|ship\|status\|say\|task\|allow` | In a team's dev box: put a branch on the preview, ship it into dev, see what runs where, post in the channel, the tasks, and ask the owners for a site the proxy refused; see [Build in the dev box](/guide/team/dev/) |
| `hi dev review\|deep-review\|brainstorm` | In a team's dev box: a second opinion from the other agent, a multi-agent review of the project, or several agents' ideas |
| `hi dev data\|settings\|incidents` | In a team's dev box: the app's data and a new fork for the preview, the settings' names and the preview's own values, what hi recorded about incidents |
| `hi dev upstream\|propose` | In a team's dev box, for an adopted project: where dev stands against the original and its refs into the box's clone; offer shipped work back as a draft pull request; see [Start from someone else's project](/guide/team/adopt/) |
| `hi team say <name> <text\|-> [--wait]` | Tell the lead something from this machine: hi posts it in the channel and the lead answers in its thread (`--wait` prints the answer); a change it asks for, the lead files as a task |
| `hi team data <name> [fork \| copy preview production [--yes] \| preview empty]` | The app's data folders, production's and the preview's fork of it; a new fork now; production from the preview, snapshotted first; or a preview that starts empty at every release |
| `hi team limit <name> [preview <usd> \| preview off]` | What the preview's model calls may cost a day ($5 unless set), and spent today |
| `hi team limit <name> memory [dev\|app <size>\|default]` | The boxes' memory: the dev box 16g and Stage's and production's boxes 4g, unless the team has its own; running boxes get a new limit at once, without a restart |
| `hi team rollback <name> v3 [--code-only] [--yes]` | Put production back on v3 with its data as v3 left it (or `--code-only`); what was entered since is kept in a snapshot. Owners do the same with Roll back on the dev page |
| `hi team backups <name> [--offsite s3:https://<host>/<bucket> [--region <r>] | --offsite off] [--keep d,w,m] [--show-key]` | Where snapshots are kept and for how long; copy them to an S3-compatible bucket too; print the key they are encrypted with |
| `hi team bom <name> [v<n>|preview]`       | Production's bill of materials (CycloneDX), or a release's, or the preview's |
| `hi team advisories <name>`               | Known advisories for what production runs, as the team's hi server last matched them; the lead has it too |
| `hi team advisory <name> ignore <id> --because "<why>" [--package <p>]` | An owner's word that an advisory doesn't apply, until the advisory changes; `unignore <id>` takes it back |
| `hi team manifest <name>`                 | The Slack app manifest again |
| `hi team github <name> <owner/repo>`      | Put the project on GitHub (a new private repository, or an empty one); the team pushes every merge there |
| `hi team upstream <name>`                 | For an adopted project: fetch the project it came from, and say which of its versions dev has, how many commits are the team's, and what it has added since, with its new tags |

In its box, the lead runs `task [list|show|add|comment|edit]` (it files what
people ask for; the owners close tasks), `versions` (production's
version and what dev has on top of it), `channel [--since 2h | --new]`,
`task start <n>` (when an owner asks: start work on it in the dev box),
`web` (the feedback from the app), `web show <feedback>`, `web questions`
(what people asked the app's help), `web link --user <member ID>` (DM an
owner their link to the dev page, as `/hi-<team> dev` does), `env`, `data`, and `helper`
(read only), `upstream`, `advisories`, `jobs [restart --incident <id>
--because <why>]` (one restart per incident), `incidents [note|ask|close]`,
and `alert`. It has no command that changes the code, dev, or production:
the owners build in the dev box and release from the dev page.

## hi box

| Command                              | Does                                                     |
|--------------------------------------|----------------------------------------------------------|
| `hi box shell`                       | A shell in a box for this project                        |
| `hi box run -- <command>`            | One command; exits with its status                       |
| `hi box ls`                          | Boxes, state, branch, and changes                        |
| `hi box attach <name>`               | Follow an agent, or take over a box's terminal           |
| `hi box diff <name> [--full]`        | Commits and files, flagging files that run on the host   |
| `hi box allow <name> [<domain>]`     | Allow a domain, or list what was refused                 |
| `hi box stop|rm <name> [--force]`   | Stop, or remove the box and its worktree                 |
| `hi box rm --all [--force] [--yes]`  | Remove every box after one question                      |

Options when starting: `--name`, `--network locked|dev|open`,
`--allow <domain>`, `--gpu`, `--data` (your team's data through the hi server),
`--worktree`, `--here`, `--image`, `--memory`, and `--bundle a,b`. A
project's `devcontainer.json` can set `network`, `domains`, `gpu`, `data`,
and `bundles` under `customizations.hi`. See
[Run code in a box](/guide/box/).

## hi bundle

| Command                              | Does                                                     |
|--------------------------------------|----------------------------------------------------------|
| `hi bundle ls [--json]`              | Bundles for `--bundle`, and where each comes from        |
| `hi bundle show <name>`              | Its skills, at which commit, what each needs, and its network |
| `hi bundle prune [--all]`            | Remove bundle images unused for 30 days (`--all`: every unused one) |

Bundles come from hi itself, your team's template sources on a hi server,
and `~/.local/share/hi/bundles` (or `HI_BUNDLES_DIR`); a later one wins on
a name. See [Bundles](/guide/agent/#bundles-skills-and-the-tools-they-need).

## hi skill

| Command                              | Does                                                     |
|--------------------------------------|----------------------------------------------------------|
| `hi skills`                          | In a terminal: search skills.sh, browse, and pick        |
| `hi skill find <query>`              | Search skills.sh: installs, makers, and audits           |
| `hi skill add <source>`              | Install from `owner/repo`, `owner/repo/skill`, a git URL, or a folder |
| `hi skill add hi`                    | Write the `hi` skill (plain `hi skill` does too, without a terminal) |
| `hi skill ls`                        | Installed skills, their source, commit, and state        |
| `hi skill show <name|source>`       | Description, license, files, and audits                  |
| `hi skill update [name…] [--check]`  | Move skills to their source's newest commit              |
| `hi skill update --bundles`          | Move the skills in `bundles/<name>.json` to newer commits |
| `hi skill rm <name>…`                | Remove skills                                            |
| `hi skill --print`                   | Print the `hi` skill; write nothing                      |

Options: `--global` (your home folder), `--skill a,b`, `--yes`,
`--accept-risk`, `--ref`, `--force`, and `--json`. `hi skills` is the same
command. See [Skills](/guide/reference/skill/).

## hi install and hi verify

```sh
hi install                     # menu: check to install, uncheck to remove
hi install --list              # tool names, and which are installed
hi install claude gh           # install or reinstall the named tools
hi install --all [strix]       # every tool; Strix Halo support only when named
hi install strix               # the menu with Strix Halo support checked
hi uninstall docker            # remove the named tools
hi verify strix                # the Strix report again, after a reboot
```

## hi net

```sh
hi net                                   # enroll; prompts for the key
hi net --setup-key-file <file>           # enroll from a file only you can read
HI_NETBIRD_SETUP_KEY=… hi net            # enroll from the environment
hi net status | down | reconnect
```

| Command                                             | Does                                                 |
|-----------------------------------------------------|------------------------------------------------------|
| `hi net expose <port>`                              | Expose `localhost:<port>` until `--max` or Ctrl+C    |
| ↳ `--max <duration>`                                | How long; default `1h`, at most `24h` (30 days with `--domain`) |
| ↳ `--domain <name>`                                 | An address that stays, under a domain verified in NetBird; needs a NetBird API token |
| ↳ `--link`, `--password <text>`, `--pin <6 digits>`, `--groups <g1,g2>`, `--public` | The lock: a generated password by default; `--link` puts a key in the link instead |
| ↳ `--allow-ip <ip,cidr>`                            | Only from these addresses, through NetBird's Access Control; needs a NetBird API token |
| ↳ `--sso <file>`                                    | With `--link` and `--domain`, people may also sign in with the OIDC provider and rules in the file (`hi team sso` writes it) |
| ↳ `--vpn`                                           | No public address: only devices on the NetBird network, at `http://<name>:<port>` |
| ↳ `--name <prefix>`, `--host <addr>`                | The start of the address; where the service listens  |
| ↳ `--detach`, `--json`                              | Run in the background; print JSON                    |
| `hi net expose ls [--json]`                         | What this machine is exposing                        |
| `hi net expose stop <name> | --all`                | Stop it now; `--all` stops every exposure of this user, a team's preview and dev page too |

See [Share a local service](/guide/workstation/expose/).

## hi adduser

```sh
hi adduser <name>    # Ubuntu adduser, plus the render and video groups
```
