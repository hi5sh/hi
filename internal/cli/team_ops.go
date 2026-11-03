package cli

// A team's ops channel (docs/specs/ideas/hi_team_ops.md, release 1): its
// incidents, warnings, security, keys, and access go to a second Slack
// channel for the people with the know-how, so the main one stays for
// releases, ideas, and the app. Without one, everything goes to the main
// channel, as before.

import (
	"fmt"
	"io"
	"path/filepath"
)

// opsChannel is where operations and security go: the ops channel, or
// the main one when the team has none.
func (c teamConfig) opsChannel() string {
	return firstNonEmpty(c.OpsChannel, c.Channel)
}

// teamChannels are the channels the lead talks in.
func (c teamConfig) teamChannels() []string {
	if c.OpsChannel == "" || c.OpsChannel == c.Channel {
		return []string{c.Channel}
	}
	return []string{c.Channel, c.OpsChannel}
}

// teamChannelLogDir is a channel's log: the main channel's in channel/,
// the ops channel's in channel/ops/.
func teamChannelLogDir(team string, config teamConfig, channel string) string {
	if channel != "" && channel == config.OpsChannel && channel != config.Channel {
		return filepath.Join(teamChannelDir(team), "ops")
	}
	return teamChannelDir(team)
}

// teamOpsCommand is hi app ops <name> [<channel ID>|off].
func teamOpsCommand(args []string, stdout io.Writer) error {
	usage := usageError{"usage: hi app ops <name> [<channel ID>|off]"}
	if len(args) == 0 || len(args) > 2 {
		return usage
	}
	name := args[0]
	config, err := loadTeam(name)
	if err != nil {
		return err
	}
	if len(args) == 1 {
		if config.OpsChannel == "" {
			fmt.Fprintf(stdout, "App %s posts everything in its channel, %s. A second channel for operations and security: hi app ops %s <channel ID>\n", name, config.Channel, name)
			return nil
		}
		fmt.Fprintf(stdout, "App %s: releases, ideas, and the app in %s; incidents, warnings, security, keys, and access in %s.\n", name, config.Channel, config.OpsChannel)
		return nil
	}
	switch id := args[1]; {
	case id == "off":
		if config.OpsChannel == "" {
			fmt.Fprintf(stdout, "App %s has no ops channel.\n", name)
			return nil
		}
		config.OpsChannel = ""
	case !slackIDPattern.MatchString(id):
		return fmt.Errorf("the ops channel is a Slack channel ID, such as C07ABC123, not %q", id)
	case id == config.Channel:
		return fmt.Errorf("%s is the app's main channel; the ops channel is a second one", id)
	default:
		secrets, err := loadTeamSecrets(name)
		if err != nil {
			return fmt.Errorf("the app's secrets: %w", err)
		}
		channel, err := teamCheckChannel(teamBotHandle(config), secrets.SlackBotToken, id)
		if err != nil {
			return err
		}
		config.OpsChannel = id
		fmt.Fprintf(stdout, "App %s posts incidents, warnings, security, keys, and access in #%s now; releases, ideas, and the app stay in %s.\n", name, channel, config.Channel)
		fmt.Fprintln(stdout, "Keep it to people with the know-how: a private channel, for owners and Pro Devs.")
	}
	if err := saveTeam(config); err != nil {
		return err
	}
	if config.OpsChannel == "" {
		fmt.Fprintf(stdout, "App %s posts everything in %s again.\n", name, config.Channel)
	}
	if teamState(name) != "up" {
		fmt.Fprintf(stdout, "The lead hears it from the app's next start: hi app up %s.\n", name)
		return nil
	}
	fmt.Fprintln(stdout, "The lead restarts to talk in both channels; the app stays up.")
	return teamUp(name, stdout)
}

// teamSkillOps tells the lead about the ops channel, when there is one.
func teamSkillOps(config teamConfig) string {
	if config.OpsChannel == "" || config.OpsChannel == config.Channel {
		return ""
	}
	return fmt.Sprintf(`
## Two channels

The app has two Slack channels, and you're in both:

- **The main channel** (%[1]s) is for the people who use and shape the
  app: releases, Stage, tasks, ideas, the helper's feedback. Talk there in
  plain words.
- **The ops channel** (%[2]s) is for the owners and the technical people:
  incidents, jobs, warnings, downtime, advisories, keys, access, and the
  dev box's site asks. hi posts those there, and wakes you in their
  threads; answer there, and be technical.

Asked in the main channel about something that's in ops (the app is
down, a job stopped), say what it means for the people in plain words,
and that the owners have it in the ops channel. Don't copy technical
detail, keys, hosts, or logs into the main channel.

Changes to permissions are said only in the ops channel: who is an
owner, a Pro Dev, a Vibe Dev, or a contributor, who may use the dev
page, the addresses' privacy and sign-in, the sites the dev box may
reach, the app's keys and sign-ins. Asked about them in the main
channel, answer the person in a DM or point them to an owner.
`, config.Channel, config.OpsChannel)
}
