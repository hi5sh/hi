---
title: What an app spends on models
description: "What each hi app's model calls cost through hi, by who, model, and day, whichever OpenRouter key paid, with the calls a Claude or ChatGPT plan paid in tokens."
---

Every model call an app makes goes through hi: its agents' (the lead's,
each member's, the dev box's) and its own code's, on Stage and in
production. No box holds a key; hi's proxy puts it in, and reads what the
answer says the call used and cost. So hi counts what each app spends,
whichever key paid: the app's own, a [shared key](/guide/reference/vault/#shared-keys),
or the machine's.

```sh
hi app budget payments                    # this month
hi app budget payments --month 2026-10    # another
```

says what the app's calls cost this month, by who made them and by
model, with their tokens. Calls a Claude or ChatGPT sign-in paid (a plan,
not dollars) are counted apart, in tokens.

On [the admin page](/guide/app/admin/), an app's **Spend** has the same,
with a bar for each day, and its Overview has the month's total. The
server's Overview lists every app's month.

hi keeps a line per call, with who made it, the model, the tokens, the
cost, and which key paid, by a fingerprint, never the key, and never the
prompt or the answer. The app's service adds them up every minute into
the month's totals, kept 13 months.

## A shared key spending outside hi

A shared key the apps use as their OpenRouter key is for them alone.
Every 15 minutes the server compares what OpenRouter counts on it with
what the apps on the machine spent with it; when OpenRouter counts more
than $1 and 10% more in about an hour, something else has the key. The
server's Slack channel hears it, and the Vault tab marks the key: change
it there, and revoke the old one at OpenRouter. An app's own key has the
same check, in [incidents](/guide/app/incidents/). The machine's key
isn't checked: Hermes on the machine uses it outside any app.
