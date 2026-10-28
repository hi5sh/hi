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
    alex: { init: "AL", name: "Alex", role: "IT · owner", tone: "sky" },
    lead: { init: "LD", name: "onboarding", role: "the team's lead · AI", tone: "violet" },
    claude: { init: "CC", name: "Claude Code", role: "in the dev box · AI", tone: "amber" },
    hi: { init: "hi", name: "hi", role: "", tone: "accent" },
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
        { who: "alex", at: 3400, atShort: 1600, text: "Sure. I'll be its owner and build it with the agents. You tell the team what you need." },
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
        { step: "4/7 People", text: "owner Alex; everyone in the channel talks with the lead", at: 3800 },
        { step: "5/7 Members", text: "lead Hermes; Claude Code and Codex in the dev box", at: 4600 },
        { step: "6/7 Models", text: "this machine's OpenRouter key", at: 5400, full: true },
        { step: "7/7 Code", text: "a new web app", at: 6000 },
        { step: "NetBird", text: "a setup key, for its addresses and the dev box ✓", at: 6600, full: true },
        { ok: "Team onboarding is ready in /home/alex/team-onboarding.", at: 7200 },
        { cmd: "hi team up onboarding", at: 8400 },
        { ok: "Team onboarding is starting. It answers in its Slack channel in a minute or so.", at: 9400 },
      ],
      toast: { who: "alex", at: 11000, atShort: 2800, text: "Your hi team is ready. We're all in #hr-onboarding." },
    },
    {
      id: "team", title: "Meet your new team", caption: "Say what you need. The lead writes it up as a task.",
      dur: 21000, short: 6000, screen: "channel",
      members: [
        { who: "emma", kind: "people" }, { who: "sarah", kind: "people" },
        { who: "alex", kind: "owners" }, { who: "lead", kind: "lead" },
      ],
      messages: [
        { who: "lead", at: 400, full: true, text: "Hi Emma, Sarah. I'm onboarding, the team's lead. Tell me what you need, and I'll write it up for Alex to build." },
        { who: "emma", at: 3000, atShort: 300, mention: true, text: "An app to manage onboarding for new hires. Steps get missed when it's all by email." },
        { who: "lead", at: 6000, full: true, text: "Who owns the steps today, and which get missed most?" },
        { who: "emma", at: 8000, full: true, text: "IT, Facilities, and the manager. The badge, usually." },
        { who: "sarah", at: 10500, full: true, text: "And a view of who starts next week." },
        { who: "lead", at: 13000, atShort: 1900, text: "Here's the task I'd file for Alex:", list: ["Add a new hire with a start date", "Their steps, each with an owner, ticked off as done", "Late steps in red", "Who starts next week"], after: "Shall I file it?" },
        { who: "emma", at: 17200, atShort: 4300, text: "Yes, file it." },
        { who: "lead", at: 18800, atShort: 5100, text: "Filed as #1 for the owners." },
      ],
    },
    {
      id: "build", title: "The owner builds it with an agent", caption: "Alex starts #1 on the dev page. Claude Code builds it.",
      dur: 17000, short: 6500, screen: "devpage", tab: "Code",
      window: "onboarding-dev · dev page",
      work: [
        { id: "#1", title: "New hires and their steps", at: 300,
          states: [{ at: 300, s: "working" }, { at: 5200, atShort: 1800, s: "asking" }, { at: 7400, atShort: 3000, s: "working" }, { at: 11000, atShort: 3800, s: "done" }] },
      ],
      chat: [
        { who: "hi", at: 300, atShort: 100, text: "Alex started #1 with Claude Code, on its own branch and worktree." },
        { who: "claude", at: 1800, working: "Reading the task and docs/map.md…", full: true },
        { who: "claude", at: 5200, atShort: 1800, ask: "When is a step late?", options: ["Past its own due date", "Past the hire's start date"], pick: 0, pickAt: 6800, pickAtShort: 2700 },
        { who: "claude", at: 8000, working: "Building… (3m · make check)", full: true },
        { who: "claude", at: 11000, atShort: 3800, text: "Done: new hires, their steps with owners, and late steps in red. make check passes." },
      ],
      actions: [{ label: "Try on preview", at: 12600, atShort: 4500 }, { label: "Ship to Dev" }],
      status: [{ at: 0, text: "Preview: dev" }, { at: 12900, atShort: 4700, text: "Preview: ▸ 1-new-hires" }],
      ready: { who: "alex", at: 14200, atShort: 5300, text: "@Emma the first version is on the preview:", link: true },
    },
    {
      id: "app", title: "The application comes alive", caption: "From idea to application.",
      dur: 12000, screen: "app",
      cursor: [{ at: 2600, to: "add" }, { at: 3400, click: true, state: "form" }, { at: 4400, type: "name" }, { at: 5400, type: "date" }, { at: 6600, to: "save" }, { at: 7200, click: true, state: "added" }, { at: 9200, to: "tick" }, { at: 9800, click: true, state: "ticked" }],
    },
    {
      id: "improve", title: "Feedback from inside the app", caption: "Send feedback from inside the app.",
      dur: 11000, short: 4200, screen: "improve",
      open: { at: 1800, atShort: 300 },
      messages: [
        { who: "emma", at: 2600, atShort: 900, text: "Can we get a reminder when a step is overdue?" },
        { who: "hi", at: 4400, full: true, text: "Sent to #hr-onboarding as Feedback f-1, with a picture of the page." },
        { who: "lead", at: 6500, atShort: 2600, text: "Thanks, Emma! Filed for the owners as #2: a message to the step's owner each morning it's late." },
      ],
    },
    {
      id: "release", title: "Live when an owner says so", caption: "Alex builds #2 the same way, then releases.",
      dur: 13000, short: 5000, screen: "devpage", tab: "Release",
      window: "onboarding-dev · dev page",
      rows: [
        { name: "Production", tone: "prod", text: "Nothing released yet. Dev has 2 changes waiting:", changes: ["t-1 New hires and their steps", "t-2 A reminder when a step is late"], button: "Release v1", at: 4200, atShort: 1600 },
        { name: "Preview", tone: "prev", text: "Shows dev, with both changes." },
        { name: "This page", text: "For the owners only." },
      ],
      status: [{ at: 5000, atShort: 2000, text: "Releasing v1… a snapshot first" }, { at: 8000, atShort: 3000, text: "Production: v1 ✓" }],
      ready: { who: "lead", at: 9200, atShort: 3600, text: "🚀 Production v1 is out, the first release, approved by Alex on the dev page.", link: "https://onboarding.apps.example.com" },
    },
    {
      id: "ecosystem", title: "The bigger picture", caption: "One team per project. Anyone can start one.",
      dur: 9000, screen: "ecosystem",
      teams: [
        { channel: "#hr-onboarding", app: "Onboarding", people: ["EM", "SA"], owner: "AL", at: 300 },
        { channel: "#sales-followup", app: "Customer follow-up", people: ["TO", "AN"], owner: "RI", at: 1500 },
        { channel: "#supplier-portal", app: "Supplier portal", people: ["KI"], owner: "JO", at: 2700 },
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
