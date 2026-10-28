---
title: When something goes wrong
description: "A hi team watches the app's long work without anyone looking: a job that fails or stops moving, or production's app that stops or stops answering, opens an incident, and the lead handles what it can on its own, asks the owners about the rest, and logs what it did."
---

An app often does long work of its own: a backfill that downloads and
classifies years of records, an import, a nightly recalculation. It runs
for hours or days, and nobody watches it. When it stops on a bad record,
crashes, or is killed, nothing says so, and the people find out days
later, when they ask.

A hi team watches that work for them. The app says what each job is
doing, hi notices when a job stops, and the lead deals with it: on its
own when it can, by asking the owners when it shouldn't decide alone.

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

When you build long work in the dev box, ask your agent to report it as
a job, and to have a job that stops on its own (too many failures in a
row, a limit) say so first. A second opinion (`hi dev review`) is a good
place to check that nothing would stop silently.

Long work runs off the thread that answers requests, in a worker thread
or a process of its own, with short writes to the database: a backfill
that holds the app's only thread makes production stop answering.
Switching the work off so the app answers is no fix: production must
answer *and* its work must move. Work the app holds back on purpose (off by a setting, or
paused until someone says) writes `blocked`, with how to resume it, so
the lead asks you rather than taking it for dead.

A job's workers run in one box at a time: at a release, the old
version's stop before the new version's start (see
[The app's workers at a release](/guide/team/releases/#the-apps-workers-at-a-release)).

An app from before hi watched jobs may have long work that doesn't
report. The first time hi starts a team whose app has merged work and no
job, it posts in the channel and the lead checks the app, then files a
task for the owners to fix what hi can't see.

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
app's own packages, is an incident here like any other: the lead files a
task for the upgrade, which the owners make in the dev box. The rest come once a week in
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
advisory in the app's own packages, freezes it on its own. An admin of
the server can also revoke the team's OpenRouter key, when the server
made it (`hi team key <name> openrouter --from-server`), and take the
team's machine off the NetBird network. `hi dev ship` refuses work that
brings in a package with an advisory known to be exploited, or that
trusts a new package's install script.

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
- the lead raised one.

An incident is one thread. hi posts it once, in the channel, and wakes
the lead there with
what it would otherwise spend its turn finding: the job's file, the app's
last lines, the rules for that job. A job that stays stuck isn't posted
again, and when it moves again hi says so in the thread and closes the
incident.

Watching costs nothing: no model reads the files. The lead is woken only
for an incident, once.

## What the lead does on its own

The lead is there so the people don't have to babysit the work. It acts
alone when the step can be undone, changes no result nobody has ruled on,
and stays within the team's budget:

- it waits, or retries, when the cause passes: a network error, a service
  that was down;
- it restarts the app once per incident. A second restart is the
  owners': the lead asks, and an owner clicks **Restart** on the
  incident on the dev page. For an app that stopped answering while its
  box runs, the lead looks first: a restart starts the same work again;
- it applies a rule the owners gave;
- it files a task when the fix is a change to the code, and says its
  number in the incident's thread.

## What it asks you

It asks the owners when no rule covers a choice that changes results,
when a step can't be undone, when the fix is a change to the code, when
its own tries ran out, or when it isn't sure. It says what it found, what depends on it, and the
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

An owner keeps the decision as the job's rule: under **Rules for the
app's jobs** on the dev page, the job's name and the rule in your words,
**Add the rule**. From then on the lead handles the case without asking.
A rule that no longer holds is removed there too. If nobody answers in a
day, hi reminds the owners once in the thread; nothing happens to the job
meanwhile.

## On the dev page

Each open incident is on the owners' [dev page](/guide/team/dev/#the-dev-page),
with **Restart**, which gives production's app (or the preview's) a new
box, and **Close**, with why. Both are logged in the incident with the
owner's name, as the lead's steps are.

Waiting is a choice too. When the lead would leave production's work
stopped beyond the incident's own cause, such as until a fix is merged
and released, it says what waiting costs and asks, rather than waiting on
its own. A production incident about a job, the app, or its workers that
has stood for 4 hours with nobody asked gets a line to the owners in its
thread, once, so a stopped backlog never sits overnight unseen.

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
| `hi team incidents <name> i-3` | One incident's timeline: what hi saw, what the lead and the owners did and why |
| `hi team incidents <name> --rules` | Every rule the owners gave for the app's jobs, with who and when |
| `hi dev incidents [<id>]` | In the dev box: the same, read-only, for an owner's agent to look into |

Ask the lead "how many a day?" or "how far is the backfill?" and it reads
the same numbers. A job that hasn't written in a month is no longer
listed; the daily backups keep its history.
