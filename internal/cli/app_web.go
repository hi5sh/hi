package cli

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
// lead says in a chat after its answer, with hi app web post. The panel
// hears new messages over GET /__hi/api/events
// (docs/specs/approved/hi_app_helper.md, slice 3).

const (
	// appWebMaxFiles is how many files a person attaches to one message;
	// the pictures of its references come on top.
	appWebMaxFiles  = 6
	appWebMaxFile   = 20 << 20
	appWebMaxUpload = 32 << 20 // a message's files and pictures together
	// appWebMaxPicture is the largest picture the lead's model looks at
	// itself, and appWebMaxPictures how many per message; the rest it
	// opens from the folder when it needs them.
	appWebMaxPicture  = 4 << 20
	appWebMaxPictures = 8
	// appWebInBox is web/ in the lead's box.
	appWebInBox = "/team/web"
	// appWebPing keeps an event stream open through the proxies on the way.
	appWebPing = 25 * time.Second
)

// appWebFile is a file on a message, in web/<id>-files.
type appWebFile struct {
	Name string `json:"name"` // its name in the folder: <message>-<name>
	Type string `json:"type"`
	Size int    `json:"size"`
	Ref  int    `json:"ref,omitempty"` // the picture of reference #n
	// Screen is a picture of what the person saw, sent with feedback.
	Screen bool `json:"screen,omitempty"`
}

// appWebUpload is a file sent with a message.
type appWebUpload struct {
	Name   string `json:"name"`
	Ref    int    `json:"ref,omitempty"`
	Screen bool   `json:"screen,omitempty"`
	Data   []byte `json:"data"`
}

// appWebPost is a message the lead posts in a chat: hi app web post.
type appWebPost struct {
	Conversation string          `json:"conversation"`
	Text         string          `json:"text"`
	Attachments  []appAttachment `json:"attachments,omitempty"`
}

// appLeadPicture is a picture the lead's model sees with a message.
type appLeadPicture struct {
	Label string // what it is, said just before it
	URL   string // a data: URL
}

func appWebFilesDir(team, id string) string { return filepath.Join(appWebStore(team), id+"-files") }

// appWebPictureType says whether a file is a picture the lead's model and
// the panel show as one. Anything else, SVG included, is only a download.
func appWebPictureType(kind string) bool {
	switch kind {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		return true
	}
	return false
}

// appWebFileType is a file's type: what its bytes are when they are a
// picture, otherwise what its name says.
func appWebFileType(name string, data []byte) string {
	sniffed := http.DetectContentType(data)
	if appWebPictureType(sniffed) {
		return sniffed
	}
	if kind, _, err := mime.ParseMediaType(mime.TypeByExtension(strings.ToLower(filepath.Ext(name)))); err == nil && kind != "" {
		return kind
	}
	kind, _, _ := mime.ParseMediaType(sniffed)
	return firstNonEmpty(kind, "application/octet-stream")
}

// appSaveWebFiles checks and writes the files of message n; refs is how
// many references it has.
func appSaveWebFiles(team, id string, n int, uploads []appWebUpload, refs int) ([]appWebFile, error) {
	attached, total := 0, 0
	for _, upload := range uploads {
		total += len(upload.Data)
		if upload.Ref == 0 && !upload.Screen {
			attached++
		}
		switch {
		case len(upload.Data) == 0:
			return nil, fmt.Errorf("%s is empty", firstNonEmpty(upload.Name, "a file"))
		case len(upload.Data) > appWebMaxFile:
			return nil, fmt.Errorf("%s is larger than %d MB", upload.Name, appWebMaxFile>>20)
		case upload.Ref < 0 || upload.Ref > refs:
			return nil, fmt.Errorf("there is no #%d for a picture of", upload.Ref)
		case upload.Ref > 0 && !appWebPictureType(http.DetectContentType(upload.Data)):
			return nil, fmt.Errorf("the picture of #%d isn't a picture", upload.Ref)
		case upload.Screen && !appWebPictureType(http.DetectContentType(upload.Data)):
			return nil, errors.New("the picture of the page isn't a picture")
		}
	}
	if attached > appWebMaxFiles {
		return nil, fmt.Errorf("attach at most %d files to a message", appWebMaxFiles)
	}
	if total > appWebMaxUpload {
		return nil, fmt.Errorf("the files are larger than %d MB together", appWebMaxUpload>>20)
	}
	if len(uploads) == 0 {
		return nil, nil
	}
	dir := appWebFilesDir(team, id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	var files []appWebFile
	for _, upload := range uploads {
		kind := appWebFileType(upload.Name, upload.Data)
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
		name = appUniqueFileName(dir, strconv.Itoa(n)+"-"+appSafeFileName(name))
		if err := os.WriteFile(filepath.Join(dir, name), upload.Data, 0o600); err != nil {
			return nil, err
		}
		files = append(files, appWebFile{Name: name, Type: kind, Size: len(upload.Data), Ref: upload.Ref, Screen: upload.Screen})
	}
	return files, nil
}

// appWebFileLabel says what a file is, in a few words.
func appWebFileLabel(file appWebFile) string {
	if file.Screen {
		return "a picture of the page as they saw it"
	}
	if file.Ref > 0 {
		return fmt.Sprintf("a picture of #%d", file.Ref)
	}
	_, name, _ := strings.Cut(file.Name, "-")
	return fmt.Sprintf("%s, %s, %s", name, file.Type, appWebSize(file.Size))
}

func appWebSize(size int) string {
	switch {
	case size >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(size)/(1<<20))
	case size >= 1<<10:
		return fmt.Sprintf("%d KB", size>>10)
	}
	return fmt.Sprintf("%d bytes", size)
}

// appWebFilesText lists a message's files where the lead finds them.
// seen marks the pictures its model was shown.
func appWebFilesText(id string, files []appWebFile, seen map[string]bool, indent string) string {
	if len(files) == 0 {
		return ""
	}
	var text strings.Builder
	fmt.Fprintf(&text, "%sFiles with this message (read-only; attach one to a task with --attach <path>):\n", indent)
	for _, file := range files {
		fmt.Fprintf(&text, "%s- %s/%s-files/%s: %s", indent, appWebInBox, id, file.Name, appWebFileLabel(file))
		if seen[file.Name] {
			text.WriteString(" (you see it below)")
		}
		text.WriteString("\n")
	}
	return text.String()
}

// ---------------------------------------------------------------------------
// the lead's side: hi app web

// appWebList is hi app web: the feedback of the last week, newest first.
func appWebList(team string, now time.Time) string {
	var lines []string
	var feedback []string
	all := loadTeamFeedbacks(team)
	for i := len(all) - 1; i >= 0 && len(feedback) < 20; i-- {
		item := all[i]
		if now.Sub(item.At) > appWeek {
			continue
		}
		who := "no name"
		if item.Name != "" {
			who = strconv.Quote(item.Name) + " (not checked)"
		}
		feedback = append(feedback, fmt.Sprintf("%s · feedback · %s · %s · %s ago: %s", item.ID, who, item.Slot, appWebAgo(now.Sub(item.At)),
			strconv.Quote(appClipStart(strings.Join(strings.Fields(item.Text), " "), 80))))
	}
	if len(feedback) == 0 {
		return "No feedback in the app in the last week."
	}
	lines = append(lines, feedback...)
	return strings.Join(lines, "\n")
}

func appWebAgo(d time.Duration) string {
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

// appWebShow is hi app web show <id>: a whole chat from the app, from
// before hi 2.4 took the chat out of the helper.
func appWebShow(team, id string) (string, error) {
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
			text.WriteString(", posted with hi app web post")
		}
		fmt.Fprintf(&text, ":\n%s\n", strings.TrimSpace(message.Text))
		if refs := appWebRefsText(message.Refs, "  "); refs != "" {
			text.WriteString(refs)
		}
		text.WriteString(appWebFilesText(chat.ID, message.Files, nil, "  "))
	}
	return text.String(), nil
}
