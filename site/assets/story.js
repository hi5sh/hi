// A scene-based animation, drawn in HTML and CSS: a feature page's demo
// (_layouts/feature.html), from the scenes in assets/demos/<name>.js, played
// as a loop in the box on the page.
(() => {
  "use strict";
  const STORY = window.HI5_STORY;
  const mount = document.querySelector("[data-story]");
  if (!STORY || !mount) return;

  // The picture's design size; it is scaled to fit. A feature page's terminal
  // is wider, with a narrower size for phones so its text stays readable.
  const size = (STORY.narrow && innerWidth < 600 ? STORY.narrow : STORY.size) || {};
  const W = size.w || 480, H = size.h || 560;
  const reduceMotion = matchMedia("(prefers-reduced-motion: reduce)");
  // The admin page's and the dev page's own stylesheets and Geist, copied
  // beside this file by the site's build (.github/workflows/pages.yml).
  const PAGES = new URL("pages/", document.currentScript.src).href;

  // ---- small DOM helpers ----------------------------------------------------
  const el = (tag, attrs, ...kids) => {
    const node = document.createElement(tag);
    for (const [k, v] of Object.entries(attrs || {})) {
      if (v == null || v === false) continue;
      if (k === "class") node.className = v;
      else if (k === "text") node.textContent = v;
      else node.setAttribute(k, v === true ? "" : v);
    }
    for (const kid of kids.flat()) if (kid != null) node.append(kid.nodeType ? kid : document.createTextNode(kid));
    return node;
  };
  // timed marks an element to appear at a time: {at}.
  const timed = (node, t) => {
    if (!t) return node;
    node.dataset.at = t.at ?? 0;
    return node;
  };
  const person = id => STORY.people[id];
  const avatar = id => { const p = person(id); return el("span", { class: "av t-" + p.tone, text: p.init }); };
  const win = (title, cls, ...kids) => el("div", { class: "w " + (cls || "") },
    el("div", { class: "w-bar" }, el("i"), el("i"), el("i"), el("span", { text: title })), ...kids);
  const message = m => {
    const p = person(m.who);
    const body = el("div", { class: "mbody" },
      el("span", { class: "who", text: p.name }, p.role ? el("small", { text: p.role }) : null));
    if (m.text) body.append(el("p", {}, m.mention ? el("b", { text: "@" + STORY.team + " " }) : null, m.text));
    if (m.list) body.append(el("ol", {}, ...m.list.map(x => el("li", { text: x }))));
    if (m.after) body.append(el("p", { text: m.after }));
    if (m.link) body.append(el("span", { class: "link", text: typeof m.link === "string" ? m.link : STORY.preview }));
    return timed(el("div", { class: "msg" + (m.who === "hi" ? " sys" : "") }, avatar(m.who), body), m);
  };
  const caption = text => text ? el("div", { class: "sc-cap", text }) : null;

  // A page scene's document: the scene's markup in the admin page's or the
  // dev page's own stylesheets, Orca's theme, and the few rules that play it
  // (an item hidden until it's due, and after its until; text typed; the
  // pointer and its click).
  const PLAY_CSS = `
html, body { height: 100%; margin: 0; overflow: hidden; }
[data-at]:not(.on), [data-until].off, .pt { display: none !important; }
[data-at].on:not(.pt) { animation: play-in .35s ease both; }
.still.on { animation: none !important; }
@keyframes play-in { from { opacity: 0; transform: translateY(6px); } }
.typing::after { content: ""; display: inline-block; width: 1.5px; height: 1.1em; margin-left: 1px; vertical-align: -.18em; background: currentColor; animation: play-caret 1s steps(1) infinite; }
@keyframes play-caret { 50% { opacity: 0; } }
.play-pointer { position: fixed; z-index: 99; left: -40px; top: -40px; width: 22px; height: 22px; pointer-events: none;
  transition: left .6s cubic-bezier(.4, 0, .2, 1), top .6s cubic-bezier(.4, 0, .2, 1); filter: drop-shadow(0 2px 4px rgba(0,0,0,.6)); }
.play-ring { position: fixed; z-index: 98; width: 30px; height: 30px; margin: -15px 0 0 -15px; border-radius: 50%; pointer-events: none;
  border: 2px solid rgba(255,255,255,.85); animation: play-ring .46s ease-out forwards; }
@keyframes play-ring { from { transform: scale(.4); opacity: .9; } to { transform: scale(1.25); opacity: 0; } }
@media (prefers-reduced-motion: reduce) { [data-at].on:not(.pt) { animation: none; } .play-pointer { display: none; } }`;
  const POINTER = '<svg class="play-pointer" viewBox="0 0 24 24" aria-hidden="true"><path d="M5 3l14 8-6.2 1.6L10 19z" fill="#fff" stroke="#151515" stroke-width="1.4" stroke-linejoin="round"/></svg>';
  const pageDoc = sc => '<!doctype html><html lang="en" data-theme="orca"><head><meta charset="utf-8">' +
    '<link rel="stylesheet" href="' + PAGES + 'dev.css">' + (sc.page === "admin" ? '<link rel="stylesheet" href="' + PAGES + 'admin.css">' : "") +
    "<style>" + PLAY_CSS + "</style></head><body>" + sc.html + POINTER + "</body></html>";

  // ---- the screens ------------------------------------------------------------
  const screens = {
    terminal(sc) {
      // A line is a command (cmd, after the line's or the scene's prompt), or
      // what a command printed: ok, warn, err, or plain out. A live
      // line is hi's progress line: each of its states replaces the one
      // before, and it clears when the last, empty one comes due.
      const live = l => el("div", { class: "ln live" }, ...l.live.map(x => timed(
        el("span", {}, x.text ? el("b", { class: "scan", text: "▬▬▬▬▬▬▬▬▬▬▬▬" }) : null, x.text ? "  " + x.text : null), x)));
      // pre keeps a line on one line, for a table's rows: smaller, and clipped.
      const pre = (node, l) => { if (l.pre) node.classList.add("pre"); return node; };
      const lines = sc.lines.map(l => l.live ? live(l) : pre(timed(
        l.cmd ? el("div", { class: "ln cmd" }, el("span", { class: "ps", text: (l.ps ?? sc.prompt ?? "$") + " " }), l.cmd)
          : l.ok ? el("div", { class: "ln ok", text: l.ok })
            : l.warn ? el("div", { class: "ln warn", text: l.warn })
              : l.err ? el("div", { class: "ln err", text: l.err })
                : el("div", { class: "ln out", text: l.out ?? "" }), l), l));
      return el("div", { class: "sc" },
        win(sc.window ?? sc.title, "w-term", el("div", { class: "w-term-body scroll" }, ...lines)),
        caption(sc.caption));
    },
    // page: the admin page or the dev page, drawn by their own stylesheets
    // in a frame of their own, at the scene's width (narrow: under 600px) and
    // scaled into the window. Its items are in sc.html, timed as a
    // terminal's lines are: data-at, and data-until to leave; data-type to
    // be typed over data-dur; and the pointer's moves, <i class="pt"
    // data-at data-to="selector" data-click>.
    page(sc) {
      const width = (size === STORY.narrow && sc.narrowWidth) || sc.width || 1000;
      const frame = el("iframe", { class: "w-page-frame", title: sc.title, tabindex: "-1", style: `width: ${width}px` });
      frame.setAttribute("inert", "");
      frame.srcdoc = pageDoc(sc);
      const body = el("div", { class: "w-page-body" }, frame);
      const node = el("div", { class: "sc" }, win(sc.window ?? sc.title, "w-page", body), caption(sc.caption));
      // Once it's on the stage: scaled to the window, and its document once loaded.
      node.fit = () => {
        const scale = body.offsetWidth / width;
        frame.style.transform = `scale(${scale})`;
        frame.style.height = body.offsetHeight / scale + "px";
      };
      node.ready = new Promise(resolve => frame.addEventListener("load", () => resolve(frame.contentDocument), { once: true }));
      return node;
    },
    // title: words on their own, over a picture, such as the site's tagline
    // before its demo: a kicker, the tagline, and a lede, each fading up in
    // turn; the scene fades out, not sideways, into the next.
    title(sc) {
      const node = el("div", { class: "sc sc-title" },
        el("div", { class: "sc-title-in" },
          sc.kicker ? timed(el("p", { class: "sc-kicker", text: sc.kicker }), { at: 0 }) : null,
          timed(el("p", { class: "sc-tagline", text: sc.tagline }), { at: 300 }),
          sc.lede ? timed(el("p", { class: "sc-lede", text: sc.lede }), { at: 900 }) : null));
      if (sc.image) node.style.setProperty("--title-image", `url("${sc.image}")`);
      return node;
    },
    channel(sc) {
      const groups = { people: "people", owners: "owner", lead: "lead · AI" };
      const strip = el("div", { class: "members" }, ...Object.entries(groups)
        .filter(([kind]) => sc.members.some(m => m.kind === kind))
        .map(([kind, label]) => el("div", { class: "mgroup" }, ...sc.members.filter(m => m.kind === kind).map(m => avatar(m.who)), el("small", { text: label }))));
      return el("div", { class: "sc" },
        win(STORY.channel, "slack", strip, el("div", { class: "msgs scroll" }, ...sc.messages.map(message))),
        caption(sc.caption));
    },
  };

  // ---- the player -------------------------------------------------------------
  class Story {
    constructor(root, opts) {
      this.opts = opts;
      this.scenes = STORY.scenes.filter(s => s.short);
      this.static = reduceMotion.matches;
      this.root = root;
      root.classList.add("st");
      root.classList.toggle("st-narrow", size === STORY.narrow);
      this.box = el("div", { class: "st-box", "aria-hidden": "true", style: `aspect-ratio: ${W} / ${H}` });
      this.stage = el("div", { class: "st-stage", style: `width: ${W}px; height: ${H}px` });
      this.box.append(this.stage);
      this.cap = el("p", { class: "st-text", "aria-live": "polite" });
      this.dots = el("div", { class: "st-dots", role: "group", "aria-label": "Scenes" });
      this.scenes.forEach((s, i) => {
        const dot = el("button", { type: "button", class: "st-dot", "aria-label": s.title, title: s.title });
        dot.onclick = () => this.seek(i, 0, true);
        this.dots.append(dot);
      });
      this.playBtn = el("button", { type: "button", class: "st-btn st-play", "aria-label": "Pause" });
      this.playBtn.onclick = () => (this.playing ? this.pause(true) : this.play(true));
      this.prev = el("button", { type: "button", class: "st-btn", "aria-label": "Previous scene", text: "‹" });
      this.next = el("button", { type: "button", class: "st-btn", "aria-label": "Next scene", text: "›" });
      this.prev.onclick = () => this.seek((this.i - 1 + this.scenes.length) % this.scenes.length, 0, true);
      this.next.onclick = () => this.seek((this.i + 1) % this.scenes.length, 0, true);
      const controls = el("div", { class: "st-controls" }, this.prev, this.playBtn, this.next, this.dots);
      this.ui = el("div", { class: "st-ui" }, this.cap, controls);
      root.append(this.box, this.ui);
      this.fit();
      this.ro = new ResizeObserver(() => this.fit());
      this.ro.observe(this.box);
      this.i = -1; this.t = 0; this.playing = false; this.userPaused = false;
      this.seek(0, 0);
      if (this.static) { this.setPlayState(false); this.playBtn.hidden = true; }
      else if (opts.autoplay) this.play();
      this.raf = requestAnimationFrame(ts => this.frame(ts));
    }
    fit() {
      const w = this.box.clientWidth || W;
      this.stage.style.transform = `scale(${w / W})`;
      if (this.current && this.current.fit) this.current.fit();
    }
    pace() { return (STORY.pace && STORY.pace.short) || 1; }
    dur(s) { return s.short * this.pace(); }
    itemTime(node) { return Math.round(+node.dataset.at * this.pace()); }
    seek(i, t = 0, byUser = false) {
      const s = this.scenes[i];
      const old = this.current;
      const next = screens[s.screen](s);
      next.classList.add("enter");
      this.stage.append(next);
      if (old) {
        old.classList.add("leave");
        setTimeout(() => old.remove(), this.static ? 0 : 450);
      }
      void next.offsetWidth; // let the enter state paint before it animates away
      next.classList.remove("enter");
      this.current = next; this.i = i; this.t = t;
      this.items = this.timedIn(next);
      if (this.static) for (const it of this.items) this.reveal(it);
      else this.advance();
      // A page scene's items are in its frame, there once it has loaded.
      if (next.fit) next.fit();
      if (next.ready) next.ready.then(doc => {
        if (this.current !== next || !doc) return;
        this.items = this.items.concat(this.timedIn(doc)).sort((a, b) => a.at - b.at);
        if (this.static) { for (const it of this.items) { this.reveal(it); if (it.until != null) it.node.classList.add("off"); } }
        else this.advance();
      });
      this.cap.replaceChildren(el("b", { text: s.title }), `scene ${i + 1} of ${this.scenes.length}`);
      [...this.dots.children].forEach((d, j) => d.setAttribute("aria-current", j === i ? "true" : "false"));
      if (byUser && !this.static && !this.playing && !this.userPaused) this.play();
    }
    // timedIn is what's timed in a scene or a page scene's document.
    timedIn(root) {
      return [...root.querySelectorAll("[data-at], [data-until]")].map(n => ({
        node: n, at: n.dataset.at == null ? 0 : this.itemTime(n),
        until: n.dataset.until == null ? null : Math.round(+n.dataset.until * this.pace()),
        type: n.dataset.type, dur: Math.round(+(n.dataset.dur || 1200) * this.pace()),
      })).sort((a, b) => a.at - b.at);
    }
    reveal(it) {
      if (it.done) return;
      it.done = true;
      const n = it.node;
      n.classList.add("on");
      if (it.type != null) { n.textContent = this.static ? it.type : ""; if (!this.static) n.classList.add("typing"); }
      if (n.classList.contains("pt")) { this.point(n); return; }
      const scroller = n.closest(".scroll");
      if (scroller) scroller.scrollTop = scroller.scrollHeight;
    }
    // point moves a page scene's pointer to what the move names, and clicks.
    point(move) {
      const doc = move.ownerDocument;
      const pointer = doc.querySelector(".play-pointer"), target = move.dataset.to && doc.querySelector(move.dataset.to);
      if (!pointer || !target || this.static) return;
      const r = target.getBoundingClientRect();
      const x = r.left + Math.min(r.width / 2, 40), y = r.top + r.height / 2;
      pointer.style.left = x + "px"; pointer.style.top = y + "px";
      if (move.dataset.click == null) return;
      setTimeout(() => {
        const ring = doc.createElement("span");
        ring.className = "play-ring";
        ring.style.left = x + "px"; ring.style.top = y + "px";
        doc.body.append(ring);
        setTimeout(() => ring.remove(), 600);
      }, 620);
    }
    advance() {
      for (const it of this.items) {
        if (it.at <= this.t) this.reveal(it);
        if (it.until != null && it.until <= this.t) it.node.classList.add("off");
        if (it.type != null && it.done && !it.typed) {
          const n = Math.min(it.type.length, Math.floor(it.type.length * (this.t - it.at) / it.dur));
          it.node.textContent = it.type.slice(0, n);
          if (n >= it.type.length) { it.typed = true; it.node.classList.remove("typing"); }
        }
      }
    }
    frame(ts) {
      this.raf = requestAnimationFrame(t2 => this.frame(t2));
      const dt = this.last ? Math.min(ts - this.last, 100) : 0;
      this.last = ts;
      if (!this.playing || this.static) return;
      this.t += dt;
      this.advance();
      const s = this.scenes[this.i];
      if (this.t >= this.dur(s)) {
        if (this.i + 1 < this.scenes.length) this.seek(this.i + 1);
        else if (this.opts.loop) this.seek(0);
        else this.pause();
      }
    }
    setPlayState(on) {
      this.playing = on;
      this.playBtn.setAttribute("aria-label", on ? "Pause" : "Play");
      this.playBtn.classList.toggle("paused", !on);
    }
    play(byUser) { if (this.static) return; if (byUser) this.userPaused = false; this.setPlayState(true); }
    pause(byUser) { if (byUser) this.userPaused = true; this.setPlayState(false); }
    destroy() { cancelAnimationFrame(this.raf); this.ro.disconnect(); this.root.replaceChildren(); }
  }

  // ---- on the page --------------------------------------------------------------
  const fallback = mount.querySelector("[data-story-fallback]");
  if (fallback) fallback.remove();
  const short = new Story(mount, { loop: true, autoplay: true });
  window.hi5Story = short;

  // Only animate while the box is on screen and the tab is visible.
  let onScreen = true;
  const io = new IntersectionObserver(([e]) => { onScreen = e.isIntersecting; sync(); }, { threshold: 0.2 });
  io.observe(short.box);
  document.addEventListener("visibilitychange", sync);
  function sync() { if (short.userPaused) return; if (onScreen && !document.hidden) short.play(); else short.pause(); }
})();
