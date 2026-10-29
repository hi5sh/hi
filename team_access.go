package main

// Who reaches what (docs/specs/approved/hi_team_dev_access.md). Owners,
// whom an admin of the machine sets, and developers, whom owners set, work
// in the development environment: the dev page and the dev box.
// Contributors, whom owners set too, report bugs as tasks, in Slack to
// the lead or signed in from the helper. Being in the team's channel gives
// nobody a level. Anyone who can open the app is a viewer, with the
// helper's Ask and Feedback.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

const (
	teamLevelOwner       = "owner"
	teamLevelDeveloper   = "developer" // a Pro Dev
	teamLevelVibe        = "vibe"      // a Vibe Dev
	teamLevelContributor = "contributor"
	teamLevelNone        = "none"
)

// teamBuilds says someone works in the development environment: an owner,
// a Pro Dev, or a Vibe Dev.
func teamBuilds(config teamConfig, user string) bool {
	return containsString(config.Owners, user) || containsString(config.Developers, user) || containsString(config.VibeDevelopers, user)
}

// teamHasShell says someone gets the dev box's terminal, VS Code, and SSH:
// an owner or a Pro Dev, not a Vibe Dev, who has the agents' Chat.
func teamHasShell(config teamConfig, user string) bool {
	return containsString(config.Owners, user) || containsString(config.Developers, user)
}

// teamParseLevel reads a level as people write it: pro or developer, vibe.
func teamParseLevel(level string) string {
	switch strings.ToLower(strings.ReplaceAll(level, " ", "")) {
	case "pro", "prodev", teamLevelDeveloper:
		return teamLevelDeveloper
	case teamLevelVibe, "vibedev":
		return teamLevelVibe
	}
	return strings.ToLower(level)
}

// teamLevel is someone's level: owner, developer, contributor, or none.
func teamLevel(config teamConfig, user string) string {
	switch {
	case containsString(config.Owners, user):
		return teamLevelOwner
	case containsString(config.Developers, user):
		return teamLevelDeveloper
	case containsString(config.VibeDevelopers, user):
		return teamLevelVibe
	case containsString(config.Contributors, user):
		return teamLevelContributor
	}
	return teamLevelNone
}

// teamSetLevel makes someone a developer, a contributor, or none, and says
// what changed. Owners are the machine admin's (hi team owners). Someone
// who stops being a developer loses their SSH key, and anyone whose level
// drops loses their links, so their next click is refused.
func teamSetLevel(config *teamConfig, user, level string) (string, error) {
	if err := teamCheckPeople(user); err != nil {
		return "", err
	}
	if containsString(config.Owners, user) {
		return "", fmt.Errorf("%s is an owner of team %s; an admin of the machine changes owners (hi team owners %s remove %s)", user, config.Name, config.Name, user)
	}
	level = teamParseLevel(level)
	if level != teamLevelDeveloper && level != teamLevelVibe && level != teamLevelContributor && level != teamLevelNone {
		return "", fmt.Errorf("the level is pro (a Pro Dev), vibe (a Vibe Dev), contributor, or none, not %q", level)
	}
	was := teamLevel(*config, user)
	if was == level {
		return teamLevelSentence(user, level, config.Name, true), nil
	}
	without := func(list []string) []string {
		return slices.DeleteFunc(slices.Clone(list), func(id string) bool { return id == user })
	}
	config.Developers, config.VibeDevelopers, config.Contributors = without(config.Developers), without(config.VibeDevelopers), without(config.Contributors)
	switch level {
	case teamLevelDeveloper:
		config.Developers = append(config.Developers, user)
	case teamLevelVibe:
		config.VibeDevelopers = append(config.VibeDevelopers, user)
	case teamLevelContributor:
		config.Contributors = append(config.Contributors, user)
	}
	if err := saveTeam(*config); err != nil {
		return "", err
	}
	text := teamLevelSentence(user, level, config.Name, false)
	var ended []string
	if was == teamLevelDeveloper {
		os.Remove(teamDevKeyPath(config.Name, user))
		if err := writeTeamDevSSH(*config); err != nil {
			return "", err
		}
		ended = append(ended, "their SSH key is out of the dev box")
	}
	if teamLevelRank(level) < teamLevelRank(was) && teamEndLinks(config.Name, user) > 0 {
		ended = append(ended, "their private links have ended")
	}
	if len(ended) > 0 {
		text += " " + teamCapitalize(strings.Join(ended, ", and ")) + "."
	}
	return text, nil
}

// teamLevelRank orders the levels, none lowest.
func teamLevelRank(level string) int {
	return slices.Index([]string{teamLevelNone, teamLevelContributor, teamLevelVibe, teamLevelDeveloper, teamLevelOwner}, level)
}

// teamLevelSentence says someone's level: "U07ANNA is a developer of team
// demo."
func teamLevelSentence(user, level, team string, already bool) string {
	if level == teamLevelNone {
		if already {
			return fmt.Sprintf("%s has no level in team %s already.", user, team)
		}
		return fmt.Sprintf("%s has no level in team %s; they don't report, and the lead doesn't take their requests.", user, team)
	}
	return fmt.Sprintf("%s is %s of team %s%s.", user, teamLevelWords(level), team, map[bool]string{true: " already"}[already])
}

// teamLevelWords names a level in a sentence: "a developer".
func teamLevelWords(level string) string {
	switch level {
	case teamLevelOwner:
		return "an owner"
	case teamLevelDeveloper:
		return "a Pro Dev"
	case teamLevelVibe:
		return "a Vibe Dev"
	case teamLevelContributor:
		return "a contributor"
	}
	return "no one in particular"
}

// teamSetDevKey keeps a builder's public SSH key for the dev box, or
// removes it when key is "", and writes the box's authorized_keys.
func teamSetDevKey(config teamConfig, user, key string) error {
	if key == "" {
		os.Remove(teamDevKeyPath(config.Name, user))
		return writeTeamDevSSH(config)
	}
	if !teamHasShell(config, user) {
		return fmt.Errorf("%s isn't an owner or a Pro Dev of team %s, who get SSH into the dev box; an owner makes them a Pro Dev first", user, config.Name)
	}
	key = strings.TrimSpace(key)
	if strings.Contains(key, "PRIVATE KEY") {
		return errors.New("that is a private key; give the public one (the .pub file), and keep the private key to yourself")
	}
	if !teamSSHKeyPattern.MatchString(key) {
		return errors.New("that isn't an SSH public key; it looks like ssh-ed25519 AAAA… you@laptop (the .pub file)")
	}
	if err := os.MkdirAll(filepath.Dir(teamDevKeyPath(config.Name, user)), 0o700); err != nil {
		return err
	}
	if err := writeFileAtomic(teamDevKeyPath(config.Name, user), []byte(key+"\n"), 0o600); err != nil {
		return err
	}
	return writeTeamDevSSH(config)
}

// teamDevKeyLabel is a builder's SSH key as hi lists it, or what to run.
func teamDevKeyLabel(team, user string) string {
	if data, err := os.ReadFile(teamDevKeyPath(team, user)); err == nil {
		return teamSSHKeyLabel(string(data))
	}
	return "no SSH key: hi team access " + team + " key " + user + " <public key>"
}

// teamAccessCommand is hi team access.
func teamAccessCommand(args []string, stdout io.Writer) error {
	usage := usageError{"usage: hi team access <team> [<member ID> pro|vibe|contributor|none | key <member ID> <public key>|--remove]"}
	if len(args) == 0 {
		return usage
	}
	config, err := loadTeam(args[0])
	if err != nil {
		return err
	}
	switch {
	case len(args) == 1:
		printTeamAccess(config, stdout)
		return nil
	case len(args) >= 4 && args[1] == "key":
		user := strings.Trim(args[2], "<@>")
		if err := teamCheckPeople(user); err != nil {
			return err
		}
		if len(args) == 4 && args[3] == "--remove" {
			if err := teamSetDevKey(config, user, ""); err != nil {
				return err
			}
			fmt.Fprintf(stdout, "%s's SSH key is out of team %s's dev box.\n", user, config.Name)
			return nil
		}
		key := strings.TrimSpace(strings.Join(args[3:], " "))
		if data, err := os.ReadFile(key); err == nil {
			key = strings.TrimSpace(string(data)) // a file with the key
		}
		if err := teamSetDevKey(config, user, key); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "%s's SSH key for team %s's dev box is %s; a key they had before no longer works.\n", user, config.Name, teamSSHKeyLabel(key))
		return nil
	case len(args) == 3:
		text, err := teamSetLevel(&config, strings.Trim(args[1], "<@>"), args[2])
		if err != nil {
			return err
		}
		fmt.Fprintln(stdout, text)
		if level := teamParseLevel(args[2]); level == teamLevelDeveloper || level == teamLevelVibe {
			fmt.Fprint(stdout, teamDeveloperNote(config, level))
		}
		return nil
	}
	return usage
}

// teamDeveloperNote is what an owner should know about a new developer.
func teamDeveloperNote(config teamConfig, level string) string {
	note := "A Pro Dev works in the dev box like an owner, except releases, rollbacks, production's data and settings, keys, and who is who.\n"
	if level == teamLevelVibe {
		note = "A Vibe Dev works in the dev page's Chat with the agents, as a Pro Dev does, but has no terminal, VS Code, or SSH. The agents still run commands in the dev box for them.\n"
	}
	if config.PreviewData != teamPreviewEmpty {
		note += "The preview runs on a copy of production's data, so a developer can read it through a preview; hi team data " + config.Name + " preview empty starts the preview empty instead.\n"
	}
	if level == teamLevelDeveloper {
		note += "For SSH into the dev box, they give a public key (hi team access " + config.Name + " key <member ID> <key>, or on the dev page), and NetBird's policy for the box has to let them reach it, as for the owners.\n"
	}
	return note
}

func printTeamAccess(config teamConfig, stdout io.Writer) {
	fmt.Fprintf(stdout, "Owners of team %s (an admin changes them: hi team owners):\n", config.Name)
	for _, owner := range config.Owners {
		fmt.Fprintf(stdout, "  %-12s %s\n", owner, teamDevKeyLabel(config.Name, owner))
	}
	fmt.Fprintln(stdout, "Pro Devs, with Chat, terminal, VS Code, and SSH (owners change them, on the dev page or here):")
	if len(config.Developers) == 0 {
		fmt.Fprintln(stdout, "  none")
	}
	for _, developer := range config.Developers {
		fmt.Fprintf(stdout, "  %-12s %s\n", developer, teamDevKeyLabel(config.Name, developer))
	}
	fmt.Fprintln(stdout, "Vibe Devs, who work in the agents' Chat only:")
	if len(config.VibeDevelopers) == 0 {
		fmt.Fprintln(stdout, "  none")
	}
	for _, vibe := range config.VibeDevelopers {
		fmt.Fprintf(stdout, "  %s\n", vibe)
	}
	fmt.Fprintln(stdout, "Contributors, who report bugs as tasks (owners change them too):")
	if len(config.Contributors) == 0 {
		fmt.Fprintln(stdout, "  none")
	}
	for _, contributor := range config.Contributors {
		fmt.Fprintf(stdout, "  %s\n", contributor)
	}
	fmt.Fprintln(stdout, "Anyone else, in the channel or not, has no level.")
}

// level is someone's level in the team.
func (b *teamBroker) level(config teamConfig, user string) string {
	return teamLevel(config, user)
}

// teamDevPerson is a row of the dev page's People table.
type teamDevPerson struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Level     string `json:"level"`
	Key       string `json:"key,omitempty"` // their SSH key for the dev box
	InChannel bool   `json:"in_channel"`
}

// teamDevPeopleView is the People table: everyone with a level and
// everyone in the team's channel, owners first, then developers,
// contributors, and the rest, each by name.
type teamDevPeopleView struct {
	People       []teamDevPerson `json:"people"`
	ChannelError string          `json:"channel_error,omitempty"`
	// PreviewData says the preview runs on a copy of production's data,
	// which a developer can read through a preview.
	PreviewData bool `json:"preview_data"`
}

func (b *teamBroker) devPeopleView(config teamConfig) teamDevPeopleView {
	view := teamDevPeopleView{People: []teamDevPerson{}, PreviewData: config.PreviewData != teamPreviewEmpty}
	members := map[string]bool{}
	if b.channel == nil {
		view.ChannelError = "hi isn't connected to Slack, so it can't list the channel"
	} else if ids, err := b.channel.members(config.Channel); err != nil {
		view.ChannelError = "Slack wouldn't list the channel: " + err.Error()
	} else {
		for _, id := range ids {
			members[id] = true
		}
	}
	seen := map[string]bool{}
	for _, id := range slices.Concat(config.Owners, config.Developers, config.Contributors, sortedKeys(members)) {
		if seen[id] {
			continue
		}
		seen[id] = true
		person := teamDevPerson{ID: id, Name: id, Level: teamLevel(config, id), InChannel: members[id]}
		if b.channel != nil {
			person.Name = firstNonEmpty(b.channel.userName(id), id)
		}
		if data, err := os.ReadFile(teamDevKeyPath(config.Name, id)); err == nil {
			person.Key = teamSSHKeyLabel(string(data))
		}
		view.People = append(view.People, person)
	}
	slices.SortStableFunc(view.People, func(a, b teamDevPerson) int {
		if rank := teamLevelRank(b.Level) - teamLevelRank(a.Level); rank != 0 {
			return rank
		}
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	return view
}

// devPeopleRoutes are the People section, which is the owners', and a
// builder's own SSH key for the dev box.
func (b *teamBroker) devPeopleRoutes(mux *http.ServeMux, builder func(http.ResponseWriter, *http.Request) (teamPersonKey, bool),
	reply func(http.ResponseWriter, int, any), failure func(http.ResponseWriter, int, error)) {
	mux.HandleFunc("GET "+teamDevPagePrefix+"api/people", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := builder(w, r); !ok {
			return
		}
		config, err := loadTeam(b.team)
		if err != nil {
			failure(w, http.StatusInternalServerError, err)
			return
		}
		reply(w, http.StatusOK, b.devPeopleView(config))
	})
	mux.HandleFunc("POST "+teamDevPagePrefix+"api/people", func(w http.ResponseWriter, r *http.Request) {
		person, ok := builder(w, r)
		if !ok {
			return
		}
		var request struct {
			User  string `json:"user"`
			Level string `json:"level"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&request); err != nil {
			failure(w, http.StatusBadRequest, err)
			return
		}
		config, err := loadTeam(b.team)
		if err != nil {
			failure(w, http.StatusInternalServerError, err)
			return
		}
		was := teamLevel(config, request.User)
		text, err := teamSetLevel(&config, request.User, request.Level)
		if err != nil {
			failure(w, http.StatusBadRequest, err)
			return
		}
		// A level is a grant of power: the channel hears who gave it.
		if now := teamLevel(config, request.User); b.channel != nil && was != now {
			text := fmt.Sprintf(":busts_in_silhouette: <@%s> made <@%s> %s.", person.User, request.User, teamLevelWords(now))
			if now == teamLevelNone {
				text = fmt.Sprintf(":busts_in_silhouette: <@%s> took away <@%s>'s level: no reports, and the lead doesn't take their requests.", person.User, request.User)
			}
			b.channel.post(config.opsChannel(), text)
		}
		reply(w, http.StatusOK, map[string]string{"message": text})
	})
	mux.HandleFunc("POST "+teamDevPagePrefix+"api/sshkey", func(w http.ResponseWriter, r *http.Request) {
		person, ok := builder(w, r)
		if !ok {
			return
		}
		var request struct {
			Key    string `json:"key"`
			Remove bool   `json:"remove,omitempty"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 16<<10)).Decode(&request); err != nil {
			failure(w, http.StatusBadRequest, err)
			return
		}
		config, err := loadTeam(b.team)
		if err != nil {
			failure(w, http.StatusInternalServerError, err)
			return
		}
		key := strings.TrimSpace(request.Key)
		if request.Remove {
			key = ""
		} else if key == "" {
			failure(w, http.StatusBadRequest, errors.New("paste your public key: the .pub file, such as ~/.ssh/id_ed25519.pub"))
			return
		}
		if err := teamSetDevKey(config, person.User, key); err != nil {
			failure(w, http.StatusBadRequest, err)
			return
		}
		reply(w, http.StatusOK, map[string]string{})
	})
}
