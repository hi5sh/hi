# Hifin Template Name

A web app on Bun alone: the server is `Bun.serve`, the pages are React,
the data is SQLite (`bun:sqlite`), and the tests are `bun test`.

## Setup

```sh
bun install
make check
make dev
```

`make dev` serves on <http://localhost:3000> and reloads on every change.
`make run` serves as previews and production do, on `0.0.0.0:$PORT`.
`bun run build` makes `dist/app`, one executable with the pages inside.
`make help` lists the other targets.

The database is `app.db` in `$HI_DATA_DIR`, or in `data/` without it.
