// Who's on this page: the admin page's admins, or an app's dev page's
// owners and developers. Each window holds a WebSocket to the page; the
// top bar's #presence shows a pill a person, in their colour while they're
// here, with how many windows they have open, greyed when none of them had
// a key, a click, or a scroll in a few minutes. The page calls
// hiPresence.start() once it's signed in.
(function () {
  "use strict";
  var IDLE_AFTER = 3 * 60 * 1000;
  var socket = null, started = false, retry = 0, wait = 1000, idle = false, lastInput = Date.now();

  function box() { return document.getElementById("presence"); }

  // Dark words on a light colour, light ones on a dark colour.
  function ink(color) {
    var n = parseInt(color.slice(1), 16);
    var r = (n >> 16) & 255, g = (n >> 8) & 255, b = n & 255;
    return 0.299 * r + 0.587 * g + 0.114 * b > 150 ? "#111" : "#fff";
  }

  function render(people) {
    var el = box();
    if (!el) return;
    el.textContent = "";
    var shown = people.slice(0, 6);
    shown.forEach(function (p) {
      var pill = document.createElement("span");
      pill.className = "presence-pill" + (p.idle ? " idle" : "") + (p.you ? " you" : "");
      pill.style.background = p.color;
      pill.style.color = ink(p.color);
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

  function send() {
    if (socket && socket.readyState === 1) socket.send(JSON.stringify({ idle: idle }));
  }

  function connect() {
    clearTimeout(retry);
    if (socket && socket.readyState <= 1) return;
    var url = (location.protocol === "https:" ? "wss://" : "ws://") + location.host + location.pathname.replace(/[^/]*$/, "") + "api/presence";
    var opened = socket = new WebSocket(url);
    opened.onopen = function () { wait = 1000; send(); };
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
  // idle after IDLE_AFTER without any.
  function input() {
    lastInput = Date.now();
    if (idle) { idle = false; send(); }
  }
  ["pointerdown", "keydown", "wheel", "touchstart", "focus"].forEach(function (kind) {
    window.addEventListener(kind, input, { passive: true, capture: true });
  });
  var moved = 0;
  window.addEventListener("pointermove", function () {
    if (Date.now() - moved > 5000) { moved = Date.now(); input(); }
  }, { passive: true });
  setInterval(function () {
    var now = Date.now() - lastInput > IDLE_AFTER;
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
