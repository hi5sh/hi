package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestTeamSay(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	channel := &fakeTeamChannel{}
	b.channel = channel
	var input string
	tries := 0
	teamSayRetry = time.Millisecond
	b.lead = func(_ context.Context, conversation, text string, _ []teamLeadPicture, _ func(string)) (string, error) {
		// A lead that is still starting is tried again.
		if tries++; tries == 1 {
			return "", errTeamLeadUnreachable
		}
		input = conversation + "\n" + text
		return "On it.", nil
	}

	if _, err := b.say(teamSayRequest{Text: "  "}); err == nil {
		t.Fatal("an empty message was sent")
	}
	// Posted in the channel, answered in its thread, kept.
	said, err := b.say(teamSayRequest{Text: "Update the docs <now>", Who: "admin", Wait: true})
	if err != nil || said.Answer != "On it." || said.Who != "admin on the machine" {
		t.Fatalf("%+v %v", said, err)
	}
	if channel.calls[0] != "post C07PAYMENTS :desktop_computer: *admin on the machine* to the lead, with hi team say:\n> Update the docs &lt;now&gt;" ||
		channel.calls[1] != "postIn C07PAYMENTS "+said.TS+" On it." {
		t.Fatalf("%q", channel.calls)
	}
	if !strings.HasPrefix(input, "say-"+said.TS+"\n[hi team say · message say:"+said.TS+" · admin on the machine") || !strings.Contains(input, "hi team task add") {
		t.Fatal(input)
	}
}

// A lead that takes long says so in the thread, once, before its answer.
func TestTeamSayWorking(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	channel := &fakeTeamChannel{}
	b.channel = channel
	teamSayWorkingAfter = 10 * time.Millisecond
	t.Cleanup(func() { teamSayWorkingAfter = 2 * time.Minute })
	b.lead = func(context.Context, string, string, []teamLeadPicture, func(string)) (string, error) {
		time.Sleep(100 * time.Millisecond)
		return "Done.", nil
	}
	said, err := b.say(teamSayRequest{Text: "Look into it", Wait: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(channel.calls) != 3 || channel.calls[1] != "postIn C07PAYMENTS "+said.TS+" The lead is still working on this; its answer comes in this thread." || channel.calls[2] != "postIn C07PAYMENTS "+said.TS+" Done." {
		t.Fatalf("%q", channel.calls)
	}
}
