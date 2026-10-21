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
	"box": printBoxUsage,
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

// TestFeatureDemosUseRealCommands checks every hi command a demo types: its
// subcommand and its flags must be in the tool's usage.
func TestFeatureDemosUseRealCommands(t *testing.T) {
	for name, usage := range featurePageUsage {
		src, err := os.ReadFile(filepath.Join("assets", "demos", name+".js"))
		if err != nil {
			t.Fatal(err)
		}
		var buf bytes.Buffer
		usage(&buf)
		help := buf.String()
		seen := 0
		for _, m := range demoCommand.FindAllStringSubmatch(string(src), -1) {
			words := strings.Fields(m[1])
			if len(words) < 2 || words[0] != "hi" || words[1] != name {
				continue
			}
			seen++
			if len(words) > 2 && !strings.HasPrefix(words[2], "-") {
				sub := words[2]
				if !regexp.MustCompile(`hi ` + name + ` (\S+\|)*` + regexp.QuoteMeta(sub) + `[\s|]`).MatchString(help) {
					t.Errorf("%s demo: %q uses %q, which hi %s's usage doesn't list", name, m[1], sub, name)
				}
			}
			for _, f := range demoFlag.FindAllStringSubmatch(m[1], -1) {
				if !regexp.MustCompile(`(^|[^a-z-])` + f[1] + `([^a-z-]|$)`).MatchString(help) {
					t.Errorf("%s demo: %q uses %s, which hi %s's usage doesn't list", name, m[1], f[1], name)
				}
			}
		}
		if seen == 0 {
			t.Errorf("%s demo types no hi %s command", name, name)
		}
	}
}
