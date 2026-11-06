---
title: The admin page
description: "hi admin start gives a machine one web page for the people who run it: every app on it, the org server if it runs one, restarts, snapshots, approvals, what runs and what it costs, jobs, logs, incidents, and hi's updates, without the command line."
---

A machine with hi runs apps, an [org server](/guide/compute/managed/), or
both. Each app has a [dev page](/guide/app/dev/) for its people. Whoever
runs the machine has the **admin page**: one web page with every app on
the machine, the org if it runs one, and what used to need SSH and a
command.

```sh
hi admin start
```

starts it, keeps it up, also after a reboot, and prints your link. The
first time, it asks your name and, optionally, your Slack member ID, and
makes you its first admin. On the org server's machine it asks nothing:
the org's owner is its first admin ([below](#on-the-org-servers-machine)).

## What it has

The dev page's design: the same dark colours and bar, with a panel on
each side of the middle. Each panel can be dragged wider, hidden, and
remembered. On a phone they lie over the middle, one at a time.

- **Home:** the machine's health: how busy its CPU is, its load, its
  memory, swap, and each disk, filling up green, then yellow, then red.
  Then every app as a card, with its state, production's version, its
  open incidents, its addresses, and what it uses: its share of the CPU
  and the memory of its lead and its boxes together, and its folder on
  disk (measured every ten minutes). The page measures while it's open.
- **Apps** (left): the apps, the ones that are down or frozen counted on
  the icon. Pick one to open it in the middle.
- **An app's Overview:** its state, addresses, members, owners, latest
  releases, snapshots, and heartbeat. **Restart** (or **Start**),
  **Stop**, **Rebuild**, **Snapshot now**, **Freeze…**, **Pause…**, and
  **Tell the lead**. Each runs the command it stands for: `hi app up`,
  `hi app down`, `hi app rebuild`, `hi app snapshot`, `hi app freeze`,
  `hi app pause`, `hi app say`.
- **Open ▾:** production, Stage, and the app's **dev page**, opened as
  you are in the app: an owner or developer with a link of their own. An
  admin with no level there gets **Make me an owner** instead, which is
  said in the app's channel like any owner change. The admin page never
  gets round the dev page's own access.
- **Machine** (left): hi's version and **Update**, with a dot on the
  icon when a new hi is out; the admin page's address and what to ask
  your IT team for; its admins.
- **Activity** (left): what admins did here, with who and when, and on
  the org server's machine the org's audit.
- **Jobs** (right): the open app's long actions, running and done, with
  their output; on Home, every job, the machine's too, such as an update.
  A blue dot while one runs, a red one when one failed.
- **Logs** (right): the open app's lead and service, as `hi app logs`
  shows them; on Home, the admin page's own, or the org server's on its
  machine. Its dot pulses when new lines come in behind another view, red
  when one says something failed.
- **Incidents** (right): the open app's, the open ones first; on Home,
  every app's.

Every change is said: in Activity, with the admin's name, and in the
app's ops channel, as the app's bot: "Olga restarted the app, from the
admin page."

## New apps

**+ New app** (on Home, or **+** in Apps) makes one without a terminal:
hi app new's ten steps as pages, with **Back** and **Next**. Each answer
is checked by the machine as it's given, the same checks as the
terminal's, and kept, so a reload or a closed laptop loses nothing: the
new apps under way are listed to carry on with, or discard.

1. **Lead:** its name, and the bot's handle it gives.
2. **Purpose:** one sentence.
3. **Slack app:** **Create the Slack app** opens Slack with the manifest
   filled in; then **Install to Workspace**, and the two tokens, each
   checked as it's given.
4. **Channel:** after `/invite @bot` in it, picked from the channels the
   bot is in, and an ops channel the same way.
5. **People:** the owners, picked by name from the workspace's people.
6. **Members:** each role's agent, model, who pays, and effort. A ChatGPT
   sign-in of the app's own shows its code and link on the page, and the
   page sees when it's done; a Claude token is pasted, or the machine's
   sign-in used. Each role's fallback on OpenRouter is checked.
7. **Models:** the app's own OpenRouter key, or the machine's.
8. **Code:** a new web app, an empty repository, a template, or someone's
   project to adopt (GitHub, a URL, or a folder here), with **Look at it**
   first: what GitHub says of it, or what the folder holds. Kept on
   GitHub or GitLab too, if you like.
9. **Network:** the NetBird setup key, or later, and how the addresses
   are named.
10. **Heartbeats:** the app's, and the machine's if it has none.

**Create** makes it as `hi app new --from` would with the same answers,
then starts it, as a job: Jobs follows it, and the app opens when it's
up. A token is sent to the machine once, never shown again, and kept for
the job in a file only it reads, which it deletes. If creating it fails,
the answers stay: fix the step and **Create** again. **From a file**
starts from answers someone wrote before, as `--from` takes them.

**Bring one back** lists the app folders beside the apps' folders that
aren't apps here (one removed with its folder kept, or moved, or copied
from another machine), or takes a folder's path, and starts it:
`hi app up <folder>`.

## Each app's settings

An app open in the middle has its sections in the bar, after Overview.
Each change runs the `hi app` command it stands for, under your name,
and the app's ops channel hears it: "Olga made Sam a Pro Dev, from the
admin page." A secret is sent once and never shown again: the page says
only whether one is set, and whose.

- **People:** the owners, Pro Devs, Vibe Devs, and contributors, each one's
  level as a list to change, and their SSH keys for the dev box; **Add**
  someone by their Slack member ID. `hi app owners`, `hi app access`.
- **Agents:** each role's model, who pays for it, its fallback on
  OpenRouter, and its effort (the app restarts); the app's own OpenRouter
  key or the machine's; its ChatGPT sign-in (the code to type shows in
  Jobs) and its Claude token, or the machine's; what Stage's model calls
  may cost a day; the boxes' memory. `hi app member`, `key`, `signin`,
  `limit`.
- **Slack:** the bot, the channel, the ops channel, the tokens again when
  Slack gave new ones, the manifest to paste into the Slack app after an
  update or a rename, and **Rename…**. `hi app ops`, `key slack`,
  `manifest`, `rename`.
- **Settings:** the settings the app's boxes get, by name only, to add,
  give a new value, or remove, for Stage and production, production's
  own, Stage's own, or the dev box's; the helper on Stage and production;
  the watcher's automatic freeze; the heartbeat; GitHub or GitLab, and an
  adopted project's upstream; and **Remove…**. `hi app env`, `helper`,
  `watch`, `heartbeat`, `github`, `gitlab`, `upstream`, `rm`.

- **Network:** its NetBird peer, and **Join** with a setup key (kept in
  a file the command reads once and deletes); how its addresses are named,
  and **Move them** (the app restarts); its name on the machine's domain;
  the addresses let in; the sign-in on top, with a provider, email
  domains, and groups; and what to ask your IT team for it, to copy.
  `hi app dev … netbird`, `addresses`, `domain`, `allow-ip`, `sso`.
- **Data:** its snapshots, each with **Restore…**, and **Snapshot now**;
  its releases, with **Roll back…** and each one's bill of materials;
  Stage's data: a new fork, starting empty at every release, or **Copy
  Stage's to production…**; its backups, how many are kept, and copying
  them offsite to an S3 bucket; its jobs. A restore, a rollback, and a
  copy to production each need the app's name typed, and say what they do
  first. `hi app snapshots`, `restore`, `rollback`, `data`, `backups`,
  `jobs`, `bom`.

On the left, **Network** has the apps' domain, the sign-in providers
(**Add one**), each app's peer and addresses, the org server's public
address for cloud runs on its machine, and **What to ask your IT team**:
every policy and record the admin page and the apps need, as text to
send. On the right, **Advisories** has the open app's, or every app's,
worst first: exploited, then to act on, then for the weekly report,
then ignored; **Ignore…** asks why, and the app's people see it.

On the left, **People** has everyone with a level in any app, a column
per app: change a level where it stands, or give someone the same level
in several apps at once, each the app's own change, said in its channel.
On the org server's machine the org's users are there too. **Keys** has
the machine's keys and sign-ins (Hermes' OpenRouter key, Claude Code's,
and Codex's), and which apps use their own.

On an app's dev page, the notes that send people to an admin of the
machine (owners, the app's keys, restoring a snapshot) link to the admin
page, at the app's section, for someone who is one of its admins.

In an app's Overview, **Rename…** and **Remove…** each need the app's
name typed. Remove keeps the app's folder unless you say otherwise, so
it can come back.

## On the org server's machine

A machine that runs the org server has the org on its admin page too. A
machine with only the org server, such as the org's own VM, has the org
and no apps.

- **Home:** the org's day: requests waiting, what runs and at what rate,
  today's spend and the month's against its budgets.
- **Requests** (right): devices that want to join and compute waiting for
  an approval, each with **Approve** (and the group, for someone new) and
  **Deny…** with a reason. It is the same as `hi org approve`, under the
  admin's own name, and nobody approves their own.
- **Running** (right): everything running, for every user, with its cost
  so far, when it stops, and **Stop**; and **Stop all** of someone's,
  with their name typed.
- **Spend** (right): per user and group, compute and models, over a week
  or a month.
- **Org** (left): the org server, and **Restart**; its owner, and **Hand
  it on…** for the owner; Slack; the policy, with each group's limits,
  hardware, approvals without a person, and budgets as a form in the
  middle, checked by the server before it's saved; the private templates,
  the Hugging Face data, the models served on the team's machines; and
  the wall screens' links.
- **People** (left): the org's users, their groups and devices; **Add**,
  and **Remove** with their name typed.
- **Keys** (left): the provider keys (RunPod, Shadeform) and the org's
  OpenRouter key for `hi q`, each checked before it's kept, and shown only
  as set or not.
- **Activity** has the org's audit, and **Logs** on Home the org server's.

**Its admins are the org's:** the owner and the admins `hi org admins`
lists. Each is an approver too, in Slack and here, and the page keeps no
list of its own. The owner adds and removes them, in Machine. A new link
comes by DM from the org's Slack app.

**Wall screens:** in Org, **+** by Wall screens makes a link for a screen.
Opened in a browser, full screen, it shows Running and Requests, with
initials for names and no reasons unless the link says otherwise. It
can't approve, stop, or open anything, and it works until an admin ends
it. It replaces `hi org live --wall` and the screens that showed it over
SSH: a screen needs only a browser on NetBird.

**The org server is a user service** here, `hi-org.service`, so the page
restarts it, and `hi update` does without `sudo`. One set up before hi
2.6.78 runs as a system service: on a terminal, `hi update` moves it, with
`sudo` once.

## Signing in

Your link signs you in for 14 days, and the sign-in keeps going while
you use it. It works on more than one device. Keep it to yourself:
whoever opens it can do what you can on the machine.

- **More admins:** in Machine, **+** adds one. Their link is shown once
  to copy, or sent by Slack DM. On the machine: `hi admin admins add
  <name> --slack <member ID>`.
- **A link that ended:** the sign-in page has **Send me a new link**. An
  admin whose Slack member ID the page knows gets one by DM, from the
  bot of an app on the machine. Without Slack, an admin makes one on the
  machine: `hi admin link <name>`.
- **Your Slack member ID** also tells each app who you are, so Open ▾
  opens its dev page as you. Add yours in Machine if you didn't give it
  at the start (in Slack: your profile, ⋮, **Copy member ID**).

## Reaching it

It can do anything the account that runs hi can do, so it is on
NetBird, never public, and asks for a sign-in on top of that. It is on
port 1984, which needs no root:

- **On a NetBird peer of its own,** on a machine with podman:
  `<machine>-admin` (`ws1-admin.corp.example:1984`), the small rootless
  container apps' addresses use. It needs a NetBird setup key once:
  `hi admin start --netbird <file>`, or on the page, in Machine. Ask
  your IT team for a NetBird policy from the admins' group to that peer,
  TCP 1984: then only admins reach it. Until the peer has joined, the
  page is on the machine's own NetBird address.
- **On the machine's NetBird address,** on a machine without podman,
  such as an org server's VM: `ws1.corp.example:1984`.
- **`admin.<domain>`,** on https, when the machine has a domain
  ([`hi net domain`](/guide/workstation/netbird/)) and the page has its
  peer. No app can be called `admin`.
- **A name your IT team picks:** `hi admin address admin.corp.example`,
  for an A record to the peer, or their proxy to its port 80;
  `--https` when their proxy ends TLS.
- **On the machine only:** `hi admin start --local`, at
  `http://127.0.0.1:1984/`; from your computer, `ssh -L
  1984:127.0.0.1:1984 <machine>`. Every way, the page is also on the
  machine's 127.0.0.1:1984.

`hi net expose` refuses port 1984.

## Safety

- **No secret comes out.** The page shows whether something is set,
  never its value; an app's addresses are shown without their keys.
- **No shell, no file browser.**
- **What can't be undone asks first**, and says what it does.
- **Only from the page itself:** every change is a POST from the page's
  own address, as JSON; another site's page can't make one.
- **What needs root or the IT team** is never done: the page says the
  command, or what to ask for. On a machine that doesn't keep the
  account's services running without a login, that is
  `sudo loginctl enable-linger <user>`.

## How it runs

`hi admin serve`, in the user service `hi-admin.service`, as the apps'
services are. A long action runs the `hi` command itself in a user unit
of its own (`systemd-run --user`), so it outlives the page: **Update**
restarts the page, the job goes on, and the page comes back on the new
hi by itself. `hi update` on the command line restarts the page too.
Its files are in `~/.local/share/hi/admin/`: `admin.json` (how it's
reached, and the admins), `keys.json` (the links and sign-ins, as
hashes), `activity.jsonl`, and `jobs/`.

The command line keeps working: both change the same files, and the page
reads again every few seconds, so a change made with a command shows.

## Commands

```text
hi admin                         whether it runs, its address, its admins
hi admin start                   start it and keep it up; the first time, your link
    [--netbird <setup key file>] its NetBird peer's setup key, the first time
    [--local]                    on this machine only, 127.0.0.1:1984
    [--name <name>] [--slack <member ID>]
                                 the first admin, without being asked
hi admin stop                    stop it; nothing else changes
hi admin link [<admin>]          a new sign-in link, for 14 days; the old one ends
hi admin admins [add <name> [--slack <member ID>] | remove <name>]
hi admin address [<host> [--https] | default]
```

Later: hi5.sh as a service, with an admin page for each customer's apps;
help on the page; sign-in by email; and the org's machines from its
server's admin page.
