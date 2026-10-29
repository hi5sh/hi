# `hi log`: anything on a machine, logged and watched

Status: Draft (2026-10-29). Rented machines and agents agreed the same day; a watch without a URL is still to discuss.

Dependencies: [hi_team_ops.md](../approved/hi_team_ops.md) (heartbeats,
the ops channel, the team's watcher), [hi_compute.md](../approved/hi_compute.md)
(runs on rented machines).

## Goal

A team watches itself, and a machine says it's alive (hi_team_ops.md).
Everything else hi runs, or runs beside, goes unwatched: a training run
on the Strix Halo box, a nightly script, `ollama`, `hi compute serve`, a
backfill someone started in tmux. When one dies, is killed for memory,
or hangs, nobody hears until they look.

`hi log` gives any program on a machine three things, with no setup
beyond one URL:

1. **Its output kept**, rotated, readable later: `hi log show <name>`.
2. **How each run ended**: exit code, signal, killed for memory, how long,
   how much memory at most.
3. **Someone told** when it ends badly, stops, or hangs: by the same kind
   of outside service the heartbeats use, which alerts where the IT admins
   chose, and, on a machine with teams, in a team's ops channel.

## What it is not

- **Not a log platform.** No search across machines, no dashboards, no
  shipping every line somewhere. Logs stay on the machine; only an
  ending, and a short excerpt, leave it.
- **Not check-ins.** A program that should call home on a schedule calls
  its service's URL itself (`curl -fsS https://hc-ping.com/<uuid>`); hi
  adds nothing there.
- **Not metrics.** CPU and memory graphs are another tool's job (netdata,
  Prometheus). `hi log` notices endings and hangs, not trends.

## The service decides who hears

As with heartbeats, hi sends and the service alerts. A watch gets a URL
from a check at healthchecks.io (hosted, or self-hosted), whose ping API
fits a run exactly:

| hi sends | When | The service |
|---|---|---|
| `<url>/start` | the run starts | measures how long it runs; alerts if it never ends, after the check's grace |
| `<url>/0` | it ends with exit code 0 | marks the check up |
| `<url>/<code>` | it ends with any other code (1 to 255) | marks the check down, and alerts |
| `<url>/fail` | it was killed (a signal, memory), or hung | down, and alerts |
| `<url>/log` | something worth a line that isn't an ending (a stall that passed) | keeps it in the check's log, no alert |

Each carries a body: what happened in a line, and the run's last lines
(at most 100, at most 10 KB), with secrets taken out (below), which the
service shows with the alert. Other services that take a URL (Better
Stack, Cronitor, Uptime Kuma) get the GETs, without the endings' codes.

## Commands

```text
hi log run <name> [--ping <url>] [--team <team>] [--stall 30m] [--keep 7d] -- <command>...
hi log unit <unit> [--ping <url>] [--team <team>] [--system]
hi log ls                              every watch: state, last run, how it ended
hi log show <name> [--since 2h] [--run <n>] [--errors]
hi log tail <name>
hi log rm <name>                       stop watching; its logs stay until --keep passes
```

### `hi log run`: a program hi starts

hi starts the command as its child, in a systemd user scope of its own
(`systemd-run --user --scope`, where there is systemd), so it can read
the scope's `memory.events` and `memory.peak`, and tell a kill for memory
from any other.

- **Output**: stdout and stderr, each line stamped, to
  `~/.local/state/hi/log/<name>/<date>.log` (0600), and to hi's own
  stdout and stderr, so it still shows in the terminal or tmux it was
  started in. Older than `--keep` (7 days by default) is deleted.
- **A run** is a line in `runs.jsonl`: started, ended, exit code or signal,
  `oom` when the kernel killed it for memory, peak memory, and the log's
  byte range.
- **The ending** goes to `--ping` as in the table, and with `--team`, as a
  line in that team's ops channel when it ended badly ("train-llm ended
  with exit code 1 after 3h12m: CUDA out of memory" and the last lines in
  a thread).
- **Stalls**: with `--stall 30m`, no new output and under 1% CPU for that
  long is a stall: `/fail` with "looks stuck", once, and `/log` when it
  moves again. Off by default: plenty of work is quiet for an hour.
- hi ends with the command's exit code, so `hi log run` fits in a cron
  line or a script unchanged. Ctrl+C and SIGTERM go to the command.

### `hi log unit`: a service systemd already runs

For what systemd keeps up (`ollama`, `hi-server`, a team's own
`hi-team-<name>`), hi doesn't wrap anything: it adds a drop-in with
`OnFailure=hi-log@%n.service`, a template unit that runs
`hi log __failed <unit>`, which reads the unit's last journal lines and
sends them as `/fail`, or to the team's ops channel. systemd calls it
when the unit fails, with no polling. `--system` writes the drop-in under
`/etc/systemd/system` and needs sudo, which hi asks for in one line; a
user unit needs none. Its output stays in the journal, where
`hi log show <unit>` reads it.

### The machine itself

The machine's heartbeat timer (`hi heartbeat`) also reads the kernel's
journal from where it last stopped (a cursor in its state), and sends to
the machine's heartbeat URL:

| Seen | Sent |
|---|---|
| a process killed for memory ("Out of memory: Killed process") | `/log`, with the process and its memory |
| a GPU reset or ring timeout (amdgpu), a disk I/O error | `/log` |
| the machine started again since the last ping (a new boot ID) | `/log`, with how long it was up before |
| a file system over 95% full | `/fail` until it is under 90% again |

On a machine with teams, each of those that names a team's box goes to
that team's ops channel too.

## Secrets

Before any line leaves the machine, hi takes out what looks like a key:
`sk-…`, `xoxb-…`, `xapp-…`, `ghp_…`, `AKIA…`, `Bearer …`, a PEM block,
and the value of any `KEY=`, `TOKEN=`, `SECRET=`, or `PASSWORD=`. The log
on the machine stays as it was.

## Releases

1. **`hi log run`** with `--ping` and `--keep`, `ls`, `show`, `tail`,
   `rm`, and taking out secrets.
2. **`hi log unit`**, and the machine's journal in its heartbeat.
3. **`--team`** (endings in a team's ops channel), **`--stall`**, and
   reading a tmux pane (`hi log attach tmux:<session>`, by `pipe-pane`)
   for something started before hi watched it.
4. **Rented machines and agents**: `hi compute run`'s command in
   `hi log run` on the instance, an instance idle but billing, and the
   dev box's agents reading their team's machine's logs (below).

## Rented machines

`hi compute run` runs its command on the instance under `hi log run`,
named after the run, so a job that dies on RunPod or Shadeform is heard
of like one on the machine: its ending, its last lines, and killed for
memory. The ping URL is the one the person gave the run (`--ping`), or
their machine's default (`hi log ping-default <url>`); without one, the
ending is still kept with the run, and `hi compute status` says it.

An instance that has run nothing for an hour while it bills is an
ending of its own kind: `/fail` with "idle for an hour, about $x so
far", once an hour, until it runs something or stops. This is
[hi_compute_idle.md](hi_compute_idle.md)'s notice, sent the same way;
that spec decides when hi may stop the instance itself.

## Agents

The dev box's agents may read their team's machine's watches:
`hi dev log ls`, `hi dev log show <name> [--since] [--errors]`, through
the broker, so "why did the training die?" is answered from the run's
own lines. They read, and never start, stop, or remove a watch: that is
someone's at the machine. The lines reach the agent without secrets, as
they leave the machine. A watch is the team's when it was started with
`--team <team>`; others' stay out of reach.

## Tests

- A command that exits 3 sends `/start` then `/3` with its last lines,
  and `hi log run` exits 3; one killed by SIGKILL sends `/fail` saying
  so; one killed for memory says "killed for memory" (a fixture
  `memory.events`).
- A line with `sk-or-…` or a PEM block reaches the service without it,
  and the log on disk keeps it.
- `hi compute run` on a fake instance wraps its command and reports its
  ending; an instance idle for an hour sends one `/fail` an hour.
- `hi dev log show` gives a team's own watch, without secrets, and
  refuses another team's and any change.
- Logs older than `--keep` are gone after the next run; `runs.jsonl`
  keeps a line per run.
- `hi log unit` writes a drop-in with `OnFailure=`, and `hi log __failed`
  sends the unit's journal lines; `--system` asks for sudo once.
- The heartbeat's journal reader sends an OOM line once, a new boot ID
  once, and `/fail` at 96% full, nothing more at 92%, and the next
  success at 89%.

## Decisions

- **The service alerts, hi sends**, as for heartbeats: one way to say who
  hears, set up once by the IT admins, which works when hi or Slack is
  what broke.
- **Logs stay on the machine.** Only an ending and its last lines leave,
  without secrets.
- **Wrap or hook, never poll a process.** A child's ending is certain; a
  unit's failure comes from systemd. Polling for a PID guesses.
- **No model** decides what is a failure. A summary of the last lines by
  a model can come later, as an option, and only reads.

## Open questions

- **A watch without a URL** (to discuss): post its endings only in a
  team's ops channel, require a URL, or keep them on the machine and say
  so in `hi log ls`?
- **healthchecks.io's `/log` and body sizes** are as documented today;
  check them against the service before building release 1.
