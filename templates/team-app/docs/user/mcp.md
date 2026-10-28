# Using the app with an agent

<!-- What an agent can do in the app, for the people who connect one and
for the agents themselves: hi lists this page in /llms.txt. The team's
coding agents keep it in step with src/tools.ts, in the same work as a
tool: each tool gets a section, in plain words first, then what it takes.
The team's lead reads it before it uses the app. -->

Agents, such as Claude or ChatGPT, and the team's own lead in Slack, can
use the app through its MCP server: the same things you do on its pages,
as tools.

## Connecting

The server is at `/mcp` on the app's address, over Streamable HTTP, such
as `https://<the app's address>/mcp`. Add it to your agent as a remote MCP
server. It answers wherever the app's pages do: on the team's network, or
behind the public address's protection.

What a tool changes, it changes for everyone, as the pages do. Tools
marked **reads only** change nothing.

## Tools

### app_status (reads only)

Whether the app and its database answer. It takes nothing.

Example: "Is the app up?"
