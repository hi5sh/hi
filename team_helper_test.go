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

	"github.com/slack-go/slack"
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

func TestTeamHelperChat(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	channel := &fakeTeamChannel{}
	b.channel = channel
	var inputs []string
	b.lead = func(ctx context.Context, conversation, input string, _ []teamLeadPicture, delta func(string)) (string, error) {
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
	server := webTestServer(t, b.teamBroker, teamSlotPreview, "U07ASKER9", "Bo")
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
	if status.Slot != "preview" || status.Version != "dev, v3 + 2 changes" || status.Commit != "abc123" || status.You == nil || status.You.ID != "U07ASKER9" || status.You.Name != "Bo" || status.Ended {
		t.Fatalf("%+v", status)
	}
	// A relay from before /docs/ doesn't send it to hi: no docs button.
	if status.Docs != "" {
		t.Fatal(status.Docs)
	}
	saveTeamSlot("payments", teamPreview{Slot: teamSlotPreview, Label: "dev, v3 + 2 changes", Commit: "abc123", Relay: teamRelayStamp("payments", teamSlotPreview)})
	response, _ = http.Get(server.URL + "/__hi/api/status")
	status = teamWebStatus{}
	json.NewDecoder(response.Body).Decode(&status)
	response.Body.Close()
	if status.Docs != "/docs/" {
		t.Fatalf("%+v", status)
	}

	names, events := webEvents(t, server, teamWebSend{Text: "Make this one red when it's overdue",
		Page: &teamWebPage{Path: "/suppliers", Title: "Suppliers", Width: 1440, Height: 900}})
	if strings.Join(names, ",") != "message,delta,delta,done" {
		t.Fatal(names, string(events["delta"]))
	}
	var done struct {
		Conversation string         `json:"conversation"`
		Message      teamWebMessage `json:"message"`
	}
	json.Unmarshal(events["done"], &done)
	if done.Conversation != "w-1" || done.Message.From != "lead" || done.Message.Text != "Overdue means past the due date?" {
		t.Fatalf("%+v", done)
	}
	for _, want := range []string{"comes from the helper in the team's app", "[web chat w-1 · message w-1.1 · Bo (U07ASKER9, signed in through Slack) · preview: dev, v3 + 2 changes · /suppliers · \"Suppliers\" · 1440×900]\n\nMake this one red"} {
		if !strings.Contains(inputs[0], want) {
			t.Fatalf("the lead's input lacks %q:\n%s", want, inputs[0])
		}
	}

	// The second message continues the chat, without the note.
	webEvents(t, server, teamWebSend{Conversation: "w-1", Text: "Yes, go ahead"})
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
	// One thread in Bo's DM, not the channel: the first message starts it.
	if len(channel.calls) != 4 || conversation.DM != "DU07ASKER9" || !strings.HasPrefix(channel.calls[0], "dm U07ASKER9 :speech_balloon: You, in the app (preview, /suppliers): Make this one red") ||
		!strings.HasPrefix(channel.calls[1], "postIn DU07ASKER9 "+conversation.Thread+" Overdue means") ||
		!strings.HasPrefix(channel.calls[2], "postIn DU07ASKER9 "+conversation.Thread+" You, in the app: Yes, go ahead") {
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
	production := webTestServer(t, b.teamBroker, teamSlotProduction, "U07ASKER9", "Bo")
	defer production.Close()
	config, _ := loadTeam("payments")
	teamSetHelper(&config, teamSlotProduction, false)
	response, _ = http.Post(production.URL+"/__hi/api/messages", "application/json", strings.NewReader(`{"person":"U07ASKER9","text":"hi"}`))
	response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatal(response.Status)
	}
	// An empty message isn't taken.
	response, _ = http.Post(server.URL+"/__hi/api/messages", "application/json", strings.NewReader(`{"name":"Cleo","text":""}`))
	response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatal(response.Status)
	}
}

func TestTeamHelperSignIn(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	called := false
	b.lead = func(context.Context, string, string, []teamLeadPicture, func(string)) (string, error) {
		called = true
		return "ok", nil
	}
	status := func(server *httptest.Server, key string) (teamWebStatus, int) {
		request, _ := http.NewRequest(http.MethodGet, server.URL+"/__hi/api/status", nil)
		if key != "" {
			request.Header.Set(netExposeUserHeader, key)
		}
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		var status teamWebStatus
		json.NewDecoder(response.Body).Decode(&status)
		messages, _ := http.NewRequest(http.MethodPost, server.URL+"/__hi/api/messages", strings.NewReader(`{"text":"hi"}`))
		if key != "" {
			messages.Header.Set(netExposeUserHeader, key)
		}
		answer, _ := http.DefaultClient.Do(messages)
		answer.Body.Close()
		return status, answer.StatusCode
	}

	// Without a key: help and feedback, no chat, and the lead isn't asked;
	// saying who you are in the message changes nothing.
	server := webTestServer(t, b.teamBroker, teamSlotPreview, "", "")
	defer server.Close()
	if got, code := status(server, ""); got.You != nil || got.Ended || code != http.StatusUnauthorized {
		t.Fatalf("%+v %d", got, code)
	}
	response, _ := http.Post(server.URL+"/__hi/api/messages", "application/json", strings.NewReader(`{"person":"U07ASKER9","name":"Bo","text":"hi"}`))
	response.Body.Close()
	for _, path := range []string{"conversation", "events?conversation=w-1", "files/w-1/1-a.png"} {
		answer, _ := http.Get(server.URL + "/__hi/api/" + path)
		answer.Body.Close()
		if answer.StatusCode != http.StatusUnauthorized {
			t.Fatal(path, answer.Status)
		}
	}
	if response.StatusCode != http.StatusUnauthorized || called {
		t.Fatal("a message without a key:", response.Status, called)
	}

	// A key hi made works; one it didn't, one that ran out, or one whose
	// person is no longer an asker has ended.
	key := teamTestKey(t, "payments", "U07ASKER9", "Bo")
	if got, code := status(server, key); got.You == nil || got.You.Name != "Bo" || code != http.StatusOK {
		t.Fatalf("%+v %d", got, code)
	}
	if got, code := status(server, "key-nobody-made-0123456789"); got.You != nil || !got.Ended || code != http.StatusUnauthorized {
		t.Fatalf("%+v %d", got, code)
	}
	people := loadTeamPeople("payments")
	people.Keys = append(people.Keys, teamPersonKey{Key: "key-old-0123456789abcdef", User: "U07ASKER9", Name: "Bo", Ends: time.Now().Add(-time.Minute)})
	saveTeamPeople("payments", people)
	if got, _ := status(server, "key-old-0123456789abcdef"); got.You != nil || !got.Ended {
		t.Fatalf("%+v", got)
	}
	config, _ := loadTeam("payments")
	config.Askers = []string{"U07ASKER1"}
	saveTeam(config)
	if got, code := status(server, key); got.You != nil || !got.Ended || code != http.StatusUnauthorized {
		t.Fatalf("%+v %d", got, code)
	}
}

func TestTeamWebLink(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	channel := &fakeTeamChannel{}
	b.channel = channel
	os.MkdirAll(filepath.Join(teamDir("payments"), "run"), 0o700)
	saveTeamSlot("payments", teamPreview{Slot: teamSlotPreview, Label: "dev", Commit: "abc123", URL: "https://team-payments-ab12.example.net/?hi_key=K1", Expose: "team-payments"})
	b.exposed = func(name string) (netExposeState, bool) { return netExposeState{}, name == "team-payments" }

	if _, err := b.webLink("not-an-id"); err == nil {
		t.Fatal("took a bad ID")
	}
	// Someone not in the channel, with no askers, gets nothing.
	if _, err := b.webLink("U07STRANGER"); err == nil || !strings.Contains(err.Error(), "isn't one of the people") {
		t.Fatal(err)
	}
	line, err := b.webLink("U07ANNA01")
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
	if _, err := b.webLink("U07ANNA01"); err != nil {
		t.Fatal(err)
	}
	people = loadTeamPeople("payments")
	if len(people.Keys) != 1 || people.Keys[0].Key == first.Key {
		t.Fatalf("%+v", people)
	}
	config, _ := loadTeam("payments")
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

func TestTeamWebApproval(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	now := time.Now().UTC()
	saveTeamWebConversation("payments", teamWebConversation{ID: "w-4", Slot: teamSlotPreview, Person: "U07ASKER9", Name: "Bo", Verified: true, Started: now, Messages: []teamWebMessage{
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
	// A chat from before /hi, where the name was only claimed, approves
	// nothing.
	config.Askers = nil
	saveTeam(config)
	saveTeamWebConversation("payments", teamWebConversation{ID: "w-5", Slot: teamSlotPreview, Name: "Bo", Started: now, Messages: []teamWebMessage{
		{N: 1, From: "person", Name: "Bo", Text: "Red?", At: now}, {N: 2, From: "lead", Name: "lead", Text: "Shall I?", At: now}, {N: 3, From: "person", Name: "Bo", Text: "Yes", At: now},
	}})
	if _, err := b.teamFindApproval(config, "w-5.3", "task"); err == nil || !strings.Contains(err.Error(), "only said who they are") {
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

func TestTeamHelperChatWithRefs(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	channel := &fakeTeamChannel{}
	b.channel = channel
	var input string
	b.lead = func(_ context.Context, _, text string, _ []teamLeadPicture, _ func(string)) (string, error) {
		input = text
		return "Red when overdue. Shall I?", nil
	}
	os.MkdirAll(teamChannelDir("payments"), 0o700)
	server := webTestServer(t, b.teamBroker, teamSlotPreview, "U07ASKER9", "Bo")
	defer server.Close()
	ref := teamWebRef{Selector: "td.status > span.badge", Tag: "span", Text: "Paid", HTML: `<span class="badge">Paid</span>`}
	webEvents(t, server, teamWebSend{Text: "Make this one red", Page: &teamWebPage{Path: "/suppliers"}, Refs: []teamWebRef{ref}})
	if !strings.Contains(input, "Make this one red\n\nReferenced elements:\n#1 span \"Paid\"\n   selector: td.status > span.badge\n") || !strings.Contains(input, "parts of the page they clicked on") {
		t.Fatal(input)
	}
	if !strings.Contains(channel.calls[0], "Make this one red\n> #1 span \"Paid\": `td.status &gt; span.badge`") {
		t.Fatalf("%q", channel.calls[0])
	}
	webEvents(t, server, teamWebSend{Text: "Yes"})
	conversation, _ := loadTeamWebConversation("payments", "w-1")
	if len(conversation.Messages[0].Refs) != 1 || conversation.Messages[0].Refs[0].N != 1 {
		t.Fatalf("%+v", conversation.Messages[0])
	}
	said, _ := teamWebApprovalChat("payments", teamApproval{TS: "w-1.3"}, nil)
	if !strings.Contains(said, "The parts of the page they pointed at:\n\n    Referenced elements:\n    #1 span \"Paid\"\n       selector: td.status > span.badge") {
		t.Fatal(said)
	}
}

func TestTeamDMApproval(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	channel := &fakeTeamChannel{}
	b.channel = channel
	now := time.Now()
	ts := func(ago time.Duration) string { return teamSlackTS(now.Add(-ago)) }
	from := func(user, text string, ago time.Duration) slack.Message {
		message := teamTestMessage(ts(ago), user, text)
		return message
	}
	bot := func(text string, ago time.Duration) slack.Message {
		message := teamTestMessage(ts(ago), "", text)
		message.BotID = "B1"
		return message
	}
	channel.dms = map[string][]slack.Message{
		"U07ANNA01": {from("U07ANNA01", "Can the badge be red?", 30*time.Minute), bot("Overdue = past due? Shall I?", 20*time.Minute), from("U07ANNA01", "Yes please", 10*time.Minute)},
		"U1":        {bot(":speech_balloon: You, in the app (preview): Make it red", 15*time.Minute), from("U1", "Do it", 10*time.Minute)},
		"U07OWNER1": {bot("Production would get the red badge. Release it?", 5*time.Minute), from("U07OWNER1", "Yes, push it", 4*time.Minute)},
	}
	// hi's copy of a chat in the app isn't the lead asking in the DM.
	saveTeamWebConversation("payments", teamWebConversation{ID: "w-1", Slot: teamSlotPreview, Person: "U1", Name: "Anna", Verified: true, Started: now, DM: "DU1", Thread: ts(15 * time.Minute),
		Messages: []teamWebMessage{{N: 1, From: "person", Name: "Anna", Text: "Make it red", At: now, SlackTS: ts(15 * time.Minute)}}})
	config, _ := loadTeam("payments")
	approval, err := b.teamFindApproval(config, "dm:U07ANNA01", "task")
	if err != nil || approval.TS != "dm:U07ANNA01:"+ts(10*time.Minute) || approval.Name != "Anna" || approval.Text != "Yes please" {
		t.Fatalf("%+v %v", approval, err)
	}
	for ref, want := range map[string]string{
		"dm:U1":                              "after one of yours",
		"dm:U07STRANGE":                      "may ask for work",
		"dm:anna":                            "dm:<their member ID>",
		"dm:U07ANNA01:" + ts(30*time.Minute): "isn't a message of theirs after one of yours",
	} {
		if _, err := b.teamFindApproval(config, ref, "task"); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v", ref, err)
		}
	}
	// An owner may release from a DM, so it takes an owner; the rest of
	// what is theirs is said in the channel.
	if _, err := b.teamFindApproval(config, "dm:U07ANNA01", "release"); err == nil || !strings.Contains(err.Error(), "isn't one of the team's owners") {
		t.Fatal(err)
	}
	if approval, err := b.teamFindApproval(config, "dm:U07OWNER1", "release"); err != nil || approval.Text != "Yes, push it" {
		t.Fatal(approval, err)
	}
	if _, err := b.teamFindApproval(config, "dm:U07OWNER1", "rollback"); err == nil || !strings.Contains(err.Error(), "owner's yes in the channel") {
		t.Fatal(err)
	}

	// Work started with it goes to the owners in a DM, not the channel.
	task, err := b.startTask(teamRequest{Role: "coder", Brief: "Red badge when overdue", Approval: "dm:U07ANNA01"})
	if err != nil {
		t.Fatal(err)
	}
	waitTeamTask(t, task.ID)
	calls := strings.Join(channel.calls, "\n")
	if !strings.Contains(calls, "dm U07OWNER1 :speech_balloon: Anna asked the lead for this in a DM: *Red badge when overdue* ("+task.ID+").") || strings.Contains(calls, "post C07PAYMENTS") {
		t.Fatalf("%q", channel.calls)
	}
	// An owner who asked isn't told what they asked for.
	config.Owners = []string{"U07OWNER1", "U07ANNA01"}
	channel.calls = nil
	b.tellOwners(config, "U07ANNA01", "hello")
	if len(channel.calls) != 1 || channel.calls[0] != "dm U07OWNER1 hello" {
		t.Fatalf("%q", channel.calls)
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

func TestTeamWebMirrorWhere(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	channel := &fakeTeamChannel{}
	b.channel = channel
	config, _ := loadTeam("payments")
	now := time.Now()
	message := teamWebMessage{N: 1, From: "person", Name: "Bo", Text: "Make it red", At: now}
	// Someone who only said who they are: the channel, with their name.
	saveTeamWebConversation("payments", teamWebConversation{ID: "w-1", Slot: teamSlotPreview, Name: "Bo", Started: now, Messages: []teamWebMessage{message}})
	// A chat whose thread is in the channel already stays there.
	saveTeamWebConversation("payments", teamWebConversation{ID: "w-2", Slot: teamSlotPreview, Person: "U07ASKER9", Name: "Bo", Verified: true, Started: now, Thread: "1700000000.000009",
		Messages: []teamWebMessage{message, {N: 2, From: "person", Name: "Bo", Text: "Yes", At: now}}})
	b.mirrorWeb(config, "w-1", 1)
	b.mirrorWeb(config, "w-2", 2)
	if len(channel.calls) != 2 || channel.calls[0] != "post C07PAYMENTS :speech_balloon: *Bo*, in the app (preview): Make it red" ||
		channel.calls[1] != "postIn C07PAYMENTS 1700000000.000009 *Bo*, in the app: Yes" {
		t.Fatalf("%q", channel.calls)
	}
	// A yes in a chat copied into the DM has no thread in the channel.
	saveTeamWebConversation("payments", teamWebConversation{ID: "w-3", Slot: teamSlotPreview, Person: "U07ASKER9", Name: "Bo", Verified: true, Started: now, Thread: "1700000000.000011", DM: "DU07ASKER9",
		Messages: []teamWebMessage{message, {N: 2, From: "lead", Name: "lead", Text: "Shall I?", At: now}, {N: 3, From: "person", Name: "Bo", Text: "Yes", At: now}}})
	approval, err := teamCheckWebApproval(config, "w-3.3", false, now)
	if err != nil || approval.Thread != "" {
		t.Fatalf("%+v %v", approval, err)
	}
}

// The helper's history: a person's past chats here, latest first, and one
// opened again becomes the chat, ending the one that was.
func TestTeamWebHistory(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	b.lead = func(_ context.Context, _, _ string, _ []teamLeadPicture, _ func(string)) (string, error) {
		return "On it.", nil
	}
	server := webTestServer(t, b.teamBroker, teamSlotProduction, "U07ASKER9", "Bo")
	defer server.Close()
	webEvents(t, server, teamWebSend{Text: "Train from   the start\nof the data"})
	http.Post(server.URL+"/__hi/api/conversation/new", "application/json", strings.NewReader("{}"))
	webEvents(t, server, teamWebSend{Text: "Make overdue red"})
	// Someone else's chat, and one on the preview, aren't in it.
	saveTeamWebConversation("payments", teamWebConversation{ID: "w-3", Slot: teamSlotProduction, Person: "U07OTHER1", Name: "Cleo", Verified: true,
		Messages: []teamWebMessage{{N: 1, From: "person", Text: "Hi", At: time.Now()}}})
	saveTeamWebConversation("payments", teamWebConversation{ID: "w-4", Slot: teamSlotPreview, Person: "U07ASKER9", Name: "Bo", Verified: true,
		Messages: []teamWebMessage{{N: 1, From: "person", Text: "Hi", At: time.Now()}}})

	history := func() []teamWebChatSummary {
		response, err := http.Get(server.URL + "/__hi/api/conversations")
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		var list struct {
			Conversations []teamWebChatSummary `json:"conversations"`
		}
		json.NewDecoder(response.Body).Decode(&list)
		return list.Conversations
	}
	chats := history()
	if len(chats) != 2 || chats[0].ID != "w-2" || !chats[0].Open || chats[0].Title != "Make overdue red" || chats[0].Messages != 2 ||
		chats[1].ID != "w-1" || chats[1].Open || chats[1].Title != "Train from the start of the data" {
		t.Fatalf("%+v", chats)
	}

	open := func(id string) (*http.Response, teamWebConversation) {
		response, err := http.Post(server.URL+"/__hi/api/conversation/open", "application/json", strings.NewReader(`{"conversation":"`+id+`"}`))
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		var conversation teamWebConversation
		json.NewDecoder(response.Body).Decode(&conversation)
		return response, conversation
	}
	if response, conversation := open("w-1"); response.StatusCode != http.StatusOK || conversation.ID != "w-1" || len(conversation.Messages) != 2 {
		t.Fatal(response.Status, conversation)
	}
	if was, _ := loadTeamWebConversation("payments", "w-2"); !was.Ended {
		t.Fatal("the chat that was open didn't end")
	}
	// The next message goes on in it, under the lead's same conversation.
	webEvents(t, server, teamWebSend{Text: "Any news?"})
	if again, _ := loadTeamWebConversation("payments", "w-1"); len(again.Messages) != 4 || again.Ended {
		t.Fatalf("%+v", again)
	}
	for _, id := range []string{"w-3", "w-4", "w-99"} {
		if response, _ := open(id); response.StatusCode != http.StatusNotFound {
			t.Fatal(id, response.Status)
		}
	}
}

// A yes in a DM's thread counts, not an older message at the top of the
// DM (v0.97.1: t-95 was approved by "is it running?").
func TestTeamDMApprovalInThread(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	channel := &fakeTeamChannel{}
	b.channel = channel
	now := time.Now()
	ts := func(ago time.Duration) string { return teamSlackTS(now.Add(-ago)) }
	bot := func(message slack.Message) slack.Message {
		message.BotID = "B1"
		return message
	}
	asked := bot(teamTestMessage(ts(60*time.Minute), "", "Production v10 is out."))
	question := teamTestMessage(ts(40*time.Minute), "U07OWNER1", "is it running? it seems stuck still ?")
	question.ReplyCount, question.LatestReply = 2, ts(5*time.Minute)
	thread := []slack.Message{
		question,
		bot(teamTestMessage(ts(35*time.Minute), "", "It is stuck. Shall I fix it?")),
		teamTestMessage(ts(5*time.Minute), "U07OWNER1", "Change of plan: fix both. Go ahead."),
	}
	messages, err := teamDMWithThreads([]slack.Message{asked, question}, func(parent string) ([]slack.Message, error) {
		if parent != question.Timestamp {
			t.Fatal("read the replies of", parent)
		}
		return thread, nil
	})
	if err != nil || len(messages) != 4 || messages[3].Text != "Change of plan: fix both. Go ahead." {
		t.Fatalf("%v %+v", err, messages)
	}
	channel.dms = map[string][]slack.Message{"U07OWNER1": messages}
	config, _ := loadTeam("payments")
	approval, err := b.teamFindApproval(config, "dm:U07OWNER1", "task")
	if err != nil || approval.Text != "Change of plan: fix both. Go ahead." || approval.TS != "dm:U07OWNER1:"+ts(5*time.Minute) {
		t.Fatalf("%+v %v", approval, err)
	}
}
