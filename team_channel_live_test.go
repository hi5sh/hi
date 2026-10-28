package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/slack-go/slack"
)

// The channel's members are its people: the team's own lead, other apps,
// and deactivated accounts are left out, so the dev page's People can't
// make a bot a developer.
func TestLiveTeamChannelMembersArePeople(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/conversations.members":
			w.Write([]byte(`{"ok":true,"members":["U07ANNA01","U07LEADBOT","U07GONE001","U07BO00001"],"response_metadata":{"next_cursor":""}}`))
		case "/users.info":
			user := r.FormValue("user")
			bot, deleted := user == "U07LEADBOT", user == "U07GONE001"
			body, _ := json.Marshal(map[string]any{"ok": true, "user": map[string]any{"id": user, "name": user, "is_bot": bot, "deleted": deleted, "profile": map[string]any{"display_name": user + " name"}}})
			w.Write(body)
		default:
			w.Write([]byte(`{"ok":false,"error":"unknown_method"}`))
		}
	}))
	defer server.Close()
	channel := &liveTeamChannel{api: slack.New("xoxb-test", slack.OptionAPIURL(server.URL+"/")), names: map[string]string{}, bots: map[string]bool{}}
	members, err := channel.members("C07PAYMENTS")
	if err != nil || !slices.Equal(members, []string{"U07ANNA01", "U07BO00001"}) {
		t.Fatal(members, err)
	}
	if channel.userName("U07ANNA01") != "U07ANNA01 name" {
		t.Fatal(channel.names)
	}
}
