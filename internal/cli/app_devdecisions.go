package cli

import (
	"cmp"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// The dev page's Decisions tab: the project's docs/decisions/, one file
// per decision, as cards to read at a glance and open to edit or delete.
// As with the specs, a change is committed to the team's branch with no
// review: documentation, not code. The agents read them from there.

const appDecisionsDir = "docs/decisions"

// appDecision is one decision, as the team's branch has it.
type appDecision struct {
	File    string    `json:"file"`             // 0003-sqlite.md
	Number  string    `json:"number,omitempty"` // 0003
	Title   string    `json:"title"`
	Summary string    `json:"summary"` // its first paragraph
	Text    string    `json:"text"`
	Added   time.Time `json:"added,omitzero"`
	Changed time.Time `json:"changed,omitzero"`
	// Blob is the file's git blob: a save made over a change since the
	// page read it is refused.
	Blob string `json:"blob"`
}

var (
	appDecisionFile   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,99}\.md$`)
	appDecisionNumber = regexp.MustCompile(`^(\d+)-`)
)

// decisions lists the decisions on the team's branch, the newest number first.
func (b *appBroker) decisions() ([]appDecision, error) {
	config, err := loadTeam(b.team)
	if err != nil {
		return nil, err
	}
	branch := config.Branch
	decisions := []appDecision{}
	for _, line := range strings.Split(boxGit(b.repo, "ls-tree", branch, appDecisionsDir+"/"), "\n") {
		// 100644 blob <sha>\tdocs/decisions/0001-x.md
		meta, file, ok := strings.Cut(line, "\t")
		fields := strings.Fields(meta)
		name := strings.TrimPrefix(file, appDecisionsDir+"/")
		if !ok || len(fields) != 3 || fields[1] != "blob" || name == "README.md" || !appDecisionFile.MatchString(name) {
			continue
		}
		text := boxGit(b.repo, "cat-file", "blob", fields[2])
		decision := appDecision{File: name, Title: appDocsTitle([]byte(text), name), Summary: appDecisionSummary(text), Text: text, Blob: fields[2]}
		if match := appDecisionNumber.FindStringSubmatch(name); match != nil {
			decision.Number = match[1]
		}
		decisions = append(decisions, decision)
	}
	// When each was written and last changed, in one walk of the log.
	var at time.Time
	seen := map[string]int{}
	for i := range decisions {
		seen[appDecisionsDir+"/"+decisions[i].File] = i
	}
	for _, line := range strings.Split(boxGit(b.repo, "log", "--format=@%aI", "--name-only", branch, "--", appDecisionsDir), "\n") {
		if stamp, ok := strings.CutPrefix(line, "@"); ok {
			at, _ = time.Parse(time.RFC3339, stamp)
		} else if i, ok := seen[line]; ok {
			if decisions[i].Changed.IsZero() {
				decisions[i].Changed = at
			}
			decisions[i].Added = at
		}
	}
	// Newest first: the highest number, then the latest written.
	slices.SortStableFunc(decisions, func(a, b appDecision) int {
		an, _ := strconv.Atoi(a.Number)
		bn, _ := strconv.Atoi(b.Number)
		return cmp.Or(bn-an, b.Added.Compare(a.Added))
	})
	return decisions, nil
}

// appDecisionSummary is the first paragraph after the title, as plain text.
func appDecisionSummary(text string) string {
	var lines []string
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "#"), strings.HasPrefix(line, "```"), strings.HasPrefix(line, "|"), strings.HasPrefix(line, "---"):
			if len(lines) > 0 {
				return appDecisionPlain(lines)
			}
		case line == "":
			if len(lines) > 0 {
				return appDecisionPlain(lines)
			}
		default:
			lines = append(lines, strings.TrimLeft(line, "-*> "))
		}
	}
	return appDecisionPlain(lines)
}

func appDecisionPlain(lines []string) string {
	text := strings.NewReplacer("**", "", "__", "", "`", "").Replace(strings.Join(lines, " "))
	if runes := []rune(text); len(runes) > 400 {
		text = strings.TrimSpace(string(runes[:400])) + "…"
	}
	return text
}

// appDecisionSlug makes a file name's words from a title.
func appDecisionSlug(title string) string {
	var out strings.Builder
	dash := false
	for _, r := range strings.ToLower(title) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			if dash && out.Len() > 0 {
				out.WriteByte('-')
			}
			out.WriteRune(r)
			dash = false
		} else {
			dash = true
		}
		if out.Len() >= 50 {
			break
		}
	}
	return cmp.Or(strings.Trim(out.String(), "-"), "decision")
}

// saveDecision writes a decision and commits it to the team's branch: a
// new one when file is empty, numbered after the last. blob is the file
// as the page read it; text "" deletes it.
func (b *appBroker) saveDecision(file, blob, text, who string) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	config, err := loadTeam(b.team)
	if err != nil {
		return "", err
	}
	if current := boxGit(b.repo, "branch", "--show-current"); current != config.Branch {
		return "", fmt.Errorf("repo/ is on %s, not %s", current, config.Branch)
	}
	text = strings.TrimSpace(strings.ReplaceAll(text, "\r\n", "\n"))
	creating := file == ""
	if creating {
		if text == "" {
			return "", errors.New("a decision needs text")
		}
		if !appDocsHeading.MatchString(text) {
			return "", errors.New("start the decision with its title, as a heading: # Use SQLite")
		}
		last := 0
		entries, _ := os.ReadDir(filepath.Join(b.repo, filepath.FromSlash(appDecisionsDir)))
		for _, entry := range entries {
			if match := appDecisionNumber.FindStringSubmatch(entry.Name()); match != nil {
				n, _ := strconv.Atoi(match[1])
				last = max(last, n)
			}
		}
		file = fmt.Sprintf("%04d-%s.md", last+1, appDecisionSlug(appDocsTitle([]byte(text), "")))
	} else if !appDecisionFile.MatchString(file) || file == "README.md" {
		return "", fmt.Errorf("no decision %q", file)
	}
	rel := appDecisionsDir + "/" + file
	now := boxGit(b.repo, "rev-parse", "--verify", "-q", config.Branch+":"+rel)
	switch {
	case creating && now != "":
		return "", fmt.Errorf("%s is there already", rel)
	case !creating && now == "":
		return "", fmt.Errorf("%s isn't there anymore", rel)
	case !creating && now != blob:
		return "", errors.New("someone changed the decision since you opened it: open it again, and make your change on theirs")
	}
	path := filepath.Join(b.repo, filepath.FromSlash(rel))
	var message string
	if text == "" {
		if out, err := appGit(b.repo, "rm", "-q", "-f", "--", rel); err != nil {
			return "", fmt.Errorf("git rm: %s", out)
		}
		message = "Delete the decision " + file
	} else {
		os.MkdirAll(filepath.Dir(path), 0o755)
		if err := os.WriteFile(path, []byte(text+"\n"), 0o644); err != nil {
			return "", err
		}
		if boxGit(b.repo, "status", "--porcelain", "--", rel) == "" {
			return file, nil // as it was
		}
		if out, err := appGit(b.repo, "add", "--", rel); err != nil {
			return "", fmt.Errorf("git add: %s", out)
		}
		message = map[bool]string{true: "Add the decision ", false: "Edit the decision "}[creating] + file
	}
	if out, err := appGit(b.repo, "commit", "-q", "-m", message+"\n\n"+who+" on the dev page.", "--", rel); err != nil {
		return "", fmt.Errorf("git commit: %s", out)
	}
	if config.Remote != "" {
		if out, err := appGit(b.repo, "push", "-q", "origin", config.Branch); err != nil {
			return file, fmt.Errorf("committed, but git push failed: %s", qFirstLine(out, err.Error()))
		}
	}
	return file, nil
}

func (b *appBroker) devDecisionRoutes(mux *http.ServeMux, builder func(http.ResponseWriter, *http.Request) (appPersonKey, bool),
	reply func(http.ResponseWriter, int, any), failure func(http.ResponseWriter, int, error), decode func(http.ResponseWriter, *http.Request, any) bool) {
	mux.HandleFunc("GET "+appDevPagePrefix+"api/decisions", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := builder(w, r); !ok {
			return
		}
		decisions, err := b.decisions()
		if err != nil {
			failure(w, http.StatusInternalServerError, err)
			return
		}
		reply(w, http.StatusOK, decisions)
	})
	// Saving one, new or edited, or deleting it: text "" with delete.
	mux.HandleFunc("POST "+appDevPagePrefix+"api/decision", func(w http.ResponseWriter, r *http.Request) {
		person, ok := builder(w, r)
		if !ok {
			return
		}
		var request struct {
			File   string `json:"file,omitempty"`
			Blob   string `json:"blob,omitempty"`
			Text   string `json:"text"`
			Delete bool   `json:"delete,omitempty"`
		}
		if !decode(w, r, &request) {
			return
		}
		switch {
		case request.Delete:
			request.Text = ""
		case strings.TrimSpace(request.Text) == "":
			failure(w, http.StatusBadRequest, errors.New("a decision needs text; delete it instead"))
			return
		}
		if request.Delete && request.File == "" {
			failure(w, http.StatusBadRequest, errors.New("which decision?"))
			return
		}
		file, err := b.saveDecision(request.File, request.Blob, request.Text, person.Name)
		if err != nil {
			failure(w, http.StatusBadRequest, err)
			return
		}
		reply(w, http.StatusOK, map[string]string{"file": file})
	})
}
