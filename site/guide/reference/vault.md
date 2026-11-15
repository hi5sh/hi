---
title: The server's vault
description: Where hi keeps every secret on a machine with a hi server or apps, encrypted on disk, how it opens, the recovery key, and each secret's versions.
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
| **recovery**: 24 words, always there | when someone types them | as tpm, on any machine |

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
protects against, and what to change.

## The recovery key

The recovery key opens the vault where nothing else does: on another
machine, after a move or a restore, or when the key file is gone. It
waits in the vault until the server's owner saves it:

```sh
hi server vault recovery
```

on a terminal on the machine. It is shown once. hi asks for two of its
words back, then forgets it. Keep it in a password manager or on paper,
away from the machine, and never in Slack or mail. Lost, or never saved:
`hi server vault recovery --new` makes another, and the old words stop
working.

Where nothing opens the vault by itself, it is **locked**: the apps'
settings can't be read, so apps can't start. To unlock it with the recovery
key, run:

```sh
hi server vault unlock
```

hi then seals a new copy to this machine's TPM, or a new key file, so the
vault opens by itself again.

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

An app's heartbeat URL stays in its `secrets.json`, which holds nothing
else, so a heartbeat still works while the vault is locked.

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
all run as the `hi` account. Root, or anyone who is `hi`, can read the
secrets, as with OpenBao or Vault on the same machine. The vault protects
everything that copies files: backups, VM images, a stolen disk (with the
TPM), and a state folder sent along by mistake.

## Heartbeats

On a machine with a server, the machine's heartbeat (`hi heartbeat <url>`)
checks the server first. When `hi-server.service` isn't running, or the
vault is locked, it sends the service a failure with the reason, on the
check's `/fail` address, and the service alerts at once. With no heartbeat
set, `hi server status` says nobody would hear it.

## Where it is

| Path | What |
|------|------|
| `~/.local/state/hi/vault/keys.json` | The vault's key, once per way it opens |
| `~/.local/state/hi/vault/paths/` | One file per path: its versions, encrypted |
| `~/.config/hi/vault-key/key` | The file way's key, mode 0400, never in a backup of the state |

Changes to the vault, such as an unlock, a rollback, or a forget, are in
the server's audit log (`hi server audit`).
