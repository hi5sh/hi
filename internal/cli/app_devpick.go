package cli

// Pointing at the preview's parts from the dev page (hi_app_helper.md, "In
// the dev page's Preview"): the page in the Preview's frame says what was
// picked, and hi looks for where it is written in the workspace, so the
// message the agent gets names the files as well as the page.

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path"
	"sort"
	"strings"
	"unicode/utf8"
)

// appDevPickClue is something an element has that its code is likely to
// have too: its id, a test id, its words, a label, a class.
type appDevPickClue struct {
	Kind string `json:"kind"`
	Text string `json:"text"`
}

// appDevPickHit is a file the element is likely written in: its best line,
// and the clues found in it.
type appDevPickHit struct {
	Path  string   `json:"path"`
	Line  int      `json:"line"`
	Text  string   `json:"text"`
	Clues []string `json:"clues"`
	score int
}

const (
	appDevPickClues = 10 // clues one look takes
	appDevPickHits  = 5  // files it names
)

// appDevPickWeight is how much finding a clue of a kind says: an id or a
// test id is the element's own, its words and labels nearly so, a class
// is shared by many.
var appDevPickWeight = map[string]int{"id": 6, "testid": 6, "text": 4, "label": 4, "name": 3, "href": 2, "class": 1}

// appDevPickSkip is what isn't where an element is written: what a build
// makes, lock files, pictures.
var appDevPickSkip = []string{":(exclude)*.min.js", ":(exclude)*.map", ":(exclude)*.lock", ":(exclude)*lock.json",
	":(exclude)*lock.yaml", ":(exclude)*.svg", ":(exclude)dist/", ":(exclude)build/", ":(exclude).next/"}

// appDevPickCluesOK keeps the clues worth looking for: known kinds, on one
// line, long enough to mean something, each once.
func appDevPickCluesOK(clues []appDevPickClue) []appDevPickClue {
	seen := map[string]bool{}
	var kept []appDevPickClue
	for _, clue := range clues {
		text := strings.TrimSpace(clue.Text)
		key := strings.ToLower(text)
		if appDevPickWeight[clue.Kind] == 0 || utf8.RuneCountInString(text) < 3 || len(text) > 100 || strings.ContainsAny(text, "\n\r\x00") || seen[key] {
			continue
		}
		seen[key] = true
		kept = append(kept, appDevPickClue{Kind: clue.Kind, Text: text})
		if len(kept) == appDevPickClues {
			break
		}
	}
	return kept
}

// appDevPickRank ranks the files git grep found: each clue found in a file
// counts once, by its kind; tests, stories, and stylesheets count half,
// since the agent changes the element where it is made.
func appDevPickRank(matches []appDevSearchMatch, clues []appDevPickClue) []appDevPickHit {
	files := map[string]*appDevPickHit{}
	found := map[string]map[string]bool{}
	var order []string
	for _, match := range matches {
		line := strings.ToLower(match.Text)
		weight := 0
		var on []string
		for _, clue := range clues {
			if strings.Contains(line, strings.ToLower(clue.Text)) {
				weight += appDevPickWeight[clue.Kind]
				on = append(on, clue.Kind+" "+clue.Text)
			}
		}
		if weight == 0 {
			continue // only around the match: a cut line
		}
		hit := files[match.Path]
		if hit == nil {
			hit = &appDevPickHit{Path: match.Path}
			files[match.Path] = hit
			found[match.Path] = map[string]bool{}
			order = append(order, match.Path)
		}
		if weight > hit.score || hit.Line == 0 {
			hit.score, hit.Line, hit.Text = weight, match.Line, strings.TrimSpace(match.Text)
		}
		for _, clue := range on {
			found[match.Path][clue] = true
		}
	}
	var hits []appDevPickHit
	for _, name := range order {
		hit := files[name]
		hit.score = 0
		for _, clue := range clues {
			if found[name][clue.Kind+" "+clue.Text] {
				hit.score += appDevPickWeight[clue.Kind]
				hit.Clues = append(hit.Clues, clue.Kind)
			}
		}
		if appDevPickAside(name) {
			hit.score = (hit.score + 1) / 2
		}
		hits = append(hits, *hit)
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].score > hits[j].score })
	if len(hits) > appDevPickHits {
		hits = hits[:appDevPickHits]
	}
	return hits
}

// appDevPickAside is a file beside the element's own code: a test, a
// story, a stylesheet, docs.
func appDevPickAside(name string) bool {
	lower := strings.ToLower(name)
	switch path.Ext(lower) {
	case ".css", ".scss", ".sass", ".less", ".md", ".mdx":
		return true
	}
	for _, part := range []string{"test", "spec", "stories", "e2e", "__mocks__", "fixtures"} {
		if strings.Contains(lower, part) {
			return true
		}
	}
	return false
}

// appDevPickLocate looks for the clues in the workspace's files, as Search
// does: tracked or new, not what git ignores.
func appDevPickLocate(team, workspace string, clues []appDevPickClue) ([]appDevPickHit, error) {
	clues = appDevPickCluesOK(clues)
	if len(clues) == 0 {
		return []appDevPickHit{}, nil
	}
	dir, err := appDevGitDir(team, workspace)
	if err != nil {
		return nil, err
	}
	args := []string{"sh", "-c", appDevSearchScript, "sh", dir, "-F", "-i"}
	for _, clue := range clues {
		args = append(args, "-e", clue.Text)
	}
	args = append(append(args, "--", "."), appDevPickSkip...)
	out, err := appDevBoxExec(team, args...)
	if err != nil {
		return nil, err
	}
	if text := strings.TrimSpace(out); text != "" && !strings.Contains(out, "\x00") {
		return nil, fmt.Errorf("git grep: %s", qFirstLine(text, "it failed"))
	}
	matches, _ := appDevSearchParse(out)
	hits := appDevPickRank(matches, clues)
	if hits == nil {
		hits = []appDevPickHit{}
	}
	return hits, nil
}

func (b *appBroker) devPickRoutes(mux *http.ServeMux, builder func(http.ResponseWriter, *http.Request) (appPersonKey, bool),
	reply func(http.ResponseWriter, int, any), failure func(http.ResponseWriter, int, error)) {
	mux.HandleFunc("POST "+appDevPagePrefix+"api/pick/locate", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := builder(w, r); !ok {
			return
		}
		var request struct {
			Workspace string           `json:"workspace"`
			Clues     []appDevPickClue `json:"clues"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&request); err != nil {
			failure(w, http.StatusBadRequest, err)
			return
		}
		if request.Workspace == "" {
			failure(w, http.StatusBadRequest, errors.New("which workspace?"))
			return
		}
		hits, err := appDevPickLocate(b.team, request.Workspace, request.Clues)
		if err != nil {
			failure(w, http.StatusBadRequest, err)
			return
		}
		reply(w, http.StatusOK, map[string]any{"hits": hits})
	})
}
