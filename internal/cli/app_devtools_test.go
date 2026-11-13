package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// appTestLocalBox has the dev box's commands run here, in a real repo
// with a workspace w2 on it.
func appTestLocalBox(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	work := t.TempDir()
	git := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = work
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
	}
	git("init", "-q", "-b", "main")
	appDevSpacesCache.Lock()
	appDevSpacesCache.team, appDevSpacesCache.at, appDevSpacesCache.spaces = "payments", time.Now().Add(time.Hour), []appDevSpace{{ID: "w2", Branch: "2-more", Path: work}}
	appDevSpacesCache.Unlock()
	t.Cleanup(func() { appDevSpacesCache.Lock(); appDevSpacesCache.team = ""; appDevSpacesCache.Unlock() })
	previous := appDevBoxExec
	appDevBoxExec = func(team string, command ...string) (string, error) {
		var stdout, stderr bytes.Buffer
		cmd := exec.Command(command[0], command[1:]...)
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		if err := cmd.Run(); err != nil {
			return stdout.String(), errors.New(strings.TrimSpace(stderr.String()) + " " + err.Error())
		}
		return stdout.String(), nil
	}
	t.Cleanup(func() { appDevBoxExec = previous })
	os.WriteFile(filepath.Join(work, "app.go"), []byte("package app\n\nfunc Search(q string) {}\n// search again\n"), 0o644)
	os.WriteFile(filepath.Join(work, ".gitignore"), []byte("vendor/\n"), 0o644)
	git("add", "-A")
	git("commit", "-q", "-m", "First")
	os.MkdirAll(filepath.Join(work, "vendor"), 0o755)
	os.WriteFile(filepath.Join(work, "vendor", "lib.go"), []byte("func Search() {}\n"), 0o644)
	os.WriteFile(filepath.Join(work, "notes.md"), []byte("Search, in a new file\n"), 0o644)
	return work
}

func TestAppDevSearch(t *testing.T) {
	appTestLocalBox(t)
	matches, more, err := appDevSearch("payments", "w2", "search", false, false)
	if err != nil || more {
		t.Fatal(err, more)
	}
	var got []string
	for _, m := range matches {
		got = append(got, m.Path+":"+strconv.Itoa(m.Line)+":"+strconv.Itoa(m.Column))
	}
	// Tracked and new files, not what git ignores; any case.
	if strings.Join(got, " ") != "app.go:3:6 app.go:4:4 notes.md:1:1" {
		t.Fatalf("%v", got)
	}
	if matches, _, _ := appDevSearch("payments", "w2", "Search", true, false); len(matches) != 2 {
		t.Fatalf("match case: %+v", matches)
	}
	if matches, _, _ := appDevSearch("payments", "w2", `func [A-Z]\w+`, true, true); len(matches) != 1 || matches[0].Text != "func Search(q string) {}" {
		t.Fatalf("regex: %+v", matches)
	}
	for _, bad := range []string{"", "  ", "a\nb", strings.Repeat("x", 201)} {
		if _, _, err := appDevSearch("payments", "w2", bad, false, false); err == nil {
			t.Errorf("searched %q", bad)
		}
	}
	if _, _, err := appDevSearch("payments", "w2", "(", false, true); err == nil {
		t.Error("a broken regex")
	}
	if _, _, err := appDevSearch("payments", "w9", "x", false, false); err == nil {
		t.Error("a workspace that isn't")
	}
	// A dash first is what to find, never an option.
	if _, _, err := appDevSearch("payments", "w2", "--output=/tmp/x", false, false); err != nil {
		t.Error(err)
	}
}

func TestAppDevSearchParse(t *testing.T) {
	long := strings.Repeat("a", 400) + "needle" + strings.Repeat("b", 100)
	out := "x.go\x007\x003\x00 found\nlong.txt\x001\x00401\x00" + long + "\nnoise without fields\n"
	matches, more := appDevSearchParse(out)
	if more || len(matches) != 2 || matches[0].Text != " found" || matches[0].Line != 7 {
		t.Fatalf("%+v", matches)
	}
	m := matches[1]
	runes := []rune(m.Text)
	if !strings.HasPrefix(m.Text, "…") || !strings.HasSuffix(m.Text, "…") || len(runes) != appDevSearchText+2 || string(runes[m.Column-1:m.Column-1+6]) != "needle" {
		t.Fatalf("%d %q", m.Column, m.Text)
	}
	var many strings.Builder
	for i := 0; i < appDevSearchMax+1; i++ {
		many.WriteString("f\x001\x001\x00x\n")
	}
	if matches, more := appDevSearchParse(many.String()); len(matches) != appDevSearchMax || !more {
		t.Fatal(len(matches), more)
	}
}

func TestAppDevChecksParse(t *testing.T) {
	makefile := "help: ## Show the targets\ncheck: lint test ## Must pass before a change is done\nlint: ## Lint only\nrun: ## Serve\nVAR := 1\ntest:\n"
	got := appDevChecksParse(makefile + "\x1d{\"scripts\":{\"test\":\"vitest\"}}\x1d")
	if len(got) != 3 || got[0].Command != "make check" || got[0].About != "Must pass before a change is done" || got[1].Name != "test" || got[2].Name != "lint" {
		t.Fatalf("%+v", got)
	}
	got = appDevChecksParse("\x1d{\"scripts\":{\"dev\":\"vite\",\"test\":\"vitest run\",\"lint\":\"biome check\"}}\x1dbun.lock\n")
	if len(got) != 2 || got[0].Command != "bun run test" || got[1].Command != "bun run lint" || got[0].About != "vitest run" {
		t.Fatalf("%+v", got)
	}
	if got := appDevChecksParse("\x1d\x1d"); len(got) != 0 {
		t.Fatalf("%+v", got)
	}
}

func TestAppDevChecksRun(t *testing.T) {
	if _, err := exec.LookPath("setsid"); err != nil {
		t.Skip("no setsid")
	}
	work := appTestLocalBox(t)
	os.WriteFile(filepath.Join(work, "Makefile"), []byte("check: ## All of it\n\t@echo checking; echo oops >&2; exit 3\ntest: ## Slow\n\t@echo started; sleep 30; echo never\n"), 0o644)
	previous := appDevBoxStream
	appDevBoxStream = func(ctx context.Context, team string, out io.Writer, command ...string) (int, error) {
		cmd := exec.CommandContext(ctx, command[0], command[1:]...)
		cmd.Stdout, cmd.Stderr = out, out
		err := cmd.Run()
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return exit.ExitCode(), nil
		}
		return 0, err
	}
	t.Cleanup(func() { appDevBoxStream = previous })
	t.Cleanup(func() {
		appDevChecks.Lock()
		appDevChecks.runs, appDevChecks.found = map[string]*appDevCheckRun{}, map[string]appDevChecksFound{}
		appDevChecks.Unlock()
	})

	checks, err := appDevChecksFor("payments", "w2")
	if err != nil || len(checks) != 2 || checks[0].Name != "check" {
		t.Fatalf("%+v %v", checks, err)
	}
	wait := func() *appDevCheckRun {
		for i := 0; i < 100; i++ {
			if run := appDevCheckView("payments", "w2", 0); run != nil && !run.Running {
				return run
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatal("the check didn't end")
		return nil
	}
	if err := appDevStartCheck("payments", "w2", "check"); err != nil {
		t.Fatal(err)
	}
	run := wait()
	if run.Exit != 2 || !strings.Contains(run.Output, "$ make check\nchecking\noops") {
		t.Fatalf("%d %q", run.Exit, run.Output) // make exits 2 when a recipe fails
	}
	if later := appDevCheckView("payments", "w2", run.Offset); later.Output != "" || later.Reset {
		t.Fatalf("%+v", later)
	}
	if err := appDevStartCheck("payments", "w2", "deploy"); err == nil {
		t.Fatal("ran a check the project hasn't")
	}

	if err := appDevStartCheck("payments", "w2", "test"); err != nil {
		t.Fatal(err)
	}
	if err := appDevStartCheck("payments", "w2", "check"); err == nil {
		t.Fatal("two checks at once")
	}
	for i := 0; i < 100 && !strings.Contains(appDevCheckView("payments", "w2", 0).Output, "started"); i++ {
		time.Sleep(50 * time.Millisecond)
	}
	if err := appDevStopCheck("payments", "w2"); err != nil {
		t.Fatal(err)
	}
	run = wait()
	if !run.Stopped || strings.Contains(run.Output, "never") || run.Exit == 0 {
		t.Fatalf("%+v", run)
	}
	if err := appDevStopCheck("payments", "w2"); err == nil {
		t.Fatal("stopped what isn't running")
	}
}

func TestAppDevCheckOutputKeepsTheEnd(t *testing.T) {
	run := &appDevCheckRun{}
	appDevChecks.Lock()
	appDevChecks.runs["payments\nw7"] = run
	appDevChecks.Unlock()
	t.Cleanup(func() { appDevChecks.Lock(); delete(appDevChecks.runs, "payments\nw7"); appDevChecks.Unlock() })
	run.Write(bytes.Repeat([]byte("x"), appDevCheckKeep))
	run.Write([]byte("tail"))
	view := appDevCheckView("payments", "w7", 0)
	if !view.Reset || len(view.Output) != appDevCheckKeep || !strings.HasSuffix(view.Output, "tail") || view.Offset != int64(appDevCheckKeep+4) {
		t.Fatalf("%v %d %d", view.Reset, len(view.Output), view.Offset)
	}
	if view := appDevCheckView("payments", "w7", view.Offset-4); view.Output != "tail" || view.Reset {
		t.Fatalf("%+v", view.Output)
	}
}

func TestAppDevLogs(t *testing.T) {
	setupTeamTest(t)
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	os.MkdirAll(filepath.Join(appDir("payments"), "run"), 0o755)
	if view, err := appDevLogs("payments", "production", "output", ""); err != nil || view.Note == "" {
		t.Fatalf("nothing released: %+v %v", view, err)
	}
	os.WriteFile(appSlotPath("payments", "production"), []byte(`{"label":"production v3","container":"hi-box-app-payments-app-production-4100","expose":"app-payments-live-Ab12","url":"https://payments.example.com/?hi_key=k"}`), 0o600)
	var asked []string
	previous := appJournal
	appJournal = func(args ...string) ([]byte, error) {
		asked = append(asked, strings.Join(args, " "))
		if strings.Contains(strings.Join(args, " "), "--after-cursor=c2") {
			return []byte(`{"MESSAGE":"later","PRIORITY":"6","__REALTIME_TIMESTAMP":"1793893289343820","__CURSOR":"c3"}` + "\n"), nil
		}
		return []byte(`{"MESSAGE":"Listening on :8080\n","PRIORITY":"6","__REALTIME_TIMESTAMP":"1793893221978157","__CURSOR":"c1"}` + "\n" +
			`{"MESSAGE":[98,97,100,255],"PRIORITY":"3","__REALTIME_TIMESTAMP":"1793893221978200","__CURSOR":"c2"}` + "\n"), nil
	}
	t.Cleanup(func() { appJournal = previous })
	view, err := appDevLogs("payments", "production", "output", "")
	if err != nil || !view.Reset || len(view.Lines) != 2 || view.Lines[0].Text != "Listening on :8080" || view.Lines[0].Error || !view.Lines[1].Error ||
		view.Lines[1].Text != "bad�" || view.Cursor != "c2|hi-box-app-payments-app-production-4100" || view.Label != "production v3" {
		t.Fatalf("%+v %v", view, err)
	}
	if !strings.Contains(asked[0], "CONTAINER_NAME=hi-box-app-payments-app-production-4100") || !strings.Contains(asked[0], "-n 400") {
		t.Fatal(asked)
	}
	view, _ = appDevLogs("payments", "production", "output", view.Cursor)
	if view.Reset || len(view.Lines) != 1 || view.Lines[0].Text != "later" || view.Cursor != "c3|hi-box-app-payments-app-production-4100" {
		t.Fatalf("%+v", view)
	}
	// A release since: another box, so from its start.
	view, _ = appDevLogs("payments", "production", "output", "c3|hi-box-app-payments-app-production-3999")
	if !view.Reset {
		t.Fatal("a cursor of the box before")
	}

	dir := filepath.Join(state, "hi", "net", "expose")
	if netExposeDir() != dir {
		dir = netExposeDir()
	}
	os.MkdirAll(dir, 0o700)
	os.WriteFile(filepath.Join(dir, "app-payments-live-Ab12.json"), []byte(`{"name":"app-payments-live"}`), 0o600)
	os.WriteFile(filepath.Join(dir, "app-payments-live-1700000000.log"), []byte("GET    /old    200  1 ms\n"), 0o600)
	log := filepath.Join(dir, "app-payments-live-1800000000.log")
	os.WriteFile(log, []byte("Exposing https://payments.example.com\nGET    /                                        200  3 ms\nPOST   /api/pay                                 502  9 ms\nGET    /half"), 0o600)
	view, err = appDevLogs("payments", "production", "requests", "")
	if err != nil || !view.Reset || len(view.Lines) != 3 || view.Lines[1].Status != 200 || view.Lines[2].Status != 502 || !view.Lines[2].Error || view.Lines[1].Error {
		t.Fatalf("%+v %v", view, err)
	}
	f, _ := os.OpenFile(log, os.O_APPEND|os.O_WRONLY, 0o600)
	f.WriteString("    404  1 ms\n")
	f.Close()
	view, _ = appDevLogs("payments", "production", "requests", view.Cursor)
	if view.Reset || len(view.Lines) != 1 || view.Lines[0].Status != 404 || !strings.HasPrefix(view.Lines[0].Text, "GET    /half") {
		t.Fatalf("%+v", view)
	}
	if _, err := appDevLogs("payments", "elsewhere", "output", ""); err == nil {
		t.Fatal("a slot that isn't")
	}
}

func TestAppDevPageFrames(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	os.MkdirAll(filepath.Join(appDir("payments"), "run"), 0o755)
	os.WriteFile(appSlotPath("payments", "preview"), []byte(`{"url":"https://payments-preview.example.com/?hi_key=k"}`), 0o600)
	os.WriteFile(appSlotPath("payments", "production"), []byte(`{"url":"javascript:alert(1)"}`), 0o600)
	if got := b.devPageFrames(); got != "frame-src https://payments-preview.example.com" {
		t.Fatal(got)
	}
	for raw, want := range map[string]string{"http://h:8080/x": "http://h:8080", "https://a.b?c": "https://a.b", "ftp://x": "", "https://a b": "", "https://x;script-src *": ""} {
		if got := appOrigin(raw); got != want {
			t.Errorf("%s: %q", raw, got)
		}
	}
}
