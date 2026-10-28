import { afterAll, expect, test } from "bun:test";
import { createServer } from "./server";

const server = createServer({ port: 0, database: ":memory:" });
afterAll(() => server.stop(true));

test("adds and lists notes", async () => {
  const added = await fetch(new URL("/api/notes", server.url), {
    method: "POST",
    body: JSON.stringify({ text: "Call the supplier" }),
  });
  expect(added.status).toBe(201);
  const notes = await (await fetch(new URL("/api/notes", server.url))).json();
  expect(notes).toHaveLength(1);
  expect(notes[0].text).toBe("Call the supplier");
});

test("refuses an empty note", async () => {
  const response = await fetch(new URL("/api/notes", server.url), {
    method: "POST",
    body: JSON.stringify({ text: " " }),
  });
  expect(response.status).toBe(400);
});

test("serves the page", async () => {
  const page = await (await fetch(server.url)).text();
  expect(page).toContain('<div id="root">');
});

test("tells agents what it is, and lets them add a note", async () => {
  expect(await (await fetch(new URL("/llms.txt", server.url))).text()).toContain(
    "[MCP server](/mcp)",
  );
  const mcp = (body: unknown) =>
    fetch(new URL("/mcp", server.url), { method: "POST", body: JSON.stringify(body) }).then(
      (response) => response.json(),
    );
  const { result } = await mcp({ jsonrpc: "2.0", id: 1, method: "tools/list" });
  expect(result.tools.map((tool: { name: string }) => tool.name)).toEqual([
    "list_notes",
    "add_note",
  ]);
  await mcp({
    jsonrpc: "2.0",
    id: 2,
    method: "tools/call",
    params: { name: "add_note", arguments: { text: "From an agent" } },
  });
  const listed = await mcp({
    jsonrpc: "2.0",
    id: 3,
    method: "tools/call",
    params: { name: "list_notes" },
  });
  expect(listed.result.structuredContent.notes[0].text).toBe("From an agent");
});
