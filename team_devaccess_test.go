package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// teamTestLookup answers names as the machine would: public ones on the
// internet, NetBird's and the LAN's inside.
func teamTestLookup(t *testing.T) {
	previous := teamDevLookup
	teamDevLookup = func(host string) ([]net.IP, error) {
		switch host {
		case "query1.finance.yahoo.com", "api.example.com", "docs.example.org":
			return []net.IP{net.ParseIP("203.0.113.7")}, nil
		case "vmserver.corp.example":
			return []net.IP{net.ParseIP("100.92.4.11")}, nil
		case "nas.example.com":
			return []net.IP{net.ParseIP("203.0.113.8"), net.ParseIP("192.168.1.20")}, nil
		}
		return nil, errors.New("no such host")
	}
	t.Cleanup(func() { teamDevLookup = previous })
}

// An agent asks for a site; the owners hear once, allow it on the page,
// and the box's allowlist has it at once and on its next start.
func TestTeamDevSiteAsk(t *testing.T) {
	b, _ := teamTestDevPage(t)
	teamTestLookup(t)
	channel := &fakeTeamChannel{}
	b.channel = channel
	os.MkdirAll(filepath.Join(teamDevDir("payments"), "net"), 0o700)
	os.WriteFile(teamDevAllowFile("payments"), []byte(strings.Join(teamDevHosts(), "\n")+"\nfiles.example.net\n"), 0o600) // an admin's hi box allow
	box := b.devHandler()
	call := func(method, path string, body, out any) error {
		data, _ := json.Marshal(body)
		recorder := httptest.NewRecorder()
		box.ServeHTTP(recorder, httptest.NewRequest(method, path, bytes.NewReader(data)))
		if recorder.Code != http.StatusOK {
			var failure struct{ Error string }
			json.Unmarshal(recorder.Body.Bytes(), &failure)
			return errors.New(failure.Error)
		}
		return json.Unmarshal(recorder.Body.Bytes(), out)
	}
	branch := func() (string, error) { return "9-game", nil }
	var out bytes.Buffer
	if err := teamDevAllowClient([]string{"https://query1.finance.yahoo.com/v8/finance/chart/AAPL", "--why", "daily price history"}, &out, call, branch); err != nil || !strings.Contains(out.String(), "Asked the team's owners for query1.finance.yahoo.com") {
		t.Fatal(err, out.String())
	}
	out.Reset()
	if err := teamDevAllowClient([]string{"query1.finance.yahoo.com", "--why", "again"}, &out, call, branch); err != nil || !strings.Contains(out.String(), "asked for query1.finance.yahoo.com already") {
		t.Fatal(err, out.String())
	}
	if len(channel.calls) != 1 || !strings.Contains(channel.calls[0], "The dev box asks to reach *query1.finance.yahoo.com* (for 9-game): daily price history.") {
		t.Fatalf("%q", channel.calls)
	}
	// Not without a reason; never inside the network.
	for args, want := range map[string]string{
		"vmserver.corp.example --why x": "inside the network (100.92.4.11)",
		"nas.example.com --why x":       "inside the network (192.168.1.20)",
		"printer.local --why x":         "a private network's name",
		"169.254.169.254 --why x":       "isn't a site's name",
		"nowhere.example.net --why x":   "doesn't resolve",
	} {
		if err := teamDevAllowClient(strings.Fields(args), &out, call, branch); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v", args, err)
		}
	}
	out.Reset()
	if err := teamDevAllowClient([]string{"api.example.com"}, &out, call, branch); err != nil || !strings.Contains(out.String(), "nobody asked for it") {
		t.Fatal(err, out.String())
	}
	if _, err := b.devSiteAsk("api.example.com", " ", ""); err == nil || !strings.Contains(err.Error(), "say what the site is for") {
		t.Fatal("asked without a reason:", err)
	}
	// The page shows the ask, and what the box was refused without one.
	log := time.Now().UTC().Format(time.RFC3339)
	os.WriteFile(filepath.Join(teamDevDir("payments"), "net", "network.log"), []byte(
		log+" blocked docs.example.org\n"+log+" blocked docs.example.org\n"+log+" blocked api.anthropic.com\n"+log+" blocked herdr.dev\n"+log+" blocked query1.finance.yahoo.com\n"+log+" allowed github.com\n"+
			time.Now().Add(-30*24*time.Hour).UTC().Format(time.RFC3339)+" blocked old.example.com\n"), 0o600)
	handler := b.devPageHandler()
	var view teamDevPageView
	_, body := teamTestDevPageCall(t, handler, teamTestOwnerKey, "GET", "api/state", nil)
	json.Unmarshal([]byte(body), &view)
	if len(view.Access.Asks) != 1 || view.Access.Asks[0].Why != "again" || len(view.Access.Refused) != 1 || view.Access.Refused[0].Host != "docs.example.org" || view.Access.Refused[0].Times != 2 {
		t.Fatalf("%+v", view.Access)
	}
	if code, _ := teamTestDevPageCall(t, handler, teamTestAskerKey, "POST", "api/site", map[string]any{"host": "query1.finance.yahoo.com", "allow": true}); code == 200 {
		t.Fatal("someone who isn't an owner allowed a site")
	}
	if code, body := teamTestDevPageCall(t, handler, teamTestOwnerKey, "POST", "api/site", map[string]any{"host": "query1.finance.yahoo.com", "allow": true}); code != 200 {
		t.Fatal(code, body)
	}
	allow, _ := os.ReadFile(teamDevAllowFile("payments"))
	if !strings.Contains(string(allow), "\nquery1.finance.yahoo.com\n") || !strings.Contains(string(allow), "files.example.net") || !boxHostAllowed(teamDevAllowHosts("payments"), "query1.finance.yahoo.com") {
		t.Fatal(string(allow))
	}
	out.Reset()
	if err := teamDevAllowClient([]string{"query1.finance.yahoo.com", "--wait"}, &out, call, branch); err != nil || !strings.Contains(out.String(), "may reach query1.finance.yahoo.com now") {
		t.Fatal(err, out.String())
	}
	access := loadTeamDevAccess("payments")
	if len(access.Allowed) != 1 || access.Allowed[0].By != "Olga" || access.Allowed[0].Why != "again" {
		t.Fatalf("%+v", access)
	}
	// Never inside the network, even from the page.
	if code, _ := teamTestDevPageCall(t, handler, teamTestOwnerKey, "POST", "api/site", map[string]any{"host": "vmserver.corp.example", "allow": true}); code == 200 {
		t.Fatal("allowed a host inside the network")
	}
	// Saying no to a refusal stops it showing; an agent asking then hears no.
	teamTestDevPageCall(t, handler, teamTestOwnerKey, "POST", "api/site", map[string]any{"host": "docs.example.org", "allow": false})
	out.Reset()
	if err := teamDevAllowClient([]string{"docs.example.org", "--why", "the API docs"}, &out, call, branch); err == nil || !strings.Contains(out.String(), "Olga said no to docs.example.org") {
		t.Fatal(err, out.String())
	}
	if b.devAccessView().Refused != nil {
		t.Fatal("a refusal an owner dismissed still shows")
	}
	// Removed, it's off the list, and an admin's line stays.
	if code, _ := teamTestDevPageCall(t, handler, teamTestOwnerKey, "POST", "api/site", map[string]any{"host": "query1.finance.yahoo.com", "remove": true}); code != 200 {
		t.Fatal("remove")
	}
	allow, _ = os.ReadFile(teamDevAllowFile("payments"))
	if strings.Contains(string(allow), "yahoo") || !strings.Contains(string(allow), "files.example.net") {
		t.Fatal(string(allow))
	}
}

// --wait waits for the owners, and hears their answer.
// An admin on the machine allows a site as the owners do on the page,
// with what for, and takes it off again; the box's list has it at once.
func TestTeamDevAllowAdmin(t *testing.T) {
	teamTestDevPage(t)
	teamTestLookup(t)
	os.MkdirAll(filepath.Join(teamDevDir("payments"), "net"), 0o700)
	var out bytes.Buffer
	if err := teamDevAllowAdmin("payments", nil, &out); err != nil || !strings.Contains(out.String(), "allowed no sites") {
		t.Fatal(err, out.String())
	}
	out.Reset()
	if err := teamDevAllowAdmin("payments", []string{"https://api.example.com/v1", "--why", "the test database"}, &out); err != nil || !strings.Contains(out.String(), "reaches api.example.com") {
		t.Fatal(err, out.String())
	}
	if data, _ := os.ReadFile(teamDevAllowFile("payments")); !strings.Contains(string(data), "api.example.com\n") {
		t.Fatal(string(data))
	}
	out.Reset()
	teamDevAllowAdmin("payments", nil, &out)
	if !strings.Contains(out.String(), "api.example.com  ") || !strings.Contains(out.String(), "on the machine") || !strings.Contains(out.String(), ": the test database") {
		t.Fatal(out.String())
	}
	for _, bad := range [][]string{{"vmserver.corp.example"}, {"api.example.com", "--why"}, {"--why", "x"}, {"--remove", "docs.example.org"}} {
		if err := teamDevAllowAdmin("payments", bad, &out); err == nil {
			t.Errorf("%q was taken", bad)
		}
	}
	if err := teamDevAllowAdmin("payments", []string{"--remove", "api.example.com"}, &out); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(teamDevAllowFile("payments")); strings.Contains(string(data), "api.example.com") {
		t.Fatal(string(data))
	}
}

func TestTeamDevSiteWait(t *testing.T) {
	b, _ := teamTestDevPage(t)
	teamTestLookup(t)
	b.channel = &fakeTeamChannel{}
	os.MkdirAll(filepath.Join(teamDevDir("payments"), "net"), 0o700)
	previous, every := teamDevAllowWait, teamDevAllowEvery
	teamDevAllowWait, teamDevAllowEvery = 2*time.Second, 20*time.Millisecond
	t.Cleanup(func() { teamDevAllowWait, teamDevAllowEvery = previous, every })
	box := b.devHandler()
	polls := 0
	call := func(method, path string, body, out any) error {
		if method == "GET" {
			if polls++; polls == 3 {
				b.devSiteDecide(teamPersonKey{Name: "Olga"}, "api.example.com", true)
			}
		}
		data, _ := json.Marshal(body)
		recorder := httptest.NewRecorder()
		box.ServeHTTP(recorder, httptest.NewRequest(method, path, bytes.NewReader(data)))
		return json.Unmarshal(recorder.Body.Bytes(), out)
	}
	var out bytes.Buffer
	if err := teamDevAllowClient([]string{"api.example.com", "--why", "the API", "--wait"}, &out, call, func() (string, error) { return "", nil }); err != nil || !strings.Contains(out.String(), "Allowed: the box reaches api.example.com now.") {
		t.Fatal(err, out.String())
	}
}

// The dev box's proxy connects only to public addresses, whatever a name
// says, and tells its agents how to ask for a site.
func TestBoxPublicDialer(t *testing.T) {
	previous := boxLookupIP
	boxLookupIP = func(ctx context.Context, host string) ([]net.IP, error) {
		switch host {
		case "inside.example.com":
			return []net.IP{net.ParseIP("10.0.0.5"), net.ParseIP("100.64.1.1")}, nil
		case "metadata.example.com":
			return []net.IP{net.ParseIP("169.254.169.254")}, nil
		}
		return []net.IP{net.ParseIP("127.0.0.1")}, nil
	}
	t.Cleanup(func() { boxLookupIP = previous })
	var logged bytes.Buffer
	dial := boxPublicDialer(&boxNetworkLog{file: &logged})
	for _, address := range []string{"inside.example.com:443", "metadata.example.com:80", "localhost:22", "[::1]:22", "100.100.100.100:53"} {
		if conn, err := dial(context.Background(), "tcp", address); err == nil {
			conn.Close()
			t.Errorf("reached %s", address)
		}
	}
	if !strings.Contains(logged.String(), "private inside.example.com") {
		t.Fatal(logged.String())
	}
	for ip, private := range map[string]bool{"100.64.0.1": true, "100.127.255.254": true, "100.128.0.1": false, "8.8.8.8": false, "fd00::1": true, "fe80::1": true, "2001:4860::8888": false, "0.0.0.0": true} {
		if boxPrivateIP(net.ParseIP(ip)) != private {
			t.Errorf("%s: private %v", ip, !private)
		}
	}
	proxy := &boxEgressProxy{allow: &boxAllowlist{hosts: []string{"github.com"}, path: "/nonexistent"}, log: &boxNetworkLog{file: &logged}, ask: "team-payments-dev"}
	recorder := httptest.NewRecorder()
	proxy.ServeHTTP(recorder, httptest.NewRequest(http.MethodConnect, "query1.finance.yahoo.com:443", nil))
	if recorder.Code != http.StatusForbidden || !strings.Contains(recorder.Body.String(), `hi dev allow query1.finance.yahoo.com --why`) {
		t.Fatal(recorder.Code, recorder.Body.String())
	}
}

// The Security tab says whose each key is, never what it is.
func TestTeamDevKeys(t *testing.T) {
	setupTeamTest(t)
	newTeamTestBroker(t)
	secrets, _ := loadTeamSecrets("payments")
	secrets.OpenRouterAPIKey, secrets.ClaudeToken = "sk-or-secret", "sk-ant-secret"
	saveTeamSecrets("payments", secrets)
	keys := teamDevKeys("payments")
	data, _ := json.Marshal(keys)
	if len(keys) != 4 || keys[0].State != "The team's own" || keys[1].State != "The team's own token" || keys[2].State != "This machine's sign-in (Codex's)" || keys[0].Command != "hi team key payments openrouter" || strings.Contains(string(data), "secret") {
		t.Fatalf("%s", data)
	}
}
