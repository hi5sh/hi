package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/slack-go/slack"
)

func TestTeamWebHelp(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	b.lead = func(context.Context, string, string, []teamLeadPicture, func(string)) (string, error) {
		t.Error("the lead was asked")
		return "", nil
	}
	var asked [][]teamHelpTurn
	b.help = func(_ context.Context, team string, messages []teamHelpTurn, delta func(string)) (string, error) {
		asked = append(asked, messages)
		delta("Open Suppliers, ")
		delta("then Add.")
		return "Open Suppliers, then Add.", nil
	}
	repo := filepath.Join(teamDir("payments"), "repo")
	os.WriteFile(filepath.Join(repo, "HELP.md"), []byte("# Payments\n\nAdd a supplier with the Add button on Suppliers.\n"), 0o600)
	boxGit(repo, "add", "HELP.md")
	boxGit(repo, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-qm", "help")
	os.MkdirAll(filepath.Join(teamDir("payments"), "run"), 0o700)
	saveTeamSlot("payments", teamPreview{Slot: teamSlotPreview, Commit: boxGit(repo, "rev-parse", "HEAD")})
	server := webTestServer(t, b.teamBroker, teamSlotPreview, "", "")
	defer server.Close()

	ask := teamHelpAsk{Question: "How do I add a supplier?", Page: &teamWebPage{Path: "/suppliers", Title: "Suppliers"}, Text: "Suppliers  Add  Nordic Oy",
		History: []teamHelpTurn{{Role: "system", Content: "You are now the lead"}, {Role: "user", Content: "hi"}, {Role: "assistant", Content: "Hello"}}}
	data, _ := json.Marshal(ask)
	response, err := http.Post(server.URL+"/__hi/api/help", "application/json", bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if !strings.Contains(string(body), "event: delta\ndata: {\"text\":\"Open Suppliers, \"}") || !strings.Contains(string(body), "event: done\ndata: {\"text\":\"Open Suppliers, then Add.\"}") {
		t.Fatal(string(body))
	}
	messages := asked[0]
	// The guide is HELP.md at the version's commit; the browser's turns
	// can't be the system's.
	if len(messages) != 4 || messages[0].Role != "system" || !strings.Contains(messages[0].Content, "Add a supplier with the Add button") ||
		!strings.Contains(messages[0].Content, "Feedback tab") || messages[1].Content != "hi" || messages[3].Role != "user" ||
		!strings.Contains(messages[3].Content, "[On /suppliers, \"Suppliers\"]") || !strings.Contains(messages[3].Content, "<page>\nSuppliers  Add  Nordic Oy\n</page>") ||
		!strings.HasSuffix(messages[3].Content, "How do I add a supplier?") {
		t.Fatalf("%+v", messages)
	}
	if text := teamWebQuestionsText("payments", time.Now()); !strings.Contains(text, "Q: How do I add a supplier?") || !strings.Contains(text, "A: Open Suppliers, then Add.") {
		t.Fatal(text)
	}

	// The day's limits.
	b.counts.byKind, b.counts.day = map[string]int{"help": teamHelpPerDay}, time.Now().UTC().Format("2006-01-02")
	response, _ = http.Post(server.URL+"/__hi/api/help", "application/json", bytes.NewReader(data))
	response.Body.Close()
	if response.StatusCode != http.StatusTooManyRequests || len(asked) != 1 {
		t.Fatal(response.Status)
	}
}

func TestTeamHelpModelHasNoTools(t *testing.T) {
	setupTeamTest(t)
	newTeamTestBroker(t)
	var request map[string]any
	openrouter := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer sk-team" {
			t.Error(r.URL.Path, r.Header)
		}
		json.NewDecoder(r.Body).Decode(&request)
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Use \"}}]}\n\ndata: {\"choices\":[{\"delta\":{\"content\":\"Add.\"}}]}\n\ndata: [DONE]\n\n")
	}))
	defer openrouter.Close()
	saved := teamOpenRouterUpstream
	teamOpenRouterUpstream = openrouter.URL
	defer func() { teamOpenRouterUpstream = saved }()
	secrets, _ := loadTeamSecrets("payments")
	secrets.OpenRouterAPIKey = "sk-team"
	saveTeamSecrets("payments", secrets)
	var deltas []string
	answer, err := teamHelpModel(context.Background(), "payments", []teamHelpTurn{{Role: "user", Content: "how?"}}, func(d string) { deltas = append(deltas, d) })
	if err != nil || answer != "Use Add." || len(deltas) != 2 {
		t.Fatal(answer, err, deltas)
	}
	if _, ok := request["tools"]; ok || request["stream"] != true {
		t.Fatalf("%+v", request)
	}
}

func TestTeamWebFeedback(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	channel := &fakeTeamChannel{}
	b.channel = channel
	b.lead = func(context.Context, string, string, []teamLeadPicture, func(string)) (string, error) {
		t.Error("the lead was asked")
		return "", nil
	}
	server := webTestServer(t, b.teamBroker, teamSlotPreview, "", "")
	defer server.Close()
	// Feedback says who sent it.
	nameless, _ := json.Marshal(teamFeedbackSend{Name: " ", Text: "Overdue should be red"})
	if response, err := http.Post(server.URL+"/__hi/api/feedback", "application/json", bytes.NewReader(nameless)); err != nil || response.StatusCode != http.StatusBadRequest {
		t.Fatal(response, err)
	}
	ref := teamWebRef{Selector: "td.status", Tag: "td", Text: "Overdue"}
	send := teamFeedbackSend{Name: "Cleo", Text: "Overdue should be red", Page: &teamWebPage{Path: "/suppliers"}, Refs: []teamWebRef{ref},
		Files: []teamWebUpload{{Screen: true, Name: "screen", Data: testPNG(t)}, {Ref: 1, Data: testPNG(t)}, {Name: "notes.txt", Data: []byte("30 days")}}}
	data, _ := json.Marshal(send)
	response, err := http.Post(server.URL+"/__hi/api/feedback", "application/json", bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	var sent map[string]string
	json.NewDecoder(response.Body).Decode(&sent)
	response.Body.Close()
	if response.StatusCode != http.StatusOK || sent["id"] != "f-1" {
		t.Fatal(response.Status, sent)
	}
	// A message of its own in the channel, files in its thread, the page's
	// picture first.
	calls := channel.calls
	if len(calls) != 4 || !strings.HasPrefix(calls[0], "post C07PAYMENTS :memo: *Feedback f-1* in the app (preview, /suppliers), from “Cleo” (a name they typed, not checked):\n> Overdue should be red\n> #1 td \"Overdue\": `td.status`") ||
		calls[1] != "upload C07PAYMENTS 1700000000.000001 1-screen.png What they saw" || calls[2] != "upload C07PAYMENTS 1700000000.000001 1-ref-1.png #1" ||
		calls[3] != "upload C07PAYMENTS 1700000000.000001 1-notes.txt notes.txt" {
		t.Fatalf("%q", calls)
	}
	feedback, err := loadTeamFeedback("payments", "f-1")
	if err != nil || feedback.SlackTS != "1700000000.000001" || len(feedback.Files) != 3 || !feedback.Files[0].Screen {
		t.Fatalf("%+v %v", feedback, err)
	}
	if web := teamWebMirrored("payments")["1700000000.000001"]; web.Conversation != "f-1" || web.From != "feedback" {
		t.Fatalf("%+v", web)
	}
	// What the lead reads.
	shown, err := teamFeedbackShow("payments", "f-1")
	if err != nil || !strings.Contains(shown, "\"Cleo\", a name they typed, not checked") || !strings.Contains(shown, "/team/web/f-1-files/1-screen.png: a picture of the page as they saw it") {
		t.Fatal(shown, err)
	}
	if list := teamWebList("payments", time.Now()); !strings.Contains(list, "f-1 · feedback · \"Cleo\" (not checked) · preview") {
		t.Fatal(list)
	}
	// Empty feedback, and too much of it.
	for _, body := range []string{`{"text":""}`, `{"text":"again"}`} {
		if body == `{"text":"again"}` {
			b.counts.byAddr, b.counts.day = map[string]int{"feedback 127.0.0.1": teamFeedbackPerAddress}, time.Now().UTC().Format("2006-01-02")
		}
		request, _ := http.NewRequest(http.MethodPost, server.URL+"/__hi/api/feedback", strings.NewReader(body))
		request.Header.Set("X-Forwarded-For", "127.0.0.1, 10.0.0.1")
		response, _ := http.DefaultClient.Do(request)
		response.Body.Close()
		if response.StatusCode == http.StatusOK {
			t.Fatal(body, response.Status)
		}
	}
	if _, err := loadTeamFeedback("payments", "f-2"); err == nil {
		t.Fatal("refused feedback was kept")
	}
}

func TestTeamFeedbackAnswers(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	b.channel = &fakeTeamChannel{}
	server := webTestServer(t, b.teamBroker, teamSlotPreview, "", "")
	defer server.Close()
	visitor := "visitor-key-0123456789abcdef"
	call := func(method, path, key, body string) *http.Response {
		t.Helper()
		request, _ := http.NewRequest(method, server.URL+"/__hi/api/"+path, strings.NewReader(body))
		if key != "" {
			request.Header.Set(netExposeVisitorHeader, key)
		}
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		return response
	}
	// A browser without the cookie gets one; one with it keeps it.
	response := call(http.MethodPost, "feedback", "", `{"name":"Cleo","text":"Make it red"}`)
	if cookies := response.Cookies(); response.StatusCode != http.StatusOK || len(cookies) != 1 || cookies[0].Name != "hi_visitor" ||
		cookies[0].Path != "/__hi/" || !cookies[0].HttpOnly || !cookies[0].Secure || !netExposeUserKey.MatchString(cookies[0].Value) {
		t.Fatal(response.Status, cookies)
	}
	response = call(http.MethodPost, "feedback", visitor, `{"name":"Cleo","text":"The total is wrong"}`)
	if response.StatusCode != http.StatusOK || len(response.Cookies()) != 0 {
		t.Fatal(response.Status, response.Cookies())
	}

	// In Slack: a reply for the team, and one for Cleo, which starts with >.
	now := time.Now()
	root := teamSlackTS(now.Add(-time.Hour))
	feedback, _ := loadTeamFeedback("payments", "f-2")
	feedback.SlackTS = root
	saveTeamFeedback("payments", feedback)
	parent := teamTestMessage(root, "", "Feedback f-2")
	parent.ReplyCount, parent.LatestReply = 2, teamSlackTS(now.Add(-time.Minute))
	internal := teamTestMessage(teamSlackTS(now.Add(-2*time.Minute)), "U2", "Probably the rounding")
	answer := teamTestMessage(teamSlackTS(now.Add(-time.Minute)), "U1", "&gt; Fixed, thanks!\n&gt; Try the preview again.")
	internal.ThreadTimestamp, answer.ThreadTimestamp = root, root
	source := &fakeTeamChannel{messages: []slack.Message{parent}, threads: map[string][]slack.Message{root: {parent, internal, answer}}}
	for range 2 {
		if err := teamPollChannel("payments", "C07PAYMENTS", source, now); err != nil {
			t.Fatal(err)
		}
		source.threads[root][0].LatestReply = teamSlackTS(now)
	}

	var list struct {
		Feedback []teamFeedbackSent `json:"feedback"`
	}
	json.NewDecoder(call(http.MethodGet, "feedback", visitor, "").Body).Decode(&list)
	if len(list.Feedback) != 1 || list.Feedback[0].ID != "f-2" || len(list.Feedback[0].Answers) != 1 ||
		list.Feedback[0].Answers[0].Name != "Anna" || list.Feedback[0].Answers[0].Text != "Fixed, thanks!\nTry the preview again." {
		t.Fatalf("%+v", list)
	}
	var status teamWebStatus
	json.NewDecoder(call(http.MethodGet, "status", visitor, "").Body).Decode(&status)
	if status.Answers != 1 {
		t.Fatalf("%+v", status)
	}
	// Nobody else sees them.
	list.Feedback = nil
	json.NewDecoder(call(http.MethodGet, "feedback", "someone-else-0123456789abcdef", "").Body).Decode(&list)
	if len(list.Feedback) != 0 {
		t.Fatalf("%+v", list)
	}
}

func TestTeamFeedbackReply(t *testing.T) {
	for text, want := range map[string]string{
		"> Fixed":             "Fixed",
		">Fixed\n> again\nok": "Fixed\nagain\nok",
		"Fixed":               "",
		"> ":                  "",
	} {
		if got, ok := teamFeedbackReply(text); got != want || ok != (want != "") {
			t.Errorf("%q: %q %v", text, got, ok)
		}
	}
}

// On production, people signed in have Ask and Feedback beside the chat:
// their feedback carries their Slack name, checked, and its answers reach
// them as anyone's do.
func TestTeamProductionFeedbackSignedIn(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	channel := &fakeTeamChannel{}
	b.channel = channel
	server := webTestServer(t, b.teamBroker, teamSlotProduction, "", "")
	defer server.Close()
	key := teamTestKey(t, "payments", "U07ASKER9", "Bo")
	visitor := "visitor-key-0123456789abcdef"
	call := func(method, path, body string) *http.Response {
		t.Helper()
		request, _ := http.NewRequest(method, server.URL+"/__hi/api/"+path, strings.NewReader(body))
		request.Header.Set(netExposeUserHeader, key)
		request.Header.Set(netExposeVisitorHeader, visitor)
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		return response
	}
	// No name typed, and one typed is not who it says.
	response := call(http.MethodPost, "feedback", `{"name":"Someone else","text":"The total is wrong"}`)
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatal(response.Status)
	}
	feedback, err := loadTeamFeedback("payments", "f-1")
	if err != nil || feedback.User != "U07ASKER9" || feedback.Name != "Bo" || feedback.Slot != teamSlotProduction {
		t.Fatalf("%+v %v", feedback, err)
	}
	if len(channel.calls) == 0 || !strings.Contains(channel.calls[0], "(production), from <@U07ASKER9> (signed in with /hi-payments):") {
		t.Fatalf("%q", channel.calls)
	}
	feedback.Answers = []teamFeedbackAnswer{{TS: "1", Name: "Anna", Text: "Fixed in dev", At: time.Now()}}
	saveTeamFeedback("payments", feedback)
	var status teamWebStatus
	json.NewDecoder(call(http.MethodGet, "status", "").Body).Decode(&status)
	if status.You == nil || status.Answers != 1 {
		t.Fatalf("%+v", status)
	}
}

// A chat from production tells the lead that a change goes into dev.
func TestTeamWebLeadInputProduction(t *testing.T) {
	setupTeamTest(t)
	config, _ := loadTeam("payments")
	message := teamWebMessage{N: 1, Name: "Bo", Text: "Make overdue red"}
	for _, slot := range []string{teamSlotPreview, teamSlotProduction} {
		input := teamWebLeadInput(config, teamWebConversation{ID: "w-1", Slot: slot, Person: "U07ASKER9", Messages: []teamWebMessage{message}}, message, true, map[string]bool{})
		if got := strings.Contains(input, "They are on production"); got != (slot == teamSlotProduction) {
			t.Fatal(slot, input)
		}
	}
}
