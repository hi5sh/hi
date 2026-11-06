package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// setupAdminTest is an admin page with one admin, Olga, and one app,
// payments, whose bot posts and DMs into lists instead of Slack.
type adminTest struct {
	server *httptest.Server
	s      *adminServer
	jobs   *[]adminJob
	posts  *[]string
	dms    *[]string
	mu     *sync.Mutex
}

func setupAdminTest(t *testing.T) adminTest {
	t.Helper()
	dir := setupTeamTest(t)
	t.Setenv("HI_ADMIN_DIR", filepath.Join(dir, "admin"))
	config := testTeamConfig()
	writeTeamForAdmin(t, config)
	if err := saveAdminConfig(adminConfig{Reach: adminReachLocal, Admins: []adminPerson{{Name: "Olga", Slack: "U07OWNER1", Added: time.Now()}}}); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var jobs []adminJob
	var posts, dms []string
	launch, post, dm, alive, journal, systemctl, latest, lingers := adminLaunch, adminPost, adminDM, adminJobAlive, adminJournal, appSystemctl, adminLatest, appLingers
	adminLaunch = func(job adminJob, hi string) error {
		mu.Lock()
		defer mu.Unlock()
		jobs = append(jobs, job)
		return nil
	}
	adminPost = func(token, channel, text string) error {
		mu.Lock()
		defer mu.Unlock()
		posts = append(posts, token+" "+channel+" "+text)
		return nil
	}
	adminDM = func(token, user, text string) error {
		mu.Lock()
		defer mu.Unlock()
		dms = append(dms, token+" "+user+" "+text)
		return nil
	}
	adminJobAlive = func(string) bool { return true }
	adminJournal = func(args ...string) ([]byte, error) { return nil, nil }
	appSystemctl = func(args ...string) (string, error) { return "inactive", nil }
	adminLatest = func() (string, error) { return "v99.0.0", nil }
	appLingers = func() bool { return true }
	t.Cleanup(func() {
		adminLaunch, adminPost, adminDM, adminJobAlive, adminJournal, appSystemctl, adminLatest, appLingers = launch, post, dm, alive, journal, systemctl, latest, lingers
	})
	s := newAdminServer("/usr/local/bin/hi", io.Discard)
	server := httptest.NewServer(s.handler)
	t.Cleanup(server.Close)
	return adminTest{server: server, s: s, jobs: &jobs, posts: &posts, dms: &dms, mu: &mu}
}

// writeTeamForAdmin writes an app's folder as hi app new leaves it,
// with a bot token, and links it by name.
func writeTeamForAdmin(t *testing.T, config appConfig) {
	t.Helper()
	folder := filepath.Join(t.TempDir(), "app-"+config.Name)
	if err := os.MkdirAll(folder, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := appRegister(config.Name, folder); err != nil {
		t.Fatal(err)
	}
	if err := saveTeam(config); err != nil {
		t.Fatal(err)
	}
	if err := saveTeamSecrets(config.Name, appSecrets{SlackBotToken: "xoxb-" + config.Name}); err != nil {
		t.Fatal(err)
	}
}

// signIn opens an admin's link and returns the sign-in cookie.
func (a adminTest) signIn(t *testing.T, admin string) *http.Cookie {
	t.Helper()
	key, err := adminMakeLink(admin, true)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Get(a.server.URL + "/?key=" + key)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusSeeOther || response.Header.Get("Location") != "/" {
		t.Fatalf("signing in: %d to %q", response.StatusCode, response.Header.Get("Location"))
	}
	for _, cookie := range response.Cookies() {
		if cookie.Name == adminCookie {
			if !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || cookie.Path != "/" {
				t.Fatalf("the cookie: %+v", cookie)
			}
			return cookie
		}
	}
	t.Fatal("no sign-in cookie")
	return nil
}

func (a adminTest) get(t *testing.T, cookie *http.Cookie, path string, into any) int {
	t.Helper()
	request, _ := http.NewRequest("GET", a.server.URL+path, nil)
	if cookie != nil {
		request.AddCookie(cookie)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if into != nil {
		json.NewDecoder(response.Body).Decode(into)
	}
	return response.StatusCode
}

// post is a change from the page itself, unless origin says otherwise.
func (a adminTest) post(t *testing.T, cookie *http.Cookie, path string, body any, into any, origin ...string) int {
	t.Helper()
	data, _ := json.Marshal(body)
	request, _ := http.NewRequest("POST", a.server.URL+path, bytes.NewReader(data))
	request.Header.Set("Content-Type", "application/json")
	if len(origin) > 0 {
		if origin[0] != "" {
			request.Header.Set("Origin", origin[0])
		}
	} else {
		request.Header.Set("Origin", a.server.URL)
	}
	if cookie != nil {
		request.AddCookie(cookie)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if into != nil {
		json.NewDecoder(response.Body).Decode(into)
	}
	return response.StatusCode
}

func TestAdminSignInWithALink(t *testing.T) {
	a := setupAdminTest(t)
	// Without a sign-in, the API asks for one; the page itself loads.
	var answer map[string]any
	if code := a.get(t, nil, "/api/state", &answer); code != http.StatusUnauthorized || answer["signin"] != true {
		t.Fatalf("%d %v", code, answer)
	}
	if code := a.get(t, nil, "/", nil); code != http.StatusOK {
		t.Fatal(code)
	}
	cookie := a.signIn(t, "Olga")
	var state adminStateView
	if code := a.get(t, cookie, "/api/state", &state); code != http.StatusOK || state.Me.Name != "Olga" || len(state.Apps) != 1 || state.Apps[0].Name != "payments" {
		t.Fatalf("%d %+v", code, state)
	}
	// keys.json keeps hashes, never a key.
	data, _ := os.ReadFile(adminKeysPath())
	if strings.Contains(string(data), cookie.Value) {
		t.Fatal("keys.json holds the sign-in's key")
	}
	// A link that isn't one goes to the sign-in, saying so.
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, _ := client.Get(a.server.URL + "/?key=" + strings.Repeat("x", 43))
	response.Body.Close()
	if response.Header.Get("Location") != "/?ended=1" {
		t.Fatal(response.Header.Get("Location"))
	}
	// hi admin link ends the old link, not the sign-in it made.
	if _, err := adminMakeLink("Olga", true); err != nil {
		t.Fatal(err)
	}
	if code := a.get(t, cookie, "/api/state", nil); code != http.StatusOK {
		t.Fatal(code)
	}
	// Signing out ends it.
	if code := a.post(t, cookie, "/api/signout", map[string]any{}, nil); code != http.StatusOK {
		t.Fatal(code)
	}
	if code := a.get(t, cookie, "/api/state", nil); code != http.StatusUnauthorized {
		t.Fatal(code)
	}
}

func TestAdminChangesComeFromThePage(t *testing.T) {
	a := setupAdminTest(t)
	cookie := a.signIn(t, "Olga")
	body := map[string]string{"app": "payments", "action": "snapshot"}
	for _, origin := range []string{"", "http://evil.example"} {
		if code := a.post(t, cookie, "/api/action", body, nil, origin); code != http.StatusForbidden {
			t.Fatalf("origin %q: %d", origin, code)
		}
	}
	// A form can't send JSON.
	request, _ := http.NewRequest("POST", a.server.URL+"/api/action", strings.NewReader("app=payments&action=snapshot"))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Origin", a.server.URL)
	request.AddCookie(cookie)
	response, _ := http.DefaultClient.Do(request)
	response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatal(response.StatusCode)
	}
	if len(*a.jobs) != 0 {
		t.Fatalf("%+v", *a.jobs)
	}
	// Over https, a browser says where it comes from.
	request, _ = http.NewRequest("POST", a.server.URL+"/api/action", strings.NewReader(`{"app":"payments","action":"snapshot"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Sec-Fetch-Site", "cross-site")
	request.Header.Set("Origin", a.server.URL)
	request.AddCookie(cookie)
	response, _ = http.DefaultClient.Do(request)
	response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatal(response.StatusCode)
	}
}

func TestAdminActionsAreTheCommands(t *testing.T) {
	a := setupAdminTest(t)
	cookie := a.signIn(t, "Olga")
	cases := map[string][]string{
		"restart":  {"app", "up", "payments"},
		"stop":     {"app", "down", "payments"},
		"rebuild":  {"app", "rebuild", "payments"},
		"snapshot": {"app", "snapshot", "payments"},
		"unfreeze": {"app", "unfreeze", "payments"},
	}
	for action, want := range cases {
		var job adminJob
		if code := a.post(t, cookie, "/api/action", map[string]string{"app": "payments", "action": action}, &job); code != http.StatusOK {
			t.Fatalf("%s: %d", action, code)
		}
		if strings.Join(job.Args, " ") != strings.Join(want, " ") || job.By != "Olga" || job.State != "running" || !adminJobID.MatchString(job.ID) {
			t.Fatalf("%s: %+v", action, job)
		}
	}
	// Freezing says why, for the ops channel.
	var failed map[string]string
	if code := a.post(t, cookie, "/api/action", map[string]string{"app": "payments", "action": "freeze"}, &failed); code != http.StatusBadRequest || !strings.Contains(failed["error"], "say why") {
		t.Fatalf("%d %v", code, failed)
	}
	var job adminJob
	a.post(t, cookie, "/api/action", map[string]string{"app": "payments", "action": "freeze", "why": "odd traffic"}, &job)
	if strings.Join(job.Args, " ") != "app freeze payments --why odd traffic" {
		t.Fatal(job.Args)
	}
	// No other app, and no other action.
	if code := a.post(t, cookie, "/api/action", map[string]string{"app": "../x", "action": "stop"}, nil); code != http.StatusBadRequest {
		t.Fatal(code)
	}
	if code := a.post(t, cookie, "/api/action", map[string]string{"app": "payments", "action": "rm"}, nil); code != http.StatusBadRequest {
		t.Fatal(code)
	}
	// Each is in Activity and in Jobs.
	var activity []adminActivity
	a.get(t, cookie, "/api/activity", &activity)
	if len(activity) == 0 || activity[0].Who != "Olga" || activity[0].App != "payments" || !strings.HasPrefix(activity[0].What, "freeze") {
		t.Fatalf("%+v", activity)
	}
	var jobs []adminJob
	a.get(t, cookie, "/api/jobs", &jobs)
	if len(jobs) != 6 {
		t.Fatalf("%d jobs", len(jobs))
	}
}

func TestAdminJobTellsTheApp(t *testing.T) {
	a := setupAdminTest(t)
	adminTellApp(adminJob{App: "payments", By: "Olga", Said: "restarted the app", Tried: "restart the app", State: "done"})
	adminTellApp(adminJob{App: "payments", By: "Olga", Said: "stopped the app", Tried: "stop the app", State: "failed"})
	adminTellApp(adminJob{App: "payments", By: "Olga", Said: "made themselves an owner of the app", Main: true, State: "done"})
	adminTellApp(adminJob{App: "payments", By: "Olga", State: "done"}) // the broker says freezes itself
	want := []string{
		"xoxb-payments C07PAYMENTS :gear: Olga restarted the app, from the admin page.",
		"xoxb-payments C07PAYMENTS :warning: Olga tried to stop the app from the admin page, and it failed. Its output is in the admin page's Jobs.",
		"xoxb-payments C07PAYMENTS :gear: Olga made themselves an owner of the app, from the admin page.",
	}
	if strings.Join(*a.posts, "\n") != strings.Join(want, "\n") {
		t.Fatalf("%q", *a.posts)
	}
	// What the command records names the admin.
	t.Setenv(adminByEnv, "Olga")
	if appActor() != "Olga on the admin page" {
		t.Fatal(appActor())
	}
}

func TestAdminJobThatStoppedFailed(t *testing.T) {
	setupAdminTest(t)
	adminJobAlive = func(string) bool { return false }
	job := adminJob{ID: "20261106-120000-abcdef", Title: "Restart payments", Args: []string{"app", "up", "payments"}, By: "Olga", State: "running", Started: time.Now().Add(-time.Minute)}
	if err := saveAdminJob(job); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(adminJobLogPath(job.ID), []byte("App payments is starting.\n"), 0o600)
	jobs := adminJobs()
	if len(jobs) != 1 || jobs[0].State != "failed" || !strings.Contains(jobs[0].Error, "without saying") {
		t.Fatalf("%+v", jobs)
	}
	if text, next := adminJobOutput(job.ID, 0); text != "App payments is starting.\n" || next != int64(len(text)) {
		t.Fatalf("%q %d", text, next)
	}
	if text, _ := adminJobOutput(job.ID, 26); text != "" {
		t.Fatal(text)
	}
	if _, err := loadAdminJob("../../etc/passwd"); err == nil {
		t.Fatal("a path as a job")
	}
}

func TestAdminSendsANewLinkByDM(t *testing.T) {
	a := setupAdminTest(t)
	adminAdd("Sam", "", "test") // no Slack: nothing to send
	var answer map[string]string
	for _, who := range []string{"nobody", "Sam", "olga", "olga"} {
		if code := a.post(t, nil, "/api/new-link", map[string]string{"who": who}, &answer); code != http.StatusOK || !strings.Contains(answer["text"], "If you're one") {
			t.Fatalf("%s: %d %v", who, code, answer)
		}
	}
	// It goes in the background, once in two minutes.
	deadline := time.Now().Add(2 * time.Second)
	for {
		a.mu.Lock()
		n := len(*a.dms)
		a.mu.Unlock()
		if n >= 1 || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond)
	a.mu.Lock()
	dms := append([]string{}, *a.dms...)
	a.mu.Unlock()
	if len(dms) != 1 || !strings.HasPrefix(dms[0], "xoxb-payments U07OWNER1 :key: Your link to the admin page") {
		t.Fatalf("%q", dms)
	}
	// The link in it signs in.
	link := dms[0][strings.Index(dms[0], "<")+1 : strings.Index(dms[0], "|")]
	parsed, _ := url.Parse(link)
	if _, _, err := adminSignIn(parsed.Query().Get(adminKeyParam)); err != nil {
		t.Fatal(err)
	}
	// From another site, nothing.
	if code := a.post(t, nil, "/api/new-link", map[string]string{"who": "olga"}, nil, "http://evil.example"); code != http.StatusForbidden {
		t.Fatal(code)
	}
}

func TestAdminAdmins(t *testing.T) {
	a := setupAdminTest(t)
	cookie := a.signIn(t, "Olga")
	var answer map[string]string
	if code := a.post(t, cookie, "/api/admins", map[string]any{"action": "add", "name": "Sam", "slack": "U07SAM"}, &answer); code != http.StatusOK || !strings.Contains(answer["link"], "?key=") {
		t.Fatalf("%d %v", code, answer)
	}
	if code := a.post(t, cookie, "/api/admins", map[string]any{"action": "add", "name": "sam"}, &answer); code != http.StatusBadRequest || !strings.Contains(answer["error"], "already") {
		t.Fatalf("%d %v", code, answer)
	}
	// Sam signs in with their link; Olga removes them, and their sign-in ends.
	samKey, _ := adminMakeLink("Sam", true)
	session, _, err := adminSignIn(samKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := adminSessionAdmin(session); !ok {
		t.Fatal("Sam isn't signed in")
	}
	if code := a.post(t, cookie, "/api/admins", map[string]any{"action": "remove", "name": "Sam"}, &answer); code != http.StatusOK {
		t.Fatalf("%d %v", code, answer)
	}
	if _, ok := adminSessionAdmin(session); ok {
		t.Fatal("Sam is still signed in")
	}
	// The last admin stays.
	if code := a.post(t, cookie, "/api/admins", map[string]any{"action": "remove", "name": "Olga"}, &answer); code != http.StatusBadRequest || !strings.Contains(answer["error"], "only admin") {
		t.Fatalf("%d %v", code, answer)
	}
	// A new link by Slack.
	if code := a.post(t, cookie, "/api/admins", map[string]any{"action": "link", "name": "Olga", "send": true}, &answer); code != http.StatusOK || len(*a.dms) != 1 {
		t.Fatalf("%d %v %q", code, answer, *a.dms)
	}
	var activity []adminActivity
	a.get(t, cookie, "/api/activity", &activity)
	var said []string
	for _, entry := range activity {
		said = append(said, entry.Who+": "+entry.What)
	}
	for _, want := range []string{"Olga on the admin page: made Sam an admin", "Olga on the admin page: took Sam off the admins", "Olga: made a new link for Olga"} {
		if !strings.Contains(strings.Join(said, "\n"), want) {
			t.Fatalf("no %q in\n%s", want, strings.Join(said, "\n"))
		}
	}
}

func TestAdminOpensTheDevPageAsTheAdminIs(t *testing.T) {
	a := setupAdminTest(t)
	cookie := a.signIn(t, "Olga")
	// Olga owns payments, which is down: no service to make the link.
	var answer map[string]any
	if code := a.post(t, cookie, "/api/devpage", map[string]string{"app": "payments"}, &answer); code != http.StatusBadRequest || !strings.Contains(fmt.Sprint(answer["error"]), "isn't up") {
		t.Fatalf("%d %v", code, answer)
	}
	// Someone with no level gets no link, and is offered to be an owner.
	adminAdd("Sam", "U07SAM", "test")
	sam := a.signIn(t, "Sam")
	if code := a.post(t, sam, "/api/devpage", map[string]string{"app": "payments"}, &answer); code != http.StatusOK || answer["builds"] != false || answer["link"] != nil {
		t.Fatalf("%d %v", code, answer)
	}
	var job adminJob
	if code := a.post(t, sam, "/api/owner", map[string]string{"app": "payments"}, &job); code != http.StatusOK || strings.Join(job.Args, " ") != "app owners payments add U07SAM" || !job.Main {
		t.Fatalf("%d %+v", code, job)
	}
	// Without a Slack member ID, the app can't tell who someone is.
	adminAdd("Kim", "", "test")
	kim := a.signIn(t, "Kim")
	a.post(t, kim, "/api/devpage", map[string]string{"app": "payments"}, &answer)
	if answer["builds"] != false || !strings.Contains(fmt.Sprint(answer["why"]), "Slack member ID") {
		t.Fatalf("%v", answer)
	}
}

func TestAdminBrokerMakesADevPageLink(t *testing.T) {
	setupAdminTest(t)
	b := &appBroker{team: "payments"}
	if _, err := b.devPageLinkFor("U07OWNER1"); err == nil || !strings.Contains(err.Error(), "no address yet") {
		t.Fatal(err)
	}
	data, _ := json.Marshal(appDevPageState{URL: "https://payments-dev.corp.example/__hi/dev/", Expose: "internal:dev"})
	os.MkdirAll(filepath.Dir(appDevPageStatePath("payments")), 0o700)
	os.WriteFile(appDevPageStatePath("payments"), data, 0o600)
	link, err := b.devPageLinkFor("U07OWNER1")
	if err != nil || !strings.HasPrefix(link, "https://payments-dev.corp.example/__hi/dev/?hi_user=") {
		t.Fatal(link, err)
	}
	key := strings.TrimPrefix(link, "https://payments-dev.corp.example/__hi/dev/?hi_user=")
	config, _ := loadTeam("payments")
	if person, ok, _ := b.webUser(config, key); !ok || person.User != "U07OWNER1" {
		t.Fatalf("%+v %v", person, ok)
	}
	if _, err := b.devPageLinkFor("U07NOBODY"); err == nil {
		t.Fatal("a link for someone who doesn't build")
	}
}

func TestAdminAppViews(t *testing.T) {
	a := setupAdminTest(t)
	cookie := a.signIn(t, "Olga")
	saveTeamWatchStop("payments", appWatchStopState{Frozen: true, By: "Olga on the admin page", Because: "odd traffic", At: time.Now()})
	saveTeamIncident("payments", appIncident{ID: "i-1", Kind: "alert", Title: "Invoices fail", Status: "open", Opened: time.Now(),
		Events: []appIncidentEvent{{At: time.Now(), Who: "lead", Action: "note", Result: "looking"}}})
	var detail adminAppDetail
	if code := a.get(t, cookie, "/api/app?name=payments", &detail); code != http.StatusOK {
		t.Fatal(code)
	}
	if detail.State != "frozen" || detail.Stopped == nil || detail.Stopped.Because != "odd traffic" || detail.Level != appLevelOwner || detail.Incidents != 1 || len(detail.Members) == 0 {
		t.Fatalf("%+v", detail)
	}
	if code := a.get(t, cookie, "/api/app?name=nothing", nil); code != http.StatusNotFound {
		t.Fatal(code)
	}
	var incidents []adminIncident
	a.get(t, cookie, "/api/incidents", &incidents)
	if len(incidents) != 1 || incidents[0].App != "payments" || incidents[0].Last != "the lead: looking" {
		t.Fatalf("%+v", incidents)
	}
	// A newer hi is out.
	saved := version
	version = "v1.0.0"
	t.Cleanup(func() { version = saved })
	a.s.latestVersion()
	time.Sleep(50 * time.Millisecond)
	var state adminStateView
	a.get(t, cookie, "/api/state", &state)
	if state.Latest != "v99.0.0" {
		t.Fatalf("%+v", state)
	}
}

func TestAdminLogs(t *testing.T) {
	a := setupAdminTest(t)
	cookie := a.signIn(t, "Olga")
	var asked [][]string
	adminJournal = func(args ...string) ([]byte, error) {
		asked = append(asked, args)
		return []byte(`{"__CURSOR":"c1","__REALTIME_TIMESTAMP":"1762430400000000","MESSAGE":"lead: ready"}` + "\n" +
			`{"__CURSOR":"c2","__REALTIME_TIMESTAMP":"1762430401000000","MESSAGE":[104,105]}` + "\n"), nil
	}
	var answer struct {
		Lines  []adminLogLine `json:"lines"`
		Cursor string         `json:"cursor"`
	}
	a.get(t, cookie, "/api/logs?app=payments", &answer)
	if len(answer.Lines) != 2 || answer.Lines[0].Text != "lead: ready" || answer.Lines[1].Text != "hi" || answer.Cursor != "c2" {
		t.Fatalf("%+v", answer)
	}
	a.get(t, cookie, "/api/logs?app=payments&cursor=c2", &answer)
	if got := strings.Join(asked[1], " "); !strings.Contains(got, "-u hi-app-payments.service -t hi") || !strings.Contains(got, "--after-cursor c2") {
		t.Fatal(got)
	}
	a.get(t, cookie, "/api/logs", &answer)
	if got := strings.Join(asked[2], " "); !strings.Contains(got, "-u hi-admin.service") {
		t.Fatal(got)
	}
	if code := a.get(t, cookie, "/api/logs?app=../x", nil); code != http.StatusBadRequest {
		t.Fatal(code)
	}
}

func TestAdminStartMakesTheFirstAdmin(t *testing.T) {
	dir := setupTeamTest(t)
	t.Setenv("HI_ADMIN_DIR", filepath.Join(dir, "admin"))
	var calls []string
	systemctl, lingers, reach, wait := appSystemctl, appLingers, adminDetectReach, adminStartWait
	adminStartWait = 0
	appSystemctl = func(args ...string) (string, error) { calls = append(calls, strings.Join(args, " ")); return "", nil }
	appLingers = func() bool { return false }
	adminDetectReach = func() (string, error) { return adminReachNetBird, nil }
	t.Cleanup(func() { appSystemctl, appLingers, adminDetectReach, adminStartWait = systemctl, lingers, reach, wait })
	name := adminMachineNetBirdName
	adminMachineNetBirdName = func() string { return "ws1.corp.example" }
	t.Cleanup(func() { adminMachineNetBirdName = name })

	var out bytes.Buffer
	if err := adminStart([]string{"--name", "Olga", "--slack", "U07OWNER1"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	config, _ := loadAdminConfig()
	if len(config.Admins) != 1 || config.Admins[0].Name != "Olga" || config.Admins[0].Slack != "U07OWNER1" || config.Reach != adminReachNetBird {
		t.Fatalf("%+v", config)
	}
	if strings.Join(calls, "|") != "daemon-reload|enable hi-admin.service|restart hi-admin.service" {
		t.Fatal(calls)
	}
	text := out.String()
	for _, want := range []string{"http://ws1.corp.example:1984/?key=", "TCP 1984", "sudo loginctl enable-linger"} {
		if !strings.Contains(text, want) {
			t.Fatalf("no %q in\n%s", want, text)
		}
	}
	unit, _ := os.ReadFile(adminUnitPath())
	if !strings.Contains(string(unit), "admin serve") || !strings.Contains(string(unit), "Environment=HI_ADMIN_DIR=") {
		t.Fatal(string(unit))
	}
	// Again: no second first admin, no link.
	out.Reset()
	if err := adminStart(nil, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	if config, _ = loadAdminConfig(); len(config.Admins) != 1 || strings.Contains(out.String(), "?key=") {
		t.Fatalf("%+v\n%s", config, out.String())
	}
	// --netbird is for a peer of its own.
	os.WriteFile(filepath.Join(dir, "key"), []byte("A1B2C3\n"), 0o600)
	if err := adminStart([]string{"--netbird", filepath.Join(dir, "key")}, strings.NewReader(""), &out); err == nil {
		t.Fatal("a setup key on a machine without podman")
	}
}

func TestAdminAddressAndReach(t *testing.T) {
	setupAdminTest(t)
	name := adminMachineNetBirdName
	adminMachineNetBirdName = func() string { return "ws1.corp.example" }
	t.Cleanup(func() { adminMachineNetBirdName = name })
	t.Setenv("HOSTNAME", "")
	config, _ := loadAdminConfig()
	if adminURL(config) != "http://127.0.0.1:1984/" {
		t.Fatal(adminURL(config))
	}
	config.Reach = adminReachNetBird
	if adminURL(config) != "http://ws1.corp.example:1984/" {
		t.Fatal(adminURL(config))
	}
	// A peer that hasn't joined: the machine's address until it has.
	config.Reach = adminReachPeer
	if adminURL(config) != "http://ws1.corp.example:1984/" || !strings.Contains(adminReachLine(config), "setup key") {
		t.Fatal(adminURL(config), adminReachLine(config))
	}
	os.MkdirAll(adminPeerDir(), 0o700)
	os.WriteFile(filepath.Join(adminPeerDir(), "default.json"), []byte("{}"), 0o600)
	os.WriteFile(filepath.Join(adminDir(), "netbird-fqdn"), []byte("ws1-admin.corp.example\n"), 0o600)
	if adminURL(config) != "http://ws1-admin.corp.example:1984/" || adminOnMachineAddress(config) {
		t.Fatal(adminURL(config))
	}
	os.WriteFile(adminHTTPSDomainFile(), []byte("corp.example\n"), 0o600)
	if adminURL(config) != "https://admin.corp.example/" {
		t.Fatal(adminURL(config))
	}
	saveAdminConfig(config)
	var out bytes.Buffer
	if err := adminAddressCommand([]string{"admin.it.example", "--https"}, &out); err != nil {
		t.Fatal(err)
	}
	config, _ = loadAdminConfig()
	if adminURL(config) != "https://admin.it.example/" {
		t.Fatal(adminURL(config))
	}
	if err := adminAddressCommand([]string{"not a host"}, &out); err == nil {
		t.Fatal("not a host")
	}
	// The peer's script passes 1984 and 80 on, and 443 with a domain.
	script := adminPeerScript(true, true)
	for _, want := range []string{"127.0.0.1:1984 unix:/hi-admin/http.sock", "127.0.0.1:80 unix:/hi-admin/http.sock", "127.0.0.1:443 unix:/hi-admin/https.sock", "--hostname " + adminPeerHostname()} {
		if !strings.Contains(script, want) {
			t.Fatalf("no %q in %s", want, script)
		}
	}
	if strings.Contains(adminPeerScript(false, false), "443") || !strings.Contains(adminPeerScript(false, false), "--setup-key-file") {
		t.Fatal(adminPeerScript(false, false))
	}
}

func TestAdminPortIsNeverPublic(t *testing.T) {
	setupAdminTest(t)
	if _, _, err := parseNetExpose([]string{"1984"}); err == nil || !strings.Contains(err.Error(), "never public") {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := appCreate("admin", "", "", nil, strings.NewReader(""), &out); err == nil || !strings.Contains(err.Error(), "admin page's name") {
		t.Fatal(err)
	}
}

func TestUpdateRestartsTheAdminPage(t *testing.T) {
	requireLinux(t)
	installedBinary(t, "v0.9.0")
	fakeReleases(t, "v0.9.0", false)
	path, _ := updateExecutable()
	fakeProc(t, map[string][3]string{
		"301": {path + " (deleted)", path + "\x00admin\x00serve\x00", "0::/user.slice/user-1000.slice/user@1000.service/app.slice/hi-admin.service\n"},
		"302": {path + " (deleted)", path + "\x00admin\x00__job\x00x\x00", "0::/user.slice/user-1000.slice/user@1000.service/app.slice/hi-admin-job-x.service\n"},
	})
	runs := recordUpdateRuns(t)
	var out, errOut bytes.Buffer
	run([]string{"update", "--restart"}, strings.NewReader(""), &out, &errOut)
	if strings.Join(*runs, "|") != "systemctl --user restart hi-admin.service|systemctl --user is-active --quiet hi-admin.service" {
		t.Fatalf("%q\n%s%s", *runs, out.String(), errOut.String())
	}
}
