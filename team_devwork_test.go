package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// teamTestDevBranch commits a file on a branch of the dev box's clone.
func teamTestDevBranch(t *testing.T, branch, file, text, subject string) {
	t.Helper()
	clone := filepath.Join(teamDevDir("payments"), "repo")
	if !fileExists(filepath.Join(clone, ".git")) {
		if err := prepareTeamDevRepo("payments"); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"checkout", "-q", "-B", branch, "main"}} {
		if out, err := teamGit(clone, args...); err != nil {
			t.Fatal(out, err)
		}
	}
	os.MkdirAll(filepath.Dir(filepath.Join(clone, file)), 0o700)
	os.WriteFile(filepath.Join(clone, file), []byte(text), 0o600)
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-q", "-m", subject}} {
		if out, err := teamGit(clone, args...); err != nil {
			t.Fatal(out, err)
		}
	}
}

// The dev box puts its branch on the preview and ships it into dev, as
// work the versions and the release list.
func TestTeamDevPreviewAndShip(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	teamTestRunnable(b)
	teamTestDevBranch(t, "chart", "src/chart.ts", "export const days = 31\n", "Show the last day on the chart")

	for _, bad := range []string{"../etc", "-x", "nope"} {
		if _, err := b.previewDevBranch(bad); err == nil {
			t.Fatal("previewed", bad)
		}
	}
	state, err := b.previewDevBranch("chart")
	if err != nil || state.Source != "box/chart" || !strings.Contains(state.Label, "not in dev yet") {
		t.Fatalf("%+v %v", state, err)
	}

	result, err := b.shipDevBranch(teamDevShipRequest{Branch: "chart"})
	if err != nil {
		t.Fatal(err)
	}
	task := result.Task
	if task.Role != teamDevRole || task.Status != "merged" || task.Title != "Show the last day on the chart" || task.Merged == "" {
		t.Fatalf("%+v", task)
	}
	if data, ok := teamGitShow(b.repo, "main", "src/chart.ts"); !ok || !strings.Contains(data, "31") {
		t.Fatal("not in dev:", data)
	}
	if result.Preview.Source != "main" {
		t.Fatalf("dev isn't on the preview: %+v", result.Preview)
	}
	view, err := teamVersionsReport("payments")
	if err != nil || len(view.Changes) != 1 || view.Changes[0].ID != task.ID || teamTaskAskedBy(view.Changes[0]) != "built in the dev box" {
		t.Fatalf("%+v %v", view, err)
	}
	saveTeamSlot("payments", result.Preview)
	if status, err := b.devStatus(); err != nil || status.Preview != "dev, 1 change, nothing released yet" {
		t.Fatalf("%+v %v", status, err)
	}
	// Released since: the preview of the same dev is called what dev is now.
	boxGit(b.repo, "branch", "-f", teamProductionBranch, "main")
	saveTeamVersions("payments", teamVersions{Production: 1})
	if status, err := b.devStatus(); err != nil || status.Preview != "dev, the same as production v1" {
		t.Fatalf("%+v %v", status, err)
	}
	if _, err := b.shipDevBranch(teamDevShipRequest{Branch: "chart"}); err == nil || !strings.Contains(err.Error(), "already") {
		t.Fatal("shipped twice:", err)
	}

	// A branch that conflicts with dev says how to fix it in the box.
	teamTestDevBranch(t, "chart-two", "src/chart.ts", "export const days = 7\n", "Show a week")
	if _, err := b.shipDevBranch(teamDevShipRequest{Branch: "chart-two", Title: "A week"}); err == nil || !strings.Contains(err.Error(), "git rebase origin/main") {
		t.Fatal(err)
	}
	if status := boxGit(b.repo, "status", "--porcelain"); status != "" {
		t.Fatal("a merge was left half done:", status)
	}
}

// The dev box's socket has nothing that moves production.
func TestTeamDevSocketHasNoProduction(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	handler := b.devHandler()
	for _, path := range []string{"/release", "/rollback", "/restore", "/data/copy", "/task", "/merge", "/snapshot", "/owners"} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, path, strings.NewReader("{}")))
		if recorder.Code != http.StatusNotFound {
			t.Errorf("%s answered %d", path, recorder.Code)
		}
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/status", nil))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"dev"`) {
		t.Fatal(recorder.Code, recorder.Body.String())
	}
}

func TestTeamTicketForge(t *testing.T) {
	setupTeamTest(t)
	os.MkdirAll(teamDir("payments"), 0o700)
	cases := map[string]teamForge{
		"":                                          {Kind: "hi"},
		"/home/hi/projects/quarry":                  {Kind: "hi"},
		"https://github.com/acme/quarry.git":        {Kind: "github", Host: "github.com", Repo: "acme/quarry"},
		"git@github.com:acme/quarry.git":            {Kind: "github", Host: "github.com", Repo: "acme/quarry"},
		"https://gitlab.com/acme/tools/quarry":      {Kind: "gitlab", Host: "gitlab.com", Repo: "acme/tools/quarry"},
		"ssh://git@gitlab.example.com:2222/a/b.git": {Kind: "gitlab", Host: "gitlab.example.com", Repo: "a/b"},
		"https://bitbucket.org/acme/quarry.git":     {Kind: "hi"},
	}
	for remote, want := range cases {
		config := testTeamConfig()
		config.Remote = remote
		saveTeam(config)
		if got := teamTicketForge("payments"); got != want {
			t.Errorf("%q: %+v, want %+v", remote, got, want)
		}
	}
}

// hi wraps gh and glab, and keeps its own tickets for a repository on
// neither.
func TestTeamTickets(t *testing.T) {
	setupTeamTest(t)
	os.MkdirAll(teamDir("payments"), 0o700)
	config := testTeamConfig()
	saveTeam(config)
	do := func(args ...string) string {
		t.Helper()
		request, err := parseTeamTicketArgs(args, strings.NewReader("Body from stdin\n"))
		if err != nil {
			t.Fatal(err)
		}
		request.Author = "the dev box"
		result, err := teamTicketDo("payments", request)
		if err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		printTeamTicketResult(request, result, &out)
		return out.String()
	}
	// hi's own.
	if out := do("add", "Chart misses the last day", "-"); !strings.Contains(out, "Filed #1") {
		t.Fatal(out)
	}
	do("add", "Export to CSV")
	do("comment", "1", "On the preview now")
	if out := do("close", "#1", "Shipped in t-3"); !strings.Contains(out, "Closed #1") {
		t.Fatal(out)
	}
	if out := do("list"); strings.Contains(out, "#1") || !strings.Contains(out, "#2") {
		t.Fatal(out)
	}
	if out := do("show", "1"); !strings.Contains(out, "Body from stdin") || !strings.Contains(out, "Shipped in t-3") || !strings.Contains(out, "(closed)") {
		t.Fatal(out)
	}
	for _, bad := range [][]string{{"show"}, {"show", "x"}, {"add"}, {"comment", "1"}, {"frob"}} {
		if _, err := parseTeamTicketArgs(bad, strings.NewReader("")); err == nil {
			t.Error("took", bad)
		}
	}

	// GitHub, through gh.
	var calls []string
	var env []string
	answers := map[string]string{}
	previous := teamForgeRun
	teamForgeRun = func(e []string, name string, args ...string) (string, error) {
		calls = append(calls, name+" "+strings.Join(args, " "))
		env = e
		for prefix, answer := range answers {
			if strings.HasPrefix(name+" "+strings.Join(args, " "), prefix) {
				return answer, nil
			}
		}
		return "", nil
	}
	t.Cleanup(func() { teamForgeRun = previous })
	config.Remote = "https://github.com/acme/quarry.git"
	saveTeam(config)
	answers["gh issue list"] = `[{"number":7,"title":"Dark mode","state":"OPEN","labels":[{"name":"idea"}],"author":{"login":"ann"},"url":"https://github.com/acme/quarry/issues/7"}]`
	answers["gh issue create"] = "https://github.com/acme/quarry/issues/8\n"
	if out := do("list"); !strings.Contains(out, "#7") || !strings.Contains(out, "Dark mode [idea]") || !strings.Contains(out, "acme/quarry") {
		t.Fatal(out)
	}
	if out := do("add", "A bug", "It breaks"); !strings.Contains(out, "Filed #8") {
		t.Fatal(out)
	}
	do("close", "8", "Fixed")
	if last := calls[len(calls)-1]; last != "gh issue close 8 --comment Fixed -R acme/quarry" {
		t.Fatal(last)
	}

	// A GitLab of one's own, through glab.
	calls = nil
	config.Remote = "git@gitlab.example.com:acme/quarry.git"
	saveTeam(config)
	answers["glab issue list"] = `[{"iid":3,"title":"Faster charts","state":"opened","labels":["perf"],"author":{"username":"bo"},"web_url":"https://gitlab.example.com/acme/quarry/-/issues/3"}]`
	if out := do("list"); !strings.Contains(out, "#3") || !strings.Contains(out, "Faster charts [perf]") {
		t.Fatal(out)
	}
	if strings.Join(env, " ") != "GITLAB_HOST=gitlab.example.com" || !strings.HasSuffix(calls[0], "-R acme/quarry") {
		t.Fatal(env, calls)
	}
	do("close", "3", "Done")
	if strings.Join(calls[1:], " | ") != "glab issue note 3 -m Done -R acme/quarry | glab issue close 3 -R acme/quarry" {
		t.Fatal(calls)
	}
}

// The skills and instructions go into the box's home; what an owner wrote
// around hi's block stays, and the block isn't added twice.
func TestTeamDevKit(t *testing.T) {
	home := t.TempDir()
	previous := teamDevHerdrSkill
	teamDevHerdrSkill = func(string) (string, error) { return "---\nname: herdr\n---\n# Herdr\n", nil }
	t.Cleanup(func() { teamDevHerdrSkill = previous })
	os.MkdirAll(filepath.Join(home, ".claude"), 0o700)
	os.WriteFile(filepath.Join(home, ".claude", "CLAUDE.md"), []byte("My own notes.\n"), 0o600)
	// A skill hi wrote under its name before 2.5.14 goes.
	os.MkdirAll(filepath.Join(home, ".agents", "skills", "hi-dev-tickets"), 0o700)
	os.MkdirAll(filepath.Join(home, ".claude", "skills"), 0o700)
	os.Symlink(filepath.Join("..", "..", ".agents", "skills", "hi-dev-tickets"), filepath.Join(home, ".claude", "skills", "hi-dev-tickets"))
	config := testTeamConfig()
	for i := 0; i < 2; i++ {
		if err := installTeamDevKit(config, home); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Lstat(filepath.Join(home, ".claude", "skills", "hi-dev-tickets")); err == nil || fileExists(filepath.Join(home, ".agents", "skills", "hi-dev-tickets")) {
		t.Fatal("the old tickets skill is still there")
	}
	for _, name := range []string{"hi-dev-ship", "hi-dev-tasks", "hi-dev-say", "hi-dev-status", "hi-dev-review", "hi-dev-brainstorm", "hi-dev-data", "hi-dev-settings", "hi-dev-incidents", "hi-dev-survey", "hi-dev-upstream", "hi-dev-agents"} {
		data, err := os.ReadFile(filepath.Join(home, ".claude", "skills", name, "SKILL.md"))
		if err != nil || !strings.Contains(string(data), "name: "+name) {
			t.Fatal(name, err)
		}
	}
	if data, _ := os.ReadFile(filepath.Join(home, ".agents", "skills", "herdr", "SKILL.md")); !strings.Contains(string(data), "# Herdr") {
		t.Fatal("no herdr skill")
	}
	claude, _ := os.ReadFile(filepath.Join(home, ".claude", "CLAUDE.md"))
	if strings.Count(string(claude), teamDevBlockStart) != 1 || !strings.Contains(string(claude), "My own notes.") || !strings.Contains(string(claude), "payments team's dev box") {
		t.Fatal(string(claude))
	}
	if codex, _ := os.ReadFile(filepath.Join(home, ".codex", "AGENTS.md")); !strings.Contains(string(codex), "hi dev ship merges it into dev (main)") {
		t.Fatal(string(codex))
	}
}

// The setup key goes as soon as the peer has joined.
func TestTeamDevJoined(t *testing.T) {
	setupTeamDevTest(t)
	previous := teamDevJoinPoll
	teamDevJoinPoll = 10 * time.Millisecond
	t.Cleanup(func() { teamDevJoinPoll = previous })
	setTeamDevNetBird("payments", "6F1A2B3C-4D5E-6F70-8192-A3B4C5D6E7F8")
	if teamDevJoined("payments", 30*time.Millisecond) {
		t.Fatal("joined without an identity")
	}
	// The key goes once the peer has joined.
	go func() {
		time.Sleep(20 * time.Millisecond)
		os.MkdirAll(filepath.Join(teamDevDir("payments"), "netbird"), 0o700)
		os.WriteFile(filepath.Join(teamDevDir("payments"), "netbird", "default.json"), []byte("{}"), 0o600)
	}()
	if !teamDevJoined("payments", time.Second) || fileExists(filepath.Join(teamDevDir("payments"), teamDevSetupKey)) {
		t.Fatal("the key stayed")
	}
}

// What people share goes with the ticket: kept on the machine for any
// forge, and fetched in the box.
func TestTeamTicketFiles(t *testing.T) {
	setupTeamTest(t)
	os.MkdirAll(teamDir("payments"), 0o700)
	saveTeam(testTeamConfig())
	picture := filepath.Join(t.TempDir(), "reference.jpg")
	os.WriteFile(picture, []byte("jpeg"), 0o600)
	request, err := parseTeamTicketArgs([]string{"add", "Neon background", "Like this", "--attach", picture}, nil)
	if err != nil || len(request.Files) != 1 || request.Body != "Like this" {
		t.Fatalf("%+v %v", request, err)
	}
	request.Author = "the lead"
	result, err := teamTicketDo("payments", request)
	if err != nil || result.Ticket == nil || len(result.Ticket.Files) != 1 || result.Ticket.Files[0] != "reference.jpg" {
		t.Fatalf("%+v %v", result, err)
	}
	shown, _ := teamTicketDo("payments", teamTicketRequest{Action: "show", Number: 1})
	if len(shown.Ticket.Comments) != 1 || !strings.Contains(shown.Ticket.Comments[0].Body, "hi dev task files 1") || len(shown.Ticket.Files) != 1 {
		t.Fatalf("%+v", shown.Ticket)
	}
	// A comment adds more; the same name is kept apart.
	more, _ := parseTeamTicketArgs([]string{"comment", "1", "--attach", picture}, nil)
	if _, err := teamTicketDo("payments", more); err != nil {
		t.Fatal(err)
	}
	files, err := teamTicketDo("payments", teamTicketRequest{Action: "files", Number: 1})
	if err != nil || len(files.Files) != 2 || files.Files[0].Name != "reference-2.jpg" || files.Files[1].Name != "reference.jpg" || string(files.Files[1].Data) != "jpeg" {
		t.Fatalf("%+v %v", files, err)
	}
	// In a box, they go to ~/hi/tasks/<n>/.
	home := t.TempDir()
	t.Setenv("HOME", home)
	var out strings.Builder
	if err := teamSaveFetchedTicketFiles(1, files, &out); err != nil || !fileExists(filepath.Join(home, "hi", "tasks", "1", "reference-2.jpg")) {
		t.Fatal(err, out.String())
	}
	if _, err := teamTicketDo("payments", teamTicketRequest{Action: "files", Number: 9}); err == nil {
		t.Fatal("files for a task without any")
	}
}
