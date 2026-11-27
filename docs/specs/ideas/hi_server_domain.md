# `hi server domain`: one name for every app, at the server

Status: Draft (2026-11-27), for review.

Dependencies: [hi_app_addresses.md](../approved/hi_app_addresses.md)
(a name for each address, `names`, `--https`, `--cert`),
[hi_net.md](../approved/hi_net.md) (`hi net domain`: a machine's
domain, its wildcard certificate over DNS-01, certmagic and libdns),
[hi_server_peer.md](../approved/hi_server_peer.md) (the server's NetBird
peer and its sockets), [hi_server_apps.md](../approved/hi_server_apps.md)
(a server on every machine with apps, for now; the machine reaches its
server, never the other way round), [hi_server_vault.md](../approved/hi_server_vault.md)
(where the DNS key is kept), and [hi_app_internal.md](../approved/hi_app_internal.md)
(each app's relays, one Unix socket per address).

## Goal

Today a company that wants its own names for the apps, such as
`demo.corp.example`, keeps them by hand (`hi app addresses demo names …`).
For each app the IT team makes three DNS records, and a reverse proxy in
front of them ends TLS. That is three records and three proxy services
per app, and the proxy is one more thing that can break. When it does,
the app looks broken, but hi can't see or fix it.

With this change, the IT team does two things, once:

1. **One DNS record:** `*.corp.example` to the server's NetBird address.
2. **One key:** a key that lets hi write the domain's `_acme-challenge`
   records, so hi can get the domain's wildcard certificate from Let's
   Encrypt.

Then the server ends TLS for every name under the domain and sends each
name to its app. A new app has its names at once, with nothing for the
IT team to do.

- No Traefik, Caddy, or other proxy. hi already has what it needs: the
  certificate code (certmagic over DNS-01, as `hi net domain` uses) and
  the name-to-address routing (each app's peer does it for its own
  names today).
- The apps keep their NetBird names (`http://demo.<netbird-domain>:8888`)
  as they are. The server's names come in addition.

## The names

A wildcard certificate covers one level of names: `*.corp.example`
covers `demo.corp.example`, not `dev.demo.corp.example`. So the names are
flat, as `hi net domain` makes them now:

| Address    | Name                         |
|------------|------------------------------|
| Production | `demo.corp.example`          |
| Stage      | `demo-stage.corp.example`    |
| Dev page   | `demo-dev.corp.example`      |
| Admin page | `admin.corp.example`         |

- `admin` is reserved: `hi app new` refuses an app named `admin`, and an
  app named so before this release keeps its old names until it is
  renamed. The same applies to `www` and `server`.
- An app's name already can't end in `-stage` or `-dev`. If it could,
  `demo-dev` the app and `demo`'s dev page would have the same name.
  Check this before building.
- A machine with a `hi net domain` names Stage `<app>-preview` today.
  The server's domain says `-stage`, as the pages do. `hi net domain`
  changes to `-stage` in the same release and keeps `-preview` as a
  second name until the next.

## The certificate

```bash
hi server domain corp.example --dns googleclouddns    # hi asks for the key (JSON)
hi server domain corp.example --dns cloudflare        # hi asks for the token
hi server domain corp.example --cert fullchain.pem --key key.pem
hi server domain --email it@corp.example
hi server domain                                      # what it is, and the certificate's end date
hi server domain --unset
```

- **DNS-01**, as `hi net domain` does it: certmagic writes
  `_acme-challenge.corp.example`, Let's Encrypt checks it, certmagic
  deletes it. The server can be on NetBird only, because Let's Encrypt
  never connects to it.
- **Google Cloud DNS** is new. hi adds `libdns/googleclouddns` beside the
  providers it has (gandi, cloudflare, route53, hetzner, digitalocean,
  rfc2136). Its key is a service account's JSON key file. `hi net domain`
  gets the provider too.
- **The key is kept in the server's vault** (`server/dns`), never in a
  file. The server reads it when it renews the certificate.
- **A key that can change only the challenge records** is best. Google
  Cloud DNS gives roles per zone, not per record. So the IT team can
  delegate a small zone, for example `acme.corp.example`, give the key
  rights on that zone only, and add one CNAME:
  `_acme-challenge.corp.example` to `_acme-challenge.acme.corp.example`.
  `--challenge-zone acme.corp.example` tells hi to write there.
  certmagic can do this (`DNS01Solver.OverrideDomain`). Check this
  before building.
- **`--cert` and `--key`:** a wildcard certificate the IT team gives.
  hi opens an incident 30 days before it ends, as for an app's
  `--cert`.
- **Renewal:** certmagic renews 30 days before the end. If renewal still
  fails 14 days before the end, hi opens an incident on the server.
- The certificate and its ACME account are in the server's state
  (`~/.local/state/hi/org/certs`), so the server's backup has them.

## The route

```text
browser ──https──▶ *.corp.example = the server's NetBird address, port 443
                     │  hi-server-peer: 443 → unix:https.sock
                     ▼
                   hi server: TLS ends here, with the wildcard
                     │  the name → (app, address)
                     ▼
                   the app's relay: ~/.local/state/hi/<app>-internal/<address>.sock
                     │  the same relay the app's peer uses today
                     ▼
                   production, Stage, or the dev page
```

- **The server's peer** forwards port 443 to a new socket, `https.sock`,
  as it forwards 7373 and 7374 now. Port 80 forwards too. The server
  answers it with a redirect to https.
- **The server and its apps are on one machine**, for now
  ([hi_server_apps.md](../approved/hi_server_apps.md), "a server on every
  machine with apps"). So the server goes straight to each app's relay
  socket. No NetBird policy from the server to the apps is necessary,
  and the server makes no new connections over the network.
- **The table** of names to relays comes from the apps on the machine,
  read again when an app is made, renamed, moved, or removed. A name
  that is not in the table gets a short page: "No app is at this
  name."
- **What passes through:** the `Host` header as it came, and
  `X-Forwarded-For` and `X-Forwarded-Proto: https`. WebSockets (the dev
  page's terminal and chat) and streamed answers pass through without a
  timeout and without buffering (`httputil.ReverseProxy` with
  `FlushInterval: -1`).
- **The app's links follow.** A new address mode, `server`, makes
  `appInternalURL` give `https://demo.corp.example/` and the other two
  names. The links in the owners' DMs, the dev page's Addresses, its
  origin check, the app's address file, and the sign-in return addresses
  then use them. `hi app addresses demo server` sets it. `hi app new`
  makes it the default when the server has a domain.
- **The admin page** is at `admin.corp.example`, through the same route,
  to the page's own socket.

## Who can reach it

- `*.corp.example` points to a NetBird address. Only peers with a NetBird
  policy to the server's peer, port 443, reach any name. The server's
  NetBird group is the one to give.
- **All names are on one port.** A person who reaches one app's name
  reaches all of them, and the dev pages too. The dev page still asks
  for each person's own link, and the admin page for a sign-in. Today
  the IT team can use NetBird policies to keep people off a dev page,
  because each address has its own port. That is not possible here.
  `hi server domain` says so when it is set. An IT team that needs it
  keeps that app on `names`, through its own proxy.
- **The server is now in front of every app.** If the server stops,
  every name stops, but the apps keep running and their NetBird names
  keep working. The server restarts in a few seconds. A long stop is
  an incident in the ops channel, as now.

## The admin page

The server's **Addresses** section:

- the domain, how the certificate is got (`googleclouddns`, a given
  certificate), its end date, and the last renewal;
- each app's three names, each with its state: answers, or what is
  wrong (for example "Stage is not running");
- what to ask the IT team for, in a box to copy: the record, the
  challenge CNAME, and the NetBird policy.

## Security

- The DNS key can write DNS records. It is kept in the vault, used only
  for the challenge records, and never shown again. With a delegated
  challenge zone, the key can't change any of the domain's other
  records.
- TLS ends at the server, so the server sees all traffic in clear. The
  server and the apps are on one machine, so this is no new place for
  the traffic to be in clear.
- The server's https listener is a new way into the server. It only
  reads the `Host` header and passes the request on. It never answers
  the server's API there. The API stays on 7373, with its signed calls.

## Considered and rejected

- **Traefik or Caddy beside the server.** Both work, but hi would still
  have to write their routes from its list of apps and keep them up to
  date. It would also be one more service to install, update, and watch.
  hi already has the certificate code and the routing.
- **`hi net domain` on each machine, with the same key.** It exists and
  works now. hi writes one A record per name, to each app's NetBird
  address. But the key must then be able to write any record in the
  zone, and IT teams don't like that. Each machine also gets its own
  wildcard certificate. It stays, for machines with no server domain.
- **Two-level names (`dev.demo.corp.example`).** Each would need a
  wildcard certificate and a DNS record per app (`*.demo.corp.example`).
  That is again work for the IT team per app.
- **The server dialing the apps' NetBird peers.** This is necessary
  only when the apps are on other machines. A server on every machine
  with apps does not need it. For one server for many machines, the
  route goes through each machine's own connection to the server, not
  through a NetBird policy into the machine.

## Releases

1. **Google Cloud DNS** for `hi net domain`, its key in the vault.
2. **`hi server domain`**: the certificate, the server peer's 443 and
   80, the route to each app's relays, the `server` address mode, and
   `hi app new`'s default. The admin page at `admin.<domain>`.
3. **The admin page's Addresses section**, the incidents for the
   certificate, and `-stage` for `hi net domain`.

## Tests

- A name to each of an app's three relays; an unknown name; a name in
  capitals; a name with a port.
- A WebSocket through the route; a streamed answer arrives in parts.
- `Host`, `X-Forwarded-For`, and `X-Forwarded-Proto` as the app sees
  them.
- An app made, renamed, and removed: the table follows without a
  restart.
- `appInternalURL` in `server` mode, and the dev page's origin check
  with it.
- The certificate with a fake ACME server (certmagic's test setup) and
  a fake libdns provider, with and without `--challenge-zone`.
- An app named `admin`, `www`, `server`, or ending in `-dev` or
  `-stage` is refused.
- The DNS key is in the vault and in no file.

## Open questions

1. Should the names be flat (`demo-dev.corp.example`)? The table above
   assumes yes.
2. The admin page at `admin.<domain>`, or a name the IT team picks?
3. Several servers on one domain (one per machine, for now): does each
   get its own subdomain (`*.m2.corp.example`), or does only one server
   per domain have names?
4. Should a person's NetBird group be able to keep them off the dev
   pages again, for example with a NetBird sign-in in front of the dev
   names only?

## Not in this

- One server for apps on many machines. That comes with
  [hi_cluster.md](hi_cluster.md).
- Names outside NetBird, on the internet.
- hi writing the `*.corp.example` record itself. The IT team does it
  once.
