# A name for each address, as the IT team allows

Status: Approved (2026-10-28). Built in v2.6.17: names, both ways of
pointing them, `--https`, and Stage in the dev page, the Slack links, the
lead's words, and the help. Not yet: `--cert` (TLS ended by hi), the
setup's check that each name resolves, and Stage in the guides' older
pages and in commands' own messages. Names are the machine admin's to
set, with `hi team addresses`, not the dev page's.

Dependencies: [hi_team_internal.md](../approved/hi_team_internal.md) (the
team's NetBird peer, its ports, internal and public addresses),
`hi team addresses` (team_addresses.go), the https names on the machine's
domain (team_https.go), [hi_team_sso.md](../approved/hi_team_sso.md)
(return addresses).

## Goal

A team has three addresses: production, the preview, and the dev page.
On the team's NetBird peer they are three ports, 80, 8080, and 8888, and
`hi team addresses` names them one of three ways:

| Way | Production | Preview | Dev page |
|---|---|---|---|
| The machine's domain (`hi net domain`) | `https://demo.hi.example/` | `https://demo-preview.hi.example/` | `https://demo-dev.hi.example/` |
| The NetBird name | `http://demo.corp.example/` | `:8080` | `:8888` |
| One name the IT team keeps | `http://demo.example.com/` | `:8080` | `:8888` |

One organisation's IT team keeps names by hand and won't put several ports on one
name. They propose a name for each address:

```text
demo.example.com        -> production
stage.demo.example.com  -> the preview, which we call Stage from now on
dev.demo.example.com    -> the dev page
```

Another IT team will want something else: one name and ports, names in
another domain, TLS on their own proxy, or only port 443 open. hi should
take what the IT team allows rather than make them take hi's scheme. So:

- **A name for each address,** any names the IT team picks.
- **However they reach the team:** A records to the team's NetBird
  address, or the IT team's reverse proxy sending each name to a port.
- **http or https,** with TLS ended by hi or by the IT team's proxy.
- **The preview is called Stage** wherever people read it.

## What hi already has

The machine's domain is this already, with hi's own names: the peer's
port 443 answers TLS, and one handler sends each name to its address's
relay (`teamHTTPSHandler`). What's missing is the IT team choosing the
names, keeping their DNS, and how TLS is ended.

## The design

### One setting: the names

```bash
hi team addresses demo names demo.example.com
```

gives production `demo.example.com`, Stage `stage.demo.example.com`, and the
dev page `dev.demo.example.com`: the IT team's proposal. Any other names:

```bash
hi team addresses demo names production=demo.example.com stage=staging.example.com dev=demo-dev.example.com
```

hi team new asks it as a fourth choice: "a name for each, which your IT
team keeps". `team.json` keeps them:

```json
"addresses": "names",
"names": {"production": "demo.example.com", "preview": "stage.demo.example.com", "dev": "dev.demo.example.com"}
```

### Two ways the IT team points the names

hi serves both at once, so the IT team picks and nothing in hi changes:

1. **A records to the team's NetBird address,** all three to the same
   address, port 80 (or 443). The peer's port 80 sends each request to
   the address its `Host` names. A name it doesn't know gets production,
   as port 80 does today.
2. **The IT team's reverse proxy** (NetBird's reverse-proxy services, or
   their own), each name to the team's NetBird address and its port:
   80, 8080, 8888. The ports stay open, as now; the names are only for
   links.

`hi team addresses demo` prints what to ask the IT team for, both ways,
with the team's NetBird address:

```text
Production  http://demo.example.com/        A demo.example.com        100.92.14.7  (or a proxy to 100.92.14.7:80)
Stage       http://stage.demo.example.com/  A stage.demo.example.com  100.92.14.7  (or a proxy to 100.92.14.7:8080)
Dev page    http://dev.demo.example.com/    A dev.demo.example.com    100.92.14.7  (or a proxy to 100.92.14.7:8888)
```

and checks each name: it resolves, to the team's address or not (a proxy
resolves elsewhere, and that's fine: hi says so rather than fails).

### http or https

- **http** (the default): what the IT team's names reach.
- **`--https`,** TLS ended in front, by the IT team's proxy: hi serves
  http, and its links say https.
- **`--cert <fullchain.pem> --key <key.pem>`,** TLS ended by hi: a
  certificate the IT team gives, for the three names (or a wildcard). The
  peer's 443 answers with it and sends each name to its address, as the
  machine's domain does now. hi opens an incident 30 days before it
  expires. hi can't get one itself: Let's Encrypt over DNS-01 needs the
  domain's DNS, which is the IT team's.

### What follows the names

Everything that says or checks an address's name takes them from one
place, `teamInternalURL`: the owners' and contributors' links in their
DMs, the dev page's Addresses, the dev page's terminal and VS Code origin
check, the app's address file, and the return addresses to register for
sign-in (`hi team sso`).

### One port, and the network's rules

On one port, NetBird's policies can't tell the dev page from production:
whoever reaches one reaches the other. The dev page still asks for a
person's own link, so this is less, not nothing. An IT team that wants
the network to keep people off the dev page points its name at port 8888
(the second way), and gives only the owners' and developers' group that
port, as now. `hi team addresses` says so when the names are on one port.

## Stage

What hi calls the preview, people call Stage from now on: the dev page's
tabs and buttons, the helper, the DMs, the lead's words, the guides, and
the default name `stage.<production's name>`. Commands take `stage` and
`preview` both (`hi team data demo stage empty`, `--stage`), and the
help says stage.

Inside hi it stays `preview`: the slot's files, the API, the containers'
names, and the names of addresses on the machine's domain that people
already have links to (`demo-preview.hi.example`). A new team on the
machine's domain gets `demo-stage`.

## Tests

- Names from one: `demo.example.com` gives `stage.demo.example.com` and
  `dev.demo.example.com`; given names are kept; a name used twice, or a
  name that isn't one, is refused.
- Port 80 sends each name's `Host` to its relay, and an unknown name to
  production; 8080 and 8888 answer as before.
- `--cert`: 443 answers TLS with the given certificate for each name; one
  that doesn't cover a name, or ends within 30 days, is refused or opens
  an incident.
- Links: the DMs, the dev page, the app's address file, and sign-in's
  return addresses say each name, http or https as set.
- Stage: the page and the helper say Stage; `stage` and `preview` do the
  same in every command.

## Questions

- With A records on one port, should hi also answer port 443 with plain
  http's redirect, or leave 443 closed unless `--cert`?
- Should the dev page be able to set the names, or is it the machine
  admin's, as `hi team addresses` is now? This draft says the admin's:
  it changes where people reach everything.
- Does the IT team point the names with A records or a proxy, and
  where does TLS end? Both work; the answer decides what to test first.
