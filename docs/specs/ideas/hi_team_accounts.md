# A Linux account for each team

Status: Draft (2026-10-28)

Dependencies: [hi_team.md](../approved/hi_team.md) (teams, their service,
the lead's box, the broker), [hi_box.md](../approved/hi_box.md) (boxes,
rootless Podman, `--userns=keep-id`), [hi_watch.md](../approved/hi_watch.md)
(heartbeats, the device key), and the roadmap's "a box's escape reaches
only its own team".

## Goal

Colleagues pentested a team on 2026-10-27. They got the lead to run
`cat /etc/passwd`, which it may: the box is the boundary. Then they asked
what the box's user is on the host. The answer was the problem: every box
on a machine runs as the account that runs hi.

Rootless Podman maps the box's `box` (uid 1000) to the host's uid 1000 on
purpose (`--userns=keep-id`), so files an agent writes in `/box/home` belong
to `hi` on the host. That account also owns:

- every team's folder: its `secrets.json` (the real Slack tokens), its
  `run/keys` (the OpenRouter key the proxy swaps in), the lead's memory;
- hi's config, with the machine's device key for the hi server;
- the machine's sign-ins (Claude, ChatGPT) the teams' proxies use;
- every team's containers, in one Podman store;
- whatever else the admin keeps there (on gpu1, a repository's `.env` with
  another server's SSH password).

So an escape from one team's box is not that team's problem alone. It
reads every other team's tokens, `podman exec`s into their boxes, and speaks to the hi
server as the machine. The walls of each box are good (no capabilities,
`no-new-privileges`, limits, a proxy for every connection), but there is
one wall, and behind it is everything.

v2.6.12 took away the worst of it on gpu1: `hi` was in the `docker` and
`lxd` groups, either of which is root without a password, and `hi team up`
now warns about them. This spec takes away the rest:

- **An escape lands as the team's own account.** It holds what that team
  holds and nothing of another team's, hi's, or the admin's.
- **Nothing changes for the people in Slack.** The lead, the apps, the dev
  box, the addresses: all the same.
- **Admins keep their commands.** `hi team ... demo` works as now, from
  the admin's account.

Out of reach for any of this: a kernel exploit is root from any account.
This is for the common case, a bug in Podman or crun, or a mount hi got
wrong, which leaves the attacker as the box's user on the host.

## What was tried first, and why not

The smaller idea was to keep the service as `hi` and give each box a user
of its own: `--userns=auto`, with the shared folders mounted `:idmap` so
the box's files still belong to `hi` on disk. Tried on gpu1 (Ubuntu 26.04,
kernel 7.0, Podman 5.7, crun, ext4) on 2026-10-28:

| Try | Result |
|---|---|
| `--userns=auto` | "not enough unused IDs": the keep-id boxes already use all of `hi`'s 65,536 sub-ids |
| `--uidmap 0:1:65536` (box 1000 → host 101000) | works: an escape would own nothing |
| the same, with an `:idmap` mount | "mount_setattr: Operation not permitted": rootless Podman can't make idmapped mounts |
| the same, a plain mount | the box can't write `hi`'s files; `hi` can't read a file the box made 0600, or write one it made 0644 |

POSIX ACLs don't save it: a file made 0600 has an ACL mask of nothing, and
the lead's Hermes writes its `auth.json` and `.env` that way. Making a
sub-id user share folders with `hi` would mean changing how every mount
is shared. A team's own account needs none of that: keep-id stays, and
maps to an account that owns the team's folder.

## The design

### The account

`hi team new demo` (and moving a team, below) makes a system account,
`hi-demo`:

- no password and no login shell; not in `sudo`, `docker`, `lxd`, or any
  other group but its own (and `render` and `video` if the team ever
  needs the GPU);
- 65,536 sub-ids of its own in `/etc/subuid` and `/etc/subgid`
  (`useradd --system` gives none; `usermod --add-subuids`);
- lingering on (`loginctl enable-linger hi-demo`), so its services run
  without a login and start at boot;
- its home, `/var/lib/hi/teams/demo`, mode 0700, which is the team's
  folder. Team folders can't stay under the admin's home: `/home/hi` is
  0750, and a team account must not be let in.

Making an account needs root, so `hi team new` and moving a team ask for
sudo, once, as `hi install` does. Everything after runs without it.

### What runs as the team

As `hi-demo`, in its user services and its own Podman store: the team's
service (`hi team __serve demo`), its proxy, resolver, and broker; the
lead's box, the preview and production, the memory, the dev box and its
peer, the members' boxes. Its base image is built in its own store.

keep-id maps `box` to `hi-demo`, so the box's files belong to the team's
account, as they belong to `hi` now. Nothing about mounts changes.

### What stays with the admin

- **hi itself.** `/usr/local/bin/hi`, readable by every account, instead
  of `~/.local/bin/hi`. `hi update` writes it with sudo, then restarts the
  teams.
- **The machine's sign-ins and keys.** The teams' proxies swap
  placeholders for keys today, reading them from the team's `run/keys`
  and from the machine's sign-ins in the admin's home. The team's own
  keys move into its folder; the machine's stay with the admin, served
  by an admin-side broker on a socket each team may ask (only for the
  calls its proxy makes, never the key itself). Which of the machine's
  sign-ins each team may use is the admin's to set, as now.
- **The hi server.** Each team gets a device key of its own, registered by
  the admin's `hi team new`, so a heartbeat speaks for that team only. The
  machine's key stays the admin's. The server shows which key sent what.
- **NetBird and `hi net expose`.** The relays stay the admin's; they reach
  the apps on `127.0.0.1` ports as now.

### The admin's commands

`hi team status demo`, `hi team env demo KEY`, `hi team rebuild demo` and
the rest run as `hi-demo`: hi re-runs itself with `sudo -u hi-demo`, so the
team's folder never needs to be readable by the admin's account. On a
machine where the admin has no sudo, nobody has made a team account
either, and teams stay as they are.

`hi team ls` lists every team, whatever account it runs as.

### Moving a team

`hi team up demo` on a team that runs as the admin's account says so, and
moves it with `--move-to-account` (asked in a terminal):

1. Make `hi-demo` (above).
2. Stop the team's service and its boxes; take a snapshot.
3. Move the folder to `/var/lib/hi/teams/demo` and give it to `hi-demo`
   (`chown -R`, and `podman unshare chown` for what a box wrote as
   another uid).
4. Build the base image in `hi-demo`'s store; install its user units.
5. Start it. The addresses stay the same; the links people have work.

If any step fails, the snapshot and the folder's old place put it back.
Teams not moved keep working as they do now, with the warning v2.6.12
added and one more: that an escape from their boxes reaches the others.

## What it costs

- A base image per team account: about 850 MB and three or four minutes
  each, built when the team starts. A read-only shared image store
  (`additionalimagestores`) might avoid it; see the questions.
- sudo, once per team, and for `hi update`.
- A broker on the admin's side for the machine's sign-ins.

## Tests

- **The escape.** As `hi-demo` on the host, the user an escape from
  demo's box would be: reading another team's folder, `secrets.json`, the
  admin's home and hi's config fails; `podman ps` shows only demo's
  boxes; another team's broker socket refuses; the hi server refuses demo's
  key for anything of another team's.
- **Nothing changed for people.** A team moved mid-life keeps its
  addresses, its links, its data, its lead's memory, and its dev box's
  SSH host key.
- **Admin commands.** Every `hi team` command run from the admin's account
  works on a moved team.
- **Unmoved teams.** A machine with one moved team and one not runs both.

## Questions

- Can a rootless account use another account's read-only image store, so
  the base image is built once? Overlay with idmapped layers may allow it
  on kernels from 5.19; to try.
- Does Hermes, or anything the lead runs, need the machine's sign-ins as
  files rather than through the proxy? Then those files have to be the
  team's copy, refreshed by the admin's side.
- The dev box's agents (Claude Code, Codex, herdr) come from the host,
  mounted read-only from the admin's home today. They move somewhere
  every account can read, as hi does.
- Is a group per team (the admin in `hi-demo`) simpler than `sudo -u`
  for the admin's commands? Not for files made 0600, which is why this
  draft says `sudo -u`.
