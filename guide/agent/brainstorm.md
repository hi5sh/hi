---
title: Brainstorm with several agents
description: hi agent brainstorm asks several agents the same question, each alone in a box on a snapshot of the project. Each writes its idea, with artifacts if it likes, and one more agent compiles them into a single proposal. Nothing is changed.
---

The best ideas often come from several minds, each with its own angle.
`hi agent brainstorm` asks Codex, Claude Code, and Gemini the same
question, each alone, then has one of them compile the ideas into one
proposal.

```sh
hi agent brainstorm "How should people sort and find cards once a board has hundreds?"
```

```text
Brainstorm b-1 on ~/projects/pocket-kanban at 4f1c2e9.
  question   How should people sort and find cards once a board has hundreds?
  ideas      codex, claude, gemini (google/gemini-4-argon), each alone, without seeing the others
  proposal   codex compiles the ideas into one
  boxes      locked network (each agent's own hosts only); the project's agent settings off
  limit      20m for each run, the ideas all at once
  budget     none; --budget sets one
  paid       codex: ChatGPT plan; claude: Claude Max; gemini: OpenRouter credits
  report     ~/.local/state/hi/brainstorms/b-1, readable only by you
Start? [y/N]
```

## How it works

1. **A snapshot.** hi freezes the project as one commit, your uncommitted
   changes included, so every agent reads the same thing.
2. **Ideas, each alone.** Every agent gets a box of its own on that
   snapshot, the question, and what you add with `--context`. None sees
   the others' answers. Each must write its idea (what, why it fits here,
   how, what it costs and risks, what it would leave out) and may add
   artifacts: a sketch, a diagram, a small prototype, a table of numbers.
3. **One proposal.** Another run, by `--lead` (the first agent unless you
   say), reads every idea and its artifacts and writes one proposal: where
   they agree, the best of each, credited by name, where they differ and
   which way it would go, and the first steps.

Nothing an agent changes in the project is kept; what counts is what it
writes. The report is `report.md`: the question, the proposal, and every
idea in full.

```sh
hi agent brainstorm show b-1 --md     # the whole report
hi agent brainstorm ls                # every brainstorm, its state and cost
```

## Who takes part

Without `--agents`, it's Codex, Claude Code, and Gemini, leaving out any
that aren't set up on the machine. Gemini is Hermes with Google's model,
through OpenRouter (`hi install hermes`). Name others, or a model:

```sh
hi agent brainstorm --agents claude,claude:sonnet,gemini:google/gemini-3.8-flash "..."
hi agent brainstorm --no-proposal "..."   # just the ideas; you write the proposal
```

## What the agents can see

By default the boxes have a locked network: each agent reaches its own
provider and nothing else. Two options widen what they work with, one at
a time:

- `--data <dir>` gives them a folder, read-only at `/data/<name>`.
- `--web` gives them the internet, to look at other services. Not with
  `--data`: an agent reading untrusted pages could be told to pass the
  data on.

The project and the question go to each agent's provider, as with any
`hi agent` run.

## In a team

A team's lead offers a brainstorm when a question is really hard, and
asks first; see [Brainstorms](/guide/team/brainstorms/).
