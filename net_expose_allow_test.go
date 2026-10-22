package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestParseNetExposeAllow(t *testing.T) {
	options, _, err := parseNetExpose([]string{"3000", "--allow-ip", "203.0.113.18, 10.1.2.3/8,::1"})
	if err != nil || strings.Join(options.AllowIPs, " ") != "203.0.113.18/32 10.0.0.0/8 ::1/128" || options.Protection != "password" {
		t.Fatal(options, err)
	}
	for _, text := range []string{",", "203.0.113", "example.com", "10.0.0.0/33"} {
		if _, _, err := parseNetExpose([]string{"3000", "--allow-ip", text}); err == nil {
			t.Errorf("--allow-ip %q was accepted", text)
		}
	}

	options, _, err = parseNetExpose([]string{"3000", "--vpn"})
	if err != nil || !options.VPN || options.Protection != "vpn" || options.Secret != "" {
		t.Fatal(options, err)
	}
	options, _, err = parseNetExpose([]string{"3000", "--vpn", "--link", "--allow-ip", "100.100.1.2"})
	if err != nil || !options.VPN || options.Protection != "link" || options.Secret == "" || options.AllowIPs[0] != "100.100.1.2/32" {
		t.Fatal(options, err)
	}
	for _, args := range [][]string{
		{"3000", "--vpn", "--password", "x"}, {"3000", "--vpn", "--pin", "123456"}, {"3000", "--vpn", "--public"},
		{"3000", "--vpn", "--groups", "devops"}, {"3000", "--vpn", "--domain", "app.example.com"},
	} {
		if _, _, err := parseNetExpose(args); err == nil || !strings.Contains(err.Error(), "--vpn") {
			t.Errorf("%v: %v", args, err)
		}
	}
}

func TestNetExposeGuard(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "in") })
	open := false
	guard := netExposeGuard(next, func() bool { return open }, []netip.Prefix{netip.MustParsePrefix("100.100.0.0/16")})
	status := func(remote string) int {
		request := httptest.NewRequest(http.MethodGet, "/", nil)
		request.RemoteAddr = remote
		recorder := httptest.NewRecorder()
		guard.ServeHTTP(recorder, request)
		return recorder.Code
	}
	if code := status("100.100.3.4:5000"); code != http.StatusServiceUnavailable {
		t.Fatal("closed, yet", code)
	}
	open = true
	if code := status("100.100.3.4:5000"); code != http.StatusOK {
		t.Fatal(code)
	}
	if code := status("100.101.3.4:5000"); code != http.StatusForbidden {
		t.Fatal("outside the block, yet", code)
	}
}

// rawNetbirdAPI is one reverse-proxy service as NetBird keeps it: what
// netbird expose made, with its password left out of what it returns.
type rawNetbirdAPI struct {
	mu       sync.Mutex
	service  map[string]any
	puts     []map[string]any
	dropRule bool // saving leaves out access_restrictions
}

func (f *rawNetbirdAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	returned := func() map[string]any {
		data, _ := json.Marshal(f.service)
		var copy map[string]any
		json.Unmarshal(data, &copy)
		if auth, ok := copy["auth"].(map[string]any); ok {
			if password, ok := auth["password_auth"].(map[string]any); ok {
				password["password"] = ""
			}
		}
		return copy
	}
	switch {
	case r.URL.Path == "/api/reverse-proxies/services" && r.Method == http.MethodGet:
		json.NewEncoder(w).Encode([]any{returned()})
	case r.URL.Path == "/api/reverse-proxies/services/svc-9" && r.Method == http.MethodGet:
		json.NewEncoder(w).Encode(returned())
	case r.URL.Path == "/api/reverse-proxies/services/svc-9" && r.Method == http.MethodPut:
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		f.puts = append(f.puts, body)
		if f.dropRule {
			delete(body, "access_restrictions")
		}
		f.service = body
		json.NewEncoder(w).Encode(body)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func fakeRawNetbird(t *testing.T, domain string) *rawNetbirdAPI {
	t.Helper()
	fake := &rawNetbirdAPI{service: map[string]any{"id": "svc-9", "domain": domain, "enabled": true,
		"auth": map[string]any{"password_auth": map[string]any{"enabled": true, "password": "amber-birch-cedar-07"}}}}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	before := netbirdAPIURL
	netbirdAPIURL = func() (string, error) { return server.URL, nil }
	t.Cleanup(func() { netbirdAPIURL = before })
	t.Setenv("NETBIRD_API_TOKEN", "nbp_test")
	return fake
}

func TestNetExposeRestrict(t *testing.T) {
	fake := fakeRawNetbird(t, "hi-demo-ab12.eu1.netbird.services")
	options := netExposeOptions{Protection: "password", Secret: "amber-birch-cedar-07", AllowIPs: []string{"203.0.113.18/32"}}
	keep, err := netExposeRestrict("https://hi-demo-ab12.eu1.netbird.services", 41001, options)
	if err != nil {
		t.Fatal(err)
	}
	// The rule is set, and the password NetBird didn't return goes back.
	put := fake.puts[0]
	auth := put["auth"].(map[string]any)["password_auth"].(map[string]any)
	if len(fake.puts) != 1 || !netExposeHasRule(put, options.AllowIPs) || auth["password"] != "amber-birch-cedar-07" {
		t.Fatal(fake.puts)
	}
	// A check that finds the rule changes nothing; one that doesn't puts it back.
	if err := keep(); err != nil || len(fake.puts) != 1 {
		t.Fatal(err, len(fake.puts))
	}
	fake.mu.Lock()
	delete(fake.service, "access_restrictions")
	fake.mu.Unlock()
	if err := keep(); err != nil || len(fake.puts) != 2 {
		t.Fatal(err, len(fake.puts))
	}
	// NetBird not keeping it is an error, and so is no service at all.
	fake.mu.Lock()
	delete(fake.service, "access_restrictions")
	fake.dropRule = true
	fake.mu.Unlock()
	if err := keep(); err == nil || !strings.Contains(err.Error(), "didn't keep") {
		t.Fatal(err)
	}
	// A service another relay has taken over is left as it is.
	fake.mu.Lock()
	fake.dropRule = false
	fake.service["targets"] = []any{map[string]any{"port": 41002}}
	fake.mu.Unlock()
	puts := len(fake.puts)
	if err := keep(); err != nil || len(fake.puts) != puts {
		t.Fatal(err, len(fake.puts))
	}
	if _, err := netExposeRestrict("https://other.eu1.netbird.services", 41001, options); err == nil || !strings.Contains(err.Error(), "no service") {
		t.Fatal(err)
	}
}

func TestNetExposeAllowIPThroughNetbirdExpose(t *testing.T) {
	record := fakeNetbirdExpose(t, "")
	fake := fakeRawNetbird(t, "127.0.0.1")
	service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "hello") }))
	defer service.Close()
	port := service.Listener.Addr().String()
	port = port[strings.LastIndex(port, ":")+1:]

	var stdout, stderr bytes.Buffer
	done := make(chan int)
	go func() {
		done <- runNetExpose([]string{port, "--host", "127.0.0.1", "--max", "2s", "--public", "--allow-ip", "203.0.113.7"}, strings.NewReader(""), &stdout, &stderr)
	}()
	var state netExposeState
	for i := 0; i < 50 && state.URL == ""; i++ {
		time.Sleep(100 * time.Millisecond)
		state = netExposeStates()["hi-test-ab12"]
	}
	if state.URL == "" {
		t.Fatalf("no state:\n%s%s", stdout.String(), stderr.String())
	}
	response, err := http.Get(state.URL)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	code := <-done
	args, _ := os.ReadFile(record)
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if code != 0 || string(body) != "hello" || len(fake.puts) != 1 || !netExposeHasRule(fake.service, []string{"203.0.113.7/32"}) ||
		!strings.Contains(stdout.String(), "only from 203.0.113.7/32") || !strings.HasPrefix(string(args), "expose ") {
		t.Fatalf("code %d, body %q, puts %v\n%s%s", code, body, fake.puts, stdout.String(), stderr.String())
	}
}

func TestNetExposeVPN(t *testing.T) {
	record := fakeNetbirdExpose(t, "")
	before := netExposeVPNName
	netExposeVPNName = func(address string) string { return address }
	t.Cleanup(func() { netExposeVPNName = before })
	service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "hello") }))
	defer service.Close()
	port := service.Listener.Addr().String()
	port = port[strings.LastIndex(port, ":")+1:]

	var stdout, stderr bytes.Buffer
	done := make(chan int)
	go func() {
		done <- runNetExpose([]string{port, "--host", "127.0.0.1", "--max", "2s", "--vpn", "--allow-ip", "127.0.0.0/8"}, strings.NewReader(""), &stdout, &stderr)
	}()
	var state netExposeState
	for i := 0; i < 50 && state.URL == ""; i++ {
		time.Sleep(100 * time.Millisecond)
		for _, running := range netExposeStates() {
			state = running
		}
	}
	if state.URL == "" || !state.VPN || state.Protection != "vpn" || !strings.HasPrefix(state.URL, "http://127.0.0.1:") {
		t.Fatalf("state %+v\n%s%s", state, stdout.String(), stderr.String())
	}
	response, err := http.Get(state.URL)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	code := <-done
	if _, err := os.Stat(record); err == nil {
		t.Fatal("--vpn ran netbird expose")
	}
	if code != 0 || string(body) != "hello" || !strings.Contains(stdout.String(), "only on the NetBird network") {
		t.Fatalf("code %d, body %q\n%s%s", code, body, stdout.String(), stderr.String())
	}
}
