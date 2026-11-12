package cli

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// appTestViewLogs gives each box's log, and says what was asked.
func appTestViewLogs(t *testing.T, logs map[string]string) *[]string {
	t.Helper()
	var asked []string
	saved := appDevViewLogs
	appDevViewLogs = func(container string, lines int, since string) (string, error) {
		asked = append(asked, container+" "+since)
		return logs[container], nil
	}
	t.Cleanup(func() { appDevViewLogs = saved })
	return &asked
}

// The app's output, its settings' values masked, then filtered.
func TestDevViewLogs(t *testing.T) {
	b, _ := appTestDevPage(t)
	os.MkdirAll(filepath.Join(appDevDir("payments"), "net"), 0o700)
	os.WriteFile(filepath.Join(appDevDir("payments"), "net", "network.log"), nil, 0o600)
	saveTeamSecrets("payments", appSecrets{
		Env:           map[string]string{"API_TOKEN": "sk-live-1234567890", "MODE": "fast"},
		EnvProduction: map[string]string{"DB_URL": "postgres://app:hunter2hunter2@db/app"},
		Files:         map[string]string{"KEY_PEM": base64.StdEncoding.EncodeToString([]byte("-----BEGIN KEY-----\nMIIEvQIBADANBgkqhkiG9w0BAQEFAASC\n-----END KEY-----\n"))},
	})
	os.MkdirAll(filepath.Join(appDir("payments"), "run"), 0o700)
	if err := saveTeamSlot("payments", appPreview{Slot: appSlotProduction, Container: "hi-box-app-payments-app-v11"}); err != nil {
		t.Fatal(err)
	}
	asked := appTestViewLogs(t, map[string]string{"hi-box-app-payments-app-v11": "start in fast mode\ncalling with sk-live-1234567890\nconnected to postgres://app:hunter2hunter2@db/app\nkey MIIEvQIBADANBgkqhkiG9w0BAQEFAASC\njob competition-research: 3 of 40\n"})

	text, err := b.devViewLogs(appDevViewRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(text, "sk-live") || strings.Contains(text, "hunter2") || strings.Contains(text, "MIIEv") ||
		!strings.Contains(text, "calling with ‹API_TOKEN›") || !strings.Contains(text, "connected to ‹DB_URL›") || !strings.Contains(text, "key ‹KEY_PEM›") || !strings.Contains(text, "fast mode") {
		t.Fatal(text)
	}
	// --grep matches the masked lines, so it can't find a value.
	if text, _ := b.devViewLogs(appDevViewRequest{Grep: "COMPETITION"}); text != "job competition-research: 3 of 40" {
		t.Fatal(text)
	}
	if text, _ := b.devViewLogs(appDevViewRequest{Grep: "hunter2"}); !strings.Contains(text, "printed nothing that matches") {
		t.Fatal("found a secret:", text)
	}
	if text, _ := b.devViewLogs(appDevViewRequest{Lines: 2, Since: "30m"}); text != "key ‹KEY_PEM›\njob competition-research: 3 of 40" || (*asked)[len(*asked)-1] != "hi-box-app-payments-app-v11 30m" {
		t.Fatal(text, *asked)
	}
	if _, err := b.devViewLogs(appDevViewRequest{Since: "yesterday"}); err == nil {
		t.Fatal("a bad --since")
	}
	if _, err := b.devViewLogs(appDevViewRequest{Slot: "stage"}); err == nil || !strings.Contains(err.Error(), "Stage has no box now") {
		t.Fatal(err)
	}
	if log, _ := os.ReadFile(filepath.Join(appDevDir("payments"), "net", "network.log")); !strings.Contains(string(log), "view production-logs") {
		t.Fatal(string(log))
	}

	// Jobs: what hi app jobs shows.
	if text, err := b.devViewJobs(appDevViewRequest{Slot: "stage"}); err != nil || !strings.Contains(text, "No jobs in") {
		t.Fatal(text, err)
	}

	// Production's view off: Stage still answers.
	var out bytes.Buffer
	t.Setenv(adminByEnv, "Ada")
	if err := appDevViewCommand("payments", []string{"production", "off"}, &out); err != nil {
		t.Fatal(err)
	}
	if _, err := b.devViewLogs(appDevViewRequest{}); err == nil || !strings.Contains(err.Error(), "off (Ada on the admin page") {
		t.Fatal(err)
	}
	if _, err := b.devViewJobs(appDevViewRequest{}); err == nil {
		t.Fatal("jobs while off")
	}
	if _, err := b.devViewJobs(appDevViewRequest{Slot: "stage"}); err != nil {
		t.Fatal(err)
	}
	appDevViewCommand("payments", []string{"production", "on"}, &out)
	if _, err := b.devViewLogs(appDevViewRequest{}); err != nil {
		t.Fatal(err)
	}
	if err := appDevViewCommand("payments", []string{"stage", "off"}, &out); err == nil {
		t.Fatal("Stage off")
	}
}

// The app's MCP from the dev box, through its socket: on production only
// the tools that read; off, nothing on production.
func TestDevViewTools(t *testing.T) {
	b, _ := appTestDevPage(t)
	os.MkdirAll(filepath.Join(appDevDir("payments"), "net"), 0o700)
	production, preview := &appMCPTestApp{}, &appMCPTestApp{}
	appMCPTestServe(t, appSlotProduction, production)
	appMCPTestServe(t, appSlotPreview, preview)
	socket := filepath.Join(t.TempDir(), "dev.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	server := &httptest.Server{Listener: listener, Config: &http.Server{Handler: b.devHandler()}}
	server.Start()
	t.Cleanup(server.Close)

	var out bytes.Buffer
	if err := appDevTools([]string{"production"}, nil, &out, socket); err != nil || !strings.Contains(out.String(), "list_suppliers") || strings.Contains(out.String(), "pay_supplier") {
		t.Fatal(out.String(), err)
	}
	out.Reset()
	if err := appDevTools([]string{"stage"}, nil, &out, socket); err != nil || !strings.Contains(out.String(), "pay_supplier") {
		t.Fatal(out.String(), err)
	}
	out.Reset()
	if err := appDevTools([]string{"production", "list_suppliers", `{"country":"SE"}`}, nil, &out, socket); err != nil || strings.TrimSpace(out.String()) != "done list_suppliers" {
		t.Fatal(out.String(), err)
	}
	out.Reset()
	if err := appDevTools([]string{"production", "pay_supplier", "{}"}, nil, &out, socket); err == nil && !strings.Contains(out.String(), "can't be used on production") {
		t.Fatal("paid on production:", out.String())
	}
	for _, call := range production.calls {
		if call == "call pay_supplier" {
			t.Fatal("the app was asked to pay")
		}
	}
	if err := appDevTools([]string{"production", "list_suppliers", "{not json"}, nil, &out, socket); err == nil {
		t.Fatal("bad JSON")
	}

	// Off: production's MCP is refused at the socket.
	appDevViewCommand("payments", []string{"production", "off"}, &out)
	if err := appDevTools([]string{"production"}, nil, &out, socket); err == nil || !strings.Contains(err.Error(), "view of production is off") {
		t.Fatal(err)
	}
	out.Reset()
	if err := appDevTools([]string{"stage"}, nil, &out, socket); err != nil {
		t.Fatal(err)
	}

	// The client: logs' and jobs' words.
	call := func(method, path string, body, answer any) error {
		data, _ := json.Marshal(body)
		recorder := httptest.NewRecorder()
		b.devHandler().ServeHTTP(recorder, httptest.NewRequest(method, path, bytes.NewReader(data)))
		return json.Unmarshal(recorder.Body.Bytes(), answer)
	}
	if err := appDevViewClient("logs", []string{"qa"}, nil, &out, call, socket); err == nil {
		t.Fatal("qa isn't a slot")
	}
	if err := appDevViewClient("logs", []string{"--lines", "x"}, nil, &out, call, socket); err == nil {
		t.Fatal("--lines x")
	}
	out.Reset()
	if err := appDevViewClient("jobs", []string{"stage"}, nil, &out, call, socket); err != nil || !strings.Contains(out.String(), "No jobs in") {
		t.Fatal(out.String(), err)
	}
}
