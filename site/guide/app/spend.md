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

## A budget

```sh
hi app budget payments --monthly 200            # dollars a month
hi app budget payments --monthly 200 --daily 20
hi app budget payments --monthly off
```

At the budget, hi's proxies stop the app's calls that cost money, its
agents' and its own code's, each in its API's own error shape with the
reason, so the agents and the app show it. Calls a Claude or ChatGPT plan
pays go on. The app's ops channel hears at 80% and at 100%, and again
when a new month (or day), or a raised budget, lets calls through. The
machine's admins set it in the app's **Spend** on the admin page, too.
The preview's own daily limit (`hi app limit`) stays as it is.

## A key of its own for each app, made by hi

With an OpenRouter management key in the server's vault, hi makes each
app a key of its own at OpenRouter, with the app's monthly budget as the
key's limit there: OpenRouter then stops the app at its budget even if
something went around hi, counts each app apart, and a leaked key is one
app's.

```sh
hi server openrouter --management-key      # asked without echo; made at openrouter.ai/settings/management-keys
hi app key payments openrouter --managed   # a key of its own, made by hi
hi app key payments openrouter --rotate    # a new one; the old one is deleted
hi server openrouter                       # the keys hi made, and what OpenRouter counts on each
```

The management key can't make model calls, so no box ever needs it. A
new budget changes the key's limit; `hi app rm` disables the key, `hi app
up` with the kept folder enables it, and `hi app rm --purge` deletes it.
New apps get one by default, in the wizard and the admin page's New app
(`"openrouter": "managed"` in an answers file). The admin page's Keys
(left panel) sets the management key too.

## A shared key spending outside hi

A shared key the apps use as their OpenRouter key is for them alone.
Every 15 minutes the server compares what OpenRouter counts on it with
what the apps on the machine spent with it; when OpenRouter counts more
than $1 and 10% more in about an hour, something else has the key. The
server's Slack channel hears it, and the Vault tab marks the key: change
it there, and revoke the old one at OpenRouter. An app's own key has the
same check, in [incidents](/guide/app/incidents/). The machine's key
isn't checked: Hermes on the machine uses it outside any app.
