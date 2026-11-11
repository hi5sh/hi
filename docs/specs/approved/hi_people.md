# People on a machine, and signing in by email

Status: Approved (2026-11-11), built in v2.6.133. Releases 1 to 3 below
are built; Later is not.

Dependencies: [hi_admin.md](hi_admin.md) (the admin page, its People
view, its sign-in), [hi_app_dev_access.md](hi_app_dev_access.md)
(owners, Pro Devs, Vibe Devs, contributors; "people by email, which waits
for sign-in"), [hi_app_helper_signin.md](hi_app_helper_signin.md) (keys
and the relay's `hi_user` cookie), [hi_app_sso.md](hi_app_sso.md) and
[hi_app_sso_hi5.md](../ideas/hi_app_sso_hi5.md) (signing in with a
provider).

## Goal

Until v2.6.133 a person was a Slack member ID. The admin page's People
was a grid of every person and app, a drop-down in each cell, and showed
the same person twice when they were in two Slack workspaces (member IDs
are a workspace's). Adding someone meant copying a member ID from Slack's
profile menu. Nobody outside Slack could be added at all. SSH keys were
per app. Taking someone out meant None in every column. And the only way
to sign in was a link Slack sent: `/hi-<app> dev`, `/hi-<app>`, or the
admin page's Send me a new link.

So:

- **A person is an email.** The machine keeps one list of people: each
  one's name, email, Slack accounts (one per workspace), and SSH key.
  Someone in two workspaces is one person.
- **People without Slack** can be added, at any level.
- **hi sends email**, and everyone with a level signs in by email on the
  dev page, in the helper, and on the admin page, beside Slack's links.
- **The admin page's People** is a list of people, each opening on a
  page of their own: their level in each app, their SSH key, their
  sign-ins, and Remove from everything.

## The machine's people

`people.json` beside the apps' links (`~/.local/share/hi/apps/`, mode
600):

```json
{
  "domains": ["corp.example"],
  "people": [
    {"email": "anna@corp.example", "name": "Anna Berg",
     "slack": ["U07ANNA", "W09ANNA"], "ssh_key": "ssh-ed25519 AAAA… anna@laptop",
     "added": "…", "by": "Olga on the admin page", "signed_in": "…", "invited": "…"}
  ]
}
```

- **Levels stay in each app's `app.json`**, so an app's folder still
  moves on its own and each change is still said in its channel. The
  lists take an email as well as a Slack member ID: someone in the app's
  workspace is listed by their member ID there, so Slack, the lead, and
  `/hi-<app>` know them; anyone else by their email.
- **Slack accounts are found by email.** With the `users:read.email`
  scope, hi asks each app's workspace who has an email
  (`users.lookupByEmail`) when someone is added or signs in, and what
  email a member ID listed in an app has (`users.info`, once an hour at
  most for each), and links them on the list. That's what makes two
  workspaces one person. Without the scope, an admin links someone by
  hand (`hi people link`, or Link one on their page), and `hi app up`
  says the app's Slack app lacks it.
- **The organisation's domains.** People are added with an address in
  one of them, unless an admin says they're outside, which the list
  keeps. No domains lets in any address.
- **One SSH key a person**, put into the dev box of every app where
  they're an owner or a Pro Dev, now and when they become one.
- An app made by `hi app new` still takes member IDs; the list fills in as
  people are found.

## The admin page

**People, on the left**, is a list: each person's name, email (or "no
email yet"), their levels in short ("Owner in 3", "payments: Pro Dev"),
admin, and when they last signed in by email. A search, a filter by app,
and a **Grid** that shows every level at once, each a badge that opens
the person. **+ Add** opens Add someone. Notes say when the machine
sends no email, and which apps' Slack apps lack `users:read.email`. At
the bottom, the organisation's domains, with Change.

**A person**, in the middle as an app is:

- Their email (Rename, or Give an email to someone known only in Slack),
  their Slack accounts (Link one), their SSH key (Set key, New key,
  Remove), and when they signed in by email and were invited.
- **Apps**: a level for each app, each change the app's own, asked first
  and said in its channel, with the entry the app lists them as.
- **Email a sign-in link**: one email, with a link for each app they have
  a level in (the dev page for someone who builds, else production, else
  Stage) and the admin page for an admin. Each link works once, within
  three days.
- **Remove from everything…**, with their email typed: None in every app,
  which ends their links and keys there, and off the list. An app's only
  owner stays, and the page says where.

**Add someone**: their email (picked from the apps' Slack workspaces, by
name, or typed), their name, outside the organisation or not, a level in
each app, and "Email them their way in", ticked when the machine sends
email.

An app's own People section takes an email as well as a member ID.

**Keys** has **Email**: the SMTP server, port, security (STARTTLS, TLS
from the start, or none for a relay on the network), user name,
password (kept, never shown), From address and name; **Send a test**;
**Turn off**; and what to ask the IT team for.

## Sending email

hi speaks SMTP only, as the sign-in speaks OIDC only: every sender has
it (an organisation's relay, Google Workspace's, Microsoft 365's,
Postmark, SES, Resend). The machine keeps one setting, `mail.json` beside
the apps' links (mode 600), which the admin page and every app's service
read.

- **Only two kinds of email:** a sign-in (a link and a code) and an
  invitation. Plain text, no images, nothing that tracks who opened it,
  `Auto-Submitted: auto-generated`.
- STARTTLS is required on 587 unless security is none; a password never
  goes without TLS except to a relay on this machine.
- Invitations are in Activity; sign-ins in the app's log; never the link.
- The IT team gives an SMTP account, or lets the machine use a relay, and
  SPF and DKIM records for the From domain; the page says so.

## Signing in by email

On the dev page (owners and developers), in the helper on Stage and
production (anyone with a level: Report, and the chat with the lead),
and on the admin page (its admins). Slack's links keep working beside
it.

1. The page asks for an email: the dev page's and the admin page's
   sign-in screens, and the helper's Feedback tab ("A contributor or
   developer here? Sign in with your email").
2. The answer is the same whether or not the email may sign in there,
   and the email goes out after the answer, so neither its words nor its
   time says who may.
3. The email has a link and a six-digit code, each good once, for 15
   minutes. A newer one ends the older.
4. **The link opens a page with a Sign in button.** The scanners in front
   of company mailboxes (Microsoft Defender's Safe Links, Mimecast) open
   every link in an email; a link that signed in on its own would be
   spent before its person clicked it.
5. **The code is typed in the tab that asked**, for mail read on a phone.
   It belongs to that tab's request; five wrong codes end it.
6. Either ends in the sign-in a Slack link gives: on an app's addresses,
   a key in the app's `people.json` that the relay turns into its
   `hi_user` cookie (a key of its own for each device, so signing in on
   a phone keeps the laptop signed in), for 14 days; on the admin page,
   its session.

What keeps it safe:

- **The link is built from the address's own setting** (the dev page's,
  the slot's, the admin page's), never from the request's Host header:
  a forged Host would otherwise have hi email a working link to someone
  else's site.
- Tokens and codes are kept as hashes, in `signins.json` in the app's
  folder and the admin page's, so a restart doesn't end them.
- Three emails in 15 minutes for an address, ten an hour from one
  address, a hundred an hour for a page.
- **The first sign-in by email is said in the app's ops channel**, with
  the address and the level, so an email typed wrong is noticed.
- Taking a level away counts at the next request, as before.
- The confirm page sends referrers only to its own address: with none, a
  browser sends the button's POST with `Origin: null`, which the pages'
  own checks refuse.

## Commands

```text
hi people                                  everyone, their levels, Slack accounts, keys, sign-ins
hi people add <email> [--name <name>] [--slack <member ID>] [--outside]
    [--app <app>=owner|pro|vibe|contributor ...] [--invite]
hi people rm <email>                       remove them from everything
hi people key <email> <public key>|--remove
hi people link <email> <member ID>
hi people domains [<domain>[,<domain>]|--none]

hi admin mail                              how the machine sends email
hi admin mail set --host <host> --from <address> [--port <port>]
    [--security starttls|tls|none] [--user <name>] [--name <from name>]
hi admin mail test <address>
hi admin mail off

hi app access <app> <member ID>|<email> pro|vibe|contributor|none
hi app owners <app> add|remove <member ID>|<email>
```

## Releases

1. **The person and mail** (v2.6.133): the machine's list, emails in the
   apps' lists, `users:read.email`, joining by email, SMTP and its
   setting.
2. **Signing in by email** (v2.6.133): the dev page, the helper, the
   admin page, invitations.
3. **The People view** (v2.6.133): the list, the person, the grid, Add
   someone, one SSH key a person, Remove from everything, Email on Keys.

## Decided (2026-11-11)

- **Mail is the machine's**, set on each machine. The org server sending
  for the machines joined to it is Later.
- **Owners sign in by email too.** The first sign-in is said in the ops
  channel; an organisation that wants more turns on SSO.
- **The organisation's domains by default**, once an admin sets them; an
  outside address is let in by name.
- **Sign-ins and invitations only.** The lead's messages to someone
  without Slack aren't emailed.

## Later

- **The org server's mail and people**: one setting and one list for the
  machines joined to an org, and the org's users (hi user names) joined to
  the apps' people by email, which hi_admin.md says are "the same person
  in both, unlinked".
- **Signing in with a provider finds the same person**: the verified
  email from `hi app sso` (and hi5.sh) is matched to the list, so a
  developer signs in to the dev page with the organisation's account.
- **The app's front door by email**: viewers let in by a code at the
  relay, for an address that isn't on NetBird. hi_app_sso_hi5.md's "a
  code by email" is then for machines without SMTP.
- Emails to someone without Slack when the lead writes to them.
