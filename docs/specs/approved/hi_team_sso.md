# `hi team sso` specification

Status: Approved (2026-10-27), built in v2.7.0. Drafted 2026-10-16 for
Google only and deferred; any OIDC provider since 2026-10-27. The ideas
under Later are not built.

Dependencies: names that stay for a team's app (`hi team domain`, v0.70.0
and v0.71.0), the relay in `hi net expose` and its link key, and the
helper's sign-in (`hi_team_helper_signin.md`).

## Goal

Let people reach a team's app without a key link by signing in with the
organisation's own identity provider: Google Workspace, Microsoft Entra
ID, Keycloak, Auth0, Okta, Authentik, Zitadel, JumpCloud, or anything
else that speaks OpenID Connect (OIDC). Someone who opens
`https://timmai.app.hi5.sh/` with no key and no session signs in there and
gets in if the team's rules allow them; everyone else is turned away. It is
optional per team, off by default. Key links keep working as they do, also
for people the provider doesn't know.

hi speaks OIDC only, so a provider is an issuer address, a client ID, and
a client secret, with nothing written for one vendor in particular. Where
providers differ (who counts as "the organisation", below), hi knows the
few that matter by their issuer.

## Commands

```text
hi team sso provider add <name> <issuer> [--client-id <id>] [--groups-claim <claim>]
                                        # checks the issuer; asks for the client ID and secret
hi team sso provider rm <name>          # refused while a team uses it
hi team sso <team> on <provider> [--domain <d>[,<d>]] [--group <g>[,<g>]] [--anyone]
hi team sso <team> off
hi team sso                             # the providers, and which teams use which, with their rules
hi team sso <team>                      # the team's provider, rules, and last sign-ins
```

A machine can keep several providers, so teams for different
organisations each sign in with their own. A team uses one.

`on` needs the team on a name that stays (`hi team domain <team> on`), and
prints the return address to register with the provider (below). It needs
at least one rule, or `--anyone`, so that nobody lets in the whole world by
leaving the rules out:

- `--domain example.com`: the account's domain. For Google this is the
  `hd` claim; for other providers, the domain of `email`, and only with
  `email_verified` true. Refused for Entra ID, whose `email` is not
  checked by Microsoft and may be changed by the user.
- `--group engineering`: one of these in the provider's groups claim
  (`groups` unless the provider was added with `--groups-claim`, which
  Auth0 needs, since its custom claims carry a namespace such as
  `https://hi5.sh/groups`).
- `--anyone`: everyone the provider signs in. Right where the issuer is
  the organisation itself: a Keycloak realm, an Entra tenant, an Okta org.
  Refused for Google, whose single issuer signs in every Google account.

`--domain` and `--group` together both have to hold; `--anyone` goes
alone. `on` again with other rules replaces them.

## Adding a provider

`hi team sso provider add` fetches
`<issuer>/.well-known/openid-configuration` and refuses the provider
unless:

- the document's `issuer` is the one given, character for character
  (ID tokens are checked against it). Auth0's issuer ends in `/`; when
  the two differ only by that, hi says which one to use.
- it offers the authorization code flow and an `S256` PKCE challenge, or
  names no challenge methods (some providers support PKCE without saying
  so).
- it is not one of Entra's shared issuers (`common`, `organizations`,
  `consumers`), whose documents name `{tenantid}` as the issuer. hi asks
  for the tenant's own address instead,
  `https://login.microsoftonline.com/<tenant-id>/v2.0`.

Then it asks for the client ID (unless `--client-id` gives it) and the
secret, or reads the secret from standard input
(`< secret.txt`), and saves them on the machine in
`sso-providers.json` beside the teams (mode 600), as `hi team domain`
saves NetBird's token. Adding a provider again under the same name
changes it for the teams that use it, at once: a new secret keeps their
sessions, a new issuer or client ID ends them. GitHub has no
discovery document, since it signs people in with OAuth and not OIDC, so
it can't be added. It would need its own way in.

## How it works

`on` writes `sso.json` in the team's folder (mode 600): the provider, its
client, the rules, a key for the sessions, and where the log goes. The
team's production and preview relays get it as `hi net expose --sso
<file>` (beside `--link` and `--domain`) and read it again when it
changes, so `on`, new rules, and `off` take effect with the next request.
No file is the key only. Both addresses sign in, each on its own: the
cookies are per address, and each has its own return address.

The relay, which already checks the link key, gets a second way in:

1. A request with neither a valid key nor a valid session, for a page
   load (`Sec-Fetch-Dest: document`), is sent to the provider's
   authorization endpoint with `openid email` scopes (and `profile`,
   for a name to show), a `state`, a PKCE challenge, and a `nonce`, plus
   `hd=<domain>` as a hint for Google. `state`, the PKCE verifier, the
   nonce, and the page asked for go in a short-lived cookie, signed with
   the team's key (`hi_sso_start`, 10 minutes, HttpOnly, Secure,
   SameSite=Lax, only for the return path, so it comes back on the
   provider's redirect). Anything else without a key or
   session still gets 401.
2. The provider sends the browser back to `/__hi/auth/callback`. The relay
   checks `state`, exchanges the code with the client secret and the PKCE
   verifier, and verifies the ID token: the provider's signature (its
   published keys, cached, fetched again for a key it hasn't seen),
   `iss`, `aud` (the client ID), `exp`, and the `nonce`. Then the team's
   rules, against the token's claims.
3. The relay sets a session cookie (`hi_sso`, HttpOnly, Secure,
   SameSite=Lax, 14 days) holding the person (`iss` and `sub`, which stay
   the same when an email changes), their email to show, the address, and
   an expiry, signed (HMAC-SHA256) with a key of the team's, and sends the browser on to the page
   it asked for. The app never sees the cookie, as it never sees the key's.
4. hi logs each sign-in (time, provider, email, address) and each refusal
   with the rule that failed, in the team's folder, and `hi team sso
   <team>` shows the last ones.

Changing the team's provider or rules, or `off`, ends the sessions: the
signing key is renewed, or the file is gone.

A team's relays that hi started before v2.7.0 have no `--sso`: the new
argument changes their stamp (`teamRelayStamp`), so a name that stays
gets a new relay, with the same key, as for any change to the relay.

The relay uses `github.com/coreos/go-oidc/v3` for discovery and the ID
token's checks, and `golang.org/x/oauth2` for the code exchange and PKCE.
They add `go-jose/v4` and make `x/oauth2`, already there, a direct
dependency. Neither keeps sessions, so the relay's own cookie stays as
above.

## The provider's side

Done once per organisation, by someone who may register applications
with its provider: a confidential **web** client using the authorization
code flow, with two return addresses per team that uses it, production's
and the preview's, such as
`https://timmai.app.hi5.sh/__hi/auth/callback` and
`https://timmai-preview.app.hi5.sh/__hi/auth/callback`; `on` prints them.
Google and Entra allow no wildcards there, which is why the team needs a
name that stays. A new team means two more addresses. That gives the client ID and secret for
`hi team sso provider add`.

| Provider | Issuer | Notes |
|---|---|---|
| Google Workspace | `https://accounts.google.com` | Consent screen of type **Internal**, so Google turns away accounts outside the organisation before hi sees them. Needs `--domain`; hi still checks `hd`, since a client could be External. |
| Microsoft Entra ID | `https://login.microsoftonline.com/<tenant-id>/v2.0` | Single-tenant app registration. The issuer pins the tenant, so `--anyone` means the tenant's people. Groups come as object IDs, with the groups claim turned on in the registration. |
| Keycloak | `https://<host>/realms/<realm>` | Confidential client with standard flow. Groups need a Group Membership mapper on the client. |
| Auth0 | `https://<tenant>.<region>.auth0.com/` | Regular Web Application. A tenant with sign-up open lets anyone make an account, so `--anyone` is only right with sign-up off. Groups need an Action that adds a namespaced claim. |
| Okta | `https://<org>.okta.com` (or a custom authorization server's) | OIDC web app. Groups need a groups claim on the authorization server. |

## Later

- **The helper's chat through the same sign-in.** The verified email finds
  the person's Slack account (`users.lookupByEmail`, which needs the
  `users:read.email` scope), so people allowed in who may ask the team
  for work get the chat with the lead without `/hi-<team>`.
- **The person, for the app.** The relay hands the signed-in person to the
  app in a header it strips from incoming requests, as it does
  `X-Hi-User` for hi's routes, so apps can show who is there.
- **One return address for every team** (such as
  `login.app.hi5.sh/callback`, which hands a short-lived signed code to the
  team's address), so a new team needs no change at the provider.
- **Signing out with the provider.** Its back-channel logout ends the
  person's sessions when the organisation signs them out or removes them.
- Single people outside the rules, by email.

## Not chosen

**NetBird's own sign-in.** NetBird's reverse-proxy services can require
SSO (`bearer_auth` with distribution groups) through the NetBird account's
identity provider. hi could switch it on through the API it already uses,
but:

- It gates the whole address, so key links stop working for people
  outside the organisation: it can't be "a key or a sign-in".
- Everyone who opens the app must be a user in the NetBird account.
- It means "the organisation's people" only if the NetBird account signs
  in through that organisation's provider.

**SAML.** Signed XML is where Go's SSO libraries break most often
(signature bypasses in `crewjam/saml`, five advisories for `gosaml2` in
2026 alone). An organisation that only has SAML, or LDAP, puts Keycloak or
Dex in front, which sign people in there and speak OIDC to hi.

**A library with an adapter per vendor** (`markbates/goth`, `hashicorp/cap`,
`zitadel/oidc`'s relying party). Every provider above speaks OIDC, so
adapters add code and dependencies without adding providers. `goth` is
for social sign-in and has no Keycloak adapter; `cap` wraps an older
`go-oidc`; `zitadel/oidc` brings its own cookies and many dependencies.

## Open questions

1. How long a session lasts: 14 days, or as long as the ID token says.
2. Whether production with sign-in on should still accept key links, or an
   owner can turn keys off for it.
3. Whether providers belong to the machine (as above) or to the team, so a
   team moved to another machine takes its provider along.

Decided while building (2026-10-27): the preview signs in too, on its own
(above), rather than sharing production's session.
