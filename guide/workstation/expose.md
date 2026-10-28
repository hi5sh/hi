---
title: Share a local service
description: Put a web app, webhook receiver, or API running on your machine on a temporary public HTTPS address with hi net expose, protected by a password and ending on its own.
---

`hi net expose` puts a service running on your machine on a public HTTPS
address for a while: a web app to show a colleague, a webhook receiver for
a test, a notebook, or an API for your phone. It ends on its own.

```sh
hi net expose 3000
```

```text
Exposing localhost:3000 at https://hi-gpu1-3000-k2x7.eu1.netbird.services
  password: plum-otter-raven-42 (a login page asks for it)
  until 16:30; Ctrl+C stops it sooner
GET    /                                        200  12 ms
POST   /api/order                               201  48 ms
```

It uses NetBird's reverse proxy, so the machine must be on
[NetBird](/guide/workstation/netbird/), and a NetBird admin must have turned
on **Peer Expose** once (Settings > Clients in the NetBird dashboard).

[See it in action](/net/): a short demo, then how it works.

{% include diagram.html name="expose" %}

## Who gets in

By default, a password page sits in front, with a password hi makes up and
prints. Choose another lock, or none:

```sh
hi net expose 3000                      # a generated password
hi net expose 3000 --password <text>    # your own password
hi net expose 3000 --link               # the key is in the link: no login page
hi net expose 3000 --pin 482913         # a 6-digit PIN
hi net expose 3000 --groups devops      # NetBird users in these groups, through SSO
hi net expose 8080 --public             # anyone with the address
```

With `--link`, hi prints one link with a key in it. Whoever opens the whole
link gets in, with no password to type: hi's relay keeps the key in a
browser cookie until the address ends, and moves the browser on to the
address without it. Anyone with the bare address gets a page asking for the
link. Scripts can send the key as an `Authorization: Bearer <key>` header.
Treat the link like a password: anyone you forward it to gets in.

Password, PIN, and SSO are login pages for people in a browser. Webhook
senders, API clients, and scripts can't pass them, so they need `--public`;
the service should then check its own token or signature, as webhook
receivers normally do.

## Only from some addresses, or only on the VPN

```sh
hi net expose 3000 --allow-ip 203.0.113.18          # only from the office
hi net expose 8080 --public --allow-ip 203.0.113.0/24 # a webhook sender's addresses
hi net expose 3000 --vpn                              # no public address at all
```

`--allow-ip` takes addresses and CIDR blocks, separated by commas, and goes
with any lock above. NetBird's proxy turns every other address away before
its login page, as a rule under Reverse Proxy > Access Control does.
`netbird expose` can't set that rule, so hi sets it through NetBird's API,
with the same token as `--domain` (in `$NETBIRD_API_TOKEN` or saved by
`hi team domain`). The address lets nobody in until NetBird has the rule,
and hi checks it every minute: if it has gone or differs, hi puts back
exactly the addresses it was given, and lets nobody in until it can. An
address added under Access Control by hand is taken out again, so give
every address to `--allow-ip` (or `hi team allow-ip`).

`--vpn` gives no public address. The service is reachable at
`http://<this machine's NetBird name>:<port>`, only from devices on your
NetBird network that NetBird's access policies let reach this machine. Add
`--link` for a key as well, or `--allow-ip` with NetBird addresses
(`100.x.y.z`) to narrow it to some devices. It needs no API token. A
visitor's device must be connected to NetBird: a phone without the NetBird
app gets nothing.

## How long

```sh
hi net expose 3000 --max 30m    # default 1h, at most 24h
```

At `--max`, or on Ctrl+C, the address stops working within seconds.

## An address that stays

```sh
hi net expose 3000 --link --domain app.apps.example.com --max 720h
```

`netbird expose` makes up each address. `--domain` puts the service on a
name you choose instead, under a domain verified in NetBird (Reverse Proxy
> Custom Domains, with a CNAME for `*.<domain>`), for up to 30 days. hi
makes it a NetBird service through NetBird's API, with a token from a
service user that may manage the reverse proxy, in `$NETBIRD_API_TOKEN` or
saved by `hi team domain`. When the exposure ends, the service is switched
off rather than removed, so the next exposure on the name takes it over
with its certificate.

hi looks for the service every minute while the exposure runs. One deleted
in NetBird's dashboard is made again, pointed at the same relay, with the
same lock and `--allow-ip` rule, within a minute; a new certificate may
take a little longer. A service another exposure has taken over on the
name is that one's to keep, so an older exposure never takes the name back.

## In the background

```sh
hi net expose 3000 --detach          # prints the address and returns
hi net expose ls                     # what is exposed, with passwords and end times
hi net expose stop hi-gpu1-3000-k2x7
```

The request log of a background exposure goes to a file under
`~/.local/state/hi/net/expose/`, which `--detach` names. `--json` prints
the address, protection, and end time for scripts and agents.

`--name <prefix>` sets the start of the address instead of
`hi-<host>-<port>`. With `--link`, `--key <key>` reuses a key you already
gave out, so a new exposure on the same `--domain` keeps the old links
working.

> `hi net expose stop --all` stops every exposure your user runs,
> including the ones [hi team](/guide/team/app/) runs for its apps'
> production, preview, and dev page. On a machine with teams, stop
> exposures by name.
{: .warning}

## Services on localhost

Most development servers listen only on `localhost`, which NetBird's proxy
can't reach: it comes in over the VPN, at the machine's NetBird address.
`hi net expose` runs a small relay on that address that passes requests to
`localhost:<port>`, keeping the original `Host` header and WebSockets.
`--host` points the relay elsewhere, such as a container's address.
`--follow <file>` relays to the port written in a file instead, read again
whenever the file changes, so a service can move to a new port, such as a
new container, without the address changing.

## What to keep in mind

- Whatever the service lets people do, anyone past the lock can do.
  Development servers often have debug pages and no sign-in.
- NetBird's proxy sees the traffic, since it handles the HTTPS.
- Other machines in the NetBird network can reach the service through the
  relay while it runs.
- On a machine connected to a [hi server](/guide/compute/managed/), each
  exposure is recorded in the server's audit log and live feed.
- NetBird's reverse proxy is in beta.
