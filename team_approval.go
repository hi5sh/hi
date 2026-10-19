package main

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/slack-go/slack"
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
	// Thread is the Slack thread the yes was given in: a reply's thread in
	// the channel, or a web chat's copy there; empty for a yes outside a
	// thread or in a DM.
	Thread string `json:"thread,omitempty"`
}

// teamFindApproval checks ref (a message's ID from hi team channel --ids)
// against the channel's log, polling Slack first when it isn't logged yet.
// For a release, a rollback, production's helper, a package's install
// script (trust), or production's data from the preview (data), only an
// owner's yes counts: production is theirs. A yes in the app's chat (w-3.4) may start
// work, but never those: its name is only claimed. A release or setting dev back ("dev") needs a yes
// of its own, never one that approved something else; one yes may start
// more than one task.
func (b *teamBroker) teamFindApproval(config teamConfig, ref, approving string) (teamApproval, error) {
	owners := approving == "release" || approving == "rollback" || approving == "helper" || approving == "trust" || approving == "data" || approving == "review" || approving == "propose"
	ref = strings.Trim(strings.TrimSpace(ref), "[]")
	if ref == "" {
		return teamApproval{}, errors.New("name the message that approved this work: hi team task <role> <brief.md> --approval <id>, " +
			"with the id from hi team channel --ids. Ask the people first, end your reply, and start the task once one of them says yes. " +
			"A small fix may start without asking: --small")
	}
	if strings.HasPrefix(ref, "dm:") {
		approval, err := b.teamCheckDMApproval(config, ref, owners, time.Now())
		if err != nil {
			return approval, err
		}
		return b.unusedApproval(approval, approving)
	}
	if teamSayApproval.MatchString(ref) {
		approval, err := teamCheckSayApproval(config, ref, owners, time.Now())
		if err != nil {
			return approval, err
		}
		return b.unusedApproval(approval, approving)
	}
	if teamWebMessageID.MatchString(ref) {
		approval, err := teamCheckWebApproval(config, ref, owners, time.Now())
		if err != nil {
			return approval, err
		}
		return b.unusedApproval(approval, approving)
	}
	approval, err := teamCheckApproval(config, ref, owners, time.Now())
	if err != nil && b.channel != nil {
		// Said a moment ago: the log may be up to two minutes behind.
		if pollErr := teamPollChannel(b.team, config.Channel, b.channel, time.Now()); pollErr == nil {
			approval, err = teamCheckApproval(config, ref, owners, time.Now())
		}
	}
	if err != nil {
		return approval, err
	}
	return b.unusedApproval(approval, approving)
}

// teamFindThread is the Slack thread of a message in the channel's log,
// by its id from hi team channel --ids: the thread it is in, or its own.
func (b *teamBroker) teamFindThread(config teamConfig, ref string) (string, error) {
	ref = strings.Trim(strings.TrimSpace(ref), "[]")
	if !teamSlackTSPattern.MatchString(ref) {
		return "", fmt.Errorf("--in takes the id of the message the fix was asked for in, from hi team channel --ids, not %q", ref)
	}
	find := func() (string, bool) {
		messages, _ := readTeamChannel(teamChannelDir(b.team), teamSlackTS(teamTSTime(ref).Add(-time.Second)))
		for _, message := range messages {
			if message.TS == ref {
				return firstNonEmpty(message.Thread, message.TS), true
			}
		}
		return "", false
	}
	if thread, ok := find(); ok {
		return thread, nil
	}
	if b.channel != nil {
		// Said a moment ago: the log may be up to two minutes behind.
		if err := teamPollChannel(b.team, config.Channel, b.channel, time.Now()); err == nil {
			if thread, ok := find(); ok {
				return thread, nil
			}
		}
	}
	return "", fmt.Errorf("no message %s in the channel; hi team channel --ids lists them", ref)
}

// unusedApproval refuses a yes that already approved something else, when
// this work needs a yes of its own.
func (b *teamBroker) unusedApproval(approval teamApproval, approving string) (teamApproval, error) {
	used := map[string]bool{}
	if approving != "task" {
		for _, task := range loadTeamTasks(b.team) {
			if task.Approval != nil {
				used[task.Approval.TS] = true
			}
			if task.TrustApproval != nil {
				used[task.TrustApproval.TS] = true
			}
		}
	}
	for _, review := range loadTeamDeepReviews(b.team) {
		if review.Approval != nil {
			used[review.Approval.TS] = true
		}
	}
	if versions, err := loadTeamVersions(b.team); err == nil {
		for _, event := range versions.History {
			if event.Approval != nil {
				used[event.Approval.TS] = true
			}
		}
	}
	if used[approval.TS] {
		return teamApproval{}, fmt.Errorf("message %s already approved something else; ask again for this", approval.TS)
	}
	return approval, nil
}

func teamCheckApproval(config teamConfig, ref string, owners bool, now time.Time) (teamApproval, error) {
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
	if owners && !containsString(config.Owners, found.User) {
		return teamApproval{}, fmt.Errorf("%s isn't one of the team's owners (%s); only an owner may approve this", found.Name, strings.Join(config.Owners, ", "))
	}
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
	return teamApproval{TS: found.TS, Name: found.Name, Text: qClip(found.Text), Thread: found.Thread}, nil
}

// teamChannelFromBot says whether a logged message is a bot's; lines
// logged before hi marked bots are known by the name the lead had then.
func teamChannelFromBot(config teamConfig, message teamChannelMessage) bool {
	return message.Bot || (message.User == "" && message.Name != "") || message.Name == "team-"+config.Name
}

// teamDMApproval is a yes in someone's DM with the lead: dm:<member>, or
// dm:<member>:<ts> for one message.
var teamDMApproval = regexp.MustCompile(`^dm:([A-Z0-9]{2,20})(?::([0-9]{9,11}\.[0-9]{6}))?$`)

// teamCheckDMApproval checks a yes given in a DM with Slack: the person's
// newest message there after one of the bot's, within a week (or the one
// named). The lead only says whose DM it was.
func (b *teamBroker) teamCheckDMApproval(config teamConfig, ref string, owners bool, now time.Time) (teamApproval, error) {
	if owners {
		return teamApproval{}, fmt.Errorf("%s is from a DM; this needs an owner's yes in the channel, where the team sees it", ref)
	}
	match := teamDMApproval.FindStringSubmatch(ref)
	if match == nil {
		return teamApproval{}, fmt.Errorf("a yes in a DM is --approval dm (in that DM's conversation) or dm:<their member ID>, not %q", ref)
	}
	user, want := match[1], match[2]
	if b.channel == nil {
		return teamApproval{}, errors.New("hi isn't connected to Slack, so it can't check a DM")
	}
	if !b.teamMayAsk(config, user) {
		return teamApproval{}, fmt.Errorf("%s isn't one of the people who may ask for work", user)
	}
	messages, err := b.channel.dmHistory(user, teamSlackTS(now.Add(-teamApprovalMaxAge)))
	if err != nil {
		return teamApproval{}, fmt.Errorf("Slack wouldn't show the DM (%v); the team's Slack app needs the im:history scope, from hi team manifest %s", err, config.Name)
	}
	var found *slack.Message
	asked := false
	mirrored := teamWebMirrored(config.Name) // the app's chats, copied into the DM
	for i := range messages {
		message := &messages[i]
		switch {
		case mirrored[message.Timestamp].Conversation != "":
		case message.BotID != "" || message.SubType == "bot_message":
			asked = true
		case message.User == user && message.SubType == "" && asked:
			if want == "" || message.Timestamp == want {
				found = message
			}
		}
	}
	if found == nil {
		if want != "" {
			return teamApproval{}, fmt.Errorf("%s isn't a message of theirs after one of yours in the DM, within a week", ref)
		}
		return teamApproval{}, errors.New("no message of theirs in the DM after one of yours, within a week: approval is a reply after you asked. Ask, end your reply, and wait for theirs")
	}
	name := user
	if b.channel != nil {
		name = firstNonEmpty(b.channel.userName(user), user)
	}
	return teamApproval{TS: "dm:" + user + ":" + found.Timestamp, Name: name, Text: qClip(found.Text)}, nil
}
