package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Queued messages, as in Orca's chat: a message sent while the agent
// works waits in a queue above the chat's box, and hi sends the next one
// when the agent's turn ends. Steer sends one at once instead, into the
// turn that runs, as Send did before: Claude Code reads it when its
// current step ends. The queue is the broker's, in run/chat-queue.json,
// so it goes on with the page closed, and after a restart.

// appDevQueued is one message that waits.
type appDevQueued struct {
	ID string `json:"id"`
	// Text is what the agent gets: with the paths of the files attached,
	// and the notes on the parts pointed at in the Preview.
	Text string `json:"text"`
	// Shown is what the person wrote, for the page.
	Shown string    `json:"shown"`
	By    string    `json:"by,omitempty"`
	At    time.Time `json:"at"`
}

// appDevQueueState is each workspace's queue, and when hi last sent one
// of it.
type appDevQueueState struct {
	mu     sync.Mutex
	queues map[string][]appDevQueued
	sent   map[string]time.Time
	loaded bool
	// running says the loop that sends runs.
	running bool
}

var (
	// appDevQueueEvery is how often the loop looks at the agents, and
	// appDevQueueGap how long after hi sent one it waits before the next,
	// so the agent has started on it.
	appDevQueueEvery = 2 * time.Second
	appDevQueueGap   = 15 * time.Second
	// appDevQueueMax is the most messages a workspace's queue holds.
	appDevQueueMax = 20
)

func appDevQueueFile(team string) string {
	return filepath.Join(appDir(team), "run", "chat-queue.json")
}

// load reads the file, once.
func (q *appDevQueueState) load(team string) {
	if q.loaded {
		return
	}
	q.loaded = true
	q.queues, q.sent = map[string][]appDevQueued{}, map[string]time.Time{}
	if data, err := os.ReadFile(appDevQueueFile(team)); err == nil {
		json.Unmarshal(data, &q.queues)
	}
}

// save writes the file; with nothing queued, the file goes.
func (q *appDevQueueState) save(team string) error {
	for space, list := range q.queues {
		if len(list) == 0 {
			delete(q.queues, space)
		}
	}
	if len(q.queues) == 0 {
		if err := os.Remove(appDevQueueFile(team)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	data, err := json.MarshalIndent(q.queues, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(appDevQueueFile(team)), 0o700); err != nil {
		return err
	}
	return writeFileAtomic(appDevQueueFile(team), data, 0o600)
}

// devQueue is a workspace's queue, oldest first.
func (b *appBroker) devQueue(workspace string) []appDevQueued {
	q := &b.devQueued
	q.mu.Lock()
	defer q.mu.Unlock()
	q.load(b.team)
	return append([]appDevQueued{}, q.queues[workspace]...)
}

// devQueueAdd puts a message at the end of the queue, and starts the loop.
func (b *appBroker) devQueueAdd(workspace string, item appDevQueued) error {
	q := &b.devQueued
	q.mu.Lock()
	defer q.mu.Unlock()
	q.load(b.team)
	if len(q.queues[workspace]) >= appDevQueueMax {
		return fmt.Errorf("the queue has %d messages, which is its most; wait for the agent, or steer one", appDevQueueMax)
	}
	item.ID = fmt.Sprintf("q%d", time.Now().UnixNano())
	item.At = time.Now().UTC()
	q.queues[workspace] = append(q.queues[workspace], item)
	if err := q.save(b.team); err != nil {
		return err
	}
	b.devQueueStart()
	return nil
}

// devQueueTake takes one message out of the queue, by its id.
func (b *appBroker) devQueueTake(workspace, id string) (appDevQueued, error) {
	q := &b.devQueued
	q.mu.Lock()
	defer q.mu.Unlock()
	q.load(b.team)
	list := q.queues[workspace]
	for i, item := range list {
		if item.ID == id {
			q.queues[workspace] = append(list[:i:i], list[i+1:]...)
			return item, q.save(b.team)
		}
	}
	return appDevQueued{}, errors.New("that message isn't in the queue any more: hi sent it, or someone took it out")
}

// devQueuePutBack puts a message back at the front, when Steer failed.
func (b *appBroker) devQueuePutBack(workspace string, item appDevQueued) {
	q := &b.devQueued
	q.mu.Lock()
	defer q.mu.Unlock()
	q.load(b.team)
	q.queues[workspace] = append([]appDevQueued{item}, q.queues[workspace]...)
	q.save(b.team)
	b.devQueueStart()
}

// devQueueStart runs the loop that sends, unless it runs. Called with
// the lock held.
func (b *appBroker) devQueueStart() {
	q := &b.devQueued
	if q.running || b.devQueueStop == nil {
		return
	}
	q.running = true
	go b.keepDevQueue(b.devQueueStop)
}

// keepDevQueue sends the queued messages, each when its agent is free,
// until the queues are empty or done.
func (b *appBroker) keepDevQueue(done <-chan struct{}) {
	ticker := time.NewTicker(appDevQueueEvery)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			b.devQueued.mu.Lock()
			b.devQueued.running = false
			b.devQueued.mu.Unlock()
			return
		case <-ticker.C:
			if !b.sendDevQueue(time.Now()) {
				return
			}
		}
	}
}

// sendDevQueue sends the next message of each queue whose agent is free,
// and says whether any wait still. With none, the loop stops.
func (b *appBroker) sendDevQueue(now time.Time) bool {
	q := &b.devQueued
	q.mu.Lock()
	q.load(b.team)
	spaces := []string{}
	for space, list := range q.queues {
		if len(list) > 0 && now.Sub(q.sent[space]) >= appDevQueueGap {
			spaces = append(spaces, space)
		}
	}
	if len(q.queues) == 0 {
		q.running = false
		q.mu.Unlock()
		return false
	}
	q.mu.Unlock()
	for _, space := range spaces {
		agent, _, err := devChatAgent(b.team, space)
		if err != nil || !agent.Running || agent.Status == "working" || agent.Status == "blocked" {
			continue
		}
		q.mu.Lock()
		list := q.queues[space]
		if len(list) == 0 {
			q.mu.Unlock()
			continue
		}
		next := list[0]
		q.mu.Unlock()
		if err := appDevChatPrompt(b.team, agent, next.Text); err != nil {
			continue // a question of the agent's own: it waits until it's answered
		}
		q.mu.Lock()
		if list := q.queues[space]; len(list) > 0 && list[0].ID == next.ID {
			q.queues[space] = list[1:]
		}
		q.sent[space] = now
		q.save(b.team)
		q.mu.Unlock()
	}
	return true
}

// appDevChatPrompt gives the agent a message, as typed in its terminal.
// An agent's own dialog, which herdr takes for idle, would take the
// message as its keys, so hi refuses then.
func appDevChatPrompt(team string, agent appDevChatAgent, text string) error {
	if !agent.Running {
		return errors.New("no agent is running in this workspace; resume the session, or start one from the task")
	}
	if agent.Status != "blocked" && agent.Status != "working" {
		if screen, _, err := devChatScreen(team, agent.Pane); err == nil {
			if ask := appDevParseAsk(screen); appDevAskTabbed(ask) || appDevAskDialog(screen, ask) {
				who := firstNonEmpty(map[string]string{"claude": "Claude", "codex": "Codex", "agy": "agy"}[agent.Agent], "The agent")
				return fmt.Errorf("%s is asking something first: answer it above, or in Terminal, then send your message again", who)
			}
		}
	}
	return appDevHerdrDo(team, "agent", "prompt", agent.Pane, text)
}

// appDevChatMessage is the text a message gives the agent: what was
// written, and the paths of the files attached, saved in the box.
func appDevChatMessage(team, workspace string, body map[string]any) (string, error) {
	text, _ := body["text"].(string)
	text = strings.TrimSpace(text)
	files, err := appDevChatFiles(body)
	switch {
	case err != nil:
		return "", err
	case text == "" && len(files) == 0:
		return "", errors.New("nothing to send")
	case len(text) > appDevChatTextMax:
		return "", fmt.Errorf("the message is longer than %s", formatDataSize(appDevChatTextMax))
	}
	paths, err := appDevSaveChatFiles(team, workspace, files)
	if err != nil {
		return "", err
	}
	if len(paths) > 0 {
		text = strings.TrimSpace(text + "\n\nAttached, in the box: " + strings.Join(paths, " "))
	}
	return text, nil
}
