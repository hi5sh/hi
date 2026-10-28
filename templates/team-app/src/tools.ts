import type { Database } from "bun:sqlite";
import type { Tool } from "./mcp";

// The app's tools, for agents at /mcp: one for each thing people do in
// the app that an agent would want to do too, added in the same work as
// the feature. Say what each does and when to use it, mark the ones that
// only read (the team's lead may use only those in production), and give
// each its part in docs/user/mcp.md.
export function appTools(db: Database): Tool[] {
  return [
    {
      name: "app_status",
      description: "Whether the app and its database answer.",
      inputSchema: { type: "object", properties: {} },
      readOnly: true,
      run: () => {
        db.query("SELECT 1").get();
        return { app: "Hifin Template Name", ok: true };
      },
    },
  ];
}
