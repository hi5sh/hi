# `hi team`: one box runs the app's workers, handed over at every deploy

Status: Approved (2026-10-22). Built in v1.3.0.

Dependencies: [hi_team.md](../approved/hi_team.md) (the broker, deploys
that start a new box beside the old one), [hi_team_jobs.md](../approved/hi_team_jobs.md)
(the app's long work, reported as jobs), [hi_team_data.md](../approved/hi_team_data.md)
and [hi_team_data_fork.md](../approved/hi_team_data_fork.md) (`$HI_SLOT`,
`$HI_DATA_DIR`, the preview's fork), [hi_team_keys.md](../approved/hi_team_keys.md)
(`hi team __app`, the relay that starts an app's box).

Code: `team_workers.go`.

## Goal

At any moment one box of a slot runs the app's background workers, and
the box that stops running them stops before the next one starts. A
deploy no longer runs the same backfill twice for a minute, and a new
version no longer fails to start because the old one is busy writing.
The people's pages keep answering through a deploy, as now.

## What happened

hi deploys without downtime: a new box starts beside the old one, on the
same data folder, and the address moves to it once it answers; then the
old box is removed. For a web page that is right. For an app with
background workers, it means two copies of every worker for that minute,
on one database.

Team orbit's app is mostly workers: a history backfill, a history
analysis that asks a model about each release, a model that trains
through history. On 2026-10-21:

- **Production failed to start four times**, at 14:58, 15:22, 19:51, and
  20:00 UTC: "the app stopped before it answered", with
  `SQLiteError: database is locked` at `openDatabase`, reading
  `PRAGMA user_version`. The old box's workers held the write lock; the
  new box gave up. Production stayed on the old version until hi tried
  again a minute later. Two of these were hi's own restarts after an
  update.
- **Two incidents** (i-2, i-4) were jobs failing on "database is locked":
  the history backfill couldn't list what to read, and the history
  analysis stopped after five failures in a row.
- **Four commits in dev that afternoon** (17:50 to 18:17) worked around
  it in the app: WAL, a 10-second busy timeout, listening before opening
  the database, retrying a locked migration, reporting a job waiting on
  the lock (the app's decision 0032). They aren't in production yet.
- During each overlap both boxes' workers ran: each box's history
  analysis could take the same releases from the queue and ask the model
  about them, the same calls paid twice.

Every app with workers meets this, and each would solve it on its own,
in its own code, after it breaks.

## The problem

The app can't solve it alone. A box doesn't know another box of the same
app is running, or which of the two should stop: only hi knows that a
deploy is under way, and which box is the new one. And the order matters.
A lock that only keeps the new box's workers waiting until the old box is
gone wouldn't have helped orbit: its new box failed while opening the
database, before any worker started, because the old box's workers were
writing. The old box's workers have to stop first.

## The design

### The workers lock

Each slot has a lock, a file in the team's folder,
`run/<slot>-workers/lock`, which the slot's boxes mount at `/hi-workers`.
The relay that starts every app box (`hi team __app`) takes it for the box
with `flock`, in the background, while the app starts as now. While the
box holds it, a file exists in the box's own `/tmp`:

```text
$HI_WORKERS_FILE    /tmp/hi/workers    exists while this box may run the app's workers
```

The app's rule, in one sentence: **start the background workers once
`$HI_WORKERS_FILE` exists, and stop them when it goes away.** An app checks
every few seconds. When the variable isn't set (a laptop, the tests, a
member's box) the workers run as before.

It goes with `$HI_SLOT`: in production the workers run in full, in the
preview on a sample; in either, only in the box that holds the slot's
lock.

### The handover

When hi puts a new version on a slot whose box is running (a release, a
rollback, a restart after a settings change or an update, `hi team jobs
restart`):

1. hi asks the old box to yield: it writes the old box's name to
   `run/<slot>-workers/yield`. The old box's relay, which reads it every
   second, removes its `$HI_WORKERS_FILE`.
2. The app stops its workers: it finishes or puts back the item in hand
   and writes no more. When it has, it says so by creating
   `$HI_WORKERS_FILE.stopped`, and the relay lets go of the lock at once.
   Otherwise the relay lets go after the grace, 30 seconds unless the app
   sets `HI_WORKERS_GRACE` (`hi team env`, at most 300).
3. hi waits for the lock to be free (it tries it on the host), for the
   grace and 10 seconds more.
4. hi starts the new box. Its relay takes the lock, which is free; the
   app opens its database with only the old box's pages using it,
   migrates, and answers; its workers start.
5. The address moves to the new box, and hi removes the old one and the
   yield file, as now.

If the new box doesn't start (it stops, or doesn't answer in time), hi
removes it, which frees the lock, and removes the yield file. The old
box's relay takes the lock again and puts its `$HI_WORKERS_FILE` back; its
workers carry on. The people's pages answered from the old box
throughout.

So a deploy costs the workers the grace and the new box's start, about a
minute, and never runs them twice. The pages still never go down.

### What doesn't change

- **Apps that don't check the file** run as today: the old box yields, the
  app doesn't notice, and after the grace both boxes' workers run for the
  overlap. Nothing breaks that works now.
- **A box from an older hi** holds no lock, so the new box gets it at once:
  the first deploy after updating hi overlaps as before.
- **Pauses** (snapshots, forks) don't release the lock: a paused box keeps
  it, measured below.
- **The preview on a fork** already stops its old box before the next one
  starts (hi_team_data_fork.md); the handover changes nothing there. A
  preview without an overlay hands over like production.
- **Each slot has its own lock.** The preview's sample and production's
  workers run on data of their own and don't wait for each other.

### What hi shows

- `hi team jobs` says which box holds each slot's workers, and since
  when, or that a handover is under way: "production's workers: handing
  over from v16 to v17, 12 s of 30".
- A new box that has waited more than five minutes for the lock is an
  incident, like a stalled job: something else holds it.
- The lead's skill, the brief rule for workers, the project's `AGENTS.md`,
  the team-app template's coder and devops roles, and the reviewer's
  checklist say the rule, beside the `$HI_SLOT` rule and the jobs rule.
  A database the app opens waits for a lock rather than failing (for
  SQLite, a busy timeout of 10 seconds or more), since the old box's
  pages still use it during a deploy.
- **Work written before.** Once per team, as for jobs: when dev has
  background workers that don't check `$HI_WORKERS_FILE`, the lead starts
  the change as one small task and asks for the release.

## Measured on this machine

Rootless Podman 5.7, kernel 7.0, two boxes on a folder from the host:

- A second box's `flock` on the same file waited until the first let go:
  7 s, the time the first held it.
- Removing the box that held it (`podman rm -f`) freed it at once.
- Pausing the box that held it (`podman pause`) kept it held.

## Decisions

The open questions of the draft, as approved:

- **The grace** is 30 s, and `HI_WORKERS_GRACE` (seconds, or a duration
  such as `2m`, at most 300) sets it. An app that says its workers
  stopped (`$HI_WORKERS_FILE.stopped`) hands over at once, so the grace
  only matters for apps that don't, and for a batch that can't stop
  sooner: it is put back, not finished. hi doesn't learn a grace.
- **A file, not a signal.** hi writes the yield file; the relay reads it
  each second. It needs nothing from the box's init process, and it
  carries an end time, so a yield left by a broker that stopped
  mid-deploy ends on its own; a broker that starts removes any yield.
- **Apps that ignore the rule** overlap as before, after the grace. hi
  doesn't pause the old box for them.
- **Migrations** run in the new box after the old box's workers stopped,
  but beside the old box's pages: they stay backward compatible for that
  minute, and the rule says so.

## Orbit under this

- `backgroundWorkersFor($HI_SLOT)` and `$HI_WORKERS_FILE` both decide:
  the workers start when the file exists, and stop when it goes away,
  putting back the batch in hand. The app writes
  `$HI_WORKERS_FILE.stopped` when its last worker has stopped.
- At a release, production's history analysis stops in the old box before
  the new one opens `app.db`; the new box migrates without the lock, and
  its analysis carries on from the database's frontier, as now.
- The workarounds in dev stay: the busy timeout and the retries still
  cover the old box's pages, the preview's fork, and a snapshot's pause.

## Not in this

- **Several machines,** or several boxes of one slot serving at once.
  One box per slot holds the workers; hi doesn't run more.
- **Draining the pages' requests** before the old box is removed.
- **Locks for the app's own use** beyond its workers, such as one per job.

## As built (v1.3.0)

- The relay takes `--workers /hi-workers --box <name>` before its usual
  arguments; a box started by an older hi runs its relay without them.
  `run/<slot>-workers/` holds `lock`, `holder` (the box that took it, and
  when), and `yield` (`{"box", "at", "until", "grace"}`) during a deploy.
- `hi team jobs restart` removes the box before starting a new one, as
  before, so there is nothing to hand over; a preview on a fork stops its
  box first too.
- The slot's state records `workers: true` for a box started with the
  lock. A deploy from a box without it doesn't wait.
- A deploy writes the yield after the new version's code is copied and
  its install command found, and before the box starts: the workers stop
  for the grace and the new box's start, not for the copy. The yield
  ends 40 minutes after the new box's start limit at the latest.
- `hi team jobs` (on the machine and in the lead's box) starts with one
  line: which box runs the workers and since when, a handover under way,
  a box that waits, or a box from before the lock.
- The incident (kind `workers`) is production's only, after five
  minutes of waiting, and says which box holds the lock and, when that
  box still runs, the `podman rm -f` that someone at the machine runs.
- The once-per-team check asks the lead when the app has reported jobs
  and dev's code (not its Markdown: `AGENTS.md` names the variable in
  every project) doesn't mention `$HI_WORKERS_FILE`.
- Measured with the release's binary as the relay in two Podman boxes:
  an app that says its workers stopped two seconds after its file went
  handed over in 4.1 s; an app that ignores the rule, with a grace of 5 s,
  in 6.2 s; a new box that exited at once gave the old box its workers
  back within a second of the yield's removal.
