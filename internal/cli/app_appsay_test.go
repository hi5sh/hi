package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func appSayPost(t *testing.T, b *appBroker, slot, body string) (appAppSayAnswer, int) {
	t.Helper()
	recorder := httptest.NewRecorder()
	b.appSayHandler(slot).ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, appAppSayPath, strings.NewReader(body)))
	var answer appAppSayAnswer
	if err := json.Unmarshal(recorder.Body.Bytes(), &answer); err != nil {
		t.Fatalf("%s: %v", recorder.Body, err)
	}
	return answer, recorder.Code
}

func TestAppSayProduction(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	channel := &fakeTeamChannel{}
	b.channel = channel

	// The channel, as is: no prefix, so a report reads as the app's.
	answer, code := appSayPost(t, b.appBroker, appSlotProduction, `{"text": "*Morning report*\n3 items"}`)
	if code != http.StatusOK || !answer.Sent || answer.Channel != "C07PAYMENTS" || answer.TS == "" || answer.To != "channel" {
		t.Fatalf("%d %+v", code, answer)
	}
	// Its thread.
	if answer, code := appSayPost(t, b.appBroker, appSlotProduction, `{"text": "Details", "thread": "`+answer.TS+`"}`); code != http.StatusOK || !answer.Sent {
		t.Fatalf("%d %+v", code, answer)
	}
	// A DM to someone in the channel.
	if answer, code := appSayPost(t, b.appBroker, appSlotProduction, `{"text": "Your report", "to": "U07ANNA01"}`); code != http.StatusOK || answer.Channel != "DU07ANNA01" {
		t.Fatalf("%d %+v", code, answer)
	}
	want := []string{"post C07PAYMENTS *Morning report*\n3 items", "postIn C07PAYMENTS 1700000000.000001 Details", "dm U07ANNA01 Your report"}
	if strings.Join(channel.calls, "|") != strings.Join(want, "|") {
		t.Fatalf("%q", channel.calls)
	}

	// Refused, and nothing sent.
	for _, body := range []string{
		`{"text": "  "}`,
		`{"text": "hi", "to": "U99STRANGER"}`, // neither in the channel nor with a level
		`{"text": "hi", "to": "#general"}`,
		`{"text": "hi", "to": "U07ANNA01", "thread": "1700000000.000001"}`,
		`{"text": "hi", "thread": "yesterday"}`,
		`{"text": "` + strings.Repeat("a", appAppSayMaxText+1) + `"}`,
		`not json`,
	} {
		if answer, code := appSayPost(t, b.appBroker, appSlotProduction, body); code == http.StatusOK || answer.Sent || !strings.HasPrefix(answer.Error, "hi: ") {
			t.Fatalf("%.60s: %d %+v", body, code, answer)
		}
	}
	if len(channel.calls) != 3 {
		t.Fatalf("%q", channel.calls)
	}
	recorder := httptest.NewRecorder()
	b.appSayHandler(appSlotProduction).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, appAppSayPath, nil))
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatal(recorder.Code)
	}
}

// The preview answers what it would have sent, and sends nothing.
func TestAppSayPreview(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	channel := &fakeTeamChannel{}
	b.channel = channel
	answer, code := appSayPost(t, b.appBroker, appSlotPreview, `{"text": "Your report", "to": "U07ANNA01"}`)
	if code != http.StatusOK || answer.Sent || !strings.Contains(answer.Note, "production would have sent this as a DM to") {
		t.Fatalf("%d %+v", code, answer)
	}
	// A person who couldn't get it is refused on the preview too.
	if _, code := appSayPost(t, b.appBroker, appSlotPreview, `{"text": "hi", "to": "U99STRANGER"}`); code != http.StatusForbidden {
		t.Fatal(code)
	}
	if len(channel.calls) != 0 {
		t.Fatalf("%q", channel.calls)
	}
}

func TestAppSayNoSlack(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	if answer, code := appSayPost(t, b.appBroker, appSlotProduction, `{"text": "hi"}`); code != http.StatusServiceUnavailable || answer.Sent {
		t.Fatalf("%d %+v", code, answer)
	}
}

func TestAppSayHourLimit(t *testing.T) {
	b := &appBroker{}
	now := time.Now()
	for i := 0; i < appAppSayPerHour; i++ {
		if !b.appSayAllow(appSlotProduction, now) {
			t.Fatal(i)
		}
	}
	if b.appSayAllow(appSlotProduction, now) {
		t.Fatal("past the hour's limit")
	}
	if !b.appSayAllow(appSlotProduction, now.Add(time.Hour+time.Second)) {
		t.Fatal("the next hour was refused")
	}
}

// The slot's socket passes /hi/say to the broker and the rest to the
// models proxy.
func TestAppBoxSayURL(t *testing.T) {
	setupTeamTest(t)
	env := appBoxOwnEnv("payments", appSlotProduction, 8000)
	if env["HI_SAY_URL"] != "http://127.0.0.1:3129/hi/say" || !appBoxEnvReserved["HI_SAY_URL"] {
		t.Fatal(env["HI_SAY_URL"])
	}
	if !strings.Contains(appSayRule, "$HI_SAY_URL") {
		t.Fatal(appSayRule)
	}
}
