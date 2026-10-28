---
title: Try what the team built
description: "How a hi team shows its work: the preview and production on the team's NetBird network, on https with a domain of yours, or public behind a key in the link, and addresses that stay."
---

When an owner's agent ships work into dev (`hi dev ship`), or puts a
branch on the preview first (`hi dev preview`), hi shows it on the team's
preview address, so the people can try the work, on their phones too:

1. hi starts the project in an app box of its own, from dev (or the
   branch on the preview), with `make run`, or the
   `start` script: `bun run start` with a `bun.lock`, else `npm start`.
   The app listens on `$PORT`; its port reaches this machine only.
2. **On the team's NetBird network**, it is at `http://<team>.<your
   network's domain>:8080/` (production at `http://<team>.<domain>/`): only
   devices on that network reach it, as NetBird's Access Control allows
   (see [Joining NetBird](/guide/team/dev/#joining-netbird)). That is all a
   new team has: its addresses are internal until an owner makes one
   public. They need no key and don't expire; WireGuard encrypts the
   traffic on the network.
3. **On https**, with a domain of yours for the machine
   (`hi net domain`): production at `https://<team>.<domain>/` and the
   preview at `https://<team>-preview.<domain>/`, still reached only on
   the network (see [The addresses on https](/guide/team/dev/#the-addresses-on-https)).
4. **Public too**, when an owner makes it so under **Where things run**
   in the [dev page](/guide/team/dev/#the-dev-page)'s Release tab:
   `hi net expose --link` puts it on an HTTPS address through this
   machine's NetBird (see [hi net](/guide/workstation/expose/)), which
   only the link with its key opens, from the addresses `hi team allow-ip`
   allows. The machine must be enrolled with `hi net`, and Peer Expose
   must be on in NetBird. A team made before hi 2.5 is public until an
   owner makes it internal only. A public link lasts a day; the preview
   gets a new one only when the old one has less than 12 hours left.
5. The address stays the same from one preview to the next: the new
   version starts beside the old one, and the address moves to it once
   it answers, so the people just reload. If the new version doesn't
   start, the address keeps showing the old one.

A new team's app is on the preview as soon as `hi team up` starts the
team, so its link works before anything is built. It is a welcome page
that points at the helper's button, whose Feedback tells the team what
the app should do. The first feature replaces it.

Production has an address of its own, which shows the latest release.
hi keeps one message in the channel with production's current link,
edited whenever the link or the version changes, and renews a public
link before it runs out each day. Each release's announcement links to
that message, so nobody has to hunt for the latest link.

When only an address ends, early or on time, the app keeps running: hi
puts the box that answers on a new address and leaves it be. A new box
starts only for a new version, new settings, or a box that doesn't answer,
so the app's data isn't opened twice for the sake of a link.

## Public addresses that stay

By default NetBird makes up each public address, and production's
changes every day. With a domain of your own verified in NetBird's
reverse proxy, a team you turn it on for gets public names that stay:
production at `<team>.<domain>` and the preview at
`<team>-preview.<domain>`, renewed every 30 days on the same name.
This is a different domain from `hi net domain`'s, which is for the
internal addresses.
Teams are off until you turn it on for them. People's cookies for an app last then, so the helper's answers to
their feedback reach them days later.

1. In NetBird, add the domain under Reverse Proxy > Custom Domains, such
   as `apps.example.com`.
2. In its DNS, add a CNAME for `*.apps.example.com` to the proxy cluster
   NetBird names, such as `eu.proxy.netbird.io`, and verify it in NetBird.
3. Make a NetBird service user with the Admin role (Team > Service
   Users), with an access token. A Network Admin may read the reverse
   proxy but not make services.
4. On the machine that runs the teams:

```sh
hi team domain apps.example.com    # asks for the token, checks the domain
hi team domain payments on         # payments.apps.example.com
hi team domain payments off        # back to an address NetBird makes up
hi team domain                     # the domain, and which teams have it on
hi team domain off                 # no domain for any team
hi team domain apps.example.com --new-token   # another token; hi also asks when NetBird refuses the saved one
```

A team's production moves within a minute of `on` or `off`, and its
preview with the next one.

To let in only some addresses, such as the office's, with the same token:

```sh
hi team allow-ip payments 203.0.113.18     # addresses and CIDR blocks, separated by commas
hi team allow-ip payments 203.0.113.18,203.0.113.20   # two addresses
hi team allow-ip payments                    # what it is now
hi team allow-ip payments off                # any address with the link
```

Change the addresses here, not under Access Control in NetBird's
dashboard: hi checks the rule every minute and puts back exactly its own
list, so an address added there by hand is gone again within a minute.
The same goes for the service itself: one deleted in the dashboard is
made again within a minute, on the same name, with the same key.

The team's production and preview move to addresses with the rule within
a minute (`hi net expose --allow-ip`). On a name that stays, the links
people have keep working; from anywhere else, NetBird turns them away. A team still running an older hi moves when
it is restarted. If
the name doesn't work, hi uses an address NetBird makes up, tries the name
again after half an hour.

To let people in without the link, by signing in with your organisation's
own provider (anything that speaks OpenID Connect: Google Workspace,
Microsoft Entra ID, Keycloak, Auth0, Okta, Authentik, Zitadel…), the team
needs a name that stays (above). Register a confidential **web** client
with the provider, then:

```sh
hi team sso provider add acme https://login.example.com/realms/acme   # checks it; asks for the client ID and secret
hi team sso payments on acme --domain example.com    # accounts on example.com
hi team sso payments on acme --group engineering     # people in a group, from the groups claim
hi team sso payments on acme --anyone                # everyone the provider signs in
hi team sso payments                                 # the rules, and the last sign-ins and refusals
hi team sso payments off                             # the link only; everyone is signed out
```

`on` prints two return addresses, production's and the preview's, such as
`https://payments.apps.example.com/__hi/auth/callback`: add both to the
client at the provider. Someone who opens the app with no key and no
session goes to the provider's sign-in and back to the page they asked
for, signed in for 14 days, if the rules let them in. Links with the key
keep working, also for people the provider doesn't know. New rules take
effect at once and sign everyone out.

| Provider | Issuer | Mind |
|---|---|---|
| Google Workspace | `https://accounts.google.com` | Needs `--domain` (Google's `hd`). Make the consent screen **Internal**. |
| Entra ID | `https://login.microsoftonline.com/<tenant-id>/v2.0` | The tenant's own address, not `common`. `--anyone` means the tenant's people; `--domain` is refused, as Entra doesn't check emails. |
| Keycloak | `https://<host>/realms/<realm>` | Groups need a Group Membership mapper on the client. |
| Auth0 | `https://<tenant>.<region>.auth0.com/` (with the `/`) | With sign-up open, anyone can make an account: use `--domain` or `--group`. Groups need an Action adding a namespaced claim, given with `--groups-claim`. |
| Okta | `https://<org>.okta.com` | Groups need a groups claim on the authorization server. |

The app box holds no Slack token and no OpenRouter key; the only keys in
it are the settings you give it with `hi team env` ([Keys and
settings](/guide/team/keys/)). The app finds its own link in the file named by `$HI_PUBLIC_URL_FILE`, which hi
rewrites whenever the link changes. Anything the people want posted on a
schedule, such as a morning digest, is the lead's scheduled job: a script
that makes the message from production's code, and the lead posts what it
prints.
