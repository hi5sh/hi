# The scene player

The website is in `site/`; paths here are from there.

The feature pages' demos (`docs/feature-pages.md`) play scenes drawn in
HTML and CSS from scene definitions, with no video and no library. The
player is three files:

| File | What it is |
|---|---|
| `assets/demos/<name>.js` | The scenes: what each shows, and when. Edit this to change a demo. |
| `assets/story.js` | The player: draws each kind of screen, keeps time, loops. |
| `assets/story.css` | How the scenes look, in the page's own colours. |

The scenes play as a loop in the box on the page, muted by nature, with
previous, play or pause, next, and a dot per scene under it.

## Changing a demo

A scene looks like this:

```js
{
  id: "shell", title: "Open a box", caption: "Your project. Nothing else.",
  short: 11000, screen: "terminal", window: "you@laptop: ~/myproject",
  lines: [
    { cmd: "hi box shell", ps: "$", at: 300 },
    { out: "Box myproject-1: shell in /home/you/myproject, network dev.", at: 1500 },
  ],
},
```

- `pace`, at the top of the file, multiplies every time (`pace: { short: 1 }`).
  It is the knob for "a little slower" or "a little faster" without touching
  the scenes.
- `short` is the scene's length in milliseconds, before `pace`. The loop's
  order is the scenes' order.
- Every timed item has `at`, its time from the scene's start.
- `caption` is the line at the foot of the picture. `title` is the scene's
  name under the picture and on its dot.
- `screen` picks the renderer in `story.js`: `terminal` or `channel`. A new
  kind of screen is a new function in `screens` in `story.js`, with its items
  marked by `timed()`.
- A terminal scene sets `window` (the title bar), `prompt`, and `lines`: a
  `cmd`, or what a command printed (`out`, `ok`, `warn`, `err`), or `live`,
  hi's progress line; `docs/feature-pages.md` has the details.
- A channel scene sets `members` and `messages`. A message's `who` names
  someone in `people` at the top of the file: initials, name, role, and a
  colour; add a person there to use them. `STORY.channel` is the channel's
  name.

Keep a demo true to what hi does: a feature that isn't built doesn't go in it.

## Seeing it

The page plays it as soon as it loads. In the browser's console, `hi5Story`
is the player: `hi5Story.seek(1, 2000)` jumps to the second scene two seconds
in, and `hi5Story.pause(true)` holds it.

## Accessibility and fallbacks

- With "reduce motion" on, nothing moves: each scene shows complete, and the
  arrows step through them. The caption under the picture is live text, so a
  screen reader hears each scene's name.
- Without JavaScript, the box shows the page's static fallback.
- The loop pauses when the box is scrolled out of view or the tab is hidden.
