// The page's theme: Orca's colours (stablyai/orca, MIT) by default, or
// one of Paseo's themes (getpaseo/paseo, Apache-2.0), kept in this browser. Loaded in the head, before the page draws,
// so a theme other than the default never flashes in; dev.css has the
// colours, by the <html>'s data-theme. The admin page and the dev page
// share it: the bar's button (#theme-btn) opens the list, and their
// terminals take their colours from hiTheme.terminal().
(function () {
  "use strict";
  var KEY = "hi-theme";
  // Each theme's name, its swatch (Paseo's), and whether it's light.
  var THEMES = [
    { id: "auto", name: "Auto", swatch: "", hint: "Light or dark, as the computer is" },
    { id: "orca", name: "Orca", swatch: "#2a2a2a" },
    { id: "claude", name: "Claude", swatch: "#d97757" },
    { id: "paseo", name: "Paseo", swatch: "#2d8b62" },
    { id: "zinc", name: "Zinc", swatch: "#808080" },
    { id: "midnight", name: "Midnight", swatch: "#4a6ba8" },
    { id: "ghostty", name: "Ghostty", swatch: "#8caaee" },
    { id: "black", name: "Pure black", swatch: "#000000" },
    { id: "light", name: "Light", swatch: "#ffffff", light: true },
  ];
  // The terminal's colours: Paseo's ANSI sets, one dark, one light. On
  // dark, bright black is lighter than Paseo's, as agents print their
  // quieter lines in it; on light, white and bright white are grays and
  // bright yellow darker, so what's printed in them shows on white.
  var DARK_ANSI = {
    red: "#e07070", green: "#5dba80", yellow: "#d4a44a", blue: "#6a9de0", magenta: "#b07ad0", cyan: "#4aabb8", white: "#d4d4d8",
    brightBlack: "#7a7f7d", brightRed: "#e89090", brightGreen: "#7ecf9a", brightYellow: "#e0be6e", brightBlue: "#8ab4e8",
    brightMagenta: "#c49ae0", brightCyan: "#6ec2cc", brightWhite: "#f0f0f2",
  };
  var LIGHT_ANSI = {
    red: "#dc2626", green: "#16a34a", yellow: "#ca8a04", blue: "#2563eb", magenta: "#9333ea", cyan: "#0891b2", white: "#71717a",
    brightBlack: "#52525b", brightRed: "#ef4444", brightGreen: "#22c55e", brightYellow: "#d97706", brightBlue: "#3b82f6",
    brightMagenta: "#a855f7", brightCyan: "#06b6d4", brightWhite: "#3f3f46",
  };
  var listeners = [];
  var system = window.matchMedia ? window.matchMedia("(prefers-color-scheme: light)") : null;

  function chosen() {
    var id = "";
    try { id = localStorage.getItem(KEY) || ""; } catch (e) { /* a private window */ }
    return THEMES.some(function (t) { return t.id === id; }) ? id : "orca";
  }
  // The theme drawn: Auto is Light or Orca, as the computer is.
  function shown(id) {
    if (id === "auto") return system && system.matches ? "light" : "orca";
    return id;
  }
  function apply() {
    var id = shown(chosen());
    var root = document.documentElement;
    if (id === "claude") root.removeAttribute("data-theme"); // dev.css's own
    else root.setAttribute("data-theme", id);
    var meta = document.querySelector('meta[name="theme-color"]');
    if (meta && document.body) meta.setAttribute("content", css("--surface0") || meta.getAttribute("content"));
    listeners.forEach(function (fn) { try { fn(); } catch (e) { /* a listener's own */ } });
  }
  function css(name) {
    return getComputedStyle(document.documentElement).getPropertyValue(name).trim();
  }
  function isLight() {
    return shown(chosen()) === "light";
  }
  function set(id) {
    try { localStorage.setItem(KEY, id); } catch (e) { /* a private window: this page only */ }
    apply();
  }

  window.hiTheme = {
    themes: THEMES,
    current: chosen,
    set: set,
    onChange: function (fn) { listeners.push(fn); },
    // xterm's theme, in the page's colours.
    terminal: function () {
      var bg = css("--surface0") || "#151515";
      var fg = css("--fg") || "#fafafa";
      var light = isLight();
      var theme = { background: bg, foreground: light ? fg : css("--text") || fg, cursor: fg, cursorAccent: bg,
        selectionBackground: light ? "rgba(0, 0, 0, 0.15)" : "rgba(255, 255, 255, 0.2)", black: light ? fg : css("--sidebar") || "#1a1918" };
      var ansi = light ? LIGHT_ANSI : DARK_ANSI;
      for (var k in ansi) theme[k] = ansi[k];
      return theme;
    },
  };
  apply();
  if (system && system.addEventListener) system.addEventListener("change", function () { if (chosen() === "auto") apply(); });
  // Another window of the page changed it.
  window.addEventListener("storage", function (event) { if (event.key === KEY) apply(); });

  // The picker: a button in the bar, a list of the themes under it.
  function picker() {
    var button = document.getElementById("theme-btn");
    if (!button) return;
    var menu = document.createElement("div");
    menu.id = "theme-menu";
    menu.className = "theme-menu";
    menu.setAttribute("role", "menu");
    menu.hidden = true;
    button.parentNode.appendChild(menu);
    function draw() {
      menu.textContent = "";
      var head = document.createElement("div");
      head.className = "theme-head";
      head.textContent = "Theme";
      menu.appendChild(head);
      var now = chosen();
      THEMES.forEach(function (theme) {
        var item = document.createElement("button");
        item.type = "button";
        item.className = "theme-item";
        item.setAttribute("role", "menuitemradio");
        item.setAttribute("aria-checked", theme.id === now ? "true" : "false");
        item.dataset.theme = theme.id;
        var swatch = document.createElement("span");
        swatch.className = "theme-swatch" + (theme.id === "auto" ? " auto" : "");
        if (theme.swatch) swatch.style.background = theme.swatch;
        var name = document.createElement("span");
        name.className = "theme-name";
        name.textContent = theme.name;
        var check = document.createElement("span");
        check.className = "theme-check";
        check.textContent = theme.id === now ? "✓" : "";
        item.appendChild(swatch);
        item.appendChild(name);
        item.appendChild(check);
        if (theme.hint) item.title = theme.hint;
        item.addEventListener("click", function () { set(theme.id); draw(); });
        menu.appendChild(item);
      });
    }
    function close() {
      menu.hidden = true;
      button.setAttribute("aria-expanded", "false");
    }
    button.setAttribute("aria-haspopup", "menu");
    button.setAttribute("aria-expanded", "false");
    button.addEventListener("click", function (event) {
      event.stopPropagation();
      var open = menu.hidden;
      if (open) draw();
      menu.hidden = !open;
      button.setAttribute("aria-expanded", open ? "true" : "false");
      if (open) {
        var first = menu.querySelector('[aria-checked="true"]') || menu.querySelector(".theme-item");
        if (first) first.focus();
      }
    });
    menu.addEventListener("click", function (event) { event.stopPropagation(); });
    menu.addEventListener("keydown", function (event) {
      var items = [].slice.call(menu.querySelectorAll(".theme-item"));
      var at = items.indexOf(document.activeElement);
      if (event.key === "ArrowDown" || event.key === "ArrowUp") {
        event.preventDefault();
        items[(at + (event.key === "ArrowDown" ? 1 : items.length - 1)) % items.length].focus();
      } else if (event.key === "Escape") {
        close();
        button.focus();
      }
    });
    document.addEventListener("click", close);
  }
  if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", function () { apply(); picker(); });
  else picker();
})();
