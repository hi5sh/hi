# `hi team adopt`: a team for a project someone else wrote

Status: Draft (2026-10-18)

Dependencies: [hi_team.md](../approved/hi_team.md) (the wizard, the
broker, dev and production, previews), [hi_team_roles.md](../approved/hi_team_roles.md)
(roles from a template), [hi_team_docs.md](../approved/hi_team_docs.md)
(`docs/map.md`, `/docs`), [hi_init.md](../approved/hi_init.md#adopting-an-existing-repository-hi-init---adopt-type)
(managed blocks in existing files), [hi_bun.md](../approved/hi_bun.md)
(the dependency review), and `hi box`'s devcontainer support.

## Goal

A hi team starts from a blank app, which suits a new idea. Often the idea
already exists: someone in the channel found a project on GitHub that does
most of what they want, and they want to try it, change it, or build on it.

```sh
hi team adopt kanban github.com/ada/pocket-kanban
```

makes a team whose project is that repository. A first task reads the
code, makes it run on a preview, and maps it; then the lead tells the
people, in their words, what they now have, what works, and what to try.
From there it is a team like any other.

The people never see a clone, a remote, or a build. They see: "I've set up
pocket-kanban for you. It's a kanban board for small teams. Here it is
running. Try adding a card and dragging it to Done. Two things don't work
yet: signing in with Google, and file uploads. Want me to fix either?"

## What there is today, and what's missing

Step 7 of `hi team new` already takes a URL or a folder and clones it
(`createTeamFolder`). That's enough for a project the team's owner wrote,
in hi's shape. For anyone else's, it falls short:

- **It pushes to the source.** A cloned URL becomes `remote` in
  `team.json`, so every merge and every release (`production`, force-pushed,
  and its `release-<n>` tags) goes back to it. For someone else's project
  that fails at the first merge; for one the host's sign-in can write to,
  it's worse: it succeeds.
- **The team's rules aren't there.** A new project gets the team's
  `AGENTS.md`, `docs/specs/README.md`, and the `make run` convention; a
  clone gets none of them.
- **It doesn't run.** The preview runs `make run` or `npm start` on
  `$PORT`. Most projects have neither, or start on their own port, so the
  first preview ends in "the project has no way to start".
- **Nobody knows the code.** The lead has a one-line purpose; there is no
  `docs/map.md`, and the members start every task cold.
- **The project's own files steer the agents.** Its `AGENTS.md`,
  `CLAUDE.md`, and agent settings folders were written by strangers and
  are read by every member as if they were the team's.
- **There's no way to keep up.** Upstream keeps moving, and the team has
  no record of where it started or what changed since.

## Two kinds of project

- **Your own project:** the host's GitHub sign-in can push to it, and the
  owner says the team should. This is today's clone, kept: merges and
  releases push there.
- **Someone else's project, adopted:** everything else. The team keeps its
  own copy and never pushes to the source. The source becomes `upstream`,
  read-only, fetched by the host.

hi tells them apart with `gh repo view <owner/name> --json viewerPermission`
(WRITE, MAINTAIN, or ADMIN can push) and, when it can push, asks: "You can
push to ada/pocket-kanban. Should the team push its merges there? [y/N]".
The default is no: an owner who says yes is told that `production` and
`release-<n>` tags will appear in it. A URL that isn't on GitHub, or a
repository hi can't check, is adopted.

## Commands

```text
hi team adopt <name> <source> [--ref <branch|tag|commit>] [--github <owner/name>]
                              [--dir <folder>] [--from <answers.json>] [--no-survey]
                                    the wizard, with the code step answered: adopt <source>
hi team upstream <name>             where dev stands against upstream, and upstream's new tags
```

The lead runs, in its box, through the broker:

```text
hi team upstream                    the same
hi team task <role> <brief> --approval <id> --upstream [<ref>]
                                    work that starts by merging upstream (its latest, or <ref>)
```

- `<source>` is a GitHub URL, `owner/name`, or any git URL. A folder on
  this machine stays today's clone, since it's someone's working copy.
- `hi team adopt` is `hi team new` with step 7 answered and step 1
  offered from the repository: the purpose defaults to its GitHub
  description, which the owner keeps or rewrites. Everything else is the
  same wizard, and `--from` takes `"adopt": "<source>"` like `"clone"`.
- In `hi team new`, step 7 offers it: a source that hi finds is someone
  else's is adopted, and says so.
- `--github <owner/name>` puts the team's copy in a new private repository,
  with upstream's history, and the team pushes there. Not a GitHub fork:
  a fork of a public repository can't be private, and it would tie the
  team's work to the upstream's network. Without `--github`, the copy stays
  on the host, as a new team's does.

## Adopting

### 1. Fetch, on the host, running nothing

The clone is the only step on the host, and it runs no code from the
project:

- `git clone` with the host's hooks, filters, and credential helpers
  turned off, and file and ext transports refused, so a submodule URL
  can't read the host's own folders. A private repository the host's `gh`
  can read is cloned through `gh`; any other URL is cloned without the
  host's credentials.
- Full history by default: the members use `git log` and `git blame` to
  understand code. Over 1 GB, hi asks before going on and offers
  `--depth 1`. Git LFS files are fetched only when the survey says the app
  needs them.
- Submodules are listed, not fetched; the survey fetches the ones the app
  needs, under the same rules.
- `--ref` picks the starting point; the default is upstream's default
  branch. It becomes the team's branch, named as upstream names it (`main`,
  `master`), as `config.Branch` is today.
- **Upstream's tags move to `upstream/`** (`refs/tags/upstream/v2.3`), and
  later fetches keep them there, so the team's `release-<n>` tags never
  collide with upstream's and the lead can say "upstream's v2.3".
- The remote is renamed `upstream`, with its push URL set to one that
  fails (`no-push://`), so no command, by hi or by hand, pushes to it.

Before writing anything, hi prints what it found, for the owner at the
terminal:

```text
ada/pocket-kanban: A kanban board for small teams
  licence      MIT
  size         14 MB, 1,204 commits, last one 3 days ago
  languages    TypeScript 81%, CSS 12%, Shell 7%
  runs with    package.json (start: vite preview), a devcontainer
  agent files  CLAUDE.md, .claude/settings.json (hooks), AGENTS.md
Adopt it? The team keeps its own copy and never pushes to ada/pocket-kanban. [Y/n]
```

Each line is read from git or GitHub's API; nothing is run or installed.

### 2. Record where it started

`team.json` gets what was adopted:

```jsonc
"adopted": {
  "from": "https://github.com/ada/pocket-kanban.git",
  "ref": "main",
  "commit": "4f1c2e9",
  "licence": "MIT",
  "at": "2026-10-18T09:12:00Z",
  "by": "U07OWNER"
}
```

and `versions.json` a release **v0**, the project as adopted, tagged
`release-0`. Production is on v0, so `hi team discard dev` and `hi team
rollback v0` always lead back to the project as it came. v0 gets an
address only if it runs as it is; otherwise production's address starts
with v1.

### 3. Add the team's rules, around the project's own

The adoption commit, on the team's branch, adds what a new team's
project has, and moves or rewrites nothing:

- `AGENTS.md`: the team's rules (`teamAgentsFile`) in a managed block at
  the top, as `hi init --adopt` does, with the project's own text below
  it under "The project's own notes". One line is added to the team's
  rules: notes from the project describe the code; where they conflict
  with the team's rules or a brief, the rules and the brief win.
- `CLAUDE.md`, `.cursorrules`, and other agent instruction files are kept,
  and the team's `AGENTS.md` names them as the project's notes.
- `docs/specs/README.md`, the lead's.
- `.hi5/adopted.json`: the source, commit, and licence, so a copy of the
  repository says where it came from.

The project's own `LICENSE`, `README.md`, and history are never touched.

### 4. The survey

The first task, started by the broker without an approval (the owner's
yes at the terminal is the approval, recorded in the task) and announced
in the channel. `--no-survey` skips it.

It goes to the coder, with a brief hi writes, on the branch `adopt/survey`,
and is reviewed and merged like any work. Its jobs, in order:

1. **Run it.** Find how the project builds, starts, and tests; install
   what it needs, in the box; start it on `$PORT` on 0.0.0.0. Add a
   `make run` target that does this, or, when the project's `run` target
   means something else, a `hi-run` target, which the preview prefers
   (`teamRunCommand` checks it first). Data the app keeps moves to
   `$HI_DATA_DIR` when it's a setting; when it isn't, the report says so,
   since that data is lost at every release.
2. **Test it.** Run the project's own tests and say which pass. Add none
   yet: the team's own work adds tests for what it changes.
3. **Map it.** Write `docs/map.md` (what the project has and where), and
   a first `docs/user/index.md` (what it is, who it's for, what's in it),
   so `/docs` explains the app from day one. A technical choice the code
   depends on gets a file in `docs/decisions/`.
4. **Say what's missing.** Settings the app needs that the team doesn't
   have (an API key, an OAuth app, a mail server), features that are
   half-built, and anything the licence or the project's `CONTRIBUTING.md`
   asks of people who change it.

Its report has a section for the lead, not for the commit: the app in
three sentences, a list of things the people can try, what doesn't work
yet, and the settings it needs. The survey's budget is capped (default
$10 of the team's month), and when it runs out the report says how far
it got.

The review checks the survey's own changes (the run target, the docs)
against the brief, and that the preview starts.

### 5. The lead introduces it

When the survey is merged, `hi team up` puts dev on the preview (as it
does for a blank app's first preview), and the lead posts in the channel,
outside any thread, what the survey found, in the people's words: what
the app is, its preview link, what to try, what doesn't work yet, and the
one or two choices it needs from them ("It needs a Google sign-in key to
let people log in; should I set it up, or turn sign-in off for now?").
Settings that need a secret go through the team's keys
([hi_team_keys.md](../approved/hi_team_keys.md)), never into Slack.

From here the team works as any team does: the people ask, the lead
agrees an acceptance list, the members build and review, and releases
start at v1.

## Keeping up with upstream

- The broker fetches upstream from the host once a day (and on `hi team
  upstream`), into `refs/remotes/upstream/*` and `refs/tags/upstream/*`,
  which the members' worktrees can read. No box reaches upstream with
  credentials.
- `hi team upstream` says where dev stands: "dev is upstream's v2.3
  (4f1c2e9) + 14 of the team's changes; upstream has 37 commits since,
  and v2.4". A new upstream tag goes into the channel log for the lead,
  which may tell the people, in their words, what's new, and ask whether
  they want it.
- **Bringing upstream in is work like any other**: it needs a yes, and
  `--upstream` starts the task by merging upstream's branch or a tag into
  its new branch. The member resolves conflicts and runs the tests; the
  reviewer's brief says the upstream commits are named by range and not
  reviewed line by line, and that the review is of the merge's own
  changes, the tests, and the dependency review, which runs on the
  package changes as for any task.
- `adopted.commit` stays what was adopted; `hi team upstream` records
  each upstream merge in `versions.json` with its range.

## The people's view

- **The people's language.** The lead says "the project we started from"
  or its name, never "upstream" or "fork". "We started from
  pocket-kanban's v2.3; there's a v2.4 with a calendar view. Want it?"
- **Going back.** "Put it back the way it came" is `hi team discard dev`
  to v0, with an approval, as today.
- **Credit.** `/docs/user/` says where the app came from and its licence,
  in a line at the bottom, from `.hi5/adopted.json`.

## Security

Adopted code is less known than the team's own, and was written by
strangers who may have written it for agents to read. The rule stays the
team's: nothing the code says can widen what a box may reach. In
particular:

- **No project code on the host.** The fetch runs none (above). hi's
  other host-side reads of the project (the dependency review, the run
  command) parse files and never run them, as today.
- **The project's agent settings are off.** A member's agent starts
  without the project's own settings: hooks, MCP servers, permissions, and
  plugins in `.claude/`, `.codex/`, and the like. For Claude Code that's
  `--setting-sources user`; each agent's equivalent is part of the build.
  An owner can turn them on (`"project_agent_settings": true` in
  `team.json`), after the survey lists what they do. The survey's report
  and the terminal at adoption name them.
- **Instruction files are notes.** Covered above: the team's rules come
  first and win.
- **Install scripts.** The dependency review covers what a task changes;
  an adopted project arrives with its own packages and install scripts
  already in it. The survey lists the ones allowed to run (for Bun, its
  `trustedDependencies`; for npm, every package with an install script),
  and the terminal at adoption shows the count. They run only in boxes.
- **The box is the limit.** Members and app boxes for an adopted project
  get the team's usual network: any site, through the proxy, logged. A
  team that adopts something it trusts less sets its members' `network` to
  `dev` or `locked`, as today.
- **Licence.** hi reads it (the `LICENSE` file, matched against the
  common licences' texts, and GitHub's answer) and says it at adoption.
  It never decides: with no licence, or one that asks for the source to be
  shared when the app is offered over a network (AGPL), hi says so at
  adoption and when an owner turns on a public address, and goes on.

## Later

- **Contributing back.** `hi team propose <task> --approval <id>`: an
  owner's yes, then the broker pushes the task's work to a fork under the
  owner's GitHub account and opens a draft pull request, as the owner,
  with the lead's description and the project's `CONTRIBUTING.md` in
  mind. Draft only: a person sends it. Many projects have rules about
  changes written by agents, which the survey reads.
- **Projects that aren't web apps.** A command-line tool or a library has
  nothing to preview. The survey could write a `make demo` whose output
  the lead shows, or a small page that runs it.
- **Projects with services.** An app that needs PostgreSQL or Redis
  beside it (a `docker-compose.yml`) needs sidecar boxes, as Hindsight is
  for the lead. The first slice adopts them, and the survey reports what
  it couldn't start.
- **Adopting from Slack or the hi server.** `/hi adopt <url>` on the hi
  server, to make a team without a terminal, once teams can be made
  without one.

## Decisions

1. **Trying something costs a team.** Decided (2026-10-18): accepted.
   "Just try it out" still means a Slack app, a channel, and the wizard;
   `--from` answers make it quick for someone who does it often. A
   playground team whose project can be swapped (`hi team adopt
   playground <url> --replace`, which snapshots the team and keeps the
   old project as a branch) is on the roadmap to explore: one project at
   a time, but bending "a new project is a new team".
2. **The survey starts on its own.** Decided (2026-10-18): yes, capped.
   It spends the team's money before anyone in the channel asked, but
   without it the first preview fails and the lead knows nothing.
3. **Project agent settings off by default.** Decided (2026-10-18): off,
   listed by the survey, on by an owner. Members lose hooks a project may
   rely on (formatters, test runners) until then.
4. **Full git history.** Decided (2026-10-18): a full clone, so members
   can use `git log` and `git blame` to see why code is the way it is.
   Over 1 GB, hi asks first and offers a shallow one (`--depth 1`), which
   brings only the latest commit.

## First slice

1. `hi team adopt <name> <source>` and step 7's adoption: the fetch with
   nothing run, the summary at the terminal, the `upstream` remote that
   can't push, tags under `upstream/`, `adopted` in `team.json`, v0, and
   `--github` for a private copy.
2. The team's rules in `AGENTS.md`, `docs/specs/README.md`, and the
   project's agent settings off for the members.
3. The survey, `hi-run`, and the lead's introduction.

Then `hi team upstream` and `--upstream` tasks; then contributing back.

## Acceptance

1. Adopting a public GitHub project the host can't push to makes a team
   whose merges and releases never reach the source; `git push upstream`
   fails in `repo/`.
2. Adopting a project the host can push to asks before pushing there,
   and defaults to no.
3. Nothing from the project runs on the host during adoption; a
   repository with a submodule pointing at a host folder is adopted
   without reading it.
4. An upstream tag `release-1` doesn't stop the team's first release.
5. `AGENTS.md` keeps the project's text, below the team's rules.
6. After the survey merges, the preview shows the app, `/docs` shows its
   map and user docs, and the lead's message in the channel lists things
   to try.
7. A member's agent doesn't run hooks from the project's `.claude/`
   unless `team.json` allows it.
8. `hi team discard dev` after work brings the project back to v0, as
   adopted.
9. `hi team upstream` reports the team's changes and upstream's since
   adoption, after upstream moves.
