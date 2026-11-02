---
title: Serve a model
description: Run a GGUF model with llama.cpp on a rented GPU or on this machine's Strix Halo, and use it through an OpenAI-compatible API from curl, Python, or any OpenAI-compatible tool.
---

`hi serve llm` starts [llama.cpp](https://github.com/ggml-org/llama.cpp)'s
server with a GGUF model, waits until it answers, and gives you an
OpenAI-compatible URL. It runs on a rented GPU, or with `--on local` on this
machine's GPU (a Strix Halo, so far). It takes a tested recipe or any GGUF
repository on the Hugging Face Hub. `hi compute serve` is another name for
it, with the same options.

## With a recipe

Recipes pick the model file, hardware, context length, and server flags that
have been tested together:

```sh
hi serve llm qwen3.8-flash-next --on colab --max 1h
```

| Recipe               | Model                                      | Hardware                             | Measured                     |
|----------------------|--------------------------------------------|--------------------------------------|------------------------------|
| `qwen3.8-flash-next` | `unsloth/Qwen3.8-Flash-Next-GGUF`, `UD-Q3_K_XL` (90 GB), 131k context | Colab `G4`, Hugging Face `rtx-pro-6000` (96 GB), Strix Halo (`--on local`, 128 GB) | Colab G4: ready in ~7 min, ~84 tokens/s. Strix Halo: ready in 29 s from disk, 24 tokens/s |

`hi serve llm` alone, or `hi serve recipes`, lists the recipes, where each
runs, and whether it fits on this machine.

[Try a 125B model on a 96 GB GPU](/guide/tutorials/big-model/) walks through
this one.

## With any GGUF model

Name a Hugging Face repository that contains GGUF files, a quantization that
exists in it, and hardware with enough memory:

```sh
hi serve llm unsloth/Qwen3-8B-GGUF --quant Q4_K_M --gpu L4 --max 1h
hi serve llm unsloth/Qwen3-0.6B-GGUF --quant Q4_K_M --gpu t4-small --max 30m
```

Choose hardware whose GPU memory is at least the model file's size plus a few
GB for the context. A 5 GB `Q4_K_M` file fits a 16 GB T4 comfortably; a 90 GB
file needs a 96 GB GPU.

| Option            | Meaning                                                     |
|-------------------|-------------------------------------------------------------|
| `--quant <quant>` | The quantization in the file names, such as `Q4_K_M` or `UD-Q3_K_XL` |
| `--gpu <hardware>`| GPU hardware; recipes choose their own                       |
| `--ctx <tokens>`  | Context length; default 32768 (recipes set their own)        |
| `--alias <id>`    | Model name in the API; default from the repository name      |
| `--args "<args>"` | Extra `llama-server` arguments                               |
| `--port <port>`   | Local port for the tunnel; default 8080                      |
| `--max`, `--name`, `--on`, `--namespace`, `--reason`, `--yes`, `--dry-run` | As for `hi compute up` |

## On this machine

On a Strix Halo with 128 GB, a recipe serves on the machine you're on:

```sh
hi serve detect                                   # what this machine can serve on
hi serve llm qwen3.8-flash-next --on local
```

```text
  detect     Strix Halo (gfx1151), /dev/kfd and /dev/dri, Podman 5.7.0 with crun; 120 GB available, teams' boxes may still take 0 MB
  check      it needs 91.0 GB with its context: fits, 21.3 GB left; 83.8 GB to download, 801 GB free
  download   unsloth/Qwen3.8-Flash-Next-GGUF:UD-Q3_K_XL at 766911a into ~/.local/share/hi/models/unsloth--Qwen3.8-Flash-Next-GGUF
  pull       ghcr.io/ggml-org/llama.cpp:server-rocm@sha256:1bc7c96d… (once; ROCm images are about 23 GB)
  start      hi-serve-qwen3-8-flash-next: a box with the GPU, no way out, the model read-only, behind its front on 127.0.0.1:8080
  [15:30:22] loading the model
  [15:30:51] ready after 29s
  ready      OpenAI-compatible API: http://127.0.0.1:8080/v1   model: qwen3.8-flash-next
```

1. **Detect.** It looks for the evidence: a GPU the kernel lists (gfx1151),
   `/dev/kfd` and `/dev/dri`, the render and video groups, and rootless
   Podman 5 with `crun`. Anything missing is named with the `hi install`
   that adds it (`hi install strix`, `hi install podman`), and nothing starts.
2. **Check.** The recipe says how much memory the model takes in all, GPU
   and host, as measured: 91 GB for this one. It must fit in what is
   available, less what teams' boxes may still take under their limits and
   8 GB of headroom, and the download must fit on the disk with 10% to
   spare. If not, hi says by how much, before downloading anything.
3. **Download.** hi downloads the files itself, from the commit the recipe
   pins, and checks each against the Hub's SHA-256. A broken download
   resumes when you run the command again; a partial file is never served.
   Models are kept in `~/.local/share/hi/models`, so the next start doesn't
   download them again.
4. **Start.** The server runs in a box with the GPU, on a network with no
   way out, with no capabilities, a read-only root, and the model mounted
   read-only. Its only way in is a small front, hi itself in a second
   container, which publishes the API on `127.0.0.1`. Both are systemd user
   units, which Podman makes from files hi writes in
   `~/.config/containers/systemd/`.

It keeps running after you log out and comes back after a reboot, until you
stop it, if your account lingers. If it doesn't, hi says so with the command
an admin runs: `sudo loginctl enable-linger <you>`.

`--max 4h` stops it after that long, also if the machine was off then. On
your own machine there is no limit by default.

| Option            | Meaning                                                     |
|-------------------|-------------------------------------------------------------|
| `--port <port>`   | Its port on `127.0.0.1`; default 8080, or the next free one. It keeps it |
| `--memory <size>` | The box's memory limit, such as `72G`; default the recipe's |
| `--backend rocm`  | The GPU backend; ROCm, with Vulkan later                     |
| `--share`         | Also on this machine's NetBird address, with a key to hand over (below) |
| `--org`           | Also on the NetBird address, for the org server's `hi org ai` (below) |
| `--name <name>`   | Its name; default from the recipe, such as `qwen3-8-flash-next` |
| `--ctx`, `--alias`, `--args`, `--quant`, `--max`, `--yes`, `--dry-run` | As above |

`--dry-run` checks the machine and the memory, and prints the download, the
image, and the units it would write, and changes nothing.

### Teams come first

The machines that serve models also run teams. A served box is the first
thing the kernel stops when memory runs out, so a team's process is never
killed to make room for a model. A box stopped that way stays stopped, and
`hi serve ls` says `stopped: out of memory`; `hi serve start <name>` checks
the memory again before it loads the model.

On a Strix Halo the model's GPU memory isn't counted in the box's memory
limit: the limit bounds the host side, and the check before the start bounds
the whole.

### Stop, start, and remove

```sh
hi serve stop qwen3-8-flash-next     # stops it and keeps it; it doesn't start at boot
hi serve start qwen3-8-flash-next    # back on the same port, after the memory check
hi serve rm qwen3-8-flash-next       # stops it and forgets it; the model stays
hi serve models                      # models downloaded here, and what uses them
hi serve models rm unsloth/Qwen3.8-Flash-Next-GGUF
hi serve pull llm qwen3.8-flash-next # download a model and its image now, serve nothing
```

`hi serve models rm` is refused while something served uses the model.
`hi update` doesn't restart a served model: its units run Podman, not hi.
It offers to restart a front that still runs the old hi, which drops open
connections and nothing else.

A model in your team's private Hugging Face organizations comes through the
org server, as `hi data` does, so no Hugging Face token is on the machine.

### Share it with a teammate

On `127.0.0.1` a local server takes no key, as llama.cpp doesn't. `--share`
also publishes it on this machine's NetBird address, where the front asks
for a key:

```sh
hi serve llm qwen3.8-flash-next --on local --share
```

```text
Shared on this machine's NetBird address. Hand a teammate these two, as you would any API key:
  OPENAI_BASE_URL=http://100.70.1.2:8080/v1
  OPENAI_API_KEY=hi-serve-…
```

Handing it over is by hand: send both to your teammate, who puts them in
their client. Nothing of hi runs on their side. Their device reaches yours
if NetBird's access control lets it, which your IT admins set.

```sh
hi serve key qwen3-8-flash-next          # print the key again
hi serve key qwen3-8-flash-next --new    # a new key; the old one stops working within seconds
```

The key is in `~/.config/hi/serve/<name>/`, readable only by you. Only the
front reads it; the server never sees it. `hi serve logs <name>` shows a line
for each request, with which key it came with, the path, the status, and how
long it took:

```text
access 2026-11-02T19:59:54Z shared POST /v1/chat/completions 200 69ms
access 2026-11-02T19:59:23Z no-key GET /v1/models 401 0s
```

### Serve it to the org

With an org server, `--org` names the model to it instead, so nobody hands
keys around:

```sh
hi serve llm qwen3.8-flash-next --on local --org --reason "bulk tagging for the team"
```

```text
Asked the org server to add it to hi org ai as model local/qwen3-8-flash-next (s-32a15f); an admin approves it in Slack.
```

Once an admin approves it, devices that joined the org use it through
`hi org ai` with their own sign-in, and the org server holds the key:

```sh
hi q --model local/qwen3-8-flash-next "show the free disk space"
```

Its use shows in `hi org spend` at no cost, and the access lines name the
user (`org:alice`). `hi serve status` says whether it is approved. `stop`
marks it stopped for the org, `start` marks it up again, and `rm` removes it
from the org. `--org` and `--share` go together if you also want a key to
hand over.

## What happens on each provider

### Colab, RunPod, and Shadeform

Recipes name hardware for Colab and Hugging Face only; on RunPod or
Shadeform, add `--gpu`.

1. `hi` starts the machine, uploads a setup script over SSH, and runs it in the
   background, so a dropped connection never stops setup.
2. The script builds llama.cpp with CUDA and downloads the model in parallel.
   `hi` prints each step as it changes:
   ```text
     [19:57:04] building llama.cpp and downloading unsloth/Qwen3.8-Flash-Next-GGUF:UD-Q3_K_XL  (model on disk: 4.5G)
     [19:58:58] llama.cpp built, downloading …  (model on disk: 39G)
     [20:01:24] loading model  (model on disk: 84G)
     [20:03:57] ready
   OpenAI-compatible API: http://127.0.0.1:8080/v1   model: qwen3.8-flash-next
   ```
3. It then tunnels the API to `http://127.0.0.1:8080/v1` and stays in the
   foreground. Press Ctrl+C to close the tunnel; `hi` asks whether to stop the
   machine.

If setup fails or you close the terminal, run the same `hi serve llm` command
with the same `--name` again: the script skips what is already done.

### Hugging Face

1. `hi` starts one job that is the server, using llama.cpp's official CUDA
   image. `llama-server` downloads the model itself.
2. When the server answers, `hi` prints an HTTPS URL and returns:
   ```text
     [20:34:55] downloading and loading unsloth/Qwen3-0.6B-GGUF:Q4_K_M
     [20:35:36] ready
   OpenAI-compatible API: https://6ab97de2…--8000.hf.jobs/v1   model: qwen3-0.6b-gguf
   It needs your hf token as the API key. It stops after 30m at 21:05.
   Local tunnel instead: hi compute tunnel t4-small-3fa1 8000:8080
   Stop it:              hi serve stop t4-small-3fa1
   ```
3. The URL works from anywhere, but only with a Hugging Face token that can
   read the account that pays; without one, it answers `401`. Use your token
   as the API key.

To use it only from your laptop instead, tunnel it:
`hi compute tunnel <name> 8000:8080`.

## Use the API

The server speaks the OpenAI chat completions API.

```sh
curl http://127.0.0.1:8080/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -d '{"model": "qwen3.8-flash-next", "messages": [{"role": "user", "content": "Hello"}]}'
```

For a Hugging Face URL, add the token as a header:

```sh
export HF_TOKEN=$(hf auth token)
curl https://<job>--8000.hf.jobs/v1/models -H "Authorization: Bearer $HF_TOKEN"
```

From Python with the `openai` package:

```python
import os
from openai import OpenAI

client = OpenAI(
    base_url="http://127.0.0.1:8080/v1",  # or the https://…hf.jobs/v1 URL
    api_key=os.environ.get("HF_TOKEN", "unused"),
)
reply = client.chat.completions.create(
    model="qwen3.8-flash-next",
    messages=[{"role": "user", "content": "Explain compute units in one sentence."}],
)
print(reply.choices[0].message.content)
```

Most OpenAI-compatible tools accept the same two settings, often as
environment variables:

```sh
export OPENAI_BASE_URL=http://127.0.0.1:8080/v1
export OPENAI_API_KEY=unused      # or your Hugging Face token for an hf.jobs URL
```

> Reasoning models such as Qwen3 think before answering. The thinking arrives
> in `reasoning_content` and counts toward `max_tokens`, so give them room
> (for example `"max_tokens": 2000`) or the answer may be empty.
{: .tip}

## See what you serve

```sh
hi serve ls
```

```text
NAME                WHERE  MODEL               API                                 STATE    STOPS IN
t4-small-3fa1       hf     qwen3-0.6b-gguf     https://6ab97de2…--8000.hf.jobs/v1  running  27m
qwen                colab  qwen3.8-flash-next  tunnel                              running  52m
qwen3-8-flash-next  local  qwen3.8-flash-next  http://127.0.0.1:8080/v1            running  no limit
```

A model on Colab, RunPod, or Shadeform is reached through its tunnel, so
its API works while the tunnel runs; `hi compute tunnel <name> 8000:8080`
opens it again. `hi serve ls --json` gives the same list, with the
repository and quant of each. `hi serve status <name>` shows one: its
address, state, memory, and image.

## Watch and troubleshoot

```sh
hi serve logs <name>              # setup, download, and server logs
hi serve logs <name> --follow
hi compute status <name>
```

| Symptom                                    | Likely cause                                                  |
|--------------------------------------------|---------------------------------------------------------------|
| `failed: download`                         | The repository does not exist, or the model is gated and needs a token |
| `failed: no Q4_K_M .gguf file in …`        | That `--quant` does not exist in the repository               |
| `failed: llama-server exited`              | The model does not fit in GPU memory; choose a smaller quant or larger GPU |
| `serving needs a GPU`                      | `--gpu` names CPU hardware                                    |
| `Address already in use`                   | Local port 8080 is taken; add `--port 8090`                   |
| `401` from an `hf.jobs` URL                | Missing or wrong token in the `Authorization` header          |

## Stop

A model server on rented hardware keeps costing money until it stops:

```sh
hi serve stop <name>
```

`hi serve stop` stops only machines that serve a model; `hi compute stop`
stops any. On this machine, `hi serve stop` keeps the server to start again;
see [Stop, start, and remove](#stop-start-and-remove).
