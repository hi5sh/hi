package cli

// The agents' notes on their workspace (docs/specs/approved/hi_app_dev_orca.md,
// release 7): hi dev note "<a line>" in the dev box is the agent's one line
// about where its work stands ("tests pass; waiting for the API key"), on
// its workspace on the dev page and in Needs you, until the next. Kept by
// the work's branch (or the workspace's label), which outlives herdr's
// numbering of its workspaces, in run/dev-notes.json.

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// appDevNoteMax is a note's length, in characters: one line.
const appDevNoteMax = 200

// appDevNote is a workspace's note.
type appDevNote struct {
	Text string    `json:"text"`
	At   time.Time `json:"at"`
}

var appDevNotesMu sync.Mutex

func appDevNotesPath(team string) string { return filepath.Join(appDir(team), "run", "dev-notes.json") }

// appDevNotes is every workspace's note, by its branch or label.
func appDevNotes(team string) map[string]appDevNote {
	notes := map[string]appDevNote{}
	if data, err := os.ReadFile(appDevNotesPath(team)); err == nil {
		json.Unmarshal(data, &notes)
	}
	return notes
}

// appDevNoteText is a note as kept: one line, trimmed, at most appDevNoteMax.
func appDevNoteText(text string) (string, error) {
	text = strings.Join(strings.Fields(text), " ")
	if !utf8.ValidString(text) {
		return "", errors.New("the note isn't text")
	}
	if utf8.RuneCountInString(text) > appDevNoteMax {
		runes := []rune(text)
		text = string(runes[:appDevNoteMax-1]) + "…"
	}
	return text, nil
}

// devNote sets, or with "" clears, the note of the workspace the agent
// runs in: by herdr's workspace, else the worktree its folder is in.
func (b *appBroker) devNote(workspace, cwd, text string) (string, error) {
	text, err := appDevNoteText(text)
	if err != nil {
		return "", err
	}
	spaces, err := appDevSpaces(b.team)
	if err != nil {
		return "", err
	}
	key := ""
	for _, space := range spaces {
		if workspace != "" && space.ID == workspace {
			key = firstNonEmpty(space.Branch, space.Label)
			break
		}
	}
	if key == "" && cwd != "" {
		for _, space := range spaces {
			if space.Path != "" && (cwd == space.Path || strings.HasPrefix(cwd, space.Path+"/")) {
				key = space.Branch
				break
			}
		}
	}
	if key == "" {
		return "", errors.New("hi dev note is for an agent in one of the dev box's workspaces: run it there, in its worktree")
	}
	appDevNotesMu.Lock()
	defer appDevNotesMu.Unlock()
	notes := appDevNotes(b.team)
	if text == "" {
		delete(notes, key)
	} else {
		notes[key] = appDevNote{Text: text, At: time.Now().UTC()}
	}
	// Notes of work that's gone go with it.
	alive := map[string]bool{}
	for _, space := range spaces {
		alive[firstNonEmpty(space.Branch, space.Label)] = true
	}
	for name := range notes {
		if !alive[name] {
			delete(notes, name)
		}
	}
	data, _ := json.Marshal(notes)
	os.MkdirAll(filepath.Dir(appDevNotesPath(b.team)), 0o700)
	if err := writeFileAtomic(appDevNotesPath(b.team), data, 0o600); err != nil {
		return "", err
	}
	return key, nil
}
