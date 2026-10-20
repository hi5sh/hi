# The lead's step budget

Status: Draft, to investigate (2026-10-20). Not approved; nothing built.

## The problem

Sometimes a team's lead ends a reply with Hermes' line ":warning: Iteration
budget exhausted (60/60) — asking model to summarise", then a summary of
what it did. The people see a warning they can't act on, wait several
minutes, and get a summary rather than an answer.

The budget is hi's. Hermes' own default has no limit (`agent.max_turns:
null`); hi writes `agent: max_turns: 60` into the lead's config.yaml
(`teamLeadMaxTurns` in team_serve.go, since v0.86.1), with the note "few
answers need more than 20". SOUL.md tells the lead to look at the code
"only as much as you need to answer or to write a brief, a few files at
most; the member reads the rest in its task", and that a turn stops after
60 steps.

## What was seen

Team cartman, 2026-10-20, lead model google/gemini-3.8-flash. Two turns in
a row used all 60 steps on the same incident (i-3, the market regime
backfill):

| Turn | Steps | search_files | read_file | terminal | Other |
| --- | --- | --- | --- | --- | --- |
| hi's scheduled check of the incident | 60 | 40 | 16 | 4 | |
| An owner's answer, "3", to the lead's question | 60 | 32 | 12 | 14 | 1 skill_view, 1 patch |

- In the scheduled turn the lead read the app's source (src/quant.ts,
  model.ts, training.ts, tests, decisions) and never handed anything on.
- In the owner's turn it recorded the rule (`hi team incidents rule`) and
  updated the spec within about 38 steps, the work that was asked for,
  then spent the rest reading src/ to plan the coder's fix itself. It also
  ran `hi team channel` five times in a row, with shorter `--since` each
  time.
- Each step resends the lead's context, so a 60-step turn is roughly 1 to
  2 million input tokens.

How the steps were counted: the tool calls between the user message and
Hermes' "maximum number of tool-calling iterations" message, in the
lead's state.db (`messages.tool_calls`).

## Is it a real problem?

To find out before changing anything:

- How often: count the lead's turns that hit the budget, across teams and
  days, from state.db, against all turns. Two in a row on one incident may
  be rare.
- Which turns: people's messages, hi's scheduled checks (incidents, jobs),
  or woken turns (task done, brainstorm ended). A scheduled check that
  reads code helps no one.
- What the steps were: reads and searches of the code against hi
  commands. A turn that runs many hi commands may be doing real work.
- What it cost: tokens and dollars per exhausted turn, from
  `session_model_usage`.
- Whether the model matters: the same counts for a lead on another model
  (aitools, or a test team).
- Whether the summary was good enough: did the people get what they asked
  for anyway?

## Ways to resolve it, to weigh

- **Nudge in hi's Hermes plugin.** hi-team already runs in the lead's
  Hermes. It could count reads and searches per turn and, past about a
  dozen, add a note to each result: "You've read enough; hand it to the
  coder with hi team task, or answer now." It works whatever the model,
  and the budget stays as the backstop. Needs a Hermes hook that can
  change a tool's result.
- **Hermes' budget warning.** `agent.budget_warning_ratio` (for example
  0.5) tells the model once, before the cap, that it is running out, so
  it can answer on its own terms. One setting; it doesn't stop the
  reading early.
- **A lower budget.** If few answers need more than 20 steps, 30 might
  do. It cuts the cost of a runaway turn but also cuts real work short.
- **A stronger model for the lead.** One that follows "a few files at
  most" better, at a higher price per step. To compare with the counts
  above.
- **Words.** Sharper SOUL.md or hi-team skill text: scheduled checks
  never read src/; after a rule or decision is recorded, hand the code
  change to the coder. The current words weren't enough for this model.
- **Hide the warning.** Only if Hermes can leave that line out of Slack;
  cosmetic, the steps are still spent.
- **Fewer repeat calls.** `hi team channel` five times in one turn
  suggests the lead didn't trust what it saw; its output could say when
  nothing newer exists.
