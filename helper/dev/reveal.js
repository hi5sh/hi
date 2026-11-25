// An eye on every field that hides what's typed (a key, a token, a
// password), on the admin page and the dev page: a click shows the value,
// to check a key you pasted, and hides it again. Fields the pages add
// later get one too. The value is shown only in this field, on this
// screen; hi never shows a saved value.
(function () {
  "use strict";
  var EYE = '<svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M2 12s3.6-7 10-7 10 7 10 7-3.6 7-10 7S2 12 2 12z"/><circle cx="12" cy="12" r="3"/></svg>';
  var EYE_OFF = '<svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M9.9 4.2A10.4 10.4 0 0 1 12 4c6.4 0 10 8 10 8a17.6 17.6 0 0 1-2.6 3.8"/><path d="M6.6 6.6C3.7 8.4 2 12 2 12s3.6 8 10 8a9.7 9.7 0 0 0 5.4-1.6"/><path d="M14.1 14.2a3 3 0 0 1-4.2-4.3"/><path d="m2 2 20 20"/></svg>';
  function show(button, input, shown) {
    input.type = shown ? "text" : "password";
    button.innerHTML = shown ? EYE_OFF : EYE;
    button.title = shown ? "Hide the value" : "Show the value";
    button.setAttribute("aria-label", button.title);
    button.setAttribute("aria-pressed", String(shown));
  }
  function add(input) {
    if (input.dataset.reveal) return;
    input.dataset.reveal = "1";
    var wrap = document.createElement("span");
    wrap.className = "reveal";
    input.parentNode.insertBefore(wrap, input);
    wrap.appendChild(input);
    var button = document.createElement("button");
    button.type = "button";
    button.className = "reveal-btn";
    show(button, input, false);
    // Not a focus stop of the form's: Tab goes from the field to the next.
    button.tabIndex = -1;
    button.addEventListener("mousedown", function (event) { event.preventDefault(); });
    button.addEventListener("click", function () {
      show(button, input, input.type === "password");
      input.focus();
    });
    // A form that's reset or sent hides it again.
    if (input.form) input.form.addEventListener("submit", function () { setTimeout(function () { show(button, input, false); }); });
    wrap.appendChild(button);
  }
  function scan(root) {
    if (root.matches && root.matches('input[type="password"]')) add(root);
    if (root.querySelectorAll) [].forEach.call(root.querySelectorAll('input[type="password"]'), add);
  }
  function start() {
    scan(document.body);
    new MutationObserver(function (changes) {
      changes.forEach(function (change) { [].forEach.call(change.addedNodes, function (node) { if (node.nodeType === 1) scan(node); }); });
    }).observe(document.body, { childList: true, subtree: true });
  }
  if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", start);
  else start();
})();
