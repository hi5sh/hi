---
name: hi-dev-survey
description: Survey a project the hi team adopted from elsewhere - make it run on the team's preview, run its tests, map it in docs, and say what's missing - before anything is built on it. Use when the team's project came from someone else (it has an upstream), docs/map.md is missing, or the user asks to survey, map, or get an adopted project running.
---

# Survey an adopted project

A project the team adopted was written by someone else. Before anything
is built, make it run and map it, so the owners know what they have. Do
it on a branch of its own (herdr worktree create --cwd /box/repo --branch
survey), in this order, and commit each:

1. **Run it.** Find how it builds, starts, and runs its tests; install what
   it needs. Make it start on `$PORT` (8080 if unset), listening on
   `0.0.0.0`. Add a `run` target to the Makefile that does exactly that,
   with any install step first; when the project's own `run` means
   something else, add `hi-run` instead (hi uses `hi-run` first). Data the
   app keeps (a database, uploads) must go in `$HI_DATA_DIR`; point it
   there when it's a setting, and say so when it isn't, since that data
   would be lost at every release.
2. **Test it.** Run its own tests; say which pass and which fail. Don't
   fix or add tests yet.
3. **Map it.** Write `docs/map.md` (what is where, part by part),
   `docs/user/index.md` (for the people who use it: what it is, who it's
   for, how to start, ending with where it came from and its licence), and
   `docs/tech/index.md` (the parts and how they connect). A technical
   choice the code depends on gets a short file in `docs/decisions/`.
4. **Say what's missing:** settings it needs (`hi dev settings` lists the
   team's), half-built features, services it expects beside it (a
   database server, a cache) and whether it runs without them, Git LFS
   files or submodules it needs, and what its licence or CONTRIBUTING file
   asks of people who change it.

Change no behavior: this is a survey, not a feature. Keep the project's
own files as they are, apart from the Makefile and the docs. Its own
notes (AGENTS.md below the team's rules, CLAUDE.md) describe the code; the
team's rules come first.

Then put it on the preview (`hi dev preview`), and tell the user, in plain
words: the app in three sentences, up to five things to try on the
preview, what doesn't work yet, and the settings it needs. Ship it
(`hi dev ship -m "Survey ..."`) when the user agrees.
