package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

// Screens as Claude Code 2.1 and Codex 0.164 draw their questions.
const (
	appTestAskClaude = `❯ Use the AskUserQuestion tool right away
  just repeat the answers back.
────────────────────────────────────────────────────────────────────
←  ☐ Color  ☐ Toppings  ✔ Submit  →

Which color do you want?

  1. Red
     A warm, bold red
❯ 2. Green
     A fresh, natural green
  3. Blue
     A cool, calm blue
  4. Type something.
────────────────────────────────────────────────────────────────────
  5. Chat about this

Enter to select · Tab/Arrow keys to navigate · Esc to cancel`

	appTestAskClaudeMulti = `────────────────────────────────────────────────────────────────────
←  ☒ Color  ☒ Toppings  ✔ Submit  →

Which toppings do you want?

❯ 1. [ ] Cheese
         Classic melted cheese
  2. [✔] Ham
         Sliced smoked ham
  3. [ ] Type something
     Submit
────────────────────────────────────────────────────────────────────
  4. Chat about this

Enter to select · Tab/Arrow keys to navigate · Esc to cancel`

	appTestAskClaudeReview = `←  ☒ Color  ☒ Toppings  ✔ Submit  →

Review your answers

 ● Which color do you want?
   → Purple please

Ready to submit your answers?

❯ 1. Submit answers
  2. Cancel`

	appTestAskClaudeTrust = `────────────────────────────────────────────────────────────────────
 Accessing workspace:

 /box/home/project

 Quick safety check: Is this a project you created or one you trust?

 ❯ No, exit
   Yes, I trust this folder

 Enter to confirm · Esc to cancel`

	appTestAskCodexApproval = `› Run the shell command: touch made.txt

• Running touch made.txt

  Would you like to run the following command?

  Environment: local

  Reason: Allow creating made.txt?

  $ touch made.txt


› 1. Yes, proceed (y)
  2. Yes, and don't ask again for commands that start with ` + "`touch made.txt`" + ` (p)
  3. No, and tell Codex what to do differently (esc)

  Press enter to confirm or esc to cancel`

	appTestAskCodexInput = `› Before planning anything, ask me two questions
  (Red, Green, Blue) and which size (Small, Large).

  Question 1/2 (2 unanswered)
  Which color?

  › 1. Red                Choose red.
    2. Green              Choose green.
    3. None of the above  Optionally, add details in notes (tab)

  tab to add notes | enter to submit answer | ←/→ to navigate questions | esc to interrupt`
)

func TestTeamDevParseAsk(t *testing.T) {
	brief := func(ask *appDevAsk) string {
		if ask == nil {
			return "nil"
		}
		var options []string
		for _, o := range ask.Options {
			s := o.Key + "." + o.Label
			if o.Detail != "" {
				s += " (" + o.Detail + ")"
			}
			if o.Cursor {
				s = ">" + s
			}
			if o.Checked {
				s += " [x]"
			} else if o.Box {
				s += " [ ]"
			}
			if o.Typed {
				s += " typed"
			}
			options = append(options, s)
		}
		tabs, _ := json.Marshal(ask.Tabs)
		return strings.Join(ask.Text, " / ") + " | " + strings.Join(options, "; ") + " | " + ask.Progress + string(tabs)
	}
	for _, c := range []struct{ name, screen, want string }{
		{"claude", appTestAskClaude, `Which color do you want? | 1.Red (A warm, bold red); >2.Green (A fresh, natural green); 3.Blue (A cool, calm blue); 4.Type something. typed; 5.Chat about this | [{"label":"Color"},{"label":"Toppings"},{"label":"Submit"}]`},
		{"claude multi", appTestAskClaudeMulti, `Which toppings do you want? | >1.Cheese (Classic melted cheese) [ ]; 2.Ham (Sliced smoked ham) [x]; 3.Type something [ ] typed; .Submit; 4.Chat about this | [{"label":"Color","done":true},{"label":"Toppings","done":true},{"label":"Submit","done":true}]`},
		{"claude review", appTestAskClaudeReview, "Review your answers / ● Which color do you want?\n→ Purple please / Ready to submit your answers? | >1.Submit answers; 2.Cancel | " + `[{"label":"Color","done":true},{"label":"Toppings","done":true},{"label":"Submit","done":true}]`},
		{"claude trust", appTestAskClaudeTrust, "Accessing workspace: / /box/home/project / Quick safety check: Is this a project you created or one you trust? | >.No, exit; .Yes, I trust this folder | null"},
		{"codex approval", appTestAskCodexApproval, "Would you like to run the following command? / Environment: local / Reason: Allow creating made.txt? / $ touch made.txt | >1.Yes, proceed (y); 2.Yes, and don't ask again for commands that start with `touch made.txt` (p); 3.No, and tell Codex what to do differently (esc) | null"},
		{"codex input", appTestAskCodexInput, "Which color? | >1.Red (Choose red.); 2.Green (Choose green.); 3.None of the above (Optionally, add details in notes (tab)) typed | Question 1/2 (2 unanswered)null"},
		{"a long question", "←  ☐ Stop  ✔ Submit  →\n\n│ How does 2% risk size a position? It needs a stop\n│ distance.\n\n❯ 1. ATR-based stop\n  2. Fixed %", `How does 2% risk size a position? It needs a stop
distance. | >1.ATR-based stop; 2.Fixed % | [{"label":"Stop"},{"label":"Submit"}]`},
		{"nothing asked", "⏺ Done.\n\n> ", "nil"},
	} {
		if got := brief(appDevParseAsk(c.screen)); got != c.want {
			t.Errorf("%s:\n got %s\nwant %s", c.name, got, c.want)
		}
	}
}

func TestTeamDevAskKeys(t *testing.T) {
	for _, c := range []struct {
		name, screen, agent string
		option              int
		text, want          string
	}{
		{"a number picks", appTestAskClaude, "claude", 2, "", "3"},
		{"Claude's own words", appTestAskClaude, "claude", 3, "Purple", "4 +text"},
		{"a box ticks", appTestAskClaudeMulti, "claude", 0, "", "1"},
		{"Submit, with the arrows", appTestAskClaudeMulti, "claude", 3, "", "down down down enter"},
		{"a dialog", appTestAskClaudeTrust, "claude", 1, "", "down enter"},
		{"Codex's notes", appTestAskCodexInput, "codex", 2, "Purple", "down down tab +text"},
		{"Codex's approval", appTestAskCodexApproval, "codex", 2, "", "3"},
	} {
		keys, typed, err := appDevAskKeys(appDevParseAsk(c.screen), c.agent, c.option, c.text)
		got := strings.Join(keys, " ")
		if typed {
			got += " +text"
		}
		if err != nil || got != c.want {
			t.Errorf("%s: %q %v, want %q", c.name, got, err, c.want)
		}
	}
	if _, _, err := appDevAskKeys(appDevParseAsk(appTestAskClaude), "claude", 3, " "); err == nil {
		t.Error("typed nothing")
	}
}

// The chat answers through herdr: the option's keys, and the person's
// words typed after them; not when the question changed.
func TestTeamDevChatAnswer(t *testing.T) {
	b, ran := appTestDevPage(t)
	handler := b.devPageHandler()
	appDevAskPause = 0
	screen := `  ⎿  Allowed by auto mode classifier

  Listed 1 directory (ctrl+o to expand)
────────────────────────────────────────────────────────────────────
←  ☐ MA type  ☐ Sizing  ☐ Round  ☐ Data  ✔ Submit  →

Moving averages 10/20/50/200: SMA or EMA?

❯ 1. SMA, with a toggle to EMA (Recommended)
     Default SMA; a button switches all four lines to EMA.
  2. SMA only
     Simplest.
  3. EMA only
     Faster-reacting lines.
  4. Type something.
────────────────────────────────────────────────────────────────────
  5. Chat about this

Enter to select · Tab/Arrow keys to navigate · Esc to cancel`
	previous := appDevBoxExec
	appDevBoxExec = func(team string, command ...string) (string, error) {
		switch strings.Join(command[:min(3, len(command))], " ") {
		case "herdr pane list":
			return `{"result":{"panes":[{"pane_id":"w2:p1","agent":"claude","agent_status":"blocked","cwd":"/box/home/.herdr/worktrees/repo/1-chart"}]}}`, nil
		case "herdr pane read":
			return screen, nil
		}
		return previous(team, command...)
	}
	var view appDevChatView
	_, body := appTestDevPageCall(t, handler, appTestOwnerKey, "GET", "api/chat?workspace=w2", nil)
	json.Unmarshal([]byte(body), &view)
	if view.Ask == nil || strings.Join(view.Ask.Text, "") != "Moving averages 10/20/50/200: SMA or EMA?" || len(view.Ask.Options) != 5 || len(view.Ask.Tabs) != 5 {
		t.Fatalf("%s", body)
	}
	answer := func(option int, label, typed string) int {
		*ran = nil
		code, _ := appTestDevPageCall(t, handler, appTestOwnerKey, "POST", "api/chat/answer", map[string]any{"workspace": "w2", "text": view.Ask.Text, "option": option, "label": label, "typed": typed})
		return code
	}
	if code := answer(1, "SMA only", ""); code != 200 || strings.Join(*ran, "\n") != "herdr agent send-keys w2:p1 2" {
		t.Fatal(code, *ran)
	}
	if code := answer(3, "Type something.", "Both, EMA by default"); code != 200 || strings.Join(*ran, "\n") != "herdr agent send-keys w2:p1 4\nherdr pane send-text w2:p1 Both, EMA by default\nherdr agent send-keys w2:p1 enter" {
		t.Fatal(code, *ran)
	}
	if code := answer(1, "EMA only", ""); code == 200 || len(*ran) != 0 {
		t.Fatal("answered a question that changed", *ran)
	}
	if code, _ := appTestDevPageCall(t, handler, appTestAskerKey, "POST", "api/chat/answer", map[string]any{"workspace": "w2", "text": view.Ask.Text, "option": 1, "label": "SMA only"}); code == 200 {
		t.Fatal("someone who isn't an owner answered")
	}
}

// Claude Code's review of its answers has no "Esc to cancel" under it, so
// herdr doesn't call it blocked; the chat asks it all the same.
func TestTeamDevChatReview(t *testing.T) {
	b, ran := appTestDevPage(t)
	handler := b.devPageHandler()
	status, screen := "working", "● I've read the task.\n\n"+appTestAskClaudeReview
	previous := appDevBoxExec
	appDevBoxExec = func(team string, command ...string) (string, error) {
		switch strings.Join(command[:min(3, len(command))], " ") {
		case "herdr pane list":
			return `{"result":{"panes":[{"pane_id":"w2:p1","agent":"claude","agent_status":"` + status + `","cwd":"/box/home/.herdr/worktrees/repo/1-chart"}]}}`, nil
		case "herdr pane read":
			return screen, nil
		}
		return previous(team, command...)
	}
	var view appDevChatView
	_, body := appTestDevPageCall(t, handler, appTestOwnerKey, "GET", "api/chat?workspace=w2", nil)
	json.Unmarshal([]byte(body), &view)
	if view.Agent.Status != "blocked" || view.Ask == nil || len(view.Ask.Options) != 2 || view.Ask.Options[0].Label != "Submit answers" {
		t.Fatalf("%s", body)
	}
	*ran = nil
	code, _ := appTestDevPageCall(t, handler, appTestOwnerKey, "POST", "api/chat/answer", map[string]any{"workspace": "w2", "text": view.Ask.Text, "option": 0, "label": "Submit answers"})
	if code != 200 || strings.Join(*ran, "\n") != "herdr agent send-keys w2:p1 1" {
		t.Fatal(code, *ran)
	}
	// Idle at its prompt, Claude isn't asking anything.
	status, screen = "idle", "● Done.\n\n────────\n❯ \n────────"
	view = appDevChatView{}
	_, body = appTestDevPageCall(t, handler, appTestOwnerKey, "GET", "api/chat?workspace=w2", nil)
	json.Unmarshal([]byte(body), &view)
	if view.Agent.Status != "idle" || view.Ask != nil {
		t.Fatalf("%s", body)
	}
}

// The tab Claude Code is on has a background; the screen's text doesn't say.
func TestTeamDevReadAskTab(t *testing.T) {
	ansi := "←  \x1b[38;2;0;0;0m☒ MA type \x1b[0m\x1b[38;2;0;0;0m\x1b[48;2;177;185;249m ☐ Sizing  \x1b[0m☐ Round  ✔ Submit  →\r\n\nSize?\n\n❯ 1. A\n  2. B"
	text := appDevANSI.ReplaceAllString(strings.ReplaceAll(ansi, "\r", ""), "")
	ask := appDevReadAsk(text, ansi)
	var current []string
	for _, tab := range ask.Tabs {
		if tab.Current {
			current = append(current, tab.Label)
		}
	}
	if strings.Join(current, ",") != "Sizing" || ask.Tabs[3].Done || !ask.Tabs[0].Done {
		t.Fatalf("%+v", ask.Tabs)
	}
}
