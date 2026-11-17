package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// A question in an agent's terminal, read from its screen so the chat can
// show it as options to tap rather than a terminal to drive with keys:
// Claude Code's AskUserQuestion, permission prompts and dialogs, and Codex's
// approvals and request_user_input. Neither writes a question to its
// transcript until it's answered, so the screen is where it is.

// appDevAsk is the question on the screen.
type appDevAsk struct {
	// Question is its words as the agent wrote them, from its transcript,
	// unwrapped: for the page to show; Text, from the screen, is what an
	// answer is checked against.
	Question string            `json:"question,omitempty"`
	Tabs     []appDevAskTab    `json:"tabs,omitempty"`     // Claude Code's questions, a tab each, and Submit
	Progress string            `json:"progress,omitempty"` // Codex's "Question 1/2 (2 unanswered)"
	Text     []string          `json:"text"`               // what it says, a paragraph each
	Options  []appDevAskOption `json:"options"`
	Multi    bool              `json:"multi,omitempty"` // check boxes: a pick toggles one
	// Arrows says the options are picked with the arrows and Enter, not
	// their numbers: Claude Code's, with a preview beside them.
	Arrows bool `json:"-"`
}

type appDevAskTab struct {
	Label   string `json:"label"`
	Done    bool   `json:"done,omitempty"`
	Current bool   `json:"current,omitempty"` // the question on the screen
}

type appDevAskOption struct {
	Key     string `json:"key,omitempty"` // its number, when it has one
	Label   string `json:"label"`
	Detail  string `json:"detail,omitempty"`
	Cursor  bool   `json:"cursor,omitempty"` // where the terminal's selection is
	Box     bool   `json:"box,omitempty"`    // a check box, ticked or not
	Checked bool   `json:"checked,omitempty"`
	Typed   bool   `json:"typed,omitempty"` // an answer in the person's own words
	// From the transcript, for the page to show: the label unwrapped, its
	// description, and its preview (Claude Code's box beside the options,
	// monospace). Label, from the screen, is what an answer is checked
	// against.
	Title   string `json:"title,omitempty"`
	About   string `json:"about,omitempty"`
	Preview string `json:"preview,omitempty"`
}

// appDevAskBox is a line of a box drawn beside the options, as Claude
// Code draws an option's preview: not a part of any option.
var appDevAskBox = regexp.MustCompile(`^\s*[│┌└├╭╰|]`)

// appDevAskQuestion is one of AskUserQuestion's questions, as the agent
// wrote it in its transcript.
type appDevAskQuestion struct {
	Question string `json:"question"`
	Header   string `json:"header"`
	Multi    bool   `json:"multiSelect"`
	Options  []struct {
		Label       string `json:"label"`
		Description string `json:"description"`
		Preview     string `json:"preview"`
	} `json:"options"`
}

// appDevAskPending is Claude Code's AskUserQuestion still waiting for an
// answer, from the end of its transcript: the tool's call without its
// result.
func appDevAskPending(home, host string) []appDevAskQuestion {
	file, err := boxedOpenFile(home, host)
	if err != nil {
		return nil
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil
	}
	const tail = 2 << 20
	at := max(0, info.Size()-tail)
	data := make([]byte, info.Size()-at)
	if _, err := file.ReadAt(data, at); err != nil && !errors.Is(err, io.EOF) {
		return nil
	}
	lines := bytes.Split(data, []byte("\n"))
	answered := map[string]bool{}
	for i := len(lines) - 1; i >= 0; i-- {
		line := lines[i]
		if !bytes.Contains(line, []byte("tool_")) {
			continue
		}
		var entry struct {
			Message struct {
				Content []struct {
					Type      string          `json:"type"`
					Name      string          `json:"name"`
					ID        string          `json:"id"`
					ToolUseID string          `json:"tool_use_id"`
					Input     json.RawMessage `json:"input"`
				} `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(line, &entry) != nil {
			continue
		}
		for _, part := range entry.Message.Content {
			switch {
			case part.Type == "tool_result":
				answered[part.ToolUseID] = true
			case part.Type == "tool_use" && part.Name == "AskUserQuestion":
				if answered[part.ID] {
					return nil
				}
				var input struct {
					Questions []appDevAskQuestion `json:"questions"`
				}
				if json.Unmarshal(part.Input, &input) != nil {
					return nil
				}
				return input.Questions
			}
		}
	}
	return nil
}

// appDevAskWords is text with its spaces and line breaks as one space, to
// compare the screen's wrapped lines with what was written.
func appDevAskWords(text string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(text, "│", " ")), " ")
}

// appDevAskWithQuestions adds the transcript's words and previews to the
// question on the screen, when it's the same question: its tab's header,
// or its words, and each option's label as the screen starts it.
func appDevAskWithQuestions(ask *appDevAsk, questions []appDevAskQuestion) {
	if ask == nil || len(questions) == 0 {
		return
	}
	var question *appDevAskQuestion
	screen := appDevAskWords(strings.Join(ask.Text, " "))
	for i := range questions {
		q := &questions[i]
		for _, tab := range ask.Tabs {
			if tab.Current && strings.TrimSpace(tab.Label) == strings.TrimSpace(q.Header) {
				question = q
			}
		}
		if question == nil && screen != "" && appDevAskWords(q.Question) == screen {
			question = q
		}
	}
	if question == nil {
		return
	}
	for i, option := range ask.Options {
		if i >= len(question.Options) {
			break
		}
		label := strings.TrimSuffix(appDevAskWords(option.Label), "…")
		if label == "" || !strings.HasPrefix(appDevAskWords(question.Options[i].Label), label) {
			return // not the options the transcript has: the screen's, as they are
		}
	}
	ask.Question = question.Question
	for i := range ask.Options {
		if i < len(question.Options) {
			ask.Options[i].Title = question.Options[i].Label
			ask.Options[i].About = question.Options[i].Description
			ask.Options[i].Preview = strings.TrimRight(question.Options[i].Preview, "\n ")
		}
	}
}

var (
	appDevAskRule     = regexp.MustCompile(`^\s*─{10,}\s*$`)
	appDevAskTabBar   = regexp.MustCompile(`^\s*←\s+(.*?)\s+→\s*$`)
	appDevAskTabItem  = regexp.MustCompile(`([☐☒✔])\s+(.+?)(?:\s{2,}|$)`)
	appDevAskProgress = regexp.MustCompile(`^\s*(Question \d+/\d+\b.*?)\s*$`)
	// The selected option: Claude Code's ❯, Codex's ›, agy's >.
	appDevAskNumbered = regexp.MustCompile(`^(\s*)([❯›>]\s*)?(\d+)\.\s+(?:\[([ ✔✓x×])\]\s+)?(.*?)\s*$`)
	appDevAskCursor   = regexp.MustCompile(`^(\s*)[❯›]\s+(.*?)\s*$`)
	// appDevAskHistory is a line of the session above the question: a
	// message, a step, or the prompt it answers.
	appDevAskHistory = regexp.MustCompile(`^[•●⏺›❯✔✻⎿] `)
)

// appDevParseAsk reads the question on an agent's screen, or nil when it
// can't tell what's asked; the chat shows the terminal then.
func appDevParseAsk(screen string) *appDevAsk {
	lines := strings.Split(strings.TrimRight(screen, "\n "), "\n")
	// The selection: the last numbered option it's on, or a dialog's.
	cursor := -1
	for i := len(lines) - 1; i >= 0 && cursor < 0; i-- {
		if m := appDevAskNumbered.FindStringSubmatch(lines[i]); m != nil && m[2] != "" {
			cursor = i
		}
	}
	numbered := cursor >= 0
	for i := len(lines) - 1; i >= 0 && cursor < 0; i-- {
		if appDevAskCursor.MatchString(lines[i]) {
			cursor = i
		}
	}
	if cursor < 0 {
		return nil
	}
	ask := &appDevAsk{Text: []string{}, Options: []appDevAskOption{}}
	// Where the question starts: under a rule, its tabs, or the session.
	start := max(0, cursor-40)
	for i := cursor - 1; i >= start; i-- {
		line := lines[i]
		if m := appDevAskTabBar.FindStringSubmatch(line); m != nil {
			done := true
			for _, tab := range appDevAskTabItem.FindAllStringSubmatch(m[1], -1) {
				if tab[1] == "✔" {
					// Submit, always with a tick: done once the questions are.
					ask.Tabs = append(ask.Tabs, appDevAskTab{Label: tab[2], Done: done})
					continue
				}
				ask.Tabs = append(ask.Tabs, appDevAskTab{Label: tab[2], Done: tab[1] == "☒"})
				done = done && tab[1] == "☒"
			}
			start = i + 1
			break
		}
		if m := appDevAskProgress.FindStringSubmatch(line); m != nil {
			ask.Progress, start = m[1], i+1
			break
		}
		if appDevAskRule.MatchString(line) && !appDevAskRuleInside(lines, i) {
			start = i + 1
			break
		}
		if !appDevAskNumbered.MatchString(line) && appDevAskHistory.MatchString(line) {
			start = i + 1
			break
		}
	}
	first := cursor
	if numbered {
		for i := start; i < cursor; i++ {
			if appDevAskNumbered.MatchString(lines[i]) {
				first = i
				break
			}
		}
		ask.Options = appDevAskNumberedOptions(lines[first:], ask)
	} else {
		first, ask.Options = appDevAskDialogOptions(lines, cursor)
	}
	var paragraph []string
	for _, line := range append(lines[start:first], "") {
		// A long question has a bar down its left.
		if line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "│")); line == "" || appDevAskRule.MatchString(line) {
			if len(paragraph) > 0 {
				ask.Text = append(ask.Text, strings.Join(paragraph, "\n"))
			}
			paragraph = nil
			continue
		}
		paragraph = append(paragraph, line)
	}
	if len(ask.Options) == 0 {
		return nil
	}
	return ask
}

// appDevAskTabbed is Claude Code's AskUserQuestion, with its tabs and
// Submit: a question even when herdr doesn't see one.
func appDevAskTabbed(ask *appDevAsk) bool {
	return ask != nil && len(ask.Tabs) > 0
}

// appDevReadAsk is the question on a screen, with the tab it's on from
// the screen's colors: Claude Code marks it with a background.
func appDevReadAsk(text, ansi string) *appDevAsk {
	ask := appDevParseAsk(text)
	if ask == nil || len(ask.Tabs) == 0 {
		return ask
	}
	for _, line := range strings.Split(ansi, "\n") {
		if !appDevAskTabBar.MatchString(appDevANSI.ReplaceAllString(strings.TrimRight(line, "\r"), "")) {
			continue
		}
		tab, marked := 0, false
		for _, part := range appDevANSIParts.FindAllStringSubmatch(line, -1) {
			if part[1] != "" {
				codes := strings.Split(part[1], ";")
				for i := 0; i < len(codes); i++ {
					switch codes[i] {
					case "", "0", "49", "27":
						marked = false
					case "48", "7":
						marked = true
					}
					if (codes[i] == "38" || codes[i] == "48" || codes[i] == "58") && i+1 < len(codes) {
						// A color's own numbers: 5;n, or 2;r;g;b.
						if codes[i+1] == "5" {
							i += 2
						} else if codes[i+1] == "2" {
							i += 4
						}
					}
				}
				continue
			}
			for _, r := range part[2] {
				if strings.ContainsRune("☐☒✔", r) {
					if marked && tab < len(ask.Tabs) {
						ask.Tabs[tab].Current = true
					}
					tab++
				}
			}
		}
	}
	return ask
}

// appDevANSIParts splits a line into its colors and its text.
var appDevANSIParts = regexp.MustCompile(`\x1b\[([0-9;]*)m|([^\x1b]+)`)

// appDevAskRuleInside is a rule between options, as Claude Code draws
// above "Chat about this", not one above the question.
func appDevAskRuleInside(lines []string, rule int) bool {
	for _, line := range lines[rule+1:] {
		if strings.TrimSpace(line) != "" {
			return appDevAskNumbered.MatchString(line)
		}
	}
	return false
}

// appDevAskNumberedOptions reads numbered options from the first: each
// with its detail, under it (Claude Code) or beside it (Codex), and the
// unnumbered Submit under Claude Code's check boxes. It stops at what
// isn't an option, the keys' line.
func appDevAskNumberedOptions(lines []string, ask *appDevAsk) []appDevAskOption {
	var options []appDevAskOption
	labelColumn, numberColumn := 0, 0
	for _, line := range lines {
		// A preview's box beside the options isn't theirs: each line up to it.
		if at := strings.IndexAny(line, "│┌└├╭╰"); at >= 0 {
			if appDevAskNumbered.MatchString(line[:at]) {
				ask.Arrows = true // its footer: "Enter to select · ↑/↓ to navigate"
			}
			if line = strings.TrimRight(line[:at], " "); strings.TrimSpace(line) == "" {
				continue
			}
		}
		if strings.TrimSpace(line) == "" || appDevAskRule.MatchString(line) {
			continue
		}
		if m := appDevAskNumbered.FindStringSubmatchIndex(line); m != nil {
			group := func(n int) string {
				if m[2*n] < 0 {
					return ""
				}
				return line[m[2*n]:m[2*n+1]]
			}
			label, detail, _ := strings.Cut(group(5), "  ")
			option := appDevAskOption{Key: group(3), Label: label, Detail: strings.TrimSpace(detail), Cursor: group(2) != ""}
			if m[8] >= 0 {
				ask.Multi, option.Box = true, true
				option.Checked = group(4) != " "
				numberColumn = utf8.RuneCountInString(line[:m[8]]) - 1
			} else {
				numberColumn = utf8.RuneCountInString(line[:m[10]])
			}
			labelColumn = utf8.RuneCountInString(line[:m[10]])
			option.Typed = strings.HasPrefix(label, "Type something") || strings.HasPrefix(label, "Write-in") || strings.Contains(option.Detail, "notes (tab)")
			options = append(options, option)
			continue
		}
		indent := utf8.RuneCountInString(line) - utf8.RuneCountInString(strings.TrimLeft(line, " "))
		if appDevAskBox.MatchString(line) || strings.HasPrefix(strings.TrimSpace(line), "Notes:") {
			continue // a preview's box beside the options, and its notes' line
		}
		switch {
		case len(options) > 0 && indent >= labelColumn:
			last := &options[len(options)-1]
			last.Detail = strings.TrimSpace(last.Detail + " " + strings.TrimSpace(line))
		case ask.Multi && indent == numberColumn:
			options = append(options, appDevAskOption{Label: strings.TrimSpace(line)})
		default:
			return options
		}
	}
	return options
}

// appDevAskDialogOptions reads a dialog's options, unnumbered: the lines
// beside the selection that line up with it.
func appDevAskDialogOptions(lines []string, cursor int) (int, []appDevAskOption) {
	m := appDevAskCursor.FindStringSubmatchIndex(lines[cursor])
	column := utf8.RuneCountInString(lines[cursor][:m[4]])
	lined := func(line string) bool {
		indent := utf8.RuneCountInString(line) - utf8.RuneCountInString(strings.TrimLeft(line, " "))
		return strings.TrimSpace(line) != "" && indent == column
	}
	first := cursor
	for first > 0 && lined(lines[first-1]) {
		first--
	}
	var options []appDevAskOption
	for i := first; i < len(lines); i++ {
		if i != cursor && !lined(lines[i]) {
			break
		}
		label := strings.TrimSpace(lines[i])
		if i == cursor {
			label = lines[cursor][m[4]:m[5]]
		}
		options = append(options, appDevAskOption{Label: label, Cursor: i == cursor})
	}
	return first, options
}

// appDevAskKeys are the keys that answer with an option, and the text to
// type after them and Enter, for an answer in the person's words. A number
// picks its option (or ticks its box); an option without one is reached
// with the arrows.
func appDevAskKeys(ask *appDevAsk, agent string, index int, text string) (keys []string, typed bool, err error) {
	if index < 0 || index >= len(ask.Options) {
		return nil, false, errors.New("no such option")
	}
	option := ask.Options[index]
	at := 0
	for i, each := range ask.Options {
		if each.Cursor {
			at = i
		}
	}
	move := func() []string {
		var keys []string
		for ; at < index; at++ {
			keys = append(keys, "down")
		}
		for ; at > index; at-- {
			keys = append(keys, "up")
		}
		return keys
	}
	switch {
	case option.Typed && strings.TrimSpace(text) == "":
		return nil, false, errors.New("say what, for " + option.Label)
	case agent == "agy":
		// agy picks with the arrows and Enter (its footer says so), not a
		// number; its Write-in takes the words after.
		return append(move(), "enter"), option.Typed, nil
	case option.Typed:
		if agent == "codex" {
			return append(move(), "tab"), true, nil // Codex's notes, beside the option
		}
		return []string{option.Key}, true, nil
	case option.Key != "" && !ask.Arrows:
		return []string{option.Key}, false, nil
	default:
		return append(move(), "enter"), false, nil
	}
}

// appDevAskPause lets the terminal catch up between keys and typing.
var appDevAskPause = 400 * time.Millisecond

// answerDevAsk answers the question on an agent's screen with an option,
// when it's still the one the page shows.
func (b *appBroker) answerDevAsk(agent appDevChatAgent, text []string, index int, label, typed string) error {
	if !agent.Running || agent.Status != "blocked" && agent.Agent != "claude" {
		return errors.New("the agent isn't asking anything now")
	}
	screen, _, err := devChatScreen(b.team, agent.Pane)
	if err != nil {
		return err
	}
	ask := appDevParseAsk(screen)
	if agent.Status != "blocked" && !appDevAskTabbed(ask) {
		return errors.New("the agent isn't asking anything now")
	}
	if ask == nil || strings.Join(ask.Text, "\n") != strings.Join(text, "\n") || index < 0 || index >= len(ask.Options) || ask.Options[index].Label != label {
		return errors.New("the question changed; look at it again")
	}
	keys, typing, err := appDevAskKeys(ask, agent.Agent, index, typed)
	if err != nil {
		return err
	}
	if len(typed) > appDevChatTextMax {
		return fmt.Errorf("the answer is longer than %s", formatDataSize(appDevChatTextMax))
	}
	if err := appDevHerdrDo(b.team, append([]string{"agent", "send-keys", agent.Pane}, keys...)...); err != nil || !typing {
		return err
	}
	time.Sleep(appDevAskPause)
	if err := appDevHerdrDo(b.team, "pane", "send-text", agent.Pane, strings.TrimSpace(typed)); err != nil {
		return err
	}
	time.Sleep(appDevAskPause)
	return appDevHerdrDo(b.team, "agent", "send-keys", agent.Pane, "enter")
}
