# `hi admin`: a machine's apps and its org server, without the command line

Status: Approved (2026-11-06). Release 1 shipped as v2.6.77, release 2 as v2.6.78, release 3 as v2.6.81, release 4 as v2.6.83, release 5 as v2.6.84. Release 6, [a machine that runs both](#a-machine-that-runs-both), approved 2026-11-07 and shipped as v2.6.86. Release 7, [a terminal in an app's boxes](#a-terminal-in-an-apps-boxes), approved 2026-11-08 and shipped as v2.6.96, and release 8, [what an app needs, installed from the page](#what-an-app-needs-installed-from-the-page), approved the same day and shipped as v2.6.97. Decided 2026-11-06: admins only; at
`admin.<domain>` unless the IT team names it; port 1984; the org server's
management is here too, on a machine that runs one. Decided later the
same day: `hi org live` and the terminal wall go, and the page's Running
view replaces them; every org has an owner, set at `hi org init`, whose
admins are the org VM's admin page's admins; the org server becomes a user
service, so no root rule is needed to restart it.

Dependencies: [hi_app.md](hi_app.md) (apps, their services,
the setup), [hi_app_adopt.md](hi_app_adopt.md),
[hi_app_internal.md](hi_app_internal.md) (an app's NetBird
peer and its relay), [hi_app_addresses.md](hi_app_addresses.md)
(names the IT team picks),
[hi_app_dev_access.md](hi_app_dev_access.md) (owners and
developers, people's links), [hi_app_dev.md](hi_app_dev.md)
(the dev page, whose design this takes),
[hi_app_sso.md](hi_app_sso.md),
[hi_app_snapshots.md](hi_app_snapshots.md),
[hi_org.md](hi_org.md) and [hi_server.md](hi_server.md)
(the org server and its admin socket),
[hi_update.md](hi_update.md), and later
[hi_app_sso_hi5.md](../ideas/hi_app_sso_hi5.md).

## Goal

A machine with hi runs apps, an org server, or both. Each app has a dev
page for its people, but whoever runs the machine has only the command
line. For apps that means `hi app ls`, `hi app new` and its ten
questions, `hi app member`, `hi app key`, `hi app restore`, and fifty
more. For the org server it means `hi org approve`, `hi org user add`,
`hi org policy edit`, `hi org provider add`, and thirty more. They SSH
in, remember the commands, and read the dev page's notes that say "an
admin of the machine changes them". Some of them have never used a
terminal.

The **admin page** is one web page per machine, for the people who run
it. On a machine with apps, they see every app, open any of them,
create new ones, and manage them. On a machine with the org server, they
approve requests, keep its people, keys, and rules, and watch what runs
and what it costs. It has everything the command line does that a web
page can safely do.

```text
hi admin start
```

starts it, keeps it up, and prints a link. From then on the admin needs
the command line only for what the admin page can't do (below).

A machine shows what it runs. With apps and no org server, it has the
apps. With an org server and no apps, such as the org's own VM, it has
the org, and **New app** for when someone wants an app there. With
both, it has both.

## Who it is for

- **The machine's admins:** whoever runs the machine. They set up its
  apps, give them keys and sign-ins, restore them, update hi, and, with
  an org server, approve and keep the org. On the command line that is
  anyone who can log in as the account that runs hi. On the admin page
  it is the people on its list of admins.
- **Not an app's people, and not the org's.** Owners and developers
  build and release on the app's dev page, as now. The admin page opens
  the dev page; it doesn't replace it. People in the org join their
  devices, ask for compute, and approve in Slack, as now.
- **Later, hi5.sh as a service:** each customer gets an admin page for
  their own apps. Nothing in it assumes it is the only admin page on the
  machine, or that its admins can log in to the machine
  ([later](#later)).

## What it looks like

The dev page's design, as far as it goes: the same dark colours, the
same bar, and a panel on each side of the work, each with its views
behind the icons along its top. Each panel can be dragged wider,
hidden, and remembered. On a phone they lie over the work, one at a
time. The left has what you set: the apps, the org, and what goes into
them. The right has what comes out: requests, activity, jobs, logs,
incidents, and spend.

```text
┌──────────────────────────────────────────────────────────────────────────────────────┐
│ [◧] hi on ws1   demo ▸ Overview People Agents Slack Network Data Settings  [Open ▾] [◨] │
├──────────────────────┬─────────────────────────────────────────┬─────────────────────┤
│ Apps³ New People Keys│                                         │ Requests² Activity   │
│ Org Network Machine• │  demo  ● up   production v12            │ Jobs• Logs Incid.¹ ✕ │
│──────────────────────│  The suppliers' invoices, paid on time  │─────────────────────│
│ APPS         [+ New] │                                         │ Olga's laptop wants  │
│ ● demo      v12      │  Production  demo.corp.example     ↗    │ to join  [Approve]   │
│ ● payments  v4   ⚠1  │  Stage       dev + 2 changes       ↗    │                      │
│ ○ quarry    down     │  Dev page                          ↗    │ Sam: an H100 for 4h, │
│ ❄ ledger    frozen   │                                         │ $9.60  [Approve]     │
│                      │  lead    hermes · glm-5 · OpenRouter    │ [Deny…]              │
│                      │  coder   claude · opus · the app's      │                      │
│                      │  Today   $1.20 of models, Stage $0.40   │                      │
│                      │                                         │                      │
│                      │  [Restart] [Stop] [Snapshot] [Freeze…]  │                      │
└──────────────────────┴─────────────────────────────────────────┴─────────────────────┘
```

- **The bar:** the left panel's button, the machine's name, the open
  app's name and its sections as chips (as the dev page's Chat,
  Terminal, and VS Code), **Open ▾** (production, Stage, and the dev
  page), and the right panel's button.
- **The middle:** **Home** with nothing open: every app as a card, and
  the org's day (requests waiting, what runs, today's spend). Or an
  app's sections, the **New app** steps, an org view opened wide, or
  the machine.
- **Numbers and dots on the icons:** requests waiting for an approval,
  the apps that are down or frozen, open incidents, advisories nobody
  has ignored, a blue dot while a job runs and a red one when it failed,
  a dot on Logs that pulses for new lines, and a dot on Machine when a
  new hi is out.

### The left panel: what you set

Since release 6 the org isn't a view here: it is a card on Home, with its
sections in the middle, and the org's users and keys are there, not in
People and Keys ([a machine that runs both](#a-machine-that-runs-both)).

| View | What it has | The commands it replaces |
|---|---|---|
| **Apps** | Every app: its state (up, down, frozen, paused), production's version, its open incidents; **+ New app**; **Bring one back** for an app whose folder is here | `hi app ls`, `hi app up <folder>` |
| **New** | Create an app, or adopt a project (the steps, [below](#new-app)) | `hi app new`, `hi app adopt` |
| **People** | Everyone with a level in any app, a column per app: give someone the same level in several apps at once, each change the app's own, said in its channel. With an org server, the org's users too: their group and devices, **Add**, **Remove**, and the Slack approvers | `hi app owners`, `hi app access`, `hi org user`, `hi org approvers` |
| **Keys** | The machine's keys and sign-ins (Hermes' OpenRouter key, Claude Code's and Codex's sign-ins), and which apps use the machine's or their own. With an org server, its provider keys (RunPod, Shadeform) and the org's OpenRouter key | `hi app key`, `hi app signin`, `hi org provider`, `hi org ai set` |
| **Org** | With an org server: its policy (each group's limits, auto-approval, and budgets, as a form, checked before it's saved), its templates, its Hugging Face data, the models served on the org's machines, its Slack app, its admins, and its wall screens' links. Without one: **Join an org** for this machine, or **Start an org server here** | `hi org policy`, `templates`, `data`, `ai`, `slack setup`, `admins`, `hi org join`, `hi org init` |
| **Network** | The machine's domain, the sign-in providers, each app's NetBird peer, the org server's public URL for cloud runs, and **What to ask your IT team**: the records and policies needed, as text to send | `hi app domain`, `hi app sso provider`, `hi app addresses`, `hi org expose` |
| **Machine** | hi's version and **Update**, with which apps and servers restart; memory, disk, and CPU, each app's boxes too; the machine's heartbeat; the admin page's own address and admins | `hi update`, `hi app limit … memory` |

### The middle: an app's sections

Each section turns one kind of command into forms. A secret is asked for
and never shown again: the page says whether it's set, and whose (the
app's own or the machine's).

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
| **Requests** | With an org server: devices waiting to join and compute waiting for an approval, each with **Approve** (and the group, for a device) and **Deny** with a reason, recorded under the admin's name as `hi org approve` is |
| **Running** | With an org server: everything running, for every user, with its cost so far and **Stop**. It replaces `hi org live`, which goes |
| **Spend** | With an org server: spend per user and group, compute and models, over a week or a month |
| **Activity** | What admins did on the admin page, newest first, with who and when; each app's releases, rollbacks, and freezes; with an org server, its audit (`hi org audit`) |
| **Jobs** | The long actions, running and recent: creating an app, a rebuild, a restore, an update. Their output as it comes, as the dev page's Checks |
| **Logs** | The open app's lead and service, or the org server's, as they come; **!** for errors only |
| **Incidents** | Every app's open incidents first, and what the lead did about them |
| **Advisories** | Every app's, worst first, with **Ignore** and a reason |

A view a machine has no use for isn't there: an org server's own VM has
no Incidents until it has an app, and a machine with apps has no
Requests until it runs an org server.

## Reaching it

The admin page can do anything the account that runs hi can do, so it
is internal only, and asks for a sign-in on top of that.

Port 1984, one of two ways, whichever the machine has. It is above
1024, so the account that runs hi listens there itself, with no root:

- **On a NetBird peer of its own,** on a machine with apps: the small
  rootless container each app's addresses already use, named
  `<machine>-admin` (`ws1-admin.corp.example:1984`). It needs a NetBird
  setup key once, and works whether or not the machine itself is on
  NetBird, as apps' addresses do.
- **On the machine's NetBird address,** on a machine that is on NetBird
  and has no podman, such as an org server's VM: `ws1.corp.example:1984`,
  as the org server listens on that address only.

Either way, NetBird's Access Control decides who reaches it: a policy
from the admins' group, TCP 1984 (and 443 with a domain). hi prints what
to ask the IT team for.
- **`admin.<domain>`,** on https, with the machine's domain
  (`hi app domain`). The app name `admin` is then refused.
- **A name the IT team picks,** as for apps' addresses:
  `hi admin address <host>` (or on the page, Machine). hi serves it on
  port 1984 and on the peer's port 80, for an A record to the peer or the
  IT team's proxy, and `--https` when their proxy ends TLS.
- **On this machine only** (`--local`): `http://127.0.0.1:1984/`, for a
  machine someone sits at, or an SSH tunnel.

It is never public: `hi net expose` refuses it, and so does the admin
page's own Network view.

## Signing in

- **An admin's link,** as a person's link to the dev page: a key that
  becomes a cookie, for 14 days, and a session that stays while it's
  used. `hi admin start` makes the first admin (it asks their name and,
  optionally, their Slack member ID) and prints their link. On the org
  server's machine it asks nothing: the org's owner is the first admin
  ([the org's owner](#the-orgs-owner)).
- **More admins** are added on the admin page (Machine) or with
  `hi admin admins add`. Each new admin gets their link the way the
  person who added them picks: shown once to copy, or sent by Slack.
- **A link that ended:** the sign-in page is one button, **Send me a
  new link**, nothing to type. It goes to the admin the browser last
  signed in as (a cookie that holds their name, no key), else to the
  machine's owner. The admin page DMs it through the
  bot of any app in their Slack workspace, or the org server's Slack
  app, as links are sent now. So the command line is needed only for an
  admin who has no Slack.
- **The org's admins:** on the org server's machine, the admin page's
  admins are the org's owner and admins, one list. Approving on the page
  is under the admin's own name, as in Slack.
- **Later, sign-in with the organisation's provider** (hi_app_sso.md, or
  hi5.sh): admins by email, links only as a way back in.
- **Opening an app's dev page** opens it as the admin is in that app,
  through the same link `/hi-<app> dev` sends. An admin with no level
  in the app gets **Make me an owner of demo** instead. That is an owner
  change like any other, said in the app's channel. The admin page
  never lets anyone get round the dev page's own access.
- **From the dev page back:** the dev page's notes that send people to
  "an admin of the machine" (owners, the app's keys and sign-ins,
  restoring a snapshot) stay for everyone. For someone who is an admin
  too, each also links to its place on the admin page.

## The org's owner

Every org has an owner: one person, with their Slack member ID and their
email.

- **Set at `hi org init`:** it asks for both (`--owner <Slack ID>
  --email <address>` without a terminal) and won't make an org without
  them. The owner is the first approver, so their Slack ID is the one
  the Slack app knows, and the first admin of the org server's admin
  page.
- **The org's admins:** the owner adds and removes admins
  (`hi org admins add <Slack ID> --name <name> --email <address>`, or on
  the page). An admin is an approver too, and an admin of the org
  server's admin page. Approvers who aren't admins stay as now: they
  approve and stop in Slack, and nothing more. One list, so the admin
  page on the org's VM keeps no list of its own.
- **Email** is for sign-in by email later, and for the owner's link when
  Slack isn't set up yet or a Slack app is gone. Until there is email
  sign-in, a link to the owner by email is printed to send, not sent.
- **An org made before this:** `hi org status`, `hi update`, and the
  admin page say it has no owner and give the command, `hi org owner
  <Slack ID> --email <address>`, run on the server's machine. Until then
  the first `hi admin start` there asks for them.
- **Handing it on:** the owner, or anyone logged in as the account the
  server runs as, runs `hi org owner` with the new owner. It is in the
  audit and said in the approvers' channel.

## A machine that runs both

Release 6. A machine runs apps, an org server, or both, and the page is
the same on each: no modes. Until release 6 the page turns the org's
views on beside the apps' when there is an org server. That works, but
on a machine with both it mixes what the icons mean, and two things
conflict.

**What conflicts now:**

- **Who the admins are.** With an org server, the page's admins are the
  org's owner and admins, in place of the machine's own list. On a
  machine with apps, its admins who aren't the org's lose the page, and
  making someone an admin to restart an app makes them an approver of
  the org's spend in Slack. Running apps and approving spend are
  different jobs.
- **Two orgs.** A machine with apps is usually joined to an org. `hi org
  init` there starts a second one, while the machine's apps still get
  their compute from the first: the page shows one org, the apps use
  another.

**What only confuses:** People has the apps' people (Slack members, a
level per app) and the org's users (hi user names, devices, groups),
and the same person is in both, unlinked. Keys has the machine's keys
and the org's provider keys. In the right panel, Jobs, Logs, Incidents,
and Advisories follow the open app, while Requests, Running, and Spend
are the org's. The Org icon is there on some machines and not others,
and moves the others along.

**What doesn't conflict:** the ports (1984, 7373, the apps'), NetBird,
and one Activity for both.

### The org is a card, as an app is

- **Home:** the machine, then the org's card if the machine runs an org
  server (requests waiting, what runs, today's spend), then the apps'.
  A machine with only the org has one card.
- **The left panel is the same on every machine,** each icon meaning one
  thing: **Home**, **People** (the apps' people only), **Keys** (the
  machine's only), **Network**, **Machine**, **Activity**. The Org view
  goes.
- **The org opened** has its sections in the middle and on the bar, as
  an app's: **Overview** (its state, requests, what runs, spend,
  Restart), **Users** (their groups, devices, Add, Remove, the Slack
  approvers), **Admins** (the owner and admins), **Policy**, **Keys**
  (the provider keys, the org's OpenRouter key), **Templates**,
  **Data**, **Models**, **Slack**, **Wall screens**. The org's users
  leave People, and its keys leave Keys.
- **The right panel is about what's open:** with the org, **Requests**,
  **Running**, **Spend**, **Logs**, **Incidents**; with an app, **Jobs**,
  **Logs**, **Incidents**, **Advisories**; with nothing open, as with an
  app, every app's. The org's incidents are what its reconcile warns of:
  an instance on the org's account that no request started, a cleanup
  that hasn't worked. Requests waiting are a number on the org's card
  and on Home's icon wherever the page is, and in Slack as now.

### Two lists of admins

- **The machine's admins** (the page's `admin.json`) sign in to the page
  and run the machine and its apps.
- **The org's owner and admins** (`people.json`) approve, set the
  policy, and change the org's users, keys, and admins.
- **On a machine with only the org,** its owner and admins are the
  machine's admins too, as now: nothing changes on the org's VM.
- **On a machine with both,** `hi org init` adds the owner to the
  machine's admins and leaves the others there. A machine admin who
  isn't the org's sees the org's card and sections, and changes nothing
  in them; an org admin who isn't the machine's signs in and sees the
  machine and the org, and changes only the org. Making someone a
  machine admin never makes them an approver.

### Starting an org where there are apps

- **`hi org init` on a machine joined to another org** refuses, and says
  `hi org leave` first. The machine's apps would otherwise ask the other
  org for compute.
- **On a machine with apps and no org,** it joins the machine to the
  new org, as `hi org join` does, so its apps' compute goes through it,
  under the owner's hi user name.
- **The org server holds the keys that spend money,** and the apps run
  agents, under the same account, and restart with it on `hi update`.
  `hi org init` on a machine with apps says so, and the guide says
  that an org of more than a few people puts its server on a small VM
  of its own. A machine with both is for a small org, or to try one.

## A terminal in an app's boxes

Release 7. An admin who fixes an app sometimes needs to look inside one
of its boxes: why the lead won't start, what production's data folder
holds, whether a package is there. Today that is `podman exec` over SSH,
which an admin who has never used a terminal can't do, and the dev
page's terminal reaches only the dev box, and only for the app's owners
and Pro Devs.

**What it reaches.** An app's boxes, one at a time: the lead's,
production's, Stage's, the dev box, and the memory's (each as it runs;
one that doesn't run isn't offered). Not the machine: the account that
runs hi holds every app's keys and the org's provider keys, so there is
still no shell on it (see [Safety](#safety)). The boxes hold no keys of
the app's: the proxy beside each adds them, so a terminal in a box shows
what the app and its agents see, and nothing more.

**Who.** The machine's owner only: the org's owner on a machine that
runs the org with its admins not apart, else the first admin `hi admin
start` made, as `admin.json` records. Other admins see the section, and
that the owner can open one. Handing the machine on (`hi admin owner
<name>`) is a command, as the org's owner's is.

**What it looks like.** A **Terminal** section in the app, after Data:
a picker of the boxes that run, and the terminal below it, xterm.js
over a WebSocket, as the dev page's terminal is. It runs a shell in the
box (`bash`, else `sh`) as the box's own user, not root, in the box's
working folder. It closes when its tab does, after 30 minutes without a
key, and when the owner's sign-in ends; the shell goes with it, nothing
keeps running.

**Every terminal is said**, as every change is: in Activity and in the
app's ops channel (or its channel), "Olga opened a terminal in demo's
production box, from the admin page", and when it closed. What's typed
isn't kept: the people's data that a terminal shows stays theirs.

**What it doesn't do:** no terminal in the org server (it runs no
boxes), none in the box proxies or the NetBird peer (their keys and
the network are the machine's), and none from a wall link.

## What an app needs, installed from the page

Release 8. An app needs tools on its machine that a fresh machine may
lack, and today the first anyone hears of one is a failed Create or an
alert hours later: a test machine's first app had no herdr (so no dev box, and
no NetBird peer for its addresses) and no restic (so no snapshots). The
page checks first, and installs what's missing when an admin presses
**Install**.

**What it checks.** At the start of **New app**, before the steps, what
every app needs:

| Needs | For | Root |
|---|---|---|
| podman | every box | yes |
| Hermes, with its Slack support | the lead | no |
| herdr | the dev box, and so the NetBird peer and the addresses | no |
| restic | the snapshots | yes |
| Lingering (`loginctl enable-linger`) | the app running after logout and at boot | yes |

And at **Create**, what the answers need: Claude Code for a role on
Claude, Codex for one on Codex, gh or glab for a copy on GitHub or
GitLab. The same list is on the machine's view, for the apps it has.

**What it looks like.** A list, each with a dot and what it's for;
what's missing has **Install**. **Install all** installs every missing
one in one job, which Jobs follows as it follows Create. The steps
start once nothing required is missing; Create waits the same way for
what the answers need.

**Root, with the machine's password.** For what needs root, the page
asks for the sudo password of the account that runs hi, in a password
field beside Install. It goes in the job's request, to `sudo` through a
pipe for that job alone: never written to disk, never in the job's
arguments, its log, or Activity, and gone when the job ends. A wrong
password is said as sudo says it. The page can't do more than the
password lets anyone do in a terminal. Installing only what needs no
root asks for nothing (`hi install` asks for sudo only when it needs
it, since v2.6.95). This replaces, for these tools only, "what needs
root is never done" in [Safety](#safety).

**Who.** Any of the machine's admins: the password is what decides,
not the page.

**Every install is said** in Activity: "Olga installed herdr and restic
from the admin page", without the password.

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
  than the apps. The admin page has no terminal on the machine and lists
  no folders. Its one terminal, from release 7, is inside an app's box,
  for the machine's owner (see [A terminal in an app's
  boxes](#a-terminal-in-an-apps-boxes)). The one path anyone types is a folder to adopt, which is
  copied without its secrets, as `hi app adopt` does.
- **What can't be undone asks first,** with the app's name typed:
  removing an app, a restore, a rollback, copying Stage's data to
  production, and a rename. With an org server, removing a user and
  stopping everything of a user. Each says what it does first, as the
  commands do (a snapshot before a restore).
- **Every change is said:** in Activity, with who, and in the app's ops
  channel (or its channel), as the dev page's changes are: "Olga gave
  demo its own OpenRouter key, from the admin page." The org server's
  changes go in its audit, as the commands' do.
- **Only from its own page:** every change is a POST from the admin
  page's own address, checked as the dev page checks its own.
- **What needs root or the IT team** (podman, lingering, packages,
  NetBird's policies, DNS records, the wall's screens) is never done.
  The page says which command an admin runs on the machine, or what to
  ask the IT team, with the text to copy. From release 8, the tools an
  app needs are the exception: installed with the sudo password typed
  on the page, used once and not kept (see [What an app needs,
  installed from the page](#what-an-app-needs-installed-from-the-page)).

## What stays on the command line

- Installing hi, and the first `hi admin start` (or saying yes when the
  installer, `hi app new`, or `hi org init` offers it).
- What needs `sudo`, shown as the command to run.
- The backups' encryption key.
- A shell on the machine, and the channel's history (`hi app channel`):
  the people's conversations, which the lead's log and the incidents
  don't need.
- Joining a device to an org (`hi org join`): it is the device's own
  key, on the device.

## How it works

- **One process per machine account:** `hi admin serve`, run by
  `hi-admin.service`, a user service, as the apps' are, so it stays up
  after a reboot. On a machine that doesn't keep the account's services
  running without a login (an org server's VM, maybe), `hi admin start`
  says the one command to change that (`sudo loginctl enable-linger`). Its files are in `~/.local/share/hi/admin/`: `admin.json` (its
  address and admins), `keys.json` (the admins' keys, 0600),
  `activity.jsonl`, and `jobs/`.
- **The page** is `helper/admin/`, embedded in hi as the dev page is.
  The dev page's panels, icons, badges, chips, dialogs, and toasts
  become a shared piece both pages load, so the two stay alike and a fix
  lands in both.
- **The same code as the commands.** Nothing on the admin page
  re-implements a command:
  - **Reads** (the apps, their state, members, snapshots, incidents) call
    what `hi app ls`, `status`, and the others call, in the process.
  - **The org server** is asked through its admin socket, as the
    `hi org` commands on its machine ask it, and Running reads the live
    stream `hi org live` read.
  - **Quick changes** (a member's model, a setting, a level, an
    approval) call the function the command calls once it has its
    answers.
  - **Long ones** (new, adopt, rebuild, restore, rollback, rm, update)
    run the `hi` command itself as a job, in a user unit of its own
    (`systemd-run --user`). A job outlives the admin page: `hi update`
    restarts the page, and the page waits for the new version to answer.
  - **The org server becomes a user service.** `hi-org.service` is a
    system unit now, though it runs as the account that keeps its state
    and listens on a port above 1024, so restarting it after an update
    needs `sudo`, which the page can't do. As a user service, beside
    `hi-admin.service`, the page restarts it as it restarts apps. `hi
    update` offers the move once, as it moved `hi-server.service` to
    `hi-org.service`: it writes the user unit from the system one, turns
    on lingering, and removes the system unit, with `sudo` that one
    time. Down for about a second.
    A job's secrets go in a 0600 file the command deletes once read, as
    `hi app dev <app> netbird <file>` does now.
  - A command the admin page runs takes all its answers without a
    terminal: flags, `--from`, a file. Where one doesn't yet, it gains
    it, and scripts get that too.
- **The command line keeps working.** Both change the same files.
  A change takes the app's lock, as commands do. A second change to a
  busy app waits, and the page says what it waits for. The page reads
  again every few seconds, as the dev page does, so a change made on the
  command line shows.
- **Tested in a real Chromium,** as the dev page is, not only in
  happy-dom.

## Commands

```text
hi admin                         whether it runs, its address, its admins
hi admin start                   start it and keep it up, also after a reboot; the
                                 first time, makes you its first admin and prints your link
    [--netbird <setup key file>] the peer's setup key, the first time (or on the page)
    [--local]                    on this machine only, 127.0.0.1:1984
hi admin stop                    stop it; nothing else changes
hi admin link [<admin>]          a new sign-in link, for 14 days; the old one ends
hi admin admins                  the admins
    [add <name> [--slack <member ID>] | remove <name>]
hi admin address [<host> [--https] | default]
                                 the name the IT team keeps for it; admin.<domain> by default
```

## Releases

1. **Running and reaching it:** `hi admin` and its commands; the peer,
   port 1984, `admin.<domain>`, and a name of the IT team's; admins'
   links and **Send me a new link**; Home, Apps, Overview's actions,
   Open ▾ with the dev page; Activity, Jobs, Logs, Incidents; Machine's
   version and **Update**.
   As built: until the peer has joined (or without a setup key), the page
   is on the machine's own NetBird address, so it works at once on a
   machine that is on NetBird; and every way, it is also on the
   machine's 127.0.0.1:1984, for an SSH tunnel. `hi admin start` takes
   `--name` and `--slack` for the first admin without a terminal. A
   link keeps working for 14 days on more than one device; each sign-in
   it makes lasts 14 days from its last use; `hi admin link` ends the
   old links, not the sign-ins. The dev page's notes linking back to the
   admin page come with release 4, beside each app's settings.
2. **The org server:** Requests, Running, Spend, the org's people,
   keys, policy, templates, data, and models, its audit in Activity; the
   org's owner and admins; wall links. `hi org live`, `hi org viewer`, and
   the terminal wall (`hi org wall`, `hi-wall-live`) go, and `hi update`
   says so on a machine that still has a wall session. The org server
   moves to a user service. An org server's VM can be run from the page
   from here on.
   As built: the org's owner and admins are in the org's `people.json`,
   and the page maps them in as its admins on the server's machine; an
   admin's name there is their hi user name, as approvals record it. Only
   the owner adds and removes them on the page; on the command line,
   anyone logged in as the server's account can (`hi org admins`). An
   admin added or removed is an approver, or not, in `slack.json` at once,
   and `hi org slack setup` adds the owner and admins to the approvers.
   Handing the org on keeps the owner before as an admin. `hi org init`
   takes `--owner-name` for the owner's hi user name (otherwise the first
   part of their email), and installs and starts the user service itself,
   unless `--dir` or `--no-start`. The policy is a form per group in the
   middle (limits, hardware, approval without a person, budgets, the
   reports), with **As text** for the rest; template sources and data per
   group stay as they are in the form. Keys holds the provider keys and
   the org's OpenRouter key; Org the models served on the team's machines.
   Wall links are kept as hashes in the page's `keys.json`, served at
   `/wall/<key>` with the page's own stylesheet, and say when each was last
   open. The org server's Restart is synchronous, not a job. `hi compute
   live`, a user's own machines in the terminal, stays.
3. **New apps:** the ten steps with the Slack link, the pickers, and the
   sign-ins on the page; adopting; **Bring one back**; Rename and Remove.
   As built: a draft per new app in the page's `drafts/` (0600), with the
   tokens given for it; the page gets only whether each is there. The
   folder is asked first, beside the machine's other apps' by default.
   Create writes the answers as `--from` takes them and the tokens in a
   0600 file the job's runner reads once and deletes, and runs `hi app
   new` then `hi app up` as one job (a job can run more than one hi
   command now); the draft goes when it went well, and stays to fix and
   try again when it didn't. `--from` gained `full`, the whole history of
   a project over 1 GB, which the terminal asks about. Adopting is the
   Code step's, or the start page's project field; **Look at it** asks
   GitHub (description, size, licence, whether this machine can push
   there) or counts the folder, and the full look runs in the job. Bring
   one back lists app folders beside the apps' folders that aren't apps
   here. Remove keeps the folder unless unticked, and is said in the app's
   channel before it goes; Rename too. The People step picks owners from
   the workspace's people (`users:read`), the Channel step from the
   channels the bot is in (`channels:read`, `groups:read`), scopes every
   app's manifest has had.
4. **Each app's settings:** People, Agents, Slack, Settings; Keys;
   People across apps.
   As built: each change runs the `hi app` command as the admin
   (`HI_ADMIN_BY`), its secret on stdin, never in its arguments, and the
   page says it in the app's ops channel and Activity; signing in to
   ChatGPT, GitHub, GitLab, and fetching upstream are jobs (the ChatGPT
   code shows in Jobs). An agent's kind isn't changed here, as the
   command doesn't; the main channel isn't either. File settings are
   listed, and set on the machine (`--file`). Advisories to ignore come
   with Advisories, in release 5. The Slack app's manifest is to copy into
   Slack (Slack has no link that updates an app). The machine's own keys
   are shown, set or not, with the command that sets each; the apps' own
   are in their Agents. The dev page gets the admin page's address for a
   person whose Slack member ID is one of its admins', and links its
   notes there.
5. **Network and data:** each app's Network and Data sections, the
   machine's Network view, What to ask your IT team, Advisories.
   As built: changes run the `hi app` commands as in release 4; moving
   the addresses, a restore, a rollback, and a copy to production are
   jobs, and the last three need the app's name typed, on the page and
   again by the server. The NetBird setup key goes in a 0600 file that
   `hi app dev … netbird` reads and deletes; the offsite bucket's keys on
   stdin. Where a command says it best (the dev box, the sign-in rules,
   Stage's data, the backups, the jobs), the section shows its words.
   Whether an address is public stays the owners', on the dev page.
   What to ask the IT team is written from each app's addressing and
   peer, and the admin page's and the org server's. Advisories carry
   their app, sorted exploited, to act on, the report, then ignored;
   Ignore asks why. The org server's public URL for cloud runs is shown,
   and `hi org expose` stays on the command line.

6. **A machine that runs both:** the org as a card on Home with its
   own sections; the left panel the same on every machine; the right
   panel following what's open, the org or an app; the machine's admins
   and the org's apart; `hi org init` refusing on a machine joined to
   another org, joining a machine with apps to the new one, and saying
   what sharing the machine means.
   [A machine that runs both](#a-machine-that-runs-both) has it.
   As built: the org's row is first in Home on the left, as "The org",
   with its requests waiting, and Home's icon counts them with the apps
   that are down. `admin.json` keeps `org_apart`, set by `hi org init`
   when the page already had admins on a machine with apps; without it,
   the org's owner and admins are the machine's, as on the org's VM, also
   once that machine gets an app. Each admin carries `machine` and `org`,
   and the page's server refuses a change outside them (`/api/org` and
   `/api/walls` are the org's, the rest the machine's), so what the page
   greys out is refused there too. An admin of the org who isn't the
   machine's opens no dev page. The org's admins are changed in its
   Admins, by its owner, with `what: admins`; a new one's link is shown
   once. The org's incidents are read from its `state.json` (cleanup
   leases, `unleased`) and its audit, a month back, as app `:org`. `hi org
   init` joins the machine only when it makes the org, in the group
   `staff`, with the machine's device key written into the new state
   before the server starts; an org in `--dir` isn't the machine's, and
   skips these steps. Jobs isn't among the org's views: following a job
   from there opens Home.

7. **A terminal in an app's boxes:** the app's Terminal section, its
   picker of the boxes that run, a shell in one for the machine's owner,
   said in Activity and the ops channel, closed after 30 idle minutes;
   `hi admin owner`.
   [A terminal in an app's boxes](#a-terminal-in-an-apps-boxes) has it.

8. **What an app needs, installed from the page:** the check at New
   app's start and at Create, and on the machine's view; Install and
   Install all as a job; the sudo password for what needs root, piped
   to sudo for that job and never kept.
   [What an app needs, installed from the page](#what-an-app-needs-installed-from-the-page) has it.

## Later

- **hi5.sh as a service:** people who host their apps with hi5.sh manage
  them on an admin page of their own, at hi5.sh, with the same views.
  It then serves more than one admin page per machine, each for its own
  account's apps, behind one name and its sign-in. That is its own spec.
- **Help:** a chat on the right that answers how to do something in hi
  from hi's guide, on the machine's OpenRouter key. It can point at the
  button or fill in the form, but an admin presses it.
- **Sign-in by email** through the organisation's provider or hi5.sh.
- **Several machines:** the org server's admin page lists the org's
  machines and their apps' states, and opens each one's admin page.

## Wall screens

A wall screen opens a **wall link** in a browser, full screen: Running
and Requests, read-only, with names as initials or in full and reasons
shown or not, as `--wall` had. Admins make and end wall links on the page
(Org). A wall link can't approve, stop, or open anything else, and ends
when it's ended, not after 14 days. It replaces the tmux session the
screens showed over SSH, so a screen needs no `sudo` setup, only a
browser on NetBird.

## Open questions

None yet.

## Acceptance criteria

1. On a machine with three apps, `hi admin start` prints a link, and the
   link shows all three with their states and their addresses.
2. On a machine that runs only an org server, the admin page shows its
   requests, users, policy, and spend, and an approval there is the same
   as `hi org approve`, recorded under the admin's name.
3. Opening an app's dev page from the admin page signs in as the admin
   is in that app, and an admin without a level there can't get round
   that.
4. A new app made on the admin page, without a terminal, is the same as
   one made by `hi app new` with the same answers: the same `team.json`
   and the same folder.
5. Every action on the admin page is in Activity and in the app's
   channel or the org's audit, with who did it.
6. No view, request, or log of the admin page holds a secret's value.
7. `hi net expose` refuses the admin page, and nothing reaches it from
   outside NetBird or the machine.
8. `hi update` from the admin page updates hi, restarts the apps and
   the admin page (and the org server), and the page
   comes back on the new version by itself.
9. A change made with a command while the page is open shows on it
   within a few seconds.
10. Removing an app, a restore, and a rollback each need the app's name
    typed.
11. It works on a phone: the panels lie over the middle, one at a time.
12. `hi org init` makes no org without an owner's Slack ID and email,
    and that owner signs in to the org server's admin page with no other
    step.
13. A wall link shows Running and Requests in a browser and can change
    nothing.
14. The left panel has the same views, in the same order, on a machine
    with apps, one with only an org server, and one with both.
15. On a machine with apps, `hi org init` keeps the machine's admins;
    making one of them an admin of the page doesn't make them an
    approver; and a machine admin who isn't the org's can't approve.
16. `hi org init` refuses on a machine joined to another org, and on a
    machine with apps it joins the machine to the new org.
