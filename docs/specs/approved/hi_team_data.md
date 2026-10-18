# `hi team` data across preview and production specification

Status: Approved (2026-10-18). Built in v0.86.0.

Dependencies: [hi_team.md](hi_team.md) (the broker, previews, releases),
[hi_team_snapshots.md](hi_team_snapshots.md) (the app's data folders,
snapshots, rollback).

## Goal

A team's app can gather or compute data once and use it in both preview and
production. Work that costs money or reaches outside services runs once. A
production that starts empty doesn't rebuild what the preview already
built.

## What happened

Team cartman built a news app. In the preview, its background workers:

- read 533k press releases from a news site;
- cached 7.7k PDF attachments (844 MB);
- read 696k daily prices from a market-data API;
- scored 8.3k releases with a model, one call each.

The first release started production on an empty `app/`. Production read
the same site again and called the model again for the same releases,
about 40 calls a minute. The preview's workers kept running beside it. Both
boxes used the team's one OpenRouter key, the same market-data key, and the
same IP towards the site.

When asked whether the release would use the same database, the lead said
no but didn't say what that meant. Afterwards it told the owner to copy
files on the machine by hand, because hi had no command for it.

## The problem

hi keeps the two data folders apart (`run/preview-data/` and `app/`) and
gives both boxes the same environment. That leaves three gaps:

1. **An app can't tell where it runs.** Without that, it can't keep paid or
   outside work to production. Every worker runs in both boxes, and twice
   during a deploy's overlap.
2. **Data never moves between the slots.** A release moves code only.
3. **The lead has nothing to say.** It can't offer a command, so it gives
   wrong or vague answers.

## Which data goes where

| Kind | Examples | Rule |
|---|---|---|
| What the people enter | accounts, entries, uploads, settings | production's alone; a preview never writes it |
| What comes from outside, or from code over it | feeds read, prices, downloaded files, model answers | gathered once, by production; a preview reads a copy |
| Test data | anything a preview makes while being tried | the preview's alone; thrown away freely |

So production owns the truth and runs the workers that cost something. A
preview works on a copy of production's data. One database shared by both
is out: a preview's migration would rewrite production's live data, and a
preview's bug would corrupt it.

Data goes from the preview to production only once: when the preview
gathered what production needs before production existed, as for cartman.
It needs an owner's yes, and a snapshot of production comes first.

## What changes

### `$HI_SLOT`

Every app box gets `HI_SLOT`, set to `preview` or `production`. Users can't
set it with `hi team env`, as with `HI_DATA_DIR`.

The lead's skill, the brief, and the project's `AGENTS.md` say:

> Work in the background that costs money (a model call per item), reads
> an outside service a lot (a backfill, a scrape), or does something
> outside the app (sends mail, posts, places orders) runs only when
> `HI_SLOT` is `production`. In a preview it does nothing, or a small sample
> such as the last few days, and the preview gets production's data with
> hi team data copy production preview.

### `hi team data`

```text
hi team data <name>                          the data folders: size, files, last change, and
                                             whether each box runs
hi team data <name> copy <from> <to>         one slot's data folder becomes a copy of the other's:
                                             preview production, or production preview
    [--yes]                                  don't ask first
```

In the lead's box:

```text
hi team data                                 the same listing
hi team data copy production preview         the preview gets a copy of production's data; no yes needed
hi team data copy preview production --approval <id>
                                             production gets a copy of the preview's data; an owner's yes
```

A copy, with the broker's locks held so no release, rollback, snapshot, or
deploy runs meanwhile:

1. Checks that the source has data and that the disk has room for the copy
   and 256 MB more.
2. For production: takes a snapshot first ("before data from the preview").
   If production has data and the snapshot fails, nothing is copied.
3. Pauses the source's box, so the copy is consistent. A paused SQLite
   database copies like one after a power cut, which SQLite recovers from.
4. Removes the target's box.
5. Copies the folder with `cp -a` beside the target (a `.restore-` name
   for production, so snapshots skip it), then swaps it in.
   - Production's old data is in the snapshot.
   - The preview's old data is kept once, as `run/preview-data-before/`.
6. Unpauses the source and starts the target's box again on the same
   address. For production, hi posts in the channel what happened, who
   approved it, and which snapshot holds production's data from before.
   `versions.json` records it, so the yes can't be used again.

Production is unreachable while its box restarts. The preview is unreachable
while it is copied from (seconds per gigabyte).

### Releasing with the preview's data

`hi team release --approval <id> --notes <file> --data preview` releases
and gives production a copy of the preview's data in one step, under the
same yes. hi team release --ask adds a line when production has no data
yet and the preview has some:

> Production starts with no data of its own; the preview has 1.6 GB. Say
> whether production should start with a copy of it.

The lead uses `--data preview` only when the owner's yes says so.

### The lead

- The lead's skill has a section on the app's data: the table above, the
  `$HI_SLOT` rule, and the two copy commands.
- Before the first release, when the preview gathered data production
  needs, the lead says so in the ask and offers to take it along.
- Asked "will the release use the same database?", it answers with this:
  separate folders, and how to copy.
- `hi team data` lists the folders, so the lead can say how much there is.

## Not in this

- **A cache of model calls in hi's models proxy, shared by both slots.**
  It would make a preview's repeat calls free with no change to the app.
  Later, with spending caps per slot.
- **A preview that starts from a copy of production automatically.** Copying
  stays something the lead asks for. Previews deploy many times a day, and
  wiping what people entered while trying one would surprise them.
- **Deploys with no overlap.** A new box starts beside the old one on the
  same data folder. Apps keep migrations backwards-compatible for that
  minute.
- **Databases other than files in `$HI_DATA_DIR`,** such as PostgreSQL in a
  sidecar box.
