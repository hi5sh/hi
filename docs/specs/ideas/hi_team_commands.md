# `/` commands in the helper's chat

Status: Draft (2026-10-19)

Dependencies: [hi_team_helper.md](../approved/hi_team_helper.md) (the
helper, its chat with the lead, approvals from the app),
[hi_team.md](../approved/hi_team.md) (the lead, its skills, who may ask
and who must approve), and the chat history and `/clear` of v0.95.0.

## Goal

The helper's chat takes plain words, and that should stay its main way in.
But some of what people do there is the same every time: start over, look
at the versions, have the app checked for accessibility, ask for a review
before a release. Today they have to know it is possible and phrase it so
the lead understands. Many never find out.

Typing `/` in the chat's box should show what can be done here, filter as
they type, and say what each one does before they pick it. Like a menu,
but from the keyboard, and it grows with the team.

So:

- **One list, three kinds.** Actions on the panel itself, answers hi
  gives without a model, and skills the lead runs. The person doesn't need
  to know which is which; the list says what each does and whether it
  needs a yes.
- **Skills are files.** hi ships some; a team adds its own in the project,
  reviewed like code. The lead runs one the same way whether it was typed
  as `/accessibility` or asked for in words.
- **Nothing gets around approval.** A command is a faster way to ask, not
  a new right. Work still waits for a yes, and what needs an owner still
  needs one in Slack.

## What people see

`/` at the start of an empty box opens a list above it:

```text
┌ Commands ─────────────────────┐ ┌ accessibility ──────────────────┐
│ In this chat                  │ │ A skill from hi                 │
│   /clear      New chat        │ │                                 │
│   /history    Past chats      │ │ Checks the page you're on, or   │
│ From hi                       │ │ the one you name, for what      │
│   /status     Versions        │ │ keeps people out: contrast,     │
│   /jobs       The app's work  │ │ labels, keyboard, screen        │
│ Skills                        │ │ readers. The lead reports and   │
│ ▸ /accessibility              │ │ proposes fixes; nothing changes │
│   /review                     │ │ without your yes.               │
│   /month-end   (this team's)  │ │                                 │
└───────────────────────────────┘ └─────────────────────────────────┘
 /acc│
```

- Typing filters by name and words in the description; ↑ ↓ move, Enter or
  Tab picks, Esc closes and leaves what was typed.
- The card beside the list (above it on a phone) has the name, where it
  comes from (hi, this team, the lead), what it does, what it takes
  ("the page you're on, or a page you name"), and whether it needs a yes.
- Picking a panel action or one from hi runs it at once. Picking a skill
  puts a chip in the box, `/accessibility`, and the person adds words
  ("the checkout page") or sends it as it is.
- A command typed in full without the list (`/clear` Enter) works the
  same. An unknown one is sent as words, with a note in the panel that no
  such command exists, so a message that happens to start with `/` still
  goes through.
- In the Ask tab, the list has the panel actions and hi's answers only.
  Feedback has none: there `/` is just text.

## The three kinds

### In this chat (the panel, no request)

| Command | Does |
|---|---|
| `/clear`, `/new` | A new chat; the last one is in the history (built, v0.95.0) |
| `/history` | The past chats (built as a button, v0.95.0) |
| `/docs` | The app's docs, in a new browser tab |
| `/point` | Select mode, as ◎ and Alt+S |
| `/feedback` | The Feedback tab, with what was typed after it |

### From hi (an answer without a model)

These read what hi already knows and answer in the chat as hi, in a
message marked as hi's, not the lead's. Nothing goes to the lead, and they
cost nothing.

| Command | Answers |
|---|---|
| `/status` | Production's version, the preview's, and what is in dev but not released |
| `/preview` | The preview's link for this person, as `/hi` gives it |
| `/tasks` | The team's tasks that are running or waiting, with who asked |
| `/jobs` | The app's long work, as `hi team jobs` |
| `/incidents` | Open incidents, as `hi team incidents` |

They show only what the person could already see from Slack: no keys, no
other people's DMs.

### Skills (run by the lead)

A skill is a `SKILL.md` with a name, a description, and instructions, the
form the lead's skills already have. Three sources, in this order in the
list:

1. **This team's**, in the project at `team/skills/<name>/SKILL.md`.
   They are reviewed and merged like code, so a skill's text is what the
   team agreed on. The month-end close, the weekly report, "add a
   supplier the way we do it".
2. **hi's**, shipped with hi and updated with it: `accessibility`,
   `review` (a deep review, which still needs an owner's yes for its
   budget), `explain` (how a page works, from the code), `bug` (a report
   with steps, what was expected, and a picture), `plan` (a plan and
   acceptance list before any work).
3. **The lead's own**, the skills Hermes writes as it learns, only those
   it marks `user-invocable: true` in their front matter. Off by default:
   a skill the lead wrote for itself shouldn't show up as a button.

Front matter:

```yaml
---
name: month-end
description: Closes the month: checks the open invoices, books the
  accruals, and makes the report for the board.
takes: a month, such as 2026-09; this month by default
where: production    # preview, production, or both (default)
user-invocable: true # for the lead's own skills only
---
```

A team's skill shadows hi's of the same name; the card says so.

## What the lead gets

A skill picked in the chat reaches the lead as a message with a header
line, like the page and the version:

```text
[web chat w-14 · message w-14.3 · Ana (U07ANA, signed in through Slack) ·
production v5 · /orders · command /accessibility (hi's skill)]

the checkout page
```

The lead's `hi-team` skill says: a message with `command /<name>` asks
for that skill; load it and follow it, with the words after it as what it
takes. The skill's text isn't pasted into the message: the lead loads it
like any other, so it stays in one place and the conversation stays short.

hi writes the team's and hi's skills into the lead's skills folder,
prefixed (`team-month-end`, `hi-accessibility`), as it writes `hi-team`
now, and again when they change on the project's main branch or with a new
hi. The lead can't change them there; it proposes a change to a team's
skill as work, like any other change to the project.

## Approvals and limits

- A skill that ends in work (code, data, a release) asks for a yes like
  any request, and the yes counts by the same rules. A command never
  approves anything by itself.
- What needs an owner (a release, a rollback, turning production's helper
  off, a deep review's budget) still needs an owner's yes in Slack. There
  is no `/release` that releases; `/release` could at most have the lead
  ask an owner in Slack, with what's in dev.
- `where` limits a skill to production or the preview; a skill not for
  this address isn't in the list, and typed anyway, hi says where it is.
- Only people who may ask for work (`config.Askers`, or anyone signed in
  when that list is empty) get skills; anyone may use the panel actions.
- Every command picked is kept with the message (`"command": {"name":
  "accessibility", "from": "hi"}`), copied into the person's DM with the
  rest of the chat, and shown in `hi team web`.

## API

- `GET /__hi/api/commands` → the list for this person and address:
  `[{name, kind: "panel" | "hi" | "skill", from: "hi" | "team" | "lead",
  title, description, takes, approval: bool}]`. hi builds it from the
  skills folders and its own table, and the helper keeps it for the
  session, again after a reload.
- `POST /__hi/api/messages` takes `command: "accessibility"`, checked
  against the list on the server: the browser naming a skill it may not
  use gets a 403, not a message to the lead.
- `POST /__hi/api/commands/<name>` runs one of hi's and returns its
  answer as a chat message (`from: "hi"`), saved in the conversation.

## Slack

The same skills in Slack are later work. Slack registers each slash
command in the app's manifest, so `/accessibility` there would mean a
manifest change and a reinstall for every new team skill. The likely way
is one command, `/hi <skill> ...` (the team's `/hi` already exists), with
Slack's own autocomplete for its first word. Not in this spec's slices.

## Not in scope

- Commands that run shell or code in the browser's name. The lead runs
  skills; the browser only names one.
- Arguments with types and forms (a date picker for `takes`). Words after
  the command, read by the lead, are enough to start.
- A store of skills from other teams. A team copies a skill into its
  project when it wants one.

## Slices

1. The list in the panel with the panel actions only (`/clear`,
   `/history`, `/docs`, `/point`, `/feedback`), filtering, the card, keys,
   and the phone layout. No server changes.
2. hi's answers: `/status`, `/preview`, `/tasks`, `/jobs`, `/incidents`,
   `GET /__hi/api/commands`, `POST /__hi/api/commands/<name>`, and
   messages `from: "hi"` in the panel and the DM copy.
3. Skills: team skills from `team/skills/`, hi's first five, the lead's
   opt-in ones, the header line, the `hi-team` skill's rule, the server's
   check, and the command kept with the message.
4. The template's coder role learns to write a team skill when the people
   ask for "a button for this", and the guide gets a page on writing one.

## Tests

- Go: the list per person and address (askers, `where`, a team skill
  shadowing hi's, the lead's opt-in); a message naming a skill it may not
  use is refused; the header line; hi's answers show nothing a person
  couldn't see in Slack; skills are written into the lead's folder and
  rewritten when they change on main.
- Helper (`bun test`, and in real Chromium for keys, as for every change
  to the composer): `/` opens the list, typing filters, ↑ ↓ Enter Tab Esc,
  the chip, a full command typed without the list, an unknown command sent
  as words, nothing in the Feedback tab.

## Open questions

- Should `/` open the list anywhere in the box, or only at the start? Only
  at the start keeps paths like `/orders/12` in a message from opening it.
- Should a team's skill be able to say who may use it (owners only), or is
  "may ask for work" enough for a first version?
- Does `/review` belong in hi's set, or is a deep review rare and costly
  enough that it should stay something people ask for in words?
