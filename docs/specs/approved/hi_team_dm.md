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
- This reads DMs, so the app needs `im:history` and the `message.im` event,
  and its Messages tab becomes writable.

## Transparency

- **Work started from a DM** is announced in the channel when it starts:
  ":speech_balloon: Anna asked for this in a DM with the lead: *Red badge
  when overdue* (t-12)."
- **Each release says who asked** for the work it holds: "Asked for by
  Anna (t-12, t-14), Bo (t-13)", from the tasks' approvals, wherever they
  were given: the channel, the app's chat, or a DM.

## The Slack app

`hi team manifest <name>` gains `im:history` and `message.im`, and the
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
