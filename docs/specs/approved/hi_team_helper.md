# `hi team` helper specification

Status: Approved (2026-10-15). Slice 1, the chat, is built (v0.57.0); slice
2, select mode, in v0.58.0; slice 3, files, pictures, and the lead's later
messages, in v0.59.0. Since v0.60.0 the chat is only for people signed in
with /hi, and anyone else gets help and feedback:
[hi_team_helper_signin.md](hi_team_helper_signin.md).

Dependencies: [hi_team.md](hi_team.md) (the broker, the lead,
the preview and production addresses, approvals, the channel's log),
[hi_net_expose.md](hi_net_expose.md) (the relay in front of
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
- The relay asks for page loads uncompressed (`Accept-Encoding: identity`;
  no header at all would let Go's transport ask for gzip), so it can find
  where the tag goes. Pages are small, and other requests stay compressed.
  An app that compresses anyway gets no helper on that page.
- The tag goes right after the first `<head …>` tag, else before `<body`,
  else after `<html …>` or the doctype. The relay scans the first 64 KB as
  it streams. A page with none of those in its first 64 KB passes through
  without the helper, and the relay logs it. A streamed page is never held
  back.
- `Content-Length` and a strong `ETag` are dropped from a changed page.
- When the app sends a `Content-Security-Policy` header, the relay adds
  `'self'` to `script-src` and `connect-src` and, when the policy uses a
  nonce, copies it onto the tag. A policy in a `<meta>` tag is not handled.
- The helper's styles go in as a constructed stylesheet
  (`adoptedStyleSheets`), which `style-src` doesn't govern, so a policy
  with `style-src 'self'` leaves the panel styled (since v0.62.2; a
  `<style>` in its shadow root was blocked, and the button sat unstyled at
  the bottom of the page). The pictures of what is pointed at still need
  inline styles: under such a policy they are left out, and the words go
  alone.

`/__hi/` is reserved on team addresses. The hi-team skill tells the lead,
and the lead's briefs tell the coder, never to use it in the app.

Because the helper comes from the same address as the app, its requests
carry the link's cookie. The relay already checks it and strips it before
anything reaches the app or the team's service. Nothing more is needed for
cross-origin requests, and no other key is involved.

## The helper in the page

One script, `helper/helper.js`, in plain JavaScript without a framework
or a build step: hi embeds the file as it is and serves it as
`/__hi/helper.js?v=<the script's hash>`, so `go build` needs no Node and a
browser keeps its copy until the script changes. It aims to be under
50 KB.

- **Isolated.** It mounts one custom element, `<hi-helper>`, at the end of
  `<body>`, with an open shadow root and the highest z-index. Its styles
  live in the shadow root, so the app's CSS can't reach in and the helper's
  can't leak out. It changes nothing else in the app's DOM except the
  outline layer in select mode, which is also inside `<hi-helper>`.
- **The button.** A round button in the bottom-right corner, which can be
  dragged to another corner; the panel opens on that side. The app's
  keyboard shortcuts don't fire while someone types in the panel. The corner, and whether the panel was open,
  are kept per browser (localStorage, wrapped in try/catch).
- **The panel.** A side panel on the right, 400 px wide, over the page. On
  a narrow screen it is a bottom sheet. It shows the lead's name ("payments
  team lead"), which version the page is ("dev, v3 + 2 changes",
  "production v3"), the conversation, and the composer.
- **Who's talking.** The first time it opens, the helper asks "Who are
  you?" and lists the people who may ask for work (the channel's members
  when the team has no askers: the owners and everyone heard in the channel
  in the last two weeks, plus a box to type a name), with names from Slack.
  The choice is kept per browser and can be changed in the panel. This is a
  claimed identity, not a sign-in; see Later.
- **The conversation.** Messages are kept by the team's service, so a
  reload, another page, or another browser shows the same conversation for
  that person and that slot. The pen button in the header, "New
  conversation" in the menu, or `/clear` (or `/new`) typed as a message
  starts a fresh one, so a new change doesn't carry the last one's talk;
  nothing is sent to the lead. The clock button lists the person's past
  chats on that address, the latest first (`GET /__hi/api/conversations`);
  opening one makes it the chat again and ends the one that was (`POST
  /__hi/api/conversation/open`), and the lead still has all of it. The
  lead's replies stream in as they are written, and are shown as a safe
  subset of Markdown (paragraphs, lists, bold, code, links), never as raw
  HTML.
- **Page context, always.** Every message carries the page's path and
  title, the viewport size, and the version the page shows, with or without
  references.
- **The page can follow it.** `<hi-helper>` says where the button is
  (`data-corner`: `tl`, `tr`, `bl`, `br`), whether the panel is open
  (`data-open`), and whether the button is hidden until a reload
  (`data-hidden`), and whether whoever is there can chat with the lead
  (`data-chat`, signed in through Slack), and a `hi-helper` event on
  `window` says when any of them changes. A `hi-helper-send` event on
  `window` with `{ text }` opens the panel on the chat (production's
  Change tab) and sends it; for someone who can't chat, the text waits in
  the panel's box. A new team's welcome page uses both: a message box in
  the middle of the page for someone signed in, which moves over to the
  panel as the message goes, an arrow at the button for anyone else, and
  what the chat is for once it is open ([hi_bun.md](hi_bun.md)).
- **A new version.** When the slot moves to a new commit (a merge reaches
  the preview, a release reaches production), the panel says "A new version
  is up" with a reload button. The lead can say "try it now" and mean it.

## Select mode

The part the people will use the most, and the reason for this spec.

- **Entering.** The ⌖ button in the composer, or `Alt+S` anywhere on the
  page, the panel open or not (it asks "Who are you?" first if it doesn't
  know). A banner at the top of the page says what to do, with ↑, ↓, and
  Done buttons; it sits over the part of the page the panel leaves. The
  panel steps aside while choosing when it would leave less than 480 px of
  the page, and on touch screens. The page itself stays as it is.
- **Hover.** The element under the pointer gets an outline (a box drawn
  over it in a layer that ignores the pointer, not a style on the element),
  a faint fill, and a label with a short name and size: `button.primary ·
  120×32`. Moving the pointer moves the box, at most once per animation
  frame.
- **The app doesn't react.** In select mode the helper takes `click`,
  `pointerdown`, `pointerup`, `mousedown`, `mouseup`, and `submit` in the
  capture phase on `window` and stops them, so a click selects instead of
  pressing the button or following the link. Scrolling still works.
- **Getting it exactly right.** `Alt+↑` and `Alt+↓` (or ↑ and ↓ in the
  banner) move the selection to the parent or back to the child, because
  the element under the pointer is often a `<span>` inside the button you
  mean. A click inside the outlined parent takes the parent; `Enter` takes
  what is outlined. The label is only a label: it never catches a click or
  a tap meant for the page.
- **Taking it.** A click adds the element to the composer as a chip
  (`#1 span "Paid"`), and select mode ends. Shift-click adds more than one
  without leaving; the ones already taken stay outlined and numbered while
  choosing. At most ten per message.
- **Chips.** Hovering a chip outlines its element again if it is still on
  the page (the element itself, or after a reload, its selector). × removes
  the chip. Chips belong to the unsent message; an unsent message survives
  a reload (sessionStorage). A sent message shows its chips too, and
  hovering them outlines the elements in the same way.
- **Touch.** A tap outlines, and "Use this" in the banner takes it.
- **Shadow DOM and iframes.** Elements in an app's open shadow roots can be
  selected (the event's `composedPath()`); their selector is the host's,
  then `>>>`, then the one inside (`status-card >>> .total`). Elements
  inside iframes can't; the iframe itself can.

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
  "landmarks": {"heading": "Suppliers", "section": "Suppliers"},
                                                   // the nearest heading before it, its form
                                                   // label, the region it is in
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
- **Stable names only.** Selectors skip names a build generates and the
  next build changes. finder's own filters already take only word-like
  names (letters and dashes, no short words), which leaves out generated
  ids (`:r3:`, `ember123`) and hashed classes from CSS modules and
  CSS-in-JS (`_title_x7f2a`, `sc-…`, `css-1x2`, `jsx-123`). The helper adds
  Tailwind's utility classes (`flex`, `mt-4`, `bg-green-600`,
  `text-center`), which say how an element looks, not which it is. The
  idea is OpenPicker's. When finder can't settle in 400 ms, the selector
  is the path by position.
- **How it gets to the page.** hi serves finder (`third_party/finder`,
  unchanged) and the helper as one script, in one closure with finder's
  `export`s dropped, so there is still no build step and no global. The
  script's URL is fixed (`/__hi/helper.js`) and served with `no-cache` and
  an ETag, so a new hi's helper reaches pages on their next load, without
  new links.
- **`source`, when the framework gives it away.** Without depending on any
  framework, the helper checks for what development builds leave on
  elements: Vue's `__vueParentComponent.type.__file`, Svelte's
  `__svelte_meta.loc`, a React fiber's component name, and a
  `data-hi-source` attribute any build can add (`src/Badge.tsx:12`). It
  records a file and line,
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
with a custom `handler` for the request and streamed replies. Slice 1
looked at it and wrote its own panel: Deep Chat 2.5.1's bundle is 387 KB
(107 KB compressed), eight times the helper's aim, for every page of every
app, and the header, "Who are you?", the chips, and messages that arrive
from outside are hi's to write either way. hi's whole helper is 25 KB
as it is, before compression. assistant-ui and other React kits are out: the helper can't assume
the app's framework.

## The chat reaches the lead

The lead answers web chat through Hermes' API server, not through Slack.
That's because the helper's messages can't arrive as Slack messages. hi
would post them with the team's bot, which is the lead's own Slack user,
and Hermes' Slack adapter drops messages from bots, its own included
(checked in its source).

- `hi team up` turns on Hermes' API server in the lead's box
  (`API_SERVER_ENABLED=true`, on 127.0.0.1:8642 inside the box, with a
  random `API_SERVER_KEY` kept in `secrets.json`). The lead's network is
  internal, so no port can be published: the team's service starts
  `hi team __leadapi` in the box (`podman exec -d`), which relays a socket
  in the broker's folder (`lead-api.sock`) to it, and starts it again
  whenever the socket stops answering. Only the team's service calls it.
- **The team's service** (the broker's process) listens on a second socket,
  `/run/user/<uid>/hi-team-<name>-web/`, one socket per address
  (`preview.sock`, `production.sock`), which the address's relay routes
  `/__hi/` to, so the service knows which address a request came from. No
  box mounts it:

```text
GET  /__hi/helper.js                      the script
GET  /__hi/api/status                     team, lead's name, slot, version, people
GET  /__hi/api/conversation?person=…      this person's open conversation on this address
POST /__hi/api/conversation/new           end it; the next message starts a new one
POST /__hi/api/messages                   {conversation, person, name, text, page, refs, files}
                                          → server-sent events: message, delta…, done or error
GET  /__hi/api/events?conversation=…&after=n   the chat's messages after n, then each new one
GET  /__hi/api/files/<chat>/<name>        a file of a message
GET  /__hi/snap.js                        modern-screenshot, for pictures in select mode
```

  The relay strips the app's cookies from these requests. The lead's
  answer is kept and copied to the channel even when the browser leaves
  before it ends; the panel looks again every few seconds until it is
  there. A second message while the lead is answering is refused.
- **One web conversation is one Hermes conversation**, through
  `/v1/responses` with `conversation: "web-<id>"`, streamed. Hermes keeps
  the transcript (in `response_store.db` in its home, so it outlives a
  restart), and the lead's long-term memory (Hindsight, the bank
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

  The first message of each chat starts with a short note on how chats in
  the app work: answer briefly, a yes here counts with its message id,
  production needs Slack. The full references are kept as JSON with the
  conversation.
- **The lead's later messages.** The lead's reply ends when its turn ends,
  but work finishes later. `hi team web post <chat> <text|-> [--attach
  <file>]…`, run by the lead through the broker like its other commands,
  adds a message to a web chat, and hi copies it into the chat's thread.
  It is refused while the lead is answering in that chat (say it in the
  answer), and in a chat the person ended (hi names their open one).
  `hi team web` lists the chats of the last week, and `hi team web show
  <chat>` shows one, with message ids, references, and files.
  The panel listens on `/__hi/api/events` (an `EventSource`, which picks
  up where it left off after a reconnect): an open panel shows the message
  at once; a closed one puts a dot on the button and a preview beside it
  for 20 seconds. The browser remembers the last message it showed with
  the panel open, so a page opened later still shows the dot.
  A posted message isn't in the chat's Hermes conversation, so the lead's
  next turn there starts with what it posted since its last answer.

## Files and pictures

- **Attaching.** The panel's 📎 button, pasting into the message box (a
  screenshot from the clipboard), or dropping files on the panel: up to 6
  files a message, 20 MB each, 32 MB in all with the pictures. A picture
  larger than 2400 pixels across or 4 MB is made smaller in the browser
  first. A message may be only files.
- **Kept with the chat**, in `web/<chat>-files/<message>-<name>`, and
  mounted read-only in the lead's box at `/team/web`.
- **What the lead gets.** The message lists each file's path. Pictures
  (PNG, JPEG, GIF, WebP, up to 4 MB, 8 a message) also go to its model
  inline, through the Responses API's `input_image` parts, each after a line
  saying what it is. If the model refuses them, hi asks again without them,
  and the lead opens the files when it needs to.
- **The member gets them.** A task approved in the chat gets the chat's
  files in `/box/files`, like the files of an approving Slack thread; the
  lead attaches others with `--attach /team/web/…`.
- **Shown in the panel.** Pictures as thumbnails, anything else as a
  download. The files come from the app's origin, so only those four
  picture types are served as what they are; everything else, SVG
  included, is served as a download, with `nosniff` and a sandbox CSP.
- **In the channel.** The files follow the message into its Slack thread
  (the bot has `files:write`).
- **A picture of each element pointed at.** In select mode, the helper
  draws the element into a picture with
  [modern-screenshot](https://github.com/qq15725/modern-screenshot)
  (MIT, vendored in `third_party/`, served at `/__hi/snap.js` and imported
  the first time it's needed, so pages that never use select mode don't
  load it). A small element (under 160×48) is drawn with what is around it,
  up to three levels out, and itself outlined in red. The picture shows in
  the chip and goes with the message as `#1`'s; if it takes more than a few
  seconds, the message goes without it.

## Everything stays in the channel

A hi team lives in its Slack channel, and its log is the team's record. Web
chat is copied there, so no conversation happens out of the people's
sight.

- Since 2026-10-19, a chat of someone signed in through Slack is copied into a
  thread in their DM with the bot instead, "💬 You, in the app (...)": a
  copy of every chat in the channel was too much for the team. A chat whose
  person isn't checked, and one whose thread was in the channel already,
  stay in the channel. A yes in it has no thread in the channel to say more
  in, as for a yes in a DM.
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
| Turned off or on by | the lead, when the people ask | an owner, with a yes in Slack |
| Command | `hi team helper preview off` | `hi team helper production off --approval <id>` |

- The setting is `"helper": {"production": false}` in `team.json` (an
  address not named has it on), and the team's service keeps
  `run/<slot>.helper` in line with it. On the machine,
  `hi team helper <name> production off` needs no approval: whoever runs the
  machine runs the team. The change is recorded in `versions.json`'s history
  with its approval, so the same yes can't be used twice.
  The next page load has the helper or doesn't; no release, no restart.
- An owner asks the lead in Slack ("turn the chat off in production before
  we give the link to the whole finance team"). The lead asks back, the
  owner says yes, and the lead runs the command with that approval, like a
  release.
- `hi team status` and the lead's `hi team helper` say whether each address
  has it; turning production's off or on is announced in the channel.
  Saying so in each release's announcement is left for later.
- The setting belongs to the address, not to a release: rolling back to v2
  doesn't turn the helper on again.
- An address made before hi had the helper gets it when its link is next
  renewed (production's every day, a preview's when it has less than 12
  hours left).
- An app that registered a service worker may still load an old page with
  the script tag from its cache. With the helper off, `/__hi/helper.js`
  isn't served, so the tag loads nothing.

## The team folder

```text
web/            the web conversations, a file each (w-12.json): who, which
                address, its thread in the channel, and the messages
web/w-12-files/ the files and pictures of its messages
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
- The pictures of what they pointed at, and their files, come with the
  message; the files are in `/team/web/<chat>-files`.
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
- A message's files are saved, listed for the lead, pictures sent to it
  inline (and sent again without them when the model refuses), uploaded to
  the thread, and copied into an approved task's files. Pictures are served
  as pictures; SVG and everything else as downloads.
- `hi team web post` reaches an open event stream at once, is copied to
  the thread, and is refused while the lead answers in that chat or in an
  ended chat. The next turn in the chat starts with what was posted.
- `helper/test` runs the script hi serves in happy-dom with `bun test`,
  against a fake of the team's service: no button when the service doesn't
  answer, help, feedback, the chat, the lead's words kept as text,
  failures that keep what was typed, select mode, and what it tells the
  page. Go's `TestHelperInABrowser` runs it where Bun is installed, and CI
  whenever the helper changes.
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
3. **After the reply.** `hi team web post`, the event stream, the dot and
   preview on the button; attaching, pasting, and dropping files (into the
   task's `/box/files`, as Slack files are); a picture of each element
   pointed at. (The new-version notice came with slice 1.)

## Later

- **Sign-in.** The person's name is only claimed, and anyone with the link
  can chat with the lead and spend the lead's model budget. Signing in with
  Slack (OpenID Connect) would make a web approval as good as a Slack one.
  Then production could show the helper to some people only.
- **Prompt injection through the page.** The references carry the app's
  text, which may hold data people typed in. The lead reads it as the
  person's context, with the same care as a shared file.
- **A sharper picture of the selection.** Drawing the page again in a
  picture isn't always exact (a web font or a line break can differ); a
  headless browser in the team's service could take the real thing.
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
