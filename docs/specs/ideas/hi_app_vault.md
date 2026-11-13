# `hi app env --from vault://`: an app's settings from the team's own Vault

Status: Draft (2026-11-13), for review. An idea from a team that already
keeps its secrets in Vault, run as a container of its own, and injects
them on the server so nothing crosses the internet. Nothing is built.

Dependencies: [hi_app_keys.md](../approved/hi_app_keys.md) (settings,
`secrets.json`, file settings), [hi_app.md](../approved/hi_app.md) (the
app's service, broker, and boxes), [hi_app_dev.md](../approved/hi_app_dev.md)
(the dev box's own settings, the dev page),
[hi_app_snapshots.md](../approved/hi_app_snapshots.md) (what a snapshot
leaves out), and the drafts [hi_app_move.md](hi_app_move.md) (what a move
carries) and, for release 3, [hi_server_apps.md](../approved/hi_server_apps.md) (the
hi server every app joins).

## Goal

A team that keeps its secrets in Vault (or OpenBao) points a hi app at a
path there, instead of copying each value into `hi app env`:

```sh
hi app env payments --from vault://kv/payments/prod
```

- **One place for a secret.** Vault stays where a value is set,
  changed, and rotated. hi doesn't keep a copy in `secrets.json`.
- **What Vault already gives:** version history and rollback (KV v2),
  an audit log of who read what, policies, namespaces.
- **Nothing new in the box.** The app's box gets the values as
  environment variables, as it does today. It never holds a Vault token,
  and the lead and the dev box still see names only.
- **A change in Vault reaches the app** without anyone running hi:
  Stage and production start again with the new values, as they do today
  after `hi app env`.

## What exists already

- Settings are in `secrets.json` in the app's folder, mode 0600, and
  reach an app's box at its start as environment variables
  (`appBoxEnv`, `internal/cli/app_keys.go`) or read-only files in
  `/hi-files`. Stage and production share `Env` and have their own
  (`EnvPreview`, `EnvProduction`); the dev box has only its own (`EnvDev`).
- The broker starts a slot again when `appSettingsStamp` changes: a hash
  of the settings in `secrets.json` and hi's version
  (`internal/cli/app_preview.go`).
- The values go to the engine in an env file under `run/`, which is
  removed once the box has started (`app_preview.go`, `writeTeamAppEnvFile`).
  hi keeps no copy of what a box started with.
- Snapshots leave out `secrets.json` and `run/`. `hi app move` carries
  `secrets.json` with the folder.

What's missing, next to Vault: no history of a setting, no rollback, no
record of who read or changed one, and a key several apps share is set
once per app.

## Idea in one line

hi's service on the machine logs in to the team's Vault with a read-only
AppRole, reads the path when a box starts, and gives the box the values
as it gives settings now; the broker checks the path's version every
minute and starts the slot again when it changes.

## Commands

```sh
hi app env payments --from vault://kv/payments/prod               # Stage and production
hi app env payments --stage --from vault://kv/payments/stage      # Stage alone
hi app env payments --production --from vault://kv/payments/prod  # production alone
hi app env payments --dev --from vault://kv/payments/test         # the dev box, from its next restart
hi app env payments --from vault://kv/payments/prod?version=7     # pinned: a rollback
hi app env payments --unset --from vault://kv/payments/prod
hi app env payments                                               # names, and where each comes from
```

`vault://<mount>/<path>` names a KV v2 secret: `kv` is the mount and
`payments/prod` the path in it. Each key of the secret becomes a
variable of the same name.

The first `--from` asks for the Vault itself, once per app:

```
Vault's address: https://vault.corp.example:8200
Namespace (Enter for none):
AppRole role ID: ********
AppRole secret ID: ********
✓ hi read kv/payments/prod (version 12): DATABASE_URL, STRIPE_KEY, WEATHER_API_KEY
```

hi checks the login and the read before it keeps anything, as `hi app
key` checks an OpenRouter key. `hi app vault payments` shows the
address, the namespace, and each path with its version and when it was
last read; `hi app vault payments --login` asks for the login again.

`hi app env payments` lists names only, as now, with their source:

```
DATABASE_URL      vault kv/payments/prod v12   Stage and production
STRIPE_KEY        vault kv/payments/prod v12   Stage and production
SENTRY_DSN        hi                           Stage and production
```

## How a value reaches the box

1. As a box starts, the broker logs in (`POST /v1/auth/approle/login`)
   unless it holds a token that is still good, and reads each path of
   that slot (`GET /v1/<mount>/data/<path>`, `?version=` when pinned),
   with `X-Vault-Namespace` when the app has one.
2. The values join the slot's settings in `appBoxEnv`: Vault's first, in
   the order the paths were added; then the ones set in hi, which win
   over a name from Vault (see [Open questions](#open-questions)).
3. They go into the env file as now, and the box starts.

The Vault token lives in the broker's memory only, never on disk or in a
box. The AppRole's role ID and secret ID are in `secrets.json`, beside
the Slack tokens. A name that isn't a valid variable, one hi sets itself
(`PORT`, `OPENROUTER_API_KEY`, … `appBoxEnvReserved`), or a value that
isn't a string is left out, and `hi app vault` says which and why.

The policy the team gives hi's AppRole is read-only, on the paths it
names:

```hcl
path "kv/data/payments/*"     { capabilities = ["read"] }
path "kv/metadata/payments/*" { capabilities = ["read"] }
```

## A change in Vault

Every minute the broker reads each path's metadata
(`GET /v1/<mount>/metadata/<path>`) and keeps its `current_version` in
memory. `appSettingsStamp` takes in each source and the version it last
saw, so a new version is a change of settings: Stage and production
start again with it, within a minute, as after `hi app env`. A pinned
path doesn't move until it's unpinned.

The channel hears it once, by name: "Settings from Vault changed
(kv/payments/prod, version 13): Stage and production started again with
them." No values, ever.

## When Vault is away

A box can need its settings at any time: a deploy, a restart after the
machine boots, a new hi. If Vault is down, sealed, or unreachable then,
the app shouldn't go down with it.

- After each good read, the broker keeps that slot's values from Vault in
  `run/<slot>-vault.json` (0600), with the version and time. `run/` is in
  no snapshot and no repository.
- When a read fails, the box starts from that copy, and the channel hears
  once: "Vault didn't answer (sealed); Stage started with kv/payments/prod
  version 12 from 40 minutes ago." hi tries again every minute and
  starts the slot again when Vault is back with a newer version.
- No copy yet (the first start): the box doesn't start, Stage's deploy
  fails saying so, and production keeps the box it has.
- A refusal (403: the policy or the AppRole changed) is told as a
  refusal, with what to ask the Vault's owners for, not as an outage.

The cache is a trade: the values are on the machine's disk again, as
they are today in `secrets.json`, in exchange for an app that survives
a sealed Vault. The question of whether a team can turn it off is under
[Open questions](#open-questions).

## Where values from Vault don't go

- **The dev box** gets only its own `--dev` paths, never Stage's or
  production's, as with settings now. Its agents can read what it gets.
- **The lead** sees names and versions, never values.
- **Snapshots** have neither the values nor the login: `secrets.json`
  and `run/` are left out already.
- **A move** carries the login in `secrets.json`. The new machine must
  reach the Vault: `hi app move` checks the login and a read from there
  before it stops anything, and refuses otherwise.
- **The dev page** shows sources and versions under The app's settings,
  names only. Adding a source there is release 2.

## Vault and OpenBao

hi uses none of Vault's code. The calls are three HTTP requests (login,
read, metadata), written with `net/http` in a file of their own, so hi
gains no dependency, and the same code talks to OpenBao, whose API is
the same.

- HashiCorp's repository is under the Business Source License since
  August 2023. Its Go client, `github.com/hashicorp/vault/api`, is still
  MPL-2.0 and could be used, but it brings about 15 modules for three
  calls.
- Namespaces are Vault Enterprise's; OpenBao has them without a licence
  since v2.3. Without namespaces a team uses paths, which is what the
  `vault://` address names anyway.
- Version history and rollback are KV v2's, in both. A KV v1 mount has
  no versions: hi reads it, with no version to show or pin, and checks
  for changes by comparing a hash of what it read.

## Security

- **Over the wire:** the address must be `https://`, with the system's
  certificates or a CA the team gives (`--ca <file>`); plain `http://`
  only to `127.0.0.1`, for a Vault on the same machine. A Vault on the
  LAN or the NetBird network never sends a value over the internet.
- **At rest:** no better than today. Root on the machine can read
  `secrets.json`, the login in it, and the cache. What Vault adds is
  the record: every read by hi's AppRole is in the Vault's audit log,
  and the policy limits it to the app's paths.
- **Rotating hi's login:** a new secret ID with `hi app vault payments
  --login`; the old one can be revoked in Vault right after.

## What it costs

- About 300 to 400 lines: the client (`app_vault.go`), `--from` in `hi
  app env`, the merge in `appBoxEnv`, the version check and cache in the
  broker, `hi app vault`, and the move's check.
- Tests against a fake Vault (`httptest`), no real one.
- One request per path a minute to the team's Vault, per app.

## Other ways, and why not

- **Vault Agent in the box.** The box would hold a token that can read
  every path the policy allows, and renew it; the app's code, which
  agents write, can read the token. hi's own injection keeps the token
  outside the box.
- **The app's code calls Vault itself.** The same token in the box, and
  each app writes its own client and its own caching.
- **hi runs an OpenBao of its own** on the hi server, one namespace per
  app. A service to unseal, back up, and upgrade, which every app's
  restart would then depend on. Worth it only once many teams want a
  store and none has one.
- **Copy Vault into `secrets.json` on a schedule.** Two places for a
  secret, and hi's copy goes stale between syncs.

## Code that changes

- `internal/cli/app.go`: `appSecrets` gains the Vault login (address,
  namespace, CA, role ID, secret ID) and the sources per slot
  (`VaultSources`, `VaultSourcesProduction`, `VaultSourcesPreview`,
  `VaultSourcesDev`).
- `internal/cli/app_vault.go` (new): login, read, metadata; the cache;
  `hi app vault`.
- `internal/cli/app_keys.go`: `--from` in `appEnv`; `appBoxEnv` merges
  the slot's values; `appSettingsStamp` takes in the sources and their
  versions; `appEnvNames` gives each name's source.
- `internal/cli/app_preview.go`: the broker's minute check, and starting
  from the cache.
- `internal/cli/app_move.go`: the check from the new machine.
- The dev page: sources and versions under the app's settings.
- `site/guide/app/keys.md`: a section, "From your Vault".

## Tests

- A read: keys become variables; reserved and invalid names and
  non-string values are left out and reported.
- A name set in hi and in Vault: hi's wins (or the decided order).
- A new version in the fake Vault changes the stamp; a pinned path
  doesn't.
- Vault sealed (503) with a cache: the box starts from it, the channel
  hears once. Without a cache: Stage's deploy fails, production stays.
- 403: told as a refusal, the cache kept.
- The dev box never gets Stage's or production's paths; the lead's names
  list has no values.
- No token or value in any file but `secrets.json` and the cache, and
  none in the engine's command line.
- `http://` refused except to `127.0.0.1`.

## Releases

1. `--from` on the machine, AppRole or a token, the version check, the
   cache, the move's check, the guide.
2. Sources on the dev page, and the Slack command for owners.
3. **The server's Vault,** on top of [hi_server_apps.md](../approved/hi_server_apps.md)
   (an app needs a hi server; one admin page for its machines). See
   [One Vault for the server's apps](#one-vault-for-the-servers-apps).

## One Vault for the server's apps

Release 3. Instead of a login per app, the hi server's admins connect one
Vault (or OpenBao) for every app on the server's machines, once, on the
server's admin page.

- **Vault runs apart from the server,** on a VM or container of its own.
  The server restarts on every `hi update`; Vault restarting is what
  seals it, and that's rare. A server restart doesn't touch Vault.
- **The server is the only one that logs in to Vault.** It holds the one
  AppRole login. A machine's broker asks the server for its apps' values
  over the connection it already has, signed with its device key, and no
  machine's `secrets.json` holds a Vault login.
- **The server gives a machine only its own apps' paths.** It knows
  which apps run where; a machine asking for another machine's app is
  refused.
- **Paths by name:** each app's are under `kv/hi/<app>/<slot>` unless the
  app names others, so `hi app env payments --from vault://` needs no path.
- **When the server is away** (seconds, for an update), a box that starts
  uses the cache, as when Vault is away. Boxes already running don't
  notice.
- **When Vault restarts** it is sealed until it's unsealed by hand or by a
  key kept elsewhere; boxes start from the cache meanwhile, and the
  server's page says Vault is sealed.
- **A team's own Vault** (releases 1 and 2) still works beside it, for an
  app whose team keeps its secrets there.

Whether hi should run that OpenBao itself stays as in
[Other ways](#other-ways-and-why-not): only once many teams want a store
and none has one.

## Not in this

- File settings from Vault (a certificate as `/hi-files/<name>`).
- Dynamic secrets (database credentials Vault makes per lease) and lease
  renewal.
- Writing to Vault: hi only reads.
- Running a Vault or OpenBao for the team.

## Open questions

1. **Which wins** when a name is set in hi and in Vault? Proposed: hi's,
   so an owner can override one value for Stage without touching Vault.
   The other way keeps Vault the only truth.
2. **Restart on every change, or at the next deploy?** Proposed: on
   every change, as `hi app env` does now; a team that rotates often may
   want production to wait.
3. **The cache:** on by default, and can a team turn it off for an app
   whose values must never be on hi's disk?
4. **Other logins:** JWT/OIDC with a token from the machine's identity,
   or TLS certificates, instead of an AppRole secret ID in
   `secrets.json`.
5. **File settings:** a key named `MONGODB_CERT` holding a PEM could go
   to `/hi-files` when the app marks it a file. Worth it in release 1?
