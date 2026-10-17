# hi team: roles that remember, and a map of the project

Status: Approved (2026-10-17). Continuing work (idea 6) is built in
v0.75.0, roles and the map (ideas 1 to 4) in v0.76.0, template roles
(idea 5) in v0.77.0. Session resume waits for evidence.

Dependencies: `hi team` (the broker, briefs, the review loop, SOUL.md, the
lead's Hindsight memory), the `team-app` template (`hi init`), and
optionally [hi_notes.md](hi_notes.md) (accepted notes across projects).

Inspiration: [OpenRig](https://openrig.dev/), an open-source tool that
runs persistent teams of coding agents, and its author's writing on
context engineering ("Context engineering as world building", and the
video "How My AI Civilization Actually Works"). The ideas here are its
structure, not its machinery: hi keeps its lead, boxes, approvals and
broker as they are.

## Goal

A coding agent (Claude Code, Codex, Hermes) is a harness for one session.
A hi team is a harness around them for a project: it holds what outlasts
any one session, which work is open and how its tasks connect, who
approved what, the branches and releases, and, with this spec, what each
role has learned. This spec is about that layer: how it's organized and
how it remembers.

Make a hi team's organization (who does what, what each role knows, and
where that knowledge lives) work better over weeks and months, mostly by
changing files and text hi already writes. Few new commands.

Two failures it should prevent:

- **A role relearns the same thing every task.** The coder finds that the
  tests need `TZ=UTC`, works around it, and the next coder finds it again.
  The reviewer flags the same mistake in three tasks in a row.
- **A member rebuilds what already exists.** Asked to "add CSV export",
  the coder writes a second export module beside the one from t-9,
  because nothing told it that one exists. OpenRig's author calls this the
  agent that "doesn't know what it doesn't know": search only finds what
  you think to look for.

## What hi team has today

What's right, and stays:

- **The team is a folder.** Everything is in `team-<name>/`, so backup,
  restore and move work on it. Any new memory goes there too, as plain
  files.
- **The lead holds the memory, members do one task each.** OpenRig keeps
  every role running in tmux. hi doesn't need to, and this spec doesn't
  change it.
- **Intent travels with the work.** A task names the people's approved
  message, the brief carries their thread, and the reviewer checks "it
  does what the brief asks, and nothing it doesn't". That's the defence
  against what OpenRig calls doghouse to moonbase, where each step makes
  sense and the result is something nobody asked for. Approvals are
  checked against the channel log, so a member can't take a go-ahead from
  an agent that lacks the context to give one.

The gaps, from the code at v0.74.0:

1. **Roles have no instructions of their own.** In `team.json` a member is
   an agent, a model and a bundle (`teamMember`). The approved spec says
   "and standing instructions", but there's no place for them. The
   reviewer's instructions are hard-coded in `teamReviewBrief`. The coder
   gets the generic `AGENTS.md`. Devops and the researcher get nothing
   beyond the brief.
2. **Members remember nothing.** Each task is a fresh session in a fresh
   box. `--on` continues the branch, not the conversation: the session
   resume in the approved spec isn't built. What a member learns is in its
   report, and the report is read once, by the lead.
3. **The lead's memory is one pile.** Hindsight takes facts from every
   turn automatically, and Hermes' notepad holds a few thousand
   characters. The lead also keeps its own skill and the specs. Hindsight
   is good at recall ("Anna prefers weekly digests"), but nobody can read
   or correct it, and it mixes conversation with the project's technical
   facts. A wrong fact in it stays wrong quietly.
4. **No map of what exists.** Members read `AGENTS.md` (rules),
   `docs/specs/` (features as agreed with the people) and `README.md`.
   Nothing lists the pages, routes, tables and shared modules already in
   the code, so neither the coder nor the reviewer can easily see "this
   duplicates X".
5. **Decisions have no home.** "We use SQLite", "sign-in comes from the
   helper" and "no ORM" live in reports and in Hindsight, not where the
   next member reads.

## The ideas

### 1. A role is a position, not a run

OpenRig separates a **seat** (a durable position with a name, a role and
what it has learned) from the **session** that fills it for a while.
"Sessions end; the seat keeps its name, its role and what it has learned,
and the next session sits down in it."

hi already has positions: the members in `team.json`. Give each one a
home in the team folder:

```text
team-payments/
  roles/
    coder/
      ROLE.md       what the coder is for and how it works on this team
      LEARNED.md    what coders on this team have learned, task by task
    reviewer/
      ROLE.md
      LEARNED.md
    devops/
      ...
```

- **ROLE.md** holds the role's standing instructions, written by
  `hi team new` from defaults, and by `hi team up` for a role without one.
  The owners and the lead may edit it. What the broker relies on stays in
  its own brief text, where an edit can't remove it: the reviewer's check
  against the brief and the people's approval, tests, secrets, and the
  verdict format the broker reads. ROLE.md holds the rest: what good work
  is on this team, and the checks particular to the role, such as the
  map (idea 3).
- **LEARNED.md** is the position's memory: short, specific, non-obvious
  things, each with the task that taught it.

  ```markdown
  - Tests need TZ=UTC: the fixtures build "today" in local time. (t-7)
  - The suppliers list is paged on the server; don't load all rows. (t-12, review)
  ```

  It's written about the position, never about the agent: "coders here",
  not "Claude". Swapping the coder from Claude Code to Codex keeps it.
- **The broker puts both into every brief** for that role, after the
  heading and before the lead's brief, as it adds the specs rule today.
  The member reads them; it doesn't write them.
- **Members propose, the lead keeps.** The report already has
  `follow_ups`. A follow-up starting with `Learned:` is a proposal, and the
  brief says so. `hi team wait` and `show` list a report's proposals under
  "The coder proposes for /team/roles/coder/LEARNED.md". The lead, with
  `/team/roles` writable in its box, adds what's true in its own words, or
  doesn't. The lead also adds what it sees across tasks, such as the same
  review note three times.
- **Small on purpose.** LEARNED.md stays under about 3 KB, a page an agent
  reads in seconds; the broker puts at most 6 KB of it in a brief and says
  so when it cut. Instruction files help only when they are specific and
  short (the research [hi_notes.md](hi_notes.md) cites). When it's full,
  the lead merges lines, drops what no longer holds, or moves a line up
  (idea 2).

What this costs: a folder, two files per role, a few lines in the broker,
and paragraphs in SOUL.md and the hi-team skill. No new command.

### 2. Three levels of knowledge, by where it lives

OpenRig's "epistemic ladder" ranks knowledge by how slowly it changes:
data, then field notes, then insights, then canon. Its rule: a claim's
address tells you how much to trust it. hi needs three levels, each with
a place it already has or nearly has:

| Level | Changes | Where | Who writes it |
|---|---|---|---|
| **Record** | constantly | the channel log, `tasks/` (briefs, reports), Hindsight | hi, the members, Hermes |
| **Learned** | days to weeks | `roles/<role>/LEARNED.md`, the lead's own skill | the lead |
| **Project truth** | rarely | the repository: `docs/specs/`, `docs/map.md`, `docs/decisions/`, `AGENTS.md`, `HELP.md` | members in tasks, reviewed and merged |

- **Records are kept, never loaded whole.** They're the evidence the
  levels above point to (`t-12`).
- **Moving up is a deliberate act.** When a learned line keeps holding
  over several tasks and belongs to the project rather than the role (a
  convention, a fact about the code), the lead puts it into the repository
  in an ordinary task. It then goes through review and travels with the
  code, into every clone and every restored team.
- **Every line carries its reason.** OpenRig: "Canon without warrants is
  cargo cult with good formatting." Here that's a task ID, a commit, or a
  short why.
- **Changing project truth is loud.** A task that removes or contradicts a
  decision, or a line in the map, says so in its report. The lead says so
  in the channel when the people would care.
- **Hindsight is for people and conversation,** not technical facts: who
  wants what, what was said, preferences, history. SOUL.md says so, so
  "the export module is in src/export.ts" goes to the map, where members
  can read it, not into a memory only the lead can recall.

This is guidance in SOUL.md and the hi-team skill, not code.

### 3. A map of the project, read before building

OpenRig's fix for agents rebuilding what exists is breadth first: "an inch
deep, a thousand miles wide". Show every important part briefly enough
that a member recognizes when one matters, with a path to the detail.
`AGENTS.md` is the trailhead that points there.

For a hi team, that is one more file in the repository, the agents'
counterpart to the people's `HELP.md`:

```markdown
# Map of the payments app

What exists, one line each, with where it lives. Read this before adding
anything; extend what's here rather than building beside it.

## Pages
- /suppliers: list, add and edit suppliers. src/pages/suppliers.tsx
## API
- GET/POST /api/suppliers: src/api/suppliers.ts
## Data
- suppliers, payments (SQLite in $HI_DATA_DIR): src/db/schema.ts
## Shared code
- src/export.ts: CSV and Excel export, used by suppliers and payments (t-9)
## Outside services
- OpenRouter for the summary on /payments (t-14)
```

- **`docs/map.md`** comes with the `team-app` template, describing the
  welcome page (a team started with an empty project gets it from its
  first task). It stays short (about 150 lines), with headings, so a brief
  can point at `docs/map.md#data`.
- **`docs/decisions/`**: one short file per technical choice, with why and
  the task, as hi's project templates already lay out
  (`NNNN-short-title.md`). It answers "why is it built like this", which
  the map doesn't.
- **Every brief says it,** after the specs rule, so teams made before this
  get it too: read `docs/map.md` first; extend what the map lists instead
  of building beside it; update the map, and add a decision for a
  technical choice, in the same work (as with `HELP.md` today); and write
  the map from the code if it's missing. A new team's `AGENTS.md` says the
  same.
- **The reviewer checks it.** Two more lines in the reviewer's ROLE.md:
  does this duplicate something on the map, and does the map still match
  the code?
- **The lead uses it.** Briefs name the parts of the map the work touches,
  and the lead reads the map to answer people's questions about what the
  app can do.

Existing teams get the map from their next task, which writes it from the
code because the brief asks for it when it's missing.

### 4. Ask what a past task knew

OpenRig keeps old sessions so a working agent can ask the one that built
something why ("a library of minds"). hi already keeps every brief and
report in `tasks/`. That covers most of it at no cost:

- The hi-team skill tells the lead: before briefing work on an area,
  check which tasks touched it (`hi team tasks`, `hi team show t-9`) and
  give the member what they decided.
- The map and decisions name their tasks, so a member reading them knows
  where to look (`tasks/` stays on the host, so the lead quotes it into
  the brief).

**Only if that proves not enough:** keep each member's session files with
its task, so `--on` resumes the conversation as the approved spec
promised, and the lead could ask a finished task a question. That is a
feature, so it waits for evidence that briefs and LEARNED.md miss things
a session would have had.

### 5. Roles come from the template

OpenRig shares a whole team as a bundle: roles, the instructions each
starts with, skills, and the project's context ("a shippable unit of
competence"). hi's version is smaller: the team's starting `roles/` and
`docs/map.md` come from the template, as the project files already do.

- The built-in `team-app` template carries default ROLE.md files for
  lead, coder, reviewer, devops and researcher.
- A private template source can carry its own, for example a researcher
  whose ROLE.md and bundle know the team's data and backtests. A team for
  that kind of project then starts with a researcher that already knows
  how the work is done.

This reuses template layers:

- **A layer's `team/` folder is the team's, not the project's.**
  `team/roles/<role>/ROLE.md` and `LEARNED.md` go to the team's `roles/`;
  `hi init` and `--update` never write `team/` into a project. The
  built-in team-app layer keeps hi's default roles there, so the defaults
  are files, not code.
- **A team template is one that extends team-app.** `hi team new` offers
  those it finds (built in, `HI_TEMPLATES_DIR`, the hi server's sources)
  beside "new" and "empty" in the wizard's code question; `"template"` in
  the answers file does the same, and team.json records it. A template
  that doesn't extend team-app is refused: a team's machinery (previews,
  the helper, `$HI_DATA_DIR`) expects its app.
- **Roles come from the template first, then hi.** A member's role takes
  the template's file when it has one, else hi's default. A role the
  template writes itself that no member fills is kept too, and
  `hi team new` says so ("add a member for one in team.json to use it").
- **A cloned or empty project still takes a template's roles**, without
  its files.

### 6. Continuing work: telling a follow-up from a new idea

`--on t-4` continues unmerged work: same branch, t-4's files and first
brief, no new yes, no new idea counted. Except in the review loop, where
the broker starts fixes itself, **the lead decides** whether a request is
more of t-4 or something new. It decides from memory, which is the gap
this spec is about. A real development harness doesn't leave that to
memory: it knows which work is open, where it came from, and what a
command would lose.

The broker can do the same with what it already records: each task's
`on`, its approval (thread included), its branch, and the open ideas.
Four kinds of response, from lightest to strongest:

- **Hint.** The broker can't know, so it says what it sees and goes on.
- **Redirect.** There's exactly one right target, so the broker uses it
  and says so.
- **Block.** Going on would lose work or skip a gate, so the broker
  refuses and gives the command that's right.
- **Recover.** It went wrong anyway; the way back with existing commands.

#### Choosing between new and continued

| Case | Before v0.75.0 | Since |
|---|---|---|
| **New task while related work is unmerged.** The people want more on t-4, and the lead starts a new task. Its branch starts from `main`, without t-4's code, so the coder redoes it or the two conflict. | Nothing; it counts as another idea. | **Block** when the approval is a later yes in the same Slack thread (or web chat) as t-4's: that's almost always more of t-4. "This yes is in the thread of t-4 (suppliers page), which isn't merged yet. If it's more of that work, `--on t-4`; if it really is separate, add `--new`." The same yes may still start several tasks, when the lead splits the work. **Hint** otherwise: every new task prints the open ideas, one line each, with "if this is more of one of them, stop it and continue that one". |
| **Continuing merged work.** `--on t-4` after t-4 is merged. | Refused: "isn't a finished task with a branch to continue". | **Redirect in words:** "t-4 is merged already: its code is in dev, where new work starts. Start a new task without --on; it needs a yes." |
| **Continuing a review.** `--on` a review's ID. | Refused, same message. | **Redirect in words** to the work it reviewed. |
| **Continuing discarded work.** | Refused, same message. | **Hint:** "t-4 was discarded on <date>. Start again with a new task; its brief is in `hi team show t-4`." |
| **`--on` for unrelated work.** The lead puts a different feature on t-4's branch, by mistake or to avoid asking for a yes or hitting the five-idea limit. Two ideas then merge or discard together, and the people never approved the second. | Nothing stops it: `--on` needs no yes. | Three guards. (1) The **reviewer checks scope against the approval**, not only the brief: its brief already carries how the work started, and its ROLE.md says work beyond what the people approved is "Changes needed: outside what was approved in t-4". (2) The **broker announces each continuation in the original approval's thread** ("Continuing t-4: sort the list by name"), so the people see what's being added to what they agreed. (3) SOUL.md says it plainly: `--on` is for more of the same request, never a new one. |
| **A failed or stopped task.** `--on` a task that failed partway. | Allowed when it has a branch. | **Hint:** "t-4 failed (<reason>); its branch has 3 commits; continuing from them." Usually right, so no block. |

#### Continuing the wrong step

| Case | Before v0.75.0 | Since |
|---|---|---|
| **An older step of an idea.** t-4 was continued by t-7; the lead, thinking "t-4", runs `--on t-4` again. The new branch starts from t-4 and leaves out t-7's commits. The idea forks, and the fork may count as a new idea. | The broker follows its own review fixes (`teamLatestWork` walks `Next`), but not continuations the lead started (`On`). | **Redirect** to the idea's latest task, as review fixes already are: "t-4 was continued by t-7; continuing t-7." If the idea has already forked (two latest tasks), **block** with both and ask which to keep (`hi team discard` the other). |
| **The idea is still working.** `--on` while t-4, a fix of it, or a continuation of it runs. | Blocked for the review loop: "still in review; hi team wait". A continuation the lead started isn't checked, so two can run at once. | **Block** for every step of the idea, using the same latest-task walk: one piece of work per idea at a time. |

#### When the code moved on

| Case | Before v0.75.0 | Since |
|---|---|---|
| **`main` moved since the idea started.** Other work was merged, and t-4's branch is behind. | The specs are handled: `teamSpecsMoved` tells the member to merge them first. Code isn't, and the merge later fails: "doesn't merge cleanly; give the coder a new task to redo it". | **Hint in the brief** for code too: if `main` changed files the idea also changed, the continuation brief says so and asks the coder to merge `main` first and resolve. **Redirect** when a merge conflicts: the broker starts the continuation itself ("Bring t-4 up to date with main": merge it, resolve the conflicting files it names, test, change nothing else), and the reviewer checks it like any other work, as the broker starts review fixes today. The lead is told, waits for it, and merges that. |
| **The branch is gone** (removed by hand, or a restore from before it). | Refused: "branch is gone". | **Hint:** "t-4's branch is gone; start a new task from its brief (`hi team show t-4`)." |

#### When the lead has lost track

| Case | Before v0.75.0 | Since |
|---|---|---|
| **After a restart or compaction** the lead doesn't remember which task is which piece of work. | `hi team tasks` lists tasks; `hi team versions` lists the open ideas. | `hi team tasks` **groups by idea**, showing the chain (t-4, then fix t-6, then t-7) with each idea's thread and status. The lead's skill says to check it before any `--on`. It's a change to existing output, not a new command. |
| **The five-idea limit is full** and the people ask for something new. | Refused, with the list. | Same, and the list says which ideas are oldest and which wait on the people, so the lead can ask "shall we drop the export idea from last week?" |

#### Recovering from a wrong choice

- **Started new instead of continuing:** `hi team discard t-8` and redo it
  as `--on t-4`. Both exist, and the new task's brief and files carry
  over by attaching them again.
- **Two ideas on one branch:** the reviewer should have caught it (above).
  If it's merged, it's in dev, not production: the people try it, and
  `hi team discard dev` puts dev back on production if they want neither.
  Splitting a branch isn't worth a command.
- **A forked idea:** the redirect above prevents new forks; an existing
  one is resolved by discarding one side.

What's new in code: following `On` as well as `Next` when finding an
idea's latest task (redirect and block), the same-thread check with a
`--new` override (an approval records its thread), one announcement line
in the approval's thread, the catch-up after a conflicting merge, the
grouped `hi team tasks`, and the brief and message texts. A continuation's
brief carries what the people said when they agreed to the work, so its
reviewer can check scope against it. The reviewer's scope check and the
SOUL.md and skill paragraphs are text. A continuation that was discarded
or failed before it had a branch no longer hides the work it continued
from the count of open ideas.

## Not taken from OpenRig

Each would be a new feature or work against how hi teams are meant to work:

- **Persistent member sessions** (tmux seats that run for weeks). The
  lead is the one persistent mind; members stay one task each, in boxes.
- **Members messaging each other.** Review and fixes already go through
  the broker. A second channel would make the work harder to follow and
  approve.
- **A workflow language.** Spec, build, review, merge, preview, release
  is fixed, and that's a feature for people who don't read code.
- **Rosters, a fleet, a team TUI, Slack routing to many leads.** One team,
  one channel, one lead.
- **Full-access agents on the host.** Boxes and the proxy stay.

## Slices

1. **Continuing work** (idea 6), v0.75.0. The safety fixes (redirect an
   `--on` to the idea's latest task, one piece of work per idea at a time,
   the reviewer's scope check against the approval) and the help (the
   same-thread block with `--new`, the thread announcement, `hi team
   tasks` grouped by idea, the up-to-date hint, and the catch-up after a
   conflicting merge).
2. **Roles and the map** (ideas 1 to 3), v0.76.0. `roles/<role>/ROLE.md`
   and `LEARNED.md`, written by `hi team new` and by `hi team up` for teams
   without them, mounted writable in the lead's box at `/team/roles`, and
   added to every brief for the role by the broker. `Learned:` follow-ups
   shown to the lead as proposals. `docs/map.md` in `team-app` and the map
   rule in every brief and a new team's `AGENTS.md`. The guidance on what
   goes where (Hindsight or files) in SOUL.md and the hi-team skill.
   Backups already include the folder.
3. **Template roles** (idea 5). `hi team new` copies `roles/` from the
   template. Private sources can ship roles.
4. **Only on evidence:** session resume for `--on`.

## How we'll know

- Reviews stop repeating notes the role's LEARNED.md already has.
- Reviews catch duplicates against the map, and fewer duplicates reach a
  merge.
- A restored or moved team, or a member switched to another agent, works
  as well as before. Everything it needs is in the folder and the
  repository.

## Decided

- **LEARNED.md doesn't expire.** At about 3 KB the lead's pruning is
  enough; review dates belong to [hi_notes.md](hi_notes.md), across
  projects.
- **`roles/` and the map are the agents' business** for now: files in the
  team folder and the repository, readable by whoever runs the team.
- **Members keep the map by hand,** in the same task as each change. A
  generated part (routes and tables read from the code), which can't go
  stale, can come later: OpenRig's "derived from source" rung.
- **hi_notes comes later.** When it exists, learned lines that apply
  beyond the project become proposals there.
- **A later yes in the same thread blocks a new task,** with `--new` to go
  on. A thread can drift, but the override costs one word, and starting
  new work where a follow-up was meant costs a branch without the code.
- **A continuation needs no yes.** It's announced in the thread instead,
  and the reviewer checks it against what the people agreed to.
