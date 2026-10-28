# `hi agent review`: an on-demand review of a whole repository

Status: Approved (2026-10-18). Release 1 built in v0.87.0 (see
[As built](#as-built-release-1)), release 2 in v0.88.0 (see
[As built](#as-built-release-2)), release 3 in v0.89.0 (see
[As built](#as-built-release-3)). From a proposal for a "HiAudit" command;
what it kept and what it left out is under [Decisions](#decisions).

Dependencies: [hi_agent.md](hi_agent.md) (runs in boxes,
reports, cost), [hi_agent_best_of.md](hi_agent_best_of.md)
(several runs at once, hi checking what agents claim, the run's own
process, `watch`, `stop`, `--budget`), [hi_team.md](hi_team.md)
(approvals, the broker, the budget), [hi_team_adopt.md](../ideas/hi_team_adopt.md)
(fetching a repository with nothing run, the project's agent settings
off), and [hi_agent_fanout.md](../ideas/hi_agent_fanout.md) (the queue, `--parallel`),
which this can share code with but doesn't need.

## Goal

A team's reviewer reads one diff at a time, against one brief. Nobody
reads the whole project: how the parts fit, what an old change left
behind, what an attacker sees. Before a release that touches sign-in or
payments, or on a project someone else wrote, people want that read, by
agents that didn't write the code, with every claim checked.

```sh
hi agent review                                   # this repository, security and correctness
hi agent review github.com/ada/pocket-kanban --area security
hi agent review --area all --agents claude,codex --budget 40
hi agent review --since release-4                 # what changed since, read with the whole project around it
hi agent review --compare r-3                     # against an earlier review
```

The output is one report: findings with the file and lines they rest on,
each confirmed, uncertain, or rejected by a second agent, and the reason.
hi never changes the code during a review and opens nothing anywhere.
Fixing is ordinary work, asked for separately.

Everything in the report is a recommendation, never a demand. The
reviewers read code; they don't know how the app is deployed, who uses
it, what protects it from outside, or what the people have decided is
good enough. A finding says what the code does and when that could
matter; whether it matters here is for the people and the lead to decide.
The report says so first ([The handover](#the-handover)).

It runs only when someone asks: a person at a terminal, or a team's lead
after an owner said yes with a budget. Nothing starts it on a commit, a
release, or a schedule.

## Commands

```text
hi agent review [<path|url>]          review a repository (default: the current one)
    [--area <a>,<b>|all|<brief.md>]   what to look for (default: security,correctness)
    [--agents claude,codex]           who investigates, in turns (default: the first ready)
    [--judge <agent>]                 who adjudicates (default: an agent that didn't investigate)
    [--passes <n>]                    investigators per area (default 1, at most 3)
    [--ref <ref>]                     a commit, branch, or tag, not the working tree
    [--since <ref>]                   focus on what changed since <ref>
    [--path <dir>]...                 only these folders
    [--context <file.md>]             what the code doesn't show: how it's run, who uses it, what protects it
    [--budget <usd>]                  no run starts once this is spent
    [--max <duration>]                per run (default 30m)
    [--parallel <k>]                  runs at once (default 3)
    [--compare <id|report.json>]      say what's new, still seen, and not seen since
    [--sarif] [--out <dir>]           also write report.sarif; write the report elsewhere
    [--detach] [--json] [--yes]
hi agent review ls                    reviews, their state, cost, and counts
hi agent review show <id> [--md|--json|--sarif|--rejected]
hi agent review show <id> --html      write report.html from report.md, for reading in a browser
hi agent review watch <id>            the live table; q leaves, the review goes on
hi agent review stop <id>             stop starting runs, stop the ones running, keep what's done
hi agent review rm <id>               remove a review and its report
```

### Areas

An area is a brief that tells an investigator what to look for and what
counts as a finding. hi has four:

| Area | Looks for |
|---|---|
| `security` | Untrusted input reaching something it shouldn't: injection, missing or wrong checks on who may do what, secrets in code or logs, unsafe defaults, a dependency used unsafely |
| `correctness` | Code that does the wrong thing for some input: edge cases, error paths that lose data, races, wrong assumptions between parts |
| `performance` | Work that grows faster than it should with data or users: queries in loops, unbounded reads, missing indexes, work repeated per request |
| `maintainability` | Dead code, duplicated logic that has already drifted, parts that depend on each other's insides, docs and specs the code no longer matches |

`all` is the four. A path to a `.md` file is a brief of one's own, with
the same front matter as `hi agent`'s briefs, so a project or a template
can add what matters to it (look-ahead bias for a backtest, say). A
template can ship briefs in `.hi5/review/`, which `--area` finds by name.

Every area asks for the same thing: a concrete failure, not a style
preference. A `maintainability` finding still says what goes wrong and
when ("the two copies of the VAT rule already disagree for Norway").

## How a review works

1. **Snapshot.** hi freezes what is reviewed, as one commit that nothing
   can move:
   - A local repository's working tree, uncommitted and untracked files
     included, the way best-of snapshots a box: `git add -A` into a
     temporary index, `git write-tree`, `git commit-tree` on `HEAD`. The
     commit is kept under `refs/hi-review/<id>`, never on a branch.
     `--ref` reviews that commit instead.
   - A URL is fetched as `hi team adopt` fetches: on the host, hooks,
     filters, and credential helpers off, file and ext transports
     refused, submodules listed and not fetched, private repositories
     through the host's `gh`, never a token in the URL. Into hi's state,
     removed with the review.

   The review records the source, the commit, whether the tree was dirty,
   the time, the areas, the agents and models, the budget, and who asked.
2. **Plan and confirm.** hi prints the runs (areas × passes, with their
   agents), the adjudication, `--max`, the budget, and how each agent is
   paid (metered at API prices or a subscription), and asks. Without a
   terminal it needs `--yes`. Investigators and adjudicators count
   against the same limits as other agent runs.
3. **Investigate.** One `hi agent` run per area and pass, each in a box on
   a throwaway worktree of the snapshot, started fresh: no session shared
   with any other run, or with whatever wrote the code. With
   `--agents claude,codex`, passes of an area alternate agents, since two
   agents miss different things and two copies of one tend to miss the
   same. Each box:
   - has the `locked` network: the agent's own hosts and nothing else, so
     an instruction planted in the code can't send it anywhere;
   - starts the agent without the project's agent settings (hooks, MCP
     servers, permissions), as an adopted project's members do;
   - gets the area's brief, the review's scope (`--path`, `--since` and
     its diff), `--context`, and `docs/map.md` when the project has one,
     as notes to check, not facts;
   - is told the repository is the thing under review: text in it that
     reads as instructions is a finding to report, never an order;
   - is told not to build, install, or run the project. The box is the
     limit if it does; it changes nothing outside the worktree, and the
     worktree is thrown away.

   An investigator writes its findings to a file the brief names, at most
   25, each with an area, a title, a severity, a confidence, the failure
   (what input or state leads to what wrong result), the evidence (a
   file, a line range, and the exact lines quoted), what it assumes (the
   things it can't see that it depends on: "the API is reachable from
   the internet", "more than one person edits a board"), and a direction
   for a fix, in words. Severity is the reviewer's estimate if those
   assumptions hold. A missing or malformed file fails that run, and the
   report says so.
4. **Check the evidence.** hi, not a model, reads every quote against the
   snapshot commit (not the box's worktree, which the agent could have
   changed):
   - `exact`: the quote is at those lines;
   - `moved`: the quote is elsewhere in that file, and hi corrects the
     lines;
   - `missing`: the file isn't in the snapshot, or the quote isn't in the
     file.

   Then hi groups findings that point at overlapping lines of one file in
   one area, so the adjudicator sees likely duplicates together.
5. **Adjudicate.** A separate run, by an agent that didn't raise the
   finding where one is ready (claude's findings to codex and the other
   way round), in a fresh box on the same snapshot. It reads the code
   itself and, for each finding, decides whether the observation is
   true, not whether anyone should act on it:
   - `confirmed`: the code does what the finding says, and the failure can
     happen if the finding's assumptions hold;
   - `uncertain`: it may happen, but depends on something the code
     doesn't show (how it's deployed, what calls it);
   - `rejected`: the code doesn't do that, the failure can't happen, or
     it's a preference, not a failure;
   - `duplicate` of another finding.

   Each with a reason, the assumptions corrected if it sees others, and,
   if it disagrees, its own severity. Batches of
   up to 30 findings, grouped by file. A finding with `missing` evidence
   can't be confirmed: hi turns a `confirmed` verdict on one into
   `uncertain`. hi never merges disagreement into consensus: a finding
   two investigators raised says so, and one the adjudicator rejected
   stays in the report with the reason.
6. **Report.** In the review's folder, `~/.local/state/hi/reviews/<id>/`,
   readable only by the user (0700, files 0600), outside the repository
   unless `--out` says otherwise:
   - `report.md`: the report people and the lead read
     ([below](#the-markdown-report));
   - `report.json`: the record, every finding with its evidence check,
     verdict, assumptions, and fingerprint, every run's agent, model,
     time, and cost. `report.md` is written from it;
   - `report.sarif` with `--sarif`, confirmed and uncertain findings only.

   Every finding says `reproduced: false`. Evidence means the lines exist
   and say what's quoted; it doesn't mean the failure was seen
   ([Later](#later) adds that).

```text
Review r-4 · ~/projects/pocket-kanban @ 4f1c2e9 (dirty) · security, correctness · 31m · $18.40

  #  SEV   VERDICT    AREA         WHERE                      FINDING
  1  high confirmed   security     api/cards.ts:88-97         any signed-in user can move another board's cards
  2  high confirmed   correctness  store/sync.ts:140-152      two tabs saving at once lose the first save
  3  med   uncertain  security     server.ts:31               CORS allows any origin; fine only if no cookies
  4  low  confirmed   correctness  ui/date.ts:12              due dates shift a day west of UTC

  2 runs per area (claude, codex) · 11 raised · 4 confirmed, 1 uncertain, 3 rejected, 3 duplicates
  Not covered: none · Reproduced: none (not attempted)
  Recommendations only: weigh each against how the app is run and who uses it.
  hi agent review show r-4
```

### The markdown report

`report.md` is for two readers: people deciding what matters, and a lead
or an agent fixing what they chose. So it reads well as plain text, and
each finding stands on its own: a section that can be copied into a brief
as it is, without the rest of the report.

1. **The handover** (below), always first.
2. **What was reviewed.** The source and commit, dirty or not, the
   areas, the scope, the agents and models, the context given, and the
   cost.
3. **What wasn't.** Failed runs, folders left out, areas the budget
   didn't reach, and that nothing was run or reproduced.
4. **Findings**, confirmed by severity, then uncertain. Each one:

   ~~~markdown
   ## F-1 · any signed-in user can move another board's cards

   - Severity: high (the reviewers' estimate) · Area: security · Confirmed
   - Where: `api/cards.ts` lines 88-97, at 4f1c2e9

   **What the code does.** `moveCard` looks the card up by its id and
   saves the new column without checking that the board is the caller's.

   ```ts
   const card = await db.card(id)
   card.column = body.column
   ```

   **When it matters.** Anyone who can sign in can move cards on boards
   they weren't invited to, by sending another board's card id.

   **Assumes.** People who sign in shouldn't all see every board. If
   every signed-in person is trusted with every board, this doesn't
   matter.

   **A direction.** Check the card's board against the caller's boards
   before saving, as `api/boards.ts` does.

   **Second opinion.** codex confirmed: "the handler never reads
   `card.board`."
   ~~~

5. **Rejected and duplicates**: a count, and with `--rejected` each
   with its reason, so nobody raises them again without seeing why.

No finding says "must": the briefs ask for "what the code does" and
"when it matters", and the directions are suggestions.

### The handover

The report opens with this, and the terminal summary, `show`, and a
lead's message in Slack carry a line of it:

> **These are recommendations, not requirements.** Agents read the code
> at one commit. They don't know how the app is deployed, who uses it,
> what protects it (a private network, a sign-in in front of it, rules
> outside the code), or what the people behind it have decided is good
> enough. Each finding says what it assumes; where those assumptions
> don't hold, it may matter less or not at all. Some suggestions will be
> more than this project needs. Decide which ones are relevant to your
> context and scope, and fix those as ordinary work. Nothing here was
> run or reproduced, and a finding that isn't here may still exist: a
> short report doesn't mean the code is safe.

When `--context` was given, the handover says the reviewers had it; when
it wasn't, it says they worked from the code alone.

### Reading it in a browser

`hi agent review show <id> --html` writes `report.html` next to
`report.md`, rendered from it with goldmark, as `/docs` renders a team's
docs: one file, styles inline, no scripts, nothing fetched, so it opens
anywhere and stays private. Only on request: the markdown is the report,
and the page is a nicer way to read it. A review in a team is never put
on `/docs`, which can be public.

### Progress

`watch` (and the terminal, unless `--detach`) shows one line per run, its
state, findings so far, and the spend against the budget, never source
text. The review runs in a process of its own, as best-of's rounds do, so
it outlives the terminal; `stop` ends it and keeps what's done.

### Cost

hi counts what agents report in dollars, as best-of's `--budget` does.
Claude Code's figure arrives when a run ends, Hermes' when it has one, and
Codex reports only tokens, which count as nothing; the confirmation says
which. So `--budget` stops new runs once it's spent, and the running ones
end at `--max`; it can be overshot by what was already running.
Where an agent takes a spending limit of its own (Claude Code's, when
metered), hi passes each run its share. The report records the real
spend.

## Comparing reviews

`--compare` matches findings by a fingerprint: the area, the file, and
the quoted lines with spaces collapsed, not the line numbers, so code that
moved within a file still matches. For each finding of the earlier
review:

- **still seen**: the same fingerprint, and its verdict now;
- **not seen, code changed**: the quoted lines aren't in the new
  snapshot. Likely fixed or moved; the report doesn't say fixed;
- **not seen, code unchanged**: the quoted lines are still there and no
  investigator raised it. Agents miss things; this one is worth a look.

And findings not in the earlier review are **new**. A rejected finding
seen again stays rejected unless the new adjudicator confirms it.

## With a team

A team's lead knows the review exists and what it costs, and offers it;
an owner decides.

- **When the lead offers it.** Its SOUL.md says: before a release whose
  changes touch sign-in, permissions, payments, people's data, or
  deployment; when people say they're worried about one of those; or
  after a project was adopted. In the people's words: "Since the last
  release we changed how people sign in and who can see a board. Want a
  deep review before we release? Two agents read the whole app for
  security and correctness problems, and a third checks what they find.
  It can cost up to $30 of this month's $300. Nothing is changed."
- **A yes, from an owner, with the budget.** The lead runs, through the
  broker:

  ```text
  hi team review --project --approval <id> --budget <usd> [--area …] [--since <version>] [--version <v>]
  ```

  The broker accepts it as it does other approvals, with two more rules:
  the yes is an owner's (askers can ask for work, not spend on this), and
  the lead's message it answers names the budget (`$30`), so the lead
  can't ask about one figure and spend another. The budget must fit in
  what is left of the team's month. The review records the approval, the
  owner, the team, the commit (dev's, or the version's with
  `--version`), the areas, and the budget.
- **A no is a no.** The lead keeps it in memory and doesn't offer again
  for that release, unless someone in the channel brings it up.
- **Who does the work.** Investigators use the coder's and the
  reviewer's agents in turns, and the adjudicator is whichever of the two
  didn't raise the finding; a team's reviewer is already a different
  agent or model from its coder. Boxes get the team's usual limits, with
  the `locked` network.
- **What the reviewers are told.** A team knows things the code doesn't,
  and the broker writes them into the review's context: whether
  production has a public address, whether it's behind the team's
  sign-in and for whom, the team's purpose, and `docs/decisions/`. The
  lead adds what the people have said that bears on it ("only the three
  of us use it", "it holds customers' addresses").
- **The result.** The broker tells the lead when it ends. The lead posts
  in the approval's thread how many findings were confirmed, by severity,
  with a one-line title each and no detail of how to exploit them, and
  the handover's first line: these are recommendations to weigh, not
  requirements. The full report stays in the team's folder
  (`reviews/<id>/`, read-only in the lead's box) and goes as a file in a
  DM to the owner who said yes, with its HTML page beside it. At the
  host, `hi team review show <id>`.
- **Deciding what matters.** The lead reads the report as advice, as the
  people should. It doesn't offer to fix everything: it says which
  findings it thinks matter for this team and why, which it thinks don't
  ("F-3 assumes the app is on the internet; ours is only on the office
  network"), and asks about the assumptions it can't settle. The people
  choose. The lead keeps what they decided in its memory, so a finding
  they set aside isn't raised again as news.
- **Fixes are work.** For what the people chose: a yes, a task, a brief
  that carries the finding's section from `report.md` as it is, the
  usual review. The task records the finding's fingerprint, so the next
  review's comparison says "not seen since t-14".
- **Stopping.** An owner says stop; the lead runs `hi team review stop
  <id>`.

## Security

The repository under review may have been written to attack the agents
that read it.

- **Nothing from it runs on the host.** The snapshot and the fetch run no
  project code; the evidence check and the comparison read files from git.
- **Nothing it says reaches further than its box.** No credentials, the
  `locked` network, a worktree that is thrown away, the project's agent
  settings off.
- **Planted instructions are findings.** Briefs say so. They can still
  steer an investigator to miss something; that's why passes alternate
  agents and why "no findings" is never reported as "safe".
- **hi checks what it can.** Evidence is read from the snapshot by hi, and
  a confirmed verdict needs evidence that exists. A model can't fabricate
  a quote into the report as checked.
- **The report is sensitive.** Private to the user or, in a team, to the
  owners and the lead. Progress and logs carry titles and counts, not
  source text. The audit log on a hi server gets that a review ran, who
  asked, its cost, and its counts, not its findings.
- **The code goes to the agents' providers**, as with any `hi agent` run
  on it. The confirmation names them.

## Later

- **Reproduction.** For a confirmed finding, a run writes a test or a
  small script that should show the failure, and hi runs it in a box
  with no network at all, on the snapshot. `reproduced` only when hi saw
  it fail as the finding says; the adjudicator checks that the test tests
  the claim. The way best-of trusts a check: run by hi, not believed.
- **Big repositories.** One investigator per area per folder, when a
  project is too big for one agent to read in `--max`, with `--split`
  like fan-out's.
- **Known and accepted.** A file in the repository (`.hi5/review.json`)
  listing fingerprints the owners have decided to live with, and why;
  findings that match are shown apart, not hidden.
- **The security advisor** on the team roadmap watches the proxy's log
  and the members' commands. It could offer a review when it sees
  something worth one.

## Decisions

1. **The name.** Decided (2026-10-18): `hi agent review`, beside
   `best-of` and `fanout`, since it is runs of agents; and `hi team
   review --project` in a team, beside the review of one task. In hi,
   "audit" means the audit log (`hi server audit`, `/hi audit`).
2. **The default size.** Decided (2026-10-18): security and
   correctness, one pass, so a first review is cheap and quick.
   `--area all --passes 2` and alternating agents are one flag away, and
   the confirmation shows what each costs. The proposal defaulted to all
   areas, twice each.
3. **Maintainability as an area.** Decided (2026-10-18): kept, out of
   the default, with the same "say what goes wrong" rule. It produces
   opinions more than failures, and adjudication will reject many.
4. **The full report in Slack.** Decided (2026-10-18): a file in a DM
   to the owner who said yes; the channel gets counts and titles.
5. **No CI mode.** Decided (2026-10-18): the review never runs on a
   commit or a schedule, so there is no `--fail-on`. SARIF is for reading
   in an editor or a code scanning page someone uploads it to, not for
   gating merges.
6. **Recommendations, not requirements.** Decided (2026-10-18): the
   report is advice. Verdicts say whether an observation is true, not
   whether to act; every finding says what it assumes; the handover says
   so first, and the lead weighs findings with the people instead of
   offering to fix them all.
7. **Markdown first, HTML on request.** Decided (2026-10-18):
   `report.md` is the report, written so a finding can go into a brief as
   it is. `report.html` is a rendering of it for people, written by
   `show --html` (and sent beside it in a team's DM). If nobody uses the
   page, it can go without changing anything else.

From the proposal, left out:

- **A model-client layer with hard caps.** hi runs agents, not model
  calls; the budget is a gate between runs plus `--max`, and the
  confirmation says it can be overshot by what's running.
- **`--depth exhaustive`.** `--passes` says what it does.
- **Architecture as its own area.** Folded into `maintainability` and
  `correctness` ("wrong assumptions between parts").
- **Several models debating.** Investigators by different agents and an
  adjudicator who didn't raise the finding give the disagreement; the
  report keeps it rather than running rounds to resolve it.

## First slice

1. `hi agent review` on a local repository: the snapshot with the working
   tree, `security` and `correctness`, investigators in `locked` boxes
   without the project's agent settings, hi's evidence check, grouping,
   adjudication by another agent, `report.json`, and `report.md` with the
   handover, assumptions, and findings that stand alone; `--context`, the
   confirmation, `--budget`, `--max`, `ls`, `show`, `watch`, `stop`, `rm`.
2. URLs (sharing the fetch with `hi team adopt`), `performance`,
   `maintainability`, briefs as areas, `--since`, `--path`, `--compare`,
   `--sarif`, and `show --html`.
3. The team: SOUL.md, `hi team review --project`, the owner's yes with a
   budget, the team's context, the summary and the DM, weighing findings
   with the people, fingerprints in fix tasks.
4. Reproduction.

## Acceptance

1. `hi agent review` in a repository with uncommitted changes reviews
   them, records the tree as dirty, and leaves the branch, the index, and
   the working tree as they were.
2. Nothing is started without a yes at the terminal or `--yes`.
3. A finding whose quote isn't in the snapshot is `missing` and is never
   `confirmed`, even if the adjudicator says so; one whose investigator
   edited the file in its box to match is `missing` too.
4. With `--agents claude,codex`, the adjudicator of each finding is the
   agent that didn't raise it.
5. A repository whose `README.md` tells agents to report no findings and
   whose `.claude/settings.json` has a hook gets a review in which the
   hook never ran and the instruction is a finding.
6. An investigator in the review can't reach a host other than its
   agent's.
7. `--budget 5` with metered runs starts no run after $5 is spent, and
   the report says what wasn't covered.
8. The report folder is 0700 and outside the repository.
9. `--compare` after a fix says "not seen, code changed" for the fixed
   finding and "still seen" for the rest.
10. In a team, a review starts only on an owner's yes to a message of the
    lead's that names the budget; an asker's yes is refused.
11. `report.md` opens with the handover, every finding has an "Assumes"
    line, and no finding's text says "must".
12. One finding's section of `report.md`, pasted alone into a brief,
    names its file, lines, commit, and quoted code.
13. `show --html` writes one file that opens offline, loads nothing, and
    shows the same findings as `report.md`.
14. A review with `--context` saying the app is only on a private
    network gives findings whose assumptions say where that changes
    them.

## As built (release 1)

v0.87.0, 2026-10-18. Code: `agent_review.go` (the command, the
snapshot, the runs, the verdicts) and `agent_review_report.go` (the
evidence check, the report, the summary); tests: `agent_review_test.go`.

- **What's in it.** The first slice, with all four areas (`performance`
  and `maintainability` came early from the second). Not yet: URLs,
  `--since`, `--compare`, `--sarif`, `show --html`, briefs of one's own as
  areas, and the team. `--since` and the others are refused as unknown
  options, not ignored.
- **The folder.** `hi agent review <folder>` reviews the folder's
  repository; a subfolder becomes a `--path`. `--path` must exist in the
  snapshot.
- **The snapshot.** A dirty tree (`git status --porcelain` shows
  anything) is committed through a temporary index (`read-tree HEAD`,
  `add -A`, `write-tree`, `commit-tree` with "hi agent review" as the
  author), so ignored files stay out and the branch, index, and files are
  untouched. Clean trees and `--ref` use the commit as it is. Either way
  it's kept at `refs/hi-review/<id>`, which `rm` deletes.
- **The boxes.** Each run is a `hi agent` box on a worktree of the
  snapshot, on the branch `hi-review/<id>/<run>`, with `locked` network
  and 16 GB. A new box option, `untrusted`, skips the project's
  `devcontainer.json` entirely (its image, `postCreateCommand`, domains,
  and bundles), so the box runs hi's base image, and starts Claude Code
  with `--setting-sources user`. Checked on 2026-10-18: a `SessionStart`
  hook in a repository's `.claude/settings.json` ran with plain
  `claude -p` and not with the flag. Codex gets a fresh home in the box
  with no trusted projects, so it doesn't load a project's
  `.codex/config.toml` (Codex's own rule; not tested here). Hermes wasn't
  checked.
- **The time limit.** `--max` wraps the agent in `timeout -k 30`, then
  `kill -9 -1`, and writes `agent.json`, as best-of's wrapper does without
  the check (`agentTimedCommand`). A run that ran out of time but wrote its
  file counts, and the report says it may have stopped early.
- **Results.** Investigators write `/box/home/.hi-agent/findings.json`
  and checkers `verdicts.json`. When a box stops, hi copies the file to
  `<run>.json` in the review's folder, takes the cost and time from the
  agent's report, and removes the box, its worktree, and its branch. A
  missing or broken file fails the run with the reason. At most 25
  findings per investigator are read.
- **The evidence check.** `git show <commit>:<file>` (files over 4 MB
  aren't read), compared line by line without indentation and trailing
  spaces; the nearest match to the line the agent named wins. Paths that
  leave the repository are `missing`. The report's code comes from the
  commit, not from the agent, unless the quote is missing, when the
  agent's text is shown with a warning.
- **Groups and duplicates.** hi groups findings on overlapping lines of
  one file across areas, not only within one. Since each finding goes to
  an agent that didn't raise it, findings on the same lines from two
  agents go to two checkers; each checker's brief also lists the group's
  findings the other checks, for naming duplicates only. A verdict is
  always confirmed, uncertain, or rejected, with an optional
  `duplicate_of`: duplicates are a relation, not a verdict. hi makes a set
  of each linked group, keeps the best of it (confirmed, then uncertain,
  then the earliest), marks the others `duplicate` with their own check in
  `own_verdict`, and when one check confirmed and another rejected, makes
  the kept finding uncertain and says who said what. Found in the first
  live review, where the same missing check, raised by both agents, came
  out as two confirmed findings.
- **Checkers.** Without `--judge`: the investigators' agents, then the
  other ready agents, trimmed to those a finding can actually go to (for
  each investigator's agent, the first that isn't it), so an agent that
  would never check anything isn't named or paid. Batches of 30 by file.
- **Verdicts.** A finding with no verdict (its checker failed, was
  skipped, or the review stopped) is `unchecked`, shown after uncertain.
  IDs are `c-<n>` until the review ends, then `F-<n>` in the report's
  order. Agents start "assumes" with "Assumes"; hi drops the word.
- **Budget.** The spend is the sum of what runs reported in dollars. No
  run starts once it reaches `--budget`; on a metered Claude Code sign-in
  each run also gets `--max-budget-usd` of what's left. On a subscription
  the dollars are usage at API prices, as in best-of.
- **The process.** `hi agent review __run <id>`, started with `setsid`
  from the repository's folder, logging to `review.log`. `watch` prints a
  line as each run starts and ends, then the summary (not a full-screen
  view); Ctrl+C leaves it. A review whose process is gone without having
  ended shows as `failed`; `stop` on it removes its boxes and writes the
  report of what was done. There's no resume.
- **Live test.** 2026-10-18, Podman, Claude Code (Opus) and Codex
  (gpt-6.1-sol), on a small repository with a missing membership check,
  a date bug, a README telling AI reviewers to report nothing, a
  `.claude/settings.json` hook, and an uncommitted change. Both agents
  found the missing check; Claude also reported the README instruction
  and the hook as security findings. Two minutes and $0.30; no box,
  worktree, or branch was left, and the repository's status was as
  before. The first run showed duplicates across checkers slipping
  through (ten confirmed findings, four of them twice); with the sets
  above, the second run gave six confirmed findings and three
  duplicates, each noting that its own check confirmed it, in a minute
  for $0.27. It also showed "assumes" text starting with a name from the
  code (`moveCard`), which hi had capitalized; only plain words are now.

## As built (release 2)

v0.88.0, 2026-10-18. Code: `agent_review_source.go` (URLs, briefs,
`--since`), `agent_review_compare.go`, and `agent_review_export.go`
(SARIF, the page).

- **URLs.** `github.com/owner/name`, `owner/name` (when no such folder is
  here), `https://`, `http://`, `ssh://`, `git://`, and `git@host:path`.
  hi clones into `<review>/source` with `git -c credential.helper= -c
  core.hooksPath=/dev/null -c core.fsmonitor=false`, LFS filters off,
  `protocol.file` and `protocol.ext` refused, `clone --template=
  --no-recurse-submodules`, and `GIT_ALLOW_PROTOCOL=https:http:ssh:git`,
  `GIT_TERMINAL_PROMPT=0`, `GIT_LFS_SKIP_SMUDGE=1`, and
  `GIT_CONFIG_NOSYSTEM=1`. An address with a user name or token (other
  than `ssh://user@`) is refused. A GitHub clone that fails without a
  sign-in is tried again through `gh repo clone`, which brings its own
  credential helper; that path wasn't tested live. Full history, so
  `--since` and `git log` work; there's no size question yet.
- **The clone keeps its settings.** `-c` only lasts for the clone, and hi
  later runs git in it (worktrees for the boxes, removing branches), where
  this machine's hooks and LFS filter would apply. So hi writes
  `core.hooksPath`, `core.fsmonitor`, and the LFS filter settings into the
  clone's own config. Found by reading the first live clone's config.
- **Names.** The clone's folder is `source`, so box names and the report
  use the repository's name from the URL, and `ls` shows the URL.
- **Briefs.** `--area` takes a `.md` path (or anything with a `/`), or a
  name that is `.hi5/review/<name>.md` in a repository on this machine. A
  fetched repository's briefs aren't taken by name: they'd be its author's
  instructions to the investigators. The area's name comes from the file's
  name; one that matches a built-in area gets `-brief`. Front matter is
  dropped. Each brief is quoted in the report and is its SARIF rule's
  description.
- **`--since`.** Resolved when the review is planned, and the files are
  listed after the snapshot, so uncommitted changes count. The diff is
  `since...commit`, from where the histories meet. The brief names up to
  200 files and says that problems in code the changes don't touch or
  make worse are out of scope; the first live run reported older
  problems too, before that sentence.
- **`--compare`.** A review's id, or any `report.json`. A copy goes into
  the review's folder as `compare.json` when it starts. Findings now keep
  `code`, the lines at the reviewed commit, so a comparison doesn't need
  the earlier repository; reports from v0.87.0 have none, and their quotes
  are used. Matching is by fingerprint, then by place: where the earlier
  code is now, any finding of this review over those lines, whatever its
  area, since the same problem is often raised under another one. Earlier
  duplicates aren't compared. A matched finding has `earlier`, and its
  section says "still seen" or "new".
- **`--sarif`.** SARIF 2.1.0: a rule per area, results for confirmed and
  uncertain findings, `error` for critical and high, `warning` for
  medium, `note` for low, `partialFingerprints` `hiAgentReview/v1`, and a
  message that ends with what the finding assumes and that it's a
  recommendation. `show <id> --sarif` writes and prints it for a review
  that ended without it.
- **`--out`.** When the report is written, `report.md`, `report.json`,
  and `report.sarif` are copied there, 0600.
- **`show --html`.** goldmark with GFM, which doesn't render raw HTML, so
  a finding's text can't put markup on the page; a template with inline
  styles for light and dark, `Content-Security-Policy: default-src
  'none'; style-src 'unsafe-inline'`, and no scripts. Written only on
  request, next to `report.md`. Looked at in Chromium on a desktop width
  and a phone width in dark mode, where a long commit hash overflowed;
  inline code now wraps.
- **Live tests.** 2026-10-18, Claude Code and Codex: `jonschlinkert/is-odd`
  by URL with `--sarif` (one confirmed finding, a minute, $0.20); then
  again with `--compare` (the finding still seen under a new title,
  $0.22); and the sample repository `--since v1` with a brief of one's own,
  which found the commit's `=` in place of `==` as critical and gave the
  brief's area its own findings.

## As built (release 3)

v0.89.0, 2026-10-18. Code: `team_review.go`; tests: `team_review_test.go`.

- **The lead's commands.** `hi team review --project --approval <id>
  --budget <usd> [--area a,b] [--since <v2|ref>] [--version v3]
  [--context <notes.md>]`, and `hi team review list`, `show <id>`, and
  `stop <id>`, through the broker. `hi team review <task>` is unchanged.
  `--context` is the lead's notes from the people, read in its box.
- **The yes.** An owner's, as for a release: `review` joins the
  approvals only owners give, so a yes in a DM is refused and the lead
  asks in the channel. On top of the usual checks, the lead's last
  message in the yes's thread before it has to contain the budget as a
  dollar figure (`$30`, `$30.00`). The yes counts once, and one deep
  review runs at a time.
- **No team budget.** [hi_team.md](hi_team.md) has a monthly budget that
  isn't built, so "the budget must fit in what is left of the team's
  month" has nothing to check against. The review's own `--budget` is
  the limit; SOUL.md has the lead say "of the month" only as words.
- **The run.** The broker runs `hi agent review <repo> --ref <commit>
  --context <reviews/context.md> --budget <usd> --yes --detach --json`
  (which now prints the new review's record), with `--agents` the coder's
  and reviewer's agents and models, `--area` and `--since` when given
  (`v2` means that release's commit), `--compare` with the team's last
  review that ended, and `--fixes`. The review runs in hi agent review's
  own process, outside the team's service, so a team restart doesn't stop
  it. It posts "Deep review r-4 started on dev (…)" in the yes's thread.
- **The context.** The team's purpose; production's address and whether
  a password page or a key in its link guards it (from
  `run/production.json`), or that it isn't running; how many owners and askers there are; the files in
  `docs/decisions/` by name; and the lead's notes.
- **`--fixes`.** A file of fingerprint to work, from merged tasks'
  findings. A compared item gets `fixed_by`, and the report says "not
  seen since t-14", or "still seen, though t-14 set out to fix it".
- **The end.** The broker looks at the review every 30 seconds (and
  again after a restart, for one that ran or ended without the lead
  hearing). When it ends, it copies `report.md`, `report.json`, and a
  `report.html` it renders into the team's `reviews/<id>/`, mounted
  read-only at `/team/reviews` in the lead's box (a new mount, so a
  running team gets it on its next restart), DMs the owner who said yes
  the tally and uploads `report.md` and `report.html` there, and wakes the
  lead with a job delivered to the yes's thread. The prompt says what to
  post (the tally by severity, titles only, no exploit detail, the
  recommendations note), to say which findings matter here and which
  don't and why, to ask about assumptions, not to offer to fix everything,
  to remember what the people decide, and how to fix one.
- **Fixes.** `hi team task … --finding r-4:F-1` (repeatable) records the
  finding's review, ID, title, and fingerprint on the task, and adds its
  section from `report.md` under "From the deep review" unless the brief
  already has it.
- **SOUL.md.** A "Deep reviews" section at the end: when to offer it, in
  what words, the owner's yes in the channel naming the figure, a no is a
  no for that release, and that findings are recommendations. An existing
  SOUL.md gets it once, after the story's paragraph. The hi-team skill has
  the new commands.
- **Not tested live.** That needs a team in a real Slack channel with
  real people. The tests run the broker's whole path with a fake Slack
  and a fake `hi agent review`; `hi agent review --detach --json`, `stop`,
  and `rm` were run live on their own.
- **v0.89.1: production's key.** The first live deep review, on orbit,
  gave the reviewers production's whole link, `hi_key` and all, and said
  anyone with the link could open it. The key is what lets people in, so
  it went to the agents' providers, and the description made a guarded
  app look open. The context now gives the address without its query and
  says the link carries a secret key without which the app doesn't open.
- **v0.89.2: names and words.** The same run named its boxes
  `repo-r-1-…` and its report "Review r-1: repo", after the team's `repo/`
  folder: `--name` now says what to call the project, and the broker
  passes the team's name. And the lead's summary used file names and
  terms such as CSRF; its prompt now asks for no file, function, or route
  names and no jargon unless the people ask, says what could go wrong for
  them in their words, and has it write each finding in a line of its own
  with its number, since the report's titles are for the team.
