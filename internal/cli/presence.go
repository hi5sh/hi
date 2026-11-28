package cli

// Who else is on a page: the admin page's admins, or an app's dev page's
// owners and developers. Each open window holds a WebSocket to its page;
// the page's top bar shows a pill for each person, in a colour that's
// theirs while they're there, with how many windows they have open, and
// greyed after a few minutes without a key or a click in any of them. A
// window that's closed, or whose connection is cut, is gone at once, or
// at the latest when it stops answering the server's pings.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// presenceColors are clearly told apart on the pages' dark background, and
// from each other; the first free one is a newcomer's.
var presenceColors = []string{"#e5484d", "#3e9bff", "#46a758", "#f5a623", "#a66cff", "#ec5fb0", "#12b5a5", "#f76b15", "#c9d34a", "#8d8d8d"}

// presencePing is the server's ping to each window; a window that doesn't
// answer in presenceWait is gone. Tests make them short.
var (
	presencePing = 15 * time.Second
	presenceWait = 45 * time.Second
)

// presenceBoard is one page's windows.
type presenceBoard struct {
	mu     sync.Mutex
	next   int
	tabs   map[int]*presenceTab
	colors map[string]int // a person's colour, while they have a window here
	// record, when set, is told the time each person spends on the page
	// with a window in use (their face time), a stretch at a time; a
	// stretch can be negative, to take back time that was idle after all.
	record func(who string, d time.Duration)
	active map[string]*presenceStretch
	// quiet is how long the window that just went idle was without
	// input, by person: the time its stretch gives back if it ends.
	quiet map[string]time.Duration
}

// presenceStretch is a person's time in use, since a window of theirs
// became active; since is how far record has been told.
type presenceStretch struct{ start, since time.Time }

type presenceTab struct {
	who, name string
	idle      bool
	changed   chan struct{}
}

// presencePerson is one pill.
type presencePerson struct {
	Name  string `json:"name"`
	Color string `json:"color"`
	Tabs  int    `json:"tabs"`
	Idle  bool   `json:"idle"`
	You   bool   `json:"you,omitempty"`
}

func newPresenceBoard() *presenceBoard {
	return &presenceBoard{tabs: map[int]*presenceTab{}, colors: map[string]int{}, active: map[string]*presenceStretch{}, quiet: map[string]time.Duration{}}
}

// presenceTallyEvery is how often a stretch in use is told to record
// while it goes on, so a restart loses little of it.
var presenceTallyEvery = time.Minute

// tally tells record of each person's time in use: a person is in use
// while one of their windows is active. A stretch that ends as its last
// window goes idle gives back the time that window was without input.
// With all, every stretch is told; else only those not told for
// presenceTallyEvery. p.mu is held.
func (p *presenceBoard) tally(now time.Time, all bool) {
	quiet := p.quiet
	p.quiet = map[string]time.Duration{}
	if p.record == nil {
		return
	}
	using := map[string]bool{}
	for _, tab := range p.tabs {
		if !tab.idle {
			using[tab.who] = true
		}
	}
	for who, stretch := range p.active {
		if using[who] {
			if all || now.Sub(stretch.since) >= presenceTallyEvery {
				p.record(who, now.Sub(stretch.since))
				stretch.since = now
			}
			continue
		}
		// The stretch ended: its time, less the quiet at its end.
		back := min(quiet[who], now.Sub(stretch.start))
		if d := now.Sub(stretch.since) - back; d != 0 {
			p.record(who, d)
		}
		delete(p.active, who)
	}
	for who := range using {
		if p.active[who] == nil {
			p.active[who] = &presenceStretch{start: now, since: now}
		}
	}
}

// join adds a window, and gives its number and what tells it of changes.
func (p *presenceBoard) join(who, name string) (int, *presenceTab) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.next++
	tab := &presenceTab{who: who, name: name, changed: make(chan struct{}, 1)}
	p.tabs[p.next] = tab
	p.tally(time.Now(), true)
	if _, ok := p.colors[who]; !ok {
		used := map[int]bool{}
		for _, c := range p.colors {
			used[c] = true
		}
		color := len(p.colors) % len(presenceColors)
		for i := range presenceColors {
			if !used[i] {
				color = i
				break
			}
		}
		p.colors[who] = color
	}
	p.tell()
	return p.next, tab
}

// leave drops a window; a person with none left gives up their colour.
func (p *presenceBoard) leave(id int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	tab := p.tabs[id]
	if tab == nil {
		return
	}
	delete(p.tabs, id)
	p.tally(time.Now(), true)
	still := false
	for _, other := range p.tabs {
		still = still || other.who == tab.who
	}
	if !still {
		delete(p.colors, tab.who)
	}
	p.tell()
}

// setIdle marks a window idle or active; quiet is how long an idle one
// was without input.
func (p *presenceBoard) setIdle(id int, idle bool, quiet time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if tab := p.tabs[id]; tab != nil && tab.idle != idle {
		tab.idle = idle
		if idle {
			p.quiet[tab.who] = quiet
		}
		p.tally(time.Now(), true)
		p.tell()
	}
}

// tell wakes every window to send the pills again; p.mu is held.
func (p *presenceBoard) tell() {
	for _, tab := range p.tabs {
		select {
		case tab.changed <- struct{}{}:
		default:
		}
	}
}

// people are the pills as one person sees them: theirs first, then the
// others by name. A person is idle when every window of theirs is.
func (p *presenceBoard) people(you string) []presencePerson {
	p.mu.Lock()
	defer p.mu.Unlock()
	byWho := map[string]*presencePerson{}
	for _, tab := range p.tabs {
		person := byWho[tab.who]
		if person == nil {
			person = &presencePerson{Name: tab.name, Color: presenceColors[p.colors[tab.who]], Idle: true, You: tab.who == you}
			byWho[tab.who] = person
		}
		person.Tabs++
		person.Idle = person.Idle && tab.idle
	}
	people := make([]presencePerson, 0, len(byWho))
	for _, person := range byWho {
		people = append(people, *person)
	}
	sort.Slice(people, func(i, j int) bool {
		if people[i].You != people[j].You {
			return people[i].You
		}
		return people[i].Name < people[j].Name
	})
	return people
}

// serve is one window's connection: the pills whenever they change, a
// ping every presencePing, and the window's {"idle": true|false}.
func (p *presenceBoard) serve(w http.ResponseWriter, r *http.Request, who, name string, sameOrigin func(*http.Request) bool) {
	upgrader := websocket.Upgrader{CheckOrigin: sameOrigin}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "hi: who's here: %v\n", err)
		return
	}
	defer conn.Close()
	id, tab := p.join(who, name)
	defer p.leave(id)
	conn.SetReadLimit(1 << 10)
	conn.SetReadDeadline(time.Now().Add(presenceWait))
	conn.SetPongHandler(func(string) error { return conn.SetReadDeadline(time.Now().Add(presenceWait)) })
	gone := make(chan struct{})
	go func() {
		defer close(gone)
		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				return
			}
			conn.SetReadDeadline(time.Now().Add(presenceWait))
			var message struct {
				Idle  *bool `json:"idle"`
				Quiet int64 `json:"quiet"` // ms without input, when idle
			}
			if json.Unmarshal(data, &message) == nil && message.Idle != nil {
				quiet := min(max(time.Duration(message.Quiet)*time.Millisecond, 0), time.Hour)
				p.setIdle(id, *message.Idle, quiet)
			}
		}
	}()
	ping := time.NewTicker(presencePing)
	defer ping.Stop()
	for {
		select {
		case <-gone:
			return
		case <-ping.C:
			p.mu.Lock()
			p.tally(time.Now(), false)
			p.mu.Unlock()
			if conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(10*time.Second)) != nil {
				return
			}
		case <-tab.changed:
			conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if conn.WriteJSON(map[string]any{"people": p.people(who)}) != nil {
				return
			}
		}
	}
}

// adminPresence is the admin page's; devPresenceOf an app's dev page's.
var adminPresence = newPresenceBoard()

var devPresence = struct {
	sync.Mutex
	boards map[string]*presenceBoard
}{boards: map[string]*presenceBoard{}}

func devPresenceOf(team string) *presenceBoard {
	devPresence.Lock()
	defer devPresence.Unlock()
	if devPresence.boards[team] == nil {
		board := newPresenceBoard()
		board.record = func(who string, d time.Duration) { appFaceTimeAdd(team, who, d) }
		devPresence.boards[team] = board
	}
	return devPresence.boards[team]
}
