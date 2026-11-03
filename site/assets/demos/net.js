// The demo at the top of hi5.sh/net/, played by assets/story.js. Every line
// a command prints is what hi net expose printed in a real run; the address,
// the password, the key, and the folder are made up.
// docs/feature-pages.md says how to edit it.
window.HI5_STORY = {
  size: { w: 580, h: 440 },
  narrow: { w: 510, h: 620 },
  pace: { short: 1 },
  people: {},
  scenes: [
    {
      id: "share", title: "Share it, behind a password", caption: "A public address, with a lock.",
      short: 12000, screen: "terminal", window: "you@laptop: ~/web",
      lines: [
        { cmd: "hi net expose 3000 --detach", at: 300 },
        { out: "Exposing localhost:3000 at https://hi-laptop-3000-k2x7.eu1.netbird.services", at: 1800 },
        { warn: "  password: plum-otter-raven-42 (a login page asks for it)", at: 2000 },
        { out: "  until 15:02, in the background; stop it with: hi net expose stop hi-laptop-3000-k2x7", at: 2200 },
        { out: "  requests are logged in /home/you/.local/state/hi/net/expose/hi-laptop-3000-1792594671.log", at: 2400 },
        { out: "# anyone without the password", at: 4400 },
        { cmd: "curl -so /dev/null -w '%{http_code}\\n' https://hi-laptop-3000-k2x7.eu1.netbird.services", at: 4600 },
        { err: "401", at: 6000 },
      ],
    },
    {
      id: "link", title: "Or the key in the link", caption: "No login page. The link is the key.",
      short: 13000, screen: "terminal", window: "you@laptop: ~/web",
      lines: [
        { cmd: "hi net expose 3000 --link --detach", at: 300 },
        { out: "Exposing localhost:3000 at https://hi-laptop-3000-3nf1.eu1.netbird.services", at: 1800 },
        { ok: "  share this link; its key lets you in: https://hi-laptop-3000-3nf1.eu1.netbird.services/?hi_key=Qe4-nT8vWzL2hKpXa9RmBw", at: 2000 },
        { out: "  until 15:08, in the background; stop it with: hi net expose stop hi-laptop-3000-3nf1", at: 2200 },
        { out: "  requests are logged in /home/you/.local/state/hi/net/expose/hi-laptop-3000-1792594709.log", at: 2400 },
        { cmd: "cat ~/.local/state/hi/net/expose/hi-laptop-3000-1792594709.log", at: 5200 },
        { err: "GET    /                                        401  no key", pre: true, at: 6300 },
        { out: "GET    /                                        200  1 ms", pre: true, at: 7300 },
        { out: "GET    /prices.json                             404  1 ms", pre: true, at: 8300 },
      ],
    },
    {
      id: "end", title: "It ends", caption: "At --max, or when you say so.",
      short: 9000, screen: "terminal", window: "you@laptop: ~/web",
      lines: [
        { cmd: "hi net expose stop hi-laptop-3000-3nf1", at: 300 },
        { ok: "Stopped hi-laptop-3000-3nf1 (https://hi-laptop-3000-3nf1.eu1.netbird.services).", at: 1500 },
        { cmd: "hi net expose ls", at: 3200 },
        { out: "Nothing is exposed from this machine.", at: 4200 },
      ],
    },
  ],
};
