// The demo at the top of hi5.sh/app/, played by assets/story.js. The
// channel scene is an example. The terminal scenes keep the shape and wording
// of what hi app printed for a real app; the page scenes are the admin page
// and the dev page in their own stylesheets, their markup and words as the
// pages draw them; the app, its people, its work, and its folder are made
// up. docs/feature-pages.md says how to edit it.

// Icons the pages draw, as their page.html has them.
const PANEL_ICON = '<svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="3" width="18" height="18" rx="2"/><path d="M9 3v18"/></svg>';
const PLUS_ICON = '<svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"><path d="M12 5v14M5 12h14"/></svg>';
const SEND_ICON = '<svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 19V5M5 12l7-7 7 7"/></svg>';
const SPARK_ICON = '<svg class="spark" viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M12 3v18M3 12h18M5.6 5.6l12.8 12.8M18.4 5.6 5.6 18.4"/></svg>';
const STEP_ICONS = {
  read: '<span class="tool-icon"><svg viewBox="0 0 16 16"><path d="M3.5 1.5h6l3 3v10h-9z M9.5 1.5v3h3 M5.5 8.5h5 M5.5 11h5" fill="none" stroke="currentColor" stroke-width="1.3" stroke-linecap="round" stroke-linejoin="round"/></svg></span>',
  edit: '<span class="tool-icon"><svg viewBox="0 0 16 16"><path d="M10.5 2.5l3 3-8 8H2.5v-3z M9 4l3 3" fill="none" stroke="currentColor" stroke-width="1.3" stroke-linecap="round" stroke-linejoin="round"/></svg></span>',
  run: '<span class="tool-icon"><svg viewBox="0 0 16 16"><path d="M1.5 2.5h13v11h-13z M4 6l2.5 2L4 10 M8 10.5h4" fill="none" stroke="currentColor" stroke-width="1.3" stroke-linecap="round" stroke-linejoin="round"/></svg></span>',
};
const step = (icon, name, what, at, extra) => `<details class="fold tool" data-at="${at}"><summary>${STEP_ICONS[icon]}<span class="tool-name">${name}</span><span class="tool-what">${what}</span>${extra || ""}</summary></details>`;
const PURPOSE = "Onboarding for new hires: each step, its owner, and its due date, so nothing is missed.";
const steps = (on) => ["Lead", "Purpose", "Slack app", "Channel", "People", "Members", "Models", "Code", "Network", "Heartbeats"]
  .map((label, i) => i < on ? `<button class="done">✓ ${label}</button>` : `<button aria-pressed="${i === on}">${i + 1} ${label}</button>`).join("");
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
    lead: { init: "LD", name: "onboarding", role: "the app's lead · AI", tone: "violet" },
    tom: { init: "TO", name: "Tom", role: "owner", tone: "warm" },
  },
  scenes: [
    {
      id: "setup", title: "Set it up", caption: "Ten steps on the admin page; Create starts it.",
      short: 14000, screen: "page", page: "admin", width: 960, narrowWidth: 470, window: "hi on build-1 · admin page",
      html: `<main id="page"><section class="code-page"><div class="code-main">
  <div class="code-bar">
    <button class="icon side-toggle">${PANEL_ICON}</button>
    <button class="team home-button">hi on build-1</button>
    <span class="crumb"><span class="muted">▸</span> <strong data-until="8200">New app</strong><strong data-at="8200" class="still">onboarding</strong></span>
    <div id="sections" class="chips" data-at="8200"><button aria-pressed="true">Overview</button><button aria-pressed="false">People</button><button aria-pressed="false">Agents</button><button aria-pressed="false">Slack</button><button aria-pressed="false">Settings</button></div>
    <span class="grow"></span>
    <div class="presence"><span class="presence-pill you" style="--who:#7dd3fc"><span class="presence-name">Ida</span></span></div>
  </div>
  <div class="code-body"><div class="code-views"><div class="admin-area">
    <section class="admin-section" data-until="4800">
      <div class="admin-head"><h1 class="admin-title">New app onboarding</h1><span class="muted small">/home/hi/app-onboarding</span></div>
      <div class="chips new-steps">${steps(1)}<button disabled>Create</button></div>
      <div class="new-step">
        <p>In one sentence, what is this app for? The lead starts from it.</p>
        <textarea id="purpose" rows="3" placeholder="What is this app for? The lead starts from it." data-until="1500"></textarea>
        <textarea rows="3" class="still" data-at="1500" data-type="${PURPOSE}" data-dur="2200"></textarea>
      </div>
      <p class="error"></p>
      <div class="data-actions"><button>Back</button><button id="next" class="primary">Next</button><span class="grow"></span><button>Later</button></div>
    </section>
    <section class="admin-section" data-at="4800" data-until="8200">
      <div class="admin-head"><h1 class="admin-title">New app onboarding</h1><span class="muted small">/home/hi/app-onboarding</span></div>
      <div class="chips new-steps">${steps(10)}<button aria-pressed="true">Create</button></div>
      <div class="new-step">
        <p>Create makes it as hi app new would with these answers, then starts it. Jobs follows it, and the app opens here when it's up.</p>
        <dl class="facts"><dt>Name</dt><dd>onboarding · @onboarding · /hi-onboarding</dd><dt>Purpose</dt><dd>${PURPOSE}</dd><dt>Channel</dt><dd>#hr-onboarding</dd><dt>Owners</dt><dd>Tom</dd>
          <dt>lead</dt><dd>hermes · google/gemini-3.8-flash</dd><dt>coder</dt><dd>claude · opus</dd><dt>reviewer</dt><dd>codex · on ChatGPT</dd><dt>Code</dt><dd>a new web app, from hi's template</dd></dl>
        <div class="data-actions"><button id="create" class="primary">Create</button></div>
      </div>
    </section>
    <section class="admin-section" data-at="8200">
      <div class="admin-head"><h1 class="admin-title">onboarding</h1>
        <span class="pill working" data-until="10800"><span class="dot working"></span>starting</span>
        <span class="pill open still" data-at="10800"><span class="dot idle"></span>up</span>
        <span class="muted">nothing released yet</span></div>
      <p class="purpose">${PURPOSE}</p>
      <div class="admin-cards">
        <section class="admin-card"><h2>Where it is</h2><ul class="list where-list">
          <li><span class="what"><strong>Production</strong> <span class="muted small">onboarding.apps.example.com</span></span><a class="button-like small">Open ↗</a></li>
          <li><span class="what"><strong>Stage</strong> <span class="muted small">onboarding-preview.apps.example.com</span></span><a class="button-like small">Open ↗</a></li>
          <li><span class="what"><strong>Dev page</strong> <span class="muted small">for its owners and developers</span></span><button class="small">Open ↗</button></li></ul></section>
        <section class="admin-card"><h2>What to do</h2><div class="data-actions"><button>Restart</button><button>Stop</button><button>Rebuild</button><button>Snapshot now</button><button>Move…</button></div>
          <div class="data-actions danger-row"><button>Rename…</button><button class="danger">Remove…</button></div></section>
        <section class="admin-card wide"><h2>What it is</h2><dl class="facts"><dt>lead</dt><dd>Hermes, google/gemini-3.8-flash</dd><dt>coder</dt><dd>Claude Code, opus on Claude</dd><dt>reviewer</dt><dd>Codex on ChatGPT</dd><dt>Channel</dt><dd>#hr-onboarding</dd><dt>Owners</dt><dd>Tom</dd></dl></section>
      </div>
    </section>
  </div></div></div>
</div></section></main>
<i class="pt" data-at="700" data-to="#purpose" data-click></i>
<i class="pt" data-at="3900" data-to="#next" data-click></i>
<i class="pt" data-at="6800" data-to="#create" data-click></i>
<i class="pt" data-at="9400" data-to=".admin-card .button-like"></i>`,
    },
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
        { who: "tom", at: 9000, text: "Late steps are red on Stage now; try it:", link: true },
      ],
    },
    {
      id: "build", title: "Built on the dev page", caption: "Ask Claude Code in the chat. It edits and tests.",
      short: 15000, screen: "page", page: "dev", width: 900, narrowWidth: 470, window: "onboarding · dev page",
      html: `<main id="page"><section class="code-page"><div class="code-main">
  <div class="code-bar">
    <button class="icon side-toggle">${PANEL_ICON}</button>
    <span class="team">onboarding</span>
    <div class="chips"><button aria-pressed="true">Chat</button><button aria-pressed="false">Terminal</button></div>
    <span class="space-wrap"><button class="space-btn"><span class="space-label">Late steps in red · Claude</span></button></span>
    <span class="grow"></span>
    <div class="presence"><span class="presence-pill you" style="--who:#f97316"><span class="presence-name">Tom</span></span></div>
  </div>
  <div class="code-body"><div class="code-views"><div class="chat"><div class="chat-col">
    <div class="chat-main"><div class="chat-log scroll"><div class="chat-entries">
      <div class="msg-user">Add a due date to each step.</div>
      <details class="fold tools"><summary>Worked for 1m 12s · Read 3 files, Edited 2 files</summary></details>
      <div class="msg-text"><div class="md"><p>Each step has a due date now, set when its owner is assigned.</p></div></div>
      <div class="msg-user" data-at="4600">Show a step in red when it's late, and remind its owner each morning.</div>
      <details class="fold tools" data-at="5400" data-until="11200"><summary id="fold"><span data-until="6300">Read a file</span><span data-at="6300" data-until="7500" class="still">Read 2 files</span><span data-at="7500" data-until="8700" class="still">Read 2 files, Edited a file</span><span data-at="8700" data-until="10300" class="still">Read 2 files, Edited a file, Ran a command</span><span data-at="10300" class="still">Worked for 41s · Read 2 files, Edited a file, Ran a command</span></summary></details>
      <details class="fold tools still" open data-at="11200"><summary>Worked for 41s · Read 2 files, Edited a file, Ran a command</summary><div class="fold-body">
        ${step("read", "Read", "src/Checklist.tsx", 11200)}${step("read", "Read", "src/remind.ts", 11200)}${step("edit", "Edit", "src/Checklist.tsx", 11200, '<span class="plus">+12</span>')}${step("run", "Bash", "npm test", 11200)}</div></details>
      <div class="chat-activity" data-at="5000" data-until="10300"><span class="spin"></span><span class="act-text"><span data-until="7500">Reading… (2s)</span><span data-at="7500" data-until="8700" class="still">Editing src/Checklist.tsx… (19s)</span><span data-at="8700" class="still">Running npm test… (31s)</span></span></div>
      <div class="msg-text" data-at="10300"><div class="md"><p>Late steps show in red now, and each owner gets a reminder at 8:00 with their late steps. The tests pass: <strong>12 of 12</strong>.</p></div></div>
    </div></div></div>
    <div class="chat-bottom"><form class="chat-form composer">
      <textarea id="say" rows="2" placeholder="Message Claude Code… Paste or drop pictures and files" data-until="1200"></textarea>
      <textarea rows="2" class="still" data-at="1200" data-until="4600" data-type="Show a step in red when it's late, and remind its owner each morning." data-dur="2600"></textarea>
      <textarea rows="2" class="still" data-at="4600" placeholder="Message Claude Code… Paste or drop pictures and files"></textarea>
      <div class="composer-bar">
        <label class="composer-icon">${PLUS_ICON}</label>
        <span class="composer-end">
          <span class="chat-pick"><label class="composer-pick">${SPARK_ICON}<select><option>Opus 5.5</option></select></label></span>
          <button type="button" class="composer-round still" data-at="4600" data-until="10300"><svg viewBox="0 0 24 24" width="12" height="12"><rect x="5" y="5" width="14" height="14" rx="2" fill="currentColor"/></svg></button>
          <button id="send" class="composer-round composer-send" data-until="3900">${SEND_ICON}</button>
          <button class="composer-round composer-send ready still" data-at="3900" data-until="4600">${SEND_ICON}</button>
          <button class="composer-round composer-send still" data-at="4600">${SEND_ICON}</button>
        </span>
      </div>
    </form></div>
  </div></div></div></div>
</div></section></main>
<i class="pt" data-at="500" data-to="#say" data-click></i>
<i class="pt" data-at="3300" data-to="#send" data-click></i>
<i class="pt" data-at="10600" data-to="#fold" data-click></i>`,
    },
    {
      id: "ship", title: "Built in the dev box", caption: "An owner builds it with Claude Code, and ships it to Stage.",
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
        { cmd: "hi app snapshots onboarding", at: 300 },
        { out: "Snapshots of app onboarding, in /home/you/.local/share/hi/app-snapshots/onboarding.", at: 1300 },
        { out: " ", at: 1300 },
        { out: "ID        TAKEN             WHY         SIZE      NEW", pre: true, at: 1600 },
        { out: "7a298224  2026-10-19 03:33  daily       12.1 MB   12.1 MB", pre: true, at: 2100 },
        { out: "6d2fc996  2026-10-19 14:17  before v1   12.6 MB   1.4 MB", pre: true, at: 2500 },
        { out: "a9773ea6  2026-10-20 08:00  before v2   14.0 MB   2.2 MB", pre: true, at: 2900 },
        { out: "66947a37  2026-10-21 03:54  daily       14.3 MB   0.9 MB", pre: true, at: 3300 },
        { cmd: "hi app ls", at: 5200 },
        { out: "APP          STATE   CHANNEL       MEMBERS                 FOLDER", pre: true, at: 6200 },
        { ok: "onboarding   up      C0EXAMPLE01   lead, coder, reviewer   /home/you/app-onboarding", pre: true, at: 6400 },
      ],
    },
  ],
};
