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

**The lead on a ChatGPT plan.** The lead can run on a ChatGPT plan the
company already pays for, instead of OpenRouter by the token. `hi team new`
asks who pays for it; for a team that exists:

```sh
hi team signin payments chatgpt                  # a code to type on any device
hi team member payments lead --provider chatgpt  # the plan's best model
hi team member payments lead --provider chatgpt --model gpt-6-luna
```

The sign-in is the team's own, apart from Codex's on the machine, so
neither ever spends the other's single-use refresh token. Only the team's
service refreshes it, and no box holds it: the team's proxy puts the
token on the lead's calls to ChatGPT. `hi team signin payments chatgpt
--machine` uses the machine's sign-in (Codex's) instead, which Codex
keeps fresh.

When ChatGPT refuses the lead (the sign-in stopped working, or the plan
reached its usage limit), the lead carries on with the same model on
OpenRouter, billed per token, and the channel hears it once, with what to
do. It goes back to ChatGPT on its own when it can. `hi team status` shows
a role on its fallback. A model the plan has and OpenRouter hasn't gets a
fallback of your choice: `--fallback <model on OpenRouter>`. The lead's
memory and the helper chat keep using OpenRouter, with that fallback model.

**The coder on the team's Claude plan.** Claude members run on this
machine's Claude sign-in unless the team has a token of its own, from
`claude setup-token` signed in to the account that pays (it lasts a year).
`hi team new` asks for it; for a team that exists:

```sh
hi team signin payments claude             # asked for; it shows as *
hi team signin payments claude --machine   # this machine's sign-in again
```

When Claude refuses a member (the sign-in stopped working, or the plan
reached its limit), the member's proxy sends the same call to OpenRouter,
with the model's name there (`claude-opus-5-5` is `anthropic/claude-opus-5.5`),
and keeps to OpenRouter for 15 minutes before it tries Claude again. The
channel hears it once, as for the lead. A short rate limit isn't a
refusal: Claude Code waits and retries itself.

**How hard each member thinks.** Every member thinks at medium effort
unless team.json says otherwise; hi passes it to Claude Code (`--effort`),
Codex (`model_reasoning_effort`), and Hermes (`reasoning_effort`), since the
machine's own settings don't reach the boxes:

```sh
hi team member payments reviewer --effort high
```

**Slack.** When Slack gives the app a new token (after a reinstall, or a
token you revoked), give it to the team; it asks for the bot token and
the app token, Enter keeps one, and a running team restarts with them:

```sh
hi team key payments slack
```

The lead's box doesn't hold the Slack tokens either. It has placeholders,
and the team's proxy puts the real tokens on the lead's calls to Slack's
API. For that, the proxy answers the box's connections to `slack.com` and
`files.slack.com` itself, with a certificate from a CA of the team's own,
which the box trusts and which can vouch for `slack.com` only. Every other
site the lead reaches stays a plain tunnel. On a machine without a bundle
of public certificates to add the CA to, hi says so when the team starts,
and the box holds the real tokens, as before v1.2.1.

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
agents write; the OpenRouter key and the Slack tokens never are.

The lead itself never joins your NetBird network: the machine runs the
app and the address, and the lead only asks for them.

New team repositories tell the coder to give the project a `make run` that
listens on `0.0.0.0` at `$PORT`, so every project starts the same way.
