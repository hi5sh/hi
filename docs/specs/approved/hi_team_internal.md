# hi team: internal by default

Status: Approved (2026-10-25). Ships as v2.5.0; one peer with ports
since v2.5.1.

## Goal

A team's addresses (production, the preview, and the dev page) are on the
team's NetBird network by default, at names such as `quarry.corp.example`, and
reach nobody who isn't on that network. An owner makes one public on the
dev page, deliberately, with the protection, the name that stays, and
the addresses it allows, through `hi net expose`, as every address is
today.

## Why

- **Security.** Most apps are used by people on the internal network. A
  public address is something anyone on the internet can try; an internal
  one is reachable only through NetBird, where Access Control says who.
- **Reliability.** On 2026-10-25 NetBird's maintenance stopped its
  exposures for an hour: every team address was down while the internal
  network worked.
- **Clear choices.** Going public is a decision with consequences: who
  can reach it, how they sign in, what name it has. It belongs on the
  dev page, explained, not as a default nobody chose.

## How it works

- **One peer, a port per address** (since v2.5.1; v2.5.0 had a peer
  per address, three names and a reusable key, which was messy). The
  team's NetBird peer, a small rootless container with NetBird's
  userspace stack, is `<team>.<network domain>`, such as
  `quarry.corp.example`: production on port 80, so people needn't think of
  ports, the preview on 8080, the dev page on 8888, and SSH to the dev
  box on 22. A peer that joined before keeps its name (`quarry-dev`), and
  hi uses the name NetBird reports, so a rename in NetBird's dashboard
  follows.
- **The internal relay.** The peer passes each address's port to a relay in the
  team's service, on a socket of its own: the same relay as `hi net
  expose`, with the helper in the app's pages, `/__hi/` for hi, and the
  person's key from their link, but no link key: NetBird's Access Control
  is the gate. It follows the app as it moves between boxes, like the
  exposure does.
- **HTTP.** The names are plain `http://`; WireGuard encrypts the traffic
  between devices. Browsers call such pages "not secure" and keep some
  features from them, such as reading the clipboard (the dev page's
  Paste key; a long press pastes instead). With a domain of the
  machine's (`hi net domain`, since v2.5.46 for the dev page and v2.5.49
  for all three), each address is also on https at a name of its own,
  `<team>`, `<team>-preview`, and `<team>-dev` under the domain, on the
  peer's port 443, with a wildcard certificate from Let's Encrypt
  (DNS-01); the service passes each name to its address's relay.
- **Joining.** The peer joins with a one-off NetBird setup key, asked for
  at `hi team new` and with `hi team dev <team> netbird <file>`, which hi
  deletes once it has; its identity stays in the team's folder and out of
  the snapshots.
- **Access.** In NetBird's Access Control, a policy from the people's
  group to the peer's group (the setup key's auto-group), TCP 80 and
  8080, and one from the owners' group, TCP 8888 and 22; both TCP 443
  with a domain. On 443 the three names share a port, so the dev page's
  own check of an owner's link is its gate there.

## Public, on the dev page

- **Addresses** on the dev page lists each one: its internal link, and
  whether it is public too, with its public link, its protection (the
  link's key), the name that stays (`hi team domain`), and the addresses
  it allows (`hi team allow-ip`).
- **Make public** and **Internal only** are an owner's click, logged with
  their name, as the other actions on the page. Making one public runs
  `hi net expose` as today; internal only stops it.
- **Defaults.** A new team's addresses are internal only (`"public": {}`
  in team.json). A team made before has no `public` and keeps its public
  addresses, until an owner makes them internal.

## Links

People's links (`/hi-<team>`) point at the addresses as they are: the
internal one, or the public one when there is one. The key in a link
signs the person in on either; on the internal address it is a cookie
for plain HTTP.

## Not in this release

- Certificates for the internal names.
- Setting the protection, the name, and the allowed addresses from the
  dev page: it shows them, and says which command on the machine changes
  them.
