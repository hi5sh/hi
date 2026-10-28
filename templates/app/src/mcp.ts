// The app's MCP server, at /mcp: its features as tools, for agents. It
// speaks Streamable HTTP without sessions and answers in JSON: initialize,
// ping, tools/list, and tools/call are all an agent needs to use the app.
// The tools are in src/tools.ts; this file is the protocol, and rarely
// changes.

export type Tool = {
  // name is what an agent calls, in snake_case: "list_notes".
  name: string;
  // description says what it does and when to use it, for the agent.
  description: string;
  // inputSchema is the JSON Schema of its arguments.
  inputSchema: { type: "object"; properties?: Record<string, unknown>; required?: string[] };
  // readOnly says it only reads: it changes nothing in the app or outside
  // it. An agent may be allowed only those, as a hi team's lead is in
  // production.
  readOnly: boolean;
  // run does it; what it returns goes back to the agent as JSON. A thrown
  // error goes back as the tool's error, for the agent to read.
  run: (args: Record<string, unknown>) => unknown | Promise<unknown>;
};

type Message = {
  jsonrpc?: string;
  id?: string | number | null;
  method?: string;
  params?: Record<string, unknown>;
};

const protocolVersion = "2025-06-18";

export function mcpHandler(server: { name: string; instructions?: string }, tools: Tool[]) {
  return async (request: Request): Promise<Response> => {
    if (request.method !== "POST") {
      return new Response("POST JSON-RPC messages here", {
        status: 405,
        headers: { Allow: "POST" },
      });
    }
    // A page on another site can't drive the app through a visitor's
    // browser (DNS rebinding): only agents, and the app's own pages.
    const origin = request.headers.get("Origin");
    if (origin && new URL(origin).host !== new URL(request.url).host) {
      return new Response("Forbidden", { status: 403 });
    }
    const message = (await request.json().catch(() => null)) as Message | null;
    if (!message || typeof message !== "object" || Array.isArray(message)) {
      return Response.json(failure(null, -32700, "send one JSON-RPC message"));
    }
    if (message.id === undefined || message.id === null) {
      // A notification, such as notifications/initialized.
      return new Response(null, { status: 202 });
    }
    return Response.json(await answer(message, server, tools));
  };
}

async function answer(
  message: Message,
  server: { name: string; instructions?: string },
  tools: Tool[],
) {
  const id = message.id ?? null;
  switch (message.method) {
    case "initialize":
      return result(id, {
        protocolVersion:
          typeof message.params?.protocolVersion === "string"
            ? message.params.protocolVersion
            : protocolVersion,
        capabilities: { tools: {} },
        serverInfo: { name: server.name, version: "1" },
        ...(server.instructions ? { instructions: server.instructions } : {}),
      });
    case "ping":
      return result(id, {});
    case "tools/list":
      return result(id, {
        tools: tools.map((tool) => ({
          name: tool.name,
          description: tool.description,
          inputSchema: tool.inputSchema,
          annotations: { readOnlyHint: tool.readOnly },
        })),
      });
    case "tools/call": {
      const name = message.params?.name;
      const tool = tools.find((each) => each.name === name);
      if (!tool) {
        return failure(id, -32602, `no tool ${String(name)}`);
      }
      const args = (message.params?.arguments ?? {}) as Record<string, unknown>;
      try {
        const value = await tool.run(args);
        return result(id, {
          content: [{ type: "text", text: JSON.stringify(value ?? null, null, 2) }],
          ...(value && typeof value === "object" && !Array.isArray(value)
            ? { structuredContent: value }
            : {}),
        });
      } catch (error) {
        return result(id, {
          content: [{ type: "text", text: error instanceof Error ? error.message : String(error) }],
          isError: true,
        });
      }
    }
    default:
      return failure(id, -32601, `no method ${String(message.method)}`);
  }
}

function result(id: string | number | null, value: unknown) {
  return { jsonrpc: "2.0", id, result: value };
}

function failure(id: string | number | null, code: number, text: string) {
  return { jsonrpc: "2.0", id, error: { code, message: text } };
}
