package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The admin page sets the server's Slack app up as hi server slack setup
// does: check reads the workspace and channels, the setup keeps the tokens
// and the channel, and later empty tokens keep the ones there are.
func TestServerSlackSetupFromThePage(t *testing.T) {
	ts := newTestServer(t)
	checked := []slackConfig{}
	setupCheck, whoAmI, channels := slackSetupCheck, slackWhoAmI, adminSlackChannels
	t.Cleanup(func() { slackSetupCheck, slackWhoAmI, adminSlackChannels = setupCheck, whoAmI, channels })
	slackSetupCheck = func(config slackConfig) (string, string, error) {
		checked = append(checked, config)
		return "Demo", "hi-server", nil
	}
	slackWhoAmI = func(bot string) (string, string, error) { return "Demo", "hi-server", nil }
	adminSlackChannels = func(bot string) ([]adminSlackChannel, error) {
		return []adminSlackChannel{{ID: "C1", Name: "approvals", Private: true}}, nil
	}
	handler := ts.server.adminHandler()
	post := func(path string, body slackSetupInput) (int, map[string]any) {
		data, _ := json.Marshal(body)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, path, strings.NewReader(string(data))))
		var answer map[string]any
		json.Unmarshal(recorder.Body.Bytes(), &answer)
		return recorder.Code, answer
	}
	if code, _ := post("/admin/slack/check", slackSetupInput{AppToken: "xoxb-wrong", BotToken: "xoxb-1"}); code != http.StatusBadRequest {
		t.Fatalf("a bot token as the app's: %d", code)
	}
	if code, _ := post("/admin/slack/check", slackSetupInput{BotToken: "xoxb-1"}); code != http.StatusBadRequest {
		t.Fatalf("no app token, nor one kept: %d", code)
	}
	code, answer := post("/admin/slack/check", slackSetupInput{AppToken: "xapp-1", BotToken: "xoxb-1"})
	if code != http.StatusOK || answer["team"] != "Demo" || len(answer["channels"].([]any)) != 1 {
		t.Fatalf("check: %d %v", code, answer)
	}
	if code, _ := post("/admin/slack", slackSetupInput{As: "olga", AppToken: "xapp-1", BotToken: "xoxb-1"}); code != http.StatusBadRequest {
		t.Fatalf("no channel: %d", code)
	}
	if code, answer := post("/admin/slack", slackSetupInput{As: "olga", AppToken: "xapp-1", BotToken: "xoxb-1", Channel: "C1"}); code != http.StatusOK || !strings.Contains(answer["text"].(string), "Demo") {
		t.Fatalf("set up: %d %v", code, answer)
	}
	config, err := loadSlackConfig(ts.server.dir)
	if err != nil || config == nil || config.AppToken != "xapp-1" || config.BotToken != "xoxb-1" || config.Channel != "C1" {
		t.Fatalf("kept: %+v %v", config, err)
	}
	// Another channel, the tokens kept.
	if code, answer := post("/admin/slack", slackSetupInput{As: "olga", Channel: "C2"}); code != http.StatusOK || answer["restart"] != true {
		t.Fatalf("change the channel: %d %v", code, answer)
	}
	if last := checked[len(checked)-1]; last.AppToken != "xapp-1" || last.BotToken != "xoxb-1" || last.Channel != "C2" {
		t.Fatalf("checked with: %+v", last)
	}
	if config, _ := loadSlackConfig(ts.server.dir); config.Channel != "C2" {
		t.Fatalf("channel: %s", config.Channel)
	}
}
