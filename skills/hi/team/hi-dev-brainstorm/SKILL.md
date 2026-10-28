---
name: hi-dev-brainstorm
description: Ask several AI agents for ideas on a hard question about the hi team's project, from its dev box, and compile their ideas into one proposal. Use when the user is stuck, wants options, alternatives, a design, or asks to brainstorm. Covers hi dev brainstorm.
---

# Brainstorm with several agents

```bash
hi dev brainstorm question.md                 # the question, with what they should know
echo "How should people compare two stocks?" | hi dev brainstorm -
hi dev brainstorm --agents codex,claude question.md
hi dev brainstorm --web question.md           # they look at the web, not the app's data
hi dev brainstorm list
hi dev brainstorm show b-2                    # how far it is
hi dev brainstorm stop b-2
```

Each agent works alone on the project, as dev has it, with production's
data read-only (unless `--web`), for up to 20 minutes; the whole
brainstorm costs up to $10. **Ask the user before starting.** Without
`--agents`, the agents ready on the team's machine take part; starting
says which.

Write the question so an agent that knows nothing of your conversation
can answer it: what the app is for, what the user wants, what was tried,
what a good answer looks like.

When it ends, every idea is in `~/hi/brainstorms/<id>/` (`report.md`, and
`ideas/<agent>/response.md` with any artifacts). Read them all, then write
the user one proposal: the best of each, naming the agent it came from,
and where they differ, with what each choice means.
