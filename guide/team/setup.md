---
title: Set up a team
description: "Install what a hi team needs, answer the setup's nine questions with hi team new, start and stop the team, put its project on GitHub, and change the agents and sign-ins its members stand for."
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

Without a name, `hi team new` asks for it first. At a terminal the
questions look like `hi install`'s: a field at a time, checked as you
answer, and a list where the answer is one of a few. The setup asks nine
things, one at a time:

1. **Lead.** What the lead goes by in Slack: a name of its own, such as
   a person's (`Maja Ström`), or Enter for the team's. Its Slack handle is
   the name in the letters Slack allows (`@maja-strom`); the
   `/hi-payments` command keeps the team's name.
2. **Purpose.** One sentence; the lead starts from it.
3. **Slack app.** hi prints a manifest in YAML named after the lead, and
   offers to copy it to the clipboard (it works over SSH too, in
   terminals that let programs copy). In Slack, create a new app from it
   (From a manifest → YAML), install it to the workspace, and paste its
   bot token (`xoxb-…`, under OAuth & Permissions) and an app-level token
   with `connections:write` (`xapp-…`, under Basic Information). Neither
   shows as you paste it.
4. **Channel.** Invite the app to the channel (`/invite @payments`, or the
   lead's handle) and paste the channel's ID (`C…`).
5. **People.** The owners' Slack member IDs (`U…`): the team's
   developers, who build in the dev box and release from the dev page.
   Everyone in the channel may talk with the lead.
6. **Members.** Each role's agent and model, and who pays for it; Enter
   keeps what's offered. The members decide the team's sign-ins (see
   [Change a member](#change-a-member)).
7. **Models.** An OpenRouter key of the team's own, so its use is billed
   and limited apart; Enter uses this machine's, from Hermes. No box ever
   holds it (see [Keys and settings](/guide/team/keys/)).
8. **Code.** Enter starts a new web app for the team to build on: a
   welcome page on Bun that points people at Feedback, with a
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
   Or name a team template: a web app for a kind of work, built into
   hi, in `HI_TEMPLATES_DIR`, or from your hi server's templates.
   A new repository can go on GitHub too: give `owner/name`, and hi
   creates a private repository with this machine's `gh` sign-in.

9. **Network.** A NetBird setup key: the team's addresses and its dev
   box are on your NetBird network, and only there until an owner makes
   one public (see [Joining NetBird](/guide/team/dev/#joining-netbird)).
   Enter skips it, and `hi team dev <team> netbird` asks again later.
   Then what the addresses are named:
   - **This machine's domain**, when it has one (`hi net domain`):
     `https://payments.dev.example.com/` and the like (see
     [The addresses on https](/guide/team/dev/#the-addresses-on-https)).
   - **A host name your IT team keeps** in its DNS, pointing at the
     team's NetBird address (`hi team dev <team>` shows it once the team
     joined): `http://payments.corp.example/`, the preview on `:8080`, the
     dev page on `:8888`.
   - **The NetBird name**, `http://payments.<your network>/`, on the same
     ports.
   - **A name for each, which your IT team keeps**, for an IT team that
     won't put several ports on one name: production at
     `payments.example.com`, Stage at `stage.payments.example.com`, and
     the dev page at `dev.payments.example.com` (or any names they pick).
     See [A name for each address](/guide/team/dev/#a-name-for-each-address).

   `hi team addresses <team> [domain|netbird|<host>|names ...]` changes it
   later, and restarts the team to move them.

hi checks both tokens, that the app is in the channel, and that the bot
token has every scope the lead needs, before it writes anything. An app
made from an older or another manifest can lack one, such as `files:read`,
and then the lead gets an error on every file people share; hi names the
missing scopes, and adding them under OAuth & Permissions and reinstalling
the app fixes it. `hi team up` checks again and warns, for teams made
before. The app has Slack's Agent experience and its MCP server off: the
lead shows it's working with a reaction on the message. For Slack's
"thinking…" line under the reply box too, turn Agent experience on in the
app's settings and add the `assistant:write` scope; apps made before
v2.6.8 have both, and keep them. Without a terminal, give the answers in
a file and the tokens in
`HI_TEAM_SLACK_BOT_TOKEN` and `HI_TEAM_SLACK_APP_TOKEN` (and the team's
OpenRouter key, if it has one, in `HI_TEAM_OPENROUTER_API_KEY`):

```sh
hi team new payments --from payments.json
```

## Start and stop it

```sh
hi team up payments       # start it now, and at boot
hi team up                # pick the team to start or restart from a list
hi team status payments   # up or down, its members, its versions, backups
hi team logs payments     # the lead's log
hi team down payments     # stop it; the folder stays
hi team rm payments       # stop it and remove it, folder and all
hi team rename payments billing   # give it another name
```

`hi team up` installs a systemd user service, `hi-team-payments`, which
restarts the team if it stops. For it to run while you're logged out and
start at boot, run `sudo loginctl enable-linger $USER` once.

`hi team rm` asks first, then removes everything hi made for the team: the
service, its name in `~/.local/share/hi/teams`, the lead's box, the dev
box, the app's boxes, and the folder. With `--keep-folder` the folder stays, and `hi team up <folder>`
brings the team back. Deleting just the folder leaves the service and the
name behind. The Slack app and channel, and a GitHub repository, are yours
to remove in Slack and GitHub.

`hi team rename` follows a new name for the Slack app: the bot's handle,
the `/hi-<name>` command, the service, and the folder when it is still
`team-<name>`. The team stops while it's done and starts again if it was
up. Its work records, memory, and snapshots carry over: team.json's `made_as`
keeps the name the memory bank and the snapshots are filed under, and a
new team given the old name is filed under `<name>-2`. Then paste
`hi team manifest <new name>` into the app's manifest in Slack. A lead with
a name of its own keeps it, and its handle.

## Put the project on GitHub

```sh
hi team github payments example-org/payments
```

hi creates a private repository with this machine's GitHub sign-in (`gh`),
or connects an existing empty one, and pushes the project. From then on the
team pushes every merge into dev there. No GitHub credential enters a
box: the pushes run on the machine, so nobody can talk an agent into
revealing a token. A team whose project was cloned from a URL of yours pushes there
already; an adopted project never pushes to the one it came from.

The specs go with the code, in the project's `docs/specs/`: the owners'
agents write them before larger work, agreed with the owner, and the lead
reads them to answer people.

## Change a member

The members in `team.json` are the lead, a coder, and a reviewer. From hi
2.4 the coder and the reviewer get no tasks: they decide which sign-ins
the team keeps (Claude, ChatGPT, OpenRouter), which the lead and the dev
box use, and their agents take turns in a
[deep review](/guide/team/reviews/#a-deep-review). The lead is always
Hermes.

```sh
hi team member payments lead --provider chatgpt     # the lead on the team's ChatGPT plan
hi team member payments coder --model opus           # the coder's model
hi team member payments lead --effort high           # how hard the lead thinks
```

`hi team member` restarts a running team; an edit to `members` in
`team.json` takes `hi team up payments`. See
[Keys and settings](/guide/team/keys/) for the sign-ins.
