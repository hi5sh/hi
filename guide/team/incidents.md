---
title: When something goes wrong
description: "A hi team watches the app's long work and its members' tasks without anyone looking: a job that fails or stops moving, production's app that stops or stops answering, or a task that fails opens an incident, and the lead handles what it can on its own, asks the people about the rest, and logs what it did."
---

An app often does long work of its own: a backfill that downloads and
classifies years of records, an import, a nightly recalculation. It runs
for hours or days, and nobody watches it. When it stops on a bad record,
crashes, or is killed, nothing says so, and the people find out days
later, when they ask.

A hi team watches that work for them. The app says what each job is
doing, hi notices when a job stops, and the lead deals with it: on its
own when it can, by asking the people when it shouldn't decide alone.

## The app reports its jobs

A job is a named piece of long work, such as `backfill-classify`. While
it runs, the app writes `$HI_JOBS_DIR/<job>.json` every few minutes
(to a temporary name, then renamed):

```json
{
  "state": "running",
  "done": 8272,
  "total": 533386,
  "unit": "releases",
  "stall_after": "30m",
  "at": "2026-10-18T14:02:11Z",
  "item": "338809a1",
  "error": ""
}
```

`state` is `running`, `blocked` (it stopped on purpose and needs a
decision), `failed`, or `done`. `stall_after` is how long the count may
stay the same before hi alerts, 30 minutes unless the job says otherwise:
a job that waits on a slow, rate-limited service can say `2h`.

An app that would rather not write JSON runs hi, which is in its box:

```sh
"$HI" team job backfill-classify --done 8272 --total 533386 --unit releases
"$HI" team job backfill-classify --blocked --item 338809a1 --error "no text and no readable PDF"
"$HI" team job backfill-classify --finished
```

You don't write this yourself: every brief tells the members that long
work reports as a job, and that a job which stops on its own (too many
failures in a row, a limit) says so first. The reviewer sends back work
that would stop silently.

A job's workers run in one box at a time: at a release, the old
version's stop before the new version's start (see
[The app's workers at a release](/guide/team/releases/#the-apps-workers-at-a-release)).

An app from before hi watched jobs may have long work that doesn't
report. The first time hi starts a team whose app has merged work and no
job, it posts in the channel and the lead checks the app, then starts a
small task to fix what hi can't see.

**A job never skips an item on its own.** In some jobs one record doesn't
matter. In others every later value depends on the one before: a series
built from the last value, a running balance. Skipping an item silently
makes everything after it wrong, and only the people know which kind a
job is. So a job stops (`blocked`) on an item it can't process, unless
the people gave a rule for that case.

## Watched from outside

Everything above is noticed by the team itself, on its machine. When
that machine is connected to a hi server (`hi connect`), the team also
sends the server a heartbeat every minute, and the server notices what
the team can't: a machine that stopped, a team that keeps restarting, a
disk filling up, snapshots that stopped, the team's key used outside hi.
What the lead can do something about, such as a full disk, comes back
and opens an incident here, which closes when the server's does.
`hi team status` says whether the team is watched, and when its last
heartbeat went. See
[Watch the teams](/guide/compute/managed/#watch-the-teams).

The server also matches what production runs against known advisories
every night. One that is known to be exploited, or likely to be, in the
app's own packages, is an incident here like any other: the lead
proposes the upgrade and waits for a yes. The rest come once a week in
one message.

```sh
hi team bom payments                     # production's bill of materials (CycloneDX)
hi team advisories payments              # what matches production now
hi team advisory payments ignore GHSA-xxxx-xxxx-xxxx --because "we never parse untrusted YAML"
```

An ignored advisory stays quiet until the advisory itself changes.

The server also reads the team's network logs, and the names the app
looks up, and can stop the team: a **freeze** cuts every box off from the
internet (the lead keeps Slack) and pauses the app; a **pause** stops
every box. The team says so in its channel, and only an admin of the hi
server undoes it. A burst of sends to a paste site, or an exploited
advisory in the app's own packages, freezes it on its own. A
task that brings in a package version with an advisory has it listed in
the reviewer's brief; one known to be exploited needs an owner's yes to
merge, like a package that runs its own install script.

## What hi watches

Every minute hi reads production's jobs, and the preview's. It opens an
**incident** when:

- a job is `blocked` or `failed`;
- a job is `running`, but its count hasn't moved in `stall_after`;
- a job is `running`, but hasn't written in `stall_after`: whatever ran it
  died without saying so;
- production's app stopped on its own (hi starts it again, and says how it
  ended and its last lines);
- production's app hasn't answered for five minutes. hi asks `/` on its
  box's port every minute, and any answer counts, a `503` while it starts
  too. An app whose box runs but doesn't answer is often blocked by its own
  work, such as a job on the thread that serves requests, so hi leaves the
  box be and says which jobs it was running. The incident closes when the
  app answers again;
- a member's task failed;
- the lead, or a member, raised one.

An incident is one thread. hi posts it once, in the channel (a task's in
the thread where its work was agreed), and wakes the lead there with
what it would otherwise spend its turn finding: the job's file, the app's
last lines, the rules for that job. A job that stays stuck isn't posted
again, and when it moves again hi says so in the thread and closes the
incident. A preview's jobs are told only in the thread of the work on it.

Watching costs nothing: no model reads the files. The lead is woken only
for an incident, once.

## What the lead does on its own

The lead is there so the people don't have to babysit the work. It acts
alone when the step can be undone, changes no result nobody has ruled on,
and stays within the team's budget:

- it waits, or retries, when the cause passes: a network error, a service
  that was down;
- it restarts the app once per incident (a second time needs a yes). For
  an app that stopped answering while its box runs, it looks first: a
  restart starts the same work again;
- it retries a member's task that failed for a reason outside the work;
- it re-briefs a member whose task failed on the work itself, twice at
  most;
- it applies a rule the people gave, with a small fix when the code
  doesn't follow it yet, and asks an owner for the release.

## What it asks you

It asks when no rule covers a choice that changes results, when a step
can't be undone, when it would cost more, when its own tries ran out, or
when it isn't sure. It says what it found, what depends on it, and the
choices:

> backfill-classify stopped at release 338809a1 (2009-12-29): its page
> has no text and its PDF can't be read.
>
> Nothing later depends on this release's sentiment, so skipping it
> changes nothing else. Choices:
> 1. Tag it invalid and move on, and do the same from now on.
> 2. Tag this one invalid, and stop again at the next.
> 3. Leave the job stopped while someone looks.
>
> I'd take 1. Which?

Your answer becomes the job's rule, kept with your message, so next time
the lead handles the case without asking. If nobody answers in a day, hi
reminds the owners once in the thread; nothing happens to the job
meanwhile.

## What the lead did, and why

Everything the lead does for an incident is logged twice: as a line in
the incident's thread when it happens ("The lead, i-3: restarted the app.
Why: the job went silent while its box ran."), and in the incident's
record in the team's folder, `incidents/i-3.json`. hi writes the record
from the commands the lead runs, not from what the lead says it did. The
backups keep it with the rest of the folder.

| Command | What it does |
|---|---|
| `hi team jobs <name> [--preview]` | Each job's state, how far it is, how many a day (from hi's hourly counts), where it is, and its last error |
| `hi team incidents <name>` | The open incidents, then the last month's |
| `hi team incidents <name> i-3` | One incident's timeline: what hi saw, what the lead did and why, what the people decided |
| `hi team incidents <name> --rules` | Every rule the people gave for the app's jobs, with who and when |

Ask the lead "how many a day?" or "how far is the backfill?" and it reads
the same numbers. A job that hasn't written in a month is no longer
listed; the daily backups keep its history.
