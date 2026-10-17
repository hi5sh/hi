// The story the homepage tells: an HR employee's idea becomes an app, with a
// hi team. Edit the dialogue and timing here; assets/story.js draws it.
//
// Each scene has: id, title, caption (shown at the foot of the picture), dur
// (its length in the full story, ms), short (its length in the homepage loop,
// ms; leave it out to keep the scene out of the loop), screen (which renderer
// in story.js draws it), and its content. Times are ms from the scene's
// start: `at` in the full story, `atShort` in the loop (otherwise `at` is
// scaled to the loop's length), and `full: true` keeps an item out of the loop.
// docs/story-animation.md says how to change and export it.
window.HI5_STORY = {
  // Every time below is multiplied by this: 1 plays the scenes as written,
  // more is slower. The loop's compressed timings read best a little slower.
  pace: { short: 1.35, full: 1.2 },
  team: "onboarding",
  channel: "#hr-onboarding",
  preview: "https://onboarding-preview.apps.example.com",
  people: {
    emma: { init: "EM", name: "Emma", role: "HR", tone: "warm" },
    sarah: { init: "SA", name: "Sarah", role: "HR", tone: "warm" },
    alex: { init: "AL", name: "Alex", role: "IT", tone: "sky" },
    lead: { init: "LD", name: "onboarding", role: "the team's lead · AI", tone: "violet" },
    coder: { init: "CO", name: "coder", role: "Claude Code · AI", tone: "amber" },
    reviewer: { init: "RV", name: "reviewer", role: "Codex · AI", tone: "sky" },
    hi: { init: "hi", name: "hi", role: "the team's coder and reviewer", tone: "accent" },
  },
  scenes: [
    {
      id: "idea", title: "It starts with an idea", caption: "It starts with an idea.",
      dur: 9000, short: 4000, screen: "idea",
      sheet: {
        title: "onboarding.xlsx",
        head: ["New hire", "Start", "Laptop", "Accounts", "Badge", "Buddy"],
        rows: [
          ["Noor Haddad", "20 Oct", "✓", "✓", "✗", "✓"],
          ["Jonas Berg", "22 Oct", "✓", "✗", "✗", "–"],
          ["Lina Costa", "27 Oct", "–", "–", "–", "–"],
          ["Sam Okafor", "3 Nov", "–", "–", "–", "–"],
        ],
      },
      think: { at: 2600, atShort: 900 },
      bubble: "What if we had our own onboarding app?",
      sketch: { at: 5200, atShort: 2200, title: "Onboarding", cards: ["Starting next week", "Steps and owners", "Late steps in red"] },
    },
    {
      id: "ask", title: "Asking IT for a team", caption: "Ask IT for a team.",
      dur: 7000, short: 3000, screen: "dm",
      with: "alex",
      messages: [
        { who: "emma", at: 300, text: "Hey Alex! I have an idea for an onboarding app for HR. Could you set up a hi team for us?" },
        { who: "alex", at: 3400, atShort: 1600, text: "Sure. Give me a minute." },
      ],
    },
    {
      id: "setup", title: "IT creates the team", caption: "IT sets it up with hi, in one command.",
      dur: 13000, short: 3600, screen: "terminal",
      title: "alex@it: ~",
      lines: [
        { cmd: "hi team new onboarding", at: 300 },
        { step: "1/7 Purpose", text: "An app for HR to run every new hire's onboarding", at: 1400 },
        { step: "2/7 Slack app", text: "made from hi's manifest; tokens checked ✓", at: 2200, full: true },
        { step: "3/7 Channel", text: "#hr-onboarding ✓", at: 3000, full: true },
        { step: "4/7 People", text: "owner Alex; Emma and Sarah may ask for work", at: 3800 },
        { step: "5/7 Members", text: "lead Hermes · coder Claude Code · reviewer Codex", at: 4600 },
        { step: "6/7 Models", text: "this machine's OpenRouter key", at: 5400, full: true },
        { step: "7/7 Code", text: "a new web app", at: 6200 },
        { ok: "Team onboarding is ready in /home/alex/team-onboarding.", at: 7200 },
        { cmd: "hi team up onboarding", at: 8400 },
        { ok: "Team onboarding is starting. It answers in its Slack channel in a minute or so.", at: 9400 },
      ],
      toast: { who: "alex", at: 11000, atShort: 2800, text: "Your hi team is ready. We're all in #hr-onboarding." },
    },
    {
      id: "team", title: "Meet your new team", caption: "Say what you need. The lead asks the rest.",
      dur: 20000, short: 5500, screen: "channel",
      members: [
        { who: "emma", kind: "people" }, { who: "sarah", kind: "people" },
        { who: "lead", kind: "lead" }, { who: "coder", kind: "agents" }, { who: "reviewer", kind: "agents" },
      ],
      messages: [
        { who: "lead", at: 400, full: true, text: "Hi Emma, Sarah. I'm onboarding, the team's lead. What should we build first?" },
        { who: "emma", at: 3000, atShort: 300, mention: true, text: "An app to manage onboarding for new hires. Steps get missed when it's all by email." },
        { who: "lead", at: 6000, full: true, text: "What matters most?" },
        { who: "emma", at: 8000, full: true, text: "New hires, their steps, and who owns each step." },
        { who: "sarah", at: 10500, full: true, text: "And a view of who starts next week." },
        { who: "lead", at: 13000, atShort: 1900, text: "Here's what the first version does:", list: ["Add a new hire with a start date", "Their steps, each with an owner, ticked off as done", "Late steps in red", "Who starts next week"], after: "Shall I start?" },
        { who: "emma", at: 17200, atShort: 4300, text: "Yes, go." },
      ],
    },
    {
      id: "build", title: "The team builds", caption: "A coder builds, a reviewer checks, hi merges only what's approved.",
      dur: 15000, short: 5000, screen: "build",
      tasks: [
        { id: "t-1", title: "New hires and their steps", who: "coder", at: 300,
          states: [{ at: 300, s: "working" }, { at: 4000, s: "in review" }, { at: 7500, s: "fixing" }, { at: 9500, s: "approved" }, { at: 10500, s: "merged" }] },
        { id: "t-2", title: "Who starts next week", who: "coder", at: 1200,
          states: [{ at: 1200, s: "working" }, { at: 6000, s: "in review" }, { at: 9000, s: "approved" }, { at: 10800, s: "merged" }] },
        { id: "t-3", title: "Review t-1", who: "reviewer", at: 4300,
          states: [{ at: 4300, s: "checking" }, { at: 6500, s: "changes: late steps aren't red" }, { at: 9500, s: "approved" }] },
      ],
      app: [{ at: 2500, part: "bar" }, { at: 5000, part: "rows" }, { at: 8500, part: "late" }, { at: 9800, part: "cards" }],
      ready: { who: "lead", at: 12000, atShort: 3700, text: "The first version is ready to try:", link: true },
    },
    {
      id: "app", title: "The application comes alive", caption: "From idea to application.",
      dur: 12000, screen: "app",
      cursor: [{ at: 2600, to: "add" }, { at: 3400, click: true, state: "form" }, { at: 4400, type: "name" }, { at: 5400, type: "date" }, { at: 6600, to: "save" }, { at: 7200, click: true, state: "added" }, { at: 9200, to: "tick" }, { at: 9800, click: true, state: "ticked" }],
    },
    {
      id: "improve", title: "Keep improving it, from inside the app", caption: "Keep improving it from inside the app.",
      dur: 14000, short: 5000, screen: "improve",
      open: { at: 1800, atShort: 300 },
      messages: [
        { who: "emma", at: 2600, atShort: 900, text: "Can we get a reminder when a step is overdue?" },
        { who: "lead", at: 5200, atShort: 2000, text: "Yes: a message to the step's owner each morning it's late. Shall I start?" },
        { who: "emma", at: 8000, atShort: 3000, text: "Yes." },
        { who: "hi", at: 9500, full: true, text: "The coder builds it; the reviewer checks it." },
        { who: "lead", at: 11500, atShort: 3900, text: "On the preview now. It reaches everyone when an owner releases it." },
      ],
      bell: { at: 11800, atShort: 4200 },
    },
    {
      id: "ecosystem", title: "The bigger picture", caption: "One team per project. Anyone can start one.",
      dur: 9000, screen: "ecosystem",
      teams: [
        { channel: "#hr-onboarding", app: "Onboarding", people: ["EM", "SA"], at: 300 },
        { channel: "#sales-followup", app: "Customer follow-up", people: ["TO", "AN"], at: 1500 },
        { channel: "#supplier-portal", app: "Supplier portal", people: ["KI"], at: 2700 },
      ],
      hub: { at: 3800, text: "on your own machines" },
    },
    {
      id: "hi5", title: "hi5", caption: "",
      dur: 7000, short: 3000, screen: "hi5",
      meet: { at: 500, atShort: 250 },
      line: { at: 2200, atShort: 1100, text: "Your ideas. Your team. Let's build." },
      sub: { at: 3400, atShort: 1800, text: "hi5 · where humans and AI build together" },
    },
  ],
};
