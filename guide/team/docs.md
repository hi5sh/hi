---
title: The app's docs
description: "Every app a hi team builds has its docs at /docs: user docs for the people who use it, tech docs for developers and agents, drawn with box diagrams and Mermaid charts."
---

Every app a hi team builds has its docs on its own addresses, at `/docs`
on the preview and on production, such as `https://app.example.com/docs`.
The first thing you see is two ways in: **User docs** and **Tech docs**.
hi serves them itself, from the project's `docs/` folder in the version
on that address, so they work whatever the app is written in, and the
docs on production always match production's code.

## User docs

For the people who use the app, in plain words and the app's own names
for its pages and buttons. They live in `docs/user/`, a page per file:

- **Why it exists** and **who it's for**: the app's story, which only its
  owners can tell. While it is missing, the lead asks an owner for it and
  has the coder write it in their words. It is also the team's north
  star: the lead reads it before planning a feature, and asks an owner
  whether the story has changed when a request no longer fits it.
- **What's in it**: the app's major parts and what each does, with a page
  of its own for a part with more to say, such as `docs/user/orders.md`.
- **How to do things**: the common tasks, step by step.

The helper's **Ask** tab answers from these pages, and points people to
the page that says more. A project from before the user docs keeps
`HELP.md`, which is then its user docs.

## Tech docs

For developers, and for agents that need to trace how the app is built
and why. The menu holds:

- **The first page**, `docs/tech/index.md`: the parts of the app and how
  they connect, drawn. A `boxes` block is a structure diagram like the
  ones in this guide (boxes in boxes, numbered marks, a key), written as
  JSON; a `mermaid` block is a [Mermaid](https://mermaid.js.org) chart,
  such as a sequence for how a request flows or an `erDiagram` of the
  tables. More pages go in `docs/tech/`.
- **The map**, `docs/map.md`: what the project has and where each part
  lives.
- **The changelog**: hi writes it from the team's releases, each version
  with its date, notes, and the work it brought; on the preview, what dev
  has that production hasn't yet.
- **Decisions** (`docs/decisions/`), **Specs** (`docs/specs/`), and the
  lead's **Notes** (`docs/specs/notes/`), each a group of its own.

The coder updates the docs in the same task as the change, and the
reviewer sends back work whose docs don't match it.

## For agents

`/docs/llms.txt` lists every page with its file, and `/docs/raw/<file>`
gives a file as it is, such as `/docs/raw/docs/tech/index.md`.

## Who can read them

Whoever can open the app's address: the docs have its key, password, or
login. The helper's button is on the docs' pages too. Turning the helper
off doesn't take the docs away; `/docs/` belongs to hi, so the app can't
use it for a page of its own.
