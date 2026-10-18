# `hi team` docs: `/docs` on every app

Status: Approved (2026-10-18), built in v0.81.0; addresses that stay get
the new routes since v0.81.1; charts drawn by pele, served from hi, since
v0.82.0.

Dependencies: [hi_team.md](hi_team.md) (the addresses, releases, the
lead and its members), [hi_team_helper.md](hi_team_helper.md) (the
relay's routes to the team's socket), and
[hi_team_helper_signin.md](hi_team_helper_signin.md) (help from the
app's guide).

## Goal

Every app a hi team builds explains itself, at a fixed address, to two
kinds of reader:

- **The people who use it**: why the app exists, who it's for, its major
  parts and what they do, and how to do things. Easy to read, no code.
- **Developers and agents**: how it's built and why, decisions over time,
  what changed in each version, and pictures of how the parts relate,
  for traceability.

Before this, a team's project had `HELP.md` for the people and
`docs/map.md`, `docs/decisions/`, and `docs/specs/` for the agents, all
read only in the repository. Nothing showed them to the people, and the
app's story (why it exists) was nowhere: the lead had only the team's
one-line purpose to steer by.

## What people see

`https://<the app's address>/docs` opens a page with two choices:

```text
┌────────────────────────────────────────────────────────────┐
│ payments docs                                 Open the app │
│                                                            │
│  payments                                                  │
│  Track supplier payments                                   │
│                                                            │
│  ┌──────────────────────────┐  ┌──────────────────────────┐│
│  │ FOR THE PEOPLE WHO USE IT│  │ FOR DEVELOPERS AND AGENTS││
│  │ User docs                │  │ Tech docs                ││
│  │ Why the app exists, who  │  │ How it's built and why...││
│  └──────────────────────────┘  └──────────────────────────┘│
│  preview: dev, v3 + 2 changes                              │
└────────────────────────────────────────────────────────────┘
```

Each part has a menu of its pages beside the page (above it on a phone),
a switch between the two parts, and under each page the file it comes
from and the version on the address. The helper's button is on every
page, as on the app's.

## Where the docs live

In the project, so they go wherever the code goes and each version's docs
match its code:

| File | Shown as |
|---|---|
| `docs/user/index.md` | `/docs/user/`: why it exists, who it's for, what's in it, getting started |
| `docs/user/<part>.md` | `/docs/user/<part>` |
| `docs/tech/index.md` | `/docs/tech/`: the parts and how they connect, drawn |
| `docs/tech/<page>.md`, `docs/tech/<dir>/<page>.md` | `/docs/tech/<page>`, grouped by folder |
| `docs/map.md` | `/docs/tech/map` |
| (versions.json) | `/docs/tech/changelog`, written by hi |
| `docs/decisions/*.md` | `/docs/tech/decisions/...`, group Decisions |
| `docs/specs/*.md` | `/docs/tech/specs/...`, group Specs |
| `docs/specs/notes/*.md` | `/docs/tech/specs/notes/...`, group Notes |

A project without `docs/user/` shows `HELP.md` as its user docs. The
team-app template starts with `docs/user/index.md` and
`docs/tech/index.md` and no `HELP.md`.

Pages are Markdown (GitHub's: tables, task lists), with:

- **`boxes` blocks**: the hi guide's structure diagrams
  ([guide-diagrams.md](../../guide-diagrams.md)), the same JSON, drawn
  by hi. One that doesn't parse shows why, with its text.
- **`mermaid` blocks**: charts in Mermaid's language, drawn in the
  browser by [pele](https://pele.run) 1.6.0 (MIT, from Obsidian's
  developers), which draws every diagram type Mermaid has built in, as
  SVG without scripts or HTML labels. hi embeds it
  (`third_party/pele/`) and serves it at `/docs/pele.js?v=<hash>`,
  cached for good, loaded only on a page that has a chart; nothing comes
  from a CDN. Its colors are the page's (`--pele-*` CSS variables set
  from the docs' tokens), so dark mode needs no second drawing; it draws
  to the column's width and turns a wide flowchart on its side on a
  phone. A chart that doesn't parse keeps its text, with the error.
  Chosen over Mermaid itself after drawing 60 charts from real projects
  with both: the same 58 drew and the same 2 failed, pele's layouts were
  easier to read, and it was about 4 times faster and a seventh of the
  size; Mermaid 11.4.1 had no radar or treemap.
- **Links between docs as files** (`../map.md`, `decisions/0001-x.md`),
  so they work on GitHub too; hi points them at the pages. Pictures and
  other files under `docs/` are served from `/docs/raw/<file>`.
- No raw HTML: it is left out.

For agents, `/docs/llms.txt` lists every page with its file, and
`/docs/raw/<file>` gives any file under `docs/` (and `HELP.md`) as it is,
as plain text unless it is a picture.

## The changelog

hi writes `/docs/tech/changelog` from versions.json: each release, newest
first, with its date, notes, and the changes it brought, marking the one
production is on. On the preview, "In dev, not released yet" lists the
merged work production hasn't got. Nobody has to keep it.

## Who keeps them

- **The story** (why it exists, who it's for) is the owners'. The
  template marks it "Not told yet". The lead's SOUL.md says it is the
  team's north star: read it before planning, ask an owner for it while
  it's missing, have the coder write it (a small task), and ask again
  when requests no longer fit it. An existing SOUL.md gets the paragraph
  on the next start.
- **The coder** updates the user docs for what people will see, the map
  for what it adds or moves, and `docs/tech/index.md` when the parts or
  how they connect change, in the same task. Every brief says so.
- **The reviewer** checks the user docs say how to use what changed and
  the tech docs still draw the parts as the code has them.
- **The lead** writes only the specs, as before.

## The helper reads them

Help (the Ask tab, for anyone with the link) answers from the user docs
at the address's commit, each page marked with its address, and may give
a page's address when the answer is longer than the panel. Without user
docs it reads `HELP.md`, then `README.md`, as before. The chat with the
lead is unchanged: the lead reads the repository.

## How it is served

The relay routes `/docs/` to the team's web socket, as it does `/__hi/`,
with a new `hi net expose --route-always <prefix>=unix:<socket>`: a route
that stays while `--inject-if`'s file is gone, so turning the helper off
doesn't take the docs away (the docs then leave out the helper's script).
`/docs` without its slash redirects to `/docs/`. The docs have the app's
protection: its key, password, or login.

Each slot remembers what its relay routes (`relay` in run/<slot>.json, a
hash of the helper's and the docs' arguments). An address on a name that
stays (hi team domain) whose relay routes less than hi does now, such as
one made before v0.81.0, moves to a new relay when the team starts on the
new hi: the same name and, with `hi net expose --key`, the same key, so
the links people have keep working and nobody gets new ones (since
v0.81.1). An address NetBird made up gets the routes with its next
address, within a day, rather than new links for everyone at once.

Members draw with pele beyond the docs too: the lead's skill says which
chart fits what (a timeline for how the app grew, a gantt for a plan, an
erDiagram for the tables), every brief's map rule says mermaid blocks
work in the docs, and the coder's role says to use the `pele` package
for charts in the app's own pages. The hi guide draws its `mermaid`
blocks with the same copy (`_layouts/guide.html`).

hi reads `docs/` and `HELP.md` from the commit with one `git archive`
(at most 2 MB a file, 32 MB in all) and keeps the last six versions in
memory.

## Not in this slice

- Tech docs only for people signed in with `/hi`, for a public
  production app whose internals shouldn't be public.
- Search across the docs.
- A check members can run on `boxes` JSON before committing.
