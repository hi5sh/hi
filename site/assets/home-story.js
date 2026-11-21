// The demo at the top of hi5.sh (index.html), played by story.js: the
// tagline over the banner, then hi app's demo (assets/demos/app.js, loaded
// before this), wider, as the page's first screen. docs/feature-pages.md
// says how to edit the scenes.
(() => {
  "use strict";
  const app = window.HI5_STORY;
  window.HI5_STORY = Object.assign({}, app, {
    size: { w: 760, h: 520 },
    narrow: { w: 400, h: 680 },
    scenes: [
      {
        id: "title", title: "Think it. Ship it.", short: 4800, screen: "title", image: "/assets/hi5-banner.jpg",
        kicker: "THE AGENT-ASSISTED SOFTWARE FACTORY", tagline: "Think it. Ship it.",
        lede: "Internal apps built by AI agents, from idea to production. On your infrastructure, your stack and your data.",
      },
      ...app.scenes,
    ],
  });
})();
