package cli

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// On a machine with apps and an org server, the machine's admins and the
// org's are two lists: each changes only its own, and making someone an
// admin of the machine doesn't make them an approver.
func TestMachineAndOrgAdminsApart(t *testing.T) {
	a, ts := orgAdminTest(t)
	saveSlackConfig(ts.dir, slackConfig{BotToken: "xoxb-org", Channel: "C1", Approvers: map[string]string{"U07OWNER1": "olga", "U07SAM": "sam"}})
	if err := saveAdminConfig(adminConfig{Reach: adminReachLocal, OrgApart: true, Admins: []adminPerson{
		{Name: "Olga", Slack: "U07OWNER1", Added: time.Now()}, {Name: "Ada", Slack: "U07ADA", Added: time.Now()}}}); err != nil {
		t.Fatal(err)
	}
	olga, ada, sam := a.signIn(t, "Olga"), a.signIn(t, "Ada"), a.signIn(t, "sam")
	var state adminStateView
	a.get(t, olga, "/api/state", &state)
	if !state.OrgApart || len(state.Admins) != 3 || !state.Me.Machine || !state.Me.Org || !state.Me.Owner {
		t.Fatalf("olga: %+v, admins %+v", state.Me, state.Admins)
	}
	state = adminStateView{}
	a.get(t, ada, "/api/state", &state)
	if !state.Me.Machine || state.Me.Org {
		t.Fatalf("ada: %+v", state.Me)
	}
	state = adminStateView{}
	a.get(t, sam, "/api/state", &state)
	if state.Me.Machine || !state.Me.Org {
		t.Fatalf("sam: %+v", state.Me)
	}

	// Each sees everything, and changes only their own.
	var answer map[string]any
	answer = nil
	if code := a.post(t, ada, "/api/org", adminOrgChange{What: "user", Action: "add", Name: "lin", Group: "staff"}, &answer); code != http.StatusForbidden || !strings.Contains(answer["error"].(string), "the org's admins") {
		t.Fatalf("ada changed the org: %d %v", code, answer)
	}
	answer = nil
	if code := a.post(t, sam, "/api/action", map[string]string{"app": "demo", "action": "restart"}, &answer); code != http.StatusForbidden || !strings.Contains(answer["error"].(string), "the machine's admins") {
		t.Fatalf("sam restarted an app: %d %v", code, answer)
	}
	answer = nil
	if code := a.post(t, sam, "/api/admins", map[string]string{"action": "add", "name": "Bo"}, &answer); code != http.StatusForbidden {
		t.Fatalf("sam added a machine admin: %d %v", code, answer)
	}
	answer = nil
	if code := a.post(t, sam, "/api/org", adminOrgChange{What: "user", Action: "add", Name: "lin", Group: "staff"}, &answer); code != http.StatusOK || ts.server.state.Users["lin"] == nil {
		t.Fatalf("sam added a user: %d %v", code, answer)
	}
	answer = nil
	if code := a.post(t, olga, "/api/signout", map[string]string{}, &answer); code != http.StatusOK {
		t.Fatalf("sign out: %d", code)
	}
	olga = a.signIn(t, "Olga")

	// A machine admin added by a machine admin: not the org's, no approver.
	answer = nil
	if code := a.post(t, ada, "/api/admins", map[string]string{"action": "add", "name": "Bo", "slack": "U07BOB"}, &answer); code != http.StatusOK {
		t.Fatalf("add Bo: %d %v", code, answer)
	}
	if _, ok := loadOrgPeople(ts.dir).find("U07BOB"); ok {
		t.Fatal("Bo went into the org's admins")
	}
	if config, _ := loadSlackConfig(ts.dir); config.Approvers["U07BOB"] != "" {
		t.Fatal("Bo approves")
	}
	data, _ := os.ReadFile(adminConfigPath())
	var own adminConfig
	json.Unmarshal(data, &own)
	if len(own.Admins) != 3 || !own.OrgApart || strings.Contains(string(data), `"org"`) || strings.Contains(string(data), "sam") {
		t.Fatalf("admin.json:\n%s", data)
	}

	// An admin of the org added by its owner signs in for the org, and
	// approves; the owner only adds them.
	answer = nil
	if code := a.post(t, ada, "/api/org", adminOrgChange{What: "admins", Action: "add", Name: "kim", Slack: "U07KIM"}, &answer); code != http.StatusForbidden {
		t.Fatalf("ada added an org admin: %d", code)
	}
	answer = nil
	if code := a.post(t, sam, "/api/org", adminOrgChange{What: "admins", Action: "add", Name: "kim", Slack: "U07KIM"}, &answer); code != http.StatusBadRequest || !strings.Contains(answer["error"].(string), "owner") {
		t.Fatalf("sam added an org admin: %d %v", code, answer)
	}
	answer = nil
	if code := a.post(t, olga, "/api/org", adminOrgChange{What: "admins", Action: "add", Name: "kim", Slack: "U07KIM"}, &answer); code != http.StatusOK {
		t.Fatalf("olga added kim: %d %v", code, answer)
	}
	link := answer["answer"].(map[string]any)["link"].(string)
	if !strings.Contains(link, "?key=") {
		t.Fatalf("kim's link: %v", answer)
	}
	config, _ := loadAdminConfig()
	if kim, ok := config.admin("U07KIM"); !ok || !kim.Org || kim.Machine {
		t.Fatalf("kim: %+v", kim)
	}
	if config, _ := loadSlackConfig(ts.dir); config.Approvers["U07KIM"] != "kim" {
		t.Fatal("kim doesn't approve")
	}
	// The machine's admins can make one of the org's admins theirs too,
	// and take them off again: they still sign in, for the org.
	answer = nil
	if code := a.post(t, ada, "/api/admins", map[string]string{"action": "add", "name": "kim"}, &answer); code != http.StatusOK {
		t.Fatalf("kim on the machine: %d %v", code, answer)
	}
	config, _ = loadAdminConfig()
	if kim, _ := config.admin("kim"); !kim.Machine || !kim.Org {
		t.Fatalf("kim: %+v", kim)
	}
	kim := a.signIn(t, "kim")
	answer = nil
	if code := a.post(t, ada, "/api/admins", map[string]string{"action": "remove", "name": "kim"}, &answer); code != http.StatusOK {
		t.Fatalf("kim off the machine: %d %v", code, answer)
	}
	state = adminStateView{}
	a.get(t, kim, "/api/state", &state)
	if state.Me.Name != "kim" || state.Me.Machine || !state.Me.Org {
		t.Fatalf("kim after: %+v", state.Me)
	}
}

// hi org init on a machine with apps keeps the machine's admins, joins
// the machine to the new org, and refuses on a machine joined to another.
func TestOrgInitBesideApps(t *testing.T) {
	newTestServer(t)
	a := setupAdminTest(t)
	_ = a
	orgDir := filepath.Join(t.TempDir(), "org")
	t.Setenv("HI_ORG_DIR", orgDir)
	adminOrgDir = func() string {
		if fileExists(filepath.Join(orgDir, "config.json")) {
			return orgDir
		}
		return ""
	}
	if err := saveServerConnection(serverConnection{URL: "http://other.example.com:7373", User: "olga"}); err != nil {
		t.Fatal(err)
	}
	args := []string{"org", "init", "--listen", "127.0.0.1:7478", "--owner", "U07NOOR", "--email", "noor@example.com", "--no-start"}
	if code, _, stderr := runHi(args...); code == 0 || !strings.Contains(stderr, "joined to the org at http://other.example.com:7373") || !strings.Contains(stderr, "hi org leave") {
		t.Fatalf("init on a machine in another org: %d %s", code, stderr)
	}
	if fileExists(filepath.Join(orgDir, "config.json")) {
		t.Fatal("init made an org anyway")
	}
	os.Remove(serverConnectionPath())
	code, stdout, stderr := runHi(args...)
	if code != 0 {
		t.Fatalf("init: %d %s%s", code, stdout, stderr)
	}
	for _, want := range []string{"joined the org as noor (staff)", "keeps the machine's admins, and noor is one too", "a small machine of its own"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("init doesn't say %q:\n%s", want, stdout)
		}
	}
	connection, _ := loadServerConnection()
	if connection == nil || connection.URL != "http://127.0.0.1:7478" || connection.User != "noor" {
		t.Fatalf("connection: %+v", connection)
	}
	server, err := openServer(orgDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if server.state.Users["noor"] == nil || len(server.state.Devices) != 1 {
		t.Fatalf("the machine isn't in its org: %+v %+v", server.state.Users, server.state.Devices)
	}
	config, _ := loadAdminConfig()
	if !config.OrgApart || len(config.Admins) != 2 {
		t.Fatalf("admins: %+v", config)
	}
	olga, _ := config.admin("Olga")
	noor, _ := config.admin("noor")
	if !olga.Machine || olga.Org || !noor.Machine || !noor.Org || !noor.Owner {
		t.Fatalf("olga %+v, noor %+v", olga, noor)
	}
}

// The org's incidents: an instance on its accounts that no request
// started, and a failed start's machine not deleted yet, while they last.
func TestOrgIncidents(t *testing.T) {
	dir := t.TempDir()
	saved := adminOrgDir
	adminOrgDir = func() string { return dir }
	t.Cleanup(func() { adminOrgDir = saved })
	now := time.Now()
	state := map[string]any{
		"leases": map[string]any{
			"gpu1":   serverLease{Name: "gpu1", Provider: "runpod", User: "alice", Request: "r-1", Started: now.Add(-time.Hour)},
			"left-1": serverLease{Name: "left-1", Provider: "shadeform", User: "noor", Request: "r-2", Started: now.Add(-time.Minute), State: "cleanup"},
		},
		"unleased": map[string]time.Time{"shadeform/stray": now.Add(-2 * time.Hour)},
	}
	data, _ := json.Marshal(state)
	writeTestFile(t, filepath.Join(dir, "state.json"), string(data), 0o600)
	lines := []auditEntry{
		{Time: now.Add(-2 * time.Hour), Actor: "server", Action: "unleased instance", Subject: "shadeform/stray", Detail: "running on the organization's account without a request, named by r-9 of ken (failed)"},
		{Time: now.Add(-48 * time.Hour), Actor: "server", Action: "unleased instance", Subject: "runpod/gone", Detail: "running on the organization's account without a request"},
		{Time: now.Add(-24 * time.Hour), Actor: "server", Action: "cleaned up", Subject: "old", Detail: "r-0 failed to start"},
		{Time: now.Add(-40 * 24 * time.Hour), Actor: "server", Action: "unleased instance", Subject: "runpod/ancient"},
	}
	var audit strings.Builder
	for _, line := range lines {
		data, _ := json.Marshal(line)
		audit.Write(append(data, '\n'))
	}
	writeTestFile(t, filepath.Join(dir, "audit.jsonl"), audit.String(), 0o600)
	open, closed := map[string]adminIncident{}, map[string]adminIncident{}
	for _, incident := range adminOrgIncidents(now) {
		if incident.App != ":org" {
			t.Fatalf("not the org's: %+v", incident)
		}
		if incident.Status == "open" {
			open[incident.ID] = incident
		} else {
			closed[incident.ID] = incident
		}
	}
	if len(open) != 2 || !strings.Contains(open["shadeform/stray"].Where, "r-9 of ken") || !strings.Contains(open["shadeform/left-1"].Title, "isn't deleted yet") {
		t.Fatalf("open: %+v", open)
	}
	if len(closed) != 2 || closed["runpod/gone"].ID == "" || closed["old"].ID == "" {
		t.Fatalf("closed: %+v", closed)
	}
}
