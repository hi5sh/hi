package cli

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	hi "github.com/hi5sh/hi"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// Every hi app has its docs at /docs on its addresses, in
// two parts: the user docs, for the people who use the app (why it exists,
// who it's for, its parts, how to do things), and the tech docs, for
// developers and agents (how it's built and why, the map, the specs, what
// changed). hi serves them from the project's docs/ in the version on the
// address, as the helper reads HELP.md, so they work whatever the app is
// written in (docs/specs/approved/hi_app_docs.md).

var appDocsPageSource = hi.AppDocsPageSource

var appDocsCSS = hi.AppDocsCSS

// appDocsPele draws the docs' Mermaid charts in the browser: pele, served
// from hi at /docs/pele.js, so the charts need no CDN (third_party/pele).
var appDocsPele = hi.AppDocsPele

var appDocsPeleVersion = fmt.Sprintf("%x", sha256.Sum256(appDocsPele))[:16]

const (
	appDocsPrefix = "/docs/"
	// The two parts, in the repository.
	appDocsUserDir = "docs/user"
	appDocsTechDir = "docs/tech"
	// appDocsMaxFile is the largest file the docs read, and
	// appDocsMaxTotal all of them together, per version.
	appDocsMaxFile  = 2 << 20
	appDocsMaxTotal = 32 << 20
)

// appDocsCharts is the address of the script that draws the charts, which
// a page loads only when it has one.
var appDocsCharts = appDocsPrefix + "pele.js?v=" + appDocsPeleVersion

// appDocsPage is one page of the docs: a Markdown file, or a page hi
// writes (the changelog).
type appDocsPage struct {
	Area  string // user or tech
	Path  string // under the area's address: "" for its first page, "orders", "decisions/0001-sqlite"
	File  string // in the repository; "" for a page hi writes
	Title string
	Group string // where the menu lists it: "" at the top, or "Decisions", "Specs", ...
}

func (p appDocsPage) URL() string { return appDocsPrefix + p.Area + "/" + p.Path }

// appDocsLink is the docs on an address's link, keeping its key.
func appDocsLink(link string) string {
	parsed, err := url.Parse(link)
	if err != nil {
		return ""
	}
	parsed.Path = appDocsPrefix
	return parsed.String()
}

// appDocsVersion is the docs of one commit: their files and pages. A
// commit's files never change, so it is read once.
type appDocsVersion struct {
	Files  map[string][]byte
	User   []appDocsPage
	Tech   []appDocsPage
	byFile map[string]appDocsPage
}

var appDocsCache = struct {
	sync.Mutex
	versions map[string]*appDocsVersion
	order    []string
}{versions: map[string]*appDocsVersion{}}

// loadTeamDocs reads the docs of the commit on an address.
func loadTeamDocs(repo, commit string) *appDocsVersion {
	appDocsCache.Lock()
	defer appDocsCache.Unlock()
	key := repo + "@" + commit
	if version, ok := appDocsCache.versions[key]; ok {
		return version
	}
	version := appDocsRead(repo, commit)
	appDocsCache.versions[key] = version
	appDocsCache.order = append(appDocsCache.order, key)
	// Preview and production, and the versions before each.
	for len(appDocsCache.order) > 6 {
		delete(appDocsCache.versions, appDocsCache.order[0])
		appDocsCache.order = appDocsCache.order[1:]
	}
	return version
}

// appDocsRead takes docs/ and HELP.md out of a commit in one git archive.
func appDocsRead(repo, commit string) *appDocsVersion {
	version := &appDocsVersion{Files: map[string][]byte{}}
	if commit != "" {
		var roots []string
		for _, name := range strings.Split(boxGit(repo, "ls-tree", "--name-only", commit), "\n") {
			if name == "docs" || name == appHelpGuide {
				roots = append(roots, name)
			}
		}
		if len(roots) > 0 {
			out, err := exec.Command("git", append([]string{"-C", repo, "archive", "--format=tar", commit, "--"}, roots...)...).Output()
			if err == nil {
				reader, total := tar.NewReader(bytes.NewReader(out)), 0
				for {
					header, err := reader.Next()
					if err != nil {
						break
					}
					if header.Typeflag != tar.TypeReg || header.Size > appDocsMaxFile || total+int(header.Size) > appDocsMaxTotal {
						continue
					}
					data, err := io.ReadAll(reader)
					if err != nil {
						break
					}
					version.Files[header.Name], total = data, total+len(data)
				}
			}
		}
	}
	version.pages()
	return version
}

// pages lists the docs' pages from their files.
func (v *appDocsVersion) pages() {
	v.byFile = map[string]appDocsPage{}
	add := func(list *[]appDocsPage, page appDocsPage) {
		page.Title = appDocsTitle(v.Files[page.File], page.File)
		*list = append(*list, page)
		v.byFile[page.File] = page
	}
	files := sortedKeys(v.Files)
	// The user docs: docs/user/, or HELP.md in a project from before them.
	for _, file := range files {
		if rest, ok := strings.CutPrefix(file, appDocsUserDir+"/"); ok && strings.HasSuffix(rest, ".md") {
			add(&v.User, appDocsPage{Area: "user", Path: appDocsPagePath(rest), File: file})
		}
	}
	if len(v.User) == 0 && v.Files[appHelpGuide] != nil {
		add(&v.User, appDocsPage{Area: "user", File: appHelpGuide})
	}
	// The tech docs: docs/tech/, the map, the decisions, and the specs
	// with the lead's notes, each where the team already keeps it.
	groups := []struct{ dir, at, group string }{
		{appDocsTechDir + "/", "", ""},
		{"docs/map.md", "map", ""},
		{"docs/decisions/", "decisions/", "Decisions"},
		{appSpecsPath + "/", "specs/", "Specs"},
		{appSpecsPath + "/notes/", "specs/notes/", "Notes"},
	}
	for _, group := range groups {
		for _, file := range files {
			if _, taken := v.byFile[file]; taken || !strings.HasSuffix(file, ".md") {
				continue
			}
			if group.group == "Specs" && strings.HasPrefix(file, appSpecsPath+"/notes/") {
				continue // the lead's notes are a group of their own
			}
			switch {
			case file == group.dir:
				add(&v.Tech, appDocsPage{Area: "tech", Path: group.at, File: file})
			case strings.HasSuffix(group.dir, "/") && strings.HasPrefix(file, group.dir):
				page := appDocsPagePath(strings.TrimPrefix(file, group.dir))
				name := group.group
				if group.at == "" && strings.Contains(page, "/") {
					first, _, _ := strings.Cut(page, "/")
					name = appDocsHumanize(first)
				}
				add(&v.Tech, appDocsPage{Area: "tech", Path: strings.TrimSuffix(group.at+page, "/"), File: file, Group: name})
			}
		}
	}
	// The specs' README is the specs' first page, under its group.
	for i, page := range v.Tech {
		if page.Path == "specs" {
			v.Tech[i].Group = "Specs"
			v.byFile[page.File] = v.Tech[i]
		}
	}
	// The pages at the top first; hi writes the changelog from the team's
	// releases, after them.
	sort.SliceStable(v.Tech, func(i, j int) bool { return v.Tech[i].Group == "" && v.Tech[j].Group != "" })
	changelog := appDocsPage{Area: "tech", Path: "changelog", Title: "Changelog"}
	at := 0
	for at < len(v.Tech) && v.Tech[at].Group == "" {
		at++
	}
	v.Tech = append(v.Tech[:at], append([]appDocsPage{changelog}, v.Tech[at:]...)...)
}

// appDocsPagePath is a file's address under its part: index.md and
// README.md are their folder's.
func appDocsPagePath(rest string) string {
	rest = strings.TrimSuffix(rest, ".md")
	for _, index := range []string{"index", "README"} {
		if rest == index {
			return ""
		}
		if dir, ok := strings.CutSuffix(rest, "/"+index); ok {
			return dir
		}
	}
	return rest
}

var appDocsHeading = regexp.MustCompile(`(?m)^#[ \t]+(.+?)[ \t#]*$`)

// appDocsTitle is a page's first heading, or its file's name.
func appDocsTitle(data []byte, file string) string {
	if match := appDocsHeading.FindSubmatch(data); match != nil {
		return strings.TrimSpace(strings.NewReplacer("`", "", "**", "").Replace(string(match[1])))
	}
	base := strings.TrimSuffix(path.Base(file), ".md")
	if base == "index" || base == "README" {
		base = path.Base(path.Dir(file))
	}
	return appDocsHumanize(base)
}

func appDocsHumanize(name string) string {
	name = strings.TrimSpace(strings.NewReplacer("-", " ", "_", " ").Replace(name))
	if name == "" {
		return name
	}
	return strings.ToUpper(name[:1]) + name[1:]
}

func (v *appDocsVersion) page(area, at string) (appDocsPage, bool) {
	list := v.User
	if area == "tech" {
		list = v.Tech
	}
	for _, page := range list {
		if page.Path == at {
			return page, true
		}
	}
	return appDocsPage{}, false
}

// ---------------------------------------------------------------------------
// Markdown

// appDocsRender turns a page's Markdown into HTML. Raw HTML in it is left
// out; a mermaid block is a chart, and a boxes block a structure diagram
// like the hi guide's. It says whether the page has a chart.
func (v *appDocsVersion) render(source []byte, from string) (template.HTML, bool) {
	code := &appDocsCode{}
	markdown := goldmark.New(
		goldmark.WithExtensions(extension.GFM),
		goldmark.WithParserOptions(parser.WithAutoHeadingID(),
			parser.WithASTTransformers(util.Prioritized(&appDocsLinks{version: v, from: from}, 100))),
		goldmark.WithRendererOptions(renderer.WithNodeRenderers(util.Prioritized(code, 100))),
	)
	var out bytes.Buffer
	if err := markdown.Convert(source, &out); err != nil {
		return template.HTML("<pre>" + template.HTMLEscapeString(string(source)) + "</pre>"), false
	}
	return template.HTML(out.String()), code.mermaid
}

// appDocsCode draws a page's fenced blocks.
type appDocsCode struct{ mermaid bool }

func (c *appDocsCode) RegisterFuncs(register renderer.NodeRendererFuncRegisterer) {
	register.Register(ast.KindFencedCodeBlock, c.fenced)
}

func (c *appDocsCode) fenced(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	block := node.(*ast.FencedCodeBlock)
	language := string(block.Language(source))
	var code bytes.Buffer
	for i := 0; i < block.Lines().Len(); i++ {
		line := block.Lines().At(i)
		code.Write(line.Value(source))
	}
	switch language {
	case "mermaid":
		c.mermaid = true
		fmt.Fprintf(w, "<pre class=\"mermaid\">%s</pre>\n", template.HTMLEscapeString(code.String()))
	case "boxes":
		w.WriteString(string(appDocsDiagramHTML(code.Bytes())))
	default:
		class := ""
		if language != "" {
			class = fmt.Sprintf(" class=\"language-%s\"", template.HTMLEscapeString(language))
		}
		fmt.Fprintf(w, "<pre><code%s>%s</code></pre>\n", class, template.HTMLEscapeString(code.String()))
	}
	return ast.WalkSkipChildren, nil
}

// appDocsLinks points a page's links to other docs at their pages, and
// its pictures at the files: docs link to each other as files, like
// decisions/0001-sqlite.md, so they read the same on GitHub.
type appDocsLinks struct {
	version *appDocsVersion
	from    string
}

func (t *appDocsLinks) Transform(document *ast.Document, _ text.Reader, _ parser.Context) {
	ast.Walk(document, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if entering {
			switch node := node.(type) {
			case *ast.Link:
				node.Destination = []byte(t.version.link(t.from, string(node.Destination)))
			case *ast.Image:
				node.Destination = []byte(t.version.link(t.from, string(node.Destination)))
			}
		}
		return ast.WalkContinue, nil
	})
}

// link is where a link in the file from goes: a page of the docs, a file
// of them, or where it said.
func (v *appDocsVersion) link(from, destination string) string {
	target, err := url.Parse(destination)
	if err != nil || target.Scheme != "" || target.Host != "" || target.Path == "" || strings.HasPrefix(target.Path, "/") {
		return destination
	}
	file := path.Clean(path.Join(path.Dir(from), target.Path))
	fragment := ""
	if target.Fragment != "" {
		fragment = "#" + target.Fragment
	}
	if page, ok := v.byFile[file]; ok {
		return page.URL() + fragment
	}
	if v.Files[file] != nil {
		return appDocsPrefix + "raw/" + file + fragment
	}
	return destination
}

// ---------------------------------------------------------------------------
// structure diagrams

// appDocsDiagram is a boxes block: the hi guide's structure diagram
// (docs/guide-diagrams.md), as JSON in the page.
type appDocsDiagram struct {
	Caption   string         `json:"caption"`
	You       string         `json:"you"`
	Via       string         `json:"via"`
	Frame     string         `json:"frame"`
	FrameMark int            `json:"frame_mark"`
	Groups    []appDocsGroup `json:"groups"`
	After     []appDocsGroup `json:"after"`
	Key       []struct {
		Mark int    `json:"mark"`
		Term string `json:"term"`
		Text string `json:"text"`
	} `json:"key"`
}

type appDocsGroup struct {
	Name    string `json:"name"`
	Label   string `json:"label"`
	Mark    int    `json:"mark"`
	Via     string `json:"via"`
	Summary string `json:"summary"`
	Items   []struct {
		Title string `json:"title"`
		Role  string `json:"role"`
		Does  string `json:"does"`
		Chip  string `json:"chip"`
		Tone  string `json:"tone"`
		Entry string `json:"entry"`
		Mark  int    `json:"mark"`
	} `json:"items"`
}

var appDocsTones = map[string]bool{"accent": true, "mint": true, "amber": true, "red": true, "sky": true, "violet": true, "dim": true}

var appDocsInlineCode = regexp.MustCompile("`([^`]+)`")

// appDocsInline is a diagram's short text, with `code`.
func appDocsInline(text string) template.HTML {
	return template.HTML(appDocsInlineCode.ReplaceAllString(template.HTMLEscapeString(text), "<code>$1</code>"))
}

var appDocsDiagramTemplate = template.Must(template.New("diagram").Funcs(template.FuncMap{
	"inline": appDocsInline,
	"tone": func(tone string) string {
		if appDocsTones[tone] {
			return tone
		}
		return "dim"
	},
}).Parse(`{{define "group"}}<div class="hd-group"><div class="hd-ghead">{{if .Mark}}<span class="hd-mk">{{.Mark}}</span>{{end}}<span class="hd-gname">{{.Name}}</span>{{if .Label}}<span class="hd-glabel">{{.Label}}</span>{{end}}</div>
{{- if .Summary}}<p class="hd-gsum">{{inline .Summary}}</p>{{end}}
{{- range .Items}}<div class="hd-card{{if .Entry}} hd-entrycard{{end}}"><div class="hd-bar"><i></i><i></i><i></i>{{if .Mark}}<span class="hd-mk">{{.Mark}}</span>{{end}}<code>{{.Title}}</code></div><div class="hd-body">
{{- if .Entry}}<div class="hd-entry">{{.Entry}}</div>{{end}}<div class="hd-role">{{.Role}}</div><p class="hd-does">{{inline .Does}}</p>
{{- if .Chip}}<span class="hd-chip hd-tone-{{tone .Tone}}"><i></i>{{.Chip}}</span>{{end}}</div></div>{{end}}</div>{{end -}}
<figure class="hd"><figcaption class="hd-sr">{{.Caption}}</figcaption><div class="hd-flow" aria-hidden="true">
{{- if .You}}<div class="hd-top"><span class="hd-you">{{.You}}</span><span class="hd-down">{{if .Via}}<em>{{.Via}}</em>{{end}}</span></div>{{end -}}
<div class="hd-frame">{{if .Frame}}<span class="hd-frametag">{{if .FrameMark}}<span class="hd-mk">{{.FrameMark}}</span>{{end}}{{.Frame}}</span>{{end}}<div class="hd-cols">
{{- range $i, $g := .Groups}}{{if $i}}<span class="hd-arrow">{{if $g.Via}}<em>{{$g.Via}}</em>{{end}}</span>{{end}}{{template "group" $g}}{{end -}}
</div></div>
{{- range .After}}<span class="hd-down">{{if .Via}}<em>{{.Via}}</em>{{end}}</span><div class="hd-cols hd-after">{{template "group" .}}</div>{{end -}}
</div>
{{- if .Key}}<dl class="hd-key">{{range .Key}}<div><dt><span class="hd-mk">{{.Mark}}</span>{{.Term}}</dt><dd>{{inline .Text}}</dd></div>{{end}}</dl>{{end -}}
</figure>
`))

// appDocsDiagramHTML draws a boxes block, or says why it can't.
func appDocsDiagramHTML(data []byte) template.HTML {
	var diagram appDocsDiagram
	err := json.Unmarshal(data, &diagram)
	if err == nil && len(diagram.Groups) == 0 {
		err = fmt.Errorf("it has no groups")
	}
	var out bytes.Buffer
	if err == nil {
		err = appDocsDiagramTemplate.Execute(&out, diagram)
	}
	if err != nil {
		return template.HTML(fmt.Sprintf("<div class=\"docs-broken\"><p>This diagram can't be drawn: %s.</p><pre><code>%s</code></pre></div>\n",
			template.HTMLEscapeString(err.Error()), template.HTMLEscapeString(string(data))))
	}
	return template.HTML(out.String())
}

// ---------------------------------------------------------------------------
// the changelog

// appDocsChangelog is the changelog page: production's versions, newest
// first, from versions.json, and on the preview what dev has that
// production hasn't.
func appDocsChangelog(team, slot string) string {
	var text strings.Builder
	text.WriteString("# Changelog\n\nWhat each version of production brought, newest first. hi writes this page from the app's releases.\n")
	view, err := appVersionsReport(team)
	if err != nil {
		return text.String()
	}
	if slot == appSlotPreview && len(view.Changes) > 0 {
		fmt.Fprintf(&text, "\n## In dev, not released yet\n\nOn the preview (%s), not in production.\n\n", view.Dev)
		for _, task := range view.Changes {
			fmt.Fprintf(&text, "- %s %s\n", task.ID, appDocsMarkdownLine(task.Title))
		}
	}
	releases := append([]appRelease{}, view.Releases...)
	sort.Slice(releases, func(i, j int) bool { return releases[i].Version > releases[j].Version })
	for _, release := range releases {
		fmt.Fprintf(&text, "\n## v%d, %s", release.Version, release.Released.UTC().Format("2 January 2006"))
		if release.Version == view.Production {
			text.WriteString(" (production now)")
		}
		text.WriteString("\n\n")
		if notes := strings.TrimSpace(release.Notes); notes != "" {
			text.WriteString(appDocsMarkdownLine(notes) + "\n\n")
		}
		for _, change := range release.Changes {
			fmt.Fprintf(&text, "- %s\n", appDocsMarkdownLine(change))
		}
	}
	if len(releases) == 0 {
		text.WriteString("\nNothing is released yet: the first release is v1.\n")
	}
	return text.String()
}

// appDocsMarkdownLine keeps text that isn't Markdown from being taken as
// some: a title with a # or a < stays as written.
func appDocsMarkdownLine(line string) string {
	return strings.NewReplacer("\n", " ", "<", "&lt;", "#", "\\#", "*", "\\*", "_", "\\_", "[", "\\[").Replace(line)
}

// ---------------------------------------------------------------------------
// the pages

var appDocsTemplate = template.Must(template.New("page").Parse(appDocsPageSource))

// appDocsView is what the page template draws.
type appDocsView struct {
	App     string
	Purpose string
	Version string // the version on the address, "preview: dev, v3 + 2 changes"
	Area    string // "" on the first page, user, or tech
	Title   string
	Menu    []appDocsMenuGroup
	Current string
	Body    template.HTML
	Source  string // the page's file, for agents and the curious
	Empty   bool
	Charts  template.JS // the import's address, as a string in quotes
	Style   template.CSS
	Users   int
	Techs   int
	Helper  bool // the helper's script, while it is on
}

type appDocsMenuGroup struct {
	Name  string
	Pages []appDocsPage
}

func appDocsMenu(pages []appDocsPage) []appDocsMenuGroup {
	var groups []appDocsMenuGroup
	for _, page := range pages {
		if len(groups) == 0 || groups[len(groups)-1].Name != page.Group {
			groups = append(groups, appDocsMenuGroup{Name: page.Group})
		}
		groups[len(groups)-1].Pages = append(groups[len(groups)-1].Pages, page)
	}
	return groups
}

// docsHandler serves /docs/ on an address.
func (b *appBroker) docsHandler(slot string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		config, err := loadTeam(b.team)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		state, _ := loadTeamSlot(b.team, slot)
		version := loadTeamDocs(filepath.Join(appDir(b.team), "repo"), state.Commit)
		view := appDocsView{App: config.Name, Purpose: config.Purpose, Style: template.CSS(appDocsCSS), Users: len(version.User), Techs: len(version.Tech) - 1, Helper: appHelperOn(config, slot)}
		if state.Label != "" {
			view.Version = slot + ": " + state.Label
		}
		rest := strings.TrimPrefix(r.URL.Path, appDocsPrefix)
		area, at, _ := strings.Cut(rest, "/")
		at = strings.Trim(at, "/")
		w.Header().Set("Cache-Control", "no-cache")
		switch {
		case rest == "":
			view.Title = "Docs"
		case rest == "llms.txt":
			appDocsIndex(w, config, version)
			return
		case rest == "pele.js":
			etag := `"` + appDocsPeleVersion + `"`
			w.Header().Set("ETag", etag)
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			if r.Header.Get("If-None-Match") == etag {
				w.WriteHeader(http.StatusNotModified)
				return
			}
			w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			w.Write(appDocsPele)
			return
		case area == "raw":
			data, ok := version.Files[at]
			if !ok {
				http.NotFound(w, r)
				return
			}
			// Files are shown, never run as a page of the app's address: a
			// kind that can't hold a script, or plain text. XML and XHTML
			// can, as HTML and SVG can.
			kind := mime.TypeByExtension(path.Ext(at))
			if base, _, _ := strings.Cut(kind, ";"); strings.HasSuffix(at, ".md") || !appDocsInertTypes[strings.TrimSpace(base)] {
				kind = "text/plain; charset=utf-8"
			}
			w.Header().Set("Content-Type", kind)
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.Write(data)
			return
		case area == "user" || area == "tech":
			view.Area = area
			pages := version.User
			if area == "tech" {
				pages = version.Tech
			}
			view.Menu, view.Current = appDocsMenu(pages), at
			page, ok := version.page(area, at)
			switch {
			case !ok && at == "" && len(pages) == 0:
				view.Title, view.Empty = map[string]string{"user": "User docs", "tech": "Tech docs"}[area], true
			case !ok:
				w.WriteHeader(http.StatusNotFound)
				view.Title = "Not found"
				view.Body = template.HTML("<h1>Not found</h1><p>The docs have no page here in this version.</p>")
			case page.File == "":
				view.Title = page.Title
				view.Body, _ = version.render([]byte(appDocsChangelog(b.team, slot)), "")
			default:
				view.Title, view.Source = page.Title, page.File
				var charts bool
				view.Body, charts = version.render(version.Files[page.File], page.File)
				if charts {
					view.Charts = template.JS(`"` + appDocsCharts + `"`)
				}
			}
		default:
			w.WriteHeader(http.StatusNotFound)
			view.Title = "Not found"
			view.Body = template.HTML("<h1>Not found</h1>")
			view.Area = "missing"
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		appDocsTemplate.Execute(w, view)
	})
}

// appLLMsPath is the app's llms.txt (llmstxt.org): what the app is and
// where its docs and its MCP server are, for agents. appLLMsFullPath is
// all the user docs in one file. hi serves both from docs/user, as it
// serves /docs/ (docs/specs/approved/hi_app_agents.md).
const (
	appLLMsPath     = "/llms.txt"
	appLLMsFullPath = "/llms-full.txt"
	// appDocsMCPPage is the user docs' page on the app's MCP server.
	appDocsMCPPage = appDocsUserDir + "/mcp.md"
)

// llmsHandler serves /llms.txt, or /llms-full.txt with full, on an address.
func (b *appBroker) llmsHandler(slot string, full bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		config, err := loadTeam(b.team)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		state, _ := loadTeamSlot(b.team, slot)
		version := loadTeamDocs(filepath.Join(appDir(b.team), "repo"), state.Commit)
		w.Header().Set("Cache-Control", "no-cache")
		if full {
			appDocsFull(w, config, version)
			return
		}
		appDocsIndex(w, config, version)
	})
}

// appDocsIndex is /llms.txt and /docs/llms.txt: what the app is, its MCP
// server when its docs tell of one, and every page with its file.
func appDocsIndex(w http.ResponseWriter, config appConfig, version *appDocsVersion) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprintf(w, "# %s\n\n> %s\n\n", config.Name, firstNonEmpty(config.Purpose, "A hi app."))
	fmt.Fprintf(w, "The user docs are for the people who use the app, the tech docs for those who build it. Each page's Markdown is at %sraw/<file>.\n", appDocsPrefix)
	if _, ok := version.Files[appDocsMCPPage]; ok {
		fmt.Fprintf(w, "\n## Use it with an agent\n\n")
		fmt.Fprintf(w, "- [MCP server](%s): the app's features as tools, over Streamable HTTP\n", appMCPPath)
		for _, page := range version.User {
			if page.File == appDocsMCPPage {
				fmt.Fprintf(w, "- [%s](%s): what each tool does, %sraw/%s\n", page.Title, page.URL(), appDocsPrefix, page.File)
			}
		}
	}
	for _, part := range []struct {
		name  string
		pages []appDocsPage
	}{{"User docs", version.User}, {"Tech docs", version.Tech}} {
		fmt.Fprintf(w, "\n## %s\n\n", part.name)
		for _, page := range part.pages {
			if page.File == "" {
				fmt.Fprintf(w, "- [%s](%s)\n", page.Title, page.URL())
				continue
			}
			fmt.Fprintf(w, "- [%s](%s): %sraw/%s\n", page.Title, page.URL(), appDocsPrefix, page.File)
		}
	}
	fmt.Fprintf(w, "\n## Optional\n\n- [All the user docs in one file](%s)\n", appLLMsFullPath)
}

// appDocsFull is /llms-full.txt: the user docs, page after page.
func appDocsFull(w http.ResponseWriter, config appConfig, version *appDocsVersion) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprintf(w, "# %s\n\n> %s\n", config.Name, firstNonEmpty(config.Purpose, "A hi app."))
	for _, page := range version.User {
		if page.File == "" {
			continue
		}
		fmt.Fprintf(w, "\n<!-- page %s, from %s -->\n\n%s\n", page.URL(), page.File, strings.TrimSpace(string(version.Files[page.File])))
	}
}

// appDocsUserText is the user docs as one text, for the helper: the
// first page first, each with its address.
func appDocsUserText(repo, commit string) string {
	version := loadTeamDocs(repo, commit)
	if len(version.User) == 0 {
		return ""
	}
	var text strings.Builder
	for _, page := range version.User {
		fmt.Fprintf(&text, "<!-- page %s -->\n%s\n\n", page.URL(), strings.TrimSpace(string(version.Files[page.File])))
	}
	return text.String()
}

// appDocsInertTypes are the kinds /docs/raw serves as themselves: none can
// hold a script.
var appDocsInertTypes = map[string]bool{
	"image/png": true, "image/jpeg": true, "image/gif": true, "image/webp": true, "image/avif": true,
	"application/pdf": true, "application/json": true, "text/plain": true, "text/csv": true,
	"video/mp4": true, "video/webm": true, "audio/mpeg": true, "audio/ogg": true, "audio/wav": true,
}
