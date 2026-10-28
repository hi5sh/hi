# `hi team dev` specification

Status: Approved (2026-10-24). Release 1 ships as v2.0.0.

Dependencies: the private links of `/hi-<team>` (team_people.go,
`hi_team_helper_signin.md`), the relay's key cookie in `hi net expose`,
NetBird (`hi net`), the proxy's token swap for boxes, and the broker's
preview, merge, release, snapshot, data and job functions.

## Goal

Building the app moves out of Slack. Each team gets a **dev box**: a
long-lived box with the team's repository, coding agents, and a
[herdr](https://herdr.dev) server that keeps their sessions. The owners,
who are the team's developers, work in it from any terminal over SSH, or
from a web page that `/hi-<team> dev` sends them. The page also shows the
state of the system: versions, preview, health, backups, jobs, incidents,
keys.

The lead stays in Slack with a smaller job: helping the app's users,
watching the system, and turning what people say into tickets. It no
longer runs coders and reviewers.

## Why

Two weeks of co-developing on team orbit through Slack showed the
paradigm doesn't work:

- **Nothing shows what is going on.** The work lives in threads, cards,
  DEV lines, warnings and incident posts spread across the channel. To
  know whether a change is building, in review, on the preview or waiting
  for a yes, the owner had to ask, or ask me to follow it.
- **The lead in front costs more than it hides.** Every step went through
  a model that relays, re-asks and summarises. On orbit a chart was done
  in 15 minutes and took 22 more of the lead checking it; a release was
  asked for twice (v21, v22, and again for v25).
- **Each fix added machinery.** Rounds, sessions, quick merges and
  docs-only approvals (v1.12 to v1.14) each made the loop faster, and each
  added rules the lead has to follow and the people can't see.

A developer working with coding agents wants what herdr, Orca or Claude
Code's web page give: the conversation, what the agent changed, and the
project's state, in front of them. herdr already does the hard part, so
hi builds less, not more.

## People

- **Owners are the developers.** Only they get into the dev box and the
  page. Several owners may attach to the same agent; they coordinate
  between themselves.
- **Everyone else in the channel** contributes ideas, suggestions and bug
  reports, which the lead turns into tickets. Nobody else starts work.
  The askers list goes.
- **IT admins** change who the owners are on the machine:
  `hi team owners <team> add|remove <@user>` (today owners are only set by
  `hi team new`, or by editing team.json). Removing an owner ends their
  links and their SSH access.

## The dev box

One per team, started with the team's service and kept like the lead's
box: replaced on a new hi, its state in the team's folder.

- **In it:** the team's repository with a worktree per ticket, the agents
  hi provides (Claude Code and Codex, signed in with the team's accounts
  through the proxy's token swap, so the box never holds a real token),
  herdr, `hi` with the broker's socket, and the dev kit (below).
- **herdr runs the work.** One workspace per ticket; in it the owner
  starts whichever agent they like. herdr's server keeps the agents
  running when nobody is attached, knows their state (working, blocked,
  done, idle), and resumes their conversations after a restart. hi
  builds none of that.
- **Permissions are skipped** (`--dangerously-skip-permissions`, and
  Codex's equivalent): the box is the boundary. The agents reach the
  internet only through the proxy's allowlist, can't reach the host,
  other teams, production's data or the team's secrets, and their
  commits stay on ticket branches. Dev and production move only through
  the broker, which checks who may do what (below).
- **Its state** (herdr's sessions, the agents' conversations, the
  worktrees) lives in the team's folder, so a new box loses nothing, and
  the team's snapshots back it up.

### Getting in

- **From any terminal, over SSH.** The dev box joins the team's NetBird
  network as its own peer (`<team>-dev`), in NetBird's userspace mode,
  which needs no TUN device in a rootless box. An SSH server in the box
  takes the owners' keys only, and NetBird's access control lets only the
  owners' group reach it. An owner then runs `ssh <team>-dev` and
  `herdr`, or adds the box to their own herdr over SSH, so the team's
  agents sit next to their local ones.
  - Owners give their public key once: `hi team owners <team> key <@user>
    <key>`, or on the page.
- **Joining NetBird.** `hi team new` asks for a NetBird setup key for the
  dev box; for an existing team, `hi team dev <team> netbird` asks. The
  admin makes it in the NetBird dashboard (Add Peer → Server; one-off,
  not ephemeral) with an auto-group such as `hi-<team>-dev`, and adds a
  policy that lets the owners' group reach that group on SSH; hi prints
  both steps, and changes nothing in NetBird itself. The box uses the key
  once. hi keeps the peer's identity (`/var/lib/netbird`) in the team's
  folder, so a new box, or a new hi, comes back as the same peer with the
  same name and address, without a key; hi doesn't keep the key.
- **From a browser.** `/hi-<team> dev` DMs an owner a private link, with
  the same per-person keys as today's `/hi-<team>` (14 days; asking again
  ends the old link). The page's terminal is xterm.js over a WebSocket to
  a herdr client in the box: the same workspaces as over SSH, from a
  laptop without a terminal, or from a phone.

## The page

Served by the team's service, like the helper, at its own address on the
relay, and opened only with an owner's key.

```text
┌───────────────┬────────────────────────────────────┬──────────────────┐
│ Tickets       │                                    │ System           │
│ ● #12 chart   │                                    │ PROD  v24  ✓     │
│ ◐ #14 news    │   herdr, in the browser            │ dev   v24 + 2    │
│ ○ #15 export  │   (xterm.js)                       │ preview: #12     │
│               │                                    │ backups ✓ 12:09  │
│ Inbox (lead)  │                                    │ jobs: 1 running  │
│ 3 new ideas   │                                    │ incidents: 0     │
│               │                                    │ [Release v25]    │
└───────────────┴────────────────────────────────────┴──────────────────┘
```

- **Left, tickets,** from the team's forge (below), each with its agent's
  state from `herdr agent list`. "Start" on a ticket makes its branch,
  worktree and herdr workspace (`hi team dev start <ticket>`).
- **Middle, herdr** in the browser.
- **Right, the system:** what hi already knows, live: production's and
  dev's versions, the preview's source, health, the last snapshot and
  offsite copy, jobs and workers, open incidents. Keys and settings
  (`hi team env`) on a tab, names only, values editable but never shown.
  **Release** is here: an owner's click is the yes, logged as the
  release's approval.

## Tickets: hi wraps `gh` or `glab`

The forge is where the team's repository lives, read from its remote:

- **github.com** (or a GitHub Enterprise host): GitHub Issues, through `gh`.
- **gitlab.com** or a self-hosted GitLab: GitLab issues, through `glab`,
  GitLab's own CLI (`GITLAB_HOST` for a self-hosted one).
- **Neither:** hi's own `tickets/` in the team's folder.

`hi team ticket list|show|add|comment|close` is the one interface; the
lead, the page and the agents all use it. `gh` and `glab` are shaped
alike (`issue list|create|close`, `gh issue comment` and `glab issue
note`), but their flags and JSON differ, so hi maps each to one ticket:
number, title, state, labels, author, link. hi runs them on the host with
the machine's sign-in, so neither the lead's box nor the dev box holds a
forge token.

## The dev kit: skills for hi's teams

Instead of a review loop, the owner asks their agent for what they need,
and the agent has skills made for hi's teams, installed for every agent
in the box. Most of them reuse what the Slack paradigm built:

| Skill | What it does | Built on |
|---|---|---|
| tickets | list, pick, comment on and close tickets; start one | the forge wrapper |
| say | post to the channel or a thread, as the owner's agent | `hi team say`, POST /say |
| preview | put the branch on the preview, with a fresh fork of production's data | `preview`, `freshDataForWork` (v1.14.2) |
| ship | merge the ticket into dev | `mergeWork` |
| release notes | draft what's new for the release, for the owner to release on the page | `teamReleaseAnnouncement` |
| review | a second opinion from the other agent (Claude reviews Codex's work, or the reverse), with the review rules that worked | `teamReviewRules`, `teamReviewHandover`, `hi agent` |
| deep review, brainstorm | the existing multi-agent reviews and brainstorms | POST /deep-review, /brainstorm |
| status | versions, jobs, workers, incidents, snapshots, the app's logs | GET /versions, /tasks, jobs.json, incidents/ |
| incident | look into an open incident with what hi recorded | incidents/ |
| data | fork production's data to the preview, query the preview's database | POST /data/copy |
| settings | the names of the app's settings; set the preview's | `hi team env` |

With them goes the team's `AGENTS.md` (and `CLAUDE.md`), written from
what the lead's soul and skill learned: specs first, docs with the code,
how the preview and its data work, what "verify on the preview" means.

The skills live in this repository under `skills/hi/team/<skill>/SKILL.md`,
next to `skills/hi/SKILL.md`, and are embedded in hi the way that one is
(skill.go). hi writes them into the dev box for each agent it provides,
as separate skills (`hi-team-tickets`, `hi-team-preview`, ...), so a new
hi brings new skills with the next box.

### What the box may do

The dev box gets the broker's socket with a role of its own, `dev`. The
broker lets it preview, merge into dev, say, file and close tickets, copy
data to the preview, and read everything. It refuses a release, a
rollback, a restore, production's settings, and changing owners: those
take an owner's click on the page, or an admin on the machine. So an
agent with permissions skipped can do anything to dev, and nothing to
production.

## In Slack

- **The lead's new role:** helping the app's users in the channel and the
  helper; watching the system (incidents, failed jobs, backups, offsite
  copies, as hi posts today); collecting feedback, ideas and bug reports
  into tickets; answering "what's the status of X" from them. Its soul
  and skill lose the development steps.
- **The channel** gets: new production versions, incidents, what the lead
  answers, and what an owner's agent posts with `say`. No DEV lines,
  progress cards or review notes.

## What changes in hi

- **New:** the dev box (herdr, the agents, sshd, NetBird), `hi team dev`
  (the page and `start`), `/hi-<team> dev`, `hi team owners`, `hi team
  ticket` with the gh, glab and local backends, the `dev` role on the
  broker, the dev kit.
- **Slimmer:** the lead's soul and skill; the channel's posts.
- **Frozen now, removed in release 4:** the Slack development loop. No
  more work goes into it, but it stays until the dev box has taken over
  orbit's development, so the team is never without a way to build
  (see Cleaning up).
- `hi agent` stays, for the review skill and for scripts.

## Cleaning up

Start lean: nothing new is built on the Slack loop, and release 4 removes
it in one go, with the lead's new soul. Removing it first would leave
orbit with no way to develop until the dev box works, and a removal
mixed into the dev box's releases would make both harder to review.

| Goes in release 4 | Why |
|---|---|
| The lead starting tasks, reviews, merges, previews, releases (its soul's development steps, the skill's go-ahead and waiting sections) | Owners do this in the dev box |
| The review loop: `nextStep`, rounds, fix briefs, `teamMustChange` | Review is a skill |
| Member sessions per chain (`tasks/<root>-sessions`) | herdr keeps the agents' sessions |
| Quick merges, docs-only approvals | No review gate to get around |
| Progress cards, DEV lines and their rules, the Told-thread texts | The page and herdr show the state |
| The release yes in Slack, `--ask`, approvals by message id for work | An owner's click on the page |
| Handoffs, the askers list | No relaying; only owners build |

| Stays, reused | Where |
|---|---|
| The broker's functions: preview, data fork, merge, release with its snapshot, rollback, restore, data copy | The dev role, the page, the skills |
| The review rules and handover prompts | The review skill |
| Deep review, brainstorm | Skills |
| Incidents, jobs, workers, health, snapshots, offsite copies | The system panel, the lead's watch |
| Per-person keys and DM links, the relay | `/hi-<team> dev` |
| The proxy's token swap, boxes, `hi agent --session` | The dev box, the review skill |
| `hi team say`, the helper, feedback | Skills, the lead's new role |

## Release 4 plan (2026-10-25)

Decided with the user on 2026-10-25: adopt's survey and upstream updates
move into the dev box; rollback is on the page; anyone in the channel
may talk to the lead; and every action that took an owner's yes in Slack
moves to the page, so Slack approvals go entirely.

### What the page gets first

So nothing is lost when the Slack side goes, the page takes over each
action an owner said yes to in Slack:

| Was: an owner's yes in Slack | Now, on the page (an owner's click) |
|---|---|
| A release | Release (release 3) |
| A rollback, of the code or the code and the data | The releases, each with Roll back |
| Production's data from the preview | Copy the preview's data to production |
| The helper on production | The helper on or off, for the preview and production |
| The lead restarting the app a second time in an incident | Each open incident, with Restart and Close |
| A rule for a job, from what the people decided | Rules, by job, added and removed |
| A deep review or brainstorm from Slack | The dev box's hi dev deep-review and hi dev brainstorm (release 2b) |
| Proposing work back to an adopted project | hi dev propose in the box, a draft pull request |

And the box takes over what the lead handed to a coder:

- **Upstream:** the lead still says in the channel when the project the
  team adopted has a new version, and files a ticket for it. `hi dev
  upstream` shows what's new and brings upstream's refs into the box's
  clone, where the owner's agent merges a tag and ships it.
- **Survey:** an adopted project's survey becomes the `hi-dev-survey`
  skill, which the owner asks their agent for; its result goes in
  `docs/` as before.

### What goes

| Goes | Where it is |
|---|---|
| The lead's tasks, reviews, merges, stops, previews of tasks, release asks, discards; its socket's routes and its commands for them | team_broker.go (startTask, newWork, launch, wait, startReview, nextStep, mergeWork, the routes and client cases), team_versions.go (askRelease, ideas), team_preview.go (task previews) |
| Members: coder and reviewer, their roles and learned notes | team.go (Members, the wizard's step), team_roles.go |
| Review rounds and fix briefs | team_rounds.go (teamPreviewCheck moves to team_devkit.go) |
| Member sessions per piece of work | team_threads.go |
| Quick merges, docs-only approvals | team_quick.go |
| Progress cards, DEV lines, the told-thread texts | team_progress.go, announceDev, tellNewPreviewLink's thread part |
| Handoffs | team_handoff.go |
| Approvals by message id, say: go-aheads, the helper's approvals | team_approval.go, team_say.go's approval, team_helper.go's |
| The askers list | team.go, team_people.go, the lead's .env (Slack users: everyone in the channel) |
| The lead's development steps | its soul and hi-team skill (team_serve.go), the soul's old-to-new updates for them |
| `hi team progress`, `hi team logs <task>`, the tasks in `hi team status` | team.go |

### What stays

- **Work records** in `tasks/`: what `hi dev ship` records, so the
  versions, the release notes ("built in the dev box"), and propose still
  read them. Old teams' records stay readable.
- **The lead's watch:** incidents, jobs, workers, snapshots, upstream's
  new versions; it notes, asks, and closes incidents, and restarts the
  app once on its own.
- **Its new work:** helping people in the channel, the helper, and DMs;
  filing what they ask for as tickets (`hi team ticket` in its box); and
  answering what's the status of something from the tickets and versions.
- `hi team release`, `rollback`, `data`, `helper`, `env` on the machine,
  for an admin, without a yes.

### Steps

1. The page's new actions, `hi dev upstream`, `hi dev propose`, and the
   survey skill (adds only).
2. The lead's new soul and skill, and `hi team ticket` in its box.
3. The removal, with tests that put work into dev the way the box does.
4. The guide: the pages about the Slack loop rewritten for the dev box.

## Later: a defined process

Once owners work in the dev box, the process can be steered with more
than habit. The inspiration is the "AI software factory"
(youtu.be/tNKmMhiOw9w): plan a release, split it into issues, run one
loop per issue (pick, build, validate the app, review, merge), test the
whole release on staging, then ship; a manager agent steps back every
few hours and asks when it needs a call; standards graded on every merge
turn a failing rule into an issue. Most of it maps onto what hi has:

| Factory | hi |
|---|---|
| Issues, split from a plan | Tickets in the forge; a "split" skill |
| One loop per issue, many at once | A herdr workspace per ticket |
| App validation | The preview, with production's data |
| Code review | The review skill (two reviewers: Claude and Codex) |
| Staging, test the whole release | The preview on dev, before Release |
| Production | Release with its snapshot; rollback |
| Manager agent | The Slack lead, watching tickets, jobs and incidents, asking the owners when something needs a call |
| Standards (bronze, silver, gold) graded on every merge | Checks hi runs on merge into dev (tests pass, specs and docs updated, CI green), shown on the page; a failing one files a ticket for an owner to approve |
| Registry: every agent and skill has an owner | The dev kit's skills, versioned with hi |

None of this is in releases 1 to 4. It gets a spec of its own once the
dev box has been used for a while.

## Releases

1. **The dev box over SSH:** the box with herdr, Claude Code and Codex,
   NetBird and sshd, `hi team owners` with keys. Owners can work in it
   from their own terminal and herdr from day one.
2. **Tickets and the kit:** `hi team ticket` over gh, glab or local; the
   `dev` role; the first skills (tickets, say, preview, ship, status,
   review).
3. **The page:** `/hi-<team> dev`, herdr in the browser, the system panel,
   Release.
4. **The lead's new role,** and removing the Slack development machinery.

## Decisions

- Owners are the developers; the rest of the channel files ideas and bug
  reports; IT admins change owners with `hi team owners` (2026-10-24).
- Tickets live in the forge the repository is on, through `gh` or
  `glab`, else in hi (2026-10-24).
- herdr runs and keeps the agents in the dev box; owners pick the agent
  (2026-10-24).
- Agents skip permissions inside the box; production is protected by the
  broker's `dev` role (2026-10-24).
- Several owners may share an agent and coordinate themselves
  (2026-10-24).
- No review loop: review is a skill the owner asks for (2026-10-24).
- One dev box per team (2026-10-24).
- The dev kit ships in `skills/hi`, embedded in hi (2026-10-24).
- Start lean: the Slack loop is frozen now and removed in release 4; a
  defined process comes later, in a spec of its own (2026-10-24).

- The dev box's NetBird key comes from the admin at `hi team new`, not
  from an API token on the machine (2026-10-24).

- Owners log in with sshd and their own keys, not NetBird's SSH: herdr's
  `--remote` expects plain ssh, and NetBird's SSH needs a switch per peer
  in the dashboard (2026-10-24).

## Spike (2026-10-24)

On gpu1, in a rootless Podman box (Alpine) with NetBird 0.80.0:

- **NetBird without a TUN device works.** With `NB_USE_NETSTACK_MODE=true`
  and `NB_ENABLE_NETSTACK_LOCAL_FORWARDING=true`, the box joined as its
  own peer (`hi-spike-devbox.corp.example`, "Interface type: Userspace"), and
  from the host over NetBird an sshd in the box on port 2222 took a key
  login, and a web server answered in 0.1 s.
- **The identity carries over.** A new box started with the first one's
  `/var/lib/netbird` and no key came back as the same peer, with the same
  name and address.
- **NetBird's own SSH** (`--allow-server-ssh`) is "locally allowed but
  disabled by management server" until the peer has SSH switched on in
  the dashboard.
- **herdr runs headless.** `herdr server` runs as a plain process with no
  terminal and answers its API; `herdr worktree create --branch <b>`
  makes the branch, the worktree and its workspace in one step (they go
  to `~/.herdr/worktrees/<repo>/<branch>` unless `--path` says where).
  After a server restart herdr restores the layout and resumes Claude
  Code (`claude --resume`) and Codex (`codex resume`); other processes
  come back as shells. `herdr --remote ssh://…` attaches over plain SSH
  and installs herdr there if it's missing. `herdr --skill` prints a
  skill that teaches an agent to drive herdr.

## How we'll know

- An owner can tell, without asking anyone, what each ticket's state is
  and what production runs.
- A small change on orbit (a chart line) goes from ticket to production
  in under 15 minutes, with no message in Slack except the release.
- The channel has a tenth of today's posts.

## Options considered for the agent's interface

| | Why not, or not alone |
|---|---|
| [CloudCLI / claudecodeui](https://github.com/siteboon/claudecodeui) | AGPL-3.0; single user, no auth of its own; runs on the host, outside hi's boxes |
| [OpenHands Agent Canvas](https://docs.openhands.dev/openhands/usage/agent-canvas) | Beta; its own model of work; Claude Code's subscription sign-in only on the same machine |
| [Vibe Kanban](https://sharedcontext.ai/plugins/external/BloopAI/vibe-kanban) | Community-maintained since April 2026; parallel-agents paradigm |
| [Orca](https://github.com/stablyai/orca) | Desktop only; can't be sent as a link (MIT, so ideas can be borrowed) |
| code-server + the [Claude Code extension](https://code.claude.com/docs/en/vs-code) | Heavy; unofficial in code-server |
| [Remote Control](https://code.claude.com/docs/en/remote-control) | Tied to one claude.ai account; no room for hi's panels |
| A chat on the [Agent SDK](https://code.claude.com/docs/en/agent-sdk) | A product on it must use an API key, not a claude.ai sign-in; re-implements permissions |
| herdr (chosen) | Apache-2.0, agent-aware, keeps sessions, SSH-native, CLI and socket API; young (v0.9.5), so tmux is the fallback for keeping sessions |

## As built

### Release 1 (v2.0.0): the dev box over SSH

- **The box** (`hi-box-team-<team>-dev`) runs hi's base image with
  openssh-server (`localhost/hi-box-dev:<hash>`), as `box` with
  `/box/home`, hardened as other boxes, 8 GB. Its main process is sshd
  (port 2222, the owners' keys only, from `dev/ssh/authorized_keys`, read
  only in the box), then `herdr server`. The team's service starts it if
  it isn't running and leaves it when it restarts; `hi team down` and
  `hi team rm` stop it; snapshots pause it with the lead.
- **The project** is the box's own clone in `dev/repo` (`/box/repo`),
  with the team's repository as `origin`, read-only.
- **The agents:** Claude Code (the host's, mounted) and Codex
  (`/opt/codex`), with permissions skipped (`defaultMode:
  bypassPermissions`; Codex `approval_policy = "never"`, `sandbox_mode =
  "danger-full-access"`). One proxy serves both: the team's Claude token,
  or this machine's, and ChatGPT under its Codex path, with OpenRouter
  when either refuses. An SSH login reads the same settings from
  `~/.hi-env`.
- **NetBird:** a peer beside the box (`-net`), on the default network and
  the box's, in userspace mode; `hi box __forward` hands its port 22 to
  the box's sshd. It joins once with `dev/netbird-setup-key`, keeps its
  identity in `dev/netbird`, and hi removes the key once the peer exists.
  Neither is in snapshots.
- **Commands:** `hi team dev <team> [restart | netbird [<file>] |
  new-host-key]`, `hi team owners <team> [add | remove | key]`; `hi team
  new` asks for the setup key at the end.
- **Checked on orbit (2026-10-24):** the box built and started; Claude
  Code and Codex each answered one call through the proxy on the team's
  sign-ins; an SSH login with an owner's key landed as `box` on
  `orbit-dev` with the agents' settings, herdr, claude, codex and hi on
  the path. NetBird waits for orbit's setup key.

### Release 2 (v2.1.0): preview, ship, tickets, and the first skills

- **The `dev` role** is a socket of its own (`dev.sock`, beside the
  broker's, mounted at `/box/run`), with only status, preview, ship, say
  and tickets on it: nothing that moves production is there to refuse.
- **`hi dev`** in the box is its client. `preview` and `ship` take the
  current branch by default. The broker fetches the branch from the box's
  clone into the team's repository as `box/<branch>`; preview shows it with
  a fresh fork of production's data when it is new work on the preview;
  ship merges it with `--no-ff`, records it as work (role `dev`, "built in
  the dev box" in the release), pushes to the team's remote, and puts dev
  on the preview. A conflict aborts the merge and says how to rebase; new
  `trustedDependencies` and exploited advisories are refused.
- **Tickets:** `hi team ticket` and `hi dev ticket` (list, show, add,
  comment, close) over `gh` for github.com, `glab` for gitlab.com or a
  GitLab of one's own (`GITLAB_HOST`), from the team's remote; hi's own
  `tickets/` otherwise. gh and glab run on the machine.
- **The kit:** `skills/hi/team/` (hi-dev-ship, hi-dev-tickets, hi-dev-say,
  hi-dev-status), embedded in hi, written to `~/.agents/skills` with links
  from `~/.claude/skills`, with herdr's own skill from `herdr --skill`; an
  instructions block at the top of `~/.claude/CLAUDE.md` and
  `~/.codex/AGENTS.md`, replaced on each start, the owner's text below it
  kept. A running box gets them when the service starts.
- **Fixes:** the setup key goes as soon as the peer has joined; a lock
  keeps the service and `hi team dev` from starting the box at once;
  tests' leftover background work no longer writes to the machine's teams.
- **Not yet:** the review skill (a second opinion by the other agent),
  deep review and brainstorm as skills, data and settings skills: release
  2b.

### Release 2b (v2.2.0): reviews, brainstorms, data, settings, incidents

- **A second opinion** (`hi dev review`) runs in the box itself, not
  through the broker: the other agent than the one asking (Claude Code
  sets `CLAUDECODE`, so Codex reviews Claude's work, and Claude the rest;
  `--with` names one) reviews what the branch adds to dev, in a detached
  worktree of its own that is removed after, with the rules a team's
  reviewer had (`Verify on the preview:`, `Later:`, `Alert:`), up to 30
  minutes. It reviews commits: uncommitted work is refused. Its report is
  printed and kept in `~/hi/reviews/`. The reviewer doesn't get the dev
  socket, so it can't ship or post.
- **Deep review and brainstorm** go through the dev socket to the broker,
  which runs them as it does for the lead (`launchDeepReview`,
  `launchBrainstorm`, split out of the Slack paths). No Slack yes: only
  owners get into the box, and the skills tell the agent to ask the user
  for the budget first. A deep review from the box may spend up to $50; a
  brainstorm, as before, $10. Records carry `dev: true`; when one ends,
  its report or ideas are copied into the box's home (`~/hi/reviews/<id>/`,
  `~/hi/brainstorms/<id>/`) instead of a DM, an upload, and waking the
  lead, and the box's agent writes the proposal.
- **Data:** `hi dev data` (the folders, without the machine's paths) and
  `hi dev data fork`. Querying the preview's data from the box waits: the
  data isn't in the box, and the app's own API reads it.
- **Settings:** `hi dev settings` shows the names; the box sets and unsets
  only the preview's own values, and no value is ever read back.
- **Incidents:** `hi dev incidents [<id>]`, read-only.
- **Skills:** hi-dev-review, hi-dev-brainstorm, hi-dev-data,
  hi-dev-settings, hi-dev-incidents; the instructions block names them.
- **Release notes** wait for the page (release 3), where the owner
  releases.

### Release 3 (v2.3.0): the dev page

- **Its address** is an exposure of its own, `team-<team>-dev`, renewed
  before it ends with the same key (a name that stays,
  `<team>-dev.<domain>`, with `hi team domain`). Everything is under
  `/__hi/dev/`, which the relay routes, always, to a socket of the
  service's (`dev.sock` in the web folder, which no box mounts), with the
  person's key from their link as `X-Hi-User`; the address itself goes to
  a listener on this machine that sends the browser to `/__hi/dev/`. The
  page and its scripts load for anyone with the address's key; its API
  answers only an owner's key.
- **Links:** the same per-person keys as `/hi-<team>`. An owner's DM gets
  a "Dev page" line with the app's links; `/hi-<team> dev` refuses anyone
  but an owner. The DMs are updated when the address changes.
- **The terminal:** a WebSocket per browser tab; the service runs
  `podman exec -it <box> herdr` under a pseudo-terminal of its own and
  passes bytes both ways, with the page's size. Closing the tab ends that
  herdr client; herdr's server and the agents stay. A WebSocket whose
  Origin isn't the page's address is refused (the key's cookie is
  SameSite=Strict too). xterm.js 5.5 and its fit addon are embedded
  (`third_party/xterm`), so the page loads nothing from elsewhere.
- **Left:** the team's tickets (cached 30 s) and herdr's workspaces with
  each agent's state (`herdr workspace list`, `herdr agent list`). A
  ticket whose branch starts with its number shows its workspace's state
  and Open (`herdr workspace focus`); otherwise Start, with Claude, Codex,
  or a shell: `hi team dev <team> start <ticket> [--agent ...]` does the
  same on the machine. Start fetches dev into the box's clone, makes
  `<number>-<words of the title>` from `origin/<branch>` with
  `herdr worktree create`, and types the agent into its pane with a
  prompt to read the ticket first.
- **Right:** production, dev and its changes, the preview, backups,
  incidents, jobs; the settings' names, with set (both, the preview, or
  production) and remove; values are never sent to the page. **Release
  v<n>** opens the notes, drafted from dev's changes; the click releases
  with the owner as the yes ("<name> on the dev page" in versions.json and
  the release's announcement).
- **On a phone** the three columns are tabs.
- **Not yet:** the lead's inbox of ideas (release 4), a rollback button
  (an admin, or the lead, rolls back), and health beyond incidents.

### Release 4 (v2.4.0): the lead's new role, and the Slack loop removed

- **The page** took every yes the Slack loop asked for: Release (release
  3), and now Roll back (code, or code and data, for each release),
  Set dev back to production, Copy the preview's data to production, the
  helper on or off for each address, each open incident with Restart and
  Close, and the jobs' rules, added and removed. Each is an owner's click,
  logged as "<name> on the dev page". On the machine, `hi team release`,
  `rollback`, `data`, `helper`, and `env` need no yes.
- **The box** took what the lead handed a coder: `hi dev upstream` (where
  dev stands, and upstream's refs into the clone), `hi dev propose` (a
  draft pull request upstream, for shipped work), and the hi-dev-survey
  and hi-dev-upstream skills. The box's instructions say when a project
  was adopted, and carry the map and drawing rules the briefs had.
- **The lead** helps people in the channel, the helper, and DMs, files
  what they ask for as tickets (`hi team ticket` in its box, without
  close), answers status questions from the tickets and `hi team
  versions`, and watches incidents, jobs, upstream, and backups; it
  restarts the app once per incident, and asks the owners for the rest.
  Its soul and hi-team skill are new; a SOUL.md that still has the lead
  building is replaced on the next start, the old one kept as
  `SOUL.before-2.4.md`. The specs are read-only in its box. Prompts hi
  wakes it with (unreported jobs, workers, incidents, deep reviews) say to
  file a ticket instead of starting a task.
- **Removed:** the lead's tasks and its socket's routes and commands for
  them (task, review, wait, show, tasks, merge, stop, preview, release
  --ask, rollback, discard, propose, data copy, helper on/off,
  brainstorm, review --project), review rounds, member sessions, quick
  merges, progress cards and `hi team progress`, DEV lines and the told
  threads, handoffs, approvals by message id (channel, DM, app's chat,
  `say:`), the askers list (everyone in the channel may talk with the
  lead), the members' roles (`/team/roles`), the survey and upstream
  tasks, release asks, ideas and their cap, task incidents, and task logs.
  About 9,400 lines, with their tests.
- **Kept, unlike the plan:** the members in team.json and the wizard's
  members step. They decide which sign-ins the team keeps (Claude,
  ChatGPT, OpenRouter), which the dev box uses too; only their tasks and
  roles went. The wizard no longer asks who may ask for work.
- **Kept:** work records in `tasks/`, which `hi dev ship` writes and the
  versions, release notes, and propose read; Slack deep reviews' and
  brainstorms' endings, for records from before.

### v2.4.2: the helper without the chat

Decided with the user on 2026-10-25: the helper's Change tab, the chat
with the lead in the app, goes. It overlapped with Feedback, which the
lead files as a ticket, and with the channel and DMs, where people talk
with the lead. The helper is help and feedback for everyone; `/hi-<team>`
sends only owners a link, to the dev page; the app's chat routes, its
copies in DMs, and `hi team web post` are gone (old chats still show with
`hi team web show w-3`). A new app's welcome page points at Feedback.

### v2.6.24: a database from the dev box

A team's app used MongoDB Atlas, and its agents couldn't test against a
database from the dev box: the box's only way out is its proxy, an HTTP
one, which a database's driver doesn't speak, and the box had no DNS for
`mongodb+srv://` to look up.

- The dev box's proxy answers the box's DNS (the box's `--dns` is the
  proxy). A name the owners allowed is the proxy's address; an allowed
  name's SRV and TXT records are looked up for it, and the SRV targets
  (Atlas's shards, under another name) become reachable for a day; any
  other name doesn't resolve, so DNS carries nothing out. IPv6 has no
  answers.
- On MongoDB's port, 27017, the proxy reads the name a connection's TLS
  hello asks for (SNI), and joins it to that name on the same port, when
  it's allowed or an SRV target, through the dialer that reaches public
  addresses only. TLS runs end to end: an X.509 client certificate signs
  in as it would directly. A connection without a name is closed. Every
  join and refusal is in the box's network log.
- The proxy binds 53 with `net.ipv4.ip_unprivileged_port_start=53` in its
  own network namespace, with every capability still dropped.
- The agents ask for the database's name with `hi dev allow`, as for a
  site.
- The dev box gets its own settings, never the app's
  (`hi team env <team> --dev KEY=VALUE` or `--dev --file KEY=<path>`), so
  production's keys stay out of the agents' reach: owners give it a test
  database's user. Files are read-only at `/hi-files`. The environment
  reaches the box from a 0600 file, no longer on the engine's command
  line.
- A dev box started before v2.6.24 keeps no DNS until it starts again
  (`hi team dev <team> restart`), which an owner does when the agents are
  idle.

