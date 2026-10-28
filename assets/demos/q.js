// The demo at the top of hi5.sh/q/, played by assets/story.js. Every line
// hi q printed is from a real run; only the folder (~/notes-demo) is made
// up. docs/feature-pages.md says how to edit it.
window.HI5_STORY = {
  size: { w: 580, h: 440 },
  narrow: { w: 400, h: 560 },
  pace: { short: 1 },
  people: {},
  scenes: [
    {
      id: "ask", title: "Ask in plain words", caption: "See the command before it runs.",
      short: 12000, screen: "terminal", window: "you@laptop: ~/notes-demo",
      lines: [
        { cmd: "hi q move all the files in this folder ending with md to a new folder called notes", at: 300 },
        { out: "  · list .", at: 1800 },
        { ok: "╭──────────────────────────────────╮", at: 3000 },
        { ok: "│ mkdir -p notes && mv *.md notes/ │", at: 3000 },
        { ok: "╰──────────────────────────────────╯", at: 3000 },
        { out: "  Create a notes folder and move all .md files into it", at: 3300 },
        { out: "  *.md matches 3 files: README.md, my notes.md, plan.md", at: 3600 },
        { warn: "  changes files", at: 3900 },
        { out: "  enter run · e edit · c copy · ? explain · esc cancel", at: 4200 },
        { cmd: "ls", at: 7000 },
        { out: "notes  todo.txt", at: 7600 },
      ],
    },
    {
      id: "look", title: "It looks before it answers", caption: "Read-only looks, one dim line each.",
      short: 7000, screen: "terminal", window: "you@laptop: ~/tool",
      lines: [
        { cmd: "hi q what Go version does this module need", at: 300 },
        { out: "  · read go.mod", at: 1800 },
        { out: "This module needs Go 1.24.0 or later.", at: 3000 },
      ],
    },
    {
      id: "pipe", title: "Ask about output", caption: "Pipe in what went wrong.",
      short: 12000, screen: "terminal", window: "you@laptop: ~/notes-demo",
      lines: [
        { cmd: "make test 2>&1 | hi q why does this fail", at: 300 },
        { out: "  · run python3 -c \"import yaml\" 2>&1 (refused: changes files)", at: 1800 },
        { out: "  · run python3 -c \"import yaml\" 2>&1 || echo \"Exit code: $?\" (refused: changes files)", at: 2600 },
        { out: "  · run python3 -c \"import sys; print(sys.version)\" 2>&1 (refused: changes files)", at: 3400 },
        { ok: "╭────────────────────╮", at: 4800 },
        { ok: "│ pip install pyyaml │", at: 4800 },
        { ok: "╰────────────────────╯", at: 4800 },
        { out: "  Install the PyYAML library so the yaml import works.", at: 5100 },
        { warn: "  changes files", at: 5400 },
        { out: "  enter run · e edit · c copy · ? explain · esc cancel", at: 5700 },
        { out: "  Not run.", at: 8200 },
      ],
    },
    {
      id: "danger", title: "Dangerous asks for yes", caption: "hi judges the command itself.",
      short: 11000, screen: "terminal", window: "you@laptop: ~/notes-demo",
      lines: [
        { cmd: "hi q remove every docker image and container I am not using", at: 300 },
        { out: "  · run docker ps -a", at: 1800 },
        { out: "  · run docker images", at: 2400 },
        { ok: "╭───────────────────────────────╮", at: 3800 },
        { ok: "│ docker image prune -a --force │", at: 3800 },
        { ok: "╰───────────────────────────────╯", at: 3800 },
        { out: "  Remove all unused Docker images without prompting.", at: 4100 },
        { err: "  dangerous: docker image prune deletes data", at: 4400 },
        { out: "  enter run (asks for yes) · e edit · c copy · ? explain · esc cancel", at: 4700 },
        { out: "  Not run.", at: 7400 },
      ],
    },
  ],
};
