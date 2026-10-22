package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type boxSlackTestUpstream struct {
	mu   sync.Mutex
	seen []string
}

func (u *boxSlackTestUpstream) RoundTrip(r *http.Request) (*http.Response, error) {
	u.mu.Lock()
	u.seen = append(u.seen, r.URL.String()+" "+r.Header.Get("Authorization"))
	u.mu.Unlock()
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"ok":true}`)), Request: r}, nil
}

func TestBoxSlackCAOnlySignsForSlack(t *testing.T) {
	dir := t.TempDir()
	ca, err := ensureBoxSlackCA(dir)
	if err != nil {
		t.Fatal(err)
	}
	if again, err := ensureBoxSlackCA(dir); err != nil || !bytes.Equal(again.Leaf.Raw, ca.Leaf.Raw) {
		t.Fatal("a second start made a new CA", err)
	}
	if info, _ := os.Stat(filepath.Join(dir, boxSlackCAKey)); info.Mode().Perm() != 0o600 {
		t.Fatalf("the CA's key is %v", info.Mode())
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca.Leaf)
	sign := func(names []string, ips []net.IP) *x509.Certificate {
		key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		template := &x509.Certificate{SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: "leaf"},
			DNSNames: names, IPAddresses: ips, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
			ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
		der, err := x509.CreateCertificate(rand.Reader, template, ca.Leaf, &key.PublicKey, ca.PrivateKey)
		if err != nil {
			t.Fatal(err)
		}
		leaf, _ := x509.ParseCertificate(der)
		return leaf
	}
	if _, err := sign([]string{"files.slack.com"}, nil).Verify(x509.VerifyOptions{Roots: roots, DNSName: "files.slack.com"}); err != nil {
		t.Fatal("a certificate for Slack doesn't verify:", err)
	}
	// With its key, the CA still can't vouch for anyone else.
	for name, leaf := range map[string]*x509.Certificate{
		"github.com":    sign([]string{"github.com"}, nil),
		"evilslack.com": sign([]string{"evilslack.com"}, nil),
		"10.0.0.1":      sign(nil, []net.IP{net.ParseIP("10.0.0.1")}),
	} {
		if _, err := leaf.Verify(x509.VerifyOptions{Roots: roots, DNSName: name}); err == nil {
			t.Errorf("the CA vouched for %s", name)
		}
	}
}

func TestBoxSlackSwap(t *testing.T) {
	dir := t.TempDir()
	ca, err := ensureBoxSlackCA(dir)
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, boxSlackEnv), []byte("SLACK_BOT_TOKEN=xoxb-real\nSLACK_APP_TOKEN=xapp-real\n"), 0o600)
	allowPath := filepath.Join(dir, "allow")
	os.WriteFile(allowPath, []byte("*\n"), 0o600)
	var logged bytes.Buffer
	networkLog := &boxNetworkLog{file: &logged}
	tunnelled := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "tunnelled") }))
	defer tunnelled.Close()
	dial := func(ctx context.Context, network, address string) (net.Conn, error) {
		return net.Dial("tcp", strings.TrimPrefix(tunnelled.URL, "http://"))
	}
	swap, err := loadBoxSlackSwap(dir, dial, networkLog)
	if err != nil || swap == nil {
		t.Fatal(swap, err)
	}
	upstream := &boxSlackTestUpstream{}
	swap.upstream = upstream
	server := httptest.NewServer(&boxEgressProxy{allow: &boxAllowlist{path: allowPath}, log: networkLog, dial: dial, slack: swap})
	defer server.Close()
	proxyURL, _ := url.Parse(server.URL)
	roots := x509.NewCertPool()
	roots.AddCert(ca.Leaf)
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL), TLSClientConfig: &tls.Config{RootCAs: roots}}}

	for _, call := range []struct{ url, auth, want string }{
		{"https://slack.com/api/chat.postMessage", "Bearer " + boxSlackBotPlacehold, "https://slack.com/api/chat.postMessage Bearer xoxb-real"},
		{"https://slack.com/api/apps.connections.open", "Bearer " + boxSlackAppPlacehold, "https://slack.com/api/apps.connections.open Bearer xapp-real"},
		{"https://files.slack.com/files-pri/T1-F1/a.png", "Bearer " + boxSlackBotPlacehold, "https://files.slack.com/files-pri/T1-F1/a.png Bearer xoxb-real"},
		// Anything but a placeholder goes on as it is.
		{"https://slack.com/api/auth.test", "Bearer xoxb-someone-elses", "https://slack.com/api/auth.test Bearer xoxb-someone-elses"},
	} {
		request, _ := http.NewRequest(http.MethodPost, call.url, strings.NewReader("{}"))
		request.Header.Set("Authorization", call.auth)
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(call.url, err)
		}
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if string(body) != `{"ok":true}` {
			t.Fatalf("%s: %q", call.url, body)
		}
		if got := upstream.seen[len(upstream.seen)-1]; got != call.want {
			t.Errorf("Slack saw %q, want %q", got, call.want)
		}
	}
	if !strings.Contains(logged.String(), "slack slack.com/api/chat.postMessage") || strings.Contains(logged.String(), "xoxb-real") {
		t.Fatalf("log:\n%s", logged.String())
	}

	// Socket Mode's websocket, and every other host, is a plain tunnel.
	for address, want := range map[string]bool{"slack.com:443": true, "Files.Slack.com:443": true, "wss-primary.slack.com:443": false, "slack.com:80": false, "hooks.slack.com:443": false, "example.com:443": false} {
		if got := swap.intercepts(address); got != want {
			t.Errorf("intercepts(%s) = %v", address, got)
		}
	}
	var none *boxSlackSwap
	if none.intercepts("slack.com:443") {
		t.Fatal("a proxy without Slack's keys intercepts")
	}

	// A handshake for another name than the CONNECT's gets no certificate.
	conn, _ := net.Dial("tcp", proxyURL.Host)
	fmt.Fprintf(conn, "CONNECT slack.com:443 HTTP/1.1\r\nHost: slack.com:443\r\n\r\n")
	status := make([]byte, len("HTTP/1.1 200 Connection established\r\n\r\n"))
	io.ReadFull(conn, status)
	if err := tls.Client(conn, &tls.Config{ServerName: "github.com", RootCAs: roots}).Handshake(); err == nil {
		t.Fatal("the proxy spoke TLS as github.com")
	}
	conn.Close()
}

func TestLoadBoxSlackSwapWithoutTokens(t *testing.T) {
	// A team whose box holds the real tokens: nothing to swap, so nothing
	// is intercepted.
	dir := t.TempDir()
	if _, err := ensureBoxSlackCA(dir); err != nil {
		t.Fatal(err)
	}
	if swap, err := loadBoxSlackSwap(dir, nil, &boxNetworkLog{file: io.Discard}); swap != nil || err != nil {
		t.Fatal(swap, err)
	}
}

func TestPrepareTeamSlackSwap(t *testing.T) {
	dir := t.TempDir()
	keys, trust := filepath.Join(dir, "keys"), filepath.Join(dir, "trust")
	roots := filepath.Join(dir, "roots.pem")
	os.WriteFile(roots, []byte("-----BEGIN CERTIFICATE-----\npublic roots\n-----END CERTIFICATE-----\n"), 0o644)
	defer func(old []string) { boxSystemCABundles = old }(boxSystemCABundles)
	boxSystemCABundles = []string{filepath.Join(dir, "missing.pem"), roots}
	secrets := teamSecrets{SlackBotToken: "xoxb-real", SlackAppToken: "xapp-real"}

	if ok, err := prepareTeamSlackSwap(keys, trust, secrets); !ok || err != nil {
		t.Fatal(ok, err)
	}
	if env, _ := os.ReadFile(filepath.Join(keys, boxSlackEnv)); string(env) != "SLACK_BOT_TOKEN=xoxb-real\nSLACK_APP_TOKEN=xapp-real\n" {
		t.Fatalf("slack.env: %q", env)
	}
	bundle, _ := os.ReadFile(filepath.Join(trust, boxSlackBundle))
	ca, _ := os.ReadFile(filepath.Join(keys, boxSlackCACert))
	if !strings.HasPrefix(string(bundle), "-----BEGIN CERTIFICATE-----\npublic roots") || !strings.HasSuffix(string(bundle), string(ca)) {
		t.Fatalf("bundle:\n%s", bundle)
	}
	// What the box can read holds no token and no key.
	entries, _ := os.ReadDir(trust)
	for _, entry := range entries {
		data, _ := os.ReadFile(filepath.Join(trust, entry.Name()))
		if strings.Contains(string(data), "xoxb-real") || strings.Contains(string(data), "PRIVATE KEY") {
			t.Fatalf("%s holds a secret", entry.Name())
		}
	}
	if block, _ := pem.Decode(ca); block == nil {
		t.Fatal("the CA isn't PEM")
	}

	// Without public roots to add to, the box keeps the real tokens, so
	// the proxy must not swap.
	boxSystemCABundles = []string{filepath.Join(dir, "missing.pem")}
	if ok, err := prepareTeamSlackSwap(keys, trust, secrets); ok || err != nil {
		t.Fatal(ok, err)
	}
	if fileExists(filepath.Join(keys, boxSlackEnv)) || fileExists(filepath.Join(trust, boxSlackBundle)) {
		t.Fatal("the swap's files are still there")
	}
}
