package main

// hi watch, release 2, on the server: each team's production bill of
// materials (team_bom.go) is matched against advisories every night and
// when a new bill arrives. OSV says which advisories name a package at a
// version; CISA's KEV says which are known to be exploited; FIRST's EPSS
// how likely each is to be in the next 30 days. A match that is exploited,
// likely to be, or critical with a fix is an incident; the rest go in the
// team's weekly report.
//
// Spec: docs/specs/approved/hi_watch.md.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	osvAPI  = "https://api.osv.dev"
	kevURL  = "https://www.cisa.gov/sites/default/files/feeds/known_exploited_vulnerabilities.json"
	epssAPI = "https://api.first.org/data/v1/epss"
)

const (
	advisoryMatchEvery  = 24 * time.Hour
	advisoryReportEvery = 7 * 24 * time.Hour
	// advisoryEPSSIncident is the EPSS at which a fixable advisory is an
	// incident rather than a line in the weekly report.
	advisoryEPSSIncident = 0.1
	osvBatchMax          = 1000
)

// ---------------------------------------------------------------------------
// OSV, KEV, and EPSS

type osvQuery struct {
	Package struct {
		Name      string `json:"name"`
		Ecosystem string `json:"ecosystem"`
	} `json:"package"`
	Version   string `json:"version"`
	PageToken string `json:"page_token,omitempty"`
}

type osvRef struct {
	ID       string `json:"id"`
	Modified string `json:"modified"`
}

// osvRecord is the part of an OSV advisory hi reads.
type osvRecord struct {
	ID       string   `json:"id"`
	Summary  string   `json:"summary"`
	Details  string   `json:"details"`
	Aliases  []string `json:"aliases"`
	Upstream []string `json:"upstream"`
	Modified string   `json:"modified"`
	Severity []struct {
		Type  string `json:"type"`
		Score string `json:"score"`
	} `json:"severity"`
	Affected []struct {
		Package struct {
			Name      string `json:"name"`
			Ecosystem string `json:"ecosystem"`
		} `json:"package"`
		Ranges []struct {
			Events []map[string]string `json:"events"`
		} `json:"ranges"`
	} `json:"affected"`
	DatabaseSpecific struct {
		Severity string `json:"severity"`
	} `json:"database_specific"`
}

func osvQueryFor(ecosystem, name, version string) osvQuery {
	var query osvQuery
	query.Package.Name, query.Package.Ecosystem, query.Version = name, ecosystem, version
	return query
}

func advisoryHTTP() *http.Client { return &http.Client{Timeout: 60 * time.Second} }

// osvQueryBatch asks OSV which advisories name each package at its
// version, following a result's further pages.
func osvQueryBatch(queries []osvQuery) ([][]osvRef, error) {
	results := make([][]osvRef, len(queries))
	for start := 0; start < len(queries); start += osvBatchMax {
		end := min(start+osvBatchMax, len(queries))
		pending := map[int]osvQuery{}
		for i := start; i < end; i++ {
			pending[i] = queries[i]
		}
		for round := 0; len(pending) > 0 && round < 10; round++ {
			indexes := sortedIntKeys(pending)
			batch := struct {
				Queries []osvQuery `json:"queries"`
			}{}
			for _, i := range indexes {
				batch.Queries = append(batch.Queries, pending[i])
			}
			var answer struct {
				Results []struct {
					Vulns []osvRef `json:"vulns"`
					Next  string   `json:"next_page_token"`
				} `json:"results"`
			}
			if err := advisoryPostJSON(osvAPI+"/v1/querybatch", batch, &answer); err != nil {
				return nil, err
			}
			if len(answer.Results) != len(indexes) {
				return nil, fmt.Errorf("OSV answered %d of %d queries", len(answer.Results), len(indexes))
			}
			next := map[int]osvQuery{}
			for n, i := range indexes {
				results[i] = append(results[i], answer.Results[n].Vulns...)
				if token := answer.Results[n].Next; token != "" {
					query := queries[i]
					query.PageToken = token
					next[i] = query
				}
			}
			pending = next
		}
	}
	return results, nil
}

func sortedIntKeys[V any](values map[int]V) []int {
	keys := make([]int, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Ints(keys)
	return keys
}

func advisoryPostJSON(target string, body, result any) error {
	data, _ := json.Marshal(body)
	response, err := advisoryHTTP().Post(target, "application/json", bytes.NewReader(data))
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		text, _ := io.ReadAll(io.LimitReader(response.Body, 512))
		return fmt.Errorf("%s: %s %s", target, response.Status, strings.TrimSpace(string(text)))
	}
	return json.NewDecoder(io.LimitReader(response.Body, 64<<20)).Decode(result)
}

func advisoryGetJSON(target string, result any) error {
	response, err := advisoryHTTP().Get(target)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %s", target, response.Status)
	}
	return json.NewDecoder(io.LimitReader(response.Body, 64<<20)).Decode(result)
}

// osvVuln is an advisory, from the cache in dir while it hasn't changed.
func osvVuln(dir string, ref osvRef) (osvRecord, error) {
	path := filepath.Join(dir, strings.ReplaceAll(ref.ID, "/", "_")+".json")
	var record osvRecord
	if data, err := os.ReadFile(path); err == nil && json.Unmarshal(data, &record) == nil && (ref.Modified == "" || record.Modified == ref.Modified) {
		return record, nil
	}
	if err := advisoryGetJSON(osvAPI+"/v1/vulns/"+url.PathEscape(ref.ID), &record); err != nil {
		return osvRecord{}, err
	}
	if dir != "" {
		os.MkdirAll(dir, 0o700)
		data, _ := json.Marshal(record)
		writeFileAtomic(path, data, 0o600)
	}
	return record, nil
}

// osvVulns fetches every advisory the results name, 16 at a time: an
// image's OS packages can match hundreds, and the cache keeps them until
// they change.
func osvVulns(dir string, results [][]osvRef) (map[string]osvRecord, error) {
	unique := map[string]osvRef{}
	for _, refs := range results {
		for _, ref := range refs {
			unique[ref.ID] = ref
		}
	}
	records := map[string]osvRecord{}
	var mu sync.Mutex
	var first error
	work := make(chan osvRef)
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ref := range work {
				record, err := osvVuln(dir, ref)
				mu.Lock()
				if err != nil && first == nil {
					first = err
				}
				records[ref.ID] = record
				mu.Unlock()
			}
		}()
	}
	for _, id := range sortedKeys(unique) {
		work <- unique[id]
	}
	close(work)
	wg.Wait()
	return records, first
}

// cves are the CVEs an advisory is about.
func (r osvRecord) cves() []string {
	seen := map[string]bool{}
	var cves []string
	for _, id := range append(append([]string{r.ID, strings.TrimPrefix(r.ID, "UBUNTU-"), strings.TrimPrefix(r.ID, "DEBIAN-")}, r.Aliases...), r.Upstream...) {
		if strings.HasPrefix(id, "CVE-") && !seen[id] {
			seen[id] = true
			cves = append(cves, id)
		}
	}
	return cves
}

// severity is the advisory's own word for it, lowercase: GitHub's, or
// Ubuntu's or Debian's priority.
func (r osvRecord) severity() string {
	if r.DatabaseSpecific.Severity != "" {
		return strings.ToLower(r.DatabaseSpecific.Severity)
	}
	for _, severity := range r.Severity {
		if !strings.HasPrefix(severity.Type, "CVSS") {
			return strings.ToLower(severity.Score)
		}
	}
	return ""
}

// fixed are the versions that fix the advisory for a package.
func (r osvRecord) fixed(ecosystem, name string) []string {
	seen := map[string]bool{}
	var fixed []string
	for _, affected := range r.Affected {
		if affected.Package.Name != name || affected.Package.Ecosystem != ecosystem {
			continue
		}
		for _, r := range affected.Ranges {
			for _, event := range r.Events {
				if version := event["fixed"]; version != "" && !seen[version] {
					seen[version] = true
					fixed = append(fixed, version)
				}
			}
		}
	}
	return fixed
}

// advisoryFeeds holds KEV and EPSS, refreshed once a day, kept in dir.
type advisoryFeeds struct {
	mu   sync.Mutex
	dir  string
	kev  map[string]bool
	at   time.Time
	epss map[string]advisoryEPSS
}

type advisoryEPSS struct {
	Score float64 `json:"score"`
	Day   string  `json:"day"`
}

func (f *advisoryFeeds) loadKEV(now time.Time) (map[string]bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.kev != nil && now.Sub(f.at) < advisoryMatchEvery {
		return f.kev, nil
	}
	path := filepath.Join(f.dir, "kev.json")
	var feed struct {
		Vulnerabilities []struct {
			CVE string `json:"cveID"`
		} `json:"vulnerabilities"`
	}
	info, statErr := os.Stat(path)
	if statErr != nil || now.Sub(info.ModTime()) >= advisoryMatchEvery {
		response, err := advisoryHTTP().Get(kevURL)
		if err == nil && response.StatusCode == http.StatusOK {
			data, _ := io.ReadAll(io.LimitReader(response.Body, 64<<20))
			response.Body.Close()
			if json.Unmarshal(data, &feed) == nil && len(feed.Vulnerabilities) > 0 {
				os.MkdirAll(f.dir, 0o700)
				writeFileAtomic(path, data, 0o600)
			}
		} else if response != nil {
			response.Body.Close()
		}
	}
	if len(feed.Vulnerabilities) == 0 {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("CISA's KEV can't be read: %w", err)
		}
		json.Unmarshal(data, &feed)
	}
	f.kev = map[string]bool{}
	for _, vulnerability := range feed.Vulnerabilities {
		f.kev[vulnerability.CVE] = true
	}
	f.at = now
	return f.kev, nil
}

// scores is EPSS for each CVE, asked of FIRST once a day.
func (f *advisoryFeeds) scores(cves []string, now time.Time) map[string]float64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	path := filepath.Join(f.dir, "epss.json")
	if f.epss == nil {
		f.epss = map[string]advisoryEPSS{}
		if data, err := os.ReadFile(path); err == nil {
			json.Unmarshal(data, &f.epss)
		}
	}
	today := now.UTC().Format("2006-01-02")
	var ask []string
	for _, cve := range cves {
		if f.epss[cve].Day != today {
			ask = append(ask, cve)
		}
	}
	for start := 0; start < len(ask); start += 100 {
		batch := ask[start:min(start+100, len(ask))]
		var answer struct {
			Data []struct {
				CVE  string `json:"cve"`
				EPSS string `json:"epss"`
			} `json:"data"`
		}
		if err := advisoryGetJSON(epssAPI+"?cve="+strings.Join(batch, ","), &answer); err != nil {
			break
		}
		for _, cve := range batch {
			f.epss[cve] = advisoryEPSS{Day: today}
		}
		for _, row := range answer.Data {
			score, _ := strconv.ParseFloat(row.EPSS, 64)
			f.epss[row.CVE] = advisoryEPSS{Score: score, Day: today}
		}
	}
	if len(ask) > 0 {
		os.MkdirAll(f.dir, 0o700)
		data, _ := json.Marshal(f.epss)
		writeFileAtomic(path, data, 0o600)
	}
	scores := map[string]float64{}
	for _, cve := range cves {
		scores[cve] = f.epss[cve].Score
	}
	return scores
}

// ---------------------------------------------------------------------------
// matching a bill

// apiAdvisory is an advisory that names a package production runs.
type apiAdvisory struct {
	ID        string   `json:"id"`
	Package   string   `json:"package"`
	Version   string   `json:"version"`
	Ecosystem string   `json:"ecosystem"`
	Part      string   `json:"part"` // app, image:base, image:memory, agent, hi
	Summary   string   `json:"summary,omitempty"`
	CVEs      []string `json:"cves,omitempty"`
	Fixed     []string `json:"fixed,omitempty"`
	Severity  string   `json:"severity,omitempty"`
	KEV       bool     `json:"kev,omitempty"`
	EPSS      float64  `json:"epss,omitempty"`
	Modified  string   `json:"modified,omitempty"`
	Ignored   string   `json:"ignored,omitempty"` // why an owner said it doesn't apply
}

// apiAdvisoryCount sums up an image's advisories, which are too many to
// list and fixed all at once by building the image again.
type apiAdvisoryCount struct {
	Part    string `json:"part"`
	Total   int    `json:"total"`
	Fixable int    `json:"fixable"`
}

// advisoriesPath keeps a team's matched advisories out of state.json,
// which is written at every heartbeat: an image's can be thousands.
func (w *serverWatch) advisoriesPath(team string) string {
	return filepath.Join(w.dir, "teams", team, "advisories.json")
}

// teamFacingAdvisories are those the team's machine keeps: every one for
// the app, the agents, and hi, and of an image's, those loud enough for an
// incident or ignored; the rest of an image's as counts.
func teamFacingAdvisories(advisories []apiAdvisory) ([]apiAdvisory, []apiAdvisoryCount) {
	var listed []apiAdvisory
	counts := map[string]*apiAdvisoryCount{}
	for _, advisory := range advisories {
		tier := advisory.tier()
		if !strings.HasPrefix(advisory.Part, "image:") || tier == "kev" || tier == "incident" || tier == "" {
			listed = append(listed, advisory)
			continue
		}
		count := counts[advisory.Part]
		if count == nil {
			count = &apiAdvisoryCount{Part: advisory.Part}
			counts[advisory.Part] = count
		}
		count.Total++
		if len(advisory.Fixed) > 0 {
			count.Fixable++
		}
	}
	var summary []apiAdvisoryCount
	for _, part := range sortedKeys(counts) {
		summary = append(summary, *counts[part])
	}
	return listed, summary
}

// tier is how loud an advisory is: "kev", "incident", or "report".
func (a apiAdvisory) tier() string {
	switch {
	case a.Ignored != "":
		return ""
	case a.KEV:
		return "kev"
	case len(a.Fixed) > 0 && (a.EPSS >= advisoryEPSSIncident || a.Severity == "critical"):
		return "incident"
	}
	return "report"
}

// advisoryMatch finds the advisories that name the bill's packages.
func advisoryMatch(bom cdxBom, cacheDir string, feeds *advisoryFeeds, now time.Time) ([]apiAdvisory, error) {
	type target struct{ ecosystem, name, version, part string }
	var targets []target
	seen := map[target]bool{}
	for _, component := range bom.Components {
		t := target{component.property(bomOSVSystem), component.property(bomOSVName), component.property(bomOSVVersion), component.property(bomPart)}
		if t.ecosystem == "" || t.name == "" || t.version == "" || seen[t] {
			continue
		}
		seen[t] = true
		targets = append(targets, t)
	}
	queries := make([]osvQuery, len(targets))
	for i, t := range targets {
		queries[i] = osvQueryFor(t.ecosystem, t.name, t.version)
	}
	results, err := osvQueryBatch(queries)
	if err != nil {
		return nil, err
	}
	kev, err := feeds.loadKEV(now)
	if err != nil {
		return nil, err
	}
	records, err := osvVulns(filepath.Join(cacheDir, "osv"), results)
	if err != nil {
		return nil, err
	}
	var advisories []apiAdvisory
	var cves []string
	for i, refs := range results {
		t := targets[i]
		for _, ref := range refs {
			record := records[ref.ID]
			advisory := apiAdvisory{ID: record.ID, Package: t.name, Version: t.version, Ecosystem: t.ecosystem, Part: t.part,
				Summary: firstNonEmpty(record.Summary, qFirstLine(record.Details, "")), CVEs: record.cves(), Fixed: record.fixed(t.ecosystem, t.name),
				Severity: record.severity(), Modified: record.Modified}
			if len(advisory.Summary) > 200 {
				advisory.Summary = advisory.Summary[:200] + "…"
			}
			for _, cve := range advisory.CVEs {
				advisory.KEV = advisory.KEV || kev[cve]
			}
			cves = append(cves, advisory.CVEs...)
			advisories = append(advisories, advisory)
		}
	}
	scores := feeds.scores(cves, now)
	for i := range advisories {
		for _, cve := range advisories[i].CVEs {
			advisories[i].EPSS = max(advisories[i].EPSS, scores[cve])
		}
	}
	sort.SliceStable(advisories, func(i, j int) bool {
		a, b := advisories[i], advisories[j]
		if a.KEV != b.KEV {
			return a.KEV
		}
		if a.EPSS != b.EPSS {
			return a.EPSS > b.EPSS
		}
		return a.ID+a.Package < b.ID+b.Package
	})
	return advisories, nil
}

// advisoryAction is what fixes an advisory in a part, for the incident.
func advisoryAction(team string, advisory apiAdvisory) string {
	fixed := "no fixed version yet"
	if len(advisory.Fixed) > 0 {
		fixed = "fixed in " + strings.Join(advisory.Fixed, ", ")
	}
	switch {
	case advisory.Part == "app":
		return fmt.Sprintf("The app's %s package %s %s (%s). The lead proposes the upgrade and waits for a yes, as for any work, then asks for the release.", advisory.Ecosystem, advisory.Package, advisory.Version, fixed)
	case strings.HasPrefix(advisory.Part, "image:base"):
		return fmt.Sprintf("In hi's base image, which the app and the lead run in: %s %s (%s). The lead can't change it: someone at the machine removes the image (podman rmi with the image in hi team bom %s) and restarts the team (hi team up %s), which builds it again with Ubuntu's current packages.", advisory.Package, advisory.Version, fixed, team, team)
	case strings.HasPrefix(advisory.Part, "image:"):
		return fmt.Sprintf("In the image of the lead's memory (Hindsight): %s %s (%s). A fix comes with a hi that uses a newer image: hi update on the machine.", advisory.Package, advisory.Version, fixed)
	case advisory.Part == "agent":
		return fmt.Sprintf("In a coding agent on the machine: %s %s (%s). hi install upgrade on the machine updates it.", advisory.Package, advisory.Version, fixed)
	}
	return fmt.Sprintf("In %s %s (%s): hi update on the machine.", advisory.Package, advisory.Version, fixed)
}

// ---------------------------------------------------------------------------
// the server's side

// apiBom is a bill a team sends when the server asks for it.
type apiBom struct {
	Team    string `json:"team"`
	Version string `json:"version"`
	Digest  string `json:"digest"`
	Bom     []byte `json:"bom"`
}

// apiWatchReport is a weekly report for the team's channel.
type apiWatchReport struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

func (s *hiServer) handleBom(w http.ResponseWriter, _ *http.Request, device serverDevice, body []byte) {
	var input apiBom
	if err := json.Unmarshal(body, &input); err != nil || !teamNamePattern.MatchString(input.Team) || len(input.Bom) == 0 {
		writeAPIError(w, http.StatusBadRequest, "malformed bill of materials")
		return
	}
	sum := sha256.Sum256(input.Bom)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	var bom cdxBom
	if digest != input.Digest || json.Unmarshal(input.Bom, &bom) != nil || bom.Format != "CycloneDX" {
		writeAPIError(w, http.StatusBadRequest, "the bill isn't CycloneDX, or doesn't match its digest")
		return
	}
	w2 := s.watch
	w2.mu.Lock()
	team := w2.state.Teams[input.Team]
	if team == nil || team.Device != device.Fingerprint {
		w2.mu.Unlock()
		writeAPIError(w, http.StatusForbidden, "send a heartbeat for team "+input.Team+" from its machine first")
		return
	}
	dir := filepath.Join(w2.dir, "teams", input.Team, "boms")
	os.MkdirAll(dir, 0o700)
	if err := writeFileAtomic(filepath.Join(dir, strings.TrimPrefix(digest, "sha256:")+".cdx.json"), input.Bom, 0o600); err != nil {
		w2.mu.Unlock()
		writeAPIError(w, http.StatusInternalServerError, err.Error())
		return
	}
	team.Bom, team.BomVersion, team.Matched = digest, input.Version, time.Time{}
	w2.saveLocked()
	w2.mu.Unlock()
	s.audit(device.User, "bill of materials", input.Team, input.Version+" "+digest[:19])
	go s.matchTeam(input.Team)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// matchAdvisoriesDue matches every team's bill that wasn't matched in the
// last day.
func (s *hiServer) matchAdvisoriesDue(now time.Time) {
	w := s.watch
	w.mu.Lock()
	var due []string
	for name, team := range w.state.Teams {
		if team.Bom != "" && now.Sub(team.Matched) >= advisoryMatchEvery && now.Sub(team.MatchTried) >= time.Hour {
			due = append(due, name)
		}
	}
	w.mu.Unlock()
	sort.Strings(due)
	for _, name := range due {
		s.matchTeam(name)
	}
}

// matchTeam matches one team's production bill, opens and closes its
// advisory incidents, and writes its weekly report when one is due.
func (s *hiServer) matchTeam(name string) {
	w := s.watch
	w.matchMu.Lock()
	defer w.matchMu.Unlock()
	now := computeNow()
	w.mu.Lock()
	team := w.state.Teams[name]
	if team == nil || team.Bom == "" {
		w.mu.Unlock()
		return
	}
	team.MatchTried = now
	path := filepath.Join(w.dir, "teams", name, "boms", strings.TrimPrefix(team.Bom, "sha256:")+".cdx.json")
	w.mu.Unlock()
	var bom cdxBom
	data, err := os.ReadFile(path)
	if err == nil {
		err = json.Unmarshal(data, &bom)
	}
	var advisories []apiAdvisory
	if err == nil {
		advisories, err = advisoryMatch(bom, w.dir, w.feeds, now)
	}
	if err != nil {
		fmt.Fprintf(s.log, "watch: matching %s's bill: %v\n", name, err)
		return
	}
	w.mu.Lock()
	team = w.state.Teams[name]
	if team == nil {
		w.mu.Unlock()
		return
	}
	for i := range advisories {
		for _, ignore := range team.Beat.Ignored {
			if ignore.ID == advisories[i].ID && ignore.Package == advisories[i].Package && ignore.Modified == advisories[i].Modified {
				advisories[i].Ignored = firstNonEmpty(ignore.Because, "ignored")
			}
		}
	}
	team.Advisories, team.Matched = advisories, now
	data, _ = json.Marshal(advisories)
	os.MkdirAll(filepath.Dir(w.advisoriesPath(name)), 0o700)
	writeFileAtomic(w.advisoriesPath(name), data, 0o600)
	events := w.advisoryRulesLocked(team, now)
	report := w.reportLocked(team, now)
	w.saveLocked()
	w.mu.Unlock()
	s.watchEvents(events)
	if report != "" {
		s.watchPost("", report, false)
	}
}

// advisoryRulesLocked opens an incident for each advisory loud enough,
// and closes those no longer matched or now ignored.
func (w *serverWatch) advisoryRulesLocked(team *watchTeam, now time.Time) []watchEvent {
	var events []watchEvent
	current := map[string]bool{}
	for _, advisory := range team.Advisories {
		tier := advisory.tier()
		if tier != "kev" && tier != "incident" {
			continue
		}
		rule := "advisory:" + advisory.ID + ":" + advisory.Package
		current[rule] = true
		opened := w.ruleLocked(team, rule, true, now, func() watchIncident {
			why := "known to be exploited (CISA KEV)"
			if tier == "incident" {
				why = fmt.Sprintf("EPSS %.2f", advisory.EPSS)
				if advisory.Severity == "critical" {
					why = "critical"
				}
			}
			return watchIncident{Title: fmt.Sprintf("%s's production runs %s %s, which has %s, %s", team.Team, advisory.Package, advisory.Version, advisory.ID, why),
				Detail: strings.TrimSpace(advisory.Summary + "\n" + advisoryAction(team.Team, advisory) + "\nhttps://osv.dev/vulnerability/" + advisory.ID),
				Tell:   advisory.Part == "app", Push: tier == "kev"}
		}, "")
		events = append(events, opened...)
		// Production's own code exploited in the wild: the team freezes
		// until an admin looks (an image's, the agents', and hi's wait
		// for one: the app may never touch that package).
		if len(opened) > 0 && tier == "kev" && advisory.Part == "app" && w.config.AutoFreeze != "off" {
			w.wantLocked(team, "freeze", "watch", fmt.Sprintf("production runs %s %s, which has %s, known to be exploited", advisory.Package, advisory.Version, advisory.ID), now)
		}
	}
	for rule := range team.Open {
		if strings.HasPrefix(rule, "advisory:") && !current[rule] {
			cleared := "Production no longer runs the package at that version."
			for _, advisory := range team.Advisories {
				if "advisory:"+advisory.ID+":"+advisory.Package == rule && advisory.Ignored != "" {
					cleared = "An owner said it doesn't apply: " + advisory.Ignored
				}
			}
			events = append(events, w.ruleLocked(team, rule, false, now, nil, cleared)...)
		}
	}
	return events
}

// reportLocked writes the team's weekly report when one is due: the
// advisories not loud enough for an incident that have a fix, and once,
// the ones without. It is posted in the watch channel, and the team's
// channel hears of its app's.
func (w *serverWatch) reportLocked(team *watchTeam, now time.Time) string {
	if now.Sub(team.Reported) < advisoryReportEvery {
		return ""
	}
	if team.NoFixTold == nil {
		team.NoFixTold = map[string]bool{}
	}
	var fixable, noFix, appLines []string
	listed, counts := teamFacingAdvisories(team.Advisories)
	for _, advisory := range listed {
		if advisory.tier() != "report" {
			continue
		}
		line := fmt.Sprintf("%s %s: %s", advisory.Package, advisory.Version, advisory.ID)
		if advisory.Severity != "" {
			line += " (" + advisory.Severity + ")"
		}
		key := advisory.ID + ":" + advisory.Package
		switch {
		case len(advisory.Fixed) > 0:
			line += ", fixed in " + strings.Join(advisory.Fixed, ", ")
			fixable = append(fixable, line)
		case !team.NoFixTold[key]:
			team.NoFixTold[key] = true
			noFix = append(noFix, line)
		default:
			continue
		}
		if advisory.Part == "app" {
			appLines = append(appLines, line)
		}
	}
	team.Reported = now
	if len(fixable)+len(noFix)+len(counts) == 0 {
		return ""
	}
	var text strings.Builder
	fmt.Fprintf(&text, ":page_facing_up: *%s's weekly advisories* (production %s): %d with a fix, %d new without one.\n", team.Team, firstNonEmpty(team.BomVersion, "?"), len(fixable), len(noFix))
	for _, line := range teamClipLines(append(fixable, noFix...), 15) {
		text.WriteString("• " + line + "\n")
	}
	for _, count := range counts {
		fmt.Fprintf(&text, "• %s: %d advisories for its OS packages, %d of them with a fix in a newer package", advisoryPartName(count.Part), count.Total, count.Fixable)
		if count.Fixable > 0 && count.Part == "image:base" {
			fmt.Fprintf(&text, ": building the image again picks those up (on the machine, podman rmi the image hi team bom %s names, then hi team up %s)", team.Team, team.Team)
		}
		text.WriteString("\n")
	}
	if len(appLines) > 0 {
		var app strings.Builder
		fmt.Fprintf(&app, ":page_facing_up: *This week's advisories for the app's packages*, none urgent (hi watch):\n")
		for _, line := range teamClipLines(appLines, 15) {
			app.WriteString("• " + line + "\n")
		}
		app.WriteString("The lead can bring these up to date in one small task when it suits; `hi team advisories` lists them.")
		team.Report = &apiWatchReport{ID: "r-" + now.UTC().Format("2006-01-02"), Text: app.String()}
	}
	return strings.TrimSpace(text.String())
}

// advisoryPartName says which part of the team a bill's part is.
func advisoryPartName(part string) string {
	switch part {
	case "image:base":
		return "hi's base image"
	case "image:memory":
		return "the memory image (Hindsight)"
	}
	return part
}

func teamClipLines(lines []string, limit int) []string {
	if len(lines) <= limit {
		return lines
	}
	return append(lines[:limit:limit], fmt.Sprintf("and %d more", len(lines)-limit))
}
