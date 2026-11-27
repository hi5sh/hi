# `hi server backups`: one bucket for every app, set on the server

Status: Approved (2026-11-26), with the draft's answers to its open
questions: the folder is the short hostname, set at setup; a key per
server is recommended and a shared one works; admins (of the server and
the machine) turn apps on and off; apps follow the server by default;
an app may keep a bucket of its own; the server's own state is release
2. Release 1 built as v2.6.268 (see [As built](#as-built-release-1)),
release 2 as v2.6.270 (see [As built](#as-built-release-2)), release 3
as v2.6.276 (see [As built](#as-built-release-3)).

Dependencies: [hi_app_snapshots.md](hi_app_snapshots.md)
(snapshots, restic, `hi app backups`), [hi_server_vault.md](hi_server_vault.md)
(the store, `hi-key.sealed`, the recovery key), [hi_server_apps.md](hi_server_apps.md)
(a server on every machine with apps), [hi_app_move.md](../ideas/hi_app_move.md).

## Goal

An admin sets one S3 bucket on the server, once, and every app on it is
copied offsite, on one schedule, seen on one page. Several servers can
use the same bucket, each in a folder of its own.

## What there is today

- Each app has its own restic repository on the machine
  (`~/.local/share/hi/app-snapshots/<app>/`), encrypted with its own key
  (in the vault at `hi/app/<app>/snapshots`).
- Each app has its own systemd timer: a snapshot every day around 03:00
  (plus up to 1 h at random), and before each release.
- Offsite is per app: `hi app backups <app> --offsite s3:…` or the app's
  **Copy offsite…**. Each app asks for an address and keys, and the keys
  are stored per app (`hi/app/<app>/offsite`). After each snapshot, hi
  runs `restic copy` to the bucket, then `forget --prune` there.
  `hi-key.sealed` beside each repository lets the recovery key open it
  after the machine is lost.
- Nothing copies the **server's own state** offsite: the vault, admins,
  policy, keys.json, Slack. Losing the machine loses them, and the app
  snapshots in the bucket do not hold them.
- Pain: 10 apps means 10 times the same address and keys, 10 places to
  change when a key rotates, and no view of which apps are covered.

## Idea in one line

The server holds the bucket, the keys, and the plan. Each app's
repository is `<bucket>/<server>/apps/<app>/`, and the server's own
state is `<bucket>/<server>/server/`. The admin page's new **Backups**
section turns apps on and off and shows each app's last copy.

## The bucket layout

```
s3://hi5-backups/                 one bucket, several servers
  aiw11/                          the server's name (its machine's short hostname)
    hi-server.json                who owns this folder: server ID, hostname, since when
    server/                       the server's state (restic), release 2
    apps/
      aitools/                    restic repository, as today
      aitools/hi-key.sealed       as today: the app's key, sealed to the recovery key
      patman/
      …
  aiw9/
    hi-server.json
    apps/…
  vmhiserver/
    server/
```

- **The server's name** is the machine's short hostname by default. The
  admin can change it once, at setup. After the first copy it is fixed,
  since moving a folder in a bucket means copying it.
- **`hi-server.json` claims the folder.** If a different server already
  claims it, hi refuses with "aiw11/ in this bucket belongs to another
  server (ID …, last copy …)". That stops two machines with the same
  hostname from writing over each other.
  - A machine rebuilt after a loss sees its old folder there. That is
    the restore case: hi offers `hi server restore` (release 3).
- **Each app keeps its own repository and key**, as today. One repository
  for the whole server would deduplicate across apps, but it would tie
  every app to one key and make a move copy everything.

## Several servers, one bucket

Two ways. Both work, and the page explains both:

| | One key for all servers | One key per server (recommended) |
|---|---|---|
| Setup | Paste the same key ID and secret on each server | In Backblaze, make a key per server with **File name prefix** `aiw11/` |
| If one machine is broken into | The attacker can delete every server's backups (they can't read them: each app's data is encrypted with its own key) | Only that server's folder is at risk |
| Rotating | One key, changed on every server | One server at a time |

To check during release 1: restic's `init`/`copy`/`prune` with a
Backblaze key limited to a bucket and a name prefix (it should only need
list and read/write under that prefix).

**Backblaze's version history:** by default a Backblaze bucket keeps
every old version of a file. So restic's `prune` frees nothing, and the
bill grows. The page tells the admin to set the bucket's lifecycle to
"Keep prior versions for 30 days". That also gives a 30-day undo if a
stolen key deletes the backups. (Check against Backblaze's docs before
writing the page's text.)

## Where settings and keys live

- `hi/server/backups` in the vault: `key_id`, `secret`. Only admins set
  them, as for `hi/server/keys`.
- The server's config (`server.json`, beside `listen`): `backups` =
  address, region, the server's name in the bucket, the plan (below),
  and whether new apps are on.
- An app's `app.json` `backups` gets `use`: `server` (the default once a
  bucket is set), `own` (today's per-app bucket, kept as an override), or
  `off`. With `server`, the app keeps no keys of its own and no address.
  The address is worked out from the server's setting and the app's name
  (pinned at the first copy; see Moves).

## The plan (schedule)

One plan for the server. Every app follows it unless it has its own
setting:

| Setting | Default | Notes |
|---|---|---|
| Daily snapshot at | 03:00 | A window, not a moment: apps go one after another |
| Copy offsite | after each daily snapshot | or: once a week, Sunday |
| Keep on the machine | 7 days, 4 weeks, 6 months | as today |
| Keep in the bucket | same as on the machine | can be longer, e.g. 12 months |
| Upload limit | none | restic `--limit-upload`, for slow offices |
| Check the bucket | weekly | `restic check` (structure only); monthly a 1% data read. Release 2 |
| New apps | on | |

Snapshots before a release stay as today: always, and copied offsite
right after (they're kept by tag).

**Who runs it.** Release 1 keeps each app's timer, and hi rewrites its
`OnCalendar` from the plan. Release 2 replaces them with one server
timer, `hi-backups.timer`. It works through the apps one at a time:
asks each app's service for its snapshot (the host broker socket, as
`hi app snapshot` does), then copies it. One queue means one upload at a
time, a "next run" to show, and one log.

## The page: Backups

A new server section beside Vault and Keys. Simple, not Proxmox:

```
Backups
───────────────────────────────────────────────────────────────────────
Bucket   s3.eu-central-003.backblazeb2.com / hi5-backups / aiw11/
         Reached 2 min ago ✓                         [Change…]  [Test]
Plan     Daily at 03:00 · offsite after each · keep 7d 4w 6m
         New apps: on                                [Change…]

  App          Backups   Last snapshot     Offsite            In bucket   Next
  ☐ aitools    On        Today 03:12 ✓     Today 03:20 ✓      14.7 GB     Tomorrow 03:00   [Back up now]
  ☐ patman     On        Today 03:02 ✓     Failed 03:05 ⚠     —           Tomorrow 03:00   [Back up now]
  ☐ mech-rms   Off       Today 03:00 ✓     —                  —           —                [Turn on]
  ☐ align      Own bucket  …                                               [Use server's]
  ☐ This server (vault, admins, settings)   Today 03:30 ✓     2 MB        release 2

  [Turn on] [Turn off] for the selected apps

Recent runs                    when, app, what, took, added, result
```

- An app's row links to its Data section (its snapshots, Restore,
  Rollback), which stays where it is.
- The app's own **Copy offsite…** becomes "Use another bucket…", for the
  rare app that must go elsewhere.
- **Off** stops only the offsite copy. Local snapshots still run: before
  releases, rollback needs them.
- A failure posts in the server's channel, and in the app's channel as
  today, and puts a badge on Backups.

UI text follows the STE style of the other pages: short bullets under
the controls.

## Commands

```
hi server backups                               the bucket, the plan, and each app's state
hi server backups --bucket s3:https://<host>/<bucket> [--region <r>] [--name <server>]
                                                asks for the keys (or HI_S3_ACCESS_KEY_ID /
                                                HI_S3_SECRET_ACCESS_KEY); checks the bucket and
                                                claims <name>/ before saving
hi server backups --bucket off                  stops all offsite copies (asks; apps' own buckets stay)
hi server backups --at 03:00 --copy daily|weekly --keep 7,4,6 [--keep-offsite 7,4,12]
                  [--limit-upload <KiB/s>] [--new-apps on|off]
hi server backups on|off <app>… | --all
hi server backups now [<app>…]                  snapshot and copy now
hi app backups <app>                            as today; --offsite server goes back to the server's
```

## Moving to it from per-app buckets

- **Apps with no offsite copy** (most): turning them on starts a new
  repository at `<server>/apps/<app>/`.
- **Apps with their own bucket** keep working and show "Own bucket".
  **Use server's** switches them to the server's bucket. A box (on by
  default) copies their history across with `restic copy`, which
  re-encrypts. The old repository stays where it is: hi says so, and the
  admin deletes it in the old bucket when ready.
- **aitools today** has a bucket address with no bucket in it (saved
  before v2.6.267). Release 1 treats an address with no bucket as unset.

## Moves between servers

An app moved from aiw9 to aiw11 has its history in `aiw9/apps/<app>/`.

- **Release 1:** the app's repository address is pinned at its first
  copy. After a move it keeps copying there, with the old server's keys
  copied over like any shared key a move needs (hi_server_vault.md,
  Snapshots, backups, and moves). That fails with a per-server key
  limited to `aiw9/`. Then the page says "can't reach aiw9/…: Start new
  here" and copies the history across as above.
- **Later:** `hi app move` copies the repository to the new server's
  folder as part of the move.

## After losing a machine (release 3)

On a new machine, `hi server restore --from s3:https://<host>/<bucket>/aiw11`
asks for the bucket's keys and the recovery key. It puts the server's
state back (the vault opens with the recovery key), then restores each
app from `apps/<app>/`, using its `hi-key.sealed`. Today that takes
one `hi app restore` per app, and the server's state is gone.

## Releases

1. **Bucket and page.** `hi/server/backups`, `server.json` `backups`,
   `hi server backups`, `use: server|own|off`, the page section with
   on/off and status, the plan written into each app's timer, the
   `hi-server.json` claim, failures posted. Check prefix-limited
   Backblaze keys.
2. **The server's state and one queue.** `<server>/server/` (the vault
   stays encrypted as it is, and the key file is never copied), one
   `hi-backups.timer` instead of per-app timers, upload limit, weekly
   `restic check`, Recent runs.
3. **Restore a whole server** from the bucket; moves copy the repository.

## Code that changes

| File | Change |
|---|---|
| `app_snapshots.go` | `config.backups()` resolves `use: server` to the server's address and keys; the plan for keep/time |
| `vault_offsite.go` | the bucket's keys come from `hi/server/backups` for `use: server`; the sealed key is unchanged |
| `org_server.go` | `serverConfig.Backups`; release 2's queue and timer |
| new `server_backups.go` | the command, the claim, the status of every app |
| `admin_*.go`, `helper/admin/admin.js` | the Backups section, its API, on/off in bulk |
| `admin_network.go` | the app's Copy offsite… becomes Use another bucket… |

## Open questions

1. **The server's name in the bucket:** the short hostname (`aiw11`),
   changeable only at setup. Or the server's NetBird name?
2. **One key for all servers, or one per server?** The draft recommends
   per server, limited by prefix, and allows both.
3. **Who turns an app's offsite copy off:** admins only (the draft), or
   the app's owners too?
4. **New apps on by default?** The draft says yes.
5. **Keep "own bucket" per app at all?** The draft keeps it as an
   override, but less visible.
6. **Should vmhiserver (no apps) back up its server state** in release 2
   too? The draft says yes: it is the cheapest copy to lose.

## As built (release 1)

v2.6.268:

- **Settings:** `backups.json` in the server's folder (bucket, region,
  name, claim, plan); the keys at `hi/server/backups` (or
  `backups-keys.json` without a vault). An app's `app.json` `backups`
  has `use` (`server`, `own`, `off`); unset, an app with a bucket of its
  own keeps it and the others follow the server. An own address with no
  bucket in it (saved before v2.6.267) counts as unset.
- **Where:** `<bucket>/<name>/apps/<app>/`, worked out at each copy, not
  pinned: an app moved to another server starts a repository in that
  server's folder, and `restic copy` fills it from the local repository
  the move brought. The old folder stays for the old server's admins.
- **The claim:** `--bucket` reads `<name>/hi-server.json` and writes it
  if it isn't there, which also checks the keys, before anything is
  saved. Another server's claim is refused, unless `--take-over`.
- **The plan:** each app's timer stays (release 2 replaces them);
  `--at` rewrites their `OnCalendar` and restarts the enabled ones. The
  server's `--keep` is the default; an app's own `--keep` wins, and
  `hi app backups <app> --keep default` drops it. Apps that set their
  keep before (with `--offsite`, which stored 7,4,6) keep theirs.
- **Now:** `hi server backups now` takes each app's snapshot through its
  service and waits (up to 3 hours) until the service's copy is in the
  bucket. The page runs it as a job.
- **The page:** the server's **Backups** section; its API
  (`/api/backups`) runs `hi server backups` as the admin, with the keys
  on stdin, and needs an admin of both the server and the machine. An
  app's Data has **Use the server's bucket**, **Use another bucket…**, and
  **Do not copy offsite**. `--offsite server` doesn't copy at once (a
  large app outlasts the page's three minutes); the next snapshot or
  **Back up now** does.
- **Not yet checked:** Backblaze keys limited by a file name prefix, and
  its lifecycle text, against a real bucket.

## As built (release 2)

v2.6.270:

- **One queue.** `hi-backups.timer` (user unit, at the plan's time, up
  to 10 minutes after; caught up after a reboot) runs `hi server backups
  run --daily`: each app whose service is enabled, in name order, takes
  its daily snapshot through its service (or itself when the app is
  down, as before) and the queue waits for its copy (up to 3 hours);
  then the server's state. An app turned down has no daily snapshot, as
  with its own timer. `hi server backups now` and the queue take one
  lock, so they wait for each other.
- **Installing it.** `hi server run` (the server's service) installs the
  timer as it starts, so an update's restart brings it, and only once
  it is enabled removes each app's own `hi-app-<app>-snapshot.timer`.
  `hi app up` installs no app timer while the queue's is there, and
  removes one it finds. `--at` rewrites the queue's timer.
- **The server's state** goes to `<name>/server/`, host
  `hi-server-<name>`: the server's folder (without the bucket keys' file
  and the lock), the vault's folder (copied with the vault's lock held;
  the key file is in `~/.config/hi/vault-key`, never copied), and the
  admin page's folder (without its sockets, jobs, and drafts). They are
  copied to `~/.cache/hi/server-backup/` first, a fixed folder so restic
  compares with the last snapshot, and that copy is removed after. It
  needs the vault: its key is `hi/server/snapshots`, given to restic by
  `hi server backups __key`, and sealed beside it as `hi-key.sealed` to
  the recovery key (app `hi-server:<name>`). Kept by the plan's numbers.
  `now --server` backs it up alone; status in `backups-status.json`.
- **Checks.** After an app's daily copy, and after the server's, when a
  week has passed: `restic check`, with `--read-data-subset=1%` when a
  month has. Results in the app's `status.json` (`checked`,
  `check_failed`) and on the page.
- **Upload limit.** `--limit-upload <KiB/s>` adds restic's
  `--limit-upload` to every copy and the server's backup.
- **Recent runs.** `backups-runs.jsonl` in the server's folder (the last
  200 steps): when, what (daily, now, check), the app or the server, how
  long, size, new bytes, and the error. The page shows the last 30.

## As built (release 3)

v2.6.276:

- **`hi server restore --from <bucket>/<name>`**, on a machine with no
  server and no vault: asks for the bucket's keys and the recovery
  key's words; reads `hi-server.json` (whose folder it is); opens the
  server's `hi-key.sealed` with the words (it must be sealed for
  `hi-server:<name>`); restores the newest snapshot of the state into a
  folder beside and moves `org/`, `vault/`, and `admin/` into place (each
  refused if its place has files); opens the vault with the words and
  seals it for this machine (`vaultUnlockHere`: the TPM, or a key file);
  keeps the bucket's keys as given; takes over the folder's claim; keeps
  the server's address if it is this machine's, or else this machine's
  NetBird address (none for a server on a peer of its own); then
  restores each app under `apps/` (listed with S3 ListObjectsV2) as
  `hi app restore --from` does, its snapshot key from its own
  `hi-key.sealed`, its settings and tokens from the restored vault, and
  following the server's bucket; and starts `hi-server.service` and the
  admin page. Apps stay down; their NetBird peers need new setup keys.
  `--no-start`, `--no-apps`, `--apps-folder`. `--drill` (v2.6.277)
  rehearses it on a spare machine while the old server runs: it implies
  `--no-start`, leaves the folder's claim alone, and clears the restored
  copy's bucket and its keys, so nothing there writes to the old
  server's folder.
- **`hi server backups test-restore [<app>... | --server]`** and the
  page's **Test**: the newest snapshot (not a warm-up) restored from the
  bucket into `~/.cache/hi/restore-test/<app>` with `restic restore
  --verify`; checked: the app's `app.json` is the app's, as many files as
  the snapshot's summary counts, and `hi-key.sealed` sealed to this
  vault's recovery key (for the server: `org/config.json`,
  `vault/keys.json` with this vault's recovery key, and its sealed key);
  then removed. Results in the status (`restore_tested`) and Recent runs.
  It takes the backup lock, so it never runs beside a backup.
- **The server's state** is backed up by folder name (`org`, `vault`,
  `admin`), so a restore puts each in its place.
- **Moves:** nothing new. The local repository moves with the app, and
  the new server's first copy fills `<new name>/apps/<app>/` from it; the
  old folder stays for the old server's admins to delete.
- Checked end to end with restic: an app and the server backed up to a
  folder, tested, the server's folder, vault, vault key, admin settings,
  and app deleted, and `hi server restore` bringing all back with the
  vault opening by itself and the app's settings from it.
- **A real drill (2026-11-27):** `hi server restore --drill` of a test
  server from a Backblaze bucket onto a spare, clean machine (hi updated
  from v1.0.3 to v2.6.277 first) took a few minutes for six apps (about
  17 GB). The vault opened with the recovery key and resealed with a key
  file (that machine's TPM library was missing). All six apps' `app.json`,
  `versions.json`, repository HEADs, and vault paths matched the live
  server; their data differed by under 1% from what the live apps had
  written since the snapshot. Nothing started there, and the live server
  ran on with no failed unit.
