---
title: Help and chat in the app
description: "The button on every page of a hi team's app: help from the app's guide, feedback to the channel, and, for the people who may ask for work, a chat with the lead that can point at parts of the page."
---

Every page of the preview and of production has a round button in the
corner. What it opens depends on who you are.

**Anyone with the app's link** gets help and feedback:

- **Ask** how something works, and get an answer from the app's
  [user docs](/guide/team/docs/) (`HELP.md` in a project from before
  them), which the lead keeps current with each change. The answer
  comes from hi, not the lead, through a model with no tools: it only
  explains, so there is nothing to talk it into. Up to 60 questions a day
  per address, 400 for the team.
- **Docs**, between Ask and Feedback, opens the app's
  [docs](/guide/team/docs/) at `/docs` in a new browser tab, so the page
  you were on stays as it was. In a preview's chat, which has no tabs,
  it is in the ⋯ menu. An address on a relay from before `/docs` has
  no Docs button.
- **Feedback** goes to the team's channel as a message of its own,
  "Feedback f-12", with what you pointed at, your files, and a picture of
  the page as you see it (untick it to leave it out). It asks for your
  name, marked as not checked. Nothing changes until someone in the
  channel asks the lead to "do f-12", and that needs a yes as any work
  does.
- **Answers**: a reply in the feedback's thread that starts with `>` shows
  up under it in the Feedback tab, in the browser that sent it; other
  replies stay in Slack. The button gets a dot when the team answers. A
  cookie only `/__hi/` gets says which browser sent what; on an address
  that changes each day it lasts until the address does (see
  [Addresses that stay](/guide/team/app/#addresses-that-stay)).

**People who may ask the team for work** type the team's command in the
channel, `/hi-<team>`, such as `/hi-payments`. hi
sends them a private link in a DM, and with it the button opens a chat
with the lead, as them, checked through Slack. On a preview the panel is
the chat. On production, where most people only use the app, it opens on
Ask, with Feedback (under their Slack name, no name to type) and a
**Change** tab for the chat; that tab says a change goes into dev first,
with a preview link to try it, and reaches production when an owner
releases it.

- The link's key never reaches the app: hi's relay keeps it in a cookie
  only `/__hi/` gets, so neither the app's server nor its scripts see it.
  It works for 14 days; the command again gives a new link and ends the
  old one.
- When an address changes, hi updates the link in the DM.
- Each team's command is its own: Slack gives a command that two apps in
  a workspace share to the one installed last. Type it in the team's
  channel or in the bot's DM; hi answers it there, privately, and the lead
  still ignores DMs otherwise.
- The Slack app needs the `/hi-<team>` command and the `commands` and `im:write`
  scopes. `hi team manifest <name>` prints the manifest that has them;
  `hi team up` says when they're missing. Until then, ask the lead in the
  channel to send you your link.

**Talk to the lead in a DM.** People who may ask for work can also write
to the team's bot directly, one to one, so the channel stays for the
group. The lead answers there without being @-mentioned, and a yes in the
DM approves its work; hi checks that yes in the DM itself. Work started
from a DM goes to the owners in a DM of their own, with who asked, not to
the channel; the channel sees it in the summary before the next release,
and each release says who asked for what it holds. Releases still need an owner's yes in
the channel. Anyone else's DM gets no answer. The Slack app needs
`im:history`, `im:read`, and the `message.im` event for this
(`hi team manifest <name>`).

In the chat with the lead:

- The lead answers as it does in Slack, with the same memory, and knows
  which page you are on and which version you see.
- **Point at what you mean.** The target button beside the message box,
  or `Alt+S` anywhere on the page, lets you click part of the page: it is outlined as you
  hover, `Alt+↑` widens it to the element around it, and a click adds it to
  your message as a chip, `#1 span "Paid"`. Shift-click adds more. The app
  ignores those clicks. The lead gets each element's text, classes, and
  HTML, and the coder finds it in the code from them. On a phone, tap it,
  then "Use this". A picture of each part you point at goes with it, so
  the lead sees what you saw.
- **Show it.** Paste a screenshot into the message box, drop files on the
  chat, or use the paperclip: up to 6 files a message. The lead sees the
  pictures, and the coder gets the files with the work.
- **The lead comes back.** When work you asked for is ready to try, the
  lead says so in the same chat. If the chat is closed, the button gets a
  dot and a preview of the message.
- hi copies each chat into a thread in your DM with the lead in Slack,
  files included, so you have it there too. The channel stays quiet.
- **The lead comes back** in your DM too, when it posts in your chat and
  you don't have the app open.
- A "yes" in the chat starts the work the lead proposed there, as one in
  Slack does. A release, a rollback, or turning production's chat off
  still needs an owner's yes in Slack, where the team sees it.

The app holds none of it. hi adds the button on the way to the browser, in
the relay in front of the team's links, so it works whatever the app is
written in, and `/__hi/` on those links is hi's. To take it out of
production, an owner asks the lead in Slack, or on the machine:

```sh
hi team helper payments                    # where it is on
hi team helper payments production off     # from the next page load; on brings it back
```

A link made before your hi had the helper gets it when the link is next
renewed. An app whose Content-Security-Policy forbids inline styles
(`style-src 'self'`) still gets the button and the panel, but the pictures
of what you point at, and of the page with feedback, are left out there.
