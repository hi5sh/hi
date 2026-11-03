package cli

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

const (
	appTestOwnerKey = "owner-key-0123456789abcdef"
	appTestAskerKey = "asker-key-0123456789abcdef"
)

// appTestDevPage gives the owner and an asker keys, and fakes herdr in
// the box; it returns the commands the box was asked to run.
func appTestDevPage(t *testing.T) (*appTestBroker, *[]string) {
	t.Helper()
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	config, _ := loadTeam("payments")
	saveTeam(config)
	ends := time.Now().Add(time.Hour)
	saveTeamPeople("payments", appPeopleFile{Keys: []appPersonKey{
		{Key: appTestOwnerKey, User: "U07OWNER1", Name: "Olga", Ends: ends},
		{Key: appTestAskerKey, User: "U07ASKER1", Name: "Ari", Ends: ends},
	}})
	var mu sync.Mutex
	var ran []string
	previous := appDevBoxExec
	appDevBoxExec = func(team string, command ...string) (string, error) {
		mu.Lock()
		ran = append(ran, strings.Join(command, " "))
		mu.Unlock()
		switch strings.Join(command[:min(3, len(command))], " ") {
		case "herdr workspace list":
			return `{"result":{"workspaces":[{"workspace_id":"w1","label":"repo","agent_status":"unknown","worktree":{"checkout_path":"/box/repo"}},` +
				`{"workspace_id":"w2","label":"1-chart","agent_status":"working","worktree":{"checkout_path":"/box/home/.herdr/worktrees/repo/1-chart"}}]}}`, nil
		case "herdr agent list":
			return `{"result":{"agents":[{"agent":"claude","agent_status":"working","workspace_id":"w2","terminal_title_stripped":"Fixing the chart"}]}}`, nil
		case "herdr worktree create":
			return `{"result":{"root_pane":{"pane_id":"w3:p1"}}}`, nil
		case "herdr pane run":
			return "", nil // it says nothing when it works
		}
		return `{"result":{}}`, nil
	}
	t.Cleanup(func() { appDevBoxExec = previous })
	return b, &ran
}

func appTestDevPageCall(t *testing.T, handler http.Handler, key, method, path string, body any) (int, string) {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		data, _ := json.Marshal(body)
		reader = bytes.NewReader(data)
	} else {
		reader = bytes.NewReader(nil)
	}
	request := httptest.NewRequest(method, appDevPagePrefix+path, reader)
	if key != "" {
		request.Header.Set(netExposeUserHeader, key)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder.Code, recorder.Body.String()
}

// A picture pasted into the terminal goes into the box, and the page gets
// its path to paste.
func TestTeamDevTermFiles(t *testing.T) {
	b, _ := appTestDevPage(t)
	handler := b.devPageHandler()
	picture := base64.StdEncoding.EncodeToString([]byte("\x89PNG"))
	code, body := appTestDevPageCall(t, handler, appTestOwnerKey, "POST", "api/term/files", map[string]any{"files": []map[string]string{{"name": "pasted-1a2b3c.png", "data": picture}}})
	var result struct{ Paths []string }
	json.Unmarshal([]byte(body), &result)
	if code != 200 || len(result.Paths) != 1 || result.Paths[0] != "/box/home/hi/chat/terminal/pasted-1a2b3c.png" {
		t.Fatalf("%d %s", code, body)
	}
	if data, _ := os.ReadFile(filepath.Join(appDevResultsDir("payments", "chat", "terminal"), "pasted-1a2b3c.png")); string(data) != "\x89PNG" {
		t.Fatalf("%q", data)
	}
	for _, bad := range []map[string]any{{}, {"files": []map[string]string{{"name": "x.png", "data": "not base64!"}}}} {
		if code, _ := appTestDevPageCall(t, handler, appTestOwnerKey, "POST", "api/term/files", bad); code == 200 {
			t.Errorf("%v was taken", bad)
		}
	}
	if code, _ := appTestDevPageCall(t, handler, appTestAskerKey, "POST", "api/term/files", map[string]any{"files": []map[string]string{{"name": "x.png", "data": picture}}}); code == 200 {
		t.Fatal("someone who isn't a builder put a file in the box")
	}
}

// Only owners get past the page; the page itself and its scripts load for
// anyone with the address's key, and say so.
func TestTeamDevPageOwnersOnly(t *testing.T) {
	b, _ := appTestDevPage(t)
	b.running = func(string) bool { return true }
	handler := b.devPageHandler()
	if code, body := appTestDevPageCall(t, handler, "", "GET", "", nil); code != 200 || !strings.Contains(body, "xterm.js") {
		t.Fatalf("the page: %d", code)
	}
	for _, key := range []string{"", appTestAskerKey, "unknown-key-0123456789abc"} {
		if code, body := appTestDevPageCall(t, handler, key, "GET", "api/state", nil); code != http.StatusUnauthorized || !strings.Contains(body, "owners") && !strings.Contains(body, "ended") {
			t.Errorf("%q: %d %s", key, code, body)
		}
	}
	saveTeamSecrets("payments", appSecrets{Env: map[string]string{"QUOTES_KEY": "s3cret"}})
	code, body := appTestDevPageCall(t, handler, appTestOwnerKey, "GET", "api/state", nil)
	if code != 200 || strings.Contains(body, "s3cret") {
		t.Fatalf("%d %s", code, body)
	}
	var view appDevPageView
	json.Unmarshal([]byte(body), &view)
	if view.You != "Olga" || view.Box != "running" || len(view.Workspaces) != 2 || view.Workspaces[1].Branch != "1-chart" ||
		view.Workspaces[1].Agent != "claude" || view.Workspaces[1].Title != "Fixing the chart" || len(view.Settings) != 1 {
		t.Fatalf("%+v", view)
	}
}

// An owner files a ticket from the page, with a file; it is in the list
// at once, and says who filed it.
func TestTeamDevPageAddTicket(t *testing.T) {
	b, _ := appTestDevPage(t)
	handler := b.devPageHandler()
	appDevPageTickets("payments", true) // the list, cached empty
	if code, _ := appTestDevPageCall(t, handler, appTestOwnerKey, "POST", "api/ticket", map[string]any{"title": " "}); code == 200 {
		t.Fatal("a task without a title")
	}
	code, body := appTestDevPageCall(t, handler, appTestOwnerKey, "POST", "api/ticket", map[string]any{
		"title": "Show the volume", "body": "Under the chart.", "files": []appAttachment{{Name: "sketch.png", Data: []byte("png")}}})
	if code != 200 || !strings.Contains(body, `"number":1`) {
		t.Fatalf("%d %s", code, body)
	}
	list, _ := appDevPageTickets("payments", false)
	if len(list.Tickets) != 1 || list.Tickets[0].Title != "Show the volume" {
		t.Fatalf("%+v", list.Tickets)
	}
	shown, _ := appTicketDo("payments", appTicketRequest{Action: "show", Number: 1})
	if !strings.Contains(shown.Ticket.Body, "Filed by Olga on the dev page.") || len(shown.Ticket.Files) != 1 {
		t.Fatalf("%+v", shown.Ticket)
	}
	// Open it: the ticket, its files, and a note with another.
	code, body = appTestDevPageCall(t, handler, appTestOwnerKey, "GET", "api/ticket?number=1", nil)
	if code != 200 || !strings.Contains(body, "Under the chart.") || !strings.Contains(body, "sketch.png") {
		t.Fatalf("%d %s", code, body)
	}
	if code, body := appTestDevPageCall(t, handler, appTestOwnerKey, "POST", "api/ticket/note", map[string]any{"number": 1, "body": "Also the 1Y view.",
		"files": []appAttachment{{Name: "page.html", Data: []byte("<script>alert(1)</script>")}}}); code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	if code, _ := appTestDevPageCall(t, handler, appTestOwnerKey, "POST", "api/ticket/note", map[string]any{"number": 1, "body": " "}); code == 200 {
		t.Fatal("an empty note")
	}
	shown, _ = appTicketDo("payments", appTicketRequest{Action: "show", Number: 1})
	if last := shown.Ticket.Comments[len(shown.Ticket.Comments)-1]; !strings.Contains(last.Body, "Also the 1Y view.") || !strings.Contains(last.Body, "From Olga on the dev page.") || len(shown.Ticket.Files) != 2 {
		t.Fatalf("%+v", shown.Ticket)
	}
	request := httptest.NewRequest("GET", appDevPagePrefix+"api/ticket/file?number=1&name=page.html", nil)
	request.Header.Set(netExposeUserHeader, appTestOwnerKey)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != 200 || recorder.Header().Get("Content-Type") != "application/octet-stream" || !strings.HasPrefix(recorder.Header().Get("Content-Disposition"), "attachment") ||
		!strings.Contains(recorder.Header().Get("Content-Security-Policy"), "sandbox") {
		t.Fatalf("an attached page: %d %v", recorder.Code, recorder.Header())
	}
	// Edit the ticket's title and text, and the note; an empty title or
	// comment is refused.
	if code, body := appTestDevPageCall(t, handler, appTestOwnerKey, "POST", "api/ticket/edit", map[string]any{"number": 1, "title": "Show the volume, top 20", "body": "Under the chart, top 20."}); code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	for _, bad := range []map[string]any{{"number": 1, "title": " ", "body": "x"}, {"number": 1, "comment": shown.Ticket.Comments[len(shown.Ticket.Comments)-1].ID, "body": " "}} {
		if code, _ := appTestDevPageCall(t, handler, appTestOwnerKey, "POST", "api/ticket/edit", bad); code == 200 {
			t.Fatalf("%v was saved", bad)
		}
	}
	edited := shown.Ticket.Comments[len(shown.Ticket.Comments)-1].ID
	if code, body := appTestDevPageCall(t, handler, appTestOwnerKey, "POST", "api/ticket/edit", map[string]any{"number": 1, "comment": edited, "body": "Also the 5Y view."}); code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	shown, _ = appTicketDo("payments", appTicketRequest{Action: "show", Number: 1})
	if last := shown.Ticket.Comments[len(shown.Ticket.Comments)-1]; shown.Ticket.Title != "Show the volume, top 20" || shown.Ticket.Body != "Under the chart, top 20." || shown.Ticket.Edited.IsZero() ||
		!strings.HasPrefix(last.Body, "Also the 5Y view.\n\nAttached: page.html") || last.ID != edited || last.Edited.IsZero() {
		t.Fatalf("%+v", shown.Ticket)
	}
	// Delete the note: its file goes with it; the ticket's first file stays.
	note := shown.Ticket.Comments[len(shown.Ticket.Comments)-1]
	if note.ID == "" {
		t.Fatalf("a comment without an ID: %+v", note)
	}
	if code, body := appTestDevPageCall(t, handler, appTestOwnerKey, "POST", "api/ticket/delete", map[string]any{"number": 1, "comment": note.ID}); code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	shown, _ = appTicketDo("payments", appTicketRequest{Action: "show", Number: 1})
	for _, comment := range shown.Ticket.Comments {
		if strings.Contains(comment.Body, "Also the 1Y view.") {
			t.Fatal("the note is still there")
		}
	}
	if files := appTicketFileNames("payments", 1); len(files) != 1 || files[0] != "sketch.png" {
		t.Fatal(files)
	}
	if code, _ := appTestDevPageCall(t, handler, appTestOwnerKey, "POST", "api/ticket/delete", map[string]any{"number": 1, "comment": note.ID}); code == 200 {
		t.Fatal("deleted a comment twice")
	}
	if code, _ := appTestDevPageCall(t, handler, appTestOwnerKey, "POST", "api/ticket/delete", map[string]any{"number": 1, "name": "../1.json"}); code == 200 {
		t.Fatal("deleted a file that isn't the task's")
	}
	if code, _ := appTestDevPageCall(t, handler, appTestOwnerKey, "POST", "api/ticket/delete", map[string]any{"number": 1, "name": "sketch.png"}); code != 200 || len(appTicketFileNames("payments", 1)) != 0 {
		t.Fatal("the file is still there")
	}
	if got := appAttachedNames("Hi.\n\nAttached: a b.png (12 KB), c.pdf (3 MB). hi keeps them with the team."); strings.Join(got, "|") != "a b.png|c.pdf" {
		t.Fatal(got)
	}
	for _, name := range []string{"../../team.json", "nothing.png", "page.html"} {
		if code, _ := appTestDevPageCall(t, handler, appTestOwnerKey, "GET", "api/ticket/file?number=1&name="+url.QueryEscape(name), nil); code != http.StatusNotFound {
			t.Fatalf("%s: %d", name, code)
		}
	}
	if code, _ := appTestDevPageCall(t, handler, appTestAskerKey, "POST", "api/ticket", map[string]any{"title": "x"}); code != http.StatusUnauthorized {
		t.Fatal("someone who isn't an owner filed a task")
	}
}

// Start: the ticket's branch from dev, its worktree and workspace, and the
// agent told to read it.
func TestTeamDevPageStart(t *testing.T) {
	_, ran := appTestDevPage(t)
	appTicketDo("payments", appTicketRequest{Action: "add", Title: "Chart misses the last day!", Author: "Olga"})
	config, _ := loadTeam("payments")
	if _, err := appDevStart(config, 1, "gemini", "Olga"); err == nil {
		t.Fatal("an unknown agent")
	}
	branch, err := appDevStart(config, 1, "claude", "Olga")
	if err != nil || branch != "1-chart-misses-the-last-day" {
		t.Fatalf("%q %v", branch, err)
	}
	all := strings.Join(*ran, "\n")
	for _, want := range []string{"git -C /box/repo fetch -q origin", "herdr worktree create --cwd /box/repo --branch 1-chart-misses-the-last-day --base origin/main --focus",
		"herdr pane run w3:p1 claude 'Work on task #1: Chart misses the last day!. Read it first with hi dev task show 1"} {
		if !strings.Contains(all, want) {
			t.Errorf("no %q in\n%s", want, all)
		}
	}
	if by := appDevStarters("payments")["1-chart-misses-the-last-day"]; by != "Olga" {
		t.Fatalf("started by %q", by)
	}
	if _, err := appDevStart(config, 9, "", "Olga"); err == nil {
		t.Fatal("a task that isn't there")
	}
}

// An owner's settings and release from the page: values never come back,
// and the click is the release's yes, with the owner's name.
func TestTeamDevPageSettingsAndRelease(t *testing.T) {
	b, _ := appTestDevPage(t)
	handler := b.devPageHandler()
	saveTeamSecrets("payments", appSecrets{})
	if code, body := appTestDevPageCall(t, handler, appTestOwnerKey, "POST", "api/settings", appDevPageSetting{Key: "QUOTES_KEY", Value: "live", Slot: appSlotProduction}); code != 200 {
		t.Fatal(code, body)
	}
	// A file, from the page: the box gets it, and the list says so.
	if code, body := appTestDevPageCall(t, handler, appTestOwnerKey, "POST", "api/settings", appDevPageSetting{Key: "DB_CERT", File: base64.StdEncoding.EncodeToString([]byte("-----BEGIN CERTIFICATE-----\n"))}); code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	if secrets, _ := loadTeamSecrets("payments"); !strings.Contains(strings.Join(appEnvNames(secrets), ","), "DB_CERT (a file)") {
		t.Fatal(appEnvNames(secrets))
	}
	if code, _ := appTestDevPageCall(t, handler, appTestOwnerKey, "POST", "api/settings", appDevPageSetting{Key: "DB_CERT", Value: "x"}); code != http.StatusBadRequest {
		t.Fatal("a file became a value")
	}
	if code, _ := appTestDevPageCall(t, handler, appTestOwnerKey, "POST", "api/settings", appDevPageSetting{Key: "DB_CERT", Unset: true}); code != 200 {
		t.Fatal("removing a file")
	}
	if code, _ := appTestDevPageCall(t, handler, appTestOwnerKey, "POST", "api/settings", appDevPageSetting{Key: "PORT", Value: "1"}); code != http.StatusBadRequest {
		t.Fatal("a reserved name was set")
	}
	if code, _ := appTestDevPageCall(t, handler, appTestAskerKey, "POST", "api/settings", appDevPageSetting{Key: "X", Value: "1"}); code != http.StatusUnauthorized {
		t.Fatal("an asker set a setting")
	}
	if secrets, _ := loadTeamSecrets("payments"); secrets.EnvProduction["QUOTES_KEY"] != "live" {
		t.Fatalf("%+v", secrets)
	}

	os.WriteFile(b.repo+"/Makefile", []byte("run:\n\t./serve\n"), 0o644)
	appGit(b.repo, "add", "-A")
	appGit(b.repo, "commit", "-q", "-m", "make run")
	appTestMerge(t, b, "Suppliers")
	if code, _ := appTestDevPageCall(t, handler, appTestAskerKey, "POST", "api/release", map[string]string{"notes": "Suppliers"}); code != http.StatusUnauthorized {
		t.Fatal("an asker released")
	}
	code, body := appTestDevPageCall(t, handler, appTestOwnerKey, "POST", "api/release", map[string]string{"notes": "- Suppliers"})
	if code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	versions, _ := loadTeamVersions("payments")
	if versions.Production != 1 || versions.Releases[0].Approval == nil || versions.Releases[0].Approval.Name != "Olga on the dev page" {
		t.Fatalf("%+v", versions)
	}
}

// The terminal: the page's keys reach the process and its output comes
// back; a WebSocket from another site is refused.
func TestTeamDevPageTerminal(t *testing.T) {
	b, _ := appTestDevPage(t)
	previous := appDevTermCommand
	appDevTermCommand = func(string) []string {
		return []string{"sh", "-c", "stty -echo; read line; echo \"got $line\"; sleep 1"}
	}
	t.Cleanup(func() { appDevTermCommand = previous })
	server := httptest.NewServer(b.devPageHandler())
	t.Cleanup(server.Close)
	address := "ws" + strings.TrimPrefix(server.URL, "http") + appDevPagePrefix + "api/term?cols=100&rows=30"

	header := http.Header{netExposeUserHeader: {appTestOwnerKey}, "Origin": {"https://evil.example"}}
	if _, response, err := websocket.DefaultDialer.Dial(address, header); err == nil || response == nil || response.StatusCode != http.StatusForbidden {
		t.Fatal("a WebSocket from another site:", err)
	}
	// The page's address as hi exposed it, whatever host the proxy says.
	state, _ := json.Marshal(appDevPageState{URL: "https://team-payments-dev.example.net/?hi_key=k", Expose: "team-payments-dev"})
	os.MkdirAll(appDir("payments")+"/run", 0o700)
	os.WriteFile(appDevPageStatePath("payments"), state, 0o600)
	header.Set("Origin", "https://team-payments-dev.example.net")
	conn, _, err := websocket.DefaultDialer.Dial(address, header)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.WriteMessage(websocket.TextMessage, []byte(`{"cols":80,"rows":24}`))
	conn.WriteMessage(websocket.BinaryMessage, []byte("hello\r"))
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	var got strings.Builder
	for !strings.Contains(got.String(), "got hello") {
		_, data, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("%v; read %q", err, got.String())
		}
		got.Write(data)
	}
}

// Owners' DMs get the dev page's link, with their key; others' don't.
func TestTeamDevPageLink(t *testing.T) {
	b, _ := appTestDevPage(t)
	state := appDevPageState{URL: "https://team-payments-dev.example.net/?hi_key=k", Expose: "team-payments-dev", Expires: time.Now().Add(time.Hour)}
	data, _ := json.Marshal(state)
	os.MkdirAll(appDir("payments")+"/run", 0o700)
	os.WriteFile(appDevPageStatePath("payments"), data, 0o600)
	b.exposed = func(name string) (netExposeState, bool) { return netExposeState{}, name == "team-payments-dev" }
	owner := b.appLinkText(appPersonKey{Key: appTestOwnerKey, User: "U07OWNER1", Name: "Olga", Ends: time.Now().Add(time.Hour)})
	if !strings.Contains(owner, "https://team-payments-dev.example.net/?hi_key=k&hi_user="+appTestOwnerKey) {
		t.Fatal(owner)
	}
	if asker := b.appLinkText(appPersonKey{Key: appTestAskerKey, User: "U07ASKER1", Name: "Ari", Ends: time.Now().Add(time.Hour)}); strings.Contains(asker, "Dev page") {
		t.Fatal(asker)
	}
	b.channel = &fakeTeamChannel{}
	if _, err := b.webLink("U07ASKER1", true); err == nil || !strings.Contains(err.Error(), "owners") {
		t.Fatal("an asker got the dev page:", err)
	}
}

// What took an owner's yes in Slack is a click on the page, logged with
// the owner's name: rollback, the helper, incidents, and job rules.
func TestTeamDevPageOwnerActions(t *testing.T) {
	b, _ := appTestDevPage(t)
	handler := b.devPageHandler()
	saveTeamSecrets("payments", appSecrets{})
	call := func(key, path string, body any) (int, string) {
		return appTestDevPageCall(t, handler, key, "POST", path, body)
	}
	os.WriteFile(b.repo+"/Makefile", []byte("run:\n\t./serve\n"), 0o644)
	appGit(b.repo, "add", "-A")
	appGit(b.repo, "commit", "-q", "-m", "make run")
	appTestMerge(t, b, "Suppliers")
	if code, body := call(appTestOwnerKey, "api/release", map[string]string{"notes": "Suppliers"}); code != 200 {
		t.Fatal(code, body)
	}
	appTestMerge(t, b, "Search")
	if code, body := call(appTestOwnerKey, "api/release", map[string]string{"notes": "Search"}); code != 200 {
		t.Fatal(code, body)
	}

	// Rollback: an asker can't; an owner can, and it's in the record.
	if code, _ := call(appTestAskerKey, "api/rollback", map[string]int{"version": 1}); code != http.StatusUnauthorized {
		t.Fatal("an asker rolled back")
	}
	if code, body := call(appTestOwnerKey, "api/rollback", map[string]int{"version": 1}); code != 200 {
		t.Fatal(code, body)
	}
	versions, _ := loadTeamVersions("payments")
	last := versions.History[len(versions.History)-1]
	if versions.Production != 1 || last.Approval == nil || last.Approval.Name != "Olga on the dev page" {
		t.Fatalf("%+v", versions)
	}
	_, body := appTestDevPageCall(t, handler, appTestOwnerKey, "GET", "api/state", nil)
	var view appDevPageView
	json.Unmarshal([]byte(body), &view)
	if len(view.Releases) != 2 || view.Releases[0].Version != 2 || view.Releases[1].By != "Olga on the dev page" {
		t.Fatalf("%+v", view.Releases)
	}

	// The helper, with no Slack yes.
	if code, body := call(appTestOwnerKey, "api/helper", map[string]any{"slot": "production", "on": true}); code != 200 {
		t.Fatal(code, body)
	}
	if config, _ := loadTeam("payments"); !appHelperOn(config, appSlotProduction) {
		t.Fatal("the helper isn't on in production")
	}

	// An incident closed, and a rule added and removed.
	os.MkdirAll(appIncidentsDir("payments"), 0o700)
	saveTeamIncident("payments", appIncident{ID: "i-1", Kind: "job", Job: "import", Title: "The import failed", Status: "open", Opened: time.Now()})
	if code, body := call(appTestOwnerKey, "api/rule", map[string]string{"job": "import", "rule": "Skip a day the market was closed", "incident": "i-1"}); code != 200 {
		t.Fatal(code, body)
	}
	if rules := loadTeamRules("payments"); len(rules) != 1 || rules[0].Approval.Name != "Olga on the dev page" {
		t.Fatalf("%+v", rules)
	}
	if code, body := call(appTestOwnerKey, "api/incident", map[string]string{"id": "i-1", "action": "close", "because": "The rule covers it"}); code != 200 {
		t.Fatal(code, body)
	}
	if incident, _ := loadTeamIncident("payments", "i-1"); incident.Status != "closed" || incident.Events[len(incident.Events)-1].Who != "Olga on the dev page" {
		t.Fatalf("%+v", incident)
	}
	if code, body := call(appTestOwnerKey, "api/rule", map[string]any{"job": "import", "rule": "Skip a day the market was closed", "remove": true}); code != 200 {
		t.Fatal(code, body)
	}
	if rules := loadTeamRules("payments"); len(rules) != 0 {
		t.Fatalf("%+v", rules)
	}
}

// Finish ends a shipped piece of work: worktree, workspace, branch, and
// its ticket; work dev hasn't stays unless it's discarded.
func TestTeamDevFinish(t *testing.T) {
	b, ran := appTestDevPage(t)
	saveTeamSecrets("payments", appSecrets{})
	appTicketDo("payments", appTicketRequest{Action: "add", Title: "Chart", Author: "Olga"})
	saveTeamTask("payments", appTask{ID: "t-4", Kind: "task", Role: appDevRole, Status: "merged", Branch: "box/1-chart", Title: "Chart"})
	state := "0\t0"
	previous := appDevBoxExec
	appDevBoxExec = func(team string, command ...string) (string, error) {
		*ran = append(*ran, strings.Join(command, " "))
		if command[0] == "sh" {
			return "/box/home/.herdr/worktrees/repo/1-chart\t" + state + "\n", nil
		}
		return previous(team, command...)
	}
	config, _ := loadTeam("payments")
	if _, err := appDevFinish(config, "w1", "Olga", false); err == nil || !strings.Contains(err.Error(), "own clone") {
		t.Fatal("finished the clone itself:", err)
	}
	state = "2\t1"
	if _, err := appDevFinish(config, "w2", "Olga", false); err == nil || !strings.Contains(err.Error(), "2 commits that dev hasn't and changes not committed") {
		t.Fatal(err)
	}
	// Ship, beside the work: nothing committed is nothing to ship; with
	// commits, it's hi dev ship's (the test box has no such branch).
	handler := b.devPageHandler()
	state = "0\t1"
	if code, body := appTestDevPageCall(t, handler, appTestOwnerKey, "POST", "api/ship", map[string]any{"workspace": "w2"}); code != 400 || !strings.Contains(body, "aren't committed yet") {
		t.Fatalf("%d %s", code, body)
	}
	state = "1\t0"
	if code, body := appTestDevPageCall(t, handler, appTestOwnerKey, "POST", "api/ship", map[string]any{"workspace": "w2"}); code != 502 || !strings.Contains(body, "dev box") {
		t.Fatalf("%d %s", code, body)
	}
	if code, _ := appTestDevPageCall(t, handler, appTestOwnerKey, "POST", "api/ship", map[string]any{"workspace": "w1"}); code != 400 {
		t.Fatal("shipped the clone itself")
	}
	state = "0\t0"
	if list, _ := appDevPageTickets("payments", true); len(list.Tickets) != 1 {
		t.Fatalf("%+v", list.Tickets)
	}
	text, err := appDevFinish(config, "1-chart", "Olga on the dev page", false)
	if err != nil || !strings.Contains(text, "Task #1 is closed: shipped as t-4") {
		t.Fatal(text, err)
	}
	all := strings.Join(*ran, "\n")
	for _, want := range []string{"herdr worktree remove --workspace w2", "git -C /box/repo branch -D 1-chart"} {
		if !strings.Contains(all, want) {
			t.Errorf("no %q in\n%s", want, all)
		}
	}
	if ticket, _ := appTicketDo("payments", appTicketRequest{Action: "show", Number: 1}); ticket.Ticket.State != "closed" || !strings.Contains(ticket.Ticket.Comments[0].Body, "Shipped into dev as t-4; finished by Olga on the dev page") {
		t.Fatalf("%+v", ticket.Ticket)
	}
	// The page's list, cached a moment ago, hasn't the closed ticket.
	if list, _ := appDevPageTickets("payments", false); len(list.Tickets) != 0 {
		t.Fatalf("the closed task is still listed: %+v", list.Tickets)
	}
	// Discarded: forced, and the ticket stays open.
	appTicketDo("payments", appTicketRequest{Action: "add", Title: "Other", Author: "Olga"})
	state = "3\t0"
	*ran = nil
	if text, err := appDevFinish(config, "w2", "Olga", true); err != nil || !strings.Contains(text, "discarded, with 3 commits") {
		t.Fatal(text, err)
	}
	if !strings.Contains(strings.Join(*ran, "\n"), "herdr worktree remove --workspace w2 --force") {
		t.Fatal(*ran)
	}
}

// The File view: the clone's files to list, preview, download, and upload
// into; nothing outside it, through .. or a link.
func TestTeamDevFiles(t *testing.T) {
	b, _ := appTestDevPage(t)
	handler := b.devPageHandler()
	repo := filepath.Join(appDevDir("payments"), "repo")
	os.MkdirAll(filepath.Join(repo, "docs"), 0o755)
	os.MkdirAll(filepath.Join(repo, ".git"), 0o755)
	os.WriteFile(filepath.Join(repo, "README.md"), []byte("# Payments\n"), 0o644)
	os.WriteFile(filepath.Join(repo, "docs", "page.html"), []byte("<script>alert(1)</script>"), 0o644)
	os.WriteFile(filepath.Join(repo, "app.bin"), []byte{0, 1, 2, 3}, 0o644)
	os.WriteFile(filepath.Join(appDevDir("payments"), "secret.txt"), []byte("not the project's"), 0o600)
	os.Symlink(filepath.Join(appDevDir("payments"), "secret.txt"), filepath.Join(repo, "escape.txt"))
	get := func(path string) *httptest.ResponseRecorder {
		request := httptest.NewRequest("GET", appDevPagePrefix+path, nil)
		request.Header.Set(netExposeUserHeader, appTestOwnerKey)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		return recorder
	}
	if body := get("api/files/roots").Body.String(); !strings.Contains(body, `"id":"repo"`) {
		t.Fatal(body)
	}
	list := get("api/files/list?root=repo&path=").Body.String()
	if !strings.Contains(list, `"name":"docs","dir":true`) || !strings.Contains(list, "README.md") || strings.Contains(list, ".git") || strings.Index(list, "docs") > strings.Index(list, "README.md") {
		t.Fatal(list)
	}
	if got := get("api/files/raw?root=repo&path=README.md"); got.Code != 200 || got.Body.String() != "# Payments\n" || !strings.HasPrefix(got.Header().Get("Content-Type"), "text/plain") {
		t.Fatalf("%d %v %s", got.Code, got.Header(), got.Body)
	}
	if got := get("api/files/raw?root=repo&path=docs/page.html"); !strings.HasPrefix(got.Header().Get("Content-Type"), "text/plain") || !strings.Contains(got.Header().Get("Content-Security-Policy"), "sandbox") {
		t.Fatalf("an HTML file runs: %v", got.Header())
	}
	if got := get("api/files/raw?root=repo&path=app.bin"); got.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("a binary previews: %d", got.Code)
	}
	if got := get("api/files/raw?root=repo&path=app.bin&download=1"); got.Code != 200 || !strings.HasPrefix(got.Header().Get("Content-Disposition"), "attachment") || got.Body.Len() != 4 {
		t.Fatalf("%d %v", got.Code, got.Header())
	}
	for _, path := range []string{"../secret.txt", "escape.txt", "..%2Fsecret.txt", "docs/../../secret.txt"} {
		if got := get("api/files/raw?root=repo&download=1&path=" + path); got.Code == 200 || strings.Contains(got.Body.String(), "not the project's") {
			t.Fatalf("%s: %d %s", path, got.Code, got.Body)
		}
	}
	if got := get("api/files/list?root=elsewhere"); got.Code != 404 {
		t.Fatal("a tree that isn't one")
	}
	upload := func(query, name, data string) *httptest.ResponseRecorder {
		var body bytes.Buffer
		form := multipart.NewWriter(&body)
		part, _ := form.CreateFormFile("file", name)
		part.Write([]byte(data))
		form.Close()
		request := httptest.NewRequest("POST", appDevPagePrefix+"api/files/upload?root=repo&"+query, &body)
		request.Header.Set("Content-Type", form.FormDataContentType())
		request.Header.Set(netExposeUserHeader, appTestOwnerKey)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		return recorder
	}
	if got := upload("path=docs", "../../sketch.png", "png"); got.Code != 200 || !fileExists(filepath.Join(repo, "docs", "sketch.png")) {
		t.Fatalf("%d %s", got.Code, got.Body)
	}
	if got := upload("path=docs", "sketch.png", "new"); got.Code != http.StatusConflict || !strings.Contains(got.Body.String(), `"exists":"sketch.png"`) {
		t.Fatalf("%d %s", got.Code, got.Body)
	}
	if got := upload("path=docs&overwrite=1", "sketch.png", "new"); got.Code != 200 {
		t.Fatalf("%d %s", got.Code, got.Body)
	}
	if data, _ := os.ReadFile(filepath.Join(repo, "docs", "sketch.png")); string(data) != "new" {
		t.Fatal(string(data))
	}
	if got := upload("path=escape.txt&overwrite=1", "x.txt", "x"); got.Code == 200 {
		t.Fatal("uploaded through a link out of the tree")
	}
	if data, _ := os.ReadFile(filepath.Join(appDevDir("payments"), "secret.txt")); string(data) != "not the project's" {
		t.Fatal("the file outside changed")
	}
	request := httptest.NewRequest("GET", appDevPagePrefix+"api/files/list?root=repo", nil)
	request.Header.Set(netExposeUserHeader, appTestAskerKey)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatal("someone who isn't an owner listed the files")
	}
}

// The chat: a Claude Code session read from its transcript, from where the
// page is on, and messages and keys to the agent through herdr.
func TestTeamDevChat(t *testing.T) {
	b, ran := appTestDevPage(t)
	handler := b.devPageHandler()
	session := "/box/home/.claude/projects/-box-home--herdr-worktrees-repo-1-chart/0b6f7f4e-1c2d-4e5f-8a9b-0c1d2e3f4a5b.jsonl"
	agent := `"agent":"claude","agent_status":"blocked","cwd":"/box/home/.herdr/worktrees/repo/1-chart","agent_session":{"agent":"claude","kind":"path","source":"hook","value":"` + session + `"}`
	previous := appDevBoxExec
	appDevBoxExec = func(team string, command ...string) (string, error) {
		switch strings.Join(command[:min(3, len(command))], " ") {
		case "herdr pane list":
			*ran = append(*ran, strings.Join(command, " "))
			return `{"result":{"panes":[{"pane_id":"w2:p1",` + agent + `}]}}`, nil
		case "herdr pane read":
			return "Allow this read?\n❯ 1. Yes\n  2. No\n", nil
		}
		return previous(team, command...)
	}
	host, _ := appDevHostPath("payments", session)
	os.MkdirAll(filepath.Dir(host), 0o700)
	lines := []string{
		`{"type":"permission-mode","permissionMode":"auto"}`,
		`{"type":"user","timestamp":"2026-10-25T18:00:00Z","message":{"role":"user","content":"Fix the chart<system-reminder>hi says hello</system-reminder>"}}`,
		`{"type":"user","isMeta":true,"message":{"role":"user","content":"Caveat: meta"}}`,
		`{"type":"assistant","cwd":"/box/home/.herdr/worktrees/repo/1-chart","message":{"role":"assistant","content":[{"type":"thinking","thinking":"Look first."},{"type":"text","text":"I'll **look**."},{"type":"tool_use","id":"t1","name":"Read","input":{"file_path":"/box/home/.herdr/worktrees/repo/1-chart/src/App.tsx"}}]}}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"export default App"}]}}`,
		`{"type":"assistant","isSidechain":true,"message":{"role":"assistant","content":[{"type":"text","text":"a subagent's"}]}}`,
		`{"type":"user","message":{"role":"user","content":"<task-notification>\n<task-id>b1</task-id>\n<status>completed</status>\n<summary>Background command \"npm test 2&gt;&amp;1\" completed (exit code 0)</summary>\n</task-notification>"}}`,
		`{"type":"queue-operation","operation":"enqueue","content":"Also the legend"}`,
		`{"type":"attachment","attachment":{"type":"queued_command","prompt":"Also the legend\n\nAttached, in the box: /box/home/hi/chat/w2/a.png","commandMode":"prompt"}}`,
		`{"type":"attachment","attachment":{"type":"total_tokens_reminder"}}`,
		`{"type":"ai-title","aiTitle":"Fix the chart"}`,
	}
	os.WriteFile(host, []byte(strings.Join(lines, "\n")+"\n"+`{"type":"assistant","message":`), 0o600) // the last line is still being written
	var view appDevChatView
	code, body := appTestDevPageCall(t, handler, appTestOwnerKey, "GET", "api/chat?workspace=w2", nil)
	json.Unmarshal([]byte(body), &view)
	var got []string
	for _, e := range view.Entries {
		got = append(got, e.T+":"+firstNonEmpty(e.Summary, e.Text))
	}
	if code != 200 || strings.Join(got, " | ") != "user:Fix the chart | thinking:Look first. | text:I'll **look**. | tool:src/App.tsx | result:export default App | note:Background command \"npm test 2>&1\" completed (exit code 0) | user:Also the legend\n\nAttached, in the box: /box/home/hi/chat/w2/a.png | title:Fix the chart" {
		t.Fatalf("%d %s", code, strings.Join(got, " | "))
	}
	if view.Session != session || !view.Reset || !view.Agent.Running || view.Agent.Status != "blocked" || !strings.Contains(view.Screen, "1. Yes") {
		t.Fatalf("%+v", view)
	}
	// The line being written comes once it's whole; only what's new.
	f, _ := os.OpenFile(host, os.O_APPEND|os.O_WRONLY, 0o600)
	f.WriteString(`{"role":"assistant","content":[{"type":"text","text":"Done."}]}}` + "\n")
	f.Close()
	offset := view.Offset
	_, body = appTestDevPageCall(t, handler, appTestOwnerKey, "GET", fmt.Sprintf("api/chat?workspace=w2&offset=%d&session=%s", offset, url.QueryEscape(session)), nil)
	view = appDevChatView{}
	json.Unmarshal([]byte(body), &view)
	if len(view.Entries) != 1 || view.Entries[0].Text != "Done." || view.Reset || view.Offset <= offset {
		t.Fatalf("%s", body)
	}
	// Messages and keys go to the agent's pane; other keys don't.
	*ran = nil
	if code, body := appTestDevPageCall(t, handler, appTestOwnerKey, "POST", "api/chat/send", map[string]any{"workspace": "w2", "text": "Use green"}); code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	// A message's files go in the box's home, and the agent gets where.
	picture := base64.StdEncoding.EncodeToString([]byte("png"))
	if code, body := appTestDevPageCall(t, handler, appTestOwnerKey, "POST", "api/chat/send", map[string]any{"workspace": "w2", "text": "", "files": []map[string]string{{"name": "my shot.png", "data": picture}, {"name": "../my shot.png", "data": picture}}}); code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	if data, _ := os.ReadFile(filepath.Join(appDevResultsDir("payments", "chat", "w2"), "my-shot-2.png")); string(data) != "png" {
		t.Fatalf("%q", data)
	}
	if !strings.Contains(strings.Join(*ran, "\n"), "herdr agent prompt w2:p1 Attached, in the box: /box/home/hi/chat/w2/my-shot.png /box/home/hi/chat/w2/my-shot-2.png") {
		t.Fatal(*ran)
	}
	if code, _ := appTestDevPageCall(t, handler, appTestOwnerKey, "POST", "api/chat/send", map[string]any{"workspace": "w2", "text": "hi", "files": []map[string]string{{"name": "a.png", "data": "not base64!"}}}); code == 200 {
		t.Fatal("sent a file it couldn't read")
	}
	if code, _ := appTestDevPageCall(t, handler, appTestOwnerKey, "POST", "api/chat/keys", map[string]any{"workspace": "w2", "keys": []string{"1"}}); code != 200 {
		t.Fatal("the answer's key")
	}
	if code, _ := appTestDevPageCall(t, handler, appTestOwnerKey, "POST", "api/chat/keys", map[string]any{"workspace": "w2", "keys": []string{"ctrl+c"}}); code == 200 {
		t.Fatal("pressed a key the chat doesn't")
	}
	all := strings.Join(*ran, "\n")
	if !strings.Contains(all, "herdr agent prompt w2:p1 Use green") || !strings.Contains(all, "herdr agent send-keys w2:p1 1") {
		t.Fatal(all)
	}
	if code, _ := appTestDevPageCall(t, handler, appTestOwnerKey, "POST", "api/chat/resume", map[string]any{"workspace": "w2", "session": "0b6f7f4e-1c2d-4e5f-8a9b-0c1d2e3f4a5b"}); code == 200 {
		t.Fatal("resumed over a running agent")
	}
	// With its agent stopped, the workspace's newest transcript, and a resume.
	agent = `"agent":"","cwd":"/box/home"`
	_, body = appTestDevPageCall(t, handler, appTestOwnerKey, "GET", "api/chat?workspace=w2", nil)
	view = appDevChatView{}
	json.Unmarshal([]byte(body), &view)
	if view.Session != session || view.Agent.Running || len(view.Entries) != 9 {
		t.Fatalf("%s", body)
	}
	if code, _ := appTestDevPageCall(t, handler, appTestOwnerKey, "POST", "api/chat/send", map[string]any{"workspace": "w2", "text": "hello"}); code == 200 {
		t.Fatal("sent to no agent")
	}
	for _, id := range []string{"x; rm -rf /", ""} {
		if code, _ := appTestDevPageCall(t, handler, appTestOwnerKey, "POST", "api/chat/resume", map[string]any{"workspace": "w2", "session": id}); code == 200 {
			t.Fatalf("resumed %q", id)
		}
	}
	if code, body := appTestDevPageCall(t, handler, appTestOwnerKey, "POST", "api/chat/resume", map[string]any{"workspace": "w2", "session": "0b6f7f4e-1c2d-4e5f-8a9b-0c1d2e3f4a5b"}); code != 200 || !strings.Contains(strings.Join(*ran, "\n"), "herdr pane run w2:p1 claude --resume 0b6f7f4e-1c2d-4e5f-8a9b-0c1d2e3f4a5b") {
		t.Fatalf("%d %s %v", code, body, *ran)
	}
	// Claude Code's own spinner line, while it works.
	for screen, want := range map[string]string{
		"⏺ Reading the chart\n\n✻ Crafting… (7s · ↓ 556 tokens · thought for 4s)\n  ⎿  Tip: Use /permissions\n\n> ": "Crafting… (7s · ↓ 556 tokens · thought for 4s)",
		"* Pondering… (12s · esc to interrupt)\n": "Pondering… (12s)",
		"✶ Thinking…\n":                           "Thinking…",
		"> plain prompt\n":                        "",
	} {
		if got := appDevActivity(screen); got != want {
			t.Errorf("%q: %q", screen, got)
		}
	}
	if code, _ := appTestDevPageCall(t, handler, appTestAskerKey, "GET", "api/chat?workspace=w2", nil); code != http.StatusUnauthorized {
		t.Fatal("someone who isn't an owner read the chat")
	}
	if code, _ := appTestDevPageCall(t, handler, appTestOwnerKey, "GET", "api/chat?workspace=../x", nil); code != 400 {
		t.Fatal("a workspace that isn't one")
	}
}

// What the preview shows, for Show dev: a branch being tried, or dev as it
// was before what was shipped since.
func TestTeamDevPreviewWhat(t *testing.T) {
	b, _ := appTestDevPage(t)
	os.MkdirAll(filepath.Join(appDir("payments"), "run"), 0o700)
	saveTeamSlot("payments", appPreview{Slot: appSlotPreview, Source: appDevBranchRef("4-top-20"), Label: "4-top-20, not in dev yet", URL: "http://payments.corp.example:8080/"})
	status, err := b.devStatus()
	if err != nil || status.PreviewBranch != "4-top-20" || status.PreviewOld {
		t.Fatalf("%+v %v", status, err)
	}
	config, _ := loadTeam("payments")
	saveTeamSlot("payments", appPreview{Slot: appSlotPreview, Source: config.Branch, Label: "dev", Commit: "0123456789abcdef0123456789abcdef01234567"})
	if status, _ := b.devStatus(); status.PreviewBranch != "" || !status.PreviewOld {
		t.Fatalf("%+v", status)
	}
	if code, _ := appTestDevPageCall(t, b.devPageHandler(), appTestAskerKey, "POST", "api/preview-dev", map[string]any{}); code != http.StatusUnauthorized {
		t.Fatal("someone who isn't an owner moved the preview")
	}
}

// Done closes a task from the page, and Reopen opens it again; both say
// who, and the lead can't reopen.
func TestTeamDevTaskState(t *testing.T) {
	b, _ := appTestDevPage(t)
	handler := b.devPageHandler()
	appTicketDo("payments", appTicketRequest{Action: "add", Title: "Black and white", Author: "the lead"})
	if code, body := appTestDevPageCall(t, handler, appTestOwnerKey, "POST", "api/ticket/state", map[string]any{"number": 1, "close": true}); code != 200 || !strings.Contains(body, "closed") {
		t.Fatalf("%d %s", code, body)
	}
	shown, _ := appTicketDo("payments", appTicketRequest{Action: "show", Number: 1})
	if shown.Ticket.State != "closed" || !strings.Contains(shown.Ticket.Comments[0].Body, "Closed by Olga on the dev page.") {
		t.Fatalf("%+v", shown.Ticket)
	}
	if list, _ := appDevPageTickets("payments", false); len(list.Tickets) != 0 {
		t.Fatal("a closed task is still listed as open")
	}
	if code, _ := appTestDevPageCall(t, handler, appTestOwnerKey, "POST", "api/ticket/state", map[string]any{"number": 1, "close": false}); code != 200 {
		t.Fatal("reopen")
	}
	if shown, _ := appTicketDo("payments", appTicketRequest{Action: "show", Number: 1}); shown.Ticket.State != "open" || !strings.Contains(shown.Ticket.Comments[1].Body, "Reopened by Olga") {
		t.Fatalf("%+v", shown.Ticket)
	}
	if !appTicketOwnerActions["reopen"] {
		t.Fatal("the lead may reopen")
	}
}

// Start something new files the words, and their pictures, as a task, and
// starts it like a task: its branch, worktree, and the agent told to read
// it; Open takes herdr's new names.
func TestTeamDevStartNew(t *testing.T) {
	b, ran := appTestDevPage(t)
	handler := b.devPageHandler()
	text := "20 companies in the card! [image: image-1.png]\nAnd sorted by size."
	code, body := appTestDevPageCall(t, handler, appTestOwnerKey, "POST", "api/start-new", map[string]any{"text": text, "agent": "claude",
		"files": []map[string]any{{"name": "image-1.png", "data": []byte("png")}}})
	if code != 200 || !strings.Contains(body, `"branch":"1-20-companies-in-the-card"`) || !strings.Contains(body, `"workspace":"w3"`) || !strings.Contains(body, `"ticket":1`) {
		t.Fatalf("%d %s", code, body)
	}
	shown, _ := appTicketDo("payments", appTicketRequest{Action: "show", Number: 1})
	if shown.Ticket.Title != "20 companies in the card!" || !strings.Contains(shown.Ticket.Body, "And sorted by size.") || strings.Join(shown.Ticket.Files, ",") != "image-1.png" {
		t.Fatalf("%+v", shown.Ticket)
	}
	if last := shown.Ticket.Comments[len(shown.Ticket.Comments)-1]; !strings.Contains(last.Body, "Started in the dev box by ") || !strings.Contains(last.Body, "branch 1-20-companies-in-the-card, with Claude Code.") {
		t.Fatalf("%+v", shown.Ticket.Comments)
	}
	if by := appDevStarters("payments")["1-20-companies-in-the-card"]; by == "" {
		t.Fatal("no one started it")
	}
	if all := strings.Join(*ran, "\n"); !strings.Contains(all, "herdr worktree create --cwd /box/repo --branch 1-20-companies-in-the-card --base origin/main --focus") ||
		!strings.Contains(all, "herdr pane run w3:p1 claude 'Work on task #1: 20 companies in the card!.") {
		t.Fatal(all)
	}
	for _, bad := range []map[string]any{{"text": " ", "agent": "claude"}, {"text": "x", "agent": "gemini"}} {
		if code, _ := appTestDevPageCall(t, handler, appTestOwnerKey, "POST", "api/start-new", bad); code == 200 {
			t.Fatalf("%v started", bad)
		}
	}
	if code, body := appTestDevPageCall(t, handler, appTestOwnerKey, "POST", "api/focus", map[string]any{"workspace": "wC"}); code != 200 {
		t.Fatalf("Open on herdr's wC: %d %s", code, body)
	}
}

// Who asked: the last "Asked by … in the channel" or "Filed by … on the
// dev page" in a ticket's text, not the forge's account that filed it.
func TestTeamTicketAsker(t *testing.T) {
	for body, want := range map[string]string{
		"Bots are too good.\n\nAsked by Mark Villarosa in the channel: https://slack/x": "Mark Villarosa",
		"Fix it\n\nFiled by Olga on the dev page.":                                      "Olga",
		"Someone said Asked by Bob in the channel, inline":                              "",
		"": "",
	} {
		if got := appTicketAsker(body); got != want {
			t.Errorf("%q: %q, want %q", body, got, want)
		}
	}
}

func TestTeamDevNewTitle(t *testing.T) {
	for text, want := range map[string]string{
		"Fix the chart\nmore":                          "Fix the chart",
		"[image: a.png]\n\nLook  here [image: b.png] ": "Look here",
		"[image: a.png]":                               "Pictures from the dev page",
		"Now that we have the base of the original game in place, let's make the twist that makes it fun": "Now that we have the base of the original game in place, let's make…",
	} {
		if got := appDevNewTitle(text); got != want {
			t.Errorf("%q: %q, want %q", text, got, want)
		}
	}
}

// The lead starts a task when an owner asks, one at a time, and says
// where to follow it.
func TestTeamLeadStart(t *testing.T) {
	b, ran := appTestDevPage(t)
	appTicketDo("payments", appTicketRequest{Action: "add", Title: "Chart misses the last day", Author: "Olga"})
	appTicketDo("payments", appTicketRequest{Action: "add", Title: "A trading game", Author: "Olga"})
	if _, err := b.leadStart(2, "U07ASKER1"); err == nil || !strings.Contains(err.Error(), "only the app's owners") {
		t.Fatal(err)
	}
	// #1 is at work in the box (1-chart): it's in progress, and #2 waits.
	if line, err := b.leadStart(1, "U07OWNER1"); err != nil || !strings.HasPrefix(line, "Task #1 is already in progress!") {
		t.Fatal(line, err)
	}
	if _, err := b.leadStart(2, "U07OWNER1"); err == nil || !strings.Contains(err.Error(), "task #1 is in progress in the dev box (1-chart)") {
		t.Fatal(err)
	}
	// With nothing at work, it starts, with Claude Code, and the link opens
	// on the task's work.
	os.MkdirAll(filepath.Dir(appDevPageStatePath("payments")), 0o700)
	writeFileAtomic(appDevPageStatePath("payments"), []byte(`{"url":"http://payments.corp.example:8888/__hi/dev/?hi-key=secret"}`), 0o600)
	previous := appDevBoxExec
	appDevBoxExec = func(team string, command ...string) (string, error) {
		if strings.Join(command, " ") == "herdr workspace list" {
			return `{"result":{"workspaces":[{"workspace_id":"w1","label":"repo","agent_status":"unknown","worktree":{"checkout_path":"/box/repo"}}]}}`, nil
		}
		return previous(team, command...)
	}
	line, err := b.leadStart(2, "U07OWNER1")
	if want := "Task #2 is now in progress! You should follow the progress in the developer view: http://payments.corp.example:8888/__hi/dev/#task-2"; err != nil || line != want {
		t.Fatalf("%q %v", line, err)
	}
	if all := strings.Join(*ran, "\n"); !strings.Contains(all, "--branch 2-a-trading-game") || !strings.Contains(all, "claude 'Work on task #2") {
		t.Fatal(all)
	}
	shown, _ := appTicketDo("payments", appTicketRequest{Action: "show", Number: 2})
	if last := shown.Ticket.Comments[len(shown.Ticket.Comments)-1]; !strings.Contains(last.Body, "as U07OWNER1 asked in Slack: branch 2-a-trading-game") {
		t.Fatalf("%+v", last)
	}
}

// The Data tab: both data folders, the snapshots, and whether the preview
// starts empty, for owners only.
func TestTeamDevPageData(t *testing.T) {
	b, _ := appTestDevPage(t)
	handler := b.devPageHandler()
	os.MkdirAll(appSlotDataDir("payments", appSlotProduction), 0o700)
	os.WriteFile(filepath.Join(appSlotDataDir("payments", appSlotProduction), "prices.db"), []byte("12345"), 0o600)
	if code, _ := appTestDevPageCall(t, handler, appTestAskerKey, "GET", "api/data", nil); code != http.StatusUnauthorized {
		t.Fatal("an asker saw the data")
	}
	code, body := appTestDevPageCall(t, handler, appTestOwnerKey, "GET", "api/data", nil)
	var data struct {
		Folders   []appDataFolder      `json:"folders"`
		Snapshots []appDevPageSnapshot `json:"snapshots"`
	}
	if json.Unmarshal([]byte(body), &data); code != 200 || len(data.Folders) != 2 || data.Folders[0].Slot != appSlotProduction || data.Folders[0].Bytes != 5 || data.Snapshots == nil {
		t.Fatal(code, body)
	}
	if code, body := appTestDevPageCall(t, handler, appTestOwnerKey, "POST", "api/data/preview", map[string]bool{"empty": true}); code != 200 {
		t.Fatal(code, body)
	}
	if config, _ := loadTeam("payments"); config.PreviewData != appPreviewEmpty {
		t.Fatal("the preview doesn't start empty")
	}
	appTestDevPageCall(t, handler, appTestOwnerKey, "POST", "api/data/preview", map[string]bool{"empty": false})
	if config, _ := loadTeam("payments"); config.PreviewData != "" {
		t.Fatal("the preview still starts empty")
	}
}
