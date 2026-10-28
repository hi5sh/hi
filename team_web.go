package main

import (
	"errors"
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// What the helper's chat has besides words: files on a message, both ways;
// a picture of each part of the page the person points at; and what the
// lead says in a chat after its answer, with hi team web post. The panel
// hears new messages over GET /__hi/api/events
// (docs/specs/approved/hi_team_helper.md, slice 3).

const (
	// teamWebMaxFiles is how many files a person attaches to one message;
	// the pictures of its references come on top.
	teamWebMaxFiles  = 6
	teamWebMaxFile   = 20 << 20
	teamWebMaxUpload = 32 << 20 // a message's files and pictures together
	// teamWebMaxPicture is the largest picture the lead's model looks at
	// itself, and teamWebMaxPictures how many per message; the rest it
	// opens from the folder when it needs them.
	teamWebMaxPicture  = 4 << 20
	teamWebMaxPictures = 8
	// teamWebInBox is web/ in the lead's box.
	teamWebInBox = "/team/web"
	// teamWebPing keeps an event stream open through the proxies on the way.
	teamWebPing = 25 * time.Second
)

// teamWebFile is a file on a message, in web/<id>-files.
type teamWebFile struct {
	Name string `json:"name"` // its name in the folder: <message>-<name>
	Type string `json:"type"`
	Size int    `json:"size"`
	Ref  int    `json:"ref,omitempty"` // the picture of reference #n
	// Screen is a picture of what the person saw, sent with feedback.
	Screen bool `json:"screen,omitempty"`
}

// teamWebUpload is a file sent with a message.
type teamWebUpload struct {
	Name   string `json:"name"`
	Ref    int    `json:"ref,omitempty"`
	Screen bool   `json:"screen,omitempty"`
	Data   []byte `json:"data"`
}

// teamWebPost is a message the lead posts in a chat: hi team web post.
type teamWebPost struct {
	Conversation string           `json:"conversation"`
	Text         string           `json:"text"`
	Attachments  []teamAttachment `json:"attachments,omitempty"`
}

// teamLeadPicture is a picture the lead's model sees with a message.
type teamLeadPicture struct {
	Label string // what it is, said just before it
	URL   string // a data: URL
}

func teamWebFilesDir(team, id string) string { return filepath.Join(teamWebStore(team), id+"-files") }

// teamWebPictureType says whether a file is a picture the lead's model and
// the panel show as one. Anything else, SVG included, is only a download.
func teamWebPictureType(kind string) bool {
	switch kind {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		return true
	}
	return false
}

// teamWebFileType is a file's type: what its bytes are when they are a
// picture, otherwise what its name says.
func teamWebFileType(name string, data []byte) string {
	sniffed := http.DetectContentType(data)
	if teamWebPictureType(sniffed) {
		return sniffed
	}
	if kind, _, err := mime.ParseMediaType(mime.TypeByExtension(strings.ToLower(filepath.Ext(name)))); err == nil && kind != "" {
		return kind
	}
	kind, _, _ := mime.ParseMediaType(sniffed)
	return firstNonEmpty(kind, "application/octet-stream")
}

// teamSaveWebFiles checks and writes the files of message n; refs is how
// many references it has.
func teamSaveWebFiles(team, id string, n int, uploads []teamWebUpload, refs int) ([]teamWebFile, error) {
	attached, total := 0, 0
	for _, upload := range uploads {
		total += len(upload.Data)
		if upload.Ref == 0 && !upload.Screen {
			attached++
		}
		switch {
		case len(upload.Data) == 0:
			return nil, fmt.Errorf("%s is empty", firstNonEmpty(upload.Name, "a file"))
		case len(upload.Data) > teamWebMaxFile:
			return nil, fmt.Errorf("%s is larger than %d MB", upload.Name, teamWebMaxFile>>20)
		case upload.Ref < 0 || upload.Ref > refs:
			return nil, fmt.Errorf("there is no #%d for a picture of", upload.Ref)
		case upload.Ref > 0 && !teamWebPictureType(http.DetectContentType(upload.Data)):
			return nil, fmt.Errorf("the picture of #%d isn't a picture", upload.Ref)
		case upload.Screen && !teamWebPictureType(http.DetectContentType(upload.Data)):
			return nil, errors.New("the picture of the page isn't a picture")
		}
	}
	if attached > teamWebMaxFiles {
		return nil, fmt.Errorf("attach at most %d files to a message", teamWebMaxFiles)
	}
	if total > teamWebMaxUpload {
		return nil, fmt.Errorf("the files are larger than %d MB together", teamWebMaxUpload>>20)
	}
	if len(uploads) == 0 {
		return nil, nil
	}
	dir := teamWebFilesDir(team, id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	var files []teamWebFile
	for _, upload := range uploads {
		kind := teamWebFileType(upload.Name, upload.Data)
		name := upload.Name
		if upload.Ref > 0 || upload.Screen {
			exts, _ := mime.ExtensionsByType(kind)
			name = fmt.Sprintf("ref-%d", upload.Ref)
			if upload.Screen {
				name = "screen"
			}
			if len(exts) > 0 {
				name += exts[len(exts)-1]
			}
		}
		name = teamUniqueFileName(dir, strconv.Itoa(n)+"-"+teamSafeFileName(name))
		if err := os.WriteFile(filepath.Join(dir, name), upload.Data, 0o600); err != nil {
			return nil, err
		}
		files = append(files, teamWebFile{Name: name, Type: kind, Size: len(upload.Data), Ref: upload.Ref, Screen: upload.Screen})
	}
	return files, nil
}

// teamWebFileLabel says what a file is, in a few words.
func teamWebFileLabel(file teamWebFile) string {
	if file.Screen {
		return "a picture of the page as they saw it"
	}
	if file.Ref > 0 {
		return fmt.Sprintf("a picture of #%d", file.Ref)
	}
	_, name, _ := strings.Cut(file.Name, "-")
	return fmt.Sprintf("%s, %s, %s", name, file.Type, teamWebSize(file.Size))
}

func teamWebSize(size int) string {
	switch {
	case size >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(size)/(1<<20))
	case size >= 1<<10:
		return fmt.Sprintf("%d KB", size>>10)
	}
	return fmt.Sprintf("%d bytes", size)
}

// teamWebFilesText lists a message's files where the lead finds them.
// seen marks the pictures its model was shown.
func teamWebFilesText(id string, files []teamWebFile, seen map[string]bool, indent string) string {
	if len(files) == 0 {
		return ""
	}
	var text strings.Builder
	fmt.Fprintf(&text, "%sFiles with this message (read-only; attach one to a task with --attach <path>):\n", indent)
	for _, file := range files {
		fmt.Fprintf(&text, "%s- %s/%s-files/%s: %s", indent, teamWebInBox, id, file.Name, teamWebFileLabel(file))
		if seen[file.Name] {
			text.WriteString(" (you see it below)")
		}
		text.WriteString("\n")
	}
	return text.String()
}

// ---------------------------------------------------------------------------
// the lead's side: hi team web

// teamWebList is hi team web: the feedback of the last week, newest first.
func teamWebList(team string, now time.Time) string {
	var lines []string
	var feedback []string
	all := loadTeamFeedbacks(team)
	for i := len(all) - 1; i >= 0 && len(feedback) < 20; i-- {
		item := all[i]
		if now.Sub(item.At) > teamWeek {
			continue
		}
		who := "no name"
		if item.Name != "" {
			who = strconv.Quote(item.Name) + " (not checked)"
		}
		feedback = append(feedback, fmt.Sprintf("%s · feedback · %s · %s · %s ago: %s", item.ID, who, item.Slot, teamWebAgo(now.Sub(item.At)),
			strconv.Quote(teamClipStart(strings.Join(strings.Fields(item.Text), " "), 80))))
	}
	if len(feedback) == 0 {
		return "No feedback in the app in the last week."
	}
	lines = append(lines, feedback...)
	return strings.Join(lines, "\n")
}

func teamWebAgo(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "moments"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

// teamWebShow is hi team web show <id>: a whole chat from the app, from
// before hi 2.4 took the chat out of the helper.
func teamWebShow(team, id string) (string, error) {
	chat, err := loadTeamWebConversation(team, id)
	if err != nil {
		return "", err
	}
	var text strings.Builder
	who := chat.Name
	if chat.Person != "" {
		who += " (" + chat.Person + ")"
	}
	fmt.Fprintf(&text, "%s · %s · %s · started %s", chat.ID, who, chat.Slot, chat.Started.Format("Jan 2 15:04"))
	if chat.Ended {
		text.WriteString(" · ended")
	}
	text.WriteString("\n")
	for _, message := range chat.Messages {
		fmt.Fprintf(&text, "\n[%s.%d] %s, %s", chat.ID, message.N, message.Name, message.At.Format("Jan 2 15:04"))
		if message.Page != nil && message.Page.Path != "" {
			text.WriteString(", on " + message.Page.Path)
		}
		if message.Posted {
			text.WriteString(", posted with hi team web post")
		}
		fmt.Fprintf(&text, ":\n%s\n", strings.TrimSpace(message.Text))
		if refs := teamWebRefsText(message.Refs, "  "); refs != "" {
			text.WriteString(refs)
		}
		text.WriteString(teamWebFilesText(chat.ID, message.Files, nil, "  "))
	}
	return text.String(), nil
}
