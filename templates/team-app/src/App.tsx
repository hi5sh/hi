import { useEffect, useState } from "react";

// The welcome page of a new team's app. It walks the people to the helper,
// the chat with the team's lead in the corner, until the team builds the
// app's first page: replace this then, with styles.css's welcome part.
// hi adds the helper to every page; <hi-helper> says where its button is
// and whether its panel is open, and a "hi-helper" event says when that
// changes.

type Helper = { corner: string; open: boolean; hidden: boolean };

function helperNow(): Helper | null {
  const host = document.querySelector<HTMLElement>("hi-helper");
  if (!host?.dataset.corner) {
    return null;
  }
  return {
    corner: host.dataset.corner,
    open: host.dataset.open === "true",
    hidden: host.dataset.hidden === "true",
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

  if (helper?.open) {
    // The text moves beside the helper's panel, which is on the button's side.
    const side = helper.corner.endsWith("l") ? "left" : "right";
    return (
      <main className={`welcome open beside-${side}`}>
        <h1>Here you talk with your team's lead</h1>
        <p>
          Say what you need in your own words: a page, a list, a form, a report. If you can imagine
          it, your team can build it.
        </p>
        <p className="example">
          Try: “A page where we keep our suppliers and what we owe each one.”
        </p>
        <p className="quiet">
          The lead asks what it needs to know, then shows you a version to try right here.
        </p>
      </main>
    );
  }
  return (
    <main className="welcome">
      <h1>Your app starts here</h1>
      <p>It's empty for now. Your team builds it from what you tell them.</p>
      {helper?.hidden && <p className="quiet">Reload the page to bring back the chat button.</p>}
      {helper && !helper.hidden && (
        <div className={`pointer ${helper.corner}`} role="note">
          <span>Press the chat button to start</span>
          <svg viewBox="0 0 48 48" aria-hidden="true">
            <path d="M8 8 C 20 14, 30 24, 38 38 M38 38 L 38 24 M38 38 L 24 36" />
          </svg>
        </div>
      )}
    </main>
  );
}
