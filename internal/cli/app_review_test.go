package cli

import (
	"encoding/json"
	"os"
	"testing"
	"time"
)

// appTestAskReview logs the lead's offer of a deep review in a thread,
// naming a figure, and someone's yes; it returns the yes's id and the
// thread.
func appTestAskReview(t *testing.T, user, question string, minutesAgo int) (string, string) {
	t.Helper()
	now := time.Now()
	ts := func(seconds int) string {
		return appSlackTS(now.Add(-time.Duration(minutesAgo)*time.Minute + time.Duration(seconds)*time.Second))
	}
	root, asked, yes := ts(0), ts(1), ts(2)
	os.MkdirAll(appChannelDir("payments"), 0o700)
	appAppendChannel(appChannelDir("payments"), []appChannelMessage{
		{TS: root, User: user, Name: user, Text: "We changed how people sign in. Is it safe?"},
		{TS: asked, Thread: root, Name: "team-payments", Bot: true, Text: question},
		{TS: yes, Thread: root, User: user, Name: user, Text: "Yes, go ahead"},
	})
	return yes, root
}

// appTestReviewState writes hi agent review's record of a review, as
// the review's own process would.
func appTestReviewState(t *testing.T, rv review, report string) {
	t.Helper()
	os.MkdirAll(reviewStateFile(rv.ID), 0o700)
	if err := saveReview(&rv); err != nil {
		t.Fatal(err)
	}
	if report != "" {
		os.WriteFile(reviewStateFile(rv.ID, "report.md"), []byte(report), 0o600)
		data, _ := json.Marshal(rv)
		os.WriteFile(reviewStateFile(rv.ID, "report.json"), data, 0o600)
	}
}
