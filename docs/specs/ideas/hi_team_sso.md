# `hi team sso` specification

Status: Draft (deferred, 2026-10-16)

Dependencies: names that stay for a team's app (`hi team domain`, v0.70.0
and v0.71.0), the relay in `hi net expose` and its link key, and the
helper's sign-in (`hi_team_helper_signin.md`).

## Goal

Let people reach a team's app without a key link by signing in with
Google, limited to one Google Workspace domain, such as plejd.com.
Someone who opens `https://timmai.app.hi5.sh/` with no key and no session
signs in with their plejd.com account and gets in; everyone else is
turned away. It is optional per team, off by default. Key links keep
working as they do, also for people outside the domain.

## Commands

```text
hi team sso google                      # the Google OAuth client for this machine: asks for its ID and secret
hi team sso <team> on <domain>[,<domain>]   # sign-in with Google for the team's app, these Workspace domains only
hi team sso <team> off
hi team sso                             # the client, and which teams have it on, for which domains
```

`on` needs the team on a name that stays (`hi team domain <team> on`), and
prints the return address to register in Google (below).

## How it works

The relay, which already checks the link key, gets a second way in:

1. A request with neither a valid key nor a valid session, for a page
   load (`Sec-Fetch-Dest: document`), is sent to Google's sign-in with
   `openid email` scope, `hd=<domain>` as a hint, a `state`, a PKCE
   challenge, and a `nonce`. `state`, the PKCE verifier, the nonce, and the
   page asked for go in a short-lived cookie (`hi_sso_start`, 10 minutes,
   HttpOnly, Secure, SameSite=Lax, so it comes back on Google's redirect).
   Anything else without a key or session still gets 401.
2. Google sends the browser back to `/__hi/auth/callback`. The relay
   checks `state`, exchanges the code with the client secret and the PKCE
   verifier, and verifies the ID token: Google's signature (its published
   keys), `iss`, `aud` (the client ID), `exp`, the `nonce`,
   `email_verified`, and **`hd` equal to one of the team's domains**. An
   email ending in `@plejd.com` is not enough: `hd` is what says the
   account belongs to the Workspace.
3. The relay sets a session cookie (`hi_sso`, HttpOnly, Secure,
   SameSite=Lax, 14 days) holding the email, the domain, and an expiry,
   signed with a key of the team's, and sends the browser on to the page
   it asked for. The app never sees the cookie, as it never sees the key's.
4. hi logs each sign-in (time, email, address) in the team's folder, and
   `hi team sso <team>` shows the last ones.

Changing the team's domains, or `off`, ends the sessions: the signing key
is renewed.

## Google's side

Done once, by someone at the organisation who may create Google Cloud
projects:

1. A Google Cloud project in the organisation's Google Workspace.
2. An OAuth consent screen of type **Internal**: Google then refuses every
   account outside the organisation before hi sees it. hi still checks
   `hd`, since a client could be External.
3. An OAuth client of type **Web application**, with one authorized
   redirect URI per team that uses it, such as
   `https://timmai.app.hi5.sh/__hi/auth/callback`. Google allows no
   wildcards there, which is why the team needs a name that stays. A new
   team means one more URI.

That gives a client ID and a client secret, which `hi team sso google`
saves on the machine (mode 600), as `hi team domain` saves NetBird's token.

## Later

- **The helper's chat through the same sign-in.** The Google email finds
  the person's Slack account (`users.lookupByEmail`, which needs the
  `users:read.email` scope), so people on the domain who may ask the team
  for work get the chat with the lead without `/hi`.
- **One return address for every team** (such as
  `login.app.hi5.sh/callback`, which hands a short-lived signed code to the
  team's address), so a new team needs no change in Google.
- Single people outside the domain, by email.

## Not chosen: NetBird's own sign-in

NetBird's reverse-proxy services can require SSO (`bearer_auth` with
distribution groups) through the NetBird account's identity provider.
hi could switch it on through the API it already uses, but:

- It gates the whole address, so key links stop working for people
  outside the domain: it can't be "a key or Google".
- Everyone who opens the app must be a user in the NetBird account.
- It means "plejd.com accounts" only if the NetBird account signs in
  through plejd.com's Google Workspace.

## Open questions

1. Whether a session should also cover the preview, or each address signs
   in on its own (cookies are per address, so separately unless the
   return address is shared).
2. How long a session lasts: 14 days, or as long as Google's own session.
3. Whether production with sign-in on should still accept key links, or an
   owner can turn keys off for it.
