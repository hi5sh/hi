---
title: Networking
description: Every port hi listens on, which service listens there, who connects, how they sign in, and the NetBird policies each one needs.
---

hi keeps everything it serves on [NetBird](/guide/workstation/netbird/),
never on a public address, with two exceptions that end on their own:
`hi net expose` and the server's address for cloud jobs. Nothing on this
page needs root: every port is a user service's, or a rootless
container's.

## The picture

```text
 devices (hi server join) ───TCP 7373────▶ hi server (hi-org.service)
                                                ▲
                                                │ the page calls the server for you
 admins, in a browser ────TCP 1984 / 443──▶ admin page (hi-admin.service)

 an app's people ─────────TCP 80, 8080────▶ ┐
 an app's owners ─────────TCP 8888, 22────▶ ┴ the app's NetBird peer, hi-<app>
                                                │
                                                ▼
                                            the app's boxes: no way out but hi's proxy

 cloud jobs (--data) ─────https───▶ netbird expose ───▶ server :7374, the data proxy only
```

## Ports

On ws1, a machine with apps in the NetBird network `corp.example`.

| Port | What listens | Where | Who connects | How they sign in |
|------|--------------|-------|--------------|------------------|
| **7373** | hi server, `hi-org.service` | the machine's NetBird address | devices that joined it, and `hi` on the machine | the device's key |
| **7374** | the server's data proxy for cloud jobs | the machine's NetBird address, published with `netbird expose` while a run needs it | rented machines in `hi compute run --data` and `up --data` | a run token, for that run's data |
| **1984** | admin page, `hi-admin.service` | its own peer `ws1-admin` (with podman), else the machine's NetBird address; always `127.0.0.1` too | the server's owner and admins | a link, then a browser cookie |
| **80** | admin page | its peer, for a name your IT team points at it (`hi server address`) | admins | as on 1984 |
| **443** | admin page over https | its peer, as `admin.<domain>`, with [`hi net domain`](/guide/workstation/netbird/) | admins | as on 1984 |
| **80** | an app's production | the app's peer `hi-<app>` | the app's people | the app's own |
| **8080** | an app's Stage (its preview) | the app's peer | the app's people | the app's own |
| **8888** | an app's dev page | the app's peer | the app's owners | the dev page's sign-in |
| **22** | SSH to the app's dev box (its sshd is on 2222 in the box) | the app's peer | the app's owners | their SSH key |
| **443** | an app's addresses over https | the app's peer, with `hi net domain` | as on 80, 8080 and 8888 | as there |
| **8080**, or the next free | a model from `hi serve llm --on local` | `127.0.0.1`; with `--share` or `--org` also the machine's NetBird address | you; with `--share` a teammate; with `--org` the server | none on `127.0.0.1`; a key on NetBird |
| any | `hi net expose <port>` | a public `https://…netbird.services` address, for a while; `--vpn`: the machine's NetBird address | whoever you give it to | a password, link, PIN, SSO, or none (`--public`) |

`hi net expose` refuses 1984: the admin page can do anything the account
that runs hi can do.

## The NetBird policies to ask for

The admin page's **Machine** section lists these with your names filled
in, ready to hand to your IT team:

- **The server:** from the server's people to the machine, TCP 7373.
- **The admin page:** from the admins' group to its peer (or the machine),
  TCP 1984, and 443 with a domain.
- **Each app**, its peer in the group `hi-<app>`:
  - from the app's people's group, TCP 80 and 8080;
  - from its owners' group, TCP 8888 and 22.
- **Cloud jobs:** no policy, but **Peer Expose** turned on once
  (Settings > Clients in the NetBird dashboard). `hi net expose` needs it
  too.

## Why the server and the page are apart

They are two services, so restarting the page doesn't drop the devices
connected to the server. Devices and people sign in differently: a device
with its key, a person with a link and a cookie. And their NetBird
policies differ: every device that joins reaches 7373, while only admins
reach 1984. The page calls the server for you; nobody opens 7373 in a
browser.

## Inside the machine

These are only on a box's own network or a socket, never on NetBird:

| Port | What |
|------|------|
| 3128 | hi's proxy, a box's only way out, to the hosts in its allowlist ([hi box](/guide/box/)) |
| 3129 | in a box: the proxy that puts real tokens in place of placeholders; in an app's box, the relay for its model calls |
| 3130 | in a box with `--data`: the data proxy |
| 8888 | Hindsight, an app lead's memory, on the app's own network |
| 8642 | Hermes' API in the lead's box, reached through a socket |
| 8081 | a served model's front: the listener that checks keys |
| 2222 | a dev box's sshd, behind its peer's 22 |

## See also

- [The admin page: reaching it](/guide/app/admin/#reaching-it)
- [Managed compute: the server](/guide/compute/managed/)
- [NetBird network](/guide/workstation/netbird/) and [Share a local service](/guide/workstation/expose/)
- [Serve a model](/guide/compute/serve/)
