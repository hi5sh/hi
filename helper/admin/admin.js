"use strict";
// The admin page (hi admin, docs/specs/approved/hi_admin.md): the
// machine's apps in the middle, what you set on the left, what comes out
// on the right, as the dev page lays itself out. Every change is a POST
// the server checks; nothing here holds a secret.

const $ = (id) => document.getElementById(id);

// shown is an address as the page shows it: without its key, which the
// link keeps.
const shown = (url) => String(url || "").split("?")[0];

// el makes an element: el("a", {href: url, class: "x"}, "text", child).
function el(tag, attrs, ...children) {
  const node = document.createElement(tag);
  for (const [key, value] of Object.entries(attrs || {})) {
    if (value === undefined || value === null || value === false) continue;
    if (key === "class") node.className = value;
    else if (key.startsWith("on")) node.addEventListener(key.slice(2), value);
    else if (key === "text") node.textContent = value;
    else node.setAttribute(key, value === true ? "" : value);
  }
  for (const child of children.flat()) {
    if (child === undefined || child === null || child === false) continue;
    node.append(child instanceof Node ? child : document.createTextNode(String(child)));
  }
  return node;
}

function ago(when) {
  const t = new Date(when).getTime();
  if (!t) return "";
  const s = Math.max(0, Math.round((Date.now() - t) / 1000));
  if (s < 45) return "just now";
  if (s < 3600) return Math.round(s / 60) + " min ago";
  if (s < 86400) return Math.round(s / 3600) + " h ago";
  if (s < 86400 * 14) return Math.round(s / 86400) + " d ago";
  return new Date(when).toLocaleDateString(undefined, { day: "numeric", month: "short" });
}

let toastTimer = 0;
function toast(text, long) {
  const box = $("toast");
  box.textContent = text;
  box.hidden = false;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => { box.hidden = true; }, long ? 9000 : 4500);
}

// api calls the page's server: a GET, or a POST of JSON.
class SignIn extends Error {}
async function api(path, body) {
  const options = { credentials: "same-origin", cache: "no-store" };
  if (body !== undefined) {
    options.method = "POST";
    options.headers = { "Content-Type": "application/json" };
    options.body = JSON.stringify(body);
  }
  const response = await fetch(path, options);
  let data = {};
  try { data = await response.json(); } catch (e) { /* not JSON */ }
  if (response.status === 401 && data && data.signin) {
    showSignin();
    throw new SignIn(data.error || "sign in");
  }
  if (!response.ok) throw new Error((data && data.error) || response.statusText);
  return data;
}

// ---------------------------------------------------------------------------
// state

let state = null;      // /api/state
let openApp = "";      // the app in the middle; "" is Home
let middle = "";       // what the middle shows: "", an app's name, or ":policy"
let detail = null;     // its /api/app
let booted = false;
let restarting = false; // the server didn't answer: an update restarts it
let waitFor = "";       // the version an update left behind

// ---------------------------------------------------------------------------
// the panels: as the dev page's, each its views behind its icons, dragged
// wider, hidden, and remembered; on a narrow screen they lie over the
// middle, one at a time.

const panes = {
  left: { view: "apps", open: true, width: 300 },
  right: { view: "logs", open: true, width: 340 },
};
const paneViews = { left: ["apps", "org", "people", "keys", "machine", "activity"], right: ["requests", "running", "spend", "jobs", "logs", "incidents"] };
const paneIds = {
  left: { panel: "left-side", toggle: "left-toggle", close: "left-close", resize: "left-resize" },
  right: { panel: "right-side", toggle: "right-toggle", close: "right-close", resize: "right-resize" },
};
const narrow = () => matchMedia("(max-width: 1100px)").matches;
const paneOf = (view) => (paneViews.left.includes(view) ? "left" : "right");

(function loadPanes() {
  try {
    const saved = JSON.parse(localStorage.getItem("hi-admin-panes") || "null");
    if (saved && saved.v === 1) {
      for (const which of ["left", "right"]) {
        const p = saved[which] || {};
        if (paneViews[which].includes(p.view)) panes[which].view = p.view;
        if (typeof p.open === "boolean") panes[which].open = p.open;
        if (Number.isFinite(p.width)) panes[which].width = Math.max(240, Math.min(900, p.width));
      }
    }
  } catch (e) { /* the defaults */ }
  if (narrow()) panes.left.open = panes.right.open = false;
})();

function keepPanes() {
  try { localStorage.setItem("hi-admin-panes", JSON.stringify({ v: 1, left: panes.left, right: panes.right })); } catch (e) { /* private mode */ }
}

function renderPanes() {
  for (const which of ["left", "right"]) {
    const p = panes[which], ids = paneIds[which];
    const panel = $(ids.panel);
    panel.hidden = !p.open;
    panel.style.setProperty("--side-width", p.width + "px");
    $(ids.toggle).setAttribute("aria-pressed", String(p.open));
    for (const button of panel.querySelectorAll(".side-tabs > button[data-side]")) {
      button.setAttribute("aria-selected", String(button.dataset.side === p.view));
    }
    for (const view of paneViews[which]) $("side-" + view).hidden = view !== p.view;
  }
}

function viewShown(view) {
  const p = panes[paneOf(view)];
  return p.open && p.view === view;
}

function setPane(which, open) {
  panes[which].open = open;
  if (open && narrow()) panes[which === "left" ? "right" : "left"].open = false;
  keepPanes();
  renderPanes();
  if (open) freshView(panes[which].view);
}

function showView(view) {
  const which = paneOf(view);
  panes[which].view = view;
  setPane(which, true);
}

for (const which of ["left", "right"]) {
  const ids = paneIds[which];
  $(ids.toggle).addEventListener("click", () => setPane(which, !panes[which].open));
  $(ids.close).addEventListener("click", () => setPane(which, false));
  for (const button of $(ids.panel).querySelectorAll(".side-tabs > button[data-side]")) {
    button.addEventListener("click", () => showView(button.dataset.side));
  }
  const handle = $(ids.resize);
  handle.addEventListener("pointerdown", (event) => {
    event.preventDefault();
    handle.setPointerCapture(event.pointerId);
    handle.classList.add("dragging");
    const startX = event.clientX, startWidth = panes[which].width;
    const move = (e) => {
      const other = panes[which === "left" ? "right" : "left"];
      const room = window.innerWidth - (other.open ? other.width : 0) - 320;
      const delta = which === "left" ? e.clientX - startX : startX - e.clientX;
      panes[which].width = Math.round(Math.max(240, Math.min(room, startWidth + delta)));
      $(ids.panel).style.setProperty("--side-width", panes[which].width + "px");
    };
    const up = () => {
      handle.classList.remove("dragging");
      handle.removeEventListener("pointermove", move);
      handle.removeEventListener("pointerup", up);
      keepPanes();
    };
    handle.addEventListener("pointermove", move);
    handle.addEventListener("pointerup", up);
  });
}

// On a narrow screen a panel lies over the middle: a click in the middle
// puts it away.
$("middle").addEventListener("click", () => {
  if (!narrow()) return;
  if (panes.left.open || panes.right.open) {
    panes.left.open = panes.right.open = false;
    keepPanes();
    renderPanes();
  }
});

function badge(id, n) {
  const node = $(id);
  node.hidden = !n;
  node.textContent = n > 99 ? "99+" : String(n || "");
}

function freshView(view) {
  if (!booted) return;
  if (view === "activity") refreshActivity();
  if (view === "jobs") { refreshJobs(); markJobsSeen(); }
  if (view === "logs") { refreshLogs(true); clearLogsDot(); }
  if (view === "incidents") refreshIncidents();
  if (view === "machine") renderMachine(true);
  if (view === "requests" || view === "running") refreshOrg();
  if (view === "spend") refreshSpend();
  if (view === "org" || view === "people" || view === "keys") {
    if (orgSettings) { if (view === "org") renderOrg(); if (view === "people") renderPeople(); if (view === "keys") renderKeys(); }
    refreshOrgSettings();
  }
}

// The views a machine has: the org's only with an org server; Incidents
// not on an org server's machine without apps.
function hasView(view) {
  if (!state) return true;
  if (["org", "people", "keys", "requests", "running", "spend"].includes(view)) return !!state.org;
  if (view === "incidents") return !state.org || state.apps.length > 0;
  return true;
}
function renderTabs() {
  for (const button of document.querySelectorAll(".side-tabs > button[data-side]")) button.hidden = !hasView(button.dataset.side);
  for (const which of ["left", "right"]) {
    if (!hasView(panes[which].view)) { panes[which].view = paneViews[which].find(hasView); renderPanes(); }
  }
}

// ---------------------------------------------------------------------------
// signing in

function showSignin() {
  $("work").hidden = true;
  $("signin").hidden = false;
  const params = new URLSearchParams(location.search);
  if (params.has("ended")) {
    $("signin-ended").hidden = false;
    history.replaceState(null, "", "/");
  }
  document.title = "Admin page: sign in";
}

$("newlink-form").addEventListener("submit", async (event) => {
  event.preventDefault();
  const who = $("newlink-who").value.trim();
  if (!who) return;
  try {
    const answer = await api("/api/new-link", { who });
    $("newlink-said").textContent = answer.text;
  } catch (e) {
    $("newlink-said").textContent = e.message;
  }
});

$("signout").addEventListener("click", async () => {
  try { await api("/api/signout", {}); } catch (e) { /* signed out anyway */ }
  showSignin();
});

// ---------------------------------------------------------------------------
// the bar, Home, and the apps

const stateWords = { up: "up", down: "down", frozen: "frozen", paused: "paused", broken: "broken", failed: "failed", activating: "starting", deactivating: "stopping" };
function stateClass(s) {
  if (s === "up") return "idle";
  if (s === "frozen" || s === "paused" || s === "activating" || s === "deactivating") return "blocked";
  if (s === "failed" || s === "broken") return "bad";
  return "";
}
function stateDot(s) { return el("span", { class: "dot " + stateClass(s), "aria-hidden": "true" }); }

function renderBar() {
  $("machine").textContent = state.machine;
  $("home-machine").textContent = state.machine;
  const named = openApp || (middle === ":policy" ? "The org's policy" : middle === ":new" ? "New app" : middle.startsWith(":new/") ? "New app " + middle.slice(5) : "");
  $("crumb").hidden = !named;
  $("sections").hidden = !openApp;
  $("open-box").hidden = !openApp;
  $("crumb-app").textContent = named;
  document.title = (named ? named + " · " : "") + "Admin page · " + state.machine;
  $("bar-note").textContent = restarting ? "The admin page is restarting…" : state.running ? (state.running === 1 ? "A job runs" : state.running + " jobs run") : "";
}

function renderApps() {
  const query = $("apps-search").value.trim().toLowerCase();
  const list = $("apps-list");
  list.replaceChildren();
  const apps = state.apps.filter((a) => !query || a.name.includes(query) || (a.purpose || "").toLowerCase().includes(query));
  if (!state.apps.length) list.append(el("li", { class: "empty" }, "No apps on this machine yet."));
  else if (!apps.length) list.append(el("li", { class: "empty" }, "No app matches."));
  for (const app of apps) {
    const row = el("button", { class: "stask-row", onclick: () => openView(app.name), title: app.purpose || app.name },
      el("span", { class: "stask-title" }, app.name),
      el("span", { class: "stask-num" }, app.state === "up" ? (app.production ? "v" + app.production : "") : stateWords[app.state] || app.state));
    const li = el("li", { class: app.name === openApp ? "current" : "" }, stateDot(app.state), row);
    if (app.incidents) li.append(el("span", { class: "count", title: app.incidents + " open incident" + (app.incidents === 1 ? "" : "s") }, app.incidents));
    list.append(li);
  }
  badge("apps-count", state.apps.filter((a) => a.state !== "up").length);
  badge("incidents-count", state.apps.reduce((n, a) => n + (!openApp || a.name === openApp ? a.incidents || 0 : 0), 0));
}
$("apps-search").addEventListener("input", renderApps);

// ---------------------------------------------------------------------------
// what the machine and its apps use

function size(bytes) {
  const n = Number(bytes) || 0;
  const units = [["TB", 2 ** 40], ["GB", 2 ** 30], ["MB", 2 ** 20], ["KB", 2 ** 10]];
  for (const [unit, value] of units) if (n >= value) return (n / value).toFixed(n / value >= 100 ? 0 : 1) + " " + unit;
  return n + " B";
}
const percent = (share) => (share < 0.01 && share > 0 ? "<1" : Math.round(share * 100)) + "%";
function level(share) { return share >= 0.9 ? "bad" : share >= 0.75 ? "warn" : ""; }
function meter(label, share, text, title) {
  const bar = el("span", { class: "meter-bar " + level(share) });
  bar.style.setProperty("--share", (Math.min(Math.max(share, 0), 1) * 100).toFixed(1) + "%");
  return el("div", { class: "meter", title: title || "" }, el("div", { class: "meter-head" }, el("span", {}, label), el("span", { class: "muted small" }, text)), bar);
}
function uptime(seconds) {
  const s = Number(seconds) || 0;
  if (s >= 86400) return Math.floor(s / 86400) + " d";
  if (s >= 3600) return Math.floor(s / 3600) + " h";
  return Math.max(1, Math.floor(s / 60)) + " min";
}

function renderHomeMachine() {
  const box = $("home-machine-card");
  const u = state.usage;
  if (!u) { box.replaceChildren(); return; }
  const head = el("div", { class: "card-head pad" }, el("span", { class: "dot " + (level(Math.max(u.cpu || 0, u.mem_total ? u.mem_used / u.mem_total : 0, ...u.disks.map((d) => d.used / d.total))) === "bad" ? "bad" : "idle"), "aria-hidden": "true" }),
    el("strong", { class: "card-name" }, state.machine), el("span", { class: "muted small" }, "up " + uptime(u.uptime)), el("span", { class: "grow" }),
    el("span", { class: "muted small", title: "The load: how many things wanted a core, over 1, 5, and 15 minutes" }, u.cores + " cores · load " + u.load.map((l) => l.toFixed(2)).join(" ")));
  const meters = el("div", { class: "meters pad" });
  meters.append(u.cpu === undefined || u.cpu === null ? meter("CPU", 0, "…") : meter("CPU", u.cpu, percent(u.cpu) + " busy", "The share of all " + u.cores + " cores busy, over the last few seconds"));
  if (u.mem_total) meters.append(meter("Memory", u.mem_used / u.mem_total, size(u.mem_used) + " of " + size(u.mem_total), "What isn't available to start something new; the kernel's cache is counted as free"));
  if (u.swap_total) meters.append(meter("Swap", u.swap_used / u.swap_total, size(u.swap_used) + " of " + size(u.swap_total)));
  for (const d of u.disks) meters.append(meter("Disk " + d.mount, d.used / d.total, size(d.used) + " of " + size(d.total), size(d.total - d.used) + " free on " + d.mount));
  box.replaceChildren(el("div", { class: "card-box machine-card" }, head, meters));
}

// usageOf is an app's use as a card shows it.
function usageOf(name) {
  const u = state.usage;
  const app = u && u.apps ? u.apps[name] : null;
  if (!app) return null;
  const row = el("div", { class: "card-usage" });
  row.append(meter("CPU", app.cpu || 0, app.cpu === undefined || app.cpu === null ? "…" : percent(app.cpu), "Its share of the machine's " + u.cores + " cores: its lead and its boxes"));
  row.append(meter("Memory", u.mem_total ? app.memory / u.mem_total : 0, size(app.memory), "Its lead and its " + app.boxes + " running boxes, of the machine's " + size(u.mem_total)));
  row.append(el("div", { class: "meter", title: "Its folder, measured every ten minutes" }, el("div", { class: "meter-head" }, el("span", {}, "Disk"), el("span", { class: "muted small" }, app.disk >= 0 ? size(app.disk) : "…"))));
  return row;
}
function usageLine(name) {
  const u = state && state.usage;
  const app = u && u.apps ? u.apps[name] : null;
  if (!app) return "";
  const cpu = app.cpu === undefined || app.cpu === null ? "" : percent(app.cpu) + " of the machine's CPU, ";
  return cpu + size(app.memory) + " of memory (its lead and " + app.boxes + " running box" + (app.boxes === 1 ? "" : "es") + ")" + (app.disk >= 0 ? ", " + size(app.disk) + " on disk (its folder)" : "");
}

function renderHome() {
  renderHomeMachine();
  renderHomeOrg();
  $("home-what").textContent = state.org && !state.apps.length ? "The org" : "Apps";
  const cards = $("home-cards");
  cards.replaceChildren();
  const up = state.apps.filter((a) => a.state === "up").length;
  $("home-summary").textContent = state.apps.length ? `${up} of ${state.apps.length} up` : "";
  if (!state.apps.length) {
    cards.append(el("div", { class: "app-card empty-card" },
      el("p", {}, state.org ? "No apps on this machine: it runs the org server." : "No apps on this machine yet."),
      el("p", { class: "muted" }, "Creating one here comes in a later hi. Until then, on the machine: ", el("code", {}, "hi app new <name>"), ".")));
    return;
  }
  for (const app of state.apps) {
    const addresses = el("div", { class: "card-links" });
    for (const address of app.addresses) {
      addresses.append(el("a", { href: address.url, target: "_blank", rel: "noopener noreferrer", onclick: (e) => e.stopPropagation() }, address.what + " ↗"));
    }
    const card = el("button", { class: "app-card", onclick: () => openView(app.name) },
      el("div", { class: "card-head" }, stateDot(app.state), el("strong", { class: "card-name" }, app.name),
        el("span", { class: "muted small" }, stateWords[app.state] || app.state),
        el("span", { class: "grow" }),
        el("span", { class: "muted small" }, app.production ? "production v" + app.production : "nothing released")),
      app.purpose ? el("p", { class: "card-purpose" }, app.purpose) : null,
      app.error ? el("p", { class: "error small" }, app.error) : null,
      app.incidents ? el("p", { class: "warn small" }, app.incidents + " open incident" + (app.incidents === 1 ? "" : "s")) : null,
      usageOf(app.name));
    cards.append(el("div", { class: "card-box" }, card, app.addresses.length ? addresses : null));
  }
}

$("home-button").addEventListener("click", (event) => {
  if (event.detail) event.currentTarget.blur(); // a click, not a key: no focus ring left on the bar
  openView("");
});

function setMiddle(what) {
  middle = what;
  const app = what && !what.startsWith(":") ? what : "";
  if (app !== openApp) detail = null;
  openApp = app;
  $("home").hidden = !!what;
  $("overview").hidden = !app;
  $("policy").hidden = what !== ":policy";
  $("newapp").hidden = !what.startsWith(":new");
  if (!what.startsWith(":new")) clearTimeout(draftPoll);
  history.replaceState(null, "", what ? "#" + encodeURIComponent(what) : "/");
  renderBar();
  renderApps();
  if (!what) renderHome();
}

async function openView(name) {
  setMiddle(name);
  if (name) {
    $("overview").replaceChildren(el("p", { class: "muted" }, "…"));
    if (narrow()) { panes.left.open = false; keepPanes(); renderPanes(); }
  }
  if (name) await refreshDetail();
  if (viewShown("logs")) refreshLogs(true);
  if (viewShown("incidents")) refreshIncidents();
  if (viewShown("jobs")) { if (jobOpen) { jobOpen = ""; $("job-one").hidden = true; $("jobs-list").hidden = false; } refreshJobs(); }
  else renderJobsBadge();
}

async function refreshDetail() {
  if (!openApp) return;
  const name = openApp;
  try {
    const fresh = await api("/api/app?name=" + encodeURIComponent(name));
    if (openApp !== name) return;
    const same = detail && JSON.stringify(detail) === JSON.stringify(fresh);
    detail = fresh;
    if (!same) renderOverview();
  } catch (e) {
    if (e instanceof SignIn) return;
    if (openApp === name) $("overview").replaceChildren(el("p", { class: "error" }, e.message));
  }
}

// ---------------------------------------------------------------------------
// an app's Overview

function renderOverview() {
  const d = detail;
  const box = $("overview");
  // What's being typed to the lead stays.
  const typing = $("say-text") ? { text: $("say-text").value, focus: document.activeElement === $("say-text") } : null;
  const stopped = d.stopped;
  const up = d.state === "up" || d.state === "paused" || d.state === "frozen";
  const head = el("div", { class: "admin-head" },
    el("h1", { class: "admin-title" }, d.name),
    el("span", { class: "pill " + (d.state === "up" ? "open" : d.state === "down" ? "closed" : "working") }, stateDot(d.state), stateWords[d.state] || d.state),
    el("span", { class: "muted" }, d.production ? "production v" + d.production : "nothing released yet"));
  const parts = [head];
  if (d.purpose) parts.push(el("p", { class: "purpose" }, d.purpose));
  if (stopped) {
    parts.push(el("p", { class: "note warn" }, (stopped.frozen ? "Frozen" : "Paused") + (stopped.by ? " by " + stopped.by : "") + (stopped.at ? ", " + ago(stopped.at) : "") + (stopped.because ? ": " + stopped.because : "") + "."));
  }
  if (d.error) parts.push(el("p", { class: "error" }, d.error));

  // Where it is.
  const where = el("ul", { class: "list where-list" });
  for (const address of d.addresses) {
    where.append(el("li", {}, el("span", { class: "what" }, el("strong", {}, address.what), " ", el("span", { class: "muted small" }, shown(address.url))),
      el("a", { class: "button-like small", href: address.url, target: "_blank", rel: "noopener noreferrer" }, "Open ↗")));
  }
  where.append(el("li", {}, el("span", { class: "what" }, el("strong", {}, "Dev page"), " ", el("span", { class: "muted small" }, d.dev_page || "while the app runs")),
    el("button", { class: "small", onclick: () => openDevPage(d.name) }, "Open ↗")));
  parts.push(el("h2", {}, "Addresses"), where);

  // What it can do.
  const actions = el("div", { class: "data-actions" });
  const button = (label, action, extra) => el("button", Object.assign({ onclick: () => act(action) }, extra || {}), label);
  if (d.state === "down" || d.state === "failed" || d.state === "broken") actions.append(button("Start", "start", { class: "primary" }));
  else actions.append(button("Restart", "restart"), button("Stop", "stop"));
  actions.append(button("Rebuild", "rebuild"), button("Snapshot now", "snapshot"));
  if (stopped && stopped.frozen) actions.append(button("Unfreeze", "unfreeze", { class: "primary" }));
  else if (up) actions.append(button("Freeze…", "freeze"));
  if (stopped && stopped.paused) actions.append(button("Unpause", "unpause", { class: "primary" }));
  else if (up && !(stopped && stopped.frozen)) actions.append(button("Pause…", "pause"));
  parts.push(el("h2", {}, "Actions"), actions);
  parts.push(el("div", { class: "data-actions danger-row" }, el("button", { onclick: () => renameApp(d.name) }, "Rename…"), el("button", { class: "danger", onclick: () => removeApp(d.name) }, "Remove…")));

  // Tell the lead.
  const say = el("form", { class: "say-form", autocomplete: "off" },
    el("textarea", { id: "say-text", rows: "2", maxlength: "4000", placeholder: "Tell " + d.name + "'s lead something: it answers in the app's channel", "aria-label": "Tell the lead" }),
    el("button", { type: "submit" }, "Tell the lead"));
  say.addEventListener("submit", async (event) => {
    event.preventDefault();
    const text = $("say-text").value.trim();
    if (!text) return;
    if (await act("say", { text })) $("say-text").value = "";
  });
  if (up) parts.push(say);

  // Who works on it.
  const facts = el("dl", { class: "facts" });
  const fact = (term, value) => { if (value) facts.append(el("dt", {}, term), el("dd", {}, value)); };
  for (const member of d.members) fact(member.role, member.label);
  fact("Channel", d.channel + (d.ops_channel ? ", ops " + d.ops_channel : ""));
  fact("Owners", (d.owners || []).join(", ") || "none");
  fact("You", d.level === "none" ? "no level in this app" : d.level);
  fact("Dev", d.dev);
  facts.append(el("dt", {}, "Uses"), el("dd", { id: "overview-usage" }, usageLine(d.name) || "…"));
  parts.push(el("h2", {}, "The app"), facts);
  const lines = [d.snapshot, d.heartbeat].filter(Boolean);
  if (lines.length) parts.push(el("div", { class: "muted small lines" }, lines.map((line) => el("p", {}, line))));

  if (d.releases.length) {
    const releases = el("ul", { class: "list" });
    for (const release of d.releases) {
      releases.append(el("li", {}, el("span", { class: "what clamp3", title: release.notes || "" }, el("strong", {}, "v" + release.version), " ", release.notes || ""),
        el("span", { class: "muted small" }, ago(release.released))));
    }
    parts.push(el("h2", {}, "Releases"), releases);
  }
  box.replaceChildren(...parts);
  if (typing && $("say-text")) {
    $("say-text").value = typing.text;
    if (typing.focus) $("say-text").focus();
  }
  renderOpenMenu();
}

// The Open ▾ menu: production, Stage, and the dev page.
function renderOpenMenu() {
  const menu = $("open-menu");
  menu.replaceChildren();
  for (const address of (detail ? detail.addresses : [])) {
    menu.append(el("button", { role: "menuitem", onclick: () => { closeMenu(); window.open(address.url, "_blank", "noopener,noreferrer"); } }, address.what + " ↗"));
  }
  menu.append(el("button", { role: "menuitem", onclick: () => { closeMenu(); openDevPage(openApp); } }, "Dev page ↗"));
}
function closeMenu() { $("open-menu").hidden = true; $("open-button").setAttribute("aria-expanded", "false"); }
$("open-button").addEventListener("click", (event) => {
  event.stopPropagation();
  const menu = $("open-menu");
  menu.hidden = !menu.hidden;
  $("open-button").setAttribute("aria-expanded", String(!menu.hidden));
});
document.addEventListener("click", (event) => { if (!$("open-box").contains(event.target)) closeMenu(); });

// openDevPage opens the dev page as the admin is in the app. The window
// opens now, while the click counts, and goes to the link once the app's
// service has made it.
async function openDevPage(app) {
  const win = window.open("about:blank", "_blank");
  try {
    const answer = await api("/api/devpage", { app });
    if (answer.builds && answer.link) {
      if (win) { win.opener = null; win.location = answer.link; } else location.assign(answer.link);
      return;
    }
    if (win) win.close();
    const ok = await ask({
      title: "Make me an owner of " + app,
      text: (answer.why || "") + " An admin of the machine can make themselves an owner. Like any owner change, it is said in the app's channel.",
      ok: "Make me an owner",
    });
    if (!ok) return;
    const job = await api("/api/owner", { app });
    toast("Making you an owner of " + app + ". When the job is done, open the dev page again.");
    followJob(job);
  } catch (e) {
    if (win) win.close();
    if (!(e instanceof SignIn)) toast(e.message, true);
  }
}

// The confirmations, each saying what the action does.
const actionAsks = {
  stop: (app) => ({ title: "Stop " + app + "?", text: "Its lead, Stage, and production stop, and it doesn't answer in Slack, until it's started again. Its folder and data stay.", ok: "Stop" }),
  rebuild: (app) => ({ title: "Rebuild " + app + "?", text: "hi builds its box image again, with Ubuntu's current packages (a few minutes), and restarts the app on it. Production keeps its address.", ok: "Rebuild" }),
  freeze: (app) => ({ title: "Freeze " + app + "?", text: "Its boxes stop, and its lead reaches nothing but Slack, until someone unfreezes it. The ops channel is told why.", ok: "Freeze", why: "Why: the ops channel is told" }),
  pause: (app) => ({ title: "Pause " + app + "?", text: "Its work stops where it is, until someone unpauses it. The ops channel is told why.", ok: "Pause", why: "Why: the ops channel is told" }),
};
const actionToasts = {
  start: "Starting", restart: "Restarting", stop: "Stopping", rebuild: "Rebuilding", snapshot: "Taking a snapshot of",
  freeze: "Freezing", unfreeze: "Unfreezing", pause: "Pausing", unpause: "Unpausing", say: "Telling the lead of",
};

async function act(action, extra) {
  const app = openApp;
  const request = Object.assign({ app, action }, extra || {});
  if (actionAsks[action]) {
    const answer = await ask(actionAsks[action](app));
    if (!answer) return false;
    if (typeof answer === "string") request.why = answer;
  }
  try {
    const job = await api("/api/action", request);
    toast(actionToasts[action] + " " + app + ". Jobs has its output.");
    followJob(job);
    return true;
  } catch (e) {
    if (!(e instanceof SignIn)) toast(e.message, true);
    return false;
  }
}

// ask is the dialog for a yes: it answers false, true, or the why typed.
function ask({ title, text, ok, why, whyOptional, typed, field, fieldCheck, check, checked }) {
  return new Promise((resolve) => {
    const dialog = $("ask");
    $("ask-title").textContent = title;
    $("ask-text").textContent = text;
    $("ask-ok").textContent = ok || "OK";
    $("ask-error").textContent = "";
    const reason = $("ask-why");
    reason.hidden = !why;
    reason.value = "";
    reason.placeholder = why || "";
    const typedBox = $("ask-typed");
    typedBox.hidden = !typed;
    typedBox.value = "";
    typedBox.placeholder = typed ? "Type " + typed : "";
    const fieldBox = $("ask-field");
    fieldBox.hidden = !field;
    fieldBox.value = "";
    fieldBox.placeholder = field || "";
    $("ask-check-label").hidden = !check;
    $("ask-check-text").textContent = check || "";
    $("ask-check").checked = !!checked;
    const done = (value) => {
      dialog.close();
      $("ask-form").onsubmit = null;
      $("ask-cancel").onclick = null;
      resolve(value);
    };
    $("ask-form").onsubmit = (event) => {
      event.preventDefault();
      if (field) {
        const value = fieldBox.value.trim();
        const problem = fieldCheck ? fieldCheck(value) : value ? "" : "Fill it in.";
        if (problem) { $("ask-error").textContent = problem; return; }
      }
      if (typed && typedBox.value.trim() !== typed) { $("ask-error").textContent = "Type " + typed + " to say so."; return; }
      if (field || check) { done({ field: fieldBox.value.trim(), checked: $("ask-check").checked }); return; }
      if (why) {
        if (!reason.value.trim() && !whyOptional) { $("ask-error").textContent = "Say why: the app's people are told."; return; }
        done(reason.value.trim() || true);
      } else done(true);
    };
    $("ask-cancel").onclick = () => done(false);
    dialog.onclose = () => resolve(false);
    dialog.showModal();
    (field ? fieldBox : typed ? typedBox : why ? reason : $("ask-ok")).focus();
  });
}

function showLink(title, text, link) {
  $("link-title").textContent = title;
  $("link-text").textContent = text;
  $("link-value").value = link;
  $("link-dialog").showModal();
  $("link-value").select();
}
$("link-copy").addEventListener("click", async () => {
  try { await navigator.clipboard.writeText($("link-value").value); toast("Copied."); } catch (e) { $("link-value").select(); document.execCommand("copy"); toast("Copied."); }
});
$("link-close").addEventListener("click", () => { $("link-value").value = ""; $("link-dialog").close(); });

// ---------------------------------------------------------------------------
// Machine: hi, the page's address, its admins

let machineShown = "";
function renderMachine(force) {
  if (!state) return;
  const shown = JSON.stringify([state.version, state.latest, state.address, state.line, state.policy, state.lingers, state.needs_key, state.admins, state.me, !!state.org]);
  if (shown === machineShown && !force) return;
  machineShown = shown;
  const version = $("machine-version");
  version.replaceChildren(el("p", {}, el("strong", {}, "hi " + state.version), state.latest ? el("span", { class: "warn" }, " · " + state.latest + " is out") : el("span", { class: "muted" }, " · the newest, as far as GitHub said")));
  version.append(el("button", { class: state.latest ? "primary" : "", onclick: update }, state.latest ? "Update to " + state.latest : "Update hi"));
  version.append(el("p", { class: "muted small" }, (state.org ? "The org server, the apps, and this page restart on the new hi" : "The apps and this page restart on the new hi") + ": each app's lead is away for about half a minute, the page for a second."));
  badge("machine-badge", state.latest ? 1 : 0);

  const reach = $("machine-reach");
  reach.replaceChildren(
    el("p", {}, el("a", { href: state.address }, state.address)),
    el("p", { class: "muted small" }, state.line));
  if (state.policy) reach.append(el("p", { class: "small" }, "What to ask your IT team: ", el("span", { class: "copyable" }, state.policy), "."));
  if (!state.lingers) {
    reach.append(el("p", { class: "warn small" }, "The page and the apps stop when " + state.user + " logs out, and don't start at boot, until someone runs this on the machine: ", el("code", {}, "sudo loginctl enable-linger " + state.user)));
  }
  $("netbird-form").hidden = !state.needs_key;
  $("machine-user").textContent = state.user;

  const list = $("admins-list");
  list.replaceChildren();
  const orgAdmins = !!(state.org && !state.org.no_owner);
  const canChange = !orgAdmins || state.me.owner;
  $("admin-add-open").hidden = !canChange;
  $("admin-add-email").hidden = !orgAdmins;
  $("admin-add-slack").required = orgAdmins;
  $("admin-add-slack").placeholder = orgAdmins ? "Their Slack member ID" : "Their Slack member ID (optional)";
  $("admin-add-name").placeholder = orgAdmins ? "Their hi user name, such as olga" : "Their name";
  $("admins-note").hidden = !orgAdmins;
  $("admins-note").textContent = orgAdmins ? "They are the org's owner and admins: each approves too, in Slack and here." + (state.me.owner ? "" : " The owner adds and removes them.") : "";
  for (const admin of state.admins) {
    const me = admin.name === state.me.name;
    const acts = el("span", { class: "actions" });
    acts.append(el("button", { onclick: () => newLink(admin, false), title: "A new link, shown to copy; their old one ends" }, "New link"));
    if (admin.slack) acts.append(el("button", { onclick: () => newLink(admin, true), title: "A new link in their Slack DMs; their old one ends" }, "By Slack"));
    if (!me && canChange && !admin.owner) acts.append(el("button", { class: "danger", onclick: () => removeAdmin(admin) }, "Remove"));
    const what = el("span", { class: "what" }, el("strong", {}, admin.name), me ? el("span", { class: "muted" }, " (you)") : null, admin.owner ? el("span", { class: "muted" }, " · the org's owner") : null,
      el("span", { class: "muted small block" }, (admin.slack ? "Slack " + admin.slack : "no Slack member ID") + (admin.email ? " · " + admin.email : "")));
    const li = el("li", {}, what, acts);
    list.append(li);
    if (me && !admin.slack && !orgAdmins) {
      const form = el("form", { class: "stack slack-form", autocomplete: "off" },
        el("label", { for: "my-slack" }, "Your Slack member ID, so you open apps' dev pages as yourself and get new links by DM (in Slack: your profile, ⋮, Copy member ID):"),
        el("div", { class: "signin-row" }, el("input", { id: "my-slack", required: true, maxlength: "20", placeholder: "U012AB3CD" }), el("button", { class: "primary", type: "submit" }, "Save")));
      form.addEventListener("submit", async (event) => {
        event.preventDefault();
        try {
          await api("/api/admins", { action: "slack", name: admin.name, slack: $("my-slack").value.trim() });
          toast("Saved.");
          await refreshState();
        } catch (e) { if (!(e instanceof SignIn)) toast(e.message, true); }
      });
      list.append(el("li", { class: "plain" }, form));
    }
  }
}

async function update() {
  const ok = await ask({ title: state.latest ? "Update hi to " + state.latest + "?" : "Update hi?", text: "hi updates itself, then restarts the apps and this page on the new version. Each app's lead is away for about half a minute; this page for a second, and it comes back by itself.", ok: "Update" });
  if (!ok) return;
  try {
    const job = await api("/api/update", {});
    waitFor = state.version;
    toast("Updating hi. Jobs has its output; this page comes back on the new hi by itself.", true);
    followJob(job);
  } catch (e) { if (!(e instanceof SignIn)) toast(e.message, true); }
}

async function newLink(admin, send) {
  try {
    const answer = await api("/api/admins", { action: "link", name: admin.name, send });
    if (answer.link) showLink("A new link for " + admin.name, answer.text + " Their old link has ended.", answer.link);
    else toast(answer.text);
  } catch (e) { if (!(e instanceof SignIn)) toast(e.message, true); }
}

async function removeAdmin(admin) {
  const ok = await ask({ title: "Take " + admin.name + " off the admins?", text: "Their links and sign-ins end now. They can still log in to the machine, if they could.", ok: "Remove" });
  if (!ok) return;
  try { toast((await api("/api/admins", { action: "remove", name: admin.name })).text); await refreshState(); } catch (e) { if (!(e instanceof SignIn)) toast(e.message, true); }
}

$("admin-add-open").addEventListener("click", () => { $("admin-add").hidden = false; $("admin-add-name").focus(); });
$("admin-add-cancel").addEventListener("click", () => { $("admin-add").hidden = true; });
$("admin-add").addEventListener("submit", async (event) => {
  event.preventDefault();
  const name = $("admin-add-name").value.trim(), slack = $("admin-add-slack").value.trim(), email = $("admin-add-email").value.trim(), send = $("admin-add-send").checked;
  try {
    const answer = await api("/api/admins", { action: "add", name, slack, email, send });
    $("admin-add").reset();
    $("admin-add").hidden = true;
    if (answer.link) showLink(name + " is an admin", answer.text, answer.link);
    else toast(answer.text);
    await refreshState();
  } catch (e) { if (!(e instanceof SignIn)) toast(e.message, true); }
});

$("netbird-form").addEventListener("submit", async (event) => {
  event.preventDefault();
  try {
    const answer = await api("/api/netbird", { key: $("netbird-key").value });
    $("netbird-key").value = "";
    toast(answer.text, true);
    await refreshState();
  } catch (e) { if (!(e instanceof SignIn)) toast(e.message, true); }
});

// ---------------------------------------------------------------------------
// Activity, Jobs, Logs, Incidents

async function refreshActivity() {
  try {
    const list = await api("/api/activity");
    const box = $("activity-list");
    box.replaceChildren();
    if (!list.length) box.append(el("li", { class: "empty" }, "Nothing yet: what admins do here shows up here" + (state && state.org ? ", with the org's audit." : ".")));
    for (const entry of list) {
      box.append(el("li", {}, el("div", {}, el("strong", {}, entry.who), " ", entry.what, entry.app ? el("span", { class: "muted" }, " · " + entry.app) : null, entry.org ? el("span", { class: "muted" }, " · the org") : null),
        el("div", { class: "muted small" }, ago(entry.at))));
    }
  } catch (e) { /* the next round */ }
}

let jobs = [];
let jobOpen = "";
let jobFrom = 0;
function jobsSeen() { return Number(localStorage.getItem("hi-admin-jobs-seen") || 0); }
function markJobsSeen() { try { localStorage.setItem("hi-admin-jobs-seen", String(Date.now())); } catch (e) { /* private mode */ } renderJobsBadge(); }
// Jobs are the open app's; on Home, every job, the machine's too.
const jobsShown = () => (openApp ? jobs.filter((j) => j.app === openApp) : jobs);
function renderJobsBadge() {
  const node = $("jobs-badge");
  const mine = jobsShown();
  const running = mine.some((j) => j.state === "running") || (!openApp && state && state.running > 0);
  const failed = mine.some((j) => j.state === "failed" && new Date(j.ended).getTime() > jobsSeen());
  node.hidden = !running && !failed;
  node.classList.toggle("running", running);
  node.classList.toggle("failed", !running && failed);
}

async function refreshJobs() {
  try { jobs = await api("/api/jobs"); } catch (e) { return; }
  renderJobsBadge();
  const list = $("jobs-list");
  list.replaceChildren();
  const shownJobs = jobsShown();
  if (!openApp && jobs.length) list.append(el("li", { class: "empty" }, "Every job, the machine's too. Open an app for its own."));
  if (!shownJobs.length) list.append(el("li", { class: "empty" }, openApp ? "No jobs for " + openApp + " in the last two weeks: a restart, a rebuild, or a snapshot runs here, with its output." : "No jobs yet: a restart, a rebuild, a snapshot, or an update runs here, with its output."));
  for (const job of shownJobs) {
    const cls = job.state === "running" ? "working" : job.state === "done" ? "done" : "bad";
    list.append(el("li", {}, el("span", { class: "dot " + cls, "aria-hidden": "true" }),
      el("button", { class: "stask-row", onclick: () => openJob(job.id) }, el("span", { class: "stask-title" }, job.title),
        el("span", { class: "stask-num" }, (openApp || !job.app ? "" : job.app + " · ") + job.by + " · " + ago(job.started)))));
  }
  if (jobOpen) refreshJob();
}

function followJob(job) {
  refreshState();
  if (!narrow()) { showView("jobs"); openJob(job.id); } else refreshJobs();
}

function openJob(id) {
  jobOpen = id;
  jobFrom = 0;
  $("jobs-list").hidden = true;
  $("job-one").hidden = false;
  $("job-output").textContent = "";
  refreshJob();
}
$("job-back").addEventListener("click", () => { jobOpen = ""; $("job-one").hidden = true; $("jobs-list").hidden = false; refreshJobs(); });

let jobTimer = 0;
async function refreshJob() {
  clearTimeout(jobTimer);
  const id = jobOpen;
  if (!id) return;
  try {
    const answer = await api("/api/job?id=" + encodeURIComponent(id) + "&from=" + jobFrom);
    if (jobOpen !== id) return;
    $("job-title").textContent = answer.job.title;
    const words = { running: "running", done: "done", failed: "failed" };
    $("job-state").textContent = (words[answer.job.state] || answer.job.state) + " · " + answer.job.by + " · " + ago(answer.job.started);
    $("job-state").className = answer.job.state === "failed" ? "error small" : "muted small";
    const out = $("job-output");
    const atEnd = out.scrollTop + out.clientHeight >= out.scrollHeight - 8;
    if (answer.text) out.textContent += answer.text;
    jobFrom = answer.next;
    if (atEnd) out.scrollTop = out.scrollHeight;
    if (answer.job.state === "running") jobTimer = setTimeout(refreshJob, 1500);
    else {
      refreshState();
      if (openApp && answer.job.app === openApp) refreshDetail();
      // A new app, or a renamed one, opens once its job is done.
      if (creating && creating.job === id && answer.job.state === "done") { const name = creating.name; creating = null; openView(name); }
    }
  } catch (e) { /* the next round */ }
}

// Logs: which app's, its lines as they come; a dot when new ones came
// while the view was away, red when one says something failed.
const logs = { app: null, cursor: "", lines: 0, at: 0, news: "" };
const errorLine = /\b(error|failed|fail|panic|fatal|refused|denied|traceback|exception)\b|\b5\d\d\b/i;
const warnLine = /\bwarn(ing)?\b/i;
// Logs are the open app's; on Home, the org server's on its machine, or
// the admin page's own.
function logsApp() {
  const app = openApp || (state && state.org ? ":org" : "");
  $("logs-whose").textContent = openApp ? openApp : app === ":org" ? "The org server" : "The admin page";
  $("logs-whose").title = openApp ? openApp + "'s lead and service" : "Open an app for its logs";
  return app;
}
$("logs-errors").addEventListener("click", () => {
  const on = $("logs-errors").getAttribute("aria-pressed") !== "true";
  $("logs-errors").setAttribute("aria-pressed", String(on));
  $("logs-lines").classList.toggle("errors-only", on);
});

async function refreshLogs(reset) {
  const app = logsApp();
  if (reset || app !== logs.app) { logs.app = app; logs.cursor = ""; logs.lines = 0; $("logs-lines").replaceChildren(); }
  logs.at = Date.now();
  try {
    const answer = await api("/api/logs?app=" + encodeURIComponent(app) + "&cursor=" + encodeURIComponent(logs.cursor));
    if (logs.app !== app) return;
    const box = $("logs-lines");
    const atEnd = box.scrollTop + box.clientHeight >= box.scrollHeight - 8;
    let failed = false;
    for (const line of answer.lines) {
      const cls = errorLine.test(line.text) ? "err" : warnLine.test(line.text) ? "warn" : "";
      if (cls === "err") failed = true;
      box.append(el("div", { class: "log-line " + cls }, el("span", { class: "log-at" }, new Date(line.at).toLocaleTimeString()), line.text));
    }
    const fresh = logs.cursor !== "" && answer.lines.length > 0;
    logs.cursor = answer.cursor;
    logs.lines += answer.lines.length;
    while (box.childElementCount > 2000) box.firstElementChild.remove();
    $("logs-note").textContent = logs.lines ? "" : "Nothing in the log yet.";
    if (atEnd || reset) box.scrollTop = box.scrollHeight;
    if (fresh && !viewShown("logs")) {
      logs.news = failed || logs.news === "failed" ? "failed" : "running";
      renderLogsDot();
    }
  } catch (e) { /* the next round */ }
}
function renderLogsDot() {
  const node = $("logs-badge");
  node.hidden = !logs.news;
  node.classList.toggle("pulse", !!logs.news);
  node.classList.toggle("running", logs.news === "running");
  node.classList.toggle("failed", logs.news === "failed");
}
function clearLogsDot() { logs.news = ""; renderLogsDot(); }

async function refreshIncidents() {
  try {
    const all = await api("/api/incidents");
    const list = openApp ? all.filter((i) => i.app === openApp) : all;
    const box = $("incidents-list");
    box.replaceChildren();
    if (!openApp && all.length) box.append(el("li", { class: "empty" }, "Every app's. Open an app for its own."));
    if (!list.length) box.append(el("li", { class: "empty" }, (openApp ? openApp + " has had no incidents" : "No incidents") + " in the last month."));
    for (const incident of list) {
      const cls = incident.status === "closed" ? "done" : incident.status === "waiting" ? "blocked" : "bad";
      box.append(el("li", { class: "incident" },
        el("div", { class: "incident-head" }, el("span", { class: "dot " + cls, "aria-hidden": "true" }),
          openApp ? null : el("button", { class: "link-button", onclick: () => openView(incident.app) }, incident.app), el("span", { class: "muted small" }, incident.id + " · " + incident.status + " · " + ago(incident.opened))),
        el("div", { class: "clamp3", title: incident.title }, incident.title),
        el("div", { class: "muted small clamp2", title: incident.last || "" }, incident.where + (incident.last ? " · " + incident.last : ""))));
    }
  } catch (e) { /* the next round */ }
}

// ---------------------------------------------------------------------------
// the org server, on its machine: Requests, Running, and Spend on the
// right; Org, People, and Keys on the left; the policy in the middle.
// Everything goes to the org server under the admin's own name.

const dollars = (n) => "$" + (Number(n) || 0).toFixed(2);
function hours(seconds) {
  const s = Number(seconds) || 0;
  if (s < 3600) return Math.round(s / 60) + " min";
  const h = s / 3600;
  return (Number.isInteger(h) ? h : h.toFixed(1)) + " h";
}
const clock = (when) => new Date(when).toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" });

let org = null;       // /api/org: what runs and what waits
let orgSettings = null; // /api/org/settings
let spendDays = 30;

async function orgDo(change, done) {
  try {
    const answer = await api("/api/org", change);
    if (done) toast(done);
    await Promise.all([refreshState(), refreshOrg(), orgSettings ? refreshOrgSettings() : null]);
    return answer.answer || {};
  } catch (e) {
    if (!(e instanceof SignIn)) toast(e.message, true);
    return null;
  }
}

async function refreshOrg() {
  if (!state || !state.org) return;
  try { org = await api("/api/org"); } catch (e) {
    if (e instanceof SignIn) return;
    org = null;
    $("requests-list").replaceChildren(el("li", { class: "empty error" }, e.message));
    $("running-list").replaceChildren();
    $("running-sum").textContent = "";
    return;
  }
  renderRequests();
  renderRunning();
}

function requestWhat(r) {
  const who = el("strong", {}, r.user);
  switch (r.kind) {
    case "enroll":
      return [who, r.owner ? " (an agent, for " + r.owner + ")" : "", " wants to join, from ", el("strong", {}, r.hostname || "a device")];
    case "extend":
      return [who, " asks for ", hours(r.seconds), " more on ", el("strong", {}, r.name)];
    case "serve":
      return [who, " wants to serve ", el("strong", {}, r.model || r.name), " to the org"];
  }
  return [who, ": ", el("strong", {}, r.hardware || "a machine"), r.provider ? " on " + r.provider : "", r.seconds ? " for " + hours(r.seconds) : "", r.rate ? ", " + r.rate : ""];
}

function renderRequests() {
  const box = $("requests-list");
  box.replaceChildren();
  const list = org ? org.requests : [];
  if (!list.length) box.append(el("li", { class: "empty" }, "Nothing waits for an approval."));
  const groups = $("org-groups");
  groups.replaceChildren(...(org ? org.groups || [] : []).map((g) => el("option", { value: g })));
  for (const r of list) {
    const li = el("li", { class: "incident" },
      el("div", {}, ...requestWhat(r)),
      el("div", { class: "muted small" }, r.id + " · " + ago(r.created) + (r.agent ? " · sent by " + r.agent : "")));
    if (r.reason) li.append(el("div", { class: "small clamp3", title: r.reason }, "Why: " + r.reason));
    if (r.budget) li.append(el("div", { class: (r.over_budget ? "warn" : "muted") + " small" }, r.budget));
    if (r.state === "starting") {
      li.append(el("div", { class: "muted small" }, "Approved by " + (r.decided_by || "?") + "; starting" + (r.progress ? ": " + r.progress : "…")));
    } else if (r.mine) {
      li.append(el("div", { class: "muted small" }, "Yours: another approver decides."));
    } else {
      const acts = el("div", { class: "data-actions" });
      let group = null;
      if (r.new_user) {
        group = el("input", { list: "org-groups", placeholder: "Their group", "aria-label": "Their group", maxlength: "32", class: "small-input" });
        acts.append(group);
      }
      acts.append(el("button", { class: "primary small", onclick: () => {
        if (group && !group.value.trim()) { group.focus(); toast(r.user + " is new: choose their group first."); return; }
        orgDo({ what: "decide", action: "approve", id: r.id, group: group ? group.value.trim() : "" }, "Approved " + r.id + ".");
      } }, "Approve"));
      acts.append(el("button", { class: "small", onclick: async () => {
        const why = await ask({ title: "Deny " + r.id + "?", text: r.user + " is told it was denied, and why if you say.", ok: "Deny", why: "Why (optional): " + r.user + " is told", whyOptional: true });
        if (why === false) return;
        orgDo({ what: "decide", action: "deny", id: r.id, reason: typeof why === "string" ? why : "" }, "Denied " + r.id + ".");
      } }, "Deny…"));
      li.append(acts);
    }
    box.append(li);
  }
}

function renderRunning() {
  const box = $("running-list");
  box.replaceChildren();
  const live = org ? org.live : null;
  const running = live ? live.running || [] : [];
  if (live) {
    let sum = running.length ? running.length + " running, " + dollars(live.rate_per_hour) + " an hour" : "Nothing runs";
    sum += " · today " + dollars(live.today) + " · this month " + dollars((live.month || 0) + (live.models_month || 0));
    if (live.month_budget) sum += " of " + dollars(live.month_budget);
    $("running-sum").textContent = sum;
  }
  if (!running.length) box.append(el("li", { class: "empty" }, "No machine runs for anyone now."));
  const perUser = {};
  for (const r of running) perUser[r.user] = (perUser[r.user] || 0) + 1;
  for (const r of running) {
    const li = el("li", { class: "incident" },
      el("div", { class: "incident-head" }, el("span", { class: "dot working", "aria-hidden": "true" }), el("strong", {}, r.name),
        el("span", { class: "muted small" }, r.user + (r.group ? " · " + r.group : "") + (r.agent ? " · " + r.agent : ""))),
      el("div", { class: "small" }, r.hardware + " on " + r.provider + " · " + r.rate + " · " + dollars(r.cost) + " so far"),
      el("div", { class: "muted small" }, "since " + clock(r.started) + ", stops at " + clock(r.deadline) + ((r.doing || []).length ? " · " + r.doing.join(", ") + " open" : "")));
    if (r.reason) li.append(el("div", { class: "muted small clamp2", title: r.reason }, r.reason));
    const acts = el("div", { class: "data-actions" },
      el("button", { class: "small", onclick: async () => {
        const ok = await ask({ title: "Stop " + r.name + "?", text: "It stops now, and " + r.user + " is told who stopped it. What's on it and not saved elsewhere is gone.", ok: "Stop" });
        if (ok) orgDo({ what: "stop", name: r.name }, "Stopping " + r.name + ".");
      } }, "Stop"));
    if (perUser[r.user] > 1) {
      acts.append(el("button", { class: "small danger", onclick: async () => {
        const ok = await ask({ title: "Stop everything of " + r.user + "?", text: "All " + perUser[r.user] + " of " + r.user + "'s machines stop now. Type their name to say so.", ok: "Stop them all", typed: r.user });
        if (ok) orgDo({ what: "stop", user: r.user }, "Stopping everything of " + r.user + ".");
      } }, "Stop all of " + r.user + "'s"));
    }
    li.append(acts);
    box.append(li);
  }
}

async function refreshSpend() {
  for (const b of document.querySelectorAll("#side-spend [data-days]")) b.setAttribute("aria-pressed", String(Number(b.dataset.days) === spendDays));
  try {
    const answer = await api("/api/org/spend?days=" + spendDays);
    $("spend-text").textContent = answer.text;
  } catch (e) { if (!(e instanceof SignIn)) $("spend-text").textContent = e.message; }
}
for (const b of document.querySelectorAll("#side-spend [data-days]")) {
  b.addEventListener("click", () => { spendDays = Number(b.dataset.days); refreshSpend(); });
}

async function refreshOrgSettings() {
  if (!state || !state.org) return;
  try { orgSettings = await api("/api/org/settings"); } catch (e) {
    if (e instanceof SignIn) return;
    orgSettings = null;
    for (const id of ["org-view", "keys-view"]) $(id).replaceChildren(el("p", { class: "error pad" }, e.message));
    renderOrgServerOnly();
    return;
  }
  if (viewShown("org")) renderOrg();
  if (viewShown("people")) renderPeople();
  if (viewShown("keys")) renderKeys();
  if (middle === ":policy" && !policyEdited) renderPolicy();
}

// A small form that opens under its button.
function inlineForm(fields, submit, label) {
  const form = el("form", { class: "machine-block stack", autocomplete: "off", hidden: true });
  const inputs = {};
  for (const f of fields) {
    const input = f.type === "checkbox"
      ? el("input", { type: "checkbox" })
      : el("input", { type: f.type || "text", required: f.required, maxlength: f.max || "200", placeholder: f.placeholder, "aria-label": f.placeholder || f.label, autocomplete: "off", value: f.value });
    inputs[f.name] = input;
    form.append(f.type === "checkbox" ? el("label", { class: "check" }, input, " " + f.label) : input);
  }
  form.append(el("div", { class: "buttons" }, el("button", { type: "button", onclick: () => { form.hidden = true; } }, "Cancel"), el("button", { class: "primary", type: "submit" }, label || "Save")));
  form.addEventListener("submit", async (event) => {
    event.preventDefault();
    const values = {};
    for (const [name, input] of Object.entries(inputs)) values[name] = input.type === "checkbox" ? input.checked : input.value.trim();
    if (await submit(values) !== false) { form.reset(); form.hidden = true; }
  });
  return form;
}
function heading(text, button) { return el("h2", { class: "with-action" }, text, el("span", { class: "grow" }), button || null); }
function plus(title, form) { return el("button", { class: "side-icon", title, "aria-label": title, onclick: () => { form.hidden = !form.hidden; const first = form.querySelector("input"); if (first && !form.hidden) first.focus(); } }, "+"); }

function renderOrgServerOnly() {
  const box = $("org-view");
  box.prepend(orgServerBlock());
}

function orgServerBlock() {
  const o = state.org;
  const block = el("div", { class: "machine-block" });
  block.append(el("p", { class: "incident-head" }, el("span", { class: "dot " + (o.up ? "idle" : "bad"), "aria-hidden": "true" }), o.up ? "The org server runs." : "The org server doesn't answer."));
  if (o.error) block.append(el("p", { class: "error small" }, o.error));
  if (orgSettings && orgSettings.listen) block.append(el("p", { class: "muted small" }, "Devices join with ", el("code", {}, "hi org join " + orgSettings.listen)));
  if (o.service === "user") {
    block.append(el("p", { class: "muted small" }, "A user service, hi-org.service, beside this page."));
    block.append(el("button", { class: "small", onclick: async () => {
      const ok = await ask({ title: "Restart the org server?", text: "It is down for about a second. Machines keep running, and requests wait.", ok: "Restart" });
      if (ok) orgDo({ what: "restart" }, "The org server restarted.");
    } }, o.up ? "Restart" : "Start"));
  } else if (o.service === "system") {
    block.append(el("p", { class: "warn small" }, "A system service, so this page can't restart it. hi update on the machine makes it a user service, with sudo this once."));
  } else {
    block.append(el("p", { class: "warn small" }, "No service runs it. On the machine: ", el("code", {}, "hi org init"), " makes one."));
  }
  block.append(el("button", { class: "small", onclick: async () => { if (middle) await openView(""); showView("logs"); } }, "Its logs"));
  return block;
}

function renderOrg() {
  const box = $("org-view");
  const s = orgSettings;
  if (!s) { box.replaceChildren(el("p", { class: "muted pad" }, "…")); return; }
  const parts = [el("h2", {}, "The org server"), orgServerBlock()];

  // The owner.
  const owner = s.people.owner;
  const handOn = inlineForm([
    { name: "slack", placeholder: "The new owner's Slack member ID", required: true, max: "20" },
    { name: "email", type: "email", placeholder: "Their email", required: true },
    { name: "name", placeholder: "Their hi user name (from their email if left out)", max: "32" },
  ], (v) => orgDo({ what: "owner", slack: v.slack, email: v.email, name: v.name }, "Handed on."), "Hand it on");
  parts.push(heading("Owner"));
  if (!owner) parts.push(el("p", { class: "warn small" }, "The org has no owner yet. On the machine: ", el("code", {}, "hi org owner <Slack member ID> --email <address>")));
  else {
    parts.push(el("div", { class: "machine-block" }, el("p", {}, el("strong", {}, owner.name), " ", el("span", { class: "muted small" }, "Slack " + owner.slack + (owner.email ? " · " + owner.email : ""))),
      el("div", { class: "data-actions" }, state.me.owner ? el("button", { class: "small", onclick: () => { handOn.hidden = !handOn.hidden; } }, "Hand it on…") : null,
        el("button", { class: "small", onclick: () => showView("machine") }, "The admins"))));
    parts.push(handOn);
  }

  // Slack.
  parts.push(heading("Slack"));
  const slack = el("div", { class: "machine-block" });
  if (s.slack.set) slack.append(el("p", { class: "small" }, "Approvals go to the channel ", el("code", {}, s.slack.channel || "?"), ", and to each approver's Home tab."));
  else slack.append(el("p", { class: "warn small" }, "Not set up, so approvals are only here and on the machine. On the machine: ", el("code", {}, "hi org slack setup"), " (its app: ", el("code", {}, "hi org slack manifest"), ")."));
  const approvers = Object.entries(s.approvers || {}).sort((a, b) => a[1].localeCompare(b[1]));
  if (approvers.length) slack.append(el("p", { class: "muted small" }, "Approvers: " + approvers.map(([id, name]) => name).join(", ") + ". The owner and the admins approve; someone who only approves: ", el("code", {}, "hi org approvers add"), "."));
  parts.push(slack);

  // The policy.
  parts.push(heading("Policy"));
  const policy = el("div", { class: "machine-block" });
  const p = s.policy || {};
  if (p.error) policy.append(el("p", { class: "error small" }, p.error + ". Until it's fixed, every start needs a person."));
  else if (!p.exists) policy.append(el("p", { class: "muted small" }, "No policy: every start needs a person, and there are no budgets."));
  else {
    const groups = Object.entries((p.policy && p.policy.groups) || {});
    if (!groups.length) policy.append(el("p", { class: "muted small" }, "No groups in it."));
    for (const [name, g] of groups) policy.append(el("p", { class: "small" }, el("strong", {}, name), ": " + groupLine(g)));
  }
  policy.append(el("button", { class: "small", onclick: openPolicy }, p.exists ? "Edit the policy" : "Make one"));
  parts.push(policy);

  // Templates.
  const tForm = inlineForm([
    { name: "name", placeholder: "Its name, such as team", required: true, max: "32" },
    { name: "url", placeholder: "Its git URL, https://…", required: true },
    { name: "ref", placeholder: "The branch or tag (optional)" },
    { name: "token", type: "password", placeholder: "A token that reads it, if it's private (kept, never shown)" },
  ], (v) => orgDo({ what: "template", action: "add", name: v.name, url: v.url, ref: v.ref, token: v.token }, "Serving " + v.name + "'s templates."), "Add");
  parts.push(heading("Templates", plus("Serve a repository's templates", tForm)), tForm);
  const templates = el("ul", { class: "list" });
  for (const t of s.templates || []) {
    templates.append(el("li", {}, el("span", { class: "what" }, el("strong", {}, t.name), el("span", { class: "muted small block" }, shown(t.url) + (t.ref ? " @ " + t.ref : "")),
      el("span", { class: "muted small block" }, (t.layers || []).length + " templates, " + (t.skills || []).length + " skills" + (t.commit ? " · " + t.commit.slice(0, 7) : "") + (t.fetched ? " · " + ago(t.fetched) : "")),
      t.problem ? el("span", { class: "warn small block" }, t.problem) : null),
      el("span", { class: "actions" }, el("button", { onclick: () => orgDo({ what: "template", action: "sync", name: t.name }, "Fetched " + t.name + ".") }, "Sync"),
        el("button", { class: "danger", onclick: async () => { if (await ask({ title: "Stop serving " + t.name + "?", text: "Devices no longer see its templates and skills. Projects made from them keep what they have.", ok: "Stop serving" })) orgDo({ what: "template", action: "remove", name: t.name }, "Stopped serving " + t.name + "."); } }, "Remove"))));
  }
  if (!(s.templates || []).length) templates.append(el("li", { class: "plain muted small" }, "None: hi init offers only hi's own."));
  parts.push(templates);

  // Data.
  const dForm = inlineForm([
    { name: "orgs", placeholder: "Hugging Face organizations, space between them", required: true },
    { name: "token", type: "password", placeholder: "A Hugging Face token that reads them (kept, never shown)", required: true },
  ], (v) => orgDo({ what: "data", action: "add", orgs: v.orgs.split(/[\s,]+/).filter(Boolean), token: v.token }, "Serving their data."), "Add");
  parts.push(heading("Data", plus("Serve a Hugging Face organization", dForm)), dForm);
  const data = el("ul", { class: "list" });
  for (const d of s.data || []) {
    data.append(el("li", {}, el("span", { class: "what" }, el("strong", {}, d.org),
      el("span", { class: "muted small block" }, d.datasets + " datasets, " + d.models + " models, " + d.buckets + " buckets" + (d.account ? " · as " + d.account + (d.role ? " (" + d.role + ")" : "") : "")),
      d.problem ? el("span", { class: "warn small block" }, d.problem) : null),
      el("span", { class: "actions" }, el("button", { onclick: () => orgDo({ what: "data", action: "test", name: d.org }, "Checked " + d.org + ".") }, "Test"),
        el("button", { class: "danger", onclick: async () => { if (await ask({ title: "Stop serving " + d.org + "?", text: "hi data no longer reaches its datasets and models. Its token is forgotten.", ok: "Stop serving" })) orgDo({ what: "data", action: "remove", name: d.org }, "Stopped serving " + d.org + "."); } }, "Remove"))));
  }
  if (!(s.data || []).length) data.append(el("li", { class: "plain muted small" }, "None."));
  parts.push(data);

  // Models served on the team's machines.
  parts.push(heading("Models on the team's machines"));
  const served = el("ul", { class: "list" });
  const list = (s.ai && s.ai.served_list) || [];
  for (const m of list) {
    served.append(el("li", {}, el("span", { class: "what" }, el("strong", {}, m.name), el("span", { class: "muted small block" }, (m.model || "") + (m.user ? " · " + m.user : "") + (m.host ? " on " + m.host : "") + " · " + (m.state && m.state !== "approved" ? m.state : m.up ? "up" : "stopped"))),
      el("span", { class: "actions" }, el("button", { class: "danger", onclick: async () => { if (await ask({ title: "Stop passing requests to " + m.name + "?", text: "hi q stops sending the org's requests to it. The machine keeps serving it.", ok: "Stop" })) orgDo({ what: "served", name: m.name }, "Stopped passing requests to " + m.name + "."); } }, "Remove"))));
  }
  if (!list.length) served.append(el("li", { class: "plain muted small" }, "None: hi serve llm --org on a machine names one to the org."));
  parts.push(served);

  // Wall screens.
  const wForm = inlineForm([
    { name: "name", placeholder: "Where the screen is, such as Office TV", required: true, max: "40" },
    { name: "full", type: "checkbox", label: "Names in full, not initials" },
    { name: "reasons", type: "checkbox", label: "Why each machine was asked for" },
  ], async (v) => {
    try {
      const answer = await api("/api/walls", { action: "add", name: v.name, full: v.full, reasons: v.reasons });
      showLink("A wall link for " + v.name, answer.text, answer.link);
      refreshOrgSettings();
    } catch (e) { if (!(e instanceof SignIn)) toast(e.message, true); return false; }
  }, "Make the link");
  parts.push(heading("Wall screens", plus("A wall link for a screen", wForm)), wForm);
  const walls = el("ul", { class: "list" });
  for (const w of s.walls || []) {
    walls.append(el("li", {}, el("span", { class: "what" }, el("strong", {}, w.name), el("span", { class: "muted small block" }, (w.full ? "names in full" : "initials") + (w.reasons ? ", with reasons" : "") + " · by " + (w.by || "?") + " " + ago(w.made) + (w.seen ? " · open " + ago(w.seen) : " · not opened yet"))),
      el("span", { class: "actions" }, el("button", { class: "danger", onclick: async () => { if (await ask({ title: "End " + w.name + "'s wall link?", text: "The screen stops showing anything at once.", ok: "End it" })) wallEnd(w.name); } }, "End"))));
  }
  if (!(s.walls || []).length) walls.append(el("li", { class: "plain muted small" }, "None: a screen opens a wall link in a browser, read-only."));
  parts.push(walls);
  if ((s.problems || []).length) parts.push(el("p", { class: "warn small" }, s.problems.join(" · ")));
  box.replaceChildren(...parts);
}

async function wallEnd(name) {
  try { toast((await api("/api/walls", { action: "end", name })).text); refreshOrgSettings(); } catch (e) { if (!(e instanceof SignIn)) toast(e.message, true); }
}

function groupLine(g) {
  const bits = [];
  if (g.max_hours) bits.push("up to " + g.max_hours + " h a start");
  if (g.hardware && g.hardware.length) bits.push("only " + g.hardware.join(", "));
  if (g.auto_approve) bits.push("approved by itself up to " + dollars(g.auto_approve.max_price_per_hour) + "/h for " + g.auto_approve.max_hours + " h");
  if (g.user_monthly_budget_usd) bits.push(dollars(g.user_monthly_budget_usd) + " a user a month");
  if (g.group_monthly_budget_usd) bits.push(dollars(g.group_monthly_budget_usd) + " for the group a month");
  return bits.join(", ") || "no limits; every start needs a person";
}

// People: the org's users, their groups and devices.
function renderPeople() {
  const box = $("people-list");
  const users = (orgSettings && orgSettings.users) || [];
  const query = $("people-search").value.trim().toLowerCase();
  box.replaceChildren();
  const shownUsers = users.filter((u) => !query || u.name.includes(query) || (u.group || "").includes(query));
  if (!users.length) box.append(el("li", { class: "empty" }, "Nobody yet: devices ask to join with hi org join, and show up in Requests."));
  else if (!shownUsers.length) box.append(el("li", { class: "empty" }, "Nobody matches."));
  for (const u of shownUsers) {
    const devices = (u.devices || []).map((d) => d.hostname + (d.last_seen && new Date(d.last_seen).getTime() > 0 ? " (" + ago(d.last_seen) + ")" : ""));
    box.append(el("li", {}, el("span", { class: "what" }, el("strong", {}, u.name), " ", el("span", { class: "muted small" }, (u.group || "no group") + (u.kind === "agent" ? " · an agent of " + u.owner : u.kind === "viewer" ? " · a viewer" : "")),
      el("span", { class: "muted small block" }, devices.length ? devices.join(", ") : "no devices")),
      el("span", { class: "actions" }, el("button", { class: "danger", onclick: async () => {
        const ok = await ask({ title: "Remove " + u.name + "?", text: "They and their " + (u.devices || []).length + " devices leave the org: nothing more starts for them. What runs keeps running until it's stopped (Running). Type their name to say so.", ok: "Remove", typed: u.name });
        if (ok) orgDo({ what: "user", action: "remove", name: u.name }, u.name + " has left the org.");
      } }, "Remove"))));
  }
}
$("people-search").addEventListener("input", renderPeople);
$("user-add-open").addEventListener("click", () => { $("user-add").hidden = !$("user-add").hidden; $("user-add-name").focus(); });
$("user-add-cancel").addEventListener("click", () => { $("user-add").hidden = true; });
$("user-add").addEventListener("submit", async (event) => {
  event.preventDefault();
  const name = $("user-add-name").value.trim();
  const done = await orgDo({ what: "user", action: "add", name, group: $("user-add-group").value.trim(), key: $("user-add-key").value.trim() }, name + " is in the org.");
  if (done) { $("user-add").reset(); $("user-add").hidden = true; }
});

// Keys: set or not, never shown.
function renderKeys() {
  const box = $("keys-view");
  const s = orgSettings;
  if (!s) return;
  const parts = [el("h2", {}, "Compute providers"), el("p", { class: "muted small" }, "The org server starts machines with these. Each key is checked with its provider before it's kept, and never shown again.")];
  const list = el("ul", { class: "list" });
  for (const p of s.providers || []) {
    const form = inlineForm([{ name: "key", type: "password", placeholder: p.name + "'s API key", required: true }], (v) => orgDo({ what: "provider", action: "set", name: p.name, key: v.key }, p.name + "'s key is kept."), "Check and keep");
    list.append(el("li", {}, el("span", { class: "what" }, el("strong", {}, p.name), " ", el("span", { class: p.set ? "muted small" : "warn small" }, p.set ? "set" : "not set")),
      el("span", { class: "actions" }, el("button", { onclick: () => { form.hidden = !form.hidden; } }, p.set ? "Replace" : "Set"),
        p.set ? el("button", { class: "danger", onclick: async () => { if (await ask({ title: "Remove " + p.name + "'s key?", text: "Nothing new starts on " + p.name + ". What runs there now keeps running, and still stops at its time.", ok: "Remove" })) orgDo({ what: "provider", action: "remove", name: p.name }, p.name + "'s key is gone."); } }, "Remove") : null)));
    list.append(el("li", { class: "plain" }, form));
  }
  parts.push(list);

  const ai = s.ai || {};
  parts.push(el("h2", {}, "Models for hi q"), el("p", { class: "muted small" }, "hi q on the org's devices asks models through the org server, with the org's key."));
  const aiBlock = el("div", { class: "machine-block" });
  aiBlock.append(el("p", {}, ai.has_key ? (ai.on ? "On" : "Off") : "No key yet", ai.model ? el("span", { class: "muted small" }, " · " + ai.model + (ai.url ? " at " + shown(ai.url).replace(/^https?:\/\//, "") : "")) : null));
  if (ai.usage) aiBlock.append(el("p", { class: "muted small" }, "This month: " + ai.usage));
  const aiForm = inlineForm([
    { name: "key", type: "password", placeholder: ai.has_key ? "A new OpenRouter key (empty keeps the one there)" : "The OpenRouter key", required: !ai.has_key },
    { name: "model", placeholder: "The default model", value: ai.model || "" },
  ], (v) => orgDo({ what: "ai", action: "set", key: v.key, model: v.model }, "Checked and kept: hi q uses it now."), "Check and keep");
  aiBlock.append(el("button", { class: "small", onclick: () => { aiForm.hidden = !aiForm.hidden; } }, ai.has_key ? "Change…" : "Set the key…"));
  if (ai.has_key) {
    aiBlock.append(el("button", { class: "small", onclick: () => orgDo({ what: "ai", action: ai.on ? "off" : "on" }, ai.on ? "Turned off." : "Turned on.") }, ai.on ? "Turn off" : "Turn on"));
    aiBlock.append(el("button", { class: "small danger", onclick: async () => { if (await ask({ title: "Remove the OpenRouter key?", text: "hi q on the org's devices can no longer ask models through the org.", ok: "Remove" })) orgDo({ what: "ai", action: "remove" }, "The key is gone."); } }, "Remove the key"));
  }
  parts.push(aiBlock, aiForm);
  box.replaceChildren(...parts);
}

// ---------------------------------------------------------------------------
// the policy, in the middle: each group's limits as a form, checked by
// the org server before it's saved.

let policyDraft = null;
let policyEdited = false;
let policyText = false;

function openPolicy() {
  setMiddle(":policy");
  policyEdited = false;
  policyText = false;
  policyDraft = null;
  if (narrow()) { panes.left.open = false; keepPanes(); renderPanes(); }
  renderPolicy();
}

function renderPolicy() {
  const box = $("policy");
  const p = orgSettings && orgSettings.policy;
  if (!p) { box.replaceChildren(el("p", { class: "muted" }, "…")); refreshOrgSettings(); return; }
  if (!policyDraft) {
    try { policyDraft = JSON.parse(JSON.stringify(p.policy || JSON.parse(p.exists && p.text ? p.text : p.example))); } catch (e) { policyDraft = { groups: {} }; }
    if (!policyDraft.groups) policyDraft.groups = {};
  }
  const changed = () => { policyEdited = true; };
  const parts = [el("div", { class: "admin-head" }, el("h1", { class: "admin-title" }, "The org's policy"),
    el("span", { class: "muted" }, p.exists ? "" : "none yet: this is the example to start from"))];
  parts.push(el("p", { class: "purpose" }, "Per group: how long a start may run, what hardware, what is approved without a person, and monthly budgets. Budgets never block: going over warns, and the request goes to a person. It applies to the next request."));
  if (p.error) parts.push(el("p", { class: "error" }, "The policy on the machine doesn't read: " + p.error));
  const error = el("p", { class: "error", id: "policy-error" });
  if (policyText) {
    const area = el("textarea", { id: "policy-text", class: "policy-text", rows: "24", spellcheck: "false", "aria-label": "The policy, as JSON" });
    area.value = JSON.stringify(policyDraft, null, 2);
    area.addEventListener("input", changed);
    parts.push(area);
  } else {
    for (const [name, g] of Object.entries(policyDraft.groups)) parts.push(policyGroup(name, g, changed));
    const add = el("form", { class: "say-form", autocomplete: "off" },
      el("input", { id: "policy-new-group", maxlength: "32", placeholder: "A new group's name, such as students", "aria-label": "A new group's name" }),
      el("button", { type: "submit" }, "Add the group"));
    add.addEventListener("submit", (event) => {
      event.preventDefault();
      const name = $("policy-new-group").value.trim().toLowerCase();
      if (!name || policyDraft.groups[name]) return;
      policyDraft.groups[name] = {};
      changed();
      renderPolicy();
    });
    parts.push(add);
    const reports = policyDraft.reports || {};
    const box2 = el("div", { class: "data-actions" }, el("span", { class: "muted small" }, "Slack reports:"));
    for (const which of ["daily", "weekly", "monthly"]) {
      const input = el("input", { type: "checkbox" });
      input.checked = reports[which] !== false;
      input.addEventListener("change", () => { policyDraft.reports = Object.assign({}, policyDraft.reports, { [which]: input.checked }); changed(); });
      box2.append(el("label", { class: "check small" }, input, " " + which));
    }
    parts.push(box2);
  }
  parts.push(error);
  parts.push(el("div", { class: "data-actions" },
    el("button", { class: "primary", onclick: savePolicy }, "Check and save"),
    el("button", { onclick: () => { if (policyText) { try { policyDraft = JSON.parse($("policy-text").value); } catch (e) { error.textContent = "That isn't JSON: " + e.message; return; } } policyText = !policyText; renderPolicy(); } }, policyText ? "As a form" : "As text"),
    el("button", { onclick: () => { policyDraft = null; policyEdited = false; renderPolicy(); } }, "Undo my changes"),
    el("button", { onclick: () => setMiddle("") }, "Close")));
  box.replaceChildren(...parts);
}

function policyGroup(name, g, changed) {
  const card = el("div", { class: "card-box policy-group" });
  const num = (label, get, set, step) => {
    const input = el("input", { type: "number", min: "0", step: step || "any", value: get() ?? "", placeholder: "none", "aria-label": name + ": " + label });
    input.addEventListener("input", () => { set(input.value === "" ? undefined : Number(input.value)); changed(); });
    return el("label", { class: "field" }, el("span", { class: "muted small" }, label), input);
  };
  const setKey = (key) => (value) => { if (value === undefined || value === 0) delete g[key]; else g[key] = value; };
  const hardware = el("input", { value: (g.hardware || []).join(", "), placeholder: "any", "aria-label": name + ": only this hardware" });
  hardware.addEventListener("input", () => { const list = hardware.value.split(/[\s,]+/).filter(Boolean); if (list.length) g.hardware = list; else delete g.hardware; changed(); });
  const auto = el("input", { type: "checkbox" });
  auto.checked = !!g.auto_approve;
  const autoFields = el("div", { class: "policy-fields", hidden: !g.auto_approve },
    num("Up to $ an hour", () => g.auto_approve && g.auto_approve.max_price_per_hour, (v) => { g.auto_approve = Object.assign({}, g.auto_approve, { max_price_per_hour: v || 0 }); }),
    num("For up to hours", () => g.auto_approve && g.auto_approve.max_hours, (v) => { g.auto_approve = Object.assign({}, g.auto_approve, { max_hours: v || 0 }); }));
  auto.addEventListener("change", () => {
    if (auto.checked) g.auto_approve = g.auto_approve || { max_price_per_hour: 1, max_hours: 2 };
    else delete g.auto_approve;
    changed();
    renderPolicy();
  });
  const kept = [];
  if (g.template_sources) kept.push("its template sources");
  if (g.data) kept.push("its data");
  card.append(el("div", { class: "card-head pad" }, el("strong", { class: "card-name" }, name), el("span", { class: "grow" }),
    el("button", { class: "small danger", onclick: () => { delete policyDraft.groups[name]; changed(); renderPolicy(); } }, "Remove the group")));
  card.append(el("div", { class: "policy-fields" },
    num("Up to hours a start", () => g.max_hours, setKey("max_hours")),
    num("$ a user a month", () => g.user_monthly_budget_usd, setKey("user_monthly_budget_usd")),
    num("$ for the group a month", () => g.group_monthly_budget_usd, setKey("group_monthly_budget_usd")),
    el("label", { class: "field wide" }, el("span", { class: "muted small" }, "Only this hardware"), hardware)));
  card.append(el("label", { class: "check pad small" }, auto, " Approved without a person, within:"), autoFields);
  if (kept.length) card.append(el("p", { class: "muted small pad" }, "Also " + kept.join(" and ") + ", kept as they are; change them as text."));
  return card;
}

async function savePolicy() {
  let text;
  if (policyText) text = $("policy-text").value;
  else text = JSON.stringify(policyDraft, null, 2);
  try {
    await api("/api/org", { what: "policy", text });
    policyEdited = false;
    policyDraft = null;
    toast("Saved. It applies to the next request.");
    await refreshOrgSettings();
    renderPolicy();
  } catch (e) {
    if (!(e instanceof SignIn)) $("policy-error").textContent = e.message;
  }
}

// Home's card: the org's day.
function renderHomeOrg() {
  const box = $("home-org");
  const o = state.org;
  if (!o) { box.replaceChildren(); return; }
  const card = el("div", { class: "card-box org-card" });
  const head = el("div", { class: "card-head pad" }, el("span", { class: "dot " + (o.up ? "idle" : "bad"), "aria-hidden": "true" }), el("strong", { class: "card-name" }, "The org"),
    el("span", { class: "muted small" }, o.up ? "runs" : "doesn't answer"), el("span", { class: "grow" }), o.owner ? el("span", { class: "muted small" }, "owned by " + o.owner) : null);
  card.append(head);
  if (o.no_owner) card.append(el("p", { class: "warn small pad" }, "No owner yet. On the machine: ", el("code", {}, "hi org owner <Slack member ID> --email <address>")));
  if (!o.up) card.append(el("p", { class: "error small pad" }, o.error || "The org server doesn't answer."));
  else {
    const stats = el("div", { class: "org-stats pad" });
    const stat = (n, label, view, warn) => el("button", { class: "org-stat" + (warn ? " warn" : ""), onclick: () => showView(view) }, el("strong", {}, n), el("span", { class: "muted small" }, label));
    stats.append(stat(o.waiting, o.waiting === 1 ? "request waits" : "requests wait", "requests", o.waiting > 0),
      stat(o.running, (o.running === 1 ? "machine runs" : "machines run") + (o.running ? ", " + dollars(o.rate_per_hour) + "/h" : ""), "running"),
      stat(dollars(o.today), "today", "spend"),
      stat(dollars(o.month), "this month" + (o.month_budget ? " of " + dollars(o.month_budget) : ""), "spend", o.month_budget && o.month > o.month_budget));
    card.append(stats);
  }
  box.replaceChildren(card);
}

// ---------------------------------------------------------------------------
// New app: hi app new's ten steps as pages, each answer checked by the
// machine as it's given and kept there, so a reload loses nothing. The
// page holds no secret: a token is sent once, and the machine says only
// whether it has one.

const newSteps = [
  ["lead", "Lead"], ["purpose", "Purpose"], ["slack", "Slack app"], ["channel", "Channel"], ["people", "People"],
  ["members", "Members"], ["models", "Models"], ["code", "Code"], ["network", "Network"], ["heartbeats", "Heartbeats"],
];
let draft = null;      // the open new app, as the machine keeps it
let newStep = 0;       // its step on the page; newSteps.length is the review
let newHome = null;    // /api/new
let slackLists = { channels: null, people: null, error: "" };
let draftPoll = 0;

function openNew(name) {
  setMiddle(name ? ":new/" + name : ":new");
  if (narrow()) { panes.left.open = false; keepPanes(); renderPanes(); }
  draft = null;
  slackLists = { channels: null, people: null, error: "" };
  if (name) loadDraft(name, true); else loadNewHome();
}
$("apps-new").addEventListener("click", () => openNew(""));
$("home-new").addEventListener("click", () => openNew(""));

async function loadNewHome() {
  $("newapp").replaceChildren(el("p", { class: "muted" }, "…"));
  try { newHome = await api("/api/new"); } catch (e) { if (!(e instanceof SignIn)) $("newapp").replaceChildren(el("p", { class: "error" }, e.message)); return; }
  renderNewHome();
}

function renderNewHome() {
  const box = $("newapp");
  const parts = [el("div", { class: "admin-head" }, el("h1", { class: "admin-title" }, "New app"))];
  parts.push(el("p", { class: "purpose" }, "An app for the people in one Slack channel, with its lead, its members, and its project. Ten steps, as hi app new asks them; what's done is kept, so you can stop and carry on."));
  const name = el("input", { id: "new-name", required: true, maxlength: "20", pattern: "[a-z][a-z0-9-]*", placeholder: "Its name, such as payments", "aria-label": "The app's name", autocomplete: "off" });
  const folder = el("input", { id: "new-folder", maxlength: "300", placeholder: (newHome.folder || "~") + "/app-<name>", "aria-label": "Its folder", autocomplete: "off" });
  const adopt = el("input", { id: "new-adopt", maxlength: "300", placeholder: "github.com/owner/name, a URL, or a folder here (optional)", "aria-label": "A project to adopt", autocomplete: "off" });
  const file = el("input", { id: "new-file", type: "file", accept: ".json,application/json", "aria-label": "Answers from a file" });
  const error = el("p", { class: "error small" });
  name.addEventListener("input", () => { folder.placeholder = (newHome.folder || "~") + "/app-" + (name.value.trim() || "<name>"); });
  const form = el("form", { class: "new-start stack", autocomplete: "off" },
    el("label", { class: "field" }, el("span", { class: "muted small" }, "Its name: lowercase letters, digits, and dashes. Its folder and its Slack command are named after it."), name),
    el("label", { class: "field" }, el("span", { class: "muted small" }, "Its folder, where its project, tasks, and memory live"), folder),
    el("label", { class: "field" }, el("span", { class: "muted small" }, "Start from someone's project, to adopt it: the app keeps its own copy and never pushes to theirs"), adopt),
    el("label", { class: "field" }, el("span", { class: "muted small" }, "From a file: answers someone wrote before, as hi app new --from takes them (no secrets in it)"), file),
    error,
    el("div", { class: "data-actions" }, el("button", { class: "primary", type: "submit" }, "Start")));
  form.addEventListener("submit", async (event) => {
    event.preventDefault();
    error.textContent = "";
    let from = null;
    if (file.files && file.files[0]) {
      try { from = JSON.parse(await file.files[0].text()); } catch (e) { error.textContent = "That file isn't JSON: " + e.message; return; }
    }
    try {
      draft = await api("/api/new", { action: "start", name: name.value.trim(), folder: folder.value.trim(), adopt: adopt.value.trim(), from });
      setMiddle(":new/" + draft.name);
      newStep = 0;
      renderDraft();
    } catch (e) { if (!(e instanceof SignIn)) error.textContent = e.message; }
  });
  parts.push(form);

  if (newHome.drafts.length) {
    const list = el("ul", { class: "list" });
    for (const d of newHome.drafts) {
      const done = newSteps.filter(([key]) => d.done && d.done[key]).length;
      list.append(el("li", {}, el("span", { class: "what" }, el("strong", {}, d.name), " ", el("span", { class: "muted small" }, d.job_state === "running" ? "being created" : d.job_state === "failed" ? "creating it failed: see Jobs" : done + " of 10 steps"),
        el("span", { class: "muted small block" }, (d.by || "?") + " started it " + ago(d.started) + " · " + d.folder)),
        el("span", { class: "actions" }, el("button", { onclick: () => openNew(d.name) }, "Carry on"),
          el("button", { class: "danger", onclick: async () => {
            if (!(await ask({ title: "Discard the new app " + d.name + "?", text: "Its answers and the tokens given for it are forgotten. Nothing was made yet.", ok: "Discard" }))) return;
            try { await api("/api/new", { action: "discard", name: d.name }); loadNewHome(); } catch (e) { if (!(e instanceof SignIn)) toast(e.message, true); }
          } }, "Discard"))));
    }
    parts.push(el("h2", {}, "Under way"), list);
  }

  parts.push(el("h2", {}, "Bring one back"));
  parts.push(el("p", { class: "muted small" }, "An app's folder that's here but isn't an app on this machine: one removed with its folder kept, or moved, or copied from another machine. It starts again from what its folder holds."));
  const back = el("ul", { class: "list" });
  for (const b of newHome.back || []) {
    back.append(el("li", {}, el("span", { class: "what" }, el("strong", {}, b.name), el("span", { class: "muted small block" }, b.folder + (b.purpose ? " · " + b.purpose : ""))),
      el("span", { class: "actions" }, el("button", { onclick: () => bringBack(b.folder, b.name) }, "Bring back"))));
  }
  if (!(newHome.back || []).length) back.append(el("li", { class: "plain muted small" }, "None beside the apps' folders."));
  parts.push(back);
  const typed = el("input", { maxlength: "300", placeholder: "Or its folder's full path", "aria-label": "An app's folder" });
  const backForm = el("form", { class: "say-form", autocomplete: "off" }, typed, el("button", { type: "submit" }, "Bring back"));
  backForm.addEventListener("submit", (event) => { event.preventDefault(); if (typed.value.trim()) bringBack(typed.value.trim(), ""); });
  parts.push(backForm);
  box.replaceChildren(...parts);
  name.focus();
}

async function bringBack(folder, name) {
  if (!(await ask({ title: "Bring back " + (name || "the app in " + folder) + "?", text: "It becomes an app on this machine again, by its name, and starts: hi app up " + folder + ".", ok: "Bring it back" }))) return;
  try { const job = await api("/api/apps", { action: "back", folder }); toast("Bringing it back. Jobs has its output."); followJob(job); } catch (e) { if (!(e instanceof SignIn)) toast(e.message, true); }
}

async function loadDraft(name, first) {
  if (first) $("newapp").replaceChildren(el("p", { class: "muted" }, "…"));
  try {
    const fresh = await api("/api/new/draft?name=" + encodeURIComponent(name));
    if (middle !== ":new/" + name) return;
    const same = draft && JSON.stringify(draft) === JSON.stringify(fresh);
    draft = fresh;
    if (first) newStep = Math.max(0, newSteps.findIndex(([key]) => !(draft.done || {})[key]));
    if (first && newSteps.every(([key]) => (draft.done || {})[key])) newStep = newSteps.length;
    if (!same && !(first === false && document.activeElement && $("newapp").contains(document.activeElement) && document.activeElement.tagName !== "BUTTON")) renderDraft();
  } catch (e) {
    if (e instanceof SignIn) return;
    if (first) $("newapp").replaceChildren(el("p", { class: "error" }, e.message), el("button", { onclick: () => openNew("") }, "New app"));
  }
}

// A step's answers go to the machine, which checks them as the terminal
// does; on to the next when it took them.
async function saveStep(answers) {
  const key = newSteps[newStep][0];
  const error = $("step-error");
  if (error) error.textContent = "";
  const busy = $("step-next");
  if (busy) { busy.disabled = true; busy.textContent = "Checking…"; }
  try {
    draft = await api("/api/new", Object.assign({ action: "step", name: draft.name, step: key }, answers));
    newStep = Math.min(newStep + 1, newSteps.length);
    renderDraft();
  } catch (e) {
    if (e instanceof SignIn) return;
    if (busy) { busy.disabled = false; busy.textContent = "Next"; }
    if ($("step-error")) $("step-error").textContent = e.message;
  }
}

function stepNav() {
  const nav = el("div", { class: "chips new-steps", role: "group", "aria-label": "The steps" });
  newSteps.forEach(([key, label], i) => {
    const done = draft.done && draft.done[key];
    nav.append(el("button", { "aria-pressed": String(i === newStep), class: done ? "done" : "", onclick: () => { newStep = i; renderDraft(); } }, (done ? "✓ " : (i + 1) + " ") + label));
  });
  const all = newSteps.every(([key]) => draft.done && draft.done[key]);
  nav.append(el("button", { "aria-pressed": String(newStep === newSteps.length), disabled: !all, onclick: () => { newStep = newSteps.length; renderDraft(); } }, "Create"));
  return nav;
}

const field = (label, input, help) => el("label", { class: "field" }, el("span", { class: "muted small" }, label), input, help ? el("span", { class: "muted small" }, help) : null);
function input(attrs) { return el("input", Object.assign({ autocomplete: "off", maxlength: "300" }, attrs)); }
function radios(name, options, value, onchange) {
  const box = el("div", { class: "radios" });
  for (const [v, label, disabled] of options) {
    const r = el("input", { type: "radio", name, value: v, disabled: !!disabled });
    r.checked = v === value;
    r.addEventListener("change", () => onchange && onchange(v));
    box.append(el("label", { class: "check" + (disabled ? " muted" : "") }, r, " " + label));
  }
  return box;
}
const picked = (name) => { const r = document.querySelector('input[name="' + name + '"]:checked'); return r ? r.value : ""; };

function renderDraft() {
  if (!draft) return;
  clearTimeout(draftPoll);
  const d = draft;
  const box = $("newapp");
  const parts = [el("div", { class: "admin-head" }, el("h1", { class: "admin-title" }, "New app " + d.name), el("span", { class: "muted small" }, d.folder))];
  if (d.job) {
    const words = { running: "It's being created now: Jobs has its output.", failed: "Creating it failed: Jobs has why. Fix the step it names, and Create again.", done: "It's made." };
    parts.push(el("p", { class: "note " + (d.job_state === "failed" ? "error" : "") }, words[d.job_state] || ""));
  }
  parts.push(stepNav());
  const body = el("div", { class: "new-step" });
  const error = el("p", { id: "step-error", class: "error" }, (d.errors && Object.values(d.errors)[0]) || "");
  let collect = null; // the step's answers, for Next
  const c = d.config;
  const m = d.machine;
  const key = newStep < newSteps.length ? newSteps[newStep][0] : "review";
  switch (key) {
    case "lead": {
      const lead = input({ id: "s-lead", maxlength: "40", value: (c.members.lead && c.members.lead.name) || "", placeholder: d.name });
      body.append(el("p", {}, "The lead talks with the people in the app's Slack channel. Give it a name of its own, such as a person's, or leave it as " + d.name + "."),
        field("The lead's name", lead, "In Slack it's @" + d.handle + (d.done.lead ? "" : " (after Next, by the name you give)") + "; its command is " + d.slash + "."));
      collect = () => ({ lead_name: lead.value.trim() });
      break;
    }
    case "purpose": {
      const purpose = el("textarea", { id: "s-purpose", rows: "3", maxlength: "400", placeholder: "What is this app for? The lead starts from it." });
      purpose.value = c.purpose || "";
      body.append(el("p", {}, "In one sentence, what is this app for? The lead starts from it."), purpose);
      collect = () => ({ purpose: purpose.value.trim() });
      break;
    }
    case "slack": {
      const bot = input({ id: "s-bot", type: "password", placeholder: d.has_bot ? "Kept; paste again to change it" : "xoxb-…" });
      const app = input({ id: "s-app", type: "password", placeholder: d.has_app ? "Kept; paste again to change it" : "xapp-…" });
      body.append(el("p", {}, "Each app has a Slack app of its own, made from a manifest hi writes for it."),
        el("ol", { class: "how" },
          el("li", {}, el("a", { class: "button-like", href: d.manifest_url, target: "_blank", rel: "noopener noreferrer" }, "Create the Slack app ↗"), " opens Slack with the manifest filled in. Pick the workspace, then ", el("strong", {}, "Create"), "."),
          el("li", {}, "Then ", el("strong", {}, "Install to Workspace"), ", and allow it."),
          el("li", {}, "The bot token is under ", el("strong", {}, "OAuth & Permissions"), " (xoxb-…)."),
          el("li", {}, "The app token is under ", el("strong", {}, "Basic Information → App-Level Tokens"), ": generate one with the connections:write scope (xapp-…).")),
        el("details", { class: "small" }, el("summary", { class: "muted" }, "The manifest, to paste by hand (api.slack.com/apps → Create New App → From a manifest → JSON)"), el("pre", { class: "job-output manifest" }, d.manifest)),
        field("Bot token", bot), field("App token", app),
        d.workspace ? el("p", { class: "muted small" }, "Both work, in the workspace " + d.workspace + ".") : "");
      collect = () => ({ bot_token: bot.value.trim(), app_token: app.value.trim() });
      break;
    }
    case "channel": {
      const main = el("select", { id: "s-channel", "aria-label": "The app's channel" });
      const ops = el("select", { id: "s-ops", "aria-label": "The ops channel" });
      const fill = () => {
        const list = slackLists.channels || [];
        const options = (value, none) => [none ? el("option", { value: "" }, none) : el("option", { value: "" }, "Choose a channel"), ...list.map((ch) => el("option", { value: ch.id, selected: ch.id === value }, (ch.private ? "🔒 " : "#") + ch.name))];
        main.replaceChildren(...options(c.channel, ""));
        ops.replaceChildren(...options(c.ops_channel, "None: everything goes to the main channel"));
        if (c.channel && !list.some((ch) => ch.id === c.channel)) main.append(el("option", { value: c.channel, selected: true }, c.channel));
      };
      fill();
      const refresh = el("button", { type: "button", class: "small", onclick: async () => { await loadSlack("channels"); fill(); note.textContent = slackLists.error || (slackLists.channels || []).length + " channels the app is in."; } }, "Read again");
      const note = el("p", { class: "muted small" }, slackLists.error || (slackLists.channels ? slackLists.channels.length + " channels the app is in." : "…"));
      body.append(el("p", {}, "Invite the Slack app to the app's channel first: in the channel, ", el("code", {}, "/invite @" + d.handle), ". Then read the channels it's in again, and pick it."),
        el("div", { class: "data-actions" }, refresh), note,
        field("The app's channel", main),
        field("An ops channel, for operations and security (optional)", ops, "Incidents, warnings, downtime, advisories, keys, and access, for the people with the know-how. A private one, for owners and Pro Devs. Invite the app there too."));
      if (!slackLists.channels) loadSlack("channels").then(() => { if (key === "channel" && newSteps[newStep][0] === "channel") { fill(); note.textContent = slackLists.error || slackLists.channels.length + " channels the app is in."; } });
      collect = () => ({ channel: main.value, ops_channel: ops.value });
      break;
    }
    case "people": {
      const chosen = new Set(c.owners || []);
      if (!chosen.size && state.me.slack) chosen.add(state.me.slack);
      const search = input({ type: "search", placeholder: "Find someone", "aria-label": "Find someone" });
      const list = el("ul", { class: "list people-pick" });
      const chips = el("div", { class: "chosen" });
      const draw = () => {
        const people = slackLists.people || [];
        const q = search.value.trim().toLowerCase();
        list.replaceChildren();
        for (const p of people.filter((p) => !q || (p.name + " " + (p.real_name || "")).toLowerCase().includes(q)).slice(0, 60)) {
          const box = el("input", { type: "checkbox" });
          box.checked = chosen.has(p.id);
          box.addEventListener("change", () => { if (box.checked) chosen.add(p.id); else chosen.delete(p.id); drawChips(); });
          list.append(el("li", {}, el("label", { class: "check" }, box, " ", el("strong", {}, p.real_name || p.name), p.real_name ? el("span", { class: "muted small" }, " @" + p.name) : null)));
        }
        if (!people.length) list.append(el("li", { class: "plain muted small" }, slackLists.error || "…"));
      };
      const drawChips = () => {
        const people = slackLists.people || [];
        chips.replaceChildren(...[...chosen].map((id) => { const p = people.find((x) => x.id === id); return el("span", { class: "pill" }, p ? p.real_name || p.name : id); }));
        if (!chosen.size) chips.append(el("span", { class: "muted small" }, "Nobody yet."));
      };
      search.addEventListener("input", draw);
      body.append(el("p", {}, "Owners build the app in the dev box and release it from the dev page. Pick them from the workspace's people."), el("div", { class: "chosen-row" }, el("span", { class: "muted small" }, "Owners: "), chips), search, list);
      draw(); drawChips();
      if (!slackLists.people) loadSlack("people").then(() => { draw(); drawChips(); });
      collect = () => ({ owners: [...chosen] });
      break;
    }
    case "members":
      collect = renderMembers(body, d);
      break;
    case "models": {
      let own = d.own_key || !m.openrouter;
      const key = input({ id: "s-orkey", type: "password", placeholder: d.has_own_key ? "Kept; paste again to change it" : "sk-or-…", disabled: !own });
      body.append(el("p", {}, "The lead, and the app itself, use models through OpenRouter" + (Object.values(c.members).some((x) => x.provider === "chatgpt" || x.agent === "codex") ? ", and the roles on ChatGPT fall back on it" : "") + ". The app's own key bills and limits its use apart; no box ever holds it."),
        radios("s-or", [["machine", "This machine's key, Hermes'", !m.openrouter], ["own", "A key of the app's own (openrouter.ai/settings/keys)"]], own ? "own" : "machine", (v) => { own = v === "own"; key.disabled = !own; if (own) key.focus(); }),
        field("The app's OpenRouter key", key));
      collect = () => ({ own_key: own, openrouter_key: key.value.trim() });
      break;
    }
    case "code":
      collect = renderCode(body, d);
      break;
    case "network": {
      const nb = input({ id: "s-netbird", type: "password", placeholder: d.has_netbird ? "Kept; paste again to change it" : "A NetBird setup key (optional)" });
      const current = c.addresses || "";
      const addresses = current === "" ? (m.domain ? "domain" : "netbird") : current === "netbird" || current === "names" ? current : "host";
      let choice = addresses;
      const production = input({ value: c.names ? c.names.production || "" : "", placeholder: "demo.example.com" });
      const https = el("input", { type: "checkbox" });
      https.checked = c.names ? !!c.names.https : true;
      const host = input({ value: choice === "host" ? current : "", placeholder: "demo.corp.example" });
      const extra = el("div", { class: "stack" });
      const drawExtra = () => {
        extra.replaceChildren();
        if (choice === "names") extra.append(field("Production's name; Stage and the dev page go under it", production), el("label", { class: "check small" }, https, " Your IT team's proxy ends TLS in front of them: the links say https"));
        if (choice === "host") extra.append(field("The name your IT team keeps, pointing at the app's NetBird address", host, "Production is http://<name>/, the preview :8080, and the dev page :8888."));
      };
      const options = [];
      if (m.domain) options.push(["domain", "This machine's domain, on https: " + m.domain + " and the like"]);
      options.push(["names", "A name for each, which your IT team keeps"], ["host", "One name your IT team keeps, with ports"], ["netbird", "The NetBird name, " + m.peer + ", on plain http"]);
      body.append(el("p", {}, "The app's addresses are on your NetBird network, and the owners build in its dev box over SSH there. The dev box joins with a setup key, once: in NetBird's dashboard, Setup Keys. Without one now: hi app dev " + d.name + " netbird, later."),
        field("NetBird setup key", nb), el("h2", {}, "What the addresses are named"), radios("s-addr", options, choice, (v) => { choice = v; drawExtra(); }), extra);
      drawExtra();
      collect = () => ({ netbird_key: nb.value.trim(), addresses: choice, production: production.value.trim(), https: https.checked, host: host.value.trim() });
      break;
    }
    case "heartbeats": {
      const beat = input({ value: d.heartbeat || "", placeholder: "https://hc-ping.com/… (optional)" });
      const every = input({ value: c.heartbeat_every || "", placeholder: "1m" });
      const machine = input({ value: d.machine_heartbeat || "", placeholder: "https://hc-ping.com/… (optional)" });
      body.append(el("p", {}, "Heartbeats tell an outside service, such as healthchecks.io (free for 20 checks), that the app runs; it alerts when they stop. Make a check there, and set there who hears of a stop: your IT team, or the ops channel."),
        field("The app's heartbeat URL", beat, "One is sent now, to see that it arrives."), field("Every", every, "Between 30s and 1h; 1m unless you say."));
      if (!m.heartbeat) body.append(field("This machine's heartbeat URL", machine, "This machine has none: one says it's up, whichever of its apps run."));
      collect = () => ({ heartbeat: beat.value.trim(), every: every.value.trim(), machine_heartbeat: machine.value.trim() });
      break;
    }
    case "review":
      renderReview(body, d);
      break;
  }
  parts.push(body, error);
  if (key !== "review") {
    const buttons = el("div", { class: "data-actions" });
    if (newStep > 0) buttons.append(el("button", { type: "button", onclick: () => { newStep--; renderDraft(); } }, "Back"));
    buttons.append(el("button", { id: "step-next", class: "primary", type: "button", onclick: () => collect && saveStep(collect()) }, "Next"));
    buttons.append(el("span", { class: "grow" }), el("button", { type: "button", onclick: () => openNew("") }, "Later"));
    parts.push(buttons);
  }
  box.replaceChildren(...parts);
  const first = body.querySelector("input:not([type=radio]):not([type=checkbox]):not(:disabled), textarea, select");
  if (first && !narrow()) first.focus();
  // A ChatGPT code waits for someone to sign in: read again until it's done.
  if (d.chatgpt_code || d.job_state === "running") draftPoll = setTimeout(() => loadDraft(d.name, false), 3000);
}

async function loadSlack(what) {
  try {
    const list = await api("/api/new/slack?name=" + encodeURIComponent(draft.name) + "&what=" + what);
    slackLists[what] = list || [];
    slackLists.error = "";
  } catch (e) {
    if (e instanceof SignIn) return;
    slackLists[what] = [];
    slackLists.error = e.message;
  }
}

// Members: each role's agent, model, who pays, and effort, and the
// sign-ins they need.
function renderMembers(body, d) {
  const c = d.config, m = d.machine;
  const roles = ["lead", ...Object.keys(c.members).filter((r) => r !== "lead").sort()];
  const rows = {};
  body.append(el("p", {}, "Each role's agent and model, and for Hermes who pays: OpenRouter, or a ChatGPT plan (falling back to OpenRouter), and how hard it thinks."));
  const signin = el("div", { class: "card-box pad stack" });
  const claude = el("div", { class: "card-box pad stack" });
  let chatgptChoice = d.chatgpt_signin || (m.chatgpt ? "machine" : "app");
  let claudeMachine = !d.has_claude && m.claude;
  const claudeToken = input({ type: "password", placeholder: d.has_claude ? "Kept; paste again to change it" : "sk-ant-oat…, from claude setup-token" });
  const needs = () => {
    let chatgpt = false, cl = false;
    for (const role of roles) {
      const r = rows[role];
      if (r.agent.value === "hermes" && r.provider.value === "chatgpt") chatgpt = true;
      if (r.agent.value === "codex" && !(m.codex_key && chatgptChoice !== "app")) chatgpt = true;
      if (r.agent.value === "claude") cl = true;
    }
    return { chatgpt, claude: cl };
  };
  const drawSignins = () => {
    const n = needs();
    signin.hidden = !n.chatgpt;
    claude.hidden = !n.claude;
    signin.replaceChildren(el("strong", {}, "ChatGPT sign-in"), el("p", { class: "muted small" }, "The roles on ChatGPT sign in once, for all of them: the app's own, with a code on any device (only the app refreshes it), or this machine's, Codex's."),
      radios("s-chatgpt", [["app", "The app's own"], ["machine", "This machine's, Codex's" + (m.chatgpt ? "" : ": not signed in"), !m.chatgpt]], chatgptChoice, (v) => { chatgptChoice = v; drawSignins(); }));
    if (chatgptChoice === "app") {
      if (d.chatgpt) signin.append(el("p", { class: "small" }, "Signed in: " + d.chatgpt + "."));
      if (d.chatgpt_code) signin.append(el("p", {}, "Open ", el("a", { href: d.chatgpt_url, target: "_blank", rel: "noopener noreferrer" }, d.chatgpt_url + " ↗"), ", sign in to the ChatGPT account that pays, and type ", el("code", { class: "big-code" }, d.chatgpt_code), ". This page sees when it's done."));
      if (d.chatgpt_error) signin.append(el("p", { class: "error small" }, d.chatgpt_error));
      signin.append(el("button", { type: "button", class: "small", onclick: async () => {
        try { draft = await api("/api/new", { action: "chatgpt", name: d.name }); renderDraft(); } catch (e) { if (!(e instanceof SignIn)) toast(e.message, true); }
      } }, d.chatgpt ? "Sign in again" : d.chatgpt_code ? "A new code" : "Sign in"));
    }
    claude.replaceChildren(el("strong", {}, "Claude"), el("p", { class: "muted small" }, "A token of the app's own, from claude setup-token signed in to the account that pays (it lasts a year), or this machine's sign-in."),
      radios("s-claude", [["machine", "This machine's sign-in" + (m.claude ? "" : ": none here"), !m.claude], ["own", "The app's own token"]], claudeMachine ? "machine" : "own", (v) => { claudeMachine = v === "machine"; claudeToken.disabled = claudeMachine; if (!claudeMachine) claudeToken.focus(); }),
      claudeToken);
    claudeToken.disabled = claudeMachine;
  };
  for (const role of roles) {
    const member = c.members[role];
    const agent = el("select", { "aria-label": role + "'s agent", disabled: role === "lead" });
    for (const [v, label] of [["hermes", "Hermes"], ["claude", "Claude Code"], ["codex", "Codex"]]) agent.append(el("option", { value: v, selected: member.agent === v }, label));
    const model = input({ value: member.model || "", placeholder: "the agent's own" , "aria-label": role + "'s model" });
    const provider = el("select", { "aria-label": role + ": who pays" });
    provider.append(el("option", { value: "openrouter", selected: member.provider !== "chatgpt" }, "OpenRouter, per call"), el("option", { value: "chatgpt", selected: member.provider === "chatgpt" }, "A ChatGPT plan, falling back to OpenRouter"));
    const effort = el("select", { "aria-label": role + "'s effort" });
    for (const e of m.efforts) effort.append(el("option", { value: e, selected: (member.effort || "medium") === e }, e));
    const fallback = input({ value: member.fallback || "", placeholder: "the usual one", "aria-label": role + "'s fallback on OpenRouter" });
    const models = el("datalist", { id: "models-" + role });
    for (const name of d.chatgpt_models || []) models.append(el("option", { value: name }));
    model.setAttribute("list", "models-" + role);
    const providerField = field("Who pays", provider);
    const sync = () => { providerField.hidden = agent.value !== "hermes"; drawSignins(); };
    agent.addEventListener("change", () => { model.value = ""; sync(); });
    provider.addEventListener("change", sync);
    rows[role] = { agent, model, provider, effort, fallback };
    body.append(el("div", { class: "card-box member-card" }, el("div", { class: "card-head pad" }, el("strong", { class: "card-name" }, role)),
      el("div", { class: "policy-fields" }, field("Agent", agent), field("Model", model), providerField, field("Effort", effort), field("Fallback on OpenRouter", fallback, "Only if OpenRouter lacks the usual one")), models));
    providerField.hidden = agent.value !== "hermes";
  }
  body.append(signin, claude);
  drawSignins();
  return () => {
    const members = {};
    for (const role of roles) {
      const r = rows[role];
      members[role] = { agent: r.agent.value, model: r.model.value.trim(), provider: r.agent.value === "hermes" ? r.provider.value : "", effort: r.effort.value, fallback: r.fallback.value.trim() };
    }
    return { members, chatgpt_signin: chatgptChoice, claude_token: claudeMachine ? "" : claudeToken.value.trim(), claude_machine: claudeMachine };
  };
}

// Code: what the project starts from.
function renderCode(body, d) {
  const code = Object.assign({}, d.code);
  const m = d.machine;
  const options = [["new", "A new web app: a blank page on Bun, with a database, tests, and the helper's chat"], ["empty", "A repository with only a README"]];
  for (const t of m.templates) options.push(["template:" + t.name, t.name + (t.summary ? ": " + t.summary : "")]);
  options.push(["adopt", "Someone's project, such as github.com/owner/name, or a folder here: adopted"]);
  let kind = code.kind === "template" ? "template:" + code.template : code.kind;
  const source = input({ value: code.source || "", placeholder: "github.com/owner/name, a URL, or a folder's path" });
  const ref = input({ value: code.ref || "", placeholder: "its default branch" });
  const shallow = el("input", { type: "checkbox" }); shallow.checked = !!code.shallow;
  const push = el("input", { type: "checkbox", id: "s-push" }); push.checked = !!code.push;
  push.addEventListener("change", () => drawForge());
  const nosurvey = el("input", { type: "checkbox" }); nosurvey.checked = !!code.no_survey;
  // Where the project is kept too: nowhere else, GitHub, or GitLab.
  let forge = code.gitlab ? "gitlab" : code.github ? "github" : "";
  const repoName = input({ value: code.gitlab || code.github || "", "aria-label": "The repository" });
  const forgeBox = el("div", { class: "stack" });
  const drawForge = () => {
    const push = kind === "adopt" && d.look && d.look.can_push && document.querySelector("#s-push") && document.querySelector("#s-push").checked;
    forgeBox.replaceChildren(el("h2", {}, "Kept on GitHub or GitLab too"),
      radios("s-forge", [["", "No: it stays on this machine"], ["github", "GitHub: a new private repository, with this machine's GitHub sign-in (gh)" + (m.gh ? "" : "; gh isn't here: hi install gh"), !m.gh], ["gitlab", "GitLab, gitlab.com or your own: a new private project, with this machine's GitLab sign-in (glab)" + (m.glab ? "" : "; glab isn't here: hi install glab, then glab auth login"), !m.glab]], forge, (v) => { forge = v; drawForge(); }));
    if (forge === "github") { repoName.placeholder = "owner/name, such as octocat/world-clock"; forgeBox.append(field("The repository", repoName, "It's made if it isn't there; an empty one that's there is used. The app pushes every merge there.")); }
    if (forge === "gitlab") { repoName.placeholder = "group/name on gitlab.com, or host/group/name, such as gitlab.example.com/team/billing"; forgeBox.append(field("The project", repoName, "It's made if it isn't there; an empty one that's there is used. The app pushes every merge there.")); }
    forgeBox.hidden = !!push;
  };
  const adoptBox = el("div", { class: "stack" });
  const look = el("div", {});
  const drawLook = () => {
    const l = d.look;
    look.replaceChildren();
    if (!l || l.source !== source.value.trim()) return;
    if (l.error) { look.append(el("p", { class: "error small" }, l.error)); return; }
    const facts = el("dl", { class: "facts" });
    const fact = (t, v) => { if (v) facts.append(el("dt", {}, t), el("dd", {}, v)); };
    fact("Project", l.folder || l.github || l.source);
    fact("About", l.description);
    fact("Size", l.size_mb ? l.size_mb + " MB" + (l.files ? ", " + l.files + " files" : "") : l.files ? l.files + " files" : "");
    fact("Branch", l.branch);
    fact("Licence", l.licence || (l.github ? "none said" : ""));
    look.append(el("div", { class: "card-box pad" }, facts,
      l.archived ? el("p", { class: "warn small" }, "It's archived: nobody works on it now.") : null,
      l.private ? el("p", { class: "muted small" }, "It's private: fetched with this machine's GitHub sign-in.") : null,
      l.folder ? el("p", { class: "muted small" }, "A folder here is copied without its history or its secrets; nothing in it changes.") : null));
  };
  const drawAdopt = () => {
    adoptBox.hidden = kind !== "adopt";
    const l = d.look && d.look.source === source.value.trim() ? d.look : null;
    adoptBox.replaceChildren(field("The project", source), el("div", { class: "data-actions" }, el("button", { type: "button", class: "small", onclick: lookNow }, "Look at it")), look);
    if (l && !l.folder && !l.error) {
      adoptBox.append(field("Branch, tag, or commit (optional)", ref));
      if (l.big) adoptBox.append(el("label", { class: "check small" }, shallow, " Only its latest commit: it's over 1 GB. Without it, its whole history, so the members see why code is the way it is."));
      else adoptBox.append(el("label", { class: "check small" }, shallow, " Only its latest commit (smaller and quicker; the copy stays on this machine)"));
      if (l.can_push) adoptBox.append(el("label", { class: "check small" }, push, " It's yours: this machine's GitHub sign-in can push there. The app pushes its merges there, and the branch production and tags release-1, release-2, …"));
    }
    if (l && !l.error) adoptBox.append(el("label", { class: "check small" }, nosurvey, " Skip the first task, the survey that makes it run and maps it"));
    drawLook();
  };
  const lookNow = async () => {
    try { draft = await api("/api/new", { action: "look", name: d.name, source: source.value.trim() }); d.look = draft.look; drawAdopt(); } catch (e) { if (!(e instanceof SignIn)) $("step-error").textContent = e.message; }
  };
  source.addEventListener("keydown", (event) => { if (event.key === "Enter") { event.preventDefault(); lookNow(); } });
  body.append(el("p", {}, "The app keeps its own copy of the project in " + d.folder + "/repo."), radios("s-code", options, kind, (v) => { kind = v; drawAdopt(); drawForge(); }), adoptBox, forgeBox);
  drawAdopt();
  drawForge();
  return () => {
    const answer = { kind: kind.startsWith("template:") ? "template" : kind, template: kind.startsWith("template:") ? kind.slice(9) : "", source: source.value.trim(), ref: ref.value.trim(), shallow: shallow.checked, push: push.checked, no_survey: nosurvey.checked, github: forge === "github" ? repoName.value.trim() : "", gitlab: forge === "gitlab" ? repoName.value.trim() : "" };
    return { code: answer };
  };
}

function renderReview(body, d) {
  const c = d.config;
  const facts = el("dl", { class: "facts" });
  const fact = (t, v) => facts.append(el("dt", {}, t), el("dd", {}, v || "—"));
  fact("Name", d.name + " · @" + d.handle + " · " + d.slash);
  fact("Folder", d.folder);
  fact("Purpose", c.purpose);
  fact("Slack", d.workspace ? "the workspace " + d.workspace : "");
  fact("Channel", c.channel + (c.ops_channel ? ", ops " + c.ops_channel : ""));
  fact("Owners", (c.owners || []).join(", "));
  for (const role of Object.keys(c.members).sort((a, b) => (a === "lead" ? -1 : b === "lead" ? 1 : a.localeCompare(b)))) {
    const x = c.members[role];
    fact(role, x.agent + (x.model ? " · " + x.model : "") + (x.provider === "chatgpt" ? " · on ChatGPT" : "") + (x.effort ? " · " + x.effort : "") + (x.fallback ? " · falls back to " + x.fallback : ""));
  }
  fact("OpenRouter", d.own_key ? "the app's own key" : "this machine's key");
  fact("Code", d.code.kind === "adopt" ? "adopts " + d.code.source + (d.code.push ? ", and pushes there" : "") : d.code.kind === "template" ? "the template " + d.code.template : d.code.kind === "empty" ? "an empty repository" : "a new web app");
  if (d.code.github) fact("GitHub", d.code.github);
  if (d.code.gitlab) fact("GitLab", d.code.gitlab);
  fact("Network", (d.has_netbird ? "joins NetBird now" : "joins NetBird later") + "; addresses " + (c.addresses || "on this machine's domain"));
  fact("Heartbeat", d.heartbeat || "none");
  body.append(el("p", {}, "Create makes it as hi app new would with these answers, then starts it. Jobs follows it, and the app opens here when it's up."), facts);
  const create = el("button", { class: "primary", disabled: d.job_state === "running", onclick: async () => {
    create.disabled = true;
    try {
      const job = await api("/api/new", { action: "create", name: d.name });
      toast("Creating " + d.name + ". Jobs has its output; it opens here when it's up.", true);
      creating = { job: job.id, name: d.name };
      followJob(job);
      loadDraft(d.name, false);
    } catch (e) { create.disabled = false; if (!(e instanceof SignIn)) $("step-error").textContent = e.message; }
  } }, "Create " + d.name);
  body.append(el("div", { class: "data-actions" }, el("button", { onclick: () => { newStep = newSteps.length - 1; renderDraft(); } }, "Back"), create));
}
let creating = null;

// Rename and Remove, from an app's Overview.
async function renameApp(app) {
  const answer = await ask({ title: "Rename " + app + "?", text: "Its Slack bot, its command, its service, and its folder (when it's named after the app) take the new name; its memory, tasks, and snapshots stay. It stops while it's done and starts again. Afterwards, update its Slack app's manifest (Slack shows the new name only then). Type the app's name to say so.", ok: "Rename", field: "The new name", fieldCheck: (v) => (/^[a-z][a-z0-9-]{0,19}$/.test(v) && v !== app ? "" : "A new name: lowercase letters, digits, and dashes, up to 20."), typed: app });
  if (!answer) return;
  try { const job = await api("/api/apps", { action: "rename", app, to: answer.field }); toast("Renaming " + app + " to " + answer.field + ". Jobs has its output."); creating = { job: job.id, name: answer.field }; followJob(job); } catch (e) { if (!(e instanceof SignIn)) toast(e.message, true); }
}
async function removeApp(app) {
  const answer = await ask({ title: "Remove " + app + "?", text: "It stops, and its service, its name here, and its members' boxes are deleted. Its snapshots stay. Its Slack app and channel, and a GitHub repository if it has one, stay too. Type the app's name to say so.", ok: "Remove", typed: app, check: "Keep its folder, so it can come back (Bring one back)", checked: true });
  if (!answer) return;
  try { const job = await api("/api/apps", { action: "remove", app, keep_folder: answer.checked }); toast("Removing " + app + ". Jobs has its output."); followJob(job); openView(""); } catch (e) { if (!(e instanceof SignIn)) toast(e.message, true); }
}

// ---------------------------------------------------------------------------
// reading again

async function refreshState() {
  try {
    const fresh = await api("/api/state");
    if (restarting) toast("The admin page is back.");
    restarting = false;
    if (waitFor && fresh.version !== waitFor) { toast("The admin page runs hi " + fresh.version + " now.", true); waitFor = ""; }
    state = fresh;
  } catch (e) {
    if (e instanceof SignIn) return false;
    restarting = true;
    if (state) renderBar();
    return false;
  }
  if (!booted) return true;
  renderTabs();
  renderBar();
  renderApps();
  if (!middle) renderHome();
  renderOrgBadges();
  renderJobsBadge();
  if (openApp && $("overview-usage")) $("overview-usage").textContent = usageLine(openApp) || "…";
  if (viewShown("machine")) renderMachine();
  else badge("machine-badge", state.latest ? 1 : 0);
  return true;
}

function renderOrgBadges() {
  const o = state.org;
  badge("requests-count", o ? o.waiting : 0);
  badge("running-count", o ? o.running : 0);
  const bad = !!(o && (!o.up || o.no_owner));
  $("org-badge").hidden = !bad;
  $("org-badge").classList.toggle("failed", bad);
}

let pollTimer = 0;
let settingsAt = 0;
async function poll() {
  clearTimeout(pollTimer);
  const ok = await refreshState();
  if (ok) {
    if (openApp) refreshDetail();
    for (const view of [panes.right.view]) if (viewShown(view) && view !== "logs" && view !== "spend") freshView(view);
    // The org's settings: every fifteen seconds while one is open.
    if ((["org", "people", "keys"].some(viewShown) || middle === ":policy") && Date.now() - settingsAt > 15000) { settingsAt = Date.now(); refreshOrgSettings(); }
    // Logs: every three seconds in view, every fifteen behind another,
    // for the dot.
    if (viewShown("logs") || Date.now() - logs.at > 15000) refreshLogs(false);
  }
  pollTimer = setTimeout(poll, restarting ? 1500 : document.hidden ? 15000 : 3000);
}
document.addEventListener("visibilitychange", () => { if (!document.hidden) poll(); });

async function boot() {
  try {
    state = await api("/api/state");
  } catch (e) {
    if (e instanceof SignIn) return;
    document.body.replaceChildren(el("p", { class: "locked" }, "The admin page didn't answer: " + e.message));
    return;
  }
  $("signin").hidden = true;
  $("work").hidden = false;
  booted = true;
  renderTabs();
  renderPanes();
  const wanted = decodeURIComponent(location.hash.slice(1));
  renderBar();
  renderApps();
  renderHome();
  renderOrgBadges();
  if (wanted && state.apps.some((a) => a.name === wanted)) await openView(wanted);
  else if (wanted === ":policy" && state.org) openPolicy();
  else if (wanted === ":new" || wanted.startsWith(":new/")) openNew(wanted.slice(5));
  for (const which of ["left", "right"]) if (panes[which].open) freshView(panes[which].view);
  refreshJobs();
  pollTimer = setTimeout(poll, 3000);
}

boot();
