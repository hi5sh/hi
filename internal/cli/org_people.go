package cli

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
	"syscall"
	"time"
)

// Every org has an owner, one person with their Slack member ID and their
// email, set at hi server init (hi_admin.md, The org's owner). The owner adds
// and removes the org's admins. An admin is an approver too, and an admin
// of the admin page on the server's machine: one list, kept in the org's
// people.json, so that page keeps no list of its own.

// orgPerson is the org's owner or one of its admins.
type orgPerson struct {
	// Name is their hi user name, as approvals and the audit record them.
	Name  string    `json:"name"`
	Slack string    `json:"slack"`
	Email string    `json:"email,omitempty"`
	Added time.Time `json:"added"`
	By    string    `json:"by,omitempty"`
}

type orgPeople struct {
	Owner  *orgPerson  `json:"owner,omitempty"`
	Admins []orgPerson `json:"admins,omitempty"`
}

// all is the owner, then the admins.
func (p orgPeople) all() []orgPerson {
	var all []orgPerson
	if p.Owner != nil {
		all = append(all, *p.Owner)
	}
	return append(all, p.Admins...)
}

// find is the owner or an admin, by hi user name or Slack member ID.
func (p orgPeople) find(who string) (orgPerson, bool) {
	who = strings.TrimSpace(strings.Trim(strings.TrimSpace(who), "<@>"))
	for _, person := range p.all() {
		if who != "" && (strings.EqualFold(person.Name, who) || person.Slack == who) {
			return person, true
		}
	}
	return orgPerson{}, false
}

func (p orgPeople) isOwner(who string) bool {
	return p.Owner != nil && who != "" && (strings.EqualFold(p.Owner.Name, who) || p.Owner.Slack == who)
}

func orgPeoplePath(dir string) string { return filepath.Join(dir, "people.json") }

func loadOrgPeople(dir string) orgPeople {
	var people orgPeople
	if data, err := os.ReadFile(orgPeoplePath(dir)); err == nil {
		json.Unmarshal(data, &people)
	}
	return people
}

// orgPeopleInput is one change: the owner, or an admin added or removed.
type orgPeopleInput struct {
	As     string `json:"as"`
	Action string `json:"action"` // owner, add, or remove
	Name   string `json:"name"`
	Slack  string `json:"slack"`
	Email  string `json:"email"`
}

// orgNameFromEmail is a hi user name from an address's first part.
func orgNameFromEmail(email string) string {
	local, _, _ := strings.Cut(strings.ToLower(strings.TrimSpace(email)), "@")
	var name strings.Builder
	for _, char := range local {
		if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || char == '-' || char == '_' || char == '.' {
			name.WriteRune(char)
		}
	}
	return name.String()
}

func orgCheckEmail(email string) (string, error) {
	email = strings.TrimSpace(email)
	local, domain, ok := strings.Cut(email, "@")
	if !ok || local == "" || !strings.Contains(domain, ".") || strings.ContainsAny(email, " <>\"`,;") || len(email) > 200 {
		return "", fmt.Errorf("%q isn't an email address", email)
	}
	return email, nil
}

// orgCheckPerson cleans what a change says about a person.
func orgCheckPerson(input orgPeopleInput, needEmail bool) (orgPerson, error) {
	slackID := strings.TrimSpace(strings.Trim(strings.TrimSpace(input.Slack), "<@>"))
	if !slackIDPattern.MatchString(slackID) {
		return orgPerson{}, fmt.Errorf("%q isn't a Slack member ID, such as U012AB3CD (in Slack: their profile, ⋮, Copy member ID)", input.Slack)
	}
	person := orgPerson{Slack: slackID, Added: computeNow().UTC(), By: input.As}
	if input.Email != "" || needEmail {
		email, err := orgCheckEmail(input.Email)
		if err != nil {
			return orgPerson{}, err
		}
		person.Email = email
	}
	person.Name = strings.ToLower(strings.TrimSpace(input.Name))
	if person.Name == "" {
		person.Name = orgNameFromEmail(person.Email)
	}
	if !validServerName(person.Name) {
		return orgPerson{}, fmt.Errorf("give their hi user name with --name, as approvals record it: lowercase letters, digits, - _ . (%q isn't one)", person.Name)
	}
	return person, nil
}

// orgPeopleResult is what a change did, for the audit and Slack.
type orgPeopleResult struct {
	People    orgPeople         `json:"people"`
	Text      string            `json:"text"`
	action    string            // the audit's action
	subject   string            // and subject
	approve   map[string]string // approvers to add: Slack member ID → hi user name
	unapprove []string          // and to remove
	said      string            // said in the approvers' channel
}

// applyOrgPeople makes one change to people.json, under its lock.
func applyOrgPeople(dir string, input orgPeopleInput) (orgPeopleResult, error) {
	os.MkdirAll(dir, 0o700)
	lock, err := os.OpenFile(filepath.Join(dir, ".people.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err == nil {
		syscall.Flock(int(lock.Fd()), syscall.LOCK_EX)
		defer func() { syscall.Flock(int(lock.Fd()), syscall.LOCK_UN); lock.Close() }()
	}
	people := loadOrgPeople(dir)
	result := orgPeopleResult{approve: map[string]string{}}
	switch input.Action {
	case "owner":
		person, err := orgCheckPerson(input, true)
		if err != nil {
			return result, err
		}
		if people.Owner != nil && people.Owner.Slack == person.Slack {
			people.Owner.Email, people.Owner.Name = person.Email, person.Name
			result.Text = fmt.Sprintf("%s is still the server's owner; their email is %s.", person.Name, person.Email)
			result.action, result.subject = "updated owner", person.Name
		} else {
			old := people.Owner
			people.Admins = slices.DeleteFunc(people.Admins, func(admin orgPerson) bool { return admin.Slack == person.Slack })
			people.Owner = &person
			result.action, result.subject = "set owner", person.Name
			result.Text = fmt.Sprintf("%s (%s) owns the server now.", person.Name, person.Email)
			if old != nil {
				// The owner before stays an admin; the new owner can remove them.
				old.By = input.As
				people.Admins = append(people.Admins, *old)
				result.Text += fmt.Sprintf(" %s, the owner before, is an admin of it.", old.Name)
				result.said = fmt.Sprintf("%s handed the server on: %s owns it now, and %s is an admin.", input.As, person.Name, old.Name)
			}
		}
		result.approve[person.Slack] = person.Name
	case "add":
		person, err := orgCheckPerson(input, false)
		if err != nil {
			return result, err
		}
		if people.isOwner(person.Slack) || people.isOwner(person.Name) {
			return result, fmt.Errorf("%s owns the server; an owner is an admin already", person.Name)
		}
		if other, ok := people.find(person.Name); ok && other.Slack != person.Slack {
			return result, fmt.Errorf("%s is the hi user name of the admin with Slack member ID %s", person.Name, other.Slack)
		}
		people.Admins = slices.DeleteFunc(people.Admins, func(admin orgPerson) bool { return admin.Slack == person.Slack })
		people.Admins = append(people.Admins, person)
		result.action, result.subject = "added admin", person.Name
		result.Text = fmt.Sprintf("%s is an admin of the server: they approve, in Slack and on the admin page, and sign in to the admin page on this machine.", person.Name)
		result.approve[person.Slack] = person.Name
		result.said = fmt.Sprintf("%s made %s an admin of the server.", input.As, person.Name)
	case "remove":
		person, ok := people.find(firstNonEmpty(input.Slack, input.Name))
		if !ok {
			return result, fmt.Errorf("%s isn't one of the server's admins", firstNonEmpty(input.Slack, input.Name))
		}
		if people.isOwner(person.Slack) {
			return result, errors.New("the owner can't be removed; hand the server on first: hi server owner <Slack ID> --email <address>")
		}
		people.Admins = slices.DeleteFunc(people.Admins, func(admin orgPerson) bool { return admin.Slack == person.Slack })
		result.action, result.subject = "removed admin", person.Name
		result.Text = fmt.Sprintf("%s isn't an admin of the server any more, nor an approver.", person.Name)
		result.unapprove = []string{person.Slack}
		result.said = fmt.Sprintf("%s took %s off the server's admins.", input.As, person.Name)
	default:
		return result, fmt.Errorf("no such change to the server's people: %q", input.Action)
	}
	data, _ := json.MarshalIndent(people, "", "  ")
	if err := writeFileAtomic(orgPeoplePath(dir), append(data, '\n'), 0o600); err != nil {
		return result, err
	}
	result.People = people
	return result, nil
}

// changeOrgPeople makes a change on the running server: in the audit, in
// the approvers, and said in the approvers' channel.
func (s *orgServer) changeOrgPeople(input orgPeopleInput) (orgPeopleResult, error) {
	result, err := applyOrgPeople(s.dir, input)
	if err != nil {
		return result, err
	}
	s.audit(input.As, result.action, result.subject, "")
	orgSyncApprovers(s.dir, s, result.approve, result.unapprove)
	if result.said != "" {
		s.notifyChannel(result.said)
	}
	return result, nil
}

// orgSyncApprovers makes the org's owner and admins approvers in Slack,
// in slack.json and, with the server running, in its memory. Without
// Slack set up there's nothing to do: hi server slack setup adds them.
func orgSyncApprovers(dir string, server *orgServer, add map[string]string, remove []string) {
	if len(add) == 0 && len(remove) == 0 {
		return
	}
	config, err := loadSlackConfig(dir)
	if err != nil || config == nil {
		return
	}
	for id, name := range add {
		config.Approvers[id] = name
	}
	for _, id := range remove {
		delete(config.Approvers, id)
	}
	if saveSlackConfig(dir, *config) != nil {
		return
	}
	if server != nil && server.slack != nil {
		server.slack.mu.Lock()
		server.slack.config.Approvers = config.Approvers
		server.slack.mu.Unlock()
	}
}

// orgAuditOffline appends to the audit log while the server isn't running.
func orgAuditOffline(dir, actor, action, subject string) {
	data, _ := json.Marshal(auditEntry{Time: computeNow().UTC(), Actor: actor, Action: action, Subject: subject})
	file, err := os.OpenFile(filepath.Join(dir, "audit.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err == nil {
		file.Write(append(data, '\n'))
		file.Close()
	}
}

// orgChangePeople makes a change through the running server, or in its
// files while it isn't running.
func orgChangePeople(dir string, input orgPeopleInput) (orgPeopleResult, error) {
	if _, err := os.Stat(filepath.Join(dir, "admin.sock")); err == nil {
		client, err := adminClient(dir)
		if err != nil {
			return orgPeopleResult{}, err
		}
		var result orgPeopleResult
		if err := doJSON(client, http.MethodPost, "http://hi-server/admin/people", input, &result, nil); err != nil {
			return result, err
		}
		return result, nil
	}
	result, err := applyOrgPeople(dir, input)
	if err != nil {
		return result, err
	}
	orgAuditOffline(dir, input.As, result.action, result.subject)
	orgSyncApprovers(dir, nil, result.approve, result.unapprove)
	return result, nil
}

func (s *orgServer) peopleAdminRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /admin/people", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, loadOrgPeople(s.dir))
	})
	mux.HandleFunc("POST /admin/people", func(w http.ResponseWriter, r *http.Request) {
		var input orgPeopleInput
		json.NewDecoder(io.LimitReader(r.Body, serverMaxBody)).Decode(&input)
		if input.As == "" {
			writeAPIError(w, http.StatusBadRequest, "say who is acting with --as")
			return
		}
		result, err := s.changeOrgPeople(input)
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, result)
	})
}

// orgNoOwnerLine says how an org made before owners gets one.
const orgNoOwnerLine = "The server has no owner yet. On this machine: hi server owner <Slack member ID> --email <address>"

// ---------------------------------------------------------------------------
// commands

// orgOwnerCommand is hi server owner: who owns the org, or hand it on.
func orgOwnerCommand(args []string, stdout, stderr io.Writer) error {
	flags, dirFlag := orgFlags("owner", stderr)
	email := flags.String("email", "", "the owner's email")
	name := flags.String("name", "", "the owner's hi user name (default: from their email)")
	as := flags.String("as", currentUserName(), "who is acting")
	positional, err := flags.parse(args)
	if err != nil {
		return err
	}
	dir, err := orgDirectory(*dirFlag)
	if err != nil {
		return err
	}
	if !fileExists(filepath.Join(dir, "config.json")) {
		return fmt.Errorf("no server in %s; run `hi server init` first", dir)
	}
	switch len(positional) {
	case 0:
		people := loadOrgPeople(dir)
		if people.Owner == nil {
			fmt.Fprintln(stdout, orgNoOwnerLine)
			return nil
		}
		fmt.Fprintf(stdout, "%s owns the server: Slack %s, %s.\n", people.Owner.Name, people.Owner.Slack, people.Owner.Email)
		return nil
	case 1:
		result, err := orgChangePeople(dir, orgPeopleInput{As: *as, Action: "owner", Slack: positional[0], Email: *email, Name: *name})
		if err != nil {
			return err
		}
		fmt.Fprintln(stdout, result.Text)
		return nil
	}
	return usageError{"usage: hi server owner [<Slack member ID> --email <address> [--name <hi user name>]]"}
}

// orgAdminsCommand is hi server admins: the owner and the admins, or a change.
func orgAdminsCommand(args []string, stdout, stderr io.Writer) error {
	flags, dirFlag := orgFlags("admins", stderr)
	email := flags.String("email", "", "their email")
	name := flags.String("name", "", "their hi user name")
	as := flags.String("as", currentUserName(), "who is acting")
	positional, err := flags.parse(args)
	if err != nil {
		return err
	}
	dir, err := orgDirectory(*dirFlag)
	if err != nil {
		return err
	}
	if !fileExists(filepath.Join(dir, "config.json")) {
		return fmt.Errorf("no server in %s; run `hi server init` first", dir)
	}
	usage := usageError{"usage: hi server admins [add <Slack member ID> --name <hi user name> [--email <address>] | remove <Slack member ID or name>]"}
	switch {
	case len(positional) == 0:
		people := loadOrgPeople(dir)
		if people.Owner == nil {
			fmt.Fprintln(stdout, orgNoOwnerLine)
		}
		table := newTable(stdout)
		fmt.Fprintln(table, "NAME\tSLACK\tEMAIL\t")
		for _, person := range people.all() {
			role := ""
			if people.isOwner(person.Slack) {
				role = "owner"
			}
			fmt.Fprintf(table, "%s\t%s\t%s\t%s\n", person.Name, person.Slack, firstNonEmpty(person.Email, "-"), role)
		}
		return table.Flush()
	case len(positional) == 2 && (positional[0] == "add" || positional[0] == "remove"):
		input := orgPeopleInput{As: *as, Action: positional[0], Slack: positional[1], Email: *email, Name: *name}
		if positional[0] == "remove" {
			input.Slack, input.Name = "", positional[1]
		} else if *name == "" && *email == "" {
			return usage
		}
		result, err := orgChangePeople(dir, input)
		if err != nil {
			return err
		}
		fmt.Fprintln(stdout, result.Text)
		return nil
	}
	return usage
}
