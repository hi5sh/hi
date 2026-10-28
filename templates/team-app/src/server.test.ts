import { afterAll, expect, test } from "bun:test";
import { createServer } from "./server";

const server = createServer({ port: 0, database: ":memory:" });
afterAll(() => server.stop(true));

test("serves the page", async () => {
  const page = await (await fetch(server.url)).text();
  expect(page).toContain('<div id="root">');
});

test("answers the health check", async () => {
  const response = await fetch(new URL("/healthz", server.url));
  expect(await response.text()).toBe("ok");
});

test("offers agents its tools", async () => {
  const response = await fetch(new URL("/mcp", server.url), {
    method: "POST",
    body: JSON.stringify({
      jsonrpc: "2.0",
      id: 1,
      method: "tools/call",
      params: { name: "app_status" },
    }),
  });
  const { result } = await response.json();
  expect(result.structuredContent.ok).toBe(true);
});
