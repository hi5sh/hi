// The hi team helper: a chat with the team's lead beside any page of the
// team's app. hi net expose adds this script to each page it relays and
// serves /__hi/ from the team's service, so the app holds none of it
// (docs/specs/approved/hi_team_helper.md). Plain JavaScript, no framework,
// no build step: hi embeds this file as it is.
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
.who-screen input { flex: 1; font: inherit; color: var(--fg); background: var(--soft); border: 1px solid var(--line); border-radius: 10px; padding: 8px 10px; }
`;

  const ICONS = {
    chat: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21 12a8 8 0 0 1-11.6 7.1L4 20l1-4.6A8 8 0 1 1 21 12z"/></svg>',
    close: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M6 6l12 12M18 6L6 18"/></svg>',
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
    <textarea rows="1" placeholder="Message the lead…" aria-label="Message"></textarea>
    <button class="send" type="submit">Send</button>
  </form>
</section>`;
  const $ = (selector) => root.querySelector(selector);
  const fab = $(".fab"), panel = $(".panel"), body = $(".body"), menu = $(".menu");
  const input = $("textarea"), sendButton = $(".send"), composer = $(".composer"), notice = $(".notice");

  // The app's keyboard shortcuts shouldn't fire while someone types here.
  for (const type of ["keydown", "keyup", "keypress"]) host.addEventListener(type, (event) => event.stopPropagation());

  const state = {
    status: null,
    who: store.get("localStorage", "who"), // {id, name}
    conversation: null,
    sending: false,
    poll: null,
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

  async function send(text) {
    state.sending = true;
    const page = { path: location.pathname + location.search + location.hash, title: document.title, width: innerWidth, height: innerHeight };
    const conversation = state.conversation || { messages: [] };
    conversation.messages = conversation.messages.concat([{ from: "person", name: state.who.name, text, pending: true }]);
    state.conversation = conversation;
    let answer = "";
    let lead = null;
    render({ typing: true });
    try {
      const response = await call("messages", {
        method: "POST",
        body: { conversation: conversation.id || "", person: state.who.id || "", name: state.who.name, text, page },
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
  // the composer: Enter sends, Shift+Enter is a new line; an unsent
  // message survives a reload

  input.value = store.get("sessionStorage", "draft") || "";
  const grow = () => { input.style.height = "auto"; input.style.height = Math.min(input.scrollHeight, 160) + "px"; };
  input.addEventListener("input", () => { grow(); store.set("sessionStorage", "draft", input.value || null); });
  input.addEventListener("keydown", (event) => {
    if (event.key === "Enter" && !event.shiftKey && !event.isComposing) { event.preventDefault(); composer.requestSubmit(); }
  });
  composer.addEventListener("submit", (event) => {
    event.preventDefault();
    const text = input.value.trim();
    if (!text || state.sending) return;
    input.value = "";
    store.set("sessionStorage", "draft", null);
    grow();
    send(text);
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
