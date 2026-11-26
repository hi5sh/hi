// The hi app's dev page (team_devpage.go): three tabs, the app's tasks
// (tickets, in hi's code), herdr with the workspaces, and the system.
(function () {
  "use strict";
  var $ = function (id) { return document.getElementById(id); };
  var state = null;
  var releasing = 0; // the version being released, while it is
  var pending = {}; // what's being done, by its button's busy key
  var tickets = null;

  function el(tag, attrs, children) {
    var node = document.createElement(tag);
    for (var key in attrs || {}) {
      if (key === "text") node.textContent = attrs[key];
      else if (key.slice(0, 2) === "on") node.addEventListener(key.slice(2), attrs[key]);
      else if (key === "busy") { node.dataset.busy = attrs[key]; if (pending[attrs[key]]) { node.disabled = true; node.classList.add("busy"); } }
      else if (attrs[key] != null) node.setAttribute(key, attrs[key]);
    }
    (children || []).forEach(function (child) { if (child) node.appendChild(child); });
    return node;
  }

  // working spins the buttons that started something until it's done; a
  // button drawn again meanwhile, as the page refreshes, spins too.
  function working(busy, promise) {
    if (!busy) return promise;
    function mark(on) {
      document.querySelectorAll("[data-busy]").forEach(function (button) {
        if (button.dataset.busy !== busy) return;
        button.disabled = on;
        button.classList.toggle("busy", on);
      });
    }
    pending[busy] = true;
    mark(true);
    return promise.finally(function () { delete pending[busy]; mark(false); });
  }

  // toast says something for a few seconds; stay keeps it until the next.
  function toast(text, stay) {
    var box = $("toast");
    box.textContent = text;
    box.hidden = false;
    clearTimeout(toast.timer);
    if (!stay) toast.timer = setTimeout(function () { box.hidden = true; }, 5000);
  }

  function api(method, path, body) {
    return fetch("api/" + path, {
      method: method,
      headers: body ? { "Content-Type": "application/json" } : {},
      body: body ? JSON.stringify(body) : undefined,
      credentials: "same-origin",
    }).then(function (response) {
      return response.json().catch(function () { return {}; }).then(function (data) {
        if (response.status === 401) lock(data.error || "Only the owners of the app can open this page.");
        if (!response.ok) {
          var error = new Error(data.error || response.statusText);
          error.data = data; // the rest of the answer, as a conflict's files
          throw error;
        }
        return data;
      });
    });
  }

  function lock(why) {
    $("locked-why").textContent = why;
    $("locked").hidden = false;
  }

  // Signing in by email, on the locked page (hi_people.md): the email's
  // link comes back here signed in, or its code is typed below.
  var emailRequest = "", emailVia = "email";
  function emailPost(path, body) {
    return fetch("api/" + path, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body), credentials: "same-origin" })
      .then(function (response) {
        return response.json().catch(function () { return {}; }).then(function (data) {
          if (!response.ok) throw new Error(data.error || response.statusText);
          return data;
        });
      });
  }
  $("email-form").addEventListener("submit", function (event) {
    event.preventDefault();
    var address = $("email-address").value.trim();
    if (!/^[^@\s]+@[^@\s]+\.[^@\s]+$/.test(address)) { $("email-said").textContent = "Enter your email address."; $("email-address").focus(); return; }
    var via = event.submitter && event.submitter.value === "slack" ? "slack" : "email";
    var buttons = $("email-form").querySelectorAll("button");
    buttons.forEach(function (b) { b.disabled = true; });
    emailPost("email", { email: address, via: via }).then(function (answer) {
      emailRequest = answer.request;
      emailVia = via;
      $("email-code-label").textContent = via === "slack" ? "The six digits in the Slack DM" : "The six digits in the email";
      $("email-said").textContent = answer.text;
      $("code-form").hidden = false;
      $("email-code").focus();
    }).catch(function (e) { $("email-said").textContent = e.message; }).finally(function () { buttons.forEach(function (b) { b.disabled = false; }); });
  });
  $("code-form").addEventListener("submit", function (event) {
    event.preventDefault();
    var code = $("email-code").value.replace(/\s/g, "");
    if (!/^[0-9]{6}$/.test(code)) { $("email-said").textContent = "Enter the six digits from the " + (emailVia === "slack" ? "Slack DM." : "email."); $("email-code").focus(); return; }
    emailPost("email/code", { request: emailRequest, code: code }).then(function (answer) {
      location.href = answer.go;
    }).catch(function (e) { $("email-said").textContent = e.message; });
  });

  // ---- the terminal
  var term = new Terminal({
    cursorBlink: true,
    fontSize: window.innerWidth < 600 ? 11 : 13,
    fontFamily: 'ui-monospace, "SF Mono", Menlo, "DejaVu Sans Mono", monospace',
    // Unicode 11's widths and the search's highlights are xterm's proposed API.
    allowProposedApi: true,
    // The page's theme's colours (theme.js).
    theme: window.hiTheme ? window.hiTheme.terminal() : undefined,
  });
  var fit = new FitAddon.FitAddon();
  term.loadAddon(fit);
  term.open($("term"));
  // A new theme, the terminal's colours with it.
  if (window.hiTheme) window.hiTheme.onChange(function () { term.options.theme = window.hiTheme.terminal(); });
  // xterm's add-ons (third_party/xterm): the GPU draws, which keeps up
  // with Claude Code's full-screen redraws, back to the DOM's drawing if
  // the browser loses its WebGL; Unicode 11's widths, for emoji and CJK;
  // links that open in a tab; iTerm's inline pictures (timg -p i); and
  // search in the scrollback, Ctrl+F or ⌘F in the terminal.
  var termSearch = null;
  if (window.Unicode11Addon) {
    term.loadAddon(new Unicode11Addon.Unicode11Addon());
    term.unicode.activeVersion = "11";
  }
  if (window.WebLinksAddon) {
    term.loadAddon(new WebLinksAddon.WebLinksAddon(function (event, uri) {
      if (/^https?:\/\//i.test(uri)) window.open(uri, "_blank", "noopener,noreferrer");
    }));
  }
  if (window.ImageAddon) {
    try { term.loadAddon(new ImageAddon.ImageAddon({ sixelSupport: false, iipSupport: true, storageLimit: 64, pixelLimit: 8388608 })); } catch (e) { /* no pictures */ }
  }
  if (window.SearchAddon) {
    termSearch = new SearchAddon.SearchAddon();
    term.loadAddon(termSearch);
  }
  var termGPU = false;
  if (window.WebglAddon) {
    try {
      var webgl = new WebglAddon.WebglAddon();
      webgl.onContextLoss(function () { webgl.dispose(); termGPU = false; });
      term.loadAddon(webgl);
      termGPU = true;
    } catch (e) { /* no WebGL here: the DOM draws */ }
  }
  $("term").dataset.renderer = termGPU ? "webgl" : "dom";
  var socket = null;
  var encoder = new TextEncoder();

  // fitTerminal sizes the terminal to its panel, and herdr's panes with it.
  // Hidden, behind the chat or another tab, it takes the size the
  // panel would have, not xterm's 80×24: Claude Code draws a question to its
  // pane, and one too small to hold it can't be read for the chat.
  function fitTerminal() {
    if (document.querySelector(".term-panel").offsetParent) {
      try { fit.fit(); } catch (e) { return; }
    } else {
      var size = hiddenTermSize();
      if (size && (size.cols !== term.cols || size.rows !== term.rows)) term.resize(size.cols, size.rows);
    }
    if (socket && socket.readyState === 1) socket.send(JSON.stringify({ cols: term.cols, rows: term.rows }));
  }
  function hiddenTermSize() {
    var shown = [$("chat"), $("editor")].filter(function (other) { return other.offsetParent; })[0];
    // Another tab: the window, less the page's tabs, the bar and the workspaces.
    var width = shown ? shown.clientWidth : window.innerWidth - (window.innerWidth < 760 ? 24 : 310);
    var height = shown ? shown.clientHeight : window.innerHeight - 110;
    var style = getComputedStyle($("term"));
    width -= (parseFloat(style.left) || 0) + (parseFloat(style.right) || 0) + ((term._core.viewport || {}).scrollBarWidth || 15);
    height -= (parseFloat(style.top) || 0) + (parseFloat(style.bottom) || 0);
    var cell = termCell();
    if (!cell.width || !cell.height) return null;
    return { cols: Math.max(40, Math.floor(width / cell.width)), rows: Math.max(16, Math.floor(height / cell.height)) };
  }
  // termCell is a character's size: xterm's, once it has drawn, else measured.
  function termCell() {
    var dims = term._core && term._core._renderService && term._core._renderService.dimensions;
    if (dims && dims.css.cell.width) return dims.css.cell;
    var probe = el("span", { text: "W".repeat(40) });
    probe.style.cssText = "position:absolute;visibility:hidden;white-space:pre;line-height:normal;font-family:" + term.options.fontFamily.replace(/"/g, "'") + ";font-size:" + term.options.fontSize + "px";
    document.body.appendChild(probe);
    var rect = probe.getBoundingClientRect();
    probe.remove();
    return { width: rect.width / 40, height: Math.ceil(rect.height) };
  }
  [".term-panel", "#chat", "#editor"].forEach(function (panel) { new ResizeObserver(fitTerminal).observe(document.querySelector(panel)); });
  window.addEventListener("resize", fitTerminal);
  // An on-screen keyboard covers the page's bottom on iOS: the page keeps
  // to what is visible, so the keys and the terminal's last line sit just
  // above the keyboard.
  if (window.visualViewport) {
    var fitView = function () {
      var height = Math.round(window.visualViewport.height) + "px";
      document.documentElement.style.height = height;
      document.body.style.height = height;
      window.scrollTo(0, 0);
    };
    window.visualViewport.addEventListener("resize", fitView);
    fitView();
  }

  // A terminal that closes connects again every second by itself, until a
  // try works; Connect again tries at once. A locked page, or a Vibe Dev's,
  // has no terminal to go back to.
  var termRetry = 0;
  function connect() {
    clearTimeout(termRetry);
    if (socket && socket.readyState <= 1) return;
    fitTerminal();
    var url = (location.protocol === "https:" ? "wss://" : "ws://") + location.host + location.pathname.replace(/[^/]*$/, "") +
      "api/term?cols=" + term.cols + "&rows=" + term.rows;
    var opened = socket = new WebSocket(url);
    socket.binaryType = "arraybuffer";
    socket.onopen = function () { $("term-ended").hidden = true; term.focus(); fitTerminal(); };
    socket.onmessage = function (event) {
      term.write(typeof event.data === "string" ? event.data : new Uint8Array(event.data));
    };
    socket.onclose = function (event) {
      if (opened !== socket) return;
      $("term-why").textContent = (event.reason ? "The terminal closed: " + event.reason + "." : "The terminal closed.") + " hi connects again…";
      $("term-ended").hidden = false;
      if ($("locked").hidden) termRetry = setTimeout(connect, 1000);
    };
  }
  function sendText(data) { if (data && socket && socket.readyState === 1) socket.send(encoder.encode(data)); }

  // ---- the keys a phone's keyboard lacks
  var ctrl = false;
  function setCtrl(on) {
    ctrl = on;
    document.querySelector('[data-key="ctrl"]').setAttribute("aria-pressed", on ? "true" : "false");
  }
  // With Ctrl on, the next key typed is sent with it: a letter as its
  // control code (Ctrl+R is \x12).
  function withCtrl(data) {
    if (!ctrl || data.length !== 1) return data;
    setCtrl(false);
    var code = data.toUpperCase().charCodeAt(0);
    if (code >= 64 && code <= 95) return String.fromCharCode(code - 64);
    if (data === " ") return "\x00";
    return data;
  }
  function arrow(letter) {
    // In an app that asked for application cursor keys (herdr, vim), the
    // arrows are ESC O x; otherwise ESC [ x.
    return (term.modes && term.modes.applicationCursorKeysMode ? "\x1bO" : "\x1b[") + letter;
  }
  var keys = {
    esc: "\x1b", tab: "\t", shifttab: "\x1b[Z", enter: "\r", space: " ", backspace: "\x7f", del: "\x1b[3~",
    ctrlc: "\x03", ctrld: "\x04",
    up: function () { return arrow("A"); }, down: function () { return arrow("B"); },
    right: function () { return arrow("C"); }, left: function () { return arrow("D"); },
  };
  var keyBar = $("keys");
  // A button mustn't take the focus: the keyboard would close.
  keyBar.addEventListener("pointerdown", function (event) { if (event.target.closest("button")) event.preventDefault(); });
  keyBar.addEventListener("click", function (event) {
    var button = event.target.closest("button");
    if (!button) return;
    var key = button.dataset.key;
    if (key === "keyboard") { term.focus(); return; }
    if (key === "ctrl") { setCtrl(!ctrl); term.focus(); return; }
    if (key === "paste") {
      if (!navigator.clipboard || !navigator.clipboard.readText) { toast("This browser does not let the page read the clipboard. Do a long press in the terminal to paste."); return; }
      navigator.clipboard.readText().then(function (text) {
        if (!text) return;
        // As a paste, so the app takes it as one, not as typing.
        sendText(term.modes && term.modes.bracketedPasteMode ? "\x1b[200~" + text + "\x1b[201~" : text);
        term.focus();
      }).catch(function () { toast("The browser did not let the page read the clipboard."); });
      return;
    }
    var data = typeof keys[key] === "function" ? keys[key]() : keys[key];
    if (data) {
      setCtrl(false);
      sendText(data);
    }
    term.focus();
  });

  // Ctrl+V pastes, as in a desktop terminal: the browser's paste, which
  // can carry a picture, not ^V for the box, whose clipboard is empty.
  // Ctrl+Shift+F, or ⌘F on a Mac, finds in the scrollback: Ctrl+F alone
  // is the shell's and the agents' (a character forward).
  term.attachCustomKeyEventHandler(function (event) {
    var v = event.key === "v" || event.key === "V";
    if (event.type === "keydown" && termSearch && (event.key === "f" || event.key === "F") &&
        ((event.ctrlKey && event.shiftKey && !event.metaKey) || (event.metaKey && !event.ctrlKey))) {
      event.preventDefault();
      openTermFind();
      return false;
    }
    return !(event.type === "keydown" && v && event.ctrlKey && !event.altKey && !event.metaKey);
  });
  // ---- Find in the terminal: xterm's search add-on, its matches lit.
  var termFindLook = { matchBackground: "#4d4430", matchOverviewRuler: "#c09664", activeMatchBackground: "#7a6233", activeMatchColorOverviewRuler: "#db932e" };
  function openTermFind() {
    if (!termSearch) return;
    $("term-find").hidden = false;
    $("term-find-open").hidden = true;
    var selected = term.getSelection();
    if (selected && selected.indexOf("\n") < 0) $("term-find-text").value = selected;
    $("term-find-text").focus();
    $("term-find-text").select();
    if ($("term-find-text").value) findInTerm(true, true);
  }
  function closeTermFind() {
    $("term-find").hidden = true;
    $("term-find-open").hidden = false;
    $("term-find-count").textContent = "";
    if (termSearch) termSearch.clearDecorations();
    term.clearSelection();
    term.focus();
  }
  function findInTerm(forward, incremental) {
    var text = $("term-find-text").value;
    if (!text) { termSearch.clearDecorations(); $("term-find-count").textContent = ""; return; }
    var options = { decorations: termFindLook, incremental: !!incremental };
    var found = forward ? termSearch.findNext(text, options) : termSearch.findPrevious(text, options);
    if (!found) $("term-find-count").textContent = "None";
  }
  if (termSearch) {
    termSearch.onDidChangeResults(function (result) {
      if (!result) return;
      $("term-find-count").textContent = result.resultCount ? (result.resultIndex + 1) + " of " + result.resultCount : "None";
    });
  }
  $("term-find").addEventListener("submit", function (event) { event.preventDefault(); findInTerm(true); });
  $("term-find-text").addEventListener("input", function () { findInTerm(true, true); });
  $("term-find-text").addEventListener("keydown", function (event) {
    if (event.key === "Escape") { event.preventDefault(); closeTermFind(); }
    else if (event.key === "Enter" && event.shiftKey) { event.preventDefault(); findInTerm(false); }
  });
  $("term-find-prev").addEventListener("click", function () { findInTerm(false); });
  $("term-find-close").addEventListener("click", closeTermFind);
  $("term-find-open").addEventListener("click", openTermFind);
  $("term-find-open").hidden = !termSearch;
  // A pasted picture goes into the box, and its path is pasted, which
  // Claude Code takes as the picture. Text pastes as before.
  $("term").addEventListener("paste", function (event) {
    var images = Array.prototype.filter.call((event.clipboardData && event.clipboardData.files) || [], function (file) {
      return /^image\//.test(file.type);
    });
    if (!images.length) return;
    event.preventDefault();
    event.stopPropagation();
    Promise.all(images.map(function (file) {
      return new Promise(function (resolve, reject) {
        var reader = new FileReader();
        reader.onload = function () { resolve({ name: "pasted-" + Math.random().toString(16).slice(2, 8) + "." + (file.type.split("/")[1] || "png").replace("jpeg", "jpg"), data: String(reader.result).replace(/^[^,]*,/, "") }); };
        reader.onerror = function () { reject(new Error("cannot read the picture")); };
        reader.readAsDataURL(file);
      });
    })).then(function (files) {
      return api("POST", "term/files", { files: files });
    }).then(function (result) {
      var text = result.paths.join(" ") + " ";
      sendText(term.modes && term.modes.bracketedPasteMode ? "\x1b[200~" + text + "\x1b[201~" : text);
      term.focus();
    }).catch(function (error) { toast(error.message); });
  }, true);

  term.onData(function (data) { sendText(withCtrl(data)); });
  term.onBinary(function (data) {
    if (!socket || socket.readyState !== 1) return;
    var bytes = new Uint8Array(data.length);
    for (var i = 0; i < data.length; i++) bytes[i] = data.charCodeAt(i) & 255;
    socket.send(bytes);
  });
  $("term-again").addEventListener("click", connect);

  // ---- the left: tickets and workspaces
  function spaceFor(ticket) {
    if (!state || !state.workspaces) return null;
    var prefix = ticket.number + "-";
    return state.workspaces.filter(function (space) {
      return space.branch === String(ticket.number) || (space.branch || "").indexOf(prefix) === 0;
    })[0] || null;
  }

  function statusDot(status, space) {
    var line = space ? hookLine(space) : "";
    return el("span", { class: "dot " + (status || ""), title: (status || "no agent") + (line ? ": " + line : "") });
  }
  // hookLine is what an agent's hooks add to its state (app_devhooks.go):
  // what it waits for, or the tool it runs, and for how long.
  // noteLine is the agent's own line on where its work stands (hi dev
  // note, app_devnote.go), with how long ago.
  function noteLine(space, tag) {
    if (!space.note) return null;
    var when = space.note_at ? ago(space.note_at) : "";
    return el(tag || "div", { class: "small space-note", title: "Note from the agent (hi dev note)" + (when ? ", " + when : ""), text: "“" + space.note + "”" + (when ? " · " + when : "") });
  }
  function hookLine(agent) {
    var what = agent.status === "blocked" && agent.waiting ? "Waits for " + agent.waiting
      : agent.status === "working" && agent.tool ? "Running " + agent.tool : "";
    if (!what) return "";
    var seconds = agent.since ? Math.round((Date.now() - Date.parse(agent.since)) / 1000) : -1;
    if (seconds >= 60) what += ", for " + (seconds < 3600 ? Math.round(seconds / 60) + " min" : Math.round(seconds / 3600) + " h");
    return what;
  }

  function focus(space) {
    // The chat follows the workspace you open, as the terminal does.
    chooseChatSpace(space.id);
    working("focus:" + space.id, api("POST", "focus", { workspace: space.id })).then(function () {
      closeOver();
      if (chat.view === "terminal") term.focus();
    }).catch(function (error) { toast(error.message); });
  }

  // ---- Start: which agent, then a branch, a worktree, and a workspace
  var starting = null;
  function startDialog(ticket) {
    starting = ticket;
    $("start-task").textContent = "#" + ticket.number + " " + ticket.title;
    $("start-error").textContent = "";
    $("start-go").disabled = false;
    $("start-go").textContent = "Start →";
    var last = "claude";
    try { last = localStorage.getItem("hi-dev-agent") || "claude"; } catch (e) { /* private window */ }
    if (last === "agy" && !(state && state.agy)) last = "claude"; // the app's agy went
    document.querySelectorAll("#start-form input[name=agent]").forEach(function (input) {
      input.checked = input.value === (last === "shell" ? "" : last);
    });
    $("start-dialog").showModal();
    $("start-go").focus();
  }
  $("start-cancel").addEventListener("click", function () { $("start-dialog").close(); });
  $("start-form").addEventListener("submit", function (event) {
    event.preventDefault();
    var agent = (document.querySelector("#start-form input[name=agent]:checked") || {}).value || "";
    try { localStorage.setItem("hi-dev-agent", agent || "shell"); } catch (e) { /* private window */ }
    var ticket = starting;
    $("start-go").disabled = true;
    $("start-go").textContent = "Making the worktree…";
    api("POST", "start", { ticket: ticket.number, agent: agent }).then(function (answer) {
      $("start-dialog").close();
      toast((answer.warning ? answer.warning + " " : "") + "Started " + answer.branch + (agent ? " with " + agent : "") + ".");
      closeOver();
      // Its chat, as Start something new opens it.
      refresh().then(function () {
        renderSideTasks();
        if (answer.workspace && agent) chooseChatSpace(answer.workspace);
      });
    }).catch(function (error) {
      $("start-error").textContent = error.message;
      $("start-go").disabled = false;
      $("start-go").textContent = "Start →";
    });
  });

  // ---- Done and Reopen: a task closed when it's done, whatever its state
  function taskDone(ticket, space) {
    var name = "#" + ticket.number;
    if (space && space.path && space.checked && !space.ahead && !space.dirty && !space.out && !space.shipped) {
      // Nothing of its own in its workspace: it goes with the task.
      ask("Close " + name + "? Its workspace has no work, so hi also removes the workspace and its agent. You can add a note to the task:", "why it is done (optional)").then(function (note) {
        if (note === null) return;
        working("finish:" + space.id, api("POST", "finish", { workspace: space.id, discard: false })).then(function () {
          refresh();
          setTaskState(ticket, true, note);
        }).catch(function (error) { toast(error.message); });
      });
      return;
    }
    if (space && space.path && space.checked && space.shipped && !space.ahead && !space.dirty) {
      // Its work is in dev: Finish, which closes the task too.
      ask("Done with " + name + "? Its work is on the preview. hi removes its worktree, workspace, agent, and branch, and closes the task.").then(function (answer) {
        if (answer === null) return;
        working("finish:" + space.id, api("POST", "finish", { workspace: space.id, discard: false })).then(function (r) { toast(r.text); refresh(); refreshTickets(true); }).catch(function (error) { toast(error.message); });
      });
      return;
    }
    var left = [];
    if (space && space.ahead) left.push("work not on the preview yet");
    if (space && space.dirty) left.push("unsaved changes");
    var question = left.length
      ? "Close " + name + "? Its workspace still has " + left.join(" and ") + ". They stay in the workspace. Use Add to Preview or Discard in Workspaces. You can add a note to the task:"
      : "Close " + name + "? You can add a note to the task:";
    ask(question, "why it is done (optional)").then(function (note) {
      if (note === null) return;
      setTaskState(ticket, true, note);
    });
  }
  function setTaskState(ticket, close, note) {
    api("POST", "ticket/state", { number: ticket.number, close: close, note: note || "" }).then(function (r) {
      toast(r.text);
      refreshTickets(true);
      if ($("view-dialog").open) openTicket(ticket.number);
      if (stask.ticket && stask.ticket.number === ticket.number) showSideTask(ticket.number, true);
    }).catch(function (error) { toast(error.message); });
  }

  // ---- the tasks: a table, as in Orca's
  var showing = "open";
  function ago(at) {
    if (!at) return "";
    var seconds = (Date.now() - new Date(at).getTime()) / 1000;
    if (seconds < 60) return "just now";
    var steps = [[60, "minute"], [3600, "hour"], [86400, "day"], [604800, "week"]];
    for (var i = steps.length - 1; i >= 0; i--) {
      if (seconds >= steps[i][0]) {
        var n = Math.floor(seconds / steps[i][0]);
        if (steps[i][1] === "day" && n === 1) return "yesterday";
        if (steps[i][1] === "week" && n > 8) return new Date(at).toLocaleDateString([], { dateStyle: "medium" });
        return n + " " + steps[i][1] + (n === 1 ? "" : "s") + " ago";
      }
    }
    return "";
  }
  // clockTime is a time of day, with its day when that isn't today.
  function clockTime(at) {
    var date = new Date(at), now = new Date();
    var time = date.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
    if (date.toDateString() === now.toDateString()) return time;
    var yesterday = new Date(now.getFullYear(), now.getMonth(), now.getDate() - 1);
    if (date.toDateString() === yesterday.toDateString()) return "yesterday " + time;
    return date.toLocaleDateString([], { day: "numeric", month: "short" }) + " " + time;
  }
  // Open or Closed, in the panel's Tasks.
  document.querySelectorAll("#stask-show button").forEach(function (button) {
    button.addEventListener("click", function () {
      showing = button.dataset.show;
      document.querySelectorAll("#stask-show button").forEach(function (other) { other.setAttribute("aria-pressed", other === button ? "true" : "false"); });
      $("stask-search").placeholder = showing === "closed" ? "Search the closed tasks" : "Search by words, #number, or who made it";
      renderSideTasks();
      refreshTickets(false);
    });
  });

  function renderWorkspaces() {
    renderChatSpaces();
    renderEditorBar();
    var list = $("workspaces");
    list.textContent = "";
    badge("spaces-count", workSpaces().length, " workspaces");
    if (!workSpaces().length) list.appendChild(el("li", { class: "muted small", text: "No work yet. Start a task in Tasks, or use + New in the chat." }));
    $("box").textContent = state.box === "running" ? "" : state.box;
    workSpaces().forEach(function (space) {
      // A piece of work, by its task's title: where it is on the way to
      // production, and its one next step (Add to Preview, or Done).
      var stage = workStage(space);
      var number = ticketOf(space);
      var doing = hookLine(space);
      var what = el("div", { class: "what" }, [
        el("div", { class: "work-title", title: space.branch || space.label, text: workName(space) }),
        el("div", { class: "muted small", text: [number ? "#" + number : "", space.agent ? agentName(space.agent) : "", space.by ? "started by " + space.by : ""].filter(Boolean).join(" · ") }),
        doing ? el("div", { class: "small " + (space.status === "blocked" ? "warn" : "muted"), text: doing }) : null,
        noteLine(space),
        stage.text ? el("div", { class: "small " + stage.tone, text: stage.text }) : null,
      ]);
      var actions = [el("button", { text: "Open", busy: "focus:" + space.id, onclick: function () { focus(space); } })];
      if (stage.add) actions.push(addButton(space));
      if (stage.done) actions.push(finishButton(space, false));
      var more = [];
      if (stage.add && space.ahead > 0) more.push({ text: "Preview only this change", pro: true, title: "Show only this change on the preview, without the rest of the next version. This adds nothing.", run: function () { previewOnly(space); } });
      if (space.path && space.checked && !stage.done) more.push({ text: "Discard…", danger: true, run: function () { finishButton(space, true).click(); } });
      if (more.length) actions.push(moreMenu(more));
      actions.push(editorSplit(space));
      list.appendChild(el("li", { class: "ticket" }, [statusDot(space.status, space), what, el("div", { class: "actions" }, actions)]));
    });
    // The project's own clone, in your own editor, when no workspace
    // above is it: before any work, too.
    if (state.ssh && !workSpaces().some(function (space) { return !space.path; })) {
      list.appendChild(el("li", { class: "ticket" }, [statusDot("", null),
        el("div", { class: "what" }, [el("div", { text: "The main clone of the project" }), el("div", { class: "muted small", text: state.ssh.project })]),
        el("div", { class: "actions" }, [editorSplit(null)])]));
    }
    renderInbox();
  }

  // ---- Needs you (app_devneeds.go): the workspaces whose agent waits
  // for someone, and those done since you last looked at them, in the top
  // bar; the browser's notification as each comes, while the page is
  // open; and the installed app's badge, the count waiting. What you
  // looked at last is this browser's.
  var inbox = { seen: {}, doneAt: {}, shown: null };
  try { inbox.seen = JSON.parse(localStorage.getItem("hi-dev-seen") || "{}") || {}; } catch (e) { /* private window */ }
  function spaceKey(space) { return space.branch || space.label; }
  function keepSeen() {
    try { localStorage.setItem("hi-dev-seen", JSON.stringify(inbox.seen)); } catch (e) { /* private window */ }
  }
  // inboxItems are what needs you now: waiting first, then done.
  function inboxItems() {
    var now = Date.now(), items = [], changed = false;
    workSpaces().forEach(function (space) {
      var key = spaceKey(space);
      if (!(key in inbox.seen)) {
        // A workspace this browser never saw: what it did before isn't news,
        // unless herdr says it's done and nobody looked.
        inbox.seen[key] = space.status === "done" ? 0 : now;
        changed = true;
      }
      if (space.id === chat.space && !document.hidden) {
        inbox.seen[key] = now; // you're looking at it
        changed = true;
      }
      var doneAt = 0;
      if (space.status === "done") doneAt = inbox.doneAt[key] = inbox.doneAt[key] || (space.since ? Date.parse(space.since) : now);
      else if (space.status === "idle" && space.since) doneAt = Date.parse(space.since);
      if (space.status !== "done") delete inbox.doneAt[key];
      if (space.status === "blocked") {
        items.push({ space: space, waits: true, id: "w " + key + " " + (space.since || ""),
          text: space.waiting ? "Waits for " + space.waiting : "Waits for an answer" });
      } else if (doneAt && doneAt > inbox.seen[key]) {
        items.push({ space: space, waits: false, id: "d " + key + " " + doneAt, at: doneAt, text: "Done" });
      }
    });
    if (changed) keepSeen();
    // A site an agent asked the dev box to reach: the owners' to answer.
    if (!developer()) {
      (((state && state.access) || {}).asks || []).forEach(function (ask) {
        items.unshift({ site: ask, space: siteSpace(ask), waits: true, id: "s " + ask.host + " " + (ask.at || ""),
          text: "Asks to reach " + ask.host + (ask.why ? ": " + ask.why : "") });
      });
    }
    return items.filter(function (item) { return item.waits; }).concat(items.filter(function (item) { return !item.waits; }));
  }
  // siteSpace is the workspace of the work that asked for a site, if any.
  function siteSpace(ask) {
    return ask.work ? workSpaces().filter(function (space) { return space.branch === ask.work; })[0] || null : null;
  }
  function renderInbox() {
    if (!state) return;
    var items = inboxItems();
    var waiting = items.filter(function (item) { return item.waits; }).length;
    var count = $("inbox-count");
    count.hidden = !items.length;
    count.textContent = items.length;
    count.classList.toggle("waits", waiting > 0);
    $("inbox-btn").title = items.length ? (waiting ? waiting + " waiting for a person" : "") + (waiting && items.length > waiting ? ", " : "") + (items.length > waiting ? items.length - waiting + " finished since you last looked" : "") : "Needs you: agents that wait for a person, and work that finished since you last looked";
    if (navigator.setAppBadge) {
      try { (waiting ? navigator.setAppBadge(waiting) : navigator.clearAppBadge()).catch(function () {}); } catch (e) { /* not installed */ }
    }
    var list = $("inbox-list");
    list.textContent = "";
    if (!items.length) list.appendChild(el("li", { class: "muted small inbox-empty", text: "Nothing waits for you. This list shows agents that ask a question, a permission, or a site. It also shows work that is done." }));
    items.forEach(function (item) {
      if (item.site) {
        list.appendChild(inboxSite(item));
        return;
      }
      var space = item.space, line = item.waits ? hookLine(space) || item.text : item.text;
      list.appendChild(el("li", {}, [el("button", { type: "button", onclick: function () { openFromInbox(space); } }, [
        statusDot(space.status, space),
        el("span", { class: "inbox-what" }, [el("strong", { text: spaceKey(space) + (space.agent ? " · " + space.agent : "") }),
          el("span", { class: "small " + (item.waits ? "warn" : "muted"), text: line }),
          noteLine(space, "span"),
          space.title ? el("span", { class: "muted small", text: space.title }) : null,
          space.by ? el("span", { class: "muted small", text: "Started by " + space.by }) : null]),
      ])]));
    });
    notifyInbox(items);
    renderInboxNotify();
  }
  // inboxSite is a site asked for, answered from the bell; the rest of it
  // opens the work's chat, or Keys, Sites, for work without a workspace.
  function inboxSite(item) {
    var ask = item.site, space = item.space;
    return el("li", { class: "inbox-site" }, [
      el("button", { type: "button", onclick: function () { openSiteFromInbox(ask, space); } }, [
        el("span", { class: "dot blocked" }),
        el("span", { class: "inbox-what" }, [el("strong", { text: ask.host }),
          el("span", { class: "small warn", text: "Asked" + (ask.work ? " for " + ask.work : "") + (ask.why ? ": " + ask.why : "") }),
          el("span", { class: "muted small", text: "The dev box cannot reach it until an owner allows it" })]),
      ]),
      el("div", { class: "actions inbox-actions" }, [
        el("button", { class: "primary small", text: "Allow", onclick: function () { siteAct(ask.host, true); } }),
        el("button", { class: "small", text: "Say no", onclick: function () { siteAct(ask.host, false); } }),
      ]),
    ]);
  }
  function openSiteFromInbox(ask, space) {
    if (space) return openFromInbox(space);
    closeInbox();
    revealKeys("sites");
  }
  // notifyInbox tells of what's new since the last look, through the
  // browser, unless you're on that workspace's chat already.
  function notifyInbox(items) {
    var before = inbox.shown;
    inbox.shown = {};
    items.forEach(function (item) { inbox.shown[item.id] = true; });
    if (!before || !window.Notification || Notification.permission !== "granted") return;
    items.forEach(function (item) {
      if (before[item.id] || (!document.hidden && item.space && item.space.id === chat.space)) return;
      if (item.site) {
        try {
          var asked = new Notification((item.site.work || "An agent") + " asks to reach " + item.site.host, { body: item.site.why || (state.team + "'s dev box"), tag: "hi-dev-" + state.team + "-site-" + item.site.host });
          asked.onclick = function () { window.focus(); asked.close(); openSiteFromInbox(item.site, item.space); };
        } catch (e) { /* a browser that only notifies from a service worker */ }
        return;
      }
      var name = spaceKey(item.space);
      var title = !item.waits ? name + " is done" : item.space.waiting === "a question" ? name + " asks a question" : name + " waits for " + (item.space.waiting || "an answer");
      try {
        var note = new Notification(title, { body: item.space.title || (state.team + "'s dev box"), tag: "hi-dev-" + state.team + "-" + name });
        note.onclick = function () { window.focus(); note.close(); openFromInbox(item.space); };
      } catch (e) { /* a browser that only notifies from a service worker */ }
    });
  }
  function renderInboxNotify() {
    var button = $("inbox-notify");
    button.hidden = !window.Notification;
    if (button.hidden) return;
    var permission = Notification.permission;
    button.textContent = permission === "granted" ? "Notifications on" : permission === "denied" ? "Notifications blocked: how to allow them" : "Notify me";
  }
  // The browser asks once; after a Block, or in a private window, or where
  // it never lets sites ask, only its own settings allow them again.
  $("inbox-notify").addEventListener("click", function () {
    var help = $("inbox-help");
    if (Notification.permission === "default") {
      var asked = Notification.requestPermission(function () { renderInboxNotify(); notifyHelp(); });
      if (asked && asked.then) asked.then(function () { renderInboxNotify(); notifyHelp(); });
      return;
    }
    if (!help.hidden) { help.hidden = true; return; }
    notifyHelp(true);
  });
  function notifyHelp(open) {
    var help = $("inbox-help");
    var permission = window.Notification ? Notification.permission : "";
    if (permission === "granted") {
      help.textContent = "This browser shows notifications while the page is open, also behind another window. To stop them, open the site settings next to the address bar.";
    } else if (permission === "denied") {
      help.textContent = "This browser blocks notifications from this site. This occurs in a private window, after you click Block, or if the browser never lets sites ask. " +
        "To allow them in Chrome or Edge, click the icon left of the address, then Notifications, Allow. In Firefox, click the same icon, then the notifications permission. " +
        "In Safari, open Settings, Websites, Notifications (and \"Allow websites to ask\"). Then reload the page.";
    } else return;
    if (open) help.hidden = false;
  }
  function openFromInbox(space) {
    closeInbox();
    setCodeView("chat");
    focus(space);
    inbox.seen[spaceKey(space)] = Date.now();
    keepSeen();
    renderInbox();
  }
  function closeInbox() {
    $("inbox").hidden = true;
    $("inbox-btn").setAttribute("aria-expanded", "false");
  }
  $("inbox-btn").addEventListener("click", function (event) {
    event.stopPropagation();
    var open = $("inbox").hidden;
    $("inbox").hidden = !open;
    $("inbox-btn").setAttribute("aria-expanded", open ? "true" : "false");
    if (open) renderInbox();
  });
  document.addEventListener("click", function (event) {
    if (!$("inbox").hidden && !event.target.closest(".inbox-wrap")) closeInbox();
  });
  document.addEventListener("keydown", function (event) {
    if (event.key === "Escape" && !$("inbox").hidden) { closeInbox(); $("inbox-btn").focus(); }
  });

  // ---- Your own editor (app_deveditor.go): a workspace in VS Code,
  // Cursor, or Zed by a link, in Orca by its steps, and in anything else
  // that works over SSH by the box's name and the folder, each with Copy.
  // An owner's and a Pro Dev's: a Vibe Dev has no SSH, and no state.ssh.
  var editors = [
    { id: "vscode", name: "VS Code", scheme: "vscode", note: "with Microsoft's Remote - SSH extension" },
    { id: "cursor", name: "Cursor", scheme: "cursor", note: "has Remote - SSH built in" },
    { id: "zed", name: "Zed", note: "has remote projects built in" },
    { id: "orca", name: "Orca", note: "agents and worktrees, with its own editor" },
  ];
  var editor = { space: null, leaving: 0 };
  function editorById(id) { return editors.filter(function (e) { return e.id === id; })[0]; }
  function editorPath(space) { return (space && space.path) || state.ssh.project; }
  function editorTarget() { return state.ssh.user + "@" + state.ssh.host; }
  // editorLink opens the folder in the editor, for those a link opens.
  function editorLink(e, path) {
    if (e.scheme) return e.scheme + "://vscode-remote/ssh-remote+" + editorTarget() + encodeURI(path);
    if (e.id === "zed") return "zed://ssh/" + editorTarget() + encodeURI(path);
    return "";
  }
  // editorBlocked says why no editor reaches the box yet, or "".
  function editorBlocked() {
    if (!state.ssh.host) return "The dev box is not on the app's NetBird network yet, so no editor can reach it. An admin of the machine must run: hi app dev " + state.team + " netbird";
    if (!state.ssh_key) return "Your SSH key is not in the dev box yet. Add it once, below. Then your editor signs in with it.";
    return "";
  }
  function keptEditor() {
    var id = "";
    try { id = localStorage.getItem("hi-dev-editor") || ""; } catch (e) { /* private window */ }
    return editorById(id);
  }
  function keepEditor(e) {
    try { localStorage.setItem("hi-dev-editor", e.id); } catch (err) { /* private window */ }
  }
  function spaceName(space) { return space && space.path ? space.branch || space.label : "the main clone of the project"; }
  // editorAnchor is a link that opens the folder in e: the browser asks
  // the computer's editor to, and the page stays.
  function editorAnchor(e, space, text, cls) {
    return el("a", { class: cls, href: editorLink(e, editorPath(space)), text: text,
      title: "Open " + spaceName(space) + " in " + e.name + ", over SSH", onclick: function () {
        editor.leaving = Date.now();
        keepEditor(e);
        if ($("editor-dialog").open) $("editor-dialog").close();
        toast("Opening " + spaceName(space) + " in " + e.name + ". The browser can ask you first. The first time, " + e.name + " asks if you trust the dev box.");
        renderEditorBar();
        renderWorkspaces();
      } });
  }
  // editorSplit is a workspace's button: straight into the editor last
  // used, with ▾ for the others; before one is, Editor ↗ shows them.
  function editorSplit(space) {
    if (!state.ssh) return null;
    var kept = keptEditor();
    var direct = kept && !editorBlocked() && editorLink(kept, "/") ? kept : null;
    var more = el("button", { type: "button", class: direct ? "editor-more" : "", text: direct ? "▾" : "Editor ↗",
      "aria-label": direct ? "Open " + spaceName(space) + " in another editor" : null,
      title: direct ? "Other editors, and how to set one up" : "Open " + spaceName(space) + " in your own editor, over SSH",
      onclick: function () { editorDialog(space); } });
    return el("span", { class: "editor-split" }, [direct ? editorAnchor(direct, space, direct.name + " ↗", "button") : null, more]);
  }
  function renderEditorBar() {
    var bar = $("editor-bar");
    // The chat's workspace, or the project's own clone before there's work.
    bar.hidden = !state || !state.ssh;
    if (bar.hidden) return;
    bar.textContent = "";
    var split = editorSplit(chatSpace());
    if (split) Array.prototype.slice.call(split.childNodes).forEach(function (node) { bar.appendChild(node); });
  }
  function editorDialog(space) {
    editor.space = space || null;
    var ssh = state.ssh, path = editorPath(space);
    $("editor-where").textContent = (space && space.path ? space.branch || space.label : "The main clone of the project") + ", in the dev box: " + path;
    var blocked = editorBlocked();
    var ready = $("editor-ready");
    ready.hidden = !blocked;
    ready.textContent = "";
    if (blocked) {
      ready.appendChild(el("p", { class: "small warn", text: blocked }));
      if (ssh.host && !state.ssh_key) ready.appendChild(el("button", { type: "button", class: "small", text: "Add my key", onclick: function () {
        $("editor-dialog").close();
        revealKeys("ssh");
        $("sshkey").focus();
      } }));
    }
    var list = $("editor-list");
    list.textContent = "";
    var kept = keptEditor();
    var host = ssh.host || "(waiting for NetBird)";
    editors.forEach(function (e) {
      var head = el("div", { class: "editor-head" }, [el("strong", { text: e.name }), el("span", { class: "muted small", text: e.note })]);
      var body = null;
      if (editorLink(e, path)) {
        body = blocked ? null : editorAnchor(e, space, "Open in " + e.name + " ↗", "button" + (kept && kept.id === e.id ? " primary" : ""));
      } else if (e.id === "orca") {
        body = editorSteps("In Orca, open Settings, SSH, Add Target. Pick " + ssh.alias + " from your SSH config (see Set up once, below), or type it. Then add the project " + ssh.project + " on it. Each workspace here is a worktree of the project. Orca hides worktrees that it did not make. To see them, click Non-Orca worktrees, Show.", [["Host", host], ["User", ssh.user], ["Project", ssh.project]]);
      }
      list.appendChild(el("li", {}, [head, body]));
    });
    var generic = $("editor-ssh");
    generic.textContent = "";
    generic.appendChild(editorSteps("", editorSSHRows(path)));
    $("editor-host").textContent = host;
    $("editor-key").textContent = state.ssh_key ? "Your SSH key is in the dev box: " + state.ssh_key + "." : "Your SSH key: paste your public key in Keys, SSH key. It is the .pub file, for example ~/.ssh/id_ed25519.pub.";
    $("editor-config").textContent = editorConfig();
    $("editor-config-copy").disabled = !ssh.host;
    var hostkey = $("editor-hostkey");
    hostkey.hidden = !ssh.host_key;
    hostkey.textContent = ssh.host_key ? "The first time, your editor asks if you trust the dev box. The fingerprint of its key is " + ssh.host_key + " (ED25519). Say yes only to this fingerprint." : "";
    $("editor-setup").open = !!blocked;
    if (!$("editor-dialog").open) $("editor-dialog").showModal();
  }
  // editorSteps are what to type in an editor without a link, each with
  // its own Copy.
  function editorSteps(text, rows) {
    return el("div", { class: "editor-steps" }, [text ? el("p", { class: "small", text: text }) : null,
      el("dl", { class: "editor-facts" }, [].concat.apply([], rows.map(function (row) {
        return [el("dt", { class: "muted small", text: row[0] }), el("dd", {}, [el("code", { text: row[1] }),
          el("button", { type: "button", class: "small link", text: "Copy", "aria-label": "Copy " + row[0], onclick: function () { copyText(row[1]); } })])];
      })))]);
  }
  // editorSSHRows are the dev box for anything else over SSH: a terminal,
  // JetBrains, another editor. One line to copy, then each part of it.
  function editorSSHRows(path) {
    var ssh = state.ssh, host = ssh.host || "(waiting for NetBird)";
    return [["ssh", ssh.host ? "ssh -t " + editorTarget() + " " + shellWord("cd " + shellWord(path) + " && exec bash -l") : host],
      ["Host", host], ["Port", "22"], ["User", ssh.user], ["Folder", path]];
  }
  // editorConfig is the dev box in ~/.ssh/config: under its own name, for
  // the editors that list those, and under its NetBird name, which the
  // links use, so a key named there counts for them too.
  function editorConfig() {
    var ssh = state.ssh, host = ssh.host || "<the NetBird name>";
    return "Host " + ssh.alias + " " + host + "\n" +
      "  HostName " + host + "\n" +
      "  User " + ssh.user + "\n" +
      "  # IdentityFile ~/.ssh/id_ed25519   your key, if ssh does not find it by itself\n";
  }
  function shellWord(text) { return /^[A-Za-z0-9_\/.,:@%+=-]+$/.test(text) ? text : "'" + text.replace(/'/g, "'\\''") + "'"; }
  $("editor-close").addEventListener("click", function () { $("editor-dialog").close(); });
  $("editor-config-copy").addEventListener("click", function () { copyText(editorConfig()); });

  // ---- the right: the system, release, settings
  function link(text, url) {
    return url ? el("a", { href: url, target: "_blank", rel: "noopener", text: text }) : document.createTextNode(text);
  }

  // previewWhat says what Stage shows: dev, dev as it was, or a
  // branch tried there, with Show dev to put dev as it is back on it.
  function previewWhat(s) {
    if (!s.preview) return document.createTextNode("none");
    var what = s.preview_branch ? s.preview_branch : s.preview.replace(/^dev, /, "dev: ");
    var nodes = [link(what, s.preview_url)];
    if (s.preview_branch || s.preview_old) {
      nodes.push(el("div", { class: "muted small", text: s.preview_branch ? "only this branch, not the next version" : "an earlier state of the next version, without the newest changes" }));
      nodes.push(showNext());
    }
    return el("div", { class: "preview-what" }, nodes);
  }
  function renderSystem() {
    var s = state.status || {};
    var facts = $("system");
    facts.textContent = "";
    function fact(name, value) {
      facts.appendChild(el("dt", { text: name }));
      facts.appendChild(el("dd", {}, [typeof value === "string" ? document.createTextNode(value) : value]));
    }
    fact("Production", s.production ? link("v" + s.production, s.production_url) : document.createTextNode("nothing published yet"));
    var dev = el("div", {}, [document.createTextNode((s.dev || "").replace(/^dev, /, ""))]);
    if (s.changes && s.changes.length) {
      dev.appendChild(el("ul", {}, s.changes.map(function (change) { return el("li", { text: change }); })));
    }
    fact("Next version (dev)", dev);
    fact("Preview", previewWhat(s));
    fact("Backups", s.snapshot || "none yet");
    fact("Incidents", document.createTextNode(state.incidents && state.incidents.length ? state.incidents.length + " open, below" : "none open"));
    if (s.jobs && s.jobs.length) fact("Jobs", el("ul", {}, s.jobs.map(function (job) { return el("li", { text: job }); })));

    // Production and Stage, in the bar's globe menu.
    var links = $("links");
    links.textContent = "";
    [["Production", s.production_url], ["Preview", s.preview_url]].forEach(function (pair) {
      if (!pair[1]) return;
      var a = el("a", { href: pair[1], target: "_blank", rel: "noopener", role: "menuitem", class: "theme-item" });
      a.appendChild(el("span", { class: "theme-name", text: pair[0] }));
      a.appendChild(el("span", { class: "links-host muted", text: pair[1].replace(/^https?:\/\//, "").replace(/\/$/, "") }));
      links.appendChild(a);
    });
    $("links-wrap").hidden = !links.childNodes.length;
  }

  // The globe's menu: opens under it, closes on a pick, a click
  // elsewhere, or Escape.
  (function () {
    var button = $("links-btn"), menu = $("links-menu");
    function close() { menu.hidden = true; button.setAttribute("aria-expanded", "false"); }
    button.addEventListener("click", function (event) {
      event.stopPropagation();
      var open = menu.hidden;
      menu.hidden = !open;
      button.setAttribute("aria-expanded", open ? "true" : "false");
      if (open) { var first = menu.querySelector("a"); if (first) first.focus(); }
    });
    menu.addEventListener("click", function (event) { event.stopPropagation(); if (event.target.closest("a")) close(); });
    menu.addEventListener("keydown", function (event) {
      var items = [].slice.call(menu.querySelectorAll("a"));
      var at = items.indexOf(document.activeElement);
      if (event.key === "ArrowDown" || event.key === "ArrowUp") {
        event.preventDefault();
        if (items.length) items[(at + (event.key === "ArrowDown" ? 1 : items.length - 1)) % items.length].focus();
      } else if (event.key === "Escape") { close(); button.focus(); }
    });
    document.addEventListener("click", close);
  })();

  // ask asks in the page's own dialog, not the browser's confirm(), which
  // in-app browsers (Slack's) may answer no without showing it. It
  // resolves to null on Cancel, or to what was typed ("" without a box).
  // Cancel isn't a submit button, so Enter in the answer is OK.
  $("ask-cancel").addEventListener("click", function () { $("ask-dialog").close("cancel"); });
  function ask(question, withInput) {
    return new Promise(function (resolve) {
      var dialog = $("ask-dialog");
      $("ask-question").textContent = question;
      var input = $("ask-input");
      input.hidden = !withInput;
      input.value = "";
      input.placeholder = withInput || "";
      dialog.onclose = function () { resolve(dialog.returnValue === "ok" ? input.value.trim() : null); };
      dialog.returnValue = "";
      dialog.showModal();
      (withInput ? input : $("ask-ok")).focus();
    });
  }

  // ---- A piece of work on its way: its worktree, then the preview (the
  // next version, what hi calls dev), then production.
  // workName is its task's title, else its workspace's name.
  function workName(space) {
    var number = ticketOf(space);
    var ticket = number && ((tickets && tickets.tickets) || []).filter(function (t) { return t.number === number; })[0];
    return ticket ? ticket.title : space.label;
  }
  // workStage says where a workspace's work is, and its next step.
  function workStage(space) {
    if (!space.path || !space.checked) return { text: "" };
    var unsaved = space.dirty ? "Changes not committed. Ask the agent to commit them." : "";
    if (space.out && !space.ahead) {
      // Taken out of the preview: its workspace keeps it, to add again.
      return { text: "Taken out of the preview" + (unsaved ? ". " + unsaved : ""), tone: "warn", add: true };
    }
    if (space.ahead > 0) {
      var text = space.branch && space.branch === (state.status || {}).preview_branch ? "Only this work is on the preview. It is not added yet." : "Not on the preview yet";
      return { text: text + (unsaved ? ". " + unsaved : ""), tone: "warn", add: true };
    }
    if (space.dirty) return { text: unsaved, tone: "warn" };
    // Level with dev, but nothing of its own went in: not done, not
    // published; Discard is in its ⋯ menu.
    if (!space.shipped) return { text: "Nothing committed yet", tone: "muted" };
    var published = !(state.changes || []).length && (state.status || {}).production;
    return { text: published ? "Published ✓" : "On the preview ✓", tone: "muted", done: true };
  }
  // Add to Preview: its commits join the next version (hi dev ship), which
  // goes on the preview; its task's title is the change's.
  function addButton(space) {
    // Taken out of the preview, it's no longer the next step: a plain Add
    // again, so it isn't pressed by mistake right after the swap.
    var again = space.out && !space.ahead;
    return el("button", { class: again ? "" : "primary", text: again ? "Add again" : "Add to Preview", busy: "ship:" + space.id, title: again ? "Put it back on the preview, in the next version" : "Add it to the next version on the preview, with the other changes. Publish sends the next version to production.", onclick: function () {
      addToPreview(space, "");
    } });
  }
  // addToPreview adds a piece of work to the preview; takeOut, the change
  // there last, taken out for it in the same step. What goes wrong opens
  // a dialog that stays, with what can be done about it.
  // combine, for Merge changes into preview, answers a clash itself: it
  // asks the work's agent to combine them, as the dialog's first choice
  // does, and says so; the dialog only when no agent can be asked.
  function addToPreview(space, takeOut, combine) {
    var name = workName(space);
    toast((takeOut ? "Swapping in " : "Adding ") + name + (takeOut ? " on the preview…" : " to the preview…"), true);
    var body = { workspace: space.id, title: name !== space.label ? name : "" }; // its task's title, else the commit's
    if (takeOut) body.take_out = takeOut;
    return working("ship:" + space.id, api("POST", "ship", body)).then(function (r) {
      $("stuck-dialog").close();
      toast(r.text);
      refresh();
      refreshTickets(false);
      return true;
    }).catch(function (error) {
      $("toast").hidden = true;
      var data = error.data || {};
      refresh();
      if (combine && data.conflict && space.agent) {
        return askToCombine(space, data).then(function () {
          toast("“" + name + "” conflicts with the preview. " + agentName(space.agent) + " combines the changes, then adds them.");
          return "asked";
        }).catch(function () {
          stuck(space, error.message, data);
          return false;
        });
      }
      stuck(space, error.message, data);
      return false;
    });
  }
  // askToCombine asks a piece of work's agent to bring in what's on the
  // preview, make both work together, and add it again.
  function askToCombine(space, data) {
    var conflict = data.conflict;
    var withTitles = (data.with || []).map(function (c) { return "“" + c.title + "”"; });
    var text = "Adding this work to the preview failed: it conflicts with what's on the preview (dev) in " + conflict.files.join(", ") +
      (withTitles.length ? ", where " + withTitles.join(" and ") + " changed the same lines" : "") +
      ". Bring dev in (git fetch origin && git rebase origin/" + (conflict.dev || "main") + "), resolve the conflicts so both changes work together, and run the checks. If they can't both be kept, ask me which to keep. Then add it to the preview with hi dev ship, and tell me what you did.";
    return api("POST", "chat/send", { workspace: space.id, text: text });
  }

  // ---- Stuck: what went wrong adding a piece of work to the preview, in
  // a dialog that stays until you choose: the agent combines the changes,
  // the change there last makes way for it, the developers are asked in Slack,
  // or Cancel.
  var stuckNow = null;
  function stuck(space, message, data) {
    stuckNow = { space: space, message: message, data: data };
    var name = "“" + workName(space) + "”";
    var conflict = data.conflict;
    var withTitles = (data.with || []).map(function (c) { return "“" + c.title + "”"; });
    $("stuck-title").textContent = name + " cannot go on the preview yet";
    var what = conflict
      ? "It changes the same lines as " + (withTitles.length ? withTitles.join(" and ") + ", which " + (withTitles.length === 1 ? "is" : "are") + " on the preview already" : "work that is on the preview already") + ". The files: " + conflict.files.join(", ") + "."
      : message;
    $("stuck-what").textContent = what;
    stuckNow.what = "Adding " + name + " to the preview: " + what;
    $("stuck-error").textContent = message;
    $("stuck-said").textContent = "";
    $("stuck-said").className = "small";
    var agent = conflict && space.agent ? agentName(space.agent) : "";
    $("stuck-agent").hidden = !agent;
    $("stuck-agent-title").textContent = "Ask " + agent + " to combine them";
    $("stuck-agent-text").textContent = agent + " gets the changes from the preview and makes both changes work together. If it cannot, it asks you. Then it adds the work again.";
    if (space.pushed) {
      $("stuck-agent-text").textContent = "hi starts new work in the dev box. " + agent + " brings in " + space.label + ", makes both changes work together, and adds them. If it cannot, it asks you. Or " + (space.pushed.author || "the person who pushed it") + " can rebase on dev and push again.";
    }
    var last = conflict && data.last;
    $("stuck-replace").hidden = !last;
    if (last) {
      $("stuck-replace-title").textContent = "Take out “" + last.title + "” and add this one instead";
      $("stuck-replace-text").textContent = "“" + last.title + "” leaves the preview. Its workspace keeps it, so you can add it again later.";
      if (space.pushed) $("stuck-replace-title").textContent = "Take out “" + last.title + "” and add " + space.label + " instead";
    }
    document.querySelectorAll("#stuck-options button").forEach(function (button) { button.disabled = false; });
    if (!$("stuck-dialog").open) $("stuck-dialog").showModal();
    var first = [].filter.call(document.querySelectorAll("#stuck-options button"), function (button) { return !button.hidden; })[0];
    if (first) first.focus();
  }
  function stuckSay(text, bad) {
    $("stuck-said").textContent = text;
    $("stuck-said").className = "small" + (bad ? " error" : "");
  }
  function stuckBusy(on) {
    document.querySelectorAll("#stuck-options button").forEach(function (button) { button.disabled = on; });
  }
  $("stuck-cancel").addEventListener("click", function () { $("stuck-dialog").close(); });
  $("stuck-agent").addEventListener("click", function () {
    var now = stuckNow;
    stuckBusy(true);
    stuckSay("Asking " + agentName(now.space.agent) + "…");
    if (now.space.pushed) {
      combinePushed(now.space, now.data).then(function (answer) {
        $("stuck-dialog").close();
        toast((answer.warning ? answer.warning + " " : "") + "Started " + answer.branch + ". " + agentName(now.space.agent) + " combines the changes, then adds them.");
        refresh().then(function () {
          renderSideTasks();
          if (answer.workspace) chooseChatSpace(answer.workspace);
        });
      }).catch(function (error) {
        stuckBusy(false);
        stuckSay(error.message, true);
      });
      return;
    }
    askToCombine(now.space, now.data).then(function () {
      $("stuck-dialog").close();
      if (chat.view !== "chat") setCodeView("chat");
      if (chat.space !== now.space.id) chooseChatSpace(now.space.id);
      toast("Asked " + agentName(now.space.agent) + " to combine the changes. It tells you here when it is done.");
      setTimeout(pollChat, 800);
    }).catch(function (error) {
      stuckBusy(false);
      stuckSay(error.message, true);
    });
  });
  $("stuck-replace").addEventListener("click", function () {
    var now = stuckNow;
    stuckBusy(true);
    stuckSay("Taking out “" + now.data.last.title + "” and adding this one…");
    if (now.space.pushed) addPushed(now.space, now.data.last.id);
    else addToPreview(now.space, now.data.last.id);
  });
  $("stuck-help").addEventListener("click", function () {
    var now = stuckNow;
    stuckBusy(true);
    stuckSay("Asking in the app's channel…");
    api("POST", "help", { workspace: now.space.id, what: now.what, error: now.message }).then(function () {
      $("stuck-dialog").close();
      toast("Asked the developers in the app's channel. hi sent the details.");
    }).catch(function (error) {
      stuckBusy(false);
      stuckSay(error.message, true);
    });
  });
  // Preview only this change: the branch alone on the preview, for a look
  // without the rest; Show the next version puts that back.
  function previewOnly(space) {
    toast("Putting only " + workName(space) + " on the preview…", true);
    act("", "ship", { workspace: space.id, preview: true }, function (r) { return r.text; }, "ship:" + space.id);
  }
  function finishButton(space, discard) {
    return el("button", {
      class: discard ? "danger" : "",
      text: discard ? "Discard…" : "Done ✓",
      busy: "finish:" + space.id,
      title: discard ? "Remove the work. Work that is not on the preview is lost." : "Remove its worktree, workspace, and branch, and close its task",
      onclick: function () {
        var left = [];
        if (space.ahead > 0 || space.out) left.push("the work that is not on the preview");
        if (space.dirty) left.push("its changes that are not committed");
        var question = discard
          ? "Discard " + workName(space) + "? " + (left.length ? "You lose " + left.join(" and ") + ". " : "") + "hi removes its worktree, workspace, agent, and branch."
          : "Done with " + workName(space) + "? Its work is on the preview. hi removes its worktree, workspace, agent, and branch, and closes its task.";
        act(question, "finish", { workspace: space.id, discard: discard }, function (r) { return r.text; }, "finish:" + space.id);
      },
    });
  }
  // moreMenu is a ⋯ button with a small menu of the rarer actions.
  function moreMenu(items) {
    var wrap = el("span", { class: "more-wrap" });
    var menu = el("div", { class: "theme-menu more-menu", role: "menu", hidden: "" });
    items.forEach(function (item) {
      menu.appendChild(el("button", { type: "button", role: "menuitem", class: "theme-item" + (item.danger ? " danger" : "") + (item.pro ? " pro-only" : ""), title: item.title || "", text: item.text,
        onclick: function (event) { event.stopPropagation(); menu.hidden = true; item.run(); } }));
    });
    var button = el("button", { type: "button", class: "more-btn", text: "⋯", title: "More", "aria-label": "More", "aria-haspopup": "menu", onclick: function (event) {
      event.stopPropagation();
      document.querySelectorAll(".more-menu").forEach(function (other) { if (other !== menu) other.hidden = true; });
      menu.hidden = !menu.hidden;
    } });
    wrap.appendChild(button);
    wrap.appendChild(menu);
    return wrap;
  }
  document.addEventListener("click", function () { document.querySelectorAll(".more-menu").forEach(function (menu) { menu.hidden = true; }); });
  function act(question, path, body, done, busy) {
    (question ? ask(question) : Promise.resolve("")).then(function (answer) {
      if (answer === null) return;
      // An action may change the tickets too, as Finish closes one.
      working(busy, api("POST", path, body)).then(function (result) {
        toast(typeof done === "function" ? done(result) : done);
        refresh();
        refreshTickets(false);
      }).catch(function (error) { toast(error.message); refresh(); refreshTickets(false); });
    });
  }

  // renderFlow draws the way to production: the work not on the preview
  // yet, each with Add to Preview; the preview, the next version, with
  // its changes and Publish; and production.
  function renderFlow() {
    var box = $("flow");
    box.textContent = "";
    var s = state.status || {};
    var changes = state.changes || [];
    var addr = {};
    (state.addresses || []).forEach(function (a) { addr[a.address] = a.internal; });
    function plural(n, one, many) { return n + " " + (n === 1 ? one : many); }
    function host(url) { return url ? link(url.replace(/^https?:\/\//, "").replace(/\/$/, "") + " ↗", url) : null; }
    function step(cls, title, url, body) {
      box.appendChild(el("section", { class: "flow-step " + cls }, [el("div", { class: "flow-head" }, [el("strong", { text: title }), url ? host(url) : null])].concat(body)));
    }

    // 1. Work not on the preview yet: the workspaces', then the branches
    // people pushed to GitHub or GitLab from their own machines.
    var work = waitingWork();
    var pushed = pushedWaiting();
    renderPreviewAct();
    if (work.length || pushed.length) {
      // Merge changes into preview: each waiting one in turn, as Add to
      // Preview does, stopping at one that clashes (its dialog says why).
      // Not the ones taken out of it: they were, on purpose.
      var mergeable = work.filter(function (space) { return space.ahead > 0; });
      var merge = mergeable.length > 1 ? el("button", { class: "primary flow-merge", busy: "merge-all", title: "Add each of these to the preview, one after another", onclick: function () { mergeAll(mergeable); } }, [
        mergeIcon(), el("span", { text: "Merge " + mergeable.length + " changes into preview" }),
      ]) : null;
      step("flow-work", "Not on the preview yet", "", [work.length ? el("ul", { class: "flow-list" }, work.map(function (space) {
        var stage = workStage(space);
        return el("li", {}, [
          el("span", { class: "flow-name" }, [el("span", { text: workName(space) }), ticketOf(space) ? el("span", { class: "muted", text: " #" + ticketOf(space) }) : null,
            space.out && !space.ahead ? el("span", { class: "small muted flow-sub", text: "Taken out of the preview" }) : null,
            space.dirty ? el("span", { class: "small warn flow-sub", text: "Changes not committed. Ask the agent to commit them." }) : null]),
          stage.add ? addButton(space) : null,
        ]);
      })) : null, merge, pushed.length ? renderPushed(pushed) : null]);
    }

    // 2. The preview: the next version, or one change alone.
    var preview = [];
    if (s.preview_branch) {
      var alone = workSpaces().filter(function (space) { return space.branch === s.preview_branch; })[0];
      var pushedAlone = /^pushed\//.test(s.preview_branch) ? s.preview_branch.slice(7) + " (pushed to " + pushedForge() + ")" : "";
      preview.push(el("div", { class: "flow-what" }, [document.createTextNode("Shows only "), el("strong", { text: alone ? workName(alone) : pushedAlone || s.preview_branch }), document.createTextNode(", not the next version. "), showNext()]));
    } else if (s.preview_old) {
      preview.push(el("div", { class: "flow-what" }, [document.createTextNode("Shows an earlier state of the next version, without the newest changes. "), showNext()]));
    } else if (!s.preview) {
      preview.push(el("div", { class: "muted small", text: "Nothing on it yet." }));
    }
    if (!s.preview_branch && !s.preview_old && s.preview) {
      preview.push(el("div", { class: "flow-what", text: changes.length ? "The next version: v" + (s.production || 0) + " + " + plural(changes.length, "change", "changes") : s.production ? "The same as production (v" + s.production + "). Nothing is new." : "The first version" }));
    }
    if (changes.length) preview.push(el("ul", { class: "flow-changes" }, changes.map(function (c) { return el("li", { text: c.title }); })));
    var next = (s.production || 0) + 1;
    if (releasing) {
      preview.push(el("div", { class: "flow-act" }, [
        el("button", { class: "primary busy", disabled: "", text: "Publishing v" + releasing + "…" }),
        el("span", { class: "muted small", text: "hi makes a snapshot of production. Then it starts v" + releasing + " in a new box. This takes 1 or 2 minutes." }),
      ]));
    } else if (changes.length || !s.production) {
      var acts = [el("button", { class: "primary", text: "Publish v" + next, title: "Send the next version to production. hi makes a snapshot first. hi records your name as the person who approved it.", onclick: openRelease })];
      if (changes.length && s.production) {
        acts.push(el("button", { class: "link danger small owners-only", text: "Clear the preview (back to v" + s.production + ")", busy: "dev-reset", onclick: function () {
          act("Clear the preview back to v" + s.production + "? The changes added since then leave the next version. Their history stays. The preview shows the code of production.", "dev/reset", {}, "The preview is back to production.", "dev-reset");
        } }));
      }
      preview.push(el("div", { class: "flow-act" }, acts));
    }
    step("flow-preview", "Preview", s.preview_url || addr.preview, preview);

    // 3. Production.
    step("flow-production", "Production", s.production_url || addr.production, [el("div", { class: "flow-what" }, [
      el("span", { class: "tag live", text: s.production ? "v" + s.production : "nothing published yet" }), document.createTextNode(s.production ? " the version that people use" : ""),
    ])]);
  }
  // ---- Branches pushed to the app's GitHub or GitLab project, from
  // people's own machines (app_pushed.go): each with Try on Preview and
  // Add to Preview, as a workspace has.
  function pushedForge() { return (state.pushed || {}).forge || "the remote"; }
  function pushedWaiting() {
    return ((state.pushed || {}).branches || []).filter(function (b) { return b.ahead > 0 || b.out; });
  }
  // pushedSpace is a pushed branch in a workspace's shape, for the Stuck
  // dialog; its agent is the one the page starts work with.
  function pushedSpace(b) {
    var agent = "claude";
    try { agent = localStorage.getItem("hi-dev-agent") || "claude"; } catch (e) { /* private window */ }
    if (agent === "shell") agent = "claude";
    return { id: "pushed:" + b.name, label: b.name, branch: "pushed/" + b.name, pushed: b, agent: agent };
  }
  function renderPushed(branches) {
    var p = state.pushed || {};
    var fetched = p.fetched ? "Checked " + new Date(p.fetched).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" }) + "." : "";
    return el("div", { class: "flow-pushed" }, [
      el("div", { class: "small muted flow-sub" }, [
        document.createTextNode("Pushed to " + pushedForge() + ". " + fetched + " "),
        el("button", { class: "link small", text: "Check now", busy: "pushed-fetch", title: "Look for new branches on " + pushedForge() + " now. hi looks every 2 minutes.", onclick: function () {
          act("", "pushed", { fetch: true }, function (r) { return r.text; }, "pushed-fetch");
        } }),
      ]),
      p.error ? el("div", { class: "small warn", text: "The last check failed: " + p.error }) : null,
      el("ul", { class: "flow-list" }, branches.map(function (b) {
        var space = pushedSpace(b);
        var what = b.ahead > 0
          ? b.ahead + (b.ahead === 1 ? " commit" : " commits") + ", " + (b.files || []).length + ((b.more || 0) ? "+" : "") + ((b.files || []).length === 1 ? " file" : " files")
          : "Taken out of the preview";
        var sub = (b.dev ? "Pushed straight to " + b.name + " by " : "By ") + (b.author || "someone") + ". " + what + ".";
        var onPreview = (state.status || {}).preview_branch === "pushed/" + b.name;
        // Pushed again since Try: the preview shows the commits before.
        var stale = onPreview && (state.status || {}).preview_commit && (state.status || {}).preview_commit !== b.commit;
        return el("li", { title: [b.subject].concat(b.files || []).join("\n") }, [
          el("span", { class: "flow-name" }, [el("span", { text: b.name }), el("span", { class: "small muted flow-sub", text: stale ? "On the preview as it was before the last push. Try again shows the new commits." : onPreview ? "Only this branch is on the preview. It is not added yet." : sub })]),
          b.ahead > 0 && (!onPreview || stale) ? el("button", { text: stale ? "Try again" : "Try", busy: "try:" + space.id, title: "Put only this branch on the preview, with a copy of production's data. It is not added yet.", onclick: function () {
            toast("Putting only " + b.name + " on the preview…", true);
            act("", "pushed", { branch: b.name, preview: true }, function (r) { return r.text; }, "try:" + space.id);
          } }) : null,
          el("button", { class: b.ahead > 0 ? "primary" : "", text: b.ahead > 0 ? "Add to Preview" : "Add again", busy: "ship:" + space.id,
            title: "Add it to the next version on the preview, with the other changes. Publish sends the next version to production.", onclick: function () { addPushed(space, ""); } }),
        ]);
      })),
    ]);
  }
  // addPushed adds a pushed branch to the preview, as addToPreview does a
  // workspace; a clash opens the Stuck dialog.
  function addPushed(space, takeOut) {
    toast((takeOut ? "Swapping in " : "Adding ") + space.label + (takeOut ? " on the preview…" : " to the preview…"), true);
    var body = { branch: space.pushed.name };
    if (takeOut) body.take_out = takeOut;
    return working("ship:" + space.id, api("POST", "pushed", body)).then(function (r) {
      $("stuck-dialog").close();
      toast(r.text);
      refresh();
      return true;
    }).catch(function (error) {
      refresh();
      stuck(space, error.message, error.data || {});
      return false;
    });
  }
  // combinePushed starts new work in the dev box: an agent brings the
  // pushed branch onto dev, makes both work together, and adds it.
  function combinePushed(space, data) {
    var b = space.pushed, dev = (data.conflict && data.conflict.dev) || "main";
    var text = "Bring in the branch " + b.name + ", which " + (b.author || "someone") + " pushed to " + pushedForge() + " from their own machine. " +
      "It conflicts with dev in " + ((data.conflict && data.conflict.files) || []).join(", ") + ". " +
      "In this worktree: git fetch origin && git merge origin/pushed/" + b.name + ". Resolve the conflicts so both changes work together, and run the checks. " +
      "If they can't both be kept, ask me which to keep. Then add it to the preview with hi dev ship, and tell me what you did.";
    return api("POST", "start-new", { text: text, agent: space.agent });
  }
  function waitingWork() {
    return workSpaces().filter(function (space) { return workStage(space).add || (space.path && space.checked && space.dirty); });
  }
  // The Preview view's one button, beside what it shows: Merge into
  // preview while work waits, then Publish the next version, which shows
  // production once it's out; nothing when there's nothing new.
  function renderPreviewAct() {
    var box = $("preview-act");
    if (!state) return;
    var s = state.status || {}, changes = state.changes || [];
    var mergeable = waitingWork().filter(function (space) { return space.ahead > 0; });
    var next = (s.production || 0) + 1;
    var kind = releasing ? "publishing" : mergeable.length ? "merge" : changes.length || (!s.production && s.preview) ? "publish" : "";
    var sig = kind + "|" + mergeable.map(function (space) { return space.id; }).join(",") + "|" + next + "|" + releasing;
    if (box.dataset.sig === sig) return;
    box.dataset.sig = sig;
    box.textContent = "";
    if (kind === "merge") {
      box.appendChild(el("button", { class: "primary small flow-merge", busy: "merge-all", title: "Add " + (mergeable.length === 1 ? "this change" : "each change, one after another,") + " to the preview. Then show the preview.", onclick: function () {
        showPreviewSlot("preview");
        mergeAll(mergeable);
      } }, [mergeIcon(), el("span", { text: "Merge " + mergeable.length + (mergeable.length === 1 ? " change" : " changes") + " into preview" })]));
    } else if (kind === "publish") {
      box.appendChild(el("button", { class: "primary small", text: "Publish v" + next, title: "Send the next version to production. hi makes a snapshot first. Then this shows production. hi records your name as the person who approved it.", onclick: openRelease }));
    } else if (kind === "publishing") {
      box.appendChild(el("button", { class: "primary small busy", disabled: "", text: "Publishing v" + releasing + "…" }));
    }
  }
  // mergeAll adds the pieces of work to the preview one after another.
  // A clash doesn't stop it: the work's agent is asked to combine them,
  // and the rest go on; only a clash with no agent to ask stops it, at
  // its dialog.
  function mergeAll(spaces) {
    var added = 0, combining = [];
    working("merge-all", spaces.reduce(function (chain, space) {
      return chain.then(function (going) {
        if (!going) return false;
        return addToPreview(space, "", true).then(function (result) {
          if (result === true) added++;
          if (result === "asked") combining.push(space);
          return result !== false;
        });
      });
    }, Promise.resolve(true)).then(function (all) {
      if (!all) return;
      if (!combining.length) { toast("All " + added + " changes are on the preview, in the next version."); return; }
      // The chat of the agent combining, to watch it work.
      if (chat.view !== "chat") setCodeView("chat");
      if (chat.space !== combining[0].id) chooseChatSpace(combining[0].id);
      setTimeout(pollChat, 800);
      toast((added ? added + (added === 1 ? " change is" : " changes are") + " on the preview. " : "") +
        combining.map(function (space) { return agentName(space.agent) + " is combining “" + workName(space) + "”"; }).join("; ") +
        " with the preview. Then it adds the work.");
    }));
  }
  // mergeIcon is git's merge: two lines joining, an arrow down, to the
  // preview under it.
  function mergeIcon() {
    var svg = document.createElementNS("http://www.w3.org/2000/svg", "svg");
    svg.setAttribute("viewBox", "0 0 24 24");
    svg.setAttribute("width", "16");
    svg.setAttribute("height", "16");
    svg.setAttribute("aria-hidden", "true");
    svg.innerHTML = '<g fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M6 3v6a6 6 0 0 0 6 6"/><path d="M18 3v6a6 6 0 0 1-6 6v6"/><path d="m8 17 4 4 4-4"/></g>';
    return svg;
  }
  // showNext puts the next version back on the preview.
  function showNext() {
    return el("button", { class: "small", text: "Show the next version", busy: "preview-dev", title: "Put the next version on the preview again. This adds or removes nothing.", onclick: function () {
      toast("Putting the next version on the preview…", true);
      act("", "preview-dev", {}, function (r) { return r.text; }, "preview-dev");
    } });
  }

  function renderOwnerActions() {
    var production = (state.status || {}).production || 0;
    // Incidents, with what an owner may do about them.
    var incidents = $("incidents");
    incidents.textContent = "";
    $("incidents-box").hidden = !(state.incidents && state.incidents.length);
    (state.incidents || []).forEach(function (incident) {
      var preview = incident.slot === "preview";
      var what = el("div", { class: "what" }, [
        el("div", { text: incident.id + " · " + incident.title }),
        el("div", { class: "muted small", text: incident.status + (incident.slot ? " · " + incident.slot : "") }),
        incident.detail ? el("details", {}, [el("summary", { class: "small", text: "What hi recorded" }), el("pre", { text: incident.detail })]) : null,
      ]);
      incidents.appendChild(el("li", { class: "ticket" }, [el("span", { class: "dot blocked" }), what, el("div", { class: "actions owners-only" }, [
        el("button", { text: preview ? "Restart the preview" : "Restart production", busy: "incident:" + incident.id, onclick: function () {
          act("Start " + (preview ? "the preview" : "production") + " again in a new box?", "incident", { id: incident.id, action: "restart", preview: preview }, "Restarted.", "incident:" + incident.id);
        } }),
        el("button", { text: "Close", busy: "incident:" + incident.id, onclick: function () {
          ask("Close " + incident.id + "? What fixed it?", "what fixed it").then(function (because) {
            if (because === null) return;
            act("", "incident", { id: incident.id, action: "close", because: because }, incident.id + " is closed.", "incident:" + incident.id);
          });
        } }),
      ])]));
    });

    // Releases, each one production can go back to.
    var releases = $("releases");
    releases.textContent = "";
    if (!state.releases || !state.releases.length) releases.appendChild(el("li", { class: "muted", text: "Nothing published yet." }));
    (state.releases || []).forEach(function (release) {
      var actions = [];
      if (release.version === production) {
        actions.push(el("span", { class: "muted small", text: "in production" }));
      } else {
        actions.push(el("button", { class: "owners-only", text: "Roll back to v" + release.version, busy: "rollback:" + release.version, onclick: function () {
          act("Put production back to the code of v" + release.version + "? The data does not change. hi makes a snapshot first.", "rollback", { version: release.version }, function (r) { return "Production runs v" + r.release.version + " now."; }, "rollback:" + release.version);
        } }));
        if (release.data) {
          actions.push(el("button", { class: "danger owners-only", text: "…and its data", busy: "rollback:" + release.version, onclick: function () {
            act("Put production back to v" + release.version + ", code and data? Production loses all data that people added after v" + release.version + ". hi makes a snapshot first.", "rollback", { version: release.version, data: true }, function (r) { return "Production runs v" + r.release.version + ", with its data, now."; }, "rollback:" + release.version);
          } }));
        }
      }
      releases.appendChild(el("li", { class: "ticket" }, [
        el("div", { class: "what" }, [
          el("div", { text: "v" + release.version + " · " + new Date(release.released).toLocaleString([], { dateStyle: "medium", timeStyle: "short" }) }),
          el("div", { class: "muted small", text: (release.by ? release.by + ": " : "") + (release.notes || "").split("\n")[0] }),
        ]),
        el("div", { class: "actions" }, actions),
      ]));
    });

    // The way to production, top to bottom: the work not on the preview
    // yet, the preview (the next version), and production.
    renderFlow();

    // Addresses, the owners': each one's, internal by default, public by
    // their choice.
    var addresses = $("addresses");
    addresses.textContent = "";
    var names = { production: "Production", preview: "Preview", dev: "This page" };
    (state.addresses || []).forEach(function (a) {
      var lines = [el("div", { class: "where-head" }, [el("strong", { text: names[a.address] }), link(a.internal.replace(/^http:\/\//, "").replace(/\/$/, ""), a.internal)])];
      if (!a.joined) lines.push(el("div", { class: "small warn", text: "The NetBird peer of the app is not connected. An admin runs on the machine: hi app dev " + state.team + " netbird <a setup key>" }));
      if (a.public) {
        var parts = [document.createTextNode(a.link ? "Public too: " : "Public too, but it has no public address now")];
        if (a.link) parts.push(link(a.link.replace(/^https:\/\//, "").split("/")[0], a.link));
        var notes = [];
        if (a.domain) notes.push("name " + a.domain);
        notes.push(a.allow_ips ? "only from " + a.allow_ips : "from anywhere, with the key in the link");
        lines.push(el("div", { class: "small warn" }, parts));
        lines.push(el("div", { class: "muted small", text: notes.join(" · ") + " (hi app domain, hi app allow-ip on the machine)" }));
      } else {
        lines.push(el("div", { class: "muted small", text: "Internal only" }));
      }
      var button = a.public
        ? el("button", { class: "small owners-only", text: "Internal only", busy: "public:" + a.address, onclick: function () {
            act("Make " + names[a.address].toLowerCase() + " internal only? Its public address stops. People reach it on the app's NetBird network, at " + a.internal, "public", { address: a.address, on: false }, names[a.address] + " is internal only.", "public:" + a.address);
          } })
        : el("button", { class: "small danger owners-only", text: "Make public", busy: "public:" + a.address, onclick: function () {
            act("Make " + names[a.address].toLowerCase() + " public too? It gets an address on the internet through NetBird. Anyone with its link can open it." + (a.address === "dev" ? " This page can build and publish the app." : ""), "public", { address: a.address, on: true }, names[a.address] + " is public too.", "public:" + a.address);
          } });
      addresses.appendChild(el("li", { class: "where-row " + a.address }, [el("div", { class: "what" }, lines), el("div", { class: "actions" }, [button])]));
    });

    $("data-copy").disabled = !production || !state.preview_data;
    $("data-copy").title = !production ? "Nothing is published yet" : (!state.preview_data ? "The preview has no data" : "");
    $("helper-preview").checked = !!(state.helper || {}).preview;
    $("helper-production").checked = !!(state.helper || {}).production;

    var rules = $("rules");
    rules.textContent = "";
    if (!state.rules || !state.rules.length) rules.appendChild(el("li", { class: "muted", text: "No rules yet." }));
    (state.rules || []).forEach(function (rule) {
      rules.appendChild(el("li", {}, [
        el("div", { class: "what" }, [el("code", { text: rule.job }), document.createTextNode(" " + rule.rule),
          el("div", { class: "muted small", text: (rule.approval && rule.approval.name) || "" })]),
        el("div", { class: "actions owners-only" }, [el("button", { text: "Remove", onclick: function () {
          act("Remove this rule for " + rule.job + "?", "rule", { job: rule.job, rule: rule.rule, remove: true }, "Removed.");
        } })]),
      ]));
    });
  }

  // The dev box's reach: an agent's ask for a site, as a card in the chat
  // of the work that asked; in Keys, Sites, what the owners allowed and what
  // the box was refused lately.
  function siteAct(host, allow) {
    act("", "site", { host: host, allow: allow }, allow ? "The dev box reaches " + host + " now." : "You said no to " + host + ".");
  }
  function renderSites() {
    var access = state.access || {};
    badge("security-count", (access.asks || []).length, (access.asks || []).length === 1 ? " site asked for" : " sites asked for");
    badge("sites-count", (access.asks || []).length, (access.asks || []).length === 1 ? " site asked for" : " sites asked for");
    chatSites();
    renderKeys();
    var list = $("sites");
    list.textContent = "";
    (access.asks || []).forEach(function (ask) {
      list.appendChild(el("li", {}, [
        el("div", { class: "what" }, [el("code", { text: ask.host }), el("div", { class: "muted small", text: "Asked" + (ask.work ? " for " + ask.work : "") + ": " + (ask.why || "") })]),
        el("div", { class: "actions owners-only" }, [
          el("button", { class: "primary", text: "Allow", onclick: function () { siteAct(ask.host, true); } }),
          el("button", { text: "Say no", onclick: function () { siteAct(ask.host, false); } }),
        ]),
      ]));
    });
    (access.allowed || []).forEach(function (site) {
      if (site.check) {
        // hi's check allowed it, for seven days unless an owner keeps it.
        var left = Math.max(0, Math.ceil((new Date(site.check.until) - Date.now()) / 86400000));
        list.appendChild(el("li", {}, [
          el("div", { class: "what" }, [el("code", { text: site.host }), el("div", { class: "muted small", text: "The check of hi allowed it. " + left + (left === 1 ? " day" : " days") + " left. Found: " + site.check.found + (site.why ? " · " + site.why : "") })]),
          el("div", { class: "actions owners-only" }, [
            el("button", { text: "Keep", onclick: function () { act("", "site", { host: site.host, keep: true }, "The dev box keeps " + site.host + "."); } }),
            el("button", { text: "Undo", onclick: function () { act("Remove " + site.host + " and say no to it?", "site", { host: site.host, undo: true }, "The dev box cannot reach " + site.host + " now."); } }),
          ]),
        ]));
        return;
      }
      list.appendChild(el("li", {}, [
        el("div", { class: "what" }, [el("code", { text: site.host }), el("div", { class: "muted small", text: ["Allowed", site.by ? "by " + site.by : "", site.why ? "· " + site.why : ""].filter(Boolean).join(" ") })]),
        el("div", { class: "actions owners-only" }, [el("button", { text: "Remove", onclick: function () {
          act("Stop the dev box from reaching " + site.host + "?", "site", { host: site.host, remove: true }, "The dev box cannot reach " + site.host + " now.");
        } })]),
      ]));
    });
    (access.refused || []).forEach(function (refused) {
      list.appendChild(el("li", {}, [
        el("div", { class: "what" }, [el("code", { text: refused.host }), el("div", { class: "muted small", text: "Refused " + refused.times + (refused.times === 1 ? " time" : " times") + ", last " + when(refused.last) + ". Nobody asked for it." })]),
        el("div", { class: "actions owners-only" }, [
          el("button", { text: "Allow", onclick: function () { act("Let the dev box reach " + refused.host + "? No agent asked for it.", "site", { host: refused.host, allow: true }, "The dev box reaches " + refused.host + " now."); } }),
          el("button", { text: "Dismiss", onclick: function () { siteAct(refused.host, false); } }),
        ]),
      ]));
    });
    if (!list.childNodes.length) list.appendChild(el("li", { class: "muted", text: "No other sites yet." }));
  }

  // chatSites shows the asks of the chat's work, and those of work that
  // has no workspace, at the end of the chat, above a question.
  function chatSites() {
    var box = $("chat-sites");
    var asks = ((state && state.access) || {}).asks || [];
    var spaces = (state && state.workspaces) || [];
    var space = spaces.filter(function (s) { return s.id === chat.space; })[0];
    asks = asks.filter(function (ask) {
      return (space && ask.work === space.branch) || !spaces.some(function (s) { return s.branch === ask.work; });
    });
    var sig = JSON.stringify(asks);
    if (sig === chat.sitesSig) return;
    var fresh = asks.length && !chat.sitesSig;
    chat.sitesSig = sig;
    box.textContent = "";
    box.hidden = !asks.length;
    asks.forEach(function (ask) {
      var card = el("div", { class: "ask-card site-card", role: "group" });
      function decide(allow) { card.classList.add("busy"); siteAct(ask.host, allow); }
      card.appendChild(el("div", { class: "ask-who" }, [el("span", { text: "The dev box asks for a site" + (ask.work && !space ? " · " + ask.work : "") })]));
      card.appendChild(el("p", { class: "ask-q" }, [document.createTextNode("Let it reach "), el("code", { text: ask.host }), document.createTextNode("?")]));
      if (ask.why) card.appendChild(el("p", { class: "ask-more", text: ask.why }));
      card.appendChild(el("div", { class: "ask-nav owners-only" }, [
        el("button", { type: "button", class: "primary", text: "Allow", onclick: function () { decide(true); } }),
        el("button", { type: "button", text: "Say no", onclick: function () { decide(false); } }),
        el("span", { class: "grow" }),
        el("span", { class: "muted small", text: "Allows the site and its subdomains from now on. Keys, Sites shows the list." }),
      ]));
      card.appendChild(el("p", { class: "developers-only muted small", text: "An owner allows sites for the dev box. Owners see this request in Keys, Sites. The channel also gets a message." }));
      box.appendChild(card);
    });
    if (fresh) $("chat-log").scrollTop = $("chat-log").scrollHeight;
  }
  // For an admin of the machine, the notes that send people to an admin
  // link to the admin page, at the app's section.
  function renderAdminLinks() {
    [["admin-owners", "people"], ["admin-keys", "agents"], ["admin-restore", ""]].forEach(function (pair) {
      var box = $(pair[0]);
      if (!box) return;
      box.hidden = !state.admin;
      if (state.admin) box.querySelector("a").href = state.admin + "#" + encodeURIComponent(state.team) + (pair[1] ? "/" + pair[1] : "");
    });
  }
  function renderKeys() {
    renderAdminLinks();
    var list = $("team-keys");
    list.textContent = "";
    (state.keys || []).forEach(function (key) {
      list.appendChild(el("li", {}, [
        el("div", { class: "what" }, [document.createTextNode(key.name + ": "), el("strong", { text: key.state }),
          el("div", { class: "muted small" }, [document.createTextNode("To change it, an admin runs "), el("code", { text: key.command }), document.createTextNode(" on the machine."),
            state.admin ? el("a", { href: state.admin + "#" + encodeURIComponent(state.team) + "/agents", target: "_blank", rel: "noopener noreferrer", text: " Or on the admin page ↗" }) : document.createTextNode("")])]),
      ]));
    });
  }

  // ---- Data: production's and Stage's data, and the
  // snapshots; asked for when the tab opens, as sizing a folder takes a
  // moment.
  // dataSize, not size: the Files view's size() below, a function of the
  // same name in the same scope, replaced this one everywhere.
  function dataSize(bytes) {
    var units = ["bytes", "KB", "MB", "GB", "TB"], i = 0;
    while (bytes >= 1000 && i < units.length - 1) { bytes /= 1000; i++; }
    return (i ? bytes.toFixed(bytes < 10 ? 1 : 0) : bytes) + " " + units[i];
  }
  function stamp(at) {
    return new Date(at).toLocaleString([], { dateStyle: "medium", timeStyle: "short" });
  }
  function refreshData() {
    api("GET", "data").then(renderData).catch(function (error) {
      $("data-snapshots").textContent = "";
      $("data-snapshots").appendChild(el("li", { class: "muted small", text: error.message }));
    });
  }
  // The preview's folder as the data panel last saw it: its fork, if any.
  var previewFolder = null;
  function renderData(data) {
    (data.folders || []).forEach(function (folder) {
      if (folder.slot === "preview") previewFolder = folder;
      var facts = $("data-" + folder.slot);
      facts.textContent = "";
      function fact(name, value) {
        facts.appendChild(el("dt", { text: name }));
        facts.appendChild(el("dd", { text: value }));
      }
      if (folder.slot === "preview" && folder.fork) {
        fact("A copy of", "production" + (folder.fork.version ? " v" + folder.fork.version : "") + ", " + stamp(folder.fork.at) + " (" + dataSize(folder.fork.bytes) + ")");
        fact("Added since", folder.files ? dataSize(folder.bytes) + " in " + folder.files + " file" + (folder.files === 1 ? "" : "s") : "nothing");
      } else {
        fact("Holds", folder.files ? dataSize(folder.bytes) + " in " + folder.files + " file" + (folder.files === 1 ? "" : "s") : "nothing yet");
      }
      if (folder.changed) fact("Last change", ago(folder.changed) || stamp(folder.changed));
      fact("The app", folder.running ? "running" : "not running");
      if (folder.slot === "preview") $("data-empty").checked = !!folder.empty;
    });
    var list = $("data-snapshots");
    list.textContent = "";
    if (data.snapshots_error) list.appendChild(el("li", { class: "muted small", text: data.snapshots_error }));
    else if (!(data.snapshots || []).length) list.appendChild(el("li", { class: "muted small", text: "None yet." }));
    (data.snapshots || []).forEach(function (snapshot) {
      list.appendChild(el("li", {}, [el("div", { class: "what" }, [
        document.createTextNode(stamp(snapshot.time) + " · " + snapshot.why),
        el("div", { class: "muted small", text: snapshot.id + (snapshot.size ? " · " + dataSize(snapshot.size) : "") }),
      ])]));
    });
    $("data-restore").textContent = "hi app restore " + (state.team || "<app>") + " <snapshot>";
  }
  function dataAct(question, path, body, done) {
    (question ? ask(question) : Promise.resolve("")).then(function (answer) {
      if (answer === null) return;
      toast("Working…");
      api("POST", path, body).then(function () { toast(done); refresh(); refreshData(); })
        .catch(function (error) { toast(error.message); refreshData(); });
    });
  }
  $("data-fork").addEventListener("click", function () {
    dataAct("Give the preview a new copy of the data in production? hi keeps one copy of the data the preview made. Production stops for a moment. The preview starts again on the new copy.", "data/copy", { to: "preview" }, "The preview has a new copy of the data in production.");
  });
  $("data-copy").addEventListener("click", function () {
    // A preview on a fork has production's data from then, not data of its
    // own: say so, or the copy looks like it brings what Stage gathered.
    var fork = previewFolder && previewFolder.fork;
    var question = fork
      ? "The preview uses a copy of the data in production" + (fork.version ? " (v" + fork.version + ")" : "") + " from " + stamp(fork.at) + ". It has no data of its own. If you copy it, production gets that copy back, plus the changes made in the preview since then (" + (previewFolder.files ? dataSize(previewFolder.bytes) : "nothing") + "). Replace the data in production with it? hi makes a snapshot of production first. Then production starts again on the copy."
      : "Replace the data in production with a copy of the preview data? hi makes a snapshot of production first. Then production starts again on the copy.";
    dataAct(question, "data/copy", {}, "Production has the preview data now.");
  });
  $("data-snapshot").addEventListener("click", function () {
    dataAct("", "data/snapshot", {}, "hi made the snapshot.");
  });
  $("data-empty").addEventListener("change", function (event) {
    dataAct("", "data/preview", { empty: event.target.checked }, event.target.checked ? "The preview starts empty at each publish." : "The preview starts on a copy of the data in production at each publish.");
  });
  ["preview", "production"].forEach(function (slot) {
    $("helper-" + slot).addEventListener("change", function (event) {
      var on = event.target.checked;
      act("", "helper", { slot: slot, on: on }, "The helper is " + (on ? "on" : "off") + (slot === "preview" ? " on the preview." : " in production."));
    });
  });
  $("rule-form").addEventListener("submit", function (event) {
    event.preventDefault();
    act("", "rule", { job: $("rule-job").value.trim(), rule: $("rule-text").value.trim() }, "The lead uses this rule from now on.");
    $("rule-text").value = "";
  });

  function renderSettings() {
    // The shared keys an admin shared to the app, as a setting's source.
    var from = $("setting-from"), chosen = from.value, shared = state.shared || [];
    while (from.options.length > 1) from.remove(1);
    shared.forEach(function (name) { from.appendChild(el("option", { value: name, text: "Use the shared key " + name })); });
    from.hidden = !shared.length;
    from.value = shared.indexOf(chosen) >= 0 ? chosen : "";
    settingInputs();
    var list = $("settings");
    list.textContent = "";
    if (!state.settings.length) list.appendChild(el("li", { class: "muted", text: "No variables yet." }));
    state.settings.forEach(function (label) {
      var name = label.split(" ")[0];
      list.appendChild(el("li", {}, [
        el("div", { class: "what" }, [el("code", { text: name }), el("span", { class: "muted small", text: label.slice(name.length) })]),
        el("div", { class: "actions owners-only" }, [el("button", {
          text: "Remove", onclick: function () {
            act("Remove " + name + " from the dev box, the preview, and production?", "settings", { key: name, unset: true }, name + " is removed.");
          },
        })]),
      ]));
    });
  }

  // A file instead of a value: the box gets it, and the variable its path.
  function settingInputs() {
    var file = !!$("setting-file").files.length, shared = !!$("setting-from").value;
    $("setting-value").required = !file && !shared;
    $("setting-value").disabled = file || shared;
    $("setting-file").disabled = shared;
  }
  $("setting-file").addEventListener("change", settingInputs);
  $("setting-from").addEventListener("change", settingInputs);

  function settingFile() {
    var file = $("setting-file").files[0];
    if (!file) return Promise.resolve("");
    if (file.size > 32 << 10) return Promise.reject(new Error(file.name + " is larger than 32 KB. Use a file only for a certificate or a key."));
    return new Promise(function (resolve, reject) {
      var reader = new FileReader();
      reader.onload = function () { resolve(String(reader.result).replace(/^[^,]*,/, "")); };
      reader.onerror = function () { reject(new Error("cannot read " + file.name)); };
      reader.readAsDataURL(file);
    });
  }

  $("setting-form").addEventListener("submit", function (event) {
    event.preventDefault();
    var key = $("setting-key").value.trim();
    settingFile().then(function (file) {
      var request = { key: key, slot: $("setting-slot").value };
      var shared = $("setting-from").value;
      if (shared) request.from = shared; else if (file) request.file = file; else request.value = $("setting-value").value;
      return api("POST", "settings", request).then(function () {
        $("setting-value").value = "";
        $("setting-key").value = "";
        $("setting-file").value = "";
        $("setting-from").value = "";
        settingInputs();
        toast(shared ? key + " uses the shared key " + shared + "." : file ? key + " is saved. The path to the file is in $" + key + "." : key + " is saved.");
        refresh();
      });
    }).catch(function (error) { toast(error.message); });
  });

  // ---- People (the owners'), and everyone's own SSH key
  // A developer: no owners' buttons. How much of the page shows is the
  // person's own: Vibe mode or Pro mode (viewMode, below).
  function developer() { return !!state && state.level === "developer"; }
  function renderSSHKey() {
    $("sshkey-now").textContent = state.ssh_key ? "Your key now: " + state.ssh_key + ". A new key replaces it." : "You have no key in the dev box yet.";
    var connect = $("sshkey-connect");
    connect.hidden = !state.ssh;
    if (!state.ssh) return;
    connect.textContent = "";
    var line = state.ssh.host ? "ssh " + editorTarget() : "";
    connect.appendChild(line ? el("div", { class: "editor-facts" }, [el("code", { text: line }),
      el("button", { type: "button", class: "small link", text: "Copy", "aria-label": "Copy the ssh command", onclick: function () { copyText(line); } })])
      : el("span", { class: "muted", text: "The dev box is not on the app's NetBird network yet. An admin of the machine must run hi app dev " + state.team + " netbird" }));
  }
  $("sshkey-form").addEventListener("submit", function (event) {
    event.preventDefault();
    api("POST", "sshkey", { key: $("sshkey").value.trim() }).then(function () {
      $("sshkey").value = "";
      toast("Your key is in the dev box.");
      refresh();
    }).catch(function (error) { toast(error.message); });
  });
  function refreshPeople() {
    api("GET", "people").then(renderPeople).catch(function (error) {
      $("people").textContent = "";
      $("people").appendChild(el("li", { class: "muted small", text: error.message }));
    });
  }
  var people = null;
  function setLevel(person, level, row) {
    var question = "";
    var data = $("people-warn").hidden ? "" : " The preview uses a copy of the data in production. They can read that data in the preview.";
    var words = { owner: "an owner", developer: "a developer", contributor: "a contributor" };
    if (level === "developer") question = "Make " + person.name + " a developer? They can use Chat, the terminal, and SSH in the dev box, and publish. They cannot roll back. They cannot change the data or Env variables of production, the keys, the sites, the addresses, or the users. Their dev page starts in Vibe mode. Pro mode is one click away." + data;
    else if (level === "none") question = "Remove the level of " + person.name + "? They cannot report bugs. The lead does not take their requests. Their links stop working.";
    else if (person.level === "developer") question = "Make " + person.name + " " + words[level] + "? Their dev page link and SSH key stop working. An open terminal closes.";
    (question ? ask(question + " The channel gets a message.") : Promise.resolve("")).then(function (answer) {
      if (answer === null) { renderPeople(people); return; }
      row.classList.add("busy");
      api("POST", "people", { user: person.id, level: level }).then(function (result) {
        toast(result.message);
        refreshPeople();
      }).catch(function (error) { toast(error.message); refreshPeople(); });
    });
  }
  function renderPeople(view) {
    people = view;
    var body = $("people");
    body.textContent = "";
    var find = $("people-search").value.trim().toLowerCase();
    var levels = ["owner", "developer", "contributor", "none"];
    (view.people || []).forEach(function (person) {
      if (find && person.name.toLowerCase().indexOf(find) < 0 && person.id.toLowerCase().indexOf(find) < 0) return;
      var notes = [];
      if (!person.in_channel) notes.push("not in the channel");
      if (person.key) notes.push("SSH: " + person.key);
      var row = el("tr", { class: person.level }, [el("td", {}, [document.createTextNode(person.name),
        notes.length ? el("span", { class: "muted small", text: notes.join(" · ") }) : null])]);
      levels.forEach(function (level) {
        var radio = el("input", { type: "radio", name: "level-" + person.id, value: level, "aria-label": person.name + ": " + level });
        radio.checked = person.level === level;
        // Owners are the machine admin's, either way.
        radio.disabled = level === "owner" || person.level === "owner";
        radio.addEventListener("change", function () { setLevel(person, level, row); });
        row.appendChild(el("td", {}, [radio]));
      });
      body.appendChild(row);
    });
    if (!body.childNodes.length) body.appendChild(el("tr", {}, [el("td", { colspan: "5", class: "muted small", text: find ? "Nobody by that name." : "Nobody yet." })]));
    if (view.channel_error) body.appendChild(el("tr", {}, [el("td", { colspan: "5", class: "muted small", text: view.channel_error })]));
    $("people-warn").hidden = !view.preview_data;
  }
  $("people-search").addEventListener("input", function () { if (people) renderPeople(people); });

  function openRelease() {
    var next = ((state.status || {}).production || 0) + 1;
    $("release-title").textContent = "Publish v" + next;
    $("release-notes").value = (state.changes || []).map(function (task) { return "- " + task.title; }).join("\n");
    $("release-error").textContent = "";
    $("release-go").disabled = false;
    // The first release may take what Stage gathered along, once.
    $("release-data").checked = false;
    // Production's data is the owners'.
    $("release-data-box").hidden = developer() || !!((state.status || {}).production) || !state.preview_data;
    // The work all in this version, to clean up after: not the work an
    // agent is still on, nor what has more to add, nor a workspace whose
    // work hasn't begun, which has nothing in this version.
    var done = publishedWork();
    $("release-clean-box").hidden = !done.length;
    $("release-clean").checked = true;
    $("release-clean-text").textContent = "Then close the " + (done.length === 1 ? "task" : "tasks") + " of " + done.map(function (space) { return workName(space); }).join(", ") + ", and remove " + (done.length === 1 ? "its workspace" : "their workspaces");
    $("release-dialog").showModal();
  }
  function publishedWork() {
    return workSpaces().filter(function (space) {
      return space.path && space.checked && space.shipped && !space.ahead && !space.out && !space.dirty && space.status !== "working" && space.status !== "blocked";
    });
  }
  // After a publish: Done for each piece of work it took, one after another.
  function cleanUp(spaces) {
    return spaces.reduce(function (chain, space) {
      return chain.then(function () { return api("POST", "finish", { workspace: space.id, discard: false }).catch(function (error) { toast(workName(space) + ": " + error.message); }); });
    }, Promise.resolve()).then(function () { refresh(); refreshTickets(false); });
  }
  $("release-form").addEventListener("submit", function (event) {
    if (event.submitter && event.submitter.value !== "go") return;
    event.preventDefault();
    // The release takes a minute or two (a snapshot, then a new box): the
    // dialog closes now, and the page says how it goes.
    releasing = ((state.status || {}).production || 0) + 1;
    $("release-dialog").close();
    $("release-error").textContent = "";
    toast("Publishing v" + releasing + ". hi makes a snapshot of production, then starts the new box…", true);
    if (state.status) renderOwnerActions();
    renderPreviewAct();
    var clean = !$("release-clean-box").hidden && $("release-clean").checked ? publishedWork() : [];
    api("POST", "release", { notes: $("release-notes").value, preview_data: $("release-data").checked }).then(function (result) {
      releasing = 0;
      showPreviewSlot("production");
      toast("Production runs v" + result.release.version + " now." + (result.warning ? " " + result.warning : "") + (clean.length ? " Closing " + clean.length + (clean.length === 1 ? " finished workspace…" : " finished workspaces…") : ""));
      if (clean.length) cleanUp(clean); else refresh();
    }).catch(function (error) {
      // Back in the dialog, with the notes as they were.
      releasing = 0;
      $("toast").hidden = true;
      if (state.status) renderOwnerActions();
      renderPreviewAct();
      $("release-error").textContent = error.message;
      $("release-go").disabled = false;
      $("release-dialog").showModal();
    });
  });

  // ---- keeping current
  function refresh() {
    return api("GET", "state").then(function (data) {
      state = data;
      if (window.hiPresence) hiPresence.start();
      // A developer's page: the owners' buttons go (the server refuses
      // them anyway), and so does Data.
      document.body.classList.toggle("developer", developer());
      document.querySelectorAll(".owners-control").forEach(function (control) { control.disabled = developer(); });
      if (developer() && panes.right.view === "data") setSide("release", null);
      if (developer() && panes[paneOf("people")].view === "people") setSide("security", null);
      showKeys(keysPart);
      renderSSHKey();
      // agy, when the app has its sign-in (app_devagy.go).
      document.querySelectorAll(".agy-only").forEach(function (item) {
        item.hidden = !data.agy;
        item.disabled = !data.agy;
        var input = item.querySelector("input");
        if (!data.agy && input && input.checked) document.querySelector('input[name="' + input.name + '"][value="claude"]').checked = true;
        if (!data.agy && item.selected) item.parentNode.value = "claude";
      });
      document.title = "Dev · " + data.team;
      $("team").textContent = data.team;
      $("team").title = data.you ? "Signed in as " + data.you : "";
      renderSystem();
      renderSites();
      renderSettings();
      renderOwnerActions();
      renderWorkspaces();
      if (viewShown("tasks")) renderSideTasks(); // its workspace's task, now that it's known
      openLinkedTask();
      badge("release-count", (data.changes || []).length, " changes to publish");
    }).catch(function () {});
  }
  // A link to a task's work, #task-12, as the lead posts when it starts
  // one, opens that work's chat once its workspace is there.
  function openLinkedTask() {
    if (location.hash === "#sites") {
      // The channel's link to an agent's ask for a site.
      history.replaceState(null, "", location.pathname + location.search);
      revealKeys("sites");
      return;
    }
    var work = /^#work-(.+)$/.exec(location.hash);
    if (work) {
      // A DM's link to a waiting agent (app_devneeds.go): its chat.
      var name = decodeURIComponent(work[1]);
      var found = workSpaces().filter(function (space) { return spaceKey(space) === name; })[0];
      if (!found) return;
      history.replaceState(null, "", location.pathname + location.search);
      setCodeView("chat");
      focus(found);
      return;
    }
    var match = /^#task-(\d+)$/.exec(location.hash);
    var space = match && spaceFor({ number: Number(match[1]) });
    if (!space) return;
    history.replaceState(null, "", location.pathname + location.search);
    setCodeView("chat");
    focus(space);
  }
  function refreshTickets(fresh) {
    var query = [fresh ? "fresh=1" : "", showing !== "open" ? "closed=1" : ""].filter(Boolean).join("&");
    return api("GET", "tickets" + (query ? "?" + query : "")).then(function (data) {
      tickets = data;
      $("tickets-where").textContent = tickets.forge === "hi" ? "Tasks in hi" : "Issues in " + (tickets.where || tickets.forge);
      $("tickets-where").title = tickets.forge === "hi" ? "hi keeps these tasks in the folder of the app" : "";
      if (viewShown("tasks")) renderSideTasks(); else renderTaskBadge();
      if (state) { renderWorkspaces(); spaceLabel(); if (state.status) renderFlow(); } // the work's names are its tasks' titles
    }).catch(function (error) { $("tickets-where").textContent = error.message; });
  }

  // ---- the chat: a workspace's Claude Code session as a conversation,
  // read from its transcript; what you write goes to the agent, so the chat
  // and the terminal are one session
  var narrow = window.matchMedia && window.matchMedia("(max-width: 760px), (pointer: coarse)").matches;
  var chat = { view: "chat", space: "", session: "", offset: 0, tools: {}, group: null, busy: false, timer: 0, agent: null, id: "", pending: [], resp: null, queue: [], queueOpen: true, turn: 0, spin: 0, composing: false, askSig: "" };
  // Rewind's points (Claude Code's checkpoints, one for each message you
  // sent), what the page last saw, and the point the dialog is open on.
  var rewind = { points: [], seen: "", open: null };
  // The two panels beside the work, as an editor's side bars: on the left
  // what goes into the app (tasks, workspaces, files, search, checks,
  // decisions, users, keys); on the right what comes out of it (source
  // control, logs, preview, release, data, system). Each has its
  // open view, whether it's shown, and how wide. On a narrow screen they
  // lie over the chat, one at a time, so they start hidden.
  var sideOver = window.matchMedia ? window.matchMedia("(max-width: 1100px)") : { matches: false };
  var panes = { left: { view: "tasks", open: true, width: 300 }, right: { view: "preview", open: true, width: 340 } };
  // ---- Vibe mode and Pro mode: how much of the page shows, each person's
  // own, in this browser; not a right (hi 2.6.202). Vibe shows the chat,
  // the work, the preview, and publishing; what more, they choose
  // (Customize Vibe mode). Pro shows everything.
  var MODE_PARTS = [
    { group: "Left panel", items: [["tasks", "Tasks"], ["spaces", "Workspaces"], ["files", "Files"], ["search", "Search"], ["checks", "Checks"], ["decisions", "Decisions"], ["people", "Users (owners)"], ["security", "Keys"]] },
    { group: "The middle", items: [["terminal", "Terminal"], ["editor", "Editor ↗"]] },
    { group: "Right panel", items: [["git", "Source control"], ["logs", "Logs"], ["preview", "Preview"], ["release", "Publish"], ["data", "Data (owners)"], ["system", "System"]] },
  ];
  var VIBE_HIDES = ["files", "search", "checks", "decisions", "people", "security", "editor", "git", "logs", "data", "system"];
  var viewMode = { mode: "vibe", hides: VIBE_HIDES.slice() };
  try {
    var keptMode = JSON.parse(localStorage.getItem("hi-dev-mode") || "null");
    if (keptMode && (keptMode.mode === "vibe" || keptMode.mode === "pro")) viewMode.mode = keptMode.mode;
    if (keptMode && Array.isArray(keptMode.hides)) viewMode.hides = keptMode.hides;
  } catch (e) { /* private window */ }
  function keepMode() {
    try { localStorage.setItem("hi-dev-mode", JSON.stringify(viewMode)); } catch (e) { /* private window: this page only */ }
  }
  // modeHides says Vibe mode hides a part now. peek is a view it hides
  // that something on the page opened on purpose (a site's ask opens
  // Keys): it shows until another view is picked in its panel.
  var peek = "";
  function modeHides(key) { return viewMode.mode === "vibe" && viewMode.hides.indexOf(key) >= 0 && key !== peek; }
  var MODE_ICONS = {
    vibe: '<svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M12 3l1.8 4.7L18.5 9.5l-4.7 1.8L12 16l-1.8-4.7L5.5 9.5l4.7-1.8z"/><path d="M19 15l.8 2.2L22 18l-2.2.8L19 21l-.8-2.2L16 18l2.2-.8z"/></svg>',
    pro: '<svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M4 6h10"/><path d="M18 6h2"/><circle cx="16" cy="6" r="2"/><path d="M4 12h4"/><path d="M12 12h8"/><circle cx="10" cy="12" r="2"/><path d="M4 18h12"/><path d="M20 18h0"/><circle cx="18" cy="18" r="2"/></svg>',
  };
  // applyMode shows and hides the parts, and moves off a view that went.
  function applyMode() {
    document.body.classList.toggle("mode-vibe", viewMode.mode === "vibe");
    MODE_PARTS.forEach(function (part) {
      part.items.forEach(function (item) { document.body.classList.toggle("mode-hide-" + item[0], modeHides(item[0])); });
    });
    var button = $("mode-btn");
    button.innerHTML = MODE_ICONS.vibe;
    button.title = (viewMode.mode === "vibe" ? "Vibe mode: the chat, your work, the preview, and publish." : "Pro mode: all of the page.") + " Click to change it.";
    button.setAttribute("aria-label", viewMode.mode === "vibe" ? "Vibe mode. Click to change it." : "Pro mode. Click to change it.");
    if (modeHides("terminal") && chat.view === "terminal") setCodeView("chat");
    renderSide();
    sideShown();
  }
  // shownViews are a panel's views that show now: not hidden by the mode,
  // nor by being the owners' only.
  function shownViews(which) {
    return [].filter.call($(paneIds[which].panel).querySelectorAll(".side-tabs button[data-side]"), function (button) {
      return !modeHides(button.dataset.side) && getComputedStyle(button).display !== "none";
    }).map(function (button) { return button.dataset.side; });
  }
  function setMode(mode) {
    viewMode.mode = mode;
    keepMode();
    applyMode();
    drawModeMenu();
  }
  function drawModeMenu() {
    var menu = $("mode-menu");
    menu.textContent = "";
    menu.appendChild(el("div", { class: "theme-head", text: "How much of the page you see" }));
    [["vibe", "Vibe", "The chat, your work, the preview, and publish"], ["pro", "Pro", "All of the page: files, source control, logs, checks…"]].forEach(function (m) {
      var on = viewMode.mode === m[0];
      var icon = el("span", { class: "mode-icon", "aria-hidden": "true" });
      icon.innerHTML = MODE_ICONS[m[0]];
      menu.appendChild(el("button", { type: "button", role: "menuitemradio", "aria-checked": on ? "true" : "false", class: "theme-item mode-item", "data-mode": m[0],
        onclick: function () { setMode(m[0]); closeModeMenu(); } }, [
        icon,
        el("span", { class: "mode-words" }, [el("span", { class: "theme-name", text: m[1] }), el("span", { class: "muted mode-about", text: m[2] })]),
        el("span", { class: "theme-check", text: on ? "✓" : "" }),
      ]));
    });
    menu.appendChild(el("div", { class: "space-sep" }));
    menu.appendChild(el("button", { type: "button", role: "menuitem", class: "theme-item", id: "mode-customize", text: "Customize Vibe mode…",
      onclick: function () { closeModeMenu(); openModeDialog(); } }));
  }
  function closeModeMenu() {
    $("mode-menu").hidden = true;
    $("mode-btn").setAttribute("aria-expanded", "false");
  }
  $("mode-btn").addEventListener("click", function (event) {
    event.stopPropagation();
    var menu = $("mode-menu");
    if (!menu.hidden) { closeModeMenu(); return; }
    closeLayoutMenu();
    drawModeMenu();
    menu.hidden = false;
    $("mode-btn").setAttribute("aria-expanded", "true");
    var first = menu.querySelector('[aria-checked="true"]');
    if (first) first.focus();
  });
  $("mode-menu").addEventListener("click", function (event) { event.stopPropagation(); });
  $("mode-menu").addEventListener("keydown", function (event) {
    var items = [].slice.call($("mode-menu").querySelectorAll(".theme-item"));
    var at = items.indexOf(document.activeElement);
    if (event.key === "ArrowDown" || event.key === "ArrowUp") {
      event.preventDefault();
      items[(at + (event.key === "ArrowDown" ? 1 : items.length - 1)) % items.length].focus();
    } else if (event.key === "Escape") { closeModeMenu(); $("mode-btn").focus(); }
  });
  document.addEventListener("click", closeModeMenu);
  // Customize Vibe mode: a box each part, ticked when Vibe mode shows it;
  // a change shows at once, in Vibe mode.
  function openModeDialog() {
    var box = $("mode-groups");
    box.textContent = "";
    MODE_PARTS.forEach(function (part) {
      var list = el("div", { class: "mode-group-items" });
      part.items.forEach(function (item) {
        var input = el("input", { type: "checkbox", value: item[0] });
        input.checked = viewMode.hides.indexOf(item[0]) < 0;
        input.addEventListener("change", function () {
          viewMode.hides = viewMode.hides.filter(function (key) { return key !== item[0]; });
          if (!input.checked) viewMode.hides.push(item[0]);
          viewMode.mode = "vibe";
          keepMode();
          applyMode();
        });
        list.appendChild(el("label", { class: "check" }, [input, document.createTextNode(" " + item[1])]));
      });
      box.appendChild(el("fieldset", { class: "mode-group" }, [el("legend", { text: part.group }), list]));
    });
    box.appendChild(el("p", { class: "muted small", text: "The chat always shows." }));
    if (!$("mode-dialog").open) $("mode-dialog").showModal();
  }
  $("mode-done").addEventListener("click", function () { $("mode-dialog").close(); });
  $("mode-reset").addEventListener("click", function () {
    viewMode.hides = VIBE_HIDES.slice();
    viewMode.mode = "vibe";
    keepMode();
    applyMode();
    openModeDialog();
  });
  var paneViews = {
    left: ["tasks", "spaces", "files", "search", "checks", "decisions", "people", "security"],
    right: ["git", "logs", "preview", "release", "data", "system"],
  };
  var paneIds = {
    left: { panel: "left-side", toggle: "left-toggle", close: "left-close", resize: "left-resize", width: 300 },
    right: { panel: "chat-side", toggle: "side-toggle", close: "side-close", resize: "side-resize", width: 340 },
  };
  function paneOf(view) { return paneViews.left.indexOf(view) >= 0 ? "left" : "right"; }
  // ---- The layout, each person's own, in this browser (hi 2.6.205):
  // three columns (the left panel, the chat, the right panel), or two:
  // the chat and one panel with every view, the panel on the right
  // ("two") or on the left ("two-left", hi 2.6.206).
  var LEFT_VIEWS = paneViews.left.slice(), RIGHT_VIEWS = paneViews.right.slice();
  var LAYOUTS = ["three", "two", "two-left"];
  var layout = "three";
  try { var keptLayout = localStorage.getItem("hi-dev-layout"); if (LAYOUTS.indexOf(keptLayout) >= 0) layout = keptLayout; } catch (e) { /* private window */ }
  // onePane is the panel with every view, in two panels.
  function onePane() { return layout === "two" ? "right" : layout === "two-left" ? "left" : ""; }
  // placeLayout moves the icons and views into their panels: each its
  // own, or all into one, the left panel's first.
  function placeLayout() {
    var one = onePane(), all = LEFT_VIEWS.concat(RIGHT_VIEWS);
    paneViews.left = one === "left" ? all : one === "right" ? [] : LEFT_VIEWS.slice();
    paneViews.right = one === "right" ? all : one === "left" ? [] : RIGHT_VIEWS.slice();
    ["left", "right"].forEach(function (which) {
      var side = $(paneIds[which].panel), tabs = side.querySelector(".side-tabs"), end = tabs.querySelector(".grow");
      paneViews[which].forEach(function (view) {
        tabs.insertBefore(document.querySelector('.side-tabs button[data-side="' + view + '"]'), end);
        side.appendChild($("side-" + view));
      });
    });
    document.body.classList.toggle("layout-one", !!one);
    var everything = "The panel: tasks, workspaces, preview, publish, and all other views";
    $("side-toggle").title = one === "right" ? everything : "Rewind, source control, logs, preview, publish, data, and system";
    $("left-toggle").title = one === "left" ? everything : "Tasks, workspaces, files, search, checks, decisions, users, and keys";
  }
  placeLayout();
  // setLayout switches it; the views open stay open, in their new panels.
  function setLayout(next) {
    if (next === layout) return;
    var open = ["left", "right"].filter(function (w) { return panes[w].open && paneViews[w].length; }).map(function (w) { return panes[w].view; });
    var widest = Math.max(panes.left.width, panes.right.width);
    layout = next;
    try { localStorage.setItem("hi-dev-layout", layout); } catch (e) { /* private window: this page only */ }
    placeLayout();
    var one = onePane();
    if (one) {
      var pane = panes[one];
      if (open.length && open.indexOf(pane.view) < 0) pane.view = open[0];
      pane.open = open.length > 0;
      pane.width = widest;
    } else {
      open.forEach(function (view) { var w = paneOf(view); panes[w].view = view; panes[w].open = true; });
      ["left", "right"].forEach(function (w) { if (paneViews[w].indexOf(panes[w].view) < 0) panes[w].view = paneViews[w][0]; });
    }
    keepSide();
    renderSide();
    sideShown();
  }
  var LAYOUT_ICONS = {
    three: '<svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><rect x="3" y="4" width="18" height="16" rx="2"/><path d="M8.5 4v16"/><path d="M15.5 4v16"/></svg>',
    two: '<svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><rect x="3" y="4" width="18" height="16" rx="2"/><path d="M14 4v16"/></svg>',
    "two-left": '<svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><rect x="3" y="4" width="18" height="16" rx="2"/><path d="M10 4v16"/></svg>',
  };
  $("layout-btn").innerHTML = LAYOUT_ICONS.three;
  function drawLayoutMenu() {
    var menu = $("layout-menu");
    menu.textContent = "";
    menu.appendChild(el("div", { class: "theme-head", text: "Panels" }));
    [["three", "Three panels", "Tasks and workspaces on the left, the chat in the middle, other views on the right"],
     ["two", "Chat left, panel right", "The chat on the left, one panel with all views on the right"],
     ["two-left", "Panel left, chat right", "One panel with all views on the left, the chat on the right"]].forEach(function (l) {
      var on = layout === l[0];
      var icon = el("span", { class: "mode-icon", "aria-hidden": "true" });
      icon.innerHTML = LAYOUT_ICONS[l[0]];
      menu.appendChild(el("button", { type: "button", role: "menuitemradio", "aria-checked": on ? "true" : "false", class: "theme-item mode-item", "data-layout": l[0],
        onclick: function () { setLayout(l[0]); closeLayoutMenu(); } }, [
        icon,
        el("span", { class: "mode-words" }, [el("span", { class: "theme-name", text: l[1] }), el("span", { class: "muted mode-about", text: l[2] })]),
        el("span", { class: "theme-check", text: on ? "✓" : "" }),
      ]));
    });
  }
  function closeLayoutMenu() {
    $("layout-menu").hidden = true;
    $("layout-btn").setAttribute("aria-expanded", "false");
  }
  $("layout-btn").addEventListener("click", function (event) {
    event.stopPropagation();
    var menu = $("layout-menu");
    if (!menu.hidden) { closeLayoutMenu(); return; }
    closeModeMenu();
    drawLayoutMenu();
    menu.hidden = false;
    $("layout-btn").setAttribute("aria-expanded", "true");
    var first = menu.querySelector('[aria-checked="true"]');
    if (first) first.focus();
  });
  $("layout-menu").addEventListener("click", function (event) { event.stopPropagation(); });
  $("layout-menu").addEventListener("keydown", function (event) {
    var items = [].slice.call($("layout-menu").querySelectorAll(".theme-item"));
    var at = items.indexOf(document.activeElement);
    if (event.key === "ArrowDown" || event.key === "ArrowUp") {
      event.preventDefault();
      items[(at + (event.key === "ArrowDown" ? 1 : items.length - 1)) % items.length].focus();
    } else if (event.key === "Escape") { closeLayoutMenu(); $("layout-btn").focus(); }
  });
  document.addEventListener("click", closeLayoutMenu);
  try {
    var keptPanes = JSON.parse(localStorage.getItem("hi-dev-panes") || "null");
    if (keptPanes && keptPanes.v === 1) {
      ["left", "right"].forEach(function (which) {
        var kept = keptPanes[which] || {};
        if (paneViews[which].indexOf(kept.view) >= 0) panes[which].view = kept.view;
        if (kept.open === false) panes[which].open = false;
        if (kept.width >= 240) panes[which].width = kept.width;
      });
    } else {
      // Before hi 2.6.76: one panel on the right, and the page's tabs.
      var keptSide = JSON.parse(localStorage.getItem("hi-dev-side") || "{}");
      if (keptSide.v === 2 && keptSide.view) panes[paneOf(keptSide.view)].view = keptSide.view;
      if (keptSide.open === false) panes.right.open = false;
      if (keptSide.width >= 240) panes.right.width = keptSide.width;
      var keptTab = localStorage.getItem("hi-dev-tab") || "";
      if (keptTab === "files") panes.left.view = "files";
      else if (keptTab && keptTab !== "terminal" && paneViews[paneOf(keptTab)].indexOf(keptTab) >= 0) {
        panes[paneOf(keptTab)].view = keptTab;
        panes[paneOf(keptTab)].open = true;
      }
    }
  } catch (e) { /* private window */ }
  if (sideOver.matches) { panes.left.open = false; panes.right.open = false; }
  // The source control view: what git last said of the chat's workspace,
  // and the file whose diff is open.
  var git = { view: null, space: "", busy: false, at: 0, diff: null, error: "" };
  // The files open in tabs, beside Chat and Terminal; the one
  // shown, and the view to go back to when the last closes.
  var opened = { tabs: [], current: "", back: "chat" };
  try {
    var keptTabs = JSON.parse(localStorage.getItem("hi-dev-open-files") || "{}");
    (keptTabs.tabs || []).forEach(function (t) { if (t && t.root && t.path && !tabOf(tabKey(t.root, t.path))) newTab(t.root, t.path); });
    if (tabOf(keptTabs.current)) opened.current = keptTabs.current;
    else if (opened.tabs.length) opened.current = opened.tabs[0].key;
  } catch (e) { /* private window */ }
  try {
    // Chat is the default since hi 2.5.24; the choice before it isn't kept.
    chat.view = localStorage.getItem("hi-dev-code-view-2") || chat.view;
    if (chat.view === "code") chat.view = "chat"; // VS Code, which hi had until 2.6.144
    chat.space = localStorage.getItem("hi-dev-chat-space") || "";
  } catch (e) { /* private window */ }
  function setCodeView(view) {
    // Files, in the bar: the file last open, else the first.
    if (view === "file" && !tabOf(opened.current) && opened.tabs.length) opened.current = opened.tabs[0].key;
    if (view === "file" && !tabOf(opened.current)) view = opened.back || "chat";
    if (view !== "file") opened.back = view;
    chat.view = view;
    try { localStorage.setItem("hi-dev-code-view-2", view); } catch (e) { /* private window */ }
    document.querySelectorAll("#code-view button").forEach(function (button) { button.setAttribute("aria-pressed", button.dataset.view === view ? "true" : "false"); });
    $("chat").hidden = view !== "chat";
    document.querySelector(".term-panel").hidden = view !== "terminal";
    $("editor").hidden = view !== "file";
    showChatStart();
    $("chat-title").hidden = view !== "chat";
    $("chat-status").hidden = view !== "chat";
    renderSide();
    renderTabs();
    if (view === "file") { showTab(opened.current); sideShown(); }
    else if (view === "terminal") { setTimeout(function () { fitTerminal(); term.focus(); }, 0); sideShown(); }
    else { pollChat(); sideShown(); }
  }
  document.querySelectorAll("#code-view button").forEach(function (button) {
    button.addEventListener("click", function () { setCodeView(button.dataset.view); });
  });
  // workSpaces are the pieces of work: a worktree, or an agent at work.
  // herdr's home (~) and the project's own clone are neither, and stay in
  // the terminal.
  function workSpaces() {
    return ((state && state.workspaces) || []).filter(function (space) { return space.path || space.agent; });
  }
  function renderChatSpaces() {
    var spaces = workSpaces();
    if (!spaces.some(function (space) { return space.id === chat.space; })) {
      var first = spaces.filter(function (space) { return space.agent === "claude"; })[0] || spaces[0];
      chooseChatSpace(first ? first.id : "", true);
    }
    showChatStart();
    if (!$("space-menu").hidden) drawSpaceMenu();
  }
  function chooseChatSpace(id, quiet) {
    if (id === chat.space && quiet) return;
    if (!quiet) chat.composing = false; // a workspace chosen leaves Start something new
    var changedSpace = id !== chat.space;
    if (changedSpace) flushDraft();
    chat.space = id;
    if (changedSpace) loadDraft(id);
    if (state) renderEditorBar();
    chat.sitesSig = null;
    $("chat-status").textContent = "";
    try { localStorage.setItem("hi-dev-chat-space", id); } catch (e) { /* private window */ }
    chat.session = "";
    chat.offset = 0;
    chat.turns = "";
    rewind.points = [];
    rewind.seen = "";
    rewind.open = null;
    renderQueue([]);
    clearChat();
    showChatStart();
    git.view = null;
    search.view = null;
    search.asked = "";
    renderSearch();
    closeDiff();
    renderGit();
    syncFilesRoot();
    sideShown();
    if (!quiet) pollChat();
  }
  // The start card: when there's no work to show, or + New was pressed.
  function showChatStart() {
    var starting = chat.composing || (!!state && !chat.space); // not before the workspaces are known
    $("chat-start").hidden = !starting;
    document.querySelector(".chat-main").hidden = starting;
    document.querySelector(".chat-col > .chat-bottom").hidden = starting;
    $("space-wrap").hidden = chat.view !== "chat";
    spaceLabel();
    $("adhoc-cancel").hidden = !chat.space;
    if (starting) $("chat-title").textContent = "";
    renderSide();
  }
  // + New atop Workspaces too: the chat's Start something new.
  $("spaces-new").addEventListener("click", function () {
    if (chat.view !== "chat") setCodeView("chat");
    startNew();
  });
  function startNew() {
    chat.composing = true;
    showChatStart();
    $("adhoc-text").focus();
  }
  $("adhoc-cancel").addEventListener("click", function () {
    chat.composing = false;
    showChatStart();
    pollChat();
  });
  // pictureBox makes a text box where a pasted, dropped, or picked file is
  // a pill in the words, Image #1 (File #2 for another kind), that ✕,
  // Backspace, or Delete takes out, with previews along the bottom. Its
  // value has each pill as [image: image-1.png], the name the task's file
  // gets, so whoever reads it (an agent too) knows which is meant. Used
  // by Start something new, New task, and a task's notes. With send,
  // Enter sends and Shift+Enter is a new line, as in the chat's box.
  function pictureBox(edit, shown, picker, send) {
    var files = {}, n = 0;
    function pills() { return [].slice.call(edit.querySelectorAll(".pic-pill")); }
    function value(node) {
      var out = "";
      [].forEach.call(node.childNodes, function (child) {
        if (child.nodeType === 3) out += child.nodeValue.replace(/ /g, " ");
        else if (child.nodeName === "BR") out += "\n";
        else if (child.classList.contains("pic-pill")) out += reference(files[child.dataset.n].file.name);
        else out += (out && !/\n$/.test(out) ? "\n" : "") + value(child);
      });
      return out;
    }
    function changed() {
      var all = pills();
      if (!all.length && !edit.textContent.trim() && edit.childNodes.length) edit.textContent = "";
      edit.parentNode.classList.toggle("filled", !!edit.childNodes.length);
      shown.textContent = "";
      shown.hidden = !all.length;
      all.forEach(function (pill) {
        var item = files[pill.dataset.n];
        shown.appendChild(el("div", { class: "chip", title: item.file.name + " (" + Math.max(1, Math.round(item.file.size / 1024)) + " KB)" }, [
          item.url ? el("img", { src: item.url, alt: pill.firstChild.nodeValue }) : el("span", { class: "chip-text", text: item.file.name }),
          el("span", { text: "#" + pill.dataset.n }),
          el("button", { type: "button", class: "link", text: "✕", title: "Leave it out", onclick: function () { pill.remove(); changed(); } })]));
      });
    }
    // put puts nodes where the cursor is, or at the end, and the cursor
    // after them.
    function put(nodes) {
      var selection = window.getSelection();
      var range = selection.rangeCount && edit.contains(selection.getRangeAt(0).commonAncestorContainer) ? selection.getRangeAt(0) : null;
      if (!range) { range = document.createRange(); range.selectNodeContents(edit); range.collapse(false); }
      range.deleteContents();
      nodes.forEach(function (node) { range.insertNode(node); range.setStartAfter(node); range.collapse(true); });
      selection.removeAllRanges();
      selection.addRange(range);
    }
    function add(list) {
      var nodes = [];
      [].slice.call(list || []).forEach(function (file) {
        var k = ++n, picture = /^image\//.test(file.type);
        var ext = (file.name.match(/\.[a-z0-9]+$/i) || [file.type === "image/jpeg" ? ".jpg" : ".png"])[0].toLowerCase();
        var named = picture && /^image(\.[a-z]+)?$/i.test(file.name.replace(/\.[a-z0-9]+$/i, "")) ? new File([file], "image-" + k + ext, { type: file.type }) : file;
        files[k] = { file: named, url: picture ? URL.createObjectURL(file) : "" };
        nodes.push(document.createTextNode(" "), el("span", { class: "pic-pill", contenteditable: "false", "data-n": k }, [
          document.createTextNode((picture ? "Image #" : "File #") + k), el("button", { type: "button", tabindex: "-1", text: "✕", title: "Leave it out" })]), document.createTextNode(" "));
      });
      if (!nodes.length) return false;
      put(nodes);
      changed();
      return true;
    }
    edit.addEventListener("input", changed);
    // A pill's ✕ (one put back by Undo too), without taking the cursor.
    edit.addEventListener("mousedown", function (event) {
      if (event.target.closest(".pic-pill button")) event.preventDefault();
    });
    edit.addEventListener("click", function (event) {
      var out = event.target.closest(".pic-pill button");
      if (out) { out.parentNode.remove(); changed(); }
    });
    // Pasted text goes in as text, never as another page's markup.
    edit.addEventListener("paste", function (event) {
      var clip = event.clipboardData || {};
      event.preventDefault();
      if (add(clip.files)) return;
      document.execCommand("insertText", false, (clip.getData ? clip.getData("text/plain") : "").replace(/\r\n/g, "\n"));
    });
    edit.addEventListener("dragover", function (event) {
      if (event.dataTransfer && [].indexOf.call(event.dataTransfer.types, "Files") >= 0) event.preventDefault();
    });
    edit.addEventListener("drop", function (event) {
      if (!event.dataTransfer || !event.dataTransfer.files.length) return;
      event.preventDefault();
      add(event.dataTransfer.files);
    });
    edit.parentNode.addEventListener("click", function (event) {
      if (event.target === this) edit.focus();
    });
    // Enter is a new line; Ctrl+Enter sends.
    edit.addEventListener("keydown", function (event) {
      if (event.key !== "Enter" || event.isComposing) return;
      event.preventDefault();
      if (send && !event.shiftKey && !narrow) send();
      else if (event.ctrlKey || event.metaKey) {
        var form = edit.closest("form");
        if (form) form.requestSubmit();
        else if (send) send();
      }
      else document.execCommand("insertLineBreak");
    });
    if (picker) picker.addEventListener("change", function () {
      edit.focus();
      add(picker.files);
      picker.value = "";
    });
    return {
      value: function () { return value(edit).replace(/ +(\[(?:image|file): [^\]]+\]) +/g, " $1 ").trim(); },
      read: function () {
        return Promise.all(pills().map(function (pill) {
          var file = files[pill.dataset.n].file;
          return new Promise(function (resolve, reject) {
            var reader = new FileReader();
            reader.onload = function () { resolve({ name: file.name, data: String(reader.result).split(",")[1] || "" }); };
            reader.onerror = function () { reject(new Error("cannot read " + file.name)); };
            reader.readAsDataURL(file);
          });
        }));
      },
      // append adds text at the end, on a line of its own.
      append: function (text) {
        var now = value(edit).replace(/\s*$/, "");
        edit.focus();
        var range = document.createRange();
        range.selectNodeContents(edit);
        range.collapse(false);
        window.getSelection().removeAllRanges();
        window.getSelection().addRange(range);
        document.execCommand("insertText", false, (now ? "\n\n" : "") + text);
      },
      clear: function () {
        Object.keys(files).forEach(function (k) { if (files[k].url) URL.revokeObjectURL(files[k].url); });
        files = {};
        n = 0;
        edit.textContent = "";
        changed();
      },
    };
  }
  // Start something new: the chat's own box, where the chat's is, with
  // the agent to pick in place of the model. Sent, the chat it starts
  // takes its place, so the box stays where it was.
  var adhoc = pictureBox($("adhoc-text"), $("adhoc-pictures"), $("adhoc-files"), adhocStart);
  function adhocReady() {
    $("adhoc-go").classList.toggle("ready", !!adhoc.value());
    $("adhoc-form").querySelector(".spark").classList.toggle("other", $("adhoc-agent").value !== "claude");
  }
  $("adhoc-text").addEventListener("input", adhocReady);
  $("adhoc-agent").addEventListener("change", function () { adhocReady(); $("adhoc-text").focus(); });
  // Not a form: Safari on an iPad puts its AutoFill bar (passwords,
  // cards, addresses) over the keyboard for a box in a form with inputs.
  $("adhoc-go").addEventListener("click", adhocStart);
  function adhocStart() {
    if ($("adhoc-go").disabled) return;
    var agent = $("adhoc-agent").value || "claude";
    var text = adhoc.value();
    if (!text) { $("adhoc-error").textContent = "Write what to do."; $("adhoc-text").focus(); return; }
    $("adhoc-go").disabled = true;
    $("adhoc-status").textContent = "Making the worktree…";
    $("adhoc-error").textContent = "";
    adhoc.read().then(function (files) {
      return api("POST", "start-new", { text: text, agent: agent, files: files });
    }).then(function (answer) {
      chat.composing = false;
      adhoc.clear();
      toast((answer.warning ? answer.warning + " " : "") + "Made task #" + answer.ticket + " and started " + answer.branch + " with " + agent + ".");
      refreshTickets(true);
      return refresh().then(function () {
        if (answer.workspace) chooseChatSpace(answer.workspace);
        if (!narrow) $("chat-input").focus();
      });
    }).catch(function (error) {
      $("adhoc-error").textContent = error.message;
    }).then(function () {
      $("adhoc-go").disabled = false;
      $("adhoc-status").textContent = "";
      adhocReady();
    });
  }
  function clearChat() {
    chatActivity(null);
    chatAsk(null);
    $("chat-entries").textContent = "";
    if (state) { chat.sitesSig = null; chatSites(); }
    chat.tools = {};
    chat.group = null;
    chat.lastAt = 0;
    chat.pending = [];
    chat.resp = newResponse();
    $("chat-title").textContent = "";
  }
  var verbs = {
    Bash: ["Ran a command", "Ran %d commands"], Read: ["Read a file", "Read %d files"], Edit: ["Edited a file", "Edited %d files"],
    MultiEdit: ["Edited a file", "Edited %d files"], Write: ["Wrote a file", "Wrote %d files"], NotebookEdit: ["Edited a notebook", "Edited %d notebooks"],
    Grep: ["Searched", "Searched %d times"], Glob: ["Looked for files", "Looked for files %d times"], WebFetch: ["Read a page", "Read %d pages"],
    WebSearch: ["Searched the web", "Searched the web %d times"], Task: ["Ran an agent", "Ran %d agents"], Agent: ["Ran an agent", "Ran %d agents"],
    TodoWrite: ["Updated the to-do list", "Updated the to-do list %d times"], AskUserQuestion: ["Asked you a question", "Asked you %d questions"],
  };
  // A step's icon, by what kind of thing its tool does.
  var toolKinds = {
    Read: "read", Edit: "edit", MultiEdit: "edit", Write: "edit", NotebookEdit: "edit", Bash: "run",
    Grep: "find", Glob: "find", WebSearch: "find", WebFetch: "web", Task: "agent", Agent: "agent",
    TodoWrite: "todo", AskUserQuestion: "ask",
  };
  var toolPaths = {
    read: "M3.5 1.5h6l3 3v10h-9z M9.5 1.5v3h3 M5.5 8.5h5 M5.5 11h5",
    edit: "M10.5 2.5l3 3-8 8H2.5v-3z M9 4l3 3",
    run: "M1.5 2.5h13v11h-13z M4 6l2.5 2L4 10 M8 10.5h4",
    find: "M7 2.5a4.5 4.5 0 1 1 0 9 4.5 4.5 0 0 1 0-9z M10.3 10.3l3.7 3.7",
    web: "M8 1.5a6.5 6.5 0 1 1 0 13 6.5 6.5 0 0 1 0-13z M1.5 8h13 M8 1.5c2 2 2 11 0 13 M8 1.5c-2 2-2 11 0 13",
    agent: "M8 1.5v13 M1.5 8h13 M3.4 3.4l9.2 9.2 M12.6 3.4l-9.2 9.2",
    todo: "M6 4h8 M6 8h8 M6 12h8 M2 4h1.5 M2 8h1.5 M2 12h1.5",
    ask: "M2 2.5h12v8.5H8l-3 3v-3H2z",
    other: "M8 5.5a2.5 2.5 0 1 1 0 5 2.5 2.5 0 0 1 0-5z",
  };
  function toolIcon(name) {
    var svg = document.createElementNS("http://www.w3.org/2000/svg", "svg");
    svg.setAttribute("viewBox", "0 0 16 16");
    svg.setAttribute("aria-hidden", "true");
    var shape = document.createElementNS("http://www.w3.org/2000/svg", "path");
    shape.setAttribute("d", toolPaths[toolKinds[name] || "other"]);
    shape.setAttribute("fill", "none");
    shape.setAttribute("stroke", "currentColor");
    shape.setAttribute("stroke-width", "1.3");
    shape.setAttribute("stroke-linecap", "round");
    shape.setAttribute("stroke-linejoin", "round");
    svg.appendChild(shape);
    return el("span", { class: "tool-icon" }, [svg]);
  }
  function groupSummary(counts) {
    return Object.keys(counts).map(function (name) {
      var verb = verbs[name] || ["Used " + name, "Used " + name + " %d times"];
      return counts[name] === 1 ? verb[0] : verb[1].replace("%d", counts[name]);
    }).join(", ");
  }
  // How long a stretch of work took, as Orca's fold says it: "14s",
  // "2m 14s", "1h 3m".
  function workedFor(ms) {
    var s = Math.round(ms / 1000);
    if (s < 60) return s + "s";
    if (s < 3600) return Math.floor(s / 60) + "m " + (s % 60) + "s";
    return Math.floor(s / 3600) + "h " + Math.floor((s % 3600) / 60) + "m";
  }
  // A group of steps' line: how long they took, from what came before them
  // to their last step, then what they did.
  function groupLine(group) {
    var took = group.start && group.end > group.start ? group.end - group.start : 0;
    group.summary.textContent = (took >= 1000 ? "Worked for " + workedFor(took) + " · " : "") + groupSummary(group.counts);
  }
  function chatEntry(entry) {
    var box = $("chat-entries");
    if (entry.t === "title") { $("chat-title").textContent = entry.text; return; }
    var at = entry.at ? Date.parse(entry.at) : 0;
    if (entry.t === "result") {
      var tool = chat.tools[entry.id];
      if (at && chat.group && tool && chat.group.body.contains(tool.box)) { chat.group.end = Math.max(chat.group.end, at); groupLine(chat.group); }
      if (at) chat.lastAt = at;
      if (!tool) return;
      tool.result.textContent = entry.text || "(nothing)";
      if (tool.edit && /created/i.test(entry.text || "") && !entry.error) { tool.edit.created = true; renderChanges(chat.resp); }
      if (tool.edit && entry.error) { tool.edit.failed = true; renderChanges(chat.resp); }
      tool.result.hidden = false;
      if (entry.error) { tool.box.classList.add("failed"); tool.result.classList.add("failed"); }
      return;
    }
    if (entry.t === "tool") {
      if (!chat.group) {
        var summary = el("summary");
        var body = el("div", { class: "fold-body" });
        var start = chat.lastAt || chat.turn || 0;
        chat.group = { counts: {}, summary: summary, body: body, start: start, end: start, images: null };
        box.appendChild(el("details", { class: "fold tools" }, [summary, body]));
      }
      chat.group.counts[entry.name] = (chat.group.counts[entry.name] || 0) + 1;
      if (at) { chat.group.end = Math.max(chat.group.end, at); chat.lastAt = at; }
      groupLine(chat.group);
      var result = el("pre", { class: "result", hidden: "" });
      var item = el("details", { class: "fold tool" }, [
        el("summary", {}, [toolIcon(entry.name), el("span", { class: "tool-name", text: entry.name }), el("span", { class: "tool-what", text: entry.summary || "" })]),
        entry.detail ? el("pre", { text: entry.detail }) : null,
        result,
      ]);
      chat.tools[entry.id] = { box: item, result: result, edit: noteEdit(entry) };
      chat.group.body.appendChild(item);
      if ((entry.images || []).length) {
        if (!chat.group.images) { chat.group.images = imageStrip([]); box.appendChild(chat.group.images); }
        addImages(chat.group.images, entry.images);
      }
      return;
    }
    chat.group = null;
    if (at) chat.lastAt = at;
    if (entry.t === "user") {
      placeChanges();
      chat.resp = newResponse();
      if (entry.at) chat.turn = Date.parse(entry.at);
      var sent = chat.pending.shift();
      if (sent) sent.node.remove();
      box.appendChild(userTurn(entry));
    } else if (entry.t === "text") {
      box.appendChild(replyTurn(entry));
    } else if (entry.t === "thinking") {
      box.appendChild(el("details", { class: "fold thought" }, [el("summary", { text: "Thought" }), el("div", { class: "fold-body" }, [el("pre", { text: entry.text })])]));
    } else if (entry.t === "note") {
      // A slash command or a shell line sent from the chat comes back as a
      // note, not as the person's message.
      if (chat.pending.length && /^[\/!]/.test(chat.pending[0].text || "")) chat.pending.shift().node.remove();
      box.appendChild(el("div", { class: "chat-note", text: entry.text }));
    }
  }
  function chatState(view) {
    var agent = view.agent || {};
    chat.agent = agent;
    chat.id = view.id || "";
    $("chat-status").textContent = agent.running && agent.status === "blocked" && agent.waiting ? "Waits for " + agent.waiting : "";
    chatActivity(agent.running && agent.status === "working" ? (view.activity || (agent.tool ? "Running " + agent.tool + "…" : "")) : null);
    var asking = agent.running && agent.status === "blocked";
    chatAsk(asking ? view.ask : null, asking ? view.screen : "", agent.agent);
    $("chat-waiting").hidden = !(asking && view.screen && !view.ask);
    if (!$("chat-waiting").hidden) $("chat-screen").textContent = view.screen;
    var stopped = !agent.running;
    $("chat-stopped").hidden = !stopped && !view.note;
    chat.sessionAgent = view.session_agent || "claude";
    var said = agentName(agent.running ? agent.agent : chat.sessionAgent);
    $("chat-stopped-why").textContent = view.note || said + " is not running in this workspace.";
    $("chat-input").placeholder = "Message " + (agent.running ? agentName(agent.agent) : said) + "… Paste or drop pictures and files";
    $("chat-resume").hidden = !(stopped && view.id && !view.note);
    $("chat-input").disabled = stopped || !!view.note;
    $("chat-send").disabled = stopped || !!view.note;
    $("chat-stop").hidden = !(agent.running && agent.status === "working");
    chatPick(view, agent);
    // Without the model and effort to pick, the row says who you talk to.
    $("chat-who").textContent = $("chat-pick").hidden && !stopped && !view.note ? agentName(agent.agent) : "";
    chatReady();
    chatPending(agent);
  }
  // The model and effort, beside the message box, when the app's admin
  // lets its developers pick them: Claude Code's /model and /effort,
  // from the next message.
  var chatModels = [["fable", "Fable"], ["opus", "Opus"], ["sonnet", "Sonnet"], ["haiku", "Haiku"]];
  var chatEfforts = [["low", "Low"], ["medium", "Medium"], ["high", "High"], ["xhigh", "XHigh"], ["max", "Max"]];
  function chatPick(view, agent) {
    var shown = !!view.choose && agent.agent === "claude" && !view.note;
    $("chat-pick").hidden = !shown;
    if (!shown) return;
    var busy = agent.status === "working" || agent.status === "blocked";
    var now = /^claude-([a-z]+)-(\d+)-(\d+)/.exec(view.model || "");
    fillPick($("chat-model"), chatModels, now ? now[1] : "", "Model", function (value, label) { return now && value === now[1] ? label + " " + now[2] + "." + now[3] : label; }, busy);
    fillPick($("chat-effort"), chatEfforts, view.effort || "", "Effort", null, busy);
  }
  function fillPick(select, options, current, empty, label, busy) {
    select.disabled = busy;
    select.title = busy ? "Claude is busy. Change this when Claude is done." : select.getAttribute("aria-label") === "The model" ? "The model that Claude uses, from the next message" : "How much Claude thinks, from the next message";
    if (document.activeElement === select) return;
    var key = current + "|" + options.map(function (o) { return label ? label(o[0], o[1]) : o[1]; }).join(",");
    if (select.dataset.key === key) return;
    select.dataset.key = key;
    select.textContent = "";
    if (!current) select.appendChild(el("option", { value: "", text: empty }));
    options.forEach(function (o) {
      var option = el("option", { value: o[0], text: label ? label(o[0], o[1]) : o[1] });
      if (o[0] === current) option.selected = true;
      select.appendChild(option);
    });
    if (!current) select.value = "";
  }
  ["chat-model", "chat-effort"].forEach(function (id) {
    $(id).addEventListener("change", function () {
      var select = $(id);
      if (!select.value) return;
      var body = { workspace: chat.space, session: chat.id };
      body[id === "chat-model" ? "model" : "effort"] = select.value;
      select.disabled = true;
      api("POST", "chat/model", body).then(function () {
        select.dataset.key = "";
        select.blur();
        pollChat();
      }).catch(function (error) {
        select.dataset.key = "";
        toast(error.message);
        select.blur();
        pollChat();
      });
    });
  });
  // chatPending keeps what was sent until the transcript has it. Claude
  // Code holds a message sent while a tool runs until the tool ends, which
  // can be many minutes, so it waits as queued while Claude works; one
  // that hasn't come 20 seconds after Claude stopped working never will.
  // It stays, marked, with its words to put back: a dialog in the terminal
  // the chat didn't see may have taken it as keys.
  function chatPending(agent) {
    var working = agent.running && (agent.status === "working" || agent.status === "blocked");
    chat.pending = chat.pending.filter(function (sent) {
      if (working) sent.since = Date.now();
      sent.node.classList.toggle("queued", !!working);
      if (Date.now() - sent.since < 20000) return true;
      notTaken(sent, agent);
      return false;
    });
  }
  function notTaken(sent, agent) {
    var node = sent.node;
    node.classList.remove("pending", "queued");
    node.classList.add("lost");
    var put = el("button", { type: "button", class: "link", text: "Put it back in the box", onclick: function () {
      var input = $("chat-input");
      input.value = sent.text + (input.value.trim() ? "\n\n" + input.value : "");
      input.dispatchEvent(new Event("input")); // its size, Send, and the draft
      input.focus();
      node.remove();
    } });
    node.appendChild(el("div", { class: "lost-note" }, [
      document.createTextNode(agentName(agent.agent) + " did not get this message. The terminal shows what it does now. "),
      sent.text ? put : null,
    ]));
  }
  // chatActivity is the conversation's last line while Claude works: what
  // its terminal says ("Crafting… (7s · ↓ 556 tokens)") when hi could read
  // it, else how long since your message, after a turning ring (dev.css).
  function chatActivity(text) {
    var line = $("chat-activity");
    if (text === null) {
      if (line) line.remove();
      return;
    }
    if (!line) {
      line = el("div", { id: "chat-activity", class: "chat-activity", role: "status" }, [el("span", { class: "spin", "aria-hidden": "true" }), el("span", { class: "act-text" })]);
    }
    $("chat-entries").appendChild(line); // always the last line
    chat.activity = text;
    var seconds = Math.round((Date.now() - (chat.turn || Date.now())) / 1000);
    line.lastChild.textContent = text || (seconds >= 0 && seconds < 3600 ? "Working… (" + seconds + "s)" : "Working…");
  }
  // ---- The files a response changed, as a card at its end, as in Orca:
  // "2 changed files +40 −3", each file Added or Edited with its lines,
  // and a click on one shows its changes. From the agent's Edit, Write,
  // and MultiEdit calls, whose inputs say what went in and what came out.
  var EDIT_TOOLS = { Edit: true, Write: true, MultiEdit: true };
  function newResponse() { return { files: {}, order: [], card: null }; }
  // lineDiff is a line diff of two texts, as ["+"|"-"|" ", line] pairs;
  // past a size it's all out and all in.
  function lineDiff(before, after) {
    var a = before === "" ? [] : before.split("\n"), b = after === "" ? [] : after.split("\n");
    if (a.length * b.length > 250000) return a.map(function (l) { return ["-", l]; }).concat(b.map(function (l) { return ["+", l]; }));
    var n = a.length, m = b.length, lcs = [];
    for (var i = 0; i <= n; i++) { lcs.push(new Array(m + 1).fill(0)); }
    for (i = n - 1; i >= 0; i--) for (var j = m - 1; j >= 0; j--) lcs[i][j] = a[i] === b[j] ? lcs[i + 1][j + 1] + 1 : Math.max(lcs[i + 1][j], lcs[i][j + 1]);
    var out = [];
    i = 0; j = 0;
    while (i < n || j < m) {
      if (i < n && j < m && a[i] === b[j]) { out.push([" ", a[i]]); i++; j++; }
      else if (i < n && (j === m || lcs[i + 1][j] >= lcs[i][j + 1])) { out.push(["-", a[i]]); i++; }
      else { out.push(["+", b[j]]); j++; }
    }
    return out;
  }
  // noteEdit adds an editing tool call to the response's files.
  function noteEdit(entry) {
    if (!EDIT_TOOLS[entry.name]) return null;
    var input;
    try { input = JSON.parse(entry.detail || ""); } catch (e) { input = null; }
    var path = (input && input.file_path) || entry.summary || "";
    if (!path) return null;
    var hunks = [];
    if (input && entry.name === "Write") hunks.push(lineDiff("", String(input.content || "")));
    else if (input && entry.name === "Edit") hunks.push(lineDiff(String(input.old_string || ""), String(input.new_string || "")));
    else if (input && entry.name === "MultiEdit") (input.edits || []).forEach(function (e) { hunks.push(lineDiff(String(e.old_string || ""), String(e.new_string || ""))); });
    var resp = chat.resp || (chat.resp = newResponse());
    var file = resp.files[path];
    if (!file) { file = resp.files[path] = { path: path, shown: entry.summary || path, edits: [] }; resp.order.push(path); }
    var edit = { name: entry.name, hunks: hunks, known: !!input, file: file };
    file.edits.push(edit);
    renderChanges(resp);
    return edit;
  }
  function fileCounts(file) {
    var added = 0, removed = 0;
    file.edits.forEach(function (edit) {
      if (edit.failed) return;
      edit.hunks.forEach(function (hunk) { hunk.forEach(function (line) { if (line[0] === "+") added++; else if (line[0] === "-") removed++; }); });
    });
    return { added: added, removed: removed };
  }
  function countSpans(c) {
    return [c.added ? el("span", { class: "plus", text: "+" + c.added }) : null, c.removed ? el("span", { class: "minus", text: "−" + c.removed }) : null];
  }
  function renderChanges(resp) {
    var files = resp.order.map(function (path) { return resp.files[path]; }).filter(function (file) {
      return file.edits.some(function (edit) { return !edit.failed; });
    });
    if (!files.length) { if (resp.card) resp.card.hidden = true; return; }
    if (!resp.card) resp.card = el("div", { class: "changes-card" });
    var card = resp.card, open = {};
    card.hidden = false;
    card.querySelectorAll("details[open]").forEach(function (d) { open[d.dataset.path] = true; });
    var total = { added: 0, removed: 0 };
    var rows = files.map(function (file) {
      var c = fileCounts(file);
      total.added += c.added; total.removed += c.removed;
      var created = file.edits[0].name === "Write" && file.edits[0].created;
      var parts = file.shown.split("/"), name = parts.pop();
      var body = el("div", { class: "change-body" });
      var text = [];
      file.edits.forEach(function (edit) {
        if (edit.failed) return;
        if (!edit.known) { body.appendChild(el("div", { class: "meta", text: "The change is too long to show here. Source control has it." })); return; }
        edit.hunks.forEach(function (hunk, k) {
          if (body.childNodes.length) body.appendChild(el("div", { class: "hunk", text: "⋯" }));
          hunk.forEach(function (line) {
            body.appendChild(el("div", { class: line[0] === "+" ? "add" : line[0] === "-" ? "del" : "", text: line[0] + " " + line[1] }));
            if (line[0] !== " ") text.push(line[0] + line[1]);
          });
        });
      });
      var copy = iconButton(ICON_COPY, "Copy the changes", "Copy the changes to " + name, function (event) { event.preventDefault(); copyText(text.join("\n")); });
      return el("details", { class: "change-file", "data-path": file.path, open: open[file.path] ? "" : null }, [
        el("summary", { title: file.path }, [
          el("span", { class: "change-verb", text: created ? "Added" : "Edited" }),
          el("span", { class: "change-name", text: name }),
          el("span", { class: "change-dir", text: parts.join("/") }),
        ].concat(countSpans(c), [el("span", { class: "grow" }), copy])),
        el("pre", { class: "diff change-diff" }, [body]),
      ]);
    });
    card.textContent = "";
    card.appendChild(el("div", { class: "changes-head" }, [el("span", { text: files.length === 1 ? "1 changed file" : files.length + " changed files" })].concat(countSpans(total))));
    rows.forEach(function (row) { card.appendChild(row); });
  }
  // placeChanges puts the response's card at its end: before the line
  // that says the agent works.
  function placeChanges() {
    var resp = chat.resp;
    if (!resp || !resp.card || resp.card.hidden) return;
    var box = $("chat-entries"), activity = $("chat-activity");
    if (activity && activity.parentNode === box) box.insertBefore(resp.card, activity);
    else box.appendChild(resp.card);
  }
  // ---- Your message in the chat: the bubble, and under it Copy, the time
  // it was sent, and Rewind, as in Orca's chat. The row shows when the
  // pointer is on the message (always on a touch screen).
  var ICON_COPY = '<svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><rect x="9" y="9" width="12" height="12" rx="2"/><path d="M5 15H4a1 1 0 0 1-1-1V4a1 1 0 0 1 1-1h10a1 1 0 0 1 1 1v1"/></svg>';
  var ICON_REWIND = '<svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M9 14 4 9l5-5"/><path d="M4 9h10.5a5.5 5.5 0 0 1 0 11H11"/></svg>';
  function copyButton(text, label) {
    var button = el("button", { type: "button", class: "msg-act", title: "Copy", "aria-label": label, onclick: function () { copyText(text); } });
    button.innerHTML = ICON_COPY;
    return button;
  }
  // The agent's reply, with Copy under it, as under your messages.
  // Its row: Copy, Fork (a new chat from here, Claude Code's only), ↑ to
  // the start of the response, and the time, as in Orca.
  var ICON_FORK = '<svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><circle cx="6" cy="5" r="2.2"/><circle cx="18" cy="5" r="2.2"/><circle cx="12" cy="19" r="2.2"/><path d="M6 7.2v1.3a3 3 0 0 0 3 3h6a3 3 0 0 0 3-3V7.2"/><path d="M12 11.5v5.3"/></svg>';
  var ICON_UP = '<svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M12 19V5"/><path d="m5 12 7-7 7 7"/></svg>';
  function iconButton(icon, title, label, onclick) {
    var button = el("button", { type: "button", class: "msg-act", title: title, "aria-label": label, onclick: onclick });
    button.innerHTML = icon;
    return button;
  }
  function msgTime(at) {
    var when = at ? new Date(at) : null;
    return when && !isNaN(when) ? el("time", { class: "msg-time", datetime: at, title: when.toLocaleString(), text: when.toLocaleTimeString([], { hour: "numeric", minute: "2-digit" }) }) : null;
  }
  function replyTurn(entry) {
    var turn = el("div", { class: "msg-turn reply" });
    turn.replyText = entry.text || "";
    var fork = entry.uuid && chat.sessionAgent !== "codex" && chat.sessionAgent !== "agy"
      ? iconButton(ICON_FORK, "Fork from this response. The new chat includes this whole response, up to the end of its turn.", "Fork from this response", function () { forkFrom(entry); })
      : null;
    var up = iconButton(ICON_UP, "Go to the start of this response", "Go to the start of this response", function () { responseStart(turn); });
    turn.appendChild(el("div", { class: "msg-text" }, [renderMarkdown(entry.text, "")]));
    if ((entry.images || []).length) turn.appendChild(imageStrip(entry.images));
    var copy = iconButton(ICON_COPY, "Copy", "Copy the response", function () { copyText(responseText(turn)); });
    turn.appendChild(el("div", { class: "msg-meta" }, [copy, fork, up, msgTime(entry.at)]));
    return turn;
  }
  // A response is the replies between two of your messages; its row is
  // under its last reply only, as in Orca (markResponses).
  function responseText(turn) {
    var texts = [];
    for (var node = turn; node && !(node.classList.contains("msg-turn") && !node.classList.contains("reply")); node = node.previousElementSibling) {
      if (node.replyText) texts.unshift(node.replyText);
    }
    return texts.join("\n\n");
  }
  function markResponses() {
    var last = null;
    [].forEach.call($("chat-entries").children, function (node) {
      if (!node.classList.contains("msg-turn")) return;
      if (node.classList.contains("reply")) {
        if (last) last.classList.remove("end");
        node.classList.add("end");
        last = node;
      } else last = null;
    });
  }
  // responseStart scrolls to where the response began: after the message
  // that asked for it.
  function responseStart(turn) {
    var node = turn;
    while (node.previousElementSibling && !(node.previousElementSibling.classList.contains("msg-turn") && !node.previousElementSibling.classList.contains("reply"))) node = node.previousElementSibling;
    node.scrollIntoView({ block: "start", behavior: "smooth" });
  }
  // forkFrom starts a new chat from a response (app_devfork.go): the
  // conversation up to the end of its turn, in a new Claude Code session
  // in this workspace. The chat before stays, in the session menu.
  function forkFrom(entry) {
    ask("Fork from this response? hi starts a new chat. It has the conversation up to the end of this response's turn. The agent stops the current chat first. You can open the current chat again from the session menu at the top.").then(function (answer) {
      if (answer === null) return;
      toast("Forking…", true);
      api("POST", "chat/fork", { workspace: chat.space, uuid: entry.uuid }).then(function () {
        toast("Forked. The new chat goes on from that response.");
        chat.offset = 0;
        chat.session = "";
        chat.turns = "";
        pollChat();
        loadPoints();
      }).catch(function (error) { toast(error.message); });
    });
  }
  // ---- Pictures the session names: a file a tool read or wrote, or one a
  // message names (app_devchat.go, appDevImages), as thumbnails, as in
  // Orca. A click shows it whole. One that isn't there goes quietly.
  function imageURL(path) { return "api/chat/image?path=" + encodeURIComponent(path); }
  function imageStrip(paths) {
    var strip = el("div", { class: "chat-images" });
    addImages(strip, paths);
    return strip;
  }
  function addImages(strip, paths) {
    paths.forEach(function (path) {
      if (strip.querySelector('[data-path="' + CSS.escape(path) + '"]')) return;
      var name = path.split("/").pop();
      var img = el("img", { src: imageURL(path), alt: name, loading: "lazy" });
      var thumb = el("button", { type: "button", class: "chat-image", "data-path": path, title: path, "aria-label": "Show " + name, onclick: function () { showImage(path); } }, [img]);
      img.addEventListener("error", function () { thumb.remove(); if (!strip.childNodes.length) strip.hidden = true; });
      strip.appendChild(thumb);
    });
  }
  function showImage(path) {
    var dialog = $("image-dialog");
    $("image-full").src = imageURL(path);
    $("image-full").alt = path.split("/").pop();
    $("image-path").textContent = path;
    $("image-open").href = imageURL(path);
    dialog.showModal();
  }
  $("image-close").addEventListener("click", function () { $("image-dialog").close(); });
  $("image-dialog").addEventListener("click", function (event) { if (event.target === $("image-dialog")) $("image-dialog").close(); });
  // A code block, with Copy in its corner.
  function codeBlock(text) {
    var copy = copyButton(text, "Copy the code");
    copy.classList.add("code-copy");
    return el("div", { class: "code-wrap" }, [el("pre", {}, [el("code", { text: text })]), copy]);
  }
  function userTurn(entry) {
    var copy = copyButton(entry.text || "", "Copy the message");
    var back = el("button", { type: "button", class: "msg-act msg-rewind", title: "Rewind to here", "aria-label": "Rewind to before this message", hidden: "",
      onclick: function () { openRewind(entry.uuid); } });
    back.innerHTML = ICON_REWIND;
    var time = msgTime(entry.at);
    var turn = el("div", { class: "msg-turn" }, [
      el("div", { class: "msg-user", "data-uuid": entry.uuid || "" }, [userText(entry.text)]),
      (entry.images || []).length ? imageStrip(entry.images) : null,
      el("div", { class: "msg-meta" }, [copy, time, back]),
    ]);
    markRewind(turn);
    return turn;
  }
  // ---- Rewind: Claude Code's /rewind, on each of your messages. Claude
  // Code keeps a checkpoint at each message you send; Rewind goes back to
  // before one: the code and the chat, the chat, or the code. Codex keeps
  // no checkpoints of the code, so it's Claude Code's only.
  function loadPoints() {
    var asked = chat.space;
    if (!asked) return;
    api("GET", "chat/points?workspace=" + encodeURIComponent(asked)).then(function (view) {
      if (asked !== chat.space) return;
      rewind.points = view.points || [];
      renderPoints();
    }).catch(function () {
      rewind.points = [];
      renderPoints();
    });
  }
  function changeWords(list) {
    return list.map(function (change) {
      return change.path + (change.added ? " +" + change.added : "") + (change.removed ? " −" + change.removed : "");
    }).join(", ");
  }
  function rewindPoint(uuid) {
    return uuid ? rewind.points.filter(function (point) { return point.uuid === uuid; })[0] : null;
  }
  function rewindUsable() {
    var agent = chat.agent || {};
    return !!agent.running && agent.agent === "claude";
  }
  // markRewind shows a message's Rewind when Claude Code has a point for it.
  function markRewind(turn) {
    var bubble = turn.querySelector(".msg-user"), button = turn.querySelector(".msg-rewind");
    var point = rewindUsable() && rewindPoint(bubble.dataset.uuid);
    button.hidden = !point;
    if (point) {
      var changes = point.changes || [];
      button.title = "Rewind to here" + (changes.length ? ". Claude changed after it: " + changeWords(changes) : "");
    }
  }
  function renderPoints() {
    document.querySelectorAll("#chat-entries .msg-turn:not(.reply)").forEach(markRewind);
    if (rewind.open && $("rewind-dialog").open) openRewind(rewind.open.uuid);
  }
  // openRewind asks how far back to go, for one message.
  function openRewind(uuid) {
    var point = rewindPoint(uuid);
    var dialog = $("rewind-dialog");
    if (!point || !rewindUsable()) {
      if (dialog.open) dialog.close();
      if (!point && rewind.open) toast("Claude Code has no point for that message now.");
      rewind.open = null;
      return;
    }
    rewind.open = point;
    var agent = chat.agent || {};
    var busy = agent.status === "working" || agent.status === "blocked";
    var bubble = document.querySelector('#chat-entries .msg-user[data-uuid="' + point.uuid + '"]');
    var text = (bubble && bubble.textContent) || point.text || "";
    $("rewind-quote").textContent = text.length > 280 ? text.slice(0, 280) + "…" : text;
    var restore = point.restore || [], changes = point.changes || [];
    $("rewind-changes").textContent = (point.at ? "Sent " + ago(point.at) + ". " : "") +
      (restore.length ? "Files that go back: " + changeWords(restore) + "." : changes.length ? "Claude changed after it: " + changeWords(changes) + "." : "Claude changed no files after this message.");
    dialog.querySelectorAll("[data-rewind]").forEach(function (option) {
      option.disabled = busy || (option.dataset.rewind !== "conversation" && !point.code);
    });
    var note = $("rewind-note");
    note.textContent = busy ? "Claude is working. Stop it first, then rewind." : !point.code ? "Claude Code has no checkpoint of the code here. Only the chat can go back." : "";
    note.hidden = !note.textContent;
    if (!dialog.open) dialog.showModal();
  }
  $("rewind-dialog").querySelectorAll("[data-rewind]").forEach(function (option) {
    option.addEventListener("click", function () {
      var point = rewind.open;
      $("rewind-dialog").close();
      if (point) rewindTo(point, option.dataset.rewind);
    });
  });
  $("rewind-cancel").addEventListener("click", function () { $("rewind-dialog").close(); });
  $("rewind-dialog").addEventListener("close", function () { rewind.open = null; });
  function rewindTo(point, mode) {
    toast("Rewinding…", true);
    api("POST", "chat/rewind", { workspace: chat.space, uuid: point.uuid, mode: mode }).then(function (result) {
      var input = $("chat-input");
      if (result.text && !input.value.trim()) {
        input.value = result.text;
        input.dispatchEvent(new Event("input"));
        input.focus();
      }
      toast(mode === "code" ? "The code is back to before that message." : "Rewound. The message is in the box. You can change it and send it again.");
      chat.offset = 0;
      chat.session = "";
      chat.turns = "";
      pollChat();
      loadPoints();
    }).catch(function (error) {
      toast(error.message);
      loadPoints();
    });
  }
  function pollChat() {
    clearTimeout(chat.timer);
    var visible = chat.view === "chat" && !document.hidden && !chat.composing;
    // Not before the workspaces are known, nor for one that's gone: one the
    // browser remembered from before the dev box restarted isn't there.
    var known = !!state && workSpaces().some(function (space) { return space.id === chat.space; });
    if (!visible || !chat.space || !known || chat.busy) { chat.timer = setTimeout(pollChat, 1500); return; }
    chat.busy = true;
    var log = $("chat-log");
    var atBottom = log.scrollHeight - log.scrollTop - log.clientHeight < 80;
    var asked = chat.space;
    api("GET", "chat?workspace=" + encodeURIComponent(chat.space) + "&offset=" + chat.offset + "&session=" + encodeURIComponent(chat.session) + "&turns=" + encodeURIComponent(chat.turns || "")).then(function (view) {
      if (asked !== chat.space) return;
      if (view.reset) {
        clearChat();
        atBottom = true;
        if (view.hidden) $("chat-entries").appendChild(el("div", { class: "chat-note", text: view.hidden + " earlier steps do not show here. The terminal has the full session." }));
      }
      chat.session = view.session || "";
      chat.sessionAgent = view.session_agent || "claude";
      chat.offset = view.offset || 0;
      chat.turns = view.turns || "";
      (view.entries || []).forEach(chatEntry);
      if ((view.entries || []).length) { markResponses(); placeChanges(); }
      if (!view.session && !$("chat-entries").childNodes.length) $("chat-entries").appendChild(el("div", { class: "chat-note", text: view.note || "No session in this workspace yet. Start one from a task or in the terminal." }));
      chatState(view);
      renderQueue(view.queue || []);
      // The timeline again when its messages, or what Claude is doing, change.
      var agent = view.agent || {};
      var seen = [agent.running, agent.agent, agent.status, view.turns, view.session].join("/");
      if (view.reset || seen !== rewind.seen) { rewind.seen = seen; loadPoints(); if (gitShown()) refreshGit(); }
      if (atBottom) log.scrollTop = log.scrollHeight;
    }).catch(function (error) {
      if (asked === chat.space) $("chat-status").textContent = error.message;
    }).then(function () {
      chat.busy = false;
      chat.timer = setTimeout(pollChat, 1500);
    });
  }
  // ---- Pointing at the preview's parts: ⌖ in the Preview's bar. The page
  // in the frame (the helper's script, which hi's relay puts in a page in a
  // frame) outlines what's under the pointer, and a click puts a pill in
  // the chat's box where the cursor is: [@1 button "Pay"]. Sent, each
  // pill's part is described under the message, for the agent: the page,
  // where on it, its selector and HTML, the source when the framework says,
  // the files git grep finds it in, and its picture (hi_app_helper.md, "In
  // the dev page's Preview").
  var picks = { on: false, ready: false, origin: "", refs: [], hover: null };
  var pickPattern = /\[@(\d+) [^\]\n]*\]/g;
  function frameWindow() { return $("preview-frame").contentWindow; }
  function originOf(url) { try { return new URL(url, location.href).origin; } catch (e) { return ""; } }
  function tellFrame(message) {
    var origin = picks.origin || originOf(preview.url);
    if (!origin || origin === "null" || !frameWindow()) return;
    try { frameWindow().postMessage(message, origin); } catch (e) { /* gone */ }
  }
  function renderPickButton() {
    var button = $("preview-pick");
    button.setAttribute("aria-pressed", picks.on ? "true" : "false");
    button.classList.toggle("waiting", !picks.ready);
  }
  window.addEventListener("message", function (event) {
    var data = event.data;
    if (!data || typeof data.hi !== "string" || event.source !== frameWindow()) return;
    // Only the app's own addresses: the frame can be sent on elsewhere.
    var ours = previewSlots().some(function (slot) { return originOf(slot.url) === event.origin; });
    if (!ours) return;
    picks.origin = event.origin;
    if (data.hi === "ready") {
      picks.ready = true;
      // A page the frame went on to while pointing goes on pointing.
      if (picks.on) tellFrame({ hi: "pick", on: true });
    } else if (data.hi === "pick") {
      picks.on = !!data.on;
      if (!picks.on && chat.view === "chat" && !narrow) $("chat-input").focus();
    } else if (data.hi === "picked" && data.ref && typeof data.ref.selector === "string") {
      addPick(data);
    } else if (data.hi === "picture" && typeof data.image === "string" && /^data:image\/(png|jpeg);base64,/.test(data.image)) {
      for (var i = picks.refs.length - 1; i >= 0; i--) {
        if (picks.refs[i].frameId === data.id && !picks.refs[i].image) { picks.refs[i].image = data.image; saveDraft(); break; }
      }
    }
    renderPickButton();
  });
  $("preview-frame").addEventListener("load", function () { tellFrame({ hi: "hello" }); });
  $("preview-pick").addEventListener("click", function () {
    if (!picks.ready) {
      toast("The page in the Preview does not answer yet. It can still be loading, or it can be a sign-in page in front of the app." +
        (location.protocol === "http:" ? " On a plain-HTTP address, the page answers only when the helper is on for the preview (System)." : "") + " Click ↻ to load it again.");
      return;
    }
    picks.on = !picks.on;
    tellFrame({ hi: "pick", on: picks.on });
    renderPickButton();
  });
  // While pointing, the keyboard stays here (the page in the frame stops
  // the mousedown that would move it): Esc, Enter, and Alt+↑/↓ go on to it.
  // Alt+S starts and stops pointing, as it does in the page.
  document.addEventListener("keydown", function (event) {
    if (event.altKey && !event.ctrlKey && !event.metaKey && event.code === "KeyS" && previewShown() && picks.ready) {
      event.preventDefault();
      $("preview-pick").click();
      return;
    }
    if (!picks.on) return;
    var key = event.key;
    if (key === "Escape" || (event.altKey && (key === "ArrowUp" || key === "ArrowDown")) || (key === "Enter" && document.activeElement !== $("chat-input"))) {
      event.preventDefault();
      event.stopPropagation();
      tellFrame({ hi: "key", key: key, altKey: event.altKey, shiftKey: event.shiftKey });
    }
  }, true);
  // pickStop ends pointing when the Preview shows another page.
  function pickStop() {
    if (picks.on) tellFrame({ hi: "pick", on: false });
    picks.on = false;
    picks.ready = false;
    picks.origin = "";
    renderPickButton();
  }
  renderPickButton();

  // pickName is a part's name in its pill: its tag, its id, its words.
  function pickName(ref) {
    var a = ref.attributes || {};
    var name = ref.tag + (a.id && /^[A-Za-z][\w-]{0,30}$/.test(a.id) ? "#" + a.id : "");
    var words = String(ref.text || a["aria-label"] || a.placeholder || a.alt || a.title || "").replace(/[\[\]"\n]/g, " ").replace(/\s+/g, " ").trim();
    if (words) name += ' "' + (words.length > 24 ? words.slice(0, 23).trim() + "…" : words) + '"';
    return name;
  }
  function pickToken(item) { return "[@" + item.n + " " + item.name + "]"; }
  // usedPicks are the parts whose pills are in the text, in its order.
  function usedPicks(text) {
    var used = [];
    String(text).replace(pickPattern, function (token, n) {
      var item = picks.refs.filter(function (r) { return String(r.n) === n && pickToken(r) === token; })[0];
      if (item && used.indexOf(item) < 0) used.push(item);
      return token;
    });
    return used;
  }
  // pickClues are what the part's code likely has too, for hi to look for.
  function pickClues(ref) {
    var a = ref.attributes || {}, clues = [];
    function add(kind, text) { if (text) clues.push({ kind: kind, text: String(text) }); }
    add("id", a.id);
    ["data-testid", "data-test-id", "data-test", "data-cy", "data-qa"].forEach(function (key) { add("testid", a[key]); });
    ["aria-label", "placeholder", "title", "alt"].forEach(function (key) { add("label", a[key]); });
    add("name", a.name);
    if (a.href && a.href.charAt(0) === "/" && a.href.length > 1) add("href", a.href.split(/[?#]/)[0]);
    var text = String(ref.text || "").trim();
    if (text) add("text", text.length <= 60 ? text : text.split(" ").slice(0, 5).join(" "));
    // Only the classes the selector chose: they name the part, not its look.
    var named = (ref.selector || "") + " " + (ref.path || []).join(" ");
    String(a.class || "").split(/\s+/).forEach(function (name) { if (name.length >= 4 && named.indexOf("." + name) >= 0) add("class", name); });
    return clues;
  }
  function addPick(data) {
    if (!chat.space) { toast("Open the chat of a workspace first. The part you point at goes in its message."); return; }
    var input = $("chat-input");
    if (usedPicks(input.value).length >= 10) { toast("You can add at most ten parts to one message."); return; }
    var n = picks.refs.reduce(function (most, r) { return Math.max(most, r.n); }, 0) + 1;
    var slot = previewSlots().filter(function (o) { return o.id === preview.slot; })[0];
    var item = { n: n, name: pickName(data.ref), ref: data.ref, url: String(data.url || ""), title: String(data.title || ""),
      shown: slot ? slot.label : "", frameId: data.id, image: "", where: null, space: chat.space };
    picks.refs.push(item);
    // Where the cursor is, a space on each side.
    var at = input.selectionStart, end = input.selectionEnd;
    if (document.activeElement !== input && !input.value.slice(at).trim()) at = end = input.value.length;
    var before = input.value.slice(0, at), after = input.value.slice(end);
    var put = (before && !/\s$/.test(before) ? " " : "") + pickToken(item) + (after && /^\s/.test(after) ? "" : " ");
    input.value = before + put + after;
    input.selectionStart = input.selectionEnd = before.length + put.length;
    input.dispatchEvent(new Event("input"));
    if (chat.view !== "chat") toast("Added to the chat message: " + pickToken(item));
    api("POST", "pick/locate", { workspace: item.space, clues: pickClues(item.ref) }).then(function (result) {
      item.where = result.hits || [];
      saveDraft();
    }).catch(function () { item.where = []; });
  }

  // The pills: the box's words again, in a layer behind it, each pill's
  // words marked. A pill goes whole with Backspace or Delete.
  function renderMarks() {
    var input = $("chat-input"), box = $("chat-marks"), value = input.value;
    box.textContent = "";
    var used = usedPicks(value);
    if (!used.length) return;
    var last = 0;
    value.replace(pickPattern, function (token, n, at) {
      if (!used.some(function (item) { return pickToken(item) === token; })) return token;
      box.appendChild(document.createTextNode(value.slice(last, at)));
      box.appendChild(el("mark", { "data-n": n, class: picks.hover && String(picks.hover.n) === n ? "hover" : null, text: token }));
      last = at + token.length;
      return token;
    });
    box.appendChild(document.createTextNode(value.slice(last) + "\n"));
    box.style.paddingRight = 4 + input.offsetWidth - input.clientWidth + "px";
    box.scrollTop = input.scrollTop;
  }
  $("chat-input").addEventListener("scroll", function () { $("chat-marks").scrollTop = $("chat-input").scrollTop; });
  $("chat-input").addEventListener("keydown", function (event) {
    if ((event.key !== "Backspace" && event.key !== "Delete") || event.isComposing || event.altKey || event.ctrlKey || event.metaKey) return;
    var input = event.target, at = input.selectionStart;
    if (at !== input.selectionEnd) return;
    var hit = null;
    input.value.replace(pickPattern, function (token, n, start) {
      var end = start + token.length;
      if (event.key === "Backspace" ? at > start && at <= end : at >= start && at < end) hit = [start, end];
      return token;
    });
    if (!hit) return;
    event.preventDefault();
    input.setSelectionRange(hit[0], hit[1]);
    // As a delete of the browser's own, so Undo brings it back.
    if (!document.execCommand || !document.execCommand("delete")) {
      input.value = input.value.slice(0, hit[0]) + input.value.slice(hit[1]);
      input.selectionStart = input.selectionEnd = hit[0];
      input.dispatchEvent(new Event("input"));
    }
  });
  // Hovering a pill outlines its part in the Preview, and says what it is.
  function pickUnder(event) {
    var found = null;
    [].forEach.call($("chat-marks").querySelectorAll("mark"), function (mark) {
      [].forEach.call(mark.getClientRects(), function (rect) {
        if (event.clientX >= rect.left && event.clientX <= rect.right && event.clientY >= rect.top && event.clientY <= rect.bottom) found = mark;
      });
    });
    return found ? picks.refs.filter(function (r) { return String(r.n) === found.dataset.n; })[0] || null : null;
  }
  function pickHover(item) {
    if (item === picks.hover) return;
    picks.hover = item;
    [].forEach.call($("chat-marks").querySelectorAll("mark"), function (mark) { mark.classList.toggle("hover", !!item && mark.dataset.n === String(item.n)); });
    $("chat-input").title = item ? pickSummary(item) : "";
    tellFrame({ hi: "peek", selector: item ? item.ref.selector : null });
  }
  $("chat-input").addEventListener("mousemove", function (event) { pickHover(pickUnder(event)); });
  $("chat-input").addEventListener("mouseleave", function () { pickHover(null); });
  function pickPage(item) {
    try {
      var url = new URL(item.url);
      ["hi_user", "key", "token"].forEach(function (name) { url.searchParams.delete(name); });
      return url.pathname + url.search;
    } catch (e) { return ""; }
  }
  function pickSummary(item) {
    var where = (item.where || []).slice(0, 2).map(function (hit) { return hit.path + ":" + hit.line; });
    return [item.name, pickPage(item) && "on " + pickPage(item), item.ref.selector, where.length ? "likely in " + where.join(", ") : ""].filter(Boolean).join("\n");
  }
  // pickNotes describe the parts under the message, for the agent; their
  // pictures go with it as files, named here.
  var pickHeading = "What I pointed at, with ⌖ in the dev page's Preview";
  function pickNotes(used) {
    var lines = ["", "", pickHeading + " (the [@n …] above):"];
    used.forEach(function (item) {
      var ref = item.ref, box = ref.box || {}, landmarks = ref.landmarks || {};
      lines.push("", pickToken(item));
      var page = pickPage(item);
      if (page) lines.push("- page: " + page + (item.title ? ' ("' + item.title + '")' : "") + (item.shown ? ", on " + item.shown : ""));
      var around = (ref.path || []).join(" › ");
      var under = [landmarks.heading && 'under the heading "' + landmarks.heading + '"', landmarks.label && 'labelled "' + landmarks.label + '"', landmarks.section && 'in "' + landmarks.section + '"'].filter(Boolean).join(", ");
      if (around || under) lines.push("- on the page: " + [around, under].filter(Boolean).join("; "));
      lines.push("- selector: `" + ref.selector + "`");
      if (ref.source && ref.source.file) lines.push("- made in: " + ref.source.file + (ref.source.line ? ":" + ref.source.line : "") + (ref.source.component ? " (" + ref.source.component + ")" : "") + ", as the app's framework says");
      else if (ref.source && ref.source.component) lines.push("- component: " + ref.source.component);
      if (item.where && item.where.length) {
        lines.push("- likely in this workspace (git grep for its " + "words, id, and labels): " + item.where.map(function (hit) {
          return hit.path + ":" + hit.line + (hit.clues && hit.clues.length ? " (" + hit.clues.join(", ") + ")" : "");
        }).join(", "));
      }
      if (ref.text) lines.push('- text: "' + ref.text + '"');
      if (ref.html) lines.push("- html: `" + String(ref.html).replace(/\s+/g, " ").slice(0, 600) + (ref.html.length > 600 ? " …" : "") + "`");
      if (box.width) lines.push("- size: " + box.width + "×" + box.height + " at " + box.x + "," + box.y + (box.viewport ? " in a " + box.viewport.join("×") + " window" : ""));
      if (item.image) lines.push("- picture: " + item.file);
    });
    return lines.join("\n");
  }
  // pickPictures name each part's picture and read it as a file to send.
  function pickPictures(used) {
    return used.filter(function (item) { return item.image; }).map(function (item) {
      var jpeg = /^data:image\/jpeg/.test(item.image);
      item.file = "pointed-" + item.n + "-" + Math.random().toString(16).slice(2, 8) + (jpeg ? ".jpg" : ".png");
      return { name: item.file, data: item.image.split(",")[1] || "" };
    });
  }
  // userText draws a message of yours: its pills as pills, and the notes
  // about them folded under it.
  function userText(text) {
    text = String(text || "");
    var at = text.indexOf("\n\n" + pickHeading);
    if (at < 0 && !pickPattern.test(text)) return linked(text);
    pickPattern.lastIndex = 0;
    var box = el("div", { class: "msg-user-text" });
    var said = at >= 0 ? text.slice(0, at) : text, notes = at >= 0 ? text.slice(at + 2) : "";
    var last = 0;
    said.replace(pickPattern, function (token, n, start) {
      if (start > last) box.appendChild(linked(said.slice(last, start)));
      box.appendChild(el("span", { class: "pick-pill", text: token }));
      last = start + token.length;
      return token;
    });
    if (last < said.length) box.appendChild(linked(said.slice(last)));
    [].forEach.call(box.querySelectorAll(".text"), function (part) { part.style.display = "inline"; });
    if (notes) {
      var count = (notes.match(/\n\[@\d+ /g) || []).length;
      box.appendChild(el("details", { class: "pick-notes" }, [el("summary", { text: "Details about the " + (count === 1 ? "part" : count + " parts") + " you pointed at" }), el("pre", { text: notes })]));
    }
    return box;
  }
  var chatFiles = attacher($("chat-files"), $("chat-input"), $("chat-attached"), true, function () { saveDraft(); chatReady(); });
  // The message being written, its files and pastes too, is kept in this
  // browser (IndexedDB, which holds files) until it's sent, so a reload or
  // a restart of the browser doesn't lose it. One a workspace: a draft
  // stays with the work it was written for, and a sent one is gone.
  var drafts = null;
  function draftStore(mode) {
    if (!drafts) drafts = new Promise(function (resolve, reject) {
      if (!window.indexedDB) { reject(new Error("no IndexedDB")); return; }
      var open = indexedDB.open("hi-dev", 1);
      open.onupgradeneeded = function () { open.result.createObjectStore("drafts"); };
      open.onsuccess = function () { resolve(open.result); };
      open.onerror = function () { reject(open.error); };
    });
    return drafts.then(function (db) { return db.transaction("drafts", mode).objectStore("drafts"); });
  }
  function draftKey(space) { return "chat:" + space; }
  function writeDraft(space) {
    if (!chatFiles || !space) return;
    var draft = chatFiles.snapshot();
    draft.text = $("chat-input").value;
    draft.picks = usedPicks(draft.text);
    var empty = !draft.text.trim() && !draft.files.length && !draft.pastes.length;
    draftStore("readwrite").then(function (store) {
      if (empty) store.delete(draftKey(space));
      else store.put(draft, draftKey(space));
    }).catch(function () { /* a private window: no draft */ });
  }
  function saveDraft() {
    if (!chatFiles) return; // not made yet
    clearTimeout(chat.draftTimer);
    var space = chat.space;
    chat.draftSpace = space;
    chat.draftTimer = setTimeout(function () { chat.draftTimer = 0; writeDraft(space); }, 300);
  }
  // A draft not written yet is written for its own workspace, before the
  // box shows another's.
  function flushDraft() {
    if (!chat.draftTimer) return;
    clearTimeout(chat.draftTimer);
    chat.draftTimer = 0;
    writeDraft(chat.draftSpace);
  }
  // loadDraft empties the box and puts the workspace's draft in it.
  function loadDraft(space) {
    $("chat-input").value = "";
    picks.refs = [];
    if (chatFiles) chatFiles.clear();
    sizeChatInput();
    chatReady();
    if (!space) return;
    draftStore("readonly").then(function (store) {
      var got = store.get(draftKey(space));
      got.onsuccess = function () {
        var draft = got.result;
        if (!draft || chat.space !== space || $("chat-input").value) return;
        $("chat-input").value = draft.text || "";
        picks.refs = draft.picks || [];
        chatFiles.restore(draft);
        sizeChatInput();
        chatReady();
      };
    }).catch(function () { /* a private window: no draft */ });
  }
  // Before drafts were a workspace's: one for the page, which a sent
  // message never cleared. It goes.
  draftStore("readwrite").then(function (store) { store.delete("chat"); }).catch(function () { /* a private window */ });
  // The workspace the page opens on, from the last visit: its draft.
  if (chat.space) loadDraft(chat.space);
  // ---- Queued messages, as in Orca: sent while the agent works, a
  // message waits above the box, and hi sends the next one when the
  // agent's turn ends. Steer sends one now, into the turn that runs; ✕
  // takes one out. The queue is the server's (app_devqueue.go), so it goes
  // on with the page closed.
  var ICON_QUEUED = '<svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M16 12H3"/><path d="M16 6H3"/><path d="M10 18H3"/><path d="M21 6v10a2 2 0 0 1-2 2h-5"/><path d="m16 16-2 2 2 2"/></svg>';
  var ICON_STEER = '<svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M5 4v7a4 4 0 0 0 4 4h11"/><path d="m16 11 4 4-4 4"/></svg>';
  function renderQueue(queue) {
    chat.queue = queue;
    var box = $("chat-queue"), list = $("chat-queue-list");
    box.hidden = !queue.length;
    var send = $("chat-send"), queues = chatQueues();
    send.title = queues ? "Queue it (Enter). hi sends it when the agent's turn ends. Ctrl+Enter steers: it goes now." : "Send (Enter)";
    send.setAttribute("aria-label", queues ? "Queue" : "Send");
    $("chat-queue-count").textContent = queue.length > 1 ? String(queue.length) : "";
    $("chat-queue-head").setAttribute("aria-expanded", String(chat.queueOpen));
    list.hidden = !chat.queueOpen;
    list.textContent = "";
    queue.forEach(function (item) {
      var icon = el("span", { class: "q-icon" });
      icon.innerHTML = ICON_QUEUED;
      var steer = el("button", { type: "button", class: "q-steer", title: "Send it now. The agent reads it when its current step ends.", onclick: function () { queueAct("steer", item, steer); } });
      steer.innerHTML = ICON_STEER;
      steer.appendChild(document.createTextNode("Steer"));
      var remove = el("button", { type: "button", class: "msg-act", title: "Take it out of the queue", "aria-label": "Take it out of the queue", text: "✕", onclick: function () { queueAct("remove", item, remove); } });
      if (!item.id) steer.disabled = remove.disabled = true; // on its way to the server
      list.appendChild(el("li", {}, [icon, el("span", { class: "q-text", title: item.shown + (item.by ? "\n— " + item.by : ""), text: item.shown }), steer, remove]));
    });
  }
  function queueAct(what, item, button) {
    button.disabled = true;
    api("POST", "chat/queue/" + what, { workspace: chat.space, id: item.id }).then(function () {
      renderQueue(chat.queue.filter(function (other) { return other.id !== item.id; }));
      if (what === "steer") {
        var node = el("div", { class: "msg-user pending" }, [userText(item.shown)]);
        chat.pending.push({ node: node, since: Date.now(), text: item.shown });
        chatPending(chat.agent || {});
        $("chat-entries").insertBefore(node, $("chat-activity"));
        $("chat-log").scrollTop = $("chat-log").scrollHeight;
      }
      pollChat();
    }).catch(function (error) { button.disabled = false; toast(error.message); });
  }
  $("chat-queue-head").addEventListener("click", function () {
    chat.queueOpen = !chat.queueOpen;
    renderQueue(chat.queue);
  });
  // chatQueues says a message goes in the queue: the agent works, or
  // others wait already.
  function chatQueues() {
    var agent = chat.agent || {};
    return !!agent.running && (agent.status === "working" || agent.status === "blocked" || chat.queue.length > 0);
  }
  function chatSend(steer) {
    var input = $("chat-input"), space = chat.space;
    var shown = input.value.trim();
    var text = chatFiles.expand(input.value).trim();
    if ((!text && !chatFiles.count()) || $("chat-send").disabled) return;
    $("chat-send").disabled = true;
    // The parts pointed at in the Preview: described under the message,
    // their pictures with it.
    var used = usedPicks(input.value), pictures = pickPictures(used);
    if (used.length) text += pickNotes(used);
    var queued = !steer && chatQueues();
    chatFiles.read().then(function (files) {
      return api("POST", queued ? "chat/queue" : "chat/send", { workspace: space, text: text, shown: shown || "Attached files", files: files.concat(pictures) });
    }).then(function () {
      if (queued) {
        input.value = "";
        picks.refs = [];
        chatFiles.clear();
        sizeChatInput();
        chatReady();
        clearTimeout(chat.draftTimer);
        chat.draftTimer = 0;
        writeDraft(space);
        renderQueue(chat.queue.concat([{ id: "", shown: shown || "Attached files" }]));
        pollChat();
        return;
      }
      input.value = "";
      picks.refs = [];
      chatFiles.clear();
      sizeChatInput();
      chatReady();
      clearTimeout(chat.draftTimer);
      chat.draftTimer = 0;
      writeDraft(space); // empty now: the draft goes
      var node = el("div", { class: "msg-user pending" }, [shown ? userText(shown) : document.createTextNode("Attached files")]);
      chat.pending.push({ node: node, since: Date.now(), text: shown });
      chatPending(chat.agent || {});
      $("chat-entries").insertBefore(node, $("chat-activity"));
      $("chat-log").scrollTop = $("chat-log").scrollHeight;
      pollChat();
    }).catch(function (error) { toast(error.message); }).then(function () { $("chat-send").disabled = false; });
  }
  // Files dropped on the chat's box are attached to the message.
  $("chat-form").addEventListener("dragover", function (event) {
    if (event.dataTransfer && [].indexOf.call(event.dataTransfer.types, "Files") >= 0) { event.preventDefault(); $("chat-form").classList.add("drop"); }
  });
  $("chat-form").addEventListener("dragleave", function () { $("chat-form").classList.remove("drop"); });
  $("chat-form").addEventListener("drop", function (event) {
    $("chat-form").classList.remove("drop");
    if (!event.dataTransfer || !event.dataTransfer.files.length) return;
    event.preventDefault();
    chatFiles.add(event.dataTransfer.files);
  });
  function sizeChatInput() {
    var input = $("chat-input");
    input.style.height = "auto";
    input.style.height = Math.min(240, input.scrollHeight) + "px";
    renderMarks();
  }
  $("chat-input").addEventListener("input", sizeChatInput);
  // Send lights up once there's something to send.
  function chatReady() {
    $("chat-send").classList.toggle("ready", !!($("chat-input").value.trim() || (chatFiles && chatFiles.count())));
  }
  $("chat-input").addEventListener("input", chatReady);
  $("chat-input").addEventListener("input", saveDraft);
  $("chat-input").addEventListener("keydown", function (event) {
    // Enter sends and Shift+Enter is a new line; on a touch keyboard, Enter is a new line.
    // Ctrl+Enter (⌘Enter) steers: now, into the turn that runs, not queued.
    if (event.key === "Enter" && (event.ctrlKey || event.metaKey) && !event.isComposing) { event.preventDefault(); chatSend(true); return; }
    if (event.key === "Enter" && !event.shiftKey && !event.isComposing && !narrow) { event.preventDefault(); chatSend(); }
  });
  $("chat-form").addEventListener("submit", function (event) { event.preventDefault(); chatSend(); });
  // A question in the terminal, as a card at the end of the chat: tap an
  // option, or say it in your own words, and hi presses the keys.
  // askPreview is an option's preview as Claude Code draws it beside the
  // options: monospace, in a box; a colour's hex code with its swatch.
  function askPreview(text) {
    var box = el("span", { class: "ask-preview" });
    text.split(/(#[0-9a-fA-F]{6}\b)/).forEach(function (part) {
      if (/^#[0-9a-fA-F]{6}$/.test(part)) {
        // In its colour, its text black or white to read on it: no wider,
        // so the columns stay as written.
        var n = parseInt(part.slice(1), 16), light = (0.299 * (n >> 16) + 0.587 * (n >> 8 & 255) + 0.114 * (n & 255)) > 150;
        var swatch = el("span", { class: "ask-swatch", text: part });
        swatch.style.background = part;
        swatch.style.color = light ? "#000" : "#fff";
        box.appendChild(swatch);
      } else if (part) box.appendChild(document.createTextNode(part));
    });
    return box;
  }
  function chatAsk(ask, screen, asker) {
    var box = $("chat-ask");
    var sig = ask ? JSON.stringify(ask) : "";
    if (sig === chat.askSig) {
      var pre = box.querySelector(".ask-terminal pre");
      if (pre) pre.textContent = screen;
      return;
    }
    chat.askSig = sig;
    box.textContent = "";
    box.hidden = !ask;
    if (!ask) return;
    var card = el("div", { class: "ask-card", role: "group" });
    function answer(index, typed) {
      card.classList.add("busy");
      api("POST", "chat/answer", { workspace: chat.space, text: ask.text, option: index, label: ask.options[index].label, typed: typed || "" })
        .then(function () { setTimeout(pollChat, 500); })
        .catch(function (error) { toast(error.message); })
        .then(function () { setTimeout(function () { card.classList.remove("busy"); }, 1500); });
    }
    var who = el("div", { class: "ask-who" }, [el("span", { text: agentName(asker || "claude") + " is asking" + (ask.progress ? " · " + ask.progress : "") })]);
    (ask.tabs || []).forEach(function (tab) { who.appendChild(el("span", { class: "ask-tab" + (tab.done ? " done" : "") + (tab.current ? " current" : ""), "aria-current": tab.current ? "step" : "false", text: (tab.done ? "✓ " : "") + tab.label })); });
    card.appendChild(who);
    // The question as the agent wrote it, when its transcript has it; else
    // as the screen shows it.
    (ask.question ? [ask.question] : ask.text || []).forEach(function (text, i) {
      card.appendChild(el("p", { class: i === 0 ? "ask-q" : "ask-more" + (/^\$ /.test(text) ? " code" : ""), text: text }));
    });
    var options = el("div", { class: "ask-options" });
    ask.options.forEach(function (option, index) {
      var key = option.box ? (option.checked ? "✓" : "") : (option.key || "→");
      var button = el("button", { type: "button", class: "ask-option", "aria-pressed": option.checked ? "true" : "false" }, [
        el("span", { class: "ask-key", text: key }),
        el("span", { class: "ask-body" }, [
          el("span", { class: "ask-label", text: option.title || option.label }),
          (option.title ? option.about : option.detail) ? el("span", { class: "ask-detail", text: option.title ? option.about : option.detail }) : null,
          option.preview ? askPreview(option.preview) : null,
        ]),
      ]);
      options.appendChild(button);
      if (!option.typed) { button.addEventListener("click", function () { answer(index); }); return; }
      // In your own words: a box under it, sent with the option.
      var input = el("textarea", { rows: "1", placeholder: "Your answer…" });
      var form = el("form", { class: "ask-typed", hidden: "" }, [input, el("button", { class: "primary", text: "Send" })]);
      function sendTyped() { if (input.value.trim()) answer(index, input.value.trim()); }
      form.addEventListener("submit", function (event) { event.preventDefault(); sendTyped(); });
      input.addEventListener("keydown", function (event) {
        if (event.key === "Enter" && !event.shiftKey && !event.isComposing && !narrow) { event.preventDefault(); sendTyped(); }
      });
      button.addEventListener("click", function () { form.hidden = !form.hidden; if (!form.hidden) input.focus(); });
      options.appendChild(form);
    });
    card.appendChild(options);
    var nav = el("div", { class: "ask-nav" });
    if ((ask.tabs && ask.tabs.length) || ask.progress) {
      nav.appendChild(el("button", { type: "button", text: "← Previous", onclick: function () { chatKeys(["left"]); } }));
      nav.appendChild(el("button", { type: "button", text: "Next →", onclick: function () { chatKeys(["right"]); } }));
    }
    nav.appendChild(el("span", { class: "grow" }));
    nav.appendChild(el("button", { type: "button", class: "link", text: "Dismiss (Esc)", onclick: function () { chatKeys(["esc"]); } }));
    card.appendChild(nav);
    var keys = el("div", { class: "chat-keys" });
    [["up", "↑"], ["down", "↓"], ["left", "←"], ["right", "→"], ["tab", "Tab"], ["space", "Space"], ["enter", "Enter"], ["esc", "Esc"]].forEach(function (k) {
      keys.appendChild(el("button", { type: "button", text: k[1], onclick: function () { chatKeys([k[0]]); } }));
    });
    card.appendChild(el("details", { class: "fold ask-terminal" }, [el("summary", { text: "The terminal" }), el("pre", { text: screen }), keys]));
    box.appendChild(card);
    $("chat-log").scrollTop = $("chat-log").scrollHeight;
  }
  function chatKeys(keys) {
    api("POST", "chat/keys", { workspace: chat.space, keys: keys }).then(function () { setTimeout(pollChat, 400); }).catch(function (error) { toast(error.message); });
  }
  $("chat-stop").addEventListener("click", function () { chatKeys(["esc"]); });
  document.querySelectorAll("[data-chat-key]").forEach(function (button) {
    button.addEventListener("click", function () { chatKeys([button.dataset.chatKey]); });
  });
  $("chat-resume").addEventListener("click", function () {
    resumeSession(chat.sessionAgent || "claude", chat.id);
  });
  // ---- A workspace's sessions, both agents', newest first, each with
  // Resume (app_devcodex.go).
  function resumeCommand(agent) { return { codex: "codex resume", agy: "agy --conversation" }[agent] || "claude --resume"; }
  function agentName(agent) { return { claude: "Claude", codex: "Codex", agy: "Antigravity" }[agent] || agent || "The agent"; }
  // resumeSession carries a session on in its workspace; running, the
  // agent there now, which stops first (after a yes).
  function resumeSession(agent, id, space, running) {
    space = space || chat.space;
    var command = resumeCommand(agent);
    var question = running
      ? "Stop " + agentName(running) + " in this workspace and resume this session? hi presses Ctrl+C in its terminal, then types " + command + ". Then " + agentName(agent) + " continues from where it stopped."
      : "Resume this session? hi types " + command + " in the terminal of the workspace. Then " + agentName(agent) + " continues from where it stopped.";
    ask(question).then(function (answer) {
      if (answer === null) return;
      if (space !== chat.space) chooseChatSpace(space);
      toast(running ? "Stopping " + agentName(running) + "…" : agentName(agent) + " is starting in the workspace…");
      api("POST", "chat/resume", { workspace: space, session: id, agent: agent, stop: !!running }).then(function () {
        if (running) toast(agentName(agent) + " is starting in the workspace…");
        setTimeout(pollChat, 1500);
      }).catch(function (error) { toast(error.message); });
    });
  }
  // ---- The bar's one selector: each workspace, its sessions under it,
  // newest first, and + New. A workspace opens its chat; a session not
  // its agent's now resumes there, stopping that agent first (after a
  // yes); the agent's own opens the chat.
  var spaceSessions = {}; // a workspace's id: its sessions, as last fetched
  var spaceAll = {};      // a workspace's id: true when Show all was pressed
  var SPACE_SHOWN = 3;
  function spaceLabel() {
    var space = workSpaces().filter(function (s) { return s.id === chat.space; })[0];
    var text = chat.composing || !space ? "Start something new" : workName(space) + (space.agent ? " · " + agentName(space.agent) : "");
    $("space-label").textContent = text;
    $("space-btn").title = text + ". Workspaces and sessions. Use + New to start new work.";
  }
  function closeSpaceMenu() {
    $("space-menu").hidden = true;
    $("space-btn").setAttribute("aria-expanded", "false");
  }
  function drawSpaceMenu() {
    var menu = $("space-menu");
    var focused = document.activeElement && menu.contains(document.activeElement) ? document.activeElement.dataset.key : "";
    menu.textContent = "";
    var spaces = workSpaces();
    if (spaces.length) menu.appendChild(el("div", { class: "theme-head", text: "Workspaces" }));
    spaces.forEach(function (space) {
      // A workspace's row is its chat: the session its agent runs now, by
      // its title. Under it, its earlier sessions, each to resume.
      var current = space.id === chat.space && !chat.composing;
      var sessions = spaceSessions[space.id];
      var live = (sessions || []).filter(function (s) { return s.live || (current && space.agent && s.id === chat.id); })[0];
      var subtitle = live ? (live.title || "(no message yet)") : space.title || "";
      var dot = el("span", { class: "dot " + (space.agent ? space.status || "unknown" : "none") });
      menu.appendChild(el("button", { type: "button", role: "menuitemradio", "aria-checked": current ? "true" : "false", class: "theme-item space-item", "data-key": "w:" + space.id,
        title: "Open its chat" + (space.path ? " (" + space.path + ")" : ""), onclick: function () { closeSpaceMenu(); chooseChatSpace(space.id); } }, [
        el("span", { class: "space-line" }, [
          dot,
          el("span", { class: "theme-name space-name", text: workName(space), title: space.branch || space.label }),
          el("span", { class: "space-agent muted", text: space.agent ? agentName(space.agent) : "no agent" }),
          el("span", { class: "theme-check", text: current ? "✓" : "" }),
        ]),
        subtitle ? el("span", { class: "space-sub muted", text: subtitle }) : null,
      ]));
      if (!sessions) return;
      var earlier = sessions.filter(function (s) { return s !== live; });
      if (!earlier.length) return;
      var shown = spaceAll[space.id] ? earlier : earlier.slice(0, SPACE_SHOWN);
      var running = space.agent || "";
      // Under a label of their own, so a session isn't taken for another
      // workspace; each by when it began, as two can have one title.
      menu.appendChild(el("div", { class: "space-earlier muted", text: "Earlier sessions here" }));
      shown.forEach(function (session) {
        var when = ["Resume", agentName(session.agent)];
        if (session.started) when.push("started " + clockTime(session.started));
        when.push((session.last ? "last active " : "") + ago(session.last || session.at));
        menu.appendChild(el("button", { type: "button", role: "menuitem", class: "theme-item space-session", "data-key": "s:" + space.id + ":" + session.id,
          title: "Resume this session here" + (running ? ", stopping " + agentName(running) + " first" : ""),
          onclick: function () { closeSpaceMenu(); resumeSession(session.agent, session.id, space.id, running); } }, [
          el("span", { class: "space-session-title", text: session.title || "(no message yet)" }),
          el("span", { class: "muted space-session-when", text: when.join(" · ") }),
        ]));
      });
      if (earlier.length > shown.length) {
        menu.appendChild(el("button", { type: "button", role: "menuitem", class: "theme-item space-session space-more", "data-key": "m:" + space.id,
          text: "Show all " + earlier.length + " earlier", onclick: function () { spaceAll[space.id] = true; drawSpaceMenu(); } }));
      }
    });
    menu.appendChild(el("div", { class: "space-sep" }));
    menu.appendChild(el("button", { type: "button", role: "menuitem", class: "theme-item space-new", id: "chat-new", "data-key": "new",
      title: "Start new work without a task", onclick: function () { closeSpaceMenu(); startNew(); } }, [
      el("span", { class: "space-plus", text: "+" }), el("span", { class: "theme-name", text: "New" }),
    ]));
    if (focused) { var again = menu.querySelector('[data-key="' + focused + '"]'); if (again) again.focus(); }
  }
  function fetchSpaceSessions() {
    workSpaces().forEach(function (space) {
      api("GET", "chat/sessions?workspace=" + encodeURIComponent(space.id)).then(function (data) {
        spaceSessions[space.id] = data.sessions || [];
      }).catch(function () { spaceSessions[space.id] = []; }).then(function () {
        if (!$("space-menu").hidden) drawSpaceMenu();
      });
    });
  }
  $("space-btn").addEventListener("click", function (event) {
    event.stopPropagation();
    var menu = $("space-menu");
    if (!menu.hidden) { closeSpaceMenu(); return; }
    drawSpaceMenu();
    menu.hidden = false;
    $("space-btn").setAttribute("aria-expanded", "true");
    var first = menu.querySelector('[aria-checked="true"]') || menu.querySelector(".theme-item");
    if (first) first.focus();
    fetchSpaceSessions();
  });
  $("space-menu").addEventListener("click", function (event) { event.stopPropagation(); });
  $("space-menu").addEventListener("keydown", function (event) {
    var items = [].slice.call($("space-menu").querySelectorAll(".theme-item"));
    var at = items.indexOf(document.activeElement);
    if (event.key === "ArrowDown" || event.key === "ArrowUp") {
      event.preventDefault();
      if (items.length) items[(at + (event.key === "ArrowDown" ? 1 : items.length - 1)) % items.length].focus();
    } else if (event.key === "Escape") { closeSpaceMenu(); $("space-btn").focus(); }
  });
  document.addEventListener("click", closeSpaceMenu);
  var startView = chat.view;
  setCodeView(startView === "file" ? "chat" : startView);

  // ---- Files, in the panel beside the chat: the chat's workspace's tree
  // (or dev's clone, or another worktree), its folders opening in place.
  // A file's preview takes the panel, with a way back.
  var files = { root: "repo", dir: "", file: "", roots: [], loaded: false, open: {}, lists: {}, sig: "", at: 0, kept: {} };
  try {
    var kept = JSON.parse(localStorage.getItem("hi-dev-files-2") || "{}");
    files.kept = kept.open || {};
  } catch (e) { /* private window */ }
  // keepFiles remembers each tree's open folders.
  function keepFiles() {
    files.kept[files.root] = Object.keys(files.open);
    try { localStorage.setItem("hi-dev-files-2", JSON.stringify({ open: files.kept })); } catch (e) { /* private window */ }
  }
  function fileQuery(path, extra) {
    return "root=" + encodeURIComponent(files.root) + "&path=" + encodeURIComponent(path) + (extra || "");
  }
  function joinPath(dir, name) { return dir ? dir + "/" + name : name; }
  // A folder's or a file's icon: drawn here, since not every computer has
  // an emoji font.
  var icons = {
    dir: "M1.5 3.5h4l1.5 1.5h7.5v8.5h-13z",
    file: "M3.5 1.5h6l3 3v10h-9z M9.5 1.5v3h3",
    up: "M6 3.5 2.5 7 6 10.5 M2.5 7h7a4 4 0 0 1 4 4v1.5",
    chevron: "M6 3.5 10.5 8 6 12.5",
  };
  function icon(kind) {
    var svg = document.createElementNS("http://www.w3.org/2000/svg", "svg");
    svg.setAttribute("viewBox", "0 0 16 16");
    svg.setAttribute("aria-hidden", "true");
    var shape = document.createElementNS("http://www.w3.org/2000/svg", "path");
    shape.setAttribute("d", icons[kind]);
    shape.setAttribute("fill", "none");
    shape.setAttribute("stroke", "currentColor");
    shape.setAttribute("stroke-width", "1.3");
    shape.setAttribute("stroke-linejoin", "round");
    svg.appendChild(shape);
    return el("span", { class: "file-icon " + kind }, [svg]);
  }
  function size(n) {
    if (n == null) return "";
    var units = ["B", "KB", "MB", "GB"], i = 0;
    while (n >= 1024 && i < units.length - 1) { n /= 1024; i++; }
    return (i ? n.toFixed(n < 10 ? 1 : 0) : n) + " " + units[i];
  }
  // The chat's workspace's tree: its worktree's, by its branch, or dev's
  // clone for a workspace with none.
  function chatSpace() {
    return ((state && state.workspaces) || []).filter(function (s) { return s.id === chat.space; })[0];
  }
  function spaceRoot() {
    var space = chatSpace();
    return space && space.path && space.branch ? space.branch : "repo";
  }
  function setRoot(root) {
    if (root === files.root) return;
    files.root = root;
    files.open = {};
    (files.kept[root] || []).forEach(function (dir) { files.open[dir] = true; });
    files.lists = {};
    files.sig = "";
    files.file = "";
    files.dir = "";
    $("files-list").textContent = "";
    if (files.roots.some(function (r) { return r.id === root; })) $("files-root").value = root;
  }
  function syncFilesRoot() {
    if (!files.loaded) { files.root = ""; return; }
    var want = spaceRoot();
    if (want === files.root) return;
    // A worktree hi hasn't listed yet: the roots again, then its tree.
    if (!files.roots.some(function (r) { return r.id === want; })) {
      loadRoots().then(function () { setRoot(files.roots.some(function (r) { return r.id === want; }) ? want : "repo"); if (filesShown()) loadTree(); });
      return;
    }
    setRoot(want);
  }
  function filesShown() { return viewShown("files"); }
  function openFiles() {
    if (!files.loaded) {
      // Not before the workspaces are known: the tree is the chat's workspace's.
      if (!state) { setTimeout(function () { if (filesShown()) openFiles(); }, 300); return; }
      files.loaded = true;
      loadRoots().then(function () {
        var want = spaceRoot();
        files.root = "";
        setRoot(files.roots.some(function (r) { return r.id === want; }) ? want : "repo");
        loadTree();
      });
      return;
    }
    syncFilesRoot();
    if (Date.now() - files.at > 3000) loadTree();
  }
  function loadRoots() {
    return api("GET", "files/roots").then(function (roots) {
      files.roots = roots;
      var select = $("files-root");
      select.textContent = "";
      roots.forEach(function (root) {
        select.appendChild(el("option", { value: root.id, text: root.label }));
      });
      if (roots.some(function (root) { return root.id === files.root; })) select.value = files.root;
    }).catch(function (error) { $("files-list").textContent = error.message; });
  }
  $("files-root").addEventListener("change", function () { setRoot(this.value); loadTree(); });
  $("files-refresh").addEventListener("click", function () { loadRoots().then(loadTree); });
  $("files-collapse").addEventListener("click", function () { files.open = {}; files.dir = ""; keepFiles(); renderTree(); target(); });
  // target is the folder an upload goes into: the one last opened, or the
  // open file's.
  function target() {
    var label = (files.roots.filter(function (root) { return root.id === files.root; })[0] || { label: files.root }).label;
    $("files-drop-where").textContent = label + "/" + files.dir;
    $("files-upload-label").title = "Upload files into /" + files.dir + ". You can also drop them here.";
  }
  function listOf(dir) {
    var root = files.root;
    return api("GET", "files/list?" + fileQuery(dir)).then(function (entries) {
      if (root === files.root) files.lists[dir] = entries;
      return entries;
    });
  }
  // loadTree lists the top and every open folder again; one that's gone
  // closes.
  function loadTree() {
    var root = files.root;
    if (!root) return Promise.resolve();
    files.at = Date.now();
    var dirs = [""].concat(Object.keys(files.open));
    return Promise.all(dirs.map(function (dir) {
      return listOf(dir).catch(function (error) {
        if (dir) { delete files.open[dir]; delete files.lists[dir]; } else throw error;
      });
    })).then(function () {
      if (root !== files.root) return;
      var sig = JSON.stringify([files.lists, files.open, gitMarks()]);
      if (sig !== files.sig) { files.sig = sig; renderTree(); }
      target();
    }).catch(function (error) {
      var list = $("files-list");
      list.textContent = "";
      list.appendChild(el("p", { class: "error empty-preview", text: error.message }));
    });
  }
  // gitMarks are the source control view's letters for the tree, when it
  // has looked at this tree's workspace.
  // Each is the file's status and its lines added and removed, staged and
  // not together; full has them for any tree, not only the one shown.
  function gitMarks(full) {
    var marks = {};
    if (!git.view || git.space !== chat.space || (!full && spaceRoot() !== files.root)) return marks;
    git.view.staged.concat(git.view.changes, git.view.conflicts || []).forEach(function (file) {
      var mark = marks[file.path] || (marks[file.path] = { status: file.status, added: 0, removed: 0, binary: false });
      // A conflict says so first; otherwise what's not staged, as git shows it.
      if (file.status === "!" || mark.status !== "!") mark.status = file.status;
      mark.added += file.added || 0;
      mark.removed += file.removed || 0;
      mark.binary = mark.binary || !!file.binary;
    });
    return marks;
  }
  // A folder's colour: of what changed in it, green when all of it is new,
  // red when all of it went.
  function folderMark(marks, path) {
    var kinds = {};
    Object.keys(marks).forEach(function (p) { if (p.indexOf(path + "/") === 0) kinds[gitClass(marks[p].status)] = true; });
    var list = Object.keys(kinds);
    if (!list.length) return "";
    if (kinds["git-conflict"]) return "git-conflict";
    return list.length === 1 ? list[0] : "git-M";
  }
  function renderTree() {
    var list = $("files-list"), top = list.scrollTop, marks = gitMarks();
    list.textContent = "";
    if (!files.lists[""]) { list.appendChild(el("p", { class: "muted tree-note", text: "Loading…" })); return; }
    if (!files.lists[""].length) list.appendChild(el("p", { class: "muted empty-preview", text: "This folder is empty. Upload files into it, or drop them here." }));
    (function rows(dir, depth) {
      (files.lists[dir] || []).forEach(function (entry) {
        var path = joinPath(dir, entry.name), open = entry.dir && files.open[path];
        var mark = !entry.dir && marks[path];
        var colour = entry.dir ? folderMark(marks, path) : mark ? gitClass(mark.status) : "";
        list.appendChild(el("button", {
          class: "file-row" + (open ? " open" : "") + (path === files.file ? " current" : "") + (entry.dir && path === files.dir ? " target" : ""),
          role: "treeitem", "aria-level": String(depth + 1), "aria-expanded": entry.dir ? String(!!open) : null, "data-path": path,
          title: path + (entry.dir ? "" : " · " + size(entry.size)) + (entry.modified ? " · " + ago(entry.modified) : ""),
          style: "padding-left:" + (8 + depth * 14) + "px",
          onclick: function () { if (entry.dir) toggleDir(path); else { files.dir = dir; target(); openTab(files.root, path); } },
        }, [
          el("span", { class: "file-chevron" }, entry.dir ? [icon("chevron").firstChild] : []),
          icon(entry.dir ? "dir" : "file"),
          el("span", { class: "file-name" + (colour ? " " + colour : ""), text: entry.name }),
          mark && !mark.binary && (mark.added || mark.removed) ? el("span", { class: "file-lines", title: mark.added + " lines added, " + mark.removed + " removed" }, [
            mark.added ? el("span", { class: "plus", text: "+" + mark.added }) : null,
            mark.removed ? el("span", { class: "minus", text: "−" + mark.removed }) : null,
          ]) : null,
          colour ? el("span", { class: "file-git " + colour, title: mark ? gitWords[mark.status] || "" : "Has changes", text: mark ? gitLetter(mark.status) : "•" }) : null,
        ]));
        if (open) {
          if (files.lists[path]) rows(path, depth + 1);
          else list.appendChild(el("p", { class: "muted tree-note", style: "padding-left:" + (34 + depth * 14) + "px", text: "Loading…" }));
        }
      });
    })("", 0);
    list.scrollTop = top;
  }
  function toggleDir(path) {
    if (files.open[path]) {
      delete files.open[path];
      Object.keys(files.open).forEach(function (dir) { if (dir.indexOf(path + "/") === 0) delete files.open[dir]; });
      files.dir = path.split("/").slice(0, -1).join("/");
    } else {
      files.open[path] = true;
      files.dir = path;
    }
    keepFiles();
    target();
    renderTree();
    if (files.open[path] && !files.lists[path]) listOf(path).then(renderTree).catch(function (error) { toast(error.message); delete files.open[path]; renderTree(); });
  }

  // widenSide gives a preview or a diff the whole page, over the bar, the
  // panel's icons, the chat and the left panel; its ✕, or Esc, puts it
  // back in its panel.
  function widenSide() { fullSide(!$("chat-side").classList.contains("full")); }
  function fullSide(on) {
    $("chat-side").classList.toggle("full", on);
    document.querySelectorAll(".side-full-btn").forEach(fullButton);
  }
  function fullButton(button) {
    var on = $("chat-side").classList.contains("full");
    button.textContent = on ? "✕" : "⤢";
    button.title = on ? "Back to the panel" : "Full screen";
    button.setAttribute("aria-label", button.title);
    button.setAttribute("aria-pressed", String(on));
    return button;
  }
  document.addEventListener("keydown", function (event) {
    if (event.key === "Escape" && $("chat-side").classList.contains("full") && !document.querySelector("dialog[open]")) fullSide(false);
  });
  // ---- the panels, each its views by the icons along its top
  function keepSide() {
    try {
      localStorage.setItem("hi-dev-panes", JSON.stringify({ v: 1,
        left: { view: panes.left.view, open: panes.left.open, width: panes.left.width },
        right: { view: panes.right.view, open: panes.right.open, width: panes.right.width } }));
    } catch (e) { /* private window */ }
  }
  // The right panel is beside the chat, the terminal, and the open files;
  // the left is beside them all.
  function sideBeside() { return chat.view === "chat" || chat.view === "terminal" || chat.view === "file"; }
  function paneShown(which) { return !$(paneIds[which].panel).hidden && !document.hidden; }
  function viewShown(view) { var which = paneOf(view); return paneShown(which) && panes[which].view === view; }
  // Both stay on Start something new: the right one has Stage, Release,
  // Logs, and the rest, which need no conversation. Until hi 2.6.118 it
  // hid there, from when its only views were the workspace's.
  function renderSide() {
    ["left", "right"].forEach(function (which) {
      var pane = panes[which], ids = paneIds[which], panel = $(ids.panel);
      var beside = which === "left" || onePane() !== "" || sideBeside();
      // The mode's: off a view it hides, to the first it shows; none, no panel.
      var views = shownViews(which);
      if (views.length && views.indexOf(pane.view) < 0) pane.view = views[0];
      beside = beside && views.length > 0;
      panel.hidden = !pane.open || !beside;
      panel.style.setProperty("--side-width", pane.width + "px");
      $(ids.toggle).setAttribute("aria-pressed", String(pane.open));
      $(ids.toggle).hidden = !beside;
      panel.querySelectorAll(".side-tabs button[data-side]").forEach(function (button) {
        button.setAttribute("aria-selected", String(button.dataset.side === pane.view));
      });
      paneViews[which].forEach(function (view) { $("side-" + view).hidden = pane.view !== view; });
    });
    // Full width is the preview's and the diff's: closed, or another view, and it's a panel again.
    var right = $("chat-side");
    if (right.classList.contains("full") && (right.hidden || (panes.right.view !== "preview" && panes.right.view !== "git"))) fullSide(false);
  }
  // sideShown brings the open views up to date, when they're on the screen.
  // Not before the page has started: their views' code comes later.
  var booted = false;
  function sideShown() {
    if (!booted) return;
    ["left", "right"].forEach(function (which) { if (paneShown(which)) viewNow(panes[which].view); });
  }
  function viewNow(view) {
    if (view === "files") { openFiles(); if (!git.view || git.space !== chat.space || Date.now() - git.at > 5000) refreshGit(); }
    if (view === "git") refreshGit();
    if (view === "search") {
      if (search.space && search.space !== chat.space) runSearch(true);
      if (!sideOver.matches) $("search-input").focus({ preventScroll: true });
    }
    if (view === "checks") loadChecks();
    if (view === "logs") { logs.news = ""; renderLogsBadge(); loadLogs(); }
    if (view === "preview") loadPreview(false);
    if (view === "tasks") { renderSideTasks(); if (stask.ticket && !$("stask-one").hidden) showSideTask(stask.ticket.number, true); }
  }
  // freshView fetches what a view shows only when it comes up: Decisions,
  // Data, and Users.
  function freshView(view) {
    if (view === "decisions") refreshDecisions();
    if (view === "data") refreshData();
    if (view === "people" && !developer()) refreshPeople();
  }
  // setSide opens a view in its panel, or with view null, shows or hides
  // the panel which. On a narrow screen, opening one hides the other.
  function setSide(view, open, which) {
    if (peek && view && view !== peek && paneOf(view) === paneOf(peek)) { peek = ""; applyMode(); }
    which = view ? paneOf(view) : which || "right";
    var pane = panes[which];
    var was = paneShown(which) ? pane.view : "";
    if (view) pane.view = view;
    if (open != null) pane.open = open;
    if (pane.open && sideOver.matches) panes[which === "left" ? "right" : "left"].open = false;
    keepSide();
    renderSide();
    sideShown();
    if (paneShown(which) && was !== pane.view) freshView(pane.view);
  }
  // revealSide opens a view from anywhere on the page: the chat, a link,
  // the other panel.
  function revealSide(view) {
    if (view !== peek && viewMode.mode === "vibe" && viewMode.hides.indexOf(view) >= 0) { peek = view; applyMode(); }
    if (paneOf(view) === "right" && !onePane() && !sideBeside()) setCodeView("chat");
    setSide(view, true);
  }
  // Keys has its parts one at a time, picked with the chips above them:
  // your SSH key, the app's settings, its keys and sign-ins (owners), and
  // the sites the dev box reaches.
  var keysPart = "ssh";
  try { keysPart = localStorage.getItem("hi-dev-keys") || keysPart; } catch (e) { /* private window */ }
  function showKeys(part) {
    var chip = document.querySelector('#keys-show [data-keys="' + part + '"]');
    if (!chip || getComputedStyle(chip).display === "none") part = "ssh";
    keysPart = part;
    try { localStorage.setItem("hi-dev-keys", part); } catch (e) { /* private window */ }
    document.querySelectorAll("#keys-show [data-keys]").forEach(function (button) { button.setAttribute("aria-pressed", String(button.dataset.keys === part)); });
    document.querySelectorAll("#side-security [data-keys-pane]").forEach(function (pane) { pane.hidden = pane.dataset.keysPane !== part; });
  }
  function revealKeys(part) {
    showKeys(part);
    revealSide("security");
  }
  document.querySelectorAll("#keys-show [data-keys]").forEach(function (button) {
    button.addEventListener("click", function () { showKeys(button.dataset.keys); });
  });
  document.querySelectorAll(".side-tabs button[data-side]").forEach(function (button) {
    button.addEventListener("click", function () { setSide(button.dataset.side, true); });
  });
  ["left", "right"].forEach(function (which) {
    var ids = paneIds[which], pane = panes[which];
    $(ids.close).addEventListener("click", function () { setSide(null, false, which); });
    $(ids.toggle).addEventListener("click", function () {
      var opening = !pane.open;
      setSide(null, opening, which);
      if (opening) freshView(pane.view);
    });
    // The panel's inner edge drags it wider or narrower; a double click
    // puts it back.
    var handle = $(ids.resize), from = 0, width = 0;
    handle.addEventListener("pointerdown", function (event) {
      if (event.button !== 0) return;
      event.preventDefault();
      from = event.clientX;
      width = $(ids.panel).getBoundingClientRect().width;
      handle.setPointerCapture(event.pointerId);
      handle.classList.add("dragging");
    });
    handle.addEventListener("pointermove", function (event) {
      if (!handle.classList.contains("dragging")) return;
      var other = which === "left" ? "right" : "left";
      var beside = paneShown(other) ? $(paneIds[other].panel).getBoundingClientRect().width : 0;
      var most = Math.max(240, document.querySelector(".code-body").clientWidth - beside - 320);
      var moved = which === "left" ? event.clientX - from : from - event.clientX;
      pane.width = Math.round(Math.min(most, Math.max(240, width + moved)));
      $(ids.panel).style.setProperty("--side-width", pane.width + "px");
    });
    function done() {
      if (!handle.classList.contains("dragging")) return;
      handle.classList.remove("dragging");
      keepSide();
    }
    handle.addEventListener("pointerup", done);
    handle.addEventListener("pointercancel", done);
    handle.addEventListener("dblclick", function () { pane.width = ids.width; keepSide(); renderSide(); });
  });
  // A narrow screen's panel lies over the chat: tapping the chat closes it.
  function closeOver() {
    if (!sideOver.matches || (!panes.left.open && !panes.right.open)) return;
    panes.left.open = false;
    panes.right.open = false;
    keepSide();
    renderSide();
  }
  $("chat-log").addEventListener("click", closeOver);
  $("term").addEventListener("pointerdown", closeOver, true); // xterm keeps its clicks

  // ---- Source control: the chat's workspace's changes, staged or not,
  // each file's diff, a commit, and its branch's commits. Try on Stage and
  // Ship to Dev too, once it has commits dev hasn't.
  function gitShown() { return viewShown("git"); }
  function refreshGit() {
    if (!chat.space) { git.error = ""; return; } // no workspace: nothing failed
    if (git.busy) return;
    var asked = chat.space;
    git.busy = true;
    api("GET", "git?workspace=" + encodeURIComponent(asked)).then(function (view) {
      if (asked !== chat.space) return;
      git.view = view;
      git.space = asked;
      git.error = "";
    }).catch(function (error) {
      if (asked === chat.space) git.error = error.message;
    }).then(function () {
      git.busy = false;
      git.at = Date.now();
      if (asked !== chat.space) return;
      renderGit();
      if (filesShown()) loadTree();
    });
  }
  setInterval(function () {
    // Beside the terminal, the chat's poll is off: Rewind asks itself.
    if (gitShown() && Date.now() - git.at > 4000) refreshGit();
    else if (filesShown() && files.loaded && Date.now() - files.at > 8000) {
      // The tree, and its letters, as the agent changes files.
      if (Date.now() - git.at > 6000) refreshGit(); else loadTree();
    }
  }, 1000);
  function count(n, one, many) { return n + " " + (n === 1 ? one : many); }
  function gitLetter(status) { return status === "?" ? "U" : status; }
  function gitClass(status) { return status === "?" ? "git-new" : status === "!" ? "git-conflict" : "git-" + status; }
  var gitWords = { M: "Changed", A: "Added", D: "Deleted", R: "Renamed", C: "Copied", T: "Changed its type", "?": "New. Git does not track it yet.", "!": "Conflict. Resolve it." };
  function gitBusyWords() {
    var agent = chat.agent || {};
    return agent.status === "working" ? " Claude is working here. It can change these files at this moment." : "";
  }
  function renderGit() {
    renderNotesBar();
    var view = git.view && git.space === chat.space ? git.view : null;
    var space = chatSpace() || {};
    var lists = $("git-lists");
    $("git-error").textContent = chat.space ? git.error : "";
    var all = view ? view.staged.length + view.changes.length + (view.conflicts || []).length : 0;
    var badge = $("git-count");
    badge.hidden = !(view ? all : space.dirty);
    badge.classList.toggle("dot", !view);
    badge.textContent = view && all ? (all > 99 ? "99+" : String(all)) : "";
    if (!view) {
      $("git-branch").textContent = space.branch || space.label || "";
      $("git-base").textContent = "";
      $("git-ship").textContent = "";
      $("git-commits").textContent = "";
      lists.textContent = "";
      lists.appendChild(el("p", { class: "muted small", text: chat.space ? (git.error ? "" : "Loading…") : "Pick a workspace to see its changes." }));
      $("git-commit").disabled = true;
      return;
    }
    $("git-branch").textContent = view.branch || "(no branch)";
    var where = [];
    if (!space.path) where.push("The main clone of the project");
    where.push(view.ahead ? count(view.ahead, "commit", "commits") + " not in dev" : view.branch === view.base ? "dev itself" : "No commits that are not in dev");
    if (view.behind) where.push("dev has " + count(view.behind, "commit", "commits") + " that this branch does not have");
    $("git-base").textContent = where.join(" · ");
    var ship = $("git-ship");
    ship.textContent = "";
    if (space.path && space.branch && view.ahead > 0) {
      var shipping = Object.assign({}, space, { ahead: view.ahead, dirty: all > 0 });
      ship.appendChild(el("button", { text: "Preview only this", busy: "ship:" + space.id, title: "Show only this branch on the preview, without the rest of the next version", onclick: function () { previewOnly(shipping); } }));
      ship.appendChild(addButton(shipping));
    }
    var commit = $("git-commit");
    commit.textContent = view.staged.length ? "Commit" : "Commit all";
    commit.disabled = !(view.staged.length || view.changes.length);
    lists.textContent = "";
    if (view.conflicts && view.conflicts.length) lists.appendChild(gitSection("Conflicts", view.conflicts, "conflict"));
    if (view.staged.length) lists.appendChild(gitSection("Staged", view.staged, "staged"));
    if (view.changes.length || !view.staged.length) lists.appendChild(gitSection("Changes", view.changes, "changes"));
    if (!all) lists.appendChild(el("p", { class: "muted small", text: "Nothing to commit. All changes are in a commit." }));
    var commits = $("git-commits");
    commits.textContent = "";
    $("git-commits-box").querySelector("summary").textContent = "Commits" + (view.ahead ? " · " + view.ahead + " new" : "");
    view.commits.forEach(function (c) {
      commits.appendChild(el("li", { class: c.new ? "new" : "", title: c.hash + "\n" + c.subject + "\n" + c.author + (c.new ? "\nNot in dev yet" : "") }, [
        el("span", { class: "git-hash", text: c.short }),
        el("span", { class: "git-subject", text: c.subject }),
        el("span", { class: "git-when", text: ago(c.at) }),
      ]));
    });
    if (!view.commits.length) commits.appendChild(el("li", { class: "muted small", text: "No commits yet." }));
  }
  function gitSection(title, list, kind) {
    var box = el("div", { class: "git-section" });
    var acts = [];
    var paths = list.map(function (file) { return file.path; });
    if (kind === "staged") acts.push(el("button", { class: "side-icon", text: "−", title: "Unstage them all", "aria-label": "Unstage them all", onclick: function () { gitDo("unstage", []); } }));
    if (kind === "changes" && list.length) {
      acts.push(el("button", { class: "side-icon", text: "↶", title: "Discard them all", "aria-label": "Discard them all", onclick: function () {
        gitDo("discard", paths, "Discard all changes that are not staged, " + count(list.length, "file", "files") + "? Each file goes back to the last commit. This also removes its staged changes. hi deletes new files. You cannot undo this." + gitBusyWords());
      } }));
      acts.push(el("button", { class: "side-icon", text: "+", title: "Stage them all", "aria-label": "Stage them all", onclick: function () { gitDo("stage", []); } }));
    }
    box.appendChild(el("div", { class: "git-section-head" }, [el("span", { text: title }), list.length ? el("span", { class: "count", text: String(list.length) }) : null, el("span", { class: "grow" })].concat(acts)));
    list.forEach(function (file) { box.appendChild(gitRow(file, kind)); });
    return box;
  }
  function gitRow(file, kind) {
    var parts = file.path.split("/"), name = parts.pop(), dir = parts.join("/");
    var diffKind = kind === "staged" ? "staged" : file.status === "?" ? "new" : "";
    var acts = [];
    function stop(fn) { return function (event) { event.stopPropagation(); fn(); }; }
    if (kind === "staged") acts.push(el("button", { class: "side-icon", text: "−", title: "Unstage", "aria-label": "Unstage", onclick: stop(function () { gitDo("unstage", file.old ? [file.old, file.path] : [file.path]); }) }));
    if (kind === "changes") {
      acts.push(el("button", { class: "side-icon", text: "↶", title: "Discard the changes", "aria-label": "Discard the changes", onclick: stop(function () { discardFile(file); }) }));
      acts.push(el("button", { class: "side-icon", text: "+", title: "Stage", "aria-label": "Stage", onclick: stop(function () { gitDo("stage", [file.path]); }) }));
    }
    if (kind === "conflict") acts.push(el("button", { class: "side-icon", text: "+", title: "Mark it resolved: stage it", "aria-label": "Mark it resolved", onclick: stop(function () { gitDo("stage", [file.path]); }) }));
    var lines = file.binary ? [el("span", { class: "muted", text: "binary" })] : [
      file.added ? el("span", { class: "plus", text: "+" + file.added }) : null,
      file.removed ? el("span", { class: "minus", text: " −" + file.removed }) : null,
    ];
    var row = el("div", {
      class: "git-file" + (git.diff && git.diff.key === diffKind + ":" + file.path ? " current" : ""), role: "button", tabindex: "0",
      title: file.path + (file.old ? " (was " + file.old + ")" : "") + " · " + (gitWords[file.status] || file.status),
      onclick: function () { openDiff(file, diffKind, kind); },
      onkeydown: function (event) { if (event.key === "Enter" && event.target === row) openDiff(file, diffKind, kind); },
    }, [
      icon("file"),
      el("span", { class: "git-name " + gitClass(file.status), text: name }),
      el("span", { class: "git-dir muted", text: file.old ? "← " + file.old : dir }),
      el("span", { class: "git-lines" }, lines),
      el("span", { class: "git-acts" }, acts),
      el("span", { class: "git-letter " + gitClass(file.status), text: gitLetter(file.status) }),
    ]);
    return row;
  }
  function discardFile(file) {
    var words = file.status === "?" ? "Delete " + file.path + "? It is a new file that git does not have. You cannot get it back."
      : "Discard the changes to " + file.path + "? The file goes back to the last commit. This also removes its staged changes. You cannot undo this.";
    gitDo("discard", [file.path], words + gitBusyWords());
  }
  function gitDo(action, paths, question) {
    var asked = chat.space;
    (question ? ask(question) : Promise.resolve("")).then(function (answer) {
      if (answer === null) return;
      var message = $("git-message").value;
      return api("POST", "git", { workspace: asked, action: action, paths: paths, message: message }).then(function (view) {
        if (asked !== chat.space) return;
        git.view = view;
        git.space = asked;
        git.error = "";
        git.at = Date.now();
        if (action === "commit") {
          $("git-message").value = "";
          toast("Committed.");
          refresh();
        }
        if (git.diff) {
          var still = view.staged.concat(view.changes).some(function (f) { return git.diff.kind + ":" + f.path === git.diff.key || f.path === git.diff.file.path; });
          if (!still || action === "discard") closeDiff(); else openDiff(git.diff.file, git.diff.kind, git.diff.list, true);
        }
        renderGit();
        if (files.loaded) { files.at = 0; loadTree(); }
      }).catch(function (error) {
        git.error = error.message;
        renderGit();
        refreshGit();
      });
    });
  }
  $("git-form").addEventListener("submit", function (event) {
    event.preventDefault();
    var message = $("git-message").value.trim();
    if (!message) { $("git-error").textContent = "Write a message that says what the commit does."; $("git-message").focus(); return; }
    var busy = gitBusyWords();
    gitDo("commit", [], busy ? "Commit the changes as they are now?" + busy : "");
  });
  $("git-message").addEventListener("keydown", function (event) {
    if (event.key === "Enter" && (event.ctrlKey || event.metaKey)) { event.preventDefault(); $("git-form").requestSubmit(); }
  });
  $("git-refresh").addEventListener("click", function () { git.at = 0; refreshGit(); });
  // A file's diff takes the view, with a way back; its lines colored.
  function openDiff(file, kind, list, again) {
    var key = kind + ":" + file.path;
    git.diff = { key: key, file: file, kind: kind, list: list };
    var box = $("git-diff");
    box.hidden = false;
    $("side-git").classList.add("showing-diff");
    var parts = file.path.split("/");
    var acts = [];
    if (list === "staged") acts.push(el("button", { class: "small", text: "Unstage", onclick: function () { gitDo("unstage", file.old ? [file.old, file.path] : [file.path]); } }));
    if (list === "changes" || list === "conflict") acts.push(el("button", { class: "small", text: "Stage", onclick: function () { gitDo("stage", [file.path]); } }));
    if (list === "changes") acts.push(el("button", { class: "small danger", text: "Discard", onclick: function () { discardFile(file); } }));
    var head = el("div", { class: "preview-head" }, [
      el("button", { class: "side-icon", text: "←", title: "Back to the changes", "aria-label": "Back to the changes", onclick: closeDiff }),
      el("strong", { class: "preview-name " + gitClass(file.status), text: parts[parts.length - 1], title: file.path }),
      el("span", { class: "muted small", text: list === "staged" ? "staged" : "" }),
      el("span", { class: "grow" }),
    ].concat(acts, [
      file.status !== "D" ? el("button", { class: "side-icon", text: "↗", title: "Open the file, to edit it", "aria-label": "Open the file", onclick: function () { openPath(file.path); } }) : null,
      fullButton(el("button", { class: "side-icon side-full-btn", text: "⤢", onclick: widenSide })),
    ]));
    var body = el("pre", { class: "diff" }, again && box.querySelector(".diff") ? [].slice.call(box.querySelector(".diff").childNodes) : [el("div", { class: "meta", text: "Loading the diff…" })]);
    box.textContent = "";
    box.appendChild(head);
    box.appendChild(body);
    var query = "git/diff?workspace=" + encodeURIComponent(chat.space) + "&kind=" + encodeURIComponent(kind) +
      (file.old && kind === "staged" ? "&path=" + encodeURIComponent(file.old) : "") + "&path=" + encodeURIComponent(file.path);
    api("GET", query).then(function (result) {
      if (!git.diff || git.diff.key !== key) return;
      body.textContent = "";
      var text = result.diff || "";
      if (!text.trim()) { body.appendChild(el("div", { class: "meta", text: file.binary ? "A binary file: no lines to show." : "No differences to show." })); return; }
      var oldLine = 0, newLine = 0, inHunk = false;
      text.replace(/\n$/, "").split("\n").forEach(function (line) {
        var cls = !inHunk && /^(diff |index |--- |\+\+\+ |new file|deleted file|similarity|rename |old mode|new mode|Binary )/.test(line) ? "meta"
          : line[0] === "@" ? "hunk" : line[0] === "+" ? "add" : line[0] === "-" ? "del" : line[0] === "\\" ? "meta" : "";
        if (cls === "hunk") {
          var at = /^@@ -(\d+)(?:,\d+)? \+(\d+)/.exec(line);
          if (at) { oldLine = Number(at[1]); newLine = Number(at[2]); inHunk = true; }
        } else if (/^diff /.test(line)) inHunk = false;
        if (!inHunk || cls === "hunk" || cls === "meta") { body.appendChild(el("div", { class: cls, text: line || " " })); return; }
        // A line of the change: a note on it says where, in the file as
        // it is now, or as it was for a line taken out.
        var where = cls === "del" ? { line: oldLine++, gone: true } : { line: newLine++, gone: false };
        if (cls !== "add" && cls !== "del") oldLine++;
        body.appendChild(diffLine(file.path, cls, line, where));
        diffNotesAt(file.path, where).forEach(function (note) { body.appendChild(noteBox(note)); });
      });
      markNoted(body);
    }).catch(function (error) {
      if (!git.diff || git.diff.key !== key) return;
      body.textContent = "";
      body.appendChild(el("div", { class: "meta", text: error.message }));
    });
    renderGit();
  }
  // ---- Notes on a diff's lines, for the agent (release 5): a click on
  // a line's + adds one; Send to the agent puts them all in the chat's
  // box, each with its file and line, as one message to add to and send.
  // They are the workspace's, in this browser, until sent or cleared.
  function notesKey() { return "hi-dev-notes-" + ((state && state.team) || "") + "-" + chat.space; }
  function loadNotes() {
    try { return JSON.parse(localStorage.getItem(notesKey()) || "[]") || []; } catch (e) { return []; }
  }
  function saveNotes(notes) {
    try { if (notes.length) localStorage.setItem(notesKey(), JSON.stringify(notes)); else localStorage.removeItem(notesKey()); } catch (e) { /* private window */ }
    renderNotesBar();
  }
  function diffNotesAt(path, where) {
    return loadNotes().filter(function (note) { return note.path === path && note.line === where.line && !!note.gone === where.gone; });
  }
  function diffLine(path, cls, text, where) {
    var row = el("div", { class: "line " + cls, text: text || " " });
    row.dataset.line = where.line;
    row.dataset.gone = where.gone ? "1" : "";
    row.insertBefore(el("button", { type: "button", class: "note-add", text: "+", tabindex: "-1",
      title: "Add a note for the agent on this line", "aria-label": "Add a note on line " + where.line + (where.gone ? ", taken out" : ""),
      onclick: function (event) { event.stopPropagation(); editNote(row, { path: path, line: where.line, gone: where.gone, code: text.slice(1).trim(), text: "" }, true); } }), row.firstChild);
    return row;
  }
  function markNoted(body) {
    var notes = loadNotes();
    body.querySelectorAll("div.line").forEach(function (row) {
      var path = git.diff && git.diff.file.path;
      row.classList.toggle("noted", notes.some(function (note) { return note.path === path && String(note.line) === row.dataset.line && !!note.gone === !!row.dataset.gone; }));
    });
  }
  // noteBox is a saved note under its line, with Edit and Delete.
  function noteBox(note) {
    var box = el("div", { class: "diff-note" });
    box.appendChild(el("div", { class: "note-text", text: note.text }));
    box.appendChild(el("div", { class: "note-acts" }, [
      el("button", { type: "button", class: "small link", text: "Edit", onclick: function () { editNote(box, note, false); } }),
      el("button", { type: "button", class: "small link", text: "Delete", onclick: function () {
        saveNotes(loadNotes().filter(function (other) { return other.id !== note.id; }));
        var body = box.parentNode;
        box.remove();
        if (body) markNoted(body);
      } }),
    ]));
    return box;
  }
  // editNote writes a note: a new one under its line, or a saved one in place.
  function editNote(anchor, note, fresh) {
    var open = anchor.parentNode && anchor.parentNode.querySelector(".diff-note.editing");
    if (open) open.querySelector("textarea").focus();
    if (open) return;
    var input = el("textarea", { "aria-label": "Your note on line " + note.line, placeholder: "What should the agent change here?" });
    input.value = note.text;
    var box = el("div", { class: "diff-note editing" }, [input]);
    function done() {
      var text = input.value.trim();
      var body = box.parentNode;
      if (!text) { box.remove(); if (!fresh) anchor.hidden = false; return; }
      var notes = loadNotes().filter(function (other) { return other.id !== note.id; });
      note.text = text;
      note.id = note.id || Date.now().toString(36) + Math.random().toString(36).slice(2, 6);
      notes.push(note);
      saveNotes(notes);
      var saved = noteBox(note);
      box.replaceWith(saved);
      if (!fresh) anchor.remove();
      if (body) markNoted(body);
    }
    box.appendChild(el("div", { class: "note-acts" }, [
      el("button", { type: "button", class: "small", text: "Cancel", onclick: function () { box.remove(); if (!fresh) anchor.hidden = false; } }),
      el("button", { type: "button", class: "small primary", text: fresh ? "Add the note" : "Save", onclick: done }),
    ]));
    input.addEventListener("keydown", function (event) {
      if (event.key === "Enter" && (event.ctrlKey || event.metaKey)) { event.preventDefault(); done(); }
      if (event.key === "Escape") { event.stopPropagation(); box.remove(); if (!fresh) anchor.hidden = false; }
    });
    if (fresh) {
      // Under the line, after its notes.
      var after = anchor;
      while (after.nextSibling && after.nextSibling.classList && after.nextSibling.classList.contains("diff-note")) after = after.nextSibling;
      after.after(box);
    } else {
      anchor.hidden = true;
      anchor.after(box);
    }
    input.focus();
  }
  function renderNotesBar() {
    var notes = state ? loadNotes() : [];
    var files = {};
    notes.forEach(function (note) { files[note.path] = true; });
    var n = Object.keys(files).length;
    $("git-notes").hidden = !notes.length;
    $("git-notes-count").textContent = notes.length + (notes.length === 1 ? " note" : " notes") + " on " + n + (n === 1 ? " file" : " files");
  }
  // notesMessage is the notes as one message: by file, then line.
  function notesMessage(notes) {
    // By file, then line; on the same number, the line taken out first, as the diff has it.
    notes = notes.slice().sort(function (a, b) { return a.path < b.path ? -1 : a.path > b.path ? 1 : a.line - b.line || (b.gone ? 1 : 0) - (a.gone ? 1 : 0); });
    var lines = ["Notes on the changes, line by line:"];
    notes.forEach(function (note) {
      var code = note.code.length > 80 ? note.code.slice(0, 77) + "…" : note.code;
      lines.push("", "- " + note.path + ":" + note.line + (note.gone ? " (a line taken out)" : "") + (code ? ", `" + code.replace(/`/g, "'") + "`" : "") + ":");
      note.text.split("\n").forEach(function (part) { lines.push("  " + part); });
    });
    return lines.join("\n");
  }
  $("git-notes-send").addEventListener("click", function () {
    var notes = loadNotes();
    if (!notes.length) return;
    var input = $("chat-input");
    if (input.disabled) { toast("The agent is not running in this workspace. Start it, then send the notes."); return; }
    input.value = (input.value.trim() ? input.value.replace(/\s+$/, "") + "\n\n" : "") + notesMessage(notes);
    input.dispatchEvent(new Event("input"));
    saveNotes([]);
    if (git.diff) openDiff(git.diff.file, git.diff.kind, git.diff.list);
    setCodeView("chat");
    input.focus();
    toast("The notes are in the chat box. Add to them, then send.");
  });
  $("git-notes-clear").addEventListener("click", function () {
    var notes = loadNotes();
    if (!notes.length || !confirm("Clear the " + (notes.length === 1 ? "note" : notes.length + " notes") + " on the changes?")) return;
    saveNotes([]);
    if (git.diff) openDiff(git.diff.file, git.diff.kind, git.diff.list);
  });
  function closeDiff() {
    git.diff = null;
    $("git-diff").hidden = true;
    $("git-diff").textContent = "";
    $("side-git").classList.remove("showing-diff");
  }
  renderSide();
  renderGit();

  // renderMarkdown builds a Markdown file's page from DOM nodes, never
  // from its HTML: headings, lists, quotes, code, tables, links, and the
  // project's own pictures.
  // renderMarkdown puts Markdown on the page; breaks keeps a paragraph's
  // line breaks, as GitHub does in issues and comments.
  function renderMarkdown(text, dir, breaks) {
    var box = el("div", { class: "md" });
    var lines = text.replace(/\r\n?/g, "\n").split("\n");
    function resolve(href) {
      var parts = (dir ? dir.split("/") : []).concat(href.split("#")[0].split("?")[0].split("/"));
      var out = [];
      parts.forEach(function (part) {
        if (part === "..") out.pop();
        else if (part && part !== ".") out.push(part);
      });
      return out.join("/");
    }
    function inline(source) {
      var nodes = [];
      var rules = [
        [/`([^`]+)`/, function (m) { return el("code", { text: m[1] }); }],
        [/!\[([^\]]*)\]\(([^)\s]+)(?:\s+"[^"]*")?\)/, function (m) {
          if (/^[a-z]+:/i.test(m[2])) return el("a", { href: m[2], target: "_blank", rel: "noopener", text: m[1] || m[2] });
          return el("img", { src: "api/files/raw?" + fileQuery(resolve(m[2])), alt: m[1] });
        }],
        [/\[([^\]]+)\]\(([^)\s]+)(?:\s+"[^"]*")?\)/, function (m) {
          var href = m[2];
          if (/^(https?:|mailto:)/i.test(href)) return el("a", { href: href, target: "_blank", rel: "noopener" }, inline(m[1]));
          if (/^[a-z]+:/i.test(href) || href.charAt(0) === "#") return el("span", {}, inline(m[1]));
          var target = resolve(href);
          return el("a", { href: "#", title: target, onclick: function (event) { event.preventDefault(); openPath(target); } }, inline(m[1]));
        }],
        [/\*\*([^*]+)\*\*|__([^_]+)__/, function (m) { return el("strong", {}, inline(m[1] || m[2])); }],
        [/\*([^*\s][^*]*)\*|\b_([^_]+)_\b/, function (m) { return el("em", {}, inline(m[1] || m[2])); }],
        [/https?:\/\/[^\s<>()]+[^\s<>().,;:!?'"]/, function (m) { return el("a", { href: m[0], target: "_blank", rel: "noopener", text: m[0] }); }],
      ];
      while (source) {
        var best = null;
        rules.forEach(function (rule) {
          var m = rule[0].exec(source);
          if (m && (!best || m.index < best.m.index)) best = { m: m, make: rule[1] };
        });
        if (!best) { nodes.push(document.createTextNode(source)); break; }
        if (best.m.index) nodes.push(document.createTextNode(source.slice(0, best.m.index)));
        nodes.push(best.make(best.m));
        source = source.slice(best.m.index + best.m[0].length);
      }
      return nodes;
    }
    var marker = /^(\s*)([-*+]|\d+[.)])\s+(.*)$/;
    // A list from line i, its items at indent; one indented under an item
    // is a list in it.
    function list(indent) {
      var first = marker.exec(lines[i]);
      var ordered = /\d/.test(first[2]);
      var node = el(ordered ? "ol" : "ul");
      if (ordered && parseInt(first[2], 10) !== 1) node.setAttribute("start", parseInt(first[2], 10));
      while (i < lines.length) {
        var m = marker.exec(lines[i]);
        if (!m || m[1].length < indent) break;
        if (m[1].length > indent) {
          (node.lastChild || node.appendChild(el("li"))).appendChild(list(m[1].length));
          continue;
        }
        var item = m[3], li = el("li");
        var task = /^\[( |x)\]\s+(.*)$/i.exec(item);
        if (task) { li.appendChild(el("input", { type: "checkbox", disabled: "" })).checked = task[1].toLowerCase() === "x"; item = " " + task[2]; }
        // Lines that carry on the item, indented.
        for (i++; i < lines.length && lines[i].trim() && /^\s/.test(lines[i]) && /^\s*/.exec(lines[i])[0].length > indent && !marker.test(lines[i]); i++) item += " " + lines[i].trim();
        inline(item).forEach(function (child) { li.appendChild(child); });
        node.appendChild(li);
        // A blank line between items keeps the list going.
        if (i + 1 < lines.length && !lines[i].trim() && (m = marker.exec(lines[i + 1])) && m[1].length >= indent) i++;
      }
      return node;
    }
    var i = 0;
    while (i < lines.length) {
      var line = lines[i];
      var m;
      if (/^\s*```/.test(line)) {
        var code = [];
        for (i++; i < lines.length && !/^\s*```/.test(lines[i]); i++) code.push(lines[i]);
        i++;
        box.appendChild(codeBlock(code.join("\n")));
      } else if ((m = /^(#{1,6})\s+(.*?)\s*#*\s*$/.exec(line))) {
        box.appendChild(el("h" + m[1].length, {}, inline(m[2])));
        i++;
      } else if (/^\s*([-*_])(\s*\1){2,}\s*$/.test(line)) {
        box.appendChild(el("hr"));
        i++;
      } else if (/^\s*>/.test(line)) {
        var quoted = [];
        for (; i < lines.length && /^\s*>/.test(lines[i]); i++) quoted.push(lines[i].replace(/^\s*>\s?/, ""));
        box.appendChild(el("blockquote", {}, [renderMarkdown(quoted.join("\n"), dir)]));
      } else if (/\|/.test(line) && i + 1 < lines.length && /^\s*\|?\s*:?-{2,}/.test(lines[i + 1])) {
        var cells = function (row) { return row.replace(/^\s*\|/, "").replace(/\|\s*$/, "").split("|").map(function (cell) { return cell.trim(); }); };
        var table = el("table", {}, [el("thead", {}, [el("tr", {}, cells(line).map(function (cell) { return el("th", {}, inline(cell)); }))])]);
        var tbody = el("tbody");
        for (i += 2; i < lines.length && /\|/.test(lines[i]) && lines[i].trim(); i++) {
          tbody.appendChild(el("tr", {}, cells(lines[i]).map(function (cell) { return el("td", {}, inline(cell)); })));
        }
        table.appendChild(tbody);
        box.appendChild(el("div", { class: "md-table" }, [table]));
      } else if (/^\s*([-*+]|\d+[.)])\s+/.test(line)) {
        box.appendChild(list(/^\s*/.exec(line)[0].length));
      } else if (!line.trim()) {
        i++;
      } else {
        var para = [];
        for (; i < lines.length && lines[i].trim() && !/^(#{1,6}\s|\s*```|\s*>|\s*([-*+]|\d+[.)])\s)/.test(lines[i]); i++) para.push(lines[i].trim());
        if (!para.length) { para.push(line.trim()); i++; }
        var p = el("p");
        para.forEach(function (text, n) {
          if (n) p.appendChild(breaks ? el("br") : document.createTextNode(" "));
          inline(text).forEach(function (child) { p.appendChild(child); });
        });
        box.appendChild(p);
      }
    }
    return box;
  }
  // openPath opens a file of the tree a Markdown link points at, or its
  // folder, in the panel beside the chat.
  function openPath(where) {
    var path = where.replace(/^\/+/, "");
    var root = spaceRoot();
    // A file opens in a tab; a folder in Files.
    api("GET", "files/stat?root=" + encodeURIComponent(root) + "&path=" + encodeURIComponent(path)).then(function () {
      openTab(root, path);
    }).catch(function (error) {
      if (/folder/.test(error.message)) revealPath(path);
      else toast(path + " is not there.");
    });
  }
  function revealPath(path) {
    revealSide("files");
    var parts = path.split("/"), dir = parts.slice(0, -1).join("/"), name = parts[parts.length - 1];
    (function wait(tries) {
      if (!files.root || !files.lists[""]) { if (tries < 40) setTimeout(function () { wait(tries + 1); }, 100); return; }
      for (var i = 1; i < parts.length; i++) files.open[parts.slice(0, i).join("/")] = true;
      keepFiles();
      loadTree().then(function () {
        var entry = (files.lists[dir] || []).filter(function (e) { return e.name === name; })[0];
        if (!entry) { toast(path + " is not there."); return; }
        files.dir = entry.dir ? path : dir;
        if (entry.dir) { if (!files.open[path]) toggleDir(path); } else openTab(files.root, path);
        target();
        var row = document.querySelector('#files-list [data-path="' + CSS.escape(path) + '"]');
        if (row) row.scrollIntoView({ block: "center" });
      });
    })(0);
  }

  // ---- Open files: each a tab beside Chat and Terminal, to read
  // and edit. A text file saves with Save or Ctrl+S, against the file as it
  // was read: one that changed since, by Claude or anyone, isn't
  // overwritten unless you say so. One open and unchanged follows the disk.
  function tabKey(root, path) { return root + "\n" + path; }
  function tabOf(key) { return opened.tabs.filter(function (t) { return t.key === key; })[0]; }
  function keepTabs() {
    try {
      localStorage.setItem("hi-dev-open-files", JSON.stringify({
        tabs: opened.tabs.map(function (t) { return { root: t.root, path: t.path }; }),
        current: opened.current,
      }));
    } catch (e) { /* private window */ }
  }
  function newTab(root, path) {
    var tab = { key: tabKey(root, path), root: root, path: path, name: path.split("/").pop(), pane: null, loaded: false,
      kind: "", base: "", eol: "\n", bom: false, saved: "", dirty: false, at: 0, busy: false, changed: null, preview: false };
    opened.tabs.push(tab);
    return tab;
  }
  function openTab(root, path, line) {
    var tab = tabOf(tabKey(root, path)) || newTab(root, path);
    tab.goto = line || 0;
    keepTabs();
    showTab(tab.key);
    closeOver();
  }
  function showTab(key) {
    opened.current = key;
    keepTabs();
    if (chat.view !== "file") { setCodeView("file"); return; } // which shows it
    renderTabs();
    var tab = tabOf(key);
    if (!tab) return;
    if (!tab.pane) tab.pane = buildPane(tab);
    var box = $("editor");
    if (tab.pane.parentNode !== box) box.appendChild(tab.pane);
    opened.tabs.forEach(function (t) { if (t.pane) t.pane.hidden = t !== tab; });
    // Its folder, state, and buttons, at the end of the tabs' row.
    $("edit-actions").replaceChildren(tab.where, tab.status, tab.previewButton, tab.download, tab.saveButton);
    if (!tab.loaded) loadTab(tab);
    else { checkTab(tab); gotoLine(tab); }
    if (tab.text && !tab.preview) tab.text.focus({ preventScroll: true });
    if (tab.root === files.root && files.file !== tab.path) { files.file = tab.path; if (filesShown()) renderTree(); }
  }
  function closeTab(key) {
    var tab = tabOf(key);
    if (!tab) return;
    (tab.dirty ? ask(tab.name + " has changes that are not saved. Close it and lose the changes?") : Promise.resolve("")).then(function (ok) {
      if (ok === null) return;
      var at = opened.tabs.indexOf(tab);
      opened.tabs.splice(at, 1);
      if (tab.pane) tab.pane.remove();
      if (opened.current === key) {
        var next = opened.tabs[Math.min(at, opened.tabs.length - 1)];
        opened.current = next ? next.key : "";
        if (chat.view === "file") { if (next) showTab(next.key); else setCodeView(opened.back || "chat"); }
      }
      if (!opened.tabs.length) $("edit-actions").textContent = "";
      if (files.file === tab.path && tab.root === files.root) { files.file = ""; if (filesShown()) renderTree(); }
      keepTabs();
      renderTabs();
    });
  }
  // The open files' tabs, in the editor's own top row; in the bar, Files,
  // with how many, to come back to them from the chat or the terminal.
  function renderTabs() {
    var strip = $("edit-tabs");
    strip.textContent = "";
    $("files-chip").hidden = !opened.tabs.length;
    $("files-count").textContent = opened.tabs.length > 1 ? String(opened.tabs.length) : "";
    var names = {};
    opened.tabs.forEach(function (t) { names[t.name] = (names[t.name] || 0) + 1; });
    opened.tabs.forEach(function (t) {
      var current = t.key === opened.current;
      var dir = t.path.split("/").slice(0, -1).join("/");
      var tab = el("button", {
        class: "edit-tab" + (current ? " current" : "") + (t.dirty ? " dirty" : ""), role: "tab", "aria-selected": String(current),
        title: t.path + (t.root !== spaceRoot() ? " · " + t.root : "") + (t.dirty ? " · not saved" : ""), "data-key": t.key,
        onclick: function (event) { if (!event.target.closest(".edit-close")) showTab(t.key); },
        onauxclick: function (event) { if (event.button === 1) { event.preventDefault(); closeTab(t.key); } },
      }, [
        el("span", { class: "edit-tab-name" + (fileMark(t) ? " " + gitClass(fileMark(t).status) : ""), text: t.name }),
        names[t.name] > 1 && dir ? el("span", { class: "edit-tab-dir", text: dir.split("/").pop() }) : null,
        el("span", { class: "edit-close", role: "button", "aria-label": "Close " + t.name, title: t.dirty ? "Not saved; close" : "Close",
          onclick: function () { closeTab(t.key); } }, [el("span", { class: "edit-dot", text: "●" }), el("span", { class: "edit-x", text: "×" })]),
      ]);
      strip.appendChild(tab);
      if (current) setTimeout(function () { tab.scrollIntoView({ block: "nearest", inline: "nearest" }); }, 0);
    });
  }
  // fileMark is git's word on an open file, when it's of the chat's
  // workspace's tree, as Source control has it.
  function fileMark(tab) {
    if (!git.view || git.space !== chat.space || spaceRoot() !== tab.root) return null;
    return gitMarks(true)[tab.path] || null;
  }
  function tabQuery(tab, extra) { return "root=" + encodeURIComponent(tab.root) + "&path=" + encodeURIComponent(tab.path) + (extra || ""); }
  function buildPane(tab) {
    var raw = "api/files/raw?" + tabQuery(tab);
    tab.status = el("span", { class: "muted small edit-status" });
    tab.saveButton = el("button", { class: "primary small", text: "Save", disabled: "", title: "Save (Ctrl+S)", onclick: function () { saveTab(tab, false); } });
    tab.previewButton = el("button", { class: "small", text: "Preview", hidden: "", onclick: function () { setPreview(tab, !tab.preview); } });
    var dir = tab.path.split("/").slice(0, -1).join(" / ");
    tab.where = el("span", { class: "muted edit-where", title: tab.path + " · " + tab.root, text: dir });
    tab.download = el("a", { class: "button-like small", href: raw + "&download=1", download: tab.name, text: "Download" });
    tab.bar = el("div", { class: "edit-bar", hidden: "" });
    tab.body = el("div", { class: "edit-body" }, [el("p", { class: "muted edit-note", text: "Opening " + tab.name + "…" })]);
    return el("div", { class: "edit-pane" }, [tab.bar, tab.body]);
  }
  function loadTab(tab, keepView, unlessTyped) {
    var raw = "api/files/raw?" + tabQuery(tab);
    var ext = ((tab.name.match(/\.([a-z0-9]+)$/i) || [])[1] || "").toLowerCase();
    tab.at = Date.now();
    if (["png", "jpg", "jpeg", "gif", "webp", "avif"].indexOf(ext) >= 0) {
      tab.kind = "image";
      tab.loaded = true;
      tab.body.textContent = "";
      tab.body.appendChild(el("div", { class: "edit-media" }, [el("img", { class: "preview-image", src: raw + "&at=" + tab.at, alt: tab.name })]));
      return Promise.resolve();
    }
    if (ext === "pdf") {
      tab.kind = "pdf";
      tab.loaded = true;
      tab.body.textContent = "";
      tab.body.appendChild(el("p", { class: "edit-note" }, [el("a", { href: raw, target: "_blank", rel: "noopener", text: "Open the PDF in a new tab" })]));
      return Promise.resolve();
    }
    return fetch(raw, { credentials: "same-origin", cache: "no-store" }).then(function (response) {
      return response.arrayBuffer().then(function (data) {
        if (!response.ok) {
          // The server's {"error": …}, in words, not as JSON.
          var said = new TextDecoder().decode(data).trim();
          try { said = JSON.parse(said).error || said; } catch (e) { /* plain text */ }
          var error = new Error(said || response.statusText);
          error.gone = response.status === 404 && /has no .* to look into/.test(said);
          throw error;
        }
        return { data: data, sha: response.headers.get("X-Hi-Sha256") || "" };
      });
    }).then(function (got) {
      // Following the disk, while someone typed since it was asked for:
      // their typing stays, and the bar says the file changed.
      if (unlessTyped && tab.text && tab.text.value !== tab.saved) {
        if (tab.changed !== got.sha) { tab.changed = got.sha; changedBar(tab, false); }
        return;
      }
      var text = new TextDecoder("utf-8", { ignoreBOM: true }).decode(got.data);
      tab.bom = text.charCodeAt(0) === 0xfeff;
      if (tab.bom) text = text.slice(1);
      tab.eol = text.indexOf("\r\n") >= 0 ? "\r\n" : "\n";
      text = text.replace(/\r\n/g, "\n");
      tab.kind = "text";
      tab.base = got.sha;
      tab.saved = text;
      tab.loaded = true;
      tab.changed = null;
      if (!tab.text) buildEditor(tab, ext);
      var scroll = tab.text.scrollTop, start = tab.text.selectionStart, end = tab.text.selectionEnd;
      tab.text.value = text;
      if (keepView) { tab.text.scrollTop = scroll; tab.text.setSelectionRange(Math.min(start, text.length), Math.min(end, text.length)); }
      edited(tab);
      if (tab.preview) setPreview(tab, true);
      if (opened.current === tab.key && chat.view === "file" && !tab.preview && !keepView) tab.text.focus({ preventScroll: true });
      gotoLine(tab);
    }).catch(function (error) {
      tab.loaded = true;
      tab.kind = "other";
      tab.body.textContent = "";
      if (error.gone && !tab.dirty) {
        // Its workspace is gone (done, or discarded): the tab goes too, and
        // with no other tab, so does Files.
        toast(tab.name + " was in a workspace that is removed now. hi closed its tab.");
        closeTab(tab.key);
        return;
      }
      tab.body.appendChild(el("p", { class: "muted edit-note", text: error.message }));
    });
  }
  function buildEditor(tab, ext) {
    tab.gutter = el("pre", { class: "edit-gutter", "aria-hidden": "true" });
    tab.text = el("textarea", { class: "edit-text", spellcheck: "false", wrap: "off", autocapitalize: "off", autocomplete: "off", "aria-label": tab.path });
    tab.md = ext === "md" || ext === "markdown";
    tab.mdBox = el("div", { class: "edit-md", hidden: "" });
    tab.previewButton.hidden = !tab.md;
    tab.body.textContent = "";
    tab.body.appendChild(el("div", { class: "edit-area" }, [tab.gutter, tab.text]));
    tab.body.appendChild(tab.mdBox);
    // A file indented with tabs keeps them; otherwise Tab puts spaces, as many as it uses.
    var lead = (tab.saved.match(/^[ \t]+/m) || [""])[0];
    tab.indent = lead.charAt(0) === "\t" ? "\t" : lead.length === 4 ? "    " : "  ";
    tab.text.addEventListener("input", function () { edited(tab); });
    tab.text.addEventListener("scroll", function () { tab.gutter.scrollTop = tab.text.scrollTop; });
    tab.text.addEventListener("keydown", function (event) {
      if ((event.ctrlKey || event.metaKey) && !event.altKey && event.key.toLowerCase() === "s") { event.preventDefault(); saveTab(tab, false); return; }
      if (event.key === "Tab" && !event.ctrlKey && !event.metaKey && !event.altKey && !event.shiftKey) {
        event.preventDefault();
        document.execCommand("insertText", false, tab.indent); // keeps undo
      }
    });
  }
  // gotoLine selects a line the tab was opened at, as Search does, and
  // brings it to a third of the way down.
  function gotoLine(tab) {
    var line = tab.goto;
    if (!line || !tab.text) return;
    tab.goto = 0;
    if (tab.preview) setPreview(tab, false);
    var value = tab.text.value, start = 0;
    for (var i = 1; i < line; i++) {
      var next = value.indexOf("\n", start);
      if (next < 0) { start = value.length; break; }
      start = next + 1;
    }
    var end = value.indexOf("\n", start);
    tab.text.focus({ preventScroll: true });
    tab.text.setSelectionRange(start, end < 0 ? value.length : end);
    var height = parseFloat(getComputedStyle(tab.text).lineHeight) || 19.5;
    tab.text.scrollTop = Math.max(0, (line - 1) * height - tab.text.clientHeight / 3);
    tab.text.scrollLeft = 0;
  }
  var lineFrame = 0;
  function edited(tab) {
    tab.dirty = tab.text.value !== tab.saved;
    var lines = tab.text.value.split("\n").length;
    if (tab.lines !== lines) {
      tab.lines = lines;
      cancelAnimationFrame(lineFrame);
      lineFrame = requestAnimationFrame(function () {
        var out = [];
        for (var i = 1; i <= tab.lines; i++) out.push(i);
        tab.gutter.textContent = out.join("\n") + "\n";
        tab.gutter.scrollTop = tab.text.scrollTop;
      });
    }
    tab.saveButton.disabled = !tab.dirty || tab.busy;
    tab.status.textContent = tab.busy ? "Saving…" : tab.dirty ? "Not saved" : "";
    var shown = document.querySelector('#edit-tabs [data-key="' + CSS.escape(tab.key) + '"]');
    if (shown && shown.classList.contains("dirty") !== tab.dirty) renderTabs();
  }
  function setPreview(tab, on) {
    tab.preview = on;
    tab.previewButton.textContent = on ? "Edit" : "Preview";
    tab.body.querySelector(".edit-area").hidden = on;
    tab.mdBox.hidden = !on;
    if (on) {
      tab.mdBox.textContent = "";
      var dir = tab.path.split("/").slice(0, -1).join("/");
      tab.mdBox.appendChild(renderMarkdown(tab.text.value, dir));
    } else tab.text.focus({ preventScroll: true });
  }
  function saveTab(tab, force) {
    if (tab.kind !== "text" || tab.busy || (!tab.dirty && !force)) return;
    var value = tab.text.value;
    tab.busy = true;
    edited(tab);
    api("POST", "files/save?" + tabQuery(tab), {
      path: tab.path, base: tab.base, force: !!force,
      content: (tab.bom ? "﻿" : "") + (tab.eol === "\n" ? value : value.replace(/\n/g, tab.eol)),
    }).then(function (state) {
      tab.base = state.sha256;
      tab.saved = value;
      tab.changed = null;
      tab.bar.hidden = true;
      toast("Saved " + tab.name + ".");
      afterSave(tab);
    }).catch(function (error) {
      if (/changed since you opened it/.test(error.message)) changedBar(tab, true);
      else toast("Not saved: " + error.message);
    }).then(function () {
      tab.busy = false;
      edited(tab);
    });
  }
  // afterSave has the tree, its colours, and Source control see the save.
  function afterSave(tab) {
    if (tab.root !== spaceRoot()) { if (filesShown()) loadTree(); return; }
    git.at = 0;
    refreshGit();
    if (git.diff && git.diff.file.path === tab.path) openDiff(git.diff.file, git.diff.kind, git.diff.list, true);
  }
  // The file changed on disk: a tab with nothing to save follows it; one
  // with changes asks.
  function changedBar(tab, saving) {
    tab.bar.textContent = "";
    tab.bar.hidden = false;
    tab.bar.appendChild(el("span", { text: saving
      ? "Not saved. Claude or another person changed " + tab.name + " after you opened it."
      : "Claude or another person changed " + tab.name + " on disk. You also have changes here." }));
    tab.bar.appendChild(el("span", { class: "grow" }));
    tab.bar.appendChild(el("button", { class: "small", text: "Take theirs", title: "Show the file as it is now. Your changes here are lost.", onclick: function () {
      ask("Take the file as it is now? Your changes here are lost.").then(function (ok) { if (ok !== null) { tab.bar.hidden = true; loadTab(tab, true); } });
    } }));
    tab.bar.appendChild(el("button", { class: "small danger", text: "Save mine over it", title: "Save your version over the other change", onclick: function () { saveTab(tab, true); } }));
  }
  function checkTab(tab) {
    if (tab.kind !== "text" || tab.busy || !tab.loaded) return;
    tab.at = Date.now();
    api("GET", "files/stat?" + tabQuery(tab)).then(function (state) {
      if (state.sha256 === tab.base || tab.busy) return;
      if (!tab.dirty) loadTab(tab, true, true);
      else if (tab.changed !== state.sha256) { tab.changed = state.sha256; changedBar(tab, false); }
    }).catch(function () { /* gone, or the box is busy: the next check */ });
  }
  setInterval(function () {
    var tab = chat.view === "file" && !document.hidden && tabOf(opened.current);
    if (tab && Date.now() - tab.at > 3000) checkTab(tab);
  }, 1000);
  window.addEventListener("beforeunload", function (event) {
    // A link to an editor may count as leaving, in some browsers; it isn't.
    if (Date.now() - editor.leaving < 2000) return;
    if (opened.tabs.some(function (t) { return t.dirty; })) { event.preventDefault(); event.returnValue = ""; }
  });

  // toChat puts text in the chat's box, for you to add to and send: what
  // Logs and Checks hand Claude.
  function toChat(text) {
    if (!chat.space) { toast("Open the chat of a workspace first."); return; }
    setCodeView("chat");
    var input = $("chat-input");
    input.value = (input.value.trim() ? input.value.replace(/\s+$/, "") + "\n\n" : "") + text;
    input.dispatchEvent(new Event("input"));
    closeOver();
    input.focus();
    input.setSelectionRange(input.value.length, input.value.length);
  }
  function plainLog(text) {
    // Colours and cursor moves out; a line redrawn with \r keeps its last.
    return text.replace(/\x1b\[[0-9;?]*[ -\/]*[@-~]/g, "").replace(/\x1b[()][A-Z0-9]/g, "")
      .split("\n").map(function (line) { var parts = line.split("\r"); return parts[parts.length - 1] || parts[parts.length - 2] || ""; }).join("\n");
  }
  function seconds(from, to) {
    var s = Math.max(0, Math.round(((to ? new Date(to) : new Date()) - new Date(from)) / 1000));
    return s < 60 ? s + "s" : Math.floor(s / 60) + "m " + (s % 60) + "s";
  }

  // ---- Search: git grep in the workspace, its files tracked or new; a
  // match opens its file in a tab, at the line.
  var search = { q: "", matchCase: false, regex: false, busy: false, timer: 0, asked: "", space: "", view: null, shut: {} };
  function searchShown() { return viewShown("search"); }
  function runSearch(now) {
    clearTimeout(search.timer);
    var q = $("search-input").value;
    if (!q.trim() || (!now && q.trim().length < 2)) { search.view = null; search.asked = ""; renderSearch(); return; }
    var key = [chat.space, q, search.matchCase, search.regex].join("\n");
    if (key === search.asked && !now) return;
    search.asked = key;
    search.busy = true;
    $("search-note").textContent = "Searching…";
    api("GET", "search?workspace=" + encodeURIComponent(chat.space) + "&q=" + encodeURIComponent(q) + (search.matchCase ? "&case=1" : "") + (search.regex ? "&regex=1" : "")).then(function (view) {
      if (search.asked !== key) return;
      search.view = view;
      search.q = q;
      search.space = chat.space;
      search.shut = {};
      renderSearch();
    }).catch(function (error) {
      if (search.asked !== key) return;
      search.view = null;
      renderSearch(error.message);
    }).then(function () { search.busy = false; });
  }
  function renderSearch(error) {
    var box = $("search-results");
    box.textContent = "";
    var view = search.view;
    if (error) { $("search-note").textContent = error; return; }
    if (!view) { $("search-note").textContent = chat.space ? "Searches all files in the workspace, also new files. It skips the files that git ignores." : "Open the chat of a workspace first."; return; }
    var matches = view.matches || [];
    var byFile = [], files = {};
    matches.forEach(function (m) {
      if (!files[m.path]) { files[m.path] = []; byFile.push(m.path); }
      files[m.path].push(m);
    });
    $("search-note").textContent = matches.length
      ? count(matches.length, "match", "matches") + " in " + count(byFile.length, "file", "files") + (view.more ? ". This shows the first " + matches.length + ". Type more words to find fewer." : "")
      : "Nothing found.";
    var pattern = null;
    try {
      pattern = new RegExp(search.regex ? search.q : search.q.replace(/[.*+?^${}()|[\]\\]/g, "\\$&"), search.matchCase ? "g" : "gi");
    } catch (e) { pattern = null; }
    var root = spaceRoot();
    byFile.forEach(function (path) {
      var parts = path.split("/"), name = parts.pop(), shut = !!search.shut[path];
      box.appendChild(el("button", { class: "search-file" + (shut ? "" : " open"), title: path, onclick: function () { search.shut[path] = !shut; renderSearch(); } }, [
        el("span", { class: "file-chevron" }, [icon("chevron").firstChild]),
        icon("file"),
        el("span", { class: "search-name", text: name }),
        el("span", { class: "muted search-dir", text: parts.join("/") }),
        el("span", { class: "count", text: String(files[path].length) }),
      ]));
      if (shut) return;
      files[path].forEach(function (m) {
        box.appendChild(el("button", { class: "search-line", title: path + ":" + m.line, onclick: function () { openTab(root, path, m.line); } }, [
          el("span", { class: "search-num", text: String(m.line) }),
          el("span", { class: "search-text" }, marked(m.text.replace(/^\s+/, ""), pattern)),
        ]));
      });
    });
  }
  function marked(text, pattern) {
    if (!pattern) return [document.createTextNode(text)];
    var nodes = [], at = 0, m;
    pattern.lastIndex = 0;
    while ((m = pattern.exec(text)) && nodes.length < 40) {
      if (!m[0]) { pattern.lastIndex++; continue; }
      if (m.index > at) nodes.push(document.createTextNode(text.slice(at, m.index)));
      nodes.push(el("mark", { text: m[0] }));
      at = m.index + m[0].length;
    }
    nodes.push(document.createTextNode(text.slice(at)));
    return nodes;
  }
  $("search-input").addEventListener("input", function () { clearTimeout(search.timer); search.timer = setTimeout(function () { runSearch(false); }, 350); });
  $("search-form").addEventListener("submit", function (event) { event.preventDefault(); runSearch(true); });
  [["search-case", "matchCase"], ["search-regex", "regex"]].forEach(function (pair) {
    $(pair[0]).addEventListener("click", function () {
      search[pair[1]] = !search[pair[1]];
      $(pair[0]).setAttribute("aria-pressed", String(search[pair[1]]));
      runSearch(true);
    });
  });

  // ---- Checks: make check, test, lint (or the package's scripts), run in
  // the dev box on the workspace, their output as it comes.
  var checks = { list: null, run: null, offset: 0, out: "", busy: false, at: 0, space: "", error: "" };
  function checksShown() { return viewShown("checks"); }
  function loadChecks() {
    if (!chat.space || checks.busy) return;
    if (checks.space !== chat.space) { checks.space = chat.space; checks.list = null; checks.run = null; checks.offset = 0; checks.out = ""; renderChecks(); }
    var asked = chat.space;
    checks.busy = true;
    api("GET", "checks?workspace=" + encodeURIComponent(asked) + "&after=" + checks.offset).then(function (view) {
      if (asked !== chat.space) return;
      checks.list = view.checks || [];
      checks.error = "";
      takeRun(view.run);
    }).catch(function (error) {
      checks.error = error.message;
    }).then(function () {
      checks.busy = false;
      checks.at = Date.now();
      if (asked === chat.space) renderChecks();
    });
  }
  function takeRun(run) {
    if (!run) { checks.run = null; checks.out = ""; checks.offset = 0; return; }
    if (run.reset || !checks.run || checks.run.started !== run.started) checks.out = "";
    checks.out += run.output || "";
    if (checks.out.length > 400000) checks.out = checks.out.slice(-300000);
    checks.offset = run.offset;
    checks.run = run;
  }
  function renderChecks() {
    var list = $("checks-list"), run = checks.run;
    list.textContent = "";
    if (!chat.space) list.appendChild(el("p", { class: "muted small", text: "Open the chat of a workspace first." }));
    else if (checks.error) list.appendChild(el("p", { class: "error small", text: checks.error }));
    else if (!checks.list) list.appendChild(el("p", { class: "muted small", text: "Loading…" }));
    else if (!checks.list.length) list.appendChild(el("p", { class: "muted small", text: "The project has no make check, test, or lint. It also has no such scripts in package.json. Ask Claude to make a Makefile with them." }));
    (checks.list || []).forEach(function (check, i) {
      list.appendChild(el("div", { class: "check-row" }, [
        el("div", { class: "check-what" }, [el("code", { text: check.command }), check.about ? el("div", { class: "muted small", text: check.about }) : null]),
        el("button", { class: "small" + (i === 0 ? " primary" : ""), text: "Run", disabled: run && run.running ? "" : null, onclick: function () { startCheck(check.name); } }),
      ]));
    });
    var box = $("checks-run");
    box.hidden = !run;
    var badge = $("checks-badge");
    badge.hidden = !run || (!run.running && run.exit === 0 && !run.error);
    badge.classList.toggle("running", !!(run && run.running));
    badge.classList.toggle("failed", !!(run && !run.running && (run.exit !== 0 || run.error)));
    if (!run) return;
    var state = $("checks-state"), acts = $("checks-acts");
    acts.textContent = "";
    if (run.running) {
      state.className = "";
      state.textContent = "Running " + run.command + "… " + seconds(run.started);
      acts.appendChild(el("button", { class: "small danger", text: "Stop", onclick: function () { checkAct({ action: "stop" }); } }));
    } else if (run.stopped) {
      state.className = "muted";
      state.textContent = "Stopped " + run.command + " after " + seconds(run.started, run.finished);
    } else if (run.exit === 0 && !run.error) {
      state.className = "git-new";
      state.textContent = "✓ " + run.command + " passed, in " + seconds(run.started, run.finished);
    } else {
      state.className = "git-conflict";
      state.textContent = "✗ " + run.command + (run.error ? ": " + run.error : " failed (exit " + run.exit + ")") + ", in " + seconds(run.started, run.finished);
      acts.appendChild(el("button", { class: "small primary", text: "Fix with Claude", title: "Put the end of its output in the chat box, ready to send", onclick: function () {
        var tail = plainLog(checks.out).trim().split("\n").slice(-60).join("\n");
        toChat("`" + run.command + "` failed in the dev box (exit " + run.exit + "). The end of its output:\n\n```\n" + tail + "\n```\n\nFind out why and fix it, then run it again.");
      } }));
    }
    var out = $("checks-output");
    var atBottom = out.scrollHeight - out.scrollTop - out.clientHeight < 40;
    var text = plainLog(checks.out);
    if (out.textContent !== text) { out.textContent = text; if (atBottom || run.running) out.scrollTop = out.scrollHeight; }
  }
  function startCheck(name) { checkAct({ action: "run", name: name }); }
  function checkAct(body) {
    body.workspace = chat.space;
    api("POST", "checks", body).then(function (view) {
      if (body.action === "run") { checks.out = ""; checks.offset = 0; checks.run = null; }
      takeRun(view.run);
      renderChecks();
      checks.at = 0;
    }).catch(function (error) { toast(error.message); });
  }
  $("checks-refresh").addEventListener("click", loadChecks);

  // ---- Logs: what production's and Stage's boxes print, and the requests
  // their addresses serve, as they come.
  var logs = { slot: "production", source: "output", errors: false, cursor: "", key: "", lines: [], busy: false, at: 0, label: "", news: "" };
  try {
    var keptLogs = JSON.parse(localStorage.getItem("hi-dev-logs") || "{}");
    if (keptLogs.slot === "preview") logs.slot = "preview";
    if (keptLogs.source === "requests") logs.source = "requests";
    logs.errors = !!keptLogs.errors;
  } catch (e) { /* private window */ }
  function keepLogs() { try { localStorage.setItem("hi-dev-logs", JSON.stringify({ slot: logs.slot, source: logs.source, errors: logs.errors })); } catch (e) { /* private window */ } }
  function logsShown() { return viewShown("logs"); }
  function renderLogsBadge() {
    var dot = $("logs-badge");
    dot.hidden = !logs.news;
    dot.className = "side-badge dot pulse " + logs.news;
    dot.title = logs.news === "failed" ? "New errors in the logs" : logs.news ? "New lines in the logs" : "";
  }
  function loadLogs() {
    if (logs.busy) return;
    var key = logs.slot + "/" + logs.source;
    if (key !== logs.key) { logs.key = key; logs.cursor = ""; logs.lines = []; $("logs-lines").textContent = ""; }
    logs.busy = true;
    api("GET", "logs?slot=" + logs.slot + "&source=" + logs.source + "&after=" + encodeURIComponent(logs.cursor)).then(function (view) {
      if (key !== logs.key) return;
      var fresh = view.reset;
      if (fresh) logs.lines = [];
      logs.lines = logs.lines.concat(view.lines || []);
      var cut = logs.lines.length > 2000;
      if (cut) logs.lines = logs.lines.slice(-2000);
      logs.cursor = view.cursor || "";
      logs.label = view.label || "";
      $("logs-note").textContent = view.note || (logs.label ? logs.label + (logs.source === "output" ? ": its output" : ": the requests it got") : "");
      renderLogs(fresh || cut ? logs.lines : view.lines || [], fresh || cut);
      // Behind another view, new lines pulse a dot on Logs' icon: red
      // when one of them is an error.
      if (logsShown()) logs.news = "";
      else if (!fresh && (view.lines || []).length) {
        var bad = view.lines.some(function (line) { return line.error || line.status >= 500; });
        logs.news = bad || logs.news === "failed" ? "failed" : "running";
      }
      renderLogsBadge();
    }).catch(function (error) {
      $("logs-note").textContent = error.message;
    }).then(function () { logs.busy = false; logs.at = Date.now(); });
  }
  function logLine(line) {
    var at = line.at ? new Date(line.at) : null;
    var kind = line.error ? " err" : line.status >= 400 ? " warn" : "";
    return el("div", { class: "log-line" + kind }, [
      at ? el("span", { class: "log-at", text: at.toTimeString().slice(0, 8), title: at.toLocaleString() }) : null,
      // The relay pads its columns for a terminal; the panel is narrower.
      document.createTextNode(line.status ? line.text.replace(/ {2,}/g, "  ") : plainLog(line.text)),
    ]);
  }
  function renderLogs(lines, fresh) {
    var box = $("logs-lines");
    var atBottom = box.scrollHeight - box.scrollTop - box.clientHeight < 40;
    if (fresh) box.textContent = "";
    var frag = document.createDocumentFragment();
    lines.forEach(function (line) { frag.appendChild(logLine(line)); });
    box.appendChild(frag);
    while (box.childNodes.length > 2000) box.removeChild(box.firstChild);
    if (!box.childNodes.length) box.appendChild(el("p", { class: "muted small log-empty", text: "Nothing yet." }));
    else { var empty = box.querySelector(".log-empty"); if (empty) empty.remove(); }
    if (atBottom || fresh) box.scrollTop = box.scrollHeight;
  }
  function renderLogTools() {
    $("logs-slot").value = logs.slot;
    document.querySelectorAll("#logs-source button").forEach(function (b) { b.setAttribute("aria-pressed", String(b.dataset.source === logs.source)); });
    $("logs-errors").setAttribute("aria-pressed", String(logs.errors));
    $("logs-lines").classList.toggle("errors-only", logs.errors);
  }
  $("logs-slot").addEventListener("change", function () { logs.slot = $("logs-slot").value; keepLogs(); renderLogTools(); loadLogs(); });
  document.querySelectorAll("#logs-source button").forEach(function (b) {
    b.addEventListener("click", function () { logs.source = b.dataset.source; keepLogs(); renderLogTools(); loadLogs(); });
  });
  $("logs-errors").addEventListener("click", function () {
    logs.errors = !logs.errors;
    keepLogs();
    renderLogTools();
    var box = $("logs-lines");
    box.scrollTop = box.scrollHeight;
  });
  $("logs-send").addEventListener("click", function () {
    var box = $("logs-lines"), picked = window.getSelection ? String(window.getSelection()) : "";
    var inside = picked && window.getSelection().rangeCount && box.contains(window.getSelection().getRangeAt(0).commonAncestorContainer);
    var text = inside ? picked : logs.lines.filter(function (l) { return !logs.errors || l.error || l.status >= 400; }).slice(-40).map(function (l) {
      return (l.at ? new Date(l.at).toISOString().slice(11, 19) + " " : "") + plainLog(l.text);
    }).join("\n");
    if (!text.trim()) { toast("Nothing to send yet."); return; }
    var whose = logs.slot === "production" ? "production" : "Preview";
    toChat("From " + (logs.label || whose) + ", " + (logs.source === "output" ? "what it printed" : "the requests it served") + ":\n\n```\n" + text.replace(/\s+$/, "") + "\n```\n\n");
  });
  renderLogTools();

  // ---- Preview: Stage or production, in the panel, on its own address.
  // The preview first, each time the page opens; production after a
  // publish, to see what went out.
  var preview = { slot: "preview", url: "", label: "", blocked: "", stale: false };
  function previewShown() { return viewShown("preview"); }
  function showPreviewSlot(id) {
    preview.slot = id;
    if (previewShown()) loadPreview(true);
  }
  function previewSlots() {
    var s = (state && state.status) || {};
    var slots = [];
    if (s.preview_url) slots.push({ id: "preview", url: s.preview_url, label: "Preview: " + (s.preview_branch || (s.preview || "").replace(/^dev, /, "dev: ")) });
    if (s.production_url) slots.push({ id: "production", url: s.production_url, label: "Production" + (s.production ? " v" + s.production : "") });
    return slots;
  }
  function loadPreview(again) {
    var slots = previewSlots(), select = $("preview-slot");
    if (!slots.some(function (o) { return o.id === preview.slot; })) preview.slot = slots.length ? slots[0].id : "";
    var sig = slots.map(function (o) { return o.id + o.label; }).join("|");
    if (select.dataset.sig !== sig) {
      select.dataset.sig = sig;
      select.textContent = "";
      slots.forEach(function (o) { select.appendChild(el("option", { value: o.id, text: o.label })); });
    }
    select.value = preview.slot;
    select.hidden = !slots.length;
    renderPreviewAct();
    var slot = slots.filter(function (o) { return o.id === preview.slot; })[0];
    var frame = $("preview-frame");
    $("preview-open").hidden = !slot;
    frame.hidden = !slot;
    if (!slot) {
      showPreviewNote("none", "Nothing to show yet. Publish, or show the branch of a workspace on the preview.");
      return;
    }
    $("preview-open").href = slot.url;
    // A new release, or another branch on Stage, loads again.
    if (again || preview.url !== slot.url || preview.label !== slot.label) {
      preview.url = slot.url;
      preview.label = slot.label;
      preview.blocked = "";
      preview.stale = false;
      pickStop();
      frame.src = slot.url;
    }
    if (preview.stale) showPreviewNote("stale", staleWords);
    else if (preview.blocked) showBlocked(slot.url, preview.blocked);
    else showPreviewNote("", "");
  }
  // The frame sent on to an origin the page doesn't frame: the address's
  // own sign-in, most likely (an IT team's proxy in front of hi, such as
  // NetBird's), which keeps its session in a cookie of the address's own.
  // A frame can't sign in there, but the whole tab can: Sign in and come
  // back takes it through the app's addresses (/__hi/return/ on each), and
  // the frame has the cookie after. hi dev web tells the agent.
  var staleWords = "This address is newer than the dev page you have open. Reload the page to show it here.";
  var returnKey = "hi-dev-preview-return";
  function hostOf(url) { try { return new URL(url).host; } catch (e) { return url; } }
  // showPreviewNote draws the note when it changes, so a button in it
  // outlasts the page's refresh each second.
  function showPreviewNote(key, text, button) {
    var note = $("preview-note");
    if (note.dataset.key === key) return;
    note.dataset.key = key;
    note.textContent = text;
    if (button) { note.appendChild(document.createTextNode(" ")); note.appendChild(button); }
  }
  function showBlocked(from, to) {
    var back = 0;
    try { back = Number(sessionStorage.getItem(returnKey)) || 0; } catch (e) { /* private window */ }
    if (Date.now() - back < 3 * 60 * 1000) {
      // Back from signing in, and still sent on: the sign-in didn't take
      // (not finished, or the account isn't let in there), or the browser
      // doesn't give the frame the sign-in's cookie (a browser setting).
      showPreviewNote("still " + from + to, hostOf(from) + " still sends the frame to " + hostOf(to) + " after you signed in. Click ↗ to open it in a new tab. Sign in there, then click ↻ here. If you are signed in there but not here, this browser does not give the sign-in to the frame.");
      return;
    }
    var button = el("button", { type: "button", class: "small primary", text: "Sign in and come back", onclick: signInAtTop });
    showPreviewNote("blocked " + from + to, hostOf(from) + " sends the frame to " + hostOf(to) + ". This is a sign-in page in front of the app. You cannot sign in inside the dev page. Sign in and come back takes this tab there and back here. Then the Preview shows the app.", button);
  }
  // signInAtTop goes through each of the app's addresses at the top of the
  // tab, then back to this page.
  function signInAtTop() {
    var to = location.href.split("#")[0], seen = {};
    previewSlots().forEach(function (slot) {
      var address;
      try { address = new URL(slot.url); } catch (e) { return; }
      if (seen[address.origin]) return;
      seen[address.origin] = true;
      // The next address rides in the path: NetBird's sign-in sends the
      // browser back to the path it came for, without the query.
      address.pathname = "/__hi/return/" + btoa(to).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "") + "/";
      address.search = "";
      address.hash = "";
      to = address.toString();
    });
    try { sessionStorage.setItem(returnKey, String(Date.now())); } catch (e) { /* private window */ }
    location.assign(to);
  }
  document.addEventListener("securitypolicyviolation", function (event) {
    if (!/^(frame-src|child-src)/.test(event.violatedDirective) || !preview.url || !event.blockedURI) return;
    var to;
    try { to = new URL(event.blockedURI).origin; } catch (e) { return; }
    if (to === "null" || preview.blocked === to) return;
    if (to === new URL(preview.url).origin) {
      // The page came before the address: what it may frame is from then.
      preview.stale = true;
      showPreviewNote("stale", staleWords);
      return;
    }
    preview.blocked = to;
    showBlocked(preview.url, to);
    api("POST", "preview/blocked", { slot: preview.slot, to: to }).catch(function () {});
  });
  $("preview-slot").addEventListener("change", function () {
    preview.slot = $("preview-slot").value;
    loadPreview(true);
  });
  $("preview-reload").addEventListener("click", function () { loadPreview(true); });
  $("preview-wide").addEventListener("click", widenSide);

  setInterval(function () {
    if (document.hidden) return;
    var now = Date.now();
    if (checksShown() ? now - checks.at > 1500 : checks.run && checks.run.running && now - checks.at > 5000) loadChecks();
    if (logsShown() ? now - logs.at > 3000 : now - logs.at > 15000) loadLogs();
    if (previewShown()) loadPreview(false);
  }, 1000);

  // Upload: picked, or dropped on the files; a file that's there
  // already is replaced only when you say so.
  function upload(list, overwrite) {
    if (!list.length) return Promise.resolve();
    var form = new FormData();
    list.forEach(function (file) { form.append("file", file, file.name); });
    toast("Uploading " + (list.length === 1 ? list[0].name : list.length + " files") + "…", true);
    return fetch("api/files/upload?" + fileQuery(files.dir, overwrite ? "&overwrite=1" : ""), { method: "POST", body: form, credentials: "same-origin" }).then(function (response) {
      return response.json().catch(function () { return {}; }).then(function (data) {
        var written = data.written || [];
        if (response.status === 409) {
          var rest = list.filter(function (file) { return written.indexOf(file.name) < 0; });
          return ask(data.exists + " is already there. Replace it and all other files here with the same name?").then(function (answer) {
            if (answer === null) { toast(written.length ? "Uploaded " + written.join(", ") + ". " + data.exists + " did not change." : data.exists + " did not change."); return; }
            return upload(rest, true);
          });
        }
        if (!response.ok) throw new Error(data.error || response.statusText);
        toast("Uploaded " + written.join(", ") + " into /" + files.dir + ".");
      });
    }).catch(function (error) { toast(error.message); }).then(function () { if (files.dir) files.open[files.dir] = true; keepFiles(); loadTree(); });
  }
  $("files-upload").addEventListener("change", function () {
    var list = [].slice.call(this.files);
    this.value = "";
    upload(list, false);
  });
  (function () {
    var box = $("files-box"), depth = 0;
    box.addEventListener("dragenter", function (event) {
      if (![].slice.call(event.dataTransfer.types || []).some(function (t) { return t === "Files"; })) return;
      event.preventDefault();
      depth++;
      $("files-drop").hidden = false;
    });
    box.addEventListener("dragover", function (event) { if (!$("files-drop").hidden) event.preventDefault(); });
    box.addEventListener("dragleave", function () { if (--depth <= 0) { depth = 0; $("files-drop").hidden = true; } });
    box.addEventListener("drop", function (event) {
      event.preventDefault();
      depth = 0;
      $("files-drop").hidden = true;
      upload([].slice.call(event.dataTransfer.files || []), false);
    });
  })();

  // ---- files for a ticket, a note or a chat message: picked, dropped, or
  // pictures pasted into its text
  function reference(name) {
    return (/\.(png|jpe?g|gif|webp)$/i.test(name) ? "[image: " : "[file: ") + name + "]";
  }
  // chips draws what's attached as small previews (the chat's): a picture's
  // thumbnail, and a long pasted text folded into a line until clicked.
  // changed is told of every change, to keep a draft.
  function attacher(input, text, shown, chips, changed) {
    var files = [];
    var pastes = []; // long pasted text: { n, text }, sent in its place
    var pasted = 0;
    function pasteRef(chunk) { return "[pasted text #" + chunk.n + " +" + chunk.text.split("\n").length + " lines]"; }
    function leaveOut(ref) { text.value = text.value.split(" " + ref).join("").split(ref).join(""); }
    function render() {
      shown.textContent = "";
      if (chips) shown.hidden = !files.length && !pastes.length;
      files.forEach(function (file, i) {
        var size = Math.max(1, Math.round(file.size / 1024)) + " KB";
        var out = el("button", { type: "button", class: "link", text: "✕", title: "Leave it out", onclick: function () {
          files.splice(i, 1);
          if (file.thumb) URL.revokeObjectURL(file.thumb);
          leaveOut(reference(file.name));
          render();
        } });
        if (!chips) { shown.appendChild(el("li", {}, [document.createTextNode(file.name + " (" + size + ")"), out])); return; }
        if (!file.thumb && /^image\//.test(file.type)) file.thumb = URL.createObjectURL(file);
        shown.appendChild(el("div", { class: "chip", title: file.name + " (" + size + ")" }, [
          file.thumb ? el("img", { src: file.thumb, alt: file.name }) : el("span", { class: "chip-text", text: file.name }), out]));
      });
      pastes.forEach(function (chunk, i) {
        var lines = chunk.text.split("\n");
        var chip = el("div", { class: "chip paste" });
        var full = el("pre", { text: chunk.text, hidden: "" });
        chip.appendChild(el("button", { type: "button", class: "chip-open", title: "Show it all", onclick: function () {
          full.hidden = !full.hidden;
          chip.classList.toggle("open", !full.hidden);
        } }, [el("span", { class: "chip-text", text: lines[0].trim() || "Pasted text" }), el("span", { class: "muted", text: lines.length + " lines" })]));
        chip.appendChild(el("button", { type: "button", class: "link", text: "✕", title: "Leave it out", onclick: function () {
          pastes.splice(i, 1);
          leaveOut(pasteRef(chunk));
          render();
        } }));
        chip.appendChild(full);
        shown.appendChild(chip);
      });
      if (changed) changed();
    }
    function add(list) {
      files = files.concat([].slice.call(list || []));
      render();
    }
    input.addEventListener("change", function () {
      add(input.files);
      input.value = "";
    });
    // What's pasted goes in where the cursor is, a space on each side.
    function insert(refs) {
      var at = text.selectionStart, end = text.selectionEnd;
      var before = text.value.slice(0, at), after = text.value.slice(end);
      var put = (before && !/\s$/.test(before) ? " " : "") + refs.join(" ") + (after && !/^\s/.test(after) ? " " : "");
      text.value = before + put + after;
      text.selectionStart = text.selectionEnd = before.length + put.length;
      text.dispatchEvent(new Event("input"));
    }
    // A pasted picture gets a name of its own and a reference where it was
    // pasted, [image: image-3fa9c1.png]: the name the ticket's files have,
    // so whoever reads the text (an agent too) knows which picture is meant.
    text.addEventListener("paste", function (event) {
      var clip = event.clipboardData || {};
      var pictures = [].slice.call(clip.files || []);
      if (!pictures.length) {
        // In the chat, a long text is a chip, [pasted text #1 +40 lines]
        // in the box, and goes whole when sent; one too long for a message
        // goes as a file.
        var data = chips && clip.getData ? clip.getData("text/plain") : "";
        if (data.split("\n").length <= 12 && data.length <= 2000) return;
        event.preventDefault();
        if (data.length > 48 * 1024) {
          var file = new File([data], "pasted-" + Math.random().toString(16).slice(2, 8) + ".txt", { type: "text/plain" });
          files.push(file);
          insert([reference(file.name)]);
        } else {
          var chunk = { n: ++pasted, text: data.replace(/\r\n/g, "\n") };
          pastes.push(chunk);
          insert([pasteRef(chunk)]);
        }
        render();
        return;
      }
      event.preventDefault();
      insert(pictures.map(function (file) {
        var ext = (file.name.match(/\.[a-z0-9]+$/i) || [file.type === "image/jpeg" ? ".jpg" : ".png"])[0];
        var name = (/^image\.[a-z]+$/i.test(file.name) ? "image" : file.name.replace(/\.[a-z0-9]+$/i, "")) + "-" + Math.random().toString(16).slice(2, 8) + ext;
        files.push(new File([file], name, { type: file.type }));
        return reference(name);
      }));
      render();
    });
    // A pasted text whose reference was deleted from the box is left out.
    text.addEventListener("input", function () {
      var kept = pastes.filter(function (chunk) { return text.value.indexOf(pasteRef(chunk)) >= 0; });
      if (kept.length !== pastes.length) { pastes = kept; render(); }
    });
    return {
      read: function () {
        return Promise.all(files.map(function (file) {
          return new Promise(function (resolve, reject) {
            var reader = new FileReader();
            reader.onload = function () { resolve({ name: file.name, data: String(reader.result).split(",")[1] || "" }); };
            reader.onerror = function () { reject(new Error("cannot read " + file.name)); };
            reader.readAsDataURL(file);
          });
        }));
      },
      // expand puts each pasted text where its reference is.
      expand: function (value) {
        pastes.forEach(function (chunk) { value = value.split(pasteRef(chunk)).join(chunk.text); });
        return value;
      },
      count: function () { return files.length; },
      add: add,
      // A draft: what's attached, and the pasted texts.
      snapshot: function () { return { files: files.slice(), pastes: pastes.slice(), pasted: pasted }; },
      restore: function (draft) {
        files = draft.files || [];
        pastes = draft.pastes || [];
        pasted = draft.pasted || 0;
        render();
      },
      clear: function () {
        files.forEach(function (file) { if (file.thumb) URL.revokeObjectURL(file.thumb); });
        files = [];
        pastes = [];
        render();
      },
    };
  }

  // ---- a new ticket
  var newFiles = pictureBox($("ticket-body"), $("ticket-attached"), $("ticket-files"));
  $("stask-new").addEventListener("click", function () {
    $("ticket-error").textContent = "";
    $("ticket-go").disabled = false;
    $("ticket-dialog").showModal();
    $("ticket-title").focus();
  });
  // Cancel isn't a submit button, so Enter in the title files the ticket.
  $("ticket-cancel").addEventListener("click", function () { $("ticket-dialog").close(); });
  $("ticket-form").addEventListener("submit", function (event) {
    event.preventDefault();
    $("ticket-go").disabled = true;
    $("ticket-error").textContent = "";
    newFiles.read().then(function (files) {
      return api("POST", "ticket", { title: $("ticket-title").value, body: newFiles.value(), files: files });
    }).then(function (ticket) {
      $("ticket-dialog").close();
      $("ticket-title").value = "";
      newFiles.clear();
      toast("Made task #" + ticket.number + ".");
      refreshTickets(true);
    }).catch(function (error) {
      $("ticket-error").textContent = error.message;
      $("ticket-go").disabled = false;
    });
  });

  // ---- a ticket, opened: its text, files, and comments, and a note to add
  var viewing = 0;
  var noteFiles = pictureBox($("note-body"), $("note-attached"), $("note-files"));
  // linked puts text on the page with its addresses as links.
  function linked(text) {
    var box = el("div", { class: "text" });
    String(text || "").split(/(https?:\/\/[^\s<>"')\]]+)/).forEach(function (part, i) {
      box.appendChild(i % 2 ? el("a", { href: part, target: "_blank", rel: "noopener", text: part }) : document.createTextNode(part));
    });
    return box;
  }
  function when(at) {
    return at ? new Date(at).toLocaleString([], { dateStyle: "medium", timeStyle: "short" }) : "";
  }
  function fileURL(number, name) {
    return "api/ticket/file?number=" + number + "&name=" + encodeURIComponent(name);
  }
  function renderTicket(ticket) {
    $("view-title").textContent = "#" + ticket.number + " " + ticket.title;
    var meta = $("view-meta");
    meta.textContent = [ticket.state, ticket.asked && ticket.asked !== ticket.author ? "asked by " + ticket.asked + (ticket.author ? " (made as " + ticket.author + ")" : "") : ticket.author, when(ticket.created), ticket.edited ? "edited " + when(ticket.edited) : ""].filter(Boolean).join(" · ");
    if (ticket.url) meta.appendChild(el("span", {}, [document.createTextNode(" · "), el("a", { href: ticket.url, target: "_blank", rel: "noopener", text: "Open the original" })]));
    var body = $("view-body");
    body.textContent = "";
    var text = ticket.body ? renderMarkdown(ticket.body, "", true) : el("p", { class: "muted", text: "No description." });
    text.classList.add("view-text");
    body.appendChild(el("div", { class: "comment-head body-head" }, [el("span"), menu([
      ["Copy text", function () { copyText(ticket.body || ""); }],
      ["Quote in a note", function () { quote(ticket.body || ""); }],
      ticket.state === "closed" ? ["Reopen", function () { setTaskState(ticket, false); }] : ["Close the task", function () { taskDone(ticket, ticket.state === "closed" ? null : spaceFor(ticket)); }],
      ["Edit", function () {
        var title = el("input", { class: "edit-title", "aria-label": "Title", autocomplete: "off" });
        title.value = ticket.title;
        editInPlace(text, ticket.body || "", [title], function (value) {
          return { number: ticket.number, title: title.value, body: value };
        }, ticket.number);
        title.focus();
      }],
    ])]));
    body.appendChild(text);
    var files = $("view-files");
    files.textContent = "";
    $("view-files-box").hidden = !(ticket.files && ticket.files.length);
    (ticket.files || []).forEach(function (name) {
      var url = fileURL(ticket.number, name);
      var picture = /\.(png|jpe?g|gif|webp)$/i.test(name);
      files.appendChild(el("li", {}, [
        picture ? el("a", { href: url, target: "_blank", rel: "noopener" }, [el("img", { src: url, alt: name, loading: "lazy" })]) : null,
        el("div", { class: "file-name" }, [el("a", { href: url, target: "_blank", rel: "noopener", text: name }),
        el("button", { type: "button", class: "link remove", text: "✕", title: "Remove " + name + " from the task", "aria-label": "Remove " + name, onclick: function () {
          removeFromTicket(ticket.number, { name: name }, "Remove " + name + " from task #" + ticket.number + "? You cannot get it back.", "Removed " + name + ".");
        } })]),
      ]));
    });
    var comments = $("view-comments");
    comments.textContent = "";
    (ticket.comments || []).forEach(function (comment) {
      var items = [
        ["Copy text", function () { copyText(comment.body); }],
        ["Quote in a note", function () { quote(comment.body); }],
      ];
      var said = renderMarkdown(comment.body, "", true);
      if (comment.id) items.push(["Edit", function () {
        editInPlace(said, comment.body, [], function (value) { return { number: ticket.number, comment: comment.id, body: value }; }, ticket.number);
      }]);
      if (comment.id) items.push(["Delete", function () {
        var files = attachedNames(comment.body);
        removeFromTicket(ticket.number, { comment: comment.id }, "Delete this comment from task #" + ticket.number + (files.length ? ", with " + files.join(", ") : "") + "? You cannot get it back.", "Deleted the comment.");
      }, "danger"]);
      comments.appendChild(el("li", {}, [
        el("div", { class: "comment-head" }, [
          el("span", { class: "muted small", text: [comment.author, when(comment.at), comment.edited ? "edited" : ""].filter(Boolean).join(" · ") }),
          menu(items),
        ]),
        said,
      ]));
    });
    if (!(ticket.comments || []).length) comments.appendChild(el("li", { class: "muted", text: "No comments yet." }));
  }
  // menu is a ⋯ button with a list of actions, as on GitHub.
  function menu(items) {
    var list = el("div", { class: "menu", role: "menu", hidden: "" }, items.map(function (item) {
      return el("button", { type: "button", role: "menuitem", class: item[2] || "", text: item[0], onclick: function () { list.hidden = true; item[1](); } });
    }));
    var button = el("button", { type: "button", class: "link more", text: "⋯", title: "More", "aria-label": "More", "aria-haspopup": "menu", onclick: function (event) {
      event.stopPropagation();
      var open = list.hidden;
      document.querySelectorAll(".menu").forEach(function (other) { other.hidden = true; });
      list.hidden = !open;
      // Near the bottom of the window, it opens upwards.
      list.classList.toggle("up", button.getBoundingClientRect().bottom + list.offsetHeight + 8 > window.innerHeight);
    } });
    return el("span", { class: "menu-box" }, [button, list]);
  }
  document.addEventListener("click", function () {
    document.querySelectorAll(".menu").forEach(function (list) { list.hidden = true; });
  });
  function quote(text) {
    noteFiles.append(text.split("\n").map(function (line) { return "> " + line; }).join("\n") + "\n\n");
  }
  // editInPlace puts a text box (and fields before it, such as the title)
  // where the text was, with Save and Cancel; Save sends what request makes
  // of the new text to api/ticket/edit.
  function editInPlace(shown, value, fields, request, number) {
    var area = el("textarea", { class: "edit-text", rows: Math.min(16, Math.max(4, value.split("\n").length + 1)), "aria-label": "Text" });
    area.value = value;
    var error = el("p", { class: "error" });
    var save = el("button", { type: "button", class: "primary", text: "Save" });
    var cancel = el("button", { type: "button", text: "Cancel" });
    var box = el("div", { class: "editing" }, fields.concat([area, error, el("div", { class: "buttons" }, [cancel, save])]));
    shown.replaceWith(box);
    if (!fields.length) area.focus();
    cancel.addEventListener("click", function () { box.replaceWith(shown); });
    save.addEventListener("click", function () {
      save.disabled = true;
      api("POST", "ticket/edit", request(area.value)).then(function () {
        toast("Saved.");
        refreshTickets(true);
        return openTicket(number);
      }).catch(function (e) { error.textContent = e.message; save.disabled = false; });
    });
  }
  function copyText(text) {
    (navigator.clipboard && window.isSecureContext ? navigator.clipboard.writeText(text) : Promise.reject()).then(function () {
      toast("Copied.");
    }).catch(function () {
      // Plain HTTP has no clipboard API: the old way.
      var area = el("textarea", { style: "position:fixed;opacity:0" });
      area.value = text;
      $("view-dialog").appendChild(area);
      area.select();
      var ok = document.execCommand("copy");
      area.remove();
      toast(ok ? "Copied." : "Couldn't copy here.");
    });
  }
  // The files a comment attached, from hi's line: "Attached: a.png (12 KB), b.pdf (3 MB)."
  function attachedNames(body) {
    var match = /^Attached: (.+)\. hi keeps them/m.exec(body || "");
    return match ? match[1].split(/ \([^()]*\)(?:, |$)/).filter(Boolean) : [];
  }
  function removeFromTicket(number, what, question, done) {
    ask(question).then(function (answer) {
      if (answer === null) return;
      api("POST", "ticket/delete", Object.assign({ number: number }, what)).then(function () {
        toast(done);
        openTicket(number);
      }).catch(function (error) { toast(error.message); });
    });
  }
  function openTicket(number) {
    viewing = number;
    $("note-error").textContent = "";
    $("view-title").textContent = "#" + number;
    $("view-meta").textContent = "";
    $("view-body").textContent = "Loading the task…";
    $("view-files-box").hidden = true;
    $("view-comments").textContent = "";
    $("view-dialog").showModal();
    return api("GET", "ticket?number=" + number).then(function (ticket) {
      if (viewing === number) renderTicket(ticket);
    }).catch(function (error) { $("view-body").textContent = error.message; });
  }
  $("view-close").addEventListener("click", function () { $("view-dialog").close(); });
  $("note-form").addEventListener("submit", function (event) {
    event.preventDefault();
    var number = viewing;
    $("note-go").disabled = true;
    $("note-error").textContent = "";
    noteFiles.read().then(function (files) {
      return api("POST", "ticket/note", { number: number, body: noteFiles.value(), files: files });
    }).then(function () {
      noteFiles.clear();
      toast("Added to #" + number + ".");
      return openTicket(number);
    }).catch(function (error) {
      $("note-error").textContent = error.message;
    }).then(function () { $("note-go").disabled = false; });
  });

  // ---- decisions: docs/decisions/ on the app's branch, as cards; one
  // opens to read all of it, and to edit or delete it, each a commit
  var decisions = { list: null, open: null };
  function refreshDecisions() {
    return api("GET", "decisions").then(function (list) {
      decisions.list = list;
      renderDecisions();
    }).catch(function (error) {
      $("decisions").textContent = "";
      $("decisions").appendChild(el("p", { class: "error", text: error.message }));
    });
  }
  function day(at) {
    return at ? new Date(at).toLocaleDateString([], { dateStyle: "medium" }) : "";
  }
  function renderDecisions() {
    var box = $("decisions");
    box.textContent = "";
    var words = $("decisions-search").value.trim().toLowerCase().split(/\s+/).filter(Boolean);
    var shown = (decisions.list || []).filter(function (decision) {
      var text = (decision.file + " " + decision.text).toLowerCase();
      return words.every(function (word) { return text.indexOf(word) >= 0; });
    });
    if (!shown.length) {
      box.appendChild(el("p", { class: "muted", text: (decisions.list || []).length ? "No decision has those words." :
        "No decisions yet. The agents write a decision for a technical choice that nobody must undo by accident. You can write one too." }));
    }
    shown.forEach(function (decision) {
      box.appendChild(el("button", { type: "button", class: "decision-card", onclick: function () { openDecision(decision, false); } }, [
        el("span", { class: "decision-top" }, [
          decision.number ? el("span", { class: "id-pill", text: decision.number }) : null,
          el("span", { class: "muted small", text: day(decision.added) }),
        ]),
        el("span", { class: "decision-card-title", text: decision.title }),
        decision.summary ? el("span", { class: "decision-summary", text: decision.summary }) : null,
      ]));
    });
  }
  $("decisions-search").addEventListener("input", renderDecisions);
  // openDecision shows one, or with edit its Markdown to change; a new
  // one (null) opens to write.
  function openDecision(decision, edit) {
    decisions.open = decision;
    var editing = edit || !decision;
    $("decision-title").textContent = decision ? decision.title : "New decision";
    $("decision-meta").textContent = decision ? [decision.file, decision.added ? "written " + day(decision.added) : "",
      day(decision.changed) !== day(decision.added) ? "changed " + day(decision.changed) : ""].filter(Boolean).join(" · ") :
      "Start with the title as a heading. Then write the decision. The card shows that paragraph.";
    var body = $("decision-body");
    body.textContent = "";
    if (decision && !editing) {
      // Its title is the dialog's already.
      var shown = renderMarkdown(decision.text, "", false);
      var first = shown.firstElementChild;
      if (first && first.tagName === "H1") first.remove();
      body.appendChild(shown);
    }
    body.hidden = editing;
    $("decision-text").hidden = !editing;
    $("decision-text").value = decision ? decision.text : "";
    $("decision-error").textContent = "";
    $("decision-delete").hidden = !decision;
    $("decision-edit").hidden = editing;
    $("decision-save").hidden = !editing;
    $("decision-save").disabled = false;
    $("decision-close").textContent = editing && decision ? "Cancel" : "Close";
    if (!$("decision-dialog").open) $("decision-dialog").showModal();
    if (editing) $("decision-text").focus();
  }
  $("decision-new").addEventListener("click", function () { openDecision(null, true); });
  // A click outside it closes it, unless that would lose an edit.
  $("decision-dialog").addEventListener("click", function (event) {
    var dialog = $("decision-dialog");
    var box = dialog.getBoundingClientRect();
    var outside = event.clientX < box.left || event.clientX > box.right || event.clientY < box.top || event.clientY > box.bottom;
    var unsaved = !$("decision-text").hidden && $("decision-text").value !== (decisions.open ? decisions.open.text : "");
    if (event.target === dialog && outside && !unsaved) dialog.close();
  });
  $("decision-edit").addEventListener("click", function () { openDecision(decisions.open, true); });
  // Close, or while editing one, Cancel: back to reading it.
  $("decision-close").addEventListener("click", function () {
    if (decisions.open && !$("decision-text").hidden) openDecision(decisions.open, false);
    else $("decision-dialog").close();
  });
  $("decision-save").addEventListener("click", function () {
    var decision = decisions.open;
    $("decision-save").disabled = true;
    $("decision-error").textContent = "";
    api("POST", "decision", { file: decision ? decision.file : "", blob: decision ? decision.blob : "", text: $("decision-text").value }).then(function (result) {
      toast(decision ? "Saved on the branch of the app." : "Added " + result.file + " to the branch of the app.");
      return refreshDecisions().then(function () {
        var saved = (decisions.list || []).filter(function (each) { return each.file === result.file; })[0];
        if (saved) openDecision(saved, false);
        else $("decision-dialog").close();
      });
    }).catch(function (error) {
      $("decision-error").textContent = error.message;
      $("decision-save").disabled = false;
    });
  });
  $("decision-delete").addEventListener("click", function () {
    var decision = decisions.open;
    ask("Delete the decision \u201c" + decision.title + "\u201d? hi removes it from the branch of the app. Git keeps its history.").then(function (answer) {
      if (answer === null) return;
      api("POST", "decision", { file: decision.file, blob: decision.blob, delete: true }).then(function () {
        toast("Deleted " + decision.file + ".");
        $("decision-dialog").close();
        refreshDecisions();
      }).catch(function (error) { $("decision-error").textContent = error.message; });
    });
  });

  // ---- Tasks in the panel: the chat's workspace's task, to read, add a
  // note to, and close; and the open tasks, short, to open or start, or
  // the closed ones, to read or reopen.
  var stask = { mode: "auto", number: 0, ticket: null, loading: 0, space: "", error: "" };
  var staskFiles = pictureBox($("stask-note"), $("stask-attached"), $("stask-files"));
  function ticketOf(space) {
    var match = space && /^(\d+)(-|$)/.exec(space.branch || "");
    return match ? Number(match[1]) : 0;
  }
  function openTickets() {
    return ((tickets && tickets.tickets) || []).filter(function (ticket) { return ticket.state !== "closed"; });
  }
  function renderTaskBadge() {
    var n = tickets ? openTickets().length : 0;
    badge("stasks-count", n, n === 1 ? " open task" : " open tasks");
  }
  // badge puts a number on a panel's icon, or takes it off at none.
  function badge(id, n, what) {
    var mark = $(id);
    mark.textContent = n > 99 ? "99+" : String(n || "");
    mark.hidden = !n;
    mark.title = n ? n + what : "";
  }
  // renderSideTasks shows the list, or one task: the workspace's, unless
  // you went back to the list or opened another.
  function renderSideTasks() {
    renderTaskBadge();
    if (stask.space !== chat.space) { stask.space = chat.space; stask.mode = "auto"; }
    var mine = ticketOf(chatSpace());
    var number = stask.mode === "one" ? stask.number : stask.mode === "auto" ? mine : 0;
    $("stask-list-box").hidden = !!number;
    $("stask-one").hidden = !number;
    if (number) return showSideTask(number, false);
    renderSideList(mine);
  }
  function renderSideList(mine) {
    var box = $("stask-mine");
    box.textContent = "";
    var space = chatSpace();
    if (space && !mine && space.id) box.appendChild(el("div", {}, [
      el("div", { text: space.label || space.branch || "This workspace" }),
      el("div", { class: "muted", text: "This work has no task. Someone started it without a task." })]));
    var list = $("stask-list");
    list.textContent = "";
    if (!tickets) { list.appendChild(el("li", { class: "empty", text: "Loading the tasks…" })); return; }
    var words = $("stask-search").value.trim().toLowerCase().split(/\s+/).filter(Boolean);
    var closed = showing === "closed";
    var pool = closed ? (tickets.tickets || []).filter(function (ticket) { return ticket.state === "closed"; }) : openTickets();
    var shown = pool.filter(function (ticket) {
      var hay = ("#" + ticket.number + " " + ticket.title + " " + (ticket.author || "") + " " + (ticket.asked || "") + " " + (ticket.labels || []).join(" ")).toLowerCase();
      return words.every(function (word) { return hay.indexOf(word) >= 0; });
    });
    if (!shown.length) list.appendChild(el("li", { class: "empty", text: words.length ? "No task matches." : closed ? "No closed tasks." : "No open tasks. Click + to make one. The lead makes tasks from requests in the channel." }));
    shown.forEach(function (ticket) {
      var space = closed ? null : spaceFor(ticket);
      var who = ticket.asked || ticket.author || "";
      list.appendChild(el("li", { class: ticket.number === mine ? "mine" : "" }, [
        space ? statusDot(space.status, space) : null,
        el("button", { type: "button", class: "stask-row", title: "#" + ticket.number + " " + ticket.title + (who ? " · " + who : "") + (ticket.updated ? " · " + ago(ticket.updated) : ""), onclick: function () { stask.mode = "one"; stask.number = ticket.number; renderSideTasks(); } }, [
          el("span", { class: "stask-num", text: "#" + ticket.number }),
          el("span", { class: "stask-title", text: ticket.title })]),
        closed ? null : space
          ? el("button", { type: "button", class: "stask-go", text: "Open", title: "Show its workspace", onclick: function () { focus(space); } })
          : el("button", { type: "button", class: "stask-go", text: "Start", title: "Make a branch and a worktree, and start an agent that reads the task", onclick: function () { startDialog(ticket); } }),
      ]));
    });
  }
  // showSideTask fetches a task when it isn't the one shown, or with fresh.
  function showSideTask(number, fresh) {
    if (!fresh && stask.ticket && stask.ticket.number === number) { renderSideTask(stask.ticket); return Promise.resolve(); }
    if (!stask.ticket || stask.ticket.number !== number) {
      stask.ticket = null;
      $("stask-id").textContent = "#" + number;
      $("stask-acts").textContent = "";
      $("stask-body").textContent = "";
      $("stask-body").appendChild(el("p", { class: "muted small", text: "Loading the task…" }));
    }
    var asked = ++stask.loading;
    return api("GET", "ticket?number=" + number).then(function (ticket) {
      if (asked !== stask.loading) return;
      stask.ticket = ticket;
      renderSideTask(ticket);
    }).catch(function (error) {
      if (asked !== stask.loading) return;
      $("stask-body").textContent = "";
      $("stask-body").appendChild(el("p", { class: "error small", text: error.message }));
    });
  }
  function renderSideTask(ticket) {
    var closed = ticket.state === "closed";
    var space = closed ? null : spaceFor(ticket);
    $("stask-id").textContent = "#" + ticket.number + (closed ? " · closed" : "");
    var acts = $("stask-acts");
    acts.textContent = "";
    if (!closed) {
      // Open its workspace, unless the chat is in it already; or Start it.
      if (space && space.id !== chat.space) acts.appendChild(el("button", { type: "button", text: "Open", title: "Show its workspace", onclick: function () { focus(space); } }));
      if (!space) acts.appendChild(el("button", { type: "button", text: "Start", title: "Make a branch and a worktree, and start an agent that reads the task", onclick: function () { startDialog(ticket); } }));
      acts.appendChild(el("button", { type: "button", class: "done", text: "Done ✓", busy: space ? "finish:" + space.id : "", title: space ? "Finish its work and close the task" : "Close the task", onclick: function () { taskDone(ticket, space); } }));
    }
    var items = [["View in full", function () { openTicket(ticket.number); }],
      ["Copy text", function () { copyText(ticket.body || ""); }]];
    if (ticket.url) items.push(["Open the original", function () { window.open(ticket.url, "_blank", "noopener"); }]);
    if (closed) items.push(["Reopen", function () { setTaskState(ticket, false); }]);
    acts.appendChild(menu(items));
    var body = $("stask-body");
    body.textContent = "";
    body.appendChild(el("p", { class: "stask-title-full", text: ticket.title }));
    body.appendChild(el("p", { class: "muted small", text: [ticket.asked || ticket.author, when(ticket.created)].filter(Boolean).join(" · ") }));
    var text = ticket.body ? renderMarkdown(ticket.body, "", true) : el("p", { class: "muted small", text: "No description." });
    body.appendChild(text);
    if ((ticket.files || []).length) {
      body.appendChild(el("h3", { text: "Files" }));
      body.appendChild(el("ul", { class: "stask-pics" }, ticket.files.map(function (name) {
        var url = fileURL(ticket.number, name);
        return el("li", {}, [/\.(png|jpe?g|gif|webp)$/i.test(name)
          ? el("a", { href: url, target: "_blank", rel: "noopener", title: name }, [el("img", { src: url, alt: name, loading: "lazy" })])
          : el("a", { href: url, target: "_blank", rel: "noopener", text: name })]);
      })));
    }
    var comments = ticket.comments || [];
    body.appendChild(el("h3", { text: comments.length ? "Comments (" + comments.length + ")" : "Comments" }));
    body.appendChild(comments.length ? el("ul", { class: "stask-comments" }, comments.map(function (comment) {
      return el("li", {}, [el("div", { class: "muted small", text: [comment.author, when(comment.at)].filter(Boolean).join(" · ") }), renderMarkdown(comment.body, "", true)]);
    })) : el("p", { class: "muted small", text: "None yet." }));
  }
  $("stask-back").addEventListener("click", function () { stask.mode = "list"; renderSideTasks(); });
  $("stask-search").addEventListener("input", function () { renderSideList(ticketOf(chatSpace())); });
  $("stask-refresh").addEventListener("click", function () { refreshTickets(true); });
  $("stask-note-form").addEventListener("submit", function (event) {
    event.preventDefault();
    var ticket = stask.ticket;
    if (!ticket) return;
    $("stask-note-go").disabled = true;
    $("stask-error").textContent = "";
    staskFiles.read().then(function (files) {
      return api("POST", "ticket/note", { number: ticket.number, body: staskFiles.value(), files: files });
    }).then(function () {
      staskFiles.clear();
      toast("Added to #" + ticket.number + ".");
      refreshTickets(true);
      return showSideTask(ticket.number, true);
    }).catch(function (error) {
      $("stask-error").textContent = error.message;
    }).then(function () { $("stask-note-go").disabled = false; });
  });

  booted = true;
  applyMode(); // Vibe mode or Pro mode, as this browser keeps it
  sideShown();
  ["left", "right"].forEach(function (which) { if (paneShown(which)) freshView(panes[which].view); });
  if (startView === "file") setCodeView("file");
  refresh().then(function () {
    if ($("locked").hidden) connect();
  });
  refreshTickets(false);
  // Behind another window too, every half minute, for Needs you's
  // notifications and the app's badge.
  var hiddenTicks = 0;
  setInterval(function () {
    if (!document.hidden) { hiddenTicks = 0; refresh(); }
    else if (++hiddenTicks % 6 === 0) refresh();
  }, 5000);
  setInterval(function () { if (!document.hidden) refreshTickets(false); }, 30000);
})();
