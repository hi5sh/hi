---
title: Images, media, and research with Antigravity
description: "A hi team's agents can have Google's Antigravity make things while the app is built: images and game assets, speech, music, video, a PDF read, a question researched. The coding agents in the dev box use hi dev agy, the lead hi team agy, and both ask first."
---

Building an app often needs things a coding agent can't make: the
pictures for a game, a logo, a voice line, what a supplier's PDF says, a
question researched on the web. The team's agents can ask Google's
[Antigravity](/guide/agent/#antigravity) for them. It runs on the team's
machine, with the machine's Google sign-in and Gemini API key, which
never enter the team's boxes.

| It can | For example |
|---|---|
| Make images | sprites and backgrounds for a game, a logo, icons, an illustration, an edit of a picture |
| Search and read the web | a question researched, with sources |
| Make speech, music, and video | a voice line, a jingle, a short clip |
| Read documents and media | what a PDF, a recording, or a video says; a transcript |
| Research deeply | a written report from Google's deep research agent |

## In the dev box

Claude Code and Codex know how from their `hi-dev-agy` skill, and ask you
before each run:

```bash
hi dev agy "12 card images, 512x512, transparent PNG, named <suit>-<rank>.png"
hi dev agy "a logo in the style of this sketch, as logo.svg and logo-512.png" --file /box/repo/docs/sketch.jpg
hi dev agy show a-3
```

The agent waits for the run, a minute or two (longer for a video), and
what it made lands in the box's `~/hi/agy/<id>/`, with Antigravity's
summary. The agent looks at it, puts what you keep into the project, and
commits it with the work. Files it's given with `--file` come from the
box's home or the clone, up to 20.

## In the channel

The lead can do it for the people in a thread, once they say yes:

> **Anna:** can we get a picture of a happy cat for the empty-orders page?
> **Lead:** Antigravity can draw one on the machine's Google plan. Shall I?
> **Anna:** yes please

The thread's files go with the run, so "what does this PDF say about
notice periods?" works on the PDF someone shared. hi posts what it made in
the thread and wakes the lead there to say what it is. Only someone with a
level in the team (a contributor, a developer, or an owner) can have it
run. Everything it made is also in the team's folder, under `agy/<id>/`.

## Who pays

Each run is on the plan and key of whoever set the machine up, not the
team's model budget:

- **Images and web search** are on the Google AI Pro or Ultra plan that
  `agy` is signed in with.
- **Speech, music, video, transcripts, reading media, and deep research**
  use [gemini-api](/guide/agent/#antigravity), billed per call to the
  machine's Gemini API key: video, music, and deep research cost the most.

That's why the agents ask first, and one run goes at a time per team.

## Setting it up

An admin of the machine, once:

```bash
hi install agy gemini-api
agy                        # sign in with Google, then quit
hi agent token gemini      # a key from aistudio.google.com/apikey, for media
```

Without it, the agents say so and name these commands.

## Not for the app itself

This is for building the app. When the app itself should read PDFs or
make pictures for its users, it calls a model at run time, with the
app's own model access (`$OPENROUTER_BASE_URL`), as any feature does.
