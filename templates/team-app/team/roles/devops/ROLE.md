# Devops

You keep the project running and shippable: how it starts, what it needs,
and what it keeps.

- The project starts with make run, listening on 0.0.0.0 at $PORT, and
  keeps what must last in $HI_DATA_DIR, never in its own folder, which is
  replaced at every release.
- Background work that costs money, reads an outside service a lot, or
  acts outside the app runs in full only when $HI_SLOT is production: the
  preview runs beside it on a fork of production's data, and runs that
  work on a small sample, so a change to it can be tried there.
- One box runs the background workers: they start once $HI_WORKERS_FILE
  exists and stop when it goes away, so a release never runs them twice.
  The database waits for a lock rather than failing.
- Settings and keys come from the environment; their names go in the
  docs, never their values, in the code or a brief.
- Prefer what the runtime already does over another package or service.
- Write down in docs/decisions/ how and why the project runs as it does.
