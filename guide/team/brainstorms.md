---
title: Brainstorms on hard questions
description: For a really hard question, the lead can start a brainstorm - Codex, Claude, and Gemini each think it through alone, with the project and its data - and compiles their ideas into one proposal. It asks first.
---

Some questions don't have an obvious answer: a feature nobody has built
before, a design that could go several ways. For those, the lead can run a
brainstorm: several agents think the question through, each alone, with
the project and its data, and the lead brings their ideas together into
one proposal.

## It asks first

When you ask for one ("brainstorm this"), the lead starts it. Otherwise it
never starts one on its own: when it judges a question really hard, it
asks, naming who would take part.

> This one is hard. Want me to start a brainstorm on it with Codex,
> Claude, and Gemini? Each thinks it through alone, and I bring their
> ideas together into one proposal. It takes about 20 minutes and changes
> nothing.

Say yes, or add or drop agents in your answer ("yes, but without
Gemini"). Codex, Claude, and Gemini are the usual three; the lead knows
which are set up on the team's machine.

## What happens

- Each agent gets dev's code, the specs, and the app's data, read-only,
  or the internet instead of the data when looking at other services
  helps. Each writes its idea, and may add a sketch, a diagram, or a small
  prototype to make it clearer.
- It takes up to 20 minutes, and at most $10 of metered use.
- When it ends, hi posts every idea in full where you asked, as a file,
  and the lead writes one proposal there: where the ideas agree, the best
  of each, by agent, where they differ and which way it would go, and the
  questions only you can answer.

A proposal is not work: nothing is built until you agree, and the work
starts as usual, with what you'll try to see it works and your yes.

## At the machine

The lead's command is `hi team brainstorm`; on the team's machine,
`hi agent brainstorm ls` lists every brainstorm, and the ideas stay in the
team's folder, in `brainstorms/`. See
[Brainstorm with several agents](/guide/agent/brainstorm/) for how the
agents work.
