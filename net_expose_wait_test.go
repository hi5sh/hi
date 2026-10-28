package main

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNetExposeWaitsForARestart(t *testing.T) {
	wait, every := netExposeDialWait, netExposeDialEvery
	defer func() { netExposeDialWait, netExposeDialEvery = wait, every }()
	netExposeDialWait, netExposeDialEvery = 3*time.Second, 20*time.Millisecond

	// A port that answers only after a moment, as a service that restarts.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()
	go func() {
		time.Sleep(300 * time.Millisecond)
		again, err := net.Listen("tcp", address)
		if err != nil {
			return
		}
		http.Serve(again, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "back") }))
	}()
	relay := httptest.NewServer(newNetExposeRelayTo(func() string { return "http://" + address }, "", time.Time{}, io.Discard, nil, nil))
	defer relay.Close()
	get := func(page, probe bool) (int, string) {
		t.Helper()
		request, _ := http.NewRequest("GET", relay.URL+"/orders", nil)
		if page {
			request.Header.Set("Sec-Fetch-Dest", "document")
		}
		if probe {
			request.Header.Set(netExposeProbeHeader, "1")
		}
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		body, _ := io.ReadAll(response.Body)
		return response.StatusCode, string(body)
	}
	if status, body := get(true, false); status != 200 || body != "back" {
		t.Fatal(status, body)
	}

	// Still down after the wait: a page that reloads itself, or a short
	// error; hi's own check doesn't wait and sees what it always has.
	netExposeDialWait = 100 * time.Millisecond
	dead, _ := net.Listen("tcp", "127.0.0.1:0")
	address = dead.Addr().String()
	dead.Close()
	if status, body := get(true, false); status != http.StatusServiceUnavailable || !strings.Contains(body, `<meta http-equiv="refresh" content="5">`) || !strings.Contains(body, "/orders") {
		t.Fatal(status, body)
	}
	if status, body := get(false, false); status != http.StatusServiceUnavailable || !strings.Contains(body, "probably restarting") {
		t.Fatal(status, body)
	}
	netExposeDialWait = time.Hour
	started := time.Now()
	if status, body := get(false, true); status != http.StatusBadGateway || !strings.Contains(body, "nothing answers on") || time.Since(started) > 5*time.Second {
		t.Fatal(status, body)
	}
}
