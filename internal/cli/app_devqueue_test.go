package cli

import (
	"os"
	"strings"
	"testing"
	"time"
)

// A message sent while the agent works waits in the queue; hi sends it
// when the agent's turn ends, one at a time. Steer sends one at once, and
// remove takes one out unsent. The queue is in a file, for a restart.
func TestTeamDevChatQueue(t *testing.T) {
	b, ran := appTestDevPage(t)
	handler := b.devPageHandler()
	status := "working"
	previous := appDevBoxExec
	appDevBoxExec = func(team string, command ...string) (string, error) {
		switch strings.Join(command[:min(3, len(command))], " ") {
		case "herdr pane list":
			return `{"result":{"panes":[{"pane_id":"w2:p1","agent":"claude","agent_status":"` + status + `","cwd":"/box/home/.herdr/worktrees/repo/1-chart"}]}}`, nil
		case "herdr pane read":
			return "> \n", nil
		}
		return previous(team, command...)
	}
	t.Cleanup(func() { appDevBoxExec = previous })
	prompts := func() []string {
		var sent []string
		for _, line := range *ran {
			if strings.HasPrefix(line, "herdr agent prompt") {
				sent = append(sent, line)
			}
		}
		return sent
	}
	for _, text := range []string{"Add a legend", "Make it green", "Then the tests"} {
		if code, body := appTestDevPageCall(t, handler, appTestOwnerKey, "POST", "api/chat/queue", map[string]any{"workspace": "w2", "text": text, "shown": text}); code != 200 {
			t.Fatal(code, body)
		}
	}
	queue := b.devQueue("w2")
	if len(queue) != 3 || queue[0].Shown != "Add a legend" || queue[0].By == "" || len(prompts()) != 0 {
		t.Fatalf("%+v %q", queue, prompts())
	}
	// It's in the file: a restart keeps it.
	b.devQueued = appDevQueueState{}
	if len(b.devQueue("w2")) != 3 {
		t.Fatal("the queue didn't come back from its file")
	}
	// Remove: out, unsent.
	if code, body := appTestDevPageCall(t, handler, appTestOwnerKey, "POST", "api/chat/queue/remove", map[string]any{"workspace": "w2", "id": queue[1].ID}); code != 200 {
		t.Fatal(code, body)
	}
	// Steer: now, into the turn that runs, though Claude works.
	if code, body := appTestDevPageCall(t, handler, appTestOwnerKey, "POST", "api/chat/queue/steer", map[string]any{"workspace": "w2", "id": queue[2].ID}); code != 200 || len(prompts()) != 1 || !strings.Contains(prompts()[0], "Then the tests") {
		t.Fatal(code, body, prompts())
	}
	if code, _ := appTestDevPageCall(t, handler, appTestOwnerKey, "POST", "api/chat/queue/steer", map[string]any{"workspace": "w2", "id": queue[2].ID}); code == 200 {
		t.Fatal("steered one twice")
	}
	// While Claude works, the queue waits; when its turn ends, the next goes.
	now := time.Now()
	b.sendDevQueue(now)
	if len(prompts()) != 1 {
		t.Fatal(prompts())
	}
	status = "idle"
	if !b.sendDevQueue(now) || len(prompts()) != 2 || !strings.Contains(prompts()[1], "Add a legend") || len(b.devQueue("w2")) != 0 {
		t.Fatal(prompts(), b.devQueue("w2"))
	}
	// Nothing waits: the loop stops, and the file goes.
	if b.sendDevQueue(now.Add(time.Minute)) {
		t.Fatal("the loop goes on with nothing queued")
	}
	if _, err := os.Stat(appDevQueueFile(b.team)); !os.IsNotExist(err) {
		t.Fatal("the queue's file stays", err)
	}
}

// One sent, the next waits a while: the agent has started on it.
func TestTeamDevChatQueueOneAtATime(t *testing.T) {
	b, ran := appTestDevPage(t)
	previous := appDevBoxExec
	appDevBoxExec = func(team string, command ...string) (string, error) {
		switch strings.Join(command[:min(3, len(command))], " ") {
		case "herdr pane list":
			return `{"result":{"panes":[{"pane_id":"w2:p1","agent":"claude","agent_status":"idle","cwd":"/box/home/.herdr/worktrees/repo/1-chart"}]}}`, nil
		case "herdr pane read":
			return "> \n", nil
		}
		return previous(team, command...)
	}
	t.Cleanup(func() { appDevBoxExec = previous })
	b.devQueueAdd("w2", appDevQueued{Text: "one", Shown: "one"})
	b.devQueueAdd("w2", appDevQueued{Text: "two", Shown: "two"})
	now := time.Now()
	b.sendDevQueue(now)
	b.sendDevQueue(now.Add(time.Second))
	count := strings.Count(strings.Join(*ran, "\n"), "herdr agent prompt")
	if count != 1 || len(b.devQueue("w2")) != 1 {
		t.Fatal(count, *ran)
	}
	b.sendDevQueue(now.Add(appDevQueueGap))
	if strings.Count(strings.Join(*ran, "\n"), "herdr agent prompt") != 2 || len(b.devQueue("w2")) != 0 {
		t.Fatal(*ran)
	}
}
