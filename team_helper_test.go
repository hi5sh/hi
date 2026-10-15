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

func TestTeamHelperChat(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	channel := &fakeTeamChannel{}
	b.channel = channel
	var inputs []string
	b.lead = func(ctx context.Context, conversation, input string, delta func(string)) (string, error) {
		if conversation != "web-w-1" {
			t.Errorf("the lead's conversation is %q", conversation)
		}
		inputs = append(inputs, input)
		delta("Overdue means ")
		delta("past the due date?")
		return "Overdue means past the due date?", nil
	}
	os.MkdirAll(teamChannelDir("payments"), 0o700)
	os.MkdirAll(filepath.Join(teamDir("payments"), "run"), 0o700)
	teamAppendChannel(teamChannelDir("payments"), []teamChannelMessage{{TS: teamSlackTS(time.Now().Add(-time.Hour)), User: "U07ASKER9", Name: "Bo", Text: "hello"}})
	saveTeamSlot("payments", teamPreview{Slot: teamSlotPreview, Label: "dev, v3 + 2 changes", Commit: "abc123"})
	server := httptest.NewServer(b.webHandler(teamSlotPreview))
	defer server.Close()

	// The script, and the status: the people to choose from, and the version.
	response, err := http.Get(server.URL + "/__hi/helper.js")
	if err != nil {
		t.Fatal(err)
	}
	script, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if !strings.Contains(response.Header.Get("Content-Type"), "javascript") || string(script) != string(teamHelperScript) || response.Header.Get("Cache-Control") != "no-cache" {
		t.Fatal("the script:", response.Header)
	}
	// A browser's copy is checked, and kept while the script is the same.
	request, _ := http.NewRequest(http.MethodGet, server.URL+"/__hi/helper.js", nil)
	request.Header.Set("If-None-Match", response.Header.Get("ETag"))
	if again, err := http.DefaultClient.Do(request); err != nil || again.StatusCode != http.StatusNotModified {
		t.Fatal("no 304 for an unchanged script:", err)
	} else {
		again.Body.Close()
	}
	response, _ = http.Get(server.URL + "/__hi/api/status")
	var status teamWebStatus
	json.NewDecoder(response.Body).Decode(&status)
	response.Body.Close()
	if status.Slot != "preview" || status.Version != "dev, v3 + 2 changes" || status.Commit != "abc123" || !status.Anyone || len(status.People) != 2 || status.People[0].Name != "Bo" {
		t.Fatalf("%+v", status)
	}

	names, events := webEvents(t, server, teamWebSend{Person: "U07ASKER9", Text: "Make this one red when it's overdue",
		Page: &teamWebPage{Path: "/suppliers", Title: "Suppliers", Width: 1440, Height: 900}})
	if strings.Join(names, ",") != "message,delta,delta,done" {
		t.Fatal(names)
	}
	var done struct {
		Conversation string         `json:"conversation"`
		Message      teamWebMessage `json:"message"`
	}
	json.Unmarshal(events["done"], &done)
	if done.Conversation != "w-1" || done.Message.From != "lead" || done.Message.Text != "Overdue means past the due date?" {
		t.Fatalf("%+v", done)
	}
	for _, want := range []string{"comes from the helper in the team's app", "[web chat w-1 · message w-1.1 · Bo (U07ASKER9) · preview: dev, v3 + 2 changes · /suppliers · \"Suppliers\" · 1440×900]\n\nMake this one red"} {
		if !strings.Contains(inputs[0], want) {
			t.Fatalf("the lead's input lacks %q:\n%s", want, inputs[0])
		}
	}

	// The second message continues the chat, without the note.
	webEvents(t, server, teamWebSend{Conversation: "w-1", Person: "U07ASKER9", Text: "Yes, go ahead"})
	if strings.Contains(inputs[1], "comes from the helper") || !strings.Contains(inputs[1], "message w-1.3 ·") {
		t.Fatal(inputs[1])
	}
	conversation, err := loadTeamWebConversation("payments", "w-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(conversation.Messages) != 4 || conversation.Name != "Bo" || conversation.Thread == "" {
		t.Fatalf("%+v", conversation)
	}
	for _, message := range conversation.Messages {
		if message.SlackTS == "" {
			t.Fatalf("message %d wasn't copied to the channel", message.N)
		}
	}
	// One thread in the channel: the first message starts it.
	if len(channel.calls) != 4 || !strings.HasPrefix(channel.calls[0], "post C07PAYMENTS :speech_balloon: *Bo*, in the app (preview, /suppliers): Make this one red") ||
		!strings.HasPrefix(channel.calls[1], "postIn C07PAYMENTS "+conversation.Thread+" Overdue means") ||
		!strings.HasPrefix(channel.calls[2], "postIn C07PAYMENTS "+conversation.Thread+" *Bo*, in the app: Yes, go ahead") {
		t.Fatalf("%q", channel.calls)
	}
	// The channel's log marks the copies, and the lead's --new skips them.
	mirrored := teamWebMirrored("payments")
	if web := mirrored[conversation.Messages[0].SlackTS]; web.Conversation != "w-1" || web.From != "person" || web.Person != "U07ASKER9" {
		t.Fatalf("%+v", mirrored)
	}

	// The same person gets the chat back after a reload; a new one ends it.
	response, _ = http.Get(server.URL + "/__hi/api/conversation?person=U07ASKER9")
	var again teamWebConversation
	json.NewDecoder(response.Body).Decode(&again)
	response.Body.Close()
	if again.ID != "w-1" || len(again.Messages) != 4 {
		t.Fatalf("%+v", again)
	}
	http.Post(server.URL+"/__hi/api/conversation/new", "application/json", strings.NewReader(`{"person":"U07ASKER9"}`))
	response, _ = http.Get(server.URL + "/__hi/api/conversation?person=U07ASKER9")
	data, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if strings.TrimSpace(string(data)) != "{}" {
		t.Fatal(string(data))
	}

	// Production's chats are its own; a turned-off helper takes no messages.
	production := httptest.NewServer(b.webHandler(teamSlotProduction))
	defer production.Close()
	config, _ := loadTeam("payments")
	teamSetHelper(&config, teamSlotProduction, false)
	response, _ = http.Post(production.URL+"/__hi/api/messages", "application/json", strings.NewReader(`{"person":"U07ASKER9","text":"hi"}`))
	response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatal(response.Status)
	}
	// A name alone is fine while anyone may ask; an empty message isn't.
	response, _ = http.Post(server.URL+"/__hi/api/messages", "application/json", strings.NewReader(`{"name":"Cleo","text":""}`))
	response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatal(response.Status)
	}
}

func TestTeamHelperChatWithAskers(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	b.lead = func(context.Context, string, string, func(string)) (string, error) { return "ok", nil }
	config, _ := loadTeam("payments")
	config.Askers = []string{"U07ASKER1"}
	saveTeam(config)
	server := httptest.NewServer(b.webHandler(teamSlotPreview))
	defer server.Close()
	response, _ := http.Post(server.URL+"/__hi/api/messages", "application/json", strings.NewReader(`{"name":"Cleo","text":"hi"}`))
	response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatal("a name alone was taken when the team has askers:", response.Status)
	}
	response, _ = http.Get(server.URL + "/__hi/api/status")
	var status teamWebStatus
	json.NewDecoder(response.Body).Decode(&status)
	response.Body.Close()
	if status.Anyone || len(status.People) != 2 {
		t.Fatalf("%+v", status)
	}
}

func TestTeamWebApproval(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	now := time.Now().UTC()
	saveTeamWebConversation("payments", teamWebConversation{ID: "w-4", Slot: teamSlotPreview, Person: "U07ASKER9", Name: "Bo", Started: now, Messages: []teamWebMessage{
		{N: 1, From: "person", Name: "Bo", Text: "Make the badge red when overdue", Page: &teamWebPage{Path: "/suppliers"}, At: now},
		{N: 2, From: "lead", Name: "payments team lead", Text: "Overdue means past the due date? Shall I?", At: now},
		{N: 3, From: "person", Name: "Bo", Text: "Yes", At: now},
	}})
	config, _ := loadTeam("payments")
	for ref, want := range map[string]string{
		"w-4.1": "doesn't answer you",
		"w-4.2": "is yours",
		"w-4.9": "has no message 9",
		"w-7.1": "no web chat w-7",
	} {
		if _, err := b.teamFindApproval(config, ref, "task"); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v", ref, err)
		}
	}
	for _, approving := range []string{"release", "rollback", "helper"} {
		if _, err := b.teamFindApproval(config, "w-4.3", approving); err == nil || !strings.Contains(err.Error(), "an owner's yes in Slack") {
			t.Errorf("%s from the app: %v", approving, err)
		}
	}

	task, err := b.startTask(teamRequest{Role: "coder", Brief: "Red badge when overdue", Approval: "w-4.3"})
	if err != nil {
		t.Fatal(err)
	}
	task = waitTeamTask(t, task.ID)
	if task.Approval == nil || task.Approval.TS != "w-4.3" || task.Approval.Name != "Bo" {
		t.Fatalf("%+v", task.Approval)
	}
	brief := b.brief(task)
	for _, want := range []string{"The chat in the app this work was agreed in", "**Bo** (", ", on /suppliers): Make the badge red", "**payments team lead**", "): Yes"} {
		if !strings.Contains(brief, want) {
			t.Fatalf("the brief lacks %q:\n%s", want, brief)
		}
	}

	// With askers, the person must be one of them.
	config.Askers = []string{"U07ASKER1"}
	saveTeam(config)
	if _, err := b.teamFindApproval(config, "w-4.3", "task"); err == nil || !strings.Contains(err.Error(), "may ask for work") {
		t.Fatal(err)
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
	now := time.Now()
	os.MkdirAll(teamChannelDir("payments"), 0o700)
	root := teamSlackTS(now.Add(-3 * time.Minute))
	yes := teamSlackTS(now.Add(-time.Minute))
	teamAppendChannel(teamChannelDir("payments"), []teamChannelMessage{
		{TS: root, User: "U07OWNER1", Name: "Anna", Text: "@lead turn the chat off in production"},
		{TS: teamSlackTS(now.Add(-2 * time.Minute)), Thread: root, Name: "team-payments", Bot: true, Text: "Turn it off?"},
		{TS: yes, Thread: root, User: "U07OWNER1", Name: "Anna", Text: "Yes"},
	})
	if _, err := b.setHelper(teamHelperRequest{Slot: "production", On: false, Approval: yes}); err != nil {
		t.Fatal(err)
	}
	config, _ = loadTeam("payments")
	if fileExists(teamHelperFile("payments", "production")) || teamHelperOn(config, "production") {
		t.Fatal("production's helper is still on")
	}
	if line := teamHelperLine(config); line != "The helper (a chat with the lead in the app): off in preview and production." {
		t.Fatal(line)
	}
	versions, _ := loadTeamVersions("payments")
	if n := len(versions.History); n == 0 || versions.History[n-1].What != "production's helper off" {
		t.Fatalf("%+v", versions.History)
	}
	if len(channel.calls) == 0 || !strings.Contains(channel.calls[len(channel.calls)-1], "is off in production now. Anna approved it.") {
		t.Fatalf("%q", channel.calls)
	}
	// The same yes doesn't turn it on again.
	if _, err := b.setHelper(teamHelperRequest{Slot: "production", On: true, Approval: yes}); err == nil || !strings.Contains(err.Error(), "already approved") {
		t.Fatal(err)
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
	answer, err := teamLeadAPI(socket, "k3y-k3y-k3y-k3y-k3y")(context.Background(), "web-w-2", "hello", nil)
	if err != nil || answer != "Sure." {
		t.Fatal(answer, err)
	}
	if _, err := teamLeadAPI(filepath.Join(t.TempDir(), "none.sock"), "k")(context.Background(), "web-w-2", "hello", nil); err == nil {
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

func TestTeamHelperChatWithRefs(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	channel := &fakeTeamChannel{}
	b.channel = channel
	var input string
	b.lead = func(_ context.Context, _, text string, _ func(string)) (string, error) {
		input = text
		return "Red when overdue. Shall I?", nil
	}
	os.MkdirAll(teamChannelDir("payments"), 0o700)
	server := httptest.NewServer(b.webHandler(teamSlotPreview))
	defer server.Close()
	ref := teamWebRef{Selector: "td.status > span.badge", Tag: "span", Text: "Paid", HTML: `<span class="badge">Paid</span>`}
	webEvents(t, server, teamWebSend{Name: "Cleo", Text: "Make this one red", Page: &teamWebPage{Path: "/suppliers"}, Refs: []teamWebRef{ref}})
	if !strings.Contains(input, "Make this one red\n\nReferenced elements:\n#1 span \"Paid\"\n   selector: td.status > span.badge\n") || !strings.Contains(input, "parts of the page they clicked on") {
		t.Fatal(input)
	}
	if !strings.Contains(channel.calls[0], "Make this one red\n> #1 span \"Paid\": `td.status &gt; span.badge`") {
		t.Fatalf("%q", channel.calls[0])
	}
	webEvents(t, server, teamWebSend{Name: "Cleo", Text: "Yes"})
	conversation, _ := loadTeamWebConversation("payments", "w-1")
	if len(conversation.Messages[0].Refs) != 1 || conversation.Messages[0].Refs[0].N != 1 {
		t.Fatalf("%+v", conversation.Messages[0])
	}
	said := teamWebApprovalChat("payments", teamApproval{TS: "w-1.3"})
	if !strings.Contains(said, "The parts of the page they pointed at:\n\n    Referenced elements:\n    #1 span \"Paid\"\n       selector: td.status > span.badge") {
		t.Fatal(said)
	}
}
