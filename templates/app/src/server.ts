import { addNote, databasePath, listNotes, openDatabase } from "./db";
import index from "./index.html";

export function createServer(options: { port?: number; database?: string } = {}) {
  const db = openDatabase(options.database ?? databasePath());
  return Bun.serve({
    port: options.port ?? Number(process.env.PORT ?? 3000),
    hostname: "0.0.0.0",
    development: process.env.NODE_ENV !== "production" && { hmr: true, console: true },
    routes: {
      "/": index,
      "/healthz": new Response("ok"),
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
