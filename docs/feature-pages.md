# Feature pages

A feature page is one tool on its own page, named after its command:
`hi box` is at https://hi5.sh/box/. It starts with a demo of the tool in a
terminal, and each section below is a step deeper than the one before. The
guide (`/guide/`) stays where the step-by-step pages live; a feature page
shows what the tool is and how it works, and links to the guide for the rest.

| File | What it is |
|---|---|
| `<name>/index.html` | The page: the hero's words in its front matter, the sections as its content. |
| `assets/demos/<name>.js` | The demo's scenes, played by `assets/story.js`. |
| `_layouts/feature.html` | The page around them: header, hero, demo, footer. |
| `assets/feature.css` | How a feature page looks, in the homepage's colours. |

## The sections, easy to deep

1. **Hero:** the tool's name, a one-line headline, a lede, and the demo.
2. **What it's for:** three uses, each with a two-line snippet.
3. **Try it:** numbered commands to copy, from install to cleaning up.
4. **How it works:** the tool's structure diagram (`{% include diagram.html name="<name>" %}`,
   see `docs/guide-diagrams.md`) and a few short paragraphs.
5. **The details:** options, settings, limits, and what the tool doesn't
   do, with the deepest part in a closed "Under the hood".
6. **Go further:** the guide's pages and the command reference.

`box/index.html` and `agent/index.html` are the examples to copy. Quote a
front-matter value that holds `: `, such as a lede with a list after a
colon; unquoted, Jekyll drops the front matter and the page loses its
layout. `feature_pages_test.go` checks for it.

## The demo

The demo uses the homepage story's player (`docs/story-animation.md`), with
its `terminal` screen. The scenes file sets `window.HI5_STORY`:

```js
window.HI5_STORY = {
  size: { w: 580, h: 440 },       // the picture's design size
  narrow: { w: 400, h: 560 },     // under 600px wide, so the text stays readable
  pace: { short: 1 },
  people: {},
  scenes: [
    {
      id: "shell", title: "Open a box", caption: "Your project. Nothing else.",
      short: 11000, screen: "terminal", window: "you@laptop: ~/myproject",
      prompt: "box@myproject-1:/home/you/myproject$",
      lines: [
        { cmd: "hi box shell", ps: "$", at: 300 },
        { out: "Box myproject-1: shell in /home/you/myproject, network dev.", at: 1500 },
      ],
    },
  ],
};
```

- A line is `cmd` (typed after the scene's `prompt`, or the line's own `ps`),
  or what a command printed: `out`, `ok` (green), `warn` (amber), or `err` (red).
- `live` is hi's progress line, as `hi agent` shows it while an agent works:
  a list of states, `{ text, at }`, each replacing the one before, with a
  red scanner. A last state without `text` clears it, as hi does when the
  report comes.
- `short` is the scene's length and `at` each line's time, in milliseconds.
  A feature page plays only the loop, so it needs no `dur`.
- Keep each scene to what fits the window at both sizes; a longer one
  scrolls, and its first lines leave the picture.

Every line a command prints is copied from a real run of that command, with
only names and folders changed. Run the commands in a scratch project, paste
what they print, and say so under the demo (`demo_caption`).
`feature_pages_test.go` checks that every `hi <name>` command a demo types
uses a subcommand and flags the tool's usage lists; a new feature page goes
in its `featurePageUsage` with its tool's usage function.

## Seeing it

Build the site and look at it in Chromium, at 1440 and 390 wide, as
`docs/guide-diagrams.md` shows. In the console, `hi5Story.seek(1, 9000)`
jumps to the second scene nine seconds in, and `hi5Story.pause(true)` holds
it there for a screenshot.
