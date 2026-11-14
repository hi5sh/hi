# `hi server vault`: the server's own encrypted store for every secret hi keeps

Status: Draft (2026-11-14), for review. Nothing is built. It replaces
the draft `hi_app_vault.md` (2026-11-13: an app's settings from a team's
own Vault, by a login per app), which is folded in here: a team's own
OpenBao or Vault becomes this store's release 3, not its starting point.

Dependencies: [hi_server_apps.md](../approved/hi_server_apps.md) (a hi
server on every machine with apps, one admin page),
[hi_app_keys.md](../approved/hi_app_keys.md) (an app's settings and
`secrets.json`), [hi_org.md](../approved/hi_org.md) (the server's
`keys.json`, audit log, and admin socket),
[hi_app_snapshots.md](../approved/hi_app_snapshots.md) (what a snapshot
and an offsite copy hold).

## Goal

Every secret hi keeps sits in one store on the machine's hi server,
encrypted on disk, readable only while the store is unlocked:

- **Encrypted at rest.** A copied disk, a VM image, a backup of the
  server's state, or an offsite copy holds no secret in the clear.
- **One place.** The server's provider and AI keys, the apps' settings,
  the Slack, NetBird, DNS, and mail tokens: one store, one audit, one
  way to rotate.
- **History.** Each secret keeps its last versions; a change can be
  rolled back.
- **A record.** hi's audit log says which app or command read which
  names, when; never a value.
- **Replaceable.** A team that runs OpenBao or Vault moves the same paths
  there with one command, or keeps some paths there, and no app notices.
- **Nothing new for an app.** Its box gets its settings as environment
  variables, as now. It never sees the store, a token, or an address.

## What exists already

Every secret hi keeps is a plain file, mode 0600, as the `hi` account:

| Secret | Where now |
|--------|-----------|
| Provider keys (RunPod, Shadeform), the AI key, data and template tokens | the server's `keys.json` |
| The server's Slack tokens | the server's `slack.json` |
| An app's settings, its Slack tokens, its OpenRouter key | the app's `secrets.json` |
| Mail settings and password | the apps' `mail.json` |
| NetBird's API token | `netbird-api-token` |
| The DNS provider's token (`hi net domain`) | its settings file |
| An app's snapshot password and offsite bucket keys | the app's snapshot key and backups settings |

Snapshots leave out `secrets.json`, but a backup of the server's state,
a copy of the VM, or the machine's disk has them all.

## Idea in one line

`hi server` keeps a store whose data key is random and never on disk in
the clear; it is unlocked by a key file, the machine's TPM, or an admin's
passphrase; the unlocked key survives `hi update` in systemd's keeping;
everything hi reads a secret from goes through one interface, whose
other implementation is a team's OpenBao or Vault.

## What it protects, and what not

- **Protects:** a stolen or copied disk (with the TPM or a passphrase), a
  copied VM image, backups of the server's state, offsite copies, a
  state folder sent along by mistake.
- **Doesn't protect:** a running, unlocked machine. The key is in the
  server's memory, and the apps, the page, and the server all run as the
  `hi` account: root, or anyone who is `hi`, can read it. That is as
  with OpenBao or Vault on the same machine.
- **A box** gets its own values in the clear, as now; it has to, to use
  them. It gets only its own.

The guide says this plainly. The store is not a sandbox between hi's own
processes.

## Commands

```sh
hi server vault                        # locked or unlocked, how it unlocks, the store, counts
hi server vault init [--unlock file|tpm|passphrase]
                                       # create it (hi update does, with file or tpm); prints the recovery key once
hi server vault unlock                 # asks for the passphrase or the recovery key, without echo
hi server vault lock                   # forget the key now; apps' boxes keep running
hi server vault unlock-with tpm|file|passphrase
                                       # change how it unlocks (asks for the current way first)
hi server vault recovery --new         # a new recovery key; the old one stops working
hi server vault ls [<prefix>]          # paths, versions, when changed; never values
hi server vault history <path>         # versions, who changed each, when
hi server vault rollback <path> <version>
hi server vault move vault://<mount>   # copy every path to an OpenBao or Vault, check, and use it (release 3)
hi server vault connect <prefix> vault://<mount>
                                       # keep paths under a prefix in a team's OpenBao or Vault (release 3)
```

What sets a secret doesn't change: `hi app env`, `hi app key`,
`hi server provider add`, `hi server slack setup`, the page's Keys. They
write to the store instead of a file. There is no `hi server vault get`
that prints a value.

## Paths

The same layout in the built-in store and in an OpenBao or Vault, so a
move copies it as it is:

```
hi/server/providers/runpod      the server's keys
hi/server/ai
hi/server/slack
hi/server/data/<org>
hi/server/templates/<source>
hi/machine/netbird              the machine's tokens
hi/machine/dns
hi/machine/mail
hi/app/<app>/shared             an app's settings for Stage and production
hi/app/<app>/stage
hi/app/<app>/production
hi/app/<app>/dev
hi/app/<app>/hi                 its Slack tokens and OpenRouter key
hi/app/<app>/offsite            its offsite bucket's keys
```

Each path holds a set of names and string values, as a KV v2 secret
does.

## How it unlocks

| Unlock | After a reboot | Protects against | For |
|--------|----------------|------------------|-----|
| **file**: a key file, 0400, in its own folder, never in a backup or a snapshot | unlocks by itself | backups, offsite copies, a copied state folder | the default where there is no TPM |
| **tpm**: the key sealed to the machine's TPM | unlocks by itself, on this machine only | the above, and a stolen or copied disk | the default where there is a TPM |
| **passphrase**: Argon2id from an admin's passphrase | stays locked until an admin unlocks it | the above, and a stolen machine | a team that accepts its apps down after a reboot until then |

- **The recovery key** is printed once, at `init`: 24 words, or a code
  to print. It unlocks in any mode. Without it and the passphrase (or the
  TPM, or the key file), the secrets are gone; `init` says so, and asks
  for the first and last word back.
- **On the page**, a locked store is the first thing an admin sees, with
  Unlock: the passphrase goes from the browser to the page, on https or
  NetBird, and from the page to the server on its local socket. The
  page never keeps it.
- **Slack** hears once when the store is locked after a reboot, and
  which apps wait for it; never asked for the passphrase.

### Restarts don't lock it

The server restarts on every `hi update`. Once unlocked, the server
gives systemd a memory-only file (`memfd`) holding the data key
(`FileDescriptorStoreMax=1`, `FileDescriptorStorePreserve=yes` in
`hi-server.service`). systemd keeps it while the user's service manager
runs and hands it to the server's next start. So `hi update`, a crash,
and the page's Restart leave the store unlocked; a reboot, or the
user's manager stopping (no lingering), locks it. Nothing new is exposed:
the `hi` account could read the running server's memory already.

A machine whose systemd can't keep a file (older than 254) unlocks again
after each restart: by itself with file or tpm, by an admin with a
passphrase, which `hi update` then warns about before it restarts.

## The store

- A random 256-bit **data key**. Each secret version is encrypted with
  XChaCha20-Poly1305 (`golang.org/x/crypto`, already an indirect
  dependency), with the path and version as associated data, so a
  version can't be moved to another path or another version.
- The data key is wrapped by the **unlock key**: the key file's, the
  TPM's (`systemd-creds` sealed to the TPM, or the TPM's own API), or
  Argon2id of the passphrase (64 MiB, 3 passes, a random salt). The
  recovery key wraps it a second time.
- On disk, in the server's state folder: `vault/` with one file per path
  (its versions, encrypted, the last 10 kept) and `vault/keys.json` (the
  wrapped data key, the salt, the unlock mode). Atomic writes, as now.
- No home-made cryptography: one AEAD and one KDF from the Go project's
  libraries. Rotating the data key re-encrypts every path; changing the
  unlock way re-wraps only the data key.

## How a secret reaches whoever reads it

- **The server itself** reads its own paths in memory.
- **An app's service** and **the page** ask the server on its local
  socket, as the page does now (`admin.sock`), naming the app and the
  slot. The server answers with that slot's names and values only.
- **A box** gets its values as now: the app's service writes them into
  the env file under `run/`, which is removed once the box has started.
- **Locked:** a box that starts waits, and says so in its deploy (`the
  server's vault is locked; an admin unlocks it on the admin page`);
  boxes already running don't notice. No plaintext copy is kept to start
  from: with the built-in store, the store is on the same machine as the
  app.
- **Every read** is a line in hi's audit log: who (app and slot, the
  page, a command), which path, which version, which names.

The socket is the `hi` account's, as now, and is never mounted into a
box. Anything that runs as `hi` can ask; the boundary is the account,
not the process.

## Moving what exists

`hi update` to the release that brings it:

1. Creates the store, unlocked by the TPM if the machine has one, or a
   key file, and prints where the recovery key is: in a file, 0400,
   that the admin is told to move off the machine and delete
   (`hi server vault recovery` shows it until then, never after).
2. Moves `keys.json`, `slack.json`, every app's `secrets.json`,
   `mail.json`, NetBird's token, and the DNS token into it, reads each
   back, and only then removes the files.
3. Says what moved, by name. Nothing an admin sets changes.

A file the store can't take stays, and the page says which.

## OpenBao or Vault in its place (release 3)

One interface in hi, two implementations:

- **built in**, above;
- **OpenBao or Vault**, KV v2 over HTTP (login, read, write, metadata),
  written with `net/http`, no Vault library. The same code talks to both.

Two ways to use one:

- **Instead:** `hi server vault move vault://kv` copies every path to
  `kv/hi/…`, reads each back, switches, and keeps the built-in store
  locked as a fallback until `hi server vault move --forget`. hi then
  logs in with a read-write AppRole limited to `kv/data/hi/*` and
  `kv/metadata/hi/*`.
- **Beside it:** `hi server vault connect corp/ vault://kv/corp` keeps
  everything under `corp/` in the team's store, read only, and an app
  takes settings from there with `hi app env payments --from
  corp/payments/prod`. hi's own paths stay built in.

With a team's store:

- **A change there reaches the app.** Every minute the server reads each
  connected path's metadata (`GET /v1/<mount>/metadata/<path>`); a new
  `current_version` is a change of the app's settings, and Stage and
  production start again with it within a minute, as after `hi app env`.
  The channel hears it once, by name and version, never values. A path
  pinned with `?version=7` doesn't move until it's unpinned: a rollback.
- **Logging in:** an AppRole (role ID and secret ID, kept in the built-in
  store under `hi/machine/vault`), with `X-Vault-Namespace` when the team
  uses one. The token is in the server's memory only. For a connected
  prefix the policy is read only:

  ```hcl
  path "kv/data/corp/*"     { capabilities = ["read"] }
  path "kv/metadata/corp/*" { capabilities = ["read"] }
  ```

- **Over the wire:** the address must be `https://`, with the system's
  certificates or the team's CA (`--ca <file>`); `http://` only to
  `127.0.0.1`.
- **When it's away** (down, sealed, unreachable): after each good read the
  server keeps the values, encrypted with the built-in store's key, and a
  box that starts uses that copy; the channel hears once ("The team's
  Vault didn't answer (sealed); Stage started with corp/payments/prod
  version 12 from 40 minutes ago."). With no copy yet, Stage's deploy
  fails saying so and production keeps the box it has. A refusal (403)
  is told as a refusal, with what to ask the Vault's owners for.
- **Names:** each key of a secret becomes a variable of the same name. A
  name that isn't a valid variable, one hi sets itself (`PORT`,
  `OPENROUTER_API_KEY`, …), or a value that isn't a string is left out,
  and `hi server vault ls` says which and why. A name set both in hi and
  in the team's store: hi's wins (Open questions).
- **OpenBao and Vault:** the same API; namespaces are Vault Enterprise's
  and OpenBao's since v2.3. A KV v1 mount is read too, with no versions,
  and changes found by a hash of what was read. HashiCorp's Go client is
  MPL-2.0 but brings about 15 modules for four calls, so hi uses
  `net/http`.
- **Vault's own audit log** shows hi's AppRole reading; hi's audit log
  says which app each read was for.

hi doesn't serve Vault's API: `bao kv get` and Terraform don't talk to
hi. A partial copy of that API would mislead tools that expect tokens,
policies, and its errors.

## Snapshots, backups, and moves

- **The server's state backed up** has `vault/` encrypted; the key file,
  if any, is in a folder of its own that no backup takes.
- **An app's snapshot** has no secrets, as now. An app's snapshot
  password stays out of the store in release 1: restoring after losing
  the machine must not need the machine's store (see Open questions).
- **`hi app move`** carries the app's paths to the new machine's store,
  over the move's own connection, and removes them from the old one once
  the app runs there.

## Security

- Locked, the store is useless without the unlock key or the recovery
  key; with file, the key file is never in a backup, snapshot, or move.
- Unlocked, no better than today against root or the `hi` account,
  as said above. Better than today against everything that copies files.
- The passphrase and the recovery key are never written, logged, or
  sent to Slack; the page forgets them once the server has them.
- A wrong passphrase: five tries, then one a minute, in the audit log.
- Values never in the audit log, the page, Slack, `hi server vault ls`,
  or an error.

## What it costs

- About 1,200 to 1,500 lines: the store and its encryption, the unlock
  ways and systemd's keeping, the socket calls, moving the files, the
  page's Vault, and the commands. Release 3 adds about 400.
- An admin's step after each reboot, with a passphrase only.
- Tests against a fake TPM and a fake OpenBao (`httptest`).

## Code that changes

- `internal/cli/vault.go` (new): the store, encryption, unlock ways,
  the recovery key, `hi server vault`.
- `internal/cli/vault_systemd.go` (new): the data key in systemd's
  keeping across restarts.
- `internal/cli/org_server.go`: the store opened at start; `keys.json`
  reads through it; the socket's read for an app's slot.
- `internal/cli/app_keys.go`, `app.go`: `appSecrets` and `appBoxEnv`
  read through the server's socket; `hi app env` writes there.
- `internal/cli/org_migrate.go`: the move from the files.
- `internal/cli/admin_*.go`, `helper/admin/`: the page's Vault section
  and a locked store's banner.
- Release 3: `internal/cli/vault_kv.go` (new), the OpenBao/Vault client.
- `site/guide/`: a page, The server's vault; Keys and Networking say
  where secrets are.

## Tests

- A value written and read back; a version can't be read under another
  path; a changed byte fails to decrypt.
- Each unlock way: file, a fake TPM, a passphrase; the recovery key
  unlocks in each; a wrong passphrase is slowed.
- The key handed across a restart through a stand-in for systemd; a
  reboot (no handed key) locks it.
- Locked: a box start waits and says so; running boxes untouched.
- Moving the files: each read back before its file goes; a failure
  leaves the file.
- An app's service gets only its slot; the dev box never gets Stage's or
  production's; no value in the audit log, the page's JSON, or an error.
- A backup of the state folder has no value in the clear and no key file.
- Release 3: a move to a fake OpenBao and back; a path under a connected
  prefix read from there; a new version restarts the slot.

## Releases

1. **The built-in store**, unlocked by a key file or the TPM; what hi
   keeps moved into it on `hi update`; the recovery key; `hi server
   vault`, `ls`, `history`, `rollback`; encrypted backups of the state.
2. **Passphrase unlock** and systemd's keeping across restarts; the
   page's Vault section, Unlock, and the locked banner; Slack told once.
3. **OpenBao or Vault:** `move`, `connect`, `--from` a connected prefix,
   changes in the team's store restarting the slot.
4. **Later:** the key split between several admins (Shamir), and in
   managed mode ([hi_server_apps.md](../approved/hi_server_apps.md)) the
   central server's store for the machines it manages.

## Not in this

- Serving Vault's API from hi.
- Dynamic secrets (database credentials made per lease).
- Keeping the server's own identity key (`server_key`) or devices'
  `device_key` in the store: the server needs its key to answer devices
  while locked.
- Separating hi's own processes from each other: one account, as now.

## Open questions

1. **The default unlock:** TPM where there is one and a key file where
   not (proposed), or ask at `hi update`?
2. **The recovery key:** printed to a file to move off the machine
   (proposed), shown once on the page, or split between the owner and an
   admin?
3. **Snapshot passwords:** outside the store (proposed, so a restore
   after losing the machine needs only the password), or in it, with the
   recovery key as the way back?
4. **Locked after a reboot:** apps wait (proposed), or production starts
   from an encrypted copy while Stage waits?
5. **Versions kept:** the last 10 per path (proposed), or by age?
6. **A name in both hi and a team's store:** hi's wins (proposed, so an
   owner can override one value for Stage), or the team's store, as the
   only truth?
7. **A change in a team's store:** restart Stage and production at once
   (proposed, as `hi app env` does), or production at its next deploy?
8. **Other logins to a team's store:** JWT/OIDC from the machine's
   identity, or TLS certificates, instead of an AppRole secret ID?
9. **File settings:** a secret holding a PEM to `/hi-files` when the app
   marks it a file?
