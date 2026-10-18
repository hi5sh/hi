---
title: A deep review before a release
description: "A hi team's lead can offer a deep review of the whole project before a risky release: agents read it for security and correctness problems, another agent checks each finding, and only an owner's yes to a named budget starts it. The findings are recommendations the people weigh with the lead."
---

Every task a member does is reviewed on its own. Nobody reads the whole
project that way: how the parts fit, what an old change left behind,
what someone outside sees. A deep review does, with
[`hi agent review`](/guide/agent/review/), and the lead offers it when it
matters. It changes nothing, and it costs money, so the lead never starts
one on its own.

## The lead offers, an owner decides

The lead offers a deep review before a release whose changes touch
signing in, who may see or change what, payments, people's data, or how
the app is deployed; when people say they're worried about one of those;
or soon after the project came from somewhere else. It says what it will
cost at most, in dollars:

> We changed how people sign in and who can see a board. Want a deep
> review before we release? Two agents read the whole app for security
> and correctness problems, and a third checks what they find. It can
> cost up to $30, and changes nothing.

Only an owner's yes, in the channel, starts it. hi checks that the yes
answers a message of the lead's naming that figure, so the lead can't
ask about one amount and spend another. A no is a no: the lead remembers
it and doesn't ask again for that release.

hi says in the thread that the review started, on which version, and up
to what amount.

## What the reviewers are told

The reviewers read code; they don't know how the app is run. So hi tells
them what the team knows: what the project is for, whether production
has an address on the internet and whether a password page guards it,
who may use it, and the team's decisions in `docs/decisions/`. The lead
adds what the people have said ("only the three of us use it", "it holds
suppliers' bank details"). The coder's and the reviewer's agents
investigate in turns, and each finding is checked by the one that didn't
raise it.

## When it ends

- The owner who said yes gets the whole report in a DM: `report.md`, and
  `report.html` to open in a browser.
- The lead is woken in the thread. It posts how many findings were
  confirmed, by severity, each with a one-line title and nothing about how
  to exploit them, and the note that these are recommendations to weigh,
  not requirements.
- Then it says which findings it thinks matter for this team and why,
  which it thinks don't ("this one assumes the app is on the internet;
  ours is only on the office network"), and asks about what it can't
  settle. The people choose; the lead remembers what they set aside.

## Fixing what the people chose

A fix is ordinary work: a yes, a task, the usual review. The lead names
the finding (`--finding r-4:F-1`), and hi puts the finding's section
from the report in the brief. The next deep review is compared with the
last one, and says of each earlier finding whether it's still seen,
"not seen since t-14" when a merged task set out to fix it, or not seen
with its code unchanged, which is worth a look.

## For the lead, and on the machine

```text
hi team review --project --approval <id> --budget 30 [--area a,b] [--since v2] [--version v3] [--context notes.md]
hi team review list
hi team review show r-4
hi team review stop r-4
hi team task coder <brief.md> --approval <id> --finding r-4:F-1
```

The reports are in the team's folder, under `reviews/`, and read-only
in the lead's box. On the machine, `hi agent review show r-4 --md` shows
one too.
