package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

// fakeOIDC is an OpenID Connect provider that signs everyone in as claims
// says, checking the PKCE verifier as a provider does.
type fakeOIDC struct {
	*httptest.Server
	key    *rsa.PrivateKey
	mu     sync.Mutex
	claims map[string]any
	codes  map[string]fakeOIDCCode
}

type fakeOIDCCode struct{ challenge, nonce, redirect string }

func newFakeOIDC(t *testing.T) *fakeOIDC {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeOIDC{key: key, codes: map[string]fakeOIDCCode{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"issuer": fake.URL, "authorization_endpoint": fake.URL + "/auth", "token_endpoint": fake.URL + "/token",
			"jwks_uri": fake.URL + "/keys", "response_types_supported": []string{"code"}, "code_challenge_methods_supported": []string{"S256"},
			"id_token_signing_alg_values_supported": []string{"RS256"}})
	})
	mux.HandleFunc("/keys", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, KeyID: "k1", Algorithm: "RS256", Use: "sig"}}})
	})
	mux.HandleFunc("/auth", func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		if query.Get("code_challenge_method") != "S256" || query.Get("nonce") == "" {
			http.Error(w, "no PKCE or nonce", http.StatusBadRequest)
			return
		}
		code := netExposeSSORandom()
		fake.mu.Lock()
		fake.codes[code] = fakeOIDCCode{challenge: query.Get("code_challenge"), nonce: query.Get("nonce"), redirect: query.Get("redirect_uri")}
		fake.mu.Unlock()
		http.Redirect(w, r, query.Get("redirect_uri")+"?"+url.Values{"code": {code}, "state": {query.Get("state")}}.Encode(), http.StatusFound)
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		fake.mu.Lock()
		code, ok := fake.codes[r.Form.Get("code")]
		delete(fake.codes, r.Form.Get("code"))
		claims := map[string]any{}
		for name, value := range fake.claims {
			claims[name] = value
		}
		fake.mu.Unlock()
		sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
		id, secret, _ := r.BasicAuth()
		if !ok || base64.RawURLEncoding.EncodeToString(sum[:]) != code.challenge || r.Form.Get("redirect_uri") != code.redirect || id != "hi-client" || secret != "s3cret" {
			http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
			return
		}
		signer, _ := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: jose.JSONWebKey{Key: key, KeyID: "k1"}}, nil)
		claims["iss"], claims["aud"], claims["nonce"] = fake.URL, "hi-client", code.nonce
		claims["iat"], claims["exp"] = time.Now().Unix(), time.Now().Add(time.Hour).Unix()
		raw, err := jwt.Signed(signer).Claims(claims).Serialize()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"access_token": "at", "token_type": "Bearer", "expires_in": 3600, "id_token": raw})
	})
	fake.Server = httptest.NewServer(mux)
	t.Cleanup(fake.Close)
	return fake
}

func (f *fakeOIDC) signInAs(claims map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.claims = claims
}

// ssoBrowser follows the relay and the provider by hand: the relay's
// cookies are Secure and its return address is the name that stays, which
// a test's http server isn't.
type ssoBrowser struct {
	t       *testing.T
	relay   string
	cookies map[string]string
}

func (b *ssoBrowser) get(address string, page bool) *http.Response {
	b.t.Helper()
	request, _ := http.NewRequest(http.MethodGet, address, nil)
	if page {
		request.Header.Set("Sec-Fetch-Dest", "document")
	}
	for name, value := range b.cookies {
		request.AddCookie(&http.Cookie{Name: name, Value: value})
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		b.t.Fatal(err)
	}
	for _, cookie := range response.Cookies() {
		if cookie.MaxAge < 0 {
			delete(b.cookies, cookie.Name)
		} else {
			b.cookies[cookie.Name] = cookie.Value
		}
	}
	return response
}

// signIn opens a page with no key, goes through the provider, and returns
// the relay's answer to the provider sending the browser back.
func (b *ssoBrowser) signIn(page string) *http.Response {
	b.t.Helper()
	out := b.get(b.relay+page, true)
	if out.StatusCode != http.StatusFound {
		b.t.Fatalf("a page load with no key: %d, not sent to sign in", out.StatusCode)
	}
	back := b.get(out.Header.Get("Location"), false)
	location, _ := url.Parse(back.Header.Get("Location"))
	if location.Host != "app.example.com" || location.Path != netExposeSSOCallback {
		b.t.Fatalf("the return address is %s", location)
	}
	return b.get(b.relay+location.RequestURI(), true)
}

func TestNetExposeSSO(t *testing.T) {
	provider := newFakeOIDC(t)
	var seen sync.Map
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen.Store("cookie", r.Header.Get("Cookie"))
		io.WriteString(w, "the app")
	}))
	defer app.Close()
	dir := t.TempDir()
	file, log := filepath.Join(dir, "sso.json"), filepath.Join(dir, "sso.log")
	write := func(config netExposeSSOConfig) {
		data, _ := json.Marshal(config)
		os.WriteFile(file, data, 0o600)
		// The relay notices a change by the file's time and size.
		later := time.Now().Add(time.Duration(len(data)) * time.Second)
		os.Chtimes(file, later, later)
	}
	config := netExposeSSOConfig{Provider: "acme", Issuer: provider.URL, ClientID: "hi-client", ClientSecret: "s3cret",
		Domains: []string{"example.com"}, Key: strings.Repeat("k", 32), Log: log}
	write(config)
	sso := newNetExposeSSO(file, "app.example.com", t.Logf)
	relay := httptest.NewServer(newNetExposeRelayTo(func() string { return app.URL }, "s3cret-key", time.Now().Add(time.Hour), io.Discard, nil, sso))
	defer relay.Close()
	browser := &ssoBrowser{t: t, relay: relay.URL, cookies: map[string]string{}}

	// Not a page load: no sign-in, only the key's 401.
	if response := browser.get(relay.URL+"/api", false); response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("a fetch with no key: %d", response.StatusCode)
	}
	// Someone on another domain is turned away, and it's logged.
	provider.signInAs(map[string]any{"sub": "u2", "email": "eve@elsewhere.com", "email_verified": true})
	if response := browser.signIn("/"); response.StatusCode != http.StatusForbidden {
		t.Fatalf("eve: %d", response.StatusCode)
	}
	// An unverified email on the domain is too.
	provider.signInAs(map[string]any{"sub": "u3", "email": "mallory@example.com", "email_verified": false})
	if response := browser.signIn("/"); response.StatusCode != http.StatusForbidden {
		t.Fatalf("mallory: %d", response.StatusCode)
	}
	// Someone on the domain gets in, back to the page they asked for.
	provider.signInAs(map[string]any{"sub": "u1", "email": "ada@example.com", "email_verified": true})
	response := browser.signIn("/reports?month=10")
	if response.StatusCode != http.StatusSeeOther || response.Header.Get("Location") != "/reports?month=10" || browser.cookies[netExposeSSOCookie] == "" {
		t.Fatalf("ada: %d to %q", response.StatusCode, response.Header.Get("Location"))
	}
	browser.cookies["theirs"] = "1"
	response = browser.get(relay.URL+"/reports?month=10", false)
	body, _ := io.ReadAll(response.Body)
	if response.StatusCode != http.StatusOK || string(body) != "the app" {
		t.Fatalf("signed in: %d %s", response.StatusCode, body)
	}
	if cookie, _ := seen.Load("cookie"); cookie != "theirs=1" {
		t.Fatalf("the app saw cookies %q", cookie)
	}
	// The log has the refusals and the sign-in.
	data, _ := os.ReadFile(log)
	if lines := strings.Split(strings.TrimSpace(string(data)), "\n"); len(lines) != 3 || !strings.Contains(lines[0], `"refused"`) ||
		!strings.Contains(lines[0], "eve@elsewhere.com") || !strings.Contains(lines[2], `"in"`) || !strings.Contains(lines[2], "ada@example.com") {
		t.Fatalf("log:\n%s", data)
	}
	// The callback without its start cookie, or with another state.
	if response := browser.get(relay.URL+netExposeSSOCallback+"?code=x&state=y", true); response.StatusCode != http.StatusBadRequest {
		t.Fatalf("a callback with no start: %d", response.StatusCode)
	}
	// New rules: a new key, and the session is gone.
	config.Key = strings.Repeat("n", 32)
	write(config)
	if response := browser.get(relay.URL+"/", false); response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("a session from before the new key: %d", response.StatusCode)
	}
	// The link's key still works beside sign-in.
	if response := browser.get(relay.URL+"/?hi_key=s3cret-key", true); response.StatusCode != http.StatusSeeOther {
		t.Fatalf("the key: %d", response.StatusCode)
	}
	// Off: no file, no sign-in, and the callback is the app's again.
	os.Remove(file)
	delete(browser.cookies, netExposeKeyCookie)
	if response := browser.get(relay.URL+"/", true); response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("off: %d", response.StatusCode)
	}
}

func TestNetExposeSSOAllow(t *testing.T) {
	google := &netExposeSSOConfig{Provider: "google", Issuer: "https://accounts.google.com", Domains: []string{"example.com"}}
	if netExposeSSOAllow(google, map[string]any{"hd": "example.com", "email": "a@example.com"}) != "" ||
		netExposeSSOAllow(google, map[string]any{"email": "a@example.com", "email_verified": true}) == "" {
		t.Fatal("Google goes by hd, not the email")
	}
	keycloak := &netExposeSSOConfig{Provider: "kc", Issuer: "https://kc.example.com/realms/acme", Groups: []string{"engineering"}}
	if netExposeSSOAllow(keycloak, map[string]any{"groups": []any{"sales", "engineering"}}) != "" ||
		netExposeSSOAllow(keycloak, map[string]any{"groups": []any{"sales"}}) == "" || netExposeSSOAllow(keycloak, map[string]any{}) == "" {
		t.Fatal("groups")
	}
	auth0 := &netExposeSSOConfig{Provider: "a0", Issuer: "https://acme.eu.auth0.com/", Groups: []string{"staff"}, GroupsClaim: "https://hi5.sh/groups",
		Domains: []string{"example.com"}}
	if netExposeSSOAllow(auth0, map[string]any{"https://hi5.sh/groups": "staff", "email": "a@Example.com", "email_verified": "true"}) != "" ||
		netExposeSSOAllow(auth0, map[string]any{"https://hi5.sh/groups": "staff", "email": "a@other.com", "email_verified": true}) == "" {
		t.Fatal("a namespaced claim, and both rules")
	}
	if netExposeSSOAllow(&netExposeSSOConfig{Anyone: true}, map[string]any{}) != "" || netExposeSSOAllow(&netExposeSSOConfig{}, map[string]any{}) == "" {
		t.Fatal("--anyone, and no rules")
	}
}

func TestNetExposeSSOCookies(t *testing.T) {
	value := netExposeSSOSign("key", "session", netExposeSSOSession{Subject: "u1"})
	var session netExposeSSOSession
	if !netExposeSSOOpen("key", "session", value, &session) || session.Subject != "u1" {
		t.Fatal("open")
	}
	var start netExposeSSOStart
	if netExposeSSOOpen("other", "session", value, &session) || netExposeSSOOpen("key", "start", value, &start) ||
		netExposeSSOOpen("key", "session", "x"+value, &session) {
		t.Fatal("another key, another kind, or changed")
	}
}

func TestParseNetExposeSSO(t *testing.T) {
	if _, _, err := parseNetExpose([]string{"3000", "--sso", "/tmp/sso.json"}); err == nil {
		t.Fatal("--sso without --link")
	}
	options, _, err := parseNetExpose([]string{"3000", "--link", "--domain", "app.example.com", "--sso", "/tmp/sso.json"})
	if err != nil || options.SSO != "/tmp/sso.json" {
		t.Fatal(options, err)
	}
	if newNetExposeSSO("/tmp/sso.json", "", nil) != nil {
		t.Fatal("sign-in with no name that stays")
	}
}
