// The demo at the top of hi5.sh/team/, played by assets/story.js. The
// channel scene is an example. The terminal scenes keep the shape and wording
// of what hi team printed for a real team; the team, its people, its work,
// and its folder are made up. docs/feature-pages.md says how to edit it.
window.HI5_STORY = {
  size: { w: 600, h: 520 },
  narrow: { w: 400, h: 720 },
  pace: { short: 1 },
  team: "onboarding",
  channel: "#hr-onboarding",
  preview: "https://onboarding-preview.apps.example.com",
  people: {
    emma: { init: "EM", name: "Emma", role: "HR", tone: "warm" },
    sarah: { init: "SA", name: "Sarah", role: "HR", tone: "warm" },
    lead: { init: "LD", name: "onboarding", role: "the team's lead · AI", tone: "violet" },
    tom: { init: "TO", name: "Tom", role: "owner", tone: "warm" },
  },
  scenes: [
    {
      id: "ask", title: "Ask in Slack", caption: "Say what you need. The lead files it for the owners.",
      short: 12000, screen: "channel",
      members: [
        { who: "emma", kind: "people" }, { who: "sarah", kind: "people" }, { who: "tom", kind: "people" },
        { who: "lead", kind: "lead" },
      ],
      messages: [
        { who: "emma", at: 300, mention: true, text: "Late steps get missed. Can they show in red?" },
        { who: "lead", at: 2200, text: "Filed for the owners as #12:", list: ["A step past its due date shows in red", "Its owner gets a reminder each morning"], after: "An owner picks it up in the dev box." },
        { who: "tom", at: 9000, text: "Late steps are red on the preview now; try it:", link: true },
      ],
    },
    {
      id: "ship", title: "Built in the dev box", caption: "An owner builds it with Claude Code, and ships it to the preview.",
      short: 13000, screen: "terminal", window: "box@onboarding-dev: ~/.herdr/worktrees/repo/12-late-steps",
      lines: [
        { cmd: "hi dev ship -m \"Late steps in red\"", at: 300 },
        { out: "12-late-steps is in dev as t-7: Late steps in red.", at: 1400 },
        { out: "Dev is on the preview: https://onboarding-preview.apps.example.com", at: 1700 },
        { out: "Production moves when an owner or a developer releases dev. In the box, git fetch origin brings dev into the clone.", at: 2000 },
        { cmd: "hi dev status", at: 4200 },
        { out: "Production: v2 https://onboarding.apps.example.com", at: 5200 },
        { out: "Dev: v2 + 1 change", at: 5400 },
        { out: "  t-7 Late steps in red (built in the dev box)", pre: true, at: 5600 },
        { out: "Preview: dev, v2 + 1 change https://onboarding-preview.apps.example.com", at: 5800 },
        { out: "Backups: Snapshots: the last was a9773ea6, Oct 20 08:00 (before v2). Copied offsite Oct 20 08:04.", at: 6000 },
        { ok: "Incidents: none open", at: 6200 },
      ],
    },
    {
      id: "snapshots", title: "Safe to go back", caption: "A snapshot every day, and before every release.",
      short: 11000, screen: "terminal", window: "you@it: ~",
      lines: [
        { cmd: "hi team snapshots onboarding", at: 300 },
        { out: "Snapshots of team onboarding, in /home/you/.local/share/hi/team-snapshots/onboarding.", at: 1300 },
        { out: " ", at: 1300 },
        { out: "ID        TAKEN             WHY         SIZE      NEW", pre: true, at: 1600 },
        { out: "7a298224  2026-10-19 03:33  daily       12.1 MB   12.1 MB", pre: true, at: 2100 },
        { out: "6d2fc996  2026-10-19 14:17  before v1   12.6 MB   1.4 MB", pre: true, at: 2500 },
        { out: "a9773ea6  2026-10-20 08:00  before v2   14.0 MB   2.2 MB", pre: true, at: 2900 },
        { out: "66947a37  2026-10-21 03:54  daily       14.3 MB   0.9 MB", pre: true, at: 3300 },
        { cmd: "hi team ls", at: 5200 },
        { out: "TEAM         STATE   CHANNEL       MEMBERS                 FOLDER", pre: true, at: 6200 },
        { ok: "onboarding   up      C0EXAMPLE01   lead, coder, reviewer   /home/you/team-onboarding", pre: true, at: 6400 },
      ],
    },
  ],
};
