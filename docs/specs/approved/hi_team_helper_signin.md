# `hi team` helper: feedback for anyone, the chat for people signed in

Status: Approved (2026-10-15), built in v0.60.0. Changes [hi_team_helper.md](../approved/hi_team_helper.md):
who is talking is no longer claimed in "Who are you?".

## Goal

Today the helper asks "Who are you?", and anyone with the team's link can
pick a name and talk to the lead as that person. The approval rules keep the
worst out, but it is impersonation all the same, and anyone with the link
spends the lead's model budget.

Instead:

1. **The ordinary link is for help and feedback.** Anyone who has it can
   ask how the app works and get an answer from the app's guide, or point
   at parts of the page, attach files, and write feedback, which the helper
   sends to the channel. The lead isn't in it and nothing changes.
2. **People who may change the app sign in through Slack.** `/hi` in the
   team's channel makes hi send them a private link in a DM. With it, the
   helper is today's chat with the lead, as that person, checked.

## The ordinary link: help and feedback

`https://team-timmai-atsg.eu1.netbird.services/?hi_key=…`

The panel is titled "Help", with two tabs: **Ask** and **Feedback**.

### Ask: how the app works

- Someone asks "how do I add a supplier?" or points at a column and asks
  what it means. hi answers itself, with one call to the lead's model
  through the team's OpenRouter key, **with no tools**: the model can't run
  anything, read the project, or reach Slack, so there is nothing to talk
  it into. It only explains.
- What it knows: the app's guide, `HELP.md` at the root of the version on
  that address (from repo/, at the slot's commit; README.md when there is
  no HELP.md), the page's visible text, its title and path, and the parts
  pointed at. The lead keeps HELP.md current: written for the people who
  use the app, updated with each change they see. hi's skill tells it so.
- The answer streams in, in a few short sentences. When the guide doesn't
  say, it says so. When the person reports a problem or wants a change, it
  points them to the Feedback tab, which takes the question along.
- The conversation lives in the browser tab; the last turns go with each
  question. hi keeps the questions and answers in `web/questions.jsonl`, so
  the lead sees where people get stuck (`hi team web questions`).
- Limits, for the budget: 60 questions a day per address, 400 a day for
  the team; the panel says so when one is reached.

### Feedback

- The Feedback tab has select mode with the
  pictures of what was pointed at, files (paste, drop, the paperclip), and
  a name, which it asks for before sending (the box flashes), marked as
  not checked. It also has a box, ticked by
  default, for "a picture of what I see": the visible page, drawn with
  modern-screenshot when the feedback is sent.
- Sending posts it in the channel as a message of its own, not a thread
  reply: "📝 Feedback in the app (preview, /suppliers), from 'Cleo' (not
  checked): …", with the references and, as replies, the files. It is kept
  as `web/f-12.json` with `web/f-12-files/`.
- **The lead isn't asked.** No model call, so no budget spent and nothing
  to talk the lead into. The feedback lines in `channel/` are new to the
  lead, unlike web chats, so its channel-watch job reads them and keeps
  what matters. When someone says in Slack "@lead do f-12", the lead runs
  `hi team web show f-12`, attaches the files to the task, and the work
  needs a yes as usual.
- The panel says "Sent to the team as f-12. Thank you."
- **Answers.** A reply in the feedback's thread that starts with `>` is
  for the sender; other replies stay in Slack, for the team. The message
  in the channel says so. hi's watch of the channel keeps these answers in
  `web/f-12.json`, without the quote marks, and the panel's Feedback tab
  shows what this browser sent with the answers under each. The button
  gets a dot, and a note says "The team answered your feedback"; the
  panel opens on the Feedback tab. Answers come within the watch's two
  minutes.
- **Who sent it, without knowing who they are.** The first feedback from
  a browser gets a key of its own in a `hi_visitor` cookie: HttpOnly,
  Secure, SameSite=Strict, for `/__hi/` only, lasting 400 days. The relay
  hands it to the helper as `X-Hi-Visitor`, as it does a person's key,
  and the app never sees it. hi keeps a hash of the key with the
  feedback, and `GET /__hi/api/feedback` returns only that browser's.
  A cookie belongs to one address: unless the apps have names that stay
  (`hi team domain`), answers reach the browser only while the address
  it sent from lasts, a day for production.
- A limit against spam: 30 feedback items a day per address, then the
  panel says so.

## `/hi`: a private link

Since v0.61.1 the command is the team's own, `/hi-<team>` (`/hi-timmai`), written `/hi`
below. Slack gives a command that two apps in a workspace share to the
one installed last, so a shared `/hi` reached whichever team was
reinstalled last (and hi server's app has `/hi` too). The plugin answers
plain `/hi` as well, for an app whose manifest still has it. Hermes drops
a slash command typed in a DM before any plugin runs, when DMs are off,
and the bot's DM is where people type it; so the plugin answers its
commands in the Slack adapter's listener itself, wherever they are typed
(since v0.61.3), with the user_id Slack sets.

1. Someone types `/hi` in the team's channel.
2. Hermes holds the app's Socket Mode connection, so the command reaches
   Hermes, which isn't a model call. hi installs a small Hermes plugin in
   the lead's home, `hi-team`, which registers `/hi`. Its handler takes
   the Slack user ID from the command's payload, which Slack sets, so it
   can't be typed, and runs `hi team web link --user U07ANNA` against the
   broker.
3. The broker checks that the person may ask for work: an owner or asker,
   or, with no askers, a member of the channel. Then it makes a key for them,
   32 random bytes, stored in `people.json` beside `secrets.json` (no box
   mounts either) with who it is for, when it was made, and when it ends:
   in 14 days. It is kept as it is, not hashed, because hi writes it again
   into the DM's links when an address changes.
4. hi sends the person a DM from the team's bot, with the preview's and
   production's links, each with `&hi_user=<key>`. The command's own reply
   is private (ephemeral): "I've sent you a private link in a DM." Someone
   who may not change the app gets: "You can send feedback with the app's
   link; ask an owner to add you."
5. `/hi` again makes a new key and ends the old one, so a link that leaked
   dies when its owner asks for a new one.

**The key never reaches the app.** The relay handles `hi_user` the way it
handles `hi_key`. It sets an HttpOnly cookie that is sent only to `/__hi/`,
takes the key out of the address, and passes the cookie to the team's
service as `X-Hi-User` on `/__hi/` requests. The app's own scripts can't
read it, its server never sees it, and a copied address doesn't carry it.

**Signed in, the helper is today's chat**, with "you're Anna (via Slack)"
in the header and no "Who are you?". A yes in the chat approves work as a
Slack yes does, now that the name is checked. Releases, rollbacks, and
production's helper still need an owner's yes in the channel, which keeps
them where the team can see them.

**Production's link changes every day** (it is renewed), and a preview
gets a new address now and then, so a link in a DM goes stale. hi keeps
the DM it sent each person and updates it, as it does its channel message
with production's link, while the key is valid.

**The DM**, for now, is only for hi: links, and the lead's later messages.
When the lead posts in a person's web chat (`hi team web post`) and their
panel isn't open, hi also sends it to their DM, with the link. The lead
doesn't answer in DMs; Hermes is told so already (`SLACK_DISABLE_DMS`),
and the app's Messages tab is read-only, so nobody writes into a void.
(Since v0.62.0 the lead answers DMs from the people who may ask for work:
[hi_team_dm.md](hi_team_dm.md).)

**When /hi isn't there.** "@lead send me a link" in the channel works too:
the lead runs `hi team web link --user <their ID>`, and the link still goes
only to that person's DM, so asking for someone else's gets them nothing.

## The Slack app

`hi team manifest` gains:

- `features.slash_commands`: `/hi`, "A private link to the app's chat with
  the lead", `should_escape: false`.
- The scopes `commands` (for slash commands) and `im:write` (to open a DM).
- `features.app_home`: the Messages tab on and read-only.
- The lead's Hermes gets the plugin in its home (`plugins/hi-team`) and
  listed in `plugins.enabled`, written on every start like its settings.

An existing team's app needs its manifest updated and the app reinstalled.
`hi team up` already says which scopes the bot lacks and how to add them;
until then `/hi` doesn't exist and the helper only takes feedback.

Each team's app has its own command, `/hi-<team>`, so several teams in a
workspace never take each other's.

## What changes for chats so far

- "Who are you?" goes away, and so does `GET /api/status`'s people list.
- Web chats made with a claimed name stay in `web/` as they are, and are
  no longer offered.
- `GET /api/status` says whether this browser is signed in, and as whom.
  Messages with a key that ended get "Your link has ended: type /hi in
  Slack for a new one", and the panel turns into the feedback form.

## Tests

- Without a key, `/__hi/api/messages` is refused and `/__hi/api/feedback`
  posts a top-level channel message without calling the lead.
- `/__hi/api/help` answers from HELP.md at the slot's commit, with no tools
  in the request, keeps the question, and stops at the limits.
- `/hi` from an asker DMs a link; from someone else, it DMs nothing and
  replies privately. A new `/hi` ends the old key.
- The relay turns `hi_user` into a `/__hi/`-only HttpOnly cookie, takes it
  out of the address, and sends it to the service as `X-Hi-User`; the app
  never gets the cookie or the header.
- An ended key, or one for someone no longer an asker, signs nobody in.
- A web yes from a signed-in asker approves a task; a release still needs
  Slack.

## Later

- **Developing in a DM.** People who may change the app talk to the lead
  in their DM, one to one, so the channel stays for the group. Larger
  changes are still announced in the channel: what changed in the latest
  updates and who asked for it, so the channel stays the team's record.
- **Production for some people only.** With people signed in, production's
  helper could be feedback-only for everyone else.
