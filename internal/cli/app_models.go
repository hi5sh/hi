package cli

import (
	"fmt"
	"io"
)

// hi app models: whether the developers pick the dev page chat's model
// and effort, with Claude Code's own /model and /effort. They do unless
// the app's admin says otherwise; then the chat shows no picker, and the
// sessions keep what they start with.

func appModelsCommand(args []string, stdout io.Writer) error {
	usage := usageError{"usage: hi app models <name> [on|off]"}
	if len(args) != 1 && len(args) != 2 {
		return usage
	}
	config, err := loadTeam(args[0])
	if err != nil {
		return err
	}
	if len(args) == 2 {
		if args[1] != "on" && args[1] != "off" {
			return usage
		}
		config.LockModels = args[1] == "off"
		if err := saveTeam(config); err != nil {
			return err
		}
	}
	fmt.Fprintln(stdout, appModelsLine(config))
	return nil
}

func appModelsLine(config appConfig) string {
	if config.LockModels {
		return fmt.Sprintf("%s's developers don't pick the chat's model and effort: its sessions keep what they start with (hi app models %s on lets them).", config.Name, config.Name)
	}
	return fmt.Sprintf("%s's developers pick the chat's model and effort on the dev page (hi app models %s off keeps them as they start).", config.Name, config.Name)
}
