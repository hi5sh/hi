---
title: A team of agents in Slack
description: "What a hi team is: one Slack channel and one app, built by the team's owners with coding agents in a dev box, with a Hermes lead in the channel that helps the people, files what they ask for as tasks, and watches the app."
---

A hi team is one Slack channel, one project, and the agents around it.
The people in the channel use the app and say what they want in plain
language; they may not read code. The team's owners, its developers,
build the app with coding agents and decide what reaches production.

- **The lead** is Hermes Agent, from Nous Research, in the channel. It
  helps people use the app, turns what they ask for into tasks, answers
  how far something is, and watches the app: incidents, jobs, backups. It
  doesn't write code, merge, or release.
- **The dev box** is where the owners build: a long-lived
  [box](/guide/box/) with the project, Claude Code, Codex, and herdr,
  reached over SSH on the team's NetBird network or from the **dev page**
  in a browser. Work leaves it for the preview and dev with `hi dev`.
- **Production** moves only when an owner clicks Release on the dev page,
  as the next version, with a snapshot first.

One team has one channel, one Slack app, and one machine. A new project is a
new team.

[See it in action](/team/): a short demo, then how it works.

{% include diagram.html name="team" %}

The lead's box and the dev box are the boundary: they hold no keys or
sign-ins (hi's proxy adds the model keys on the way out), and the lead
sees only the team's folder, with the project read-only. The dev box's
agents work without asking before commands, but its connection to the
team's service can't release, roll back, restore, or change production's
settings.

## What people do

- **Ask** in the channel, by @-mentioning the team (`@payments`), or in
  a DM with its bot. Anyone in the channel may. The lead talks it
  through and files it as a task for the owners.
- **Try** each change on the preview link, on their phones too, and say
  whether it works.
- **Send feedback** from the app's helper, which the lead files as a
  task.

## What owners do

- **Build** in the dev box, from a task: Start on the dev page gives it
  a branch and an agent, they talk with the agent in the page's chat or
  over SSH, and the agent puts the work on the preview and ships it into
  dev.
- **Release** dev to production from the dev page, and roll back from
  there when a release goes wrong.
- **Decide** what the lead can't: a second restart in an incident, a rule
  for a job, the helper on or off, production's data and settings.

## In this section

| Page | What it covers |
|---|---|
| [Set up a team](/guide/team/setup/) | What to install, the setup's seven questions, starting and stopping, GitHub, and changing a member |
| [Start from someone else's project](/guide/team/adopt/) | `hi team adopt`: a team on a project found on GitHub, its own copy, a survey in the dev box, and keeping up with upstream |
| [How the team works](/guide/team/how-it-works/) | From a request to production: tasks, the dev box, the dev page, who may do what, and the lead's memory |
| [Build in the dev box](/guide/team/dev/) | The dev box, `hi dev`, the dev page, and the agents' skills |
| [Try what the team built](/guide/team/app/) | Previews and production on links that open on a phone, and addresses that stay |
| [Apps agents can use](/guide/team/agents/) | The app's MCP server, its page in the docs, `/llms.txt`, and what the lead may do with the app's tools |
| [Help and feedback in the app](/guide/team/helper/) | The button on every page: help from the app's docs, and feedback the lead files as a task |
| [Keys and settings](/guide/team/keys/) | The team's model key, sign-ins, Slack tokens, and the app's settings, none of them in Slack or the code |
| [Dev and production](/guide/team/releases/) | Versions, releases from the dev page, and going back |
| [Preview and production data](/guide/team/data/) | Production owns the data; the preview runs on a fork of it. Step by step, with pictures |
| [Reviews from the dev box](/guide/team/reviews/) | A second opinion from the other agent, and a deep review of the whole project |
| [Brainstorms on hard questions](/guide/team/brainstorms/) | Several agents think a question through alone, from the dev box |
| [When something goes wrong](/guide/team/incidents/) | Jobs and incidents, what the lead does on its own, and what the owners decide |
| [The folder and backups](/guide/team/folder/) | Everything the team knows in one folder, snapshots, restore, rollback, and offsite copies |
