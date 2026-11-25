---
name: hi-dev-say
description: Post a message in the hi app's Slack channel from the dev box. Use when the user asks to tell, notify, update, or ask the team in Slack, such as announcing that something is on the preview or asking people to try it.
---

# Post in the app's channel

```bash
hi dev say "The chart now shows the last day; try it on the preview: <link>"
hi dev say --thread 1792845304.432739 "Fixed, on the preview now"
echo "Several lines" | hi dev say -
```

The message shows in the channel with a :computer: in front, from the
app's bot, so people know it came from the dev box.

- Post only when the user asks you to, and say what you posted.
- Write for the people in the channel, who may not read code: what
  changed for them, and what to try. No branch names or stack traces
  unless they asked.
- Links from `hi dev preview` and `hi dev status` are the ones to share.

## When the app itself sends to Slack

This skill is for you, posting from the dev box. When the app's code
must send something itself, such as a scheduled report or a DM to
someone in the app, don't give the app a Slack token. It POSTs JSON to
`$HI_SAY_URL`, and hi sends it from the app's bot:

```bash
curl -s -X POST "$HI_SAY_URL" -H 'Content-Type: application/json' \
  -d '{"text": "*Morning report*\n…", "to": "U0123ABCD"}'
```

`to` is `"channel"` (the default) or a Slack member ID of someone in the
channel or with a level in the app. Production sends. The preview only
answers `{"sent": false, "note": …}` with what it would have sent.
Without `$HI_SAY_URL` (the dev box, tests), log the message instead.
