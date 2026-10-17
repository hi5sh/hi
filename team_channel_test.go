package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/slack-go/slack"
)

type fakeTeamChannel struct {
	dms      map[string][]slack.Message // by person, oldest first
	messages []slack.Message
	threads  map[string][]slack.Message
	calls    []string
	// streamErr is what appendStream answers, such as stopped_by_user.
	streamErr string
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
	return map[string]string{"U1": "Anna", "U2": "Bo", "U07ANNA01": "Anna"}[id]
}

func (f *fakeTeamChannel) post(channel, text string) (string, error) {
	f.calls = append(f.calls, "post "+channel+" "+text)
	return fmt.Sprintf("1700000000.%06d", len(f.calls)), nil
}

func (f *fakeTeamChannel) postIn(channel, thread, text string) (string, error) {
	f.calls = append(f.calls, "postIn "+channel+" "+thread+" "+text)
	return fmt.Sprintf("1700000000.%06d", len(f.calls)), nil
}

func (f *fakeTeamChannel) upload(channel, thread, path, title string) error {
	f.calls = append(f.calls, "upload "+channel+" "+thread+" "+filepath.Base(path)+" "+title)
	return nil
}

func (f *fakeTeamChannel) update(channel, ts, text string) error {
	f.calls = append(f.calls, "update "+channel+" "+ts+" "+text)
	return nil
}

func (f *fakeTeamChannel) startStream(channel, thread, user string, chunks []map[string]any) (string, error) {
	data, _ := json.Marshal(chunks)
	f.calls = append(f.calls, "startStream "+channel+" "+thread+" "+user+" "+string(data))
	return fmt.Sprintf("1700000000.%06d", len(f.calls)), nil
}

func (f *fakeTeamChannel) appendStream(channel, ts string, chunks []map[string]any) error {
	data, _ := json.Marshal(chunks)
	f.calls = append(f.calls, "appendStream "+channel+" "+ts+" "+string(data))
	if f.streamErr != "" {
		return teamSlackError{f.streamErr}
	}
	return nil
}

func (f *fakeTeamChannel) stopStream(channel, ts string) error {
	f.calls = append(f.calls, "stopStream "+channel+" "+ts)
	return nil
}

func (f *fakeTeamChannel) permalink(channel, ts string) (string, error) {
	return "https://slack.example.com/archives/" + channel + "/p" + strings.ReplaceAll(ts, ".", ""), nil
}

func (f *fakeTeamChannel) dm(user, text string) (string, string, error) {
	f.calls = append(f.calls, "dm "+user+" "+text)
	return "D" + user, fmt.Sprintf("1700000000.%06d", len(f.calls)), nil
}

func (f *fakeTeamChannel) dmHistory(user, oldest string) ([]slack.Message, error) {
	var out []slack.Message
	for _, message := range f.dms[user] {
		if teamTSBefore(oldest, message.Timestamp) {
			out = append(out, message)
		}
	}
	return out, nil
}

func (f *fakeTeamChannel) inChannel(channel, user string) bool {
	return user == "U1" || user == "U2" || user == "U07ANNA01"
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
	// hi's copies of the app's chats aren't new to the lead: it was there.
	teamAppendChannel(dir, []teamChannelMessage{
		{TS: teamSlackTS(time.Now()), Name: "team-payments", Bot: true, Text: "*Bo*, in the app: make it red", Web: &teamChannelWeb{Conversation: "w-1", From: "person"}},
		{TS: teamSlackTS(time.Now().Add(time.Second)), User: "U2", Name: "Bo", Text: "and bigger"},
	})
	out.Reset()
	if err := runTeamChannel([]string{"--new"}, &out); err != nil || strings.Contains(out.String(), "make it red") || !strings.Contains(out.String(), "and bigger") {
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

func TestTeamMemorySettings(t *testing.T) {
	setupTeamTest(t)
	if ip := teamMemoryIP("10.234.76.2"); ip != "10.234.76.200" {
		t.Fatal(ip)
	}
	config := testTeamConfig()
	lead := filepath.Join(teamDir("payments"), "lead")
	os.MkdirAll(filepath.Join(lead, "hindsight"), 0o700)
	// The lead's own tuning stays; the server and the bank are hi's.
	writeSkillTestFile(t, filepath.Join(lead, "hindsight", "config.json"), `{"recall_budget": "high", "api_url": "http://old", "bank_id_template": "x-{user}"}`, 0o600)
	if err := writeTeamLead(config, teamSecrets{}, "10.234.76.2", "10.234.76.200"); err != nil {
		t.Fatal(err)
	}
	var settings map[string]any
	data, _ := os.ReadFile(filepath.Join(lead, "hindsight", "config.json"))
	json.Unmarshal(data, &settings)
	if settings["mode"] != "local_external" || settings["api_url"] != "http://10.234.76.200:8888" || settings["bank_id"] != "team-payments" ||
		settings["recall_budget"] != "high" || settings["bank_id_template"] != nil {
		t.Fatalf("%v", settings)
	}
	yaml, _ := os.ReadFile(filepath.Join(lead, "config.yaml"))
	for _, want := range []string{"memory:\n  provider: hindsight", "  enabled:\n    - hi-team\n    - hindsight", "gateway_restart_notification: false", "      dm_top_level_threads_as_sessions: false"} {
		if !strings.Contains(string(yaml), want) {
			t.Fatalf("config.yaml lacks %q:\n%s", want, yaml)
		}
	}
	// The plugin that gives the channel /hi is in the lead's home.
	if plugin, err := os.ReadFile(filepath.Join(lead, "plugins", "hi-team", "__init__.py")); err != nil || !strings.Contains(string(plugin), `"team", "web", "link", "--user", user`) ||
		!fileExists(filepath.Join(lead, "plugins", "hi-team", "plugin.yaml")) {
		t.Fatal("no /hi plugin:", err)
	}
	// Without the server, the lead keeps Hermes' built-in memory.
	writeTeamLead(config, teamSecrets{}, "10.234.76.2", "")
	if yaml, _ = os.ReadFile(filepath.Join(lead, "config.yaml")); strings.Contains(string(yaml), "hindsight") {
		t.Fatal(string(yaml))
	}
	config.Memory = "off"
	if _, ok := teamPrepareMemory(boxEngine{}, config, hermesRuntime{}, io.Discard); ok {
		t.Fatal("memory off still prepares Hindsight")
	}

	writeSkillTestFile(t, filepath.Join(hermesInstallDir(), "plugin-catalog", "hindsight.yaml"),
		"name: hindsight\nrepo: https://github.com/vectorize-io/hindsight\nsha: d56c4acdf59c41957613d399094cdf8c489b060c\nsubdir: hindsight-integrations/hermes\n", 0o644)
	catalog, err := readTeamHindsightCatalog()
	if err != nil || catalog.Subdir != "hindsight-integrations/hermes" || catalog.SHA != "d56c4acdf59c41957613d399094cdf8c489b060c" {
		t.Fatal(catalog, err)
	}
}
