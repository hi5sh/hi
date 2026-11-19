# `hi server net`: the server on a NetBird peer of its own

Status: Approved (2026-11-19). Built in v2.6.222, releases 1 to 3 at
once (see [As built](#as-built-v26222)).

Dependencies: [hi_server.md](hi_server.md) (the client API on
port 7373, device keys, signed calls, the server key),
[hi_server_expose.md](hi_server_expose.md) (the instance
listener and `netbird expose`), [hi_server_ai.md](hi_server_ai.md)
(models served on the team's machines), [hi_server_vault.md](hi_server_vault.md)
(team stores), [hi_server_apps.md](hi_server_apps.md) (a machine
reaches its server, never the other way round), [hi_app_internal.md](hi_app_internal.md)
and [hi_admin.md](hi_admin.md) (the peers apps and the admin
page already have), and [hi_net.md](hi_net.md) (the machine's
own enrollment).

## Goal

Today `hi server` listens on the machine's own NetBird address,
`100.x.y.z:7373`, written into `config.json` when the server is made. That
ties the server to the machine's enrollment:

- **Moving the server to another NetBird network moves the whole machine.**
  The machine leaves its network, with SSH and everything else that reached
  it there, and the server stops starting until someone edits `listen` in
  `config.json` by hand.
- **One set of access policies for both.** "Who may reach the server" and
  "who may reach this machine" are the same peer in NetBird's Access
  Control.
- **The address is a number.** Devices keep `http://100.x.y.z:7373` in
  their `org.json`. A new enrollment gives a new number, and every device
  has to join again.

The server gets a **NetBird peer of its own**, as each app and the admin
page have: a small rootless container, joined with a setup key of its own,
named `<machine>-server`. The server keeps running where it runs now, as
`hi-server.service` on the machine, under the machine's user. Only how it is
reached moves into the peer.

- **Moving the server to another network is one command,** and the machine
  stays where it is.
- **Devices follow a move by themselves,** told by the server and checked
  with its key, while the old peer still answers.
- **Nothing is pinned to an address.** The server listens on Unix sockets,
  and the peer passes its ports to them.

**hi server needs podman,** as an app does. `hi server init` installs it,
or says how, as for `hi app new`.

## What changes, and what doesn't

What reaches the server today, and how it does with a peer:

| Who | Today | With a peer |
|-----|-------|-------------|
| Devices: laptops, other machines, agents (`hi server join`) | the machine's NetBird address, :7373 | the peer's name, `<machine>-server.<netbird domain>`, :7373 |
| hi and the apps on the same machine | the machine's NetBird address, :7373 | `client.sock` in the server's folder, with no network in between |
| The admin page | `admin.sock` | unchanged |
| Cloud runs (`hi server expose`) | `netbird expose` of the instance listener on the machine | `netbird expose` inside the peer, of its port 7374 |

What the server reaches:

| What | Today | With a peer |
|------|-------|-------------|
| Slack, providers, Hugging Face, OpenRouter, template repositories on the internet | the machine's internet | unchanged |
| Models served on the team's machines (`hi q`) | their NetBird addresses, through the machine's `wt0` | through the peer, over its SOCKS5 proxy |
| A team store (OpenBao, Vault) on a NetBird address | through the machine's `wt0` | through the peer |

Unchanged: the vault and its key (TPM, key file, systemd's file
descriptor store), the state folder, the unit, the admin socket, `hi
update --restart`.

## How it works

### The peer

A container named `hi-server-peer`, made the way `hi-admin-peer` is
(`internal/cli/admin_reach.go`):

- hi's box image, `--cap-drop=ALL`, `--cap-add=NET_BIND_SERVICE`,
  `no-new-privileges`, 256 MB of memory;
- NetBird's userspace stack (`NB_USE_NETSTACK_MODE=true`,
  `NB_ENABLE_NETSTACK_LOCAL_FORWARDING=true`), so it needs no `wt0`, no
  root, and no device;
- the machine's `netbird` and `hi` binaries mounted read-only;
- its identity in `~/.local/state/hi/org/net/peer` (NetBird's
  `/var/lib/netbird`), kept when the container is made again;
- the setup key mounted only until it has joined, then deleted, as for
  the admin page.

What it runs:

```text
hi box __forward 127.0.0.1:7373 unix:/hi-server/client.sock &
hi box __forward 127.0.0.1:7374 unix:/hi-server/instance.sock &
hi box __forward unix:/hi-server/out.sock 127.0.0.1:1080 &
netbird service run &
netbird up --hostname <machine>-server --disable-dns \
  [--management-url URL] [--setup-key-file /run/netbird-key]
```

`/hi-server` is `~/.local/state/hi/org/net/sock` (mode `0700`). The server
listens on `client.sock` and `instance.sock` there. Rootless podman maps
the container's root to the machine's user, so the sockets belong to it,
and nobody else on the machine can open them.

`hi box __forward` learns to listen on a Unix socket as well as on TCP,
for `out.sock`.

The container is labelled with a stamp of what it runs, so `hi update`
makes it again only when that changes, as the admin page's peer is.
Restarting the server doesn't touch the peer: a device's connection waits
the few seconds the server takes to restart, as it does now.

### Into the server

The peer's ports 7373 and 7374 lead to the server's sockets. The server's
API, its signed calls, and its checks are the same. Only the listener is
different.

**hi and the apps on the same machine** use `client.sock`. `hi server
join` on the server's own machine, and `hi server init` joining it,
write `"url": "unix:~/.local/state/hi/org/net/sock/client.sock"` into
`org.json`. The client dials the socket, and every call is still signed
with the device key. This is how the machine keeps reaching its own server
whatever network the peer is on. It also no longer needs a NetBird policy
from itself to itself.

### Out of the server

The peer's NetBird has a SOCKS5 proxy in userspace mode
(`NB_SOCKS5_LISTENER_ADDRESS=127.0.0.1`, `NB_SOCKS5_LISTENER_PORT=1080`).
The server reaches it through `out.sock`.

The server dials through the peer when the target is on the peer's
network: an address in its NetBird range, or a name under its NetBird
domain, both read from `netbird status --json` in the peer. Everything
else goes direct, from the machine. One dialer does this for every HTTP
client the server makes (served models, team stores, served model
checks), so no caller has to choose.

Names under the NetBird domain: the peer runs with `--disable-dns`, as the
admin page's does, so the machine's resolver isn't changed. If NetBird's
SOCKS5 proxy doesn't resolve NetBird names then, hi resolves them itself
from the peers that `netbird status --json` lists. See Findings to check.

### Cloud runs

`hi server expose` runs `netbird expose 7374 --with-name-prefix hi-…` with
`podman exec` in the peer instead of on the machine. The exposure is the
peer's, so it is on the peer's network and account, and ends when the
peer does. The run tokens, the routes, and the lifetime don't change.

### Health

The server's check every half minute (the one behind Incidents) looks at
the peer:

- not running: made again, and started;
- running, not on its network for more than two minutes: an incident,
  "The server's NetBird peer is off its network", in Incidents and in
  the ops channel, with what `netbird status` says (the setup key
  expired, the management server away, the peer removed in NetBird's
  dashboard).

## Moving to another network

```sh
hi server net                         # the peer: its name, address, network, state
hi server net join [--setup-key-file F] [--management-url URL] [--name N]
hi server net forget-old              # stop answering on the old network now
```

The setup key is asked for hidden, or read from a file only the user can
read, or from `HI_NETBIRD_SETUP_KEY`, as for `hi net`. Without
`--management-url`, the peer joins the network it is on now, or NetBird's
hosted one for a first join.

`join` on a server that has a peer is a **move**:

1. A second peer, `hi-server-peer-new`, joins the new network, with
   identity of its own. Joining fails? Nothing else has changed, and the
   command says why.
2. The server answers on both. The old peer becomes `hi-server-peer-old`.
3. **Devices are told.** Each device's open connection, or its next call,
   gets a notice, signed with the server key: "the server is at
   `http://<new name>:7373` now". The device calls the new address. If it
   answers with the same server key (pinned in `org.json` since the first
   join), the device writes the new URL. A device that can't reach the new
   address keeps the old one and says so on its next `hi server status`,
   with the NetBird policy to ask the IT team for.
4. **The old peer goes** once no device has called it for a day, after 14
   days whatever happens, or at `hi server net forget-old`. `hi server
   net` says which devices still use it, by user and device.

Devices that were off for the whole move join again with `hi server join
<new address>`. Their device key is known and approved already, so they
are back at once, with their user and group.

**The admin page moves with the server when it is reached through the
machine's NetBird address, or when it has a peer and the machine runs only
the server.** Its peer then moves at the same step, and its old one goes
with the server's. On a machine with apps, the page's peer stays, since the
apps' people reach the page there. `--page` and `--no-page` choose.

## A server from before

Servers made before this keep listening on the machine's NetBird address
(`listen` in `config.json`) and work as now. `hi server net join` gives one
a peer:

- the server answers on both: the old `listen`, and the peer;
- devices are told, as in a move, and `hi server net forget-old` stops the
  old `listen` (it is dropped from `config.json`);
- `hi update` doesn't do it by itself: a peer needs a setup key, which an
  admin gets from NetBird's dashboard.

`hi server init` always makes a peer from the release this ships in.

## The admin page

The server's **Overview** has a **Network** card for the owner:

- the peer's name and address, its network (the management server's
  host), and whether it's on it;
- the devices still on the old address during a move, and **Forget the
  old network**;
- **Move to another network…**: the setup key, the management URL
  (optional), and whether the admin page moves too. It runs `hi server
  net join` and shows where the devices are as they follow.
- The access policies to ask for: devices to the peer on TCP 7373; the
  peer to the team's machines on the ports their models are served on.
  These are said as policies to ask the IT team for, never as something
  the page does.

## Security

- **The setup key** never lands on disk longer than the join takes, nor in
  output or history (as `hi net`).
- **The sockets** are in a `0700` folder of the machine's user. Anyone who
  can open them is already that user, who can open `admin.sock`.
- **`out.sock` is a way into the peer's network** for whoever can open it:
  the same user. hi never listens on TCP for it on the machine, so other
  users on the machine can't use it.
- **A move can't be faked.** Devices take a new address only in a notice
  signed with the server key, and only once the new address answers with
  that same key.
- **The peer can't reach the machine's own services** beyond the sockets
  mounted into it: NetBird's userspace stack forwards to the container's
  loopback, where only hi's forwards listen.

## Considered and rejected

- **Following the machine's NetBird address instead of a pinned one.**
  This mends only the hand edit: a move still takes the machine off its
  network, and the policies stay shared.
- **The server inside the container.** The vault's key (TPM, systemd's
  file descriptor store), the provider and Slack connections, and `hi
  update` all assume the server is a service on the machine. Only the
  network needs to move.
- **The peer in the same network as the machine, always.** That avoids the
  SOCKS5 path, but it's exactly the move people ask for that it rules out.
- **A second NetBird profile on the machine** (`netbird profile`). Only one
  profile is active at a time, so the machine leaves its network all the
  same.

## Releases

1. **The peer.** `hi server init` makes one. `hi server net` and `hi server
   net join` on a server with none give it one. `client.sock` for the
   machine's own hi and apps, the dialer through `out.sock`, and `netbird
   expose` in the peer. Servers from before keep `listen` beside it.
2. **Moving.** `join` on a server that has a peer, devices told with
   signed notices, the old peer kept and forgotten, a known device key
   joining again at once, and the admin page's peer moving with it.
3. **The admin page's Network card**, and the incident when the peer is
   off its network.

Related, small, and separate: `hi net --management-url` for the machine's
own enrollment, which `hi net` can't give today.

## Tests

- The peer's script: the forwards, `--management-url` only when given, the
  setup key only before the first join; the stamp changes when they do.
- The server answers on `client.sock` and `instance.sock` with the same
  signed-call checks as on TCP. A client with a `unix:` URL calls it.
- The dialer: an address in the peer's range and a name under its domain go
  through `out.sock`; anything else goes direct; with the peer down, calls
  into its network fail with a message that says so.
- A move: notices are signed; a device takes the new URL only when it
  answers with the pinned server key, and keeps the old one otherwise; the
  old peer goes after a day of no calls, after 14 days, and at
  `forget-old`.
- A known device key joining again is approved at once, with its user and
  group.
- A server from before answers on `listen` and the peer, and `forget-old`
  drops `listen` from `config.json`.
- On a test machine, end to end: a device joins through the peer; `hi q`
  reaches a model served on another machine through it; a cloud run
  reaches the instance listener through `netbird expose` in the peer; the
  server moves to a second network, and a device follows.

## Findings to check before building

On a test machine, with NetBird 0.82:

1. **NetBird's SOCKS5 proxy in userspace mode**: that it runs with
   `NB_SOCKS5_LISTENER_*`, reaches other peers, and whether it resolves
   NetBird names with `--disable-dns`.
2. **`netbird expose` from a userspace peer**: that it publishes a port
   on the container's loopback.
3. **Two peers on one machine in the same network** (the machine and the
   server's peer): that devices and the machine reach the peer directly,
   not through a relay.
4. **What the peer costs**: memory and CPU at rest, and with a few dozen
   devices connected.

## Open questions

1. **Template sources and team stores on NetBird addresses** reached with
   `git` or another program, not hi's own HTTP clients: `git` takes a
   SOCKS5 proxy only on TCP. Either hi passes such a `git` a short-lived
   forward of its own, or those sources stay on the machine's network.
2. **The peer's name.** `<machine>-server` beside `<machine>-admin`, or a
   name the owner gives at `hi server init`, for an IT team that keeps
   names such as `hi.corp.example`?
3. **A server on a machine with no podman** (a small VM with only the
   server): is installing podman for it acceptable, or should such a
   server keep the machine's address as today?
4. **Devices during a move** that are on the old network only: is a day
   of no calls the right time to forget the old peer, or should it wait
   for an admin every time?

## Not in this

- The server reachable from more than one NetBird network at once, for
  good. A move keeps two for a while; more is for later.
- Apps' boxes reaching the server directly. As far as the code shows, they
  don't: box code reads no `org.json`, and what reaches the server goes
  through the app's broker on the machine, which uses `client.sock` like
  the rest of the machine's hi. Release 1 checks this, and lists anything
  in a box that calls the server's address.

## As built (v2.6.222)

All three releases, with these differences from the draft:

- **The machine's own hi reaches the server at `127.0.0.1:7373`**, not
  `client.sock`. hi hands the server's address to other programs (hi
  data's `HF_ENDPOINT`, the AI endpoint `hi q` writes into agents'
  settings), which can't open a socket. A loopback listener is no wider
  than NetBird: every call is still signed with an approved device key.
  `local_listen` in `config.json` moves it, or `"off"`. `hi server net
  join` points the machine's `org.json` there when it was joined at the
  machine's NetBird address, and `app_move` tells the same server by its
  key, since its own machine reaches it at another address.
- **A socket per peer**, `client-<id>.sock`, so the server knows the
  address a call came in at: an old peer's, or the machine's from before,
  get the notice; the current peer and `127.0.0.1` don't. Peers are
  `hi-server-peer-<id>`, in `net/peer-<id>/`, with `peer` and `old_peer`
  in `config.json`; hi changes only those keys there, and keeps what an
  admin wrote.
- **`hi server init`** gives the server a peer when it has a setup key
  (`--setup-key-file`, `HI_NETBIRD_SETUP_KEY`, or asked at a terminal
  where Podman and NetBird are), with `--management-url` and
  `--peer-name`; Enter, or no terminal, listens on the machine's NetBird
  address as before. Run again, it keeps the peer. So open question 3 is
  answered: a machine without Podman keeps the machine's address.
- **The notice and the proof.** Answers at an old address carry
  `Hi-Server-Moved` and `Hi-Server-Moved-Signature` (the server key over
  `hi-server-moved\n<url>`). A device checks it with the key it pinned,
  then calls the new address with a random `Hi-Server-Challenge`, which
  must come back signed (`Hi-Server-Proof`). A device that can't reach the
  new address yet says so once, with the policy to ask for. `hi server
  join <new address>` takes the same proof, and keeps the device's user.
- **The old peer** goes as drafted (a quiet day after the move, 14 days,
  or `forget-old`); a restart counts as a call, so it waits a day after
  one. Open question 4 stays as drafted.
- **Not built:** the admin page's own peer moving with the server
  (`--page`): the page keeps its own reach (`hi server init --netbird`).
  Template sources and team stores reached with `git` on a NetBird
  address (open question 1) still go through the machine's network.
- **The peer's name** is `<machine>-server`; `--name` (or `--peer-name`
  at init) gives another (open question 2).

Findings, on NetBird 0.82 with the admin page's peer:

1. **SOCKS5:** userspace mode runs it on `127.0.0.1:1080` by default; the
   peer sets `NB_SOCKS5_LISTENER_PORT=1080` anyway. Traffic through it
   shows in the peer's own WireGuard counters for the target, and a
   direct call from the container doesn't: it goes out through the
   peer's network. With `--disable-dns` it doesn't resolve NetBird names,
   so hi resolves them from the peers `netbird status --json` lists.
2. **`podman exec` doesn't pass a signal on**: SIGINT ends the client and
   leaves the process in the container running. hi stops `netbird
   expose` in the peer with `pkill -INT` there. Whether `netbird expose`
   works from a userspace peer is checked on the first real server
   (below).
3. **Two peers on one machine** in one network reach each other directly
   (the admin page's peer and the machine: P2P, host candidates).

Checked end to end on a test machine: not yet. The first server with a
peer checks a device joining through it, `hi q` reaching a served model
through it, a cloud run through `netbird expose` in it, and a move.

