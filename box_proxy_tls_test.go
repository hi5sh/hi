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
	"io"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/miekg/dns"
)

// A dev box's DNS: an allowed name is the proxy, its SRV record's targets
// become reachable, and a name nobody allowed doesn't resolve.
func TestBoxDatabasesDNS(t *testing.T) {
	d, logged := newBoxDatabasesTest(t, "cluster0.abcd.mongodb.net\n")
	d.lookupSRV = func(_ context.Context, name string) ([]*net.SRV, error) {
		if name != "_mongodb._tcp.cluster0.abcd.mongodb.net" {
			t.Fatalf("looked up %s", name)
		}
		return []*net.SRV{{Target: "ac-1-shard-00-00.abcd.mongodb.net.", Port: 27017}, {Target: "ac-1-shard-00-01.abcd.mongodb.net.", Port: 27017}}, nil
	}
	d.lookupTXT = func(context.Context, string) ([]string, error) {
		return []string{"authSource=admin&replicaSet=atlas-1"}, nil
	}
	server := &dns.Server{PacketConn: boxTestPacketConn(t), Handler: d}
	go server.ActivateAndServe()
	defer server.Shutdown()
	ask := func(name string, kind uint16) *dns.Msg {
		query := new(dns.Msg)
		query.SetQuestion(dns.Fqdn(name), kind)
		reply, err := dns.Exchange(query, server.PacketConn.LocalAddr().String())
		if err != nil {
			t.Fatal(err)
		}
		return reply
	}
	if reply := ask("ac-1-shard-00-00.abcd.mongodb.net", dns.TypeA); reply.Rcode != dns.RcodeNameError {
		t.Fatal("a shard resolved before its cluster's SRV record named it")
	}
	reply := ask("_mongodb._tcp.cluster0.abcd.mongodb.net", dns.TypeSRV)
	if len(reply.Answer) != 2 || reply.Answer[0].(*dns.SRV).Target != "ac-1-shard-00-00.abcd.mongodb.net." {
		t.Fatal(reply)
	}
	if reply := ask("cluster0.abcd.mongodb.net", dns.TypeTXT); len(reply.Answer) != 1 || reply.Answer[0].(*dns.TXT).Txt[0] != "authSource=admin&replicaSet=atlas-1" {
		t.Fatal(reply)
	}
	reply = ask("AC-1-shard-00-01.abcd.mongodb.net", dns.TypeA)
	if len(reply.Answer) != 1 || !reply.Answer[0].(*dns.A).A.Equal(net.ParseIP("10.234.9.2")) {
		t.Fatal(reply)
	}
	if reply := ask("ac-1-shard-00-01.abcd.mongodb.net", dns.TypeAAAA); reply.Rcode != dns.RcodeSuccess || len(reply.Answer) != 0 {
		t.Fatal(reply)
	}
	for _, name := range []string{"secret-data.attacker.example", "abcd.mongodb.net", "mongodb.net"} {
		if reply := ask(name, dns.TypeA); reply.Rcode != dns.RcodeNameError {
			t.Errorf("%s resolved", name)
		}
	}
	if !strings.Contains(logged.String(), "dns-srv _mongodb._tcp.cluster0.abcd.mongodb.net") {
		t.Fatal(logged.String())
	}
}

// On MongoDB's port the proxy joins a TLS connection to the name its
// hello asks for, with TLS end to end: the client's certificate reaches
// the database, and a name nobody allowed reaches nothing.
func TestBoxDatabasesTLS(t *testing.T) {
	d, logged := newBoxDatabasesTest(t, "cluster0.abcd.mongodb.net\n")
	listener, _ := net.Listen("tcp", "127.0.0.1:0")
	defer listener.Close()
	serverCert, clientCert, pool := boxTestCertificates(t)
	databaseListener := tls.NewListener(boxTestListener(t), &tls.Config{Certificates: []tls.Certificate{serverCert}, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: pool})
	defer databaseListener.Close()
	signedIn := make(chan string, 1)
	go func() {
		conn, err := databaseListener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		tlsConn := conn.(*tls.Conn)
		if err := tlsConn.Handshake(); err != nil {
			signedIn <- "handshake: " + err.Error()
			return
		}
		signedIn <- tlsConn.ConnectionState().PeerCertificates[0].Subject.CommonName
		io.Copy(conn, conn) // an echo
	}()
	var mu sync.Mutex
	var dialed []string
	d.dial = func(ctx context.Context, network, address string) (net.Conn, error) {
		mu.Lock()
		dialed = append(dialed, address)
		mu.Unlock()
		return (&net.Dialer{}).DialContext(ctx, network, databaseListener.Addr().String())
	}
	go d.serveTLS(listener, "27017")

	// Through the proxy, as a driver connects to a shard its SRV record named.
	d.learn("ac-1-shard-00-00.abcd.mongodb.net.")
	conn, err := tls.Dial("tcp", listener.Addr().String(), &tls.Config{ServerName: "ac-1-shard-00-00.abcd.mongodb.net", RootCAs: pool, Certificates: []tls.Certificate{clientCert}})
	if err != nil {
		t.Fatal(err)
	}
	if who := <-signedIn; who != "app-user" {
		t.Fatal(who)
	}
	conn.Write([]byte("ping"))
	got := make([]byte, 4)
	io.ReadFull(conn, got)
	conn.Close()
	mu.Lock()
	first := strings.Join(dialed, ",")
	mu.Unlock()
	if string(got) != "ping" || first != "ac-1-shard-00-00.abcd.mongodb.net:27017" {
		t.Fatal(string(got), dialed)
	}
	// A name nobody allowed, and a connection without TLS.
	if conn, err := tls.Dial("tcp", listener.Addr().String(), &tls.Config{ServerName: "elsewhere.example", InsecureSkipVerify: true}); err == nil {
		conn.Close()
		t.Fatal("reached a name nobody allowed")
	}
	plain, _ := net.Dial("tcp", listener.Addr().String())
	plain.Write([]byte("GET / HTTP/1.0\r\n\r\n"))
	plain.SetReadDeadline(time.Now().Add(5 * time.Second))
	if n, _ := plain.Read(make([]byte, 10)); n != 0 {
		t.Fatal("answered without TLS")
	}
	plain.Close()
	time.Sleep(100 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if len(dialed) != 1 || !strings.Contains(logged.String(), "allowed ac-1-shard-00-00.abcd.mongodb.net:27017") || !strings.Contains(logged.String(), "blocked elsewhere.example:27017") {
		t.Fatal(dialed, logged.String())
	}
}

func newBoxDatabasesTest(t *testing.T, allowed string) (*boxDatabases, *boxTestSyncWriter) {
	t.Helper()
	file := filepath.Join(t.TempDir(), "allow")
	os.WriteFile(file, []byte(allowed), 0o600)
	logged := &boxTestSyncWriter{}
	log := &boxNetworkLog{file: logged}
	d := newBoxDatabases(&boxAllowlist{path: file}, net.ParseIP("10.234.9.2"), log, nil)
	return d, logged
}

// boxTestSyncWriter is a log the proxy's goroutines write while the test
// reads it.
type boxTestSyncWriter struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (w *boxTestSyncWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buffer.Write(p)
}

func (w *boxTestSyncWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buffer.String()
}

func boxTestPacketConn(t *testing.T) net.PacketConn {
	t.Helper()
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	return conn
}

func boxTestListener(t *testing.T) net.Listener {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	return listener
}

// boxTestCertificates are a database's certificate and a client's, from
// one CA, as Atlas's X.509 users have.
func boxTestCertificates(t *testing.T) (server, client tls.Certificate, pool *x509.CertPool) {
	t.Helper()
	caKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	caTemplate := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test CA"}, NotBefore: time.Now().Add(-time.Hour),
		NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	ca, _ := x509.ParseCertificate(caDER)
	pool = x509.NewCertPool()
	pool.AddCert(ca)
	issue := func(serial int64, name string, usage x509.ExtKeyUsage) tls.Certificate {
		key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		template := &x509.Certificate{SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: name}, DNSNames: []string{name},
			NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), ExtKeyUsage: []x509.ExtKeyUsage{usage}}
		der, err := x509.CreateCertificate(rand.Reader, template, ca, &key.PublicKey, caKey)
		if err != nil {
			t.Fatal(err)
		}
		return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
	}
	return issue(2, "ac-1-shard-00-00.abcd.mongodb.net", x509.ExtKeyUsageServerAuth), issue(3, "app-user", x509.ExtKeyUsageClientAuth), pool
}
