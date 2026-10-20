---
title: The folder and backups
description: "Everything a hi team knows and has made lives in one folder; hi snapshots it daily and before every release, restores it, rolls production back with its data, and copies it offsite."
---

Everything a team knows and has made is in one folder: `team-<name>` in
the folder where you ran `hi team new`, or wherever `--dir` puts it. hi
finds it by name from anywhere, through a link in `~/.local/share/hi/teams`.

| Path | What |
|---|---|
| `team.json` | Purpose, channel, owners, members |
| `secrets.json` | The Slack tokens, readable only by you |
| `lead/` | The lead's Hermes home: `SOUL.md`, its memory, its sessions |
| `repo/` | The project's git repository; `repo/docs/specs/` holds what the lead and the people agreed, one file per feature, and `notes/` there the lead's notes for the members |
| `roles/` | A folder per member's role: `ROLE.md`, how it works on this team, and `LEARNED.md`, what its earlier tasks taught it |
| `tasks/` | Each task's brief and report |
| `incidents/` | What went wrong while nobody looked, and what the lead did about it, one file each; `rules.json`, the people's decisions about the app's jobs. See [When something goes wrong](/guide/team/incidents/) |
| `channel/` | The channel's log, a file per day, and the files people shared |
| `memory/` | The lead's long-term memory: Hindsight's database |
| `app/` | Production's data: what the app keeps in `$HI_DATA_DIR` |

The containers are rebuilt from the folder on every start, so a copy of the
folder is a copy of the team. To move it, stop the team, move the folder,
and start it by its new folder: `hi team up ~/projects/team-payments`. The
same works for a folder copied from another machine. `secrets.json` is
listed in the folder's `.gitignore`, in case the folder sits inside a
repository. `lead/SOUL.md`
is the lead's identity: edit it to change how the lead works, then
`hi team up payments` to restart it. Conversations already going, a DM
with the lead too, follow the new SOUL.md from their next message.

## Snapshots and restore

The code is in git, but the lead's memory, the tasks, the channel's log,
and the app's data are only in the team's folder. hi snapshots them:

- **every day**, around 03:00, while the team is up;
- **before every release**, and before production goes back to an earlier
  one, so each version has the data and memory as they were when it went
  out;
- **when you ask**: `hi team snapshot payments`.

The team keeps running. hi waits until no merge or release is half done,
pauses the lead, its memory, and production for about a second while
restic copies the folder, and lets them go. A snapshot of an idle team adds
a few kilobytes: restic stores only what changed, encrypted.

```sh
hi team snapshots payments
```

```text
ID        TAKEN             WHY          SIZE      NEW
cfd8fa7c  2026-10-13 03:12  daily        226.2 MB  208.6 MB
2938aa9e  2026-10-14 03:40  daily        226.2 MB  30.1 KB
5d1e0b77  2026-10-14 11:02  before v4    226.3 MB  41.9 KB
```

To put the team back as it was, name a snapshot by its ID, `latest`, the
version it was taken before (`v4`), or a day (`2026-10-13`, its last):

```sh
hi team restore payments v4
```

hi says what it puts back and asks first. It stops the team, takes a
snapshot of how things are now, puts back the lead's memory, the tasks, the
channel's log, and production's data, and starts the team again. The
channel hears what happened, with the snapshot that undoes it. The code
stays as it is unless you add `--code`, and so do the team's settings
(`team.json`, `secrets.json`).

Snapshots live in `~/.local/share/hi/team-snapshots/<name>/`, outside the
team's folder, with the key they are encrypted with. They stay when the
team is removed. hi keeps the last 7 days, 4 weeks, and 6 months, every
snapshot from the last week, and every snapshot taken before a release.
When a snapshot fails, the owners hear about it in the channel, and
`hi team status` says so until one works.

## Rollback

When a release goes wrong, production can go back to an earlier version
two ways:

- `hi team release v3` (the lead, once an owner says yes) puts back v3's
  code and keeps production's data as it is. That's right when the new
  version is merely wrong.
- `hi team rollback v3` puts back v3's code *and* production's data as v3
  left it, from the snapshot taken when production moved on. That's for a
  release that damaged the data. What was entered since is set aside in a
  snapshot, and rolling forward the same way puts it back.

The lead proposes a rollback in the channel and says what will be set
aside; only an owner's yes carries it out. On the machine, an owner runs it
directly:

```sh
hi team rollback payments v3          # asks first; --code-only keeps the data
```

Neither touches the lead's memory, the tasks, or the channel's log; that
is `hi team restore`.

## Offsite copies

A copy on the same disk doesn't survive the disk. Give the
team an S3-compatible bucket (Hetzner, Cloudflare R2, Backblaze B2, AWS,
and others), and every snapshot is copied there too:

```sh
hi team backups payments --offsite s3:https://fsn1.your-objectstorage.com/acme-backups/payments
```

hi asks for the bucket's access key without echoing it, copies the
snapshots taken so far, and copies each new one in the background. Add
`--region` for a bucket outside `us-east-1`, and `--keep 14,8,12` to keep
more daily, weekly, and monthly snapshots. The bucket can only be read with
the snapshots' key, so keep a copy of it somewhere other than this machine:

```sh
hi team backups payments --show-key
```

## The app's data

An app box keeps what it must not lose in
`$HI_DATA_DIR` (`/data`). Production's is the team's `app/` folder, which
outlives every release and is in every snapshot. Each preview has a data
folder of its own, apart from production's. The app's own folder is a
fresh copy of the code at each release, so anything written there is
lost. The lead and the coder are told to keep data in `$HI_DATA_DIR`.

The preview and production run at the same time. `$HI_SLOT` tells the app
which one it is (`preview` or `production`). Work that costs money, such
as a model call per item, a backfill from an outside service, or mail,
runs only in production; otherwise it runs twice, on the same keys.

A release moves the code, never the data. To move data between the two:

```sh
hi team data payments                              # both folders: size, files, last change
hi team data payments copy production preview      # try a change on production's data
hi team data payments copy preview production      # production starts from what the preview gathered
```

A copy pauses the box it copies from, so the copy is consistent, and
restarts the other on it. A copy to production snapshots production's data
first and is posted in the channel. A copy to the preview keeps the
preview's data from before in `run/preview-data-before/`. The lead can ask
for either: the preview's copy on its own, production's with an owner's
yes. For a first release, the lead can take the preview's data along
(`hi team release --data preview`), and the release summary says when
there is data to take.
