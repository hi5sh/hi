package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A name for each address, as an IT team keeps them: from production's, or
// each given; refused when one is missing, used twice, or isn't a name.
func TestTeamParseNames(t *testing.T) {
	names, err := teamParseNames([]string{"Demo.Example.com."})
	if err != nil || names != (teamNames{Production: "demo.example.com", Preview: "stage.demo.example.com", Dev: "dev.demo.example.com"}) {
		t.Fatal(names, err)
	}
	names, err = teamParseNames([]string{"production=demo.example.com", "stage=staging.example.com", "dev=demo-dev.example.com"})
	if err != nil || names.Preview != "staging.example.com" || names.Dev != "demo-dev.example.com" {
		t.Fatal(names, err)
	}
	if names, err := teamParseNames([]string{"production=demo.example.com", "preview=staging.example.com", "dev=demo-dev.example.com"}); err != nil || names.Preview != "staging.example.com" {
		t.Fatal("preview= is stage=:", names, err)
	}
	for _, bad := range [][]string{
		{"production=demo.example.com", "stage=staging.example.com"},
		{"production=demo.example.com", "stage=demo.example.com", "dev=dev.example.com"},
		{"production=demo.example.com", "stage=not a name", "dev=dev.example.com"},
		{"prod=demo.example.com"},
	} {
		if _, err := teamParseNames(bad); err == nil {
			t.Errorf("took %v", bad)
		}
	}
}

// With names, the links are each name's, https when TLS ends in front;
// port 80 sends each name to its address, and anything else to
// production.
func TestTeamNamesAddresses(t *testing.T) {
	setupTeamTest(t)
	newTeamTestBroker(t)
	var out strings.Builder
	if err := teamAddressesCommand([]string{"payments", "names", "demo.example.com", "--https"}, &out); err != nil {
		t.Fatal(err)
	}
	config, _ := loadTeam("payments")
	if config.Addresses != teamAddressesNames || config.Names == nil || !config.Names.HTTPS {
		t.Fatalf("%+v", config)
	}
	for address, want := range map[string]string{teamSlotProduction: "https://demo.example.com/", teamSlotPreview: "https://stage.demo.example.com/", teamAddressDev: "https://dev.demo.example.com/"} {
		if got := teamInternalURL("payments", address); got != want {
			t.Errorf("%s: %s", address, got)
		}
	}
	if !strings.Contains(out.String(), "at demo.example.com, stage.demo.example.com, and dev.demo.example.com, names kept by hand, on https") ||
		!strings.Contains(out.String(), "Stage       https://stage.demo.example.com/   an A record to its NetBird address, or a proxy to") || !strings.Contains(out.String(), ":8080") {
		t.Fatal(out.String())
	}
	relays := map[string]http.Handler{}
	for _, address := range []string{teamSlotProduction, teamSlotPreview, teamAddressDev} {
		relays[address] = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, address) })
	}
	handler := teamNamesHandler(config, relays)
	for host, want := range map[string]string{"demo.example.com": "production", "STAGE.demo.example.com": "preview", "dev.demo.example.com:80": "dev", "100.92.14.7": "production"} {
		request := httptest.NewRequest("GET", "/", nil)
		request.Host = host
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Body.String() != want {
			t.Errorf("%s: %s", host, recorder.Body.String())
		}
	}
	// Back to one name: the names go, and port 80 is production's alone.
	if err := teamAddressesCommand([]string{"payments", "demo.example.com"}, io.Discard); err != nil {
		t.Fatal(err)
	}
	config, _ = loadTeam("payments")
	if config.Names != nil || teamInternalURL("payments", teamSlotPreview) != "http://demo.example.com:8080/" {
		t.Fatalf("%+v", config)
	}
	request := httptest.NewRequest("GET", "/", nil)
	request.Host = "stage.demo.example.com"
	recorder := httptest.NewRecorder()
	teamNamesHandler(config, relays).ServeHTTP(recorder, request)
	if recorder.Body.String() != "production" {
		t.Fatal(recorder.Body.String())
	}
}

// Stage is what people call the preview: hi team helper <team> stage off
// is the preview's helper.
func TestTeamStageIsThePreview(t *testing.T) {
	setupTeamTest(t)
	newTeamTestBroker(t)
	var out strings.Builder
	if code := runTeam([]string{"helper", "payments", "stage", "off"}, strings.NewReader(""), &out, &out); code != 0 {
		t.Fatal(out.String())
	}
	if config, _ := loadTeam("payments"); teamHelperOn(config, teamSlotPreview) || !teamHelperOn(config, teamSlotProduction) {
		t.Fatalf("%+v", config.Helper)
	}
	if teamAddressLabel(teamSlotPreview) != "Stage" {
		t.Fatal(teamAddressLabel(teamSlotPreview))
	}
}

// hi team limit <team> memory: the dev box's and the app's boxes' memory,
// hi's unless the team sets its own, given to running boxes at once, and
// never most of the machine.
func TestTeamBoxMemory(t *testing.T) {
	setupTeamTest(t)
	newTeamTestBroker(t)
	savedUpdate, savedMachine := teamUpdateMemory, teamMachineMemory
	t.Cleanup(func() { teamUpdateMemory, teamMachineMemory = savedUpdate, savedMachine })
	var updates []string
	teamUpdateMemory = func(team, box, size string) ([]string, error) {
		updates = append(updates, box+"="+size)
		return []string{"hi-box-team-" + team + "-" + box}, nil
	}
	teamMachineMemory = func() int64 { return 64 << 30 }
	config, _ := loadTeam("payments")
	if teamBoxMemory(config, teamBoxDev) != "16g" || teamBoxMemory(config, teamBoxApp) != "4g" {
		t.Fatal("hi's limits")
	}
	var out strings.Builder
	if err := teamLimitCommand([]string{"payments", "memory", "dev", "24g"}, &out); err != nil {
		t.Fatal(err)
	}
	config, _ = loadTeam("payments")
	if teamBoxMemory(config, teamBoxDev) != "24g" || strings.Join(updates, " ") != "dev=24g" || !strings.Contains(out.String(), "Running now with it, without a restart: hi-box-team-payments-dev") ||
		!strings.Contains(out.String(), "dev box may use 24g") {
		t.Fatal(out.String(), updates)
	}
	for _, bad := range [][]string{{"memory", "dev", "512m"}, {"memory", "dev", "60g"}, {"memory", "lead", "8g"}, {"memory", "dev", "lots"}} {
		if err := teamLimitCommand(append([]string{"payments"}, bad...), io.Discard); err == nil {
			t.Errorf("took %v", bad)
		}
	}
	if err := teamLimitCommand([]string{"payments", "memory", "dev", "default"}, io.Discard); err != nil {
		t.Fatal(err)
	}
	if config, _ = loadTeam("payments"); teamBoxMemory(config, teamBoxDev) != "16g" || updates[len(updates)-1] != "dev=16g" {
		t.Fatal(config.BoxMemory, updates)
	}
}
