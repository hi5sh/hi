# `hi team`: review rounds that end, and briefs that say what's new

Status: Approved (2026-10-24). Built in v1.12.0.

Dependencies: [hi_team.md](../approved/hi_team.md) (the broker's review
loop, `--small`, `--on`, approvals),
[hi_team_roles.md](../approved/hi_team_roles.md) (ROLE.md and LEARNED.md in
every brief), [hi_team_subscriptions.md](../approved/hi_team_subscriptions.md)
(effort per role).

## Goal

A small piece of work stays small. The coder thinks hard when a person
asks it to, once; each fix round gets what changed and nothing it already
had; a review ends when the work does what was agreed; and the lead does one
thing at a time unless the people say otherwise.

## What happened

On 2026-10-23 Iman asked cartman to download OMXS30's daily prices and
store them, for the Backtest's benchmark (19:52 UTC). By 21:01, when it was
stopped, it had taken 7 tasks and 68 minutes: 251 Claude calls, 40 ChatGPT
calls, about 215,000 Claude output tokens, and 3,700 changed lines. It was
not done. Beside it, the lead had started 5 other tasks without being asked.

| Task | What | Minutes | Brief (words) |
|---|---|---|---|
| t-174 | download and store | 23.6 | 3,628 |
| t-175 | review: changes | 3.0 | |
| t-176 | fix | 18.3 | 4,031 |
| t-178 | review: changes | 2.3 | |
| t-179 | fix | 15.7 | 4,024 |
| t-182 | review: changes, only "verify on the preview" | 1.6 | |
| t-185 | verify on the preview | stopped | 3,883 |

The causes, in what they cost:

1. **A thinking keyword in every brief.** Iman's request at 18:15, for
   the Backtest, ended "Send 'ultrathink' to coder", meant for that one
   task. It reached every coder task after it, in two ways. hi pastes the
   thread the work was agreed in, and on a continuation the first task's
   whole brief, into each brief; that thread held the request. And the
   lead wrote the word into its own text of t-174, a new task on a later
   yes that didn't ask for it. Claude Code takes "ultrathink" as a request
   for its deepest thinking: hidden reasoning was 60–64% of t-174's,
   t-176's, and t-179's output.
2. **Fix briefs that carry everything again.** The broker writes each fix
   round's brief itself: the reviewer's notes, about 400 words, then
   t-174's whole brief again (1,900 words, under "How this work started",
   with the thread inside it), then ROLE.md and LEARNED.md (750). The
   coder's fresh session reads all of it, and the code, every round. The
   lead's own first brief was 1,460 words and asked for more than the
   people did (identity checks on the index, gap tracking with retries, a
   rate-limited route, a decision record), so there was more to review.
3. **Reviews that couldn't pass.** Each review's first change was to
   verify the download against Börsdata's real API on the preview. Only
   the preview has the key; no box can do it. The lead put the branch on
   the preview at 20:58, after the third review. Meanwhile each round
   added edge cases that weren't in the acceptance list (retrying a
   missing day, empty answers, the stop signal), as changes needed.
4. **Three rounds, then more.** The loop runs three reviews; when the
   last still asked for changes, the lead started another round (t-185)
   with `--on`, which needs no yes.
5. **Side work beside the feature.** A test that fails now and then, seen
   in `make check`, became an incident, and the lead started four small
   tasks and a review for it and two others (t-177, t-180, t-181, t-184,
   t-183): 55 more minutes and 114 more Claude calls, on the same Claude
   plan, at the same time. `--small` needs no yes.

## The design

### 1. A thinking keyword reaches one task

The keywords are Claude Code's: *ultrathink*, *megathink*, *think
harder*, *think hard*, *think very hard*, *think really hard*, *think
super hard*, in any case.

A keyword reaches a member only in **the first task started on the
message that has it**: a task on a yes (`--approval`) whose thread, up to
that yes, has a person's message with the keyword that no earlier task in
that thread was given. hi knows which messages an earlier task got: the
thread up to its own yes.

Everywhere else hi replaces the keyword, wherever it is in the brief (the
lead's text, the people's words, the brief a continuation repeats), with
*(thinking keyword: first task only)*:

- a task that continues another (`--on`), and every fix round;
- a review;
- a small fix (`--small`), or a task for an incident;
- a task on a yes whose keyword an earlier task already had.

When hi takes one out, the task's hints say so to the lead: "hi took
'ultrathink' out of this brief: a person's thinking keyword reaches the
first task after they write it, once. How hard each member thinks is its
effort (hi team member)." The lead's skill says the same, so it stops
adding the word itself.

Effort per role (`medium` by default) stays how hard members think on
every task.

### 2. A brief says what's new

**Fix rounds and continuations.** hi no longer pastes the first task's
brief into each one. It saves it with the task's files, as
`/box/files/<first task>-brief.md`, beside the thread the work was agreed
in, `/box/files/<first task>-thread.md`, and the brief says in one line
where they are: "How this work started: t-174's brief and the people's
words are in /box/files. The work stays within what they agreed." The
reviewer's notes, the project's rules, ROLE.md, and LEARNED.md stay in the
brief, as now.

For t-176 that is about 2,100 words instead of 4,031, and when the
handover of [hi_team_threads.md](hi_team_threads.md) lands, the role and
rules go too.

**The lead's briefs.** The soul's step 3 and the hi-team skill say: a
brief is the goal, the acceptance list, where to look, and the tests that
must pass, in a few hundred words. hi adds the people's words, the role,
what it learned, and the project's rules; don't copy them in. Detail that
should outlast the task goes in the spec, and the brief points to it. Ask
for what the people asked for; what else would be good is a proposal for
them, not part of the brief.

When the lead's own text is over 800 words, the task still starts, and
its hints say how long it was and what the brief is for.

### 3. Reviews that can end

The reviewer's brief gains three rules:

- **What only the running app can show isn't the reviewer's to check.**
  The real service behind a key only the app has, the app's own data, the
  deployed app: not a change needed. The reviewer lists each as a line
  starting "Verify on the preview:" in follow_ups, and approves if the
  rest is right.
- **Edge cases the acceptance list doesn't ask for are for later.** A
  line starting "Later:" in follow_ups, not a change needed, unless it
  loses data, leaks a secret, breaks what worked, or fails the acceptance
  list.
- **A second review checks the first one's changes.** From the second
  round, the reviewer checks that the changes asked for were made and
  that they broke nothing. Something new is a change needed only if it
  loses data, leaks a secret, breaks what worked, or fails the acceptance
  list.

When an approved review has "Verify on the preview:" lines, hi's message
to the lead lists them, with the next step: hi team preview t-N, check
each there, and merge, or continue the task with what it saw.

### 4. Two rounds, then the people decide

The loop runs **two reviews** instead of three. When the second still
asks for changes, the loop stops and the lead tells the people in the
thread what is left and asks whether to go on.

Continuing that work (`--on`) then needs a yes (`--approval`), like new
work: hi counts the reviews asking for changes since the chain's last yes,
and from two it refuses a continuation without one, saying why. A yes
starts the count again. Continuing work after an approved review, or
after checking the preview, needs no yes, as now.

The soul's steps 4 and 5 say two rounds, and to ask the people after
them.

### 5. One thing at a time, unless the people say

`--small` and `--incident` tasks don't start while another member's task
is running or in its review loop. hi refuses them, saying what's under way
and how to go on: ask the people in the thread, saying what and why, and
start it with their yes (`--approval`), or start it when the work under
way is done. A task on a yes starts beside other work, as now.

The lead's skill says it: what you notice while work is going (a flaky
test, an incident, an idea) you note, say in the thread, and start after,
or now with a yes.

## What doesn't change

- The effort per role, and how members are signed in.
- `--on` while nothing has run out of rounds: no yes.
- A small fix while nothing else runs: no yes.
- The review's verdict words, "Approve:" and "Changes needed:".

## Decisions

- **A keyword is replaced, not removed.** The brief still reads as
  written, and the member sees that a word was there.
- **Scoped by message, not by thread.** A person may write "ultrathink"
  again later in the same thread, for the next big piece; that message
  then reaches the next task started on it.
- **Incidents wait too.** A production incident during a feature is
  rare, and an owner's yes takes a minute; the feature's work is
  reviewed and merged before a release anyway. An incident found by hi
  is still raised and posted at once; only the task for it waits.
- **The files keep the whole story.** A member that needs the first brief
  or the thread reads it; it isn't pushed into every round.

## Not in this

- Sessions that last across rounds, with the handover going straight to
  the other member: [hi_team_threads.md](hi_team_threads.md).
- The coder and the reviewer in one box.

## Releases

One release, v1.12.0. It needs `hi team restart` (or `hi update
--restart`) for the new skill, soul steps, and broker.

## How we'll know

On cartman, a piece of work the size of the OMXS30 download: one coder
task and one or two reviews, under 25 minutes together; no brief over
2,500 words after the first; no keyword after the first task; no task
started beside it without a yes.

## As built (v1.12.0)

- `team_rounds.go`: the keywords, `keepsThinking`, `teamChangesSinceYes`,
  `teamBusyWork`, `teamPreviewChecks`, and the reviewer's rules.
- A continuation's files hold `<first task>-brief.md` and
  `<first task>-thread.md`; a continuation on a yes gets that yes in its
  brief, under "Going on was agreed", not the thread again.
- The soul's steps 3, 4, and 5 and the small-fix paragraph change in an
  existing SOUL.md too, as other updates do.

## As built (v1.12.1)

Found on cartman's first task under v1.12.0 (t-194, a title fix):

- The fix brief listed every follow_up under "What must change",
  "Verify on the preview:" lines too. It now lists only the coder's
  changes: not "Verify on the preview:", "Later:", "Alert:", or
  "Learned:" lines.
- A review whose notes are all for the lead starts no fix round; hi team
  wait says the notes are the lead's.
- The reviewer's rules gain one: a check that fails without this work too
  (a new advisory for a package the work didn't touch, a flaky test) is an
  "Alert:" line, not this work's change needed. A happy-dom advisory
  published that morning had blocked the title fix.

## As built (v1.14.3)

Found on cartman (2026-10-24): a chart plotting OMXS30 beside the
portfolio was done and approved at 07:54, 15 minutes after it was asked
for. The lead then sent the review's "Verify on the preview:" lines to the
coder as three more tasks (t-203, t-205, t-207), each with a review, until
08:16: 22 minutes of checking work that was finished.

- **Preview checks go to the people.** hi team wait, the soul's step 5,
  the skill, and the reviewer's rules say: merge, put it on the preview,
  and pass what the review lists to verify to the people, as what to try.
  A task starts when they find something wrong.
- **Docs only, no review again** (`team_quick.go`). A task whose commits
  since an approving review in its chain change only `docs/` starts no
  review, and merges on that approval; the merge commit names it.
- **Quick work merges before its review.** Work whose first task was a
  small fix, or whose change outside `docs/` since the team's branch is
  at most 100 lines added and removed, may merge while its review runs.
  hi team wait ends when the coder is done, and says so. When that review
  then asks for changes, hi wakes the lead in the work's thread to fix
  them in dev with a small fix. Dev isn't production: a release still
  needs an owner's yes.
