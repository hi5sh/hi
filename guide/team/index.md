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
hi install hermes claude codex podman restic
```

Hermes needs an OpenRouter key (`hermes setup`), Claude Code and Codex need
to be signed in, and `podman` (or Docker) runs the boxes. The keys stay on
the machine: the boxes reach the models through hi's proxy, which puts the
keys in. restic keeps the team's [snapshots](#snapshots-and-restore).

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
   holds it (see [Keys and settings](#keys-and-settings)).
7. **Code.** Enter starts a new git repository; or give one to clone: a
   URL, with this machine's git credentials, or a folder on this machine.
   A new repository can go on GitHub too: give `owner/name`, and hi
   creates a private repository with this machine's `gh` sign-in.

hi checks both tokens, that the app is in the channel, and that the bot
token has every scope the lead needs, before it writes anything. An app
made from an older or another manifest can lack one, such as `files:read`,
and then the lead gets an error on every file people share; hi names the
missing scopes, and adding them under OAuth & Permissions and reinstalling
the app fixes it. `hi team up` checks again and warns, for teams made
before. Without a terminal, give the answers in a file and the tokens in
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

After a merge, the lead shows the new version on the team's preview link,
so the people can try the work, on their phones too. The link carries its
own key, so there is no password to copy:

1. hi starts the project in an app box of its own, from the merged code (or
   a task's branch, before it is merged), with `make run`, or the
   `start` script: `bun run start` with a `bun.lock`, else `npm start`.
   The app listens on `$PORT`; its port reaches this machine only.
2. `hi net expose --link` puts it on a temporary HTTPS address through this
   machine's NetBird (see [hi net](/guide/workstation/expose/)), which only
   the link with its key opens. The machine must be enrolled with `hi net`,
   and Peer Expose must be on in NetBird.
3. The link stays the same from one preview to the next: the new version
   starts beside the old one, and the address moves to it once it
   answers, so the people just reload. A link lasts a day; a preview gets
   a new link only when the old one has less than 12 hours left. If the
   new version doesn't start, the link keeps showing the old one.

Production has a link of its own, which shows the latest release. hi
renews it before it runs out each day, and keeps one message in the
channel with production's current link, edited whenever the link or the
version changes. Each release's announcement links to that message, so
nobody has to hunt for the latest link.

The app box holds no credentials: no Slack token and no API keys. The app
finds its own link in the file named by `$HI_PUBLIC_URL_FILE`, which hi
rewrites whenever the link changes. Anything the people want posted on a
schedule, such as a morning digest, is the lead's scheduled job: a script
that makes the message from production's code, and the lead posts what it
prints.

### Help, feedback, and the lead in the app

Every page of the preview and of production has a round button in the
corner. What it opens depends on who you are.

**Anyone with the app's link** gets help and feedback:

- **Ask** how something works, and get an answer from the app's guide,
  `HELP.md`, which the lead keeps current with each change. The answer
  comes from hi, not the lead, through a model with no tools: it only
  explains, so there is nothing to talk it into. Up to 60 questions a day
  per address, 400 for the team.
- **Feedback** goes to the team's channel as a message of its own,
  "Feedback f-12", with what you pointed at, your files, and a picture of
  the page as you see it (untick it to leave it out). Your name is
  optional, and marked as not checked. Nothing changes until someone in
  the channel asks the lead to "do f-12", and that needs a yes as any work
  does.

**People who may ask the team for work** type the team's command in the
channel, `/hi-<team>`, such as `/hi-payments`. hi
sends them a private link in a DM, and with it the button opens a chat
with the lead, as them, checked through Slack:

- The link's key never reaches the app: hi's relay keeps it in a cookie
  only `/__hi/` gets, so neither the app's server nor its scripts see it.
  It works for 14 days; the command again gives a new link and ends the
  old one.
- When an address changes, hi updates the link in the DM.
- Each team's command is its own: Slack gives a command that two apps in
  a workspace share to the one installed last. Type it in the team's
  channel, not in a DM; the lead ignores DMs.
- The Slack app needs the `/hi-<team>` command and the `commands` and `im:write`
  scopes. `hi team manifest <name>` prints the manifest that has them;
  `hi team up` says when they're missing. Until then, ask the lead in the
  channel to send you your link.

In the chat with the lead:

- The lead answers as it does in Slack, with the same memory, and knows
  which page you are on and which version you see.
- **Point at what you mean.** The target button beside the message box,
  or `Alt+S` anywhere on the page, lets you click part of the page: it is outlined as you
  hover, `Alt+↑` widens it to the element around it, and a click adds it to
  your message as a chip, `#1 span "Paid"`. Shift-click adds more. The app
  ignores those clicks. The lead gets each element's text, classes, and
  HTML, and the coder finds it in the code from them. On a phone, tap it,
  then "Use this". A picture of each part you point at goes with it, so
  the lead sees what you saw.
- **Show it.** Paste a screenshot into the message box, drop files on the
  chat, or use the paperclip: up to 6 files a message. The lead sees the
  pictures, and the coder gets the files with the work.
- **The lead comes back.** When work you asked for is ready to try, the
  lead says so in the same chat. If the chat is closed, the button gets a
  dot and a preview of the message.
- hi copies each chat into a thread in the channel, files included, so the
  team sees it.
- **The lead comes back** in your DM too, when it posts in your chat and
  you don't have the app open.
- A "yes" in the chat starts the work the lead proposed there, as one in
  Slack does. A release, a rollback, or turning production's chat off
  still needs an owner's yes in Slack, where the team sees it.

The app holds none of it. hi adds the button on the way to the browser, in
the relay in front of the team's links, so it works whatever the app is
written in, and `/__hi/` on those links is hi's. To take it out of
production, an owner asks the lead in Slack, or on the machine:

```sh
hi team helper payments                    # where it is on
hi team helper payments production off     # from the next page load; on brings it back
```

A link made before your hi had the helper gets it when the link is next
renewed.

### Keys and settings

Nobody pastes a key into Slack: the lead puts what it is given into
briefs, and the coder into the code, where every member and every copy of
the project can read it. Keys go to the team on the machine instead.

**Models.** The app calls OpenRouter's API at `$OPENROUTER_BASE_URL` with
`$OPENROUTER_API_KEY`, in previews and production. That key is a
placeholder: the call goes through a socket to hi's team service on the
machine, which puts the team's real key on it. The lead's own model calls
take the same key, through its proxy. Give the team a key of its own, or
change it, at any time; it applies to the next call:

```sh
hi team key payments openrouter            # asked for; it shows as *
hi team key payments openrouter --unset    # this machine's key again
```

**Slack.** When Slack gives the app a new token (after a reinstall, or a
token you revoked), give it to the team; it asks for the bot token and
the app token, Enter keeps one, and a running team restarts with them:

```sh
hi team key payments slack
```

**Settings.** Other keys and settings the app needs reach its boxes as
environment variables, and nothing else: not the lead, not the members,
not briefs. The lead sees their names, so it can tell the coder which
variable to read.

```sh
hi team env payments WEATHER_API_KEY=…     # or WEATHER_API_KEY alone, to be asked
hi team env payments                       # their names
hi team env payments --unset WEATHER_API_KEY
```

The next preview has a change, and hi starts production again on the same
link within a minute. A setting is readable by the app's code, which the
agents write; only the OpenRouter key never is.

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
  of its own, with a link of its own. It changes only when an owner says
  yes. Each release gets the next number: v1, v2, and so on, tagged
  `release-1`, `release-2`.

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
| `app/` | Production's data: what the app keeps in `$HI_DATA_DIR` |

The containers are rebuilt from the folder on every start, so a copy of the
folder is a copy of the team. To move it, stop the team, move the folder,
and start it by its new folder: `hi team up ~/projects/team-payments`. The
same works for a folder copied from another machine. `secrets.json` is
listed in the folder's `.gitignore`, in case the folder sits inside a
repository. `lead/SOUL.md`
is the lead's identity: edit it to change how the lead works, then
`hi team up payments` to restart it.

## Snapshots and restore

The code is in git, but the lead's memory, the tasks, the channel's log,
and the app's data are only in the team's folder. hi snapshots them:

- **every day**, around 03:00, while the team is up;
- **before every release**, and before production goes back to an earlier
  one, so each version has the data and memory as they were when it went
  out;
- **when you ask**: `hi team snapshot payments`.

The team keeps running. hi waits until no merge or release is half done,
pauses the lead, its memory, and production for about a second while
restic copies the folder, and lets them go. A snapshot of an idle team adds
a few kilobytes: restic stores only what changed, encrypted.

```sh
hi team snapshots payments
```

```text
ID        TAKEN             WHY          SIZE      NEW
cfd8fa7c  2026-10-13 03:12  daily        226.2 MB  208.6 MB
2938aa9e  2026-10-14 03:40  daily        226.2 MB  30.1 KB
5d1e0b77  2026-10-14 11:02  before v4    226.3 MB  41.9 KB
```

To put the team back as it was, name a snapshot by its ID, `latest`, the
version it was taken before (`v4`), or a day (`2026-10-13`, its last):

```sh
hi team restore payments v4
```

hi says what it puts back and asks first. It stops the team, takes a
snapshot of how things are now, puts back the lead's memory, the tasks, the
channel's log, and production's data, and starts the team again. The
channel hears what happened, with the snapshot that undoes it. The code
stays as it is unless you add `--code`, and so do the team's settings
(`team.json`, `secrets.json`).

Snapshots live in `~/.local/share/hi/team-snapshots/<name>/`, outside the
team's folder, with the key they are encrypted with. They stay when the
team is removed. hi keeps the last 7 days, 4 weeks, and 6 months, every
snapshot from the last week, and every snapshot taken before a release.
When a snapshot fails, the owners hear about it in the channel, and
`hi team status` says so until one works.

**Rollback.** When a release goes wrong, production can go back to an
earlier version two ways:

- `hi team release v3` (the lead, once an owner says yes) puts back v3's
  code and keeps production's data as it is. That's right when the new
  version is merely wrong.
- `hi team rollback v3` puts back v3's code *and* production's data as v3
  left it, from the snapshot taken when production moved on. That's for a
  release that damaged the data. What was entered since is set aside in a
  snapshot, and rolling forward the same way puts it back.

The lead proposes a rollback in the channel and says what will be set
aside; only an owner's yes carries it out. On the machine, an owner runs it
directly:

```sh
hi team rollback payments v3          # asks first; --code-only keeps the data
```

Neither touches the lead's memory, the tasks, or the channel's log; that
is `hi team restore`.

**Offsite.** A copy on the same disk doesn't survive the disk. Give the
team an S3-compatible bucket (Hetzner, Cloudflare R2, Backblaze B2, AWS,
and others), and every snapshot is copied there too:

```sh
hi team backups payments --offsite s3:https://fsn1.your-objectstorage.com/acme-backups/payments
```

hi asks for the bucket's access key without echoing it, copies the
snapshots taken so far, and copies each new one in the background. Add
`--region` for a bucket outside `us-east-1`, and `--keep 14,8,12` to keep
more daily, weekly, and monthly snapshots. The bucket can only be read with
the snapshots' key, so keep a copy of it somewhere other than this machine:

```sh
hi team backups payments --show-key
```

**The app's data.** An app box keeps what it must not lose in
`$HI_DATA_DIR` (`/data`). Production's is the team's `app/` folder, which
outlives every release and is in every snapshot. Each preview has a data
folder of its own, apart from production's. The app's own folder is a
fresh copy of the code at each release, so anything written there is
lost. The lead and the coder are told to keep data in `$HI_DATA_DIR`.

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
