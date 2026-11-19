package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// hi dev web: the addresses as the machine sees them, a name that points
// at a proxy, what each says about frames, and what the dev page's
// Preview reported it couldn't show.
func TestTeamDevWeb(t *testing.T) {
	b, _ := appTestDevPage(t)
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, _ := strings.Cut(r.Host, ":")
		switch host {
		case "production.test":
			w.Header().Set("X-Frame-Options", "DENY")
			w.Header().Set("Content-Security-Policy", "default-src 'self'; frame-ancestors 'none'")
			w.Write([]byte("app"))
		case "stage.test":
			http.Redirect(w, r, "https://login.example/authorize?state=x", http.StatusFound)
		default:
			http.Error(w, "Forbidden", http.StatusForbidden)
		}
	}))
	defer app.Close()
	port := app.URL[strings.LastIndex(app.URL, ":"):]
	previousClient, previousLookup := appDevWebClient, appDevLookupCNAME
	t.Cleanup(func() { appDevWebClient, appDevLookupCNAME = previousClient, previousLookup })
	appDevWebClient = &http.Client{
		Timeout:       2 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Transport: &http.Transport{DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, strings.TrimPrefix(app.URL, "http://"))
		}},
	}
	appDevLookupCNAME = func(_ context.Context, host string) (string, error) {
		if host == "stage.test" {
			return "eu1.netbird.services.", nil
		}
		return host + ".", nil
	}
	write := func(slot string, state appPreview) {
		data, _ := json.Marshal(state)
		os.MkdirAll(filepath.Dir(appSlotPath("payments", slot)), 0o700)
		if err := os.WriteFile(appSlotPath("payments", slot), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(appSlotProduction, appPreview{Label: "production v3", URL: "http://production.test" + port + "/"})
	write(appSlotPreview, appPreview{Label: "dev, v3 + 1 change", URL: "http://stage.test" + port + "/"})
	data, _ := json.Marshal(appDevPageState{URL: "http://dev.test" + port + "/?hi_key=addresskey", Expires: time.Now().Add(time.Hour)})
	os.MkdirAll(filepath.Dir(appDevPageStatePath("payments")), 0o700)
	os.WriteFile(appDevPageStatePath("payments"), data, 0o600)

	// The dev page reports what its Preview couldn't show: an origin, for
	// the preview or production, from someone who builds.
	page := b.devPageHandler()
	for _, bad := range []map[string]string{{"slot": "preview", "to": "https://login.example/authorize?x"}, {"slot": "docs", "to": "https://login.example"}, {"slot": "preview", "to": "javascript:alert(1)"}} {
		if status, body := appTestDevPageCall(t, page, appTestOwnerKey, "POST", "api/preview/blocked", bad); status != http.StatusBadRequest {
			t.Fatal(bad, status, body)
		}
	}
	if status, _ := appTestDevPageCall(t, page, appTestAskerKey, "POST", "api/preview/blocked", map[string]string{"slot": "preview", "to": "https://login.example"}); status == http.StatusOK {
		t.Fatal("someone who doesn't build reported a block")
	}
	if status, body := appTestDevPageCall(t, page, appTestOwnerKey, "POST", "api/preview/blocked", map[string]string{"slot": "preview", "to": "https://login.example"}); status != http.StatusOK {
		t.Fatal(status, body)
	}

	box := b.devHandler()
	recorder := httptest.NewRecorder()
	box.ServeHTTP(recorder, httptest.NewRequest("GET", "/web", nil))
	var view appDevWebView
	if err := json.Unmarshal(recorder.Body.Bytes(), &view); err != nil || recorder.Code != http.StatusOK {
		t.Fatal(recorder.Code, recorder.Body.String())
	}
	if len(view.Addresses) != 3 {
		t.Fatalf("%+v", view.Addresses)
	}
	production, stage, dev := view.Addresses[0], view.Addresses[1], view.Addresses[2]
	if production.Status != 200 || production.Frames != "X-Frame-Options: DENY; CSP frame-ancestors 'none'" || production.Points != "" {
		t.Fatalf("%+v", production)
	}
	if stage.Status != http.StatusFound || stage.Sends != "https://login.example" || stage.Points != "eu1.netbird.services" || !strings.Contains(stage.Front, "NetBird") {
		t.Fatalf("%+v", stage)
	}
	if dev.Status != http.StatusForbidden || strings.Contains(dev.URL, "addresskey") || !strings.HasSuffix(dev.URL, appDevPagePrefix) {
		t.Fatalf("%+v", dev)
	}
	if view.Blocked == nil || view.Blocked.Slot != "preview" || view.Blocked.To != "https://login.example" || view.Blocked.From != "http://stage.test"+port {
		t.Fatalf("%+v", view.Blocked)
	}
	if len(view.Frames) != 2 {
		t.Fatalf("%q", view.Frames)
	}

	var out bytes.Buffer
	printTeamDevWeb(view, view.Blocked.At.Add(3*time.Minute), &out)
	for _, want := range []string{
		"its Chat is this session",
		"its name points at eu1.netbird.services: NetBird's reverse proxy",
		"from the machine: 302, sent on to https://login.example",
		"about frames: X-Frame-Options: DENY",
		"It last couldn't show preview's address, 3 minutes ago",
		"The hi-dev-web skill says more.",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("no %q in:\n%s", want, out.String())
		}
	}
}

// The box's agents are told where the people see them, and get the skill.
func TestTeamDevWebTold(t *testing.T) {
	if !strings.Contains(appDevInstructions, "hi dev web") || !strings.Contains(appDevUsage, "hi dev web") {
		t.Fatal("the box isn't told of hi dev web")
	}
	data, err := appDevSkills.ReadFile("skills/hi/app/hi-dev-web/SKILL.md")
	if err != nil || !strings.Contains(string(data), "name: hi-dev-web") || !strings.Contains(string(data), "hi dev web") {
		t.Fatal(err)
	}
}

// /__hi/return/ on the app's addresses sends the browser back after a
// sign-in in front of them: only to the app's own addresses, always on,
// the helper or not.
func TestTeamReturnToPage(t *testing.T) {
	b, _ := appTestDevPage(t)
	write := func(slot, address string) {
		data, _ := json.Marshal(appPreview{URL: address})
		os.MkdirAll(filepath.Dir(appSlotPath("payments", slot)), 0o700)
		os.WriteFile(appSlotPath("payments", slot), data, 0o600)
	}
	write(appSlotProduction, "https://payments.corp.example/")
	write(appSlotPreview, "https://stage.payments.corp.example/")
	data, _ := json.Marshal(appDevPageState{URL: "https://dev.payments.corp.example/?hi_key=addresskey", Expires: time.Now().Add(time.Hour)})
	os.MkdirAll(filepath.Dir(appDevPageStatePath("payments")), 0o700)
	os.WriteFile(appDevPageStatePath("payments"), data, 0o600)
	handler := b.webHandler(appSlotProduction)
	visit := func(to string) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest("GET", appReturnPrefix+"?to="+url.QueryEscape(to), nil))
		return recorder
	}
	for _, to := range []string{"https://dev.payments.corp.example/__hi/dev/", "https://stage.payments.corp.example/__hi/return/?to=https%3A%2F%2Fdev.payments.corp.example%2F__hi%2Fdev%2F"} {
		if got := visit(to); got.Code != http.StatusSeeOther || got.Header().Get("Location") != to {
			t.Fatal(to, got.Code, got.Header())
		}
	}
	for _, to := range []string{"https://evil.example/", "https://dev.payments.corp.example.evil.example/", "javascript:alert(1)", "//evil.example/", "https://user@dev.payments.corp.example/", ""} {
		if got := visit(to); got.Code != http.StatusBadRequest {
			t.Fatal(to, got.Code, got.Header())
		}
	}
	if args := strings.Join(appExposeHelperArgs("payments", appSlotProduction), " "); !strings.Contains(args, "--route-always "+appReturnPrefix+"=unix:") {
		t.Fatal(args)
	}
	if _, _, err := parseNetExpose(append([]string{"8080", "--link"}, appExposeHelperArgs("payments", appSlotProduction)...)); err != nil {
		t.Fatal(err)
	}
}
