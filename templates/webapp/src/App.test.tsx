import { afterEach, expect, mock, spyOn, test } from "bun:test";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { App } from "./App";

afterEach(() => {
  cleanup();
  mock.restore();
});

test("adds a note", async () => {
  const notes = [{ id: 1, text: "Call the supplier", created: "2026-10-16 09:00:00" }];
  const added = { id: 2, text: "Pay the invoice", created: "2026-10-16 09:05:00" };
  spyOn(globalThis, "fetch").mockImplementation((async (_url, init) =>
    init?.method === "POST"
      ? Response.json(added, { status: 201 })
      : Response.json(notes)) as typeof fetch);
  render(<App />);
  expect(await screen.findByText("Call the supplier")).toBeTruthy();
  fireEvent.change(screen.getByLabelText("Note"), { target: { value: "Pay the invoice" } });
  fireEvent.click(screen.getByRole("button", { name: "Add" }));
  expect(await screen.findByText("Pay the invoice")).toBeTruthy();
});
