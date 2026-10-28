---
name: hi-dev-incidents
description: Look into the hi team's incidents from its dev box - what hi recorded when an app job, the app, or a task failed. Use when the user asks why something broke, about errors, failures, outages, or an incident number, or when hi dev status shows open incidents. Covers hi dev incidents.
---

# Incidents

```bash
hi dev incidents          # the open ones, then the last month's
hi dev incidents i-3      # one, with what hi knew and its timeline
```

hi opens an incident when one of the app's jobs fails or stops reporting,
the app doesn't answer, or a task fails. Each has what hi knew when it
opened (the job's state, the app's last lines, the error) and a timeline
of what was done.

To look into one:

1. Read it, then the code it points to, on the branch production runs
   (`git log origin/<branch>`; hi dev status says which version).
2. Reproduce it on the preview if you can.
3. Fix it on a branch, with a test, and ship it the usual way; tell the
   user it needs a release to reach production.

The incident closes when hi sees the job or the app well again. Tell the
user what you found; post in the channel (hi dev say) only when they ask.
