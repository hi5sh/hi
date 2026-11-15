# `hi server vault`: the server's own encrypted store for every secret hi keeps

Status: Approved (2026-11-14), after its open questions were decided
one by one the same day (Open questions). Release 0 built as v2.6.152.
It replaces the draft `hi_app_vault.md` (2026-11-13: an app's settings from a team's
own Vault, by a login per app), which is folded in here: a team's own
OpenBao or Vault becomes this store's release 3, not its starting point.

Dependencies: [hi_server_apps.md](hi_server_apps.md) (a hi
server on every machine with apps, one admin page),
[hi_app_keys.md](hi_app_keys.md) (an app's settings and
`secrets.json`), [hi_org.md](hi_org.md) (the server's
`keys.json`, audit log, and admin socket),
[hi_app_snapshots.md](hi_app_snapshots.md) (what a snapshot
and an offsite copy hold).

## Goal

Every secret hi keeps sits in one store on the machine's hi server,
encrypted on disk, readable only while the store is unlocked:

- **Encrypted at rest.** A copied disk, a VM image, a backup of the
  server's state, or an offsite copy holds no secret in the clear.
- **One place.** The server's provider and AI keys, the apps' settings,
  the Slack, NetBird, DNS, and mail tokens: one store, one audit, one
  way to rotate.
- **History.** Each secret keeps every version; a change can be rolled
  back, and a leaked value forgotten.
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
                                       # create it (hi update does, with tpm or file); the recovery key waits for the owner
hi server vault unlock                 # asks for the passphrase or the recovery key, without echo
hi server vault lock                   # forget the key now; apps' boxes keep running
hi server vault unlock-with add|remove tpm|file|passphrase
                                       # add or remove a slot (unlocked first); the secrets aren't re-encrypted
hi server vault recovery               # the owner, in a terminal: show the waiting recovery key once, two words back
hi server vault recovery --new         # the owner, unlocked: a new recovery key; the old one stops working
hi server vault ls [<prefix>]          # paths, versions, when changed; never values
hi server vault history <path>         # versions, who changed each, when
hi server vault rollback <path> <version>
hi server vault forget <path> --version <n>
                                       # destroy one version's value (leaked); its history entry stays
hi server vault shared set <name>      # admins: a key for several apps, asked without echo
hi server vault shared give <name> <app>...
                                       # admins: let these apps use it; take back with --take
hi server vault shared rm <name>       # admins; refused while an app uses it
hi server vault move vault://<mount> [--login jwt|approle]
                                       # copy every path to an OpenBao or Vault, check, and use it (release 3)
hi server vault connect <prefix> vault://<mount> [--login jwt|approle]
                                       # keep paths under a prefix in a team's OpenBao or Vault (release 3)
hi server vault login [--new-key]      # how hi signs in to a team's store, and what to hand its owners (release 3)
```

What sets a secret doesn't change: `hi app env`, `hi app key`,
`hi server provider add`, `hi server slack setup`, the page's Keys. They
write to the store instead of a file. There is no `hi server vault get`
that prints a value.

`hi app rm` keeps the app's paths, as it keeps its snapshots;
`hi app rm --purge` deletes them too (see Removing an app).

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
hi/app/<app>/snapshots          the key its snapshots are encrypted with
hi/shared/<name>                a key for several apps, such as hi/shared/openrouter
```

Each path holds a set of names and string values, as a KV v2 secret
does.

### Shared keys

- A key several apps use, such as one OpenRouter key, is in
  `hi/shared/<name>`, never copied into an app's path. Only admins set,
  give, and remove one (`hi server vault shared`, or the page's Shared
  keys): it crosses apps.
- An app's owners use a shared key an admin gave their app by
  reference: `hi app env payments OPENROUTER_API_KEY --from
  shared/openrouter`. The app holds "this name comes from
  shared/openrouter", not the value.
- The page shows which apps use each shared key. A change reaches them
  as any change does (see Settings in a box). `rm` is refused while an app uses it;
  removing an app removes only its reference.
- The server's own keys for every app (the AI key, providers) stay in
  `hi/server/`.

### Versions

- Every version is kept, values too: the store holds a few bytes each,
  and a rollback can go as far back as needed.
- **`forget`** destroys one version's value, for a value that leaked.
  Its history entry stays: "version 4, set by …, value forgotten on
  …". The page offers it beside each version ("Leaked? Forget this
  value"), with what it can't do: backups of the state taken before
  still hold it, so change the secret at its source too.
- History entries (who changed which names, when) are never removed.

### Removing an app

- **`hi app rm`** keeps the app's paths, marked as a removed app's, as it
  keeps its snapshots. No box reads them: nothing runs by that name. It
  says so: "Its settings stay in the server's vault; hi app rm --purge
  payments deletes them."
- **Back:** `hi app up <folder>` with the kept folder, or a restore from
  its snapshots, gets its settings, tokens, snapshot key, and their
  history back. A new app that only has the name is asked first:
  "Settings from a removed app called payments are kept. Use them?
  [y/N]".
- **`hi app rm --purge`** deletes them as well: the settings, the
  snapshot key, and the local snapshots (one switch, since the snapshots
  are unreadable without their key). History entries stay, without
  values. It asks first, by name: "This deletes payments' settings, its
  snapshot key, and 14 snapshots. Its offsite copy in s3:… stays." What
  isn't hi's (the bucket, Slack, GitHub) stays, and it says so. The
  page's Removed apps has Purge, with the same question.

## How it unlocks

The data key is kept several times, each copy locked by one **slot**, as
LUKS does. Any one slot unlocks the store:

| Slot | After a reboot | Goes with a move to another machine | Protects against |
|------|----------------|-------------------------------------|------------------|
| **tpm**: sealed to the machine's TPM | unlocks by itself | no: sealed to this chip | backups, offsite copies, a copied state folder, a stolen or copied disk |
| **file**: a key file, 0400, in its own folder, never in a backup or a snapshot | unlocks by itself | only if you carry the file | backups, offsite copies, a copied state folder |
| **passphrase**: Argon2id from an admin's passphrase | when an admin types it | yes | the above, and a stolen machine |
| **recovery**: 24 words, always there | when an admin types them | yes | as the passphrase |

- **The default**, when `hi update` creates the store: **tpm** if hi can
  use the TPM without root, else **file**; and always **recovery**.
  `hi update` says which. Nothing changes for the apps: after a reboot
  the store unlocks by itself, as the plain files do now.
- **The tpm slot is `systemd-creds --user`** (systemd 256 or newer),
  which seals through systemd's own credentials service, so `hi` needs
  no group and no root. Tried on our three machines (2026-11-14, systemd
  259, Ubuntu 26.04):
  - In `--user` mode `--with-key=tpm2` alone is refused; `host+tpm2` is
    the one to use: the host's key (`/var/lib/systemd/credential.secret`,
    root's) and the TPM, and the user's ID, all needed to open it.
  - **It falls back to the host key alone, without a word,** when
    systemd's TPM support is partial, as it is on a fresh Ubuntu 26.04:
    `libtss2-rc0t64` isn't installed. The host key is on the disk, so a
    copied disk opens such a slot: no better than file. So hi checks
    `systemd-analyze has-tpm2` (exit 0 only with full support) before,
    and checks the sealed result after, and never calls a host-only slot
    tpm. The check after reads the credential's first 16 bytes, the
    sealing kind's ID: `55b9ed1d-3859-4d43-a831-9d2ebb332ac6` is host
    only; with the TPM it is another one, `ef4ac136-79a9-480e-a7db-68897f9f165d`
    seen on systemd 259 (tested 2026-11-15). Size is no test: a TPM one
    is ~540 bytes, a host-only one 130–170, depending on the name.
  - Where the TPM is there but the support partial, hi makes a file slot
    and the page and `hi doctor` say "This machine has a TPM; ask your IT
    team to install libtss2-rc0t64 (sudo apt install libtss2-rc0t64),
    and hi moves the store to it." Once it's installed, hi adds the tpm
    slot by itself and removes the file slot (`hi update`, or the server's
    next start), and says so.
  - A machine with no TPM, such as a VM without a virtual one, gets a
    file slot; the page says "No TPM: a copied disk opens this store.
    Ask your IT team for a virtual TPM, or add a passphrase."
  - The `tss` group, and hi talking to `/dev/tpmrm0` itself, aren't
    needed, so they aren't used.
- **Slots are added and removed** with `hi server vault unlock-with
  add|remove`, or on the page; only the data key's copy changes.
- **OR means the weakest slot sets the protection.** The page and
  `hi doctor` say in one line what the slots protect against together
  ("Protects backups and copies; a copied disk can unlock it (file
  slot)"). A slot that undoes a stronger one, such as file beside a
  passphrase, is allowed (moving from one to another needs both for a
  while) but flagged, with "Remove it?". A store with only tpm and
  recovery gets "Add a passphrase, so moving the server doesn't need the
  recovery key."
- **On another machine** the tpm slot can't open. Restoring or moving
  the server asks for the passphrase or the recovery key (or uses the key
  file, if you carried it) before the apps start. Once unlocked, hi seals
  a new tpm slot to the new machine's TPM in place of the old one,
  without asking; with no TPM hi can use, it offers a file slot and says
  what that protects against.
- **The recovery key**, 24 words, opens the store when no other slot
  can. There is one; a new one replaces it.
  - **It waits in the store** until the owner saves it: encrypted like
    any secret, never in a file or in the clear on disk. `hi update` and
    `init` say "The recovery key waits for the owner on the admin page."
  - **Only the server's owner** sees it, and makes a new one. The page
    shows the owner "Save your recovery key"; it is shown once, two of
    its words are asked back, and then hi forgets it. Admins see that it
    waits for the owner. On a machine without the page, `hi server vault
    recovery` in a terminal does the same.
  - **Not saved**, the page, `hi doctor`, and Slack (once a week) remind
    the owner. Lost, or never saved: while the store is unlocked, the
    owner makes a new one (`recovery --new`, or on the page).
  - **A new one** stops the old words at once, on this machine. Backups
    of the state taken before still hold the old slot, so the old words
    open those, with the secrets as they were; when a key may have
    leaked, the page says to change the secrets that matter at their
    source too.
  - **Never** by Slack or mail, which keep it on someone else's servers.
- **On the page**, a locked store is the first thing an admin sees, with
  Unlock: the passphrase goes from the browser to the page, on https or
  NetBird, and from the page to the server on its local socket. The
  page never keeps it.
- **Who hears it's locked:** the page's banner, `hi doctor`, and the
  heartbeats (see When the server is locked or down). hi sends nothing
  to Slack while locked: its own Slack token is in the store.
- **No automatic slot left:** when an admin removes the last tpm or
  file slot, the page warns first: "After a reboot, every app waits
  until someone unlocks. TPM and a passphrase keep them running and
  still protect a copied disk."

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
- The data key is wrapped once per **slot**: by the key file's key, the
  TPM (`systemd-creds --user` sealed to the TPM, or the TPM's own API
  through `tss`), Argon2id of the passphrase (64 MiB, 3 passes, a random
  salt), and Argon2id of the recovery key.
- On disk, in the server's state folder: `vault/` with one file per path
  (its versions, encrypted, every one kept) and `vault/keys.json` (the
  slots: each wrapped data key, its kind, its salt). Atomic writes, as now.
- No home-made cryptography: one AEAD and one KDF from the Go project's
  libraries. Rotating the data key re-encrypts every path; adding or
  removing a slot re-wraps only the data key.

## How a secret reaches whoever reads it

- **The server itself** reads its own paths in memory.
- **An app's service** and **the page** ask the server on its local
  socket, as the page does now (`admin.sock`), naming the app and the
  slot. The server answers with that slot's names and values only.
- **A box** gets its values in memory only (see Settings in a box).
- **Locked:** a box that starts waits, and says so in its deploy (`the
  server's vault is locked; an admin unlocks it on the admin page`);
  once unlocked, every waiting box starts by itself. Boxes already
  running don't notice. A box with nothing in the store for its slot
  doesn't wait. No plaintext copy is kept to start from, not even for
  production: a copy that starts without a person needs a key on the
  machine, which is a tpm or file slot.
- **Every read** is a line in hi's audit log: who (app and slot, the
  page, a command), which path, which version, which names.

The socket is the `hi` account's, as now, and is never mounted into a
box. Anything that runs as `hi` can ask; the boundary is the account,
not the process.

### Settings in a box

As OpenBao and Vault's own agent does (files it keeps current, or
environment variables for a process it starts), with the server as the
agent:

- **Files, kept current:** `/run/hi/settings/<NAME>`, one per name, and
  `/run/hi/settings.env`, all of them as an env file. On tmpfs, never on
  disk; read only in the box, 0400 for the app's user. Within a minute of
  a change (in the store, a shared key, a team's store) hi rewrites
  them, each in one step, so nothing reads half a value.
- **Environment variables at start:** the box starts the app through
  `hi-env`, which reads `/run/hi/settings.env` and then runs the app
  with them. podman is never given a value, so none is in its
  `config.json` on disk or in `podman inspect`. `hi-env <command>` in
  the box runs anything with the current values, so a worker started
  after a change has the new ones.
- **A change restarts nothing by default.** The files are new; a
  running process's environment can't be changed from outside, so it
  keeps its start's values, and whether it re-reads is the app's
  business. The page and `hi app env` say "production's files have
  version 13, its environment version 12". No signal either: SIGHUP's
  default ends a Node or Python process. app.json can ask for one:
  - `"on_settings_change": "signal SIGHUP"` (or another signal), for an
    app that reloads on it;
  - `"on_settings_change": "restart"`, for one that only reads at start:
    a new box beside the old, as at a release, with no gap, and the old
    box kept if the new one doesn't start.
- **File settings** (`hi app env payments --file MONGODB_CERT=<path>`,
  a certificate with its key, say) stay at `/hi-files/<NAME>`, with the
  path in the variable, as now, but on tmpfs, never written to the app's
  folder on disk (today `run/<slot>-files/`). In the store a file is kept
  as bytes in its slot's path, marked as a file, so a binary one (a
  `.p12`) works. From a team's store, a key becomes a file when the app
  says so: `hi app env payments --from corp/payments/prod --file
  MONGODB_CERT`; a value there is a string, so a binary file is kept as
  base64 and `--base64` decodes it. A change replaces the file in one
  step, as a value's. At most 1 MiB a file: "Files bigger than this
  belong in the app's data, not its settings."
- **The dev box** gets the same files, and the same default.
- **A pin holds a slot** where a team wants changes only at its
  releases: `--from corp/payments/prod?version=12`, or a shared key's.
- **Placeholders for API keys:** `hi app env payments MARKET_KEY --for
  api.market.example` gives the box a placeholder; hi's proxy puts the
  real key on the box's calls to that host, as it does for Slack and
  OpenRouter. The key is in no file and no variable in the box, and a
  change needs no re-read. When a value looks like an API key, `hi app
  env` asks which host it is sent to; Enter skips it.

## When the server is locked or down

hi only sends heartbeats; who hears, and how (a Slack channel, mail,
SMS, a pager), is set up at the service, such as healthchecks.io. So it
works while the store is locked, with no token of hi's.

- **The machine's heartbeat** (`hi heartbeat <url>`, a systemd timer,
  every 5 minutes) checks the server first, on a machine that runs one:

  | The server | The heartbeat sends | The service |
  |------------|---------------------|-------------|
  | running, unlocked | a ping | nothing |
  | running, locked | `/fail`: "the server's vault is locked; unlock it on the admin page" | alerts at once |
  | not running | `/fail`: "hi-server.service isn't running" | alerts at once |
  | the machine is down | nothing | alerts after the check's grace |

- **An app's heartbeat** fails only when the app is hurt: it waits for
  the locked store (`/fail`: "waiting for the server's vault, locked"),
  or a deploy can't reach the server. While the server is down and the
  app runs on, its heartbeat pings as usual, with "the server isn't
  reachable since <time>" in the ping's text.
- **After 24 hours without the server**, a running app's helper says so
  in the app's ops channel, once: "This app hasn't reached the hi server
  since <time>. It runs as before; deploys and new settings wait until
  the server is back." And once more when it's back, with how long it
  was gone. The helper has its Slack token from its start; an app that
  restarted while the store was locked has none, and its heartbeat
  fails instead.
- **Heartbeat URLs stay outside the store**: the machine's in
  `heartbeat.json`, an app's beside its settings, so a locked store can
  still say it's locked. A leaked URL only lets someone send pings. As
  now, a URL is never printed or logged, only its host.
- **No heartbeat set** on a machine with a server: the page's banner and
  `hi doctor` say "Nobody would hear that the server is locked or down:
  set a heartbeat (hi heartbeat <url>)."

## Moving what exists

`hi update` to the release that brings it:

1. Creates the store with a tpm slot if hi can use the TPM, else a
   file slot (see How it unlocks), and a recovery key that waits in the
   store for the owner to save it.
2. Moves `keys.json`, `slack.json`, every app's `secrets.json` and
   snapshot key, `mail.json`, NetBird's token, and the DNS token into
   it, reads each back, and only then removes the files. Apps with a
   bucket get `hi-key.sealed` at once; it is only as good as a saved
   recovery key, which the page reminds the owner of.
3. Says what moved, by name. Nothing an admin sets changes.

A file the store can't take stays, and the page says which.

## OpenBao or Vault in its place (release 3)

One interface in hi, two implementations:

- **built in**, above;
- **OpenBao or Vault**, KV v2 over HTTP (login, read, write, metadata),
  written with `net/http`, no Vault library. The same code talks to both.

Two ways to use one:

- **Instead:** `hi server vault move vault://kv` copies every path to
  `kv/hi/…`, reads each back, and switches. The built-in store keeps
  hi's login to the team's store (`hi/machine/vault`, unlocked by its
  slots as before) and the copy of every path as a fallback until
  `hi server vault move --forget`. hi then logs in with a read-write
  role limited to `kv/data/hi/*` and `kv/metadata/hi/*`.
- **Beside it:** `hi server vault connect corp/ vault://kv/corp` keeps
  everything under `corp/` in the team's store, read only, and an app
  takes settings from there with `hi app env payments --from
  corp/payments/prod`. hi's own paths stay built in.

With a team's store:

- **A change there reaches the app.** Every minute the server reads each
  connected path's metadata (`GET /v1/<mount>/metadata/<path>`); a new
  `current_version` is a change of the app's settings, and reaches the
  boxes within a minute as any change does (see Settings in a box).
  The channel hears it once, by name and version, never values. A path
  pinned with `?version=7` doesn't move until it's unpinned: a rollback.
- **Logging in**, two ways (`--login jwt|approle` on `move` and
  `connect`, or the page), with `X-Vault-Namespace` when the team uses
  one. The token is in the server's memory only.
  - **jwt, recommended:** no secret is handed over. hi makes a key pair
    for this login alone, kept in the built-in store under
    `hi/machine/vault` (not `server_key`, so replacing it touches no
    device). Each login is a JWT it signs, good for 5 minutes, with the
    subject `hi-server:<name>`. The team's store checks it against the
    public key, registered once on a JWT auth mount
    (`jwt_validation_pubkeys`, in OpenBao and Vault).
    `hi server vault login --new-key` makes a new pair and prints the
    new public key; the old one works until the store's owners swap it.
  - **approle**, for a store that allows only that: the role ID and the
    secret ID, kept in the built-in store under `hi/machine/vault`. hi
    says when the secret ID nears its end (`secret_id_ttl`).
  - **What to hand the store's owners:** `connect` and `move`, and the
    page, print it ready to pass on, as the page lists NetBird's
    policies for IT: the auth mount, the role bound to the server's
    name, the policy, and the public key (jwt). For example:

    ```sh
    bao write auth/jwt/config jwt_validation_pubkeys=@hi-server-ws1.pub
    bao write auth/jwt/role/hi-server-ws1 role_type=jwt \
        bound_subject=hi-server:ws1 bound_audiences=hi \
        user_claim=sub token_policies=hi-corp-read token_ttl=15m
    ```

  - TLS certificates and cloud identities: later, if a team asks.

  For a connected prefix the policy is read only:

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
  in the team's store: see Two values for one name.
- **OpenBao and Vault:** the same API; namespaces are Vault Enterprise's
  and OpenBao's since v2.3. A KV v1 mount is read too, with no versions,
  and changes found by a hash of what was read. HashiCorp's Go client is
  MPL-2.0 but brings about 15 modules for four calls, so hi uses
  `net/http`.
- **Vault's own audit log** shows hi's role reading; hi's audit log
  says which app each read was for.

hi doesn't serve Vault's API: `bao kv get` and Terraform don't talk to
hi. A partial copy of that API would mislead tools that expect tokens,
policies, and its errors.

### Two values for one name

Nothing wins silently; a value's source is always one place, shown.

- **A hi setting over a team's store's:** setting a name that a `--from`
  already gives is refused unless it says so: "MAIL_URL comes from
  corp/payments/prod. --override sets it here instead (Stage only)."
  An override is per slot: Stage's leaves production on the team's
  value. `hi app env`, `hi server vault ls`, and the page mark it
  (`MAIL_URL  overridden here (Stage); corp/payments/prod has its
  own`). When the team's store changes that name, the channel hears
  "corp/payments/prod changed MAIL_URL; Stage overrides it and keeps its
  value."
- **Two sources for one name** (two `--from` paths, or one and a
  `--from shared/...`): the second `--from` is refused, naming the name
  and both sources, until the owner leaves the name out of one
  (`--from corp/payments/prod --skip DB_URL`) or sets it with
  `--override`.
- **A clash that comes later**, when a team's store adds a name another
  source already gives: the app keeps the value from the source that had
  it first, and the channel and the page say so, with the two ways to
  settle it. Nothing restarts for it.

## Snapshots, backups, and moves

- **The server's state backed up** has `vault/` encrypted; the key file,
  if any, is in a folder of its own that no backup takes.
- **An app's snapshot** has no secrets, as now.
- **An app's snapshot key** (restic's password, today the plain file
  `app-snapshots/<app>/key`) moves into the store, at
  `hi/app/<app>/snapshots`. hi gives it to restic through a pipe
  (`RESTIC_PASSWORD_COMMAND`), never a file.
- **A sealed copy beside each offsite repository**, so the recovery key
  alone reads the snapshots after the machine is lost:
  - When a recovery key is made, hi derives a key pair from it (X25519,
    by Argon2id from the words) and keeps only the public half in the
    store. So hi can seal to it at any time without the words, which it
    has forgotten.
  - With `--offsite` on, hi puts `hi-key.sealed` in the bucket, beside
    restic's repository: the snapshot key sealed to that public half
    (`nacl/box`, anonymous). It is written again when the snapshot key or
    the recovery key changes; `recovery --new` re-seals every app's copy
    and only then forgets the old public half.
  - **After losing the machine:** on a new one, `hi app restore <app>
    --from s3:https://<host>/<bucket>[/<path>]` asks for the bucket's
    keys and the recovery key, opens `hi-key.sealed`, puts the snapshot
    key in the new machine's store, and restores as `hi app restore`
    does.
  - Whoever has the recovery key can read every offsite snapshot; it
    already opens every secret in the store.
  - `hi app snapshots --show-key` stays, for an owner who wants their own
    copy too.
- **An app without a bucket** has only local snapshots, which go with the
  machine anyway; its key is just in the store. It is told, as now, "Not
  copied offsite (hi app backups)".
- **`hi app move`** carries the app's paths to the new machine's store,
  over the move's own connection, and removes them from the old one once
  the app runs there. Its references to shared keys go with it; a shared
  key the new machine's store doesn't have is copied, and its admins
  told.

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
- `internal/cli/heartbeat.go`, `app_heartbeat.go`: the server checked
  before the machine's ping, `/fail` with a reason; an app's ping when
  it waits or can't reach the server; the helper's 24-hour notice.
- `internal/cli/admin_*.go`, `helper/admin/`: the page's Vault section
  and a locked store's banner.
- Release 3: `internal/cli/vault_kv.go` (new), the OpenBao/Vault client.
- `site/guide/`: a page, The server's vault; Keys and Networking say
  where secrets are.

## Tests

- A value written and read back; a version can't be read under another
  path; a changed byte fails to decrypt.
- Each slot: file, a fake TPM, a passphrase; the recovery key unlocks
  without the others; it is shown only to the owner, once, then gone
  from the store; `--new` stops the old words; a wrong passphrase is slowed; adding or removing a slot
  leaves every secret readable.
- What the slots protect against together, and the flag for a slot that
  undoes a stronger one.
- On another machine (a fake TPM that doesn't match): the tpm slot
  fails, the passphrase unlocks, a new tpm slot replaces the old.
- The key handed across a restart through a stand-in for systemd; a
  reboot (no handed key) locks it.
- Locked: a box start waits and says so, and starts once unlocked; a
  box with nothing in the store doesn't wait; running boxes untouched.
- Heartbeats (a fake service): the machine's sends `/fail` with the
  reason when the server is locked or stopped; an app's fails when it
  waits, pings with the note when only the server is gone; the ops
  notice after 24 hours, once, and the one when it's back.
- Moving the files: each read back before its file goes; a failure
  leaves the file.
- Snapshot keys: restic gets the key through the pipe; `hi-key.sealed`
  opens with the recovery key's words and not with others; a new
  recovery key re-seals every bucket's copy; a restore on a fresh machine
  from a fake bucket with only the recovery key.
- Two values: a name a `--from` gives is refused without `--override`;
  an override is per slot, marked, and kept when the team's store
  changes; a second source for a name is refused; `--skip` leaves it
  out; a later clash keeps the first source's value and says so.
- Versions: every one kept; `forget` leaves the entry without the
  value. Shared keys: given apps read them by reference, others can't;
  `rm` refused while used; owners can't set or give one. `hi app rm`
  keeps the paths, `up` gets them back, a new app with the name is
  asked; `--purge` deletes paths, key, and local snapshots.
- An app's service gets only its slot; the dev box never gets Stage's or
  production's; no value in the audit log, the page's JSON, or an error.
- A backup of the state folder has no value in the clear and no key file.
- Settings in a box: the files change within a minute, in one step, on
  tmpfs; no value in podman's `config.json` or `podman inspect`;
  `hi-env` gives the current values; nothing restarts or is signalled
  unless app.json asks, and then only as asked; a `--for` placeholder
  reaches the host as the real key and nowhere else. File settings:
  on tmpfs, none in the app's folder; a binary one round-trips; one over
  1 MiB refused with the message; `--file` and `--base64` from a fake
  OpenBao.
- Release 3: a jwt login a fake OpenBao accepts with the registered
  public key and refuses after `--new-key` is swapped in; AppRole; the
  printed setup for the store's owners. A move to a fake OpenBao and back; a path under a connected
  prefix read from there; a new version reaches the files.

## Releases

0. **Settings off podman's disk**, on its own and first, since it is a
   leak today: settings reach a box through tmpfs files and `hi-env`,
   never podman's `--env`, so `config.json` and `podman inspect` hold
   none (for a running app, at its next start); `/hi-files` on tmpfs,
   not `run/<slot>-files/`; file settings at most 1 MiB; `on_settings_change`.
   Before the store exists, from `secrets.json`. Built in v2.6.152.
   - **`--for` placeholders**, a release of their own: Stage's and
     production's boxes reach the internet themselves, through a resolver
     that logs names (hi_watch.md), with no proxy on the way out to put
     a real key in. A placeholder needs one for the hosts named with
     `--for`: the box's calls to them go to hi's proxy (the resolver
     answers those names with the proxy's address), which ends TLS with
     a certificate from the app's own CA, as the lead's proxy does for
     Slack, and every other host stays as it is.
1. **The built-in store**, unlocked by a key file or the TPM; the
   heartbeats that tell a locked or stopped server; what hi
   keeps moved into it on `hi update`; the recovery key; `hi server
   vault`, `ls`, `history`, `rollback`; encrypted backups of the state.
2. **Passphrase unlock** and systemd's keeping across restarts; the
   page's Vault section, Unlock, and the locked banner.
3. **OpenBao or Vault:** `move`, `connect`, `--from` a connected prefix,
   changes in the team's store reaching the boxes.
4. **Later:** the key split between several admins (Shamir), and in
   managed mode ([hi_server_apps.md](hi_server_apps.md)) the
   central server's store for the machines it manages.

## Not in this

- Serving Vault's API from hi.
- Dynamic secrets (database credentials made per lease), OpenBao and
  Vault's own advice where a secret can be short lived: later.
- Updating a running process's environment: Linux has no way to.
- Keeping the server's own identity key (`server_key`) or devices'
  `device_key` in the store: the server needs its key to answer devices
  while locked.
- Separating hi's own processes from each other: one account, as now.

## Open questions

None left. Decided (2026-11-14):

1. **The default unlock:** slots, any one unlocking; tpm where hi can
   use it, else file, and always recovery (see How it unlocks).
2. **The recovery key:** one; kept in the store until the owner saves it
   from the page (or `hi server vault recovery` in a terminal), shown
   once, then forgotten; only the owner sees it or makes a new one.
3. **Snapshot keys:** in the store, and sealed to the recovery key in a
   file beside each offsite repository, so the recovery key and the
   bucket are enough after the machine is lost.
4. **Locked after a reboot:** apps wait and start once unlocked; no
   plaintext copy for production. Heartbeats, not Slack, tell it: the
   machine's fails at once when the server is locked or stopped; an
   app's fails when it waits; after 24 hours without the server, the
   app's ops channel hears it runs on.
5. **Versions kept:** all, values too; `forget` destroys a leaked one,
   its history entry stays. Shared keys in `hi/shared/`, used by
   reference, admins only. `hi app rm` keeps an app's settings;
   `--purge` deletes them with its snapshots.
6. **A name in both hi and a team's store:** neither wins silently; a
   hi value over a team's needs `--override`, per slot, and is marked;
   a second source for a name is refused (`--skip` or `--override`).
7. **A change, from any source:** settings files on tmpfs, rewritten
   within a minute; environment variables from the start, through
   `hi-env`, never podman's; no restart or signal unless app.json asks
   (`on_settings_change`). `--for` placeholders, suggested for API keys.
   Settings off podman's disk first, as release 0.
8. **Logging in to a team's store:** jwt signed by a key of its own
   (recommended, no secret handed over) or AppRole; hi prints what to
   hand the store's owners. Certificates and cloud identities later.
9. **File settings:** in the store as bytes, at `/hi-files` on tmpfs
   (release 0), from a team's store with `--file` (and `--base64`); at
   most 1 MiB a file.

