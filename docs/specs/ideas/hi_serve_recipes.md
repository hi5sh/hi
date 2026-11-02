# `hi serve` recipes: web servers, file shares, databases, and more

Status: Draft (2026-11-02)

Dependencies: [hi_serve.md](../approved/hi_serve.md) (the frame: kinds,
recipes, where, the served box, its front and units, teams first),
[hi_box.md](../approved/hi_box.md) (network modes and the proxy's
allowlist), [hi_agent_bundles.md](hi_agent_bundles.md) (bundles as data
files from hi, the org's template sources, and your own folder),
[hi_server.md](../approved/hi_server.md#template-sources) (template sources),
[hi_team_helper_signin.md](../approved/hi_team_helper_signin.md) (key links),
[hi_net_expose.md](../approved/hi_net_expose.md) (public addresses),
[hi_team.md](../approved/hi_team.md) and
[hi_team_snapshots.md](../approved/hi_team_snapshots.md) (what a team keeps
itself), and [hi_cluster.md](hi_cluster.md) (`--on <machine>`).

## Goal

`hi serve llm` serves a model from a recipe. The same should hold for
anything a person or the org wants running: a folder as a website, a file
share, a PostgreSQL database for an experiment, a DuckDB file that others
query. Each is a recipe of a kind, served with the same commands, in the
same box, with the same rules:

```text
hi serve web static ./site
hi serve files webdav ./share --write --share
hi serve db postgres --name experiments
hi serve db duckdb ./prices.duckdb
hi serve ls
```

- **Recipes, not setups.** Someone who has never configured PostgreSQL
  gets a working, safe one with a password made for it, and a line to
  connect with.
- **Anyone can add a recipe.** New software of a kind hi knows is a JSON
  file in the org's template source or your own folder, checked by hi
  before it runs. A new kind comes with a hi release (hi_serve.md, decision
  5).
- **The same safety for everything.** Each served box is on a network with
  no way out, reached only through its front, with a hard memory limit
  that keeps teams first. It holds only the folders you name.
- **Data you don't lose by accident.** `stop` and `rm` keep what a database
  wrote; deleting it is its own, named step.

## What `hi serve` is for, and what it isn't

| You want | Use |
|---|---|
| A ready-made server running from a recipe: a model, a site from a folder, a share, a database | `hi serve` |
| An app your team builds and ships, with preview, production, and its own data | `hi team`; the team's data stays in the team, with its snapshots |
| A port that already runs, made reachable, also publicly | `hi net expose` |
| A box to work in, or an agent in a box | `hi box`, `hi agent` |
| The team's datasets and models on Hugging Face | `hi data` |

They meet without overlapping. A site served with `hi serve web` goes
public through `hi net expose`. A team's app may use a model or a database
that someone serves (open question 6), but a team's own database is part
of the team.

## Kinds

Each kind fixes what clients speak, how hi knows it is ready, how access is
checked, and what it keeps:

| Kind | Clients speak | Ready when | Access | Keeps |
|---|---|---|---|---|
| `llm` | OpenAI-compatible HTTP | `/health` answers | none on `127.0.0.1`; a key at the front when shared | nothing; models are shared and read-only |
| `web` | HTTP | `/` answers | none on `127.0.0.1`; a key link at the front when shared | nothing; the folder is read-only |
| `files` | WebDAV, SFTP, or FTP | the protocol answers | always: a password made for it, or your SSH keys | nothing; the folder is yours, read-only unless `--write` |
| `db` | the database's own protocol, or SQL over HTTP | the engine's own check | always: a password made for it | its data folder, until `rm --data` |

Only `web`, `files webdav`, and `llm` speak HTTP, so only they can go
public through `hi net expose`, and `hi net expose` warns about a port that
`hi serve` serves without a key. A `db` is never offered publicly.

### `web`

```text
hi serve web static <folder> [--spa] [--port P] [--share]
```

- `static` serves a folder with Caddy's `file_server`, read-only, from a
  configuration hi writes and mounts. `--spa` answers unknown paths with
  `index.html`, for single-page apps.
- Shared, the front takes the key once and keeps it in an HttpOnly
  cookie, the way a team's key links do (hi_team_helper_signin.md), so a
  browser can open `http://<netbird-address>:<port>/?hi_key=…`.
- Later recipes: a folder of Markdown rendered as a site, and a file
  browser.

### `files`

```text
hi serve files webdav <folder> [--write] [--share]
hi serve files sftp   <folder> [--write] [--share]
hi serve files ftp    <folder> [--write] [--share]
```

- One pinned rclone image serves all three (`rclone serve webdav`, `sftp`,
  `ftp`), so they behave the same: one folder, read-only unless `--write`.
- WebDAV is the default to point people at: Finder, Windows Explorer,
  GNOME Files, and rclone open it. Its password is made by hi and given to
  rclone as an htpasswd file; `hi serve key <name>` prints the user and
  password.
- SFTP accepts your SSH public keys (`~/.ssh/*.pub`, or `--key <file>`),
  so `scp`, `rsync`, and `sftp` work with no password.
- FTP sends passwords in the clear and needs a range of passive ports. It
  is offered only on `127.0.0.1` and the VPN, which encrypts, never
  publicly, and only once open question 2 is settled.
- A folder must exist, and may not be your home itself, `/`, or hold
  `~/.ssh`, `~/.gnupg`, or `~/.config/hi`.

### `db`

```text
hi serve db postgres [--version 16|17|18] [--database D] [--share]
hi serve db valkey [--share]
hi serve db duckdb <file.duckdb | folder> [--write] [--share]
```

- **PostgreSQL** from the official image, pinned per major version. hi
  makes the password and passes it as `POSTGRES_PASSWORD_FILE`. The data
  is in `~/.local/share/hi/serve/<name>/data`. `hi serve status` prints
  the line to connect with:

  ```text
  experiments   db postgres 18   local   running since 09:12
    address     127.0.0.1:5433
    connect     psql "postgres://hi@127.0.0.1:5433/experiments"    password: hi serve key experiments
    data        ~/.local/share/hi/serve/experiments/data   1.2 GB
  ```

- **Valkey**, the open-source fork of Redis, with its password in a
  configuration file hi mounts, never as an argument.
- **DuckDB** is a library, not a server, so serving it means putting
  something in front of it. A file is served read-only by default, so
  your own scripts can still read it; a reader can't open it while
  something writes, and `--write` holds the only writable connection.
  A folder of Parquet or CSV files is served as one view per file. Which
  server answers is open question 1.

### Later kinds

Candidates, each with its own section here before it is built:
notebooks (Jupyter); object storage (`files s3`, with `rclone serve s3`);
a vector database (Qdrant, as a `db` recipe); dashboards over a served
database; speech and embeddings (open question 8).

## Recipe files

A recipe is one JSON file. hi checks it before it runs anything, and
`hi serve recipes check <file>` runs the same check for authors.

```json
{
  "kind": "db",
  "name": "postgres",
  "description": "PostgreSQL, one database, a password made for it",
  "options": [
    { "name": "version", "values": ["16", "17", "18"], "default": "18" },
    { "name": "database", "type": "name", "default": "{name}" }
  ],
  "image": {
    "16": "docker.io/library/postgres:16.11@sha256:…",
    "17": "docker.io/library/postgres:17.7@sha256:…",
    "18": "docker.io/library/postgres:18.1@sha256:…"
  },
  "image_by": "version",
  "ports": [ { "name": "sql", "port": 5432, "protocol": "postgres", "default": 5432 } ],
  "secrets": [ { "name": "password", "type": "password", "mount": "/run/secrets/password" } ],
  "env": {
    "POSTGRES_USER": "hi",
    "POSTGRES_DB": "{option.database}",
    "POSTGRES_PASSWORD_FILE": "{secret.password}"
  },
  "data": [ { "mount": "/var/lib/postgresql" } ],
  "writable": ["/run/postgresql", "/tmp"],
  "health": { "exec": ["pg_isready", "-U", "hi", "-d", "{option.database}"] },
  "memory": "1G",
  "on": ["local", "machine"],
  "connect": "postgres://hi@{address.sql}/{option.database}",
  "tested": { "local": "Strix Halo, 2026-…: PostgreSQL 18.1, ready in 3 s" }
}
```

| Field | Says |
|---|---|
| `kind`, `name`, `description` | Which kind it is; its name within the kind; one line for `hi serve recipes` |
| `image` | An image pinned by digest, or one per value of an option (`image_by`) |
| `args` | The server's arguments, as a list |
| `inputs` | Positional arguments: a folder or a file, where it is mounted, read-only unless the kind's write flag is given |
| `options` | Flags of this recipe: a name, a type (`enum`, `int`, `size`, `name`), a default, and a help line. They can't take a name hi uses |
| `ports` | Each port the server listens on, with its protocol and a default on `127.0.0.1` |
| `secrets` | What hi makes for it (`password`, `key`, `htpasswd`), kept 0600 under `~/.config/hi/serve/<name>/`, mounted read-only as files |
| `env` | Environment values that aren't secret; they may name a secret's file |
| `data` | Folders the server keeps across restarts, under `~/.local/share/hi/serve/<name>/` |
| `writable` | Paths given as tmpfs, since the root filesystem is read-only |
| `health` | An HTTP path, a TCP port, or a command run in the box |
| `memory`, `cpus`, `gpu` | Its limits, which the teams-first check uses; `gpu` is `none`, `optional`, or `required` |
| `network` | Hosts it must reach, each with a reason, through the front's allowlist as with bundles; none by default |
| `on` | Where it can run |
| `connect` | The line `hi serve status` prints |
| `tested` | Where and when it was last tried, and what it did there |

Placeholders are only `{name}`, `{option.*}`, `{input.*}`, `{secret.*}`
(a file's path, never its content), `{port.*}`, and `{address.*}`, and
only inside list items and values. There is no field for a shell command,
privileges, added capabilities, devices beyond `gpu`, host paths beyond
`inputs`, or the host's network, so a recipe can't ask for them.

## Where recipes come from

As bundles (hi_agent_bundles.md), from three sources:

1. **hi itself**, built in under `recipes/<kind>/`.
2. **The org's template sources**, from a `recipes/` folder in the same
   repositories as templates and bundles, fetched through the org server
   (hi_server.md, template sources) at a commit.
3. **Your own folder**, `HI_RECIPES_DIR`, for writing and trying recipes.

Later ones win, so an org can replace a built-in recipe with its own
image. `hi serve recipes` shows each recipe's source and commit, and
`status` shows which one a served thing came from. Recipes from hi and the
org must pin images by digest. Your own may use a tag, and hi says so each
time. On the org's machines (hi_cluster.md), only recipes from hi and the
org run.

## Data

- What a recipe keeps (`data`) is in `~/.local/share/hi/serve/<name>/`,
  owned by you.
- `stop` and `rm` keep it. `rm <name> --data` deletes it, after showing the
  folder and its size and asking; without a terminal it needs `--yes`.
  `hi serve ls` shows a forgotten server's data as left behind, so it is
  never lost track of.
- A served thing records the image digest it runs. A new hi with a newer
  recipe changes nothing that runs. `hi serve upgrade <name>` moves it to
  the recipe's current image only when the recipe marks the data as
  compatible (a PostgreSQL minor release); a major version needs the
  engine's own upgrade (open question 4).
- Backups are open question 3.

## The org

Release 3 of hi_serve.md lets a shared model be named to the org with
`--org`. Here every kind can be:

```text
hi serve db postgres --name experiments --share --org
hi org services              # what is named to the org: kind, owner, machine, address
```

- Naming something to the org is a request, approved in Slack like any
  other, and recorded in the audit log.
- Members see it in `hi org services` and the Slack Home. A model is still
  reached through `hi org ai`; other kinds are reached at their own
  address, over NetBird, with their own credentials (open question 5).
- `hi org policy` can say which kinds and recipe sources a group may serve
  on the org's machines.

## Releases

1. **Recipes from anywhere, and a first database.** The full recipe
   format with `inputs`, `options`, `secrets`, `data`, and `writable`;
   `HI_RECIPES_DIR`, the org's `recipes/` folders, and `hi serve recipes
   check`; `rm --data`; and `hi serve db postgres`, which proves a kind with
   data and a protocol that isn't HTTP.
2. **Folders.** `hi serve web static` with key links at the front, and `hi
   serve files webdav` and `sftp`.
3. **More databases.** `hi serve db valkey`, and `hi serve db duckdb` once
   open question 1 is settled.
4. **The org's catalog.** `--org` for every kind, `hi org services`, and
   the policy on the org's machines.
5. **FTP**, if open question 2 keeps it.
6. **Later kinds**, each with its own section here first.

## Acceptance criteria

1. A recipe that asks for something the format can't say, or that names an
   unpinned image from hi or the org, is refused by `hi serve recipes
   check` and never run.
2. Every served box, of every kind, has no way out except the hosts its
   recipe names with a reason, and is reached only through its front.
3. Secrets are files, made by hi, 0600 on the host and read-only in the
   box. They appear in no process's arguments and no environment value.
4. A folder given to `web` or `files` is mounted read-only unless the
   kind's write flag is given, and a home folder or one holding keys is
   refused.
5. `stop`, `start`, and `rm` keep a database's data; only `rm --data`
   deletes it, after asking; and `hi serve ls` shows data left behind.
6. A new hi changes no image a served thing runs until `hi serve upgrade`,
   which refuses an incompatible one.
7. On the org's machines, only recipes from hi and the org run.

## Open questions

1. **What serves DuckDB.** The `httpserver` community extension (SQL over
   HTTP), the DuckDB UI (which loads its pages from ui.duckdb.org, so the
   recipe would name that host), a PostgreSQL-protocol front so `psql` and
   BI tools connect, or a small server shipped in the image. Also whether
   hi's release workflow builds and pins that image.
2. **FTP at all.** Clear-text passwords (the VPN encrypts them in
   transit), passive ports through the front, and rclone taking the FTP
   password only as an argument. The proposal is to leave it out until
   someone needs a client that speaks only FTP.
3. **Backups.** Reuse the teams' snapshots for a served thing's data
   folder, or the engine's own dump (`pg_dump`, a DuckDB `EXPORT
   DATABASE`), on a schedule or on demand (`hi serve backup <name>`)?
4. **Major upgrades** of a database: refuse with the steps, or run the
   engine's upgrade into a new data folder and keep the old one?
5. **Credentials for the org's members.** The owner hands them out with `hi
   serve key`, or the org server gives each member their own, as `hi org
   ai` does with signed requests?
6. **Teams using a served thing.** A team's boxes reach only what their
   proxy allows; should a team's owners be able to allow a served thing's
   VPN address, and is that shown in `hi org services`?
7. **Recipe names without a kind's recipe.** Should `hi serve web <folder>`
   mean `static` when a kind has one obvious recipe? The proposal is no:
   one grammar, `hi serve <kind> <recipe>`, and `hi serve web` lists the
   recipes.
8. **Speech and embeddings.** Recipes of `llm`, which speak the same
   OpenAI-compatible API (`/v1/embeddings`, `/v1/audio/transcriptions`),
   or kinds of their own?
9. **Rented hardware for other kinds.** A database on a rented machine
   loses its data with the lease, so the proposal is `llm` only.
