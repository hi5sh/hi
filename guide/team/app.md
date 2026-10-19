---
title: Try what the team built
description: "How a hi team shows its work: the preview and production links that open on a phone, what the app box holds, and addresses that stay."
---

After a merge, the lead shows the new version on the team's preview link,
so the people can try the work, on their phones too. The link carries its
own key, so there is no password to copy:

1. hi starts the project in an app box of its own, from the merged code (or
   a task's branch, before it is merged), with `make run`, or the
   `start` script: `bun run start` with a `bun.lock`, else `npm start`.
   The app listens on `$PORT`; its port reaches this machine only.
2. `hi net expose --link` puts it on a temporary HTTPS address through this
   machine's NetBird (see [hi net](/guide/workstation/expose/)), which only
   the link with its key opens. The machine must be enrolled with `hi net`,
   and Peer Expose must be on in NetBird.
3. The link stays the same from one preview to the next: the new version
   starts beside the old one, and the address moves to it once it
   answers, so the people just reload. A link lasts a day; a preview gets
   a new link only when the old one has less than 12 hours left. If the
   new version doesn't start, the link keeps showing the old one.

A new team's app is on the preview as soon as `hi team up` starts the
team, so the links people get from `/hi` work before anything is built.
It is a welcome page that asks "What should your app do?" with a message
box in the middle: what you type there opens the chat with the lead beside
the page and goes to it. Before you sign in with `/hi`, it points at the
chat button instead. The first feature replaces it. After the first
merge, the lead makes the previews.

Production has a link of its own, which shows the latest release. hi
renews it before it runs out each day, and keeps one message in the
channel with production's current link, edited whenever the link or the
version changes. Each release's announcement links to that message, so
nobody has to hunt for the latest link.

## Addresses that stay

By default NetBird makes up each address, and production's changes every
day. With a domain of your own verified in NetBird, a team you turn it on
for gets names that stay: production at `<team>.<domain>` and the preview
at `<team>-preview.<domain>`, renewed every 30 days on the same name.
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
preview with the next one. A team still running an older hi moves when
it is restarted. If
the name doesn't work, hi uses an address NetBird makes up, tries the name
again after half an hour, and keeps why in `hi team preview`'s answer.

The app box holds no credentials: no Slack token and no API keys. The app
finds its own link in the file named by `$HI_PUBLIC_URL_FILE`, which hi
rewrites whenever the link changes. Anything the people want posted on a
schedule, such as a morning digest, is the lead's scheduled job: a script
that makes the message from production's code, and the lead posts what it
prints.
