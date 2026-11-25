package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// The app says things in Slack through hi: a report, a reminder, an alert
// for its people. Its code posts to $HI_SAY_URL, at the models relay in
// its box (127.0.0.1, so no browser reaches it), and the broker sends the
// message with the app's own Slack bot, so no app holds a Slack token.
// It goes to the app's channel, or as a DM to someone in the app: one with
// a level in it, or in its channel. Production sends; the preview answers
// what it would have sent and sends nothing, so trying a report on the
// preview never reaches anyone.
//
// Spec: docs/specs/approved/hi_app_say.md.

const (
	// appAppSayPath is where the slot's socket takes a message.
	appAppSayPath = "/hi/say"
	// appAppSayMaxText is the longest message: Slack cuts a message's text
	// at 40,000 characters.
	appAppSayMaxText = 30000
	// appAppSayPerHour is how many messages a slot sends in an hour, so an
	// app that loops doesn't flood the channel or someone's DMs.
	appAppSayPerHour = 60
)

// appAppSayURL is $HI_SAY_URL in an app's box.
var appAppSayURL = fmt.Sprintf("http://127.0.0.1:%d%s", appBoxModelsPort, appAppSayPath)

// appAppSayRequest is what the app posts.
type appAppSayRequest struct {
	Text string `json:"text"`
	// To is "channel" (or empty), the app's channel, or a Slack member ID
	// for a DM.
	To string `json:"to,omitempty"`
	// Thread is a message in the channel to answer in.
	Thread string `json:"thread,omitempty"`
}

// appAppSayAnswer is what the app gets back.
type appAppSayAnswer struct {
	Sent    bool   `json:"sent"`
	To      string `json:"to"`
	Channel string `json:"channel,omitempty"`
	TS      string `json:"ts,omitempty"`
	Note    string `json:"note,omitempty"`
	Error   string `json:"error,omitempty"`
}

// appSayRule is in the dev box's instructions: how the app's code sends
// Slack messages.
const appSayRule = "Messages from the app to Slack (a scheduled report, a reminder, an alert for its people) go through hi, never a Slack token of the app's own: " +
	`the app POSTs {"text": "...", "to": "channel" | "<Slack member ID>", "thread": "<ts>"} as JSON to $HI_SAY_URL, ` +
	"and hi sends it from the app's Slack bot, in the app's channel or as a DM to someone who is in the channel or has a level in the app. " +
	`The answer is {"sent": true, "channel": "...", "ts": "..."}; ts is what a later "thread" takes. Text is Slack's mrkdwn, up to 30,000 characters; at most 60 messages an hour. ` +
	"Production sends. The preview sends nothing and answers {\"sent\": false, \"note\": ...} with what it would have sent, so a report can be tried on the preview without reaching anyone; the code treats that as success. " +
	"Without $HI_SAY_URL, as in the dev box and the tests, the code logs the message instead of sending it. " +
	"Record what was sent (the ts, per report and date), so a restart never sends the same report twice. " +
	"hi dev say is the dev box's own post in the channel, not the app's."

// appSayHandler takes the slot's messages on its socket.
func (b *appBroker) appSayHandler(slot string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		answer, status := b.appSay(slot, r)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(answer)
	})
}

func (b *appBroker) appSay(slot string, r *http.Request) (appAppSayAnswer, int) {
	refuse := func(status int, format string, args ...any) (appAppSayAnswer, int) {
		return appAppSayAnswer{Error: "hi: " + fmt.Sprintf(format, args...)}, status
	}
	if r.Method != http.MethodPost {
		return refuse(http.StatusMethodNotAllowed, `POST {"text": "...", "to": "channel" | "<Slack member ID>"} as JSON`)
	}
	var request appAppSayRequest
	data, err := io.ReadAll(io.LimitReader(r.Body, 4*appAppSayMaxText))
	if err != nil {
		return refuse(http.StatusBadRequest, "%v", err)
	}
	if err := json.Unmarshal(data, &request); err != nil {
		return refuse(http.StatusBadRequest, `the body isn't JSON like {"text": "...", "to": "channel"}: %v`, err)
	}
	text := strings.TrimSpace(request.Text)
	to := strings.TrimSpace(request.To)
	if to == "" {
		to = "channel"
	}
	thread := strings.TrimSpace(request.Thread)
	switch {
	case text == "":
		return refuse(http.StatusBadRequest, "say something: text is empty")
	case len(text) > appAppSayMaxText:
		return refuse(http.StatusBadRequest, "the message is longer than %d characters", appAppSayMaxText)
	case to != "channel" && !slackIDPattern.MatchString(to):
		return refuse(http.StatusBadRequest, `to is "channel" or a Slack member ID (such as U0123ABCD), not %q`, to)
	case to != "channel" && thread != "":
		return refuse(http.StatusBadRequest, "thread is for the channel; a DM starts on its own")
	case thread != "" && !appSlackTSPattern.MatchString(thread):
		return refuse(http.StatusBadRequest, "thread is a message's ts, such as 1792845304.432739, not %q", thread)
	}
	config, err := loadTeam(b.team)
	if err != nil {
		return refuse(http.StatusInternalServerError, "%v", err)
	}
	if b.channel == nil {
		return refuse(http.StatusServiceUnavailable, "hi isn't connected to Slack, so nothing was sent")
	}
	if to != "channel" && appLevel(config, to) == appLevelNone && !b.channel.inChannel(config.Channel, to) {
		return refuse(http.StatusForbidden, "%s isn't in the app's channel and has no level in the app, so the app doesn't DM them; an owner adds them on the dev page, or invites them to the channel", to)
	}
	where := "the app's channel"
	if to != "channel" {
		where = "a DM to " + firstNonEmpty(b.channel.userName(to), to)
	}
	if slot != appSlotProduction {
		return appAppSayAnswer{To: to, Note: "the preview doesn't send to Slack; production would have sent this as " + where}, http.StatusOK
	}
	if !b.appSayAllow(slot, time.Now()) {
		return refuse(http.StatusTooManyRequests, "the app sent %d messages in the last hour, which is its most; nothing was sent", appAppSayPerHour)
	}
	answer := appAppSayAnswer{Sent: true, To: to}
	switch {
	case to != "channel":
		answer.Channel, answer.TS, err = b.channel.dm(to, text)
	case thread != "":
		answer.Channel = config.Channel
		answer.TS, err = b.channel.postIn(config.Channel, thread, text)
	default:
		answer.Channel = config.Channel
		answer.TS, err = b.channel.post(config.Channel, text)
	}
	if err != nil {
		return refuse(http.StatusBadGateway, "Slack wouldn't take the message for %s: %v", where, err)
	}
	return answer, http.StatusOK
}

// appSayAllow counts a slot's message against its hour.
func (b *appBroker) appSayAllow(slot string, now time.Time) bool {
	b.appSayMu.Lock()
	defer b.appSayMu.Unlock()
	if b.appSaySent == nil {
		b.appSaySent = map[string][]time.Time{}
	}
	var recent []time.Time
	for _, at := range b.appSaySent[slot] {
		if now.Sub(at) < time.Hour {
			recent = append(recent, at)
		}
	}
	if len(recent) >= appAppSayPerHour {
		b.appSaySent[slot] = recent
		return false
	}
	b.appSaySent[slot] = append(recent, now)
	return true
}
