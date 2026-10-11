package main

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// A new piece of work starts only once the people said yes. Asking and
// starting in one reply is easy for a model to slip into, so the broker
// checks: a task that doesn't continue another names the message that
// approved it, and that message must be in the channel's log, from one of
// the team's people, in reply to the lead (after one of its messages in the
// same thread). The log comes from Slack through the host, so the lead
// can't write one. A small fix may start without it (--small), and the
// broker says so in the channel.

const teamApprovalMaxAge = 7 * 24 * time.Hour

// teamApproval is the message a task started on.
type teamApproval struct {
	TS   string `json:"ts"`
	Name string `json:"name"`
	Text string `json:"text"`
}

// teamFindApproval checks ref (a message's ID from hi team channel --ids)
// against the channel's log, polling Slack first when it isn't logged yet.
func (b *teamBroker) teamFindApproval(config teamConfig, ref string) (teamApproval, error) {
	ref = strings.Trim(strings.TrimSpace(ref), "[]")
	if ref == "" {
		return teamApproval{}, errors.New("name the message that approved this work: hi team task <role> <brief.md> --approval <id>, " +
			"with the id from hi team channel --ids. Ask the people first, end your reply, and start the task once one of them says yes. " +
			"A small fix may start without asking: --small")
	}
	approval, err := teamCheckApproval(config, ref, time.Now())
	if err != nil && b.channel != nil {
		// Said a moment ago: the log may be up to two minutes behind.
		if pollErr := teamPollChannel(b.team, config.Channel, b.channel, time.Now()); pollErr == nil {
			approval, err = teamCheckApproval(config, ref, time.Now())
		}
	}
	return approval, err
}

func teamCheckApproval(config teamConfig, ref string, now time.Time) (teamApproval, error) {
	messages, err := readTeamChannel(teamChannelDir(config.Name), teamSlackTS(now.Add(-teamApprovalMaxAge)))
	if err != nil {
		return teamApproval{}, err
	}
	var found *teamChannelMessage
	for i := range messages {
		if messages[i].TS == ref {
			found = &messages[i]
		}
	}
	if found == nil {
		return teamApproval{}, fmt.Errorf("no message %s in the channel in the last week; hi team channel --ids shows each message's id", ref)
	}
	if teamChannelFromBot(config, *found) {
		return teamApproval{}, fmt.Errorf("message %s is a bot's (yours?); the approval must come from one of the people", ref)
	}
	allowed := append(append([]string{}, config.Owners...), config.Askers...)
	if len(config.Askers) > 0 && !containsString(allowed, found.User) {
		return teamApproval{}, fmt.Errorf("%s isn't one of the team's people who may ask for work", found.Name)
	}
	// It must answer the lead: after a message of the lead's in the same
	// thread, not the request that started it.
	root := firstNonEmpty(found.Thread, found.TS)
	answered := false
	for _, message := range messages {
		if !teamTSBefore(message.TS, found.TS) || !teamChannelFromBot(config, message) {
			continue
		}
		if firstNonEmpty(message.Thread, message.TS) == root || (found.Thread == "" && message.Thread == "") {
			answered = true
		}
	}
	if !answered {
		return teamApproval{}, fmt.Errorf("message %s from %s doesn't answer you: approval is a reply after you asked, in the same thread. Ask, end your reply, and wait for theirs", ref, found.Name)
	}
	return teamApproval{TS: found.TS, Name: found.Name, Text: qClip(found.Text)}, nil
}

// teamChannelFromBot says whether a logged message is a bot's; lines
// logged before hi marked bots are known by the lead's name.
func teamChannelFromBot(config teamConfig, message teamChannelMessage) bool {
	return message.Bot || (message.User == "" && message.Name != "") || message.Name == teamBotName(config.Name)
}
