---
title: People and signing in by email
description: "One list of the people on a machine with hi apps, by email: their level in each app, their Slack accounts, their SSH key, and signing in with a link and a code hi emails."
---

A machine with apps keeps one list of its people. A person is an email:
their name, their Slack accounts (one for each workspace), and one SSH
key. Their level in each app stays the app's own
([owner, developer, or contributor](/guide/app/dev/#owners-developers-and-contributors)),
so each change is still said in that app's channel. Someone without
Slack can be on the list too, at any level.

The machine's admins keep the list, on the
[admin page](/guide/app/admin/) under **People**, or with `hi people`:

```sh
hi people                                   # everyone, with their levels in each app
hi people add anna@corp.example --name "Anna Berg" --app payments=developer --invite
hi people key anna@corp.example "ssh-ed25519 AAAA… anna@laptop"
hi people rm anna@corp.example              # out of everything
```

## Adding someone

`hi people add <email>` puts them on the list. `--app <app>=<level>`
gives them a level in an app, and can be given for several apps. In an
app whose Slack workspace has them, they are listed by their Slack
account there, so the lead and `/hi-<app>` know them; elsewhere by their
email. `--invite` emails them a sign-in link for each app they have a
level in, good once, within three days.

On the admin page, **+ Add** does the same: their email, their name, a
level in each app, and whether to email them their way in.

## Slack accounts

hi finds a person's Slack account by their email, in each app's
workspace. That is how someone in two workspaces is one person on the
list. It needs the `users:read.email` scope in the app's Slack app;
`hi app up` says when an app's Slack app lacks it. `hi people` lists
anyone who has a level but no email yet.

When Slack has another email for someone, link them by hand:

```sh
hi people link anna@corp.example U07ABC123
```

## The organisation's domains

```sh
hi people domains                  # what they are
hi people domains corp.example     # set them; several with commas
hi people domains --none           # any address may be added
```

With domains set, someone is added with an address in one of them. An
address from elsewhere is let in by name, with `--outside`, or
**Allow this address** on the admin page.

## One SSH key a person

`hi people key <email> <public key>` sets their key. hi puts it into the
dev box of every app where they are an owner or a developer, now and
when they become one. `--remove` takes it out of each.

## Taking someone out

`hi people rm <email>`, or **Remove from everything…** on their page,
takes their level away in every app, with their SSH key and their
sign-ins, and takes them off the list. An app's only owner stays in that
app, and hi says where; add another owner there first.

## Signing in by email

Slack's links (`/hi-<app>`, `/hi-<app> dev`) keep working. When the
machine also sends email, people sign in without Slack: owners and
developers on the dev page, anyone with a level in the
[helper](/guide/app/helper/)'s Feedback tab, and admins on the admin
page.

1. The page asks for your email. Its answer is the same whether or not
   that email may sign in there.
2. hi emails a link and a six-digit code. Each is good once, for 15
   minutes, and a newer email ends the older one.
3. The link opens a page with a **Sign in** button. Or type the code in
   the tab that asked, when you read the email on a phone. Five wrong
   codes end the request.

On the dev page and in the helper, **Send it to my Slack** sends the
same link and code in a DM from the app's Slack app instead, to the
Slack account that email has in the app's workspace. It works without
email set up on the machine; someone with no Slack account there gets
nothing.

The first time someone signs in by email, the app's ops channel hears
it, with the address and the level, so an email typed wrong is noticed.
hi sends at most three links in 15 minutes to one address.

## How the machine sends email

hi sends through an SMTP account: the organisation's relay, Google
Workspace's, Microsoft 365's, or a service such as Postmark or SES. An
admin sets it on the admin page, under **Keys**, **Email**, or on the
machine:

```sh
hi server mail set --host smtp.corp.example --from hi@corp.example --user hi < password.txt
hi server mail test anna@corp.example
hi server mail            # how it sends, or that it sends none
hi server mail off        # send none: people sign in with Slack links
```

hi sends only two kinds of email, a sign-in and an invitation, in plain
text. Ask your IT team for the SMTP account, or for the machine to use a
relay, and for SPF and DKIM records for the From domain.

See [All commands](/guide/reference/commands/#hi-people) for every
option.
