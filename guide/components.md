---
title: The components
description: The standard components hi5's software factory is built from, such as hi box, hi agent, hi compute, hi server, and hi net. A hi team puts them together, and each one also works on its own.
---

hi5's software factory is built from standard components. A hi team puts
them together, and each one is also a command you can use on its own, in
your terminal: hand a task to an agent, run code in a box, rent a GPU, or
share what you're running.

<div class="cards">
  <a href="{{ '/team/' | relative_url }}"><strong>hi team</strong><span>An app in Slack, built by its owners in a dev box.</span></a>
  <a href="{{ '/agent/' | relative_url }}"><strong>hi agent</strong><span>Hand a task to a coding agent.</span></a>
  <a href="{{ '/autoresearch/' | relative_url }}"><strong>hi agent best-of</strong><span>Several attempts at once, and rounds that keep only gains.</span></a>
  <a href="{{ '/box/' | relative_url }}"><strong>hi box</strong><span>Run code with nothing to steal.</span></a>
  <a href="{{ '/compute/' | relative_url }}"><strong>hi compute</strong><span>GPUs on Colab, Hugging Face, RunPod, Shadeform.</span></a>
  <a href="{{ '/q/' | relative_url }}"><strong>hi q</strong><span>A shell command from plain words.</span></a>
  <a href="{{ '/init/' | relative_url }}"><strong>hi init</strong><span>Start a project agents work well in.</span></a>
  <a href="{{ '/net/' | relative_url }}"><strong>hi net</strong><span>Share what you're running on a temporary address.</span></a>
  <a href="{{ '/server/' | relative_url }}"><strong>hi server</strong><span>One place for the team's keys and approvals.</span></a>
  <a href="{{ '/data/' | relative_url }}"><strong>hi data</strong><span>Your team's datasets, without a token.</span></a>
</div>

## Agents

Different agents have different strengths. The same tools a hi team uses
work in your terminal: hand work to Claude Code, Codex, or Hermes, try ideas
several ways at once, and keep only what works.

### Delegate a task

`hi agent` hands a task to any agent. It works without permission prompts
in a box, on its own branch, and you get one report back, the same for every
agent.

```text
$ hi agent "make the flaky sync test reliable"
The test now waits on the thread's event.
Changed on hi-box/myproject-1: tests/test_sync.py
```

[Hand tasks to agents]({{ '/guide/agent/' | relative_url }})

### Compare several attempts

`hi agent best-of` runs one task several ways at once, mixing agents. hi
runs your check on each result itself and ranks them; you keep the best.

```sh
hi agent best-of 3 --agents claude,codex "make the loader 2x faster"
```

[Several attempts at once]({{ '/guide/autoresearch/best-of/' | relative_url }})

### Improve overnight

Give a score to improve and let it run: each round starts from the best so
far, and hi commits a result only when its score beats it.

```sh
hi agent best-of 4 --rounds 100 --score "make score" --lower
```

[Rounds that keep only gains]({{ '/guide/autoresearch/rounds/' | relative_url }})

### Ask for a command

`hi q` takes what you want in plain words and gives back one shell command.
You see it, and what it would change, before anything runs.

```text
$ hi q move the md files into notes
mkdir -p notes && mv -- *.md notes/
```

[Ask for a command]({{ '/guide/q/' | relative_url }})

## Infrastructure

Every agent works somewhere it can't hurt you, and every GPU stops when its
time is up.

1. **You** start `hi box shell` or `hi agent`.
2. **The box** holds the project, and nothing else from your home folder.
3. **The proxy** is the box's only way out, and adds keys the box never holds.
4. **Allowed hosts**, such as GitHub and package registries, are all it
   reaches, and every request is logged.

| Component | What it does |
|-----------|--------------|
| [`hi box`]({{ '/guide/box/' | relative_url }}) | A room for every agent: a rootless container with the project and nothing else. No SSH keys, no tokens, and a network that reaches only what you allow. The GPU too, if you want it. |
| [`hi compute`]({{ '/guide/compute/' | relative_url }}) | GPUs when you need them: one set of commands for Colab, Hugging Face, RunPod, and Shadeform to run a job, open a shell, or serve a model. Every machine has a time limit. |
| [`hi server`]({{ '/server/' | relative_url }}) | One place for the team's keys: it holds the provider keys, approves GPU starts in Slack, warns on budgets, and serves the team's data, model, and private templates. |
| [`hi net expose`]({{ '/guide/workstation/expose/' | relative_url }}) | Share what you're running: a web app on your machine on a temporary HTTPS address, behind a password, a PIN, or a key in the link. It ends on its own. |

## The workstation

hi5 started as one command that turns fresh Ubuntu into an AI workstation.
It's still the foundation: a single static binary, with no runtime to
install. [Install hi]({{ '/guide/install/' | relative_url }}) has the
commands, and [Set up a workstation]({{ '/guide/workstation/' | relative_url }})
walks through it.

Every workstation gets the same coding agents and GPU tooling at first login:

| Tool | Command |
|------|---------|
| [Claude Code](https://code.claude.com/docs) | `claude` |
| [Codex CLI](https://developers.openai.com/codex/cli) | `codex` |
| [Hermes Agent](https://github.com/NousResearch/hermes-agent) | `hermes` |
| [omp](https://omp.sh) | `omp` |
| [herdr](https://herdr.dev) | `herdr` |
| [AMD ROCm 10](https://rocm.docs.amd.com) | `hi install strix` |
| [amd-debug-tools](https://pypi.org/project/amd-debug-tools/) | `hi install strix` |

It also gets [uv](https://docs.astral.sh/uv/), [pipx](https://pipx.pypa.io),
[Node.js](https://nodejs.org), [Docker](https://docs.docker.com/engine/),
[Podman](https://podman.io), [GitHub CLI](https://cli.github.com),
[NetBird](https://netbird.io),
[Colab CLI](https://github.com/googlecolab/google-colab-cli),
[hf CLI](https://huggingface.co/docs/huggingface_hub/guides/cli),
[btop](https://github.com/aristocratos/btop),
[tmux](https://github.com/tmux/tmux), and
[timg](https://github.com/hzeller/timg).

### Tested on Strix Halo

hi5 is tested on an AMD Ryzen AI Max+ 395 with Radeon 8060S graphics; the
final report finds the GPU through `amd-smi` and `rocminfo`.

1. Run the Strix Halo one-liner on Ubuntu 26.04.
2. Pick a hostname and enter your sudo password.
3. Reboot, then `hi verify strix`.
4. `hi net` to join NetBird.

## Projects agents work well in

`hi init` starts a repository with one check agents run, short agent rules,
safe settings, and CI, from built-in templates or your team's own.
`hi skills` installs agent skills, pinned and audited. `hi data` downloads
your team's private datasets without a token on your machine.

```sh
hi init python pricing-tools
cd pricing-tools && make check
```

[Start a project]({{ '/guide/projects/' | relative_url }}),
[Download the team's data]({{ '/guide/data/' | relative_url }}),
[Skills]({{ '/guide/reference/skill/' | relative_url }})

## Every command

| Command | What it does |
|---------|--------------|
| `hi install [strix]` | Install the workstation software; with `strix`, ROCm and GPU groups too, ending with a hardware report. |
| `hi verify strix` | Re-run the hardware report after the reboot. Nothing is uploaded. |
| `hi net` · `status` · `down` · `reconnect` | Join NetBird, then check, disconnect, or reconnect. |
| `hi net domain <domain>` | https, with a trusted certificate, for the teams' addresses on the network. |
| `hi net expose <port>` | A local service on a temporary public HTTPS address, password-protected by default. |
| `hi init <template> <dir>` | Start a project agents work well in; `--update` keeps it current. |
| `hi q <what you want>` | A shell command from plain words, or a chat. |
| `hi agent <task>` | A task for Claude Code, Codex, or Hermes, in a box, with one report. |
| `hi agent best-of <n> <task>` | One task in n boxes, checked and ranked; with `--score`, rounds that keep only gains. |
| `hi agent brainstorm <question>` | Several agents think it through alone; one compiles a proposal. |
| `hi team new <name>` | A team in one Slack channel that runs an app: a lead for the people, a dev box for its owners. |
| `hi box shell` · `run` | A shell or a command in a rootless container with no credentials. |
| `hi compute run` · `up` · `serve` · `stop` | Remote GPUs on Colab, Hugging Face, RunPod, or Shadeform, with time limits. |
| `hi connect <server>` | Join your team's hi server. |
| `hi data` | Your team's private Hugging Face datasets, models, and buckets. |
| `hi skills` | Agent skills from skills.sh, pinned and audited. |
| `hi bundle ls` · `show` | Skills and the tools they need, for `hi agent --bundle`. |
| `hi adduser <name>` | A user with render and video access. |
| `hi update` · `version` | Update hi, checksum-verified, or print its version. |

[All commands]({{ '/guide/reference/commands/' | relative_url }}) has every
option.
