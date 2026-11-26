---
title: Work from your own machine
description: "Developers clone the app's GitHub or GitLab project, work in their own editor with their own coding agent, and push a branch. hi finds it within 2 minutes, and the dev page tries it on Stage, adds it to the next version, and publishes it."
---

Developers don't have to use the [dev box](/guide/app/dev/). You can
clone the app's project from GitHub or GitLab, work on your own laptop
with your own editor and coding agent, and push a branch. hi handles
that branch the way it handles work from the dev box:

1. The branch shows on the dev page within 2 minutes.
2. **Try** puts it on Stage, on a copy of production's data.
3. **Add to Preview** puts it in the next version.
4. **Publish** sends the next version to production.

The one rule: **push branches, and let hi merge them.** hi owns the
app's branch (dev, `main` unless the app was set up with another) and
`production`. It pushes both to GitHub or GitLab itself.

## How the branches fit

| Branch | Who moves it | What it is |
|---|---|---|
| `main` | hi, when someone clicks **Add to Preview** | Dev: the next version, which Stage shows |
| `production` | hi, when someone clicks **Publish** | What people use, tagged `release-1`, `release-2`, and so on |
| Your branches | You, with `git push` | Work in progress. The dev page lists them |

```text
your laptop ── git push ──▶ GitLab: better-chart
                                  │  hi looks every 2 minutes
                                  ▼
dev page, Publish:  Try ─────────────▶ Stage shows only better-chart
                    Add to Preview ──▶ main (dev) ──▶ Stage shows the next version
                    Publish ─────────▶ production
```

## Before you start

You do these once.

1. **Get access to the project** on GitHub or GitLab from its
   maintainers. hi doesn't give that access. You only need to push your
   own branches: on GitLab that is the Developer role.
2. **Get a place on the dev page.** An owner makes you a developer in
   the dev page's **Users** panel (or with `hi app access`; see
   [Owners, developers, and contributors](/guide/app/dev/#owners-developers-and-contributors)).
   Then `/hi-<app> dev` in the app's Slack channel sends you the link.
   You can push without this, but then an owner or a developer must
   click Try and Add for you.
3. **Clone the project and run it.** Its README says how. A project
   that hi set up has `make run`, which listens on `$PORT` and keeps
   its data in `$HI_DATA_DIR`. Locally, set both yourself:

   ```bash
   git clone https://gitlab.com/acme/billing.git && cd billing
   PORT=3000 HI_DATA_DIR=./local-data make run
   ```

   The app's keys and settings stay in hi. They are not in the
   repository, and hi doesn't hand them out. Locally, use your own test
   values, in a file that git ignores (such as `.env`).

## Step by step

1. **Start from dev.** Start a new branch from the latest `main` on
   GitLab, not from an old local copy:

   ```bash
   git fetch origin
   git switch -c better-chart origin/main
   ```

2. **Build and commit.** Run the tests and the checks, as you would
   anywhere.

3. **Push the branch.**

   ```bash
   git push -u origin better-chart
   ```

   A branch name may have letters, digits, `.`, `_`, `-`, and `/`, up
   to 100 characters. hi skips other names, and also `production` and
   names that start with `box/` or `pushed/`.

4. **Open a merge request, if you like** (a pull request on GitHub), so
   teammates can review the code. Do not merge it on GitLab: step 7
   does that, and GitLab then shows it as merged.

5. **Find your branch on the dev page.** Open **Publish**, then **Not
   on the preview yet**. Your branch is listed under "Pushed to GitLab".
   hi looks every 2 minutes; **Check now** looks at once. The row shows:
   - the branch name
   - who wrote its last commit
   - how many commits dev doesn't have yet
   - how many files it changes

   Hover over the row to see the commit message and the files.

6. **Try it on Stage.** **Try** puts only your branch on Stage. When the
   branch is new there, Stage first gets a fresh copy of production's
   data. Stage is one address for the whole team: while your branch is
   on it, nobody sees the next version there. **Show the next version**,
   under Preview in the same panel, puts that back.

   If you push again after Try, the row says "On the preview as it was
   before the last push". Click **Try again** to see the new commits.

7. **Add it to the next version.** **Add to Preview** does all of this:
   - merges your branch into `main`, with the message "From
     better-chart, pushed to GitLab by Ada"
   - runs the same checks as for work from the dev box (see
     [What hi checks](#what-hi-checks))
   - pushes `main` to GitLab
   - puts the next version on Stage

   Your merge request now shows as merged. The release notes say
   "pushed to GitLab by Ada".

8. **Publish.** When the next version is good, an owner or a developer
   clicks **Publish v4**. hi takes a snapshot, moves `production`, and
   tags the release. See [Dev and production](/guide/app/releases/).

9. **Tidy up.** Delete the branch on GitLab, and start the next one from
   the new `main`:

   ```bash
   git push origin --delete better-chart
   git fetch origin && git switch -c next-thing origin/main
   ```

If you push more commits to a branch after it was added, it shows again
with only the new commits. **Add to Preview** adds them.

## When it conflicts with dev

If someone else's work changed the same lines, **Add to Preview** stops.
A dialog names the files and offers these choices:

- **Rebase it yourself.** This is usually the fastest:

  ```bash
  git fetch origin
  git rebase origin/main          # fix the conflicts, run the tests
  git push --force-with-lease
  ```

  Within 2 minutes the row shows the new commits. Click **Add to
  Preview** again.
- **Ask an agent to combine them.** hi starts new work in the dev box.
  Its agent merges your branch with dev, makes both changes work, and
  adds the result. If both changes can't stay, it asks you which to keep.
- **Take out the last change on the preview** and add yours instead.
  The change you take out stays where it was, so you can add it again
  later.
- **Ask for help in Slack.** hi posts the details in the app's channel.

## What hi checks

**Add to Preview** checks pushed work the same way as work from the dev
box:

- **New `trustedDependencies` are refused.** Those packages' install
  scripts would run on every machine that installs the app, so an admin
  of the machine merges that change.
- **Packages with an advisory known to be exploited** (CISA KEV) are
  refused. Use a fixed version instead.
- **Dependency changes are listed** with the work.

hi doesn't run your CI and doesn't wait for it. Look at the pipeline on
GitLab before you click Add.

Pushing to GitLab gives you no rights in hi. Only the app's owners and
developers can add work and publish, on the dev page.

## Settings and data

- **A new setting:** if your branch needs a key or setting the app
  doesn't have yet, ask an owner to add it in the dev page's **Keys**,
  **Env variables**, before you click Try. Never commit keys.
- **Stage's data is a copy:** Stage runs on a fork of production's data,
  and nothing done on Stage reaches production. A branch that changes
  the database's shape runs on that copy first, so Try is the place to
  test a migration. **Publish** then runs it on production's data. See
  [Stage and production data](/guide/app/data/).

## Pushes hi doesn't take as they are

- **Straight to `main`.** hi lists it as the branch `main` ("Pushed
  straight to main by …"), and it is added like any other branch. hi
  never force-pushes `main`, so nothing is lost. Until it is added, hi
  can't push other work to GitLab: the next Add says "git push failed …
  add that branch too". The same happens when someone merges a merge
  request on GitLab.
- **To `production`.** hi never takes it. The app's ops channel hears
  about it once, and the next Publish overwrites it. Production moves
  only by Publish.

## Protect main and production

Protect both branches, so these mistakes can't happen. On GitLab, open
**Settings**, **Repository**, **Protected branches**:

| Branch | Allowed to merge | Allowed to push and merge | Allowed to force push |
|---|---|---|---|
| `main` | Maintainers | Maintainers | Off |
| `production` | Maintainers | Maintainers | **On**: Publish and rollbacks force-push it |

hi pushes as the account that `glab` is signed in as on the app's
machine. That account must be a Maintainer. Give the people who push
branches the Developer role.

On GitHub, add a ruleset for `main` and `production` that restricts
updates. Put hi's account (the one `gh` is signed in as on the machine)
on its bypass list.

## Carry on in the dev box

A branch pushed from a laptop can be continued in the dev box, by you or
by someone else, with an agent:

1. Open the chat's **Start something new** (**+ New** in Workspaces).
   Under the text, the card **Pushed to GitLab** lists the newest pushed
   branches that dev doesn't have yet, with who pushed them and when.
2. Pick the agent at the bottom, and click **Work on it** beside the
   branch. hi makes a workspace whose branch has the same name and
   starts where the pushed branch is.
3. The agent reads the branch first and says what it does and whether
   it looks ready. It changes nothing until you say what to do.
4. When the work is done, **Add to Preview** on the workspace adds it,
   as for any workspace. The pushed branch's row then goes, because dev
   has its commits.

If a workspace on that branch exists already, the button says **Open**.

A push straight to `main` is in the card too. A workspace can't have
`main` as its branch, so **Work on it** names it after the push, such as
`main-2b3f2cd`. Adding that workspace adds the push, and hi can push
`main` to GitLab again.

The workspace's commits don't go back to the branch on GitLab. If the
author keeps working on their laptop after that, they start again from
`origin/main` (`git fetch origin && git rebase origin/main`).

The agents see pushed branches without the card too:

- `hi dev branches` lists them.
- `git fetch origin` brings them into the box's clone as
  `origin/pushed/<branch>`.
- You can ask any agent to "review better-chart" or "make the tests
  pass on better-chart".

## Commands

| Command | What it does |
|---|---|
| `hi app branches <app> [--fetch]` | On the machine: the branches pushed to the app's project. `--fetch` looks at once |
| `hi dev branches [--fetch]` | In the dev box: the same list |
| `hi dev preview --pushed <branch>` | In the dev box: a pushed branch on Stage, as it was pushed |
| `hi dev ship --pushed <branch> [-m <title>]` | In the dev box: a pushed branch into dev, as its author's work |

## When something is wrong

- **My branch doesn't show.** Wait 2 minutes, or click **Check now**.
  Check the name ([step 3](#step-by-step)), and check that dev doesn't
  already have its commits.
- **"The last check failed".** The app's machine can't read the project
  on GitLab, usually because its `glab` (or `gh`) sign-in expired. Ask
  an admin of the machine to sign it in again, as the account hi runs
  as: `glab auth login` (or `gh auth login`).
- **"git push failed".** Someone pushed straight to `main`, or merged a
  merge request on GitLab. Add the branch `main` from the list, and hi
  can push again.
