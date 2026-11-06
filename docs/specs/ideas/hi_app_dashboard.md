# The dashboard: every app on a machine, without the command line

Status: Draft (2026-11-06).

Dependencies: [hi_app.md](../approved/hi_app.md) (apps, their services,
the setup), [hi_app_adopt.md](../approved/hi_app_adopt.md),
[hi_app_internal.md](../approved/hi_app_internal.md) (an app's NetBird
peer and its relay), [hi_app_addresses.md](../approved/hi_app_addresses.md),
[hi_app_dev_access.md](../approved/hi_app_dev_access.md) (owners and
developers, people's links), [hi_app_dev.md](../approved/hi_app_dev.md)
(the dev page, whose design this takes),
[hi_app_sso.md](../approved/hi_app_sso.md),
[hi_app_snapshots.md](../approved/hi_app_snapshots.md),
[hi_update.md](../approved/hi_update.md), and later
[hi_app_sso_hi5.md](hi_app_sso_hi5.md) and [hi_org.md](../approved/hi_org.md).

## Goal

A machine with hi runs several apps. Each app has a dev page for its
people, but the person who hosts the apps has only the command line:
`hi app ls`, `hi app status`, `hi app new` with its ten questions,
`hi app up`, `hi app member`, `hi app key`, `hi app restore`, `hi update`,
and fifty more. They have to SSH into the machine, remember the
commands, and read the dev page's notes that say "an admin of the
machine changes them". Some of them have never used a terminal.

The **dashboard** is one web page per machine where its admins see every
app, open any of them, create new ones, and manage them: everything the
command line does that a web page can safely do.

```text
hi app dashboard start
```

starts it, keeps it up, and prints a link. From then on the admin needs
the command line only for what the dashboard can't do (below).

## Who it is for

- **The machine's admins:** whoever hosts the apps: sets them up, gives
  them keys and sign-ins, moves them, restores them, and updates hi. On
  the command line that is anyone who can log in as the account that
  runs hi. On the dashboard it is the people on its list of admins.
- **Not an app's people.** Owners and developers build and release on
  the app's dev page, as now. The dashboard opens the dev page; it
  doesn't replace it. A home for a person with a level in several apps
  is [later](#later).

## What it looks like

The dev page's design, as far as it goes: the same dark colours, the
same bar, and a panel on each side of the work with its views by the
icons along its top. Each panel is dragged wider, hidden, and
remembered, and on a phone they lie over the work, one at a time. The
left has what you set: the apps and what goes into them. The right has
what comes out: activity, jobs, logs, incidents, and advisories.

```text
┌──────────────────────────────────────────────────────────────────────────────────────┐
│ [◧] hi on ws1   demo ▸ Overview People Agents Slack Network Data Settings  [Open ▾] [◨] │
├──────────────────────┬─────────────────────────────────────────┬─────────────────────┤
│ Apps³ New People Keys│                                         │ Activity Jobs• Logs  │
│ Network Machine•   ✕ │  demo  ● up   production v12            │ Incidents¹ Advis.² ✕ │
│──────────────────────│  The suppliers' invoices, paid on time  │─────────────────────│
│ APPS         [+ New] │                                         │ 10:42 Olga restarted │
│ ● demo      v12      │  Production  demo.corp.example     ↗    │   payments           │
│ ● payments  v4   ⚠1  │  Stage       dev + 2 changes       ↗    │ 10:30 Sam gave demo  │
│ ○ quarry    down     │  Dev page                          ↗    │   its own OpenRouter │
│ ❄ ledger    frozen   │                                         │   key                │
│                      │  lead    hermes · glm-5 · OpenRouter    │ 09:58 v12 of demo    │
│                      │  coder   claude · opus · the app's      │   released by Anna   │
│                      │  Today   $1.20 of models, Stage $0.40   │                      │
│                      │                                         │                      │
│                      │  [Restart] [Stop] [Snapshot] [Freeze…]  │                      │
└──────────────────────┴─────────────────────────────────────────┴─────────────────────┘
```

- **The bar:** the left panel's button, the machine's name, the open
  app's name and its sections as chips (as the dev page's Chat,
  Terminal, and VS Code), **Open ▾** (production, Stage, and the dev
  page), and the right panel's button.
- **The middle:** **Home** (every app as a card) with no app open, an
  app's sections, the **New app** steps, or the machine.
- **Numbers and dots on the icons:** the apps that are down or frozen,
  open incidents, advisories nobody has ignored, a blue dot while a job
  runs and a red one when it failed, a dot on Logs that pulses for new
  lines, and a dot on Machine when a new hi is out.

### The left panel: what you set

| View | What it has | The command it replaces |
|---|---|---|
| **Apps** | Every app: its state (up, down, frozen, paused), production's version, its open incidents; **+ New app**; **Bring one back** for an app whose folder is here | `hi app ls`, `hi app up <folder>` |
| **New** | Create an app, or adopt a project (the steps, [below](#new-app)) | `hi app new`, `hi app adopt` |
| **People** | Everyone with a level in any app, a column per app: give someone the same level in several apps at once, each change the app's own, said in its channel | `hi app owners`, `hi app access` |
| **Keys** | The machine's keys and sign-ins (Hermes' OpenRouter key, Claude Code's and Codex's sign-ins), and which apps use the machine's or their own | `hi app key`, `hi app signin` |
| **Network** | The machine's domain, the sign-in providers, each app's NetBird peer, and **What to ask your IT team**: the records and policies the apps need, as text to send | `hi app domain`, `hi app sso provider`, `hi app addresses` |
| **Machine** | hi's version and **Update**, with which apps restart; memory, disk, and CPU, each app's boxes too; the machine's heartbeat; the dashboard's own address and admins | `hi update`, `hi app limit … memory` |

### The middle: an app's sections

Each section is the commands of one kind, as forms. A secret is asked
for and never shown again: the page says whether it's set, and whose
(the app's own or the machine's).

| Section | What it has | Commands |
|---|---|---|
| **Overview** | State, addresses, versions, members, today's model costs; **Restart**, **Stop**, **Rebuild**, **Snapshot now**, **Freeze**, **Pause**; **Tell the lead** | `up`, `down`, `rebuild`, `snapshot`, `freeze`, `pause`, `status`, `say` |
| **People** | The owners, each one's level, their SSH keys: the dev page's People table | `owners`, `access` |
| **Agents** | Each role's agent, model, provider, fallback, and effort; the app's own OpenRouter key; its ChatGPT sign-in (the code to type, shown on the page) and Claude token; Stage's limit a day and the boxes' memory | `member`, `key openrouter`, `signin`, `limit` |
| **Slack** | The channel and the ops channel, picked from the channels the bot is in; the tokens again; the manifest, with **Update the Slack app**; **Rename** | `ops`, `key slack`, `manifest`, `rename` |
| **Network** | Joining NetBird (the setup key), how the addresses are named, the domain, public or internal per address, the addresses allowed in, the sign-in rules, the heartbeat | `dev netbird`, `addresses`, `domain`, `allow-ip`, `sso`, `heartbeat` |
| **Data** | The snapshots, **Restore**, **Roll back**, Stage's data (fork, copy to production, start empty), backups (offsite, how many kept), the jobs, the bill of materials | `snapshots`, `restore`, `rollback`, `data`, `backups`, `jobs`, `bom` |
| **Settings** | The app's settings by name (Stage, production, the dev box), the helper on or off, the watcher's automatic freeze, advisories to ignore with a reason, GitHub, the upstream of an adopted project; at the end, **Remove the app** | `env`, `helper`, `watch`, `advisory`, `github`, `upstream`, `rm` |

### The right panel: what comes out

| View | What it has |
|---|---|
| **Activity** | What admins did on the dashboard, newest first, with who and when, and each app's releases, rollbacks, and freezes from its record |
| **Jobs** | The long actions, running and recent: creating an app, a rebuild, a restore, an update. Their output as it comes, as the dev page's Checks |
| **Logs** | The open app's lead and service, as they come; **!** for errors only |
| **Incidents** | Every app's open incidents first, and what the lead did about them |
| **Advisories** | Every app's, worst first, with **Ignore** and a reason |
| **Help** | Ask how to do something in hi ([later](#later)) |

## Reaching it

The dashboard can do anything the account that runs hi can do, so it is
internal only, and asks for a sign-in on top of that:

- **On the machine's NetBird address** when the machine is on NetBird,
  as the org server listens on its NetBird address only:
  `http://ws1.corp.example:7380/`. NetBird's Access Control says who
  reaches it: a policy from the admins' group, TCP 7380. hi prints what
  to ask the IT team for.
- **On a NetBird peer of its own** (`--netbird <setup key file>`) when
  the machine isn't on NetBird: the small rootless container each app's
  addresses already use, named `hi-<machine>`, on port 80. With the
  machine's domain (`hi app domain`), on https at
  `dashboard.<domain>` too; hi then refuses an app named `dashboard`.
- **On this machine only** (`--local`): `http://127.0.0.1:7380/`, for a
  machine someone sits at, or an SSH tunnel.

It is never public: `hi net expose` refuses it, and so does the
dashboard's own Network view.

## Signing in

- **An admin's link,** as a person's link to the dev page: a key that
  becomes a cookie, for 14 days, and a session that stays while it's
  used. `hi app dashboard start` makes the first admin (it asks their
  name and, optionally, their Slack member ID) and prints their link.
- **More admins** are added on the dashboard (Machine) or with
  `hi app dashboard admins add`. Each new admin gets their link the way
  the person who added them picks: shown once to copy, or sent by Slack.
- **A link that ended:** an admin with a Slack member ID gets **Send me
  a new link** on the sign-in page. The dashboard DMs it through the bot
  of any app in their Slack workspace, as apps send people their links
  now. So the command line is needed only for an admin who has no Slack.
- **Later, sign-in with the organisation's provider** (hi_app_sso.md, or
  hi5.sh): admins by email, links only as a way back in.
- **Opening an app's dev page** opens it as the admin is in that app,
  through the same link `/hi-<app> dev` sends. An admin with no level
  in the app gets **Make me an owner of demo** instead. That is an owner
  change like any other, said in the app's channel. The dashboard never
  lets anyone round the dev page's own access.

## New app

`hi app new`'s ten steps, as pages with **Back** and **Next**. Each
answer is checked as it's given, and what's done is kept, so a reload
or a closed laptop loses nothing. The command line asks for IDs. The
page asks for what people know:

1. **Lead:** its name; it shows the bot's handle as it will be.
2. **Purpose:** one sentence.
3. **Slack app:** **Create the Slack app** opens Slack with the manifest
   filled in (`https://api.slack.com/apps?new_app=1&manifest_json=…`,
   Slack's own link for sharing a manifest), and the page says the two
   clicks after it: **Create**, then **Install to Workspace**. Then the
   two tokens, each checked as it's pasted, with where Slack keeps it
   (a picture of each place helps).
4. **Channel:** after **/invite @bot** in the channel, the page lists
   the channels the bot is in (it has `channels:read` and
   `groups:read`) to pick from, and the ops channel the same way.
5. **People:** the owners, picked by name from the workspace's people
   (`users:read`), not member IDs.
6. **Members:** each role's agent, model, and effort, as lists. A
   ChatGPT sign-in shows its code and link on the page; a Claude token
   is pasted from `claude setup-token`, with the command to copy.
7. **Models:** the app's own OpenRouter key, or the machine's.
8. **Code:** a new web app, an empty repository, a clone, a project on
   GitHub to adopt (its URL, and a look at it before going on), or a
   folder on this machine (its path, typed); GitHub or not.
9. **Network:** the NetBird setup key, or later; how the addresses are
   named, with the names they'll have.
10. **Heartbeats:** the app's, and the machine's if it has none.

**Create** writes the answers as `hi app new --from` takes them and runs
the setup as a job: the page follows it in Jobs, and opens the app when
it's up. **From a file** takes answers someone wrote before, as
`--from` does.

## Safety

- **Secrets go in, never out.** A key, a token, or a setting's value is
  asked for and written. The page shows only whether it's set and
  whose. No view reads one back. The backups' encryption key stays on
  the command line (`hi app backups <app> --show-key`).
- **No shell, no file browser.** The account that runs hi holds more
  than the apps. The dashboard has no terminal on the machine and lists
  no folders. The one path anyone types is a folder to adopt, which is
  copied without its secrets, as `hi app adopt` does.
- **What can't be undone asks first,** with the app's name typed:
  removing an app, a restore, a rollback, copying Stage's data to
  production, and a rename. Each says what it does first, as the
  commands do (a snapshot before a restore).
- **Every change is said:** in Activity, with who, and in the app's ops
  channel (or its channel), as the dev page's changes are: "Olga gave
  demo its own OpenRouter key, from the dashboard."
- **Only from its own page:** every change is a POST from the
  dashboard's own address, checked as the dev page checks its own.
- **What needs root or the IT team** (podman, lingering, packages,
  NetBird's policies, DNS records) is never done. The page says which
  command an admin runs on the machine, or what to ask the IT team,
  with the text to copy.

## What stays on the command line

- Installing hi, and the first `hi app dashboard start` (or saying yes
  when the installer offers it).
- What needs `sudo`, shown as the command to run.
- The backups' encryption key.
- A shell on the machine, and the channel's history (`hi app channel`):
  the people's conversations, which the lead's log and the incidents
  don't need.

## How it works

- **One process per machine:** `hi app dashboard serve`, run by a user
  service, `hi-dashboard.service`, as the apps' are, so it stays up after
  a reboot. Its files are in `~/.local/share/hi/dashboard/`:
  `dashboard.json` (its address and admins), `keys.json` (the admins'
  keys, 0600), `activity.jsonl`, and `jobs/`.
- **The page** is `helper/dashboard/`, embedded in hi as the dev page
  is. The dev page's panels, icons, badges, chips, dialogs, and toasts
  become a shared piece both pages load, so the two stay alike and a fix
  lands in both.
- **The same code as the commands.** Nothing in the dashboard
  re-implements a command:
  - **Reads** (the apps, their state, members, snapshots, incidents) call
    what `hi app ls`, `status`, and the others call, in the process.
  - **Quick changes** (a member's model, a setting, a level) call the
    function the command calls once it has its answers.
  - **Long ones** (new, adopt, rebuild, restore, rollback, rm, update)
    run the `hi` command itself as a job, in a user unit of its own
    (`systemd-run --user`). A job outlives the dashboard: `hi update`
    restarts the dashboard, and the page waits for the new version to
    answer. A job's secrets go in a 0600 file the command deletes once
    read, as `hi app dev <app> netbird <file>` does now.
  - A command the dashboard runs takes all its answers without a
    terminal: flags, `--from`, a file. Where one doesn't yet, it gains
    it, and scripts get that too.
- **The command line keeps working.** Both change the same files.
  A change takes the app's lock, as commands do. A second change to a
  busy app waits, and the page says what it waits for. The page reads
  again every few seconds, as the dev page does, so a change made on the
  command line shows.
- **Tested in a real Chromium,** as the dev page is, not only in
  happy-dom.

## Releases

1. **Running and reaching it:** `hi app dashboard start`, `stop`,
   `link`, `admins`; the three ways to reach it; admins' links and
   **Send me a new link**; Home, Apps, Overview's actions, Open ▾ with
   the dev page; Activity, Jobs, Logs, Incidents; Machine's version and
   **Update**.
2. **New apps:** the ten steps with the Slack link, the pickers, and the
   sign-ins on the page; adopting; **Bring one back**; Rename and Remove.
3. **Each app's settings:** People, Agents, Slack, Settings; Keys;
   People across apps.
4. **Network and data:** each app's Network and Data sections, the
   machine's Network view, What to ask your IT team, Advisories.

## Later

- **Help:** a chat on the right that answers how to do something in hi
  from hi's guide, on the machine's OpenRouter key. It can point at the
  button or fill in the form, but an admin presses it.
- **Sign-in by email** through the organisation's provider or hi5.sh.
- **A person's own home:** someone with a level in several apps sees
  theirs, with their links. That needs one identity across apps, and
  apps may be in different Slack workspaces. Email, through sign-in,
  is the likely answer.
- **Several machines:** the org server lists each machine's dashboard,
  with its apps' states, and opens it.

## Open questions

1. **Its address on the machine's domain:** `dashboard.<domain>`, with
   the app name `dashboard` refused, or a name the IT team picks, as
   apps' names are?
2. **A read-only level,** for a colleague or a wall display, or admins
   only?
3. **The dev page's "an admin of the machine changes them":** should
   each one link to the right section of the dashboard, for someone who
   is an admin too?
4. **The port:** 7380 beside the org server's 7373, or 80 on the
   machine's NetBird address when nothing else has it?

## Acceptance criteria

1. On a machine with three apps, `hi app dashboard start` prints a link,
   and the link shows all three with their states and their addresses.
2. Opening an app's dev page from the dashboard signs in as the admin is
   in that app, and an admin without a level there can't get round that.
3. A new app made on the dashboard, without a terminal, is the same as
   one made by `hi app new` with the same answers: the same `team.json`
   and the same folder.
4. Every action on the dashboard is in Activity and in the app's
   channel, with who did it.
5. No view, request, or log of the dashboard holds a secret's value.
6. `hi net expose` refuses the dashboard, and nothing reaches it from
   outside NetBird or the machine.
7. `hi update` from the dashboard updates hi, restarts the apps and the
   dashboard, and the page comes back on the new version by itself.
8. A change made with a command while the page is open shows on it
   within a few seconds.
9. Removing an app, a restore, and a rollback each need the app's name
   typed.
10. It works on a phone: the panels lie over the middle, one at a time.
