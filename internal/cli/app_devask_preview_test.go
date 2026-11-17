package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Claude Code's AskUserQuestion with previews: a box beside the options,
// the selected option's. The screen's options aren't the box's lines; the
// transcript gives each option its words and its preview.
func TestAppDevAskPreview(t *testing.T) {
	screen := strings.Join([]string{
		"● The app currently uses TradingView's dark palette, so before I make it more colorful I need a few answers from",
		"  you about which direction you want. · summary",
		"",
		"────────────────────────────────────────────────────────────────────────────────────────────────────",
		"←  ☐ Palette  ☐ Chart  ✔ Submit  →",
		"",
		"│ The app is currently TradingView dark: grey-navy background, teal/red candles, one blue accent. Which direction",
		"│ should \"more colorful\" go?",
		"",
		"❯ 1. Neon arcade (Recommended)        ┌──────────────────────────────────────┐",
		"  2. Sunset warm                      │ bg      #140f2e  deep indigo         │",
		"  3. Bright light theme               │ panel   #1f1846  violet              │",
		"  4. Keep it dark, add color          │ border  #3a2f72                      │",
		"     accents                          │ accent  #ff3ea5  hot pink            │",
		"                                      └──────────────────────────────────────┘",
		"",
		"                                      Notes: press n to add notes",
		"",
		"────────────────────────────────────────────────────────────────────────────────────────────────────",
		"  Chat about this",
		"",
		"Enter to select · ↑/↓ to navigate · n to add notes · Tab to switch questions · Esc to cancel",
	}, "\n")
	ask := appDevParseAsk(screen)
	if ask == nil || len(ask.Options) != 4 {
		t.Fatalf("%+v", ask)
	}
	for i, want := range []string{"Neon arcade (Recommended)", "Sunset warm", "Bright light theme", "Keep it dark, add color"} {
		if o := ask.Options[i]; o.Label != want || strings.ContainsAny(o.Detail, "│┌└#") {
			t.Fatalf("option %d: %+v", i+1, o)
		}
	}
	ask.Tabs[0].Current = true
	// Beside a preview, a number doesn't pick: the arrows, then Enter.
	if keys, _, err := appDevAskKeys(ask, "claude", 3, ""); err != nil || strings.Join(keys, " ") != "down down down enter" {
		t.Fatal(keys, err)
	}
	if keys, _, _ := appDevAskKeys(ask, "claude", 0, ""); strings.Join(keys, " ") != "enter" {
		t.Fatal(keys)
	}

	home := t.TempDir()
	transcript := filepath.Join(home, "s.jsonl")
	write := func(lines ...string) { os.WriteFile(transcript, []byte(strings.Join(lines, "\n")+"\n"), 0o600) }
	call := `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_1","name":"AskUserQuestion","input":{"questions":[` +
		`{"header":"Palette","question":"The app is currently TradingView dark: grey-navy background, teal/red candles, one blue accent. Which direction should \"more colorful\" go?","multiSelect":false,"options":[` +
		`{"label":"Neon arcade (Recommended)","description":"Deep indigo and hot pink","preview":"bg      #140f2e  deep indigo\npanel   #1f1846  violet\n"},` +
		`{"label":"Sunset warm","description":"Plum and orange","preview":"bg      #1d1020  plum\n"},` +
		`{"label":"Bright light theme","description":"White with strong colors"},` +
		`{"label":"Keep it dark, add color accents","description":"The same dark, brighter accents"}]},` +
		`{"header":"Chart","question":"Candles?","options":[{"label":"Brighter"},{"label":"The same"}]}]}}]}}`
	write(`{"type":"user","message":{"content":"Work on task #19"}}`, call)
	questions := appDevAskPending(home, transcript)
	if len(questions) != 2 {
		t.Fatalf("%+v", questions)
	}
	appDevAskWithQuestions(ask, questions)
	if !strings.HasPrefix(ask.Question, "The app is currently") || !strings.Contains(ask.Question, "Which direction should") {
		t.Fatal(ask.Question)
	}
	if o := ask.Options[3]; o.Title != "Keep it dark, add color accents" || o.About != "The same dark, brighter accents" || o.Label != "Keep it dark, add color" {
		t.Fatalf("%+v", o)
	}
	if o := ask.Options[0]; o.Preview != "bg      #140f2e  deep indigo\npanel   #1f1846  violet" {
		t.Fatalf("%q", o.Preview)
	}

	// Answered: its result is there, and the screen's own is shown.
	write(call, `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"Neon arcade"}]}}`)
	if questions := appDevAskPending(home, transcript); questions != nil {
		t.Fatalf("answered: %+v", questions)
	}
	// Another tab on the screen than the transcript's questions: left as it is.
	other := appDevParseAsk(screen)
	other.Tabs[1].Current = true
	other.Text = []string{"Something else?"}
	appDevAskWithQuestions(other, []appDevAskQuestion{{Header: "Palette", Question: "x"}})
	if other.Question != "" || other.Options[0].Title != "" {
		t.Fatalf("%+v", other)
	}
	// Options the screen has otherwise: left as they are.
	moved := appDevParseAsk(screen)
	moved.Tabs[0].Current = true
	moved.Options[1].Label = "Ocean cool"
	appDevAskWithQuestions(moved, questions[:1])
	if moved.Question != "" || moved.Options[0].Title != "" {
		t.Fatalf("matched other options: %+v", moved)
	}
}

// agy's question: a long option wrapped to the margin is still one
// option, the ones after it are there, and "Question 1/3: …" is where it
// is and the question.
func TestAppDevAskAgyWrapped(t *testing.T) {
	screen := strings.Join([]string{
		"? What style and sound source should we use for the background music loop?",
		"? When should the music start and stop?",
		"",
		"Question",
		"────────────────────────────────────────────────────────────",
		"",
		"Question 1/3: What style and sound source should we use for the background music loop?",
		"",
		"> 1. (Recommended) Synthesized via Web Audio API — an upbeat neon-arcade / synthwave loop with no external",
		"audio files",
		"  2. Minimal rhythmic pulse / metronome beat via Web Audio API",
		"  3. Other / custom audio file",
		"  4. Write-in...",
		"",
		"  ↑/↓ Navigate · → Next · enter Select · esc Skip",
		"esc to cancel",
	}, "\n")
	ask := appDevParseAsk(screen)
	if ask == nil || len(ask.Options) != 4 {
		t.Fatalf("%+v", ask)
	}
	if ask.Progress != "Question 1/3" || len(ask.Text) != 1 || ask.Text[0] != "What style and sound source should we use for the background music loop?" {
		t.Fatalf("%q %q", ask.Progress, ask.Text)
	}
	if o := ask.Options[0]; !strings.HasSuffix(o.Label, "with no external audio files") || !o.Cursor {
		t.Fatalf("%+v", o)
	}
	if !ask.Options[3].Typed {
		t.Fatal("Write-in is in their words")
	}
	if keys, _, err := appDevAskKeys(ask, "agy", 2, ""); err != nil || strings.Join(keys, " ") != "down down enter" {
		t.Fatal(keys, err)
	}
}
