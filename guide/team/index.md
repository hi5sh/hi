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
   (`team-payments`). In Slack, create a new app from it, install it to the
   workspace, and paste its bot token (`xoxb-…`, under OAuth & Permissions)
   and an app-level token with `connections:write` (`xapp-…`, under Basic
   Information). Neither shows as you paste it.
3. **Channel.** Invite the app to the channel (`/invite @team-payments`) and
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
hi team status payments   # up or down, its members, its tasks
hi team logs payments     # the lead's log
hi team down payments     # stop it; the folder stays
```

`hi team up` installs a systemd user service, `hi-team-payments`, which
restarts the team if it stops. For it to run while you're logged out and
start at boot, run `sudo loginctl enable-linger $USER` once.

## How the team works

Someone in the channel asks for something. The lead agrees with them what
will change and a short list of things they can try, writes it down in
`docs/specs/`, and briefs the coder. The reviewer checks the coder's work; if it
needs changes, the coder gets a new task on the same branch. Once the
reviewer approves, the lead merges and tells the people what changed and
how to try it. A feature is done when they agree it works.

The lead answers only when someone @-mentions it (`@team-payments`), even
in a thread it's part of, so people can talk among themselves in the
channel. It still follows everything: hi keeps a log of the channel,
threads and shared files included, in the team's `channel/` folder, and
every 15 minutes the lead reads what's new and keeps what matters in its
memory: a decision made in passing, a link or a video someone shared, who
cares about what. `hi team channel payments --since 2h` shows you the log.

The lead puts members to work with `hi team task`, `review`, `wait`, and
`merge` in its box. These go to a broker that `hi team` runs on the
machine: it checks each request against `team.json`, starts the member as
[`hi agent`](/guide/agent/), and refuses a merge that no review approved.
The lead never holds the agents' keys or the repository's credentials, and
nothing it asks for widens a member's box.

## Try what the team built

After a merge, the lead runs a preview and posts its address and a password
in the channel, so the people can try the work, on their phones too:

1. hi starts the project in an app box of its own, from the merged code (or
   a task's branch, before it is merged), with `make run` or `npm start`.
   The app listens on `$PORT`; its port reaches this machine only.
2. `hi net expose` puts it on a temporary HTTPS address behind a password,
   through this machine's NetBird (see [hi net](/guide/workstation/)). The
   machine must be enrolled with `hi net`, and Peer Expose must be on in
   NetBird.
3. A preview lasts four hours by default. A new one replaces it.

The lead itself never joins your NetBird network: the machine runs the
app and the address, and the lead only asks for them.

New team repositories tell the coder to give the project a `make run` that
listens on `0.0.0.0` at `$PORT`, so every project starts the same way.

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
