---
title: Managed compute for your team
description: Run hi org so employees, students, and their agents use GPUs through one set of provider keys, with every start approved and every machine stopped at its limit.
---

Your team can run `hi org` on a small always-on machine inside its NetBird
network. The server holds the provider keys, makes every provider call itself,
and starts nothing until someone approves it. People join with `hi org join`
and then use the same `hi compute` commands as before.

- Nobody but the admins holds a provider key, so nobody can spend outside
  `hi`, and taking someone's access away is one command.
- Every paid start is approved by a person who is not the requester.
- The server stops every machine at its `--max`, even when the laptop that
  asked for it is off.

> Management is opt-in. If you never run `hi org join`, `hi` works exactly as
> before, with your own keys. Colab is never managed: it always runs under
> your own Google sign-in.
{: .note}

hi manages RunPod and Shadeform. Admins approve from Slack or the
[admin page](/guide/app/admin/) on the server's machine, groups can have
budgets, and `hi compute live` and the admin page show what is running.

[See it in action](/org/): a short demo, then how it works.

{% include diagram.html name="managed" %}

## For users

### Join the server

Your admin tells you the server's name, such as `compute.internal`. On a
machine that is already in the NetBird network:

```sh
hi org join compute.internal
```

```text
Asked http://compute.internal:7373 to let laptop-alice join as alice (request e-3f09a1).
An admin approves it with: hi org approve e-3f09a1 --group <group>
Waiting for approval… (Ctrl-C stops waiting; the request stays open)
Connected to http://compute.internal:7373 as alice (students).
Managed by the server: runpod. Starting there needs an approval.
Colab and any other provider still use your own sign-in.
```

`hi org join` creates a key for this device in `~/.config/hi/device_key`,
readable only by you, and signs every request to the server with it. Your
user name defaults to your login name; choose another with `--user`.

### Start a machine

```sh
hi compute up --on runpod --gpu rtx-4090 --max 2h --reason "LoRA sweep for my thesis"
```

```text
Start runpod/rtx-4090-1c2d on rtx-4090 ($0.89/h), stopping after 2h at 16:05.
Start it? [y/N] y
Sent request r-9b41e0 to http://compute.internal:7373.
Waiting for approval… (Ctrl-C stops waiting; the request stays open)
Approved by bob.
Starting: Created pod 7xk2q9; waiting for it to start (this can take a few minutes)...
Starting: running
rtx-4090-1c2d is running.

rtx-4090-1c2d is up. Next:
  hi compute ssh rtx-4090-1c2d
  hi compute tunnel rtx-4090-1c2d 8000
  hi compute stop rtx-4090-1c2d
```

Two things differ from using your own key:

- **A reason and a time limit are required.** Approvers see the reason, and
  `--max none` is refused. Without `--reason`, `hi` asks for one.
- **You wait for a person.** If you stop waiting, the request stays open.
  Check it later with `hi compute requests`. A request with no decision
  expires after 30 minutes.

If the approved hardware is sold out by the time it starts, the approval
still counts. The server starts the cheapest free hardware with at least as
much memory, costing at most twice the approved price or the approved price
plus $1.00/h, whichever is higher, and says so:

```text
rtx-4090-1c2d is running on rtx-3090 ($0.50/h); the approved l4 ($0.49/h) was sold out.
```

After that, `ssh`, `tunnel`, `logs`, `ls`, and `stop` work as usual.
`hi compute run` doesn't go through the server yet: start a machine with
`up` and run your script over `hi compute ssh`. The
machine accepts the SSH key from your `~/.ssh`, and connections go straight
to it, not through the server.

### Stop it

```sh
hi compute stop rtx-4090-1c2d
```

Stopping never needs approval. You can stop your own machines only;
`hi compute stop --all` stops all of yours and nobody else's. If you forget,
the server stops the machine at its `--max`.

### Ask for more time

```sh
hi compute extend rtx-4090-1c2d 2h --reason "needs two more epochs"
```

An extension goes through the same approval as a start. Once approved, the
machine's limit moves, and `hi compute ls` shows the new time.

### Budgets

If your group has a monthly budget, `hi org status`, `hi compute ls`, and
every request show what you have spent this month:

```text
This month: $42.10 of noor's $100.00 · staff $120.00 of $300.00
```

Going over a budget never blocks you. The request shows a warning, the
approvers see it too, and it waits for a person even if the policy would
otherwise approve it by itself.

### Check your requests

```sh
hi compute requests                    # your recent requests
hi compute requests r-9b41e0           # one request
hi compute requests r-9b41e0 --wait    # wait for the decision and the start
```

| Exit status | Meaning                                  |
|-------------|------------------------------------------|
| 0           | Running, or already stopped              |
| 3           | Still waiting for approval               |
| 4           | Denied; the message has the reason       |
| 1           | Failed to start, or expired              |

### Agents

Coding agents use the same commands and the same approval. An agent working
on your laptop acts as you. Agents can't be prompted and have time limits on
their commands, so they use:

```sh
hi compute up --on runpod --gpu rtx-4090 --max 1h --reason "eval run" --yes --no-wait
hi compute requests r-9b41e0 --wait --timeout 10m
```

`--no-wait` returns at once with exit status 3 and the request ID. Nobody can
approve their own request, so an agent can't approve its own either. The
[agent skill](/guide/reference/skill/) teaches agents this flow.

An agent that runs on its own, such as a scheduled job on a build server,
joins as itself with a person responsible for it:

```sh
hi org join compute.internal --agent build-bot --owner noor
```

Approvers see it as 🤖 **build-bot** (owner noor), with an *Approve as
agent* button that puts it in the `agents` group. Requests from an agent on
someone's laptop show as `noor via Claude Code`; set `HI_AGENT` to name any
other agent.

### Link your Slack account

Link your Slack account to get a message when your request is approved,
denied (with the reason), started, near its time limit, or stopped, and to
see and stop your machines from the app's Home tab in Slack:

1. In Slack, type `/hi link <your hi user name>`, such as `/hi link noor`.
2. Run the command it replies with on your own device, within 10 minutes:

   ```sh
   hi org slack link 4F7K2Q
   ```

The code only works from a device that is connected as that user, so nobody
can link themselves to someone else. `/hi unlink` undoes it.

### Watch your machines live

```sh
hi compute live
```

A full-screen view of your managed machines: running time, cost so far, a
bar towards each time limit, your requests, and your budget. Select a
machine with the arrow keys and press `s` to stop it, or `q` to quit.

### Use your team's model with hi q

When the server serves a model, [`hi q`](/guide/q/) uses it without a key or
any setup, and `hi q --setup` lists it first. Your choice of model is
yours; the server passes requests to the upstream with your team's key.
`hi q --status` shows `… via <server>`.

If the server can't be reached, `hi q` falls back to your own key or Claude
Code, if you have one, and says so in a dim line. Without one, it stops
until the server is back.

### Download your team's data

When the server serves your team's Hugging Face organizations,
[`hi data`](/guide/data/) lists and downloads their private datasets,
models, and buckets with no Hugging Face token on your machine.
`hi compute run --data` gives a cloud job the same data, and
`hi compute up --data` a RunPod or Shadeform machine.

### Share a local service

[`hi net expose`](/guide/workstation/expose/) works without a server, but on
a connected machine each exposure is recorded: the server's audit log and
live feed show who exposed which address, from which port, with which
lock, and until when.

### Leave the server

```sh
hi org leave
```

This deletes the device key and the server address. `hi compute` then uses
your own keys again. Machines you started through the server keep its limits
until they stop.

## For admins

{% include diagram.html name="server" %}

### Set up the server

On a small always-on Linux machine, enrolled in NetBird with `hi net`:

```sh
hi org init --owner U0123456789 --email olga@example.com
hi org provider add runpod     # asks for the key without echoing it
hi org provider add shadeform  # the same for Shadeform, if you use it
hi admin start                 # the admin page, where the owner and the admins run the org
```

`hi org init` listens on this machine's NetBird address, port 7373, and
starts the server as a user service, `hi-org.service`, beside the admin
page and any apps. It keeps its state, audit log, and provider keys in
`~/.local/state/hi/org`, readable only by the account it runs as. Change
that with `--dir` or `HI_ORG_DIR`; with `--dir` it prints the service
instead of starting it. On a machine that doesn't keep the account's
services running without a login, it says the one command to change that
(`sudo loginctl enable-linger <user>`).

**On a machine with apps:** the org server can run beside them, for a
small org or to try one. `hi org init` there refuses while the machine is
joined to another org (`hi org leave` first), joins the machine to the
new org under the owner's hi user name, so its apps' compute goes through
it, and keeps the admin page's admins apart from the org's
([admin page](/guide/app/admin/#on-the-org-servers-machine)). The org
server keeps the keys that spend money, and the apps' agents run beside
it as the same account: for more than a few people, give it a small
machine of its own.

### The org's owner and admins

Every org has an owner: one person, with their Slack member ID and their
email. `hi org init` asks for them at a terminal, or takes `--owner` and
`--email`, and makes no org without them. The owner is the first approver
and the first admin of the [admin page](/guide/app/admin/) on the server's
machine. Their hi user name is the first part of their email, unless
`--owner-name` says another.

```sh
hi org admins                                    # the owner and the admins
hi org admins add U0123456789 --name sam [--email sam@example.com]
hi org admins remove sam
hi org owner U0987654321 --email noor@example.com   # hand the org on
```

An admin is an approver too, in Slack and on the admin page, and signs in
to the admin page on the server's machine: one list, so that page keeps
none of its own. The owner adds and removes them on the page; on the
machine, anyone logged in as the account the server runs as can. Handing
the org on keeps the owner before as an admin, and is said in the
approvers' channel. Every change is in the audit.

An org made before owners has none: `hi org status` on the server's
machine and the admin page say so, with the command, `hi org owner <Slack
member ID> --email <address>`. Until then, the first `hi admin start` there
asks for them.

It listens only on its NetBird address. Clients sign every request, so a
forged, altered, or replayed request is refused, even from inside the VPN.

### Update the server

As the account the server runs as:

```sh
hi update
```

After installing the new binary, `hi update` finds the running server, which
still runs the old one, and asks to restart its service with
`systemctl --user restart hi-org.service`. It is down for about a second.
It also asks when the binary is already current but the server was never
restarted. `--restart` restarts without asking, for scripts, and
`--no-restart` only prints the command. A server started by hand, outside
systemd, is reported but not restarted.

State, keys, and devices carry over; connected devices need no change.
Devices on an older `hi` keep working, and get new features when they run
`hi update` themselves.

If systemd warns that `hi-org.service` changed on disk, compare
`systemctl --user cat hi-org` with what you meant to change, then run
`systemctl --user daemon-reload` and restart. The reload alone doesn't
restart the server.

#### From a system service to a user service

Until hi 2.6.78 the org server ran as a system service, in
`/etc/systemd/system`, so restarting it needed `sudo`, which the admin page
can't do. On a terminal, `hi update` offers to move it, with `sudo` this
once: it writes `hi-org.service` in `~/.config/systemd/user` from the system
unit (without `User=`, and anything else you added kept), keeps the
account's services running without a login (`loginctl enable-linger`),
stops the system unit and starts the user one, then removes the system
unit. If the user service doesn't come up, the system unit runs again.
Without a terminal, or with `--no-restart`, it only says so, and the server
runs as before.

#### From `hi-server.service` to `hi-org.service`

A server set up before hi 2.6.57 runs as `hi-server.service`, with its
state in `~/.local/state/hi/server`. On a terminal, `hi update` offers to
move it: it writes `hi-org.service` from the old unit, with `HI_ORG_DIR` and
`org run` in place of `HI_SERVER_DIR` and `server run` and anything else
you added kept, then disables the old unit and starts the new one. On
start, the server moves its state to `~/.local/state/hi/org` and leaves a
link at the old path. If the new unit doesn't come up, the old one is
started again. The old unit stays on disk, disabled, until you remove it.

Until you move it, everything keeps working under the old names, and
`hi update` asks again each time. A state folder of your own choosing
(`--dir`, or `HI_SERVER_DIR` set to another path) stays where it is.

### Approve and deny

Admin commands talk to the running server through a socket that only the
server's user can open, so they work on the server box:

```sh
hi org requests                            # what is waiting
hi org approve e-3f09a1 --group students   # a new device; new users need a group
hi org approve r-9b41e0                    # a compute request
hi org deny r-9b41e0 --reason "use a 4090, not an H100"
```

Decisions are recorded under your login name; change it with `--as`. Nobody
can decide their own request. To add your own device, pre-approve its key:
run `hi org key` on the device, then:

```sh
hi org user add alice --group staff --key <key>
```

### Approve from Slack

The server can post every request to a private Slack channel, with buttons
to approve, deny, and stop. It connects to Slack over Socket Mode, which is
an outbound connection, so the server needs no public address.

1. At [api.slack.com/apps](https://api.slack.com/apps), choose **Create New
   App → From a manifest** and paste the output of
   `hi org slack manifest`. Install it to your workspace.
2. Create a private channel, such as `#compute-approvals`, and invite the
   app with `/invite @hi org`.
3. On the server box, run `hi org slack setup`. It asks for an **App-Level
   Token** with `connections:write` (`xapp-…`, under Basic Information),
   the **Bot User OAuth Token** (`xoxb-…`), and the channel's ID (at the bottom
   of the channel's details). It posts a test message, then saves them.
4. Add each approver by Slack member ID (profile → ⋮ → Copy member ID) and
   the `hi` user name they request compute as:

   ```sh
   hi org approvers add U0123456789 --name bob
   ```

   The org's owner and admins are approvers already
   ([above](#the-orgs-owner-and-admins)); this is for someone who only
   approves.

5. Restart the server: `systemctl --user restart hi-org`, or **Restart** on
   the admin page (Org).

An app made before hi 2.6.57 is called *hi compute*. It works as it is; to
rename it, change its name and the bot's display name under **App Home** at
api.slack.com.

In the channel:

- **Join requests** have *Approve as staff*, *Approve as student*, and
  *Deny…* buttons.
- **Compute requests** show the user, hardware, price, maximum cost, and
  reason, with *Approve* and *Deny…*. *Deny…* asks for a reason, which the
  requester sees.
- The message changes as the request moves on: 🔵 starting, 🟢 running with a
  **Stop** button, ⚪ stopped with its run time and cost, 🔴 denied, ❌ failed.
- Its thread records the start, a replacement for sold-out hardware, a
  warning at 80% of the time limit, and the stop.
- A machine on the provider account that the server didn't start gets its
  own alert.

Only approvers can click or use `/hi`; anyone else is told so, and the
attempt is logged. Nobody can approve their own request, so an approver's
own requests need another approver.

| Command                  | Does                                                  |
|--------------------------|-------------------------------------------------------|
| `/hi status`             | What is running, the rate right now, with Stop buttons |
| `/hi stop <name>`        | A Stop button for one machine                         |
| `/hi stop user <user>`   | A button to stop everything that user runs            |
| `/hi stop all`           | A button to stop everything                           |

Answers are visible only to you, and every stop needs a click to confirm.

### Policy, budgets, and reports

With no policy, every start needs a person. A policy lets groups run cheap,
short jobs without waiting, caps what each group may use, and sets monthly
budgets:

```sh
hi org policy example > /tmp/policy.json   # a starting point
hi org policy edit                         # opens $EDITOR, checks, saves
hi org policy show
hi org policy check                        # checks policy.json without changing it
```

| Setting                     | Meaning                                                   |
|-----------------------------|-----------------------------------------------------------|
| `max_hours`                 | Refuse starts and extensions that would run longer        |
| `hardware`                  | Refuse other hardware                                     |
| `auto_approve`              | Approve without a person up to this price and total time  |
| `user_monthly_budget_usd`   | Warn when a user in the group goes over                   |
| `group_monthly_budget_usd`  | Warn when the whole group goes over                       |

Budgets warn and never block. An over-budget request shows ⚠️ and the spend
in Slack, and always waits for a person. The channel gets one alert a month
when a user or group crosses its budget. Changes apply to the next request;
no restart needed.

At 09:00 server time the channel gets a report: yesterday's spend every
day, the week by user and group with the most-used hardware and silent
devices on Mondays, and the month on the 1st. Turn any off with
`"reports": {"daily": false}`.

```sh
hi org spend                 # this month, per user and group
hi org spend --since 7d
```

In Slack:

| Command                         | Does                                           |
|---------------------------------|------------------------------------------------|
| `/hi spend`                     | This month's spend per user and group          |
| `/hi users`                     | Users, groups, devices, and spend              |
| `/hi audit [user]`              | The last 15 audit entries                      |
| `/hi budget <group> <usd>`      | Set each user's monthly budget in a group      |
| `/hi budget <group> total <usd>` | Set the whole group's monthly budget          |

### Slack Home tab

Open the hi org app in Slack for a dashboard that is rebuilt each time
you open it:

- **Now:** each running machine with its user, hardware, cost so far, and a
  **Stop** button, plus **Stop all…**.
- **Waiting:** requests with **Approve** and **Deny…** buttons.
- **This month:** spend per group against its budget.
- **Devices:** each device, when it was last seen, and its `hi` version,
  flagged when it is older than the server's or silent for a week.

A linked user who isn't an approver sees only their own machines, requests,
and budget there, and can stop their own machines. Anyone else is told how
to link.

The Home tab needs two settings in the Slack app that older setups lack. At
[api.slack.com/apps](https://api.slack.com/apps), open the app, choose **App
Manifest**, replace it with the output of `hi org slack manifest`, save,
and reinstall if Slack asks.

### What runs, and a wall screen

The [admin page](/guide/app/admin/) on the server's machine shows
everything running, for every user, with its cost so far and **Stop**
(Running), what waits for an approval, with **Approve** and **Deny**
(Requests), and spend per user and group (Spend). Each decision there is
under the admin's own name, as in Slack, and nobody approves their own.
Connected clients report when they open `ssh`, a tunnel, `logs`, or
`serve` on a machine, with the command and instance names only, and
Running shows it.

A **wall link** shows Running and Requests on a shared screen, in a
browser, full screen. Admins make one on the admin page (Org, Wall
screens): it shows initials instead of names and hides reasons, unless the
link says otherwise, and it can approve, stop, or open nothing. It works
until an admin ends it. A screen needs only a browser on NetBird.

`hi org live`, `hi org viewer`, and the terminal wall (`hi org wall` and
its `hi-wall-live` sessions) are gone since hi 2.6.78: the admin page and
wall links replace them. `hi update` says so on a machine that still has a
wall session, with the commands to remove it.

### Serve private project templates

The server can also hand connected devices private project templates and agent skills, which `hi init` then offers next to the built-in
ones. Users never need access to the repository on GitHub.

```sh
hi org templates add private https://github.com/<org>/<private-repo>
                                  # asks for a read-only token, shown as *;
                                  # --ref serves a branch or tag other than the default
hi org templates list          # each source, its commit, layers, and skills
hi org templates sync          # fetch now instead of within 15 minutes
hi org templates rename private team   # keeps the token and history
hi org templates remove private
```

- Use a fine-grained GitHub token limited to that one repository with
  read access to contents: on GitHub, Settings → Developer settings →
  Personal access tokens → Fine-grained tokens, with the organization as
  resource owner. It stays in the server's `keys.json`. An `ssh://` or
  `git@` URL uses the server account's SSH key instead, such as a deploy key,
  where the organization allows them.
- When the token expires, fetching fails and the channel gets an alert, but
  devices keep the last good commit. Run `remove` and `add` again with a new
  token; the commits stay the same, so nothing devices cached changes.
- Every git command gives up after 2 minutes, so a slow GitHub shows up as a
  problem in `hi org templates list` instead of blocking the commands.
- Every new commit is checked before devices get it: valid `layer.json`
  files, no built-in template names, and skills with a `SKILL.md`. A commit
  that fails keeps the previous one in service and posts an alert.
- Devices check that each bundle is signed with the server's key, which they
  stored the first time they connected.
- To keep a group from seeing a source, list the ones it may use in
  `policy.json`: `"template_sources": ["private"]`, or `[]` for none.

### Serve your team's Hugging Face data

The server can hold one read token per Hugging Face organization and hand
out the organizations' datasets, models, and buckets through a proxy, so
nobody needs a Hugging Face account or token of their own:

```sh
hi org data add acme           # asks for the token; it shows as *
hi org data add acme acme-research --token-file token.txt
                                  # several organizations, one token, read from a file
hi org data                    # a menu: add, test, remove
hi org data list               # each organization and whose token it uses
hi org data test|remove <hf-org>
```

- Use a fine-grained, read-only token limited to the organizations. It
  stays in `keys.json`; the proxy passes on only reads, but a read-only
  token is safer if the box is ever compromised. The sign-in that
  `hf auth login` saves works too, but it can do everything the account
  can, so hi warns about it as it does about a write token. `list` and
  `test` show whose token each organization uses.
- Every group reads everything by default. To limit one, add
  `"data": ["acme/bars-1d", "acme/public-*"]` to it in `policy.json`;
  `[]` allows nothing.
- Token grants are in the audit log, and each download in
  `data_usage.jsonl`.

**Cloud jobs.** A rented machine can't reach the server inside NetBird.
For `hi compute run --data` on Hugging Face Jobs, and `hi compute up --data`
on RunPod and Shadeform, the server opens a second listener (its NetBird
address, port 7374) that answers only the data proxy, and publishes it with
`netbird expose` while runs need it. Each run gets a run token for the data
it named, until its `--max`. The server closes the address once no run token
is valid and nothing has called for 10 minutes.

- Turn on **Peer Expose** in the NetBird dashboard (Settings > Clients),
  once. Without it, `hi compute run --data` falls back to signed download
  links, which work but last about an hour and are limited in size on
  Hugging Face, and `hi compute up --data` is refused. Runs on other
  providers always use signed links.
- `hi org expose` shows the current public address, and each run with
  a run token: who started it, from which device, for which data, and
  until when. `hi org expose stop` closes the address now.
- `hi org expose revoke <run>` ends one run's token at once, for
  example a run left going on a machine you no longer trust. The server
  remembers it until the token would have expired, and refuses that run a
  new token; other runs keep working.
- `"expose_listen"` in `config.json` moves the listener, or `"off"` turns
  exposure off.

See [Download your team's data](/guide/data/) for what users do.

### Serve a model to hi q

The server can pass `hi q`'s requests to OpenRouter, or any
OpenAI-compatible endpoint, with one key for your team, so nobody needs a key of
their own:

```sh
hi org ai set                       # asks for the OpenRouter key, shown as *
hi org ai set --model google/gemini-2.5-flash   # change the default
hi org ai set --url http://gpu-1.example.com:8731/v1 --model <name> --no-key
                                       # a model on your own machine, no key
hi org ai                           # the setup and this month's use per user
hi org ai off                       # stop serving; keeps the key
hi org ai remove                    # stop and delete the key
```

- It is a plain pass-through: users and agents may ask for any model the
  upstream has. The default, `anthropic/claude-haiku-5.5` unless you set
  another, is what `hi q` uses when the user hasn't chosen one.
- `set` makes one small request with the key and model before saving them.
  Run it again to change them; Enter at the key prompt keeps the stored key.
- Each request is recorded in `ai_usage.jsonl` with the user, device, model,
  tokens, and cost, never the messages or the answer. `hi org spend`
  shows it under "Models through hi q".
- Nothing in hi limits spending on models. Set a credit limit on the key at
  OpenRouter as the backstop.
- The admin page and the Slack Home tab show this month's model spend next
  to compute.
- Prompts carry the user's shell history and folder names. The server
  passes them on and stores none of them, but whoever runs it could read
  them in transit; tell your users.

#### A model on your own machine

Any OpenAI-compatible server on the network works as the upstream, such as
llama.cpp's `llama-server`, vLLM, or Ollama (`http://<host>:11434/v1`), and
prompts then never leave your network. `--no-key` is for servers that take
no key; `--model` must be a name from the server's `/v1/models`.

It needs a model that calls tools well, since `hi q` lists and reads files
through tool calls. Tested on 2026-10-02 with Qwen 3.8 Flash Next served by llama.cpp on a
Strix Halo workstation: correct
commands and tool calls, 4–14 seconds an answer, against about 2 seconds for
Claude Haiku 4.5 through OpenRouter. Large models need most of the machine's
memory, so share it knowingly: a 4-bit Qwen 3.8 Flash Next is about 110 GB.

Switch back to OpenRouter with `hi org ai set` without `--url`; the
stored key is kept.

#### Models served on the apps' machines

A teammate who serves a model with `hi serve llm <recipe> --on local --org`
asks this server to pass requests to it. The request comes to Slack, or to
`hi org requests`, as `serve local/<name> … from <machine> to hi org ai`,
and an admin approves it as any other (`hi org approve s-…`). Devices then
ask for model `local/<name>`, next to the upstream's models, and the server
sends their requests to the machine's NetBird address with the key it was
given, which nobody else sees. It works with no upstream of its own, too.

```sh
hi org ai                           # lists them: who serves what, from where, up or stopped
hi org ai remove local/<name>       # stop passing requests to one, and forget its key
```

The server only reaches NetBird addresses this way. Use is recorded as for
the upstream's models, at no cost. A request can take up to 10 minutes,
against 90 seconds for the upstream, since a local model answers slower.
See [Serve it to the org](/guide/compute/serve/#serve-it-to-the-org).

### Exposed services

Connected machines report each [`hi net expose`](/guide/workstation/expose/)
to the server, which writes `exposed` and `stopped exposing` to the audit
log and shows them in the live feed:

```sh
hi org audit --since 1d | grep expos
```

This is a record, not a control. Whether anyone can expose at all is
NetBird's **Peer Expose** setting (Settings > Clients in the NetBird
dashboard), which also covers `netbird expose` run without `hi`.

### Watch and stop

```sh
hi org ls                      # everything running, for every user
hi org stop rtx-4090-1c2d
hi org stop --user alice       # everything alice runs
hi org stop --all
hi org audit --since 24h       # requests, approvals, starts, and stops
```

When approved hardware is sold out, the server starts a replacement with
at least as much memory, and records it in the audit log. A replacement may
cost up to twice the approved price or the approved price plus $1.00/h,
whichever is higher, so cheap hardware gets a lot of room and expensive
hardware stays near 2×:

| Approved | Replacement up to |
|----------|-------------------|
| $0.24/h  | $1.24/h           |
| $0.49/h  | $1.49/h           |
| $1.00/h  | $2.00/h           |
| $3.49/h  | $6.98/h           |

Change it with `"fallback_price_factor"` (default `2`) and
`"fallback_price_extra"` (default `1.0`) in the server's `config.json`, then
restart the server. A factor of `1` and an extra of `0` turn replacements
off.

Every 30 seconds the server checks each provider account. It stops machines
past their `--max`, and it records a machine it didn't start, such as one
created on the provider's website, as `unleased instance` in the audit log.

### Watch the apps

Apps watch themselves, on their own machine, since hi 2.6.32: each
app's watcher runs the health, network, and advisory rules every minute
and posts what it finds in the app's ops channel (see
[When something goes wrong](/guide/app/incidents/#the-apps-watcher)).
The server watches nothing, and keeps no app's logs or keys.

What a machine can't say is that it stopped. Give the server's machine,
and each machine that runs apps, a heartbeat to an outside service such
as healthchecks.io, which alerts when it stops:

```sh
hi heartbeat https://hc-ping.com/<uuid>     # this machine, every 5 minutes
```

A server that watched apps before keeps its `watch/` folder, read by
nothing: delete it when you like. Its dead-man's switch is this
heartbeat now.

### Remove a user

```sh
hi org user remove alice
hi org stop --user alice
```

Removing a user refuses their devices at once. Their running machines keep
running until stopped or until their limit.
