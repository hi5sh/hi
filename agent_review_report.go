package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"text/tabwriter"
)

// The evidence check, the report, and the summary of hi agent review
// (agent_review.go).

// reviewMaxFile is the largest file the evidence check reads.
const reviewMaxFile = 4 << 20

// reviewFileLines is a file's lines at a commit, or nil when it isn't
// there (or isn't text hi will read).
func reviewFileLines(root, commit, file string) []string {
	var out bytes.Buffer
	if err := boxCommand(nil, &out, io.Discard, "git", "-C", root, "show", commit+":"+file); err != nil || out.Len() > reviewMaxFile {
		return nil
	}
	text := strings.ReplaceAll(out.String(), "\r\n", "\n")
	return strings.Split(strings.TrimSuffix(text, "\n"), "\n")
}

// reviewCleanPath is a finding's file as a path from the repository's
// root, or "" for one that leaves it.
func reviewCleanPath(file string) string {
	file = strings.TrimSpace(strings.ReplaceAll(file, "\\", "/"))
	file = strings.TrimPrefix(file, "./")
	if file == "" || strings.HasPrefix(file, "/") {
		return ""
	}
	clean := path.Clean(file)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return ""
	}
	return clean
}

// checkReviewEvidence reads a finding's quote against the reviewed commit,
// never against the agent's box: exact when the quote is at the lines it
// names, moved when it's elsewhere in the file (and the lines are
// corrected), missing otherwise. Lines are compared without their
// indentation and trailing spaces, which agents often change.
func checkReviewEvidence(root, commit string, f *reviewFinding) {
	f.Evidence = "missing"
	file := reviewCleanPath(f.File)
	if file == "" {
		return
	}
	f.File = file
	quote := reviewQuoteLines(f.Quote)
	if len(quote) == 0 {
		return
	}
	lines := reviewFileLines(root, commit, file)
	if lines == nil {
		return
	}
	matchAt := func(start int) bool {
		if start < 0 || start+len(quote) > len(lines) {
			return false
		}
		for i, want := range quote {
			if strings.TrimSpace(lines[start+i]) != want {
				return false
			}
		}
		return true
	}
	if f.Start >= 1 && matchAt(f.Start-1) {
		f.Evidence, f.End = "exact", f.Start+len(quote)-1
		return
	}
	// The nearest place the quote is, to where the agent said.
	best := -1
	for start := 0; start+len(quote) <= len(lines); start++ {
		if matchAt(start) && (best < 0 || abs(start+1-f.Start) < abs(best+1-f.Start)) {
			best = start
		}
	}
	if best >= 0 {
		f.Evidence, f.Start, f.End = "moved", best+1, best+len(quote)
	}
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// reviewQuoteLines is a quote's lines, trimmed, without the blank lines
// around it.
func reviewQuoteLines(quote string) []string {
	lines := strings.Split(strings.ReplaceAll(quote, "\r\n", "\n"), "\n")
	for i := range lines {
		lines[i] = strings.TrimSpace(lines[i])
	}
	for len(lines) > 0 && lines[0] == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// reviewFingerprint matches a finding across reviews: its area, file, and
// the quoted code with spaces collapsed, not its line numbers.
func reviewFingerprint(f reviewFinding, root, commit string) string {
	code := strings.Join(reviewQuoteLines(f.Quote), "\n")
	if f.Evidence != "missing" {
		if lines := reviewFileLines(root, commit, f.File); f.End <= len(lines) && f.Start >= 1 {
			code = strings.Join(lines[f.Start-1:f.End], "\n")
		}
	}
	sum := sha256.Sum256([]byte(f.Area + "\n" + f.File + "\n" + strings.Join(strings.Fields(code), " ")))
	return hex.EncodeToString(sum[:8])
}

// ---------------------------------------------------------------------------
// the report

// reviewHandover opens every report: what the findings are, and what
// they aren't.
const reviewHandover = `> **These are recommendations, not requirements.** Agents read the code
> at one commit. They don't know how the app is deployed, who uses it,
> what protects it (a private network, a sign-in in front of it, rules
> outside the code), or what the people behind it have decided is good
> enough. Each finding says what it assumes; where those assumptions
> don't hold, it may matter less or not at all. Some suggestions will be
> more than this project needs. Decide which ones are relevant to your
> context and scope, and fix those as ordinary work. Nothing here was
> run or reproduced, and a finding that isn't here may still exist: a
> short report doesn't mean the code is safe.`

// reviewHandoverLine is the handover in one line, for summaries.
const reviewHandoverLine = "Recommendations only: weigh each against how the app is run and who uses it."

// writeReviewReport writes report.md and report.json in the review's
// folder, readable only by the user.
func writeReviewReport(rv review) error {
	rv.PID = 0
	data, err := json.MarshalIndent(rv, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(reviewStateFile(rv.ID, "report.json"), append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.WriteFile(reviewStateFile(rv.ID, "report.md"), []byte(reviewMarkdown(rv)), 0o600)
}

// reviewMarkdown is report.md: the handover, what was reviewed and what
// wasn't, then each finding as a section that stands on its own, so it can
// go into a brief as it is.
func reviewMarkdown(rv review) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Review %s: %s\n\n", rv.ID, path.Base(rv.Root))
	b.WriteString(reviewHandover + "\n>\n")
	if rv.Context != "" {
		b.WriteString("> The reviewers had the context below from the people who asked; they knew nothing else about the app.\n\n")
	} else {
		b.WriteString("> The reviewers worked from the code alone: nobody told them how the app is run or who uses it.\n\n")
	}

	b.WriteString("## What was reviewed\n\n")
	what := "`" + rv.Commit + "`"
	switch {
	case rv.Ref != "":
		what += " (" + rv.Ref + ")"
	case rv.Dirty:
		what += fmt.Sprintf(", a snapshot of the working tree with its uncommitted changes, on `%s`", shortCommit(rv.Head))
	}
	fmt.Fprintf(&b, "- **Repository:** %s at %s\n", homeTilde(rv.Root), what)
	if len(rv.Paths) > 0 {
		fmt.Fprintf(&b, "- **Only:** %s\n", strings.Join(rv.Paths, ", "))
	}
	fmt.Fprintf(&b, "- **Areas:** %s\n", strings.Join(rv.Areas, ", "))
	var investigators, checkers []string
	for _, run := range rv.Runs {
		text := fmt.Sprintf("%s (%s%s)", run.ID, run.Agent, reviewModelText(run.Model))
		if run.Kind == "investigate" {
			investigators = append(investigators, text)
		} else {
			checkers = append(checkers, text)
		}
	}
	fmt.Fprintf(&b, "- **Investigators:** %s\n", strings.Join(investigators, ", "))
	if len(checkers) > 0 {
		fmt.Fprintf(&b, "- **Checked by:** %s; each finding by an agent that didn't raise it where one was ready\n", strings.Join(checkers, ", "))
	}
	fmt.Fprintf(&b, "- **When:** %s, by %s; %s\n", rv.Created.Format("2006-01-02 15:04 UTC"), firstNonEmpty(rv.By, "someone"), reviewStateText(rv))
	cost := fmt.Sprintf("$%.2f reported by the agents", reviewSpent(rv))
	if rv.Budget > 0 {
		cost += fmt.Sprintf(", of a $%.2f budget", rv.Budget)
	}
	fmt.Fprintf(&b, "- **Cost:** %s\n", cost)
	if rv.Context != "" {
		b.WriteString("\n**Context given to the reviewers:**\n\n" + reviewIndent(rv.Context, "> ") + "\n")
	}

	b.WriteString("\n## What wasn't\n\n")
	gaps := reviewGaps(rv)
	gaps = append(gaps, "Nothing was built, run, or reproduced: a finding is what the code says, checked by reading.")
	for _, gap := range gaps {
		b.WriteString("- " + gap + "\n")
	}

	counts := map[string]int{}
	for _, f := range rv.Findings {
		counts[f.Verdict]++
	}
	fmt.Fprintf(&b, "\n## Findings\n\n%s.\n", reviewTallyText(rv))
	if counts["confirmed"]+counts["uncertain"]+counts["unchecked"] == 0 {
		b.WriteString("\nNo findings came through the checks. That says what these agents saw in this time, not that the code has no problems.\n")
	}
	for _, f := range rv.Findings {
		if f.Verdict == "rejected" || f.Verdict == "duplicate" {
			continue
		}
		b.WriteString("\n" + reviewFindingMarkdown(rv, f))
	}
	if counts["rejected"]+counts["duplicate"] > 0 {
		b.WriteString("\n## Rejected and duplicates\n\nThe second opinion didn't hold these up, or they repeat a finding above. They're here so nobody raises them again without seeing why.\n\n")
		for _, f := range rv.Findings {
			switch f.Verdict {
			case "rejected":
				fmt.Fprintf(&b, "- **%s · %s** (`%s`, %s, %s): rejected by %s: %s\n", f.ID, f.Title, f.File, f.Area, f.Agent, f.Judge, firstNonEmpty(f.Reason, "no reason given"))
			case "duplicate":
				own := ""
				if f.OwnVerdict != "" && f.Judge != "" {
					own = fmt.Sprintf("; %s %s it", f.Judge, f.OwnVerdict)
				}
				fmt.Fprintf(&b, "- **%s · %s** (`%s`, %s): the same as %s%s\n", f.ID, f.Title, f.File, f.Agent, f.DuplicateOf, own)
			}
		}
	}
	return b.String()
}

func reviewModelText(model string) string {
	if model == "" {
		return ""
	}
	return ", " + model
}

func reviewStateText(rv review) string {
	switch rv.State {
	case "done":
		if !rv.Ended.IsZero() {
			return "took " + formatDuration(rv.Ended.Sub(rv.Created))
		}
		return "done"
	case "running":
		return "still running: " + rv.Phase
	}
	return rv.State + firstNonEmpty(": "+rv.Reason, "")
}

// reviewGaps are what the review didn't cover: runs that failed, were
// skipped, or ran out of time, and findings nobody checked.
func reviewGaps(rv review) []string {
	var gaps []string
	for _, run := range rv.Runs {
		what := run.Area
		if run.Kind == "adjudicate" {
			what = fmt.Sprintf("checking %s", plural(len(run.Batch), "finding"))
		}
		switch {
		case run.State == "failed" || run.State == "skipped" || run.State == "stopped":
			gaps = append(gaps, fmt.Sprintf("%s (%s, %s) %s: %s.", run.ID, what, run.Agent, run.State, firstNonEmpty(run.Note, "no reason recorded")))
		case run.State == "done" && run.TimedOut:
			gaps = append(gaps, fmt.Sprintf("%s (%s, %s) ran out of time after writing its results, so it may have stopped early.", run.ID, what, run.Agent))
		case run.State == "waiting" || run.State == "running":
			gaps = append(gaps, fmt.Sprintf("%s (%s, %s) hadn't finished.", run.ID, what, run.Agent))
		}
	}
	unchecked := 0
	for _, f := range rv.Findings {
		if f.Verdict == "unchecked" {
			unchecked++
		}
	}
	if unchecked > 0 {
		gaps = append(gaps, fmt.Sprintf("%s had no second opinion; they're listed as unchecked.", plural(unchecked, "finding")))
	}
	if len(gaps) == 0 {
		gaps = append(gaps, "Every run finished.")
	}
	return gaps
}

func reviewTallyText(rv review) string {
	counts := map[string]int{}
	for _, f := range rv.Findings {
		counts[f.Verdict]++
	}
	parts := []string{fmt.Sprintf("%d confirmed", counts["confirmed"]), fmt.Sprintf("%d uncertain", counts["uncertain"])}
	for _, verdict := range []string{"unchecked", "rejected", "duplicate"} {
		if counts[verdict] > 0 {
			word := verdict
			if verdict == "duplicate" {
				word = "duplicates"
				if counts[verdict] == 1 {
					word = "duplicate"
				}
			}
			parts = append(parts, fmt.Sprintf("%d %s", counts[verdict], word))
		}
	}
	return fmt.Sprintf("%s raised: %s", plural(len(rv.Findings), "thing"), strings.Join(parts, ", "))
}

// reviewFindingMarkdown is one finding's section. Its code comes from the
// reviewed commit, not from the agent, unless hi couldn't find it there.
func reviewFindingMarkdown(rv review, f reviewFinding) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## %s · %s\n\n", f.ID, f.Title)
	verdict := map[string]string{"confirmed": "Confirmed", "uncertain": "Uncertain", "unchecked": "Not checked by a second agent"}[f.Verdict]
	severity := firstNonEmpty(f.severity(), "unrated")
	if f.JudgeSeverity != "" {
		severity += fmt.Sprintf(" (raised as %s)", firstNonEmpty(f.Severity, "unrated"))
	}
	fmt.Fprintf(&b, "- Severity: %s, the reviewers' estimate · Area: %s · %s\n", severity, f.Area, verdict)
	lines := fmt.Sprintf("lines %d-%d", f.Start, f.End)
	if f.Start == f.End {
		lines = fmt.Sprintf("line %d", f.Start)
	}
	fmt.Fprintf(&b, "- Where: `%s` %s, at %s\n", f.File, lines, shortCommit(rv.Commit))
	raised := f.Agent
	if len(f.AlsoRaisedBy) > 0 {
		raised += ", and separately by " + strings.Join(f.AlsoRaisedBy, ", ")
	}
	fmt.Fprintf(&b, "- Raised by: %s\n\n", raised)
	if f.What != "" {
		fmt.Fprintf(&b, "**What the code does.** %s\n\n", f.What)
	}
	code, label := "", ""
	switch f.Evidence {
	case "exact", "moved":
		if all := reviewFileLines(rv.Root, rv.Commit, f.File); f.Start >= 1 && f.End <= len(all) {
			code = strings.Join(all[f.Start-1:f.End], "\n")
		}
	case "missing":
		code, label = strings.Trim(f.Quote, "\n"), "As the reviewer quoted it; hi couldn't find these lines in that file at this commit, so read the code before relying on this finding.\n\n"
	}
	if strings.TrimSpace(code) != "" {
		fence := reviewFence(code)
		fmt.Fprintf(&b, "%s%s%s\n%s\n%s\n\n", label, fence, reviewLanguage(f.File), code, fence)
	}
	fmt.Fprintf(&b, "**When it matters.** %s\n\n", firstNonEmpty(f.Failure, "The reviewer didn't say."))
	assumes := firstNonEmpty(f.Assumes, "The reviewer didn't say; consider how the app is run and who uses it before acting on this.")
	fmt.Fprintf(&b, "**Assumes.** %s\n\n", assumes)
	if f.JudgeAssumes != "" {
		fmt.Fprintf(&b, "The second opinion would put it this way: %s\n\n", f.JudgeAssumes)
	}
	if f.Direction != "" {
		fmt.Fprintf(&b, "**A direction.** %s\n\n", f.Direction)
	}
	if f.Judge != "" {
		fmt.Fprintf(&b, "**Second opinion.** %s %s: %s\n", f.Judge, f.Verdict, firstNonEmpty(f.Reason, "no reason given."))
	} else {
		b.WriteString("**Second opinion.** None: no agent checked this finding.\n")
	}
	return b.String()
}

// reviewFence is a code fence longer than any run of backticks in the code.
func reviewFence(code string) string {
	longest, run := 0, 0
	for _, r := range code {
		if r == '`' {
			run++
			longest = max(longest, run)
		} else {
			run = 0
		}
	}
	return strings.Repeat("`", max(3, longest+1))
}

func reviewLanguage(file string) string {
	languages := map[string]string{".go": "go", ".py": "python", ".ts": "ts", ".tsx": "tsx", ".js": "js", ".jsx": "jsx", ".rs": "rust",
		".rb": "ruby", ".java": "java", ".kt": "kotlin", ".cs": "csharp", ".php": "php", ".sh": "sh", ".sql": "sql", ".yml": "yaml",
		".yaml": "yaml", ".json": "json", ".toml": "toml", ".html": "html", ".css": "css", ".swift": "swift", ".c": "c", ".h": "c",
		".cpp": "cpp", ".md": "markdown"}
	return languages[strings.ToLower(path.Ext(file))]
}

// ---------------------------------------------------------------------------
// the summary

// printReviewSummary is the review in the terminal: a row per finding that
// came through the checks, and the tally.
func printReviewSummary(rv review, rejected bool, w io.Writer) {
	what := shortCommit(rv.Commit)
	if rv.Dirty {
		what += " (with uncommitted changes)"
	}
	header := fmt.Sprintf("Review %s · %s @ %s · %s", rv.ID, homeTilde(rv.Root), what, strings.Join(rv.Areas, ", "))
	if !rv.Ended.IsZero() {
		header += " · " + formatDuration(rv.Ended.Sub(rv.Created))
	}
	if spent := reviewSpent(rv); spent > 0 {
		header += fmt.Sprintf(" · $%.2f", spent)
	}
	fmt.Fprintln(w, header)
	if rv.State == "running" {
		fmt.Fprintf(w, "\n  Still running: %s. hi agent review watch %s follows it.\n", rv.Phase, rv.ID)
		return
	}
	fmt.Fprintln(w)
	table := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(table, "  #\tSEV\tVERDICT\tAREA\tWHERE\tFINDING")
	rows := 0
	for _, f := range rv.Findings {
		if !rejected && (f.Verdict == "rejected" || f.Verdict == "duplicate") {
			continue
		}
		rows++
		where := fmt.Sprintf("%s:%d", f.File, f.Start)
		if f.End > f.Start {
			where += fmt.Sprintf("-%d", f.End)
		}
		title := f.Title
		if f.Verdict == "duplicate" {
			title = "same as " + f.DuplicateOf + ": " + title
		}
		fmt.Fprintf(table, "  %s\t%s\t%s\t%s\t%s\t%s\n", strings.TrimPrefix(f.ID, "F-"), reviewShortSeverity(f.severity()), f.Verdict, f.Area,
			qClipWidth(where, 34), reviewLine(title, 80))
	}
	table.Flush()
	if rows == 0 {
		fmt.Fprintln(w, "  No findings came through the checks.")
	}
	fmt.Fprintln(w)
	var investigators []string
	for _, run := range rv.Runs {
		if run.Kind == "investigate" {
			investigators = append(investigators, run.Agent)
		}
	}
	fmt.Fprintf(w, "  %s (%s) · %s\n", plural(len(investigators), "investigator"), strings.Join(reviewUnique(investigators), ", "), reviewTallyText(rv))
	gaps := reviewGaps(rv)
	if len(gaps) == 1 && gaps[0] == "Every run finished." {
		fmt.Fprintln(w, "  Not covered: nothing left out · Reproduced: none (not attempted)")
	} else {
		fmt.Fprintln(w, "  Not covered:")
		for _, gap := range gaps {
			fmt.Fprintln(w, "    "+gap)
		}
	}
	if rv.State != "done" {
		fmt.Fprintf(w, "  The review %s.\n", reviewStateText(rv))
	}
	fmt.Fprintln(w, "  "+reviewHandoverLine)
	fmt.Fprintf(w, "  hi agent review show %s --md   the report, with each finding in full (%s)\n", rv.ID, homeTilde(reviewStateFile(rv.ID, "report.md")))
}

func reviewShortSeverity(severity string) string {
	if severity == "medium" {
		return "med"
	}
	return firstNonEmpty(severity, "–")
}

func reviewUnique(items []string) []string {
	var out []string
	for _, item := range items {
		if !containsString(out, item) {
			out = append(out, item)
		}
	}
	return out
}
