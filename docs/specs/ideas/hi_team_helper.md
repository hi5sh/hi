# `hi team` helper specification

Status: Idea (2026-10-15).

Dependencies: [hi_team.md](../approved/hi_team.md) (the broker, the lead,
the preview and production addresses, approvals, the channel's log),
[hi_net_expose.md](../approved/hi_net_expose.md) (the relay in front of
every team address), and Hermes' API server (the lead answers outside
Slack).

## Goal

The people talk to the lead from inside the app the team is building, and
point at what they mean.

Today a person tries the preview, switches to Slack, and writes "the green
button under the table on the suppliers page is too small". The lead
guesses which button, the coder guesses again, and a round goes by. Lovable
and similar tools solve this with a chat beside the app and a way to click
on the part you mean. A hi team should have the same, for every app it
builds, whatever the app is written in.

```text
┌──────────────────────────────────────────┬─────────────────────────┐
│  Suppliers                     [+ New]   │  payments team lead   × │
│ ┌──────────────────────────────────────┐ │  dev, v3 + 2 changes    │
│ │ Name        IBAN          Status     │ │                         │
│ │ Acme AB     SE45 5000...  ┏━━━━━━━━┓ │ │  Anna: Make this one    │
│ │ Nordic Oy   FI21 1234...  ┃ Paid   ┃ │ │  red when it's overdue  │
│ │                           ┗━━━━━━━━┛ │ │  [#1 span "Paid"]       │
│ └───────────────────── span.badge ─────┘ │                         │
│                                          │  Lead: Overdue means    │
│                                          │  past the due date with │
│                                          │  no payment? I'd turn   │
│                                          │  the badge red and ...  │
│                                     (◉)  │  [⌖ Select] [ Message ] │
└──────────────────────────────────────────┴─────────────────────────┘
```

- **A chat with the lead, in the app.** A small button floats in a corner
  of every page of the preview and of production. It opens a side panel:
  a conversation with the team's lead, the same lead as in Slack, with the
  same memory.
- **Point at what you mean.** In select mode, hovering outlines the element
  under the pointer with a box and a label; a click freezes it and adds it
  to the message as a reference. The lead gets the page, the element, and
  enough about it for a coder to find it in the code.
- **Any framework.** hi adds the helper on the way to the browser, in the
  relay that already sits in front of every team address. The app's code
  holds nothing of it: React, Vue, server-rendered Go templates, or a
  Streamlit page all get the same helper.
- **On in dev and production, until an owner says otherwise.** Production is
  where people find problems, so the helper is on there too. An owner can
  ask the lead to turn it off for production, and it is gone with no new
  release: the app was never changed.

Security is kept light on purpose. These are internal apps behind a link
with a key, and the team is early. What is deferred, and why, is listed
under Later.

## What there is today

- The preview and production each run in an app box, published to this
  machine's loopback. A `hi net expose --link --follow` relay, a Go
  `httputil.ReverseProxy` on this machine, puts the box on an address and
  checks the link's key (a cookie after the first visit).
- The lead talks only in Slack, through Hermes' gateway. Hermes also has an
  API server (`API_SERVER_ENABLED`, a bearer key, `/v1/responses` with a
  named `conversation`, server-sent events), off in a team today.
- The broker logs the channel into `channel/` and checks approvals there: a
  message from an owner or asker, in a thread the lead replied in.

## How the helper reaches the page

The relay of a team address injects one script tag into each page and
passes the helper's own paths to the team's service.

```text
browser ──> relay (hi net expose, on this machine)
             ├─ /__hi/*        ──> the team's service (unix socket): the helper's
             │                     script, its API, the chat with the lead
             └─ everything else ──> the app box, as now; HTML pages come back
                                    with <script src="/__hi/helper.js"> added
```

New flags on `hi net expose`, general enough for any exposure, which the
broker passes for team addresses:

```text
--route /__hi/=unix:<socket>   serve a path prefix from a socket instead of the target
--inject <url>                 add <script src="<url>" defer> to HTML pages
--inject-if <file>             inject, and serve the routes, only while the file exists
```

The broker gives each slot its own file, `run/<slot>.helper`. Turning the
helper off removes the file. The relay checks it on each page, with the same
cached stat as `--follow`, so the change takes effect on the next page load
with no restart. While it is off, `/__hi/` passes through to the app like
any other path.

Which responses get the tag:

- Only page loads: a `GET` whose `Sec-Fetch-Dest` is `document`, answered
  with `200` and `text/html`. HTML fragments fetched by the page (htmx,
  Turbo, `fetch`) and pages inside iframes are left alone, so the helper
  appears once, at the top.
- The relay drops `Accept-Encoding` from page-load requests, so the app
  answers them uncompressed. Pages are small, and other requests stay
  compressed.
- The tag goes right after the first `<head …>` tag, else before `<body`,
  else after `<html …>` or the doctype. The relay scans the first 64 KB as
  it streams. A page with none of those in its first 64 KB passes through
  without the helper, and the relay logs it. A streamed page is never held
  back.
- `Content-Length` and a strong `ETag` are dropped from a changed page.
- When the app sends a `Content-Security-Policy` header, the relay adds
  `'self'` to `script-src` and `connect-src` and, when the policy uses a
  nonce, copies it onto the tag. A policy in a `<meta>` tag is not handled.

`/__hi/` is reserved on team addresses. The hi-team skill tells the lead,
and the lead's briefs tell the coder, never to use it in the app.

Because the helper comes from the same address as the app, its requests
carry the link's cookie. The relay already checks it and strips it before
anything reaches the app or the team's service. Nothing more is needed for
cross-origin requests, and no other key is involved.

## The helper in the page

One script, written in TypeScript without a framework, built with esbuild
into `helper/dist/helper.js`, and committed. `go build` needs no Node:
hi embeds the file and serves it as `/__hi/helper.js?v=<hi version>`. It
aims to be under 50 KB.

- **Isolated.** It mounts one custom element, `<hi-helper>`, at the end of
  `<body>`, with an open shadow root and the highest z-index. Its styles
  live in the shadow root, so the app's CSS can't reach in and the helper's
  can't leak out. It changes nothing else in the app's DOM except the
  outline layer in select mode, which is also inside `<hi-helper>`.
- **The button.** A round button in the bottom-right corner, which can be
  dragged to another corner. The corner, and whether the panel was open,
  are kept per browser (localStorage, wrapped in try/catch).
- **The panel.** A side panel on the right, 400 px wide, over the page. On
  a narrow screen it is a bottom sheet. It shows the lead's name ("payments
  team lead"), which version the page is ("dev, v3 + 2 changes",
  "production v3"), the conversation, and the composer.
- **Who's talking.** The first time it opens, the helper asks "Who are
  you?" and lists the people who may ask for work (the channel's members
  when the team has no askers), with names from Slack. The choice is kept
  per browser and can be changed in the panel. This is a claimed identity,
  not a sign-in; see Later.
- **The conversation.** Messages are kept by the team's service, so a
  reload, another page, or another browser shows the same conversation for
  that person and that slot. "New conversation" starts a fresh one. The
  lead's replies stream in as they are written, and are shown as a safe
  subset of Markdown (paragraphs, lists, bold, code, links), never as raw
  HTML.
- **Page context, always.** Every message carries the page's path and
  title, the viewport size, and the version the page shows, with or without
  references.
- **A new version.** When the slot moves to a new commit (a merge reaches
  the preview, a release reaches production), the panel says "A new version
  is up" with a reload button. The lead can say "try it now" and mean it.

## Select mode

The part the people will use the most, and the reason for this spec.

- **Entering.** The ⌖ button in the composer, or `Alt+S` while the panel is
  open. A thin banner at the top of the page says "Select mode: click what
  you mean · Esc to stop". The page itself stays as it is.
- **Hover.** The element under the pointer gets an outline (a box drawn
  over it in a layer that ignores the pointer, not a style on the element),
  a faint fill, and a label with a short name and size: `button.primary ·
  120×32`. Moving the pointer moves the box, at most once per animation
  frame.
- **The app doesn't react.** In select mode the helper takes `click`,
  `pointerdown`, `pointerup`, `mousedown`, `mouseup`, and `submit` in the
  capture phase on `window` and stops them, so a click selects instead of
  pressing the button or following the link. Scrolling still works.
- **Getting it exactly right.** `Alt+↑` and `Alt+↓` (or the arrows on the
  label) move the selection to the parent or back to the child, because the
  element under the pointer is often a `<span>` inside the button you mean.
- **Freezing.** A click freezes the box: it stays drawn, follows the
  element through scrolling and resizing, and the element is added to the
  composer as a chip (`#1 span "Paid"`). Select mode ends after a click.
  Shift-click adds more than one element without leaving.
- **Chips.** Hovering a chip outlines its element again if it is still on
  the page. × removes the chip. Chips belong to the unsent message; an
  unsent message survives a reload (sessionStorage).
- **Touch.** A tap outlines, and a tap on the label's ✓ freezes.
- **Shadow DOM and iframes.** Elements in an app's open shadow roots can be
  selected (the event's `composedPath()`). Elements inside iframes can't;
  the iframe itself can.

What a reference holds, gathered in the page when it is frozen:

```jsonc
{
  "n": 1,
  "selector": "main table.suppliers > tbody > tr:nth-child(1) > td.status > span.badge",
  "tag": "span",
  "text": "Paid",                                  // trimmed, up to 200 characters
  "attributes": {"class": "badge badge-ok", "data-testid": "status"},
                                                   // id, class, role, aria-*, name, type,
                                                   // href, alt, title, placeholder, data-*
  "html": "<span class=\"badge badge-ok\" data-testid=\"status\">Paid</span>",
                                                   // outerHTML, up to 2 KB, long text cut
  "path": ["main", "section \"Suppliers\"", "table.suppliers", "tr", "td.status"],
  "landmarks": {"heading": "Suppliers", "label": null},  // the nearest heading and form label
  "box": {"x": 812, "y": 248, "width": 54, "height": 22, "viewport": [1440, 900]},
  "style": {"color": "rgb(255,255,255)", "background-color": "rgb(34,139,34)",
            "font-size": "12px", "display": "inline-block"},
  "source": null                                   // see below
}
```

- **The selector** comes from [`@medv/finder`](https://github.com/antonmedv/finder)
  (MIT), vendored into `third_party/`: the shortest selector unique on the
  page, preferring ids, `data-testid`, and stable classes over
  `nth-child`. It finds the element again in the same page; the text,
  attributes, and path are what let a coder find it in the code with grep.
- **Stable names only.** The helper passes `finder` filters that skip
  names a build generates and the next build changes: ids that look
  generated (`:r3:`, `ember123`, long hex or digit runs), hashed classes
  from CSS modules and CSS-in-JS (`_title_x7f2a`, `sc-…`, `css-…`,
  `jsx-…`, `svelte-…`), and Tailwind's utility classes (`flex`, `mt-4`,
  `bg-green-600`), which say nothing about which element it is. Such a
  selector still finds the element, and a coder can grep for it in the
  code. The filters are OpenPicker's idea, written as `finder` options.
- **`source`, when the framework gives it away.** Without depending on any
  framework, the helper checks for what development builds leave on
  elements: Vue's `__vueParentComponent.type.__file`, Svelte's
  `__svelte_meta.loc`, a React fiber's component name, and a
  `data-hi-source` attribute any build can add. It records a file and line,
  or just a component name, when one is there, and nothing when none is.
  It's a hint, never required.

## Picking a library, or not

The picker is a few hundred lines on top of `elementFromPoint`, an overlay,
and a selector generator, and it has to fit the helper's own chat and
transport. The open-source tools found so far each come close and miss on
one point:

| Tool | What it is | Why not as is |
|---|---|---|
| [stagewise](https://github.com/stagewise-io/stagewise) | A browser toolbar: select elements, comment, send to an agent | AGPLv3, open core; built to send to an IDE agent (Cursor, Copilot), not to a server |
| [React Grab](https://github.com/aidenybai/react-grab) | Point at an element, copy its context for an agent | React only, development builds only |
| [Agentation](https://github.com/benjitaylor/agentation) | Click to annotate UI for agents, with an MCP server | A React component; the app has to include it |
| [element-picker](https://github.com/jamesbechet/element-picker) | Vanilla JS: hover highlight, a callback on click (MIT) | Only the hover and the click: no label, parent and child, freezing, or stopping the app's clicks, which are most of the work; 23 stars, no releases |
| [OpenPicker](https://openpicker.dev/) | A picker for no-code tools, with selectors that skip generated ids and hashed classes (MIT) | Its overlay is a browser extension, so the people would have to install it. Its filtering is worth copying (below) |
| [Viselect](https://github.com/simonwep/viselect) | Drag a rectangle to select many elements | Not needed: Shift-click covers several elements |
| [SnagRelay](https://snagrelay.com/features/element-picker/) | A hosted bug-report product with an element picker | A hosted service: the app's pages would go to a third party |
| [`@medv/finder`](https://github.com/antonmedv/finder) | A CSS selector generator | Just the selector, which is the part worth reusing |

So: hi writes the picker and reuses `finder`. The first slice checks this
table against the current releases again. A framework-agnostic,
permissively licensed picker that can be driven from our own panel would
replace hi's.

**The panel** is the other half of the work: message list, streaming,
Markdown, a growing composer, attachments. [Deep Chat](https://github.com/OvidijusParsiunas/deep-chat)
(MIT) is a `<deep-chat>` web component that works without a framework,
with a custom `handler` for the request and streamed replies, so it could
sit inside `<hi-helper>` instead of a panel hi writes. Slice 1 tries it
and keeps it if it allows chips in the composer, a header with the version,
and messages that arrive from outside (`hi team web post`), and if its
size is acceptable. That size would replace the 50 KB aim for the
script. assistant-ui and other React kits are out: the helper can't
assume the app's framework.

## The chat reaches the lead

The lead answers web chat through Hermes' API server, not through Slack.
That's because the helper's messages can't arrive as Slack messages. hi
would post them with the team's bot, which is the lead's own Slack user,
and Hermes, like most bots, skips its own messages so it never answers
itself. The first slice confirms this against the Hermes version hi pins.

- `hi team up` turns on Hermes' API server in the lead's box
  (`API_SERVER_ENABLED=true`, `API_SERVER_HOST=0.0.0.0`, a random
  `API_SERVER_KEY` in `secrets.json`), published to this machine's loopback
  only, on a port recorded in `run/`. Only the team's service calls it.
- **The team's service** (the broker's process) listens on a second socket,
  `/run/user/<uid>/hi-team-<name>-web`, which each relay routes `/__hi/` to:

```text
GET  /__hi/helper.js                      the script
GET  /__hi/api/status                     team, lead's name, slot, version, people
GET  /__hi/api/conversation?person=…      this person's conversation in this slot
POST /__hi/api/messages                   {conversation, person, text, page, refs}
                                          → the lead's reply as server-sent events
GET  /__hi/api/events?conversation=…      later messages and "new version", as events
```

  The relay adds the slot to each request (`X-Hi-Slot`), since one service
  serves both addresses.
- **One web conversation is one Hermes conversation**, through
  `/v1/responses` with `conversation: "web-<id>"`, streamed. Hermes keeps
  the transcript, and the lead's long-term memory (Hindsight, the bank
  `team-<name>`) is shared with Slack, so what is said in either place is
  remembered in both.
- **What the lead reads.** The service sends the person's text with a
  header and the references, as text the lead can pass on:

```text
[web chat w-12 · Anna (U07ANNA) · preview: dev, v3 + 2 changes
 · /suppliers · "Suppliers – Payments" · 1440×900]

Make this one red when it's overdue

Referenced elements:
#1 span "Paid"
   selector: main table.suppliers > tbody > tr:nth-child(1) > td.status > span.badge
   html: <span class="badge badge-ok" data-testid="status">Paid</span>
   under: main › section "Suppliers" › table.suppliers › td.status
   box: 54×22 at (812, 248); color white on rgb(34,139,34), 12px
```

  The full references are kept as JSON with the conversation.
- **The lead's later messages.** The lead's reply ends when its turn ends,
  but work finishes later. `hi team web post <conversation> <file|->`, run
  by the lead through the broker like its other commands, adds a message to
  a web conversation. The open panel shows it at once, and a closed one
  shows a dot on the button. The hi-team skill tells the lead to use it when
  work asked for in the app is ready to try.

## Everything stays in the channel

A hi team lives in its Slack channel, and its log is the team's record. Web
chat is copied there, so no conversation happens out of the people's
sight.

- The first message of a web conversation starts a thread in the channel,
  posted by hi: "💬 *Anna*, in the app (preview, /suppliers): Make this one
  red when it's overdue", with the references as a short list. Every later
  message, the person's and the lead's, is a reply in that thread.
- In `channel/`, these lines carry `"web": {"conversation": "w-12",
  "person": "U07ANNA", "from": "person" | "lead"}`. `hi team channel --new`
  leaves them out of `channel-watch`, since the lead was in the
  conversation.
- **Approvals.** A web message from a person who may approve counts as an
  approval in the usual way: in a thread where the lead's message came
  first, within a week. So "yes, go ahead" in the app starts the work the
  lead proposed there. The task's brief gets the web conversation, with
  its references, as it gets an approving Slack thread today. **Releases,
  rollbacks, and turning production's helper off or on still need an
  owner's yes in Slack.** In the app, the person's name is only claimed.
- People in Slack can read along. A reply in Slack to the thread is an
  ordinary Slack message to the lead; it doesn't reach the app (Later).

## Dev and production

| | Preview (dev, or a task's branch) | Production |
|---|---|---|
| Default | on | on |
| Turned off or on by | anyone who may ask for work | an owner, with a yes in Slack |
| Command | `hi team helper preview off` | `hi team helper production off --approval <id>` |

- The setting is `"helper": {"preview": true, "production": true}` in
  `team.json`, and the broker keeps `run/<slot>.helper` in line with it.
  The next page load has the helper or doesn't; no release, no restart.
- An owner asks the lead in Slack ("turn the chat off in production before
  we give the link to the whole finance team"). The lead asks back, the
  owner says yes, and the lead runs the command with that approval, like a
  release.
- Each release's announcement says whether production has the helper, and
  so do `hi team status` and `hi team versions` ("production v3, helper
  off").
- The setting belongs to the address, not to a release: rolling back to v2
  doesn't turn the helper on again.
- An app that registered a service worker may still load an old page with
  the script tag from its cache. With the helper off, `/__hi/helper.js`
  isn't served, so the tag loads nothing.

## The team folder

```text
web/            the web conversations: one JSON line per message, with its
                references, a file per conversation (w-12.jsonl)
run/<slot>.helper   present while the slot's helper is on
```

`web/` is part of the team's record and goes into snapshots with
`channel/` and `tasks/`.

## The lead's skill

`hi-team` gains a section on web chat:

- Messages headed `[web chat …]` come from the app; the person sees the
  page as described, and the references are what they pointed at.
- Answer in a few sentences: the panel is narrow. Ask about behavior, never
  about code, as in Slack.
- Put the references in the brief (the broker adds the conversation to an
  approved task on its own) and name them in the acceptance list ("the
  status badge on the suppliers page turns red when …").
- When work asked for in the app is on the preview, say so in that
  conversation with `hi team web post`.
- `/__hi/` is hi's; the app never uses it.

## Tests

- The relay adds the tag once to a page load, after `<head>`, and to
  nothing else: not to a fetch of HTML, not inside an iframe, not to JSON,
  not to a `304`.
- A page with no `<head>`, a page streamed in chunks, a page larger than
  64 KB, a gzip-only app, and a strict CSP with a nonce each get the tag (or
  are passed untouched and logged) as specified.
- With `run/<slot>.helper` removed, the next page has no tag and `/__hi/`
  reaches the app. Putting the file back brings it back without a restart.
- `/__hi/api/messages` reaches the lead's API server with the conversation
  name and the header, streams the reply, and copies both into one Slack
  thread with the `web` fields in the log.
- A web "yes" from an asker, after the lead's proposal in the same web
  conversation, approves a task. A web message never approves a release or
  a change to production's helper.
- `hi team helper production off` without an owner's approval is refused.
- In a test page (plain HTML, React, Vue, and a server-rendered form),
  select mode outlines what is hovered, stops the click from reaching the
  app, freezes on click, walks to the parent with `Alt+↑`, and gives a
  selector that finds the same element again.

## First slices

1. **Chat.** The relay's `--route`, `--inject`, and `--inject-if`; the
   helper's button, panel, identity, and conversation; the lead's API
   server; the Slack thread; approvals from the app; `hi team helper`.
2. **Select mode.** Hover, freeze, parent and child, chips, references to
   the lead and into briefs, source hints.
3. **After the reply.** `hi team web post`, the new-version notice, and
   pasting or attaching an image or file to a message (into the task's
   `/box/files`, as Slack files are).

## Later

- **Sign-in.** The person's name is only claimed, and anyone with the link
  can chat with the lead and spend the lead's model budget. Signing in with
  Slack (OpenID Connect) would make a web approval as good as a Slack one.
  Then production could show the helper to some people only.
- **Prompt injection through the page.** The references carry the app's
  text, which may hold data people typed in. The lead reads it as the
  person's context, with the same care as a shared file.
- **A screenshot of the selection**, taken in the page (a DOM-to-image
  library) or by the team's service with a headless browser, so the lead and
  the coder see what the person saw.
- **Slack replies back in the app.** A person in Slack answers in the
  mirrored thread, and the app shows it.
- **Edits in place.** Lovable-style direct changes (text, colors, spacing)
  previewed in the page and sent as a precise request.
- **Hermes' browser control**, so the lead can open the page the person is
  on and look for itself.

## Not in this

- Apps outside a hi team, and members' own dev servers in their boxes.
- Chat with members other than the lead.
- A rate limit or a separate budget for web chat; it is the lead's spend.
