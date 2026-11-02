# `hi org` and `hi serve`: renaming `hi server`

Status: Approved (2026-11-02). Releases 1 to 3 built in v2.6.55 and v2.6.56
(release 2 is the guide).

Dependencies: [hi_server.md](hi_server.md) (every command being
renamed), [hi_server_ai.md](hi_server_ai.md),
[hi_server_expose.md](hi_server_expose.md),
[hi_compute.md](hi_compute.md) (`hi compute serve`, `--on`),
[hi_update.md](hi_update.md) (restarting a server that runs an
old binary), [hi_cluster.md](../ideas/hi_cluster.md) (the organization's machines),
[hi_model.md](../ideas/hi_model.md) and [hi_llm.md](../ideas/hi_llm.md) (serving and using
models).

## Goal

Four top-level commands are one thing seen from different sides: `hi server`
runs it, `hi connect` and `hi disconnect` join and leave it, and the
`hi cluster` draft adds the organization's machines to it. A fifth, `hi
serve`, is wanted for models, and it reads too much like `hi server`.

- **One command for the organization.** `hi org` replaces `hi server`, `hi
  connect`, and `hi disconnect`, and takes in the machines from
  hi_cluster.md. `hi help` gets four lines shorter, not one longer.
- **`hi serve` free for models.** `hi serve llm` is the one way to serve a
  model: on this machine, on one of the organization's, or on rented
  hardware.
- **Nothing breaks.** The old commands keep working, without a warning, for
  as long as there are systemd units, scripts, and fingers that type them.
  No state moves and nothing on the wire changes, so a renamed client talks
  to an old server and an old client to a renamed one.

## The name

`hi server` names how the thing runs, not what it is. What it holds is the
organization: who belongs, their devices and groups, what they may start,
who approves, what they spend, and the keys, templates, data, and models
they share. `hi org` says that, and leaves "serve" to serving.

Considered and not taken:

| Name | Why not |
|---|---|
| `hi hub` | Says nothing about what is in it |
| `hi admin` | Half of it is for everyone: joining, `status`, `live`, offering a machine |
| `hi team` | Taken: a team is an app in a Slack channel, and an organization has many |
| `hi cluster`, `hi fleet` | Only the machines; users, policy, and templates aren't a fleet |

"Server" stays as the name of the process that runs it. Prose says "the
org server" where it means the process and "your org" where it means the
people and rules; the systemd unit stays `hi-server.service`.

## Commands

```text
# Joining, on any device
hi org join <server> [--user NAME] [--no-wait]      # was hi connect <server>
hi org join <server> --agent NAME --owner PERSON    # was hi connect --agent
hi org status                                       # was hi connect status
hi org key                                          # was hi connect key
hi org slack link <code>                            # was hi connect slack <code>
hi org leave                                        # was hi disconnect

# Running it, on the server box
hi org init [--listen ADDR]                         # was hi server init
hi org run                                          # was hi server run
hi org provider | user | requests | approve | deny | ls | stop | audit
hi org slack setup | manifest
hi org approvers | policy | spend | ai | viewer | wall
hi org templates | data | expose

# Either: the admin socket on the server box, a viewer's device elsewhere
hi org live [--wall]

# The organization's machines (../ideas/hi_cluster.md)
hi org offer --cpu 8 --ram 64G [--gpu none]         # on the machine: offer room to guests
hi org offer                                        # what this machine offers now
hi org offer off                                    # stop offering; running guests finish
hi org machines [--json]                            # every machine: offered, used, free, what runs where
hi org machines drain | undrain <machine>           # on the server box
```

Every subcommand of `hi server` keeps its name, flags, and output under `hi
org`. The four joining commands move under it with their flags unchanged.
`hi connect slack <code>` becomes `hi org slack link <code>`, next to the
admin's `hi org slack setup`, rather than a bare `hi org slack` that means
two things.

`hi org` with no arguments prints the usage, in two groups: "On any device"
and "On the server box". A server-box command run elsewhere fails as it does
today ("the server is not running in …"), with one more line: "This runs on
the server box. On a device, see `hi org status`; ask your IT team for the
rest."

### Words inside `hi org`

`hi org data add <org>` already takes a Hugging Face organization. Its
usage and help say `<hf-org>` so the two orgs don't blur:

```text
hi org data add <hf-org>... [--token-file F]
```

## The old names

| Old | Runs |
|---|---|
| `hi server <anything>` | `hi org <anything>` |
| `hi connect <server> …` | `hi org join <server> …` |
| `hi connect status` / `key` | `hi org status` / `key` |
| `hi connect slack <code>` | `hi org slack link <code>` |
| `hi disconnect` | `hi org leave` |

- They are kept indefinitely and, until the last release, print nothing
  extra. `hi server run` runs under systemd on every org server installed so
  far, and a notice there would only fill the journal; the last release adds
  a hint on a terminal only.
- They are not in `hi help`, the guide, or the `hi` skill, so new users and
  agents learn one name. `hi server --help` prints the `hi org` usage.
- `hi server watch` keeps its "is gone" message, and `hi org watch` gets the
  same one.

## What does not change, until the last release

These keep their old names while everything else moves, so nothing breaks
mid-way. The last release (see [Cleaning up](#cleaning-up)) renames them
with a migration.

| Thing | Stays |
|---|---|
| The HTTP API, signed requests, device keys | Unchanged; old and new clients and servers mix freely |
| The server's state | `~/.local/state/hi/server`, and `HI_SERVER_DIR` |
| The device's connection | `~/.config/hi/server.json` and its device key |
| The systemd unit | `hi-server.service`; `hi heartbeat` and `hi org wall` look for it by name |
| The Slack app | Its name, its manifest's scopes, and `/hi` |
| `hi q`'s saved provider | `server`, as written in existing configs |
| The audit log | Entries already written keep their wording |

`hi org init` prints a unit whose `ExecStart` is `… org run`, under the same
unit name. `hi update` finds a stale server whether its command line is
`server run` or `org run`.

## `hi serve`

```text
hi serve llm <recipe | owner/repo-GGUF --quant Q> [--on <where>] [--gpu H] [--max 8h] [--name N]
hi serve ls [--json]            # every model this user serves, where, its URL, until when
hi serve stop <name>
hi serve logs <name> [--follow]
```

`--on` takes the same values everywhere in hi:

| `--on` | Where | From |
|---|---|---|
| `colab`, `hf`, `runpod`, `shadeform` | Rented hardware, through the org when managed | Today's `hi compute serve` |
| `local` | This machine, in a box | hi_model.md, folded in here |
| `<machine>` | One of the organization's machines | hi_cluster.md |
| `auto` | An organization machine that fits | hi_cluster.md |

A machine may not be named like a provider, `local`, or `auto`; `hi org
offer` refuses such a name.

- `hi compute serve` stays as another name for `hi serve llm`, with its flags.
  `hi compute ls`, `stop`, and `logs` still show and stop a rented model
  server, which is also a compute instance.
- `llm` leaves room for other kinds later (speech, embeddings) without
  guessing at them now. A bare `hi serve <recipe>` is not accepted, so that
  room stays.
- Serving on `local`, on a machine, and the org knowing about served models
  (so `hi org ai` and `hi llm --model local` can find one) get their own
  spec, `hi_serve.md`, built from hi_model.md. This spec only takes the name
  and moves `hi compute serve` under it.

## Docs, site, and skills

- **The `hi` skill** learns `hi org` and `hi serve llm` in the same release
  as the CLI. Agents read it, and an agent that tells a user to run `hi
  server approve` is still right, just dated.
- **The guide** and the site: every command line, and the prose where it
  names the command. `/server/` moves to `/org/`; `/server/` stays as a page
  that sends visitors to `/org/`, since links to it are out there. Its demo
  is re-recorded from a real run, as docs/feature-pages.md asks.
- **Approved specs are records** of what was built and stay as they are.
  hi_server.md gets one line under its status: "Its commands are `hi org`
  since vX.Y.Z (hi_org.md)." Drafts in `specs/ideas/` are updated, since
  they describe what is still to come.
- **The roadmap's released entries** stay as written; planned entries use
  the new names.

## Releases

1. **`hi org`.** The command, the old names as silent aliases, every usage
   line, error, and hint that names a command (including Slack's messages
   and the `hi org init` unit), `hi update`'s stale-server match, the `hi`
   skill, and the `/server/` page's demo, whose commands the feature-page
   tests check against the usage.
2. **The guide and the site.** Guide pages, `/org/` with `/server/` sending
   there, `llms.txt`, the README, the diagrams, and the note on
   hi_server.md.
3. **`hi serve llm`.** `hi compute serve` moved under it, with `hi serve ls`,
   `stop`, and `logs` over the instances that serve a model.
4. **Later, in their own specs.** `hi serve llm --on local` ([hi_serve.md](../ideas/hi_serve.md)),
   then the machines: `hi org offer` and `hi org machines`, and `--on
   <machine|auto>` (../ideas/hi_cluster.md).
5. **Cleaning up**, last: what was kept so nothing broke gets the new name
   (below).

## Cleaning up

The last release, once every org server and device runs a release with `hi
org`. Each rename migrates on its own, keeps reading the old name for a
while, and says what it moved.

| Kept | Becomes | Migration |
|---|---|---|
| `~/.local/state/hi/server` | `~/.local/state/hi/org` | `hi org run` moves the directory on start when only the old one exists, and leaves a symlink at the old path |
| `HI_SERVER_DIR` | `HI_ORG_DIR` | Both read; `HI_ORG_DIR` wins. `hi org init` writes only the new one |
| `~/.config/hi/server.json` | `~/.config/hi/org.json` | Moved on the first command that reads it; the device key stays where it is |
| `hi-server.service` | `hi-org.service` | `hi update` on the server box offers to write the new unit, enable it, and disable the old one (sudo, asked). `hi heartbeat` and `hi org wall` look for either |
| `ExecStart=… server run` in units | `… org run` | Written by the same step |
| `hi q`'s saved provider `server` | `org` | Both read; written as `org` on the next save |
| Go files and identifiers (`server*.go`, `runServer`, `hiServer`, `serverDirectory`) | `org*.go`, `runOrg`, … | A rename-only commit, no behavior change |
| The admin socket's host name `hi-server` | `hi-org` | Internal; changed with the identifiers |
| The Slack app's name "hi compute" | "hi org" | Shown in `hi org slack manifest`; existing apps are renamed by an admin in Slack, which the release notes say |
| `hi server`, `hi connect`, `hi disconnect` | Print a one-line hint naming the new command, on a terminal only | Never removed: old units and scripts keep running |

Not renamed: audit entries already written, and approved specs, which are
records.

## Acceptance criteria

1. Every `hi server …`, `hi connect …`, and `hi disconnect` form in the
   current help runs the same code as its `hi org` form, with the same
   output, exit code, and files written; a table test lists them all.
2. `hi help` lists `hi org` (and `hi serve` from release 3), and none of
   `server`, `connect`, or `disconnect`.
3. No usage line, error, hint, or Slack message names `hi server`, `hi
   connect`, or `hi disconnect`; a test parses the Go sources and checks
   every string literal outside the alias table and tests. Comments may
   keep the old words.
4. A unit with `ExecStart=… server run`, and a new one with `… org run`,
   both start the server, and `hi update` offers to restart either.
5. A client of this release joins, requests, and is approved by a server of
   the release before, and the other way around.
6. The `hi` skill and the guide use only the new names; the feature-page
   tests pass with `/org/`, and `/server/` reaches it.
7. `hi serve llm qwen3.8-flash-next --on colab` and `hi compute serve
   qwen3.8-flash-next --on colab` do the same thing.

## Open questions

1. Are the old command names ever removed, or kept forever with the hint
   from the last release? Proposed: kept, since removing them can stop an
   org server whose unit was never rewritten.
2. Should `hi org status` also show this machine's offer (../ideas/hi_cluster.md) once
   that exists, or keep that to `hi org offer`?
