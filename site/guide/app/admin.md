---
title: The admin page
description: "hi admin start gives a machine one web page for the people who run it: every app on it, their state, restarts, snapshots, jobs, logs, incidents, and hi's updates, without the command line."
---

A machine with hi runs apps. Each app has a [dev page](/guide/app/dev/)
for its people. Whoever runs the machine has the **admin page**: one web
page with every app on the machine, and what used to need SSH and a
command.

```sh
hi admin start
```

starts it, keeps it up, also after a reboot, and prints your link. The
first time, it asks your name and, optionally, your Slack member ID, and
makes you its first admin.

## What it has

The dev page's design: the same dark colours and bar, with a panel on
each side of the middle. Each panel can be dragged wider, hidden, and
remembered. On a phone they lie over the middle, one at a time.

- **Home:** every app as a card, with its state, production's version,
  its open incidents, and its addresses.
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
- **Activity** (right): what admins did here, with who and when.
- **Jobs** (right): the long actions, running and done, with their
  output. A blue dot while one runs, a red one when one failed.
- **Logs** (right): an app's lead and service, as `hi app logs` shows
  them, or the admin page's own. Its dot pulses when new lines come in
  behind another view, red when one says something failed.
- **Incidents** (right): every app's, the open ones first.

Every change is said: in Activity, with the admin's name, and in the
app's ops channel, as the app's bot: "Olga restarted the app, from the
admin page."

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

Coming next: the org server on the same page (requests, what runs, and
what it costs), creating apps on it, and each app's settings.
