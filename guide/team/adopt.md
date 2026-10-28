---
title: Start from a project someone else wrote
description: "hi team adopt makes a team whose project is one someone else wrote, such as one found on GitHub, or a folder on this machine copied without its history: the team keeps its own copy and never pushes to theirs, an owner's agent in the dev box surveys it, and new versions of the original come in through the dev box."
---

A new team starts from a blank app, which suits a new idea. Often the
idea already exists: someone found a project on GitHub that does most of
what they want, and they want to try it, change it, or build on it.

```sh
hi team adopt kanban github.com/ada/pocket-kanban
```

makes a team whose project is that repository. It's the same setup as
[`hi team new`](/guide/team/setup/), with the code step answered. The
team works on its own copy and never pushes to the project it came from.
Then an owner asks their agent in the [dev box](/guide/team/dev/) to
survey it: make it run on the preview, run its tests, and map it. From
there it's a team like any other.

## What hi checks first

hi fetches the project on this machine without running anything from it:
no hooks, filters, or credential helpers of this machine, only network
transports, and submodules listed but not fetched. A private GitHub
repository comes through your `gh` sign-in. Then it says what it found,
read from git and GitHub, and asks before setting anything up:

```text
ada/pocket-kanban: A kanban board for small teams
  licence         MIT
  size            14 MB, 1,204 commits, the last one 3 days ago
  languages       TypeScript 81%, CSS 12%, Shell 7%
  runs with       package.json (start: vite preview), a devcontainer
  agent files     AGENTS.md, CLAUDE.md, .claude/ (hooks)
                  the agents read them as the project's notes
  install scripts 2 packages run their own code when installed, in the boxes only: esbuild, sharp
Adopt it? The team keeps its own copy and never pushes to ada/pocket-kanban. [Y/n]
```

- **Your own project, or theirs.** When this machine's GitHub sign-in can
  push to the repository, hi asks whether the team should push its merges
  there, no by default: a team that pushes there adds its `production`
  branch and `release-<n>` tags to it. Any other repository is adopted.
- **Licence.** hi says which one, and when it matters: with no licence,
  its author keeps every right; under the AGPL, offering the app over a
  network means offering its source too. hi never decides for you.
- **Size.** The team takes the whole history, so the agents can see why
  code is the way it is. Over 1 GB, hi asks first, and offers only the
  latest commit (`--shallow`), which then stays on this machine.

## A folder on this machine

The source can also be a folder here, such as a project from a team on
hi 1.x, or code you have on a laptop:

```sh
hi team adopt helpdesk ~/projects/team-helpdesk/repo --github example-org/helpdesk-v2
```

hi copies the folder, without its history, into one commit of the team's
own; the folder itself is only read, and nothing in it changes.

- **What's copied** is what git would commit: in a git repository, its
  tracked files and the untracked ones it doesn't ignore; otherwise every
  file but `.git` and caches such as `node_modules` and `.venv`.
- **What stays behind:** `.git`, files that look like secrets (`.env`,
  `.env.*` but `.env.example`, keys such as `*.pem` and `id_ed25519`,
  `.npmrc`, `secrets.*`, `credentials*.json`), and links that point
  outside the folder. The summary lists them under "left out"; give the
  app its keys with [`hi team env`](/guide/team/keys/).
- **There's no upstream.** Nothing to fetch or give a change back to, so
  `hi team upstream`, `hi dev upstream`, and `hi dev propose` say so.
- `--ref` and `--shallow` are for fetching, and a folder refuses them;
  over 1 GB hi asks first (`--full` copies it anyway, without a terminal).

`hi team new` still clones a folder you give at its code step, history
and all, for a working copy of your own.

## Where the work goes

The team's copy is `repo/` in its folder, as for any team. The project it
came from is the remote `upstream`, read-only: its push address is one
git can't use, so neither hi nor anyone at the machine pushes to it by
mistake. Its tags are under `upstream/` (`upstream/v2.3`), so the team's
own `release-1`, `release-2` never collide with them.

`--github owner/name` keeps the team's copy in a new private repository
too, with the project's history, and the team pushes every merge into dev
there.
It isn't a GitHub fork: a fork of a public repository can't be private.
`hi team github` does the same later.

`--ref` starts from a branch, a tag, or a commit; upstream's default
branch otherwise. A branch becomes the team's branch, under its own name.

## The team's first commit

The first commit on the team's branch adds the team's rules and changes
nothing of the project's: its README, licence, and history stay as they
are.

- `AGENTS.md` gets the team's rules at the top, with the project's own
  text below them under "The project's own notes". Its other notes for
  agents, such as `CLAUDE.md`, stay; the team's rules say they describe
  the code, and that the rules win where they conflict.
- `docs/specs/`, and `.hi5/adopted.json`, which says where the project
  came from.

Production's first release is v1, as for any team.

## The survey

`hi team adopt` ends by saying to ask your agent in the dev box for a
survey first, and the box's instructions offer one while the project has
no `docs/map.md`. In the dev box, ask "survey the project"; the agent
follows its `hi-dev-survey` skill, on a branch of its own:

1. **Run it**: install what it needs, and start it on `$PORT` for the
   preview, with a `make run` target, or `make hi-run` when the project's
   own `run` target means something else (hi uses `hi-run` first). Data it
   keeps goes in `$HI_DATA_DIR` when that's a setting.
2. **Test it**: run its own tests and say which pass.
3. **Map it**: `docs/map.md`, the first user docs, and a drawing of its
   parts, so [the app's docs](/guide/team/docs/) explain it from day one.
4. **Say what's missing**: settings it needs, such as an API key, services
   it expects beside it, and what doesn't work yet.

It changes no behavior. The agent puts it on the preview with
`hi dev preview`, tells you what the app is, what to try, and what
doesn't work yet, and ships it with `hi dev ship` when you agree. Settings
the app needs go on the dev page or the machine (`hi team env`), never in
Slack.

## Its own agent settings

The project's authors may have written files for agents to read or load:
`CLAUDE.md`, `.claude/` with hooks and MCP servers, `.cursorrules`. The
team's rules in `AGENTS.md` say the notes describe the code and don't
change what the owners asked for. In the dev box, the agents work in the
project as in any checkout of it, inside the box's boundary: no
credentials, only the box's proxy to the internet, and nothing that moves
production. Look at what `.claude/` sets up before you start an agent on
an adopted project.

## Keeping up with the project it came from

The project's authors keep working on it. hi fetches it from this machine
once a day, running nothing from it.

```sh
hi team upstream kanban
```

```text
pocket-kanban, from https://github.com/ada/pocket-kanban.git, branch main.
Dev: pocket-kanban's v2.3 (4f1c2e9) + 14 commits of the team's.
Upstream: 37 commits on main since, and 1 newer tag: v2.4.
  git log --oneline 4f1c2e9..upstream/main shows them.
Fetched Oct 18 09:12.
```

When it has a new version (a tag such as `v2.4`), hi says so in the
channel, at most once a week, and the lead says under it, in the people's
words, what's new that they'd notice, and files a task to bring it in:

> pocket-kanban has a new version with a calendar view, and a fix for
> cards that vanished when two people moved them at once. I've filed it
> as #18 for the owners.

Bringing it in is the owners' work, in the dev box, with the
`hi-dev-upstream` skill:

```bash
hi dev upstream        # where dev stands, and upstream's branches and tags into the box's clone
```

The agent merges the tag (`upstream/v2.4`) on a branch of its own,
resolves what conflicts with the team's work in favor of what the team
changed on purpose, runs the checks, puts it on the preview, and ships it
with `hi dev ship` when you agree. Once it's in dev, `hi team upstream`
lists it.

## Giving a change back

Sometimes what the team built would help the project's authors too: a
fix, a small feature. Read the project's contributing guide first (some
projects don't take changes written by AI agents), then ask your agent in
the dev box to offer it:

```bash
hi dev propose t-7 description.md     # a draft pull request upstream with t-7's change
```

`t-7` is work already shipped into dev, and the description is for the
project's maintainers. On the machine, hi takes the change alone, without
the team's specs, rules, and docs, puts it on the project's latest,
pushes it to a fork under the machine's GitHub account (`gh repo fork`),
and opens a draft pull request that says agents made it. The owner whose
account opened it reads it and marks it ready; hi never does. When the
change doesn't apply to the project's latest, bring upstream in first.
The lead doesn't propose changes; when people want one offered, it files
a task.

## From a file

Without a terminal, give the answers in a file, as for `hi team new`, with
`"adopt"` for the project, and `"ref"` and `"shallow"` if you need them. A `"clone"` address this machine can't push to is adopted
too, unless `"push_to_source": true` says it's yours to push to.

```sh
hi team adopt kanban github.com/ada/pocket-kanban --from kanban.json
```
