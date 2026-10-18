package main

import (
	"archive/tar"
	"bytes"
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

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// Every app a hi team builds has its docs at /docs on its addresses, in
// two parts: the user docs, for the people who use the app (why it exists,
// who it's for, its parts, how to do things), and the tech docs, for
// developers and agents (how it's built and why, the map, the specs, what
// changed). hi serves them from the project's docs/ in the version on the
// address, as the helper reads HELP.md, so they work whatever the app is
// written in (docs/specs/approved/hi_team_docs.md).

//go:embed helper/docs/page.html
var teamDocsPageSource string

//go:embed helper/docs/docs.css
var teamDocsCSS string

const (
	teamDocsPrefix = "/docs/"
	// The two parts, in the repository.
	teamDocsUserDir = "docs/user"
	teamDocsTechDir = "docs/tech"
	// teamDocsMaxFile is the largest file the docs read, and
	// teamDocsMaxTotal all of them together, per version.
	teamDocsMaxFile  = 2 << 20
	teamDocsMaxTotal = 32 << 20
	// teamDocsMermaid is the Mermaid the pages load for their charts, from
	// a CDN, only on a page that has one.
	teamDocsMermaid = "https://cdn.jsdelivr.net/npm/mermaid@11.4.1/dist/mermaid.esm.min.mjs"
)

// teamDocsPage is one page of the docs: a Markdown file, or a page hi
// writes (the changelog).
type teamDocsPage struct {
	Area  string // user or tech
	Path  string // under the area's address: "" for its first page, "orders", "decisions/0001-sqlite"
	File  string // in the repository; "" for a page hi writes
	Title string
	Group string // where the menu lists it: "" at the top, or "Decisions", "Specs", ...
}

func (p teamDocsPage) URL() string { return teamDocsPrefix + p.Area + "/" + p.Path }

// teamDocsLink is the docs on an address's link, keeping its key.
func teamDocsLink(link string) string {
	parsed, err := url.Parse(link)
	if err != nil {
		return ""
	}
	parsed.Path = teamDocsPrefix
	return parsed.String()
}

// teamDocsVersion is the docs of one commit: their files and pages. A
// commit's files never change, so it is read once.
type teamDocsVersion struct {
	Files  map[string][]byte
	User   []teamDocsPage
	Tech   []teamDocsPage
	byFile map[string]teamDocsPage
}

var teamDocsCache = struct {
	sync.Mutex
	versions map[string]*teamDocsVersion
	order    []string
}{versions: map[string]*teamDocsVersion{}}

// loadTeamDocs reads the docs of the commit on an address.
func loadTeamDocs(repo, commit string) *teamDocsVersion {
	teamDocsCache.Lock()
	defer teamDocsCache.Unlock()
	key := repo + "@" + commit
	if version, ok := teamDocsCache.versions[key]; ok {
		return version
	}
	version := teamDocsRead(repo, commit)
	teamDocsCache.versions[key] = version
	teamDocsCache.order = append(teamDocsCache.order, key)
	// Preview and production, and the versions before each.
	for len(teamDocsCache.order) > 6 {
		delete(teamDocsCache.versions, teamDocsCache.order[0])
		teamDocsCache.order = teamDocsCache.order[1:]
	}
	return version
}

// teamDocsRead takes docs/ and HELP.md out of a commit in one git archive.
func teamDocsRead(repo, commit string) *teamDocsVersion {
	version := &teamDocsVersion{Files: map[string][]byte{}}
	if commit != "" {
		var roots []string
		for _, name := range strings.Split(boxGit(repo, "ls-tree", "--name-only", commit), "\n") {
			if name == "docs" || name == teamHelpGuide {
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
					if header.Typeflag != tar.TypeReg || header.Size > teamDocsMaxFile || total+int(header.Size) > teamDocsMaxTotal {
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
func (v *teamDocsVersion) pages() {
	v.byFile = map[string]teamDocsPage{}
	add := func(list *[]teamDocsPage, page teamDocsPage) {
		page.Title = teamDocsTitle(v.Files[page.File], page.File)
		*list = append(*list, page)
		v.byFile[page.File] = page
	}
	files := sortedKeys(v.Files)
	// The user docs: docs/user/, or HELP.md in a project from before them.
	for _, file := range files {
		if rest, ok := strings.CutPrefix(file, teamDocsUserDir+"/"); ok && strings.HasSuffix(rest, ".md") {
			add(&v.User, teamDocsPage{Area: "user", Path: teamDocsPagePath(rest), File: file})
		}
	}
	if len(v.User) == 0 && v.Files[teamHelpGuide] != nil {
		add(&v.User, teamDocsPage{Area: "user", File: teamHelpGuide})
	}
	// The tech docs: docs/tech/, the map, the decisions, and the specs
	// with the lead's notes, each where the team already keeps it.
	groups := []struct{ dir, at, group string }{
		{teamDocsTechDir + "/", "", ""},
		{"docs/map.md", "map", ""},
		{"docs/decisions/", "decisions/", "Decisions"},
		{teamSpecsPath + "/", "specs/", "Specs"},
		{teamSpecsPath + "/notes/", "specs/notes/", "Notes"},
	}
	for _, group := range groups {
		for _, file := range files {
			if _, taken := v.byFile[file]; taken || !strings.HasSuffix(file, ".md") {
				continue
			}
			if group.group == "Specs" && strings.HasPrefix(file, teamSpecsPath+"/notes/") {
				continue // the lead's notes are a group of their own
			}
			switch {
			case file == group.dir:
				add(&v.Tech, teamDocsPage{Area: "tech", Path: group.at, File: file})
			case strings.HasSuffix(group.dir, "/") && strings.HasPrefix(file, group.dir):
				page := teamDocsPagePath(strings.TrimPrefix(file, group.dir))
				name := group.group
				if group.at == "" && strings.Contains(page, "/") {
					first, _, _ := strings.Cut(page, "/")
					name = teamDocsHumanize(first)
				}
				add(&v.Tech, teamDocsPage{Area: "tech", Path: strings.TrimSuffix(group.at+page, "/"), File: file, Group: name})
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
	changelog := teamDocsPage{Area: "tech", Path: "changelog", Title: "Changelog"}
	at := 0
	for at < len(v.Tech) && v.Tech[at].Group == "" {
		at++
	}
	v.Tech = append(v.Tech[:at], append([]teamDocsPage{changelog}, v.Tech[at:]...)...)
}

// teamDocsPagePath is a file's address under its part: index.md and
// README.md are their folder's.
func teamDocsPagePath(rest string) string {
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

var teamDocsHeading = regexp.MustCompile(`(?m)^#[ \t]+(.+?)[ \t#]*$`)

// teamDocsTitle is a page's first heading, or its file's name.
func teamDocsTitle(data []byte, file string) string {
	if match := teamDocsHeading.FindSubmatch(data); match != nil {
		return strings.TrimSpace(strings.NewReplacer("`", "", "**", "").Replace(string(match[1])))
	}
	base := strings.TrimSuffix(path.Base(file), ".md")
	if base == "index" || base == "README" {
		base = path.Base(path.Dir(file))
	}
	return teamDocsHumanize(base)
}

func teamDocsHumanize(name string) string {
	name = strings.TrimSpace(strings.NewReplacer("-", " ", "_", " ").Replace(name))
	if name == "" {
		return name
	}
	return strings.ToUpper(name[:1]) + name[1:]
}

func (v *teamDocsVersion) page(area, at string) (teamDocsPage, bool) {
	list := v.User
	if area == "tech" {
		list = v.Tech
	}
	for _, page := range list {
		if page.Path == at {
			return page, true
		}
	}
	return teamDocsPage{}, false
}

// ---------------------------------------------------------------------------
// Markdown

// teamDocsRender turns a page's Markdown into HTML. Raw HTML in it is left
// out; a mermaid block is a chart, and a boxes block a structure diagram
// like the hi guide's. It says whether the page has a chart.
func (v *teamDocsVersion) render(source []byte, from string) (template.HTML, bool) {
	code := &teamDocsCode{}
	markdown := goldmark.New(
		goldmark.WithExtensions(extension.GFM),
		goldmark.WithParserOptions(parser.WithAutoHeadingID(),
			parser.WithASTTransformers(util.Prioritized(&teamDocsLinks{version: v, from: from}, 100))),
		goldmark.WithRendererOptions(renderer.WithNodeRenderers(util.Prioritized(code, 100))),
	)
	var out bytes.Buffer
	if err := markdown.Convert(source, &out); err != nil {
		return template.HTML("<pre>" + template.HTMLEscapeString(string(source)) + "</pre>"), false
	}
	return template.HTML(out.String()), code.mermaid
}

// teamDocsCode draws a page's fenced blocks.
type teamDocsCode struct{ mermaid bool }

func (c *teamDocsCode) RegisterFuncs(register renderer.NodeRendererFuncRegisterer) {
	register.Register(ast.KindFencedCodeBlock, c.fenced)
}

func (c *teamDocsCode) fenced(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
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
		w.WriteString(string(teamDocsDiagramHTML(code.Bytes())))
	default:
		class := ""
		if language != "" {
			class = fmt.Sprintf(" class=\"language-%s\"", template.HTMLEscapeString(language))
		}
		fmt.Fprintf(w, "<pre><code%s>%s</code></pre>\n", class, template.HTMLEscapeString(code.String()))
	}
	return ast.WalkSkipChildren, nil
}

// teamDocsLinks points a page's links to other docs at their pages, and
// its pictures at the files: docs link to each other as files, like
// decisions/0001-sqlite.md, so they read the same on GitHub.
type teamDocsLinks struct {
	version *teamDocsVersion
	from    string
}

func (t *teamDocsLinks) Transform(document *ast.Document, _ text.Reader, _ parser.Context) {
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
func (v *teamDocsVersion) link(from, destination string) string {
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
		return teamDocsPrefix + "raw/" + file + fragment
	}
	return destination
}

// ---------------------------------------------------------------------------
// structure diagrams

// teamDocsDiagram is a boxes block: the hi guide's structure diagram
// (docs/guide-diagrams.md), as JSON in the page.
type teamDocsDiagram struct {
	Caption   string          `json:"caption"`
	You       string          `json:"you"`
	Via       string          `json:"via"`
	Frame     string          `json:"frame"`
	FrameMark int             `json:"frame_mark"`
	Groups    []teamDocsGroup `json:"groups"`
	After     []teamDocsGroup `json:"after"`
	Key       []struct {
		Mark int    `json:"mark"`
		Term string `json:"term"`
		Text string `json:"text"`
	} `json:"key"`
}

type teamDocsGroup struct {
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

var teamDocsTones = map[string]bool{"accent": true, "mint": true, "amber": true, "red": true, "sky": true, "violet": true, "dim": true}

var teamDocsInlineCode = regexp.MustCompile("`([^`]+)`")

// teamDocsInline is a diagram's short text, with `code`.
func teamDocsInline(text string) template.HTML {
	return template.HTML(teamDocsInlineCode.ReplaceAllString(template.HTMLEscapeString(text), "<code>$1</code>"))
}

var teamDocsDiagramTemplate = template.Must(template.New("diagram").Funcs(template.FuncMap{
	"inline": teamDocsInline,
	"tone": func(tone string) string {
		if teamDocsTones[tone] {
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

// teamDocsDiagramHTML draws a boxes block, or says why it can't.
func teamDocsDiagramHTML(data []byte) template.HTML {
	var diagram teamDocsDiagram
	err := json.Unmarshal(data, &diagram)
	if err == nil && len(diagram.Groups) == 0 {
		err = fmt.Errorf("it has no groups")
	}
	var out bytes.Buffer
	if err == nil {
		err = teamDocsDiagramTemplate.Execute(&out, diagram)
	}
	if err != nil {
		return template.HTML(fmt.Sprintf("<div class=\"docs-broken\"><p>This diagram can't be drawn: %s.</p><pre><code>%s</code></pre></div>\n",
			template.HTMLEscapeString(err.Error()), template.HTMLEscapeString(string(data))))
	}
	return template.HTML(out.String())
}

// ---------------------------------------------------------------------------
// the changelog

// teamDocsChangelog is the changelog page: production's versions, newest
// first, from versions.json, and on the preview what dev has that
// production hasn't.
func teamDocsChangelog(team, slot string) string {
	var text strings.Builder
	text.WriteString("# Changelog\n\nWhat each version of production brought, newest first. hi writes this page from the team's releases.\n")
	view, err := teamVersionsReport(team)
	if err != nil {
		return text.String()
	}
	if slot == teamSlotPreview && len(view.Changes) > 0 {
		fmt.Fprintf(&text, "\n## In dev, not released yet\n\nOn the preview (%s), not in production.\n\n", view.Dev)
		for _, task := range view.Changes {
			fmt.Fprintf(&text, "- %s %s\n", task.ID, teamDocsMarkdownLine(task.Title))
		}
	}
	releases := append([]teamRelease{}, view.Releases...)
	sort.Slice(releases, func(i, j int) bool { return releases[i].Version > releases[j].Version })
	for _, release := range releases {
		fmt.Fprintf(&text, "\n## v%d, %s", release.Version, release.Released.UTC().Format("2 January 2006"))
		if release.Version == view.Production {
			text.WriteString(" (production now)")
		}
		text.WriteString("\n\n")
		if notes := strings.TrimSpace(release.Notes); notes != "" {
			text.WriteString(teamDocsMarkdownLine(notes) + "\n\n")
		}
		for _, change := range release.Changes {
			fmt.Fprintf(&text, "- %s\n", teamDocsMarkdownLine(change))
		}
	}
	if len(releases) == 0 {
		text.WriteString("\nNothing is released yet: the first release is v1.\n")
	}
	return text.String()
}

// teamDocsMarkdownLine keeps text that isn't Markdown from being taken as
// some: a title with a # or a < stays as written.
func teamDocsMarkdownLine(line string) string {
	return strings.NewReplacer("\n", " ", "<", "&lt;", "#", "\\#", "*", "\\*", "_", "\\_", "[", "\\[").Replace(line)
}

// ---------------------------------------------------------------------------
// the pages

var teamDocsTemplate = template.Must(template.New("page").Parse(teamDocsPageSource))

// teamDocsView is what the page template draws.
type teamDocsView struct {
	App     string
	Purpose string
	Version string // the version on the address, "preview: dev, v3 + 2 changes"
	Area    string // "" on the first page, user, or tech
	Title   string
	Menu    []teamDocsMenuGroup
	Current string
	Body    template.HTML
	Source  string // the page's file, for agents and the curious
	Empty   bool
	Mermaid template.JS // the import's address, as a string in quotes
	Style   template.CSS
	Users   int
	Techs   int
	Helper  bool // the helper's script, while it is on
}

type teamDocsMenuGroup struct {
	Name  string
	Pages []teamDocsPage
}

func teamDocsMenu(pages []teamDocsPage) []teamDocsMenuGroup {
	var groups []teamDocsMenuGroup
	for _, page := range pages {
		if len(groups) == 0 || groups[len(groups)-1].Name != page.Group {
			groups = append(groups, teamDocsMenuGroup{Name: page.Group})
		}
		groups[len(groups)-1].Pages = append(groups[len(groups)-1].Pages, page)
	}
	return groups
}

// docsHandler serves /docs/ on an address.
func (b *teamBroker) docsHandler(slot string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		config, err := loadTeam(b.team)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		state, _ := loadTeamSlot(b.team, slot)
		version := loadTeamDocs(filepath.Join(teamDir(b.team), "repo"), state.Commit)
		view := teamDocsView{App: config.Name, Purpose: config.Purpose, Style: template.CSS(teamDocsCSS), Users: len(version.User), Techs: len(version.Tech) - 1, Helper: teamHelperOn(config, slot)}
		if state.Label != "" {
			view.Version = slot + ": " + state.Label
		}
		rest := strings.TrimPrefix(r.URL.Path, teamDocsPrefix)
		area, at, _ := strings.Cut(rest, "/")
		at = strings.Trim(at, "/")
		w.Header().Set("Cache-Control", "no-cache")
		switch {
		case rest == "":
			view.Title = "Docs"
		case rest == "llms.txt":
			teamDocsIndex(w, config, version)
			return
		case area == "raw":
			data, ok := version.Files[at]
			if !ok {
				http.NotFound(w, r)
				return
			}
			kind := mime.TypeByExtension(path.Ext(at))
			if strings.HasSuffix(at, ".md") || kind == "" || strings.HasPrefix(kind, "text/html") || strings.Contains(kind, "svg") {
				// Files are shown, never run as a page of the app's address.
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
			view.Menu, view.Current = teamDocsMenu(pages), at
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
				view.Body, _ = version.render([]byte(teamDocsChangelog(b.team, slot)), "")
			default:
				view.Title, view.Source = page.Title, page.File
				var mermaid bool
				view.Body, mermaid = version.render(version.Files[page.File], page.File)
				if mermaid {
					view.Mermaid = template.JS(`"` + teamDocsMermaid + `"`)
				}
			}
		default:
			w.WriteHeader(http.StatusNotFound)
			view.Title = "Not found"
			view.Body = template.HTML("<h1>Not found</h1>")
			view.Area = "missing"
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		teamDocsTemplate.Execute(w, view)
	})
}

// teamDocsIndex is /docs/llms.txt: every page with its file, for agents.
func teamDocsIndex(w http.ResponseWriter, config teamConfig, version *teamDocsVersion) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprintf(w, "# %s docs\n\n> %s\n\n", config.Name, firstNonEmpty(config.Purpose, "An app a hi team builds."))
	fmt.Fprintf(w, "Each page's Markdown is at %sraw/<file>.\n", teamDocsPrefix)
	for _, part := range []struct {
		name  string
		pages []teamDocsPage
	}{{"User docs", version.User}, {"Tech docs", version.Tech}} {
		fmt.Fprintf(w, "\n## %s\n\n", part.name)
		for _, page := range part.pages {
			if page.File == "" {
				fmt.Fprintf(w, "- [%s](%s)\n", page.Title, page.URL())
				continue
			}
			fmt.Fprintf(w, "- [%s](%s): %sraw/%s\n", page.Title, page.URL(), teamDocsPrefix, page.File)
		}
	}
}

// teamDocsUserText is the user docs as one text, for the helper: the
// first page first, each with its address.
func teamDocsUserText(repo, commit string) string {
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
