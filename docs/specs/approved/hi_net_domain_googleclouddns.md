# Google Cloud DNS for `hi net domain`

Status: Approved (2026-11-27). Releases 1 and 2 built as v2.6.275 (see
[As built](#as-built-v26275)). This replaces the earlier draft
of the same day, `hi server domain` (a front door at the server, for
every app's names), which is shelved: see [Considered and
rejected](#considered-and-rejected).

Dependencies: [hi_net.md](hi_net.md) (`hi net domain`: the
machine's domain, its records, its wildcard certificate over DNS-01,
certmagic and libdns), [hi_app_addresses.md](hi_app_addresses.md)
(the address modes, among them `names`), and
[hi_server_vault.md](hi_server_vault.md) (where the
provider's key is kept).

## Goal

A company keeps its domain at Google Cloud DNS and wants its apps at
names under it, such as `demo.corp.example`, on https, with nothing for
the IT team to do per app.

`hi net domain` does this already, with other DNS providers. A machine
with a domain has, for each app:

| Address    | Name                          |
|------------|-------------------------------|
| Production | `demo.corp.example`           |
| Stage      | `demo-preview.corp.example`   |
| Dev page   | `demo-dev.corp.example`       |
| Admin page | `admin.corp.example`          |

- hi writes each name in the zone, through the provider's API, as an A
  record to the app's NetBird address. A new app has its names at once.
  A removed app's records go.
- hi gets the domain's wildcard certificate from Let's Encrypt over
  DNS-01 (certmagic), and renews it.
- Each app's NetBird peer ends TLS on port 443 and sends each name to
  production, Stage, or the dev page.

It works with gandi, cloudflare, route53, hetzner, digitalocean, and
rfc2136. Google Cloud DNS is missing. This draft adds it, and nothing
else is necessary: the IT team gives one key, once.

## The provider

```bash
hi net domain corp.example --dns googleclouddns --key-file sa.json
```

- **One more entry in `hiDNSKinds`** (`internal/cli/net_domain.go`), with
  `github.com/libdns/googleclouddns`. That package uses libdns 1.0's
  record types, as hi's other providers do.
- **The key** is a service account's JSON key file, from the IT team.
  It needs a role that may change the zone's records, for example
  `roles/dns.admin` on the zone. hi writes A records for the apps and the
  `_acme-challenge` TXT record for the certificate, so a key for the
  challenge records only is not enough.
- **The project** comes from the key's `project_id`. hi asks for it only
  when the key has none.
- **`--key-file`**, because the key has many lines and is not easy to
  paste at a prompt. hi reads the file once and keeps the key with the
  domain's other settings, in the vault (`readSecretFile`, as for the
  other providers' tokens). It then says the file can be deleted. Without
  `--key-file`, hi asks for the key's path.
- **The package wants a file path** (`ServiceAccountJSON` is passed to
  `option.WithCredentialsFile`). hi writes the key to a file of its own
  in `$XDG_RUNTIME_DIR` (memory, mode 0600) when it makes the provider,
  and removes the file when the call ends. Better, if upstream takes it:
  a field for the key's contents (`option.WithCredentialsJSON`). Then no
  file is necessary. hi sends that change upstream, and uses the file
  until it is released.
- **The zone** is found as now: the longest zone of the project that the
  domain is in, or `--zone`.
- The help says what to ask the IT team for: "a service account key
  (JSON) with `roles/dns.admin` on the zone".

## One small change beside it

**`-stage` for Stage.** The pages call it Stage, and the names say
`-preview`. `hi net domain` names it `<app>-stage.<domain>`, and keeps
`<app>-preview.<domain>` as a second name for one release. Both go into
DNS, and the certificate covers both, because it is a wildcard. The
release after that removes `-preview` and its records.

## Moving an app's names to the domain

An app on `names` (names kept by hand, through the IT team's proxy)
keeps them until someone changes it:

```bash
hi app addresses demo domain
```

Its links, the owners' DMs, the dev page's origin check, and the
sign-in return addresses then use the domain's names. The old names stop
working when the IT team removes their records or their proxy services.

## The admin page

It is already at `admin.<domain>` on a machine with a domain, when its
address is not set by hand (`adminHasDomain`). A page whose address was
set by hand (`hi server address <name> --https`) keeps that address until
it is unset.

Added after release 2 (v2.6.279): the page's name under the domain is a
setting, `hi server address <name>.<domain>` or **Admin page address**
in the page's Network view (`domain_name` in `admin.json`; `admin` when
empty). hi keeps the new name's record and removes the old one. It also
works when the server's peer carries the page: the record points at the
server's peer, and that peer passes 443 to the page's https socket.

## Several machines on one domain

Each machine with the same domain writes only its own apps' names, and
gets its own copy of the wildcard certificate. Two apps with the same
name on two machines would want the same names. `hi net domain` already
refuses a name that a record not made by this machine has. Check this
before building, and say which machine has the name.

## Who can reach it

As now with a domain: all of an app's names are on its peer's port 443.
NetBird's policies can't keep a person off the dev page by its port, as
they can with the ports 80, 8080, and 8888. The dev page asks for each
person's own link. The review on 2026-11-27 decided that this is enough:
the IT team has not kept people apart by environment before.

The peer's ports 80, 8080, and 8888 stay, whatever the mode.

## Considered and rejected

- **A front door at the server** (the first draft, `hi server domain`):
  one record, `*.corp.example`, to the server, which ends TLS and sends
  each name to its app. It needs a DNS key for the challenge only, but
  the key may change the whole zone, so that is no advantage. It also
  needs new routing in the server, and every app's names stop when the
  server stops. `hi net domain` already has the rest, and goes straight
  to each app's peer.
- **Traefik or Caddy.** hi would have to write their routes from its list
  of apps and keep them up to date, and they are one more service to
  install and update. hi already has the certificate code (certmagic is
  Caddy's) and the routing. If the IT team wants to run its own proxy,
  `names --https` already works with it.
- **Two-level names (`dev.demo.corp.example`).** Each app would need its
  own certificate, for `*.demo.corp.example`. Flat names need one
  certificate per machine (decided 2026-11-27).

## Releases

1. Google Cloud DNS, with `--key-file`, and the key in the vault.
2. `-stage` for Stage, with `-preview` as a second name for one release.

## Tests

- The provider is made from the settings, with a key that has a
  `project_id` and with one that has none.
- The key is in the vault after `--key-file`, and in no file after the
  provider's call ends.
- A bad key, or a key without the role: hi says what is wrong and what
  to ask the IT team for. It does not keep the domain.
- `-stage` and `-preview` both resolve to the app's address, and both
  are answered.

## Findings to check before building

- `libdns/googleclouddns` with hi's `libdns` (v1.1.1): it builds, and
  certmagic's DNS-01 works with it against a real zone.
- How it finds the zone: by the domain's name in the project, or does it
  need the zone's ID?
- The size the Google API client adds to hi's binary.

## As built (v2.6.275)

- `googleclouddns` in `hiDNSKinds`, with `libdns/googleclouddns` v1.2.0
  behind a wrapper of hi's (`internal/cli/net_domain_google.go`). The
  wrapper keeps the key's JSON in the domain's settings (the vault), and
  gives the library a file of its own in `$XDG_RUNTIME_DIR`, mode 0600,
  only for each call. The Google client reads the file when it is made,
  and keeps the key in memory after that.
- `--key-file <path>` (or the path, asked at the prompt; `~/` is the
  home). hi checks that it is a service account's key, with a private
  key and a client email. `--project <id>` when the key has no
  `project_id`. Both flags are refused with another provider.
- Stage is `<app>-stage.<domain>`. `<app>-preview.<domain>` is a second
  record and a second name the peer answers, until a later release.
- hi's binary is about 6 MB larger (38.8 to 44.6 MB), for Google's API
  client.
- Not yet checked against a real Google Cloud zone: the first
  `hi net domain … --dns googleclouddns` on a machine is that check.
