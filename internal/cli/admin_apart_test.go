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

// One list (hi_server_apps.md, release 3): on a machine that kept its
// admins apart from the server's, each machine admin with a Slack member ID
// becomes an admin of the server, once; one with none is named; and the
// flag is gone.
func TestOneListOfAdmins(t *testing.T) {
	a, ts := orgAdminTest(t)
	writeTestFile(t, adminConfigPath(), `{"reach": "local", "org_apart": true, "machine_owner": "Ada", "admins": [
		{"name": "Olga", "slack": "U07OWNER1"}, {"name": "Ada Lovelace", "slack": "U07ADA"}, {"name": "Bo"}]}`, 0o600)
	said := strings.Join(adminMergeLists(), "\n")
	if !strings.Contains(said, "ada-lovelace is an admin of the server") || !strings.Contains(said, "Bo was an admin of the machine only") || strings.Contains(said, "Olga") {
		t.Fatalf("said:\n%s", said)
	}
	if again := adminMergeLists(); len(again) != 0 {
		t.Fatalf("merged twice: %v", again)
	}
	if data, _ := os.ReadFile(adminConfigPath()); strings.Contains(string(data), "org_apart") {
		t.Fatalf("the flag stays:\n%s", data)
	}
	people := loadOrgPeople(ts.dir)
	if _, ok := people.find("U07ADA"); !ok {
		t.Fatalf("ada isn't the server's: %+v", people)
	}
	config, _ := loadAdminConfig()
	if config.apart() || config.machineOwner() != "olga" {
		t.Fatalf("one list, one owner: %+v", config)
	}
	ada := a.signIn(t, "ada-lovelace")
	var state adminStateView
	a.get(t, ada, "/api/state", &state)
	if state.OrgApart || !state.Me.Machine || !state.Me.Org || state.Me.Owner {
		t.Fatalf("ada: %+v", state.Me)
	}
	var answer map[string]any
	if code := a.post(t, ada, "/api/action", map[string]string{"app": "demo", "action": "restart"}, &answer); code == http.StatusForbidden {
		t.Fatalf("ada can't run the apps: %v", answer)
	}
}

// hi server init on a machine with apps keeps the machine's admins, joins
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
	if code, _, stderr := runHi(args...); code == 0 || !strings.Contains(stderr, "joined through the server at http://other.example.com:7373") || !strings.Contains(stderr, "hi server leave") {
		t.Fatalf("init on a machine in another server: %d %s", code, stderr)
	}
	if fileExists(filepath.Join(orgDir, "config.json")) {
		t.Fatal("init made a server anyway")
	}
	os.Remove(serverConnectionPath())
	code, stdout, stderr := runHi(args...)
	if code != 0 {
		t.Fatalf("init: %d %s%s", code, stdout, stderr)
	}
	for _, want := range []string{"joined the server as noor (staff)", "olga is an admin of the server", "The server's owner and admins run them"} {
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
	if config.apart() || len(config.Admins) != 2 {
		t.Fatalf("admins: %+v", config)
	}
	olga, _ := config.admin("olga")
	noor, _ := config.admin("noor")
	if !olga.Machine || !olga.Org || !noor.Machine || !noor.Org || !noor.Owner || config.machineOwner() != "noor" {
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
		{Time: now.Add(-2 * time.Hour), Actor: "server", Action: "unleased instance", Subject: "shadeform/stray", Detail: "running on the server's account without a request, named by r-9 of ken (failed)"},
		{Time: now.Add(-48 * time.Hour), Actor: "server", Action: "unleased instance", Subject: "runpod/gone", Detail: "running on the server's account without a request"},
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
			t.Fatalf("not the server's: %+v", incident)
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
