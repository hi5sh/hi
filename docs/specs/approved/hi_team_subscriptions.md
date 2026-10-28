# `hi team` subscriptions specification

Status: Approved (2026-10-23). Built in releases (see [Releases](#releases)).

Dependencies: [hi_team.md](../approved/hi_team.md) (the lead, the members'
boxes), [hi_team_keys.md](../approved/hi_team_keys.md) (the team's own
OpenRouter key, the "Enter: this machine's" pattern),
[hi_box.md](../approved/hi_box.md) (the proxy's placeholder injection).

## Goal

A team's roles run on the subscriptions the company already pays for, and
OpenRouter is only what catches them when a subscription fails.

The lead is Hermes, and Hermes runs only on OpenRouter, billed per token.
That is now most of a team's model spend, while the ChatGPT plan that pays
for the reviewer (Codex) sits under its limits. Each role should say who
pays for it, the setup should sign it in, and a role whose subscription
fails should keep working on OpenRouter, with the team told once.

## What there is today

- **Lead (Hermes):** OpenRouter only. `hermesReady` refuses any other
  provider. The team's proxy swaps a placeholder for the team's key, else
  the machine's (`box_proxy.go`, `newBoxOpenRouterInjector`).
- **Coder (Claude Code):** this machine's Claude sign-in (a `claude
  setup-token` file, or `~/.claude/.credentials.json`), injected by the
  member box's proxy. No box holds it.
- **Reviewer (Codex):** this machine's `~/.codex/auth.json`, *copied into
  the box*. `syncCodexAuth` copies a refresh back when the box ends. The
  token is in the box, and two boxes that refresh the same sign-in spend
  the same single-use refresh token; OpenAI answers that by revoking the
  sign-in, on this machine too.
- The memory (Hindsight), the helper chat (`teamHelpModel`), and the app
  the team builds use OpenRouter's chat-completions API, on the lead's
  model or their own.

## The spike (2026-10-23)

Run on gpu1 with Codex's current access token, read and never refreshed:

- A proxy that swaps a placeholder for the access token and adds
  `ChatGPT-Account-ID` (from the token) is accepted by
  `https://chatgpt.com/backend-api/codex/responses`: the model answered on
  the subscription.
- Hermes, as the lead is set up (a placeholder key, the proxy's address),
  made tool calls through that proxy and finished the task. It needs a
  **named** custom provider: a plain `provider: custom` drops
  `codex_responses` for any host but OpenAI's
  (`hermes_cli/runtime_provider.py`, `_resolve_plain_custom_api_mode`) and
  falls back to chat completions, which the endpoint answers 404:

  ```yaml
  model:
    default: gpt-6.1-sol
    provider: custom:hi-chatgpt
  providers:
    hi-chatgpt:
      api: http://<proxy>:<port>/backend-api/codex
      api_key: hi-box-placeholder
      transport: codex_responses
      discover_models: false
  ```

  Hermes still probes a few paths (`/v1/models`, `/props`, `/version`);
  they 404 and do no harm. The proxy can answer them itself.
- Which models a ChatGPT account may use changes: `gpt-5.5`, still in
  Hermes' list, is refused ("not supported when using Codex with a ChatGPT
  account"); Codex's cache on this machine lists `gpt-6.1-sol`,
  `gpt-6-sol`, `gpt-6-astra`, `gpt-6-luna`, `gpt-5.6-*`. hi asks the
  account (`/backend-api/codex/models`), not Hermes' list.

Not yet tried: Codex through the proxy (a custom model provider, see
below), and Claude Code against OpenRouter's Anthropic endpoint. OpenRouter
documents the latter (`ANTHROPIC_BASE_URL=https://openrouter.ai/api`);
Codex documents custom providers with `wire_api = "responses"`.

## Providers

A member gets a provider beside its agent and model:

| Provider     | Pays with                         | Agents        |
|--------------|-----------------------------------|---------------|
| `openrouter` | the team's key, else the machine's | hermes        |
| `chatgpt`    | a ChatGPT plan (Plus, Pro, Team…) | hermes, codex |
| `claude`     | a Claude subscription             | claude        |

Claude's subscription is for Claude Code only, so Hermes never gets
`claude`. Copilot and others can join later as rows of the same table.

`team.json` gains `provider` on a member; empty means what the agent used
before (OpenRouter for Hermes, the machine's sign-in for Claude and Codex),
so existing teams don't change.

## Sign-ins

Each subscription a team uses has one sign-in, kept in the team's
`secrets.json` (0600), or the machine's when the person presses Enter:

- **ChatGPT:** a device-code sign-in run by the wizard (the flow `codex
  login --device-auth` and Hermes use: open a link, type a code). It is
  the team's own grant, apart from Codex's on this machine, so neither
  ever spends the other's refresh token. One grant serves every `chatgpt`
  role of the team: the lead and the reviewer share it.
- **Claude:** a token from `claude setup-token`, pasted. It lasts a year
  and has no refresh to manage.

**Only the team's server refreshes.** It refreshes the ChatGPT grant on
the host, ahead of expiry (the access token lasts ten days), and writes it
atomically. The proxies read it again for each call, as they do Claude's
credentials now. No box holds a token, and no box ever refreshes one.

Choosing "this machine's" ChatGPT sign-in means the proxy reads
`~/.codex/auth.json` and never refreshes it; Codex on the machine keeps it
fresh. The wizard says so, and recommends the team's own.

## The proxy

The team's proxy, and each member box's, routes by path on the inject port:

- `/backend-api/codex/…` → `chatgpt.com`, with the access token for the
  placeholder, `ChatGPT-Account-ID` and the residency header from the
  token, and `originator`/`User-Agent` naming hi.
- `/v1/messages…` → `api.anthropic.com`, as now.
- `/api/v1/…` → OpenRouter, as now.

**The reviewer stops holding `auth.json`.** Its box gets a Codex config
with a custom provider at the proxy and a placeholder key:

```toml
model_provider = "hi"
[model_providers.hi]
base_url = "http://<proxy>:<port>/backend-api/codex"
wire_api = "responses"
env_key = "HI_BOX_KEY"   # the placeholder
```

`syncCodexAuth` goes away for team boxes. (`hi agent codex` outside a team
can follow later.)

## Failover to OpenRouter

Every role fails over, with the same model, on OpenRouter:

- **When:** the subscription answers that the sign-in is bad (401, a
  revoked or expired grant hi can't refresh) or that the plan's limit is
  reached (429 with the plan's limit error, not a short rate limit, which
  is retried). Other errors are passed on as they are.
- **How:** the proxy sends the same request to OpenRouter with the model's
  OpenRouter name, in the same API: Responses for Hermes and Codex,
  Anthropic Messages for Claude Code (OpenRouter's Anthropic endpoint).
  The role's next calls go to OpenRouter directly until the subscription
  answers again; the proxy tries it again every 15 minutes.
- **The model's name:** the wizard finds each role's OpenRouter name when
  the role is set (`gpt-6.1-sol` → `openai/gpt-6.1-sol`, `opus` →
  `anthropic/claude-opus-…`) and checks it against OpenRouter's list. A
  model only the subscription has (`gpt-5.3-codex-spark`) gets a fallback
  the person picks. `team.json` keeps it as the member's `fallback`.
- **Spend:** OpenRouter calls count against the team's model limit, as
  now.
- **No key, no failover:** a team with no OpenRouter key (its own or the
  machine's) passes the subscription's error on, and the warning says so.

**Told once.** The first failover of a role posts in the lead's channel:

> The reviewer's ChatGPT sign-in failed (expired); it uses OpenRouter
> (openai/gpt-6.1-sol) now, billed per token. An owner can sign it in
> again with `/hi-orbit signin chatgpt`.

hi remembers it per role in the team folder and never says it again for
that role, failing back and over again included. Signing the
subscription in again clears it, so a later failure is told once more.
`hi team status` always shows which roles are on their fallback.

## Signing in again

A sign-in hi can't refresh (revoked, expired, the password changed) is
found by the server's refresh or by a call answered "signed out"; the
roles fail over, and the channel is told as above. It is signed in again
from Slack, by an owner, with the team's own command (`/hi-<team>`; plain
`/hi` is the hi server's app):

- **`/hi-<team> signin chatgpt`:** hi starts a device-code sign-in and
  DMs that owner, and only them, the link and the code: "Open
  https://auth.openai.com/codex/device on any device and type ABCD-1234,
  signed in to the company's ChatGPT account. The code works for 15
  minutes." The team's server waits for it and stores the new grant. The
  channel is told: "ChatGPT is signed in again (Team plan); the lead and
  the reviewer are back on it."
- **`/hi-<team> signin claude`:** Claude has no device code. hi DMs the
  owner how to make a token (`claude setup-token` on any computer with
  Claude Code) and a private link to a page of hi's, good once and for 15
  minutes, where they paste it. The token never goes through Slack.

Only owners may: a sign-in decides whose subscription pays. Anyone else
gets "Ask an owner: …". The code is never posted in the channel.

On the machine, the same:

```sh
hi team signin <name> chatgpt   # prints the link and the code
hi team signin <name> claude    # asks for a setup token
```

A team on this machine's sign-in can't be signed in again by hi: the
message says to run `codex login` (or `claude`) on the machine, and to ask
the IT team when you can't.

## The wizard

Step 5 asks each role's agent, model, and provider, and signs in what's
new; each answer is checked as it is given, as now:

```
5/7 Members
Each role's agent, model, and who pays for it. Enter keeps what's offered.
  lead     agent [hermes]  provider [chatgpt]  model [gpt-6.1-sol]
           ChatGPT: sign in for this team (Enter: this machine's, from Codex)
           Open https://auth.openai.com/codex/device and type ABCD-1234 … signed in, ChatGPT Team plan.
           Fallback: openai/gpt-6.1-sol on OpenRouter ✓
  coder    agent [claude]  provider [claude]   model [opus]
           Claude: paste a token from `claude setup-token` (Enter: this machine's)
           Fallback: anthropic/claude-opus-… on OpenRouter ✓
  reviewer agent [codex]   provider [chatgpt]  model [gpt-6-sol]
           ChatGPT: the team's sign-in, from the lead.
           Fallback: openai/gpt-6-sol on OpenRouter ✓
```

Models are offered from the account (ChatGPT's list, Claude's aliases,
OpenRouter's list). Step 6 (Models) is still asked: OpenRouter is every
role's fallback, and the memory, the helper chat, and the app use it.

`hi team new --from <file>` takes `provider` and `fallback` per member,
and the sign-ins from `HI_TEAM_CHATGPT_AUTH` (a path to an `auth.json`)
and `HI_TEAM_CLAUDE_TOKEN`.

## Commands

```sh
hi team signin <name> chatgpt   # sign the team in to ChatGPT again (device code)
hi team signin <name> claude    # paste a new setup token
hi team member <name> <role> --provider chatgpt [--model M] [--fallback M]
```

In Slack, owners only: `/hi-<team> signin chatgpt` and `/hi-<team> signin
claude` (see [Signing in again](#signing-in-again)).

## What stays on OpenRouter

- The memory (Hindsight) and the helper chat call chat completions, which
  the ChatGPT endpoint doesn't serve. They keep the lead's model on
  OpenRouter when the lead is on `openrouter`, and otherwise its fallback
  name.
- The app the team builds (`OPENROUTER_BASE_URL`), as now.

## Billing and the watcher

`agentBilling` reports by provider: "ChatGPT Team plan" for a Hermes lead
on `chatgpt`, not "OpenRouter, billed per token". The watcher adds up
OpenRouter spend as now, which is now only the fallbacks, the memory, the
helper, and the app, and shows a role on its fallback.

## Tests

- A member with no `provider` behaves as before (Hermes on OpenRouter,
  Claude and Codex on the machine's sign-ins).
- The proxy routes the three paths, swaps the placeholder for the right
  credential, and adds the ChatGPT headers from the token.
- The lead's Hermes config uses a named provider with `codex_responses`.
- A reviewer box has no `auth.json` and reaches ChatGPT through the proxy.
- The server refreshes a ChatGPT grant before it expires; no box or proxy
  writes it.
- A 401 or a plan-limit 429 fails over with the fallback model; a short
  rate limit doesn't. The warning is posted once per role, not again after
  failing back and over, and again after `hi team signin`.
- With no OpenRouter key, the error passes through and the warning says
  so.
- `/hi-<team> signin chatgpt` from an owner DMs only them the link and
  code, stores the grant when it's used, and tells the channel; from
  anyone else it refuses. `/hi-<team> signin claude` gives a one-time page
  that takes the token, and the token never reaches Slack.
- The wizard offers the account's models and checks each fallback on
  OpenRouter.

## Not in this

- Hermes on a Claude subscription (Claude Code only).
- Other subscriptions (Copilot, Nous, …): the provider table leaves room.
- `hi agent` outside a team: it can take the same providers later.
- The helper chat on the subscription (it would need the Responses API).

## Releases

1. **The lead on ChatGPT.** The `provider` field; the team's ChatGPT
   sign-in (the wizard, `hi team signin <name> chatgpt`), refreshed by the
   team's server; the proxy's ChatGPT route; the lead's Hermes on a named
   provider; the lead's failover to OpenRouter, told once; the billing
   label; `hi team member`.
2. **The reviewer through the proxy.** Codex on a custom provider at the
   proxy, the team's ChatGPT sign-in, no `auth.json` in the box; its
   failover.
3. **Claude.** The team's own setup token; the coder's failover through
   OpenRouter's Anthropic endpoint.
4. **Signing in again from Slack.** `/hi-<team> signin chatgpt|claude`,
   owners only, and the one-time page for Claude's token.

## Open questions

- Does the company's ChatGPT workspace allow its plan in a third-party
  harness? Hermes notes that OpenAI asks such tools to name themselves,
  which the proxy does.
- The lead and the reviewer share one plan's limits. If the lead's volume
  pushes the reviewer onto OpenRouter, a team may want a second ChatGPT
  sign-in for the lead.

## As built

Release 1, v1.8.0, as above, except:

- The wizard's ChatGPT question offers the team's own sign-in, and Enter
  takes it; `machine` uses Codex's. The spec offered this machine's on
  Enter while recommending the team's.
- The lead uses Hermes' own fallback (`fallback_providers`) rather than
  the proxy resending its calls: Hermes switches at once on a 401 or 403,
  after its retries on a limit, waits out a plan limit's reset time, and
  converts the conversation for OpenRouter's API itself. The proxy logs
  each refusal of a model call (`chatgpt-refused <status>:signin|limit|other`)
  and the server tells the channel from that log. Codex and Claude Code
  have no such fallback; releases 2 and 3 resend in the proxy.
- Hermes' notice on each switch is turned off with
  `display.suppress_warning_notifications`, which mutes Hermes' other
  warnings in Slack too.
- Until release 4, the channel's message names `hi team signin` for an
  admin on the machine.

Release 4, v1.11.0, as above, except:

- Claude's page is on the team's app, under `/__hi/signin/`, where the
  helper is served: production's address, else the preview's. With
  neither, the owner is told to have an admin run `hi team signin`.
- One ChatGPT device code waits at a time; a second asks to wait.
- The lead can run the plugin's command too (`hi team web signin`); the
  code or page still goes only to the owner named, in a DM that says to
  ignore it if they didn't ask.

Release 3, v1.9.0, before release 2, as above, except:

- Every Claude member falls back, whatever its `provider`; `claude` is
  accepted as its provider and means the same as empty.
- The fallback model is each call's own model by its OpenRouter name, so
  Claude Code's small calls stay small; a member's `fallback` replaces it
  for every call. The wizard shows the main model's fallback and asks for
  one when OpenRouter has none.
- A 429 is the plan's limit when Claude says the plan's window is
  rejected or asks for more than a minute's wait; a shorter one goes back
  to Claude Code, which retries.
- Members' proxies write fallbacks and their spend to the team's
  `run/events/<box>.log`, mounted into the proxy, which the server reads.
Release 2, v1.10.0, as above, except:

- Every Codex member goes through the proxy when the team has a ChatGPT
  sign-in or this machine's Codex is signed in to ChatGPT, whatever its
  `provider`; on an API key it keeps `auth.json` in the box.
- On fallback the proxy drops earlier reasoning items and the request
  for encrypted reasoning: ChatGPT encrypts them for its own account.
- The proxy names Hermes only on the lead's calls; Codex names itself.

- Each member's effort (`effort` in team.json, medium unless set) was
  added: hi passes it as Claude Code's `--effort`, Codex's
  `model_reasoning_effort`, and Hermes' `reasoning_effort`.
