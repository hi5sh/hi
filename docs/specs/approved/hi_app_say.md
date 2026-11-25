# `$HI_SAY_URL`: the app's messages to Slack, sent by hi

Status: Approved (2026-11-25), built in v2.6.245. Drafted after a task asked
an app to send a weekday Slack report, which would have meant a Slack token
of the app's own.

Dependencies: [hi_app.md](hi_app.md) (the broker, its Slack bot, preview and
production), [hi_app_keys.md](hi_app_keys.md) (the models socket an app's box
already reaches), [hi_app_dm.md](hi_app_dm.md) (who the bot DMs).

## Goal

Apps send messages to their people: a morning report, a reminder, an alert.
Without hi, each app needs a Slack app and a bot token of its own, which its
owners must create, keep secret, and give the right scopes. hi already has
the app's Slack bot. The app hands hi the message instead.

`hi app say` (an admin to the lead) and `hi dev say` (the dev box in the
channel) don't serve: neither is reachable from the running app, neither
DMs, and both mark the message as theirs.

## How

- The app's box has `$HI_SAY_URL`, `http://127.0.0.1:3129/hi/say`: the
  relay that already carries its model calls to the broker's per-slot
  socket. Only processes in the box reach it, never a browser.
- The app POSTs JSON: `{"text": "...", "to": "channel" | "<Slack member
  ID>", "thread": "<ts>"}`. `to` defaults to the channel; `thread` is for
  the channel only.
- hi sends the text as is, from the app's bot: in the app's channel, in a
  thread of it, or as a DM. The answer is `{"sent": true, "channel", "ts"}`.
- **Who gets a DM:** someone with a level in the app, or in its channel.
  Anyone else is refused (403), so an app can't message the workspace.
- **Production sends; the preview doesn't.** The preview checks the message
  as production would and answers `{"sent": false, "note": "...would have
  sent this as a DM to Anna"}`, so a report can be tried on the preview
  without reaching anyone.
- **Limits:** 30,000 characters a message; 60 messages an hour per slot
  (429 past that).
- Refusals answer `{"error": "hi: ..."}` with a status that says why: 400 for
  the request, 403 for the person, 503 when hi isn't connected to Slack, 502
  when Slack refused.

## The dev box

The dev box's instructions say how app code sends Slack messages: through
`$HI_SAY_URL`, never a token of its own; logging instead when the variable
is missing (the dev box, tests); recording each sent report's ts so a
restart never sends it twice.

## Not now

- Files and Slack blocks: text (mrkdwn) only.
- Editing or deleting a sent message.
- Sending from the dev box's own runs of the app.
