// The demo at the top of hi5.sh/box/, played by assets/story.js. Every line
// a command prints is what hi box printed in a real run; only the folder
// (/home/you/myproject) is made up. docs/feature-pages.md says how to edit it.
window.HI5_STORY = {
  size: { w: 580, h: 440 },
  narrow: { w: 400, h: 560 },
  pace: { short: 1 },
  people: {},
  scenes: [
    {
      id: "shell", title: "Open a box", caption: "Your project. Nothing else.",
      short: 11000, screen: "terminal", window: "you@laptop: ~/myproject", prompt: "box@myproject-1:/home/you/myproject$",
      lines: [
        { cmd: "hi box shell", ps: "$", at: 300 },
        { out: "Box myproject-1: shell in /home/you/myproject, network dev.", at: 1500 },
        { cmd: "ls ~/.ssh", at: 2600 },
        { err: "ls: cannot access '/box/home/.ssh': No such file or directory", at: 3600 },
        { cmd: "env | grep -ci token", at: 4900 },
        { out: "0", at: 5800 },
        { cmd: "make test", at: 7000 },
        { ok: "3 passed", at: 8000 },
      ],
    },
    {
      id: "network", title: "Only the hosts you allow", caption: "One way out, and it keeps a list.",
      short: 13000, screen: "terminal", window: "you@laptop: ~/myproject", prompt: "box@myproject-1:/home/you/myproject$",
      lines: [
        { cmd: "curl -so /dev/null -w '%{http_code}\\n' https://pypi.org", at: 300 },
        { ok: "200", at: 1500 },
        { cmd: "curl -sS https://example.com", at: 2700 },
        { err: "curl: (56) CONNECT tunnel failed, response 403", at: 3700 },
        { out: "# in another terminal, outside the box", at: 4800 },
        { cmd: "hi box allow myproject-1", ps: "$", at: 5600 },
        { out: "myproject-1 was refused:", at: 6600 },
        { out: "  example.com (1 times)", at: 6700 },
        { out: "Allow one with hi box allow myproject-1 <domain>.", at: 6800 },
        { cmd: "hi box allow myproject-1 example.com", ps: "$", at: 8400 },
        { ok: "myproject-1 may now reach example.com and its subdomains. No restart needed.", at: 9600 },
      ],
    },
    {
      id: "review", title: "Read it before you merge", caption: "Changes wait on a branch.",
      short: 14000, screen: "terminal", window: "you@laptop: ~/myproject", prompt: "box@myproject-2$",
      lines: [
        { cmd: "hi box shell --worktree", ps: "$", at: 300 },
        { out: "Box myproject-2: shell in /home/you/.local/state/hi/box/myproject-2/work (branch hi-box/myproject-2), network dev.", at: 1400 },
        { cmd: "echo 'lint: ; ruff check .' >> Makefile", at: 2600 },
        { cmd: "git commit -qam 'Add a lint target' && exit", at: 3800 },
        { cmd: "hi box diff myproject-2", ps: "$", at: 5200 },
        { out: "myproject-2 on hi-box/myproject-2, from 766729f9866a:", at: 6200 },
        { out: "Commits", at: 6400 },
        { out: "  ec0c4cc Add a lint target", at: 6500 },
        { out: "Files", at: 6700 },
        { warn: "  M Makefile   ⚠ runs on the host later", at: 6800 },
        { out: "Read Makefile before you run anything from this branch on your machine.", at: 7600 },
        { cmd: "git merge hi-box/myproject-2", ps: "$", at: 10000 },
      ],
    },
  ],
};
