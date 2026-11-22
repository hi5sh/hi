# Sign-in health: when an app's subscription stops working

Status: Draft (2026-11-22).

Dependencies: [hi_app_subscriptions.md](../approved/hi_app_subscriptions.md)
(the sign-ins, the proxy's refusals and fallback, "told once"),
[hi_server_apps.md](../approved/hi_server_apps.md) (the server's Slack
app and its channel, the admin page), [hi_app_ops.md](../approved/hi_app_ops.md)
(an app's ops channel).

## Goal

The admins hear in the hi server's channel, and see on the admin page,
when an app's agent subscription stops working (signed out, or its limit
reached) or is close to its weekly limit. The three are Claude Code on a
Claude plan, Codex on a ChatGPT plan, and agy on a Google account.

Today each app's service tells its own ops channel once when a lead or
member falls back to OpenRouter. The dev box, where people work, is never
watched; agy isn't watched at all; the server's channel hears nothing; and
the admin page shows how each sign-in is set up, with one health signal
("ChatGPT stopped working").

## Decisions (2026-11-22)

- The channel is the hi server's own Slack channel (`slack.json`'s
  `Channel`, the one the admin page sets up for approvals and warnings).
- Warn when a sign-in is broken (signed out, or a limit reached), and when
  **less than 10% of a weekly limit is left**. Nothing for the 5-hour
  windows: they come back by themselves within hours.

## What each CLI lets hi see

Checked on 2026-11-22 against the sign-ins of every app on two machines
(Claude Code 2.1.330, Codex 0.164.0, agy 1.5.9), read only, nothing posted.

| | Claude Code | Codex (ChatGPT) | agy (Google) |
|---|---|---|---|
| Traffic through hi's proxy | yes (placeholder token) | yes (placeholder token) | no: a CONNECT tunnel to Google; hi sees host names only |
| On every call | `anthropic-ratelimit-unified-status` (allowed, rejected), `-7d-utilization` (0.02 = 2% used), `-7d-reset`, `-7d-status`, and the 5-hour ones | `x-codex-primary-*` and `x-codex-secondary-*`: `used-percent`, `window-minutes`, `reset-at` | nothing hi can read |
| Free check | `POST /v1/messages/count_tokens` answers 200 on a working token: alive or not, no usage | `GET chatgpt.com/backend-api/wham/usage` with the access token the app's service already refreshes: `plan_type`, `rate_limit.allowed`, `limit_reached`, each window's `used_percent`, `limit_window_seconds`, `reset_at` | `agy models` in the dev box (about a second; fails signed out, and refreshes agy's token), then `POST cloudcode-pa.googleapis.com/v1internal:retrieveUserQuotaSummary` with `{}`: groups of buckets (`weekly`, `5h`) with `remainingFraction` and `resetTime` |
| Usage without traffic | **not for apps**: a `claude setup-token` token is refused by `api/oauth/usage` (403, needs `user:profile`); only a full sign-in (the machine's) can read it | yes (above) | yes (above) |
| In the session files | the transcript's `isApiErrorMessage` entries with `error`: `authentication_failed`, `rate_limit`, `billing_error` | each turn's `token_count` event has `rate_limits` (`primary`, `secondary`, `plan_type`, `rate_limit_reached_type`) | `~/.gemini/antigravity-cli/log/`: "You are not logged into Antigravity", quota refusals |
| Expiry | a setup token lasts a year from its sign-in; a machine sign-in has `loginExpiresAt` (`claude auth status --json`) | refreshed by the app's service; a refused refresh sets `Failed` | the access token lasts an hour and is refreshed only while agy runs: in idle dev boxes it is days old, so hi checks through agy itself |

Found on the way:

- ChatGPT Pro and Pro Lite plans show one weekly window (`primary`, 168
  hours) and no 5-hour one: "weekly" is the window of 7 days or more,
  whichever of the two it is.
- Google refuses the quota summary with 403 for Python's default
  `User-Agent`, and for a request naming the project from
  `loadCodeAssist`; agy's own `User-Agent` and an empty body work.
- The usage answers carry the account's email; hi never posts it.

## The health record

The app's service keeps one record per sign-in it uses, in
`run/signin-health.json`, and rewrites it when something changes:

```jsonc
{
  "claude": {
    "source": "own",            // own | shared/<name> | machine
    "key": "c1f3…",             // a hash of the credential, to group apps that share one
    "state": "low",             // ok | low | limited | signed-out | expiring | unknown
    "weekly_left": 7,           // percent, when known
    "resets": "2026-11-28T02:00:00Z",
    "why": "93% of the weekly limit used",
    "seen": "2026-11-22T10:20:00Z",   // when it was last known
    "from": "traffic"           // traffic | check | session | expiry
  },
  "chatgpt": { … },
  "agy": { … }
}
```

- **ok**, or **unknown** when hi hasn't seen it yet (a Claude token with no
  traffic since the service started).
- **low**: less than 10% of a weekly window left.
- **limited**: a limit reached (any window), until `resets`.
- **signed-out**: refused as signed out, or a ChatGPT refresh refused.
- **expiring**: a Claude setup token 335 days after its sign-in, a machine
  sign-in within 14 days of its expiry.

### Where each comes from

- **Claude.** The proxies (the lead's, members', and the dev box's) write
  a line when the unified headers change, `claude-usage 7d=0.93
  status=allowed reset=…`, beside the refusals they already log; the
  service reads the dev box's log too (today it reads only the lead's and
  the members' events). Every hour, and when a refusal says signed out,
  the service checks the token with `count_tokens`. Between calls the
  weekly figure is the last one seen, with its time.
- **ChatGPT.** The service asks `wham/usage` every 15 minutes, beside the
  refresh it already does there, and the proxies' refusals as now.
- **agy.** When the dev box is up, every hour, the service runs `agy
  models` in it, then reads the quota summary with the box's token. A
  failed `agy models` is `signed-out`. With the dev box stopped, the last
  record stays, marked with its time.

## The admin page

- `/api/state` carries each app's worst state; the list of apps shows a
  dot on an app with a `low`, `limited`, `signed-out`, or `expiring`
  sign-in, with its words on hover.
- The app's Overview has a **Sign-ins** card: one line per sign-in with
  its source, state, weekly use and reset, and what to do ("Sign it in
  again: `/hi-orders signin claude`").
- **Keys** gets a column per sign-in with the state, and agy as a row.

## The server's channel

The admin page's process (`hi server __page`) reads the records of the
machine's apps when it builds `/api/state` and posts through the server's
Slack app, only on a change of state, one message per credential: apps
that share one (`shared/claude-max`) get one message naming them.

> **Claude stopped working** for orders and billing (shared/claude-max):
> signed out. Their agents are on OpenRouter now, billed per token. An
> owner signs it in again: `/hi-orders signin claude`.

> **ChatGPT is at 93% of its weekly limit** for orders (Pro plan); it resets
> Thu 28 Nov 02:00.

> **agy hit its limit** for orders: Claude and GPT models, until 15:20.

> **Claude works again** for orders and billing.

What was posted is kept per credential in the server's state, so a restart
doesn't post again; `low` is posted once per week window, not each time
the figure moves. The app's own ops channel keeps its messages as now.

## Tests

- Each source sets the state it should: a 401 and a 429 with
  `status=rejected` from the proxy, the headers' 7-day utilization (0.89
  ok, 0.91 low), a `wham/usage` answer with `limit_reached`, a quota
  bucket at 0.08, a failed `agy models`, a token 335 days old.
- Two apps on one shared token post one message naming both; the same
  state twice posts once; a restart posts nothing new; recovering posts
  "works again".
- The admin page shows the dot, the Overview card, and the Keys column.
- Nothing posted or shown carries an email or a token.

## Releases

1. The record and the admin page (Claude and ChatGPT from the proxies and
   `wham/usage`; the dev box's log read).
2. The server's channel.
3. agy: the hourly check in the dev box and its quota.

## Open questions

- Members' and the lead's boxes run the same CLIs through proxies, so
  release 1 covers them; does hi still need the app-channel messages once
  the server's channel has them?
- The quota summary and `wham/usage` are the CLIs' own endpoints, not
  documented APIs; hi should treat an unexpected answer as `unknown`, not
  as broken.
