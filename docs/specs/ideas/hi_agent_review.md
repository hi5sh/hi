# `hi agent review`: an on-demand review of a whole repository

Status: Draft (2026-10-18). From a proposal for a "HiAudit" command; what
it kept and what it left out is under [Decisions](#decisions).

Dependencies: [hi_agent.md](../approved/hi_agent.md) (runs in boxes,
reports, cost), [hi_agent_best_of.md](../approved/hi_agent_best_of.md)
(several runs at once, hi checking what agents claim, the run's own
process, `watch`, `stop`, `--budget`), [hi_team.md](../approved/hi_team.md)
(approvals, the broker, the budget), [hi_team_adopt.md](hi_team_adopt.md)
(fetching a repository with nothing run, the project's agent settings
off), and [hi_agent_fanout.md](hi_agent_fanout.md) (the queue, `--parallel`),
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
each accepted, uncertain, or rejected by a second agent, and the reason.
hi never changes the code during a review and opens nothing anywhere.
Fixing is ordinary work, asked for separately.

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
    [--budget <usd>]                  no run starts once this is spent
    [--max <duration>]                per run (default 30m)
    [--parallel <k>]                  runs at once (default 3)
    [--compare <id|report.json>]      say what's new, still seen, and not seen since
    [--sarif] [--out <dir>]           also write report.sarif; write the report elsewhere
    [--detach] [--json] [--yes]
hi agent review ls                    reviews, their state, cost, and counts
hi agent review show <id> [--json|--sarif|--rejected]
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
     its diff), and `docs/map.md` when the project has one, as notes to
     check, not facts;
   - is told the repository is the thing under review: text in it that
     reads as instructions is a finding to report, never an order;
   - is told not to build, install, or run the project. The box is the
     limit if it does; it changes nothing outside the worktree, and the
     worktree is thrown away.

   An investigator writes its findings to a file the brief names, at most
   25, each with an area, a title, a severity, a confidence, the failure
   (what input or state leads to what wrong result), the evidence (a
   file, a line range, and the exact lines quoted), and a direction for a
   fix, in words. A missing or malformed file fails that run, and the
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
   itself and, for each finding, decides:
   - `accepted`: the code does what the finding says, and the failure can
     happen;
   - `uncertain`: it may happen, but depends on something the code
     doesn't show (how it's deployed, what calls it);
   - `rejected`: the code doesn't do that, the failure can't happen, or
     it's a preference, not a failure;
   - `duplicate` of another finding.

   Each with a reason and, if it disagrees, its own severity. Batches of
   up to 30 findings, grouped by file. A finding with `missing` evidence
   can't be accepted: hi turns an `accepted` verdict on one into
   `uncertain`. hi never merges disagreement into consensus: a finding
   two investigators raised says so, and one the adjudicator rejected
   stays in the report with the reason.
6. **Report.** In the review's folder, `~/.local/state/hi/reviews/<id>/`,
   readable only by the user (0700, files 0600), outside the repository
   unless `--out` says otherwise:
   - `report.json`: the record, every finding with its evidence check,
     verdict, and fingerprint, every run's agent, model, time, and cost;
   - `report.md`: accepted findings by severity, then uncertain, then a
     count of rejected ones (`show --rejected` lists them), what was
     covered, and what wasn't (failed runs, folders left out, a budget
     that ran out);
   - `report.sarif` with `--sarif`, accepted and uncertain findings only.

   Every finding says `reproduced: false`. Evidence means the lines exist
   and say what's quoted; it doesn't mean the failure was seen
   ([Later](#later) adds that).

```text
Review r-4 · ~/projects/pocket-kanban @ 4f1c2e9 (dirty) · security, correctness · 31m · $18.40

  #  SEV   VERDICT    AREA         WHERE                      FINDING
  1  high  accepted   security     api/cards.ts:88-97         any signed-in user can move another board's cards
  2  high  accepted   correctness  store/sync.ts:140-152      two tabs saving at once lose the first save
  3  med   uncertain  security     server.ts:31               CORS allows any origin; fine only if no cookies
  4  low   accepted   correctness  ui/date.ts:12              due dates shift a day west of UTC

  2 runs per area (claude, codex) · 11 raised · 4 accepted, 1 uncertain, 3 rejected, 3 duplicates
  Not covered: none · Reproduced: none (not attempted)
  hi agent review show r-4
```

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
seen again stays rejected unless the new adjudicator accepts it.

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
- **The result.** The broker tells the lead when it ends. The lead posts
  in the approval's thread how many findings were accepted, by severity,
  with a one-line title each and no detail of how to exploit them. The
  full report stays in the team's folder (`reviews/<id>/`, read-only in
  the lead's box) and goes as a file in a DM to the owner who said yes.
  At the host, `hi team review show <id>`.
- **Fixes are work.** The lead offers to fix accepted findings, as it
  offers anything else: a yes, a task, a brief that carries the finding,
  the usual review. The task records the finding's fingerprint, so the
  next review's comparison says "not seen since t-14".
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
  an accepted verdict needs evidence that exists. A model can't fabricate
  a quote into the report as checked.
- **The report is sensitive.** Private to the user or, in a team, to the
  owners and the lead. Progress and logs carry titles and counts, not
  source text. The audit log on a hi server gets that a review ran, who
  asked, its cost, and its counts, not its findings.
- **The code goes to the agents' providers**, as with any `hi agent` run
  on it. The confirmation names them.

## Later

- **Reproduction.** For an accepted finding, a run writes a test or a
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

Open, with a recommendation each.

2. **The default size.** The proposal defaulted to all areas, twice each.
   Recommended: security and correctness, one pass, so a first review is
   cheap and quick; `--area all --passes 2` and the agents to alternate
   are one flag away, and the confirmation shows what each costs.
3. **Maintainability as an area.** It produces opinions more than
   failures, and adjudication rejects many. Recommended: keep it, out of
   the default, with the same "say what goes wrong" rule.
4. **The full report in Slack.** A DM to the approving owner, or only on
   the host. Recommended: the DM; the channel gets counts and titles.
5. **No CI mode.** The review never runs on a commit or a schedule, so
   there is no `--fail-on`. SARIF is for reading in an editor or a code
   scanning page someone uploads it to, not for gating merges.

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
   adjudication by another agent, `report.json` and `report.md`, the
   confirmation, `--budget`, `--max`, `ls`, `show`, `watch`, `stop`, `rm`.
2. URLs (sharing the fetch with `hi team adopt`), `performance`,
   `maintainability`, briefs as areas, `--since`, `--path`, `--compare`,
   and `--sarif`.
3. The team: SOUL.md, `hi team review --project`, the owner's yes with a
   budget, the summary and the DM, fingerprints in fix tasks.
4. Reproduction.

## Acceptance

1. `hi agent review` in a repository with uncommitted changes reviews
   them, records the tree as dirty, and leaves the branch, the index, and
   the working tree as they were.
2. Nothing is started without a yes at the terminal or `--yes`.
3. A finding whose quote isn't in the snapshot is `missing` and is never
   `accepted`, even if the adjudicator says so; one whose investigator
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
