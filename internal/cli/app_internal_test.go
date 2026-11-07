package cli

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
	old := appConfig{Name: "payments"}
	for _, address := range []string{appSlotProduction, appSlotPreview, appAddressDev} {
		if !appPublicOn(old, address) {
			t.Fatalf("a team from before isn't public on %s", address)
		}
	}
	// Turning one off keeps the others as a team from before has them.
	if err := appSetPublic(&old, appSlotPreview, false); err != nil || appPublicOn(old, appSlotPreview) || !appPublicOn(old, appSlotProduction) {
		t.Fatalf("%+v %v", old.Public, err)
	}
	fresh := appConfig{Name: "payments", Public: &appPublic{}}
	if appPublicOn(fresh, appSlotProduction) || appPublicOn(fresh, appAddressDev) {
		t.Fatal("a new team is public")
	}
	if err := appSetPublic(&fresh, "elsewhere", true); err == nil {
		t.Fatal("set an address that isn't one")
	}
	previous := appNetbirdDomain
	appNetbirdDomain = func() string { return "corp.example" }
	t.Cleanup(func() { appNetbirdDomain = previous })
	// One peer, named after the team, with an address per port; once
	// NetBird says its name, that one.
	os.MkdirAll(appDevDir("payments"), 0o700)
	if got := appInternalURL("payments", appSlotProduction) + " " + appInternalURL("payments", appSlotPreview) + " " + appInternalURL("payments", appAddressDev); got != "http://payments.corp.example/ http://payments.corp.example:8080/ http://payments.corp.example:8888/" {
		t.Fatal(got)
	}
	os.WriteFile(filepath.Join(appDevDir("payments"), "netbird-fqdn"), []byte("payments-dev.corp.example\n"), 0o600)
	if got := appInternalURL("payments", appSlotPreview); got != "http://payments-dev.corp.example:8080/" {
		t.Fatal(got)
	}
	if !appExposeKindOutdated(fresh, appSlotProduction, "team-payments-live-x1") || appExposeKindOutdated(fresh, appSlotProduction, "internal:production") {
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
	helper := &netExposeHelper{Routes: []netExposeRoute{{Prefix: appHelperPrefix, Socket: socket, Always: true}}, Plain: true}
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
	previous := appNetbirdDomain
	appNetbirdDomain = func() string { return "corp.example" }
	t.Cleanup(func() { appNetbirdDomain = previous })
	config, _ := loadTeam("payments")
	config.Public = &appPublic{}
	saveTeam(config)
	appTestRunnable(b)

	state, err := b.preview(appPreviewRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if state.URL != "http://payments.corp.example:8080/" || state.Expose != "internal:preview" || strings.Contains(strings.Join(b.calls, "\n"), "net expose") {
		t.Fatalf("%+v %v", state, b.calls)
	}
	// A peer that NetBird names otherwise moves the address with it.
	os.MkdirAll(appDevDir("payments"), 0o700)
	os.WriteFile(filepath.Join(appDevDir("payments"), "netbird-fqdn"), []byte("payments-dev.corp.example\n"), 0o600)
	if state, err = b.preview(appPreviewRequest{}); err != nil || state.URL != "http://payments-dev.corp.example:8080/" {
		t.Fatalf("%+v %v", state, err)
	}
	os.Remove(filepath.Join(appDevDir("payments"), "netbird-fqdn"))
	if err := b.devPagePublic(appSlotPreview, true, "Olga"); err != nil {
		t.Fatal(err)
	}
	state, _ = loadTeamSlot("payments", appSlotPreview)
	if appIsInternal(state.Expose) || !strings.HasPrefix(state.URL, "https://") {
		t.Fatalf("%+v", state)
	}
	public := state.Expose
	if err := b.devPagePublic(appSlotPreview, false, "Olga"); err != nil {
		t.Fatal(err)
	}
	state, _ = loadTeamSlot("payments", appSlotPreview)
	if state.Expose != "internal:preview" || !strings.Contains(strings.Join(b.calls, "\n"), "net expose stop "+public) {
		t.Fatalf("%+v %v", state, b.calls)
	}
	if config, _ := loadTeam("payments"); config.Public == nil || config.Public.Preview {
		t.Fatalf("%+v", config.Public)
	}
	// Made internal in app.json, not on the dev page: the keep loop moves
	// the preview there, as it does production and the dev page.
	if err := b.devPagePublic(appSlotPreview, true, "Olga"); err != nil {
		t.Fatal(err)
	}
	state, _ = loadTeamSlot("payments", appSlotPreview)
	public = state.Expose
	config, _ = loadTeam("payments")
	config.Public = &appPublic{}
	saveTeam(config)
	done := make(chan struct{})
	close(done)
	b.keepLinks(done, io.Discard)
	state, _ = loadTeamSlot("payments", appSlotPreview)
	if state.Expose != "internal:preview" || !strings.Contains(strings.Join(b.calls, "\n"), "net expose stop "+public) {
		t.Fatalf("%+v %v", state, b.calls)
	}
}

// The dev page that isn't public is on the dev box's peer.
func TestTeamInternalDevPage(t *testing.T) {
	b, _ := appTestDevPage(t)
	previous := appNetbirdDomain
	appNetbirdDomain = func() string { return "corp.example" }
	t.Cleanup(func() { appNetbirdDomain = previous })
	config, _ := loadTeam("payments")
	config.Public = &appPublic{}
	saveTeam(config)
	os.MkdirAll(filepath.Join(appDir("payments"), "run"), 0o700)
	if err := b.applyDevPage(true, io.Discard); err != nil {
		t.Fatal(err)
	}
	if state, ok := loadTeamDevPage("payments"); !ok || state.URL != "http://payments.corp.example:8888/" || !appIsInternal(state.Expose) {
		t.Fatalf("%+v", state)
	}
	os.MkdirAll(appDevDir("payments"), 0o700)
	os.WriteFile(filepath.Join(appDevDir("payments"), "netbird-fqdn"), []byte("payments-dev.corp.example\n"), 0o600)
	if err := b.applyDevPage(true, io.Discard); err != nil {
		t.Fatal(err)
	}
	if state, _ := loadTeamDevPage("payments"); state.URL != "http://payments-dev.corp.example:8888/" {
		t.Fatalf("%+v", state)
	}
	os.Remove(filepath.Join(appDevDir("payments"), "netbird-fqdn"))
	view := b.devPageView(appPersonKey{Name: "Olga"}, appLevelOwner)
	if len(view.Addresses) != 3 || view.Addresses[2].Internal != "http://payments.corp.example:8888/" || view.Addresses[0].Public {
		t.Fatalf("%+v", view.Addresses)
	}
}

// The team's one peer passes each address's port to its internal relay,
// and SSH to the dev box.
func TestTeamPeerScript(t *testing.T) {
	setupTeamDevTest(t)
	script := appPeerScript("payments", "10.0.0.3:2222", false)
	for _, want := range []string{"hi box __forward 127.0.0.1:80 unix:/hi-internal/production.sock", "hi box __forward 127.0.0.1:8080 unix:/hi-internal/preview.sock",
		"hi box __forward 127.0.0.1:8888 unix:/hi-internal/dev.sock", "hi box __forward 127.0.0.1:22 10.0.0.3:2222", "--hostname payments ", "--setup-key-file /run/netbird-key"} {
		if !strings.Contains(script, want) {
			t.Errorf("no %q in %s", want, script)
		}
	}
	// A peer that joined before 2.5.1 keeps its name.
	os.MkdirAll(filepath.Join(appDevDir("payments"), "netbird"), 0o700)
	os.WriteFile(filepath.Join(appDevDir("payments"), "netbird", "default.json"), []byte("{}"), 0o600)
	if appPeerHostname("payments") != "payments-dev" {
		t.Fatal(appPeerHostname("payments"))
	}
}
