# `hi team jobs`: the app's long work, watched

Status: Draft (2026-10-18).

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
- **The lead investigates and either resolves it or asks a person**, with
  what it found and the choices. It never decides on its own what to do
  with data it couldn't process.
- **Status questions take one step**: `hi team jobs` gives every job's
  state, count, rate, and last error.

## What a job reports

A job is a named piece of long work, such as `backfill-classify`. The app
writes one file per job, `$HI_JOBS_DIR/<job>.json` (in the app's box,
`/hi/jobs`, mounted from `run/<slot>-jobs/` in the team's folder). It
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
| `stall_after` | how long `done` may stay the same before the job counts as stalled (default 30m) |
| `at` | when the app last wrote the file; a heartbeat even when `done` doesn't move |
| `item` | the item the job is on, or stopped at |
| `error` | why it is `blocked` or `failed`, in a sentence a person can read |
| `note` | anything else worth showing |

`blocked` means the job stopped deliberately and needs a decision: an
item it can't process and a rule that says not to guess. `failed` means
it broke. The difference matters to the lead (below), not to the broker,
which alerts on both.

For apps that would rather not write files, the `hi` binary is already in
the app's box (the relay that starts it), and gives the same thing in one
call:

```sh
"$HI" team job backfill-classify --running --done 8272 --total 533386
"$HI" team job backfill-classify --blocked --item 338809a1 --error "the release has no text and its PDF can't be read"
"$HI" team job backfill-classify --done
```

The team's templates and the coder's role instructions carry the rule:
any work that can outlive a request (a loop over records, a queue
consumer, a scheduled recalculation) reports as a job. A reviewer asks for
it when it is missing.

## What the broker watches

Every minute the broker reads production's and the preview's jobs, as it
already checks production's address. It raises an alert when:

- **A job is `blocked` or `failed`.**
- **A job is stalled**: `running`, but `done` hasn't changed in
  `stall_after`.
- **A job is silent**: `running`, but `at` is older than `stall_after`.
  Whatever ran it died without saying so.
- **The app's box stopped** while a job was `running`: the box's exit
  code and the last lines of its log are in the alert.

An incident is one thread. The broker posts the alert once, in the
channel, and keeps that thread's record in `run/jobs.json`. A job that
stays stuck gets no second alert. The thread gets one line when it
recovers ("backfill-classify is running again: 8,410 of 533,386"), and
the incident closes. A job that fails again later opens a new one. The
preview's jobs alert only in the thread of the task that is shown, never
in the channel, since a preview is somebody's work in progress.

Nothing in the watching costs a model call. The lead is woken only for an
incident, once.

## What the lead does with an alert

The broker starts a lead turn for the incident, the way `hi team say`
does, with what the lead would otherwise spend its turn finding: the
job's record, the item, the error, the box's last log lines, and how far
the job had come. The lead's answer goes in the alert's thread.

The lead's instructions for an alert:

1. **Find out why**, reading only: the item, the logs, the code that
   stopped, the job's history. Nothing in production changes.
2. **Check whether the job has a recorded rule for this case.** A job's
   rules are what the people decided about it before, in the project's
   specs and the lead's memory: "a release with no text and no readable
   PDF is tagged invalid and the job moves on". Only a decision someone
   made counts as a rule. The lead's own guess doesn't.
3. **Then either resolve it or ask:**
   - **It resolves it** when the cause is something that passes (a
     network error, a service that was down), and the job is running
     again by the time the lead answers. It says what happened. It also
     resolves it when a recorded rule covers the case and the job's code
     doesn't follow it yet: it proposes the fix as ordinary work, which
     waits for a yes as all work does.
   - **It asks a person** in every other case: no rule covers it, the
     fix would change data already computed, or it isn't sure. It says
     what it found, what depends on the item (does later work use it?),
     and the choices, each with what it would do to the results:

     > backfill-classify stopped at release 338809a1 (2009-12-29): its
     > page has no text and its PDF can't be read.
     >
     > Nothing later depends on this release's sentiment, so skipping it
     > changes nothing else. Choices:
     > 1. Tag it invalid and move on, and do the same for such releases
     >    from now on (a rule for this job).
     > 2. Tag it invalid and move on, and stop again at the next one.
     > 3. Leave the job stopped while someone looks.
     >
     > I'd take 1. @owner, which?

The answer becomes the job's rule. The lead keeps it in its memory and in
the job's spec, and the coder's next brief for that job carries it. The
next time the case comes up, the lead resolves it without asking.

**Skipping is never the default.** In some jobs one item doesn't matter:
a release that can't be read is just missing. In others every later value
depends on it: a series computed from the one before, a running balance, a
model fed the last result. There, skipping an item silently makes
everything after it wrong. Only the people know which kind a job is. So a
job's code stops (`blocked`) on an item it can't process unless a rule for
that case has been recorded, and the lead asks rather than assumes. When
the people answer, the lead also records which kind of job it is, so the
next question can say so.

If nobody answers in the thread within a day, the lead posts one reminder
there, @-mentioning the owners. Nothing happens to the job meanwhile.

## Status questions

```text
hi team jobs [<name>]           every job in production: state, done/total, rate per day, last error
hi team jobs --preview          the preview's
hi team jobs --json
```

The rate comes from the broker's own samples of `done`: one an hour, a
week of them, in `run/jobs.json`. So "how many per day" is a number hi
already has, not something the lead works out from the database. The
lead's hi-team skill says to answer status questions with this first.

In the incident that led to this spec, the lead used up its 60 turns on a
question this answers in one.

## `hi team alert`

A smaller, separate thing, for the members and the lead's own cron jobs:

```sh
hi team alert "production's export has been returning 500 since 14:00"
```

It posts in the channel with the alert style and wakes the lead as an
incident would. A member that notices production broken while working on
something else uses it instead of burying the note in its report. It is
rate-limited per team (one a minute, ten a day). It doesn't cover the
app's jobs, which report as above.

## Decisions

- **The broker watches; the lead doesn't poll.** A cron job that asks the
  lead to check production every hour costs a turn every hour, and still
  misses a crash between checks. A crash should be caught by plain code,
  with the model woken only to deal with it.
- **Files, not an endpoint.** Every language can rename a file. The relay
  in the app's box only relays the model socket today, and an HTTP
  endpoint would mean one more thing in the box that must stay up for the
  watching to work. `$HI team job` is there for apps that want a call.
- **No skip by default, per job rules instead.** See above.
  Whether an item can be dropped depends on the job, and only people can
  say. A rule, once given, saves asking again.
- **One alert per incident.** A job that stays stuck for a day must not
  post 1,440 times. The thread is where it is discussed and where it
  recovers.

## Open questions

- Should a member's failed task also wake the lead as an incident? The
  progress card shows it, but the lead learns of it only from the
  member's report.
- `stall_after` per job, or per job and per state (a job that waits on a
  rate-limited API may sit still for an hour on purpose)?
- How long to keep a finished job's record and samples: a week, or until
  the next release?
