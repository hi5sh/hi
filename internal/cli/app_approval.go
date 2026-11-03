package cli

import "time"

// appWeek is how far back the web chats, the help's questions, and hi
// team say's messages are kept for the lead.
const appWeek = 7 * 24 * time.Hour

// appApproval is who said yes to a move of production, a copy of its
// data, or another step only the owners take: an owner on the dev page, or
// someone on the machine. Records from before hi 2.4 hold a Slack
// message's id and text too, from when the yes was given in the channel.
type appApproval struct {
	TS   string `json:"ts,omitempty"`
	Name string `json:"name"`
	Text string `json:"text,omitempty"`
	// Thread is the Slack thread a yes was given in, before hi 2.4.
	Thread string `json:"thread,omitempty"`
}

// appChannelFromBot says whether a logged message is a bot's; lines
// logged before hi marked bots are known by the name the lead had then.
func appChannelFromBot(config appConfig, message appChannelMessage) bool {
	return message.Bot || (message.User == "" && message.Name != "") || message.Name == "team-"+config.Name
}
