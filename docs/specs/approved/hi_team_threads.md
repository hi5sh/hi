# `hi team`: one session per member for each piece of work

Status: Approved (2026-10-24). Release 1 built in v1.13.0.

Dependencies: [hi_team_rounds.md](hi_team_rounds.md) (two review rounds,
keywords once, shorter briefs), [hi_team.md](../approved/hi_team.md) (the
broker's review loop, `--on`), [hi_agent.md](../approved/hi_agent.md)
(the member's box, `claude -p` and `codex exec`, the report),
[hi_team_subscriptions.md](../approved/hi_team_subscriptions.md) (the
proxy that signs members in, OpenRouter as the fallback).

## Goal

The coder and the reviewer each keep one conversation for a piece of
work, from its first task to its merge. A fix round is the next turn in
the coder's conversation, with the reviewer's notes; a second review is the
next turn in the reviewer's, with the coder's answer. Neither starts cold
again, re-reads the brief, or rediscovers the code it worked on minutes
before.

## Why

Today every task and every review is a new box and a new session. All
the member knows is its brief, so hi puts everything into each brief, and
the member reads it and the code again. On cartman's OMXS30 work (see
[hi_team_rounds.md](hi_team_rounds.md#what-happened)) each fix round spent
its first minutes re-reading, and each review read the whole work again
and found something new in it, instead of checking what it asked for.
Notes, LEARNED.md, and memory are how the team copes with that loss; they
help, but a conversation remembers better.

A team of people works this way: the reviewer remembers what they asked
for, and the coder remembers why they did it.

## The design

### A piece of work has its sessions

A piece of work is a chain: the first task on a yes, its reviews, its fix
rounds, and the tasks that continue it (`--on`), until it is merged or
discarded. hi keeps, for each chain, one session per role, in
`tasks/<first task>-sessions/<role>/` on the team's machine (0700): the
member's own session files, as Claude Code (`~/.claude/projects/`) and
Codex (`~/.codex/sessions/`) write them.

**The box needn't stay up.** A session is files. Each round is still a
box, started from the branch's head as now, with that folder mounted
where the member keeps its sessions; hi starts it with the session it
had: `claude -p --resume <id>`, `codex exec resume <id>`. Between rounds
no box runs and nothing holds memory or a browser. hi records each
member's session ID already (`agent.go`, the report's `session_id`).

**The first turn is the brief, as now.** The first task gets the lead's
brief, the people's words, the role, LEARNED.md, and the rules; the first
review gets its brief. Only these go into a session's first turn.

### The handover is the next turn

The broker passes each round's result to the other member, as the next
turn of its session:

- **To the coder**, after a review asking for changes: the reviewer's
  summary and its changes, and "make them, commit, and report". Nothing
  repeated: the coder has the brief, the role, and its own work.
- **To the reviewer**, after a fix: the coder's report, the commits since
  the last review (`git log <last reviewed>..HEAD`), and "check that your
  changes were made and nothing broke".
- **A continuation from the lead** (`--on`): the lead's new brief, and
  what the people said since, as the coder's next turn.

The broker stays in the middle, though the members speak to each other.
It counts the rounds and stops at two
([hi_team_rounds.md](hi_team_rounds.md#4-two-rounds-then-the-people-decide)),
keeps each turn in the task's files, and the lead sees every handover in
hi team show and hi team wait. Two agents in a loop with no one counting
is what went wrong on OMXS30; the broker is the one counting.

Tasks stay what the lead and the people see: t-176 is still "the coder's
fix of t-175", with a box, a branch, a report, and its cost. Only how the
member starts changes.

### Long conversations

Both tools compact a conversation when it fills their window, and keep
working. On top of that, hi starts a new session for a role when its
conversation has grown past a limit (about half its window, measured by
the last turn's input tokens), with a handover from the old one: its last
turn asks the member for what the next session needs (what was done, what
is left, decisions and traps), and the new session's first turn is the
brief plus that.

### When a session can't go on

A session that won't resume (its files are gone, the tool's version
changed its format, the model changed) is not an error: hi starts a new
session with the brief, as today, and says so in the task's hints.

A Claude session that fails over to OpenRouter mid-work goes on there,
as now; a conversation with Claude's signed thinking may not resume on
OpenRouter's Anthropic endpoint, and then the round starts a new session
there. Release 1 measures this before it ships.

### When a chain ends

Its sessions are kept for 7 days after its merge or discard, for hi team
show and for looking back at what went wrong, then deleted.

## What doesn't change

- Each round is a box from the branch's head, with the proxy; nothing in
  a box sees the team's credentials.
- The lead is Hermes in its own box with its own sessions, and the only
  one who talks to the people, deploys, and merges.
- Effort per role; a thinking keyword in the first turn only.

## Decisions

- **Sessions persist, not boxes.** Idle boxes would hold memory and a
  browser for every open idea, and a box that runs for hours is harder
  to trust. A resumed session gives the member the same memory.
- **Through the broker, not a direct link between members.** The broker
  already knows the rounds, the tasks, and what the lead must hear. A
  protocol between agents (A2A) adds a party to secure and nothing a
  team on one machine needs.
- **The lead stays out of the members' boxes.** The coder runs packages
  and browses pages; the lead can deploy, post in Slack, and start work.
  A page that steers the coder must not reach those.
- **The coder and the reviewer keep separate boxes, for now.** One box
  with both would share a working copy and save the branch handover, but
  the box boundary costs seconds a round, and the reviewer's checkout is
  read-only by design. Measure after this ships; a later spec if it's
  worth it.

## Releases

1. **hi agent resumes a session.** `hi agent run --session <dir>`: the
   folder mounted as the agent's sessions, and the run resumes the last
   session there, or starts one. Measured on its own: a Claude session
   resumed in a new box; Codex through the proxy; a resume after a
   failover to OpenRouter.
2. **The review loop uses it.** Each chain's sessions, the handover turns,
   the limit and its handover, cleanup.

## Open questions

- Claude Code keys a session by its working directory: the box's is
  always `/box/repo`, so a resume finds it. To confirm in release 1.
- How much a resumed turn costs on a long conversation, against a fresh
  session that re-reads: cache reads are cheap, but not free, and the
  Claude plan has limits. Measure on cartman.
- Whether the lead's notes on a continuation should go in as the
  people's words, or as the lead's.

## How we'll know

On cartman, against the OMXS30 rounds: a fix round's first commit within
3 minutes of its start, against about 5 today; a second review that
checks the first review's changes and adds nothing outside the four
reasons; fewer Claude calls a round.

## As built

### Release 1 (v1.13.0): hi agent --session

- `agent_session.go`. The folder holds `hi-session.json` (agent, model,
  ID, and the session file's path in the agent's home) and a copy of that
  file under `files/`. The folder isn't mounted in the box: hi copies the
  file into the new box's home before it starts, and back when it ends
  (`hi agent wait`, or any report), so the box's home stays its own.
- Claude Code finds a session under its working folder, which is new for
  each box: hi puts the copy under the new box's folder
  (`~/.claude/projects/<working folder, non-alphanumerics as ->`), with the
  folder of large tool results beside it.
- Codex resumes by ID wherever it was: `codex exec <options> resume <id>
  -`. Claude Code: `claude -p ... --resume <id>`. Both keep the session's
  ID and add to its file.
- The window: the latest call's input from the session's file, against
  200,000 tokens for Claude Code and Codex's own `model_context_window`.
  Past half, the run starts a new session.
- A resumed run's steps and tokens count from where it began: the box's
  `.hi-agent/resumed.json` holds the file's size then, and Codex's totals.
  The report says `resumed: <id>`.
- Measured on aiw9: a Claude Code session (sonnet) and a Codex session
  (gpt-6.1-sol, signed in on the machine) each recalled a code word from
  the box before, in a new box, with the same session ID. Not measured:
  a resume after a failover to OpenRouter, which can't be caused on
  purpose; release 2 starts the round again without the session when a
  resumed run fails.
