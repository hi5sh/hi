package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Folds: with --folds, a validated run walks forward through n windows. One
// validation window wears out, since every gate it decides is a little
// selection on it. On a fold change hi runs --fold on the host, in a
// temporary worktree of the gains' tip, so the old validation block joins the
// data in the project and the next block becomes --validate-data; commits
// what it changed with a message that names the fold and nothing else; and
// measures the tip again, score and validation, as the new baseline. See
// docs/specs/approved/hi_agent_best_of_validate.md#expanding-window-release-2.

// bestOfFoldMax is how long a --fold command may run.
var bestOfFoldMax = time.Hour

// bestOfFold is one fold change, in run.json.
type bestOfFold struct {
	Fold int `json:"fold"`
	// After is the last round of the fold before.
	After      int       `json:"after"`
	Commit     string    `json:"commit,omitempty"` // none when --fold changed nothing in the project
	Baseline   *float64  `json:"baseline,omitempty"`
	Validation *float64  `json:"validation,omitempty"`
	Ended      time.Time `json:"ended"`
}

// parseBestOfFolds reads --folds, --fold, and --fold-every into the loop.
func parseBestOfFolds(flags bestOfFlags, loop *bestOfLoop) error {
	if flags.folds == "" {
		if flags.fold != "" || flags.foldEvery != "" {
			return usageError{`--fold and --fold-every go with --folds <n>`}
		}
		return nil
	}
	n, err := strconv.Atoi(flags.folds)
	if err != nil || n < 2 {
		return usageError{fmt.Sprintf("--folds is a number of 2 or more, not %q", flags.folds)}
	}
	switch {
	case loop.Validate == "":
		return usageError{`--folds moves the validation window, so it needs --validate "<command>"`}
	case strings.TrimSpace(flags.fold) == "":
		return usageError{`--folds needs --fold "<command>" to move to the next fold, such as --fold "make fold"`}
	}
	loop.Folds, loop.FoldCommand, loop.Fold = n, flags.fold, 1
	if flags.foldEvery != "" {
		k, err := strconv.Atoi(flags.foldEvery)
		if err != nil || k < 1 {
			return usageError{fmt.Sprintf("--fold-every is a number of rounds, 1 or more, not %q", flags.foldEvery)}
		}
		loop.FoldEvery = k
	} else if flags.patience == "" {
		return usageError{"say when a fold ends: --fold-every <rounds>, or --patience <rounds> to move on when gains stop"}
	}
	return nil
}

// foldsLeft reports whether the run has a fold after this one.
func (l *bestOfLoop) foldsLeft() bool { return l.Folds > 0 && l.Fold < l.Folds }

// foldDue reports whether the current fold has ended: after --fold-every
// rounds, or without it once --patience runs out.
func (l *bestOfLoop) foldDue() bool {
	if !l.foldsLeft() || l.Round <= l.FoldStart {
		return false
	}
	if l.FoldEvery > 0 {
		return l.Round-l.FoldStart >= l.FoldEvery
	}
	return l.Patience > 0 && l.Round-max(l.BestRound, l.Since) >= l.Patience
}

func (l *bestOfLoop) foldsText() string {
	when := fmt.Sprintf("after every %s", plural(l.FoldEvery, "round"))
	if l.FoldEvery == 0 {
		when = fmt.Sprintf("after %d rounds without a gain", l.Patience)
	}
	return fmt.Sprintf("%d, moved by %s on this machine %s; each fold starts from a new baseline", l.Folds, l.FoldCommand, when)
}

// foldBaseline is the score and validation the current fold started from.
func (l *bestOfLoop) foldBaseline() (*float64, *float64) {
	if n := len(l.FoldHistory); n > 0 {
		return l.FoldHistory[n-1].Baseline, l.FoldHistory[n-1].Validation
	}
	return l.Baseline, l.BaselineValidation
}

// moveBestOfFold moves the run to its next fold and measures the new
// baseline. If it fails part way, resume runs it again: --fold writes the
// windows for HI_FOLD, so a second run changes nothing it already changed.
func moveBestOfFold(run *bestOfRun, log io.Writer) error {
	loop := run.Loop
	next := loop.Fold + 1
	loop.Phase = fmt.Sprintf("fold %d of %d: %s", next, loop.Folds, loop.FoldCommand)
	if err := saveBestOf(run); err != nil {
		return err
	}
	fmt.Fprintf(log, "%s: fold %d: %s\n", time.Now().Format(time.DateTime), next, loop.FoldCommand)
	commit, err := runBestOfFold(*run, next, log)
	if err != nil {
		return fmt.Errorf("fold %d: %w", next, err)
	}
	if commit != "" {
		loop.Tip = commit
		if err := saveBestOf(run); err != nil {
			return err
		}
	}

	loop.Phase = fmt.Sprintf("fold %d of %d: measuring the new baseline", next, loop.Folds)
	saveBestOf(run)
	base := bestOfNameBase(filepath.Base(run.Root))
	name := fmt.Sprintf("%s-b%s-f%d-base", base, strings.TrimPrefix(run.ID, "b-"), next)
	options := run.Options.box()
	options.name, options.branch, options.from = name, fmt.Sprintf("best-of/%s/fold%d", run.ID, next), loop.Tip
	score, seconds, _, err := bestOfBaseline(run, options, nil, log, log)
	if err != nil {
		return fmt.Errorf("fold %d's baseline: %w", next, err)
	}
	validation := validateBestOf(*run, fmt.Sprintf("fold%d", next), nil)
	if validation.Value == nil {
		return fmt.Errorf("fold %d's validation doesn't work on the gains' tip (exit status %d); its output ends:\n%s",
			next, validation.Exit, qIndent(firstNonEmpty(validation.Output, "(nothing)"), "  "))
	}
	// More data can take longer to score and validate.
	loop.ScoreMax = max(120, 2*seconds+60)
	loop.ValidateMax = max(120, 2*validation.Seconds+60)
	// The best so far is measured on the new windows, not carried over, and
	// patience counts from here.
	loop.Best, loop.BestValidation, loop.PeakValidation = &score, validation.Value, validation.Value
	loop.Fold, loop.FoldStart, loop.Since, loop.Phase = next, loop.Round, loop.Round, ""
	loop.FoldHistory = append(loop.FoldHistory, bestOfFold{Fold: next, After: loop.Round, Commit: commit,
		Baseline: &score, Validation: validation.Value, Ended: time.Now().UTC()})
	fmt.Fprintf(log, "%s: fold %d: baseline %s, validation %s\n", time.Now().Format(time.DateTime), next, formatScore(score), formatScore(*validation.Value))
	return saveBestOf(run)
}

// runBestOfFold runs --fold in a temporary worktree of the gains' tip and
// commits what it changed to the gains' branch. It returns the commit, or ""
// when the command changed nothing in the project.
func runBestOfFold(run bestOfRun, fold int, log io.Writer) (string, error) {
	loop := run.Loop
	dir := bestOfStateFile(run.ID, "fold", filepath.Base(run.Root))
	os.RemoveAll(dir)
	boxGit(run.Root, "worktree", "prune")
	var out bytes.Buffer
	if err := boxCommand(nil, &out, &out, "git", "-C", run.Root, "worktree", "add", "--detach", dir, loop.Tip); err != nil {
		return "", fmt.Errorf("git worktree add: %s", qFirstLine(out.String(), err.Error()))
	}
	defer func() {
		boxGit(run.Root, "worktree", "remove", "--force", dir)
		os.RemoveAll(dir)
		boxGit(run.Root, "worktree", "prune")
	}()

	ctx, cancel := context.WithTimeout(context.Background(), bestOfFoldMax)
	defer cancel()
	command := exec.CommandContext(ctx, "sh", "-c", loop.FoldCommand)
	command.Dir = dir
	// The worktree isn't your checkout, so the command learns where the
	// project and the validation folder are.
	command.Env = append(os.Environ(), "HI_FOLD="+strconv.Itoa(fold), "HI_FOLDS="+strconv.Itoa(loop.Folds),
		"HI_BEST_OF_RUN="+run.ID, "HI_PROJECT="+run.Root, "HI_VALIDATE_DATA="+loop.ValidateData)
	command.Stdin, command.Stdout, command.Stderr = nil, log, log
	if err := command.Run(); err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("%s ran out of time (%s)", loop.FoldCommand, formatDuration(bestOfFoldMax))
		}
		return "", fmt.Errorf("%s failed: %v; its output is in the run's log", loop.FoldCommand, err)
	}

	out.Reset()
	if err := boxCommand(nil, &out, &out, "git", "-C", dir, "add", "-A"); err != nil {
		return "", fmt.Errorf("git add: %s", qFirstLine(out.String(), err.Error()))
	}
	changed := splitLines(boxGit(dir, "diff", "--cached", "--name-only"))
	if len(changed) == 0 {
		fmt.Fprintf(log, "%s: fold %d changed nothing in the project\n", time.Now().Format(time.DateTime), fold)
		return "", nil
	}
	outside := map[string]bool{}
	for _, path := range bestOfOutside(loop.Edit, changed) {
		outside[path] = true
	}
	var inside []string
	for _, path := range changed {
		if !outside[path] {
			inside = append(inside, path)
		}
	}
	if len(inside) > 0 {
		return "", fmt.Errorf("%s changed %s, inside --edit; a fold moves the data, never the agents' files", loop.FoldCommand, strings.Join(inside, ", "))
	}
	tree := boxGit(dir, "write-tree")
	if tree == "" {
		return "", fmt.Errorf("git write-tree failed in the fold's worktree")
	}
	commit, err := bestOfCommitTree(run.Root, tree, loop.Tip, fmt.Sprintf("best-of %s fold %d\n", run.ID, fold))
	if err != nil {
		return "", err
	}
	out.Reset()
	if err := boxCommand(nil, &out, &out, "git", "-C", run.Root, "update-ref", "refs/heads/"+loop.Branch, commit, loop.Tip); err != nil {
		return "", fmt.Errorf("git update-ref %s: %s", loop.Branch, qFirstLine(out.String(), err.Error()))
	}
	return commit, nil
}
