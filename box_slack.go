package main

// A team's Slack tokens stay in its proxy, as its OpenRouter key does. The
// lead's box gets placeholders, and every https call it makes to Slack's
// Web API (the SDK's, Hermes' own posts and file downloads, hi's hooks)
// comes to the proxy as a CONNECT. For Slack's API hosts only, the proxy
// ends the TLS itself with a certificate from the team's own CA, puts the
// real token in place of the placeholder, and passes the request on to
// Slack. The CA may sign for slack.com and nothing else, and its key is in
// the proxy alone; the box trusts its certificate through SSL_CERT_FILE.
// Socket Mode's websocket carries a ticket rather than a token, so it
// stays a plain tunnel, as does every other host.

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/httputil"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	boxSlackBotPlacehold = "xoxb-" + boxClaudePlacehold
	boxSlackAppPlacehold = "xapp-" + boxClaudePlacehold
	// In the team's keys directory, which only its proxy mounts.
	boxSlackEnv    = "slack.env"
	boxSlackCACert = "slack-ca.pem"
	boxSlackCAKey  = "slack-ca-key.pem"
	// In the directory the lead's box mounts: the public roots and the CA.
	boxSlackBundle = "ca-bundle.pem"
	// boxSlackKeysEnv tells the proxy where the team's keys are.
	boxSlackKeysEnv = "HI_BOX_SLACK_KEYS"
	// boxSlackDomain is all the CA may sign for.
	boxSlackDomain = "slack.com"
)

// boxSlackHosts are the hosts whose requests carry a token.
var boxSlackHosts = map[string]bool{"slack.com": true, "files.slack.com": true}

// boxSystemCABundles are where Linux distributions keep the public roots.
var boxSystemCABundles = []string{"/etc/ssl/certs/ca-certificates.crt", "/etc/pki/tls/certs/ca-bundle.crt", "/etc/ssl/cert.pem"}

// prepareTeamSlackSwap puts the team's Slack tokens and its CA in keysDir,
// where only the proxy reads them, and the bundle the lead's box trusts in
// trustDir. It returns false, and takes the tokens away again, when this
// machine has no bundle of public roots to add the CA to: the box then
// gets the real tokens, as before.
func prepareTeamSlackSwap(keysDir, trustDir string, secrets teamSecrets) (bool, error) {
	roots, err := readSystemCABundle()
	if err != nil || secrets.SlackBotToken == "" || secrets.SlackAppToken == "" {
		for _, file := range []string{filepath.Join(keysDir, boxSlackEnv), filepath.Join(trustDir, boxSlackBundle)} {
			if err := os.Remove(file); err != nil && !os.IsNotExist(err) {
				return false, err
			}
		}
		return false, nil
	}
	for _, dir := range []string{keysDir, trustDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return false, err
		}
	}
	ca, err := ensureBoxSlackCA(keysDir)
	if err != nil {
		return false, err
	}
	env := "SLACK_BOT_TOKEN=" + secrets.SlackBotToken + "\nSLACK_APP_TOKEN=" + secrets.SlackAppToken + "\n"
	if err := os.WriteFile(filepath.Join(keysDir, boxSlackEnv), []byte(env), 0o600); err != nil {
		return false, err
	}
	bundle := append(append(roots, '\n'), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.Leaf.Raw})...)
	if err := os.WriteFile(filepath.Join(trustDir, boxSlackBundle), bundle, 0o600); err != nil {
		return false, err
	}
	return true, nil
}

func readSystemCABundle() ([]byte, error) {
	for _, path := range boxSystemCABundles {
		if data, err := os.ReadFile(path); err == nil && len(data) > 0 {
			return data, nil
		}
	}
	return nil, errors.New("no CA bundle on this machine")
}

// ensureBoxSlackCA loads the team's CA, or makes a new one when there is
// none or it ends within a month; the box gets the new one on the same
// start, so nothing it trusts goes stale.
func ensureBoxSlackCA(dir string) (tls.Certificate, error) {
	if ca, err := loadBoxSlackCA(dir); err == nil && time.Until(ca.Leaf.NotAfter) > 30*24*time.Hour {
		return ca, nil
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		return tls.Certificate{}, err
	}
	_, allIPv4, _ := net.ParseCIDR("0.0.0.0/0")
	_, allIPv6, _ := net.ParseCIDR("::/0")
	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "hi box Slack proxy"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(2, 0, 0),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
		// Even with its key, the CA signs for nothing but Slack.
		PermittedDNSDomainsCritical: true,
		PermittedDNSDomains:         []string{boxSlackDomain},
		ExcludedIPRanges:            []*net.IPNet{allIPv4, allIPv6},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return tls.Certificate{}, err
	}
	if err := os.WriteFile(filepath.Join(dir, boxSlackCAKey), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		return tls.Certificate{}, err
	}
	if err := os.WriteFile(filepath.Join(dir, boxSlackCACert), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		return tls.Certificate{}, err
	}
	return loadBoxSlackCA(dir)
}

func loadBoxSlackCA(dir string) (tls.Certificate, error) {
	ca, err := tls.LoadX509KeyPair(filepath.Join(dir, boxSlackCACert), filepath.Join(dir, boxSlackCAKey))
	if err != nil {
		return tls.Certificate{}, err
	}
	if ca.Leaf == nil {
		if ca.Leaf, err = x509.ParseCertificate(ca.Certificate[0]); err != nil {
			return tls.Certificate{}, err
		}
	}
	if !ca.Leaf.IsCA || len(ca.Leaf.PermittedDNSDomains) != 1 || ca.Leaf.PermittedDNSDomains[0] != boxSlackDomain {
		return tls.Certificate{}, errors.New("the Slack CA isn't limited to " + boxSlackDomain)
	}
	return ca, nil
}

// boxSlackSwap ends the TLS of the box's CONNECTs to Slack's API hosts and
// passes each request on with the real token.
type boxSlackSwap struct {
	ca  tls.Certificate
	env string // the team's slack.env, read again for each request
	log *boxNetworkLog
	// upstream sends the request on to Slack; tests replace it.
	upstream http.RoundTripper

	mu    sync.Mutex
	leafs map[string]*tls.Certificate
}

// loadBoxSlackSwap reads the team's keys in dir; with no Slack tokens
// there, the box has the real ones, and there is nothing to swap.
func loadBoxSlackSwap(dir string, dial func(ctx context.Context, network, address string) (net.Conn, error), networkLog *boxNetworkLog) (*boxSlackSwap, error) {
	env := filepath.Join(dir, boxSlackEnv)
	if !fileExists(env) {
		return nil, nil
	}
	ca, err := loadBoxSlackCA(dir)
	if err != nil {
		return nil, err
	}
	upstream := &http.Transport{DialContext: dial, Proxy: nil, ForceAttemptHTTP2: true, TLSHandshakeTimeout: 15 * time.Second}
	return &boxSlackSwap{ca: ca, env: env, log: networkLog, upstream: upstream, leafs: map[string]*tls.Certificate{}}, nil
}

// intercepts is whether a CONNECT to address ends here.
func (s *boxSlackSwap) intercepts(address string) bool {
	host, port, err := net.SplitHostPort(address)
	return s != nil && err == nil && port == "443" && boxSlackHosts[strings.ToLower(host)]
}

// certificate is the proxy's certificate for host, made for a week and
// kept until a day before it ends.
func (s *boxSlackSwap) certificate(host string) (*tls.Certificate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if leaf, ok := s.leafs[host]; ok && time.Until(leaf.Leaf.NotAfter) > 24*time.Hour {
		return leaf, nil
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		return nil, err
	}
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: host},
		DNSNames:     []string{host},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(7 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, s.ca.Leaf, &key.PublicKey, s.ca.PrivateKey)
	if err != nil {
		return nil, err
	}
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	leaf := &tls.Certificate{Certificate: [][]byte{der, s.ca.Leaf.Raw}, PrivateKey: key, Leaf: parsed}
	s.leafs[host] = leaf
	return leaf, nil
}

// serve speaks TLS as host to the box on a hijacked CONNECT, and HTTP/1.1
// inside it, until the box hangs up.
func (s *boxSlackSwap) serve(client net.Conn, host string) {
	host = strings.ToLower(host)
	conn := tls.Server(client, &tls.Config{
		MinVersion: tls.VersionTLS12,
		NextProtos: []string{"http/1.1"},
		GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
			// The name in the handshake is the one the box asked the
			// proxy for, or there is no certificate.
			if name := strings.ToLower(hello.ServerName); name != "" && name != host {
				return nil, fmt.Errorf("the box asked for %s and greeted %s", host, name)
			}
			return s.certificate(host)
		},
	})
	server := &http.Server{Handler: s.handler(host), ReadHeaderTimeout: time.Minute, ErrorLog: log.New(io.Discard, "", 0)}
	server.Serve(&boxOneConnListener{conn: conn})
}

func (s *boxSlackSwap) handler(host string) http.Handler {
	return &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.Out.URL.Scheme, r.Out.URL.Host, r.Out.Host = "https", host, host
			s.swap(r.Out)
		},
		Transport:     s.upstream,
		FlushInterval: -1,
		ErrorLog:      log.New(io.Discard, "", 0),
	}
}

// swap puts the real token in place of a placeholder; any other
// Authorization goes on as it is.
func (s *boxSlackSwap) swap(r *http.Request) {
	auth, name := r.Header.Get("Authorization"), ""
	switch {
	case strings.Contains(auth, boxSlackBotPlacehold):
		name = "SLACK_BOT_TOKEN"
	case strings.Contains(auth, boxSlackAppPlacehold):
		name = "SLACK_APP_TOKEN"
	}
	if name != "" {
		token, err := readDotEnvValue(s.env, name)
		if err != nil {
			// The request goes on with the placeholder and fails; the log
			// says why.
			s.log.record("slack-token-problem", err.Error())
		} else {
			r.Header.Set("Authorization", "Bearer "+token)
		}
	}
	s.log.record("slack", r.URL.Host+r.URL.Path)
}

// boxOneConnListener hands http.Server one connection, then says it's
// closed; the connection is served until it ends all the same.
type boxOneConnListener struct {
	conn net.Conn
	once sync.Once
}

func (l *boxOneConnListener) Accept() (net.Conn, error) {
	var conn net.Conn
	l.once.Do(func() { conn = l.conn })
	if conn == nil {
		return nil, net.ErrClosed
	}
	return conn, nil
}

func (l *boxOneConnListener) Close() error   { return nil }
func (l *boxOneConnListener) Addr() net.Addr { return l.conn.LocalAddr() }

// boxBufferedConn is a hijacked connection whose first bytes are already
// in the server's buffer.
type boxBufferedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *boxBufferedConn) Read(p []byte) (int, error) { return c.reader.Read(p) }
