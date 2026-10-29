---
title: Dev and production
description: "A hi team's project has two versions: dev, where the owners ship work from the dev box and people try it on the preview, and production, which moves only on an owner's click on the dev page, as numbered releases that can be undone."
---

The project has two versions:

- **Dev** is where the owners' work goes, with `hi dev ship` from the
  [dev box](/guide/team/dev/). The preview shows it, on a fork of
  production's data, for the people to try.
- **Production** is the version people rely on, on a `production` branch
  of its own, with a link of its own. It changes only when an owner
  releases it from the dev page. Each release gets the next number: v1,
  v2, and so on, tagged `release-1`, `release-2`.

## Release

On the dev page's Release tab, **Release v4** shows in Production's row
when dev has work production hasn't. It opens the release notes, drafted from dev's changes; edit them
in the people's words and click Release. hi takes a snapshot, moves
production, gives the preview a new fork of production's data, and posts
one line in the channel, with the notes, who released it, and the work it
brings in its thread:

> **orbit** New PROD version (v16) :rocket: -> App: link - Docs: link

The click is the owner's yes, logged as "Anna on the dev page" in the
versions and the announcement. Work shipped into dev doesn't reach the
channel; the owner's agent says what it wants to with `hi dev say`.

## Going back

The dev page lists the releases. Each one production can go back to has
**Roll back to v3**, which puts back v3's code and keeps production's data
as it is, and, when hi kept production's data as v3 left it, **…and its
data**, which puts that back too. hi snapshots production first, and the
channel gets "PROD back on v3". See [Rollback](/guide/team/folder/#rollback)
for which to choose.

When the owners want everything in dev since the last release gone,
**Set dev back to production**, beside Release, sets dev's code back to what production
runs. What dev had stays in the project's history, and the preview shows
production's code.

On the machine, an admin can do the same without the page:

```sh
hi team rollback payments v3              # code and data; asks first
hi team rollback payments v3 --code-only  # the code alone
```

`hi team status payments` shows production's version and what dev has on
top of it; the lead's `hi team versions` shows the same, and it answers
people's "is it out yet?" from it.

## The app's workers at a release

A release, a rollback, or a restart starts the new version in a box
beside the old one, on the same data, and moves the address to it once
it answers: the app's pages never go down. An app's background workers
(a backfill, a queue, a model call per record) must not run in both boxes
at once. They would do the same work twice, and the new version could
fail to start because the old one's workers hold the database.

So one box of production, and one of the preview, runs the workers at a
time. hi holds a lock for that box, and while it does, the file named by
`$HI_WORKERS_FILE` exists inside it. The app starts its workers once the
file exists and stops them when it goes away. At each release:

1. hi tells the old box to let go, and its workers file goes away.
2. The app stops its workers, putting back what it had in hand, and
   creates `$HI_WORKERS_FILE.stopped`. An app that doesn't say so gets
   30 seconds (`hi team env <team> HI_WORKERS_GRACE=120` for longer, up
   to 300).
3. hi starts the new version. It opens the data with no workers writing,
   migrates, answers, and takes the workers over.
4. The address moves, and the old box is removed.

If the new version doesn't start, hi removes it and the old box takes its
workers back. `hi team jobs <team>` starts with which box runs them, and
when production's box has waited five minutes for them, hi opens an
incident.

The project's `AGENTS.md` gives the agents this rule. An app written
before hi handed workers over keeps running them in both boxes for that
minute, as before; the first time hi starts its team, the lead checks the
app and files a task for the owners to change it. The release after
that change still overlaps; the ones after it don't.
