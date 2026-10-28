---
title: Apps agents can use
description: "A hi team's app can be used by agents as well as people: its features as tools on an MCP server at /mcp, a page in its docs that says what each does, and /llms.txt from hi. The team's lead uses the tools, on production only those that read."
---

People use the app through its pages. Agents use it through its **MCP
server**: the same features, as tools an agent calls. The team's lead in
Slack is the first such agent; people's own, such as Claude or ChatGPT,
can be next.

## What the app offers agents

| Address | What it is | Who makes it |
|---|---|---|
| `/mcp` | The app's MCP server, over Streamable HTTP: its features as tools | The app: the team template starts it, your agents grow it |
| `/docs/user/mcp` | "Using the app with an agent": each tool, in plain words | Your agents, in `docs/user/mcp.md` |
| `/llms.txt` | What the app is, its docs, and its MCP server, for agents ([llmstxt.org](https://llmstxt.org)) | hi, from `docs/user/` |
| `/llms-full.txt` | All the user docs in one file | hi |

hi serves `/llms.txt` and `/llms-full.txt` for every team's app, as it
serves [`/docs/`](/guide/team/docs/): nothing to keep up.

## A tool per feature

When your agent builds a feature people use, it adds a tool for it in the
same work, where an agent would want one, such as `list_orders` or
`add_supplier`, and the tool's section in `docs/user/mcp.md`. Each tool
says whether it **only reads**: changes nothing in the app or outside it.

A new team's project has `/mcp` from the start, with one tool,
`app_status`. A project made before gets it when you ask your agent: the
`hi-dev-agents` skill in the dev box says how, in any language.

## What the lead does with them

The lead has the app's tools in Slack, on both addresses:

- **Production**: only the tools that read. It answers from the app's
  real data ("how many orders came in today?") instead of guessing from
  the code. hi keeps it to those, outside the lead's box, so a tool that
  changes something never runs on production for the lead.
- **The preview**: every tool, on the preview's
  [own copy of production's data](/guide/team/data/). It tries there what
  someone reports before it files a task.

When the tools change, with a release or a branch on the preview, the
lead sees the new ones within a minute. An app without `/mcp` simply gives
the lead no tools, and it says so.

## Who else can use them

`/mcp` is reachable as the app's pages are: on the team's network, or
behind the public address's protection. Agents of people outside the team
need a sign-in of their own first, which comes later.
