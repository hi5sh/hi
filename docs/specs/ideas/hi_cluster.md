# `hi org machines`: workloads on the organization's own machines

Status: Draft (2026-11-01; named `hi cluster` until 2026-11-02), revised
on 2026-11-03 with its three open questions decided (Decisions 7 to 9), for
review. `hi serve llm --on <machine>` (hi_serve.md, release 5) is built on
its release 3.

Dependencies: [hi_server.md](../approved/hi_server.md) (the server, device
keys, groups, policy, approvals, the audit log, the reconciler, the live
dashboard), [hi_org.md](../approved/hi_org.md) (`hi server` and `hi connect` became `hi
org`, and `--on` means the same everywhere), [hi_compute.md](../approved/hi_compute.md) (the `computeProvider`
interface, `--max`), [hi_box.md](../approved/hi_box.md) (boxes, rootless
Podman, the proxy, "Remote sandboxes"), [hi_agent.md](../approved/hi_agent.md)
(`--on <machine>`, release 4), [hi_net.md](../approved/hi_net.md) (NetBird),
[hi_app_ops.md](../approved/hi_app_ops.md) (machine heartbeats, a server
that watches nothing), and [hi_app_accounts.md](hi_app_accounts.md) (a
Linux account per team, deferred).

## Goal

An organization has physical machines in its VPN: workstations, a Strix Halo
box, a GPU server or two. Some run teams; most have room left over most of
the time. Anyone allowed should be able to see from one place what is free,
and put something there for an hour or for weeks: a box, an agent, a run to
completion, a team.

- **One view of the machines.** CPU, RAM, disk, and GPU: what each offers,
  what is used, and what runs where.
- **One flag to run elsewhere.** `--on <machine>` or `--on auto` on the
  commands that already exist, instead of a second set of commands.
- **The same rules as rented compute.** Device keys, groups, policy,
  approvals in Slack, leases, `--max`, the audit log, the live dashboard.
- **A team's load is never interrupted.** Guests get only what a machine
  offers, and nothing ever preempts a team.

## Idea in one line

The organization's machines become one more compute provider on the org
server, `own`, priced at zero. What the org server already does for RunPod
(approve, lease, list, reconcile, stop, show) it does for them, and `hi
compute hardware` shows a free spot on a workstation next to an H100 on
RunPod.

## What exists already

| Need | In hi today |
|---|---|
| A private network between machines | NetBird (`hi net`) and its access control |
| A central place, users, keys | `hi org`: ed25519 device keys approved by an admin, users, groups, policy, budgets |
| Approvals, audit, watching | Slack approvals, the audit log, `hi org live`, the reconciler that stops what has expired |
| Finding and reaching a machine | `computeProvider` (`compute.go`) and the `managed` driver: `hardware`, `create`, `list`, `stop`, `ssh`, `logs`, `runJob`, `wait`; the `--max` watchdog |
| The thing that runs | `hi box`: rootless Podman, the egress proxy, presets, limits; `hi agent` and `hi team` build on it |
| A machine that reports | The machine heartbeat timer (hi_app_ops.md, release 2) |
| Already planned | `hi agent --on <machine>` and `hi box --remote <hardware>`, "when `hi box` gains remote boxes" |

## What is new

1. **A machine offers room.** On the machine, `hi org offer` uses its
   existing device key to ask the server to add it as a host; an admin
   approves it in Slack, as with enrollment. The offer says what guests may
   use, for example 64 GB of RAM and 8 cores, and whether the GPU. Running
   `hi org offer` again changes the offer; a smaller one takes nothing from
   guests already running. Only a machine that runs no teams may offer
   room (Decision 7): `hi org offer` refuses on one that does, and `hi team
   up` refuses on a host that offers, naming `hi org offer off`.
2. **An inventory.** Every device that runs `hi org offer`, and every
   machine with teams that chooses to, reports its totals, what its teams
   and boxes reserve, and what is left for guests, signed with its device
   key, from a systemd user timer of its own every minute. (The machine
   heartbeat of hi_app_ops.md goes to an outside service, not to the org
   server, so this is a new timer, not that one.) It reads what `hi serve
   detect` reads, from evidence: AMD GPUs from the kernel's KFD topology,
   NVIDIA ones from `nvidia-smi`. On Strix Halo, GPU memory is system RAM,
   so free GPU memory and free RAM are counted as one pool. A team's
   reservation is what its boxes' cgroup limits still allow (Decision 9).
   A host that stops reporting for five minutes gets no new work, and `hi
   org machines` shows it as unreachable; what runs there keeps running.
3. **Placement.** First fit, with headroom kept on each host. No queue at
   first: when nothing fits, hi says so and shows the closest fits, as
   RunPod's "suggest free alternatives" does.
4. **A host agent.** A systemd user service on each host that pulls its
   work from the server over outbound calls only, as Socket Mode does for
   Slack. It starts the box with the Podman limits it was granted, and
   stops it when the lease ends, even while the server is down.
5. **Direct reach.** SSH, tunnels, and logs go from the laptop to the host
   over NetBird, never through the server, as with rented compute. The
   server puts the user's SSH public key in place for the lease.
6. **Served models on a host** (hi_serve.md, release 5). `hi serve llm
   <recipe> --on <host|auto>` asks the server for a lease; the host agent
   runs `hi serve llm <recipe> --on local --org` under the host's account,
   with the same box, front, units, and memory check as on your own
   machine, and the lease's end as its `--max`. The model is then reached
   as `local/<name>` through `hi org ai`, as with `--org`, by the user who
   asked and whoever the group policy allows; nobody holds its key by
   hand. `hi serve ls`, `status`, `logs`, `stop`, and `rm` work on it from
   the laptop through the lease.

## Commands

Workloads take a flag on the commands that exist; the machines themselves
are under `hi org`, next to the users and policy that decide who may use
them.

```text
# Workloads
hi box --on gpu1 ...
hi box --on auto --ram 32G --gpu ...       # any host that fits
hi agent claude "<task>" --on auto
hi compute up --on auto --ram 64G --max 8h
hi compute run train.py --on auto --max 6h
hi team new demo --on gpu1                 # long-running, pinned
hi serve llm qwen3.8-flash-next --on auto  # a host whose GPU and memory fit it

# The machines
hi org offer --cpu 8 --ram 64G [--gpu none]   # on the host: offer room to guests
hi org offer                                  # on the host: what it offers now
hi org offer off                              # on the host: running guests finish, no new ones
hi org machines [--json]                      # hosts, free and used, what runs where
hi org machines drain <host>                  # on the server box: no new work goes there
hi org machines undrain <host>
```

`--on` rather than `hi org machines <machine> box ...`: it matches the
approved `hi agent --on`, every command learns it the same way, and a
machine name in a subcommand's place collides with subcommands (v2.5.57,
"No machine called --help").

`--on` already names a provider in `hi compute` (`--on runpod`). It keeps
that meaning and gains machine names, `local`, and `auto` (hi_org.md), so a
machine may not be named like a provider; `hi org offer` refuses one.

## Flow

```text
 laptop                       org server                      host (gpu1)
┌──────────────────┐ signed ┌──────────────────────┐ pull   ┌────────────────────┐
│ hi box --on auto │──────▶ │ policy, approval     │ ◀───── │ host agent         │
│   --ram 32G      │        │ placement (first fit)│ lease  │ guest account      │
└────────┬─────────┘        │ lease, audit, live   │ ─────▶ │ podman, limits,    │
         │                  └──────────────────────┘        │ proxy, --max       │
         │ ssh / tunnel over NetBird, straight to the host  └────────────────────┘
         └──────────────────────────────────────────────────────────▲
```

1. The user asks, with the size and the lifetime.
2. The server applies the group's policy: auto-approved on free hosts within
   limits, otherwise an approver in Slack.
3. The server picks a host and writes a lease: user, host, size, end time.
4. The host agent takes the lease, starts the box under the guest account
   with its limits, and reports the box's SSH address.
5. The CLI connects straight to the host.
6. At the lease's end, or on `stop`, the host agent stops the box. The
   server's reconciler checks that it did.

## Decisions

1. **Teams first, always.** A host offers guests only what it declares.
   Every guest box gets hard cgroup limits for CPU, memory, and disk, so it
   can't grow into a team's share. Under memory pressure a guest is killed
   first (`oom_score_adj`). Nothing ever preempts a team, and there is no
   spot-like guest class that a team's load can be traded against.
2. **Isolation before guests share a machine with teams.** Today every box
   on a machine runs as the one account that owns every team's
   `secrets.json` and the device key (hi_app_accounts.md). A guest's box
   escape would reach all of it. So either release 2 places guests only on
   hosts without teams, or a guest account (or one per user) from
   hi_app_accounts.md comes first.
3. **The server is a ledger and a matchmaker.** v2.6.32 took watching off
   the server so teams don't depend on it. This keeps that: a host's teams
   and running boxes don't need the server; only new placement does, and
   leases are enforced on the host.
4. **One machine per workload.** Hosts don't talk to each other at first.
   NetBird already connects them, so multi-machine work (Ray, distributed
   training) later is mostly an access-control change.
5. **Who may place** is the existing groups and policy: for example staff
   auto-approved on free hosts up to a number of hours, students through an
   approver, agents in their own group.
6. **The name is `hi org machines`, not `hi cluster`.** "Cluster" suggests
   Slurm-like coupling that this doesn't have, and a separate top-level
   command would split the organization in two: its people and rules under
   one name, its machines under another. They live together under `hi org`
   (hi_org.md).

Taken on 2026-11-03:

7. **Guests run only on machines set aside for them.** A machine that runs
   production teams offers no room, until the guest accounts of
   hi_app_accounts.md exist, so a guest's box can never reach a team's
   `secrets.json` or the machine's device key. This settles Decision 2.
   Such machines may still report their inventory, so `hi org machines`
   shows where everything runs.
8. **Only through the org server.** Every placement goes through `hi org`:
   device keys, policy, Slack approvals, leases, and the audit log. There
   is no mode without an org server: plain SSH over NetBird already does
   that, and hi would add nothing to it.
9. **A team's reservation is its boxes' limits.** A team holds what its
   boxes' cgroup limits still allow beyond what they use now, as `hi serve
   detect` counts it, so the reservation is measured, not declared, and
   grows only when a team's limits do.

## Existing tools

Studied for what to take, not to adopt as the core: none of them knows
boxes, teams, the proxy, or approvals, which are the hard part here.

| Tool | Fit | Take |
|---|---|---|
| [dstack](https://dstack.ai/docs/concepts/fleets/) SSH fleets | Closest: enrolls existing servers over SSH, dev environments, tasks, services, GPU blocks. Needs Docker on hosts (hi left the docker group on purpose), its own server, its own users | Enrollment over SSH; splitting a GPU host into blocks |
| [SkyPilot](https://docs.skypilot.co/en/stable/reservations/existing-machines.html) SSH Node Pools | `sky ssh up` on existing machines, then `--infra ssh/<pool>`. Its own API server and runtime | Could sit on top later, for heavy ML users |
| Slurm | Batch queues and multi-node MPI; munge, matching uids, a shared filesystem | Wrong shape for boxes and teams that run for weeks |
| Ray | A Python framework for distributed compute, not a container placer | A workload on top: a template that runs Ray across boxes |
| Nomad | A good general placer with a Podman driver and drain; another daemon and ACL system, BSL licence | Resource fingerprinting; `drain` |
| HTCondor | Built on owners first, guests on what is idle | The policy in decision 1, not the system |

## Releases

0. **First, `hi org`** (hi_org.md, release 1), so the machines are born under
   their own name.
1. **Inventory.** `hi org offer`, `offer off`, and `hi org machines`, with
   the hosts' resource reports. Read-only, and useful by itself: where is
   there room?
2. **The `own` provider.** `hi compute up`, `ssh`, `tunnel`, `logs`, and
   `stop` with `--on <host|auto>`: a box with a lease, `--max`, and
   approval.
3. **`--on` across commands.** `hi box`, `hi agent`, `hi compute run`, and
   `hi serve llm` (hi_serve.md, release 5) with `--detach`, `wait`, and
   `logs`.
4. **Teams placed and moved.** `hi team new --on`, and moving a team, with
   the planned export and import ("Restore a team ... on another machine").
5. **Later.** A queue when nothing fits, drain with maintenance windows,
   multi-machine work.

## Open questions

1. Who may use a model served on a host for someone: only the user who
   asked, or anyone the group policy allows through `hi org ai`? The draft
   above says the policy, as for `--org`.
