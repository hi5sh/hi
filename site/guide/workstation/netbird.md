---
title: NetBird network
description: Enroll a machine in the hi5 NetBird network with a setup key that never lands in shell history, and manage the connection afterwards.
---

[NetBird](https://netbird.io) connects hi5 machines in a private network.
`hi net` enrolls a machine without exposing the setup key: it never appears in
shell history, process lists, or output.

`hi install` installs NetBird first; `hi net` says so if it is missing.

## Enroll a machine

```sh
hi net
```

1. Paste the setup key when asked. Nothing is shown as you type.
2. Choose the machine's name in the network. Press Enter to use its hostname.

`hi` writes the key to a temporary file only you can read, runs
`netbird up --setup-key-file … --hostname …`, and deletes the file whether
enrollment succeeds, fails, or is interrupted.

> The old form `hi net <setup-key>` is refused, because it would put the key
> in your shell history.
{: .warning}

## Enroll from a script

Provide the key through a protected file or an environment variable:

```sh
chmod 600 netbird-key.txt
hi net --setup-key-file netbird-key.txt

HI_NETBIRD_SETUP_KEY="$KEY_FROM_YOUR_SECRET_STORE" hi net
```

`hi` refuses a key file that other users can read.

## Manage the connection

```sh
hi net status       # netbird status, unchanged
hi net down         # disconnect
hi net reconnect    # disconnect and reconnect with the stored enrollment
```

`reconnect` does not need the setup key again.

## A domain for the teams' addresses

[hi teams](/guide/team/) on this machine put their apps and dev pages on
plain http at the team peer's NetBird name. `hi net domain` gives them
https names under a domain of yours instead, such as `dev.example.com`,
with a certificate browsers trust:

```sh
hi net domain dev.example.com --dns gandi   # asks for the provider's token, then an email
hi net domain                               # what's set
hi net domain --email ops@example.com       # where Let's Encrypt writes about the certificate
hi net domain --unset                       # back to plain http
```

Each team's production is then on `https://<team>.dev.example.com/`, its
preview on `<team>-preview`, and its dev page on `<team>-dev`. hi keeps
those names in the domain's DNS, pointing at the team's NetBird address,
so only the network reaches them, and gets one wildcard certificate from
Let's Encrypt by proving the domain with a TXT record. `--dns` takes
`gandi`, `cloudflare`, `route53`, `hetzner`, `digitalocean`, or `rfc2136`
(a DNS server of your own); `--zone` names the zone when it isn't the
domain's registered part. hi checks that the token can read the zone
before it keeps it, in `~/.local/share/hi/domain.json`, which only you
can read.

In NetBird's Access Control, let people reach the teams' peers on TCP 443
as well. See [The addresses on https](/guide/team/dev/#the-addresses-on-https).

## Share a service

To put a service on this machine on a temporary public address through
NetBird, see [Share a local service](/guide/workstation/expose/).
