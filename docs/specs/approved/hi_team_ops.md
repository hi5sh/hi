# `hi team ops`: a team that watches itself, and a channel for its operations

Status: Approved (2026-10-29), the first draft's open questions answered
the same day. Release 1 built in v2.6.29, release 2 in v2.6.31, release 3 in v2.6.32. Replaces most of
[hi_watch.md](hi_watch.md) as releases 3 and 4 land.

Dependencies: [hi_team.md](../approved/hi_team.md) (the wizard, the broker,
`__serve`, the lead), [hi_team_jobs.md](../approved/hi_team_jobs.md)
(incidents), [hi_watch.md](../approved/hi_watch.md) (the rules, bills,
advisories, freeze, which move here), [hi_team_dev_access.md](../approved/hi_team_dev_access.md)
(levels: owners, Pro Devs, Vibe Devs, contributors).

## Goal

Two changes in direction:

1. **The team watches itself, on its own machine.** The rules that the hi
   server runs today on a team's heartbeat (health, network, advisories,
   freeze) run in the team's broker, which already has every log and
   number they read. The only thing a machine can't report is its own
   death; an outside heartbeat service (healthchecks.io and the like)
   does that better than the hi server. hi sends heartbeats at two
   levels, each optional: one per team, and one per machine that runs
   hi's services. The wizard offers both.
2. **Operations get a channel of their own.** A team has its main channel,
   for releases, ideas, and talking about the app with the lead, and
   optionally a second one, for operations and security: incidents,
   warnings, downtime, advisories, keys, access. Those need technical
   know-how and shouldn't fill the channel the vibe coders and creatives
   work in. Without a second channel, everything goes to the main one,
   as today.

## Why

- **The server became every team's dependency.** A team's safety net ran
  somewhere else, needed `hi connect`, a server with a Slack app of its
  own, a watch channel, ntfy, and OpenRouter and NetBird admin keys. A
  team on a machine without a server had no watcher at all (an open
  question in hi_watch.md).
- **The data is on the machine already.** Every heartbeat field is read
  there; the network log, the resolver's log, the bills, the incidents
  are there. The server copied them to judge them.
- **The case the rules are for is a compromised box, not machine.** The
  proxy, the broker and `__serve` are outside every box (hi_watch.md,
  "What it can and can't see"), so a watcher in the broker is as honest
  about a steered lead or a package that phones home as the server's.
- **A dead machine needs no hi to notice.** A heartbeat service is free or
  cheap, runs on other infrastructure, and alerts by email, SMS, push, or
  into a Slack channel through its own integration, which works when the
  team's Slack app is the thing that's down.
- **The main channel is for the people who use and shape the app.** A
  full disk, a KEV advisory in the base image, or a burst of denied hosts
  mean nothing to most of them, and teach them to skim past hi.

## What we give up

Said plainly, so it's a choice:

- **An off-machine copy of the network log.** A machine that is itself
  compromised can now rewrite its logs before anyone reads them. The
  offsite snapshots still carry them, a day late; the ops channel's
  posts are a record the machine's bot can only delete one by one.
- **A watcher the machine can't change.** On a compromised machine the
  local watcher can be silenced. The server's couldn't be, but it judged
  what that same machine sent it, so it was blind from the same moment.
- **Keys made by the server, and revoke and cut off from it**, which
  needed the server to own the team's OpenRouter key and a NetBird admin
  token. A team's OpenRouter key is only the one entered in the wizard
  (or later with `hi team key`), if any. An incident that calls for a
  revoke or a cut off says what to do and where (the key's page at
  OpenRouter, the peer in NetBird), for an admin to do by hand.
- **One view of every team.** `hi server watch` showed all teams' health
  on the server. Each team's ops channel shows its own, and there is no
  view across teams.

## Two channels

`team.json` gets `ops_channel` beside `channel`. Empty means the main
channel takes everything, as today.

| Goes to | What |
|---|---|
| **Main** | releases and their notes; Stage (a preview is up, what's on it); tasks: filed, started, shipped; the helper's feedback and contributors' bug reports; deep reviews and brainstorms; a template's new version (upstream); the lead's ideas and questions about the app; anything people ask the lead there |
| **Ops** | every incident: jobs, workers, production's app stopped and back, OOM kills, disk, snapshots, spend loops, a key used outside hi; sign-ins that stopped working and fallbacks to OpenRouter; key changes; access changes (who is an owner, Pro Dev, Vibe Dev); the dev box's site asks; trust approvals (install scripts, KEV packages); advisories; network rules; freeze and pause; hi's own notices (a new hi feature, a handover); the weekly ops report |

- **Permissions are ops' alone**: who is an owner, Pro Dev, Vibe Dev, or
  contributor, who may use the dev page, the addresses' privacy and
  sign-in (SSO, allowed IPs), the dev box's sites, keys and sign-ins. hi
  posts them only there, and the lead discusses them only there (or in a
  DM), never in main.
- **Downtime is ops'**, like every incident: production stopping, a
  freeze that takes the pages down. Without an ops channel it goes to
  main, as everything does.
- **The lead is in both**, with one personality and two registers: plain
  words in main, technical in ops. An incident wakes it in the ops
  thread, and it answers there. Asked in main about an outage, it says
  what it is in plain words and that the owners have it in ops. Its SOUL.md
  and the incident prompts say which channel is which.
- **Who's in ops** is the owners' choice in Slack. hi recommends a
  private channel, and `hi team status` warns when someone below Pro Dev
  is in it (hi already lists the channel's members for access); it never
  removes anyone.
- **Approvals** are asked where the matter lives: a trust approval or a
  freeze in ops, a release in main. Who may answer is unchanged (owners).
- **DMs and the slash command** (`/hi-<team>`) are unchanged.
- **Existing teams** keep one channel until an owner adds the second.

## The watcher on the machine

The broker runs the rules that hi_watch.md's server ran, every minute,
with no model, each opening at most one incident until it clears. They
are ordinary team incidents (`i-<n>`), posted in ops, and they wake the
lead, which can read them and say what it knows but can't quiet, close,
or change them.

- **Health**: a part not up for 3 checks; the gateway silent (from
  Hermes' state file, as built); `__serve` restarted more than 3 times in
  a day; production restarting more than 3 times in an hour; any OOM
  kill; disk or inodes over 85%, or full within 3 days; no snapshot (or
  offsite copy) in 26 hours; model spend over three times the 7-day
  hourly average and over $5; the key spending more than the proxy
  counted.
- **Network**: the same rules, reading `run/proxy/network.log`,
  `resolve.log` and the member boxes' logs where they are: deny bursts,
  raw IPs, hosts on `watch-hosts.txt` (now in the team's folder, with the
  same starter list), large uploads, `allowlist-added`. A host production
  hadn't looked up in 30 days goes in the weekly ops report.
- **Bills and advisories**: the bill is made at release, as today. The
  broker matches production's bill against OSV once a night and when a
  bill changes, with KEV and EPSS fetched once a day, cached on the
  machine and shared by its teams (the dependency review already asks
  OSV from the machine). Tiers, `hi team advisory … ignore`, VEX, and
  the weekly look at what runs are as before.
- **Freeze and pause** are local: `hi team freeze|unfreeze|pause|unpause
  <team> --why "…"` on the machine, or `/hi-<team> freeze …` by an owner.
  They do what the server's freeze and pause did. The two automatic
  freezes (a KEV advisory in the app's own packages, a burst to a
  watched host) stay, and `hi team watch <team> --auto-freeze off` turns
  them off.

## Heartbeats: when a team or a machine stops

The one thing a machine can't report is that it stopped. hi sends
heartbeats to an outside service that alerts when they stop:
healthchecks.io (hosted, free for 20 checks, or self-hosted), Better
Stack heartbeats, Cronitor, Uptime Kuma's push monitors: anything that
takes a plain URL. hi only sends; how the service alerts, by email, SMS,
or into Slack through its own integration, is set up there by the IT
admins, so an alert still arrives when the team's Slack app is what
stopped. hi suggests the ops channel as the place for it.

Two levels, each optional and independent:

### A team's heartbeat

The wizard asks for it after the channels:

```text
Heartbeat URL for this team, so you hear when it stops [Enter: none]
  Sent every minute by default. Make a check with that period at a
  service such as healthchecks.io, and set there who it alerts.
Every [1m]
```

- `__serve`, the team's service on the machine, sends a GET to the URL at
  that interval (30 seconds to an hour), with no body and nothing about
  the team; the URL is the secret, kept in `secrets.json`. It is sent
  from the team's service, outside its boxes: a box reaches the internet
  only through its proxy, and a box must not be able to send the
  heartbeat for a team that has stopped.
- It stops when the team's service stops, so the service hears of a dead
  machine, a broker in a restart loop, and a team someone took down.
- `hi team down` and `hi update --restart` stop it too. A restart is
  shorter than a period; for a down, hi says in ops and on the command
  line that the check will alert and should be paused at the service.
- The wizard also prints production's public address, to give the same
  service as an HTTP check from outside, which finds a broken certificate
  or DNS record that nothing inside sees.

### The machine's heartbeat

For a machine that runs hi's services all the time: teams, a hi server,
a wall. It says the machine and its systemd are alive, apart from any one
team.

```text
hi heartbeat <url> [--every 5m]     send it from this machine
hi heartbeat                        the URL's service, the last ping, and its answer
hi heartbeat test                   one ping now
hi heartbeat off
```

- A systemd user timer (`hi-heartbeat.timer`), with lingering on, as
  teams' services have, so it runs with nobody logged in. Every 5
  minutes by default, 1 minute to an hour.
- The first `hi team new` on a machine without one asks for it, after
  the team's own; `hi server` setup asks the same. It replaces the hi
  server's dead-man's switch (`hi server watch --deadman`), which was this
  for the server's machine.
- On a machine that runs none of hi's services (a workstation, a laptop)
  it means little, and `hi heartbeat` says so, but sends it if asked.
- Kept in `~/.config/hi/heartbeat.json` (0600).

`hi team status` says when a team has no heartbeat, and `hi heartbeat`
when its machine has none, once a day each: nobody would hear that it
stopped.

## Commands

```text
hi team ops <name>                     the main and ops channels
hi team ops <name> <channel ID>|off    set the ops channel, or send all to main again
hi team heartbeat <name> <url> [--every 1m]|off   the team's heartbeat
hi team heartbeat <name> test          one ping now, and what the service answered
hi heartbeat <url> [--every 5m]|off|test          the machine's heartbeat
hi team watch <name>                   the watcher's view: parts, rules, open incidents
hi team watch <name> --auto-freeze on|off
hi team freeze|unfreeze|pause|unpause <name> --why "…"
hi team bom <name> [version]           as before
hi team advisories <name>              as before
hi team advisory <name> ignore <id> --because "…"
```

`hi team new`'s answers file takes `ops_channel`, `heartbeat_url`,
`heartbeat_every`, and `machine_heartbeat_url`.

## The hi server afterwards

- The server keeps what isn't watching: approvals, compute, data, AI,
  templates, client reports. Its machine's heartbeat is `hi heartbeat`,
  like any machine's.
- Removed: `/v1/watch/heartbeat`, `/v1/watch/bom` and `/v1/watch/key`,
  the rules, the advisory matching, the copies of teams' logs, freeze and
  pause in the heartbeat's answer, the OpenRouter keys it made, revoke,
  rekey, cut off, ntfy pushes, `hi server watch`, `/hi teams` and
  `/hi team freeze…`.
- **Migration**: from this release a team sends no heartbeat. A server on
  the new hi answers the old endpoint with 410 and a line naming
  `hi team watch`; it closes its open watch incidents with a note, and
  keeps `watch/` as it was, read by nothing, for an admin to delete. A
  server on an old hi sees the team stop: the release notes say to
  update the server first.
- **Keys the server made** stay normal OpenRouter keys on the teams that
  have them and keep working; nothing makes new ones (`hi team key …
  --from-server` goes).

## Releases

1. **The ops channel.** `ops_channel`, the wizard step,
   `hi team ops`, the routing in the table above, the lead in both
   channels with its SOUL.md and prompts. Useful on its own, with the
   server's watcher still running.
2. **Heartbeats.** The team's and the machine's, their wizard steps,
   `hi team heartbeat`, `hi heartbeat`, the warnings. Before the server
   stops watching, so there's no week without anyone noticing a dead
   machine.
3. **The watcher on the machine.** The rules, advisories, freeze and pause
   move into the broker; teams stop sending heartbeats; the server
   answers 410.
4. **The server without watch.** Its code and commands removed; its
   machine's heartbeat is `hi heartbeat`.

## Tests

- With an ops channel, an OOM incident, an advisory, a site ask, a key
  change and an access change post in ops; a release, a preview, a
  shipped task and a contributor's report post in main; with none, all
  post in main.
- Production stopping, and coming back, post in ops; with no ops
  channel, in main.
- The lead's gateway allows both channels; an incident's prompt names
  the ops thread.
- `hi team status` warns about a Vibe Dev in ops, and never removes one.
- A team's heartbeat URL gets a GET at its interval while `__serve` runs,
  none after `hi team down`; the URL never appears in a log or a post.
- The machine's timer pings at its interval with no team running;
  `hi heartbeat off` removes the timer; the first `hi team new` asks for
  it only when there is none.
- Each moved rule opens one incident from fixture logs as its server
  version did; a box can't write any file the rules read, except Hermes'
  state file, as before.
- A KEV match in the app's packages freezes the team unless
  auto-freeze is off; `unfreeze` restores everything as before.
- The server answers a heartbeat with 410; open watch incidents close
  with a note.

## Decisions

- **On the machine, not the server.** The rules protect against a
  compromised box, which a broker outside the boxes judges as honestly as
  a server; a compromised machine fooled the server too.
- **A third-party heartbeat for a dead machine.** It is the one check
  that needs outside infrastructure; services built for it do it well,
  alert through more paths than hi, and cost little or nothing.
- **Ops is optional.** A team of two owners doesn't need it; one channel
  must keep working exactly as today.
- **Two heartbeats.** A team's says that team runs; the machine's says
  the machine does, for the server and for teams that are down on
  purpose. Either alone leaves a gap.
- **hi sends, the service alerts.** Who hears of a stop, and how, is the
  IT admins' setup at the service, not hi's.
- **No model decides**, as before.

## Open questions

- **Heartbeat bodies.** healthchecks.io accepts a body (shown in its log)
  and `/fail` for a known failure. hi sends nothing: should a team's
  heartbeat go to `/fail` while an incident is open, or is that the ops
  channel's job?

## Later: `hi log`

The same local engine, for anything on a machine that isn't a team: wrap
a program (`hi log run <name> -- cmd`), systemd's `OnFailure=`, check-ins
(`hi log ping`), a one-minute timer reading the journal for OOM kills,
GPU resets, reboots and full disks, posting to a channel. A spec of its
own once this is built.

## Changes to approved specs, on approval

- **hi_watch.md**: Status becomes "Superseded by hi_team_ops.md
  (releases 3 and 4)"; its rules, bills and advisories are referenced
  from here; its As built stays as the record.
- **hi_team.md**: `ops_channel` and `heartbeat_url` in team.json and the
  wizard; the lead in two channels; `hi team ops`,
  `hi team heartbeat`.
- **hi_team_jobs.md**: incidents post in the ops channel when there is
  one; production's stop adds the line in main.
- **hi_team_dev_access.md**: site asks and access changes post in ops;
  `hi team status` warns about members below Pro Dev in ops.
- **hi_server.md**: watch removed; its machine's heartbeat is
  `hi heartbeat`; the server makes no OpenRouter keys.
- **hi_team_keys.md**: `--from-server` removed; a team's key is the one
  entered in the wizard or with `hi team key`.
- **docs/roadmap.md**: the four releases, as planned work.

## As built (release 1, v2.6.29)

Code: `team_ops.go`, and the posts it moved.

- `team.json` has `ops_channel`; the wizard asks for it after the main
  channel (Enter: none), checks the app is in it, and refuses the main
  channel's own ID. `hi team ops <team> <ID>|off` sets it later, checked
  in Slack, and restarts a running team so the lead hears it; the app
  stays up. (`hi team channel` was taken: it is the lead's command for
  the channel's log.)
- **Ops gets**: incidents (`openIncident` and everything in their
  threads), hi's notices about jobs and workers, sign-ins that stopped
  and came back, the snapshots' warnings, access changes, the addresses'
  privacy changes from the dev page, the watch's reports, key and freeze
  notices from the server, the dev box's site asks, the snapshots'
  warnings, and production getting a copy of the preview's data.
  Everything else stays in main; dev set back to an earlier version stays
  there, as part of releasing.
- **Permissions only in ops** (added the same day): no post of hi's about
  levels, access, addresses, sites, or keys goes to main, and the lead's
  skill says it discusses them only in ops or a DM.
- **An incident remembers its channel** (`channel`, empty for main): one
  opened in a work's thread stays in that thread, in main, and incidents
  from before the ops channel stay where they were posted.
- **The lead**: `SLACK_ALLOWED_CHANNELS` lists both, the home channel
  stays main, and the hi-team skill gains "Two channels", which says what
  each is for and to keep technical detail out of main. SOUL.md, the
  owners' own, is unchanged.
- **Logs**: the broker logs the ops channel too, in `channel/ops/`, so an
  incident's reminder sees the owners' answers there and a task filed
  from an ops thread gets that thread's files. The lead's channel-watch
  job still reads only the main channel's log.
- Not built yet: `hi team status` warning about members below Pro Dev in
  the ops channel.
- v2.6.31: an owner's answer to a site ask (from the dev page or
  `hi team dev <team> allow`) is said in the ask's thread, where hi posted
  it: the ask keeps its channel and thread.

## As built (release 2, v2.6.31)

Code: `heartbeat.go` (the ping, the machine's), `team_heartbeat.go` (the
team's, the wizard's step).

- **The ping** is a GET with a 10-second timeout; a 2xx is a success. Its
  errors name the service's host and never the URL, which is the secret,
  and nothing prints or logs the URL.
- **A team's**: the URL in `secrets.json` (`heartbeat_url`), the interval
  in `team.json` (`heartbeat_every`, 30s to 1h, empty for 1m). The
  broker's loop reads both before each beat, so `hi team heartbeat`
  changes a running team without a restart. The last beat and its error
  are in `run/heartbeat.json`, and `hi team status` shows the host, the
  interval, and the last beat, or that there is none. `hi team down`
  says on the command line and in the ops channel that the check will
  alert. Setting one pings once first, and refuses a URL whose first
  ping fails.
- **The machine's**: `~/.config/hi/heartbeat.json` (0600) with the URL,
  the interval (1m to 1h, 5m by default), and the last ping;
  `hi-heartbeat.timer` and `.service` in the user's systemd units, the
  timer with `OnActiveSec=1min` and `OnUnitActiveSec=<interval>`
  (measured on aiw9: the first ping at set, then one a minute). It warns
  without lingering, and says it means little on a machine with no team
  or hi server. `hi heartbeat off` removes the units and the file.
- **The wizard** has a tenth step, Heartbeats: the team's URL (checked
  with a ping) and interval, then the machine's when it has none. The
  answers file takes `heartbeat_url`, `heartbeat_every`, and
  `machine_heartbeat_url`; a machine's existing heartbeat is never
  replaced from there.
- Not built: `hi server` setup asking for the machine's heartbeat; the
  server keeps its own dead-man's switch until release 4.

## As built (release 3, v2.6.32)

Code: `team_watch_local.go`; the server's rules, unchanged, in
`server_watch.go` (`beatLocked`, split from the server's heartbeat),
`server_watch_network.go`, and `server_advisories.go` (`matchTeam`,
split likewise).

- **The same rules, run in the broker.** Each minute `__serve` makes the
  heartbeat it made before and hands it to the team's own copy of the
  server's watcher, in the team's folder (`watch/`: `state.json`,
  `config.json`, `watch-hosts.txt`, the bills it matched, the advisory
  caches). Nothing goes to a hi server. The watcher keeps no copy of the
  logs it reads (`local`), only how far it has read them.
- **Every incident is told**, not only those the server told the team:
  each opens a team incident (`watch`) in the ops channel, and closes
  with the watcher's. A key used outside hi doesn't wake the lead; an
  exploited advisory calls the owners by name in its thread.
- **Advisories** are matched in the background once a day and after a
  release, from the team's machine (OSV's API, KEV, EPSS, as the server
  did), with their caches per team.
- **Freeze and pause** from the machine: `hi team freeze|unfreeze|pause|
  unpause <team> --why "…"` writes `run/watch-want.json`, which the
  broker takes at its next beat and carries out as before. Freeze and
  pause need a why. `hi team watch <team> --auto-freeze on|off` writes
  the watcher's `config.json`, read at each beat. Their messages and the
  lead's skill name the machine, not the server. `/hi-<team> freeze` in
  Slack isn't built.
- **Taking over**: the first check after an update closes the incidents
  the server had told the team ("this opens again here if it still
  holds"), and the watcher opens what still holds. The network logs are
  read from where the server had them.
- **The server** answers a heartbeat with 410 and closes what it had open
  for that team. `hi team key … --from-server` says the server no longer
  makes keys; revoke, rekey, and cut off can no longer reach a team, and
  their code goes in release 4. `hi team up` warns about a team with no
  heartbeat instead of one with no hi server.

