# What the dev page takes from Orca

Status: Approved (2026-11-16): the plan, and release 1, built in
v2.6.168 and narrowed in v2.6.169 (questions 1 and 2 answered). Each
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

- hi's own hooks (question 2): Orca's, if someone's Orca wrote them,
  stay beside them.
- hi writes the hooks into the dev box's `~/.claude/settings.json` and
  Codex's config when the box starts, beside what an owner set: each runs
  `hi dev __hook <event>`, which hands the event to the broker's socket
  and never fails the agent's turn.
- The broker keeps each workspace's last events: the state, since when,
  the tool running, and what it's waiting for.
- The page's dots and the chat's status line come from it, with herdr's
  state where a hook says nothing. The screen stays where the question's
  options come from; a hook says only that one is there.
- For other agents, the status sequence some agent CLIs print
  (Orca's OSC 7501), if herdr passes it on, then the title.

## Release 3: needs you

- **An inbox** in the top bar: the workspaces whose agent waits for
  someone (a question, a permission, a site), and those done since you
  last looked, with who started each. A click opens its chat.
- **Notifications** while the page or the installed app is open: "14-news
  asks a question", through the browser's own, and the installed app's
  badge (the count waiting).
- **When nobody's here:** the page knows who's on it (v2.6.167). An
  agent that has waited ten minutes with nobody on the page gets one DM
  to whoever started its work, with the link.
- Push to a phone with the page closed needs a push service; later.

## Release 4: the terminal

xterm.js's add-ons, embedded as xterm.js is (`third_party/xterm`): WebGL
drawing, which keeps up with Claude Code's full-screen redraws; search
in the scrollback (Ctrl+F in the terminal); Unicode 11 widths, for
emoji and CJK; links that open; and pictures in the terminal (sixel and
iTerm's), for `timg`. Then xterm.js 6 once it's out of beta.

## Release 5: notes on a diff, for the agent

In Source control's diff, a click on a line adds a note to it; **Send
to the agent** puts them in the chat's box as one message, each with its
file and line, for the person to add to and send. The same on Rewind's
change of a message.

## Release 6: Codex in Chat, and every session

- Codex's sessions (`~/.codex/sessions/…/rollout-*.jsonl`) in Chat as
  Claude Code's are: its answers, its commands folded into a line, what
  it's doing. Its questions are already cards.
- A workspace's past sessions, both agents', newest first, each with
  **Resume** (`claude --resume`, `codex resume`).

## Release 7: the agents' notes on their workspace

`hi dev note "<a line>"` in the box: the agent's one line about where
its work stands ("tests pass; waiting for the API key"), on its
workspace's card and in the inbox, until the next. The skills tell the
agents when.

## Not taken

- **Orca's browser client, or its server, in the page:** above.
- **Orca's relay for phones:** its sign-in service isn't open source,
  and the page installs as an app already.
- **The same task to several agents to compare:** `hi dev review`,
  `brainstorm`, and `deep-review` do the useful part in the box; a
  button for it waits for someone to ask.

## Tests

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
3. **Release 3's DM:** to whoever started the work, or to the owners?
   After ten minutes, or a setting?
4. **More agents to start** (OpenCode, Gemini CLI, and the like): which,
   on whose sign-ins? The box's proxy puts only the app's Claude and
   ChatGPT sign-ins and its OpenRouter key on calls.
