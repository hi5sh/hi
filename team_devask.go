package main

import (
	"errors"
	"fmt"
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

// teamDevAsk is the question on the screen.
type teamDevAsk struct {
	Tabs     []teamDevAskTab    `json:"tabs,omitempty"`     // Claude Code's questions, a tab each, and Submit
	Progress string             `json:"progress,omitempty"` // Codex's "Question 1/2 (2 unanswered)"
	Text     []string           `json:"text"`               // what it says, a paragraph each
	Options  []teamDevAskOption `json:"options"`
	Multi    bool               `json:"multi,omitempty"` // check boxes: a pick toggles one
}

type teamDevAskTab struct {
	Label   string `json:"label"`
	Done    bool   `json:"done,omitempty"`
	Current bool   `json:"current,omitempty"` // the question on the screen
}

type teamDevAskOption struct {
	Key     string `json:"key,omitempty"` // its number, when it has one
	Label   string `json:"label"`
	Detail  string `json:"detail,omitempty"`
	Cursor  bool   `json:"cursor,omitempty"` // where the terminal's selection is
	Box     bool   `json:"box,omitempty"`    // a check box, ticked or not
	Checked bool   `json:"checked,omitempty"`
	Typed   bool   `json:"typed,omitempty"` // an answer in the person's own words
}

var (
	teamDevAskRule     = regexp.MustCompile(`^\s*─{10,}\s*$`)
	teamDevAskTabBar   = regexp.MustCompile(`^\s*←\s+(.*?)\s+→\s*$`)
	teamDevAskTabItem  = regexp.MustCompile(`([☐☒✔])\s+(.+?)(?:\s{2,}|$)`)
	teamDevAskProgress = regexp.MustCompile(`^\s*(Question \d+/\d+\b.*?)\s*$`)
	teamDevAskNumbered = regexp.MustCompile(`^(\s*)([❯›]\s*)?(\d+)\.\s+(?:\[([ ✔✓x×])\]\s+)?(.*?)\s*$`)
	teamDevAskCursor   = regexp.MustCompile(`^(\s*)[❯›]\s+(.*?)\s*$`)
	// teamDevAskHistory is a line of the session above the question: a
	// message, a step, or the prompt it answers.
	teamDevAskHistory = regexp.MustCompile(`^[•●⏺›❯✔✻⎿] `)
)

// teamDevParseAsk reads the question on an agent's screen, or nil when it
// can't tell what's asked; the chat shows the terminal then.
func teamDevParseAsk(screen string) *teamDevAsk {
	lines := strings.Split(strings.TrimRight(screen, "\n "), "\n")
	// The selection: the last numbered option it's on, or a dialog's.
	cursor := -1
	for i := len(lines) - 1; i >= 0 && cursor < 0; i-- {
		if m := teamDevAskNumbered.FindStringSubmatch(lines[i]); m != nil && m[2] != "" {
			cursor = i
		}
	}
	numbered := cursor >= 0
	for i := len(lines) - 1; i >= 0 && cursor < 0; i-- {
		if teamDevAskCursor.MatchString(lines[i]) {
			cursor = i
		}
	}
	if cursor < 0 {
		return nil
	}
	ask := &teamDevAsk{Text: []string{}, Options: []teamDevAskOption{}}
	// Where the question starts: under a rule, its tabs, or the session.
	start := max(0, cursor-40)
	for i := cursor - 1; i >= start; i-- {
		line := lines[i]
		if m := teamDevAskTabBar.FindStringSubmatch(line); m != nil {
			done := true
			for _, tab := range teamDevAskTabItem.FindAllStringSubmatch(m[1], -1) {
				if tab[1] == "✔" {
					// Submit, always with a tick: done once the questions are.
					ask.Tabs = append(ask.Tabs, teamDevAskTab{Label: tab[2], Done: done})
					continue
				}
				ask.Tabs = append(ask.Tabs, teamDevAskTab{Label: tab[2], Done: tab[1] == "☒"})
				done = done && tab[1] == "☒"
			}
			start = i + 1
			break
		}
		if m := teamDevAskProgress.FindStringSubmatch(line); m != nil {
			ask.Progress, start = m[1], i+1
			break
		}
		if teamDevAskRule.MatchString(line) && !teamDevAskRuleInside(lines, i) {
			start = i + 1
			break
		}
		if !teamDevAskNumbered.MatchString(line) && teamDevAskHistory.MatchString(line) {
			start = i + 1
			break
		}
	}
	first := cursor
	if numbered {
		for i := start; i < cursor; i++ {
			if teamDevAskNumbered.MatchString(lines[i]) {
				first = i
				break
			}
		}
		ask.Options = teamDevAskNumberedOptions(lines[first:], ask)
	} else {
		first, ask.Options = teamDevAskDialogOptions(lines, cursor)
	}
	var paragraph []string
	for _, line := range append(lines[start:first], "") {
		// A long question has a bar down its left.
		if line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "│")); line == "" || teamDevAskRule.MatchString(line) {
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

// teamDevReadAsk is the question on a screen, with the tab it's on from
// the screen's colors: Claude Code marks it with a background.
func teamDevReadAsk(text, ansi string) *teamDevAsk {
	ask := teamDevParseAsk(text)
	if ask == nil || len(ask.Tabs) == 0 {
		return ask
	}
	for _, line := range strings.Split(ansi, "\n") {
		if !teamDevAskTabBar.MatchString(teamDevANSI.ReplaceAllString(strings.TrimRight(line, "\r"), "")) {
			continue
		}
		tab, marked := 0, false
		for _, part := range teamDevANSIParts.FindAllStringSubmatch(line, -1) {
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

// teamDevANSIParts splits a line into its colors and its text.
var teamDevANSIParts = regexp.MustCompile(`\x1b\[([0-9;]*)m|([^\x1b]+)`)

// teamDevAskRuleInside is a rule between options, as Claude Code draws
// above "Chat about this", not one above the question.
func teamDevAskRuleInside(lines []string, rule int) bool {
	for _, line := range lines[rule+1:] {
		if strings.TrimSpace(line) != "" {
			return teamDevAskNumbered.MatchString(line)
		}
	}
	return false
}

// teamDevAskNumberedOptions reads numbered options from the first: each
// with its detail, under it (Claude Code) or beside it (Codex), and the
// unnumbered Submit under Claude Code's check boxes. It stops at what
// isn't an option, the keys' line.
func teamDevAskNumberedOptions(lines []string, ask *teamDevAsk) []teamDevAskOption {
	var options []teamDevAskOption
	labelColumn, numberColumn := 0, 0
	for _, line := range lines {
		if strings.TrimSpace(line) == "" || teamDevAskRule.MatchString(line) {
			continue
		}
		if m := teamDevAskNumbered.FindStringSubmatchIndex(line); m != nil {
			group := func(n int) string {
				if m[2*n] < 0 {
					return ""
				}
				return line[m[2*n]:m[2*n+1]]
			}
			label, detail, _ := strings.Cut(group(5), "  ")
			option := teamDevAskOption{Key: group(3), Label: label, Detail: strings.TrimSpace(detail), Cursor: group(2) != ""}
			if m[8] >= 0 {
				ask.Multi, option.Box = true, true
				option.Checked = group(4) != " "
				numberColumn = utf8.RuneCountInString(line[:m[8]]) - 1
			} else {
				numberColumn = utf8.RuneCountInString(line[:m[10]])
			}
			labelColumn = utf8.RuneCountInString(line[:m[10]])
			option.Typed = strings.HasPrefix(label, "Type something") || strings.Contains(option.Detail, "notes (tab)")
			options = append(options, option)
			continue
		}
		indent := utf8.RuneCountInString(line) - utf8.RuneCountInString(strings.TrimLeft(line, " "))
		switch {
		case len(options) > 0 && indent >= labelColumn:
			last := &options[len(options)-1]
			last.Detail = strings.TrimSpace(last.Detail + " " + strings.TrimSpace(line))
		case ask.Multi && indent == numberColumn:
			options = append(options, teamDevAskOption{Label: strings.TrimSpace(line)})
		default:
			return options
		}
	}
	return options
}

// teamDevAskDialogOptions reads a dialog's options, unnumbered: the lines
// beside the selection that line up with it.
func teamDevAskDialogOptions(lines []string, cursor int) (int, []teamDevAskOption) {
	m := teamDevAskCursor.FindStringSubmatchIndex(lines[cursor])
	column := utf8.RuneCountInString(lines[cursor][:m[4]])
	lined := func(line string) bool {
		indent := utf8.RuneCountInString(line) - utf8.RuneCountInString(strings.TrimLeft(line, " "))
		return strings.TrimSpace(line) != "" && indent == column
	}
	first := cursor
	for first > 0 && lined(lines[first-1]) {
		first--
	}
	var options []teamDevAskOption
	for i := first; i < len(lines); i++ {
		if i != cursor && !lined(lines[i]) {
			break
		}
		label := strings.TrimSpace(lines[i])
		if i == cursor {
			label = lines[cursor][m[4]:m[5]]
		}
		options = append(options, teamDevAskOption{Label: label, Cursor: i == cursor})
	}
	return first, options
}

// teamDevAskKeys are the keys that answer with an option, and the text to
// type after them and Enter, for an answer in the person's words. A number
// picks its option (or ticks its box); an option without one is reached
// with the arrows.
func teamDevAskKeys(ask *teamDevAsk, agent string, index int, text string) (keys []string, typed bool, err error) {
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
	case option.Typed:
		if strings.TrimSpace(text) == "" {
			return nil, false, errors.New("say what, for " + option.Label)
		}
		if agent == "codex" {
			return append(move(), "tab"), true, nil // Codex's notes, beside the option
		}
		return []string{option.Key}, true, nil
	case option.Key != "":
		return []string{option.Key}, false, nil
	default:
		return append(move(), "enter"), false, nil
	}
}

// teamDevAskPause lets the terminal catch up between keys and typing.
var teamDevAskPause = 400 * time.Millisecond

// answerDevAsk answers the question on an agent's screen with an option,
// when it's still the one the page shows.
func (b *teamBroker) answerDevAsk(agent teamDevChatAgent, text []string, index int, label, typed string) error {
	if !agent.Running || agent.Status != "blocked" {
		return errors.New("the agent isn't asking anything now")
	}
	screen, _, err := devChatScreen(b.team, agent.Pane)
	if err != nil {
		return err
	}
	ask := teamDevParseAsk(screen)
	if ask == nil || strings.Join(ask.Text, "\n") != strings.Join(text, "\n") || index < 0 || index >= len(ask.Options) || ask.Options[index].Label != label {
		return errors.New("the question changed; look at it again")
	}
	keys, typing, err := teamDevAskKeys(ask, agent.Agent, index, typed)
	if err != nil {
		return err
	}
	if len(typed) > teamDevChatTextMax {
		return fmt.Errorf("the answer is longer than %s", formatDataSize(teamDevChatTextMax))
	}
	if err := teamDevHerdrDo(b.team, append([]string{"agent", "send-keys", agent.Pane}, keys...)...); err != nil || !typing {
		return err
	}
	time.Sleep(teamDevAskPause)
	if err := teamDevHerdrDo(b.team, "pane", "send-text", agent.Pane, strings.TrimSpace(typed)); err != nil {
		return err
	}
	time.Sleep(teamDevAskPause)
	return teamDevHerdrDo(b.team, "agent", "send-keys", agent.Pane, "enter")
}
