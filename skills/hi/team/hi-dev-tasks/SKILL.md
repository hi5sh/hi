---
name: hi-dev-tasks
description: Read, file, comment on, edit, and close the hi team's tasks from its dev box (GitHub Issues, GitLab issues, or hi's own). Use when the user mentions tasks, tickets, issues, bugs reported by the team, ideas to work on, what to do next, or wants something written down for later.
---

# The team's tasks

Ideas, suggestions, and bug reports from the team's Slack channel become
tasks. They live where the team's repository is: GitHub Issues, GitLab
issues, or hi's own when it is on neither. `hi dev task` works the same
for all three; hi runs gh or glab on the machine, so you need no token.

```bash
hi dev task list              # open tasks
hi dev task list --closed     # all of them
hi dev task show 12           # one, with its comments
hi dev task add "Chart misses the last day" "What happens, and how to see it"
echo "Long text" | hi dev task add "Title" -
hi dev task comment 12 "Fixed on branch chart-last-day; on the preview now"
echo "The whole new text" | hi dev task edit 12 [--title "..."] -
hi dev task close 12 "Shipped in t-31"
hi dev task files 12          # its attachments, into ~/hi/tasks/12/
hi dev task comment 12 "Before and after" --attach before.png --attach after.png
```

A task's attachments (pictures, PDFs, whatever people shared with it)
are kept by hi on the team's machine, whichever forge has the task:
`hi dev task show` lists them, and `hi dev task files` fetches them.
Look at them before you start: a picture often says more than the
text. A reference such as `[image: image-3fa9c1.png]` in the text is one
of those files.

## How to use them

- **Starting work:** read the task, and name the branch after it, such
  as `12-chart-last-day`.
- **While working:** comment when there is something the reporter should
  know, such as a link to the preview.
- **When it ships:** close it with what shipped (`hi dev ship` prints the
  change's id, such as t-31). Don't close what isn't in dev.
- **Found something on the way** that isn't this work: file it instead of
  fixing it now, unless the user asks.
