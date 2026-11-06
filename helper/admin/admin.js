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
  right: { view: "activity", open: true, width: 340 },
};
const paneViews = { left: ["apps", "machine"], right: ["activity", "jobs", "logs", "incidents"] };
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
  $("crumb").hidden = !openApp;
  $("sections").hidden = !openApp;
  $("open-box").hidden = !openApp;
  $("crumb-app").textContent = openApp;
  document.title = (openApp ? openApp + " · " : "") + "Admin page · " + state.machine;
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
  badge("incidents-count", state.apps.reduce((n, a) => n + (a.incidents || 0), 0));
}
$("apps-search").addEventListener("input", renderApps);

function renderHome() {
  const cards = $("home-cards");
  cards.replaceChildren();
  const up = state.apps.filter((a) => a.state === "up").length;
  $("home-summary").textContent = state.apps.length ? `${up} of ${state.apps.length} up` : "";
  if (!state.apps.length) {
    cards.append(el("div", { class: "app-card empty-card" },
      el("p", {}, "No apps on this machine yet."),
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
      app.incidents ? el("p", { class: "warn small" }, app.incidents + " open incident" + (app.incidents === 1 ? "" : "s")) : null);
    cards.append(el("div", { class: "card-box" }, card, app.addresses.length ? addresses : null));
  }
}

$("home-button").addEventListener("click", () => openView(""));

async function openView(name) {
  openApp = name;
  detail = null;
  $("home").hidden = !!name;
  $("overview").hidden = !name;
  if (name) {
    $("overview").replaceChildren(el("p", { class: "muted" }, "…"));
    if (narrow()) { panes.left.open = false; keepPanes(); renderPanes(); }
  }
  history.replaceState(null, "", name ? "#" + encodeURIComponent(name) : "/");
  renderBar();
  renderApps();
  if (name) await refreshDetail();
  if (viewShown("logs")) refreshLogs(true);
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
function ask({ title, text, ok, why }) {
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
    const done = (value) => {
      dialog.close();
      $("ask-form").onsubmit = null;
      $("ask-cancel").onclick = null;
      resolve(value);
    };
    $("ask-form").onsubmit = (event) => {
      event.preventDefault();
      if (why) {
        if (!reason.value.trim()) { $("ask-error").textContent = "Say why: the app's people are told."; return; }
        done(reason.value.trim());
      } else done(true);
    };
    $("ask-cancel").onclick = () => done(false);
    dialog.onclose = () => resolve(false);
    dialog.showModal();
    (why ? reason : $("ask-ok")).focus();
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
  const shown = JSON.stringify([state.version, state.latest, state.address, state.line, state.policy, state.lingers, state.needs_key, state.admins, state.me]);
  if (shown === machineShown && !force) return;
  machineShown = shown;
  const version = $("machine-version");
  version.replaceChildren(el("p", {}, el("strong", {}, "hi " + state.version), state.latest ? el("span", { class: "warn" }, " · " + state.latest + " is out") : el("span", { class: "muted" }, " · the newest, as far as GitHub said")));
  version.append(el("button", { class: state.latest ? "primary" : "", onclick: update }, state.latest ? "Update to " + state.latest : "Update hi"));
  version.append(el("p", { class: "muted small" }, "The apps and this page restart on the new hi: each app's lead is away for about half a minute, the page for a second."));
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
  for (const admin of state.admins) {
    const me = admin.name === state.me.name;
    const acts = el("span", { class: "actions" });
    acts.append(el("button", { onclick: () => newLink(admin, false), title: "A new link, shown to copy; their old one ends" }, "New link"));
    if (admin.slack) acts.append(el("button", { onclick: () => newLink(admin, true), title: "A new link in their Slack DMs; their old one ends" }, "By Slack"));
    if (!me) acts.append(el("button", { class: "danger", onclick: () => removeAdmin(admin) }, "Remove"));
    const what = el("span", { class: "what" }, el("strong", {}, admin.name), me ? el("span", { class: "muted" }, " (you)") : null,
      el("span", { class: "muted small block" }, admin.slack ? "Slack " + admin.slack : "no Slack member ID"));
    const li = el("li", {}, what, acts);
    list.append(li);
    if (me && !admin.slack) {
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
  const name = $("admin-add-name").value.trim(), slack = $("admin-add-slack").value.trim(), send = $("admin-add-send").checked;
  try {
    const answer = await api("/api/admins", { action: "add", name, slack, send });
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
    if (!list.length) box.append(el("li", { class: "empty" }, "Nothing yet: what admins do here shows up here."));
    for (const entry of list) {
      box.append(el("li", {}, el("div", {}, el("strong", {}, entry.who), " ", entry.what, entry.app ? el("span", { class: "muted" }, " · " + entry.app) : null),
        el("div", { class: "muted small" }, ago(entry.at))));
    }
  } catch (e) { /* the next round */ }
}

let jobs = [];
let jobOpen = "";
let jobFrom = 0;
function jobsSeen() { return Number(localStorage.getItem("hi-admin-jobs-seen") || 0); }
function markJobsSeen() { try { localStorage.setItem("hi-admin-jobs-seen", String(Date.now())); } catch (e) { /* private mode */ } renderJobsBadge(); }
function renderJobsBadge() {
  const node = $("jobs-badge");
  const running = jobs.some((j) => j.state === "running") || (state && state.running > 0);
  const failed = jobs.some((j) => j.state === "failed" && new Date(j.ended).getTime() > jobsSeen());
  node.hidden = !running && !failed;
  node.classList.toggle("running", running);
  node.classList.toggle("failed", !running && failed);
}

async function refreshJobs() {
  try { jobs = await api("/api/jobs"); } catch (e) { return; }
  renderJobsBadge();
  const list = $("jobs-list");
  list.replaceChildren();
  if (!jobs.length) list.append(el("li", { class: "empty" }, "No jobs yet: a restart, a rebuild, a snapshot, or an update runs here, with its output."));
  for (const job of jobs) {
    const cls = job.state === "running" ? "working" : job.state === "done" ? "done" : "bad";
    list.append(el("li", {}, el("span", { class: "dot " + cls, "aria-hidden": "true" }),
      el("button", { class: "stask-row", onclick: () => openJob(job.id) }, el("span", { class: "stask-title" }, job.title),
        el("span", { class: "stask-num" }, job.by + " · " + ago(job.started)))));
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
    else { refreshState(); if (openApp && answer.job.app === openApp) refreshDetail(); }
  } catch (e) { /* the next round */ }
}

// Logs: which app's, its lines as they come; a dot when new ones came
// while the view was away, red when one says something failed.
const logs = { app: null, cursor: "", lines: 0, at: 0, news: "" };
const errorLine = /\b(error|failed|fail|panic|fatal|refused|denied|traceback|exception)\b|\b5\d\d\b/i;
const warnLine = /\bwarn(ing)?\b/i;
function logsApp() {
  const select = $("logs-app");
  const wanted = openApp || select.value || (state && state.apps[0] ? state.apps[0].name : "");
  const options = [["", "The admin page"], ...(state ? state.apps.map((a) => [a.name, a.name]) : [])];
  if (select.options.length !== options.length || [...select.options].some((o, i) => o.value !== options[i][0])) {
    select.replaceChildren(...options.map(([value, label]) => el("option", { value }, label)));
  }
  if (openApp && select.value !== openApp) select.value = openApp;
  else if (logs.app === null) select.value = wanted;
  return select.value;
}
$("logs-app").addEventListener("change", () => refreshLogs(true));
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
    const list = await api("/api/incidents");
    const box = $("incidents-list");
    box.replaceChildren();
    if (!list.length) box.append(el("li", { class: "empty" }, "No incidents in the last month."));
    for (const incident of list) {
      const cls = incident.status === "closed" ? "done" : incident.status === "waiting" ? "blocked" : "bad";
      box.append(el("li", { class: "incident" },
        el("div", { class: "incident-head" }, el("span", { class: "dot " + cls, "aria-hidden": "true" }),
          el("button", { class: "link-button", onclick: () => openView(incident.app) }, incident.app), el("span", { class: "muted small" }, incident.id + " · " + incident.status + " · " + ago(incident.opened))),
        el("div", { class: "clamp3", title: incident.title }, incident.title),
        el("div", { class: "muted small clamp2", title: incident.last || "" }, incident.where + (incident.last ? " · " + incident.last : ""))));
    }
  } catch (e) { /* the next round */ }
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
  renderBar();
  renderApps();
  renderHome();
  renderJobsBadge();
  if (viewShown("machine")) renderMachine();
  else badge("machine-badge", state.latest ? 1 : 0);
  return true;
}

let pollTimer = 0;
async function poll() {
  clearTimeout(pollTimer);
  const ok = await refreshState();
  if (ok) {
    if (openApp) refreshDetail();
    for (const view of [panes.right.view]) if (viewShown(view) && view !== "logs") freshView(view);
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
  renderPanes();
  const wanted = decodeURIComponent(location.hash.slice(1));
  renderBar();
  renderApps();
  renderHome();
  if (wanted && state.apps.some((a) => a.name === wanted)) await openView(wanted);
  for (const which of ["left", "right"]) if (panes[which].open) freshView(panes[which].view);
  refreshJobs();
  pollTimer = setTimeout(poll, 3000);
}

boot();
