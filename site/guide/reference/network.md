---
title: Networking
description: Every port hi listens on, split by the server and admin page, each app, and your own machine; who connects, how they sign in, and the NetBird policies each needs.
---

hi keeps everything it serves on [NetBird](/guide/workstation/netbird/),
never on a public address, with two exceptions that end on their own:
`hi net expose` and the server's address for cloud jobs. Nothing on this
page needs root: every port is a user service's, or a rootless
container's.

It comes in three parts, each with its own NetBird peer and policies:

- **[The server and the admin page](#the-server-and-the-admin-page)**,
  once per machine: what devices join, and where the admins work.
- **[Each app](#each-app)**, on a peer of its own: its production, Stage,
  dev page, and dev box.
- **[Your own machine](#your-own-machine)**: what you share yourself, for
  a while.

## The picture

```text
 THE SERVER AND THE ADMIN PAGE, once per machine
 devices (hi server join) ──TCP 7373──────────▶ the server's peer ws1-server ──▶ hi server
 admins, in a browser ─────TCP 1984, 80, 443──▶ the page's peer ws1-admin
                                                (or the server's peer) ─────▶ admin page ──▶ calls the server for you
 cloud jobs (--data) ──────https──▶ netbird expose ──▶ the server's :7374, the data proxy only

 EACH APP, on its own peer hi-<app>
 the app's people ─────────TCP 80, 8080 ──┐
 its owners and developers ─TCP 8888, 22 ──┴──▶ hi-<app> ──▶ production, Stage, the dev page, SSH to the dev box
                             (and 443 for both on the machine's domain)
```

The examples are for ws1, a machine with apps, in the NetBird network
`corp.example`.

## The server and the admin page

One `hi server` and one admin page per machine with apps
([`hi server init`](/guide/compute/managed/)), or on a machine of their
own. Each can have a NetBird peer of its own; without one they are on the
machine's NetBird address.

| Port | What listens | Where | Who connects | How they sign in |
|------|--------------|-------|--------------|------------------|
| **7373** | hi server, `hi-server.service` | its own peer `ws1-server` ([`hi server net join`](/guide/compute/managed/#a-netbird-peer-of-its-own)), else the machine's NetBird address; `127.0.0.1` too | devices that joined it; `hi` on the machine, at `127.0.0.1` | the device's key |
| **7374** | the server's data proxy for cloud jobs | its peer, or the machine's NetBird address, published with `netbird expose` while a run needs it | rented machines in `hi compute run --data` and `up --data` | a run token, for that run's data |
| **1984** | admin page, `hi-admin.service` | its own peer `ws1-admin` when it has one; else the server's peer `ws1-server`, beside the server, or the machine's NetBird address; always `127.0.0.1` too | the server's owner and admins | a link, then a browser cookie |
| **80** | admin page | the same peer as 1984, for a name your IT team points at it (`hi server address`) | admins | as on 1984 |
| **443** | admin page over https | only its own peer, as `admin.<domain>`, with [`hi net domain`](/guide/workstation/netbird/); not on the server's peer | admins | as on 1984 |

`hi net expose` refuses 1984: the admin page can do anything the account
that runs hi can do.

### The policies to ask for

The admin page's **Network**, on the left, lists these under **What to
ask your IT team**, with your names filled in, ready to copy and send:

- **The server:** from the server's people to its peer `ws1-server` (or
  the machine, without one), TCP 7373; and from the peer to the machines
  with models served for the server (`hi serve llm --org`), on their
  ports.
- **The admin page:** from the admins' group to its peer, or the server's
  peer when the page has none of its own (or the machine), TCP 1984, and
  443 with a domain.
- **Cloud jobs:** no policy, but **Peer Expose** turned on once
  (Settings > Clients in the NetBird dashboard). `hi net expose` needs it
  too.

### Why the server and the page are apart

They are two services, so restarting the page doesn't drop the devices
connected to the server. Devices and people sign in differently: a device
with its key, a person with a link and a cookie. And their NetBird
policies differ: every device that joins reaches 7373, while only admins
reach 1984. The page calls the server for you; nobody opens 7373 in a
browser.

## Each app

Every app has a NetBird peer of its own, `hi-<app>`, which joins once with
a setup key ([the dev page's guide](/guide/app/dev/)); its production,
Stage, dev page, and dev box are all reached through it, and nothing else
of the app is on the network. Which ports depends on how the app's
addresses are named (`hi app addresses <app>`):

| | On its NetBird name (the default) | On the machine's domain ([`hi net domain`](/guide/app/dev/#the-addresses-on-https)) | A name for each, A records ([`names`](/guide/app/dev/#a-name-for-each-address)) |
|---|---|---|---|
| **Production** | 80 | 443, `<app>.<domain>` | 80, by its name |
| **Stage** | 8080 | 443, `<app>-preview.<domain>` | 80, by its name |
| **Dev page** | 8888 | 443, `<app>-dev.<domain>` | 80, by its name |
| **SSH to the dev box** | 22 | 22 | 22 |

- On the machine's domain, the NetBird name's ports keep answering too.
- One host name your IT team keeps by hand (`hi app addresses <app>
  <host name>`) has the NetBird name's ports.
- Names that your IT team's reverse proxy sends on reach the peer on 80,
  8080, and 8888, from the proxy; TLS may end there (`--https`).
- The dev box's sshd is on 2222 inside the box; the peer passes 22 to it.

Who connects, and how they sign in:

| Address | Who | How they sign in |
|---------|-----|------------------|
| Production and Stage | the app's people | the app's own sign-in; on a [public address](/guide/app/try/#public-addresses-that-stay) too, its link or your organisation's (`hi app sso`) |
| Dev page | its owners and developers | their dev page link, or email |
| SSH to the dev box | its owners and developers | their SSH key |

### The policies to ask for

Each app's **Network** section on the admin page has its own **What to
ask your IT team**. The peer is in the group `hi-<app>` (the setup key's
auto-group):

- From the app's people's group, TCP 80 and 8080.
- From the group of its owners and developers, TCP 8888 and 22.
- Both TCP 443 as well on the machine's domain. With a name for each
  address, A records need only 80 (and 22).

Where the dev page shares a port with production (443 on the domain, 80
with names), NetBird can't keep anyone off it by its port: the dev page's
own sign-in is what does.

## Your own machine

What you share yourself, neither the server's nor an app's:

| Port | What listens | Where | Who connects | How they sign in |
|------|--------------|-------|--------------|------------------|
| **8080**, or the next free | a model from `hi serve llm --on local` | `127.0.0.1`; with `--share` or `--org` also the machine's NetBird address | you; with `--share` a teammate; with `--org` the server | none on `127.0.0.1`; a key on NetBird |
| any | `hi net expose <port>` | a public `https://…netbird.services` address, for a while; `--vpn`: the machine's NetBird address | whoever you give it to | a password, link, PIN, SSO, or none (`--public`) |

## Inside the machine

These are only on a box's own network, the machine's `127.0.0.1`, or a
socket, never on NetBird.

**An app's boxes:**

| Port | What |
|------|------|
| 3128 | hi's proxy, the only way out of the lead's box and the dev box, to the hosts in its allowlist; Stage's and production's boxes reach the internet themselves, through a resolver of the app's that logs every name they look up |
| 3129 | in the lead's box and the dev box, the proxy that puts the app's real tokens in place of placeholders (Claude, ChatGPT, OpenRouter); in Stage's and production's boxes, the relay for their model calls |
| 53, 27017 | in the dev box's proxy: its DNS, and TLS to MongoDB for the hosts it may reach |
| 8888 | Hindsight, the lead's memory, on the app's own network |
| 8642 | Hermes' API in the lead's box, reached through a socket |
| 2222 | the dev box's sshd, behind its peer's 22 |
| a port of its own | Stage's and production's boxes on `127.0.0.1`, where their relays pass each address's requests |

**The server's, and `hi box` on any machine:**

| Port | What |
|------|------|
| 1080 | NetBird's SOCKS5 proxy in the server's peer: the server's way into its network, through a socket |
| 3128, 3129 | in a `hi box`: the proxy out, and the one that puts real tokens in place of placeholders ([hi box](/guide/box/)) |
| 3130 | in a box with `--data`: the data proxy |
| 8081 | a served model's front: the listener that checks keys |

## See also

- [The admin page: reaching it](/guide/app/admin/#reaching-it)
- [Managed compute: the server](/guide/compute/managed/)
- [The dev page: the app's addresses](/guide/app/dev/#a-name-for-each-address)
- [NetBird network](/guide/workstation/netbird/) and [Share a local service](/guide/workstation/expose/)
- [Serve a model](/guide/compute/serve/)
