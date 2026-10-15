package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testPNG(t *testing.T) []byte {
	t.Helper()
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, image.NewRGBA(image.Rect(0, 0, 4, 3))); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func TestTeamWebFiles(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	channel := &fakeTeamChannel{}
	b.channel = channel
	var input string
	var pictures []teamLeadPicture
	b.lead = func(_ context.Context, _, text string, shown []teamLeadPicture, _ func(string)) (string, error) {
		input, pictures = text, shown
		return "I see it.", nil
	}
	os.MkdirAll(teamChannelDir("payments"), 0o700)
	server := httptest.NewServer(b.webHandler(teamSlotPreview))
	defer server.Close()
	picture := testPNG(t)
	ref := teamWebRef{Selector: "span.badge", Tag: "span", Text: "Paid"}
	webEvents(t, server, teamWebSend{Name: "Cleo", Text: "Like this", Refs: []teamWebRef{ref}, Files: []teamWebUpload{
		{Name: "mockup.png", Data: picture},
		{Name: "notes.txt", Data: []byte("columns: due, paid")},
		{Name: "evil.svg", Data: []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`)},
		{Ref: 1, Data: picture},
	}})
	conversation, _ := loadTeamWebConversation("payments", "w-1")
	files := conversation.Messages[0].Files
	if len(files) != 4 || files[0].Name != "1-mockup.png" || files[0].Type != "image/png" || files[1].Type != "text/plain" || files[3].Name != "1-ref-1.png" || files[3].Ref != 1 {
		t.Fatalf("%+v", files)
	}
	for _, file := range files {
		if !fileExists(filepath.Join(teamWebFilesDir("payments", "w-1"), file.Name)) {
			t.Fatal(file.Name)
		}
	}
	// The lead sees the pictures, the picture of #1 first, and reads where
	// all the files are.
	if len(pictures) != 2 || !strings.HasPrefix(pictures[0].Label, "#1 as it looks") || !strings.HasPrefix(pictures[0].URL, "data:image/png;base64,") || !strings.HasPrefix(pictures[1].Label, "mockup.png") {
		t.Fatalf("%+v", pictures)
	}
	for _, want := range []string{"/team/web/w-1-files/1-mockup.png: mockup.png, image/png, 121 bytes (you see it below)", "/team/web/w-1-files/1-notes.txt: notes.txt, text/plain", "/team/web/w-1-files/1-ref-1.png: a picture of #1 (you see it below)", "--attach <path>"} {
		if !strings.Contains(input, want) {
			t.Fatalf("no %q in\n%s", want, input)
		}
	}
	// The files follow the message into the thread, #1's picture first.
	uploads := 0
	for _, call := range channel.calls {
		if strings.HasPrefix(call, "upload ") {
			uploads++
		}
	}
	if uploads != 4 || !strings.Contains(strings.Join(channel.calls, "\n"), "upload C07PAYMENTS 1700000000.000001 1-ref-1.png #1\nupload C07PAYMENTS 1700000000.000001 1-mockup.png mockup.png") {
		t.Fatalf("%q", channel.calls)
	}
	// A picture shows in the page; anything else, SVG too, only downloads.
	get := func(name string) *http.Response {
		response, err := http.Get(server.URL + "/__hi/api/files/w-1/" + name)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		return response
	}
	if response := get("1-mockup.png"); response.StatusCode != 200 || response.Header.Get("Content-Type") != "image/png" || response.Header.Get("Content-Disposition") != "" || response.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal(response.Status, response.Header)
	}
	if response := get("1-evil.svg"); response.Header.Get("Content-Type") != "application/octet-stream" || response.Header.Get("Content-Disposition") != `attachment; filename=evil.svg` {
		t.Fatal(response.Header)
	}
	if response := get("2-other.png"); response.StatusCode != 404 {
		t.Fatal(response.Status)
	}
	production := httptest.NewServer(b.webHandler(teamSlotProduction))
	defer production.Close()
	if response, _ := http.Get(production.URL + "/__hi/api/files/w-1/1-mockup.png"); response.StatusCode != 404 {
		t.Fatal("production serves the preview's files")
	}
	// The member gets the files with the chat.
	var added []string
	said, err := teamWebApprovalChat("payments", teamApproval{TS: "w-1.1"}, func(source, from string) (string, error) {
		added = append(added, filepath.Base(source)+" "+from)
		return "/box/files/" + filepath.Base(source), nil
	})
	if err != nil || len(added) != 4 || added[3] != "1-ref-1.png how #1 looked on Cleo's screen" || !strings.Contains(said, "[Cleo attached /box/files/1-mockup.png]") || !strings.Contains(said, "[a picture of #1: /box/files/1-ref-1.png]") {
		t.Fatal(said, added, err)
	}
	// Only files: no words needed; a picture of what wasn't pointed at, and
	// a reference's picture that isn't one, are refused.
	webEvents(t, server, teamWebSend{Name: "Cleo", Files: []teamWebUpload{{Name: "shot.png", Data: picture}}})
	for _, send := range []teamWebSend{
		{Name: "Cleo", Text: "x", Files: []teamWebUpload{{Ref: 1, Data: picture}}},
		{Name: "Cleo", Text: "x", Refs: []teamWebRef{ref}, Files: []teamWebUpload{{Ref: 1, Data: []byte("not a picture")}}},
		{Name: "Cleo", Text: "x", Files: []teamWebUpload{{Name: "empty.txt"}}},
	} {
		data, _ := json.Marshal(send)
		response, _ := http.Post(server.URL+"/__hi/api/messages", "application/json", bytes.NewReader(data))
		if response.StatusCode != http.StatusBadRequest {
			t.Fatalf("%+v: %s", send.Files, response.Status)
		}
	}
}

func TestTeamWebPictureFallback(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	var calls []int
	b.lead = func(_ context.Context, _, text string, pictures []teamLeadPicture, _ func(string)) (string, error) {
		calls = append(calls, len(pictures))
		if len(pictures) > 0 {
			return "", errors.New("this model can't see pictures")
		}
		if strings.Contains(text, "(you see it below)") {
			t.Error("told it sees a picture it wasn't shown")
		}
		return "I'll open it.", nil
	}
	server := httptest.NewServer(b.webHandler(teamSlotPreview))
	defer server.Close()
	names, last := webEvents(t, server, teamWebSend{Name: "Cleo", Text: "See", Files: []teamWebUpload{{Name: "shot.png", Data: testPNG(t)}}})
	if len(calls) != 2 || calls[0] != 1 || calls[1] != 0 || names[len(names)-1] != "done" || !strings.Contains(string(last["done"]), "I'll open it.") {
		t.Fatal(calls, names)
	}
}

// readEvent reads the next server-sent event's id, name, and data.
func readEvent(t *testing.T, reader *bufio.Reader) (string, string, string) {
	t.Helper()
	var id, name, data string
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		line = strings.TrimRight(line, "\n")
		switch {
		case line == "" && name != "":
			return id, name, data
		case strings.HasPrefix(line, "id: "):
			id = line[4:]
		case strings.HasPrefix(line, "event: "):
			name = line[7:]
		case strings.HasPrefix(line, "data: "):
			data = line[6:]
		}
	}
}

func TestTeamWebPost(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	channel := &fakeTeamChannel{}
	b.channel = channel
	var inputs []string
	b.lead = func(_ context.Context, _, text string, _ []teamLeadPicture, _ func(string)) (string, error) {
		inputs = append(inputs, text)
		return "Shall I make it red?", nil
	}
	os.MkdirAll(teamChannelDir("payments"), 0o700)
	server := httptest.NewServer(b.webHandler(teamSlotPreview))
	defer server.Close()
	webEvents(t, server, teamWebSend{Name: "Cleo", Text: "Make Paid red"})
	webEvents(t, server, teamWebSend{Name: "Cleo", Text: "Yes"})

	// A panel listening from its second message on hears the rest, then
	// the lead's post, as it comes.
	response, err := http.Get(server.URL + "/__hi/api/events?conversation=w-1&person=&name=Cleo&after=2")
	if err != nil || response.StatusCode != 200 || response.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatal(err, response.Status)
	}
	defer response.Body.Close()
	events := bufio.NewReader(response.Body)
	for _, want := range []string{"3", "4"} {
		if id, name, _ := readEvent(t, events); id != want || name != "message" {
			t.Fatal(id, name)
		}
	}
	shot := filepath.Join(t.TempDir(), "preview.png")
	os.WriteFile(shot, testPNG(t), 0o600)
	attachment, _ := teamReadAttachment(shot)
	line, err := b.webPost(teamWebPost{Conversation: "w-1", Text: "It's red on the preview now: reload to see it.", Attachments: []teamAttachment{attachment}})
	if err != nil || !strings.Contains(line, "Posted w-1.5 to Cleo on preview") {
		t.Fatal(line, err)
	}
	id, _, data := readEvent(t, events)
	var posted teamWebMessage
	json.Unmarshal([]byte(data), &posted)
	if id != "5" || posted.From != "lead" || !posted.Posted || posted.Text != "It's red on the preview now: reload to see it." || len(posted.Files) != 1 || posted.Files[0].Name != "5-preview.png" {
		t.Fatalf("%s %+v", id, posted)
	}
	calls := strings.Join(channel.calls, "\n")
	if !strings.Contains(calls, "postIn C07PAYMENTS 1700000000.000001 It's red on the preview now") || !strings.Contains(calls, "upload C07PAYMENTS 1700000000.000001 5-preview.png preview.png") {
		t.Fatal(calls)
	}
	// The lead's next answer here knows what it posted from elsewhere.
	webEvents(t, server, teamWebSend{Name: "Cleo", Text: "Looks great"})
	if last := inputs[len(inputs)-1]; !strings.HasPrefix(last, "(Since your last answer here, you also wrote in this chat, with hi team web post:\n> It's red on the preview now: reload to see it.)\n\n[web chat w-1") {
		t.Fatal(last)
	}
	if !strings.Contains(inputs[0], "hi team web post") {
		t.Fatal("the first note doesn't say how to post later")
	}

	// Not while it answers there, not in an ended chat, nor in someone
	// else's slot through the events.
	b.webMu.Lock()
	b.answering["w-1"] = true
	b.webMu.Unlock()
	if _, err := b.webPost(teamWebPost{Conversation: "w-1", Text: "hi"}); err == nil || !strings.Contains(err.Error(), "waiting for your answer") {
		t.Fatal(err)
	}
	b.webMu.Lock()
	delete(b.answering, "w-1")
	b.webMu.Unlock()
	if _, err := b.webPost(teamWebPost{Conversation: "w-1"}); err == nil {
		t.Fatal("an empty post")
	}
	http.Post(server.URL+"/__hi/api/conversation/new", "application/json", strings.NewReader(`{"name":"Cleo"}`))
	if _, err := b.webPost(teamWebPost{Conversation: "w-1", Text: "hi"}); err == nil || !strings.Contains(err.Error(), "ended w-1") {
		t.Fatal(err)
	}
	webEvents(t, server, teamWebSend{Name: "Cleo", Text: "Another thing"})
	if _, err := b.webPost(teamWebPost{Conversation: "w-1", Text: "hi"}); err == nil || !strings.Contains(err.Error(), "new chat since, w-2: post there") {
		t.Fatal(err)
	}
	for _, query := range []string{"conversation=w-2&name=Bo", "conversation=w-9&name=Cleo"} {
		if response, _ := http.Get(server.URL + "/__hi/api/events?" + query); response.StatusCode != 404 {
			t.Fatal(query, response.Status)
		}
	}

	// What the lead reads with hi team web.
	list := teamWebList("payments", time.Now())
	if !strings.HasPrefix(list, "w-2 · Cleo · preview · 2 messages · last moments ago, from") || !strings.Contains(list, "w-1 · Cleo · preview · 7 messages") || !strings.Contains(list, "· ended") {
		t.Fatal(list)
	}
	shown, err := teamWebShow("payments", "w-1")
	if err != nil || !strings.Contains(shown, "[w-1.5] payments team lead") || !strings.Contains(shown, "posted with hi team web post:\nIt's red") || !strings.Contains(shown, "/team/web/w-1-files/5-preview.png: preview.png, image/png") {
		t.Fatal(shown, err)
	}
}

func TestTeamLeadAPIPictures(t *testing.T) {
	var got []any
	socket := filepath.Join(shortTempDir(t), "lead.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Input []any }
		json.NewDecoder(r.Body).Decode(&body)
		got = body.Input
		io.WriteString(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"A badge.\"}\n\ndata: {\"type\":\"response.completed\"}\n\n")
	})}
	go server.Serve(listener)
	defer server.Close()
	answer, err := teamLeadAPI(socket, "k")(context.Background(), "web-w-1", "What is this?", []teamLeadPicture{{Label: "#1:", URL: "data:image/png;base64,AAAA"}}, nil)
	if err != nil || answer != "A badge." {
		t.Fatal(answer, err)
	}
	data, _ := json.Marshal(got)
	if string(data) != `[{"content":[{"text":"What is this?","type":"input_text"},{"text":"#1:","type":"input_text"},{"image_url":"data:image/png;base64,AAAA","type":"input_image"}],"role":"user"}]` {
		t.Fatal(string(data))
	}
}

func TestTeamHelperServesSnap(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	server := httptest.NewServer(b.webHandler(teamSlotPreview))
	defer server.Close()
	response, err := http.Get(server.URL + "/__hi/snap.js")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.Header.Get("Content-Type") != "text/javascript; charset=utf-8" || !strings.Contains(string(body), "export { createContext") || !strings.Contains(string(body), "domToCanvas") {
		t.Fatal(response.Header)
	}
	request, _ := http.NewRequest("GET", server.URL+"/__hi/snap.js", nil)
	request.Header.Set("If-None-Match", response.Header.Get("ETag"))
	if again, _ := http.DefaultClient.Do(request); again.StatusCode != http.StatusNotModified {
		t.Fatal(again.Status)
	}
}
