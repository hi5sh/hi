---
name: hi-dev-agents
description: Make the hi team's app usable by agents - its MCP server at /mcp, a tool per feature, and docs/user/mcp.md - so the team's lead (and people's own agents) can use it. Use when adding a feature people use, when asked to add MCP or tools or llms.txt to the app, or when the lead can't do something in the app.
---

# Apps agents can use

Agents use the app as well as people: the team's lead in Slack first,
and people's own (Claude, ChatGPT, Cursor). They use it through three
things on the app's addresses:

| Address | What | Who keeps it |
|---|---|---|
| `/mcp` | The app's MCP server: its features as tools | You, in the app |
| `docs/user/mcp.md` | "Using the app with an agent": each tool, in plain words | You, in the same work |
| `/llms.txt`, `/llms-full.txt` | What the app is, its docs, and its MCP server, for agents | hi, from `docs/user/` |

## The lead uses the app's tools

hi gives the team's lead two MCP servers: `app`, production, and
`app_preview`, the preview. On production the lead gets **only the tools
marked read-only**; hi enforces it outside the lead's box. On the preview,
which runs on its own copy of production's data, every tool. When the
tools change (a release, a branch on the preview), the lead sees the new
ones within a minute.

So the tools decide what the lead can answer: "how many orders came in
today?" needs a tool that reads the orders.

## A tool per thing people do

When you add or change a feature people use, add or change its tool in
the same work, where an agent would want it:

- **A clear name and description**: `list_orders`, "The orders, newest
  first; filter by supplier or day." Say when to use it.
- **Its arguments as JSON Schema**, with a description each.
- **Read-only only when it changes nothing**, in the app or outside it
  (no writes, no mail, no model calls that cost money). Mark it, and the
  lead can use it in production.
- **The same code as the page's API route**, never a copy: the tool
  calls what the route calls.
- **Its section in `docs/user/mcp.md`**: what it does in plain words,
  whether it reads only, what it takes, and an example of what one would
  ask for. hi lists the page in `/llms.txt`.
- **A test** that calls it through `/mcp`.

## In the app

A project from hi's team template has it: `src/mcp.ts` is the protocol
(leave it be), `src/tools.ts` the tools, and `/mcp` a route in
`src/server.ts`. Add a tool:

```ts
{
  name: "list_orders",
  description: "The orders, newest first. Use it to answer what came in and when.",
  inputSchema: { type: "object", properties: { day: { type: "string", description: "YYYY-MM-DD" } } },
  readOnly: true,
  run: ({ day }) => ({ orders: listOrders(db, typeof day === "string" ? day : undefined) }),
},
```

An app without `/mcp` (one made before, or not on Bun) gets one: the
protocol is small, four JSON-RPC methods over one POST route, in any
language. Streamable HTTP without sessions, answering in JSON:

- `initialize`: `{protocolVersion, capabilities: {tools: {}}, serverInfo: {name, version}, instructions?}`,
  the client's `protocolVersion` echoed back.
- `ping`: `{}`.
- `tools/list`: `{tools: [{name, description, inputSchema, annotations: {readOnlyHint}}]}`.
- `tools/call` with `{name, arguments}`: `{content: [{type: "text", text}], structuredContent?}`,
  or `isError: true` with the error as the text.
- A message without an `id` is a notification: answer `202` with no body.
- Refuse a request whose `Origin` header is another site's (`403`).

Copy `src/mcp.ts` from hi's app template when the app is TypeScript;
otherwise use the official MCP SDK for its language, in stateless JSON
mode. Then write `docs/user/mcp.md`, and add `/mcp` to `docs/map.md`.

## Trying it

```bash
make dev &        # the app on localhost:3000
curl -s localhost:3000/mcp -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}' | jq
curl -s localhost:3000/mcp -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"list_orders","arguments":{}}}' | jq
```

Once shipped, tell the owner which tools the lead has now, and which of
them it may use in production.
