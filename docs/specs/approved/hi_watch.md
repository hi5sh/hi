# `hi watch`: a team watched from outside

Status: Approved (2026-10-22). Release 1 built in v1.4.0.

Dependencies: [hi_server.md](../approved/hi_server.md) (the server, its
Slack app, client reports, the audit log, the device key),
[hi_team.md](../approved/hi_team.md) (the broker, `__serve` under systemd),
[hi_team_jobs.md](../approved/hi_team_jobs.md) (incidents, which this
reuses), [hi_box.md](../approved/hi_box.md) (the egress proxy and its
network log), [hi_team_snapshots.md](../approved/hi_team_snapshots.md)
(releases and their snapshots), [hi_bun.md](../approved/hi_bun.md) (the
dependency review), [hi_net.md](../approved/hi_net.md) (NetBird).

## Goal

A team runs for weeks on one machine with nobody looking. hi already
notices a lot from inside: a job that stalls, production's app that
stops, a member's task that fails. All of that is noticed by the broker,
on the team's machine, in the team's own process. Three kinds of trouble
it can't see:

- **The machine itself.** A power cut, a full disk, a lost network, a
  broker in a restart loop. Nothing on the machine says so, because the
  thing that would say it is what stopped.
- **Its own compromise.** The lead reads any page people link (its
  allowlist is `*`), and an app box has the whole internet with no proxy.
  A lead talked into sending the project somewhere, or a dependency that
  phones home, leaves traces in logs on the same machine it controls.
- **Old code turning bad.** A package that was fine when it merged gets an
  advisory a month later. Nobody re-checks what production runs.

So:

- **Every team sends a heartbeat to its hi server.** The server notices
  when one stops, which is the only way to notice a machine that died.
- **Every release declares what it runs**, as a bill of materials
  (CycloneDX), kept with the release and sent to the server.
- **The server matches those bills against free advisory feeds every
  night** and opens an incident in the team when production runs
  something known to be vulnerable, most urgently something known to be
  exploited.
- **The server reads the team's network log and health as they arrive**,
  with plain rules, no model, and raises what looks wrong.
- **It can stop a team in steps**, from cutting its internet to pausing
  its boxes, and only the steps that can be undone happen without a
  person.
- **People hear about it outside the team's channel**, because the lead
  can post in that channel.

## What there is today

- The broker watches production's and the preview's jobs, production's
  app, and members' tasks, and opens incidents (`team_incidents.go`).
- Members' and the lead's boxes reach the internet only through the
  proxy, which logs one line per decision to `run/proxy/network.log`:
  time, `allow` or `deny`, host. It logs no sizes. The lead's box uses
  `--dns 127.0.0.1`, so it can't look names up around the proxy.
- An app box (preview, production) runs on its own network with full
  internet and no proxy. Nothing records where it connects.
- A task that changes a JavaScript project's packages gets a dependency
  review, and a newly trusted install script needs an owner's yes
  (`team_deps.go`). Nothing checks packages for known advisories, and
  nothing checks again after the merge.
- A connected client reports user, hostname, hi version, OS, and online
  to the server on every command, and a user timer sends a heartbeat
  every 5 minutes (hi_server.md §6). A team doesn't report anything.
- Snapshots go to a restic repository, and offsite when set. Nobody is
  told when the last one is old.
- The server can stop compute from Slack, with the audit log. It can't
  stop a team.

## Where the watcher runs

On the hi server, as part of `hi server`. Not in the team's broker, and
not on the team's machine:

- the broker is one of the things being watched, and a machine that died
  can't report its own death;
- a compromised team must not be able to change its own watcher, its
  rules, or its records;
- the server already has what this needs: a Slack app separate from the
  teams', approvers, the audit log, the device keys, and `/hi` commands.

### What it can and can't see

Two kinds of compromise, with different answers:

- **A box is compromised**: the lead talked into something, a member
  steered by a page it read, a package that phones home. The proxy, the
  broker, `__serve`, and the device key are outside every box, so the
  heartbeat, its network lines, and the obeying of a freeze stay honest.
  This is the case the rules are for, and freeze and pause answer it.
- **The machine is compromised**: whoever controls it holds the device
  key, so from then on it can send any heartbeat, any network lines, and
  ignore a freeze while saying it obeyed. What the server already has,
  the heartbeats and network lines sent before, stays as it was: the copy
  off the machine is a record of the past, not a guarantee of the future.
  Only steps that don't need the machine's help answer this: revoke and
  cut off.

A team whose machine isn't connected to a hi server has no watcher. `hi
team up` says so once, and `hi team status` says so on every run. (See
[Open questions](#open-questions).)

## The heartbeat

`hi team __serve` posts to the server every 60 seconds, signed with the
machine's device key (`hi connect`):

```json
{
  "team": "payments",
  "at": "2026-10-22T09:14:00Z",
  "hi": "v1.3.0",
  "production": "v14",
  "bom": "sha256:4be1…",
  "parts": {
    "broker": "up",
    "lead": "up",
    "gateway": { "state": "up", "last_event": "2026-10-22T09:13:41Z" },
    "proxy": "up",
    "production": { "state": "up", "restarts_24h": 0 },
    "memory": "up"
  },
  "serve_restarts_24h": 0,
  "disk": { "team": 0.41, "containers": 0.63, "inodes": 0.12 },
  "oom_kills_24h": 0,
  "snapshot": { "last": "2026-10-22T03:00:12Z", "offsite": "2026-10-22T03:01:40Z" },
  "incidents_open": 1,
  "spend_1h_usd": 0.42,
  "key_usage_1h_usd": 0.42,
  "network": { "from": 1832211, "lines": ["2026-10-22T09:13:02Z allow api.github.com", "…"] }
}
```

| Field | Meaning |
|---|---|
| `production`, `bom` | the version production runs, and the digest of its bill of materials |
| `parts` | each part's state; `gateway.last_event` is the last message of any kind on the gateway's Slack socket, Slack's pings included, so a gateway that lost its socket while its process lives is seen |
| `serve_restarts_24h` | how often systemd started `__serve` again; a loop shows here and nowhere else |
| `disk` | the share used of the team's folder's file system, podman's storage, and inodes |
| `oom_kills_24h` | from the team's cgroups' `memory.events` |
| `snapshot` | when the last snapshot, and the last offsite copy, finished |
| `spend_1h_usd` | model spend through the team's proxy in the last hour |
| `key_usage_1h_usd` | what OpenRouter says the team's key spent in the last hour (`GET /api/v1/key`, which the broker asks with the key it holds) |
| `network` | the network log's lines from byte offset `from`, at most 2,000 |

The heartbeat carries the network log so the server keeps a copy off the
team's machine (see [What it can and can't see](#what-it-can-and-cant-see)
for how far that goes). The server answers with the offset it has kept up
to, and the next heartbeat sends on from there: a heartbeat that wasn't
answered sends the same lines again, and the server keeps each offset
once. While more than 2,000 lines wait, as after an hour offline,
`__serve` sends every 5 seconds until it has caught up. A network log
that was rotated or shortened starts again from 0, and the server notes
it in the team's record.

The server's answer also says what it wants done (see
[Stopping a team](#stopping-a-team)), usually nothing.

### A heartbeat that stops

No heartbeat for 3 minutes is an incident on the server: posted in the
server's watch channel and pushed to the team's owners (see
[Who is told](#who-is-told)). The team's channel can't hear it: the team
is what stopped. When heartbeats come again the thread says for how long
they stopped and what `serve_restarts_24h` says about why.

A stop that was meant isn't an incident. When systemd stops `__serve`, it
sends one last heartbeat with `"stopping": "restart"`, or `"down"` for
`hi team down`. After a restart (`hi update --restart`, a reboot) the
server waits 10 minutes instead of 3 for the next one; after a down it
waits for `hi team up`. A machine that loses its power sends nothing,
and is an incident after 3 minutes as before.

### Watching the server

The server pings an outside dead-man's switch every minute:
healthchecks.io (free for 20 checks, or self-hosted, it is open source),
set with `hi server watch --deadman <url>`. When the server stops, that
service emails or pushes the admins. Without one, `hi server status`
warns that nobody would know the server is down.

The same service can check production's public address from outside
(`hi net expose` domains), which finds a broken DNS record or certificate
that every check from inside passes.

## Bills of materials

Each release declares what production runs, as a
[CycloneDX 1.6](https://cyclonedx.org/) JSON file. hi makes it on the
machine, from git and the images, without running project code, as
`team_deps.go` reads lockfiles today:

- **the images**: the team's base image, the app's, Hindsight's, by
  digest, with their OS packages (Syft, `hi install syft`);
- **the app's packages**, from its lockfiles (`bun.lock`, `uv.lock`,
  `go.sum`, `package-lock.json`, …), at the release's commit;
- **hi and the agents**: hi's version, and Claude Code's, Codex's, and
  Hermes' as the boxes have them, as purls;
- **skills and MCP servers** installed for the team's roles, each with its
  source and commit: they are code the agents run;
- **models and providers** the team calls (CycloneDX `services`), and the
  hosts in each box's allowlist.

It is kept as `boms/<version>.cdx.json` in the team's folder, beside the
release's snapshot, and sent to the server. The heartbeat's `bom` names
the one production runs, so the server always knows which bill to check.
A preview gets one too, with no advisory incidents: it is someone's work
in progress.

**What runs differs from what was declared.** Agents and apps install
things at run time. Once a week hi runs Syft against production's
running box and compares it with the release's bill. A package that
isn't declared is an incident in the team's channel: either the bill
missed it, or something installed it.

## Advisories

The server keeps a copy of these, refreshed every night, all free:

| Source | Used for |
|---|---|
| [OSV.dev](https://osv.dev) bulk data | matching: it gathers GitHub's advisories, PyPI, npm, Go, crates, RubyGems, Debian, Ubuntu, and Alpine, by package and version |
| [CISA KEV](https://www.cisa.gov/known-exploited-vulnerabilities-catalog) | which advisories are known to be exploited |
| [FIRST EPSS](https://www.first.org/epss/) | how likely each is to be exploited in the next 30 days |

NVD isn't used for matching: it names products, not packages, and has
fallen far behind since 2024. OSV already carries its CVSS scores.

Matching is `osv-scanner` (Apache-2.0, Go, reads CycloneDX, works
offline) run against every production bill, after each refresh and when a
new bill arrives. Each advisory that matches production gets one of:

| When | What happens |
|---|---|
| in KEV | an incident in the team's channel at once, and a push to the owners |
| EPSS ≥ 0.1, or critical, with a fixed version | an incident in the team's channel |
| anything else with a fixed version | the team's weekly report |
| no fixed version | the weekly report, once |

An advisory incident wakes the lead like any other, with the advisory,
the package, which part of the bill it came from, and the fixed version.
The lead proposes the upgrade and waits for a yes, as for any work. The
reviewer's brief for that task names the advisory.

**Not affected.** An owner can say a match doesn't apply, with why:

```sh
hi team advisory payments ignore GHSA-xxxx-xxxx-xxxx --because "we never parse untrusted YAML"
```

It is kept as a VEX statement in the team's folder and sent to the
server, which stops raising it for that package, and raises it again when
the advisory changes.

**Before the merge.** The dependency review runs the same match on the
task's new and updated packages, and the reviewer's brief lists what
matches. A package with a KEV advisory can't be merged without an
owner's yes, like a trusted install script.

## What else the server watches

Every rule is plain code on the server, with no model, like the jobs
watcher. Each opens at most one incident until it clears.

**Health**, from the heartbeat:

- a part not `up` for 3 heartbeats in a row;
- nothing from Slack, not even its pings, for 10 minutes
  (`gateway.last_event` counts every message on the gateway's socket,
  which Slack pings while it is open): a gateway whose process lives but
  whose socket is gone. The server doesn't read the team's channel;
- `serve_restarts_24h` over 3, or production restarting more than 3 times
  in an hour;
- any OOM kill;
- disk or inodes over 85%, or full within 3 days at the last week's rate;
- no snapshot in 26 hours, or no offsite copy in 26 hours when offsite is
  set;
- model spend through the proxy in an hour over three times the team's
  7-day hourly average and over $5: a loop;
- the key spending more than the proxy counted, by over $1 in an hour:
  someone uses the team's key outside hi. The key never enters a box, so
  that spend can't show in the proxy, only in OpenRouter's count.

**Network**, from the lead's and members' network logs:

- more than 20 denies from one box in 10 minutes (often a member, or the
  lead, being steered somewhere it shouldn't go);
- a raw IP address, or a host on the server's list of paste, file-drop,
  and tunnel services (`watch-hosts.txt`, one per line, with a starter list);
- more than 50 MB sent to one host in an hour, from any box, unless the
  host is the model provider's, GitHub, or the team's own S3 bucket;
- a host added to a box's allowlist.

For these lines to say how much was sent, the proxy logs a second line
when a tunnel closes, after the decision line, which stays as it is:

```text
2026-10-22T09:13:02Z allow api.github.com
2026-10-22T09:13:05Z closed api.github.com 1204 88231 3.1
```

(bytes up, bytes down, seconds).

**App boxes.** Production and the preview keep their full internet: an
app talks to databases, mail servers, and WebSockets, which a CONNECT
proxy can't carry, and a proxy set through `HTTP_PROXY` is ignored by
many libraries, so its log would be both partial and easy to go around.
Instead they get a resolver of their own (`--dns` to a small resolver in
the team's proxy box) that answers every name and logs it as
`<time> resolve <host>`. That changes nothing for any protocol, and shows
where production connects by name. What it misses: connections to raw IP
addresses and lookups over DNS-over-HTTPS, so it doesn't stop a
determined program, and it records no sizes. A host production hasn't
looked up in 30 days is in the weekly report, not an incident: apps call
new services all the time.

## Stopping a team

From the gentlest step to the hardest:

| Step | How | Undo | Works on a compromised machine |
|---|---|---|---|
| **Freeze** | every box's allowlist is emptied, and the proxies reread it within a second; the app boxes' public addresses stop and the boxes are disconnected from their network, so nothing reaches them and they reach nothing; the broker starts no tasks | `unfreeze` restores the allowlists, the network, and the addresses, with the same links | no |
| **Pause** | `podman pause` on every box of the team, which keeps their memory for a look later | `unpause` | no |
| **Revoke** | the team's OpenRouter key is disabled through OpenRouter's API, when the server made the key | a new key | **yes** |
| **Cut off** | the machine's peer is disabled in NetBird | enable it again | **yes**, for the mesh |

Freeze and pause go to the team in the answer to its heartbeat, which
`__serve` carries out and confirms in the next one. That needs the
machine to be on and honest; revoke and cut off don't, which is why they
exist.

Freeze takes production's pages down until it is undone: that is the
point when production runs something being exploited, and it is why a
freeze always pushes the owners at once.

**What happens on its own.** Freeze, for a KEV advisory that matches
production, or for a burst of sends to a host on `watch-hosts.txt`.
Nothing else. Pause,
revoke, and cut off need an admin, from the server's watch channel
(buttons on the incident), `/hi team freeze|pause|revoke|cutoff <team>`,
or `hi server team … ` on the server box when Slack is down. Every step
is in the audit log, and posted in the incident's thread and in the
team's channel.

The lead can't stop, unfreeze, or quiet its own watcher. It can read the
incident and say in the channel what it knows.

## Who is told

- **The team's channel**: health and advisory incidents the lead can do
  something about (a full disk, an upgrade), as now.
- **The server's watch channel** (`#hi-watch` by default): everything,
  including what the team's channel hears. The teams' bots aren't in it.
- **The team's owners, by push** (ntfy): a stopped heartbeat, a KEV
  advisory, anything that froze the team. Slack is where the team is; a
  push reaches a person when the team's Slack is the problem. Each team
  has a topic of its own on an ntfy server that needs a token to read
  (self-hosted, or a paid account), set with `hi server watch --ntfy
  <url> --ntfy-token <token>`; `hi server watch subscribe <team>` gives an
  owner the link for their team's topic. On a public ntfy server a
  topic's name is its only secret, so hi doesn't use one. A push says
  only the team, what happened in a line, and the incident's link: the
  detail stays on the server and in Slack.

## Commands

```text
hi server watch                    what every team's heartbeat says now, and open watch incidents
hi server watch --deadman <url>    the outside dead-man's switch the server pings
hi server watch --ntfy <url> --ntfy-token <token>   where owners' pushes go
hi server watch subscribe <team>   the link an owner opens to get the team's pushes
/hi teams                          the same in Slack
/hi team freeze|unfreeze|pause|unpause|revoke|cutoff <team>

hi team bom <name> [version]       production's bill of materials, or a release's
hi team advisories <name>          what matches production now, and what was ignored
hi team advisory <name> ignore <id> --because "<why>"
```

## Releases

1. **Heartbeat.** `__serve` sends it; the server shows it, opens
   incidents for a stopped one, pings the dead-man's switch, and pushes
   owners. Health rules, including the key's spend outside hi. This
   closes the largest blind spot by itself.
2. **Bills and advisories.** Bills at release, the nightly match, KEV and
   EPSS, advisory incidents, `ignore`, the check in the dependency review.
3. **Network rules and stopping.** The proxy's `closed` lines with
   sizes, the network rules, freeze and pause (with the app boxes), the
   app boxes' logging resolver, the weekly comparison of what runs with
   what was declared.
4. **Revoke and cut off**, once the server makes the teams' OpenRouter
   keys and holds a NetBird API token.

A model reading the incidents to say what they likely mean comes, if at
all, after these, and only reads.

## Tests

- A heartbeat signed with another device's key is refused; one from a
  team the machine doesn't own is refused.
- Three missed heartbeats open one incident and one push; heartbeats again
  close it with the time they stopped; `hi team down` opens none.
- The network lines a heartbeat carries are kept once, also when the
  heartbeat is retried; a backlog of 10,000 lines arrives whole, in
  order, at 2,000 a heartbeat.
- A `stopping: restart` heartbeat and 5 minutes of silence open nothing;
  10 minutes do. A `stopping: down` waits for `hi team up`.
- Key usage $2 over the proxy's spend in an hour opens one incident.
- A bill made from a fixture project names its lockfile's packages, the
  images' digests, hi's version, and its skills, and making it runs no
  project code.
- With a fixed OSV, KEV, and EPSS copy, a KEV match opens an incident
  and a push, a low one goes to the weekly report, an ignored one raises
  nothing until its advisory changes.
- A task adding a package with a KEV advisory can't be merged without an
  owner's yes.
- The proxy writes a `closed` line with sizes for a tunnel, and the
  decision line as before.
- Freeze empties every allowlist, disconnects the app boxes, stops their
  addresses, and the broker refuses a new task; unfreeze restores the
  allowlists, the network, and the same links exactly; both are in the
  audit log.
- An app box's lookups are logged as `resolve` lines, and a Postgres or
  SMTP connection from it works as before.
- A push carries no more than the team, a line, and a link.
- The lead's socket has no command that changes the watcher.

## Decisions

- **On the server, not the machine.** A watcher on the machine dies with
  it and can be changed by what compromised it. The server already has a
  separate Slack app, approvers, an audit log, and device keys.
- **Push, not poll.** The team sends heartbeats; the server needs no way
  into the team's machine, and silence is itself the signal.
- **No model decides.** The lead reads pages anyone links, and the logs
  carry what those pages said. A model that can freeze a team can be
  talked into freezing it, or into staying quiet. Rules decide, as for
  jobs.
- **Only what can be undone happens alone.** Freeze and pause lose
  nothing. A wrong freeze costs an hour; revoking a key or cutting a
  machine off costs more and needs a person.
- **CycloneDX, not SPDX.** It has fields for services, ML models, and
  VEX, which a team's bill needs, and osv-scanner and Dependency-Track
  read it.
- **OSV, not NVD, for matching.** OSV names packages and versions; NVD
  names products and is months behind. KEV and EPSS only rank.
- **No packet capture.** Traffic is TLS through CONNECT: a capture shows
  the host, which the proxy already logs, and the sizes, which it now
  logs too.
- **The network log leaves the machine.** A copy the team can't change
  after the fact is worth more than one more rule. It protects the past,
  not what a compromised machine sends from then on.
- **Freeze includes production's app.** The advisory that freezes on its
  own is about what production runs; a freeze that left the app box
  reachable would stop everything but the thing at risk.
- **A resolver, not a proxy, for app boxes.** It breaks no protocol. It
  misses raw IPs and DNS-over-HTTPS, and the spec says so rather than
  promise a log it can't keep.
- **People are told outside the team's channel** for what matters most,
  because the lead posts there.

## Open questions

- **Teams without a server.** Some teams may run on a machine never
  connected to a hi server. A watcher could run in a separate user on the
  same machine, which covers advisories and network rules but not a dead
  machine. Is that worth building, or should a team need a server?
- **Dependency-Track.** OWASP Dependency-Track does bills, matching,
  policies, and VEX as a service (Java and PostgreSQL). With many teams
  it may be worth running beside the server instead of osv-scanner.
- **Automatic freeze.** Now that freeze takes production's pages down, is
  freezing on its own, for the two cases above, what owners want, or
  should every step wait for an admin at first?
- **The server making OpenRouter keys.** Revoke needs the server to own
  each team's key (OpenRouter's provisioning keys). Today people paste
  a key into `hi team key`.
- **Other teams' channels.** Should the watch channel be per server, or
  per group of teams?

## Not in this

- Watching machines that run no team (`hi compute` instances, which the
  server already tracks, and workstations).
- Scanning the project's code for secrets or bugs; the reviewer does
  that.
- Intrusion detection inside the boxes (process or system-call tracing).
- Keeping logs other than the network log off the machine; the snapshots'
  offsite copy covers the rest a day late.

## As built (release 1, v1.4.0)

Code: `team_watch.go` (the team's side), `server_watch.go` (the
server's).

- **The heartbeat** goes to `POST /v1/watch/heartbeat` on the signed
  client API, every minute from `__serve`, every 5 seconds while more
  than 2,000 network lines wait. Each part is an object
  (`{"state": "up|down|off", "detail", ...}`); production's has
  `restarts_1h` and `restarts_24h`, counted from the team's "Production's
  app stopped" incidents. `serve_restarts_24h` comes from
  `run/serve-starts.log`, which `__serve` appends to when it starts.
  OOM kills are each team box's `oom_kill` in its cgroup's
  `memory.events`, as increases since the last heartbeat.
- **The network log** goes as `{"from", "to", "lines", "rotated"}`, whole
  lines only, and the answer's `network_kept` is where the next starts.
  A heartbeat from before the server's offset is skipped, one after it
  is kept with a line noting the gap. The server keeps it in
  `watch/teams/<team>/network.log`.
- **Spend through hi**: the app's models proxy writes each call's cost to
  `run/spend.log`, and the lead's proxy (which Hindsight's calls go
  through too) writes a `spend <usd>` line in the network log. The key's
  usage is `GET /api/v1/key`'s `usage`, sampled each heartbeat; both are
  summed over the same window, up to an hour from the oldest sample
  within it (`spend_window_minutes`). A team on this machine's shared
  key reports no key usage, since others spend on that key too.
- **The gateway** is read from Hermes' `gateway_state.json`, which it
  rewrites every few seconds: up when the gateway runs, its Slack
  platform says `connected`, and the file is under two minutes old. That
  replaces "every message on the socket, Slack's pings included", which
  hi can't see: the socket is a tunnel through the proxy. The lead's box
  can write that file, so a compromised lead could hide a dead gateway;
  the other parts are read from outside the boxes.
- **Stops**: a SIGTERM sends `"stopping": "restart"` before the lead
  stops; `hi team down` then sends `"down"`, which wins. After a restart
  the server waits 10 minutes, after a down until the next heartbeat,
  which also closes an incident a silence opened.
- **Who owns a team**: the first device that sends its heartbeat. A
  heartbeat for the same team from another device is refused until
  `hi server watch forget <team>`.
- **Incidents** are the server's own (`w-1`, `w-2`), in
  `watch/state.json`, one per rule and team until it clears, posted in
  `--channel` or else the approvals channel, with the closing in the
  thread, and in the audit log. Those the lead can act on come back in
  the heartbeat's answer as `tell`; the team opens a `watch` incident for
  each once and closes it when the server's is gone from the list. A
  key used outside hi is never told to the team.
- **Pushes** go only for a stopped heartbeat in this release, to
  `<ntfy>/hi-watch-<team>` with the token as a bearer. The spend rule
  waits for 24 hourly samples before it judges. The new team's first
  day has no snapshot rule.
- **The dead-man's switch** is a GET every minute from the server's
  30-second tick. `hi server watch` warns without one, and without
  pushes; there is no `hi server status`.
- `/hi teams` in Slack answers with the same text as `hi server watch`.
- The proxy's `closed` lines with sizes stay in release 3, with the
  network rules that read them.
