---
title: Dev and production
description: "A hi team's project has two versions: dev, where every merge goes and people try previews, and production, which moves only on an owner's yes, as numbered releases that can be undone."
---

The project has two versions, and the lead always says which one people
are looking at:

- **Dev** is where every merge goes. The people try it in previews, and
  what doesn't work out can be thrown away.
- **Production** is the version people rely on, on a `production` branch
  of its own, with a link of its own. It changes only when an owner says
  yes. Each release gets the next number: v1, v2, and so on, tagged
  `release-1`, `release-2`.

Each new version gets one line in the channel, and what it brings goes in
that line's thread, where people read it, ask about it, or ask for a fix:

> **cartman** New DEV version (v15 + 2 changes) :test_tube: -> App: link - Docs: link

> **cartman** New PROD version (v16) :rocket: -> App: link - Docs: link

The DEV line comes when the preview starts showing a new dev, after a
merge, with the lead's notes on what's new in its thread (or the merged
work and who asked for it). The PROD line comes with a release, with the
release notes, who approved it, and who asked for each piece in its
thread. Otherwise work for dev stays out of the channel: the lead answers
in the thread or DM where it was asked.

**An owner may release from their DM** with the lead: "push dev to
prod". The lead says there what production would get and asks for a yes;
the owner's yes in the DM releases it, and the channel gets only the PROD
line. A rollback, or production's helper, still needs an owner's yes in
the channel.

When the lead runs `hi team release --ask` in an owner's DM, hi asks
them there. In the channel, hi posts one line, "Ready for production
v4? (v3 + 2 changes)", and in its thread one summary:

> :package: **Ready for production v4?** Dev has what production v3 hasn't:
> • Red badge when overdue (t-12), asked for by Anna in a DM
> • Fix the date format (t-14), a small fix by the lead
>
> Try it on the preview. An owner's yes in this thread releases it as v4.

Only an owner's yes, in that thread or in their DM with the lead, lets
the broker move production (`hi team release`). If a release goes badly, an owner may ask to put
production back on an earlier version.

Every change to production gets its line in the channel: "New PROD
version (v4)", or "PROD back on v3", with "Production v4 is out (it was
v3), approved by Anna" and the lead's notes in its thread. When the
people want everything in dev since the last release gone, the lead sets
dev's code back to production's (after someone says yes). That is posted
too, with what was set aside, and it stays in the project's history.

`hi team status payments` shows production's version and what dev has on
top of it. When a release goes wrong, see
[Rollback](/guide/team/folder/#rollback).

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

The members write workers this way: every brief carries the rule, and
the reviewer sends back a worker that would start regardless. An app
written before hi handed workers over keeps running them in both boxes
for that minute, as before; the first time hi starts its team, the lead
checks the app and starts a small task to change it. The release after
that change still overlaps; the ones after it don't.
