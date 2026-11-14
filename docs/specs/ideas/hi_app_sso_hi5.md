# Sign-in through hi5.sh

Status: Draft (2026-11-06). Decided 2026-11-06: free for anyone, run
by Himode AB, no logs, private apps only (below).

Dependencies: [hi_app_sso.md](../approved/hi_app_sso.md) (the relay's
OIDC sign-in, `sso.json`, `sso-providers.json`),
[hi_app_dev_access.md](../approved/hi_app_dev_access.md) (owners,
developers, contributors, viewers; people by email, which waits for
sign-in), [hi_app_addresses.md](../approved/hi_app_addresses.md)
(production, Stage, and the dev page, under any names the IT team picks),
[hi_org.md](../approved/hi_org.md) (the machine's device key, where it
has one).

## Decided

- **Free for anyone** who installs hi, with or without a hi server.
- **Run by Himode AB**, which registers hi5.sh with Google and Microsoft,
  goes through their verification, and publishes the terms and the
  privacy policy.
- **No logs.** hi5.sh keeps no record of who signed in, to which app, or
  when, and says so plainly on the sign-in page and in the privacy
  policy. It keeps only what it needs to work: each app's registration
  (client ID, return addresses, the machine's key) and a block list.
- **Himode AB may block or approve** an app, a machine, a domain, or a
  person's account, where it sees malicious intent: phishing, an app
  that turns out to be public, abuse of the emailed codes. The terms say
  so. Blocks come from what hi5.sh sees at registration and at the
  moment of a sign-in, and from reports, not from logs.
- **Private apps only.** hi5.sh signs people in to apps behind NAT and
  reached through a VPN (NetBird or the organisation's own), never to an
  app open to the internet. A public app uses the bring-your-own
  provider mode of hi_app_sso.md. How hi5.sh holds that line is under
  [Private apps only](#private-apps-only).

## Goal

`hi app sso` signs people in with the organisation's own provider. That
works, but someone has to set it up first: an admin who may register
applications with the provider makes a web client, registers two return
addresses for every app, and hands hi the client ID and secret. The app
also needs a name that stays, since Google and Entra allow no wildcards
in return addresses. Many teams never get past that step, and an app
reached only inside a network (`http://demo.corp.example/`) can't use it
at all. Those private apps are exactly the ones hi5.sh is for.

hi5.sh can do that step once, for everyone: it becomes an **identity
broker**. hi5.sh registers itself with Google, Microsoft, and GitHub,
and an app treats hi5.sh as its OIDC provider. In the app it is one
command, or one choice on the dev page:

```text
hi app sso <app> on hi5
```

Someone who opens the app without a session sees hi5.sh's sign-in page
with the providers the app offers, signs in, and comes back to the app,
which decides whether they get in.

**hi5.sh says who someone is; the machine decides who gets in.** The
rules for production, Stage, and the dev page stay in the app's folder,
are set on the dev page, and are enforced by the relay and the dev page's
gate, as now. hi5.sh keeps no rules.

## Why it works inside a network

Every redirect goes through the person's browser, so hi5.sh never
reaches the app:

```text
browser  -> demo.corp.example              relay: no key, no session
         -> hi5.sh/authorize?client_id=<app>&redirect_uri=http://demo.corp.example/__hi/auth/callback
         -> Google / Microsoft / GitHub    hi5.sh's own client
         -> hi5.sh/callback                checks the return address against the app's list
         -> demo.corp.example/__hi/auth/callback?code=...
relay:      exchanges the code with hi5.sh, verifies hi5.sh's ID token, applies the app's rules,
            sets its own cookie (hi_sso), as now
```

- The browser has to reach hi5.sh and the upstream provider. People
  inside a network almost always can.
- The machine needs outbound HTTPS to hi5.sh, for the code exchange and
  hi5.sh's published keys. A machine without it can take the ID token
  straight from the browser (`response_mode=form_post`, signed, with the
  `nonce` the relay set) and check it against keys it fetched earlier
  (Open question 4).
- Google and Microsoft only ever see hi5.sh's one return address. The
  app's return address is anything the browser reaches: a NetBird name,
  a name the IT team keeps, `http://localhost:8080`, a LAN address.
  hi5.sh keeps the list per app, so the app needs no name that stays.
  This is hi_app_sso.md's Later item "one return address for every team",
  taken all the way.

## On the machine

hi5.sh is one more provider in `sso-providers.json`, named `hi5`, with
issuer `https://hi5.sh`. The relay's code (`net_expose_sso.go`) already
speaks OIDC to any issuer, so most of it stays. What changes:

- **No client secret to type.** `hi app sso <app> on hi5` registers the
  app with hi5.sh, signed with the machine's key (the hi server device key
  where there is one, otherwise a key hi makes on first use), and gets a
  client ID and secret back (OAuth dynamic client registration, RFC
  7591, behind that key). hi5.sh refuses a public address (below), and
  `on` says why and points to the bring-your-own mode. The app's return addresses are the ones
  `hi app addresses` gives it, one per address that signs in; a change of
  addresses updates them at hi5.sh.
- **No name that stays needed** when the provider is `hi5`. `on` still
  refuses an app with no address that a browser can reach.
- **The app's own audience.** Every app has its own client ID, so a token
  for one app is refused by another (`aud`).
- **The upstream provider in the token.** hi5.sh's ID token carries the
  usual `sub`, `email`, and `email_verified`, plus `idp` (`google`,
  `microsoft`, `github`, `email`) and what identifies the organisation at
  that provider: `hd` for Google, `tid` for Microsoft. The rules match on
  those, not on the text of an email.
- **`off`** removes the client at hi5.sh as well as `sso.json`.

The bring-your-own provider mode stays as it is, for organisations that
won't send their sign-ins through hi5.sh, and for Okta, Keycloak, and the
rest until hi5.sh has enterprise connections (below).

## On the dev page

A **Sign-in** section, for owners:

| Setting | Values |
|---|---|
| Sign in with | hi5 (Google, Microsoft, GitHub, a code by email; tick which), or a provider on the machine |
| Production | people at an organisation (`corp.example`), people by email, or anyone signed in |
| Stage | owners, developers, contributors, and people by email |
| Dev page | owners and developers |

- **An organisation** is typed as a domain. For Google that is `hd`. For
  Microsoft hi looks the tenant up from the domain
  (`https://login.microsoftonline.com/<domain>/v2.0/.well-known/openid-configuration`
  names it) and matches `tid`, since Entra's `email` isn't checked. GitHub
  and a code by email have no organisation, only people by email.
- **People by email** finish what hi_app_dev_access.md waits for. A
  signed-in email finds the person's Slack account
  (`users.lookupByEmail`, scope `users:read.email`) and so their level:
  a developer signs in to the dev page instead of asking
  `/hi-<team> dev` for a link, and a contributor's Report goes through
  `X-Hi-SSO`. People outside Slack can be added by email to Stage and
  production as viewers.
- **"Anyone signed in"** with hi5 means anyone with a Google, Microsoft,
  or GitHub account, or an email address. The page says so before an
  owner picks it.
- The dev page is never open to "anyone": owners and developers only, as
  the gate has it now.
- Groups from the provider don't come through hi5.sh's shared client
  (Google puts none in its ID tokens, and Entra's need more consent), so
  the lists above take their place.

## Providers through hi5.sh

| Provider | hi5.sh's side | The organisation's side |
|---|---|---|
| Google | One External OAuth client. `openid email profile` are non-sensitive scopes: brand verification, no security assessment. | Nothing. A Workspace admin can block third-party apps; then they allow hi5's client ID. |
| Microsoft | One multi-tenant app registration, with publisher verification (many tenants refuse unverified publishers). | Often an admin's consent, once per tenant, where user consent is off. hi says so and names the step for IT. |
| GitHub | One OAuth app. GitHub speaks OAuth, not OIDC; hi5.sh turns its user and verified emails into an ID token. | Nothing. |
| A code by email | hi5.sh sends a six-digit code. For contractors and people without an organisation's account. Rate-limited per address and per app. | Nothing. |

**Enterprise connections** (later): an organisation's own Okta,
Keycloak, Authentik, or SAML provider, registered once with hi5.sh,
with hi5.sh's single return address, instead of once per app on each
machine. Its groups can then come through, since the client is the
organisation's.

## On hi5.sh

Today hi5.sh is a static site: the guide and `install.sh`. This is its
first service.

- **What runs:** [Dex](https://dexidp.io) (Go, CNCF; connectors for
  Google, Microsoft, GitHub, OIDC, SAML, LDAP) as the broker, and a small
  registration API in front of Dex's gRPC API that checks the device key
  and the return addresses. The page people see is hi's own, with the
  app's name and address on it, so a look-alike page asking for a sign-in
  is easier to spot.
- **The signing key** can vouch for anyone at every app that uses hi5.
  It lives in a KMS or HSM, signs short-lived ID tokens (5 minutes; the
  relay's own cookie carries the session), and rotates through the
  published keys.
- **Return addresses match exactly.** No wildcards, no prefixes: a loose
  match is an open redirect that hands codes to someone else.
- **Registration is tied to a machine.** An app's client belongs to the
  key that made it, so a machine can be blocked, and `off` or
  `hi app rm` cleans up.
- **Plain http** return addresses are allowed (internal names often have
  no TLS) with a warning on the dev page: the code crosses the network
  in the clear (PKCE still keeps it useless on its own) and the cookie
  can't be `Secure`.
- **Down means no new sign-ins.** Sessions already made keep working,
  since the relay checks its own cookie for 14 days. `hi app sso <app>`
  shows when hi5.sh last answered.
- **Privacy.** hi5.sh sees who signs in to which app, and when, while
  the sign-in happens, and writes none of it down: no request logs, no
  sign-in records, no analytics. The web server and Dex run with their
  access logs off; errors are logged without addresses, emails, or
  tokens. Rate limits (per app, per email for the codes) are counters in
  memory that expire. What is stored is the registrations and the block
  list, hosted in the EU. The privacy policy, from Himode AB, says
  exactly this, and the sign-in page says "hi5.sh keeps no record of
  this sign-in". Google and Microsoft verification need that policy, a
  verified domain, and Himode AB as the entity behind them.

## Private apps only

hi5.sh signs in to apps behind NAT, reached through a VPN. It checks
each return address when an app registers, again when its addresses
change, and from time to time after (daily), and refuses or blocks an
app that fails:

- **The address is private.** A literal IP must be loopback, RFC 1918
  (`10/8`, `172.16/12`, `192.168/16`), CGNAT `100.64/10` (NetBird's
  addresses), or IPv6 ULA (`fc00::/7`). A name must not resolve, in
  public DNS from hi5.sh, to anything else: it either has no public
  record (an internal name, `.internal`, `.lan`, a NetBird name) or
  resolves only to private addresses (as the machine's domain does,
  pointing at the team's NetBird address).
- **It doesn't answer from the internet.** hi5.sh tries the address
  from outside (TCP on its port, and 80 and 443); an answer means it is
  public, whatever DNS says.
- **The relay says so too.** The relay refuses to start the hi5 sign-in
  when `hi net expose --public` or a public NetBird reverse-proxy
  service serves the app, so the honest case is caught on the machine
  before hi5.sh has to.

These keep honest setups honest and make abuse take effort; they can't
prove an address is private (a name can resolve differently inside and
out, a firewall can let only hi5.sh's prober out). The terms forbid a
public app, and a block is the answer when one is found or reported.

The machine's https names (`demo.hi.example`) pass: they point at the
team's NetBird address. The NetBird reverse proxy's public addresses
and `--public` don't.

## Releases

1. hi5.sh with Google, Microsoft, and GitHub, identity only;
   `hi app sso <app> on hi5`; registration with the machine's key; the
   private-address checks and the block list; no logs; the
   rules as hi_app_sso.md has them (`--domain`, matched by `hd` and
   `tid`, and `--anyone`).
2. The dev page's Sign-in section: rules per address, people by email,
   developers signing in to the dev page.
3. A code by email.
4. Enterprise connections, with groups.

## Not chosen

- **Rules on hi5.sh.** Simpler to show in one place, but then a breach
  of hi5.sh opens every app, and the rules would leave the app's folder,
  so they wouldn't move with it, and the gate would need hi5.sh to answer
  for every request.
- **hi5.sh in the path** (a tunnel or proxy in front of the app, like
  Cloudflare Access). It would see every request, not only sign-ins, and
  wouldn't reach apps inside a network without a tunnel.
- **Writing the broker from scratch** with `go-oidc`. The token part is
  small; the connectors (GitHub's OAuth, Microsoft's tenants, SAML later)
  and their edge cases are what Dex already has.

## Open questions

Who runs it, what it costs, and what it keeps are decided (above).

1. What limits a free app has (sign-ins a minute, emailed codes an
   hour), so one app can't spend the service for everyone.
2. Whether to support machines with no outbound HTTPS (`form_post` and
   keys fetched ahead of time), or require it.
3. Whether key links stay on for production when hi5 sign-in is on
   (hi_app_sso.md's Open question 2 applies here too).
4. How an app that was blocked hears about it and appeals: a message
   in `hi app sso <app>` and on the dev page, and an address at Himode
   AB to write to.
