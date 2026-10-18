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

Work for dev stays out of the channel: the lead answers in the thread or
DM where it was asked, and work asked for in a DM, or a small fix the lead
started on its own, goes to the owners in a DM. The channel hears about
dev once, before a release. When the people are happy with dev, the lead
runs `hi team release --ask`, and hi posts one summary in the channel:

> :package: **Ready for production v4?** Dev has what production v3 hasn't:
> • Red badge when overdue (t-12), asked for by Anna in a DM
> • Fix the date format (t-14), a small fix by the lead
>
> Try it on the preview. An owner's yes in this thread releases it as v4.

Only an owner's yes in that thread lets the broker move production
(`hi team release`). If a release goes badly, an owner may ask to put
production back on an earlier version.

Every change to production is posted in the channel itself, not in a
thread: "Production v4 is out (it was v3), approved by Anna", with the
lead's notes on what changed, or "Production is back on v3". When the
people want everything in dev since the last release gone, the lead sets
dev's code back to production's (after someone says yes). That is posted
too, with what was set aside, and it stays in the project's history.

`hi team status payments` shows production's version and what dev has on
top of it. When a release goes wrong, see
[Rollback](/guide/team/folder/#rollback).
