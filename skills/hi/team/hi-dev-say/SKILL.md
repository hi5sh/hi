---
name: hi-dev-say
description: Post a message in the hi team's Slack channel from the dev box. Use when the user asks to tell, notify, update, or ask the team in Slack, such as announcing that something is on the preview or asking people to try it.
---

# Post in the team's channel

```bash
hi dev say "The chart now shows the last day; try it on the preview: <link>"
hi dev say --thread 1792845304.432739 "Fixed, on the preview now"
echo "Several lines" | hi dev say -
```

The message shows in the channel with a :computer: in front, from the
team's bot, so people know it came from the dev box.

- Post only when the user asks you to, and say what you posted.
- Write for the people in the channel, who may not read code: what
  changed for them, and what to try. No branch names or stack traces
  unless they asked.
- Links from `hi dev preview` and `hi dev status` are the ones to share.
