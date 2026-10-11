package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/slack-go/slack"
)

type fakeTeamChannel struct {
	messages []slack.Message
	threads  map[string][]slack.Message
	calls    []string
}

func (f *fakeTeamChannel) history(channel, oldest string) ([]slack.Message, error) {
	f.calls = append(f.calls, "history "+channel)
	var out []slack.Message
	for _, message := range f.messages {
		if teamTSBefore(oldest, message.Timestamp) {
			out = append(out, message)
		}
	}
	return out, nil
}

func (f *fakeTeamChannel) replies(channel, thread, oldest string) ([]slack.Message, error) {
	f.calls = append(f.calls, "replies "+thread+" "+oldest)
	return f.threads[thread], nil
}

func (f *fakeTeamChannel) userName(id string) string {
	return map[string]string{"U1": "Anna", "U2": "Bo"}[id]
}

func (f *fakeTeamChannel) download(url string, w io.Writer) error {
	_, err := io.WriteString(w, "video bytes from "+url)
	return err
}

func teamTestMessage(ts, user, text string) slack.Message {
	var message slack.Message
	message.Timestamp, message.User, message.Text = ts, user, text
	return message
}

func TestTeamChannelLog(t *testing.T) {
	setupTeamTest(t)
	os.MkdirAll(teamDir("payments"), 0o700)
	if err := saveTeam(testTeamConfig()); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	ts := func(ago time.Duration) string { return teamSlackTS(now.Add(-ago)) }
	first, video, thread := ts(3*time.Hour), ts(2*time.Hour), ts(90*time.Minute)

	joined := teamTestMessage(ts(4*time.Hour), "U2", "<@U2> has joined the channel")
	joined.SubType = "channel_join"
	shared := teamTestMessage(video, "U2", "Look at this, <@U1>: <https://example.com/demo|the demo>")
	shared.Files = []slack.File{{ID: "F1", Name: "demo clip.mp4", Mimetype: "video/mp4", Size: 2048, URLPrivateDownload: "https://files.slack.com/F1"},
		{ID: "F2", Name: "huge.mov", Size: teamChannelMaxFile + 1, URLPrivateDownload: "https://files.slack.com/F2", Permalink: "https://slack.com/F2"}}
	parent := teamTestMessage(thread, "U1", "Should the export be daily?")
	parent.ReplyCount, parent.LatestReply = 1, ts(80*time.Minute)
	broadcast := teamTestMessage(ts(80*time.Minute), "U2", "Daily, &amp; at 6.")
	broadcast.ThreadTimestamp, broadcast.SubType = thread, "thread_broadcast"
	source := &fakeTeamChannel{
		messages: []slack.Message{teamTestMessage(first, "U1", "We want the totals in euros"), joined, shared, parent, broadcast},
		threads:  map[string][]slack.Message{thread: {parent, broadcast}},
	}
	if err := teamPollChannel("payments", "C07PAYMENTS", source, now); err != nil {
		t.Fatal(err)
	}
	dir := teamChannelDir("payments")
	messages, err := readTeamChannel(dir, "0")
	if err != nil {
		t.Fatal(err)
	}
	// The join is skipped, and the reply sent to the channel too is logged once, in its thread.
	if len(messages) != 4 || messages[0].Text != "We want the totals in euros" || messages[3].Thread != thread || messages[3].Text != "Daily, & at 6." {
		t.Fatalf("%+v", messages)
	}
	if messages[1].Text != "Look at this, @Anna: the demo (https://example.com/demo)" || messages[1].Name != "Bo" {
		t.Fatalf("%+v", messages[1])
	}
	files := messages[1].Files
	if len(files) != 2 || files[0].Path == "" || files[1].Path != "" || files[1].Link != "https://slack.com/F2" {
		t.Fatalf("%+v", files)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, files[0].Path)); string(data) != "video bytes from https://files.slack.com/F1" {
		t.Fatal(string(data))
	}

	// The next poll adds only what's new: a message, and a reply in the thread.
	later := teamTestMessage(ts(time.Minute), "U1", "Thanks!")
	reply := teamTestMessage(ts(30*time.Second), "U1", "Agreed")
	reply.ThreadTimestamp = thread
	parent.LatestReply = reply.Timestamp
	source.messages = []slack.Message{teamTestMessage(first, "U1", "We want the totals in euros"), shared, parent, broadcast, later}
	source.threads[thread] = []slack.Message{parent, broadcast, reply}
	source.calls = nil
	if err := teamPollChannel("payments", "C07PAYMENTS", source, now); err != nil {
		t.Fatal(err)
	}
	if messages, _ = readTeamChannel(dir, "0"); len(messages) != 6 || messages[5].Text != "Agreed" {
		t.Fatalf("%+v", messages)
	}
	// A third poll with nothing new asks for no thread's replies.
	source.calls = nil
	teamPollChannel("payments", "C07PAYMENTS", source, now)
	if len(source.calls) != 1 {
		t.Fatal(source.calls)
	}

	// On the host, by name.
	var out bytes.Buffer
	if err := runTeamChannel([]string{"payments", "--since", "150m"}, &out); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "euros") || !strings.Contains(out.String(), "Bo: Look at this") ||
		!strings.Contains(out.String(), "Bo, in Anna's thread from") || !strings.Contains(out.String(), "[file: huge.mov, unknown type, 50.0 MB: https://slack.com/F2]") {
		t.Fatal(out.String())
	}

	// In the lead's box: --new prints each message once.
	teamChannelInBox = dir
	t.Cleanup(func() { teamChannelInBox = "/team/channel" })
	t.Setenv("HOME", t.TempDir())
	out.Reset()
	if err := runTeamChannel([]string{"--new"}, &out); err != nil || !strings.Contains(out.String(), "euros") {
		t.Fatal(out.String(), err)
	}
	out.Reset()
	if err := runTeamChannel([]string{"--new"}, &out); err != nil || out.String() != "" {
		t.Fatalf("%q %v", out.String(), err)
	}
}

func TestTeamTSBefore(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{{"", "1.0", true}, {"1.0", "", false}, {"", "", false}, {"999.5", "1000.1", true}, {"1000.000002", "1000.000010", true}, {"1000.1", "1000.1", false}} {
		if got := teamTSBefore(c.a, c.b); got != c.want {
			t.Errorf("teamTSBefore(%q, %q) = %v", c.a, c.b, got)
		}
	}
}
