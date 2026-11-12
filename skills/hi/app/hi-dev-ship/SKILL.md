---
name: hi-dev-ship
description: Put work from this hi app's dev box on the app's preview, and ship it into dev. Use when the user wants to see a branch running with real data, try a change on the preview, share it with the team, merge or ship work, or asks how work gets to production. Covers hi dev preview and hi dev ship.
---

# Preview and ship from the dev box

You are in a hi app's **dev box**. The project is a clone in `/box/repo`;
each piece of work has its own branch and worktree (herdr makes them:
`herdr worktree create --cwd /box/repo --branch <name>`). Your commits stay
in this clone until you ship them. There is no `git push`: the app's
repository is `origin`, read-only.

## How work moves

1. **Build and commit** on the work's branch. Run the tests first.
2. **Preview:** `hi dev preview` (the current branch) or `hi dev preview <branch>`.
   It puts the branch on the app's preview address, and prints the link.
   When it is new work there, the preview first gets a fresh copy of
   production's data, so the change is tried on real data. Give the user
   the link and say what to look at. The preview is one address: while it
   shows the branch it doesn't show dev, and trying it changes nothing in
   dev. `hi dev preview --dev` puts dev back on it, as Show dev on the
   dev page does.
3. **Ship:** `hi dev ship -m "<what it does, in a line the team understands>"`.
   It merges the branch into dev, records it as work, and puts dev on the
   preview. Ship only when the user says so.
4. **Production** moves only when an owner or a developer releases dev,
   on the dev page. You can't release,
   roll back, or restore from here, and shouldn't try another way.

After a ship, `git fetch origin` brings dev into the clone; start the next
work from `origin/<the app's branch>`.

## When the work is done

Once a branch is shipped and the user is happy with it, it's finished on
the dev page: **Done ✓** beside its workspace removes the worktree, the
herdr workspace with its agent (you), and the branch, and closes the
task its number names. Tell the user that when you've shipped; don't
remove your own worktree. Work dev hasn't (commits not shipped, changes
not committed) shows there, and only **Discard** drops it.

## When something goes wrong

- **Is it working on Stage or production?** Look, don't guess, and don't
  curl their addresses (they answer only the sign-in page):
  `hi dev jobs` (the app's jobs and workers: state, progress, last
  error), `hi dev logs [stage|production] --since 1h --grep <text>` (the
  app's own output, its settings masked), and `hi dev tools production`
  then `hi dev tools production <tool> '<json>'` (the app's MCP tools: on
  production only those that read). Production is the default; say
  `stage` for the preview. All of it only reads.

- **Conflicts:** `hi dev ship` says which files. Rebase on dev
  (`git fetch origin && git rebase origin/<branch>`), fix, test, ship again.
- **"adds ... to trustedDependencies"** or **"advisory known to be
  exploited":** the box can't ship that. Use another package or a fixed
  version, or tell the user an admin of the machine has to merge it.
- **"the app's service isn't answering":** it is restarting; try again in
  a minute.
- **A site the box can't reach** (a 403 from its proxy, "not allowed"): ask
  the app's owners for it right away, saying what it's for in a line, and
  wait for their answer:
  `hi dev allow query1.finance.yahoo.com --why "daily price history for the game" --wait`.
  They see it on the dev page and in the channel, and allow it with a tap;
  the box reaches it at once. If they say no, or don't answer in time, tell
  the user and carry on without it; never work around the proxy. Sites
  inside the network (the machine, its LAN, NetBird) are never allowed.
  The app's own Stage, the organisation's sites, and the usual downloads
  (packages, Playwright's browsers, Debian's and Ubuntu's packages) need
  no ask; the app's production does. Ask for the sites a piece of work
  needs together, one after another: they share one post. hi checks each
  site first and may allow it at once ("Allowed by hi's check for 7
  days"): then carry on, no need to wait. Say plainly what the site is
  for; the owners read it.
- **A database on the internet** that speaks TLS on MongoDB's port
  (27017), such as MongoDB Atlas: ask for its name the same way (the host
  in `mongodb+srv://<host>/`). Once it's allowed, the box's DNS gives it
  and the driver connects as it would anywhere; the shards its SRV record
  names come with it.
