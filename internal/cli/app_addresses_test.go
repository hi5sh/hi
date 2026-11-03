package cli

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
	names, err := appParseNames([]string{"Demo.Example.com."})
	if err != nil || names != (appNames{Production: "demo.example.com", Preview: "stage.demo.example.com", Dev: "dev.demo.example.com"}) {
		t.Fatal(names, err)
	}
	names, err = appParseNames([]string{"production=demo.example.com", "stage=staging.example.com", "dev=demo-dev.example.com"})
	if err != nil || names.Preview != "staging.example.com" || names.Dev != "demo-dev.example.com" {
		t.Fatal(names, err)
	}
	if names, err := appParseNames([]string{"production=demo.example.com", "preview=staging.example.com", "dev=demo-dev.example.com"}); err != nil || names.Preview != "staging.example.com" {
		t.Fatal("preview= is stage=:", names, err)
	}
	for _, bad := range [][]string{
		{"production=demo.example.com", "stage=staging.example.com"},
		{"production=demo.example.com", "stage=demo.example.com", "dev=dev.example.com"},
		{"production=demo.example.com", "stage=not a name", "dev=dev.example.com"},
		{"prod=demo.example.com"},
	} {
		if _, err := appParseNames(bad); err == nil {
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
	if err := appAddressesCommand([]string{"payments", "names", "demo.example.com", "--https"}, &out); err != nil {
		t.Fatal(err)
	}
	config, _ := loadTeam("payments")
	if config.Addresses != appAddressesNames || config.Names == nil || !config.Names.HTTPS {
		t.Fatalf("%+v", config)
	}
	for address, want := range map[string]string{appSlotProduction: "https://demo.example.com/", appSlotPreview: "https://stage.demo.example.com/", appAddressDev: "https://dev.demo.example.com/"} {
		if got := appInternalURL("payments", address); got != want {
			t.Errorf("%s: %s", address, got)
		}
	}
	if !strings.Contains(out.String(), "at demo.example.com, stage.demo.example.com, and dev.demo.example.com, names kept by hand, on https") ||
		!strings.Contains(out.String(), "Stage       https://stage.demo.example.com/   an A record to its NetBird address, or a proxy to") || !strings.Contains(out.String(), ":8080") {
		t.Fatal(out.String())
	}
	relays := map[string]http.Handler{}
	for _, address := range []string{appSlotProduction, appSlotPreview, appAddressDev} {
		relays[address] = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, address) })
	}
	handler := appNamesHandler(config, relays)
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
	if err := appAddressesCommand([]string{"payments", "demo.example.com"}, io.Discard); err != nil {
		t.Fatal(err)
	}
	config, _ = loadTeam("payments")
	if config.Names != nil || appInternalURL("payments", appSlotPreview) != "http://demo.example.com:8080/" {
		t.Fatalf("%+v", config)
	}
	request := httptest.NewRequest("GET", "/", nil)
	request.Host = "stage.demo.example.com"
	recorder := httptest.NewRecorder()
	appNamesHandler(config, relays).ServeHTTP(recorder, request)
	if recorder.Body.String() != "production" {
		t.Fatal(recorder.Body.String())
	}
}

// Stage is what people call the preview: hi app helper <team> stage off
// is the preview's helper.
func TestTeamStageIsThePreview(t *testing.T) {
	setupTeamTest(t)
	newTeamTestBroker(t)
	var out strings.Builder
	if code := runTeam([]string{"helper", "payments", "stage", "off"}, strings.NewReader(""), &out, &out); code != 0 {
		t.Fatal(out.String())
	}
	if config, _ := loadTeam("payments"); appHelperOn(config, appSlotPreview) || !appHelperOn(config, appSlotProduction) {
		t.Fatalf("%+v", config.Helper)
	}
	if appAddressLabel(appSlotPreview) != "Stage" {
		t.Fatal(appAddressLabel(appSlotPreview))
	}
}

// hi app limit <team> memory: the dev box's and the app's boxes' memory,
// hi's unless the team sets its own, given to running boxes at once, and
// never most of the machine.
func TestTeamBoxMemory(t *testing.T) {
	setupTeamTest(t)
	newTeamTestBroker(t)
	savedUpdate, savedMachine := appUpdateMemory, appMachineMemory
	t.Cleanup(func() { appUpdateMemory, appMachineMemory = savedUpdate, savedMachine })
	var updates []string
	appUpdateMemory = func(team, box, size string) ([]string, error) {
		updates = append(updates, box+"="+size)
		return []string{"hi-box-app-" + team + "-" + box}, nil
	}
	appMachineMemory = func() int64 { return 64 << 30 }
	config, _ := loadTeam("payments")
	if appBoxMemory(config, appBoxDev) != "16g" || appBoxMemory(config, appBoxApp) != "4g" {
		t.Fatal("hi's limits")
	}
	var out strings.Builder
	if err := appLimitCommand([]string{"payments", "memory", "dev", "24g"}, &out); err != nil {
		t.Fatal(err)
	}
	config, _ = loadTeam("payments")
	if appBoxMemory(config, appBoxDev) != "24g" || strings.Join(updates, " ") != "dev=24g" || !strings.Contains(out.String(), "Running now with it, without a restart: hi-box-app-payments-dev") ||
		!strings.Contains(out.String(), "dev box may use 24g") {
		t.Fatal(out.String(), updates)
	}
	for _, bad := range [][]string{{"memory", "dev", "512m"}, {"memory", "dev", "60g"}, {"memory", "lead", "8g"}, {"memory", "dev", "lots"}} {
		if err := appLimitCommand(append([]string{"payments"}, bad...), io.Discard); err == nil {
			t.Errorf("took %v", bad)
		}
	}
	if err := appLimitCommand([]string{"payments", "memory", "dev", "default"}, io.Discard); err != nil {
		t.Fatal(err)
	}
	if config, _ = loadTeam("payments"); appBoxMemory(config, appBoxDev) != "16g" || updates[len(updates)-1] != "dev=16g" {
		t.Fatal(config.BoxMemory, updates)
	}
}
