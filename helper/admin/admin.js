"use strict";
// The admin page (hi server's, docs/specs/approved/hi_admin.md and hi_server_apps.md): the
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
let middle = "";       // what the middle shows: "", an app's name, ":org", or ":new…"
let orgSection = "overview"; // the server's section, with ":org" open
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
const paneViews = { left: ["apps", "people", "keys", "network", "machine", "activity"], right: ["requests", "running", "spend", "jobs", "logs", "incidents", "advisories"] };
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
  placeOpenBox();
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
  if (view === "machine") { renderMachine(true); refreshMachines(); }
  if (view === "requests" || view === "running") refreshOrg();
  if (view === "spend") refreshSpend();
  if (view === "people") { renderPeople(); refreshPeopleApps(); }
  if (view === "network") { renderMachineNetwork(); refreshMachineNetwork(); }
  if (view === "advisories") { renderAdvisories(); refreshAdvisories(); }
  if (view === "keys") { renderKeys(); refreshVault(); refreshMachineKeys(); refreshMail(); }
}

// The left panel is the same on every machine. The right is about what's
// open: the server's views with the server, an app's with an app; on Home,
// every app's, or the server's on a machine that runs only the server.
const orgOpen = () => middle === ":org";
function rightViews() {
  const orgViews = ["requests", "running", "spend", "logs", "incidents"];
  if (state && state.org && orgOpen()) return orgViews;
  if (state && state.org && !openApp && !state.apps.length) return ["requests", "running", "spend", "jobs", "logs", "incidents"];
  return ["jobs", "logs", "incidents", "advisories"];
}
function hasView(view) {
  if (!state || paneOf(view) === "left") return true;
  return rightViews().includes(view);
}
// renderTabs shows the views there are; a panel on one that went shows
// Logs, which every place has. It says whether the right one moved.
function renderTabs() {
  for (const button of document.querySelectorAll(".side-tabs > button[data-side]")) button.hidden = !hasView(button.dataset.side);
  let moved = false;
  for (const which of ["left", "right"]) {
    if (!hasView(panes[which].view)) { panes[which].view = hasView("logs") ? "logs" : paneViews[which].find(hasView); moved = moved || which === "right"; renderPanes(); }
  }
  return moved;
}

// ---------------------------------------------------------------------------
// signing in

function showSignin() {
  if (window.hiPresence) hiPresence.stop();
  $("work").hidden = true;
  $("signin").hidden = false;
  const params = new URLSearchParams(location.search);
  if (params.has("ended")) {
    $("signin-ended").hidden = false;
    history.replaceState(null, "", "/");
  }
  document.title = "Admin page: sign in";
}

// Signing in by email (hi_people.md): an admin whose email the machine
// knows gets a link and a code.
let emailRequest = "";
let emailVia = "email";
$("email-form").addEventListener("submit", async (event) => {
  event.preventDefault();
  const address = $("email-address").value.trim();
  if (!/^[^@\s]+@[^@\s]+\.[^@\s]+$/.test(address)) { $("email-said").textContent = "Type your email address."; $("email-address").focus(); return; }
  const via = event.submitter && event.submitter.value === "slack" ? "slack" : "email";
  const buttons = $("email-form").querySelectorAll("button");
  buttons.forEach((b) => { b.disabled = true; });
  try {
    const answer = await api("/api/email", { email: address, via });
    emailRequest = answer.request;
    emailVia = via;
    $("email-code-label").textContent = via === "slack" ? "The six digits in the Slack DM" : "The six digits in the email";
    $("email-said").textContent = answer.text;
    $("code-form").hidden = false;
    $("email-code").focus();
  } catch (e) { $("email-said").textContent = e.message; }
  buttons.forEach((b) => { b.disabled = false; });
});
$("code-form").addEventListener("submit", async (event) => {
  event.preventDefault();
  const code = $("email-code").value.replace(/\s/g, "");
  if (!/^[0-9]{6}$/.test(code)) { $("email-said").textContent = "Type the six digits from the " + (emailVia === "slack" ? "Slack DM." : "email."); $("email-code").focus(); return; }
  try {
    await api("/api/email/code", { request: emailRequest, code });
    location.href = "/";
  } catch (e) { $("email-said").textContent = e.message; }
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

// An app's sign-ins (Claude, ChatGPT, agy): whether each works, as its
// service last checked (admin_signin_health.go).
const signinWords = { ok: "works", low: "nearly used up", limited: "limit reached", "signed-out": "signed out", expiring: "about to end", unknown: "not checked yet" };
function signinClass(s) {
  if (s === "ok") return "idle";
  if (s === "low" || s === "expiring") return "blocked";
  if (s === "limited" || s === "signed-out") return "bad";
  return "";
}
function signinSource(source) {
  if (source === "own") return "its own";
  if (source === "machine") return "the machine's";
  return source || "";
}
function signinWhen(t) {
  if (!t) return "";
  const d = new Date(t);
  return d.toLocaleString([], { weekday: "short", day: "numeric", month: "short", hour: "2-digit", minute: "2-digit" });
}
// signinLine is one sign-in in words: what it is, how it is, and why.
function signinLine(g) {
  const bits = [signinWords[g.state] || g.state];
  if (g.state !== "signed-out" && g.weekly_left !== undefined && g.weekly_left !== null) bits.push(g.weekly_left + "% of the weekly limit left");
  if (g.resets && (g.state === "limited" || g.state === "low")) bits.push((g.state === "limited" ? "until " : "resets ") + signinWhen(g.resets));
  return bits.join(", ");
}
function signinTitle(app) {
  return (app.signins || []).filter((g) => g.state !== "ok" && g.state !== "unknown").map((g) => g.label + ": " + (g.why || signinWords[g.state])).join(" · ");
}

// movedOpen says the open app moved to another machine: it has no
// sections, menu, or logs here.
function movedOpen() {
  return !!(openApp && state && (state.moved || []).some((m) => m.app === openApp) && !state.apps.some((a) => a.name === openApp));
}

function renderBar() {
  $("machine").textContent = state.machine;
  $("home-machine").textContent = state.machine;
  const person = middle.startsWith(":person/") ? (personOpen === "new" ? "Add someone" : ((peopleApps && peopleApps.people.find((x) => x.key === personOpen)) || {}).name || personOpen) : "";
  const named = openApp || (orgOpen() ? "The server" : middle === ":new" ? "New app" : middle.startsWith(":new/") ? "New app " + middle.slice(5) : person ? "People ▸ " + person : "");
  $("crumb").hidden = !named;
  $("sections").hidden = !openApp || movedOpen();
  $("org-sections").hidden = !orgOpen();
  placeOpenBox();
  $("crumb-app").textContent = named;
  document.title = (named ? named + " · " : "") + "Admin page · " + state.machine;
  $("bar-note").textContent = restarting ? "The admin page is restarting…" : state.running ? (state.running === 1 ? "A job runs" : state.running + " jobs run") : "";
}

function renderApps() {
  const query = $("apps-search").value.trim().toLowerCase();
  const list = $("apps-list");
  list.replaceChildren();
  const apps = state.apps.filter((a) => !query || a.name.includes(query) || (a.purpose || "").toLowerCase().includes(query));
  // The server first, on a machine that runs it: opened as an app is.
  const o = state.org;
  if (o && (!query || "the server".includes(query))) {
    const li = el("li", { class: orgOpen() ? "current" : "" }, el("span", { class: "dot " + (o.up && !o.no_owner ? "idle" : "bad"), "aria-hidden": "true" }),
      el("button", { class: "stask-row", onclick: () => openOrg(""), title: "Open the server: requests, users, admins, budgets and keys" },
        el("span", { class: "stask-title" }, "The server"), el("span", { class: "stask-num" }, o.up ? (o.running ? o.running + " running" : "") : "does not answer")));
    if (o.waiting) li.append(el("span", { class: "count", title: o.waiting + " request" + (o.waiting === 1 ? "" : "s") + " wait" + (o.waiting === 1 ? "s" : "") }, o.waiting));
    list.append(li);
  }
  if (!state.apps.length) list.append(el("li", { class: "empty" }, (state.moved || []).length ? "No apps run on this machine now." : o ? "No apps on this machine." : "No apps on this machine yet."));
  else if (!apps.length) list.append(el("li", { class: "empty" }, "No app matches."));
  for (const app of apps) {
    const row = el("button", { class: "stask-row", onclick: () => openView(app.name), title: app.purpose || app.name },
      el("span", { class: "stask-title" }, app.name),
      el("span", { class: "stask-num" }, app.state === "up" ? (app.production ? "v" + app.production : "") : stateWords[app.state] || app.state),
      app.purpose ? el("span", { class: "stask-sub" }, app.purpose) : null);
    const li = el("li", { class: app.name === openApp ? "current" : "" }, stateDot(app.state), row);
    if (app.signin_worst) {
      li.append(el("span", { class: "count signin-mark " + signinClass(app.signin_worst), title: "Sign-ins: " + signinTitle(app) }, "!"));
    }
    const unseen = unseenIncidents(app.name, app.incidents_opened);
    if (unseen) li.append(el("span", { class: "count", "data-app": app.name, title: unseen + " open incident" + (unseen === 1 ? "" : "s") + " that you did not see yet" }, unseen));
    li.append(el("button", { class: "row-open", "data-open": app.name, "aria-haspopup": "menu", "aria-expanded": "false", title: "Open production, Stage or the dev page of " + app.name,
      onclick: (event) => { event.stopPropagation(); toggleMenu(app.name, event.currentTarget); } }, "Open ▾"));
    if (app.name === openApp) li.dataset.app = app.name;
    list.append(li);
  }
  for (const m of (state.moved || []).filter((x) => !query || x.app.includes(query))) {
    const row = el("button", { class: "stask-row", onclick: () => openView(m.app), title: m.app + " moved to " + m.to + ", " + ago(m.at) },
      el("span", { class: "stask-title muted" }, m.app), el("span", { class: "stask-num" }, "→ " + m.to));
    const li = el("li", { class: m.app === openApp ? "current" : "" }, el("span", { class: "dot", "aria-hidden": "true" }), row);
    list.append(li);
  }
  placeOpenBox();
  // Home's number: apps that aren't up, and the server's requests waiting.
  badge("apps-count", state.apps.filter((a) => a.state !== "up").length + (o ? o.waiting + (o.up ? 0 : 1) : 0));
  const orgIncidents = o ? unseenIncidents(":org", o.incidents_opened) : 0;
  badge("incidents-count", orgOpen() ? orgIncidents : state.apps.reduce((n, a) => n + (!openApp || a.name === openApp ? unseenIncidents(a.name, a.incidents_opened) : 0), openApp ? 0 : orgIncidents));
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
    el("span", { class: "muted small", title: "Load: the average number of tasks that wait for a core, over 1, 5 and 15 minutes" }, u.cores + " cores · load " + u.load.map((l) => l.toFixed(2)).join(" ")));
  const meters = el("div", { class: "meters pad" });
  meters.append(u.cpu === undefined || u.cpu === null ? meter("CPU", 0, "…") : meter("CPU", u.cpu, percent(u.cpu) + " busy", "Use of all " + u.cores + " cores in the last few seconds"));
  if (u.mem_total) meters.append(meter("Memory", u.mem_used / u.mem_total, size(u.mem_used) + " of " + size(u.mem_total), "Memory in use. hi counts the kernel cache as free."));
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
  row.append(meter("CPU", app.cpu || 0, app.cpu === undefined || app.cpu === null ? "…" : percent(app.cpu), "CPU use of the app lead and boxes, of " + u.cores + " cores"));
  row.append(meter("Memory", u.mem_total ? app.memory / u.mem_total : 0, size(app.memory), "Memory of the app lead and " + app.boxes + " running boxes. The machine has " + size(u.mem_total) + "."));
  row.append(el("div", { class: "meter", title: "Size of the app folder. hi measures it every ten minutes." }, el("div", { class: "meter-head" }, el("span", {}, "Disk"), el("span", { class: "muted small" }, app.disk >= 0 ? size(app.disk) : "…"))));
  return row;
}
function usageLine(name) {
  const u = state && state.usage;
  const app = u && u.apps ? u.apps[name] : null;
  if (!app) return "";
  const cpu = app.cpu === undefined || app.cpu === null ? "" : "CPU " + percent(app.cpu) + ", ";
  return cpu + "memory " + size(app.memory) + " (the lead and " + app.boxes + " running box" + (app.boxes === 1 ? "" : "es") + ")" + (app.disk >= 0 ? ", disk " + size(app.disk) + " (the app folder)" : "");
}

function renderHome() {
  renderHomeMachine();
  renderHomeOrg();
  $("home-what").textContent = state.org && !state.apps.length ? "The server" : "Apps";
  $("home-new").hidden = !state.me.machine;
  const note = $("home-note");
  note.hidden = !!state.me.machine;
  note.textContent = state.me.machine ? "" : "You are an admin of the server. You can see this machine and its apps. Only an admin of the machine can change them.";
  const cards = $("home-cards");
  cards.replaceChildren();
  const up = state.apps.filter((a) => a.state === "up").length;
  $("home-summary").textContent = state.apps.length ? `${up} of ${state.apps.length} up` : "";
  for (const m of state.moved || []) {
    cards.append(el("div", { class: "card-box" }, el("button", { class: "app-card moved-card", onclick: () => openView(m.app) },
      el("div", { class: "card-head" }, el("span", { class: "dot", "aria-hidden": "true" }), el("strong", { class: "card-name" }, m.app),
        el("span", { class: "muted small" }, "moved to " + m.to), el("span", { class: "grow" }), el("span", { class: "muted small" }, ago(m.at))))));
  }
  if (!state.apps.length) {
    cards.append(el("div", { class: "app-card empty-card" },
      el("p", {}, state.org ? "No apps on this machine: it runs the server." : "No apps on this machine yet."),
      el("p", { class: "muted" }, "To make an app, click + New app. Or run ", el("code", {}, "hi app new <name>"), " on the machine.")));
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
  $("orgview").hidden = what !== ":org" || orgSection === "policy";
  $("policy").hidden = what !== ":org" || orgSection !== "policy";
  $("newapp").hidden = !what.startsWith(":new");
  $("person").hidden = !what.startsWith(":person/");
  if (!what.startsWith(":person/")) personOpen = "";
  if (!what.startsWith(":new")) clearTimeout(draftPoll);
  history.replaceState(null, "", what ? "#" + encodeURIComponent(what).replace(/^%3A/, ":") + (what === ":org" && orgSection !== "overview" ? "/" + orgSection : "") : "/");
  renderBar();
  renderApps();
  if (!what) renderHome();
}

// moved: the right panel follows what's open now.
function moved() {
  renderTabs();
  if (viewShown("logs")) refreshLogs(true);
  if (viewShown("incidents")) refreshIncidents();
  if (viewShown("requests") || viewShown("running")) refreshOrg();
  if (viewShown("spend")) refreshSpend();
  if (viewShown("advisories")) refreshAdvisories(); else if (advisories) renderAdvisories();
  if (viewShown("jobs")) { if (jobOpen) { jobOpen = ""; $("job-one").hidden = true; $("jobs-list").hidden = false; } refreshJobs(); }
  else renderJobsBadge();
  renderApps();
}

async function openView(name, section) {
  if (name !== openApp || (section && section !== "terminal")) closeTerm();
  if (name !== openApp || section) {
    appSection = section || "overview";
    settings = null;
    for (const chip of document.querySelectorAll("#sections [data-section]")) chip.setAttribute("aria-pressed", String(chip.dataset.section === appSection));
  }
  setMiddle(name);
  if (name) {
    $("overview").replaceChildren(el("p", { class: "muted" }, "…"));
    if (narrow()) { panes.left.open = false; keepPanes(); renderPanes(); }
  }
  moved();
  if (name && appSection !== "overview") showSection(appSection);
  else if (name) await refreshDetail();
}

async function refreshDetail() {
  if (!openApp) return;
  const name = openApp;
  try {
    const fresh = await api("/api/app?name=" + encodeURIComponent(name));
    if (openApp !== name) return;
    const same = detail && JSON.stringify(detail) === JSON.stringify(fresh);
    detail = fresh;
    if (!same && appSection === "overview") renderOverview();
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
  if (d.moved) { box.replaceChildren(...movedView(d.moved)); return; }
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
  parts.push(el("p", { class: "small" }, el("span", { class: "muted" }, "Models this month: "), dollars(d.month_usd), d.budget_monthly ? " of " + dollars(d.budget_monthly) : "", " ",
    el("button", { class: "link-button", "data-nav": "", onclick: () => showSection("spend") }, "Spend")));

  // The rest in cards, as the dev page's Release view has them: where it
  // is and what to do side by side, then what it is, its boxes, and its
  // releases (dev.css's raised cards, admin.css's .admin-cards).
  const cards = el("div", { class: "admin-cards" });

  // Where it is.
  // Every address goes through the app's NetBird peer: until it joins,
  // they're names nobody reaches, so they don't open.
  const where = el("ul", { class: "list where-list" });
  for (const address of d.addresses) {
    where.append(el("li", {}, el("span", { class: "what" }, el("strong", {}, address.what), " ", el("span", { class: "muted small" }, address.waits ? "after the dev box joins NetBird" : shown(address.url))),
      d.joined && address.url ? el("a", { class: "button-like small", href: address.url, target: "_blank", rel: "noopener noreferrer" }, "Open ↗") : ""));
  }
  where.append(el("li", {}, el("span", { class: "what" }, el("strong", {}, "Dev page"), " ", el("span", { class: "muted small" }, d.dev_waits ? "after the dev box joins NetBird" : d.dev_page || "when the app runs")),
    d.joined ? el("button", { class: "small", onclick: () => openDevPage(d.name) }, "Open ↗") : ""));
  cards.append(card("Where it is", !d.joined ? el("p", { class: "note warn" }, "You cannot open " + d.name + " yet. All its addresses go through NetBird, and the app did not join NetBird. Give the app a setup key in ",
    el("button", { class: "link-button", onclick: () => showSection("network") }, "Network"), ".") : null, where));

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
  actions.append(el("button", { onclick: () => moveApp(d.name) }, "Move…"));
  const rarer = el("div", { class: "data-actions danger-row" }, el("button", { onclick: () => renameApp(d.name) }, "Rename…"), el("button", { class: "danger", onclick: () => removeApp(d.name) }, "Remove…"));

  // Tell the lead.
  const say = el("form", { class: "say-form", autocomplete: "off" },
    el("textarea", { id: "say-text", rows: "2", maxlength: "4000", placeholder: "Send a message to the lead of " + d.name + ". The lead answers in the app channel.", "aria-label": "Tell the lead" }),
    el("button", { type: "submit" }, "Tell the lead"));
  say.addEventListener("submit", async (event) => {
    event.preventDefault();
    const text = $("say-text").value.trim();
    if (!text) return;
    if (await act("say", { text })) $("say-text").value = "";
  });
  cards.append(card("What to do", actions, rarer, up ? say : null));
  if ((d.signins || []).length) cards.append(signinsCard(d));

  // Who works on it.
  const facts = el("dl", { class: "facts" });
  const fact = (term, value) => { if (value) facts.append(el("dt", {}, term), el("dd", {}, value)); };
  for (const member of d.members) fact(member.role, member.label);
  fact("Channel", d.channel + (d.ops_channel ? ", ops " + d.ops_channel : ""));
  fact("Owners", (d.owners || []).join(", ") || "none");
  fact("You", d.level === "none" ? "no access level in this app" : d.level);
  fact("Dev", d.dev);
  facts.append(el("dt", {}, "Uses"), el("dd", { id: "overview-usage" }, usageLine(d.name) || "…"));
  const lines = [d.snapshot, d.heartbeat].filter(Boolean);
  cards.append(wide(card("What it is", facts, lines.length ? el("div", { class: "muted small lines" }, lines.map((line) => el("p", {}, line))) : null)));
  cards.append(wide(el("section", { class: "admin-card" }, ...boxesPart(d))));

  if (d.releases.length) {
    const releases = el("ul", { class: "list" });
    for (const release of d.releases) {
      releases.append(el("li", {}, el("span", { class: "what clamp3", title: release.notes || "" }, el("strong", {}, "v" + release.version), " ", release.notes || ""),
        el("span", { class: "muted small" }, ago(release.released))));
    }
    cards.append(wide(card("Releases", releases)));
  }
  parts.push(cards);
  box.replaceChildren(...parts);
  if (typing && $("say-text")) {
    $("say-text").value = typing.text;
    if (typing.focus) $("say-text").focus();
  }
  if (menuApp) renderOpenMenu(menuApp);
}

// signinsCard is the Overview's Sign-ins: each subscription the app's
// agents run on, whether it works, and what to do when it doesn't.
function signinsCard(d) {
  const list = el("ul", { class: "list signin-list" });
  for (const g of d.signins) {
    const about = [signinSource(g.source) + (g.source && g.source.startsWith("shared/") ? "" : " sign-in"), g.plan].filter(Boolean).join(", ");
    const item = el("li", {}, el("span", { class: "what" },
      el("span", { class: "dot " + signinClass(g.state), "aria-hidden": "true" }), " ", el("strong", {}, g.label), " ",
      el("span", { class: "muted small" }, about),
      el("div", { class: "small" + (g.state === "signed-out" || g.state === "limited" ? " error" : g.state === "low" || g.state === "expiring" ? " warn" : "") }, signinLine(g) + (g.why && (g.state === "signed-out" || g.state === "expiring") ? ": " + g.why : "")),
      g.fix ? el("div", { class: "muted small" }, g.fix) : null),
      el("span", { class: "muted small", title: g.seen ? new Date(g.seen).toLocaleString() : "" }, g.seen ? "seen " + ago(g.seen) : ""));
    list.append(item);
  }
  return card("Sign-ins", el("p", { class: "muted small" }, "The subscriptions that the app agents use. hi tells the server channel when a subscription stops, or has less than 10% of its weekly limit left."), list);
}

// card is one of an Overview's cards: its title, then what it holds; wide
// ones take the row.
function card(title, ...children) {
  return el("section", { class: "admin-card" }, el("h2", {}, title), ...children);
}
function wide(node) {
  node.classList.add("wide");
  return node;
}

// An app's boxes: each container it has on the machine, running or not,
// what it's for, and what it uses. CPU and memory come with each poll's
// usage; the rest with the app's detail.
const boxStates = { running: "up", paused: "paused", created: "activating", restarting: "activating" };
function boxesPart(d) {
  const boxes = d.boxes || [];
  if (!boxes.length) return [el("h2", {}, "Boxes"), el("p", { class: "muted" }, "No box of " + d.name + " is on the machine now.")];
  const running = boxes.filter((b) => b.state === "running").length;
  const intro = el("p", { class: "muted small" }, running + " of " + boxes.length + " running. Each box is a separate container on " + state.machine +
    ". It has its own files, processes and limits. Production, stage and dev boxes do not share a container.");
  const table = el("table", { class: "boxes-table" },
    el("thead", {}, el("tr", {}, el("th", {}, "Box"), el("th", {}, "State"), el("th", { class: "num" }, "CPU"), el("th", { class: "num" }, "Memory"), el("th", {}, ""))));
  const body = el("tbody");
  for (const b of boxes) {
    const facts = [b.name, b.image];
    if (b.version) facts.push(b.version + (b.commit ? " (" + b.commit + ")" : ""));
    if (b.command) facts.push((b.port ? "port " + b.port + ": " : "") + b.command);
    if (b.pids) facts.push(b.pids + " process" + (b.pids === 1 ? "" : "es"));
    if (b.memory_peak) facts.push("peak " + size(b.memory_peak));
    if (b.cpus) facts.push("at most " + b.cpus + " core" + (b.cpus === 1 ? "" : "s"));
    if (b.restarts) facts.push(b.restarts + " restart" + (b.restarts === 1 ? "" : "s"));
    let when = "";
    if (b.state === "running" && b.started) when = "for " + uptime((Date.now() - new Date(b.started)) / 1000);
    else if (b.finished) when = ago(b.finished) + (b.exit_code ? ", exit " + b.exit_code : "");
    const terminal = b.term && b.state === "running" && state.me.machine
      ? el("button", { class: "small", title: "Open a shell in the " + b.what, onclick: () => { termWanted = b.term; showSection("terminal"); } }, "Terminal")
      : "";
    body.append(el("tr", { "data-box": b.name },
      el("td", {}, el("strong", {}, b.what), b.does ? el("div", { class: "muted small" }, b.does) : null,
        el("div", { class: "muted small box-facts" }, facts.filter(Boolean).join(" · ")),
        b.oom_kills ? el("div", { class: "warn small" }, b.oom_kills + " process" + (b.oom_kills === 1 ? " was" : "es were") + " killed because memory was low, since the box started") : null),
      el("td", { class: "nowrap" }, stateDot(boxStates[b.state] || "down"), " ", b.state, el("div", { class: "muted small" }, when)),
      el("td", { class: "num nowrap", "data-cpu": "" }, "…"),
      el("td", { class: "num nowrap", "data-memory": "" }, "…"),
      el("td", { class: "num" }, terminal)));
  }
  table.append(body);
  // Filled now as well as with each poll, so a new render isn't blank.
  setTimeout(() => boxUsage(d.name, boxes));
  return [el("h2", {}, "Boxes"), intro, el("div", { class: "boxes-scroll" }, table)];
}
function boxUsage(name, boxes) {
  const u = state && state.usage;
  const each = (u && u.apps && u.apps[name] && u.apps[name].per_box) || {};
  for (const b of boxes) {
    const row = document.querySelector('#overview tr[data-box="' + CSS.escape(b.name) + '"]');
    if (!row) continue;
    const use = each[b.name];
    const cpu = row.querySelector("[data-cpu]"), memory = row.querySelector("[data-memory]");
    if (b.state !== "running" || !use) { cpu.textContent = b.state === "running" ? "…" : ""; memory.textContent = b.state === "running" ? "…" : ""; memory.className = "num nowrap"; continue; }
    cpu.textContent = use.cpu === undefined || use.cpu === null ? "…" : percent(use.cpu);
    cpu.title = "CPU use, of " + u.cores + " cores";
    memory.textContent = size(use.memory) + (b.memory_max ? " of " + size(b.memory_max) : "");
    memory.title = b.memory_max ? "If the box goes past its limit, the kernel kills a process in it" : "This box has no memory limit";
    memory.className = "num nowrap " + (b.memory_max ? level(use.memory / b.memory_max) : "");
  }
}

// The Open ▾ menu: production, Stage, and the dev page. Each app's row in
// the apps list has its own Open button; with the list not shown (another
// view, the panel closed, or a search that leaves the app out), the open
// app's button is in the bar. One menu serves them all. It lives in the
// body, fixed under the button that opened it: inside a panel, the panel
// would clip it, and the other panel and its drag edge would lie over it.
let menuApp = "";          // the app the menu is open for; "" is closed
let menuButton = null;     // the button that opened it
document.body.append($("open-menu"));
function placeOpenBox() {
  const inList = viewShown("apps") && Boolean($("apps-list").querySelector("li[data-app]"));
  $("open-box").hidden = !openApp || movedOpen() || inList;
  if (!menuApp) return;
  // The list was drawn again: the menu follows its app's new button.
  const rowButton = $("apps-list").querySelector('button[data-open="' + CSS.escape(menuApp) + '"]');
  const button = rowButton && viewShown("apps") ? rowButton : menuApp === openApp && !$("open-box").hidden ? $("open-button") : null;
  if (!button) { closeMenu(); return; }
  if (button !== menuButton) { menuButton.setAttribute("aria-expanded", "false"); menuButton = button; button.setAttribute("aria-expanded", "true"); }
  placeOpenMenu();
}
function placeOpenMenu() {
  const menu = $("open-menu"), at = menuButton.getBoundingClientRect();
  menu.style.top = at.bottom + 4 + "px";
  menu.style.left = Math.max(8, Math.min(at.left, window.innerWidth - menu.offsetWidth - 8)) + "px";
}
function renderOpenMenu(app) {
  const menu = $("open-menu");
  const shown = state && state.apps.find((a) => a.name === app);
  const addresses = (shown && shown.addresses) || (detail && detail.name === app ? detail.addresses : []) || [];
  menu.replaceChildren();
  for (const address of addresses) {
    menu.append(address.url
      ? el("button", { role: "menuitem", onclick: () => { closeMenu(); window.open(address.url, "_blank", "noopener,noreferrer"); } }, address.what + " ↗")
      : el("button", { role: "menuitem", disabled: true, title: "After the app joins NetBird" }, address.what + " (waits)"));
  }
  menu.append(el("button", { role: "menuitem", onclick: () => { closeMenu(); openDevPage(app); } }, "Dev page ↗"));
}
function toggleMenu(app, button) {
  const again = menuApp === app && menuButton === button;
  closeMenu();
  if (again) return;
  menuApp = app;
  menuButton = button;
  renderOpenMenu(app);
  $("open-menu").hidden = false;
  button.setAttribute("aria-expanded", "true");
  placeOpenMenu();
}
function closeMenu() {
  $("open-menu").hidden = true;
  if (menuButton) menuButton.setAttribute("aria-expanded", "false");
  menuApp = "";
  menuButton = null;
}
$("open-button").addEventListener("click", (event) => { event.stopPropagation(); toggleMenu(openApp, event.currentTarget); });
$("apps-list").addEventListener("scroll", closeMenu);
window.addEventListener("resize", closeMenu);
document.addEventListener("click", (event) => { if (!$("open-menu").contains(event.target)) closeMenu(); });
document.addEventListener("keydown", (event) => { if (event.key === "Escape" && menuApp) { const b = menuButton; closeMenu(); b.focus(); } });

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
      text: (answer.why || "") + " An admin of the machine can make themselves an owner. hi tells the app channel about each owner change.",
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
  stop: (app) => ({ title: "Stop " + app + "?", text: "The lead, stage and production stop. The app does not answer in Slack until someone starts it again. The folder and data stay.", ok: "Stop" }),
  rebuild: (app) => ({ title: "Rebuild " + app + "?", text: "hi builds the box image of this machine again, with the current Ubuntu packages. This takes a few minutes. Then all apps on this machine move to the new image. Their addresses do not change. Dev boxes move when they restart.", ok: "Rebuild" }),
  freeze: (app) => ({ title: "Freeze " + app + "?", text: "The boxes stop. The lead can reach only Slack. This continues until someone unfreezes the app. hi tells the ops channel why.", ok: "Freeze", why: "Why? hi tells the ops channel." }),
  pause: (app) => ({ title: "Pause " + app + "?", text: "The work stops where it is, until someone unpauses the app. hi tells the ops channel why.", ok: "Pause", why: "Why? hi tells the ops channel." }),
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
    toast(actionToasts[action] + " " + app + ". See the output in Jobs.");
    followJob(job);
    return true;
  } catch (e) {
    if (!(e instanceof SignIn)) toast(e.message, true);
    return false;
  }
}

// ask is the dialog for a yes: it answers false, true, or the why typed.
function ask({ title, text, ok, why, whyOptional, typed, field, fieldValue, fieldCheck, check, checked }) {
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
    fieldBox.value = fieldValue || "";
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
        const problem = fieldCheck ? fieldCheck(value) : value ? "" : "Fill in this field.";
        if (problem) { $("ask-error").textContent = problem; return; }
      }
      if (typed && typedBox.value.trim() !== typed) { $("ask-error").textContent = "Type " + typed + " to continue."; return; }
      if (field || check) { done({ field: fieldBox.value.trim(), checked: $("ask-check").checked }); return; }
      if (why) {
        if (!reason.value.trim() && !whyOptional) { $("ask-error").textContent = "Give a reason. hi tells the people of the app."; return; }
        done(reason.value.trim() || true);
      } else done(true);
    };
    $("ask-cancel").onclick = () => done(false);
    // A closed ask's close event comes a moment later, when the next ask
    // may already be open: only a close that leaves the dialog shut
    // (Escape) cancels this one.
    dialog.onclose = () => { if (!dialog.open) resolve(false); };
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
  await copyText($("link-value").value);
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
  version.replaceChildren(el("p", {}, el("strong", {}, "hi " + state.version), state.latest ? el("span", { class: "warn" }, " · " + state.latest + " is out") : el("span", { class: "muted" }, " · the newest version on GitHub")));
  version.append(el("button", { class: state.latest ? "primary" : "", onclick: update }, state.latest ? "Update to " + state.latest : "Update hi"));
  version.append(el("p", { class: "muted small" }, (state.org ? "After an update, the server, the apps and this page restart." : "After an update, the apps and this page restart.") + " Each app lead stops for about 30 seconds. This page stops for about one second."));
  badge("machine-badge", state.latest ? 1 : 0);

  const reach = $("machine-reach");
  reach.replaceChildren(
    el("p", {}, el("a", { href: state.address }, state.address)),
    el("p", { class: "muted small" }, state.line));
  if (state.policy) reach.append(el("p", { class: "small" }, "Ask your IT team for this: ", el("span", { class: "copyable" }, state.policy), "."));
  // What the apps need on this machine, Start at boot among it, with
  // Install (hi_admin.md, release 8).
  reach.append(el("h2", {}, "What the apps need here"), needsBox("", null));
  $("netbird-form").hidden = !state.needs_key;
  $("machine-user").textContent = state.user;

  renderAdmins();
}

// The admins: the server's owner and admins, or the machine's own, in
// People under Server (or Admins on a machine without a server).
function renderAdmins() {
  if (!state) return;
  const list = $("admins-list");
  list.replaceChildren();
  // One list, the server's, on a machine that runs only the server; the
  // machine's own beside an org that keeps its admins apart.
  const orgAdmins = !!(state.org && !state.org.no_owner && !state.org_apart);
  const canChange = (!orgAdmins || state.me.owner) && state.me.machine;
  $("admin-add-open").hidden = !canChange;
  $("admin-add-email").hidden = !orgAdmins;
  $("admin-add-slack").required = orgAdmins;
  $("admin-add-slack").placeholder = orgAdmins ? "Their Slack member ID" : "Their Slack member ID (optional)";
  $("admin-add-name").placeholder = orgAdmins ? "Their hi user name, such as olga" : "Their name";
  $("admins-note").hidden = !orgAdmins && !state.org_apart;
  $("admins-note").textContent = orgAdmins ? "Each admin can also approve requests, in Slack and here." + (state.me.owner ? "" : " Only the owner adds and removes admins.")
    : state.org_apart ? "These admins run this machine and its apps. The owner and admins of the server are in the server section Admins. An admin of this machine is not an approver." : "";
  $("admins-title").textContent = orgAdmins ? "Server owner and admins" : "Machine admins";
  const query = $("people-search").value.trim().toLowerCase();
  const admins = state.admins.filter((a) => (!state.org_apart || a.machine) && (!query || [a.name, a.email, a.slack].join(" ").toLowerCase().includes(query)));
  if (!admins.length) list.append(el("li", { class: "empty" }, "No one matches."));
  for (const admin of admins) {
    const me = admin.name === state.me.name;
    const acts = el("span", { class: "actions" });
    acts.append(el("button", { onclick: () => newLink(admin, false), title: "Make a new link to copy. Their old link stops working." }, "New link"));
    if (admin.slack) acts.append(el("button", { onclick: () => newLink(admin, true), title: "Send a new link in a Slack DM. Their old link stops working." }, "By Slack"));
    if (!me && canChange && (!admin.owner || state.org_apart)) acts.append(el("button", { class: "danger", onclick: () => removeAdmin(admin) }, "Remove"));
    const what = el("span", { class: "what" }, adminPersonName(admin), me ? el("span", { class: "muted" }, " (you)") : null, admin.owner ? el("span", { class: "muted" }, " · server owner") : state.org_apart && admin.org ? el("span", { class: "muted" }, " · also a server admin") : null,
      el("span", { class: "muted small block" }, (admin.slack ? "Slack " + admin.slack : "no Slack member ID") + (admin.email ? " · " + admin.email : "")));
    const li = el("li", {}, what, acts);
    list.append(li);
    if (me && !admin.slack && !orgAdmins && !admin.org) {
      const form = el("form", { class: "stack slack-form", autocomplete: "off" },
        el("label", { for: "my-slack" }, "Your Slack member ID. With it, you open dev pages as yourself and get new links in a Slack DM. To find it in Slack, open your profile, click ⋮, then Copy member ID."),
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
  const ok = await ask({ title: state.latest ? "Update hi to " + state.latest + "?" : "Update hi?", text: "hi updates itself. Then it restarts the apps and this page on the new version. Each app lead stops for about 30 seconds. This page stops for about one second, then comes back.", ok: "Update" });
  if (!ok) return;
  try {
    const job = await api("/api/update", {});
    waitFor = state.version;
    toast("Updating hi. See the output in Jobs. This page comes back on the new version.", true);
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
  const ok = await ask({ title: "Remove " + admin.name + " from the admins?", text: "Their links and sign-ins stop now. If they can log in to the machine, they still can.", ok: "Remove" });
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
// Other machines, and an app's Move… (hi_app_move.md): this machine's key,
// the machines it moves apps to, and the ones it takes apps from.

let machines = null;

async function refreshMachines() {
  const box = $("machines-view");
  if (!machines) box.replaceChildren(el("p", { class: "muted small" }, "Checking the other machines…"));
  try { machines = await api("/api/machines"); } catch (e) { if (!(e instanceof SignIn)) box.replaceChildren(el("p", { class: "error small" }, e.message)); return; }
  renderMachines();
}

function machineState(m) {
  if (m.takes) return el("span", { class: "muted small" }, " can take apps from this machine" + (m.version ? " · hi " + m.version : ""));
  return el("span", { class: "warn small" }, " " + (m.problem || "does not answer"));
}

function renderMachines() {
  const box = $("machines-view");
  if (!machines) return;
  const parts = [el("p", { class: "muted small" }, "hi can move an app to another machine. The addresses of the app do not change. A machine can take apps from another machine only if it has the key of that machine. The key lets it receive and start apps. It does not let it log in.")];

  // Where this machine moves apps to.
  const to = el("ul", { class: "list machines-list" });
  for (const m of machines.to) {
    to.append(el("li", {}, el("span", { class: "what" }, el("span", { class: "dot " + (m.takes ? "idle" : "bad"), "aria-hidden": "true" }), el("strong", {}, m.name), machineState(m)),
      el("span", { class: "actions" }, el("button", { class: "small", onclick: () => forgetMachine(m.name) }, "Remove"))));
    if (!m.takes) to.append(el("li", { class: "plain" }, machineFix(m)));
  }
  if (!machines.to.length) to.append(el("li", { class: "empty" }, "No machines yet. Add one here, or with Move… on an app."));
  const addTo = el("form", { class: "signin-row", autocomplete: "off" }, input({ placeholder: "Name on NetBird, for example " + machines.machine, title: "Name of the other machine on NetBird. This machine is " + machines.machine + ".", "aria-label": "A machine to move apps to", required: true }), el("button", { type: "submit" }, "Add"));
  addTo.addEventListener("submit", async (event) => {
    event.preventDefault();
    try { await api("/api/machines", { action: "to", name: addTo.querySelector("input").value.trim() }); addTo.reset(); await refreshMachines(); } catch (e) { if (!(e instanceof SignIn)) toast(e.message, true); }
  });
  parts.push(el("h3", {}, "Moves apps to"), to, addTo);

  // This machine's key, for another machine to take apps from it.
  parts.push(el("h3", {}, "This machine's key"),
    el("p", { class: "muted small" }, "To let another machine take apps from this one, paste this line on its admin page, under Takes apps from. Or run the line on that machine, as the account that runs hi."),
    el("div", { class: "key-line" }, el("code", {}, machines.line), el("button", { class: "small", onclick: () => copyText(machines.line) }, "Copy")));

  // Where this machine takes apps from.
  const from = el("ul", { class: "list machines-list" });
  for (const name of machines.from) {
    from.append(el("li", {}, el("span", { class: "what" }, el("strong", {}, name)), el("span", { class: "actions" }, el("button", { class: "small danger", onclick: () => untakeMachine(name) }, "Remove"))));
  }
  if (!machines.from.length) from.append(el("li", { class: "empty" }, "None. No machine can move apps to this machine."));
  const pasted = el("textarea", { rows: "2", maxlength: "800", placeholder: "Key of the other machine. Its admin page shows the key under This machine's key.", "aria-label": "Key of the other machine", required: true });
  const take = el("form", { class: "stack", autocomplete: "off" }, pasted, el("div", { class: "buttons" }, el("button", { class: "primary", type: "submit" }, "Take apps from it")));
  take.addEventListener("submit", async (event) => {
    event.preventDefault();
    try { toast((await api("/api/machines", { action: "take", key: pasted.value })).text); take.reset(); await refreshMachines(); } catch (e) { if (!(e instanceof SignIn)) toast(e.message, true); }
  });
  parts.push(el("h3", {}, "Takes apps from"), from, take);
  box.replaceChildren(...parts);
}

// machineFix says what to do about a machine that doesn't take apps from
// this one: the line to paste there, or why it may not answer.
function machineFix(m) {
  const again = el("button", { class: "small", onclick: () => refreshMachines() }, "Check again");
  if ((m.problem || "").includes("doesn't take apps")) {
    return el("div", { class: "machine-fix small" },
      el("p", {}, "Open the admin page of " + m.name + ". Go to Machine, Other machines. Paste this line under Takes apps from. Or run the line on " + m.name + ", as the account that runs hi."),
      el("div", { class: "key-line" }, el("code", {}, machines.line), el("button", { class: "small", onclick: () => copyText(machines.line) }, "Copy")),
      el("div", { class: "data-actions" }, again));
  }
  return el("div", { class: "machine-fix small" },
    el("p", {}, "Check the name. Use the name of the machine on NetBird. This machine is " + machines.machine + ". The other machine must be on, on NetBird, and have hi."),
    el("div", { class: "data-actions" }, again));
}

// movedView is an app that moved to another machine: where, and how the
// move went.
function movedView(m) {
  const seconds = (n) => (n >= 90 ? Math.round(n / 60) + " minutes" : n + " seconds");
  const facts = el("dl", { class: "facts" });
  const fact = (term, value) => { if (value) facts.append(el("dt", {}, term), el("dd", {}, value)); };
  fact("Moved", new Date(m.at).toLocaleString() + " (" + ago(m.at) + ")" + (m.by ? ", by " + m.by : ""));
  fact("Copied", m.size ? size(m.size) + " (folder, secrets, NetBird identity and snapshots)" : "");
  fact("Took", m.took ? seconds(m.took) : "");
  fact("Stopped for", m.stopped ? seconds(m.stopped) + ". hi copied the last changes and started the app there." : "");
  fact("Snapshot", m.snapshot ? m.snapshot + ". hi took it here just before the move." : "");
  fact("Folder here", m.folder ? el("code", {}, m.folder) : "");
  const actions = el("div", { class: "data-actions" });
  if (m.admin) actions.append(el("a", { class: "button-like primary", href: m.admin, target: "_blank", rel: "noopener noreferrer" }, "Open the admin page of " + m.to + " ↗"));
  actions.append(el("button", { onclick: () => forgetMoved(m.app) }, "Remove from this list"));
  return [
    el("div", { class: "admin-head" }, el("h1", { class: "admin-title" }, m.app), el("span", { class: "pill open" }, "moved"), el("span", { class: "muted" }, "to " + m.to)),
    el("p", { class: "note" }, m.app + " runs on " + m.to + " now. Its addresses, people, links and Slack channel did not change. Manage it on the admin page of " + m.to + "."),
    facts, actions,
    el("div", { class: "how muted small" }, el("h3", {}, "How it works"), el("ul", {},
      el("li", {}, "To bring the app back, open the admin page of " + m.to + ". Open " + m.app + ", click Move…, and pick " + (state && state.move_name ? state.move_name : "this machine") + "."),
      el("li", {}, "hi keeps the app folder on this machine. A move back copies only the changes."),
      el("li", {}, "Remove from this list does not delete the folder.")))];
}

async function forgetMoved(app) {
  try { toast((await api("/api/machines", { action: "forget-moved", name: app })).text); await refreshState(); openView(""); } catch (e) { if (!(e instanceof SignIn)) toast(e.message, true); }
}

async function forgetMachine(name) {
  try { await api("/api/machines", { action: "forget", name }); await refreshMachines(); } catch (e) { if (!(e instanceof SignIn)) toast(e.message, true); }
}

async function untakeMachine(name) {
  const ok = await ask({ title: "Stop taking apps from " + name + "?", text: name + " cannot move apps to this machine after this. Apps that it moved here stay.", ok: "Stop" });
  if (!ok) return;
  try { toast((await api("/api/machines", { action: "untake", name })).text); await refreshMachines(); } catch (e) { if (!(e instanceof SignIn)) toast(e.message, true); }
}

// moveApp is Move…: pick a machine, see what it has and lacks for the app,
// then move it as a job.
async function moveApp(app) {
  const dialog = $("move-dialog");
  let picked = "";
  let checking = 0;
  $("move-title").textContent = "Move " + app + " to another machine";
  $("move-text").textContent = "The lead, production, stage and the dev page stop for about one minute. Then they run on the other machine, at the same addresses. hi copies the data, snapshots and secrets of the app. The folder stays on this machine, with a note that tells where the app went.";
  $("move-ok").disabled = true;
  $("move-check").replaceChildren();
  const list = $("move-list");
  const draw = () => {
    list.replaceChildren();
    if (machines) $("move-add-name").placeholder = "Name of another machine on NetBird, for example " + machines.machine;
    const known = machines ? machines.to : [];
    for (const m of known) {
      const li = el("li", { role: "option", "aria-selected": String(m.name === picked), tabindex: "0" },
        el("span", { class: "what" }, el("span", { class: "dot " + (m.takes ? "idle" : "bad"), "aria-hidden": "true" }), el("strong", {}, m.name), machineState(m)));
      li.addEventListener("click", () => pick(m.name));
      li.addEventListener("keydown", (e) => { if (e.key === "Enter" || e.key === " ") { e.preventDefault(); pick(m.name); } });
      list.append(li);
    }
    if (!machines) list.append(el("li", { class: "empty" }, "Checking the other machines…"));
    else if (!known.length) list.append(el("li", { class: "empty" }, "No machines yet. Add the machine to move the app to."));
  };
  const pick = async (name) => {
    picked = name;
    draw();
    $("move-ok").disabled = true;
    const box = $("move-check");
    box.replaceChildren(el("p", { class: "muted small" }, "Checking " + name + " for " + app + "…"));
    const mine = ++checking;
    let view;
    try { view = await api("/api/move/check", { app, machine: name }); } catch (e) { if (mine === checking && !(e instanceof SignIn)) box.replaceChildren(el("p", { class: "error small" }, e.message)); return; }
    if (mine !== checking) return;
    const parts = [];
    if (view.error) {
      parts.push(el("p", { class: "error small" }, view.error + "."));
      if (view.access) {
        parts.push(el("p", { class: "small" }, "Open the admin page of " + view.machine + ". Go to Machine, Other machines. Paste this line under Takes apps from. Or run the line on " + view.machine + ", as the account that runs hi. Then pick the machine again."),
          el("div", { class: "key-line" }, el("code", {}, view.access), el("button", { class: "small", onclick: () => copyText(view.access) }, "Copy")));
      }
    } else {
      parts.push(el("p", { class: "small" }, view.machine + " runs hi " + view.version + ". The folder goes to ", el("code", {}, view.folder), " on that machine."));
      if (view.problems.length) parts.push(el("p", { class: "error small" }, "Fix these first:"), el("ul", { class: "small" }, ...view.problems.map((p) => el("li", {}, p))));
      if (view.warnings.length) parts.push(el("p", { class: "warn small" }, "These work differently on that machine:"), el("ul", { class: "small" }, ...view.warnings.map((w) => el("li", {}, w))));
      if (!view.problems.length) parts.push(el("p", { class: "muted small" }, "Nothing stops the move."));
      $("move-ok").disabled = view.problems.length > 0;
    }
    box.replaceChildren(...parts);
    // What the check found about the machine shows in the list too.
    if (machines) { const m = machines.to.find((x) => x.name === name); if (m) { m.takes = !view.access && !(view.error || "").includes("doesn't answer"); if (view.version) m.version = view.version; m.problem = view.access ? "doesn't take apps from this machine yet" : m.takes ? "" : view.error; draw(); } }
  };
  $("move-add").onsubmit = async (event) => {
    event.preventDefault();
    const name = $("move-add-name").value.trim();
    if (!name) return;
    try {
      const answer = await api("/api/machines", { action: "to", name });
      $("move-add-name").value = "";
      if (machines && !machines.to.some((m) => m.name === answer.machine.name)) machines.to.push(answer.machine);
      pick(answer.machine.name);
    } catch (e) { if (!(e instanceof SignIn)) toast(e.message, true); }
  };
  const close = () => { checking++; dialog.close(); };
  $("move-cancel").onclick = close;
  $("move-ok").onclick = async () => {
    if (!picked) return;
    const machine = picked;
    close();
    try {
      const job = await api("/api/action", { app, action: "move", machine });
      toast("Moving " + app + " to " + machine + ". See the progress in Jobs.", true);
      followJob(job);
      const wait = async () => {
        let answer;
        try { answer = await api("/api/job?id=" + encodeURIComponent(job.id) + "&from=0"); } catch (e) { setTimeout(wait, 3000); return; }
        if (answer.job.state === "running") { setTimeout(wait, 3000); return; }
        await refreshState();
        if (openApp === app) { detail = null; refreshDetail(); }
        if (answer.job.state === "done") toast(app + " moved to " + machine + " and runs there now.", true);
        else toast(app + " did not move. It runs here as before. See the reason in Jobs.", true);
      };
      setTimeout(wait, 3000);
    } catch (e) { if (!(e instanceof SignIn)) toast(e.message, true); }
  };
  draw();
  dialog.showModal();
  if (!machines) refreshMachines().then(draw);
  else api("/api/machines").then((fresh) => { machines = fresh; draw(); }).catch(() => {});
}

// ---------------------------------------------------------------------------
// Activity, Jobs, Logs, Incidents

async function refreshActivity() {
  try {
    const list = await api("/api/activity");
    const box = $("activity-list");
    box.replaceChildren();
    if (!list.length) box.append(el("li", { class: "empty" }, "Nothing yet. Actions of admins show here" + (state && state.org ? ", with the server audit." : ".")));
    for (const entry of list) {
      box.append(el("li", {}, el("div", {}, el("strong", {}, entry.who), " ", entry.what, entry.app ? el("span", { class: "muted" }, " · " + entry.app) : null, entry.org ? el("span", { class: "muted" }, " · the server") : null),
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
  if (!openApp && jobs.length) list.append(el("li", { class: "empty" }, "All jobs, also the machine jobs. Open an app to see only its jobs."));
  if (!shownJobs.length) list.append(el("li", { class: "empty" }, openApp ? "No jobs for " + openApp + " in the last two weeks. Restarts, rebuilds and snapshots show here, with their output." : "No jobs yet. Restarts, rebuilds, snapshots and updates show here, with their output."));
  for (const job of shownJobs) {
    const cls = job.state === "running" ? "working" : job.state === "done" ? "done" : "bad";
    list.append(el("li", {}, el("span", { class: "dot " + cls, "aria-hidden": "true" }),
      el("button", { class: "stask-row", onclick: () => openJob(job.id) }, el("span", { class: "stask-title" }, job.title),
        el("span", { class: "stask-num" }, (openApp || !job.app ? "" : job.app + " · ") + job.by + " · " + ago(job.started)))));
  }
  if (jobOpen) refreshJob();
}

function followJob(job) {
  if (!hasView("jobs")) openView("");
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
// Logs are what's open: an app's, or the server's; on Home, the
// server's on a machine that runs only the server, or the admin page's own.
function logsApp() {
  const here = openApp && !movedOpen() ? openApp : "";
  const app = here || (state && state.org && (orgOpen() || !state.apps.length) ? ":org" : "");
  $("logs-whose").textContent = here ? here : app === ":org" ? "The server" : "The admin page";
  $("logs-whose").title = here ? "The lead and service of " + here : app === ":org" ? "hi-server.service" : "Open an app or the server to see its logs";
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

// Incidents seen: for each app (":org" for the server), when the newest open
// incident Incidents has shown opened. The red counts are the open ones
// opened since; this browser's own, as Jobs' dot is.
function incidentsSeen() {
  try { return JSON.parse(localStorage.getItem("hi-admin-incidents-seen") || "{}") || {}; } catch (e) { return {}; }
}
function unseenIncidents(whose, opened) {
  const seen = incidentsSeen()[whose] || 0;
  return (opened || []).filter((t) => new Date(t).getTime() > seen).length;
}
function markIncidentsSeen(list) {
  const seen = incidentsSeen();
  const changed = new Set();
  for (const incident of list) {
    const t = new Date(incident.opened).getTime();
    if (incident.status !== "closed" && t > (seen[incident.app] || 0)) { seen[incident.app] = t; changed.add(incident.app); }
  }
  if (!changed.size) return;
  try { localStorage.setItem("hi-admin-incidents-seen", JSON.stringify(seen)); } catch (e) { /* private mode */ }
  if (!state) return;
  // The seen apps' red counts shrink and fade first, then the list
  // draws without them.
  const going = [...$("apps-list").querySelectorAll(".count[data-app]")].filter((n) => changed.has(n.dataset.app));
  if (!going.length) { renderApps(); return; }
  for (const n of going) n.classList.add("leaving");
  let done = false;
  const finish = () => { if (!done) { done = true; renderApps(); } };
  going[0].addEventListener("animationend", finish, { once: true });
  setTimeout(finish, 600);
}

async function refreshIncidents() {
  try {
    const all = await api("/api/incidents");
    const whose = orgOpen() ? ":org" : openApp;
    const list = whose ? all.filter((i) => i.app === whose) : all;
    const box = $("incidents-list");
    box.replaceChildren();
    if (!whose && all.length) box.append(el("li", { class: "empty" }, state && state.org ? "Incidents of all apps and the server. Open one to see only its incidents." : "Incidents of all apps. Open an app to see only its incidents."));
    if (!list.length) box.append(el("li", { class: "empty" }, (whose === ":org" ? "The server has no incidents. An incident is an instance that no request started, or a machine that a failed start left behind" : whose ? whose + " has had no incidents" : "No incidents") + (whose === ":org" ? "." : " in the last month.")));
    for (const incident of list) {
      const cls = incident.status === "closed" ? "done" : incident.status === "waiting" ? "blocked" : "bad";
      box.append(el("li", { class: "incident" },
        el("div", { class: "incident-head" }, el("span", { class: "dot " + cls, "aria-hidden": "true" }),
          whose ? null : el("button", { class: "link-button", onclick: () => (incident.app === ":org" ? openOrg("") : openView(incident.app)) }, incident.app === ":org" ? "the server" : incident.app), el("span", { class: "muted small" }, (incident.app === ":org" ? "" : incident.id + " · ") + incident.status + " · " + ago(incident.opened))),
        el("div", { class: "clamp3", title: incident.title }, incident.title),
        el("div", { class: "muted small clamp2", title: incident.last || "" }, incident.where + (incident.last ? " · " + incident.last : ""))));
    }
    if (viewShown("incidents")) markIncidentsSeen(list);
  } catch (e) { /* the next round */ }
}

// ---------------------------------------------------------------------------
// the server, on its machine: Requests, Running, and Spend on the
// right; Org, People, and Keys on the left; the policy in the middle.
// Everything goes to the server under the admin's own name.

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
      return [who, r.owner ? " (an agent for " + r.owner + ")" : "", " wants to join, from ", el("strong", {}, r.hostname || "a device")];
    case "extend":
      return [who, " asks for ", hours(r.seconds), " more on ", el("strong", {}, r.name)];
    case "serve":
      return [who, " wants to serve ", el("strong", {}, r.model || r.name), " to the server"];
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
    if (r.waits && r.state === "pending") li.append(el("div", { class: "muted small" }, "A person must approve: " + r.waits + "."));
    if (r.state === "starting") {
      li.append(el("div", { class: "muted small" }, "Approved by " + (r.decided_by || "?") + ". Starting" + (r.progress ? ": " + r.progress : "…")));
    } else if (r.mine) {
      li.append(el("div", { class: "muted small" }, "Your request. Another approver decides."));
    } else {
      const acts = el("div", { class: "data-actions" });
      let group = null;
      if (r.new_user) {
        group = el("input", { list: "org-groups", placeholder: "Their group", "aria-label": "Their group", maxlength: "32", class: "small-input" });
        acts.append(group);
      }
      acts.append(el("button", { class: "primary small", onclick: () => {
        if (group && !group.value.trim()) { group.focus(); toast(r.user + " is a new user. Choose a group first."); return; }
        orgDo({ what: "decide", action: "approve", id: r.id, group: group ? group.value.trim() : "" }, "Approved " + r.id + ".");
      } }, "Approve"));
      if (r.kind === "compute" && r.rate && r.policy_group && !r.over_budget && r.waits) {
        acts.append(el("button", { class: "small", title: "Approve this request. Also change auto-approval for " + r.policy_group + ", so that similar requests start without approval.", onclick: () => approveAndAllow(r) }, "Approve, and auto-approve similar…"));
      }
      acts.append(el("button", { class: "small", onclick: async () => {
        const why = await ask({ title: "Deny " + r.id + "?", text: "hi tells " + r.user + " that you denied the request. If you give a reason, hi tells it too.", ok: "Deny", why: "Reason (optional). hi tells " + r.user + ".", whyOptional: true });
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
  if (!running.length) box.append(el("li", { class: "empty" }, "No compute machine runs now."));
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
        const ok = await ask({ title: "Stop " + r.name + "?", text: "The machine stops now. hi tells " + r.user + " who stopped it. Data on the machine that is not saved in another place is lost.", ok: "Stop" });
        if (ok) orgDo({ what: "stop", name: r.name }, "Stopping " + r.name + ".");
      } }, "Stop"));
    if (perUser[r.user] > 1) {
      acts.append(el("button", { class: "small danger", onclick: async () => {
        const ok = await ask({ title: "Stop all machines of " + r.user + "?", text: "All " + perUser[r.user] + " machines of " + r.user + " stop now. Type the user name to continue.", ok: "Stop all", typed: r.user });
        if (ok) orgDo({ what: "stop", user: r.user }, "Stopping all machines of " + r.user + ".");
      } }, "Stop all of " + r.user));
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
    if (orgOpen() && orgSection !== "overview" && orgSection !== "policy") $("orgview").replaceChildren(orgHead(), el("p", { class: "error" }, e.message));
    else if (orgOpen() && orgSection === "overview") renderOrgSection();
    return;
  }
  if (orgOpen() && orgSection === "policy") { if (!policyEdited) renderPolicy(); }
  else if (orgOpen()) {
    const typing = document.activeElement && $("orgview").contains(document.activeElement) && /INPUT|TEXTAREA|SELECT/.test(document.activeElement.tagName);
    if (!typing) renderOrgSection();
  }
}

// A small form that opens under its button.
function inlineForm(fields, submit, label) {
  const form = el("form", { class: "machine-block stack", autocomplete: "off", hidden: true });
  const inputs = {};
  for (const f of fields) {
    const input = f.type === "checkbox"
      ? el("input", { type: "checkbox" })
      : el("input", { type: f.type || "text", required: f.required, maxlength: f.max || "200", placeholder: f.placeholder, "aria-label": f.placeholder || f.label, autocomplete: "off", value: f.value, list: f.list, pattern: f.pattern });
    inputs[f.name] = input;
    form.append(f.type === "checkbox" ? el("label", { class: "check" }, input, " " + f.label) : input);
  }
  form.append(el("div", { class: "buttons" }, el("button", { type: "button", onclick: () => { form.hidden = true; } }, "Cancel"), el("button", { class: "primary", type: "submit" }, label || "Save")));
  form.addEventListener("submit", async (event) => {
    event.preventDefault();
    const values = {};
    for (const [name, input] of Object.entries(inputs)) values[name] = input.type === "checkbox" ? input.checked : input.value.trim();
    // false or null (orgDo's failure, already toasted) keeps what was typed.
    const result = await submit(values);
    if (result !== false && result !== null) { form.reset(); form.hidden = true; }
  });
  return form;
}
function heading(text, button) { return el("h2", { class: "with-action" }, text, el("span", { class: "grow" }), button || null); }
function plus(title, form) { return el("button", { class: "side-icon", title, "aria-label": title, onclick: () => { form.hidden = !form.hidden; const first = form.querySelector("input"); if (first && !form.hidden) first.focus(); } }, "+"); }

// ---------------------------------------------------------------------------
// the server, opened: its sections in the middle and on the bar, as an app's.
// Someone who isn't one of the server's admins sees them, and changes nothing.

const orgSectionNames = { overview: "Overview", vault: "Vault", backups: "Backups", users: "Users", admins: "Admins", policy: "Budgets and approval", keys: "Keys", templates: "Templates", data: "Data", models: "Models", slack: "Slack", walls: "Wall screens" };
for (const chip of document.querySelectorAll("#org-sections [data-org-section]")) {
  chip.addEventListener("click", () => openOrg(chip.dataset.orgSection));
}

function openOrg(section) {
  if (!state || !state.org) return;
  const was = orgOpen();
  orgSection = orgSectionNames[section] ? section : "overview";
  for (const chip of document.querySelectorAll("#org-sections [data-org-section]")) chip.setAttribute("aria-pressed", String(chip.dataset.orgSection === orgSection));
  if (orgSection === "policy") { policyEdited = false; policyText = false; policyDraft = null; }
  setMiddle(":org");
  if (narrow()) { panes.left.open = false; keepPanes(); renderPanes(); }
  if (!was) moved();
  if (orgSection === "policy") renderPolicy(); else renderOrgSection();
  if (orgSection === "vault") { vaultHistory = null; refreshVault(); }
  else if (orgSection === "backups") refreshBackups();
  else if (orgSection !== "overview" || !orgSettings) refreshOrgSettings();
}

function orgHead() {
  const o = state.org;
  return el("div", { class: "admin-head" }, el("h1", { class: "admin-title" }, "The server"),
    el("span", { class: "pill " + (o.up ? "open" : "closed") }, el("span", { class: "dot " + (o.up ? "idle" : "bad"), "aria-hidden": "true" }), o.up ? "runs" : "does not answer"),
    el("span", { class: "muted" }, orgSection === "overview" ? (o.owner ? "owned by " + o.owner : "") : orgSectionNames[orgSection]));
}
const orgCanChange = () => !!(state && state.me && state.me.org);
function orgReadOnlyNote() {
  return orgCanChange() ? null : el("p", { class: "note muted small" }, "You can see the server. Only its owner and admins can change it.");
}

function renderOrgSection() {
  if (!orgOpen() || orgSection === "policy") return;
  const box = $("orgview");
  const parts = [orgHead()];
  const note = orgReadOnlyNote();
  if (note) parts.push(note);
  const s = orgSettings;
  if (orgSection === "overview") parts.push(...orgOverview(s));
  else if (orgSection === "vault") parts.push(vaultBlock(true));
  else if (orgSection === "backups") parts.push(...backupsBlock());
  else if (!s) parts.push(el("p", { class: "muted" }, "…"));
  else {
    const build = { users: orgUsers, admins: orgAdmins, keys: orgKeys, templates: orgTemplates, data: orgData, models: orgModels, slack: orgSlack, walls: orgWalls }[orgSection];
    parts.push(...build(s));
    if ((s.problems || []).length) parts.push(el("p", { class: "warn small" }, s.problems.join(" · ")));
  }
  box.replaceChildren(...parts);
}

function orgStats(o) {
  const stats = el("div", { class: "org-stats pad" });
  const stat = (n, label, view, warn) => el("button", { class: "org-stat" + (warn ? " warn" : ""), "data-nav": "", onclick: (e) => { e.stopPropagation(); if (!orgOpen()) openOrg(""); showView(view); } }, el("strong", {}, n), el("span", { class: "muted small" }, label));
  stats.append(stat(o.waiting, o.waiting === 1 ? "request waits" : "requests wait", "requests", o.waiting > 0),
    stat(o.running, (o.running === 1 ? "machine runs" : "machines run") + (o.running ? ", " + dollars(o.rate_per_hour) + "/h" : ""), "running"),
    stat(dollars(o.today), "today", "spend"),
    stat(dollars(o.month), "this month" + (o.month_budget ? " of " + dollars(o.month_budget) : ""), "spend", o.month_budget && o.month > o.month_budget));
  if (o.incidents) stats.append(stat(o.incidents, o.incidents === 1 ? "incident" : "incidents", "incidents", true));
  return stats;
}

function orgOverview(s) {
  const o = state.org;
  const parts = [];
  if (o.no_owner) parts.push(el("p", { class: "warn" }, "The server has no owner yet. On the machine, run ", el("code", {}, "hi server owner <Slack member ID> --email <address>")));
  if (o.up) parts.push(el("div", { class: "card-box org-card" }, orgStats(o)));
  // In cards, as an app's Overview.
  const cards = el("div", { class: "admin-cards" });
  parts.push(cards);
  cards.append(card("The server", orgServerBlock(s)));
  if (s && s.net) cards.append(card("Network", orgNetBlock(s.net)));
  const apps = (state.apps || []).filter((a) => !a.error);
  if (apps.length) {
    const t = el("table", { class: "people-matrix spend-table" });
    t.append(el("tr", {}, el("th", {}, "App"), el("th", {}, "Models this month")));
    let total = 0;
    for (const a of [...apps].sort((x, y) => (y.month_usd || 0) - (x.month_usd || 0))) {
      total += a.month_usd || 0;
      t.append(el("tr", {}, el("th", { scope: "row" }, el("button", { class: "link-button", onclick: async () => { await openView(a.name); showSection("spend"); } }, a.name)), el("td", {}, dollars(a.month_usd))));
    }
    t.append(el("tr", {}, el("th", { scope: "row" }, "All"), el("td", {}, el("strong", {}, dollars(total)))));
    cards.append(wide(card("Model spend", el("div", { class: "matrix-scroll" }, t))));
  }
  if (state.org_apart) parts.push(el("p", { class: "muted small" }, "This machine also runs apps. The machine admins manage the apps. The server owner and admins (in Admins) manage the server."));
  return parts;
}

// The server's NetBird peer (hi_server_peer.md): where devices reach it,
// its old addresses during a move, and, for its owner, moving it to
// another network. The machine stays where it is.
function orgNetBlock(n) {
  const owns = orgCanChange() && !!(state.me && state.me.owner);
  const block = el("div", { class: "machine-block" });
  const p = n.peer;
  const move = inlineForm([
    { name: "key", type: "password", placeholder: "Setup key from the NetBird dashboard (Setup Keys)", required: true },
    { name: "url", placeholder: "Management server, if not the NetBird cloud, for example https://netbird.example.com" },
    { name: "name", placeholder: "Peer name (optional, default " + ((p && p.name) || "<machine>-server") + ")", max: "63", pattern: "[a-z0-9]([a-z0-9-]*[a-z0-9])?" },
  ], async (v) => {
    toast("Joining the network. This can take up to 90 seconds…", true);
    const answer = await orgDo({ what: "net", action: "join", key: v.key, url: v.url, name: v.name }, "");
    if (answer === null) return false;
    if (answer.text) toast(answer.text, true);
  }, p ? "Move" : "Join");
  if (!p) {
    block.append(el("p", { class: "small" }, n.listen ? "The server uses the NetBird address of this machine, " : "The server has no NetBird address yet", n.listen ? el("code", {}, n.listen) : null,
      n.listen ? ". If the machine moves, the server moves with it." : "."),
      el("p", { class: "muted small" }, "With its own NetBird peer, the server can move to another network without the machine. It can also have its own access policies."));
    if (owns) block.append(el("div", { class: "data-actions" }, el("button", { class: "small", onclick: () => { move.hidden = !move.hidden; } }, "Give the server its own peer…")), move);
    return block;
  }
  block.append(el("p", { class: "incident-head" }, el("span", { class: "dot " + (p.connected ? "idle" : "bad"), "aria-hidden": "true" }),
    "On NetBird as ", el("code", {}, p.fqdn || p.name), p.address ? " (" + p.address + ")" : "", ", at " + (p.network || "NetBird") + "."));
  if (!p.connected) block.append(el("p", { class: "warn small" }, "The peer is not on its network" + (p.problem ? ": " + p.problem : "") + ". Devices can reach the server again when the peer is back."));
  const old = [];
  if (n.old) old.push(el("p", { class: "small" }, "The server also answers as ", el("code", {}, n.old.fqdn || n.old.name), ". This is the old peer from before the move. It answers until all devices move to the new address, or until " + new Date(n.old.until).toLocaleDateString() + "."));
  if (n.listen) old.push(el("p", { class: "small" }, "The server also answers at ", el("code", {}, n.listen), ". This is the NetBird address of this machine, from before the server had its own peer."));
  if (old.length) {
    block.append(...old);
    const recent = (n.old_devices || []).filter((d) => Date.now() - new Date(d.last).getTime() < 24 * 3600 * 1000);
    block.append(el("p", { class: "muted small" }, recent.length ? "Devices that used the old address in the last day: " + recent.map((d) => d.user + " on " + d.hostname).join(", ") + ". hi tells these devices the new address, and they move to it." : "No device used the old address in the last day."));
    if (owns) block.append(el("div", { class: "data-actions" }, el("button", { class: "small", onclick: async () => {
      if (await ask({ title: "Stop answering at the old addresses?", text: recent.length ? "Devices that did not move yet must join again at the new address, with hi server join " + n.join + ". They stay the same devices." : "All devices moved, or no device used the old address in the last day.", ok: "Stop" })) { const answer = await orgDo({ what: "net", action: "forget-old" }, ""); if (answer && answer.text) toast(answer.text); }
    } }, "Forget the old address")));
  }
  if (n.page) block.append(el("p", { class: "small" }, "The admin page is also at ", el("code", {}, "http://" + (n.join || p.name) + ":1984/"), "."));
  block.append(el("p", { class: "muted small" }, "Ask your IT team for these NetBird policies. Devices can reach ", el("code", {}, n.join || p.name), " on TCP 7373.", n.page ? " Admins can reach it on TCP 1984." : "", " The peer can reach the team machines on the ports that serve their models."));
  if (owns && !n.old) block.append(el("div", { class: "data-actions" }, el("button", { class: "small", onclick: () => { move.hidden = !move.hidden; } }, "Move to another network…")), move);
  return block;
}

function orgServerBlock(s) {
  const o = state.org;
  const block = el("div", { class: "machine-block" });
  block.append(el("p", { class: "incident-head" }, el("span", { class: "dot " + (o.up ? "idle" : "bad"), "aria-hidden": "true" }), o.up ? "The server runs." : "The server does not answer."));
  if (o.error) block.append(el("p", { class: "error small" }, o.error));
  const join = (s && s.net && s.net.join) || (s && s.listen);
  if (join) block.append(el("p", { class: "muted small" }, "To join a device, run ", el("code", {}, "hi server join " + join)));
  const actions = el("div", { class: "data-actions" });
  if (o.service === "user") {
    block.append(el("p", { class: "muted small" }, "The server runs as a user service, hi-server.service, next to this page."));
    actions.append(el("button", { class: "small", onclick: async () => {
      const ok = await ask({ title: "Restart the server?", text: "The server stops for about one second. Machines continue to run, and requests wait.", ok: "Restart" });
      if (ok) orgDo({ what: "restart" }, "The server restarted.");
    } }, o.up ? "Restart" : "Start"));
  } else if (o.service === "system") {
    block.append(el("p", { class: "warn small" }, "The server runs as a system service, so this page cannot restart it. Run hi update on the machine to make it a user service. This needs sudo one time."));
  } else {
    block.append(el("p", { class: "warn small" }, "No service runs the server. To make one, run ", el("code", {}, "hi server init"), " on the machine."));
  }
  actions.append(el("button", { class: "small", "data-nav": "", onclick: () => showView("logs") }, "Logs"));
  block.append(actions);
  return block;
}

// Users: the server's users, their groups and devices.
function orgUsers(s) {
  const users = s.users || [];
  const search = el("input", { type: "search", placeholder: "Find someone", "aria-label": "Find someone", autocomplete: "off", "data-nav": "" });
  const list = el("ul", { class: "list people-list" });
  const show = () => {
    const query = search.value.trim().toLowerCase();
    const shownUsers = users.filter((u) => !query || u.name.includes(query) || (u.group || "").includes(query));
    list.replaceChildren();
    if (!users.length) list.append(el("li", { class: "empty" }, "No users yet. A device asks to join with hi server join. The request shows in Requests."));
    else if (!shownUsers.length) list.append(el("li", { class: "empty" }, "Nobody matches."));
    for (const u of shownUsers) {
      const devices = (u.devices || []).map((d) => d.hostname + (d.last_seen && new Date(d.last_seen).getTime() > 0 ? " (" + ago(d.last_seen) + ")" : ""));
      list.append(el("li", {}, el("span", { class: "what" }, el("strong", {}, u.name), " ", el("span", { class: "muted small" }, (u.group || "no group") + (u.kind === "agent" ? " · agent of " + u.owner : u.kind === "viewer" ? " · viewer" : "")),
        el("span", { class: "muted small block" }, devices.length ? devices.join(", ") : "no devices")),
        el("span", { class: "actions" }, el("button", { class: "danger", onclick: async () => {
          const ok = await ask({ title: "Remove " + u.name + "?", text: "The user and their " + (u.devices || []).length + " devices leave the server. Nothing new starts for them. Machines that run now continue until someone stops them in Running. Type the user name to continue.", ok: "Remove", typed: u.name });
          if (ok) orgDo({ what: "user", action: "remove", name: u.name }, u.name + " has left the server.");
        } }, "Remove"))));
    }
  };
  search.addEventListener("input", show);
  show();
  const add = inlineForm([
    { name: "name", placeholder: "Their hi user name, for example olga", required: true, max: "32", pattern: "[a-z0-9._\\-]+" },
    { name: "group", placeholder: "Their group, for example staff", required: true, max: "32", list: "org-groups" },
    { name: "key", placeholder: "Device key, from hi server key on the device (optional)" },
  ], async (v) => ((await orgDo({ what: "user", action: "add", name: v.name, group: v.group, key: v.key }, v.name + " is on the server.")) === null ? false : undefined), "Add");
  $("org-groups").replaceChildren(...((org && org.groups) || []).map((g) => el("option", { value: g })));
  return [el("p", { class: "purpose" }, "Users ask the server for compute from their devices and agents. Each user is in a group. The policy sets limits for each group. Users are not the people of an app (People), and not admins of this page."),
    heading("Users", plus("Add a user", add)), add, el("div", { class: "logs-tools" }, search), list];
}

// Admins: the owner, and the admins, who approve and run the server.
function orgAdmins(s) {
  const owner = s.people.owner;
  const owns = !!(state.me && state.me.owner);
  const parts = [el("p", { class: "purpose" }, "The owner and admins approve requests, in Slack and here. They change the users, policy and keys of the server. " +
    (state.org_apart ? "They sign in to this page to manage the server. The machine admins (Machine) manage the apps." : "On this machine, they are also admins of this page."))];
  const handOn = inlineForm([
    { name: "slack", placeholder: "Slack member ID of the new owner", required: true, max: "20" },
    { name: "email", type: "email", placeholder: "Their email", required: true },
    { name: "name", placeholder: "Their hi user name (if empty, hi uses their email)", max: "32" },
  ], (v) => orgDo({ what: "owner", slack: v.slack, email: v.email, name: v.name }, "The server has a new owner."), "Change owner");
  parts.push(heading("Owner"));
  if (!owner) parts.push(el("p", { class: "warn small" }, "The server has no owner yet. On the machine, run ", el("code", {}, "hi server owner <Slack member ID> --email <address>")));
  else {
    parts.push(el("div", { class: "machine-block" }, el("p", {}, el("strong", {}, owner.name), " ", el("span", { class: "muted small" }, "Slack " + owner.slack + (owner.email ? " · " + owner.email : ""))),
      owns ? el("div", { class: "data-actions" }, el("button", { class: "small", onclick: () => { handOn.hidden = !handOn.hidden; } }, "Change owner…")) : null));
    parts.push(handOn);
  }
  const add = inlineForm([
    { name: "name", placeholder: "Their hi user name, for example olga", required: true, max: "32" },
    { name: "slack", placeholder: "Their Slack member ID", required: true, max: "20" },
    { name: "email", type: "email", placeholder: "Their email (optional)" },
  ], async (v) => {
    const answer = await orgDo({ what: "admins", action: "add", name: v.name, slack: v.slack, email: v.email });
    if (answer === null) return false;
    if (answer.link) showLink(v.name + " is an admin of the server", answer.text, answer.link); else toast(answer.text || "Done.");
  }, "Add");
  parts.push(heading("Admins", owns ? plus("Add an admin of the server", add) : null), add);
  const list = el("ul", { class: "list admins-list" });
  for (const a of s.people.admins || []) {
    list.append(el("li", {}, el("span", { class: "what" }, el("strong", {}, a.name), el("span", { class: "muted small block" }, "Slack " + a.slack + (a.email ? " · " + a.email : "") + (a.by ? " · added by " + a.by : ""))),
      owns ? el("span", { class: "actions" }, el("button", { class: "danger", onclick: async () => {
        if (await ask({ title: "Remove " + a.name + " from the server admins?", text: "They cannot approve requests or change the server after this." + (state.org_apart ? " If they are a machine admin, they still manage the machine." : " Their links to this page stop working."), ok: "Remove" })) {
          const answer = await orgDo({ what: "admins", action: "remove", name: a.slack });
          if (answer) toast(answer.text || "Done.");
        }
      } }, "Remove")) : null));
  }
  if (!(s.people.admins || []).length) list.append(el("li", { class: "plain muted small" }, "No admins other than the owner."));
  parts.push(list);
  const approvers = Object.entries(s.approvers || {}).sort((a, b) => a[1].localeCompare(b[1]));
  if (approvers.length) parts.push(el("p", { class: "muted small" }, "Approvers in Slack: " + approvers.map(([id, name]) => name).join(", ") + ". To add a person who only approves, run ", el("code", {}, "hi server approvers add"), "."));
  return parts;
}

// Keys: the providers' and the server's OpenRouter key, set or not, never shown.
function orgKeys(s) {
  const parts = [heading("Compute providers"), el("p", { class: "muted small" }, "The server uses these keys to start machines. hi checks each key with the provider before it keeps it. hi never shows a key again.")];
  const list = el("ul", { class: "list" });
  for (const p of s.providers || []) {
    const form = inlineForm([{ name: "key", type: "password", placeholder: "API key for " + p.name, required: true }], (v) => orgDo({ what: "provider", action: "set", name: p.name, key: v.key }, "hi saved the key for " + p.name + "."), "Check and save");
    list.append(el("li", {}, el("span", { class: "what" }, el("strong", {}, p.name), " ", el("span", { class: p.set ? "muted small" : "warn small" }, p.set ? "set" : "not set")),
      el("span", { class: "actions" }, el("button", { onclick: () => { form.hidden = !form.hidden; } }, p.set ? "Replace" : "Set"),
        p.set ? el("button", { class: "danger", onclick: async () => { if (await ask({ title: "Remove the key for " + p.name + "?", text: "Nothing new starts on " + p.name + ". Machines that run there now continue, and stop at their usual time.", ok: "Remove" })) orgDo({ what: "provider", action: "remove", name: p.name }, "hi removed the key for " + p.name + "."); } }, "Remove") : null)));
    list.append(el("li", { class: "plain" }, form));
  }
  parts.push(list);
  const ai = s.ai || {};
  parts.push(heading("Models for hi q"), el("p", { class: "muted small" }, "On the server devices, hi q sends model requests through the server. The server uses its own key."));
  const aiBlock = el("div", { class: "machine-block" });
  aiBlock.append(el("p", {}, ai.has_key ? (ai.on ? "On" : "Off") : "No key yet", ai.model ? el("span", { class: "muted small" }, " · " + ai.model + (ai.url ? " at " + shown(ai.url).replace(/^https?:\/\//, "") : "")) : null));
  if (ai.usage) aiBlock.append(el("p", { class: "muted small" }, "This month: " + ai.usage));
  const aiForm = inlineForm([
    { name: "key", type: "password", placeholder: ai.has_key ? "New OpenRouter key (leave empty to keep the current key)" : "OpenRouter key", required: !ai.has_key },
    { name: "model", placeholder: "Default model", value: ai.model || "" },
  ], (v) => orgDo({ what: "ai", action: "set", key: v.key, model: v.model }, "hi checked and saved it. hi q uses it now."), "Check and save");
  const aiActions = el("div", { class: "data-actions" });
  aiBlock.append(aiActions);
  aiActions.append(el("button", { class: "small", onclick: () => { aiForm.hidden = !aiForm.hidden; } }, ai.has_key ? "Change…" : "Set the key…"));
  if (ai.has_key) {
    aiActions.append(el("button", { class: "small", onclick: () => orgDo({ what: "ai", action: ai.on ? "off" : "on" }, ai.on ? "Turned off." : "Turned on.") }, ai.on ? "Turn off" : "Turn on"));
    aiActions.append(el("button", { class: "small danger", onclick: async () => { if (await ask({ title: "Remove the OpenRouter key?", text: "hi q on the server devices cannot send model requests through the server after this.", ok: "Remove" })) orgDo({ what: "ai", action: "remove" }, "hi removed the key."); } }, "Remove the key"));
  }
  parts.push(aiBlock, aiForm);
  parts.push(el("p", { class: "muted small" }, "The keys of this machine, for its apps, are in Keys on the left."));
  return parts;
}

function orgTemplates(s) {
  const tForm = inlineForm([
    { name: "name", placeholder: "Name, for example team", required: true, max: "32" },
    { name: "url", placeholder: "Git URL, https://…", required: true },
    { name: "ref", placeholder: "Branch or tag (optional)" },
    { name: "token", type: "password", placeholder: "Read token, if the repository is private (hi never shows it)" },
  ], (v) => orgDo({ what: "template", action: "add", name: v.name, url: v.url, ref: v.ref, token: v.token }, "The server now serves the templates of " + v.name + "."), "Add");
  const templates = el("ul", { class: "list" });
  for (const t of s.templates || []) {
    templates.append(el("li", {}, el("span", { class: "what" }, el("strong", {}, t.name), el("span", { class: "muted small block" }, shown(t.url) + (t.ref ? " @ " + t.ref : "")),
      el("span", { class: "muted small block" }, (t.layers || []).length + " templates, " + (t.skills || []).length + " skills" + (t.commit ? " · " + t.commit.slice(0, 7) : "") + (t.fetched ? " · " + ago(t.fetched) : "")),
      t.problem ? el("span", { class: "warn small block" }, t.problem) : null),
      el("span", { class: "actions" }, el("button", { onclick: () => orgDo({ what: "template", action: "sync", name: t.name }, "Fetched " + t.name + ".") }, "Sync"),
        el("button", { class: "danger", onclick: async () => { if (await ask({ title: "Stop serving " + t.name + "?", text: "Devices cannot see its templates and skills after this. Projects made from them do not change.", ok: "Stop serving" })) orgDo({ what: "template", action: "remove", name: t.name }, "Stopped serving " + t.name + "."); } }, "Remove"))));
  }
  if (!(s.templates || []).length) templates.append(el("li", { class: "plain muted small" }, "None. hi init offers only the built-in templates."));
  return [el("p", { class: "purpose" }, "Repositories of templates and skills. hi init offers them on the server devices."), heading("Templates", plus("Add a template repository", tForm)), tForm, templates];
}

function orgData(s) {
  const dForm = inlineForm([
    { name: "orgs", placeholder: "Hugging Face organizations, with spaces between them", required: true },
    { name: "token", type: "password", placeholder: "Hugging Face read token (hi never shows it)", required: true },
  ], (v) => orgDo({ what: "data", action: "add", orgs: v.orgs.split(/[\s,]+/).filter(Boolean), token: v.token }, "The server now serves their data."), "Add");
  const data = el("ul", { class: "list" });
  for (const d of s.data || []) {
    data.append(el("li", {}, el("span", { class: "what" }, el("strong", {}, d.org),
      el("span", { class: "muted small block" }, d.datasets + " datasets, " + d.models + " models, " + d.buckets + " buckets" + (d.account ? " · as " + d.account + (d.role ? " (" + d.role + ")" : "") : "")),
      d.problem ? el("span", { class: "warn small block" }, d.problem) : null),
      el("span", { class: "actions" }, el("button", { onclick: () => orgDo({ what: "data", action: "test", name: d.org }, "Checked " + d.org + ".") }, "Test"),
        el("button", { class: "danger", onclick: async () => { if (await ask({ title: "Stop serving " + d.org + "?", text: "hi data cannot reach its datasets and models after this. hi deletes its token.", ok: "Stop serving" })) orgDo({ what: "data", action: "remove", name: d.org }, "Stopped serving " + d.org + "."); } }, "Remove"))));
  }
  if (!(s.data || []).length) data.append(el("li", { class: "plain muted small" }, "None."));
  return [el("p", { class: "purpose" }, "Hugging Face organizations. On the server devices, hi data can reach their datasets and models."), heading("Data", plus("Serve a Hugging Face organization", dForm)), dForm, data];
}

function orgModels(s) {
  const served = el("ul", { class: "list" });
  const list = (s.ai && s.ai.served_list) || [];
  for (const m of list) {
    served.append(el("li", {}, el("span", { class: "what" }, el("strong", {}, m.name), el("span", { class: "muted small block" }, (m.model || "") + (m.user ? " · " + m.user : "") + (m.host ? " on " + m.host : "") + " · " + (m.state && m.state !== "approved" ? m.state : m.up ? "up" : "stopped"))),
      el("span", { class: "actions" }, el("button", { class: "danger", onclick: async () => { if (await ask({ title: "Stop sending requests to " + m.name + "?", text: "hi q stops sending server requests to this model. The machine continues to serve it.", ok: "Stop" })) orgDo({ what: "served", name: m.name }, "Stopped sending requests to " + m.name + "."); } }, "Remove"))));
  }
  if (!list.length) served.append(el("li", { class: "plain muted small" }, "None. To add one, run hi serve llm --org on a machine."));
  return [el("p", { class: "purpose" }, "Models that team machines serve. hi q sends server requests to these models."), served];
}

// The server's Slack app, set up here as hi server slack setup does on the
// machine. What's typed lives in slackSetup, so the page drawing again
// every few seconds keeps it.
let slackSetup = null;

function orgSlack(s) {
  const slack = el("div", { class: "machine-block" });
  const owns = orgCanChange() && !!state.me.owner;
  const sl = s.slack || {};
  if (sl.set) slack.append(el("p", { class: "small" }, "hi sends approvals to the channel ", el("code", {}, sl.channel || "?"), ", and to the Home tab of each approver."),
    owns && !slackSetup ? el("div", { class: "data-actions" }, el("button", { class: "small", onclick: () => { slackSetup = {}; renderOrgSection(); } }, "Change tokens or channel…")) : null);
  else slack.append(el("p", { class: "warn small" }, "Slack is not set up. Approvals are only here and on the machine." + (owns ? " Set it up below." : " The server owner can set it up here, or on the machine with "), owns ? null : el("code", {}, "hi server slack setup")));
  const approvers = Object.entries(s.approvers || {}).sort((a, b) => a[1].localeCompare(b[1]));
  if (approvers.length) slack.append(el("p", { class: "muted small" }, "Approvers: " + approvers.map(([id, name]) => name).join(", ") + ". The owner and admins approve. To add a person who only approves, run ", el("code", {}, "hi server approvers add"), "."));
  const parts = [el("p", { class: "purpose" }, "The Slack app of the server. hi sends approvals, warnings and reports to it."), slack];
  if (owns && (!sl.set || slackSetup)) parts.push(slackSetupForm(sl));
  return parts;
}

function slackSetupForm(sl) {
  const st = slackSetup || (slackSetup = {});
  const box = el("div", { class: "new-step" });
  const bound = (attrs, key) => { const i = input(attrs); i.value = st[key] || ""; i.addEventListener("input", () => { st[key] = i.value; }); return i; };
  const app = bound({ type: "password", placeholder: sl.has_app ? "Saved. Paste a new token to change it." : "xapp-…" }, "app");
  const bot = bound({ type: "password", placeholder: sl.set ? "Saved. Paste a new token to change it." : "xoxb-…" }, "bot");
  const error = el("p", { class: "error" }, st.error || "");
  const tokens = () => ({ app_token: (st.app || "").trim(), bot_token: (st.bot || "").trim() });
  const check = el("button", { type: "button", class: (st.checked ? "" : "primary"), onclick: async () => {
    check.disabled = true; error.textContent = "";
    try {
      const a = (await api("/api/org", Object.assign({ what: "slack", action: "check" }, tokens()))).answer || {};
      Object.assign(st, { checked: true, team: a.team, user: a.user, channels: a.channels || null, problem: a.channels_problem || "", channel: st.channel || a.channel || "", error: "" });
    } catch (e) { if (!(e instanceof SignIn)) st.error = e.message; }
    renderOrgSection();
  } }, st.checked ? (st.channels ? "Read the channels again" : "Check again") : "Check the tokens");
  box.append(el("h2", {}, sl.set ? "Change the Slack app" : "Set up the Slack app"),
    el("p", {}, "The server has its own Slack app. hi writes the manifest for it. hi sends approvals, warnings and reports to one private channel. Approvers act from that channel."),
    el("ol", { class: "how" },
      el("li", {}, "Click ", el("a", { class: "button-like rainbow", href: sl.manifest_url, target: "_blank", rel: "noopener noreferrer" }, "Create the Slack app ↗"), ". Slack opens with the manifest filled in. Pick the workspace, then click ", el("strong", {}, "Create"), "."),
      el("li", {}, "On the page that Slack opens, go to ", el("strong", {}, "Basic Information → App-Level Tokens"), ". Generate an app token with the connections:write scope (xapp-…)."),
      el("li", {}, "Click ", el("strong", {}, "Install to Workspace"), ", and allow it. Copy the bot token from ", el("strong", {}, "OAuth & Permissions"), " (xoxb-…)."),
      el("li", {}, "Make a private channel for approvals. Invite the app with ", el("code", {}, "/invite @" + (st.user || "hi server")), " in the channel. Then check the tokens and pick the channel.")),
    el("details", { class: "small" }, el("summary", { class: "muted" }, "Manifest to paste by hand (api.slack.com/apps → Create New App → From a manifest → JSON)"),
      el("div", { class: "data-actions" }, el("button", { type: "button", class: "small", onclick: () => copyText(sl.manifest) }, "Copy the manifest")),
      el("pre", { class: "job-output manifest" }, sl.manifest || "")),
    field("App token", app), field("Bot token", bot), el("div", { class: "data-actions" }, check));
  if (st.checked) {
    // Without channels:read and groups:read the app can't list them: its ID, then.
    let pick;
    if (st.channels) {
      pick = el("select", { "aria-label": "Approvals channel" }, el("option", { value: "" }, "Choose a channel"),
        ...st.channels.map((ch) => el("option", { value: ch.id, selected: ch.id === st.channel }, (ch.private ? "🔒 " : "#") + ch.name)));
      if (st.channel && !st.channels.some((ch) => ch.id === st.channel)) pick.append(el("option", { value: st.channel, selected: true }, st.channel));
      pick.addEventListener("change", () => { st.channel = pick.value; });
    } else {
      pick = input({ placeholder: "C0123456789", "aria-label": "Approvals channel ID", maxlength: "20" });
      pick.value = st.channel || "";
      pick.addEventListener("input", () => { st.channel = pick.value.trim(); });
    }
    const connect = el("button", { type: "button", class: "primary", onclick: async () => {
      if (!st.channel) { error.textContent = st.channels ? "Choose the channel for approvals." : "Type the channel ID, for example C0123456789."; return; }
      connect.disabled = true; error.textContent = "";
      try {
        const a = (await api("/api/org", Object.assign({ what: "slack", action: "set", channel: st.channel }, tokens()))).answer || {};
        slackSetup = null;
        toast(a.text || "Slack is set up.");
        await Promise.all([refreshState(), refreshOrg(), refreshOrgSettings()]);
      } catch (e) { if (!(e instanceof SignIn)) { st.error = e.message; error.textContent = e.message; } connect.disabled = false; }
    } }, "Connect");
    box.append(el("p", { class: "muted small" }, "Both tokens work, in the workspace " + (st.team || "?") + " as @" + (st.user || "?") + ". " +
      (!st.channels ? "" : st.channels.length ? "The app is in " + st.channels.length + " channels." : "The app is in no channel yet. Invite it, then read the channels again.")),
      st.problem ? el("p", { class: "warn small" }, st.problem) : null,
      field(st.channels ? "Approvals channel" : "Approvals channel ID", pick, (st.channels ? "" : "Find the ID at the bottom of the channel details in Slack. ") + "Connect posts a message in the channel. After that, the server sends its approvals there."),
      el("div", { class: "data-actions" }, connect, sl.set ? el("button", { type: "button", onclick: () => { slackSetup = null; renderOrgSection(); } }, "Cancel") : null));
  }
  box.append(error);
  return box;
}

function orgWalls(s) {
  const wForm = inlineForm([
    { name: "name", placeholder: "Where the screen is, for example Office TV", required: true, max: "40" },
    { name: "full", type: "checkbox", label: "Show full names, not initials" },
    { name: "reasons", type: "checkbox", label: "Show the reason for each machine request" },
  ], async (v) => {
    try {
      const answer = await api("/api/walls", { action: "add", name: v.name, full: v.full, reasons: v.reasons });
      showLink("A wall link for " + v.name, answer.text, answer.link);
      refreshOrgSettings();
    } catch (e) { if (!(e instanceof SignIn)) toast(e.message, true); return false; }
  }, "Make the link");
  const walls = el("ul", { class: "list" });
  for (const w of s.walls || []) {
    walls.append(el("li", {}, el("span", { class: "what" }, el("strong", {}, w.name), el("span", { class: "muted small block" }, (w.full ? "names in full" : "initials") + (w.reasons ? ", with reasons" : "") + " · by " + (w.by || "?") + " " + ago(w.made) + (w.seen ? " · open " + ago(w.seen) : " · not opened yet"))),
      el("span", { class: "actions" }, el("button", { class: "danger", onclick: async () => { if (await ask({ title: "End the wall link for " + w.name + "?", text: "The screen stops showing data immediately.", ok: "End" })) wallEnd(w.name); } }, "End"))));
  }
  if (!(s.walls || []).length) walls.append(el("li", { class: "plain muted small" }, "None. A screen opens a wall link in a browser. The link is read-only."));
  return [el("p", { class: "purpose" }, "Show Running and Requests on a screen. The link is read-only. It works until an admin ends it."), heading("Wall screens", plus("Make a wall link for a screen", wForm)), wForm, walls];
}

async function wallEnd(name) {
  try { toast((await api("/api/walls", { action: "end", name })).text); refreshOrgSettings(); } catch (e) { if (!(e instanceof SignIn)) toast(e.message, true); }
}

function groupLine(g) {
  const bits = [];
  if (g.max_hours) bits.push("up to " + g.max_hours + " h for each start");
  if (g.hardware && g.hardware.length) bits.push("only " + g.hardware.join(", "));
  if (g.auto_approve) bits.push("auto-approved up to " + dollars(g.auto_approve.max_price_per_hour) + "/h for " + g.auto_approve.max_hours + " h");
  if (g.user_monthly_budget_usd) bits.push(dollars(g.user_monthly_budget_usd) + " for each user each month");
  if (g.group_monthly_budget_usd) bits.push(dollars(g.group_monthly_budget_usd) + " for the group each month");
  return bits.join(", ") || "no limits. A person approves each start";
}

// People: everyone with a level in the apps. The server's users are the server's.
function renderPeople() {
  $("people-apps").replaceChildren(peopleAppsBlock());
}
$("people-search").addEventListener("input", renderPeople);
$("people-add").addEventListener("click", () => openPerson("new"));

// Keys: the machine's, set or not, never shown. The server's are the server's.
function renderKeys() {
  const parts = [state && state.org ? vaultLine() : vaultBlock(false), machineKeysBlock(), mailBlock()];
  if (state && state.org) parts.push(el("p", { class: "muted small" }, "The server keys, for compute providers and hi q, are in ", el("button", { class: "link-button", "data-nav": "", onclick: () => openOrg("keys") }, "Server > Keys"), "."));
  $("keys-view").replaceChildren(...parts);
}

// ---------------------------------------------------------------------------
// Budgets and approval, in the middle: each group's people and this
// month's spend, its limits and budgets, and what is approved without a
// person, with what the last 30 days' requests say a rule approves. The
// server checks the policy before it's saved.

let policyDraft = null;
let policyEdited = false;
let policyText = false;

function openPolicy() { openOrg("policy"); }

function hoursText(h) {
  h = Number(h) || 0;
  if (h < 1) return Math.round(h * 60) + " min";
  return (Number.isInteger(h) ? h : h.toFixed(1)) + " h";
}
// policyCovers says whether an auto-approval covers a request the like
// of one asked (budgets aside).
function policyCovers(auto, a) {
  return !!auto && a.rate <= (auto.max_price_per_hour || 0) + 1e-9 && a.hours <= (auto.max_hours || 0) + 1e-9;
}
const byPolicy = (a) => (a.by || "").startsWith("policy");
const byPerson = (a) => !!a.by && !byPolicy(a);
// policyAsked is a group's requests of the last 30 days.
function policyAsked(spend, name) { return ((spend && spend.asked) || []).filter((a) => a.group === name); }

function policyRuleWords(g) {
  const a = g.auto_approve;
  if (!a) return "A person must approve each request.";
  return "A request for up to " + dollars(a.max_price_per_hour) + "/h and up to " + hoursText(a.max_hours) + " starts without approval" +
    (g.user_monthly_budget_usd || g.group_monthly_budget_usd ? ", if the user and group are under their monthly budgets" : "") + ". A larger request waits for a person.";
}

function policyPreviewWords(name, g, spend) {
  const asked = policyAsked(spend, name);
  if (!asked.length) return "No requests from this group in the last 30 days.";
  const people = asked.filter(byPerson).length;
  const covered = asked.filter((a) => policyCovers(g.auto_approve, a)).length;
  let text = "Last 30 days: " + asked.length + (asked.length === 1 ? " request" : " requests") + ". A person decided " + people + " of them. ";
  text += g.auto_approve ? "This rule auto-approves " + covered + " of the " + asked.length + "." : "There is no auto-approval, so a person decides all of them.";
  return text;
}

function renderPolicy() {
  const box = $("policy");
  const p = orgSettings && orgSettings.policy;
  if (!p) { box.replaceChildren(el("p", { class: "muted" }, "…")); refreshOrgSettings(); return; }
  if (!policyDraft) {
    try { policyDraft = JSON.parse(JSON.stringify(p.policy || JSON.parse(p.exists && p.text ? p.text : p.example))); } catch (e) { policyDraft = { groups: {} }; }
    if (!policyDraft.groups) policyDraft.groups = {};
  }
  const spend = p.spend || { users: {}, groups: {}, asked: [] };
  const changed = () => { policyEdited = true; };
  const parts = [orgHead()];
  if (orgReadOnlyNote()) parts.push(orgReadOnlyNote());
  if (!p.exists) parts.push(el("p", { class: "muted small" }, "No policy yet. This is an example to start from."));
  parts.push(el("p", { class: "purpose" }, "Monthly budgets, limits and auto-approval for each group."));
  if (p.error) parts.push(el("p", { class: "error" }, "hi cannot read the policy on the machine: " + p.error));
  // Where the approvals come from: the groups a person still decides for.
  const waiting = Object.keys(policyDraft.groups).map((name) => [name, policyAsked(spend, name).filter((a) => byPerson(a) && !policyCovers(policyDraft.groups[name].auto_approve, a)).length]).filter(([, n]) => n > 0);
  if (waiting.length) {
    parts.push(el("p", { class: "warn small" }, "In the last 30 days, a person decided these requests: " + waiting.map(([name, n]) => n + " from " + name).join(", ") + ". The rules below still send them to a person."));
  }
  const outside = Object.entries(spend.users || {}).filter(([, u]) => u.group && !policyDraft.groups[u.group]).map(([name, u]) => name + " (" + u.group + ")");
  if (outside.length) parts.push(el("p", { class: "muted small" }, "These users are in a group that is not in the policy. A person approves all their requests: " + outside.join(", ") + "."));
  const error = el("p", { class: "error", id: "policy-error" });
  if (policyText) {
    const area = el("textarea", { id: "policy-text", class: "policy-text", rows: "24", spellcheck: "false", "aria-label": "Policy as JSON" });
    area.value = JSON.stringify(policyDraft, null, 2);
    area.addEventListener("input", changed);
    parts.push(area);
  } else {
    for (const [name, g] of Object.entries(policyDraft.groups)) parts.push(policyGroup(name, g, spend, changed));
    const add = el("form", { class: "say-form", autocomplete: "off" },
      el("input", { id: "policy-new-group", maxlength: "32", placeholder: "Name of a new group, for example students", "aria-label": "Name of a new group" }),
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
    el("button", { "data-nav": "", onclick: () => { if (policyText) { try { policyDraft = JSON.parse($("policy-text").value); } catch (e) { error.textContent = "This is not valid JSON: " + e.message; return; } } policyText = !policyText; renderPolicy(); } }, policyText ? "As a form" : "As text"),
    el("button", { onclick: () => { policyDraft = null; policyEdited = false; renderPolicy(); } }, "Undo my changes")));
  parts.push(el("div", { class: "how muted small" }, el("h3", {}, "How it works"), el("ul", {},
    el("li", {}, "A request inside the auto-approval of its group starts immediately, if the user and group are under budget."),
    el("li", {}, "Other requests wait for an approver, in Slack and in Requests."),
    el("li", {}, "A budget never stops a machine that runs. When a user or group goes over budget, hi sends a warning, and new requests go to a person."),
    el("li", {}, "A change applies to the next request."))));
  box.replaceChildren(...parts);
}

function policyGroup(name, g, spend, changed) {
  const card = el("div", { class: "card-box policy-group" });
  const words = el("p", { class: "small pad policy-words" });
  const preview = el("p", { class: "muted small pad" });
  const budgets = el("div", { class: "meters pad policy-spend" });
  const members = Object.entries(spend.users || {}).filter(([, u]) => u.group === name).sort((a, b) => b[1].spend - a[1].spend);
  const drawSpend = () => {
    const groupSpend = (spend.groups || {})[name] || 0;
    const rows = [g.group_monthly_budget_usd
      ? meter("The group this month", groupSpend / g.group_monthly_budget_usd, dollars(groupSpend) + " of " + dollars(g.group_monthly_budget_usd))
      : el("div", { class: "meter" }, el("div", { class: "meter-head" }, el("span", {}, "The group this month"), el("span", { class: "muted small" }, dollars(groupSpend) + ", no group budget")))];
    for (const [user, u] of members.slice(0, 12)) {
      rows.push(g.user_monthly_budget_usd
        ? meter(user, u.spend / g.user_monthly_budget_usd, dollars(u.spend) + " of " + dollars(g.user_monthly_budget_usd))
        : el("div", { class: "meter" }, el("div", { class: "meter-head" }, el("span", {}, user), el("span", { class: "muted small" }, dollars(u.spend)))));
    }
    if (members.length > 12) rows.push(el("p", { class: "muted small" }, "And " + (members.length - 12) + " more. See them in Users."));
    budgets.replaceChildren(...rows);
  };
  const redraw = () => { words.textContent = policyRuleWords(g); preview.textContent = policyPreviewWords(name, g, spend); drawSpend(); changed(); };
  const num = (label, get, set, step, hint) => {
    const input = el("input", { type: "number", min: "0", step: step || "any", value: get() ?? "", placeholder: hint || "none", "aria-label": name + ": " + label });
    input.addEventListener("input", () => { set(input.value === "" ? undefined : Number(input.value)); redraw(); });
    return el("label", { class: "field" }, el("span", { class: "muted small" }, label), input);
  };
  const setKey = (key) => (value) => { if (value === undefined || value === 0) delete g[key]; else g[key] = value; };
  const hardware = el("input", { value: (g.hardware || []).join(", "), placeholder: "any", "aria-label": name + ": only this hardware" });
  hardware.addEventListener("input", () => { const list = hardware.value.split(/[\s,]+/).filter(Boolean); if (list.length) g.hardware = list; else delete g.hardware; changed(); });
  const auto = el("input", { type: "checkbox", "aria-label": name + ": auto-approve" });
  auto.checked = !!g.auto_approve;
  const autoFields = el("div", { class: "policy-fields", hidden: !g.auto_approve },
    num("Up to $ per hour", () => g.auto_approve && g.auto_approve.max_price_per_hour, (v) => { g.auto_approve = Object.assign({}, g.auto_approve, { max_price_per_hour: v || 0 }); }, "0.01"),
    num("Up to hours for each start", () => g.auto_approve && g.auto_approve.max_hours, (v) => { g.auto_approve = Object.assign({}, g.auto_approve, { max_hours: v || 0 }); }, "0.5"));
  auto.addEventListener("change", () => {
    if (auto.checked) g.auto_approve = g.auto_approve || { max_price_per_hour: 1, max_hours: 2 };
    else delete g.auto_approve;
    changed();
    renderPolicy();
  });
  // Cover what the group asked in the last 30 days, rounded up.
  const asked = policyAsked(spend, name);
  const cover = asked.length ? el("button", { class: "small", title: "Set auto-approval to the highest rate and longest start that the group asked for in the last 30 days", onclick: () => {
    const rate = Math.ceil(Math.max(...asked.map((a) => a.rate)) * 100 - 1e-6) / 100;
    const hours = Math.ceil(Math.max(...asked.map((a) => a.hours)) * 2 - 1e-6) / 2;
    g.auto_approve = { max_price_per_hour: Math.max(rate, (g.auto_approve && g.auto_approve.max_price_per_hour) || 0), max_hours: Math.max(hours, (g.auto_approve && g.auto_approve.max_hours) || 0) };
    changed();
    renderPolicy();
  } }, "Cover recent requests") : null;
  const kept = [];
  if (g.template_sources) kept.push("template sources");
  if (g.data) kept.push("data");
  card.append(el("div", { class: "card-head pad" }, el("strong", { class: "card-name" }, name),
    el("span", { class: "muted small" }, members.length + (members.length === 1 ? " user" : " users")), el("span", { class: "grow" }),
    el("button", { class: "small danger", onclick: () => { delete policyDraft.groups[name]; changed(); renderPolicy(); } }, "Remove the group")));
  card.append(budgets);
  card.append(el("h3", { class: "pad policy-sub" }, "Auto-approval"));
  card.append(el("div", { class: "data-actions pad" }, el("label", { class: "check small" }, auto, " On, with these limits:"), el("span", { class: "grow" }), cover));
  card.append(autoFields, words, preview);
  card.append(el("h3", { class: "pad policy-sub" }, "Budgets and limits"));
  card.append(el("div", { class: "policy-fields" },
    num("$ for each user each month", () => g.user_monthly_budget_usd, setKey("user_monthly_budget_usd")),
    num("$ for the group each month", () => g.group_monthly_budget_usd, setKey("group_monthly_budget_usd")),
    num("Up to hours for each start", () => g.max_hours, setKey("max_hours")),
    el("label", { class: "field wide" }, el("span", { class: "muted small" }, "Only this hardware"), hardware)));
  if (kept.length) card.append(el("p", { class: "muted small pad" }, "This group also has " + kept.join(" and ") + ". hi keeps them. To change them, use As text."));
  words.textContent = policyRuleWords(g);
  preview.textContent = policyPreviewWords(name, g, spend);
  drawSpend();
  return card;
}

// approveAndAllow approves a request and widens its group's
// auto-approval to cover the like of it from now on.
async function approveAndAllow(r) {
  const rate = Math.ceil((parseFloat(String(r.rate || "").replace(/[^0-9.]/g, "")) || 0) * 100 - 1e-6) / 100;
  const hours = Math.ceil(((r.max_seconds || 0) / 3600) * 2 - 1e-6) / 2;
  const group = r.policy_group;
  const ok = await ask({ title: "Auto-approve requests like " + r.id + "?", ok: "Approve and save",
    text: "From now on, requests from the group " + group + " for up to " + dollars(rate) + "/h and up to " + hoursText(hours) + " for each start do not need a person. This applies while the user and group are under budget. A rule that already allows more does not change. You approve " + r.id + " now. To change the rule later, go to Server > Budgets and approval." });
  if (!ok) return;
  try {
    const settings = await api("/api/org/settings");
    const p = settings.policy || {};
    const policy = JSON.parse(JSON.stringify(p.policy || JSON.parse(p.exists && p.text ? p.text : p.example)));
    policy.groups = policy.groups || {};
    const g = policy.groups[group] = policy.groups[group] || {};
    const was = g.auto_approve || {};
    g.auto_approve = { max_price_per_hour: Math.max(rate, was.max_price_per_hour || 0), max_hours: Math.max(hours, was.max_hours || 0) };
    await api("/api/org", { what: "policy", text: JSON.stringify(policy, null, 2) });
    if (!policyEdited) policyDraft = null;
  } catch (e) { if (!(e instanceof SignIn)) toast("hi did not save the policy: " + e.message, true); return; }
  orgDo({ what: "decide", action: "approve", id: r.id, group: "" }, "Approved " + r.id + ". Similar requests are auto-approved from now on.");
}

async function savePolicy() {
  let text;
  if (policyText) text = $("policy-text").value;
  else text = JSON.stringify(policyDraft, null, 2);
  try {
    await api("/api/org", { what: "policy", text });
    policyEdited = false;
    policyDraft = null;
    toast("Saved. The change applies to the next request.");
    await refreshOrgSettings();
    renderPolicy();
  } catch (e) {
    if (!(e instanceof SignIn)) $("policy-error").textContent = e.message;
  }
}

// Home's card: the server's day. It opens the server, as an app's card opens
// the app.
function renderHomeOrg() {
  const box = $("home-org");
  const o = state.org;
  if (!o) { box.replaceChildren(); return; }
  const card = el("div", { class: "card-box org-card" });
  const head = el("button", { class: "card-head pad org-open", onclick: () => openOrg(""), title: "Open the server: requests, users, admins, budgets and keys" },
    el("span", { class: "dot " + (o.up ? "idle" : "bad"), "aria-hidden": "true" }), el("strong", { class: "card-name" }, "The server"),
    el("span", { class: "muted small" }, o.up ? "runs" : "does not answer"), el("span", { class: "grow" }), o.owner ? el("span", { class: "muted small" }, "owned by " + o.owner) : null);
  card.append(head);
  if (o.no_owner) card.append(el("p", { class: "warn small pad" }, "The server has no owner yet. On the machine, run ", el("code", {}, "hi server owner <Slack member ID> --email <address>")));
  if (!o.up) card.append(el("p", { class: "error small pad" }, o.error || "The server does not answer."));
  else card.append(orgStats(o));
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
  parts.push(el("p", { class: "purpose" }, "An app serves the people in one Slack channel. It has a lead, members and a project. There are ten steps, the same as in hi app new. hi saves each step, so you can stop and continue later."));
  const name = el("input", { id: "new-name", required: true, maxlength: "20", pattern: "[a-z][a-z0-9-]*", placeholder: "App name, for example payments", "aria-label": "App name", autocomplete: "off" });
  const folder = el("input", { id: "new-folder", maxlength: "300", placeholder: (newHome.folder || "~") + "/app-<name>", "aria-label": "App folder", autocomplete: "off" });
  const file = el("input", { id: "new-file", type: "file", accept: ".json,application/json", "aria-label": "Answers from a file" });
  const error = el("p", { class: "error small" });
  name.addEventListener("input", () => { folder.placeholder = (newHome.folder || "~") + "/app-" + (name.value.trim() || "<name>"); });
  const form = el("form", { class: "new-start stack", autocomplete: "off" },
    el("label", { class: "field" }, el("span", { class: "muted small" }, "Name. Use lowercase letters, digits and dashes. The folder and the Slack command get this name."), name),
    el("label", { class: "field" }, el("span", { class: "muted small" }, "Folder. The project, tasks and memory of the app go here."), folder),
    el("label", { class: "field" }, el("span", { class: "muted small" }, "Answers from a file, in the format of hi app new --from. Do not put secrets in it."), file),
    error,
    el("div", { class: "data-actions" }, el("button", { class: "primary", type: "submit", id: "new-start", disabled: true }, "Start")));
  form.addEventListener("submit", async (event) => {
    event.preventDefault();
    error.textContent = "";
    let from = null;
    if (file.files && file.files[0]) {
      try { from = JSON.parse(await file.files[0].text()); } catch (e) { error.textContent = "This file is not valid JSON: " + e.message; return; }
    }
    try {
      draft = await api("/api/new", { action: "start", name: name.value.trim(), folder: folder.value.trim(), from });
      setMiddle(":new/" + draft.name);
      newStep = 0;
      renderDraft();
    } catch (e) { if (!(e instanceof SignIn)) error.textContent = e.message; }
  });
  parts.push(el("h2", {}, "What all apps need on this machine"),
    needsBox("", (ready) => { const start = $("new-start"); if (start) start.disabled = !ready; }));
  parts.push(form);

  if (newHome.drafts.length) {
    const list = el("ul", { class: "list" });
    for (const d of newHome.drafts) {
      const done = newSteps.filter(([key]) => d.done && d.done[key]).length;
      list.append(el("li", {}, el("span", { class: "what" }, el("strong", {}, d.name), " ", el("span", { class: "muted small" }, d.job_state === "running" ? "hi is making it" : d.job_state === "failed" ? "failed. See Jobs" : done + " of 10 steps"),
        el("span", { class: "muted small block" }, "started by " + (d.by || "?") + " " + ago(d.started) + " · " + d.folder)),
        el("span", { class: "actions" }, el("button", { onclick: () => openNew(d.name) }, "Continue"),
          el("button", { class: "danger", onclick: async () => {
            if (!(await ask({ title: "Discard the new app " + d.name + "?", text: "hi deletes the answers and tokens for this app. hi did not make the app yet.", ok: "Discard" }))) return;
            try { await api("/api/new", { action: "discard", name: d.name }); loadNewHome(); } catch (e) { if (!(e instanceof SignIn)) toast(e.message, true); }
          } }, "Discard"))));
    }
    parts.push(el("h2", {}, "In progress"), list);
  }

  parts.push(el("h2", {}, "Bring an app back"));
  parts.push(el("p", { class: "muted small" }, "These app folders are on this machine, but they are not apps here now. An admin removed the app and kept the folder, or moved the app, or copied the folder from another machine. The app starts again from its folder."));
  const back = el("ul", { class: "list" });
  for (const b of newHome.back || []) {
    back.append(el("li", {}, el("span", { class: "what" }, el("strong", {}, b.name), el("span", { class: "muted small block" }, b.folder + (b.purpose ? " · " + b.purpose : ""))),
      el("span", { class: "actions" }, el("button", { onclick: () => bringBack(b.folder, b.name) }, "Bring back"))));
  }
  if (!(newHome.back || []).length) back.append(el("li", { class: "plain muted small" }, "None, other than the folders of the apps."));
  parts.push(back);
  const typed = el("input", { maxlength: "300", placeholder: "Or the full path of the app folder", "aria-label": "App folder" });
  const backForm = el("form", { class: "say-form", autocomplete: "off" }, typed, el("button", { type: "submit" }, "Bring back"));
  backForm.addEventListener("submit", (event) => { event.preventDefault(); if (typed.value.trim()) bringBack(typed.value.trim(), ""); });
  parts.push(backForm);
  box.replaceChildren(...parts);
  name.focus();
}

async function bringBack(folder, name) {
  if (!(await ask({ title: "Bring back " + (name || "the app in " + folder) + "?", text: "hi makes it an app on this machine again, with the same name, and starts it. This is the same as hi app up " + folder + ".", ok: "Bring back" }))) return;
  try { const job = await api("/api/apps", { action: "back", folder }); toast("Bringing the app back. See the output in Jobs."); followJob(job); } catch (e) { if (!(e instanceof SignIn)) toast(e.message, true); }
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
  const nav = el("div", { class: "chips new-steps", role: "group", "aria-label": "Steps" });
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
    const words = { running: "hi is making the app now. See the output in Jobs.", failed: "hi could not make the app. See the reason in Jobs. Fix the step that it names, then click Create again.", done: "The app is ready." };
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
      body.append(el("p", {}, "The lead talks with the people in the Slack channel of the app. Give it a name, for example a person's name. Or keep " + d.name + "."),
        field("Lead name", lead, "In Slack, the lead is @" + d.handle + (d.done.lead ? "" : " (after Next, it uses the name you give)") + ". Its command is " + d.slash + "."));
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
      const bot = input({ id: "s-bot", type: "password", placeholder: d.has_bot ? "Saved. Paste a new value to change it." : "xoxb-…" });
      const app = input({ id: "s-app", type: "password", placeholder: d.has_app ? "Saved. Paste a new value to change it." : "xapp-…" });
      body.append(el("p", {}, "Each app has its own Slack app. hi writes the manifest for it."),
        el("ol", { class: "how" },
          el("li", {}, "Click ", el("a", { class: "button-like rainbow", href: d.manifest_url, target: "_blank", rel: "noopener noreferrer" }, "Create the Slack app ↗"), ". Slack opens with the manifest filled in. Pick the workspace, then click ", el("strong", {}, "Create"), "."),
          el("li", {}, "On the page that Slack opens, go to ", el("strong", {}, "Basic Information → App-Level Tokens"), ". Generate an app token with the connections:write scope (xapp-…)."),
          el("li", {}, "Click ", el("strong", {}, "Install to Workspace"), ", and allow it. Copy the bot token from ", el("strong", {}, "OAuth & Permissions"), " (xoxb-…).")),
        el("details", { class: "small" }, el("summary", { class: "muted" }, "Manifest to paste by hand (api.slack.com/apps → Create New App → From a manifest → JSON)"), el("pre", { class: "job-output manifest" }, d.manifest)),
        field("App token", app), field("Bot token", bot),
        d.workspace ? el("p", { class: "muted small" }, "Both tokens work, in the workspace " + d.workspace + ".") : "");
      collect = () => ({ bot_token: bot.value.trim(), app_token: app.value.trim() });
      break;
    }
    case "channel": {
      const main = el("select", { id: "s-channel", "aria-label": "App channel" });
      const ops = el("select", { id: "s-ops", "aria-label": "Ops channel" });
      const fill = () => {
        const list = slackLists.channels || [];
        const options = (value, none) => [none ? el("option", { value: "" }, none) : el("option", { value: "" }, "Choose a channel"), ...list.map((ch) => el("option", { value: ch.id, selected: ch.id === value }, (ch.private ? "🔒 " : "#") + ch.name))];
        main.replaceChildren(...options(c.channel, ""));
        ops.replaceChildren(...options(c.ops_channel, "None. All messages go to the main channel."));
        if (c.channel && !list.some((ch) => ch.id === c.channel)) main.append(el("option", { value: c.channel, selected: true }, c.channel));
      };
      fill();
      const refresh = el("button", { type: "button", class: "small", onclick: async () => { await loadSlack("channels"); fill(); note.textContent = slackLists.error || "The app is in " + (slackLists.channels || []).length + " channels."; } }, "Read again");
      const note = el("p", { class: "muted small" }, slackLists.error || (slackLists.channels ? "The app is in " + slackLists.channels.length + " channels." : "…"));
      body.append(el("p", {}, "First, invite the Slack app to the app channel. In the channel, type ", el("code", {}, "/invite @" + d.handle), ". Then click Read again, and pick the channel."),
        el("div", { class: "data-actions" }, refresh), note,
        field("App channel", main),
        field("Ops channel, for operations and security (optional)", ops, "hi sends incidents, warnings, downtime, advisories, key changes and access changes here. Use a private channel for owners and developers. Invite the app to it too."));
      if (!slackLists.channels) loadSlack("channels").then(() => { if (key === "channel" && newSteps[newStep][0] === "channel") { fill(); note.textContent = slackLists.error || "The app is in " + slackLists.channels.length + " channels."; } });
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
      body.append(el("p", {}, "Owners build the app in the dev box and publish it from the dev page. Pick the owners from the people in the workspace."), el("div", { class: "chosen-row" }, el("span", { class: "muted small" }, "Owners: "), chips), search, list);
      draw(); drawChips();
      if (!slackLists.people) loadSlack("people").then(() => { draw(); drawChips(); });
      collect = () => ({ owners: [...chosen] });
      break;
    }
    case "members":
      collect = renderMembers(body, d);
      break;
    case "models": {
      m.shared = m.shared || [];
      let way = d.own_key ? "own" : c.openrouter_from ? "shared" : m.shared.length && !m.openrouter ? "shared" : m.openrouter ? "machine" : "own";
      const key = input({ id: "s-orkey", type: "password", placeholder: d.has_own_key ? "Saved. Paste a new value to change it." : "sk-or-…" });
      const pick = sharedKeyPicker(m.shared, c.openrouter_from, "An OpenRouter key, sk-or-…, from openrouter.ai/settings/keys.");
      const ways = { shared: el("div", { class: "field" }, el("span", { class: "muted small" }, "Shared key"), pick.box), own: field("OpenRouter key of the app", key) };
      const show = () => { for (const [k, f] of Object.entries(ways)) f.hidden = k !== way; };
      body.append(el("p", {}, "The lead and the app use models through OpenRouter" + (Object.values(c.members).some((x) => x.provider === "chatgpt" || x.agent === "codex") ? ". Roles on ChatGPT use OpenRouter as a fallback" : "") + ". With its own key, the app has separate billing and limits. A shared key or the machine key counts use together with the other apps. No box ever holds the key."),
        radios("s-or", [["machine", "The key of this machine (the Hermes key)" + (m.openrouter ? "" : ". There is none here"), !m.openrouter],
          ["shared", "A shared key from the vault. Many apps can use one key, and you change it in one place"],
          ["own", "A key for this app only (openrouter.ai/settings/keys)"]], way, (v) => { way = v; show(); if (way === "own") key.focus(); }),
        ways.shared, ways.own);
      show();
      collect = () => ({ own_key: way === "own", openrouter_key: way === "own" ? key.value.trim() : "", openrouter_from: way === "shared" ? pick.value() : "" });
      break;
    }
    case "code":
      collect = renderCode(body, d);
      break;
    case "network": {
      const nb = input({ id: "s-netbird", type: "password", placeholder: d.has_netbird ? "Saved. Paste a new value to change it." : "NetBird setup key (optional)" });
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
        if (choice === "names") extra.append(field("Production name. Stage and the dev page use names under it.", production), el("label", { class: "check small" }, https, " A proxy of your IT team handles TLS for these names, so the links use https"));
        if (choice === "host") extra.append(field("Name that your IT team keeps. It points to the NetBird address of the app.", host, "Production is http://<name>/, the preview is :8080, and the dev page is :8888."));
      };
      const options = [];
      if (m.domain) options.push(["domain", "The domain of this machine, on https: " + m.domain + " and names like it"]);
      options.push(["names", "A name for each address, kept by your IT team"], ["host", "One name kept by your IT team, with ports"], ["netbird", m.peer ? "The NetBird name, " + m.peer + ", on plain http" : "The NetBird name of the dev box, on plain http"]);
      body.append(el("p", {}, "The addresses of the app are on your NetBird network. The owners build in the dev box over SSH on that network. The dev box joins NetBird one time, with a setup key from the NetBird dashboard (Setup Keys). If you give no key now, nobody can open the addresses or the dev page until the dev box joins. To join later, give a key in Network of the app, or run hi app dev " + d.name + " netbird."),
        field("NetBird setup key", nb), el("h2", {}, "Address names"), radios("s-addr", options, choice, (v) => { choice = v; drawExtra(); }), extra);
      drawExtra();
      collect = () => ({ netbird_key: nb.value.trim(), addresses: choice, production: production.value.trim(), https: https.checked, host: host.value.trim() });
      break;
    }
    case "heartbeats": {
      const beat = input({ value: d.heartbeat || "", placeholder: "https://hc-ping.com/… (optional)" });
      const every = input({ value: c.heartbeat_every || "", placeholder: "1m" });
      const machine = input({ value: d.machine_heartbeat || "", placeholder: "https://hc-ping.com/… (optional)" });
      body.append(el("p", {}, "A heartbeat tells an outside service that the app runs. An example is healthchecks.io, which is free for 20 checks. The service sends an alert when the heartbeats stop. Make a check there. Then set who gets the alert, for example your IT team or the ops channel."),
        field("Heartbeat URL of the app", beat, "hi sends one heartbeat now, as a test."), field("Every", every, "From 30s to 1h. The default is 1m."));
      if (!m.heartbeat) body.append(field("Heartbeat URL of this machine", machine, "This machine has no heartbeat. A machine heartbeat tells that the machine is up, even if no app runs."));
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
    buttons.append(el("span", { class: "grow" }), el("button", { type: "button", onclick: () => openNew("") }, "Continue later"));
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
  body.append(el("p", {}, "Set the agent, model and effort for each role. For Hermes, also set who pays: OpenRouter, or a ChatGPT plan with OpenRouter as a fallback."));
  const signin = el("div", { class: "card-box pad stack" });
  const claude = el("div", { class: "card-box pad stack" });
  let chatgptChoice = d.chatgpt_signin || (m.chatgpt ? "machine" : "app");
  m.shared = m.shared || [];
  let claudeWay = c.claude_from ? "shared" : d.has_claude || !m.claude ? "own" : "machine";
  const claudeToken = input({ type: "password", placeholder: d.has_claude ? "Saved. Paste a new value to change it." : "sk-ant-oat…, from claude setup-token" });
  const claudePick = sharedKeyPicker(m.shared, c.claude_from, "A Claude token, sk-ant-oat…. Get it with claude setup-token, signed in to the account that pays.");
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
    signin.replaceChildren(el("strong", {}, "ChatGPT sign-in"), el("p", { class: "muted small" }, "All roles on ChatGPT use one sign-in. Use a sign-in for the app only, with a code on any device. Only the app refreshes it. Or use the Codex sign-in of this machine."),
      radios("s-chatgpt", [["app", "For the app only"], ["machine", "The Codex sign-in of this machine" + (m.chatgpt ? "" : " (not signed in)"), !m.chatgpt]], chatgptChoice, (v) => { chatgptChoice = v; drawSignins(); }));
    if (chatgptChoice === "app") {
      if (d.chatgpt) signin.append(el("p", { class: "small" }, "Signed in: " + d.chatgpt + "."));
      if (d.chatgpt_code) signin.append(el("p", {}, "Open ", el("a", { href: d.chatgpt_url, target: "_blank", rel: "noopener noreferrer" }, d.chatgpt_url + " ↗"), ". Sign in to the ChatGPT account that pays, and type ", el("code", { class: "big-code" }, d.chatgpt_code), ". This page shows when you are done."));
      if (d.chatgpt_error) signin.append(el("p", { class: "error small" }, d.chatgpt_error));
      signin.append(el("button", { type: "button", class: "small", onclick: async () => {
        try { draft = await api("/api/new", { action: "chatgpt", name: d.name }); renderDraft(); } catch (e) { if (!(e instanceof SignIn)) toast(e.message, true); }
      } }, d.chatgpt ? "Sign in again" : d.chatgpt_code ? "Get a new code" : "Sign in"));
    }
    const showClaude = () => { claudeToken.hidden = claudeWay !== "own"; claudePick.box.hidden = claudeWay !== "shared"; };
    claude.replaceChildren(el("strong", {}, "Claude"), el("p", { class: "muted small" }, "Use the sign-in of this machine, a shared key from the vault, or a token for the app only. Many apps can use one shared key. Get a token with claude setup-token, signed in to the account that pays. A token lasts one year."),
      radios("s-claude", [["machine", "The sign-in of this machine" + (m.claude ? "" : " (none here)"), !m.claude], ["shared", "A shared key from the vault"], ["own", "A token for the app only"]], claudeWay, (v) => { claudeWay = v; showClaude(); if (v === "own") claudeToken.focus(); }),
      claudePick.box, claudeToken);
    showClaude();
  };
  for (const role of roles) {
    const member = c.members[role];
    const agent = el("select", { "aria-label": role + ": agent", disabled: role === "lead" });
    for (const [v, label] of [["hermes", "Hermes"], ["claude", "Claude Code"], ["codex", "Codex"]]) agent.append(el("option", { value: v, selected: member.agent === v }, label));
    const model = input({ value: member.model || "", placeholder: member.agent === "hermes" && member.provider === "chatgpt" ? "first model of the plan" : "default of the agent", "aria-label": role + ": model" });
    const provider = el("select", { "aria-label": role + ": who pays" });
    provider.append(el("option", { value: "openrouter", selected: member.provider !== "chatgpt" }, "OpenRouter, per call"), el("option", { value: "chatgpt", selected: member.provider === "chatgpt" }, "A ChatGPT plan, with OpenRouter as fallback"));
    const effort = el("select", { "aria-label": role + ": effort" });
    for (const e of m.efforts) effort.append(el("option", { value: e, selected: (member.effort || "medium") === e }, e));
    const fallback = input({ value: member.fallback || "", placeholder: "the default", "aria-label": role + ": fallback on OpenRouter" });
    const models = el("datalist", { id: "models-" + role });
    for (const name of d.chatgpt_models || []) models.append(el("option", { value: name }));
    model.setAttribute("list", "models-" + role);
    const providerField = field("Who pays", provider);
    const sync = () => {
      providerField.hidden = agent.value !== "hermes";
      model.placeholder = agent.value === "hermes" && provider.value === "chatgpt" ? "first model of the plan" : "default of the agent";
      drawSignins();
    };
    agent.addEventListener("change", () => { model.value = ""; sync(); });
    provider.addEventListener("change", sync);
    rows[role] = { agent, model, provider, effort, fallback };
    body.append(el("div", { class: "card-box member-card" }, el("div", { class: "card-head pad" }, el("strong", { class: "card-name" }, role)),
      el("div", { class: "policy-fields" }, field("Agent", agent), field("Model", model), providerField, field("Effort", effort), field("Fallback on OpenRouter", fallback, "Only if OpenRouter does not have the default model")), models));
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
    return { members, chatgpt_signin: chatgptChoice, claude_token: claudeWay === "own" ? claudeToken.value.trim() : "", claude_machine: claudeWay === "machine", claude_from: claudeWay === "shared" ? claudePick.value() : "" };
  };
}

// Code: what the project starts from.
function renderCode(body, d) {
  const code = Object.assign({}, d.code);
  const m = d.machine;
  const options = [["new", "A new web app: a blank page on Bun, with a database, tests and the helper chat"], ["empty", "A repository with only a README"]];
  for (const t of m.templates) options.push(["template:" + t.name, t.name + (t.summary ? ": " + t.summary : "")]);
  options.push(["adopt", "Adopt a project, for example github.com/owner/name, or a folder on this machine"]);
  let kind = code.kind === "template" ? "template:" + code.template : code.kind;
  const source = input({ value: code.source || "", placeholder: "github.com/owner/name, a URL, or a folder path" });
  const ref = input({ value: code.ref || "", placeholder: "default branch" });
  const shallow = el("input", { type: "checkbox" }); shallow.checked = !!code.shallow;
  const push = el("input", { type: "checkbox", id: "s-push" }); push.checked = !!code.push;
  push.addEventListener("change", () => drawForge());
  const nosurvey = el("input", { type: "checkbox" }); nosurvey.checked = !!code.no_survey;
  // Where the project is kept too: nowhere else, GitHub, or GitLab.
  let forge = code.gitlab ? "gitlab" : code.github ? "github" : "";
  const repoName = input({ value: code.gitlab || code.github || "", "aria-label": "Repository" });
  const forgeBox = el("div", { class: "stack" });
  const drawForge = () => {
    const push = kind === "adopt" && d.look && d.look.can_push && document.querySelector("#s-push") && document.querySelector("#s-push").checked;
    forgeBox.replaceChildren(el("h2", {}, "Also keep it on GitHub or GitLab"),
      radios("s-forge", [["", "No. It stays on this machine"], ["github", "GitHub: a new private repository, with the GitHub sign-in (gh) of this machine" + (m.gh ? "" : ". gh is not installed. Run hi install gh"), !m.gh], ["gitlab", "GitLab (gitlab.com or your own): a new private project, with the GitLab sign-in (glab) of this machine" + (m.glab ? "" : ". glab is not installed. Run hi install glab, then glab auth login"), !m.glab]], forge, (v) => { forge = v; drawForge(); }));
    if (forge === "github") { repoName.placeholder = "owner/name, for example octocat/world-clock"; forgeBox.append(field("Repository", repoName, "hi makes it if it does not exist. hi uses an empty one that exists. The app pushes each merge to it.")); }
    if (forge === "gitlab") { repoName.placeholder = "group/name on gitlab.com, or host/group/name, for example gitlab.example.com/team/billing"; forgeBox.append(field("Project", repoName, "hi makes it if it does not exist. hi uses an empty one that exists. The app pushes each merge to it.")); }
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
    fact("Project", l.folder || l.github || l.gitlab || l.source);
    fact("About", l.description);
    fact("Size", l.size_mb ? l.size_mb + " MB" + (l.files ? ", " + l.files + " files" : "") : l.files ? l.files + " files" : "");
    fact("Branch", l.branch);
    fact("Licence", l.licence || (l.github ? "not given" : ""));
    look.append(el("div", { class: "card-box pad" }, facts,
      l.archived ? el("p", { class: "warn small" }, "The project is archived. Nobody works on it now.") : null,
      l.private ? el("p", { class: "muted small" }, "The project is private. hi gets it with the " + (l.gitlab ? "GitLab sign-in (glab)" : "GitHub sign-in") + " of this machine.") : null,
      l.folder ? el("p", { class: "muted small" }, "hi copies the folder without its history or secrets. hi does not change the folder.") : null));
  };
  const drawAdopt = () => {
    adoptBox.hidden = kind !== "adopt";
    const l = d.look && d.look.source === source.value.trim() ? d.look : null;
    adoptBox.replaceChildren(field("Project", source), el("div", { class: "data-actions" }, el("button", { type: "button", class: "small", onclick: lookNow }, "Check it")), look);
    if (l && !l.folder && !l.error) {
      adoptBox.append(field("Branch, tag, or commit (optional)", ref));
      if (l.big) adoptBox.append(el("label", { class: "check small" }, shallow, " Only the latest commit. The project is over 1 GB. With the full history, the members can see why the code is as it is."));
      else adoptBox.append(el("label", { class: "check small" }, shallow, " Only the latest commit (smaller and faster; the copy stays on this machine)"));
      if (l.can_push) adoptBox.append(el("label", { class: "check small" }, push, " Push to it. The " + (l.gitlab ? "GitLab sign-in (glab)" : "GitHub sign-in") + " of this machine can push there. The app pushes its merges, the branch production, and the tags release-1, release-2, …"));
    }
    if (l && !l.error) adoptBox.append(el("label", { class: "check small" }, nosurvey, " Skip the first task (the survey that makes the project run and maps it)"));
    drawLook();
  };
  const lookNow = async () => {
    try { draft = await api("/api/new", { action: "look", name: d.name, source: source.value.trim() }); d.look = draft.look; drawAdopt(); } catch (e) { if (!(e instanceof SignIn)) $("step-error").textContent = e.message; }
  };
  source.addEventListener("keydown", (event) => { if (event.key === "Enter") { event.preventDefault(); lookNow(); } });
  body.append(el("p", {}, "The app keeps a copy of the project in " + d.folder + "/repo."), radios("s-code", options, kind, (v) => { kind = v; drawAdopt(); drawForge(); }), adoptBox, forgeBox);
  drawAdopt();
  drawForge();
  return () => {
    const answer = { kind: kind.startsWith("template:") ? "template" : kind, template: kind.startsWith("template:") ? kind.slice(9) : "", source: source.value.trim(), ref: ref.value.trim(), shallow: shallow.checked, push: push.checked, no_survey: nosurvey.checked, github: forge === "github" ? repoName.value.trim() : "", gitlab: forge === "gitlab" ? repoName.value.trim() : "" };
    return { code: answer };
  };
}

// What an app needs on this machine (hi_admin.md, release 8): each tool,
// with Install, and Install all; what needs root takes the machine's
// sudo password, which the page passes to that install alone. ready says
// whether nothing is missing, each time the list is drawn.
function needsBox(name, ready) {
  const box = el("div", { class: "needs" }, el("p", { class: "muted small" }, "Checking what apps need on this machine…"));
  let busy = "";
  const draw = async () => {
    let view;
    try { view = await api("/api/needs" + (name ? "?name=" + encodeURIComponent(name) : "")); } catch (e) {
      // The server checks again at Start and Create: the buttons stay usable.
      if (!(e instanceof SignIn)) box.replaceChildren(el("p", { class: "error small" }, e.message));
      if (ready) ready(true);
      return;
    }
    const missing = view.needs.filter((n) => !n.present);
    const password = missing.some((n) => n.root) ? input({ type: "password", placeholder: "sudo password of the account that runs hi", "aria-label": "sudo password of the machine", autocomplete: "new-password" }) : null;
    const error = el("p", { class: "error small" });
    const install = async (ids) => {
      error.textContent = "";
      const root = view.needs.some((n) => ids.includes(n.id) && n.root);
      if (root && password && !password.value) { error.textContent = "This install needs root. Type the sudo password."; password.focus(); return; }
      try {
        const job = await api("/api/needs", { ids, password: root && password ? password.value : "" });
        busy = job.id;
        toast("Installing. See the output in Jobs.", true);
        draw();
        const wait = async () => {
          let answer;
          try { answer = await api("/api/job?id=" + encodeURIComponent(job.id) + "&from=0"); } catch (e) { setTimeout(wait, 2000); return; }
          if (answer.job.state === "running") { setTimeout(wait, 2000); return; }
          busy = "";
          if (answer.job.state === "failed") toast("The install failed. See the reason in Jobs.", true);
          draw();
        };
        setTimeout(wait, 2000);
      } catch (e) { if (!(e instanceof SignIn)) error.textContent = e.message; }
    };
    const rows = view.needs.map((n) => el("li", {},
      el("span", { class: "what" }, el("span", { class: "dot " + (n.present ? "idle" : "bad"), "aria-hidden": "true" }), " ", el("strong", {}, n.title), " ",
        el("span", { class: "muted small" }, n.for + (n.present ? "" : n.root ? " · missing, needs the sudo password" : " · missing"))),
      n.present || busy ? "" : el("button", { class: "small", onclick: () => install([n.id]) }, "Install")));
    const parts = [el("ul", { class: "list needs-list" }, ...rows)];
    if (busy) parts.push(el("p", { class: "muted small" }, "Installing. See the progress in Jobs. This list checks again when the install is done."));
    else if (missing.length) {
      if (password) parts.push(field("Some tools need root. Type the sudo password of the account that runs hi. hi uses it for this install only, and does not keep it.", password));
      if (missing.length > 1) parts.push(el("div", { class: "data-actions" }, el("button", { class: "primary", onclick: () => install(missing.map((n) => n.id)) }, "Install all")));
      parts.push(error);
    }
    box.replaceChildren(...parts);
    if (ready) ready(missing.length === 0 && !busy);
  };
  draw();
  return box;
}

function renderReview(body, d) {
  const c = d.config;
  const facts = el("dl", { class: "facts" });
  const fact = (t, v) => facts.append(el("dt", {}, t), el("dd", {}, v || "—"));
  fact("Name", d.name + " · @" + d.handle + " · " + d.slash);
  fact("Folder", d.folder);
  fact("Purpose", c.purpose);
  fact("Slack", d.workspace ? "workspace " + d.workspace : "");
  fact("Channel", c.channel + (c.ops_channel ? ", ops " + c.ops_channel : ""));
  fact("Owners", (c.owners || []).join(", "));
  for (const role of Object.keys(c.members).sort((a, b) => (a === "lead" ? -1 : b === "lead" ? 1 : a.localeCompare(b)))) {
    const x = c.members[role];
    fact(role, x.agent + (x.model ? " · " + x.model : "") + (x.provider === "chatgpt" ? " · on ChatGPT" : "") + (x.effort ? " · " + x.effort : "") + (x.fallback ? " · fallback " + x.fallback : ""));
  }
  fact("OpenRouter", d.own_key ? "a key for this app only" : c.openrouter_from ? c.openrouter_from + ", a shared key from the vault" : "the key of this machine");
  fact("Code", d.code.kind === "adopt" ? "adopts " + d.code.source + (d.code.push ? ", and pushes to it" : "") : d.code.kind === "template" ? "the template " + d.code.template : d.code.kind === "empty" ? "an empty repository" : "a new web app");
  if (d.code.github) fact("GitHub", d.code.github);
  if (d.code.gitlab) fact("GitLab", d.code.gitlab);
  fact("Network", (d.has_netbird ? "joins NetBird now" : "joins NetBird later. The addresses do not work until it joins, with a setup key in Network") + ". Addresses: " + (c.addresses || "on the domain of this machine"));
  fact("Heartbeat", d.heartbeat || "none");
  body.append(el("p", {}, "Create makes the app with these answers, the same as hi app new, then starts it. See the progress in Jobs. The app opens here when it is up."), facts);
  const create = el("button", { class: "primary", disabled: true, onclick: async () => {
    create.disabled = true;
    try {
      const job = await api("/api/new", { action: "create", name: d.name });
      toast("Creating " + d.name + ". See the output in Jobs. The app opens here when it is up.", true);
      creating = { job: job.id, name: d.name };
      followJob(job);
      loadDraft(d.name, false);
    } catch (e) { create.disabled = false; if (!(e instanceof SignIn)) $("step-error").textContent = e.message; }
  } }, "Create " + d.name);
  body.append(el("h2", {}, "What the app needs on this machine"),
    needsBox(d.name, (ready) => { create.disabled = !ready || d.job_state === "running"; }));
  body.append(el("div", { class: "data-actions" }, el("button", { onclick: () => { newStep = newSteps.length - 1; renderDraft(); } }, "Back"), create));
}
let creating = null;

// Rename and Remove, from an app's Overview.
async function renameApp(app) {
  const answer = await ask({ title: "Rename " + app + "?", text: "The Slack bot, command and service get the new name. The folder also gets it, if the folder has the app name. Memory, tasks and snapshots stay. The app stops during the rename, then starts again. After the rename, update the manifest of the Slack app. Slack shows the new name only after that. Type the app name to continue.", ok: "Rename", field: "New name", fieldCheck: (v) => (/^[a-z][a-z0-9-]{0,19}$/.test(v) && v !== app ? "" : "Give a new name. Use lowercase letters, digits and dashes, up to 20."), typed: app });
  if (!answer) return;
  try { const job = await api("/api/apps", { action: "rename", app, to: answer.field }); toast("Renaming " + app + " to " + answer.field + ". See the output in Jobs."); creating = { job: job.id, name: answer.field }; followJob(job); } catch (e) { if (!(e instanceof SignIn)) toast(e.message, true); }
}
async function removeApp(app) {
  const answer = await ask({ title: "Remove " + app + "?", text: "The app stops. hi deletes its service, its name on this machine and the boxes of its members. The snapshots stay. The Slack app, the channel and any GitHub repository also stay. Type the app name to continue.", ok: "Remove", typed: app, check: "Keep the folder, so the app can come back (Bring an app back)", checked: true });
  if (!answer) return;
  try { const job = await api("/api/apps", { action: "remove", app, keep_folder: answer.checked }); toast("Removing " + app + ". See the output in Jobs."); followJob(job); openView(""); } catch (e) { if (!(e instanceof SignIn)) toast(e.message, true); }
}

// ---------------------------------------------------------------------------
// An app's sections: People, Agents, Slack, and Settings. What's shown is
// read from the machine, never a secret's value; each change runs the hi
// app command, and the app's ops channel hears it.

let appSection = "overview";
let settings = null; // /api/app/settings for the open app

for (const chip of document.querySelectorAll("#sections [data-section]")) {
  chip.addEventListener("click", () => showSection(chip.dataset.section));
}

function showSection(section) {
  if (section !== "terminal") closeTerm();
  appSection = section;
  for (const chip of document.querySelectorAll("#sections [data-section]")) chip.setAttribute("aria-pressed", String(chip.dataset.section === section));
  if (openApp) history.replaceState(null, "", "#" + encodeURIComponent(openApp) + (section === "overview" ? "" : "/" + section));
  if (section === "overview") { if (detail) renderOverview(); else refreshDetail(); return; }
  if (settings && settings.name === openApp && settings._section === sectionSource(section)) renderSection(); else $("overview").replaceChildren(el("p", { class: "muted" }, "…"));
  refreshSettings();
}

// sectionSource is where a section reads from.
function sectionSource(section) { return { network: "/api/app/network", data: "/api/app/data", terminal: "/api/app/terminal", spend: "/api/app/spend" }[section] || "/api/app/settings"; }

async function refreshSettings() {
  if (!openApp || appSection === "overview") return;
  const name = openApp;
  try {
    const source = sectionSource(appSection);
    const fresh = await api(source + "?name=" + encodeURIComponent(name) + (appSection === "spend" && spendMonth ? "&month=" + spendMonth : ""));
    if (openApp !== name || sectionSource(appSection) !== source) return;
    fresh._section = source;
    const same = settings && JSON.stringify(settings) === JSON.stringify(fresh);
    settings = fresh;
    const typing = document.activeElement && $("overview").contains(document.activeElement) && /INPUT|TEXTAREA|SELECT/.test(document.activeElement.tagName);
    if (!same && !typing) renderSection();
  } catch (e) { if (!(e instanceof SignIn) && openApp === name) $("overview").replaceChildren(el("p", { class: "error" }, e.message)); }
}

// change sends one change, and shows what the command said.
async function change(what, fields, done) {
  try {
    const answer = await api("/api/app/settings", Object.assign({ app: openApp, what }, fields));
    if (answer.job) { toast((done || "Started") + ". See the output in Jobs.", true); followJob(answer.job); }
    else toast(done || firstLine(answer.text) || "Done.", true);
    await refreshSettings();
    return true;
  } catch (e) {
    if (!(e instanceof SignIn)) toast(e.message, true);
    return false;
  }
}
const firstLine = (text) => String(text || "").split("\n")[0];

function renderSection() {
  const s = settings;
  const box = $("overview");
  const head = el("div", { class: "admin-head" }, el("h1", { class: "admin-title" }, s.name), el("span", { class: "muted" }, { people: "People", agents: "Agents", slack: "Slack", settings: "Settings", network: "Network", data: "Data", spend: "Spend", terminal: "Terminal" }[appSection]));
  const parts = [head];
  if (appSection === "people") parts.push(...sectionPeople(s));
  if (appSection === "agents") parts.push(...sectionAgents(s));
  if (appSection === "slack") parts.push(...sectionSlack(s));
  if (appSection === "settings") parts.push(...sectionSettings(s));
  if (appSection === "network") parts.push(...sectionNetwork(s));
  if (appSection === "data") parts.push(...sectionData(s));
  if (appSection === "spend") parts.push(...sectionSpend(s));
  if (appSection === "terminal") parts.push(...sectionTerminal(s));
  box.replaceChildren(...parts);
}

// ---------------------------------------------------------------------------
// an app's Spend (hi_model_spend.md): what its model calls cost through
// hi this month, or another, by who, model, and day, and the plans' calls
// in tokens.

let spendMonth = "";
const tokens = (n) => (n = Number(n) || 0) >= 1e6 ? (n / 1e6).toFixed(1) + "M" : n >= 1e3 ? Math.round(n / 1e3) + "k" : String(n);
const monthName = (m) => { const [y, mo] = m.split("-"); return new Date(Date.UTC(+y, +mo - 1, 1)).toLocaleDateString(undefined, { month: "long", year: "numeric", timeZone: "UTC" }); };

// spendBudget is the app's budget: how much of it is spent, and, for the
// machine's admins, a form to change it.
function spendBudget(s) {
  const b = s.budget || {};
  const box = el("div", { class: "machine-block" });
  if (b.monthly) box.append(meter("This month", b.month_usd / b.monthly, dollars(b.month_usd) + " of " + dollars(b.monthly)));
  if (b.daily) box.append(meter("Today", b.day_usd / b.daily, dollars(b.day_usd) + " of " + dollars(b.daily)));
  if (!b.monthly && !b.daily) box.append(el("p", { class: "muted small" }, "No budget. hi does not stop paid calls of the app at any amount."));
  if (b.stopped) box.append(el("p", { class: "warn small" }, b.message));
  if (!s.may_change) return box;
  const monthly = el("input", { type: "number", min: "0", step: "1", placeholder: "Monthly, $", value: b.monthly || "", "aria-label": "Monthly budget in dollars" });
  const daily = el("input", { type: "number", min: "0", step: "1", placeholder: "Daily, $ (optional)", value: b.daily || "", "aria-label": "Daily budget in dollars" });
  const form = el("form", { class: "signin-row spend-budget", autocomplete: "off" }, monthly, daily, el("button", { class: "small", type: "submit" }, "Set budget"));
  form.addEventListener("submit", async (event) => {
    event.preventDefault();
    try {
      const answer = await api("/api/app/spend", { name: s.name, monthly: Number(monthly.value) || 0, daily: Number(daily.value) || 0 });
      toast(answer.text, true);
      refreshSettings();
    } catch (e) { if (!(e instanceof SignIn)) toast(e.message, true); }
  });
  box.append(form, el("div", { class: "how muted small" }, el("h3", {}, "How it works"), el("ul", {},
    el("li", {}, "At the budget, hi stops the paid calls of the app, from its agents and its code."),
    el("li", {}, "Calls that a Claude or ChatGPT plan pays for continue."),
    el("li", {}, "hi tells the ops channel at 80% and at 100%."),
    el("li", {}, "Empty or 0 means no budget."))));
  return box;
}

function sectionSpend(s) {
  const sp = s.spend;
  const parts = [el("p", { class: "purpose" }, "The cost of model calls that the app makes through hi. This includes calls from its agents, its dev box, and its code in production and the preview, with any key. hi counts calls on a Claude or ChatGPT sign-in in tokens, not dollars.")];
  const months = sp.months.length ? sp.months : [sp.month];
  if (!months.includes(sp.month)) months.unshift(sp.month);
  const pick = el("select", { "aria-label": "Month" }, ...months.map((m) => el("option", { value: m, selected: m === sp.month }, monthName(m))));
  pick.addEventListener("change", () => { spendMonth = pick.value; refreshSettings(); });
  parts.push(el("div", { class: "data-actions spend-head" }, el("strong", { class: "spend-total" }, dollars(sp.usd)), el("span", { class: "muted small" }, sp.calls + (sp.calls === 1 ? " call" : " calls")), el("span", { class: "grow" }), pick));
  parts.push(spendBudget(s));
  if (!sp.calls) {
    parts.push(el("p", { class: "muted small" }, "No model calls through hi this month yet. hi counts the calls of an agent after its box starts again on this version of hi."));
    return parts;
  }
  const table = (title, rows, money, name) => {
    if (!rows.length) return [];
    const t = el("table", { class: "people-matrix spend-table" });
    t.append(el("tr", {}, el("th", {}, name), money ? el("th", {}, "Cost") : null, el("th", {}, "Calls"), el("th", {}, "In"), el("th", {}, "Out")));
    for (const r of rows) t.append(el("tr", {}, el("th", { scope: "row" }, r.name), money ? el("td", {}, dollars(r.usd)) : null, el("td", {}, String(r.calls)), el("td", {}, tokens(r.in)), el("td", {}, tokens(r.out))));
    return [el("h2", {}, title), el("div", { class: "matrix-scroll" }, t)];
  };
  // By day: a bar each, as tall as its share of the busiest day.
  if (sp.by_day.length) {
    const most = Math.max(...sp.by_day.map((r) => r.usd), 0.0001);
    const bars = el("div", { class: "spend-days", role: "img", "aria-label": "Cost by day" });
    for (const r of sp.by_day) bars.append(el("div", { class: "spend-day", title: r.name + ": " + dollars(r.usd) + ", " + r.calls + " calls" }, el("span", { style: "height:" + Math.max(2, Math.round(60 * r.usd / most)) + "px" })));
    parts.push(el("h2", {}, "By day"), bars);
  }
  parts.push(...table("By who", sp.by_who, true, "Who"), ...table("By model", sp.by_model, true, "Model"), ...table("On a plan (tokens)", sp.plans, false, "Who"));
  return parts;
}

// ---------------------------------------------------------------------------
// an app's Terminal: a shell in one of its boxes, for the machine's owner
// (hi_admin.md, release 7), as the dev page's terminal is.

let adminTerm = null; // the open terminal: { app, box, term, fit, socket, node }
let termWanted = "";  // the box Overview's Terminal button picked

function closeTerm() {
  if (!adminTerm) return;
  const t = adminTerm;
  adminTerm = null;
  t.closing = true;
  if (t.socket) t.socket.close();
  if (t.observer) t.observer.disconnect();
  t.term.dispose();
}

function sectionTerminal(s) {
  const parts = [el("p", {}, "A shell in a running box of " + s.name + ", as the user of the box.")];
  if (!s.you) parts.push(el("p", { class: "note" }, "Only the owner of the machine" + (s.owner ? ", " + s.owner + "," : "") + " can open a terminal here. This is the server owner. To change the owner, run hi server owner <Slack ID> --email <address> on the machine."));
  if (!s.boxes.length) {
    if (adminTerm && adminTerm.app === s.name) closeTerm();
    parts.push(el("p", { class: "muted" }, "No box of " + s.name + " runs now."));
    parts.push(terminalHow());
    return parts;
  }
  if (!s.you) {
    parts.push(el("ul", { class: "list" }, ...s.boxes.map((b) => el("li", {}, b.label))));
    parts.push(terminalHow());
    return parts;
  }
  const mine = adminTerm && adminTerm.app === s.name;
  const select = el("select", { "aria-label": "Box" });
  for (const b of s.boxes) select.append(el("option", { value: b.id, selected: mine ? adminTerm.box === b.id : termWanted === b.id }, b.label));
  parts.push(el("div", { class: "data-actions" }, select, el("button", { onclick: () => openTerm(s.name, select.value) }, mine ? "Open again" : "Open")));
  if (mine) parts.push(adminTerm.node);
  parts.push(terminalHow());
  return parts;
}

function terminalHow() {
  return el("div", { class: "how muted small" }, el("h3", {}, "How it works"), el("ul", {},
    el("li", {}, "The shell is in the box, not on the machine. The boxes hold no keys of the app."),
    el("li", {}, "hi writes each terminal in Activity and tells the ops channel of the app."),
    el("li", {}, "A terminal closes after 30 minutes without a key press.")));
}

function openTerm(app, box) {
  closeTerm();
  const screen = el("div", { class: "admin-term-screen" });
  const why = el("p", { class: "muted small" });
  const node = el("div", { class: "admin-term" }, why, screen);
  const term = new Terminal({
    cursorBlink: true,
    fontSize: 13,
    fontFamily: 'ui-monospace, "SF Mono", Menlo, "DejaVu Sans Mono", monospace',
    // The page's theme's colours (theme.js).
    theme: window.hiTheme ? window.hiTheme.terminal() : undefined,
  });
  const fit = new FitAddon.FitAddon();
  term.loadAddon(fit);
  const t = adminTerm = { app, box, term, fit, node, socket: null };
  renderSection();
  term.open(screen);
  if (window.hiTheme) window.hiTheme.onChange(function () { term.options.theme = window.hiTheme.terminal(); });
  try { fit.fit(); } catch (e) { /* not drawn yet */ }
  const url = (location.protocol === "https:" ? "wss://" : "ws://") + location.host + "/api/app/term?name=" + encodeURIComponent(app) +
    "&box=" + encodeURIComponent(box) + "&cols=" + term.cols + "&rows=" + term.rows;
  const socket = t.socket = new WebSocket(url);
  socket.binaryType = "arraybuffer";
  const encoder = new TextEncoder();
  socket.onopen = () => { why.textContent = ""; term.focus(); };
  socket.onmessage = (event) => term.write(typeof event.data === "string" ? event.data : new Uint8Array(event.data));
  socket.onclose = (event) => {
    if (t.closing) return;
    why.textContent = "The terminal closed" + (event.reason ? ": " + event.reason : "") + ". Click Open again for a new terminal.";
  };
  term.onData((data) => { if (socket.readyState === 1) socket.send(encoder.encode(data)); });
  const resize = () => {
    try { fit.fit(); } catch (e) { return; }
    if (socket.readyState === 1) socket.send(JSON.stringify({ cols: term.cols, rows: term.rows }));
  };
  t.observer = new ResizeObserver(resize);
  t.observer.observe(screen);
}

// A developer is "pro" between the page and the machine, as it was.
const levelOptions = [["owner", "Owner"], ["pro", "Developer"], ["contributor", "Contributor"], ["none", "None"]];
function levelSelect(value, label, onchange) {
  const select = el("select", { "aria-label": label });
  for (const [v, text] of levelOptions) select.append(el("option", { value: v, selected: v === value }, text));
  select.addEventListener("change", () => onchange(select.value, select));
  return select;
}
async function setLevel(app, person, name, level, select, before) {
  const words = { owner: "an owner", pro: "a developer", contributor: "a contributor", none: "nobody in it" };
  if (!(await ask({ title: "Make " + (name || person) + " " + words[level] + " of " + app + "?", text: level === "none" ? "They lose their access level, their private links and their SSH key. hi tells the app channel." : "hi tells the app channel." + (level === "owner" ? " An owner has only the owner level. Their old level goes." : ""), ok: "Change" }))) { if (select) select.value = before; return false; }
  try {
    await api("/api/app/settings", { app, what: "level", person, level });
    toast((name || person) + " is " + words[level] + " of " + app + ".");
    return true;
  } catch (e) { if (select) select.value = before; if (!(e instanceof SignIn)) toast(e.message, true); return false; }
}

function sectionPeople(s) {
  const list = el("ul", { class: "list" });
  for (const p of s.people) {
    const keyButtons = el("span", { class: "actions" },
      el("button", { class: "small", onclick: async () => {
        const answer = await ask({ title: "SSH key of " + (p.name || p.id) + " for the dev box", text: "Paste their public key, for example the contents of ~/.ssh/id_ed25519.pub. Their old key stops working.", ok: "Save", field: "ssh-ed25519 AAAA…" });
        if (answer) change("sshkey", { person: p.id, key: answer.field }, "The SSH key of " + (p.name || p.id) + " is in the dev box.");
      } }, p.key ? "New key" : "Set key"),
      p.key ? el("button", { class: "small", onclick: async () => { if (await ask({ title: "Remove the SSH key of " + (p.name || p.id) + "?", text: "They cannot SSH into the dev box after this.", ok: "Remove" })) change("sshkey", { person: p.id, remove: true }); } }, "Remove key") : null);
    list.append(el("li", {}, el("span", { class: "what" }, el("strong", {}, p.name || p.id), " ", el("span", { class: "muted small" }, p.id), el("span", { class: "muted small block" }, p.key ? "SSH key " + p.key : p.level === "owner" || p.level === "pro" ? "no SSH key" : "")),
      levelSelect(p.level, "Access level of " + (p.name || p.id), async (level, select) => { if (await setLevel(s.name, p.id, p.name, level, select, p.level)) refreshSettings(); }), keyButtons));
  }
  if (!s.people.length) list.append(el("li", { class: "plain muted small" }, "Nobody yet."));
  const id = el("input", { maxlength: "200", placeholder: "Their email, or Slack member ID", "aria-label": "Their email, or Slack member ID" });
  let level = "pro";
  const add = el("form", { class: "say-form", autocomplete: "off" }, id, levelSelect("pro", "Their access level", (v) => { level = v; }), el("button", { type: "submit" }, "Add"));
  add.addEventListener("submit", async (event) => {
    event.preventDefault();
    const who = id.value.trim();
    if (!who) return;
    // By email: the machine's list finds their Slack account (hi_people.md).
    if (who.includes("@")) {
      if (!(await ask({ title: "Make " + who + " " + ({ owner: "an owner", pro: "a developer", contributor: "a contributor" })[level] + " of " + s.name + "?", text: "hi tells the app channel.", ok: "Add" }))) return;
      try {
        const answer = await api("/api/person", { action: "add", email: who, levels: { [s.name]: level } });
        toast(answer.text, true);
        id.value = "";
        refreshSettings();
      } catch (e) { if (!(e instanceof SignIn)) toast(e.message, true); }
      return;
    }
    if (await setLevel(s.name, who, "", level, null, "none")) { id.value = ""; refreshSettings(); }
  });
  return [el("p", { class: "purpose" }, "The people of the app and their access levels."),
    list, el("h2", {}, "Add a person"), add, el("p", { class: "muted small" }, "Type an email or a Slack member ID. If the person is in the Slack workspace of the app, hi adds their Slack account."),
    el("div", { class: "how muted small" }, el("h3", {}, "How it works"), el("ul", {},
      el("li", {}, "Owners build in the dev box and publish."),
      el("li", {}, "Developers can do the same, but not rollbacks. They also cannot change production data, production env variables, keys or access levels."),
      el("li", {}, "Contributors report bugs."),
      el("li", {}, "Vibe and Pro are views of the dev page. Each person picks their own view."),
      el("li", {}, "This is the same as hi app owners and hi app access. On the dev page, owners can change all levels except owner."),
      el("li", {}, "To see the SSH key, sign-ins and other apps of a person, open People on the left.")))];
}

// signinNote is how the open app's sign-in of a kind is, for Agents.
function signinNote(kind) {
  const app = state && state.apps.find((a) => a.name === openApp);
  const g = app && (app.signins || []).find((x) => x.kind === kind);
  if (!g || g.state === "unknown") return "";
  return el("p", { class: "small key-status" }, el("span", { class: "dot " + signinClass(g.state), "aria-hidden": "true" }),
    el("span", {}, signinLine(g) + (g.why && (g.state === "signed-out" || g.state === "expiring") ? ": " + g.why : "") + (g.seen ? " (seen " + ago(g.seen) + ")" : "")));
}

function sectionAgents(s) {
  const parts = [el("p", { class: "purpose" }, "The model, payer and effort of each role. The app restarts after a change. This is the same as hi app member.")];
  for (const m of s.members) {
    const provider = el("select", { "aria-label": m.role + ": who pays", disabled: m.agent !== "hermes" });
    for (const [v, text] of [["openrouter", "OpenRouter, per call"], ["chatgpt", "A ChatGPT plan, with OpenRouter as fallback"], ["claude", "Claude plan"]]) {
      if (v === "claude" && m.agent !== "claude") continue;
      if (v !== "claude" && m.agent === "claude") continue;
      provider.append(el("option", { value: v, selected: m.provider === v }, text));
    }
    const model = el("input", { value: m.model || "", placeholder: "default of the agent", "aria-label": m.role + ": model" });
    const fallback = el("input", { value: m.fallback || "", placeholder: "the default", "aria-label": m.role + ": fallback" });
    const effort = el("select", { "aria-label": m.role + ": effort" });
    for (const e of ["low", "medium", "high", "xhigh", "max"]) effort.append(el("option", { value: e, selected: m.effort === e }, e));
    parts.push(el("div", { class: "card-box member-card" }, el("div", { class: "card-head pad" }, el("strong", { class: "card-name" }, m.role), el("span", { class: "muted small" }, m.label)),
      el("div", { class: "policy-fields" }, field("Who pays", provider), field("Model", model), field("Fallback on OpenRouter", fallback), field("Effort", effort)),
      el("div", { class: "data-actions pad" }, el("button", { class: "small", onclick: () => {
        const fields = { role: m.role };
        if (m.agent === "hermes" && provider.value !== m.provider) fields.provider = provider.value;
        if (model.value.trim() !== (m.model || "")) fields.model = model.value.trim();
        if (fallback.value.trim() !== (m.fallback || "")) fields.fallback = fallback.value.trim();
        if (effort.value !== m.effort) fields.effort = effort.value;
        if (Object.keys(fields).length === 1) { toast("Nothing changed."); return; }
        change("member", fields, "The " + m.role + " changed. The app restarts.");
      } }, "Save"))));
  }
  // Keys and sign-ins.
  const or = el("div", { class: "machine-block" }, el("p", {}, el("strong", {}, "OpenRouter: "), s.openrouter_from ? s.openrouter_from + ", a shared key from the vault" : s.own_openrouter ? "a key for this app only" : "the key of this machine" + (s.machine_openrouter ? "" : ". This machine has no key")));
  const orShared = useSharedRow(s, s.openrouter_from, "An OpenRouter key, sk-or-…, from openrouter.ai/settings/keys.", (from) => change("openrouter", { from }, "The app now uses the OpenRouter key " + from + "."));
  or.append(el("div", { class: "data-actions" },
    el("button", { class: "small", onclick: async () => { const answer = await ask({ title: "OpenRouter key for this app only", text: "The app gets separate billing and limits. hi checks the key and keeps it on the machine. hi never shows it again.", ok: "Check and save", field: "sk-or-…" }); if (answer) change("openrouter", { key: answer.field }, "The app has its own OpenRouter key."); } }, s.own_openrouter ? "Replace the key" : "Give the app its own key"),
    orShared.button,
    s.own_openrouter || s.openrouter_from ? el("button", { class: "small", onclick: async () => { if (await ask({ title: "Use the OpenRouter key of this machine?", text: s.openrouter_from ? "The app stops using " + s.openrouter_from + "." : "hi deletes the key of the app.", ok: "Use the machine key" })) change("openrouter", { remove: true }); } }, "Use the machine key") : null),
    orShared.row);
  const models = el("input", { type: "checkbox" });
  models.checked = s.models;
  models.addEventListener("change", () => change("models", { on: models.checked }));
  parts.push(el("h2", {}, "Dev page chat"), el("label", { class: "check" }, models, " Developers can pick the model and effort next to the message box. The pick is also the default for new sessions in the dev box. When this is off, there is no picker, and each session keeps its first model (hi app models)."));
  parts.push(el("h2", {}, "Keys and sign-ins"), or);
  if (s.uses_chatgpt) {
    parts.push(el("div", { class: "machine-block" }, el("p", {}, el("strong", {}, "ChatGPT: "), s.chatgpt ? "a sign-in for this app only, " + s.chatgpt : "the Codex sign-in of this machine" + (s.machine_chatgpt ? "" : ", which is not signed in")),
      s.chatgpt_failed ? el("p", { class: "error small" }, "The sign-in stopped working: " + s.chatgpt_failed + ". Sign in again.") : signinNote("chatgpt"),
      el("div", { class: "data-actions" }, el("button", { class: "small", onclick: () => change("chatgpt", {}, "Signing in. Find the code to type in Jobs") }, s.chatgpt ? "Sign in again" : "Sign the app in"),
        s.chatgpt ? el("button", { class: "small", onclick: () => change("chatgpt", { machine: true }) }, "Use the machine sign-in") : "")));
  }
  if (s.uses_claude) {
    const clShared = useSharedRow(s, s.claude_from, "A Claude token, sk-ant-oat…. Get it with claude setup-token, signed in to the account that pays.", (from) => change("claude", { from }, "The Claude members of the app now use " + from + "."));
    parts.push(el("div", { class: "machine-block" }, el("p", {}, el("strong", {}, "Claude: "), s.claude_from ? s.claude_from + ", a shared key from the vault" : s.own_claude ? "a token for this app only" : "the sign-in of this machine" + (s.machine_claude ? "" : ", which does not exist")),
      signinNote("claude"),
      el("div", { class: "data-actions" }, el("button", { class: "small", onclick: async () => { const answer = await ask({ title: "Claude token for this app only", text: "Get it with claude setup-token, signed in to the account that pays. It lasts one year. hi keeps it on the machine and never shows it again.", ok: "Save", field: "sk-ant-oat…" }); if (answer) change("claude", { key: answer.field }, "The app has its own Claude token."); } }, s.own_claude ? "Replace the token" : "Give the app its own token"),
        clShared.button,
        s.own_claude || s.claude_from ? el("button", { class: "small", onclick: () => change("claude", { machine: true }) }, "Use the machine sign-in") : ""),
      clShared.row));
  }
  // Stage's limit, and the boxes' memory.
  const usd = el("input", { value: s.stage_limit > 0 ? String(s.stage_limit) : "", placeholder: "off", "aria-label": "Dollars a day", class: "small-input" });
  parts.push(el("h2", {}, "Stage model calls"), el("p", { class: "small" }, (s.stage_limit > 0 ? "Up to $" + s.stage_limit.toFixed(2) + " each day" : "No daily limit") + ". Today: $" + s.stage_spent.toFixed(2) + " in " + s.stage_calls + " call" + (s.stage_calls === 1 ? "" : "s") + ". hi sets no limit for production."),
    el("div", { class: "data-actions" }, el("span", { class: "muted small" }, "$ each day"), usd, el("button", { class: "small", onclick: () => change("limit", { usd: usd.value.trim() || "off" }) }, "Save"), el("button", { class: "small", onclick: () => change("limit", { usd: "off" }) }, "No limit")));
  const dev = el("input", { value: s.memory_dev, class: "small-input", "aria-label": "Dev box memory" });
  const app = el("input", { value: s.memory_app, class: "small-input", "aria-label": "Memory of stage and production boxes" });
  parts.push(el("h2", {}, "Memory"), el("div", { class: "data-actions" }, el("span", { class: "muted small" }, "Dev box"), dev, el("button", { class: "small", onclick: () => change("memory", { box: "dev", size: dev.value.trim() }) }, "Save"),
    el("span", { class: "muted small" }, "Stage and production boxes"), app, el("button", { class: "small", onclick: () => change("memory", { box: "app", size: app.value.trim() }) }, "Save"),
    s.memory_own ? el("button", { class: "small", onclick: () => change("memory", { box: "default" }) }, "Use the hi default") : ""), el("p", { class: "muted small" }, "For example 24g or 512m. Running boxes get the change immediately."));
  return parts;
}

function sectionSlack(s) {
  const ops = el("input", { value: s.ops_channel || "", placeholder: "C… (empty for none)", "aria-label": "Ops channel ID", class: "small-input wide-input" });
  const bot = el("input", { type: "password", placeholder: "xoxb-… (leave empty to keep the current token)", "aria-label": "Bot token" });
  const appToken = el("input", { type: "password", placeholder: "xapp-… (leave empty to keep the current token)", "aria-label": "App token" });
  const copy = el("button", { class: "small", onclick: () => copyText(s.manifest) }, "Copy the manifest");
  return [
    el("dl", { class: "facts" }, el("dt", {}, "Bot"), el("dd", {}, "@" + s.handle + " · " + s.slash), el("dt", {}, "Channel"), el("dd", {}, s.channel), el("dt", {}, "Tokens"), el("dd", {}, (s.has_bot ? "bot token set" : "no bot token") + ", " + (s.has_app ? "app token set" : "no app token"))),
    el("h2", {}, "Ops channel"), el("p", { class: "muted small" }, "A second channel for incidents, warnings, security, keys and access. First invite the app with /invite @" + s.handle + " in that channel."),
    el("div", { class: "data-actions" }, ops, el("button", { class: "small", onclick: () => change("ops", { channel: ops.value.trim() }) }, "Save")),
    el("h2", {}, "Tokens"), el("p", { class: "muted small" }, "Use this when Slack gives new tokens. The app token is under Basic Information. The bot token is under OAuth & Permissions. hi checks each token, then the app restarts."),
    field("App token", appToken), field("Bot token", bot), el("div", { class: "data-actions" }, el("button", { class: "small", onclick: () => change("slack", { bot: bot.value.trim(), app_token: appToken.value.trim() }, "hi saved the tokens. The app restarts.") }, "Check and save")),
    el("h2", {}, "Slack app manifest"), el("p", { class: "muted small" }, "Update the Slack app after a hi update or a rename. At api.slack.com/apps, open the app and go to App Manifest. Paste this manifest over the old one and click Save. Reinstall the app if Slack asks."),
    el("div", { class: "data-actions" }, copy, el("a", { class: "button-like", href: "https://api.slack.com/apps", target: "_blank", rel: "noopener noreferrer" }, "Your Slack apps ↗")),
    el("details", { class: "small" }, el("summary", { class: "muted" }, "Manifest"), el("pre", { class: "job-output manifest" }, s.manifest)),
    el("h2", {}, "Name"), el("div", { class: "data-actions" }, el("button", { onclick: () => renameApp(s.name) }, "Rename…"))];
}

function sectionSettings(s) {
  const scopes = { all: "stage and production", production: "production only", stage: "stage only", dev: "dev box only" };
  const list = el("ul", { class: "list" });
  for (const e of s.env) {
    list.append(el("li", {}, el("span", { class: "what" }, el("code", {}, e.name), " ", el("span", { class: "muted small" }, scopes[e.scope] + (e.file ? ", a file" : "") + (e.from ? ", from " + e.from : ""))),
      el("span", { class: "actions" }, e.file || e.from ? "" : el("button", { class: "small", onclick: () => setEnv(e.name, e.scope) }, "New value"),
        el("button", { class: "small danger", onclick: async () => { if (await ask({ title: "Remove " + e.name + "?", text: "hi removes it from the settings files of the app boxes within one minute. It leaves the environment of a box when the box starts again.", ok: "Remove" })) change("env", { name: e.name, scope: e.scope, remove: true }); } }, "Remove"))));
  }
  if (!s.env.length) list.append(el("li", { class: "plain muted small" }, "None yet."));
  const helper = (slot, label) => {
    const box = el("input", { type: "checkbox" });
    box.checked = !!s.helper[slot];
    box.addEventListener("change", () => change("helper", { slot, on: box.checked }));
    return el("label", { class: "check" }, box, " " + label);
  };
  const freeze = el("input", { type: "checkbox" });
  freeze.checked = s.auto_freeze;
  freeze.addEventListener("change", () => change("watch", { on: freeze.checked }));
  const beat = el("input", { placeholder: s.heartbeat ? "Set. A new URL replaces it." : "https://hc-ping.com/…", "aria-label": "Heartbeat URL" });
  const every = el("input", { value: s.every || "", placeholder: "1m", class: "small-input", "aria-label": "Every" });
  const parts = [
    el("h2", {}, "Env variables"), el("p", { class: "muted small" }, "Env variables in the boxes of the app."),
    list, el("div", { class: "data-actions" }, el("button", { class: "small", onclick: () => setEnv("", "all") }, "+ Env variable"),
      (s.shared || []).length ? el("button", { class: "small", onclick: () => setEnvFrom(s) }, "+ From a shared key") : ""),
    el("div", { class: "how muted small" }, el("h3", {}, "How it works"), el("ul", {},
      el("li", {}, "The app reads each variable from its environment."),
      el("li", {}, "hi keeps the values encrypted in the server vault, under hi/app/" + s.name + "/…. This page never shows a value."),
      el("li", {}, "The Vault tab shows the versions of each value."),
      el("li", {}, "A variable can get its value from a shared key that an admin shared with the app. When the shared key changes, the variable changes too."),
      el("li", {}, "To set a file, for example a certificate, run hi app env " + s.name + " --file KEY=<path> on the machine."))),
    el("h2", {}, "Helper"), el("p", { class: "muted small" }, "Help and feedback on the pages of the app. A change applies at the next page load."), el("div", { class: "radios" }, helper("preview", "On stage"), helper("production", "On production")),
    el("h2", {}, "Watcher"), el("label", { class: "check" }, freeze, " Freeze the app automatically if an advisory for its packages is exploited, or if it sends a burst of traffic to a watched host. When this is off, an owner decides."),
    el("h2", {}, "Heartbeat"), el("p", { class: "muted small" }, s.heartbeat ? "The app sends a heartbeat" + (s.every ? " every " + s.every : " every minute") + "." : "No heartbeat. Nobody gets an alert if the app stops."),
    el("div", { class: "data-actions" }, beat, every, el("button", { class: "small", onclick: () => change("heartbeat", { url: beat.value.trim(), every: every.value.trim() }) }, "Save"), s.heartbeat ? el("button", { class: "small", onclick: () => change("heartbeat", { remove: true }) }, "Off") : ""),
  ];
  parts.push(el("h2", {}, "Code"));
  if (s.remote) parts.push(el("p", { class: "small" }, "The app pushes each merge to ", el("code", {}, shown(s.remote)), s.forge !== "hi" ? ". Its tasks are " + (s.forge === "github" ? "GitHub" : "GitLab") + " issues." : "."));
  else {
    const repo = el("input", { placeholder: "owner/name, or for GitLab group/name or host/group/name", "aria-label": "Repository" });
    parts.push(el("p", { class: "muted small" }, "The code is only on this machine. You can put it on GitHub or GitLab, in a new private repository or an empty one. hi uses the sign-in of this machine."),
      el("div", { class: "data-actions" }, repo, el("button", { class: "small", onclick: () => change("github", { repo: repo.value.trim() }, "Putting the code on GitHub") }, "On GitHub"), el("button", { class: "small", onclick: () => change("gitlab", { repo: repo.value.trim() }, "Putting the code on GitLab") }, "On GitLab")));
  }
  if (s.adopted) parts.push(el("p", { class: "small" }, "Adopted from ", el("code", {}, s.adopted.from), s.adopted.local ? ", a folder on this machine." : ". It follows " + (s.adopted.follow || "the default branch") + "."),
    s.adopted.local ? "" : el("div", { class: "data-actions" }, el("button", { class: "small", onclick: () => change("upstream", {}, "Fetching the upstream") }, "Fetch upstream changes")));
  parts.push(el("h2", {}, "Remove the app"), el("div", { class: "data-actions" }, el("button", { class: "danger", onclick: () => removeApp(s.name) }, "Remove…")));
  return parts;
}

async function setEnvFrom(s) {
  const values = await vaultForm({ title: "Env variable from a shared key", ok: "Save",
    text: "The app keeps only a reference. The value stays in the vault. When the value changes, the app boxes get the new value within one minute.",
    fields: [{ label: "NAME, for example MARKET_API_KEY" }, { label: "Shared key", options: s.shared }],
    live: (v) => {
      const bad = v[0] && !/^[A-Za-z_][A-Za-z0-9_]*$/.test(v[0].trim()) ? [0] : [];
      return { ok: /^[A-Za-z_][A-Za-z0-9_]*$/.test(v[0].trim()) && !!v[1], bad, hint: bad.length ? "Use letters, digits and _." : "" };
    } });
  if (!values) return;
  change("env", { name: values[0].trim(), from: values[1], scope: "all" }, values[0].trim() + " now gets its value from " + values[1] + ".");
}

async function setEnv(name, scope) {
  const answer = await ask({ title: name ? "New value for " + name : "New env variable", text: (name ? "" : "Type the name, then the value. ") + "hi sends the value to the machine one time and never shows it again. The app boxes get it in their settings files within one minute. A box gets it in its environment when it starts again.", ok: "Save", field: name ? "Value" : "NAME=value", fieldCheck: (v) => (name ? (v ? "" : "Type a value.") : /^[A-Za-z_][A-Za-z0-9_]*=/.test(v) ? "" : "Use NAME=value, for example STRIPE_KEY=sk_live_…"), check: name ? "" : "Production only. Stage must not use it." });
  if (!answer) return;
  let key = name, value = answer.field;
  if (!name) { key = answer.field.slice(0, answer.field.indexOf("=")); value = answer.field.slice(answer.field.indexOf("=") + 1); scope = answer.checked ? "production" : "all"; }
  change("env", { name: key, value, scope }, key + " is set.");
}

// ---------------------------------------------------------------------------
// People across apps, and the machine's keys

// People: one row a person, joined by email across the apps and their
// Slack workspaces (hi_people.md). A person opens in the middle, as an
// app does, with their level in each app, their SSH key, their sign-ins,
// and Remove from everything. The grid shows every level at once.
let peopleApps = null;   // /api/people
let peopleGrid = false;  // the grid instead of the list
let peopleFilter = "";   // an app, or ""
let personOpen = "";     // the person in the middle: their key, or "new"
let slackPeople = null;  // the workspaces' people, for Add person
async function refreshPeopleApps() {
  try { peopleApps = await api("/api/people"); } catch (e) { return; }
  if (viewShown("people")) renderPeople();
  if (middle.startsWith(":person/")) renderPerson();
}
const levelWords = { owner: "Owner", pro: "Developer", vibe: "Developer", contributor: "Contributor" };
function personTitle(person) { return person.name || person.email || person.key; }
function personSummary(person) {
  const apps = Object.keys(person.levels).sort();
  if (!apps.length) return "no access level in any app";
  const counts = {};
  for (const app of apps) counts[person.levels[app]] = (counts[person.levels[app]] || 0) + 1;
  if (apps.length > 2) return Object.entries(counts).map(([level, n]) => levelWords[level] + " in " + n).join(" · ");
  return apps.map((app) => app + ": " + levelWords[person.levels[app]]).join(" · ");
}
// The People dropdown's choice for the server's owner and admins.
const peopleServer = ":server";
function adminPersonName(admin) {
  const person = peopleApps && admin.email && peopleApps.people.find((x) => x.email && x.email.toLowerCase() === admin.email.toLowerCase());
  if (!person) return el("strong", {}, admin.name);
  return el("button", { class: "link-button", title: "Apps, Slack and sign-ins of this person", onclick: () => openPerson(person.key) }, el("strong", {}, admin.name));
}
function peopleAppsBlock() {
  const p = peopleApps;
  const box = el("div", { class: "pad" });
  if (!p) return box;
  const tools = el("div", { class: "people-tools" });
  const server = state && (state.admins || []).length ? el("option", { value: peopleServer, selected: peopleFilter === peopleServer }, state.org && !state.org.no_owner ? "Server" : "Admins") : null;
  if (!server && peopleFilter === peopleServer) peopleFilter = "";
  const filter = el("select", { "aria-label": "Show people of" }, el("option", { value: "" }, "All apps"), server, ...p.apps.map((app) => el("option", { value: app, selected: app === peopleFilter }, app)));
  filter.addEventListener("change", () => { peopleFilter = filter.value; renderPeople(); });
  const toggle = el("div", { class: "chips", role: "group", "aria-label": "View" },
    el("button", { "aria-pressed": String(!peopleGrid), onclick: () => { peopleGrid = false; renderPeople(); } }, "List"),
    el("button", { "aria-pressed": String(peopleGrid), onclick: () => { peopleGrid = true; renderPeople(); } }, "Grid"));
  const onServer = peopleFilter === peopleServer;
  $("people-admins").hidden = !onServer;
  tools.append(filter, el("span", { class: "grow" }), ...(onServer ? [] : [toggle]));
  box.append(tools);
  if (onServer) { renderAdmins(); return box; }
  if (!p.mail) box.append(el("p", { class: "note muted small" }, "This machine does not send email. People can sign in only with Slack links. ", el("button", { class: "link-button", "data-nav": "", onclick: () => showView("keys") }, "Set up email"), "."));
  if ((p.no_email || []).length) box.append(el("p", { class: "note muted small" }, "The Slack apps of " + p.no_email.join(", ") + " do not have the users:read.email scope. So hi cannot find the email of their people. A person can show two times. A person can sign in by email only after you give their address. Add the scope, then click Reinstall to Workspace."));
  const query = $("people-search").value.trim().toLowerCase();
  const people = p.people.filter((x) => (!query || [x.name, x.email, x.key, ...x.slack].join(" ").toLowerCase().includes(query)) && (!peopleFilter || x.levels[peopleFilter]));
  if (peopleGrid) box.append(peopleGridBlock(p, people));
  else {
    const list = el("ul", { class: "stask-list person-list" });
    for (const person of people) {
      const bits = [personSummary(person)];
      if (person.admin) bits.push("admin");
      list.append(el("li", { class: middle === ":person/" + person.key ? "current" : "" },
        el("button", { class: "stask-row person-row", onclick: () => openPerson(person.key), title: person.email || person.key },
          el("span", { class: "person-line" }, el("span", { class: "stask-title" }, personTitle(person)),
            el("span", { class: "muted small person-signed" }, person.signed_in ? ago(person.signed_in) : "")),
          el("span", { class: "muted small person-sub" }, (person.email || "no email yet") + " · " + bits.join(" · ")))));
    }
    if (!people.length) list.append(el("li", { class: "empty" }, p.people.length ? "No one matches." : "No people yet."));
    box.append(list);
  }
  const domains = (p.domains || []).join(", ");
  box.append(el("p", { class: "muted small" }, "Email domains of the organisation: " + (domains || "any") + " ",
    el("button", { class: "link-button", onclick: async () => {
      const answer = await ask({ title: "Email domains of the organisation", text: "A new person must have an address in one of these domains. You can allow a person from outside the organisation. If this is empty, all addresses are allowed.", ok: "Save", field: domains || "corp.example, corp-labs.example", fieldCheck: () => "" });
      if (answer === false) return;
      try { toast((await api("/api/person", { action: "domains", domains: answer.field })).text); refreshPeopleApps(); } catch (e) { if (!(e instanceof SignIn)) toast(e.message, true); }
    } }, "Change")));
  return box;
}
function peopleGridBlock(p, people) {
  const table = el("table", { class: "people-matrix people-grid" });
  table.append(el("tr", {}, el("th", {}, ""), ...p.apps.map((a) => el("th", { title: a }, a))));
  for (const person of people) {
    table.append(el("tr", {}, el("th", { scope: "row", title: person.email || person.key }, el("button", { class: "link-button", onclick: () => openPerson(person.key) }, personTitle(person))),
      ...p.apps.map((app) => {
        const level = person.levels[app] || "none";
        return el("td", {}, el("button", { class: "level-badge level-" + level, title: personTitle(person) + " in " + app + ": " + (levelWords[level] || "no access level") + ". Click to change.", onclick: () => openPerson(person.key) }, level === "none" ? "·" : levelWords[level]));
      })));
  }
  return el("div", { class: "matrix-scroll" }, table);
}

function openPerson(key) {
  personOpen = key;
  setMiddle(":person/" + key);
  if (narrow()) { panes.left.open = false; keepPanes(); renderPanes(); }
  moved();
  if (viewShown("people")) renderPeople();
  if (!peopleApps) refreshPeopleApps(); else renderPerson();
}

async function personDo(change, said) {
  try {
    const answer = await api("/api/person", change);
    toast(said || answer.text, true);
    if (answer.key && personOpen !== "new" && answer.key !== personOpen) openPerson(answer.key);
    await refreshPeopleApps();
    return answer;
  } catch (e) { if (!(e instanceof SignIn)) toast(e.message, true); return null; }
}

// outsideWords is the box that lets in an address outside the
// organisation's domains, or "" when none are set and any address goes.
function outsideWords() {
  const domains = (peopleApps && peopleApps.domains) || [];
  if (!domains.length) return "";
  return "Allow this address. It is not at " + (domains.length === 1 ? domains[0] : domains.slice(0, -1).join(", ") + " or " + domains[domains.length - 1]);
}

function renderPerson() {
  const box = $("person");
  if (personOpen === "new") { box.replaceChildren(...addPersonView()); return; }
  const p = peopleApps;
  if (!p) { box.replaceChildren(el("p", { class: "muted" }, "…")); return; }
  const person = p.people.find((x) => x.key === personOpen);
  if (!person) { box.replaceChildren(el("p", { class: "muted" }, "The person " + personOpen + " is not on this machine now.")); return; }
  const title = personTitle(person);
  const parts = [el("div", { class: "admin-head" }, el("h1", { class: "admin-title" }, title), person.admin ? el("span", { class: "pill open" }, "admin") : null, person.outside ? el("span", { class: "pill" }, "outside the organisation") : null,
    el("span", { class: "grow" }),
    el("button", { class: "small", disabled: !person.email || !p.mail, title: !person.email ? "Give an email address first" : !p.mail ? "This machine does not send email yet. Set it up in Keys, Email." : "Send one email with a link to each app of this person", onclick: async () => {
      if (await ask({ title: "Send sign-in links to " + person.email + "?", text: "hi sends one email. It has a sign-in link to the dev page or the app, for each app where the person has an access level" + (person.admin ? ", and a link to this page" : "") + ". Each link works one time, for three days.", ok: "Send" })) personDo({ action: "invite", key: person.key });
    } }, "Email a sign-in link"))];
  // Who they are.
  const who = el("dl", { class: "person-facts" });
  const editEmail = el("button", { class: "link-button", onclick: async () => {
    const answer = await ask({ title: person.email ? "Name of " + title : "Email for " + title, text: person.email ? "The name that people use. It shows here and in the app links." : "The email connects the Slack accounts of the person. It also lets them sign in by email. hi gets the email from Slack if the Slack app has users:read.email.", ok: "Save", field: person.email ? title : "anna@corp.example", check: person.email ? "" : outsideWords() });
    if (!answer) return;
    personDo(person.email ? { action: "update", key: person.key, name: answer.field } : { action: "update", key: person.key, email: answer.field, name: person.name, outside: answer.checked });
  } }, person.email ? "Rename" : "Add an email");
  who.append(el("dt", {}, "Email"), el("dd", {}, person.email || el("span", { class: "muted" }, "none yet"), " ", editEmail));
  who.append(el("dt", {}, "Slack"), el("dd", {}, person.slack.length ? person.slack.join(", ") : el("span", { class: "muted" }, "not found in the Slack workspaces of the apps"),
    person.email ? el("span", {}, " ", el("button", { class: "link-button", onclick: async () => {
      const answer = await ask({ title: "Slack account of this person", text: "Their member ID in a workspace where Slack has a different email for them. To find it in Slack, open their profile, click ⋮, then Copy member ID.", ok: "Link", field: "U07ABC123" });
      if (answer) personDo({ action: "update", key: person.key, slack: answer.field });
    } }, "Link an account")) : null));
  who.append(el("dt", {}, "SSH key"), el("dd", {}, person.ssh_key || el("span", { class: "muted" }, "none"), " ",
    person.email ? el("button", { class: "link-button", onclick: async () => {
      const answer = await ask({ title: "SSH key of " + title, text: "Paste their public key, for example the contents of ~/.ssh/id_ed25519.pub. hi puts it in the dev box of each app where they are an owner or developer. This also applies to apps where they get that level later. Their old key stops working.", ok: "Save", field: "ssh-ed25519 AAAA…" });
      if (answer) personDo({ action: "key", key: person.key, ssh_key: answer.field });
    } }, person.ssh_key ? "New key" : "Set key") : null,
    person.ssh_key && person.email ? el("button", { class: "link-button", onclick: async () => { if (await ask({ title: "Remove the SSH key of " + title + "?", text: "hi removes it from the dev box of each app. They cannot SSH in after this.", ok: "Remove" })) personDo({ action: "key", key: person.key, ssh_key: "" }); } }, "Remove") : null));
  who.append(el("dt", {}, "Signed in"), el("dd", {}, person.signed_in ? "by email " + ago(person.signed_in) : "never by email", person.invited ? el("span", { class: "muted" }, " · invited " + ago(person.invited)) : null));
  parts.push(who);
  // Their level in each app.
  parts.push(el("h2", {}, "Apps"));
  const table = el("table", { class: "people-matrix person-apps" });
  for (const app of p.apps) {
    const level = person.levels[app] || "none";
    table.append(el("tr", {}, el("th", { scope: "row" }, el("button", { class: "link-button", onclick: () => openView(app, "people") }, app)),
      el("td", {}, levelSelect(level, title + " in " + app, async (next, select) => {
        const words = { owner: "an owner", pro: "a developer", contributor: "a contributor", none: "nobody in it" };
        if (!(await ask({ title: "Make " + title + " " + words[next] + " of " + app + "?", text: next === "none" ? "They lose their access level in this app, their private links and their SSH key there. hi tells the app channel." : "hi tells the app channel." + (next === "owner" ? " An owner has only the owner level. Their old level goes." : "") + (person.email || person.slack.length ? "" : ""), ok: "Change" }))) { select.value = level; return; }
        if (!(await personDo({ action: "levels", key: person.key, levels: { [app]: next } }))) select.value = level;
      })),
      el("td", { class: "muted small" }, person.ids[app] && person.ids[app] !== person.email ? "as " + person.ids[app] : "")));
  }
  parts.push(el("div", { class: "matrix-scroll" }, table),
    el("p", { class: "muted small" }, "hi tells the app channel about each change. If the person has a Slack account in the workspace of the app, hi adds them by that account. Then the lead and Slack know them. hi adds other people by email."));
  // Leaving.
  parts.push(el("h2", {}, "Remove"), el("div", { class: "data-actions" }, el("button", { class: "danger", onclick: async () => {
    const typed = person.email || person.key;
    if (await ask({ title: "Remove " + title + " from everything?", text: "They lose their access level in all apps. Their links and sign-ins stop at their next click. hi removes their SSH key from all dev boxes and removes them from this machine. hi tells each app channel. If they are the only owner of an app, they stay owner until the app has another owner.", ok: "Remove", typed })) {
      const answer = await personDo({ action: "remove", key: person.key });
      if (answer) openView("");
    }
  } }, "Remove from everything…")));
  box.replaceChildren(...parts);
}

async function loadSlackPeople() {
  if (slackPeople) return slackPeople;
  try { slackPeople = await api("/api/people/slack"); } catch (e) { slackPeople = []; }
  return slackPeople;
}

function addPersonView() {
  const p = peopleApps || { apps: [], mail: false };
  const email = el("input", { type: "email", required: true, maxlength: "200", placeholder: "anna@corp.example", "aria-label": "Their email", autocomplete: "off", list: "slack-people" });
  const name = el("input", { maxlength: "80", placeholder: "Their name, as people know them", "aria-label": "Their name", autocomplete: "off" });
  const options = el("datalist", { id: "slack-people" });
  loadSlackPeople().then((list) => {
    for (const x of list) if (x.email) options.append(el("option", { value: x.email }, (x.real_name || x.name) + " · " + "Slack of " + x.app));
  });
  email.addEventListener("change", () => {
    const found = (slackPeople || []).find((x) => x.email === email.value.trim().toLowerCase());
    if (found && !name.value.trim()) name.value = found.real_name || found.name;
  });
  const outside = el("input", { type: "checkbox" });
  const invite = el("input", { type: "checkbox", checked: p.mail, disabled: !p.mail });
  const levels = {};
  const table = el("table", { class: "people-matrix person-apps" });
  for (const app of p.apps) table.append(el("tr", {}, el("th", { scope: "row" }, app), el("td", {}, levelSelect("none", "Access level in " + app, (v) => { levels[app] = v; }))));
  const error = el("p", { class: "error small" });
  const form = el("form", { class: "stack person-add", autocomplete: "off" },
    el("label", { class: "field" }, el("span", { class: "muted small" }, "Email. It identifies the person in all apps and Slack workspaces. Pick a person from Slack, or type an address."), email, options),
    el("label", { class: "field" }, el("span", { class: "muted small" }, "Their name"), name),
    outsideWords() ? el("label", { class: "check small" }, outside, " " + outsideWords()) : null,
    p.apps.length ? el("div", { class: "field" }, el("span", { class: "muted small" }, "Access level in each app. hi tells the app channel about each change."), el("div", { class: "matrix-scroll" }, table)) : null,
    el("label", { class: "check small" }, invite, " " + (p.mail ? "Send them an email with a sign-in link to each app" : "Send them an email with sign-in links (this machine does not send email yet. Set it up in Keys, Email)")),
    error, el("div", { class: "buttons" }, el("button", { type: "button", onclick: () => openView("") }, "Cancel"), el("button", { class: "primary", type: "submit" }, "Add")));
  form.addEventListener("submit", async (event) => {
    event.preventDefault();
    error.textContent = "";
    if (!email.value.trim()) { error.textContent = "Type their email."; email.focus(); return; }
    const chosen = Object.fromEntries(Object.entries(levels).filter(([, v]) => v !== "none"));
    try {
      const answer = await api("/api/person", { action: "add", email: email.value.trim(), name: name.value.trim(), outside: outside.checked, levels: chosen, invite: invite.checked });
      toast(answer.text, true);
      await refreshPeopleApps();
      openPerson(answer.key);
    } catch (e) { if (!(e instanceof SignIn)) error.textContent = e.message; }
  });
  return [el("div", { class: "admin-head" }, el("h1", { class: "admin-title" }, "Add a person")),
    el("p", { class: "purpose" }, "Add a person to this machine by email, with or without Slack. hi uses the email to find their Slack accounts in the workspaces of the apps."), form];
}

let machineKeys = null;
// Email: how the machine sends sign-in links and invitations
// (hi_people.md). The password is never shown.
let mailSettings = null;
async function refreshMail() {
  try { mailSettings = await api("/api/mail"); } catch (e) { return; }
  if (viewShown("keys")) renderKeys();
}
function mailBlock() {
  const m = mailSettings;
  const box = el("div", {});
  box.append(el("h2", {}, "Email"));
  if (!m) return box;
  box.append(el("p", { class: "small incident-head" }, el("span", { class: "dot " + (m.on ? "idle" : ""), "aria-hidden": "true" }),
    m.on ? "From " + (m.name ? m.name + " <" + m.from + ">" : m.from) + ", through " + m.host + ":" + m.port + " (" + m.security + ")" : "Not set up. People can sign in only with Slack links."));
  const field = (name, placeholder, value, type) => el("input", { name, type: type || "text", placeholder, value: value || "", "aria-label": placeholder, autocomplete: "off", maxlength: "200" });
  const host = field("host", "SMTP server, for example smtp.corp.example", m.host);
  const port = field("port", "Port: 587, or 465 for TLS", m.port ? String(m.port) : "");
  const security = el("select", { "aria-label": "Security" }, ...[["starttls", "STARTTLS (587)"], ["tls", "TLS from the start (465)"], ["none", "None (a relay on the network)"]].map(([v, t]) => el("option", { value: v, selected: (m.security || "starttls") === v }, t)));
  const user = field("user", "User name, if the server needs one", m.username);
  const password = field("password", m.password_set ? "Password (saved. Type to change it)" : "Password", "", "password");
  const from = field("from", "From address, for example hi@corp.example", m.from);
  const name = field("name", "From name, for example hi on " + (state ? state.machine : "this machine"), m.name);
  const form = el("form", { class: "machine-block stack", autocomplete: "off" }, host, el("div", { class: "signin-row" }, port, security), user, password, from, name,
    el("div", { class: "buttons" }, el("button", { class: "primary", type: "submit" }, "Save")));
  form.addEventListener("submit", async (event) => {
    event.preventDefault();
    try {
      toast((await api("/api/mail", { host: host.value.trim(), port: Number(port.value.trim()) || 0, security: security.value, username: user.value.trim(), password: password.value, from: from.value.trim(), name: name.value.trim() })).text);
      refreshMail();
    } catch (e) { if (!(e instanceof SignIn)) toast(e.message, true); }
  });
  box.append(form);
  if (m.on) box.append(el("div", { class: "data-actions" },
    el("button", { class: "small", onclick: async () => {
      const answer = await ask({ title: "Send a test email", text: "hi sends a short email to this address. This checks that the machine can send email.", ok: "Send", field: "you@corp.example" });
      if (!answer) return;
      try { toast((await api("/api/mail", { test: answer.field })).text, true); } catch (e) { if (!(e instanceof SignIn)) toast(e.message, true); }
    } }, "Send a test"),
    el("button", { class: "small danger", onclick: async () => {
      if (!(await ask({ title: "Stop sending email?", text: "Nobody can sign in by email until you set it up again. Slack links continue to work.", ok: "Stop" }))) return;
      try { toast((await api("/api/mail", { off: true })).text); refreshMail(); } catch (e) { if (!(e instanceof SignIn)) toast(e.message, true); }
    } }, "Turn off")));
  box.append(el("div", { class: "how muted small" }, el("h3", {}, "How it works"), el("ul", {},
    el("li", {}, "hi sends only sign-in links, codes and invitations. It does not track who opens them."),
    el("li", {}, "Ask your IT team for an SMTP account, or a relay that lets this machine send."),
    el("li", {}, "Also ask for SPF and DKIM records for the From domain. Without them, the emails go to spam."))));
  return box;
}

// ---------------------------------------------------------------------------
// The server's vault, first in Keys (hi_server_vault.md): open or locked,
// Unlock and Lock, the ways it opens, the recovery key for the owner,
// each path's versions with Rollback and Forget, and removed apps'
// settings with Purge. No value is ever on the page.

let vaultView = null;
let vaultHistory = null; // { path, versions }
// refreshVault redraws the vault; a poll waits while someone types, a
// change of theirs (force) doesn't.
async function refreshVault(force) {
  try { vaultView = await api("/api/vault"); } catch (e) { return; }
  if (viewShown("keys")) renderKeys();
  const typing = document.activeElement && $("orgview").contains(document.activeElement) && /INPUT|TEXTAREA|SELECT/.test(document.activeElement.tagName);
  if (orgOpen() && orgSection === "vault" && (force || !typing)) renderOrgSection();
}

// ---------------------------------------------------------------------------
// the server's Backups: one bucket for every app on this machine

let backupsView = null;
const backupsPicked = new Set();
const canBackups = () => !!(state && state.me && state.me.machine);

let backupsTimer = 0;
async function refreshBackups() {
  clearTimeout(backupsTimer);
  try { backupsView = await api("/api/backups"); } catch (e) { if (!(e instanceof SignIn)) toast(e.message, true); return; }
  const shown = orgOpen() && orgSection === "backups";
  const typing = document.activeElement && $("orgview").contains(document.activeElement) && /INPUT|TEXTAREA|SELECT/.test(document.activeElement.tagName);
  if (shown && !typing) renderOrgSection();
  // While a backup runs, the table follows it.
  if (shown && (backupsView.running || []).length) backupsTimer = setTimeout(refreshBackups, 5000);
}
// backupsCopying is the running job that copies app a now, if any.
function backupsCopying(a) {
  return (backupsView.running || []).find((j) => !j.server && !j.test && (!j.apps || !j.apps.length ? !!a.offsite : j.apps.includes(a.name)) && (!a.copied || a.copied < j.started));
}
// backupsSize is bytes as people read them.
function backupsSize(n) {
  if (!n) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  let i = 0;
  while (n >= 1000 && i < units.length - 1) { n /= 1000; i++; }
  return (n >= 10 || i === 0 ? Math.round(n) : n.toFixed(1)) + " " + units[i];
}

// backupsDo sends a change; a failure is toasted, unless handled says it
// took care of it.
async function backupsDo(body, done, handled) {
  try {
    const answer = await api("/api/backups", body);
    if (answer.job) { toast((done || "Started") + ". See the output in Jobs.", true); followJob(answer.job); }
    else toast(done || firstLine(answer.text) || "Done.", true);
    if (answer.view) backupsView = answer.view;
    if (answer.job) await refreshBackups();
    else if (orgOpen() && orgSection === "backups") renderOrgSection();
    return true;
  } catch (e) {
    if (!(e instanceof SignIn) && !(handled && await handled(e))) toast(e.message, true);
    return false;
  }
}

// backupsWhen is a time, short ("3 h ago"), with the full one on hover.
function backupsWhen(t) {
  if (!t) return "—";
  return el("span", { title: new Date(t).toLocaleString() }, ago(t));
}

async function backupsSetBucket() {
  const v = backupsView || {};
  const address = await ask({ title: "The bucket for every app", text: "Give an S3-compatible bucket, as s3:https://<host>/<bucket>. Each app goes in its own folder in it.", ok: "Next",
    field: "s3:https://s3.eu-central-003.backblazeb2.com/my-bucket", fieldValue: v.bucket || "",
    fieldCheck: (x) => (!x.startsWith("s3:") ? "The address must start with s3:" : /^s3:(https?:\/\/)?[^/]+\/[^/]+/.test(x) ? "" : "Add the bucket name after the host: s3:https://<host>/<bucket>") });
  if (!address) return;
  const region = await ask({ title: "Region of the bucket", text: "For example eu-central-003 for Backblaze. Leave it empty to use us-east-1.", ok: "Next", field: "us-east-1",
    fieldValue: bucketRegion(address.field) || v.region || "", fieldCheck: (x) => (/^[a-z0-9-]*$/.test(x) ? "" : "Use only small letters, digits, and -.") });
  if (!region) return;
  const name = await ask({ title: "This server's folder", text: "The folder of this server in the bucket. Each server that uses the bucket needs its own folder. You cannot easily change it later.", ok: "Next", field: v.name || "server",
    fieldValue: v.name || "", fieldCheck: (x) => (/^[a-z0-9][a-z0-9-]{0,62}$/.test(x) ? "" : "Use only small letters, digits, and -.") });
  if (!name) return;
  const id = await ask({ title: "Access key ID of the bucket", text: "hi keeps it in the vault and never shows it again.", ok: "Next", field: "Access key ID" });
  if (!id) return;
  const secret = await ask({ title: "Secret access key of the bucket", text: "hi keeps it in the vault and never shows it again.", ok: "Save", field: "Secret access key" });
  if (!secret) return;
  const body = { what: "bucket", bucket: address.field, region: region.field, name: name.field, key_id: id.field, secret: secret.field };
  const done = "The bucket is set. hi copies each app after its next snapshot.";
  // A folder another server claims: offer to take it over.
  await backupsDo(body, done, async (e) => {
    if (!/belongs to another server/.test(e.message)) return false;
    const take = await ask({ title: "Take over the folder " + name.field + "?", text: e.message + " Take it over only if this machine replaces that server. Otherwise use another folder name.", ok: "Take over", typed: name.field });
    if (take) await backupsDo(Object.assign(body, { take_over: true }), done);
    return true;
  });
}

function backupsBlock() {
  const v = backupsView;
  if (!v) return [el("p", { class: "muted" }, "…")];
  const can = canBackups();
  const parts = [el("p", { class: "purpose" }, "Copies of the apps' snapshots in one S3 bucket, off this machine.")];
  // The bucket.
  const bucket = el("div", { class: "machine-block" });
  if (v.on) {
    bucket.append(el("p", { class: "small" }, "Bucket: ", el("code", {}, v.folder), v.region ? " (region " + v.region + ")" : ""));
  } else {
    bucket.append(el("p", { class: "warn small" }, "No bucket. The apps' snapshots are only on this machine."));
  }
  bucket.append(el("ul", { class: "bullets small muted" },
    el("li", {}, "One bucket for all apps on this machine."),
    el("li", {}, "Each app goes to " + v.name + "/apps/<app>/ in the bucket."),
    el("li", {}, "Several servers can use one bucket. Each server has its own folder."),
    el("li", {}, "Safer: one key for each server, limited to its folder (Backblaze: File name prefix " + v.name + "/)."),
    el("li", {}, "Backblaze: set the bucket's lifecycle to keep prior versions for 30 days. Otherwise deleted snapshots still cost space.")));
  if (can) bucket.append(el("div", { class: "data-actions" },
    el("button", { class: "small" + (v.on ? "" : " primary"), onclick: backupsSetBucket }, v.on ? "Change the bucket or keys…" : "Set the bucket…"),
    v.on ? el("button", { class: "small danger", onclick: async () => {
      if (await ask({ title: "Stop copying to the bucket?", text: "Apps that use the server's bucket are no longer copied offsite. What is in the bucket stays there.", ok: "Stop" })) backupsDo({ what: "off" }, "The server's bucket is off.");
    } }, "Stop using the bucket…") : null));
  parts.push(card("Bucket", bucket));
  // The plan.
  const plan = el("div", { class: "machine-block" });
  const at = input({ type: "time", value: v.at, class: "small-input", "aria-label": "Daily snapshot at" });
  const keep = input({ value: v.keep, class: "small-input", placeholder: "7,4,6", "aria-label": "Keep daily, weekly, monthly" });
  const follow = el("input", { type: "checkbox" });
  follow.checked = !v.apps_off;
  const limit = input({ value: v.limit_upload ? String(v.limit_upload) : "", class: "small-input", placeholder: "none", inputmode: "numeric", "aria-label": "Upload limit in KiB/s" });
  plan.append(el("ul", { class: "bullets small muted" },
    v.queued ? el("li", {}, "Every day at this time, hi backs up the apps one after another, then this server.")
      : el("li", {}, "hi takes a snapshot of each app every day, up to an hour after this time."),
    el("li", {}, "hi copies each snapshot to the bucket, and before each release."),
    el("li", {}, "Once a week, hi reads each copy in the bucket back, to check it. Once a month it also reads 1% of the data."),
    el("li", {}, "Keep: how many daily, weekly and monthly snapshots stay. An app can keep its own number.")),
    el("div", { class: "data-actions" }, el("span", { class: "muted small" }, "Daily at"), at, el("span", { class: "muted small" }, "Keep daily, weekly, monthly"), keep,
      el("span", { class: "muted small" }, "Upload limit (KiB/s)"), limit),
    el("label", { class: "check small" }, follow, " Copy new apps, and apps that did not choose"),
    can ? el("div", { class: "data-actions" }, el("button", { class: "small", onclick: () => backupsDo({ what: "plan", at: at.value, keep: keep.value.trim(), apps_default: follow.checked ? "on" : "off", limit_upload: limit.value.trim() || "off" }, "The plan is saved.") }, "Save the plan")) : null);
  parts.push(card("Plan", plan));
  // The apps.
  const apps = v.apps || [];
  for (const name of [...backupsPicked]) if (!apps.some((a) => a.name === name)) backupsPicked.delete(name);
  const where = { server: "Bucket", own: "Own bucket", off: "Off", none: "—" };
  const t = el("table", { class: "people-matrix backups-table" });
  t.append(el("tr", {}, el("th", {}, ""), el("th", {}, "App"), el("th", { title: "The last snapshot on this machine" }, "Snapshot"), el("th", { title: "The last copy to the bucket" }, "Copy"), el("th", { title: "The last weekly check of the copy in the bucket" }, "Checked"), el("th", { title: "The last restore test: the copy came back whole" }, "Restored"), el("th", {}, "")));
  const testing = (a) => (v.running || []).find((j) => j.test && !j.server && (!j.apps || !j.apps.length ? !!a.offsite : j.apps.includes(a.name)) && (!a.restore_tested || a.restore_tested < j.started));
  const testCell = (tested, failed, running) => running ? el("span", { class: "muted" }, "Testing…")
    : failed ? el("span", { class: "warn", title: failed }, "Failed") : backupsWhen(tested);
  for (const a of apps) {
    const pick = el("input", { type: "checkbox", "aria-label": "Pick " + a.name, disabled: !can });
    pick.checked = backupsPicked.has(a.name);
    pick.addEventListener("change", () => { if (pick.checked) backupsPicked.add(a.name); else backupsPicked.delete(a.name); renderOrgSection(); });
    const snap = a.failed && (!a.last || a.failed_at > a.last) ? el("span", { class: "warn", title: a.failed }, "Failed " + ago(a.failed_at)) : backupsWhen(a.last);
    let copy = a.offsite ? backupsWhen(a.copied) : el("span", { class: "muted", title: a.use === "off" ? "Not copied offsite: turned off" : "Not copied offsite: no bucket" }, where[a.use] || "—");
    const copying = a.offsite && backupsCopying(a);
    if (copying) copy = el("span", { class: "muted", title: copying.title }, "Copying…");
    else if (a.offsite && a.copy_failed) copy = el("span", { class: "warn", title: a.copy_failed }, "Failed " + ago(a.copy_failed_at));
    else if (a.offsite && !a.copied) copy = el("span", { class: "muted" }, "Not yet");
    if (a.use === "own" && a.offsite) copy = el("span", { title: "Its own bucket: " + a.offsite }, copy, " · own bucket");
    t.append(el("tr", {}, el("td", {}, pick), el("th", { scope: "row" }, a.name),
      el("td", {}, snap), el("td", { title: a.offsite || "" }, copy),
      el("td", {}, !a.offsite ? "—" : a.check_failed ? el("span", { class: "warn", title: a.check_failed }, "Failed") : backupsWhen(a.checked)),
      el("td", {}, !a.offsite ? "—" : testCell(a.restore_tested, a.restore_test_failed, testing(a))),
      el("td", { class: "row-buttons" }, can && a.offsite && !copying ? el("button", { class: "small", onclick: () => backupsDo({ what: "now", apps: [a.name] }, "Backing up " + a.name), title: "Back up " + a.name + " now" }, "Back up") : "",
        can && a.offsite && !testing(a) ? el("button", { class: "small", onclick: () => backupsDo({ what: "test", apps: [a.name] }, "Testing the restore of " + a.name), title: "Restore " + a.name + "'s newest copy to a temporary folder, check it, and delete it" }, "Test") : "")));
  }
  if (v.on) {
    const sv = v.server || {};
    const serverCopying = (v.running || []).find((j) => !j.test && (j.server || !j.apps || !j.apps.length) && (!sv.last || sv.last < j.started));
    let last = sv.last ? el("span", { title: new Date(sv.last).toLocaleString() + ", " + backupsSize(sv.size) }, ago(sv.last)) : el("span", { class: "muted" }, "Not yet");
    if (serverCopying) last = el("span", { class: "muted" }, "Copying…");
    else if (sv.failed && (!sv.last || sv.failed_at > sv.last)) last = el("span", { class: "warn", title: sv.failed }, "Failed " + ago(sv.failed_at));
    if (!v.vault) last = el("span", { class: "muted" }, "Needs the vault");
    t.append(el("tr", { class: "backups-server" }, el("td", {}, ""), el("th", { scope: "row", title: "The vault (encrypted), the admins, and the settings of this server" }, "This server"),
      el("td", {}, "—"), el("td", {}, last),
      el("td", {}, sv.check_failed ? el("span", { class: "warn", title: sv.check_failed }, "Failed") : backupsWhen(sv.checked)),
      el("td", {}, testCell(sv.restore_tested, sv.restore_test_failed, (v.running || []).find((j) => j.test && (j.server || !j.apps || !j.apps.length) && (!sv.restore_tested || sv.restore_tested < j.started)))),
      el("td", { class: "row-buttons" }, can && v.vault && !serverCopying ? el("button", { class: "small", onclick: () => backupsDo({ what: "now", server: true }, "Backing up the server"), title: "Back up this server now" }, "Back up") : "",
        can && v.vault && sv.last ? el("button", { class: "small", onclick: () => backupsDo({ what: "test", server: true }, "Testing the restore of this server"), title: "Restore this server's newest copy to a temporary folder, check it, and delete it" }, "Test") : "")));
  }
  const picked = [...backupsPicked];
  const appsBox = el("div", { class: "machine-block" });
  for (const j of v.running || []) {
    const left = apps.filter((a) => a.offsite && backupsCopying(a) === j).length;
    appsBox.append(el("p", { class: "small" }, "Running: " + j.title + ", started " + ago(j.started) + (left ? ", " + left + (left === 1 ? " app" : " apps") + " to go. " : ". "),
      el("button", { class: "small", onclick: () => followJob(j) }, "Show the output")));
  }
  if (!apps.length) appsBox.append(el("p", { class: "muted small" }, "This machine has no apps."));
  else appsBox.append(el("div", { class: "table-scroll" }, t));
  for (const a of apps) if (a.offsite && a.copy_failed) appsBox.append(el("p", { class: "warn small" }, a.name + ": " + a.copy_failed));
  if (can && apps.length) appsBox.append(el("div", { class: "data-actions" },
    el("span", { class: "muted small" }, picked.length ? picked.length + " picked:" : "Pick apps to change them together."),
    el("button", { class: "small", disabled: !picked.length || !v.on, onclick: () => backupsDo({ what: "apps", apps: picked, on: true }, "Copies to the bucket are on for " + picked.join(", ")) }, "Copy to the bucket"),
    el("button", { class: "small", disabled: !picked.length, onclick: () => backupsDo({ what: "apps", apps: picked, on: false }, "Copies offsite are off for " + picked.join(", ")) }, "Do not copy"),
    el("button", { class: "small", disabled: !apps.some((a) => a.offsite) || (v.running || []).length > 0, onclick: () => backupsDo({ what: "now", apps: picked }, picked.length ? "Backing up " + picked.join(", ") : "Backing up every app") }, picked.length ? "Back up picked now" : "Back up all now"),
    el("button", { class: "small", disabled: !apps.some((a) => a.offsite) || (v.running || []).length > 0, onclick: () => backupsDo({ what: "test", apps: picked }, picked.length ? "Testing the restore of " + picked.join(", ") : "Testing the restore of every app and the server") }, picked.length ? "Test restore picked" : "Test restore all")));
  appsBox.append(el("ul", { class: "bullets small muted" },
    el("li", {}, "Do not copy: the snapshots stay on this machine. Rollback still works."),
    el("li", {}, "Own bucket: the app uses its own bucket (hi app backups <app> --offsite). Copy to the bucket moves it to the server's bucket. Its old copies stay where they are."),
    el("li", {}, "This server: the vault (still encrypted), the admins and the settings. Not the vault's key file."),
    el("li", {}, "Test: hi restores the newest copy to a temporary folder, reads every file back, checks it, and deletes it. Nothing else changes."),
    el("li", {}, "A lost machine comes back with hi server restore --from " + (v.folder || "s3:https://<host>/<bucket>/" + v.name + "/") + " on a new machine, with the recovery key."),
    el("li", {}, "The snapshot keys are in the bucket too, sealed to the vault's recovery key. Keep the recovery key off this machine.")));
  const ownKeep = apps.filter((a) => a.own_keep);
  if (ownKeep.length) appsBox.append(el("p", { class: "muted small" }, "Keep their own number of snapshots: " + ownKeep.map((a) => a.name + " (" + a.keep + ")").join(", ") + ". The others keep " + v.keep + "."));
  for (const a of apps) if (a.offsite && a.check_failed) appsBox.append(el("p", { class: "warn small" }, a.name + ": the copy in the bucket failed its check: " + a.check_failed));
  parts.push(card("Apps", appsBox));
  // Recent runs, newest first.
  const runs = v.runs || [];
  const runsBox = el("div", { class: "machine-block" });
  if (!runs.length) runsBox.append(el("p", { class: "muted small" }, "No backups yet."));
  else {
    const rt = el("table", { class: "people-matrix backups-runs" });
    rt.append(el("tr", {}, el("th", {}, "When"), el("th", {}, "What"), el("th", {}, "Took"), el("th", {}, "Size"), el("th", {}, "New"), el("th", {}, "Result")));
    const kinds = { daily: "Daily", now: "Back up now", check: "Check", test: "Restore test", server: "Server" };
    for (const r of runs) {
      const took = r.took < 60 ? Math.round(r.took) + " s" : Math.round(r.took / 60) + " min";
      rt.append(el("tr", {}, el("td", {}, backupsWhen(r.at)), el("td", {}, (kinds[r.what] || r.what) + ": " + (r.app || "this server")),
        el("td", {}, took), el("td", {}, r.size ? backupsSize(r.size) : "—"), el("td", {}, r.added ? backupsSize(r.added) : "—"),
        el("td", {}, r.error ? el("span", { class: "warn", title: r.error }, "Failed") : (r.note || "OK"))));
    }
    runsBox.append(el("div", { class: "table-scroll" }, rt));
  }
  parts.push(card("Recent runs", runsBox));
  return parts;
}

// openVault opens the server's Vault in the middle.
function openVault() {
  if (state && state.org) openOrg("vault");
  else showView("keys");
}
async function vaultDo(body) {
  try {
    const answer = await api("/api/vault", body);
    if (answer.text && !String(body.action).startsWith("store-")) toast(answer.text, true);
    refreshVault(true);
    refreshState();
    return answer;
  } catch (e) { if (!(e instanceof SignIn)) toast(e.message, true); return null; }
}

// renderVaultBanner is the first thing an admin sees while the vault is
// locked, and the owner's reminder while the recovery key waits.
function renderVaultBanner() {
  const v = state && state.vault;
  const box = $("vault-banner");
  const open = openVault;
  if (v && v.locked) {
    box.className = "vault-banner vault-locked";
    box.replaceChildren(el("strong", {}, "The server vault is locked."), " hi cannot read the env variables of the apps. An app that starts must wait. ",
      el("button", { class: "small primary", onclick: open }, "Unlock"));
    box.hidden = false;
  } else if (v && v.recovery && state.me && state.me.machine_owner) {
    box.className = "vault-banner";
    box.replaceChildren("Save the recovery key of the vault. If nothing else can open the vault, the recovery key opens all secrets here. ",
      el("button", { class: "small", onclick: open }, "Save it"));
    box.hidden = false;
  } else box.hidden = true;
}

const vaultWays = { tpm: "the TPM of this machine", file: "a key file", passphrase: "a passphrase", recovery: "the recovery key" };

// vaultLine is the vault in the left panel's Keys: open or locked, and
// the way to the server's Vault in the middle.
function vaultLine() {
  const v = vaultView;
  const box = el("div", {}, el("h2", {}, "Server vault"));
  if (!v || !v.exists) return box;
  box.append(el("p", { class: "small incident-head" }, el("span", { class: "dot " + (v.open ? "idle" : "blocked"), "aria-hidden": "true" }),
    v.open ? "Open; " + v.paths.length + (v.paths.length === 1 ? " secret" : " secrets") + ". " : "Locked. ",
    el("button", { class: "link-button", "data-nav": "", onclick: openVault }, v.open ? "The vault" : "Unlock")));
  return box;
}

function vaultBlock(middle) {
  const v = vaultView;
  const box = el("div", { class: middle ? "vault-view" : "" });
  if (!middle) box.append(el("h2", {}, "Server vault"));
  if (!v) { box.append(el("p", { class: "muted" }, "…")); return box; }
  if (!v.exists) {
    box.append(el("p", { class: "muted small" }, "No vault yet. hi keeps the secrets as files. hi update makes the vault on a machine that runs a server or apps."));
    return box;
  }
  box.append(el("p", { class: "small incident-head" }, el("span", { class: "dot " + (v.open ? "idle" : "blocked"), "aria-hidden": "true" }),
    v.open ? "Open. " : "Locked: " + (v.why || "nothing opened it") + ". ", el("span", { class: "muted" }, v.protects || "")));
  if (!v.open) {
    const secret = el("input", { type: "password", placeholder: "Passphrase, or the 24 words of the recovery key", "aria-label": "Passphrase or recovery key", class: "wide-input", autocomplete: "off" });
    const form = el("form", { class: "machine-block signin-row", autocomplete: "off" }, secret, el("button", { class: "primary", type: "submit" }, "Unlock"));
    form.addEventListener("submit", async (event) => {
      event.preventDefault();
      const answer = await vaultDo({ action: "unlock", secret: secret.value });
      secret.value = "";
      if (answer) refreshVault();
    });
    box.append(form, el("p", { class: "muted small" }, "This goes to the server on this machine. The server keeps the vault open. The page does not keep it."));
    return box;
  }
  for (const line of v.advice) box.append(el("p", { class: "warn small" }, line));
  if (!v.heartbeat) box.append(el("p", { class: "warn small" }, "Nobody gets an alert if the server is locked or down. Set a heartbeat with hi heartbeat <url>."));

  // How it opens.
  const ways = el("ul", { class: "list" });
  for (const kind of v.kinds) {
    const row = el("li", { class: "row" }, el("span", {}, vaultWays[kind] || kind), el("span", { class: "grow" }));
    if (kind !== "recovery") row.append(el("button", { class: "small danger", onclick: () => vaultRemoveWay(kind, v) }, "Remove"));
    ways.append(row);
  }
  const adds = el("div", { class: "data-actions" });
  for (const kind of ["tpm", "passphrase"]) {
    if (v.kinds.includes(kind) || (kind === "tpm" && v.tpm)) continue;
    adds.append(el("button", { class: "small", onclick: () => vaultAddWay(kind) }, "Add " + vaultWays[kind]));
  }
  if (!v.kinds.includes("file") && !v.kinds.includes("tpm")) adds.append(el("button", { class: "small", onclick: () => vaultAddWay("file") }, "Add a key file"));
  box.append(el("h3", {}, "Ways to open"), ways, adds);
  if (v.tpm && !v.kinds.includes("tpm")) box.append(el("p", { class: "muted small" }, "The TPM cannot keep the key: " + v.tpm + "."));
  box.append(el("div", { class: "data-actions" }, el("button", { class: "small", onclick: async () => {
    if (!(await ask({ title: "Lock the vault?", text: "The server forgets its key now. Running boxes continue to run. An app that starts waits until someone unlocks the vault" + (v.kinds.some((k) => k === "tpm" || k === "file") ? ". The vault opens automatically when the server starts again." : "."), ok: "Lock" }))) return;
    vaultDo({ action: "lock" });
  } }, "Lock now")));

  // The recovery key.
  box.append(el("h3", {}, "Recovery key"));
  if (v.recovery && v.owner) box.append(el("p", { class: "small" }, "You did not save it yet. ", el("button", { class: "small primary", onclick: vaultShowRecovery }, "Save your recovery key")));
  else if (v.recovery) box.append(el("p", { class: "muted small" }, "The server owner" + (v.owner_is ? ", " + v.owner_is + "," : "") + " must save it, here or with hi server vault recovery."));
  else box.append(el("p", { class: "muted small" }, "Saved. hi does not keep a copy. It opens the vault on any machine, for example after a move, a restore or a lost key file."));
  if (v.owner) box.append(el("div", { class: "data-actions" }, el("button", { class: "small", onclick: async () => {
    if (!(await ask({ title: "Make a new recovery key?", text: "The old words stop working on this machine immediately. Older backups of the hi state still open with the old words. If a key can have leaked, also change the important secrets at their source.", ok: "Make a new key" }))) return;
    if (await vaultDo({ action: "recovery-new" })) vaultShowRecovery();
  } }, "New recovery key")));

  // Each path's versions.
  const table = el("table", { class: "people-matrix vault-paths" });
  table.append(el("tr", {}, el("th", {}, "Path"), el("th", {}, "Version"), el("th", {}, "Changed"), el("th", {}, "Names")));
  for (const p of v.paths) {
    table.append(el("tr", { class: p.removed ? "muted" : "" },
      el("th", { scope: "row" }, el("button", { class: "link-button", onclick: () => vaultOpenHistory(p.path) }, p.path)),
      el("td", {}, "v" + p.version), el("td", { class: "small" }, ago(p.time) + (p.by ? " · " + p.by : "")),
      el("td", { class: "small" }, p.names.length ? p.names.join(" ") : "empty", p.removed ? el("span", { class: "muted" }, " (removed app)") : "")));
  }
  box.append(el("h3", {}, "Secrets"), v.paths.length ? el("div", { class: "matrix-scroll" }, table) : el("p", { class: "muted small" }, "None yet."));
  if (vaultHistory) box.append(vaultHistoryBlock());

  box.append(vaultSharedBlock(v));
  box.append(vaultStoresBlock(v));

  if (v.removed.length) {
    box.append(el("h3", {}, "Removed apps"), el("p", { class: "muted small" }, "Their env variables and snapshots stay, but nothing reads them. To get them back, run hi app up with the kept folder."));
    const list = el("ul", { class: "list" });
    for (const name of v.removed) list.append(el("li", { class: "row" }, el("span", {}, name), el("span", { class: "grow" }), el("button", { class: "small danger", onclick: async () => {
      if (!(await ask({ title: "Delete the env variables and snapshots of " + name + "?", text: "This deletes the env variables, the snapshot key and the local snapshots of " + name + ". You cannot get them back. An offsite copy stays.", ok: "Delete", typed: name }))) return;
      vaultDo({ action: "purge", app: name });
    } }, "Purge")));
    box.append(list);
  }
  box.append(el("div", { class: "how muted small" }, el("h3", {}, "How it works"), el("ul", {},
    el("li", {}, "The vault is encrypted on disk. The server holds the key while the vault is open."),
    el("li", {}, "On a running machine, root and the user " + ((state && state.user) || "hi") + " can read the secrets."),
    el("li", {}, "The vault protects all copies of the disk and of the hi state."))));
  return box;
}

// vaultForm asks for a few fields in one dialog of its own, and gives
// their values, or null when cancelled. live checks as one types: it
// gives the fields that are wrong (a red border), a hint, and whether
// the form can be sent, which greys the button until it can. A password
// field gets an eye, at the first one's right, that shows them all.
function vaultForm({ title, text, fields, ok, live }) {
  return new Promise((resolve) => {
    const inputs = fields.map((f) => f.options
      ? el("select", { "aria-label": f.label }, ...f.options.map((o) => el("option", { value: o }, o)))
      : el("input", { type: f.type || "text", placeholder: f.label, "aria-label": f.label, autocomplete: "off", spellcheck: "false", value: f.value || "" }));
    const secret = inputs.filter((input) => input.type === "password");
    const rows = inputs.map((input, i) => {
      if (input.type !== "password" || input !== secret[0]) return el("div", { class: "vault-field" }, input);
      const eye = el("button", { type: "button", class: "vault-eye", title: "Show", "aria-label": "Show", "aria-pressed": "false" }, vaultEyeIcon(false));
      eye.addEventListener("click", () => {
        const shown = eye.getAttribute("aria-pressed") !== "true";
        for (const field of secret) field.type = shown ? "text" : "password";
        eye.setAttribute("aria-pressed", String(shown));
        eye.title = shown ? "Hide" : "Show";
        eye.setAttribute("aria-label", eye.title);
        eye.replaceChildren(vaultEyeIcon(shown));
        input.focus();
      });
      return el("div", { class: "vault-field" }, input, eye);
    });
    const hint = el("p", { class: "muted small vault-hint" });
    const send = el("button", { class: "primary", type: "submit" }, ok);
    const form = el("form", { class: "stack", autocomplete: "off", novalidate: true }, el("h2", {}, title), el("p", { class: "small" }, text), ...rows, hint,
      el("div", { class: "buttons" }, el("button", { type: "button", value: "cancel" }, "Cancel"), send));
    const dialog = el("dialog", { class: "vault-dialog" }, form);
    const judge = () => {
      const values = inputs.map((input) => input.value);
      const verdict = live ? live(values) : { ok: values.every((v) => v.trim()), bad: [], hint: "" };
      inputs.forEach((input, i) => {
        const wrong = (verdict.bad || []).includes(i);
        input.classList.toggle("vault-wrong", wrong);
        input.setAttribute("aria-invalid", String(wrong));
      });
      hint.textContent = verdict.hint || "";
      hint.classList.toggle("error", (verdict.bad || []).length > 0);
      send.disabled = !verdict.ok;
      return verdict.ok;
    };
    for (const input of inputs) { input.addEventListener("input", judge); input.addEventListener("change", judge); }
    let result = null;
    form.addEventListener("submit", (event) => {
      event.preventDefault();
      if (!judge()) return;
      result = inputs.map((input) => input.value);
      dialog.close();
    });
    form.querySelector('button[value="cancel"]').addEventListener("click", () => dialog.close());
    dialog.addEventListener("close", () => { for (const input of inputs) input.value = ""; dialog.remove(); resolve(result); }, { once: true });
    document.body.append(dialog);
    judge();
    dialog.showModal();
    inputs[0].focus();
  });
}

function vaultEyeIcon(shown) {
  const svg = document.createElementNS("http://www.w3.org/2000/svg", "svg");
  svg.setAttribute("viewBox", "0 0 24 24");
  svg.setAttribute("width", "18");
  svg.setAttribute("height", "18");
  svg.setAttribute("fill", "none");
  svg.setAttribute("stroke", "currentColor");
  svg.setAttribute("stroke-width", "1.8");
  svg.setAttribute("stroke-linecap", "round");
  svg.setAttribute("aria-hidden", "true");
  const paths = ["M2 12s3.6-7 10-7 10 7 10 7-3.6 7-10 7S2 12 2 12z", "M12 9a3 3 0 1 0 0 6 3 3 0 0 0 0-6z"];
  if (shown) paths.push("M3 3l18 18");
  for (const d of paths) {
    const path = document.createElementNS("http://www.w3.org/2000/svg", "path");
    path.setAttribute("d", d);
    svg.append(path);
  }
  return svg;
}

// vaultPassphraseCheck is the passphrase form's live check: long enough,
// and the second the same as the first.
function vaultPassphraseCheck([a, b]) {
  const bad = b && b !== a ? [1] : [];
  let hint = "";
  if (a && a.length < 12) hint = "Use at least 12 characters. You typed " + a.length + ".";
  else if (bad.length) hint = "The two passphrases are not the same.";
  return { ok: a.length >= 12 && a === b, bad, hint };
}

// vaultSharedBlock is the shared keys: a key several apps use, by
// reference; admins set it, give it to apps, and remove it.
function vaultSharedBlock(v) {
  const box = el("div", {});
  box.append(el("h3", { class: "with-action" }, "Shared keys", el("span", { class: "grow" }),
    el("button", { class: "small", onclick: () => vaultSetShared("") }, "New shared key")));
  box.append(el("p", { class: "muted small" }, "One key that many apps use, for example a market-data API key. An app uses it by reference (hi app env <app> NAME --from shared/<name>). A change reaches all apps within one minute."));
  const list = el("ul", { class: "list" });
  for (const s of v.shared) {
    let note = s.using.length ? "used by " + s.using.join(", ") : s.given.length ? "not used yet" : "shared with no app";
    const spent = Object.entries(s.spent || {});
    if (spent.length) note += " · " + dollars(spent.reduce((n, [, usd]) => n + usd, 0)) + " this month";
    if (s.outside) note += " · ⚠ " + dollars(s.outside.usd) + " spent outside hi apps " + ago(s.outside.at) + ". Change the key, and revoke the old key at OpenRouter";
    list.append(el("li", { class: "vault-key-row" },
      el("strong", {}, "shared/" + s.name),
      el("span", { class: "muted small" }, "v" + s.version + (s.changed ? " · " + ago(s.changed) : "") + " · " + note),
      el("span", { class: "grow" }),
      el("button", { class: "small", title: s.given.length ? "Shared with " + s.given.join(", ") : "Not shared with an app yet", onclick: () => vaultGiveApps(s, v.apps) },
        "Shared with" + (s.given.length ? " (" + s.given.length + ")" : "")),
      el("button", { class: "small", onclick: () => vaultSetShared(s.name) }, "Change value"),
      el("button", { class: "small danger", disabled: s.using.length > 0, title: s.using.length ? "Used by " + s.using.join(", ") : "", onclick: async () => {
        if (!(await ask({ title: "Remove shared/" + s.name + "?", text: "hi deletes its value and versions. The history of who changed it stays.", ok: "Remove" }))) return;
        vaultDo({ action: "shared-rm", name: s.name });
      } }, "Remove")));
  }
  if (!v.shared.length) list.append(el("li", { class: "muted small" }, "None yet."));
  box.append(list);
  return box;
}

// vaultStoresBlock is the team stores: a team's OpenBao or Vault that
// hi reads (apps take paths there with hi app env --from), and where
// hi's own paths are when they moved.
function vaultStoresBlock(v) {
  const box = el("div", {});
  box.append(el("h3", {}, "Team stores"), el("p", { class: "muted small" }, "The OpenBao or Vault of a team. hi reads a prefix there. An app can use the names in a path as env variables (hi app env <app> --from corp/payments/prod). A new version reaches the app within one minute. If the store is not available, the app keeps the last values that hi read."));
  if (v.moved) box.append(el("p", { class: "small" }, v.moved));
  const list = el("ul", { class: "list" });
  for (const s of v.stores) {
    const handoff = el("details", { class: "small" }, el("summary", {}, "For the store owners"),
      el("pre", { class: "job-output short" }, s.handoff),
      el("button", { class: "small", onclick: () => { navigator.clipboard.writeText(s.handoff); toast("Copied."); } }, "Copy"));
    const actions = el("span", { class: "inline-actions" },
      el("button", { class: "small", onclick: async () => { const a = await vaultDo({ action: "store-login", store: { prefix: s.prefix } }); if (a) vaultShowText("Logging in to " + s.prefix + "/", a.text); } }, "Check login"));
    if (s.login === "jwt") actions.append(el("button", { class: "small", onclick: async () => {
      if (!(await ask({ title: "New key pair for " + s.prefix + "/?", text: "hi makes a new key. The old key still logs in until the store owners put in the new public key.", ok: "Make key" }))) return;
      const a = await vaultDo({ action: "store-new-key", store: { prefix: s.prefix } });
      if (a) vaultShowText("New public key for the store owners", a.text);
    } }, "New key"));
    actions.append(el("button", { class: "small danger", onclick: async () => {
      if (!(await ask({ title: "Disconnect " + s.prefix + "/?", text: "hi stops reading the store and deletes its login. This is not possible while an app gets env variables from it.", ok: "Disconnect" }))) return;
      vaultDo({ action: "store-disconnect", store: { prefix: s.prefix } });
    } }, "Disconnect"));
    list.append(el("li", { class: "stack" },
      el("div", { class: "row" }, el("strong", {}, s.prefix + "/"), el("span", { class: "muted small" }, " " + s.where + " in " + s.address + " · " + s.login + " as " + s.role), el("span", { class: "grow" }), actions),
      s.ends ? el("p", { class: "warn small" }, "The secret ID ends " + s.ends + ". Ask the store owners for a new one.") : null,
      handoff));
  }
  box.append(list);
  if (v.sources.length) {
    const table = el("table", { class: "people-matrix" });
    table.append(el("tr", {}, el("th", {}, "Path"), el("th", {}, "Apps"), el("th", {}, "Version"), el("th", {}, "Names")));
    for (const s of v.sources) {
      const names = el("td", { class: "small" }, s.names.join(" ") || "—");
      for (const [name, why] of Object.entries(s.left || {})) names.append(el("div", { class: "muted" }, name + " left out: " + why));
      if (s.error) names.append(el("div", { class: "warn" }, s.error));
      table.append(el("tr", {}, el("th", { scope: "row" }, s.from), el("td", { class: "small" }, s.apps.join(", ")),
        el("td", { class: "small" }, s.read ? (s.version ? "v" + s.version : "read") + " · " + ago(s.read) : "not read yet"), names));
    }
    box.append(el("div", { class: "matrix-scroll" }, table));
  }
  box.append(el("div", { class: "data-actions" }, el("button", { class: "small", onclick: vaultConnectStore }, "+ Connect a team store")));
  return box;
}

async function vaultConnectStore() {
  const values = await vaultForm({ title: "Connect a team store", ok: "Connect",
    text: "hi reads all data under the prefix. It cannot write. With jwt, hi makes a key pair and gives you the public key for the store owners. No secret goes between you. An AppRole needs its role ID and secret ID.",
    fields: [{ label: "Prefix, for example corp" }, { label: "Address, for example https://bao.corp.example:8200" }, { label: "Where, for example vault://kv/corp" },
      { label: "Login: jwt or approle" }, { label: "Namespace (if the store uses one)" }, { label: "AppRole role ID", type: "password" }, { label: "AppRole secret ID", type: "password" }],
    live: (v) => {
      const bad = [];
      if (v[0] && !/^[a-z][a-z0-9-]{0,39}\/?$/.test(v[0].trim())) bad.push(0);
      if (v[1] && !/^(https:\/\/|http:\/\/(127\.0\.0\.1|localhost))/.test(v[1].trim())) bad.push(1);
      if (v[2] && !/^vault:\/\/[^/]+/.test(v[2].trim())) bad.push(2);
      const login = (v[3].trim() || "jwt").toLowerCase();
      if (login !== "jwt" && login !== "approle") bad.push(3);
      const filled = v[0].trim() && v[1].trim() && v[2].trim() && (login !== "approle" || (v[5] && v[6]));
      return { ok: !!filled && bad.length === 0, bad, hint: bad.includes(1) ? "Use https://, or http:// only for this machine." : bad.includes(2) ? "vault://<mount>[/<path>]" : "" };
    } });
  if (!values) return;
  const a = await vaultDo({ action: "store-connect", store: { prefix: values[0].trim().replace(/\/$/, ""), address: values[1].trim(), where: values[2].trim(), login: (values[3].trim() || "jwt").toLowerCase(), namespace: values[4].trim(), role_id: values[5], secret_id: values[6] } });
  if (a) vaultShowText("Connected", a.text);
}

// vaultShowText shows what a command said, such as what to hand a
// store's owners, to read and copy.
function vaultShowText(title, text) {
  const pre = el("pre", { class: "job-output" }, text);
  const dialog = el("dialog", { class: "vault-dialog" }, el("h2", {}, title), pre,
    el("div", { class: "buttons" }, el("button", { onclick: () => { navigator.clipboard.writeText(text); toast("Copied."); } }, "Copy"), el("button", { class: "primary", onclick: () => dialog.close() }, "Done")));
  dialog.addEventListener("close", () => dialog.remove(), { once: true });
  document.body.append(dialog);
  dialog.showModal();
}

// vaultGiveApps picks which apps may use a shared key: every app, ticked
// where it has it now; one that uses it stays until it stops.
function vaultGiveApps(s, apps) {
  const boxes = apps.map((app) => {
    const input = el("input", { type: "checkbox", value: app });
    input.checked = s.given.includes(app);
    const using = s.using.includes(app);
    if (using) input.disabled = true;
    return { app, input, row: el("label", { class: "check vault-app" }, input, " " + app, using ? el("span", { class: "muted small" }, " · uses the key") : "") };
  });
  const all = el("input", { type: "checkbox" });
  const sync = () => { all.checked = boxes.length > 0 && boxes.every((b) => b.input.checked); all.indeterminate = !all.checked && boxes.some((b) => b.input.checked); };
  all.addEventListener("change", () => { for (const b of boxes) if (!b.input.disabled) b.input.checked = all.checked; sync(); });
  for (const b of boxes) b.input.addEventListener("change", sync);
  sync();
  const dialog = el("dialog", { class: "vault-dialog" });
  const save = el("button", { class: "primary", type: "submit" }, "Save");
  const form = el("form", { class: "stack" },
    el("h2", {}, "Apps that can use shared/" + s.name),
    el("p", { class: "small" }, "A ticked app can use the key. The app then names the env variable that gets it (hi app env <app> NAME --from shared/" + s.name + "). An app that uses the key stays ticked until it stops using it."),
    el("label", { class: "check vault-app vault-all" }, all, " All apps"),
    el("div", { class: "vault-apps" }, ...boxes.map((b) => b.row)),
    el("div", { class: "buttons" }, el("button", { type: "button", onclick: () => dialog.close() }, "Cancel"), save));
  form.addEventListener("submit", async (event) => {
    event.preventDefault();
    dialog.close();
    await vaultDo({ action: "shared-apps", name: s.name, apps: boxes.filter((b) => b.input.checked).map((b) => b.app) });
  });
  dialog.append(form);
  dialog.addEventListener("close", () => dialog.remove(), { once: true });
  document.body.append(dialog);
  dialog.showModal();
}

// sharedKeyPicker picks one of the server's shared keys for an app's
// OpenRouter key or Claude token, or makes a new one in place: names is
// the list it shows, and a new key joins it. what says what the key is
// for, as the new key's dialog asks for it.
function sharedKeyPicker(names, current, what, onchange) {
  const pick = el("select", { "aria-label": "Shared key" });
  const none = el("span", { class: "muted small" }, "No shared keys in the vault yet.");
  const draw = (chosen) => {
    pick.replaceChildren(...names.map((n) => el("option", { value: n, selected: "shared/" + n === chosen || n === chosen }, "shared/" + n)));
    pick.hidden = !names.length;
    none.hidden = names.length > 0;
  };
  pick.addEventListener("change", () => onchange && onchange());
  const make = el("button", { type: "button", class: "small", onclick: async () => {
    const values = await vaultForm({ title: "New shared key", ok: "Save",
      text: "hi keeps it in the server vault. " + what + " Each app that you pick it for can use it. A new value reaches all these apps within one minute.",
      fields: [{ label: "Name, for example " + (what.includes("Claude") ? "claude" : "openrouter") }, { label: "Value", type: "password" }],
      live: sharedNameCheck });
    if (!values) return;
    const name = values[0].trim();
    try {
      await api("/api/vault", { action: "shared-set", name, secret: values[1] });
    } catch (e) { if (!(e instanceof SignIn)) toast(e.message, true); return; }
    if (!names.includes(name)) { names.push(name); names.sort(); }
    draw(name);
    onchange && onchange();
  } }, "New shared key");
  draw(current);
  const box = el("div", { class: "data-actions" }, pick, none, make);
  return { box, value: () => (names.length && pick.value ? "shared/" + pick.value : ""), set disabled(off) { pick.disabled = off; make.disabled = off; } };
}

// useSharedRow is an app setting's "Use a shared key" button, and the
// row it opens: a shared key to pick or make, and Use it.
function useSharedRow(s, current, what, use) {
  const pick = sharedKeyPicker(s.server_shared || (s.server_shared = []), current, what);
  const row = el("div", { class: "data-actions" }, pick.box, el("button", { class: "small primary", onclick: () => {
    const from = pick.value();
    if (!from) { toast("Make a shared key first.", true); return; }
    use(from);
  } }, "Use"));
  row.hidden = true;
  const button = el("button", { class: "small", onclick: () => { row.hidden = !row.hidden; } }, current ? "Another shared key" : "Use a shared key");
  return { button, row };
}

// sharedNameCheck judges a new shared key's name and value as typed.
function sharedNameCheck(v) {
  const badName = v[0].trim() !== "" && !/^[a-z][a-z0-9-]{0,39}$/.test(v[0].trim());
  return { ok: /^[a-z][a-z0-9-]{0,39}$/.test(v[0].trim()) && v[1] !== "", bad: badName ? [0] : [],
    hint: badName ? "Use lowercase letters, digits and dashes." : "" };
}

async function vaultSetShared(name) {
  const fields = name ? [] : [{ label: "Name, for example openrouter" }];
  fields.push({ label: "Value", type: "password" });
  const values = await vaultForm({ title: name ? "New value for shared/" + name : "New shared key", ok: "Save", fields,
    text: name ? "All apps that use it get the new value within one minute." : "After this, share it with the apps that can use it.",
    live: name ? (v) => ({ ok: v[0] !== "", bad: [], hint: "" }) : sharedNameCheck });
  if (!values) return;
  vaultDo({ action: "shared-set", name: name || values[0].trim(), secret: values[values.length - 1] });
}

async function vaultAddWay(kind) {
  if (kind === "passphrase") {
    const values = await vaultForm({ title: "Passphrase for the vault", ok: "Add",
      text: "After a reboot, an admin types it to unlock the vault, here or with hi server vault unlock. Use at least 12 characters. A few words are easiest. hi does not keep it. If you lose it, the recovery key still opens the vault.",
      fields: [{ label: "Passphrase", type: "password" }, { label: "Type it again", type: "password" }],
      live: vaultPassphraseCheck });
    if (values) vaultDo({ action: "add", kind, secret: values[0] });
    return;
  }
  vaultDo({ action: "add", kind });
}

async function vaultRemoveWay(kind, v) {
  const left = v.kinds.filter((k) => k !== kind && k !== "recovery");
  const byItself = left.some((k) => k === "tpm" || k === "file");
  let text = "The vault cannot open with " + vaultWays[kind] + " after this.";
  if (!byItself && kind !== "passphrase") text = v.warning;
  if (!(await ask({ title: "Remove " + vaultWays[kind] + "?", text, ok: "Remove" }))) return;
  vaultDo({ action: "remove", kind, yes: true });
}

async function vaultOpenHistory(path) {
  try { vaultHistory = await api("/api/vault/history?path=" + encodeURIComponent(path)); } catch (e) { if (!(e instanceof SignIn)) toast(e.message, true); return; }
  if (orgOpen() && orgSection === "vault") renderOrgSection(); else renderKeys();
}

function vaultHistoryBlock() {
  const h = vaultHistory;
  const list = el("ul", { class: "list" });
  h.versions.forEach((version, i) => {
    let what = version.changed.length ? "changed " + version.changed.join(" ") : "no change";
    if (version.rollback) what = "put version " + version.rollback + " back (" + what + ")";
    const row = el("li", { class: "row small" }, el("strong", {}, "v" + version.version), " " + ago(version.time) + " · " + (version.by || "hi") + " · " + what, el("span", { class: "grow" }));
    if (version.forgotten) row.append(el("span", { class: "muted" }, "value deleted"));
    else if (i > 0) {
      row.append(el("button", { class: "small", onclick: async () => {
        if (!(await ask({ title: "Go back to version " + version.version + "?", text: "Its values become a new version of " + h.path + ". The boxes get them within one minute.", ok: "Roll back" }))) return;
        await vaultDo({ action: "rollback", path: h.path, version: version.version });
        vaultOpenHistory(h.path);
      } }, "Roll back"),
      el("button", { class: "small danger", title: "Delete this value, for example after a leak", onclick: async () => {
        if (!(await ask({ title: "Delete the value of version " + version.version + "?", text: "hi destroys the value of this version. The entry stays in the history. Older backups of the hi state still have the value, so also change the secret at its source.", ok: "Delete" }))) return;
        await vaultDo({ action: "forget", path: h.path, version: version.version });
        vaultOpenHistory(h.path);
      } }, "Delete value"));
    }
    list.append(row);
  });
  const names = el("ul", { class: "list" });
  for (const n of h.names || []) {
    const row = el("li", { class: "row small" }, el("code", {}, n.name), n.file ? el("span", { class: "muted" }, " (file)") : "", el("span", { class: "grow" }));
    if (h.editable && !n.file) row.append(el("button", { class: "small", onclick: () => vaultSetName(h.path, n.name) }, "Change"));
    if (h.editable) row.append(el("button", { class: "small danger", onclick: async () => {
      if (!(await ask({ title: "Remove " + n.name + "?", text: "hi removes it from the settings files of the app boxes within one minute. It leaves the environment of a box when the box starts again. Its versions stay here.", ok: "Remove" }))) return;
      await vaultDo({ action: "path-remove", path: h.path, kind: n.name });
      vaultOpenHistory(h.path);
    } }, "Remove"));
    names.append(row);
  }
  const how = h.editable
    ? el("div", { class: "data-actions" }, el("button", { class: "small", onclick: () => vaultSetName(h.path, "") }, "+ Env variable"), el("span", { class: "muted small" }, "To set a file, run hi app env <app> --file NAME=<path>, or use the dev page."))
    : el("p", { class: "muted small" }, "Change it with " + h.how + ".");
  const close = () => { vaultHistory = null; if (orgOpen() && orgSection === "vault") renderOrgSection(); else renderKeys(); };
  return el("div", { class: "card-box vault-history" }, el("div", { class: "row" }, el("strong", {}, h.path), el("span", { class: "grow" }), el("button", { class: "small", onclick: close }, "Close")),
    el("h3", {}, "Current names"), names, how, el("h3", {}, "Versions"), list);
}

// vaultSetName sets one name in an app's settings path, through hi app
// env, so it's checked and reaches the boxes as any change.
async function vaultSetName(path, name) {
  const fields = name ? [] : [{ label: "NAME, for example STRIPE_KEY" }];
  fields.push({ label: "Value", type: "password" });
  const values = await vaultForm({ title: name ? "New value for " + name : "New env variable in " + path, ok: "Save", fields,
    text: "hi sends the value to the vault one time and never shows it again. The app boxes get it within one minute.",
    live: (v) => {
      const bad = !name && v[0] && !/^[A-Za-z_][A-Za-z0-9_]*$/.test(v[0].trim()) ? [0] : [];
      return { ok: (name || /^[A-Za-z_][A-Za-z0-9_]*$/.test(v[0].trim())) && v[v.length - 1] !== "", bad, hint: bad.length ? "Use letters, digits and _." : "" };
    } });
  if (!values) return;
  await vaultDo({ action: "path-set", path, kind: name || values[0].trim(), secret: values[values.length - 1] });
  vaultOpenHistory(path);
}

// vaultShowRecovery shows the recovery key once, then asks two of its
// words back; once they match, hi forgets it.
async function vaultShowRecovery() {
  let answer;
  try { answer = await api("/api/vault", { action: "recovery-show" }); } catch (e) { if (!(e instanceof SignIn)) toast(e.message, true); return; }
  const grid = el("ol", { class: "recovery-words" }, ...answer.words.map((w) => el("li", {}, w)));
  const dialog = el("dialog", { class: "recovery-dialog" },
    el("h2", {}, "Your recovery key"),
    el("p", { class: "small" }, "Write it down, or keep it in a password manager, not on this machine. It opens all secrets here. hi shows it only now. Never send it by Slack or email."),
    grid,
    el("div", { class: "buttons" },
      el("button", { title: "Copy the 24 words on one line, with numbers, for a password manager", onclick: async (event) => {
        const button = event.currentTarget;
        button.textContent = (await copyText(answer.words.map((w, i) => (i + 1) + ". " + w).join(" "))) ? "Copied" : "Not copied. Write them down.";
        setTimeout(() => { button.textContent = "Copy"; }, 2000);
      } }, "Copy"),
      el("button", { class: "primary", onclick: () => dialog.close("saved") }, "I saved it")));
  document.body.append(dialog);
  dialog.showModal();
  await new Promise((resolve) => dialog.addEventListener("close", resolve, { once: true }));
  grid.replaceChildren();
  dialog.remove();
  const [a, b] = answer.ask;
  const values = await vaultForm({ title: "Check two words", ok: "Done", text: "Type words " + a + " and " + b + " of the 24. This checks that you saved the key.",
    fields: [{ label: "Word " + a }, { label: "Word " + b }] });
  if (!values) { toast("You did not save the recovery key yet. Show it again when you are ready.", true); return; }
  vaultDo({ action: "recovery-saved", words: values.map((w) => w.trim()) });
}

async function refreshMachineKeys() {
  try { machineKeys = await api("/api/keys"); } catch (e) { return; }
  if (viewShown("keys")) renderKeys();
}
function machineKeysBlock() {
  const k = machineKeys;
  const box = el("div", {});
  if (!k) return box;
  const line = (label, ok, how) => el("p", { class: "small key-status" }, el("span", { class: "dot " + (ok ? "idle" : ""), "aria-hidden": "true" }),
    el("span", {}, el("strong", {}, label), ": " + (ok ? "set" : "not set"), ok ? "" : el("span", { class: "muted" }, " · " + how)));
  box.append(el("h2", {}, "Machine keys"), el("div", { class: "machine-block" },
    line("OpenRouter (Hermes)", k.openrouter, "run hermes setup on the machine"),
    line("Claude Code sign-in", k.claude, "run claude, then /login, on the machine"),
    line("Codex ChatGPT sign-in", k.chatgpt, k.codex_key ? "Codex uses an API key" : "run codex login on the machine")),
    el("p", { class: "muted small" }, "Apps without their own keys use these. The keys of an app are in its Agents section."));
  if (k.apps.length) {
    const table = el("table", { class: "people-matrix" });
    table.append(el("tr", {}, el("th", {}, ""), el("th", {}, "OpenRouter"), el("th", {}, "Claude"), el("th", {}, "ChatGPT"), el("th", {}, "agy")));
    for (const a of k.apps) {
      const states = a.states || {};
      const cell = (v, kind) => {
        const words = v === "own" ? "own" : v === "machine" ? "machine" : String(v).startsWith("shared/") ? v : "—";
        const st = kind && v ? states[kind] : "";
        return el("td", { class: (v === "own" || String(v).startsWith("shared/") ? "" : "muted") + " nowrap", title: st ? signinWords[st] || st : "" },
          st ? el("span", { class: "dot " + signinClass(st), "aria-hidden": "true" }) : "", st ? " " : "", words);
      };
      table.append(el("tr", {}, el("th", { scope: "row" }, el("button", { class: "link-button", onclick: async () => { await openView(a.app); showSection("agents"); } }, a.app)),
        cell(a.openrouter, ""), cell(a.claude, "claude"), cell(a.chatgpt, "chatgpt"), cell(a.agy, "agy")));
    }
    box.append(el("h2", {}, "App keys"), el("div", { class: "matrix-scroll" }, table));
  }
  return box;
}

// ---------------------------------------------------------------------------
// An app's Network and Data, the machine's Network, and Advisories.

function sectionNetwork(s) {
  const parts = [el("p", { class: "purpose" }, "How people reach the app: the NetBird peer, the address names, the allowed addresses and the sign-in.")];
  // NetBird.
  const nb = el("input", { type: "password", placeholder: "NetBird setup key", "aria-label": "NetBird setup key", class: "wide-input" });
  parts.push(el("h2", {}, "NetBird"), el("p", { class: "small incident-head" }, el("span", { class: "dot " + (s.joined ? "idle" : "blocked"), "aria-hidden": "true" }), s.joined ? (s.peer ? "The peer " + s.peer + " is on NetBird." : "The dev box is on NetBird.") : "The dev box did not join NetBird."),
    el("pre", { class: "job-output short" }, s.dev),
    el("div", { class: "data-actions" }, nb, el("button", { class: "small", onclick: () => change("netbird", { key: nb.value.trim() }, "The dev box joins NetBird within one minute.") }, s.joined ? "Join again" : "Join")),
    el("p", { class: "muted small" }, "In the NetBird dashboard, click Add Peer → Server. Make a one-off key with the auto-group hi-" + s.name + ". hi uses it one time, then deletes it."));
  // Addresses.
  let mode = s.mode;
  const host = el("input", { value: s.host || "", placeholder: "demo.corp.example", class: "wide-input", "aria-label": "Host name" });
  const production = el("input", { value: s.names ? s.names.production || "" : "", placeholder: "demo.example.com", class: "wide-input", "aria-label": "Production name" });
  const https = el("input", { type: "checkbox" }); https.checked = s.names ? !!s.names.https : true;
  const extra = el("div", { class: "stack" });
  const draw = () => {
    extra.replaceChildren();
    if (mode === "host") extra.append(field("Name that your IT team keeps. It is an A record to the NetBird address of the app.", host));
    if (mode === "names") extra.append(field("Production name. Stage and the dev page use names under it.", production), el("label", { class: "check small" }, https, " A proxy of your IT team handles TLS, so the links use https"));
  };
  const options = [];
  if (s.domain) options.push(["domain", "The domain of this machine, on https"]);
  options.push(["names", "A name for each address, kept by your IT team"], ["host", "One name kept by your IT team, with ports"], ["netbird", s.peer ? "The NetBird name, " + s.peer + ", on plain http" : "The NetBird name of the dev box, on plain http"]);
  parts.push(el("h2", {}, "Addresses"), el("p", { class: "small" }, "Now: " + s.addresses + "."),
    el("ul", { class: "list" }, ...s.urls.map((u) => el("li", {}, el("span", { class: "what" }, el("strong", {}, u.what), " ", el("span", { class: "muted small" }, u.waits ? "after the dev box joins NetBird" : shown(u.url)))))),
    radios("n-addr", options, mode, (v) => { mode = v; draw(); }), extra,
    el("div", { class: "data-actions" }, el("button", { class: "small", onclick: async () => {
      if (!(await ask({ title: "Change the addresses of " + s.name + "?", text: "The app restarts. The owners get the new dev page link in Slack. Links to the old addresses stop working.", ok: "Change" }))) return;
      change("addresses", { mode, host: host.value.trim(), production: production.value.trim(), https: https.checked }, "Changing the addresses");
    } }, "Change addresses")));
  draw();
  if (s.domain) {
    const on = el("input", { type: "checkbox" }); on.checked = s.domain_on;
    on.addEventListener("change", () => change("domain", { on: on.checked }));
    parts.push(el("label", { class: "check small" }, on, " Fixed names: production at " + s.name + "." + s.domain + " and stage at " + s.name + "-preview." + s.domain));
  }
  // Who gets in.
  const ips = el("input", { value: s.allow_ips.join(","), placeholder: "any address with the link", class: "wide-input", "aria-label": "Allowed addresses" });
  parts.push(el("h2", {}, "Allowed addresses"), el("p", { class: "muted small" }, "Stage and production let in only these IPs or ranges. hi sets this in NetBird Access Control, with the domain token. If this is empty, any address with the link can get in."),
    el("div", { class: "data-actions" }, ips, el("button", { class: "small", onclick: () => change("allowip", { ips: ips.value.trim() }) }, "Save")));
  // Sign-in.
  const provider = el("select", { "aria-label": "Sign-in provider" });
  provider.append(el("option", { value: "" }, "None. The link is enough"), ...s.providers.map((p) => el("option", { value: p }, p)));
  const domains = el("input", { placeholder: "example.com (optional)", class: "wide-input", "aria-label": "Email domains" });
  const groups = el("input", { placeholder: "a group (optional)", class: "wide-input", "aria-label": "Groups" });
  const anyone = el("input", { type: "checkbox" });
  parts.push(el("h2", {}, "Sign-in"), el("pre", { class: "job-output short" }, s.sso),
    s.providers.length ? el("div", { class: "stack" }, field("Provider", provider), field("Only these email domains", domains), field("Only these groups", groups), el("label", { class: "check small" }, anyone, " Anyone who signs in with this provider"),
      el("div", { class: "data-actions" }, el("button", { class: "small", onclick: () => change("sso", { sso: provider.value, domains: domains.value.trim(), groups: groups.value.trim(), anyone: anyone.checked }) }, "Save"))) : el("p", { class: "muted small" }, "This machine has no sign-in provider yet. Add one in Network, on the left."));
  // hi's check of the sites the dev box asks for.
  const checkOn = s.sites_check !== "off";
  parts.push(el("h2", {}, "Dev box sites"),
    el("p", { class: "small" }, checkOn
      ? "The hi site check is on."
      : "The hi site check is off. The owners decide on each site that the dev box asks for."),
    s.sites_paused ? el("p", { class: "small" }, "Paused: " + s.sites_paused + ".") : "",
    el("div", { class: "data-actions" }, el("button", { class: "small", onclick: () => change("sites-check", { on: !checkOn || !!s.sites_paused }, checkOn && !s.sites_paused ? "The owners now decide on all sites." : "The hi site check is on.") },
      checkOn && !s.sites_paused ? "Turn off" : (s.sites_paused ? "Turn on again" : "Turn on"))),
    checkOn ? el("div", { class: "how muted small" }, el("h3", {}, "How it works"), el("ul", {},
      el("li", {}, "When the dev box asks for a site, hi can allow it automatically for 7 days."),
      el("li", {}, "hi allows a site only if it is in the 100,000 most used sites, is more than one year old, and is on no threat list."),
      el("li", {}, "Also, only the site owner can make names or put files on it."),
      el("li", {}, "hi tells the ops channel about these sites once a day, with Keep and Undo."),
      el("li", {}, "The owners decide on all other sites."),
      s.sites_check === "watch" ? el("li", {}, "Gemini Decision 1 also watches. It judges each request that the rules send to the owners because of rank or age. hi records what it would do, but allows nothing. To see how often it agreed with the owners, run hi app dev " + s.name + " sites check.") : null)) : "");
  parts.push(el("h2", {}, "What to ask your IT team"), el("pre", { class: "job-output copyable" }, s.ask),
    el("div", { class: "data-actions" }, el("button", { class: "small", onclick: () => copyText(s.ask) }, "Copy")));
  return parts;
}
// copyText copies text. On plain http, where browsers offer no clipboard
// API, it copies the old way, from a text box it selects for a moment, in
// the open dialog when there is one (a modal dialog makes the rest inert).
async function copyText(text) {
  if (navigator.clipboard && window.isSecureContext) {
    try { await navigator.clipboard.writeText(text); toast("Copied."); return true; } catch (e) { /* the old way */ }
  }
  const box = el("textarea", { readonly: "", "aria-hidden": "true", style: "position:fixed;top:0;left:0;width:1px;height:1px;opacity:0" });
  box.value = text;
  (document.querySelector("dialog[open]") || document.body).append(box);
  box.select();
  let copied = false;
  try { copied = document.execCommand("copy"); } catch (e) { /* not copied */ }
  box.remove();
  if (copied) toast("Copied."); else toast("Select it and copy it.", true);
  return copied;
}

function sectionData(s) {
  const parts = [el("p", { class: "purpose" }, "Snapshots, releases, stage data and backups of the app. For an action that you cannot undo, hi first tells what it does. Then you type the app name.")];
  const snaps = el("ul", { class: "list" });
  for (const snap of s.snapshots.slice(0, 40)) {
    snaps.append(el("li", {}, el("span", { class: "what" }, el("strong", {}, snap.id), " ", el("span", { class: "muted small" }, new Date(snap.time).toLocaleString() + " · " + (snap.label || snap.kind) + (snap.size ? " · " + size(snap.size) : "")), snap.warning ? el("span", { class: "warn small block" }, snap.warning) : ""),
      el("button", { class: "small", onclick: () => restore(s.name, snap.id) }, "Restore…")));
  }
  if (!s.snapshots.length) snaps.append(el("li", { class: "plain muted small" }, s.snap_error || "None yet."));
  parts.push(el("h2", { class: "with-action" }, "Snapshots", el("span", { class: "grow" }), el("button", { class: "small", onclick: () => act("snapshot") }, "Snapshot now")), snaps);
  const releases = el("ul", { class: "list" });
  for (const r of s.releases) {
    releases.append(el("li", {}, el("span", { class: "what clamp2" }, el("strong", {}, "v" + r.version), " ", r.version === s.production ? el("span", { class: "pill open" }, "production") : "", " ", el("span", { class: "muted small" }, r.notes || "")),
      el("span", { class: "actions" }, r.version !== s.production ? el("button", { class: "small", onclick: () => rollback(s.name, r.version) }, "Roll back…") : "",
        el("a", { class: "button-like small", href: "/api/app/bom?name=" + encodeURIComponent(s.name) + "&release=v" + r.version }, "BOM"))));
  }
  if (!s.releases.length) releases.append(el("li", { class: "plain muted small" }, "Nothing published yet."));
  parts.push(el("h2", {}, "Releases"), releases);
  // Stage's data.
  const empty = el("input", { type: "checkbox" }); empty.checked = s.stage_empty;
  empty.addEventListener("change", () => change("stagedata", { on: empty.checked }));
  parts.push(el("h2", {}, "Stage data"), el("pre", { class: "job-output short" }, s.data),
    el("label", { class: "check small" }, empty, " Start stage empty at each release. Use this if stage must not show the data of real people."),
    el("div", { class: "data-actions" }, el("button", { class: "small", onclick: () => change("fork", {}, "Stage has a new fork of the production data.") }, "New fork for stage"),
      el("button", { class: "small danger", onclick: async () => {
        const answer = await ask({ title: "Copy the stage data to production?", text: "Production starts from the stage data. hi takes a snapshot of the production data first, so a restore can bring it back. Type the app name to continue.", ok: "Copy", typed: s.name });
        if (answer) change("copy", { confirm: s.name }, "Copying the stage data to production");
      } }, "Copy stage data to production…")));
  // Backups.
  const keep = el("input", { placeholder: "7,4,6", class: "small-input", "aria-label": "Daily, weekly, monthly" });
  parts.push(el("h2", {}, "Backups"), el("pre", { class: "job-output short" }, s.backups),
    el("div", { class: "data-actions" }, el("span", { class: "muted small" }, "Keep daily, weekly, monthly"), keep, el("button", { class: "small", onclick: () => change("keep", { keep: keep.value.trim() }) }, "Save")),
    el("div", { class: "data-actions" },
      state && state.org ? el("button", { class: "small", onclick: () => openOrg("backups") }, "The server's Backups") : null,
      el("button", { class: "small", onclick: () => change("offsite", { offsite: "server" }, "The app now uses the server's bucket. hi copies it after its next snapshot.") }, "Use the server's bucket"),
      el("button", { class: "small", onclick: offsite }, "Use another bucket…"),
      el("button", { class: "small", onclick: () => change("offsite", { offsite: "off" }) }, "Do not copy offsite")),
    el("ul", { class: "bullets small muted" },
      el("li", {}, "The server's Backups sets one bucket for all apps."),
      el("li", {}, "Use another bucket only if this app must go somewhere else."),
      el("li", {}, "To see the encryption key of the backups, run hi app backups " + s.name + " --show-key.")));
  parts.push(el("h2", {}, "Jobs"), el("pre", { class: "job-output short" }, s.jobs),
    el("div", { class: "data-actions" }, el("a", { class: "button-like small", href: "/api/app/bom?name=" + encodeURIComponent(s.name) }, "Production bill of materials (CycloneDX)")));
  return parts;
}

async function restore(app, snapshot) {
  const answer = await ask({ title: "Restore " + app + " from " + snapshot + "?", text: "The memory of the lead, the record of the app and its data go back to that time. hi takes a snapshot of the current state first. Type the app name to continue.", ok: "Restore", typed: app, check: "Also restore the code (repo/)" });
  if (answer) change("restore", { snapshot, code: answer.checked, confirm: app }, "Restoring " + app);
}
async function rollback(app, version) {
  const answer = await ask({ title: "Roll " + app + " back to v" + version + "?", text: "Production goes back to v" + version + ", with the data from that version. hi keeps newer data in a snapshot. Type the app name to continue.", ok: "Roll back", typed: app, check: "Only the code. The production data does not change." });
  if (answer) change("rollback", { version, code: answer.checked, confirm: app }, "Rolling " + app + " back");
}
async function offsite() {
  const answer = await ask({ title: "Another bucket for this app", text: "Only for an app that must not use the server's bucket. Give an S3-compatible bucket, as s3:https://<host>/<bucket>. Add /<app> to keep each app in its own folder. Then give its region and keys.", ok: "Next", field: "s3:https://s3.eu-central-003.backblazeb2.com/my-bucket/app", fieldCheck: (v) => (!v.startsWith("s3:") ? "The address must start with s3:" : /^s3:(https?:\/\/)?[^/]+\/[^/]+/.test(v) ? "" : "Add the bucket name after the host: s3:https://<host>/<bucket>") });
  if (!answer) return;
  const region = await ask({ title: "Region of the bucket", text: "For example eu-central-003 for Backblaze, from the host s3.eu-central-003.backblazeb2.com. Leave it empty to use us-east-1.", ok: "Next", field: "us-east-1", fieldValue: bucketRegion(answer.field), fieldCheck: (v) => (/^[a-z0-9-]*$/.test(v) ? "" : "Use only small letters, digits, and -.") });
  if (!region) return;
  const id = await ask({ title: "Access key ID of the bucket", text: "hi keeps it on the machine and never shows it again.", ok: "Next", field: "Access key ID" });
  if (!id) return;
  const secret = await ask({ title: "Secret access key of the bucket", text: "hi keeps it on the machine and never shows it again.", ok: "Save", field: "Secret access key" });
  if (!secret) return;
  change("offsite", { offsite: answer.field, region: region.field, key_id: id.field, secret: secret.field }, "hi now also copies the snapshots offsite.");
}
// bucketRegion is the region in a bucket's host, when the host names it.
function bucketRegion(address) {
  const host = address.replace(/^s3:/, "").replace(/^https?:\/\//, "").split("/")[0].toLowerCase();
  const m = host.match(/^s3[.-]([a-z0-9-]+)\.(?:backblazeb2|wasabisys|amazonaws)\.com$/);
  return m ? m[1] : "";
}

// The machine's Network.
let machineNet = null;
async function refreshMachineNetwork() {
  try { machineNet = await api("/api/network"); } catch (e) { return; }
  if (viewShown("network")) renderMachineNetwork();
}
function renderMachineNetwork() {
  const box = $("network-view");
  const n = machineNet;
  if (!n) { box.replaceChildren(el("p", { class: "muted pad" }, "…")); return; }
  const domain = el("input", { value: n.domain || "", placeholder: "apps.example.com", "aria-label": "App domain" });
  const token = el("input", { type: "password", placeholder: n.has_token ? "NetBird token saved. A new token replaces it." : "NetBird API token", "aria-label": "NetBird token" });
  const apps = el("ul", { class: "list" });
  for (const a of n.apps) {
    apps.append(el("li", {}, el("span", { class: "what" }, el("button", { class: "link-button", onclick: async () => { await openView(a.name); showSection("network"); } }, a.name),
      el("span", { class: "muted small block" }, (a.joined ? "" : "not on NetBird · ") + (a.peer ? a.peer + " · " : "") + a.addresses + (a.allow_ips && a.allow_ips.length ? " · only " + a.allow_ips.join(", ") : "")))));
  }
  const pName = el("input", { placeholder: "Name, for example google", "aria-label": "Provider name" });
  const pIssuer = el("input", { placeholder: "Issuer, https://…", "aria-label": "Issuer" });
  const pClient = el("input", { placeholder: "Client ID", "aria-label": "Client ID" });
  const pSecret = el("input", { type: "password", placeholder: "Client secret (hi never shows it)", "aria-label": "Client secret" });
  const parts = [...adminPageAddress(n),
    el("h2", {}, "App domain"), el("p", { class: "muted small" }, n.domain ? "Apps can have fixed names under " + n.domain + "." : "NetBird makes up the addresses of the apps. With a domain verified in NetBird, apps get fixed names."),
    el("div", { class: "stack" }, domain, token, el("div", { class: "data-actions" }, el("button", { class: "small", onclick: async () => { await netChange("domain", { name: domain.value.trim(), key: token.value.trim() }); } }, "Save"), n.domain ? el("button", { class: "small", onclick: () => netChange("domain", { name: "off" }) }, "Off") : "")),
    el("h2", {}, "App peers"), apps,
    el("h2", {}, "Sign-in providers"), el("pre", { class: "job-output short" }, n.sso),
    el("details", { class: "small" }, el("summary", {}, "Add a provider"), el("div", { class: "stack" }, pName, pIssuer, pClient, pSecret,
      el("button", { class: "small", onclick: () => netChange("provider", { sso: pName.value.trim(), host: pIssuer.value.trim(), name: pClient.value.trim(), secret: pSecret.value.trim() }) }, "Check and add"))),
  ];
  parts.push(...machineSites(n.sites));
  if (n.expose) parts.push(el("h2", {}, "Server for cloud runs"), el("p", { class: "small" }, el("code", {}, shown(n.expose))));
  parts.push(el("h2", {}, "What to ask your IT team"), el("p", { class: "muted small" }, "Text to send to your IT team. It lists the policies and records that the admin page and the apps need."),
    el("pre", { class: "job-output copyable" }, n.ask || "Nothing."), el("div", { class: "data-actions" }, el("button", { class: "small", onclick: () => copyText(n.ask) }, "Copy")));
  box.replaceChildren(...parts);
}
// The admin page's own address: where it is, and its name under the
// machine's domain (hi server address), which hi keeps.
function adminPageAddress(n) {
  const parts = [el("h2", {}, "Admin page address"), el("p", { class: "small" }, "Now: ", el("a", { href: n.page_url }, n.page_url))];
  if (n.page_host) {
    parts.push(el("ul", { class: "bullets small muted" },
      el("li", {}, "Your IT team keeps this name."),
      el("li", {}, "To have hi keep a name under this machine's domain instead: hi server address default, then set the name here.")));
    return parts;
  }
  if (!n.page_domain) {
    parts.push(el("ul", { class: "bullets small muted" },
      el("li", {}, "This machine has no domain. With one (hi net domain), the page gets a name under it, on https."),
      el("li", {}, "A name your IT team keeps: hi server address <host>.")));
    return parts;
  }
  const name = el("input", { value: n.page_name || "admin", placeholder: "admin", "aria-label": "Name of the admin page", pattern: "[a-z0-9]([a-z0-9-]*[a-z0-9])?", maxlength: "63" });
  const save = async () => {
    const v = name.value.trim().toLowerCase();
    if (!v || v === (n.page_name || "admin")) return;
    if (!(await ask({ title: "Move the admin page to " + v + "." + n.page_domain + "?", text: "hi puts the new name in the zone and removes the old name. The old address stops working. Open the page at the new address after about a minute.", ok: "Move" }))) return;
    await netChange("page-address", { name: v });
  };
  name.addEventListener("keydown", (e) => { if (e.key === "Enter") { e.preventDefault(); save(); } });
  parts.push(el("div", { class: "data-actions page-name" }, name, el("span", { class: "muted" }, "." + n.page_domain), el("button", { class: "small", onclick: save }, "Save"),
    n.page_name && n.page_name !== "admin" ? el("button", { class: "small", onclick: () => { name.value = "admin"; save(); } }, "Use admin") : null),
    el("ul", { class: "bullets small muted" },
      el("li", {}, "hi keeps the name in the zone, with the certificate for *." + n.page_domain + "."),
      el("li", {}, "The NetBird policy to the page needs TCP 443."),
      el("li", {}, "A name your IT team keeps instead: hi server address <host>.")));
  return parts;
}

// What every app's dev box reaches beyond its own list: the sites an
// admin allowed, the organisation's domains, and sites several apps'
// owners allowed, to allow for all.
function machineSites(s) {
  if (!s) return [];
  const site = el("input", { placeholder: "docs.example.com", "aria-label": "Site for all dev boxes" });
  const why = el("input", { placeholder: "Reason", "aria-label": "Why the dev boxes need the site" });
  const allowed = el("ul", { class: "list" });
  for (const a of s.allowed) {
    allowed.append(el("li", {}, el("span", { class: "what" }, el("code", {}, a.host),
      el("span", { class: "muted small block" }, [a.by, (a.at || "").slice(0, 10), a.why].filter(Boolean).join(" · "))),
      el("button", { class: "small", onclick: () => netChange("site", { name: a.host, remove: true }) }, "Remove")));
  }
  if (!s.allowed.length) allowed.append(el("li", { class: "muted small" }, "None yet."));
  const parts = [
    el("h2", {}, "Dev box sites"),
    el("p", { class: "muted small" }, "All dev boxes can reach these sites. They can also reach the sites that development needs, their own stage and dev page, and the sites that their owners allowed."),
    allowed,
    el("div", { class: "stack" }, site, why, el("div", { class: "data-actions" }, el("button", { class: "small", onclick: () => { if (site.value.trim()) netChange("site", { name: site.value.trim(), key: why.value.trim() }); } }, "Allow for all apps"))),
  ];
  if (s.org.length) {
    parts.push(el("p", { class: "small" }, s.org_on ? "All dev boxes can reach the organisation domains, " + s.org.join(", ") + ". This does not include the production of an app." : "The dev boxes cannot reach the organisation domains, " + s.org.join(", ") + ".",
      " ", el("button", { class: "small", onclick: () => netChange("sites-org", { remove: s.org_on }) }, s.org_on ? "Off" : "On")));
  } else {
    parts.push(el("p", { class: "muted small" }, "After you set the organisation domains (in People), the dev boxes can also reach them."));
  }
  if (s.shared.length) {
    const shared = el("ul", { class: "list" });
    for (const x of s.shared) {
      shared.append(el("li", {}, el("span", { class: "what" }, el("code", {}, x.host), el("span", { class: "muted small block" }, "Allowed in " + x.apps.join(", "))),
        el("button", { class: "small", onclick: () => netChange("site", { name: x.host }) }, "Allow for all apps")));
    }
    parts.push(el("h3", {}, "Allowed in several apps"), shared);
  }
  return parts;
}
async function netChange(what, fields) {
  try { const answer = await api("/api/network", Object.assign({ what }, fields)); toast(firstLine(answer.text) || "Done.", true); refreshMachineNetwork(); } catch (e) { if (!(e instanceof SignIn)) toast(e.message, true); }
}

// Advisories: every app's, worst first.
let advisories = null;
async function refreshAdvisories() {
  try { advisories = await api("/api/advisories"); } catch (e) { return; }
  renderAdvisories();
}
// checkPending says an app's ask to check its advisories waits or runs.
function checkPending(c) { return !!(c && c.asked && !c.error); }
// advisoriesChecked is the line above the list: when production's packages
// were last matched, or that a check waits, runs, or failed.
function advisoriesChecked() {
  const checks = advisories.checks || {};
  if (openApp) {
    const c = checks[openApp];
    if (!c) return "";
    if (c.error) return "The check failed: " + c.error + ".";
    if (c.started && !c.error) return "Checking the packages of production " + c.bom + ", since " + ago(c.started) + "…";
    if (c.asked) return "Asked " + ago(c.asked) + ". The app starts the check within one minute.";
    if (!c.bom) return "Nothing published, so there is nothing to check.";
    if (!c.matched) return "Production " + c.bom + " is not checked yet.";
    return "Production " + c.bom + " checked " + ago(c.matched) + ". hi checks again each day.";
  }
  const all = Object.values(checks);
  const pending = all.filter(checkPending).length;
  const failed = Object.entries(checks).filter(([, c]) => c.error).map(([app]) => app);
  if (pending) return "Checking " + pending + (pending === 1 ? " app" : " apps") + "…";
  const times = all.map((c) => c.matched).filter(Boolean).sort();
  let text = times.length ? "Oldest check " + ago(times[0]) + ", newest " + ago(times[times.length - 1]) + "." : "";
  if (failed.length) text += " Failed: " + failed.join(", ") + ".";
  return text.trim();
}
let advisoriesPoll = 0;
async function checkAdvisories() {
  const button = $("advisories-check");
  button.disabled = true;
  try {
    const answer = await api("/api/advisories/check", { app: openApp || "" });
    const skipped = Object.entries(answer.skipped || {}).map(([app, why]) => app + ": " + why);
    toast((answer.asked.length ? "Checking " + answer.asked.join(", ") + ". This takes a few minutes." : "Nothing to check.") + (skipped.length ? " Not " + skipped.join("; ") + "." : ""), skipped.length > 0);
  } catch (e) { if (!(e instanceof SignIn)) toast(e.message, true); }
  refreshAdvisories();
}
function renderAdvisories() {
  const list = $("advisories-list");
  if (!advisories) return;
  const all = advisories.advisories.filter((a) => !openApp || a.app === openApp);
  badge("advisories-count", advisories.advisories.filter((a) => (a.tier === "kev" || a.tier === "incident") && (!openApp || a.app === openApp)).length);
  if (!viewShown("advisories")) return;
  const checks = advisories.checks || {};
  const pending = openApp ? checkPending(checks[openApp]) : Object.values(checks).some(checkPending);
  const c = openApp ? checks[openApp] : null;
  $("advisories-checked").textContent = advisoriesChecked();
  const button = $("advisories-check");
  button.textContent = pending ? "Checking…" : "Check now";
  button.disabled = pending || (openApp ? !c || !c.bom || !c.up : !Object.values(checks).some((x) => x.bom && x.up));
  button.title = openApp && c && !c.up ? openApp + " does not run. Its watcher checks when it starts." : "Check the production packages of " + (openApp || "all apps") + " against advisories now. hi also checks once a day.";
  button.onclick = checkAdvisories;
  clearTimeout(advisoriesPoll);
  if (pending) advisoriesPoll = setTimeout(() => { if (viewShown("advisories")) refreshAdvisories(); }, 5000);
  list.replaceChildren();
  if (!openApp) list.append(el("li", { class: "empty" }, "Advisories of all apps. Open an app to see only its advisories."));
  if (!all.length) list.append(el("li", { class: "empty" }, "No advisories for the production packages." + Object.entries(advisories.notes || {}).filter(([app]) => !openApp || app === openApp).map(([app, note]) => " " + app + ": " + note + ".").join("")));
  const words = { kev: "exploited", incident: "act now", report: "for the weekly report", "": "ignored" };
  for (const a of all) {
    const cls = a.tier === "kev" ? "bad" : a.tier === "incident" ? "blocked" : a.tier === "" ? "done" : "";
    list.append(el("li", { class: "incident" },
      el("div", { class: "incident-head" }, el("span", { class: "dot " + cls, "aria-hidden": "true" }), openApp ? null : el("button", { class: "link-button", onclick: () => openView(a.app) }, a.app), el("strong", {}, a.id), el("span", { class: "muted small" }, words[a.tier])),
      el("div", { class: "small" }, a.package + " " + a.version + " (" + (a.part === "machine" ? "Linux kernel of the machine" + (a.kernel ? ", " + a.kernel : "") : a.part) + ")" + ((a.fixed || []).length ? ", fixed in " + a.fixed.join(", ") : "") + (a.epss >= 0.01 ? ", EPSS " + a.epss.toFixed(2) : "")),
      a.summary ? el("div", { class: "muted small clamp2", title: a.summary }, a.summary) : null,
      a.action ? el("div", { class: "small advisory-action" }, el("strong", {}, a.part === "machine" ? "The machine needs an update. " : "Fix: "), a.action) : null,
      a.ignored ? el("div", { class: "muted small" }, "Ignored: " + a.ignored) : null,
      el("div", { class: "data-actions" }, a.tier === "" ? el("button", { class: "small", onclick: () => advisoryChange(a, true) }, "Show again") : el("button", { class: "small", onclick: () => advisoryChange(a, false) }, "Ignore…"))));
  }
}
async function advisoryChange(a, unignore) {
  let because = "";
  if (!unignore) {
    const answer = await ask({ title: "Ignore " + a.id + " in " + a.app + "?", text: "You say that it does not apply. This stays until the advisory changes. The people of the app see your reason.", ok: "Ignore", why: "Why it does not apply" });
    if (!answer) return;
    because = typeof answer === "string" ? answer : "";
  }
  try {
    await api("/api/app/settings", { app: a.app, what: "advisory", id: a.id, package: a.package, because, remove: unignore });
    toast(unignore ? "hi shows " + a.id + " again if it matches." : a.id + " is ignored.");
    refreshAdvisories();
  } catch (e) { if (!(e instanceof SignIn)) toast(e.message, true); }
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
  if (orgOpen() && !state.org) setMiddle("");
  if (renderTabs() && panes.right.open) freshView(panes.right.view);
  renderBar();
  renderApps();
  if (!middle) renderHome();
  renderOrgBadges();
  renderJobsBadge();
  renderVaultBanner();
  if (openApp && $("overview-usage")) $("overview-usage").textContent = usageLine(openApp) || "…";
  if (openApp && detail && detail.boxes) boxUsage(detail.name, detail.boxes);
  if (viewShown("machine")) renderMachine();
  else badge("machine-badge", state.latest ? 1 : 0);
  return true;
}

function renderOrgBadges() {
  const o = state.org;
  badge("requests-count", o ? o.waiting : 0);
  badge("running-count", o ? o.running : 0);
  // Who may change what: the page shows the rest, and changes nothing.
  document.body.classList.toggle("no-org-changes", !!o && !(state.me && state.me.org));
  document.body.classList.toggle("no-machine-changes", !(state.me && state.me.machine));
}

let pollTimer = 0;
let settingsShownAt = 0;
let advisoriesAt = 0;
let settingsAt = 0;
async function poll() {
  clearTimeout(pollTimer);
  const ok = await refreshState();
  if (ok) {
    if (openApp && appSection === "overview") refreshDetail();
    if (openApp && appSection !== "overview" && appSection !== "data" && Date.now() - settingsShownAt > 10000) { settingsShownAt = Date.now(); refreshSettings(); }
    if (Date.now() - advisoriesAt > 60000) { advisoriesAt = Date.now(); refreshAdvisories(); }
    for (const view of [panes.right.view]) if (viewShown(view) && view !== "logs" && view !== "spend") freshView(view);
    // The server's settings: every fifteen seconds while one is open.
    if (orgOpen() && orgSection === "vault" && Date.now() - settingsAt > 15000) { settingsAt = Date.now(); refreshVault(); }
    else if (orgOpen() && orgSection !== "overview" && Date.now() - settingsAt > 15000) { settingsAt = Date.now(); refreshOrgSettings(); }
    if (orgOpen() && orgSection === "overview") renderOrgSection();
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
    document.body.replaceChildren(el("p", { class: "locked" }, "The admin page does not answer: " + e.message));
    return;
  }
  $("signin").hidden = true;
  $("work").hidden = false;
  booted = true;
  if (window.hiPresence) hiPresence.start();
  renderTabs();
  renderPanes();
  let wanted = "";
  try { wanted = decodeURIComponent(location.hash.slice(1)); } catch (e) { /* a malformed #… opens Home */ }
  renderBar();
  renderApps();
  renderHome();
  renderOrgBadges();
  renderVaultBanner();
  const [wantedApp, wantedSection] = wanted.split("/");
  if (wantedApp && state.apps.some((a) => a.name === wantedApp)) await openView(wantedApp, ["people", "agents", "slack", "settings", "network", "data", "spend"].includes(wantedSection) ? wantedSection : "");
  else if (wantedApp === ":org" && state.org) openOrg(wantedSection || "");
  else if (wanted === ":policy" && state.org) openOrg("policy");
  else if (wanted === ":new" || wanted.startsWith(":new/")) openNew(wanted.slice(5));
  else if (wanted.startsWith(":person/")) openPerson(wanted.slice(8));
  for (const which of ["left", "right"]) if (panes[which].open) freshView(panes[which].view);
  refreshJobs();
  pollTimer = setTimeout(poll, 3000);
}

boot();
