# `hi team`: talking to the lead in a DM

Status: Approved (2026-10-15), built in v0.62.0. Follows
[hi_team_helper_signin.md](hi_team_helper_signin.md), whose DM so far only
carried hi's links.

## Goal

People who may change the app talk to the lead one to one, in the bot's
DM, so the channel stays for the group. The channel still sees what
matters: work started from a DM is announced there, with who asked, and
each release says who asked for what it holds.

## Who

- The people who may ask for work: the owners and askers, or with no
  askers, the members of the channel. The same rule as `/hi-<team>`.
- Anyone else who writes to the bot gets nothing; Hermes' gate would let
  the whole workspace in when the team has no askers, so the plugin in the
  lead's home asks hi (`hi team web may --user U…`) before Hermes reads a
  DM, and drops it otherwise. Answers are kept for a few minutes.
- Group DMs count as DMs: everyone in them must be allowed for the lead to
  answer; in practice, write one to one.

## The conversation

- One conversation per DM, as in a chat app: Hermes'
  `dm_top_level_threads_as_sessions: false`. The lead's memory is the
  same as in the channel.
- The lead answers without being @-mentioned there.
- hi's own messages (the links, the lead's later messages from the app)
  arrive in the same DM.

## Approvals

- A yes in the DM approves work as one in the channel does. The lead
  passes `--approval dm`: in its box, hi knows from Hermes whose DM it is
  answering (`HERMES_SESSION_USER_ID`), or the lead names them,
  `--approval dm:U07ANNA`.
- hi checks it with Slack, not the lead's word: it opens the DM with that
  person and takes their newest message that comes after one of the bot's,
  within a week. That message is the approval, kept with the task as
  `dm:U07ANNA:<ts>`. As in the channel, one yes may start the tasks it
  agreed to, but a release needs a yes of its own.
- A release, a rollback, or production's helper still needs an owner's
  yes in the channel, where the team sees it.
- This reads DMs, so the app needs `im:history`, `im:read` (Hermes looks
  a DM up with it), and the `message.im` event,
  and its Messages tab becomes writable.

## Transparency

- **Work started from a DM** goes to the team's owners in a DM when it
  starts, all but the one who asked: ":speech_balloon: Anna asked the lead
  for this in a DM: *Red badge when overdue* (t-12). It goes into dev; it
  reaches production only in a release you say yes to." Until v0.83.0
  this was posted in the channel, which made it chatty: every small
  change for dev was a message there. A small fix the lead starts outside
  a thread goes to the owners the same way.
- **Before a release**, `hi team release --ask [--in <id>] [--notes
  <file>]` posts one summary: each piece of work dev has that production
  hasn't (its first task's title, with the tasks that continued it, such
  as a fix after a review), who asked for it and where (a DM, the app,
  the channel, or a small fix by the lead), and the preview's links to
  the app and its docs, asking the owners for a yes in its thread. It
  goes in the channel, or, when someone asked for the release, in the
  thread of their message (`--in`). Each new dev on the preview, and each
  release, also gets one line in the channel, with what it brings in its
  thread (v0.97.0).
- **Each release says who asked** for the work it holds: "Asked for by
  Anna (t-12, t-14), Bo (t-13)", from the tasks' approvals, wherever they
  were given: the channel, the app's chat, or a DM.

## From the machine: `hi team say`

The person who runs hi on the machine may talk to the lead without Slack:
`hi team say <name> <text|-> [--wait]`. hi posts it in the channel
(":desktop_computer: *admin on the machine* to the lead, with hi team
say:" and the text quoted), so the team sees it; the lead answers through
Hermes' API server, as it does the app's chat, in a conversation per
message, and hi posts the answer in that message's thread. `--wait`
prints the answer too.

Nobody in Slack sees that conversation until it ends, and a reply in the
thread is a conversation of its own, and the answer ends the lead's turn.
So hi asks the lead to start the work the message asks for (hi team
task, with a short brief), then answer briefly with what it started,
and to leave reading the code to the member a task goes to.
When there is no answer after 2 minutes, hi posts "The lead is still
working on this; its answer comes in this thread." Every turn of the
lead's stops after 60 model calls (`agent.max_turns`) and answers with
what it has. (Before v0.86.1 a lead searched team cartman's code for 13
minutes, 200 calls, and said nothing.)

The message is a go-ahead for the work it asks for, as
`--approval say:<ts>`, kept in `run/say.json` for a week: the machine's
admin can change anything there already. A release, a rollback, or
production's helper still needs an owner's yes in the channel. Since
v0.97.0 an owner may release dev from their own DM with the lead
(`--approval dm`); the channel then gets only the PROD line.

## The Slack app

`hi team manifest <name>` gains `im:history`, `im:read`, and `message.im`, and the
Messages tab is no longer read-only. Update the manifest and reinstall;
`hi team up` says which scopes are missing.

## Tests

- A DM approval: the person's newest message after the bot's counts; none
  after the bot's, or from someone who may not ask, is refused; a release
  refuses `dm`.
- Starting work with a DM approval posts in the channel; a release lists
  who asked.
- The manifest has `im:history`, `message.im`, and a writable Messages tab;
  the lead's settings have DMs on and one session per DM.

## Later

- An owner's yes in a DM for a release, announced in the channel.
- A daily digest in the channel of what was discussed in DMs.
