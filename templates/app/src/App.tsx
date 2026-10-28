import { type FormEvent, useEffect, useState } from "react";
import type { Note } from "./db";

export function App() {
  const [notes, setNotes] = useState<Note[]>([]);
  const [text, setText] = useState("");
  const [error, setError] = useState("");

  useEffect(() => {
    fetch("/api/notes")
      .then((response) => response.json())
      .then(setNotes)
      .catch(() => setError("The notes didn't load."));
  }, []);

  async function add(event: FormEvent) {
    event.preventDefault();
    const response = await fetch("/api/notes", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ text }),
    });
    if (!response.ok) {
      setError("The note wasn't saved.");
      return;
    }
    const note: Note = await response.json();
    setNotes([note, ...notes]);
    setText("");
    setError("");
  }

  return (
    <main>
      <h1>Hifin Template Name</h1>
      <form onSubmit={add}>
        <input aria-label="Note" value={text} onChange={(event) => setText(event.target.value)} />
        <button type="submit">Add</button>
      </form>
      {error && <p role="alert">{error}</p>}
      <ul>
        {notes.map((note) => (
          <li key={note.id}>{note.text}</li>
        ))}
      </ul>
    </main>
  );
}
