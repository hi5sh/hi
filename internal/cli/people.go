package cli

// The machine's people (docs/specs/approved/hi_people.md). A person is an
// email: their name, their Slack accounts (one per workspace), and their
// SSH key. Each app keeps its own levels in app.json, by Slack member ID
// or by email, so its folder still moves on its own; this list joins
// them, so someone in two Slack workspaces is one person, and someone
// signs in by email wherever they have a level.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/slack-go/slack"
)

// appEmailPattern is an email address as hi keeps it: lowercased, and
// safe as a file name (an SSH key's, app_dev.go).
var appEmailPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._%+'-]{0,63}@[a-z0-9]([a-z0-9-]{0,62}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,62}[a-z0-9])?)+$`)

// appIsEmail says an entry of an app's lists is an email, not a Slack
// member ID.
func appIsEmail(id string) bool { return appEmailPattern.MatchString(id) }

// appPersonID is someone as people type them: <@U07ANNA>, U07ANNA, or
// Anna@Corp.example, which hi keeps as anna@corp.example.
func appPersonID(text string) string {
	text = strings.Trim(strings.TrimSpace(text), "<@>")
	if strings.Contains(text, "@") {
		return strings.ToLower(text)
	}
	return text
}

// appMention is someone in a Slack message: a mention for a member ID,
// their address for an email.
func appMention(id string) string {
	if appIsEmail(id) {
		return appSlackEscape(id)
	}
	return "<@" + id + ">"
}

// machinePerson is one person on the machine.
type machinePerson struct {
	Email string `json:"email"`
	Name  string `json:"name,omitempty"`
	// Slack is their member IDs, one for each workspace hi found them in.
	Slack []string `json:"slack,omitempty"`
	// SSHKey is their public key, put into the dev box of every app where
	// they are an owner or a Pro Dev.
	SSHKey string `json:"ssh_key,omitempty"`
	// Outside lets in an address outside the machine's domains.
	Outside  bool      `json:"outside,omitempty"`
	Added    time.Time `json:"added"`
	By       string    `json:"by,omitempty"`
	SignedIn time.Time `json:"signed_in,omitzero"`
	Invited  time.Time `json:"invited,omitzero"`
}

// ids is every entry an app's lists may have for them.
func (p machinePerson) ids() []string {
	return append([]string{p.Email}, p.Slack...)
}

// machinePeople is people.json, beside the apps' links.
type machinePeople struct {
	// Domains are the organisation's email domains: people are added with
	// an address in one of them, unless an admin says they're outside.
	// None lets in any.
	Domains []string        `json:"domains,omitempty"`
	People  []machinePerson `json:"people"`
}

func machinePeoplePath() string { return filepath.Join(appsDir(), "people.json") }

var machinePeopleMu sync.Mutex

// machinePeopleLock holds the list for a change, across hi's processes:
// the admin page's, the apps' services, and the command line.
func machinePeopleLock() func() {
	machinePeopleMu.Lock()
	os.MkdirAll(appsDir(), 0o700)
	file, err := os.OpenFile(filepath.Join(appsDir(), ".people.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return machinePeopleMu.Unlock
	}
	syscall.Flock(int(file.Fd()), syscall.LOCK_EX)
	return func() {
		syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		file.Close()
		machinePeopleMu.Unlock()
	}
}

func loadMachinePeople() machinePeople {
	var people machinePeople
	if data, err := os.ReadFile(machinePeoplePath()); err == nil {
		json.Unmarshal(data, &people)
	}
	if people.People == nil {
		people.People = []machinePerson{}
	}
	return people
}

func saveMachinePeople(people machinePeople) error {
	sort.Slice(people.People, func(i, j int) bool {
		return strings.ToLower(firstNonEmpty(people.People[i].Name, people.People[i].Email)) < strings.ToLower(firstNonEmpty(people.People[j].Name, people.People[j].Email))
	})
	data, _ := json.MarshalIndent(people, "", "  ")
	return writeFileAtomic(machinePeoplePath(), append(data, '\n'), 0o600)
}

// changeMachinePeople changes the list under its lock.
func changeMachinePeople(change func(*machinePeople) error) error {
	unlock := machinePeopleLock()
	defer unlock()
	people := loadMachinePeople()
	if err := change(&people); err != nil {
		return err
	}
	return saveMachinePeople(people)
}

// find is the person an entry belongs to, by email or Slack member ID,
// or -1.
func (m machinePeople) find(id string) int {
	id = appPersonID(id)
	if id == "" {
		return -1
	}
	return slices.IndexFunc(m.People, func(p machinePerson) bool { return p.Email == id || slices.Contains(p.Slack, id) })
}

// person is the one an entry belongs to.
func (m machinePeople) person(id string) (machinePerson, bool) {
	if i := m.find(id); i >= 0 {
		return m.People[i], true
	}
	return machinePerson{}, false
}

// inDomains says an address may be added without saying it is outside.
func (m machinePeople) inDomains(email string) bool {
	if len(m.Domains) == 0 {
		return true
	}
	_, domain, _ := strings.Cut(email, "@")
	return slices.Contains(m.Domains, domain)
}

// link adds a Slack member ID to the person with that email, making them
// if they aren't there, and takes it from anyone else.
func (m *machinePeople) link(email, slackID, name, by string) {
	for i := range m.People {
		if m.People[i].Email != email {
			m.People[i].Slack = slices.DeleteFunc(m.People[i].Slack, func(id string) bool { return id == slackID })
		}
	}
	i := slices.IndexFunc(m.People, func(p machinePerson) bool { return p.Email == email })
	if i < 0 {
		m.People = append(m.People, machinePerson{Email: email, Name: name, Added: time.Now().UTC(), By: by})
		i = len(m.People) - 1
	}
	if slackID != "" && !slices.Contains(m.People[i].Slack, slackID) {
		m.People[i].Slack = append(m.People[i].Slack, slackID)
	}
	if m.People[i].Name == "" {
		m.People[i].Name = name
	}
}

// appCheckEmail cleans an email address someone typed.
func appCheckEmail(text string) (string, error) {
	email := appPersonID(text)
	if !appIsEmail(email) {
		return "", fmt.Errorf("%q isn't an email address", strings.TrimSpace(text))
	}
	return email, nil
}

// ---------------------------------------------------------------------------
// Slack, by email

// appSlackUserByEmail is the member with that email in the bot's
// workspace, and their name; "" when there's none, or the app lacks
// users:read.email. Tests replace it.
var appSlackUserByEmail = func(botToken, email string) (id, name string) {
	user, err := slack.New(botToken).GetUserByEmail(email)
	if err != nil || user == nil || user.Deleted || user.IsBot {
		return "", ""
	}
	return user.ID, firstNonEmpty(user.RealName, user.Profile.DisplayName, user.Name)
}

// appSlackUserEmail is a member's email, and their name; "" without
// users:read.email. Tests replace it.
var appSlackUserEmail = func(botToken, id string) (email, name string) {
	user, err := slack.New(botToken).GetUserInfo(id)
	if err != nil || user == nil {
		return "", ""
	}
	return strings.ToLower(user.Profile.Email), firstNonEmpty(user.RealName, user.Profile.DisplayName, user.Name)
}

// appWhoByEmail is who someone signing in with an email is in an app:
// the entry of its lists that is theirs, and their name. who is "" when
// they have no level there.
func appWhoByEmail(config appConfig, email string) (who, name string) {
	people := loadMachinePeople()
	person, known := people.person(email)
	name = firstNonEmpty(person.Name, email)
	if known {
		for _, id := range person.ids() {
			if appLevel(config, id) != appLevelNone {
				return id, name
			}
		}
	} else if appLevel(config, email) != appLevelNone {
		return email, name
	}
	// Someone the machine doesn't know by this email yet: the app's
	// workspace may.
	secrets, _ := loadTeamSecrets(config.Name)
	if secrets.SlackBotToken == "" {
		return "", name
	}
	id, slackName := appSlackUserByEmail(secrets.SlackBotToken, email)
	if id == "" || appLevel(config, id) == appLevelNone {
		return "", name
	}
	if !known {
		name = firstNonEmpty(slackName, email)
	}
	changeMachinePeople(func(m *machinePeople) error {
		m.link(email, id, firstNonEmpty(slackName, person.Name), "hi, from Slack")
		return nil
	})
	return id, name
}

// machineLinkSlack finds the email of each Slack member with a level in
// an app whom the machine doesn't know yet, so someone in two workspaces
// is one person. Slack is asked once an hour for each, at most.
func machineLinkSlack(configs []appConfig) {
	type found struct{ email, name, id string }
	var links []found
	people := loadMachinePeople()
	for _, config := range configs {
		secrets, _ := loadTeamSecrets(config.Name)
		if secrets.SlackBotToken == "" {
			continue
		}
		for _, id := range slices.Concat(config.Owners, config.Developers, config.VibeDevelopers, config.Contributors) {
			if appIsEmail(id) || people.find(id) >= 0 || !machineAskSlack(id) {
				continue
			}
			if email, name := appSlackUserEmail(secrets.SlackBotToken, id); appIsEmail(email) {
				links = append(links, found{email, name, id})
			}
		}
	}
	if len(links) == 0 {
		return
	}
	changeMachinePeople(func(m *machinePeople) error {
		for _, l := range links {
			m.link(l.email, l.id, l.name, "hi, from Slack")
		}
		return nil
	})
}

var machineAsked = struct {
	sync.Mutex
	at map[string]time.Time
}{at: map[string]time.Time{}}

// machineAskSlack says whether to ask Slack about a member now.
func machineAskSlack(id string) bool {
	machineAsked.Lock()
	defer machineAsked.Unlock()
	if time.Since(machineAsked.at[id]) < time.Hour {
		return false
	}
	machineAsked.at[id] = time.Now()
	return true
}

// ---------------------------------------------------------------------------
// hi people

const peopleUsage = `usage: hi people                           everyone on the machine, with their levels in each app
       hi people add <email> [--name <name>] [--slack <member ID>] [--outside]
           [--app <app>=owner|pro|vibe|contributor ...] [--invite]
                                          add someone; --invite emails them a sign-in
       hi people rm <email>               remove them from everything: every app's level, their
                                          SSH key, their sign-ins; and from the list
       hi people key <email> <public key>|--remove
                                          their SSH key, for every app where they get a shell
       hi people link <email> <member ID> their Slack account, when Slack's email isn't this one
       hi people domains [<domain>[,<domain>]|--none]
                                          the organisation's email domains`

func runPeople(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return exitCode(peopleList(stdout), stderr)
	}
	var err error
	switch args[0] {
	case "help", "-h", "--help":
		fmt.Fprintln(stdout, peopleUsage)
		return 0
	case "add":
		err = peopleAdd(args[1:], stdout)
	case "rm", "remove":
		if len(args) != 2 {
			err = usageError{peopleUsage}
		} else {
			err = peopleRemove(args[1], adminActor(), stdout)
		}
	case "key":
		err = peopleKeyCommand(args[1:], stdout)
	case "link":
		if len(args) != 3 {
			err = usageError{peopleUsage}
		} else {
			err = peopleLink(args[1], args[2], stdout)
		}
	case "domains":
		err = peopleDomains(args[1:], stdout)
	default:
		fmt.Fprintf(stderr, "hi: unknown people command %q\n\n%s\n", args[0], peopleUsage)
		return 2
	}
	return exitCode(err, stderr)
}

// machineAppConfigs is every app on the machine.
func machineAppConfigs() []appConfig {
	var configs []appConfig
	for _, name := range adminAppNames() {
		if config, err := loadTeam(name); err == nil {
			configs = append(configs, config)
		}
	}
	return configs
}

// machinePersonLevels is someone's level in each app they have one in.
func machinePersonLevels(person machinePerson, configs []appConfig) map[string]string {
	levels := map[string]string{}
	for _, config := range configs {
		best := appLevelNone
		for _, id := range person.ids() {
			if level := appLevel(config, id); appLevelRank(level) > appLevelRank(best) {
				best = level
			}
		}
		if best != appLevelNone {
			levels[config.Name] = best
		}
	}
	return levels
}

func peopleList(stdout io.Writer) error {
	configs := machineAppConfigs()
	machineLinkSlack(configs)
	people := loadMachinePeople()
	if len(people.Domains) > 0 {
		fmt.Fprintf(stdout, "The organisation's domains: %s\n", strings.Join(people.Domains, ", "))
	}
	if len(people.People) == 0 {
		fmt.Fprintln(stdout, "No one yet: hi people add <email> --app <app>=pro")
	}
	for _, person := range people.People {
		var levels []string
		for app, level := range machinePersonLevels(person, configs) {
			levels = append(levels, app+": "+appLevelWords(level))
		}
		sort.Strings(levels)
		signed := "never signed in by email"
		if !person.SignedIn.IsZero() {
			signed = "signed in " + person.SignedIn.Local().Format("2 Jan 15:04")
		}
		fmt.Fprintf(stdout, "%-24s %-30s %s\n", firstNonEmpty(person.Name, "-"), person.Email, strings.Join(levels, ", "))
		fmt.Fprintf(stdout, "%-24s Slack %s; %s; %s\n", "", firstNonEmpty(strings.Join(person.Slack, ", "), "none"), map[bool]string{true: "SSH key " + appSSHKeyLabel(person.SSHKey), false: "no SSH key"}[person.SSHKey != ""], signed)
	}
	// Someone with a level the machine knows only by a Slack member ID.
	var unknown []string
	for _, config := range configs {
		for _, id := range slices.Concat(config.Owners, config.Developers, config.VibeDevelopers, config.Contributors) {
			if people.find(id) < 0 && !slices.Contains(unknown, id) {
				unknown = append(unknown, id)
			}
		}
	}
	if len(unknown) > 0 {
		fmt.Fprintf(stdout, "\nWith a level, but no email yet (the apps' Slack apps need users:read.email, or hi people link <email> <member ID>):\n  %s\n", strings.Join(unknown, ", "))
	}
	return nil
}

// peopleAddRequest is someone to add, from the command line or the
// admin page.
type peopleAddRequest struct {
	Email   string            `json:"email"`
	Name    string            `json:"name"`
	Slack   string            `json:"slack"`
	Outside bool              `json:"outside"`
	Levels  map[string]string `json:"levels"` // app → owner, pro, vibe, contributor
	Invite  bool              `json:"invite"`
	By      string            `json:"-"`
}

func peopleAdd(args []string, stdout io.Writer) error {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return usageError{peopleUsage}
	}
	request := peopleAddRequest{Email: args[0], Levels: map[string]string{}, By: adminActor()}
	for i := 1; i < len(args); i++ {
		flag := args[i]
		value := ""
		if i+1 < len(args) {
			value = args[i+1]
		}
		switch flag {
		case "--name":
			request.Name, i = value, i+1
		case "--slack":
			request.Slack, i = value, i+1
		case "--app":
			app, level, ok := strings.Cut(value, "=")
			if !ok {
				return fmt.Errorf("--app takes <app>=<level>, such as demo=pro, not %q", value)
			}
			request.Levels[app], i = level, i+1
		case "--outside":
			request.Outside = true
		case "--invite":
			request.Invite = true
		default:
			return usageError{peopleUsage}
		}
	}
	person, err := machineAddPerson(request)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "%s (%s) is on the machine's list of people.\n", firstNonEmpty(person.Name, person.Email), person.Email)
	for _, app := range sortedKeys(request.Levels) {
		who := machineWhoIn(app, person)
		level := appParseLevel(request.Levels[app])
		var out strings.Builder
		if level == appLevelOwner {
			err = appOwnersCommand([]string{app, "add", who}, strings.NewReader(""), &out)
		} else {
			err = appAccessCommand([]string{app, who, level}, &out)
		}
		if err != nil {
			fmt.Fprintf(stdout, "  %s: %v\n", app, err)
			continue
		}
		fmt.Fprintf(stdout, "  %s: %s\n", app, strings.TrimSpace(strings.SplitN(out.String(), "\n", 2)[0]))
	}
	if person.SSHKey != "" {
		machineApplyKey(person, stdout)
	}
	if request.Invite {
		for _, line := range machineInvite(person, request.By) {
			fmt.Fprintf(stdout, "  %s\n", line)
		}
	}
	return nil
}

// machineAddPerson puts someone on the list, or changes their name and
// Slack account; their levels are each app's, given by the caller.
func machineAddPerson(request peopleAddRequest) (machinePerson, error) {
	email, err := appCheckEmail(request.Email)
	if err != nil {
		return machinePerson{}, err
	}
	name := strings.TrimSpace(request.Name)
	if len(name) > 80 || strings.ContainsAny(name, "<>\n\r") {
		return machinePerson{}, errors.New("a name is a name, as people call them, up to 80 letters")
	}
	slackID := appPersonID(request.Slack)
	if slackID != "" && appCheckPeople(slackID) != nil {
		return machinePerson{}, fmt.Errorf("a Slack member ID looks like U07ABC123, not %q", request.Slack)
	}
	var person machinePerson
	err = changeMachinePeople(func(m *machinePeople) error {
		if !request.Outside && !m.inDomains(email) {
			if i := m.find(email); i < 0 || !m.People[i].Outside {
				return fmt.Errorf("%s is outside the organisation's domains (%s); to let it in, tick Allow this address, or add --outside on the command line", email, strings.Join(m.Domains, ", "))
			}
		}
		if i := m.find(email); i < 0 || m.People[i].Email != email {
			m.People = append(m.People, machinePerson{Email: email, Added: time.Now().UTC(), By: request.By})
		}
		i := slices.IndexFunc(m.People, func(p machinePerson) bool { return p.Email == email })
		if name != "" {
			m.People[i].Name = name
		}
		if request.Outside {
			m.People[i].Outside = true
		}
		if slackID != "" {
			m.link(email, slackID, name, request.By)
		}
		person = m.People[i]
		return nil
	})
	if err != nil {
		return person, err
	}
	// Their Slack accounts, in the apps' workspaces, so Slack and email
	// are the same person in every app.
	if slackID == "" {
		for _, config := range machineAppConfigs() {
			secrets, _ := loadTeamSecrets(config.Name)
			if secrets.SlackBotToken == "" {
				continue
			}
			if id, slackName := appSlackUserByEmail(secrets.SlackBotToken, email); id != "" {
				changeMachinePeople(func(m *machinePeople) error {
					m.link(email, id, firstNonEmpty(name, slackName), request.By)
					return nil
				})
			}
		}
		person, _ = loadMachinePeople().person(email)
	}
	return person, nil
}

// machineWhoIn is the entry an app's lists should have for someone: a
// Slack member ID of theirs in the app's workspace, so Slack and the lead
// know them, else their email.
func machineWhoIn(app string, person machinePerson) string {
	config, err := loadTeam(app)
	if err != nil {
		return person.Email
	}
	for _, id := range person.ids() {
		if appLevel(config, id) != appLevelNone {
			return id
		}
	}
	secrets, _ := loadTeamSecrets(app)
	if secrets.SlackBotToken != "" && len(person.Slack) > 0 {
		if id, _ := appSlackUserByEmail(secrets.SlackBotToken, person.Email); id != "" {
			return id
		}
	}
	return person.Email
}

// machinePersonIn is the entry an app's lists have for someone, or "".
func machinePersonIn(config appConfig, person machinePerson) string {
	for _, id := range person.ids() {
		if appLevel(config, id) != appLevelNone {
			return id
		}
	}
	return ""
}

// peopleRemove takes someone out of everything: their level in every app
// (an app's only owner stays, said), their SSH keys, their links, and the
// list.
func peopleRemove(text, by string, stdout io.Writer) error {
	people := loadMachinePeople()
	person, ok := people.person(text)
	if !ok {
		id := appPersonID(text)
		if id == "" {
			return usageError{peopleUsage}
		}
		person = machinePerson{Email: id}
		if !appIsEmail(id) {
			person = machinePerson{Slack: []string{id}}
		}
	}
	var kept []string
	for _, config := range machineAppConfigs() {
		for _, id := range person.ids() {
			if id == "" {
				continue
			}
			var out strings.Builder
			var err error
			switch appLevel(config, id) {
			case appLevelNone:
				continue
			case appLevelOwner:
				err = appOwnersCommand([]string{config.Name, "remove", id}, strings.NewReader(""), &out)
			default:
				err = appAccessCommand([]string{config.Name, id, appLevelNone}, &out)
			}
			if err != nil {
				kept = append(kept, config.Name+": "+err.Error())
				continue
			}
			fmt.Fprintf(stdout, "%s: %s\n", config.Name, strings.TrimSpace(out.String()))
		}
	}
	if len(kept) > 0 {
		return fmt.Errorf("%s stays where hi couldn't take them out:\n  %s", firstNonEmpty(person.Name, person.Email), strings.Join(kept, "\n  "))
	}
	if person.Email != "" {
		if err := changeMachinePeople(func(m *machinePeople) error {
			m.People = slices.DeleteFunc(m.People, func(p machinePerson) bool { return p.Email == person.Email })
			return nil
		}); err != nil {
			return err
		}
	}
	fmt.Fprintf(stdout, "%s is off the machine: no level in any app, no SSH key, no sign-ins.\n", firstNonEmpty(person.Name, person.Email, strings.Join(person.Slack, "")))
	return nil
}

func peopleKeyCommand(args []string, stdout io.Writer) error {
	if len(args) < 2 {
		return usageError{peopleUsage}
	}
	key := strings.TrimSpace(strings.Join(args[1:], " "))
	if key == "--remove" {
		key = ""
	} else if data, err := os.ReadFile(key); err == nil {
		key = strings.TrimSpace(string(data))
	}
	person, err := machineSetKey(args[0], key)
	if err != nil {
		return err
	}
	machineApplyKey(person, stdout)
	return nil
}

// machineSetKey keeps someone's public SSH key, or takes it away.
func machineSetKey(text, key string) (machinePerson, error) {
	key = strings.TrimSpace(key)
	if key != "" {
		if strings.Contains(key, "PRIVATE KEY") {
			return machinePerson{}, errors.New("that is a private key; give the public one (the .pub file), and keep the private key to yourself")
		}
		if !appSSHKeyPattern.MatchString(key) {
			return machinePerson{}, errors.New("that isn't an SSH public key; it looks like ssh-ed25519 AAAA… you@laptop (the .pub file)")
		}
	}
	var person machinePerson
	err := changeMachinePeople(func(m *machinePeople) error {
		i := m.find(text)
		if i < 0 {
			return fmt.Errorf("%s isn't on the machine's list of people: hi people add <email>", text)
		}
		m.People[i].SSHKey = key
		person = m.People[i]
		return nil
	})
	return person, err
}

// machineApplyKey puts someone's key into the dev box of each app where
// they get a shell, and takes an old one out where they have none.
func machineApplyKey(person machinePerson, stdout io.Writer) {
	for _, config := range machineAppConfigs() {
		who := machinePersonIn(config, person)
		if who == "" || !appHasShell(config, who) {
			continue
		}
		if err := appSetDevKey(config, who, person.SSHKey); err != nil {
			fmt.Fprintf(stdout, "  %s: %v\n", config.Name, err)
			continue
		}
		if person.SSHKey == "" {
			fmt.Fprintf(stdout, "  %s: their SSH key is out of the dev box\n", config.Name)
		} else {
			fmt.Fprintf(stdout, "  %s: their SSH key is %s\n", config.Name, appSSHKeyLabel(person.SSHKey))
		}
	}
}

func peopleLink(text, slackID string, stdout io.Writer) error {
	email, err := appCheckEmail(text)
	if err != nil {
		return err
	}
	slackID = appPersonID(slackID)
	if err := appCheckPeople(slackID); err != nil || appIsEmail(slackID) {
		return fmt.Errorf("a Slack member ID looks like U07ABC123, not %q", slackID)
	}
	if err := changeMachinePeople(func(m *machinePeople) error {
		if m.find(email) < 0 && !m.inDomains(email) {
			return fmt.Errorf("%s is outside the organisation's domains; hi people add %s --outside first", email, email)
		}
		m.link(email, slackID, "", adminActor())
		return nil
	}); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "%s is %s in Slack.\n", email, slackID)
	return nil
}

func peopleDomains(args []string, stdout io.Writer) error {
	if len(args) == 0 {
		domains := loadMachinePeople().Domains
		if len(domains) == 0 {
			fmt.Fprintln(stdout, "No domains: anyone's address can be added. hi people domains corp.example keeps it to the organisation's.")
		} else {
			fmt.Fprintf(stdout, "The organisation's domains: %s. Someone else is added with --outside.\n", strings.Join(domains, ", "))
		}
		return nil
	}
	domains, err := machineCheckDomains(strings.Join(args, ","))
	if err != nil {
		return err
	}
	before := loadMachinePeople().Domains
	if err := changeMachinePeople(func(m *machinePeople) error { m.Domains = domains; return nil }); err != nil {
		return err
	}
	// The dev boxes reach the organisation's domains (hi server sites org).
	appDevSitesRefresh(before...)
	return peopleDomains(nil, stdout)
}

// machineCheckDomains reads a list of domains; --none is none.
func machineCheckDomains(text string) ([]string, error) {
	if strings.TrimSpace(text) == "--none" {
		return nil, nil
	}
	var domains []string
	for _, domain := range splitList(strings.ToLower(text)) {
		domain = strings.TrimPrefix(strings.TrimSpace(domain), "@")
		if domain == "" {
			continue
		}
		if !appIsEmail("x@" + domain) {
			return nil, fmt.Errorf("%q isn't a domain, such as corp.example", domain)
		}
		if !slices.Contains(domains, domain) {
			domains = append(domains, domain)
		}
	}
	return domains, nil
}
