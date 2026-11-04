# `hi app target`: production on a machine of your own, over SSH

Status: Draft (2026-11-04), for review.

Dependencies: [hi_app.md](../approved/hi_app.md) (the app, its service,
its folder), [hi_app_internal.md](../approved/hi_app_internal.md) (the
peer and the internal relay), [hi_app_addresses.md](../approved/hi_app_addresses.md)
(names the IT team keeps, `--https`), [hi_app_snapshots.md](../approved/hi_app_snapshots.md)
(snapshots, restore, rollback), [hi_app_data_fork.md](../approved/hi_app_data_fork.md)
(Stage's fork), [hi_app_workers_handover.md](../approved/hi_app_workers_handover.md),
[hi_app_keys.md](../approved/hi_app_keys.md) (settings, the models
socket), [hi_watch.md](../approved/hi_watch.md) (the resolver),
[hi_box.md](../approved/hi_box.md), [hi_compute.md](../approved/hi_compute.md)
(`sshTarget`), and [hi_cluster.md](hi_cluster.md) (`hi org machines`,
which this is not).

## Goal

Today an app's boxes all run on the machine that runs the app: the lead,
the dev box, Stage, and production. Many organizations already host their
apps somewhere: a VPS at Hostinger or Hetzner, an EC2 instance, a Compute
Engine VM. They want production there, on their hosting, at their name,
while the lead, the dev box, and the dev page stay where they are.

- **Production where the organization hosts.** Any Linux machine hi can
  SSH into is a place production can run.
- **Nothing else changes.** People ask the lead, owners build in the dev
  box, try on Stage, and click Release, as today. Releases, rollbacks,
  snapshots, the helper, jobs, and incidents work the same.
- **Raw SSH first.** No provider's API or CLI in the first releases. A
  recipe per provider comes later, and only makes a machine that is an
  SSH target.

## Idea in one line

The app's machine keeps the control: the lead, the broker, the
repository, the versions, the keys, and the snapshots. A **target** is a
machine where a slot's boxes run, reached over one SSH connection, with
the same hi on it doing locally what the broker does locally today.

## Where things run

| | The app's machine (home) | The target |
|---|---|---|
| The lead, Slack, the dev page | Yes | No |
| The dev box | Yes | No (release 4 at the earliest) |
| The repository, versions, tasks | Yes | No: a copy of each release's code |
| Stage | Yes (release 2 may move it) | Optional, release 2 |
| Production's box and its address | No | Yes |
| Production's data | A mirror, as fresh as the last snapshot | Yes, the real one |
| Model keys, sign-ins, Slack tokens | Yes | No |
| Production's settings (`hi app env --production`) | Yes | Yes: the box needs them, as today |
| Snapshots and their key | Yes | No |

## What exists already

| Need | In hi today |
|---|---|
| Starting a slot's box from a commit | `show()` and `startApp()` (`app_preview.go`): `git archive` into `run/app-<slot>-<port>`, `podman run`, wait until it answers, move the port file, remove the old box |
| An address that follows the box | The relay (`net_expose.go`) follows a port file; routes `/__hi/`, `/docs/`, `/llms.txt` to the app's web socket; injects the helper; checks SSO |
| TLS | certmagic (`app_https.go`), today over DNS-01 for the machine's domain |
| TLS ended in front | `hi app addresses <app> ... --https`: hi serves http, its links say https |
| Seams for a box elsewhere | The broker's function fields: `app`, `running`, `answers`, `removeApp`, `oldImage`, `takeSnapshot`, `restoreData` (`app_broker.go`) |
| The workers' lock | A file lock in the box's workers folder (`app_workers.go`): needs both boxes on one machine, which a target keeps |
| SSH to a machine | `sshTarget` (`compute.go`) |
| hi's own binary in boxes | Mounted at `appBoxRelay`; one static Linux binary |

## What is new

1. **A target in `app.json`.** Per slot: `"targets": {"production":
   {"ssh": "deploy@203.0.113.7", "port": 22, "host_key": "...", "front":
   "https" | "behind-proxy", "name": "payments.example.com"}}`. No
   target is the app's machine, as today.
2. **`hi app target`**, an admin's command on the app's machine. It checks
   the target, puts hi there, and moves the slot (see Commands).
3. **The app's SSH key.** hi makes an ed25519 key for the app, in its
   folder beside `secrets.json`, and prints its public half for the
   target's `authorized_keys`. The target's host key is pinned on first
   contact, after the admin compares its fingerprint.
4. **hi on the target.** hi copies itself, the same version, to
   `~/.local/share/hi/targets/<app>/hi`, and runs `hi app __runner <app>`
   there as a systemd user service. The runner does on the target what the
   broker does at home for a slot: build the base image, start and stop
   boxes, wait for one to answer, hold the workers' lock, run the relay
   and the resolver, and keep the slot's state in `run/<slot>.json`. On
   the target's reboot it starts production again from that state,
   without home.
5. **One SSH connection per target**, a unit of its own under the app's
   service (`hi-app-<name>-target`), as the resolver has
   (`app_resolver.go`). Home connects to the target; the target never
   connects home, so home may sit behind NAT or in the VPN. It carries:
   - `-L`: the runner's socket, for the broker's calls;
   - `-R`: the models socket, so the app's model calls go home, where the
     broker's proxy adds the key;
   - `-R`: the slot's web socket, so the helper, `/docs/`, and
     `/llms.txt` are answered at home.

   OpenSSH removes a stale forwarded socket only with sshd's
   `StreamLocalBindUnlink yes`, which a stock sshd doesn't set. So before
   each connection hi removes the old sockets on the target over a
   plain `ssh` command.
6. **The broker calls the runner.** The seams above (`app`, `running`,
   `answers`, `removeApp`, `oldImage`, logs) get target-aware versions.
   `show()` splits in two: the broker's half (the commit, the version,
   the announcement, the channel's link) stays; the place's half (copy the
   code, start the box, wait, hand over the workers, move the port file,
   remove the old box) runs where the slot lives. At home it is a call in
   the same process; on a target, a call over the runner's socket. The
   code is sent as `git archive <commit>`, streamed over the connection.
7. **The front on the target.** The runner's relay is production's
   address there, with the helper, SSO, and people's key links as today:
   - **`https`**: the relay listens on 80 and 443 and gets a certificate
     for the name from Let's Encrypt over HTTP-01 (certmagic, as
     `app_https.go` already uses). The IT team points the name at the
     target first; hi checks it before moving.
   - **`behind-proxy`**: the relay serves http on a port, and the
     provider's load balancer or the IT team's proxy ends TLS, as
     `--https` means today.
8. **Data on the target, a mirror at home.** Production's data is in
   `~/.local/share/hi/targets/<app>/app/` on the target. A snapshot first
   copies it home with rsync, without pausing, then pauses production's
   box, copies what changed in the meantime, and unpauses it, as the
   warm pass before a pause does today (`appSnapshotWarmup`). Then restic
   snapshots the folder at home as today, with `app/` as that mirror. So
   a snapshot pauses production only for the last copy.
9. **Restore, rollback with data, and Stage's fork use the mirror.** A
   rollback with its data stops production on the target, copies the
   snapshot's `app/` there with `rsync --delete`, and starts the old
   version. Stage's fork takes a fresh copy first, then forks the mirror as
   today. rsync replaces files instead of writing over them (never
   `--inplace`), so a fork's links into the mirror keep the old files.
10. **Watching.** The broker's checks (incidents, jobs, workers, the
    resolver's log) read through the runner. When the connection is down
    for five minutes, the lead opens an incident: "production's target
    doesn't answer", and the dev page shows when it was last seen.

## Commands

```text
hi app target <app>                                    # where each slot runs, the connection, last seen
hi app target <app> check deploy@203.0.113.7           # what the machine has and lacks, with the commands for each
hi app target <app> production deploy@203.0.113.7 \
    --name payments.example.com [--behind-proxy [--port 8080]]
hi app target <app> production local                   # back to the app's machine
hi app target <app> key                                # the app's public key, for authorized_keys
hi app target <app> cloud-init                         # a machine set up as a target, for a provider's "user data" (release 3)
```

`check` reports, for each thing, what it found and the one command that
fixes it, for the machine's admin or the IT team (as hi does elsewhere):

| Needs | Why |
|---|---|
| Linux, x86-64 or arm64 | hi's binary |
| SSH with the app's key, as an account that isn't root | the connection; boxes run rootless |
| Rootless Podman, with `pasta` and rootless overlays (hi checks the version) | the boxes, the resolver, Stage's fork |
| `loginctl enable-linger <user>` | the runner keeps running with nobody logged in, and after a reboot |
| rsync | the mirror |
| Ports 80 and 443 open, and `net.ipv4.ip_unprivileged_port_start=80` (`https` only) | a rootless relay on 80 and 443 |
| The name resolving to the target (`https` only) | HTTP-01 |
| Memory and disk for production's box and data | `hi app limit` |

## Moving production to a target

`hi app target payments production deploy@203.0.113.7 --name
payments.example.com`:

1. Checks the target, and stops with what is missing.
2. Shows the host key's fingerprint and asks to pin it.
3. Copies hi, starts the runner and the connection, and builds the base
   image on the target. That takes a few minutes, while production
   still runs at home.
4. Takes a snapshot, then copies production's data to the target in a
   warm pass.
5. Starts production's version on the target, on that data, and waits
   until it answers.
6. Stops production at home, copies what changed since the warm pass,
   and restarts the box on the target. Production is down from the stop
   until the box answers again: seconds for a small app.
7. Gets the certificate, or waits for the proxy in front, and checks the
   name answers from outside.
8. Posts in the channel: "Production moved to payments.example.com", with
   the new link, and updates the message with production's link and
   people's DMs, as a new address does today. A link to the old address
   says where production went, until the next release.

`production local` does the same the other way. If any step fails,
production keeps running where it was, and the step's error says why.

## Flow

```text
 app's machine (home)                                    target (203.0.113.7)
┌──────────────────────────────┐                   ┌──────────────────────────────┐
│ lead, Slack, dev page        │   one SSH conn    │ hi app __runner payments     │
│ broker ── runner calls ──────┼──── -L sock ────▶ │  ├ production box (podman)   │
│ models proxy (keys) ◀────────┼──── -R sock ───── │  ├ relay :443 (helper, SSO)  │
│ helper, /docs/ ◀─────────────┼──── -R sock ───── │  ├ resolver, workers lock    │
│ repo, versions, snapshots    │ ◀─── rsync ────── │  └ app/ (production's data)  │
│ dev box, Stage               │                   └──────────────▲───────────────┘
└──────────────────────────────┘                                  │ https
                                                       people ────┘
```

## When something is down

| What | Production's pages | What stops |
|---|---|---|
| The app's machine | Keep working | The helper and `/docs/` ("hi is away, try again soon"), model calls, releases, snapshots, the lead |
| The SSH connection | Keep working | The same, until it reconnects; an incident after five minutes |
| The target | Down | Everything on the target; the lead opens an incident, and an admin can move production back with `production local`, from the last snapshot |
| A target reboot | Down until the runner starts it again | Nothing else |

Today all of these are one machine. With a target, production's pages
depend only on the target.

## Decisions (proposed)

1. **Home keeps the control; a target only runs.** The lead, the broker,
   the repository, the versions, the keys, and the snapshots stay on the
   app's machine. A target holds only what production needs to run: its
   code, its settings, its data.
2. **Raw SSH first.** A target is a user and an address. No provider API,
   CLI, or account in releases 1 and 2.
3. **Model keys stay home.** Production's model calls go through the
   connection, so a target never holds a key, at the cost of the app's
   model calls stopping while home is down. A key for the target alone is
   an owner's decision, later.
4. **The front on the target is public.** That is the point of hosting
   there. An internal-only address on a target needs the app's NetBird
   peer there, and comes later (release 4).
5. **Only an admin on the app's machine sets a target**, with `hi app
   target`, logged and posted in the channel. Owners see it on the dev
   page's Addresses tab. The dev box has no command for targets, so it
   still can't touch production.
6. **One app with `https` per target** until targets share a front
   (release 3). Behind a proxy, any number of apps, each on a port.
7. **The dev box stays home.** It holds the agents' sign-ins through its
   proxy, owners reach it over SSH on the app's peer, and the dev page's
   chat talks to it directly. Moving it means moving the proxy and the
   sign-ins, which is a different decision from where production runs.
   [hi_cluster.md](hi_cluster.md) covers more room for boxes on the
   organization's own machines.

## Code that changes

| Where | What |
|---|---|
| `app.go`, `appConfig` | `targets`, per slot |
| `app_target.go` (new) | The command, `check`, the key, the host key, copying hi, the connection's unit |
| `app_runner.go` (new) | `hi app __runner`: its socket API, the slot's state, starting production again after a reboot |
| `app_preview.go` | `show()` split into the broker's half and the place's half; `startApp()` on paths of the place |
| `app_broker.go` | The seams' target-aware versions |
| `net_expose.go`, `app_https.go` | The relay on its own, without NetBird, listening on 80/443 or a port, with an HTTP-01 certificate |
| `app_snapshots.go` | The mirror before the pause, and the pause on the target |
| `app_data_fork.go` | A fresh copy before a fork |
| `app_incidents.go`, `app_jobs.go`, `app_workers.go`, `app_resolver.go` | Read through the runner for a slot on a target |
| `app_devpage.go` | Where each slot runs, on the Addresses tab |
| `site/guide/app/` | A page "Production on a machine of your own"; *The app's boxes* gets a column for where each box runs |

## Releases

1. **Production on an SSH target.** `hi app target`, the runner, the
   connection, `https` and `behind-proxy`, the mirror, releases,
   rollbacks, snapshots, incidents. One app per target with `https`.
2. **Stage on a target.** The same target as production or another one.
   On production's target, Stage forks production's data there, so the
   data never leaves the target except for the mirror.
3. **Recipes for providers.** First `hi app target <app> cloud-init`:
   one cloud-init file that installs Podman and rsync, adds the account,
   its key, and lingering, and opens the ports. Most VPS and cloud
   consoles take cloud-init as "user data" (Hetzner, DigitalOcean, EC2,
   Compute Engine; Hostinger to be checked). The same steps come as a
   shell script for one that doesn't. Then, per provider whose CLI is
   signed in on the machine (`aws`, `gcloud`, `az`, `doctl`, `hcloud`),
   `hi app target <app> new --on aws` makes the VM with that file, its
   firewall, and a fixed address, as data files in `recipes/targets/`
   beside `recipes/llm/`. The result is always an SSH target. Targets
   also share a front, so several apps use 443 on one machine.
4. **Later.** Internal-only addresses on a target (the app's peer
   there); the dev box on a target; a target from `hi org machines`.

## Not SSH targets

Fly.io, Cloud Run, App Runner, Render, and Railway run images, not
machines with Podman and an SSH account. hi's runner can't sit beside the
box there, so the workers' handover, the pause for a snapshot, and the
relay with the helper don't work as they do on a target. Supporting them
would be a second kind of target: an image hi builds, the provider's
volume for the data (Cloud Run has none that suits SQLite), and the
provider's own snapshots. That waits until SSH targets have proved out,
and may never come: each of these providers also offers plain VMs.

## Open questions

1. **Data that must stay on the target.** For an organization that hosts
   somewhere for data residency, the mirror at home is a copy outside it.
   Should a target be able to keep its snapshots on its side (restic on
   the target, to the organization's storage), with no mirror, at the
   cost of Stage forking and rolling back only there?
2. **How fresh the mirror is.** Daily and before each release, as
   snapshots are now, or more often for a slot on a target, since the
   mirror is also what an admin moves back from when the target is gone?
3. **Model calls while home is down.** Is "the app's AI stops while the
   app's machine is down" acceptable, or does release 1 need the target's
   own key as an option?
4. **Who may move production.** An admin only, as proposed, or also an
   owner from the dev page once a target is set up?
5. **Docker targets.** Many VPS images come with Docker, not Podman. hi
   left Docker on its own machines on purpose; should a target accept
   rootful Docker, with a warning, or refuse it?
