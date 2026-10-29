package main

// A team's heartbeat (docs/specs/approved/hi_team_ops.md, release 2):
// while the team's service runs, it sends a GET to the URL the owners
// gave, at the interval they chose, so an outside service hears when the
// team, its broker, or its machine stops. The URL is in secrets.json; the
// interval in team.json. A running team takes a change at its next beat.

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	teamHeartbeatDefault = time.Minute
	teamHeartbeatLeast   = 30 * time.Second
	teamHeartbeatMost    = time.Hour
)

func teamHeartbeatLastPath(team string) string {
	return filepath.Join(teamDir(team), "run", "heartbeat.json")
}

// teamHeartbeatEvery is the team's interval, or the default.
func teamHeartbeatEvery(config teamConfig) time.Duration {
	if every, err := parseHeartbeatEvery(config.HeartbeatEvery, teamHeartbeatLeast, teamHeartbeatMost); err == nil {
		return every
	}
	return teamHeartbeatDefault
}

func loadTeamHeartbeatLast(team string) heartbeatLast {
	var last heartbeatLast
	if data, err := os.ReadFile(teamHeartbeatLastPath(team)); err == nil {
		json.Unmarshal(data, &last)
	}
	return last
}

// beat sends the team's heartbeat once, when it has one, and records it.
func (b *teamBroker) beat() time.Duration {
	config, err := loadTeam(b.team)
	if err != nil {
		return teamHeartbeatDefault
	}
	secrets, err := loadTeamSecrets(b.team)
	if err != nil || secrets.HeartbeatURL == "" {
		return teamHeartbeatEvery(config)
	}
	last := heartbeatLast{At: time.Now().UTC()}
	if err := heartbeatPing(secrets.HeartbeatURL); err != nil {
		last.Error = err.Error()
	}
	if data, err := json.Marshal(last); err == nil {
		os.MkdirAll(filepath.Dir(teamHeartbeatLastPath(b.team)), 0o700)
		writeFileAtomic(teamHeartbeatLastPath(b.team), data, 0o600)
	}
	return teamHeartbeatEvery(config)
}

// keepHeartbeat beats until done is closed.
func (b *teamBroker) keepHeartbeat(done <-chan struct{}) {
	for {
		every := b.beat()
		select {
		case <-done:
			return
		case <-time.After(every):
		}
	}
}

// teamHeartbeatLine is what hi team status says about the heartbeat.
func teamHeartbeatLine(config teamConfig) string {
	secrets, err := loadTeamSecrets(config.Name)
	if err != nil || secrets.HeartbeatURL == "" {
		return fmt.Sprintf("Heartbeat: none, so nobody would hear that the team stopped (hi team heartbeat %s <url>).", config.Name)
	}
	return fmt.Sprintf("Heartbeat: to %s every %s; the last was %s.", heartbeatHost(secrets.HeartbeatURL),
		heartbeatDuration(teamHeartbeatEvery(config)), loadTeamHeartbeatLast(config.Name))
}

// teamHeartbeatCommand is hi team heartbeat <name> [<url> [--every 1m] |
// off | test].
func teamHeartbeatCommand(args []string, stdout io.Writer) error {
	usage := usageError{"usage: hi team heartbeat <name> [<url> [--every 1m] | off | test]"}
	if len(args) == 0 {
		return usage
	}
	name := args[0]
	config, err := loadTeam(name)
	if err != nil {
		return err
	}
	secrets, err := loadTeamSecrets(name)
	if err != nil {
		return fmt.Errorf("the team's secrets: %w", err)
	}
	rest := args[1:]
	switch {
	case len(rest) == 0:
		fmt.Fprintln(stdout, teamHeartbeatLine(config))
		return nil
	case len(rest) == 1 && rest[0] == "test":
		if secrets.HeartbeatURL == "" {
			return fmt.Errorf("team %s has no heartbeat; hi team heartbeat %s <url> sets one", name, name)
		}
		if err := heartbeatPing(secrets.HeartbeatURL); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "%s took the heartbeat.\n", heartbeatHost(secrets.HeartbeatURL))
		return nil
	case len(rest) == 1 && rest[0] == "off":
		if secrets.HeartbeatURL == "" {
			fmt.Fprintf(stdout, "Team %s has no heartbeat.\n", name)
			return nil
		}
		secrets.HeartbeatURL = ""
		if err := saveTeamSecrets(name, secrets); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Team %s sends no heartbeat now. Pause or remove its check at the service, or it will alert.\n", name)
		return nil
	}
	target, every := "", teamHeartbeatEvery(config)
	for i := 0; i < len(rest); i++ {
		switch {
		case rest[i] == "--every" && i+1 < len(rest):
			i++
			if every, err = parseHeartbeatEvery(rest[i], teamHeartbeatLeast, teamHeartbeatMost); err != nil {
				return err
			}
		case strings.HasPrefix(rest[i], "-") || target != "":
			return usage
		default:
			target = rest[i]
		}
	}
	if target == "" {
		return usage
	}
	if err := setTeamHeartbeat(&config, &secrets, target, every); err != nil {
		return err
	}
	if err := saveTeam(config); err != nil {
		return err
	}
	if err := saveTeamSecrets(name, secrets); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Team %s sends a heartbeat to %s every %s while it runs; the first arrived. Set the check's period there to %s, and who it alerts: the ops channel is a good place.\n",
		name, heartbeatHost(target), heartbeatDuration(every), heartbeatDuration(every))
	return nil
}

// setTeamHeartbeat checks a heartbeat with a first ping, and sets it.
func setTeamHeartbeat(config *teamConfig, secrets *teamSecrets, target string, every time.Duration) error {
	target = strings.TrimSpace(target)
	if err := checkHeartbeatURL(target); err != nil {
		return err
	}
	if err := heartbeatPing(target); err != nil {
		return fmt.Errorf("the first heartbeat didn't arrive: %w", err)
	}
	secrets.HeartbeatURL = target
	config.HeartbeatEvery = ""
	if every != teamHeartbeatDefault {
		config.HeartbeatEvery = heartbeatDuration(every)
	}
	return nil
}

// teamHeartbeatDown says, when a team with a heartbeat stops, that its
// check will alert: on the command line, and in the ops channel.
func teamHeartbeatDown(config teamConfig, stdout io.Writer) {
	secrets, err := loadTeamSecrets(config.Name)
	if err != nil || secrets.HeartbeatURL == "" {
		return
	}
	text := fmt.Sprintf("Team %s's heartbeat to %s stops with it, so its check will alert: pause it there until the team is up again.", config.Name, heartbeatHost(secrets.HeartbeatURL))
	fmt.Fprintln(stdout, text)
	if secrets.SlackBotToken != "" {
		newLiveTeamChannel(secrets.SlackBotToken).post(config.opsChannel(), ":double_vertical_bar: "+text)
	}
}

// teamAskHeartbeats is the wizard's last step: the team's heartbeat, and
// the machine's when it has none.
func teamAskHeartbeats(stdin io.Reader, stdout io.Writer, styled bool, answers *teamAnswers) error {
	teamSay(stdout, styled, "Heartbeats tell an outside service, such as healthchecks.io (free for 20 checks), that the team",
		"runs; it alerts when they stop. Make a check there, and set there who hears of a stop: your IT team, or the ops channel.")
	target, err := teamAskUntil(stdin, stdout, "Team heartbeat URL [Enter: none]", false, func(target string) error {
		if target == "" {
			return nil
		}
		if err := checkHeartbeatURL(target); err != nil {
			return err
		}
		if err := heartbeatPing(target); err != nil {
			return fmt.Errorf("the heartbeat didn't arrive: %w", err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	answers.HeartbeatURL = strings.TrimSpace(target)
	if answers.HeartbeatURL != "" {
		every, err := teamAskUntil(stdin, stdout, "Every [1m]", false, func(text string) error {
			if text == "" {
				return nil
			}
			_, err := parseHeartbeatEvery(text, teamHeartbeatLeast, teamHeartbeatMost)
			return err
		})
		if err != nil {
			return err
		}
		if every != "" {
			if d, _ := parseHeartbeatEvery(every, teamHeartbeatLeast, teamHeartbeatMost); d != teamHeartbeatDefault {
				answers.HeartbeatEvery = heartbeatDuration(d)
			}
		}
	}
	if _, set := loadMachineHeartbeat(); set {
		return nil
	}
	teamSay(stdout, styled, "This machine has no heartbeat of its own: one says the machine is up, whichever of its teams run.")
	machine, err := teamAskUntil(stdin, stdout, "Machine heartbeat URL [Enter: none]", false, func(target string) error {
		if target == "" {
			return nil
		}
		return checkHeartbeatURL(target)
	})
	if err != nil {
		return err
	}
	answers.MachineHeartbeatURL = strings.TrimSpace(machine)
	return nil
}

// applyTeamHeartbeats keeps what the answers gave once the team exists:
// the team's heartbeat in its secrets, and the machine's timer.
func applyTeamHeartbeats(name string, answers teamAnswers, stdout io.Writer) {
	if answers.HeartbeatURL != "" {
		secrets, err := loadTeamSecrets(name)
		if err == nil {
			secrets.HeartbeatURL = answers.HeartbeatURL
			err = saveTeamSecrets(name, secrets)
		}
		if err != nil {
			fmt.Fprintf(stdout, "The team's heartbeat isn't set: %v\nTry again with: hi team heartbeat %s <url>\n", err, name)
		}
	}
	if answers.MachineHeartbeatURL != "" {
		if _, set := loadMachineHeartbeat(); !set {
			if err := setMachineHeartbeat(answers.MachineHeartbeatURL, machineHeartbeatDefault, stdout); err != nil {
				fmt.Fprintf(stdout, "The machine's heartbeat isn't set: %v\nTry again with: hi heartbeat <url>\n", err)
			}
		}
	}
}
