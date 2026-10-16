package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeNetbirdAPI is NetBird's management API, with one peer and the
// reverse proxy's services.
type fakeNetbirdAPI struct {
	mu       sync.Mutex
	services map[string]netbirdService
	calls    []string
	domains  func(http.ResponseWriter) // GET /api/reverse-proxies/domains
	readOnly bool                      // the token may read, not make services
}

func (f *fakeNetbirdAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, r.Method+" "+r.URL.RequestURI())
	if r.Header.Get("Authorization") != "Token nbp_test" {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]string{"message": "token invalid"})
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/reverse-proxies/services/")
	switch {
	case r.URL.Path == "/api/peers":
		if r.URL.Query().Get("ip") == "100.64.0.7" {
			json.NewEncoder(w).Encode([]map[string]string{{"id": "peer-7"}})
		} else {
			json.NewEncoder(w).Encode([]any{})
		}
	case r.URL.Path == "/api/reverse-proxies/domains" && f.domains != nil:
		f.domains(w)
	case r.URL.Path == "/api/reverse-proxies/services" && r.Method == http.MethodGet:
		var all []netbirdService
		for _, service := range f.services {
			all = append(all, service)
		}
		json.NewEncoder(w).Encode(all)
	case r.URL.Path == "/api/reverse-proxies/services" && r.Method == http.MethodPost:
		var service netbirdService
		json.NewDecoder(r.Body).Decode(&service)
		if !strings.HasSuffix(service.Domain, ".apps.example.com") {
			w.WriteHeader(http.StatusUnprocessableEntity)
			json.NewEncoder(w).Encode(map[string]string{"message": "domain not found in account"})
			return
		}
		if len(service.Targets) == 0 {
			w.WriteHeader(http.StatusUnprocessableEntity)
			json.NewEncoder(w).Encode(map[string]string{"message": "at least one target is required"})
			return
		}
		if f.readOnly {
			w.WriteHeader(http.StatusForbidden)
			json.NewEncoder(w).Encode(map[string]string{"message": "permission denied"})
			return
		}
		service.ID = "svc-1"
		f.services[service.ID] = service
		json.NewEncoder(w).Encode(service)
	case r.Method == http.MethodGet:
		json.NewEncoder(w).Encode(f.services[id])
	case r.Method == http.MethodDelete:
		delete(f.services, id)
	case r.Method == http.MethodPut:
		var service netbirdService
		json.NewDecoder(r.Body).Decode(&service)
		service.ID = id
		f.services[id] = service
		json.NewEncoder(w).Encode(service)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func fakeNetbird(t *testing.T) *fakeNetbirdAPI {
	t.Helper()
	fake := &fakeNetbirdAPI{services: map[string]netbirdService{}}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	before := netbirdAPIURL
	netbirdAPIURL = func() (string, error) { return server.URL, nil }
	t.Cleanup(func() { netbirdAPIURL = before })
	t.Setenv("NETBIRD_API_TOKEN", "nbp_test")
	return fake
}

func TestNetExposePublish(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	fake := fakeNetbird(t)
	// The first exposure makes the service, pointed at this machine's relay.
	stopFirst, err := netExposePublish("payments.apps.example.com", "100.64.0.7", 41001)
	if err != nil {
		t.Fatal(err)
	}
	service := fake.services["svc-1"]
	if !service.Enabled || service.Domain != "payments.apps.example.com" || len(service.Targets) != 1 ||
		service.Targets[0] != (netbirdTarget{TargetID: "peer-7", TargetType: "peer", Protocol: "http", Path: "/", Port: 41001, Enabled: true}) {
		t.Fatalf("%+v", service)
	}
	// The next takes it over before the first ends: no gap, and the first
	// leaves it on.
	stopNext, err := netExposePublish("payments.apps.example.com", "100.64.0.7", 41002)
	if err != nil || len(fake.services) != 1 || fake.services["svc-1"].Targets[0].Port != 41002 {
		t.Fatal(err, fake.services)
	}
	stopFirst()
	if !fake.services["svc-1"].Enabled {
		t.Fatal("the first exposure switched off the next one's service")
	}
	stopNext()
	if fake.services["svc-1"].Enabled {
		t.Fatal("still on after its exposure ended")
	}

	// A domain NetBird doesn't have, and a token it refuses.
	if _, err := netExposePublish("payments.other.example.com", "100.64.0.7", 41003); err == nil || !strings.Contains(err.Error(), "Custom Domains") {
		t.Fatal(err)
	}
	t.Setenv("NETBIRD_API_TOKEN", "nbp_wrong")
	if _, err := netExposePublish("payments.apps.example.com", "100.64.0.7", 41003); !errors.Is(err, errNetbirdToken) {
		t.Fatal(err)
	}
	// Without a token at all.
	t.Setenv("NETBIRD_API_TOKEN", "")
	if _, err := netExposePublish("payments.apps.example.com", "100.64.0.7", 41003); err == nil || !strings.Contains(err.Error(), "hi team domain") {
		t.Fatal(err)
	}
	if err := saveNetbirdAPIToken("nbp_test\n"); err != nil {
		t.Fatal(err)
	}
	if token, err := netbirdAPIToken(); err != nil || token != "nbp_test" {
		t.Fatal(token, err)
	}
}

func TestParseNetExposeDomain(t *testing.T) {
	options, _, err := parseNetExpose([]string{"3000", "--link", "--domain", "Payments.Apps.Example.com", "--max", "720h"})
	if err != nil || options.Domain != "payments.apps.example.com" || options.Max != netExposeDomainMax {
		t.Fatal(options, err)
	}
	if _, _, err := parseNetExpose([]string{"3000", "--max", "48h"}); err == nil {
		t.Fatal("48 hours without --domain")
	}
	for _, domain := range []string{"localhost", "-x.example.com", "a_b.example.com", "https://x.example.com"} {
		if _, _, err := parseNetExpose([]string{"3000", "--domain", domain}); err == nil {
			t.Error(domain)
		}
	}
	if until := netExposeUntil(time.Now().Add(72 * time.Hour)); !strings.Contains(until, " ") {
		t.Error(until)
	}
}
