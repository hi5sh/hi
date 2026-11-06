package cli

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

// hi app say is how the person who runs hi on the machine talks to a
// team's lead without Slack: hi posts the message in the channel, so the
// team sees it, the lead answers through Hermes' API server as it does
// the app's chat, and hi posts the answer in that message's thread. What
// it asks to change, the lead files as a ticket for the owners.

// appSaid is one message sent with hi app say, in run/say.json: what
// the approval is checked against, since the channel only shows the bot.
type appSaid struct {
	TS     string    `json:"ts"`
	Who    string    `json:"who"`
	Text   string    `json:"text"`
	At     time.Time `json:"at"`
	Answer string    `json:"answer,omitempty"`
	Error  string    `json:"error,omitempty"`
}

type appSayRequest struct {
	Text string `json:"text"`
	Who  string `json:"who"`
	Wait bool   `json:"wait,omitempty"`
}

const appSayMaxText = 8000

// appSayLeadWait is how long a message waits for a lead that isn't up
// yet, trying every appSayRetry.
var appSayLeadWait, appSayRetry = 5 * time.Minute, 10 * time.Second

// appSayWorkingAfter is when a thread with no answer yet says the lead is
// on it, so nobody asks again while it works: the people in Slack can't
// see a turn through Hermes' API server, and their reply in the thread is
// a conversation of its own.
var appSayWorkingAfter = 2 * time.Minute

var appSayApproval = regexp.MustCompile(`^say:([0-9]{9,11}\.[0-9]{6})$`)

func appSayPath(team string) string { return filepath.Join(appDir(team), "run", "say.json") }

func loadTeamSaid(team string) []appSaid {
	var said []appSaid
	if data, err := os.ReadFile(appSayPath(team)); err == nil {
		json.Unmarshal(data, &said)
	}
	return said
}

// saveTeamSaid keeps a message, replacing the one with its ts; messages
// older than an approval may be are dropped.
func (b *appBroker) saveTeamSaid(said appSaid) error {
	b.sayMu.Lock()
	defer b.sayMu.Unlock()
	var keep []appSaid
	for _, old := range loadTeamSaid(b.team) {
		if old.TS != said.TS && time.Since(old.At) < appWeek {
			keep = append(keep, old)
		}
	}
	keep = append(keep, said)
	if err := os.MkdirAll(filepath.Dir(appSayPath(b.team)), 0o700); err != nil {
		return err
	}
	data, _ := json.MarshalIndent(keep, "", "  ")
	return writeFileAtomic(appSayPath(b.team), append(data, '\n'), 0o600)
}

// say posts the admin's message in the channel and has the lead answer it
// in its thread; with wait, it returns once the lead has.
func (b *appBroker) say(request appSayRequest) (appSaid, error) {
	text := strings.TrimSpace(request.Text)
	switch {
	case text == "":
		return appSaid{}, errors.New("say something: hi app say <name> <text|->")
	case len(text) > appSayMaxText:
		return appSaid{}, fmt.Errorf("the message is longer than %d characters", appSayMaxText)
	case b.channel == nil:
		return appSaid{}, errors.New("hi isn't connected to Slack, so the people in the channel wouldn't see the message")
	}
	config, err := loadTeam(b.team)
	if err != nil {
		return appSaid{}, err
	}
	who := firstNonEmpty(strings.TrimSpace(request.Who), "someone")
	if !strings.HasSuffix(who, " on the admin page") {
		who += " on the machine"
	}
	said := appSaid{Who: who, Text: text, At: time.Now().UTC()}
	quoted := "> " + strings.ReplaceAll(appSlackEscape(text), "\n", "\n> ")
	if said.TS, err = b.channel.post(config.Channel, fmt.Sprintf(":desktop_computer: *%s* to the lead, with hi app say:\n%s", appSlackEscape(said.Who), quoted)); err != nil {
		return appSaid{}, err
	}
	if err := b.saveTeamSaid(said); err != nil {
		return said, err
	}
	answered := make(chan appSaid, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), appLeadTimeout)
		defer cancel()
		// The note never comes after the answer.
		var workingMu sync.Mutex
		answering := false
		working := time.AfterFunc(appSayWorkingAfter, func() {
			workingMu.Lock()
			defer workingMu.Unlock()
			if !answering {
				b.channel.postIn(config.Channel, said.TS, "The lead is still working on this; its answer comes in this thread.")
			}
		})
		answer, err := b.lead(ctx, "say-"+said.TS, appSayLeadInput(config, said), nil, nil)
		// A lead whose box is starting, as after hi update, gets a while.
		for start := time.Now(); errors.Is(err, errTeamLeadUnreachable) && time.Since(start) < appSayLeadWait; {
			select {
			case <-ctx.Done():
			case <-time.After(appSayRetry):
			}
			answer, err = b.lead(ctx, "say-"+said.TS, appSayLeadInput(config, said), nil, nil)
		}
		working.Stop()
		workingMu.Lock()
		answering = true
		workingMu.Unlock()
		done := said
		if answer = strings.TrimSpace(answer); err != nil && answer == "" {
			done.Error = err.Error()
			b.channel.postIn(config.Channel, said.TS, "The lead couldn't answer: "+appSlackEscape(err.Error()))
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

// appSayLeadInput is what the lead reads: who wrote, where its answer
// goes, and what the message may start.
func appSayLeadInput(config appConfig, said appSaid) string {
	return fmt.Sprintf("[hi app say · message say:%s · %s: the person who runs hi on the machine the app runs on, not one of the people in Slack · hi posted it in the channel as message %s, and posts your answer in its thread]\n\n%s\n\n"+
		"(Your answer ends your turn, and nothing happens after it until someone writes again. When this asks for a change to the app, file it as a task (hi app task add) and say its number: the owners build in the dev box. Nobody sees anything until you answer, so don't read through the code.)",
		said.TS, said.Who, said.TS, said.Text)
}

// appSayCommand is hi app say <name> <text|-> [--wait] on the machine.
func appSayCommand(args []string, stdin io.Reader, stdout io.Writer) error {
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
		return usageError{"usage: hi app say <name> <text|-> [--wait]"}
	}
	team, text := words[0], strings.Join(words[1:], " ")
	if text == "-" {
		data, err := io.ReadAll(io.LimitReader(stdin, appSayMaxText+1))
		if err != nil {
			return err
		}
		text = string(data)
	}
	if _, err := loadTeam(team); err != nil {
		return err
	}
	var said appSaid
	who := currentUserName()
	if by := strings.TrimSpace(os.Getenv(adminByEnv)); by != "" {
		who = by + " on the admin page"
	}
	asked, err := appHostCall(team, "/say", appSayRequest{Text: text, Who: who, Wait: wait}, &said)
	if !asked {
		return fmt.Errorf("app %s isn't up, or runs a hi from before hi app say; hi app up %s, then try again", team, team)
	}
	if err != nil {
		return err
	}
	switch {
	case !wait:
		fmt.Fprintf(stdout, "Posted in app %s's channel; the lead answers in its thread (hi app channel %s shows it).\n", team, team)
	case said.Error != "":
		return fmt.Errorf("posted in the channel, but the lead couldn't answer: %s", said.Error)
	default:
		fmt.Fprintf(stdout, "The lead answered, in the message's thread in the channel:\n\n%s\n", said.Answer)
	}
	return nil
}
