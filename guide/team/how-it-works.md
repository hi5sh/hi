---
title: How the team works
description: "From a request in Slack to production: the lead files what people ask for as tasks, the owners build it with coding agents in the dev box and release it from the dev page, and the lead watches the app and remembers the people."
---

## From a request to production

Someone in the channel asks for something: an idea, a suggestion, a bug
report, or feedback from the app (f-12). The lead shapes it into a task
before it files:

1. **It looks first**: at the open tasks, adding to one that is there
   rather than filing it twice; at the app's docs and code, for how it
   works now; and, when it touches something outside the app (a data
   source, a service, a library, a rule), at the web and that thing's own
   docs.
2. **It talks it through** with the person: what it found, in plain
   words, the choices there are, an idea of its own when it has a good
   one, and the two or three questions that decide it.
3. **It files the task when they agree**, and says its number. The
   task is written for the owner's coding agent: what they want and why,
   what it should do, how the app does it now and where in the code,
   what the lead found with its links, and what's still open.

A typo or a bug with clear steps it files at once; most tasks take a
round or two of talk.

The owners pick tasks up in the team's [dev box](/guide/team/dev/), with
Claude Code or Codex. Their agent builds the change on a branch of its
own, puts it on the preview with `hi dev preview`, and takes it into dev
with `hi dev ship`. Production moves only when an owner clicks
**Release** on the dev page.

When someone asks how far something is, the lead answers from the
tasks and the versions (`hi team versions` in its box): filed, started,
in dev to try on the preview, or in production. It doesn't promise when:
the owners decide what comes next.

## What the lead does, and doesn't

The lead is Hermes, in the team's Slack channel. From hi 2.4 it doesn't
build, merge, or release, and starts work in the dev box only when an
owner asks it to. It:

- **helps people use the app**, in the channel and in DMs, from the user
  docs, the specs, and dev's code, which it reads but can't change, and
  files the feedback people send from the app's helper;
- **shapes what people ask for into tasks**: it reads the code, the
  docs, and the web, talks it through, and files it (`hi team task add`
  and `comment` in its box; the owners close them);
- **starts a task** in the dev box when an owner asks ("start on #9"),
  while no other task is in progress there, and answers with the dev
  page's link to follow it (`hi team task start`);
- **answers what's the status of something** from the tasks and the
  versions;
- **watches the system**: incidents, jobs, a new version of a project the
  team [adopted](/guide/team/adopt/), and the backups. It restarts the app
  once per incident on its own and asks the owners about the rest (see
  [When something goes wrong](/guide/team/incidents/));
- **says what's the owners'**: anything that changes the app, its data,
  its settings, or what production runs.

## Who may do what

| Who | What they do |
|---|---|
| Anyone in the channel | Talk with the lead in the channel or a DM; anyone with the app's link sends feedback from its helper |
| Owners | All of that, and build in the dev box and use the dev page: Release, Roll back, set dev back to production, the preview's data to production, the helper, incidents, jobs' rules, and settings |
| An admin on the machine | Change the owners, and roll back, copy data, turn the helper on or off, and set settings with `hi team`, without anyone's yes |

Each action on the dev page is an owner's click, logged with their name
("Anna on the dev page") in the versions and the channel. Nothing in
Slack moves production: the lead has no command that can.

## The members

`team.json` still lists the members: the lead, a coder, and a reviewer.
They no longer get tasks. They decide which sign-ins the team keeps
(Claude, ChatGPT, OpenRouter), which the lead and the dev box use, and
the coder's and reviewer's agents take turns in a
[deep review](/guide/team/reviews/#a-deep-review). See
[Change a member](/guide/team/setup/#change-a-member).

## The lead in the channel

The lead answers when someone @-mentions it (`@payments`). In that
thread, and in a thread the lead started, it answers every reply
without a fresh @, except one that opens by @-mentioning someone else
(`@anna what do you think?`); elsewhere in the channel people talk among
themselves. It still follows everything: hi keeps a log of the channel,
threads and shared files included, in the team's `channel/` folder, and
every 15 minutes the lead reads what's new and keeps what matters in its
memory: a decision made in passing, a link or a video someone shared, who
cares about what. `hi team channel payments --since 2h` shows you the log.

The channel gets new production versions, incidents, what the lead
answers, and what an owner's agent posts with `hi dev say`. Work in
progress stays in the dev box and on the dev page.

## The lead's memory

The lead's long-term memory is [Hindsight](https://github.com/vectorize-io/hindsight):
a memory server that runs beside the lead, keeps the facts, people, and
decisions from every conversation, and recalls the ones that matter before
the lead answers. It runs in a container of its own on the team's private
network, with its database in the team's `memory/` folder. Its model calls
go through the team's proxy like the lead's, so it holds no keys. The
first `hi team up` downloads its image, about 2.6 GB. To do without it, set
`"memory": "off"` in `team.json`; the lead then keeps Hermes' small
built-in notepad only.

The memory is for the people and the conversation. Facts about the app
belong in the project, where the owners' agents read them: the lead puts
what it learns in the task, and the owners put it in the docs.

## Its SOUL.md from before 2.4

`lead/SOUL.md` is the lead's identity. A SOUL.md from before hi 2.4,
which still has the lead handing tasks to members, is replaced by the new
one on the team's next start; hi keeps the old one beside it as
`lead/SOUL.before-2.4.md`, so you can carry over what you wrote in it.

## The project's map

The project has a map for the agents, `docs/map.md`: what it already has
(pages, routes, tables, shared code) and where each lives, the agents'
counterpart to the user docs (see [The app's docs](/guide/team/docs/)).
The dev box's instructions tell its agents to read it before building,
to extend what it lists rather than build something beside it, and to
keep it current in the same work. Why it's built as it is goes in
`docs/decisions/`.
