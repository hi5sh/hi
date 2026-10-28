---
name: hi-dev-review
description: Get a review of work in this hi team dev box - a second opinion on the branch from the other coding agent (Claude reviews Codex's work, Codex reviews Claude's), or a deep multi-agent review of the whole project. Use before shipping, when the user asks for a review, a second opinion, a security or quality check, or wants to know what is wrong with the project. Covers hi dev review and hi dev deep-review.
---

# Reviews

## A second opinion on the branch

```bash
hi dev review                          # the other agent reviews what the branch adds to dev
hi dev review "the date handling in src/chart.ts"   # with what to look at
hi dev review --with claude            # name the reviewer (claude or codex)
```

The other agent than you (Codex when you are Claude, Claude when you are
Codex) reads the branch's commits in a copy of its own, may run the tests,
and changes nothing. It takes a few minutes. Commit first: it reviews
commits, not what is uncommitted.

Its report starts with **Approve:** or **Changes needed:**, then:

- **Verify on the preview:** what only the running app shows. Put the
  branch on the preview (hi dev preview) and tell the user what to try.
- **Later:** edge cases for later. Offer to file them as tasks; don't
  fix them now unless the user asks.
- **Alert:** a problem that isn't this work's, such as a test that fails on
  dev too. Tell the user.

Weigh the changes it asks for: you know the user's intent and it doesn't.
Make the ones you agree with, say which you didn't make and why, and
review again if the changes were large. The report is kept in
`~/hi/reviews/`.

Offer a second opinion before shipping work that is larger than a small
fix, or touches sign-in, payments, or people's data.

## A deep review of the whole project

```bash
hi dev deep-review --budget 20                 # the whole project, as dev has it
hi dev deep-review --budget 20 --area security,correctness
hi dev deep-review --budget 20 --since v3      # what changed since a release
echo "Only the three owners use it; it holds bank details." | hi dev deep-review --budget 20 --context -
hi dev deep-review list
hi dev deep-review show r-4                    # how far it is
hi dev deep-review stop r-4
```

Several agents review the project in their own boxes on the team's
machine, and confirm each other's findings. It costs real money, up to
the budget ($50 at most from the box), and often takes an hour. **Ask the
user for the budget before starting**, and pass what the team knows that
the code doesn't show (who uses the app, what protects it) with
`--context`.

When it ends, the report is in `~/hi/reviews/<id>/report.md` (and
`report.html`). Read it, then tell the user, in their words, which findings
matter for this app and why, and which don't because of how it is run.
They are recommendations to weigh, not requirements. Offer tasks for
the ones the user wants fixed.
