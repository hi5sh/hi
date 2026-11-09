package cli

// Who reaches what (docs/specs/approved/hi_app_dev_access.md). Owners,
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
	appLevelOwner       = "owner"
	appLevelDeveloper   = "developer" // a Pro Dev
	appLevelVibe        = "vibe"      // a Vibe Dev
	appLevelContributor = "contributor"
	appLevelNone        = "none"
)

// appBuilds says someone works in the development environment: an owner,
// a Pro Dev, or a Vibe Dev.
func appBuilds(config appConfig, user string) bool {
	return containsString(config.Owners, user) || containsString(config.Developers, user) || containsString(config.VibeDevelopers, user)
}

// appHasShell says someone gets the dev box's terminal, VS Code, and SSH:
// an owner or a Pro Dev, not a Vibe Dev, who has the agents' Chat.
func appHasShell(config appConfig, user string) bool {
	return containsString(config.Owners, user) || containsString(config.Developers, user)
}

// appParseLevel reads a level as people write it: pro or developer, vibe.
func appParseLevel(level string) string {
	switch strings.ToLower(strings.ReplaceAll(level, " ", "")) {
	case "pro", "prodev", appLevelDeveloper:
		return appLevelDeveloper
	case appLevelVibe, "vibedev":
		return appLevelVibe
	}
	return strings.ToLower(level)
}

// appLevel is someone's level: owner, developer, contributor, or none.
func appLevel(config appConfig, user string) string {
	switch {
	case containsString(config.Owners, user):
		return appLevelOwner
	case containsString(config.Developers, user):
		return appLevelDeveloper
	case containsString(config.VibeDevelopers, user):
		return appLevelVibe
	case containsString(config.Contributors, user):
		return appLevelContributor
	}
	return appLevelNone
}

// appSetLevel makes someone a developer, a contributor, or none, and says
// what changed. Owners are the machine admin's (hi app owners). Someone
// who stops being a developer loses their SSH key, and anyone whose level
// drops loses their links, so their next click is refused.
func appSetLevel(config *appConfig, user, level string) (string, error) {
	if err := appCheckPeople(user); err != nil {
		return "", err
	}
	if containsString(config.Owners, user) {
		return "", fmt.Errorf("%s is an owner of app %s; an admin of the machine changes owners (hi app owners %s remove %s)", user, config.Name, config.Name, user)
	}
	level = appParseLevel(level)
	if level != appLevelDeveloper && level != appLevelVibe && level != appLevelContributor && level != appLevelNone {
		return "", fmt.Errorf("the level is pro (a Pro Dev), vibe (a Vibe Dev), contributor, or none, not %q", level)
	}
	was := appLevel(*config, user)
	if was == level {
		return appLevelSentence(user, level, config.Name, true), nil
	}
	without := func(list []string) []string {
		return slices.DeleteFunc(slices.Clone(list), func(id string) bool { return id == user })
	}
	config.Developers, config.VibeDevelopers, config.Contributors = without(config.Developers), without(config.VibeDevelopers), without(config.Contributors)
	switch level {
	case appLevelDeveloper:
		config.Developers = append(config.Developers, user)
	case appLevelVibe:
		config.VibeDevelopers = append(config.VibeDevelopers, user)
	case appLevelContributor:
		config.Contributors = append(config.Contributors, user)
	}
	if err := saveTeam(*config); err != nil {
		return "", err
	}
	text := appLevelSentence(user, level, config.Name, false)
	var ended []string
	if was == appLevelDeveloper {
		os.Remove(appDevKeyPath(config.Name, user))
		if err := writeTeamDevSSH(*config); err != nil {
			return "", err
		}
		ended = append(ended, "their SSH key is out of the dev box")
	}
	if appLevelRank(level) < appLevelRank(was) && appEndLinks(config.Name, user) > 0 {
		ended = append(ended, "their private links have ended")
	}
	if len(ended) > 0 {
		text += " " + appCapitalize(strings.Join(ended, ", and ")) + "."
	}
	return text, nil
}

// appLevelRank orders the levels, none lowest.
func appLevelRank(level string) int {
	return slices.Index([]string{appLevelNone, appLevelContributor, appLevelVibe, appLevelDeveloper, appLevelOwner}, level)
}

// appLevelSentence says someone's level: "U07ANNA is a developer of team
// demo."
func appLevelSentence(user, level, team string, already bool) string {
	if level == appLevelNone {
		if already {
			return fmt.Sprintf("%s has no level in app %s already.", user, team)
		}
		return fmt.Sprintf("%s has no level in app %s; they don't report, and the lead doesn't take their requests.", user, team)
	}
	return fmt.Sprintf("%s is %s of app %s%s.", user, appLevelWords(level), team, map[bool]string{true: " already"}[already])
}

// appLevelWords names a level in a sentence: "a developer".
func appLevelWords(level string) string {
	switch level {
	case appLevelOwner:
		return "an owner"
	case appLevelDeveloper:
		return "a Pro Dev"
	case appLevelVibe:
		return "a Vibe Dev"
	case appLevelContributor:
		return "a contributor"
	}
	return "no one in particular"
}

// appSetDevKey keeps a builder's public SSH key for the dev box, or
// removes it when key is "", and writes the box's authorized_keys.
func appSetDevKey(config appConfig, user, key string) error {
	if key == "" {
		os.Remove(appDevKeyPath(config.Name, user))
		return writeTeamDevSSH(config)
	}
	if !appHasShell(config, user) {
		return fmt.Errorf("%s isn't an owner or a Pro Dev of app %s, who get SSH into the dev box; an owner makes them a Pro Dev first", user, config.Name)
	}
	key = strings.TrimSpace(key)
	if strings.Contains(key, "PRIVATE KEY") {
		return errors.New("that is a private key; give the public one (the .pub file), and keep the private key to yourself")
	}
	if !appSSHKeyPattern.MatchString(key) {
		return errors.New("that isn't an SSH public key; it looks like ssh-ed25519 AAAA… you@laptop (the .pub file)")
	}
	if err := os.MkdirAll(filepath.Dir(appDevKeyPath(config.Name, user)), 0o700); err != nil {
		return err
	}
	if err := writeFileAtomic(appDevKeyPath(config.Name, user), []byte(key+"\n"), 0o600); err != nil {
		return err
	}
	return writeTeamDevSSH(config)
}

// appDevKeyLabel is a builder's SSH key as hi lists it, or what to run.
func appDevKeyLabel(team, user string) string {
	if data, err := os.ReadFile(appDevKeyPath(team, user)); err == nil {
		return appSSHKeyLabel(string(data))
	}
	return "no SSH key: hi app access " + team + " key " + user + " <public key>"
}

// appAccessCommand is hi app access.
func appAccessCommand(args []string, stdout io.Writer) error {
	usage := usageError{"usage: hi app access <app> [<member ID> pro|vibe|contributor|none | key <member ID> <public key>|--remove]"}
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
		if err := appCheckPeople(user); err != nil {
			return err
		}
		if len(args) == 4 && args[3] == "--remove" {
			if err := appSetDevKey(config, user, ""); err != nil {
				return err
			}
			fmt.Fprintf(stdout, "%s's SSH key is out of app %s's dev box.\n", user, config.Name)
			return nil
		}
		key := strings.TrimSpace(strings.Join(args[3:], " "))
		if data, err := os.ReadFile(key); err == nil {
			key = strings.TrimSpace(string(data)) // a file with the key
		}
		if err := appSetDevKey(config, user, key); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "%s's SSH key for app %s's dev box is %s; a key they had before no longer works.\n", user, config.Name, appSSHKeyLabel(key))
		return nil
	case len(args) == 3:
		text, err := appSetLevel(&config, strings.Trim(args[1], "<@>"), args[2])
		if err != nil {
			return err
		}
		fmt.Fprintln(stdout, text)
		if level := appParseLevel(args[2]); level == appLevelDeveloper || level == appLevelVibe {
			fmt.Fprint(stdout, appDeveloperNote(config, level))
		}
		return nil
	}
	return usage
}

// appDeveloperNote is what an owner should know about a new developer.
func appDeveloperNote(config appConfig, level string) string {
	note := "A Pro Dev works in the dev box and releases like an owner, except rollbacks, production's data and settings, keys, and who is who.\n"
	if level == appLevelVibe {
		note = "A Vibe Dev works in the dev page's Chat with the agents, as a Pro Dev does, and releases, but has no terminal, VS Code, or SSH. The agents still run commands in the dev box for them.\n"
	}
	if config.PreviewData != appPreviewEmpty {
		note += "The preview runs on a copy of production's data, so a developer can read it through a preview; hi app data " + config.Name + " preview empty starts the preview empty instead.\n"
	}
	if level == appLevelDeveloper {
		note += "For SSH into the dev box, they give a public key (hi app access " + config.Name + " key <member ID> <key>, or on the dev page), and NetBird's policy for the box has to let them reach it, as for the owners.\n"
	}
	return note
}

func printTeamAccess(config appConfig, stdout io.Writer) {
	names := appPeopleNames(config, slices.Concat(config.Owners, config.Developers, config.VibeDevelopers, config.Contributors))
	fmt.Fprintf(stdout, "Owners of app %s (an admin changes them: hi app owners):\n", config.Name)
	for _, owner := range config.Owners {
		fmt.Fprintf(stdout, "  %s %s\n", appPersonColumn(owner, names), appDevKeyLabel(config.Name, owner))
	}
	fmt.Fprintln(stdout, "Pro Devs, with Chat, terminal, VS Code, and SSH (owners change them, on the dev page or here):")
	if len(config.Developers) == 0 {
		fmt.Fprintln(stdout, "  none")
	}
	for _, developer := range config.Developers {
		fmt.Fprintf(stdout, "  %s %s\n", appPersonColumn(developer, names), appDevKeyLabel(config.Name, developer))
	}
	fmt.Fprintln(stdout, "Vibe Devs, who work in the agents' Chat only:")
	if len(config.VibeDevelopers) == 0 {
		fmt.Fprintln(stdout, "  none")
	}
	for _, vibe := range config.VibeDevelopers {
		fmt.Fprintf(stdout, "  %s\n", strings.TrimRight(appPersonColumn(vibe, names), " "))
	}
	fmt.Fprintln(stdout, "Contributors, who report bugs as tasks (owners change them too):")
	if len(config.Contributors) == 0 {
		fmt.Fprintln(stdout, "  none")
	}
	for _, contributor := range config.Contributors {
		fmt.Fprintf(stdout, "  %s\n", strings.TrimRight(appPersonColumn(contributor, names), " "))
	}
	fmt.Fprintln(stdout, "Anyone else, in the channel or not, has no level.")
}

// appSlackUserName asks Slack for a member's name; tests replace it.
var appSlackUserName = func(botToken, id string) string {
	name := newLiveTeamChannel(botToken).userName(id)
	if name == id {
		return ""
	}
	return name
}

// appPeopleNames is the names of a team's members by Slack member ID,
// for listings an admin or an agent reads: those hi already knows, from
// the links it sent them, and Slack's for the rest. A name it can't find
// is left out, and the ID stands alone.
func appPeopleNames(config appConfig, ids []string) map[string]string {
	names := map[string]string{}
	for _, key := range loadTeamPeople(config.Name).Keys {
		if key.Name != "" && key.Name != key.User {
			names[key.User] = key.Name
		}
	}
	secrets, _ := loadTeamSecrets(config.Name)
	for _, id := range ids {
		if names[id] == "" && secrets.SlackBotToken != "" {
			if name := appSlackUserName(secrets.SlackBotToken, id); name != "" {
				names[id] = name
			}
		}
	}
	return names
}

// appPersonColumn is a member's ID and name, padded to line up.
func appPersonColumn(id string, names map[string]string) string {
	return fmt.Sprintf("%-12s %-22s", id, names[id])
}

// level is someone's level in the team.
func (b *appBroker) level(config appConfig, user string) string {
	return appLevel(config, user)
}

// appDevPerson is a row of the dev page's People table.
type appDevPerson struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Level     string `json:"level"`
	Key       string `json:"key,omitempty"` // their SSH key for the dev box
	InChannel bool   `json:"in_channel"`
}

// appDevPeopleView is the People table: everyone with a level and
// everyone in the team's channel, owners first, then developers,
// contributors, and the rest, each by name.
type appDevPeopleView struct {
	People       []appDevPerson `json:"people"`
	ChannelError string         `json:"channel_error,omitempty"`
	// PreviewData says the preview runs on a copy of production's data,
	// which a developer can read through a preview.
	PreviewData bool `json:"preview_data"`
}

func (b *appBroker) devPeopleView(config appConfig) appDevPeopleView {
	view := appDevPeopleView{People: []appDevPerson{}, PreviewData: config.PreviewData != appPreviewEmpty}
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
	for _, id := range slices.Concat(config.Owners, config.Developers, config.VibeDevelopers, config.Contributors, sortedKeys(members)) {
		if seen[id] {
			continue
		}
		seen[id] = true
		person := appDevPerson{ID: id, Name: id, Level: appLevel(config, id), InChannel: members[id]}
		if b.channel != nil {
			person.Name = firstNonEmpty(b.channel.userName(id), id)
		}
		if data, err := os.ReadFile(appDevKeyPath(config.Name, id)); err == nil {
			person.Key = appSSHKeyLabel(string(data))
		}
		view.People = append(view.People, person)
	}
	slices.SortStableFunc(view.People, func(a, b appDevPerson) int {
		if rank := appLevelRank(b.Level) - appLevelRank(a.Level); rank != 0 {
			return rank
		}
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	return view
}

// devPeopleRoutes are the People section, which is the owners', and a
// builder's own SSH key for the dev box.
func (b *appBroker) devPeopleRoutes(mux *http.ServeMux, builder func(http.ResponseWriter, *http.Request) (appPersonKey, bool),
	reply func(http.ResponseWriter, int, any), failure func(http.ResponseWriter, int, error)) {
	mux.HandleFunc("GET "+appDevPagePrefix+"api/people", func(w http.ResponseWriter, r *http.Request) {
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
	mux.HandleFunc("POST "+appDevPagePrefix+"api/people", func(w http.ResponseWriter, r *http.Request) {
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
		was := appLevel(config, request.User)
		text, err := appSetLevel(&config, request.User, request.Level)
		if err != nil {
			failure(w, http.StatusBadRequest, err)
			return
		}
		// A level is a grant of power: the channel hears who gave it.
		if now := appLevel(config, request.User); b.channel != nil && was != now {
			text := fmt.Sprintf(":busts_in_silhouette: <@%s> made <@%s> %s.", person.User, request.User, appLevelWords(now))
			if now == appLevelNone {
				text = fmt.Sprintf(":busts_in_silhouette: <@%s> took away <@%s>'s level: no reports, and the lead doesn't take their requests.", person.User, request.User)
			}
			b.channel.post(config.opsChannel(), text)
		}
		reply(w, http.StatusOK, map[string]string{"message": text})
	})
	mux.HandleFunc("POST "+appDevPagePrefix+"api/sshkey", func(w http.ResponseWriter, r *http.Request) {
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
		if err := appSetDevKey(config, person.User, key); err != nil {
			failure(w, http.StatusBadRequest, err)
			return
		}
		reply(w, http.StatusOK, map[string]string{})
	})
}
