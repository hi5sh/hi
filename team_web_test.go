package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

func testPNG(t *testing.T) []byte {
	t.Helper()
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, image.NewRGBA(image.Rect(0, 0, 4, 3))); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
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
	server := webTestServer(t, b.teamBroker, teamSlotPreview, "U07CLEO01", "Cleo")
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
