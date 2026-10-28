import { expect, test } from "bun:test";
import { mcpHandler, type Tool } from "./mcp";

const tools: Tool[] = [
  {
    name: "echo",
    description: "Says it back.",
    inputSchema: { type: "object", properties: { text: { type: "string" } }, required: ["text"] },
    readOnly: true,
    run: ({ text }) => ({ text }),
  },
  {
    name: "fail",
    description: "Always fails.",
    inputSchema: { type: "object" },
    readOnly: false,
    run: () => {
      throw new Error("it failed");
    },
  },
];
const handle = mcpHandler({ name: "Test", instructions: "Be kind." }, tools);

async function call(body: unknown, headers: Record<string, string> = {}) {
  return handle(
    new Request("http://app.test/mcp", {
      method: "POST",
      headers: { "Content-Type": "application/json", ...headers },
      body: JSON.stringify(body),
    }),
  );
}

test("starts a session in the client's version", async () => {
  const response = await call({
    jsonrpc: "2.0",
    id: 1,
    method: "initialize",
    params: { protocolVersion: "2025-11-25" },
  });
  const { result } = await response.json();
  expect(result.protocolVersion).toBe("2025-11-25");
  expect(result.instructions).toBe("Be kind.");
});

test("lists the tools, each saying whether it only reads", async () => {
  const { result } = await (await call({ jsonrpc: "2.0", id: 2, method: "tools/list" })).json();
  expect(result.tools.map((tool: { name: string }) => tool.name)).toEqual(["echo", "fail"]);
  expect(result.tools[0].annotations).toEqual({ readOnlyHint: true });
  expect(result.tools[1].annotations).toEqual({ readOnlyHint: false });
});

test("runs a tool, and gives its error back", async () => {
  const echo = await (
    await call({
      jsonrpc: "2.0",
      id: 3,
      method: "tools/call",
      params: { name: "echo", arguments: { text: "hi" } },
    })
  ).json();
  expect(echo.result.structuredContent).toEqual({ text: "hi" });
  const fail = await (
    await call({ jsonrpc: "2.0", id: 4, method: "tools/call", params: { name: "fail" } })
  ).json();
  expect(fail.result.isError).toBe(true);
  expect(fail.result.content[0].text).toBe("it failed");
  const missing = await (
    await call({ jsonrpc: "2.0", id: 5, method: "tools/call", params: { name: "nope" } })
  ).json();
  expect(missing.error.code).toBe(-32602);
});

test("takes notifications without an answer, and no other site's pages", async () => {
  expect((await call({ jsonrpc: "2.0", method: "notifications/initialized" })).status).toBe(202);
  expect(
    (await call({ jsonrpc: "2.0", id: 6, method: "ping" }, { Origin: "https://elsewhere.example" }))
      .status,
  ).toBe(403);
  expect(
    (await call({ jsonrpc: "2.0", id: 7, method: "ping" }, { Origin: "http://app.test" })).status,
  ).toBe(200);
  expect((await handle(new Request("http://app.test/mcp"))).status).toBe(405);
});
