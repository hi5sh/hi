package main

import (
	"encoding/base64"
	"encoding/json"
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

// teamWebPictures are a message's pictures for the lead's model, as data
// URLs: the pictures of its references first, then what was attached.
func teamWebPictures(team, id string, files []teamWebFile, from string) ([]teamLeadPicture, map[string]bool) {
	var pictures []teamLeadPicture
	seen := map[string]bool{}
	for _, refs := range []bool{true, false} {
		for _, file := range files {
			if (file.Ref > 0) != refs || !teamWebPictureType(file.Type) || file.Size > teamWebMaxPicture || len(pictures) == teamWebMaxPictures {
				continue
			}
			data, err := os.ReadFile(filepath.Join(teamWebFilesDir(team, id), file.Name))
			if err != nil {
				continue
			}
			label := fmt.Sprintf("%s, which %s attached:", teamWebFileLabel(file), from)
			if file.Ref > 0 {
				label = fmt.Sprintf("#%d as it looks on their screen (a small part is shown with what is around it, itself outlined):", file.Ref)
			}
			pictures = append(pictures, teamLeadPicture{Label: label, URL: "data:" + file.Type + ";base64," + base64.StdEncoding.EncodeToString(data)})
			seen[file.Name] = true
		}
	}
	return pictures, seen
}

// ---------------------------------------------------------------------------
// new messages, as they come

// webPublish tells the panels that show a chat about a new message. The
// caller holds webMu.
func (b *teamBroker) webPublish(id string, message teamWebMessage) {
	for listener := range b.webListeners[id] {
		select {
		case listener <- message:
		default: // a panel that can't keep up loads the chat again
		}
	}
}

// webEvents is GET /__hi/api/events: the chat's messages after the
// panel's last one, then each new one as it is saved.
func (b *teamBroker) webEvents(w http.ResponseWriter, r *http.Request, slot, person string) {
	query := r.URL.Query()
	id := query.Get("conversation")
	after, _ := strconv.Atoi(query.Get("after"))
	if last, err := strconv.Atoi(r.Header.Get("Last-Event-ID")); err == nil && last > after {
		after = last
	}
	b.webMu.Lock()
	conversation, err := loadTeamWebConversation(b.team, id)
	if err != nil || conversation.Slot != slot || !conversation.Verified || conversation.Person != person {
		b.webMu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "no such chat here"})
		return
	}
	listener := make(chan teamWebMessage, 32)
	if b.webListeners[id] == nil {
		b.webListeners[id] = map[chan teamWebMessage]bool{}
	}
	b.webListeners[id][listener] = true
	b.webMu.Unlock()
	defer func() {
		b.webMu.Lock()
		delete(b.webListeners[id], listener)
		if len(b.webListeners[id]) == 0 {
			delete(b.webListeners, id)
		}
		b.webMu.Unlock()
	}()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	flush := func() {
		if flusher != nil {
			flusher.Flush()
		}
	}
	send := func(message teamWebMessage) {
		data, _ := json.Marshal(message)
		fmt.Fprintf(w, "id: %d\nevent: message\ndata: %s\n\n", message.N, data)
	}
	fmt.Fprintf(w, "retry: 5000\n\n")
	for _, message := range conversation.Messages {
		if message.N > after {
			send(message)
			after = message.N
		}
	}
	flush()
	ping := time.NewTicker(teamWebPing)
	defer ping.Stop()
	for {
		select {
		case message := <-listener:
			if message.N > after {
				send(message)
				after = message.N
				flush()
			}
		case <-ping.C:
			fmt.Fprintf(w, ": ping\n\n")
			flush()
		case <-r.Context().Done():
			return
		}
	}
}

// webFile is GET /__hi/api/files/<id>/<name>: a file of a chat in this
// slot. The app's origin serves it, so only pictures show in the page;
// everything else downloads.
func (b *teamBroker) webFile(w http.ResponseWriter, r *http.Request, slot, person string) {
	id, name := r.PathValue("id"), r.PathValue("name")
	b.webMu.Lock()
	conversation, err := loadTeamWebConversation(b.team, id)
	b.webMu.Unlock()
	var file *teamWebFile
	if err == nil && conversation.Slot == slot && conversation.Verified && conversation.Person == person {
		for _, message := range conversation.Messages {
			for i := range message.Files {
				if message.Files[i].Name == name {
					file = &message.Files[i]
				}
			}
		}
	}
	if file == nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	if teamWebPictureType(file.Type) {
		w.Header().Set("Content-Type", file.Type)
	} else {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, original, _ := strings.Cut(file.Name, "-")
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": original}))
	}
	http.ServeFile(w, r, filepath.Join(teamWebFilesDir(b.team, id), file.Name))
}

// ---------------------------------------------------------------------------
// the lead's side: hi team web

// webPost puts a message of the lead's in a chat, after its answer: work
// that is ready to try, a question that came up. The panel shows it at
// once, or with a dot on the button, and hi copies it into the chat's
// thread in Slack.
func (b *teamBroker) webPost(post teamWebPost) (string, error) {
	config, err := loadTeam(b.team)
	if err != nil {
		return "", err
	}
	text := strings.TrimSpace(post.Text)
	if text == "" && len(post.Attachments) == 0 {
		return "", errors.New("say something, or attach a file")
	}
	if len(text) > teamWebMaxText {
		return "", fmt.Errorf("the message is longer than %d characters", teamWebMaxText)
	}
	b.webMu.Lock()
	conversation, err := loadTeamWebConversation(b.team, post.Conversation)
	if err != nil {
		b.webMu.Unlock()
		return "", fmt.Errorf("%w; hi team web lists the chats", err)
	}
	fail := func(err error) (string, error) {
		b.webMu.Unlock()
		return "", err
	}
	switch {
	case conversation.Ended:
		if open, ok := b.openConversation(conversation.Slot, conversation.Person); ok && conversation.Person != "" {
			return fail(fmt.Errorf("%s started a new chat since, %s: post there", conversation.Name, open.ID))
		}
		return fail(fmt.Errorf("%s ended %s and has no chat open; tell them in Slack", conversation.Name, conversation.ID))
	case b.answering[conversation.ID]:
		return fail(fmt.Errorf("%s is waiting for your answer in %s right now: say it in that answer, or post when it's done", conversation.Name, conversation.ID))
	case !teamHelperOn(config, conversation.Slot):
		return fail(fmt.Errorf("the helper is off on %s; tell them in Slack", conversation.Slot))
	}
	n := len(conversation.Messages) + 1
	var uploads []teamWebUpload
	for _, attachment := range post.Attachments {
		uploads = append(uploads, teamWebUpload{Name: attachment.Name, Data: attachment.Data})
	}
	files, err := teamSaveWebFiles(b.team, conversation.ID, n, uploads, 0)
	if err != nil {
		return fail(err)
	}
	message := teamWebMessage{N: n, From: "lead", Name: teamBotName(b.team) + " team lead", Text: text, Files: files, Posted: true, At: time.Now().UTC()}
	conversation.Messages = append(conversation.Messages, message)
	if err := saveTeamWebConversation(b.team, conversation); err != nil {
		return fail(err)
	}
	b.webPublish(conversation.ID, message)
	watching := len(b.webListeners[conversation.ID]) > 0
	b.webMu.Unlock()
	b.mirrorWeb(config, conversation.ID, n)
	told := "The app shows it at once, since they have it open"
	if !watching {
		told = "They don't have the app open, so it waits there with a dot on the button"
		if conversation.Verified && b.channel != nil {
			// Someone signed in with /hi hears about it in their DM.
			dm := fmt.Sprintf(":speech_balloon: The %s team lead wrote in your chat in the app (%s):\n%s\nOpen the app from your links above to answer.",
				teamBotName(b.team), conversation.Slot, teamSlackQuote(teamClipStart(text, 1500)))
			if _, _, err := b.channel.dm(conversation.Person, dm); err == nil {
				told += ", and hi told them in their DM"
			}
		}
	}
	copied := "the channel"
	if conversation.DM != "" {
		copied = "their DM"
	}
	return fmt.Sprintf("Posted %s.%d to %s on %s. %s; it's in the chat's thread in %s too.",
		conversation.ID, n, conversation.Name, conversation.Slot, told, copied), nil
}

// teamWebList is hi team web: the chats of the last week, newest first.
func teamWebList(team string, now time.Time) string {
	chats := loadTeamWebConversations(team)
	var lines []string
	for i := len(chats) - 1; i >= 0 && len(lines) < 20; i-- {
		chat := chats[i]
		if len(chat.Messages) == 0 {
			continue
		}
		last := chat.Messages[len(chat.Messages)-1]
		if now.Sub(last.At) > teamApprovalMaxAge {
			continue
		}
		who := chat.Name
		if chat.Person != "" {
			who += " (" + chat.Person + ")"
		}
		line := fmt.Sprintf("%s · %s · %s · %d messages · last %s ago, from %s: %s", chat.ID, who, chat.Slot, len(chat.Messages),
			teamWebAgo(now.Sub(last.At)), last.Name, strconv.Quote(teamClipStart(strings.Join(strings.Fields(last.Text), " "), 80)))
		if chat.Ended {
			line += " · ended"
		}
		lines = append(lines, line)
	}
	var feedback []string
	all := loadTeamFeedbacks(team)
	for i := len(all) - 1; i >= 0 && len(feedback) < 20; i-- {
		item := all[i]
		if now.Sub(item.At) > teamApprovalMaxAge {
			continue
		}
		who := "no name"
		if item.Name != "" {
			who = strconv.Quote(item.Name) + " (not checked)"
		}
		feedback = append(feedback, fmt.Sprintf("%s · feedback · %s · %s · %s ago: %s", item.ID, who, item.Slot, teamWebAgo(now.Sub(item.At)),
			strconv.Quote(teamClipStart(strings.Join(strings.Fields(item.Text), " "), 80))))
	}
	if len(lines) == 0 && len(feedback) == 0 {
		return "No chats or feedback in the app in the last week."
	}
	if len(feedback) > 0 {
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, feedback...)
	}
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

// teamWebShow is hi team web show <id>: a whole chat, with each message's
// id, the parts of the page pointed at, and the files.
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
