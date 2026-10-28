// The demo at the top of hi5.sh/server/, played by assets/story.js. Every line
// a command prints is what hi printed in a real run on a device connected to
// a hi server; the server's name, the people, and the request names are
// made up. docs/feature-pages.md says how to edit it.
window.HI5_STORY = {
  size: { w: 580, h: 440 },
  narrow: { w: 400, h: 600 },
  pace: { short: 1 },
  people: {},
  scenes: [
    {
      id: "status", title: "One server for the team", caption: "The keys stay on the server.",
      short: 10000, screen: "terminal", window: "sam@laptop: ~",
      lines: [
        { cmd: "hi connect status", at: 300 },
        { ok: "Connected to http://hi.example.com:7373 as sam (staff).", at: 1500 },
        { out: "Managed by the server: runpod, shadeform. Starting there needs an approval.", at: 1700 },
        { out: "Colab and any other provider still use your own sign-in.", at: 1900 },
        { out: "This month: $0.01 of sam's $300.00 · staff $0.01 of $2000.00", at: 2100 },
      ],
    },
    {
      id: "approve", title: "Nothing starts without a yes", caption: "Every paid start is approved.",
      short: 13000, screen: "terminal", window: "sam@laptop: ~",
      lines: [
        { cmd: "hi compute ls", at: 300 },
        { out: "No instances are running.", at: 1300 },
        { out: " ", at: 1400 },
        { out: "runpod is managed by http://hi.example.com:7373: you are sam (staff), and starting needs an approval.", at: 1500 },
        { out: "This month: $0.01 of sam's $300.00 · staff $0.01 of $2000.00", at: 1700 },
        { out: " ", at: 1800 },
        { out: "shadeform is managed by http://hi.example.com:7373: you are sam (staff), and starting needs an approval.", at: 1900 },
        { out: "This month: $0.01 of sam's $300.00 · staff $0.01 of $2000.00", at: 2100 },
        { cmd: "hi compute requests", at: 4200 },
        { out: "ID        STATE    NAME         HARDWARE               MAX  SENT", pre: true, at: 5200 },
        { out: "r-5273fb  stopped  data-check   runpod/rtx-2000-ada    30m  333h29m ago", pre: true, at: 5300 },
        { out: "r-992934  stopped  sweep-a      shadeform/a4000        30m  504h04m ago", pre: true, at: 5400 },
        { out: "r-d90b7f  stopped  cpu-test     runpod/cpu3c           30m  504h04m ago", pre: true, at: 5500 },
        { out: "r-f76b55  stopped  lora-run     runpod/l40s@community  10m  528h27m ago", pre: true, at: 5600 },
        { err: "r-115ab5  failed   lora-run     runpod/rtx-2000-ada    30m  530h25m ago", pre: true, at: 5700 },
      ],
    },
    {
      id: "templates", title: "The team's own templates", caption: "Private templates, without GitHub access.",
      short: 11000, screen: "terminal", window: "sam@laptop: ~",
      lines: [
        { cmd: "hi init --list", at: 300 },
        { out: "TEMPLATE         SOURCE           SUMMARY", pre: true, at: 1300 },
        { ok: "analysis         private c4a9263  The team's analysis project, with its checks", pre: true, at: 1400 },
        { out: "app              hi v2.5.55       Web app on Bun alone (Bun.serve, React, SQLite, bun test)", pre: true, at: 1500 },
        { out: "autoresearch-ml  hi v2.5.55       Autoresearch on model training: train.py trains for a fixed time, a fixed evaluation scores it, and rounds keep only gains", pre: true, at: 1600 },
        { out: "ml               hi v2.5.55       Model training (PyTorch on CPU, CUDA, or ROCm)", pre: true, at: 1700 },
        { out: "pipeline         hi v2.5.55       Data pipeline: fetch, land raw, parse, load (idempotent per date)", pre: true, at: 1800 },
        { out: "python           hi v2.5.55       Python library or scripts (uv, ruff, pyrefly, pytest)", pre: true, at: 1900 },
        { out: "service          hi v2.5.55       Backend service or API (FastAPI, SQLAlchemy, Alembic)", pre: true, at: 2000 },
        { out: "web              hi v2.5.55       Web app (React and TypeScript on Vite, Bun, Biome, Vitest)", pre: true, at: 2100 },
      ],
    },
  ],
};
