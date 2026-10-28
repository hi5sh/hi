package main

// A team's dev box reaches a database on the internet that speaks TLS,
// such as MongoDB Atlas, through its proxy as well, since a database's
// driver doesn't speak to an HTTP proxy. The proxy answers the box's DNS:
// a name the owners allowed is the proxy's own address, its SRV and TXT
// records (mongodb+srv://) are looked up for it, and a name nobody allowed
// doesn't resolve, so DNS carries nothing out of the box. On the
// database's port the proxy reads the name a connection's TLS hello asks
// for (SNI) and joins the connection to that name, when it's allowed or an
// allowed name's SRV record gave it. TLS runs end to end, so a client
// certificate signs in as it would directly.

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/miekg/dns"
)

const (
	// boxSelfEnv is the proxy's address on the box's network, which the
	// box's DNS gives an allowed name.
	boxSelfEnv = "HI_BOX_SELF"
	// boxTLSPortsEnv are the ports, comma-separated, on which the proxy
	// joins TLS connections to allowed names: MongoDB's.
	boxTLSPortsEnv = "HI_BOX_TLS_PORTS"
	// boxDatabasePorts are a dev box's.
	boxDatabasePorts = "27017"
	// boxLearnedFor is how long an SRV record's target stays allowed;
	// drivers look the record up again well before.
	boxLearnedFor = 24 * time.Hour
)

// boxDatabases are the box's DNS and its TLS ports.
type boxDatabases struct {
	allow *boxAllowlist
	self  net.IP
	log   *boxNetworkLog
	dial  func(ctx context.Context, network, address string) (net.Conn, error)

	mu      sync.Mutex
	learned map[string]time.Time // an allowed name's SRV targets, until

	// These are replaced in tests.
	lookupSRV func(ctx context.Context, name string) ([]*net.SRV, error)
	lookupTXT func(ctx context.Context, name string) ([]string, error)
}

func newBoxDatabases(allow *boxAllowlist, self net.IP, log *boxNetworkLog, dial func(ctx context.Context, network, address string) (net.Conn, error)) *boxDatabases {
	return &boxDatabases{allow: allow, self: self, log: log, dial: dial, learned: map[string]time.Time{},
		lookupSRV: func(ctx context.Context, name string) ([]*net.SRV, error) {
			_, records, err := net.DefaultResolver.LookupSRV(ctx, "", "", name)
			return records, err
		},
		lookupTXT: net.DefaultResolver.LookupTXT,
	}
}

// allows says whether the box may reach a name: allowed, or an allowed
// name's SRV target.
func (d *boxDatabases) allows(name string) bool {
	name = strings.ToLower(strings.TrimSuffix(name, "."))
	if d.allow.allows(name) {
		return true
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return time.Now().Before(d.learned[name])
}

func (d *boxDatabases) learn(name string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.learned[strings.ToLower(strings.TrimSuffix(name, "."))] = time.Now().Add(boxLearnedFor)
}

// ServeDNS answers the box's lookups.
func (d *boxDatabases) ServeDNS(w dns.ResponseWriter, r *dns.Msg) {
	reply := new(dns.Msg)
	reply.SetReply(r)
	defer w.WriteMsg(reply)
	if len(r.Question) != 1 {
		reply.Rcode = dns.RcodeRefused
		return
	}
	question := r.Question[0]
	name := strings.ToLower(strings.TrimSuffix(question.Name, "."))
	if !d.allows(name) {
		reply.Rcode = dns.RcodeNameError
		return
	}
	header := dns.RR_Header{Name: question.Name, Rrtype: question.Qtype, Class: dns.ClassINET, Ttl: 60}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	switch question.Qtype {
	case dns.TypeA:
		reply.Answer = append(reply.Answer, &dns.A{Hdr: header, A: d.self})
	case dns.TypeSRV:
		records, err := d.lookupSRV(ctx, name)
		if err != nil {
			reply.Rcode = dns.RcodeServerFailure
			return
		}
		for _, record := range records {
			d.learn(record.Target)
			reply.Answer = append(reply.Answer, &dns.SRV{Hdr: header, Priority: record.Priority, Weight: record.Weight,
				Port: record.Port, Target: dns.Fqdn(record.Target)})
		}
		d.log.record("dns-srv", name)
	case dns.TypeTXT:
		texts, err := d.lookupTXT(ctx, name)
		if err != nil {
			reply.Rcode = dns.RcodeServerFailure
			return
		}
		for _, text := range texts {
			reply.Answer = append(reply.Answer, &dns.TXT{Hdr: header, Txt: []string{text}})
		}
	}
	// Anything else, IPv6 too, has no answer: the box reaches the proxy
	// over IPv4.
}

// serveTLS joins the TLS connections on a port to the names they ask for.
func (d *boxDatabases) serveTLS(listener net.Listener, port string) error {
	for {
		conn, err := listener.Accept()
		if err != nil {
			return err
		}
		go d.join(conn, port)
	}
}

func (d *boxDatabases) join(client net.Conn, port string) {
	defer client.Close()
	client.SetReadDeadline(time.Now().Add(10 * time.Second))
	name, hello, err := boxReadServerName(client)
	client.SetReadDeadline(time.Time{})
	switch {
	case err != nil || name == "":
		d.log.record("blocked", "tls:"+port+" (no name in its hello)")
		return
	case !d.allows(name):
		d.log.record("blocked", name+":"+port)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	upstream, err := d.dial(ctx, "tcp", net.JoinHostPort(name, port))
	cancel()
	if err != nil {
		d.log.record("failed", name+":"+port)
		return
	}
	defer upstream.Close()
	d.log.record("allowed", name+":"+port)
	started := time.Now()
	if _, err := upstream.Write(hello); err != nil {
		return
	}
	up := make(chan int64, 1)
	go func() {
		n, _ := io.Copy(upstream, client)
		upstream.Close()
		up <- n
	}()
	down, _ := io.Copy(client, upstream)
	client.Close()
	d.log.closed(name, <-up+int64(len(hello)), down, time.Since(started))
}

var errBoxHelloRead = errors.New("hello read")

// boxReadServerName reads a TLS client hello for the name it asks for,
// and gives back the bytes it read, for the server.
func boxReadServerName(conn net.Conn) (string, []byte, error) {
	var read bytes.Buffer
	name := ""
	err := tls.Server(boxHelloConn{reader: io.TeeReader(conn, &read)}, &tls.Config{
		GetConfigForClient: func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
			name = hello.ServerName
			return nil, errBoxHelloRead
		},
	}).Handshake()
	if name == "" && !errors.Is(err, errBoxHelloRead) {
		return "", nil, err
	}
	return name, read.Bytes(), nil
}

// boxHelloConn lets crypto/tls read a hello and write nothing.
type boxHelloConn struct{ reader io.Reader }

func (c boxHelloConn) Read(p []byte) (int, error)     { return c.reader.Read(p) }
func (boxHelloConn) Write([]byte) (int, error)        { return 0, io.ErrClosedPipe }
func (boxHelloConn) Close() error                     { return nil }
func (boxHelloConn) LocalAddr() net.Addr              { return nil }
func (boxHelloConn) RemoteAddr() net.Addr             { return nil }
func (boxHelloConn) SetDeadline(time.Time) error      { return nil }
func (boxHelloConn) SetReadDeadline(time.Time) error  { return nil }
func (boxHelloConn) SetWriteDeadline(time.Time) error { return nil }

// startBoxDatabases starts the box's DNS, on port 53, and its TLS ports,
// when the proxy has them (a team's dev box). A port it can't have is in
// the log; the proxy carries on without it.
func startBoxDatabases(egress *boxEgressProxy) {
	ports := strings.Split(os.Getenv(boxTLSPortsEnv), ",")
	self := net.ParseIP(os.Getenv(boxSelfEnv))
	if ports[0] == "" || self == nil {
		return
	}
	d := newBoxDatabases(egress.allow, self, egress.log, egress.dialer())
	for _, network := range []string{"udp", "tcp"} {
		server := &dns.Server{Addr: ":53", Net: network, Handler: d}
		go func() {
			if err := server.ListenAndServe(); err != nil {
				egress.log.record("problem", "dns over "+network+": "+err.Error())
			}
		}()
	}
	for _, port := range ports {
		listener, err := net.Listen("tcp", ":"+port)
		if err != nil {
			egress.log.record("problem", "port "+port+": "+err.Error())
			continue
		}
		go func() {
			if err := d.serveTLS(listener, port); err != nil {
				egress.log.record("problem", "port "+port+": "+err.Error())
			}
		}()
	}
}
