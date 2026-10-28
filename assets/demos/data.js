// The demo at the top of hi5.sh/data/, played by assets/story.js. Every line
// hi prints is what it printed in a real run; the server, the organization,
// the datasets, and the folder are made up, and hf's own update hint is left
// out. docs/feature-pages.md says how to edit it.
window.HI5_STORY = {
  size: { w: 580, h: 490 },
  narrow: { w: 400, h: 680 },
  pace: { short: 1 },
  people: {},
  scenes: [
    {
      id: "ls", title: "What you may download", caption: "Your team's data. No token here.",
      short: 10000, screen: "terminal", window: "you@laptop: ~/prices",
      lines: [
        { cmd: "hi data ls --kind dataset", at: 300 },
        { out: "KIND     NAME                     SIZE      UPDATED", pre: true, at: 1500 },
        { out: "dataset  example-org/news         19.0 GB   55 days ago", pre: true, at: 1600 },
        { out: "dataset  example-org/news-scores  304.4 MB  30 days ago", pre: true, at: 1700 },
        { out: "dataset  example-org/prices-1d    76.5 MB   6 months ago", pre: true, at: 1800 },
        { out: "dataset  example-org/filings      22.7 GB   41 days ago", pre: true, at: 1900 },
        { cmd: "hi data info example-org/prices-1d", at: 4200 },
        { out: "dataset example-org/prices-1d", at: 5200 },
        { out: "  private", at: 5300 },
        { out: "  3 files, 76.5 MB", at: 5400 },
        { out: "  updated 6 months ago, at e47e3b0", at: 5500 },
        { out: "Download it with `hi data get example-org/prices-1d`.", at: 5700 },
      ],
    },
    {
      id: "get", title: "Download it, and record it", caption: "The project remembers the exact commit.",
      short: 12000, screen: "terminal", window: "you@laptop: ~/prices",
      lines: [
        { cmd: "hi data get example-org/prices-1d", at: 300 },
        { out: "Downloading dataset example-org/prices-1d at e47e3b0 into data/prices-1d through http://hi.example.com:7373", at: 1500 },
        { ok: "Downloaded example-org/prices-1d into data/prices-1d", at: 4000 },
        { out: "Recorded in .hi5/data.json; `hi data get` there fetches the same files again.", at: 4200 },
        { cmd: "cat .hi5/data.json", at: 6000 },
        { out: "{\n  \"data\": [\n    {\n      \"kind\": \"dataset\",\n      \"id\": \"example-org/prices-1d\",\n      \"revision\": \"e47e3b0e46bbb023ff8da38eded7e727c784de75\",\n      \"to\": \"data/prices-1d\",\n      \"fetched\": \"2026-10-21T14:59:28.123005077Z\"\n    }\n  ]\n}", pre: true, at: 7000 },
      ],
    },
    {
      id: "box", title: "Even in a box", caption: "A placeholder inside; the real token stays out.",
      short: 12000, screen: "terminal", window: "you@laptop: ~/prices", prompt: "box@prices-1:/home/you/prices$",
      lines: [
        { cmd: "hi box shell --data", ps: "$", at: 300 },
        { out: "Box prices-1: shell in /home/you/prices, network dev, hi data through the server.", at: 1500 },
        { cmd: "echo $HF_TOKEN", at: 2800 },
        { out: "hi-box-placeholder", at: 3600 },
        { cmd: "uvx -q --from huggingface_hub hf download --repo-type dataset example-org/prices-1d README.md --local-dir ~/x", at: 5000 },
        { ok: "  path: /box/home/x/README.md", at: 8000 },
      ],
    },
  ],
};
