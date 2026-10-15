package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
)

// An exposure can add a script to the pages it relays and serve a path
// prefix from a socket instead of the service: --inject, --route, and
// --inject-if. hi team uses them to put its helper, a chat with the lead,
// into every page of a team's app whatever it is written in, without the
// app holding any of it (docs/specs/approved/hi_team_helper.md).

// netExposeInjectScan is how much of a page the relay reads looking for
// where the script goes; a page with no <head>, <body>, or <html> that far
// is passed on as it is.
const netExposeInjectScan = 64 << 10

// A person's own key to the routes: a link with hi_user=<key> leaves it in
// a cookie only the routes' paths get, and the relay hands it to the
// socket as X-Hi-User. The service never sees it, and the page's scripts
// can't read it (docs/specs/approved/hi_team_helper_signin.md).
const (
	netExposeUserParam  = "hi_user"
	netExposeUserCookie = "hi_user"
	netExposeUserHeader = "X-Hi-User"
	netExposeUserLasts  = 14 * 24 * time.Hour
)

var netExposeUserKey = regexp.MustCompile(`^[A-Za-z0-9_-]{20,100}$`)

// netExposeRoute serves the paths under Prefix from a socket.
type netExposeRoute struct {
	Prefix string `json:"prefix"`
	Socket string `json:"socket"`
}

// netExposeHelper is what an exposure adds to the service; nil adds
// nothing.
type netExposeHelper struct {
	Routes   []netExposeRoute
	Inject   string // the script's URL
	InjectIf string // inject and route only while this file exists; "" always
	log      func(format string, args ...any)
}

func newNetExposeHelper(options netExposeOptions, log func(string, ...any)) *netExposeHelper {
	if len(options.Routes) == 0 && options.Inject == "" {
		return nil
	}
	return &netExposeHelper{Routes: options.Routes, Inject: options.Inject, InjectIf: options.InjectIf, log: log}
}

// parseNetExposeRoute reads --route <prefix>=unix:<socket>.
func parseNetExposeRoute(text string) (netExposeRoute, error) {
	prefix, target, ok := strings.Cut(text, "=")
	socket, isUnix := strings.CutPrefix(target, "unix:")
	if !ok || !isUnix || socket == "" || !strings.HasPrefix(prefix, "/") || !strings.HasSuffix(prefix, "/") {
		return netExposeRoute{}, fmt.Errorf("--route takes /prefix/=unix:<socket>, not %q", text)
	}
	return netExposeRoute{Prefix: prefix, Socket: socket}, nil
}

// on says whether the helper is on now: --inject-if's file is read on
// every request, so turning it off takes effect on the next page.
func (h *netExposeHelper) on() bool {
	if h == nil {
		return false
	}
	if h.InjectIf == "" {
		return true
	}
	_, err := os.Stat(h.InjectIf)
	return err == nil
}

// signIn handles a link's hi_user: the key goes into a cookie for each
// route, and the browser on to the address without it. It says whether it
// answered the request.
func (h *netExposeHelper) signIn(w http.ResponseWriter, r *http.Request) bool {
	// Only the relay says who someone is, and only to the routes.
	r.Header.Del(netExposeUserHeader)
	if h == nil || len(h.Routes) == 0 {
		return false
	}
	if cookie, err := r.Cookie(netExposeUserCookie); err == nil && netExposeUserKey.MatchString(cookie.Value) {
		for _, route := range h.Routes {
			if strings.HasPrefix(r.URL.Path, route.Prefix) {
				r.Header.Set(netExposeUserHeader, cookie.Value)
			}
		}
	}
	netExposeDropCookie(r, netExposeUserCookie)
	query := r.URL.Query()
	if !query.Has(netExposeUserParam) {
		return false
	}
	if key := query.Get(netExposeUserParam); netExposeUserKey.MatchString(key) {
		for _, route := range h.Routes {
			http.SetCookie(w, &http.Cookie{Name: netExposeUserCookie, Value: key, Path: route.Prefix, MaxAge: int(netExposeUserLasts / time.Second),
				Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode})
		}
	}
	query.Del(netExposeUserParam)
	clean := *r.URL
	clean.RawQuery = query.Encode()
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		r.URL = &clean
		return false
	}
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, clean.RequestURI(), http.StatusSeeOther)
	return true
}

// route is the handler for a path under one of the routes, or nil.
func (h *netExposeHelper) route(path string) http.Handler {
	for _, route := range h.Routes {
		if strings.HasPrefix(path, route.Prefix) {
			socket := route.Socket
			upstream, _ := url.Parse("http://helper")
			return &httputil.ReverseProxy{
				Rewrite: func(out *httputil.ProxyRequest) {
					out.SetURL(upstream)
					out.SetXForwarded()
					// The app's cookies are the app's. The person's key, when
					// there is one, came out of them as X-Hi-User (signIn).
					out.Out.Header.Del("Cookie")
				},
				Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					return (&net.Dialer{}).DialContext(ctx, "unix", socket)
				}},
				ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
					http.Error(w, "hi net expose: nothing answers on "+socket, http.StatusBadGateway)
				},
				FlushInterval: -1,
			}
		}
	}
	return nil
}

// netExposePageLoad says whether a request is the browser loading a page,
// at the top: not a fragment the page fetches (htmx, Turbo, fetch), and not
// a page in an iframe.
func netExposePageLoad(r *http.Request) bool {
	return r.Method == http.MethodGet && r.Header.Get("Sec-Fetch-Dest") == "document"
}

// prepare readies a page load for the script: the service answers it
// uncompressed, so the relay can find where the script goes. ("identity"
// rather than no header, which Go's transport would fill with gzip.)
func (h *netExposeHelper) prepare(r *http.Request) {
	r.Header.Set("Accept-Encoding", "identity")
}

// modify adds the script to a page: right after <head>, else before
// <body>, else after <html> or the doctype.
func (h *netExposeHelper) modify(response *http.Response) error {
	if response.StatusCode != http.StatusOK || !strings.HasPrefix(strings.ToLower(response.Header.Get("Content-Type")), "text/html") {
		return nil
	}
	if encoding := response.Header.Get("Content-Encoding"); encoding != "" && encoding != "identity" {
		return nil // compressed anyway; left as it is
	}
	nonce := ""
	if policy := response.Header.Get("Content-Security-Policy"); policy != "" {
		var changed string
		changed, nonce = netExposeCSP(policy)
		response.Header.Set("Content-Security-Policy", changed)
	}
	tag := `<script src="` + htmlAttribute(h.Inject) + `" defer`
	if nonce != "" {
		tag += ` nonce="` + htmlAttribute(nonce) + `"`
	}
	tag += `></script>`
	path := response.Request.URL.Path
	response.Body = &netExposeInjector{source: response.Body, tag: []byte(tag), missed: func() {
		if h.log != nil {
			h.log("hi: no <head>, <body>, or <html> in the first %d KB of %s; it has no helper", netExposeInjectScan>>10, clipPath(path))
		}
	}}
	response.ContentLength = -1
	response.Header.Del("Content-Length")
	if etag := response.Header.Get("ETag"); etag != "" && !strings.HasPrefix(etag, "W/") {
		response.Header.Del("ETag")
	}
	return nil
}

func htmlAttribute(text string) string {
	return strings.NewReplacer("&", "&amp;", `"`, "&quot;", "<", "&lt;", ">", "&gt;").Replace(text)
}

// netExposeInjector streams a page with the script added. It holds the
// page back only until it knows where the script goes.
type netExposeInjector struct {
	source  io.ReadCloser
	tag     []byte
	missed  func()
	pending []byte // what is read but not yet passed on
	done    bool   // the script is in, or won't be: pass the rest through
	eof     bool
}

func (n *netExposeInjector) Read(p []byte) (int, error) {
	for !n.done {
		if at, ok := netExposeInjectAt(n.pending, n.eof || len(n.pending) >= netExposeInjectScan); ok {
			n.pending = append(n.pending[:at:at], append(append([]byte{}, n.tag...), n.pending[at:]...)...)
			n.done = true
			break
		}
		if n.eof || len(n.pending) >= netExposeInjectScan {
			if n.missed != nil {
				n.missed()
			}
			n.done = true
			break
		}
		buffer := make([]byte, 16<<10)
		read, err := n.source.Read(buffer)
		n.pending = append(n.pending, buffer[:read]...)
		if errors.Is(err, io.EOF) {
			n.eof = true
		} else if err != nil {
			return 0, err
		}
	}
	if len(n.pending) > 0 {
		copied := copy(p, n.pending)
		n.pending = n.pending[copied:]
		return copied, nil
	}
	if n.eof {
		return 0, io.EOF
	}
	return n.source.Read(p)
}

func (n *netExposeInjector) Close() error { return n.source.Close() }

var (
	netExposeHeadTag    = regexp.MustCompile(`(?i)<head(\s[^>]*)?>`)
	netExposeBodyTag    = regexp.MustCompile(`(?i)<body[\s>]`)
	netExposeHTMLTag    = regexp.MustCompile(`(?i)<html(\s[^>]*)?>`)
	netExposeDoctypeTag = regexp.MustCompile(`(?i)<!doctype[^>]*>`)
)

// netExposeInjectAt is where the script goes in the start of a page. Until
// final, only <head> and <body> count: <head> may still come after <html>.
func netExposeInjectAt(page []byte, final bool) (int, bool) {
	if match := netExposeHeadTag.FindIndex(page); match != nil {
		return match[1], true
	}
	if match := netExposeBodyTag.FindIndex(page); match != nil {
		return match[0], true
	}
	if !final {
		return 0, false
	}
	for _, tag := range []*regexp.Regexp{netExposeHTMLTag, netExposeDoctypeTag} {
		if match := tag.FindIndex(page); match != nil {
			return match[1], true
		}
	}
	return 0, false
}

// netExposeCSP lets a page's Content-Security-Policy load the script from
// the page's own address and reach its API there, and returns the policy's
// nonce for the script tag when it uses one.
func netExposeCSP(policy string) (string, string) {
	directives := strings.Split(policy, ";")
	names := map[string]int{}
	for i, directive := range directives {
		fields := strings.Fields(directive)
		if len(fields) > 0 {
			names[strings.ToLower(fields[0])] = i
		}
	}
	nonce := ""
	for _, name := range []string{"script-src-elem", "script-src", "default-src"} {
		if i, ok := names[name]; ok && nonce == "" {
			for _, source := range strings.Fields(directives[i]) {
				if value, ok := strings.CutPrefix(source, "'nonce-"); ok {
					nonce = strings.TrimSuffix(value, "'")
					break
				}
			}
		}
	}
	allow := func(i int) {
		fields := strings.Fields(directives[i])
		kept := fields[:1]
		for _, source := range fields[1:] {
			if source == "'self'" {
				return
			}
			if source != "'none'" {
				kept = append(kept, source)
			}
		}
		directives[i] = " " + strings.Join(append(kept, "'self'"), " ")
	}
	for _, name := range []string{"script-src", "script-src-elem", "connect-src"} {
		if i, ok := names[name]; ok {
			allow(i)
		}
	}
	// Without script-src or connect-src, default-src rules them.
	_, script := names["script-src"]
	_, connect := names["connect-src"]
	if i, ok := names["default-src"]; ok && (!script || !connect) {
		allow(i)
	}
	return strings.TrimSpace(strings.Join(directives, ";")), nonce
}
