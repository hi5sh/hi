# Branches pushed to the app's repository: work from people's own machines

Status: Approved (2026-11-26). Release 1 built in v2.6.260. Drafted after a developer on an adopted app
worked on their own laptop with their own coding agent, pushed a branch to
the app's GitLab project, and found hi never saw it: the dev box's agent
said "GitLab needs a sign-in", which was wrong.

Dependencies: [hi_app.md](hi_app.md) (dev and production, the
broker's merge and release), [hi_app_dev.md](hi_app_dev.md)
(`hi dev preview` and `ship`, the dev page, the dev box's clone),
[hi_app_adopt.md](hi_app_adopt.md) (upstream),
[hi_app_data_fork.md](hi_app_data_fork.md) (the preview's data).

## Goal

Professional developers work where they like: their own laptop, IDE and
agent. They clone the app's GitHub or GitLab project, make a branch, and
push. hi takes it from there, as it does work from the dev box:

1. The branch shows up on the dev page within two minutes, with who
   pushed it and what it changes.
2. **Try on Preview** puts it on Stage, with a fork of production's data.
3. **Add to Preview** merges it into dev, with the same checks as the dev
   box's work. A merge request for it on GitLab or a pull request on
   GitHub then shows as merged by itself.
4. **Publish** takes dev to production, as today.

Nobody needs the dev box, a hi command, or a new sign-in for this.

## What there is today, and what's missing

- hi **pushes** the team's repository (`repo/`) to its remote after each
  merge and each release. It never **fetches** from it. So a branch a
  person pushes there never reaches `repo/`, and the dev box, whose
  `origin` is `repo/` (`/team/origin`), can't see it either.
- The dev box's proxy refuses github.com and gitlab.com, on purpose: the
  box has no forge credentials. So the agent can't fetch the branch
  itself.
- A bug: the daily fetch of an adopted project's **upstream** clears git's
  credential helpers and puts back only `gh`'s. A private upstream on
  GitLab always fails with "could not read Username".

## How

### Fetching (the broker, on the machine)

- Every 2 minutes, the broker runs `git ls-remote` on the team's remote,
  with the machine's `gh` or `glab` sign-in (the one it pushes with). When
  a branch changed, it fetches. As with upstream: no hooks, no LFS
  smudge, no submodules, nothing from the project runs.
- Each branch other than dev's and `production` is kept in `repo/` as
  `refs/heads/pushed/<branch>`. In the dev box, `git fetch origin` brings
  them in as `origin/pushed/<branch>`, so an agent can read a branch,
  review it, or rebase it. A branch deleted on the remote goes from
  `pushed/` at the next fetch, unless it is on Stage.
- **Dev's branch pushed by a person** (the remote's `main` has commits
  dev hasn't): it shows as one more branch, `main`, "pushed straight to
  main". Adding it merges it, and the push after is a fast-forward of the
  remote. hi never force-pushes dev, so nothing is lost either way.
- **`production` pushed by a person:** hi doesn't take it. It posts in the
  ops channel once, and the next release force-pushes `production`, as
  today. The guide says to protect `production` (and `main`) on the forge
  so only hi's account pushes there.
- `hi app branches <app>` on the machine and `hi dev branches` in the box
  list them; `--fetch` fetches first.

### On the dev page

- The work list has **From GitLab** (or **From GitHub**): one row per
  pushed branch, with the branch's name, its last author, when it was
  pushed, how many commits dev hasn't, and its files. Where an open merge
  request or pull request has the branch, its title and a link.
- The row has **Try on Preview** and **Add to Preview**, as a workspace
  has. The states are the same: "Not on the preview yet", "On the
  preview ✓", "Published ✓".
- When it conflicts with dev, the Stuck dialog says so, as for a
  workspace, and offers:
  - **Ask an agent to combine them:** a new workspace on
    `origin/pushed/<branch>`; its agent rebases onto dev, resolves the
    conflicts, and adds it.
  - **Ask the author:** a Slack DM to the person who pushed (found by the
    commit's email, as the dev page finds people), with the files and the
    command `git fetch && git rebase origin/main`, then push again.
- A new pushed branch is a line in **Needs you**. It isn't posted in the
  channel.

### What it records

- Adding it is a task like the dev box's: `Kind: task`, `Agent: "pushed
  by <author>"`, `Branch: "pushed/<branch>"`, the merge commit. The
  release notes say "pushed to GitLab by <author>". The merge message says
  "From <branch>, pushed to <remote host> by <author>."
- It goes through the same checks as `hi dev ship`: new
  `trustedDependencies` and packages with an exploited advisory are
  refused, and the dependency changes are listed.
- Who may add it: the people who may add a workspace's work to the
  preview today. Pushing to the forge gives no right in hi.

### The upstream fix

The upstream fetch uses the same sign-in as the push: `gh`'s for a GitHub
host, `glab`'s for GitLab (and `GITLAB_HOST` for one's own GitLab).

## Releases

1. **Fetch and add** (one release): the upstream fix; the 2-minute fetch
   into `pushed/`; `hi app branches`, `hi dev branches`; the dev page's
   From GitLab rows with Try on Preview and Add to Preview; the conflict
   choice "ask an agent"; the record and the checks; the ops post for a
   pushed `production`. The guide gets a section for developers who work
   on their own machine.
2. **Forge links:** the merge request or pull request on each row; Ask
   the author; Needs you. hi checks that `main` and `production` are
   protected, and says how to protect them when they aren't.

## Not in it

- **A webhook from the forge.** Two minutes of polling is enough. A
  webhook needs an address the forge can reach, which most machines
  don't have.
- **Merging on the forge.** Merging a merge request on GitLab pushes to
  `main` itself: that is "dev's branch pushed by a person" above, and
  works, but skips hi's checks until it is added. The guide tells people
  to leave merging to hi.
- **Keys for people to push with.** People push with their own forge
  accounts. hi's account stays the machine's.

## Decisions (2026-11-26)

1. A direct push to `main` is taken as one more branch, `main`, added
   like the others. hi doesn't refuse it.
2. A pushed `production` is ignored: one post in the ops channel, and the
   next release force-pushes it.
3. A new pushed branch shows only on the dev page, not in the channel.
