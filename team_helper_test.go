package main

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// webTestServer serves a slot's helper to someone signed in with /hi: it
// adds their key, as the relay does from the cookie. An empty user signs
// nobody in.
func webTestServer(t *testing.T, b *teamBroker, slot, user, name string) *httptest.Server {
	t.Helper()
	key := ""
	if user != "" {
		key = teamTestKey(t, b.team, user, name)
	}
	handler := b.webHandler(slot)
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if key != "" && r.Header.Get(netExposeUserHeader) == "" {
			r.Header.Set(netExposeUserHeader, key)
		}
		handler.ServeHTTP(w, r)
	}))
}

// teamTestKey gives someone a key, as /hi does, and returns it.
func teamTestKey(t *testing.T, team, user, name string) string {
	t.Helper()
	people := loadTeamPeople(team)
	key := "key-" + user + "-0123456789abcdef"
	now := time.Now().UTC()
	people.Keys = append(people.Keys, teamPersonKey{Key: key, User: user, Name: name, Made: now, Ends: now.Add(teamKeyLasts)})
	if err := saveTeamPeople(team, people); err != nil {
		t.Fatal(err)
	}
	return key
}

// webEvents posts a message to the helper and returns its events, in order.
func webEvents(t *testing.T, server *httptest.Server, send teamWebSend) ([]string, map[string]json.RawMessage) {
	t.Helper()
	data, _ := json.Marshal(send)
	response, err := http.Post(server.URL+"/__hi/api/messages", "application/json", strings.NewReader(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("%s: %s", response.Status, body)
	}
	var names []string
	last := map[string]json.RawMessage{}
	for _, chunk := range strings.Split(strings.TrimSpace(string(body)), "\n\n") {
		var name, payload string
		for _, line := range strings.Split(chunk, "\n") {
			if value, ok := strings.CutPrefix(line, "event: "); ok {
				name = value
			} else if value, ok := strings.CutPrefix(line, "data: "); ok {
				payload = value
			}
		}
		names = append(names, name)
		last[name] = json.RawMessage(payload)
	}
	return names, last
}

func TestTeamWebLink(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	channel := &fakeTeamChannel{}
	b.channel = channel
	os.MkdirAll(filepath.Join(teamDir("payments"), "run"), 0o700)
	saveTeamSlot("payments", teamPreview{Slot: teamSlotPreview, Label: "dev", Commit: "abc123", URL: "https://team-payments-ab12.example.net/?hi_key=K1", Expose: "team-payments"})
	b.exposed = func(name string) (netExposeState, bool) { return netExposeState{}, name == "team-payments" }
	config, _ := loadTeam("payments")
	config.Owners = append(config.Owners, "U07ANNA01")
	saveTeam(config)

	if _, err := b.webLink("not-an-id", false); err == nil {
		t.Fatal("took a bad ID")
	}
	// Private links are the owners': anyone else hears the app's own link
	// is all they need, and gets no key.
	if line, err := b.webLink("U07STRANGER", false); err != nil || !strings.Contains(line, "Private links are for") {
		t.Fatal(line, err)
	}
	if _, err := b.webLink("U07STRANGER", true); err == nil || !strings.Contains(err.Error(), "owners") {
		t.Fatal(err)
	}
	line, err := b.webLink("U07ANNA01", false)
	if err != nil || !strings.Contains(line, "DM, Anna") {
		t.Fatal(line, err)
	}
	people := loadTeamPeople("payments")
	if len(people.Keys) != 1 || people.Keys[0].User != "U07ANNA01" || len(people.Keys[0].Key) != 43 || people.Keys[0].DM != "DU07ANNA01" {
		t.Fatalf("%+v", people)
	}
	first := people.Keys[0]
	dm := channel.calls[len(channel.calls)-1]
	if !strings.HasPrefix(dm, "dm U07ANNA01 ") || !strings.Contains(dm, "https://team-payments-ab12.example.net/?hi_key=K1&hi_user="+first.Key+"|open the app>") || strings.Contains(dm, "Production") {
		t.Fatal(dm)
	}
	// /hi again: a new key, and the old one's DM says it ended.
	if _, err := b.webLink("U07ANNA01", false); err != nil {
		t.Fatal(err)
	}
	people = loadTeamPeople("payments")
	if len(people.Keys) != 1 || people.Keys[0].Key == first.Key {
		t.Fatalf("%+v", people)
	}
	if _, ok, ended := b.webUser(config, first.Key); ok || !ended {
		t.Fatal("the old key still works")
	}
	if !strings.Contains(strings.Join(channel.calls, "\n"), "update DU07ANNA01 "+first.DMTS+" :lock: These links have ended") {
		t.Fatalf("%q", channel.calls)
	}
	// A new address: the DM gets it.
	saveTeamSlot("payments", teamPreview{Slot: teamSlotPreview, Label: "dev", Commit: "abc123", URL: "https://team-payments-cd34.example.net/?hi_key=K2", Expose: "team-payments"})
	b.refreshLinks()
	if last := channel.calls[len(channel.calls)-1]; !strings.HasPrefix(last, "update DU07ANNA01 ") || !strings.Contains(last, "cd34.example.net/?hi_key=K2&hi_user="+people.Keys[0].Key) {
		t.Fatal(last)
	}
}

func TestTeamHelperSwitch(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	channel := &fakeTeamChannel{}
	b.channel = channel
	config, _ := loadTeam("payments")
	if err := teamSyncHelper(config); err != nil {
		t.Fatal(err)
	}
	for _, slot := range []string{"preview", "production"} {
		if !fileExists(teamHelperFile("payments", slot)) {
			t.Fatalf("the %s helper isn't on by default", slot)
		}
	}
	// The preview's goes off when asked; production's needs an owner's yes.
	if _, err := b.setHelper(teamHelperRequest{Slot: "preview", On: false}); err != nil {
		t.Fatal(err)
	}
	if fileExists(teamHelperFile("payments", "preview")) {
		t.Fatal("the preview's helper is still on")
	}
	if _, err := b.setHelper(teamHelperRequest{Slot: "production", On: false}); err == nil {
		t.Fatal("production's helper went off without an approval")
	}
	if _, err := b.setHelper(teamHelperRequest{Slot: "production", On: false, owner: "Anna on the dev page"}); err != nil {
		t.Fatal(err)
	}
	config, _ = loadTeam("payments")
	if fileExists(teamHelperFile("payments", "production")) || teamHelperOn(config, "production") {
		t.Fatal("production's helper is still on")
	}
	if line := teamHelperLine(config); line != "The helper (help and feedback in the app): off in preview and production." {
		t.Fatal(line)
	}
	versions, _ := loadTeamVersions("payments")
	if n := len(versions.History); n == 0 || versions.History[n-1].What != "production's helper off" {
		t.Fatalf("%+v", versions.History)
	}
	if len(channel.calls) == 0 || !strings.Contains(channel.calls[len(channel.calls)-1], "is off in production now. Anna on the dev page approved it.") {
		t.Fatalf("%q", channel.calls)
	}
	// On the machine, no approval: and on is the default again.
	var out strings.Builder
	if err := teamHelperCommand([]string{"payments", "production", "on"}, &out); err != nil {
		t.Fatal(err)
	}
	if err := teamHelperCommand([]string{"payments", "preview", "on"}, &out); err != nil {
		t.Fatal(err)
	}
	config, _ = loadTeam("payments")
	if config.Helper != nil || !fileExists(teamHelperFile("payments", "production")) {
		t.Fatalf("%+v", config.Helper)
	}
	if !strings.Contains(out.String(), "on in preview and production") {
		t.Fatal(out.String())
	}
}

func TestTeamExposeHasTheHelper(t *testing.T) {
	setupTeamTest(t)
	args := strings.Join(teamExposeHelperArgs("payments", teamSlotProduction), " ")
	for _, want := range []string{"--route /__hi/=unix:" + teamWebSocket("payments", "production"), "--inject /__hi/helper.js --inject-if",
		"--inject-if " + filepath.Join(teamDir("payments"), "run", "production.helper")} {
		if !strings.Contains(args, want) {
			t.Fatalf("%s lacks %s", args, want)
		}
	}
	if _, _, err := parseNetExpose(append([]string{"8080", "--link"}, teamExposeHelperArgs("payments", "preview")...)); err != nil {
		t.Fatal(err)
	}
}

func TestTeamReadLeadStream(t *testing.T) {
	stream := "event: response.created\ndata: {\"type\":\"response.created\"}\n\n" +
		": keepalive\n\n" +
		"event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"Hej \"}\n\n" +
		"event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"Bo\"}\n\n" +
		"event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{}}\n\n"
	var deltas []string
	text, err := teamReadLeadStream(strings.NewReader(stream), func(delta string) { deltas = append(deltas, delta) })
	if err != nil || text != "Hej Bo" || len(deltas) != 2 {
		t.Fatal(text, err, deltas)
	}
	failed := "event: response.failed\ndata: {\"type\":\"response.failed\",\"response\":{\"error\":{\"message\":\"rate limited\"}}}\n\n"
	if _, err := teamReadLeadStream(strings.NewReader(failed), nil); err == nil || err.Error() != "rate limited" {
		t.Fatal(err)
	}
	if _, err := teamReadLeadStream(strings.NewReader(""), nil); err == nil {
		t.Fatal("an empty answer passed")
	}
}

func TestTeamLeadAPIRelay(t *testing.T) {
	// Hermes' API server, inside the box.
	hermes := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if r.URL.Path != "/v1/responses" || r.Header.Get("Authorization") != "Bearer k3y-k3y-k3y-k3y-k3y" || body["conversation"] != "web-w-2" || body["stream"] != true {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"Sure.\"}\n\nevent: response.completed\ndata: {\"type\":\"response.completed\"}\n\n")
	}))
	defer hermes.Close()
	port := hermes.URL[strings.LastIndex(hermes.URL, ":")+1:]
	socket := filepath.Join(shortTempDir(t), teamLeadAPISocket)
	go runTeamLeadAPI([]string{socket, port}, io.Discard)
	for i := 0; i < 100 && !fileExists(socket); i++ {
		time.Sleep(10 * time.Millisecond)
	}
	if conn, err := net.Dial("unix", socket); err != nil {
		t.Fatal(err)
	} else {
		conn.Close()
	}
	answer, err := teamLeadAPI(socket, "k3y-k3y-k3y-k3y-k3y")(context.Background(), "web-w-2", "hello", nil, nil)
	if err != nil || answer != "Sure." {
		t.Fatal(answer, err)
	}
	if _, err := teamLeadAPI(filepath.Join(t.TempDir(), "none.sock"), "k")(context.Background(), "web-w-2", "hello", nil, nil); err == nil {
		t.Fatal("no lead, no error")
	}
	os.Remove(socket)
}

func TestWriteTeamLeadOpensTheAPI(t *testing.T) {
	setupTeamTest(t)
	config := testTeamConfig()
	os.MkdirAll(filepath.Join(teamDir("payments"), "lead"), 0o700)
	if err := writeTeamLead(config, teamSecrets{SlackBotToken: "xoxb-1", SlackAppToken: "xapp-1", LeadAPIKey: "0123456789abcdef0123"}, "10.0.0.2", ""); err != nil {
		t.Fatal(err)
	}
	env, _ := os.ReadFile(filepath.Join(teamDir("payments"), "lead", ".env"))
	for _, want := range []string{"API_SERVER_ENABLED=true", "API_SERVER_HOST=127.0.0.1", "API_SERVER_PORT=8642", "API_SERVER_KEY=0123456789abcdef0123"} {
		if !strings.Contains(string(env), want) {
			t.Fatalf(".env lacks %s:\n%s", want, env)
		}
	}
}

func TestTeamHelperScriptHasFinder(t *testing.T) {
	script := string(teamHelperScript)
	if !strings.Contains(script, "function finder(input, options)") || strings.Contains(script, "\nexport ") || !strings.HasPrefix(script, "(() => {") {
		t.Fatal("the served script isn't finder and the helper in one closure")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("no node to parse it with")
	}
	path := filepath.Join(t.TempDir(), "helper.js")
	os.WriteFile(path, teamHelperScript, 0o600)
	if out, err := exec.Command(node, "--check", path).CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
}

// TestHelperInABrowser runs helper/test, the helper's tests in happy-dom,
// on the exact script hi serves. It needs Bun and the network for the
// first install, so it is skipped without Bun; CI runs it whenever the
// helper changes.
func TestHelperInABrowser(t *testing.T) {
	bun, err := exec.LookPath("bun")
	if err != nil {
		t.Skip("no bun to run the helper's tests with")
	}
	script := filepath.Join(t.TempDir(), "helper.js")
	os.WriteFile(script, teamHelperScript, 0o600)
	for _, args := range [][]string{{"install", "--frozen-lockfile", "--silent"}, {"test"}} {
		command := exec.Command(bun, args...)
		command.Dir = filepath.Join("helper", "test")
		command.Env = append(os.Environ(), "HI_HELPER_SCRIPT="+script)
		if out, err := command.CombinedOutput(); err != nil {
			t.Fatalf("bun %s: %v\n%s", args[0], err, out)
		}
	}
}

func TestTeamWebRefs(t *testing.T) {
	long := strings.Repeat("é", 3000)
	refs := []teamWebRef{
		{Selector: "", Tag: "div"}, // no selector: dropped
		{N: 9, Selector: "main table.suppliers > tbody > tr:nth-child(1) > td.status > span.badge", Tag: "SPAN", Text: "Paid",
			Attributes: map[string]string{"class": "badge badge-ok", "data-testid": "status"},
			HTML:       `<span class="badge badge-ok" data-testid="status">Paid</span>`,
			Path:       []string{"main", `section "Suppliers"`, "table.suppliers", "tr", "td.status"},
			Landmarks:  map[string]string{"heading": "Suppliers"},
			Box:        &teamWebBox{X: 812, Y: 248, Width: 54, Height: 22, Viewport: [2]int{1440, 900}},
			Style:      map[string]string{"color": "rgb(255, 255, 255)", "background-color": "rgb(34, 139, 34)", "font-size": "12px"},
			Source:     &teamWebSource{File: "src/StatusBadge.vue", Line: 12, Component: "StatusBadge"}},
		{Selector: "#notes", Tag: "textarea", HTML: long, Text: long},
	}
	clean := teamCleanRefs(refs)
	if len(clean) != 2 || clean[0].N != 1 || clean[0].Tag != "span" || clean[1].N != 2 {
		t.Fatalf("%+v", clean)
	}
	if len(clean[1].HTML) > 2048 || !strings.HasSuffix(clean[1].HTML, "…") || !utf8.ValidString(clean[1].HTML) || len(clean[1].Text) > 200 {
		t.Fatal("a long reference isn't clipped cleanly:", len(clean[1].HTML), len(clean[1].Text))
	}
	text := teamWebRefsText(clean, "")
	for _, want := range []string{
		"Referenced elements:\n#1 span \"Paid\"\n",
		"   selector: main table.suppliers > tbody > tr:nth-child(1) > td.status > span.badge\n",
		"   under: main › section \"Suppliers\" › table.suppliers › tr › td.status\n",
		"   near: heading \"Suppliers\"\n",
		"   box: 54×22 at (812, 248) in a 1440×900 viewport; rgb(255, 255, 255) on rgb(34, 139, 34), 12px\n",
		"   source: src/StatusBadge.vue:12 (StatusBadge)\n",
		"#2 textarea \"éééé",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("lacks %q:\n%s", want, text)
		}
	}
	var many []teamWebRef
	for i := 0; i < 30; i++ {
		many = append(many, teamWebRef{Selector: "a", Tag: "a"})
	}
	if len(teamCleanRefs(many)) != teamWebMaxRefs {
		t.Fatal("more than ten references kept")
	}
}

func TestTeamAskedBy(t *testing.T) {
	tasks := []teamTask{
		{ID: "t-1", Approval: &teamApproval{Name: "Anna"}},
		{ID: "t-2", Approval: &teamApproval{Name: "Bo"}},
		{ID: "t-3", Approval: &teamApproval{Name: "Anna"}},
		{ID: "t-4", Small: true},
		{ID: "t-5"},
	}
	// A fix after a review counts as the work it fixed, asked for by the
	// same person.
	tasks = append(tasks, teamTask{ID: "t-6", On: "t-2"})
	if got := teamAskedBy(teamDevWork(tasks, tasks)); got != "Anna (t-1, t-3), Bo (t-2, t-6), the lead, as small fixes (t-4)" {
		t.Fatal(got)
	}
	text := teamReleaseAnnouncement(teamReleaseResult{Release: teamRelease{Version: 2}, Previous: 1, AskedBy: "Anna (t-1)"}, teamApproval{Name: "Owner"}, "Red badges.")
	if !strings.HasSuffix(text, "Red badges.\n\nAsked for by Anna (t-1).") {
		t.Fatal(text)
	}
}

func TestTeamReleaseHelperNote(t *testing.T) {
	if note := teamReleaseHelperNote(teamConfig{}); note != "" {
		t.Fatalf("on by default, but %q", note)
	}
	if note := teamReleaseHelperNote(teamConfig{Helper: map[string]bool{teamSlotProduction: true, teamSlotPreview: false}}); note != "" {
		t.Fatalf("on in production, but %q", note)
	}
	if note := teamReleaseHelperNote(teamConfig{Helper: map[string]bool{teamSlotProduction: false}}); !strings.Contains(note, "off in production") {
		t.Fatalf("off in production, but %q", note)
	}
}
