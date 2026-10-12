---
title: Run a team in Slack
description: Set up a hi team, a virtual team of agents in one Slack channel. A Hermes lead talks with the people in plain language and keeps the project's memory; a coder and a reviewer do the work in boxes, and the lead merges what the reviewer approves.
---

A hi team is a virtual team of agents that lives in one Slack channel and
builds one project for the people in it. The people may not read code: they
say what they want in plain language, try what the team makes, and say
whether it works.

- **The lead** is Hermes Agent, from Nous Research. It
  talks in the channel, agrees with the people what will change and how
  they'll know it works, writes the specs, and keeps the project's memory.
  It doesn't write code.
- **The members** do the work, one task at a time, each in a
  [box](/guide/box/) of its own on a branch: by default a coder (Claude
  Code) and a reviewer (Codex), so the code is checked by an agent other
  than its author.
- **The lead merges** a task only after the reviewer approves its latest
  commit.

One team has one channel, one Slack app, and one machine. A new project is a
new team.

The lead and the members can reach any website, so the lead can read the
links people give it and the members can install what they need. Their
boxes are the boundary: they hold no keys or sign-ins (hi's proxy adds the
model keys on the way out), and they see only the team's folder. To narrow
a member, give it a `network` in `team.json` (see below).

## Before you start

On the machine that will run the team:

```sh
hi install hermes claude codex podman
```

Hermes needs an OpenRouter key (`hermes setup`), Claude Code and Codex need
to be signed in, and `podman` (or Docker) runs the boxes. The keys stay on
the machine: the boxes reach the models through hi's proxy, which puts the
keys in.

## Set up a team

```sh
hi team new payments
```

The setup asks six things, one at a time:

1. **Purpose.** One sentence; the lead starts from it.
2. **Slack app.** hi prints a manifest named after the team
   (`payments`). In Slack, create a new app from it, install it to the
   workspace, and paste its bot token (`xoxb-…`, under OAuth & Permissions)
   and an app-level token with `connections:write` (`xapp-…`, under Basic
   Information). Neither shows as you paste it.
3. **Channel.** Invite the app to the channel (`/invite @payments`) and
   paste the channel's ID (`C…`).
4. **People.** The owners' Slack member IDs (`U…`), and who else may ask
   for work; Enter lets everyone in the channel.
5. **Members.** Each role's agent and model; Enter keeps what's offered.
6. **Code.** Enter starts a new git repository; or give one to clone: a
   URL, with this machine's git credentials, or a folder on this machine.
   A new repository can go on GitHub too: give `owner/name`, and hi
   creates a private repository with this machine's `gh` sign-in.

hi checks both tokens and that the app is in the channel before it writes
anything. Without a terminal, give the answers in a file and the tokens in
`HI_TEAM_SLACK_BOT_TOKEN` and `HI_TEAM_SLACK_APP_TOKEN`:

```sh
hi team new payments --from payments.json
```

## Start and stop it

```sh
hi team up payments       # start it now, and at boot
hi team up                # pick the team to start or restart from a list
hi team status payments   # up or down, its members, its tasks
hi team logs payments     # the lead's log
hi team down payments     # stop it; the folder stays
hi team rm payments       # stop it and remove it, folder and all
```

`hi team up` installs a systemd user service, `hi-team-payments`, which
restarts the team if it stops. For it to run while you're logged out and
start at boot, run `sudo loginctl enable-linger $USER` once.

`hi team rm` asks first, then removes everything hi made for the team: the
service, its name in `~/.local/share/hi/teams`, the members' boxes, and the
folder. With `--keep-folder` the folder stays, and `hi team up <folder>`
brings the team back. Deleting just the folder leaves the service and the
name behind. The Slack app and channel, and a GitHub repository, are yours
to remove in Slack and GitHub.

## How the team works

Someone in the channel asks for something. The lead agrees with them what
will change and a short list of things they can try, writes it down in
`docs/specs/`, and briefs the coder. When the coder finishes, the broker has
the reviewer check the work; if it needs changes, the broker gives the coder
the reviewer's notes on the same branch, up to three reviews, without the
lead in between. The lead hears once: approved, or changes still needed.
Once the reviewer approves, the lead merges and tells the people what
changed and how to try it. A feature is done when they agree it works.

A member sees only its brief and the project, so the broker sends what
the people gave along with it: the thread the work was agreed in, in their
words with their links, and the files shared there (screenshots,
mockups, documents), in a read-only folder in the member's box. The lead
attaches anything else, such as a file shared earlier in the channel. A
follow-up and its review get the same files.

Work starts only after someone says yes. The lead proposes the plan and
the list of things to try, and waits; the broker refuses a new task unless
the lead names the message that approved it, a reply from one of the
team's people after the lead asked, in the same thread. Small fixes, such
as a typo, may start without asking, and the channel is told when one
does.

The lead answers only when someone @-mentions it (`@payments`), even
in a thread it's part of, so people can talk among themselves in the
channel. It still follows everything: hi keeps a log of the channel,
threads and shared files included, in the team's `channel/` folder, and
every 15 minutes the lead reads what's new and keeps what matters in its
memory: a decision made in passing, a link or a video someone shared, who
cares about what. `hi team channel payments --since 2h` shows you the log.

The lead's long-term memory is [Hindsight](https://github.com/vectorize-io/hindsight):
a memory server that runs beside the lead, keeps the facts, people, and
decisions from every conversation, and recalls the ones that matter before
the lead answers. It runs in a container of its own on the team's private
network, with its database in the team's `memory/` folder. Its model calls
go through the team's proxy like the lead's, so it holds no keys. The
first `hi team up` downloads its image, about 2.6 GB. To do without it, set
`"memory": "off"` in `team.json`; the lead then keeps Hermes' small
built-in notepad only.

The lead puts members to work with `hi team task`, `review`, `wait`,
`merge`, `release`, and `discard` in its box. These go to a broker that `hi team` runs on the
machine: it checks each request against `team.json`, starts the member as
[`hi agent`](/guide/agent/), runs the review rounds, and refuses a merge that no review approved, and a release that no owner approved.
The lead never holds the agents' keys or the repository's credentials, and
nothing it asks for widens a member's box.

## Try what the team built

After a merge, the lead runs a preview and posts a link in the channel, so
the people can try the work, on their phones too. The link carries its own
key, so there is no password to copy:

1. hi starts the project in an app box of its own, from the merged code (or
   a task's branch, before it is merged), with `make run` or `npm start`.
   The app listens on `$PORT`; its port reaches this machine only.
2. `hi net expose --link` puts it on a temporary HTTPS address through this
   machine's NetBird (see [hi net](/guide/workstation/expose/)), which only
   the link with its key opens. The machine must be enrolled with `hi net`,
   and Peer Expose must be on in NetBird.
3. A preview lasts four hours by default. A new one replaces it.

The lead itself never joins your NetBird network: the machine runs the
app and the address, and the lead only asks for them.

New team repositories tell the coder to give the project a `make run` that
listens on `0.0.0.0` at `$PORT`, so every project starts the same way.

## Dev and production

The project has two versions, and the lead always says which one people
are looking at:

- **Dev** is where every merge goes. The people try it in previews, and
  what doesn't work out can be thrown away.
- **Production** is the version people rely on, on a `production` branch
  of its own. It changes only when an owner says yes. Each release gets the
  next number: v1, v2, and so on, tagged `release-1`, `release-2`.

When the people are happy with dev, the lead asks an owner whether to
release it, and only that owner's reply lets the broker move production
(`hi team release`). If a release goes badly, an owner may ask to put
production back on an earlier version.

Every change to production is posted in the channel itself, not in a
thread: "Production v4 is out (it was v3), approved by Anna", with the
lead's notes on what changed, or "Production is back on v3". When the
people want everything in dev since the last release gone, the lead sets
dev's code back to production's (after someone says yes). That is posted
too, with what was set aside, and it stays in the project's history.

Unmerged ideas are capped at five. Each new task starts one, and its
follow-ups continue it. The broker refuses a sixth until one is merged or
discarded, so ideas can't pile up beyond what anyone can follow in Slack.
`hi team status payments` shows production's version, what dev has on top
of it, and the ideas still open.

## The team's folder

Everything a team knows and has made is in one folder: `team-<name>` in
the folder where you ran `hi team new`, or wherever `--dir` puts it. hi
finds it by name from anywhere, through a link in `~/.local/share/hi/teams`.

| Path | What |
|---|---|
| `team.json` | Purpose, channel, owners, members |
| `secrets.json` | The Slack tokens, readable only by you |
| `lead/` | The lead's Hermes home: `SOUL.md`, its memory, its sessions |
| `repo/` | The project's git repository; `repo/docs/specs/` holds what the lead and the people agreed, one file per feature |
| `tasks/` | Each task's brief and report |
| `channel/` | The channel's log, a file per day, and the files people shared |
| `memory/` | The lead's long-term memory: Hindsight's database |

The containers are rebuilt from the folder on every start, so a copy of the
folder is a copy of the team. To move it, stop the team, move the folder,
and start it by its new folder: `hi team up ~/projects/team-payments`. The
same works for a folder copied from another machine. `secrets.json` is
listed in the folder's `.gitignore`, in case the folder sits inside a
repository. `lead/SOUL.md`
is the lead's identity: edit it to change how the lead works, then
`hi team up payments` to restart it.

## Put the project on GitHub

```sh
hi team github payments imanhabib/payments
```

hi creates a private repository with this machine's GitHub sign-in (`gh`),
or connects an existing empty one, and pushes the project. From then on the
team pushes every merge there. No GitHub credential enters a box: the
pushes run on the machine, so nobody can talk the lead into revealing a
token. A team whose project was cloned from a URL pushes there already.

The specs go with the code: the lead writes them in the project's
`docs/specs/`, and hi commits its changes whenever a task starts and
before a merge, so each member's branch starts with the latest specs. They
are the only commits that skip review: documentation, not code.

## Change a member

Edit `members` in `team.json` and restart the team. A member may have a
`bundle` (see [bundles](/guide/agent/#bundles-skills-and-the-tools-they-need)),
and a `network` to narrow what it reaches: `dev` (code hosts and package
registries, plus any `allow`) or `locked`:

```json
"coder": { "agent": "claude", "model": "opus", "bundle": "web", "network": "dev", "allow": ["cdn.example.com"] }
```

The lead is always Hermes, and the reviewer must be a different agent or
model from the coder.
