# hi team: roles that remember, and a map of the project

Status: Draft (2026-10-17)

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
  `hi team new` from defaults. The owners and the lead may edit it. The
  reviewer's checklist moves here out of the Go code. hi keeps it current
  the way it keeps SOUL.md: it replaces a paragraph only where the text is
  still word for word its own.
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
  `follow_ups`. A follow-up starting with `Learned:` is a proposal. The
  lead reads it with the report and adds it to the role's LEARNED.md, or
  doesn't. The lead also adds what it sees across tasks, such as the same
  review note three times.
- **Small on purpose.** LEARNED.md stays under about 3 KB, a page an agent
  reads in seconds. Instruction files help only when they are specific and
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
| **Project truth** | rarely | the repository: `docs/specs/`, `docs/map.md`, `docs/decisions.md`, `AGENTS.md`, `HELP.md` | members in tasks, reviewed and merged |

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
  welcome page. It stays short (about 150 lines), with headings, so a brief
  can point at `docs/map.md#data`.
- **`docs/decisions.md`**: one dated paragraph per technical choice, with
  why and the task. It answers "why is it built like this", which the map
  doesn't.
- **`AGENTS.md` gains three lines:** read `docs/map.md` first; extend what
  the map lists instead of building beside it; update the map and
  decisions in the same task as the change (as with `HELP.md` today).
- **The reviewer checks it.** Two more lines in the reviewer's ROLE.md:
  does this duplicate something on the map, and does the map still match
  the code?
- **The lead uses it.** Briefs name the parts of the map the work touches,
  and the lead reads the map to answer people's questions about what the
  app can do.

Existing teams get the files through the template's `--update` path, or
the lead's first task writes the map from the code.

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

This reuses template layers; the only new part is that `hi team new` copies
`roles/` from the composed template into the team folder.

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

1. **Text only.** Add `docs/map.md` and `docs/decisions.md` to `team-app`.
   Add the trailhead lines to `AGENTS.md`, the map checks to the review
   brief, and the guidance on what goes where to SOUL.md and the hi-team
   skill. Existing teams get it through `teamSoulUpdates` and the
   template's update.
2. **Roles folder.** `roles/<role>/ROLE.md` and `LEARNED.md`: written by
   `hi team new`, created on `hi team up` for teams without them, and
   added to briefs by the broker. The reviewer's text moves out of
   `teamReviewBrief` into its ROLE.md default. Lead guidance covers
   `Learned:` follow-ups and keeping the file small. Backups already
   include the folder.
3. **Template roles.** `hi team new` copies `roles/` from the template.
   Private sources can ship roles.
4. **Only on evidence:** session resume for `--on`.

## How we'll know

- Reviews stop repeating notes the role's LEARNED.md already has.
- Reviews catch duplicates against the map, and fewer duplicates reach a
  merge.
- A restored or moved team, or a member switched to another agent, works
  as well as before. Everything it needs is in the folder and the
  repository.

## Open questions

- Should LEARNED.md lines expire, as notes do in
  [hi_notes.md](hi_notes.md) (a review date), or is the lead's pruning
  enough at this size?
- Should the people see `roles/` and the map (a page in the app's helper,
  or a channel command), or is it the agents' business?
- Is `docs/map.md` kept by hand, by members in each task, or partly
  generated (routes and tables from the code), which can't go stale? The
  generated part could come later, as OpenRig's "derived from source" top
  rung.
- When a hi team uses [hi_notes.md](hi_notes.md), do learned lines that
  apply beyond the project go there as proposals?
