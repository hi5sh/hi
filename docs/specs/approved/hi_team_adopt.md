# `hi team adopt`: a team for a project someone else wrote

Status: Approved (2026-10-18), built in three slices: adopting, the
survey, and the lead's introduction (v0.90.0); keeping up with upstream
(v0.91.0); and contributing back (v0.92.0). The ideas under Later are not
built.

Dependencies: [hi_team.md](hi_team.md) (the wizard, the
broker, dev and production, previews), [hi_team_roles.md](hi_team_roles.md)
(roles from a template), [hi_team_docs.md](hi_team_docs.md)
(`docs/map.md`, `/docs`), [hi_init.md](hi_init.md#adopting-an-existing-repository-hi-init---adopt-type)
(managed blocks in existing files), [hi_bun.md](hi_bun.md)
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
                              [--shallow|--full] [--no-survey] [--dir <folder>]
                              [--from <answers.json>] [--yes]
                                    the wizard, with the code step answered: adopt <source>
hi team upstream <name>             fetch upstream, and say where dev stands against it, with its new tags
```

The lead runs, in its box, through the broker:

```text
hi team upstream                    the same
hi team task <role> <brief> --approval <id> --upstream[=<tag>]
                                    work that starts by merging upstream (its latest, or <tag>)
hi team propose <task> --approval <id> --notes <file>
                                    offer merged work to upstream as a draft pull request (an owner's yes)
```

- `<source>` is a GitHub URL, `owner/name`, or any git URL. A folder on
  this machine stays today's clone, since it's someone's working copy.
- `hi team adopt` is `hi team new` with step 7 answered and step 1
  offered from the repository: the purpose defaults to its GitHub
  description, which the owner keeps or rewrites. hi fetches the project
  and asks before the wizard starts, so nobody sets up a Slack app for a
  project they then turn down. Everything else is the same wizard, and
  `--from` takes `"adopt": "<source>"`, with `"ref"`, `"shallow"`, and
  `"no_survey"`; a `"clone"` address is adopted too unless
  `"push_to_source": true` says it's the owner's to push to.
- In `hi team new`, step 7 offers it: a source that hi finds is someone
  else's is adopted, and says so. A no there asks for the repository
  again.
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
  can't read the host's own folders: `hi agent review`'s fetch
  (`fetchRepository`). A private repository the host's `gh` can read is
  cloned through `gh`; any other URL is cloned without the host's
  credentials. The clone keeps those settings for what hi does in it
  later.
- Full history by default: the members use `git log` and `git blame` to
  understand code. Over 1 GB (GitHub's size), hi asks before going on and
  offers only the latest commit (`--shallow`, `--depth 1`); without a
  terminal it needs `--shallow` or `--full`. A shallow copy stays on the
  host: it can't seed a new GitHub repository. Git LFS files stay
  pointers, and the survey says when the app needs them.
- Submodules are listed, not fetched; the survey says which the app
  needs.
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
  licence         MIT
  size            14 MB, 1,204 commits, the last one 3 days ago
  languages       TypeScript 81%, CSS 12%, Shell 7%
  runs with       package.json (start: vite preview), a devcontainer
  agent files     AGENTS.md, CLAUDE.md, .claude/ (hooks)
                  the members read them as the project's notes; its agent settings stay off
  install scripts 2 packages run their own code when installed, in the boxes only: esbuild, sharp
Adopt it? The team keeps its own copy and never pushes to ada/pocket-kanban. [Y/n]
```

Each line is read from git or GitHub's API (through `gh` when it's signed
in, else without a sign-in); nothing is run or installed. Languages are
by size, from git's own list of files; the licence is GitHub's answer,
else the common licence the `LICENSE` file's text matches.

### 2. Record where it started

`team.json` gets what was adopted:

```jsonc
"adopted": {
  "from": "https://github.com/ada/pocket-kanban.git",
  "github": "ada/pocket-kanban",
  "name": "pocket-kanban",
  "follow": "main",          // upstream's branch the team keeps up with
  "commit": "4f1c2e9…",      // upstream's commit it started from
  "licence": "MIT",
  "at": "2026-10-18T09:12:00Z",
  "by": "imanhabib"          // who ran hi team adopt, on the machine
}
```

and `versions.json` a release **v0**, the adoption commit (below), tagged
`release-0`. Production stays "nothing released yet": v0 is where the
team started, not a version anyone relies on, so it has no address, and
production's first release is v1. Before that, `hi team discard dev`
puts dev back to v0, the project as it came; after it, back to
production, as for any team.

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

It goes to the coder, with a brief hi writes, and is reviewed like any
work; the lead merges it. Its jobs, in order:

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

Its report ends with a section for the lead, "For the lead": the app in
three sentences, things the people can try, what doesn't work yet, and
the settings it needs. When the coder is Claude Code, the survey may
spend at most $10 (`hi agent --max-budget-usd`); Codex has no such
limit of its own.

The review checks the survey's own changes (the run target, the docs)
against the brief, and that the preview starts.

### 5. The lead introduces it

When the survey's work and its reviews end, hi wakes the lead once (a
Hermes job that runs once, as for work whose wait was cut short). The
lead merges it, runs `hi team preview`, and its answer is posted in the
thread of hi's message that the team adopted the project, in the
people's words: what
the app is, its preview link, what to try, what doesn't work yet, and the
one or two choices it needs from them ("It needs a Google sign-in key to
let people log in; should I set it up, or turn sign-in off for now?").
Settings that need a secret go through the team's keys
([hi_team_keys.md](hi_team_keys.md)), never into Slack.

From here the team works as any team does: the people ask, the lead
agrees an acceptance list, the members build and review, and releases
start at v1.

## Keeping up with upstream

- The broker fetches upstream from the host once a day (and on `hi team
  upstream`, and before an `--upstream` task), into `refs/remotes/upstream/*`
  and `refs/tags/upstream/*`, which the members' worktrees can read, with
  the same settings as the clone; a private repository through `gh`'s
  sign-in. No box reaches upstream with credentials. `run/upstream.json`
  says when it last fetched, and why it couldn't.
- `hi team upstream` says where dev stands: "Dev: pocket-kanban's v2.3
  (4f1c2e9) + 14 commits of the team's. Upstream: 37 commits on main
  since, and 1 newer tag: v2.4." The tag that names dev's upstream commit
  comes from `git tag --points-at`, since `git describe` gives a moved
  annotated tag its old name.
- **A new version.** When the daily fetch finds a new release tag (`v2.4`,
  `1.10.0`; not nightlies), hi posts it in the channel and wakes the lead
  under that message, at most once a week: the lead reads what changed
  (`git log`, the changelog) and says, in the people's words, what's new
  that they'd notice and whether they want it, and starts nothing. The
  first check takes what's there as what the team started with; a new
  version within the quiet week waits for the next check after it.
- **Bringing upstream in is work like any other**: it needs a yes (not
  `--small`, not `--on`), and `--upstream` (the latest on the branch the
  team follows) or `--upstream=<tag>` starts the task's brief with the
  merge to run on its branch, and asks the member to resolve conflicts,
  run the tests, and say what upstream changed that the people would
  notice. The reviewer's brief says the upstream commits are named by
  range and not reviewed line by line, and that the review is of the
  merge's own changes (`git show --remerge-diff`), the tests, and the
  dependency review, which runs on the package changes as for any task.
  Work dev already has is refused.
- `adopted.commit` stays what was adopted; each merge of such work is
  recorded in `team.json`'s `adopted.merges`, with what it brought in,
  from which commit, and the task, and `hi team upstream` lists them.

## Contributing back

When the people want a change of the team's offered to the project's
authors, the lead asks an owner, in plain words: which change, that it
goes to the public project as a draft under the GitHub account on the
team's machine, and that its maintainers may turn it down. It reads the
project's contributing guide first, since some projects don't take
changes written by agents. On an owner's yes:

```text
hi team propose t-7 --approval <id> --notes <description.md>
```

- **Merged work only:** what the people agreed works. Naming any task of
  a line of work finds the merge that brought it into dev. Work that
  brought upstream in has nothing of the team's to offer, and is refused;
  so is work proposed already, with its link.
- **The change alone.** hi takes the merge's diff, leaving out the team's
  own files (`docs/specs/`, `.hi5/`, `AGENTS.md`, `docs/map.md`,
  `docs/user/`, `docs/tech/`, `docs/decisions/`) unless upstream has them
  too, and applies it (`git apply --3way`) to upstream's latest, fetched
  first, in a worktree of its own that it removes after. When it doesn't
  apply, hi says to bring upstream in first.
- **A draft, from a fork.** The commit is the task's title, by the
  machine's own git identity. `gh repo fork` makes a fork under the
  machine's GitHub account, or finds it; hi pushes the commit to a branch
  `hi-team-<team>-<task>` there with `gh`'s sign-in, from the host, trying
  again while a new fork is being made, and opens a draft pull request
  against upstream's branch with the lead's description and a line that
  agents made the change and a person marks it ready. The task records
  the link and the owner's yes.

Nothing reaches the project without an owner's yes in the channel, and
nothing is marked ready by hi: a person sends it.

## The people's view

- **The people's language.** The lead says "the project we started from"
  or its name, never "upstream" or "fork". "We started from
  pocket-kanban's v2.3; there's a v2.4 with a calendar view. Want it?"
- **Going back.** "Put it back the way it came" is `hi team discard dev`
  to v0, with an approval, as today.
- **Credit.** The user docs the survey writes end with a line that says
  where the app came from and its licence, so `/docs/user/` says it.

## Security

Adopted code is less known than the team's own, and was written by
strangers who may have written it for agents to read. The rule stays the
team's: nothing the code says can widen what a box may reach. In
particular:

- **No project code on the host.** The fetch runs none (above). hi's
  other host-side reads of the project (the dependency review, the run
  command) parse files and never run them, as today.
- **The project's agent settings are off.** Members run Claude Code
  without the project's own settings: hooks, MCP servers, permissions,
  and plugins in `.claude/` (`--setting-sources user`, through `hi agent
  --no-project-settings`, which the broker passes for every task and
  review of an adopted team). Codex and Hermes members are as on any
  team: hi has no switch for their project settings. Someone at the
  machine can turn them on (`"project_agent_settings": true` in
  `team.json`) once an owner knows what they do; the terminal at
  adoption names the files.
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
  It never decides: with no licence, one hi doesn't know, or one that asks
  for the source to be shared when the app is offered over a network
  (AGPL), hi says so at adoption and in the first release's ask, and goes
  on; the lead's skill tells it to remind the owners before the app
  reaches people outside the team.

## Later

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
- **A playground team** whose project can be swapped, for trying
  projects out without a Slack app each (Decisions, 1).
- **Codex's and Hermes' project settings.** Members other than Claude
  Code still load what a project sets up for them; each needs a switch
  of its own.

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

## Slices

1. **v0.90.0.** `hi team adopt <name> <source>` and step 7's adoption:
   the fetch with nothing run, the summary at the terminal, the
   `upstream` remote that can't push, tags under `upstream/`, `adopted`
   in `team.json`, v0, and `--github` for a private copy. The team's
   rules in `AGENTS.md`, `docs/specs/README.md`, and the project's agent
   settings off for the members. The survey, `hi-run`, and the lead's
   introduction.
2. **v0.91.0.** `hi team upstream`, the daily fetch and the lead woken
   for a new version, and `--upstream` tasks.
3. **v0.92.0.** `hi team propose`.

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
10. A task with `--upstream=<tag>` starts by merging it, needs a yes, and
    its review checks the merge's own changes; `team.json` records the
    merge.
11. `hi team propose` opens a draft pull request with the change alone on
    upstream's latest, only on an owner's yes, and leaves no worktree.
