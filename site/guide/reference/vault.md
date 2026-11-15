---
title: The server's vault
description: Where hi keeps every secret on a machine with a hi server or apps, encrypted on disk, how it opens, the passphrase and the recovery key, locking and unlocking, the admin page's Vault, and each secret's versions.
---

On a machine with a hi server or apps, hi keeps every secret in one store,
the **vault**, encrypted on disk:

- the server's provider keys, model key, template and data tokens, and its
  Slack tokens;
- each app's settings (`hi app env`), its Slack tokens and OpenRouter key,
  its offsite bucket's keys, and the key its snapshots are encrypted with;
- the machine's mail settings, its NetBird API token, and its DNS
  provider's settings (`hi net domain`).

A copy of the disk, a backup of hi's state, or a VM image holds none of
them in the clear. Each secret keeps every version, so a change can be
rolled back.

## How it opens

The vault's key is random and never on disk in the clear. It is kept
several times, each copy locked one way, and any one of them opens the
vault:

| Way | Opens by itself | Protects against |
|-----|-----------------|------------------|
| **tpm**: sealed to this machine's TPM | yes, on this machine only | backups, copies of hi's state, a stolen or copied disk |
| **file**: a key file, `~/.config/hi/vault-key/key`, outside hi's state | yes | backups and copies of hi's state; not a copied disk |
| **passphrase**: one an admin chooses | when an admin types it | as tpm, and a stolen machine, on any machine |
| **recovery**: 24 words, always there | when someone types them | as the passphrase |

`hi update` makes the vault with **tpm** where hi can use the TPM without
root, else **file**, and always **recovery**, and moves every secret file
into it. Nothing changes for the apps: after a reboot the vault opens by
itself, as the files did.

hi uses the TPM through systemd (`systemd-creds --user`), so it needs no
root and no group. Ubuntu leaves out one library systemd needs for it,
and then systemd quietly seals with a key on the disk instead. hi
notices, and uses a file instead. `hi server vault` then says to ask
your IT team for:

```sh
sudo apt install libtss2-rc0t64
```

and `hi server vault unlock-with add tpm`, then `remove file`, moves the
vault to the TPM. A VM without a virtual TPM gets a file; ask your IT team
for a virtual TPM.

`hi server vault` and `hi server status` say in one line what the vault
protects against, and what to change. The weakest way sets it: a key
file beside the TPM or a passphrase undoes them, so hi says to remove it.

### A passphrase

```sh
hi server vault unlock-with add passphrase      # asked twice, without echo; at least 12 characters
hi server vault unlock-with remove tpm          # then only a person opens it
```

With only a passphrase (and the recovery key), even the machine itself
can't open the vault: after a reboot, every app waits until an admin
unlocks it. hi asks before you remove the last way it opens by itself.
TPM and a passphrase together keep the apps running after a reboot, and
let the vault open on another machine without the recovery key.

## The server holds it

hi's server (`hi-server.service`) opens the vault as it starts and holds
its key in memory. The apps' services, the admin page, and commands ask
the server to read or write a secret; each reader's first read of each
version is in the audit log, by name and never with its value. Where no
server runs, a command opens the vault itself with the TPM or the key
file.

A restart, such as the one `hi update` does, doesn't lock it: systemd
keeps the key in memory for the server's next start. A reboot does, and
then the vault opens by itself with the TPM or the key file, or waits for
an admin with the passphrase.

### Locked

```sh
hi server vault lock        # the server forgets the key now
hi server vault unlock      # the passphrase, or the recovery key's 24 words
```

While it's locked, boxes that run keep running. An app that starts waits,
says so in its log, and fails its heartbeat with the reason, so its check
alerts; once an admin unlocks the vault, it starts by itself. Five wrong
tries in a row, and the next waits a minute; every try is in the audit
log.

## On the admin page

Keys, then **The server's vault**: whether it's open, **Unlock** with the
passphrase or the recovery key, **Lock now**, the ways it opens (add a
passphrase, the TPM, or a key file; remove one), each secret's versions
with **Roll back** and **Forget**, and the removed apps' settings with
**Purge**. While it's locked, a banner on every view says so, with
Unlock. The passphrase goes from the browser to the page and from the
page to the server; the page keeps none of it, and no value is ever on
it.

## The recovery key

The recovery key opens the vault where nothing else does: on another
machine, after a move or a restore, or when the key file is gone. It
waits in the vault until the server's owner saves it:

```sh
hi server vault recovery
```

on a terminal on the machine, or **Save your recovery key** on the admin
page, which only the server's owner sees. It is shown once. hi asks for
two of its words back, then forgets it. Keep it in a password manager or on paper,
away from the machine, and never in Slack or mail. Lost, or never saved:
`hi server vault recovery --new` makes another, and the old words stop
working.

On another machine, or when the key file is gone, the vault is locked
until someone types the passphrase or the recovery key (`hi server vault
unlock`, or Unlock on the page). hi then seals a new copy to this
machine's TPM, or a new key file, in place of the one that no longer
opens, so the vault opens by itself again.

## Each secret's versions

```sh
hi server vault ls [hi/app/payments]        # paths, versions, when, and names; never values
hi server vault history hi/app/payments/production
hi server vault rollback hi/app/payments/production 12
hi server vault forget hi/app/payments/production --version 11
```

- **rollback** puts an old version's values back, as a new version. A box
  gets them within a minute, and its app reads them as it does any change
  (see [Keys and settings](/guide/app/keys/)).
- **forget** destroys one version's value, such as one that leaked; its
  entry stays in the history. Backups of hi's state taken before still
  hold it, so change the secret at its source too. The current version
  can't be forgotten: set a new value first.

No command prints a value.

## Apps

| Path | What |
|------|------|
| `hi/app/<app>/shared` | Settings for Stage and production |
| `hi/app/<app>/production`, `.../stage` | One slot's own |
| `hi/app/<app>/dev` | The dev box's own |
| `hi/app/<app>/hi` | Its Slack tokens, OpenRouter key, and sign-ins |
| `hi/app/<app>/offsite` | Its offsite bucket's keys |
| `hi/app/<app>/snapshots` | The key its snapshots are encrypted with; restic gets it from hi through a command, never a file |

An app's heartbeat URL is in its `app.json`, out of the vault, so the
heartbeat still goes while the vault is locked. It only lets someone send
a beat.

- **`hi app rename`** takes its paths to the new name.
- **`hi app move`** carries them to the other machine over the move's own
  connection, and removes them here once the app runs there.
- **`hi app rm`** keeps them, read by nothing, as it keeps the snapshots.
  `hi app up` with the kept folder gets them back; a new app with the name
  asks first.
- **`hi app rm --purge`** deletes them too, with the snapshot key and the
  local snapshots. An offsite copy stays, and so does the history of who
  changed what, without values.

## What it doesn't protect

While the machine runs, the vault is open, and hi's server, page, and apps
all run as the `hi` account: the server holds the key in memory. Root, or anyone who is `hi`, can read the
secrets, as with OpenBao or Vault on the same machine. The vault protects
everything that copies files: backups, VM images, a stolen disk (with the
TPM), and a state folder sent along by mistake.

## Heartbeats

On a machine with a server, the machine's heartbeat (`hi heartbeat <url>`)
checks the server first. When `hi-server.service` isn't running, or the
vault is locked, it sends the service a failure with the reason, on the
check's `/fail` address, and the service alerts at once. An app that
waits for a locked vault fails its own heartbeat the same way. With no
heartbeat set, `hi server status` and the page say nobody would hear it.

## Where it is

| Path | What |
|------|------|
| `~/.local/state/hi/vault/keys.json` | The vault's key, once per way it opens |
| `~/.local/state/hi/vault/paths/` | One file per path: its versions, encrypted |
| `~/.config/hi/vault-key/key` | The file way's key, mode 0400, never in a backup of the state |

Changes to the vault, such as an unlock, a rollback, or a forget, are in
the server's audit log (`hi server audit`).
