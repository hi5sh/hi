import { afterEach, expect, test } from "bun:test";
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { App, handOverMs } from "./App";

afterEach(() => {
  cleanup();
  for (const host of document.querySelectorAll("hi-helper")) {
    host.remove();
  }
});

// helper stands in for hi's helper: its element and its event.
function helper(state: {
  corner?: string;
  open?: boolean;
  hidden?: boolean;
  chat?: boolean;
  width?: number;
}) {
  let host = document.querySelector<HTMLElement>("hi-helper");
  if (!host) {
    host = document.createElement("hi-helper");
    document.body.appendChild(host);
  }
  host.dataset.corner = state.corner ?? "br";
  host.dataset.open = String(state.open ?? false);
  host.dataset.hidden = String(state.hidden ?? false);
  host.dataset.chat = String(state.chat ?? false);
  host.dataset.width = String(state.width ?? 400);
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
  // As wide as the person made the panel.
  helper({ corner: "bl", open: true, width: 560 });
  expect(screen.getByRole("main").className).toBe("welcome open beside-left");
  expect(screen.getByRole("main").style.getPropertyValue("--helper-width")).toBe("560px");
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

// Someone who can chat with the lead starts in the middle of the page: the
// message goes to the helper once the box has moved over to it.
test("hands a message from the middle of the page to the helper", async () => {
  const sent: string[] = [];
  const record = (event: Event) => sent.push((event as CustomEvent).detail.text);
  addEventListener("hi-helper-send", record);
  try {
    render(<App />);
    helper({ chat: true });
    expect(screen.getByRole("heading").textContent).toBe("What should your app do?");
    expect(screen.queryByRole("note")).toBeNull();
    const box = screen.getByLabelText("Message the lead") as HTMLTextAreaElement;
    const send = screen.getByLabelText("Send") as HTMLButtonElement;
    expect(send.disabled).toBe(true);

    fireEvent.change(box, { target: { value: "A page for our suppliers" } });
    fireEvent.keyDown(box, { key: "Enter", shiftKey: true });
    expect(screen.getByRole("main").className).toBe("welcome start");
    fireEvent.keyDown(box, { key: "Enter" });
    expect(screen.getByRole("main").className).toBe("welcome start leaving toward-right");
    expect(sent).toEqual([]);
    await act(() => new Promise((resolve) => setTimeout(resolve, handOverMs + 50)));
    expect(sent).toEqual(["A page for our suppliers"]);
    expect(box.value).toBe("");

    // The helper opens beside the page.
    helper({ chat: true, open: true });
    expect(screen.getByRole("heading").textContent).toBe("Here you talk with your team's lead");
  } finally {
    removeEventListener("hi-helper-send", record);
  }
});
