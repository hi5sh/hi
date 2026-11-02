# `hi serve`: recipes served on rented hardware and on this machine

Status: Approved (2026-11-02), for `hi serve llm --on local` and for the
frame it is built on, so other kinds fit later. Other kinds, recipes from
the org and your own folder, and the org's catalog of what is served are a
draft: [hi_serve_recipes.md](../ideas/hi_serve_recipes.md).

`hi model` (once docs/specs/ideas/hi_model.md) is merged in here, and there
is no `hi model` command; see "From `hi model`".

Dependencies: [hi_org.md](hi_org.md) (`hi serve llm`, `ls`, `logs`, `stop`,
and what `--on` means), [hi_compute.md](hi_compute.md) (serving on rented
hardware), [hi_box.md](hi_box.md) (rootless Podman, the hardening, `--gpu`,
the internal network and its proxy), [hi_data.md](hi_data.md) (the team's
private models through the org server), [hi_server_ai.md](hi_server_ai.md)
(the org's model pass-through),
[hi_compute_serve_pinning.md](../ideas/hi_compute_serve_pinning.md) (one
pinned llama.cpp per recipe), [hi_llm.md](../ideas/hi_llm.md) (named
upstreams), and [hi_cluster.md](../ideas/hi_cluster.md) (`--on <machine>`
later).

## Goal

The team's Strix Halo machines have 128 GB of memory that a GPU can use,
but a model served on one is started by hand, and nothing in hi knows it
is there. `hi serve llm --on local` serves a model on the machine you're on,
the way `hi serve llm --on colab` serves one on a rented GPU.

- **The same command and recipes.** A recipe that runs on Colab also says
  how it runs on a Strix Halo, and `hi serve ls`, `logs`, and `stop` show
  local models next to rented ones.
- **Safe to leave running.** The server runs in a box that can't reach
  out, holds no credentials, and has the model read-only. The only way in
  is its port on `127.0.0.1`, unless you share it.
- **Teams first.** A model never takes memory a team's load needs, and
  never makes the kernel kill a team's process to fit.
- **A service, not a terminal.** It keeps running after you log out and
  after a reboot, until you stop it or its `--max` ends.
- **Room for more than models.** A web server, a file share, or a database
  is served the same way later (hi_serve_recipes.md). Release 1 builds the
  frame they need, so a new kind is a new recipe and a little code, not a
  new command.

## The frame: kinds, recipes, and where

```text
hi serve <kind> <recipe> [inputs] [--on <where>] [options]
```

- **A kind** is what clients speak and how hi checks and secures it: `llm`
  is an OpenAI-compatible API. Kinds are code in hi: each says how a
  server of its kind is checked for health, what its address looks like,
  what credentials it takes, and how to connect to it. A new kind comes
  with a hi release.
- **A recipe** is one piece of software set up one way: an image pinned by
  digest, its arguments, its ports, its memory, what it mounts, and where
  it has been tested. Recipes are data files, not code, and never shell
  commands. In this spec they are built in; hi_serve_recipes.md adds the
  org's recipes and your own.
- **Where** is `--on`, with the meaning it has everywhere in hi
  (hi_org.md): a provider for rented hardware, `local` for this machine,
  and later a machine name or `auto` (hi_cluster.md). A recipe says where
  it can run; rented hardware is for `llm` only.

`hi serve` takes kinds and verbs in the same place, so they share one list
of names and a recipe can't be named like either:

| Words | |
|---|---|
| Kinds | `llm` (this spec); `web`, `files`, `db` (hi_serve_recipes.md) |
| Verbs | `ls`, `status`, `logs`, `stop`, `start`, `rm`, `key`, `detect`, `recipes`, `pull`, `models`, `help` |

A bare `hi serve <recipe>` is not accepted (hi_org.md). `hi serve <kind>`
alone lists that kind's recipes and what each needs.

## Commands

```text
hi serve llm <recipe | owner/repo-GGUF --quant Q> --on local [options]
    --port <port>          on 127.0.0.1 (default: 8080, or the next free one; kept across restarts)
    --ctx <tokens>         context length (recipes set their own)
    --memory <size>        the box's memory limit (default: the recipe's need plus headroom)
    --backend rocm|vulkan  default: rocm on Strix Halo
    --max <duration>       stop after this long (default: none; it's your machine)
    --share                also on this machine's NetBird address, with a key
    --name, --alias, --args, --yes, --dry-run   as on rented hardware

hi serve ls [--json]            everything you serve, rented and local, with its state
hi serve status <name>          its address, how to connect, health, memory, image, since when
hi serve logs <name> [--follow]
hi serve stop <name>            local: stops it and keeps it; rented: ends the machine
hi serve start <name>           local: starts a stopped one again, on the same port, with the same key
hi serve rm <name>              local: stops it and forgets it
hi serve key <name>             print a shared server's key

hi serve detect                 what this machine can serve on: GPU, backend, Podman, free memory and disk, what teams use
hi serve recipes [<kind>]       the recipes, where each runs, and whether this machine fits
hi serve pull llm <recipe>      download a model and its image now, and serve nothing
hi serve models [--json]        models downloaded here, their size, and which servers use them
hi serve models rm <repo>       delete a downloaded model that no server uses
```

`hi serve ls` shows `local` in the `WHERE` column and
`http://127.0.0.1:<port>/v1` as the address, and a `STATE`: starting,
running, stopped, or failed. A rented server is gone when it stops, since
a rented machine can't be kept without paying for it; a local one is kept,
stopped, until `hi serve rm`, so `start` brings it back as it was.

### From `hi model`

hi_model.md asked for `hi model detect`, `list`, `pull`, `serve`, `status`,
`logs`, and `stop`. They are all `hi serve`, so models and everything else
served share one list and one set of verbs:

| `hi model` | `hi serve` |
|---|---|
| `detect` | `hi serve detect` |
| `list` | `hi serve recipes llm` |
| `pull <recipe>` | `hi serve pull llm <recipe>` |
| `serve <recipe> [--dry-run]` | `hi serve llm <recipe> --on local [--dry-run]` |
| `status` | `hi serve ls`, `hi serve status <name>` |
| `logs`, `stop` | `hi serve logs`, `hi serve stop` |

What it required is kept below: detection from evidence, recipes as pinned
data, one validated profile per backend, storage that outlives containers,
`127.0.0.1` by default, keys never in arguments, and lifecycle commands that
touch only what hi labeled as its own.

## What happens

```text
 hi serve llm qwen3.8-flash-next --on local
   1. detect     Strix Halo (gfx1151), /dev/kfd and /dev/dri, ROCm, Podman with crun; 104 GB free, teams use 18 GB
   2. check      the recipe needs ~95 GB with its context: fits, 9 GB left; 41 GB to download, 300 GB free
   3. download   unsloth/Qwen3.8-Flash-Next-GGUF:UD-Q3_K_XL at a8c1f0e into ~/.local/share/hi/models, verified
   4. start      hi-serve-qwen3-8-flash-next: a box with the GPU, no way out, the model read-only
   5. wait       until /health answers, printing each step
   6. ready      OpenAI-compatible API: http://127.0.0.1:8080/v1   model: qwen3.8-flash-next
```

1. **Detect.** Evidence, not names. A Strix Halo is `gfx1151` with
   `/dev/kfd`, `/dev/dri`, and the render and video groups (what `hi verify
   strix` checks), and a ROCm device that `rocminfo` lists (or, for
   `--backend vulkan`, one that `vulkaninfo` lists). It also needs rootless
   Podman with `crun`, and lingering for the account (below). Anything
   missing is reported with the `hi install` command that fixes it, and
   nothing is guessed.
2. **Check memory and disk.** On Strix Halo the GPU's memory is system
   RAM, so the need is the model file plus the context's cache, from the
   recipe. The room is `MemAvailable`, less what the teams on the machine
   reserve and a headroom of 8 GB. The download must fit on the disk with
   10% to spare. If either doesn't fit, hi says by how much and starts
   nothing. `--yes` doesn't override it.
3. **Download on the host, not in the box.** hi downloads the GGUF files
   from the Hub itself, at the revision the recipe pins, resumably, into
   `~/.local/share/hi/models/<owner>--<repo>/`, and checks each file's
   SHA-256 against the Hub's. Before downloading, it shows the repository,
   revision, files, sizes, license link, and destination. A partial file is
   kept as partial and never served. A model in the team's private
   organizations comes through the org server (`hi data`), so no Hugging
   Face token is on the machine. A model already there is used as it is.
4. **Start the box as a service** (below), with the GPU devices from `hi box
   --gpu` and the model folder mounted read-only.
5. **Wait** for llama.cpp's `/health`, printing the steps from its log, as
   on rented hardware, within the same 45 minutes. If it fails, hi shows the
   log's end and leaves it stopped, with nothing half-replaced.

`--dry-run` does steps 1 and 2, then prints the download, the image, the
mounts, the devices, the units, and the address, and changes nothing.

## How a served box runs

The same for every kind and recipe, so a new kind gets it for free.

- **Its own network, with no way out.** Each served box is on its own
  internal Podman network, as `hi box` boxes are. A box with `--network
  none` can't be reached on a published port, so that isn't used.
- **A front.** A small container on that network and a normal one, running
  hi read-only as the `hi box` proxy does, is the only way in. It publishes
  the box's ports on `127.0.0.1` and, when shared, on this machine's
  NetBird address, and forwards them to the box. For HTTP kinds it checks
  the key and writes an access line for each request (who, from which
  address, which path, the status); for other protocols it passes the
  connection through. Its egress allowlist is empty: a recipe that needs a
  host names it, with a reason, as bundles do (hi_serve_recipes.md), and no
  `llm` recipe does.
- **Hardened as `hi box` boxes are.** Rootless Podman under your own
  account, no capabilities, no privilege gain, a pids limit, a read-only
  root filesystem with the recipe's writable paths as tmpfs, only the
  mounts the recipe declares, never the container engine's socket, and no
  credentials. A secret the server needs is a file mounted read-only,
  never an argument or an environment value.
- **A hard memory limit and `oom_score_adj=1000`**, so under pressure the
  kernel kills the served box before anything of a team's.
- **Systemd user units, written as Quadlet files.** `hi serve` writes
  `hi-serve-<name>.network`, `hi-serve-<name>.container`, and
  `hi-serve-<name>-front.container` under `~/.config/containers/systemd/`.
  Podman makes the units `hi-serve-<name>.service` and
  `hi-serve-<name>-front.service` from them. Their command is Podman, not
  hi, so `hi update` never restarts a server. It offers to restart a front
  that runs an old hi, which drops open connections and nothing else.
  `Restart=on-failure`, enabled so they come back after a reboot, with
  `RuntimeMaxSec` when `--max` is set.
- **Lingering.** User units stop when you log out unless the account
  lingers. If it doesn't, `hi serve` says so with the command, `sudo
  loginctl enable-linger <user>`, as `hi team up` and `hi heartbeat` do.
- **Podman only.** Docker's daemon runs boxes as root, and Quadlet is
  Podman's. Without Podman, hi says to run `hi install podman`.
- **Labels.** Every container, network, and unit carries `hi.serve=<name>`.
  `stop`, `start`, and `rm` touch only what carries it, never anything
  matched by name alone.

A served thing is recorded in `serving.json` in hi's compute state, as
rented ones are since v2.6.56. The record gains its kind, recipe, where,
state, port, units, image digest, and whether it is shared. Records written
before that read as `llm` on their provider.

## Recipes

A recipe is a JSON file, built in under `recipes/<kind>/<name>.json`. The
`qwen3.8-flash-next` recipe moves from Go into one, with an entry per place
it runs:

```json
{
  "kind": "llm",
  "name": "qwen3.8-flash-next",
  "description": "Qwen 3.8 Flash Next, 3-bit, 128k context",
  "model": { "repo": "unsloth/Qwen3.8-Flash-Next-GGUF", "revision": "<commit>", "quant": "UD-Q3_K_XL" },
  "alias": "qwen3.8-flash-next",
  "context": 131072,
  "args": ["--temp", "1.0", "--top-p", "0.95", "--top-k", "20", "--min-p", "0.0"],
  "on": {
    "colab": { "hardware": "G4", "args": ["-ot", "per_layer_token_embd\\.weight=CPU", "--lazy-mode", "off"],
               "tested": "Colab G4, 2026-09-27: ~84 tokens/s, 62.9 GB VRAM at 131k context, ready in ~7 min" },
    "hf": { "hardware": "rtx-pro-6000" },
    "local": {
      "rocm": { "image": "ghcr.io/ggml-org/llama.cpp:server-rocm-<build>@sha256:…", "memory": "95G",
                "tested": "Strix Halo, 2026-11-…: … tokens/s, ready in … min" }
    }
  }
}
```

- One llama.cpp build per recipe, pinned by digest
  (hi_compute_serve_pinning.md), with one image per backend.
- Arguments are lists, never a shell line. They can't set where the server
  listens or its key; hi sets those.
- An ad hoc model (`owner/repo-GGUF --quant Q`) runs on the default
  backend, at the repository's current revision, with no tested line, and
  its need is the file size plus a context estimate.
- `--args` adds arguments, as on rented hardware. They are shown in the
  dry run, and refused if they set the address, the port, or a key.
- A CPU profile is only for a recipe with a validated one, and it is never
  a silent fallback after a GPU check fails.

## Sharing

`--share` also publishes the API on this machine's NetBird address, so a
teammate's device on the VPN can use it. A shared server always has a key.
hi makes one, keeps it at `~/.config/hi/serve/<name>.key` (0600), hands it
only to the front, which checks it, and prints it once; `hi serve key
<name>` prints it again. The key never reaches the box. The address and the
key are what `hi q --setup` or any OpenAI-compatible client needs.

The key is the only check hi makes: who may reach the machine over NetBird
is set by the IT admins in NetBird's access control, not by hi. On
`127.0.0.1` no key is asked for, as llama.cpp does by default. Public
addresses are not offered; `hi net expose` stays the tool for that, with
its own rules.

## The org knows (release 3)

A shared model can be named to the org:

```text
hi serve llm qwen3.8-flash-next --on local --share --org
```

The device asks the org server to add it as a named upstream of `hi org ai`
(the named upstreams of hi_llm.md), with its NetBird address and key; an
admin approves it in Slack, as with any request. Devices then reach it
through the org server, signed, and its use shows in `hi org spend` at zero
cost. `hi serve rm` removes it from the org; `stop` marks it down.

## Teams first

The machines that serve models also run teams, and a team's load is never
interrupted.

- Every served box has a hard memory limit (`--memory`, from the recipe)
  and `oom_score_adj=1000`.
- What teams reserve comes from their boxes' memory limits, as `hi serve
  detect` shows. A team started after the model may still need room. hi
  doesn't stop the model for it, but `hi team up` warns when a served box
  leaves too little, naming it and `hi serve stop`.
- There is no queue and no eviction: a model that doesn't fit is refused.

## Verify before building

Each check below is tried on a Strix Halo machine that runs no production
teams, and the result, with the date, versions, and commands, is written
under Findings. A check that fails changes the design here first.

1. A Quadlet `.container` on two networks starts as a user unit, survives
   logout with lingering, and starts again after a reboot, with rootless
   Podman 5.7 and `crun`.
2. A box on an `--internal` network can't reach any address outside it,
   and the front reaches the box and publishes its port on `127.0.0.1`.
3. The pinned llama.cpp `server-rocm` image finds the gfx1151 GPU with
   `--device /dev/kfd --device /dev/dri --group-add keep-groups`, loads
   qwen3.8-flash-next, and serves it. Record the tokens per second and the
   time until ready.
4. With `--memory` and `oom_score_adj=1000`, memory pressure kills the
   served box and not a team's process.
5. The front passes streamed answers through as they come, without
   buffering them.

## Releases

1. **Local on Strix Halo.** `--on local` with the ROCm backend, built on the
   frame: recipe files (the existing recipe moved into one), the record's
   new fields, the served box, its front and units, and lingering. Also
   `hi serve detect`, `recipes`, `pull`, `models`, `ls`, `status`, `logs`,
   `stop`, `start`, and `rm`, and the memory and disk checks.
   qwen3.8-flash-next is tested on a Strix Halo that runs no production
   teams.
2. **Sharing.** `--share`, the key at the front, `hi serve key`, and the
   access lines in `hi serve logs`.
3. **The org knows.** `--org`, with hi_llm.md's named upstreams.
4. **More backends.** Vulkan on Strix Halo, then NVIDIA through Podman's CDI
   devices, with the driver checked against the image's CUDA before the
   pull, and CPU profiles where a recipe has a validated one. Each comes
   with its own tested line.
5. **On the org's machines.** `--on <machine>` and `--on auto` through `hi
   org machines` (hi_cluster.md, release 3): the same box and units, placed
   by the org server.

Other kinds follow in hi_serve_recipes.md, after release 2 here.

## Acceptance criteria

1. On a machine without a usable GPU or Podman, `hi serve detect` and `hi
   serve llm --on local` say what is missing and which `hi install` adds
   it, and start nothing.
2. A model that doesn't fit in what teams leave, or on the disk, is refused
   before any download, with the numbers.
3. `--dry-run` downloads, pulls, creates, and starts nothing.
4. Downloads resume, are pinned to the recipe's revision, and every file is
   checked against the Hub's SHA-256. A partial file is never served.
5. The served box can't reach anything outside its network. It has no
   capabilities and holds no credentials, its model is read-only, and its
   port is on `127.0.0.1` only unless `--share`.
6. A shared server refuses requests without its key, and the key is in no
   process's arguments and never in the box.
7. Under memory pressure the served box is killed before any team's
   process.
8. A local server survives logout and reboot, stops at `--max`, and `hi
   update` leaves it running. `stop` keeps it, and `start` brings it back
   on the same port with the same key.
9. `hi serve ls`, `logs`, and `stop` show and stop local and rented models
   alike, and `stop`, `start`, and `rm` touch only units and containers
   labeled as hi serve's.
10. `hi serve models rm` is refused while a server uses the model.
11. Rented serving works as in v2.6.56, and records written by v2.6.56 still
    list.

## Decisions

Taken on 2026-11-02:

1. **ROCm is the default backend on Strix Halo**, with Vulkan as
   `--backend vulkan`.
2. **A local server comes back after a reboot** by default; `--max` is for
   one that should end.
3. **A shared server's key is enough.** Who may connect over the VPN is for
   the IT admins to set in NetBird's access control.
4. **`hi model` is `hi serve`.** One command for everything served, with no
   `hi model`. Each of its verbs has a `hi serve` form, and models are one
   kind among others.
5. **Kinds are code, recipes are data.** New software of an existing kind
   is a new recipe file. A new kind, which changes what hi checks and
   secures, comes with a hi release.
6. **Every served box is reached through a front**, on a network with no
   way out. That gives one place for keys and access lines for every
   kind.
7. **Local servers are kept when stopped.** `stop` keeps the port, the
   key, and later a database's data; `rm` forgets them.
8. **Podman only, with Quadlet units.**

## Open questions

1. Whether the front's access lines also go to the org server's audit log
   when the server is named to the org (release 3).

## Findings

None yet; see "Verify before building".
