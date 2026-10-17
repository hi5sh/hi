# Map of Hifin Template Name

What the project already has, one line each, and where it lives: the map
the team's agents read before building, so they extend what exists rather
than build beside it. Keep it short, an inch deep and a mile wide: say
what each thing is and where to look, and leave the detail to the code and
`docs/decisions/`. Update it in the same work as the change.

## Pages

- `/`: the welcome page, until the first feature replaces it.
  `src/index.html`, `src/main.tsx`, `src/App.tsx`, `src/styles.css`

## API

- `GET /healthz`: says the app and its database answer. `src/server.ts`,
  where every route goes.

## Data

- SQLite, `app.db` in `$HI_DATA_DIR`, which outlives every release. No
  tables yet; tables change only by a new line in `migrations`, `src/db.ts`.

## Shared code

- `src/db.ts`: opens the database and runs its migrations.
- `src/test-dom.ts`: a document for page tests (happy-dom).

## Outside services

- Models: OpenRouter at `$OPENROUTER_BASE_URL` with `$OPENROUTER_API_KEY`;
  hi adds the team's real key on each call. The app's box has no `hi`, no
  agents, and no logins, so `hi agent` and Codex or Claude Code don't run
  there: for an agent's model, call it through OpenRouter (an `openai/`
  model for Codex's).
- The helper, the chat with the team in the corner of every page: hi adds
  it, and `/__hi/` belongs to hi.

## Docs

- `HELP.md`: the guide for the people who use the app; the helper answers
  from it.
- `docs/specs/`: what the lead and the people agreed, feature by feature.
- `docs/decisions/`: why the project is built as it is, one file each.
