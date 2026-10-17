package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The guide shows how each tool's parts fit together with a structure
// diagram (_includes/diagram.html, docs/guide-diagrams.md). These tests
// keep the diagrams well formed and make one part of every tool's page.

// guideDiagramPages are the pages that must open with a diagram, and which.
// A new tool's guide page goes here with its diagram.
var guideDiagramPages = map[string]string{
	"guide/box/index.md":          "box",
	"guide/agent/index.md":        "agent",
	"guide/team/index.md":         "team",
	"guide/compute/managed.md":    "managed",
	"guide/workstation/expose.md": "expose",
}

type guideDiagramItem struct {
	Title string `json:"title"`
	Role  string `json:"role"`
	Does  string `json:"does"`
	Chip  string `json:"chip"`
	Tone  string `json:"tone"`
	Mark  int    `json:"mark"`
	Entry string `json:"entry"`
}

type guideDiagramGroup struct {
	Name    string             `json:"name"`
	Label   string             `json:"label"`
	Mark    int                `json:"mark"`
	Summary string             `json:"summary"`
	Via     string             `json:"via"`
	Items   []guideDiagramItem `json:"items"`
}

type guideDiagram struct {
	Caption   string              `json:"caption"`
	You       string              `json:"you"`
	Via       string              `json:"via"`
	Frame     string              `json:"frame"`
	FrameMark int                 `json:"frame_mark"`
	Groups    []guideDiagramGroup `json:"groups"`
	After     []guideDiagramGroup `json:"after"`
	Key       []struct {
		Mark int    `json:"mark"`
		Term string `json:"term"`
		Text string `json:"text"`
	} `json:"key"`
}

var (
	guideDiagramInclude = regexp.MustCompile(`\{%\s*include\s+diagram\.html\s+name="([a-z0-9-]+)"\s*%\}`)
	guideCodeSpan       = regexp.MustCompile("`[^`]*`")
	guideDiagramTones   = map[string]bool{"": true, "accent": true, "mint": true, "amber": true, "red": true, "sky": true, "violet": true, "dim": true}
)

func loadGuideDiagrams(t *testing.T) map[string]guideDiagram {
	t.Helper()
	files, err := filepath.Glob("_data/diagrams/*.json")
	if err != nil || len(files) == 0 {
		t.Fatal("no diagrams in _data/diagrams", err)
	}
	diagrams := map[string]guideDiagram{}
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		var diagram guideDiagram
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&diagram); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		diagrams[strings.TrimSuffix(filepath.Base(file), ".json")] = diagram
	}
	return diagrams
}

func TestGuideDiagramsAreWellFormed(t *testing.T) {
	for name, d := range loadGuideDiagrams(t) {
		problem := func(format string, args ...any) {
			t.Errorf(name+": "+format, args...)
		}
		// The caption is the diagram for screen readers and search: whole
		// sentences, not a title.
		if len(d.Caption) < 120 || !strings.HasSuffix(d.Caption, ".") {
			problem("the caption should say in sentences what the diagram shows")
		}
		if len(d.Groups) == 0 || len(d.Groups) > 4 {
			problem("%d groups in the frame; 1 to 4 fit side by side", len(d.Groups))
		}
		used := map[int]int{}
		if d.FrameMark != 0 {
			used[d.FrameMark]++
		}
		// Markdown fields: an angle bracket outside a code span is HTML.
		markdown := func(where, text string) {
			if strings.ContainsAny(guideCodeSpan.ReplaceAllString(text, ""), "<>") {
				problem("%s: put <placeholders> in backticks: %q", where, text)
			}
		}
		for _, group := range append(append([]guideDiagramGroup{}, d.Groups...), d.After...) {
			if group.Name == "" || len(group.Items) == 0 {
				problem("a group needs a name and at least one item")
			}
			if group.Mark != 0 {
				used[group.Mark]++
			}
			markdown(group.Name+" summary", group.Summary)
			for _, item := range group.Items {
				if item.Title == "" || item.Role == "" || item.Does == "" {
					problem("%s: an item needs a title, a role, and what it does", group.Name)
				}
				if !guideDiagramTones[item.Tone] {
					problem("%s: tone %q isn't one of the diagram's colours", item.Title, item.Tone)
				}
				if item.Mark != 0 {
					used[item.Mark]++
				}
				markdown(item.Title, item.Does)
			}
		}
		// Every mark is explained in the key, once, and numbered 1, 2, 3...
		keyed := map[int]bool{}
		for _, entry := range d.Key {
			if entry.Term == "" || entry.Text == "" || keyed[entry.Mark] {
				problem("key %d: a term and a text, once each", entry.Mark)
			}
			keyed[entry.Mark] = true
			markdown(entry.Term, entry.Text)
		}
		var marks []int
		for mark, count := range used {
			if count > 1 {
				problem("mark %d is used %d times", mark, count)
			}
			if !keyed[mark] {
				problem("mark %d isn't in the key", mark)
			}
			marks = append(marks, mark)
		}
		sort.Ints(marks)
		for i, mark := range marks {
			if mark != i+1 {
				problem("marks are %v; number them 1, 2, 3...", marks)
				break
			}
		}
		if len(keyed) != len(used) {
			problem("the key explains %d marks, the diagram has %d", len(keyed), len(used))
		}
	}
}

func TestGuidePagesHaveTheirDiagrams(t *testing.T) {
	diagrams := loadGuideDiagrams(t)
	usedBy := map[string]string{}
	err := filepath.Walk("guide", func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".md") {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, match := range guideDiagramInclude.FindAllStringSubmatch(string(data), -1) {
			if _, ok := diagrams[match[1]]; !ok {
				t.Errorf("%s: no diagram %q in _data/diagrams", path, match[1])
			}
			usedBy[match[1]] = path
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for page, name := range guideDiagramPages {
		if usedBy[name] != page {
			t.Errorf("%s should show the %s diagram: {%% include diagram.html name=%q %%}", page, name, name)
		}
	}
	for name := range diagrams {
		if usedBy[name] == "" {
			t.Errorf("the %s diagram isn't on any page", name)
		}
	}
}
