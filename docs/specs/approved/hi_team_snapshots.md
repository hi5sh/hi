# `hi team` snapshots and restore specification

Status: Approved (2026-10-14). Built in v0.55.0 (see [As built](#as-built)).

Dependencies: [hi_team.md](hi_team.md) (the team's folder, the broker,
releases, and [Backups and rollback](hi_team.md#backups-and-rollback), which
this builds the first half of).

## Goal

A team that loses its lead's memory, its record, or the app's data can be
put back as it was yesterday, or as it was before the last release, with one
command, and without the team going quiet in Slack while snapshots are taken.

## What there is today

- Code is safe: every merge is a commit, every release a tag, pushed when the
  team has a remote.
- Nothing else is. The lead's memory (`lead/`, `memory/`), the tasks, the
  channel's log, and `versions.json` are in the folder and nowhere else.
- The app has no data folder. Each deploy runs a fresh copy of the code in
  `run/app-<slot>-<port>/`, so whatever the app writes is lost at the next
  release. Team bulletin's app keeps its news in `data/latest.json` there.

## The app's data

An app box gets a data folder at `/data`, named by `$HI_DATA_DIR`, that
outlives deploys:

| Box | Folder | In snapshots |
|---|---|---|
| production | `app/` | yes; it is the people's real work |
| preview | `run/preview-data/`, over a fork of production in `run/preview-base/` ([hi_team_data_fork.md](hi_team_data_fork.md)) | no; previews may be thrown away |

The `hi-team` skill and the project's `AGENTS.md` tell the lead and the
coder that the app keeps what it must not lose in `$HI_DATA_DIR`, and
nothing it needs later in its own folder.

## Commands

```text
hi team snapshot <name>                take a snapshot now; the team keeps running
hi team snapshots <name>               list them: when, why, how much was new, offsite or not
hi team restore <name> <snapshot>      put the team back as it was then
    <snapshot>                         an ID from hi team snapshots, latest, v3 (the one taken
                                       just before v3 went out), or a day: 2026-10-12 (its last)
    [--code]                           repo/ too; without it the code stays as it is
    [--yes]                            don't ask first
hi team backups <name>                 where snapshots are kept, how long, and offsite
    [--offsite s3:https://<host>/<bucket>[/<path>] | --offsite off]
                                       copy every snapshot to an S3-compatible bucket; the keys
                                       are asked without echo (or HI_TEAM_S3_ACCESS_KEY_ID and
                                       HI_TEAM_S3_SECRET_ACCESS_KEY)
    [--keep <daily>,<weekly>,<monthly>]  how many to keep; 7,4,6 by default
    [--show-key]                       print the key the snapshots are encrypted with
```

`hi install restic` installs restic from Ubuntu. `hi team up` warns when it
is missing; snapshots fail, and say so, until it is there.

## What a snapshot holds

Everything in the team's folder except:

- `secrets.json`: a snapshot copied offsite must not hold the Slack tokens.
- `run/`, which `hi team up` rebuilds, except `run/proxy/network.log`.
- sockets, and PostgreSQL's `postmaster.pid`, which can keep a restored
  memory server from starting.

So: `team.json`, `versions.json`, `lead/`, `memory/`, `tasks/`, `channel/`,
`logs/`, `app/`, and `repo/`, which is small and is the only copy for a team
without a remote.

## Where snapshots are kept

A restic repository per team in `~/.local/share/hi/team-snapshots/<name>/`,
outside the team's folder, with the key that encrypts it (`key`, 0600) and
hi's record of the last snapshot and copy (`status.json`). They stay when
`hi team rm` deletes the folder, and `hi team rm` says where.

restic encrypts and deduplicates: a daily snapshot of an idle team adds a
few kilobytes. Team bulletin's folder (230 MB) takes about a second the first
time and under one after.

## Taking one without stopping the team

The team's service takes the snapshot, since it holds the moving parts. The
host's `hi team snapshot` asks the broker on its socket, and takes it itself
only when the team is down.

1. The broker takes both its locks, so no merge, task start, release, or
   preview is half done.
2. hi pauses the lead's box, the memory server, and production's box
   (`podman pause`). The Slack connection outlasts a pause of a second.
3. restic backs up the folder.
4. hi unpauses them and releases the locks, then copies the snapshot
   offsite in the background.

A frozen copy restores like a power cut, which SQLite, PostgreSQL, and git
recover from. A box that won't pause is noted on the snapshot, and the
snapshot is still taken.

## When

- **Daily**, from a systemd user timer next to the team's service
  (`hi-team-<name>-snapshot.timer`, around 03:00, caught up after a
  reboot). `hi team up` turns it on, `down` and `rm` turn it off. After
  the daily snapshot hi forgets old ones: the last 7 days, 4 weeks, and 6
  months by default, everything from the last 7 days, and every snapshot
  taken before a release.
- **Before every release**, and before production goes back to an earlier
  one, labelled with the version it is for. A snapshot that fails doesn't
  stop the release; the owners hear about it.
- **By hand**, with `hi team snapshot`.
- **Before a restore**, so a restore can be undone.

A snapshot or an offsite copy that fails is posted in the channel for the
owners, and shown in `hi team status` until one succeeds.

## Restore

`hi team restore payments v3`:

1. says what it puts back, and asks;
2. stops the team's service and production's box;
3. takes a snapshot of how things are now (`before restoring <id>`);
4. restores the snapshot into a folder beside the others, and swaps it in:
   everything a snapshot holds, except the team's settings (`team.json`,
   like `secrets.json`), `repo/` unless `--code` (or the folder has none),
   and the network log, whose past is kept;
5. starts the team again if it was up; production starts on its link with
   the restored data within a minute;
6. posts in the channel what was put back, and the snapshot that undoes it.

`team.json` and `secrets.json` stay as they are: owners, members, the
channel, the remote, and the bucket are settings, and going back on them
would surprise whoever changed them. Restoring a team whose folder is gone,
or on another machine, is `hi team import`'s job, later.

## Offsite

`--offsite` takes a restic repository URL for an S3-compatible bucket, and
`--region` its region, `us-east-1` by default: restic hangs looking the
region up from most S3-compatible services, which accept `us-east-1`. hi
initializes it with the local repository's chunker parameters, so copies
deduplicate, and after each snapshot runs `restic copy` to it. The keys are
in `secrets.json`; the URL and the retention in `team.json`. The bucket's
snapshots use the same key as the local ones: keep a copy of it
(`hi team backups <name> --show-key`) somewhere other than this machine,
or the bucket can't be read if the machine is lost.

## Tests

- A snapshot holds the folder without `secrets.json`, `run/`, or sockets,
  but with the network log; its tags say why it was taken.
- The broker holds its locks and pauses the boxes while restic runs, and
  unpauses them when restic fails.
- A release takes a snapshot labelled with its version first; a failed
  snapshot leaves a warning, not a failed release.
- Restore picks a snapshot by ID, `latest`, version, and day; keeps
  `secrets.json`, `run/`, and (without `--code`) `repo/`; takes a snapshot
  first; and removes what is newer than the snapshot.
- Production and preview boxes get their data folders.
- With restic on the machine, a real snapshot and restore round-trip.

## Rollback (release 2)

Status: Approved (2026-10-14). Built in v0.56.0.

A release that damaged production's data needs its code and its data
put back together. `hi team release v2` already puts back the code, and
keeps the data, which is what a release that is merely wrong needs.

```text
hi team rollback <name> v2 [--code-only] [--yes]   on the machine
hi team rollback v2 --approval <id>                the lead, with an owner's yes
```

- **Which data.** Production's data as v2 left it: the snapshot taken the
  last time production moved off v2. Each move of production records in
  `versions.json` the versions it went between and the snapshot taken just
  before it. A version left before v0.56.0, or whose snapshot failed, has
  none, and rolls back its code only (`release v2`, `--code-only`).
- **What goes back.** Production's code and `app/`. Not the lead's memory,
  the tasks, or the channel's log: a lead that forgot why it rolled back
  would be worse off. That is `hi team restore`.
- **Undo.** The snapshot taken before the rollback is production's data as
  the newer version left it, so rolling forward to it the same way puts
  that data back. A rollback whose snapshot of now fails moves nothing.
- **Who.** An owner's yes in the channel, checked by the broker as for a
  release; this replaces the spec's owner's button, because the yes is
  already how owners approve releases. On the machine, whoever runs
  `hi team rollback` is trusted: it goes through a second broker socket,
  `$XDG_RUNTIME_DIR/hi-team-<name>-host/broker.sock`, which no box
  mounts. `hi team snapshot` moved there from the lead's socket.
- **Steps.** With the broker's lock held: the snapshot of now, production's
  box stopped, `app/` restored beside the folder and swapped in, the
  production branch moved, the move recorded. Then production starts on
  its link and the channel hears it, with the data's date and the
  snapshot that undoes it.

## Not in this

- Export and import, and restoring on another machine.
- The app's database dumped with its own tool rather than paused.

## As built

v0.55.0, as specified, with:

- `hi install restic` installs Ubuntu's restic (0.18.1 on 26.04).
- The repository and `key` are in `~/.local/share/hi/team-snapshots/<name>/`;
  restic's tags are `hi-team`, `kind:<daily|release|manual|restore>`, and
  `label:<version or snapshot>`, on host `hi-team-<name>`.
- A team still running a hi from before v0.55.0 answers the broker's new
  `/snapshot` with 404; `hi team snapshot` then takes the snapshot itself,
  with the boxes paused but without the broker's locks.
- Checking for the bucket's repository is given two minutes, since restic
  keeps retrying a bucket it can't reach.

Checked on the live team bulletin (226 MB): the first snapshot took 4.4 s
with the repository's set-up, the next 1.5 s and 30 KB; the lead, the
memory server, and production were running again after each. Its snapshot
restored with Hermes' three SQLite databases passing `integrity_check`, and
Hindsight's PostgreSQL ran crash recovery ("not properly shut down;
automatic recovery in progress") and accepted connections. Offsite copies,
`--keep`, `status`, and a restore that keeps the bucket's settings were
checked against an S3 server (`rclone serve s3`).

## As built (v1.14.4)

- **A first pass before the pause.** A snapshot now runs restic once
  while everything runs, tagged `kind:warmup`, then pauses the boxes and
  runs it again: the paused pass reads only what changed since the first,
  and the first pass's snapshot is forgotten. Found on orbit
  (2026-10-24): v21's release snapshot paused the lead, its memory, and
  production for about 4 minutes, against 17 seconds at v20's that
  morning, because a backfill had grown the app's attachments to 266 GB
  and its database to 5.6 GB in between, and restic read all of it with
  the boxes frozen.

## As built (v1.14.9)

- **A restart doesn't leave the team paused.** While the boxes are paused
  the service keeps a note, `run/paused`, with its process id and the
  containers, and while it releases, `run/releasing`. `hi update
  --restart` waits for both to go before restarting a team (up to 30
  minutes; a note of a process that has stopped doesn't count). As it
  starts, the service unpauses what was left paused, production, the
  preview, and the containers the note names, and logs each.
