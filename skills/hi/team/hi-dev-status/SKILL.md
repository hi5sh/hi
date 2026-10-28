---
name: hi-dev-status
description: See the hi team's state from its dev box - what production runs, what dev has on top of it, what is on the preview, backups, open incidents, and the app's jobs. Use when the user asks what is deployed, released, on the preview or in production, whether something shipped, about backups, incidents, or whether the app's jobs run.
---

# The team's state

```bash
hi dev status
```

It prints:

- **Production:** its version and address. Owners release dev to
  production; the box can't.
- **Dev:** production's version plus the changes since, each with who or
  what built it.
- **Preview:** what is on it (dev, or a branch of the box) and its address.
- **Backups:** the last snapshot and its offsite copy.
- **Incidents:** what hi opened when a job, the app, or a task failed.
- **Jobs:** the app's long work, as the app reports it.

Use it before shipping (is dev what you think it is?), after shipping (is
your work in dev?), and when the user asks what runs where. For the code
itself, `git fetch origin` and `git log origin/<branch>` in `/box/repo`.
