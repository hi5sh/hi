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
