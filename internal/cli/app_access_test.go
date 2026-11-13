package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

const appTestDeveloperKey = "developer-key-0123456789ab"

// appTestDevPageWithDeveloper is the dev page's test team, with Dev a
// developer signed in.
func appTestDevPageWithDeveloper(t *testing.T) *appTestBroker {
	t.Helper()
	b, _ := appTestDevPage(t)
	config, _ := loadTeam("payments")
	config.Developers = []string{"U07DEV001"}
	saveTeam(config)
	people := loadTeamPeople("payments")
	people.Keys = append(people.Keys, appPersonKey{Key: appTestDeveloperKey, User: "U07DEV001", Name: "Dev", Ends: time.Now().Add(time.Hour)})
	saveTeamPeople("payments", people)
	return b
}

// Levels: owners are the admin's, developers and contributors the
// owners', and being in the channel gives nobody one.
func TestTeamAccessLevels(t *testing.T) {
	setupTeamTest(t)
	os.MkdirAll(appDir("payments"), 0o700)
	saveTeam(testTeamConfig())
	config, _ := loadTeam("payments")
	owner := config.Owners[0]
	if appLevel(config, owner) != appLevelOwner || appLevel(config, "U07CHAN01") != appLevelNone {
		t.Fatal("levels")
	}
	// A contributor, by an owner's choice.
	if text, err := appSetLevel(&config, "U07CHAN01", appLevelContributor); err != nil || text != "U07CHAN01 is a contributor of app payments." {
		t.Fatal(text, err)
	}
	if _, err := appSetLevel(&config, owner, appLevelDeveloper); err == nil || !strings.Contains(err.Error(), "hi app owners") {
		t.Fatal("an owner made a developer:", err)
	}
	if _, err := appSetLevel(&config, "U07DEV001", "admin"); err == nil {
		t.Fatal("took a level that isn't one")
	}
	// A developer, with a key and a link.
	text, err := appSetLevel(&config, "U07DEV001", appLevelDeveloper)
	if err != nil || text != "U07DEV001 is a Pro Dev of app payments." {
		t.Fatal(text, err)
	}
	saved, _ := loadTeam("payments")
	if !appBuilds(saved, "U07DEV001") || appLevel(saved, "U07DEV001") != appLevelDeveloper || appLevel(saved, "U07CHAN01") != appLevelContributor {
		t.Fatalf("%+v", saved)
	}
	if err := appSetDevKey(saved, "U07DEV001", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExample dev@laptop"); err != nil {
		t.Fatal(err)
	}
	if keys, _ := os.ReadFile(appDevDir("payments") + "/ssh/authorized_keys"); !strings.Contains(string(keys), "dev@laptop") {
		t.Fatalf("the box doesn't take the developer's key: %q", keys)
	}
	if err := appSetDevKey(saved, "U07CHAN01", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExample chan@laptop"); err == nil {
		t.Fatal("a contributor's key went into the dev box")
	}
	saveTeamPeople("payments", appPeopleFile{Keys: []appPersonKey{{Key: appTestDeveloperKey, User: "U07DEV001", Name: "Dev", Ends: time.Now().Add(time.Hour)}}})
	// Back to a contributor: the key is out, the link ends.
	text, err = appSetLevel(&saved, "U07DEV001", appLevelContributor)
	if err != nil || !strings.Contains(text, "U07DEV001 is a contributor of app payments. Their SSH key is out of the dev box, and their private links have ended.") {
		t.Fatal(text, err)
	}
	if keys, _ := os.ReadFile(appDevDir("payments") + "/ssh/authorized_keys"); strings.Contains(string(keys), "dev@laptop") {
		t.Fatal("the key stayed")
	}
	if people := loadTeamPeople("payments"); time.Now().Before(people.Keys[0].Ends) {
		t.Fatal("the link still works")
	}
	// No level any more.
	if text, err := appSetLevel(&saved, "U07CHAN01", appLevelNone); err != nil || !strings.Contains(text, "has no level in app payments") {
		t.Fatal(text, err)
	}
	saved, _ = loadTeam("payments")
	if appLevel(saved, "U07CHAN01") != appLevelNone || slices.Contains(saved.Contributors, "U07CHAN01") {
		t.Fatalf("%+v", saved)
	}
	// hi app access lists who is which.
	var out strings.Builder
	if err := appAccessCommand([]string{"payments", "U07DEV001", "developer"}, &out); err != nil || !strings.Contains(out.String(), "a developer can read it through a preview") {
		t.Fatal(out.String(), err)
	}
	out.Reset()
	appAccessCommand([]string{"payments"}, &out)
	if !strings.Contains(out.String(), "Pro Devs, with Chat, terminal, and SSH") || !strings.Contains(out.String(), "U07DEV001") || !strings.Contains(out.String(), "Anyone else, in the channel or not, has no level.") {
		t.Fatal(out.String())
	}
}

// Every route of the dev page, found in hi's source, goes through the
// gate: without a builder's key, 401; for a developer, 403 unless the
// route is a developer's; an owner gets past it everywhere.
func TestTeamDevPageRoutesByLevel(t *testing.T) {
	source, _ := os.Getwd() // the tests' setup moves to a folder of its own
	b := appTestDevPageWithDeveloper(t)
	b.running = func(string) bool { return true }
	handler := b.devPageHandler()
	pattern := regexp.MustCompile(`"([A-Z]+) "\+appDevPagePrefix\+"(api/[a-z/-]+)"`)
	var routes [][2]string
	files, _ := os.ReadDir(source)
	for _, file := range files {
		if !strings.HasSuffix(file.Name(), ".go") || strings.HasSuffix(file.Name(), "_test.go") {
			continue
		}
		data, _ := os.ReadFile(source + "/" + file.Name())
		for _, match := range pattern.FindAllStringSubmatch(string(data), -1) {
			routes = append(routes, [2]string{match[1], match[2]})
		}
	}
	if len(routes) < 40 {
		t.Fatalf("found only %d routes", len(routes))
	}
	call := func(key, method, path string) int {
		request := httptest.NewRequest(method, appDevPagePrefix+path, strings.NewReader("{"))
		if key != "" {
			request.Header.Set(netExposeUserHeader, key)
		}
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		return recorder.Code
	}
	developerRoutes := 0
	for _, route := range routes {
		method, path := route[0], route[1]
		ours := appDevDeveloperRoutes[method+" "+appDevPagePrefix+path]
		if ours {
			developerRoutes++
		}
		if code := call("", method, path); code != http.StatusUnauthorized {
			t.Errorf("%s %s with no key: %d", method, path, code)
		}
		if code := call(appTestAskerKey, method, path); code != http.StatusUnauthorized {
			t.Errorf("%s %s for someone in the channel: %d", method, path, code)
		}
		code := call(appTestDeveloperKey, method, path)
		if ours && (code == http.StatusUnauthorized || code == http.StatusForbidden) || !ours && code != http.StatusForbidden {
			t.Errorf("%s %s for a developer: %d", method, path, code)
		}
		if code := call(appTestOwnerKey, method, path); code == http.StatusUnauthorized || code == http.StatusForbidden {
			t.Errorf("%s %s for an owner: %d", method, path, code)
		}
	}
	// Rollbacks, data, settings, keys, sites, addresses, and People are
	// the owners'; the rest is a developer's.
	for _, owners := range []string{"POST api/rollback", "POST api/dev/reset", "GET api/data", "POST api/data/copy", "POST api/settings", "POST api/site", "POST api/public", "POST api/helper", "POST api/incident", "POST api/rule", "GET api/people", "POST api/people"} {
		method, path, _ := strings.Cut(owners, " ")
		if appDevDeveloperRoutes[method+" "+appDevPagePrefix+path] {
			t.Errorf("%s is a developer's", owners)
		}
	}
	// The views the page shows a developer: source control, search,
	// checks, logs, and the editor.
	for _, builders := range []string{"GET api/git", "GET api/git/diff", "POST api/git", "GET api/search", "GET api/checks", "POST api/checks", "GET api/logs", "GET api/files/stat", "POST api/files/save"} {
		method, path, _ := strings.Cut(builders, " ")
		if !appDevDeveloperRoutes[method+" "+appDevPagePrefix+path] {
			t.Errorf("%s isn't a developer's", builders)
		}
	}
	if developerRoutes < 20 {
		t.Fatalf("only %d of the routes are a developer's", developerRoutes)
	}
}

// A change made from another page is refused, the owner's cookie or not:
// the app's preview, on the same machine at another port, is code from
// the dev box.
func TestTeamDevPageRefusesOtherPages(t *testing.T) {
	b := appTestDevPageWithDeveloper(t)
	b.running = func(string) bool { return true }
	handler := b.devPageHandler()
	post := func(headers map[string]string) int {
		request := httptest.NewRequest("POST", "http://peer.example:8888"+appDevPagePrefix+"api/people", strings.NewReader(`{"user":"U07ASKER1","level":"developer"}`))
		request.Header.Set(netExposeUserHeader, appTestOwnerKey)
		for key, value := range headers {
			request.Header.Set(key, value)
		}
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		return recorder.Code
	}
	for name, headers := range map[string]map[string]string{
		"the preview":     {"Origin": "http://peer.example:8080", "Sec-Fetch-Site": "same-site"},
		"a sibling name":  {"Origin": "https://payments.example.com", "Sec-Fetch-Site": "same-site"},
		"no Origin":       {"Sec-Fetch-Site": "cross-site"},
		"a forged origin": {"Origin": "http://evil.example"},
	} {
		if code := post(headers); code != http.StatusForbidden {
			t.Errorf("%s: %d", name, code)
		}
	}
	if code := post(map[string]string{"Origin": "http://peer.example:8888", "Sec-Fetch-Site": "same-origin"}); code == http.StatusForbidden || code == http.StatusUnauthorized {
		t.Errorf("the page itself: %d", code)
	}
}

// A developer's page: their level, no keys, their own SSH key; the
// owners' People section changes levels and the channel hears it.
func TestTeamDevPagePeople(t *testing.T) {
	b := appTestDevPageWithDeveloper(t)
	b.running = func(string) bool { return true }
	channel := &fakeTeamChannel{}
	b.channel = channel
	saveTeamSecrets("payments", appSecrets{})
	handler := b.devPageHandler()
	code, body := appTestDevPageCall(t, handler, appTestDeveloperKey, "GET", "api/state", nil)
	if code != 200 || !strings.Contains(body, `"level":"developer"`) || strings.Contains(body, `"keys"`) {
		t.Fatalf("%d %s", code, body)
	}
	if code, body := appTestDevPageCall(t, handler, appTestOwnerKey, "GET", "api/state", nil); code != 200 || !strings.Contains(body, `"level":"owner"`) || !strings.Contains(body, `"keys"`) {
		t.Fatalf("%d %s", code, body)
	}
	// Their own key, from the page.
	if code, body := appTestDevPageCall(t, handler, appTestDeveloperKey, "POST", "api/sshkey", map[string]string{"key": "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExample dev@laptop"}); code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	if _, body := appTestDevPageCall(t, handler, appTestDeveloperKey, "GET", "api/state", nil); !strings.Contains(body, `"ssh_key":"ssh-ed25519 dev@laptop"`) {
		t.Fatal(body)
	}
	// The owners' People section: the channel's people are contributors.
	code, body = appTestDevPageCall(t, handler, appTestOwnerKey, "GET", "api/people", nil)
	// Owners first, then developers, then the rest of the channel, who
	// have no level until an owner gives one.
	var view appDevPeopleView
	json.Unmarshal([]byte(body), &view)
	var order []string
	for _, person := range view.People {
		order = append(order, person.ID+":"+person.Level)
	}
	if code != 200 || strings.Join(order, " ") != "U07OWNER1:owner U07DEV001:developer U07ANNA01:none U1:none U2:none" || !view.PreviewData {
		t.Fatalf("%d %s", code, body)
	}
	if code, body := appTestDevPageCall(t, handler, appTestOwnerKey, "POST", "api/people", map[string]string{"user": "U07ANNA01", "level": "developer"}); code != 200 || !strings.Contains(body, "U07ANNA01 is a Pro Dev") {
		t.Fatalf("%d %s", code, body)
	}
	appTestDevPageCall(t, handler, appTestOwnerKey, "POST", "api/people", map[string]string{"user": "U07ANNA01", "level": "contributor"})
	appTestDevPageCall(t, handler, appTestOwnerKey, "POST", "api/people", map[string]string{"user": "U07ANNA01", "level": "none"})
	calls := strings.Join(channel.calls, "\n")
	if !strings.Contains(calls, "made <@U07ANNA01> a Pro Dev.") || !strings.Contains(calls, "made <@U07ANNA01> a contributor.") || !strings.Contains(calls, "took away <@U07ANNA01>'s level") {
		t.Fatalf("%q", channel.calls)
	}
	if code, _ := appTestDevPageCall(t, handler, appTestDeveloperKey, "POST", "api/people", map[string]string{"user": "U2", "level": "developer"}); code != http.StatusForbidden {
		t.Fatal("a developer made a developer:", code)
	}
	// The developer is no longer one: their next click is refused.
	appTestDevPageCall(t, handler, appTestOwnerKey, "POST", "api/people", map[string]string{"user": "U07DEV001", "level": "contributor"})
	if code, _ := appTestDevPageCall(t, handler, appTestDeveloperKey, "GET", "api/state", nil); code != http.StatusUnauthorized {
		t.Fatal("a former developer got the page:", code)
	}
}

// A developer's open terminal closes when they stop being one, without
// waiting for their next click.
func TestTeamDevPageTerminalEndsWithAccess(t *testing.T) {
	b := appTestDevPageWithDeveloper(t)
	previousCommand, previousCheck := appDevTermCommand, appDevAccessCheck
	appDevTermCommand = func(string) []string { return []string{"sh", "-c", "sleep 30"} }
	appDevAccessCheck = 50 * time.Millisecond
	t.Cleanup(func() { appDevTermCommand, appDevAccessCheck = previousCommand, previousCheck })
	server := httptest.NewServer(b.devPageHandler())
	t.Cleanup(func() { server.CloseClientConnections(); server.Close(); appDevSocketsOpen.Wait() })
	state := `{"url":"https://app-payments-dev.example.net/?hi_key=k","expose":"app-payments-dev"}`
	os.MkdirAll(appDir("payments")+"/run", 0o700)
	os.WriteFile(appDevPageStatePath("payments"), []byte(state), 0o600)
	header := http.Header{netExposeUserHeader: {appTestDeveloperKey}, "Origin": {"https://app-payments-dev.example.net"}}
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+appDevPagePrefix+"api/term", header)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	config, _ := loadTeam("payments")
	if _, err := appSetLevel(&config, "U07DEV001", appLevelContributor); err != nil {
		t.Fatal(err)
	}
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			if strings.Contains(err.Error(), "timeout") {
				t.Fatal("the terminal stayed open")
			}
			return
		}
	}
}

// /hi in the channel: a contributor gets the app's links, for Report, and
// no dev page; someone in the channel with no level gets none.
func TestTeamContributorLink(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	channel := &fakeTeamChannel{}
	b.channel = channel
	os.MkdirAll(appDir("payments")+"/run", 0o700)
	saveTeamSlot("payments", appPreview{Slot: appSlotPreview, Label: "dev", URL: "https://team-payments-ab12.example.net/?hi_key=K1", Expose: "team-payments"})
	b.exposed = func(name string) (netExposeState, bool) { return netExposeState{}, name == "team-payments" }
	if line, err := b.webLink("U07ANNA01", false); err != nil || !strings.Contains(line, "an owner adds contributors on the dev page") {
		t.Fatal("someone in the channel with no level got a link:", line, err)
	}
	config, _ := loadTeam("payments")
	appSetLevel(&config, "U07ANNA01", appLevelContributor)
	if _, err := b.webLink("U07ANNA01", true); err == nil || !strings.Contains(err.Error(), "owners and developers") {
		t.Fatal("a contributor got the dev page:", err)
	}
	line, err := b.webLink("U07ANNA01", false)
	if err != nil || !strings.Contains(line, "DM, Anna") {
		t.Fatal(line, err)
	}
	dm := channel.calls[len(channel.calls)-1]
	if !strings.Contains(dm, "the helper (the button in the corner) has Report") || strings.Contains(dm, "Dev page") || !strings.Contains(dm, "hi_user=") {
		t.Fatal(dm)
	}
	if _, err := appSetLevel(&config, "U07ANNA01", appLevelNone); err != nil {
		t.Fatal(err)
	}
	if line, err := b.webLink("U07ANNA01", false); err != nil || !strings.Contains(line, "Private links are for") {
		t.Fatal(line, err)
	}
	// A developer's DM has the dev page.
	appSetLevel(&config, "U07ANNA01", appLevelDeveloper)
	if _, err := b.webLink("U07ANNA01", true); err != nil {
		t.Fatal(err)
	}
}

// A Vibe Dev has the dev page and the agents' Chat, not the terminal, VS
// Code, or an SSH key; moved down from Pro Dev, their open terminal
// closes like anyone's whose level drops.
func TestTeamVibeDev(t *testing.T) {
	b := appTestDevPageWithDeveloper(t)
	b.running = func(string) bool { return true }
	config, _ := loadTeam("payments")
	if text, err := appSetLevel(&config, "U07DEV001", "Vibe Dev"); err != nil || text != "U07DEV001 is a Vibe Dev of app payments. Their SSH key is out of the dev box, and their private links have ended." {
		t.Fatal(text, err)
	}
	if !appBuilds(config, "U07DEV001") || appHasShell(config, "U07DEV001") || appLevel(config, "U07DEV001") != appLevelVibe {
		t.Fatalf("%+v", config)
	}
	if err := appSetDevKey(config, "U07DEV001", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExample dev@laptop"); err == nil {
		t.Fatal("a Vibe Dev's key went into the dev box")
	}
	people := loadTeamPeople("payments")
	people.Keys = append(people.Keys, appPersonKey{Key: "vibe-key-0123456789abcdefgh", User: "U07DEV001", Name: "Dev", Ends: time.Now().Add(time.Hour)})
	saveTeamPeople("payments", people)
	handler := b.devPageHandler()
	call := func(method, path string) int {
		request := httptest.NewRequest(method, appDevPagePrefix+path, strings.NewReader("{"))
		request.Header.Set(netExposeUserHeader, "vibe-key-0123456789abcdefgh")
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		return recorder.Code
	}
	for _, shell := range [][2]string{{"GET", "api/term"}, {"POST", "api/term/files"}, {"POST", "api/sshkey"}, {"POST", "api/rollback"}} {
		if code := call(shell[0], shell[1]); code != http.StatusForbidden {
			t.Errorf("%v for a Vibe Dev: %d", shell, code)
		}
	}
	for _, chat := range [][2]string{{"GET", "api/state"}, {"GET", "api/chat"}, {"POST", "api/chat/send"}, {"POST", "api/start-new"}, {"GET", "api/tickets"}, {"POST", "api/release"}} {
		if code := call(chat[0], chat[1]); code == http.StatusForbidden || code == http.StatusUnauthorized {
			t.Errorf("%v for a Vibe Dev: %d", chat, code)
		}
	}
	if _, body := appTestDevPageCall(t, handler, "vibe-key-0123456789abcdefgh", "GET", "api/state", nil); !strings.Contains(body, `"level":"vibe"`) {
		t.Fatal(body)
	}
}

// No test asks Slack for a name.
func init() { appSlackUserName = func(string, string) string { return "" } }

// The listings say each member's name beside their ID: the one their
// link has, else Slack's; and a new owner is an owner only.
func TestTeamPeopleNames(t *testing.T) {
	setupTeamDevTest(t)
	saveTeamPeople("payments", appPeopleFile{Keys: []appPersonKey{{Key: "k", User: "U07OWNER1", Name: "Olga Owner", Ends: farFuture()}}})
	secrets, _ := loadTeamSecrets("payments")
	secrets.SlackBotToken = "xoxb-test"
	saveTeamSecrets("payments", secrets)
	previous := appSlackUserName
	defer func() { appSlackUserName = previous }()
	appSlackUserName = func(token, id string) string {
		if token == "xoxb-test" && id == "U07DEV1" {
			return "Mark Dev"
		}
		return ""
	}
	config, _ := loadTeam("payments")
	config.Developers = []string{"U07DEV1"}
	saveTeam(config)
	var out bytes.Buffer
	printTeamAccess(config, &out)
	if text := out.String(); !strings.Contains(text, "U07OWNER1    Olga Owner") || !strings.Contains(text, "U07DEV1      Mark Dev") {
		t.Fatal(text)
	}
	out.Reset()
	if err := appOwnersCommand([]string{"payments", "add", "U07DEV1"}, strings.NewReader(""), &out); err != nil || !strings.Contains(out.String(), "U07DEV1 (Mark Dev) is an owner") {
		t.Fatal(out.String(), err)
	}
	if config, _ := loadTeam("payments"); len(config.Developers) != 0 {
		t.Fatal("still a Pro Dev:", config.Developers)
	}
	out.Reset()
	appOwnersCommand([]string{"payments"}, strings.NewReader(""), &out)
	if !strings.Contains(out.String(), "Olga Owner") || !strings.Contains(out.String(), "Mark Dev") {
		t.Fatal(out.String())
	}
}

// Pro Devs and Vibe Devs release dev, logged with their name; production's
// data and going back stay the owners'.
func TestTeamDevPageDeveloperReleases(t *testing.T) {
	b := appTestDevPageWithDeveloper(t)
	handler := b.devPageHandler()
	call := func(path string, body any) (int, string) {
		return appTestDevPageCall(t, handler, appTestDeveloperKey, "POST", path, body)
	}
	os.WriteFile(b.repo+"/Makefile", []byte("run:\n\t./serve\n"), 0o644)
	appGit(b.repo, "add", "-A")
	appGit(b.repo, "commit", "-q", "-m", "make run")
	appTestMerge(t, b, "Suppliers")
	if code, body := call("api/release", map[string]any{"notes": "Suppliers", "preview_data": true}); code != http.StatusForbidden || !strings.Contains(body, "owners") {
		t.Fatal("a developer started production with Stage's data:", code, body)
	}
	if code, body := call("api/release", map[string]string{"notes": "Suppliers"}); code != 200 {
		t.Fatal(code, body)
	}
	// A Vibe Dev, the same.
	config, _ := loadTeam("payments")
	config.Developers, config.VibeDevelopers = nil, []string{"U07DEV001"}
	saveTeam(config)
	appTestMerge(t, b, "Search")
	if code, body := call("api/release", map[string]string{"notes": "Search"}); code != 200 {
		t.Fatal(code, body)
	}
	if code, _ := call("api/rollback", map[string]int{"version": 1}); code != http.StatusForbidden {
		t.Fatal("a developer rolled back")
	}
	versions, _ := loadTeamVersions("payments")
	last := versions.History[len(versions.History)-1]
	if versions.Production != 2 || last.Approval == nil || last.Approval.Name != "Dev on the dev page" {
		t.Fatalf("%+v", versions)
	}
}

// A Vibe Dev is on the People table even when Slack can't list the
// channel, as the other levels are.
func TestTeamDevPeopleHasVibeDevs(t *testing.T) {
	b := appTestDevPageWithDeveloper(t)
	b.channel = nil
	config, _ := loadTeam("payments")
	config.VibeDevelopers = []string{"U07VIBE01"}
	for _, person := range b.devPeopleView(config).People {
		if person.ID == "U07VIBE01" {
			return
		}
	}
	t.Fatal("the Vibe Dev isn't on the People table")
}
