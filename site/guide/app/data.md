---
title: Stage and production data
description: "How a hi app keeps its data: production owns it, Stage runs on a fork of it from the last release, and Stage can't act on the world. Step by step, from the first release on."
---

An app runs twice: production, which people use, and Stage,
where they try what the team is building. Both run at the same time, and
both keep data. hi keeps them apart with three rules, the same for every
app, whatever it does:

1. **Production owns the data.** It builds up in one place, the app's
   `app/` folder, which snapshots keep.
2. **Stage runs on a fork of production.** It sees all of
   production's data as of the last release, in seconds and without a
   copy, however big the data is. Whatever it writes stays its own, so
   nothing tried on Stage reaches production.
3. **Stage doesn't act on the world.** Keys that send, post, order,
   or pay can be production's only, and Stage's model calls stop
   for the day at a limit.

The app needs nothing for this beyond keeping its data in the folder named
by `$HI_DATA_DIR`. `$HI_SLOT` tells it which one it is, `preview` (Stage) or
`production`, so it can run its background work in full in production
and on a small sample on Stage.

{% include diagram.html name="app-data" %}

## Step by step

What each step does to production's data and Stage's, for an app
that keeps a database (`app.db`) and the files people upload
(`receipts/`).

{% include steps.html name="app-data" %}

## How the fork works

A fork has to be fast, safe for production, and cheap on disk, so hi
treats production's files in two ways:

| Files | What hi does | Why |
|---|---|---|
| Written once: uploads, downloads, PDFs, images, checkpoints | hardlinks them into `run/preview-base/` | a link takes no disk and no time; Stage's box reads them but can't write to them |
| Changed in place: databases (SQLite, DuckDB, a PostgreSQL folder), and whatever production was writing in the last ten minutes | copies them whole into `run/preview-data/` | production keeps writing to them, and Stage needs a copy that holds still |

Production is paused while hi does this, as for a snapshot, so the copies
are consistent: a few seconds. On orbit, a news app with 157 GB in
210,000 files, the links took under five seconds and the database, 3.6 GB,
about one more. A file is linked however new it is: a download finished an
hour ago won't change again.

Stage's box mounts the fork read only with its own folder over it,
an overlay that Podman mounts without root. Inside the box it is one
ordinary folder at `/data`. A file Stage changes is copied into its
own folder first; a file it deletes is only hidden from it.

## What you see

```text
$ hi app data payments
production  …/app-payments/app: 157.3 GB in 210585 file(s), last changed Wed Oct 21 18:26; its box runs
preview     …/app-payments/run/preview-data: a fork of production v16 from Wed Oct 21 19:40 (157.3 GB in 210585 file(s)), and 3.7 GB of its own in 41 file(s); its box runs
```

The lead runs the same command in its box, and `hi dev data` shows it in
the dev box. The lead answers from it when people ask whether Stage
has real data or a release uses the same database:
production's data is production's; Stage runs on a fork of it from
the last release; what people try there never reaches production.

## Commands

```sh
hi app data payments                              # both folders, and when Stage was forked
hi app data payments fork                         # Stage gets a new fork now (hi dev data fork in the dev box)
hi app data payments copy stage production        # production starts from what Stage gathered
hi app data payments stage empty                  # Stage starts empty instead; stage fork undoes it
hi app env payments --production MARKET_KEY       # a key Stage doesn't get
hi app env payments --stage MAIL_URL              # Stage's own value, such as a test key
hi app limit payments stage 10                    # what Stage's model calls may cost a day
```

- **A new fork** comes at every release, and whenever someone asks. A
  plain deploy to Stage keeps the fork it has, so what people entered while
  trying a version stays until the next release. Stage's box stops
  for a few seconds at each deploy, since two boxes mustn't write to one
  overlay.
- **A copy to production** is an owner's click in the dev page's Data
  tab, **Copy Stage's data to production**, or an admin's
  `hi app data copy` on the machine. The same tab has **Copy
  production's data to Stage**, a fresh fork now, and **Take a
  snapshot now**. hi snapshots production's data first, and it is posted
  in the channel. It is for a Stage that gathered data before
  production existed: release first, then copy.
- **A Stage that starts empty** is for an app whose people's data
  Stage shouldn't show, such as personal records: Stage starts on
  an empty folder at every release instead of a fork.
- **Settings for one slot**: `--production` keeps a key out of
  Stage, so a worker that forgets `$HI_SLOT` still can't send mail or
  spend a quota from it. `--stage` gives Stage a value of its own;
  production keeps its value. `hi app env payments` lists which slots get
  each.
- **Stage's model limit** is $5 a day unless set (`off` for none). A
  call over it gets an error that says so; production's calls have no
  limit from hi.

## When a fork isn't possible

A fork needs Podman, which mounts the overlay, and the app's folder on one
filesystem, so hi can link. With Docker, or where links don't work, Stage
gets a whole copy of production's data instead, as before forks,
and `hi app data` says why. A release then leaves Stage's data as
it is, since a whole copy at every release would be slow and take the
disk.

Data outside `$HI_DATA_DIR`, such as a database hosted elsewhere, can't be
forked: give Stage its own address with `hi app env <name>
--stage DATABASE_URL`.

See also [The folder and backups](/guide/app/folder/) for snapshots and
rollback, and the spec,
[hi_app_data_fork.md](https://github.com/hi5sh/hi/blob/main/docs/specs/approved/hi_app_data_fork.md).
