import { expect, test } from "bun:test";
import { databasePath, openDatabase } from "./db";

test("opens the database", () => {
  const db = openDatabase(":memory:");
  expect(db.query("SELECT 1 AS one").get()).toEqual({ one: 1 });
});

test("stores the database in HI_DATA_DIR", () => {
  expect(databasePath({ HI_DATA_DIR: "/data" })).toBe("/data/app.db");
  expect(databasePath({})).toBe("data/app.db");
});
