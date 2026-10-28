package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A contributor, signed in with their own link, reports a bug:
// it is a task at once, with who, where, and what they pointed at, and the
// channel hears it. Without a link, or stopped by an owner, Feedback is
// all there is.
func TestTeamWebReport(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	channel := &fakeTeamChannel{}
	b.channel = channel
	os.MkdirAll(filepath.Join(teamDir("payments"), "run"), 0o700)
	saveTeamSlot("payments", teamPreview{Slot: teamSlotPreview, Label: "dev, v3 + 1 change"})
	config, _ := loadTeam("payments")
	config.Contributors = []string{"U07ANNA01"}
	saveTeam(config)
	server := webTestServer(t, b.teamBroker, teamSlotPreview, "U07ANNA01", "Anna")
	defer server.Close()
	status := func() teamWebStatus {
		t.Helper()
		response, err := http.Get(server.URL + "/__hi/api/status")
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		var status teamWebStatus
		json.NewDecoder(response.Body).Decode(&status)
		return status
	}
	report := func(send teamFeedbackSend, key string) (*http.Response, teamReportSent) {
		t.Helper()
		data, _ := json.Marshal(send)
		request, _ := http.NewRequest(http.MethodPost, server.URL+"/__hi/api/report", bytes.NewReader(data))
		if key != "" {
			request.Header.Set(netExposeUserHeader, key)
		}
		request.Header.Set("User-Agent", "Firefox/150")
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		var sent teamReportSent
		json.NewDecoder(response.Body).Decode(&sent)
		return response, sent
	}
	if s := status(); !s.Report || s.You != "Anna" {
		t.Fatalf("%+v", s)
	}
	send := teamFeedbackSend{Text: "The export button does nothing\nOn the orders page, since this morning", Page: &teamWebPage{Path: "/orders"},
		Refs: []teamWebRef{{Selector: "button.export", Tag: "button", Text: "Export"}}, Files: []teamWebUpload{{Ref: 1, Data: testPNG(t)}}}
	response, sent := report(send, "")
	if response.StatusCode != http.StatusOK || sent.Number != 1 {
		t.Fatal(response.Status, sent)
	}
	shown, err := teamTicketDo("payments", teamTicketRequest{Action: "show", Number: 1})
	if err != nil || shown.Ticket == nil {
		t.Fatal(err)
	}
	if shown.Ticket.Title != "The export button does nothing" || !strings.Contains(shown.Ticket.Body, "Reported by Anna from the app's helper, on preview (dev, v3 + 1 change), /orders.") ||
		!strings.Contains(shown.Ticket.Body, "Browser: Firefox/150") || !strings.Contains(shown.Ticket.Body, "button.export") {
		t.Fatalf("%+v", shown.Ticket)
	}
	files, _ := teamTicketDo("payments", teamTicketRequest{Action: "files", Number: 1})
	if data, _ := json.Marshal(files); !strings.Contains(string(data), "pointed-at-1") {
		t.Fatalf("no picture of what they pointed at: %s", data)
	}
	if calls := strings.Join(channel.calls, "\n"); !strings.Contains(calls, ":lady_beetle: <@U07ANNA01> reported #1 from the app's helper (preview (dev, v3 + 1 change), /orders): The export button does nothing") {
		t.Fatalf("%q", channel.calls)
	}
	// No link of their own: Feedback, not Report.
	if response, _ := report(send, "no-such-key-0123456789abcdef"); response.StatusCode != http.StatusForbidden {
		t.Fatal("a report without a link:", response.Status)
	}
	// No longer a contributor.
	config.Contributors = nil
	saveTeam(config)
	if s := status(); s.Report {
		t.Fatalf("%+v", s)
	}
	if response, _ := report(send, ""); response.StatusCode != http.StatusForbidden {
		t.Fatal("a stopped person's report:", response.Status)
	}
}

// Anonymous feedback is for people in the channel: nothing says the lead
// files it.
func TestTeamFeedbackIsForPeople(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	channel := &fakeTeamChannel{}
	b.channel = channel
	config, _ := loadTeam("payments")
	b.postFeedback(config, teamFeedback{ID: "f-1", Slot: teamSlotPreview, Name: "Cleo", Text: "Overdue should be red"})
	if calls := strings.Join(channel.calls, "\n"); !strings.Contains(calls, "It's for people here to handle; nothing files it as a task on its own (f-1)") || strings.Contains(calls, "The lead files") {
		t.Fatalf("%q", channel.calls)
	}
}
