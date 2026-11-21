# The pages' design, from Orca

Status: Approved (2026-11-21): release 1, Orca's look on the admin page
and the dev page alike (its colours by default, its chat box, its other
elements), built in v2.6.230; its questions answered the same day (Geist
served with the pages, the helper the same look, Orca's terminal, the
theme named Orca), built in v2.6.231; release 2 built on main.

Dependencies: [hi_admin.md](hi_admin.md) (the admin page),
[hi_app_dev.md](hi_app_dev.md) (the dev page),
[hi_app_dev_orca.md](hi_app_dev_orca.md) (what the dev page takes from
Orca's features; this is its look).

## Why

[Orca](https://github.com/stablyai/orca) (MIT) draws its app on
[orca.dev](https://orca.dev/) in HTML and CSS: the hero's app, and the
twelve demos of "Agent-first, end to end." (Orca CLI, Split Anything,
Jump Anywhere, Drag Files to Agents, Design Mode, Rich Previews, Mobile
Emulator, Dictation, Usage & Accounts, Automations, Computer Use,
Plugins). They are a clear, quiet design for people working beside
agents, which is what both of hi's pages are.

hi's pages already share one stylesheet (`helper/dev/dev.css`, the admin
page adds `helper/admin/admin.css`) and one theme picker (`theme.js`),
from Paseo's themes, Ghostty's by default. They had drifted apart in
places, and the default was an editor's blue-gray rather than a design
of its own.

## What Orca's app looks like

Measured from the page's own elements in Chromium on 2026-11-21 (the
hero and the twelve demos; their `--p-*` tokens and computed styles).

| What | Orca |
|---|---|
| The work (chat, panes) | `#151515` |
| Tab bars, worker cards, the status bar | `#171717` |
| The left panel | `#2a2a2a`, lighter than the work, a line of white at 7% on its right |
| Lines | white at 7%; inputs' white at 10–15% |
| The message box | white at 4% on the work, a line of `rgba(240,240,240,.09)`, corners 14px |
| Your message | `#252525`, a line of white at 6%, corners 14px, at most 80% wide |
| Words | `#f0f0f0`; the agent's prose `#dadada`; quiet `#acacac`; quieter `#929292`; monospace `#c0c0c0` at 12px |
| The one action | near-white (`#e5e5e5`, words `#171717`): Send, Download, Add |
| State | working a yellow (`#eab308`) turning ring; waiting orange; done green (`#10b981`); mentions and glows sky (`#7dd3fc`) |
| A chosen row | white at 10%, outlined in white at 14–24%, corners 8–10px |
| Counts | a pill of `#353535` with a hairline |
| Usage bars | gray; yellow when high |
| Popovers | `#171717` at 96%, a line, a deep shadow; overlays black at 50% |
| Type | its own sans (a Geist), 13px in lists, 14px in the chat; SF Mono |
| The terminal | Ghostty's `#282c34`, in the neutral app |

The chat: your message a bubble on the right; the agent's steps folded
under one line ("Worked for 4s ›") with a hairline under it the width of
the chat; each step a row of its icon, its verb, and what it touched in
monospace, with `+6` in green for an edit; while it works, a turning
ring and words with a light running through them ("Working…"). The box
below: what to say on top; `+` on the left; the model, a microphone,
and a round Send on the right, near-white once there is something to
send.

## Review: the two pages against it

Found on 2026-11-21, on both pages in Chromium (a desktop and a phone),
before release 1:

1. **The default** was Ghostty's blue-gray, an editor's colours, the
   left panel a step darker than the work. Release 1: Orca's.
2. **The chat's steps** were a comma list ("Read a file, Searched,
   Edited a file ▸") over a rule down their left, each step its tool's
   name and a line in the same face. Release 1: Orca's rows.
3. **While the agent works** the line was Claude Code's own, orange and
   monospace, a glyph cycling in JavaScript, in every theme. Release 1:
   a ring in the theme's working colour and a running light, still for
   whoever asks for less motion.
4. **The message box** had a paperclip, and the model and effort on the
   left, far from Send. Release 1: `+`, and the picks beside Send.
5. **The one action** was the theme's accent (blue, green, orange)
   beside status colours of the same loudness. Release 1: near-white in
   Orca's; colour is for state.
6. **Working dots** pulsed blue: a colour only. Release 1: a turning ring,
   which reads as busy in any theme and to the colour-blind.
7. **Cards:** the admin page's Home cards were the left panel's colour,
   the dev page's a lift off the work. Release 1: the lift on both, a
   stronger line under the pointer.
8. **Inline code:** gold on the admin page, a neutral chip on the dev
   page. Release 1: the chip on both.
9. **Links that are buttons:** the admin page had its own, outlined,
   with smaller corners. Release 1: the dev page's.
10. **Signing in:** a card on the admin page, words alone on the dev
    page. Release 1: the card on both.
11. **Meters** were green when all was well. Release 1: gray, then the
    warning's or the error's colour, as Orca's usage bars.
12. **Warnings in the middle** (an app not reachable yet, a paused app)
    were amber words. Release 1: Orca's tinted callout, readable words.
13. **Weights:** 600 in a few admin places, 500 everywhere else.
    Release 1: 500.
14. **The wall screen** loaded no theme, so it was Claude's colours.
    Release 1: the default.
15. **Type** is the computer's own, so the pages differ from a Mac to a
    Linux desktop, and none is Orca's. v2.6.231: Geist (question 1).
16. **The admin page's middle**: an app's Overview is labels and loose
    rows (Addresses, Actions, The app, Boxes), where the dev page puts
    the same kind of thing in cards (Release's where, flow, and
    releases cards). Release 2.
17. **The admin page's left list**: an app's name, then "down" in the
    same line; the dev page's lists and Orca's give the name a line and
    the state a quiet second line or the row's end. Release 2.
18. **"Worked for"**: Orca's fold says how long the agent worked; hi's
    says what it did. hi has the time of each message, so it could say
    both. Release 2.
19. **The helper** on the app's own pages (`helper/helper.js`) and the
    app's docs page (`helper/docs/docs.css`) have colours of their own.
    v2.6.231: the helper (question 2); the docs page, release 3.
20. **The terminal** takes the work's colour; Orca keeps Ghostty's in
    its neutral app. v2.6.231: Ghostty's, as Orca (question 3).

## Release 1: Orca's look (v2.6.230)

- **Orca's colours, by default** (`dev.css`, `theme.js`): a theme of
  its own, first in the picker after Auto, and Auto's dark. Its lines,
  inputs, and cards are white at a few percent, so they show on the
  lighter left panel and on the work alike. A theme someone picked
  stays picked; Ghostty and the others stay to pick. The dev page's
  install manifest and `theme-color`, and the wall screen, are Orca's.
- **The chat:** 14px words; your messages Orca's bubble; each group of
  steps under one line with a chevron that turns, and a hairline under
  it; each step its icon (read, edit, run, find, web, agent, to-do,
  question), its verb, and what it touched in monospace; the thought's
  fold keeps its rule. While the agent works: a ring in the theme's
  working colour, and what the terminal says with a light running
  through it (none for less motion).
- **The message box:** corners of 14px; `+` to attach; the model and
  effort, or the agent to start, beside Stop and Send on the right; a
  quiet line round it while you write.
- **Shared elements:** a working dot is a turning ring; the chosen row
  in a list (a file, a change, an app, a person) is outlined; counts
  have a hairline; cards lift off the work and their line strengthens
  under the pointer; dialogs and the message box have the same corners.
- **The admin page as the dev page:** Home's cards, inline code, links
  that are buttons, the sign-in card (now on the dev page too),
  weights, meters, and warnings, as above.

## Release 1's questions, answered (v2.6.231)

- **Geist** (question 1): Vercel's Geist and Geist Mono, variable, some
  70 KB each (`third_party/geist`, OFL), served beside `dev.css` on the
  admin page and the dev page and under the helper's `/__hi/`, asked for
  by version (`geist.woff2?v=1.9.2`) and kept a year. The terminals keep
  the computer's monospace, as Orca's do, so xterm's grid never waits for
  a font.
- **The helper** (question 2): Orca's look, whatever the app's or the
  computer's colours, as the pages' default: the panel near-black; your
  message a bubble and the answer plain words; a turning ring while it
  answers; the box a card with the point-at button, `+` to attach on the
  Feedback tab, and Send round and near-white; the round button dark with
  an orange dot for news; the app's outlines blue. Geist comes through
  the FontFace API, by names of hi's own (`hi-geist`), so an app's
  Content-Security-Policy that refuses a `<style>` still lets it in, and
  the app's own styles never pick it up.
- **The terminal** (question 3): Orca's theme draws the terminal as Orca
  does, in Ghostty's colours: `#282c34`, white words, Ghostty's palette,
  bright black lifted for agents' quieter lines.
- **The name** (question 4): Orca.

## Release 2: the admin page's middle as the dev page's (built)

- An app's Overview in cards (admin.css's `.admin-cards`): **Where it
  is** (its addresses and dev page, with the not-reachable callout) and
  **What to do** (its actions, Rename and Remove, Tell the lead) side by
  side where there's room; **What it is** (its lead, members, channel,
  owners, snapshots, heartbeat), **Boxes**, and **Releases** the full
  width; one column on a phone. The server's Overview the same: **The
  server**, **Network**, **Model spend**.
- The left list's rows as Orca's sidebar: the name, its state quiet at
  the row's end, what the app is for on a second line; the open one
  outlined, with Open ▾.
- The chat's fold says how long the steps under it took, from what came
  before them (your message, or the agent's last words) to their last
  step, from the transcript's times: "Worked for 2m 14s · Read a file,
  Edited 2 files". A transcript without times says what they did alone.

## Release 3: the docs page

The app's docs page (`helper/docs`) on the same tokens and Geist, so the
app's own pages look like hi's, as the helper does since v2.6.231.

## Release 4: demos (later)

How Orca's demos are made, for hi's own later (an admin setting up an
app; then a developer's day on the dev page):

- Each is a stage of the app's real markup, at its real size, scaled
  into a card, playing a timeline: a pointer that moves and clicks (a
  ring that grows and fades, 460 ms), words typed with a caret, panes
  that fade in (150 ms), a spinner (0.9 s a turn), the running light
  over "Working…" (2 s), overlays at black 50%. Each card loops, and
  holds still for whoever asks for less motion.
- hi's site already plays scenes from definitions, with no video and no
  library ([story-animation.md](../../story-animation.md):
  `site/assets/story.js`, terminal and channel screens). A demo of the
  pages is a new kind of screen there that draws the admin page and the
  dev page from their own `dev.css` and `admin.css`, so what the site
  shows is what the pages are. Release 1's shared tokens are what make
  that possible.

## Not taken

- Orca's name, logo, mascots, and window chrome: the theme's name in the
  picker says where its colours come from, as Paseo's and Ghostty's do.
- A bar a pane: hi's one bar spans the page and its panels.
- Full access, the microphone, and dictation in the message box: the
  agents work in their box with what the app allows; nothing to switch.
- Orca's 36px bars: hi's bar stays 48px, for fingers.

## Tests

- Both pages in Chromium (`hi server __page`'s handler on demo apps, and
  the dev page's files behind a stand-in API with a chat), at 1440 and
  390 pixels wide, in Orca, Light, and Ghostty: the chat with its steps
  folded and open, the message box empty and written in, Start
  something new, the admin page's Home, an app, New app, and both
  sign-ins.
- A step whose tool hi doesn't know gets the plain dot icon.
- Less motion: no turning ring or running light, the ring whole.
- Geist on all three: the admin page's, the dev page's, and the helper's
  `/__hi/` serve both fonts without a sign-in, as `font/woff2`, and
  `dev.css` and the helper ask for the same version.
- The helper in Chromium on an app whose policy is `default-src 'self';
  style-src 'self'`: Geist loads, Enter and Send both ask, and
  happy-dom's tests pass on the bytes hi serves.

## Questions

All four answered (2026-11-21), above under release 1's questions.
