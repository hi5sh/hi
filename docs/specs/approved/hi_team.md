# `hi team` specification

Status: Approved (2026-10-10). The first slice is built (v0.37.0).

Dependencies: `hi box` (boxes, the proxy, token injection, the allowlist),
`hi agent` (Claude Code, Codex, and Hermes in a box, reports, stats, models),
[hi_agent_best_of.md](hi_agent_best_of.md) (checked and ranked runs),
[hi_agent_bundles.md](hi_agent_bundles.md) (bundles and briefs), and
optionally `hi server` (Slack approvals, budgets, `hi server ai`).

## Goal

A hi team is a virtual team of agents that lives in one Slack channel and
builds and runs one project for the people in it: an app, a tool, a data
pipeline, an integration.

```sh
hi team new payments     # a wizard: Slack app, channel, people, members, keys
hi team up payments      # one line: the team starts and stays up
```

The people in the channel may not read code. They may be admins who are
experts in their own field and want "an app that does X". They talk to the
team in plain language, try what it builds, and say whether it works. The
team does the rest.

- **One team, one channel, one Slack app, one host.** A team never spans
  hosts or channels. A new project is a new team.
- **The team is a folder.** Everything a team knows and has made lives in
  one folder on the host. The containers are disposable, rebuilt from the
  folder. Backing up, restoring, or moving a team works on the folder.
- **No docker compose.** A team is hi boxes, started and kept up by hi.

Wording: in hi's docs, "your team" is always the people (and "your team's
hi server" their server). "A hi team" or "the payments team" is always the
virtual one.

## Members

A team is a set of members. A member is a role: an agent, a model, a
bundle, and standing instructions.

```jsonc
// ./team-payments/team.json
{
  "name": "payments",
  "purpose": "An app for the finance admins to track supplier payments",
  "channel": "C07PAYMENTS",
  "members": {
    "lead":       { "agent": "hermes", "model": "qwen/qwen3.8-max-0902" },
    "coder":      { "agent": "claude", "model": "opus",   "bundle": "web-dev" },
    "researcher": { "agent": "codex",  "bundle": "research" },
    "reviewer":   { "agent": "codex",  "model": "gpt-5.5" },
    "devops":     { "agent": "claude", "model": "sonnet", "bundle": "devops" }
  },
  "budget_usd_month": 300
}
```

- **Each role has a home** in the team's folder, `roles/<role>/`: its
  standing instructions (`ROLE.md`) and what it has learned on this team
  (`LEARNED.md`), given with every brief for the role. See
  [hi_team_roles.md](hi_team_roles.md) (v0.76.0).
- **The lead** (Hermes) is the only member that runs all the time. It is
  the communicator and coordinator: it talks in the channel, keeps the
  project's memory, specs, and docs, breaks work into tasks, and decides
  which member gets each one. It uses Hermes' own Slack gateway, memory,
  cron, and subagents.
- **Every other member** is called for one task at a time: the lead hands
  it a brief, it works in a box of its own, and it leaves a report, as
  `hi agent` does today. Continuity comes from the lead's memory and from
  resuming the member's last session for the same piece of work
  (`claude --resume`), not from a process that stays up.
- **Work waits for a yes.** A new task (not `--on`, which continues one)
  names the message that approved it: `--approval <id>`, the id from
  `hi team channel --ids`. The broker accepts it only if that message is
  in the channel's log (from Slack, through the host, so the lead can't
  write one), from an owner or asker (anyone when the team has no askers),
  within a week, and after a message of the lead's in the same thread: a
  reply to the lead's question, never the request itself. It polls Slack
  first if the message isn't logged yet. `--small` starts a small fix
  without one, and the broker posts in the channel that it did. The task
  records its approval.
- **The lead answers only when @-mentioned**, even in its own threads
  (Hermes' `require_mention` and `strict_mention`), so people can talk
  among themselves. It still takes in everything: Hermes drops messages
  that don't mention it, so the broker logs the channel from the host with
  the bot's token (`conversations.history` and `conversations.replies`
  every two minutes, threads up to two weeks old, shared files up to 50 MB)
  into `channel/`, mounted read-only in the lead's box. The lead reads it
  with `hi team channel [--since 2h]`, and a Hermes cron job,
  `channel-watch`, runs every 15 minutes: its script prints what's new
  (`hi team channel --new`) or wakes nothing, and the lead keeps what
  matters in its memory and posts nothing.
- **Memory.** Hermes' built-in memory is a small notepad (a few thousand
  characters), enough for the essentials but not for everything said in a
  channel. The lead's long-term memory is Hindsight, a memory provider
  Hermes supports: it extracts facts and entities from every turn and
  recalls them by meaning before each one. It runs as one more container
  per team (`ghcr.io/vectorize-io/hindsight`, pinned), at .200 on the
  team's internal network, its database in `memory/`, its web console off.
  Its model calls go to the team's proxy, which adds the OpenRouter key,
  with the lead's model; embeddings and reranking run in the container.
  The lead uses Hermes' hindsight plugin in `local_external` mode, with a
  memory bank named `team-<name>`: hi copies the plugin at the commit
  Hermes' catalog pins, and installs its one client library
  (`hindsight-client`, plus `aiohttp-retry`) once per machine in
  `~/.local/share/hi`, mounted read-only and on `PYTHONPATH`, so the box
  installs nothing. If any of it fails, the team starts with the built-in
  memory and says why in its log. `"memory": "off"` in `team.json` leaves
  it out.
- In the channel the lead speaks of members by role ("I've asked the
  researcher to compare the two payment providers"), so the channel reads
  like a team at work.
- `hi team add <team> <role>` and `hi team rm <team> <role>` hire and drop
  members without running the wizard again.

The default roster is lead, coder, reviewer, and devops; the researcher and
others are optional. The reviewer must be a different agent or model from
the coder, so the code is checked by someone other than its author.

## The lead bridges the gap

The lead keeps two versions of every piece of work:

- **For the people:** what it will do, in their words, and how they will
  know it works: a short list of things they can try ("Open the payments
  page, add a supplier, and the supplier shows in the list"). The people
  agree to this list before work starts. It is the feature's acceptance
  list.
- **For the members:** the technical spec and the brief: files, data
  model, tests, the checks that must pass, and the acceptance list turned
  into automated tests where it can be.

Both live in the team folder (`specs/`) and in the repository's docs, so a
new member, a restored team, or a person reading later sees the same
history. The lead never asks the people a code question; when it needs a
decision it asks it as a choice about behavior.

## Definition of done

A feature is done when it runs on the live app and the people agree it
works. Getting there:

1. **Spec.** The lead and the people agree the acceptance list.
2. **Build.** The coder works on a branch in its own box, with tests for
   the acceptance list.
3. **Review.** The reviewer reads the diff against the spec and runs the
   checks; for important work, `hi agent best-of` runs the task more than
   once and keeps the best. A rejected change goes back to the coder with
   the reviewer's notes, up to a set number of rounds, then to the lead.
4. **Merge.** Only the lead merges to `main`, and only after the review
   passes. No person reviews code. `main` is always deployable.
5. **Preview.** Devops deploys `main` to the team's preview app, and the
   lead asks the people to try the acceptance list there, with a link.
   `main` is **dev**: what the people try, and may throw away.
6. **Live.** When the people say it works, devops deploys to the live
   app. Small fixes (a typo, a broken button, a wrong label) start without
   waiting for a go-ahead, but reach production only with the next
   release, like everything else. Every live deploy is a release with a
   number (see Dev and production), and a snapshot is taken first (below).
7. **Done, or a bug.** The lead marks the feature done in its notes. A bug
   found later by a person, the lead, or the reviewer becomes a new task
   linked to the feature, and goes through the same steps.

To the people this looks like "straight to main": nobody waits for a human
code review. The branch, the review, and the merge cost the agents little,
and they keep `main` working, give every change a clear point to roll back
to, and keep the coder's mistakes out of the live app.

Who decides what is set in the wizard: who may ask for work (members of
the channel by default), who may approve a live deploy, and who may roll
back or change the budget (the team's owners).

## Dev and production

Two versions of the project, named the same way everywhere:

- **Dev** is the team's branch (`main`). Every merge goes there; previews
  run it. What the people try there may be thrown away.
- **Production** is the `production` branch. Only the broker moves it, and
  only on an owner's yes (`hi team release --approval <id> --notes <file>`):
  dev's latest commit becomes the next version, v1, v2, ..., tagged
  `release-<n>` and pushed with the branch. `hi team release v2 --approval
  <id>` puts production back on an earlier release (the remote branch is
  force-pushed: it is the broker's alone). The numbers never repeat.
- An approval counts once: a yes to a task doesn't also release it.
- **Announced.** Every move of production, and every time dev is set back
  to it, is posted in the channel itself, outside any thread: the version,
  the one before, who approved it, and the lead's notes for the people.
- **Discarding.** `hi team discard <task>` drops an idea that isn't merged:
  the task, the tasks continuing it or that it continues, their reviews,
  boxes, and branches. `hi team discard dev --approval <id>` (anyone who
  may ask for work) sets dev's code back to production's, as a new commit
  that keeps the lead's specs; the merged tasks since production are marked
  discarded and listed in the commit and the channel.
- **At most five ideas.** An idea is a line of tasks not merged or
  discarded, counted once by its latest task. A new task past five is
  refused, with the list, until one is merged or discarded; continuing the
  latest task of an idea (`--on`) adds none.
- **Always named.** `hi team versions` and `hi team status` say
  "production v3" and "dev, v3 + 2 changes"; a preview says which it runs
  (`hi team preview production` runs production), and the lead uses those
  words in the channel.

The record is `versions.json` in the team's folder: the releases, the
version production is on, and every move with its approval.

## The team folder

```text
./team-payments/   (where hi team new ran, or --dir)
  team.json          members, channel, owners, budget (no secrets)
  versions.json      the releases, the version production is on, and every move
  secrets.json       Slack tokens, keys (0600, never exported in the clear)
  slack-manifest.json
  lead/              Hermes' home: memory, state.db, SOUL.md, skills, cron
  repo/              the project's git repository (main, branches, tags);
                     repo/docs/specs is what the lead and the people agreed
  roles/             each member's role: ROLE.md and LEARNED.md (v0.76.0)
  tasks/             each task's brief (t-3.md) and record (t-3.json)
  channel/           the channel's log (a JSON line per message, a file per
                     day) and the files shared in it
  memory/            the lead's long-term memory: Hindsight's database
  app/               the running app's data: databases, uploads
  run/               the lead box's home and its proxy's allowlist and log
```

The folder lives where the team was made, beside the user's other
projects. `~/.local/share/hi/teams/<name>` is a link to it, so commands find
a team by name from anywhere, and the systemd unit names only the team.
A moved or copied folder is registered again with `hi team up <folder>`.
`secrets.json` and `run/` are in the folder's `.gitignore`.

The broker's socket is in the user's runtime folder
(`/run/user/<uid>/hi-team-<name>`), since a socket's path must be short.

Bind-mounted into the boxes; no Docker volumes, so a copy of the folder is
a copy of the team.

## Commands

```text
hi team new <name>              the wizard
hi team up <name>               start the team and keep it up (also after a reboot)
hi team down <name>             stop it; the folder stays
hi team rm <name> [--keep-folder]  stop it and remove it from this host: its service,
                                its boxes, and (unless --keep-folder) its folder
hi team ls                      teams on this host, up or down, spend this month
hi team status <name>           members, running tasks, preview and live versions
hi team logs <name> [<task>] [-f]  the lead's log, or a task's
hi team add <name> <role>       hire a member
hi team rm <name> <role>        drop a member
hi team snapshot <name>         take a snapshot now
hi team snapshots <name>        list them
hi team restore <name> <snapshot>  put data, memory, and the record back (hi_team_snapshots.md)
hi team backups <name>          where snapshots are kept, and offsite
hi team rollback <name> <release>  production's code and data back (hi_team_snapshots.md)
hi team export <name> <file>    the folder as one encrypted file
hi team import <file>           a team from an export, on this or another host
hi team manifest <name>         print the Slack app manifest again
hi team github <name> <owner/repo>  a new private GitHub repository (or an empty one) for the project
hi team remove <name>           delete the team (asks; offers a last export)
```

The lead runs these in its box, through the broker:

```text
hi team task <role> <brief.md|-> [--approval <id> [--new] | --small | --on <task>] [--attach <file>]...
                                                 start a task; --on continues a task's work on its branch
hi team review <task> [<notes.md>]               the reviewer checks a task's latest commit again
hi team wait <task>                              wait for a task and its review rounds, and print how they ended
hi team show <task>                              its state and report, without waiting
hi team tasks                                    the ideas not merged, each as its line of tasks, then the tasks
hi team merge <task> [--approval <id>]           merge it, once a review approved its latest commit (an owner's yes for a new install script)
hi team stop <task>                              stop a task's box
hi team preview [<task>] [--port <n>]            show dev or a task's branch on the preview link
hi team preview stop                             end the preview
hi team preview production                       production's link, and hi's message that keeps it current
hi team versions                                 production's version, what dev has on top, open ideas
hi team release --approval <id> --notes <file>   dev to production as the next version (an owner's yes)
hi team release v2 --approval <id>               production back on v2 (an owner's yes)
hi team discard <task>                           drop an idea that isn't merged
hi team discard dev --approval <id>              dev's code back to production's
```

- A team has two addresses, each with a record in `run/`: the preview
  (dev or a task's branch) and production. Showing a commit copies it to
  `run/app-<slot>-<port>`, runs `make run` or `npm start` in a new app box
  with `$PORT` (8080) published to 127.0.0.1 only, and waits for it to
  answer. The address is a `hi net expose --link --follow run/<slot>.port`
  on the host, for a day: the broker writes the new box's port to that
  file, so the address moves to the new version without changing, and
  then removes the old box. A failed start leaves the old version showing.
  The lead never joins NetBird.
- A preview keeps its address while it has 12 hours left; otherwise it
  gets a new one and the old ends. A preview whose address ended is
  stopped.
- Production's address follows the production branch: a release, and the
  broker's check each minute, show its latest commit, and the broker gives
  it a new address 30 minutes before the old one ends. hi keeps one
  message in the channel with production's current link, editing it when
  the link or the version changes, and a release's announcement links to
  that message. Links in the lead's own messages die with their address,
  and the lead can't edit those, so it points people to hi's message.
- The specs live in the project, `docs/specs`, mounted writable at the
  lead's /team/specs; the rest of the project stays read-only for it. The
  broker commits the lead's changes there when a task starts and before a
  merge, and pushes them: the only commits without a review.
- The project reaches GitHub with the host's `gh` sign-in: the setup can
  create a private repository, and `hi team github` connects an existing
  team. The broker pushes from the host; no GitHub credential enters a box.
- hi rewrites the lead's hi-team skill only when its own text changes; the
  lead's edits stay until then, and are kept as `SKILL.md.lead` when hi
  replaces them. The paragraphs the lead added move to its own
  `hi-team-notes` skill, which hi makes once and never rewrites, and where
  the hi-team skill tells the lead to keep what it learns.
- hi writes the lead's SOUL.md once; on each start it replaces the
  paragraphs an earlier hi wrote that this hi words differently, where they
  are still word for word hi's.
- A review reads the specs on the team's branch, the lead's current ones,
  not the copy on the task's branch, which may be older; a follow-up task
  merges the team's branch first when its specs moved.

- A brief gets a heading before it is saved, so front matter in it can't
  widen the member's box.
- **Everything the people gave reaches the member.** A task gets a folder,
  `tasks/<id>-files`, mounted read-only in its box at `/box/files`
  (`hi agent --files`): the files shared in the thread the task was
  approved in, the files the lead attaches (`--attach <file>`, read in the
  lead's box and sent through the socket, 50 MB each, 150 MB together),
  and for a follow-up (`--on`) the files of the task it continues. The
  brief gets the approving thread in the people's words, with their links
  (up to 24 KB, the latest kept), a list of the files and where each came
  from, and for a follow-up the brief the work started with. A review gets
  the files of the task it reviews. The folder goes when the work is
  merged or discarded.
- A review starts on the task's branch (`hi agent --from`). Its verdict is
  approve only when the reviewer finished and its summary starts with
  "Approve".
- **The broker runs the review rounds, not the lead.** When a member's
  task finishes with commits, the broker starts its review; when the
  review asks for changes, it starts the coder on the same branch (`--on`)
  with the reviewer's summary and follow_ups, then reviews again, up to
  three reviews. Each step names the next (`next` in its task file), saved
  before the step shows as finished. `hi team wait <task>` follows the
  steps to the end and prints them, how it ended, the latest work's report,
  and the last review. `merge`, `stop`, `preview`, and `--on` given the
  first task act on its latest fix. The lead relays no notes: one task, one
  wait, one merge.
- A merge is `git merge --no-ff` into the team's branch, then a push when
  the repository was cloned. The merged tasks' boxes and branches, and
  their reviews', are removed.
- **Continuing work** (v0.75.0; [hi_team_roles.md](hi_team_roles.md#6-continuing-work-telling-a-follow-up-from-a-new-idea)).
  `--on` given any task of an idea continues its latest work, and says
  so; it's refused while any of the idea's work runs, when the idea split
  in two, and for merged, discarded, or review tasks, each with what to do
  instead. A new task whose yes is a later reply in the thread of an
  unmerged idea is refused unless `--new`: more of it is `--on`. Each
  continuation the lead starts is announced in the thread its work was
  agreed in, and its brief carries what the people said there, which the
  reviewer checks the work against. A continuation whose files also
  changed on the team's branch is told to merge it first. A merge that
  conflicts starts that member's "bring it up to date" continuation, which
  is reviewed as usual, and tells the lead to merge it instead.

## The wizard

`hi team new <name>` asks, one step at a time, in a terminal:

1. **Name and purpose.** One sentence; the lead's first memory, and the
   start of its SOUL.md.
2. **Slack app.** hi prints a manifest with the team's name: the bot is
   "payments team", the description is the purpose, and the scopes,
   events, and slash commands are those Hermes needs, from
   `hermes slack manifest` with hi's additions. hi tells the user to
   create the app from it (Create New App → From a manifest) and install
   it, then asks for the bot token and the app token without echo, and
   checks both.
3. **Channel.** The channel ID; hi checks the bot is in it.
4. **People.** Owners and who may ask for work, as Slack members.
5. **Members.** The default roster, with each member's agent and model;
   Enter keeps the defaults.
6. **Model keys.** Through your team's hi server (`hi server ai`), which
   records the team's spend, or keys of its own. Either way the keys stay
   outside the boxes, through the proxy.
7. **Code.** A new repository, which starts as a blank web app from the
   hidden `team-app` template ([hi_bun.md](hi_bun.md)) or, with `empty`,
   with only a README; or an existing one and a token limited to it.
8. **Backups.** The S3-compatible bucket offsite snapshots go to (its
   endpoint, bucket, and keys, without echo), and how long to keep them.
9. **Budget.** Dollars a month; the lead stops starting tasks when it is
   spent and says so in the channel.

Each answer can also come from a flag or a file, for setting up a team
without a terminal (`hi team new payments --from payments.json`).

At the end hi prints what it set up and the one line to start it.

## How it runs

- **The lead's box** runs all the time: Hermes' Slack gateway, with the
  team folder's `lead/` as its home. Slack over Socket Mode is outbound
  only, so the host needs no public address.
- **Member boxes** start for a task and stop after it, as `hi agent` boxes
  with a worktree of `repo/`.
- **A broker on the host** is the lead's only way to start member boxes:
  a socket in the lead's box that accepts "run this brief as this role",
  "status", "report", and "stop". hi checks the role and the budget, then
  starts the box. hi sets no limit on how many boxes run at once; capacity
  is up to whoever runs the host. The lead never holds
  Claude, Codex, or GitHub credentials.
- **The app** runs in boxes of its own on the team's host: preview and
  live, reachable over NetBird, or on a public address when the team allows
  it. It stays on the host until someone running the host decides to move
  it.
- **Staying up.** `hi team up` installs a systemd user unit for the team,
  so it starts at boot and restarts after a crash, as `hi server` does.

## Backups and rollback

Snapshots and restore are built (v0.55.0): see
[hi_team_snapshots.md](hi_team_snapshots.md), which differs from this
section in a few places: the app's data is `app/` at `$HI_DATA_DIR`, a
restore keeps `team.json` as well as `secrets.json`, and restic runs while
the boxes are paused rather than packing an archive first. Rollback is built
(v0.56.0): to a release only, production's code and data without the
lead's memory, approved by an owner's yes rather than a button.

A team has three kinds of state, each kept differently:

| State | Where | How it is kept |
|---|---|---|
| Code and specs | `repo/` (specs in `repo/docs/specs`) | git: every merge is a commit, every release a tag, pushed to the remote |
| The lead's memory | `lead/` (Hermes' SQLite databases, memories, skills), `memory/` (Hindsight's PostgreSQL) | snapshots |
| The team's record | `team.json`, `tasks/`, `channel/`, `run/proxy/network.log` | snapshots |
| The app's data | `app/` | snapshots; this is the people's real work and matters most |

The rest of `run/` (the lead's home, the preview's copy of the code, the
preview's record) is rebuilt by `hi team up` and is left out. So is
`secrets.json`: a snapshot in a bucket must not hold the team's Slack
tokens. A restore asks for them again, as `hi team new` does.

**Snapshots** are taken:

- **before every live deploy**, so each release pairs its code with the
  data and memory as they were (the snapshot is named after the release);
- **every day**, by a systemd user timer next to the team's service, kept
  rolling: 7 daily, 4 weekly, 6 monthly by default;
- **by hand** with `hi team snapshot`.

**Consistency without stopping the team.** Copying the folder while the
team runs can catch a SQLite database mid-write (Hermes' `state.db` keeps a
write-ahead log beside it) or git halfway through a merge, and such a copy
may not open. Stopping the team for each snapshot would take the bot off
Slack and cut off a lead mid-conversation. Instead the team's own service
takes the snapshot, since it holds the moving parts:

1. it takes the broker's lock, so no merge or task start is half done;
2. it pauses the lead's box and the memory server (`podman pause`),
   freezing Hermes and Hindsight's PostgreSQL where they are; the Slack
   connection outlasts a pause of a few seconds;
3. it packs the folder, minus what is left out above, into one archive
   (about a second for a team of 50 MB);
4. it unpauses the box and releases the lock, then copies the archive
   offsite in the background, so nobody waits on the upload.

A frozen copy restores like a power cut, which SQLite and git are built to
recover from. The app's box is paused in the same way, or its database
dumped with its own tool when it has one. Member boxes still working are
left alone: their work is on task branches, and the next snapshot has it.
Copying each database with SQLite's backup API would avoid even the pause,
but would miss any database a later Hermes adds.

Snapshots are encrypted and deduplicated (restic, or an equivalent hi
ships), kept on the host and copied offsite to the S3-compatible bucket chosen
in the wizard. A snapshot that fails, or an upload that fails, is posted in
the channel for the owners and shown in `hi team status`.

Not per commit: commits to `main` are already kept by git, and the app's
data changes on its own schedule, so the snapshots that matter are before
a deploy and daily.

The containers are never snapshotted. They are rebuilt from the base image
and the folder.

**Rollback.** `hi team rollback payments r12` puts back the release's code
and its snapshot of data and memory, and deploys it. `--code-only` and
`--memory-only` put back just one. In Slack the lead may propose a
rollback ("the last release broke the export; roll back to yesterday?"),
and only an owner's button carries it out.

## Security

- Anyone who can post in the channel can steer the team, so only the
  listed people are heard, and nothing the people say can widen what a box
  may reach.
- Boxes reach any site by default, through the proxy, which logs every
  connection: people give the lead links, and members install what they
  need. Exposure is limited by the box: no credentials, only the team's
  folder, and model keys added by the proxy on the way out. A member's
  `network` in team.json narrows it to the dev or locked preset.
- Later, a security advisor: a member, or Hermes' own guard
  (`approvals.mode: smart`, which asks an LLM about risky commands), that
  reviews the proxy's log, the members' commands, and merged changes, and
  tells the owners about anything risky instead of blocking the team.
- Model keys never enter a box; tokens for Slack and the repository are
  scoped to this team only.
- Live deploys, rollbacks, and budget changes need an owner.
- Every task, review, merge, deploy, and rollback is in the team's log.

## Reused and new

Reused: boxes, the proxy and token injection, `hi agent` and its reports
and stats, best-of, bundles, the per-agent models, Hermes' Slack gateway,
memory, cron, and manifest, `hi server ai` for spend, and the systemd
pattern from `hi server`.

New: the team folder, the wizard, the broker, keeping the lead up, the
preview and live app boxes, snapshots and rollback, and export and import.

## First slice

`hi team new`, `up`, `down`, `ls`, `status`, `logs`, and `manifest`: the
lead in one channel, its memory in the folder, and the coder and reviewer
through the broker, merging to the team's branch. Built in v0.37.0; the
wizard asks for purpose, Slack, channel, people, members, and code, and
leaves backups and the budget to later slices. Then the app's preview and
live boxes, then snapshots and rollback, then export and import.
