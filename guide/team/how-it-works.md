---
title: How the team works
description: "From a request in Slack to merged work: how the lead agrees work with the people, how the broker runs members in boxes and has their work reviewed, and how the team remembers what it learns."
---

## From a request to a merge

Someone in the channel asks for something. The lead agrees with them what
will change and a short list of things they can try, writes it down in
`docs/specs/`, and briefs the coder. When the coder finishes, the broker has
the reviewer check the work; if it needs changes, the broker gives the coder
the reviewer's notes on the same branch, up to three reviews, without the
lead in between. The lead hears once: approved, or changes still needed.
Once the reviewer approves, the lead merges and tells the people what
changed and how to try it. A feature is done when they agree it works.

The lead puts members to work with `hi team task`, `review`, `wait`,
`merge`, `release`, and `discard` in its box. These go to a broker that `hi team` runs on the
machine: it checks each request against `team.json`, starts the member as
[`hi agent`](/guide/agent/), runs the review rounds, and refuses a merge that no review approved, and a release that no owner approved.
The lead never holds the agents' keys or the repository's credentials, and
nothing it asks for widens a member's box.

## Work waits for a yes

Work starts only after someone says yes. The lead proposes the plan and
the list of things to try, and waits; the broker refuses a new task unless
the lead names the message that approved it, a reply from one of the
team's people after the lead asked, in the same thread. Small fixes, such
as a typo, may start without asking, and the channel is told when one
does.

## What a member gets

A member sees only its brief and the project, so the broker sends what
the people gave along with it: the thread the work was agreed in, in their
words with their links, and the files shared there (screenshots,
mockups, documents), in a read-only folder in the member's box. The lead
attaches anything else, such as a file shared earlier in the channel. A
follow-up and its review get the same files.

## Packages

Work that adds packages gets a closer look. The reviewer's brief lists what
the task adds, removes, and updates, and the reviewer checks each new
package and runs the audit. Bun runs a package's install script only when
the project trusts it (`trustedDependencies`) or Bun vets it, so a task
that trusts a new package can't be merged until an owner says yes: the
lead asks in the channel what the package is and why the app needs it.

## More of the same, or something new

Unmerged ideas are capped at five. Each new task starts one, and its
follow-ups continue it. The broker refuses a sixth until one is merged or
discarded, so ideas can't pile up beyond what anyone can follow in Slack.

When you ask for more of something that isn't merged yet ("nice, but sort
it by name"), the lead continues that work on its branch rather than
starting over, and hi says so in the thread where you agreed to it, so
you see what's added to what you said yes to. Anything new still waits
for a yes. hi keeps the lead from losing work along the way: it refuses a
new task when your yes came later in the thread of work still open,
continues the latest version of a piece of work rather than an older one,
and when work no longer merges because dev moved on, it has the member
bring it up to date and the reviewer check it again.
`hi team status payments` shows what dev has on top of production and the
ideas still open.

## The lead in the channel

The lead answers only when someone @-mentions it (`@payments`), even
in a thread it's part of, so people can talk among themselves in the
channel. It still follows everything: hi keeps a log of the channel,
threads and shared files included, in the team's `channel/` folder, and
every 15 minutes the lead reads what's new and keeps what matters in its
memory: a decision made in passing, a link or a video someone shared, who
cares about what. `hi team channel payments --since 2h` shows you the log.

## The lead's memory

The lead's long-term memory is [Hindsight](https://github.com/vectorize-io/hindsight):
a memory server that runs beside the lead, keeps the facts, people, and
decisions from every conversation, and recalls the ones that matter before
the lead answers. It runs in a container of its own on the team's private
network, with its database in the team's `memory/` folder. Its model calls
go through the team's proxy like the lead's, so it holds no keys. The
first `hi team up` downloads its image, about 2.6 GB. To do without it, set
`"memory": "off"` in `team.json`; the lead then keeps Hermes' small
built-in notepad only.

## Roles that remember

Each member is a role that outlasts its tasks. A task is a fresh agent in
a fresh box, but the role keeps a folder in the team's `roles/`: `ROLE.md`,
how it works on this team, and `LEARNED.md`, what earlier tasks taught it,
such as a trap in the tests. hi gives both to every task for that role, so
the next coder starts where the last one left off, also when you switch
the coder to another agent. Members propose lessons in their reports, and
the lead decides what to keep. Edit a `ROLE.md` to change how a role works
for your team, such as a reviewer who also checks accessibility. A team
template, from the setup's code question, can bring roles of its own: a
researcher who knows your data, a coder with your conventions.

## The project's map

The project has a map for the agents, `docs/map.md`: what it already has
(pages, routes, tables, shared code) and where each lives, the agents'
counterpart to `HELP.md`. Members read it before building and keep it
current, and the reviewer sends back work that builds a second version of
something on it. Why it's built as it is goes in `docs/decisions/`.
