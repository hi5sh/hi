---
title: Brainstorms on hard questions
description: "From a hi team's dev box, an owner's agent can start a brainstorm on a really hard question: Codex, Claude, and Gemini each think it through alone, with the project and its data, and the agent compiles their ideas into one proposal. It asks first."
---

Some questions don't have an obvious answer: a feature nobody has built
before, a design that could go several ways. For those, ask your agent in
the [dev box](/guide/team/dev/) for a brainstorm: several agents think the
question through, each alone, with the project and its data, and your
agent brings their ideas together into one proposal. It knows how from
its `hi-dev-brainstorm` skill, and asks you before it starts one.

```bash
hi dev brainstorm question.md                 # the question, with what they should know
echo "How should people compare two stocks?" | hi dev brainstorm -
hi dev brainstorm --agents codex,claude question.md
hi dev brainstorm --web question.md           # they look at the web, not the app's data
hi dev brainstorm list
hi dev brainstorm show b-2                    # how far it is
hi dev brainstorm stop b-2
```

## What happens

- Each agent gets dev's code, the specs, and production's data,
  read-only, or the internet instead of the data with `--web`, when
  looking at other services helps. Each writes its idea, and may add a
  sketch, a diagram, or a small prototype to make it clearer.
- Without `--agents`, the agents ready on the team's machine take part,
  usually Codex, Claude, and Gemini; starting says which.
- Each agent has up to 20 minutes, and the whole brainstorm costs at most
  $10 of metered use.
- When it ends, every idea lands in the box, in `~/hi/brainstorms/<id>/`:
  `report.md`, and each agent's response with what it made. Your agent
  reads them all and writes you one proposal: where the ideas agree, the
  best of each, by agent, where they differ and which way it would go,
  and the questions only you can answer.

A proposal is not work: nothing is built until you decide, and then it is
ordinary work, from a task, on a branch.

Write the question so an agent that knows nothing of your conversation
can answer it: what the app is for, what the people want, what was tried,
and what a good answer looks like. Your agent does this when it writes
the question file for you.

## On the machine

On the team's machine, `hi agent brainstorm ls` lists every brainstorm,
and the ideas stay in the team's folder, in `brainstorms/`. See
[Brainstorm with several agents](/guide/agent/brainstorm/) for how the
agents work.
