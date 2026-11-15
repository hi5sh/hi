# Guarding what agents do: their tool calls, logged, and checked by safeagents

Status: Idea (2026-11-15), held here until
[huggingface/safeagents](https://github.com/huggingface/safeagents) is
more mature: today it is version 0.0.1, about 2.5 weeks of history, no
releases, and it doesn't check Codex (OpenAI's Responses API). Revisit
when it checks the Responses API, has tagged releases, and its proxy can
be bound and authenticated; then this goes to approved, and its first
part could go sooner on its own.

Dependencies: [hi_app_subscriptions.md](../approved/hi_app_subscriptions.md)
(the box proxies, which see every model answer),
[hi_model_spend.md](../approved/hi_model_spend.md) (the `call` lines this
adds to), [hi_box.md](../approved/hi_box.md) (the network allowlist),
[hi_watch.md](../approved/hi_watch.md) (incidents).

## Goal

- **Every tool call an agent's model asks for is on record:** which box,
  which tool, when, and the call's arguments where they hold no secret, so
  an app's owners and its admin can see what its agents did, not only
  what they said.
- **A tool call that shouldn't run doesn't:** checked before the agent
  sees it, by safeagents' detectors and, for a suspicious one, a judge
  model; blocked calls replaced by a notice the agent reads.
- For Claude Code, Codex, and Hermes, the three agents hi runs, in the
  dev box, the lead's box, and members' boxes.

## What hi has already, and what this adds

- **Already:** boxes hold no keys (the proxy puts them in); a box reaches
  only the names its allowlist has, and its DNS resolves nothing else;
  production's keys never reach the dev box; releases need a person.
  These are enforced by the network and the container, below anything a
  model says.
- **safeagents** (a Python proxy between an agent and its model API)
  reads the tool calls in each answer and blocks: a tool the agent didn't
  declare, a host that appears nowhere in the conversation, what its
  detectors (atif-scan, rules) find at or above a severity (reading
  `/proc/1/environ` or a service account's token, `unshare`, `curl
  --noproxy`, fetching a task's published answer), and, with a judge
  model, a suspicious call the judge says to block. Its host check is
  weaker than hi's allowlist; its tool-call rules and the judge are what
  hi lacks.
- **What neither claims:** scanning tool results or fetched pages for
  instructions planted there (prompt injection). safeagents' judge sees
  the user's words, the call, and the findings, not the results.

## 1. The tool-call log (hi's own; could go first)

- The box proxy already passes every answer (`box_proxy.go`). It reads
  the tool calls in them: Anthropic's `tool_use` blocks, OpenAI's
  `tool_calls`, and the Responses API's `function_call` items, streamed or
  not, as `hi_model_spend.md` reads `usage`.
- A `tool` line per call, beside the `call` line: who, the tool's name,
  and its arguments cut to 2 KB with anything shaped like a key, a token,
  or a password masked (the masks hi's logs use). Kept 35 days.
- **The dev page** shows a box's recent tool calls under its session;
  **the admin page** an app's, by box, searchable. `hi app tools <app>
  [--box <role>] [--since 1h]` lists them.

## 2. safeagents, logging only

- **Where:** between the agent and hi's proxy, so it never sees a key:
  the box's model calls go to safeagents first (`ANTHROPIC_BASE_URL`, or
  Hermes' providers), which forwards to the proxy. It runs as a container
  of its own beside each box's proxy, on the box's network only (it has
  no authentication of its own), from an image hi builds with a pinned
  version, so the machine needs no Python.
- `--log-only` first: what it would have blocked lands in the tool-call
  log as `would-block`, with the detector and severity, and nothing
  changes for the agent. An owner turns it on per app (`hi app guard
  <app> log`), for some weeks, to see what it finds and how often it's
  wrong.
- Claude Code and Hermes only; Codex's calls aren't checked until
  safeagents reads the Responses API, and the page says so.

## 3. safeagents, blocking

- `hi app guard <app> on`: calls at or above `--block-severity high`
  are blocked; the agent reads the notice; the app's ops channel hears
  each block once a day per detector.
- **A judge:** an OpenRouter model through the app's own key, so its
  cost is the app's (`hi_model_spend.md` counts it); off unless the
  owner names one.
- **Fail closed or open:** safeagents allows a call when the judge fails;
  hi keeps that default and says so, since a judge that's down shouldn't
  stop every agent. A plugin of hi's own adds rules for hi's boxes: never
  read `/secrets`, `~/.claude`, `~/.codex`, or the proxy's address by
  other ports.
- Its decisions in the tool-call log, and blocks count toward `hi watch`'s
  incidents when they repeat.

## Open questions, to settle before approval

- Run one safeagents per box, or one per app with a route per box?
- Which detectors are on, and at which severity, by default?
- Is its 10 to 20 ms a call, and a judge's latency, acceptable for the
  dev page's chat?
- Does it keep up with Claude Code's and Hermes' releases, which change
  their APIs' use often?

## Not in this

- Writing hi's own judge or detectors: start from safeagents' and its
  plugins.
- Guarding what an app's own code does with models (its users' prompts):
  the app's business.
