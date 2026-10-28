package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// teamDocsTestRepo commits files to the team's repository and puts the
// commit on the preview.
func teamDocsTestRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	repo := filepath.Join(teamDir("payments"), "repo")
	for name, text := range files {
		path := filepath.Join(repo, name)
		os.MkdirAll(filepath.Dir(path), 0o700)
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
		boxGit(repo, "add", name)
	}
	boxGit(repo, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-qm", "docs")
	commit := boxGit(repo, "rev-parse", "HEAD")
	os.MkdirAll(filepath.Join(teamDir("payments"), "run"), 0o700)
	saveTeamSlot("payments", teamPreview{Slot: teamSlotPreview, Label: "dev, v1 + 1 change", Commit: commit})
	return commit
}

var teamDocsTestFiles = map[string]string{
	"docs/user/index.md":  "# Payments\n\n## Why it exists\n\nSuppliers get paid on time.\n\nSee [orders](orders.md#adding) and ![the list](pic.png).\n\n<script>alert(1)</script>\n",
	"docs/user/orders.md": "# Orders\n\n## Adding\n\nPress **Add**.\n",
	"docs/user/pic.png":   "\x89PNG",
	"docs/tech/index.md": "# How Payments is built\n\nRead [the map](../map.md) and [why SQLite](../decisions/0001-sqlite.md).\n\n" +
		"```mermaid\nsequenceDiagram\n    A->>B: <hi>\n```\n\n" +
		"```boxes\n{\"caption\": \"How it runs\", \"frame\": \"the machine\", \"frame_mark\": 1, \"groups\": [{\"name\": \"app\", \"items\": [{\"title\": \"src/server.ts\", \"role\": \"Bun\", \"does\": \"Serves `$PORT`.\", \"chip\": \"Bun\", \"tone\": \"nope\"}]}, {\"name\": \"db\", \"via\": \"SQL\", \"items\": []}], \"key\": [{\"mark\": 1, \"term\": \"Machine\", \"text\": \"Runs `hi`.\"}]}\n```\n\n" +
		"```boxes\n{not json\n```\n\n```go\nfmt.Println(\"<b>\")\n```\n",
	"docs/tech/runbooks/restore.md":  "# Restoring a snapshot\n",
	"docs/map.md":                    "# Map of Payments\n",
	"docs/decisions/0001-sqlite.md":  "# SQLite, not Postgres\n",
	"docs/specs/README.md":           "# Specs\n",
	"docs/specs/suppliers.md":        "# Suppliers\n",
	"docs/specs/notes/openrouter.md": "# How OpenRouter behaves\n",
	"src/server.ts":                  "secret code\n",
}

func TestTeamDocsPages(t *testing.T) {
	setupTeamTest(t)
	newTeamTestBroker(t)
	commit := teamDocsTestRepo(t, teamDocsTestFiles)
	version := loadTeamDocs(filepath.Join(teamDir("payments"), "repo"), commit)
	var user, tech []string
	for _, page := range version.User {
		user = append(user, page.URL()+" "+page.Title)
	}
	for _, page := range version.Tech {
		tech = append(tech, page.Group+"|"+page.URL()+" "+page.Title)
	}
	if got := strings.Join(user, "\n"); got != "/docs/user/ Payments\n/docs/user/orders Orders" {
		t.Fatal(got)
	}
	// The tech docs' own pages, the map, and the changelog come first, then
	// the decisions, the specs, and the lead's notes, each a group.
	want := strings.Join([]string{
		"|/docs/tech/ How Payments is built",
		"|/docs/tech/map Map of Payments",
		"|/docs/tech/changelog Changelog",
		"Runbooks|/docs/tech/runbooks/restore Restoring a snapshot",
		"Decisions|/docs/tech/decisions/0001-sqlite SQLite, not Postgres",
		"Specs|/docs/tech/specs Specs",
		"Specs|/docs/tech/specs/suppliers Suppliers",
		"Notes|/docs/tech/specs/notes/openrouter How OpenRouter behaves",
	}, "\n")
	if got := strings.Join(tech, "\n"); got != want {
		t.Fatalf("%s\nwant\n%s", got, want)
	}
	if _, ok := version.Files["src/server.ts"]; ok {
		t.Fatal("the docs read the code")
	}

	// A project from before the user docs: HELP.md is its user docs.
	old := teamDocsTestRepo(t, nil)
	boxGit(filepath.Join(teamDir("payments"), "repo"), "rm", "-rq", "docs/user")
	os.WriteFile(filepath.Join(teamDir("payments"), "repo", "HELP.md"), []byte("# Help\n"), 0o600)
	boxGit(filepath.Join(teamDir("payments"), "repo"), "add", "HELP.md")
	boxGit(filepath.Join(teamDir("payments"), "repo"), "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-qm", "help")
	old = boxGit(filepath.Join(teamDir("payments"), "repo"), "rev-parse", "HEAD")
	if pages := loadTeamDocs(filepath.Join(teamDir("payments"), "repo"), old).User; len(pages) != 1 || pages[0].File != "HELP.md" || pages[0].URL() != "/docs/user/" {
		t.Fatalf("%+v", pages)
	}
}

func TestTeamDocsRender(t *testing.T) {
	setupTeamTest(t)
	newTeamTestBroker(t)
	commit := teamDocsTestRepo(t, teamDocsTestFiles)
	version := loadTeamDocs(filepath.Join(teamDir("payments"), "repo"), commit)

	body, mermaid := version.render(version.Files["docs/user/index.md"], "docs/user/index.md")
	html := string(body)
	if mermaid || !strings.Contains(html, `href="/docs/user/orders#adding"`) || !strings.Contains(html, `src="/docs/raw/docs/user/pic.png"`) ||
		strings.Contains(html, "<script>") {
		t.Fatal(html)
	}

	body, mermaid = version.render(version.Files["docs/tech/index.md"], "docs/tech/index.md")
	html = string(body)
	for _, want := range []string{
		`href="/docs/tech/map"`, `href="/docs/tech/decisions/0001-sqlite"`,
		// A chart is drawn in the browser, from its text as written.
		`<pre class="mermaid">sequenceDiagram
    A-&gt;&gt;B: &lt;hi&gt;
</pre>`,
		// The hi guide's structure diagram: the frame, its mark, the groups
		// with the arrow between, a tone it knows, and the key.
		`<figure class="hd"><figcaption class="hd-sr">How it runs</figcaption>`,
		`<span class="hd-frametag"><span class="hd-mk">1</span>the machine</span>`,
		`<span class="hd-arrow"><em>SQL</em></span>`,
		`<p class="hd-does">Serves <code>$PORT</code>.</p>`,
		`hd-tone-dim`, `<dd>Runs <code>hi</code>.</dd>`,
		// A diagram that doesn't parse says so, with what was written.
		`<div class="docs-broken"><p>This diagram can't be drawn:`,
		`<pre><code class="language-go">fmt.Println(&#34;&lt;b&gt;&#34;)`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("no %s in\n%s", want, html)
		}
	}
	if !mermaid {
		t.Fatal("no chart")
	}
}

func TestTeamDocsServe(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	b.help = func(context.Context, string, []teamHelpTurn, func(string)) (string, error) { return "", nil }
	teamDocsTestRepo(t, teamDocsTestFiles)
	release := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
	saveTeamVersions("payments", teamVersions{Production: 1, Releases: []teamRelease{{Version: 1, Commit: "abc", Notes: "The first <one>", Changes: []string{"t-4 Suppliers #1"}, Released: release}}})
	server := webTestServer(t, b.teamBroker, teamSlotPreview, "", "")
	defer server.Close()
	get := func(path string) (int, string, string) {
		t.Helper()
		response, err := http.Get(server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		body, _ := io.ReadAll(response.Body)
		return response.StatusCode, response.Header.Get("Content-Type"), string(body)
	}

	// The first page: two ways in, and the helper.
	status, kind, body := get("/docs/")
	if status != 200 || !strings.HasPrefix(kind, "text/html") || !strings.Contains(body, `<a class="choice" href="/docs/user/">`) ||
		!strings.Contains(body, `<a class="choice" href="/docs/tech/">`) || !strings.Contains(body, `<script src="/__hi/helper.js" defer>`) ||
		!strings.Contains(body, "preview: dev, v1 &#43; 1 change") || !strings.Contains(body, "2 pages") {
		t.Fatal(body)
	}
	// A part: its menu, the page, where it comes from; the charts' script
	// only where a page has a chart.
	_, _, body = get("/docs/user/orders")
	if !strings.Contains(body, `<a href="/docs/user/orders" aria-current="page">Orders</a>`) || !strings.Contains(body, "<strong>Add</strong>") ||
		!strings.Contains(body, `From <a href="/docs/raw/docs/user/orders.md">docs/user/orders.md</a>`) || strings.Contains(body, teamDocsCharts) {
		t.Fatal(body)
	}
	if _, _, body = get("/docs/tech/"); !strings.Contains(body, teamDocsCharts) || !strings.Contains(body, `<p class="menu-group">Decisions</p>`) {
		t.Fatal(body)
	}
	// The script that draws the charts, from hi, kept by the browser.
	if status, kind, body = get(teamDocsCharts); status != 200 || kind != "text/javascript; charset=utf-8" || body != string(teamDocsPele) {
		t.Fatal(status, kind, len(body))
	}
	request, _ := http.NewRequest("GET", server.URL+teamDocsCharts, nil)
	request.Header.Set("If-None-Match", `"`+teamDocsPeleVersion+`"`)
	if response, err := http.DefaultClient.Do(request); err != nil || response.StatusCode != http.StatusNotModified {
		t.Fatal(err, response.StatusCode)
	}
	// The changelog, from the releases, with dev's changes on the preview.
	_, _, body = get("/docs/tech/changelog")
	if !strings.Contains(body, "v1, 12 October 2026 (production now)") || !strings.Contains(body, "The first &lt;one&gt;") ||
		!strings.Contains(body, "<li>t-4 Suppliers #1</li>") {
		t.Fatal(body)
	}
	// The files as they are, never as a page; nothing outside the docs.
	if status, kind, body = get("/docs/raw/docs/tech/index.md"); status != 200 || kind != "text/plain; charset=utf-8" || !strings.HasPrefix(body, "# How Payments is built") {
		t.Fatal(status, kind, body)
	}
	if status, kind, _ = get("/docs/raw/docs/user/pic.png"); status != 200 || kind != "image/png" {
		t.Fatal(status, kind)
	}
	for _, path := range []string{"/docs/raw/src/server.ts", "/docs/raw/../src/server.ts", "/docs/user/nothing", "/docs/elsewhere"} {
		if status, _, body = get(path); status != 404 || strings.Contains(body, "secret") {
			t.Fatal(path, status)
		}
	}
	// For agents: every page, with its file.
	if _, kind, body = get("/docs/llms.txt"); !strings.HasPrefix(kind, "text/plain") ||
		!strings.Contains(body, "- [Orders](/docs/user/orders): /docs/raw/docs/user/orders.md") || !strings.Contains(body, "- [Changelog](/docs/tech/changelog)\n") {
		t.Fatal(body)
	}
	// /llms.txt at the root says the same, and no MCP server while the docs
	// tell of none; /llms-full.txt has the user docs themselves.
	if status, kind, body = get("/llms.txt"); status != 200 || !strings.HasPrefix(kind, "text/plain") || !strings.HasPrefix(body, "# payments\n") ||
		!strings.Contains(body, "- [Orders](/docs/user/orders)") || strings.Contains(body, "/mcp") || !strings.Contains(body, "(/llms-full.txt)") {
		t.Fatal(status, body)
	}
	if _, _, body = get("/llms-full.txt"); !strings.Contains(body, "<!-- page /docs/user/orders, from docs/user/orders.md -->\n\n# Orders\n\n## Adding") || strings.Contains(body, "How Payments is built") {
		t.Fatal(body)
	}
	teamDocsTestRepo(t, map[string]string{"docs/user/mcp.md": "# Using Payments with an agent\n\n## pay_supplier\n"})
	if _, _, body = get("/llms.txt"); !strings.Contains(body, "## Use it with an agent\n\n- [MCP server](/mcp): ") ||
		!strings.Contains(body, "- [Using Payments with an agent](/docs/user/mcp): what each tool does, /docs/raw/docs/user/mcp.md") {
		t.Fatal(body)
	}

	// Without the helper, the docs stay, without its script.
	config, _ := loadTeam("payments")
	config.Helper = map[string]bool{teamSlotPreview: false}
	saveTeam(config)
	if _, _, body = get("/docs/"); strings.Contains(body, "/__hi/helper.js") {
		t.Fatal(body)
	}
}

func TestTeamDocsEmpty(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	server := webTestServer(t, b.teamBroker, teamSlotPreview, "", "")
	defer server.Close()
	// No version on the address yet: the user docs say what they will be.
	response, err := http.Get(server.URL + "/docs/user/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != 200 || !strings.Contains(string(body), "Nothing here yet.") {
		t.Fatal(response.Status, string(body))
	}
}

// The helper's help answers from the user docs, and gives their addresses.
func TestTeamHelpReadsTheUserDocs(t *testing.T) {
	setupTeamTest(t)
	newTeamTestBroker(t)
	teamDocsTestRepo(t, teamDocsTestFiles)
	guide, source := teamHelpGuideText("payments", teamSlotPreview)
	if source != "the user docs, docs/user/" || !strings.Contains(guide, "<!-- page /docs/user/ -->\n# Payments") ||
		!strings.Contains(guide, "<!-- page /docs/user/orders -->\n# Orders") {
		t.Fatal(source, guide)
	}
	system := teamHelpSystem(testTeamConfig(), teamSlotPreview, guide, source)
	if !strings.Contains(system, "people can read at /docs/user/") || !strings.Contains(system, "Press **Add**") {
		t.Fatal(system)
	}
}

// /docs is /docs/, and the docs stay when the helper is off.
func TestNetExposeRouteAlways(t *testing.T) {
	options, _, err := parseNetExpose([]string{"8080", "--link", "--route", "/__hi/=unix:/run/a.sock", "--route-always", "/docs/=unix:/run/a.sock", "--inject-if", "/nonexistent"})
	if err != nil {
		t.Fatal(err)
	}
	helper := newNetExposeHelper(options, nil)
	if helper.route("/__hi/api/status", helper.on()) != nil || helper.route("/docs/user/", helper.on()) == nil || helper.route("/", helper.on()) != nil {
		t.Fatal("the routes")
	}
	recorder := httptest.NewRecorder()
	helper.route("/docs", false).ServeHTTP(recorder, httptest.NewRequest("GET", "/docs?x=1", nil))
	if recorder.Code != http.StatusMovedPermanently || recorder.Header().Get("Location") != "/docs/?x=1" {
		t.Fatal(recorder.Code, recorder.Header())
	}
	// --key keeps a link's key on a new relay.
	if options, _, err := parseNetExpose([]string{"8080", "--link", "--key", "rsRBVGgndWebrISOCchVlw"}); err != nil || options.Secret != "rsRBVGgndWebrISOCchVlw" {
		t.Fatal(options.Secret, err)
	}
	for _, args := range [][]string{{"8080", "--key", "rsRBVGgndWebrISOCchVlw"}, {"8080", "--link", "--key", "short"}} {
		if _, _, err := parseNetExpose(args); err == nil || !strings.Contains(err.Error(), "--key goes with --link") {
			t.Fatal(args, err)
		}
	}
	if _, _, err := parseNetExpose([]string{"8080", "--link", "--route-always", "docs"}); err == nil || !strings.Contains(err.Error(), "--route-always takes") {
		t.Fatal(err)
	}
}

func TestTeamDocsLinkKeepsKey(t *testing.T) {
	if got := teamDocsLink("https://payments.example.com/?hi_key=abc"); got != "https://payments.example.com/docs/?hi_key=abc" {
		t.Fatal(got)
	}
}

// The boxes block every brief shows members draws, with each part.
func TestTeamDrawRuleExampleDraws(t *testing.T) {
	start := strings.Index(teamDrawRule, "```boxes\n")
	end := strings.Index(teamDrawRule[start+3:], "```") + start + 3
	block := teamDrawRule[start : end+3]
	version := &teamDocsVersion{Files: map[string][]byte{}}
	body, _ := version.render([]byte("# Parts\n\n"+block+"\n"), "docs/tech/index.md")
	html := string(body)
	if strings.Contains(html, "docs-broken") {
		t.Fatal(html)
	}
	for _, want := range []string{"People using the app", "src/server.ts", "app.db", "api.example.com", "The team&#39;s machine", "<code>code</code>"} {
		if !strings.Contains(html, want) {
			t.Fatalf("no %q in\n%s", want, html)
		}
	}
}
