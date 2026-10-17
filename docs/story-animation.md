# The story animation on the homepage

The "From idea to application" box on hi5.sh plays a story: an HR employee's
idea becomes an app, with a hi team. It is drawn in HTML and CSS from scene
definitions, with no video and no library, in three files:

| File | What it is |
|---|---|
| `assets/story-scenes.js` | The scenes: dialogue, timing, and what each shows. Edit this to change the story. |
| `assets/story.js` | The player: draws each kind of screen, keeps time, loops, opens the dialog. |
| `assets/story.css` | How the scenes look, in the page's own colours. |

Two versions come from the same scenes: the loop in the box (seven scenes,
about 39 seconds, muted by nature, restarting without a cut) and the full
story (nine scenes, about 2:07) behind "Watch the full story", in a dialog
with play, pause, previous, next, and a dot per scene.

## Changing the story

A scene in `story-scenes.js` looks like this:

```js
{
  id: "ask", title: "Asking IT for a team", caption: "Ask IT for a team.",
  dur: 7000, short: 3000, screen: "dm",
  with: "alex",
  messages: [
    { who: "emma", at: 300, text: "Hey Alex! I have an idea …" },
    { who: "alex", at: 3400, atShort: 1600, text: "Sure. Give me a minute." },
  ],
},
```

- `pace`, at the top of the file, multiplies every time: `short` for the
  loop and `full` for the full story. It is the knob for "a little slower"
  or "a little faster" without touching the scenes.
- `dur` is the scene's length in the full story and `short` its length in the
  loop, both in milliseconds, before `pace`. Leave `short` out to keep a scene out of the
  loop. The loop's order is the scenes' order.
- Every timed item has `at`, its time from the scene's start in the full
  story. In the loop it appears at `atShort`, or at `at` scaled to the loop's
  length when there is no `atShort`. `full: true` keeps an item out of the loop.
- `caption` is the line at the foot of the picture. `title` is the scene's
  name under the picture and on its dot.
- `who` names someone in `people` at the top of the file: initials, name, role,
  and a colour. Add a person there to use them in a scene.
- `screen` picks the renderer in `story.js`: `idea`, `dm`, `terminal`,
  `channel`, `build`, `app`, `improve`, `ecosystem`, or `hi5`. Each reads its
  own fields from the scene (the terminal's `lines`, the build's `tasks`); the
  existing scenes show the shape. A new kind of screen is a new function in
  `screens` in `story.js`, with its items marked by `timed()`.

Keep the story true to what hi does. The scenes show a hi team as it works
today; a feature that isn't built doesn't go in the story.

## Seeing it

The page shows it as soon as it loads. In the browser's console,
`hi5Story` is the loop's player and `hi5StoryFull` the dialog's while it is
open: `hi5Story.seek(3, 2000)` jumps to the fourth scene two seconds in, and
`hi5Story.pause(true)` holds it. Opening `/?story=full` opens the full story
at once.

## Recording the video

The full story can be recorded from a browser, for YouTube or a talk, with
Playwright. From a checkout with the site served locally (`python3 -m
http.server 8000`):

```js
// record.mjs: needs playwright-core and a Chromium it can launch
import { chromium } from "playwright-core";
const browser = await chromium.launch();
const context = await browser.newContext({
  viewport: { width: 760, height: 900 },
  recordVideo: { dir: "video", size: { width: 760, height: 900 } },
});
const page = await context.newPage();
await page.goto("http://127.0.0.1:8000/?story=full");
await page.waitForFunction(() => window.hi5StoryFull && !window.hi5StoryFull.playing, null, { timeout: 180000 });
await context.close(); // writes video/<id>.webm
await browser.close();
```

The dialog is sized to the window, so a taller window gives a bigger picture.
Crop the recording to the dialog, and convert it with ffmpeg if a `.webm`
won't do: `ffmpeg -i video/*.webm -c:v libx264 -pix_fmt yuv420p story.mp4`.

## Accessibility and fallbacks

- With "reduce motion" on, nothing moves: each scene shows complete, and the
  arrows step through them. The caption under the picture is live text, so a
  screen reader hears each scene's name.
- Without JavaScript, the box shows a static thread from the story.
- The loop pauses when the box is scrolled out of view or the tab is hidden.
