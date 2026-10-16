import { Database } from "bun:sqlite";
import { mkdirSync } from "node:fs";
import { dirname, join } from "node:path";

// Each change to the tables is a new line at the end, such as
// "CREATE TABLE suppliers (id INTEGER PRIMARY KEY, name TEXT NOT NULL)";
// a database runs the ones it hasn't yet, and PRAGMA user_version counts
// them.
const migrations: string[] = [];

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
