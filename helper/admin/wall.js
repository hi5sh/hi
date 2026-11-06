"use strict";
// A wall screen (hi_admin.md, Wall screens): the org's Running and
// Requests, read-only, from a wall link. It reads every five seconds and
// changes nothing.

const $ = (id) => document.getElementById(id);
const key = location.pathname.split("/")[2] || "";
const dollars = (n) => "$" + (Number(n) || 0).toFixed(2);
const clock = (when) => new Date(when).toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" });
function hours(seconds) {
  const s = Number(seconds) || 0;
  if (s < 3600) return Math.round(s / 60) + " min";
  const h = s / 3600;
  return (Number.isInteger(h) ? h : h.toFixed(1)) + " h";
}
function ago(when) {
  const s = Math.max(0, Math.round((Date.now() - new Date(when).getTime()) / 1000));
  if (s < 60) return "now";
  if (s < 3600) return Math.round(s / 60) + " min ago";
  return Math.round(s / 3600) + " h ago";
}
function el(tag, cls, ...children) {
  const node = document.createElement(tag);
  if (cls) node.className = cls;
  for (const child of children.flat()) if (child !== null && child !== undefined && child !== "") node.append(child instanceof Node ? child : document.createTextNode(String(child)));
  return node;
}

let lastOK = 0;
async function read() {
  try {
    const response = await fetch("/wall/" + encodeURIComponent(key) + "/state", { cache: "no-store" });
    const data = await response.json();
    if (data.ended) { $("wall-ended").hidden = false; document.querySelector(".wall-columns").hidden = true; return false; }
    if (!response.ok) throw new Error(data.error || response.statusText);
    lastOK = Date.now();
    $("wall-stale").hidden = true;
    render(data);
  } catch (e) {
    if (lastOK) { $("wall-stale").hidden = false; $("wall-stale-at").textContent = clock(lastOK); }
  }
  return true;
}

function render(d) {
  document.title = d.name + " · hi on " + d.machine;
  $("wall-title").textContent = "hi on " + d.machine + " · " + d.name;
  $("wall-totals").textContent = (d.running.length ? d.running.length + " running, " + dollars(d.rate_per_hour) + " an hour" : "Nothing runs") +
    " · today " + dollars(d.today) + " · this month " + dollars(d.month) + (d.month_budget ? " of " + dollars(d.month_budget) : "");
  $("wall-budgets").replaceChildren(...(d.budgets || []).map((b) => {
    const share = b.budget ? Math.min(1, b.spend / b.budget) : 0;
    const bar = el("span", "wall-bar" + (b.spend > b.budget ? " over" : share > 0.8 ? " near" : ""));
    bar.style.setProperty("--share", (share * 100).toFixed(1) + "%");
    return el("span", "wall-budget", el("span", "", b.group), bar, el("span", "muted", dollars(b.spend) + " of " + dollars(b.budget)));
  }));
  $("wall-running-n").textContent = d.running.length || "";
  $("wall-running").replaceChildren(...(d.running.length ? d.running.map((r) => {
    const left = new Date(r.deadline).getTime() - Date.now();
    return el("li", "wall-item",
      el("div", "wall-line", el("strong", "", r.name), el("span", "muted", r.who + (r.agent ? " · " + r.agent : "")), el("span", "grow"), el("span", "", dollars(r.cost))),
      el("div", "wall-line muted", r.hardware + " · " + r.rate, el("span", "grow"), el("span", left < 15 * 60000 ? "warn" : "", "stops " + clock(r.deadline))),
      r.reason ? el("div", "wall-line muted wall-reason", r.reason) : null);
  }) : [el("li", "wall-empty muted", "Nothing runs.")]));
  $("wall-waiting-n").textContent = d.waiting.length || "";
  $("wall-waiting").replaceChildren(...(d.waiting.length ? d.waiting.map((w) => {
    const what = w.kind === "enroll" ? "wants to join" : w.kind === "extend" ? "asks for " + hours(w.seconds) + " more on " + w.name : w.kind === "serve" ? "wants to serve a model" : (w.hardware || "a machine") + (w.seconds ? " for " + hours(w.seconds) : "") + (w.rate ? ", " + w.rate : "");
    return el("li", "wall-item", el("div", "wall-line", el("strong", "", w.who), el("span", "", what), el("span", "grow"), el("span", "muted", ago(w.created))),
      w.over_budget ? el("div", "wall-line warn", "over budget") : null, w.reason ? el("div", "wall-line muted wall-reason", w.reason) : null);
  }) : [el("li", "wall-empty muted", "Nothing waits.")]));
  $("wall-activity").replaceChildren(...(d.activity || []).slice(0, 12).map((a) => el("li", "wall-line", el("span", "muted wall-at", clock(a.time)), el("span", "", a.text))));
}

function tick() { $("wall-clock").textContent = new Date().toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" }); }
tick();
setInterval(tick, 10000);
(async function loop() { if (await read()) setTimeout(loop, 5000); })();
