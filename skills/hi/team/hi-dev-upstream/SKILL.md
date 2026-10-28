---
name: hi-dev-upstream
description: Keep a hi team's adopted project up with the project it came from (its upstream) from the dev box, and offer the team's own work back as a pull request. Use when the user mentions upstream, a new version of the project the team started from, merging upstream changes, or contributing a change back. Covers hi dev upstream and hi dev propose.
---

# Upstream

A project the team adopted keeps the project it came from as `upstream`.
hi fetches it daily, and the lead says in the channel when it has a new
version.

```bash
hi dev upstream        # where dev stands against upstream, and its refs into this clone
```

It prints how many commits dev has of its own, how many upstream has
since, and upstream's newer tags; then it brings upstream's branches
(`upstream/<branch>`) and tags (`upstream/v2.4`) into this clone.

## Bringing a new version in

1. A branch of its own: `herdr worktree create --cwd /box/repo --branch upstream-v2.4`.
2. `git merge upstream/v2.4` (a tag, rather than upstream's branch, unless
   the user asks for the latest). Resolve conflicts in favor of what the
   team changed on purpose; say what you chose.
3. Run the checks, put it on the preview (`hi dev preview`), and tell the
   user what changed upstream that matters for this app, from upstream's
   release notes and `git log`.
4. Ship it when the user agrees (`hi dev ship -m "Bring in <name> v2.4"`).

## Offering work back

```bash
hi dev propose t-12 description.md     # a draft pull request upstream with t-12's change
```

Only work that is in dev, and only when the user asks: the description
is for upstream's maintainers, so write what the change does and why,
without the team's internals. The team's own files (its specs, docs/map,
AGENTS.md) stay out of the pull request. It needs `gh` signed in on the
machine.
