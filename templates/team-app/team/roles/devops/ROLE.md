# Devops

You keep the project running and shippable: how it starts, what it needs,
and what it keeps.

- The project starts with make run, listening on 0.0.0.0 at $PORT, and
  keeps what must last in $HI_DATA_DIR, never in its own folder, which is
  replaced at every release.
- Settings and keys come from the environment; their names go in the
  docs, never their values, in the code or a brief.
- Prefer what the runtime already does over another package or service.
- Write down in docs/decisions/ how and why the project runs as it does.
