# What each app spends on models: per call, per app, with budgets and keys of its own

Status: Approved (2026-11-15). The owner chose, after the shared
OpenRouter key (`hi app key <app> openrouter --from shared/<name>`, in
[hi_server_vault.md](hi_server_vault.md)) put several apps on one key:
hi counts what each app spends through its own proxies, whatever key
pays; each app gets a monthly budget, which hi enforces, and which
OpenRouter enforces too on a key hi makes for the app from one
management key. No gateway of anyone else's: hi's proxies are the
gateway.

Dependencies: [hi_server_vault.md](hi_server_vault.md) (shared keys, the
vault's machine paths), [hi_app_subscriptions.md](hi_app_subscriptions.md)
(the members' proxies, Claude and ChatGPT sign-ins, fallback to
OpenRouter), [hi_app_data_fork.md](hi_app_data_fork.md) (the preview's
daily limit), [hi_watch.md](hi_watch.md) (spend outside hi),
[hi_app_ops.md](hi_app_ops.md) (the ops channel).

## Goal

- **Every model call an app makes through hi is counted to the app:**
  who made it (the lead, a member by role, the dev box, the app's
  production or preview), the model, the tokens, and what it cost, or
  that a plan (Claude's, ChatGPT's) paid it. The same with the app's own
  key, a shared key, or this machine's.
- **An app has a budget**, monthly and optionally daily, in dollars.
  hi's proxies stop the calls that cost money once it's spent, and the
  app's ops channel hears at 80% and at 100%.
- **One secret, a key per app:** with an OpenRouter management key in
  the vault, hi makes each app a key of its own, with the app's budget as
  the key's limit. OpenRouter then stops the app at its budget even if
  something went around hi, counts each app apart, and a leaked key is
  one app's, revoked alone.
- **A key that spends outside hi is noticed**, for a shared key too.

## What exists already

- **The proxies are the gateway.** No box holds a key. An app's code
  calls models on its slot's models socket (`app_keys.go`), which puts the
  key in and passes the call to OpenRouter. The lead's box, each member's,
  and the dev box reach OpenRouter, Anthropic, and ChatGPT through a proxy
  container that puts in the key or sign-in (`box_proxy.go`,
  `box_fallback.go`).
- **They read what a call cost:** OpenRouter puts it in every answer's
  `usage` (on by default now, streamed last). The models socket writes
  each cost to `run/spend.log` (with the slot), the lead's proxy writes
  `spend` lines in its network log, and members' and the dev box's
  fallbacks write them in `run/events/<box>.log`.
- **What they don't keep:** the model, the tokens, who among the members
  it was, and calls a plan paid (Claude's or ChatGPT's), which cost no
  dollars but do cost tokens and the plan's limits.
- **Limits:** the preview's daily limit ($5 unless set), on its socket
  alone. Production and the agents have none.
- **Spend outside hi:** `hi watch` compares what OpenRouter counts on an
  app's own key (`GET /api/v1/key`) with what hi saw, and raises "the
  key spent $X outside hi". For a shared key or this machine's there is
  no such check (an app on one can't tell the others' spend from a leak).

## The record

- **A line per call, from the proxy that passed it:** `call` and a JSON
  object, in the files the proxies write already (the lead's network log,
  `run/events/<box>.log`, and `run/models/calls.log` for the models
  socket):

  ```json
  {"who": "lead", "via": "openrouter", "model": "anthropic/claude-sonnet-4.6",
   "in": 12043, "out": 812, "cached": 9000, "reasoning": 0,
   "usd": 0.0123, "key": "app", "kfp": "aaf58e45", "id": "gen-…"}
  ```

  - `who`: `lead`, a member's role, `dev`, `production`, or `preview`.
  - `via`: `openrouter`, `anthropic` (Claude Code on a sign-in or a key),
    or `chatgpt` (Codex on a sign-in).
  - `key`: `app` (the app's key file: its own, a shared key, or one hi
    made), `machine` (Hermes'), or `plan` (a Claude or ChatGPT sign-in);
    `kfp` the first 8 hex of the key's SHA-256, never the key. A plan's
    calls have `usd` 0.
  - The tokens from the answer's `usage`: OpenAI's chat and Responses
    names (`prompt_tokens`, `input_tokens`, their `_details`), and
    Anthropic's (`input_tokens`, `output_tokens`,
    `cache_read_input_tokens`; a stream's `message_start` and
    `message_delta`). The model from the request, or the answer.
  - No prompt, no answer, no key.
- **Kept and summed by the app's service:** every minute it reads the new
  lines (from where it left off, as it reads the network log for the
  watcher) into `run/models/spend-YYYY-MM.json`: per day, per `who`, per
  model, per `key`: calls, tokens, and dollars. The month's raw lines
  stay 35 days, then go; the monthly sums stay 13 months.
- **The watcher** adds up the call lines themselves (and an older
  proxy's spend lines), since it looks at the last hour, which the day's
  sums can't say.

## Budgets

```sh
hi app budget payments                       # this month: spent, budget, by who and model
hi app budget payments --monthly 200 [--daily 20]
hi app budget payments --monthly off
```

- In `app.json` (`model_budget`: `monthly`, `daily`, in dollars). The
  machine's admins set it, on the page or here; the app's owners see it.
- **What counts:** every call that costs dollars, through every proxy of
  the app: the agents, the dev box, production, and the preview. A plan's
  calls don't (they're counted in tokens).
- **At 80%** the app's ops channel hears it once a month (or day); **at
  100%** hi's proxies refuse the calls that cost dollars, in each API's
  own error shape with the reason ("App payments spent its $200 for
  November; an admin raises it on the admin page"), so the app's code
  and the agents show it. Calls a plan pays go on. The channel hears it,
  and again when a new month or a raised budget lets calls through.
- **How the proxies know:** the app's service writes
  `run/models/budget.json` (month, day, spent, the budget, and whether
  each is spent) each minute and after each sum; each proxy reads it per
  call, as it reads the key file. A minute's lag can overshoot by a
  minute's calls; a key hi made has OpenRouter's limit behind it.
- **The preview's daily limit** stays as it is, inside the budget.

## Keys hi makes

```sh
hi server openrouter                          # the management key, if any, and the keys hi made
hi server openrouter --management-key         # asked without echo; kept in the vault
hi app key payments openrouter --managed      # a key of the app's own, made by hi
hi app key payments openrouter --rotate       # a new one in its place; the old one is deleted
```

- **The management key** (made at openrouter.ai/settings/management-keys,
  read and write) is kept at `hi/machine/openrouter` in the vault. It
  can't make model calls (OpenRouter refuses them), so a box never needs
  it.
- **`--managed`** makes a key (`POST /api/v1/keys`) named
  `hi <machine> <app>`, with the app's monthly budget as its `limit` and
  `limit_reset: monthly` (none without a budget), and keeps it as the
  app's own key, with its `hash` in `app.json` (`openrouter_managed`).
  A shared key or own key the app had goes, as with any key change.
- **The budget changes the limit** (`PATCH /api/v1/keys/{hash}`).
- **Removed, back, purged:** `hi app rm` disables the key (`disabled:
  true`), `hi app up` with the kept folder enables it, `--purge` deletes
  it. `hi app move` keeps it: a machine whose management key is the same
  OpenRouter organization's manages it there; another keeps it as the
  app's own key.
- **Rotate:** a new key, the proxy's file swapped, then the old one
  deleted.
- **New apps:** with a management key, the wizard and the page's New app
  offer "a key of its own, made by hi" first; an answers file takes
  `"openrouter": "managed"`.
- **What OpenRouter counts** on each (`usage_monthly`, `limit_remaining`)
  shows beside hi's own count.

## Spend outside hi

- **An app's own key, and one hi made:** as now, per key.
- **A shared key:** the server compares what OpenRouter counts on it
  (`GET /api/v1/key`, every 15 minutes) with what every app on this
  machine spent with it (`run/models/keys.json`, by `kfp` and hour). More
  than $1 and 10% apart in about an hour is told in the server's Slack
  channel, the audit log, and on the key in the Vault tab, once in six
  hours: "shared/openrouter spent $4.10 outside hi's apps in the
  last hour: something else has it. Change it (hi server vault shared set
  openrouter) and revoke the old one at OpenRouter." A shared key is for
  hi's apps alone; another tool using it shows as this.
- **This machine's key** isn't checked: Hermes on the machine, outside
  any app, uses it too.

## On the page

- **An app's Overview** has its month: spent and budget, a bar, and the
  top three models.
- **An app's Spend** (a section of its own): this month and last, by day,
  by who, by model, tokens and dollars, and the plans' calls in tokens;
  what OpenRouter counts on its key, where hi made it or it's the app's
  own; the budget, which an admin changes there.
- **The server's Overview** has every app's month in one table, and the
  Vault's Shared keys says what each one spent, by app.
- **Keys** (left panel) has the management key: set or not, Set, and
  the keys hi made.

## Releases

1. **The record:** every proxy's `call` lines (OpenRouter, Anthropic,
   ChatGPT; models, tokens, dollars, key), the monthly sums, `hi app
   budget <app>` without options (the month), the page's Spend and the
   Overviews' month, the watcher on the sums, and the shared key's check
   for spend outside hi.
2. **Budgets:** `--monthly`, `--daily`, the page's, `budget.json`, every
   proxy refusing at 100%, the 80% and 100% notices.
3. **Keys hi makes:** the management key, `--managed`, the limit from the
   budget, disable, enable, delete, rotate, a move, new apps' default,
   OpenRouter's counts beside hi's.

## Tests

- Each proxy's line for a call: a streamed and a plain answer from a fake
  OpenRouter, Anthropic (`message_start`, `message_delta`), and ChatGPT
  (Responses `response.completed`); the model, tokens, and dollars right;
  no prompt, answer, or key in the line.
- The sums: two days, three `who`s, two models, a plan's call; read twice
  from offsets, counted once; a month's file; raw lines older than 35 days
  gone.
- Budgets: 80% told once; at 100% each proxy refuses a paid call in its
  API's error shape and passes a plan's; a new month or a raised budget
  lets calls through, and the channel hears it.
- Keys hi makes, against a fake OpenRouter: the key made with the
  budget's limit, the limit changed with the budget, disabled on `rm`,
  enabled on `up`, deleted on `--purge`, rotated; the management key never
  in a box or a log.
- The shared key's check: a fake key usage above the sums by more than $1
  and 10% is an incident; equal isn't.

## Not in this

- A gateway of anyone else's (LiteLLM, Portkey): hi's proxies already
  pass every call, with the key put in; another would sit behind them
  and see less (not the plans' calls).
- Splitting one key's spend by OpenRouter's `user` field: its reports
  don't show it, as documented.
- Guarding what agents do with tool calls: [hi_agent_guard.md](../ideas/hi_agent_guard.md),
  an idea until the tool it starts from matures.
