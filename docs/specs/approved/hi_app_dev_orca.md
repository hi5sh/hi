# What the dev page takes from Orca

Status: Approved (2026-11-16): the plan; release 1, built in v2.6.168
and narrowed in v2.6.169 (questions 1 and 2 answered); release 2, built
in v2.6.170; release 3, built in v2.6.172; releases 5 and 7, built in v2.6.173. Each
later release waits for its questions below.

Dependencies: [hi_app_dev.md](hi_app_dev.md) (the dev box, its page,
herdr), [hi_app_dev_access.md](hi_app_dev_access.md) (who has SSH:
owners and Pro Devs), [hi_app_snapshots.md](hi_app_snapshots.md),
[hi_app_dev_sites.md](hi_app_dev_sites.md) (what the box reaches).

## Why

[Orca](https://github.com/stablyai/orca) (MIT) runs Claude Code, Codex,
and about forty other agent CLIs side by side, each in a git worktree,
with a terminal, an editor, diffs, and notifications around them.
hi_app_dev.md set it aside as desktop only. It has a browser client
since (`orca serve`), so it was looked at again (2026-11-16), as a pane
of the dev page or as the whole of it. It doesn't fit either way:

- **One person's app.** Whoever pairs with an Orca server controls all
  of it: no people, levels, or roles. A Vibe Dev's Chat without the
  terminal, an open terminal that closes when someone's level changes,
  who's here: none of it can hold inside it.
- **Its own security.** No TLS, and a pairing link that works as a
  password; its docs say not to put it on the internet.
- **Its own machinery.** Its own terminal daemon instead of herdr, its
  own worktrees, which the page's Tasks, Ship to Dev, and Done ✓ don't
  know, and hooks it writes into `~/.claude/settings.json`.
- **All or nothing.** Its browser client is the whole app, about ten
  thousand files on some 890 calls to its server, with no part
  published on its own, released almost daily, client and server at
  the same version.

So Orca stays a tool someone may use on their own computer (release 1),
and the dev page takes what it does well, in hi's own code, a release at
a time.

v2.6.144 took VS Code (code-server) off the page so that people bring
their own editor over SSH instead. Release 1 makes that a click.

## What Orca does, and what the page does about it

| Orca | The dev page today | Here |
|---|---|---|
| Open a worktree in VS Code, Cursor, or Zed over SSH | SSH, with `hi app dev <app>`'s one line | Release 1: a link per workspace, for any editor |
| An agent's state from its hooks (Claude Code's, Codex's), a status sequence in the terminal, and the terminal's title | herdr's state, and the screen read for questions | Release 2 |
| Notifications and an inbox: an agent waiting for you, or done | A dot on a workspace | Release 3 |
| xterm.js with WebGL, search, Unicode 11, links, images | xterm.js 5.5, fit only | Release 4 |
| Notes on a diff's lines, sent to the agent | Source control's diff, read only | Release 5 |
| Every agent's past sessions, resumed in a click | Claude Code's sessions in Chat; Codex's in the terminal | Release 6 |
| An agent sets its worktree's comment and status from the CLI | The workspace's terminal title | Release 7 |
| Forty agents to start | Claude Code, Codex, a shell | A question below |
| The same task to several agents, to compare | `hi dev review`, `brainstorm`, `deep-review`; `hi agent best-of` on a machine | Not now |
| Usage and account switching | The app's model spend (v2.6.163); the sign-ins are the app's | Not needed |
| Ports forwarded to the laptop | Stage and Preview; editors forward their own | Not needed |
| A phone app, with push | The installable page with a key bar | Release 3's push, later |

## Release 1: your own editor (v2.6.168)

An owner or a Pro Dev opens a workspace in the editor on their own
computer, which works on the dev box over SSH. The agents keep working
in herdr as before; what the person changes in the editor is in the
worktree for the agents at once, and the other way round.

- **Where:** beside each workspace in Workspaces, and in the top bar for
  the chat's workspace. Before someone picks an editor it says **Editor
  ↗**; after, the editor's name (**Cursor ↗**), which opens it straight
  away, with **▾** for the others. The page remembers the pick in the
  browser. A Vibe Dev has no SSH, so none of it.
- **The editors hi supports** (v2.6.169), each of which brings its
  server from the computer when the box can't download it (question 1):
  - VS Code, by a link (the browser asks the computer's editor to open
    it): `vscode://vscode-remote/ssh-remote+box@<peer>/<folder>`, with
    Microsoft's Remote - SSH extension.
  - Cursor: the same with `cursor://`; its Remote - SSH is built in.
  - Zed: `zed://ssh/box@<peer>/<folder>`.
  - Orca, by what to type, each with Copy: Add Target from the SSH
    config, then the project `/box/repo`, whose worktrees are the
    workspaces; Orca hides worktrees it didn't make until **Non-Orca
    worktrees, Show**; it has no link for an SSH folder.
- **Anything else over SSH**, in the same dialog for a terminal,
  JetBrains Gateway, or any other editor: the workspace's line (`ssh -t
  box@<peer> 'cd <folder> && exec bash -l'`), then the host, port 22,
  user, and folder, each with Copy. The Security view's **Your SSH key**
  has `ssh box@<peer>` with Copy. v2.6.168 also had Devin Desktop
  (`devin://`), JetBrains, and a terminal in the list; they left it in
  v2.6.169.
- **The folder** is the workspace's worktree, or `/box/repo` for the
  project's own clone: the same paths in the box as over SSH.
- **The peer** is the app's NetBird name as NetBird gave it
  (`dev/netbird-fqdn`), not the short name, which needs NetBird's DNS
  search domain. Before the peer joined, the page says so, and what an
  admin runs: `hi app dev <app> netbird`.
- **Set up once**, in the dialog: the person's key (or **Add my key**,
  to Security's box for it); NetBird's group that reaches the peer on
  port 22, which is the IT team's; and an `~/.ssh/config` block, `Host
  hi-<app> <peer>`, so editors that list the config's hosts show it and
  a key named there counts for the links too.
- **The host key:** the page shows the box's host key fingerprint
  (`SHA256:…`, from `dev/ssh/host_ed25519.pub`), for the question every
  editor asks on its first connect.
- **Snapshots** leave out what editors install in the box's home
  (`.vscode-server`, `.cursor-server`, `.zed_server`, Orca's
  `.orca-remote`, and those of editors reached by plain SSH, such as
  `.devin-server` and `.cache/JetBrains`), a few hundred megabytes each
  and again per version, which come back on the next connect. Only in
  the box's home: a project's folder of the same name stays.
- **What the editors download** in the box: each puts its server and its
  extensions there on its first connect, from the computer when the box
  can't reach their sites (question 1).

## Release 2: the agents' state from their hooks

Today the page has herdr's state for each agent (working, idle,
blocked, done) and reads the screen for a question. Claude Code says
more, and says it exactly, through hooks: a session started, a prompt
sent, a tool about to run and done, a permission asked, a notification,
stopped, a subagent done, compacted. Codex has its `notify` program.

Built in v2.6.170:

- **hi's own hooks** (question 2), beside the owners', herdr's (its
  SessionStart hook, which tells herdr the session), and Orca's, if
  someone's Orca wrote some. On each start of the team's service, hi
  puts `/box/bin/hi dev __hook claude` in the dev box's
  `~/.claude/settings.json` for SessionStart, SessionEnd,
  UserPromptSubmit, PreToolUse, PostToolUse, PostToolUseFailure,
  PermissionRequest, Notification, PreCompact, Stop, and StopFailure,
  once each, with a 5-second timeout. Codex's config, when hi writes it,
  gets `notify = ["/box/bin/hi", "dev", "__hook", "codex"]`, which says
  when a turn is done.
- **hi dev __hook** reads the event (Claude Code's on its input,
  Codex's as its last argument), takes the workspace and pane from
  herdr's environment (`HERDR_WORKSPACE_ID`, `HERDR_PANE_ID`), and
  hands it to the broker's socket (`POST /hook`) within a second. It
  prints nothing and exits 0 whatever happens. Outside herdr (someone's
  own session over SSH, or in their editor) it does nothing.
- **The broker** keeps each workspace's state, in memory: working,
  blocked, or idle; since when; the tool running; and what it waits for.
  - A prompt starts a turn: working. A tool about to run: working, with
    its name. A tool done: working.
  - AskUserQuestion: blocked, on a question. ExitPlanMode: blocked, on a
    plan to approve. A permission asked, or its notification: blocked,
    on permission for the tool. An MCP tool's question: blocked.
  - Stop, or a failed stop: idle. A session's start: idle, except after
    a compaction, which carries on the turn. A session's end: forgotten.
  - A subagent's events keep the main agent working, with the main
    agent's tool.
- **With herdr's:** the hooks' state counts while herdr has that agent
  in the workspace, in that session. herdr's wins where the two differ
  and the hooks have been quiet 15 seconds: a turn stopped with Esc
  fires no Stop. herdr's done (finished, and nobody looked) stays done.
  After a dev box restart herdr numbers its workspaces again; the
  session tells an old state from a new one.
- **The page:** the dots in Workspaces and on Tasks come from it; a
  workspace says what its agent runs or waits for, and for how long once
  it's a minute ("Running Bash, for 3 min", "Waits for permission for
  Edit"). In the chat, the top bar says what it waits for, and the
  working line names the tool when the terminal's line can't be read.
  The screen stays where a question's options come from.

Not done: other agents' state from the status sequence some agent CLIs
print (Orca's OSC 7501): herdr 0.9 doesn't pass it on. The title stays
for them.

## Release 3: needs you

Built in v2.6.172:

- **An inbox** in the top bar, a bell with a count (amber while
  something waits): the workspaces whose agent waits for someone (a
  question, a permission, a plan to approve, as release 2 says), first,
  with what for and how long; then those done since you last looked,
  with who started each. A click opens its chat; Escape or a click
  elsewhere closes it.
  - **Done** is herdr's done, or the hooks' stop after the time you last
    had that workspace's chat in front of you. What you last looked at
    is this browser's (`hi-dev-seen`); a workspace it never saw starts
    as seen, so its old turns aren't news.
- **Notifications** while the page or the installed app is open, through
  the browser's own once **Notify me** in the inbox was allowed: "2-news
  asks a question", "2-news waits for permission for Edit", "1-chart is
  done", each once, and none for the chat you're looking at. A click
  opens its chat. Behind another window the page asks for its state
  every 30 seconds (every 5 in front). The installed app's badge is the
  count waiting.
- **When nobody's here:** every minute the app's service looks at the
  dev box's workspaces. An agent that has waited ten minutes, with
  nobody active on the page (who's here, v2.6.167; a window idle for
  minutes doesn't count), gets one DM (question 3): to whoever started
  its work, from the dev page or the channel (`run/dev-start-users.json`,
  their Slack member ID), else to each owner. It names the work, what it
  waits for, and how long, with the page's link opening its chat
  (`#work-<branch>`, without the address's key: the person's browser has
  it). One per wait: a new wait gets one again.
- Not yet: an agent waiting for a site (`hi dev allow --wait`) is a
  running Bash to the hooks; Security has its badge. Push to a phone
  with the page closed needs a push service; later.

## Release 4: the terminal

xterm.js's add-ons, embedded as xterm.js is (`third_party/xterm`): WebGL
drawing, which keeps up with Claude Code's full-screen redraws; search
in the scrollback (Ctrl+F in the terminal); Unicode 11 widths, for
emoji and CJK; links that open; and pictures in the terminal (sixel and
iTerm's), for `timg`. Then xterm.js 6 once it's out of beta.

## Release 5: notes on a diff, for the agent

Built in v2.6.173:

- In Source control's diff, each line of the change has a **+** (on
  hover, as a forge's review has): it adds a note under the line, saved
  with **Add the note** or Ctrl+Enter; a saved one has **Edit** and
  **Delete**, and its line a mark.
- A note says where: the line's number in the file as it is now, or as
  it was for a line taken out, read from the hunks' headers.
- A bar under Source control, in its list and on a diff, counts them
  ("2 notes on 1 file"), with **Send to the agent** and **Clear**.
  **Send** puts them in the chat's box, after what's there, as one
  message, by file, then line, each with its line's code, for the person
  to add to and send:

  ```
  Notes on the changes, line by line:

  - web/chart.js:11 (a line taken out), `ctx.strokeStyle = "red";`:
    Keep red as the fallback.

  - web/chart.js:11, `ctx.strokeStyle = up ? "green" : "red";`:
    Use the theme's green, not a literal.
  ```
- The notes are the workspace's, in this browser, until sent or cleared.
- Not Rewind: its timeline shows how many files and lines a message
  changed, not the lines, so there's nothing there to note on.

## Release 6: Codex in Chat, and every session

- Codex's sessions (`~/.codex/sessions/…/rollout-*.jsonl`) in Chat as
  Claude Code's are: its answers, its commands folded into a line, what
  it's doing. Its questions are already cards.
- A workspace's past sessions, both agents', newest first, each with
  **Resume** (`claude --resume`, `codex resume`).

## Release 7: the agents' notes on their workspace

Built in v2.6.173:

- `hi dev note "<a line>"` in the box: the agent's one line about where
  its work stands ("tests pass; waiting for the API key"), until the
  next; `--clear` takes it away. One line, at most 200 characters.
- Its workspace: herdr's (`HERDR_WORKSPACE_ID`), else the worktree its
  folder is in; elsewhere it says to run it in a workspace. Kept by the
  work's branch in `run/dev-notes.json`, so it outlives a dev box
  restart, and goes with the work.
- On the page: under its workspace in Workspaces, and on its item in
  Needs you, in quotes, with how long ago.
- The agents' instructions in the box (CLAUDE.md, AGENTS.md) say when:
  a step done, waiting for someone, stopping.

## Not taken

- **Orca's browser client, or its server, in the page:** above.
- **Orca's relay for phones:** its sign-in service isn't open source,
  and the page installs as an app already.
- **The same task to several agents to compare:** `hi dev review`,
  `brainstorm`, and `deep-review` do the useful part in the box; a
  button for it waits for someone to ask.

## Tests

Releases 5 and 7:

- In Chromium: the lines' numbers, new and taken out, from a hunk; a
  note added, its line marked, the bar's count; one by Ctrl+Enter;
  edited; kept when the diff opens again; Send: after what the box had,
  by file and line, the bar gone, the focus in the box. A workspace's
  note in Needs you.
- hi dev note: by herdr's workspace, by the worktree's folder, cut to
  200 characters, one line, a folder that's no workspace's refused,
  cleared; the agents' instructions and hi dev's help name it.

Release 3:

- The DM: not before ten minutes; not while someone's active on the
  page; to the owners when nobody known started it; once per wait; to
  the starter by their member ID for a new wait; the link without the
  address's key.
- In Chromium: the count, amber while something waits; waiting first,
  then done, with what for, how long, and who started it; a click opens
  that chat and the done goes; a new wait elsewhere notifies once, and
  what was there before doesn't; `#work-<branch>` opens its chat.
- By hand: a permission in a workspace, the page in another window, its
  notification; the page closed, the DM after ten minutes.

Release 2:

- Claude Code's events one after another, and the state after each:
  herdr idle while the hooks say working; a question herdr misses; a
  permission and its tool; Esc (herdr's idle wins after 15 seconds);
  herdr's done kept; another session or agent in the workspace: herdr's;
  a session's end; Codex's turn done; a workspace or agent hi doesn't
  know, dropped.
- The settings: hi's hook once for each event after two starts, beside
  herdr's, an owner's model kept.
- hi dev __hook through a real socket: nothing outside herdr; the tool
  and the pane; Codex's notice; the page's workspaces with what it
  waits for; no broker, or input that isn't JSON: nothing printed, no
  failure.
- In Chromium: a workspace's "Running Bash, for 2 min".
- By hand, on a team's dev box: a turn, a permission in auto mode, a
  question, Esc, and Codex's turn.

Release 1:

- The page's state: an owner and a Pro Dev get the peer, the account,
  the alias, the project, and the host key's fingerprint (as
  `ssh-keygen -lf` says); before NetBird no peer; a Vibe Dev nothing.
- Snapshots' excludes: the editors' folders in the dev box's home, and
  nothing outside it (restic matches `dev/home/.vscode-server` there,
  not a project's `sub/.vscode-server`, tried with restic 0.18).
- In Chromium, on the page with its real policy: **Editor ↗** before a
  pick; the four editors; each link for the workspace and for the
  project's own clone; Anything else over SSH, its command quoted, and
  its Copy; the Security view's `ssh` line; a kept editor no longer
  listed (Devin Desktop) back to **Editor ↗**; the SSH config; Copy; a pick
  kept, the bar then **Cursor ↗** with **▾**, and the page still there
  after the link; no key: no links, **Add my key** to its box; no peer:
  the admin's command; a Vibe Dev: nothing.
- By hand, on a team's dev box: VS Code, Cursor, and Zed from the
  links, Orca by its steps, and a terminal by the `ssh` line; each one's first connect,
  what it downloaded, and through which sites.

## Questions

1. **The editors' sites.** Answered (2026-11-16): hi supports VS
   Code, Cursor, Zed, and Orca, which need none, and plain SSH for the
   rest; the box's list stays as it is. What each does when the box's
   proxy refuses its sites (from their code, 2026-11-16):
   - VS Code and Cursor try the box first, then download their server
     on the computer and copy it over (`remote.SSH.localServerDownload`,
     `auto`); VS Code does the same for extensions. Zed does too, and
     its extensions come from the computer. Orca uploads everything.
   - Devin Desktop has no such fallback (`windsurf-stable.codeiumdata.com`,
     `marketplace.windsurf.com`); JetBrains downloads its IDE on the box
     (`download.jetbrains.com`) unless Gateway gets **Upload installer
     file**. Someone who uses them asks for the sites in Security.
2. **Orca's hooks in the box.** Answered (2026-11-16): release 2 writes
   hi's own hooks; Orca's stay (outside Orca they answer `{}` and exit
   at once).
3. **Release 3's DM:** answered (2026-11-16): to whoever started the
   work, else the owners; after ten minutes, fixed for now.
4. **More agents to start** (OpenCode, Gemini CLI, and the like): which,
   on whose sign-ins? The box's proxy puts only the app's Claude and
   ChatGPT sign-ins and its OpenRouter key on calls.
