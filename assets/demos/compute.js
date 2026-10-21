// The demo at the top of hi5.sh/compute/, played by assets/story.js. Every
// line a command prints is what hi compute printed in a real run; the account
// (example-org), the hi server's address, and the people are made up. The
// starts are --dry-run, which shows the request without sending it.
// docs/feature-pages.md says how to edit it.
window.HI5_STORY = {
  size: { w: 580, h: 440 },
  narrow: { w: 400, h: 600 },
  pace: { short: 1 },
  people: {},
  scenes: [
    {
      id: "hardware", title: "What's there, and what it costs", caption: "Four providers. One list.",
      short: 12000, screen: "terminal", window: "you@laptop: ~/train",
      lines: [
        { cmd: "hi compute providers", at: 300 },
        { ok: "colab    ready", at: 1200 },
        { ok: "hf       ready", at: 1300 },
        { out: "runpod   managed by http://hi.example.com:7373", at: 1400 },
        { out: "shadeform managed by http://hi.example.com:7373", at: 1500 },
        { cmd: "hi compute hardware --on hf", at: 3000 },
        { out: "PROVIDER  HARDWARE         KIND  MEMORY            RATE", pre: true, at: 4000 },
        { out: "hf        t4-small         GPU   16 GB VRAM        $0.40/h", pre: true, at: 4200 },
        { out: "hf        a10g-small       GPU   24 GB VRAM        $1.00/h", pre: true, at: 4400 },
        { out: "hf        l40sx1           GPU   48 GB VRAM        $1.80/h", pre: true, at: 4600 },
        { out: "hf        a100-large       GPU   80 GB VRAM        $2.50/h", pre: true, at: 4800 },
        { out: "hf        rtx-pro-6000     GPU   96 GB VRAM        $2.75/h", pre: true, at: 5000 },
        { out: "hf        h200             GPU   141 GB VRAM       $5.00/h", pre: true, at: 5200 },
        { out: "…", at: 5400 },
      ],
    },
    {
      id: "run", title: "A run that stops by itself", caption: "Every machine has a time limit.",
      short: 12000, screen: "terminal", window: "you@laptop: ~/train",
      lines: [
        { cmd: "hi compute run --dry-run --gpu t4-small --max 30m train.py", at: 300 },
        { warn: "Run train.py on hf t4-small ($0.40/h, billed to example-org), stopping after 30m at 15:29.", at: 1600 },
        { out: "Would run: POST https://huggingface.co/api/jobs/<your account> {\"arguments\":[],\"command\":[\"bash\",\"-c\",\"echo \\\"$HI_SCRIPT_B64\\\" | base64 -d > /tmp/script.py && unset HI_SCRIPT_B64 && exec uv run /tmp/script.py \\\"$@\\\"\",\"hi\"],\"dockerImage\":\"ghcr.io/astral-sh/uv:python3.12-bookworm\",…,\"flavor\":\"t4-small\",\"timeoutSeconds\":1800}", at: 2600 },
        { cmd: "hi compute ls", at: 6000 },
        { out: "No instances are running.", at: 7000 },
        { out: " ", at: 7100 },
        { out: "Colab balance: 1791.85 compute units, currently using 0.00 units/h", at: 7200 },
      ],
    },
    {
      id: "team", title: "A team's GPUs, with a yes", caption: "The server holds the keys and the budget.",
      short: 13000, screen: "terminal", window: "you@laptop: ~/train",
      lines: [
        { cmd: "hi compute up --dry-run --gpu rtx-4090 --max 2h", at: 300 },
        { warn: "Start runpod/rtx-4090-0f16 on rtx-4090 ($0.89/h), stopping after 2h at 16:59.", at: 1500 },
        { out: "Would run: POST http://hi.example.com:7373/v1/requests {\"provider\":\"runpod\",\"hardware\":\"rtx-4090\",\"name\":\"rtx-4090-0f16\",\"max_seconds\":7200,\"public_key\":\"<your ~/.ssh public key>\",\"reason\":\"\",\"agent\":\"Claude Code\"}", at: 2500 },
        { cmd: "hi compute ls", at: 5600 },
        { out: "No instances are running.", at: 6600 },
        { out: " ", at: 6700 },
        { out: "runpod is managed by http://hi.example.com:7373: you are sam (staff), and starting needs an approval.", at: 6800 },
        { ok: "This month: $0.01 of sam's $300.00 · staff $0.01 of $2000.00", at: 7000 },
      ],
    },
  ],
};
