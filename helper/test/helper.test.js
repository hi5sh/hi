// Tests of the helper as a browser runs it: the script hi serves, on a
// page of a team's app, talking to a fake of the team's service at
// /__hi/api/. `hi team` serves the script from Go; the Go test
// TestHelperInABrowser runs these with the exact bytes it serves
// (HI_HELPER_SCRIPT), and without it they put the script together the
// same way from its two files.
import { afterEach, beforeEach, expect, mock, spyOn, test } from "bun:test";
import { readFileSync } from "node:fs";
import { join } from "node:path";

const script = process.env.HI_HELPER_SCRIPT
  ? readFileSync(process.env.HI_HELPER_SCRIPT, "utf8")
  : (() => {
      const finder = readFileSync(join(import.meta.dir, "../../third_party/finder/finder.js"), "utf8").replace(/^export /gm, "");
      const helper = readFileSync(join(import.meta.dir, "../helper.js"), "utf8");
      return `(() => {\n"use strict";\n${finder}\n${helper}\n})();\n`;
    })();

// sse is a stream of server-sent events, as the service answers a
// question or a message.
function sse(events) {
  const text = events.map(([name, payload]) => `event: ${name}\ndata: ${JSON.stringify(payload)}\n\n`).join("");
  const bytes = new TextEncoder().encode(text);
  return new Response(
    new ReadableStream({
      start(controller) {
        // In two pieces, so an event split across reads is read whole.
        const half = Math.floor(bytes.length / 2);
        controller.enqueue(bytes.slice(0, half));
        controller.enqueue(bytes.slice(half));
        controller.close();
      },
    }),
    { headers: { "Content-Type": "text/event-stream" } },
  );
}

const json = (value, status = 200) => Response.json(value, { status });

let requests;
let appClicks;

// start loads the helper on a fresh page with the given service: routes
// are "METHOD path" → (body) => Response.
async function start(routes, { button = true } = {}) {
  requests = [];
  appClicks = 0;
  document.body.innerHTML = '<main><h1>Orders</h1><p>Order 1042, 3 boxes</p><button id="pay">Pay</button></main>';
  document.getElementById("pay").addEventListener("click", () => appClicks++);
  spyOn(globalThis, "fetch").mockImplementation(async (url, init = {}) => {
    const path = new URL(String(url), location.href).pathname.replace("/__hi/api/", "");
    const method = init.method || "GET";
    const body = init.body ? JSON.parse(init.body) : null;
    requests.push({ method, path, body });
    const route = routes[`${method} ${path}`];
    return route ? route(body) : json({ error: "not found" }, 404);
  });
  (0, eval)(script);
  if (button) await until(() => $(".fab"));
}

const shadow = () => document.querySelector("hi-helper")?.shadowRoot;
const $ = (selector) => shadow()?.querySelector(selector);
const $$ = (selector) => Array.from(shadow()?.querySelectorAll(selector) || []);

async function until(check, what = String(check)) {
  for (let i = 0; i < 300; i++) {
    const value = check();
    if (value) return value;
    await new Promise((resolve) => setTimeout(resolve, 10));
  }
  throw new Error(`timed out waiting for ${what}`);
}

function open() {
  $(".fab").dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true }));
}

function type(text) {
  $("textarea").value = text;
  $("textarea").dispatchEvent(new Event("input", { bubbles: true }));
  $(".composer").dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
}

const sent = (path) => requests.filter((request) => request.method === "POST" && request.path === path);

beforeEach(() => {
  for (const host of document.querySelectorAll("hi-helper")) host.remove();
  delete window.__hiHelper;
  localStorage.clear();
  sessionStorage.clear();
});

afterEach(() => mock.restore());

const anyone = { "GET status": () => json({ team: "payments", version: "v3", commit: "abc", command: "/hi-payments" }) };

test("shows no button when the team's service doesn't answer", async () => {
  await start({}, { button: false });
  await until(() => requests.length);
  await new Promise((resolve) => setTimeout(resolve, 50));
  expect(document.querySelector("hi-helper")).toBeNull();
});

test("loads once on a page", async () => {
  await start(anyone);
  (0, eval)(script);
  expect(document.querySelectorAll("hi-helper")).toHaveLength(1);
});

test("answers a question from the app's guide", async () => {
  await start({
    ...anyone,
    "POST help": () =>
      sse([
        ["delta", { text: "Press **Pay** " }],
        ["delta", { text: "on the order." }],
        ["done", { text: "Press **Pay** on the order." }],
      ]),
  });
  open();
  expect($(".panel").classList.contains("open")).toBe(true);
  expect($(".name").textContent).toBe("Help · payments");
  expect($(".intro").textContent).toContain("/hi-payments");
  type("How do I pay?");
  const answer = await until(() => $$(".msg.lead").find((node) => node.textContent.includes("on the order.")));
  expect(answer.querySelector("strong").textContent).toBe("Pay");
  const [ask] = sent("help");
  expect(ask.body.question).toBe("How do I pay?");
  expect(ask.body.page.path).toBe("/orders");
  expect(ask.body.text).toContain("Order 1042");
  expect(ask.body.history).toEqual([]);
  // The conversation survives a reload, and can go to the team as feedback.
  expect(JSON.parse(sessionStorage.getItem("hi-helper-help")).map((turn) => turn.from)).toEqual(["person", "lead"]);
  expect($(".link-button").textContent).toBe("Tell the team about this as feedback");
});

test("shows what the lead writes as text, not as the page's HTML", async () => {
  window.hacked = undefined;
  await start({
    ...anyone,
    "POST help": () =>
      sse([["done", { text: '<img src=x onerror="window.hacked=1"> [click](javascript:alert(1)) and https://example.com/a' }]]),
  });
  open();
  type("Anything?");
  const answer = await until(() => $$(".msg.lead")[0]);
  expect(answer.querySelector("img")).toBeNull();
  expect(answer.textContent).toContain('<img src=x onerror="window.hacked=1">');
  const links = Array.from(answer.querySelectorAll("a"));
  expect(links.map((link) => link.getAttribute("href"))).toEqual(["https://example.com/a"]);
  expect(links[0].getAttribute("rel")).toBe("noopener noreferrer");
  expect(window.hacked).toBeUndefined();
});

test("puts a question back when the help fails", async () => {
  await start({ ...anyone, "POST help": () => json({ error: "The help is off for this app" }, 503) });
  open();
  type("How do I pay?");
  const error = await until(() => $(".msg.error"));
  expect(error.textContent).toBe("The help is off for this app");
  expect($("textarea").value).toBe("How do I pay?");
  expect($$(".msg.person")).toHaveLength(0);
});

test("sends feedback to the team", async () => {
  await start({ ...anyone, "POST feedback": () => json({ id: "f-7" }) });
  open();
  $(".tabs [data-tab=feedback]").click();
  expect($(".extra").hidden).toBe(false);
  $(".your-name").value = "Sam";
  $(".your-name").dispatchEvent(new Event("input"));
  $(".screen").checked = false;
  type("The total is wrong on order 1042");
  await until(() => $(".sent"));
  expect($(".sent strong").textContent).toBe("Sent to the team as f-7. Thank you!");
  const [feedback] = sent("feedback");
  expect(feedback.body).toMatchObject({ name: "Sam", text: "The total is wrong on order 1042", refs: [], files: [] });
  expect(localStorage.getItem("hi-helper-name")).toBe('"Sam"');
});

// The browser's own form checks would stop every send, also in chat and
// Ask where the name box is hidden; hi checks the name itself.
test("leaves the form to hi, not the browser's checks", async () => {
  await start(anyone);
  expect($(".composer").noValidate).toBe(true);
  expect($$(".composer [required]")).toHaveLength(0);
});

test("feedback waits for a name", async () => {
  await start({ ...anyone, "POST feedback": () => json({ id: "f-8" }) });
  localStorage.removeItem("hi-helper-name");
  open();
  $(".tabs [data-tab=feedback]").click();
  $(".your-name").value = "";
  type("The total is wrong");
  expect($(".your-name").classList.contains("needed")).toBe(true);
  expect($("textarea").value).toBe("The total is wrong");
  expect(sent("feedback")).toHaveLength(0);
  $(".your-name").value = "Sam";
  $(".your-name").dispatchEvent(new Event("input"));
  expect($(".your-name").classList.contains("needed")).toBe(false);
});

test("shows the team's answers to feedback", async () => {
  await start({
    "GET status": () => json({ team: "payments", version: "v3", commit: "abc", answers: 1 }),
    "GET feedback": () => json({ feedback: [{ id: "f-4", text: "The total is wrong", at: "2026-10-15T09:00:00Z",
      answers: [{ name: "Anna", text: "Fixed, thanks!\nTry again.", at: "2026-10-16T10:00:00Z" }] }] }),
  });
  // The button says the team answered, and the panel opens on it.
  await until(() => $(".fab.unread"));
  expect($(".toast .text").textContent).toBe("answered your feedback.");
  open();
  await until(() => $(".msg.answer"));
  expect($(".tabs [data-tab=feedback]").classList.contains("on")).toBe(true);
  expect($(".msg.person .when").textContent).toStartWith("f-4 · ");
  expect($(".msg.person .text").textContent).toBe("The total is wrong");
  expect($(".msg.answer .who").textContent).toStartWith("Anna · ");
  expect($(".msg.answer .text").textContent).toBe("Fixed, thanks!\nTry again.");
  expect(localStorage.getItem("hi-helper-answers-seen")).toBe("1");
});

const signedIn = {
  "GET status": () => json({ team: "payments", version: "v3", commit: "abc", lead: "Payments lead", you: { id: "U07ANA", name: "Ana" } }),
  "GET conversation": () => json({}),
};

test("is a chat with the lead for someone signed in", async () => {
  let n = 0;
  await start({
    ...signedIn,
    "POST messages": (body) =>
      sse([
        ["message", { conversation: "c-1", message: { n: ++n, from: "person", name: "Ana", text: body.text } }],
        ["delta", { text: "On it" }],
        ["done", { message: { n: ++n, from: "lead", name: "Payments lead", text: "Done: **v4** is up." } }],
      ]),
  });
  open();
  expect($(".name").textContent).toBe("Payments lead");
  expect($(".version").textContent).toBe("v3 · you're Ana (via Slack)");
  expect($(".tabs").hidden).toBe(true);
  type("Add a column for the supplier");
  await until(() => $$(".msg.lead").length === 1 && !$(".msg.streaming"));
  expect($$(".msg").map((node) => node.className)).toEqual(["msg person", "msg lead"]);
  expect($(".msg.lead strong").textContent).toBe("v4");
  type("Thanks");
  await until(() => $$(".msg.lead").length === 2);
  expect(sent("messages").map((request) => [request.body.conversation, request.body.text])).toEqual([
    ["", "Add a column for the supplier"],
    ["c-1", "Thanks"],
  ]);
});

// Production is for using the app: signed in, the chat is the Change
// tab beside Ask and Feedback, and says where a change goes.
test("on production, the chat with the lead is a tab for someone signed in", async () => {
  await start({
    "GET status": () => json({ team: "payments", slot: "production", version: "v3", commit: "abc", lead: "Payments lead", you: { id: "U07ANA", name: "Ana" } }),
    "GET conversation": () => json({}),
    "GET feedback": () => json({ feedback: [] }),
    "POST feedback": () => json({ id: "f-9" }),
    "POST messages": (body) => sse([["done", { message: { n: 2, from: "lead", name: "Payments lead", text: "It goes into dev." } }]]),
  });
  open();
  expect($(".tabs").hidden).toBe(false);
  expect($(".tabs [data-tab=change]").hidden).toBe(false);
  expect($(".tabs [data-tab=ask]").classList.contains("on")).toBe(true);
  expect($(".name").textContent).toBe("Help · payments");
  expect($(".version").textContent).toBe("v3 · you're Ana (via Slack)");

  // Feedback: Slack says who, so there is no name box to fill.
  $(".tabs [data-tab=feedback]").click();
  expect($(".your-name").style.display).toBe("none");
  $(".screen").checked = false;
  type("The total is wrong");
  await until(() => $(".sent"));
  expect(sent("feedback")[0].body.name).toBe("");

  $(".tabs [data-tab=change]").click();
  expect($(".name").textContent).toBe("Payments lead");
  expect($(".where").textContent).toStartWith("You're on production (v3). A change goes into dev first");
  type("Make overdue red");
  await until(() => $$(".msg.lead").length === 1);
  expect(sent("messages")).toHaveLength(1);
  expect(sent("help")).toHaveLength(0);
});

// Not signed in, or on a preview, there is no Change tab.
test("has no Change tab for anyone else", async () => {
  sessionStorage.setItem("hi-helper-tab", '"change"');
  await start({ "GET status": () => json({ team: "payments", slot: "production", version: "v3", commit: "abc" }) });
  open();
  expect($(".tabs [data-tab=change]").hidden).toBe(true);
  expect($(".tabs [data-tab=ask]").classList.contains("on")).toBe(true);
});

// The docs are a button between Ask and Feedback, and in the menu for a
// preview's chat; an address without /docs has neither.
test("opens the app's docs from the panel", async () => {
  const opened = spyOn(window, "open").mockImplementation(() => null);
  await start({ "GET status": () => json({ team: "payments", slot: "production", version: "v3", commit: "abc", docs: "/docs/" }) });
  open();
  const labels = $$(".tabs button").filter((button) => !button.hidden).map((button) => button.textContent);
  expect(labels).toEqual(["Ask", "Docs", "Feedback"]);
  $(".tabs .docs").click();
  expect(opened).toHaveBeenCalledWith("/docs/", "_blank", "noopener");
  expect($(".tabs [data-tab=ask]").classList.contains("on")).toBe(true);
  expect($("[data-action=docs]").hidden).toBe(false);
});

test("opens the docs from the menu in a preview's chat", async () => {
  const opened = spyOn(window, "open").mockImplementation(() => null);
  await start({ ...signedIn, "GET status": () => json({ team: "payments", slot: "preview", version: "dev", commit: "abc", you: { id: "U07ANA", name: "Ana" }, docs: "/docs/" }) });
  open();
  expect($(".tabs").hidden).toBe(true);
  $("[data-action=docs]").click();
  expect(opened).toHaveBeenCalledWith("/docs/", "_blank", "noopener");
});

test("has no docs button where the address has no docs", async () => {
  await start(anyone);
  open();
  expect($(".tabs .docs").hidden).toBe(true);
  expect($("[data-action=docs]").hidden).toBe(true);
});

test("keeps a message to send again when the lead can't take it", async () => {
  await start({ ...signedIn, "POST messages": () => sse([["error", { error: "The lead is restarting; try again in a minute" }]]) });
  open();
  type("Add a column");
  const error = await until(() => $(".msg.error"));
  expect(error.textContent).toBe("The lead is restarting; try again in a minute");
  expect($("textarea").value).toBe("Add a column");
  expect($$(".msg.person")).toHaveLength(0);
});

test("points at part of the page without the app seeing the click", async () => {
  await start({ ...anyone, "POST help": () => sse([["done", { text: "That pays the order." }]]) });
  dispatchEvent(new KeyboardEvent("keydown", { key: "s", code: "KeyS", altKey: true, bubbles: true }));
  expect($(".pick").classList.contains("on")).toBe(true);
  const pay = document.getElementById("pay");
  pay.dispatchEvent(new MouseEvent("click", { bubbles: true, cancelable: true }));
  expect(appClicks).toBe(0);
  // Taking it ends select mode and opens the panel with a chip for it.
  expect($(".pick").classList.contains("on")).toBe(false);
  expect($(".panel").classList.contains("open")).toBe(true);
  expect($(".composer .chips").textContent).toContain('#1 button "Pay"');
  // Out of select mode, the app gets its clicks again.
  pay.click();
  expect(appClicks).toBe(1);
  type("What does this do?");
  await until(() => $$(".msg.lead").length);
  const [ask] = sent("help");
  expect(ask.body.refs).toHaveLength(1);
  expect(ask.body.refs[0]).toMatchObject({ selector: "#pay", tag: "button", text: "Pay", landmarks: { heading: "Orders" } });
  expect(ask.body.refs[0].path).toEqual(["main"]);
});

test("Escape leaves select mode", async () => {
  await start(anyone);
  dispatchEvent(new KeyboardEvent("keydown", { key: "s", code: "KeyS", altKey: true, bubbles: true }));
  expect($(".pick").classList.contains("on")).toBe(true);
  dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
  expect($(".pick").classList.contains("on")).toBe(false);
  document.getElementById("pay").click();
  expect(appClicks).toBe(1);
});

test("tells the page where its button is and whether it is open", async () => {
  const events = [];
  const record = (event) => events.push(event.detail);
  addEventListener("hi-helper", record);
  try {
    await start(anyone);
    const host = document.querySelector("hi-helper");
    expect(host.dataset).toMatchObject({ corner: "br", open: "false", hidden: "false", chat: "false" });
    open();
    expect(host.dataset.open).toBe("true");
    $(".close").click();
    expect(host.dataset.open).toBe("false");
    $(".more").click();
    $("[data-action=hide]").click();
    expect(host.dataset.hidden).toBe("true");
    expect(events).toEqual([
      { corner: "br", open: false, hidden: false, chat: false },
      { corner: "br", open: true, hidden: false, chat: false },
      { corner: "br", open: false, hidden: false, chat: false },
      { corner: "br", open: false, hidden: true, chat: false },
    ]);
  } finally {
    removeEventListener("hi-helper", record);
  }
});

// The welcome page's own message box: the helper opens on the chat and
// sends what was typed there, on production in the Change tab.
test("sends a message the page hands it", async () => {
  for (const slot of ["preview", "production"]) {
    await start({
      "GET status": () => json({ team: "payments", slot, version: "v3", commit: "abc", lead: "Payments lead", you: { id: "U07ANA", name: "Ana" } }),
      "GET conversation": () => json({}),
      "POST messages": (body) => sse([["done", { message: { n: 2, from: "lead", name: "Payments lead", text: "On it." } }]]),
    });
    expect(document.querySelector("hi-helper").dataset.chat).toBe("true");
    dispatchEvent(new CustomEvent("hi-helper-send", { detail: { text: "  A page for our suppliers " } }));
    expect($(".panel").classList.contains("open")).toBe(true);
    await until(() => $$(".msg.lead").length === 1);
    expect(sent("messages").map((request) => request.body.text)).toEqual(["A page for our suppliers"]);
    expect($(".name").textContent).toBe("Payments lead");
    expect($("textarea").value).toBe("");
    mock.restore();
    document.querySelector("hi-helper").remove();
    delete window.__hiHelper;
    sessionStorage.clear();
    localStorage.clear();
  }
});

test("leaves a handed message in the box for someone who can't chat", async () => {
  await start(anyone);
  dispatchEvent(new CustomEvent("hi-helper-send", { detail: { text: "A page for our suppliers" } }));
  expect($(".panel").classList.contains("open")).toBe(true);
  expect($("textarea").value).toBe("A page for our suppliers");
  expect(requests.filter((request) => request.method === "POST")).toHaveLength(0);
});
