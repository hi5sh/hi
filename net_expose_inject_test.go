package main

import (
	"compress/gzip"
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

const testHelperTag = `<script src="/__hi/helper.js?v=1" defer></script>`

// relayWithHelper relays to service with --inject, a --route to a socket
// that answers "helper <path>", and --inject-if; it returns the relay and
// the --inject-if file.
func relayWithHelper(t *testing.T, service http.Handler) (*httptest.Server, string) {
	t.Helper()
	app := httptest.NewServer(service)
	t.Cleanup(app.Close)
	socket := filepath.Join(shortTempDir(t), "web.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	helperServer := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "helper "+r.URL.Path+" cookie="+r.Header.Get("Cookie"))
	})}
	go helperServer.Serve(listener)
	t.Cleanup(func() { helperServer.Close() })
	on := filepath.Join(t.TempDir(), "preview.helper")
	writeTestFile(t, on, "", 0o600)
	helper := newNetExposeHelper(netExposeOptions{Inject: "/__hi/helper.js?v=1", InjectIf: on,
		Routes: []netExposeRoute{{Prefix: "/__hi/", Socket: socket}}}, nil)
	relay := httptest.NewServer(newNetExposeRelayTo(func() string { return app.URL }, "", time.Time{}, io.Discard, helper))
	t.Cleanup(relay.Close)
	return relay, on
}

// shortTempDir is a folder whose path is short enough for a socket.
func shortTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "hi")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

func fetchPage(t *testing.T, url, dest string, header ...string) (*http.Response, string) {
	t.Helper()
	request, _ := http.NewRequest(http.MethodGet, url, nil)
	if dest != "" {
		request.Header.Set("Sec-Fetch-Dest", dest)
	}
	for i := 0; i+1 < len(header); i += 2 {
		request.Header.Set(header[i], header[i+1])
	}
	response, err := http.DefaultTransport.RoundTrip(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	return response, string(body)
}

func TestNetExposeInjectsPageLoadsOnly(t *testing.T) {
	page := `<!doctype html><html><head><title>Suppliers</title></head><body><header>x</header></body></html>`
	relay, _ := relayWithHelper(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api":
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"html":"<head>"}`)
		case "/missing":
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusNotFound)
			io.WriteString(w, page)
		default:
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("ETag", `"abc"`)
			io.WriteString(w, page)
		}
	}))
	response, body := fetchPage(t, relay.URL+"/suppliers", "document")
	want := strings.Replace(page, "<head>", "<head>"+testHelperTag, 1)
	if body != want {
		t.Fatalf("page load:\n%s\nwant\n%s", body, want)
	}
	if response.Header.Get("ETag") != "" {
		t.Fatal("a changed page kept its strong ETag:", response.Header)
	}
	for _, dest := range []string{"", "empty", "iframe"} {
		if _, body := fetchPage(t, relay.URL+"/suppliers", dest); body != page {
			t.Fatalf("Sec-Fetch-Dest %q got the helper: %s", dest, body)
		}
	}
	if _, body := fetchPage(t, relay.URL+"/api", "document"); strings.Contains(body, "script") {
		t.Fatal("JSON got the helper:", body)
	}
	if _, body := fetchPage(t, relay.URL+"/missing", "document"); strings.Contains(body, "helper.js") {
		t.Fatal("a 404 got the helper:", body)
	}
}

func TestNetExposeInjectPlaces(t *testing.T) {
	big := strings.Repeat("x", netExposeInjectScan+10)
	for _, test := range []struct{ name, page, want string }{
		{"head with attributes", `<html><HEAD lang="en"><title>t</title>`, `<html><HEAD lang="en">` + testHelperTag + `<title>t</title>`},
		{"no head", `<html><body class="x">hi</body></html>`, `<html>` + testHelperTag + `<body class="x">hi</body></html>`},
		{"header isn't head", `<html><header>a</header><body>b</body>`, `<html><header>a</header>` + testHelperTag + `<body>b</body>`},
		{"html only", `<!DOCTYPE html><html lang="sv"><p>hej`, `<!DOCTYPE html><html lang="sv">` + testHelperTag + `<p>hej`},
		{"doctype only", `<!doctype html><p>hej`, `<!doctype html>` + testHelperTag + `<p>hej`},
		{"nothing", `just text`, `just text`},
		{"too far", big + `<head>`, big + `<head>`},
	} {
		t.Run(test.name, func(t *testing.T) {
			injector := &netExposeInjector{source: io.NopCloser(strings.NewReader(test.page)), tag: []byte(testHelperTag)}
			got, _ := io.ReadAll(injector)
			if string(got) != test.want {
				t.Fatalf("got\n%.200s\nwant\n%.200s", got, test.want)
			}
		})
	}
}

func TestNetExposeInjectStreams(t *testing.T) {
	release := make(chan struct{})
	relay, _ := relayWithHelper(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		io.WriteString(w, "<html><he")
		w.(http.Flusher).Flush()
		time.Sleep(20 * time.Millisecond)
		io.WriteString(w, "ad><title>t</title>")
		w.(http.Flusher).Flush()
		<-release
		io.WriteString(w, "</head><body>late</body>")
	}))
	request, _ := http.NewRequest(http.MethodGet, relay.URL, nil)
	request.Header.Set("Sec-Fetch-Dest", "document")
	response, err := http.DefaultTransport.RoundTrip(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	// The start of the page arrives before the app finishes it.
	start := make([]byte, len("<html><head>"+testHelperTag))
	if _, err := io.ReadFull(response.Body, start); err != nil {
		t.Fatal(err)
	}
	if string(start) != "<html><head>"+testHelperTag {
		t.Fatal(string(start))
	}
	close(release)
	rest, _ := io.ReadAll(response.Body)
	if string(rest) != "<title>t</title></head><body>late</body>" {
		t.Fatal(string(rest))
	}
}

func TestNetExposeInjectUncompressed(t *testing.T) {
	seen := make(chan string, 2)
	relay, _ := relayWithHelper(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- r.Header.Get("Accept-Encoding")
		w.Header().Set("Content-Type", "text/html")
		if r.URL.Path == "/stubborn" {
			// An app that compresses whatever it is asked.
			w.Header().Set("Content-Encoding", "gzip")
			zipped := gzip.NewWriter(w)
			io.WriteString(zipped, "<head></head>")
			zipped.Close()
			return
		}
		io.WriteString(w, "<head></head>")
	}))
	if _, body := fetchPage(t, relay.URL, "document", "Accept-Encoding", "gzip, br"); !strings.Contains(body, testHelperTag) {
		t.Fatal(body)
	}
	if encoding := <-seen; encoding != "identity" {
		t.Fatal("the app was asked for", encoding)
	}
	response, _ := fetchPage(t, relay.URL+"/stubborn", "document", "Accept-Encoding", "gzip")
	<-seen
	if response.Header.Get("Content-Encoding") != "gzip" {
		t.Fatal("a compressed page was changed:", response.Header)
	}
}

func TestNetExposeInjectCSP(t *testing.T) {
	relay, _ := relayWithHelper(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'nonce-r4nd0m' 'strict-dynamic'; img-src *")
		io.WriteString(w, "<head></head>")
	}))
	response, body := fetchPage(t, relay.URL, "document")
	if !strings.Contains(body, `defer nonce="r4nd0m"></script>`) {
		t.Fatal(body)
	}
	policy := response.Header.Get("Content-Security-Policy")
	if policy != "default-src 'self'; script-src 'nonce-r4nd0m' 'strict-dynamic' 'self'; img-src *" {
		t.Fatal(policy)
	}
	for _, test := range []struct{ policy, want string }{
		{"script-src 'self' https://cdn; connect-src https://api", "script-src 'self' https://cdn; connect-src https://api 'self'"},
		{"default-src 'self'", "default-src 'self'"},
		{"script-src https://cdn; connect-src 'self'; default-src 'none'", "script-src https://cdn 'self'; connect-src 'self'; default-src 'none'"},
	} {
		if got, _ := netExposeCSP(test.policy); got != test.want {
			t.Errorf("%s\n got %s\nwant %s", test.policy, got, test.want)
		}
	}
}

func TestNetExposeHelperRouteAndSwitch(t *testing.T) {
	relay, on := relayWithHelper(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		io.WriteString(w, "app "+r.URL.Path+"<head></head>")
	}))
	if _, body := fetchPage(t, relay.URL+"/__hi/api/status", "", "Cookie", "session=app-secret"); body != "helper /__hi/api/status cookie=" {
		t.Fatal("the route:", body)
	}
	os.Remove(on)
	if _, body := fetchPage(t, relay.URL+"/__hi/api/status", ""); !strings.HasPrefix(body, "app /__hi/api/status") {
		t.Fatal("off, /__hi/ should reach the app:", body)
	}
	if _, body := fetchPage(t, relay.URL+"/", "document"); strings.Contains(body, "helper.js") {
		t.Fatal("off, the page got the helper:", body)
	}
	writeTestFile(t, on, "", 0o600)
	if _, body := fetchPage(t, relay.URL+"/", "document"); !strings.Contains(body, testHelperTag) {
		t.Fatal("on again, no helper:", body)
	}
}

func TestParseNetExposeHelperFlags(t *testing.T) {
	options, _, err := parseNetExpose([]string{"3000", "--link", "--route", "/__hi/=unix:/run/web.sock", "--inject", "/__hi/helper.js", "--inject-if", "/tmp/on"})
	if err != nil {
		t.Fatal(err)
	}
	if len(options.Routes) != 1 || options.Routes[0] != (netExposeRoute{Prefix: "/__hi/", Socket: "/run/web.sock"}) || options.Inject != "/__hi/helper.js" || options.InjectIf != "/tmp/on" {
		t.Fatalf("%+v", options)
	}
	for _, bad := range [][]string{{"3000", "--route", "/__hi=unix:/x"}, {"3000", "--route", "/__hi/=tcp:1"}, {"3000", "--inject-if", "/tmp/on"}} {
		if _, _, err := parseNetExpose(bad); err == nil {
			t.Error("accepted", bad)
		}
	}
}
