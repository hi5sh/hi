import { databasePath, openDatabase } from "./db";
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
      // API routes go here, such as "/api/suppliers": { GET, POST }, with
      // the database passed to each.
      "/healthz": () => {
        db.query("SELECT 1").get();
        return new Response("ok");
      },
      // The app's tools, for agents (src/tools.ts, docs/user/mcp.md). hi
      // serves /llms.txt from the user docs.
      "/mcp": mcpHandler({ name: "Hifin Template Name" }, appTools(db)),
    },
  });
}

if (import.meta.main) {
  const server = createServer();
  console.log(`Listening on ${server.url}`);
}
