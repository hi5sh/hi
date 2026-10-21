---
title: A team of agents in Slack
description: "What a hi team is: a virtual team of agents in one Slack channel that builds and runs one project for the people in it. A Hermes lead talks with them in plain language; a coder and a reviewer do the work in boxes."
---

A hi team is a virtual team of agents that lives in one Slack channel and
builds one project for the people in it. The people may not read code: they
say what they want in plain language, try what the team makes, and say
whether it works.

- **The lead** is Hermes Agent, from Nous Research. It
  talks in the channel, agrees with the people what will change and how
  they'll know it works, writes the specs, and keeps the project's memory.
  It doesn't write code.
- **The members** do the work, one task at a time, each in a
  [box](/guide/box/) of its own on a branch: by default a coder (Claude
  Code) and a reviewer (Codex), so the code is checked by an agent other
  than its author.
- **The lead merges** a task only after the reviewer approves its latest
  commit.

One team has one channel, one Slack app, and one machine. A new project is a
new team.

[See it in action](/team/): a short demo, then how it works.

{% include diagram.html name="team" %}

The lead and the members can reach any website, so the lead can read the
links people give it and the members can install what they need. Their
boxes are the boundary: they hold no keys or sign-ins (hi's proxy adds the
model keys on the way out), and they see only the team's folder. To narrow
a member, give it a `network` in `team.json` (see
[Change a member](/guide/team/setup/#change-a-member)).

## What people do

- **Ask** in the channel, by @-mentioning the team (`@payments`), in a
  DM with its bot, or in the chat on the app's own pages.
- **Agree** to the plan: the lead proposes what will change and a short
  list of things to try, and work starts only after someone says yes.
- **Try** each change on the preview link, on their phones too, and say
  whether it works.
- **Release** it: an owner's yes puts dev into production, as the next
  version, with a snapshot first.

## In this section

| Page | What it covers |
|---|---|
| [Set up a team](/guide/team/setup/) | What to install, the setup's seven questions, starting and stopping, GitHub, and changing a member |
| [Start from someone else's project](/guide/team/adopt/) | `hi team adopt`: a team on a project found on GitHub, its own copy, and a first task that makes it run |
| [How the team works](/guide/team/how-it-works/) | From a request to a merge: approvals, reviews, the lead's memory, roles that learn, the project's map, and follow-ups |
| [Try what the team built](/guide/team/app/) | Previews and production on links that open on a phone, and addresses that stay |
| [Help and chat in the app](/guide/team/helper/) | The button on every page: help, feedback, and a chat with the lead |
| [Keys and settings](/guide/team/keys/) | The team's model key, Slack tokens, and the app's settings, none of them in Slack or the code |
| [Dev and production](/guide/team/releases/) | Versions, releases, and going back |
| [Preview and production data](/guide/team/data/) | Production owns the data; the preview runs on a fork of it. Step by step, with pictures |
| [A deep review before a release](/guide/team/reviews/) | A review of the whole project, on an owner's yes with a budget |
| [The folder and backups](/guide/team/folder/) | Everything the team knows in one folder, snapshots, restore, rollback, and offsite copies |
