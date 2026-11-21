package cli

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// A feature page (hi5.sh/box/, _layouts/feature.html, docs/feature-pages.md)
// opens with a demo of the tool in a terminal, from assets/demos/<name>.js.
// These tests keep the demo's hi commands ones the tool has.

// featurePageUsage is each feature page's tool and the usage it prints. A
// new feature page goes here with its tool's usage.
var featurePageUsage = map[string]func(io.Writer){
	"box":          printBoxUsage,
	"agent":        printAgentUsage,
	"app":          printTeamUsage,
	"compute":      printComputeUsage,
	"q":            printQUsage,
	"init":         printInitUsage,
	"net":          func(w io.Writer) { printNetUsage(w); printNetExposeUsage(w) },
	"server":       printOrgUsage,
	"data":         printDataUsage,
	"autoresearch": printBestOfUsage,
}

// featureCommandPage is a command whose page isn't named after it: hi
// agent best-of has its own.
var featureCommandPage = map[string]string{"agent best-of": "autoresearch"}

// featureFreeWords are the tools whose words after the name are free text,
// such as hi q's question, not a subcommand.
var featureFreeWords = map[string]bool{"q": true}

var (
	demoCommand = regexp.MustCompile(`\{ cmd: "((?:[^"\\]|\\.)*)"`)
	demoFlag    = regexp.MustCompile(`(?:^|\s)(--[a-z][a-z-]*)`)
)

func TestFeaturePagesHaveTheirDemo(t *testing.T) {
	t.Chdir(siteDir)
	for name := range featurePageUsage {
		page, err := os.ReadFile(filepath.Join(name, "index.html"))
		if err != nil {
			t.Fatalf("feature page %s: %v", name, err)
		}
		for _, want := range []string{"layout: feature", "permalink: /" + name + "/", "demo: " + name + "\n"} {
			if !bytes.Contains(page, []byte(want)) {
				t.Errorf("%s/index.html: front matter lacks %q", name, want)
			}
		}
		// Jekyll drops a page's front matter, and with it the layout, when
		// a plain value holds ": ", so such a value must be quoted.
		front, _, _ := strings.Cut(strings.TrimPrefix(string(page), "---\n"), "\n---\n")
		for _, line := range strings.Split(front, "\n") {
			key, value, ok := strings.Cut(line, ": ")
			if ok && !strings.HasPrefix(key, " ") && !strings.HasPrefix(value, `"`) && strings.Contains(value, ": ") {
				t.Errorf("%s/index.html: quote the value of %s; it holds \": \"", name, key)
			}
		}
		if _, err := os.Stat(filepath.Join("assets", "demos", name+".js")); err != nil {
			t.Errorf("feature page %s has no demo: %v", name, err)
		}
	}
	demos, _ := filepath.Glob(filepath.Join("assets", "demos", "*.js"))
	for _, demo := range demos {
		name := strings.TrimSuffix(filepath.Base(demo), ".js")
		if featurePageUsage[name] == nil {
			t.Errorf("%s: no feature page in featurePageUsage", demo)
		}
	}
}

// TestFeatureDemosUseRealCommands checks every hi command a demo types, of
// any tool with a feature page: its subcommand and its flags must be in that
// tool's usage.
func TestFeatureDemosUseRealCommands(t *testing.T) {
	t.Chdir(siteDir)
	for name := range featurePageUsage {
		src, err := os.ReadFile(filepath.Join("assets", "demos", name+".js"))
		if err != nil {
			t.Fatal(err)
		}
		seen := 0
		for _, m := range demoCommand.FindAllStringSubmatch(string(src), -1) {
			words := strings.Fields(m[1])
			if len(words) < 2 || words[0] != "hi" {
				continue
			}
			tool, page, rest := words[1], words[1], words[2:]
			if len(words) > 2 && featureCommandPage[words[1]+" "+words[2]] != "" {
				tool, rest = words[1]+" "+words[2], words[3:]
			}
			if featureCommandPage[tool] != "" {
				page = featureCommandPage[tool]
			}
			if featurePageUsage[page] == nil {
				continue
			}
			if page == name {
				seen++
			}
			var buf bytes.Buffer
			featurePageUsage[page](&buf)
			help := buf.String()
			// A number is a count, such as best-of's <n>, not a subcommand.
			if len(rest) > 0 && !featureFreeWords[tool] && !strings.HasPrefix(rest[0], "-") && !strings.HasPrefix(rest[0], `\"`) && strings.Trim(rest[0], "0123456789") != "" {
				sub := rest[0]
				if !regexp.MustCompile(`hi ` + tool + ` \[?(\S+\|)*` + regexp.QuoteMeta(sub) + `[\s|\]]`).MatchString(help) {
					t.Errorf("%s demo: %q uses %q, which hi %s's usage doesn't list", name, m[1], sub, tool)
				}
			}
			for _, f := range demoFlag.FindAllStringSubmatch(m[1], -1) {
				if !regexp.MustCompile(`(^|[^a-z-])` + f[1] + `([^a-z-]|$)`).MatchString(help) {
					t.Errorf("%s demo: %q uses %s, which hi %s's usage doesn't list", name, m[1], f[1], tool)
				}
			}
		}
		if seen == 0 {
			t.Errorf("%s demo types no hi %s command", name, name)
		}
	}
}

// TestFeaturePagesInTheSiteMenu keeps the site menu (_data/features.yml)
// and the feature pages in step: each page is listed, and each entry is a page.
func TestFeaturePagesInTheSiteMenu(t *testing.T) {
	t.Chdir(siteDir)
	data, err := os.ReadFile(filepath.Join("_data", "features.yml"))
	if err != nil {
		t.Fatal(err)
	}
	listed := map[string]bool{}
	for _, line := range strings.Split(string(data), "\n") {
		if url, ok := strings.CutPrefix(line, "- url: "); ok {
			name := strings.Trim(url, "/")
			listed[name] = true
			if featurePageUsage[name] == nil {
				t.Errorf("_data/features.yml lists %s, which isn't a feature page in featurePageUsage", url)
			}
		}
	}
	for name := range featurePageUsage {
		if !listed[name] {
			t.Errorf("feature page %s isn't in _data/features.yml, so the site menu leaves it out", name)
		}
	}
}

// A page scene draws the admin page and the dev page with their own
// stylesheets and Geist, which the site's build copies to assets/pages/
// (story.js): the build copies each file, by the name dev.css asks for the
// fonts by, and a page scene's selectors and classes are the pages' own.
func TestFeatureDemoPagesFromTheirStylesheets(t *testing.T) {
	root := filepath.Join("..", "..")
	read := func(name string) string {
		data, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	workflow, story, devCSS := read(".github/workflows/pages.yml"), read("site/assets/story.js"), read("helper/dev/dev.css")
	for _, copy := range []string{
		"cp helper/dev/dev.css helper/admin/admin.css site/assets/pages/",
		"cp third_party/geist/Geist-Variable.woff2 site/assets/pages/geist.woff2",
		"cp third_party/geist/GeistMono-Variable.woff2 site/assets/pages/geist-mono.woff2",
		`- "helper/dev/dev.css"`, `- "helper/admin/admin.css"`,
	} {
		if !strings.Contains(workflow, copy) {
			t.Errorf("the site's build doesn't %s", copy)
		}
	}
	for _, font := range []string{`url("geist.woff2?`, `url("geist-mono.woff2?`} {
		if !strings.Contains(devCSS, font) {
			t.Errorf("dev.css doesn't ask for %s", font)
		}
	}
	if !strings.Contains(story, `PAGES + 'dev.css`) || !strings.Contains(story, `PAGES + 'admin.css`) {
		t.Error("story.js doesn't draw the pages with their own stylesheets")
	}
	demo := read("site/assets/demos/app.js")
	for _, class := range []string{"code-bar", "admin-cards", "admin-card", "chat-entries", "msg-user", "composer-bar", "composer-send", "fold tools", "chat-activity"} {
		if !strings.Contains(devCSS+read("helper/admin/admin.css"), "."+strings.Fields(class)[len(strings.Fields(class))-1]) || !strings.Contains(demo, class) {
			t.Errorf("app.js's page scenes and the pages' stylesheets don't share %s", class)
		}
	}
}
