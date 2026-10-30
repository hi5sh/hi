---
name: hi-dev-agy
description: Have Google's Antigravity make or find things for the app while you build it - images and game assets, a logo or icons, speech, music, or a short video, a transcript, what a PDF, recording, or video says, or a question researched on the web - with hi dev agy. Use when the work needs a picture, a sound, media, a document read, or research that you can't make yourself; ask the user first.
---

# Antigravity: assets, media, documents, research

`hi dev agy` hands a task to Google's Antigravity agent, which runs on the
team's machine, not in this box. It makes files and finds things; it
doesn't write the app's code. What it makes comes back into this box, and
you put it into the project.

| It can | For example |
|---|---|
| Make images | sprites, tiles, and backgrounds for a game; a logo, icons, an illustration for a page; a picture to edit |
| Search and read the web | "what do the three biggest Nordic payroll apps charge?", with sources |
| Make speech, music, and video | a voice line, a jingle, a 10-second intro clip |
| Read documents and media | what a PDF, a recording, or a video says; a transcript (md, srt) |
| Research deeply | a written report from Google's deep research agent |

Images and web search are on the Google AI plan of whoever set the
machine up. Speech, music, video, transcripts, reading media, and deep
research use gemini-api, billed per call to their Gemini API key, when the
machine has one: video, music, and deep research cost the most.

## Ask first

It runs on someone's plan and key, not the team's budget, so **ask the
user before every run**: say what you'd ask it for and what it may cost,
such as "Antigravity can make the 12 card images in one run on the Google
plan, OK?" or "a 10-second video is billed to the Gemini key, a dollar or
so; go ahead?". One yes covers that run, not the next.

## Run it

```bash
hi dev agy "12 square card images, 512x512, flat style, transparent background, one per suit and rank in cards.md"
hi dev agy "a logo in the style of this sketch, as logo.svg and logo-512.png" --file /box/repo/docs/sketch.jpg
hi dev agy "what does this contract say about notice periods? quote the clauses" --file ~/contract.pdf
hi dev agy show                 # the latest runs
hi dev agy show a-3             # one run: what it made and its summary
```

- Say exactly what you want: how many, the sizes and formats, the style,
  the names of the files. It can't see the project; give it what it needs
  with `--file` (up to 20 files, from your home or the clone).
- It waits for the run, often a minute or two, longer for a video. One
  run at a time per team.
- What it made is in `~/hi/agy/<id>/`, with its summary in `summary.md`.
  Look at it before you use it, move what you keep into the project, and
  commit it with the work. Say what you used.

## Not for the app itself

This is for building the app. When the app itself should read PDFs,
search, or make pictures for its users, it calls a model at run time:
`$OPENROUTER_BASE_URL` with `$OPENROUTER_API_KEY` (Gemini is there too;
see hi dev settings), not `hi dev agy`.

When hi says Antigravity isn't set up on the team's machine, tell the user
an admin of the machine runs `hi install agy` and signs in with `agy` (and
`hi install gemini-api` and `hi agent token gemini` for media): "ask your
IT team".
