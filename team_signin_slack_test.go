package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestTeamSigninFromSlackIsForOwners(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	b.channel = &fakeTeamChannel{}
	if _, err := b.webSignin("not-an-id", "chatgpt"); err == nil {
		t.Fatal("took a bad ID")
	}
	if _, err := b.webSignin("U07OWNER1", "bedrock"); err == nil || !strings.Contains(err.Error(), "/hi-payments signin chatgpt") {
		t.Fatal(err)
	}
	if _, err := b.webSignin("U07ANNA01", "chatgpt"); err == nil || !strings.Contains(err.Error(), "only the team's owners") || !strings.Contains(err.Error(), "<@U07OWNER1>") {
		t.Fatal(err)
	}
}

func TestTeamSigninChatGPTFromSlack(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	channel := &fakeTeamChannel{}
	b.channel = channel
	saveTeamSecrets("payments", teamSecrets{SlackBotToken: "xoxb"})
	issuer := &fakeChatGPTIssuer{}
	issuer.start(t)
	signedIn := make(chan struct{})
	saved := chatGPTDeviceWaitFor
	chatGPTDeviceWaitFor = func(device chatGPTDevice, now func() time.Time) (teamChatGPTGrant, error) {
		defer close(signedIn)
		device.Interval = time.Millisecond
		return chatGPTDeviceWait(device, now)
	}
	t.Cleanup(func() { chatGPTDeviceWaitFor = saved })
	line, err := b.webSignin("U07OWNER1", "chatgpt")
	if err != nil || !strings.Contains(line, "a code in a DM") {
		t.Fatal(line, err)
	}
	// One at a time.
	if _, err := b.webSignin("U07OWNER1", "chatgpt"); err == nil || !strings.Contains(err.Error(), "already waiting") {
		t.Fatal(err)
	}
	if dm := channel.calls[0]; !strings.HasPrefix(dm, "dm U07OWNER1 ") || !strings.Contains(dm, "/codex/device>") || !strings.Contains(dm, "*ABCD-1234*") {
		t.Fatal(dm)
	}
	<-signedIn
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(strings.Join(channel.calls, "\n"), "signed in to ChatGPT") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if last := channel.calls[len(channel.calls)-1]; !strings.HasPrefix(last, "update DU07OWNER1 ") || !strings.Contains(last, "ChatGPT Pro plan") {
		t.Fatal(channel.calls)
	}
	secrets, _ := loadTeamSecrets("payments")
	if secrets.ChatGPT == nil || secrets.ChatGPT.RefreshToken != "rt-authorization_code-0" || secrets.SlackBotToken != "xoxb" {
		t.Fatalf("%+v", secrets)
	}
	// Done: another may start.
	b.signins.mu.Lock()
	waiting := !b.signins.chatgpt.IsZero()
	b.signins.mu.Unlock()
	if waiting {
		t.Fatal("the sign-in still waits")
	}
}

func TestTeamSigninClaudeFromSlack(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	channel := &fakeTeamChannel{}
	b.channel = channel
	saveTeamSecrets("payments", teamSecrets{SlackBotToken: "xoxb"})
	b.exposed = func(name string) (netExposeState, bool) { return netExposeState{}, name == "team-payments" }
	// No address yet: the machine, then.
	if _, err := b.webSignin("U07OWNER1", "claude"); err == nil || !strings.Contains(err.Error(), "hi team signin payments claude") {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Join(teamDir("payments"), "run"), 0o700)
	saveTeamSlot("payments", teamPreview{Slot: teamSlotProduction, URL: "https://payments.example.net/?hi_key=K1", Expose: "team-payments"})
	if _, err := b.webSignin("U07OWNER1", "claude"); err != nil {
		t.Fatal(err)
	}
	dm := channel.calls[len(channel.calls)-1]
	link := regexp.MustCompile(`<(https://payments\.example\.net/__hi/signin/[A-Za-z0-9_-]{43})\|`).FindStringSubmatch(dm)
	if link == nil || !strings.HasPrefix(dm, "dm U07OWNER1 ") || !strings.Contains(dm, "claude setup-token") {
		t.Fatal(dm)
	}
	server := httptest.NewServer(b.webHandler(teamSlotProduction))
	defer server.Close()
	page := server.URL + strings.TrimPrefix(link[1], "https://payments.example.net")
	get := func() (int, string) {
		response, err := http.Get(page)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		data, _ := io.ReadAll(response.Body)
		return response.StatusCode, string(data)
	}
	post := func(token string) (int, string) {
		response, err := http.PostForm(page, url.Values{"token": {token}})
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		data, _ := io.ReadAll(response.Body)
		return response.StatusCode, string(data)
	}
	if status, body := get(); status != 200 || !strings.Contains(body, `type="password"`) {
		t.Fatal(status, body)
	}
	if status, body := post("hello"); status != 400 || !strings.Contains(body, "isn't a Claude token") {
		t.Fatal(status, body)
	}
	if status, body := post("sk-ant-oat01-from-the-page"); status != 200 || !strings.Contains(body, "Saved") {
		t.Fatal(status, body)
	}
	secrets, _ := loadTeamSecrets("payments")
	if secrets.ClaudeToken != "sk-ant-oat01-from-the-page" || secrets.ClaudeSignedIn.IsZero() || secrets.SlackBotToken != "xoxb" ||
		readTestFile(t, filepath.Join(teamKeysDir("payments"), teamClaudeKeyFile)) != "sk-ant-oat01-from-the-page\n" {
		t.Fatalf("%+v", secrets)
	}
	if last := channel.calls[len(channel.calls)-1]; !strings.HasPrefix(last, "dm U07OWNER1 ") || !strings.Contains(last, "new Claude token") {
		t.Fatal(last)
	}
	// Once only.
	if status, _ := get(); status != 404 {
		t.Fatal(status)
	}
	if status, _ := post("sk-ant-oat01-again"); status != 404 {
		t.Fatal(status)
	}
	// A page that ran out has ended.
	b.webSignin("U07OWNER1", "claude")
	b.signins.mu.Lock()
	for id, signin := range b.signins.claude {
		signin.Ends = time.Now().Add(-time.Second)
		b.signins.claude[id] = signin
	}
	b.signins.mu.Unlock()
	link = regexp.MustCompile(`<(https://payments\.example\.net/__hi/signin/[A-Za-z0-9_-]{43})\|`).FindStringSubmatch(channel.calls[len(channel.calls)-1])
	page = server.URL + strings.TrimPrefix(link[1], "https://payments.example.net")
	if status, body := get(); status != 404 || !strings.Contains(body, "/hi-payments signin claude") {
		t.Fatal(status, body)
	}
}
