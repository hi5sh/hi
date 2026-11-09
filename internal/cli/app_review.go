package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// A deep review is hi agent review of the team's project, run by the
// broker once an owner says yes to the lead's offer, with a budget the
// lead named. The broker gives the reviewers what the team knows that the
// code doesn't show, keeps the report in reviews/, sends it to the owner
// who said yes, and wakes the lead in the thread to weigh the findings
// with the people. See docs/specs/approved/hi_agent_review.md.

// appDeepReview is one deep review, in reviews.json.
type appDeepReview struct {
	ID       string       `json:"id"` // hi agent review's, such as r-4
	Approval *appApproval `json:"approval"`
	Owner    string       `json:"owner"` // the owner who said yes, by member ID
	Budget   float64      `json:"budget_usd"`
	Areas    []string     `json:"areas"`
	Commit   string       `json:"commit"`
	Version  string       `json:"version,omitempty"` // v3, or "" for dev
	Since    string       `json:"since,omitempty"`
	Compare  string       `json:"compare,omitempty"` // the earlier review it was compared with
	Status   string       `json:"status"`            // running, done, stopped, or failed
	Started  time.Time    `json:"started"`
	Ended    time.Time    `json:"ended,omitzero"`
	// Told says the report went to the owner and the lead was woken, or,
	// for the dev box's, into its home.
	Told bool `json:"told,omitempty"`
	// Dev says an owner in the dev box asked for it.
	Dev bool `json:"dev,omitempty"`
}

// appDeepReviewRequest is the lead's hi app review --project.
type appDeepReviewRequest struct {
	Budget  float64 `json:"budget_usd"`
	Areas   string  `json:"areas,omitempty"`
	Since   string  `json:"since,omitempty"`
	Version string  `json:"version,omitempty"`
	// Context is what the people said that bears on the review, in the
	// lead's words.
	Context string `json:"context,omitempty"`
}

// appTaskFinding is a review's finding a task sets out to fix.
type appTaskFinding struct {
	Review      string `json:"review"`
	ID          string `json:"id"`
	Title       string `json:"title"`
	Fingerprint string `json:"fingerprint"`
}

var (
	appDeepReviewMu sync.Mutex
	// appDeepReviewPoll is how often the broker looks at a running review;
	// tests shorten it.
	appDeepReviewPoll = 30 * time.Second
	appFindingRef     = regexp.MustCompile(`^(r-[0-9]{1,6}):(F-[0-9]{1,4})$`)
	appDollars        = regexp.MustCompile(`\$\s?([0-9]+(?:\.[0-9]{1,2})?)`)
)

func appReviewsDir(team string) string  { return filepath.Join(appDir(team), "reviews") }
func appReviewsPath(team string) string { return filepath.Join(appDir(team), "reviews.json") }

func loadTeamDeepReviews(team string) []appDeepReview {
	var reviews []appDeepReview
	if data, err := os.ReadFile(appReviewsPath(team)); err == nil {
		json.Unmarshal(data, &reviews)
	}
	return reviews
}

// saveTeamDeepReview adds or replaces one review in reviews.json.
func saveTeamDeepReview(team string, review appDeepReview) error {
	appDeepReviewMu.Lock()
	defer appDeepReviewMu.Unlock()
	reviews := loadTeamDeepReviews(team)
	replaced := false
	for i := range reviews {
		if reviews[i].ID == review.ID {
			reviews[i], replaced = review, true
		}
	}
	if !replaced {
		reviews = append(reviews, review)
	}
	data, _ := json.MarshalIndent(reviews, "", "  ")
	return writeFileAtomic(appReviewsPath(team), append(data, '\n'), 0o600)
}

// appDeepReviewRunning refuses a second review while one runs.
func appDeepReviewRunning(team string) error {
	for _, earlier := range loadTeamDeepReviews(team) {
		if earlier.Status == "running" {
			return fmt.Errorf("deep review %s is still running; wait for it, or stop it with hi app review stop %s", earlier.ID, earlier.ID)
		}
	}
	return nil
}

// launchDeepReview starts a review whose yes and budget were checked:
// what to review, what the team knows for the reviewers, and the agents.
func (b *appBroker) launchDeepReview(config appConfig, request appDeepReviewRequest, record appDeepReview) (appDeepReview, error) {
	var err error
	versions, _ := loadTeamVersions(b.team)
	release := func(version string) (string, error) {
		number, _ := strconv.Atoi(strings.TrimPrefix(version, "v"))
		for _, r := range versions.Releases {
			if r.Version == number {
				return r.Commit, nil
			}
		}
		return "", fmt.Errorf("there's no %s; hi app versions lists them", version)
	}
	if request.Version != "" {
		if !appVersionArg.MatchString(request.Version) {
			return appDeepReview{}, fmt.Errorf("--version is a version such as v3, not %q", request.Version)
		}
		if record.Commit, err = release(request.Version); err != nil {
			return appDeepReview{}, err
		}
	} else if record.Commit = boxGit(b.repo, "rev-parse", "--verify", "-q", config.Branch); record.Commit == "" {
		return appDeepReview{}, fmt.Errorf("the project has no %s branch yet", config.Branch)
	}
	since := request.Since
	if appVersionArg.MatchString(since) {
		if since, err = release(since); err != nil {
			return appDeepReview{}, err
		}
	}
	record.Since = request.Since

	if err := os.MkdirAll(appReviewsDir(b.team), 0o700); err != nil {
		return appDeepReview{}, err
	}
	contextFile := filepath.Join(appReviewsDir(b.team), "context.md")
	if err := os.WriteFile(contextFile, []byte(b.deepReviewContext(config, versions, request.Context)), 0o600); err != nil {
		return appDeepReview{}, err
	}
	args := []string{"agent", "review", b.repo, "--name", b.team, "--ref", record.Commit, "--context", contextFile,
		"--budget", strconv.FormatFloat(request.Budget, 'f', 2, 64), "--yes", "--detach", "--json"}
	if request.Areas != "" {
		args = append(args, "--area", request.Areas)
	}
	if since != "" {
		args = append(args, "--since", since)
	}
	// The coder's and the reviewer's agents take turns, and each finding
	// goes to the one that didn't raise it: a team's reviewer is already
	// another agent or model than its coder.
	var agents []string
	for _, role := range []string{"coder", "reviewer"} {
		if member, ok := config.Members[role]; ok && agentKinds[member.Agent] {
			agent := member.Agent
			if member.Model != "" {
				agent += ":" + member.Model
			}
			if !containsString(agents, agent) {
				agents = append(agents, agent)
			}
		}
	}
	if len(agents) > 0 {
		args = append(args, "--agents", strings.Join(agents, ","))
	}
	// Compared with the last review that ended, saying which findings a
	// merged task set out to fix.
	reviews := loadTeamDeepReviews(b.team)
	for i := len(reviews) - 1; i >= 0; i-- {
		if reviews[i].Status == "done" && fileExists(reviewStateFile(reviews[i].ID, "report.json")) {
			record.Compare = reviews[i].ID
			args = append(args, "--compare", reviews[i].ID)
			break
		}
	}
	fixes := map[string]string{}
	for _, task := range loadTeamTasks(b.team) {
		if task.Status == "merged" {
			for _, finding := range task.Findings {
				fixes[finding.Fingerprint] = task.ID
			}
		}
	}
	if len(fixes) > 0 {
		data, _ := json.Marshal(fixes)
		fixesFile := filepath.Join(appReviewsDir(b.team), "fixes.json")
		if err := os.WriteFile(fixesFile, data, 0o600); err != nil {
			return appDeepReview{}, err
		}
		args = append(args, "--fixes", fixesFile)
	}
	out, err := b.run(args...)
	if err != nil {
		return appDeepReview{}, fmt.Errorf("hi agent review: %w", err)
	}
	var started review
	if err := json.Unmarshal(out, &started); err != nil || started.ID == "" {
		return appDeepReview{}, fmt.Errorf("hi agent review: %s", qFirstLine(string(out), "no review started"))
	}
	record.ID, record.Areas, record.Started = started.ID, started.Areas, time.Now().UTC()
	if err := saveTeamDeepReview(b.team, record); err != nil {
		return record, err
	}
	if approval := record.Approval; b.channel != nil && !record.Dev {
		thread := firstNonEmpty(approval.Thread, approval.TS)
		b.channel.postIn(config.Channel, thread, fmt.Sprintf("Deep review %s started on %s: %s, up to $%s, as %s agreed. Nothing in the project is changed; the lead hears when it ends.",
			record.ID, appReviewWhat(record), strings.Join(record.Areas, " and "), strconv.FormatFloat(record.Budget, 'f', -1, 64), appSlackEscape(approval.Name)))
	}
	go b.watchDeepReview(record.ID)
	return record, nil
}

func appReviewWhat(review appDeepReview) string {
	what := "dev (" + shortCommit(review.Commit) + ")"
	if review.Version != "" {
		what = review.Version
	}
	if review.Since != "" {
		what += ", what changed since " + review.Since
	}
	return what
}

// deepReviewContext is what the team knows that the code doesn't show:
// the purpose, where production is and who can reach it, the decisions,
// and what the lead adds from the people.
func (b *appBroker) deepReviewContext(config appConfig, versions appVersions, lead string) string {
	var c strings.Builder
	fmt.Fprintf(&c, "This is the project of an app that agents build in a Slack channel, for the people in it.\n\nWhat it's for: %s\n\n", config.Purpose)
	var production appPreview
	if data, err := os.ReadFile(appSlotPath(b.team, appSlotProduction)); err == nil && json.Unmarshal(data, &production) == nil && production.URL != "" {
		// The address without its key: the key is what lets people in,
		// and it isn't the reviewers' to have.
		address, guard := production.URL, "Anyone who has the address can open it; there's no login page in front of it."
		if parsed, err := url.Parse(production.URL); err == nil {
			if parsed.Query().Get(netExposeKeyParam) != "" {
				guard = "Its link carries a secret key: without the key, the app doesn't open, and the page asks for the link. Only the people the app's team gives the link to can reach it."
			}
			parsed.RawQuery, parsed.Fragment = "", ""
			address = parsed.String()
		}
		if production.Password != "" {
			guard = "A login page with a shared password is in front of it."
		}
		fmt.Fprintf(&c, "Production (v%d) is at %s, reachable from the internet. %s\n\n", versions.Production, address, guard)
	} else {
		c.WriteString("Production isn't running at an address yet.\n\n")
	}
	fmt.Fprintf(&c, "It has %s, who build it; anyone in the app's channel may suggest changes.\n\n", plural(len(config.Owners), "owner"))
	if entries, err := os.ReadDir(filepath.Join(b.repo, "docs", "decisions")); err == nil {
		var decisions []string
		for _, entry := range entries {
			if strings.HasSuffix(entry.Name(), ".md") {
				decisions = append(decisions, "- docs/decisions/"+entry.Name())
			}
		}
		if len(decisions) > 0 {
			c.WriteString("The app's technical decisions are in docs/decisions/; read them before calling something a mistake:\n\n" + strings.Join(decisions, "\n") + "\n\n")
		}
	}
	if lead = strings.TrimSpace(lead); lead != "" {
		c.WriteString("From the lead, what the people have said:\n\n" + lead + "\n")
	}
	return c.String()
}

// watchDeepReview waits for a review to end, then keeps its report in
// reviews/<id>/, sends it to the owner who said yes, and wakes the lead
// in the thread it was agreed in.
func (b *appBroker) watchDeepReview(id string) {
	for {
		rv, err := loadReview(id)
		if err != nil {
			b.endDeepReview(id, "failed", nil)
			return
		}
		if rv.State != "running" {
			b.endDeepReview(id, rv.State, &rv)
			return
		}
		time.Sleep(appDeepReviewPoll)
	}
}

func (b *appBroker) endDeepReview(id, state string, rv *review) {
	var record *appDeepReview
	for _, r := range loadTeamDeepReviews(b.team) {
		if r.ID == id {
			record = &r
		}
	}
	if record == nil || record.Told {
		return
	}
	record.Status, record.Ended = state, time.Now().UTC()
	// A review whose record is gone has nothing to tell.
	record.Told = rv == nil
	saveTeamDeepReview(b.team, *record)
	config, err := loadTeam(b.team)
	if err != nil || rv == nil {
		return
	}
	// The report stays in the team's folder, read-only in the lead's box.
	folder := filepath.Join(appReviewsDir(b.team), id)
	if err := os.MkdirAll(folder, 0o700); err == nil {
		writeReviewHTML(*rv)
		for _, name := range []string{"report.md", "report.html", "report.json"} {
			if data, err := os.ReadFile(reviewStateFile(id, name)); err == nil {
				os.WriteFile(filepath.Join(folder, name), data, 0o600)
			}
		}
	}
	// The dev box's own goes into its home, for the agent that asked.
	if record.Dev {
		appCopyTreeIn(appDevHome(b.team), folder, appDevResultsDir(b.team, "reviews", id))
		record.Told = true
		saveTeamDeepReview(b.team, *record)
		return
	}
	// The whole report goes to the owner who said yes; the channel gets
	// counts and titles from the lead.
	sent := false
	if b.channel != nil && record.Owner != "" {
		if im, _, err := b.channel.dm(record.Owner, fmt.Sprintf("The deep review you agreed to (%s) has ended: %s. Here is the full report; the lead posts a summary in the thread. %s",
			id, reviewTallyText(*rv), reviewHandoverLine)); err == nil {
			sent = b.channel.upload(im, "", filepath.Join(folder, "report.md"), "Review "+id) == nil
			b.channel.upload(im, "", filepath.Join(folder, "report.html"), "Review "+id+" (to open in a browser)")
		}
	}
	if b.wakeLead != nil {
		thread := firstNonEmpty(record.Approval.Thread, record.Approval.TS)
		if err := b.wakeLead("hi-review-"+id, appDeepReviewPrompt(*record, *rv, sent), "slack:"+config.Channel+":"+thread); err != nil {
			return
		}
	}
	record.Told = true
	saveTeamDeepReview(b.team, *record)
}

// appDeepReviewPrompt wakes the lead when a review ends.
func appDeepReviewPrompt(record appDeepReview, rv review, sent bool) string {
	owner := firstNonEmpty(record.Approval.Name, "the owner")
	delivered := fmt.Sprintf("hi sent %s the full report in a DM.", owner)
	if !sent {
		delivered = fmt.Sprintf("hi couldn't send %s the report in a DM; tell them it's on the app's machine (hi agent review show %s --md).", owner, rv.ID)
	}
	compared := ""
	if line := reviewCompareCounts(rv); line != "" {
		compared = " Compared with the last review, " + line + "."
	}
	ended := "It has ended: " + reviewTallyText(rv) + "."
	if rv.State != "done" {
		ended = fmt.Sprintf("It %s. What it found so far: %s.", reviewStateText(rv), reviewTallyText(rv))
	}
	return fmt.Sprintf(`This is hi, not a person. The deep review %[1]s that %[2]s agreed to (%[3]s) is over. %[4]s%[5]s %[6]s

The report is /team/reviews/%[1]s/report.md. Read it, then write for the people in this thread. They may not read code: leave out file, function, and route names, and jargon such as CSRF, SSRF, or JSON, unless they ask. Say what could go wrong for them, in their words: "someone with the link could stop the history from filling in", not "POST /api/history/pause has no auth". The report's titles are for the developers; write each finding in a line of your own, with its number (F-1) so people can name it.

- How many findings were confirmed, by severity, each in that line of yours, and nothing about how to exploit them.
- That these are recommendations to weigh, not requirements: the reviewers read the code and didn't know how the app is run, who uses it, or what protects it.
- Which findings you think matter for this app and why, and which you think don't, because of how the app is run, who uses it, or what protects it (say what each one assumes when that's the reason).
- The questions you can't settle yourself, about those assumptions.

Don't offer to fix everything; the people choose. Remember what they decide, so a finding they set aside isn't raised again as news. For what they choose, file a ticket for the owners (hi app ticket add), with the finding's number (%[1]s:F-1) and its section from the report.`,
		rv.ID, owner, appReviewWhat(record), ended, compared, delivered)
}

// resumeDeepReviews watches the reviews that were running, or ended
// without the lead hearing, when the broker stopped.
func (b *appBroker) resumeDeepReviews() {
	for _, review := range loadTeamDeepReviews(b.team) {
		if review.Status == "running" || !review.Told {
			go b.watchDeepReview(review.ID)
		}
	}
}

func (b *appBroker) stopDeepReview(id string) (string, error) {
	for _, review := range loadTeamDeepReviews(b.team) {
		if review.ID == id {
			out, err := b.run("agent", "review", "stop", id)
			return strings.TrimSpace(string(out)), err
		}
	}
	return "", fmt.Errorf("no deep review %s; hi app review list shows them", id)
}

// showDeepReviews is the team's deep reviews, or one review's findings.
func (b *appBroker) showDeepReviews(id string) (string, error) {
	var out bytes.Buffer
	reviews := loadTeamDeepReviews(b.team)
	if id == "" {
		if len(reviews) == 0 {
			return "No deep reviews yet.", nil
		}
		for _, r := range reviews {
			fmt.Fprintf(&out, "%s  %-8s %s, %s, up to $%s, agreed by %s on %s\n", r.ID, r.Status, appReviewWhat(r), strings.Join(r.Areas, ", "),
				strconv.FormatFloat(r.Budget, 'f', -1, 64), r.Approval.Name, r.Started.Format("2006-01-02"))
		}
		return strings.TrimRight(out.String(), "\n"), nil
	}
	for _, r := range reviews {
		if r.ID == id {
			rv, err := loadReview(id)
			if err != nil {
				return "", err
			}
			printReviewSummary(rv, false, &out)
			fmt.Fprintf(&out, "\nThe report: /team/reviews/%s/report.md, once it ends.\n", id)
			return out.String(), nil
		}
	}
	return "", fmt.Errorf("no deep review %s; hi app review list shows them", id)
}
