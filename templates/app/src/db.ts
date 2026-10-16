import { Database } from "bun:sqlite";
import { mkdirSync } from "node:fs";
import { dirname, join } from "node:path";

export type Note = { id: number; text: string; created: string };

// Each change to the tables is a new line at the end; a database runs the
// ones it hasn't yet, and PRAGMA user_version counts them.
const migrations = [
  "CREATE TABLE notes (id INTEGER PRIMARY KEY, text TEXT NOT NULL, created TEXT NOT NULL DEFAULT (datetime('now')))",
];

// databasePath is in $HI_DATA_DIR, which a hi team keeps and backs up;
// the app's own folder is replaced at every release.
export function databasePath(env: Record<string, string | undefined> = process.env): string {
  return join(env.HI_DATA_DIR ?? "data", "app.db");
}

export function openDatabase(path: string): Database {
  if (path !== ":memory:") {
    mkdirSync(dirname(path), { recursive: true });
  }
  const db = new Database(path, { create: true, strict: true });
  db.run("PRAGMA foreign_keys = ON");
  const version =
    db.query<{ user_version: number }, []>("PRAGMA user_version").get()?.user_version ?? 0;
  for (const [index, sql] of migrations.entries()) {
    if (index >= version) {
      db.transaction(() => {
        db.run(sql);
        db.run(`PRAGMA user_version = ${index + 1}`);
      })();
    }
  }
  return db;
}

export function listNotes(db: Database): Note[] {
  return db.query<Note, []>("SELECT id, text, created FROM notes ORDER BY id DESC").all();
}

export function addNote(db: Database, text: string): Note {
  const note = db
    .query<Note, { text: string }>(
      "INSERT INTO notes (text) VALUES ($text) RETURNING id, text, created",
    )
    .get({ text });
  if (!note) {
    throw new Error("the note wasn't saved");
  }
  return note;
}
