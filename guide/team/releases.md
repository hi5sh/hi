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

When the people are happy with dev, the lead asks an owner whether to
release it, and only that owner's reply lets the broker move production
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
