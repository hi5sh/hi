package main

// Sign-in with an organisation's OpenID Connect provider, in the relay,
// beside the link's key (docs/specs/approved/hi_team_sso.md). --sso names
// a file with the provider and the rules, which hi team sso writes; the
// relay reads it again when it changes, and no file is sign-in off. The
// provider sends people back to the name that stays (--domain), so without
// one there is only the key.

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

const (
	netExposeSSOCallback     = "/__hi/auth/callback"
	netExposeSSOCookie       = "hi_sso"
	netExposeSSOStartCookie  = "hi_sso_start"
	netExposeSSOLasts        = 14 * 24 * time.Hour
	netExposeSSOStartLasts   = 10 * time.Minute
	netExposeSSODefaultClaim = "groups"
)

// netExposeSSOConfig is the --sso file: the provider, as the machine has
// it, and the team's rules. Every rule given has to hold.
type netExposeSSOConfig struct {
	Provider     string   `json:"provider"` // its name on the machine
	Issuer       string   `json:"issuer"`
	ClientID     string   `json:"client_id"`
	ClientSecret string   `json:"client_secret"`
	GroupsClaim  string   `json:"groups_claim,omitempty"`
	Domains      []string `json:"domains,omitempty"`
	Groups       []string `json:"groups,omitempty"`
	Anyone       bool     `json:"anyone,omitempty"`
	// Key signs the sessions; a new one ends them all.
	Key string `json:"key"`
	// Log gets a JSON line for each sign-in and each refusal.
	Log string `json:"log,omitempty"`
}

// netExposeSSOGoogle says the issuer is Google's, the one issuer for every
// Google account, where the Workspace is the hd claim.
func netExposeSSOGoogle(issuer string) bool {
	return strings.TrimSuffix(issuer, "/") == "https://accounts.google.com"
}

// netExposeSSO is a relay's sign-in. A nil one is off.
type netExposeSSO struct {
	file string
	host string // the name that stays
	log  func(format string, args ...any)

	mu       sync.Mutex
	modified time.Time
	size     int64
	config   *netExposeSSOConfig
	provider *oidc.Provider
}

func newNetExposeSSO(file, host string, log func(string, ...any)) *netExposeSSO {
	if file == "" || host == "" {
		return nil
	}
	return &netExposeSSO{file: file, host: host, log: log}
}

// current is the file's settings now, or nil while sign-in is off.
func (s *netExposeSSO) current() *netExposeSSOConfig {
	if s == nil {
		return nil
	}
	info, err := os.Stat(s.file)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		s.config, s.provider, s.modified = nil, nil, time.Time{}
		return nil
	}
	if info.ModTime().Equal(s.modified) && info.Size() == s.size {
		return s.config
	}
	var config netExposeSSOConfig
	data, err := os.ReadFile(s.file)
	if err != nil || json.Unmarshal(data, &config) != nil || config.Issuer == "" || config.ClientID == "" || len(config.Key) < 32 {
		s.config = nil // half written, or broken: the key only
		return nil
	}
	if s.config == nil || s.config.Issuer != config.Issuer || s.config.ClientID != config.ClientID {
		s.provider = nil
	}
	s.config, s.modified, s.size = &config, info.ModTime(), info.Size()
	return s.config
}

// oauth is the provider's endpoints and the ID token's checks, found from
// its discovery document the first time they're needed.
func (s *netExposeSSO) oauth(ctx context.Context, config *netExposeSSOConfig) (oauth2.Config, *oidc.IDTokenVerifier, error) {
	s.mu.Lock()
	provider := s.provider
	s.mu.Unlock()
	if provider == nil {
		found, err := oidc.NewProvider(ctx, config.Issuer)
		if err != nil {
			return oauth2.Config{}, nil, err
		}
		s.mu.Lock()
		s.provider, provider = found, found
		s.mu.Unlock()
	}
	scopes := []string{oidc.ScopeOpenID, "email", "profile"}
	return oauth2.Config{ClientID: config.ClientID, ClientSecret: config.ClientSecret, Endpoint: provider.Endpoint(),
			RedirectURL: "https://" + s.host + netExposeSSOCallback, Scopes: scopes},
		provider.Verifier(&oidc.Config{ClientID: config.ClientID}), nil
}

// netExposeSSOSession is the hi_sso cookie: who signed in, for which
// address, until when.
type netExposeSSOSession struct {
	Issuer  string `json:"iss"`
	Subject string `json:"sub"`
	Email   string `json:"email,omitempty"`
	Host    string `json:"host"`
	Expires int64  `json:"exp"`
}

// netExposeSSOStart is the hi_sso_start cookie, from the way out to the
// provider until it sends the browser back.
type netExposeSSOStart struct {
	State    string `json:"state"`
	Verifier string `json:"verifier"`
	Nonce    string `json:"nonce"`
	Page     string `json:"page"`
	Expires  int64  `json:"exp"`
}

// netExposeSSOSign makes a cookie's value: the JSON, and its HMAC with the
// team's key. kind keeps one cookie from passing for the other.
func netExposeSSOSign(key, kind string, value any) string {
	data, _ := json.Marshal(value)
	payload := base64.RawURLEncoding.EncodeToString(data)
	mac := hmac.New(sha256.New, []byte(key))
	io.WriteString(mac, kind+"."+payload)
	return payload + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// netExposeSSOOpen reads a value netExposeSSOSign made with the same key.
func netExposeSSOOpen(key, kind, cookie string, value any) bool {
	payload, sum, ok := strings.Cut(cookie, ".")
	if !ok {
		return false
	}
	given, err := base64.RawURLEncoding.DecodeString(sum)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(key))
	io.WriteString(mac, kind+"."+payload)
	if !hmac.Equal(given, mac.Sum(nil)) {
		return false
	}
	data, err := base64.RawURLEncoding.DecodeString(payload)
	return err == nil && json.Unmarshal(data, value) == nil
}

func netExposeSSORandom() string {
	value := make([]byte, 24)
	rand.Read(value)
	return base64.RawURLEncoding.EncodeToString(value)
}

// signedIn says the request has a session for this address, under the
// team's rules as they are now.
func (s *netExposeSSO) signedIn(r *http.Request) bool {
	config := s.current()
	if config == nil {
		return false
	}
	cookie, err := r.Cookie(netExposeSSOCookie)
	if err != nil {
		return false
	}
	var session netExposeSSOSession
	return netExposeSSOOpen(config.Key, "session", cookie.Value, &session) &&
		session.Host == s.host && session.Issuer == config.Issuer && time.Now().Unix() < session.Expires
}

// start sends a page load with no key and no session to the provider, and
// says whether it did.
func (s *netExposeSSO) start(w http.ResponseWriter, r *http.Request) bool {
	config := s.current()
	if config == nil || !netExposePageLoad(r) {
		return false
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	conf, _, err := s.oauth(ctx, config)
	if err != nil {
		s.log("hi: sign-in with %s: %v", config.Provider, err)
		netExposeSSOPage(w, http.StatusBadGateway, "Sign-in isn't working", "hi couldn't reach the sign-in ("+config.Provider+") just now. Try again in a minute, or open the page with the whole link you were sent.")
		return true
	}
	start := netExposeSSOStart{State: netExposeSSORandom(), Verifier: oauth2.GenerateVerifier(), Nonce: netExposeSSORandom(),
		Page: r.URL.RequestURI(), Expires: time.Now().Add(netExposeSSOStartLasts).Unix()}
	http.SetCookie(w, &http.Cookie{Name: netExposeSSOStartCookie, Value: netExposeSSOSign(config.Key, "start", start), Path: netExposeSSOCallback,
		MaxAge: int(netExposeSSOStartLasts.Seconds()), Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	options := []oauth2.AuthCodeOption{oauth2.S256ChallengeOption(start.Verifier), oidc.Nonce(start.Nonce)}
	if netExposeSSOGoogle(config.Issuer) {
		// A hint for Google's account chooser; hd in the token is the rule.
		hint := "*"
		if len(config.Domains) == 1 {
			hint = config.Domains[0]
		}
		options = append(options, oauth2.SetAuthURLParam("hd", hint))
	}
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, conf.AuthCodeURL(start.State, options...), http.StatusFound)
	return true
}

// callback is the provider sending the browser back: it says whether the
// request was that, and answered it.
func (s *netExposeSSO) callback(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.Path != netExposeSSOCallback {
		return false
	}
	config := s.current()
	if config == nil {
		return false
	}
	again := "Open the page again to sign in once more, or with the whole link you were sent."
	var start netExposeSSOStart
	cookie, err := r.Cookie(netExposeSSOStartCookie)
	query := r.URL.Query()
	switch {
	case err != nil || !netExposeSSOOpen(config.Key, "start", cookie.Value, &start) || time.Now().Unix() >= start.Expires:
		netExposeSSOPage(w, http.StatusBadRequest, "Sign-in took too long", "This sign-in started more than ten minutes ago, or in another browser. "+again)
		return true
	case query.Get("error") != "":
		reason := firstNonEmpty(query.Get("error_description"), query.Get("error"))
		s.record(config, r, "refused", "", "the provider: "+reason)
		netExposeSSOPage(w, http.StatusForbidden, "Not signed in", config.Provider+" said: "+reason+". "+again)
		return true
	case query.Get("state") != start.State:
		netExposeSSOPage(w, http.StatusBadRequest, "Sign-in didn't match", "This answer from the sign-in belongs to another attempt. "+again)
		return true
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	conf, verifier, err := s.oauth(ctx, config)
	var token *oauth2.Token
	if err == nil {
		token, err = conf.Exchange(ctx, query.Get("code"), oauth2.VerifierOption(start.Verifier))
	}
	var id *oidc.IDToken
	if err == nil {
		raw, _ := token.Extra("id_token").(string)
		if raw == "" {
			err = errors.New("no ID token")
		} else if id, err = verifier.Verify(ctx, raw); err == nil && id.Nonce != start.Nonce {
			err = errors.New("the ID token is for another sign-in (nonce)")
		}
	}
	if err != nil {
		s.log("hi: sign-in with %s: %v", config.Provider, err)
		s.record(config, r, "refused", "", err.Error())
		netExposeSSOPage(w, http.StatusBadGateway, "Sign-in didn't work", "hi couldn't finish the sign-in with "+config.Provider+". "+again)
		return true
	}
	var claims map[string]any
	id.Claims(&claims)
	email, _ := claims["email"].(string)
	if reason := netExposeSSOAllow(config, claims); reason != "" {
		s.record(config, r, "refused", email, reason)
		who := firstNonEmpty(email, "This account")
		netExposeSSOPage(w, http.StatusForbidden, "Not let in", who+" signed in, but this app lets in only "+netExposeSSORules(config)+". Ask the team's owners, or open the page with the whole link you were sent.")
		return true
	}
	s.record(config, r, "in", email, "")
	session := netExposeSSOSession{Issuer: id.Issuer, Subject: id.Subject, Email: email, Host: s.host, Expires: time.Now().Add(netExposeSSOLasts).Unix()}
	http.SetCookie(w, &http.Cookie{Name: netExposeSSOCookie, Value: netExposeSSOSign(config.Key, "session", session), Path: "/",
		MaxAge: int(netExposeSSOLasts.Seconds()), Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	http.SetCookie(w, &http.Cookie{Name: netExposeSSOStartCookie, Path: netExposeSSOCallback, MaxAge: -1, Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	page := start.Page
	if !strings.HasPrefix(page, "/") || strings.HasPrefix(page, "//") || strings.HasPrefix(page, "/\\") {
		page = "/"
	}
	w.Header().Set("Referrer-Policy", "no-referrer")
	http.Redirect(w, r, page, http.StatusSeeOther)
	return true
}

// netExposeSSOAllow checks the ID token's claims against the rules, and
// says which failed, or "".
func netExposeSSOAllow(config *netExposeSSOConfig, claims map[string]any) string {
	if len(config.Domains) == 0 && len(config.Groups) == 0 && !config.Anyone {
		return "no rules" // a broken file lets nobody in
	}
	if len(config.Domains) > 0 {
		var domain string
		if netExposeSSOGoogle(config.Issuer) {
			// The email's domain isn't enough: hd says the account is the
			// Workspace's.
			domain, _ = claims["hd"].(string)
		} else if netExposeSSOTrue(claims["email_verified"]) {
			email, _ := claims["email"].(string)
			if at := strings.LastIndex(email, "@"); at >= 0 {
				domain = email[at+1:]
			}
		}
		if !slices.ContainsFunc(config.Domains, func(allowed string) bool { return strings.EqualFold(allowed, domain) }) {
			return "not on " + strings.Join(config.Domains, ", ")
		}
	}
	if len(config.Groups) > 0 {
		claim := firstNonEmpty(config.GroupsClaim, netExposeSSODefaultClaim)
		var groups []string
		switch value := claims[claim].(type) {
		case string:
			groups = []string{value}
		case []any:
			for _, group := range value {
				if text, ok := group.(string); ok {
					groups = append(groups, text)
				}
			}
		}
		if !slices.ContainsFunc(config.Groups, func(allowed string) bool { return slices.Contains(groups, allowed) }) {
			return "in none of the groups " + strings.Join(config.Groups, ", ")
		}
	}
	return ""
}

// netExposeSSOTrue reads email_verified, which some providers send as a
// string.
func netExposeSSOTrue(value any) bool {
	switch value := value.(type) {
	case bool:
		return value
	case string:
		return value == "true"
	}
	return false
}

// netExposeSSORules says who the rules let in.
func netExposeSSORules(config *netExposeSSOConfig) string {
	var rules []string
	if len(config.Domains) > 0 {
		rules = append(rules, "accounts on "+strings.Join(config.Domains, " or "))
	}
	if len(config.Groups) > 0 {
		rules = append(rules, "people in "+strings.Join(config.Groups, " or "))
	}
	if len(rules) == 0 {
		return "everyone " + config.Provider + " signs in"
	}
	return strings.Join(rules, " who are ")
}

// record adds a line to the team's sign-in log.
func (s *netExposeSSO) record(config *netExposeSSOConfig, r *http.Request, result, email, reason string) {
	if config.Log == "" {
		return
	}
	line, _ := json.Marshal(netExposeSSORecord{Time: time.Now().UTC(), Host: s.host, Result: result, Email: email, Reason: reason, Address: teamWebAddress(r)})
	file, err := os.OpenFile(config.Log, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer file.Close()
	file.Write(append(line, '\n'))
}

// netExposeSSORecord is a line of the sign-in log.
type netExposeSSORecord struct {
	Time    time.Time `json:"time"`
	Host    string    `json:"host"`
	Result  string    `json:"result"` // in, refused
	Email   string    `json:"email,omitempty"`
	Reason  string    `json:"reason,omitempty"`
	Address string    `json:"address,omitempty"`
}

// drop keeps the sign-in's cookies from the service, as the key's are.
func (s *netExposeSSO) drop(r *http.Request) {
	if s == nil {
		return
	}
	netExposeDropCookie(r, netExposeSSOCookie)
	netExposeDropCookie(r, netExposeSSOStartCookie)
}

func netExposeSSOPage(w http.ResponseWriter, status int, title, text string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	fmt.Fprintf(w, `<!doctype html><meta charset="utf-8"><meta name="viewport" content="width=device-width">
<title>%s</title><body style="font-family:system-ui,sans-serif;max-width:32rem;margin:4rem auto;padding:0 1rem">
<h1>%s</h1><p>%s</p></body>`, html.EscapeString(title), html.EscapeString(title), html.EscapeString(text))
}
