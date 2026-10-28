import llms from "../llms.txt" with { type: "text" };
import { addNote, databasePath, listNotes, openDatabase } from "./db";
import index from "./index.html";
import { mcpHandler } from "./mcp";
import { appTools } from "./tools";

export function createServer(options: { port?: number; database?: string } = {}) {
  const db = openDatabase(options.database ?? databasePath());
  return Bun.serve({
    port: options.port ?? Number(process.env.PORT ?? 3000),
    hostname: "0.0.0.0",
    development: process.env.NODE_ENV !== "production" && { hmr: true, console: true },
    routes: {
      "/": index,
      "/healthz": new Response("ok"),
      // For agents: what the app is, and its tools (src/tools.ts).
      "/llms.txt": new Response(llms, { headers: { "Content-Type": "text/plain; charset=utf-8" } }),
      "/mcp": mcpHandler({ name: "Hifin Template Name" }, appTools(db)),
      "/api/notes": {
        GET: () => Response.json(listNotes(db)),
        POST: async (request) => {
          const body = (await request.json().catch(() => null)) as { text?: unknown } | null;
          const text = typeof body?.text === "string" ? body.text.trim() : "";
          if (!text) {
            return Response.json({ error: "a note needs text" }, { status: 400 });
          }
          return Response.json(addNote(db, text), { status: 201 });
        },
      },
    },
  });
}

if (import.meta.main) {
  const server = createServer();
  console.log(`Listening on ${server.url}`);
}
