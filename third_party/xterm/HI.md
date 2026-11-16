# xterm.js v5.5.0 and its add-ons

Copies of `lib/xterm.js` and `css/xterm.css` from
[@xterm/xterm](https://github.com/xtermjs/xterm.js) v5.5.0, and of each
add-on's `lib/addon-*.js` from the releases made with it (2024-04-05),
without their source map lines (MIT, see LICENSE):

- @xterm/addon-fit v0.10.0: the terminal fills its panel.
- @xterm/addon-webgl v0.18.0: drawing on the GPU.
- @xterm/addon-search v0.15.0: search in the scrollback.
- @xterm/addon-unicode11 v0.8.0: Unicode 11's widths, for emoji and CJK.
- @xterm/addon-web-links v0.11.0: links that open.
- @xterm/addon-image v0.8.0: pictures in the terminal (iTerm's inline
  images; its sixel decoder needs WebAssembly, which the page's policy
  doesn't allow, so it's off).

The terminal of a hi app's dev page. hi embeds them and serves them with
the page (app_devpage.go), so the page loads nothing from elsewhere.
