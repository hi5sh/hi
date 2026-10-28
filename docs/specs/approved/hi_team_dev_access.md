# Owners, developers, contributors, and viewers

Status: Approved (2026-10-28). Built in v2.6.13, except people by email,
which waits for sign-in (below). Code: team_access.go (levels, `hi team
access`, People), team_devpage.go (the gate), team_report.go (Report).

Dependencies: [hi_team_dev.md](../approved/hi_team_dev.md) (the dev box,
the dev page, owners, `/hi-<team> dev`, the broker's `dev` role),
[hi_team_helper_signin.md](../approved/hi_team_helper_signin.md) (the
helper: Ask and Feedback for anyone, more for people signed in),
[hi_team_sso.md](../approved/hi_team_sso.md) (signing in with the
organisation's provider), [hi_team_internal.md](../approved/hi_team_internal.md)
(internal and public addresses).

## Goal

Today a team has owners and everyone else. Owners build: only they get
into the dev box and the dev page, and only they get a private link.
Everyone else has the app's ordinary link, whose helper answers questions
and sends feedback to the channel, and can talk to the lead in Slack.

Teams want people who build without owning production, and people who
report what's wrong in a way that lands as a task, with their name on
it, without anyone copying it over. So four levels:

| Level | Who | What |
|---|---|---|
| **Owner** | An admin of the machine (`hi team owners`), as now | The development environment, production, and who is a developer |
| **Developer** | An owner, from the dev page | The development environment; production stays the owners' |
| **Contributor** | An owner, on the dev page's People table; later, people by email too | Files bug reports as tasks, from Slack or the helper, signed in |
| **Viewer** | Anyone who can open the app; later, people an owner adds by email | Uses the app; the helper's Ask and Feedback, without signing in |

**Only owners and developers reach the development environment**: the
dev page, the dev box, and everything in them. Contributors and viewers
never see it.

## Developers

### Where the line is

The development environment is one level, not a set of switches. The dev
page's terminal, VS Code, an agent's chat, starting a task, uploading
files, and SSH all run as the same user in the same dev box, with the
agents' permissions skipped. VS Code has a terminal of its own, and an
agent runs whatever it is asked, so "VS Code but no terminal" would hide
a button and enforce nothing. A developer has all of it.

What a developer can't do is what the broker already refuses the dev box:
a release, a rollback, a restore, production's settings, and changing
owners. Those stay an owner's click.

**A developer can read production's data.** The box's broker role lets it
preview any branch, and the preview runs on a fork of production's data,
so a branch that prints it shows it, unless the preview starts empty
(`hi team data <team> preview empty`). The page says so when an owner
makes someone a developer.

### What a developer has, and what stays the owners'

| On the dev page | Developer | Owner |
|---|---|---|
| Tasks: everything, starting one, something new | ✓ | ✓ |
| Code: workspaces, chat, terminal, VS Code | ✓ | ✓ |
| Files: read, download, upload | ✓ | ✓ |
| SSH into the dev box | ✓ | ✓ |
| Release: where things run, versions, the preview on dev | ✓ | ✓ |
| Release, roll back, set dev back to production | | ✓ |
| Data: folders, snapshots, copies | | ✓ |
| System: open incidents and jobs | ✓ | ✓ |
| Act on incidents; the helper; job rules | | ✓ |
| Security: the app's settings by name; the sites the box reaches | ✓ | ✓ |
| Set a setting; keys and sign-ins; allow a site | | ✓ |
| Addresses: public or internal | | ✓ |
| People: who is a developer | | ✓ |

### Who is one

- **Owners decide**, on the dev page's **People** table, or on the
  machine with `hi team access <team> <@user> developer|contributor|none`.
  Developers and contributors are Slack members, by member ID, in
  `team.json`:

  ```json
  "developers": ["U07ANNA", "U07BO"],
  "contributors": ["U07CLEO"]
  ```

- **The People table** (since v2.6.15) has a row for everyone with a level
  and everyone in the team's channel, owners first, then developers,
  contributors, and the rest, each by name, with a search box. Each row
  has a radio for Owner, Developer, Contributor, and None. Owner is the
  machine admin's and can't be changed there; making someone a
  developer, taking a developer's level down, or taking anyone's level
  away asks first. People who aren't in the channel say so.

- **Owners can't make owners.** Owners release to production, so the
  machine's admin keeps that one (`hi team owners`, as now).
- **The channel hears it** when an owner changes it on the page: "Anna
  made Bo a developer." `hi team access` on the machine prints it instead.
- **Getting in** is as for owners: `/hi-<team> dev` DMs a private link,
  for 14 days. SSH takes developers' keys as well as owners'
  (`hi team access <team> key <@user> <key>`, or on the page). NetBird's
  policy, which the admin keeps, has to let developers reach the box; hi
  prints the step when the first developer is added.
- **Taking it away counts at once.** hi looks the level up at every
  request, so the next click of someone who is no longer a developer is
  refused, an open terminal or VS Code closes, and their links and SSH
  key end.

## Contributors

A contributor reports what's wrong, and it becomes a task with their
name on it. They never reach the development environment, and nothing
they write runs anything: a task is text until a developer starts it.

### From Slack

A contributor tells the lead, in the channel or a DM, and the lead files
it as a task (`hi team task add`), with who asked and a link to the
thread. **Being in the channel gives nobody a level** (since v2.6.15):
the lead files tasks only for contributors, developers, and owners
(`hi team web may --user <ID>` says whether someone is one), answers
anyone's questions, and DMs only from people with a level.

### From the helper, signed in

Signed in, the helper has a **Report** tab beside Ask, in place of
Feedback:

- A title, what happened, what they expected, and what they point at on
  the page (the helper's picker, as Feedback has now), with screenshots
  or files.
- **Send** files it as a task at once (without a label: `gh` refuses one
  the repository doesn't have), with what hi knows
  without asking: who sent it, the address and version (preview v12 +
  3, production v24), the page's path, and the browser. The task's
  number comes back: "Filed as #31."
- The channel hears it ("Anna reported #31: the export button…").
- The helper keeps no list of reports. Word that it's fixed comes from
  people: the developer who fixed it posts it, or the lead does when the
  task is closed, in the report's thread in the channel.
- A day's limit per person, as feedback has per address.

There are two ways to be signed in:

1. **Through Slack.** `/hi-<team>` in the channel DMs a private link to
   contributors, developers, and owners, and the helper says "you're Anna
   (via Slack)". The report says "from @Anna". Anyone else hears that an
   owner adds contributors on the dev page.
2. **Through the organisation's sign-in** (later, with SSO; below).

An owner takes a contributor's level away with None, which ends their
links (`hi team access <team> <@user> none`).

### Later, with sign-in: people who aren't in Slack

Once the app's address has sign-in (`hi team sso <team> on`), people who
have no Slack account can be contributors or viewers. An owner adds them
by email on the People section, or on the machine:

```text
hi team access <team> anna@example.com contributor
hi team access <team> bo@partner.example viewer
```

- **The relay says who signed in.** It hands the verified email and name
  to hi's routes in a header it strips from incoming requests
  (`X-Hi-SSO`), as it hands on `X-Hi-User`; the app's own pages never see
  it.
- **A contributor by email** gets Report in the helper, as one signed in
  through Slack does. The report says "from anna@example.com (signed
  in)". The helper is their only way to report: they have no Slack to
  tell the lead in. So it works where the helper is shown, and only
  there: on an address whose helper an owner turned off
  (`hi team helper <team> preview|production off`), they can't report.
  The People section says so when an owner adds a contributor by email
  and the helper is off on the addresses they can open, and `hi team
  access` prints the same.
- **A viewer by email** is someone the sign-in's rules wouldn't let in,
  let in by name: a partner's address outside the organisation's domain
  or groups (the SSO spec's "single people outside the rules").
- **Anyone else the rules let in** is a viewer: signing in doesn't make
  someone a contributor. An owner adds contributors.
- Emails sit in `team.json` beside the Slack IDs, with their level.

## Viewers

Anyone who can open the app. Who that is depends on the address, as now:
anyone on the organisation's network for an internal one, anyone with the
link for a public one, and, with sign-in on, anyone the provider's rules
let in and anyone an owner added by email. An owner sets it on the dev
page's Addresses.

A viewer's helper is Ask and Feedback. Feedback is anonymous and only
goes to the channel. Nothing files it: the lead doesn't make it a task
on its own, and people in the channel decide what to do with it. That
changes today's feedback message, which says the lead files what it
asks for.

## How hi enforces it

- **The dev page:** `devPageOwner` becomes `devPagePerson`, which returns
  the person and whether they are an owner or a developer, and refuses
  everyone else, as it refuses non-owners now.
- **One gate, owners' by default.** Every route but the page's own files
  goes through one gate in front of the mux: an owner or a developer, and
  for a developer only the routes named in `teamDevDeveloperRoutes`. A
  route added without being named there is the owners' alone. That
  includes the terminal's WebSocket, VS Code's proxy, the agents' chat,
  the files and uploads. An open terminal or VS Code asks the gate again
  every 15 seconds and closes when its person no longer passes, even when
  the change came from `hi team access` in another process.
- **The state is built for the level.** `api/state` leaves out what a
  developer doesn't get (keys and sign-ins, data) rather than sending it
  and hiding it in the browser, and the page hides the owners' buttons.
- **The broker is unchanged.** It already refuses the dev role what is
  the owners'.
- **The helper's Report** is a route on the helper's side (`/__hi/`),
  open to a contributor's key, and later to an `X-Hi-SSO` identity an
  owner added as a contributor, and nothing else. It files through the
  same code as `hi team task add`.

## Tests

- Every dev page route found in hi's source, at every level (owner,
  developer, a contributor's key, no key), against the real mux: 401, 403
  for a developer on an owners' route, and past the gate otherwise.
- A developer's release, rollback, data copy, setting, key, site, and
  People change each get 403 from the page, and the broker still refuses
  the box the same.
- A developer removed: their next request is refused, their terminal
  closes, their SSH key is gone from the box.
- A contributor's key (Slack or `X-Hi-SSO`) opens no dev page route.
- Report signed in through Slack files a task with the person, version,
  and page; with no key, Report is refused and Feedback works as now.
- Anonymous feedback reaches the channel, and the lead files nothing
  from it.
- Later, with sign-in: an `X-Hi-SSO` header sent by a browser is
  stripped by the relay; an email added as a contributor gets Report;
  anyone else signed in gets Ask and Feedback only. Adding a contributor by email
  while the helper is off on both addresses warns.

## Decided

- **No list of reports in the helper** (2026-10-28). Word that something
  is fixed comes from the developer, or the lead when the task closes.
- **Anonymous feedback stays in Slack** (2026-10-28), for people in the
  channel to handle; nothing files it as a task.
- **People outside Slack** (2026-10-28) are added by email, as
  contributors or viewers, once the address has sign-in.
- **Pro Dev and Vibe Dev** (2026-10-28, v2.6.20). A developer is a Pro Dev
  (Chat, terminal, VS Code, SSH) or a Vibe Dev (Chat only), as the user
  asked. Running code in the dev box is still one power: a Vibe Dev's
  agents run commands for them, so the line is a simpler view and no SSH,
  not a wall. The gate refuses a Vibe Dev the terminal, VS Code, and an
  SSH key (`teamDevShellRoutes`), and an open terminal closes when a Pro
  Dev becomes a Vibe Dev.
- **No level by default** (2026-10-28, v2.6.15). Being in the team's
  channel gave everyone Report and the lead's ear; now an owner ticks
  Contributor for each person, on a table of everyone.
- **Their reports go through the helper** (2026-10-28), where it is
  shown; with the helper off, a contributor without Slack can't report,
  and hi says so when they are added.
