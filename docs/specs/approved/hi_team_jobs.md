# `hi team jobs`: the app's long work, watched

Status: Approved (2026-10-19), built in v0.93.0. Drafted 2026-10-18 after a
production backfill sat stopped on one record for days without anyone
knowing.

Dependencies: [hi_team.md](../approved/hi_team.md) (the broker, the lead,
approvals, preview and production), [hi_team_data.md](../approved/hi_team_data.md)
(`$HI_DATA_DIR`, production's data), and `hi team say` (v0.86), whose
path to the lead an alert reuses.

## Goal

A team's app often does long work of its own: a backfill that downloads
and classifies years of records, an import, a nightly recalculation. It
runs for hours or days inside the app's box, and nobody watches it. When
it stops, on a bad record, a crash, or a killed process, nothing says so.
The people find out when they ask, sometimes days later, and the lead
then spends its turn finding out what happened instead of answering.

hi already tells the people when a member's task fails: the broker knows,
and the progress card shows it. It can't know that an app's worker has
stopped, because to hi a stopped worker and an idle one look the same.
The app has to say what it is doing, and hi has to notice when it stops
saying it.

So:

- **The app reports its jobs** in a small, fixed form any language can
  write.
- **The broker watches them** without a model: a job that fails, goes
  quiet, or whose box stops raises an alert in the channel and wakes the
  lead once.
- **A member's failed task wakes the lead too**, as an incident of the
  same kind.
- **The lead handles what it can and asks about the rest.** The lead is
  there so the people don't have to babysit the work: it retries, restarts,
  re-briefs, and applies the rules the people gave, on its own. It asks a
  person when a choice would change results nobody has ruled on, can't be
  undone, or goes past what it may spend.
- **Everything the lead does on its own is logged**: in the incident's
  thread as it happens, and in the incident's record in the team folder,
  so anyone can see later what it decided and why.
- **Status questions take one step**: `hi team jobs` gives every job's
  state, count, rate, and last error.

## What a job reports

A job is a named piece of long work, such as `backfill-classify`. The app
writes one file per job, `$HI_JOBS_DIR/<job>.json` (in the app's box,
`/jobs`, mounted from `run/<slot>-jobs/` in the team's folder). It
writes to a temporary name and renames, so hi never reads half a file:

```json
{
  "job": "backfill-classify",
  "state": "running",
  "done": 8272,
  "total": 533386,
  "unit": "releases",
  "stall_after": "30m",
  "at": "2026-10-18T14:02:11Z",
  "item": "338809a1",
  "error": "",
  "note": "oldest first; 10 workers"
}
```

| Field | Meaning |
|---|---|
| `state` | `running`, `blocked`, `failed`, or `done` |
| `done`, `total`, `unit` | progress; `total` may be left out when unknown |
| `stall_after` | how long `done` may stay the same before the job counts as stalled (default 30m); each job sets what is normal for it, so one that waits on a rate-limited API can say 2h |
| `at` | when the app last wrote the file; a heartbeat even when `done` doesn't move |
| `item` | the item the job is on, or stopped at |
| `error` | why it is `blocked` or `failed`, in a sentence a person can read |
| `note` | anything else worth showing |

`blocked` means the job stopped deliberately and needs a decision: an
item it can't process and a rule that says not to guess. `failed` means
it broke. The difference matters to the lead (below), not to the broker,
which alerts on both.

For apps that would rather not write files, the `hi` binary is already in
the app's box (the relay that starts it, as `$HI`), and gives the same
thing in one call. What a call doesn't say stays as the file had it, and
a job that runs again drops its last error:

```sh
"$HI" team job backfill-classify --done 8272 --total 533386 --unit releases
"$HI" team job backfill-classify --blocked --item 338809a1 --error "the release has no text and its PDF can't be read"
"$HI" team job backfill-classify --finished
```

The states are `--running` (the default), `--blocked`, `--failed`, and
`--finished`; `--done` is the count.

Every brief carries the rule, after the drawing rule: any work that can
outlive a request (a loop over records, a queue consumer, a scheduled
recalculation) reports as a job, and stops on an item it can't process
unless its spec gives a rule for the case. The team-app template's coder
role says the same, for new teams.

A job that stops on its own for any other reason, such as too many
failures in a row or a limit it reached, writes `failed` or `blocked`
with why before it stops: one that only stops looks idle to hi. Long work
a change touches that doesn't report yet is made to report in the same
change, and the reviewer counts long work that would stop without saying
so as a change needed (v0.96.1).

### Work written before

An app written before v0.93.0 may run long work that never reports, and
hi can't see it stop. In one team, a history worker stopped after five
failures in a row, kept only its own status, and nobody heard. So, once
per team, when dev has merged work and no job has ever reported, hi posts
in the channel that the lead checks the app's long work, and wakes the
lead in that thread. The lead reads dev for such work, starts the fix as
one small task when any doesn't report or stops without saying so, and
asks an owner for the release once it is merged. A team with no merged
work is marked as checked without asking, since every brief and review
carries the rule. hi keeps that it asked in `run/jobs-checked`.

## What the broker watches

Every minute the broker reads production's and the preview's jobs, as it
already checks production's address. It raises an alert when:

- **A job is `blocked` or `failed`.**
- **A job is stalled**: `running`, but `done` hasn't changed in
  `stall_after`.
- **A job is silent**: `running`, but `at` is older than `stall_after`.
  Whatever ran it died without saying so.
- **Production's app stopped on its own**, whether or not a job was
  running: the box's exit code (137 is said to be a kill, often for
  memory), the jobs that were running, and the last lines of its log are
  in the alert. hi already starts a stopped production again; the
  incident is posted before it does. A box that stopped before the team
  started (a reboot, `hi team down`) isn't one.

## Incidents

For the first 10 minutes after the team starts, hi holds a job's
incident. Starting the team (a new hi, a reboot) stops hi's proxy for
the app's model calls and starts the app in a new box, so a job can trip
on calls cut short; one that moves again in that time raises nothing.
One still failed, blocked, stalled, or silent after it opens its
incident then, and says when hi started the team. (In v0.98.0 orbit's
history analysis failed on ECONNRESET 14 seconds after an update
restarted the team, and was running again a minute later.)

An incident is one thread, and one record. The broker posts the alert
once, in the channel, and writes the record, `incidents/i-<n>.json` in
the team's folder. A job that stays stuck gets no second alert. The
thread gets one line when it recovers ("backfill-classify is running
again: 8,410 of 533,386"), and the incident closes. A job that fails
again later opens a new one. The preview's jobs alert only in the thread
of the task that is shown, never in the channel, since a preview is
somebody's work in progress.

A member's task that fails is an incident too: a box that didn't start, a
run that hit `--max`, an agent that crashed or gave up, a report that says
it couldn't finish. Its alert goes in the thread where the work was
agreed, beside the progress card, not in the channel.

Nothing in the watching costs a model call. The lead is woken only for an
incident, once.

## What the lead does

The broker wakes the lead for the incident with a job in its Hermes that
runs once, as it does when a wait missed the end of work, with what the
lead would otherwise spend its turn finding: for a job, its file, the
box's last log lines, and the people's rules for it. The lead answers in
the incident's thread. A failed task's incident comes to the lead with
the task: `hi team wait` prints it, and when no wait is there, hi's
wake-up for work that ended says it.

It first finds out why, reading only: the item, the logs, the code, the
job's history, the task's report. Then it decides whether it can handle
the incident alone.

### What it handles alone

The lead is there to take the micromanaging off the people, so most
incidents should end without anyone being asked. It acts on its own when
the action can be undone, changes no result nobody has ruled on, and
stays within the team's budget:

- **Wait or retry** when the cause passes: a network error, a service
  that was down, a rate limit. It checks back once the job should have
  moved.
- **Restart the app's box** when a job went silent while the box still
  runs, once per incident: `hi team jobs restart [--preview] --incident
  <id> --because "<why>"`. A second restart needs a person's yes
  (`--approval <id>`). A box that stopped hi starts again itself.
- **Retry a member's task** that failed for a reason outside the work (the
  box didn't start, the agent's service was down), once, with `--on`.
- **Re-brief a member** whose task failed on the work itself: a brief that
  was too large is split, an unclear one is made clearer, a missing
  detail is added. Twice at most; then it asks.
- **Apply a rule the people gave.** A job's rules are what the people
  decided about it, kept in the job's spec and the lead's memory: "a
  release with no text and no readable PDF is tagged invalid and the job
  moves on". When the job's code doesn't follow the rule yet, the lead
  starts the fix as a small task (`--small`). It reaches production as
  every change does, with an owner's yes for the release, and the lead
  asks for that yes in the same thread.

### What it asks about

It asks a person when:

- **No rule covers a choice that changes results.** Above all, what to do
  with an item a job can't process (below).
- **The action can't be undone**: deleting or rewriting data already
  computed, a rollback.
- **It would spend more** than the team's budget leaves, or more than the
  incident is worth.
- **Its own attempts ran out**: the box stopped again after a restart, a
  re-briefed task failed twice.
- **It isn't sure** what happened.

It says what it found, what depends on it, and the choices, each with
what it would do to the results, and which it would take:

> backfill-classify stopped at release 338809a1 (2009-12-29): its page has
> no text and its PDF can't be read.
>
> Nothing later depends on this release's sentiment, so skipping it
> changes nothing else. Choices:
> 1. Tag it invalid and move on, and do the same for such releases from
>    now on (a rule for this job).
> 2. Tag it invalid and move on, and stop again at the next one.
> 3. Leave the job stopped while someone looks.
>
> I'd take 1. @owner, which?

The answer becomes the job's rule, recorded with who gave it. The lead
keeps it in its memory and in the job's spec, and the coder's next brief
for that job carries it. The next time the case comes up, the lead
handles it alone.

The lead marks the question with `hi team incidents ask <id> "<the
question>"`. If nobody answers in the thread within a day, hi posts one
reminder there, @-mentioning the owners. Nothing happens to the job
meanwhile.

A rule needs the people's message: `hi team incidents rule <job> "<the
rule>" --approval <id> [--incident <id>]`, which hi checks as it checks a
yes for work, and keeps in `incidents/rules.json` with who gave it. Every
wake-up for that job carries its rules.

### Skipping is never the default

In some jobs one item doesn't matter: a release that can't be read is
just missing. In others every later value depends on it: a series
computed from the one before, a running balance, a model fed the last
result. There, skipping an item silently makes everything after it wrong.
Only the people know which kind a job is. So a job's code stops
(`blocked`) on an item it can't process unless a rule for that case has
been recorded, and the lead asks rather than assumes. When the people
answer, the lead also records which kind of job it is, so the next
question can say so.

## What the lead did, and why

Everything the lead does on its own in an incident is logged twice:

- **In the thread, as it happens**: one line per action, saying what and
  why ("Restarted the app's box: it was killed for memory at 14:02, and
  the job had been silent since. Once per incident."). People who follow
  the channel see the decisions without asking for them.
- **In the incident's record**, `incidents/i-<n>.json`: when it was
  raised and by what, what the lead found, each action (who took it,
  which command, why, what happened), each question and its answer with
  who answered, the rule it produced, and how the incident closed.

hi writes the record, not the lead: the lead's actions go through hi's
commands (`hi team task`, the restart, the rule), each with `--incident
<id> --because "<why>"`, and the broker appends them. A task started for
an incident is followed: hi notes how it ended, a failure of it stays in
the same incident, and a failed task's incident closes once the work
after it is done. A note the lead writes itself is kept too, marked as
its own. So the record shows what was done, not only what the lead says
it did.

```text
hi team incidents [<name>]                 open incidents, then the last month's
hi team incidents [<name>] <id>            one incident's timeline
hi team incidents [<name>] --rules         every rule the people gave, by job, with who and when
hi team incidents ask <id> "<question>"    the lead: waiting for the people (hi reminds once)
hi team incidents note <id> "<text>"       the lead: a note of its own
hi team incidents close <id> --because ... the lead: resolved
hi team incidents rule <job> "<rule>" --approval <id> [--incident <id>]
hi team jobs restart [--preview] --incident <id> --because ... [--approval <id>]
hi team jobs forget <job> [--preview] --because ...   a job a release took out
```

On the machine the commands take the team's name and read its folder, so
they work while the team is down; in the lead's box they go to its
socket.

Incidents live in the team's folder, beside `tasks/`, so snapshots and the
daily backups keep them.

## Status questions

```text
hi team jobs [<name>]           every job in production: state, done/total, rate per day, last error
hi team jobs --preview          the preview's
hi team jobs --json
```

The rate comes from the broker's own samples of `done`, one an hour, in
`run/jobs.json`. So "how many per day" is a number hi already has, not
something the lead works out from the database. The lead's hi-team skill
says to answer status questions with this first.

In the incident that led to this spec, the lead used up its 60 turns on a
question this answers in one.

Job records and samples older than a month are dropped: by then they are
stale, and the daily backups keep the history. A job that hasn't written
its file in a month is no longer listed.

## `hi team alert`

A smaller, separate thing, for the lead's own cron jobs and turns:

```sh
hi team alert "production's export has been returning 500 since 14:00"
```

It opens an incident, with the alert style in the channel, and wakes the
lead as any incident does. A member's box can't reach the broker, so a
member that notices production broken while working on something else
puts a line starting with `Alert:` in its report's follow-ups, which
every brief says, and hi opens the incident when the task ends, instead
of the note being buried in the report. Alerts are rate-limited per team
(one a minute, ten a day). They don't cover the app's jobs, which report
as above.

## Decisions

- **The broker watches; the lead doesn't poll.** A cron job that asks the
  lead to check production every hour costs a turn every hour, and still
  misses a crash between checks. A crash should be caught by plain code,
  with the model woken only to deal with it.
- **Files, not an endpoint.** Every language can rename a file. The relay
  in the app's box only relays the model socket today, and an HTTP
  endpoint would mean one more thing in the box that must stay up for the
  watching to work. `$HI team job` is there for apps that want a call.
- **The lead acts alone on what can be undone.** People shouldn't have to
  approve a retry or a restart. They are asked about choices that change
  results, can't be undone, or cost more, and about anything the lead
  tried twice.
- **No skip by default; rules per job instead.** Whether an item can be
  dropped depends on the job, and only people can say. A rule, once
  given, saves asking again.
- **hi keeps the log, not the lead.** A record the lead writes about
  itself is only as good as its memory of the turn. The broker records
  each command as it runs.
- **One alert per incident.** A job that stays stuck for a day must not
  post 1,440 times. The thread is where it is discussed and where it
  recovers.
- **A member's failed task is an incident.** The progress card shows the
  people a failure, but the lead learned of it only from the report, if
  at all.
- **`stall_after` per job.** What counts as stuck depends on the job.
  Per state can come later if a job needs it.
- **A month of job history.** Older records are stale, and the daily
  backups keep the history. Incidents are kept, as tasks are.
