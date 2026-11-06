package cli

import (
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Every org has an owner, set at hi org init; the owner's admins are
// approvers too, and handing the org on keeps the owner before as an admin.
func TestOrgOwnerAndAdmins(t *testing.T) {
	newTestServer(t)
	dir := filepath.Join(t.TempDir(), "org")
	if code, _, stderr := runHi("org", "init", "--listen", "127.0.0.1:7476", "--dir", dir); code == 0 || !strings.Contains(stderr, "an org has an owner") {
		t.Fatalf("an org without an owner: %d %s", code, stderr)
	}
	if fileExists(filepath.Join(dir, "config.json")) {
		t.Fatal("init wrote an org without an owner")
	}
	code, stdout, stderr := runHi("org", "init", "--listen", "127.0.0.1:7476", "--dir", dir, "--owner", "U07OWNER1", "--email", "Olga.K@example.com")
	if code != 0 || !strings.Contains(stdout, "olga.k (Olga.K@example.com) owns the org now.") || !strings.Contains(stdout, "hi-org.service") {
		t.Fatalf("init: %d\n%s%s", code, stdout, stderr)
	}
	if fileExists(orgUserUnitPath()) {
		t.Fatal("init with --dir installed a user service")
	}
	// Slack set up later makes the owner an approver; admins added after
	// are approvers at once.
	saveSlackConfig(dir, slackConfig{BotToken: "xoxb-org", Channel: "C1", Approvers: map[string]string{"UBOB": "bob"}})
	if code, stdout, stderr := runHi("org", "admins", "add", "U07SAM", "--name", "sam", "--dir", dir); code != 0 || !strings.Contains(stdout, "sam is an admin") {
		t.Fatalf("add: %d %s%s", code, stdout, stderr)
	}
	if code, _, stderr := runHi("org", "admins", "add", "nope", "--name", "x", "--dir", dir); code == 0 || !strings.Contains(stderr, "isn't a Slack member ID") {
		t.Fatalf("a bad ID: %d %s", code, stderr)
	}
	config, _ := loadSlackConfig(dir)
	if config.Approvers["U07SAM"] != "sam" || config.Approvers["UBOB"] != "bob" {
		t.Fatalf("approvers: %v", config.Approvers)
	}
	if _, stdout, _ := runHi("org", "admins", "--dir", dir); !strings.Contains(stdout, "olga.k") || !strings.Contains(stdout, "owner") || !strings.Contains(stdout, "sam") {
		t.Fatalf("list:\n%s", stdout)
	}
	if code, _, stderr := runHi("org", "admins", "remove", "olga.k", "--dir", dir); code == 0 || !strings.Contains(stderr, "can't be removed") {
		t.Fatalf("removed the owner: %d %s", code, stderr)
	}
	// Handing it on: the owner before is an admin.
	if code, stdout, stderr := runHi("org", "owner", "U07NOOR", "--email", "noor@example.com", "--dir", dir); code != 0 || !strings.Contains(stdout, "noor (noor@example.com) owns the org now. olga.k, the owner before, is an admin") {
		t.Fatalf("hand on: %d %s%s", code, stdout, stderr)
	}
	people := loadOrgPeople(dir)
	if people.Owner.Name != "noor" || len(people.Admins) != 2 {
		t.Fatalf("people: %+v", people)
	}
	if code, _, _ := runHi("org", "admins", "remove", "sam", "--dir", dir); code != 0 {
		t.Fatal("remove sam")
	}
	if config, _ := loadSlackConfig(dir); config.Approvers["U07SAM"] != "" || config.Approvers["U07NOOR"] != "noor" {
		t.Fatalf("approvers after: %v", config.Approvers)
	}
	audit, _ := os.ReadFile(filepath.Join(dir, "audit.jsonl"))
	for _, want := range []string{`"set owner","subject":"olga.k"`, `"added admin","subject":"sam"`, `"removed admin","subject":"sam"`, `"set owner","subject":"noor"`} {
		if !strings.Contains(string(audit), want) {
			t.Errorf("audit lacks %s:\n%s", want, audit)
		}
	}
}

// orgAdminTest is an admin page on the org server's machine: the org's
// owner olga and admin sam, the server's admin socket answering.
func orgAdminTest(t *testing.T) (adminTest, *testServer) {
	t.Helper()
	ts := newTestServer(t)
	a := setupAdminTest(t)
	writeTestFile(t, filepath.Join(ts.dir, "config.json"), `{"listen": "127.0.0.1:7477"}`, 0o600)
	for _, input := range []orgPeopleInput{{As: "test", Action: "owner", Slack: "U07OWNER1", Email: "olga@example.com", Name: "olga"},
		{As: "test", Action: "add", Slack: "U07SAM", Name: "sam"}} {
		if _, err := applyOrgPeople(ts.dir, input); err != nil {
			t.Fatal(err)
		}
	}
	listener, err := net.Listen("unix", filepath.Join(ts.dir, "admin.sock"))
	if err != nil {
		t.Skip(err)
	}
	admin := &http.Server{Handler: ts.server.adminHandler()}
	go admin.Serve(listener)
	t.Cleanup(func() { admin.Close() })
	adminOrgDir = func() string { return ts.dir }
	return a, ts
}

func TestAdminPageRunsTheOrg(t *testing.T) {
	a, ts := orgAdminTest(t)
	olga := a.signIn(t, "olga")
	var state adminStateView
	a.get(t, olga, "/api/state", &state)
	if state.Org == nil || !state.Org.Up || state.Org.Owner != "olga" || len(state.Admins) != 2 || !state.Me.Owner {
		t.Fatalf("state: %+v %+v", state.Org, state.Admins)
	}

	now := time.Now()
	ts.server.state.Users["alice"] = &serverUser{Name: "alice", Group: "staff"}
	ts.server.state.Requests["r-1"] = &serverRequest{ID: "r-1", Kind: "compute", State: "pending", User: "alice", Provider: "runpod",
		Hardware: "l4", Rate: "$0.49/h", Name: "train", MaxSeconds: 3600, Reason: "a secret plan", Created: now}
	ts.server.state.Requests["r-2"] = &serverRequest{ID: "r-2", Kind: "compute", State: "pending", User: "olga", Provider: "runpod",
		Hardware: "l4", Name: "mine", MaxSeconds: 3600, Created: now}
	ts.server.state.Requests["e-1"] = &serverRequest{ID: "e-1", Kind: "enroll", State: "pending", User: "ken", Hostname: "kens-laptop",
		PublicKey: "AAAA", Device: "fp", Created: now}
	ts.server.state.Leases["gpu1"] = &serverLease{Name: "gpu1", Provider: "runpod", Hardware: "l4", Rate: "$1.00/h", User: "alice",
		Request: "r-0", Started: now.Add(-time.Hour), Deadline: now.Add(time.Hour)}
	ts.server.state.Requests["r-0"] = &serverRequest{ID: "r-0", Kind: "compute", State: "running", User: "alice", Reason: "a secret plan"}
	ts.fake.instances["gpu1"] = upRequest{}

	var live adminOrgLive
	if code := a.get(t, olga, "/api/org", &live); code != http.StatusOK || len(live.Requests) != 3 || len(live.Live.Running) != 1 {
		t.Fatalf("org: %d %+v", code, live)
	}
	for _, r := range live.Requests {
		if (r.ID == "r-2") != r.Mine || (r.ID == "e-1") != r.NewUser {
			t.Fatalf("request %s: mine %v new %v", r.ID, r.Mine, r.NewUser)
		}
	}
	var answer map[string]any
	if code := a.post(t, olga, "/api/org", adminOrgChange{What: "decide", Action: "approve", ID: "r-2"}, &answer); code != http.StatusBadRequest || !strings.Contains(answer["error"].(string), "own request") {
		t.Fatalf("approved her own: %d %v", code, answer)
	}
	if code := a.post(t, olga, "/api/org", adminOrgChange{What: "decide", Action: "approve", ID: "e-1"}, &answer); code != http.StatusBadRequest || !strings.Contains(answer["error"].(string), "new user") {
		t.Fatalf("a new user without a group: %d %v", code, answer)
	}
	if code := a.post(t, olga, "/api/org", adminOrgChange{What: "decide", Action: "approve", ID: "e-1", Group: "staff"}, &answer); code != http.StatusOK {
		t.Fatalf("enroll: %d %v", code, answer)
	}
	if code := a.post(t, olga, "/api/org", adminOrgChange{What: "decide", Action: "deny", ID: "r-1", Reason: "not today"}, &answer); code != http.StatusOK {
		t.Fatalf("deny: %d %v", code, answer)
	}
	if ts.server.state.Requests["r-1"].DecidedBy != "olga" || ts.server.state.Users["ken"].Group != "staff" {
		t.Fatalf("decided by %q", ts.server.state.Requests["r-1"].DecidedBy)
	}
	if code := a.post(t, olga, "/api/org", adminOrgChange{What: "stop", Name: "gpu1"}, &answer); code != http.StatusOK {
		t.Fatalf("stop: %d %v", code, answer)
	}
	if _, running := ts.server.state.Leases["gpu1"]; running {
		t.Fatal("gpu1 still runs")
	}

	// The policy is checked before it's saved.
	if code := a.post(t, olga, "/api/org", adminOrgChange{What: "policy", Text: `{"groups": {"staff": {"max_hourz": 3}}}`}, &answer); code != http.StatusBadRequest {
		t.Fatalf("a broken policy: %d", code)
	}
	if code := a.post(t, olga, "/api/org", adminOrgChange{What: "policy", Text: `{"groups": {"staff": {"max_hours": 3}}}`}, &answer); code != http.StatusOK {
		t.Fatalf("policy: %d %v", code, answer)
	}
	if ts.server.policy().Groups["staff"].MaxHours != 3 {
		t.Fatal("the policy wasn't saved")
	}

	// A provider key goes in and never comes out.
	check := serverKeyCheck["runpod"]
	serverKeyCheck["runpod"] = func(string) error { return nil }
	t.Cleanup(func() { serverKeyCheck["runpod"] = check })
	if code := a.post(t, olga, "/api/org", adminOrgChange{What: "provider", Action: "set", Name: "runpod", Key: "rp-SECRETKEY"}, &answer); code != http.StatusOK {
		t.Fatalf("provider: %d %v", code, answer)
	}
	request, _ := http.NewRequest("GET", a.server.URL+"/api/org/settings", nil)
	request.AddCookie(olga)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	var raw bytes.Buffer
	raw.ReadFrom(response.Body)
	response.Body.Close()
	if strings.Contains(raw.String(), "SECRETKEY") || !strings.Contains(raw.String(), `{"name":"runpod","set":true}`) {
		t.Fatalf("settings: %s", raw.String())
	}

	// Users.
	if code := a.post(t, olga, "/api/org", adminOrgChange{What: "user", Action: "add", Name: "lin", Group: "staff"}, &answer); code != http.StatusOK || ts.server.state.Users["lin"] == nil {
		t.Fatalf("add a user: %d %v", code, answer)
	}
	if code := a.post(t, olga, "/api/org", adminOrgChange{What: "user", Action: "remove", Name: "lin"}, &answer); code != http.StatusOK || ts.server.state.Users["lin"] != nil {
		t.Fatalf("remove a user: %d %v", code, answer)
	}

	// Activity has the org's audit, under the admin's name.
	var activity []adminActivity
	a.get(t, olga, "/api/activity", &activity)
	found := false
	for _, entry := range activity {
		if entry.Org && entry.Who == "olga" && strings.HasPrefix(entry.What, "denied r-1") {
			found = true
		}
	}
	if !found {
		t.Fatalf("activity: %+v", activity)
	}

	// Only the owner changes the admins; sam is an admin, not the owner.
	sam := a.signIn(t, "sam")
	if code := a.post(t, sam, "/api/admins", map[string]string{"action": "add", "name": "lin", "slack": "U07LIN"}, &answer); code != http.StatusBadRequest {
		t.Fatalf("sam added an admin: %d", code)
	}
	if code := a.post(t, olga, "/api/admins", map[string]string{"action": "add", "name": "lin", "slack": "U07LIN"}, &answer); code != http.StatusOK || answer["link"] == nil {
		t.Fatalf("olga added an admin: %d %v", code, answer)
	}
	if people := loadOrgPeople(ts.dir); len(people.Admins) != 2 {
		t.Fatalf("org admins: %+v", people)
	}
	if code := a.post(t, olga, "/api/admins", map[string]string{"action": "remove", "name": "lin"}, &answer); code != http.StatusOK {
		t.Fatalf("remove lin: %d %v", code, answer)
	}

	// A new link comes from the org's Slack app.
	saveSlackConfig(ts.dir, slackConfig{BotToken: "xoxb-org", Channel: "C1", Approvers: map[string]string{}})
	a.s.sendNewLink("sam")
	a.mu.Lock()
	dms := strings.Join(*a.dms, "\n")
	a.mu.Unlock()
	if !strings.HasPrefix(dms, "xoxb-org U07SAM") {
		t.Fatalf("dms: %s", dms)
	}
}

// A wall link shows Running and Requests, with initials and no reasons
// unless it says so, can change nothing, and ends when ended.
func TestWallLinksShowAndChangeNothing(t *testing.T) {
	a, ts := orgAdminTest(t)
	olga := a.signIn(t, "olga")
	now := time.Now()
	ts.server.state.Leases["gpu1"] = &serverLease{Name: "gpu1", Provider: "runpod", Hardware: "l4", Rate: "$1.00/h", User: "alice",
		Request: "r-0", Started: now.Add(-time.Hour), Deadline: now.Add(time.Hour)}
	ts.server.state.Requests["r-0"] = &serverRequest{ID: "r-0", Kind: "compute", State: "running", User: "alice", Reason: "a secret plan"}
	ts.server.state.Requests["r-1"] = &serverRequest{ID: "r-1", Kind: "compute", State: "pending", User: "sam", Hardware: "l4", Reason: "thesis", Created: now}

	var answer map[string]string
	if code := a.post(t, olga, "/api/walls", map[string]any{"action": "add", "name": "Office TV"}, &answer); code != http.StatusOK || !strings.Contains(answer["link"], "/wall/") {
		t.Fatalf("add: %d %v", code, answer)
	}
	key := answer["link"][strings.LastIndex(answer["link"], "/")+1:]
	read := func(path string) (int, string) {
		response, err := http.Get(a.server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		var body bytes.Buffer
		body.ReadFrom(response.Body)
		return response.StatusCode, body.String()
	}
	if code, page := read("/wall/" + key); code != http.StatusOK || !strings.Contains(page, "wall.js") {
		t.Fatalf("page: %d", code)
	}
	code, body := read("/wall/" + key + "/state")
	var wall adminWallState
	json.Unmarshal([]byte(body), &wall)
	if code != http.StatusOK || len(wall.Running) != 1 || wall.Running[0].Who != "A." || len(wall.Waiting) != 1 || wall.Waiting[0].Who != "S." {
		t.Fatalf("state: %d %s", code, body)
	}
	if strings.Contains(body, "secret") || strings.Contains(body, "thesis") || strings.Contains(body, "alice") {
		t.Fatalf("the wall shows names or reasons: %s", body)
	}
	// A wall's key signs in to nothing.
	if code, _ := read("/api/state?key=" + key); code != http.StatusUnauthorized {
		t.Fatalf("the wall key reached the API: %d", code)
	}
	var settings adminOrgSettings
	a.get(t, olga, "/api/org/settings", &settings)
	if len(settings.Walls) != 1 || settings.Walls[0].Name != "Office TV" || settings.Walls[0].Seen.IsZero() {
		t.Fatalf("walls: %+v", settings.Walls)
	}
	if code := a.post(t, olga, "/api/walls", map[string]any{"action": "end", "name": "office tv"}, &answer); code != http.StatusOK {
		t.Fatalf("end: %d %v", code, answer)
	}
	if code, _ := read("/wall/" + key + "/state"); code != http.StatusNotFound {
		t.Fatalf("an ended wall: %d", code)
	}
}

// On the org server's machine, hi admin start asks nothing: the org's
// owner is the first admin, and gets the link.
func TestAdminStartOnTheOrgMachine(t *testing.T) {
	a, _ := orgAdminTest(t)
	_ = a
	os.Remove(adminConfigPath())
	reach, wait := adminDetectReach, adminStartWait
	adminDetectReach = func() (string, error) { return adminReachLocal, nil }
	adminStartWait = 0
	t.Cleanup(func() { adminDetectReach, adminStartWait = reach, wait })
	var out bytes.Buffer
	if err := adminStart([]string{"--local"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Your link, olga,") || !strings.Contains(out.String(), "the org's owner and admins") {
		t.Fatalf("start:\n%s", out.String())
	}
	var own adminConfig
	data, _ := os.ReadFile(adminConfigPath())
	json.Unmarshal(data, &own)
	if len(own.Admins) != 0 {
		t.Fatalf("admin.json keeps admins of its own: %+v", own.Admins)
	}
}
