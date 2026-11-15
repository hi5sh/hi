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

and the server's next start (`hi update` restarts it) moves the vault to
the TPM by itself, and removes the key file. A VM without a virtual TPM gets a file; ask your IT team
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

Open **The server**, then its **Vault** tab, beside Overview: whether it's open, **Unlock** with the
passphrase or the recovery key, **Lock now**, the ways it opens (add a
passphrase, the TPM, or a key file; remove one), each secret's versions
with **Roll back** and **Forget**, and the removed apps' settings with
**Purge**, and the shared keys. While it's locked, a banner on every
view says so, with Unlock, which opens the tab. The left panel's Keys
says whether it's open, with the way there. The passphrase goes from the browser to the page and from the
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
away from the machine, and never in Slack or mail. Until it's saved, the
page reminds the owner, and so does Slack once a week, if the server has
its Slack app. Lost, or never saved:
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

## Shared keys

A key several apps use, such as one OpenRouter key, is kept once, at
`hi/shared/<name>`, never copied into an app's settings:

```sh
hi server vault shared set openrouter                 # asked without echo
hi server vault shared give openrouter payments orders
hi app env payments OPENROUTER_KEY --from shared/openrouter
```

An admin sets it and gives it to apps; an app's owners use it by
reference, which the app's `app.json` keeps, not the value. A new value
reaches every app that uses it within a minute, as any change does.
Taken back (`give … --take`), the app's boxes no longer get it. It can't
be removed while an app uses it. The admin page's Keys lists each one,
the apps given it, and the apps that use it. `hi app move` takes the
shared keys an app uses along: a machine that doesn't have one gets it,
given to that app alone.

## A team's OpenBao or Vault

hi talks to OpenBao and HashiCorp Vault alike (KV v2, and KV v1 without
versions), two ways.

### Beside the vault: a team's paths as an app's settings

```sh
hi server vault connect corp/ vault://kv/corp --address https://bao.corp.example:8200
hi app env payments --from corp/payments/prod
```

`connect` reads everything under `kv/corp` as `corp/`, read only. It
logs in with a key pair hi makes for this login alone (`--login jwt`,
the default: no secret is handed over), or an AppRole (`--login
approle`, its role ID and secret ID asked without echo); `--namespace`
and `--ca <file>` where the store needs them. The address is `https://`,
or `http://` to this machine only. It prints what the store's owners run
once: the policy, the JWT role bound to this server's name, and the
public key. `hi server vault login` checks the login and prints it
again; `--new-key` makes a new pair, and the old one logs in until the
owners swap it.

An app takes a path with `--from`: each string under a variable's name
is a setting. A name that isn't a variable, one hi sets itself, or a
value that isn't a string is left out, and `hi app env` and `hi server
vault ls` say which and why. `--production`, `--preview`, or `--dev`
takes it for one slot; `?version=12` pins a version; `--unset-from`
drops it.

- **A change there reaches the app:** every minute the server looks at
  each path's version; a new one reaches the boxes within a minute, and
  the app's ops channel hears which names changed, never the values.
- **While it's away** (down, sealed): the boxes keep the values hi last
  read, kept encrypted in the vault, and a box that starts uses them.
  The channel hears it once, and when it answers again. A refusal is
  told as one, with what to ask the store's owners for.
- **Two values for one name:** nothing wins silently. Taking a path
  that gives a name the app already has from elsewhere is refused:
  leave it out (`--skip NAME`) or unset it first. Setting a name a path
  gives is refused unless it says `--override`, for that slot alone, and
  `hi app env` marks it. A name a path adds later keeps the value of the
  source that had it first, and the channel says so.

### Instead of the vault: hi's own paths there

```sh
hi server vault move vault://kv --address https://bao.corp.example:8200
```

copies every one of hi's paths to `kv/hi/…`, reads each back, and
switches: hi reads and writes there, and its history and rollbacks are
there. Before the store's owners have set up hi's login (read and write
on `kv/hi/`), `move` prints what they run, and keeps its key for the
next try. The vault keeps hi's login to the store, the recovery key,
and what hi read from connected stores, and a copy of every path, which
stands in while the store is away, until `hi server vault move
--forget`.

On the admin page, the Vault tab's **Team stores** connects one, checks
its login, makes a new key, shows what the owners run, and lists the
paths the apps take, with their versions and what's left out.

## Apps

| Path | What |
|------|------|
| `hi/app/<app>/shared` | Settings for Stage and production |
| `hi/app/<app>/production`, `.../stage` | One slot's own |
| `hi/app/<app>/dev` | The dev box's own |
| `hi/app/<app>/hi` | Its Slack tokens, OpenRouter key, and sign-ins |
| `hi/app/<app>/offsite` | Its offsite bucket's keys |
| `hi/app/<app>/snapshots` | The key its snapshots are encrypted with; restic gets it from hi through a command, never a file |

### After losing the machine

An app with an offsite copy (`hi app backups <app> --offsite s3:…`) has
its snapshot key beside it in the bucket, as `hi-key.sealed`: sealed to
the vault's recovery key, which hi can do without knowing the words.
So the recovery key and the bucket's keys alone bring the app back on
another machine:

```sh
hi app restore payments --from s3:https://s3.example.com/backups/payments
```

asks for the bucket's keys and the recovery key's words, opens the
snapshot key, copies the snapshots into a repository here, and puts the
latest into a new folder (`--folder`), which becomes the app. Its
settings and tokens were in the old machine's vault: you set them again,
then `hi app up`. A new recovery key seals each copy again, and the old
words stop opening it. `hi app snapshots` says whether the key is there.

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
waits for a locked vault fails its own heartbeat the same way. An app
that runs on while the server is down pings as usual, with "the server
isn't reachable since …" in the ping's text; after a day, its ops channel
hears it once, and again when the server is back. With no
heartbeat set, `hi server status` and the page say nobody would hear it.

## Where it is

| Path | What |
|------|------|
| `~/.local/state/hi/vault/keys.json` | The vault's key, once per way it opens |
| `~/.local/state/hi/vault/paths/` | One file per path: its versions, encrypted |
| `~/.config/hi/vault-key/key` | The file way's key, mode 0400, never in a backup of the state |

Changes to the vault, such as an unlock, a rollback, or a forget, are in
the server's audit log (`hi server audit`).
