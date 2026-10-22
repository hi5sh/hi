package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func teamTestBomTools() teamBomMachine {
	return teamBomMachine{
		image: func(image string) (teamImageList, error) {
			if image == teamHindsightImage {
				return teamImageList{ID: "sha256:hind", OS: "debian", Release: "12", Packages: [][4]string{{"glibc", "2.36-9", "libc6", "2.36-9"}}}, nil
			}
			return teamImageList{ID: "sha256:abc", Digest: "sha256:def", OS: "ubuntu", Release: "24.04",
				Packages: [][4]string{{"openssl", "3.0.13-0ubuntu3.5", "libssl3t64", "3.0.13-0ubuntu3.5"}, {"curl", "8.5.0-2ubuntu10.6", "curl", "8.5.0-2ubuntu10.6"}},
				Runtimes: map[string]string{"bun": "1.4.3", "node": "24.21.0"}}, nil
		},
		tool: func(name string, args ...string) string {
			if name == "claude" {
				return "2.6.1"
			}
			return ""
		},
		baseImage: func() string { return "localhost/hi-box:9be3b547a8a7" },
	}
}

func TestTeamBomListsWhatProductionRuns(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	os.WriteFile(filepath.Join(b.repo, "bun.lock"), []byte(`{"lockfileVersion": 1, "packages": {"react": ["react@19.3.0", "", {}, "sha512-a"], "@biomejs/biome": ["@biomejs/biome@2.5.15", "", {}, "sha512-b"],},}`), 0o644)
	os.MkdirAll(filepath.Join(b.repo, "tools"), 0o755)
	os.WriteFile(filepath.Join(b.repo, "tools", "uv.lock"), []byte("version = 1\n\n[[package]]\nname = \"httpx\"\nversion = \"0.28.1\"\n\n[package.dependencies]\nname = \"x\"\n"), 0o644)
	os.WriteFile(filepath.Join(b.repo, "go.mod"), []byte("module example.com/x\n\ngo 1.26\n\nrequire (\n\tgolang.org/x/net v0.40.0 // indirect\n)\n"), 0o644)
	exec.Command("git", "-C", b.repo, "add", "-A").Run()
	exec.Command("git", "-C", b.repo, "commit", "-q", "-m", "lockfiles").Run()
	commit := boxGit(b.repo, "rev-parse", "HEAD")
	saveTeamVersions("payments", teamVersions{Production: 4, Releases: []teamRelease{{Version: 4, Commit: commit}}})

	path, version, err := teamProductionBom("payments", teamTestBomTools())
	if err != nil || version != "v4" || path != teamBomPath("payments", "v4") {
		t.Fatal(path, version, err)
	}
	var bom cdxBom
	data, _ := os.ReadFile(path)
	if err := json.Unmarshal(data, &bom); err != nil || bom.Format != "CycloneDX" || bom.Spec != "1.6" || bom.Metadata.Component.Version != "v4" {
		t.Fatalf("%v %+v", err, bom.Metadata)
	}
	byPurl := map[string]cdxComponent{}
	for _, component := range bom.Components {
		byPurl[component.Purl] = component
	}
	for purl, want := range map[string][3]string{
		"pkg:npm/react@19.3.0":                                            {"app", "npm", "react"},
		"pkg:npm/%40biomejs/biome@2.5.15":                                 {"app", "npm", "@biomejs/biome"},
		"pkg:pypi/httpx@0.28.1":                                           {"app", "PyPI", "httpx"},
		"pkg:golang/golang.org/x/net@v0.40.0":                             {"app", "Go", "golang.org/x/net"},
		"pkg:deb/ubuntu/libssl3t64@3.0.13-0ubuntu3.5?distro=ubuntu-24.04": {"image:base", "Ubuntu:24.04:LTS", "openssl"},
		"pkg:npm/%40anthropic-ai/claude-code@2.6.1":                       {"agent", "npm", "@anthropic-ai/claude-code"},
	} {
		component, ok := byPurl[purl]
		if !ok || component.property(bomPart) != want[0] || component.property(bomOSVSystem) != want[1] || component.property(bomOSVName) != want[2] {
			t.Errorf("%s: %+v", purl, component)
		}
	}
	if !strings.Contains(string(data), `"name": "bun"`) || !strings.Contains(string(data), "OpenRouter") || !strings.Contains(string(data), `"hi:commit"`) {
		t.Fatal("no runtimes or services")
	}
	// Made once per release; the digest names it.
	again, _, _ := teamProductionBom("payments", teamTestBomTools())
	if teamBomDigest(again) != teamBomDigest(path) || !strings.HasPrefix(teamBomDigest(path), "sha256:") {
		t.Fatal("made twice")
	}
}

func TestTeamBomParsers(t *testing.T) {
	for in, want := range map[[2]string]string{{"ubuntu", "24.04"}: "Ubuntu:24.04:LTS", {"ubuntu", "25.10"}: "Ubuntu:25.10", {"debian", "12.7"}: "Debian:12", {"alpine", "3.20.3"}: "Alpine:v3.20"} {
		if got := teamOSVEcosystem(in[0], in[1]); got != want {
			t.Errorf("%v: %s", in, got)
		}
	}
	if got := teamGoModRequires("module x\nrequire golang.org/x/text v0.20.0\nrequire (\n  a.b/c v1.0.0\n)\n"); len(got) != 2 || got[0][0] != "golang.org/x/text" || got[1][1] != "v1.0.0" {
		t.Fatal(got)
	}
}

// fakeAdvisories is OSV, CISA's KEV, and FIRST's EPSS for a test.
type fakeAdvisories struct {
	mu      sync.Mutex
	vulns   map[string]osvRecord // by "ecosystem name version" → its advisory
	kev     []string
	epss    map[string]string
	queries int
}

func (f *fakeAdvisories) start(t *testing.T) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		switch {
		case r.URL.Path == "/v1/querybatch":
			f.queries++
			var batch struct {
				Queries []osvQuery `json:"queries"`
			}
			json.NewDecoder(r.Body).Decode(&batch)
			var results []map[string]any
			for _, query := range batch.Queries {
				record, ok := f.vulns[query.Package.Ecosystem+" "+query.Package.Name+" "+query.Version]
				result := map[string]any{}
				if ok {
					result["vulns"] = []osvRef{{ID: record.ID, Modified: record.Modified}}
				}
				results = append(results, result)
			}
			json.NewEncoder(w).Encode(map[string]any{"results": results})
		case strings.HasPrefix(r.URL.Path, "/v1/vulns/"):
			for _, record := range f.vulns {
				if record.ID == strings.TrimPrefix(r.URL.Path, "/v1/vulns/") {
					json.NewEncoder(w).Encode(record)
					return
				}
			}
			http.NotFound(w, r)
		case r.URL.Path == "/kev.json":
			var list []map[string]string
			for _, cve := range f.kev {
				list = append(list, map[string]string{"cveID": cve})
			}
			json.NewEncoder(w).Encode(map[string]any{"vulnerabilities": list})
		case r.URL.Path == "/epss":
			var data []map[string]string
			for _, cve := range strings.Split(r.URL.Query().Get("cve"), ",") {
				if score, ok := f.epss[cve]; ok {
					data = append(data, map[string]string{"cve": cve, "epss": score})
				}
			}
			json.NewEncoder(w).Encode(map[string]any{"data": data})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	saved := [3]string{osvAPI, kevURL, epssAPI}
	osvAPI, kevURL, epssAPI = server.URL, server.URL+"/kev.json", server.URL+"/epss"
	t.Cleanup(func() { osvAPI, kevURL, epssAPI = saved[0], saved[1], saved[2] })
}

func testOSVRecord(id, ecosystem, name, fixed, cve, severity, modified string) osvRecord {
	var record osvRecord
	record.ID, record.Summary, record.Modified, record.Aliases = id, "A flaw in "+name, modified, []string{cve}
	record.DatabaseSpecific.Severity = severity
	record.Affected = append(record.Affected, record.Affected...)
	record.Affected = make([]struct {
		Package struct {
			Name      string `json:"name"`
			Ecosystem string `json:"ecosystem"`
		} `json:"package"`
		Ranges []struct {
			Events []map[string]string `json:"events"`
		} `json:"ranges"`
	}, 1)
	record.Affected[0].Package.Name, record.Affected[0].Package.Ecosystem = name, ecosystem
	record.Affected[0].Ranges = []struct {
		Events []map[string]string `json:"events"`
	}{{Events: []map[string]string{{"introduced": "0"}, {"fixed": fixed}}}}
	return record
}

// A dependency review lists what OSV knows for the versions a task adds,
// and a package known to be exploited needs an owner's yes to merge.
func TestTeamMergeAsksAnOwnerForAnExploitedPackage(t *testing.T) {
	setupTeamTest(t)
	fake := &fakeAdvisories{vulns: map[string]osvRecord{"npm sharp 0.34.0": testOSVRecord("GHSA-sh4r-p000-0001", "npm", "sharp", "0.34.5", "CVE-2026-1111", "HIGH", "2026-10-01T00:00:00Z")},
		kev: []string{"CVE-2026-1111"}}
	fake.start(t)
	teamDependencyAdvisories = teamLookupDependencyAdvisories
	b := newTeamTestBroker(t)
	b.files = map[string]map[string]string{"payments-t-1": {
		"package.json": `{"dependencies": {"sharp": "^0.34.0"}}`,
		"bun.lock":     `{"packages": {"sharp": ["sharp@0.34.0", "", {}, "sha512-d"],},}`,
	}}
	task, err := b.startTask(teamRequest{Role: "coder", Brief: "Resize the photos", Small: true})
	if err != nil {
		t.Fatal(err)
	}
	waitTeamTask(t, task.ID)
	b.reports["payments-t-2"] = `{"status": "complete", "summary": "Approve: it needs sharp."}`
	review, err := b.startReview(teamRequest{Task: "t-1"})
	if err != nil {
		t.Fatal(err)
	}
	waitTeamTask(t, review.ID)
	brief, _ := os.ReadFile(filepath.Join(teamTaskDir("payments"), "t-2.md"))
	if !strings.Contains(string(brief), "sharp 0.34.0: GHSA-sh4r-p000-0001, KNOWN TO BE EXPLOITED") || !strings.Contains(string(brief), "fixed in 0.34.5") {
		t.Fatal(string(brief))
	}
	if _, err := b.merge("t-1", ""); err == nil || !strings.Contains(err.Error(), "known to be exploited") {
		t.Fatal(err)
	}
	now := time.Now()
	ask, owner := teamSlackTS(now.Add(-2*time.Minute)), teamSlackTS(now.Add(-time.Minute))
	os.MkdirAll(teamChannelDir("payments"), 0o700)
	teamAppendChannel(teamChannelDir("payments"), []teamChannelMessage{
		{TS: ask, Name: "team-payments", Bot: true, Text: "t-1 brings in sharp 0.34.0, which attackers already use. May it?"},
		{TS: owner, Thread: ask, User: "U07OWNER1", Name: "Owner", Text: "Yes, for now"},
	})
	if merged, err := b.merge("t-1", owner); err != nil || merged.TrustApproval == nil {
		t.Fatal(err, merged.TrustApproval)
	}
}

// The server asks for the bill a heartbeat names, matches it, and the team
// hears of what the lead can act on; an owner's ignore closes it until the
// advisory changes.
func TestWatchMatchesProductionsBill(t *testing.T) {
	ts := newTestServer(t)
	ts.connectAs(t, "anna", "staff")
	key, err := loadDeviceKey(false)
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeAdvisories{vulns: map[string]osvRecord{
		"npm react 19.3.0":                           testOSVRecord("GHSA-r3ac-t000-0001", "npm", "react", "19.3.1", "CVE-2026-2222", "HIGH", "2026-10-01T00:00:00Z"),
		"Ubuntu:24.04:LTS openssl 3.0.13-0ubuntu3.5": testOSVRecord("UBUNTU-CVE-2026-3333", "Ubuntu:24.04:LTS", "openssl", "3.0.13-0ubuntu3.6", "CVE-2026-3333", "", "2026-10-02T00:00:00Z"),
		"npm @biomejs/biome 2.5.15":                  testOSVRecord("GHSA-b10m-e000-0001", "npm", "@biomejs/biome", "2.5.16", "CVE-2026-4444", "LOW", "2026-10-03T00:00:00Z"),
	}, kev: []string{"CVE-2026-2222"}, epss: map[string]string{"CVE-2026-3333": "0.42", "CVE-2026-4444": "0.001"}}
	fake.start(t)
	var pushes []string
	var pushMu sync.Mutex
	ts.server.watch.config.Ntfy = "https://ntfy.example"
	ts.server.watch.push = func(config watchConfig, topic, title, text string) error {
		pushMu.Lock()
		defer pushMu.Unlock()
		pushes = append(pushes, text)
		return nil
	}

	setupTeamTest(t)
	b := newTeamTestBroker(t)
	teamTestWakes(b.teamBroker)
	channel := &fakeTeamChannel{}
	b.channel = channel
	saved, savedMachine, savedTools := teamWatchClient, teamWatchMachine, teamBomTools
	t.Cleanup(func() { teamWatchClient, teamWatchMachine, teamBomTools = saved, savedMachine, savedTools })
	teamWatchClient = func() (*serverClient, string) { return newServerClient(ts.url, key), ts.url }
	teamWatchMachine = (&teamTestProbe{states: map[string]string{}, disk: 0.3}).probe()
	teamBomTools = teamTestBomTools()
	os.WriteFile(filepath.Join(b.repo, "bun.lock"), []byte(`{"packages": {"react": ["react@19.3.0", "", {}, "sha512-a"], "@biomejs/biome": ["@biomejs/biome@2.5.15", "", {}, "sha512-b"],},}`), 0o644)
	exec.Command("git", "-C", b.repo, "add", "-A").Run()
	exec.Command("git", "-C", b.repo, "commit", "-q", "-m", "lockfile").Run()
	saveTeamVersions("payments", teamVersions{Production: 2, Releases: []teamRelease{{Version: 2, Commit: boxGit(b.repo, "rev-parse", "HEAD")}}})

	// The first heartbeat names a bill the server doesn't have: it's sent,
	// and matched.
	if _, err := b.sendHeartbeat(""); err != nil {
		t.Fatal(err)
	}
	teamTestEventually(t, "never matched", func() bool {
		view := ts.server.watch.view()
		return len(view.Teams) == 1 && !view.Teams[0].Matched.IsZero()
	})
	view := ts.server.watch.view()
	if view.Teams[0].Advisories != 3 || view.Teams[0].Loud != 2 {
		t.Fatalf("%+v", view.Teams[0])
	}
	var rules []string
	for _, incident := range view.Incidents {
		rules = append(rules, incident.Rule)
	}
	if strings.Join(rules, " ") != "advisory:GHSA-r3ac-t000-0001:react advisory:UBUNTU-CVE-2026-3333:openssl" {
		t.Fatal(rules)
	}
	teamTestEventually(t, "no push for the exploited one", func() bool {
		pushMu.Lock()
		defer pushMu.Unlock()
		return len(pushes) == 1 && strings.Contains(pushes[0], "react 19.3.0")
	})

	// The next heartbeat: the lead hears of the app's, not the base image's,
	// and an exploited one in the app's own packages freezes the team.
	b.sendHeartbeat("")
	if stop := loadTeamWatchStop("payments"); !stop.Frozen || stop.By != "watch" || !strings.Contains(stop.Because, "react 19.3.0") {
		t.Fatalf("%+v", stop)
	}
	incidents := loadTeamIncidents("payments")
	if len(incidents) != 1 || !strings.Contains(incidents[0].Title, "react 19.3.0, which has GHSA-r3ac-t000-0001, known to be exploited") || !strings.Contains(incidents[0].Detail, "The lead proposes the upgrade") {
		t.Fatalf("%+v", incidents)
	}
	var out strings.Builder
	teamAdvisoriesCommand([]string{"payments"}, &out)
	if !strings.Contains(out.String(), "2 advisories to act on, 1 for the weekly report") || !strings.Contains(out.String(), "EXPLOITED GHSA-r3ac-t000-0001 react 19.3.0 (app), fixed in 19.3.1") {
		t.Fatal(out.String())
	}
	// The weekly report reached the team's channel, once.
	b.sendHeartbeat("")
	reports := 0
	for _, call := range channel.calls {
		if strings.Contains(call, "This week's advisories") && strings.Contains(call, "@biomejs/biome 2.5.15") {
			reports++
		}
	}
	if reports != 1 {
		t.Fatal(channel.calls)
	}

	// An owner says the react one doesn't apply: closed here and there.
	out.Reset()
	if err := teamAdvisoryCommand([]string{"payments", "ignore", "GHSA-r3ac-t000-0001", "--because", "we never render untrusted HTML"}, &out); err != nil {
		t.Fatal(err)
	}
	b.sendHeartbeat("")
	b.sendHeartbeat("")
	if incident, _ := loadTeamIncident("payments", incidents[0].ID); incident.Status != "closed" {
		t.Fatalf("%+v", incident)
	}
	vex, _ := os.ReadFile(filepath.Join(teamBomDir("payments"), "vex.cdx.json"))
	if !strings.Contains(string(vex), `"not_affected"`) || !strings.Contains(string(vex), "untrusted HTML") {
		t.Fatal(string(vex))
	}
	// The advisory changes: raised again.
	fake.mu.Lock()
	record := fake.vulns["npm react 19.3.0"]
	record.Modified = "2026-10-20T00:00:00Z"
	fake.vulns["npm react 19.3.0"] = record
	fake.mu.Unlock()
	ts.server.matchTeam("payments")
	open := 0
	for _, incident := range ts.server.watch.view().Incidents {
		if incident.Rule == "advisory:GHSA-r3ac-t000-0001:react" {
			open++
		}
	}
	if open != 1 {
		t.Fatal("a changed advisory stayed ignored")
	}
	_ = io.Discard
}

// An image's advisories are thousands: the team keeps its own and the
// loud ones, the rest as counts, and the weekly report says one line per
// image.
func TestWatchSumsUpAnImagesAdvisories(t *testing.T) {
	var advisories []apiAdvisory
	for i := range 3000 {
		advisory := apiAdvisory{ID: fmt.Sprintf("UBUNTU-CVE-2026-%d", i), Package: "pkg", Part: "image:base"}
		if i%3 == 0 {
			advisory.Fixed = []string{"2"}
		}
		advisories = append(advisories, advisory)
	}
	advisories = append(advisories,
		apiAdvisory{ID: "UBUNTU-CVE-2026-9999", Package: "openssl", Part: "image:base", KEV: true},
		apiAdvisory{ID: "GHSA-1", Package: "react", Part: "app", Fixed: []string{"19.3.1"}})
	listed, counts := teamFacingAdvisories(advisories)
	if len(listed) != 2 || len(counts) != 1 || counts[0].Total != 3000 || counts[0].Fixable != 1000 {
		t.Fatalf("%d listed, %+v", len(listed), counts)
	}
	w := &serverWatch{state: watchState{Teams: map[string]*watchTeam{}, Incidents: map[string]*watchIncident{}}}
	team := &watchTeam{Team: "payments", BomVersion: "v4", Advisories: advisories}
	report := w.reportLocked(team, time.Now())
	if strings.Count(report, "\n") > 5 || !strings.Contains(report, "hi's base image: 3000 advisories for its OS packages, 1000 of them with a fix") ||
		!strings.Contains(report, "GHSA-1, fixed in 19.3.1") {
		t.Fatal(report)
	}
	if team.Report == nil || !strings.Contains(team.Report.Text, "GHSA-1") || strings.Contains(team.Report.Text, "UBUNTU") {
		t.Fatalf("%+v", team.Report)
	}
}
