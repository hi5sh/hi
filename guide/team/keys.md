---
title: Keys and settings
description: "Give a hi team its own OpenRouter key, its Claude and ChatGPT sign-ins, new Slack tokens, and the app's settings on the machine or the dev page, so no key is ever pasted into Slack or the code."
---

Nobody pastes a key into Slack: what the lead is given ends up in its
memory and in tasks, and what an agent is given ends up in the code,
where every copy of the project can read it. Keys go to the team on the
machine, or on the dev page, instead.

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
memory (Hindsight) keeps using OpenRouter, with this machine's Hermes model,
a cheap one, unless team.json names one (`"memory_model":
"google/gemini-3.8-flash"`); the helper's answers use the lead's model, through OpenRouter.
hi counts what each of these calls cost from OpenRouter's answer, asking
for it when the caller didn't.

**Claude Code on the team's Claude plan.** Claude Code in the dev box,
and the team's Claude members in a deep review, run on this machine's
Claude sign-in unless the team has a token of its own, from
`claude setup-token` signed in to the account that pays (it lasts a year).
`hi team new` asks for it; for a team that exists:

```sh
hi team signin payments claude             # asked for; it shows as *
hi team signin payments claude --machine   # this machine's sign-in again
```

When Claude refuses (the sign-in stopped working, or the plan reached its
limit), the box's proxy sends the same call to OpenRouter,
with the model's name there (`claude-opus-5-5` is `anthropic/claude-opus-5.5`),
and keeps to OpenRouter for 15 minutes before it tries Claude again. The
channel hears it once, as for the lead. A short rate limit isn't a
refusal: Claude Code waits and retries itself.

**Codex on the team's ChatGPT plan.** Codex in the dev box uses the
team's ChatGPT sign-in (`hi team signin <name> chatgpt`), or this machine's
Codex sign-in when the team has none, and the box holds neither: Codex
there calls ChatGPT through its proxy with a placeholder for a key, and
nothing in the box refreshes a token. When ChatGPT refuses it, the proxy
sends the call to the same model on OpenRouter and the channel hears it
once. A machine whose Codex is on an API key keeps it, as before.

**Signing in again from Slack.** When a sign-in stops working, the
channel hears it once, and an owner signs the team in again from the
channel or the bot's DM, without the machine:

- `/hi-payments signin chatgpt` sends that owner, and nobody else, a code
  in a DM to type at OpenAI on any device. The team's service waits for
  it and keeps the new sign-in; the DM says when it's done.
- `/hi-payments signin claude` sends a one-time page on the team's app,
  good for 15 minutes, where the owner pastes a token from
  `claude setup-token`. The token never goes through Slack.

Only owners may: a sign-in decides whose subscription pays. On the
machine, `hi team signin payments chatgpt|claude` does the same.

**How hard the lead thinks.** The lead thinks at medium effort unless
team.json says otherwise; hi passes it to Hermes (`reasoning_effort`),
since the machine's own settings don't reach its box:

```sh
hi team member payments lead --effort high
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
environment variables, and nothing else: not the lead, not the dev box.
The lead and the dev box's agents see their names, so the code reads the
right variable.

```sh
hi team env payments WEATHER_API_KEY=…     # or WEATHER_API_KEY alone, to be asked
hi team env payments                       # their names
hi team env payments --unset WEATHER_API_KEY
```

Some services take a file instead: a certificate with its private key,
such as a MongoDB Atlas X.509 user's `.pem`. Give it as a file; hi keeps
it, and the app's boxes get it read-only, with its path in the variable:

```sh
hi team env payments --file MONGODB_CERT=~/Downloads/X509-cert-123.pem
```

In the app, `$MONGODB_CERT` is `/hi-files/MONGODB_CERT`; hand that path to
the driver (`tlsCertificateKeyFile` for MongoDB). The file you gave isn't
needed on the machine afterwards: keep the original in a password manager
or vault. Like a value, it can be for the preview or production only
(`--preview`, `--production`), and `--unset` removes it. It never reaches
the dev box or the repository, and snapshots don't copy it.

The next preview has a change, and hi starts production again on the same
link within a minute. A setting is readable by the app's code, which the
agents write; the OpenRouter key and the Slack tokens never are.

Owners set them on the [dev page](/guide/team/dev/#the-dev-page) too,
under Security, **The app's settings**: a name, a value that is never shown again (or a file), and whether it
is for both, the preview only, or production only. In the dev box,
`hi dev settings KEY=VALUE` sets only the preview's own value.

The lead itself never joins your NetBird network: the machine runs the
app and its addresses. The dev box does, as a peer of its own that only
the owners reach (see [Joining NetBird](/guide/team/dev/#joining-netbird)).

New team repositories tell the agents to give the project a `make run`
that listens on `0.0.0.0` at `$PORT`, so every project starts the same
way.
