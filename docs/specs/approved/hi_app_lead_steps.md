# The lead's step budget

Status: Approved and built in v1.0.4 (2026-10-21): the nudge, Hermes'
budget warning, and the words in SOUL.md ("What hi does" below).

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

Team orbit, 2026-10-20, lead model google/gemini-3.8-flash. Two turns in
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

What was asked before changing anything (answers in "What was found"):

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
  (helpdesk, or a test team).
- Whether the summary was good enough: did the people get what they asked
  for anyway?
- What the next turn knew: whether a turn after an exhausted one started
  from what the first had found, or read the same files again. In orbit
  the second turn reread much of src/.

## What was found

From both leads' state.db on 2026-10-21, counting tool calls per turn
(a turn starts at a user message):

| Team | Turns | 1–10 steps | 11–20 | 21–40 | 41+ | All 60 |
| --- | --- | --- | --- | --- | --- | --- |
| orbit | 286 | 191 | 35 | 20 | 12 | 11 |
| helpdesk | 107 | 74 | 10 | 9 | 1 | 1 |

- How often: about 4% of orbit's turns and 1% of helpdesk', all from
  2026-10-18 on. One orbit turn on 10-18 ran 215 steps.
- Which turns: mostly people's messages and threads ("Yes", "ok do it",
  "yes go ahead"), plus a background command that ended and a hi-team
  skill turn. People's yes to work is where the lead plans the coder's
  change itself instead of starting the task.
- What the steps were: in every exhausted turn, most calls were
  search_files and read_file, and most of those under /team/repo/src (21
  to 48 of 60). Turns that ran many terminal commands (helpdesk' waits on
  a background process) stayed under the cap.
- What it cost: Hermes recorded no prices for this provider
  (estimated_cost_usd is about 0). In tokens, orbit's lead averaged
  about 69,000 input tokens a call, most of them cached, so a 60-step turn
  is about 4 million input tokens.
- Whether the model matters: both teams' leads run
  google/gemini-3.8-flash, so the data can't say. That is why the limits
  below are marked with the model they were set for.
- What the next turn knew: in orbit the turn after an exhausted one
  read much of src/ again.

## What hi does

- **The nudge.** hi's plugin in the lead's Hermes (helper/hermes in hi)
  counts search_files and read_file per turn with Hermes'
  transform_tool_result hook. From the 13th, each result ends with:
  "[hi: that is N reads of the code in this turn. You have enough
  pointers for the member's head start: put them under Where to look in
  the brief and start the task (hi team task), or note them on the
  incident (hi team incidents note <id> "..."), then answer. The member
  reads the code properly in its task.]" At 12, it reaches the turns that
  ran out at step 13 to 24, and 18 of the 370 turns that didn't (good
  turns read 11 to 13 files at the 95th percentile). At 50 it would have
  come at step 51 to 59 or never: those turns were almost all reads. A
  Hermes without the hook leaves the results alone; the budget still
  stops the turn.
- **Hermes' budget warning** at 0.75 of the budget, so once, at 45 of 60
  steps: Hermes' own words tell the lead to keep its progress somewhere
  that lasts, then go on.
- **Scouting**, in SOUL.md, after "Be quick", in place of "a few files
  at most", which said how little to read but not what the reading is
  for, so the lead had no point to stop at: its reading is scouting for
  the member's head start. It finds where the answer or the change lives
  (files, functions, spec, decision), puts those in the brief under
  "Where to look" with what it noticed, and stops; it doesn't work out
  the fix. The hi-team skill's list of what a brief says has "where to
  look" in place of "where in the project the work goes".
- **Keep what a turn found**, in SOUL.md, after scouting: write it where
  the next turn looks (the brief, the spec, an incident note), read an
  incident's notes and the thread before reading the code again, and a
  check hi wakes the lead for hands the code to a member. hi adds the
  paragraph to an existing lead's SOUL.md when the team starts, as it
  does its other paragraphs.
- **The budget stays at 60.** The 21–40 turns above did real work; a
  lower cap would cut them. No turn doing real work (hi commands, the
  terminal) came near 60, so 100 would only give the reading 40 more
  steps, and a wait of up to 8.5 minutes would grow to about 14. Raise it
  if turns still run out while running hi commands rather than reading.
- **Marked with the model.** The budget, the warning, and the nudge note
  in code that they were set for gemini-3.8-flash with the counts above.
  When a team's lead or hi's default lead moves to another model, count
  again as above before keeping them.

Not done: hi writing an exhausted turn's summary into an incident by
itself (the lead's words and the warning ask for it; a turn isn't always
about an incident), a budget per model (one model in the data), hiding
Hermes' warning line, and fewer repeat `hi team channel` calls. Each
stays below as it was weighed.

## Limits written for one model

The budget and SOUL.md's "a few files at most" are guesses about how a
model behaves, and a guess made for one model goes stale with the next.
Anthropic saw this with their own agents: they added context resets
because one model wrapped up early as its context filled, and on the next
model the resets were only overhead ("Scaling Managed Agents", 2026).
Whatever this spec settles on, hi should:

- Note beside each limit and each nudge which model it was written for
  and what was seen, as the table above does for gemini-3.8-flash.
- Re-check them when a team's lead model changes, or when hi's default
  lead model does: the counts above, on the new model, before keeping a
  nudge that may no longer be needed.
- Consider a budget per model rather than one `teamLeadMaxTurns`, if the
  counts differ much between models.

So the model question under "Is it a real problem?" comes before the
nudges below: if a stronger model doesn't hit the budget, a nudge written
for a weaker one is a cost for every team.

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
- **Keep what a turn found.** When a turn runs out, what it found lives
  only in its context and the summary, so the next turn starts over. The
  lead could write its findings somewhere durable as it goes, on the
  incident with `hi team incidents note`, or in the thread or the brief
  it is writing, and SOUL.md could say to read those first. hi could also
  do it itself: when a turn hits the budget, add the turn's summary to
  the incident as a note, so a scheduled check or the owner's next answer
  picks up from there instead of rereading the code. This doesn't lower
  the steps of the first turn but should cut the second.
- **Hide the warning.** Only if Hermes can leave that line out of Slack;
  cosmetic, the steps are still spent.
- **Fewer repeat calls.** `hi team channel` five times in one turn
  suggests the lead didn't trust what it saw; its output could say when
  nothing newer exists.
