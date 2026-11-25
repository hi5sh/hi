---
name: hi-dev-web
description: Know the web around this hi app - the dev page the people talk to you on (its chat, panels, and Preview), the app's addresses (production, the preview, the dev page), what sits in front of them (hi's relay, an IT team's proxy or sign-in), and why a page won't show inside the dev page's Preview. Use when the user mentions the dev page, "the preview", "the view window", a panel, a tab, or a link, when the app doesn't load or show somewhere, when something says "blocked", "refused to connect", "Forbidden", or a login page appears, or before changing headers, cookies, CORS, redirects, or frames.
---

# The web around the app

You can't see the people's browser, and the box can't open the app's
addresses. Find out what you can before you ask, and ask in their words.

```bash
hi dev web
```

It prints, as the machine sees it: the dev page, production's and the
preview's addresses, how they're reached, what each name points at (an
IT team's proxy, say), what each answers and says about frames, which
addresses the dev page's Preview may show, and the last thing the
Preview couldn't show and where the frame was sent instead.

## Where the people see you

The owners and developers work on the app's **dev page**, in their
browser (an address of its own, `/__hi/dev/`):

- **Chat** is this session, as a conversation; **Terminal** is the same
  session as a terminal. What they type there reaches you as typed.
- A panel on each side. The left: **Tasks**, **Workspaces**, **Files**,
  **Search**, **Checks**, **Decisions**, **Users**, **Keys**. The right:
  **Source control**, **Logs**, **Preview**, **Publish**, **Data**,
  **System**.
- **Rewind** is on each of their messages in Chat (the ↶ under it, by
  Copy and the time): back to before that message, the code and the
  chat, the chat, or the code (Claude Code's `/rewind`).
- **Preview** shows the preview (Stage) or production *inside the page*,
  in a frame, with a drop-down to pick which, ↻ to load it again, ↗ to
  open it in a tab of its own, and ⤢ for the whole screen. "The view",
  "the view window", "the preview pane", or "the panel" usually means
  this.
- **Publish** releases dev to production; only people do that.

## The addresses

- **Production** and **the preview** (Stage, also called the preview)
  each run the app in a box of their own; `hi dev status` gives their
  versions and links. The **dev page** is a third address.
- **hi's relay** is in front of the app at each address: it lets in who
  may, adds the help button (the helper) to the app's pages, and keeps
  `/__hi/` for itself. The app never sees a link's key. Don't build
  routes under `/__hi/`.
- **In front of hi** there may be more, which the organisation's IT team
  keeps: names of their own for each address, their reverse proxy
  (NetBird's or another), ending TLS and often asking people to **sign
  in** before they reach the app. `hi dev web` says when a name points at
  one. That sign-in is theirs, not the app's: the app's code can't
  change it, and its pages look like a login page or "Forbidden".
- **From the box** the addresses answer 403 or nothing: its proxy and
  their sign-in keep it out. That says nothing about what people see.
  To check the app's own behaviour, run it in the box (`make run`) and
  request it on localhost.

## When the Preview won't show the app

A page shows in the Preview only if both sides allow it:

1. **The dev page** frames only the preview's and production's own
   addresses. When the address sends the frame on elsewhere, such as a
   sign-in page on another name, the browser blocks it. Chrome and Edge
   usually say "This content is blocked. Contact the site owner to fix the
   issue." The dev page says where it was sent, under the Preview's
   row, and `hi dev web` shows it too.
2. **The page shown** can refuse frames with `X-Frame-Options` or a
   CSP `frame-ancestors`. Chrome usually says "<name> refused to connect."
   `hi dev web` shows those headers as the machine gets them.

What to do:

- **A sign-in in front** (most often): the Preview then offers **Sign in
  and come back**. The whole tab goes through the app's addresses (hi's
  `/__hi/return/` on each), the proxy signs it in there and keeps its
  session in a cookie of the address's own, and the tab comes back to
  the dev page, whose frame then has the cookie. Point the person at the
  button. If the Preview says the frame is still sent on after that, the
  sign-in didn't take there (not finished, or the proxy doesn't let their
  account into that address: their IT team's to change), or the browser
  keeps the sign-in from the frame (the dev page and the address on two
  different sites, or a browser setting). ↗ opens the address in a tab of
  its own to sign in there, then ↻ on the Preview. Don't change the app
  for it.
- **The app refuses frames**: allow only the dev page, with
  `Content-Security-Policy: frame-ancestors 'self' <the dev page's
  origin>`, and only if the owners want the Preview to show it. Never
  remove a frame protection for everyone.
- **A page opened before the address changed** frames only the addresses
  it knew: the dev page says to reload it.

## Asking the people

Ask what you can't find out, in a few concrete questions: which address
(production or the preview), what the page or tab shows, word for word
or as a picture, and whether ↗ in its own tab works. Don't ask which
browser or which window when `hi dev web` already says.
