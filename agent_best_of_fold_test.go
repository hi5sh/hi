package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBestOfFoldFlags(t *testing.T) {
	data := t.TempDir()
	valid := bestOfFlags{score: "x", higher: true, edit: []string{"a.py"}, validate: "v", validateData: data}
	with := func(change func(*bestOfFlags)) bestOfFlags { flags := valid; change(&flags); return flags }
	for name, flags := range map[string]bestOfFlags{
		"without --validate": {score: "x", higher: true, edit: []string{"a.py"}, folds: "3", fold: "make fold", foldEvery: "5"},
		"without --fold":     with(func(f *bestOfFlags) { f.folds, f.foldEvery = "3", "5" }),
		"one fold":           with(func(f *bestOfFlags) { f.folds, f.fold, f.foldEvery = "1", "make fold", "5" }),
		"no end to a fold":   with(func(f *bestOfFlags) { f.folds, f.fold = "3", "make fold" }),
		"--fold alone":       with(func(f *bestOfFlags) { f.fold = "make fold" }),
		"--fold-every 0":     with(func(f *bestOfFlags) { f.folds, f.fold, f.foldEvery = "3", "make fold", "0" }),
		"without --score":    {folds: "3", fold: "make fold", foldEvery: "5"},
	} {
		if _, err := parseBestOfLoop(flags); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	loop, err := parseBestOfLoop(with(func(f *bestOfFlags) { f.folds, f.fold, f.patience = "3", "make fold", "4" }))
	if err != nil || loop.Folds != 3 || loop.Fold != 1 || loop.FoldCommand != "make fold" || loop.FoldEvery != 0 {
		t.Fatalf("%+v %v", loop, err)
	}
}

func TestBestOfFoldDue(t *testing.T) {
	for _, c := range []struct {
		loop bestOfLoop
		want bool
	}{
		{bestOfLoop{Folds: 3, Fold: 1, FoldEvery: 5, Round: 4}, false},
		{bestOfLoop{Folds: 3, Fold: 1, FoldEvery: 5, Round: 5}, true},
		{bestOfLoop{Folds: 3, Fold: 2, FoldEvery: 5, Round: 9, FoldStart: 5}, false},
		{bestOfLoop{Folds: 3, Fold: 3, FoldEvery: 5, Round: 15, FoldStart: 10}, false},            // the last fold
		{bestOfLoop{Folds: 3, Fold: 1, Patience: 3, Round: 5, BestRound: 2}, true},                // patience ran out
		{bestOfLoop{Folds: 3, Fold: 2, Patience: 3, Round: 5, BestRound: 2, FoldStart: 5}, false}, // just moved
	} {
		if got := c.loop.foldDue(); got != c.want {
			t.Errorf("%+v: %v, want %v", c.loop, got, c.want)
		}
	}
	// Patience ends the run only in the last fold.
	loop := &bestOfLoop{Folds: 2, Fold: 1, Patience: 3, Round: 5, BestRound: 2}
	if _, reason := bestOfShouldStop(bestOfRun{ID: "b-none", Loop: loop}); reason != "" {
		t.Fatalf("patience ended a run with a fold left: %s", reason)
	}
	loop.Fold = 2
	if _, reason := bestOfShouldStop(bestOfRun{ID: "b-none", Loop: loop}); !strings.Contains(reason, "patience") {
		t.Fatalf("patience didn't end the last fold: %q", reason)
	}
}

func TestBestOfRoundsWithFolds(t *testing.T) {
	project, _, exited := agentTestSetup(t)
	os.WriteFile(filepath.Join(project, "strategy.py"), []byte("v = 0\n"), 0o644)
	boxGit(project, "add", ".")
	boxGit(project, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "strategy")
	data := filepath.Join(filepath.Dir(project), "project-validation")
	os.MkdirAll(data, 0o755)
	*exited = "0"
	bestOfPoll = time.Millisecond
	previousSpawn := bestOfSpawn
	bestOfSpawn = func(string) error { return nil }
	t.Cleanup(func() { bestOfPoll, bestOfSpawn = 3*time.Second, previousSpawn })

	// Higher is better. Fold 1: the baseline scores 1.0 and validates at
	// 0.8; round 1 reaches 1.3 and validates at 0.82: kept. Fold 2 moves the
	// data (fold.txt), and the tip measures 0.9 and 0.7 there. Round 2
	// reaches 1.2, below fold 1's best but above fold 2's baseline, and
	// validates at 0.75: kept.
	scores := map[string]string{"r1-1": "1.3", "r2-1": "1.2"}
	validations := map[string]string{"v = 0": "0.8", "v = 1": "0.82", "v = 1 fold": "0.7", "v = 2 fold": "0.75"}
	files := map[string]string{"r1-1": "v = 1\n", "r2-1": "v = 2\n"}
	previousCommand := boxCommand
	boxCommand = func(stdin io.Reader, stdout, stderr io.Writer, name string, args ...string) error {
		if name != "git" && args[0] == "run" && strings.HasPrefix(args[3], "hi-validate-") {
			for i, arg := range args {
				if arg == "-v" && strings.HasSuffix(args[i+1], ":/box/project") {
					folder := strings.TrimSuffix(args[i+1], ":/box/project")
					strategy, _ := os.ReadFile(filepath.Join(folder, "strategy.py"))
					key := strings.TrimSpace(string(strategy))
					if fileExists(filepath.Join(folder, "fold.txt")) {
						key += " fold"
					}
					fmt.Fprintf(stdout, "sharpe: %s\n", validations[key])
				}
			}
			return nil
		}
		err := previousCommand(stdin, stdout, stderr, name, args...)
		if name == "git" || args[0] != "run" {
			return err
		}
		var box string
		for i, arg := range args {
			if arg == "--name" {
				box = strings.TrimPrefix(args[i+1], "hi-box-")
			}
		}
		home := boxStateFile(box, "home")
		if strings.HasSuffix(box, "-base") {
			score := "1.0"
			if strings.Contains(box, "-f2-") {
				score = "0.9"
			}
			os.WriteFile(filepath.Join(home, "score.log"), []byte("sharpe: "+score+"\n"), 0o600)
			os.WriteFile(filepath.Join(home, "baseline.txt"), []byte("0 0 30\n"), 0o600)
			return err
		}
		meta, _ := loadBoxMeta(box)
		key := box[strings.Index(box, "-r")+1:]
		results := filepath.Join(home, agentResultDir)
		if key == "r2-1" {
			task, _ := os.ReadFile(filepath.Join(results, "task.md"))
			for _, want := range []string{"fold 2", "The best score so far is 0.9", "moves forward 1 times"} {
				if !strings.Contains(string(task), want) {
					t.Errorf("round 2's task lacks %q:\n%s", want, task)
				}
			}
			for _, secret := range []string{"0.7", "0.82"} {
				if strings.Contains(string(task), secret) {
					t.Errorf("round 2's task shows a validation number %s:\n%s", secret, task)
				}
			}
			if !fileExists(filepath.Join(meta.Workdir, "fold.txt")) {
				t.Error("round 2's box doesn't start from the fold's commit")
			}
		}
		os.WriteFile(filepath.Join(meta.Workdir, "strategy.py"), []byte(files[key]), 0o644)
		boxGit(meta.Workdir, "add", "-A")
		tree := boxGit(meta.Workdir, "write-tree")
		boxGit(meta.Workdir, "reset", "-q")
		os.WriteFile(filepath.Join(results, "tree.txt"), []byte(tree+"\n"), 0o600)
		os.WriteFile(filepath.Join(results, "check.json"), []byte(`{"agent_exit":0,"agent_seconds":60,"check_exit":0,"check_seconds":1,"score_exit":0,"score_seconds":30}`), 0o600)
		os.WriteFile(filepath.Join(results, "agent.json"), []byte(`{"agent_exit":0,"agent_seconds":60}`), 0o600)
		os.WriteFile(filepath.Join(results, "score.log"), []byte("sharpe: "+scores[key]+"\n"), 0o600)
		os.WriteFile(filepath.Join(results, "result.json"), []byte(`{"result":"ok\n<<<REPORT\n{\"status\":\"complete\",\"summary\":\"idea `+key+`\"}\nREPORT>>>","total_cost_usd":0.5}`), 0o600)
		return err
	}
	t.Cleanup(func() { boxCommand = previousCommand })

	fold := `echo "$HI_FOLD of $HI_FOLDS" > fold.txt; echo "$HI_PROJECT" > "$HI_VALIDATE_DATA/moved"`
	var stdout, stderr bytes.Buffer
	args := []string{"best-of", "1", "--rounds", "2", "--score", "make score", "--higher", "--edit", "strategy.py",
		"--validate", "make validate", "--validate-data", data, "--folds", "2", "--fold", fold, "--fold-every", "1", "--yes", "improve it"}
	if status := runAgent(args, strings.NewReader(""), &stdout, &stderr); status != 0 {
		t.Fatalf("status %d: %s %s", status, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "folds   2, moved by") {
		t.Errorf("the summary doesn't show the folds:\n%s", stdout.String())
	}
	var log bytes.Buffer
	if err := driveBestOf("b-1", &log); err != nil {
		t.Fatalf("%v\n%s", err, log.String())
	}
	run, _ := loadBestOf("b-1")
	loop := run.Loop
	if loop.Fold != 2 || loop.FoldStart != 1 || len(loop.FoldHistory) != 1 || *loop.FoldHistory[0].Baseline != 0.9 || *loop.FoldHistory[0].Validation != 0.7 {
		t.Fatalf("fold %d from round %d, history %+v", loop.Fold, loop.FoldStart, loop.FoldHistory)
	}
	if len(loop.History) != 2 || loop.History[0].Result != "kept" || loop.History[1].Result != "kept" {
		t.Fatalf("history %+v", loop.History)
	}
	if *loop.Best != 1.2 || *loop.PeakValidation != 0.75 || loop.State != "done" {
		t.Fatalf("best %v (peak %v), %s: %s", *loop.Best, *loop.PeakValidation, loop.State, loop.Reason)
	}
	// The fold's commit sits between the two gains, names the fold and
	// nothing else, and holds what --fold wrote.
	messages := splitLines(boxGit(project, "log", "--format=%s", run.Base+".."+loop.Branch))
	if len(messages) != 3 || messages[1] != "best-of b-1 fold 2" {
		t.Fatalf("the gains' branch: %q", messages)
	}
	if got := boxGit(project, "show", loop.FoldHistory[0].Commit+":fold.txt"); got != "2 of 2" {
		t.Fatalf("fold.txt is %q", got)
	}
	if moved, _ := os.ReadFile(filepath.Join(data, "moved")); strings.TrimSpace(string(moved)) != project {
		t.Fatalf("HI_PROJECT was %q", moved)
	}
	if worktrees := boxGit(project, "worktree", "list"); strings.Contains(worktrees, "fold") {
		t.Fatalf("the fold's worktree is still there:\n%s", worktrees)
	}
	stdout.Reset()
	runAgent([]string{"best-of", "show", "b-1"}, strings.NewReader(""), &stdout, &stderr)
	for _, want := range []string{"fold 2 of 2", "Best: 1.2 (validation 0.75) from round 2, fold 2's baseline 0.9 (validation 0.7)", "fold 2  0.9"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("show lacks %q:\n%s", want, stdout.String())
		}
	}
}

// A --fold that changes an agent's file fails the run before the next round.
func TestBestOfFoldMayNotTouchEdit(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	project, _, _ := agentTestSetup(t)
	tip := boxGit(project, "rev-parse", "HEAD")
	loop := &bestOfLoop{Tip: tip, Branch: "best-of/b-8/best", Edit: []string{"strategy.py"}, Folds: 2, Fold: 1,
		FoldCommand: "echo x > strategy.py; echo y > data.txt"}
	boxGit(project, "branch", "-f", loop.Branch, tip)
	run := bestOfRun{ID: "b-8", Root: project, Loop: loop}
	os.MkdirAll(bestOfStateFile("b-8"), 0o700)
	_, err := runBestOfFold(run, 2, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "strategy.py, inside --edit") {
		t.Fatalf("err %v", err)
	}
	if got := boxGit(project, "rev-parse", loop.Branch); got != tip {
		t.Fatal("a fold that touched --edit was committed")
	}
}
