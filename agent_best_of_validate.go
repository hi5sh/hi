package main

import (
	"archive/tar"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Validation: with --validate, a round's winner is kept only if it also
// holds on data the agents never see. hi runs the command in a container of
// its own, never in an agent's box: the code being validated is the agent's,
// and in its box the agent could have prepared a way out (the project's .git
// is writable there, --edit is checked only after the score ran, and the
// box's proxy reaches the agent's model). The container gets the gains' tip
// with only the --edit files taken from the box's snapshot, the data
// read-only at /box/data, no network, and no .git. Validation numbers stay
// out of everything a box can read: the next round's task, commit messages,
// and the attempt refs. See docs/specs/approved/hi_agent_best_of_validate.md.

const bestOfDataInBox = "/box/data"

// bestOfValidation is what hi measured in a validation container.
type bestOfValidation struct {
	Value   *float64 `json:"value,omitempty"`
	Exit    int      `json:"exit"`
	Seconds int      `json:"seconds"`
	Output  string   `json:"output,omitempty"` // the last lines
}

// parseBestOfValidate reads --validate, --validate-data, and
// --validate-slack into the loop.
func parseBestOfValidate(flags bestOfFlags, loop *bestOfLoop) error {
	if flags.validate == "" {
		if flags.validateData != "" || flags.validateSlack != "" {
			return usageError{`--validate-data and --validate-slack go with --validate "<command>"`}
		}
		return nil
	}
	if len(flags.edit) == 0 {
		return usageError{"--validate needs --edit: hi validates your files with only the agents' --edit files in place, so it must know which those are"}
	}
	loop.Validate = flags.validate
	if flags.validateSlack != "" {
		value, err := strconv.ParseFloat(flags.validateSlack, 64)
		if err != nil || value < 0 {
			return usageError{fmt.Sprintf("--validate-slack is a number of 0 or more, not %q", flags.validateSlack)}
		}
		loop.ValidateSlack = value
	}
	if flags.validateData != "" {
		path, err := filepath.Abs(flags.validateData)
		if err != nil {
			return err
		}
		if info, err := os.Stat(path); err != nil || !info.IsDir() {
			return fmt.Errorf("--validate-data %s is not a folder; the template's make data writes it", flags.validateData)
		}
		loop.ValidateData = path
	}
	return nil
}

// checkBestOfValidateData refuses validation data inside the project, where
// every box would see it.
func checkBestOfValidateData(loop *bestOfLoop, root string) error {
	if loop == nil || loop.ValidateData == "" {
		return nil
	}
	data, _ := filepath.EvalSymlinks(loop.ValidateData)
	project, _ := filepath.EvalSymlinks(root)
	if rel, err := filepath.Rel(project, data); err == nil && rel != ".." && !strings.HasPrefix(rel, "../") {
		return fmt.Errorf("--validate-data %s is inside the project, so every box would see it; keep it outside, such as ../%s-validation", loop.ValidateData, filepath.Base(root))
	}
	return nil
}

// validateBestOfBaseline validates the run's starting commit before the
// first round; a validation that doesn't work stops the run before it costs
// tokens.
func validateBestOfBaseline(run *bestOfRun, info io.Writer) error {
	loop := run.Loop
	fmt.Fprintf(info, "Validating the baseline: %s, in a container without network.\n", loop.Validate)
	result := validateBestOf(*run, "base", nil)
	if result.Value == nil {
		return fmt.Errorf("the validation doesn't work on your last commit (exit status %d), so nothing was started; its output ends:\n%s",
			result.Exit, qIndent(firstNonEmpty(result.Output, "(nothing)"), "  "))
	}
	loop.BaselineValidation, loop.BestValidation, loop.PeakValidation = result.Value, result.Value, result.Value
	// A validation that runs much longer than the baseline's is stopped.
	loop.ValidateMax = max(120, 2*result.Seconds+60)
	fmt.Fprintf(info, "Baseline validation: %s, in %s.\n", formatScore(*result.Value), formatDuration(time.Duration(result.Seconds)*time.Second))
	return nil
}

// validateBestOfRound validates each box whose result could be kept: with
// the GPU one at a time, like the scores, otherwise all at once.
func validateBestOfRound(run *bestOfRun, phase func(string)) {
	var boxes []*bestOfBox
	for i := range run.Boxes {
		box := &run.Boxes[i]
		if run.good(*box) && box.Files > 0 && box.Snapshot != "" {
			boxes = append(boxes, box)
		}
	}
	if len(boxes) == 0 {
		return
	}
	if run.Options.GPU {
		for i, box := range boxes {
			phase(fmt.Sprintf("round %d: validating %s (%d of %d)", run.Loop.Round, box.Name, i+1, len(boxes)))
			result := validateBestOf(*run, box.Name, box)
			box.Validation = &result
		}
		return
	}
	phase(fmt.Sprintf("round %d: validating %s", run.Loop.Round, bestOfBoxes(len(boxes), "", "")))
	var wait sync.WaitGroup
	for _, box := range boxes {
		wait.Add(1)
		go func() {
			defer wait.Done()
			result := validateBestOf(*run, box.Name, box)
			box.Validation = &result
		}()
	}
	wait.Wait()
}

// bestOfHolds reports whether a winner's validation passes the gate: no
// worse than the highest validation of any kept version, the baseline's
// included, by more than --validate-slack. Comparing with the highest, not
// the latest, stops validation sliding down by the slack at every kept round.
func bestOfHolds(run bestOfRun, box bestOfBox) bool {
	loop := run.Loop
	if box.Validation == nil || box.Validation.Value == nil {
		return false
	}
	peak := loop.peakValidation()
	if peak == nil {
		return true
	}
	value, best := *box.Validation.Value, *peak
	worse := best - value
	if loop.Lower {
		worse = value - best
	}
	return worse <= loop.ValidateSlack
}

// peakValidation is the highest validation of any kept version; runs
// started before v0.52.1 recorded only the latest.
func (l *bestOfLoop) peakValidation() *float64 {
	if l.PeakValidation != nil {
		return l.PeakValidation
	}
	return l.BestValidation
}

// validateBestOf builds a box's validation folder and runs --validate on it
// in a container of its own. Without a box it validates the tip as it is.
func validateBestOf(run bestOfRun, name string, box *bestOfBox) bestOfValidation {
	loop := run.Loop
	failed := func(err error) bestOfValidation {
		return bestOfValidation{Exit: -1, Output: "hi: " + err.Error()}
	}
	dir := bestOfStateFile(run.ID, "validate", name)
	os.RemoveAll(dir)
	defer os.RemoveAll(dir)
	project, home := filepath.Join(dir, "project"), filepath.Join(dir, "home")
	for _, folder := range []string{project, home} {
		if err := os.MkdirAll(folder, 0o755); err != nil {
			return failed(err)
		}
	}
	if err := buildBestOfValidateFolder(run.Root, loop.Tip, box, project); err != nil {
		return failed(err)
	}
	engine, _, err := detectBoxEngine()
	if err != nil {
		return failed(err)
	}
	image := loop.Image
	if box != nil {
		if meta, err := loadBoxMeta(box.Name); err == nil && meta.Image != "" {
			image = meta.Image
		}
	}
	if image == "" {
		return failed(errors.New("no image to validate in"))
	}
	args := bestOfValidateArgs(engine, run, "hi-validate-"+name, project, home, image)
	var out bytes.Buffer
	start := time.Now()
	err = boxCommand(nil, &out, &out, engine.bin, args...)
	result := bestOfValidation{Seconds: int(time.Since(start) / time.Second), Output: lastLines(out.String(), 5)}
	var exitErr *exec.ExitError
	switch {
	case errors.As(err, &exitErr):
		result.Exit = exitErr.ExitCode()
		if result.Exit == 124 {
			result.Output = strings.TrimSpace(result.Output + "\nthe validation ran out of time")
		}
	case err != nil:
		return failed(err)
	}
	if value, ok := lastNumber(out.String()); ok && result.Exit == 0 {
		result.Value = &value
	}
	return result
}

// bestOfValidateArgs is the engine's run command for a validation
// container: no network, no proxy, no .git, the folder at /box/project, the
// data read-only at /box/data, and the command under a time limit.
func bestOfValidateArgs(engine boxEngine, run bestOfRun, container, project, home, image string) []string {
	loop := run.Loop
	args := []string{"run", "--rm", "--name", container, "--network", "none"}
	args = append(args, engine.userArgs()...)
	args = append(args, boxHardening(firstNonEmpty(run.Options.Memory, "16g"))...)
	if run.Options.GPU {
		args = append(args, engine.gpuArgs()...)
	}
	args = append(args, "-v", project+":/box/project", "-v", home+":/box/home")
	if loop.ValidateData != "" {
		args = append(args, "-v", loop.ValidateData+":"+bestOfDataInBox+":ro")
	}
	env := map[string]string{}
	if devcontainer, err := readBoxDevcontainer(run.Root); err == nil && devcontainer != nil {
		for key, value := range devcontainer.ContainerEnv {
			env[key] = value
		}
	}
	// HI_BOX makes a project's Makefile pick the image's tools, as in a box.
	env["HOME"], env["HI_BOX"], env["HI_VALIDATE"] = "/box/home", container, "1"
	for _, key := range sortedKeys(env) {
		args = append(args, "-e", key+"="+env[key])
	}
	args = append(args, "-w", "/box/project", image,
		"sh", "-c", `timeout -k 10 "$1" sh -c "$2" < /dev/null 2>&1`, "sh", strconv.Itoa(loop.ValidateMax), loop.Validate)
	return args
}

// buildBestOfValidateFolder writes the tip's files into folder, then the
// box's changed files from its snapshot, which are all inside --edit: the
// harness, the Makefile, and the data come from the tip, whatever the
// agent did to its own copies.
func buildBestOfValidateFolder(root, tip string, box *bestOfBox, folder string) error {
	if err := extractBestOfArchive(root, folder, tip); err != nil {
		return err
	}
	if box == nil || len(box.ChangedFiles) == 0 {
		return nil
	}
	present := map[string]bool{}
	for _, path := range splitLines(boxGit(root, append([]string{"ls-tree", "-r", "--name-only", box.Snapshot, "--"}, box.ChangedFiles...)...)) {
		present[path] = true
	}
	var paths []string
	for _, path := range box.ChangedFiles {
		if present[path] {
			paths = append(paths, path)
		} else {
			os.Remove(filepath.Join(folder, filepath.FromSlash(path)))
		}
	}
	if len(paths) == 0 {
		return nil
	}
	return extractBestOfArchive(root, folder, box.Snapshot, paths...)
}

// extractBestOfArchive writes git archive's files of a commit or tree into
// folder.
func extractBestOfArchive(root, folder, treeish string, paths ...string) error {
	var archive, stderr bytes.Buffer
	args := append([]string{"-C", root, "archive", "--format=tar", treeish}, append([]string{"--"}, paths...)...)
	if len(paths) == 0 {
		args = args[:len(args)-1]
	}
	if err := boxCommand(nil, &archive, &stderr, "git", args...); err != nil {
		return fmt.Errorf("git archive %s: %s", treeish, qFirstLine(stderr.String(), err.Error()))
	}
	reader := tar.NewReader(&archive)
	for {
		header, err := reader.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		name := filepath.Clean(filepath.FromSlash(header.Name))
		if name == "." || filepath.IsAbs(name) || name == ".." || strings.HasPrefix(name, ".."+string(filepath.Separator)) {
			continue
		}
		target := filepath.Join(folder, name)
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeSymlink:
			os.MkdirAll(filepath.Dir(target), 0o755)
			os.Remove(target)
			if err := os.Symlink(header.Linkname, target); err != nil {
				return err
			}
		case tar.TypeReg:
			os.MkdirAll(filepath.Dir(target), 0o755)
			os.Remove(target)
			mode := os.FileMode(0o644)
			if header.Mode&0o111 != 0 {
				mode = 0o755
			}
			file, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
			if err != nil {
				return err
			}
			_, err = io.Copy(file, reader)
			file.Close()
			if err != nil {
				return err
			}
		}
	}
}
