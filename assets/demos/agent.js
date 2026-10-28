// The demo at the top of hi5.sh/agent/, played by assets/story.js. Every line
// a command prints is what hi agent printed in a real run; only the folder
// (/home/you) is made up. docs/feature-pages.md says how to edit it.
window.HI5_STORY = {
  size: { w: 580, h: 440 },
  narrow: { w: 400, h: 600 },
  pace: { short: 1 },
  people: {},
  scenes: [
    {
      id: "task", title: "Hand off a task", caption: "One task. One report.",
      short: 16000, screen: "terminal", window: "you@laptop: ~/pricing",
      lines: [
        { cmd: "hi agent claude --model sonnet \"make make check pass, and commit the fix\"", at: 300 },
        { out: "Box pricing-1: claude in /home/you/.local/state/hi/box/pricing-1/work (branch hi-box/pricing-1), model sonnet, network dev.", at: 1600 },
        { out: "Claude Code is working in pricing-1. hi box attach pricing-1 follows it; Ctrl+C stops waiting, not the agent.", at: 1900 },
        { live: [
          { text: "1s · starting", at: 2400 },
          { text: "5s · 16k in · 59 out · 1 step · Bash: make check 2>&1 | tail -40", at: 3600 },
          { text: "6s · 33k in · 139 out · 2 steps · Bash: cat Makefile; git ls-fil…", at: 5000 },
          { text: "8s · 50k in · 304 out · 3 steps · Bash: sed -i 's/sum(prices) \\*…", at: 6400 },
          { at: 8200 },
        ] },
        { out: " ", at: 8200 },
        { out: "`make check` now passes (2 tests) and the fix is committed as c34ea4f.", at: 8200 },
        { out: " ", at: 9200 },
        { out: "Took 10s · 3 steps · 67k tokens in (62k cached) · 527 out · claude-sonnet-5-5 · $0.03 at API prices", at: 9200 },
        { out: " ", at: 9200 },
        { ok: "Changed on hi-box/pricing-1:", at: 10000 },
        { ok: "  pricing.py", at: 10100 },
      ],
    },
    {
      id: "second", title: "A second opinion", caption: "Another vendor checks the work.",
      short: 12000, screen: "terminal", window: "you@laptop: ~/pricing",
      lines: [
        { cmd: "hi agent codex --from hi-box/pricing-1 \"review the last commit; don't edit files\"", at: 300 },
        { out: "Box pricing-2: codex in /home/you/.local/state/hi/box/pricing-2/work (branch hi-box/pricing-2), model gpt-6.1-sol, network dev.", at: 1600 },
        { out: "Codex is working in pricing-2. hi box attach pricing-2 follows it; Ctrl+C stops waiting, not the agent.", at: 1900 },
        { out: " ", at: 4800 },
        { out: "No findings in commit `c34ea4f`. The calculation now correctly applies the discount.", at: 4800 },
        { out: " ", at: 5800 },
        { out: "Took 33s · 6 steps · 106k tokens in (97k cached) · 673 out · gpt-6.1-sol", at: 5800 },
        { out: " ", at: 6600 },
        { ok: "No files changed. hi box rm pricing-2 removes the box.", at: 6600 },
      ],
    },
    {
      id: "keep", title: "Keep what you agree with", caption: "The work waits on a branch.",
      short: 12000, screen: "terminal", window: "you@laptop: ~/pricing",
      lines: [
        { cmd: "hi box diff pricing-1", at: 300 },
        { out: "pricing-1 on hi-box/pricing-1, from 5aa2d7052c54:", at: 1300 },
        { out: "Commits", at: 1500 },
        { out: "  c34ea4f Fix discount calculation in total", at: 1600 },
        { out: "Files", at: 1800 },
        { out: "  M pricing.py", at: 1900 },
        { cmd: "git merge hi-box/pricing-1", at: 3600 },
        { out: "Updating 5aa2d70..c34ea4f", at: 4600 },
        { out: "Fast-forward", at: 4700 },
        { out: " pricing.py | 2 +-", at: 4800 },
        { cmd: "hi box rm pricing-1", at: 6400 },
        { ok: "Removed pricing-1. Its branch hi-box/pricing-1 is kept; delete it with git branch -D hi-box/pricing-1.", at: 7400 },
      ],
    },
  ],
};
