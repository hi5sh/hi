package main

import (
	"context"
	"fmt"
	"html"
	"net"
	"net/http"
	"time"
)

// A service the relay sends to may be restarting, as a hi team's service
// does on hi update --restart, for a few seconds. The relay waits for it
// rather than showing an error: a connection that never opened sent
// nothing, so trying again is safe for any request. If it still doesn't
// answer, a page load gets a page that says so and reloads itself.

// netExposeDialWait is how long the relay keeps trying to reach a service,
// every netExposeDialEvery; tests shorten it.
var netExposeDialWait, netExposeDialEvery = 10 * time.Second, 250 * time.Millisecond

type netExposeNoWaitKey struct{}

// netExposeNoWait marks a request that shouldn't wait for the service:
// hi's own check that the address works.
func netExposeNoWait(r *http.Request) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), netExposeNoWaitKey{}, true))
}

// netExposeDial connects to the service, trying again while it doesn't
// answer, up to netExposeDialWait.
func netExposeDial(ctx context.Context, network, address string) (net.Conn, error) {
	deadline := time.Now().Add(netExposeDialWait)
	for {
		conn, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, address)
		if err == nil || ctx.Value(netExposeNoWaitKey{}) != nil || time.Now().After(deadline) {
			return conn, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(netExposeDialEvery):
		}
	}
}

// netExposeTransport is the relay's transport to the service on a port,
// shared so connections are kept for the next request.
var netExposeTransport = func() *http.Transport {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = netExposeDial
	return transport
}()

// netExposeDown answers a request the service didn't: a page that reloads
// itself for a page load, a short error otherwise. what names the service
// for hi's own check, which still sees the 502 it always has.
func netExposeDown(w http.ResponseWriter, r *http.Request, what string) {
	if r.Header.Get(netExposeProbeHeader) != "" {
		http.Error(w, "hi net expose: nothing answers on "+what, http.StatusBadGateway)
		return
	}
	w.Header().Set("Retry-After", "5")
	w.Header().Set("Cache-Control", "no-store")
	if !netExposePageLoad(r) {
		http.Error(w, "The app isn't answering right now, probably restarting; try again in a few seconds.", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusServiceUnavailable)
	fmt.Fprintf(w, netExposeDownPage, html.EscapeString(r.URL.Path))
}

const netExposeDownPage = `<!doctype html><meta charset="utf-8"><meta name="viewport" content="width=device-width">
<meta http-equiv="refresh" content="5">
<title>Restarting</title><body style="font-family:system-ui,sans-serif;max-width:32rem;margin:4rem auto;padding:0 1rem">
<h1>Just a moment</h1><p>The app isn't answering right now, most likely because it is restarting. This page tries again by itself every few seconds.</p>
<p style="color:#777;font-size:14px">%s</p></body>`
