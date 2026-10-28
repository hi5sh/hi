import { type CSSProperties, useEffect, useState } from "react";

// The welcome page of a new team's app. It walks people to the helper's
// button, whose Feedback goes to the team: the lead files it for the
// owners, who build the app. Replace this when the team builds the app's
// first page, with styles.css's welcome part.
// hi adds the helper to every page; <hi-helper> says where its button is,
// whether its panel is open and how wide, and a "hi-helper" event says
// when that changes.

type Helper = { corner: string; open: boolean; hidden: boolean; width: number };

function helperNow(): Helper | null {
  const host = document.querySelector<HTMLElement>("hi-helper");
  if (!host?.dataset.corner) {
    return null;
  }
  return {
    corner: host.dataset.corner,
    open: host.dataset.open === "true",
    hidden: host.dataset.hidden === "true",
    width: Number(host.dataset.width) || 400,
  };
}

export function App() {
  const [helper, setHelper] = useState(helperNow);
  useEffect(() => {
    const follow = () => setHelper(helperNow());
    addEventListener("hi-helper", follow);
    follow();
    return () => removeEventListener("hi-helper", follow);
  }, []);

  const side = helper?.corner.endsWith("l") ? "left" : "right";
  if (helper?.open) {
    // The text moves beside the helper's panel, which is on the button's
    // side, as wide as the person made it.
    return (
      <main
        className={`welcome open beside-${side}`}
        style={{ "--helper-width": `${helper.width}px` } as CSSProperties}
      >
        <div className="column">
          <h1>Tell your team what this app should do</h1>
          <p>
            Under Feedback, say what you need in your own words: a page, a list, a form, a report.
            If you can imagine it, your team can build it.
          </p>
          <p className="example">
            Try: “A page where we keep our suppliers and what we owe each one.”
          </p>
          <p className="quiet">
            Your team's lead reads it and files it for the people who build the app; you try each
            change on the preview first. You can also ask the lead in the team's Slack channel.
          </p>
        </div>
      </main>
    );
  }
  return (
    <main className="welcome">
      <div className="column">
        <h1>Your app starts here</h1>
        <p>It's empty for now. Your team builds it from what you tell them.</p>
        {helper?.hidden && <p className="quiet">Reload the page to bring back the help button.</p>}
      </div>
      {helper && !helper.hidden && (
        <div className={`pointer ${helper.corner}`} role="note">
          <span>Press the help button to start</span>
          <svg viewBox="0 0 48 48" aria-hidden="true">
            <path d="M8 8 C 20 14, 30 24, 38 38 M38 38 L 38 24 M38 38 L 24 36" />
          </svg>
        </div>
      )}
    </main>
  );
}
