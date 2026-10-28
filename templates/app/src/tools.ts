import type { Database } from "bun:sqlite";
import { addNote, listNotes } from "./db";
import type { Tool } from "./mcp";

// The app's tools, for agents at /mcp: one for each thing people do in
// the app that an agent would want to do too. Say what each does and when
// to use it, mark the ones that only read, and keep llms.txt (and in a hi
// team docs/user/mcp.md) in step.
export function appTools(db: Database): Tool[] {
  return [
    {
      name: "list_notes",
      description: "The notes, newest first.",
      inputSchema: { type: "object", properties: {} },
      readOnly: true,
      run: () => ({ notes: listNotes(db) }),
    },
    {
      name: "add_note",
      description: "Add a note, as the box at the top of the page does.",
      inputSchema: {
        type: "object",
        properties: { text: { type: "string", description: "The note" } },
        required: ["text"],
      },
      readOnly: false,
      run: ({ text }) => {
        const note = typeof text === "string" ? text.trim() : "";
        if (!note) {
          throw new Error("a note needs text");
        }
        return addNote(db, note);
      },
    },
  ];
}
