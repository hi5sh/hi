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

func TestBestOfValidateFlags(t *testing.T) {
	data := t.TempDir()
	for _, flags := range []bestOfFlags{
		{validate: "make validate"},                                                                  // without --score
		{score: "x", lower: true, validate: "make validate"},                                         // without --edit
		{score: "x", lower: true, edit: []string{"a.py"}, validateData: data},                        // --validate-data without --validate
		{score: "x", lower: true, edit: []string{"a.py"}, validate: "v", validateSlack: "-1"},        // negative slack
		{score: "x", lower: true, edit: []string{"a.py"}, validate: "v", validateData: data + "/no"}, // not a folder
	} {
		if _, err := parseBestOfLoop(flags); err == nil {
			t.Errorf("%+v was accepted", flags)
		}
	}
	loop, err := parseBestOfLoop(bestOfFlags{score: "x", higher: true, edit: []string{"a.py"}, validate: "make validate", validateData: data, validateSlack: "0.05"})
	if err != nil || loop.Validate != "make validate" || loop.ValidateData != data || loop.ValidateSlack != 0.05 {
		t.Fatalf("%+v %v", loop, err)
	}
	project := t.TempDir()
	os.MkdirAll(filepath.Join(project, "eval", "validation"), 0o755)
	if err := checkBestOfValidateData(&bestOfLoop{ValidateData: filepath.Join(project, "eval", "validation")}, project); err == nil {
		t.Fatal("validation data inside the project was accepted")
	}
	if err := checkBestOfValidateData(&bestOfLoop{ValidateData: data}, project); err != nil {
		t.Fatal(err)
	}
}

func TestBestOfHolds(t *testing.T) {
	value := func(v float64) *bestOfValidation { return &bestOfValidation{Value: &v} }
	best := 0.81
	higher := bestOfRun{Loop: &bestOfLoop{BestValidation: &best, ValidateSlack: 0.05}}
	lower := bestOfRun{Loop: &bestOfLoop{Lower: true, BestValidation: &best, ValidateSlack: 0.05}}
	for _, c := range []struct {
		run  bestOfRun
		box  bestOfBox
		want bool
	}{
		{higher, bestOfBox{Validation: value(0.78)}, true},  // 0.03 worse, inside the slack
		{higher, bestOfBox{Validation: value(0.74)}, false}, // 0.07 worse
		{higher, bestOfBox{Validation: value(0.90)}, true},
		{lower, bestOfBox{Validation: value(0.84)}, true},
		{lower, bestOfBox{Validation: value(0.88)}, false},
		{higher, bestOfBox{Validation: &bestOfValidation{Exit: 1}}, false}, // no number
		{higher, bestOfBox{}, false},                                       // never validated
	} {
		if got := bestOfHolds(c.run, c.box); got != c.want {
			t.Errorf("lower %v, %+v: %v, want %v", c.run.Loop.Lower, c.box.Validation, got, c.want)
		}
	}
}

func TestBestOfValidateFolder(t *testing.T) {
	project, _, _ := agentTestSetup(t)
	write := func(name, text string) { os.WriteFile(filepath.Join(project, name), []byte(text), 0o644) }
	write("Makefile", "validate:\n\tpython evaluate.py\n")
	write("strategy.py", "x = 1\n")
	write("old.py", "gone\n")
	boxGit(project, "add", ".")
	boxGit(project, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "tip")
	tip := boxGit(project, "rev-parse", "HEAD")
	// The agent changes its file, removes one, and also changes the
	// Makefile, which is outside --edit and so not among ChangedFiles.
	write("strategy.py", "x = 2\n")
	write("Makefile", "validate:\n\tcat /box/data/*\n")
	os.Remove(filepath.Join(project, "old.py"))
	boxGit(project, "add", "-A")
	tree := boxGit(project, "write-tree")
	folder := t.TempDir()
	box := &bestOfBox{Snapshot: tree, ChangedFiles: []string{"strategy.py", "old.py"}}
	if err := buildBestOfValidateFolder(project, tip, box, folder); err != nil {
		t.Fatal(err)
	}
	read := func(name string) string { data, _ := os.ReadFile(filepath.Join(folder, name)); return string(data) }
	if read("strategy.py") != "x = 2\n" || !strings.Contains(read("Makefile"), "evaluate.py") || fileExists(filepath.Join(folder, "old.py")) || fileExists(filepath.Join(folder, ".git")) {
		t.Fatalf("folder: strategy %q, Makefile %q, old.py %v", read("strategy.py"), read("Makefile"), fileExists(filepath.Join(folder, "old.py")))
	}
}

func TestBestOfRoundsWithValidation(t *testing.T) {
	project, calls, exited := agentTestSetup(t)
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

	// Higher is better. The baseline scores 1.0 and validates at 0.8. Round
	// 1's box reaches 1.3 but validates at 0.6: rejected. Round 2's reaches
	// 1.1 and validates at 0.82: kept.
	scores := map[string]string{"r1-1": "1.3", "r2-1": "1.1"}
	validations := map[string]string{"v = 0": "0.8", "v = 1": "0.6", "v = 2": "0.82"}
	files := map[string]string{"r1-1": "v = 1\n", "r2-1": "v = 2\n"}
	var validateArgs [][]string
	previousCommand := boxCommand
	boxCommand = func(stdin io.Reader, stdout, stderr io.Writer, name string, args ...string) error {
		if name != "git" && args[0] == "run" && strings.HasPrefix(args[3], "hi-validate-") {
			validateArgs = append(validateArgs, args)
			for i, arg := range args {
				if arg == "-v" && strings.HasSuffix(args[i+1], ":/box/project") {
					folder := strings.TrimSuffix(args[i+1], ":/box/project")
					strategy, _ := os.ReadFile(filepath.Join(folder, "strategy.py"))
					fmt.Fprintf(stdout, "sharpe: %s\n", validations[strings.TrimSpace(string(strategy))])
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
			os.WriteFile(filepath.Join(home, "score.log"), []byte("sharpe: 1.0\n"), 0o600)
			os.WriteFile(filepath.Join(home, "baseline.txt"), []byte("0 0 30\n"), 0o600)
			return err
		}
		meta, _ := loadBoxMeta(box)
		key := box[strings.Index(box, "-r")+1:]
		results := filepath.Join(home, agentResultDir)
		if key == "r2-1" {
			task, _ := os.ReadFile(filepath.Join(results, "task.md"))
			for _, want := range []string{"rejected", "data that is not in your box"} {
				if !strings.Contains(string(task), want) {
					t.Errorf("round 2's task lacks %q:\n%s", want, task)
				}
			}
			for _, secret := range []string{"0.6", "0.8"} {
				if strings.Contains(string(task), secret) {
					t.Errorf("round 2's task shows a validation number %s:\n%s", secret, task)
				}
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

	var stdout, stderr bytes.Buffer
	args := []string{"best-of", "1", "--rounds", "2", "--score", "make score", "--higher", "--edit", "strategy.py",
		"--validate", "make validate", "--validate-data", data, "--yes", "improve it"}
	if status := runAgent(args, strings.NewReader(""), &stdout, &stderr); status != 0 {
		t.Fatalf("status %d: %s %s", status, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "Baseline validation: 0.8") {
		t.Fatalf("no baseline validation: %s", stdout.String())
	}
	var log bytes.Buffer
	if err := driveBestOf("b-1", &log); err != nil {
		t.Fatal(err)
	}
	run, _ := loadBestOf("b-1")
	loop := run.Loop
	if loop.History[0].Result != "rejected" || *loop.History[0].Validation != 0.6 || loop.History[1].Result != "kept" {
		t.Fatalf("history %+v", loop.History)
	}
	if *loop.Best != 1.1 || *loop.BestValidation != 0.82 || loop.BestRound != 2 {
		t.Fatalf("best %v (%v) from round %d", *loop.Best, *loop.BestValidation, loop.BestRound)
	}
	// Commits and attempt refs carry no validation number.
	for _, message := range []string{boxGit(project, "log", "--format=%B", run.Base+".."+loop.Branch), boxGit(project, "log", "--format=%B", "refs/best-of/b-1/r1-1", "-1")} {
		if strings.Contains(message, "0.6") || strings.Contains(message, "0.82") || strings.Contains(message, "validation") {
			t.Fatalf("a commit shows validation: %q", message)
		}
	}
	// Validation containers: no network, the data read-only, no .git, no
	// proxy; agents' boxes never get the data.
	if len(validateArgs) != 3 {
		t.Fatalf("%d validation containers", len(validateArgs))
	}
	for _, call := range validateArgs {
		line := strings.Join(call, " ")
		for _, want := range []string{"--network none", data + ":/box/data:ro", "HI_VALIDATE=1", "make validate"} {
			if !strings.Contains(line, want) {
				t.Errorf("validation container lacks %q: %s", want, line)
			}
		}
		if strings.Contains(line, ".git") || strings.Contains(line, "PROXY") {
			t.Errorf("validation container reaches git or a proxy: %s", line)
		}
	}
	for _, call := range *calls {
		if call[0] == "run" && strings.Contains(strings.Join(call, " "), data) {
			t.Fatalf("a box got the validation data: %v", call)
		}
	}
	stdout.Reset()
	runAgent([]string{"best-of", "show", "b-1"}, strings.NewReader(""), &stdout, &stderr)
	for _, want := range []string{"validated by: make validate", "Best: 1.1 (validation 0.82) from round 2, baseline 1 (validation 0.8)", "VALID.", "rejected"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("show lacks %q:\n%s", want, stdout.String())
		}
	}
}
