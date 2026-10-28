// The hi team helper: a panel beside any page of the team's app, with
// help, answered from the app's guide, and feedback to the team, which
// the lead files for the owners (from hi 2.4 the chat with the lead is in
// Slack, not here). hi net expose adds this script to each page
// it relays and serves /__hi/ from the team's service, so the app holds
// none of it (docs/specs/approved/hi_team_helper.md and
// hi_team_helper_signin.md). Plain JavaScript, no framework,
// no build step: hi serves this file inside one closure with finder
// (third_party/finder), whose finder() makes select mode's selectors.
(() => {
  "use strict";
  if (window.__hiHelper || !document.body) return;
  window.__hiHelper = true;

  const API = "/__hi/api/";
  const store = {
    get(area, key) {
      try { return JSON.parse(window[area].getItem("hi-helper-" + key)); } catch { return null; }
    },
    set(area, key, value) {
      try {
        if (value === null || value === undefined) window[area].removeItem("hi-helper-" + key);
        else window[area].setItem("hi-helper-" + key, JSON.stringify(value));
      } catch { /* private window, blocked storage: the helper works without it */ }
    },
  };

  const CSS = `
:host { all: initial; position: fixed; z-index: 2147483647; inset: 0 auto auto 0; width: 0; height: 0;
  --bg: #ffffff; --fg: #1d1d1f; --muted: #6e6e73; --line: #e3e3e8; --soft: #f4f4f7;
  --accent: #2f5bea; --accent-fg: #ffffff; --mine: #e9eefe; --warn: #fff4d6; --danger: #d93025; --shadow: 0 12px 40px rgba(0,0,0,.18);
  font: 14px/1.45 system-ui, -apple-system, "Segoe UI", Roboto, sans-serif; color: var(--fg); }
@media (prefers-color-scheme: dark) {
  :host { --bg: #1c1c1f; --fg: #f2f2f5; --muted: #a1a1a8; --line: #34343a; --soft: #26262b;
    --accent: #6d8cff; --accent-fg: #0d0d10; --mine: #26304f; --warn: #3a3220; --danger: #ff6b5e; --shadow: 0 12px 40px rgba(0,0,0,.5); }
}
* { box-sizing: border-box; }
button { font: inherit; color: inherit; cursor: pointer; }
.fab { position: fixed; width: 48px; height: 48px; border-radius: 50%; border: 0; background: var(--accent);
  color: var(--accent-fg); box-shadow: var(--shadow); display: grid; place-items: center; touch-action: none; }
.fab svg { width: 24px; height: 24px; }
.fab .dot { position: absolute; top: 4px; right: 4px; width: 10px; height: 10px; border-radius: 50%;
  background: #e5484d; border: 2px solid var(--bg); display: none; }
.fab.unread .dot { display: block; }
.fab.tl, .fab.tr { top: 16px; } .fab.bl, .fab.br { bottom: 16px; }
.fab.tl, .fab.bl { left: 16px; } .fab.tr, .fab.br { right: 16px; }
.panel { position: fixed; top: 0; bottom: 0; width: min(var(--width, 400px), 100vw); background: var(--bg);
  border-left: 1px solid var(--line); box-shadow: var(--shadow); display: none; flex-direction: column; }
.panel.right { right: 0; } .panel.left { left: 0; border-left: 0; border-right: 1px solid var(--line); }
.panel.open { display: flex; }
.grip { position: absolute; top: 0; bottom: 0; width: 10px; cursor: col-resize; touch-action: none; z-index: 1; outline: none; }
.panel.right .grip { left: -5px; } .panel.left .grip { right: -5px; }
.grip::after { content: ""; position: absolute; top: 0; bottom: 0; left: 4px; width: 2px; background: transparent; transition: background .15s; }
.grip:hover::after, .grip:focus-visible::after, .panel.resizing .grip::after { background: var(--accent); }
.panel.resizing { user-select: none; }
@media (max-width: 640px) {
  .panel, .panel.left, .panel.right { top: auto; left: 0; right: 0; width: 100vw; height: 85vh;
    border: 0; border-top: 1px solid var(--line); border-radius: 14px 14px 0 0; }
  .grip { display: none; }
}
header { display: flex; align-items: center; gap: 8px; padding: 12px 12px 12px 16px; border-bottom: 1px solid var(--line); }
header .title { flex: 1; min-width: 0; }
header .name { font-weight: 600; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
header .version { color: var(--muted); font-size: 12px; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.icon { border: 0; background: none; width: 32px; height: 32px; border-radius: 8px; display: grid; place-items: center; color: var(--muted); }
.icon:hover { background: var(--soft); color: var(--fg); }
.icon svg { width: 18px; height: 18px; }
.icon[aria-pressed="true"] { background: var(--soft); color: var(--fg); }
.icon[hidden] { display: none; }
.menu { position: absolute; top: 52px; right: 12px; background: var(--bg); border: 1px solid var(--line);
  border-radius: 10px; box-shadow: var(--shadow); padding: 4px; display: none; min-width: 200px; }
.menu.open { display: block; }
.menu button { display: block; width: 100%; text-align: left; border: 0; background: none; padding: 8px 10px; border-radius: 6px; }
.menu button:hover { background: var(--soft); }
.menu button[hidden] { display: none; }
.notice { margin: 8px 12px 0; padding: 8px 10px; border-radius: 8px; background: var(--warn); font-size: 13px;
  display: flex; gap: 8px; align-items: center; }
.notice[hidden] { display: none; }
.notice button { margin-left: auto; border: 1px solid var(--line); background: var(--bg); border-radius: 6px; padding: 2px 8px; }
.body { flex: 1; overflow-y: auto; padding: 12px 16px; display: flex; flex-direction: column; gap: 10px; }
.empty { color: var(--muted); margin: auto 8px; text-align: center; }
.msg { max-width: 92%; padding: 8px 12px; border-radius: 12px; overflow-wrap: anywhere; }
.msg.person { align-self: flex-end; background: var(--mine); border-bottom-right-radius: 4px; white-space: pre-wrap; }
.msg.lead { align-self: flex-start; background: var(--soft); border-bottom-left-radius: 4px; }
.msg.answer { white-space: pre-wrap; }
.msg .when { font-size: 11px; color: var(--muted); margin-bottom: 2px; }
.msg.error { align-self: stretch; background: var(--warn); font-size: 13px; }
.msg .who { font-size: 11px; color: var(--muted); margin-bottom: 2px; }
.msg p { margin: 0 0 6px; } .msg p:last-child { margin-bottom: 0; }
.msg ul, .msg ol { margin: 0 0 6px; padding-left: 20px; }
.msg code { font: 12px/1.4 ui-monospace, SFMono-Regular, Menlo, monospace; background: var(--line); padding: 1px 4px; border-radius: 4px; }
.msg pre { margin: 0 0 6px; padding: 8px; background: var(--line); border-radius: 6px; overflow-x: auto; }
.msg pre code { background: none; padding: 0; }
.msg a { color: var(--accent); }
.typing { color: var(--muted); font-size: 13px; display: flex; align-items: center; gap: 8px; }
.typing::before { content: ""; width: 14px; height: 14px; flex: none; border-radius: 50%; border: 2px solid var(--line);
  border-top-color: var(--accent); animation: spin .8s linear infinite; }
@keyframes spin { to { transform: rotate(360deg); } }
@media (prefers-reduced-motion: reduce) { .typing::before { animation-duration: 2.4s; } }
@keyframes blink { 50% { opacity: .2; } }
.composer { border-top: 1px solid var(--line); padding: 10px 12px; display: flex; gap: 8px; align-items: flex-end; }
textarea { flex: 1; resize: none; font: inherit; color: var(--fg); background: var(--soft); border: 1px solid var(--line);
  border-radius: 10px; padding: 8px 10px; max-height: 160px; min-height: 38px; outline: none; }
textarea:focus { border-color: var(--accent); }
.send { border: 0; background: var(--accent); color: var(--accent-fg); border-radius: 10px; height: 38px; padding: 0 14px; font-weight: 600; }
.send:disabled { opacity: .5; cursor: default; }
.tabs { display: flex; gap: 4px; padding: 8px 12px 0; }
.tabs[hidden] { display: none; }
.tabs button { flex: 1; border: 1px solid var(--line); background: var(--bg); color: var(--muted); border-radius: 8px; padding: 6px 10px; font-weight: 600; }
.tabs button.on { background: var(--mine); color: var(--fg); border-color: var(--accent); }
.tabs button[hidden] { display: none; }
.tabs button.new::after { content: " •"; color: var(--accent); }
.extra { display: flex; flex-wrap: wrap; gap: 6px 12px; align-items: center; font-size: 13px; color: var(--muted); }
.extra[hidden] { display: none; }
.extra input.your-name { flex: 1 1 160px; font: inherit; color: var(--fg); background: var(--soft); border: 1px solid var(--line); border-radius: 8px; padding: 5px 8px; }
.extra input.your-name.needed { border-color: var(--danger); animation: flash .45s ease-in-out 4; }
@keyframes flash { 50% { border-color: var(--line); box-shadow: 0 0 0 3px var(--danger); } }
@media (prefers-reduced-motion: reduce) { .extra input.your-name.needed { animation: none; } }
.extra label { display: inline-flex; gap: 6px; align-items: center; cursor: pointer; }
.intro { color: var(--muted); margin: auto 8px; text-align: center; display: flex; flex-direction: column; gap: 8px; }
.intro p { margin: 0; }
.sent { margin: auto 8px; text-align: center; display: flex; flex-direction: column; gap: 10px; align-items: center; }
.sent strong { font-size: 15px; }
.link-button { border: 0; background: none; color: var(--accent); padding: 0; text-decoration: underline; font-size: 13px; align-self: flex-start; }
.link-button.center { align-self: center; }
.composer { flex-direction: column; align-items: stretch; }
.composer .row { display: flex; gap: 8px; align-items: flex-end; }
.pick { border: 1px solid var(--line); background: var(--bg); color: var(--muted); border-radius: 10px; width: 38px; height: 38px;
  display: grid; place-items: center; flex: none; }
.pick:hover, .pick.on { color: var(--accent); border-color: var(--accent); }
.pick svg { width: 18px; height: 18px; }
.chips { display: flex; flex-wrap: wrap; gap: 6px; }
.chips:empty { display: none; }
.chip { display: inline-flex; align-items: center; gap: 4px; max-width: 100%; font-size: 12px; padding: 2px 4px 2px 8px;
  border-radius: 999px; background: var(--mine); border: 1px solid var(--line); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.chip button { border: 0; background: none; color: var(--muted); padding: 0 4px; font-size: 14px; line-height: 1; }
.msg .chips { margin-top: 6px; }
.msg .chip { padding-right: 8px; background: var(--bg); }
.layer { position: fixed; inset: 0; pointer-events: none; }
.box { position: fixed; border: 2px solid var(--accent); background: color-mix(in srgb, var(--accent) 12%, transparent);
  border-radius: 3px; display: none; }
.box.frozen { border-style: solid; background: color-mix(in srgb, var(--accent) 8%, transparent); }
.box .n { position: absolute; top: -10px; left: -10px; min-width: 20px; height: 20px; border-radius: 10px; background: var(--accent);
  color: var(--accent-fg); font-size: 11px; font-weight: 700; display: grid; place-items: center; padding: 0 5px; }
.label { position: fixed; display: none; background: var(--accent); color: var(--accent-fg); font: 12px/1.3 ui-monospace, SFMono-Regular, Menlo, monospace;
  border-radius: 4px; padding: 2px 6px; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; max-width: 90vw; }
.banner { position: fixed; top: 10px; left: 50%; transform: translateX(-50%); display: none; align-items: center; gap: 6px;
  pointer-events: auto; background: var(--fg); color: var(--bg); border-radius: 999px; padding: 5px 5px 5px 14px; font-size: 13px;
  box-shadow: var(--shadow); max-width: calc(100vw - 20px); }
.banner .hint { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.banner button { border: 0; border-radius: 999px; padding: 4px 10px; background: var(--bg); color: var(--fg); font-weight: 600; flex: none; }
.banner .step, .banner .ok { display: none; }
.banner.outlined .step { display: inline-block; }
.banner.outlined.touch .ok { display: inline-block; background: var(--accent); color: var(--accent-fg); }
.selecting .banner { display: flex; }
.panel.away { display: none; }
.pick.attach { cursor: pointer; }
.files { display: flex; flex-wrap: wrap; gap: 6px; margin-top: 6px; align-items: flex-start; }
.files .file svg { width: 13px; height: 13px; flex: none; }
.files:empty { display: none; }
.files a.picture { display: block; line-height: 0; border-radius: 8px; overflow: hidden; border: 1px solid var(--line); background: var(--bg); }
.files a.picture img { max-width: 220px; max-height: 160px; display: block; }
.files .file { display: inline-flex; align-items: center; gap: 4px; font-size: 12px; padding: 4px 8px; border-radius: 8px;
  border: 1px solid var(--line); background: var(--bg); color: var(--fg); text-decoration: none; max-width: 100%; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.composer .files { margin-top: 0; }
.composer .files .item { position: relative; }
.composer .files img { height: 48px; max-width: 96px; display: block; border-radius: 6px; border: 1px solid var(--line); object-fit: cover; }
.composer .files .item button { position: absolute; top: -6px; right: -6px; width: 18px; height: 18px; border-radius: 50%; border: 0;
  background: var(--fg); color: var(--bg); font-size: 12px; line-height: 18px; padding: 0; }
.chip img { height: 16px; max-width: 40px; border-radius: 3px; object-fit: cover; flex: none; }
.panel.dragging .body { outline: 2px dashed var(--accent); outline-offset: -8px; }
.toast { position: fixed; max-width: min(320px, calc(100vw - 32px)); text-align: left; border: 1px solid var(--line); background: var(--bg);
  color: var(--fg); border-radius: 12px; box-shadow: var(--shadow); padding: 8px 12px; font-size: 13px; display: none; }
.toast.show { display: block; }
.toast .from { display: block; font-size: 11px; color: var(--muted); }
.toast .text { display: -webkit-box; -webkit-line-clamp: 3; -webkit-box-orient: vertical; overflow: hidden; }
.toast.tl, .toast.tr { top: 72px; } .toast.bl, .toast.br { bottom: 72px; }
.toast.tl, .toast.bl { left: 16px; } .toast.tr, .toast.br { right: 16px; }
`;

  const ICONS = {
    chat: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21 12a8 8 0 0 1-11.6 7.1L4 20l1-4.6A8 8 0 1 1 21 12z"/></svg>',
    close: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M6 6l12 12M18 6L6 18"/></svg>',
    target: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><circle cx="12" cy="12" r="7"/><circle cx="12" cy="12" r="2"/><path d="M12 2v3M12 19v3M2 12h3M19 12h3"/></svg>',
    clip: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21 11.5l-8.6 8.6a5 5 0 0 1-7.1-7.1l8.6-8.6a3.3 3.3 0 0 1 4.7 4.7l-8.6 8.6a1.7 1.7 0 0 1-2.4-2.4l7.9-7.9"/></svg>',
    more: '<svg viewBox="0 0 24 24" fill="currentColor"><circle cx="5" cy="12" r="1.8"/><circle cx="12" cy="12" r="1.8"/><circle cx="19" cy="12" r="1.8"/></svg>',
    fresh: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 4H6a2 2 0 0 0-2 2v12a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2v-6"/><path d="M18.4 2.6a2 2 0 0 1 2.9 2.9L12 14.8l-3.6.8.8-3.6z"/></svg>',
  };

  // ---------------------------------------------------------------------
  // a safe subset of Markdown: everything is escaped first, then
  // paragraphs, lists, bold, italics, code, and http(s) links

  const escape = (text) => text.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;");

  function inline(text) {
    const codes = [];
    let html = escape(text).replace(/`([^`]+)`/g, (_, code) => { codes.push("<code>" + code + "</code>"); return "\u0001" + (codes.length - 1) + "\u0001"; });
    html = html.replace(/\*\*([^*]+)\*\*/g, "<strong>$1</strong>");
    html = html.replace(/(^|[^*\w])\*([^*\s][^*]*?)\*(?!\w)/g, "$1<em>$2</em>");
    html = html.replace(/\[([^\]]+)\]\((https?:\/\/[^\s)]+)\)/g, '<a href="$2" target="_blank" rel="noopener noreferrer">$1</a>');
    html = html.replace(/(^|[\s(])(https?:\/\/[^\s<)]+[^\s<).,;:!?])/g, '$1<a href="$2" target="_blank" rel="noopener noreferrer">$2</a>');
    return html.replace(/\u0001(\d+)\u0001/g, (_, i) => codes[+i]);
  }

  function markdown(text) {
    const blocks = [];
    text = text.replace(/```[^\n]*\n([\s\S]*?)(```|$)/g, (_, code) => {
      blocks.push("<pre><code>" + escape(code.replace(/\n$/, "")) + "</code></pre>");
      return "\n\n\u0000" + (blocks.length - 1) + "\u0000\n\n";
    });
    let html = "";
    for (const paragraph of text.split(/\n{2,}/)) {
      const trimmed = paragraph.trim();
      if (!trimmed) continue;
      const block = /^\u0000(\d+)\u0000$/.exec(trimmed);
      if (block) { html += blocks[+block[1]]; continue; }
      let list = null, lines = [];
      const flush = () => { if (lines.length) { html += "<p>" + lines.map(inline).join("<br>") + "</p>"; lines = []; } };
      const close = () => { if (list) { html += "</" + list + ">"; list = null; } };
      for (const line of paragraph.split("\n")) {
        const item = /^\s*([-*•]|\d+[.)])\s+(.*)$/.exec(line);
        if (item) {
          flush();
          const kind = /\d/.test(item[1]) ? "ol" : "ul";
          if (list !== kind) { close(); html += "<" + kind + ">"; list = kind; }
          html += "<li>" + inline(item[2]) + "</li>";
        } else if (/^#{1,6}\s+/.test(line)) {
          flush(); close();
          html += "<p><strong>" + inline(line.replace(/^#{1,6}\s+/, "")) + "</strong></p>";
        } else {
          close();
          lines.push(line);
        }
      }
      flush(); close();
    }
    return html;
  }

  // ---------------------------------------------------------------------
  // the element and its parts

  const host = document.createElement("hi-helper");
  const root = host.attachShadow({ mode: "open" });
  // The styles go in as a constructed stylesheet, which an app's
  // Content-Security-Policy (style-src 'self') doesn't block, as it does a
  // <style>; browsers without one get the <style>.
  let constructed = false;
  try {
    const sheet = new CSSStyleSheet();
    sheet.replaceSync(CSS);
    root.adoptedStyleSheets = [sheet];
    constructed = true;
  } catch { /* an older browser */ }
  root.innerHTML = (constructed ? "" : `<style>${CSS}</style>`) + `
<button class="fab" part="button" aria-label="Help" title="Help">${ICONS.chat}<span class="dot"></span></button>
<button class="toast" aria-live="polite"><span class="from"></span><span class="text"></span></button>
<section class="panel" role="dialog" aria-label="Help">
  <div class="grip" role="separator" aria-orientation="vertical" aria-label="Drag to make the panel wider or narrower" title="Drag to resize; double-click for the usual width" tabindex="0"></div>
  <header>
    <div class="title"><div class="name"></div><div class="version"></div></div>
    <button class="icon fresh" aria-label="New questions" title="New questions (or type /clear)">${ICONS.fresh}</button>
    <button class="icon more" aria-label="More">${ICONS.more}</button>
    <button class="icon close" aria-label="Close">${ICONS.close}</button>
    <div class="menu" role="menu">
      <button data-action="new" role="menuitem">New questions</button>
      <button data-action="docs" role="menuitem" hidden>The app's docs</button>
      <button data-action="hide" role="menuitem">Hide the button until reload</button>
    </div>
  </header>
  <div class="tabs" role="tablist" hidden><button type="button" role="tab" data-tab="ask">Ask</button><button type="button" class="docs" title="The app's docs, in a new tab" hidden>Docs</button><button type="button" role="tab" data-tab="feedback">Feedback</button></div>
  <div class="notice" hidden><span>A new version is up.</span><button data-action="reload">Reload</button></div>
  <div class="body" aria-live="polite"></div>
  <form class="composer" novalidate>
    <div class="chips"></div>
    <div class="files"></div>
    <div class="extra" hidden><input class="your-name" placeholder="Your name (not checked)" aria-label="Your name" aria-required="true" maxlength="60"><label><input class="screen" type="checkbox" checked> A picture of what I see</label></div>
    <div class="row">
      <button class="pick" type="button" aria-label="Point at part of the page (Alt+S)" title="Point at part of the page (Alt+S)">${ICONS.target}</button>
      <label class="pick attach" aria-label="Attach files" title="Attach files, or paste or drop them here">${ICONS.clip}<input type="file" multiple hidden></label>
      <textarea rows="1" placeholder="Ask how something works…" aria-label="Message"></textarea>
      <button class="send" type="submit">Send</button>
    </div>
  </form>
</section>
<div class="layer">
  <div class="box hover"></div>
  <div class="frozen-boxes"></div>
  <div class="label"></div>
  <div class="banner"><span class="hint"></span><button class="step up" aria-label="The element around it (Alt+↑)" title="The element around it (Alt+↑)">↑</button><button class="step down" aria-label="Back (Alt+↓)" title="Back (Alt+↓)">↓</button><button class="ok">Use this</button><button class="done">Done</button></div>
</div>`;
  const $ = (selector) => root.querySelector(selector);
  const fab = $(".fab"), panel = $(".panel"), body = $(".body"), menu = $(".menu");
  const input = $("textarea"), sendButton = $(".send"), composer = $(".composer"), notice = $(".notice");
  const layer = $(".layer"), hoverBox = $(".box.hover"), frozenBoxes = $(".frozen-boxes"), label = $(".label"), banner = $(".banner"), chips = $(".chips"), pick = $(".pick");
  const pending = $(".composer .files"), fileInput = $(".attach input"), toast = $(".toast");
  const tabs = $(".tabs"), extra = $(".extra"), nameInput = $(".extra .your-name"), screenBox = $(".extra .screen"), attachLabel = $(".attach");

  // The app's keyboard shortcuts shouldn't fire while someone types here.
  for (const type of ["keydown", "keyup", "keypress"]) host.addEventListener(type, (event) => event.stopPropagation());

  const state = {
    status: null,
    tab: store.get("sessionStorage", "tab") || "ask", // ask or feedback
    help: store.get("sessionStorage", "help") || [], // this tab's questions and answers
    sent: null, // the id of the feedback just sent
    feedback: null, // what this browser sent, newest first, with the team's answers
    sending: false,
    poll: null,
    refs: [], // the elements pointed at for the next message: {ref, el, image, shot}
    files: [], // the files for the next message: {file, url}
  };
  const feedbackTab = () => state.tab === "feedback";
  // Someone signed in with their own link reports bugs, which hi files as
  // tasks, where anyone else sends feedback to the team's channel.
  const reporting = () => !!state.status?.report;
  nameInput.value = store.get("localStorage", "name") || "";
  nameInput.addEventListener("input", () => {
    store.set("localStorage", "name", nameInput.value.trim() || null);
    if (nameInput.value.trim()) nameInput.classList.remove("needed");
  });
  tabs.addEventListener("click", (event) => {
    const button = event.target.closest("button");
    if (button?.classList.contains("docs")) openDocs();
    else if (button?.dataset.tab) setTab(button.dataset.tab);
  });
  // openDocs opens the app's docs: in a new browser tab from the app, so
  // the page stays as it was, or in this one from a page of the docs.
  function openDocs() {
    const docs = state.status?.docs;
    if (!docs) return;
    if (location.pathname.startsWith(docs)) location.assign(docs);
    else window.open(docs, "_blank", "noopener");
  }
  function setTab(tab) {
    state.tab = tab;
    store.set("sessionStorage", "tab", tab);
    state.sent = null;
    render();
    input.focus();
    if (tab === "feedback") loadFeedback();
  }

  // ---------------------------------------------------------------------
  // the button: a click opens the panel; a drag moves it to another corner

  // The button starts bottom left, and the panel opens on the left, beside
  // the page, as in the tools people know; it can be dragged to any corner.
  let corner = store.get("localStorage", "corner") || "bl";
  function placeButton() {
    fab.className = "fab " + corner + (fab.classList.contains("unread") ? " unread" : "");
    toast.className = "toast " + corner + (toast.classList.contains("show") ? " show" : "");
    panel.classList.toggle("left", corner === "tl" || corner === "bl");
    panel.classList.toggle("right", corner === "tr" || corner === "br");
    announce();
  }

  // The panel's width: its inner edge drags wider or narrower, between a
  // phone's width and most of the window, and the browser keeps it.
  const grip = $(".grip");
  const usualWidth = 400;
  const fit = (value) => Math.round(Math.max(320, Math.min(value, Math.max(320, Math.min(1100, innerWidth - 240)))));
  let width = fit(store.get("localStorage", "width") || usualWidth);
  panel.style.setProperty("--width", width + "px");
  function setWidth(value, keep) {
    width = fit(value);
    panel.style.setProperty("--width", width + "px");
    grip.setAttribute("aria-valuenow", String(width));
    if (keep) store.set("localStorage", "width", width === usualWidth ? null : width);
    announce();
  }
  let resizing = false;
  grip.addEventListener("pointerdown", (event) => {
    event.preventDefault();
    resizing = true;
    grip.setPointerCapture(event.pointerId);
    panel.classList.add("resizing");
  });
  grip.addEventListener("pointermove", (event) => {
    if (resizing) setWidth(panel.classList.contains("left") ? event.clientX : innerWidth - event.clientX);
  });
  const stopResizing = () => {
    if (!resizing) return;
    resizing = false;
    panel.classList.remove("resizing");
    setWidth(width, true);
  };
  grip.addEventListener("pointerup", stopResizing);
  grip.addEventListener("pointercancel", stopResizing);
  grip.addEventListener("dblclick", () => setWidth(usualWidth, true));
  grip.addEventListener("keydown", (event) => {
    const wider = panel.classList.contains("left") ? "ArrowRight" : "ArrowLeft";
    const narrower = wider === "ArrowRight" ? "ArrowLeft" : "ArrowRight";
    if (event.key === wider || event.key === narrower) {
      event.preventDefault();
      setWidth(width + (event.key === wider ? 40 : -40), true);
    }
  });
  addEventListener("resize", () => setWidth(store.get("localStorage", "width") || usualWidth));

  // The page can follow the helper, as a new team's welcome page does to
  // point at the button: <hi-helper> says where the button is
  // (data-corner: tl, tr, bl, br), whether the panel is open (data-open),
  // whether the button is hidden (data-hidden), whether whoever is here
  // can chat with the lead (data-chat: no one, since hi 2.4), and how
  // wide the panel is in pixels (data-width), and a "hi-helper" event on
  // window says when any of them changes.
  function announce() {
    const detail = { corner, open: panel.classList.contains("open"), hidden: fab.style.display === "none" && !panel.classList.contains("open"), chat: false, width };
    if (Object.keys(detail).every((key) => host.dataset[key] === String(detail[key]))) return;
    for (const key in detail) host.dataset[key] = String(detail[key]);
    dispatchEvent(new CustomEvent("hi-helper", { detail }));
  }

  // The page can hand the panel some text, as a new team's welcome page
  // does from its own message box: a "hi-helper-send" event on window with
  // { text } opens the panel with the text in its box.
  addEventListener("hi-helper-send", (event) => {
    const text = String(event.detail?.text || "").trim();
    if (!text || !state.status || !host.isConnected) return;
    toggle(true);
    input.value = text;
    store.set("sessionStorage", "draft", text);
    grow();
  });
  let drag = null;
  fab.addEventListener("pointerdown", (event) => {
    drag = { x: event.clientX, y: event.clientY, moved: false };
    fab.setPointerCapture(event.pointerId);
  });
  fab.addEventListener("pointermove", (event) => {
    if (!drag) return;
    if (Math.abs(event.clientX - drag.x) + Math.abs(event.clientY - drag.y) > 6) {
      drag.moved = true;
      fab.style.left = event.clientX - 24 + "px";
      fab.style.top = event.clientY - 24 + "px";
      fab.style.right = fab.style.bottom = "auto";
    }
  });
  fab.addEventListener("pointerup", (event) => {
    if (!drag) return;
    const moved = drag.moved;
    drag = null;
    fab.style.left = fab.style.top = fab.style.right = fab.style.bottom = "";
    if (moved) {
      corner = (event.clientY < innerHeight / 2 ? "t" : "b") + (event.clientX < innerWidth / 2 ? "l" : "r");
      store.set("localStorage", "corner", corner);
      placeButton();
    } else {
      toggle(!panel.classList.contains("open"));
    }
  });
  fab.addEventListener("keydown", (event) => {
    if (event.key === "Enter" || event.key === " ") { event.preventDefault(); toggle(true); }
  });

  function toggle(open, quietly) {
    panel.classList.toggle("open", open);
    fab.style.display = open ? "none" : "";
    store.set("localStorage", "open", open);
    announce();
    if (open) {
      // The team answered feedback: that's what the panel opens on.
      if (answersUnseen()) { state.tab = "feedback"; store.set("sessionStorage", "tab", "feedback"); }
      fab.classList.remove("unread");
      toast.classList.remove("show");
      render();
      if (feedbackTab()) loadFeedback();
      if (!quietly) input.focus();
    } else {
      menu.classList.remove("open");
      fab.focus();
    }
  }
  $(".close").addEventListener("click", () => toggle(false));
  panel.addEventListener("keydown", (event) => {
    if (event.key === "Escape") { menu.classList.contains("open") ? menu.classList.remove("open") : toggle(false); }
  });
  $(".more").addEventListener("click", () => menu.classList.toggle("open"));
  $(".fresh").addEventListener("click", () => newConversation());
  menu.addEventListener("click", async (event) => {
    const action = event.target.closest("button")?.dataset.action;
    menu.classList.remove("open");
    if (action === "docs") openDocs();
    if (action === "new") {
      newConversation();
    } else if (action === "hide") {
      toggle(false);
      fab.style.display = "none";
      announce();
    }
  });
  notice.addEventListener("click", (event) => { if (event.target.dataset.action === "reload") location.reload(); });

  // newConversation starts a new page of questions. The header's button,
  // the menu, and /clear in the box do it.
  function newConversation() {
    state.help = [];
    store.set("sessionStorage", "help", null);
    state.sent = null;
    render();
    input.focus();
  }

  // ---------------------------------------------------------------------
  // talking to the team's service

  async function call(path, options = {}) {
    const response = await fetch(API + path, {
      method: options.method || "GET",
      headers: options.body ? { "Content-Type": "application/json" } : {},
      body: options.body ? JSON.stringify(options.body) : undefined,
      credentials: "same-origin",
      cache: "no-store",
    });
    if (!response.ok) {
      const failure = await response.json().catch(() => ({}));
      throw new Error(failure.error || response.statusText || "the helper isn't answering");
    }
    return response;
  }

  // base64 is a file's bytes as JSON carries them.
  function base64(blob) {
    return new Promise((resolve, reject) => {
      const reader = new FileReader();
      reader.onload = () => resolve(String(reader.result).replace(/^data:[^,]*,/, ""));
      reader.onerror = () => reject(reader.error);
      reader.readAsDataURL(blob);
    });
  }

  // readEvents reads a stream of server-sent events, calling on(name,
  // payload) for each, until it ends.
  async function readEvents(response, on) {
    const reader = response.body.getReader();
    const decoder = new TextDecoder();
    let buffer = "";
    for (;;) {
      const { value, done } = await reader.read();
      if (done) return;
      buffer += decoder.decode(value, { stream: true });
      let end;
      while ((end = buffer.indexOf("\n\n")) >= 0) {
        const chunk = buffer.slice(0, end);
        buffer = buffer.slice(end + 2);
        let name = "message", data = "";
        for (const line of chunk.split("\n")) {
          if (line.startsWith("event:")) name = line.slice(6).trim();
          else if (line.startsWith("data:")) data += line.slice(5).trim();
        }
        if (data) on(name, JSON.parse(data));
      }
    }
  }

  const page = () => ({ path: location.pathname + location.search + location.hash, title: document.title, width: innerWidth, height: innerHeight });

  // ask asks the help how the app works: hi answers from the app's guide,
  // with the page's text and what was pointed at.
  async function ask(text, items) {
    state.sending = true;
    const history = state.help.filter((turn) => !turn.failed).slice(-12).map((turn) => ({ role: turn.from === "person" ? "user" : "assistant", content: turn.text }));
    const question = { from: "person", text, refs: items.map((item) => item.ref), shots: items.map((item) => item.image) };
    const answer = { from: "lead", name: "Help", text: "", streaming: true };
    state.help.push(question);
    render({ typing: true });
    try {
      const response = await call("help", {
        method: "POST",
        body: { question: text, history, page: page(), text: tidy(document.body.innerText, 8000), refs: question.refs },
      });
      await readEvents(response, (name, payload) => {
        if (name === "delta") {
          if (!state.help.includes(answer)) state.help.push(answer);
          answer.text += payload.text;
          scheduleRender();
        } else if (name === "done") {
          answer.text = payload.text;
        } else if (name === "error") {
          throw new Error(payload.error);
        }
      });
      if (!answer.text) throw new Error("The help didn't answer; try again.");
      delete answer.streaming;
      if (!state.help.includes(answer)) state.help.push(answer);
    } catch (error) {
      state.help = state.help.filter((turn) => turn !== question && turn !== answer);
      input.value = text;
      state.refs = items;
      saveRefs();
      drawChips();
      state.sending = false;
      render();
      showError(error);
      return;
    }
    state.sending = false;
    store.set("sessionStorage", "help", state.help.slice(-24).map((turn) => ({ from: turn.from, name: turn.name, text: turn.text, refs: turn.refs })));
    render();
  }

  // sendFeedback sends feedback to the team's channel, with a picture of
  // the page when the box is ticked.
  async function sendFeedback(text, items, files) {
    state.sending = true;
    render();
    try {
      const uploads = [];
      if (screenBox.checked) {
        const shot = await captureScreen().catch(() => null);
        if (shot) uploads.push({ name: "screen", screen: true, data: shot.replace(/^data:[^,]*,/, "") });
      }
      await Promise.race([Promise.all(items.map((item) => item.shot)), new Promise((resolve) => setTimeout(resolve, 4000))]);
      items.forEach((item, i) => { if (item.image) uploads.push({ ref: i + 1, data: item.image.replace(/^data:[^,]*,/, "") }); });
      for (const item of files) uploads.push({ name: item.file.name, data: await base64(item.file) });
      const body = { name: nameInput.value.trim(), text, page: page(), refs: items.map((item) => item.ref), files: uploads };
      if (reporting()) {
        const response = await call("report", { method: "POST", body });
        state.sent = "#" + (await response.json()).number;
      } else {
        const response = await call("feedback", { method: "POST", body });
        state.sent = (await response.json()).id;
      }
      for (const item of files) URL.revokeObjectURL(item.url);
      if (!reporting()) loadFeedback();
    } catch (error) {
      input.value = text;
      state.refs = items;
      state.files = files;
      saveRefs();
      drawChips();
      drawFiles();
      state.sending = false;
      render();
      showError(error);
      return;
    }
    state.sending = false;
    render();
  }

  let renderQueued = false;
  function scheduleRender() {
    if (renderQueued) return;
    renderQueued = true;
    requestAnimationFrame(() => {
      renderQueued = false;
      // While the answer streams in, only it is drawn again.
      const node = body.querySelector(".msg.streaming");
      const streaming = state.help.find((message) => message.streaming);
      if (node && streaming) {
        const atBottom = body.scrollHeight - body.scrollTop - body.clientHeight < 40;
        node.innerHTML = '<div class="who"></div>' + markdown(streaming.text || "");
        node.querySelector(".who").textContent = streaming.name;
        if (atBottom) body.scrollTop = body.scrollHeight;
      } else {
        render({ typing: false });
      }
    });
  }

  function showError(error) {
    const node = document.createElement("div");
    node.className = "msg error";
    node.textContent = String(error.message || error);
    body.appendChild(node);
    body.scrollTop = body.scrollHeight;
  }

  // ---------------------------------------------------------------------
  // drawing the panel

  function render(options = {}) {
    const status = state.status || {};
    if (state.tab !== "ask" && state.tab !== "feedback") state.tab = "ask";
    $(".name").textContent = "Help · " + (status.team || "the app");
    $(".version").textContent = status.version || "";
    fab.title = "Help";
    fab.setAttribute("aria-label", "Help");
    panel.setAttribute("aria-label", "Help");
    tabs.hidden = false;
    // The docs: a button beside Ask and Feedback, and in the menu.
    tabs.querySelector(".docs").hidden = !status.docs;
    $("[data-action=docs]").hidden = !status.docs;
    const feedback = feedbackTab();
    for (const button of tabs.querySelectorAll("button")) {
      button.classList.toggle("on", button.dataset.tab === state.tab);
      button.setAttribute("aria-selected", button.dataset.tab === state.tab);
    }
    tabs.querySelector('[data-tab="feedback"]').textContent = reporting() ? "Report" : "Feedback";
    extra.hidden = !feedback || reporting();
    attachLabel.style.display = feedback ? "" : "none";
    $("[data-action=new]").style.display = !feedback && state.help.length ? "" : "none";
    $(".fresh").hidden = !(!feedback && state.help.length);
    input.placeholder = feedback ? (reporting() ? "What's wrong? The first line is its title." : "What's wrong, or what would make it better?") : "Ask how something works…";
    sendButton.textContent = feedback ? "Send" : "Ask";
    sendButton.disabled = state.sending;
    const atBottom = body.scrollHeight - body.scrollTop - body.clientHeight < 40;
    body.className = "body";
    body.innerHTML = "";
    clearInterval(state.ticking);
    if (feedback) renderFeedback();
    else renderHelp();
    if (atBottom || options.typing !== undefined) body.scrollTop = body.scrollHeight;
    announce();
  }

  // drawMessages draws a conversation: the person's messages and the
  // lead's, or the help's.
  function drawMessages(messages, leadName) {
    for (const message of messages) {
      const node = document.createElement("div");
      node.className = "msg " + (message.from === "lead" ? "lead" : "person") + (message.streaming ? " streaming" : "");
      if (message.from === "lead") {
        node.innerHTML = '<div class="who"></div>' + markdown(message.text || "");
        node.querySelector(".who").textContent = message.name || leadName;
      } else {
        node.textContent = message.text;
        if (message.refs && message.refs.length) node.appendChild(refChips(message.refs, false, (message.shots || []).slice()));
      }
      body.appendChild(node);
    }
  }

  // working shows a spinner, and how long it has been, until the first
  // words come.
  function working(since, words) {
    const typing = document.createElement("div");
    typing.className = "typing";
    body.appendChild(typing);
    const tick = () => {
      const seconds = Math.max(0, Math.round((Date.now() - since) / 1000));
      typing.textContent = words(seconds);
    };
    tick();
    state.ticking = setInterval(tick, 1000);
  }

  function renderHelp() {
    if (!state.help.length) {
      body.insertAdjacentHTML("beforeend", '<div class="intro"><p>Ask how something in the app works. Point at what you mean with ◎ (Alt+S).</p>' +
        (reporting()
          ? "<p>Something wrong? Report it: it becomes a task for the people who build the app, with your name on it.</p></div>"
          : "<p>Something wrong, or an idea? Tell the team under Feedback: it reaches their Slack channel.</p></div>"));
      return;
    }
    drawMessages(state.help, "Help");
    const last = state.help[state.help.length - 1];
    if (state.sending && last.from === "person") {
      const since = last.sentAt = last.sentAt || Date.now();
      working(since, () => "Looking it up…");
    } else if (!state.sending && last.from === "lead") {
      const more = document.createElement("button");
      more.type = "button";
      more.className = "link-button";
      more.textContent = reporting() ? "Report this as a bug" : "Tell the team about this as feedback";
      more.addEventListener("click", () => {
        const asked = [...state.help].reverse().find((turn) => turn.from === "person");
        if (asked && !input.value.trim()) {
          input.value = asked.text;
          store.set("sessionStorage", "draft", input.value);
        }
        setTab("feedback");
        grow();
      });
      body.appendChild(more);
    }
  }

  function renderFeedback() {
    if (state.sending) {
      working(Date.now(), () => reporting() ? "Filing it as a task…" : "Sending to the team…");
      return;
    }
    if (reporting()) {
      renderReport();
      return;
    }
    if (state.sent) {
      const sent = document.createElement("div");
      sent.className = "sent";
      sent.innerHTML = "<strong></strong><span>The team sees it in their Slack channel. Their answers show up here, in this browser.</span>";
      sent.querySelector("strong").textContent = "Sent to the team as " + state.sent + ". Thank you!";
      const again = document.createElement("button");
      again.type = "button";
      again.className = "link-button center";
      again.textContent = "Send more";
      again.addEventListener("click", () => { state.sent = null; render(); input.focus(); });
      sent.appendChild(again);
      body.appendChild(sent);
      drawFeedback();
      return;
    }
    body.insertAdjacentHTML("beforeend", '<div class="intro"><p>Tell the team what\'s wrong, or what would make the app better.</p>' +
      "<p>Point at parts of the page with ◎ (Alt+S), and attach files. It goes to the team's Slack channel as it is; nothing changes until they decide. Their answers show up here.</p></div>");
    drawFeedback();
  }

  // renderReport is Report, for someone signed in: what it does, or the
  // number of the task just filed. The helper keeps no list of reports:
  // the people who fix one say so in the team's channel.
  function renderReport() {
    if (state.sent) {
      const sent = document.createElement("div");
      sent.className = "sent";
      sent.innerHTML = "<strong></strong><span>The people who build the app see it as a task, and the team's Slack channel hears of it.</span>";
      sent.querySelector("strong").textContent = "Filed as " + state.sent + ". Thank you!";
      const again = document.createElement("button");
      again.type = "button";
      again.className = "link-button center";
      again.textContent = "Report something else";
      again.addEventListener("click", () => { state.sent = null; render(); input.focus(); });
      sent.appendChild(again);
      body.appendChild(sent);
      return;
    }
    const intro = document.createElement("div");
    intro.className = "intro";
    intro.innerHTML = "<p>Report what's wrong, as <strong></strong>. It becomes a task for the people who build the app, with your name, this page, and the version you're on.</p>" +
      "<p>Say what happened and what you expected; the first line is the task's title. Point at parts of the page with ◎ (Alt+S), and attach screenshots or files.</p>";
    intro.querySelector("strong").textContent = state.status.you || "you";
    body.appendChild(intro);
  }

  // drawFeedback draws what this browser sent, oldest first, each with
  // the team's answers under it.
  function drawFeedback() {
    const sent = (state.feedback || []).slice().reverse();
    for (const item of sent) {
      if (item.id === state.sent) continue;
      const node = document.createElement("div");
      node.className = "msg person";
      node.innerHTML = '<div class="when"></div><div class="text"></div>';
      node.querySelector(".when").textContent = item.id + " · " + when(item.at);
      node.querySelector(".text").textContent = item.text;
      body.appendChild(node);
      for (const answer of item.answers || []) {
        const reply = document.createElement("div");
        reply.className = "msg lead answer";
        reply.innerHTML = '<div class="who"></div><div class="text"></div>';
        reply.querySelector(".who").textContent = (answer.name || "The team") + " · " + when(answer.at);
        reply.querySelector(".text").textContent = answer.text;
        body.appendChild(reply);
      }
    }
    if (panel.classList.contains("open") && state.status) store.set("localStorage", "answers-seen", state.status.answers || 0);
  }

  function when(at) {
    const date = new Date(at);
    if (isNaN(date)) return "";
    const today = new Date().toDateString() === date.toDateString();
    return today ? date.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" }) : date.toLocaleDateString([], { month: "short", day: "numeric" });
  }

  // loadFeedback gets what this browser sent and the team's answers: the
  // hi_visitor cookie says which, and only /__hi/ gets it.
  async function loadFeedback() {
    try {
      const response = await call("feedback");
      state.feedback = (await response.json()).feedback || [];
    } catch { return; }
    if (!state.sending && state.tab === "feedback") render();
  }

  // answersUnseen says the team answered feedback since the panel last
  // showed it.
  function answersUnseen() {
    return (state.status?.answers || 0) > (store.get("localStorage", "answers-seen") || 0);
  }

  function noticeAnswers() {
    if (!answersUnseen()) return;
    if (panel.classList.contains("open")) {
      if (state.tab === "feedback") loadFeedback();
      return;
    }
    fab.classList.add("unread");
    showToast({ name: "The team", text: "answered your feedback." });
  }

  // ---------------------------------------------------------------------
  // files on messages

  const pictureType = /^image\/(png|jpeg|gif|webp)$/;

  // shrink makes a large picture one the lead's model takes: at most 2400
  // pixels across and 4 MB.
  async function shrink(file) {
    const bitmap = await createImageBitmap(file);
    const longest = Math.max(bitmap.width, bitmap.height);
    if (longest <= 2400 && file.size <= 4 * 1024 * 1024) { bitmap.close(); return file; }
    const scale = Math.min(1, 2400 / longest);
    const canvas = document.createElement("canvas");
    canvas.width = Math.round(bitmap.width * scale);
    canvas.height = Math.round(bitmap.height * scale);
    canvas.getContext("2d").drawImage(bitmap, 0, 0, canvas.width, canvas.height);
    bitmap.close();
    const encode = (type, quality) => new Promise((resolve) => canvas.toBlob(resolve, type, quality));
    let blob = await encode(file.type === "image/png" ? "image/png" : "image/jpeg", 0.88);
    if (blob && blob.size > 4 * 1024 * 1024) blob = await encode("image/jpeg", 0.85);
    if (!blob) return file;
    const name = blob.type === file.type ? file.name : file.name.replace(/\.\w+$/, "") + ".jpg";
    return new File([blob], name, { type: blob.type });
  }

  async function addFiles(list) {
    if (state.tab !== "feedback") setTab("feedback"); // the help takes no files
    for (const original of Array.from(list || [])) {
      if (state.files.length >= 6) { showError(new Error("Attach at most 6 files to a message.")); break; }
      let file = original;
      if (/^image\/(png|jpeg|webp)$/.test(file.type)) {
        try { file = await shrink(file); } catch { /* sent as it is */ }
      }
      if (file.size > 20 * 1024 * 1024) { showError(new Error(file.name + " is larger than 20 MB.")); continue; }
      if (!file.size) continue;
      state.files.push({ file, url: URL.createObjectURL(file) });
    }
    drawFiles();
  }

  function drawFiles() {
    pending.replaceChildren(...state.files.map((item) => {
      const box = document.createElement("span");
      box.className = "item";
      if (pictureType.test(item.file.type)) {
        const image = new Image();
        image.src = item.url;
        image.alt = image.title = item.file.name;
        box.appendChild(image);
      } else {
        const name = document.createElement("span");
        name.className = "file";
        name.innerHTML = ICONS.clip;
        name.appendChild(document.createTextNode(item.file.name));
        box.appendChild(name);
      }
      const remove = document.createElement("button");
      remove.type = "button";
      remove.setAttribute("aria-label", "Remove " + item.file.name);
      remove.textContent = "×";
      remove.addEventListener("click", () => {
        state.files.splice(state.files.indexOf(item), 1);
        URL.revokeObjectURL(item.url);
        drawFiles();
      });
      box.appendChild(remove);
      return box;
    }));
  }

  fileInput.addEventListener("change", () => { addFiles(fileInput.files).then(() => { fileInput.value = ""; }); });
  const attach = $(".attach");
  attach.tabIndex = 0;
  attach.addEventListener("keydown", (event) => { if (event.key === "Enter" || event.key === " ") { event.preventDefault(); fileInput.click(); } });
  input.addEventListener("paste", (event) => {
    const files = event.clipboardData && event.clipboardData.files;
    if (files && files.length) { event.preventDefault(); addFiles(files); }
  });
  const dragsFiles = (event) => event.dataTransfer && Array.from(event.dataTransfer.types || []).includes("Files");
  panel.addEventListener("dragover", (event) => {
    if (!dragsFiles(event)) return;
    event.preventDefault();
    panel.classList.add("dragging");
  });
  panel.addEventListener("dragleave", (event) => { if (!panel.contains(event.relatedTarget)) panel.classList.remove("dragging"); });
  panel.addEventListener("drop", (event) => {
    panel.classList.remove("dragging");
    if (!dragsFiles(event)) return;
    event.preventDefault();
    addFiles(event.dataTransfer.files);
    input.focus();
  });

  // ---------------------------------------------------------------------
  // the team's answers to feedback: with the panel closed, the button gets
  // a dot and a preview shows for a while


  let toastTimer = null;
  function showToast(message) {
    if (fab.style.display === "none") return;
    toast.querySelector(".from").textContent = message.name || state.status?.lead || "The lead";
    toast.querySelector(".text").textContent = tidy(message.text, 300) || "Sent a file";
    toast.classList.add("show");
    clearTimeout(toastTimer);
    toastTimer = setTimeout(() => toast.classList.remove("show"), 20000);
  }
  toast.addEventListener("click", () => { toast.classList.remove("show"); toggle(true); });

  // ---------------------------------------------------------------------
  // select mode: hovering outlines the element under the pointer, a click
  // adds it to the message. The app sees none of the clicks.

  const generatedClass = /^-?(?:[mp][trblxyse]?|space-[xy]|gap(?:-[xy])?|[wh]|min-[wh]|max-[wh]|size|inset|top|left|right|bottom|z|order|basis|grow|shrink|flex|grid|col|row|items|justify|content|self|place|text|font|leading|tracking|bg|from|via|to|border|rounded|ring|shadow|opacity|outline|divide|fill|stroke|object|overflow|cursor|select|transition|duration|ease|delay|animate|transform|scale|rotate|translate|skew|origin|aspect|line-clamp|truncate|underline|uppercase|lowercase|capitalize|italic|antialiased|sr-only|container|block|inline|hidden|visible|invisible|static|fixed|absolute|relative|sticky|isolate|contents|decoration|whitespace|break|align|float|clear|box|appearance|pointer-events|resize|snap|touch|will-change|backdrop|blur|brightness|contrast|grayscale|invert|saturate|sepia|drop-shadow|mix-blend)(?:-|$)/;
  // finder's own filters already skip names with digits, underscores, or
  // short words (hashed classes, generated ids); these also skip Tailwind's
  // utility classes, which say how an element looks, not which it is.
  const finderOptions = {
    className: (name) => className(name) && !generatedClass.test(name),
    timeoutMs: 400,
  };

  function selectorOf(el) {
    const rootNode = el.getRootNode();
    try {
      if (rootNode instanceof ShadowRoot) {
        return selectorOf(rootNode.host) + " >>> " + finder(el, Object.assign({ root: rootNode }, finderOptions));
      }
      return finder(el, finderOptions);
    } catch {
      // A page that changes as it is read: the path by position.
      const parts = [];
      for (let node = el; node && node !== document.documentElement; node = parentOf(node)) {
        const index = node.parentElement ? Array.prototype.indexOf.call(node.parentElement.children, node) + 1 : 1;
        parts.unshift(node.tagName.toLowerCase() + ":nth-child(" + index + ")");
      }
      return parts.join(" > ");
    }
  }

  // parentOf steps out of an open shadow root to its host.
  function parentOf(el) {
    if (el.parentElement) return el.parentElement;
    const rootNode = el.getRootNode();
    return rootNode instanceof ShadowRoot ? rootNode.host : null;
  }

  const tidy = (text, limit) => (text || "").replace(/\s+/g, " ").trim().slice(0, limit);

  function shortName(el) {
    let name = el.tagName.toLowerCase();
    const id = el.getAttribute("id");
    if (id && idName(id)) return name + "#" + id;
    const stable = Array.from(el.classList).find((name) => className(name) && !generatedClass.test(name));
    if (stable) name += "." + stable;
    const aria = el.getAttribute("aria-label");
    if (aria) name += ' "' + tidy(aria, 30) + '"';
    return name;
  }

  function textOf(el) {
    return tidy(el.innerText || el.getAttribute("aria-label") || el.getAttribute("alt") || el.getAttribute("title") || el.getAttribute("placeholder") || "", 200);
  }

  // sourceOf is where the element comes from, when the app's framework
  // leaves it on the element in development builds; null otherwise.
  function sourceOf(el) {
    const marked = el.closest && el.closest("[data-hi-source]");
    if (marked) {
      const match = /^(.*?)(?::(\d+))?(?::\d+)?$/.exec(marked.getAttribute("data-hi-source"));
      return { file: match[1], line: +match[2] || 0 };
    }
    for (let node = el; node; node = parentOf(node)) {
      try {
        const vue = node.__vueParentComponent;
        if (vue && vue.type) return { file: vue.type.__file || "", component: vue.type.__name || vue.type.name || "" };
        if (node.__vue__) return { file: node.__vue__.$options.__file || "", component: node.__vue__.$options.name || "" };
        const svelte = node.__svelte_meta && node.__svelte_meta.loc;
        if (svelte) return { file: svelte.file || "", line: (svelte.line || 0) + 1 };
        const fiberKey = Object.keys(node).find((key) => key.startsWith("__reactFiber$"));
        if (fiberKey) {
          for (let fiber = node[fiberKey]; fiber; fiber = fiber.return) {
            const type = fiber.type;
            const component = type && typeof type !== "string" && (type.displayName || type.name || (type.render && (type.render.displayName || type.render.name)));
            if (component) {
              const debug = fiber._debugSource || {};
              return { component, file: debug.fileName || "", line: debug.lineNumber || 0 };
            }
          }
          return null;
        }
      } catch { return null; }
    }
    return null;
  }

  // describe is what the lead and the coder get about an element.
  function describe(el) {
    const tag = el.tagName.toLowerCase();
    const attributes = {};
    for (const attribute of Array.from(el.attributes)) {
      if (/^(id|class|role|name|type|href|src|alt|title|placeholder|for)$|^aria-|^data-/.test(attribute.name)) attributes[attribute.name] = attribute.value.slice(0, 200);
    }
    let html = el.outerHTML;
    if (html.length > 2048) {
      const open = html.slice(0, html.indexOf(">") + 1);
      html = open.length < 1800 ? open + tidy(el.textContent, 160) + " … </" + tag + ">" : html.slice(0, 2000) + " …";
    }
    const path = [];
    for (let node = parentOf(el); node && node !== document.body && node !== document.documentElement && path.length < 8; node = parentOf(node)) path.unshift(shortName(node));
    const landmarks = {};
    let heading = null;
    for (const candidate of document.querySelectorAll("h1, h2, h3, h4, h5, h6, legend")) {
      const position = candidate.compareDocumentPosition(el);
      if (candidate === el || candidate.contains(el) || position & Node.DOCUMENT_POSITION_FOLLOWING) heading = candidate;
    }
    if (heading && heading !== el) landmarks.heading = tidy(heading.innerText, 100);
    const owner = (el.labels && el.labels[0]) || (el.closest && el.closest("label"));
    if (owner && owner !== el) landmarks.label = tidy(owner.innerText, 100);
    const region = el.closest && el.closest("[aria-label], section[id], form[name], form[id]");
    if (region && region !== el) landmarks.section = tidy(region.getAttribute("aria-label") || region.id || region.getAttribute("name"), 100);
    const rect = el.getBoundingClientRect();
    const computed = getComputedStyle(el);
    const style = {};
    for (const name of ["color", "background-color", "font-size", "font-weight", "display"]) style[name] = computed.getPropertyValue(name);
    return {
      selector: selectorOf(el), tag, text: textOf(el), attributes, html, path, landmarks,
      box: { x: Math.round(rect.left), y: Math.round(rect.top), width: Math.round(rect.width), height: Math.round(rect.height), viewport: [innerWidth, innerHeight] },
      style, source: sourceOf(el),
    };
  }

  function chipText(ref, n) {
    const text = tidy(ref.text, 28);
    return "#" + n + " " + ref.tag + (text ? ' "' + text + (ref.text.length > 28 ? "…" : "") + '"' : "");
  }

  // findRef is a reference's element on this page, if it is still there.
  function findRef(item) {
    if (item.el && item.el.isConnected) return item.el;
    try {
      if (!item.ref.selector.includes(">>>")) return document.querySelector(item.ref.selector);
    } catch { /* a selector this page can't read */ }
    return null;
  }

  const select = { on: false, hover: null, children: [], touch: false };
  let peek = null; // an element to outline while a chip is hovered

  function place(box, el) {
    const rect = el.getBoundingClientRect();
    box.style.display = "block";
    box.style.left = rect.left - 2 + "px";
    box.style.top = rect.top - 2 + "px";
    box.style.width = rect.width + 4 + "px";
    box.style.height = rect.height + 4 + "px";
    return rect;
  }

  function draw() {
    const outlined = select.on ? select.hover : peek;
    if (outlined && outlined.isConnected) {
      const rect = place(hoverBox, outlined);
      if (select.on) {
        label.textContent = shortName(outlined) + " · " + Math.round(rect.width) + "×" + Math.round(rect.height);
        label.style.display = "block";
        const above = rect.top - 26;
        label.style.top = (above > 4 ? above : Math.min(rect.bottom + 6, innerHeight - 30)) + "px";
        label.style.left = Math.max(4, Math.min(rect.left, innerWidth - label.offsetWidth - 4)) + "px";
      } else {
        label.style.display = "none";
      }
    } else {
      hoverBox.style.display = "none";
      label.style.display = "none";
    }
    banner.classList.toggle("outlined", !!(select.on && select.hover));
    // The elements already chosen stay outlined, numbered, while choosing.
    const shown = select.on ? state.refs : [];
    while (frozenBoxes.children.length < shown.length) frozenBoxes.insertAdjacentHTML("beforeend", '<div class="box frozen"><span class="n"></span></div>');
    Array.from(frozenBoxes.children).forEach((box, i) => {
      const el = i < shown.length && findRef(shown[i]);
      if (!el) { box.style.display = "none"; return; }
      place(box, el);
      box.querySelector(".n").textContent = i + 1;
    });
  }

  let drawing = false;
  function keepDrawing() {
    if (drawing) return;
    drawing = true;
    const frame = () => {
      draw();
      if (select.on || peek) requestAnimationFrame(frame);
      else drawing = false;
    };
    requestAnimationFrame(frame);
  }

  const ours = (event) => event.composedPath().includes(host);
  const targetOf = (event) => event.composedPath().find((node) => node instanceof Element) || null;
  const usable = (el) => el && el !== document.documentElement && el !== document.body;

  function onMove(event) {
    if (ours(event) || event.pointerType === "touch") return;
    const el = targetOf(event);
    if (usable(el) && el !== select.hover && !(select.hover && select.children.length && select.hover.contains(el))) {
      select.hover = el;
      select.children = [];
    }
  }

  function onBlock(event) {
    if (ours(event)) return;
    event.preventDefault();
    event.stopImmediatePropagation();
    if (event.type !== "click") return;
    const el = targetOf(event);
    // After Alt+↑, a click inside the outlined element means the outlined one.
    const chosen = select.hover && select.children.length && select.hover.contains(el) ? select.hover : el;
    if (!usable(chosen)) return;
    if (event.pointerType === "touch" || (event.pointerType === undefined && select.touch)) {
      // A tap outlines; ✓ on the label takes it.
      select.touch = true;
      select.hover = chosen;
      select.children = [];
      return;
    }
    take(chosen, event.shiftKey);
  }

  function onKey(event) {
    if (event.key === "Escape") {
      event.preventDefault();
      event.stopImmediatePropagation();
      stopSelect();
    } else if (event.altKey && event.key === "ArrowUp" && select.hover) {
      event.preventDefault();
      event.stopImmediatePropagation();
      const parent = parentOf(select.hover);
      if (usable(parent)) { select.children.push(select.hover); select.hover = parent; }
    } else if (event.altKey && event.key === "ArrowDown" && select.children.length) {
      event.preventDefault();
      event.stopImmediatePropagation();
      select.hover = select.children.pop();
    } else if (event.key === "Enter" && select.hover && !ours(event)) {
      event.preventDefault();
      event.stopImmediatePropagation();
      take(select.hover, event.shiftKey);
    }
  }

  const blocked = ["pointerdown", "pointerup", "mousedown", "mouseup", "click", "dblclick", "auxclick", "submit", "contextmenu"];

  function startSelect() {
    if (select.on) return;
    select.on = true;
    select.hover = null;
    select.children = [];
    select.touch = matchMedia("(pointer: coarse)").matches;
    banner.classList.toggle("touch", select.touch);
    banner.querySelector(".hint").textContent = select.touch
      ? "Tap what you mean, then Use this · ↑ the element around it"
      : "Click what you mean · Alt+↑ the element around it · Shift-click for more · Esc";
    layer.classList.add("selecting");
    // The panel steps aside when it would leave little of the page to point
    // at; otherwise the banner sits over the part of the page it leaves.
    const open = panel.classList.contains("open");
    const room = innerWidth - (open ? panel.offsetWidth : 0);
    if (open && (room < 480 || select.touch)) panel.classList.add("away");
    const left = panel.classList.contains("left") && open && !panel.classList.contains("away") ? panel.offsetWidth : 0;
    banner.style.left = (panel.classList.contains("away") || !open ? innerWidth / 2 : left + room / 2) + "px";
    pick.classList.add("on");
    addEventListener("pointermove", onMove, true);
    for (const type of blocked) addEventListener(type, onBlock, true);
    addEventListener("keydown", onKey, true);
    keepDrawing();
  }

  function stopSelect() {
    if (!select.on) return;
    select.on = false;
    select.hover = null;
    layer.classList.remove("selecting");
    panel.classList.remove("away");
    pick.classList.remove("on");
    removeEventListener("pointermove", onMove, true);
    for (const type of blocked) removeEventListener(type, onBlock, true);
    removeEventListener("keydown", onKey, true);
    draw();
  }

  function take(el, more) {
    if (state.refs.some((item) => item.el === el)) {
      if (!more) stopSelect();
      return;
    }
    if (state.refs.length >= 10) { stopSelect(); return; }
    const item = { ref: describe(el), el, image: null };
    item.shot = capture(el).then((image) => { item.image = image; drawChips(); }).catch(() => { /* the words still say which */ });
    state.refs.push(item);
    saveRefs();
    drawChips();
    if (!more) {
      stopSelect();
      if (!panel.classList.contains("open")) toggle(true);
      input.focus();
    }
  }

  const step = (key) => () => onKey({ altKey: true, key, preventDefault() {}, stopImmediatePropagation() {} });
  banner.querySelector(".up").addEventListener("click", step("ArrowUp"));
  banner.querySelector(".down").addEventListener("click", step("ArrowDown"));
  banner.querySelector(".ok").addEventListener("click", () => { if (select.hover) take(select.hover, false); });
  banner.querySelector(".done").addEventListener("click", stopSelect);
  pick.addEventListener("click", () => (select.on ? stopSelect() : startSelect()));
  // Alt+S starts and stops select mode from anywhere on the page, the panel
  // open or not: people click on the page before they think of it.
  addEventListener("keydown", (event) => {
    if (!event.altKey || event.ctrlKey || event.metaKey || event.code !== "KeyS" || !host.isConnected) return;
    event.preventDefault();
    event.stopImmediatePropagation();
    if (select.on) stopSelect();
    else startSelect();
  }, true);

  // A picture of each element pointed at goes with it, drawn by
  // modern-screenshot (third_party), which loads the first time.
  let snap = null;
  function backgroundOf(el) {
    for (let node = el; node; node = parentOf(node)) {
      const color = getComputedStyle(node).backgroundColor;
      if (color && color !== "transparent" && !/^rgba\(.*,\s*0\)$/.test(color)) return color;
    }
    return "#ffffff";
  }

  function loadSnap() {
    snap = snap || import(new URL("/__hi/snap.js", location.href).href).catch((error) => { snap = null; throw error; });
    return snap;
  }

  // captureScreen is a picture of the page as the person sees it, without
  // the helper, for feedback.
  async function captureScreen() {
    const { domToCanvas } = await loadSnap();
    const scale = Math.min(window.devicePixelRatio || 1, 1600 / Math.max(innerWidth, innerHeight));
    const page = await domToCanvas(document.documentElement, {
      scale, backgroundColor: backgroundOf(document.body), timeout: 5000, filter: (node) => node !== host,
      width: document.documentElement.scrollWidth, height: Math.min(document.documentElement.scrollHeight, scrollY + innerHeight + 1),
    });
    const canvas = document.createElement("canvas");
    canvas.width = Math.round(innerWidth * scale);
    canvas.height = Math.round(innerHeight * scale);
    canvas.getContext("2d").drawImage(page, Math.round(scrollX * scale), Math.round(scrollY * scale), canvas.width, canvas.height, 0, 0, canvas.width, canvas.height);
    let image = canvas.toDataURL("image/png");
    if (image.length > 4 * 1024 * 1024) image = canvas.toDataURL("image/jpeg", 0.85);
    return image;
  }

  async function capture(el) {
    const { domToCanvas } = await loadSnap();
    // A small element is shown with what is around it, itself outlined.
    let target = el, rect = el.getBoundingClientRect();
    for (let i = 0; i < 3 && (rect.width < 160 || rect.height < 48); i++) {
      const parent = parentOf(target);
      if (!usable(parent)) break;
      const around = parent.getBoundingClientRect();
      if (around.width * around.height > innerWidth * innerHeight * 0.6) break;
      target = parent;
      rect = around;
    }
    if (!rect.width || !rect.height) return null;
    const scale = Math.min(window.devicePixelRatio || 1, 2, 1600 / Math.max(rect.width, rect.height));
    const canvas = await domToCanvas(target, { scale, backgroundColor: backgroundOf(target), timeout: 3000, filter: (node) => node !== host });
    if (target !== el) {
      const inner = el.getBoundingClientRect();
      const k = canvas.width / rect.width;
      const context = canvas.getContext("2d");
      context.setTransform(1, 0, 0, 1, 0, 0);
      context.strokeStyle = "#e5484d";
      context.lineWidth = Math.max(2, 2 * k);
      context.strokeRect((inner.left - rect.left) * k - context.lineWidth, (inner.top - rect.top) * k - context.lineWidth,
        inner.width * k + 2 * context.lineWidth, inner.height * k + 2 * context.lineWidth);
    }
    let image = canvas.toDataURL("image/png");
    if (image.length > 4 * 1024 * 1024) image = canvas.toDataURL("image/jpeg", 0.85);
    return image;
  }

  // refChips draws references as chips: removable in the composer; in a
  // sent message, hovering one outlines its element if it is on this page.
  // pictures are the references' pictures, by position.
  function refChips(refs, removable, pictures = []) {
    const row = document.createElement("div");
    row.className = "chips";
    refs.forEach((item, i) => {
      const ref = item.ref || item;
      const chip = document.createElement("span");
      chip.className = "chip";
      chip.title = ref.selector;
      const picture = item.image || pictures[i];
      if (picture) {
        const image = new Image();
        image.src = picture;
        image.alt = "";
        image.addEventListener("click", () => { if (!picture.startsWith("data:")) open(picture, "_blank", "noopener"); });
        chip.appendChild(image);
      }
      chip.appendChild(document.createTextNode(chipText(ref, i + 1)));
      chip.addEventListener("pointerenter", () => { peek = findRef(item.ref ? item : { ref }); keepDrawing(); });
      chip.addEventListener("pointerleave", () => { peek = null; });
      if (removable) {
        const remove = document.createElement("button");
        remove.type = "button";
        remove.setAttribute("aria-label", "Remove");
        remove.textContent = "×";
        remove.addEventListener("click", () => {
          state.refs.splice(state.refs.indexOf(item), 1);
          peek = null;
          saveRefs();
          drawChips();
        });
        chip.appendChild(remove);
      }
      row.appendChild(chip);
    });
    return row;
  }

  function drawChips() {
    chips.replaceChildren(...refChips(state.refs, true).childNodes);
  }

  // An unsent message's references survive a reload, with the draft.
  function saveRefs() {
    store.set("sessionStorage", "refs", state.refs.length ? state.refs.map((item) => item.ref) : null);
  }

  function restoreRefs(refs) {
    state.refs = (refs || []).map((ref) => ({ ref, el: null }));
    saveRefs();
    drawChips();
  }

  // ---------------------------------------------------------------------
  // the composer: Enter sends, Shift+Enter is a new line; an unsent
  // message survives a reload

  input.value = store.get("sessionStorage", "draft") || "";
  restoreRefs(store.get("sessionStorage", "refs"));
  const grow = () => { input.style.height = "auto"; input.style.height = Math.min(input.scrollHeight, 160) + "px"; };
  input.addEventListener("input", () => { grow(); store.set("sessionStorage", "draft", input.value || null); });
  input.addEventListener("keydown", (event) => {
    if (event.key === "Enter" && !event.shiftKey && !event.isComposing) { event.preventDefault(); composer.requestSubmit(); }
  });
  composer.addEventListener("submit", (event) => {
    event.preventDefault();
    const text = input.value.trim();
    if (!text || state.sending) return;
    // /clear (or /new) starts a new page of questions instead of asking it.
    if (!feedbackTab() && /^\/(clear|new)$/i.test(text)) {
      input.value = "";
      store.set("sessionStorage", "draft", null);
      grow();
      newConversation();
      return;
    }
    // Feedback says who sent it: the name box flashes until it's filled.
    if (feedbackTab() && !reporting() && !nameInput.value.trim()) {
      nameInput.classList.remove("needed");
      void nameInput.offsetWidth;
      nameInput.classList.add("needed");
      nameInput.focus();
      return;
    }
    state.sent = null;
    const items = state.refs, files = state.files;
    input.value = "";
    store.set("sessionStorage", "draft", null);
    state.refs = [];
    state.files = [];
    saveRefs();
    drawChips();
    drawFiles();
    grow();
    if (feedbackTab()) sendFeedback(text, items, files);
    else ask(text, items);
  });

  // ---------------------------------------------------------------------
  // start: show the button once the team's service answers, and say when
  // a new version is up

  // refreshStatus says whether anything the panel shows changed.
  async function refreshStatus() {
    const response = await call("status");
    const status = await response.json();
    const before = state.status;
    if (before && before.commit && status.commit && status.commit !== before.commit) notice.hidden = false;
    // The commit stays the one this page was loaded with.
    state.status = Object.assign({}, status, { commit: before?.commit || status.commit });
    if (before && (before.answers || 0) !== (status.answers || 0)) noticeAnswers();
    return !before || before.version !== status.version || before.lead !== status.lead;
  }

  (async () => {
    try {
      await refreshStatus();
    } catch {
      return; // the helper is off or the team is down: no button at all
    }
    document.body.appendChild(host);
    placeButton();
    // Open again after a reload, without taking the page's focus.
    if (store.get("localStorage", "open")) toggle(true, true);
    else {
      noticeAnswers();
      render();
    }
    grow();
    setInterval(() => { refreshStatus().then((changed) => { if (changed && !state.sending) render(); }).catch(() => {}); }, 60000);
  })();
})();
