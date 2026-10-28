---
title: Help and feedback in the app
description: "The button on every page of a hi team's app: help from the app's guide, feedback to the team's channel, and, for people signed in with their own link, bug reports filed as tasks."
---

Every page of the preview and of production has a round button in the
bottom-left corner; drag it to another corner if it covers something. The
panel opens on its side, and you can drag the panel's edge to make it
wider. It has help and feedback, for anyone with the app's link:

- **Ask** how something works, and get an answer from the app's
  [user docs](/guide/team/docs/) (`HELP.md` in a project from before
  them), which the owners' agents keep current with each change. The answer
  comes from hi, not the lead, through a model with no tools: it only
  explains, so there is nothing to talk it into. Up to 60 questions a day
  per address, 400 for the team. The pen button at the top, or `/clear`,
  starts a new page of questions.
- **Docs**, between Ask and Feedback, opens the app's
  [docs](/guide/team/docs/) at `/docs` in a new browser tab, so the page
  you were on stays as it was. An address on a relay from before `/docs`
  has no Docs button.
- **Feedback** goes to the team's channel as a message of its own,
  "Feedback f-12", with what you pointed at, your files, and a picture of
  the page as you see it (untick it to leave it out). It asks for your
  name, marked as not checked. It is for the people in the channel to
  handle: nothing files it as a task on its own, and the lead files it
  only when one of them asks.
- **Report**, in place of Feedback, for someone signed in with their own
  link (below): what's wrong, what you pointed at, your screenshots and
  files. hi files it as a task at once, with your name, the page, the
  version you're on, and your browser, and says its number ("Filed as
  #31"); the channel hears ":lady_beetle: @Anna reported #31". The
  helper keeps no list: the developer who fixes it, or the lead when the
  task is closed, says so in that message's thread. Up to 20 reports a
  day per person.
- **Point at what you mean.** The target button beside the message box,
  or `Alt+S` anywhere on the page, lets you click part of the page: it is
  outlined as you hover, `Alt+↑` widens it to the element around it, and a
  click adds it to your message as a chip, `#1 span "Paid"`. Shift-click
  adds more. The app ignores those clicks. The element's text, classes,
  and HTML go with the feedback, so the owners' agents find it in the
  code. On a phone, tap it, then "Use this".
- **Answers**: a reply in the feedback's thread that starts with `>` shows
  up under it in the Feedback tab, in the browser that sent it; other
  replies stay in Slack. The button gets a dot when the team answers. A
  cookie only `/__hi/` gets says which browser sent what; on an address
  that changes each day it lasts until the address does (see
  [Public addresses that stay](/guide/team/app/#public-addresses-that-stay)).

To talk with the lead, people use the team's Slack channel or the bot's
DM: from hi 2.4 the helper has no chat with the lead.

**Talk to the lead in a DM.** A contributor, developer, or owner can write to the
team's bot directly, one to one, so the channel stays for the group. The
lead answers there without being @-mentioned, and files what they ask for
as a task, as in the channel. Anyone else gets no answer. The Slack app needs `im:history`, `im:read`, and the `message.im`
event for this (`hi team manifest <name>`).

**Your own links.** A contributor, developer, or owner types the team's
command, `/hi-<team>` (such as `/hi-payments`), in the channel or the
bot's DM, and hi sends them private links to the app in a DM. Signed in
with them, the helper has Report. Owners and developers get the team's
[dev page](/guide/team/dev/#the-dev-page) with them too, also with
`/hi-<team> dev`. Anyone else hears that an owner adds contributors on
the dev page. See
[Owners, developers, and contributors](/guide/team/dev/#owners-developers-and-contributors).

- The link's key never reaches the app: hi's relay keeps it in a cookie
  only `/__hi/` gets. It works for 14 days; the command again gives a new
  link and ends the old one. When an address changes, hi updates the link
  in the DM.
- Each team's command is its own: Slack gives a command that two apps in
  a workspace share to the one installed last.
- The Slack app needs the `/hi-<team>` command and the `commands` and
  `im:write` scopes. `hi team manifest <name>` prints the manifest that has
  them; `hi team up` says when they're missing.

The app holds none of it. hi adds the button on the way to the browser, in
the relay in front of the team's links, so it works whatever the app is
written in, and `/__hi/` on those links is hi's. To take it out of
production, an owner unticks **In production** under The helper on the
[dev page](/guide/team/dev/#the-dev-page), or an admin runs, on the
machine:

```sh
hi team helper payments                    # where it is on
hi team helper payments production off     # from the next page load; on brings it back
```

A link made before your hi had the helper gets it when the link is next
renewed. An app whose Content-Security-Policy forbids inline styles
(`style-src 'self'`) still gets the button and the panel, but the pictures
of what you point at, and of the page with feedback, are left out there.
