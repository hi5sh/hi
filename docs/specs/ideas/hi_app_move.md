# `hi app move` and `hi app standby`: an app that can run on either of two machines

Status: Draft (2026-11-10), for review. An exploration first: what high
availability can mean for a hi app, what it can't, and the order to build
it in. Release 1, moving an app, was asked for the same day and built as
v2.6.111 (see [As built](#as-built-v26111)); releases 2 and 3 are still a
draft.

Dependencies: [hi_app.md](../approved/hi_app.md) (the app is a folder; its
service, broker, and boxes), [hi_app_internal.md](../approved/hi_app_internal.md)
(the app's NetBird peer), [hi_app_addresses.md](../approved/hi_app_addresses.md)
(names the IT team keeps), [hi_app_snapshots.md](../approved/hi_app_snapshots.md)
(the pause, the warm pass, the repository and its key),
[hi_admin.md](../approved/hi_admin.md) (the admin page),
[hi_org.md](../approved/hi_org.md) and [hi_server.md](../approved/hi_server.md)
(device keys, signed calls), [hi_app_ops.md](../approved/hi_app_ops.md)
(heartbeats to an outside service), [hi_update.md](../approved/hi_update.md)
(waiting for pauses and releases before a restart), and two drafts it sits
beside: [hi_app_targets.md](hi_app_targets.md) (production on another
machine, over SSH) and [hi_cluster.md](hi_cluster.md) (`hi org machines`).

## Goal

Two machines have hi: ws1 and ws2. Today each app lives on one of them. When
that machine is down, so is the app: its lead in Slack, production, Stage,
and the dev page. A snapshot in the bucket can't be restored on the other
machine yet ([hi_app_snapshots.md](../approved/hi_app_snapshots.md), "Not in
this").

- **Move an app** from ws1 to ws2 with a stop of about a minute, at the same
  addresses, losing nothing: before an OS upgrade, for a new machine, or for
  more room.
- **Keep a copy ready** on ws2, so that when ws1 dies the app runs again
  within minutes, losing at most the last few minutes of its data.
- **Never two at once.** Whatever fails, hi starts an app on a second
  machine only when it knows the first has stopped it, or an admin says so.

## What high availability can mean here

| Level | What happens when ws1 dies | Data lost | What it needs |
|---|---|---|---|
| Today | The app is down until ws1 is fixed | Nothing, once ws1 is back; if its disk is gone, everything since the last snapshot in the bucket, and restoring it elsewhere isn't built | Snapshots, a bucket |
| 1. Move | The same, but planned stops (upgrades, reboots, hardware) cost a minute instead of the length of the work | Nothing | ws2 with hi; SSH between the two |
| 2. Standby, an admin takes over | Running again a few minutes after an admin presses **Take over** on ws2 | What changed since the last copy: 5 minutes by default | ws2 holds a copy refreshed every few minutes; a third voice (the org server) or an admin's word |
| 3. Standby that takes over by itself | Running again about 7 minutes after ws1 stops answering, with nobody involved | The same | The same, and the org server on a third machine |
| Both at once | Nothing stops | Nothing | Not possible for a hi app: see below |

**Why not both at once.** Several things in an app may only run in one place:

| What | Why there can only be one |
|---|---|
| The lead's Slack connection | Slack's Socket Mode allows several connections per Slack app, but it sends each event to only one of them. Two leads would each hear about half the messages, and both would answer. |
| The app's NetBird peer | Its identity (a WireGuard key) is its address. Two machines with the same identity online fight over it. |
| The folder's writers | Hermes' SQLite databases, Hindsight's PostgreSQL, the app's own database in `$HI_DATA_DIR`, and git each take one writer. Two copies that both take writes split, and nothing can merge them. |
| The broker | It alone moves the production branch, force-pushing it, and it runs the review rounds, the jobs, and the snapshots. |

An app could be written for a database that runs on several machines, but a
hi app is a folder with SQLite in it, and its lead is one Hermes. So high
availability here means **one machine runs the app, a second is ready to,
and hi makes sure there is never more than one**. That covers what actually
happens to a machine: it needs a reboot, it needs an upgrade, it loses a disk
or a power supply, it gets full.

## Idea in one line

An app is a folder, and its addresses belong to its own NetBird peer, not
to the machine it runs on. So any machine that holds the folder can run the
app at the same addresses. hi copies the folder to the other machine ahead
of time, and makes sure exactly one machine runs it.

## What exists already

| Need | In hi today |
|---|---|
| The app as one folder | [hi_app.md](../approved/hi_app.md): "Backing up, restoring, or moving a team works on the folder"; containers are rebuilt from it; `hi app up <folder>` registers a moved folder |
| An address that isn't the machine's | The app's NetBird peer (`appPeerArgs`, `app_internal.go`), its identity in the folder (`dev/netbird`). Its IP and name (`demo.corp.example`), NetBird's policies for its group, the IT team's A records and proxies ([hi_app_addresses.md](../approved/hi_app_addresses.md)), and the machine domain's records (`demo.hi.example`, which point at the peer's NetBird address) all name the peer, not the machine |
| A consistent copy while the app runs | Snapshots: the broker's locks, `podman pause` of the lead, the memory server, and production, a warm pass before the pause (v1.14.4) |
| Copying data to another machine while it runs | [hi_app_targets.md](hi_app_targets.md)'s mirror: rsync without a pause, then a short pause for what changed |
| Waiting for a release or a pause before stopping | `hi update --restart` waits for `run/paused` and `run/releasing` |
| Being told a machine is dead | Heartbeats to an outside service ([hi_app_ops.md](../approved/hi_app_ops.md)) |
| Signed calls to a third machine | The org server and device keys ([hi_org.md](../approved/hi_org.md)) |

## What a move carries

What the folder holds goes as it is. What lives beside it on ws1 goes with
it, or is made again. What belongs to ws2 is checked first.

| | What | In a move |
|---|---|---|
| The folder | Everything but `run/` | Copied |
| | `secrets.json`, the peer's NetBird identity | Copied, unlike in snapshots: they are the app's, and the identity is what keeps its addresses |
| | `run/` | Made again by `hi app up`; the proxy's network log is copied |
| Beside the folder on ws1 | The snapshot repository and its key (`app-snapshots/<name>`) | Copied. Snapshots, restores, and rollbacks go on from ws2, in the same repository with the same restic host tag (`hi-team-<name>`) and the same bucket |
| | The app's link in `apps/`, its units and timers | Made again by `hi app up` on ws2, removed on ws1 |
| | Its record in the machine domain's DNS (`dev/https-record.json`) | Goes with the folder. ws1's hi stops keeping it, without deleting it |
| | A public address (`netbird expose`, `hi app domain`) | Ended on ws1, started on ws2 with the same name |
| ws2's own | hi at the same version or newer; Podman, restic, rsync, lingering | Checked |
| | Hermes and Hindsight's client in `~/.local/share/hi` | Installed by `hi app up`, as on any machine |
| | The machine's keys the app uses: Hermes' OpenRouter key, Claude Code's and Codex's sign-ins, `gh`'s sign-in for the broker's pushes | Checked: an app on the machine's keys needs ws2's own, or keys of its own first (`hi app key`) |
| | The machine's domain (`hi net domain`) | Checked: the same domain on ws2 keeps the https names; without it they stop working there |
| | The org (`hi org ai`, served models) | Checked: ws2 in the same org |
| | Room for the app's limits (`hi app limit`) and disk for its folder | Checked |
| | The box images | Built on ws2 before the stop |

Nothing changes for the IT team: the names point at the peer, and the peer
goes with the app.

## Commands

```text
# Moving
hi app move <app> <machine> [--now] [--yes]     a stop of about a minute, then the app runs on <machine>
hi app move <app> <machine> --check             what <machine> has and lacks for the app, with the command for each

# A standby (release 2)
hi app standby <app> <machine> [--every 5m]     keep a copy on <machine>, ready to take over
hi app standby <app>                            where the copy is, how old, what a takeover would lose
hi app standby <app> off                        stop copying; the copy on <machine> is removed
hi app takeover <app> [--force]                 on the standby: run the app here, from the latest copy

# Taking over by itself (release 3)
hi app standby <app> <machine> --auto [--after 5m]
hi app standby <app> --away 15m                 before a planned stop: no takeover by itself for that long

# The machines an app can go to
hi app machines                                 machines this one sends apps to or takes them from
hi app machines add <machine> <key>             on the receiving machine: take apps from <machine>
hi app machines rm <machine>
```

`<machine>` is a machine with hi on the NetBird network, by its name there
(`ws2`, `ws2.corp.example`), reached over SSH as the account that runs hi.
`--check` prints this machine's key and the line to run on ws2,
`hi app machines add ws1 ssh-ed25519 AAAA...`, when ws2 doesn't take apps
from ws1 yet. ws1's key on ws2 runs only hi's receiving end, `hi app
__receive` (a forced command in `authorized_keys`): it can check, receive
into a folder of its own, start, and report, but it gets no shell.

On the admin page: **Move…** and **Standby…** on an app's Overview, a list
of the machines that take apps from this one under Machine, and on the
standby's page a card for the app, "standby of ws1, copy from 3 minutes
ago", with **Take over**.

## Release 1: moving an app

`hi app move demo ws2`, on ws1:

1. **Checks ws2** (the table above) and stops with what's missing, and the
   command on ws2 for each one, or "ask your IT team" where it's theirs.
2. **Gets ws2 ready** while demo runs: its images built, the folder
   copied in a warm pass, and again until what's left is small. The
   folder goes to the same path on ws2 when it's free, so anything that
   names its folder (git's worktrees) still finds it; otherwise to
   `--dir`, and hi repairs the worktrees.
3. **Waits** for running tasks, review rounds, a release, or a snapshot to
   end, up to 30 minutes, as `hi update --restart` does. `--now` doesn't
   wait: tasks are stopped, their committed work stays on their branches,
   and the lead is told which.
4. **Takes a snapshot**, labelled `before moving to ws2`.
5. **Says so in the channel:** "I'm moving to another machine; production
   and I are away for about a minute."
6. **Stops demo on ws1**: the lead, the peer, production, Stage, and the
   dev box. Copies what changed since the warm pass, exactly
   (`--delete`), with the secrets and the peer's identity.
7. **Starts demo on ws2** with `hi app up`: the peer comes up with its own
   identity, so it has the same address and name; production answers; the
   lead connects to Slack.
8. **Checks from ws1** that production answers at its address and the lead
   is connected. Then ws1 removes demo's units and link, keeps the folder
   with a note, `moved.json` (where to, when), and refuses `hi app up` on
   it, naming the command that moves it back. The folder stays until an
   admin deletes it; ws1's admin page reminds them after 7 days.
9. **Says so:** "Back, on another machine. Nothing changes for you." In the
   admin page's Activity on both machines, with who moved it.

Until step 8 passes, ws1's folder is the app: if ws2 doesn't answer within
10 minutes, hi stops it there and starts demo on ws1 again, and nothing
written on ws2 is kept. After it passes, ws2's folder is the app.

The stop is step 6 to step 7: the last copy, and the time Hermes, the peer,
and production take to start. About a minute for a small app, to measure
(Verify 4).

`hi app move demo ws1`, on ws2, moves it back the same way. Moving every app
off a machine before its upgrade is a loop of moves; the admin page's
Machine view gets **Move all to…**.

## Release 2: a standby

`hi app standby demo ws2` checks ws2 as a move does, copies the folder there,
and from then on refreshes the copy every 5 minutes (`--every`).

**A round** is a snapshot's steps, with rsync to ws2 in place of restic:

1. a warm pass while everything runs;
2. the broker's locks, then the lead, the memory server, and production
   paused;
3. a pass for what changed since the warm pass;
4. unpaused, the locks released;
5. ws2 told the round is whole.

So the copy is in the state a power cut leaves, as a snapshot is, which
SQLite, PostgreSQL, and git recover from.

**The copy is always whole.** ws2 keeps two copies that take turns: the one
it would start from, and the one being filled. A round writes into the
second with `--inplace`, so a large database costs only its changed blocks,
and when the round is whole ws2 swaps the two. A round that stops halfway,
because ws1 died during it, leaves the first copy as it was. This costs
twice the folder's disk on ws2.

**The snapshots go too.** Each round copies the snapshot repository's new
files (restic adds and deletes files, but never changes one), so a
takeover keeps the app's snapshots, restores, and rollbacks.

**ws2 stays ready.** It builds the app's images when ws1's change, and `hi
update` on ws1 says when ws2's hi is older, which a takeover would refuse.

**Watching.** A round that fails is retried at the next one. A copy older
than three rounds is an incident for the owners and a line on both admin
pages. `hi app standby demo` and the admin page say how old the copy is, how
long the last round paused the app, and what a takeover would lose.

**A move to the standby is quick.** `hi app move demo ws2` with a standby
there copies only what changed since the last round.

### Taking over

`hi app takeover demo`, on ws2, or **Take over** on its admin page:

1. Asks the org server whether ws1 still runs demo ([below](#exactly-one-machine-runs-an-app)).
   If it does, it refuses: "ws1 runs demo. Move it instead: on ws1, `hi app
   move demo ws2`."
2. Says how old the copy is and what goes missing: "demo's copy is from
   14:32, 6 minutes before ws1 stopped answering. What the app saved after
   14:32 is not in it." Asks for the app's name to be typed, as a restore
   does.
3. Starts demo from the copy, with the next epoch (below), and tells the
   org server.
4. The lead posts in the channel: "I was away for 9 minutes while my machine
   was down, and I'm back on another one. Anything saved in the app between
   14:32 and 14:38 may be missing: please check what you did then." Slack's
   messages aren't lost: the broker reads the channel's history again, two
   weeks back, as it does every two minutes.

**When ws1 comes back** it asks before it starts demo, learns that ws2 took
over, and doesn't start it. Its folder may hold minutes the copy never got.
hi can't merge them, so it keeps that folder aside,
`<folder>.before-takeover-<date>`, says so on both admin pages, and offers
to make ws1 the standby: on ws2, `hi app standby demo ws1`.

## Exactly one machine runs an app

An app with a standby runs only while its machine holds a **lease**: a
go-ahead that lasts 60 seconds, which it asks for again every 10. Every move
and takeover adds one to the app's **epoch**, kept in the folder
(`place.json`: the machine, the epoch, the standby) and at the org server.

- **The running machine asks both** the org server and the standby: "I run
  demo at epoch 7; may I go on?" Either answer is enough. Each says yes
  unless it knows a later epoch, and the standby says no once it has begun
  to take over.
- **It stops by itself** when 45 seconds pass with no yes, counted from when
  it asked: the lead, the peer, production, Stage, and the dev box. It
  can't tell "I'm cut off" from "the others think I'm dead", so it acts as
  if they do. It starts them again at the next yes.
- **A takeover needs the org server's yes.** ws2 asks for it only when it
  has given ws1 no yes for 90 seconds, and gives none from then on; the
  org server allows it only when it hasn't given ws1 one for 90 seconds
  either. ws1 stopped by itself at 45 seconds, so it has been stopped for
  at least 45 when ws2 starts.
- **At boot**, an app with a standby starts only after a yes.
- **Planned stops.** `hi update --restart`, and an admin before a reboot
  (`hi app standby demo --away 15m`), tell both "away for up to 15
  minutes": a takeover by itself waits for that long (release 3); an admin
  can still take over.
- **An app without a standby** has no lease, and runs as it does today.

Each party counts with its own clock from the moment it sent its message,
so a slow network only shortens the running machine's time, never makes it
longer.

| What happens | demo, with ws1 running it and ws2 its standby |
|---|---|
| ws1 dies | No yes from anyone; after 90 seconds the org server allows a takeover |
| ws2 dies | ws1 gets its yes from the org server and runs on; the copy is an incident |
| The org server is down | ws1 gets its yes from ws2 and runs on. A takeover needs an admin's word (`--force`) |
| ws1 is cut off from both | ws1 stops demo after 45 seconds; a takeover is allowed after 90 |
| ws1 and ws2 reach each other, not the org server | ws1 runs on; ws2 can't take over by itself |
| ws1 and the org server reach each other, not ws2 | ws1 runs on; ws2's takeover is refused |
| All three cut off from each other | ws1 stops demo; nobody takes over until an admin acts |

**An admin's word.** With no org server, or with it down, `hi app takeover
--force` takes the admin's word that ws1 is off and stays off, with the
app's name typed. hi shows what it can see first: when ws1 was last heard
from, and when its heartbeat service last heard from it. If ws1 is in fact
running and cut off, it stops demo by itself after 45 seconds, and the
admin's word is safe. If ws1 is running and still reaches the org server,
only the admin's word stands between two copies. The page says so.

**The org server must be a third machine.** An org server on ws1 or ws2
can't tell which of them is cut off. Then the standby takes the admin's word
only, and `--auto` is refused.

## Release 3: taking over by itself

`hi app standby demo ws2 --auto` lets ws2 take over when the org server
allows it and ws1 has been gone for `--after`, 5 minutes by default, so a
reboot that runs long doesn't move the app. ws2 then does what `hi app
takeover` does and tells the admins: in the ops channel, on both admin
pages, and as an incident. It never moves the app back by itself: going
back is an admin's `hi app move`, at a time they choose.

The whole stop is about 7 minutes: 90 seconds for the lease, 5 minutes'
wait, and the start.

## When something goes wrong

| What happens | Today | Release 1 | Release 2 | Release 3 |
|---|---|---|---|---|
| ws1 needs a reboot or an OS upgrade | Down while it lasts | Moved first: a minute's stop, twice | The same, shorter: only the last round to copy | The same |
| ws1's disk or board dies | Down until it's mended; the bucket's last snapshot, restored by hand | The same | Running minutes after an admin takes over; up to 5 minutes lost | Running in about 7 minutes; up to 5 minutes lost |
| A release breaks the app's data | `hi app rollback` | The same | The same: the standby copied the damage within minutes | The same |
| ws2 dies | | | demo runs on; an incident for the copy | The same |
| The org server is down | demo runs | demo runs | demo runs; a takeover needs an admin's word | No takeover by itself |
| ws1 loses NetBird | The internal addresses are down; the lead talks on | The same | ws1 stops demo after 45 seconds; an admin may take over | ws2 takes over |

The standby is not a backup. It copies a bad release's damage as faithfully
as good work, within minutes. Going back in time is what snapshots are for.

## What it costs

- **Disk on ws2:** twice the folder (two copies taking turns), and the
  snapshot repository.
- **A pause every round:** the lead, the memory server, and production are
  paused for the last pass. Under a second for a small app; longer for one
  with hundreds of gigabytes of attachments, which rsync has to look at
  each time. hi warns when a round pauses for more than 2 seconds, and
  suggests a longer `--every` (Verify 4).
- **The network:** the whole folder once, then what changes every round.
- **Room on ws2:** enough for the app as well as its own apps. Two machines
  that are each other's standby each need room for both.
- **Trust in ws2:** it holds the app's secrets (its Slack tokens, keys, and
  the peer's identity), which snapshots and the bucket never do.
- **A stop when nobody answers:** an app with a standby stops by itself
  when its machine hears from neither the standby nor the org server.

## Flow

```text
 ws1: runs demo (epoch 7)                                   ws2: demo's standby
┌──────────────────────────────┐   every 5 min, over SSH  ┌──────────────────────────────┐
│ hi-app-demo: lead, peer,     │ ─ warm pass, pause, ───▶ │ hi app __receive             │
│ production, Stage, dev box   │   last pass, unpause     │  ├ copy 1: what it'd start    │
│ app-snapshots/demo           │                          │  ├ copy 2: being filled       │
└──────────────┬───────────────┘                          │  └ images built, same hi      │
               │                                          └───────────────┬──────────────┘
               │ every 10 s: "epoch 7, may I go on?"                      │
               ├──────────────────────────────────────────▶ ws2 says yes  │
               └──────────▶ org server (a third machine) ◀────────────────┘
                            says yes to ws1, or, after 90 s of silence
                            from both, lets ws2 take over at epoch 8
```

## Other ways, and why not

| Way | What it gives | Why not now |
|---|---|---|
| A floating IP (keepalived, VRRP) | An address that moves between two machines | The app's NetBird peer is already an address that moves, and works across networks, not one LAN |
| Replicating each database (Litestream for SQLite, PostgreSQL's streaming replication) | Seconds of loss | Covers only the databases it's set up for. A hi app is a folder: Hermes' databases, Hindsight's, git, uploads, and whatever the app writes in `$HI_DATA_DIR` |
| Filesystem snapshots (btrfs or ZFS send and receive) | An exact copy of a moment with no pause, sending only changed blocks | The best way to make a round, but it needs the apps' folders on btrfs or ZFS on both machines. A later way to make a round, with the same rest |
| Block replication (DRBD) | No loss | A kernel module, a dedicated disk, and its own split-brain handling, for two machines |
| Shared storage (NFS, Ceph) | Nothing to copy | SQLite on NFS isn't safe; Ceph wants three machines and someone to run it |
| Syncing tools (Syncthing, Unison) | Copies both ways | Built to merge edits, which live databases can't take |
| An orchestrator (Kubernetes, Nomad) | Rescheduling on another node | A system of its own, and its storage still has to be one of the above. [hi_cluster.md](hi_cluster.md) studied Nomad and others and took ideas, not the tool |

## Decisions (proposed)

1. **One machine runs an app.** No load-balanced or active-active app: a
   Slack connection, a NetBird identity, and a folder's databases each
   take one.
2. **The address goes with the app.** The peer's identity moves with the
   folder, so nothing the IT team set up changes, and no link breaks.
3. **The folder is what's copied.** No replication per database. A faster
   way to copy, filesystem snapshots, can come later without changing what
   a round is.
4. **A standby is not a backup.** Snapshots stay the way back in time; the
   standby holds them too, so a takeover keeps them.
5. **Safety before staying up.** An app with a standby stops by itself
   when nobody answers, and a takeover needs the org server or an admin's
   word, so the app never runs twice.
6. **Machines talk over SSH on NetBird.** A machine's key on another runs
   only hi's receiving end, not a shell.
7. **The standby holds the secrets.** It is a machine the same admins run;
   the bucket still never holds them.
8. **Never back by itself.** Only an admin moves an app back after a
   takeover.
9. **Admins only.** Moves, standbys, and takeovers are an admin's, on the
   command line or the admin page, said in the app's channel. Owners see
   where the app runs on the dev page.
10. **One standby per app.** Each machine may be the standby for the
    other's apps.

## Verify before building

1. **The peer moves.** Its identity, started on ws2 after stopping on ws1,
   keeps its NetBird address, name, and group, and how soon other devices
   reach it there.
2. **Slack and two connections.** With a test Slack app, two Socket Mode
   connections each get some of the events, which is why only one lead may
   run. Check what a lead that reconnects a minute later receives, and that
   the broker's channel log has the rest.
3. **A folder on another machine.** demo's folder, copied to a machine with
   another path and another user id, starts: Hermes, Hindsight's
   PostgreSQL, git's worktrees (`git worktree repair`), the dev box's home,
   and nothing in the folder holding a path from ws1. Files a box left
   under another user id copy too.
4. **The stops, measured.** A move's stop for a small app, and a round's
   pause for a small app and for one with hundreds of gigabytes of
   attachments.
5. **A public address moves.** After ws1 ends demo's `netbird expose`, ws2
   gets the same name.
6. **The machine domain.** ws2 gets a certificate for demo's https names,
   and ws1's hi forgets demo's DNS record without deleting it.

## Code that changes

| Where | What |
|---|---|
| `app_move.go` (new) | `hi app move`: the check, the warm and last passes, the stop and start, going back when ws2 doesn't answer, the folder left with `moved.json` |
| `app_receive.go` (new) | `hi app __receive`, the forced command: check, receive into its own folder, start, report, swap the copies |
| `app_machines.go` (new) | `hi app machines`: this machine's key, the machines it takes apps from |
| `app_standby.go` (new) | Rounds, the two copies, status, `takeover`, the folder kept aside after a takeover |
| `app_lease.go` (new) | The lease in the app's service: asking, stopping by itself, waiting at boot, planned stops |
| `app_snapshots.go` | The warm pass and the pause, for rounds as well as snapshots |
| `app.go` | `place.json`; `hi app up` refuses a folder that moved or was taken over |
| `net_domain.go`, `app_https.go` | Handing over an app's DNS record |
| `net_expose.go` | Ending and starting a public address by its name |
| `org_server.go` | The witness: signed lease requests, takeover, epochs, from device keys |
| `admin*.go` | **Move…**, **Standby…**, **Take over**, **Move all to…**; where each app runs and its standby, on Home and Overview |
| `app_devpage.go` | Where the app runs and its standby, on the Addresses tab |
| `site/guide/app/` | A page "Two machines" |

## Releases

1. **Move.** `hi app move`, `--check`, `hi app machines`, and the admin page's
   **Move…**. Useful by itself: upgrades without downtime, and a new machine.
2. **A standby, taken over by an admin.** Rounds, the two copies, the lease
   and the org server as witness, `takeover`, the folder kept aside.
3. **Taking over by itself.** `--auto`, `--after`.
4. **Later.** Rounds by filesystem snapshots for large apps, every minute and
   with no pause; a standby chosen from `hi org machines`; the org server's
   admin page showing every machine's apps and standbys (hi_admin.md's
   "Several machines"); a standby for an app whose production is on a
   target ([hi_app_targets.md](hi_app_targets.md)).

## Not in this

- Moving the org server, or a standby for it. It was moved once by copying
  its state folder and pointing its clients at the new one. `hi org move`
  is the same shape, with its own spec.
- Production on a machine of the organization's elsewhere: that is
  [hi_app_targets.md](hi_app_targets.md), where home keeps the control. A
  standby is a second home.

## Open questions

1. **How often a round.** Every 5 minutes, as proposed, costs a short pause
   of the lead and production each time. Less often loses more in a
   takeover.
2. **Stopping by itself.** Is it acceptable that an app with a standby stops
   when its machine hears from neither the standby nor the org server for
   45 seconds? The other way is never to stop, and to allow a takeover only
   on an admin's word, with no release 3.
3. **No third machine.** When the org server runs on ws1 or ws2, should hi
   refuse a standby, or allow one that only an admin's word can take over?
4. **Restoring on a new machine without a standby.** The bucket has neither
   the secrets nor the peer's identity, so an app restored from it
   elsewhere gets a new address and asks for its Slack tokens again.
   Should the bucket hold both, encrypted with a key kept off the machine,
   so a restore keeps the addresses?
5. **Machines that know each other through the org.** When both machines are
   in one org, should their device keys replace `hi app machines add`?
6. **Stage and the dev box in the copy.** The copy has everything, the dev
   box's home too, where owners' unmerged work is. Is that wanted, or only
   production and the lead?

## As built (v2.6.111)

Release 1, with these differences from the draft:

- **The admin page** (v2.6.112): **Move…** among an app's Actions, and
  **Other machines** in the Machine view, where each machine's key is
  shown to copy and another machine's is pasted, so pairing two machines
  needs no terminal. **Move all to…** comes later.
- **The whole folder goes,** `run/` too, but for sockets and PostgreSQL's
  `postmaster.pid`: what's in it (people's link keys, subscriptions,
  Stage's record) is the app's, and `hi app up` copes with boxes that
  aren't there.
- **The receiving machine says where snapshots go** when the move starts,
  rather than the sender assuming the same path.
- **Without a domain there,** the app's https names stop and it stays on
  http at its NetBird name: the app's service already falls back so, and
  leaves the DNS records alone, so they work again on a machine with the
  domain.
- **Checked on two machines (2026-11-10):** an app of 408 MB moved in 40
  seconds, stopped for 24. Its peer came up there with the same NetBird
  address and name (Verify 1, for a planned move), Hermes and Hindsight
  started from the copied folder at the same path (part of Verify 3), and
  the lead was back on Slack's Socket Mode 16 seconds after the start.
