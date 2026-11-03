package cli

// A team's heartbeat (docs/specs/approved/hi_app_ops.md, release 2):
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
	appHeartbeatDefault = time.Minute
	appHeartbeatLeast   = 30 * time.Second
	appHeartbeatMost    = time.Hour
)

func appHeartbeatLastPath(team string) string {
	return filepath.Join(appDir(team), "run", "heartbeat.json")
}

// appHeartbeatEvery is the team's interval, or the default.
func appHeartbeatEvery(config appConfig) time.Duration {
	if every, err := parseHeartbeatEvery(config.HeartbeatEvery, appHeartbeatLeast, appHeartbeatMost); err == nil {
		return every
	}
	return appHeartbeatDefault
}

func loadTeamHeartbeatLast(team string) heartbeatLast {
	var last heartbeatLast
	if data, err := os.ReadFile(appHeartbeatLastPath(team)); err == nil {
		json.Unmarshal(data, &last)
	}
	return last
}

// beat sends the team's heartbeat once, when it has one, and records it.
func (b *appBroker) beat() time.Duration {
	config, err := loadTeam(b.team)
	if err != nil {
		return appHeartbeatDefault
	}
	secrets, err := loadTeamSecrets(b.team)
	if err != nil || secrets.HeartbeatURL == "" {
		return appHeartbeatEvery(config)
	}
	last := heartbeatLast{At: time.Now().UTC()}
	if err := heartbeatPing(secrets.HeartbeatURL); err != nil {
		last.Error = err.Error()
	}
	if data, err := json.Marshal(last); err == nil {
		os.MkdirAll(filepath.Dir(appHeartbeatLastPath(b.team)), 0o700)
		writeFileAtomic(appHeartbeatLastPath(b.team), data, 0o600)
	}
	return appHeartbeatEvery(config)
}

// keepHeartbeat beats until done is closed.
func (b *appBroker) keepHeartbeat(done <-chan struct{}) {
	for {
		every := b.beat()
		select {
		case <-done:
			return
		case <-time.After(every):
		}
	}
}

// appHeartbeatLine is what hi app status says about the heartbeat.
func appHeartbeatLine(config appConfig) string {
	secrets, err := loadTeamSecrets(config.Name)
	if err != nil || secrets.HeartbeatURL == "" {
		return fmt.Sprintf("Heartbeat: none, so nobody would hear that the app stopped (hi app heartbeat %s <url>).", config.Name)
	}
	return fmt.Sprintf("Heartbeat: to %s every %s; the last was %s.", heartbeatHost(secrets.HeartbeatURL),
		heartbeatDuration(appHeartbeatEvery(config)), loadTeamHeartbeatLast(config.Name))
}

// appHeartbeatCommand is hi app heartbeat <name> [<url> [--every 1m] |
// off | test].
func appHeartbeatCommand(args []string, stdout io.Writer) error {
	usage := usageError{"usage: hi app heartbeat <name> [<url> [--every 1m] | off | test]"}
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
		return fmt.Errorf("the app's secrets: %w", err)
	}
	rest := args[1:]
	switch {
	case len(rest) == 0:
		fmt.Fprintln(stdout, appHeartbeatLine(config))
		return nil
	case len(rest) == 1 && rest[0] == "test":
		if secrets.HeartbeatURL == "" {
			return fmt.Errorf("app %s has no heartbeat; hi app heartbeat %s <url> sets one", name, name)
		}
		if err := heartbeatPing(secrets.HeartbeatURL); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "%s took the heartbeat.\n", heartbeatHost(secrets.HeartbeatURL))
		return nil
	case len(rest) == 1 && rest[0] == "off":
		if secrets.HeartbeatURL == "" {
			fmt.Fprintf(stdout, "App %s has no heartbeat.\n", name)
			return nil
		}
		secrets.HeartbeatURL = ""
		if err := saveTeamSecrets(name, secrets); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "App %s sends no heartbeat now. Pause or remove its check at the service, or it will alert.\n", name)
		return nil
	}
	target, every := "", appHeartbeatEvery(config)
	for i := 0; i < len(rest); i++ {
		switch {
		case rest[i] == "--every" && i+1 < len(rest):
			i++
			if every, err = parseHeartbeatEvery(rest[i], appHeartbeatLeast, appHeartbeatMost); err != nil {
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
	fmt.Fprintf(stdout, "App %s sends a heartbeat to %s every %s while it runs; the first arrived. Set the check's period there to %s, and who it alerts: the ops channel is a good place.\n",
		name, heartbeatHost(target), heartbeatDuration(every), heartbeatDuration(every))
	return nil
}

// setTeamHeartbeat checks a heartbeat with a first ping, and sets it.
func setTeamHeartbeat(config *appConfig, secrets *appSecrets, target string, every time.Duration) error {
	target = strings.TrimSpace(target)
	if err := checkHeartbeatURL(target); err != nil {
		return err
	}
	if err := heartbeatPing(target); err != nil {
		return fmt.Errorf("the first heartbeat didn't arrive: %w", err)
	}
	secrets.HeartbeatURL = target
	config.HeartbeatEvery = ""
	if every != appHeartbeatDefault {
		config.HeartbeatEvery = heartbeatDuration(every)
	}
	return nil
}

// appHeartbeatDown says, when a team with a heartbeat stops, that its
// check will alert: on the command line, and in the ops channel.
func appHeartbeatDown(config appConfig, stdout io.Writer) {
	secrets, err := loadTeamSecrets(config.Name)
	if err != nil || secrets.HeartbeatURL == "" {
		return
	}
	text := fmt.Sprintf("App %s's heartbeat to %s stops with it, so its check will alert: pause it there until the app is up again.", config.Name, heartbeatHost(secrets.HeartbeatURL))
	fmt.Fprintln(stdout, text)
	if secrets.SlackBotToken != "" {
		newLiveTeamChannel(secrets.SlackBotToken).post(config.opsChannel(), ":double_vertical_bar: "+text)
	}
}

// appAskHeartbeats is the wizard's last step: the team's heartbeat, and
// the machine's when it has none.
func appAskHeartbeats(stdin io.Reader, stdout io.Writer, styled bool, answers *appAnswers) error {
	appSay(stdout, styled, "Heartbeats tell an outside service, such as healthchecks.io (free for 20 checks), that the app",
		"runs; it alerts when they stop. Make a check there, and set there who hears of a stop: your IT team, or the ops channel.")
	target, err := appAskUntil(stdin, stdout, "App heartbeat URL [Enter: none]", false, func(target string) error {
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
		every, err := appAskUntil(stdin, stdout, "Every [1m]", false, func(text string) error {
			if text == "" {
				return nil
			}
			_, err := parseHeartbeatEvery(text, appHeartbeatLeast, appHeartbeatMost)
			return err
		})
		if err != nil {
			return err
		}
		if every != "" {
			if d, _ := parseHeartbeatEvery(every, appHeartbeatLeast, appHeartbeatMost); d != appHeartbeatDefault {
				answers.HeartbeatEvery = heartbeatDuration(d)
			}
		}
	}
	if _, set := loadMachineHeartbeat(); set {
		return nil
	}
	appSay(stdout, styled, "This machine has no heartbeat of its own: one says the machine is up, whichever of its apps run.")
	machine, err := appAskUntil(stdin, stdout, "Machine heartbeat URL [Enter: none]", false, func(target string) error {
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
func applyTeamHeartbeats(name string, answers appAnswers, stdout io.Writer) {
	if answers.HeartbeatURL != "" {
		secrets, err := loadTeamSecrets(name)
		if err == nil {
			secrets.HeartbeatURL = answers.HeartbeatURL
			err = saveTeamSecrets(name, secrets)
		}
		if err != nil {
			fmt.Fprintf(stdout, "The app's heartbeat isn't set: %v\nTry again with: hi app heartbeat %s <url>\n", err, name)
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
