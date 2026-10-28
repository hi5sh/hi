package main

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"golang.org/x/text/unicode/norm"
)

// How hi team new looks. At a terminal its questions are hi install's: a
// field at a time, a list where the answer is one of a few, a hidden field
// for a token, and a line left behind for each answer. Without a terminal
// (a script, the tests) each is a line to answer, as before.

// errTeamSetupStopped is ctrl+c in a question: nothing was set up.
var errTeamSetupStopped = errors.New("setup stopped; nothing was set up")

// teamStyled says whether the setup asks with hi install's fields.
func teamStyled(stdin io.Reader, stdout io.Writer) bool {
	_, ok := newMenuUI(stdin, stdout).(*styledUI)
	return ok
}

// teamQuestion splits a question such as "  provider [openrouter]" into its
// title and what Enter gives, shown in the field as its placeholder.
func teamQuestion(question string) (title, hint string) {
	title = strings.TrimSpace(question)
	if i := strings.LastIndex(title, " ["); i > 0 && strings.HasSuffix(title, "]") {
		title, hint = title[:i], title[i+2:len(title)-1]
	}
	return title, strings.TrimPrefix(hint, "Enter: ")
}

// teamFieldAsk asks one question in a field, until check takes the answer:
// a refused answer is asked again, with why under the title. Checks run
// after the field closes, since some ask questions of their own.
func teamFieldAsk(stdin io.Reader, stdout io.Writer, question string, secret bool, check func(string) error) (string, error) {
	ui := &styledUI{in: stdin, out: stdout}
	title, hint := teamQuestion(question)
	refused, value := "", ""
	for {
		field := huh.NewInput().Title(title).Placeholder(hint).Value(&value)
		if secret {
			field = field.EchoMode(huh.EchoModePassword)
		}
		if refused != "" {
			field = field.Description(lipgloss.NewStyle().Foreground(colorRed).Render("✗ " + refused))
		}
		if err := ui.run(field); err != nil {
			if errors.Is(err, errMenuBack) {
				return "", errTeamSetupStopped
			}
			return "", err
		}
		answer := strings.TrimSpace(stripTerminalCodes(value))
		err := check(answer)
		if err == nil {
			shown := firstNonEmpty(answer, hint)
			if secret {
				shown = "(hidden)"
				if answer == "" {
					shown = firstNonEmpty(hint, "(none)")
				}
			}
			ui.answered(title, shown)
			return answer, nil
		}
		refused = err.Error()
	}
}

// teamOption is one answer a list offers: what it gives, and how it shows.
type teamOption struct{ value, label string }

// teamChoose asks for one of options; offered is the one Enter takes.
// Without a terminal it's a line, which must be one of them.
func teamChoose(stdin io.Reader, stdout io.Writer, question string, options []teamOption, offered string) (string, error) {
	title := strings.TrimSpace(question)
	if !teamStyled(stdin, stdout) {
		var values []string
		for _, option := range options {
			values = append(values, option.value)
		}
		answer, err := teamAskUntil(stdin, stdout, question+" ["+offered+"]", false, func(answer string) error {
			if !containsString(values, firstNonEmpty(answer, offered)) {
				return fmt.Errorf("the %s is %s, not %q", strings.ToLower(title), strings.Join(values, ", "), answer)
			}
			return nil
		})
		return firstNonEmpty(answer, offered), err
	}
	width := 0
	for _, option := range options {
		width = max(width, len(option.value))
	}
	var shown []string
	start := 0
	for i, option := range options {
		label := option.value
		if option.label != "" {
			label = fmt.Sprintf("%-*s  %s", width, option.value, option.label)
		}
		shown = append(shown, label)
		if option.value == offered {
			start = i
		}
	}
	// hi's own list, not huh's: huh's scrolls the offered answer to the
	// top, hiding the ones above it.
	choice, err := runListSelectAt(stdin, stdout, title, shown, start)
	if err != nil {
		if errors.Is(err, errMenuBack) {
			return "", errTeamSetupStopped
		}
		return "", err
	}
	(&styledUI{in: stdin, out: stdout}).answered(title, options[choice].value)
	return options[choice].value, nil
}

// teamBannerShown: the setup's banner is out, before the team's name was
// asked.
var teamBannerShown bool

// teamSetupBanner opens the setup, as hi compute's menu opens; with no
// name yet, before it's asked.
func teamSetupBanner(stdout io.Writer, styled bool, name, dir string) {
	if !styled {
		fmt.Fprintf(stdout, "Setting up team %s in %s. Each answer is checked as you give it.\n", name, dir)
		return
	}
	dim := lipgloss.NewStyle().Foreground(colorDim)
	if !teamBannerShown {
		badge := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#ffffff")).Background(colorAccent).Padding(0, 1)
		fmt.Fprintf(stdout, "\n%s %s\n", badge.Render("hi team new"), dim.Render("a team: its lead in Slack, its members, and its project"))
		fmt.Fprintln(stdout, dim.Render("Each answer is checked as you give it. Ctrl+C stops; nothing is set up until the end."))
		teamBannerShown = true
	}
	if name != "" {
		fmt.Fprintln(stdout, dim.Render("Team "+name+", in "+dir+"."))
	}
}

// teamStep heads one step of the setup, such as "3/8 Slack app".
func teamStep(stdout io.Writer, styled bool, step string) {
	if !styled {
		fmt.Fprintln(stdout, "\n"+step)
		return
	}
	fmt.Fprintln(stdout)
	fmt.Fprintln(stdout, lipgloss.NewStyle().Foreground(colorAccent).Bold(true).Render(step))
}

// teamSay is what a step tells before it asks: dim at a terminal, so the
// questions stand out.
func teamSay(stdout io.Writer, styled bool, lines ...string) {
	for _, line := range lines {
		if styled {
			line = lipgloss.NewStyle().Foreground(colorDim).Render(line)
		}
		fmt.Fprintln(stdout, line)
	}
}

// teamShowManifest shows the Slack app's manifest, in YAML, which is half
// the lines of the JSON, and at a terminal offers to copy it.
func teamShowManifest(stdin io.Reader, stdout io.Writer, styled bool, config teamConfig) error {
	manifest := teamManifestYAML(config)
	fmt.Fprintln(stdout)
	fmt.Fprintln(stdout, manifest)
	if !styled {
		return nil
	}
	answer, err := teamChoose(stdin, stdout, "The manifest", []teamOption{
		{"copy", "copy it to the clipboard"},
		{"skip", "I've copied it myself"},
	}, "copy")
	if err != nil {
		return err
	}
	if answer == "copy" {
		teamCopy(stdout, manifest+"\n")
		teamSay(stdout, true, "Copied, if this terminal lets programs copy (most do, over SSH too). If not, select the text above.")
	}
	return nil
}

// teamCopy puts text on the clipboard of the terminal the person sits at,
// with OSC 52, through tmux and SSH too.
func teamCopy(stdout io.Writer, text string) {
	sequence := "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte(text)) + "\a"
	if os.Getenv("TMUX") != "" {
		// tmux takes it with set-clipboard on, and passes it on to the
		// terminal outside with allow-passthrough on: both, then.
		sequence += "\x1bPtmux;" + strings.ReplaceAll(sequence, "\x1b", "\x1b\x1b") + "\x1b\\"
	}
	fmt.Fprint(stdout, sequence)
}

// teamManifestYAML is teamManifest in YAML, which Slack takes too: lists of
// words on a line or two, not one line each.
func teamManifestYAML(config teamConfig) string {
	var b strings.Builder
	teamWriteYAML(&b, teamManifestData(config), 0)
	return strings.TrimRight(b.String(), "\n")
}

func teamWriteYAML(b *strings.Builder, value any, indent int) {
	pad := strings.Repeat("  ", indent)
	switch value := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(value))
		for key := range value {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			switch inner := value[key].(type) {
			case map[string]any:
				fmt.Fprintf(b, "%s%s:\n", pad, key)
				teamWriteYAML(b, inner, indent+1)
			case []map[string]any:
				fmt.Fprintf(b, "%s%s:\n", pad, key)
				for _, item := range inner {
					var nested strings.Builder
					teamWriteYAML(&nested, map[string]any(item), indent+2)
					// The first key goes on the dash's line.
					text := nested.String()
					fmt.Fprintf(b, "%s  - %s", pad, strings.TrimPrefix(text, strings.Repeat("  ", indent+2)))
				}
			case []string:
				fmt.Fprintf(b, "%s%s: %s\n", pad, key, teamYAMLList(inner, len(pad)+len(key)+2))
			default:
				fmt.Fprintf(b, "%s%s: %s\n", pad, key, teamYAMLScalar(inner))
			}
		}
	}
}

// teamYAMLList is a flow list, broken before it runs past 100 columns.
func teamYAMLList(items []string, column int) string {
	var b strings.Builder
	b.WriteString("[")
	width := column + 1
	for i, item := range items {
		quoted := strconv.Quote(item)
		if i > 0 {
			b.WriteString(",")
			width++
			if width+1+len(quoted) > 100 {
				b.WriteString("\n" + strings.Repeat(" ", column+1))
				width = column + 1
			} else {
				b.WriteString(" ")
				width++
			}
		}
		b.WriteString(quoted)
		width += len(quoted)
	}
	return b.String() + "]"
}

var yamlPlain = regexp.MustCompile(`^\p{L}[\p{L}0-9 ,._/'()-]*[\p{L}0-9.)]$`)

func teamYAMLScalar(value any) string {
	text, ok := value.(string)
	if !ok {
		return fmt.Sprint(value)
	}
	switch strings.ToLower(text) {
	case "true", "false", "yes", "no", "on", "off", "null", "y", "n":
		return strconv.Quote(text)
	}
	if yamlPlain.MatchString(text) {
		return text
	}
	return strconv.Quote(text)
}

// teamLeadName is what the lead goes by in Slack: the name it was given,
// such as a person's, or the team's.
func teamLeadName(config teamConfig) string {
	return firstNonEmpty(config.Members["lead"].Name, config.Name)
}

// teamLeadTitle heads what the lead is: "Maja, the payments team's
// lead", or "The payments team's lead" when it has the team's name.
func teamLeadTitle(config teamConfig) string {
	if name := config.Members["lead"].Name; name != "" {
		return name + ", the " + config.Name + " team's lead"
	}
	return "The " + config.Name + " team's lead"
}

// teamLeadIntro is teamLeadTitle within a sentence.
func teamLeadIntro(config teamConfig) string {
	if config.Members["lead"].Name != "" {
		return teamLeadTitle(config)
	}
	return "the " + config.Name + " team's lead"
}

// teamBotHandle is the lead's @handle in Slack: its name in the letters a
// bot's handle may have (a-z, 0-9, - _ .), so Åsa Berg is @asa-berg.
func teamBotHandle(config teamConfig) string {
	name := config.Members["lead"].Name
	if name == "" {
		return teamBotName(config.Name)
	}
	var b strings.Builder
	for _, r := range norm.NFD.String(strings.ToLower(name)) {
		switch {
		case unicode.Is(unicode.Mn, r):
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '.':
			b.WriteRune(r)
		case r == 'ø':
			b.WriteRune('o')
		case r == 'æ':
			b.WriteString("ae")
		case r == 'ß':
			b.WriteString("ss")
		default:
			b.WriteRune('-')
		}
	}
	handle := strings.Trim(regexp.MustCompile(`-+`).ReplaceAllString(b.String(), "-"), "-._")
	if handle == "" {
		return teamBotName(config.Name)
	}
	return handle
}

// teamCheckLeadName refuses a name Slack wouldn't take for the app: it
// keeps an app's name to 35 characters.
func teamCheckLeadName(name string) error {
	switch {
	case utf8.RuneCountInString(name) > 35:
		return fmt.Errorf("the lead's name is up to 35 characters, as Slack keeps an app's name; %q has %d", name, utf8.RuneCountInString(name))
	case strings.ContainsAny(name, "@<>\n\t"):
		return fmt.Errorf("the lead's name has no @, <, or >: %q", name)
	case name != strings.TrimSpace(name):
		return fmt.Errorf("the lead's name doesn't start or end with a space: %q", name)
	}
	return nil
}

// teamNote is a line a member's questions tell, indented under the role
// on a line of its own; at a terminal, dim like teamSay's.
func teamNote(stdin io.Reader, stdout io.Writer, line string) {
	if teamStyled(stdin, stdout) {
		line = lipgloss.NewStyle().Foreground(colorDim).Render(strings.TrimSpace(line))
	}
	fmt.Fprintln(stdout, line)
}
