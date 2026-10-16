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
