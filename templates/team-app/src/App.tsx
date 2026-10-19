import { type CSSProperties, type KeyboardEvent, useEffect, useRef, useState } from "react";

// The welcome page of a new team's app. Someone signed in starts here: what
// they type in the middle of the page goes to the helper, the chat with
// the team's lead, which opens beside it. Anyone else is walked to the
// helper's button. Replace this when the team builds the app's first page,
// with styles.css's welcome part.
// hi adds the helper to every page; <hi-helper> says where its button is,
// whether its panel is open and how wide, and whether whoever is here can chat with the
// lead, and a "hi-helper" event says when that changes. A "hi-helper-send"
// event hands it a message to send.

type Helper = { corner: string; open: boolean; hidden: boolean; chat: boolean; width: number };

function helperNow(): Helper | null {
  const host = document.querySelector<HTMLElement>("hi-helper");
  if (!host?.dataset.corner) {
    return null;
  }
  return {
    corner: host.dataset.corner,
    open: host.dataset.open === "true",
    hidden: host.dataset.hidden === "true",
    chat: host.dataset.chat === "true",
    width: Number(host.dataset.width) || 400,
  };
}

// How long the message box takes to move over to the helper's panel.
export const handOverMs = 420;

export function App() {
  const [helper, setHelper] = useState(helperNow);
  const [draft, setDraft] = useState("");
  const [leaving, setLeaving] = useState(false);
  const box = useRef<HTMLTextAreaElement>(null);
  useEffect(() => {
    const follow = () => setHelper(helperNow());
    addEventListener("hi-helper", follow);
    follow();
    return () => removeEventListener("hi-helper", follow);
  }, []);
  // The box is what the page is for: ready to type in.
  const ready = helper?.chat && !helper.hidden && !helper.open;
  useEffect(() => {
    if (ready) {
      box.current?.focus();
    }
  }, [ready]);

  // The box fades over to the helper's side, then the helper opens there
  // and sends the message.
  function handOver(event: { preventDefault(): void }) {
    event.preventDefault();
    const text = draft.trim();
    if (!text || leaving) {
      return;
    }
    setLeaving(true);
    const still = window.matchMedia?.("(prefers-reduced-motion: reduce)").matches;
    setTimeout(
      () => {
        dispatchEvent(new CustomEvent("hi-helper-send", { detail: { text } }));
        setDraft("");
        setLeaving(false);
      },
      still ? 0 : handOverMs,
    );
  }

  function onKeyDown(event: KeyboardEvent<HTMLTextAreaElement>) {
    if (event.key === "Enter" && !event.shiftKey && !event.nativeEvent.isComposing) {
      handOver(event);
    }
  }

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
          <h1>Here you talk with your team's lead</h1>
          <p>
            Say what you need in your own words: a page, a list, a form, a report. If you can
            imagine it, your team can build it.
          </p>
          <p className="example">
            Try: “A page where we keep our suppliers and what we owe each one.”
          </p>
          <p className="quiet">
            The lead asks what it needs to know, then shows you a version to try right here.
          </p>
        </div>
      </main>
    );
  }
  if (helper?.chat && !helper.hidden) {
    return (
      <main className={`welcome start${leaving ? ` leaving toward-${side}` : ""}`}>
        <div className="column">
          <h1>What should your app do?</h1>
          <p className="lede">Tell your team's lead in your own words. Your team builds it.</p>
          <form className="ask" onSubmit={handOver}>
            <textarea
              aria-label="Message the lead"
              placeholder="A page where we keep our suppliers and what we owe each one…"
              rows={3}
              value={draft}
              onChange={(event) => setDraft(event.target.value)}
              onKeyDown={onKeyDown}
              disabled={leaving}
              ref={box}
            />
            <div className="row">
              <span className="quiet">Enter sends, Shift+Enter is a new line</span>
              <button type="submit" aria-label="Send" disabled={!draft.trim() || leaving}>
                <svg viewBox="0 0 24 24" aria-hidden="true">
                  <path d="M12 19V5M5 12l7-7 7 7" />
                </svg>
              </button>
            </div>
          </form>
        </div>
      </main>
    );
  }
  return (
    <main className="welcome">
      <div className="column">
        <h1>Your app starts here</h1>
        <p>It's empty for now. Your team builds it from what you tell them.</p>
        {helper?.hidden && <p className="quiet">Reload the page to bring back the chat button.</p>}
      </div>
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
