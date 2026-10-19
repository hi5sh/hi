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
