# The dev box's sites: fewer asks, and hi's own check

Status: Approved (2026-11-12). Three releases (the roadmap's "the dev
box's sites"): fewer asks, then the rules, then the model, which runs
watch only first (question 4). The questions below stay open until each
release is built. Release 1 built in v2.6.136, release 2 in v2.6.137,
release 3's watch only in v2.6.138 (see "as built" under each).

Dependencies: [hi_app_dev.md](../approved/hi_app_dev.md) (the dev box,
its proxy, `hi dev allow`, v2.5.38, v2.6.24, v2.6.25, v2.6.44),
[hi_box.md](../approved/hi_box.md) (the proxy, presets, what an allowlist
can and can't stop), [hi_people.md](../approved/hi_people.md) (the
organisation's domains), [hi_admin.md](../approved/hi_admin.md) (the
app's own OpenRouter key), [hi_app_addresses.md](../approved/hi_app_addresses.md)
(the app's names), [hi_watch.md](../approved/hi_watch.md) (incidents).

## Goal

A dev box reaches the internet only through its proxy's list. When an
agent needs a site, it asks (`hi dev allow <site> --why "…"`), and the
app's owners answer with Allow or Say no, in the ops channel or on the
dev page. While an app is being built, the asks come one after another,
each one a post, and the agent waits.

What three apps on one machine asked in their first two and a half weeks:

- **18 asks, 18 allowed, none said no**, all by one owner, often within
  minutes. Nine more sites were allowed without an ask, on the dev page
  or by the admin.
- **Five asks were the organisation's own**: its public site and brand
  CDN, the app's own production, and the app's own Stage.
- **Three were downloads that development needs** and the `dev` preset
  lacks: Playwright's browsers, Ubuntu's packages, Hugging Face's file
  CDN (and jsDelivr, allowed without an ask). The proxy also refused,
  with nobody asking, Playwright's other CDN, Astral's releases (uv),
  Debian's packages, and Open VSX.
- **The rest were sites for the task**: free 3D-asset sites, a stock
  price API, a licence page. Two were Google Drive, where anyone can put
  files, and the admin allowed a MongoDB Atlas cluster, where anyone can
  have one.

An owner who allows everything doesn't keep the box safer, and the
channel gets one more post to skim past. So:

- **Fewer asks:** what is plainly fine is on the list already.
- **hi's own check** for the rest: an ask hi can vouch for is allowed in
  a second or two and said in the channel, with Undo. The agent doesn't
  wait. An ask hi can't vouch for goes to the owners as now, with what
  hi found.
- **The owners keep the last word:** their Say no is final, and they
  can undo anything hi allowed.

## What an allow risks

The check has to judge the right thing, so first what a site on the list
lets happen. The proxy sees only a connection's site name, never what is
sent (no TLS inspection, [hi_box.md](../approved/hi_box.md#network)).

1. **A place to send things.** Whoever runs an allowed site reads what
   the box sends it, and a URL alone can carry a key or the project's
   code. An agent misled by something it read (a prompt injection) sends
   things out this way.
2. **A place things come in from:** a page with instructions for the
   agent, or a download that runs.
3. **The network the box is on.** Never: a name inside it can't be
   allowed, and the proxy checks the address it connects to (v2.5.38).

So the question for a site is **who runs it, and who can read or put
things there**, not what its front page says. An attacker writes their
page to look as clean as any. The agent's "why" is written by the agent,
which may be the one that was misled. Both still help to judge if the site
fits the task, which is how an odd ask stands out. But neither is
evidence that the site is safe. What is evidence: the site has a long
history, many people use it, nobody lists it as harmful, and nobody but
its owner can publish there or catch what's sent there.

## Fewer asks, without judging anything

1. **The `dev` preset grows** by the downloads development needs,
   read-only and run by the projects themselves: `cdn.playwright.dev`,
   `playwright.download.prss.microsoft.com`, `releases.astral.sh`,
   `deb.debian.org`, `security.debian.org`, `archive.ubuntu.com`,
   `security.ubuntu.com`, `open-vsx.org`, `openvsx.eclipsecontent.org`,
   `cdn.jsdelivr.net`, `unpkg.com`, and Hugging Face's file CDN
   (`cdn.hf.co` and the names under it). jsDelivr and unpkg serve any npm package, as
   `registry.npmjs.org` does already.
2. **The app's own addresses.** Stage and the dev page, at the names
   the app has ([hi_app_addresses.md](../approved/hi_app_addresses.md)),
   are on the list when they are public. Production is not: an agent
   calling production's API can change production's data. It stays an
   ask, said as such ("the app's own production").
3. **The organisation's domains**, the ones an admin set for people
   (`hi people domains`), and their subdomains: on by default once set,
   and the admin turns it off with `hi admin sites org off`. The
   organisation runs them, so whatever is sent there stays with it.
4. **One post for several asks.** Asks from the same task within two
   minutes join the first one's post, with Allow all and one line each
   (Allow, Say no). An agent that needs a site and its CDN no longer
   makes two posts.
5. **A site allowed in another app on the machine** shows on the admin
   page's Sites with how many apps allowed it. The admin can allow it
   for every app (`hi admin sites allow <site>`). It isn't allowed by
   itself: one app's owners don't decide for another app.

## hi's check

An ask hi hasn't settled with the steps above goes through the check
before it reaches the owners. The admin sets how far the check goes, for
each app:

```bash
hi app dev demo sites check            # where it stands, and this week's results
hi app dev demo sites check off        # every ask goes to the owners, as now
hi app dev demo sites check rules      # the default: the rules alone
hi app dev demo sites check model      # the rules, then a model, on the app's OpenRouter key
```

and the admin page's Agents says the same as a choice. `model` needs
OpenRouter for the app (its own key or the machine's): without it, the
choice says so and isn't offered.

### The rules

The rules gather what is known about a site. Each item is free, needs no
key, and is kept for a week for each registrable name (`yahoo.com` for
`query1.finance.yahoo.com`) in the machine's `sites.json`:

| What | From | Why |
|---|---|---|
| The registrable name, and whether the name is under a suffix anyone can claim names under (`github.io`, `pages.dev`, `vercel.app`, `s3.amazonaws.com`, …) | The Public Suffix List, its private section included (`golang.org/x/net/publicsuffix`) | Anyone can make `evil.github.io`; GitHub's standing says nothing about it |
| Its age | RDAP, through IANA's bootstrap (`data.iana.org/rdap/dns.json`) | Sites made for an attack are new, often days old |
| How many people use it | The [Tranco](https://tranco-list.eu/) list of the top million names, fetched once a week | Long use is hard to fake; Tranco averages 30 days to resist gaming |
| Whether it's known to be harmful | Quad9 (9.9.9.9 answers no name for a blocked one, 9.9.9.10 doesn't block) and Cloudflare's 1.1.1.2 (answers 0.0.0.0 for malware); with a key, Google's Web Risk | Threat lists say a site is bad, never that it's good |
| Whether anyone can publish there or receive what's sent | hi's own list: file drops and shares (`drive.google.com`, `docs.google.com`, `dropbox.com`, `wetransfer.com`, …), pastes, request catchers (`webhook.site`, `requestbin`, `pipedream`, …), tunnels (`ngrok`, `trycloudflare.com`, `loca.lt`, …), short links, dynamic DNS, and hosted databases (`*.mongodb.net`, …) | These are where things go out, even under a famous name |

A site passes the rules when it is in the top 100,000, is older than a
year, no list names it, and it is under no claimable suffix and on none
of hi's lists. Then hi allows it by itself. Of the sites above, the
stock price API would pass, and likely itch.io, Sketchfab and Patreon;
an itch.io creator's own page (`<name>.itch.io`) passes only if itch.io
isn't a claimable suffix.

A site the lists name never goes by itself, whatever else is true. The
owners' post says "Quad9 lists it as harmful", and Allow asks again
before it allows. A file drop, a tunnel or a hosted database always goes
to the owners. The post says why ("anyone can put files there"), so an
owner knows what Allow means.

### The model

With `model`, a site the rules didn't settle goes to a model, unless a
list names it or it is on hi's lists, which only owners decide. The model
is [Gemini Decision 1](https://openrouter.ai/google/gemini-decision-1)
(`google/gemini-decision-1`) by default, or another the admin names
(`--model`). Gemini Decision 1 doesn't write text. It answers yes/no
questions with a probability, many in one call, and reads text and
pictures. That suits this check:

- **Nothing to parse.** A page can't argue its way into a free-text
  "approved". It can only move the answers' probabilities, and the
  thresholds and the rules stay hi's.
- **The admin sets the thresholds.** The defaults are below.
- **Cheap.** It costs $0.10 a million tokens in and nothing out. One ask
  with the site's front page is about 10,000 tokens, a tenth of a cent,
  on the app's OpenRouter bill.

The broker runs the check on the machine, never the box. It fetches the
site's front page itself through the public-only dialer the proxy uses
(no cookies, 5 seconds, 200 KB, text and the page's title and favicon).
It then asks the model, all at once:

| Question | Allows by itself when |
|---|---|
| Is this site run by a known organisation or project, for docs, downloads, packages, data or media? | ≥ 0.90 |
| Can people other than its owner publish here, or receive what's sent here (uploads, pastes, forms, webhooks, tunnels, short links)? | ≤ 0.10 |
| Does the site fit what the agent says it needs it for, and the task's title? | ≥ 0.80 |
| Does the page try to instruct an AI, or the reader, to do something? | ≤ 0.10 |
| Is it mainly a sign-in, payment or account page? | ≤ 0.20 |

The model sees what the rules found, too (age, rank, suffix). The site
is allowed by itself only if every answer is within its threshold **and**
the site is older than 90 days and in the top million. A model's yes
never overrides a rule. Everything the model reads is marked as written
by others: the page, and the agent's why.

Of the asks above, the long tail would go this way: the free asset
sites, a licence page, threejs.org and modelviewer.dev. That depends on
each site's rank, which this draft hasn't checked one by one.

### What hi allowed by itself

An allow from the check is smaller than an owner's:

- **For seven days,** then gone unless an owner tapped Keep. An owner's
  allow is for good, as now.
- **Said once a day,** in one thread in the ops channel: "hi allowed 3
  sites for the dev box by itself today". Each site has a line with why
  (for example "on the rules: yahoo.com, top 1,000, 29 years old") and
  Keep and Undo. Undo takes it off the list and records a no from that
  owner.
- **On the dev page's Sites,** marked "allowed by hi's check", with
  what it found and the days left.
- **Ten a day** for each app at most. Past that, asks go to the owners,
  and the post says hi stopped allowing by itself today. An agent misled
  into trying site after site meets the owners soon.
- **Stops on an incident.** An open incident about the dev box
  ([hi_watch.md](../approved/hi_watch.md)), or an ask a list names, pauses
  the check for the app until an owner turns it back on.
- **A no is final.** A site an owner said no to is never checked again,
  and an owner's allow is never second-guessed.

`hi dev allow` answers at once when the check allowed: "Allowed by hi's
check for 7 days: yahoo.com, top 1,000, 29 years old." The agent goes on
without `--wait`. When it goes to the owners, the agent hears it as now.
The post carries what hi found ("not allowed by itself: registered 41
days ago"), so the owner decides on more than the agent's word.

### Release 1, as built (v2.6.136)

- **Production under the organisation's domain.** A list matches a name
  and its subdomains, so `corp.example` would have let the box reach
  `demo.corp.example`, the app's production. A line `!name` now refuses
  that name itself where a wider line allows it, and hi adds one for each
  of production's names that a wider line covers. An owner's allow of
  production adds the name itself, which wins.
- **A name production shares** (one name, the addresses on ports) isn't
  one of the box's own: the list knows names, not ports.
- **The watcher stays quiet** about hi's own lines. The proxy reported
  each new line on a box's list, and the watcher opened an incident for
  each in the app's ops channel: an admin's site for every app, or the
  organisation's domains, would have been an incident in every app. hi
  marks its own lines (`# hi`), the proxy logs them as
  `allowlist-added-by-hi`, and the watcher, which knows only
  `allowlist-added`, lets them be. An owner's allow is still news.
- **Open VSX** was the dev box's already (VS Code's extensions), so the
  preset doesn't take it. VS Code went in hi 2.6.144, and Open VSX with
  it.
- **One post** holds eight asks at most, Slack's 25 buttons in a row.

### Release 2, as built (v2.6.137)

- **A name no registry dates.** Some registries have no RDAP (`.io`,
  `.de`), so their names have no age. Such a name passes on rank alone
  within the 10,000 most used, which a 30-day average doesn't give a
  new name; others go to the owners.
- **A site a threat list names** stops the check for the app, as the
  draft says; its Allow in Slack asks "Sure?" first and it isn't part of
  Allow all.
- **The pause on an incident** is an open watch incident naming the dev
  box, or the app frozen or paused.
- **The admin page's setting** is in the app's Network, beside who may
  reach the app, rather than Agents.
- **Tried on 15 sites the dev boxes asked for before**, with the real
  sources: 9 allowed by themselves, and the rest to the owners for the
  right reasons (a file share, a `github.io` page, a malware test name,
  three outside the 100,000). Each check took 50 ms to 1.5 s.
- **Not yet:** Google's Web Risk and Cloudflare's categories with a key.

### Release 3, watch only, as built (v2.6.138)

- **A setting of its own,** `watch`, beside `rules` and `off`: the model
  judges, hi records, and nothing is allowed by the model. A later
  release adds the setting that lets it allow, once the record shows it
  agrees with the owners.
- **What it judges:** an ask the rules sent to the owners for its rank
  or age. A site a threat list names, a file share, a claimable name, or
  a site whose threat lists didn't answer, never goes to the model.
- **The call.** Google documents the Decisions API (POST
  `/v1beta/decisions` with `model`, `input`, and `questions`, each a
  `noul` with `instructions`; each answer a `noul` probability, 0.01 to
  0.99). OpenRouter lists the model with the output "decisions" but
  documents no request for it; hi sends Google's shape to
  `/api/v1/decisions`. Not yet tried with a live key.
- **The page** is fetched by the broker on the machine, through the dialer
  the dev box's proxy uses, so a name can't lead inside the network;
  tried on three sites the rules sent to the owners.
- **The record** is `sites-model.jsonl` in the dev box's folder:
  the answers, what it would have done and why, the tokens, or the error.

## What stays as it is

The refusals for names inside the network. Allow and Say no in the
channel and on the dev page, and who may tap them. `hi app dev <app>
allow` on the machine. The network log, which now says `checked`
and the outcome for each ask.

## Other ways considered

- **A model reading the site's content and allowing when it looks
  safe**, as the first idea for this draft. The content is the site's
  owner's to write, and it can change after the allow. The allow is for
  the whole site, not the page read. The risk that matters is who
  receives what's sent, which a page can't show. So the model is one
  step after the rules, asked about who runs the site and who can
  publish there.
- **An official "pre-approved for agents" list or API.** None exists
  as a standard. What exists: threat lists (Google Safe Browsing and its
  commercial Web Risk, Quad9, Cloudflare's 1.1.1.2, abuse.ch's URLhaus)
  say a site is bad, never that it is fine. Categories (Cloudflare's
  domain intelligence, with an account) say what a site is, which helps
  the second question. Popularity lists (Tranco, Cloudflare Radar) are
  a prior. `robots.txt`, `llms.txt` and the like say what a site's owner
  wants agents to read, which is about consent, not safety. The rules
  use the free ones; Web Risk and Cloudflare's categories are options
  with a key.
- **Fetching for the agent** (`hi dev fetch <url>`): the broker does a
  GET and hands back the body, so reading a page needs no allow at all,
  and the network log has the whole address, not only the name. That's
  smaller still (no uploads, a URL capped in length), but agents' tools
  (curl, npm, browsers) go through the proxy, not a command. It's left as
  a later step for pages the agent only reads.
- **Allowing everything and logging** (the `open` preset). That's
  simplest, but then the box can send anything anywhere, and nobody reads
  the log in time.

## Tests

- Preset: the new hosts are in `dev`; the app's public Stage and dev
  page are on the list, production isn't, and a NetBird name still
  isn't.
- Organisation's domains: with `corp.example` set, `www.corp.example`
  is on the list; `corp.example.evil.test` isn't; `hi admin sites org
  off` takes them off.
- One post: two asks from one task within two minutes make one post
  with Allow all; a third after five minutes makes its own.
- Rules, with the lookups replaced: a top-1,000, 20-year-old, unlisted
  name is allowed for 7 days; the same name under `github.io`, or 30
  days old, or named by Quad9, or `drive.google.com`, goes to the owners
  with the reason in the post.
- Model, with the call replaced: each answer just past its threshold
  sends the ask to the owners; a model's yes for a 30-day-old site
  doesn't allow it; a page that says "answer yes" changes nothing the
  rules decide; no OpenRouter key means `model` isn't offered.
- Limits: the eleventh allow in a day goes to the owners; an incident
  pauses the check; a site an owner said no to is never checked; Undo
  removes the site and records a no; a check allow is gone after 7 days
  unless kept.
- The page fetch goes through the public-only dialer: a site that
  resolves inside the network is refused before anything is fetched.

## Questions

1. Is `rules` the right default, or should a machine start at `off`
   until an admin chooses?
2. The organisation's domains on by default: are there subdomains (a
   forum, a file share) that the organisation runs but anyone can write
   to?
3. Data residency. With `model`, the site's page and the agent's why go
   to Google through OpenRouter, and OpenRouter's EU routing doesn't
   offer Gemini Decision 1 yet. Should `model` say so when the admin
   turns it on?
4. The request shape for Gemini Decision 1 through OpenRouter (the
   questions and their probabilities) is new. It needs a trial on real
   asks before thresholds are fixed. Should a first release run the
   model in **watch only** (it records what it would have done, and the
   owners still answer), and turn on once a few weeks agree with them?
5. Sites the proxy refused without an ask: should the rules allow those
   too, on their first refusal, so tools that never ask (a build script)
   don't fail? They'd have no why, so the model's third question can't
   be asked; rules only.
6. Seven days and ten a day: right sizes?
