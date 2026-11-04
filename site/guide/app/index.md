---
title: What a hi app is
description: "What a hi app is: one Slack channel and one app, built by the app's owners with coding agents in a dev box, with a Hermes lead in the channel that helps the people, files what they ask for as tasks, and watches the app."
---

A hi app is one Slack channel, one project, and the agents around it.
The people in the channel use the app and say what they want in plain
language; they may not read code. The app's owners, its developers,
build the app with coding agents and decide what reaches production.

- **The lead** is Hermes Agent, from Nous Research, in the channel. It
  helps people use the app, turns what they ask for into tasks, answers
  how far something is, and watches the app: incidents, jobs, backups. It
  doesn't write code, merge, or release.
- **The dev box** is where the owners build: a long-lived
  [box](/guide/box/) with the project, Claude Code, Codex, and herdr,
  reached over SSH on the app's NetBird network or from the **dev page**
  in a browser. Work leaves it for Stage and dev with `hi dev`.
- **Production** moves only when an owner, a Pro Dev, or a Vibe Dev clicks
  Release on the dev page, as the next version, with a snapshot first.

One app has one channel, one Slack app, and one machine. A new project is a
new app.

## The app's boxes

Dev, Stage, and production are separate boxes, each a container with its
own data, all on the app's one machine:

| Box | What runs there | Its data |
|---|---|---|
| **The lead** | Hermes, in the channel | Its memory, in `lead/` and `memory/` |
| **The dev box** | Where the owners build, with the project, Claude Code, Codex, and herdr, over SSH or from the dev page | Its home, in `dev/` |
| **Stage** | The `dev` branch, or a branch put up with `hi dev preview`, on Stage's link | A fork of production's data, renewed at each release |
| **Production** | The last release, on the app's link | `app/`, which only production writes |

- **Dev** is also the name of a branch: `hi dev ship` takes work from
  the dev box into the `dev` branch, and Stage runs it. Production is the
  `production` branch, and it moves only at a Release.
- **The dev box can't touch production.** Its connection to the app's
  service has no command to release, roll back, restore, or change
  production's settings, and Stage never writes production's data.
- **They share the machine and the app's folder.** A snapshot takes them
  all together, and when the machine is down, so is each box. To move
  the app, the whole folder moves.
- **A release starts a new production box** beside the old one, on the
  same data, and moves the link to it once it answers. See
  [Dev and production](/guide/app/releases/).

Each box gets its own settings with `hi app env <app> --production`,
`--stage`, or `--dev`, and its own memory limit with `hi app limit`:
16g for the dev box and 4g for Stage's and production's, unless the
app sets its own.

[See it in action](/app/): a short demo, then how it works.

{% include diagram.html name="app" %}

The lead's box and the dev box are the boundary: they hold no keys or
sign-ins (hi's proxy adds the model keys on the way out), and the lead
sees only the app's folder, with the project read-only. The dev box's
agents work without asking before commands, but its connection to the
app's service can't release, roll back, restore, or change production's
settings.

## What people do

- **Ask** in the channel, by @-mentioning the lead (`@payments`), or in
  a DM with its bot. Anyone in the channel may ask questions. For its
  contributors, developers, and owners, the lead talks a request
  through and files it as a task for the owners.
- **Try** each change on Stage's link, on their phones too, and say
  whether it works.
- **Send feedback** from the app's helper, which goes to the channel.
  Someone signed in with their own link reports a bug, and hi files it
  as a task.

## What owners do

- **Build** in the dev box, from a task: Start on the dev page gives it
  a branch and an agent, they talk with the agent in the page's chat or
  over SSH, and the agent puts the work on Stage and ships it into dev.
- **Release** dev to production from the dev page, and roll back from
  there when a release goes wrong.
- **Decide** what the lead can't: a second restart in an incident, a rule
  for a job, the helper on or off, production's data and settings.

## In this section

| Page | What it covers |
|---|---|
| [Set up an app](/guide/app/setup/) | What to install, the setup's ten questions, starting and stopping, GitHub, and changing a member |
| [Start from someone else's project](/guide/app/adopt/) | `hi app adopt`: an app on a project found on GitHub, its own copy, a survey in the dev box, and keeping up with upstream |
| [How the app works](/guide/app/how-it-works/) | From a request to production: tasks, the dev box, the dev page, who may do what, and the lead's memory |
| [Build in the dev box](/guide/app/dev/) | The dev box, `hi dev`, the dev page, and the agents' skills |
| [Try the app](/guide/app/try/) | Stage and production on links that open on a phone, and addresses that stay |
| [Apps agents can use](/guide/app/agents/) | The app's MCP server, its page in the docs, `/llms.txt`, and what the lead may do with the app's tools |
| [Help and feedback in the app](/guide/app/helper/) | The button on every page: help from the app's docs, feedback for the channel, and bug reports filed as tasks |
| [Keys and settings](/guide/app/keys/) | The app's model key, sign-ins, Slack tokens, and the app's settings, none of them in Slack or the code |
| [Dev and production](/guide/app/releases/) | Versions, releases from the dev page, and going back |
| [Stage and production data](/guide/app/data/) | Production owns the data; Stage runs on a fork of it. Step by step, with pictures |
| [Reviews from the dev box](/guide/app/reviews/) | A second opinion from the other agent, and a deep review of the whole project |
| [Brainstorms on hard questions](/guide/app/brainstorms/) | Several agents think a question through alone, from the dev box |
| [When something goes wrong](/guide/app/incidents/) | Jobs and incidents, what the lead does on its own, and what the owners decide |
| [The folder and backups](/guide/app/folder/) | Everything the app knows in one folder, snapshots, restore, rollback, and offsite copies |
