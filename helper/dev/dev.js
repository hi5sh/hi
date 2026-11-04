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
        if (response.status === 401) lock(data.error || "This page is for the app's owners.");
        if (!response.ok) throw new Error(data.error || response.statusText);
        return data;
      });
    });
  }

  function lock(why) {
    $("locked-why").textContent = why;
    $("locked").hidden = false;
  }

  // ---- tabs: Tasks, Code (herdr), System; the page remembers the last one.
  // Files are beside the chat, in its panel.
  function show(tab) {
    document.querySelectorAll(".tabs button").forEach(function (button) {
      button.setAttribute("aria-selected", button.dataset.tab === tab ? "true" : "false");
    });
    document.querySelectorAll("#page > .panel").forEach(function (panel) {
      panel.hidden = panel.id !== "tab-" + tab;
    });
    try { localStorage.setItem("hi-dev-tab", tab); } catch (e) { /* private window */ }
    if (tab === "terminal") setTimeout(function () { if (chat.view === "terminal") { fitTerminal(); term.focus(); } else { pollChat(); sideShown(); } }, 0);
    if (tab === "decisions") refreshDecisions();
    if (tab === "data") refreshData();
    if (tab === "security" && !developer()) refreshPeople();
  }
  document.querySelectorAll(".tabs button").forEach(function (button) {
    button.addEventListener("click", function () { show(button.dataset.tab); });
  });

  // ---- the terminal
  var term = new Terminal({
    cursorBlink: true,
    fontSize: window.innerWidth < 600 ? 11 : 13,
    fontFamily: 'ui-monospace, "SF Mono", Menlo, "DejaVu Sans Mono", monospace',
    allowProposedApi: false,
    // VS Code's own terminal colors, 2026 Dark, on the card it sits in.
    theme: {
      background: "#121314", foreground: "#bfbfbf", cursor: "#bfbfbf", selectionBackground: "#3994bc33",
      black: "#000000", red: "#cd3131", green: "#0dbc79", yellow: "#e5e510",
      blue: "#2472c8", magenta: "#bc3fbc", cyan: "#11a8cd", white: "#e5e5e5",
      brightBlack: "#666666", brightRed: "#f14c4c", brightGreen: "#23d18b", brightYellow: "#f5f543",
      brightBlue: "#3b8eea", brightMagenta: "#d670d6", brightCyan: "#29b8db", brightWhite: "#e5e5e5",
    },
  });
  var fit = new FitAddon.FitAddon();
  term.loadAddon(fit);
  term.open($("term"));
  var socket = null;
  var encoder = new TextEncoder();

  // fitTerminal sizes the terminal to its panel, and herdr's panes with it.
  // Hidden, behind the chat, VS Code or another tab, it takes the size the
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
    var shown = [$("chat"), $("vscode")].filter(function (other) { return other.offsetParent; })[0];
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
  [".term-panel", "#chat", "#vscode"].forEach(function (panel) { new ResizeObserver(fitTerminal).observe(document.querySelector(panel)); });
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
      $("term-why").textContent = (event.reason ? "The terminal closed: " + event.reason + "." : "The terminal closed.") + " Connecting again…";
      $("term-ended").hidden = false;
      if ($("locked").hidden && !vibe()) termRetry = setTimeout(connect, 1000);
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
      if (!navigator.clipboard || !navigator.clipboard.readText) { toast("This browser doesn't let the page read the clipboard: paste with a long press in the terminal."); return; }
      navigator.clipboard.readText().then(function (text) {
        if (!text) return;
        // As a paste, so the app takes it as one, not as typing.
        sendText(term.modes && term.modes.bracketedPasteMode ? "\x1b[200~" + text + "\x1b[201~" : text);
        term.focus();
      }).catch(function () { toast("The browser didn't allow reading the clipboard."); });
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
  term.attachCustomKeyEventHandler(function (event) {
    var v = event.key === "v" || event.key === "V";
    return !(event.type === "keydown" && v && event.ctrlKey && !event.altKey && !event.metaKey);
  });
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
        reader.onerror = function () { reject(new Error("couldn't read the picture")); };
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

  function statusDot(status) {
    return el("span", { class: "dot " + (status || ""), title: status || "no agent" });
  }

  function focus(space) {
    // The chat follows the workspace you open, as the terminal does.
    chooseChatSpace(space.id);
    working("focus:" + space.id, api("POST", "focus", { workspace: space.id })).then(function () {
      show("terminal");
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
    $("start-go").textContent = "Creating the worktree…";
    api("POST", "start", { ticket: ticket.number, agent: agent }).then(function (answer) {
      $("start-dialog").close();
      toast((answer.warning ? answer.warning + " " : "") + "Started " + answer.branch + (agent ? " with " + agent : "") + ".");
      show("terminal");
      refresh().then(renderTickets);
    }).catch(function (error) {
      $("start-error").textContent = error.message;
      $("start-go").disabled = false;
      $("start-go").textContent = "Start →";
    });
  });

  // ---- Done and Reopen: a task closed when it's done, whatever its state
  function taskDone(ticket, space) {
    var name = "#" + ticket.number;
    if (space && space.path && space.checked && !space.ahead && !space.dirty) {
      // Its work is in dev: Finish, which closes the task too.
      ask("Done with " + name + "? Its work is in dev: its worktree, workspace, agent, and branch go, and the task closes.").then(function (answer) {
        if (answer === null) return;
        working("finish:" + space.id, api("POST", "finish", { workspace: space.id, discard: false })).then(function (r) { toast(r.text); refresh(); refreshTickets(true); }).catch(function (error) { toast(error.message); });
      });
      return;
    }
    var left = [];
    if (space && space.ahead) left.push(space.ahead + (space.ahead === 1 ? " commit" : " commits") + " not in dev");
    if (space && space.dirty) left.push("changes not committed");
    var question = left.length
      ? "Close " + name + "? Its workspace still has " + left.join(" and ") + "; they stay there, for Ship to Dev or Discard in the Code tab. A note for the task, if you like:"
      : "Close " + name + "? A note for the task, if you like:";
    ask(question, "why it's done (optional)").then(function (note) {
      if (note === null) return;
      setTaskState(ticket, true, note);
    });
  }
  function setTaskState(ticket, close, note) {
    api("POST", "ticket/state", { number: ticket.number, close: close, note: note || "" }).then(function (r) {
      toast(r.text);
      refreshTickets(true);
      if ($("view-dialog").open) openTicket(ticket.number);
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
  // The task filter's own chips: the Code tab has chips of its own.
  document.querySelectorAll(".tasks-tools .chips button").forEach(function (button) {
    button.addEventListener("click", function () {
      showing = button.dataset.show;
      document.querySelectorAll(".tasks-tools .chips button").forEach(function (other) { other.setAttribute("aria-pressed", other === button ? "true" : "false"); });
      refreshTickets(false);
    });
  });
  $("tasks-search").addEventListener("input", renderTickets);

  function renderTickets() {
    var list = $("tickets");
    list.textContent = "";
    if (!tickets) return;
    var where = tickets.forge === "hi" ? "hi's own tasks, in the app's folder" : "Issues in " + (tickets.where || tickets.forge);
    $("tickets-where").textContent = where;
    var all = (tickets.tickets || []).filter(function (ticket) {
      return showing === "all" || (showing === "closed" ? ticket.state === "closed" : ticket.state !== "closed");
    });
    $("tasks-count").textContent = showing === "open" ? String(all.length || "") : "";
    var words = $("tasks-search").value.trim().toLowerCase().split(/\s+/).filter(Boolean);
    var shown = all.filter(function (ticket) {
      var hay = ("#" + ticket.number + " " + ticket.title + " " + (ticket.author || "") + " " + (ticket.asked || "") + " " + (ticket.labels || []).join(" ")).toLowerCase();
      return words.every(function (word) { return hay.indexOf(word) >= 0; });
    });
    if (!shown.length) {
      list.appendChild(el("div", { class: "row empty muted", text: words.length ? "No task matches." : showing === "closed" ? "No closed tasks." : "No open tasks. + files one; the lead files what people ask for in the channel." }));
      return;
    }
    shown.forEach(function (ticket) {
      var space = ticket.state === "closed" ? null : spaceFor(ticket);
      var status = ticket.state === "closed" ? ["closed", "Closed"] : space ? ["working", "In progress"] : ["open", "Open"];
      // Who asked for it, not the forge's account the lead filed it as;
      // and who started its work.
      var who = ticket.asked || ticket.author;
      var context = [who ? el("span", { text: who, title: ticket.asked && ticket.author && ticket.asked !== ticket.author ? "Filed as " + ticket.author : "" }) : null,
        space && space.by && space.by !== who ? el("span", { text: "· started by " + space.by }) : null].concat((ticket.labels || []).map(function (label) {
        return el("span", { class: "label", text: label });
      }));
      var items = [["View", function () { openTicket(ticket.number); }]];
      if (ticket.url) items.push(["Open where it's kept", function () { window.open(ticket.url, "_blank", "noopener"); }]);
      if (space) items.push(["Show its workspace", function () { focus(space); }]);
      if (ticket.state === "closed") items.push(["Reopen", function () { setTaskState(ticket, false); }]);
      var doneButton = ticket.state === "closed" ? null : el("button", { class: "done", text: "Done ✓", busy: space ? "finish:" + space.id : "", title: space ? "Finish its work and close the task" : "Close the task", onclick: function () { taskDone(ticket, space); } });
      var action = ticket.state === "closed" ? el("span") : space
        ? el("button", { class: "go", text: "Open →", title: "Show its workspace in the terminal", onclick: function () { focus(space); } })
        : el("button", { class: "go", text: "Start →", title: "A branch, a worktree, and an agent told to read the task", onclick: function () { startDialog(ticket); } });
      list.appendChild(el("div", { class: "row", role: "row" }, [
        el("span", { class: "id", role: "cell" }, [el("button", { class: "id-pill", text: "#" + ticket.number, title: "View the task", onclick: function () { openTicket(ticket.number); } })]),
        el("span", { class: "title", role: "cell" }, [
          el("button", { class: "open-ticket", text: ticket.title, title: "View the task: its text, files, and comments", onclick: function () { openTicket(ticket.number); } }),
          el("span", { class: "context muted small" }, context),
        ]),
        el("span", { class: "status", role: "cell" }, [el("span", { class: "pill " + status[0] }, [space ? statusDot(space.status) : null, document.createTextNode(status[1])])]),
        el("span", { class: "updated muted small", role: "cell", title: ticket.updated ? when(ticket.updated) : "", text: ago(ticket.updated || ticket.created) }),
        el("span", { class: "actions", role: "cell" }, [action, doneButton, menu(items)]),
      ]));
    });
  }

  function renderWorkspaces() {
    renderChatSpaces();
    var list = $("workspaces");
    list.textContent = "";
    $("spaces-count").textContent = String(workSpaces().length || "");
    if (!workSpaces().length) list.appendChild(el("li", { class: "muted small", text: "No work yet. Start a task in Tasks, or + New in the chat." }));
    $("box").textContent = state.box === "running" ? "" : state.box;
    workSpaces().forEach(function (space) {
      // A piece of work's worktree: how much of it dev has, and Finish.
      var where = "";
      var unshipped = space.checked && (space.ahead > 0 || space.dirty);
      if (space.path && space.checked) {
        var left = [];
        if (space.ahead > 0) left.push(space.ahead + (space.ahead === 1 ? " commit" : " commits") + " not shipped");
        if (space.dirty) left.push("changes not committed");
        where = left.length ? left.join(", ") : "everything in dev";
      }
      var what = el("div", { class: "what" }, [
        el("div", { text: space.label + (space.agent ? " · " + space.agent : "") }),
        space.title ? el("div", { class: "muted small", text: space.title }) : null,
        space.by ? el("div", { class: "muted small", text: "Started by " + space.by }) : null,
        where ? el("div", { class: "small " + (unshipped ? "warn" : "muted"), text: where }) : null,
      ]);
      var actions = [el("button", { text: "Open", busy: "focus:" + space.id, onclick: function () { focus(space); } })];
      if (space.path && space.checked && space.ahead > 0) {
        // What hi dev preview and hi dev ship do in the box, by a click.
        actions.push(tryButton(space));
        actions.push(shipButton(space));
      }
      if (space.path && space.checked) {
        actions.push(el("button", {
          class: unshipped ? "danger" : "",
          text: unshipped ? "Discard…" : "Done ✓",
          busy: "finish:" + space.id,
          title: unshipped ? "Remove it without shipping what dev hasn't" : "Remove its worktree, workspace, and branch, and close its task",
          onclick: function () {
            var question = unshipped
              ? "Discard " + space.branch + "? " + where + " will be lost: its worktree, workspace, agent, and branch go."
              : "Done with " + space.branch + "? Its work is in dev. Its worktree, workspace, agent, and branch go, and its task closes.";
            act(question, "finish", { workspace: space.id, discard: unshipped }, function (r) { return r.text; }, "finish:" + space.id);
          },
        }));
      }
      list.appendChild(el("li", { class: "ticket" }, [statusDot(space.status), what, el("div", { class: "actions" }, actions)]));
    });
  }

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
      nodes.push(el("div", { class: "muted small", text: s.preview_branch ? "a branch being tried, not dev" : "dev as it was, before what was shipped since" }));
      nodes.push(el("button", { class: "small show-dev", text: "Show dev", busy: "preview-dev", title: "Put dev as it is on Stage again; nothing in dev changes", onclick: function () {
        toast("Putting dev on Stage…", true);
        act("", "preview-dev", {}, function (r) { return r.text; }, "preview-dev");
      } }));
    }
    return el("div", { class: "preview-what" }, nodes);
  }
  function renderPreviewLine() {
    var s = state.status || {};
    var line = $("preview-line");
    line.hidden = !s.preview;
    line.textContent = "";
    if (!s.preview) return;
    line.appendChild(el("span", { class: "muted", text: "Stage: " }));
    line.appendChild(previewWhat(s));
  }

  function renderSystem() {
    renderPreviewLine();
    var s = state.status || {};
    var facts = $("system");
    facts.textContent = "";
    function fact(name, value) {
      facts.appendChild(el("dt", { text: name }));
      facts.appendChild(el("dd", {}, [typeof value === "string" ? document.createTextNode(value) : value]));
    }
    fact("Production", s.production ? link("v" + s.production, s.production_url) : document.createTextNode("nothing released yet"));
    var dev = el("div", {}, [document.createTextNode((s.dev || "").replace(/^dev, /, ""))]);
    if (s.changes && s.changes.length) {
      dev.appendChild(el("ul", {}, s.changes.map(function (change) { return el("li", { text: change }); })));
    }
    fact("Dev", dev);
    fact("Stage", previewWhat(s));
    fact("Backups", s.snapshot || "none yet");
    fact("Incidents", document.createTextNode(state.incidents && state.incidents.length ? state.incidents.length + " open, below" : "none open"));
    if (s.jobs && s.jobs.length) fact("Jobs", el("ul", {}, s.jobs.map(function (job) { return el("li", { text: job }); })));

    var links = $("links");
    links.textContent = "";
    if (s.production_url) links.appendChild(link("Production", s.production_url));
    if (s.preview_url) links.appendChild(link("Stage", s.preview_url));
  }

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

  // Asks, then does an owner's action, and says how it went.
  // Try on Stage and Ship to Dev: what hi dev preview and hi dev ship do
  // in the box, for a workspace's branch, by a click.
  function tryButton(space) {
    return el("button", { text: "Try on Stage", busy: "try:" + space.id, title: "Show this branch on Stage, alone, to try it; dev doesn't change, and Show dev puts it back", onclick: function () {
      toast("Putting " + space.branch + " on Stage…", true);
      act("", "ship", { workspace: space.id, preview: true }, function (r) { return r.text; }, "try:" + space.id);
    } });
  }
  function shipButton(space) {
    return el("button", { class: "primary", text: "Ship to Dev", busy: "ship:" + space.id, title: "Merge its commits into dev, as hi dev ship does", onclick: function () {
      ask("Ship " + space.branch + " into dev? Its " + (space.ahead === 1 ? "commit merges" : space.ahead + " commits merge") + " into dev, and dev goes on Stage; production moves when someone releases it." +
        (space.dirty ? " Its changes not committed stay in the worktree." : "") + " A title for the change, for the release notes:", "the last commit's title").then(function (title) {
        if (title === null) return;
        toast("Shipping " + space.branch + " into dev…", true);
        act("", "ship", { workspace: space.id, title: title }, function (r) { return r.text; }, "ship:" + space.id);
      });
    } });
  }
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
        el("button", { text: preview ? "Restart Stage" : "Restart production", busy: "incident:" + incident.id, onclick: function () {
          act("Give " + (preview ? "Stage" : "production") + "'s app a new box?", "incident", { id: incident.id, action: "restart", preview: preview }, "Restarted.", "incident:" + incident.id);
        } }),
        el("button", { text: "Close", busy: "incident:" + incident.id, onclick: function () {
          ask("Close " + incident.id + "? What resolved it?", "what resolved it").then(function (because) {
            if (because === null) return;
            act("", "incident", { id: incident.id, action: "close", because: because }, incident.id + " is closed.", "incident:" + incident.id);
          });
        } }),
      ])]));
    });

    // Releases, each one production can go back to.
    var releases = $("releases");
    releases.textContent = "";
    if (!state.releases || !state.releases.length) releases.appendChild(el("li", { class: "muted", text: "Nothing released yet." }));
    (state.releases || []).forEach(function (release) {
      var actions = [];
      if (release.version === production) {
        actions.push(el("span", { class: "muted small", text: "in production" }));
      } else {
        actions.push(el("button", { class: "owners-only", text: "Roll back to v" + release.version, busy: "rollback:" + release.version, onclick: function () {
          act("Put production back on v" + release.version + "'s code? Its data stays as it is; hi snapshots it first.", "rollback", { version: release.version }, function (r) { return "Production runs v" + r.release.version + " now."; }, "rollback:" + release.version);
        } }));
        if (release.data) {
          actions.push(el("button", { class: "danger owners-only", text: "…and its data", busy: "rollback:" + release.version, onclick: function () {
            act("Put production back on v" + release.version + ", code and data? What was entered since v" + release.version + " left is lost from production (hi snapshots it first).", "rollback", { version: release.version, data: true }, function (r) { return "Production runs v" + r.release.version + ", with its data, now."; }, "rollback:" + release.version);
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

    // Where things run: each address, what it shows now, and the work that
    // could go on Stage; internal by default, public by an owner's
    // choice.
    var addresses = $("addresses");
    addresses.textContent = "";
    var names = { production: "Production", preview: "Stage", dev: "This page" };
    var s = state.status || {};
    var changes = state.changes || [];
    function plural(n, one, many) { return n + " " + (n === 1 ? one : many); }
    (state.addresses || []).forEach(function (a) {
      var head = el("div", { class: "where-head" }, [el("strong", { text: names[a.address] }), document.createTextNode(" · "), link(a.internal.replace(/^http:\/\//, "").replace(/\/$/, ""), a.internal)]);
      var lines = [head];
      if (a.address === "production") {
        lines.push(el("div", { class: "where-what" }, [el("span", { class: "tag live", text: s.production ? "v" + s.production : "nothing released yet" }), document.createTextNode(s.production ? " what everyone uses" : "")]));
        if (changes.length) {
          lines.push(el("div", { class: "where-what" }, [
            el("span", {}, [el("span", { class: "arrow", text: "↑ " }), document.createTextNode("dev has " + plural(changes.length, "change", "changes") + " waiting: " + changes.map(function (c) { return c.title; }).slice(0, 3).join("; ") + (changes.length > 3 ? "; …" : ""))]),
          ]));
        } else if (s.production) {
          lines.push(el("div", { class: "muted small", text: "Dev is the same: nothing to release." }));
        }
        // An app that starts from code it already had has no tasks of its
        // own yet, and dev is still its first release.
        if (releasing) {
          lines.push(el("div", { class: "where-what" }, [
            el("button", { class: "primary small busy", disabled: "", text: "Releasing v" + releasing + "…" }),
            el("span", { class: "muted small", text: "A snapshot of production first, then v" + releasing + " in a new box: a minute or two." }),
          ]));
        } else if (changes.length || !s.production) {
          var release = [el("button", { class: "primary small", text: "Release v" + ((s.production || 0) + 1), title: "Dev goes to production, with a snapshot first. Your click is the yes, logged with your name.", onclick: openRelease })];
          if (changes.length && s.production) {
            release.push(el("button", { class: "link danger small owners-only", text: "Set dev back to production", busy: "dev-reset", onclick: function () {
              act("Set dev's code back to what production runs? What dev has on top of it stays in its history, and Stage shows production's code.", "dev/reset", {}, "Dev is the same as production now.", "dev-reset");
            } }));
          }
          lines.push(el("div", { class: "where-what" }, release));
        }
      } else if (a.address === "preview") {
        var showing = [el("span", { class: "muted", text: "showing " })];
        if (s.preview_branch) {
          showing.push(el("span", { class: "tag active", text: "▸ " + s.preview_branch }), document.createTextNode(" a branch being tried, not dev "));
        } else if (s.preview_old) {
          showing.push(el("span", { class: "tag", text: "dev as it was" }), document.createTextNode(" before what was shipped since "));
        } else if (s.preview) {
          showing.push(el("span", { class: "tag active", text: "dev" }), document.createTextNode(" " + (changes.length ? "v" + (s.production || 0) + " + " + plural(changes.length, "change", "changes") : "the same as production") + " "));
        } else {
          showing.push(el("span", { class: "tag", text: "nothing" }));
        }
        if (s.preview_branch || s.preview_old) {
          showing.push(el("button", { class: "small", text: "Show dev", busy: "preview-dev", title: "Put dev as it is on Stage again; nothing in dev changes", onclick: function () {
            toast("Putting dev on Stage…", true);
            act("", "preview-dev", {}, function (r) { return r.text; }, "preview-dev");
          } }));
        }
        lines.push(el("div", { class: "where-what" }, showing));
        // The work dev hasn't got: which is on Stage, and the rest to try.
        var work = workSpaces().filter(function (space) { return space.path && space.checked && (space.ahead > 0 || space.dirty); });
        if (work.length) {
          lines.push(el("ul", { class: "where-work" }, work.map(function (space) {
            var left = [];
            if (space.ahead) left.push(plural(space.ahead, "commit", "commits") + " not in dev");
            if (space.dirty) left.push("changes not committed");
            var on = space.branch === s.preview_branch;
            var end = on ? el("span", { class: "on-preview", text: "← on Stage now" })
              : space.ahead > 0 ? el("button", { class: "small", text: "Try on Stage", busy: "try:" + space.id, title: "Show this branch on Stage, alone; dev doesn't change", onclick: function () {
                  toast("Putting " + space.branch + " on Stage…", true);
                  act("", "ship", { workspace: space.id, preview: true }, function (r) { return r.text; }, "try:" + space.id);
                } })
              : el("span", { class: "muted small", text: "commit it to try it" });
            return el("li", { class: on ? "on" : "" }, [el("span", { class: "dot " + (on ? "working" : "") }), el("span", { class: "work-info" }, [el("span", { class: "work-name", text: space.branch }), el("span", { class: "muted small", text: left.join(", ") })]), end]);
          })));
        } else {
          lines.push(el("div", { class: "muted small", text: "No work waiting to be tried." }));
        }
      } else {
        lines.push(el("div", { class: "muted small", text: "The dev page, for the owners." }));
      }
      if (!a.joined) lines.push(el("div", { class: "small warn", text: "The app's NetBird peer hasn't joined: hi app dev " + state.team + " netbird <a setup key>, on the machine" }));
      if (a.public) {
        var publicLine = a.link ? "Public too: " : "Public too, but it has no public address right now";
        var parts = [document.createTextNode(publicLine)];
        if (a.link) parts.push(link(a.link.replace(/^https:\/\//, "").split("/")[0], a.link));
        var notes = [];
        if (a.domain) notes.push("name " + a.domain);
        notes.push(a.allow_ips ? "only from " + a.allow_ips : "from anywhere, with the link's key");
        lines.push(el("div", { class: "small warn" }, parts));
        lines.push(el("div", { class: "muted small", text: notes.join(" · ") + " (hi app domain, hi app allow-ip on the machine)" }));
      } else {
        lines.push(el("div", { class: "muted small", text: "Internal only" }));
      }
      var button = a.public
        ? el("button", { class: "small owners-only", text: "Internal only", busy: "public:" + a.address, onclick: function () {
            act("Make " + names[a.address].toLowerCase() + " internal only? Its public address ends: people reach it on the app's NetBird network, at " + a.internal, "public", { address: a.address, on: false }, names[a.address] + " is internal only.", "public:" + a.address);
          } })
        : el("button", { class: "small danger owners-only", text: "Make public", busy: "public:" + a.address, onclick: function () {
            act("Make " + names[a.address].toLowerCase() + " public too? It gets an address on the internet through NetBird, which anyone with its link can open" + (a.address === "dev" ? ", and this page builds and releases the app." : "."), "public", { address: a.address, on: true }, names[a.address] + " is public too.", "public:" + a.address);
          } });
      addresses.appendChild(el("li", { class: "where-row " + a.address }, [el("div", { class: "what" }, lines), el("div", { class: "actions" }, [button])]));
    });

    $("data-copy").disabled = !production || !state.preview_data;
    $("data-copy").title = !production ? "Nothing is released yet" : (!state.preview_data ? "Stage has no data" : "");
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
  // of the work that asked; in Security, what the owners allowed and what
  // the box was refused lately.
  function siteAct(host, allow) {
    act("", "site", { host: host, allow: allow }, allow ? "The dev box reaches " + host + " now." : "Said no to " + host + ".");
  }
  function renderSites() {
    var access = state.access || {};
    $("security-count").textContent = String((access.asks || []).length || "");
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
      list.appendChild(el("li", {}, [
        el("div", { class: "what" }, [el("code", { text: site.host }), el("div", { class: "muted small", text: ["Allowed", site.by ? "by " + site.by : "", site.why ? "· " + site.why : ""].filter(Boolean).join(" ") })]),
        el("div", { class: "actions owners-only" }, [el("button", { text: "Remove", onclick: function () {
          act("Stop the dev box reaching " + site.host + "?", "site", { host: site.host, remove: true }, "The dev box doesn't reach " + site.host + " any more.");
        } })]),
      ]));
    });
    (access.refused || []).forEach(function (refused) {
      list.appendChild(el("li", {}, [
        el("div", { class: "what" }, [el("code", { text: refused.host }), el("div", { class: "muted small", text: "Refused " + refused.times + (refused.times === 1 ? " time" : " times") + ", last " + when(refused.last) + "; nobody asked for it" })]),
        el("div", { class: "actions owners-only" }, [
          el("button", { text: "Allow", onclick: function () { act("Let the dev box reach " + refused.host + "? No agent asked for it.", "site", { host: refused.host, allow: true }, "The dev box reaches " + refused.host + " now."); } }),
          el("button", { text: "Dismiss", onclick: function () { siteAct(refused.host, false); } }),
        ]),
      ]));
    });
    if (!list.childNodes.length) list.appendChild(el("li", { class: "muted", text: "Nothing else yet." }));
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
        el("span", { class: "muted small", text: "The site and its subdomains, from now on; Security lists them." }),
      ]));
      card.appendChild(el("p", { class: "developers-only muted small", text: "An owner allows sites for the dev box; they see this ask in Security, and the channel hears of it." }));
      box.appendChild(card);
    });
    if (fresh) $("chat-log").scrollTop = $("chat-log").scrollHeight;
  }
  function renderKeys() {
    var list = $("team-keys");
    list.textContent = "";
    (state.keys || []).forEach(function (key) {
      list.appendChild(el("li", {}, [
        el("div", { class: "what" }, [document.createTextNode(key.name + ": "), el("strong", { text: key.state }),
          el("div", { class: "muted small" }, [document.createTextNode("To change it, an admin runs "), el("code", { text: key.command }), document.createTextNode(" on the machine.")])]),
      ]));
    });
  }

  // ---- the Data tab: production's and Stage's data, and the
  // snapshots; asked for when the tab opens, as sizing a folder takes a
  // moment.
  function size(bytes) {
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
  function renderData(data) {
    (data.folders || []).forEach(function (folder) {
      var facts = $("data-" + folder.slot);
      facts.textContent = "";
      function fact(name, value) {
        facts.appendChild(el("dt", { text: name }));
        facts.appendChild(el("dd", { text: value }));
      }
      if (folder.slot === "preview" && folder.fork) {
        fact("A copy of", "production's" + (folder.fork.version ? " v" + folder.fork.version : "") + ", " + stamp(folder.fork.at) + " (" + size(folder.fork.bytes) + ")");
        fact("Written since", folder.files ? size(folder.bytes) + " in " + folder.files + " file" + (folder.files === 1 ? "" : "s") : "nothing");
      } else {
        fact("Holds", folder.files ? size(folder.bytes) + " in " + folder.files + " file" + (folder.files === 1 ? "" : "s") : "nothing yet");
      }
      if (folder.changed) fact("Last written", ago(folder.changed) || stamp(folder.changed));
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
        el("div", { class: "muted small", text: snapshot.id + (snapshot.size ? " · " + size(snapshot.size) : "") }),
      ])]));
    });
    $("data-restore").textContent = "hi app restore " + (state.team || "<app>") + " <snapshot>";
  }
  function dataAct(question, path, body, done) {
    (question ? ask(question) : Promise.resolve("")).then(function (answer) {
      if (answer === null) return;
      toast("Working on it…");
      api("POST", path, body).then(function () { toast(done); refresh(); refreshData(); })
        .catch(function (error) { toast(error.message); refreshData(); });
    });
  }
  $("data-fork").addEventListener("click", function () {
    dataAct("Give Stage a fresh copy of production's data? What Stage wrote itself is kept once, apart; production pauses for a moment, and Stage restarts on the copy.", "data/copy", { to: "preview" }, "Stage has a fresh copy of production's data.");
  });
  $("data-copy").addEventListener("click", function () {
    dataAct("Replace production's data with a copy of Stage's? hi snapshots production's data first, and production restarts on the copy.", "data/copy", {}, "Production has Stage's data now.");
  });
  $("data-snapshot").addEventListener("click", function () {
    dataAct("", "data/snapshot", {}, "The snapshot is taken.");
  });
  $("data-empty").addEventListener("change", function (event) {
    dataAct("", "data/preview", { empty: event.target.checked }, event.target.checked ? "Stage starts empty at every release." : "Stage starts on a copy of production's data at every release.");
  });
  ["preview", "production"].forEach(function (slot) {
    $("helper-" + slot).addEventListener("change", function (event) {
      var on = event.target.checked;
      act("", "helper", { slot: slot, on: on }, "The helper is " + (on ? "on" : "off") + (slot === "preview" ? " on Stage." : " in production."));
    });
  });
  $("rule-form").addEventListener("submit", function (event) {
    event.preventDefault();
    act("", "rule", { job: $("rule-job").value.trim(), rule: $("rule-text").value.trim() }, "The lead follows it from now on.");
    $("rule-text").value = "";
  });

  function renderSettings() {
    var list = $("settings");
    list.textContent = "";
    if (!state.settings.length) list.appendChild(el("li", { class: "muted", text: "The app gets no settings." }));
    state.settings.forEach(function (label) {
      var name = label.split(" ")[0];
      list.appendChild(el("li", {}, [
        el("div", { class: "what" }, [el("code", { text: name }), el("span", { class: "muted small", text: label.slice(name.length) })]),
        el("div", { class: "actions owners-only" }, [el("button", {
          text: "Remove", onclick: function () {
            act("Remove " + name + " from Stage, production, and the dev box?", "settings", { key: name, unset: true }, name + " is removed.");
          },
        })]),
      ]));
    });
  }

  // A file instead of a value: the box gets it, and the variable its path.
  $("setting-file").addEventListener("change", function () {
    var file = !!$("setting-file").files.length;
    $("setting-value").required = !file;
    $("setting-value").disabled = file;
  });

  function settingFile() {
    var file = $("setting-file").files[0];
    if (!file) return Promise.resolve("");
    if (file.size > 32 << 10) return Promise.reject(new Error(file.name + " is larger than 32 KB; a file setting is for a certificate or a key"));
    return new Promise(function (resolve, reject) {
      var reader = new FileReader();
      reader.onload = function () { resolve(String(reader.result).replace(/^[^,]*,/, "")); };
      reader.onerror = function () { reject(new Error("couldn't read " + file.name)); };
      reader.readAsDataURL(file);
    });
  }

  $("setting-form").addEventListener("submit", function (event) {
    event.preventDefault();
    var key = $("setting-key").value.trim();
    settingFile().then(function (file) {
      var request = { key: key, slot: $("setting-slot").value };
      if (file) request.file = file; else request.value = $("setting-value").value;
      return api("POST", "settings", request).then(function () {
        $("setting-value").value = "";
        $("setting-key").value = "";
        $("setting-file").value = "";
        $("setting-value").required = true;
        $("setting-value").disabled = false;
        toast(file ? key + " is set: the app finds the file at $" + key + "." : key + " is set.");
        refresh();
      });
    }).catch(function (error) { toast(error.message); });
  });

  // ---- People (the owners'), and everyone's own SSH key
  // A Pro Dev or a Vibe Dev: no owners' buttons. A Vibe Dev also has the
  // agents' Chat only, without the terminal, VS Code, or SSH.
  function developer() { return !!state && (state.level === "developer" || state.level === "vibe"); }
  function vibe() { return !!state && state.level === "vibe"; }
  function renderSSHKey() {
    $("sshkey-now").textContent = state.ssh_key ? "Now: " + state.ssh_key + ". A new one replaces it." : "You have no key in the dev box yet.";
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
    var data = $("people-warn").hidden ? "" : " Stage runs on a copy of production's data, which they can read through Stage.";
    var words = { owner: "an owner", developer: "a Pro Dev", vibe: "a Vibe Dev", contributor: "a contributor" };
    if (level === "developer") question = "Make " + person.name + " a Pro Dev? They work in the dev box as you do, with Chat, terminal, VS Code, and SSH, and release, except rollbacks, production's data and settings, keys, sites, addresses, and who is who." + data;
    else if (level === "vibe") question = "Make " + person.name + " a Vibe Dev? They work with the agents in Chat, without the terminal, VS Code, or SSH; the agents still run commands in the dev box for them. They release too." + data;
    else if (level === "none") question = "Take away " + person.name + "'s level? They can't report, the lead won't take their requests, and their links end.";
    else if (person.level === "developer" || person.level === "vibe") question = "Make " + person.name + " " + words[level] + "? Their dev page link" + (person.level === "developer" ? " and SSH key end, and an open terminal or VS Code closes." : " ends.");
    (question ? ask(question + " The channel hears it.") : Promise.resolve("")).then(function (answer) {
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
    var levels = ["owner", "developer", "vibe", "contributor", "none"];
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
    if (!body.childNodes.length) body.appendChild(el("tr", {}, [el("td", { colspan: "6", class: "muted small", text: find ? "Nobody by that name." : "Nobody yet." })]));
    if (view.channel_error) body.appendChild(el("tr", {}, [el("td", { colspan: "6", class: "muted small", text: view.channel_error })]));
    $("people-warn").hidden = !view.preview_data;
  }
  $("people-search").addEventListener("input", function () { if (people) renderPeople(people); });

  function openRelease() {
    var next = ((state.status || {}).production || 0) + 1;
    $("release-title").textContent = "Release v" + next;
    $("release-notes").value = (state.changes || []).map(function (task) { return "- " + task.title; }).join("\n");
    $("release-error").textContent = "";
    $("release-go").disabled = false;
    // The first release may take what Stage gathered along, once.
    $("release-data").checked = false;
    // Production's data is the owners'.
    $("release-data-box").hidden = developer() || !!((state.status || {}).production) || !state.preview_data;
    $("release-dialog").showModal();
  }
  $("release-form").addEventListener("submit", function (event) {
    if (event.submitter && event.submitter.value !== "go") return;
    event.preventDefault();
    // The release takes a minute or two (a snapshot, then a new box): the
    // dialog closes now, and the page says how it goes.
    releasing = ((state.status || {}).production || 0) + 1;
    $("release-dialog").close();
    $("release-error").textContent = "";
    toast("Releasing v" + releasing + ": a snapshot of production first, then the new box…", true);
    if (state.status) renderOwnerActions();
    api("POST", "release", { notes: $("release-notes").value, preview_data: $("release-data").checked }).then(function (result) {
      releasing = 0;
      toast("Production runs v" + result.release.version + " now." + (result.warning ? " " + result.warning : ""));
      refresh();
    }).catch(function (error) {
      // Back in the dialog, with the notes as they were.
      releasing = 0;
      $("toast").hidden = true;
      if (state.status) renderOwnerActions();
      $("release-error").textContent = error.message;
      $("release-go").disabled = false;
      $("release-dialog").showModal();
    });
  });

  // ---- keeping current
  function refresh() {
    return api("GET", "state").then(function (data) {
      state = data;
      // A developer's page: the owners' buttons go (the server refuses
      // them anyway), and so does the Data tab.
      document.body.classList.toggle("developer", developer());
      document.body.classList.toggle("vibe", vibe());
      if (vibe() && chat.view !== "chat") setCodeView("chat");
      document.querySelectorAll(".owners-control").forEach(function (control) { control.disabled = developer(); });
      if (developer() && !$("tab-data").hidden) show("tasks");
      renderSSHKey();
      document.title = "Dev · " + data.team;
      $("team").textContent = data.team;
      $("you").textContent = data.you;
      renderSystem();
      renderSites();
      renderSettings();
      renderOwnerActions();
      renderWorkspaces();
      renderTickets();
      openLinkedTask();
      $("release-count").textContent = String((data.changes || []).length || "");
    }).catch(function () {});
  }
  // A link to a task's work, #task-12, as the lead posts when it starts
  // one, opens that work's chat once its workspace is there.
  function openLinkedTask() {
    if (location.hash === "#sites") {
      // The channel's link to an agent's ask for a site.
      history.replaceState(null, "", location.pathname + location.search);
      show("security");
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
      renderTickets();
    }).catch(function (error) { $("tickets-where").textContent = error.message; });
  }
  $("tickets-refresh").addEventListener("click", function () { refreshTickets(true); });

  // ---- the chat: a workspace's Claude Code session as a conversation,
  // read from its transcript; what you write goes to the agent, so the chat
  // and the terminal are one session
  var narrow = window.matchMedia && window.matchMedia("(max-width: 760px), (pointer: coarse)").matches;
  var chat = { view: "chat", space: "", session: "", offset: 0, tools: {}, group: null, busy: false, timer: 0, agent: null, id: "", pending: [], turn: 0, spin: 0, composing: false, askSig: "" };
  // The rewind timeline's messages, what the page last saw, and the one open.
  var rewind = { points: [], seen: "", open: "" };
  // The panel beside the chat: its open view, whether it's shown, and how
  // wide. On a narrow screen it lies over the chat, so it starts hidden.
  var sideOver = window.matchMedia ? window.matchMedia("(max-width: 1100px)") : { matches: false };
  var side = { view: "files", open: true, width: 340 };
  try {
    var keptSide = JSON.parse(localStorage.getItem("hi-dev-side") || "{}");
    if (["files", "git", "rewind"].indexOf(keptSide.view) >= 0) side.view = keptSide.view;
    if (keptSide.open === false) side.open = false;
    if (keptSide.width >= 240) side.width = keptSide.width;
  } catch (e) { /* private window */ }
  if (sideOver.matches) side.open = false;
  // The source control view: what git last said of the chat's workspace,
  // and the file whose diff is open.
  var git = { view: null, space: "", busy: false, at: 0, diff: null, error: "" };
  try {
    // Chat is the default since hi 2.5.24; the choice before it isn't kept.
    chat.view = localStorage.getItem("hi-dev-code-view-2") || chat.view;
    if (narrow && chat.view === "code") chat.view = "chat"; // VS Code is for a wide screen
    chat.space = localStorage.getItem("hi-dev-chat-space") || "";
  } catch (e) { /* private window */ }
  function setCodeView(view) {
    chat.view = view;
    try { localStorage.setItem("hi-dev-code-view-2", view); } catch (e) { /* private window */ }
    document.querySelectorAll("#code-view button").forEach(function (button) { button.setAttribute("aria-pressed", button.dataset.view === view ? "true" : "false"); });
    $("chat").hidden = view !== "chat";
    document.querySelector(".term-panel").hidden = view !== "terminal";
    $("vscode").hidden = view !== "code";
    $("chat-space").hidden = view !== "chat" && view !== "code";
    showChatStart();
    $("chat-title").hidden = view !== "chat";
    $("chat-status").hidden = view !== "chat";
    if (view === "terminal") setTimeout(function () { fitTerminal(); term.focus(); }, 0);
    else if (view === "code") openVSCode();
    else { pollChat(); sideShown(); }
  }
  // VS Code: code-server in the dev box, which hi starts the first time
  // (and fetches, the very first), on the workspace's worktree.
  var vscode = { folder: "", starting: false };
  function openVSCode() {
    var space = ((state && state.workspaces) || []).filter(function (s) { return s.id === chat.space; })[0];
    var folder = (space && space.path) || "/box/repo";
    if (folder === vscode.folder || vscode.starting) return;
    vscode.starting = true;
    $("vscode-status").textContent = "Starting VS Code in the dev box… The first time, hi fetches it, which takes a minute.";
    api("POST", "code/start", {}).then(function () {
      vscode.folder = folder;
      var src = "code/?folder=" + encodeURIComponent(folder);
      $("vscode-frame").src = src;
      $("vscode-open").href = src;
      $("vscode-status").textContent = (space ? (space.branch || space.label) : "The project's clone") + ": " + folder +
        // The viewers (PDFs, CSV tables, Markdown's preview) draw in
        // webviews, which the browser gives only to https pages.
        (location.protocol === "https:" ? "" : ". Previews of PDFs, tables, and Markdown need the app's https address (hi net domain).");
    }).catch(function (error) {
      $("vscode-status").textContent = error.message;
    }).then(function () { vscode.starting = false; });
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
    var select = $("chat-space");
    var options = spaces.map(function (space) { return space.id + "|" + space.label + "|" + (space.agent || ""); }).join(",");
    if (select.dataset.options === options) { select.value = chat.space; return; }
    select.dataset.options = options;
    select.textContent = "";
    spaces.forEach(function (space) {
      select.appendChild(el("option", { value: space.id, text: space.label + (space.agent ? " · " + space.agent : "") }));
    });
    select.value = chat.space;
  }
  $("chat-space").addEventListener("change", function () { chooseChatSpace(this.value); });
  function chooseChatSpace(id, quiet) {
    if (id === chat.space && quiet) return;
    chat.space = id;
    chat.sitesSig = null;
    try { localStorage.setItem("hi-dev-chat-space", id); } catch (e) { /* private window */ }
    $("chat-space").value = id;
    chat.session = "";
    chat.offset = 0;
    chat.turns = "";
    rewind.points = [];
    rewind.seen = "";
    rewind.open = "";
    renderPoints();
    clearChat();
    showChatStart();
    git.view = null;
    closeDiff();
    renderGit();
    syncFilesRoot();
    sideShown();
    if (chat.view === "code") openVSCode();
    if (!quiet) pollChat();
  }
  // The start card: when there's no work to show, or + New was pressed.
  function showChatStart() {
    var starting = chat.composing || (!!state && !chat.space); // not before the workspaces are known
    $("chat-start").hidden = !starting;
    document.querySelector(".chat-main").hidden = starting;
    document.querySelector(".chat-bottom").hidden = starting;
    $("chat-new").hidden = starting || chat.view !== "chat";
    $("chat-space").hidden = chat.view !== "chat" || !workSpaces().length;
    $("adhoc-cancel").hidden = !chat.space;
    if (starting) $("chat-title").textContent = "";
    renderSide();
  }
  $("chat-new").addEventListener("click", function () {
    chat.composing = true;
    showChatStart();
    $("adhoc-text").focus();
  });
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
  // by Start something new, New task, and a task's notes.
  function pictureBox(edit, shown, picker) {
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
      if (event.key !== "Enter") return;
      event.preventDefault();
      if (event.ctrlKey || event.metaKey) {
        var form = edit.closest("form");
        if (form) form.requestSubmit();
        else edit.closest(".start-card").querySelector(".primary").click();
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
            reader.onerror = function () { reject(new Error("couldn't read " + file.name)); };
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
  // The start card's box.
  var adhoc = pictureBox($("adhoc-text"), $("adhoc-pictures"));
  // Not a form: Safari on an iPad puts its AutoFill bar (passwords,
  // cards, addresses) over the keyboard for a box in a form with inputs.
  $("adhoc-go").addEventListener("click", function () {
    var agent = (document.querySelector("input[name=adhoc-agent]:checked") || {}).value || "claude";
    var text = adhoc.value();
    if (!text) { $("adhoc-error").textContent = "Say what to do."; $("adhoc-text").focus(); return; }
    $("adhoc-go").disabled = true;
    $("adhoc-go").textContent = "Creating the worktree…";
    $("adhoc-error").textContent = "";
    adhoc.read().then(function (files) {
      return api("POST", "start-new", { text: text, agent: agent, files: files });
    }).then(function (answer) {
      chat.composing = false;
      adhoc.clear();
      toast((answer.warning ? answer.warning + " " : "") + "Filed task #" + answer.ticket + " and started " + answer.branch + " with " + agent + ".");
      refreshTickets(true);
      return refresh().then(function () {
        if (answer.workspace) chooseChatSpace(answer.workspace);
        // The chat shows Claude Code's sessions; Codex's is in the terminal.
        if (agent === "codex" && !vibe()) setCodeView("terminal");
      });
    }).catch(function (error) {
      $("adhoc-error").textContent = error.message;
    }).then(function () {
      $("adhoc-go").disabled = false;
      $("adhoc-go").textContent = "Start →";
    });
  });
  function clearChat() {
    chatActivity(null);
    chatAsk(null);
    $("chat-entries").textContent = "";
    if (state) { chat.sitesSig = null; chatSites(); }
    chat.tools = {};
    chat.group = null;
    chat.pending = [];
    $("chat-title").textContent = "";
  }
  var verbs = {
    Bash: ["Ran a command", "Ran %d commands"], Read: ["Read a file", "Read %d files"], Edit: ["Edited a file", "Edited %d files"],
    MultiEdit: ["Edited a file", "Edited %d files"], Write: ["Wrote a file", "Wrote %d files"], NotebookEdit: ["Edited a notebook", "Edited %d notebooks"],
    Grep: ["Searched", "Searched %d times"], Glob: ["Looked for files", "Looked for files %d times"], WebFetch: ["Read a page", "Read %d pages"],
    WebSearch: ["Searched the web", "Searched the web %d times"], Task: ["Ran an agent", "Ran %d agents"], Agent: ["Ran an agent", "Ran %d agents"],
    TodoWrite: ["Updated the to-do list", "Updated the to-do list %d times"], AskUserQuestion: ["Asked you a question", "Asked you %d questions"],
  };
  function groupSummary(counts) {
    return Object.keys(counts).map(function (name) {
      var verb = verbs[name] || ["Used " + name, "Used " + name + " %d times"];
      return counts[name] === 1 ? verb[0] : verb[1].replace("%d", counts[name]);
    }).join(", ");
  }
  function chatEntry(entry) {
    var box = $("chat-entries");
    if (entry.t === "title") { $("chat-title").textContent = entry.text; return; }
    if (entry.t === "result") {
      var tool = chat.tools[entry.id];
      if (!tool) return;
      tool.result.textContent = entry.text || "(nothing)";
      tool.result.hidden = false;
      if (entry.error) { tool.box.classList.add("failed"); tool.result.classList.add("failed"); }
      return;
    }
    if (entry.t === "tool") {
      if (!chat.group) {
        var summary = el("summary");
        var body = el("div", { class: "fold-body" });
        chat.group = { counts: {}, summary: summary, body: body };
        box.appendChild(el("details", { class: "fold tools" }, [summary, body]));
      }
      chat.group.counts[entry.name] = (chat.group.counts[entry.name] || 0) + 1;
      chat.group.summary.textContent = groupSummary(chat.group.counts);
      var result = el("pre", { class: "result", hidden: "" });
      var item = el("details", { class: "fold tool" }, [
        el("summary", {}, [el("span", { class: "tool-name", text: entry.name }), el("span", { class: "tool-what", text: entry.summary || "" })]),
        entry.detail ? el("pre", { text: entry.detail }) : null,
        result,
      ]);
      chat.tools[entry.id] = { box: item, result: result };
      chat.group.body.appendChild(item);
      return;
    }
    chat.group = null;
    if (entry.t === "user") {
      if (entry.at) chat.turn = Date.parse(entry.at);
      var sent = chat.pending.shift();
      if (sent) sent.node.remove();
      box.appendChild(el("div", { class: "msg-user", "data-uuid": entry.uuid || "" }, [linked(entry.text)]));
    } else if (entry.t === "text") {
      box.appendChild(el("div", { class: "msg-text" }, [renderMarkdown(entry.text, "")]));
    } else if (entry.t === "thinking") {
      box.appendChild(el("details", { class: "fold thought" }, [el("summary", { text: "Thought" }), el("div", { class: "fold-body" }, [el("pre", { text: entry.text })])]));
    } else if (entry.t === "note") {
      box.appendChild(el("div", { class: "chat-note", text: entry.text }));
    }
  }
  function chatState(view) {
    var agent = view.agent || {};
    chat.agent = agent;
    chat.id = view.id || "";
    $("chat-status").textContent = "";
    chatActivity(agent.running && agent.status === "working" ? (view.activity || "") : null);
    var asking = agent.running && agent.status === "blocked";
    chatAsk(asking ? view.ask : null, asking ? view.screen : "", agent.agent);
    $("chat-waiting").hidden = !(asking && view.screen && !view.ask);
    if (!$("chat-waiting").hidden) $("chat-screen").textContent = view.screen;
    var stopped = !agent.running;
    $("chat-stopped").hidden = !stopped && !view.note;
    $("chat-stopped-why").textContent = view.note || "Claude isn't running in this workspace.";
    $("chat-resume").hidden = !(stopped && view.id && !view.note);
    $("chat-input").disabled = stopped || !!view.note;
    $("chat-send").disabled = stopped || !!view.note;
    $("chat-stop").hidden = !(agent.running && agent.status === "working");
    chatPending(agent);
  }
  // chatPending keeps what was sent until the transcript has it. Claude
  // Code holds a message sent while a tool runs until the tool ends, which
  // can be many minutes, so it waits as queued while Claude works; one
  // that hasn't come 20 seconds after Claude stopped working never will.
  function chatPending(agent) {
    var working = agent.running && (agent.status === "working" || agent.status === "blocked");
    chat.pending = chat.pending.filter(function (sent) {
      if (working) sent.since = Date.now();
      sent.node.classList.toggle("queued", !!working);
      if (Date.now() - sent.since < 20000) return true;
      sent.node.remove();
      return false;
    });
  }
  // chatActivity is the conversation's last line while Claude works, as in
  // its terminal: "✻ Crafting… (7s · ↓ 556 tokens)", from the terminal when
  // hi could read it, else how long since your message.
  var spinner = ["·", "✢", "✳", "✶", "✻", "✽", "✻", "✶", "✳", "✢"];
  function chatActivity(text) {
    var line = $("chat-activity");
    if (text === null) {
      if (line) line.remove();
      clearInterval(chat.spin);
      chat.spin = 0;
      return;
    }
    if (!line) {
      line = el("div", { id: "chat-activity", class: "chat-activity", role: "status" }, [el("span", { class: "spin", "aria-hidden": "true", text: "✻" }), el("span", { class: "act-text" })]);
      var frame = 0;
      chat.spin = setInterval(function () { line.firstChild.textContent = spinner[frame++ % spinner.length]; }, 120);
    }
    $("chat-entries").appendChild(line); // always the last line
    chat.activity = text;
    var seconds = Math.round((Date.now() - (chat.turn || Date.now())) / 1000);
    line.lastChild.textContent = text || (seconds >= 0 && seconds < 3600 ? "Working… (" + seconds + "s)" : "Working…");
  }
  // ---- Rewind: Claude Code's /rewind as a timeline beside the chat: its
  // messages, newest last, each with what the code did after it. One opens
  // to go back to before it: the code and the chat, the chat, or the code.
  // Codex keeps no checkpoints of the code, so it's Claude Code's only.
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
  function changeLine(list) {
    var added = 0, removed = 0;
    list.forEach(function (change) { added += change.added; removed += change.removed; });
    return [
      el("span", { text: list.length === 1 ? list[0].path.split("/").pop() : list.length + " files" }),
      added ? el("span", { class: "plus", text: " +" + added }) : null,
      removed ? el("span", { class: "minus", text: " −" + removed }) : null,
    ];
  }
  function changeWords(list) {
    return list.map(function (change) {
      return change.path + (change.added ? " +" + change.added : "") + (change.removed ? " −" + change.removed : "");
    }).join(", ");
  }
  function renderPoints() {
    var list = $("rewind-points"), empty = $("rewind-empty");
    var agent = chat.agent || {};
    var usable = agent.running && agent.agent === "claude";
    list.textContent = "";
    empty.hidden = usable && rewind.points.length > 0;
    empty.textContent = agent.running && agent.agent !== "claude" ? "Rewind is Claude Code's: " + agent.agent + " keeps no checkpoints of the code."
      : !usable ? "Rewind works while Claude Code runs in this workspace: each message you send is a point to go back to."
      : "Nothing to go back to yet: each message you send is a point here.";
    if (!empty.hidden) return;
    var busy = agent.status === "working" || agent.status === "blocked";
    rewind.points.forEach(function (point) { list.appendChild(pointItem(point, busy)); });
    list.appendChild(el("li", { class: "rp now" }, [el("span", { class: "rp-dot", "aria-hidden": "true" }), el("div", { class: "rp-now", text: "Now" })]));
  }
  function pointItem(point, busy) {
    var open = rewind.open === point.uuid;
    var changes = point.changes || [];
    var meta = [el("span", { text: point.at ? ago(point.at) : "" })];
    meta.push(el("span", { class: "rp-sep", text: " · " }));
    meta = meta.concat(changes.length ? changeLine(changes) : [el("span", { text: "No code changes" })]);
    var card = el("button", { type: "button", class: "rp-card", "aria-expanded": String(open),
      title: changes.length ? "After it: " + changeWords(changes) : "No code changes after it",
      onclick: function () {
        rewind.open = open ? "" : point.uuid;
        renderPoints();
        if (!open) showMessage(point.uuid);
      } }, [el("div", { class: "rp-text", text: point.text }), el("div", { class: "rp-meta" }, meta)]);
    var item = el("li", { class: "rp" + (changes.length ? " changed" : "") + (open ? " open" : "") }, [el("span", { class: "rp-dot", "aria-hidden": "true" }), card]);
    if (!open) return item;
    var actions = el("div", { class: "rp-actions" });
    [["both", "Code and chat", "primary"], ["conversation", "Chat only", ""], ["code", "Code only", ""]].forEach(function (choice) {
      var attrs = { type: "button", class: "small " + choice[2], text: choice[1], onclick: function () { rewindTo(point, choice[0]); } };
      if (busy || (choice[0] !== "conversation" && !point.code)) attrs.disabled = "";
      actions.appendChild(el("button", attrs));
    });
    item.appendChild(actions);
    item.appendChild(el("p", { class: "rp-note", text: busy ? "Claude is at work: Stop it first, then rewind."
      : !point.code ? "Claude Code has no checkpoint of the code here, so only the chat goes back."
      : "Back to before this message: the chat, the code Claude edited, or both." }));
    return item;
  }
  // showMessage scrolls the chat to a message the timeline names.
  function showMessage(uuid) {
    var message = uuid && document.querySelector('.msg-user[data-uuid="' + uuid + '"]');
    if (!message) return;
    message.scrollIntoView({ block: "center", behavior: "smooth" });
    message.classList.add("flash");
    setTimeout(function () { message.classList.remove("flash"); }, 1600);
  }
  function rewindTo(point, mode) {
    var restore = point.restore || [];
    var words = "Rewind to before “" + (point.text.length > 90 ? point.text.slice(0, 90) + "…" : point.text) + "”?";
    if (mode !== "code") words += " This message and everything after it leave the conversation; the message comes back in the box, to change and send again.";
    if (mode !== "conversation") {
      words += restore.length ? " The code goes back: " + changeWords(restore) + "." : " The code Claude edited is as it was then already.";
      words += " Restored files go back whole, losing later changes by anyone; what shell commands changed stays.";
    }
    ask(words).then(function (answer) {
      if (answer === null) return;
      toast("Rewinding…", true);
      api("POST", "chat/rewind", { workspace: chat.space, uuid: point.uuid, mode: mode }).then(function (result) {
        rewind.open = "";
        var input = $("chat-input");
        if (result.text && !input.value.trim()) {
          input.value = result.text;
          input.dispatchEvent(new Event("input"));
          input.focus();
        }
        toast(mode === "code" ? "The code is back to before that message." : "Rewound. The message is in the box, to change and send again.");
        chat.offset = 0;
        chat.session = "";
        chat.turns = "";
        pollChat();
        loadPoints();
      }).catch(function (error) {
        toast(error.message);
        loadPoints();
      });
    });
  }
  function pollChat() {
    clearTimeout(chat.timer);
    var visible = !$("tab-terminal").hidden && chat.view === "chat" && !document.hidden && !chat.composing;
    if (!visible || !chat.space || chat.busy) { chat.timer = setTimeout(pollChat, 1500); return; }
    chat.busy = true;
    var log = $("chat-log");
    var atBottom = log.scrollHeight - log.scrollTop - log.clientHeight < 80;
    var asked = chat.space;
    api("GET", "chat?workspace=" + encodeURIComponent(chat.space) + "&offset=" + chat.offset + "&session=" + encodeURIComponent(chat.session) + "&turns=" + encodeURIComponent(chat.turns || "")).then(function (view) {
      if (asked !== chat.space) return;
      if (view.reset) {
        clearChat();
        atBottom = true;
        if (view.hidden) $("chat-entries").appendChild(el("div", { class: "chat-note", text: view.hidden + " earlier steps aren't shown here; the terminal has the whole session." }));
      }
      chat.session = view.session || "";
      chat.offset = view.offset || 0;
      chat.turns = view.turns || "";
      (view.entries || []).forEach(chatEntry);
      if (!view.session && !$("chat-entries").childNodes.length) $("chat-entries").appendChild(el("div", { class: "chat-note", text: view.note || "No Claude Code session in this workspace yet. Start one from a task, or in the terminal." }));
      chatState(view);
      // The timeline again when its messages, or what Claude is doing, change.
      var agent = view.agent || {};
      var seen = [agent.running, agent.agent, agent.status, view.turns, view.session].join("/");
      if (view.reset || seen !== rewind.seen) { rewind.seen = seen; loadPoints(); if (gitShown()) refreshGit(); }
      if (atBottom) log.scrollTop = log.scrollHeight;
    }).catch(function (error) {
      $("chat-status").textContent = error.message;
    }).then(function () {
      chat.busy = false;
      chat.timer = setTimeout(pollChat, 1500);
    });
  }
  var chatFiles = attacher($("chat-files"), $("chat-input"), $("chat-attached"), true, saveDraft);
  // The message being written, its files and pastes too, is kept in this
  // browser (IndexedDB, which holds files) until it's sent, so a reload or
  // a restart of the browser doesn't lose it.
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
  function saveDraft() {
    if (!chatFiles) return; // not made yet
    clearTimeout(chat.draftTimer);
    chat.draftTimer = setTimeout(function () {
      var draft = chatFiles.snapshot();
      draft.text = $("chat-input").value;
      var empty = !draft.text.trim() && !draft.files.length && !draft.pastes.length;
      draftStore("readwrite").then(function (store) {
        if (empty) store.delete("chat");
        else store.put(draft, "chat");
      }).catch(function () { /* a private window: no draft */ });
    }, 300);
  }
  draftStore("readonly").then(function (store) {
    var got = store.get("chat");
    got.onsuccess = function () {
      var draft = got.result;
      if (!draft || $("chat-input").value) return;
      $("chat-input").value = draft.text || "";
      chatFiles.restore(draft);
      sizeChatInput();
    };
  }).catch(function () { /* a private window: no draft */ });
  function chatSend() {
    var input = $("chat-input");
    var shown = input.value.trim();
    var text = chatFiles.expand(input.value).trim();
    if ((!text && !chatFiles.count()) || $("chat-send").disabled) return;
    $("chat-send").disabled = true;
    chatFiles.read().then(function (files) {
      return api("POST", "chat/send", { workspace: chat.space, text: text, files: files });
    }).then(function () {
      input.value = "";
      chatFiles.clear();
      sizeChatInput();
      var node = el("div", { class: "msg-user pending", text: shown || "Attached files" });
      chat.pending.push({ node: node, since: Date.now() });
      chatPending(chat.agent || {});
      $("chat-entries").insertBefore(node, $("chat-activity"));
      $("chat-log").scrollTop = $("chat-log").scrollHeight;
      pollChat();
    }).catch(function (error) { toast(error.message); }).then(function () { $("chat-send").disabled = false; });
  }
  // Files dropped on the chat's box are attached to the message.
  $("chat-form").addEventListener("dragover", function (event) {
    if (event.dataTransfer && [].indexOf.call(event.dataTransfer.types, "Files") >= 0) event.preventDefault();
  });
  $("chat-form").addEventListener("drop", function (event) {
    if (!event.dataTransfer || !event.dataTransfer.files.length) return;
    event.preventDefault();
    chatFiles.add(event.dataTransfer.files);
  });
  function sizeChatInput() {
    var input = $("chat-input");
    input.style.height = "auto";
    input.style.height = Math.min(200, input.scrollHeight + 2) + "px";
  }
  $("chat-input").addEventListener("input", sizeChatInput);
  $("chat-input").addEventListener("input", saveDraft);
  $("chat-input").addEventListener("keydown", function (event) {
    // Enter sends and Shift+Enter is a new line; on a touch keyboard, Enter is a new line.
    if (event.key === "Enter" && !event.shiftKey && !event.isComposing && !narrow) { event.preventDefault(); chatSend(); }
  });
  $("chat-form").addEventListener("submit", function (event) { event.preventDefault(); chatSend(); });
  // A question in the terminal, as a card at the end of the chat: tap an
  // option, or say it in your own words, and hi presses the keys.
  function chatAsk(ask, screen, agentName) {
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
    var who = el("div", { class: "ask-who" }, [el("span", { text: (agentName === "codex" ? "Codex" : "Claude") + " is asking" + (ask.progress ? " · " + ask.progress : "") })]);
    (ask.tabs || []).forEach(function (tab) { who.appendChild(el("span", { class: "ask-tab" + (tab.done ? " done" : "") + (tab.current ? " current" : ""), "aria-current": tab.current ? "step" : "false", text: (tab.done ? "✓ " : "") + tab.label })); });
    card.appendChild(who);
    (ask.text || []).forEach(function (text, i) {
      card.appendChild(el("p", { class: i === 0 ? "ask-q" : "ask-more" + (/^\$ /.test(text) ? " code" : ""), text: text }));
    });
    var options = el("div", { class: "ask-options" });
    ask.options.forEach(function (option, index) {
      var key = option.box ? (option.checked ? "✓" : "") : (option.key || "→");
      var button = el("button", { type: "button", class: "ask-option", "aria-pressed": option.checked ? "true" : "false" }, [
        el("span", { class: "ask-key", text: key }),
        el("span", {}, [el("span", { class: "ask-label", text: option.label }), option.detail ? el("span", { class: "ask-detail", text: option.detail }) : null]),
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
    ask("Resume this session? hi types claude --resume in the workspace's terminal, so Claude carries on where it stopped.").then(function (answer) {
      if (answer === null) return;
      api("POST", "chat/resume", { workspace: chat.space, session: chat.id }).then(function () {
        toast("Claude is starting in the workspace…");
        setTimeout(pollChat, 1500);
      }).catch(function (error) { toast(error.message); });
    });
  });
  setCodeView(chat.view);

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
    showPreview(null);
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
  function filesShown() { return sideOpen() && side.view === "files"; }
  function openFiles() {
    if (!files.loaded) {
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
    $("files-upload-label").title = "Upload files into /" + files.dir + "; or drop them here";
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
  function gitMarks() {
    var marks = {};
    if (!git.view || git.space !== chat.space || spaceRoot() !== files.root) return marks;
    git.view.staged.concat(git.view.changes, git.view.conflicts || []).forEach(function (file) { marks[file.path] = file.status; });
    return marks;
  }
  function renderTree() {
    var list = $("files-list"), top = list.scrollTop, marks = gitMarks();
    list.textContent = "";
    if (!files.lists[""]) { list.appendChild(el("p", { class: "muted tree-note", text: "Loading…" })); return; }
    if (!files.lists[""].length) list.appendChild(el("p", { class: "muted empty-preview", text: "An empty folder. Upload files into it, or drop them here." }));
    (function rows(dir, depth) {
      (files.lists[dir] || []).forEach(function (entry) {
        var path = joinPath(dir, entry.name), open = entry.dir && files.open[path];
        var mark = marks[path];
        var changed = !entry.dir ? mark : Object.keys(marks).some(function (p) { return p.indexOf(path + "/") === 0; }) ? "dot" : "";
        list.appendChild(el("button", {
          class: "file-row" + (open ? " open" : "") + (path === files.file ? " current" : "") + (entry.dir && path === files.dir ? " target" : ""),
          role: "treeitem", "aria-level": String(depth + 1), "aria-expanded": entry.dir ? String(!!open) : null, "data-path": path,
          title: path + (entry.dir ? "" : " · " + size(entry.size)) + (entry.modified ? " · " + ago(entry.modified) : ""),
          style: "padding-left:" + (8 + depth * 14) + "px",
          onclick: function () { if (entry.dir) toggleDir(path); else { files.dir = dir; target(); openFile(path, entry); } },
        }, [
          el("span", { class: "file-chevron" }, entry.dir ? [icon("chevron").firstChild] : []),
          icon(entry.dir ? "dir" : "file"),
          el("span", { class: "file-name" + (changed && changed !== "dot" ? " " + gitClass(changed) : ""), text: entry.name }),
          changed ? el("span", { class: "file-git " + gitClass(changed === "dot" ? "M" : changed), text: changed === "dot" ? "•" : gitLetter(changed) }) : null,
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
  function showPreview(nodes) {
    var preview = $("files-preview");
    preview.textContent = "";
    $("files-main").classList.toggle("showing-file", !!nodes);
    (nodes || []).forEach(function (node) { if (node) preview.appendChild(node); });
  }
  // widenSide gives a preview or a diff room: half the chat's width, at least.
  function widenSide() {
    var room = Math.round($("chat").clientWidth * 0.55);
    if (side.width >= room) return;
    side.width = room;
    keepSide();
    renderSide();
  }
  // ---- the panel beside the chat: Files, Source control, and Rewind, by
  // the icons along its top.
  function keepSide() {
    try { localStorage.setItem("hi-dev-side", JSON.stringify({ view: side.view, open: side.open, width: side.width })); } catch (e) { /* private window */ }
  }
  function sideOpen() {
    return !$("chat-side").hidden && !$("tab-terminal").hidden && chat.view === "chat" && !document.hidden;
  }
  function renderSide() {
    var starting = !$("chat-start").hidden;
    var panel = $("chat-side");
    panel.hidden = !side.open || starting;
    panel.style.setProperty("--side-width", side.width + "px");
    $("side-toggle").setAttribute("aria-pressed", String(side.open));
    $("side-toggle").hidden = starting || chat.view !== "chat";
    document.querySelectorAll(".side-tabs button[data-side]").forEach(function (button) {
      button.setAttribute("aria-selected", String(button.dataset.side === side.view));
    });
    $("side-files").hidden = side.view !== "files";
    $("side-git").hidden = side.view !== "git";
    $("side-rewind").hidden = side.view !== "rewind";
  }
  // sideShown brings the open view up to date, when it's on the screen.
  function sideShown() {
    if (!sideOpen()) return;
    if (side.view === "files") openFiles();
    if (side.view === "git") refreshGit();
    if (side.view === "rewind") renderPoints();
  }
  function setSide(view, open) {
    if (view) side.view = view;
    if (open != null) side.open = open;
    keepSide();
    renderSide();
    sideShown();
  }
  // revealSide opens a view from anywhere on the page: the Code tab, its
  // chat, the panel.
  function revealSide(view) {
    if ($("tab-terminal").hidden) show("terminal");
    if (chat.view !== "chat") setCodeView("chat");
    setSide(view, true);
  }
  document.querySelectorAll(".side-tabs button[data-side]").forEach(function (button) {
    button.addEventListener("click", function () { setSide(button.dataset.side, true); });
  });
  $("side-close").addEventListener("click", function () { setSide(null, false); });
  $("side-toggle").addEventListener("click", function () { setSide(null, !side.open); });
  // The panel's left edge drags it wider or narrower; a double click puts
  // it back.
  (function () {
    var handle = $("side-resize"), from = 0, width = 0;
    handle.addEventListener("pointerdown", function (event) {
      if (event.button !== 0) return;
      event.preventDefault();
      from = event.clientX;
      width = $("chat-side").getBoundingClientRect().width;
      handle.setPointerCapture(event.pointerId);
      handle.classList.add("dragging");
    });
    handle.addEventListener("pointermove", function (event) {
      if (!handle.classList.contains("dragging")) return;
      var most = Math.max(240, $("chat").clientWidth - 320);
      side.width = Math.round(Math.min(most, Math.max(240, width + from - event.clientX)));
      $("chat-side").style.setProperty("--side-width", side.width + "px");
    });
    function done() {
      if (!handle.classList.contains("dragging")) return;
      handle.classList.remove("dragging");
      keepSide();
    }
    handle.addEventListener("pointerup", done);
    handle.addEventListener("pointercancel", done);
    handle.addEventListener("dblclick", function () { side.width = 340; keepSide(); renderSide(); });
  })();
  // A narrow screen's panel lies over the chat: tapping the chat closes it.
  $("chat-log").addEventListener("click", function () { if (sideOver.matches && side.open) setSide(null, false); });

  // ---- Source control: the chat's workspace's changes, staged or not,
  // each file's diff, a commit, and its branch's commits. Try on Stage and
  // Ship to Dev too, once it has commits dev hasn't.
  function gitShown() { return sideOpen() && side.view === "git"; }
  function refreshGit() {
    if (!chat.space || git.busy) return;
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
    if (gitShown() && Date.now() - git.at > 4000) refreshGit();
    else if (filesShown() && files.loaded && Date.now() - files.at > 8000) {
      // The tree, and its letters, as the agent changes files.
      if (Date.now() - git.at > 15000) refreshGit(); else loadTree();
    }
  }, 1000);
  function count(n, one, many) { return n + " " + (n === 1 ? one : many); }
  function gitLetter(status) { return status === "?" ? "U" : status; }
  function gitClass(status) { return status === "?" ? "git-new" : status === "!" ? "git-conflict" : "git-" + status; }
  var gitWords = { M: "Changed", A: "Added", D: "Deleted", R: "Renamed", C: "Copied", T: "Changed its kind", "?": "New: git doesn't know it yet", "!": "A conflict, to resolve" };
  function gitBusyWords() {
    var agent = chat.agent || {};
    return agent.status === "working" ? " Claude is at work here: it may be changing these files right now." : "";
  }
  function renderGit() {
    var view = git.view && git.space === chat.space ? git.view : null;
    var space = chatSpace() || {};
    var lists = $("git-lists");
    $("git-error").textContent = git.error;
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
    if (!space.path) where.push("The project's own clone");
    where.push(view.ahead ? count(view.ahead, "commit", "commits") + " dev hasn't" : view.branch === view.base ? "dev itself" : "Nothing committed that dev hasn't");
    if (view.behind) where.push("dev has " + count(view.behind, "commit", "commits") + " it hasn't");
    $("git-base").textContent = where.join(" · ");
    var ship = $("git-ship");
    ship.textContent = "";
    if (space.path && space.branch && view.ahead > 0) {
      var shipping = Object.assign({}, space, { ahead: view.ahead, dirty: all > 0 });
      ship.appendChild(tryButton(shipping));
      ship.appendChild(shipButton(shipping));
    }
    var commit = $("git-commit");
    commit.textContent = view.staged.length ? "Commit" : "Commit all";
    commit.disabled = !(view.staged.length || view.changes.length);
    lists.textContent = "";
    if (view.conflicts && view.conflicts.length) lists.appendChild(gitSection("Conflicts", view.conflicts, "conflict"));
    if (view.staged.length) lists.appendChild(gitSection("Staged", view.staged, "staged"));
    if (view.changes.length || !view.staged.length) lists.appendChild(gitSection("Changes", view.changes, "changes"));
    if (!all) lists.appendChild(el("p", { class: "muted small", text: "Nothing to commit: every change is in a commit." }));
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
        gitDo("discard", paths, "Discard every change not staged, " + count(list.length, "file", "files") + "? Each goes back as the last commit has it, staged changes to it too; a new file is deleted. There's no undo." + gitBusyWords());
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
    var words = file.status === "?" ? "Delete " + file.path + "? It's new, and git hasn't it, so it's gone for good."
      : "Discard the changes to " + file.path + "? It goes back as the last commit has it, staged changes too. There's no undo.";
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
    if (!message) { $("git-error").textContent = "Say what the commit does, in its message."; $("git-message").focus(); return; }
    var busy = gitBusyWords();
    gitDo("commit", [], busy ? "Commit what's there now?" + busy : "");
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
      file.status !== "D" ? el("button", { class: "side-icon", text: "↗", title: "Open it in Files", "aria-label": "Open it in Files", onclick: function () { openPath(file.path); } }) : null,
      el("button", { class: "side-icon", text: "⤢", title: "Wider", "aria-label": "Wider", onclick: widenSide }),
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
      text.replace(/\n$/, "").split("\n").forEach(function (line) {
        var cls = /^(diff |index |--- |\+\+\+ |new file|deleted file|similarity|rename |old mode|new mode|Binary )/.test(line) ? "meta"
          : line[0] === "@" ? "hunk" : line[0] === "+" ? "add" : line[0] === "-" ? "del" : "";
        body.appendChild(el("div", { class: cls, text: line || " " }));
      });
    }).catch(function (error) {
      if (!git.diff || git.diff.key !== key) return;
      body.textContent = "";
      body.appendChild(el("div", { class: "meta", text: error.message }));
    });
    renderGit();
  }
  function closeDiff() {
    git.diff = null;
    $("git-diff").hidden = true;
    $("git-diff").textContent = "";
    $("side-git").classList.remove("showing-diff");
  }
  renderSide();
  renderGit();

  function openFile(path, entry) {
    files.file = path;
    var name = path.split("/").pop();
    var raw = "api/files/raw?" + fileQuery(path);
    var head = el("div", { class: "preview-head" }, [
      el("button", { class: "side-icon", text: "←", title: "Back to the files", "aria-label": "Back to the files", onclick: function () { files.file = ""; showPreview(null); renderTree(); } }),
      el("strong", { class: "preview-name", text: name, title: path }),
      el("span", { class: "muted small", text: entry ? size(entry.size) : "" }),
      el("span", { class: "grow" }),
      el("button", { class: "side-icon", text: "⤢", title: "Wider", "aria-label": "Wider", onclick: widenSide }),
      el("a", { class: "button-like", href: raw + "&download=1", download: name, text: "Download" }),
    ]);
    var body = el("div", { class: "preview-body" }, [el("p", { class: "muted", text: "Opening " + name + "…" })]);
    showPreview([head, body]);
    var ext = (name.match(/\.([a-z0-9]+)$/i) || [])[1];
    ext = (ext || "").toLowerCase();
    if (["png", "jpg", "jpeg", "gif", "webp", "avif", "svg"].indexOf(ext) >= 0 && ext !== "svg") {
      body.textContent = "";
      body.appendChild(el("img", { class: "preview-image", src: raw, alt: name }));
      return;
    }
    if (ext === "pdf") {
      body.textContent = "";
      body.appendChild(el("p", {}, [el("a", { href: raw, target: "_blank", rel: "noopener", text: "Open the PDF in a new tab" })]));
      return;
    }
    fetch(raw, { credentials: "same-origin" }).then(function (response) {
      return response.text().then(function (text) {
        if (!response.ok) throw new Error(text.trim() || response.statusText);
        return text;
      });
    }).then(function (text) {
      if (files.file !== path) return;
      body.textContent = "";
      if (ext === "md" || ext === "markdown") {
        var dir = path.split("/").slice(0, -1).join("/");
        body.appendChild(renderMarkdown(text, dir));
      } else {
        body.appendChild(el("pre", { class: "preview-text", text: text }));
      }
    }).catch(function (error) {
      body.textContent = "";
      body.appendChild(el("p", { class: "muted", text: error.message }));
    });
  }

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
        box.appendChild(el("pre", {}, [el("code", { text: code.join("\n") })]));
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
    revealSide("files");
    var parts = path.split("/"), dir = parts.slice(0, -1).join("/"), name = parts[parts.length - 1];
    (function wait(tries) {
      if (!files.root || !files.lists[""]) { if (tries < 40) setTimeout(function () { wait(tries + 1); }, 100); return; }
      for (var i = 1; i < parts.length; i++) files.open[parts.slice(0, i).join("/")] = true;
      keepFiles();
      loadTree().then(function () {
        var entry = (files.lists[dir] || []).filter(function (e) { return e.name === name; })[0];
        if (!entry) { toast(path + " isn't there."); return; }
        files.dir = entry.dir ? path : dir;
        if (entry.dir) { if (!files.open[path]) toggleDir(path); } else openFile(path, entry);
        target();
        var row = document.querySelector('#files-list [data-path="' + CSS.escape(path) + '"]');
        if (row) row.scrollIntoView({ block: "center" });
      });
    })(0);
  }

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
          return ask(data.exists + " is there already. Replace it, and any other file here with the same name?").then(function (answer) {
            if (answer === null) { toast(written.length ? "Uploaded " + written.join(", ") + "; " + data.exists + " stays as it was." : data.exists + " stays as it was."); return; }
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
            reader.onerror = function () { reject(new Error("couldn't read " + file.name)); };
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
  $("ticket-new").addEventListener("click", function () {
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
      toast("Filed task #" + ticket.number + ".");
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
    meta.textContent = [ticket.state, ticket.asked && ticket.asked !== ticket.author ? "asked by " + ticket.asked + (ticket.author ? " (filed as " + ticket.author + ")" : "") : ticket.author, when(ticket.created), ticket.edited ? "edited " + when(ticket.edited) : ""].filter(Boolean).join(" · ");
    if (ticket.url) meta.appendChild(el("span", {}, [document.createTextNode(" · "), el("a", { href: ticket.url, target: "_blank", rel: "noopener", text: "Open where it's kept" })]));
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
          removeFromTicket(ticket.number, { name: name }, "Remove " + name + " from task #" + ticket.number + "? It can't be brought back.", "Removed " + name + ".");
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
        removeFromTicket(ticket.number, { comment: comment.id }, "Delete this comment from task #" + ticket.number + (files.length ? ", with " + files.join(", ") : "") + "? It can't be brought back.", "Deleted the comment.");
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
    $("view-body").textContent = "Fetching the task…";
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
        "No decisions yet. The agents write one for a technical choice someone shouldn't undo by accident, and so can you." }));
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
      "Start with its title as a heading, then what was decided: the card shows that paragraph.";
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
      toast(decision ? "Saved, on the app's branch." : "Added " + result.file + " to the app's branch.");
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
    ask("Delete the decision \u201c" + decision.title + "\u201d? It goes from the app's branch; git keeps its history.").then(function (answer) {
      if (answer === null) return;
      api("POST", "decision", { file: decision.file, blob: decision.blob, delete: true }).then(function () {
        toast("Deleted " + decision.file + ".");
        $("decision-dialog").close();
        refreshDecisions();
      }).catch(function (error) { $("decision-error").textContent = error.message; });
    });
  });

  // On a phone, the workspaces start folded, and the terminal has the room.
  if (window.matchMedia && window.matchMedia("(max-width: 900px)").matches) $("spaces-box").open = false;
  var tab = "tasks";
  try { tab = localStorage.getItem("hi-dev-tab") || "tasks"; } catch (e) { /* private window */ }
  if (tab === "files") { tab = "terminal"; side.view = "files"; side.open = true; } // the Files tab, before it moved beside the chat
  show(["tasks", "decisions", "terminal", "release", "system"].indexOf(tab) >= 0 ? tab : "tasks");
  refresh().then(function () {
    if ($("locked").hidden && !vibe()) connect();
  });
  refreshTickets(false);
  setInterval(function () { if (!document.hidden) refresh(); }, 5000);
  setInterval(function () { if (!document.hidden) refreshTickets(false); }, 30000);
})();
