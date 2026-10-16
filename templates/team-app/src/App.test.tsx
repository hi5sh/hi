import { afterEach, expect, test } from "bun:test";
import { act, cleanup, render, screen } from "@testing-library/react";
import { App } from "./App";

afterEach(() => {
  cleanup();
  for (const host of document.querySelectorAll("hi-helper")) {
    host.remove();
  }
});

// helper stands in for hi's helper: its element and its event.
function helper(state: { corner?: string; open?: boolean; hidden?: boolean }) {
  let host = document.querySelector<HTMLElement>("hi-helper");
  if (!host) {
    host = document.createElement("hi-helper");
    document.body.appendChild(host);
  }
  host.dataset.corner = state.corner ?? "br";
  host.dataset.open = String(state.open ?? false);
  host.dataset.hidden = String(state.hidden ?? false);
  act(() => {
    dispatchEvent(new CustomEvent("hi-helper"));
  });
}

test("welcomes without the helper", () => {
  render(<App />);
  expect(screen.getByRole("heading").textContent).toBe("Your app starts here");
  expect(screen.queryByRole("note")).toBeNull();
});

test("points at the chat button, then says what the chat is for", () => {
  render(<App />);
  helper({ corner: "br" });
  expect(screen.getByRole("note").className).toBe("pointer br");
  expect(screen.getByRole("note").textContent).toBe("Press the chat button to start");
  helper({ corner: "br", open: true });
  expect(screen.getByRole("heading").textContent).toBe("Here you talk with your team's lead");
  expect(screen.getByRole("main").className).toBe("welcome open beside-right");
  expect(screen.queryByRole("note")).toBeNull();
  helper({ corner: "tl" });
  expect(screen.getByRole("note").className).toBe("pointer tl");
});

test("follows a helper that was there before the page", () => {
  const host = document.createElement("hi-helper");
  Object.assign(host.dataset, { corner: "bl", open: "false", hidden: "false" });
  document.body.appendChild(host);
  render(<App />);
  expect(screen.getByRole("note").className).toBe("pointer bl");
});

test("says how to bring back a hidden button", () => {
  render(<App />);
  helper({ hidden: true });
  expect(screen.queryByRole("note")).toBeNull();
  expect(screen.getByText("Reload the page to bring back the chat button.")).toBeTruthy();
});
