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
let middle = "";       // what the middle shows: "", an app's name, ":org", or ":new…"
let orgSection = "overview"; // the org's section, with ":org" open
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
  if (view === "keys") { renderKeys(); refreshMachineKeys(); }
}

// The left panel is the same on every machine. The right is about what's
// open: the org's views with the org, an app's with an app; on Home,
// every app's, or the org's on a machine that runs only the org.
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
  try {
    const answer = await api("/api/new-link", {});
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

// movedOpen says the open app moved to another machine: it has no
// sections, menu, or logs here.
function movedOpen() {
  return !!(openApp && state && (state.moved || []).some((m) => m.app === openApp) && !state.apps.some((a) => a.name === openApp));
}

function renderBar() {
  $("machine").textContent = state.machine;
  $("home-machine").textContent = state.machine;
  const named = openApp || (orgOpen() ? "The org" : middle === ":new" ? "New app" : middle.startsWith(":new/") ? "New app " + middle.slice(5) : "");
  $("crumb").hidden = !named;
  $("sections").hidden = !openApp || movedOpen();
  $("org-sections").hidden = !orgOpen();
  $("open-box").hidden = !openApp || movedOpen();
  $("crumb-app").textContent = named;
  document.title = (named ? named + " · " : "") + "Admin page · " + state.machine;
  $("bar-note").textContent = restarting ? "The admin page is restarting…" : state.running ? (state.running === 1 ? "A job runs" : state.running + " jobs run") : "";
}

function renderApps() {
  const query = $("apps-search").value.trim().toLowerCase();
  const list = $("apps-list");
  // The Open menu may sit on a row: out first, or it goes with the row.
  if (list.contains($("open-box"))) $("right-toggle").before($("open-box"));
  list.replaceChildren();
  const apps = state.apps.filter((a) => !query || a.name.includes(query) || (a.purpose || "").toLowerCase().includes(query));
  // The org first, on a machine that runs it: opened as an app is.
  const o = state.org;
  if (o && (!query || "the org".includes(query))) {
    const li = el("li", { class: orgOpen() ? "current" : "" }, el("span", { class: "dot " + (o.up && !o.no_owner ? "idle" : "bad"), "aria-hidden": "true" }),
      el("button", { class: "stask-row", onclick: () => openOrg(""), title: "The org server: its requests, users, admins, policy, and keys" },
        el("span", { class: "stask-title" }, "The org"), el("span", { class: "stask-num" }, o.up ? (o.running ? o.running + " running" : "") : "doesn't answer")));
    if (o.waiting) li.append(el("span", { class: "count", title: o.waiting + " request" + (o.waiting === 1 ? "" : "s") + " wait" + (o.waiting === 1 ? "s" : "") }, o.waiting));
    list.append(li);
  }
  if (!state.apps.length) list.append(el("li", { class: "empty" }, (state.moved || []).length ? "No apps run on this machine now." : o ? "No apps on this machine." : "No apps on this machine yet."));
  else if (!apps.length) list.append(el("li", { class: "empty" }, "No app matches."));
  for (const app of apps) {
    const row = el("button", { class: "stask-row", onclick: () => openView(app.name), title: app.purpose || app.name },
      el("span", { class: "stask-title" }, app.name),
      el("span", { class: "stask-num" }, app.state === "up" ? (app.production ? "v" + app.production : "") : stateWords[app.state] || app.state));
    const li = el("li", { class: app.name === openApp ? "current" : "" }, stateDot(app.state), row);
    const unseen = unseenIncidents(app.name, app.incidents_opened);
    if (unseen) li.append(el("span", { class: "count", "data-app": app.name, title: unseen + " open incident" + (unseen === 1 ? "" : "s") + " not seen in Incidents yet" }, unseen));
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
  // Home's number: apps that aren't up, and the org's requests waiting.
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
  $("home-new").hidden = !state.me.machine;
  const note = $("home-note");
  note.hidden = !!state.me.machine;
  note.textContent = state.me.machine ? "" : "You're an admin of the org: you see the machine and its apps here, and the machine's admins change them.";
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
      el("p", {}, state.org ? "No apps on this machine: it runs the org server." : "No apps on this machine yet."),
      el("p", { class: "muted" }, "Make one with + New app, or on the machine with ", el("code", {}, "hi app new <name>"), ".")));
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

  // Where it is.
  // Every address goes through the app's NetBird peer: until it joins,
  // they're names nobody reaches, so they don't open.
  const where = el("ul", { class: "list where-list" });
  for (const address of d.addresses) {
    where.append(el("li", {}, el("span", { class: "what" }, el("strong", {}, address.what), " ", el("span", { class: "muted small" }, shown(address.url))),
      d.joined ? el("a", { class: "button-like small", href: address.url, target: "_blank", rel: "noopener noreferrer" }, "Open ↗") : ""));
  }
  where.append(el("li", {}, el("span", { class: "what" }, el("strong", {}, "Dev page"), " ", el("span", { class: "muted small" }, d.dev_page || "while the app runs")),
    d.joined ? el("button", { class: "small", onclick: () => openDevPage(d.name) }, "Open ↗") : ""));
  parts.push(el("h2", {}, "Addresses"));
  if (!d.joined) {
    parts.push(el("p", { class: "note warn" }, "Not reachable yet: " + d.name + " hasn't joined NetBird, which its addresses go through. Give it a setup key in ",
      el("button", { class: "link-button", onclick: () => showSection("network") }, "Network"), "."));
  }
  parts.push(where);

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

// The Open ▾ menu: production, Stage, and the dev page. It sits on the open
// app's row in the apps list, next to where the app was picked; with the
// list not shown (another view, the panel closed, or a search that leaves
// the app out), in the bar, where it was before.
function placeOpenBox() {
  const openBox = $("open-box"), openBoxHome = $("right-toggle");
  const row = viewShown("apps") ? $("apps-list").querySelector("li[data-app]") : null;
  if (row) {
    if (openBox.parentNode !== row) row.append(openBox);
  } else if (openBox.nextSibling !== openBoxHome) {
    openBoxHome.before(openBox);
  }
  openBox.classList.toggle("in-list", Boolean(row));
  if (!$("open-menu").hidden) placeOpenMenu();
}
// In the list, the menu opens fixed under its button: the panel would clip it.
function placeOpenMenu() {
  const menu = $("open-menu");
  if (!$("open-box").classList.contains("in-list")) {
    menu.style.position = menu.style.top = menu.style.left = menu.style.right = "";
    return;
  }
  const at = $("open-button").getBoundingClientRect();
  menu.style.position = "fixed";
  menu.style.right = "auto";
  menu.style.top = at.bottom + 4 + "px";
  menu.style.left = Math.max(8, Math.min(at.left, window.innerWidth - menu.offsetWidth - 8)) + "px";
}
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
  if (!menu.hidden) placeOpenMenu();
});
$("apps-list").addEventListener("scroll", closeMenu);
window.addEventListener("resize", closeMenu);
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
  version.replaceChildren(el("p", {}, el("strong", {}, "hi " + state.version), state.latest ? el("span", { class: "warn" }, " · " + state.latest + " is out") : el("span", { class: "muted" }, " · the newest, as far as GitHub said")));
  version.append(el("button", { class: state.latest ? "primary" : "", onclick: update }, state.latest ? "Update to " + state.latest : "Update hi"));
  version.append(el("p", { class: "muted small" }, (state.org ? "The org server, the apps, and this page restart on the new hi" : "The apps and this page restart on the new hi") + ": each app's lead is away for about half a minute, the page for a second."));
  badge("machine-badge", state.latest ? 1 : 0);

  const reach = $("machine-reach");
  reach.replaceChildren(
    el("p", {}, el("a", { href: state.address }, state.address)),
    el("p", { class: "muted small" }, state.line));
  if (state.policy) reach.append(el("p", { class: "small" }, "What to ask your IT team: ", el("span", { class: "copyable" }, state.policy), "."));
  // What the apps need on this machine, Start at boot among it, with
  // Install (hi_admin.md, release 8).
  reach.append(el("h2", {}, "What the apps need here"), needsBox("", null));
  $("netbird-form").hidden = !state.needs_key;
  $("machine-user").textContent = state.user;

  const list = $("admins-list");
  list.replaceChildren();
  // One list, the org's, on a machine that runs only the org; the
  // machine's own beside an org that keeps its admins apart.
  const orgAdmins = !!(state.org && !state.org.no_owner && !state.org_apart);
  const canChange = (!orgAdmins || state.me.owner) && state.me.machine;
  $("admin-add-open").hidden = !canChange;
  $("admin-add-email").hidden = !orgAdmins;
  $("admin-add-slack").required = orgAdmins;
  $("admin-add-slack").placeholder = orgAdmins ? "Their Slack member ID" : "Their Slack member ID (optional)";
  $("admin-add-name").placeholder = orgAdmins ? "Their hi user name, such as olga" : "Their name";
  $("admins-note").hidden = !orgAdmins && !state.org_apart;
  $("admins-note").textContent = orgAdmins ? "They are the org's owner and admins: each approves too, in Slack and here." + (state.me.owner ? "" : " The owner adds and removes them.")
    : state.org_apart ? "The machine's admins: they run it and its apps. The org's owner and admins run the org, in the org's Admins; making someone an admin here doesn't make them an approver." : "";
  for (const admin of state.admins.filter((a) => !state.org_apart || a.machine)) {
    const me = admin.name === state.me.name;
    const acts = el("span", { class: "actions" });
    acts.append(el("button", { onclick: () => newLink(admin, false), title: "A new link, shown to copy; their old one ends" }, "New link"));
    if (admin.slack) acts.append(el("button", { onclick: () => newLink(admin, true), title: "A new link in their Slack DMs; their old one ends" }, "By Slack"));
    if (!me && canChange && (!admin.owner || state.org_apart)) acts.append(el("button", { class: "danger", onclick: () => removeAdmin(admin) }, "Remove"));
    const what = el("span", { class: "what" }, el("strong", {}, admin.name), me ? el("span", { class: "muted" }, " (you)") : null, admin.owner ? el("span", { class: "muted" }, " · the org's owner") : state.org_apart && admin.org ? el("span", { class: "muted" }, " · an admin of the org too") : null,
      el("span", { class: "muted small block" }, (admin.slack ? "Slack " + admin.slack : "no Slack member ID") + (admin.email ? " · " + admin.email : "")));
    const li = el("li", {}, what, acts);
    list.append(li);
    if (me && !admin.slack && !orgAdmins && !admin.org) {
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
// Other machines, and an app's Move… (hi_app_move.md): this machine's key,
// the machines it moves apps to, and the ones it takes apps from.

let machines = null;

async function refreshMachines() {
  const box = $("machines-view");
  if (!machines) box.replaceChildren(el("p", { class: "muted small" }, "Asking the other machines…"));
  try { machines = await api("/api/machines"); } catch (e) { if (!(e instanceof SignIn)) box.replaceChildren(el("p", { class: "error small" }, e.message)); return; }
  renderMachines();
}

function machineState(m) {
  if (m.takes) return el("span", { class: "muted small" }, " takes apps from here" + (m.version ? " · hi " + m.version : ""));
  return el("span", { class: "warn small" }, " " + (m.problem || "doesn't answer"));
}

function renderMachines() {
  const box = $("machines-view");
  if (!machines) return;
  const parts = [el("p", { class: "muted small" }, "Apps move between machines with hi, at the same addresses. A machine takes apps from another once it has that machine's key; the key lets it only receive and start apps, not log in.")];

  // Where this machine moves apps to.
  const to = el("ul", { class: "list machines-list" });
  for (const m of machines.to) {
    to.append(el("li", {}, el("span", { class: "what" }, el("span", { class: "dot " + (m.takes ? "idle" : "bad"), "aria-hidden": "true" }), el("strong", {}, m.name), machineState(m)),
      el("span", { class: "actions" }, el("button", { class: "small", onclick: () => forgetMachine(m.name) }, "Remove"))));
    if (!m.takes) to.append(el("li", { class: "plain" }, machineFix(m)));
  }
  if (!machines.to.length) to.append(el("li", { class: "empty" }, "None yet: add one here, or in an app's Move…."));
  const addTo = el("form", { class: "signin-row", autocomplete: "off" }, input({ placeholder: "Its name on NetBird, as this one's is " + machines.machine, title: "The other machine's name on NetBird, as this one's is " + machines.machine, "aria-label": "A machine to move apps to", required: true }), el("button", { type: "submit" }, "Add"));
  addTo.addEventListener("submit", async (event) => {
    event.preventDefault();
    try { await api("/api/machines", { action: "to", name: addTo.querySelector("input").value.trim() }); addTo.reset(); await refreshMachines(); } catch (e) { if (!(e instanceof SignIn)) toast(e.message, true); }
  });
  parts.push(el("h3", {}, "Moves apps to"), to, addTo);

  // This machine's key, for another machine to take apps from it.
  parts.push(el("h3", {}, "This machine's key"),
    el("p", { class: "muted small" }, "For a machine to take apps from this one, paste this on its admin page, under Takes apps from, or run it there as the account that runs hi:"),
    el("div", { class: "key-line" }, el("code", {}, machines.line), el("button", { class: "small", onclick: () => copyText(machines.line) }, "Copy")));

  // Where this machine takes apps from.
  const from = el("ul", { class: "list machines-list" });
  for (const name of machines.from) {
    from.append(el("li", {}, el("span", { class: "what" }, el("strong", {}, name)), el("span", { class: "actions" }, el("button", { class: "small danger", onclick: () => untakeMachine(name) }, "Remove"))));
  }
  if (!machines.from.length) from.append(el("li", { class: "empty" }, "None: no machine can move apps here."));
  const pasted = el("textarea", { rows: "2", maxlength: "800", placeholder: "The other machine's key: the line its admin page shows under This machine's key", "aria-label": "The other machine's key", required: true });
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
      el("p", {}, "On " + m.name + "'s admin page, in Machine, Other machines, paste this under Takes apps from (or run it there as the account that runs hi):"),
      el("div", { class: "key-line" }, el("code", {}, machines.line), el("button", { class: "small", onclick: () => copyText(machines.line) }, "Copy")),
      el("div", { class: "data-actions" }, again));
  }
  return el("div", { class: "machine-fix small" },
    el("p", {}, "Check its name: it's the machine's name on NetBird, as this one's is " + machines.machine + ". The machine needs to be on, on NetBird, and to have hi."),
    el("div", { class: "data-actions" }, again));
}

// movedView is an app that moved to another machine: where, and how the
// move went.
function movedView(m) {
  const seconds = (n) => (n >= 90 ? Math.round(n / 60) + " minutes" : n + " seconds");
  const facts = el("dl", { class: "facts" });
  const fact = (term, value) => { if (value) facts.append(el("dt", {}, term), el("dd", {}, value)); };
  fact("Moved", new Date(m.at).toLocaleString() + " (" + ago(m.at) + ")" + (m.by ? ", by " + m.by : ""));
  fact("Copied", m.size ? size(m.size) + ": its folder, secrets, NetBird identity, and snapshots" : "");
  fact("Took", m.took ? seconds(m.took) : "");
  fact("Stopped for", m.stopped ? seconds(m.stopped) + ", while the last changes were copied and it started there" : "");
  fact("Snapshot", m.snapshot ? m.snapshot + ", taken here just before" : "");
  fact("Its folder here", m.folder ? el("code", {}, m.folder) : "");
  const actions = el("div", { class: "data-actions" });
  if (m.admin) actions.append(el("a", { class: "button-like primary", href: m.admin, target: "_blank", rel: "noopener noreferrer" }, "Open " + m.to + "'s admin page ↗"));
  actions.append(el("button", { onclick: () => forgetMoved(m.app) }, "Take it off this list"));
  return [
    el("div", { class: "admin-head" }, el("h1", { class: "admin-title" }, m.app), el("span", { class: "pill open" }, "moved"), el("span", { class: "muted" }, "to " + m.to)),
    el("p", { class: "note" }, m.app + " runs on " + m.to + " now, at the same addresses: its people, links, and Slack channel are as they were. Manage it on " + m.to + "'s admin page."),
    facts, actions,
    el("p", { class: "muted small" }, "To bring it back: on " + m.to + "'s admin page, open " + m.app + ", Move…, and pick this machine, " + (state && state.move_name ? state.move_name : "this one") + ". Its folder here is kept, so a move back copies only what changed. Taking it off this list keeps the folder.")];
}

async function forgetMoved(app) {
  try { toast((await api("/api/machines", { action: "forget-moved", name: app })).text); await refreshState(); openView(""); } catch (e) { if (!(e instanceof SignIn)) toast(e.message, true); }
}

async function forgetMachine(name) {
  try { await api("/api/machines", { action: "forget", name }); await refreshMachines(); } catch (e) { if (!(e instanceof SignIn)) toast(e.message, true); }
}

async function untakeMachine(name) {
  const ok = await ask({ title: "Stop taking apps from " + name + "?", text: name + " can no longer move apps here. Apps it moved here already stay.", ok: "Stop" });
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
  $("move-text").textContent = "The lead, production, Stage, and the dev page stop for about a minute, then run on the other machine at the same addresses, with the app's data, snapshots, and secrets. The folder here stays, with a note of where the app went.";
  $("move-ok").disabled = true;
  $("move-check").replaceChildren();
  const list = $("move-list");
  const draw = () => {
    list.replaceChildren();
    if (machines) $("move-add-name").placeholder = "Another machine: its name on NetBird, as this one's is " + machines.machine;
    const known = machines ? machines.to : [];
    for (const m of known) {
      const li = el("li", { role: "option", "aria-selected": String(m.name === picked), tabindex: "0" },
        el("span", { class: "what" }, el("span", { class: "dot " + (m.takes ? "idle" : "bad"), "aria-hidden": "true" }), el("strong", {}, m.name), machineState(m)));
      li.addEventListener("click", () => pick(m.name));
      li.addEventListener("keydown", (e) => { if (e.key === "Enter" || e.key === " ") { e.preventDefault(); pick(m.name); } });
      list.append(li);
    }
    if (!machines) list.append(el("li", { class: "empty" }, "Asking the other machines…"));
    else if (!known.length) list.append(el("li", { class: "empty" }, "No machine yet: add the one to move it to."));
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
        parts.push(el("p", { class: "small" }, "On " + view.machine + "'s admin page, in Machine, Other machines, paste this under Takes apps from (or run it there as the account that runs hi), then pick it again:"),
          el("div", { class: "key-line" }, el("code", {}, view.access), el("button", { class: "small", onclick: () => copyText(view.access) }, "Copy")));
      }
    } else {
      parts.push(el("p", { class: "small" }, view.machine + " runs hi " + view.version + "; the folder goes to ", el("code", {}, view.folder), " there."));
      if (view.problems.length) parts.push(el("p", { class: "error small" }, "What it lacks first:"), el("ul", { class: "small" }, ...view.problems.map((p) => el("li", {}, p))));
      if (view.warnings.length) parts.push(el("p", { class: "warn small" }, "What works differently there:"), el("ul", { class: "small" }, ...view.warnings.map((w) => el("li", {}, w))));
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
      toast("Moving " + app + " to " + machine + ": Jobs follows it.", true);
      followJob(job);
      const wait = async () => {
        let answer;
        try { answer = await api("/api/job?id=" + encodeURIComponent(job.id) + "&from=0"); } catch (e) { setTimeout(wait, 3000); return; }
        if (answer.job.state === "running") { setTimeout(wait, 3000); return; }
        await refreshState();
        if (openApp === app) { detail = null; refreshDetail(); }
        if (answer.job.state === "done") toast(app + " moved to " + machine + ", and runs there now.", true);
        else toast(app + " didn't move, and runs here as before: Jobs has why.", true);
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
// Logs are what's open: an app's, or the org server's; on Home, the org
// server's on a machine that runs only the org, or the admin page's own.
function logsApp() {
  const here = openApp && !movedOpen() ? openApp : "";
  const app = here || (state && state.org && (orgOpen() || !state.apps.length) ? ":org" : "");
  $("logs-whose").textContent = here ? here : app === ":org" ? "The org server" : "The admin page";
  $("logs-whose").title = here ? here + "'s lead and service" : app === ":org" ? "hi-org.service" : "Open an app, or the org, for its logs";
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

// Incidents seen: for each app (":org" for the org), when the newest open
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
    if (!whose && all.length) box.append(el("li", { class: "empty" }, state && state.org ? "Every app's, and the org's. Open one for its own." : "Every app's. Open an app for its own."));
    if (!list.length) box.append(el("li", { class: "empty" }, (whose === ":org" ? "The org has had no incidents: an instance on its accounts that no request started, or a failed start's machine left behind, shows here" : whose ? whose + " has had no incidents" : "No incidents") + (whose === ":org" ? "." : " in the last month.")));
    for (const incident of list) {
      const cls = incident.status === "closed" ? "done" : incident.status === "waiting" ? "blocked" : "bad";
      box.append(el("li", { class: "incident" },
        el("div", { class: "incident-head" }, el("span", { class: "dot " + cls, "aria-hidden": "true" }),
          whose ? null : el("button", { class: "link-button", onclick: () => (incident.app === ":org" ? openOrg("") : openView(incident.app)) }, incident.app === ":org" ? "the org" : incident.app), el("span", { class: "muted small" }, (incident.app === ":org" ? "" : incident.id + " · ") + incident.status + " · " + ago(incident.opened))),
        el("div", { class: "clamp3", title: incident.title }, incident.title),
        el("div", { class: "muted small clamp2", title: incident.last || "" }, incident.where + (incident.last ? " · " + incident.last : ""))));
    }
    if (viewShown("incidents")) markIncidentsSeen(list);
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
    if (r.waits && r.state === "pending") li.append(el("div", { class: "muted small" }, "Waits for a person: " + r.waits + "."));
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
      if (r.kind === "compute" && r.rate && r.policy_group && !r.over_budget && r.waits) {
        acts.append(el("button", { class: "small", title: "Approve it, and widen " + r.policy_group + "'s auto-approval so the like of it starts by itself", onclick: () => approveAndAllow(r) }, "Approve, and the like by itself…"));
      }
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
// the org, opened: its sections in the middle and on the bar, as an app's.
// Someone who isn't one of the org's admins sees them, and changes nothing.

const orgSectionNames = { overview: "Overview", users: "Users", admins: "Admins", policy: "Budgets and approval", keys: "Keys", templates: "Templates", data: "Data", models: "Models", slack: "Slack", walls: "Wall screens" };
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
  if (orgSection !== "overview" || !orgSettings) refreshOrgSettings();
}

function orgHead() {
  const o = state.org;
  return el("div", { class: "admin-head" }, el("h1", { class: "admin-title" }, "The org"),
    el("span", { class: "pill " + (o.up ? "open" : "closed") }, el("span", { class: "dot " + (o.up ? "idle" : "bad"), "aria-hidden": "true" }), o.up ? "runs" : "doesn't answer"),
    el("span", { class: "muted" }, orgSection === "overview" ? (o.owner ? "owned by " + o.owner : "") : orgSectionNames[orgSection]));
}
const orgCanChange = () => !!(state && state.me && state.me.org);
function orgReadOnlyNote() {
  return orgCanChange() ? null : el("p", { class: "note muted small" }, "You see the org: its owner and admins change it.");
}

function renderOrgSection() {
  if (!orgOpen() || orgSection === "policy") return;
  const box = $("orgview");
  const parts = [orgHead()];
  const note = orgReadOnlyNote();
  if (note) parts.push(note);
  const s = orgSettings;
  if (orgSection === "overview") parts.push(...orgOverview(s));
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
  if (o.no_owner) parts.push(el("p", { class: "warn" }, "No owner yet. On the machine: ", el("code", {}, "hi org owner <Slack member ID> --email <address>")));
  if (o.up) parts.push(el("div", { class: "card-box org-card" }, orgStats(o)));
  parts.push(heading("The org server"), orgServerBlock(s));
  if (state.org_apart) parts.push(el("p", { class: "muted small" }, "This machine runs apps too. Its admins run the apps; the org's owner and admins (Admins) run the org."));
  return parts;
}

function orgServerBlock(s) {
  const o = state.org;
  const block = el("div", { class: "machine-block" });
  block.append(el("p", { class: "incident-head" }, el("span", { class: "dot " + (o.up ? "idle" : "bad"), "aria-hidden": "true" }), o.up ? "The org server runs." : "The org server doesn't answer."));
  if (o.error) block.append(el("p", { class: "error small" }, o.error));
  if (s && s.listen) block.append(el("p", { class: "muted small" }, "Devices join with ", el("code", {}, "hi org join " + s.listen)));
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
  block.append(el("button", { class: "small", "data-nav": "", onclick: () => showView("logs") }, "Its logs"));
  return block;
}

// Users: the org's users, their groups and devices.
function orgUsers(s) {
  const users = s.users || [];
  const search = el("input", { type: "search", placeholder: "Find someone", "aria-label": "Find someone", autocomplete: "off", "data-nav": "" });
  const list = el("ul", { class: "list people-list" });
  const show = () => {
    const query = search.value.trim().toLowerCase();
    const shownUsers = users.filter((u) => !query || u.name.includes(query) || (u.group || "").includes(query));
    list.replaceChildren();
    if (!users.length) list.append(el("li", { class: "empty" }, "Nobody yet: devices ask to join with hi org join, and show up in Requests."));
    else if (!shownUsers.length) list.append(el("li", { class: "empty" }, "Nobody matches."));
    for (const u of shownUsers) {
      const devices = (u.devices || []).map((d) => d.hostname + (d.last_seen && new Date(d.last_seen).getTime() > 0 ? " (" + ago(d.last_seen) + ")" : ""));
      list.append(el("li", {}, el("span", { class: "what" }, el("strong", {}, u.name), " ", el("span", { class: "muted small" }, (u.group || "no group") + (u.kind === "agent" ? " · an agent of " + u.owner : u.kind === "viewer" ? " · a viewer" : "")),
        el("span", { class: "muted small block" }, devices.length ? devices.join(", ") : "no devices")),
        el("span", { class: "actions" }, el("button", { class: "danger", onclick: async () => {
          const ok = await ask({ title: "Remove " + u.name + "?", text: "They and their " + (u.devices || []).length + " devices leave the org: nothing more starts for them. What runs keeps running until it's stopped (Running). Type their name to say so.", ok: "Remove", typed: u.name });
          if (ok) orgDo({ what: "user", action: "remove", name: u.name }, u.name + " has left the org.");
        } }, "Remove"))));
    }
  };
  search.addEventListener("input", show);
  show();
  const add = inlineForm([
    { name: "name", placeholder: "Their hi user name, such as olga", required: true, max: "32", pattern: "[a-z0-9._\\-]+" },
    { name: "group", placeholder: "Their group, such as staff", required: true, max: "32", list: "org-groups" },
    { name: "key", placeholder: "A device's key, from hi org key on it (optional)" },
  ], async (v) => ((await orgDo({ what: "user", action: "add", name: v.name, group: v.group, key: v.key }, v.name + " is in the org.")) === null ? false : undefined), "Add");
  $("org-groups").replaceChildren(...((org && org.groups) || []).map((g) => el("option", { value: g })));
  return [el("p", { class: "purpose" }, "Everyone whose devices and agents ask the org for compute, by their hi user name, each in a group the policy sets limits for. They aren't the apps' people (People), nor this page's admins."),
    heading("Users", plus("Add a user", add)), add, el("div", { class: "logs-tools" }, search), list];
}

// Admins: the owner, and the admins, who approve and run the org.
function orgAdmins(s) {
  const owner = s.people.owner;
  const owns = !!(state.me && state.me.owner);
  const parts = [el("p", { class: "purpose" }, "The owner and the admins approve, in Slack and here, and change the org's users, policy, and keys. " +
    (state.org_apart ? "They sign in to this page for the org; the machine's admins (Machine) run the apps." : "On this machine they are the page's admins too."))];
  const handOn = inlineForm([
    { name: "slack", placeholder: "The new owner's Slack member ID", required: true, max: "20" },
    { name: "email", type: "email", placeholder: "Their email", required: true },
    { name: "name", placeholder: "Their hi user name (from their email if left out)", max: "32" },
  ], (v) => orgDo({ what: "owner", slack: v.slack, email: v.email, name: v.name }, "Handed on."), "Hand it on");
  parts.push(heading("Owner"));
  if (!owner) parts.push(el("p", { class: "warn small" }, "The org has no owner yet. On the machine: ", el("code", {}, "hi org owner <Slack member ID> --email <address>")));
  else {
    parts.push(el("div", { class: "machine-block" }, el("p", {}, el("strong", {}, owner.name), " ", el("span", { class: "muted small" }, "Slack " + owner.slack + (owner.email ? " · " + owner.email : ""))),
      owns ? el("div", { class: "data-actions" }, el("button", { class: "small", onclick: () => { handOn.hidden = !handOn.hidden; } }, "Hand it on…")) : null));
    parts.push(handOn);
  }
  const add = inlineForm([
    { name: "name", placeholder: "Their hi user name, such as olga", required: true, max: "32" },
    { name: "slack", placeholder: "Their Slack member ID", required: true, max: "20" },
    { name: "email", type: "email", placeholder: "Their email (optional)" },
  ], async (v) => {
    const answer = await orgDo({ what: "admins", action: "add", name: v.name, slack: v.slack, email: v.email });
    if (answer === null) return false;
    if (answer.link) showLink(v.name + " is an admin of the org", answer.text, answer.link); else toast(answer.text || "Done.");
  }, "Add");
  parts.push(heading("Admins", owns ? plus("Add an admin of the org", add) : null), add);
  const list = el("ul", { class: "list admins-list" });
  for (const a of s.people.admins || []) {
    list.append(el("li", {}, el("span", { class: "what" }, el("strong", {}, a.name), el("span", { class: "muted small block" }, "Slack " + a.slack + (a.email ? " · " + a.email : "") + (a.by ? " · added by " + a.by : ""))),
      owns ? el("span", { class: "actions" }, el("button", { class: "danger", onclick: async () => {
        if (await ask({ title: "Take " + a.name + " off the org's admins?", text: "They no longer approve, nor change the org." + (state.org_apart ? " If they're one of the machine's admins, they still run it." : " Their links to this page end."), ok: "Remove" })) {
          const answer = await orgDo({ what: "admins", action: "remove", name: a.slack });
          if (answer) toast(answer.text || "Done.");
        }
      } }, "Remove")) : null));
  }
  if (!(s.people.admins || []).length) list.append(el("li", { class: "plain muted small" }, "None besides the owner."));
  parts.push(list);
  const approvers = Object.entries(s.approvers || {}).sort((a, b) => a[1].localeCompare(b[1]));
  if (approvers.length) parts.push(el("p", { class: "muted small" }, "Approvers in Slack: " + approvers.map(([id, name]) => name).join(", ") + ". Someone who only approves: ", el("code", {}, "hi org approvers add"), "."));
  return parts;
}

// Keys: the providers' and the org's OpenRouter key, set or not, never shown.
function orgKeys(s) {
  const parts = [heading("Compute providers"), el("p", { class: "muted small" }, "The org server starts machines with these. Each key is checked with its provider before it's kept, and never shown again.")];
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
  parts.push(heading("Models for hi q"), el("p", { class: "muted small" }, "hi q on the org's devices asks models through the org server, with the org's key."));
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
  parts.push(el("p", { class: "muted small" }, "This machine's own keys, for its apps, are in Keys on the left."));
  return parts;
}

function orgTemplates(s) {
  const tForm = inlineForm([
    { name: "name", placeholder: "Its name, such as team", required: true, max: "32" },
    { name: "url", placeholder: "Its git URL, https://…", required: true },
    { name: "ref", placeholder: "The branch or tag (optional)" },
    { name: "token", type: "password", placeholder: "A token that reads it, if it's private (kept, never shown)" },
  ], (v) => orgDo({ what: "template", action: "add", name: v.name, url: v.url, ref: v.ref, token: v.token }, "Serving " + v.name + "'s templates."), "Add");
  const templates = el("ul", { class: "list" });
  for (const t of s.templates || []) {
    templates.append(el("li", {}, el("span", { class: "what" }, el("strong", {}, t.name), el("span", { class: "muted small block" }, shown(t.url) + (t.ref ? " @ " + t.ref : "")),
      el("span", { class: "muted small block" }, (t.layers || []).length + " templates, " + (t.skills || []).length + " skills" + (t.commit ? " · " + t.commit.slice(0, 7) : "") + (t.fetched ? " · " + ago(t.fetched) : "")),
      t.problem ? el("span", { class: "warn small block" }, t.problem) : null),
      el("span", { class: "actions" }, el("button", { onclick: () => orgDo({ what: "template", action: "sync", name: t.name }, "Fetched " + t.name + ".") }, "Sync"),
        el("button", { class: "danger", onclick: async () => { if (await ask({ title: "Stop serving " + t.name + "?", text: "Devices no longer see its templates and skills. Projects made from them keep what they have.", ok: "Stop serving" })) orgDo({ what: "template", action: "remove", name: t.name }, "Stopped serving " + t.name + "."); } }, "Remove"))));
  }
  if (!(s.templates || []).length) templates.append(el("li", { class: "plain muted small" }, "None: hi init offers only hi's own."));
  return [el("p", { class: "purpose" }, "Repositories of templates and skills that hi init offers on the org's devices."), heading("Templates", plus("Serve a repository's templates", tForm)), tForm, templates];
}

function orgData(s) {
  const dForm = inlineForm([
    { name: "orgs", placeholder: "Hugging Face organizations, space between them", required: true },
    { name: "token", type: "password", placeholder: "A Hugging Face token that reads them (kept, never shown)", required: true },
  ], (v) => orgDo({ what: "data", action: "add", orgs: v.orgs.split(/[\s,]+/).filter(Boolean), token: v.token }, "Serving their data."), "Add");
  const data = el("ul", { class: "list" });
  for (const d of s.data || []) {
    data.append(el("li", {}, el("span", { class: "what" }, el("strong", {}, d.org),
      el("span", { class: "muted small block" }, d.datasets + " datasets, " + d.models + " models, " + d.buckets + " buckets" + (d.account ? " · as " + d.account + (d.role ? " (" + d.role + ")" : "") : "")),
      d.problem ? el("span", { class: "warn small block" }, d.problem) : null),
      el("span", { class: "actions" }, el("button", { onclick: () => orgDo({ what: "data", action: "test", name: d.org }, "Checked " + d.org + ".") }, "Test"),
        el("button", { class: "danger", onclick: async () => { if (await ask({ title: "Stop serving " + d.org + "?", text: "hi data no longer reaches its datasets and models. Its token is forgotten.", ok: "Stop serving" })) orgDo({ what: "data", action: "remove", name: d.org }, "Stopped serving " + d.org + "."); } }, "Remove"))));
  }
  if (!(s.data || []).length) data.append(el("li", { class: "plain muted small" }, "None."));
  return [el("p", { class: "purpose" }, "Hugging Face organizations whose datasets and models hi data reaches on the org's devices."), heading("Data", plus("Serve a Hugging Face organization", dForm)), dForm, data];
}

function orgModels(s) {
  const served = el("ul", { class: "list" });
  const list = (s.ai && s.ai.served_list) || [];
  for (const m of list) {
    served.append(el("li", {}, el("span", { class: "what" }, el("strong", {}, m.name), el("span", { class: "muted small block" }, (m.model || "") + (m.user ? " · " + m.user : "") + (m.host ? " on " + m.host : "") + " · " + (m.state && m.state !== "approved" ? m.state : m.up ? "up" : "stopped"))),
      el("span", { class: "actions" }, el("button", { class: "danger", onclick: async () => { if (await ask({ title: "Stop passing requests to " + m.name + "?", text: "hi q stops sending the org's requests to it. The machine keeps serving it.", ok: "Stop" })) orgDo({ what: "served", name: m.name }, "Stopped passing requests to " + m.name + "."); } }, "Remove"))));
  }
  if (!list.length) served.append(el("li", { class: "plain muted small" }, "None: hi serve llm --org on a machine names one to the org."));
  return [el("p", { class: "purpose" }, "Models served on the team's machines, which hi q passes the org's requests to."), served];
}

function orgSlack(s) {
  const slack = el("div", { class: "machine-block" });
  if (s.slack.set) slack.append(el("p", { class: "small" }, "Approvals go to the channel ", el("code", {}, s.slack.channel || "?"), ", and to each approver's Home tab."));
  else slack.append(el("p", { class: "warn small" }, "Not set up, so approvals are only here and on the machine. On the machine: ", el("code", {}, "hi org slack setup"), " (its app: ", el("code", {}, "hi org slack manifest"), ")."));
  const approvers = Object.entries(s.approvers || {}).sort((a, b) => a[1].localeCompare(b[1]));
  if (approvers.length) slack.append(el("p", { class: "muted small" }, "Approvers: " + approvers.map(([id, name]) => name).join(", ") + ". The owner and the admins approve; someone who only approves: ", el("code", {}, "hi org approvers add"), "."));
  return [el("p", { class: "purpose" }, "The org's Slack app: where approvals, warnings, and reports go."), slack];
}

function orgWalls(s) {
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
  const walls = el("ul", { class: "list" });
  for (const w of s.walls || []) {
    walls.append(el("li", {}, el("span", { class: "what" }, el("strong", {}, w.name), el("span", { class: "muted small block" }, (w.full ? "names in full" : "initials") + (w.reasons ? ", with reasons" : "") + " · by " + (w.by || "?") + " " + ago(w.made) + (w.seen ? " · open " + ago(w.seen) : " · not opened yet"))),
      el("span", { class: "actions" }, el("button", { class: "danger", onclick: async () => { if (await ask({ title: "End " + w.name + "'s wall link?", text: "The screen stops showing anything at once.", ok: "End it" })) wallEnd(w.name); } }, "End"))));
  }
  if (!(s.walls || []).length) walls.append(el("li", { class: "plain muted small" }, "None: a screen opens a wall link in a browser, read-only."));
  return [el("p", { class: "purpose" }, "Running and Requests on a screen, read-only, from a link that works until it's ended."), heading("Wall screens", plus("A wall link for a screen", wForm)), wForm, walls];
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

// People: everyone with a level in the apps. The org's users are the org's.
function renderPeople() {
  $("people-apps").replaceChildren(peopleAppsBlock());
}
$("people-search").addEventListener("input", renderPeople);

// Keys: the machine's, set or not, never shown. The org's are the org's.
function renderKeys() {
  const parts = [machineKeysBlock()];
  if (state && state.org) parts.push(el("p", { class: "muted small" }, "The org's keys, for its compute providers and hi q, are in ", el("button", { class: "link-button", "data-nav": "", onclick: () => openOrg("keys") }, "the org's Keys"), "."));
  $("keys-view").replaceChildren(...parts);
}

// ---------------------------------------------------------------------------
// Budgets and approval, in the middle: each group's people and this
// month's spend, its limits and budgets, and what is approved without a
// person, with what the last 30 days' requests say a rule approves. The
// org server checks the policy before it's saved.

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
const groupsOf = (name) => name + (name.endsWith("s") ? "'" : "'s");
// policyAsked is a group's requests of the last 30 days.
function policyAsked(spend, name) { return ((spend && spend.asked) || []).filter((a) => a.group === name); }

function policyRuleWords(g) {
  const a = g.auto_approve;
  if (!a) return "Every request waits for a person.";
  return "A start at up to " + dollars(a.max_price_per_hour) + "/h for up to " + hoursText(a.max_hours) + " starts without a person" +
    (g.user_monthly_budget_usd || g.group_monthly_budget_usd ? ", while its user and group are under their monthly budgets" : "") + "; more waits for a person.";
}

function policyPreviewWords(name, g, spend) {
  const asked = policyAsked(spend, name);
  if (!asked.length) return "No requests from this group in the last 30 days.";
  const people = asked.filter(byPerson).length;
  const covered = asked.filter((a) => policyCovers(g.auto_approve, a)).length;
  let text = "Last 30 days: " + asked.length + (asked.length === 1 ? " request" : " requests") + ", " + people + " of them decided by a person. ";
  text += g.auto_approve ? "This rule starts " + covered + " of the " + asked.length + " by itself." : "With no auto-approval, a person decides all of them.";
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
  if (!p.exists) parts.push(el("p", { class: "muted small" }, "None yet: this is the example to start from."));
  parts.push(el("p", { class: "purpose" }, "Each group's monthly budgets, its limits, and what starts without a person. A request within the group's auto-approval starts at once while its user and group are under budget; the rest wait for an approver, in Slack and in Requests. Going over a budget never stops what runs: it warns, and new requests go to a person. It applies to the next request."));
  if (p.error) parts.push(el("p", { class: "error" }, "The policy on the machine doesn't read: " + p.error));
  // Where the approvals come from: the groups a person still decides for.
  const waiting = Object.keys(policyDraft.groups).map((name) => [name, policyAsked(spend, name).filter((a) => byPerson(a) && !policyCovers(policyDraft.groups[name].auto_approve, a)).length]).filter(([, n]) => n > 0);
  if (waiting.length) {
    parts.push(el("p", { class: "warn small" }, "In the last 30 days, a person decided " + waiting.map(([name, n]) => n + " of " + groupsOf(name)).join(", ") + " requests that the rules below still send to a person."));
  }
  const outside = Object.entries(spend.users || {}).filter(([, u]) => u.group && !policyDraft.groups[u.group]).map(([name, u]) => name + " (" + u.group + ")");
  if (outside.length) parts.push(el("p", { class: "muted small" }, "In a group the policy doesn't name, so a person approves all they ask: " + outside.join(", ") + "."));
  const error = el("p", { class: "error", id: "policy-error" });
  if (policyText) {
    const area = el("textarea", { id: "policy-text", class: "policy-text", rows: "24", spellcheck: "false", "aria-label": "The policy, as JSON" });
    area.value = JSON.stringify(policyDraft, null, 2);
    area.addEventListener("input", changed);
    parts.push(area);
  } else {
    for (const [name, g] of Object.entries(policyDraft.groups)) parts.push(policyGroup(name, g, spend, changed));
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
    el("button", { "data-nav": "", onclick: () => { if (policyText) { try { policyDraft = JSON.parse($("policy-text").value); } catch (e) { error.textContent = "That isn't JSON: " + e.message; return; } } policyText = !policyText; renderPolicy(); } }, policyText ? "As a form" : "As text"),
    el("button", { onclick: () => { policyDraft = null; policyEdited = false; renderPolicy(); } }, "Undo my changes")));
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
    if (members.length > 12) rows.push(el("p", { class: "muted small" }, "And " + (members.length - 12) + " more: Users lists them."));
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
  const auto = el("input", { type: "checkbox", "aria-label": name + ": approved without a person" });
  auto.checked = !!g.auto_approve;
  const autoFields = el("div", { class: "policy-fields", hidden: !g.auto_approve },
    num("Up to $ an hour", () => g.auto_approve && g.auto_approve.max_price_per_hour, (v) => { g.auto_approve = Object.assign({}, g.auto_approve, { max_price_per_hour: v || 0 }); }, "0.01"),
    num("For up to hours a start", () => g.auto_approve && g.auto_approve.max_hours, (v) => { g.auto_approve = Object.assign({}, g.auto_approve, { max_hours: v || 0 }); }, "0.5"));
  auto.addEventListener("change", () => {
    if (auto.checked) g.auto_approve = g.auto_approve || { max_price_per_hour: 1, max_hours: 2 };
    else delete g.auto_approve;
    changed();
    renderPolicy();
  });
  // Cover what the group asked in the last 30 days, rounded up.
  const asked = policyAsked(spend, name);
  const cover = asked.length ? el("button", { class: "small", title: "Set the auto-approval to the dearest rate and the longest start the group asked for in the last 30 days", onclick: () => {
    const rate = Math.ceil(Math.max(...asked.map((a) => a.rate)) * 100 - 1e-6) / 100;
    const hours = Math.ceil(Math.max(...asked.map((a) => a.hours)) * 2 - 1e-6) / 2;
    g.auto_approve = { max_price_per_hour: Math.max(rate, (g.auto_approve && g.auto_approve.max_price_per_hour) || 0), max_hours: Math.max(hours, (g.auto_approve && g.auto_approve.max_hours) || 0) };
    changed();
    renderPolicy();
  } }, "Cover what it asked lately") : null;
  const kept = [];
  if (g.template_sources) kept.push("its template sources");
  if (g.data) kept.push("its data");
  card.append(el("div", { class: "card-head pad" }, el("strong", { class: "card-name" }, name),
    el("span", { class: "muted small" }, members.length + (members.length === 1 ? " user" : " users")), el("span", { class: "grow" }),
    el("button", { class: "small danger", onclick: () => { delete policyDraft.groups[name]; changed(); renderPolicy(); } }, "Remove the group")));
  card.append(budgets);
  card.append(el("h3", { class: "pad policy-sub" }, "Approved without a person"));
  card.append(el("div", { class: "data-actions pad" }, el("label", { class: "check small" }, auto, " On, within:"), el("span", { class: "grow" }), cover));
  card.append(autoFields, words, preview);
  card.append(el("h3", { class: "pad policy-sub" }, "Budgets and limits"));
  card.append(el("div", { class: "policy-fields" },
    num("$ a user a month", () => g.user_monthly_budget_usd, setKey("user_monthly_budget_usd")),
    num("$ for the group a month", () => g.group_monthly_budget_usd, setKey("group_monthly_budget_usd")),
    num("Up to hours a start", () => g.max_hours, setKey("max_hours")),
    el("label", { class: "field wide" }, el("span", { class: "muted small" }, "Only this hardware"), hardware)));
  if (kept.length) card.append(el("p", { class: "muted small pad" }, "Also " + kept.join(" and ") + ", kept as they are; change them as text."));
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
  const ok = await ask({ title: "Approve the like of " + r.id + " by itself?", ok: "Approve and save",
    text: "From now on, what the " + group + " group asks for at up to " + dollars(rate) + "/h, for up to " + hoursText(hours) + " a start, starts without a person while its user and group are under budget. A rule that already allows more stays as it is. " + r.id + " is approved now. Org, Budgets and approval changes it later." });
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
  } catch (e) { if (!(e instanceof SignIn)) toast("The policy wasn't saved: " + e.message, true); return; }
  orgDo({ what: "decide", action: "approve", id: r.id, group: "" }, "Approved " + r.id + "; the like of it starts by itself from now on.");
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

// Home's card: the org's day. It opens the org, as an app's card opens
// the app.
function renderHomeOrg() {
  const box = $("home-org");
  const o = state.org;
  if (!o) { box.replaceChildren(); return; }
  const card = el("div", { class: "card-box org-card" });
  const head = el("button", { class: "card-head pad org-open", onclick: () => openOrg(""), title: "Open the org: its requests, users, admins, policy, and keys" },
    el("span", { class: "dot " + (o.up ? "idle" : "bad"), "aria-hidden": "true" }), el("strong", { class: "card-name" }, "The org"),
    el("span", { class: "muted small" }, o.up ? "runs" : "doesn't answer"), el("span", { class: "grow" }), o.owner ? el("span", { class: "muted small" }, "owned by " + o.owner) : null);
  card.append(head);
  if (o.no_owner) card.append(el("p", { class: "warn small pad" }, "No owner yet. On the machine: ", el("code", {}, "hi org owner <Slack member ID> --email <address>")));
  if (!o.up) card.append(el("p", { class: "error small pad" }, o.error || "The org server doesn't answer."));
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
    el("div", { class: "data-actions" }, el("button", { class: "primary", type: "submit", id: "new-start", disabled: true }, "Start")));
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
  parts.push(el("h2", {}, "What every app needs on this machine"),
    needsBox("", (ready) => { const start = $("new-start"); if (start) start.disabled = !ready; }));
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
          el("li", {}, el("a", { class: "button-like rainbow", href: d.manifest_url, target: "_blank", rel: "noopener noreferrer" }, "Create the Slack app ↗"), " opens Slack with the manifest filled in. Pick the workspace, then ", el("strong", {}, "Create"), "."),
          el("li", {}, "The app token is on the page Slack opens, under ", el("strong", {}, "Basic Information → App-Level Tokens"), ": generate one with the connections:write scope (xapp-…)."),
          el("li", {}, "Then ", el("strong", {}, "Install to Workspace"), ", and allow it. The bot token is under ", el("strong", {}, "OAuth & Permissions"), " (xoxb-…).")),
        el("details", { class: "small" }, el("summary", { class: "muted" }, "The manifest, to paste by hand (api.slack.com/apps → Create New App → From a manifest → JSON)"), el("pre", { class: "job-output manifest" }, d.manifest)),
        field("App token", app), field("Bot token", bot),
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
      body.append(el("p", {}, "The app's addresses are on your NetBird network, and the owners build in its dev box over SSH there. The dev box joins with a setup key, once: in NetBird's dashboard, Setup Keys. Without one now, nobody reaches its addresses or its dev page until it joins, with a key in the app's Network, or hi app dev " + d.name + " netbird."),
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
    const model = input({ value: member.model || "", placeholder: member.agent === "hermes" && member.provider === "chatgpt" ? "the plan's first" : "the agent's own", "aria-label": role + "'s model" });
    const provider = el("select", { "aria-label": role + ": who pays" });
    provider.append(el("option", { value: "openrouter", selected: member.provider !== "chatgpt" }, "OpenRouter, per call"), el("option", { value: "chatgpt", selected: member.provider === "chatgpt" }, "A ChatGPT plan, falling back to OpenRouter"));
    const effort = el("select", { "aria-label": role + "'s effort" });
    for (const e of m.efforts) effort.append(el("option", { value: e, selected: (member.effort || "medium") === e }, e));
    const fallback = input({ value: member.fallback || "", placeholder: "the usual one", "aria-label": role + "'s fallback on OpenRouter" });
    const models = el("datalist", { id: "models-" + role });
    for (const name of d.chatgpt_models || []) models.append(el("option", { value: name }));
    model.setAttribute("list", "models-" + role);
    const providerField = field("Who pays", provider);
    const sync = () => {
      providerField.hidden = agent.value !== "hermes";
      model.placeholder = agent.value === "hermes" && provider.value === "chatgpt" ? "the plan's first" : "the agent's own";
      drawSignins();
    };
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

// What an app needs on this machine (hi_admin.md, release 8): each tool,
// with Install, and Install all; what needs root takes the machine's
// sudo password, which the page passes to that install alone. ready says
// whether nothing is missing, each time the list is drawn.
function needsBox(name, ready) {
  const box = el("div", { class: "needs" }, el("p", { class: "muted small" }, "Checking what an app needs on this machine…"));
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
    const password = missing.some((n) => n.root) ? input({ type: "password", placeholder: "The sudo password of the account that runs hi", "aria-label": "The machine's sudo password", autocomplete: "new-password" }) : null;
    const error = el("p", { class: "error small" });
    const install = async (ids) => {
      error.textContent = "";
      const root = view.needs.some((n) => ids.includes(n.id) && n.root);
      if (root && password && !password.value) { error.textContent = "What needs root takes the machine's sudo password."; password.focus(); return; }
      try {
        const job = await api("/api/needs", { ids, password: root && password ? password.value : "" });
        busy = job.id;
        toast("Installing. Jobs has its output.", true);
        draw();
        const wait = async () => {
          let answer;
          try { answer = await api("/api/job?id=" + encodeURIComponent(job.id) + "&from=0"); } catch (e) { setTimeout(wait, 2000); return; }
          if (answer.job.state === "running") { setTimeout(wait, 2000); return; }
          busy = "";
          if (answer.job.state === "failed") toast("Installing failed: Jobs has why.", true);
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
    if (busy) parts.push(el("p", { class: "muted small" }, "Installing: Jobs follows it, and this list checks again when it's done."));
    else if (missing.length) {
      if (password) parts.push(field("For what needs root, the sudo password of the account that runs hi: used for this install alone, never kept", password));
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
  fact("Network", (d.has_netbird ? "joins NetBird now" : "joins NetBird later: its addresses don't work until it does, with a setup key in its Network") + "; addresses " + (c.addresses || "on this machine's domain"));
  fact("Heartbeat", d.heartbeat || "none");
  body.append(el("p", {}, "Create makes it as hi app new would with these answers, then starts it. Jobs follows it, and the app opens here when it's up."), facts);
  const create = el("button", { class: "primary", disabled: true, onclick: async () => {
    create.disabled = true;
    try {
      const job = await api("/api/new", { action: "create", name: d.name });
      toast("Creating " + d.name + ". Jobs has its output; it opens here when it's up.", true);
      creating = { job: job.id, name: d.name };
      followJob(job);
      loadDraft(d.name, false);
    } catch (e) { create.disabled = false; if (!(e instanceof SignIn)) $("step-error").textContent = e.message; }
  } }, "Create " + d.name);
  body.append(el("h2", {}, "What it needs on this machine"),
    needsBox(d.name, (ready) => { create.disabled = !ready || d.job_state === "running"; }));
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
function sectionSource(section) { return { network: "/api/app/network", data: "/api/app/data", terminal: "/api/app/terminal" }[section] || "/api/app/settings"; }

async function refreshSettings() {
  if (!openApp || appSection === "overview") return;
  const name = openApp;
  try {
    const source = sectionSource(appSection);
    const fresh = await api(source + "?name=" + encodeURIComponent(name));
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
    if (answer.job) { toast((done || "Started") + ". Jobs has its output.", true); followJob(answer.job); }
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
  const head = el("div", { class: "admin-head" }, el("h1", { class: "admin-title" }, s.name), el("span", { class: "muted" }, { people: "People", agents: "Agents", slack: "Slack", settings: "Settings", network: "Network", data: "Data", terminal: "Terminal" }[appSection]));
  const parts = [head];
  if (appSection === "people") parts.push(...sectionPeople(s));
  if (appSection === "agents") parts.push(...sectionAgents(s));
  if (appSection === "slack") parts.push(...sectionSlack(s));
  if (appSection === "settings") parts.push(...sectionSettings(s));
  if (appSection === "network") parts.push(...sectionNetwork(s));
  if (appSection === "data") parts.push(...sectionData(s));
  if (appSection === "terminal") parts.push(...sectionTerminal(s));
  box.replaceChildren(...parts);
}

// ---------------------------------------------------------------------------
// an app's Terminal: a shell in one of its boxes, for the machine's owner
// (hi_admin.md, release 7), as the dev page's terminal is.

let adminTerm = null; // the open terminal: { app, box, term, fit, socket, node }

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
  const parts = [el("p", {}, "A shell in one of " + s.name + "'s boxes that run, as the box's own user. Not on the machine: the boxes hold none of the app's keys. Each terminal is said in Activity and the app's ops channel, and closes after 30 minutes without a key.")];
  if (!s.you) parts.push(el("p", { class: "note" }, "Only the machine's owner" + (s.owner ? ", " + s.owner + "," : "") + " opens a terminal here. On the machine, hi admin owner <admin> hands it on."));
  if (!s.boxes.length) {
    if (adminTerm && adminTerm.app === s.name) closeTerm();
    parts.push(el("p", { class: "muted" }, "None of " + s.name + "'s boxes runs now."));
    return parts;
  }
  if (!s.you) {
    parts.push(el("ul", { class: "list" }, ...s.boxes.map((b) => el("li", {}, b.label))));
    return parts;
  }
  const mine = adminTerm && adminTerm.app === s.name;
  const select = el("select", { "aria-label": "The box" });
  for (const b of s.boxes) select.append(el("option", { value: b.id, selected: mine && adminTerm.box === b.id }, b.label));
  parts.push(el("div", { class: "data-actions" }, select, el("button", { onclick: () => openTerm(s.name, select.value) }, mine ? "Open again" : "Open")));
  if (mine) parts.push(adminTerm.node);
  return parts;
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
    // The dev page's colours: VS Code's 2026 Dark.
    theme: {
      background: "#121314", foreground: "#bfbfbf", cursor: "#bfbfbf", selectionBackground: "#3994bc33",
      black: "#000000", red: "#cd3131", green: "#0dbc79", yellow: "#e5e510",
      blue: "#2472c8", magenta: "#bc3fbc", cyan: "#11a8cd", white: "#e5e5e5",
      brightBlack: "#666666", brightRed: "#f14c4c", brightGreen: "#23d18b", brightYellow: "#f5f543",
      brightBlue: "#3b8eea", brightMagenta: "#d670d6", brightCyan: "#29b8db", brightWhite: "#e5e5e5",
    },
  });
  const fit = new FitAddon.FitAddon();
  term.loadAddon(fit);
  const t = adminTerm = { app, box, term, fit, node, socket: null };
  renderSection();
  term.open(screen);
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
    why.textContent = "The terminal closed" + (event.reason ? ": " + event.reason : "") + ". Open again opens a new one.";
  };
  term.onData((data) => { if (socket.readyState === 1) socket.send(encoder.encode(data)); });
  const resize = () => {
    try { fit.fit(); } catch (e) { return; }
    if (socket.readyState === 1) socket.send(JSON.stringify({ cols: term.cols, rows: term.rows }));
  };
  t.observer = new ResizeObserver(resize);
  t.observer.observe(screen);
}

const levelOptions = [["owner", "Owner"], ["pro", "Pro Dev"], ["vibe", "Vibe Dev"], ["contributor", "Contributor"], ["none", "None"]];
function levelSelect(value, label, onchange) {
  const select = el("select", { "aria-label": label });
  for (const [v, text] of levelOptions) select.append(el("option", { value: v, selected: v === value }, text));
  select.addEventListener("change", () => onchange(select.value, select));
  return select;
}
async function setLevel(app, person, name, level, select, before) {
  const words = { owner: "an owner", pro: "a Pro Dev", vibe: "a Vibe Dev", contributor: "a contributor", none: "nobody in it" };
  if (!(await ask({ title: "Make " + (name || person) + " " + words[level] + " of " + app + "?", text: level === "none" ? "Their level goes, and their private links and SSH key with it. The app's channel hears it." : "The app's channel hears it." + (level === "owner" ? " An owner is an owner only: a level they had goes." : ""), ok: "Change it" }))) { if (select) select.value = before; return false; }
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
        const answer = await ask({ title: (p.name || p.id) + "'s SSH key for the dev box", text: "Their public key, such as the contents of ~/.ssh/id_ed25519.pub. A key they had before stops working.", ok: "Save", field: "ssh-ed25519 AAAA…" });
        if (answer) change("sshkey", { person: p.id, key: answer.field }, (p.name || p.id) + "'s SSH key is in the dev box.");
      } }, p.key ? "New key" : "Set key"),
      p.key ? el("button", { class: "small", onclick: async () => { if (await ask({ title: "Take " + (p.name || p.id) + "'s SSH key out?", text: "They can no longer SSH into the dev box.", ok: "Take it out" })) change("sshkey", { person: p.id, remove: true }); } }, "Remove key") : null);
    list.append(el("li", {}, el("span", { class: "what" }, el("strong", {}, p.name || p.id), " ", el("span", { class: "muted small" }, p.id), el("span", { class: "muted small block" }, p.key ? "SSH key " + p.key : p.level === "owner" || p.level === "pro" ? "no SSH key" : "")),
      levelSelect(p.level, (p.name || p.id) + "'s level", async (level, select) => { if (await setLevel(s.name, p.id, p.name, level, select, p.level)) refreshSettings(); }), keyButtons));
  }
  if (!s.people.length) list.append(el("li", { class: "plain muted small" }, "Nobody yet."));
  const id = el("input", { maxlength: "20", placeholder: "Their Slack member ID, U…", "aria-label": "Their Slack member ID" });
  let level = "pro";
  const add = el("form", { class: "say-form", autocomplete: "off" }, id, levelSelect("pro", "Their level", (v) => { level = v; }), el("button", { type: "submit" }, "Add"));
  add.addEventListener("submit", async (event) => { event.preventDefault(); if (await setLevel(s.name, id.value.trim(), "", level, null, "none")) { id.value = ""; refreshSettings(); } });
  return [el("p", { class: "purpose" }, "Owners build in the dev box and release; Pro Devs too, except rollbacks, production's data and settings, keys, and who is who; Vibe Devs in the dev page's Chat only; contributors report bugs. The same as hi app owners and hi app access, and owners change all but owners on the dev page."),
    list, el("h2", {}, "Add someone"), add, el("p", { class: "muted small" }, "In Slack: their profile, ⋮, Copy member ID. People in several apps: People, on the left.")];
}

function sectionAgents(s) {
  const parts = [el("p", { class: "purpose" }, "Each role's model, who pays for it, and how hard it thinks; the app restarts when one changes. The same as hi app member.")];
  for (const m of s.members) {
    const provider = el("select", { "aria-label": m.role + ": who pays", disabled: m.agent !== "hermes" });
    for (const [v, text] of [["openrouter", "OpenRouter, per call"], ["chatgpt", "A ChatGPT plan, falling back to OpenRouter"], ["claude", "Claude's plan"]]) {
      if (v === "claude" && m.agent !== "claude") continue;
      if (v !== "claude" && m.agent === "claude") continue;
      provider.append(el("option", { value: v, selected: m.provider === v }, text));
    }
    const model = el("input", { value: m.model || "", placeholder: "the agent's own", "aria-label": m.role + "'s model" });
    const fallback = el("input", { value: m.fallback || "", placeholder: "the usual one", "aria-label": m.role + "'s fallback" });
    const effort = el("select", { "aria-label": m.role + "'s effort" });
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
        change("member", fields, "The " + m.role + " is changed; the app restarts.");
      } }, "Save"))));
  }
  // Keys and sign-ins.
  const or = el("div", { class: "machine-block" }, el("p", {}, el("strong", {}, "OpenRouter: "), s.own_openrouter ? "the app's own key" : "this machine's key" + (s.machine_openrouter ? "" : ", which it hasn't got")));
  or.append(el("div", { class: "data-actions" },
    el("button", { class: "small", onclick: async () => { const answer = await ask({ title: "The app's own OpenRouter key", text: "Its use is billed and limited apart. It's checked, kept on the machine, and never shown again.", ok: "Check and keep", field: "sk-or-…" }); if (answer) change("openrouter", { key: answer.field }, "The app has its own OpenRouter key."); } }, s.own_openrouter ? "Replace the key" : "Give it its own key"),
    s.own_openrouter ? el("button", { class: "small", onclick: async () => { if (await ask({ title: "Use this machine's OpenRouter key?", text: "The app's own key is forgotten.", ok: "Use the machine's" })) change("openrouter", { remove: true }); } }, "Use this machine's") : null));
  const models = el("input", { type: "checkbox" });
  models.checked = s.models;
  models.addEventListener("change", () => change("models", { on: models.checked }));
  parts.push(el("h2", {}, "The dev page's chat"), el("label", { class: "check" }, models, " Developers pick its model and effort, beside the message box; a pick is also the dev box's default for new sessions. Off, the picker isn't there, and each session keeps what it starts with (hi app models)."));
  parts.push(el("h2", {}, "Keys and sign-ins"), or);
  if (s.uses_chatgpt) {
    parts.push(el("div", { class: "machine-block" }, el("p", {}, el("strong", {}, "ChatGPT: "), s.chatgpt ? "the app's own sign-in, " + s.chatgpt : "this machine's sign-in, Codex's" + (s.machine_chatgpt ? "" : ", which isn't signed in")),
      s.chatgpt_failed ? el("p", { class: "error small" }, "It stopped working: " + s.chatgpt_failed + ". Sign in again.") : "",
      el("div", { class: "data-actions" }, el("button", { class: "small", onclick: () => change("chatgpt", {}, "Signing in: the code to type is in Jobs") }, s.chatgpt ? "Sign in again" : "Sign the app in"),
        s.chatgpt ? el("button", { class: "small", onclick: () => change("chatgpt", { machine: true }) }, "Use this machine's") : "")));
  }
  if (s.uses_claude) {
    parts.push(el("div", { class: "machine-block" }, el("p", {}, el("strong", {}, "Claude: "), s.own_claude ? "the app's own token" : "this machine's sign-in" + (s.machine_claude ? "" : ", which isn't there")),
      el("div", { class: "data-actions" }, el("button", { class: "small", onclick: async () => { const answer = await ask({ title: "The app's own Claude token", text: "From claude setup-token, signed in to the account that pays; it lasts a year. Kept on the machine, never shown again.", ok: "Keep it", field: "sk-ant-oat…" }); if (answer) change("claude", { key: answer.field }, "The app has its own Claude token."); } }, s.own_claude ? "Replace the token" : "Give it its own token"),
        s.own_claude ? el("button", { class: "small", onclick: () => change("claude", { machine: true }) }, "Use this machine's") : "")));
  }
  // Stage's limit, and the boxes' memory.
  const usd = el("input", { value: s.stage_limit > 0 ? String(s.stage_limit) : "", placeholder: "off", "aria-label": "Dollars a day", class: "small-input" });
  parts.push(el("h2", {}, "Stage's model calls"), el("p", { class: "small" }, (s.stage_limit > 0 ? "Up to $" + s.stage_limit.toFixed(2) + " a day" : "No daily limit") + "; today $" + s.stage_spent.toFixed(2) + " in " + s.stage_calls + " call" + (s.stage_calls === 1 ? "" : "s") + ". Production has no limit from hi."),
    el("div", { class: "data-actions" }, el("span", { class: "muted small" }, "$ a day"), usd, el("button", { class: "small", onclick: () => change("limit", { usd: usd.value.trim() || "off" }) }, "Save"), el("button", { class: "small", onclick: () => change("limit", { usd: "off" }) }, "No limit")));
  const dev = el("input", { value: s.memory_dev, class: "small-input", "aria-label": "The dev box's memory" });
  const app = el("input", { value: s.memory_app, class: "small-input", "aria-label": "The app's boxes' memory" });
  parts.push(el("h2", {}, "Memory"), el("div", { class: "data-actions" }, el("span", { class: "muted small" }, "The dev box"), dev, el("button", { class: "small", onclick: () => change("memory", { box: "dev", size: dev.value.trim() }) }, "Save"),
    el("span", { class: "muted small" }, "Stage's and production's boxes"), app, el("button", { class: "small", onclick: () => change("memory", { box: "app", size: app.value.trim() }) }, "Save"),
    s.memory_own ? el("button", { class: "small", onclick: () => change("memory", { box: "default" }) }, "hi's default") : ""), el("p", { class: "muted small" }, "Such as 24g or 512m. Running boxes get it at once."));
  return parts;
}

function sectionSlack(s) {
  const ops = el("input", { value: s.ops_channel || "", placeholder: "C… (empty: none)", "aria-label": "The ops channel's ID", class: "small-input wide-input" });
  const bot = el("input", { type: "password", placeholder: "xoxb-… (empty keeps the one it has)", "aria-label": "Bot token" });
  const appToken = el("input", { type: "password", placeholder: "xapp-… (empty keeps the one it has)", "aria-label": "App token" });
  const copy = el("button", { class: "small", onclick: () => copyText(s.manifest) }, "Copy the manifest");
  return [
    el("dl", { class: "facts" }, el("dt", {}, "Bot"), el("dd", {}, "@" + s.handle + " · " + s.slash), el("dt", {}, "Channel"), el("dd", {}, s.channel), el("dt", {}, "Tokens"), el("dd", {}, (s.has_bot ? "bot token set" : "no bot token") + ", " + (s.has_app ? "app token set" : "no app token"))),
    el("h2", {}, "Ops channel"), el("p", { class: "muted small" }, "A second channel for incidents, warnings, security, keys, and access, for the people with the know-how. Invite the app there first: /invite @" + s.handle + "."),
    el("div", { class: "data-actions" }, ops, el("button", { class: "small", onclick: () => change("ops", { channel: ops.value.trim() }) }, "Save")),
    el("h2", {}, "Tokens"), el("p", { class: "muted small" }, "When Slack gave new ones: the app token under Basic Information, the bot token under OAuth & Permissions. Each is checked; the app restarts."),
    field("App token", appToken), field("Bot token", bot), el("div", { class: "data-actions" }, el("button", { class: "small", onclick: () => change("slack", { bot: bot.value.trim(), app_token: appToken.value.trim() }, "The tokens are kept; the app restarts.") }, "Check and keep")),
    el("h2", {}, "The Slack app's manifest"), el("p", { class: "muted small" }, "To update the Slack app after a hi update, or a rename: at api.slack.com/apps, open the app, App Manifest, paste this over it, Save, and reinstall if Slack asks."),
    el("div", { class: "data-actions" }, copy, el("a", { class: "button-like", href: "https://api.slack.com/apps", target: "_blank", rel: "noopener noreferrer" }, "Your Slack apps ↗")),
    el("details", { class: "small" }, el("summary", { class: "muted" }, "The manifest"), el("pre", { class: "job-output manifest" }, s.manifest)),
    el("h2", {}, "Its name"), el("div", { class: "data-actions" }, el("button", { onclick: () => renameApp(s.name) }, "Rename…"))];
}

function sectionSettings(s) {
  const scopes = { all: "Stage and production", production: "production's own", stage: "Stage's own", dev: "the dev box's own" };
  const list = el("ul", { class: "list" });
  for (const e of s.env) {
    list.append(el("li", {}, el("span", { class: "what" }, el("code", {}, e.name), " ", el("span", { class: "muted small" }, scopes[e.scope] + (e.file ? ", a file" : ""))),
      el("span", { class: "actions" }, e.file ? "" : el("button", { class: "small", onclick: () => setEnv(e.name, e.scope) }, "New value"),
        el("button", { class: "small danger", onclick: async () => { if (await ask({ title: "Remove " + e.name + "?", text: "The app's boxes no longer get it, from their next start.", ok: "Remove" })) change("env", { name: e.name, scope: e.scope, remove: true }); } }, "Remove"))));
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
  const beat = el("input", { placeholder: s.heartbeat ? "Set; a new URL replaces it" : "https://hc-ping.com/…", "aria-label": "The heartbeat URL" });
  const every = el("input", { value: s.every || "", placeholder: "1m", class: "small-input", "aria-label": "Every" });
  const parts = [
    el("h2", {}, "Settings, by name"), el("p", { class: "muted small" }, "Environment variables in the app's boxes. Their values stay on the machine: the page never shows one. Files (certificates) are set on the machine: hi app env " + s.name + " --file KEY=<path>."),
    list, el("div", { class: "data-actions" }, el("button", { class: "small", onclick: () => setEnv("", "all") }, "+ A setting")),
    el("h2", {}, "The helper"), el("p", { class: "muted small" }, "Help and feedback on the app's pages, from the next page load."), el("div", { class: "radios" }, helper("preview", "On Stage"), helper("production", "On production")),
    el("h2", {}, "The watcher"), el("label", { class: "check" }, freeze, " It freezes the app on its own for an exploited advisory in the app's packages, or a burst to a watched host; off, each waits for an owner."),
    el("h2", {}, "Heartbeat"), el("p", { class: "muted small" }, s.heartbeat ? "The app sends one" + (s.every ? " every " + s.every : " every minute") + "." : "None, so nobody would hear that the app stopped."),
    el("div", { class: "data-actions" }, beat, every, el("button", { class: "small", onclick: () => change("heartbeat", { url: beat.value.trim(), every: every.value.trim() }) }, "Save"), s.heartbeat ? el("button", { class: "small", onclick: () => change("heartbeat", { remove: true }) }, "Off") : ""),
  ];
  parts.push(el("h2", {}, "Code"));
  if (s.remote) parts.push(el("p", { class: "small" }, "It pushes every merge to ", el("code", {}, shown(s.remote)), s.forge !== "hi" ? "; its tasks are " + (s.forge === "github" ? "GitHub" : "GitLab") + " issues." : "."));
  else {
    const repo = el("input", { placeholder: "owner/name, or for GitLab group/name or host/group/name", "aria-label": "The repository" });
    parts.push(el("p", { class: "muted small" }, "It stays on this machine. Put it on GitHub or GitLab: a new private repository, or an empty one, with this machine's sign-in."),
      el("div", { class: "data-actions" }, repo, el("button", { class: "small", onclick: () => change("github", { repo: repo.value.trim() }, "Putting it on GitHub") }, "On GitHub"), el("button", { class: "small", onclick: () => change("gitlab", { repo: repo.value.trim() }, "Putting it on GitLab") }, "On GitLab")));
  }
  if (s.adopted) parts.push(el("p", { class: "small" }, "Adopted from ", el("code", {}, s.adopted.from), s.adopted.local ? ", a folder here." : ", following " + (s.adopted.follow || "its default branch") + "."),
    s.adopted.local ? "" : el("div", { class: "data-actions" }, el("button", { class: "small", onclick: () => change("upstream", {}, "Fetching its upstream") }, "Fetch what's new there")));
  parts.push(el("h2", {}, "Remove the app"), el("div", { class: "data-actions" }, el("button", { class: "danger", onclick: () => removeApp(s.name) }, "Remove…")));
  return parts;
}

async function setEnv(name, scope) {
  const answer = await ask({ title: name ? "A new value for " + name : "A setting", text: (name ? "" : "Its name, then its value. ") + "The value goes to the machine once and is never shown again. The app's boxes get it from their next start.", ok: "Save", field: name ? "Its value" : "NAME=value", fieldCheck: (v) => (name ? (v ? "" : "Give its value.") : /^[A-Za-z_][A-Za-z0-9_]*=/.test(v) ? "" : "NAME=value, such as STRIPE_KEY=sk_live_…"), check: name ? "" : "Production's own, which Stage mustn't use" });
  if (!answer) return;
  let key = name, value = answer.field;
  if (!name) { key = answer.field.slice(0, answer.field.indexOf("=")); value = answer.field.slice(answer.field.indexOf("=") + 1); scope = answer.checked ? "production" : "all"; }
  change("env", { name: key, value, scope }, key + " is set.");
}

// ---------------------------------------------------------------------------
// People across apps, and the machine's keys

let peopleApps = null;
async function refreshPeopleApps() {
  try { peopleApps = await api("/api/people"); } catch (e) { return; }
  if (viewShown("people")) renderPeople();
}
function peopleAppsBlock() {
  const p = peopleApps;
  const box = el("div", { class: "pad" });
  if (!p) return box;
  box.append(el("h2", {}, "In the apps"));
  if (!p.apps.length) { box.append(el("p", { class: "muted small" }, "No apps on this machine.")); return box; }
  const query = $("people-search").value.trim().toLowerCase();
  const table = el("table", { class: "people-matrix" });
  table.append(el("tr", {}, el("th", {}, ""), ...p.apps.map((a) => el("th", { title: a }, a))));
  const named = {};
  for (const x of p.people) named[x.name] = (named[x.name] || 0) + 1;
  for (const person of p.people.filter((x) => !query || (x.name + " " + x.id).toLowerCase().includes(query))) {
    table.append(el("tr", {}, el("th", { scope: "row", title: person.id }, (person.name || person.id) + (person.name && named[person.name] > 1 ? " (" + person.id + ")" : "")),
      ...p.apps.map((app) => el("td", {}, levelSelect(person.levels[app] || "none", (person.name || person.id) + " in " + app, async (level, select) => { if (await setLevel(app, person.id, person.name, level, select, person.levels[app] || "none")) refreshPeopleApps(); })))));
  }
  box.append(el("div", { class: "matrix-scroll" }, table));
  // Someone in several apps at once.
  const id = el("input", { maxlength: "20", placeholder: "Slack member ID, U…", "aria-label": "Their Slack member ID" });
  let level = "pro";
  const checks = p.apps.map((app) => { const c = el("input", { type: "checkbox", value: app }); return [app, c]; });
  const form = el("form", { class: "stack", autocomplete: "off" }, el("strong", {}, "Someone in several apps"), id, levelSelect("pro", "Their level", (v) => { level = v; }),
    el("div", { class: "radios" }, ...checks.map(([app, c]) => el("label", { class: "check small" }, c, " " + app))), el("button", { type: "submit" }, "Give them the level"));
  form.addEventListener("submit", async (event) => {
    event.preventDefault();
    const levels = {};
    for (const [app, c] of checks) if (c.checked) levels[app] = level;
    if (!id.value.trim() || !Object.keys(levels).length) { toast("Their member ID, and the apps."); return; }
    if (!(await ask({ title: "Give " + id.value.trim() + " that level in " + Object.keys(levels).length + " app" + (Object.keys(levels).length === 1 ? "" : "s") + "?", text: "Each is the app's own change, said in its channel.", ok: "Give it" }))) return;
    try {
      const answer = await api("/api/people", { person: id.value.trim(), levels });
      toast((answer.done.length ? "Done in " + answer.done.join(", ") + ". " : "") + (answer.failed.length ? "Not in " + answer.failed.join("; ") : ""), true);
      refreshPeopleApps();
    } catch (e) { if (!(e instanceof SignIn)) toast(e.message, true); }
  });
  box.append(form);
  return box;
}

let machineKeys = null;
async function refreshMachineKeys() {
  try { machineKeys = await api("/api/keys"); } catch (e) { return; }
  if (viewShown("keys")) renderKeys();
}
function machineKeysBlock() {
  const k = machineKeys;
  const box = el("div", {});
  if (!k) return box;
  const line = (label, ok, how) => el("p", { class: "small incident-head" }, el("span", { class: "dot " + (ok ? "idle" : ""), "aria-hidden": "true" }), el("strong", {}, label), ": " + (ok ? "set" : "not set"), ok ? "" : el("span", { class: "muted" }, " · " + how));
  box.append(el("h2", {}, "This machine's"), el("div", { class: "machine-block" },
    line("OpenRouter, Hermes'", k.openrouter, "hermes setup, on the machine"),
    line("Claude Code's sign-in", k.claude, "claude, then /login, on the machine"),
    line("Codex's ChatGPT sign-in", k.chatgpt, k.codex_key ? "Codex is on an API key" : "codex login, on the machine")),
    el("p", { class: "muted small" }, "Apps without their own use these. An app's own are in its Agents section."));
  if (k.apps.length) {
    const table = el("table", { class: "people-matrix" });
    table.append(el("tr", {}, el("th", {}, ""), el("th", {}, "OpenRouter"), el("th", {}, "Claude"), el("th", {}, "ChatGPT")));
    for (const a of k.apps) {
      table.append(el("tr", {}, el("th", { scope: "row" }, el("button", { class: "link-button", onclick: async () => { await openView(a.app); showSection("agents"); } }, a.app)),
        ...[a.openrouter, a.claude, a.chatgpt].map((v) => el("td", { class: v === "own" ? "" : "muted" }, v === "own" ? "its own" : v === "machine" ? "the machine's" : "—"))));
    }
    box.append(el("h2", {}, "The apps'"), el("div", { class: "matrix-scroll" }, table));
  }
  return box;
}

// ---------------------------------------------------------------------------
// An app's Network and Data, the machine's Network, and Advisories.

function sectionNetwork(s) {
  const parts = [el("p", { class: "purpose" }, "Where the app is reached, and who may: its NetBird peer, how its addresses are named, the addresses let in, and the sign-in on top.")];
  // NetBird.
  const nb = el("input", { type: "password", placeholder: "A NetBird setup key", "aria-label": "NetBird setup key", class: "wide-input" });
  parts.push(el("h2", {}, "NetBird"), el("p", { class: "small incident-head" }, el("span", { class: "dot " + (s.joined ? "idle" : "blocked"), "aria-hidden": "true" }), s.joined ? "Its peer " + s.peer + " is on NetBird." : "Its peer " + s.peer + " hasn't joined NetBird."),
    el("pre", { class: "job-output short" }, s.dev),
    el("div", { class: "data-actions" }, nb, el("button", { class: "small", onclick: () => change("netbird", { key: nb.value.trim() }, "The dev box joins NetBird in a minute.") }, s.joined ? "Join again" : "Join")),
    el("p", { class: "muted small" }, "In NetBird's dashboard: Add Peer → Server, a one-off key with the auto-group hi-" + s.name + ". It's used once and deleted."));
  // Addresses.
  let mode = s.mode;
  const host = el("input", { value: s.host || "", placeholder: "demo.corp.example", class: "wide-input", "aria-label": "Host name" });
  const production = el("input", { value: s.names ? s.names.production || "" : "", placeholder: "demo.example.com", class: "wide-input", "aria-label": "Production's name" });
  const https = el("input", { type: "checkbox" }); https.checked = s.names ? !!s.names.https : true;
  const extra = el("div", { class: "stack" });
  const draw = () => {
    extra.replaceChildren();
    if (mode === "host") extra.append(field("The name your IT team keeps, an A record to its NetBird address", host));
    if (mode === "names") extra.append(field("Production's name; Stage and the dev page go under it", production), el("label", { class: "check small" }, https, " Their proxy ends TLS: the links say https"));
  };
  const options = [];
  if (s.domain) options.push(["domain", "This machine's domain, on https"]);
  options.push(["names", "A name for each, which your IT team keeps"], ["host", "One name your IT team keeps, with ports"], ["netbird", "The NetBird name, " + s.peer + ", on plain http"]);
  parts.push(el("h2", {}, "Addresses"), el("p", { class: "small" }, "Now: " + s.addresses + "."),
    el("ul", { class: "list" }, ...s.urls.map((u) => el("li", {}, el("span", { class: "what" }, el("strong", {}, u.what), " ", el("span", { class: "muted small" }, shown(u.url)))))),
    radios("n-addr", options, mode, (v) => { mode = v; draw(); }), extra,
    el("div", { class: "data-actions" }, el("button", { class: "small", onclick: async () => {
      if (!(await ask({ title: "Move " + s.name + "'s addresses?", text: "The app restarts to move them, and the owners get the dev page's new link in Slack. Links people have to the old addresses stop working.", ok: "Move them" }))) return;
      change("addresses", { mode, host: host.value.trim(), production: production.value.trim(), https: https.checked }, "Moving the addresses");
    } }, "Move them")));
  draw();
  if (s.domain) {
    const on = el("input", { type: "checkbox" }); on.checked = s.domain_on;
    on.addEventListener("change", () => change("domain", { on: on.checked }));
    parts.push(el("label", { class: "check small" }, on, " Production at " + s.name + "." + s.domain + " and Stage at " + s.name + "-preview." + s.domain + ", names that stay"));
  }
  // Who gets in.
  const ips = el("input", { value: s.allow_ips.join(","), placeholder: "any address with its link", class: "wide-input", "aria-label": "Addresses let in" });
  parts.push(el("h2", {}, "Addresses let in"), el("p", { class: "muted small" }, "The only addresses Stage and production let in, as IPs or ranges (NetBird's Access Control, with the domain's token). Empty: any address that has its link."),
    el("div", { class: "data-actions" }, ips, el("button", { class: "small", onclick: () => change("allowip", { ips: ips.value.trim() }) }, "Save")));
  // Sign-in.
  const provider = el("select", { "aria-label": "Sign-in provider" });
  provider.append(el("option", { value: "" }, "None: the link is enough"), ...s.providers.map((p) => el("option", { value: p }, p)));
  const domains = el("input", { placeholder: "example.com (optional)", class: "wide-input", "aria-label": "Email domains" });
  const groups = el("input", { placeholder: "a group (optional)", class: "wide-input", "aria-label": "Groups" });
  const anyone = el("input", { type: "checkbox" });
  parts.push(el("h2", {}, "Sign-in"), el("pre", { class: "job-output short" }, s.sso),
    s.providers.length ? el("div", { class: "stack" }, field("Provider", provider), field("Only these email domains", domains), field("Only these groups", groups), el("label", { class: "check small" }, anyone, " Anyone who signs in with it"),
      el("div", { class: "data-actions" }, el("button", { class: "small", onclick: () => change("sso", { sso: provider.value, domains: domains.value.trim(), groups: groups.value.trim(), anyone: anyone.checked }) }, "Save"))) : el("p", { class: "muted small" }, "No sign-in provider on this machine yet: Network, on the left."));
  parts.push(el("h2", {}, "What to ask your IT team"), el("pre", { class: "job-output copyable" }, s.ask),
    el("div", { class: "data-actions" }, el("button", { class: "small", onclick: () => copyText(s.ask) }, "Copy")));
  return parts;
}
// copyText copies text. On plain http, where browsers offer no clipboard
// API, it copies the old way, from a text box it selects for a moment, in
// the open dialog when there is one (a modal dialog makes the rest inert).
async function copyText(text) {
  if (navigator.clipboard && window.isSecureContext) {
    try { await navigator.clipboard.writeText(text); toast("Copied."); return; } catch (e) { /* the old way */ }
  }
  const box = el("textarea", { readonly: "", "aria-hidden": "true", style: "position:fixed;top:0;left:0;width:1px;height:1px;opacity:0" });
  box.value = text;
  (document.querySelector("dialog[open]") || document.body).append(box);
  box.select();
  let copied = false;
  try { copied = document.execCommand("copy"); } catch (e) { /* not copied */ }
  box.remove();
  if (copied) toast("Copied."); else toast("Select it and copy it.", true);
}

function sectionData(s) {
  const parts = [el("p", { class: "purpose" }, "The app's snapshots and releases, Stage's data, and its backups. What can't be undone needs the app's name typed, and says what it does first.")];
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
  if (!s.releases.length) releases.append(el("li", { class: "plain muted small" }, "Nothing released yet."));
  parts.push(el("h2", {}, "Releases"), releases);
  // Stage's data.
  const empty = el("input", { type: "checkbox" }); empty.checked = s.stage_empty;
  empty.addEventListener("change", () => change("stagedata", { on: empty.checked }));
  parts.push(el("h2", {}, "Stage's data"), el("pre", { class: "job-output short" }, s.data),
    el("label", { class: "check small" }, empty, " Stage starts empty at every release, for an app whose people's data Stage shouldn't show"),
    el("div", { class: "data-actions" }, el("button", { class: "small", onclick: () => change("fork", {}, "Stage has a new fork of production's data.") }, "A new fork for Stage"),
      el("button", { class: "small danger", onclick: async () => {
        const answer = await ask({ title: "Copy Stage's data to production?", text: "Production starts from what Stage gathered. Its own data is snapshotted first, so a restore brings it back. Type the app's name to say so.", ok: "Copy it", typed: s.name });
        if (answer) change("copy", { confirm: s.name }, "Copying Stage's data to production");
      } }, "Copy Stage's to production…")));
  // Backups.
  const keep = el("input", { placeholder: "7,4,6", class: "small-input", "aria-label": "Daily, weekly, monthly" });
  parts.push(el("h2", {}, "Backups"), el("pre", { class: "job-output short" }, s.backups),
    el("div", { class: "data-actions" }, el("span", { class: "muted small" }, "Keep daily, weekly, monthly"), keep, el("button", { class: "small", onclick: () => change("keep", { keep: keep.value.trim() }) }, "Save")),
    el("div", { class: "data-actions" }, el("button", { class: "small", onclick: offsite }, "Copy them offsite…"), el("button", { class: "small", onclick: () => change("offsite", { offsite: "off" }) }, "Stop copying offsite")),
    el("p", { class: "muted small" }, "The key they're encrypted with stays on the machine: hi app backups " + s.name + " --show-key."));
  parts.push(el("h2", {}, "Jobs"), el("pre", { class: "job-output short" }, s.jobs),
    el("div", { class: "data-actions" }, el("a", { class: "button-like small", href: "/api/app/bom?name=" + encodeURIComponent(s.name) }, "Production's bill of materials (CycloneDX)")));
  return parts;
}

async function restore(app, snapshot) {
  const answer = await ask({ title: "Restore " + app + " from " + snapshot + "?", text: "The lead's memory, the app's record, and its data go back to how they were then. hi takes a snapshot of how it is first. Type the app's name to say so.", ok: "Restore", typed: app, check: "The code (repo/) too" });
  if (answer) change("restore", { snapshot, code: answer.checked, confirm: app }, "Restoring " + app);
}
async function rollback(app, version) {
  const answer = await ask({ title: "Roll " + app + " back to v" + version + "?", text: "Production goes back to v" + version + ", with its data as v" + version + " left it; what was entered since is kept in a snapshot. Type the app's name to say so.", ok: "Roll back", typed: app, check: "The code only: production's data stays as it is" });
  if (answer) change("rollback", { version, code: answer.checked, confirm: app }, "Rolling " + app + " back");
}
async function offsite() {
  const answer = await ask({ title: "Copy the snapshots offsite", text: "An S3-compatible bucket, as s3:https://<host>/<bucket>. Its keys come next.", ok: "Next", field: "s3:https://s3.example.com/backups", fieldCheck: (v) => (v.startsWith("s3:") ? "" : "It starts with s3:") });
  if (!answer) return;
  const id = await ask({ title: "The bucket's access key ID", text: "Kept on the machine, never shown again.", ok: "Next", field: "Access key ID" });
  if (!id) return;
  const secret = await ask({ title: "Its secret access key", text: "Kept on the machine, never shown again.", ok: "Save", field: "Secret access key" });
  if (!secret) return;
  change("offsite", { offsite: answer.field, key_id: id.field, secret: secret.field }, "The snapshots go offsite too.");
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
  const domain = el("input", { value: n.domain || "", placeholder: "apps.example.com", "aria-label": "The apps' domain" });
  const token = el("input", { type: "password", placeholder: n.has_token ? "NetBird's token is kept; a new one replaces it" : "NetBird API token", "aria-label": "NetBird token" });
  const apps = el("ul", { class: "list" });
  for (const a of n.apps) {
    apps.append(el("li", {}, el("span", { class: "what" }, el("button", { class: "link-button", onclick: async () => { await openView(a.name); showSection("network"); } }, a.name),
      el("span", { class: "muted small block" }, (a.joined ? "" : "not on NetBird · ") + a.peer + " · " + a.addresses + (a.allow_ips && a.allow_ips.length ? " · only " + a.allow_ips.join(", ") : "")))));
  }
  const pName = el("input", { placeholder: "Its name, such as google", "aria-label": "Provider's name" });
  const pIssuer = el("input", { placeholder: "Its issuer, https://…", "aria-label": "Issuer" });
  const pClient = el("input", { placeholder: "Client ID", "aria-label": "Client ID" });
  const pSecret = el("input", { type: "password", placeholder: "Client secret (kept, never shown)", "aria-label": "Client secret" });
  const parts = [
    el("h2", {}, "The apps' domain"), el("p", { class: "muted small" }, n.domain ? "Apps can have names that stay under " + n.domain + "." : "The apps get addresses NetBird makes up. A domain verified in NetBird gives them names that stay."),
    el("div", { class: "stack" }, domain, token, el("div", { class: "data-actions" }, el("button", { class: "small", onclick: async () => { await netChange("domain", { name: domain.value.trim(), key: token.value.trim() }); } }, "Save"), n.domain ? el("button", { class: "small", onclick: () => netChange("domain", { name: "off" }) }, "Off") : "")),
    el("h2", {}, "Each app's peer"), apps,
    el("h2", {}, "Sign-in providers"), el("pre", { class: "job-output short" }, n.sso),
    el("details", { class: "small" }, el("summary", {}, "Add one"), el("div", { class: "stack" }, pName, pIssuer, pClient, pSecret,
      el("button", { class: "small", onclick: () => netChange("provider", { sso: pName.value.trim(), host: pIssuer.value.trim(), name: pClient.value.trim(), secret: pSecret.value.trim() }) }, "Check and add"))),
  ];
  if (n.expose) parts.push(el("h2", {}, "The org server for cloud runs"), el("p", { class: "small" }, el("code", {}, shown(n.expose))));
  parts.push(el("h2", {}, "What to ask your IT team"), el("p", { class: "muted small" }, "As text to send: the policies and records the admin page and the apps need."),
    el("pre", { class: "job-output copyable" }, n.ask || "Nothing."), el("div", { class: "data-actions" }, el("button", { class: "small", onclick: () => copyText(n.ask) }, "Copy")));
  box.replaceChildren(...parts);
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
    if (c.started && !c.error) return "Checking production " + c.bom + "'s packages, since " + ago(c.started) + "…";
    if (c.asked) return "Asked " + ago(c.asked) + ": the app starts the check within a minute.";
    if (!c.bom) return "Nothing released, so nothing to check.";
    if (!c.matched) return "Production " + c.bom + " isn't checked yet.";
    return "Production " + c.bom + " checked " + ago(c.matched) + "; again every day.";
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
    toast((answer.asked.length ? "Checking " + answer.asked.join(", ") + ": a few minutes." : "Nothing to check.") + (skipped.length ? " Not " + skipped.join("; ") + "." : ""), skipped.length > 0);
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
  button.title = openApp && c && !c.up ? openApp + " isn't running: its watcher checks once it starts" : (openApp ? "Match " + openApp : "Match every app") + "'s production packages against the advisories now, not at the day's check";
  button.onclick = checkAdvisories;
  clearTimeout(advisoriesPoll);
  if (pending) advisoriesPoll = setTimeout(() => { if (viewShown("advisories")) refreshAdvisories(); }, 5000);
  list.replaceChildren();
  if (!openApp) list.append(el("li", { class: "empty" }, "Every app's. Open an app for its own."));
  if (!all.length) list.append(el("li", { class: "empty" }, "No advisories for what production runs." + Object.entries(advisories.notes || {}).filter(([app]) => !openApp || app === openApp).map(([app, note]) => " " + app + ": " + note + ".").join("")));
  const words = { kev: "exploited", incident: "act on it", report: "for the weekly report", "": "ignored" };
  for (const a of all) {
    const cls = a.tier === "kev" ? "bad" : a.tier === "incident" ? "blocked" : a.tier === "" ? "done" : "";
    list.append(el("li", { class: "incident" },
      el("div", { class: "incident-head" }, el("span", { class: "dot " + cls, "aria-hidden": "true" }), openApp ? null : el("button", { class: "link-button", onclick: () => openView(a.app) }, a.app), el("strong", {}, a.id), el("span", { class: "muted small" }, words[a.tier])),
      el("div", { class: "small" }, a.package + " " + a.version + " (" + (a.part === "machine" ? "the machine's Linux kernel" + (a.kernel ? ", " + a.kernel : "") : a.part) + ")" + ((a.fixed || []).length ? ", fixed in " + a.fixed.join(", ") : "") + (a.epss >= 0.01 ? ", EPSS " + a.epss.toFixed(2) : "")),
      a.summary ? el("div", { class: "muted small clamp2", title: a.summary }, a.summary) : null,
      a.action ? el("div", { class: "small advisory-action" }, el("strong", {}, a.part === "machine" ? "The machine needs an update. " : "What fixes it: "), a.action) : null,
      a.ignored ? el("div", { class: "muted small" }, "Ignored: " + a.ignored) : null,
      el("div", { class: "data-actions" }, a.tier === "" ? el("button", { class: "small", onclick: () => advisoryChange(a, true) }, "Raise it again") : el("button", { class: "small", onclick: () => advisoryChange(a, false) }, "Ignore…"))));
  }
}
async function advisoryChange(a, unignore) {
  let because = "";
  if (!unignore) {
    const answer = await ask({ title: "Ignore " + a.id + " in " + a.app + "?", text: "Your word that it doesn't apply, until the advisory changes. The app's people see why.", ok: "Ignore it", why: "Why it doesn't apply" });
    if (!answer) return;
    because = typeof answer === "string" ? answer : "";
  }
  try {
    await api("/api/app/settings", { app: a.app, what: "advisory", id: a.id, package: a.package, because, remove: unignore });
    toast(unignore ? a.id + " is raised again if it matches." : a.id + " is ignored.");
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
  if (openApp && $("overview-usage")) $("overview-usage").textContent = usageLine(openApp) || "…";
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
    // The org's settings: every fifteen seconds while one is open.
    if (orgOpen() && orgSection !== "overview" && Date.now() - settingsAt > 15000) { settingsAt = Date.now(); refreshOrgSettings(); }
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
    document.body.replaceChildren(el("p", { class: "locked" }, "The admin page didn't answer: " + e.message));
    return;
  }
  $("signin").hidden = true;
  $("work").hidden = false;
  booted = true;
  renderTabs();
  renderPanes();
  let wanted = "";
  try { wanted = decodeURIComponent(location.hash.slice(1)); } catch (e) { /* a malformed #… opens Home */ }
  renderBar();
  renderApps();
  renderHome();
  renderOrgBadges();
  const [wantedApp, wantedSection] = wanted.split("/");
  if (wantedApp && state.apps.some((a) => a.name === wantedApp)) await openView(wantedApp, ["people", "agents", "slack", "settings", "network", "data"].includes(wantedSection) ? wantedSection : "");
  else if (wantedApp === ":org" && state.org) openOrg(wantedSection || "");
  else if (wanted === ":policy" && state.org) openOrg("policy");
  else if (wanted === ":new" || wanted.startsWith(":new/")) openNew(wanted.slice(5));
  for (const which of ["left", "right"]) if (panes[which].open) freshView(panes[which].view);
  refreshJobs();
  pollTimer = setTimeout(poll, 3000);
}

boot();
