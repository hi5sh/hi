---
title: Set up an app
description: "Install what a hi app needs, answer the setup's ten questions with hi app new, start and stop the app, put its project on GitHub, and change the agents and sign-ins its members stand for."
---

## Before you start

On the machine that will run the app:

```sh
hi install hermes claude codex podman restic
```

Hermes needs an OpenRouter key (`hermes setup`), Claude Code and Codex need
to be signed in, and `podman` (or Docker) runs the boxes. The keys stay on
the machine: the boxes reach the models through hi's proxy, which puts the
keys in. restic keeps the app's [snapshots](/guide/app/folder/#snapshots-and-restore).

## Set up an app

```sh
hi app new payments
```

Without a name, `hi app new` asks for it first. At a terminal the
questions look like `hi install`'s: a field at a time, checked as you
answer, and a list where the answer is one of a few. The setup asks ten
things, one at a time:

1. **Lead.** What the lead goes by in Slack: a name of its own, such as
   a person's (`Maja Ström`), or Enter for the app's. Its Slack handle is
   the name in the letters Slack allows (`@maja-strom`); the
   `/hi-payments` command keeps the app's name.
2. **Purpose.** One sentence; the lead starts from it.
3. **Slack app.** hi prints a manifest in YAML named after the lead, and
   offers to copy it to the clipboard (it works over SSH too, in
   terminals that let programs copy). In Slack, create a new app from it
   (From a manifest → YAML), install it to the workspace, and paste its
   bot token (`xoxb-…`, under OAuth & Permissions) and an app-level token
   with `connections:write` (`xapp-…`, under Basic Information). Neither
   shows as you paste it.
4. **Channel.** Invite the app to the channel (`/invite @payments`, or the
   lead's handle) and paste the channel's ID (`C…`). Then, optionally, a
   second channel for operations and security: incidents, warnings,
   downtime, advisories, keys, access, and the dev box's site asks, which
   need technical know-how. Make it private, for owners and Pro Devs, and
   invite the app there too. Without it, all of that goes to the main
   channel. `hi app ops <app> <channel ID>` adds it later, and
   `hi app ops <app> off` takes it away.
5. **People.** The owners' Slack member IDs (`U…`): the app's
   developers, who build in the dev box and release from the dev page.
   Everyone in the channel may talk with the lead.
6. **Members.** Each role's agent and model, who pays for it, and how
   hard it thinks (its effort); Enter keeps what's offered. The members decide the app's sign-ins (see
   [Change a member](#change-a-member)).
7. **Models.** An OpenRouter key of the app's own, so its use is billed
   and limited apart; Enter uses this machine's, from Hermes. No box ever
   holds it (see [Keys and settings](/guide/app/keys/)).
8. **Code.** Enter starts a new web app to build on: a
   welcome page on Bun that points people at Feedback, with a
   database and tests (the `webapp` template in
   [Projects](/guide/projects/#templates)).
   Type `empty` for a repository with only a README. Give a project
   someone else wrote, such as `github.com/owner/name`, to adopt it: the
   app works on its own copy and never pushes to theirs (see
   [Start from someone else's project](/guide/app/adopt/), and
   `hi app adopt`, which asks about it first). A repository this
   machine can push to is yours: hi asks whether the app should push
   its merges there. A folder on this machine is cloned, and its owner
   pulls from the app's copy.
   Or name an app template: a web app for a kind of work, built into
   hi, in `HI_TEMPLATES_DIR`, or from your org server's templates.
   A new repository can go on GitHub too: give `owner/name`, and hi
   creates a private repository with this machine's `gh` sign-in.

9. **Network.** A NetBird setup key: the app's addresses and its dev
   box are on your NetBird network, and only there until an owner makes
   one public (see [Joining NetBird](/guide/app/dev/#joining-netbird)).
   Enter skips it, and `hi app dev <app> netbird` asks again later.
   Then what the addresses are named:
   - **This machine's domain**, when it has one (`hi net domain`):
     `https://payments.dev.example.com/` and the like (see
     [The addresses on https](/guide/app/dev/#the-addresses-on-https)).
   - **A host name your IT team keeps** in its DNS, pointing at the
     app's NetBird address (`hi app dev <app>` shows it once the app
     joined): `http://payments.corp.example/`, Stage on `:8080`, the
     dev page on `:8888`.
   - **The NetBird name**: the name NetBird gives the app's dev box when
     it joins, on the same ports. The setup key decides which NetBird
     network that is, so hi shows the name only once it has joined, and
     follows it if an admin renames the peer.
   - **A name for each, which your IT team keeps**, for an IT team that
     won't put several ports on one name: production at
     `payments.example.com`, Stage at `stage.payments.example.com`, and
     the dev page at `dev.payments.example.com` (or any names they pick).
     See [A name for each address](/guide/app/dev/#a-name-for-each-address).

   `hi app addresses <app> [domain|netbird|<host>|names ...]` changes it
   later, and restarts the app to move them.
10. **Heartbeats.** Optional: so someone hears when the app, or the
    machine, stops. hi sends a GET to a URL at an interval, and an outside
    service such as [healthchecks.io](https://healthchecks.io) (free for
    20 checks, or self-hosted), Better Stack, Cronitor, or Uptime Kuma
    alerts when they stop. Make a check there with the same period, and
    set there who it alerts, by email, SMS, or into the ops channel
    through its own Slack integration, which works when the app's Slack
    app is what stopped. First the app's (every minute unless you say,
    from 30 seconds to an hour), then, when the machine has none, the
    machine's, which says the machine is up whichever apps run.
    `hi app heartbeat <app> <url> [--every 1m]` and
    `hi heartbeat <url> [--every 5m]` set them later; `off` stops one,
    `test` sends one now. Only the URL's host is ever shown: the URL is
    the secret.

hi checks both tokens, that the app is in the channel, and that the bot
token has every scope the lead needs, before it writes anything. An app
made from an older or another manifest can lack one, such as `files:read`,
and then the lead gets an error on every file people share; hi names the
missing scopes, and adding them under OAuth & Permissions and reinstalling
the app fixes it. `hi app up` checks again and warns, for apps made
before. The app has Slack's Agent experience and its MCP server off: the
lead shows it's working with a reaction on the message. For Slack's
"thinking…" line under the reply box too, turn Agent experience on in the
app's settings and add the `assistant:write` scope. Without a terminal, give the answers in
a file and the tokens in
`HI_APP_SLACK_BOT_TOKEN` and `HI_APP_SLACK_APP_TOKEN` (and the app's
OpenRouter key, if it has one, in `HI_APP_OPENROUTER_API_KEY`):

```sh
hi app new payments --from payments.json
```

## Start and stop it

```sh
hi app up payments       # start it now, and at boot
hi app up                # pick the app to start or restart from a list
hi app status payments   # up or down, its members, its versions, backups
hi app logs payments     # the lead's log
hi app down payments     # stop it; the folder stays
hi app rm payments       # stop it and remove it, folder and all
hi app rename payments billing   # give it another name
```

`hi app up` installs a systemd user service, `hi-app-payments`, which
restarts the app if it stops. For it to run while you're logged out and
start at boot, run `sudo loginctl enable-linger $USER` once.

[The admin page](/guide/app/admin/) does the same from a browser, for
every app on the machine: `hi admin start` prints its link.

An app made before hi v2.6.67 keeps running after `hi update`, under its
old names. Its next `hi app up <name>` moves it to the new ones: the
service, the boxes, the folder (`app-<name>`), and `app.json`. Stage's,
production's, and the dev page's addresses change then, unless they are on
a domain of yours; the lead posts the new links, and the links' keys stay.

`hi app rm` asks first, then removes everything hi made for the app: the
service, its name in `~/.local/share/hi/apps`, the lead's box, the dev
box, the app's boxes, and the folder. With `--keep-folder` the folder stays, and `hi app up <folder>`
brings the app back. Deleting just the folder leaves the service and the
name behind. The Slack app and channel, and a GitHub repository, are yours
to remove in Slack and GitHub.

`hi app rename` follows a new name for the Slack app: the bot's handle,
the `/hi-<name>` command, the service, and the folder when it is still
`app-<name>`. The app stops while it's done and starts again if it was
up. Its work records, memory, and snapshots carry over: app.json's `made_as`
keeps the name the memory bank and the snapshots are filed under, and a
new app given the old name is filed under `<name>-2`. Then paste
`hi app manifest <new name>` into the app's manifest in Slack. A lead with
a name of its own keeps it, and its handle.

## Put the project on GitHub

```sh
hi app github payments example-org/payments
```

hi creates a private repository with this machine's GitHub sign-in (`gh`),
or connects an existing empty one, and pushes the project. From then on the
app pushes every merge into dev there. No GitHub credential enters a
box: the pushes run on the machine, so nobody can talk an agent into
revealing a token. An app whose project was cloned from a URL of yours pushes there
already; an adopted project never pushes to the one it came from.

### Or on GitLab

```sh
hi app gitlab payments example-group/payments            # gitlab.com
hi app gitlab payments gitlab.example.com/team/payments  # a GitLab of your own
```

The same, with this machine's GitLab sign-in (`glab`; `hi install glab`,
then `glab auth login`, with `--hostname` for your own GitLab): a new
private project, or an existing empty one. The app's repository signs in
through glab for that host only. Its tasks are then GitLab issues. In
`hi app new`, and on the [admin page](/guide/app/admin/), the Code step
asks for GitHub or GitLab; in a file of answers, it's `"gitlab"`.

The specs go with the code, in the project's `docs/specs/`: the owners'
agents write them before larger work, agreed with the owner, and the lead
reads them to answer people.

## Change a member

The members in `app.json` are the lead, a coder, and a reviewer. The
coder and the reviewer get no tasks: they decide which sign-ins
the app keeps (Claude, ChatGPT, OpenRouter), which the lead and the dev
box use, and their agents take turns in a
[deep review](/guide/app/reviews/#a-deep-review). The lead is always
Hermes.

```sh
hi app member payments lead --provider chatgpt     # the lead on the app's ChatGPT plan
hi app member payments coder --model opus           # the coder's model
hi app member payments lead --effort high           # how hard the lead thinks
```

`hi app member` restarts a running app; an edit to `members` in
`app.json` takes `hi app up payments`. See
[Keys and settings](/guide/app/keys/) for the sign-ins.
