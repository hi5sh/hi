---
title: Start from a project someone else wrote
description: "hi team adopt makes a team whose project is one someone else wrote, such as one found on GitHub: the team keeps its own copy and never pushes to theirs, a first task makes it run and maps it, and the lead introduces it to the people in their words."
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
A first task, the survey, reads the code, makes it run on a preview, and
maps it; then the lead tells the people what they have:

> I've set up pocket-kanban for you. It's a kanban board for small
> teams. Here it is running: https://kanban-preview.example.net/?hi_key=…
> Try adding a card and dragging it to Done. Two things don't work yet:
> signing in with Google, and file uploads. Want me to fix either?

From there it's a team like any other.

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
                  the members read them as the project's notes; its agent settings stay off
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
- **Size.** The team takes the whole history, so the members can see why
  code is the way it is. Over 1 GB, hi asks first, and offers only the
  latest commit (`--shallow`), which then stays on this machine.

## Where the work goes

The team's copy is `repo/` in its folder, as for any team. The project it
came from is the remote `upstream`, read-only: its push address is one
git can't use, so neither hi nor anyone at the machine pushes to it by
mistake. Its tags are under `upstream/` (`upstream/v2.3`), so the team's
own `release-1`, `release-2` never collide with them.

`--github owner/name` keeps the team's copy in a new private repository
too, with the project's history, and the team pushes every merge there.
It isn't a GitHub fork: a fork of a public repository can't be private.
`hi team github` does the same later.

`--ref` starts from a branch, a tag, or a commit; upstream's default
branch otherwise. A branch becomes the team's branch, under its own name.

## v0, the project as it came

The first commit on the team's branch adds the team's rules and changes
nothing of the project's: its README, licence, and history stay as they
are.

- `AGENTS.md` gets the team's rules at the top, with the project's own
  text below them under "The project's own notes". Its other notes for
  agents, such as `CLAUDE.md`, stay; the team's rules say they describe
  the code, and that the rules and the brief win where they conflict.
- `docs/specs/` for the lead, and `.hi5/adopted.json`, which says where
  the project came from.

That commit is v0, tagged `release-0`. Before the first release, `hi team
discard dev` puts dev back to it. Production's first release is v1, as
for any team.

## The survey

When the team first starts, hi has the coder survey the project, with
your yes at the terminal as its go-ahead, and says so in the channel:

1. **Run it**: install what it needs in its box, and start it on `$PORT`
   for the preview, with a `make run` target, or `make hi-run` when the
   project's own `run` target means something else (hi uses `hi-run`
   first). Data it keeps goes in `$HI_DATA_DIR` when that's a setting.
2. **Test it**: run its own tests and say which pass.
3. **Map it**: `docs/map.md`, the first user docs, and a drawing of its
   parts, so [the app's docs](/guide/team/docs/) explain it from day one.
4. **Say what's missing**: settings it needs, such as an API key, services
   it expects beside it, and what doesn't work yet.

The reviewer checks it like any work. When it's done, hi wakes the lead,
which merges it, puts dev on the preview, and introduces the project
under hi's message in the channel. Settings the app needs are added on
the machine, with `hi team env`, never posted in Slack.

When the coder is Claude Code, the survey may spend at most $10.
`--no-survey` skips it: the lead then starts from the project as it is.

## Its own agent settings stay off

The project's authors may have written files for agents to read or load:
`CLAUDE.md`, `.claude/` with hooks and MCP servers, `.cursorrules`. The
members read the notes as the project's, and run Claude Code without its
hooks, MCP servers, or permissions (`hi agent --no-project-settings`).
Members and the app's boxes are limited the usual way: no credentials,
only the team's folder, and the proxy in between. To let members load the
project's settings, once an owner knows what they do, set
`"project_agent_settings": true` in `team.json`.

## Keeping up with the project it came from

The project's authors keep working on it. hi fetches it from this machine
once a day, running nothing from it, so the members' copies always have
its latest branches and tags.

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
words, what's new that they'd notice and whether they want it:

> pocket-kanban has a new version with a calendar view, and a fix for
> cards that vanished when two people moved them at once. Want it?

Bringing it in is work like any other, and waits for a yes. The lead
starts it with `--upstream=v2.4` (or `--upstream` for the latest on its
branch): the coder merges it into a branch of its own, resolves what
conflicts with the team's work, and runs the tests. The reviewer checks
what the merge itself changed, how each conflict was resolved and the
packages it changes, not the authors' own commits line by line. Once it's
merged, `hi team upstream` lists it.

## From a file

Without a terminal, give the answers in a file, as for `hi team new`, with
`"adopt"` for the project, and `"ref"`, `"shallow"`, and `"no_survey"` if
you need them. A `"clone"` address this machine can't push to is adopted
too, unless `"push_to_source": true` says it's yours to push to.

```sh
hi team adopt kanban github.com/ada/pocket-kanban --from kanban.json
```
