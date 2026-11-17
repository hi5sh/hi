---
title: Build in the dev box
description: "Each hi app has a dev box: a long-lived box with the project, Claude Code, Codex, and herdr, where the app's owners build with their agents over SSH on the app's NetBird network."
---

The app's owners and developers build the app themselves, with coding
agents, in the app's **dev box**. The dev box is one long-lived [box](/guide/box/) per app:

- **The project:** a clone of the app's repository in `/box/repo`. The
  app's repository is its `origin`, read-only: the box fetches from it,
  and its commits stay in the box until they are taken into dev.
- **The agents:** Claude Code and Codex, on the app's sign-ins. As in any
  box, the sign-ins stay outside: the box's proxy puts them on the
  agents' calls. The agents don't ask before commands; the box is the
  boundary. Claude Code runs on Opus unless an owner picks another
  model with `/model` in the box, which stays its default.
- **The internet:** the box's proxy lets it reach what development needs
  (GitHub, npm, PyPI, Go modules, crates.io, Hugging Face, the agents'
  services), and nothing else until an owner allows it. See
  [Sites the box reaches](#sites-the-box-reaches).
- **[herdr](https://herdr.dev):** a terminal workspace manager for coding
  agents. Its server starts with the box and keeps the agents running
  when nobody is attached; `herdr worktree create --branch <name>` gives
  a piece of work its own branch, worktree, and workspace.

The app's service starts the dev box, and leaves it running when it
restarts, so agents keep working through an update of hi. `hi app down`
stops it. Its home, with the agents' conversations and herdr's sessions,
is in the app's folder, `dev/`, and in the app's snapshots.

## From the box to production

Work leaves the box through `hi dev`, which talks to the app's service.
The agents know it from their skills (below), so you can just ask them to
"put it on Stage" or "ship it".

```bash
hi dev preview              # the current branch on Stage, with production's data
hi dev ship -m "Show the last day on the chart"   # into dev, and dev on Stage
hi dev status               # what production runs, what dev has, Stage, backups, incidents
hi dev say "The chart has the last day now; try it on Stage"   # in the app's channel
hi dev task list          # the app's tasks
```

- **Preview** puts a branch on Stage, the app's address for trying
  work. When it is new work there, Stage first gets a fresh copy of
  production's data.
- **Ship** merges the branch into dev, records it as work, and puts dev
  on Stage. A branch that conflicts with dev says how to rebase it.
  A change that adds an install script to `trustedDependencies`, or a
  package with an exploited advisory, can't be shipped from the box.
- **Production** moves only when an owner or a developer releases dev
  from the dev page. The box can't release, roll back, restore, or change production's settings: those
  aren't on its connection to the app's service at all.

### Tasks

Ideas, suggestions, and bug reports become tasks where the app's
repository is: GitHub Issues on github.com, through `gh`; GitLab issues on
gitlab.com or a GitLab of your own, through `glab`; or hi's own in the
app's folder (`tickets/`) when it is on neither. hi runs `gh` and `glab`
on the machine with its sign-in, so the box holds no token.

```bash
hi dev task list [--closed]
hi dev task show 12
hi dev task add "Chart misses the last day" "What happens, and how to see it"
hi dev task comment 12 "On Stage now"
hi dev task edit 12 [--title "..."] -   # its whole new text, on stdin
hi dev task close 12 "Shipped in t-31"
hi dev task files 12            # its attachments, into ~/hi/tasks/12/
```

What people share with a request (a picture, a screenshot, a PDF) goes
with its task: when the lead files or comments from a thread in the
channel, hi attaches every file shared in that thread, once each; files
from elsewhere the lead attaches (`--attach <file>` with `add` or
`comment`). hi keeps them on the app's machine, in `tickets/files/<n>/`,
whichever forge has the task. `hi dev task show` lists the files, and
**Start** on the dev page tells the agent to fetch them.

On the dev page, **+** beside the search files one with a title,
what it's about, and files: paste or drop a picture into the text, or
pick files, and each is a pill where it went, **Image #1**, with a
preview along the box's bottom; ✕, Backspace, or Delete takes it out.
The task's text says `[image: image-1.png]` there, the name it has in
the task's files. A note on a task takes pictures the same way.
The task says which owner filed it. A task's title opens it: its
whole text with its links, its files (pictures shown, the rest to open or
download), its comments, and a note to add, with files of its own. The
task's ⋯ and each comment's copy it, quote it in a note, or edit it,
as on GitHub; a comment's also deletes it, with the files it attached,
and a file's ✕ removes it. An owner's delete can't be undone,
and is logged with their name.

On the machine, `hi app task <app> ...` does the same.

### Images, media, and research

`hi dev agy "<task>"` has Google's Antigravity make what the app needs
and the agent can't: images and game assets, speech, music, video, a PDF
read, a question researched. It runs on the machine's Google plan and
Gemini key, so the agent asks first; see
[Images, media, and research](/guide/app/agy/).

### Reviews and brainstorms

```bash
hi dev review                       # a second opinion on the branch from the other agent
hi dev deep-review --budget 20      # several agents review the whole project
hi dev brainstorm question.md       # several agents' ideas on a hard question
```

- **A second opinion:** the other agent than the one you work with (Codex
  reviews Claude's work, Claude reviews Codex's) reads what the branch adds
  to dev, in a copy of its own, may run the tests, and changes nothing. Its
  report says whether to ship, what to change, and what to try on
  Stage. It runs in the box, in a few minutes, on the app's sign-ins.
- **A deep review** is [hi agent review](/guide/agent/review/) of the
  project, up to the budget you give ($50 at most from the box); it often
  takes an hour. The report lands in the box, in `~/hi/reviews/<id>/`.
- **A brainstorm** is [hi agent brainstorm](/guide/agent/brainstorm/):
  each agent works alone on the question, up to $10 in all; the ideas land
  in `~/hi/brainstorms/<id>/`, and your agent writes them up as one
  proposal.

The agents ask you for the budget before they start one of these.

### Data, settings, and incidents

```bash
hi dev data                         # production's and Stage's data
hi dev data fork                    # a new fork of production's data for Stage
hi dev settings                     # the names of the app's settings
hi dev settings QUOTES_URL=https://sandbox.example.com   # Stage's own value
hi dev incidents                    # what failed, and what hi recorded
```

### What Stage and production are doing

The box reaches neither app's sign-in, and production moves only on
Release, but its agents can see what's running, read-only, through the
app's service:

```bash
hi dev jobs                         # production's jobs and workers: state, progress, last error
hi dev jobs stage
hi dev logs --since 1h --grep research   # production's own output, its settings masked
hi dev logs stage --lines 500
hi dev tools production             # the app's MCP tools that only read
hi dev tools production list_suppliers '{"country":"SE"}'
hi dev mcp stage                    # the same as an MCP server, over stdio
```

In the logs, each value of the app's settings (8 characters or more)
shows as its name, `‹API_TOKEN›`, and `--grep` searches what's left. On
production only the app's tools marked `readOnlyHint` answer, as for the
lead; on Stage, every tool, on Stage's own copy of the data. Each read of
production is in the box's network log. The app's output can hold its
people's data: an admin turns production's view off with
`hi app dev <app> view production off` (Stage stays).

The box sets only Stage's settings and never sees a value;
production's settings and data are the owners', on the dev page, or an
admin's, on the machine.

### The agents' skills

The box's agents get skills made for hi's apps, kept current by each new
hi: `hi-dev-ship` (Stage and ship), `hi-dev-tasks`, `hi-dev-say`,
`hi-dev-status`, `hi-dev-review`, `hi-dev-brainstorm`, `hi-dev-data`,
`hi-dev-settings`, `hi-dev-incidents`, `hi-dev-agents` (the app's MCP
server, for agents), `hi-dev-agy` (Antigravity), and herdr's own. For a project the
app [adopted](/guide/app/adopt/), `hi-dev-survey` makes it run and maps
it, and `hi-dev-upstream` brings in the original's new versions
(`hi dev upstream`) and offers work back (`hi dev propose`). Their instructions (`~/.claude/CLAUDE.md`
and `~/.codex/AGENTS.md` in the box) start with a block hi writes about the
box: where the project is, how work moves, and how the app works; what
you write below it stays.

### Sites the box reaches

Without an ask, the box reaches what development needs (GitHub, the
package registries, Playwright's browsers, uv, Debian's and Ubuntu's
packages, jsDelivr and unpkg, Hugging Face), the app's own Stage and dev
page when they're on the internet, the organisation's domains once an
admin sets them (`hi people domains`), and the sites an admin allowed
for every app (`hi server sites`). The app's own production stays an ask,
also under the organisation's domain: an agent calling production's API
can change its data.

When the proxy refuses a site an agent needs, the agent asks the app's
owners for it, saying what for, and waits for their answer:

```bash
hi dev allow query1.finance.yahoo.com --why "daily price history for the game" --wait
```

The ask shows as a card in the chat of the work that asked (in every
chat, for work without a workspace), with a count on **Security**, and in
the ops channel (the app's channel, without one) with **Allow** and
**Say no** buttons that answer for an owner who clicks them, and a link.
Asks from one piece of work a moment apart share one post, with **Allow
all** and a pair of buttons for each.
**Allow** lets the box reach the site and its subdomains at once, and on
its next start; **Say no** tells the agent, which carries on without it.
Whichever answers, the channel's post then says who did, and an agent
that stopped waiting hears the answer in its session, so nobody has to
tell it. **Security** lists the sites the owners
allowed (**Remove** takes one off) and the ones the box was refused in
the last week that nobody asked for, to allow or dismiss. Each site added
is also an incident in the ops channel, from the app's
[watcher](/guide/app/incidents/#the-apps-watcher).

#### hi's check

Before an ask reaches the owners, hi checks the site, and allows it by
itself when it vouches for it: the site's name is among the 100,000 most
used ([Tranco](https://tranco-list.eu/)'s list), was registered over a
year ago (its registry, over RDAP; a name whose registry doesn't say
needs to be among the 10,000 most used), no threat list names it (Quad9,
Cloudflare's 1.1.1.2), and nobody but its owner can make a name under it
or put files there: not a file share, a paste site, a tunnel, a request
catcher, a short link, a hosted database, or a name under `github.io`
and the like. The agent's reason and the site's pages never count: whoever
runs a site writes both.

A site hi allowed is the box's for 7 days, and the agent hears so at
once. The ops channel hears of them in one post a day, "hi allowed 2
sites for the dev box by itself today", with **Keep** (an owner's allow,
for good) and **Undo** (off, and a no) for each; Security on the dev page
has the same, with the days left. hi allows ten a day at most, and stands
aside while the app is frozen or paused, or an incident about the dev box
is open. A site a threat list names goes to the owners with a warning,
its **Allow** asks again, and the check stops until an owner turns it on.
Anything the check doesn't vouch for goes to the owners as above, the
post saying what hi found ("not allowed by itself, example.com was
registered 30 days ago"). A site an owner said no to is never checked.

```sh
hi app dev payments sites check          # how it stands, and what it allowed
hi app dev payments sites check off      # every ask to the owners
hi app dev payments sites check rules    # on again (the default), also after a pause
hi app dev payments sites check watch    # the rules, and a model watching
```

With `watch`, a decision model, [Gemini Decision
1](https://openrouter.ai/google/gemini-decision-1) on the app's
OpenRouter key, judges each ask the rules send to the owners for its rank
or age (never a file share, a claimable name, or a site a threat list
names). hi fetches the site's front page from the machine, through the
same check that keeps the box out of the network, and the model answers
five questions with a probability each: is it run by a known
organisation or project; can others publish there or catch what's sent;
does it fit the agent's reason and the task; does the page try to
instruct; is it a sign-in or payment page. The agent's reason and the
page are marked as written by others. hi records what the model would
have done, and `hi app dev <app> sites check` says how often it agreed
with the owners, and which sites it would have allowed that they said no
to. It allows nothing yet: that waits until it has agreed for a few
weeks. The page, the reason, and the task's title go to Google through
OpenRouter, about a tenth of a cent an ask.

The admin page has the same, in the app's Network.

A database on the internet that speaks TLS on MongoDB's port, 27017,
such as MongoDB Atlas, is allowed the same way, by the name in its
`mongodb+srv://` address. The box's DNS then gives that name, and the
shards its SRV record names, and the proxy joins the driver's connection
to the database with TLS end to end, so a certificate signs in as it
would anywhere. Give the dev box its own database user, not
production's: `hi app env <app> --dev` ([Keys and
settings](/guide/app/keys/)). A dev box started before hi v2.6.24 gets
its DNS when it starts again (`hi app dev <app> restart`).

A site inside the network is never allowed, from the page or by an
agent: names under `.local`, `.internal` and the like, and any name that
resolves to the machine, its LAN, NetBird's peers (100.64.0.0/10) or a
cloud's metadata address. The proxy checks the address it connects to as
well, so a name allowed once can't later be pointed inside.

On the machine, an admin allows sites as the page does, a database's name
too, and the box reaches them at once:

```sh
hi app dev payments allow cluster0.abcd.mongodb.net --why "the test database"
hi app dev payments allow                       # what's allowed, and asks waiting
hi app dev payments allow --remove cluster0.abcd.mongodb.net
```

The list is about accidents and casual leaks, not a wall: an agent can
send what it reads to any site it may reach, GitHub included. Allow a
site for what the work needs, and say no to one nobody can explain.

## The dev page

`/hi-<app> dev` in the app's Slack channel sends an owner or a developer
a private link to the app's **dev page**, in a DM; their links from
`/hi-<app>` have it too. They work for 14 days, and asking again ends the
old ones. A developer's page has no Data view and none of the owners'
buttons ([below](#owners-developers-and-contributors)).

The page installs as an app of its own, named for the app, with hi5's
icon: in Chrome or Edge, **Install** in the address bar or the menu (on
Android, **Add to home screen**, then **Install**); in Safari on an
iPhone or iPad, **Share**, then **Add to Home Screen**; in Safari on a
Mac, **File**, then **Add to Dock**. It opens in its own window, signed
in as the link left you. When the link's 14 days are over, open your new
link once and the app is signed in again. A browser installs only a page
on https, such as the dev page on the machine's domain or a public
address; a NetBird address on plain http stays a page in the browser.

The page is the work, Chat or the terminal, with a panel on each side
of it, as an editor's side bars, their views by the icons along their
tops. The left has what goes into the app: **Tasks**,
**Workspaces**, **Files**, **Search**, **Checks**, **Decisions**, and
**Security**. The right has what comes out of it: **Rewind**, **Source
control**, **Logs**, **Preview**, **Release**, **Data**, and
**System**. Each panel's inner edge drags it wider (a double click puts
it back), its ✕ hides it, and the buttons at either end of the bar
show and hide them; the page remembers each one's view, width, and
whether it's shown. On a narrow screen they lie over the work, one at a
time. An icon has a number for what's waiting: the open tasks, the
workspaces, the changes to release, and the sites the dev box asked
for; Checks and Logs have a dot.

- **Tasks:** the app's tasks: it opens on the task the chat's
  workspace was started for (its text, pictures, and comments, a note
  to add, and **Done ✓**), and **←** goes to the list, each task's
  number and title, with **Open** and **Closed** and a search by words,
  number, or who asked for it (the one who asked the lead in the
  channel, or filed it on the page, not the account the forge shows); a
  dot shows a task with a workspace, and its agent's state. **Start**
  asks which agent (Claude Code, Codex, or a shell; it remembers your
  last) and gives the task its branch (its number and a few words of its
  title), a worktree, and a herdr workspace, with the agent told to read
  it, and a note on the task saying who started it. Each workspace in
  **Code** says who started it too. `hi app dev <app> start <task>
  --agent claude` does the same on the machine. A task that has a workspace has **Open** instead;
  **+** files one.
  **Done ✓** closes a task once it's done: when its work is all in dev,
  it finishes the workspace too (worktree, workspace, branch); when the
  workspace still has work dev hasn't got, that stays there, and only
  the task closes. A closed task's ⋯ has **Reopen**.
- **The page's words** (since v2.6.192): work goes from its workspace
  to the **Preview**, the next version, then to production with
  **Publish**. The preview is what the commands and the rest of this
  guide call Stage, and the next version is dev (`hi dev preview`,
  `hi dev ship`, `hi dev logs stage`).
- **Workspaces:** the pieces of work in the dev box, each by its task's
  title (its branch on hover), with its task's number, its agent, where
  it is ("Not on the preview yet", "On the preview ✓", "Published ✓"),
  and its next step: **Add to Preview**, or **Done ✓** once it's there.
  **Open** shows its chat, **Editor ↗** opens it in [your own
  editor](#your-own-editor), and its **⋯** has **Preview only this
  change** (Pro Devs and owners) and **Discard…**
  ([below](#beside-the-chat)). **+ New** is at the top. Its
  dot is its agent's state: working, waiting for someone, idle, or done.
  Claude Code, Codex, and Antigravity tell hi through their hooks, which
  hi sets in the dev box, so the page also says what Claude runs ("Running Bash,
  for 3 min") or waits for ("Waits for permission for Edit", "a
  question"); the chat's top bar says the same.
- **Needs you** (the bell in the top bar): the workspaces whose agent
  waits for someone, with what for and how long, then those done since
  you last looked; a click opens its chat. For an owner, a site an agent
  asked the dev box to reach comes first, with **Allow** and **Say no**
  right there. **Notify me** there turns on
  the browser's notifications while the page is open. An agent that has
  waited ten minutes while nobody's active on the page sends one Slack
  DM, to whoever started its work, or else to the app's owners, with a
  link to its chat.
  An agent also says where its work stands in a line of its own
  ("tests pass; waiting for the API key", `hi dev note`), on its
  workspace and in Needs you.
- **Production and Stage** (the globe in the top bar): opens either in
  a new tab, with its address under its name.
- **Theme** (the half-filled circle in the top bar): Claude, the
  default, or Paseo, Zinc, Midnight, Ghostty, Pure black, Light, or Auto,
  which follows the computer's light or dark. This browser keeps it, for
  the dev pages and the admin page alike.
- **Antigravity:** with the app's own Google sign-in for agy
  ([keys](keys.md)), Start and **+ New** offer it beside Claude Code and
  Codex. Chat shows its session and its questions as Claude Code's, and
  the workspace menu has its conversations, to resume.
- **Codex in Chat:** Chat shows Codex's session as it shows Claude
  Code's.
- **The workspace menu** (the top bar's one drop-down): each workspace,
  with its status dot, its agent, and the title of the session its agent
  runs now, which it opens; under it its earlier sessions, every
  agent's, newest first (three, then **Show all**), then **+ New**. An
  earlier session resumes there, `claude --resume`,
  `codex resume`, or `agy --conversation` in its terminal; when an agent
  runs there, hi asks first, then stops it with Ctrl+C.
- **Notes on a diff** (Source control): the **+** beside a line of a
  file's diff adds a note on it, for the agent; **Send to the agent**
  puts all of them in the chat's box as one message, each with its file
  and line, for you to add to and send. They wait in this browser until
  you send or clear them.
- **Decisions:** the project's `docs/decisions/`, the technical choices
  nobody should undo by accident, as cards: each one's number, when it
  was written, its title, and its first paragraph, newest first, with a
  search. A card opens to all of it, with **Edit** (its Markdown) and
  **Delete**; **New decision** writes one, numbered after the last and
  named from its title. Each change is a commit to the app's branch,
  with who made it, as the specs are: no review, and the agents read it
  from there. A save over a change someone made since you opened it is
  refused, so open it again and make yours on theirs.
- **The work:** herdr, the dev box's workspaces and agents, in the
  browser, the same ones as over SSH.
  Closing the tab leaves the agents working. Ctrl+V pastes there as in
  a desktop terminal, a picture too: the page puts it in the box and
  pastes its path, which Claude Code shows as `[Image #1]`. **Chat** shows a
  workspace's Claude Code session as a conversation instead, as Claude
  Code's web view does: its answers as Markdown, each run of tool calls
  folded into a line ("Ran 3 commands, read 2 files") that opens to the
  commands and what they gave, and what it's doing now. What you write
  goes to the agent as if typed in the terminal, so the chat and the
  terminal are one session; **Stop** presses Esc. Beside the box, the
  model and the effort Claude answers with (Opus 5.5, Med) pick another
  from the next message, with Claude Code's own `/model` and `/effort`,
  which also make it the dev box's default for new sessions. They're
  there unless the app's admin keeps them as they are (`hi app models
  <name> off`, or in the admin page's Agents). Paste a picture
  (Ctrl+V), drop a file on the box, or pick one with the clip, and it
  goes with the message: a small preview over the box until you send,
  then a file in the box's `~/hi/chat/<workspace>/` that the agent is
  told about. A long paste (a log, a stack trace) folds into a line,
  `[pasted text #1 +40 lines]`, that opens when clicked and goes whole
  when you send. Until you send, the message, its pictures and pastes
  too, is kept in your browser, so a reload or a restart doesn't lose
  it. When the agent asks
  something in the terminal (a permission, a choice, a few questions in
  a row), the chat shows it as a card at the end of the conversation:
  tap an option, tick boxes, or open **Type something** and answer in
  your own words, and hi presses the keys for you. Claude Code's
  questions show as Claude wrote them, from its transcript: each
  option's whole label, its description, and its preview (the box
  Claude Code draws beside the options), in monospace, each colour's
  hex code in its colour. It works for Codex's
  approvals and questions too, and **The terminal** under the card has
  the screen and its keys for anything else. A workspace whose agent stopped
  shows its last session, with **Resume this session**. The right
  panel is beside the chat, the terminal, and the open files. Beside
  the terminal it follows the workspace you open in
  Workspaces ([below](#beside-the-chat)).
  **Rewind** is a Claude Code conversation's messages
  as a timeline, newest last: each with when, and what the code did
  after it (green when it changed: `sand.ts +20`). A message opens to go
  back to before it, as Claude Code's `/rewind` does: **Code and chat**,
  **Chat only**, or **Code only**. hi presses `/rewind`'s keys in the
  terminal for you, checking at each step that Claude Code shows what
  the chat does, and backs out if not; the message comes back in the
  chat's box, to change and send again. Restored files go back whole,
  and what shell commands changed stays, as with `/rewind`. Codex keeps
  no checkpoints of the code, so it has no timeline. The workspaces
  listed are pieces of work: a task's, or one with an agent at work
  (herdr's home and the project's own clone stay in the terminal). With
  none, or with **+ New**, the chat has **Start something new**: say
  what to do, pasting pictures in as you go (each an **Image #1** pill
  that ✕, Backspace, or Delete takes out), and hi files it in Tasks,
  pictures and all, gives it the task's branch and worktree from dev,
  and starts the agent on it, its chat open (Start on a task opens its
  chat too). It ships, and is done, like any task. The chat is the
  default; the page remembers your choice. hi trusts the worktree for
  the agent before it starts or resumes there, so Claude Code, Codex,
  and Antigravity never stop at "Trust this folder?" in the terminal.
- <a id="beside-the-chat"></a>**The panels' views** for the work:
  - **Rewind**, as above.
  - **Files** is the chat's workspace's files, a tree whose folders
    open in place: its worktree, or dev's clone for the project's own
    workspace; the list at the top picks another. A changed file is in
    git's colour (orange changed, green new, red deleted) with its lines
    added and removed and git's letter, and a folder with changes in it
    has a dot. A file opens in a tab of its own after **Chat** and
    **Terminal**, as many as you like, and the page
    keeps them open: text and code to edit, Markdown with **Preview**
    (the project's own pictures, and its links to other files), and
    pictures; anything else downloads. **Save**, or Ctrl+S, writes it,
    keeping its line endings, and the tree and **Source control** show
    the change. A tab you haven't changed follows the file as Claude
    edits it; one with changes not saved says so when the file changes
    under it, and a save never replaces a change it hasn't seen unless
    you choose **Save mine over it**. A file the chat links to opens in
    a tab too. **Upload**, or files dropped on the tree, go into the
    folder last opened; a file that's there already is replaced only
    when you say so. It shows nothing outside the project, nor writes
    in its `.git`.
  - **Source control** is the workspace's branch, how it stands against
    dev, and its changes not committed: **Staged** and **Changes**, each
    file with its lines added and removed and git's letter (M changed,
    U new, D deleted, R renamed). A file opens to its diff. **+**
    stages, **−** unstages, and **↶** discards: the file goes back as
    the last commit has it, and a new one is deleted, after you say so.
    **Commit** commits what's staged, or with nothing staged
    (**Commit all**) every change, as the dev box's author, as the
    agents commit. Under it, the branch's last commits, those dev
    hasn't in green. Once it has commits dev hasn't, **Preview only
    this** and **Add to Preview** are there too.
  - **Search** finds a word, or with **.\*** a regular expression, in
    the workspace's files, new ones too, not those git ignores; **Aa**
    matches case. The matches are by file, and one opens its file in a
    tab at its line. It shows the first 500.
  - **Checks** are the project's `make check`, `test`, `lint`,
    `typecheck`, and `fix`, or without a Makefile those scripts of its
    `package.json`, run in the dev box on the workspace, one at a time,
    for at most half an hour. Their output comes as they run, and
    **Stop** ends one. One that fails has **Fix with Claude**, which
    puts the end of its output in the chat's box for you to send; the
    icon has a red dot until one passes.
  - **Logs** are what production or Stage prints (**Output**, its
    errors in red) and the requests its address served (**Requests**,
    with their status: a 5xx in red, a 4xx in yellow), as they come;
    **!** shows the errors only. **↗** puts the lines you selected, or
    the last 40, in the chat's box. A release starts a new box, and its
    output from the start. Paths only: a link's key is never in them.
    Behind another view, new lines pulse a dot on its icon, red when
    one of them is an error; opening Logs clears it.
  - **Preview** is Stage or production, on its own address, in the
    panel: **↻** loads it again, **↗** opens it in a new tab, **⤢**
    makes the panel wider. It loads again when something new goes on
    Stage. An app that doesn't let other pages show it stays blank
    here; **↗** still opens it.
- **Add to Preview**, beside a piece of work not on the preview yet,
  adds it to the next version (dev, `hi dev ship` in the box), with its
  task's title for the version's notes, and puts the next version on the
  preview. Changes not committed stay in the worktree ("Unsaved changes:
  ask the agent to commit them"). Add several pieces of work and try
  them together on the preview, then **Publish** them at once.
  **Preview only this change** (its **⋯**) shows that one alone on the
  preview, with a fresh copy of production's data, and adds nothing
  (`hi dev preview`); **Show the next version** puts the rest back.

  When a piece of work changes the same lines as a change already on the
  preview, Add to Preview opens a dialog that stays until you choose:
  - **Ask the agent to combine them:** its agent brings in what's on
    the preview, makes both changes work together (asking you when they
    can't both be kept), and adds it again (`hi dev ship`). Its chat
    opens.
  - **Take out "…" and add this one instead**, when the change on the
    preview last is the one in the way: one step, which leaves the
    preview as it was if this one still doesn't go in. The change taken
    out stays in its workspace, "Taken out of the preview", and **Add
    again** puts it back.
  - **Ask a Pro Dev in Slack:** hi posts what went wrong in the app's
    channel, to its Pro Devs (its owners, when it has none), with the
    details and the branch.
  - **Cancel.** Under them, **Details, for a Pro Dev** has what git
    said. Other errors adding work open the same dialog, with Ask a Pro
    Dev.

  ```
  Add to Preview             it joins the next version; the preview shows that
  Preview only this change   the preview shows only it (nothing is added)
  Show the next version      the preview shows the next version again
  Publish v16                the next version goes to production
  ```
- **Done ✓**, beside a piece of work in Workspaces (and beside its
  task), ends it once it's on the preview: its worktree, its herdr
  workspace with the agent in it, and its branch go, and its task closes
  ("Shipped into dev as t-31"). With work not on the preview, or unsaved
  changes, it's **Discard…** in its **⋯** instead, which asks first.
  `hi app dev <app> finish <branch>` does the same on the machine.
- **Publish:** at the top, **From your work to production**, top to
  bottom: the work **Not on the preview yet**, each with **Add to
  Preview** (and with two or more, **Merge N changes into preview**,
  which adds each in turn and stops at one that clashes); the **Preview** with its address, what it shows (the next
  version, "v15 + 2 changes", and those changes; or "Shows only …" with
  **Show the next version**), and **Publish v16**; and **Production**,
  the version everyone uses. Under it:
  - **Publish v16** puts the next version in production, with notes for
    the channel drafted from its changes. It also offers to close the
    work that went out with it (their tasks and workspaces), checked by
    default; work an agent is still on, or with more to add, stays.
    Owners and developers publish; at the first, only an owner may
    **Start production with a copy of the preview's data**. **Clear
    the preview (back to v15)** drops what was added since (see [Dev and
    production](/guide/app/releases/)).
  - **Published versions**, each with **Roll back**, of the code, or the
    code and its data.
  - **Addresses** (owners): each address's internal link and whether
    its NetBird peer has joined; **Make public** puts one on the
    internet too, with its link's key, and **Internal only** takes it
    off again. Both are an owner's click, said in the channel.
- **Data:** production's data and Stage's side by side: how much
  each holds, when it was last written, and what Stage's is a copy
  of. Then what you can do with them:
  - **Copy production's data to Stage**, a fresh copy now; what
    Stage wrote itself is kept once, apart.
  - **Copy Stage's data to production**, with a snapshot of
    production's first.
  - **Take a snapshot now**, and the newest snapshots. Putting the app
    back as it was in one is an admin's, on the machine:
    `hi app restore <app> <snapshot>`.
  - **Start Stage empty at every release**, for an app whose
    people's data Stage shouldn't show.
- **System:** what production runs, what dev has, Stage, backups,
  incidents, and jobs, and what an owner decides:
  - **Incidents**, each open one with **Restart** and **Close** (see
    [When something goes wrong](/guide/app/incidents/)).
  - **The helper**, **On Stage** and **In production**.
  - **Rules for the app's jobs**, added and removed.

  Each click is the yes, logged with your name.
- **Security:** what reaches what, and the keys:
  - **Sites the dev box reaches**, beyond what development needs: the
    agents' asks, what the owners allowed, and what the box was refused
    lately (see [Sites the box reaches](#sites-the-box-reaches)).
  - **The app's settings**, its API keys among them, by name: set one for
    Stage and production, Stage only, production only, or the dev box
    only (from its next restart); nobody sees a value.
  - **The app's keys and sign-ins**: whether the OpenRouter key, the
    Claude and ChatGPT sign-ins, and the Slack tokens are the app's own
    or the machine's, and the command an admin runs to change each.

In the terminal, **Ctrl+Shift+F** (⌘F on a Mac), or the ⌕ at its
corner, finds a word in the scrollback: Enter for the next, Shift+Enter
for the one before, Esc to close. Links in it open in a tab, emoji and
Chinese, Japanese, or Korean characters take their right width, and a
picture shown with iTerm's protocol (`timg -p i`) shows in it.

On a phone or a tablet, a bar of keys
under the terminal has what a touch keyboard may lack: Paste, Esc, Tab,
Shift+Tab, Enter, the arrows, Space, Backspace, Del, Ctrl+C, Ctrl+D, and
a Ctrl that goes with the next key you type, such as Ctrl then R. It
stays above the keyboard while you type.

## Getting in over SSH

Owners and Pro Devs reach the dev box over SSH on your NetBird network,
at the app's peer, named `<app>`:

```bash
ssh box@orbit            # then: herdr
herdr --remote box@orbit # or from your own herdr
```

`hi app dev <app>` prints the name. An older app's peer may be named
`<app>-dev` (`ssh box@orbit-dev`). Several people may attach to the same
agent; they coordinate between themselves.

### Your own editor

Any editor that works over SSH works on the dev box. On the dev page,
**Editor ↗** beside a workspace (in Workspaces, and in the top bar for
the chat's) opens it there, on its worktree in the box. Before there's
any work, Workspaces lists the project's own clone (`/box/repo`) with
it, and the top bar opens that:

- **VS Code, Cursor, Zed:** a link; the browser asks your computer's
  editor to open it. VS Code needs Microsoft's **Remote - SSH**
  extension; the others have theirs built in. After the first, the
  button says your editor's name and opens it straight away; **▾** has
  the others.
- **[Orca](https://github.com/stablyai/orca):** the host, the user, and
  the project to type in, with Copy. Add the target, then the project
  `/box/repo` on it: each workspace on the page is one of its worktrees,
  which Orca hides until **Non-Orca worktrees, Show**.
- **Anything else over SSH** (a terminal, JetBrains Gateway, another
  editor): the `ssh` line for the workspace, and its host, port, user,
  and folder, each with Copy. The Security view has the plain
  `ssh box@<peer>` line with Copy too.

These four bring their server to the dev box from your computer when
the box can't download it, so they need no site added. Another editor
may need its own sites in the box (Security, Sites the dev box reaches).

**Set up once**, in the same dialog:

1. Your SSH key in the dev box ([below](#owners-and-their-keys); Pro Devs
   paste theirs on the dev page's Security view).
2. Your computer on the app's NetBird network, in a group that reaches
   the app's peer on port 22: your IT team's.
3. For an editor that lists the hosts in your SSH config (Orca, VS
   Code's Remote Explorer, JetBrains), and for a key ssh doesn't try by
   itself, this in `~/.ssh/config`, which the dialog has with your app's
   names:

   ```
   Host hi-orbit orbit.corp.example
     HostName orbit.corp.example
     User box
     # IdentityFile ~/.ssh/id_ed25519
   ```

The first time, your editor asks whether to trust the dev box; the
dialog shows its key's fingerprint (`SHA256:…`) to compare. Each editor
then puts its own server in the dev box, in a minute or so, and keeps it
there for the next time; the app's snapshots leave those out. The agents
keep working whether an editor is connected or not, and what you change
in the editor is in the workspace for them at once. A Vibe Dev has no
SSH, so no editor either.

### Joining NetBird

The app is on your NetBird network as one peer, such as
`orbit.corp.example`: production on port 80 (`http://orbit.corp.example/`), Stage
on 8080, the dev page on 8888, and SSH to the dev box on 22. An older
app's peer keeps its name, `orbit-dev`; rename it in NetBird's
dashboard if you like, and hi follows.
`hi app new` asks for a NetBird setup key at the end; for an app made
before, run `hi app dev <app> netbird`. In NetBird's dashboard:

1. **Add Peer → Server:** a one-off key, not ephemeral, with an
   auto-group such as `hi-orbit`.
2. **Access Control:** a policy from the people's group to that group,
   TCP ports 80 and 8080 (and 443 once the addresses are on https,
   below), and one from the owners' group, TCP 8888, 443 and 22.

hi uses the key once. After that the peer stays the same, with the same
name and address, also when it is replaced; hi doesn't keep the key, and
the peer's identity stays out of snapshots.

### A name for each address

Some IT teams keep names by hand and won't put several ports on one name.
Give each address a name of its own, which they keep:

```sh
hi app addresses payments names payments.example.com --https
# production payments.example.com, Stage stage.payments.example.com,
# the dev page dev.payments.example.com
hi app addresses payments names production=payments.example.com stage=staging.example.com dev=payments-dev.example.com
hi app addresses payments                  # the names, and what to ask the IT team for
```

They can point the names either way, and hi takes both at once:

- **A records** to the app's NetBird address, all on port 80: hi sends
  each name to its address. NetBird's policies then can't keep anyone off
  the dev page by its port; the dev page's own links do.
- **Their reverse proxy** (NetBird's reverse-proxy services, or their
  own), each name to the app's NetBird peer and its port: production
  80, Stage 8080, the dev page 8888. The dev page's port can stay closed
  to everyone but the owners' and developers' group.

`--https` is for TLS ended in front of hi, by their proxy: hi serves http,
and its links say https. Without it, the links say http.

**Stage** is the address where dev is tried before a release. `hi dev`
and some of hi's output still call it the preview, and `hi app`'s
commands take `stage` and `preview` both (`hi app data <app> stage
empty`, `--stage`).

### The addresses on https

On their NetBird names the addresses are plain http, which browsers keep
some things from: the clipboard's buttons, and the app's own use of
them. Give the machine a
domain of yours, and every app's production, Stage, and dev page move
to https, with a certificate browsers trust and nothing to install on
anyone's devices. An app stays off it when its setup said so, or after
`hi app addresses <app> netbird` (or a host name your IT team keeps):

```sh
hi net domain dev.example.com --dns gandi     # asks for the provider's token, then an email
hi net domain --email ops@example.com         # the email alone, later
```

The email is optional: Let's Encrypt writes to it if the certificate's
renewal keeps failing, well before it ends.

- **The names:** production is `https://<app>.dev.example.com/`,
  Stage `https://<app>-preview.dev.example.com/`, and the dev page
  `https://<app>-dev.dev.example.com/`. hi keeps their records in the
  domain's DNS, pointing at the app's NetBird address: anyone can look
  the names up, but only the network reaches them. The http addresses on
  the NetBird name keep working.
- **The certificate:** one for `*.dev.example.com` from Let's Encrypt,
  which hi gets by putting a TXT record in the zone (DNS-01) and renews
  before it ends. Nothing reaches the machine from outside, and the app
  names stay out of the certificates' public logs.
- **The DNS provider** is a setting: Gandi, Cloudflare, Amazon Route 53,
  Hetzner, DigitalOcean, or a DNS server of your own that takes RFC 2136
  updates. Give hi a token that may change only that zone's records; it
  stays on the machine, in the server's [vault](/guide/reference/vault/),
  encrypted. To move to another provider, run the command again with it.
- **NetBird:** the people's and the owners' Access Control policies need
  TCP 443 to the app's peer. The three names share that port, so the
  people can reach the dev page's name too; it still asks for an owner's
  link, as on its NetBird name. The peer starts again once when the
  addresses move, which ends SSH sessions to the dev box; the agents and
  the app keep running.

The owners get the dev page's new link in their DMs; `/hi-<app> dev`
gives it too, and the dev page's Addresses show production's and
Stage's. `hi net domain` says what's set, and `hi net domain --unset`
puts the addresses back on their NetBird names (the records hi made stay
in the zone).

### Owners, developers, and contributors

| Who | Set by | What they do |
|---|---|---|
| **Owners** | An admin of the machine (`hi app owners`) | Everything on the dev page and in the dev box, and production: releases, rollbacks, its data and settings, keys, sites, addresses, and who is a developer |
| **Pro Devs** | An owner, on the dev page's People table, or `hi app access <app> <ID> pro` | Build as owners do: the dev box, its agents' Chat, the terminal, files, SSH, Stage, shipping into dev, and releasing it to production. Rollbacks and production's data and settings stay the owners' |
| **Vibe Devs** | An owner, the same way (`vibe`) | Build and release with the agents in Chat, as Pro Devs do, without the terminal or SSH. The agents still run commands in the dev box for them, so it's a simpler view, not a wall |
| **Contributors** | An owner, on the dev page's People table, or `hi app access` | Report bugs as tasks: to the lead in Slack, or from the app's [helper](/guide/app/helper/) signed in with their own link. They never reach the dev page or the dev box |
| **Viewers** | Whoever can open the app | Use it, with the helper's Ask and Feedback |

Terminal, an agent's chat, starting a task, uploading files, and SSH
are one level, not separate switches: they all run in the same dev box. A developer also has the dev
box's broker role, so **a developer can read production's data** through
Stage, with their own work on it, since Stage runs on a copy of it,
unless Stage starts empty (`hi app data <app> stage empty`).
The page says so when an owner makes someone a developer, and the channel
hears every change ("Olga made Bo a Pro Dev").

Nobody has a level for being in the app's channel. The dev page's
Security view has a **People** table: everyone with a level and everyone
in the channel, owners first, with a radio per level and a search box.
Without a level, someone can use the app and ask the lead questions, but
the lead files no tasks for them, doesn't answer their DMs, and
`/hi-<app>` gives them no link.

```bash
hi app access orbit                            # who is which, and their SSH keys
hi app access orbit U083YSQFD0B pro            # a Pro Dev; also on the dev page: Security, People
hi app access orbit U083YSQFD0B vibe           # a Vibe Dev: Chat only
hi app access orbit U083YSQFD0B contributor    # no longer a developer: key and links end
hi app access orbit U07CLEO001 contributor     # may report bugs as tasks
hi app access orbit U07CLEO001 none            # no level: their links end
hi app access orbit key U083YSQFD0B "$(cat ~/.ssh/id_ed25519.pub)"
```

Taking away someone's level counts at once: their next click is refused,
and an open terminal closes within seconds. Pro Devs paste
their own SSH key on the dev page's Security view; NetBird's policy for the
dev box has to let them reach it, as for the owners. Vibe Devs have no SSH.

### Owners and their keys

An admin of the machine manages the owners, and each owner gives a public
SSH key once, on the dev page or here:

```bash
hi app owners orbit                             # the owners, and whose key the box has
hi app owners orbit add U083YSQFD0B
hi app owners orbit key U083YSQFD0B "$(cat ~/.ssh/id_ed25519.pub)"
hi app owners orbit key U083YSQFD0B --remove    # take a key out, such as a lost laptop's
hi app owners orbit remove U083YSQFD0B          # no longer an owner: key and links end
```

A new key replaces the old one at once. If the dev box's own host key
should change, `hi app dev orbit new-host-key` makes a new one; owners'
ssh then warns once that it changed.

## Commands

| Command | What it does |
|---|---|
| `hi app dev <app>` | Whether the dev box runs, its NetBird name, the owners' keys, and how to get in |
| `hi app dev <app> start <task> [--agent claude\|codex]` | A task's branch, worktree, and herdr workspace in the dev box |
| `hi app dev <app> finish <branch> [--discard]` | Once it's shipped: its worktree, workspace, and branch go, and its task closes |
| `hi app dev <app> restart` | A new dev box; herdr brings the agents' conversations back |
| `hi app dev <app> netbird [<key file>]` | Join the app's NetBird network with a setup key (asked for, or from a file) |
| `hi app dev <app> new-host-key` | A new SSH host key for the dev box |
| `hi app owners <app> [add\|remove <member ID>]` | The app's owners, who build in the dev box |
| `hi app owners <app> key <member ID> <public key>\|--remove` | An owner's SSH key for the dev box |
| `hi app task <app> [list\|show\|add\|comment\|close]` | The app's tasks, on the machine |
| `hi dev preview [<branch>\|--dev]` | In the box: a branch on Stage; `--dev` puts dev back on it |
| `hi dev ship [<branch>] [-m <title>]` | In the box: a branch into dev |
| `hi dev status` | In the box: what runs where |
| `hi dev say [--thread <ts>] <text\|->` | In the box: a message in the app's channel |
| `hi dev note "<a line>"` \| `--clear` | In the box: the agent's line on where its workspace's work stands, on the dev page and in Needs you, until the next |
| `hi dev allow <site> --why <what for> [--wait]` | In the box: ask the owners for a site the proxy refused; alone, what's allowed and refused |
| `hi dev task [list\|show\|add\|comment\|edit\|close\|files]` | In the box: the app's tasks |
| `hi dev review [--with claude\|codex] [<notes>]` | In the box: a second opinion on the branch from the other agent |
| `hi dev deep-review --budget <usd>` | In the box: a multi-agent review of the project (also `list`, `show`, `stop`) |
| `hi dev brainstorm <question file\|->` | In the box: several agents' ideas (also `list`, `show`, `stop`) |
| `hi dev agy "<task>" [--file <path>]...` | In the box: Google's Antigravity makes images, media, or research on the machine, into `~/hi/agy/<id>/` (also `show`) |
| `hi dev data [fork]` | In the box: the app's data; a new fork for Stage |
| `hi dev settings [KEY=VALUE\|--unset KEY]` | In the box: the settings' names; Stage's own values |
| `hi dev incidents [<id>]` | In the box: incidents and what hi recorded |
| `hi dev logs [stage\|production] [--lines N] [--since 30m] [--grep text]` | In the box: the app's own output, production by default, its settings' values masked |
| `hi dev jobs [stage\|production]` | In the box: the app's jobs and workers |
| `hi dev tools stage\|production [<tool> [<json>\|-]]` | In the box: the app's MCP tools (production: only those that read), or one tool's answer; `hi dev mcp` is the same over stdio |
| `hi dev upstream` | In the box, for an adopted project: where dev stands against the original, and its branches and tags into the box's clone |
| `hi dev propose <work> <description file\|->` | In the box, for an adopted project: offer shipped work to the original, as a draft pull request |
