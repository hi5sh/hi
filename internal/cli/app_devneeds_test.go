package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// An agent that waits gets one DM, once it has waited ten minutes with
// nobody active on the dev page: to whoever started its work, else the
// owners. A new wait gets one again.
func TestAppDevNeedsYouDM(t *testing.T) {
	appTestHooksReset(t)
	b, _ := appTestDevPage(t)
	channel := &fakeTeamChannel{}
	b.channel = channel
	data, _ := json.Marshal(appDevPageState{URL: "https://dev-payments.corp.example/__hi/dev/?k=secret"})
	os.MkdirAll(filepath.Dir(appDevPageStatePath("payments")), 0o700)
	os.WriteFile(appDevPageStatePath("payments"), data, 0o600)
	dms := func() []string {
		var out []string
		for _, call := range channel.callsNow() {
			if strings.HasPrefix(call, "dm ") {
				out = append(out, call)
			}
		}
		return out
	}
	start := time.Now()
	appDevHookRecord("payments", appDevHookEvent{Workspace: "w2", Agent: "claude", Event: "PermissionRequest", Tool: "Edit"}, start)
	if err := b.checkNeedsYou(start.Add(5 * time.Minute)); err != nil || len(dms()) != 0 {
		t.Fatal("too soon:", err, dms())
	}
	// Someone's on the page, and active: they see it.
	board := devPresenceOf("payments")
	id, _ := board.join("U07OWNER1", "Olga")
	b.checkNeedsYou(start.Add(11 * time.Minute))
	if len(dms()) != 0 {
		t.Fatal("someone's here:", dms())
	}
	// Their window idle: nobody's really here. Nobody known started it: the owners.
	board.setIdle(id, true, 0)
	defer board.leave(id)
	if err := b.checkNeedsYou(start.Add(11 * time.Minute)); err != nil {
		t.Fatal(err)
	}
	got := dms()
	if len(got) != 1 || !strings.HasPrefix(got[0], "dm U07OWNER1 ") || !strings.Contains(got[0], "*1-chart*") || !strings.Contains(got[0], "permission for Edit") ||
		!strings.Contains(got[0], "waited 11 minutes") || !strings.Contains(got[0], "https://dev-payments.corp.example/__hi/dev/#work-1-chart") || strings.Contains(got[0], "secret") {
		t.Fatal(got)
	}
	// Once per wait.
	b.checkNeedsYou(start.Add(12 * time.Minute))
	if len(dms()) != 1 {
		t.Fatal("twice:", dms())
	}
	// Answered, then a new wait, by work someone started from the page: to them.
	appDevStartedUser("payments", "1-chart", "U07ASKER1")
	appDevStartedUser("payments", "2-other", "not an id")
	if users := appDevStarterUsers("payments"); users["1-chart"] != "U07ASKER1" || users["2-other"] != "" {
		t.Fatal(users)
	}
	later := start.Add(20 * time.Minute)
	appDevHookRecord("payments", appDevHookEvent{Workspace: "w2", Agent: "claude", Event: "PostToolUse"}, later)
	b.checkNeedsYou(later.Add(time.Minute))
	appDevHookRecord("payments", appDevHookEvent{Workspace: "w2", Agent: "claude", Event: "PreToolUse", Tool: "AskUserQuestion"}, later.Add(2*time.Minute))
	b.checkNeedsYou(later.Add(13 * time.Minute))
	got = dms()
	if len(got) != 2 || !strings.HasPrefix(got[1], "dm U07ASKER1 ") || !strings.Contains(got[1], "a question") {
		t.Fatal(got)
	}
	if !fileExists(filepath.Join(appDir("payments"), "run", "dev-start-users.json")) {
		t.Fatal("no starters' file")
	}
}
