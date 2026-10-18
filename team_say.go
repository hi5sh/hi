package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// hi team say is how the person who runs hi on the machine talks to a
// team's lead without Slack: hi posts the message in the channel, so the
// team sees it, the lead answers through Hermes' API server as it does
// the app's chat, and hi posts the answer in that message's thread. The
// message is a go-ahead for the work it asks for (--approval say:<ts>),
// as the machine's admin can already change anything; a release, a
// rollback, and production's helper still need an owner's yes in Slack.

// teamSaid is one message sent with hi team say, in run/say.json: what
// the approval is checked against, since the channel only shows the bot.
type teamSaid struct {
	TS     string    `json:"ts"`
	Who    string    `json:"who"`
	Text   string    `json:"text"`
	At     time.Time `json:"at"`
	Answer string    `json:"answer,omitempty"`
	Error  string    `json:"error,omitempty"`
}

type teamSayRequest struct {
	Text string `json:"text"`
	Who  string `json:"who"`
	Wait bool   `json:"wait,omitempty"`
}

const teamSayMaxText = 8000

// teamSayLeadWait is how long a message waits for a lead that isn't up
// yet, trying every teamSayRetry.
var teamSayLeadWait, teamSayRetry = 5 * time.Minute, 10 * time.Second

// teamSayWorkingAfter is when a thread with no answer yet says the lead is
// on it, so nobody asks again while it works: the people in Slack can't
// see a turn through Hermes' API server, and their reply in the thread is
// a conversation of its own.
var teamSayWorkingAfter = 2 * time.Minute

var teamSayApproval = regexp.MustCompile(`^say:([0-9]{9,11}\.[0-9]{6})$`)

func teamSayPath(team string) string { return filepath.Join(teamDir(team), "run", "say.json") }

func loadTeamSaid(team string) []teamSaid {
	var said []teamSaid
	if data, err := os.ReadFile(teamSayPath(team)); err == nil {
		json.Unmarshal(data, &said)
	}
	return said
}

// saveTeamSaid keeps a message, replacing the one with its ts; messages
// older than an approval may be are dropped.
func (b *teamBroker) saveTeamSaid(said teamSaid) error {
	b.sayMu.Lock()
	defer b.sayMu.Unlock()
	var keep []teamSaid
	for _, old := range loadTeamSaid(b.team) {
		if old.TS != said.TS && time.Since(old.At) < teamApprovalMaxAge {
			keep = append(keep, old)
		}
	}
	keep = append(keep, said)
	if err := os.MkdirAll(filepath.Dir(teamSayPath(b.team)), 0o700); err != nil {
		return err
	}
	data, _ := json.MarshalIndent(keep, "", "  ")
	return writeFileAtomic(teamSayPath(b.team), append(data, '\n'), 0o600)
}

// say posts the admin's message in the channel and has the lead answer it
// in its thread; with wait, it returns once the lead has.
func (b *teamBroker) say(request teamSayRequest) (teamSaid, error) {
	text := strings.TrimSpace(request.Text)
	switch {
	case text == "":
		return teamSaid{}, errors.New("say something: hi team say <name> <text|->")
	case len(text) > teamSayMaxText:
		return teamSaid{}, fmt.Errorf("the message is longer than %d characters", teamSayMaxText)
	case b.channel == nil:
		return teamSaid{}, errors.New("hi isn't connected to Slack, so the team wouldn't see the message")
	}
	config, err := loadTeam(b.team)
	if err != nil {
		return teamSaid{}, err
	}
	said := teamSaid{Who: firstNonEmpty(strings.TrimSpace(request.Who), "someone") + " on the machine", Text: text, At: time.Now().UTC()}
	quoted := "> " + strings.ReplaceAll(teamSlackEscape(text), "\n", "\n> ")
	if said.TS, err = b.channel.post(config.Channel, fmt.Sprintf(":desktop_computer: *%s* to the lead, with hi team say:\n%s", teamSlackEscape(said.Who), quoted)); err != nil {
		return teamSaid{}, err
	}
	if err := b.saveTeamSaid(said); err != nil {
		return said, err
	}
	answered := make(chan teamSaid, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), teamLeadTimeout)
		defer cancel()
		// The note never comes after the answer.
		var workingMu sync.Mutex
		answering := false
		working := time.AfterFunc(teamSayWorkingAfter, func() {
			workingMu.Lock()
			defer workingMu.Unlock()
			if !answering {
				b.channel.postIn(config.Channel, said.TS, "The lead is still working on this; its answer comes in this thread.")
			}
		})
		answer, err := b.lead(ctx, "say-"+said.TS, teamSayLeadInput(config, said), nil, nil)
		// A lead whose box is starting, as after hi update, gets a while.
		for start := time.Now(); errors.Is(err, errTeamLeadUnreachable) && time.Since(start) < teamSayLeadWait; {
			select {
			case <-ctx.Done():
			case <-time.After(teamSayRetry):
			}
			answer, err = b.lead(ctx, "say-"+said.TS, teamSayLeadInput(config, said), nil, nil)
		}
		working.Stop()
		workingMu.Lock()
		answering = true
		workingMu.Unlock()
		done := said
		if answer = strings.TrimSpace(answer); err != nil && answer == "" {
			done.Error = err.Error()
			b.channel.postIn(config.Channel, said.TS, "The lead couldn't answer: "+teamSlackEscape(err.Error()))
		} else {
			done.Answer = answer
			b.channel.postIn(config.Channel, said.TS, answer)
		}
		b.saveTeamSaid(done)
		answered <- done
	}()
	if request.Wait {
		return <-answered, nil
	}
	return said, nil
}

// teamSayLeadInput is what the lead reads: who wrote, where its answer
// goes, and what the message may start.
func teamSayLeadInput(config teamConfig, said teamSaid) string {
	return fmt.Sprintf("[hi team say · message say:%s · %s: the person who runs hi on the machine the team runs on, not one of the people in Slack · hi posted it in the channel as message %s, and posts your answer in its thread]\n\n%s\n\n"+
		"(Your answer ends your turn, and nothing happens after it until someone writes again. So when this asks for work, start it first, with hi team task and a short brief, then answer briefly with what you started; when you need to know something first, ask. Nobody sees anything until you answer, so don't read through the code: the member a task goes to does that. "+
		"This message is a go-ahead for the work it asks for: start it with --approval say:%s, and it shows in that thread. A release, a rollback, or production's helper still needs an owner's yes in the channel.)",
		said.TS, said.Who, said.TS, said.Text, said.TS)
}

// teamCheckSayApproval is a go-ahead given with hi team say.
func teamCheckSayApproval(config teamConfig, ref string, owners bool, now time.Time) (teamApproval, error) {
	if owners {
		return teamApproval{}, fmt.Errorf("%s is from hi team say on the machine; this needs an owner's yes in the channel", ref)
	}
	ts := teamSayApproval.FindStringSubmatch(ref)[1]
	for _, said := range loadTeamSaid(config.Name) {
		if said.TS != ts {
			continue
		}
		if now.Sub(said.At) > teamApprovalMaxAge {
			return teamApproval{}, fmt.Errorf("message %s is more than a week old; ask again", ref)
		}
		return teamApproval{TS: ref, Name: said.Who, Text: qClip(said.Text), Thread: said.TS}, nil
	}
	return teamApproval{}, fmt.Errorf("no message %s from hi team say in the last week", ref)
}

// teamSayCommand is hi team say <name> <text|-> [--wait] on the machine.
func teamSayCommand(args []string, stdin io.Reader, stdout io.Writer) error {
	var wait bool
	var words []string
	for _, arg := range args {
		switch {
		case arg == "--wait":
			wait = true
		case arg != "-" && strings.HasPrefix(arg, "-"):
			return usageError{"unknown option " + arg}
		default:
			words = append(words, arg)
		}
	}
	if len(words) < 2 {
		return usageError{"usage: hi team say <name> <text|-> [--wait]"}
	}
	team, text := words[0], strings.Join(words[1:], " ")
	if text == "-" {
		data, err := io.ReadAll(io.LimitReader(stdin, teamSayMaxText+1))
		if err != nil {
			return err
		}
		text = string(data)
	}
	if _, err := loadTeam(team); err != nil {
		return err
	}
	var said teamSaid
	asked, err := teamHostCall(team, "/say", teamSayRequest{Text: text, Who: currentUserName(), Wait: wait}, &said)
	if !asked {
		return fmt.Errorf("team %s isn't up, or runs a hi from before hi team say; hi team up %s, then try again", team, team)
	}
	if err != nil {
		return err
	}
	switch {
	case !wait:
		fmt.Fprintf(stdout, "Posted in team %s's channel; the lead answers in its thread (hi team channel %s shows it).\n", team, team)
	case said.Error != "":
		return fmt.Errorf("posted in the channel, but the lead couldn't answer: %s", said.Error)
	default:
		fmt.Fprintf(stdout, "The lead answered, in the message's thread in the channel:\n\n%s\n", said.Answer)
	}
	return nil
}
