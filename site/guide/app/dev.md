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
for every app (`hi admin sites`). The app's own production stays an ask,
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

The page is the work, Chat, the terminal, or VS Code, with a panel on
each side of it, as VS Code's side bars, their views by the icons along
their tops. The left has what goes into the app: **Tasks**,
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
- **Workspaces:** the pieces of work in the dev box, each with what dev
  has of it, **Open**, **Try on Stage**, **Ship to Dev**, and **Done ✓**
  ([below](#beside-the-chat)), and what Stage shows at the top.
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
  your own words, and hi presses the keys for you. It works for Codex's
  approvals and questions too, and **The terminal** under the card has
  the screen and its keys for anything else. A workspace whose agent stopped
  shows its last session, with **Resume this session**. The right
  panel is beside the chat, the terminal, and the open files; VS Code
  has its own. Beside the terminal it follows the workspace you open in
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
  and starts the agent on it. It ships, and is done, like any task. The chat is the
  default; the page remembers your choice. Codex's sessions
  are in the terminal.
- <a id="beside-the-chat"></a>**The panels' views** for the work:
  - **Rewind**, as above.
  - **Files** is the chat's workspace's files, a tree whose folders
    open in place: its worktree, or dev's clone for the project's own
    workspace; the list at the top picks another. A changed file is in
    git's colour (orange changed, green new, red deleted) with its lines
    added and removed and git's letter, and a folder with changes in it
    has a dot. A file opens in a tab of its own after **Chat**,
    **Terminal**, and **VS Code**, as many as you like, and the page
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
    hasn't in green. Once it has commits dev hasn't, **Try on Stage**
    and **Ship to Dev** are there too, as in the workspaces' list.
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
- **VS Code**, the third view on a wide screen, is VS Code in the
  browser ([code-server](https://github.com/coder/code-server)) on the
  workspace's worktree: the same files the agent works on, to edit by
  hand, search, read the git history, or open a terminal of your own.
  hi fetches it into the dev box the first time (about a minute) and
  starts it when the view opens; only owners and Pro Devs reach it, as
  with the terminal. It saves as you type, so the agent sees your changes. Type
  `/ide` in the chat and Claude links to it: its changes open as diffs
  in the editor, and it sees what you select. Extensions come from
  [Open VSX](https://open-vsx.org); **Open in a new tab** gives it the
  whole window. hi adds viewers for files VS Code shows only as text:
  PDFs open as pages, **Edit as csv** shows a CSV as a table (Rainbow
  CSV colours its columns), Mermaid diagrams draw in Markdown's preview,
  and notebooks show their saved outputs, Plotly and Vega among them. On a dev page that is plain http, browsers keep some of
  VS Code's parts from it: previews, these viewers, and extensions' own
  panels don't show, and copy and paste go through the keyboard only. Put the
  addresses [on https](#the-addresses-on-https) for all of it.
- **Try on Stage** and **Ship to Dev**, beside a workspace with commits
  dev hasn't got, do what `hi dev preview` and `hi dev ship` do in the
  box. Stage is one address, and it shows one thing at a time:
  dev, or a branch being tried. Trying a branch puts it there alone,
  with a fresh copy of production's data, and changes nothing in dev;
  **Show dev**, beside "Stage:" at the top of Workspaces and in
  Release, puts dev back (`hi dev preview --dev` in the box). Ship to
  Dev merges the branch's commits into dev (with a title for the
  release notes; the last commit's by default) and puts dev on
  Stage. Changes not committed stay in the worktree.

  ```
  Try on Stage     Stage shows only your branch   (dev doesn't change)
  Show dev         Stage shows dev again           (undoes the line above)
  Ship to Dev      your branch joins dev; Stage shows dev
  Release          dev becomes production
  ```
- **Done ✓**, beside a workspace in Workspaces (and beside its task),
  ends it once it's shipped: its worktree, its herdr workspace with the agent in it, and its
  branch go, and its task closes ("Shipped into dev as t-31"). Each
  workspace says how much dev has: "everything in dev", or commits not
  shipped and changes not committed, when the button is **Discard…**
  instead and asks first. `hi app dev <app> finish <branch>` does the
  same on the machine.
- **Addresses**, in Release's Where things run: each address's internal link and whether
  its NetBird peer has joined; **Make public** puts one on the internet
  too, with its link's key, and **Internal only** takes it off again. Both
  are an owner's click, said in the channel.
- **Release:** at the top, **Where things run**: each address and what
  it shows now. Production, in green, with the version everyone uses
  and, when dev has changes waiting, what they are and **Release**.
  Stage, in blue, with what it shows (dev, or ▸ a branch being tried,
  with **Show dev**), and under it the work dev hasn't got: the one on
  Stage marked "← on Stage now", the others with **Try on Stage**. And
  this page. Each has **Make public** or **Internal only**. Under it:
  - **Release** puts dev in production, with notes for the channel
    drafted from dev's changes. Owners and developers release; at the
    first release, only an owner may **Start production with a copy of
    Stage's data**. **Set dev back to production** drops
    what dev has on top of it (see [Dev and production](/guide/app/releases/)).
  - **Releases**, each with **Roll back**, of the code, or the code and
    its data.
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
some things from: VS Code's previews and extensions' panels, the
clipboard's buttons, and the app's own use of them. Give the machine a
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
  stays on the machine, in `~/.local/share/hi/domain.json`, readable by
  you alone. To move to another provider, run the command again with it.
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
| **Pro Devs** | An owner, on the dev page's People table, or `hi app access <app> <ID> pro` | Build as owners do: the dev box, its agents' Chat, the terminal, VS Code, files, SSH, Stage, shipping into dev, and releasing it to production. Rollbacks and production's data and settings stay the owners' |
| **Vibe Devs** | An owner, the same way (`vibe`) | Build and release with the agents in Chat, as Pro Devs do, without the terminal, VS Code, or SSH. The agents still run commands in the dev box for them, so it's a simpler view, not a wall |
| **Contributors** | An owner, on the dev page's People table, or `hi app access` | Report bugs as tasks: to the lead in Slack, or from the app's [helper](/guide/app/helper/) signed in with their own link. They never reach the dev page or the dev box |
| **Viewers** | Whoever can open the app | Use it, with the helper's Ask and Feedback |

Terminal, VS Code, an agent's chat, starting a task, uploading files, and
SSH are one level, not separate switches: they all run in the same dev
box, and VS Code has a terminal of its own. A developer also has the dev
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
and an open terminal or VS Code closes within seconds. Pro Devs paste
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
| `hi dev allow <site> --why <what for> [--wait]` | In the box: ask the owners for a site the proxy refused; alone, what's allowed and refused |
| `hi dev task [list\|show\|add\|comment\|edit\|close\|files]` | In the box: the app's tasks |
| `hi dev review [--with claude\|codex] [<notes>]` | In the box: a second opinion on the branch from the other agent |
| `hi dev deep-review --budget <usd>` | In the box: a multi-agent review of the project (also `list`, `show`, `stop`) |
| `hi dev brainstorm <question file\|->` | In the box: several agents' ideas (also `list`, `show`, `stop`) |
| `hi dev agy "<task>" [--file <path>]...` | In the box: Google's Antigravity makes images, media, or research on the machine, into `~/hi/agy/<id>/` (also `show`) |
| `hi dev data [fork]` | In the box: the app's data; a new fork for Stage |
| `hi dev settings [KEY=VALUE\|--unset KEY]` | In the box: the settings' names; Stage's own values |
| `hi dev incidents [<id>]` | In the box: incidents and what hi recorded |
| `hi dev upstream` | In the box, for an adopted project: where dev stands against the original, and its branches and tags into the box's clone |
| `hi dev propose <work> <description file\|->` | In the box, for an adopted project: offer shipped work to the original, as a draft pull request |
