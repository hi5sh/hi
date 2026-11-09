package cli

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// The machine's owner is the first admin, or the one hi admin owner
// names; the org's owner where the org's admins are the machine's.
func TestAdminMachineOwner(t *testing.T) {
	setupAdminTest(t)
	if _, err := adminAdd("Sam", "U07SAM", "", "test"); err != nil {
		t.Fatal(err)
	}
	config, _ := loadAdminConfig()
	if owner := config.machineOwner(); owner != "Olga" {
		t.Fatalf("the first admin: %q", owner)
	}
	var out strings.Builder
	if err := adminOwnerCommand([]string{"Nobody"}, &out); err == nil {
		t.Fatal("someone who isn't an admin owns the machine")
	}
	if err := adminOwnerCommand([]string{"Sam"}, &out); err != nil {
		t.Fatal(err)
	}
	config, _ = loadAdminConfig()
	sam, _ := config.admin("Sam")
	olga, _ := config.admin("Olga")
	if config.machineOwner() != "Sam" || !sam.MachineOwner || olga.MachineOwner {
		t.Fatalf("handed on: %q %+v %+v", config.machineOwner(), sam, olga)
	}
	// Removing the owner hands it back to the first admin.
	if _, err := adminRemove("Sam", "test"); err != nil {
		t.Fatal(err)
	}
	config, _ = loadAdminConfig()
	if owner := config.machineOwner(); owner != "Olga" {
		t.Fatalf("after the owner went: %q", owner)
	}
	// One list with the org's: its owner.
	org := adminConfig{org: "/org", Admins: []adminPerson{
		{Name: "Ada", Machine: true, Org: true, Added: time.Now().Add(-time.Hour)},
		{Name: "Iman", Machine: true, Org: true, Owner: true, Added: time.Now()}}}
	if owner := org.machineOwner(); owner != "Iman" {
		t.Fatalf("the org's owner: %q", owner)
	}
}

func TestAdminTerminalInAnAppsBox(t *testing.T) {
	a := setupAdminTest(t)
	running, command, end := adminBoxRunning, adminTermCommand, adminTermEnd
	ended := ""
	adminBoxRunning = func(container string) bool {
		return container == "hi-box-app-payments" || container == appMemoryContainer("payments")
	}
	adminTermCommand = func(container, mark string) []string {
		return []string{"sh", "-c", "echo in " + container + "; read line; echo got $line"}
	}
	adminTermEnd = func(container, mark string) { ended = container }
	t.Cleanup(func() { adminBoxRunning, adminTermCommand, adminTermEnd = running, command, end })
	if _, err := adminAdd("Sam", "U07SAM", "", "test"); err != nil {
		t.Fatal(err)
	}
	olga, sam := a.signIn(t, "Olga"), a.signIn(t, "Sam")

	var view adminTermView
	if code := a.get(t, olga, "/api/app/terminal?name=payments", &view); code != http.StatusOK || !view.You || view.Owner != "Olga" ||
		len(view.Boxes) != 2 || view.Boxes[0].ID != "lead" || view.Boxes[1].ID != "memory" {
		t.Fatalf("the owner's view: %d %+v", code, view)
	}
	if code := a.get(t, sam, "/api/app/terminal?name=payments", &view); code != http.StatusOK || view.You || len(view.Boxes) != 2 {
		t.Fatalf("another admin's view: %d %+v", code, view)
	}

	address := "ws" + strings.TrimPrefix(a.server.URL, "http") + "/api/app/term?name=payments&box=lead&cols=80&rows=24"
	dial := func(cookie *http.Cookie, origin, url string) (*websocket.Conn, *http.Response, error) {
		header := http.Header{"Origin": {origin}}
		header.Set("Cookie", cookie.String())
		return websocket.DefaultDialer.Dial(url, header)
	}
	// Only the owner, only from the page, only a box that runs.
	if _, response, err := dial(sam, a.server.URL, address); err == nil || response == nil || response.StatusCode != http.StatusForbidden {
		t.Fatalf("another admin opened one: %v", err)
	}
	if _, response, err := dial(olga, "http://elsewhere.example", address); err == nil || response == nil || response.StatusCode != http.StatusForbidden {
		t.Fatalf("another site opened one: %v", err)
	}
	if _, response, err := dial(olga, a.server.URL, strings.Replace(address, "box=lead", "box=dev", 1)); err == nil || response == nil || response.StatusCode != http.StatusNotFound {
		t.Fatalf("a box that doesn't run: %v", err)
	}

	conn, _, err := dial(olga, a.server.URL, address)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	read := func(want string) {
		t.Helper()
		var seen string
		for !strings.Contains(seen, want) {
			_, data, err := conn.ReadMessage()
			if err != nil {
				t.Fatalf("waiting for %q, saw %q: %v", want, seen, err)
			}
			seen += string(data)
		}
	}
	read("in hi-box-app-payments")
	conn.WriteMessage(websocket.BinaryMessage, []byte("hello\r"))
	read("got hello")
	// The shell ends: the terminal closes, and what it left goes.
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			break
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		a.mu.Lock()
		said := strings.Join(*a.posts, "\n")
		a.mu.Unlock()
		if strings.Contains(said, "closed the terminal") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	a.mu.Lock()
	said := strings.Join(*a.posts, "\n")
	a.mu.Unlock()
	if !strings.Contains(said, "Olga opened a terminal in payments's lead's box, from the admin page") || !strings.Contains(said, "Olga closed the terminal in payments's lead's box") {
		t.Fatalf("the ops channel: %s", said)
	}
	if ended != "hi-box-app-payments" {
		t.Fatalf("what it left: %q", ended)
	}
	activity := adminActivities(10)
	if len(activity) < 2 || !strings.Contains(activity[0].What, "closed the terminal") || strings.Contains(activity[0].What, "hello") {
		t.Fatalf("Activity: %+v", activity)
	}
}

func TestAdminTerminalClosesWhenIdle(t *testing.T) {
	a := setupAdminTest(t)
	running, command, end, idle := adminBoxRunning, adminTermCommand, adminTermEnd, adminTermIdle
	adminBoxRunning = func(container string) bool { return container == "hi-box-app-payments" }
	adminTermCommand = func(container, mark string) []string { return []string{"sh", "-c", "echo ready; sleep 30"} }
	adminTermEnd = func(container, mark string) {}
	adminTermIdle = 300 * time.Millisecond
	t.Cleanup(func() { adminBoxRunning, adminTermCommand, adminTermEnd, adminTermIdle = running, command, end, idle })
	// Before those are put back: the terminals end, which outlive the test's
	// requests.
	t.Cleanup(func() { a.server.CloseClientConnections(); adminTermsOpen.Wait() })
	olga := a.signIn(t, "Olga")
	header := http.Header{"Origin": {a.server.URL}}
	header.Set("Cookie", olga.String())
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(a.server.URL, "http")+"/api/app/term?name=payments&box=lead", header)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		_, _, err := conn.ReadMessage()
		if err == nil {
			continue
		}
		if closed, ok := err.(*websocket.CloseError); !ok || !strings.Contains(closed.Text, "no key for") {
			t.Fatalf("closed with %v", err)
		}
		return
	}
}
