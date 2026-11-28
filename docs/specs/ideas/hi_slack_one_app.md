# One Slack app for the server and all its apps

Status: Draft (2026-11-28), for review.

Dependencies: [hi_server.md](../approved/hi_server.md) (the server's Slack
app, Socket Mode, `/hi`), [hi_app.md](../approved/hi_app.md) (one app, one
channel, one Slack app), [hi_app_dm.md](../approved/hi_app_dm.md) (DMs to
the lead), [hi_app_helper_signin.md](../approved/hi_app_helper_signin.md)
(`/hi-<app>`), [hi_app_say.md](../approved/hi_app_say.md) (`$HI_SAY_URL`),
[hi_people.md](../approved/hi_people.md) (Slack IDs per workspace),
[hi_server_apps.md](../approved/hi_server_apps.md) (one server per machine,
Managed mode later), [hi_server_vault.md](../approved/hi_server_vault.md).

## Goal

Today every app has a Slack app of its own. A new app costs about seven
steps at api.slack.com: create the app from the manifest, make the
app-level token, install, copy the bot token, invite the bot to two
channels, copy IDs. A change to the manifest is pasted and reinstalled
once per app. In a workspace where an admin approves each new Slack app,
every hi app also waits for that approval.

- **One Slack app per server, set up once.** The server's Slack app
  becomes the bot of every app on it. A new app takes no step in Slack:
  hi makes or joins its channel.
- **Each lead still feels like its own.** Its messages carry the lead's
  name and picture. People talk to it in its channel and in DMs.
- **No lead reaches another app.** One token for all apps must not let
  one lead read or post in another app's channels or DMs.
- **Hermes stays as it is.** Its Slack adapter, streaming, buttons and
  status keep working, with no fork.

Not in this: several machines' apps on one server (Managed mode,
[hi_server_apps.md](../approved/hi_server_apps.md)). Release 1 needs the
apps on the server's own machine, which is the rule today.

## What there is today

### The server's Slack app

`hi server slack setup` or the admin page (Server → Slack) sets it up
(`slack.go:937-1038`, `admin.js:1979-2055`), with a prefilled
create-app link (`slackManifestURL`, `slack.go:1040-1050`).

- **Manifest** (`slack.go:30-48`): `/hi`, App Home, interactivity, Socket
  Mode. Scopes `chat:write commands users:read users:read.email im:write
  channels:read groups:read`. Events: `app_home_opened` only. No
  message-reading scopes or events, on purpose (`slack.go:43-47`).
- **Tokens:** vault `hi/server/slack` (`vault_files.go:48-50`).
- **Runtime:** `hi-server.service` holds one Socket Mode connection with
  reconnects (`slackBridge.run`, `slack.go:848-916`). It handles buttons
  and modals (approvals), `/hi`, App Home, channel posts (`notifyChannel`)
  and DMs (`notifyUser`).
- The admin page already uses its token first for sign-in DMs
  (`mail_signin.go:834-850`) and sign-in health posts
  (`admin_signin_health.go:137-145`).

### Each app's Slack app

- **Setup:** wizard step 3 of 10 (`app.go:1537-1556`; the admin page:
  `admin_new.go:493-505`). Manifest from `appManifestData`
  (`app_serve.go:749-777`): the bot is the lead (its name and handle),
  `/hi-<app>`, Messages tab, Socket Mode. Scopes (`app.go:910-916`):
  `app_mentions:read channels:history channels:read chat:write commands
  files:read files:write groups:history groups:read im:history im:read
  im:write reactions:read reactions:write users:read users:read.email`.
  Events: `app_home_opened app_mention message.channels message.groups
  message.im reaction_added reaction_removed`.
- **Tokens:** `appSecrets.SlackBotToken`, `SlackAppToken` (`app.go:177-178`),
  vault `hi/app/<app>`.
- **Channels:** `Channel`, `OpsChannel` in `app.json`, IDs the person types
  or picks (`app.go:288-293`). The bot must be invited by hand.

### Who receives an app's events: Hermes only

- hi's Go code never opens Socket Mode for an app. Hermes in the lead's
  box does, with the app's `xapp` token (`app_serve.go:518-542`), Socket
  Mode only (Hermes `plugins/platforms/slack/adapter.py:1006-1009`).
- `/hi-<app>`, the `hi_site_*` buttons and the DM gate are in hi's Hermes
  plugin (`helper/hermes/__init__.py:71-166, 233-256`). The app is
  whichever box got the event.
- **The box never holds the tokens.** It gets placeholders. Its proxy
  ends TLS for `slack.com` and `files.slack.com` with a per-app CA and
  puts the real token in (`box_slack.go:1-13, 259-293`). The Socket Mode
  websocket (`wss-primary.slack.com`) is a plain tunnel.
- Hermes's Slack API base URL can't be set (`adapter.py:1750-1753`,
  slack_sdk `BASE_URL`). The proxy is the only place between Hermes and
  Slack.

### Everything else hi says, with the app's bot token

The broker, the CLI and the admin page post with the app's bot token
through `newLiveTeamChannel` (`app_channel.go:117`): the channel log every
2 minutes (`conversations.history`, `conversations.replies`, files),
`/hi-<app>` link and sign-in DMs and their `chat.update`
(`app_people.go:166-293`), `$HI_SAY_URL` (`app_appsay.go:118-140`),
`hi app say`, incidents, releases, previews, watches, jobs, workers,
pushed branches, subscriptions, file uploads (reviews, reports, feedback),
"Needs you" DMs, site buttons, ops notices, `adminTell`
(`admin_jobs.go:300-321`), and people lookups by email (`people.go`).

### What assumes one bot is one app

1. One Socket Mode connection per app. Slack gives each event to only
   **one** of an app's connections (`ideas/hi_app_move.md:52`), so leads
   can't share an `xapp` token: each would hear a random share.
2. `/hi-<app>` is in each app's manifest; the plugin answers its own.
3. One DM per person per bot. Hermes keeps one session per DM
   (`dm_top_level_threads_as_sessions: false`). Link DMs are edited in
   place. A DM approval is "the newest message after one of the bot's"
   (`hi_app_dm.md:40-42`, `app_versions.go:519, 567`).
4. The lead's name and handle are the bot's (`app_serve.go:759-761`); the
   wizard says "In Slack it's @handle" (`app.go:1525`).
5. One workspace per app: people, the admin page's DM fallback and the
   people picker loop over every app's token (`people.go:530-545`,
   `mail_signin.go:832-850`, `admin_people.go:392-414`).
6. "Scoped to this team only" (`hi_app.md:557-558`): a lead's token can
   reach only what its own bot was invited to.

Bot messages are told apart by `BotID != ""`, not by which bot
(`app_channel.go:445`), so that part keeps working.

## The design

### One bot: the server's Slack app, with the leads' scopes

The server's manifest gains what an app's has:
`app_mentions:read channels:history groups:history im:history im:read
files:read files:write reactions:read reactions:write`, the events
`app_mention message.channels message.groups message.im reaction_added
reaction_removed`, the Messages tab, and three more scopes:

- `chat:write.customize`: each message says which lead wrote it.
- `channels:manage`, `groups:write`, `channels:join`: hi makes an app's
  channel, or joins a public one, and invites its people.

Its display name is the server's (`hi` by default, `hi aiw11` if several
servers share a workspace). The admin page's Slack section says which
scopes are missing and shows the new manifest to paste, once for the
server, as it does now (`slack.go:1222-1234`).

### The server routes each event to one app

`hi-server.service` already holds the one Socket Mode connection. Each
envelope goes to one app, or to the server itself:

| Event | Goes to |
|---|---|
| Message, mention or reaction in an app's channel or ops channel | that app |
| `/hi <app> …` | that app, as `/hi-<app> …` |
| `/hi …` in an app's channel, with no app named | that app |
| other `/hi …` | the server, as now |
| Button or modal | the app named in its `action_id` or `private_metadata` (hi adds `app=<name>`), else the channel's app, else the server |
| DM | see [DMs](#dms) |
| `app_home_opened` | the server |

A channel belongs to one app. hi refuses a channel that another app on
the server already has.

The server acks Slack at once for events. For slash commands and buttons
it waits up to 2.5 seconds for the lead's ack and passes on its
`payload`, then acks alone. Slack wants an ack within 3 seconds.

When the app's lead isn't running, the server says so in the thread
("patman's lead isn't running; an admin can start it on the admin
page"), once per hour per channel.

### Each lead gets a Socket Mode of its own, from its proxy

Hermes is not changed. Its proxy already ends TLS for `slack.com`. Now
it also does this:

1. `apps.connections.open` with the app placeholder: the proxy answers
   itself, `{"ok":true,"url":"wss://slack.com/__hi/socket"}`.
2. The websocket to `slack.com/__hi/socket` is served by the proxy. It
   connects to the server's socket for this app,
   `~/.local/state/hi/server/slack/<app>.sock`, which only this app's
   proxy mounts. The proxy then passes Socket Mode frames both ways:
   `hello`, envelopes, `disconnect`, acks.
3. The server knows which app is calling by which socket it is. No token
   is needed for this.

Fallback if (1)-(2) break with a Hermes or slack_sdk change: Hermes's
relay platform (`gateway/relay/`, `GATEWAY_RELAY_URL`) is built for one
bot in front of many instances. It is marked experimental, so it is not
the first choice.

### What a lead may do with the one token

The proxy puts the server's bot token in place of the placeholder (from
the server's `slack.env` for the app, written by the server), and
checks every call before it goes on:

- **Channels:** every method that takes a `channel` (chat, conversations,
  reactions, files, assistant.threads, pins) is allowed for the app's
  channels, and for DMs and DM threads the server has given the app
  (below). Anything else gets `{"ok":false,"error":"channel_not_found"}`
  as from Slack, and a line in the network log.
- **Methods:** an allowlist of what Hermes and the hooks use (posting,
  updating, streaming, files, reactions, replies, `users.info`,
  `auth.test`, `conversations.open` for people who may talk to the lead).
  No `conversations.list`, `users.list`, `admin.*`, `apps.*`, `team.*`.
- **Name:** `chat.postMessage` and `chat.postEphemeral` get `username`
  (the lead's name) and `icon_url` (the app's picture, served by the
  admin page) unless they set one. To check before release 2: whether
  `chat.startStream` (Hermes's streamed replies) takes them too. If not,
  streamed replies show the bot's name, or the lead streams off.
- **Reading a DM:** `conversations.history` on a DM is answered with the
  app's threads only. `conversations.replies` is allowed for a thread the
  app has.

hi's own code (broker, CLI, admin page) is trusted on the host and uses
the server's token directly, with the same `username` and `icon_url`.

### DMs

One person has one DM with the bot, for every app. A **thread** belongs
to one app:

- **A message from hi in a DM** (links, sign-in, Needs you, reviews,
  approvals) starts a thread that belongs to its app. Its first line
  names the app. Replies in it go to that app's lead.
- **A new top-level DM** goes to the person's app when they are in only
  one app on the server. Otherwise the bot answers in the thread with a
  button per app; the click gives the thread to that app and passes the
  message on. `patman: …` at the start picks the app at once.
- The server keeps the threads in `slack/dm-threads.json` (person, DM
  channel, thread ts, app).
- Hermes treats each thread as its own conversation:
  `dm_top_level_threads_as_sessions: true`.
- A DM approval is "the newest message in the app's thread, after one of
  the bot's" (`app_versions.go:519, 567`).
- `/hi <app> link` still answers in the DM, in that app's link thread,
  edited in place as now.

This is the largest change to how people use it. See question 1.

### Channels without steps in Slack

The app wizard's step 3 (Slack app) goes. Step 4 (Channel) becomes:

- **Make one** (default): hi makes `#<app>` (or the name given) and
  `#<app>-ops` if asked, private if asked, and invites the owners.
- **Use one:** a list of channels. hi joins a public one itself. For a
  private one, hi says "type `/invite @hi` in it" and waits.

Owners are picked by name from the workspace, through the server's bot
(no member IDs to copy).

### Setup once, on the server

`hi server init` and the admin page's Server → Slack take the Slack app
as they do now, with the larger manifest. Without the server's Slack app,
`hi app new` stops at the Channel step and says "set up the server's
Slack app first (Server → Slack)".

### Existing apps

Each app moves on its own: `hi app slack shared <app>`, or **Use the
server's Slack app** in the app's Slack section.

1. The server's bot joins the app's public channels. For private ones,
   hi asks for `/invite @hi`, and checks.
2. The lead restarts with the placeholder tokens now meaning the server's
   (about 30 seconds).
3. hi drops the app's own tokens from the vault, and says "you can delete
   the Slack app <name> at api.slack.com".

Old DMs with the app's own bot stay as history. The first DM to the
shared bot picks the app as above.

An app can keep its own Slack app: `hi app slack own <app>`, for an app
in another workspace. Everything above then works as today for that app.

### Several servers in one workspace

Each server has its own Slack app, named after it (`hi aiw11`). Each has
`/hi`, and Slack shows both commands with the app's name, as for any two
apps with one command. With Managed mode, one server carries several
machines' apps and their events go over its link to each machine (not
in this spec).

## Releases

1. **The server's bot posts for the apps.** A bigger manifest, the
   routing table, `username` and `icon_url`. The broker, CLI and admin
   page post with the server's token for apps that use it. New apps make
   or join their channel. The lead still has its own bot. Small and safe.
2. **The leads on the shared bot.** The server's per-app sockets, the
   proxy's own Socket Mode and its checks, `/hi <app>`, and buttons
   carrying their app. DMs in threads. `hi app slack shared`.
3. **Remove each app's own Slack app from the wizard.** It becomes
   `hi app slack own`. Spec texts are updated
   (`hi_app.md:30`, `hi_app_dm.md`, `hi_app_helper_signin.md:92-97`,
   `hi_people.md:52-65`, `hi_app_say.md:14-16`).

## Considered and rejected

- **Every lead on the same `xapp` token.** Slack gives each event to one
  connection, so each lead hears a random share.
- **The lead through its API server** (as `hi app say` and the web chat
  do). It loses Hermes's streaming, status, buttons, reactions and files
  in Slack.
- **A slash command per app in one manifest**, updated with
  `apps.manifest.update`. It needs a configuration token that ends every
  12 hours, and every new app changes the Slack app. `/hi <app>` needs
  neither.
- **Patching slack_sdk's `BASE_URL` in the box** (like
  `hermesSlackStatusPrelude`). It works, but the proxy already sits there
  and holds the token; a second seam is more to keep up with.

## Questions for the reviewer

1. **DMs in threads per app.** Is a thread per app, with a picker when a
   person is in several apps, right? Or should DMs stay with one app per
   person (their main app), and other apps only in channels?
2. **Keep `hi app slack own`?** For apps in another workspace, or for
   leads that must not share a token even through the proxy's checks.
3. **The bot's name.** `hi` (or the server's name) for the bot, with
   each message showing the lead's name. Or one name per server only.
4. **hi makes the channels.** It needs `channels:manage` and
   `groups:write`. Some workspaces don't allow that for apps; then "Use
   one" stays.
