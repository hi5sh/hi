package cli

// The admin page on the org server's machine (hi_admin.md, release 2):
// Requests, Running, and Spend on the right; the org's people, keys, and
// rules on the left; its audit in Activity. Everything goes through the
// server's admin socket, as the hi org commands on its machine do, under
// the admin's own hi user name, so an approval here is the same as
// hi org approve and nobody approves their own request.

import (
	"bufio"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// adminOrgCall asks the org server through its admin socket.
func adminOrgCall(method, path string, body, result any) error {
	dir := adminOrgDir()
	if dir == "" {
		return errors.New("this machine runs no org server")
	}
	client, err := adminClient(dir)
	if err != nil {
		return errors.New("the org server isn't running: " + orgRestartLine())
	}
	return doJSON(client, method, "http://hi-org"+path, body, result, nil)
}

// adminOrgSummary is the org as /api/state carries it, every few seconds:
// enough for Home's card and the icons' numbers.
type adminOrgSummary struct {
	Up          bool    `json:"up"`
	Error       string  `json:"error,omitempty"`
	Owner       string  `json:"owner,omitempty"`
	NoOwner     bool    `json:"no_owner,omitempty"`
	Waiting     int     `json:"waiting"`
	Running     int     `json:"running"`
	RatePerHour float64 `json:"rate_per_hour"`
	Today       float64 `json:"today"`
	Month       float64 `json:"month"`
	MonthBudget float64 `json:"month_budget"`
	Service     string  `json:"service"`   // user, system, or none
	Restart     string  `json:"restart"`   // how to restart it on the machine
	Incidents   int     `json:"incidents"` // open: what its check warns of
	// IncidentsOpened is when each open one opened, as on an app.
	IncidentsOpened []time.Time `json:"incidents_opened"`
}

func adminOrgService() string {
	switch {
	case fileExists(orgUserUnitPath()):
		return "user"
	case systemOrgUnit() != "":
		return "system"
	}
	return "none"
}

func adminOrgSummaryOf() *adminOrgSummary {
	dir := adminOrgDir()
	if dir == "" {
		return nil
	}
	people := loadOrgPeople(dir)
	summary := &adminOrgSummary{NoOwner: people.Owner == nil, Service: adminOrgService(), Restart: orgRestartLine()}
	if people.Owner != nil {
		summary.Owner = people.Owner.Name
	}
	var live liveSnapshot
	if err := adminOrgCall(http.MethodGet, "/admin/live", nil, &live); err != nil {
		summary.Error = err.Error()
		return summary
	}
	summary.Up = true
	for _, incident := range adminOrgIncidents(time.Now()) {
		if incident.Status == "open" {
			summary.Incidents++
			summary.IncidentsOpened = append(summary.IncidentsOpened, incident.Opened)
		}
	}
	summary.Running, summary.RatePerHour = len(live.Running), live.RatePerHour
	summary.Today, summary.Month, summary.MonthBudget = live.Today, live.Month+live.ModelsMonth, live.MonthBudget
	for _, waiting := range live.Waiting {
		if waiting.State == "pending" {
			summary.Waiting++
		}
	}
	return summary
}

// adminOrgRequest is a request as Requests shows it.
type adminOrgRequest struct {
	serverRequest
	Mine     bool   `json:"mine,omitempty"`     // the admin's own: someone else decides
	NewUser  bool   `json:"new_user,omitempty"` // an enrollment of someone with no group yet
	Seconds  int64  `json:"seconds,omitempty"`  // how long it asks for
	Describe string `json:"describe,omitempty"` // as Slack says it
}

// adminOrgLive is Requests and Running, read while they're shown.
type adminOrgLive struct {
	Live     liveSnapshot      `json:"live"`
	Requests []adminOrgRequest `json:"requests"`
	Groups   []string          `json:"groups"`
}

func adminOrgLiveOf(admin adminPerson) (adminOrgLive, error) {
	var answer adminOrgLive
	if err := adminOrgCall(http.MethodGet, "/admin/live", nil, &answer.Live); err != nil {
		return answer, err
	}
	var requests []serverRequest
	if err := adminOrgCall(http.MethodGet, "/admin/requests", nil, &requests); err != nil {
		return answer, err
	}
	var users []struct {
		Name string `json:"name"`
	}
	adminOrgCall(http.MethodGet, "/admin/users", nil, &users)
	known := map[string]bool{}
	for _, user := range users {
		known[user.Name] = true
	}
	answer.Requests = []adminOrgRequest{}
	for _, request := range requests {
		row := adminOrgRequest{serverRequest: request, Mine: request.User == admin.orgName(), Seconds: request.MaxSeconds,
			NewUser: request.Kind == "enroll" && !known[request.User], Describe: describeServerRequest(&request)}
		row.PublicKey = "" // a device's key is no secret, but the page has no use for it
		answer.Requests = append(answer.Requests, row)
	}
	var policy struct {
		Policy *serverPolicy `json:"policy"`
	}
	adminOrgCall(http.MethodGet, "/admin/policy", nil, &policy)
	if policy.Policy != nil {
		answer.Groups = sortedKeys(policy.Policy.Groups)
	}
	return answer, nil
}

// adminOrgSettings is the left panel's Org, People, and Keys, read when
// one of them is open. It holds no secret: only whether each is set.
type adminOrgSettings struct {
	People    orgPeople         `json:"people"`
	Users     json.RawMessage   `json:"users"`
	Approvers map[string]string `json:"approvers"`
	Providers []adminProvider   `json:"providers"`
	AI        json.RawMessage   `json:"ai"`
	Templates json.RawMessage   `json:"templates"`
	Data      json.RawMessage   `json:"data"`
	Policy    json.RawMessage   `json:"policy"`
	Slack     adminOrgSlack     `json:"slack"`
	Walls     []adminWallView   `json:"walls"`
	Listen    string            `json:"listen"`
	Problems  []string          `json:"problems,omitempty"`
}

type adminProvider struct {
	Name string `json:"name"`
	Set  bool   `json:"set"`
}

type adminOrgSlack struct {
	Set     bool   `json:"set"`
	Channel string `json:"channel,omitempty"`
}

func adminOrgSettingsOf() (adminOrgSettings, error) {
	dir := adminOrgDir()
	settings := adminOrgSettings{People: loadOrgPeople(dir), Walls: adminWallViews()}
	if data, err := os.ReadFile(filepath.Join(dir, "config.json")); err == nil {
		var config serverConfig
		json.Unmarshal(data, &config)
		settings.Listen = config.Listen
	}
	if config, err := loadSlackConfig(dir); err == nil && config != nil {
		settings.Slack = adminOrgSlack{Set: config.BotToken != "", Channel: config.Channel}
	}
	var set []string
	if err := adminOrgCall(http.MethodGet, "/admin/providers", nil, &set); err != nil {
		return settings, err
	}
	for _, name := range managedProviderNames() {
		settings.Providers = append(settings.Providers, adminProvider{Name: name, Set: slices.Contains(set, name)})
	}
	for _, read := range []struct {
		path string
		into *json.RawMessage
	}{{"/admin/users", &settings.Users}, {"/admin/ai", &settings.AI}, {"/admin/templates", &settings.Templates},
		{"/admin/data", &settings.Data}, {"/admin/policy", &settings.Policy}} {
		if err := adminOrgCall(http.MethodGet, read.path, nil, read.into); err != nil {
			settings.Problems = append(settings.Problems, err.Error())
			*read.into = json.RawMessage("null")
		}
	}
	if err := adminOrgCall(http.MethodGet, "/admin/approvers", nil, &settings.Approvers); err != nil {
		settings.Approvers = map[string]string{}
	}
	return settings, nil
}

// adminOrgChange is one change to the org from the page. Each goes to the
// admin socket under the admin's hi user name, and the server records it
// in its audit, as the command does.
type adminOrgChange struct {
	What   string   `json:"what"`   // decide, stop, user, provider, ai, policy, template, data, served, owner, restart
	Action string   `json:"action"` // what to do with it: approve, deny, add, remove, set, off, on, sync, test
	ID     string   `json:"id"`
	Name   string   `json:"name"`
	User   string   `json:"user"`
	Group  string   `json:"group"`
	Reason string   `json:"reason"`
	Key    string   `json:"key"` // a provider's or OpenRouter key, or a device key; written, never read back
	Model  string   `json:"model"`
	URL    string   `json:"url"`
	Ref    string   `json:"ref"`
	Token  string   `json:"token"` // a repository's or Hugging Face token; written, never read back
	Orgs   []string `json:"orgs"`
	Text   string   `json:"text"` // the policy
	Slack  string   `json:"slack"`
	Email  string   `json:"email"`
}

func adminOrgDo(admin adminPerson, change adminOrgChange) (any, error) {
	as := admin.orgName()
	escape := url.PathEscape
	var result any
	call := func(method, path string, body any) (any, error) {
		var answer any
		err := adminOrgCall(method, path, body, &answer)
		return answer, err
	}
	switch change.What {
	case "decide":
		if change.Action != "approve" && change.Action != "deny" {
			return nil, errors.New("approve or deny")
		}
		return call(http.MethodPost, "/admin/requests/"+escape(change.ID)+"/"+change.Action,
			map[string]string{"as": as, "group": change.Group, "reason": change.Reason})
	case "stop":
		if change.Name == "" && change.User == "" {
			return nil, errors.New("say what to stop")
		}
		return call(http.MethodPost, "/admin/stop", map[string]any{"as": as, "name": change.Name, "user": change.User})
	case "user":
		switch change.Action {
		case "add":
			return call(http.MethodPost, "/admin/users", map[string]string{"as": as, "name": change.Name, "group": change.Group, "key": change.Key})
		case "remove":
			return call(http.MethodDelete, "/admin/users/"+escape(change.Name)+"?as="+url.QueryEscape(as), nil)
		}
	case "provider":
		switch change.Action {
		case "set":
			return call(http.MethodPost, "/admin/providers", map[string]string{"as": as, "name": change.Name, "key": change.Key})
		case "remove":
			return call(http.MethodDelete, "/admin/providers/"+escape(change.Name)+"?as="+url.QueryEscape(as), nil)
		}
	case "ai":
		switch change.Action {
		case "set":
			dir := adminOrgDir()
			settings := readServerAISettings(dir)
			sameHost := true
			if change.URL != "" {
				if err := qValidBaseURL(change.URL); err != nil {
					return nil, err
				}
				sameHost = strings.EqualFold(qHost(strings.TrimRight(change.URL, "/")), qHost(settings.URL))
				settings.URL = strings.TrimRight(change.URL, "/")
			}
			settings.Model = firstNonEmpty(strings.TrimSpace(change.Model), settings.Model)
			key := strings.TrimSpace(change.Key)
			check := key
			// The kept key is for the kept endpoint's host; checking another
			// host with it would send it there.
			if check == "" && !sameHost && !settings.NoKey {
				return nil, fmt.Errorf("give the key for %s: the one kept is for %s", qHost(change.URL), qHost(readServerAISettings(dir).URL))
			}
			if check == "" && sameHost {
				keys := map[string]string{}
				if data, err := os.ReadFile(filepath.Join(dir, "keys.json")); err == nil {
					json.Unmarshal(data, &keys)
				}
				check = keys[serverAIKeyName]
			}
			if check == "" && !settings.NoKey {
				return nil, errors.New("give the OpenRouter key")
			}
			if err := checkServerAIKey(settings.URL, check, settings.Model); err != nil {
				return nil, fmt.Errorf("the key or model doesn't work: %w", err)
			}
			off := false
			return call(http.MethodPost, "/admin/ai", map[string]any{"as": as, "key": key, "url": settings.URL, "model": settings.Model, "off": &off})
		case "off", "on":
			return call(http.MethodPost, "/admin/ai", map[string]any{"as": as, "off": change.Action == "off"})
		case "remove":
			return call(http.MethodDelete, "/admin/ai?as="+url.QueryEscape(as), nil)
		}
	case "policy":
		return call(http.MethodPost, "/admin/policy", map[string]string{"as": as, "text": change.Text})
	case "template":
		switch change.Action {
		case "add":
			return call(http.MethodPost, "/admin/templates", map[string]string{"As": as, "Name": change.Name, "URL": change.URL, "Ref": change.Ref, "Token": change.Token})
		case "remove":
			return call(http.MethodDelete, "/admin/templates/"+escape(change.Name)+"?as="+url.QueryEscape(as), nil)
		case "sync":
			return call(http.MethodPost, "/admin/templates/sync", map[string]string{"As": as, "Name": change.Name})
		}
	case "data":
		switch change.Action {
		case "add":
			return call(http.MethodPost, "/admin/data", map[string]any{"As": as, "Orgs": change.Orgs, "Token": change.Token})
		case "remove":
			return call(http.MethodDelete, "/admin/data/"+escape(change.Name)+"?as="+url.QueryEscape(as), nil)
		case "test":
			return call(http.MethodPost, "/admin/data/test", map[string]string{"Org": change.Name})
		}
	case "served":
		return call(http.MethodDelete, "/admin/ai/served/"+escape(change.Name)+"?as="+url.QueryEscape(as), nil)
	case "admins":
		// The org's admins, in the org's own section: its owner adds and
		// removes them; each is an approver, and signs in here for the org.
		if !admin.Owner {
			return nil, errors.New("the org's owner adds and removes its admins")
		}
		switch change.Action {
		case "add":
			if _, err := call(http.MethodPost, "/admin/people", orgPeopleInput{As: as, Action: "add", Slack: change.Slack, Email: change.Email, Name: change.Name}); err != nil {
				return nil, err
			}
			config, _ := loadAdminConfig()
			added, ok := config.admin(change.Slack)
			if !ok {
				return map[string]string{"text": "They are an admin of the org."}, nil
			}
			key, err := adminMakeLink(added.Name, false)
			if err != nil {
				return nil, err
			}
			adminRecord(admin.Name, "", "made "+added.Name+" an admin of the org")
			return map[string]string{"link": adminLinkURL(config, key), "text": added.Name + " is an admin of the org, and approves. Their link to this page, for 14 days, is shown only now."}, nil
		case "remove":
			config, _ := loadAdminConfig()
			gone, ok := config.admin(change.Name)
			if !ok || !gone.Org {
				return nil, fmt.Errorf("%s isn't an admin of the org", change.Name)
			}
			if _, err := call(http.MethodPost, "/admin/people", orgPeopleInput{As: as, Action: "remove", Slack: gone.Slack}); err != nil {
				return nil, err
			}
			if !gone.Machine || !config.OrgApart {
				adminEndAll(gone.Name)
			}
			adminRecord(admin.Name, "", "took "+gone.Name+" off the org's admins")
			return map[string]string{"text": gone.Name + " isn't an admin of the org any more."}, nil
		}
	case "owner":
		if !admin.Owner {
			return nil, errors.New("only the org's owner hands it on")
		}
		return call(http.MethodPost, "/admin/people", orgPeopleInput{As: as, Action: "owner", Slack: change.Slack, Email: change.Email, Name: change.Name})
	case "restart":
		if adminOrgService() != "user" {
			return nil, fmt.Errorf("the org server is a system service here, so restarting it needs sudo: %s. hi update on the machine makes it a user service, once", orgRestartLine())
		}
		if out, err := appSystemctl("restart", orgUnit); err != nil {
			return nil, fmt.Errorf("systemctl --user restart %s: %s", orgUnit, qFirstLine(out, err.Error()))
		}
		adminRecord(admin.Name, "", "restarted the org server")
		return map[string]string{"text": "The org server restarted."}, nil
	}
	return result, fmt.Errorf("no such change to the org: %s %s", change.What, change.Action)
}

// adminOrgIncidents are the org's incidents: what its check every half
// minute warns of. An instance on the org's accounts that no request
// started, and a failed start's machine not deleted yet, are open while
// they last; those of the last month that are gone are closed.
func adminOrgIncidents(now time.Time) []adminIncident {
	dir := adminOrgDir()
	if dir == "" {
		return nil
	}
	var state struct {
		Leases   map[string]*serverLease `json:"leases"`
		Unleased map[string]time.Time    `json:"unleased"`
	}
	if data, err := os.ReadFile(filepath.Join(dir, "state.json")); err == nil {
		json.Unmarshal(data, &state)
	}
	list := []adminIncident{}
	seen := map[string]bool{}
	for _, lease := range state.Leases {
		if lease.State != "cleanup" {
			continue
		}
		key := lease.Provider + "/" + lease.Name
		seen[key] = true
		list = append(list, adminIncident{App: ":org", ID: key, Status: "open", Opened: lease.Started,
			Title: lease.Name + ": a failed start left it on " + lease.Provider + ", and it isn't deleted yet",
			Where: "the org server tries again every half minute · " + lease.Request + " of " + lease.User})
	}
	// What the audit said of each, newest last.
	said := map[string]auditEntry{}
	if file, err := os.Open(filepath.Join(dir, "audit.jsonl")); err == nil {
		scanner := bufio.NewScanner(file)
		scanner.Buffer(make([]byte, 64<<10), 1<<20)
		for scanner.Scan() {
			var entry auditEntry
			if json.Unmarshal(scanner.Bytes(), &entry) == nil && entry.Actor == "server" && now.Sub(entry.Time) < 30*24*time.Hour {
				switch entry.Action {
				case "unleased instance":
					said[entry.Subject] = entry
				case "cleaned up":
					said["cleaned up "+entry.Subject] = entry
				}
			}
		}
		file.Close()
	}
	for key, since := range state.Unleased {
		seen[key] = true
		entry := said[key]
		list = append(list, adminIncident{App: ":org", ID: key, Status: "open", Opened: since,
			Title: key + " is on the org's account without a request", Where: firstNonEmpty(entry.Detail, "no request started it") + " · stop it on the provider's website if nobody expects it"})
	}
	for key, entry := range said {
		if strings.Contains(key, " ") || seen[key] {
			continue
		}
		list = append(list, adminIncident{App: ":org", ID: key, Status: "closed", Opened: entry.Time,
			Title: key + " was on the org's account without a request", Where: entry.Detail, Last: "gone from the provider"})
	}
	for key, entry := range said {
		if name, ok := strings.CutPrefix(key, "cleaned up "); ok {
			list = append(list, adminIncident{App: ":org", ID: name, Status: "closed", Opened: entry.Time,
				Title: name + ": what a failed start left", Where: entry.Detail, Last: "the org server cleaned it up"})
		}
	}
	return list
}

// adminOrgSpend is spend per user and group over a week or a month.
func adminOrgSpend(days int) (string, error) {
	if days <= 0 || days > 400 {
		days = 30
	}
	var answer map[string]string
	err := adminOrgCall(http.MethodGet, fmt.Sprintf("/admin/spend?seconds=%d", days*86400), nil, &answer)
	return strings.ReplaceAll(answer["text"], "•", "-"), err
}

// adminOrgAudit is the org's audit as Activity shows it, newest first.
func adminOrgAudit(limit int) []adminActivity {
	dir := adminOrgDir()
	if dir == "" {
		return nil
	}
	file, err := os.Open(filepath.Join(dir, "audit.jsonl"))
	if err != nil {
		return nil
	}
	defer file.Close()
	var entries []auditEntry
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64<<10), 1<<20)
	for scanner.Scan() {
		var entry auditEntry
		if json.Unmarshal(scanner.Bytes(), &entry) == nil {
			entries = append(entries, entry)
			if len(entries) > limit*2 {
				entries = entries[len(entries)-limit:]
			}
		}
	}
	if len(entries) > limit {
		entries = entries[len(entries)-limit:]
	}
	list := make([]adminActivity, 0, len(entries))
	for i := len(entries) - 1; i >= 0; i-- {
		entry := entries[i]
		what := strings.TrimSpace(entry.Action + " " + entry.Subject)
		if entry.Detail != "" {
			what += ": " + entry.Detail
		}
		list = append(list, adminActivity{At: entry.Time, Who: entry.Actor, What: what, Org: true})
	}
	return list
}

// adminActivitiesWithOrg is the page's activity and the org's audit, by
// time, newest first.
func adminActivitiesWithOrg(limit int) []adminActivity {
	list := append(adminActivities(limit), adminOrgAudit(limit)...)
	sort.SliceStable(list, func(i, j int) bool { return list[i].At.After(list[j].At) })
	if len(list) > limit {
		list = list[:limit]
	}
	return list
}

// ---------------------------------------------------------------------------
// wall links: Running and Requests on a screen, read-only

// adminWall is a wall link, kept as its key's hash. It ends when an admin
// ends it.
type adminWall struct {
	Hash    string    `json:"hash"`
	Name    string    `json:"name"`
	Full    bool      `json:"full,omitempty"`    // names in full, not initials
	Reasons bool      `json:"reasons,omitempty"` // why each was asked for
	Made    time.Time `json:"made"`
	By      string    `json:"by,omitempty"`
	Seen    time.Time `json:"seen,omitzero"`
}

type adminWallView struct {
	Name    string    `json:"name"`
	Full    bool      `json:"full"`
	Reasons bool      `json:"reasons"`
	Made    time.Time `json:"made"`
	By      string    `json:"by,omitempty"`
	Seen    time.Time `json:"seen,omitzero"`
}

func adminWallViews() []adminWallView {
	views := []adminWallView{}
	for _, wall := range loadAdminKeys().Walls {
		views = append(views, adminWallView{Name: wall.Name, Full: wall.Full, Reasons: wall.Reasons, Made: wall.Made, By: wall.By, Seen: wall.Seen})
	}
	return views
}

// adminMakeWall makes a wall link; a wall of that name has a new one.
func adminMakeWall(name string, full, reasons bool, by string) (string, error) {
	name = strings.Join(strings.Fields(name), " ")
	if name == "" || len(name) > 40 || strings.ContainsAny(name, "<>\"`\\") {
		return "", errors.New("a wall's name says where it is, up to 40 letters, such as Office TV")
	}
	key, err := adminNewKey()
	if err != nil {
		return "", err
	}
	unlock := adminLock()
	defer unlock()
	keys := loadAdminKeys()
	keys.Walls = slices.DeleteFunc(keys.Walls, func(w adminWall) bool { return strings.EqualFold(w.Name, name) })
	keys.Walls = append(keys.Walls, adminWall{Hash: adminHash(key), Name: name, Full: full, Reasons: reasons, Made: time.Now().UTC(), By: by})
	return key, saveAdminKeys(keys)
}

func adminEndWall(name string) error {
	unlock := adminLock()
	defer unlock()
	keys := loadAdminKeys()
	before := len(keys.Walls)
	keys.Walls = slices.DeleteFunc(keys.Walls, func(w adminWall) bool { return strings.EqualFold(w.Name, name) })
	if len(keys.Walls) == before {
		return fmt.Errorf("no wall called %s", name)
	}
	return saveAdminKeys(keys)
}

// adminWallOf is the wall a link's key opens.
func adminWallOf(key string) (adminWall, bool) {
	if key == "" {
		return adminWall{}, false
	}
	keys := loadAdminKeys()
	for i, wall := range keys.Walls {
		if subtle.ConstantTimeCompare([]byte(wall.Hash), []byte(adminHash(key))) == 1 {
			if time.Since(wall.Seen) > 10*time.Minute {
				unlock := adminLock()
				fresh := loadAdminKeys()
				for j := range fresh.Walls {
					if fresh.Walls[j].Hash == wall.Hash {
						fresh.Walls[j].Seen = time.Now().UTC()
					}
				}
				saveAdminKeys(fresh)
				unlock()
			}
			return keys.Walls[i], true
		}
	}
	return adminWall{}, false
}

// adminInitials is a person as a wall shows them: S. for sam.
func adminInitials(name string) string {
	for _, char := range name {
		if unicode.IsLetter(char) || unicode.IsDigit(char) {
			return string(unicode.ToUpper(char)) + "."
		}
	}
	return "?"
}

// adminWallState is what a wall shows: what runs and what waits, today's
// and the month's spend, and recent activity. Names are initials unless
// the wall shows them in full, and reasons only when it shows them.
type adminWallState struct {
	Name        string           `json:"name"`
	Machine     string           `json:"machine"`
	Now         time.Time        `json:"now"`
	Running     []adminWallRun   `json:"running"`
	Waiting     []adminWallWait  `json:"waiting"`
	RatePerHour float64          `json:"rate_per_hour"`
	Today       float64          `json:"today"`
	Month       float64          `json:"month"`
	MonthBudget float64          `json:"month_budget"`
	Budgets     []liveBudget     `json:"budgets"`
	Activity    []adminWallEvent `json:"activity"`
}

type adminWallRun struct {
	Name     string    `json:"name"`
	Who      string    `json:"who"`
	Group    string    `json:"group,omitempty"`
	Agent    string    `json:"agent,omitempty"`
	Hardware string    `json:"hardware"`
	Rate     string    `json:"rate"`
	Cost     float64   `json:"cost"`
	Started  time.Time `json:"started"`
	Deadline time.Time `json:"deadline"`
	Reason   string    `json:"reason,omitempty"`
	Doing    []string  `json:"doing,omitempty"`
}

type adminWallWait struct {
	Kind     string    `json:"kind"`
	Who      string    `json:"who"`
	Group    string    `json:"group,omitempty"`
	Name     string    `json:"name,omitempty"`
	Hardware string    `json:"hardware,omitempty"`
	Rate     string    `json:"rate,omitempty"`
	Seconds  int64     `json:"seconds,omitempty"`
	Created  time.Time `json:"created"`
	Over     bool      `json:"over_budget,omitempty"`
	Reason   string    `json:"reason,omitempty"`
}

type adminWallEvent struct {
	Time time.Time `json:"time"`
	Text string    `json:"text"`
}

func adminWallStateOf(wall adminWall) (adminWallState, error) {
	var live liveSnapshot
	if err := adminOrgCall(http.MethodGet, "/admin/live", nil, &live); err != nil {
		return adminWallState{}, err
	}
	who := func(name string) string {
		if wall.Full || name == "" {
			return name
		}
		return adminInitials(name)
	}
	state := adminWallState{Name: wall.Name, Machine: adminMachineName(), Now: live.Now, RatePerHour: live.RatePerHour,
		Today: live.Today, Month: live.Month + live.ModelsMonth, MonthBudget: live.MonthBudget, Budgets: live.Budgets,
		Running: []adminWallRun{}, Waiting: []adminWallWait{}, Activity: []adminWallEvent{}}
	for _, run := range live.Running {
		row := adminWallRun{Name: run.Name, Who: who(run.User), Group: run.Group, Agent: run.Agent, Hardware: run.Hardware,
			Rate: run.Rate, Cost: run.Cost, Started: run.Started, Deadline: run.Deadline, Doing: run.Doing}
		if wall.Reasons {
			row.Reason = run.Reason
		}
		state.Running = append(state.Running, row)
	}
	for _, waiting := range live.Waiting {
		row := adminWallWait{Kind: waiting.Kind, Who: who(waiting.User), Group: waiting.Group, Name: waiting.Name, Hardware: waiting.Hardware,
			Rate: waiting.Rate, Seconds: waiting.Seconds, Created: waiting.Created, Over: waiting.Over}
		if wall.Reasons {
			row.Reason = waiting.Reason
		}
		state.Waiting = append(state.Waiting, row)
	}
	for _, event := range live.Activity {
		text := event.Text
		if !wall.Full && event.User != "" {
			text = strings.ReplaceAll(text, event.User, adminInitials(event.User))
		}
		state.Activity = append(state.Activity, adminWallEvent{Time: event.Time, Text: text})
	}
	return state, nil
}

// adminWallRoutes are a wall's page and what it reads: no sign-in, its
// key in the address, and nothing that changes anything.
func (s *adminServer) adminWallRoutes(mux *http.ServeMux, reply func(http.ResponseWriter, int, any)) {
	mux.HandleFunc("GET /wall/{key}", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := adminWallOf(r.PathValue("key")); !ok {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprintln(w, "This wall link has ended. An admin makes a new one on the admin page, in Org.")
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; frame-ancestors 'none'; form-action 'none'; base-uri 'none'")
		w.Write(adminWallHTML)
	})
	mux.HandleFunc("GET /wall/{key}/state", func(w http.ResponseWriter, r *http.Request) {
		wall, ok := adminWallOf(r.PathValue("key"))
		if !ok {
			reply(w, http.StatusNotFound, map[string]any{"error": "This wall link has ended.", "ended": true})
			return
		}
		state, err := adminWallStateOf(wall)
		if err != nil {
			reply(w, http.StatusServiceUnavailable, map[string]string{"error": "The org server doesn't answer."})
			return
		}
		reply(w, http.StatusOK, state)
	})
	mux.HandleFunc("GET /wall.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Write(adminWallScript)
	})
}

// ---------------------------------------------------------------------------
// the routes

func (s *adminServer) orgRoutes(mux *http.ServeMux, reply func(http.ResponseWriter, int, any),
	decode func(http.ResponseWriter, *http.Request, any) bool,
	signedIn func(func(http.ResponseWriter, *http.Request, adminPerson)) http.HandlerFunc) {
	failure := func(w http.ResponseWriter, status int, err error) {
		reply(w, status, map[string]string{"error": err.Error()})
	}
	noOrg := func(w http.ResponseWriter) bool {
		if adminOrgDir() == "" {
			failure(w, http.StatusNotFound, errors.New("this machine runs no org server"))
			return true
		}
		return false
	}
	mux.HandleFunc("GET /api/org", signedIn(func(w http.ResponseWriter, r *http.Request, admin adminPerson) {
		if noOrg(w) {
			return
		}
		answer, err := adminOrgLiveOf(admin)
		if err != nil {
			failure(w, http.StatusServiceUnavailable, err)
			return
		}
		reply(w, http.StatusOK, answer)
	}))
	mux.HandleFunc("GET /api/org/settings", signedIn(func(w http.ResponseWriter, r *http.Request, admin adminPerson) {
		if noOrg(w) {
			return
		}
		settings, err := adminOrgSettingsOf()
		if err != nil {
			failure(w, http.StatusServiceUnavailable, err)
			return
		}
		reply(w, http.StatusOK, settings)
	}))
	mux.HandleFunc("GET /api/org/spend", signedIn(func(w http.ResponseWriter, r *http.Request, admin adminPerson) {
		if noOrg(w) {
			return
		}
		days, _ := strconv.Atoi(r.URL.Query().Get("days"))
		text, err := adminOrgSpend(days)
		if err != nil {
			failure(w, http.StatusServiceUnavailable, err)
			return
		}
		reply(w, http.StatusOK, map[string]string{"text": text})
	}))
	mux.HandleFunc("POST /api/org", signedIn(func(w http.ResponseWriter, r *http.Request, admin adminPerson) {
		if noOrg(w) {
			return
		}
		var change adminOrgChange
		if !decode(w, r, &change) {
			return
		}
		answer, err := adminOrgDo(admin, change)
		if err != nil {
			failure(w, http.StatusBadRequest, err)
			return
		}
		reply(w, http.StatusOK, map[string]any{"ok": true, "answer": answer})
	}))
	mux.HandleFunc("POST /api/walls", signedIn(func(w http.ResponseWriter, r *http.Request, admin adminPerson) {
		if noOrg(w) {
			return
		}
		var request struct {
			Action  string `json:"action"` // add or end
			Name    string `json:"name"`
			Full    bool   `json:"full"`
			Reasons bool   `json:"reasons"`
		}
		if !decode(w, r, &request) {
			return
		}
		switch request.Action {
		case "add":
			key, err := adminMakeWall(request.Name, request.Full, request.Reasons, admin.Name)
			if err != nil {
				failure(w, http.StatusBadRequest, err)
				return
			}
			config, _ := loadAdminConfig()
			adminRecord(admin.Name, "", "made a wall link for "+request.Name)
			reply(w, http.StatusOK, map[string]string{"link": adminURL(config) + "wall/" + key,
				"text": "Open it on the screen, full screen. It shows what runs and what waits, and can change nothing. It works until an admin ends it; it is shown only now."})
		case "end":
			if err := adminEndWall(request.Name); err != nil {
				failure(w, http.StatusBadRequest, err)
				return
			}
			adminRecord(admin.Name, "", "ended the wall link for "+request.Name)
			reply(w, http.StatusOK, map[string]string{"text": "The wall link for " + request.Name + " has ended."})
		default:
			failure(w, http.StatusBadRequest, errors.New("add or end"))
		}
	}))
	s.adminWallRoutes(mux, reply)
}
