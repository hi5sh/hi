package main

import (
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTeamDomain(t *testing.T) {
	setupTeamTest(t)
	before := netExposeAddress
	netExposeAddress = func() (string, error) { return "100.64.0.7", nil }
	t.Cleanup(func() { netExposeAddress = before })
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	fake := fakeNetbird(t)
	if teamSlotDomain("payments", teamSlotProduction) != "" {
		t.Fatal("a name without a domain")
	}
	var out strings.Builder
	// NetBird must have the domain, verified.
	domains := fakeNetbirdDomains(t, fake, `[{"id":"d1","domain":"apps.example.com","validated":false,"target_cluster":"eu.proxy.netbird.io"}]`)
	if err := teamDomainCommand([]string{"apps.example.com"}, strings.NewReader(""), &out); err == nil || !strings.Contains(err.Error(), "eu.proxy.netbird.io") {
		t.Fatal(err)
	}
	*domains = `[{"id":"d1","domain":"apps.example.com","validated":true}]`
	if err := teamDomainCommand([]string{"other.example.com"}, strings.NewReader(""), &out); err == nil || !strings.Contains(err.Error(), "Custom Domains") {
		t.Fatal(err)
	}
	// A token that may read but not make services, as a Network Admin's.
	fake.readOnly = true
	if err := teamDomainCommand([]string{"apps.example.com"}, strings.NewReader(""), &out); err == nil || !strings.Contains(err.Error(), "Admin role") {
		t.Fatal(err)
	}
	fake.readOnly = false
	if err := teamDomainCommand([]string{"Apps.Example.com."}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	// The check leaves no service behind.
	if len(fake.services) != 0 {
		t.Fatalf("%+v", fake.services)
	}
	// A token NetBird doesn't know any more: a new one is asked for.
	saveNetbirdAPIToken("nbp_gone")
	t.Setenv("NETBIRD_API_TOKEN", "")
	gone := filepath.Join(t.TempDir(), "new.txt")
	os.WriteFile(gone, []byte("nbp_test\n"), 0o600)
	newFile, _ := os.Open(gone)
	defer newFile.Close()
	out.Reset()
	if err := teamDomainCommand([]string{"apps.example.com"}, newFile, &out); err != nil || !strings.Contains(out.String(), "doesn't know the saved token") {
		t.Fatal(err, out.String())
	}
	t.Setenv("NETBIRD_API_TOKEN", "nbp_test")
	// The token from $NETBIRD_API_TOKEN is saved for the teams, which run
	// without it.
	if token, err := os.ReadFile(netbirdTokenPath()); err != nil || strings.TrimSpace(string(token)) != "nbp_test" {
		t.Fatal(string(token), err)
	}
	// Without one, it reads the token from standard input when that isn't
	// a terminal.
	t.Setenv("NETBIRD_API_TOKEN", "")
	os.Remove(netbirdTokenPath())
	piped := filepath.Join(t.TempDir(), "token.txt")
	os.WriteFile(piped, []byte("nbp_test\n"), 0o600)
	file, _ := os.Open(piped)
	defer file.Close()
	if err := teamDomainCommand([]string{"apps.example.com"}, file, &out); err != nil {
		t.Fatal(err)
	}
	if token, _ := os.ReadFile(netbirdTokenPath()); strings.TrimSpace(string(token)) != "nbp_test" {
		t.Fatal(string(token))
	}
	// A team moves only once it's on for it.
	os.MkdirAll(teamDir("payments"), 0o700)
	if err := saveTeam(testTeamConfig()); err != nil {
		t.Fatal(err)
	}
	if teamSlotDomain("payments", teamSlotProduction) != "" {
		t.Fatal("on for a team that didn't ask")
	}
	if err := teamDomainCommand([]string{"payments", "on"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	if got := teamSlotDomain("payments", teamSlotProduction) + " " + teamSlotDomain("payments", teamSlotPreview); got != "payments.apps.example.com payments-preview.apps.example.com" {
		t.Fatal(got)
	}
	out.Reset()
	teamDomainCommand(nil, strings.NewReader(""), &out)
	if !strings.Contains(out.String(), "payments: https://payments.apps.example.com") {
		t.Fatal(out.String())
	}
	if err := teamDomainCommand([]string{"payments", "off"}, strings.NewReader(""), &out); err != nil || teamSlotDomain("payments", teamSlotProduction) != "" {
		t.Fatal(err)
	}
	teamDomainCommand([]string{"payments", "on"}, strings.NewReader(""), &out)
	if err := teamDomainCommand([]string{"off"}, strings.NewReader(""), &out); err != nil || teamSlotDomain("payments", teamSlotProduction) != "" {
		t.Fatal(err)
	}
}

func TestTeamDomainSettled(t *testing.T) {
	if !teamDomainSettled(teamPreview{Domain: "a.example.com"}, "a.example.com") || teamDomainSettled(teamPreview{}, "a.example.com") ||
		!teamDomainSettled(teamPreview{DomainError: "not verified", DomainChecked: time.Now()}, "a.example.com") ||
		teamDomainSettled(teamPreview{DomainError: "not verified", DomainChecked: time.Now().Add(-time.Hour)}, "a.example.com") ||
		teamDomainSettled(teamPreview{Domain: "a.example.com"}, "") {
		t.Fatal("settled")
	}
}

// fakeNetbirdDomains serves the custom domains the fake NetBird has.
func fakeNetbirdDomains(t *testing.T, fake *fakeNetbirdAPI, body string) *string {
	t.Helper()
	domains := body
	fake.domains = func(w http.ResponseWriter) { w.Write([]byte(domains)) }
	return &domains
}

func TestTeamPreviewDomain(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	os.WriteFile(filepath.Join(b.repo, "Makefile"), []byte("run:\n\t./serve\n"), 0o644)
	exec.Command("git", "-C", b.repo, "add", "-A").Run()
	exec.Command("git", "-C", b.repo, "commit", "-q", "-m", "app").Run()
	if err := saveTeamsSettings(teamsSettings{Domain: "apps.example.com"}); err != nil {
		t.Fatal(err)
	}
	config, _ := loadTeam("payments")
	config.Domain = true
	saveTeam(config)
	// A name NetBird doesn't have yet: an address it makes up, and why.
	b.domainFails = true
	first, err := b.preview(teamPreviewRequest{})
	if err != nil || first.Domain != "" || !strings.Contains(first.URL, ".example.net") || !strings.Contains(first.DomainError, "doesn't have it") {
		t.Fatalf("%+v %v", first, err)
	}
	// It isn't tried again at once.
	b.domainFails = false
	if again, err := b.preview(teamPreviewRequest{}); err != nil || again.URL != first.URL {
		t.Fatalf("%+v %v", again, err)
	}
	// Once it's time, the address moves to the name that stays, for 30 days.
	saved, _ := loadTeamSlot("payments", teamSlotPreview)
	saved.DomainChecked = time.Now().Add(-time.Hour)
	saveTeamSlot("payments", saved)
	second, err := b.preview(teamPreviewRequest{})
	if err != nil || second.Domain != "payments-preview.apps.example.com" || second.URL != "https://payments-preview.apps.example.com/?hi_key=k" || second.DomainError != "" || !second.NewLink {
		t.Fatalf("%+v %v", second, err)
	}
	last := b.calls[len(b.calls)-1]
	for _, call := range b.calls {
		if strings.Contains(call, "--domain") {
			last = call
		}
	}
	if !strings.Contains(last, "--max 720h0m0s") || !strings.Contains(last, "--name team-payments --domain payments-preview.apps.example.com ") {
		t.Fatal(last)
	}
	// A relay from a hi that routed less (before /docs/): a new relay on
	// the same name, with the same key, so the links people have work.
	saved, _ = loadTeamSlot("payments", teamSlotPreview)
	if saved.Relay != teamRelayStamp("payments", teamSlotPreview) {
		t.Fatal("no relay stamp")
	}
	saved.Relay = ""
	saveTeamSlot("payments", saved)
	if !teamRelayOutdated(saved, teamSlotDomain("payments", teamSlotPreview), teamRelayStamp("payments", teamSlotPreview)) {
		t.Fatal("not outdated")
	}
	calls := len(b.calls)
	third, err := b.preview(teamPreviewRequest{})
	if err != nil || third.URL != second.URL || third.NewLink || third.Relay != teamRelayStamp("payments", teamSlotPreview) {
		t.Fatalf("%+v %v", third, err)
	}
	if moved := strings.Join(b.calls[calls:], "\n"); !strings.Contains(moved, "--domain payments-preview.apps.example.com --key k --route") ||
		!strings.Contains(moved, "--route-always /docs/=unix:") {
		t.Fatal(moved)
	}
	// An address NetBird made up waits for its next one.
	if teamRelayOutdated(teamPreview{URL: first.URL}, "", "x") {
		t.Fatal("made-up address")
	}
}

func TestTeamAppBoxesFollowHi(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	os.WriteFile(filepath.Join(b.repo, "Makefile"), []byte("run:\n\t./serve\n"), 0o644)
	exec.Command("git", "-C", b.repo, "add", "-A").Run()
	exec.Command("git", "-C", b.repo, "commit", "-q", "-m", "app").Run()
	first, err := b.preview(teamPreviewRequest{})
	if err != nil {
		t.Fatal(err)
	}
	// The same hi: nothing to do.
	if loaded, _ := loadTeamSlot("payments", teamSlotPreview); loaded.Settings != teamSettingsStamp("payments") {
		t.Fatal("stamp")
	}
	// An update: the preview moves to a new box, on the same address,
	// still the same version.
	before := teamStampVersion
	teamStampVersion = "v9.9.9"
	t.Cleanup(func() { teamStampVersion = before })
	saved, _ := loadTeamSlot("payments", teamSlotPreview)
	if saved.Settings == teamSettingsStamp("payments") {
		t.Fatal("the stamp doesn't follow hi")
	}
	if err := b.refreshPreview(saved); err != nil {
		t.Fatal(err)
	}
	after, _ := loadTeamSlot("payments", teamSlotPreview)
	if after.URL != first.URL || after.Container == first.Container || after.Commit != first.Commit || after.Label != first.Label || after.Settings != teamSettingsStamp("payments") {
		t.Fatalf("%+v\n%+v", first, after)
	}
}
