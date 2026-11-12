# The dev box's view of Stage and production

Status: Approved (2026-11-12). Built in v2.6.140; the admin page's switch
waits (question 1).

Dependencies: [hi_app_dev.md](hi_app_dev.md) (the dev box, its broker
socket, `hi dev`), [hi_app_jobs.md](hi_app_jobs.md) (jobs and workers),
[hi_app_agents.md](hi_app_agents.md) (the app's MCP, production's tools
that only read), [hi_app_keys.md](hi_app_keys.md) (settings).

## Goal

An owner asks an agent in the dev box "is the competition research
working on production?", and the agent can't tell. The box reaches
Stage's and production's addresses, but only their sign-in pages, and
what answers the question isn't on a page anyway: it's the app's output,
its jobs, and what the app itself can say about its state. The lead has
two of these (the app's MCP, and `hi app jobs` on the machine); the dev
box has none, only `hi dev status`'s one line about jobs.

So the dev box gets a read-only view of Stage and production through its
broker socket, as the lead has: no network reach, no sign-in, and nothing
that changes either. The agents in the box run without asking, so what
they can do there is only what can't harm.

## Commands, in the box

```text
hi dev logs [stage|production] [--lines N] [--since 30m] [--grep text]
                     the app's own output, its settings' values masked
hi dev jobs [stage|production]
                     the app's jobs and workers, as hi app jobs shows them
hi dev mcp stage|production
                     the app's MCP server over stdio, as the lead has it:
                     on production only the tools that read
hi dev tools stage|production [<tool> [<json arguments>|-]]
                     the same from a shell: the tools, or one tool's answer
```

Production is the default for `logs` and `jobs`; the agent says `stage`
for the preview (`preview` is taken too, as everywhere).

### Logs

- The broker reads the slot's box's log from the engine (the same lines
  an incident keeps), at most 2,000 lines, 200 by default, `--since` and
  `--grep` on the broker's side.
- **Masked:** every value of the app's settings for that slot (shared
  and the slot's own) of 8 characters or more becomes `‹KEY›`, and each
  line of a file setting of 16 characters or more `‹KEY›` too. A line
  that a setting's value could still hide in (a log of a decoded token)
  is the app's to avoid; the docs say so.
- The app's output can hold its people's data. The owners turn the view
  off for production (below).

### Jobs

What `hi app jobs <app> [--preview]` prints on the machine: the workers'
box and each job's state, count, rate a day, current item, and last
error, from the same report.

### The app's MCP

The broker's route for the lead, `POST /mcp/{slot}`, on the dev box's
socket too, with its rule: on production, only tools the app marks as
reading (`readOnlyHint`); on Stage, which runs on its own copy of the
data, every tool. `hi dev mcp` is the same stdio bridge as the lead's,
for an agent that takes MCP servers; `hi dev tools` is it from a shell,
one call at a time. An app without MCP says so.

## Turning it off

```bash
hi app dev <app> view production off   # the dev box sees only Stage
hi app dev <app> view production on    # the default
```

An admin's or the owners' choice (the app's settings; the admin page
later). Off, `logs`, `jobs`, and `mcp` on production say it's off and
who turned it off. Stage is always on: it is the dev box's own trial,
on its own copy of the data.

## Who and how much

- Every call is in the broker's log with the slot and the command, and
  production's in the dev box's network log as `view production logs`,
  so the watcher sees reads of production like any other reach.
- Nothing here writes: no restart, no release, no tool that changes
  production. Production moves only on Release, as now.

## Tests

- Logs: the slot's box's lines, masked (a value of 8+ characters gone,
  a short one kept), `--grep` and `--lines`, `--since`; a slot with no
  box says so.
- Jobs: the same report as `hi app jobs`, both slots.
- MCP: the dev box's socket passes messages to the app as the lead's
  does; on production a tool without `readOnlyHint` is refused.
- Off: production's logs, jobs, and MCP refused with who turned it off;
  Stage still answers.

## Questions

- Should the admin page's app settings show the switch now, or once a
  team asks?
- Is 2,000 lines enough, or should `logs` also read the slot's earlier
  boxes (a release replaces the box, and its log goes with it)?
