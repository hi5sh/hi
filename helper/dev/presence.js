// Who's on this page: the admin page's admins, or an app's dev page's
// owners and developers. Each window holds a WebSocket to the page; the
// top bar's #presence shows a quiet pill a person, a dot in their colour
// while they're here, with how many windows they have open, greyed when none of them had
// a key, a click, or a scroll in a few minutes. The page calls
// hiPresence.start() once it's signed in.
(function () {
  "use strict";
  var IDLE_AFTER = 3 * 60 * 1000;
  var socket = null, started = false, retry = 0, wait = 1000, idle = false, lastInput = Date.now();

  function box() { return document.getElementById("presence"); }

  function render(people) {
    var el = box();
    if (!el) return;
    el.textContent = "";
    var shown = people.slice(0, 6);
    shown.forEach(function (p) {
      var pill = document.createElement("span");
      pill.className = "presence-pill" + (p.idle ? " idle" : "") + (p.you ? " you" : "");
      pill.style.setProperty("--who", p.color); // the dot, and a faint tint (dev.css)
      pill.title = p.name + (p.you ? " (you)" : "") + " · " + p.tabs + (p.tabs === 1 ? " window" : " windows") + (p.idle ? " · idle for a few minutes" : "");
      var name = document.createElement("span");
      name.className = "presence-name";
      name.textContent = p.name;
      pill.appendChild(name);
      if (p.tabs > 1) {
        var count = document.createElement("span");
        count.className = "presence-count";
        count.textContent = String(p.tabs);
        pill.appendChild(count);
      }
      el.appendChild(pill);
    });
    if (people.length > shown.length) {
      var more = document.createElement("span");
      more.className = "presence-more muted small";
      more.textContent = "+" + (people.length - shown.length);
      more.title = people.slice(shown.length).map(function (p) { return p.name; }).join(", ");
      el.appendChild(more);
    }
    el.hidden = !people.length;
  }

  // An idle window says how long it was without input, so its time in
  // use (hi's face time, presence.go) leaves that out.
  function send() {
    if (socket && socket.readyState === 1) socket.send(JSON.stringify(idle ? { idle: true, quiet: document.hidden ? 0 : Date.now() - lastInput } : { idle: false }));
  }

  function connect() {
    clearTimeout(retry);
    if (socket && socket.readyState <= 1) return;
    var url = (location.protocol === "https:" ? "wss://" : "ws://") + location.host + location.pathname.replace(/[^/]*$/, "") + "api/presence";
    var opened = socket = new WebSocket(url);
    opened.onopen = function () { wait = 1000; if (document.hidden) idle = true; send(); };
    opened.onmessage = function (event) {
      try { render(JSON.parse(event.data).people || []); } catch (e) { /* not ours */ }
    };
    opened.onclose = function () {
      if (opened !== socket) return;
      render([]);
      retry = setTimeout(connect, wait);
      wait = Math.min(wait * 2, 30000);
    };
  }

  // Input marks the window active at once; a check every 15 s marks it
  // idle after IDLE_AFTER without any. A hidden window is idle at once.
  function input() {
    lastInput = Date.now();
    if (idle) { idle = false; send(); }
  }
  document.addEventListener("visibilitychange", function () {
    if (document.hidden) { if (!idle) { idle = true; send(); } }
    else input();
  });
  ["pointerdown", "keydown", "wheel", "touchstart", "focus"].forEach(function (kind) {
    window.addEventListener(kind, input, { passive: true, capture: true });
  });
  var moved = 0;
  window.addEventListener("pointermove", function () {
    if (Date.now() - moved > 5000) { moved = Date.now(); input(); }
  }, { passive: true });
  setInterval(function () {
    var now = document.hidden || Date.now() - lastInput > IDLE_AFTER;
    if (now !== idle) { idle = now; send(); }
  }, 15000);
  // A window that's closed says so at once; the server would also notice
  // when it stops answering pings.
  window.addEventListener("pagehide", function () { if (socket) socket.close(); });
  window.addEventListener("pageshow", function (event) { if (event.persisted && started) connect(); });

  window.hiPresence = {
    start: function () { if (!started) { started = true; connect(); } },
    stop: function () { started = false; clearTimeout(retry); var s = socket; socket = null; if (s) s.close(); render([]); },
  };
})();
