# `hi team` data: a preview runs on a fork of production

Status: Approved (2026-10-21). Built in v1.1.0 (see [As built](#as-built)).
Replaces the copy to the preview and the data rules in
[hi_team_data.md](hi_team_data.md).

Dependencies: [hi_team.md](hi_team.md) (the broker, previews,
releases), [hi_team_data.md](hi_team_data.md) (`$HI_SLOT`, the
two data folders, `hi team data`),
[hi_team_snapshots.md](hi_team_snapshots.md) (pausing boxes,
snapshots), [hi_team_keys.md](hi_team_keys.md) (settings, the
models socket).

## Goal

One way of handling data that suits most apps a team builds, whatever the
app does, and asks nothing of the app beyond keeping its data in
`$HI_DATA_DIR`:

- Production's data is safe from a preview's code.
- A preview shows production's real data, as of the last release, at once
  and at no cost in disk, however big the data is.
- Work that costs money or acts outside the app happens once.
- A change to how the app gathers or works out its data can be tried in a
  preview before it reaches production.

## What happened

v0.86.0 gave each slot its own data folder, `$HI_SLOT`, and `hi team data
copy`. Three days later, team orbit's data looks like this:

| | Database | Downloaded files | Last change | Box |
|---|---|---|---|---|
| production (`app/`) | 3.6 GB | 155 GB, 210k files | today | runs |
| preview (`run/preview-data/`) | 1.4 GB | 47 GB | two days ago | stopped |

- **The preview falls behind.** Orbit's workers run only in production
  (its decision 0013, following the lead's skill), so the preview holds
  what it had when it was last copied, and less every day.
- **Catching up costs a full copy.** `hi team data copy production
  preview` duplicates 157 GB on disk and leaves the preview unreachable
  while it copies. Most of that is PDFs that never change once downloaded.
- **The heart of the app can't be tried in a preview.** Orbit is mostly
  its workers: reading releases, scoring them, training a model. With
  workers off in a preview, a change to any of them first runs for real in
  production.

## The problem

hi_team_data.md sorts data into what people enter, what comes from
outside, and test data, and gives each slot a separate folder that data
only crosses by a full copy. That fits an app whose data is a small
database of what people entered. It fits poorly for an app whose data is
mostly gathered or downloaded, and those are many of the apps teams build:
news and feeds, research, quant, document search, media.

Asking each app to sort its own data (a shared folder for downloads, its
own for the rest) would fix orbit, but every app, coder, and lead would
have to get the sorting right, and an app that gets it wrong loses data or
pays twice. One pattern that needs no sorting is better.

## What others do

Hosted app builders and platforms have met the same question:

- **Replit: separate databases, a dev one that starts empty.** The schema
  moves to production on publish; data doesn't. Copying production's data
  back to dev is a script people write by hand. Replit's agent can't touch
  production's database.
- **Lovable: isolated test and live databases, withdrawn.** Lovable
  shipped separate Test and Live databases in February 2026 and stopped
  offering them to new projects on March 24, "iterating on this feature
  based on beta feedback". People reported orbit's problem in reverse:
  the agent wrote data into Test while the live app read an empty
  database. Lovable's newer Drafts share the project's live data.
- **Neon with Vercel: a copy-on-write branch of production per preview.**
  Neon forks production's database for every Vercel preview deploy in
  about a second, with no added storage until the branch changes.
- **Vercel: settings per environment.** A setting can have one value in
  production and another, or none, in previews.

What these show: when the two slots' data never meet, it surprises people,
whichever slot fills up first. Forking production per preview is the one
approach that keeps production safe and gives the preview real data, and
settings per environment are how previews stay out of the outside world
without the app's code having to remember.

## The pattern

Three rules for every team's app:

1. **Production owns the data.** Data builds up in one place: production's
   `app/`, which snapshots keep.
2. **A preview runs on a fork of production.** It sees all of production's
   data as of the fork, and whatever it writes stays its own. Nothing a
   preview does can change production's files.
3. **A preview doesn't act on the world.** Settings that send, post,
   order, or cost per call can be production's only. Model calls from a
   preview have a daily limit. `$HI_SLOT` stays, for an app's code to keep
   its workers to production.

### Which apps it fits

| App | Its data | Under the pattern |
|---|---|---|
| A tool people fill in (entries, accounts, settings) | a small database | the preview shows real entries; a release's migration has run on a copy of them first |
| Gathered data (feeds, scrapes, prices, documents) | a database and many downloaded files | the preview has all of it at once; its workers can run on a sample |
| Media and uploads | files written once | the preview shows them at no cost in disk |
| An agent or automation (mail, posts, orders) | a small database; the risk is what it does | production's keys stay out of the preview |
| A model or analysis that trains | checkpoints, results | the preview starts from production's latest |
| A page over outside APIs | little or none | no change |

Where the pattern doesn't fit, see [Variants](#variants).

## What changes

### The fork

A fork is made with the broker's locks held, as a copy is now:

1. Pause production's box, as for a snapshot.
2. Hardlink production's folder into `run/preview-base/` (`cp -al`). Each
   file is linked, not copied, so this takes seconds and no disk.
3. Copy the files production changes in place into
   `run/preview-data/`, which starts empty: databases (SQLite and its
   `-wal`, `-shm`, `-journal`; DuckDB; a PostgreSQL data folder) and any
   file changed in the last ten minutes, which production may have been
   writing as it paused (a day until v1.1.1). These are copied in full, not
   linked, so production writing to them later changes nothing in the
   preview.
4. Unpause production.
5. Start the preview's box with its data folder as an overlay:
   `run/preview-base/` below, read only, and `run/preview-data/` above,
   holding everything the preview writes. Podman does this rootless
   (`-v base:/data:O,upperdir=…,workdir=…`).

The app sees one ordinary folder at `$HI_DATA_DIR`. Production's own
folder, and how snapshots take it, don't change.

Measured on this machine (ext4, kernel 7.0, Podman 5.7) with orbit's
production data: hardlinking 155 GB in 210k files took 4.6 s and used no
disk; copying the 3.6 GB database took about 1 s from cache. In a Podman
box, a write to a file from the base went to the upper folder and left the
base's file as it was.

A file a preview deletes or changes is hidden or copied up into its own
folder. The links in `run/preview-base/` keep a file's blocks on disk
after production deletes it, until the next fork.

### When a preview gets a new fork

- **At every release.** After a release the preview runs production's
  code. It starts again on a new fork, and its own writes from before are
  kept once, as `run/preview-data-before/`. The next migration then runs
  on a fresh copy of production's real data before it reaches production.
- **When the lead or an owner asks:** `hi team data fork` in the lead's
  box, no yes needed.
- **Not on a plain preview deploy.** Previews deploy many times a day, and
  wiping what people entered while trying one would surprise them.

A team whose production has no data yet gets an empty preview, as now.

### Settings per slot

```text
hi team env <name> KEY=VALUE                     both slots, as now
hi team env <name> --production KEY=VALUE        production only; a preview doesn't have it
hi team env <name> --preview KEY=VALUE           the preview's own value, such as a test key
```

`hi team env <name>` lists each setting's name and which slots have it. The
lead's skill says: a key that sends mail, posts, places orders, or pays
per call is set `--production`, or `--preview` with a test key, so a
preview can't act with it even when the code forgets `$HI_SLOT`.

### Models

The models socket counts each slot's spending. A preview's model calls
stop for the day at a limit, $5 by default (`hi team limit <name> preview
<usd>`), and the box gets an answer that says so. Production has no limit
from hi.

### `$HI_SLOT`

Stays as it is. With a fork, a preview's workers don't need production's
work redone: the results are already in the fork. The lead's skill and
`AGENTS.md` say a preview's workers run on a small sample (the last few
days), so a change to them can be tried, and production's run in full.

### Commands

```text
hi team data <name>                              both folders: size, last change, whether each box
                                                 runs; for the preview, when it was forked and how
                                                 much it wrote itself
hi team data <name> fork                         the preview gets a new fork of production
hi team data <name> copy preview production      production gets the preview's data (base and its own
    [--yes]                                      writes, flattened), after a snapshot; an owner's yes
```

`hi team data <name> copy production preview` stays as another name for
`fork`.

### The lead

- The skill's data section becomes the three rules, the fork, and when a
  preview gets a new one.
- Asked "does the preview have real data?" or "will the release use the
  same database?", the lead says: production's data is production's; the
  preview has a fork of it from the last release (date, size); what people
  try in the preview never reaches production.
- When a brief adds a setting that sends, posts, orders, or pays, the lead
  tells the owner to set it with `--production`, naming the command.

## Variants

Two cases where the pattern bends. Each is a setting, not a new pattern.

- **People's personal data that previews shouldn't show.** `hi team data
  <name> preview empty` makes the preview start from an empty folder (or
  the app's `make seed`) at every release instead of a fork. Back with
  `preview fork`.
- **Data outside `$HI_DATA_DIR`,** such as a hosted database. hi can't
  fork it. A `--preview` setting gives the preview its own database
  address.

The other way round, a preview that gathered data before production
existed (orbit's first release), stays as it is: `hi team release
--data preview` with an owner's yes.

## Risks

- **Production changes an old file in place.** A file linked into the
  base and later changed in place by production changes under the
  preview too. The preview may read a half-written file; production is
  never affected. Databases are copied, and so is whatever production was
  writing as it paused, so this is rare: other files are written once. A filesystem with reflinks (XFS, Btrfs) avoids it:
  there hi would reflink every file instead of hardlinking them.
- **Two preview boxes on one overlay.** A preview deploy starts the new
  box beside the old one. In a test both boxes mounted the same upper
  folder and saw each other's writes, but the kernel says sharing an
  upper folder between mounts is unsupported. So a preview deploy on a
  fork stops the old box first: a few seconds without a preview.
- **Docker.** hi falls back to Docker when Podman is missing. Docker has
  no overlay volumes, so a fork there is a full copy, as `copy` is today.
- **A data folder on another filesystem.** Hardlinks need `run/` and
  `app/` on one filesystem; otherwise the fork is a full copy, and `hi
  team data` says so.
- **The pause.** Production is paused for the links and the copies,
  under 10 s for orbit. Copying SQLite with its backup API instead, and
  pausing only for other databases, is for later if the pause shows.
- **The model limit.** $5 a day, for the preview only; whether members'
  boxes get one too is for later.

## Orbit under this

- Its preview shows all 157 GB of production's data, as of the last
  release, using a few GB of its own: the 3.6 GB database and the last
  day's files.
- Its workers in the preview run on the last few days, so a change to
  scoring or training is tried on real releases before it is released.
- Its market-data key is set `--production`, so a preview can't spend its
  quota even if a worker forgets `$HI_SLOT`.
- `run/preview-data-before/` and the 47 GB in today's preview folder go
  at the first fork.

## Not in this

- **A cache of model calls shared by both slots.** It would make a
  preview's repeat calls free. Later, with the limits above in place.
- **Forks for members' boxes,** so the coder tests on real data, not made
  up answers. Later, read only, on the same base.
- **More than one preview,** each on its own fork.
- **Deploys with no overlap for production.** As in hi_team_data.md.

## As built

v1.1.0:

- `team_data_fork.go` forks: it finds the databases (SQLite and DuckDB by
  their first bytes, `-wal`, `-shm`, `-journal`, anything under a folder
  with `PG_VERSION`) before production pauses, then, paused, hardlinks
  every other file into `run/preview-base/` and copies the databases and
  the files changed in the last ten minutes into `run/preview-data/`.
  `run/preview-fork.json`
  says when, from which version, and how much.
- The preview's box mounts `-v run/preview-base:/data:O,upperdir=run/preview-data,workdir=run/preview-work`.
  Production's mount doesn't change.
- Reflinks: the copies use Go's `copy_file_range`, which shares blocks on
  XFS and Btrfs; the base stays hardlinks everywhere. A reflinked base is
  for later.
- `hi team data copy preview production` and `release --data preview`
  merge a forked preview on the host: the base linked, the preview's own
  files copied (never written through a link), and its deletions left
  out, as the overlay marks them: a 0:0 character device for a file,
  `user.overlay.opaque` (or `trusted.`) for a folder, and the `.wh.`
  names of fuse-overlayfs.
- The fork at a release happens after production shows the new version,
  and only with Podman and production having data. The lead hears it in
  `hi team release`'s output; the channel doesn't.
- `hi team data <name> preview empty` sets `preview_data` in team.json;
  the preview then starts on an empty folder at every release. hi doesn't
  run a seed target; the app seeds itself if it does.
- Settings per slot are `env_production` and `env_preview` in
  secrets.json, beside `env`; a name is in one of them or in `env`.
- Each slot has its own models socket, `<models>/production/` and
  `<models>/preview/`, which its box mounts. The socket in the models
  folder itself stays, as production's, for boxes started by an older hi
  until they restart. The preview's socket adds up the last `"cost"` in
  each answer (`usage.cost`, which OpenRouter always sends, last in a
  streamed one) in `run/models-preview.json`, per UTC day, and answers 429
  in OpenRouter's error shape at the limit. `hi team limit` sets
  `preview_model_usd` in team.json.
- Orbit's first fork keeps its 47 GB preview folder once, as
  `run/preview-data-before/`, until the next fork replaces it.

v1.1.1:

- The fork copies files changed in the last ten minutes, not the last
  day. Orbit's first fork, on v1.1.0, copied 124,321 files, 102 GB: a
  backfill had downloaded that many PDFs that day, each written once. It
  took three minutes, with production paused, and 102 GB of disk. A file
  written once is linked however new; databases are found by their bytes.

v1.14.2:

- **When new work goes on the preview.** `hi team preview t-N` for work
  whose chain isn't the one on the preview (dev, or another piece of
  work) forks production again first, when the fork is over an hour old.
  A fix or follow-up of the work on the preview keeps its fork, and what
  people tried in it. The preview's writes from before are kept once, as
  at a release. `hi team preview` says so to the lead.
- Found on orbit (2026-10-24): the preview was forked at v20's release,
  and the runs and OMXS30 history checked that morning came after it, so
  the coder found an empty preview. An owner asked for the copy by hand.
- Not on every task, as asked first: a fork pauses production for a few
  seconds, and a piece of work has many tasks.
