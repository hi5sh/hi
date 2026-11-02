package main

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"
)

// A served box's front is hi, run read-only in a container on the box's
// network and on a normal one of its own: the only way in (hi_serve.md,
// How a served box runs). It passes requests to the box and answers as
// they come, so streamed tokens aren't held back.

func runServeFront(args []string, stderr io.Writer) int {
	if len(args) != 2 {
		fmt.Fprintln(stderr, "usage: hi serve __front <listen> <upstream>")
		return 2
	}
	upstream, err := url.Parse(args[1])
	if err != nil || upstream.Scheme != "http" || upstream.Host == "" {
		fmt.Fprintf(stderr, "hi: invalid upstream %q\n", args[1])
		return 2
	}
	server := &http.Server{Addr: args[0], Handler: newServeFront(upstream, stderr), ReadHeaderTimeout: 30 * time.Second}
	fmt.Fprintf(stderr, "hi serve front: %s to %s\n", args[0], upstream)
	if err := server.ListenAndServe(); err != nil {
		fmt.Fprintf(stderr, "hi: %v\n", err)
		return 1
	}
	return 0
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
