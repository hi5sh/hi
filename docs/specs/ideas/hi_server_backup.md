# `hi server snapshot` and `hi server restore`: backups of the server and its vault

Status: Draft (2026-11-19), for review.

Dependencies: [hi_server.md](../approved/hi_server.md) (the server's state,
its key, devices), [hi_server_vault.md](../approved/hi_server_vault.md)
(the vault, its slots, the recovery key, and the sealed key beside an
app's offsite copy), [hi_app_snapshots.md](../approved/hi_app_snapshots.md)
(restic, the timer, retention, offsite, which this follows),
[hi_server_apps.md](../approved/hi_server_apps.md) (machines and apps
reach their server), and a draft it sits beside:
[hi_server_peer.md](hi_server_peer.md) (the server's NetBird peer, whose
identity a snapshot then holds).

## Goal

Apps have snapshots: daily, before each release, offsite to a bucket, and
restorable with the recovery key after the machine is lost. The server
has none. Its users, devices, policy, audit, and every secret in the vault
are on one disk, and a copy is whatever an admin made by hand before a
move.

- **Lose the machine, keep the server.** With the recovery key and a
  bucket's keys, a new machine runs the same server: same server key, same
  devices and users, same secrets. Devices keep working once they reach
  it.
- **Undo a mistake.** A removed user, a broken policy, or a bad change put
  back as it was yesterday, with one command.
- **Nothing in the clear.** A snapshot, local or offsite, opens only with
  its key, and that key opens only with the recovery key or the vault.

Apps are not in this: each app has its own snapshots
([hi_app_snapshots.md](../approved/hi_app_snapshots.md)). A server restored
on a new machine has its apps' secrets in its vault, so the apps can be
restored after it with `hi app restore --from`.

## What a snapshot holds

The server's folder and the vault, as one restic snapshot:

| What | Where | Why it's needed |
|------|-------|-----------------|
| The server's key | `org/server_key` | Devices trust the server by it: a new key means every device joins again |
| Users, devices, requests, leases, template sources | `org/state.json` | Who may do what, and every device's key |
| Owner and admins | `org/people.json` | Who approves |
| Policy, settings, the AI's settings | `org/policy.json`, `config.json`, `ai.json` | How the server decides |
| The audit, and the AI's usage | `org/audit.jsonl`, `ai_usage.jsonl` | What happened, and spend |
| The vault | `vault/keys.json`, `vault/paths/`, `vault/shared.json` | Every secret: provider keys, Slack, apps' settings, snapshot keys |
| The server's NetBird peer, with [hi_server_peer.md](hi_server_peer.md) | `org/net/peer` | The same name and address on a new machine |

Not in a snapshot:

- **The vault's key file**, when the vault opens with one: it is in
  `~/.config/hi/vault-key`, outside hi's state, so that nothing holding
  the vault holds what opens it.
- **The template mirrors** (`org/templates/`): the server fetches them
  again from their sources, whose tokens are in the vault.
- **Sockets**: `admin.sock`, and the peer's.

The vault's slots are in `keys.json`, and so in the snapshot: the TPM slot
(useless on another machine), a passphrase slot if there is one, and the
recovery slot. That is what lets the recovery key open a restored vault
anywhere.

`server_key` is in the clear on the machine today, as it is on any server.
In a snapshot it is encrypted with the snapshot's key.

## Taking one

The server takes its own snapshots, since it holds the moving parts. The
`hi server snapshot` command asks it on `admin.sock`, and takes one itself
only when the server is down.

1. The server takes its state lock and the vault's, so no request,
   approval, or secret is half written. This lasts as long as it takes to
   copy a few megabytes.
2. It copies what a snapshot holds into a folder only its user can read
   (`~/.local/share/hi/server-snapshots/staging/`), and lets go of the
   locks.
3. restic backs up that folder, and hi empties it.
4. hi copies the snapshot offsite in the background, when there is a
   bucket.

A server's state is small (a few megabytes, most of it the audit), so a
snapshot takes about a second, and a day's adds kilobytes.

## Where they are kept

A restic repository in `~/.local/share/hi/server-snapshots/repo/`, outside
the server's folder, and `status.json` beside it: the last snapshot, the
last offsite copy, and what failed.

**The snapshots' key** is in the vault, at `hi/server/snapshots`, as an
app's is at `hi/app/<app>/snapshots`. hi reads it from the vault for each
snapshot. With an offsite bucket, hi also puts a sealed copy in it,
`hi-key.sealed`, sealed to the vault's recovery key (as an app's offsite
copy has: `vault_offsite.go`). So **the recovery key and the bucket's keys
are all a restore on a new machine needs**.

## When

- **Daily**, from a systemd user timer next to the server's service
  (`hi-server-snapshot.timer`, around 03:30, after the apps' snapshots,
  caught up after a reboot). `hi server init` turns it on. After each
  daily snapshot hi forgets old ones: the last 7 days, 4 weeks, and 6
  months by default.
- **Before `hi update` restarts the server**, labelled with the version it
  updates from: a new version may change the state's format.
- **Before a restore**, so a restore can be undone.
- **By hand**, with `hi server snapshot`, or **Take a snapshot** on the
  admin page.

A snapshot or an offsite copy that fails is an incident (in Incidents, and
in the ops channel when Slack is set up), until one succeeds.

## Commands

```text
hi server snapshot                   take one now; the server keeps running
hi server snapshots                  list them: when, why, how much was new, offsite or not
hi server restore <snapshot>         put this server back as it was then
    <snapshot>                       an ID, latest, a day (2026-11-18: its last),
                                     or the version of a hi update (v2.6.220)
    [--vault]                        the vault too; without it, the vault stays as it is
    [--yes]                          don't ask first
hi server restore --from <repository> [--snapshot <id>]
                                     a server on this machine from a snapshot made on another:
                                     s3:https://<host>/<bucket>[/<path>], or a folder
hi server backups                    where snapshots are kept, how long, and offsite
    [--offsite s3:https://<host>/<bucket>[/<path>] | --offsite off] [--region R]
    [--keep <daily>,<weekly>,<monthly>]
```

`hi server init` installs restic, as `hi app new` does, or says how.

The owner restores and changes `backups`. Any admin takes a snapshot and
lists them. Each is in the audit.

## Restoring on the same machine

`hi server restore 2026-11-18`, to undo a mistake:

1. says what it puts back (users, devices, policy, people, settings) and
   what changed since, and asks;
2. takes a snapshot of how things are now (`before restoring <id>`);
3. stops the server;
4. puts the snapshot's server folder in place of this one, **keeping the
   audit**: the audit since the snapshot stays, with a line saying what
   was restored and the snapshot that undoes it;
5. starts the server again.

**The vault stays as it is without `--vault`.** It has its own history
and rollback for each secret (`hi server vault history`, `rollback`),
which undoes one secret without going back on the others. `--vault` puts
the whole vault back, for when that isn't enough.

Devices that joined after the snapshot aren't in it, so they have to be
approved again; `restore` names them first.

## Restoring on a new machine

The machine is gone. On a new one, with hi installed and on the same NetBird
network:

```sh
hi server restore --from s3:https://s3.example.com/hi-backups/server
```

1. hi asks for the bucket's keys (hidden, or `HI_SERVER_S3_ACCESS_KEY_ID`
   and `HI_SERVER_S3_SECRET_ACCESS_KEY`), and for the recovery key's 24
   words.
2. It opens `hi-key.sealed` with the recovery key, and with it the
   repository, and shows the snapshots: by default the latest.
3. **It makes sure the old server is gone.** If the old server's address
   answers with the same server key, hi stops: two servers with one key
   and one set of devices would each approve and spend on their own.
   `--replace` goes on when the old one can't be stopped, and says what
   that means.
4. It restores the server folder and the vault into hi's state, opens the
   vault with the recovery key, **seals it to this machine** (the TPM, or a
   key file where there is no TPM, as `hi server vault init` chooses), and
   removes the old machine's TPM slot, which can never open again.
5. It writes the server's service and the snapshot timer, starts the
   server, and takes a first snapshot here, offsite to the same bucket.
6. It says what's next:
   - **devices** reach the server at its old address, or at its new one.
     Without the server's own peer, the address is this machine's: each
     device runs `hi server join <new address>`, and since its key is
     known and approved, it's back at once, with its user and group. With
     [hi_server_peer.md](hi_server_peer.md), the peer's identity comes
     back with the snapshot, at the same name and address, and devices
     don't notice;
   - **apps** on lost machines: `hi app restore <app> --from <their
     bucket>`, whose keys are in this vault;
   - **the admin page** on this machine, if it should run here:
     `hi server init` adds it.

A repository folder (`--from /mnt/usb/server-snapshots`) works the same
way, for a copy carried by hand. Its key is then read from the folder's
`hi-key.sealed`, as from a bucket.

## Offsite

As for apps: `--offsite` takes an S3-compatible restic repository URL and
`--region` its region (`us-east-1` by default). hi initializes the bucket
with the local repository's chunker parameters, so copies deduplicate, and
after each snapshot runs `restic copy` to it. The bucket's keys and URL are
in the vault at `hi/server/offsite`.

**hi turns offsite on only once the recovery key is saved** (`hi server
vault recovery`). Until then, hi-key.sealed would be sealed to a key nobody
has yet, and a bucket no one could open after the machine is lost would
look like a backup.

The same bucket can hold the apps' copies too, under paths of their own.

## The admin page

The server's **Overview** has a **Snapshots** card:

- the last snapshot and the last offsite copy, and what failed;
- **Take a snapshot** for an admin;
- for the owner, the list, with **Restore…** for one (the same check and
  question as the command, the vault only when ticked), and the offsite
  bucket with **Change…**;
- a note when there is no offsite copy: the snapshots are on this
  machine's disk only.

Restoring on a new machine stays a command: the page runs on the server
that is gone.

## Security

- **The snapshots are encrypted** with their key, which is in the vault,
  and sealed to the recovery key in the bucket. A copied disk, a bucket, or
  a stolen USB copy opens with neither.
- **The recovery key opens everything**: the vault and every snapshot of
  the server and its apps. That is already true of the vault, and the
  guide says to keep it where the machine isn't.
- **The key file stays out** of every snapshot.
- **Two servers with one key** is the one thing a restore must not make:
  hence the check that the old server is gone, and `--replace` only when it
  can't be.
- **The audit is never shortened** by a restore on the same machine.

## Considered and rejected

- **The server in a container, backed up as an image.** An image holds the
  binary and the OS, which `hi install` gives back. What matters is the
  state, the same folder either way.
- **Copying the folders to a bucket as they are.** The vault would be
  safe, since it's encrypted, but `server_key` and the state would not be,
  and there would be no history to go back to.
- **The snapshots' key in a file beside the repository,** as apps had
  before the vault. A copy of the machine's disk would then hold both the
  repository and its key.
- **One snapshot for the server and its apps.** They're restored at
  different times for different reasons, and an app's data is much larger.

## Releases

1. **Snapshots on the machine**: `hi server snapshot`, `snapshots`, the
   timer, a snapshot before `hi update` restarts the server, `restore` on
   the same machine (`--vault`), incidents when one fails.
2. **Offsite and a new machine**: `hi server backups --offsite`,
   hi-key.sealed, `restore --from`, sealing the vault to the new machine,
   the check that the old server is gone, and a known device joining again
   at once.
3. **The admin page's Snapshots card.**

## Tests

- A snapshot holds the server folder and the vault, without the key file,
  the template mirrors, or sockets; its tags say why it was taken.
- The state lock and the vault's are held while the copy is made, and let
  go when the copy fails.
- `hi update` takes a snapshot labelled with the version before it
  restarts the server; a failed one warns, and doesn't stop the update.
- `restore` picks a snapshot by ID, `latest`, day, and version; takes a
  snapshot first; keeps the audit since, with a line saying what was
  restored; keeps the vault without `--vault`, and puts it back with it.
- Offsite is refused until the recovery key is saved; hi-key.sealed opens
  with the recovery key's words and nothing else.
- `restore --from` on a new machine: the vault opens with the recovery key,
  gets this machine's slot, and loses the old machine's TPM slot; the
  server starts with the same server key; it stops when the old address
  answers with the same key, and goes on with `--replace`.
- A device whose key is known joins again at once, with its user and
  group.
- With restic on the machine, a real round trip: a snapshot, offsite to a
  local S3 stand-in, and a restore into an empty home.

## Open questions

1. **The admin page's own settings** (`admin.json`: the machine's admins,
   its links, its reach). On a machine that runs only the server, the page
   comes with it, so they could be in the snapshot. On a machine with
   apps, they belong to the machine. Snapshot them, or have `hi server
   init` set the page up again after a restore?
2. **A snapshot before every vault change** (`hi server vault move`,
   `connect`, a rollback)? The vault's history covers each secret, but not
   its slots.
3. **Retention for the audit**: snapshots keep it as it was, and the audit
   grows. Is 6 months of monthly snapshots right for a file people may
   want to keep for longer, or shorter?
4. **Restoring an old snapshot after a hi update has changed the format**:
   hi reads the state of older versions today. Should `restore` refuse a
   snapshot newer than the running hi?

## Not in this

- Apps' snapshots, and an app's restore on another machine
  ([hi_app_snapshots.md](../approved/hi_app_snapshots.md),
  [hi_app_move.md](hi_app_move.md)).
- Moving a running server to another machine on purpose (`hi server move`):
  a restore from a fresh snapshot does it, with the old server stopped
  first. A command for it can follow once this and
  [hi_server_peer.md](hi_server_peer.md) are built.
- A team store (OpenBao, Vault) the vault is connected to: the team backs
  that up itself.
