package cli

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// A served box's front is hi, run read-only in a container on the box's
// network and on a normal one of its own: the only way in (hi_serve.md,
// How a served box runs). It passes requests to the box and answers as
// they come, so streamed tokens aren't held back.
//
// It listens twice. The first listener is published on 127.0.0.1 and asks
// for no key, as llama.cpp doesn't. The second, there only for a shared
// server, is published on the NetBird address and lets a request through
// only with one of the keys in its keys folder: "shared", the key handed
// over by hand, and "org", the org server's (hi_serve.md, Sharing). Each
// request gets an access line on stderr, which the journal keeps; the
// client's address doesn't reach the front through rootless Podman, so the
// line names the key (Findings 2).

// serveUserHeader is set by the org server, with the org's key, to the
// user it passes a request on for; the front writes it in the access line.
const serveUserHeader = "Hi-Serve-User"

func runServeFront(args []string, stderr io.Writer) int {
	usage := func() int {
		fmt.Fprintln(stderr, "usage: hi serve __front <listen> <upstream> [--shared <listen> --keys <folder>]")
		return 2
	}
	if len(args) != 2 && len(args) != 6 {
		return usage()
	}
	upstream, err := url.Parse(args[1])
	if err != nil || upstream.Scheme != "http" || upstream.Host == "" {
		fmt.Fprintf(stderr, "hi: invalid upstream %q\n", args[1])
		return 2
	}
	shared, keysDir := "", ""
	for i := 2; i < len(args); i += 2 {
		switch args[i] {
		case "--shared":
			shared = args[i+1]
		case "--keys":
			keysDir = args[i+1]
		default:
			return usage()
		}
	}
	if (shared == "") != (keysDir == "") {
		return usage()
	}
	log := &serveAccessLog{out: stderr}
	proxy := newServeFront(upstream, stderr)
	errs := make(chan error, 2)
	listen := func(address string, handler http.Handler) {
		server := &http.Server{Addr: address, Handler: handler, ReadHeaderTimeout: 30 * time.Second}
		errs <- server.ListenAndServe()
	}
	go listen(args[0], log.wrap(proxy, nil))
	fmt.Fprintf(stderr, "hi serve front: %s to %s\n", args[0], upstream)
	if shared != "" {
		keys := &serveKeys{dir: keysDir}
		if len(keys.load()) == 0 {
			fmt.Fprintf(stderr, "hi: no keys in %s\n", keysDir)
			return 1
		}
		go listen(shared, log.wrap(proxy, keys))
		fmt.Fprintf(stderr, "hi serve front: %s to %s, with a key\n", shared, upstream)
	}
	fmt.Fprintf(stderr, "hi: %v\n", <-errs)
	return 1
}

func newServeFront(upstream *url.URL, stderr io.Writer) http.Handler {
	proxy := httputil.NewSingleHostReverseProxy(upstream)
	proxy.FlushInterval = -1
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		// The box isn't listening yet while it starts.
		http.Error(w, "the server isn't answering yet", http.StatusBadGateway)
	}
	return proxy
}

// serveKeys are the keys a shared front takes, one per file, named by the
// file. They are read again every few seconds, so a new key works without
// a restart that would drop open connections.
type serveKeys struct {
	dir    string
	mu     sync.Mutex
	read   time.Time
	byName map[string]string
}

func (k *serveKeys) load() map[string]string {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.byName != nil && time.Since(k.read) < 3*time.Second {
		return k.byName
	}
	keys := map[string]string{}
	entries, _ := os.ReadDir(k.dir)
	for _, entry := range entries {
		if entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(k.dir, entry.Name()))
		if key := strings.TrimSpace(string(data)); err == nil && key != "" {
			keys[entry.Name()] = key
		}
	}
	k.byName, k.read = keys, time.Now()
	return keys
}

// match names the key a request carries, or "".
func (k *serveKeys) match(request *http.Request) string {
	given, ok := strings.CutPrefix(request.Header.Get("Authorization"), "Bearer ")
	if !ok {
		return ""
	}
	given = strings.TrimSpace(given)
	found := ""
	for name, key := range k.load() {
		if subtle.ConstantTimeCompare([]byte(given), []byte(key)) == 1 {
			found = name
		}
	}
	return found
}

type serveAccessLog struct {
	mu  sync.Mutex
	out io.Writer
}

// wrap checks the key, when keys is set, and writes the access line.
func (l *serveAccessLog) wrap(next http.Handler, keys *serveKeys) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		who := "local"
		recorder := &serveStatusWriter{ResponseWriter: w, status: http.StatusOK}
		// The line is written also when the client leaves halfway, which
		// the proxy ends by panicking with http.ErrAbortHandler.
		defer func() {
			aborted := recover()
			if keys != nil || r.URL.Path != "/health" { // hi's own checks, every few seconds
				cut := ""
				if aborted != nil {
					cut = " cut"
				}
				l.mu.Lock()
				fmt.Fprintf(l.out, "access %s %s %s %s %d %s%s\n", time.Now().UTC().Format(time.RFC3339), who, r.Method,
					cleanAccessWord(r.URL.Path), recorder.status, time.Since(started).Round(time.Millisecond), cut)
				l.mu.Unlock()
			}
			if aborted != nil {
				panic(aborted)
			}
		}()
		if keys != nil {
			who = keys.match(r)
			// Only the org server holds the org key, so only it can say who.
			user := r.Header.Get(serveUserHeader)
			r.Header.Del(serveUserHeader)
			switch {
			case who == "":
				who = "no-key"
				recorder.Header().Set("Content-Type", "application/json")
				recorder.WriteHeader(http.StatusUnauthorized)
				json.NewEncoder(recorder).Encode(map[string]any{"error": map[string]string{
					"message": "this server takes its key: Authorization: Bearer <key>; whoever serves it has it (hi serve key)"}})
			case who == "org" && user != "":
				who = "org:" + cleanAccessWord(user)
				fallthrough
			default:
				r.Header.Del("Authorization")
				next.ServeHTTP(recorder, r)
			}
		} else {
			next.ServeHTTP(recorder, r)
		}
	})
}

// cleanAccessWord keeps an access line one line of plain words.
func cleanAccessWord(word string) string {
	if len(word) > 200 {
		word = word[:200]
	}
	return strings.Map(func(r rune) rune {
		if r <= ' ' || r == 0x7f {
			return '_'
		}
		return r
	}, word)
}

// serveStatusWriter keeps the status for the access line, and passes
// flushes on, so streamed answers aren't held back.
type serveStatusWriter struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (w *serveStatusWriter) WriteHeader(status int) {
	if !w.wrote {
		w.status, w.wrote = status, true
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *serveStatusWriter) Write(data []byte) (int, error) {
	w.wrote = true
	return w.ResponseWriter.Write(data)
}

func (w *serveStatusWriter) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *serveStatusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
