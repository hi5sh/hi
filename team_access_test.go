package main

import (
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

const teamTestDeveloperKey = "developer-key-0123456789ab"

// teamTestDevPageWithDeveloper is the dev page's test team, with Dev a
// developer signed in.
func teamTestDevPageWithDeveloper(t *testing.T) *teamTestBroker {
	t.Helper()
	b, _ := teamTestDevPage(t)
	config, _ := loadTeam("payments")
	config.Developers = []string{"U07DEV001"}
	saveTeam(config)
	people := loadTeamPeople("payments")
	people.Keys = append(people.Keys, teamPersonKey{Key: teamTestDeveloperKey, User: "U07DEV001", Name: "Dev", Ends: time.Now().Add(time.Hour)})
	saveTeamPeople("payments", people)
	return b
}

// Levels: owners are the admin's, developers and contributors the
// owners', and being in the channel gives nobody one.
func TestTeamAccessLevels(t *testing.T) {
	setupTeamTest(t)
	os.MkdirAll(teamDir("payments"), 0o700)
	saveTeam(testTeamConfig())
	config, _ := loadTeam("payments")
	owner := config.Owners[0]
	if teamLevel(config, owner) != teamLevelOwner || teamLevel(config, "U07CHAN01") != teamLevelNone {
		t.Fatal("levels")
	}
	// A contributor, by an owner's choice.
	if text, err := teamSetLevel(&config, "U07CHAN01", teamLevelContributor); err != nil || text != "U07CHAN01 is a contributor of team payments." {
		t.Fatal(text, err)
	}
	if _, err := teamSetLevel(&config, owner, teamLevelDeveloper); err == nil || !strings.Contains(err.Error(), "hi team owners") {
		t.Fatal("an owner made a developer:", err)
	}
	if _, err := teamSetLevel(&config, "U07DEV001", "admin"); err == nil {
		t.Fatal("took a level that isn't one")
	}
	// A developer, with a key and a link.
	text, err := teamSetLevel(&config, "U07DEV001", teamLevelDeveloper)
	if err != nil || text != "U07DEV001 is a Pro Dev of team payments." {
		t.Fatal(text, err)
	}
	saved, _ := loadTeam("payments")
	if !teamBuilds(saved, "U07DEV001") || teamLevel(saved, "U07DEV001") != teamLevelDeveloper || teamLevel(saved, "U07CHAN01") != teamLevelContributor {
		t.Fatalf("%+v", saved)
	}
	if err := teamSetDevKey(saved, "U07DEV001", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExample dev@laptop"); err != nil {
		t.Fatal(err)
	}
	if keys, _ := os.ReadFile(teamDevDir("payments") + "/ssh/authorized_keys"); !strings.Contains(string(keys), "dev@laptop") {
		t.Fatalf("the box doesn't take the developer's key: %q", keys)
	}
	if err := teamSetDevKey(saved, "U07CHAN01", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExample chan@laptop"); err == nil {
		t.Fatal("a contributor's key went into the dev box")
	}
	saveTeamPeople("payments", teamPeopleFile{Keys: []teamPersonKey{{Key: teamTestDeveloperKey, User: "U07DEV001", Name: "Dev", Ends: time.Now().Add(time.Hour)}}})
	// Back to a contributor: the key is out, the link ends.
	text, err = teamSetLevel(&saved, "U07DEV001", teamLevelContributor)
	if err != nil || !strings.Contains(text, "U07DEV001 is a contributor of team payments. Their SSH key is out of the dev box, and their private links have ended.") {
		t.Fatal(text, err)
	}
	if keys, _ := os.ReadFile(teamDevDir("payments") + "/ssh/authorized_keys"); strings.Contains(string(keys), "dev@laptop") {
		t.Fatal("the key stayed")
	}
	if people := loadTeamPeople("payments"); time.Now().Before(people.Keys[0].Ends) {
		t.Fatal("the link still works")
	}
	// No level any more.
	if text, err := teamSetLevel(&saved, "U07CHAN01", teamLevelNone); err != nil || !strings.Contains(text, "has no level in team payments") {
		t.Fatal(text, err)
	}
	saved, _ = loadTeam("payments")
	if teamLevel(saved, "U07CHAN01") != teamLevelNone || slices.Contains(saved.Contributors, "U07CHAN01") {
		t.Fatalf("%+v", saved)
	}
	// hi team access lists who is which.
	var out strings.Builder
	if err := teamAccessCommand([]string{"payments", "U07DEV001", "developer"}, &out); err != nil || !strings.Contains(out.String(), "a developer can read it through a preview") {
		t.Fatal(out.String(), err)
	}
	out.Reset()
	teamAccessCommand([]string{"payments"}, &out)
	if !strings.Contains(out.String(), "Pro Devs, with Chat, terminal, VS Code, and SSH") || !strings.Contains(out.String(), "U07DEV001") || !strings.Contains(out.String(), "Anyone else, in the channel or not, has no level.") {
		t.Fatal(out.String())
	}
}

// Every route of the dev page, found in hi's source, goes through the
// gate: without a builder's key, 401; for a developer, 403 unless the
// route is a developer's; an owner gets past it everywhere.
func TestTeamDevPageRoutesByLevel(t *testing.T) {
	source, _ := os.Getwd() // the tests' setup moves to a folder of its own
	b := teamTestDevPageWithDeveloper(t)
	b.running = func(string) bool { return true }
	handler := b.devPageHandler()
	pattern := regexp.MustCompile(`"([A-Z]+) "\+teamDevPagePrefix\+"(api/[a-z/-]+)"`)
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
	routes = append(routes, [2]string{"GET", "code/"})
	call := func(key, method, path string) int {
		request := httptest.NewRequest(method, teamDevPagePrefix+path, strings.NewReader("{"))
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
		ours := teamDevDeveloperRoutes[method+" "+teamDevPagePrefix+path]
		if ours {
			developerRoutes++
		}
		if code := call("", method, path); code != http.StatusUnauthorized {
			t.Errorf("%s %s with no key: %d", method, path, code)
		}
		if code := call(teamTestAskerKey, method, path); code != http.StatusUnauthorized {
			t.Errorf("%s %s for someone in the channel: %d", method, path, code)
		}
		code := call(teamTestDeveloperKey, method, path)
		if ours && (code == http.StatusUnauthorized || code == http.StatusForbidden) || !ours && code != http.StatusForbidden {
			t.Errorf("%s %s for a developer: %d", method, path, code)
		}
		if code := call(teamTestOwnerKey, method, path); code == http.StatusUnauthorized || code == http.StatusForbidden {
			t.Errorf("%s %s for an owner: %d", method, path, code)
		}
	}
	// Releases, data, settings, keys, sites, addresses, and People are
	// the owners'; the rest is a developer's.
	for _, owners := range []string{"POST api/release", "POST api/rollback", "POST api/dev/reset", "GET api/data", "POST api/data/copy", "POST api/settings", "POST api/site", "POST api/public", "POST api/helper", "POST api/incident", "POST api/rule", "GET api/people", "POST api/people"} {
		method, path, _ := strings.Cut(owners, " ")
		if teamDevDeveloperRoutes[method+" "+teamDevPagePrefix+path] {
			t.Errorf("%s is a developer's", owners)
		}
	}
	if developerRoutes < 20 {
		t.Fatalf("only %d of the routes are a developer's", developerRoutes)
	}
}

// A developer's page: their level, no keys, their own SSH key; the
// owners' People section changes levels and the channel hears it.
func TestTeamDevPagePeople(t *testing.T) {
	b := teamTestDevPageWithDeveloper(t)
	b.running = func(string) bool { return true }
	channel := &fakeTeamChannel{}
	b.channel = channel
	saveTeamSecrets("payments", teamSecrets{})
	handler := b.devPageHandler()
	code, body := teamTestDevPageCall(t, handler, teamTestDeveloperKey, "GET", "api/state", nil)
	if code != 200 || !strings.Contains(body, `"level":"developer"`) || strings.Contains(body, `"keys"`) {
		t.Fatalf("%d %s", code, body)
	}
	if code, body := teamTestDevPageCall(t, handler, teamTestOwnerKey, "GET", "api/state", nil); code != 200 || !strings.Contains(body, `"level":"owner"`) || !strings.Contains(body, `"keys"`) {
		t.Fatalf("%d %s", code, body)
	}
	// Their own key, from the page.
	if code, body := teamTestDevPageCall(t, handler, teamTestDeveloperKey, "POST", "api/sshkey", map[string]string{"key": "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExample dev@laptop"}); code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	if _, body := teamTestDevPageCall(t, handler, teamTestDeveloperKey, "GET", "api/state", nil); !strings.Contains(body, `"ssh_key":"ssh-ed25519 dev@laptop"`) {
		t.Fatal(body)
	}
	// The owners' People section: the channel's people are contributors.
	code, body = teamTestDevPageCall(t, handler, teamTestOwnerKey, "GET", "api/people", nil)
	// Owners first, then developers, then the rest of the channel, who
	// have no level until an owner gives one.
	var view teamDevPeopleView
	json.Unmarshal([]byte(body), &view)
	var order []string
	for _, person := range view.People {
		order = append(order, person.ID+":"+person.Level)
	}
	if code != 200 || strings.Join(order, " ") != "U07OWNER1:owner U07DEV001:developer U07ANNA01:none U1:none U2:none" || !view.PreviewData {
		t.Fatalf("%d %s", code, body)
	}
	if code, body := teamTestDevPageCall(t, handler, teamTestOwnerKey, "POST", "api/people", map[string]string{"user": "U07ANNA01", "level": "developer"}); code != 200 || !strings.Contains(body, "U07ANNA01 is a Pro Dev") {
		t.Fatalf("%d %s", code, body)
	}
	teamTestDevPageCall(t, handler, teamTestOwnerKey, "POST", "api/people", map[string]string{"user": "U07ANNA01", "level": "contributor"})
	teamTestDevPageCall(t, handler, teamTestOwnerKey, "POST", "api/people", map[string]string{"user": "U07ANNA01", "level": "none"})
	calls := strings.Join(channel.calls, "\n")
	if !strings.Contains(calls, "made <@U07ANNA01> a Pro Dev.") || !strings.Contains(calls, "made <@U07ANNA01> a contributor.") || !strings.Contains(calls, "took away <@U07ANNA01>'s level") {
		t.Fatalf("%q", channel.calls)
	}
	if code, _ := teamTestDevPageCall(t, handler, teamTestDeveloperKey, "POST", "api/people", map[string]string{"user": "U2", "level": "developer"}); code != http.StatusForbidden {
		t.Fatal("a developer made a developer:", code)
	}
	// The developer is no longer one: their next click is refused.
	teamTestDevPageCall(t, handler, teamTestOwnerKey, "POST", "api/people", map[string]string{"user": "U07DEV001", "level": "contributor"})
	if code, _ := teamTestDevPageCall(t, handler, teamTestDeveloperKey, "GET", "api/state", nil); code != http.StatusUnauthorized {
		t.Fatal("a former developer got the page:", code)
	}
}

// A developer's open terminal closes when they stop being one, without
// waiting for their next click.
func TestTeamDevPageTerminalEndsWithAccess(t *testing.T) {
	b := teamTestDevPageWithDeveloper(t)
	previousCommand, previousCheck := teamDevTermCommand, teamDevAccessCheck
	teamDevTermCommand = func(string) []string { return []string{"sh", "-c", "sleep 30"} }
	teamDevAccessCheck = 50 * time.Millisecond
	t.Cleanup(func() { teamDevTermCommand, teamDevAccessCheck = previousCommand, previousCheck })
	server := httptest.NewServer(b.devPageHandler())
	t.Cleanup(server.Close)
	state := `{"url":"https://team-payments-dev.example.net/?hi_key=k","expose":"team-payments-dev"}`
	os.MkdirAll(teamDir("payments")+"/run", 0o700)
	os.WriteFile(teamDevPageStatePath("payments"), []byte(state), 0o600)
	header := http.Header{netExposeUserHeader: {teamTestDeveloperKey}, "Origin": {"https://team-payments-dev.example.net"}}
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+teamDevPagePrefix+"api/term", header)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	config, _ := loadTeam("payments")
	if _, err := teamSetLevel(&config, "U07DEV001", teamLevelContributor); err != nil {
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
	os.MkdirAll(teamDir("payments")+"/run", 0o700)
	saveTeamSlot("payments", teamPreview{Slot: teamSlotPreview, Label: "dev", URL: "https://team-payments-ab12.example.net/?hi_key=K1", Expose: "team-payments"})
	b.exposed = func(name string) (netExposeState, bool) { return netExposeState{}, name == "team-payments" }
	if line, err := b.webLink("U07ANNA01", false); err != nil || !strings.Contains(line, "an owner adds contributors on the dev page") {
		t.Fatal("someone in the channel with no level got a link:", line, err)
	}
	config, _ := loadTeam("payments")
	teamSetLevel(&config, "U07ANNA01", teamLevelContributor)
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
	if _, err := teamSetLevel(&config, "U07ANNA01", teamLevelNone); err != nil {
		t.Fatal(err)
	}
	if line, err := b.webLink("U07ANNA01", false); err != nil || !strings.Contains(line, "Private links are for") {
		t.Fatal(line, err)
	}
	// A developer's DM has the dev page.
	teamSetLevel(&config, "U07ANNA01", teamLevelDeveloper)
	if _, err := b.webLink("U07ANNA01", true); err != nil {
		t.Fatal(err)
	}
}

// A Vibe Dev has the dev page and the agents' Chat, not the terminal, VS
// Code, or an SSH key; moved down from Pro Dev, their open terminal
// closes like anyone's whose level drops.
func TestTeamVibeDev(t *testing.T) {
	b := teamTestDevPageWithDeveloper(t)
	b.running = func(string) bool { return true }
	config, _ := loadTeam("payments")
	if text, err := teamSetLevel(&config, "U07DEV001", "Vibe Dev"); err != nil || text != "U07DEV001 is a Vibe Dev of team payments. Their SSH key is out of the dev box, and their private links have ended." {
		t.Fatal(text, err)
	}
	if !teamBuilds(config, "U07DEV001") || teamHasShell(config, "U07DEV001") || teamLevel(config, "U07DEV001") != teamLevelVibe {
		t.Fatalf("%+v", config)
	}
	if err := teamSetDevKey(config, "U07DEV001", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExample dev@laptop"); err == nil {
		t.Fatal("a Vibe Dev's key went into the dev box")
	}
	people := loadTeamPeople("payments")
	people.Keys = append(people.Keys, teamPersonKey{Key: "vibe-key-0123456789abcdefgh", User: "U07DEV001", Name: "Dev", Ends: time.Now().Add(time.Hour)})
	saveTeamPeople("payments", people)
	handler := b.devPageHandler()
	call := func(method, path string) int {
		request := httptest.NewRequest(method, teamDevPagePrefix+path, strings.NewReader("{"))
		request.Header.Set(netExposeUserHeader, "vibe-key-0123456789abcdefgh")
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		return recorder.Code
	}
	for _, shell := range [][2]string{{"GET", "api/term"}, {"POST", "api/term/files"}, {"POST", "api/code/start"}, {"GET", "code/"}, {"POST", "api/sshkey"}, {"POST", "api/release"}} {
		if code := call(shell[0], shell[1]); code != http.StatusForbidden {
			t.Errorf("%v for a Vibe Dev: %d", shell, code)
		}
	}
	for _, chat := range [][2]string{{"GET", "api/state"}, {"GET", "api/chat"}, {"POST", "api/chat/send"}, {"POST", "api/start-new"}, {"GET", "api/tickets"}} {
		if code := call(chat[0], chat[1]); code == http.StatusForbidden || code == http.StatusUnauthorized {
			t.Errorf("%v for a Vibe Dev: %d", chat, code)
		}
	}
	if _, body := teamTestDevPageCall(t, handler, "vibe-key-0123456789abcdefgh", "GET", "api/state", nil); !strings.Contains(body, `"level":"vibe"`) {
		t.Fatal(body)
	}
}
