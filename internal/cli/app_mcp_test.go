package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// appMCPTestApp is an app with /mcp: a tool that reads and one that
// writes, answered as JSON or, with stream, as an event stream, in a
// session it forgets when told.
type appMCPTestApp struct {
	mu      sync.Mutex
	stream  bool
	session string
	calls   []string
	extra   string // a third tool, once the app has it
	// starting is how many more requests it answers 503, as an app does
	// while it starts.
	starting int
}

func (a *appMCPTestApp) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != appMCPPath || r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	var message appMCPMessage
	json.NewDecoder(r.Body).Decode(&message)
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.starting > 0 {
		a.starting--
		http.Error(w, "The app is starting.", http.StatusServiceUnavailable)
		return
	}
	if message.Method == "initialize" {
		a.session = fmt.Sprintf("s%d", len(a.calls))
		w.Header().Set("Mcp-Session-Id", a.session)
	} else if r.Header.Get("Mcp-Session-Id") != a.session {
		http.NotFound(w, r)
		return
	}
	a.calls = append(a.calls, message.Method)
	if len(message.ID) == 0 {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	var result any
	switch message.Method {
	case "initialize":
		result = map[string]any{"protocolVersion": appMCPProtocol, "capabilities": map[string]any{"tools": map[string]any{}},
			"serverInfo": map[string]string{"name": "payments"}, "instructions": "Amounts are in cents."}
	case "tools/list":
		tools := []any{
			map[string]any{"name": "list_suppliers", "inputSchema": map[string]any{"type": "object"}, "annotations": map[string]bool{"readOnlyHint": true}},
			map[string]any{"name": "pay_supplier", "inputSchema": map[string]any{"type": "object"}},
		}
		if a.extra != "" {
			tools = append(tools, map[string]any{"name": a.extra, "inputSchema": map[string]any{"type": "object"}, "annotations": map[string]bool{"readOnlyHint": true}})
		}
		result = map[string]any{"tools": tools}
	case "tools/call":
		var params struct{ Name string }
		json.Unmarshal(message.Params, &params)
		a.calls = append(a.calls, "call "+params.Name)
		result = map[string]any{"content": []map[string]string{{"type": "text", "text": "done " + params.Name}}}
	}
	data, _ := json.Marshal(result)
	answer, _ := json.Marshal(appMCPMessage{JSONRPC: "2.0", ID: message.ID, Result: data})
	if a.stream {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "event: message\ndata: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/progress\"}\n\nevent: message\ndata: %s\n\n", answer)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(answer)
}

// appMCPTestServe runs app as the address's app, on its port file.
func appMCPTestServe(t *testing.T, slot string, app http.Handler) {
	t.Helper()
	server := httptest.NewServer(app)
	t.Cleanup(server.Close)
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(server.URL, "http://"))
	os.MkdirAll(appDir("payments")+"/run", 0o700)
	if err := os.WriteFile(appSlotPortFile("payments", slot), []byte(port+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func appMCPTestCall(t *testing.T, b *appBroker, slot, method, params string) appMCPMessage {
	t.Helper()
	message := appMCPMessage{JSONRPC: "2.0", ID: json.RawMessage(`7`), Method: method}
	if params != "" {
		message.Params = json.RawMessage(params)
	}
	answer := b.leadMCP(context.Background(), slot, message)
	if answer == nil || string(answer.ID) != "7" {
		t.Fatalf("%s: %+v", method, answer)
	}
	return *answer
}

func TestTeamMCPLead(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t).appBroker

	// No app yet: a server without tools, which says so.
	answer := appMCPTestCall(t, b, appSlotProduction, "initialize", `{"protocolVersion":"2025-11-25"}`)
	if text := string(answer.Result); !strings.Contains(text, `"protocolVersion":"2025-11-25"`) || !strings.Contains(text, `"listChanged":true`) ||
		!strings.Contains(text, "It has no MCP server at /mcp yet") {
		t.Fatal(text)
	}
	if answer = appMCPTestCall(t, b, appSlotProduction, "tools/list", ""); string(answer.Result) != `{"tools":[]}` {
		t.Fatal(string(answer.Result))
	}

	production, preview := &appMCPTestApp{}, &appMCPTestApp{stream: true}
	appMCPTestServe(t, appSlotProduction, production)
	appMCPTestServe(t, appSlotPreview, preview)

	// Production: the app's instructions, and only the tools that read.
	answer = appMCPTestCall(t, b, appSlotProduction, "initialize", `{}`)
	if text := string(answer.Result); !strings.Contains(text, "only the app's tools that read") || !strings.Contains(text, "Amounts are in cents.") {
		t.Fatal(text)
	}
	if text := string(appMCPTestCall(t, b, appSlotProduction, "tools/list", "").Result); !strings.Contains(text, "list_suppliers") || strings.Contains(text, "pay_supplier") {
		t.Fatal(text)
	}
	if text := string(appMCPTestCall(t, b, appSlotProduction, "tools/call", `{"name":"list_suppliers","arguments":{}}`).Result); !strings.Contains(text, "done list_suppliers") {
		t.Fatal(text)
	}
	// One that writes isn't passed on, even when asked for by name.
	if text := string(appMCPTestCall(t, b, appSlotProduction, "tools/call", `{"name":"pay_supplier","arguments":{}}`).Result); !strings.Contains(text, `"isError":true`) || !strings.Contains(text, "app_preview") {
		t.Fatal(text)
	}
	for _, call := range production.calls {
		if call == "call pay_supplier" {
			t.Fatal("production paid a supplier", production.calls)
		}
	}

	// The preview, answering in an event stream: every tool.
	if text := string(appMCPTestCall(t, b, appSlotPreview, "tools/list", "").Result); !strings.Contains(text, "pay_supplier") {
		t.Fatal(text)
	}
	if text := string(appMCPTestCall(t, b, appSlotPreview, "tools/call", `{"name":"pay_supplier","arguments":{}}`).Result); !strings.Contains(text, "done pay_supplier") {
		t.Fatal(text)
	}

	// A new app (a release) has forgotten the session: hi starts another.
	production.mu.Lock()
	production.session = "gone"
	production.mu.Unlock()
	if text := string(appMCPTestCall(t, b, appSlotProduction, "tools/call", `{"name":"list_suppliers"}`).Result); !strings.Contains(text, "done list_suppliers") {
		t.Fatal(text)
	}

	// Only tools: not the app's resources, and no address but the two.
	if answer = appMCPTestCall(t, b, appSlotPreview, "resources/list", ""); answer.Error == nil || answer.Error.Code != -32601 {
		t.Fatalf("%+v", answer)
	}
	if b.leadMCP(context.Background(), appSlotPreview, appMCPMessage{JSONRPC: "2.0", Method: "notifications/initialized"}) != nil {
		t.Fatal("a notification got an answer")
	}
	response := httptest.NewRecorder()
	b.handler().ServeHTTP(response, httptest.NewRequest("POST", "/mcp/dev", strings.NewReader(`{}`)))
	if response.Code != http.StatusNotFound {
		t.Fatal(response.Code)
	}
}

// TestTeamMCPBridge runs hi app mcp as Hermes would: lines in, lines out,
// and a notice when the app's tools change.
func TestTeamMCPBridge(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t).appBroker
	app := &appMCPTestApp{}
	appMCPTestServe(t, appSlotPreview, app)
	broker := httptest.NewServer(b.handler())
	defer broker.Close()
	target, _ := url.Parse(broker.URL)
	client := &http.Client{Transport: appMCPTestTransport{target}}

	if err := runTeamMCPBridge([]string{"dev"}, strings.NewReader(""), io.Discard, client); err == nil {
		t.Fatal("bridged dev")
	}
	defer func(poll time.Duration) { appMCPPoll = poll }(appMCPPoll)
	appMCPPoll = 20 * time.Millisecond

	in, feed := io.Pipe()
	out := &appMCPTestLines{}
	done := make(chan error, 1)
	go func() { done <- runTeamMCPBridge([]string{appSlotPreview}, in, out, client) }()
	fmt.Fprintln(feed, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`)
	fmt.Fprintln(feed, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	fmt.Fprintln(feed, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	out.wait(t, `"id":2`)
	app.mu.Lock()
	app.extra = "list_payments"
	app.mu.Unlock()
	out.wait(t, `"method":"notifications/tools/list_changed"`)
	fmt.Fprintln(feed, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"pay_supplier"}}`)
	out.wait(t, "done pay_supplier")
	feed.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if text := out.String(); !strings.Contains(text, `"id":1,"result":{`) || strings.Count(text, "\n") < 4 {
		t.Fatal(text)
	}
}

// A lead that connects while the app starts gets its tools once the app
// is up, rather than an error that makes Hermes park the server; an app
// that doesn't come up is reported once the wait is over.
func TestTeamMCPBridgeWaitsForTheApp(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t).appBroker
	app := &appMCPTestApp{starting: 1000}
	appMCPTestServe(t, appSlotPreview, app)
	broker := httptest.NewServer(b.handler())
	defer broker.Close()
	target, _ := url.Parse(broker.URL)
	client := &http.Client{Transport: appMCPTestTransport{target}}
	defer func(wait, retry time.Duration) { appMCPStartWait, appMCPStartRetry = wait, retry }(appMCPStartWait, appMCPStartRetry)
	appMCPStartRetry = 10 * time.Millisecond
	bridge := func(input string) string {
		t.Helper()
		out := &appMCPTestLines{}
		if err := runTeamMCPBridge([]string{appSlotPreview}, strings.NewReader(input), out, client); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	starting := func(n int) {
		app.mu.Lock()
		app.starting = n
		app.mu.Unlock()
	}
	initialize := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}` + "\n"

	// An app that doesn't come up is reported once the wait is over.
	appMCPStartWait = 50 * time.Millisecond
	if text := bridge(initialize); !strings.Contains(text, "isn't answering now") || !strings.Contains(text, "503") {
		t.Fatalf("an app that doesn't start: %s", text)
	}
	// One that does is waited for.
	appMCPStartWait = time.Second
	starting(3)
	if text := bridge(initialize); !strings.Contains(text, `"id":1,"result":{`) || strings.Contains(text, "isn't answering") {
		t.Fatalf("while the app started: %s", text)
	}
	// tools/list too: the broker answers it with the app's 503 as an error.
	starting(2)
	list := initialize + `{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n" + `{"jsonrpc":"2.0","id":2,"method":"tools/list"}` + "\n"
	if text := bridge(list); !strings.Contains(text, `"id":2,"result":{"tools":[`) {
		t.Fatalf("tools/list: %s", text)
	}
}

type appMCPTestTransport struct{ target *url.URL }

func (t appMCPTestTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r.URL.Scheme, r.URL.Host = t.target.Scheme, t.target.Host
	return http.DefaultTransport.RoundTrip(r)
}

type appMCPTestLines struct {
	mu   sync.Mutex
	text strings.Builder
}

func (l *appMCPTestLines) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.text.Write(p)
}

func (l *appMCPTestLines) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.text.String()
}

func (l *appMCPTestLines) wait(t *testing.T, want string) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if strings.Contains(l.String(), want) {
			return
		}
	}
	t.Fatalf("no %s in:\n%s", want, l.String())
}

func TestNetExposeFileRoute(t *testing.T) {
	route, err := parseNetExposeRoute("/llms.txt=unix:/x")
	if err != nil || route.Prefix != "/llms.txt" {
		t.Fatal(route, err)
	}
	helper := &netExposeHelper{Routes: []netExposeRoute{route}}
	if helper.route("/llms.txt", true) == nil || helper.route("/llms.txt.bak", true) != nil || helper.route("/llms.txt/x", true) != nil {
		t.Fatal("a file's route takes other paths")
	}
}
