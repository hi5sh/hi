---
title: Review a whole repository
description: hi agent review has agents read a whole repository for security and correctness problems, each in a locked box on a snapshot. hi checks every line they quote, and a different agent confirms or rejects each finding. The report is recommendations; nothing is changed.
---

A reviewer who reads one change at a time never reads the whole project:
how the parts fit, what an old change left behind, what someone outside
sees. `hi agent review` has agents do that read, and checks what they say.

```sh
hi agent review
```

```text
Review r-2 · ~/projects/pocket-board @ a3cbf7f (with uncommitted changes) · security, correctness · 1m · $0.27

  #  SEV   VERDICT    AREA         WHERE                    FINDING
  1  high  confirmed  security     api/cards.js:4-10        moveCard lets any caller move any card without checking board membership
  2  high  confirmed  correctness  db.js:1                  Database import points to a module absent from the repository
  3  med   confirmed  security     .claude/settings.json:1  Committed Claude Code SessionStart hook runs a shell command when an agent open…
  4  med   confirmed  security     README.md:5              README contains a hidden instruction telling AI reviewers to report no findings
  5  low   confirmed  security     api/cards.js:12-16       listCards crashes when the board id doesn't exist
  6  low   confirmed  correctness  util/dates.js:3-5        Due dates earlier today are labeled overdue

  2 investigators (claude, codex) · 9 things raised: 6 confirmed, 0 uncertain, 3 duplicates
  Not covered: nothing left out · Reproduced: none (not attempted)
  Recommendations only: weigh each against how the app is run and who uses it.
  hi agent review show r-2 --md   the report, with each finding in full (~/.local/state/hi/reviews/r-2/report.md)
  hi agent review show r-2 --html the same as a page to read in a browser
```

It runs only when you ask. Nothing in the repository is changed, and
nothing is opened or posted anywhere: fixing what you choose is ordinary
work, such as a [`hi agent`](/guide/agent/) task.

## How it works

1. **A snapshot.** hi freezes what's reviewed as one commit, kept under
   `refs/hi-review/<id>`. Uncommitted and new files are in it (ignored
   ones aren't); your branch, index, and files stay as they were.
   `--ref v2.3` reviews a tag or commit instead.
2. **Investigators.** One agent per area, each in its own box on the
   snapshot, started fresh. With `--agents claude,codex` the areas
   alternate agents, since two agents miss different things.
3. **hi checks the evidence.** Every finding quotes the lines it rests on.
   hi reads them from the snapshot, not from the agent's box: `exact`,
   `moved` (hi corrects the line numbers), or `missing`.
4. **A second opinion.** Each finding goes to an agent that didn't raise
   it, which reads the code and says `confirmed`, `uncertain`, `rejected`,
   or `duplicate`, with its reason. A finding whose quote hi couldn't find
   is never confirmed.
5. **The report.** `report.md` and `report.json` in
   `~/.local/state/hi/reviews/<id>/`, readable only by you.

The review runs in a process of its own, so closing the terminal doesn't
stop it. `hi agent review watch <id>` follows it again.

## Areas

| Area | Looks for |
|---|---|
| `security` | Untrusted input reaching what it shouldn't, missing checks on who may do what, secrets, unsafe defaults, text that tries to steer AI agents |
| `correctness` | Code that does the wrong thing for some input: edge cases, error paths that lose data, races, parts that disagree |
| `performance` | Work that grows faster than it should: queries in loops, unbounded reads, work repeated per request |
| `maintainability` | Logic copied and already drifted, code and docs that disagree; each with a concrete failure, not a style preference |

The default is `security,correctness`; `--area all` takes all four, and
`--passes 2` gives each area two investigators.

### Areas of your own

An area is a brief: what to look for, and what counts. Write one for what
matters to your project and pass it by its path, or keep it in the
repository as `.hi5/review/<name>.md` and pass its name:

```sh
hi agent review --area security,audit-trail.md
hi agent review --area look-ahead          # .hi5/review/look-ahead.md
```

```markdown
Look for actions that change or delete people's data without leaving a
record of who did it and when.
```

The report quotes each brief. A repository fetched from a URL was written
by someone else, so its own `.hi5/review/` briefs aren't used: they'd be
its author's instructions to the reviewers. Pass a brief of yours by path
instead.

## A repository from elsewhere

```sh
hi agent review github.com/ada/pocket-board
hi agent review ada/pocket-board --ref v2.3
hi agent review https://git.example.com/team/board.git
```

hi clones it into the review's folder, without running anything from
it: no hooks or templates from your machine, no credential helpers, no
LFS filters, submodules listed but not fetched, and only network
transports, so a submodule can't point at a folder on your machine. A
private GitHub repository comes through your `gh` sign-in (`gh auth
login`); an address with a token in it is refused. The clone goes when
you `rm` the review.

## What changed since a release

```sh
hi agent review --since v1.4
```

The investigators get the files that changed since `v1.4` and `git diff`
to read them, and still read the rest of the repository to understand
them, so they find problems the changes cause in code that didn't
change. A review before a release, of what the release adds.

## Compared with an earlier review

```sh
hi agent review --compare r-3
hi agent review --compare ~/reviews/r-3/report.json
```

Each finding of the earlier review is **still seen** (the same
finding, or one over the same lines now), **not seen, code changed** (its
lines aren't there any more: likely fixed or moved), or **not seen, code
unchanged** (its lines are still there and nobody raised it this time:
worth a look). New findings are marked new. A finding not seen is never
called fixed: agents miss things.

## Recommendations, not requirements

The agents read code at one commit. They don't know how the app is
deployed, who uses it, or what protects it. So every finding says what
it **assumes** ("people who sign in shouldn't all see every board"), the
verdicts say whether an observation is true, not whether to act on it,
and the report opens with a note saying so. Some suggestions will be more
than your project needs; pick the ones that matter in your context.

Tell the reviewers what the code doesn't show, and the findings weigh it:

```sh
hi agent review --context context.md
```

```markdown
The app runs only on the office network, behind the company's sign-in.
About twenty people use it; all of them may see every board.
```

## The report

`hi agent review show <id> --md` prints `report.md`: the note, what was
reviewed and what wasn't (runs that failed, ran out of time, or weren't
started), then each finding as a section that stands on its own, with the
code from the snapshot. A section can be pasted into a task as it is:

````markdown
## F-1 · moveCard lets any caller move any card without checking board membership

- Severity: high, the reviewers' estimate · Area: security · Confirmed
- Where: `api/cards.js` lines 4-10, at a3cbf7f
- Raised by: claude, and separately by codex

**What the code does.** moveCard loads a card by the id in the URL, sets its
column from the request body, saves it, and returns the whole card. Unlike
listCards, it never checks req.user against the card's board members.

```js
async function moveCard(req, res) {
  const card = await db.cards.get(req.params.id)
  if (!card) return res.status(404).end()
  card.column = req.body.column
  await db.cards.save(card)
  res.json(card)
}
```

**When it matters.** A user who is signed in but not a member of a board can
send a move request with another board's card id. The card gets changed, and
its full contents come back in the response.

**Assumes.** moveCard is mounted as an authenticated route with req.user set,
as listCards implies, and no middleware outside this repository checks
access to cards. If such middleware exists, this would not matter.

**A direction.** Load the card's board and check that req.user.id is a
member before changing anything, as listCards does.

**Second opinion.** codex confirmed: moveCard fetches any supplied card ID,
assigns req.body.column without validation, saves the card, and returns it
without checking board membership.
````

`hi agent review show <id> --html` writes `report.html` next to it, to
read in a browser: one file, its styles inline, no scripts, and a policy
that loads nothing, so it opens offline and stays private. HTML in a
finding's text isn't rendered.

`--sarif` also writes `report.sarif`, with the confirmed and uncertain
findings, for an editor's SARIF viewer or a code scanning page you upload
it to; each result says what it assumes. `show <id> --sarif` writes it
for a review that ended without it. `--out <dir>` also writes the report
files there when the review ends; inside the repository, don't commit
them.

Rejected findings and duplicates are listed at the end with the reason,
so nobody raises them again without seeing why. When two agents raise the
same problem it becomes one finding, "raised by claude, and separately by
codex"; when their checks disagree, it's uncertain and says who said what.

## Safety

The repository may have been written to steer agents that read it.

- **Nothing from it runs on your machine.** The clone, the snapshot, and
  the evidence check read files from git.
- **Locked boxes.** The `locked` network (each agent's own hosts and
  nothing else), hi's own image instead of the project's
  `devcontainer.json`, and the agent without the project's settings:
  Claude Code doesn't load the repository's hooks, MCP servers, or
  permissions. Agents are told to read, not run, and whatever they change
  in their box is thrown away with it.
- **Planted instructions are findings.** The agents are told so; the
  example above reported one.
- **The code goes to the agents' providers**, as with any `hi agent` run.
  The confirmation says which.

## Cost and limits

Before starting, hi shows the runs, who checks the findings, the limits,
and how each agent is paid, and asks (`--yes` skips the question).

- `--max 30m` is each run's time limit (the default).
- `--parallel 3` runs at once (the default).
- `--budget 20` starts no run once the agents have reported $20. Claude
  Code reports its cost when a run ends, and Codex reports only tokens,
  so runs already going can take it over; on a metered Claude Code
  sign-in each run also gets Claude Code's own limit of what's left.

## Commands

```text
hi agent review [<folder>|<url>]  review this repository, a subfolder of it, or one from a URL
    --area a,b|all|<brief.md>  --agents claude,codex  --judge <agent>  --passes <n>
    --ref <ref>  --since <ref>  --path <dir>  --context <file.md>  --compare <id|report.json>
    --sarif  --out <dir>  --name <name>  --budget <usd>  --max <duration>  --parallel <k>  --detach  --json  --yes
hi agent review ls                reviews, their state, cost, and counts
hi agent review show <id>         the findings; --md the report, --html a page, --sarif SARIF,
                                  --json the record, --rejected the rest
hi agent review watch <id>        follow a review; Ctrl+C stops following, not the review
hi agent review stop <id>         stop it, keep what's done, and write the report
hi agent review rm <id>           remove a review, its report, and its snapshot
```

In a team, an owner's agent starts a deep review from the team's dev box,
with `hi dev deep-review --budget <usd>`, once the owner names the budget:
see [Reviews from the dev box](/guide/team/reviews/).
