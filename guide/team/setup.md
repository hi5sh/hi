---
title: Set up a team
description: "Install what a hi team needs, answer the setup's seven questions with hi team new, start and stop the team, put its project on GitHub, and change its members."
---

## Before you start

On the machine that will run the team:

```sh
hi install hermes claude codex podman restic
```

Hermes needs an OpenRouter key (`hermes setup`), Claude Code and Codex need
to be signed in, and `podman` (or Docker) runs the boxes. The keys stay on
the machine: the boxes reach the models through hi's proxy, which puts the
keys in. restic keeps the team's [snapshots](/guide/team/folder/#snapshots-and-restore).

## Set up a team

```sh
hi team new payments
```

The setup asks seven things, one at a time:

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
6. **Models.** An OpenRouter key of the team's own, so its use is billed
   and limited apart; Enter uses this machine's, from Hermes. No box ever
   holds it (see [Keys and settings](/guide/team/keys/)).
7. **Code.** Enter starts a new web app for the team to build on: a
   welcome page on Bun that starts the chat with the lead, with a
   database and tests (the `app` template in
   [Projects](/guide/projects/#templates)).
   Type `empty` for a repository with only a README. Give a project
   someone else wrote, such as `github.com/owner/name`, to adopt it: the
   team works on its own copy and never pushes to theirs (see
   [Start from someone else's project](/guide/team/adopt/), and
   `hi team adopt`, which asks about it first). A repository this
   machine can push to is yours: hi asks whether the team should push
   its merges there. A folder on this machine is cloned, and its owner
   pulls from the team.
   Or name a team template: a web app with roles of its own for a kind of
   work, built into hi, in `HI_TEMPLATES_DIR`, or from your hi server's
   templates.
   A new repository can go on GitHub too: give `owner/name`, and hi
   creates a private repository with this machine's `gh` sign-in.

hi checks both tokens, that the app is in the channel, and that the bot
token has every scope the lead needs, before it writes anything. An app
made from an older or another manifest can lack one, such as `files:read`,
and then the lead gets an error on every file people share; hi names the
missing scopes, and adding them under OAuth & Permissions and reinstalling
the app fixes it. `hi team up` checks again and warns, for teams made
before. Apps made before v0.96.0 lack `assistant:write`, which lets
Slack show that the lead is thinking while it works: paste the manifest
`hi team manifest <name>` prints into the app's App Manifest, Save, and
reinstall. It turns on Slack's agent view, which Slack can't turn off
again. Without a terminal, give the answers in a file and the tokens in
`HI_TEAM_SLACK_BOT_TOKEN` and `HI_TEAM_SLACK_APP_TOKEN` (and the team's
OpenRouter key, if it has one, in `HI_TEAM_OPENROUTER_API_KEY`):

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
hi team rename payments billing   # give it another name
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

`hi team rename` follows a new name for the Slack app: the bot's handle,
the `/hi-<name>` command, the service, and the folder when it is still
`team-<name>`. The team stops while it's done and starts again if it was
up. Its tasks, memory, and snapshots carry over: team.json's `made_as`
keeps the name the memory bank and the snapshots are filed under. Then
paste `hi team manifest <new name>` into the app's manifest in Slack.

## Put the project on GitHub

```sh
hi team github payments imanhabib/payments
```

hi creates a private repository with this machine's GitHub sign-in (`gh`),
or connects an existing empty one, and pushes the project. From then on the
team pushes every merge there. No GitHub credential enters a box: the
pushes run on the machine, so nobody can talk the lead into revealing a
token. A team whose project was cloned from a URL of yours pushes there
already; an adopted project never pushes to the one it came from.

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
model from the coder. How each role works on your team is in its
`roles/<role>/ROLE.md` ([Roles that remember](/guide/team/how-it-works/#roles-that-remember)).
