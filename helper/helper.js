// The hi team helper: a chat with the team's lead beside any page of the
// team's app. hi net expose adds this script to each page it relays and
// serves /__hi/ from the team's service, so the app holds none of it
// (docs/specs/approved/hi_team_helper.md). Plain JavaScript, no framework,
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
  --accent: #2f5bea; --accent-fg: #ffffff; --mine: #e9eefe; --warn: #fff4d6; --shadow: 0 12px 40px rgba(0,0,0,.18);
  font: 14px/1.45 system-ui, -apple-system, "Segoe UI", Roboto, sans-serif; color: var(--fg); }
@media (prefers-color-scheme: dark) {
  :host { --bg: #1c1c1f; --fg: #f2f2f5; --muted: #a1a1a8; --line: #34343a; --soft: #26262b;
    --accent: #6d8cff; --accent-fg: #0d0d10; --mine: #26304f; --warn: #3a3220; --shadow: 0 12px 40px rgba(0,0,0,.5); }
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
.panel { position: fixed; top: 0; bottom: 0; width: min(400px, 100vw); background: var(--bg);
  border-left: 1px solid var(--line); box-shadow: var(--shadow); display: none; flex-direction: column; }
.panel.right { right: 0; } .panel.left { left: 0; border-left: 0; border-right: 1px solid var(--line); }
.panel.open { display: flex; }
@media (max-width: 640px) {
  .panel, .panel.left, .panel.right { top: auto; left: 0; right: 0; width: 100vw; height: 85vh;
    border: 0; border-top: 1px solid var(--line); border-radius: 14px 14px 0 0; }
}
header { display: flex; align-items: center; gap: 8px; padding: 12px 12px 12px 16px; border-bottom: 1px solid var(--line); }
header .title { flex: 1; min-width: 0; }
header .name { font-weight: 600; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
header .version { color: var(--muted); font-size: 12px; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.icon { border: 0; background: none; width: 32px; height: 32px; border-radius: 8px; display: grid; place-items: center; color: var(--muted); }
.icon:hover { background: var(--soft); color: var(--fg); }
.icon svg { width: 18px; height: 18px; }
.menu { position: absolute; top: 52px; right: 12px; background: var(--bg); border: 1px solid var(--line);
  border-radius: 10px; box-shadow: var(--shadow); padding: 4px; display: none; min-width: 200px; }
.menu.open { display: block; }
.menu button { display: block; width: 100%; text-align: left; border: 0; background: none; padding: 8px 10px; border-radius: 6px; }
.menu button:hover { background: var(--soft); }
.notice { margin: 8px 12px 0; padding: 8px 10px; border-radius: 8px; background: var(--warn); font-size: 13px;
  display: flex; gap: 8px; align-items: center; }
.notice[hidden] { display: none; }
.notice button { margin-left: auto; border: 1px solid var(--line); background: var(--bg); border-radius: 6px; padding: 2px 8px; }
.body { flex: 1; overflow-y: auto; padding: 12px 16px; display: flex; flex-direction: column; gap: 10px; }
.empty { color: var(--muted); margin: auto 8px; text-align: center; }
.msg { max-width: 92%; padding: 8px 12px; border-radius: 12px; overflow-wrap: anywhere; }
.msg.person { align-self: flex-end; background: var(--mine); border-bottom-right-radius: 4px; white-space: pre-wrap; }
.msg.lead { align-self: flex-start; background: var(--soft); border-bottom-left-radius: 4px; }
.msg.error { align-self: stretch; background: var(--warn); font-size: 13px; }
.msg .who { font-size: 11px; color: var(--muted); margin-bottom: 2px; }
.msg p { margin: 0 0 6px; } .msg p:last-child { margin-bottom: 0; }
.msg ul, .msg ol { margin: 0 0 6px; padding-left: 20px; }
.msg code { font: 12px/1.4 ui-monospace, SFMono-Regular, Menlo, monospace; background: var(--line); padding: 1px 4px; border-radius: 4px; }
.msg pre { margin: 0 0 6px; padding: 8px; background: var(--line); border-radius: 6px; overflow-x: auto; }
.msg pre code { background: none; padding: 0; }
.msg a { color: var(--accent); }
.typing { color: var(--muted); font-size: 13px; }
.typing::after { content: "…"; animation: blink 1.2s infinite; }
@keyframes blink { 50% { opacity: .2; } }
.composer { border-top: 1px solid var(--line); padding: 10px 12px; display: flex; gap: 8px; align-items: flex-end; }
textarea { flex: 1; resize: none; font: inherit; color: var(--fg); background: var(--soft); border: 1px solid var(--line);
  border-radius: 10px; padding: 8px 10px; max-height: 160px; min-height: 38px; outline: none; }
textarea:focus { border-color: var(--accent); }
.send { border: 0; background: var(--accent); color: var(--accent-fg); border-radius: 10px; height: 38px; padding: 0 14px; font-weight: 600; }
.send:disabled { opacity: .5; cursor: default; }
.who-screen { padding: 16px; display: flex; flex-direction: column; gap: 8px; overflow-y: auto; }
.who-screen h2 { font-size: 16px; margin: 0 0 4px; }
.who-screen p { margin: 0 0 8px; color: var(--muted); }
.who-screen .person { text-align: left; border: 1px solid var(--line); background: var(--bg); border-radius: 10px; padding: 10px 12px; }
.who-screen .person:hover { border-color: var(--accent); }
.who-screen form { display: flex; gap: 8px; margin-top: 8px; }
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
.who-screen input { flex: 1; font: inherit; color: var(--fg); background: var(--soft); border: 1px solid var(--line); border-radius: 10px; padding: 8px 10px; }
`;

  const ICONS = {
    chat: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21 12a8 8 0 0 1-11.6 7.1L4 20l1-4.6A8 8 0 1 1 21 12z"/></svg>',
    close: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M6 6l12 12M18 6L6 18"/></svg>',
    target: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><circle cx="12" cy="12" r="7"/><circle cx="12" cy="12" r="2"/><path d="M12 2v3M12 19v3M2 12h3M19 12h3"/></svg>',
    more: '<svg viewBox="0 0 24 24" fill="currentColor"><circle cx="5" cy="12" r="1.8"/><circle cx="12" cy="12" r="1.8"/><circle cx="19" cy="12" r="1.8"/></svg>',
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
  root.innerHTML = `<style>${CSS}</style>
<button class="fab" part="button" aria-label="Chat with the team lead" title="Chat with the team lead">${ICONS.chat}<span class="dot"></span></button>
<section class="panel" role="dialog" aria-label="Chat with the team lead">
  <header>
    <div class="title"><div class="name"></div><div class="version"></div></div>
    <button class="icon more" aria-label="More">${ICONS.more}</button>
    <button class="icon close" aria-label="Close">${ICONS.close}</button>
    <div class="menu" role="menu">
      <button data-action="new" role="menuitem">New conversation</button>
      <button data-action="who" role="menuitem">Change who you are</button>
      <button data-action="hide" role="menuitem">Hide the button until reload</button>
    </div>
  </header>
  <div class="notice" hidden><span>A new version is up.</span><button data-action="reload">Reload</button></div>
  <div class="body" aria-live="polite"></div>
  <form class="composer">
    <div class="chips"></div>
    <div class="row">
      <button class="pick" type="button" aria-label="Point at part of the page (Alt+S)" title="Point at part of the page (Alt+S)">${ICONS.target}</button>
      <textarea rows="1" placeholder="Message the lead…" aria-label="Message"></textarea>
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

  // The app's keyboard shortcuts shouldn't fire while someone types here.
  for (const type of ["keydown", "keyup", "keypress"]) host.addEventListener(type, (event) => event.stopPropagation());

  const state = {
    status: null,
    who: store.get("localStorage", "who"), // {id, name}
    conversation: null,
    sending: false,
    poll: null,
    refs: [], // the elements pointed at for the next message: {ref, el}
  };

  // ---------------------------------------------------------------------
  // the button: a click opens the panel; a drag moves it to another corner

  let corner = store.get("localStorage", "corner") || "br";
  function placeButton() {
    fab.className = "fab " + corner + (fab.classList.contains("unread") ? " unread" : "");
    panel.classList.toggle("left", corner === "tl" || corner === "bl");
    panel.classList.toggle("right", corner === "tr" || corner === "br");
  }
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
    if (open) {
      fab.classList.remove("unread");
      render();
      if (state.who && !quietly) input.focus();
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
  menu.addEventListener("click", async (event) => {
    const action = event.target.closest("button")?.dataset.action;
    menu.classList.remove("open");
    if (action === "new") {
      await call("conversation/new", { method: "POST", body: { person: state.who?.id || "", name: state.who?.name || "" } }).catch(showError);
      state.conversation = null;
      render();
      input.focus();
    } else if (action === "who") {
      state.who = null;
      store.set("localStorage", "who", null);
      state.conversation = null;
      render();
    } else if (action === "hide") {
      toggle(false);
      fab.style.display = "none";
    }
  });
  notice.addEventListener("click", (event) => { if (event.target.dataset.action === "reload") location.reload(); });

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

  async function loadConversation() {
    if (!state.who) return;
    const query = "person=" + encodeURIComponent(state.who.id || "") + "&name=" + encodeURIComponent(state.who.name || "");
    const response = await call("conversation?" + query);
    const conversation = await response.json();
    state.conversation = conversation.id ? conversation : null;
  }

  // The lead may still be answering a message sent before a reload: look
  // again until its answer is there.
  function waitForAnswer() {
    clearTimeout(state.poll);
    const messages = state.conversation?.messages || [];
    if (state.sending || !messages.length || messages[messages.length - 1].from !== "person") return;
    state.poll = setTimeout(async () => {
      try {
        await loadConversation();
        const latest = state.conversation?.messages || [];
        if (latest.length && latest[latest.length - 1].from === "lead" && !panel.classList.contains("open")) fab.classList.add("unread");
        render();
      } catch { /* try again next time */ }
      waitForAnswer();
    }, 5000);
  }

  async function send(text, refs) {
    state.sending = true;
    const page = { path: location.pathname + location.search + location.hash, title: document.title, width: innerWidth, height: innerHeight };
    const conversation = state.conversation || { messages: [] };
    conversation.messages = conversation.messages.concat([{ from: "person", name: state.who.name, text, refs, pending: true }]);
    state.conversation = conversation;
    let answer = "";
    let lead = null;
    render({ typing: true });
    try {
      const response = await call("messages", {
        method: "POST",
        body: { conversation: conversation.id || "", person: state.who.id || "", name: state.who.name, text, page, refs },
      });
      const reader = response.body.getReader();
      const decoder = new TextDecoder();
      let buffer = "", finished = false;
      while (!finished) {
        const { value, done } = await reader.read();
        if (done) break;
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
          if (!data) continue;
          const payload = JSON.parse(data);
          if (name === "message") {
            conversation.id = payload.conversation;
            conversation.messages[conversation.messages.length - 1] = payload.message;
          } else if (name === "delta") {
            answer += payload.text;
            if (!lead) { lead = { from: "lead", name: state.status?.lead || "Lead", text: "" }; conversation.messages.push(lead); }
            lead.text = answer;
            scheduleRender();
          } else if (name === "done") {
            if (lead) conversation.messages.pop();
            conversation.messages.push(payload.message);
            finished = true;
          } else if (name === "error") {
            if (lead) conversation.messages.pop();
            throw new Error(payload.error);
          }
        }
      }
      if (!finished) {
        // The stream was cut; the lead still answers, and the next look shows it.
        if (lead) conversation.messages.pop();
      }
      state.sending = false;
      render();
      waitForAnswer();
    } catch (error) {
      state.sending = false;
      const last = conversation.messages[conversation.messages.length - 1];
      if (last && last.pending) {
        conversation.messages.pop();
        input.value = text;
        restoreRefs(refs);
      }
      render();
      showError(error);
    }
  }

  let renderQueued = false;
  function scheduleRender() {
    if (renderQueued) return;
    renderQueued = true;
    requestAnimationFrame(() => { renderQueued = false; render({ typing: false }); });
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
    $(".name").textContent = status.lead || "Team lead";
    $(".version").textContent = [status.version, state.who && "you're " + state.who.name].filter(Boolean).join(" · ");
    if (!state.who) {
      composer.style.display = "none";
      renderWho();
      return;
    }
    composer.style.display = "";
    sendButton.disabled = state.sending;
    const atBottom = body.scrollHeight - body.scrollTop - body.clientHeight < 40;
    body.className = "body";
    body.innerHTML = "";
    const messages = state.conversation?.messages || [];
    if (!messages.length) {
      const empty = document.createElement("div");
      empty.className = "empty";
      empty.textContent = "Ask the lead for a change, report a problem, or ask how something works. You're on " + (location.pathname || "/") + ".";
      body.appendChild(empty);
    }
    for (const message of messages) {
      const node = document.createElement("div");
      node.className = "msg " + (message.from === "lead" ? "lead" : "person");
      if (message.from === "lead") {
        node.innerHTML = '<div class="who"></div>' + markdown(message.text || "");
        node.querySelector(".who").textContent = message.name || status.lead || "Lead";
      } else {
        node.textContent = message.text;
        if (message.refs && message.refs.length) node.appendChild(refChips(message.refs, false));
      }
      body.appendChild(node);
    }
    const last = messages[messages.length - 1];
    if ((options.typing || (!state.sending && last && last.from === "person")) && !(last && last.from === "lead")) {
      const typing = document.createElement("div");
      typing.className = "typing";
      typing.textContent = (status.lead || "The lead") + " is answering";
      body.appendChild(typing);
    }
    if (atBottom || options.typing !== undefined) body.scrollTop = body.scrollHeight;
  }

  function renderWho() {
    const status = state.status || {};
    body.className = "body who-screen";
    body.innerHTML = "<h2>Who are you?</h2><p>The lead needs to know who's asking. This browser remembers it.</p>";
    for (const person of status.people || []) {
      const button = document.createElement("button");
      button.className = "person";
      button.textContent = person.name;
      button.addEventListener("click", () => choose({ id: person.id, name: person.name }));
      body.appendChild(button);
    }
    if (status.anyone) {
      const form = document.createElement("form");
      form.innerHTML = '<input placeholder="Or type your name" aria-label="Your name" maxlength="60"><button class="send" type="submit">Go</button>';
      form.addEventListener("submit", (event) => {
        event.preventDefault();
        const name = form.querySelector("input").value.trim();
        if (name) choose({ id: "", name });
      });
      body.appendChild(form);
    } else if (!(status.people || []).length) {
      body.insertAdjacentHTML("beforeend", "<p>The team has no people listed yet.</p>");
    }
  }

  async function choose(who) {
    state.who = who;
    store.set("localStorage", "who", who);
    state.conversation = null;
    try { await loadConversation(); } catch (error) { showError(error); }
    render();
    waitForAnswer();
    input.focus();
  }


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
    if (select.on || !state.who) return;
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
    state.refs.push({ ref: describe(el), el });
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
  panel.addEventListener("keydown", (event) => {
    if (event.altKey && event.code === "KeyS") { event.preventDefault(); select.on ? stopSelect() : startSelect(); }
  });

  // refChips draws references as chips: removable in the composer; in a
  // sent message, hovering one outlines its element if it is on this page.
  function refChips(refs, removable) {
    const row = document.createElement("div");
    row.className = "chips";
    refs.forEach((item, i) => {
      const ref = item.ref || item;
      const chip = document.createElement("span");
      chip.className = "chip";
      chip.title = ref.selector;
      chip.textContent = chipText(ref, i + 1);
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
    const refs = state.refs.map((item) => item.ref);
    input.value = "";
    store.set("sessionStorage", "draft", null);
    state.refs = [];
    saveRefs();
    drawChips();
    grow();
    send(text, refs);
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
    if (state.who && state.who.id && !(status.people || []).some((person) => person.id === state.who.id)) {
      state.who = null; // no longer one of the team's people
      store.set("localStorage", "who", null);
      return true;
    }
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
    try { await loadConversation(); } catch { /* shown when the panel opens */ }
    // Open again after a reload, without taking the page's focus.
    if (store.get("localStorage", "open")) toggle(true, true);
    else render();
    grow();
    waitForAnswer();
    setInterval(() => { refreshStatus().then((changed) => { if (changed && !state.sending) render(); }).catch(() => {}); }, 60000);
  })();
})();
