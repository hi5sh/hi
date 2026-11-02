# `hi serve llm --on local`: a model on this machine

Status: Draft (2026-11-02)

Dependencies: [hi_org.md](../approved/hi_org.md) (`hi serve llm`, `ls`,
`logs`, `stop`, and what `--on` means), [hi_compute.md](../approved/hi_compute.md)
(the recipes in `compute_serve.go`), [hi_box.md](../approved/hi_box.md)
(rootless Podman, hardening, `--gpu`), [hi_data.md](../approved/hi_data.md)
(the team's private models through the org server),
[hi_server_ai.md](../approved/hi_server_ai.md) (the org's model
pass-through), [hi_compute_serve_pinning.md](hi_compute_serve_pinning.md)
(one pinned llama.cpp per recipe), [hi_llm.md](hi_llm.md) (named upstreams,
`--model local`), and [hi_cluster.md](hi_cluster.md) (`--on <machine>`
later). It takes in [hi_model.md](hi_model.md), which stays as background.

## Goal

The team's Strix Halo machines have 128 GB of memory that a GPU can use,
and models are served on them today by hand: `halogen-qwen3.8-flash-next`
on gpu2 is started outside hi, and nothing in hi knows it is there. `hi
serve llm --on local` serves a model on the machine you're on, the way `hi
serve llm --on colab` serves one on a rented GPU.

- **The same command and recipes.** A recipe that runs on Colab also says
  how it runs on a Strix Halo, and `hi serve ls`, `logs`, and `stop` show
  local models next to rented ones.
- **Safe to leave running.** The server runs in a box with no network and
  no credentials, the model mounted read-only, and an API on `127.0.0.1`
  only, unless you share it.
- **Teams first.** A model never takes memory a team's load needs, and
  never makes the kernel kill a team's process to fit.
- **A service, not a terminal.** It keeps running after you log out and
  after a reboot, until you stop it or its `--max` ends.
- **Later, the org knows.** A shared model can be named to `hi org ai`, so
  `hi q` and `hi llm --model local` use it without anyone copying a URL.

## Commands

```text
hi serve llm <recipe | owner/repo-GGUF --quant Q> --on local [options]
    --port <port>          API port on 127.0.0.1 (default: 8080, or the next free one)
    --ctx <tokens>         context length (recipes set their own)
    --memory <size>        the box's memory limit (default: the recipe's need plus headroom)
    --backend vulkan|rocm  default: the recipe's tested one for this machine
    --max <duration>       stop after this long (default: none; it's your machine)
    --share                also listen on this machine's NetBird address, with a key
    --name, --alias, --args, --yes, --dry-run   as on rented hardware

hi serve detect            what this machine can serve on: GPU, backend, free memory, what teams use
hi serve models [--json]   models downloaded here, their size, and which servers use them
hi serve models rm <repo>  delete a downloaded model that no server uses
hi serve key <name>        print a shared server's API key
```

`hi serve ls`, `logs`, and `stop` work on local servers as they do on
rented ones, with `local` in the `PROVIDER` column and
`http://127.0.0.1:<port>/v1` as the API.

## What happens

```text
 hi serve llm qwen3.8-flash-next --on local
   1. detect     Strix Halo (gfx1151), /dev/kfd and /dev/dri, Vulkan; 104 GB free, teams use 18 GB
   2. check      the recipe needs ~95 GB with its context: fits, 9 GB left
   3. download   unsloth/Qwen3.8-Flash-Next-GGUF:UD-Q3_K_XL into ~/.local/share/hi/models, verified
   4. start      hi-serve-qwen3-8-flash-next.service: a box with the GPU, no network, the model read-only
   5. wait       until /health answers, printing each step
   6. ready      OpenAI-compatible API: http://127.0.0.1:8080/v1   model: qwen3.8-flash-next
```

1. **Detect.** As hi_model.md asked: evidence, not names. A Strix Halo is
   `gfx1151` with `/dev/kfd`, `/dev/dri`, and the render and video groups
   (what `hi verify strix` checks), and a Vulkan device that `vulkaninfo`
   lists. Anything missing is reported with the `hi install` command that
   fixes it; nothing is guessed.
2. **Check memory.** On Strix Halo the GPU's memory is system RAM, so the
   need is the model file plus the context's cache, from the recipe, and
   the room is `MemAvailable` less what the teams on the machine reserve
   and a headroom of 8 GB. If it doesn't fit, hi says by how much and
   starts nothing. `--yes` doesn't override it.
3. **Download on the host, not in the box.** hi downloads the GGUF files
   from the Hub itself, resumably, into `~/.local/share/hi/models/<owner>--<repo>/`,
   and checks each file's SHA-256 against the Hub's. A partial download is
   kept as partial and never served. A model in the team's private
   organizations comes through the org server (`hi data`), so no Hugging
   Face token is on the machine. A model already there is used as it is.
4. **Start a box as a systemd user service.** `hi-serve-<name>.service`
   runs Podman directly, with the box's hardening (no capabilities, no
   privilege gain, a pids limit), the GPU devices from `hi box --gpu`, the
   model folder mounted read-only, `--network none`, and the API published
   on `127.0.0.1:<port>` only. The service's command is Podman, not hi, so
   `hi update` never restarts a model server. `Restart=on-failure`, enabled
   so it comes back after a reboot, with `RuntimeMaxSec` when `--max` is
   set.
5. **Wait** for llama.cpp's `/health`, printing the steps from its log, as
   on rented hardware, within the same 45 minutes.

`--dry-run` does steps 1 and 2, then prints the download, the image, the
mounts, the devices, the unit, and the endpoint, and changes nothing.

## Teams first

The machines that serve models also run teams, and a team's load is never
interrupted.

- The box gets a hard memory limit (`--memory`, from the recipe) and
  `oom_score_adj=1000`, so under pressure the kernel kills the model server
  before anything of a team's.
- What teams reserve comes from their boxes' memory limits, as `hi serve
  detect` shows. A team started after the model may still need room; hi
  doesn't stop the model for it, but `hi team up` warns when a model
  server leaves too little, naming the server and `hi serve stop`.
- There is no queue and no eviction: a model that doesn't fit is refused.

## Images and recipes

One llama.cpp build per recipe, pinned by image digest
(hi_compute_serve_pinning.md), with one image per backend:
`ghcr.io/ggml-org/llama.cpp:server-vulkan-<build>@sha256:…` and
`server-rocm-<build>@sha256:…`. A recipe gains a `local` entry per
backend, with its own flags and its own tested line:

```go
"qwen3.8-flash-next": {
    ...
    hardware: map[string]string{"colab": "G4", "hf": "rtx-pro-6000"},
    local: map[string]serveLocal{
        "vulkan": {memory: 95 << 30, args: "…", tested: "gpu2 (Strix Halo), 2026-11-…: … tokens/s, ready in … min"},
    },
},
```

An ad hoc model (`owner/repo-GGUF --quant Q`) runs on the default backend
with no tested line, and its need is the file size plus a context estimate.
Extra `--args` are allowed, as on rented hardware, and shown in the
dry run.

## Sharing

`--share` also publishes the API on this machine's NetBird address, so a
teammate's device on the VPN can use it. A shared server always has a key:
hi makes one, keeps it at `~/.config/hi/serve/<name>.key` (0600), passes it
to llama.cpp from a file, never as an argument, and prints it once; `hi
serve key <name>` prints it again. The address and the key are what `hi q
--setup` or any OpenAI-compatible client needs. Public addresses are not
offered; `hi net expose` stays the tool for that, with its own rules.

## The org knows (release 3)

A shared model can be named to the org:

```text
hi serve llm qwen3.8-flash-next --on local --share --org
```

The device asks the org server to add it as a named upstream of `hi org ai`
(the named upstreams of hi_llm.md), with its NetBird address and key; an
admin approves it in Slack, as with any request. Devices then reach it
through the org server, signed, and its use shows in `hi org spend` at zero
cost. `hi serve stop` removes it from the org.

## Releases

1. **Local on Strix Halo.** `--on local` with the Vulkan backend, `hi serve
   detect`, the host download into the models folder, the box and its unit,
   the memory check, `ls`, `logs`, `stop`, and `hi serve models`. The
   qwen3.8-flash-next recipe tested on a Strix Halo that runs no production
   teams, against halogen's numbers on gpu2.
2. **Sharing.** `--share` and `hi serve key`.
3. **The org knows.** `--org`, with hi_llm.md's named upstreams.
4. **More backends.** ROCm on Strix Halo, then NVIDIA through Podman's CDI
   devices, each with its own tested line.
5. **On the org's machines.** `--on <machine>` and `--on auto` through `hi
   org machines` (hi_cluster.md, release 3); the same box and unit, placed
   by the org server.

## Acceptance criteria

1. On a machine without a usable GPU, `hi serve detect` and `hi serve llm
   --on local` say what is missing and which `hi install` adds it, and
   start nothing.
2. A model that doesn't fit in what teams leave is refused before any
   download, with the numbers.
3. `--dry-run` downloads, pulls, creates, and starts nothing.
4. Downloads resume, every file is checked against the Hub's SHA-256, and
   a partial file is never served.
5. The server's box has no network, no capabilities, the model read-only,
   and its API on `127.0.0.1` only unless `--share`; a shared API refuses
   requests without its key, and the key is in no process's arguments.
6. Under memory pressure the model server is killed before any team's
   process.
7. The server survives logout and reboot, stops at `--max`, and `hi update`
   leaves it running.
8. `hi serve ls`, `logs`, and `stop` show and stop local and rented models
   alike, and `stop` touches only units and containers that hi serve
   labeled as its own.
9. Deleting a model with `hi serve models rm` is refused while a server
   uses it.

## Open questions

1. Vulkan or ROCm as the default on Strix Halo? Proposed: Vulkan, which is
   what llama.cpp users report as faster and steadier on gfx1151 today;
   release 1 measures both on one recipe before deciding.
2. Should a local server survive a reboot by default, or only with a flag
   such as `--keep`? Proposed: by default, since a model on the team's
   machine is usually meant to stay; `--max` is there for the rest.
3. Should hi adopt the model halogen already serves on gpu2, or leave it
   until someone moves it to `hi serve` by hand?
4. Is a shared server's key enough on the VPN, or should `--share` also
   limit who may connect through NetBird's access control?
