package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
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
	made     int                       // services made, for their IDs
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
		f.made++
		service.ID = fmt.Sprintf("svc-%d", f.made)
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
	stopFirst, _, err := netExposePublish("payments.apps.example.com", "100.64.0.7", 41001, nil)
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
	stopNext, _, err := netExposePublish("payments.apps.example.com", "100.64.0.7", 41002, nil)
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
	if _, _, err := netExposePublish("payments.other.example.com", "100.64.0.7", 41003, nil); err == nil || !strings.Contains(err.Error(), "Custom Domains") {
		t.Fatal(err)
	}
	t.Setenv("NETBIRD_API_TOKEN", "nbp_wrong")
	if _, _, err := netExposePublish("payments.apps.example.com", "100.64.0.7", 41003, nil); !errors.Is(err, errNetbirdToken) {
		t.Fatal(err)
	}
	// Without a token at all.
	t.Setenv("NETBIRD_API_TOKEN", "")
	if _, _, err := netExposePublish("payments.apps.example.com", "100.64.0.7", 41003, nil); err == nil || !strings.Contains(err.Error(), "hi team domain") {
		t.Fatal(err)
	}
	if err := saveNetbirdAPIToken("nbp_test\n"); err != nil {
		t.Fatal(err)
	}
	if token, err := netbirdAPIToken(); err != nil || token != "nbp_test" {
		t.Fatal(token, err)
	}
}

func TestNetExposePublishMakesADeletedServiceAgain(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	fake := fakeNetbird(t)
	stop, ensure, err := netExposePublish("payments.apps.example.com", "100.64.0.7", 41001, []string{"203.0.113.7/32"})
	if err != nil {
		t.Fatal(err)
	}
	if made, err := ensure(); made || err != nil || len(fake.services) != 1 {
		t.Fatal("made a service that was there", made, err)
	}
	// Someone deletes it in NetBird's dashboard: the next check makes it
	// again, pointed here, with the --allow-ip rule.
	delete(fake.services, "svc-1")
	if made, err := ensure(); !made || err != nil {
		t.Fatal(made, err)
	}
	again, ok := fake.services["svc-2"]
	if !ok || !again.Enabled || again.Domain != "payments.apps.example.com" || again.Targets[0].Port != 41001 ||
		again.AccessRestrictions == nil || len(again.AccessRestrictions.AllowedCIDRs) != 1 {
		t.Fatalf("%+v", fake.services)
	}
	// stop switches off the service made again, not the deleted one.
	stop()
	if fake.services["svc-2"].Enabled {
		t.Fatal("still on after its exposure ended")
	}
}

func TestNetExposePublishLeavesATakenOverServiceAlone(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	fake := fakeNetbird(t)
	_, ensureOld, err := netExposePublish("payments.apps.example.com", "100.64.0.7", 41001, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, ensureNew, err := netExposePublish("payments.apps.example.com", "100.64.0.7", 41002, nil)
	if err != nil {
		t.Fatal(err)
	}
	// The old relay sees the new one has it, and doesn't take it back.
	if made, err := ensureOld(); made || err != nil || fake.services["svc-1"].Targets[0].Port != 41002 {
		t.Fatal(made, err, fake.services)
	}
	// Once it's deleted, the old relay leaves it to the new one, which
	// makes it again, pointed at itself.
	delete(fake.services, "svc-1")
	if made, err := ensureOld(); made || err != nil || len(fake.services) != 0 {
		t.Fatal("the old relay made the service again", made, err)
	}
	if made, err := ensureNew(); !made || err != nil || fake.services["svc-2"].Targets[0].Port != 41002 {
		t.Fatal(made, err, fake.services)
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

func TestWaitForEveryProxy(t *testing.T) {
	// Two proxies for the name: one serves it, one has no certificate for
	// it yet, then gets one.
	serving := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusUnauthorized) }))
	defer serving.Close()
	var ready atomic.Bool
	late := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusUnauthorized) }))
	late.TLS = &tls.Config{GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
		if !ready.Load() {
			return nil, errors.New("no certificate yet")
		}
		return &serving.TLS.Certificates[0], nil
	}}
	late.StartTLS()
	defer late.Close()
	roots := x509.NewCertPool()
	roots.AddCert(serving.Certificate())
	addresses := map[string]string{"192.0.2.1": serving.Listener.Addr().String(), "192.0.2.2": late.Listener.Addr().String()}
	before := []any{netExposeLookup, netExposeProxyAddress, netExposeProxyRoots}
	netExposeLookup = func(context.Context, string) ([]string, error) { return []string{"192.0.2.1", "192.0.2.2"}, nil }
	netExposeProxyAddress = func(ip, _ string) string { return addresses[ip] }
	netExposeProxyRoots = roots
	t.Cleanup(func() {
		netExposeLookup = before[0].(func(context.Context, string) ([]string, error))
		netExposeProxyAddress = before[1].(func(string, string) string)
		netExposeProxyRoots = before[2].(*x509.CertPool)
	})

	// httptest's certificate is for example.com.
	if err := waitForEveryProxy("example.com", "443", time.Second); err == nil || !strings.Contains(err.Error(), "192.0.2.2") || strings.Contains(err.Error(), "192.0.2.1") {
		t.Fatal(err)
	}
	ready.Store(true)
	if err := waitForEveryProxy("example.com", "443", time.Second); err != nil {
		t.Fatal(err)
	}
}
