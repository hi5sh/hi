---
title: Keys and settings
description: "Give a hi team its own OpenRouter key, new Slack tokens, and the app's settings on the machine, so no key is ever pasted into Slack, a brief, or the code."
---

Nobody pastes a key into Slack: the lead puts what it is given into
briefs, and the coder into the code, where every member and every copy of
the project can read it. Keys go to the team on the machine instead.

**Models.** The app calls OpenRouter's API at `$OPENROUTER_BASE_URL` with
`$OPENROUTER_API_KEY`, in previews and production. That key is a
placeholder: the call goes through a socket to hi's team service on the
machine, which puts the team's real key on it. The lead's own model calls
take the same key, through its proxy. Give the team a key of its own, or
change it, at any time; it applies to the next call:

```sh
hi team key payments openrouter            # asked for; it shows as *
hi team key payments openrouter --unset    # this machine's key again
```

**Slack.** When Slack gives the app a new token (after a reinstall, or a
token you revoked), give it to the team; it asks for the bot token and
the app token, Enter keeps one, and a running team restarts with them:

```sh
hi team key payments slack
```

**Settings.** Other keys and settings the app needs reach its boxes as
environment variables, and nothing else: not the lead, not the members,
not briefs. The lead sees their names, so it can tell the coder which
variable to read.

```sh
hi team env payments WEATHER_API_KEY=…     # or WEATHER_API_KEY alone, to be asked
hi team env payments                       # their names
hi team env payments --unset WEATHER_API_KEY
```

The next preview has a change, and hi starts production again on the same
link within a minute. A setting is readable by the app's code, which the
agents write; only the OpenRouter key never is.

The lead itself never joins your NetBird network: the machine runs the
app and the address, and the lead only asks for them.

New team repositories tell the coder to give the project a `make run` that
listens on `0.0.0.0` at `$PORT`, so every project starts the same way.
