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
