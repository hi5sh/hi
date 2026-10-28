---
title: Reviews from the dev box
description: "In a hi team's dev box, an owner's agent asks the other agent for a second opinion before shipping, or starts a deep review of the whole project with a budget: agents read it for security and correctness problems, and another checks each finding. The findings are recommendations the owner weighs."
---

There is no review loop in a hi team: nothing waits for a reviewer
before it can be shipped. Instead, the owner asks their agent in the
[dev box](/guide/team/dev/) for a review when the work calls for one, and
the agent has two kinds. The agents know them from their `hi-dev-review`
skill, so you can ask in plain words: "get a second opinion before you
ship", "run a deep review of the app, up to $20".

```bash
hi dev review                          # a second opinion on the branch from the other agent
hi dev deep-review --budget 20         # several agents review the whole project
```

## A second opinion

`hi dev review` has the other agent than the one you work with (Codex
reviews Claude's work, Claude reviews Codex's) read what the branch adds
to dev. It runs in the box, in a copy of the branch of its own, on the
team's sign-ins, for up to 30 minutes, usually a few.

```bash
hi dev review "the date handling in src/chart.ts"   # with what to look at
hi dev review --with claude                         # name the reviewer
```

- It reviews commits: commit first, since uncommitted work is refused.
- It may run the project's checks and tests, and changes nothing. It
  can't ship or post in the channel.
- It judges the work against the project's specs, its `AGENTS.md` or
  `CLAUDE.md`, and the code around it on dev.

Its report starts with **Approve:** or **Changes needed:**, with a
numbered list of what to change and where, and then:

- **Verify on the preview:** what only the running app can show, such as
  a service behind a key only the app has. Put the branch on the preview
  with `hi dev preview` and try those.
- **Later:** an edge case the work doesn't set out to handle, for a
  task.
- **Alert:** a problem that isn't this work's, such as a test that also
  fails on dev.

Your agent weighs it: it knows what you asked for, and the reviewer
doesn't. The report is printed and kept in the box, in `~/hi/reviews/`.

## A deep review

Each change can be reviewed on its own. Nobody reads the whole project
that way: how the parts fit, what an old change left behind, what someone
outside sees. A deep review does, with
[`hi agent review`](/guide/agent/review/). It changes nothing, and it
costs money, so the agents ask you for the budget before they start one.
It's worth it before a release whose changes touch signing in, who may
see or change what, payments, people's data, or how the app is deployed,
and soon after the project came from somewhere else.

```bash
hi dev deep-review --budget 20                 # the whole project, as dev has it
hi dev deep-review --budget 20 --area security,correctness
hi dev deep-review --budget 20 --since v3      # what changed since a release
echo "Only the three owners use it; it holds bank details." | hi dev deep-review --budget 20 --context -
hi dev deep-review list
hi dev deep-review show r-4                    # how far it is
hi dev deep-review stop r-4
```

- **The budget** is in dollars, $50 at most from the box. A deep review
  often takes an hour.
- **The agents** are the team's coder's and reviewer's (Claude Code and
  Codex unless you changed them): they investigate in turns, and each
  finding is checked by the one that didn't raise it.
- **What they're told:** reviewers read code; they don't know how the app
  is run. So hi tells them what the team knows: what the project is for,
  whether production has an address on the internet and whether a
  password page guards it, and the team's decisions in `docs/decisions/`.
  Add what the code doesn't show with `--context`.
- **Compared with the last one:** each deep review says of an earlier
  finding whether it's still seen.

When it ends, the report lands in the box, in `~/hi/reviews/<id>/`:
`report.md`, and `report.html` to open in a browser. Your agent reads it
and tells you which findings matter for this app and why, and which
don't because of how it is run ("this one assumes the app is on the
internet; ours is only on the office network"). They are recommendations
to weigh, not requirements: a fix you choose is ordinary work, from a
task, on a branch, with `hi dev ship`.

## On the machine

The deep reviews are kept in the team's folder, under `reviews/`.
`hi agent review show r-4 --md` shows one, and `hi agent review ls` lists
them.
