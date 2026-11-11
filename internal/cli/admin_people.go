package cli

// People on the admin page (docs/specs/approved/hi_people.md): everyone
// on the machine, one row a person, joined by email across the apps and
// their Slack workspaces; a person's page with their level in each app,
// their SSH key, their sign-ins, and Remove from everything; and how the
// machine sends email.

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

// adminPersonRow is one person on the page. Key is their email, or, for
// someone the machine knows only by a Slack member ID, that.
type adminPersonRow struct {
	Key      string            `json:"key"`
	Email    string            `json:"email,omitempty"`
	Name     string            `json:"name,omitempty"`
	Slack    []string          `json:"slack"`
	SSHKey   string            `json:"ssh_key,omitempty"` // its label, never the key
	Outside  bool              `json:"outside,omitempty"`
	SignedIn time.Time         `json:"signed_in,omitzero"`
	Invited  time.Time         `json:"invited,omitzero"`
	Admin    bool              `json:"admin,omitempty"`
	Levels   map[string]string `json:"levels"` // app → owner, pro, vibe, contributor
	// IDs is the entry for them in each app's lists.
	IDs map[string]string `json:"ids"`
}

// adminPeopleView is the People view.
type adminPeopleView struct {
	Apps    []string         `json:"apps"`
	People  []adminPersonRow `json:"people"`
	Domains []string         `json:"domains"`
	Mail    bool             `json:"mail"` // the machine sends email
	// NoEmail says how many apps' Slack apps lack users:read.email.
	NoEmail []string `json:"no_email,omitempty"`
}

var adminScopesCache = struct {
	sync.Mutex
	missing map[string][]string
	at      map[string]time.Time
}{missing: map[string][]string{}, at: map[string]time.Time{}}

// adminLacksEmailScope says an app's Slack app can't find people by
// email; Slack is asked once an hour.
func adminLacksEmailScope(config appConfig) bool {
	adminScopesCache.Lock()
	missing, at := adminScopesCache.missing[config.Name], adminScopesCache.at[config.Name]
	adminScopesCache.Unlock()
	if time.Since(at) > time.Hour {
		secrets, _ := loadTeamSecrets(config.Name)
		if secrets.SlackBotToken == "" {
			return false
		}
		missing = appMissingScopes(secrets.SlackBotToken)
		adminScopesCache.Lock()
		adminScopesCache.missing[config.Name], adminScopesCache.at[config.Name] = missing, time.Now()
		adminScopesCache.Unlock()
	}
	return slices.Contains(missing, "users:read.email")
}

func (s *adminServer) peopleView() adminPeopleView {
	configs := machineAppConfigs()
	machineLinkSlack(configs)
	directory := loadMachinePeople()
	_, mailOn := loadMailSettings()
	view := adminPeopleView{Apps: []string{}, People: []adminPersonRow{}, Domains: directory.Domains, Mail: mailOn}
	if view.Domains == nil {
		view.Domains = []string{}
	}
	admins := map[string]bool{}
	if config, ok := loadAdminConfig(); ok {
		for _, admin := range config.Admins {
			if admin.Email != "" {
				admins[strings.ToLower(admin.Email)] = true
			}
			if admin.Slack != "" {
				admins[admin.Slack] = true
			}
		}
	}
	rows := map[string]*adminPersonRow{}
	for _, person := range directory.People {
		row := &adminPersonRow{Key: person.Email, Email: person.Email, Name: person.Name, Slack: slices.Clone(person.Slack), Outside: person.Outside,
			SignedIn: person.SignedIn, Invited: person.Invited, Levels: map[string]string{}, IDs: map[string]string{}}
		if row.Slack == nil {
			row.Slack = []string{}
		}
		if person.SSHKey != "" {
			row.SSHKey = appSSHKeyLabel(person.SSHKey)
		}
		for _, id := range person.ids() {
			row.Admin = row.Admin || admins[id]
		}
		rows[person.Email] = row
	}
	for _, config := range configs {
		view.Apps = append(view.Apps, config.Name)
		if adminLacksEmailScope(config) {
			view.NoEmail = append(view.NoEmail, config.Name)
		}
		ids := slices.Concat(config.Owners, config.Developers, config.VibeDevelopers, config.Contributors)
		names := s.names.of(config, ids)
		for _, id := range ids {
			key := id
			if person, ok := directory.person(id); ok {
				key = person.Email
			}
			row := rows[key]
			if row == nil {
				row = &adminPersonRow{Key: id, Name: names[id], Slack: []string{}, Levels: map[string]string{}, IDs: map[string]string{}, Admin: admins[id]}
				if appIsEmail(id) {
					row.Email = id
				} else {
					row.Slack = []string{id}
				}
				rows[key] = row
			}
			row.Name = firstNonEmpty(row.Name, names[id])
			level := adminPageLevel(config, id)
			if adminLevelRank(level) > adminLevelRank(row.Levels[config.Name]) {
				row.Levels[config.Name], row.IDs[config.Name] = level, id
			}
			if row.SSHKey == "" {
				if label := appDevKeyLabel(config.Name, id); !strings.HasPrefix(label, "no SSH key") {
					row.SSHKey = label
				}
			}
		}
	}
	for _, row := range rows {
		view.People = append(view.People, *row)
	}
	sort.Slice(view.People, func(i, j int) bool {
		return strings.ToLower(firstNonEmpty(view.People[i].Name, view.People[i].Key)) < strings.ToLower(firstNonEmpty(view.People[j].Name, view.People[j].Key))
	})
	return view
}

func adminLevelRank(level string) int {
	return slices.Index([]string{"", "none", "contributor", "vibe", "pro", "owner"}, level)
}

// adminPersonChange is one change to a person from the page.
type adminPersonChange struct {
	Action  string            `json:"action"` // add, update, levels, key, invite, remove, domains
	Key     string            `json:"key"`    // the person: an email, or a Slack member ID
	Email   string            `json:"email"`
	Name    string            `json:"name"`
	Slack   string            `json:"slack"`
	Outside bool              `json:"outside"`
	Levels  map[string]string `json:"levels"` // app → owner, pro, vibe, contributor, none
	Invite  bool              `json:"invite"`
	SSHKey  string            `json:"ssh_key"`
	Domains string            `json:"domains"`
}

func (s *adminServer) changePerson(admin adminPerson, change adminPersonChange) (map[string]any, error) {
	by := admin.Name + " on the admin page"
	switch change.Action {
	case "domains":
		domains, err := machineCheckDomains(firstNonEmpty(change.Domains, "--none"))
		if err != nil {
			return nil, err
		}
		if err := changeMachinePeople(func(m *machinePeople) error { m.Domains = domains; return nil }); err != nil {
			return nil, err
		}
		adminRecord(admin.Name, "", "set the organisation's email domains: "+firstNonEmpty(strings.Join(domains, ", "), "none"))
		return map[string]any{"text": "The organisation's domains: " + firstNonEmpty(strings.Join(domains, ", "), "none, so any address") + "."}, nil
	case "add":
		person, err := machineAddPerson(peopleAddRequest{Email: change.Email, Name: change.Name, Slack: change.Slack, Outside: change.Outside, By: by})
		if err != nil {
			return nil, err
		}
		adminRecord(admin.Name, "", "added "+person.Email+" to the machine's people")
		lines := s.setPersonLevels(admin, person, change.Levels)
		if change.Invite {
			lines = append(lines, machineInvite(person, by)...)
		}
		return map[string]any{"key": person.Email, "text": firstNonEmpty(person.Name, person.Email) + " is added." + adminLines(lines)}, nil
	}
	directory := loadMachinePeople()
	person, known := directory.person(change.Key)
	switch change.Action {
	case "update":
		// An email for someone known only in Slack, or a new name.
		if !known {
			slackID := appPersonID(change.Key)
			if appIsEmail(slackID) || appCheckPeople(slackID) != nil {
				return nil, fmt.Errorf("no one %q on the machine", change.Key)
			}
			added, err := machineAddPerson(peopleAddRequest{Email: change.Email, Name: change.Name, Slack: slackID, Outside: change.Outside, By: by})
			if err != nil {
				return nil, err
			}
			adminRecord(admin.Name, "", "gave "+slackID+" the email "+added.Email)
			return map[string]any{"key": added.Email, "text": firstNonEmpty(added.Name, slackID) + " is " + added.Email + " now."}, nil
		}
		name := strings.TrimSpace(change.Name)
		if len(name) > 80 || strings.ContainsAny(name, "<>\n\r") {
			return nil, errors.New("a name is a name, as people call them, up to 80 letters")
		}
		if err := changeMachinePeople(func(m *machinePeople) error {
			if i := m.find(person.Email); i >= 0 {
				m.People[i].Name = firstNonEmpty(name, m.People[i].Name)
				m.People[i].Outside = change.Outside || m.People[i].Outside
			}
			return nil
		}); err != nil {
			return nil, err
		}
		if slackID := appPersonID(change.Slack); slackID != "" {
			if err := peopleLink(person.Email, slackID, &strings.Builder{}); err != nil {
				return nil, err
			}
		}
		return map[string]any{"key": person.Email, "text": "Saved."}, nil
	case "levels":
		if !known {
			id := appPersonID(change.Key)
			if appCheckPeople(id) != nil {
				return nil, fmt.Errorf("no one %q on the machine", change.Key)
			}
			person = machinePerson{Email: id}
			if !appIsEmail(id) {
				person = machinePerson{Slack: []string{id}}
			}
		}
		lines := s.setPersonLevels(admin, person, change.Levels)
		return map[string]any{"text": strings.TrimSpace(adminLines(lines))}, nil
	case "key":
		if !known {
			return nil, errors.New("give them an email first: an SSH key is kept for a person on the machine's list")
		}
		updated, err := machineSetKey(person.Email, change.SSHKey)
		if err != nil {
			return nil, err
		}
		var out strings.Builder
		machineApplyKey(updated, &out)
		said := map[bool]string{true: "gave " + firstNonEmpty(person.Name, person.Email) + " an SSH key for the dev boxes", false: "took " + firstNonEmpty(person.Name, person.Email) + "'s SSH key out of the dev boxes"}[change.SSHKey != ""]
		adminRecord(admin.Name, "", said)
		return map[string]any{"text": strings.TrimSpace(firstNonEmpty(out.String(), "Kept: it goes into a dev box when they're an owner or a Pro Dev there."))}, nil
	case "invite":
		if !known {
			return nil, errors.New("give them an email first")
		}
		if _, ok := loadMailSettings(); !ok {
			return nil, mailErrOff
		}
		return map[string]any{"text": "Sent: " + strings.Join(machineInvite(person, by), "; ") + "."}, nil
	case "remove":
		if !known {
			id := appPersonID(change.Key)
			if appCheckPeople(id) != nil {
				return nil, fmt.Errorf("no one %q on the machine", change.Key)
			}
			person = machinePerson{Email: id}
			if !appIsEmail(id) {
				person = machinePerson{Slack: []string{id}}
			}
		}
		none := map[string]string{}
		for _, config := range machineAppConfigs() {
			if machinePersonIn(config, person) != "" {
				none[config.Name] = "none"
			}
		}
		lines := s.setPersonLevels(admin, person, none)
		for _, config := range machineAppConfigs() {
			if who := machinePersonIn(config, person); who != "" {
				return nil, fmt.Errorf("%s stays in %s, where hi couldn't take them out:%s", firstNonEmpty(person.Name, person.Email, who), config.Name, adminLines(lines))
			}
		}
		if person.Email != "" && known {
			if err := changeMachinePeople(func(m *machinePeople) error {
				m.People = slices.DeleteFunc(m.People, func(p machinePerson) bool { return p.Email == person.Email })
				return nil
			}); err != nil {
				return nil, err
			}
		}
		adminRecord(admin.Name, "", "removed "+firstNonEmpty(person.Name, person.Email, strings.Join(person.Slack, ""))+" from everything on the machine")
		return map[string]any{"text": firstNonEmpty(person.Name, person.Email, strings.Join(person.Slack, "")) + " is off the machine: no level in any app, no SSH key, no sign-ins."}, nil
	}
	return nil, fmt.Errorf("no action %q", change.Action)
}

// setPersonLevels gives someone a level in each app, each the app's own
// change, said in its channel; their SSH key goes where they get a shell.
func (s *adminServer) setPersonLevels(admin adminPerson, person machinePerson, levels map[string]string) []string {
	var lines []string
	for _, app := range sortedKeys(levels) {
		level := levels[app]
		if level == "developer" {
			level = "pro"
		}
		config, err := loadTeam(app)
		if err != nil {
			lines = append(lines, app+": no such app")
			continue
		}
		who := machinePersonIn(config, person)
		if who == "" {
			if level == "none" {
				continue
			}
			who = person.Email
			if who == "" && len(person.Slack) > 0 {
				who = person.Slack[0]
			} else if who != "" {
				who = machineWhoIn(app, person)
			}
		}
		if _, err := s.adminChangeSetting(admin, adminSettingChange{App: app, What: "level", Person: who, Level: level}); err != nil {
			lines = append(lines, app+": "+err.Error())
			continue
		}
		lines = append(lines, app+": "+map[bool]string{true: "no level", false: adminLevelWords[level]}[level == "none"])
	}
	if person.Email != "" {
		if fresh, ok := loadMachinePeople().person(person.Email); ok && fresh.SSHKey != "" {
			machineApplyKey(fresh, &strings.Builder{})
		}
	}
	return lines
}

func adminLines(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	return "\n" + strings.Join(lines, "\n")
}

// adminMailView is how the machine sends email, without the password.
type adminMailView struct {
	On          bool   `json:"on"`
	Host        string `json:"host,omitempty"`
	Port        int    `json:"port,omitempty"`
	Security    string `json:"security,omitempty"`
	Username    string `json:"username,omitempty"`
	PasswordSet bool   `json:"password_set,omitempty"`
	From        string `json:"from,omitempty"`
	Name        string `json:"name,omitempty"`
}

func adminMailViewOf() adminMailView {
	settings, ok := loadMailSettings()
	if !ok {
		return adminMailView{}
	}
	return adminMailView{On: true, Host: settings.Host, Port: settings.Port, Security: settings.Security, Username: settings.Username,
		PasswordSet: settings.Password != "", From: settings.From, Name: settings.Name}
}

// adminSlackPeopleCache keeps the workspaces' people for the picker.
var adminSlackPeopleCache = struct {
	sync.Mutex
	list []adminSlackPerson
	at   time.Time
}{}

// adminSlackEveryone is everyone in the apps' Slack workspaces, once
// each, for Add person's picker.
func adminSlackEveryone() []adminSlackPerson {
	adminSlackPeopleCache.Lock()
	defer adminSlackPeopleCache.Unlock()
	if time.Since(adminSlackPeopleCache.at) < 10*time.Minute && adminSlackPeopleCache.list != nil {
		return adminSlackPeopleCache.list
	}
	seen := map[string]bool{}
	list := []adminSlackPerson{}
	tokens := map[string]bool{}
	for _, config := range machineAppConfigs() {
		secrets, _ := loadTeamSecrets(config.Name)
		if secrets.SlackBotToken == "" || tokens[secrets.SlackBotToken] {
			continue
		}
		tokens[secrets.SlackBotToken] = true
		people, err := adminSlackPeople(secrets.SlackBotToken)
		if err != nil {
			continue
		}
		for _, person := range people {
			if seen[person.ID] {
				continue
			}
			seen[person.ID] = true
			person.App = config.Name
			list = append(list, person)
		}
	}
	adminSlackPeopleCache.list, adminSlackPeopleCache.at = list, time.Now()
	return list
}

func (s *adminServer) peopleRoutes(mux *http.ServeMux, reply func(http.ResponseWriter, int, any),
	decode func(http.ResponseWriter, *http.Request, any) bool,
	signedIn func(func(http.ResponseWriter, *http.Request, adminPerson)) http.HandlerFunc) {
	failure := func(w http.ResponseWriter, status int, err error) {
		reply(w, status, map[string]string{"error": err.Error()})
	}
	mux.HandleFunc("GET /api/people/slack", signedIn(func(w http.ResponseWriter, r *http.Request, admin adminPerson) {
		reply(w, http.StatusOK, adminSlackEveryone())
	}))
	mux.HandleFunc("POST /api/person", signedIn(func(w http.ResponseWriter, r *http.Request, admin adminPerson) {
		var change adminPersonChange
		if !decode(w, r, &change) {
			return
		}
		answer, err := s.changePerson(admin, change)
		if err != nil {
			failure(w, http.StatusBadRequest, err)
			return
		}
		reply(w, http.StatusOK, answer)
	}))
	mux.HandleFunc("GET /api/mail", signedIn(func(w http.ResponseWriter, r *http.Request, admin adminPerson) {
		reply(w, http.StatusOK, adminMailViewOf())
	}))
	mux.HandleFunc("POST /api/mail", signedIn(func(w http.ResponseWriter, r *http.Request, admin adminPerson) {
		var request struct {
			mailSettings
			Off  bool   `json:"off"`
			Test string `json:"test"`
		}
		if !decode(w, r, &request) {
			return
		}
		switch {
		case request.Test != "":
			if err := adminMailTest(request.Test); err != nil {
				failure(w, http.StatusBadRequest, err)
				return
			}
			adminRecord(admin.Name, "", "sent a test email to "+request.Test)
			reply(w, http.StatusOK, map[string]string{"text": "Sent to " + request.Test + ". If it isn't there in a minute, look in spam: the From domain needs SPF and DKIM for this server."})
		case request.Off:
			if err := saveMailSettings(mailSettings{}); err != nil {
				failure(w, http.StatusInternalServerError, err)
				return
			}
			adminRecord(admin.Name, "", "turned email off")
			reply(w, http.StatusOK, map[string]string{"text": "The machine sends no email now: people sign in with Slack links."})
		default:
			if err := adminMailSet(request.mailSettings, admin.Name, nil); err != nil {
				failure(w, http.StatusBadRequest, err)
				return
			}
			reply(w, http.StatusOK, map[string]string{"text": "Saved. Send yourself a test."})
		}
	}))
}
