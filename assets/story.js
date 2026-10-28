// The story on the homepage: a scene-based animation, drawn in HTML and CSS
// from the scenes in assets/story-scenes.js. The short loop plays in the
// box on the page; "Watch the full story" opens every scene in a dialog.
// Nothing here changes what hi does; the scenes show what a hi team does today.
// A feature page (_layouts/feature.html) plays its own scenes, from
// assets/demos/<name>.js, with the same player.
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
  // timed marks an element to appear at a time: {at, atShort, full}.
  const timed = (node, t) => {
    if (!t) return node;
    node.dataset.at = t.at ?? 0;
    if (t.atShort != null) node.dataset.atShort = t.atShort;
    if (t.full) node.dataset.full = "";
    return node;
  };
  // state is an invisible mark: when it comes due, the scene gets data-state.
  const state = (t, name) => timed(el("i", { class: "st-mark", "data-state": name }), t);
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

  // ---- the screens ------------------------------------------------------------
  const screens = {
    idea(sc) {
      const table = el("table", {},
        el("thead", {}, el("tr", {}, ...sc.sheet.head.map(x => el("th", { text: x })))),
        el("tbody", {}, ...sc.sheet.rows.map(r => el("tr", {}, ...r.map((c, i) =>
          el("td", { class: c === "✗" ? "no" : c === "✓" ? "yes" : "", text: c }))))));
      return el("div", { class: "sc sc-idea" },
        el("div", { class: "desk-name", text: "Emma · HR · Monday morning" }),
        win(sc.sheet.title, "sheet", el("div", { class: "sheet-body" }, table)),
        state(sc.think, "thinking"),
        timed(el("div", { class: "bubble", text: sc.bubble }), sc.think),
        timed(el("div", { class: "sketch" },
          el("div", { class: "sketch-bar", text: sc.sketch.title }),
          ...sc.sketch.cards.map(c => el("div", { class: "sketch-card", text: c }))), sc.sketch),
        caption(sc.caption));
    },
    dm(sc) {
      const p = person(sc.with);
      return el("div", { class: "sc" },
        win(p.name + " · " + p.role + " · direct message", "slack",
          el("div", { class: "msgs scroll" }, ...sc.messages.map(message))),
        caption(sc.caption));
    },
    terminal(sc) {
      // A line is a command (cmd, after the line's or the scene's prompt), a
      // step, or what a command printed: ok, warn, err, or plain out. A live
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
                : l.out != null ? el("div", { class: "ln out", text: l.out })
                  : el("div", { class: "ln" }, el("span", { class: "ln-step", text: l.step }), " " + l.text), l), l));
      const t = sc.toast;
      return el("div", { class: "sc" },
        win(sc.window ?? sc.title, "w-term", el("div", { class: "w-term-body scroll" }, ...lines)),
        t ? timed(el("div", { class: "toast" }, avatar(t.who), el("div", {},
          el("span", { class: "who", text: person(t.who).name }, el("small", { text: "Slack" })), el("p", { text: t.text }))), t) : null,
        caption(sc.caption));
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
    devpage(sc) {
      // The team's dev page, as an owner sees it: the Code tab's workspaces and
      // the agent's chat, or the Release tab's Where things run. A press is a
      // timed outline over a button; status lines replace the one before.
      const press = t => t && t.at != null ? timed(el("i", { class: "dp-press" }), t) : null;
      const button = (label, t) => el("span", { class: "dp-btn" }, label, press(t));
      const chips = states => el("div", { class: "task-states" }, ...states.map(x => timed(el("span", { class: "chip s-" + x.s.split(/[ :]/)[0], text: x.s }), x)));
      const tabs = el("div", { class: "dp-tabs" }, ...["Tasks", "Code", "Files", "Release", "Data", "System", "Security"].map(t => el("span", { class: t === sc.tab ? "on" : "", text: t })));
      const status = el("div", { class: "dp-status" }, ...(sc.status || []).map(x => timed(el("span", { text: x.text }), x)));
      const item = c => {
        if (c.working) return timed(el("div", { class: "dp-working" }, el("b", { text: "✻" }), " " + c.working), c);
        if (c.ask) return timed(el("div", { class: "dp-ask" }, el("p", { text: c.ask }),
          ...c.options.map((o, i) => el("span", { class: "dp-opt" }, (i + 1) + ". " + o, i === c.pick ? press({ at: c.pickAt, atShort: c.pickAtShort }) : null))), c);
        return message(c);
      };
      let body;
      if (sc.tab === "Release") {
        body = el("div", { class: "dp-where" }, el("div", { class: "dp-h", text: "Where things run" }),
          ...sc.rows.map(r => el("div", { class: "dp-row " + (r.tone || "") },
            el("div", { class: "dp-row-head" }, el("b", { text: r.name }), r.button ? button(r.button, r) : null),
            el("span", { text: r.text }),
            r.changes ? el("ul", {}, ...r.changes.map(x => el("li", { text: x }))) : null)),
          status);
      } else {
        const side = el("div", { class: "dp-side" }, el("div", { class: "dp-h", text: "Workspaces" }),
          ...sc.work.map(w => timed(el("div", { class: "dp-ws" }, el("b", { text: w.id }), el("span", { text: w.title }), chips(w.states)), w)));
        body = el("div", { class: "dp-body" }, side, el("div", { class: "dp-main" },
          el("div", { class: "msgs scroll dp-chat" }, ...sc.chat.map(item)),
          el("div", { class: "dp-actions" }, status, ...sc.actions.map(a => button(a.label, a)))));
      }
      return el("div", { class: "sc sc-dev" },
        win(sc.window, "dp", tabs, body),
        sc.ready ? timed(el("div", { class: "ready" }, message({ ...sc.ready, at: 0 })), sc.ready) : null,
        caption(sc.caption));
    },
    app(sc, opts) {
      // The onboarding app as the team built it: an example, not a real app.
      const hire = (name, date, done, total, cls) => el("div", { class: "hire " + (cls || "") },
        el("b", { text: name }), el("span", { text: date }), el("i", { class: "bar" }, el("i", { style: "width:" + Math.round(100 * done / total) + "%" })), el("small", { text: done + "/" + total + " steps" }));
      const step = (text, owner, cls) => el("div", { class: "step " + (cls || "") }, el("i", { class: "tick" }), el("span", { text }), el("small", { text: owner }));
      const app = el("div", { class: "app" },
        el("div", { class: "app-bar" }, el("b", { text: "Onboarding" }), el("span", { text: STORY.preview.replace("https://", "") })),
        el("div", { class: "app-h", text: "Starting next week" }),
        el("div", { class: "hires" }, hire("Noor Haddad", "Mon 20 Oct", 3, 7, "noor"), hire("Jonas Berg", "Wed 22 Oct", 0, 7), el("div", { class: "hire added" }, el("b", { text: "Priya Shah" }), el("span", { text: "Mon 27 Oct" }), el("i", { class: "bar" }, el("i", { style: "width:0" })), el("small", { text: "0/7 steps" }))),
        el("div", { class: "app-h" }, "Noor's steps", el("button", { class: "btn-add", type: "button", tabindex: "-1", text: "+ Add new hire" })),
        el("div", { class: "steps" }, step("Laptop ordered", "IT", "done"), step("Accounts created", "IT", "done"), step("Badge requested", "Facilities", "late"), step("Buddy assigned", "Emma", "tick-me"), step("First-week plan", "Manager")),
        el("div", { class: "form" }, el("div", { class: "form-h", text: "New hire" }), el("label", {}, "Name", el("span", { class: "field name" })), el("label", {}, "Start date", el("span", { class: "field date" })), el("button", { class: "btn-save", type: "button", tabindex: "-1", text: "Add" })),
        el("button", { class: "helper", type: "button", tabindex: "-1", "aria-hidden": "true" }, el("i")),
        el("div", { class: "cursor" }));
      const marks = (sc.cursor || []).map(c => timed(el("i", { class: "st-mark", "data-cursor": c.to || "", "data-click": c.click ? "" : null, "data-type": c.type || "", "data-state": c.state || "" }), c));
      return el("div", { class: "sc sc-app" }, app, ...marks, caption(sc.caption));
    },
    improve(sc) {
      const root = screens.app({ cursor: [], caption: sc.caption });
      root.classList.add("sc-improve");
      root.dataset.state = "settled";
      const panel = el("div", { class: "panel" },
        el("div", { class: "panel-tabs" }, el("span", { text: "Ask" }), el("span", { text: "Docs" }), el("span", { class: "on", text: "Feedback" })),
        el("div", { class: "msgs scroll" }, ...sc.messages.map(message)));
      root.insertBefore(state(sc.open, "open"), root.lastChild);
      root.insertBefore(timed(el("i", { class: "st-mark", "data-cursor": "helper", "data-click": "" }), { at: sc.open.at - 600, atShort: 100 }), root.lastChild);
      root.insertBefore(panel, root.lastChild);
      if (sc.bell) root.querySelector(".step.late").append(timed(el("span", { class: "bell", text: "reminder on" }), sc.bell));
      return root;
    },
    ecosystem(sc) {
      const cards = sc.teams.map((t, i) => timed(el("div", { class: "teamcard c" + i },
        el("b", { text: t.channel }), el("span", { text: t.app }),
        el("div", { class: "crew" }, ...t.people.map(p => el("span", { class: "av t-warm", text: p })), el("span", { class: "av t-sky", text: t.owner }), el("span", { class: "av t-violet", text: "LD" }))), t));
      const svg = document.createElementNS("http://www.w3.org/2000/svg", "svg");
      svg.setAttribute("viewBox", "0 0 480 560"); svg.setAttribute("class", "wires");
      svg.innerHTML = '<path d="M240 330 L120 190"/><path d="M240 330 L360 190"/><path d="M240 330 L240 440"/>';
      return el("div", { class: "sc sc-eco" }, timed(svg, sc.hub), ...cards,
        timed(el("div", { class: "hub" }, el("b", { text: "hi" }), el("span", { text: sc.hub.text })), sc.hub),
        caption(sc.caption));
    },
    hi5(sc) {
      return el("div", { class: "sc sc-hi5" },
        el("div", { class: "hands" }, el("div", { class: "hand l" }), el("div", { class: "hand r" })),
        state(sc.meet, "meet"),
        el("div", { class: "brand" },
          timed(el("div", { class: "brand-line", text: sc.line.text }), sc.line),
          timed(el("div", { class: "brand-sub" }, el("b", { text: "hi5" }), " · ", sc.sub.text.replace(/^hi5 · /, "")), sc.sub)));
    },
  };

  // ---- the player -------------------------------------------------------------
  class Story {
    constructor(root, opts) {
      this.opts = opts;
      this.scenes = STORY.scenes.filter(s => opts.mode === "full" || s.short);
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
    }
    pace() { return (STORY.pace && STORY.pace[this.opts.mode]) || 1; }
    dur(s) { return (this.opts.mode === "full" ? s.dur : s.short) * this.pace(); }
    itemTime(s, node) {
      let t = +node.dataset.at;
      if (this.opts.mode === "short") t = node.dataset.atShort != null ? +node.dataset.atShort : t * s.short / (s.dur || s.short);
      return Math.round(t * this.pace());
    }
    seek(i, t = 0, byUser = false) {
      const s = this.scenes[i];
      const old = this.current;
      const next = screens[s.screen](s, this.opts);
      next.classList.add("enter");
      this.stage.append(next);
      if (old) {
        old.classList.add("leave");
        setTimeout(() => old.remove(), this.static ? 0 : 450);
      }
      void next.offsetWidth; // let the enter state paint before it animates away
      next.classList.remove("enter");
      this.current = next; this.i = i; this.t = t;
      if (this.opts.mode === "short") for (const n of next.querySelectorAll("[data-full]")) n.remove();
      this.items = [...next.querySelectorAll("[data-at]")]
        .map(n => ({ node: n, at: this.itemTime(s, n) }))
        .sort((a, b) => a.at - b.at);
      if (this.static) for (const it of this.items) this.reveal(it);
      else this.advance();
      this.cap.replaceChildren(el("b", { text: s.title }), `scene ${i + 1} of ${this.scenes.length}`);
      [...this.dots.children].forEach((d, j) => d.setAttribute("aria-current", j === i ? "true" : "false"));
      if (byUser && !this.static && !this.playing && !this.userPaused) this.play();
    }
    reveal(it) {
      if (it.done) return;
      it.done = true;
      const n = it.node;
      n.classList.add("on");
      if (n.dataset.state) this.current.dataset.state = n.dataset.state;
      if (n.dataset.cursor != null) this.moveCursor(n);
      if (n.dataset.type) this.current.querySelector(".field." + n.dataset.type)?.classList.add("filled");
      const scroller = n.closest(".scroll");
      if (scroller) scroller.scrollTop = scroller.scrollHeight;
    }
    moveCursor(n) {
      const cur = this.current.querySelector(".cursor");
      if (!cur) return;
      const targets = { add: ".btn-add", save: ".btn-save", tick: ".tick-me .tick", helper: ".helper" };
      const target = n.dataset.cursor && this.current.querySelector(targets[n.dataset.cursor]);
      if (target) {
        // The cursor sits in the app frame; measure from there, in the picture's own pixels.
        const a = cur.offsetParent.getBoundingClientRect(), b = target.getBoundingClientRect(), k = this.stage.getBoundingClientRect().width / W;
        cur.style.left = (b.left - a.left + b.width / 2) / k + "px";
        cur.style.top = (b.top - a.top + b.height / 2) / k + "px";
        cur.classList.add("on");
      }
      if (n.hasAttribute("data-click")) { cur.classList.add("click"); setTimeout(() => cur.classList.remove("click"), 300); }
    }
    advance() {
      for (const it of this.items) if (it.at <= this.t) this.reveal(it);
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
  const short = new Story(mount, { mode: "short", loop: true, autoplay: true });
  window.hi5Story = short;

  // Only animate while the box is on screen and the tab is visible.
  let onScreen = true;
  const io = new IntersectionObserver(([e]) => { onScreen = e.isIntersecting; sync(); }, { threshold: 0.2 });
  io.observe(short.box);
  document.addEventListener("visibilitychange", sync);
  function sync() { if (short.userPaused) return; if (onScreen && !document.hidden) short.play(); else short.pause(); }

  // The full story, in a dialog.
  const dialog = document.querySelector("[data-story-dialog]");
  const opener = document.querySelector("[data-story-open]");
  if (dialog && opener) {
    const holder = dialog.querySelector("[data-story-full]");
    let full = null;
    const fitDialog = () => {
      if (!full) return;
      const w = Math.min(innerWidth * 0.92 - 32, (innerHeight - 190) * W / H, 640);
      full.box.style.width = Math.max(240, w) + "px";
      full.fit();
    };
    const open = () => {
      short.pause();
      full = new Story(holder, { mode: "full", loop: false, autoplay: true });
      window.hi5StoryFull = full;
      fitDialog();
      dialog.showModal();
    };
    const close = () => { if (full) { full.destroy(); full = null; } window.hi5StoryFull = null; sync(); opener.focus(); };
    opener.addEventListener("click", open);
    dialog.addEventListener("close", close);
    dialog.querySelector("[data-story-close]")?.addEventListener("click", () => dialog.close());
    dialog.addEventListener("click", e => { if (e.target === dialog) dialog.close(); });
    addEventListener("resize", fitDialog);
    if (new URLSearchParams(location.search).get("story") === "full") open();
  }
})();
