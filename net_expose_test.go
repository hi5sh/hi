package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// fakeNetbirdExpose stands in for `netbird expose <port> …`: it prints a
// name and a URL straight to the relay's port, and records its arguments.
func fakeNetbirdExpose(t *testing.T, output string) string {
	t.Helper()
	dir := t.TempDir()
	record := filepath.Join(dir, "args")
	script := filepath.Join(dir, "netbird")
	body := output
	if body == "" {
		body = "echo 'Service exposed successfully!'\necho '  Name:     hi-test-ab12'\necho \"  URL:      http://127.0.0.1:$2\"\nexec sleep 600\n"
	}
	writeTestFile(t, script, "#!/bin/sh\necho \"$*\" > "+record+"\n"+body, 0o755)
	previous, previousAddress := exposeCommand, netExposeAddress
	exposeCommand = script
	netExposeAddress = func() (string, error) { return "127.0.0.1", nil }
	t.Cleanup(func() { exposeCommand, netExposeAddress = previous, previousAddress })
	t.Setenv("XDG_STATE_HOME", filepath.Join(t.TempDir(), "state"))
	return record
}

func TestNetExposeOptions(t *testing.T) {
	options, detach, err := parseNetExpose([]string{"3000"})
	if err != nil || detach || options.Port != 3000 || options.Max != time.Hour || options.Protection != "password" ||
		strings.Count(options.Secret, "-") != 3 || !strings.HasPrefix(options.Name, "hi-") || !strings.HasSuffix(options.Name, "-3000") {
		t.Fatalf("defaults: %+v %v", options, err)
	}
	if options, _, err := parseNetExpose([]string{"8080", "--public", "--max", "30m", "--name", "demo", "--detach"}); err != nil ||
		options.Protection != "public" || options.Max != 30*time.Minute || options.Name != "demo" {
		t.Fatalf("public: %+v %v", options, err)
	}
	for _, args := range [][]string{
		{"3000", "--max", "none"}, {"3000", "--max", "48h"}, {"3000", "--pin", "12"},
		{"3000", "--public", "--pin", "123456"}, {"3000", "--name", "Bad Name"}, {}, {"notaport"},
	} {
		if _, _, err := parseNetExpose(args); err == nil {
			t.Errorf("%v was accepted", args)
		}
	}
	args := netExposeArgs(netExposeOptions{Name: "demo", Protection: "groups", Groups: "devops"}, 4000)
	if strings.Join(args, " ") != "expose 4000 --with-name-prefix demo --with-user-groups devops" {
		t.Fatalf("args: %v", args)
	}
}

func TestNetExposeRelaysALocalServiceUntilMax(t *testing.T) {
	ts := newTestServer(t)
	ts.connectAs(t, "alice", "students")
	record := fakeNetbirdExpose(t, "")
	var hosts []string
	service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hosts = append(hosts, r.Host)
		w.Write([]byte("hello from " + r.URL.Path))
	}))
	defer service.Close()
	port := service.Listener.Addr().(interface{ String() string }).String()
	port = port[strings.LastIndex(port, ":")+1:]

	var stdout, stderr bytes.Buffer
	done := make(chan int)
	go func() {
		done <- runNetExpose([]string{port, "--host", "127.0.0.1", "--max", "2s", "--pin", "123456"}, strings.NewReader(""), &stdout, &stderr)
	}()
	var url string
	for i := 0; i < 50 && url == ""; i++ {
		time.Sleep(100 * time.Millisecond)
		if states := netExposeStates(); len(states) == 1 {
			url = states["hi-test-ab12"].URL
		}
	}
	if url == "" {
		t.Fatalf("no state:\n%s%s", stdout.String(), stderr.String())
	}
	response, err := http.Get(url + "/page")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if string(body) != "hello from /page" || !strings.HasPrefix(hosts[len(hosts)-1], "127.0.0.1:") {
		t.Fatalf("relay: %q, hosts %v", body, hosts)
	}
	code := <-done
	args, _ := os.ReadFile(record)
	if code != 0 || !strings.Contains(stdout.String(), "Exposing 127.0.0.1:"+port+" at http://127.0.0.1:") || !strings.Contains(stdout.String(), "PIN: 123456") ||
		!strings.Contains(stdout.String(), "GET    /page") || !strings.Contains(stderr.String(), "--max") ||
		!strings.Contains(string(args), "--with-pin 123456") {
		t.Fatalf("code %d\n%s%s\nargs %s", code, stdout.String(), stderr.String(), args)
	}
	if len(netExposeStates()) != 0 {
		t.Fatal("the state file outlived the exposure")
	}
}

func TestServerRecordsExposures(t *testing.T) {
	ts := newTestServer(t)
	ts.connectAs(t, "alice", "students")
	reportNetExpose(netExposeState{URL: "https://hi-demo-ab12.eu1.netbird.services", Port: 3000, Protection: "password",
		Expires: computeNow().Add(time.Hour)}, true)
	reportNetExpose(netExposeState{URL: "https://hi-demo-ab12.eu1.netbird.services", Port: 3000, Protection: "password"}, false)
	audit, _ := os.ReadFile(filepath.Join(ts.dir, "audit.jsonl"))
	if !strings.Contains(string(audit), `"action":"exposed","subject":"https://hi-demo-ab12.eu1.netbird.services"`) ||
		!strings.Contains(string(audit), `"action":"stopped exposing"`) || !strings.Contains(string(audit), "port 3000, password") {
		t.Fatalf("audit = %s", audit)
	}
	client, _, _ := dataClient()
	if err := client.call(http.MethodPost, "/v1/activity", apiActivity{Instance: "http://plain", Command: "expose", Open: true}, nil); err == nil {
		t.Fatal("a non-https exposure was recorded")
	}
}

func TestNetExposeExplainsPeerExposeOff(t *testing.T) {
	fakeNetbirdExpose(t, "echo 'Error: receive expose event: peer expose is not enabled for this account'\nexit 1\n")
	code, _, stderr := runHi("net", "expose", "3999", "--public")
	if code == 0 || !strings.Contains(stderr, "turns on Peer Expose in Settings > Clients") {
		t.Fatalf("code %d\n%s", code, stderr)
	}
	if code, _, stderr := runHi("net", "expose", "stop", "nothing"); code == 0 || !strings.Contains(stderr, "nothing named") {
		t.Fatalf("stop: %d %s", code, stderr)
	}
	if code, stdout, _ := runHi("net", "expose", "ls"); code != 0 || !strings.Contains(stdout, "Nothing is exposed") {
		t.Fatalf("ls: %d %s", code, stdout)
	}
}

func TestNetExposeLinkKey(t *testing.T) {
	var seen []*http.Request
	service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r)
		w.Write([]byte("app " + r.URL.RequestURI()))
	}))
	defer service.Close()
	relay := httptest.NewServer(newNetExposeRelayWithKey(service.URL, "s3cret-key", time.Now().Add(time.Hour), io.Discard))
	defer relay.Close()
	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	// Without the key: the page that asks for the link, and the app sees nothing.
	for _, path := range []string{"/", "/?hi_key=wrong"} {
		response, _ := noRedirect.Get(relay.URL + path)
		if response.StatusCode != http.StatusUnauthorized || len(seen) != 0 {
			t.Fatal(path, response.StatusCode, len(seen))
		}
	}
	// The link: a cookie, and on to the address without the key.
	response, err := noRedirect.Get(relay.URL + "/clock?city=Oslo&hi_key=s3cret-key")
	if err != nil {
		t.Fatal(err)
	}
	cookies := response.Cookies()
	if response.StatusCode != http.StatusSeeOther || response.Header.Get("Location") != "/clock?city=Oslo" || len(cookies) != 1 || !cookies[0].HttpOnly || !cookies[0].Secure {
		t.Fatal(response.StatusCode, response.Header, cookies)
	}
	// With the cookie the app answers, and never sees the key's cookie.
	request, _ := http.NewRequest(http.MethodGet, relay.URL+"/clock?city=Oslo", nil)
	request.AddCookie(cookies[0])
	request.AddCookie(&http.Cookie{Name: "theme", Value: "zen"})
	response, err = noRedirect.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	if string(body) != "app /clock?city=Oslo" || seen[0].Header.Get("Cookie") != "theme=zen" {
		t.Fatal(string(body), seen[0].Header)
	}
	// Scripts send the key as a Bearer token.
	request, _ = http.NewRequest(http.MethodPost, relay.URL+"/api", nil)
	request.Header.Set("Authorization", "Bearer s3cret-key")
	if response, _ = noRedirect.Do(request); response.StatusCode != http.StatusOK || seen[1].Header.Get("Authorization") != "" {
		t.Fatal(response.StatusCode)
	}
	if link := netExposeLink("https://team-x.eu1.netbird.services/", "k"); link != "https://team-x.eu1.netbird.services/?hi_key=k" {
		t.Fatal(link)
	}
	if options, _, err := parseNetExpose([]string{"3000", "--link"}); err != nil || options.Protection != "link" || len(options.Secret) != 22 {
		t.Fatal(options, err)
	}
	if args := netExposeArgs(netExposeOptions{Name: "x", Protection: "link"}, 4000); strings.Join(args, " ") != "expose 4000 --with-name-prefix x" {
		t.Fatal(args)
	}
}

func TestNetExposeFollow(t *testing.T) {
	serve := func(name string) (*httptest.Server, int) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, name) }))
		t.Cleanup(server.Close)
		port, _ := parsePort(server.URL[strings.LastIndex(server.URL, ":")+1:])
		return server, port
	}
	_, oldPort := serve("old")
	_, newPort := serve("new")
	file := filepath.Join(t.TempDir(), "port")
	relay := httptest.NewServer(newNetExposeRelayTo(netExposeFollow(file, "127.0.0.1", oldPort), "", time.Time{}, io.Discard, nil))
	defer relay.Close()
	get := func() string {
		response, err := http.Get(relay.URL)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		body, _ := io.ReadAll(response.Body)
		return string(body)
	}
	// Until the file has a port, the one it started with.
	if got := get(); got != "old" {
		t.Fatal(got)
	}
	writeFileAtomic(file, []byte(strconv.Itoa(newPort)+"\n"), 0o600)
	if got := get(); got != "new" {
		t.Fatal(got)
	}
	writeFileAtomic(file, []byte(strconv.Itoa(oldPort)+"\n"), 0o600)
	if got := get(); got != "old" {
		t.Fatal(got)
	}
}
