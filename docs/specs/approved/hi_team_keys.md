# `hi team` keys and settings specification

Status: Approved (2026-10-13). Built in v0.53.0 (see [As built](#as-built)).

Dependencies: [hi_team.md](../approved/hi_team.md) (the broker, the lead's
proxy, the app boxes), [hi_box.md](../approved/hi_box.md) (the proxy's
placeholder injection).

## Goal

A team's app can use models, and other services that need a key, without
anyone pasting a key into Slack, a brief, or the code.

Team bulletin needed OpenRouter for its comic images. The app box has no
credentials and no way to the team's proxy, so someone gave the lead a key
in Slack, the lead put it in four briefs, and the coder committed it. Every
member's box and every copy of the project can read it now, and it stays in
git history.

## What there is today

- The lead's model calls go through the team's proxy, which swaps a
  placeholder for the OpenRouter key in this machine's Hermes `.env`. The
  team has no key of its own and bills the machine owner's.
- An app box (preview, production) runs on its own network, with full
  internet and no proxy. It reaches this machine at
  `host.containers.internal`, and nothing else of the team.

## Commands

```sh
hi team new                              # also asks for an OpenRouter key (Enter: this machine's)
hi team key <name> openrouter            # set or replace the team's OpenRouter key (asked, not an argument)
hi team env <name>                       # list the settings' names, never their values
hi team env <name> KEY=VALUE [KEY=VALUE...]
hi team env <name> KEY                   # asks for the value, so it stays out of shell history
hi team env <name> --unset KEY
```

`hi team new --from <file>` takes the key from `HI_TEAM_OPENROUTER_API_KEY`,
as it takes the Slack tokens. All of it lives in the team's `secrets.json`
(mode 0600), beside the Slack tokens, never in `team.json`, the repo, or a
box's files.

## How a key reaches the work, without anyone seeing it

**OpenRouter.** The team's proxy takes the team's key when it has one, else
the machine's, as now, reading both again for each call. An app box can't
reach the proxy, or this machine's loopback, from its own network, so the
broker serves OpenRouter's API on a Unix socket in a folder the app's box
mounts, and puts the team's key on each call. hi starts the app's box with
hi itself as the first process (`hi team __app`), which relays
`127.0.0.1:3129` in the box to that socket and then runs the app. The box
gets:

```
OPENROUTER_BASE_URL=http://127.0.0.1:3129/api/v1
OPENROUTER_API_KEY=hi-box-placeholder
```

So a coder writes ordinary OpenRouter code (base URL and key from the
environment), it works in previews and production, and the real key is in
no box. The socket is the only way in: nothing listens on the network.
Members' boxes keep their own model access, as now.

**Settings (`hi team env`).** Each setting reaches the app boxes, preview and
production, as an environment variable, and nothing else: not the lead, not
the members, not the briefs. The lead sees the names (`hi team env` in its
box lists them), so it can tell the coder "the app gets WEATHER_API_KEY".
A new setting applies when the app box next starts: the next preview, and
production, which hi starts again on the same link within a minute when
the settings change.

A setting is plainly visible to the app's code, which the agents write: a
coder could read it out in a test. It keeps keys out of Slack, briefs, and
git, which is the point; a key that must never be readable by agent-written
code belongs behind the proxy, like OpenRouter.

## The lead's skill

`hi-team` says: the app gets models through `OPENROUTER_BASE_URL` and
`OPENROUTER_API_KEY`, and other keys as settings the people add with
`hi team env`. When someone offers a key in the channel, the lead asks them
to run `hi team env` (or `hi team key`) on the machine instead, and never
uses it.

## Tests

- `hi team new` stores the OpenRouter key in `secrets.json`, and Enter keeps
  the machine's.
- The proxy and the models socket use the team's key when there is one,
  else the machine's, without a restart.
- An app box gets `OPENROUTER_BASE_URL`, the placeholder, and the settings,
  and no key; the relay in the box reaches the socket and the app's exit
  status is the box's.
- A changed setting starts production again on the same link.
- `hi team env` lists names only; `--unset` removes one.

## Not in this

- Keys for members' boxes beyond models (a member running the app's tests
  against a live service).
- Other providers than OpenRouter behind the proxy.

## As built

v0.53.0, as above, except where the idea differed:

- No app token and no loopback port: an app box reaches neither the team's
  proxy nor this machine's loopback, and a port on the machine's own
  address would be on the network. The models socket, mounted only into
  app boxes, is the capability instead.
- The settings reach the box through `--env-file` (a 0600 file removed once
  the box starts), so they are not on the engine's command line, which
  anyone on the machine can see.
- A slot remembers a stamp of the settings; production starts again when it
  differs. The stamp also changed for every box started by an older hi, so
  production restarts once after the update and gets the models and its
  address file.
