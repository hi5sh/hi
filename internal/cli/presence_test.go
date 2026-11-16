package cli

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// presenceWindow is one browser window on a test board.
type presenceWindow struct {
	t    *testing.T
	conn *websocket.Conn
}

func openPresence(t *testing.T, url, who string) *presenceWindow {
	t.Helper()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(url, "http")+"?who="+who, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return &presenceWindow{t: t, conn: conn}
}

// until reads the pills until they're what's wanted.
func (w *presenceWindow) until(want func([]presencePerson) bool) []presencePerson {
	w.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		w.conn.SetReadDeadline(deadline)
		var message struct {
			People []presencePerson `json:"people"`
		}
		if err := w.conn.ReadJSON(&message); err != nil {
			w.t.Fatal(err)
		}
		if want(message.People) {
			return message.People
		}
	}
	w.t.Fatal("not as wanted")
	return nil
}

func pill(people []presencePerson, name string) presencePerson {
	for _, p := range people {
		if p.Name == name {
			return p
		}
	}
	return presencePerson{}
}

// Who's on a page: a pill a person, theirs first, a colour each, the
// windows counted, idle when every window is, and a closed or cut window
// gone.
func TestPresence(t *testing.T) {
	savedPing, savedWait := presencePing, presenceWait
	t.Cleanup(func() { presencePing, presenceWait = savedPing, savedWait })
	presencePing, presenceWait = 50*time.Millisecond, 300*time.Millisecond
	board := newPresenceBoard()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		who := r.URL.Query().Get("who")
		board.serve(w, r, who, strings.ToUpper(who[:1])+who[1:], func(*http.Request) bool { return true })
	}))
	defer server.Close()

	olga := openPresence(t, server.URL, "olga")
	olga.until(func(p []presencePerson) bool { return len(p) == 1 && p[0].You && p[0].Tabs == 1 })
	olga2 := openPresence(t, server.URL, "olga")
	olga.until(func(p []presencePerson) bool { return len(p) == 1 && p[0].Tabs == 2 })
	sam := openPresence(t, server.URL, "sam")
	people := sam.until(func(p []presencePerson) bool { return len(p) == 2 })
	if !people[0].You || people[0].Name != "Sam" || pill(people, "Olga").Tabs != 2 || pill(people, "Olga").Color == pill(people, "Sam").Color {
		t.Fatalf("%+v", people)
	}
	// Idle: every window of a person's.
	olga.conn.WriteJSON(map[string]bool{"idle": true})
	if p := pill(sam.until(func(p []presencePerson) bool { return true }), "Olga"); p.Idle {
		t.Fatal("idle with a window still active")
	}
	olga2.conn.WriteJSON(map[string]bool{"idle": true})
	sam.until(func(p []presencePerson) bool { return pill(p, "Olga").Idle })
	olga.conn.WriteJSON(map[string]bool{"idle": false})
	sam.until(func(p []presencePerson) bool { return !pill(p, "Olga").Idle })
	// A window closed: the count goes down; the last one: the pill goes.
	olga2.conn.Close()
	sam.until(func(p []presencePerson) bool { return pill(p, "Olga").Tabs == 1 })
	olga.conn.Close()
	sam.until(func(p []presencePerson) bool { return len(p) == 1 })
	// A cut connection, one that stops answering pings: gone too.
	ken := openPresence(t, server.URL, "ken")
	sam.until(func(p []presencePerson) bool { return len(p) == 2 })
	ken.conn.SetPingHandler(func(string) error { return nil }) // no pong
	ken.conn.UnderlyingConn().(*net.TCPConn).CloseRead()
	sam.until(func(p []presencePerson) bool { return len(p) == 1 })
	// A colour is free again once its person has gone.
	if len(board.colors) != 1 {
		t.Fatal(board.colors)
	}
}

// Only the page's own people: an admin signed in, or an app's owner or
// developer on its dev page.
func TestPresenceSignedIn(t *testing.T) {
	a := setupAdminTest(t)
	request, _ := http.NewRequest("GET", a.server.URL+"/api/presence", nil)
	if response, err := http.DefaultClient.Do(request); err != nil || response.StatusCode == http.StatusSwitchingProtocols || response.StatusCode == http.StatusOK {
		t.Fatal("the admin page's presence without signing in:", err)
	}
	b, _ := appTestDevPage(t)
	if code, _ := appTestDevPageCall(t, b.devPageHandler(), appTestAskerKey, "GET", "api/presence", nil); code == http.StatusOK || code == http.StatusSwitchingProtocols {
		t.Fatal("someone who isn't a developer sees who's on the dev page:", code)
	}
}
