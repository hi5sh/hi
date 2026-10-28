# `hi team`: apps agents can use

Status: Approved (2026-10-26), built in v2.5.32.

Dependencies: [hi_team_docs.md](../approved/hi_team_docs.md) (`/docs` on
every app, from `docs/user`), [hi_team_internal.md](../approved/hi_team_internal.md)
(the relays to each address), [hi_team_data_fork.md](../approved/hi_team_data_fork.md)
(the preview's own copy of production's data).

## Goal

A team's app can be used by agents as well as by people: the team's lead
first, and later any agent a person points at it. The lead answers "how
many orders came in today?" by asking the app, tries what a person reports
on the preview, and shapes a task from what the app really does, not only
from its code.

## Why

- The lead reads the code and the docs, but can't see the app's data or
  try a feature. It guesses at what a person sees.
- People increasingly reach apps through their own agents (Claude, ChatGPT,
  Cursor). An app with an MCP server and an llms.txt is one they can use.
- What an agent may do in the app should be designed and written down, as
  the pages are, not left to whatever a browser can click.

## What an app offers agents

Three things, at the app's addresses:

| Address | What it is | Who makes it |
|---|---|---|
| `/mcp` | The app's MCP server: its features as tools | The app (the template starts it; the owners' agents grow it) |
| `/llms.txt` | What the app is and where its docs are, for agents (llmstxt.org) | hi, from `docs/user/` |
| `/llms-full.txt` | All the user docs in one file | hi, from `docs/user/` |
| `/docs/user/<page>.md` | Each docs page as plain Markdown | hi |

hi makes the last three for every team's app, old and new, as it makes
`/docs/`: nothing for the owners to keep up.

### The MCP server

- **Streamable HTTP at `/mcp`**, stateless, answering in JSON: initialize,
  tools/list, tools/call, ping. The template has a small server of its own
  (`src/mcp.ts`, no dependency) and one tool, `app_status`.
- **A tool per thing people do**, where an agent would want it: "list the
  top 20", "add a supplier". The owners' agents add one in the same work as
  the feature, as they update the docs and the map.
- **Each tool says whether it changes anything**: `readOnlyHint: true` for
  those that only read. hi relies on it (below).
- **Its page in the docs**: `docs/user/mcp.md`, "Using the app with an
  agent": how to connect, and each tool with what it does, what it takes,
  whether it changes anything, and an example. The owners' agents write it
  as they add tools; hi's docs show it under the user docs and list it in
  `/llms.txt`.

## The lead uses the app

- **Two MCP servers in the lead's Hermes**: `app`, production, and
  `app_preview`, the preview. Each is `hi team mcp production|preview` in
  the lead's box: a bridge from Hermes (stdio) to the app's `/mcp`, over a
  socket the team's service gives the lead's box for each address.
- **Production, only tools that read.** The bridge lists and calls only
  tools marked `readOnlyHint: true` on production. On the preview, which
  runs on its own copy of production's data, every tool.
- **An app without `/mcp`** gets a server with no tools and a line saying
  so; nothing fails. When the app's tools change (a release, a branch on
  the preview), the bridge tells Hermes, which lists them again.
- **The skill says when**: to answer from the app's data, to see what a
  person sees, and to try a report on the preview before filing it; and
  that the preview may show a branch being tried (`hi team versions`).
- **Every call is logged** in the team's service's log, with its tool and
  address, and every refusal.

## The owners' agents keep it up

- The team-app template's AGENTS.md and `docs/map.md` say: a feature people
  use gets an MCP tool where an agent would want it, marked read-only or
  not, with its part in `docs/user/mcp.md`, in the same work.
- A dev skill, `hi-dev-agents`, says how to add `/mcp` to an app that
  hasn't one (any language: the protocol is four JSON-RPC methods), how to
  add a tool, and how to try it (the app on localhost and `curl`).
- An app made before gets it as a task, when its owners want it.

## Not in this release

- Agents outside the team (a person's own Claude) on `/mcp`: they need a
  sign-in. Until then `/mcp` is reachable as the app's pages are: on the
  internal network, or behind the public address's protection.
- A browser for the lead (Playwright MCP): the app's tools say more, with
  less risk. Revisit for apps that have no tools yet.
- OpenAPI and `/.well-known/` cards: llms.txt and MCP first.
