package main

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTeamPublicSettings(t *testing.T) {
	setupTeamTest(t)
	old := teamConfig{Name: "payments"}
	for _, address := range []string{teamSlotProduction, teamSlotPreview, teamAddressDev} {
		if !teamPublicOn(old, address) {
			t.Fatalf("a team from before isn't public on %s", address)
		}
	}
	// Turning one off keeps the others as a team from before has them.
	if err := teamSetPublic(&old, teamSlotPreview, false); err != nil || teamPublicOn(old, teamSlotPreview) || !teamPublicOn(old, teamSlotProduction) {
		t.Fatalf("%+v %v", old.Public, err)
	}
	fresh := teamConfig{Name: "payments", Public: &teamPublic{}}
	if teamPublicOn(fresh, teamSlotProduction) || teamPublicOn(fresh, teamAddressDev) {
		t.Fatal("a new team is public")
	}
	if err := teamSetPublic(&fresh, "elsewhere", true); err == nil {
		t.Fatal("set an address that isn't one")
	}
	previous := teamNetbirdDomain
	teamNetbirdDomain = func() string { return "corp.example" }
	t.Cleanup(func() { teamNetbirdDomain = previous })
	// One peer, named after the team, with an address per port; once
	// NetBird says its name, that one.
	os.MkdirAll(teamDevDir("payments"), 0o700)
	if got := teamInternalURL("payments", teamSlotProduction) + " " + teamInternalURL("payments", teamSlotPreview) + " " + teamInternalURL("payments", teamAddressDev); got != "http://payments.corp.example/ http://payments.corp.example:8080/ http://payments.corp.example:8888/" {
		t.Fatal(got)
	}
	os.WriteFile(filepath.Join(teamDevDir("payments"), "netbird-fqdn"), []byte("payments-dev.corp.example\n"), 0o600)
	if got := teamInternalURL("payments", teamSlotPreview); got != "http://payments-dev.corp.example:8080/" {
		t.Fatal(got)
	}
	if !teamExposeKindOutdated(fresh, teamSlotProduction, "team-payments-live-x1") || teamExposeKindOutdated(fresh, teamSlotProduction, "internal:production") {
		t.Fatal("the kind of address")
	}
}

// The internal relay passes the app on with the helper's routes, and its
// cookies work on plain HTTP.
func TestTeamInternalRelay(t *testing.T) {
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "app %s plain=%q", r.URL.Path, r.Header.Get(netExposePlainHeader))
	}))
	defer app.Close()
	socket := filepath.Join(t.TempDir(), "helper.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	helperServer := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "hi %s user=%q plain=%q host=%q", r.URL.Path, r.Header.Get(netExposeUserHeader), r.Header.Get(netExposePlainHeader), r.Header.Get("X-Forwarded-Host"))
	})}
	go helperServer.Serve(listener)
	defer helperServer.Close()
	helper := &netExposeHelper{Routes: []netExposeRoute{{Prefix: teamHelperPrefix, Socket: socket, Always: true}}, Plain: true}
	relay := httptest.NewServer(newNetExposeRelayTo(func() string { return app.URL }, "", time.Time{}, io.Discard, helper, nil))
	defer relay.Close()

	key := "person-key-0123456789abcdef"
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Get(relay.URL + "/__hi/dev/?hi_user=" + key)
	if err != nil {
		t.Fatal(err)
	}
	cookie := response.Header.Get("Set-Cookie")
	if response.StatusCode != http.StatusSeeOther || !strings.Contains(cookie, "hi_user="+key) || strings.Contains(cookie, "Secure") {
		t.Fatalf("%d %q", response.StatusCode, cookie)
	}
	request, _ := http.NewRequest("GET", relay.URL+"/__hi/api/status", nil)
	request.Header.Set("Cookie", "hi_user="+key)
	request.Header.Set(netExposePlainHeader, "forged")
	response, _ = http.DefaultClient.Do(request)
	body, _ := io.ReadAll(response.Body)
	if !strings.Contains(string(body), `user="`+key+`"`) || !strings.Contains(string(body), `plain="1"`) || !strings.Contains(string(body), "host=") {
		t.Fatal(string(body))
	}
	response, _ = http.Get(relay.URL + "/orders")
	body, _ = io.ReadAll(response.Body)
	if string(body) != `app /orders plain=""` {
		t.Fatal(string(body))
	}
}

// A preview that isn't public is on its internal address; made public it
// gets an exposure, and internal again it loses it.
func TestTeamInternalAddresses(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	previous := teamNetbirdDomain
	teamNetbirdDomain = func() string { return "corp.example" }
	t.Cleanup(func() { teamNetbirdDomain = previous })
	config, _ := loadTeam("payments")
	config.Public = &teamPublic{}
	saveTeam(config)
	teamTestRunnable(b)

	state, err := b.preview(teamPreviewRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if state.URL != "http://payments.corp.example:8080/" || state.Expose != "internal:preview" || strings.Contains(strings.Join(b.calls, "\n"), "net expose") {
		t.Fatalf("%+v %v", state, b.calls)
	}
	// A peer that NetBird names otherwise moves the address with it.
	os.MkdirAll(teamDevDir("payments"), 0o700)
	os.WriteFile(filepath.Join(teamDevDir("payments"), "netbird-fqdn"), []byte("payments-dev.corp.example\n"), 0o600)
	if state, err = b.preview(teamPreviewRequest{}); err != nil || state.URL != "http://payments-dev.corp.example:8080/" {
		t.Fatalf("%+v %v", state, err)
	}
	os.Remove(filepath.Join(teamDevDir("payments"), "netbird-fqdn"))
	if err := b.devPagePublic(teamSlotPreview, true, "Olga"); err != nil {
		t.Fatal(err)
	}
	state, _ = loadTeamSlot("payments", teamSlotPreview)
	if teamIsInternal(state.Expose) || !strings.HasPrefix(state.URL, "https://") {
		t.Fatalf("%+v", state)
	}
	public := state.Expose
	if err := b.devPagePublic(teamSlotPreview, false, "Olga"); err != nil {
		t.Fatal(err)
	}
	state, _ = loadTeamSlot("payments", teamSlotPreview)
	if state.Expose != "internal:preview" || !strings.Contains(strings.Join(b.calls, "\n"), "net expose stop "+public) {
		t.Fatalf("%+v %v", state, b.calls)
	}
	if config, _ := loadTeam("payments"); config.Public == nil || config.Public.Preview {
		t.Fatalf("%+v", config.Public)
	}
}

// The dev page that isn't public is on the dev box's peer.
func TestTeamInternalDevPage(t *testing.T) {
	b, _ := teamTestDevPage(t)
	previous := teamNetbirdDomain
	teamNetbirdDomain = func() string { return "corp.example" }
	t.Cleanup(func() { teamNetbirdDomain = previous })
	config, _ := loadTeam("payments")
	config.Public = &teamPublic{}
	saveTeam(config)
	os.MkdirAll(filepath.Join(teamDir("payments"), "run"), 0o700)
	if err := b.applyDevPage(true, io.Discard); err != nil {
		t.Fatal(err)
	}
	if state, ok := loadTeamDevPage("payments"); !ok || state.URL != "http://payments.corp.example:8888/" || !teamIsInternal(state.Expose) {
		t.Fatalf("%+v", state)
	}
	os.MkdirAll(teamDevDir("payments"), 0o700)
	os.WriteFile(filepath.Join(teamDevDir("payments"), "netbird-fqdn"), []byte("payments-dev.corp.example\n"), 0o600)
	if err := b.applyDevPage(true, io.Discard); err != nil {
		t.Fatal(err)
	}
	if state, _ := loadTeamDevPage("payments"); state.URL != "http://payments-dev.corp.example:8888/" {
		t.Fatalf("%+v", state)
	}
	os.Remove(filepath.Join(teamDevDir("payments"), "netbird-fqdn"))
	view := b.devPageView(teamPersonKey{Name: "Olga"}, teamLevelOwner)
	if len(view.Addresses) != 3 || view.Addresses[2].Internal != "http://payments.corp.example:8888/" || view.Addresses[0].Public {
		t.Fatalf("%+v", view.Addresses)
	}
}

// The team's one peer passes each address's port to its internal relay,
// and SSH to the dev box.
func TestTeamPeerScript(t *testing.T) {
	setupTeamDevTest(t)
	script := teamPeerScript("payments", "10.0.0.3:2222", false)
	for _, want := range []string{"hi box __forward 127.0.0.1:80 unix:/hi-internal/production.sock", "hi box __forward 127.0.0.1:8080 unix:/hi-internal/preview.sock",
		"hi box __forward 127.0.0.1:8888 unix:/hi-internal/dev.sock", "hi box __forward 127.0.0.1:22 10.0.0.3:2222", "--hostname payments ", "--setup-key-file /run/netbird-key"} {
		if !strings.Contains(script, want) {
			t.Errorf("no %q in %s", want, script)
		}
	}
	// A peer that joined before 2.5.1 keeps its name.
	os.MkdirAll(filepath.Join(teamDevDir("payments"), "netbird"), 0o700)
	os.WriteFile(filepath.Join(teamDevDir("payments"), "netbird", "default.json"), []byte("{}"), 0o600)
	if teamPeerHostname("payments") != "payments-dev" {
		t.Fatal(teamPeerHostname("payments"))
	}
}
