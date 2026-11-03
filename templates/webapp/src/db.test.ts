import { expect, test } from "bun:test";
import { addNote, databasePath, listNotes, openDatabase } from "./db";

test("keeps notes, newest first", () => {
  const db = openDatabase(":memory:");
  addNote(db, "first");
  addNote(db, "second");
  expect(listNotes(db).map((note) => note.text)).toEqual(["second", "first"]);
});

test("stores the database in HI_DATA_DIR", () => {
  expect(databasePath({ HI_DATA_DIR: "/data" })).toBe("/data/app.db");
  expect(databasePath({})).toBe("data/app.db");
});
