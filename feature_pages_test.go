package main

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
	"box":     printBoxUsage,
	"agent":   printAgentUsage,
	"team":    printTeamUsage,
	"compute": printComputeUsage,
}

var (
	demoCommand = regexp.MustCompile(`\{ cmd: "((?:[^"\\]|\\.)*)"`)
	demoFlag    = regexp.MustCompile(`(?:^|\s)(--[a-z][a-z-]*)`)
)

func TestFeaturePagesHaveTheirDemo(t *testing.T) {
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
	for name := range featurePageUsage {
		src, err := os.ReadFile(filepath.Join("assets", "demos", name+".js"))
		if err != nil {
			t.Fatal(err)
		}
		seen := 0
		for _, m := range demoCommand.FindAllStringSubmatch(string(src), -1) {
			words := strings.Fields(m[1])
			if len(words) < 2 || words[0] != "hi" || featurePageUsage[words[1]] == nil {
				continue
			}
			tool := words[1]
			if tool == name {
				seen++
			}
			var buf bytes.Buffer
			featurePageUsage[tool](&buf)
			help := buf.String()
			if len(words) > 2 && !strings.HasPrefix(words[2], "-") && !strings.HasPrefix(words[2], `\"`) {
				sub := words[2]
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
