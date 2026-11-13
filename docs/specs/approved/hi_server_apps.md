# `hi server` again: the server apps join, and one admin page for its machines

Status: Approved (2026-11-13). Release 1 built as v2.6.147, release 2 as v2.6.148. Decided the same day: every app needs a server, existing ones too (no grace: hi is on
a few machines of ours, two with apps, and no one else's); the server's
admins can make themselves a machine's owner, on demand, never on joining;
`hi admin` is retired and removed, with no alias, and a machine has no admin
page of its own.

Dependencies: [hi_org.md](hi_org.md) (the rename this one
turns back, and why), [hi_server.md](hi_server.md) (the
server, device keys, signed calls), [hi_admin.md](hi_admin.md)
(the admin page, a machine that runs both, the two lists of admins),
[hi_app.md](hi_app.md), and three drafts that build on it:
[hi_cluster.md](../ideas/hi_cluster.md) (`hi org machines`),
[hi_app_move.md](../ideas/hi_app_move.md) (moving an app between machines), and
[hi_app_vault.md](../ideas/hi_app_vault.md) (an app's settings from Vault,
release 3).

## Goal

A company's network has several machines with hi, each serving several
apps, and one hi server they join. Three changes, in this order:

- **`hi server` is the name again.** `hi org` keeps working, without a
  warning.
- **An app needs a server.** `hi app new` asks for one first. A machine
  can run the server itself.
- **One admin page for every machine.** The server shows each machine and
  its apps, so an admin opens one page, not one per machine. A machine
  has no page of its own, and `hi admin` goes.

## Why rename back

[hi_org.md](hi_org.md) took `hi org` because `hi server`
named how it runs rather than what it holds. In use, "org" says too
much:

- **It sounds like the one thing that defines the company.** It isn't. It
  is a server some machines and people join, and a company can run more
  than one: a test server next to the real one, or one per department.
  Two "orgs" in one company reads as a mistake; two servers doesn't.
- **What it does is a server's job.** It approves and pays for compute,
  holds the shared keys, templates, data, and models, and, with this
  draft, keeps the machines and their apps in one place.

The reason we left `hi server` still holds: it reads like `hi serve`,
which serves models. We keep both names and make them hard to mix up
(see [`hi serve` and `hi server`](#hi-serve-and-hi-server)).

## The rename

Only names change. No state moves and nothing on the wire changes, as in
hi_org.md, so old and new clients and servers mix freely.

```text
hi server join <server> [--user NAME] [--no-wait]   # was hi org join
hi server status | key | leave | slack link <code>
hi server init | run | provider | user | requests | approve | deny | ls | stop | audit
hi server slack setup | manifest
hi server approvers | policy | spend | ai | viewer | wall
hi server templates | data | expose | live
hi server offer | machines                          # hi_cluster.md
```

- **`hi org …` stays,** silently, for as long as scripts and fingers
  type it, and `hi connect` and `hi disconnect` too. They run the same
  code. The hint `oldNameHint` prints on a terminal now says "is now
  hi server …", and `hi org` gets no hint at all.
- **Files keep their names.** `~/.config/hi/org.json`, the state in
  `~/.local/state/hi/org`, and the unit `hi-org.service` stay: they
  moved once already, in v2.6.57, and moving them back gains nothing.
- **Code keeps its names** (`org.go`, `runOrg`, `people.json`), as the
  [app rename](hi_app.md) left its plumbing.
- **Prose:** "the server", or "your hi server" where it could be
  confused with another server. "Org" leaves help, the guide, the admin
  page, and what Slack hears: the admin page's org card becomes the
  **server's** card, and its Org section becomes **Server**.
- **The guide:** `site/guide/org/` becomes `site/guide/server/`, and the
  old address redirects.
- **The drafts that say `hi org`** (hi_cluster.md, hi_app_move.md,
  hi_serve_recipes.md, …) change when they are next built on, not now.

## `hi serve` and `hi server`

Both stay. They're kept apart in two ways:

1. **They mean different things, and are typed differently.** `hi serve`
   always takes a kind (`hi serve llm <recipe>`). `hi server` is mostly
   typed once, at setup, and then lives on the admin page.
2. **Help puts them apart, one line each,** saying what each is for and
   that the other exists:

```text
  hi serve llm <recipe>         Serve a model with an OpenAI-compatible API, rented or on this machine
                                (hi serve help; not hi server)
  …
  hi server                     The hi server your machines and people join: approvals and spend, shared
                                keys, templates and data (hi server help)
```

As built in release 1. "And the admin page for every machine" joins the
`hi server` line with release 3, and "or the server's machines" the `hi
serve` line with hi_serve.md's release 5.

`hi serve` stays where it is in help, with compute. `hi server` moves
down, beside `hi app`, which now depends on it, and takes `hi admin`'s
line.

A slip between the two fails loudly and says what was meant: `hi serve
join …` answers "hi serve serves models; did you mean hi server join?",
and `hi server llm …` answers the other way round. Neither guesses and
runs.

## An app needs a server

Today an app runs on a machine with or without a server. Most machines
with apps are joined to one already, for compute. From this draft on:

- **`hi app new`** on a machine joined to no server stops and says how
  to get one, as a [machine action](hi_admin.md) the admin
  runs:

  ```
  hi app new needs a hi server. Either:
    hi server join <server>   join your company's (ask your IT team for its address)
    hi server init            run one on this machine (fine for a small team or to try hi)
  ```

  The server's admin page has no machine to offer **New app** on until
  the machine has joined, so there it's the command line.
- **Apps that exist already need one too.** hi is on a few machines of
  ours and no one else's, so there is no grace period and no notice to
  keep up: the machines with apps join a server before the release that
  checks it, and from that release `hi app up`, the broker, and `hi
  update` on a machine with apps and no server stop with the same
  message. Apps already running aren't stopped; they can't start again
  until the machine joins.
- **A machine joined to a second, test server** stays joined to that
  one: it is the server its apps use, and its page shows them.
- **A machine that runs both** is the small case, as in
  [hi_admin.md](hi_admin.md#a-machine-that-runs-both). The
  guide keeps saying a server for more than a few people goes on a small
  VM of its own.

**What an app gets from its server:**

- the one admin page, below;
- its settings from the server's Vault ([hi_app_vault.md](../ideas/hi_app_vault.md),
  release 3);
- moves between the server's machines without exchanging move keys by
  hand ([hi_app_move.md](../ideas/hi_app_move.md));
- compute, approvals, and spend, as now.

**When the server is away,** an app keeps running. Its boxes already
have what they started with, and its broker starts them again from what
the machine has (`secrets.json`, the Vault cache). What waits for the
server: `hi app new`, a move, a setting from Vault that was never read,
and the admin page. No machine has a page of its own to fall back on:
an admin who must act while the server is down uses SSH and `hi app …`
on the machine, as before the admin page. With a few machines and a
server that's away for seconds on an update, that's the trade.

## One admin page for every machine

The server's admin page ([hi_admin.md](hi_admin.md), on its
usual address) gains the machines joined to it.

```
┌──────────────────────────────────────────────────────────────────────┐
│ ☰  hi.corp.example                                      Requests² ✕ │
├──────────────────────────────────────────────────────────────────────┤
│ Server   ● up   v2.6.150   2 requests waiting   today $14            │
│                                                                      │
│ ws1      ● up   3 apps     CPU 22%  memory 41/128G                   │
│   demo       ● up   production v12                                   │
│   payments   ● up   production v31                                   │
│   wiki       ○ stopped                                               │
│ ws2      ● up   1 app      CPU 64%  memory 88/128G                   │
│   reports    ● up   production v4                                    │
│ gpu1     ✕ not heard from for 6 min                                  │
└──────────────────────────────────────────────────────────────────────┘
```

- **Home** is the server's card, then each machine with its apps' cards.
  An app opened has the sections it has today, on whichever machine it
  runs.
- **A machine opened** has the views a machine's own page has now:
  People, Keys, Network, Machine, Activity.
- **A machine has no page of its own.** Its People, Keys, Network,
  Machine, Activity, its apps' sections, and their terminals are only on
  the server's page.

## `hi admin` goes

The admin page stays, as the server's, under its name. The command
that ran it on each machine goes, with no alias: hi is on a few
machines of ours and no one else's, so nothing else types it.

| Was | Is |
|---|---|
| `hi admin` (whether it runs, its address, its admins) | `hi server status` |
| `hi admin start` / `stop` | Gone. The page runs with the server (`hi server init`, `hi-org.service`). On a machine, the process that keeps the connection to the server starts by itself on `hi server join`, and with the apps' service after a reboot. |
| `hi admin link [<admin>]` | `hi server link [<admin>]` |
| `hi admin admins` | `hi server admins` for the server's; `hi server admins --machine ws1` for a machine's, which the machine still keeps and checks |
| `hi admin address` | `hi server address` |
| `hi admin owner <name>` | `hi server owner <name> --machine ws1`, or **Make me the owner** on the page |
| `hi admin remote off` (this draft) | `hi server remote off`, run on the machine: the one setting the machine keeps for itself |

`hi admin …` after the release answers "hi admin is gone: the admin
page is the server's (hi server status)", and runs nothing.

**On our machines,** the release that removes it stops and removes each
machine's page service, and its NetBird peer `<machine>-admin`, on `hi
update`, and says so. A machine's `admin.json` (its admins and owner)
stays: it is the list the machine checks actions against.

### How the server reaches a machine

The machine reaches the server, never the other way round. A machine
can be behind NAT or a firewall the server can't get through, and the
server shouldn't need a way into every machine.

- Each joined machine's hi keeps one connection open to the server, signed with its device key, as `hi
  server join --agent` machines do now.
- Up it, the machine sends what its page shows: its state, its apps,
  their boxes. Down it come the actions the server's page asks for, and
  the terminal's stream.
- The machine checks each action against its own list of admins before
  it runs it, so the server can't do on a machine what that machine's
  admins couldn't.

### Who can do what

The decision in [hi_admin.md](hi_admin.md#two-lists-of-admins)
holds: running apps and approving spend are different jobs.

- **A machine's admins** sign in once, to the server's page, and run
  their machines and those machines' apps. An admin of ws1 and not of
  ws2 sees ws2 and changes nothing there.
- **The server's owner and admins** approve, set the policy, and keep
  the server's users and keys. They see every machine and app, and change
  one only if they are its machine's admin too.
- **Joining a machine to a server** adds no one to its admins.
- **The server's owner and admins can make themselves a machine's
  owner,** on demand: **Make me the owner of ws1** on the machine's view,
  as an admin with no level in an app gets **Make me an owner of demo**
  today. It is said on the machine's Activity and to the machine's owner
  before (by Slack DM), who stays one of its admins. It goes down the
  connection like any action, so a machine with `hi server remote off`
  refuses it.
- Making someone a machine admin never makes them an approver, as now.

### A machine can say no

A machine's admins may not want the server able to act on it at all:
restarts, the terminal into a box. `hi server remote off`, run on the
machine, keeps the machine on the server's page, read only. The server's
page says why its buttons are gone.

## Security

- **What the server can see** is what a machine's page showed: names,
  states, versions, usage. No setting's value crosses the connection.
- **What the server can do** is only what a machine admin signed in to
  it may do, checked on the machine. Someone who breaks into the server
  and signs in as a machine admin can do what that admin can: restart,
  deploy, open a terminal in a box. That is the trade for one page, and
  `hi server remote off` is the way out of it for a machine that wants
  none of it.
- **The terminal** goes through the server, over https on both legs: the
  server could read it. It isn't encrypted end to end ([Decided](#decided)).

## Code that changes

- `internal/cli/main.go`: `hi server` first in the switch and in help,
  `hi org` silent; the "did you mean" for `serve`/`server`.
- `internal/cli/org.go`: `oldNameHint` and `orgFormOf` say `hi server`;
  help and messages say "server".
- `internal/cli/app.go` (`hi app new`) and the admin page's New app: the
  check for a server.
- `internal/cli/admin*.go`: the page moves to the server only; on a
  machine, what's left is a new `admin_remote.go`: the connection to the
  server, the actions down it, the check against `admin.json`, `hi
  server remote`. `hi admin` leaves `main.go` and help; the page's
  service and its NetBird peer are removed on `hi update`.
- `internal/cli/org.go`: `hi server link | admins | address | owner`.
- The server: the machines' connections, Home with machines, a machine's
  views, forwarding an app's sections and the terminal.
- `site/guide/org/` → `site/guide/server/`, with a redirect; every page
  that says `hi org`.

## Tests

- `hi org …`, `hi server …`, `hi connect`, and `hi disconnect` run the
  same code; `hi org` prints nothing extra.
- `hi serve join` and `hi server llm` fail with the "did you mean" and
  run nothing.
- `hi app new`, `hi app up`, the broker, and `hi update` on a machine
  with apps and no server stop with both ways; with one, go on. A box
  already running isn't stopped.
- **Make me the owner** by a server admin: the machine's owner changes,
  the old owner stays an admin and is told; refused with remote off and
  for someone who isn't the server's admin.
- The server's page lists a joined machine and its apps from the
  machine's connection; a machine that stops being heard from shows as
  such.
- An action from the server's page by someone who isn't the machine's
  admin is refused on the machine, even with the server faked.
- `hi server remote off`: the machine shows read only, and refuses
  actions.
- `hi admin …` runs nothing and points to `hi server status`. After `hi
  update`, a machine has no page service and no `<machine>-admin` peer,
  and its `admin.json` is unchanged.
- No setting's value in anything the machine sends up.

## Releases

1. **The rename:** `hi server` in help, messages, the admin page, and
   the guide; `hi org` silent; the "did you mean".
2. **A server for every app:** `hi app new`, `hi app up`, the broker,
   `hi update`, and New app check for one. Our machines with apps join
   one before this ships.
3. **The machines on the server's page, read only:** the connection
   up, Home with machines and apps, a machine's views.
4. **Acting from it:** actions down the connection, checked on the
   machine; the terminal; `hi server remote off`; **Make me the owner**.
   `hi admin` and each machine's page go in this release, not before:
   until the server's page can act, the machine's page is the only one
   that can.
5. **What builds on it:** settings from the server's Vault
   ([hi_app_vault.md](../ideas/hi_app_vault.md), release 3), moves between the
   server's machines ([hi_app_move.md](../ideas/hi_app_move.md)).

## Not in this

- Merging the machines' admin lists into the server's.
- Running a machine's apps from the server when the machine is down
  (that is hi_app_move.md's standby).
- More than one server per machine.

## Decided

- **The terminal through the server** is over https on both legs, and
  the server could read it. Not encrypted end to end: the server's admins
  can make themselves a machine's owner and open the terminal anyway.
  End to end only if a machine's admins ask for it.
