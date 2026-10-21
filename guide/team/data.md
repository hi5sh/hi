---
title: Preview and production data
description: "How a hi team keeps its app's data: production owns it, the preview runs on a fork of it from the last release, and the preview can't act on the world. Step by step, from the first release on."
---

A team's app runs twice: production, which people use, and the preview,
where they try what the team is building. Both run at the same time, and
both keep data. hi keeps them apart with three rules, the same for every
team's app, whatever it does:

1. **Production owns the data.** It builds up in one place, the team's
   `app/` folder, which snapshots keep.
2. **The preview runs on a fork of production.** It sees all of
   production's data as of the last release, in seconds and without a
   copy, however big the data is. Whatever it writes stays its own, so
   nothing tried in a preview reaches production.
3. **The preview doesn't act on the world.** Keys that send, post, order,
   or pay can be production's only, and the preview's model calls stop
   for the day at a limit.

The app needs nothing for this beyond keeping its data in the folder named
by `$HI_DATA_DIR`. `$HI_SLOT` tells it which one it is, `preview` or
`production`, so it can run its background work in full in production
and on a small sample in the preview.

{% include diagram.html name="team-data" %}

## Step by step

What each step does to production's data and the preview's, for an app
that keeps a database (`app.db`) and the files people upload
(`receipts/`).

{% include steps.html name="team-data" %}

## How the fork works

A fork has to be fast, safe for production, and cheap on disk, so hi
treats production's files in two ways:

| Files | What hi does | Why |
|---|---|---|
| Written once: uploads, downloads, PDFs, images, checkpoints | hardlinks them into `run/preview-base/` | a link takes no disk and no time; the preview's box reads them but can't write to them |
| Changed in place: databases (SQLite, DuckDB, a PostgreSQL folder), and whatever production was writing in the last ten minutes | copies them whole into `run/preview-data/` | production keeps writing to them, and the preview needs a copy that holds still |

Production is paused while hi does this, as for a snapshot, so the copies
are consistent: a few seconds. On team cartman, a news app with 157 GB in
210,000 files, the links took under five seconds and the database, 3.6 GB,
about one more. A file is linked however new it is: a download finished an
hour ago won't change again.

The preview's box mounts the fork read only with its own folder over it,
an overlay that Podman mounts without root. Inside the box it is one
ordinary folder at `/data`. A file the preview changes is copied into its
own folder first; a file it deletes is only hidden from it.

## What you see

```text
$ hi team data payments
production  …/team-payments/app: 157.3 GB in 210585 file(s), last changed Wed Oct 21 18:26; its box runs
preview     …/team-payments/run/preview-data: a fork of production v16 from Wed Oct 21 19:40 (157.3 GB in 210585 file(s)), and 3.7 GB of its own in 41 file(s); its box runs
```

The lead runs the same command in its box, and answers from it when people
ask whether the preview has real data or a release uses the same database:
production's data is production's; the preview runs on a fork of it from
the last release; what people try there never reaches production.

## Commands

```sh
hi team data payments                              # both folders, and when the preview was forked
hi team data payments fork                         # the preview gets a new fork now (the lead can too)
hi team data payments copy preview production      # production starts from what the preview gathered
hi team data payments preview empty                # previews start empty instead; preview fork undoes it
hi team env payments --production MARKET_KEY       # a key the preview doesn't get
hi team env payments --preview MAIL_URL            # the preview's own value, such as a test key
hi team limit payments preview 10                  # what the preview's model calls may cost a day
```

- **A new fork** comes at every release, and whenever someone asks. A
  plain preview deploy keeps the fork it has, so what people entered while
  trying a version stays until the next release. The preview's box stops
  for a few seconds at each deploy, since two boxes mustn't write to one
  overlay.
- **A copy to production** needs an owner's yes and snapshots production's
  data first; it is posted in the channel. It is for a preview that
  gathered data before production existed. For a first release, `hi team
  release --data preview` does it in the same step, and `hi team release
  --ask` says when there is data to take.
- **Previews that start empty** are for an app whose people's data the
  preview shouldn't show, such as personal records: the preview starts on
  an empty folder at every release instead of a fork.
- **Settings for one slot**: `--production` keeps a key out of the
  preview, so a worker that forgets `$HI_SLOT` still can't send mail or
  spend a quota from it. `--preview` gives the preview a value of its own;
  production keeps its value. `hi team env payments` lists which slots get
  each.
- **The preview's model limit** is $5 a day unless set (`off` for none). A
  call over it gets an error that says so; production's calls have no
  limit from hi.

## When a fork isn't possible

A fork needs Podman, which mounts the overlay, and the team's folder on one
filesystem, so hi can link. With Docker, or where links don't work, the
preview gets a whole copy of production's data instead, as before forks,
and `hi team data` says why. A release then leaves the preview's data as
it is, since a whole copy at every release would be slow and take the
disk.

Data outside `$HI_DATA_DIR`, such as a database hosted elsewhere, can't be
forked: give the preview its own address with `hi team env <team>
--preview DATABASE_URL`.

See also [The folder and backups](/guide/team/folder/) for snapshots and
rollback, and the spec,
[hi_team_data_fork.md](https://github.com/hi5sh/hi/blob/main/docs/specs/approved/hi_team_data_fork.md).
